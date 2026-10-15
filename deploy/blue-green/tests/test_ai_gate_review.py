"""Verify redacted AI gate evidence and its deterministic trust boundary."""

import json
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "ai-gate-review.py"


class AiGateReviewTest(unittest.TestCase):
    """Keep AI attribution advisory and bound to machine evidence."""

    def run_build(self, post_error=False):
        """Build a review package from a pair of redacted application logs."""
        with tempfile.TemporaryDirectory(prefix="new-api-ai-review-") as temp:
            root = Path(temp)
            (root / "observation.result").write_text(
                "observation=failed release_id=r production=candidate version=v requested_seconds=600 elapsed_seconds=600\n"
            )
            (root / "observation.metrics").write_text(
                "baseline_samples=10 baseline_errors_5xx=1 samples=10 errors_5xx=2 "
                "allowed_errors_5xx=2 actionable_errors_5xx=1 actionable_allowed_errors_5xx=1 "
                "protocol_stability_rc=3\n"
            )
            (root / "gate.result").write_text("gate=passed candidate=candidate version=v\n")
            (root / "bounded.result").write_text("result=evidence_inconclusive observation_exit=3 action=hold\n")
            (root / "public-browser.exit").write_text("0\n")
            baseline = (
                "[ERR] | req1 | user 1 | No available channel for model x under group g (distributor)\n"
                "[GIN] | t | req1 | 503 | 1ms | ip | POST /v1/messages\n"
            )
            candidate = baseline
            if post_error:
                candidate += "[ERR] | req2 | user 1 | convert_request_failed\n[GIN] | t | req2 | 500 | 1ms | ip | POST /v1/messages\n"
            (root / "observation-baseline-app.log").write_text(baseline)
            (root / "observation-app.log").write_text(candidate)
            output = root / "input.json"
            result = subprocess.run(
                ["python3", str(SCRIPT), "build", "--state-dir", str(root), "--output", str(output)],
                capture_output=True, text=True, check=False,
            )
            return result, json.loads(output.read_text())

    def test_matching_pre_upstream_rejection_is_eligible(self):
        result, evidence = self.run_build()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(evidence["eligibility"]["external_incident"])
        self.assertEqual(evidence["fingerprints"][0]["category"], "pre_upstream_rejected")

    def test_new_conversion_error_is_not_eligible(self):
        result, evidence = self.run_build(post_error=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(evidence["eligibility"]["external_incident"])

    def test_verdict_schema_and_machine_boundary(self):
        with tempfile.TemporaryDirectory(prefix="new-api-ai-validate-") as temp:
            root = Path(temp)
            build_result, evidence = self.run_build()
            self.assertEqual(build_result.returncode, 0)
            evidence_path = root / "input.json"
            evidence_path.write_text(json.dumps(evidence))
            verdict = root / "verdict.json"
            verdict.write_text(json.dumps({
                "verdict": "external_incident", "confidence": 0.9,
                "reason": "基线和候选均为选路前拒绝", "evidence_refs": ["fingerprints"],
                "required_action": "finalize",
            }))
            output = root / "result.json"
            result = subprocess.run(
                ["python3", str(SCRIPT), "validate", "--input", str(evidence_path),
                 "--verdict-file", str(verdict), "--output", str(output)],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            invalid = json.loads(verdict.read_text())
            invalid["confidence"] = 2
            verdict.write_text(json.dumps(invalid))
            result = subprocess.run(
                ["python3", str(SCRIPT), "validate", "--input", str(evidence_path),
                 "--verdict-file", str(verdict), "--output", str(output)],
                capture_output=True, text=True, check=False,
            )
            self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
