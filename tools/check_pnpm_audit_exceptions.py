#!/usr/bin/env python3
import argparse
import json
import sys
from datetime import date


SEVERITIES = ("info", "low", "moderate", "high", "critical")
HIGH_SEVERITIES = {"high", "critical"}
REQUIRED_FIELDS = {"package", "advisory", "severity", "mitigation", "expires_on"}


def split_kv(line: str) -> tuple[str, str]:
    # 解析 "key: value" 形式的简单 YAML 行，并去除引号。
    key, value = line.split(":", 1)
    value = value.strip()
    if (value.startswith('"') and value.endswith('"')) or (
        value.startswith("'") and value.endswith("'")
    ):
        value = value[1:-1]
    return key.strip(), value


def parse_exceptions(path: str) -> list[dict]:
    # 轻量解析异常清单，避免引入额外依赖。
    exceptions = []
    current = None
    with open(path, "r", encoding="utf-8") as handle:
        for raw in handle:
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("version:") or line.startswith("exceptions:"):
                continue
            if line.startswith("- "):
                if current:
                    exceptions.append(current)
                current = {}
                line = line[2:].strip()
                if line:
                    key, value = split_kv(line)
                    current[key] = value
                continue
            if current is not None and ":" in line:
                key, value = split_kv(line)
                current[key] = value
    if current:
        exceptions.append(current)
    return exceptions


def pick_advisory_id(advisory: dict) -> str | None:
    # 优先使用可稳定匹配的标识（GHSA/URL/CVE），避免误匹配到其他同名漏洞。
    return (
        advisory.get("github_advisory_id")
        or advisory.get("url")
        or (advisory.get("cves") or [None])[0]
        or (str(advisory.get("id")) if advisory.get("id") is not None else None)
        or advisory.get("title")
        or advisory.get("advisory")
        or advisory.get("overview")
    )


def iter_vulns(data: dict):
    # 兼容 pnpm audit 的不同输出结构（advisories / vulnerabilities），并提取 advisory 标识。
    advisories = data.get("advisories")
    if isinstance(advisories, dict):
        for advisory in advisories.values():
            name = advisory.get("module_name") or advisory.get("name")
            severity = advisory.get("severity")
            advisory_id = pick_advisory_id(advisory)
            title = (
                advisory.get("title")
                or advisory.get("advisory")
                or advisory.get("overview")
                or advisory.get("url")
            )
            yield name, severity, advisory_id, title

    vulnerabilities = data.get("vulnerabilities")
    if isinstance(vulnerabilities, dict):
        for name, vuln in vulnerabilities.items():
            severity = vuln.get("severity")
            via = vuln.get("via", [])
            titles = []
            advisories = []
            if isinstance(via, list):
                for item in via:
                    if isinstance(item, dict):
                        advisories.append(
                            item.get("github_advisory_id")
                            or item.get("url")
                            or item.get("source")
                            or item.get("title")
                            or item.get("name")
                        )
                        titles.append(
                            item.get("title")
                            or item.get("url")
                            or item.get("advisory")
                            or item.get("source")
                        )
                    elif isinstance(item, str):
                        advisories.append(item)
                        titles.append(item)
            elif isinstance(via, str):
                advisories.append(via)
                titles.append(via)
            title = "; ".join(str(t) for t in titles if t)
            for advisory_id in [a for a in advisories if a]:
                yield name, severity, advisory_id, title


def is_nonempty_text(value) -> bool:
    return isinstance(value, str) and bool(value.strip())


def validate_advisory_fields(advisory, location: str) -> None:
    if not isinstance(advisory, dict):
        raise ValueError(f"{location} must be an object")
    for field in (
        "module_name",
        "name",
        "github_advisory_id",
        "url",
        "title",
        "advisory",
        "overview",
    ):
        value = advisory.get(field)
        if value is not None and not isinstance(value, str):
            raise ValueError(f"{location}.{field} must be a string")
    for field in ("id", "source"):
        value = advisory.get(field)
        if value is not None and not (
            is_nonempty_text(value) or (type(value) is int and value >= 0)
        ):
            raise ValueError(f"{location}.{field} must be a string or integer")
    cves = advisory.get("cves")
    if cves is not None and not (
        isinstance(cves, list) and all(is_nonempty_text(cve) for cve in cves)
    ):
        raise ValueError(f"{location}.cves must be a list of nonempty strings")
    if "severity" in advisory:
        severity = advisory["severity"]
        if (
            not is_nonempty_text(severity)
            or normalize_severity(severity) not in SEVERITIES
        ):
            raise ValueError(f"{location}.severity must be a recognized severity")


