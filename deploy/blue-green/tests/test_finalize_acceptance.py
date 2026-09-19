"""Verify explicit finalization of a fully observed but inconclusive release."""

import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "release-remote.sh"


class FinalizeAcceptanceTest(unittest.TestCase):
    """Keep statistical evidence immutable while allowing audited operator disposition."""

    def invoke(self, observation="inconclusive", args=("--dry-run",), accept=False,
               reason="低流量但健康和硬门禁均通过"):
        """Run action_finalize with deterministic Docker and release state fixtures."""
        with tempfile.TemporaryDirectory(prefix="new-api-finalize-test-") as temp:
            root = Path(temp)
            state = root / "state"
            state.mkdir()
            (state / "role-state.env").write_text(
                "NEW=candidate\nOLD=standby\n"
            )
            (state / "observation.result").write_text(
                f"observation={observation} release_id=fixture production=candidate "
                "version=fixture-version requested_seconds=600 elapsed_seconds=600\n"
            )
            definitions = SCRIPT.read_text().split('ACTION="${1:-}"')[0]
            (root / "definitions.sh").write_text(definitions)
            harness = r'''source "$1/definitions.sh"
STATE_DIR="$1/state"
RELEASE_DIR="$1"
SCRIPT_DIR="$1"
RELEASE_ID=fixture
VERSION=fixture-version
load_config() { :; }
proxy_version() { echo fixture-version; }
public_version() { echo fixture-version; }
sleep() { :; }
docker() {
  case "$1 $2 $3" in
    "inspect -f {{.State.Health.Status}}") echo healthy ;;
    "inspect -f {{.HostConfig.RestartPolicy.Name}}") echo unless-stopped ;;
    "inspect -f {{.State.Status}}") echo exited ;;
    "inspect -f {{.RestartCount}}") echo 0 ;;
    "inspect -f {{.State.OOMKilled}}") echo false ;;
    "update --restart=unless-stopped") : > "$1-update" ;;
    "stop --time 30") : > "$1-stop" ;;
    *) return 0 ;;
  esac
}
shift
action_finalize "$@"
'''
            env = {**os.environ}
            if accept:
                env.update(ACCEPT_INCONCLUSIVE="1", ACCEPT_REASON=reason,
                           CONFIRM_FINALIZE="fixture")
            result = subprocess.run(
                ["bash", "-c", harness, "fixture", str(root), *args],
                capture_output=True,
                text=True,
                env=env,
            )
            decision = state / "decision.result"
            final = state / "final.result"
            return result, (state / "observation.result").read_text(), decision.exists(), final.exists()

    def test_inconclusive_requires_explicit_acceptance(self):
        """An evidence gap cannot finalize through the ordinary path."""
        result, _, decision, final = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("acceptance_required=1", result.stderr)
        self.assertFalse(decision)
        self.assertFalse(final)

    def test_inconclusive_acceptance_is_dry_run_safe(self):
        """Dry-run validates the explicit disposition without writing a terminal record."""
        result, observation, decision, final = self.invoke(
            args=("--accept-inconclusive", "--dry-run"), accept=True
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("decision=accepted_inconclusive", result.stdout)
        self.assertIn("observation=inconclusive", observation)
        self.assertFalse(decision)
        self.assertFalse(final)

    def test_inconclusive_acceptance_records_decision_without_rewriting_observation(self):
        """Execution records the operator decision and keeps the original evidence state."""
        result, observation, decision, final = self.invoke(
            args=("--accept-inconclusive", "--execute"), accept=True
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("decision=accepted_inconclusive", result.stdout)
        self.assertIn("observation=inconclusive", observation)
        self.assertTrue(decision)
        self.assertTrue(final)

    def test_failed_observation_cannot_be_accepted(self):
        """Human acceptance is limited to evidence gaps, never deterministic failures."""
        result, _, decision, final = self.invoke(
            observation="failed", args=("--accept-inconclusive", "--dry-run"), accept=True
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("observation_failed", result.stderr)
        self.assertFalse(decision)
        self.assertFalse(final)

    def test_acceptance_requires_a_bounded_reason(self):
        """An audited acceptance cannot omit or overflow its operator rationale."""
        result, _, decision, final = self.invoke(
            args=("--accept-inconclusive", "--dry-run"), accept=True, reason=""
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("accept_reason_required", result.stderr)
        self.assertFalse(decision)
        self.assertFalse(final)


if __name__ == "__main__":
    unittest.main()
