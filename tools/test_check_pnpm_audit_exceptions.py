#!/usr/bin/env python3
"""Regression tests for the fail-closed audit boundary and existing exception policy."""

import copy
import io
import json
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from datetime import date
from pathlib import Path
from unittest.mock import patch

import check_pnpm_audit_exceptions as checker


class FixedDate(date):
    @classmethod
    def today(cls):
        return cls(2026, 10, 17)


def report(advisories=None, **counts):
    # pnpm 9's successful JSON shape, including the valid zero-advisory case.
    return {
        "actions": [],
        "advisories": {} if advisories is None else advisories,
        "muted": [],
        "metadata": {
            "vulnerabilities": {severity: counts.get(severity, 0) for severity in checker.SEVERITIES},
            "dependencies": 2,
            "devDependencies": 0,
            "optionalDependencies": 0,
            "totalDependencies": 2,
        },
    }


def advisory(severity="high", **overrides):
    result = {
        "id": 1234567,
        "module_name": "example-package",
        "severity": severity,
        "github_advisory_id": "GHSA-aaaa-bbbb-cccc",
        "url": "https://github.com/advisories/GHSA-aaaa-bbbb-cccc",
        "title": "Example advisory",
        "cves": ["CVE-2026-12345"],
        "findings": [{"version": "1.0.0", "paths": [". > example-package@1.0.0"]}],
    }
    result.update(overrides)
    return result


def vulnerability_report(via=None, severity="high"):
    data = report(**{severity: 1})
    del data["advisories"]
    data["vulnerabilities"] = {
        "example-package": {
            "name": "example-package",
            "severity": severity,
            "via": [advisory(severity)] if via is None else via,
        },
    }
    return data


def exception_yaml(
    package="example-package", advisory_id="GHSA-aaaa-bbbb-cccc",
    severity="high", expires_on="2026-10-17",
):
    return (
        "version: 1\nexceptions:\n"
        f"  - package: {package}\n"
        f'    advisory: "{advisory_id}"\n'
        f"    severity: {severity}\n"
        '    mitigation: "Untrusted input is blocked"\n'
        f'    expires_on: "{expires_on}"\n'
    )