def validate_audit_report(data) -> None:
    # Local hardening of an uncovered upstream-origin checker gap, not an upstream
    # port: pnpm 9 emits {"error": ...} on registry failure. See also
    # https://github.com/pnpm/pnpm/issues/13033 for upstream audit failures.
    # CI captures nonzero audit exits to allow exceptions, so validate the report
    # before applying policy: https://pnpm.io/cli/audit
    if not isinstance(data, dict):
        raise ValueError("audit report must be an object")
    if "error" in data or "errors" in data:
        raise ValueError("audit command reported an error; no successful audit report")
    report_fields = {"advisories", "vulnerabilities"}.intersection(data)
    if not report_fields:
        raise ValueError("audit report must contain advisories or vulnerabilities")
    for field in report_fields:
        if not isinstance(data[field], dict):
            raise ValueError(f"{field} must be an object")

    metadata = data.get("metadata")
    counts = metadata.get("vulnerabilities") if isinstance(metadata, dict) else None
    if not isinstance(counts, dict) or not set(SEVERITIES).issubset(counts):
        raise ValueError("metadata.vulnerabilities must contain all severity counts")
    for severity, count in counts.items():
        if severity not in (*SEVERITIES, "total") or type(count) is not int or count < 0:
            raise ValueError(f"invalid metadata.vulnerabilities count: {severity}")

    for key, advisory in data.get("advisories", {}).items():
        location = f"advisories[{key}]"
        validate_advisory_fields(advisory, location)
        if not is_nonempty_text(advisory.get("module_name") or advisory.get("name")):
            raise ValueError(f"{location} missing package name")
        if "severity" not in advisory:
            raise ValueError(f"{location} missing severity")
        if not is_nonempty_text(pick_advisory_id(advisory)):
            raise ValueError(f"{location} missing advisory id")

    for name, vuln in data.get("vulnerabilities", {}).items():
        location = f"vulnerabilities[{name}]"
        validate_advisory_fields(vuln, location)
        if not is_nonempty_text(name):
            raise ValueError(f"{location} missing package name")
        if "severity" not in vuln:
            raise ValueError(f"{location} missing severity")
        via = vuln.get("via")
        if isinstance(via, str):
            via = [via]
        if not isinstance(via, list) or not via:
            raise ValueError(f"{location}.via must contain advisory data")
        for item in via:
            if isinstance(item, str):
                if not is_nonempty_text(item):
                    raise ValueError(f"{location}.via contains an empty advisory id")
                continue
            validate_advisory_fields(item, f"{location}.via")
            advisory_id = (
                item.get("github_advisory_id")
                or item.get("url")
                or item.get("source")
                or item.get("title")
                or item.get("name")
            )
            if not (is_nonempty_text(advisory_id) or type(advisory_id) is int):
                raise ValueError(f"{location}.via missing advisory id")
            if "severity" in item and (
                SEVERITIES.index(normalize_severity(item["severity"]))
                > SEVERITIES.index(normalize_severity(vuln["severity"]))
            ):
                raise ValueError(f"{location}.severity understates a via advisory")

    # Counts may represent dependency paths rather than distinct advisories.
    # Require evidence for positive severe counts, without requiring equal totals.
    reported_severities = {normalize_severity(v[1]) for v in iter_vulns(data)}
    for severity in HIGH_SEVERITIES:
        if counts[severity] > 0 and severity not in reported_severities:
            raise ValueError(
                f"metadata reports {severity} vulnerabilities without advisory data"
            )


def normalize_severity(severity: str) -> str:
    # 统一大小写，避免比较失败。
    return (severity or "").strip().lower()


