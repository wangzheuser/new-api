"""Exercise the release gate against an isolated PostgreSQL log table."""

import csv
import io
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(
    os.environ.get(
        "GATE_SCRIPT",
        Path(__file__).resolve().parents[1] / "protocol-stability-gate.sh",
    )
)
CONTAINER = os.environ.get("GATE_TEST_POSTGRES", "")


@unittest.skipUnless(
    CONTAINER, "GATE_TEST_POSTGRES must name an isolated fixture container"
)
class ProtocolStabilityGateTest(unittest.TestCase):
    """Assert final request outcomes, rather than successful HTTP writes or billing."""

    @classmethod
    def setUpClass(cls):
        """Reject any container that is not explicitly isolated and marked as a fixture."""
        inspected = subprocess.run(
            ["docker", "inspect", CONTAINER], capture_output=True, text=True, check=True
        )
        container = json.loads(inspected.stdout)[0]
        labels = container["Config"].get("Labels") or {}
        if (
            not CONTAINER.startswith("new-api-gate-test-")
            or labels.get("com.new-api.test-fixture") != "protocol-stability"
            or container["HostConfig"]["NetworkMode"] != "none"
            or "/var/lib/postgresql/data"
            not in (container["HostConfig"].get("Tmpfs") or {})
        ):
            raise RuntimeError(
                "refusing database reset: not an isolated protocol-stability fixture"
            )

    def setUp(self):
        """Reset only the dedicated fixture table before each scenario."""
        self.psql(
            "DROP TABLE IF EXISTS logs; CREATE TABLE logs (id bigserial PRIMARY KEY, request_id text, created_at bigint, channel_id int, model_name text, type int, is_stream boolean, is_intermediate boolean, other text);"
        )
        self.rows = []
        for window, epoch in (("pre", 950), ("post", 1050)):
            for path in (
                "/v1/messages",
                "/v1/responses",
                "/v1/chat/completions",
                "/v1beta/models/test:generateContent",
            ):
                for stream in (False, True):
                    for _ in range(2):
                        self.rows.append(
                            {
                                "request_id": f"{window}-{len(self.rows)}",
                                "created_at": epoch,
                                "channel_id": 1,
                                "type": 2,
                                "is_stream": stream,
                                "is_intermediate": False,
                                "other": {
                                    "request_path": path,
                                    "upstream_model_name": "fixture-upstream",
                                    "stream_status": {
                                        "status": "ok",
                                        "end_reason": "eof",
                                        "terminal_seen": False,
                                    },
                                },
                            }
                        )

    def psql(self, sql):
        """Run real SQL in a disposable, network-isolated database container."""
        return subprocess.run(
            [
                "docker",
                "exec",
                "-i",
                CONTAINER,
                "psql",
                "-X",
                "-v",
                "ON_ERROR_STOP=1",
                "-U",
                "postgres",
                "-d",
                "postgres",
            ],
            input=sql,
            text=True,
            capture_output=True,
            check=True,
        ).stdout

    def run_gate(self, app_logs=None, upstream_evidence=None, **options):
        """Insert explicit records and execute the public gate CLI."""
        values = []
        for row in self.rows:
            encoded = json.dumps(row["other"]).replace("'", "''")
            values.append(
                f"('{row['request_id']}',{row['created_at']},{row['channel_id']},'fixture-client',{row['type']},{str(row['is_stream']).lower()},{str(row['is_intermediate']).lower()},'{encoded}')"
            )
        if values:
            self.psql(
                "INSERT INTO logs(request_id,created_at,channel_id,model_name,type,is_stream,is_intermediate,other) VALUES "
                + ",".join(values)
                + ";"
            )
        with tempfile.TemporaryDirectory(prefix="new-api-gate-test-") as temp:
            env = Path(temp) / "server.env"
            env.write_text(
                f"POSTGRES_CONTAINER={CONTAINER}\nPOSTGRES_USER=postgres\nPOSTGRES_DB=postgres\n"
            )
            output = Path(temp) / "result"
            args = [
                "bash",
                str(SCRIPT),
                "--channel-id",
                "1",
                "--cutover-epoch",
                "1000",
                "--output-dir",
                str(output),
                "--seconds",
                "100",
                "--drain-seconds",
                "0",
                "--settle-seconds",
                "10",
                "--min-requests",
                "2",
            ]
            if app_logs is not None:
                for window, flag in (
                    ("pre", "--baseline-log"),
                    ("post", "--observation-log"),
                ):
                    log = Path(temp) / (window + ".log")
                    log.write_text(app_logs[window])
                    args += [flag, str(log)]
            if upstream_evidence is not None:
                evidence = Path(temp) / "upstream-evidence.json"
                evidence.write_text(json.dumps(upstream_evidence))
                args += ["--verified-upstream-503-file", str(evidence)]
            for key, value in options.items():
                args += ["--" + key.replace("_", "-"), str(value)]
            result = subprocess.run(
                args,
                env={**os.environ, "SERVER_ENV": str(env)},
                capture_output=True,
                text=True,
                check=False,
            )
            files = {p.name: p.read_text() for p in output.glob("*") if p.is_file()}
            self.assertNotIn("ERROR:", result.stderr, result.stderr)
            return result, files

    def test_normal_legacy_eof_and_all_protocols_pass(self):
        """Normal EOF remains valid without a synthetic terminal event."""
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_partial_billing_is_not_success(self):
        """Billing a partial stream must not turn its failure into gate success."""
        for row in self.rows:
            if (
                row["created_at"] > 1000
                and row["other"]["request_path"] == "/v1/messages"
                and row["is_stream"]
            ):
                row["other"]["stream_status"] = {
                    "status": "error",
                    "billing_finalization": "settled_partial",
                }
                row["is_stream"] = True
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 3, result.stdout)
        # Two-request cohorts retain the failure signal but do not establish a rate regression.

    def test_nonstream_success_does_not_hide_stream_regression(self):
        """A protocol's total rate cannot mask its streaming cohort dropping."""
        # Aggregate Messages success stays 50%; streaming falls 100% -> 0%.
        for row in self.rows:
            if row["other"]["request_path"] == "/v1/messages" and (
                (row["created_at"] < 1000 and not row["is_stream"])
                or (row["created_at"] > 1000 and row["is_stream"])
            ):
                row["type"] = 5
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 3, result.stdout)
        # Two-request cohorts retain the failure signal but do not establish a rate regression.

    def test_chat_failure_is_not_omitted(self):
        """Chat failures are part of the four-protocol release surface."""
        for row in self.rows:
            if (
                row["created_at"] > 1000
                and row["other"]["request_path"] == "/v1/chat/completions"
            ):
                row["type"] = 5
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 3, result.stdout)
        # Two-request cohorts retain the failure signal but do not establish a rate regression.

    def test_retry_then_success_is_one_final_request(self):
        """Intermediate failures and a later channel success do not double count."""
        final = self.rows[-1]
        retry = {
            **final,
            "created_at": 1049,
            "type": 5,
            "is_intermediate": True,
            "other": dict(final["other"]),
        }
        final["channel_id"] = 2
        self.rows.insert(0, retry)
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_missing_final_record_fails(self):
        """Unsettled requests are evidence gaps, not zero-cost successes."""
        self.rows[-1]["type"] = 5
        self.rows[-1]["is_intermediate"] = True
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 1, result.stdout)

    def test_sparse_cohort_is_reported_separately(self):
        """A sparse cohort is not claimed covered while populated cohorts remain comparable."""
        self.rows.pop()
        result, files = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("insufficient_samples", files["comparison.tsv"])

    def test_settlement_deadline_excludes_late_success(self):
        """A final record after the settle allowance cannot repair this window."""
        row = self.rows[-1]
        self.rows.insert(0, {**row, "type": 5, "is_intermediate": True})
        row["created_at"] = 1111
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 1, result.stdout)

    def test_unknown_stream_status_is_not_success(self):
        """A charged stream without a recorded terminal status is not verified success."""
        self.rows[-1]["other"].pop("stream_status")
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 3, result.stdout)
        # Two-request cohorts retain the failure signal but do not establish a rate regression.

    def test_cancellation_and_rate_limit_are_reported_not_waived(self):
        """Cancellation/429 counters remain visible and cannot pass a sparse comparison."""
        row = self.rows[-1]
        row["type"] = 5
        row["other"].update(
            status_code=429,
            stream_status={"status": "error", "end_reason": "client_gone"},
        )
        result, files = self.run_gate()
        self.assertEqual(result.returncode, 3, result.stdout)
        # Two-request cohorts retain the failure signal but do not establish a rate regression.
        counts = list(
            csv.DictReader(
                io.StringIO(files["final-request-success.tsv"]), delimiter="\t"
            )
        )
        self.assertEqual(sum(int(r["client_canceled"]) for r in counts), 1)
        self.assertEqual(sum(int(r["rate_limited"]) for r in counts), 1)

    def test_query_strings_and_gemini_stream_endpoint_share_protocol(self):
        """Query strings and Gemini stream operation names do not create new protocols."""
        for row in self.rows:
            path = row["other"]["request_path"]
            if ":generateContent" in path and row["is_stream"]:
                path = path.replace(":generateContent", ":streamGenerateContent")
            row["other"]["request_path"] = path + "?beta=true"
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_model_regression_is_not_hidden_by_another_model(self):
        """A fixed client alias can still cover distinct actual upstream models."""
        additions = []
        for row in self.rows:
            if row["other"]["request_path"] == "/v1/messages":
                other = {
                    **row,
                    "request_id": row["request_id"] + "-second",
                    "other": {**row["other"], "upstream_model_name": "second-model"},
                }
                row["type"] = 5 if row["created_at"] > 1000 else 2
                other["type"] = 2 if row["created_at"] > 1000 else 5
                additions.append(other)
        self.rows.extend(additions)
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 3, result.stdout)
        # Two-request cohorts retain the failure signal but do not establish a rate regression.

    def test_cross_window_retry_is_not_counted_twice(self):
        """The first observed attempt assigns a request to one measurement window."""
        self.rows.append({**self.rows[0], "created_at": 1005})
        result, files = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)
        counts = list(
            csv.DictReader(
                io.StringIO(files["final-request-success.tsv"]), delimiter="\t"
            )
        )
        self.assertEqual(sum(int(r["final_requests"]) for r in counts), 32)

    def test_all_channel_mode_still_resolves_retries_globally(self):
        """Release mode covers all channels without duplicating a retried request."""
        final = self.rows[-1]
        self.rows.insert(
            0, {**final, "created_at": 1049, "type": 5, "is_intermediate": True}
        )
        final["channel_id"] = 2
        result, files = self.run_gate(channel_id=0)
        self.assertEqual(result.returncode, 0, result.stderr)
        counts = list(
            csv.DictReader(
                io.StringIO(files["final-request-success.tsv"]), delimiter="\t"
            )
        )
        self.assertEqual(sum(int(r["final_requests"]) for r in counts), 32)

    def test_zero_sample_requirement_is_rejected(self):
        """Operators cannot turn an empty cohort into successful evidence."""
        result, _ = self.run_gate(min_requests=0)
        self.assertEqual(result.returncode, 2, result.stdout)

    def app_logs(self):
        """Render the gateway's access log format without production identities or payloads."""
        logs = {"pre": "", "post": ""}
        for row in self.rows:
            window = "pre" if row["created_at"] < 1000 else "post"
            logs[window] += (
                f"[GIN] fixture | relay | {row['request_id']} | 200 | 1ms | 127.0.0.1 | POST {row['other']['request_path']}\n"
            )
        return logs

    def test_slot_logs_exclude_unrelated_shared_database_requests(self):
        """Shared database traffic outside the observed containers cannot dilute the gate."""
        logs = self.app_logs()
        self.rows.append({**self.rows[-1], "request_id": "not-in-slot-log", "type": 5})
        result, _ = self.run_gate(app_logs=logs)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_access_window_keeps_final_record_before_database_boundary(self):
        """The observed request ID, not DB write timing, owns the window."""
        logs = self.app_logs()
        self.rows[0]["created_at"] = 899
        result, files = self.run_gate(app_logs=logs)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("<unrecorded>", files["final-request-coverage.tsv"])

    def test_distributor_rejection_is_not_pending_settlement(self):
        """A known pre-upstream failure stays visible without inventing consumption."""
        logs = self.app_logs()
        logs["post"] += (
            "[ERR] fixture | invalid-model | user 1 | No available channel for model bad-model under group test (distributor)\n"
            "[GIN] fixture | relay | invalid-model | 503 | 1ms | 127.0.0.1 | POST /v1/messages\n"
        )
        result, files = self.run_gate(app_logs=logs)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(files["http-observations.json"])
        self.assertEqual(report["post"]["/v1/messages:pre_upstream_rejected"], 1)

    def test_policy_marker_requires_matching_request_and_403(self):
        """Keep raw counts; only a same-request local policy marker creates evidence."""
        logs = self.app_logs()
        logs["post"] += (
            "[INFO] fixture | denied | relay_policy_rejection reason=auth_group_not_allowed status=403\n"
            "[GIN] fixture | relay | denied | 403 | 1ms | local | POST /v1/messages\n"
            "[GIN] fixture | relay | unknown | 403 | 1ms | local | POST /v1/messages\n"
            "[INFO] fixture | error | relay_policy_rejection reason=auth_group_not_allowed status=403\n"
            "[GIN] fixture | relay | error | 500 | 1ms | local | POST /v1/messages\n"
        )
        result, files = self.run_gate(app_logs=logs)
        self.assertNotEqual(result.returncode,0)
        report = json.loads(files["http-observations.json"])
        self.assertEqual(report["post"]["/v1/messages:http_403"],2)
        self.assertEqual(report["post"]["/v1/messages:http_500"],1)
        self.assertEqual(report["policy_rejections"]["post"],[
            dict(request_id="denied",path="/v1/messages",status=403,reason="auth_group_not_allowed")])

    def test_only_reviewed_transport_503_is_excluded(self):
        """External cause review and actual transport evidence are both mandatory."""
        source = dict(self.rows[-1])
        source.update(request_id="external-503", type=5, is_stream=False)
        source["other"] = {
            "request_path": "/v1/messages", "status_code": 503,
            "upstream_model_name": "fixture-upstream",
            "admin_info": {"upstream_status_code": 503},
        }
        self.rows.append(source)
        logs = self.app_logs()
        logs["post"] = logs["post"].replace("external-503 | 200", "external-503 | 503")
        evidence = [{"request_id": "external-503", "evidence": "fixture provider outage independently confirmed"}]
        result, files = self.run_gate(app_logs=logs, upstream_evidence=evidence)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(files["verified-upstream-counts.txt"], "0 1\n")
        self.assertEqual(json.loads(files["http-observations.json"])["post"]["/v1/messages:http_503"], 1)

    def test_reviewed_sse_503_preserves_http_and_failure_audit(self):
        """A reviewed upstream rejection after HTTP commit is not an HTTP 5xx."""
        row = dict(self.rows[-1])
        row.update(request_id="external-sse-503", type=5, is_stream=True)
        row["other"] = {
            "request_path": "/v1/messages", "status_code": 503,
            "upstream_model_name": "fixture-upstream",
            "admin_info": {"upstream_status_code": 503},
            "stream_status": {"status": "error", "app_http_committed": True,
                              "client_payload_committed": False},
        }
        self.rows.append(row)
        result, files = self.run_gate(app_logs=self.app_logs(), upstream_evidence=[{
            "request_id": row["request_id"], "evidence": "same upstream failure independently reproduced on old slot",
        }])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(files["verified-upstream-counts.txt"], "0 0\n")
        audit = list(csv.DictReader(io.StringIO(files["verified-upstream-503.tsv"]), delimiter="\t"))
        self.assertEqual(audit, [{"period": "post", "request_id": row["request_id"], "http_status": "200"}])
        counts = json.loads(files["http-observations.json"])["post"]
        self.assertEqual(counts["/v1/messages:http_200"], 5)
        self.assertNotIn("/v1/messages:http_503", counts)

    def test_sse_exception_requires_complete_failure_evidence(self):
        """Missing review, source receipt or uncommitted error state must block."""
        for change in (
            {"admin_info": {}},
            {"stream_status": {"status": "ok", "app_http_committed": True}},
            {"stream_status": {"status": "error"}},
            {"stream_status": {"status": "error", "app_http_committed": True, "client_payload_committed": True}},
            {"stream_status": {"status": "error", "app_http_committed": True, "billing_finalization": "settled_partial"}},
            {"error_code": "convert_request_failed"},
        ):
            with self.subTest(change=change):
                self.setUp()
                row = self.rows[-1]
                row.update(type=5, is_stream=True)
                row["other"].update(status_code=503, admin_info={"upstream_status_code": 503},
                                    stream_status={"status": "error", "app_http_committed": True})
                row["other"].update(change)
                result, _ = self.run_gate(app_logs=self.app_logs(), upstream_evidence=[{
                    "request_id": row["request_id"], "evidence": "fixture external failure review",
                }])
                self.assertNotEqual(result.returncode, 0)

    def test_public_503_without_transport_proof_is_not_exempt(self):
        """An error mapping to 503 is not evidence that upstream caused the failure."""
        row = self.rows[-1]
        row.update(type=5, is_stream=False)
        row["other"].update(status_code=503)
        logs = self.app_logs()
        logs["post"] = logs["post"].replace(row["request_id"] + " | 200", row["request_id"] + " | 503")
        result, _ = self.run_gate(app_logs=logs, upstream_evidence=[{
            "request_id": row["request_id"], "evidence": "unverified claim",
        }])
        self.assertNotEqual(result.returncode, 0)

    def test_distributor_message_cannot_hide_unknown_500(self):
        """A routing message does not excuse an unrelated internal HTTP failure."""
        logs = self.app_logs()
        logs["post"] += (
            "[ERR] fixture | invalid | user 1 | No available channel for model bad under group test (distributor)\n"
            "[GIN] fixture | relay | invalid | 500 | 1ms | 127.0.0.1 | POST /v1/messages\n"
        )
        result, _ = self.run_gate(app_logs=logs)
        self.assertEqual(result.returncode, 1)

    def test_upstream_receipt_cannot_exempt_partial_or_local_failures(self):
        """Transport evidence cannot waive a committed stream, conversion error or late final record."""
        for change in (
            {"stream_status": {"client_payload_committed": True}},
            {"stream_status": {"billing_finalization": "settled_partial"}},
            {"error_code": "convert_request_failed"},
            {"created_at": 9999},
        ):
            with self.subTest(change=change):
                self.setUp()
                row = self.rows[-1]
                row.update(type=5, is_stream=False)
                row["other"].update(status_code=503, admin_info={"upstream_status_code": 503})
                logs = self.app_logs()
                logs["post"] = logs["post"].replace(row["request_id"] + " | 200", row["request_id"] + " | 503")
                if "created_at" in change:
                    row["created_at"] = change["created_at"]
                else:
                    row["other"].update(change)
                result, _ = self.run_gate(app_logs=logs, upstream_evidence=[{
                    "request_id": row["request_id"], "evidence": "fixture reviewed upstream outage",
                }])
                self.assertNotEqual(result.returncode, 0)

    def test_http_200_without_final_record_is_not_success(self):
        """Access logs expose silent record gaps that database-only coverage misses."""
        logs = self.app_logs()
        logs["post"] += (
            "[GIN] fixture | relay | no-final | 200 | 1ms | 127.0.0.1 | POST /v1/messages\n"
        )
        result, _ = self.run_gate(app_logs=logs)
        self.assertEqual(result.returncode, 1, result.stdout)

    def test_explicit_cancellation_is_separate_from_missing_final_record(self):
        """A canceled unlogged request is reported, never added to successful consumption."""
        logs = self.app_logs()
        logs["post"] += (
            "[INFO] fixture | canceled-request | relay canceled by client\n[GIN] fixture | relay | canceled-request | 200 | 1ms | 127.0.0.1 | POST /v1/messages\n"
        )
        result, files = self.run_gate(app_logs=logs)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(files["http-observations.json"])
        self.assertEqual(report["post"]["/v1/messages:client_canceled"], 1)
        rows = list(
            csv.DictReader(
                io.StringIO(files["final-request-success.tsv"]), delimiter="\t"
            )
        )
        self.assertEqual(sum(int(r["successes"]) for r in rows), 32)

    def test_static_and_rate_limit_changes_remain_separate(self):
        """Static traffic and local 429s do not inflate final-business success samples."""
        logs = self.app_logs()
        logs["post"] += (
            "[GIN] fixture | web | asset | 200 | 1ms | 127.0.0.1 | GET /assets/main.js\n[GIN] fixture | relay | limited | 429 | 1ms | 127.0.0.1 | POST /v1/messages\n"
        )
        result, files = self.run_gate(app_logs=logs)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(files["http-observations.json"])
        self.assertEqual(report["post"]["/v1/messages:http_429"], 1)
        self.assertNotIn("/assets/main.js:http_200", report["post"])

    def test_unknown_upstream_error_model_is_evidence_gap(self):
        """Errors without historical model evidence cannot prove per-model stability."""
        row = self.rows[-1]
        row["type"] = 5
        row["other"].pop("upstream_model_name")
        result, files = self.run_gate()
        self.assertEqual(result.returncode, 3, result.stdout)
        # Two-request cohorts retain the failure signal but do not establish a rate regression.
        self.assertIn("legacy_client_model", files["comparison.tsv"])

    def test_native_consumption_uses_existing_unmapped_model_contract(self):
        """Successful unmapped logs already identify the actual model by model_name."""
        for row in self.rows:
            row["other"].pop("upstream_model_name")
        result, _ = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_canceled_intermediate_attempt_is_not_pending_settlement(self):
        """An explicit cancellation resolves the coverage gap without inventing success."""
        logs = self.app_logs()
        final = self.rows[-1]
        final["type"] = 5
        final["is_intermediate"] = True
        logs["post"] += (
            f"[INFO] fixture | {final['request_id']} | relay canceled by client\n"
        )
        result, files = self.run_gate(app_logs=logs)
        # Explicit cancellation leaves a reported coverage gap, not an invented failure.
        self.assertEqual(result.returncode, 0, result.stdout)
        coverage = list(
            csv.DictReader(
                io.StringIO(files["final-request-coverage.tsv"]), delimiter="\t"
            )
        )
        self.assertEqual(sum(int(row["unresolved_requests"]) for row in coverage), 0)


if __name__ == "__main__":
    unittest.main()
