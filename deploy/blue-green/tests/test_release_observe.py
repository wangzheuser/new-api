"""Exercise observation orchestration with fake external commands and a virtual clock."""

import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(
    os.environ.get(
        "RELEASE_SCRIPT", Path(__file__).resolve().parents[1] / "release-remote.sh"
    )
)


class ReleaseObserveTest(unittest.TestCase):
    """Protect failure evidence and independent business/HTTP gates."""

    def run_observe(self, protocol_rc=0, http_code=200, unhealthy=False, verified=0, allow=False, valid=True, baseline_empty=False):
        """Run the real function; replace only Docker, configuration, time and child gate."""
        with tempfile.TemporaryDirectory(prefix="new-api-observe-test-") as temp:
            root = Path(temp)
            state = root / "state"
            state.mkdir()
            backup = root / "backup" / "fixture"
            backup.mkdir(parents=True)
            (backup / "nginx-config.sha256").write_text("fixture-hash\n")
            (state / "role-state.env").write_text(
                "NEW=candidate\nOLD=production\nCUTOVER_AT=2026-09-07T00:00:00Z\nCUTOVER_EPOCH=1000\n"
            )
            # Source definitions without dispatch, so real infrastructure is never addressed.
            definitions = SCRIPT.read_text().split('ACTION="${1:-}"')[0]
            (root / "release-definitions.sh").write_text(definitions)
            (root / "protocol-stability-gate.sh").write_text(
                f'printf "%s\\n" "$@" > "{root}/protocol-args"\n'
                'while [[ $# -gt 0 ]]; do if [[ "$1" == --output-dir ]]; then out="$2"; break; fi; shift; done\n'
                f'mkdir -p "$out"; printf "0 {verified}\\n" > "$out/verified-upstream-counts.txt"\nexit {protocol_rc}\n'
            )
            (root / "low-traffic-evidence.py").write_text("raise SystemExit(" + ("0" if valid else "1") + ")\n")
            harness = r"""source "$1/release-definitions.sh"
STATE_DIR="$1/state"
BACKUP_ROOT="$1/backup"
SCRIPT_DIR="$1"
SERVER_ENV="$1/server.env"
RELEASE_ID=fixture
VERSION=fixture-version
load_config() { :; }
public_version() { echo fixture-version; }
proxy_version() { echo fixture-version; }
nginx_hash_matches() { return 0; }
sleep() { SECONDS=$((SECONDS + $1)); }
date() {
  if [[ "$*" == *+%s* ]]; then echo 1000;
  else echo 2026-09-07T00:00:00Z; fi
}
docker() {
  if [[ "$1" == inspect ]]; then
    case "$3" in
      *Health.Status*) echo "${TEST_HEALTH}" ;;
      *RestartCount*) echo 0 ;;
      *OOMKilled*) echo false ;;
    esac
  elif [[ "$1" == logs ]]; then
    [[ "$TEST_BASELINE_EMPTY" == 1 && "$*" == *production ]] && return 0
    local code=200
    local index
    for index in {1..10}; do
      code=200
      [[ "$*" == *candidate && "$index" -le 2 ]] && code="$TEST_HTTP_CODE"
      printf '[GIN] fixture | relay | fixture-%s | %s | 1ms | 127.0.0.1 | POST /v1/messages\n' "$index" "$code"
    done
  fi
}
action_observe --seconds 600 --interval 30
"""
            result = subprocess.run(
                ["bash", "-c", harness, "fixture", str(root)],
                env={
                    **os.environ,
                    "TEST_HTTP_CODE": str(http_code),
                    "ALLOW_LOW_TRAFFIC_RELEASE": "1" if allow else "0",
                    "TEST_BASELINE_EMPTY": "1" if baseline_empty else "0",
                    "TEST_HEALTH": "unhealthy" if unhealthy else "healthy",
                },
                text=True,
                capture_output=True,
                check=False,
            )
            evidence = (state / "observation.result").read_text()
            args = (
                (root / "protocol-args").read_text()
                if (root / "protocol-args").exists()
                else ""
            )
            return result, evidence, args

    def test_failure_after_window_retains_timing_and_checks(self):
        """A failed HTTP gate still records the actual completed observation window."""
        result, evidence, _ = self.run_observe(http_code=503)
        self.assertEqual(result.returncode, 1, result.stderr)
        for field in (
            "observation=failed",
            "requested_seconds=600",
            "elapsed_seconds=600",
            "checks=21",
            "errors_5xx=2",
            "start=",
            "end=",
        ):
            self.assertIn(field, evidence)

    def test_early_failure_retains_zero_check_evidence(self):
        """An unhealthy first sample cannot claim a completed ten-minute observation."""
        result, evidence, _ = self.run_observe(unhealthy=True)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("checks=0", evidence)
        self.assertIn("elapsed_seconds=0", evidence)

    def test_business_gate_failure_blocks_http_success(self):
        """HTTP 200 cannot waive a failed final-business-result gate."""
        result, evidence, args = self.run_observe(protocol_rc=1)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("observation=failed", evidence)
        self.assertIn("--settle-seconds\n300", args)

    def test_both_gates_pass_and_use_equal_windows(self):
        """The child gate gets the measured release window, not its three-hour default."""
        result, evidence, args = self.run_observe()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("observation=passed", evidence)
        self.assertIn("elapsed_seconds=600", evidence)
        self.assertIn("--seconds\n600", args)
        self.assertIn("--cutover-epoch\n1000", args)

    def test_verified_external_503_does_not_trigger_rollback_gate(self):
        """Reviewed external failures remain raw errors but not regression failures."""
        result, evidence, _ = self.run_observe(http_code=503, verified=2)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("observation=passed", evidence)
        self.assertIn("errors_5xx=2", evidence)

    def test_protocol_uncertainty_preserves_distinct_exit(self):
        """Insufficient evidence neither finalizes nor claims a candidate fault."""
        result, evidence, _ = self.run_observe(protocol_rc=3)
        self.assertEqual(result.returncode, 3)
        self.assertIn("observation=inconclusive", evidence)
        self.assertIn("elapsed_seconds=600", evidence)

    def test_http_failure_is_not_waived_by_protocol_uncertainty(self):
        """A hard HTTP regression takes precedence over a protocol hold."""
        result, evidence, _ = self.run_observe(protocol_rc=3, http_code=503)
        self.assertEqual(result.returncode, 1)
        self.assertIn("observation=failed", evidence)

    def test_empty_baseline_needs_explicit_delivery_evidence(self):
        self.assertEqual(self.run_observe(protocol_rc=3, baseline_empty=True)[0].returncode, 3)
        result, evidence, _ = self.run_observe(protocol_rc=3, baseline_empty=True, allow=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("evidence_mode=verified_low_traffic", evidence)

    def test_low_traffic_mode_cannot_hide_errors_or_missing_probes(self):
        for kwargs in ({"protocol_rc":1}, {"protocol_rc":3,"http_code":503}, {"protocol_rc":3,"valid":False}):
            self.assertNotEqual(self.run_observe(allow=True, **kwargs)[0].returncode, 0)


if __name__ == "__main__":
    unittest.main()
