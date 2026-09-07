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

    def run_observe(self, protocol_rc=0, http_code=200, unhealthy=False):
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
                f'printf "%s\\n" "$@" > "{root}/protocol-args"\nexit {protocol_rc}\n'
            )
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
    local code=200
    [[ "$*" == *candidate ]] && code="$TEST_HTTP_CODE"
    local index
    for index in {1..10}; do
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
            "errors_5xx=10",
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


if __name__ == "__main__":
    unittest.main()