class AuditCheckerTests(unittest.TestCase):
    def run_checker(self, data, exceptions="version: 1\nexceptions: []\n", raw=False):
        with tempfile.TemporaryDirectory() as directory:
            audit_path = Path(directory) / "audit.json"
            exceptions_path = Path(directory) / "exceptions.yml"
            audit_path.write_text(data if raw else json.dumps(data), encoding="utf-8")
            exceptions_path.write_text(exceptions, encoding="utf-8")
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch("sys.argv", ["checker", "--audit", str(audit_path), "--exceptions", str(exceptions_path)]),
                patch.object(checker, "date", FixedDate),
                redirect_stdout(stdout),
                redirect_stderr(stderr),
            ):
                status = checker.main()
            return status, stdout.getvalue(), stderr.getvalue()

    def assert_invalid(self, data, message=None, raw=False):
        status, stdout, stderr = self.run_checker(data, raw=raw)
        self.assertEqual(status, 1)
        self.assertEqual(stdout, "")
        self.assertIn("Invalid audit report:", stderr)
        if message:
            self.assertIn(message, stderr)

    def test_valid_empty_pnpm9_report_passes(self):
        status, stdout, stderr = self.run_checker(report())
        self.assertEqual(status, 0)
        self.assertEqual(stdout, "Audit exceptions validated.\n")
        self.assertEqual(stderr, "")

    def test_valid_empty_vulnerabilities_shape_passes(self):
        data = report()
        data["vulnerabilities"] = data.pop("advisories")
        self.assertEqual(self.run_checker(data)[0], 0)

    def test_valid_nonempty_below_threshold_needs_no_exception(self):
        data = report({"1": advisory("low"), "2": advisory("moderate")}, low=1, moderate=1)
        self.assertEqual(self.run_checker(data)[0], 0)

    def test_high_and_critical_require_exceptions(self):
        for severity in ("high", "critical"):
            with self.subTest(severity=severity):
                data = report({"1": advisory(severity)}, **{severity: 1})
                status, stdout, stderr = self.run_checker(data)
                self.assertEqual(status, 1)
                self.assertEqual(stdout, "")
                self.assertIn("High/Critical vulnerabilities missing exceptions:", stderr)

    def test_valid_exception_expires_today_still_passes(self):
        data = report({"1": advisory()}, high=1)
        self.assertEqual(self.run_checker(data, exception_yaml())[0], 0)

    def test_expired_exception_fails(self):
        data = report({"1": advisory()}, high=1)
        status, _, stderr = self.run_checker(data, exception_yaml(expires_on="2026-10-16"))
        self.assertEqual(status, 1)
        self.assertIn("Exceptions expired:", stderr)

    def test_wrong_package_or_advisory_cannot_match(self):
        data = report({"1": advisory()}, high=1)
        for exception in (exception_yaml(package="other"), exception_yaml(advisory_id="GHSA-other")):
            with self.subTest(exception=exception):
                status, _, stderr = self.run_checker(data, exception)
                self.assertEqual(status, 1)
                self.assertIn("High/Critical vulnerabilities missing exceptions:", stderr)

    def test_exception_severity_must_match(self):
        status, _, stderr = self.run_checker(report({"1": advisory()}, high=1), exception_yaml(severity="critical"))
        self.assertEqual(status, 1)
        self.assertIn("Exception severity mismatch:", stderr)

    def test_bad_exception_date_missing_field_and_duplicate_fail(self):
        data = report({"1": advisory()}, high=1)
        cases = (
            (exception_yaml(expires_on="not-a-date"), "invalid expires_on date"),
            (exception_yaml().replace('    mitigation: "Untrusted input is blocked"\n', ""), "missing required fields"),
            (exception_yaml() + exception_yaml(), "Duplicate exception"),
        )
        for exception, message in cases:
            with self.subTest(message=message):
                status, _, stderr = self.run_checker(data, exception)
                self.assertEqual(status, 1)
                self.assertIn(message, stderr)

    def test_normalization_and_duplicate_advisories_preserve_policy(self):
        item = advisory(" HIGH ", module_name=" example-package ", github_advisory_id=" GHSA-AAAA-BBBB-CCCC ")
        data = report({"1": item, "2": copy.deepcopy(item)}, high=5)
        self.assertEqual(self.run_checker(data, exception_yaml())[0], 0)
        status, _, stderr = self.run_checker(data)
        self.assertEqual(status, 1)
        self.assertEqual(stderr.count("-  example-package "), 1)

    def test_metadata_counts_need_not_equal_distinct_advisory_counts(self):
        data = report({"1": advisory()}, high=7)
        self.assertEqual(self.run_checker(data, exception_yaml())[0], 0)

    def test_real_pnpm9_network_error_shape_fails(self):
        self.assert_invalid({"error": {
            "code": "ECONNREFUSED",
            "message": "request to http://127.0.0.1:9/-/npm/v1/security/audits failed, reason: connect ECONNREFUSED 127.0.0.1:9",
        }}, "audit command reported an error")

    def test_error_key_cannot_be_masked_by_success_shape(self):
        for key in ("error", "errors"):
            for value in (None, False, "", {}, [], ["registry unavailable"]):
                with self.subTest(key=key, value=value):
                    data = report()
                    data[key] = value
                    self.assert_invalid(data, "audit command reported an error")

    def test_unknown_or_nonobject_report_fails(self):
        for data in (None, [], "", 0, False, {}, {"metadata": report()["metadata"]}):
            with self.subTest(data=data):
                self.assert_invalid(data)

    def test_malformed_json_fails_without_traceback(self):
        for text in ("", "not json", '{"advisories":'):
            with self.subTest(text=text):
                self.assert_invalid(text, raw=True)

    def test_missing_or_malformed_metadata_fails(self):
        for metadata in (None, [], False, {}, {"vulnerabilities": []}, {"vulnerabilities": {}}):
            with self.subTest(metadata=metadata):
                data = report()
                data["metadata"] = metadata
                self.assert_invalid(data, "metadata.vulnerabilities")
        data = report()
        del data["metadata"]
        self.assert_invalid(data, "metadata.vulnerabilities")

    def test_missing_or_invalid_counts_fail(self):
        for severity in checker.SEVERITIES:
            for value in (None, True, False, -1, 1.5, "0", [], {}):
                with self.subTest(severity=severity, value=value):
                    data = report()
                    data["metadata"]["vulnerabilities"][severity] = value
                    self.assert_invalid(data, "count")
            data = report()
            del data["metadata"]["vulnerabilities"][severity]
            self.assert_invalid(data, "counts")
        data = report()
        data["metadata"]["vulnerabilities"]["unknown"] = 1
        self.assert_invalid(data, "count")

    def test_optional_total_count_is_validated(self):
        data = report()
        data["metadata"]["vulnerabilities"]["total"] = 0
        self.assertEqual(self.run_checker(data)[0], 0)
        data["metadata"]["vulnerabilities"]["total"] = True
        self.assert_invalid(data, "count")

    def test_positive_severe_counts_require_same_severity_evidence(self):
        for severity in ("high", "critical"):
            with self.subTest(severity=severity):
                self.assert_invalid(report(**{severity: 1}), "without advisory data")
                self.assert_invalid(report({"1": advisory("low")}, **{severity: 1}), "without advisory data")
                data = report(**{severity: 1})
                data["vulnerabilities"] = data.pop("advisories")
                self.assert_invalid(data, "without advisory data")

    def test_malformed_report_maps_cannot_be_ignored(self):
        for field in ("advisories", "vulnerabilities"):
            for value in (None, [], "", 0, False):
                with self.subTest(field=field, value=value):
                    data = report()
                    data[field] = value
                    self.assert_invalid(data, "must be an object")

    def test_malformed_advisory_records_fail(self):
        for value in (None, [], "", 0, False, {}):
            with self.subTest(value=value):
                self.assert_invalid(report({"1": value}, high=1))

    def test_invalid_package_severity_and_identity_fields_fail(self):
        for field, values in (
            ("module_name", (None, "", " ", 1, True, [], {})),
            ("severity", (None, "", " ", "unknown", 1, True, [], {})),
            ("github_advisory_id", (True, 1, [], {}, " ")),
            ("url", (True, 1, [], {})),
            ("title", (True, 1, [], {})),
            ("id", (True, -1, 1.2, [], {})),
            ("cves", ("CVE-2026-12345", [None], [True], [" "], {})),
        ):
            for value in values:
                with self.subTest(field=field, value=value):
                    self.assert_invalid(report({"1": advisory(**{field: value})}, high=1))
        data = report({"1": advisory()}, high=1)
        del data["advisories"]["1"]["severity"]
        self.assert_invalid(data, "missing severity")

    def test_missing_advisory_identifier_fails(self):
        self.assert_invalid(report({"1": {"module_name": "example-package", "severity": "high"}}, high=1), "missing advisory id")

    def test_existing_advisory_identifier_fallbacks_remain_supported(self):
        for field, value in (("url", "https://example.test/advisory"), ("cves", ["CVE-2026-12345"]), ("id", 1234567), ("title", "Advisory title")):
            with self.subTest(field=field):
                data = report({"1": {"module_name": "example-package", "severity": "high", field: value}}, high=1)
                identifier = value[0] if isinstance(value, list) else str(value)
                self.assertEqual(self.run_checker(data, exception_yaml(advisory_id=identifier))[0], 0)

    def test_valid_vulnerabilities_shape_preserves_exception_policy(self):
        data = vulnerability_report()
        self.assertEqual(self.run_checker(data, exception_yaml())[0], 0)
        status, _, stderr = self.run_checker(data)
        self.assertEqual(status, 1)
        self.assertIn("High/Critical vulnerabilities missing exceptions:", stderr)

    def test_via_numeric_source_and_string_reference_remain_supported(self):
        for via, identifier in (([{"source": 1234567}], "1234567"), (["dependency"], "dependency"), ("dependency", "dependency")):
            with self.subTest(via=via):
                self.assertEqual(self.run_checker(vulnerability_report(via), exception_yaml(advisory_id=identifier))[0], 0)

    def test_missing_empty_or_malformed_via_fails(self):
        for via in (None, [], "", " ", {}, 1, True, [None], [False], [1], [{}], [""], [" "]):
            with self.subTest(via=via):
                data = vulnerability_report()
                data["vulnerabilities"]["example-package"]["via"] = via
                self.assert_invalid(data)
        data = vulnerability_report()
        del data["vulnerabilities"]["example-package"]["via"]
        self.assert_invalid(data, "must contain advisory data")

    def test_malformed_vulnerabilities_record_or_name_fails(self):
        for value in (None, [], "", 0, False, {}):
            with self.subTest(value=value):
                data = vulnerability_report()
                data["vulnerabilities"]["example-package"] = value
                self.assert_invalid(data)
        data = vulnerability_report()
        data["vulnerabilities"][" "] = data["vulnerabilities"].pop("example-package")
        self.assert_invalid(data, "missing package name")
        for severity in (None, True, "unknown", ""):
            with self.subTest(severity=severity):
                data = vulnerability_report()
                data["vulnerabilities"]["example-package"]["severity"] = severity
                self.assert_invalid(data, "recognized severity")

    def test_malformed_via_advisory_fields_fail(self):
        for item in ({"source": True}, {"source": []}, {"source": -1}, {"title": 123}, {"url": {}}, {"source": 123, "severity": "unknown"}):
            with self.subTest(item=item):
                self.assert_invalid(vulnerability_report([item]))

    def test_via_cannot_hide_higher_severity_behind_lower_package_severity(self):
        self.assert_invalid(vulnerability_report([advisory("critical")], "high"), "understates a via advisory")
        self.assertEqual(self.run_checker(vulnerability_report([advisory("low")]), exception_yaml())[0], 0)


if __name__ == "__main__":
    unittest.main()
