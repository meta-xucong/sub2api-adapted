#!/usr/bin/env python3
"""Portable CI suite v1: same 22 middleware cases, with Playwright-managed Chromium."""
import argparse
import json
import re
import time
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

from playwright.sync_api import sync_playwright


MOBILE_UA = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
TRUSTED_CALLBACK = "https://video.aiself.vip/auth/veyra/callback"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--negative-csp", action="store_true")
    args = parser.parse_args()
    manifest = json.loads(Path(args.manifest).read_text())
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    permitted_origins = set(manifest["urls"].values())
    for origin in permitted_origins:
        parsed = urlsplit(origin)
        assert parsed.scheme == "http" and parsed.hostname == "127.0.0.1", origin
    results = {
        "started_at_unix": time.time(),
        "fixture": manifest,
        "external_requests_sent": 0,
        "external_interception": "HTTP(S): only exact loopback fixture origins continued; all others fulfilled locally. WebSockets closed without connecting. Chromium remote DNS disabled.",
        "cases": [],
        "kind": "expected-negative-csp-control" if args.negative_csp else "positive-browser-acceptance",
    }

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            headless=True,
            args=[
                "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
                "--disable-component-update", "--disable-default-apps", "--disable-extensions",
                "--disable-sync", "--disable-domain-reliability", "--no-first-run",
                "--no-default-browser-check", "--metrics-recording-only",
                "--disable-features=MediaRouter,OptimizationHints,AutofillServerCommunication",
                "--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE localhost, EXCLUDE 127.0.0.1",
            ],
        )
        results["browser_version"] = browser.version

        def run_case(name, action, mobile=False):
            if args.negative_csp != name.startswith("negative-csp-"):
                return
            record = {"name": name, "mobile": mobile, "intercepted_external": [], "local_requests": [], "route_decisions": [], "blocked_websockets": [], "browser_requests": [], "request_failures": [], "csp_violations": [], "console": [], "page_errors": []}
            options = {"viewport": {"width": 1365, "height": 900}, "service_workers": "block", "accept_downloads": False}
            if mobile:
                options.update(user_agent=MOBILE_UA, viewport={"width": 390, "height": 844}, is_mobile=True, has_touch=True)
            context = browser.new_context(**options)

            def intercept(route):
                request = route.request
                parts = urlsplit(request.url)
                origin = parts.scheme + "://" + parts.netloc
                if origin in permitted_origins:
                    record["local_requests"].append({"url": request.url, "method": request.method})
                    record["route_decisions"].append({"url": request.url, "method": request.method, "decision": "continue-exact-loopback-fixture"})
                    route.continue_()
                    return
                record["intercepted_external"].append({"url": request.url, "method": request.method, "post_data": request.post_data, "resource_type": request.resource_type})
                record["route_decisions"].append({"url": request.url, "method": request.method, "decision": "fulfill-locally-no-network"})
                route.fulfill(status=200, content_type="text/html; charset=utf-8", body="<!doctype html><title>Intercepted external destination</title><p id=external-intercept>Local interception only</p>")

            context.route("**/*", intercept)
            # The portal has no WebSocket dependency. Do not call connect_to_server:
            # a routed WebSocket remains local and is closed before any upstream hop.
            def block_websocket(websocket):
                record["blocked_websockets"].append({"url": websocket.url, "decision": "close-without-upstream-connection"})
                websocket.close(code=1008, reason="Browser acceptance has no WebSocket dependency")
            context.route_web_socket("**/*", block_websocket)
            context.on("request", lambda request: record["browser_requests"].append({"url": request.url, "method": request.method, "resource_type": request.resource_type}))
            context.on("requestfailed", lambda request: record["request_failures"].append({"url": request.url, "method": request.method, "failure": request.failure}))
            context.expose_binding("__auditReportCSP", lambda source, event: record["csp_violations"].append(event))
            context.add_init_script("""
                (() => {
                  window.__auditViolations = [];
                  document.addEventListener('securitypolicyviolation', event => {
                    const item = {effectiveDirective: event.effectiveDirective, blockedURI: event.blockedURI, disposition: event.disposition};
                    window.__auditViolations.push(item);
                    window.__auditReportCSP(item);
                  });
                  const allowed = """ + json.dumps(sorted(permitted_origins)) + """;
                  if (allowed.includes(location.origin)) {
                    localStorage.setItem('auth_token', """ + json.dumps(manifest["synthetic_token"]) + """);
                    localStorage.setItem('auth_user', JSON.stringify({id: 991, username: 'Local audit user'}));
                    localStorage.setItem('token_expires_at', String(Date.now() + 3600000));
                  }
                })();
            """)
            page = context.new_page()
            page.set_default_timeout(7000)
            page.on("console", lambda message: record["console"].append({"type": message.type, "text": message.text}))
            page.on("pageerror", lambda error: record["page_errors"].append(str(error)))
            try:
                action(page, record)
                assert not record["page_errors"], record["page_errors"]
                assert not record["blocked_websockets"], record["blocked_websockets"]
                record["status"] = "passed"
            except Exception as error:
                record["status"] = "failed"
                record["error"] = str(error)
            finally:
                record["final_url"] = page.url
                screenshot = output / (name + ".png")
                try:
                    page.screenshot(path=str(screenshot), full_page=True)
                    record["screenshot"] = str(screenshot)
                except Exception as error:
                    record["screenshot_error"] = str(error)
                context.close()
                results["cases"].append(record)
                (output / "results.json").write_text(json.dumps(results, indent=2, ensure_ascii=False) + "\n")
                print(record["status"].upper() + " " + name, flush=True)

        def policy_check(response, record, portal=True):
            policy = response.headers.get("content-security-policy", "")
            directives = {fields[0]: fields[1:] for entry in policy.split(";") if (fields := entry.split())}
            record["csp_policy"] = policy
            assert "'self'" in directives.get("form-action", []), policy
            assert any(value.startswith("'nonce-") for value in directives.get("script-src", [])), policy
            assert "'unsafe-inline'" not in directives.get("script-src", []), policy
            assert directives.get("media-src") == ["'self'", "data:", "https://vidgen.x.ai"], policy
            if portal:
                assert directives["form-action"] == ["'self'", TRUSTED_CALLBACK], directives["form-action"]
            else:
                assert directives["form-action"] == ["'self'"], directives["form-action"]

        def ready(page, record, path="/?desktop=1"):
            response = page.goto(manifest["urls"]["enabled"] + path, wait_until="networkidle")
            policy_check(response, record)
            page.locator('#sessionState[data-authenticated="true"]').wait_for()
            return response

        def expect_external(page, record, host, path, method):
            page.wait_for_url(lambda url: urlsplit(url).hostname == host, wait_until="domcontentloaded")
            navigations = [request for request in record["intercepted_external"] if request["resource_type"] == "document"]
            assert len(navigations) == 1, navigations
            request = navigations[0]
            parsed = urlsplit(request["url"])
            assert parsed.scheme == "https" and parsed.hostname == host and parsed.path == path, request
            assert request["method"] == method, request
            if method == "POST":
                assert parsed.query == "", request
                body = parse_qs(request["post_data"] or "")
                assert len(body.get("ticket", [""])[0]) == 64, request
                assert set(body) == {"ticket"}, request
            else:
                assert len(parse_qs(parsed.query).get("ticket", [""])[0]) == 64, request
            assert not record["csp_violations"], record["csp_violations"]

        def desktop_root(page, record):
            ready(page, record)
            assert urlsplit(page.url).path == "/", page.url
            assert page.locator(".page-shell").count() == 1
            assert not record["csp_violations"], record["csp_violations"]
        run_case("desktop-root-default-csp", desktop_root)

        def desktop_video(page, record):
            ready(page, record)
            page.locator('[data-route="video"]').click()
            expect_external(page, record, "video.aiself.vip", "/auth/veyra/callback", "POST")
        run_case("desktop-video-exact-callback-post", desktop_video)

        def expect_form_action_block(page, record):
            # Observe the existing browser event binding from Python. Playwright's
            # wait_for_function uses eval, which the production CSP forbids.
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if any(item["effectiveDirective"] == "form-action" and
                       item["disposition"] == "enforce"
                       for item in record["csp_violations"]):
                    return
                # Pump Playwright events without evaluating script in the page.
                page.wait_for_timeout(25)
            raise AssertionError("No enforced form-action violation: " +
                                 repr(record["csp_violations"]))

        def negative_csp_control(page, record):
            response = page.goto(manifest["urls"]["enabled"] + "/?desktop=1", wait_until="networkidle")
            policy_check(response, record, portal=False)
            page.locator('#sessionState[data-authenticated="true"]').wait_for()
            page.locator('[data-route="video"]').click()
            expect_form_action_block(page, record)
            assert not record["intercepted_external"], record["intercepted_external"]
            assert any(item["url"].endswith("/api/veyra/login-ticket") and item["method"] == "POST" for item in record["local_requests"])
            record["expected_negative_observed"] = "The real portal issued a local synthetic Video ticket, then Chromium blocked its callback form under the unmodified self-only policy."
        run_case("negative-csp-video-post-blocked-with-repair-disabled", negative_csp_control)

        for suffix, destination in [
            ("attacker", "https://attacker.invalid/steal"),
            ("sibling-host", "https://video.aiself.vip.attacker.invalid/auth/veyra/callback"),
            ("wrong-path", "https://video.aiself.vip/auth/other"),
            ("path-suffix", "https://video.aiself.vip/auth/veyra/callback/extra"),
            ("http-downgrade", "http://video.aiself.vip/auth/veyra/callback"),
            ("wrong-port", "https://video.aiself.vip:444/auth/veyra/callback"),
        ]:
            def blocked_form(page, record, destination=destination):
                ready(page, record)
                page.evaluate("""destination => {
                    const form = document.createElement('form');
                    form.method = 'POST'; form.action = destination;
                    form.innerHTML = '<input name="ticket" value="synthetic-attacker-probe">';
                    document.body.appendChild(form); form.submit();
                }""", destination)
                expect_form_action_block(page, record)
                assert not record["intercepted_external"], record["intercepted_external"]
                assert urlsplit(page.url).hostname == "127.0.0.1", page.url
            run_case("csp-blocks-" + suffix, blocked_form)

        def nonportal_callback_blocked(page, record):
            response = page.goto(manifest["urls"]["enabled"] + "/dashboard", wait_until="networkidle")
            policy_check(response, record, portal=False)
            page.evaluate("""destination => {const f = document.createElement('form'); f.method='POST'; f.action=destination; document.body.appendChild(f); f.submit();}""", TRUSTED_CALLBACK)
            expect_form_action_block(page, record)
            assert not record["intercepted_external"], record["intercepted_external"]
        run_case("nonportal-csp-stays-self-only", nonportal_callback_blocked)

        def mobile_root(page, record):
            ready(page, record, "/?audit=mobile#anchor")
            page.wait_for_url("**/_veyra/mobile.html?audit=mobile#anchor")
            assert page.locator(".mobile-shell").count() == 1
            assert not record["csp_violations"], record["csp_violations"]
        run_case("mobile-root-navigation-query-hash", mobile_root, mobile=True)

        def mobile_desktop_override(page, record):
            ready(page, record, "/?desktop=1")
            assert urlsplit(page.url).path == "/", page.url
            assert page.locator(".page-shell").count() == 1
            assert not record["csp_violations"], record["csp_violations"]
        run_case("mobile-desktop-override", mobile_desktop_override, mobile=True)

        def brand_root(page, record):
            ready(page, record)
            page.locator(".brand-lockup").click()
            page.wait_for_url(manifest["urls"]["enabled"] + "/")
            assert page.title() == "Veyra Agent", page.title()
            assert not page.locator("#original-spa").count()
        run_case("desktop-brand-returns-root", brand_root)

        for mobile in (False, True):
            for target, host, path, method in [
                ("alchemy", "alchemy.aiself.vip", "/", "GET"),
                ("alchemy-mobile", "alchemy.aiself.vip", "/h5", "GET"),
                ("video", "video.aiself.vip", "/auth/veyra/callback", "POST"),
            ]:
                def return_launch(page, record, target=target, host=host, path=path, method=method):
                    page.goto(manifest["urls"]["enabled"] + "/_veyra/return?target=" + target, wait_until="domcontentloaded")
                    expect_external(page, record, host, path, method)
                run_case(("mobile" if mobile else "desktop") + "-return-" + target, return_launch, mobile=mobile)

        def mobile_console_return(page, record):
            page.goto(manifest["urls"]["enabled"] + "/_veyra/return?target=sub2api-console", wait_until="domcontentloaded")
            page.wait_for_url(manifest["urls"]["enabled"] + "/dashboard")
            assert page.locator("#original-spa").count() == 1
            assert not record["intercepted_external"]
            assert not record["csp_violations"], record["csp_violations"]
        run_case("mobile-return-console", mobile_console_return, mobile=True)

        for mode in ("disabled", "bridge_off", "portal_off"):
            def disabled_passthrough(page, record, mode=mode):
                for path in ("/", "/_veyra/return?target=video", "/_veyra/mobile.html"):
                    response = page.goto(manifest["urls"][mode] + path, wait_until="networkidle")
                    policy_check(response, record, portal=False)
                    assert response.headers.get("x-audit-spa-fallback") == "original-spa-sentinel"
                    assert page.locator("#original-spa").count() == 1
                    assert page.title() == "Original SPA sentinel"
                assert not record["intercepted_external"]
            run_case("disabled-flags-preserve-spa-" + mode, disabled_passthrough)

        browser.close()
    results["finished_at_unix"] = time.time()
    results["passed"] = sum(case["status"] == "passed" for case in results["cases"])
    results["failed"] = sum(case["status"] == "failed" for case in results["cases"])
    results["status"] = "passed" if not results["failed"] else "failed"
    (output / "results.json").write_text(json.dumps(results, indent=2, ensure_ascii=False) + "\n")
    print(json.dumps({key: results[key] for key in ("status", "passed", "failed", "external_requests_sent")}))
    return 0 if results["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
