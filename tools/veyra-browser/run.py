#!/usr/bin/env python3
"""Portable CI v1. No imports from a cloud workspace or private evidence runner."""
import argparse
from datetime import datetime, timezone
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import subprocess
import sys
import time
from urllib.parse import urlsplit

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
IDENTITY = "veyra-portable-browser-ci-v1"


def now():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def git(*args):
    env = {"PATH": os.environ["PATH"], "HOME": os.environ.get("HOME", "/tmp"),
           "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_OPTIONAL_LOCKS": "0"}
    return subprocess.check_output(["git", "-C", str(REPO), *args], env=env)


def file_record(path):
    if path.is_symlink():
        data = os.readlink(path).encode()
        return {"kind": "symlink", "target": os.readlink(path), "sha256": hashlib.sha256(data).hexdigest()}
    stat = path.stat()
    return {"kind": "file", "bytes": stat.st_size, "mode": stat.st_mode & 0o777, "sha256": sha256(path)}


def source_snapshot():
    tracked = {}
    object_format = git("rev-parse", "--show-object-format").decode().strip()
    for entry in git("ls-tree", "-r", "-z", "HEAD").split(b"\0"):
        if not entry:
            continue
        metadata, name = entry.split(b"\t", 1)
        mode, kind, expected_blob = metadata.decode().split()
        name = os.fsdecode(name)
        if kind != "blob":
            raise RuntimeError("Unsupported tracked non-blob entry: " + name)
        path = REPO / name
        data = os.readlink(path).encode() if path.is_symlink() else path.read_bytes()
        digest = hashlib.new(object_format, b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()
        if digest != expected_blob:
            raise RuntimeError("Tracked file does not match HEAD: " + name)
        actual_mode = "120000" if path.is_symlink() else "100755" if path.stat().st_mode & 0o111 else "100644"
        if actual_mode != mode:
            raise RuntimeError("Tracked mode does not match HEAD: " + name)
        tracked[name] = dict(file_record(path), git_blob=expected_blob, git_mode=mode)
    assets_root = REPO / "backend/internal/web/dist"
    assets = {str(path.relative_to(REPO)): file_record(path) for path in sorted(assets_root.rglob("*")) if path.is_file() or path.is_symlink()}
    if not assets:
        raise RuntimeError("Build fresh embedded frontend assets before browser acceptance")
    portal = {name: record for name, record in tracked.items() if name.startswith(("backend/internal/veyra/portal_dist/", "extensions/veyra/portal/"))}
    state = {"head": git("rev-parse", "HEAD").decode().strip(), "tree": git("rev-parse", "HEAD^{tree}").decode().strip(),
             "worktree_status": git("status", "--porcelain=v1", "--untracked-files=all").decode(),
             "object_format": object_format, "tracked_files": tracked, "portal_files": portal, "embedded_assets": assets}
    state["fingerprint_sha256"] = hashlib.sha256(json.dumps(state, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return state


def clean_environment(output):
    # The fixture/browser receives no GitHub token, cloud secret, provider API key,
    # proxy credential, auth cookie, or the runner's persistent browser profile.
    selected = ("PATH", "GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE", "PLAYWRIGHT_BROWSERS_PATH")
    env = {key: os.environ[key] for key in selected if key in os.environ}
    env.update(HOME=os.environ.get("HOME", "/tmp"), GOENV="off", GOTOOLCHAIN="local")
    caches = json.loads(subprocess.check_output(["go", "env", "-json", "GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE"], env=env))
    env.update(caches)
    home = output / "isolated-home"
    home.mkdir()
    env.update(HOME=str(home), LANG="C.UTF-8", LC_ALL="C.UTF-8", TZ="UTC", TMPDIR=str(output),
               GOMAXPROCS="2", GOFLAGS="-p=1", GOMEMLIMIT="3GiB", GOPROXY="off", GOSUMDB="off",
               GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null", PYTHONDONTWRITEBYTECODE="1",
               VEYRA_BROWSER_MANIFEST=str(output / "manifest.json"), VEYRA_BROWSER_STOP=str(output / "stop"))
    return env


def process_group_alive(process):
    if process is None:
        return False
    process.poll()  # Reap the owned leader before checking its descendants.
    try:
        os.killpg(process.pid, 0)
        return True
    except ProcessLookupError:
        return False


def stop_process(process):
    # A crashed/reaped Python or Go leader can leave descendants in its session.
    # start_new_session=True gives each owned command its own process group.
    for sig in (signal.SIGTERM, signal.SIGKILL):
        if not process_group_alive(process):
            return True
        try:
            os.killpg(process.pid, sig)
        except ProcessLookupError:
            return True
        deadline = time.monotonic() + 10
        while process_group_alive(process) and time.monotonic() < deadline:
            time.sleep(0.1)
    return not process_group_alive(process)


def ports_closed(manifest):
    result = {}
    for name, url in manifest.get("urls", {}).items():
        parsed = urlsplit(url)
        if parsed.hostname != "127.0.0.1":
            raise RuntimeError("Fixture manifest has a non-loopback origin")
        try:
            with socket.create_connection((parsed.hostname, parsed.port), timeout=0.3):
                result[name] = False
        except OSError:
            result[name] = True
    return result


def verify_network_receipts(result, manifest):
    allowed_origins = set(manifest["urls"].values())
    expected_modes = {"enabled", "disabled", "bridge_off", "portal_off"}
    if set(manifest["urls"]) != expected_modes or len(allowed_origins) != 4:
        raise RuntimeError("Expected four distinct loopback fixture origins")
    for origin in allowed_origins:
        parsed = urlsplit(origin)
        if parsed.scheme != "http" or parsed.hostname != "127.0.0.1" or not parsed.port or parsed.path or parsed.query or parsed.fragment or parsed.username:
            raise RuntimeError("Invalid fixture origin: " + origin)
    decisions = []
    for case in result["cases"]:
        if case.get("blocked_websockets"):
            raise RuntimeError("Unexpected WebSocket attempt: " + case["name"])
        for row in case["route_decisions"]:
            parsed = urlsplit(row["url"])
            origin = parsed.scheme + "://" + parsed.netloc
            expected = "continue-exact-loopback-fixture" if origin in allowed_origins else "fulfill-locally-no-network"
            if row["decision"] != expected:
                raise RuntimeError("Invalid network routing decision: " + repr(row))
            decisions.append(dict(case=case["name"], **row))
        if not case["route_decisions"]:
            raise RuntimeError("Missing interception receipt for case: " + case["name"])
    return decisions


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--expected-sha", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.expected_sha):
        parser.error("Expected an exact 40-character Git commit SHA")
    out = args.output.resolve()
    if out == REPO or REPO in out.parents:
        parser.error("Evidence output must be outside the checkout")
    out.mkdir(parents=True, exist_ok=False)
    receipt = {"schema_version": 1, "runner_identity": IDENTITY, "state": "running", "started_utc": now(),
               "expected_sha": args.expected_sha, "browser_cases_expected": 22, "browser_cases_run": 0,
               "kind": "positive-browser-acceptance", "limits": [
                   "Actual middleware and portal assets are tested; synthetic auth and in-memory tickets are used.",
                   "Disabled SPA behavior is a downstream sentinel pass-through check, not production Vue rendering.",
                   "Dependency/tool/browser installation is a prior networked setup phase. Application requests during acceptance are loopback or locally intercepted.",
                   "Route receipts are application-level interception evidence, not packet capture.",
                   "This portable CI source/runner identity is separate from any cloud test receipt."]}
    write_json(out / "receipt.json", receipt)
    before = None
    server = browser = None
    try:
        before = source_snapshot()
        write_json(out / "source-before.json", before)
        if before["head"] != args.expected_sha or before["worktree_status"]:
            raise RuntimeError("Checkout must be the exact clean PR head")
        receipt.update(source_sha=before["head"], source_tree=before["tree"], source_fingerprint_sha256=before["fingerprint_sha256"], embedded_assets_count=len(before["embedded_assets"]))
        receipt["free_bytes_start"] = shutil.disk_usage(out).free
        if receipt["free_bytes_start"] < 900 * 1024 * 1024:
            raise RuntimeError("Less than 900 MiB free; no cleanup attempted")
        env = clean_environment(out)
        write_json(out / "environment.json", env)
        receipt["go_version"] = subprocess.check_output(["go", "version"], env=env).decode().strip()
        receipt["node_version"] = subprocess.check_output(["node", "--version"], env=env).decode().strip()
        receipt["pnpm_version"] = subprocess.check_output(["pnpm", "--version"], env=env).decode().strip()
        receipt["python_version"] = sys.version
        receipt["python_packages"] = {name: importlib.metadata.version(name) for name in ("playwright", "pyee", "greenlet", "typing_extensions")}
        receipt["harness_files"] = {path.name: sha256(path) for path in sorted(HERE.iterdir()) if path.is_file()}
        overlay = {"Replace": {str(REPO / "backend/internal/veyra/acceptance_harness_test.go"): str(HERE / "fixture_test.go")}}
        write_json(out / "overlay.json", overlay)
        cmd = ["go", "test", "-p", "1", "-overlay", str(out / "overlay.json"), "-run", "^TestLocalBrowserAcceptanceHarness$", "-count=1", "-timeout=20m", "-v", "./internal/veyra"]
        browser_cmd = [sys.executable, str(HERE / "browser_acceptance.py"), "--manifest", str(out / "manifest.json"), "--output", str(out)]
        receipt.update(command=cmd, browser_command=browser_cmd)
        write_json(out / "receipt.json", receipt)
        with (out / "server.log").open("wb") as server_log:
            server = subprocess.Popen(cmd, cwd=REPO / "backend", env=env, stdout=server_log, stderr=subprocess.STDOUT, start_new_session=True)
            deadline = time.monotonic() + 900
            while not (out / "manifest.json").exists():
                if server.poll() is not None:
                    raise RuntimeError("Fixture compile/start exited " + str(server.returncode) + "; see server.log")
                if time.monotonic() >= deadline:
                    raise RuntimeError("Fixture readiness timeout")
                time.sleep(0.5)
            with (out / "browser.log").open("wb") as browser_log:
                browser = subprocess.Popen(browser_cmd, cwd=HERE, env=env, stdout=browser_log, stderr=subprocess.STDOUT, start_new_session=True)
                receipt["browser_exit_code"] = browser.wait(timeout=600)
            (out / "stop").touch()
            receipt["server_exit_code"] = server.wait(timeout=20)
        if not (out / "results.json").exists():
            raise RuntimeError("Browser produced no case results; startup/runtime failure is preserved in browser.log")
        result = json.loads((out / "results.json").read_text())
        manifest = json.loads((out / "manifest.json").read_text())
        receipt["browser_cases_run"] = len(result["cases"])
        receipt["browser_summary"] = {key: result[key] for key in ("status", "passed", "failed", "external_requests_sent", "browser_version")}
        receipt["network_decisions"] = verify_network_receipts(result, manifest)
        if len(result["cases"]) != 22 or len({case["name"] for case in result["cases"]}) != 22 or result["passed"] != 22 or result["failed"] != 0 or result["external_requests_sent"] != 0:
            raise RuntimeError("Browser acceptance did not pass all 22 cases; retain raw failures")
        if receipt["browser_exit_code"] or receipt["server_exit_code"]:
            raise RuntimeError("Nonzero browser or fixture exit")
        receipt["state"] = "passed"
    except BaseException as error:
        receipt["state"] = "failed-or-incomplete"
        receipt["blocker"] = f"{type(error).__name__}: {error}"
    finally:
        (out / "stop").touch()
        browser_group_closed = stop_process(browser)
        if server is not None and server.poll() is None:
            try:
                server.wait(timeout=10)
            except subprocess.TimeoutExpired:
                pass
        server_group_closed = stop_process(server)
        manifest = json.loads((out / "manifest.json").read_text()) if (out / "manifest.json").exists() else {}
        receipt["cleanup"] = {"server_returncode": None if server is None else server.poll(), "browser_returncode": None if browser is None else browser.poll(), "process_groups_closed": {"browser": browser_group_closed, "server": server_group_closed}, "loopback_ports_closed": ports_closed(manifest)}
        try:
            after = source_snapshot()
            write_json(out / "source-after.json", after)
            receipt["source_and_assets_unchanged"] = before == after
            if not receipt["source_and_assets_unchanged"]:
                receipt["state"] = "failed-source-check"
        except BaseException as error:
            receipt["state"] = "failed-source-check"
            receipt["source_check_error"] = str(error)
        if not all(receipt["cleanup"]["loopback_ports_closed"].values()) or not all(receipt["cleanup"]["process_groups_closed"].values()):
            receipt["state"] = "failed-cleanup-check"
        receipt["ended_utc"] = now()
        receipt["free_bytes_end"] = shutil.disk_usage(out).free
        receipt["artifacts"] = {path.name: {"bytes": path.stat().st_size, "sha256": sha256(path)} for path in sorted(out.iterdir()) if path.is_file() and path.name != "receipt.json"}
        write_json(out / "receipt.json", receipt)
    print(json.dumps({"receipt": str(out / "receipt.json"), "state": receipt["state"], "summary": receipt.get("browser_summary"), "blocker": receipt.get("blocker")}))
    return 0 if receipt["state"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