def normalize_package(name: str) -> str:
    # 包名只去掉首尾空白，保留原始大小写，同时兼容非字符串输入。
    if name is None:
        return ""
    return str(name).strip()


def normalize_advisory(advisory: str) -> str:
    # advisory 统一为小写匹配，避免 GHSA/URL 因大小写差异导致漏匹配。
    # pnpm 的 source 字段可能是数字，这里统一转为字符串以保证可比较。
    if advisory is None:
        return ""
    return str(advisory).strip().lower()


def parse_date(value: str) -> date | None:
    # 仅接受 ISO8601 日期格式，非法值视为无效。
    try:
        return date.fromisoformat(value)
    except ValueError:
        return None


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--audit", required=True)
    parser.add_argument("--exceptions", required=True)
    args = parser.parse_args()

    try:
        with open(args.audit, "r", encoding="utf-8") as handle:
            audit = json.load(handle)
        validate_audit_report(audit)
    except (OSError, ValueError, UnicodeError) as error:
        sys.stderr.write(f"Invalid audit report: {error}\n")
        return 1

    # 读取异常清单并建立索引，便于快速匹配包名 + advisory。
    exceptions = parse_exceptions(args.exceptions)
    exception_index = {}
    errors = []

    for exc in exceptions:
        missing = [field for field in REQUIRED_FIELDS if not exc.get(field)]
        if missing:
            errors.append(
                f"Exception missing required fields {missing}: {exc.get('package', '<unknown>')}"
            )
            continue
        exc_severity = normalize_severity(exc.get("severity"))
        exc_package = normalize_package(exc.get("package"))
        exc_advisory = normalize_advisory(exc.get("advisory"))
        exc_date = parse_date(exc.get("expires_on"))
        if exc_date is None:
            errors.append(
                f"Exception has invalid expires_on date: {exc.get('package', '<unknown>')}"
            )
            continue
        if not exc_package or not exc_advisory:
            errors.append("Exception missing package or advisory value")
            continue
        key = (exc_package, exc_advisory)
        if key in exception_index:
            errors.append(
                f"Duplicate exception for {exc_package} advisory {exc.get('advisory')}"
            )
            continue
        exception_index[key] = {
            "raw": exc,
            "severity": exc_severity,
            "expires_on": exc_date,
        }

    today = date.today()
    missing_exceptions = []
    expired_exceptions = []

    # 去重处理：同一包名 + advisory 可能在不同字段重复出现。
    seen = set()
    for name, severity, advisory_id, title in iter_vulns(audit):
        sev = normalize_severity(severity)
        if sev not in HIGH_SEVERITIES or not name:
            continue
        advisory_key = normalize_advisory(advisory_id)
        if not advisory_key:
            errors.append(
                f"High/Critical vulnerability missing advisory id: {name} ({sev})"
            )
            continue
        key = (normalize_package(name), advisory_key)
        if key in seen:
            continue
        seen.add(key)
        exc = exception_index.get(key)
        if exc is None:
            missing_exceptions.append((name, sev, advisory_id, title))
            continue
        if exc["severity"] and exc["severity"] != sev:
            errors.append(
                "Exception severity mismatch: "
                f"{name} ({advisory_id}) expected {sev}, got {exc['severity']}"
            )
        if exc["expires_on"] and exc["expires_on"] < today:
            expired_exceptions.append(
                (name, sev, advisory_id, exc["expires_on"].isoformat())
            )

    if missing_exceptions:
        errors.append("High/Critical vulnerabilities missing exceptions:")
        for name, sev, advisory_id, title in missing_exceptions:
            label = f"{name} ({sev})"
            if advisory_id:
                label = f"{label} [{advisory_id}]"
            if title:
                label = f"{label}: {title}"
            errors.append(f"- {label}")

    if expired_exceptions:
        errors.append("Exceptions expired:")
        for name, sev, advisory_id, expires_on in expired_exceptions:
            errors.append(
                f"- {name} ({sev}) [{advisory_id}] expired on {expires_on}"
            )

    if errors:
        sys.stderr.write("\n".join(errors) + "\n")
        return 1

    print("Audit exceptions validated.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())