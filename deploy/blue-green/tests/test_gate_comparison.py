"""Exercise the gate comparison against explicit final-outcome TSV contracts."""
import csv
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(os.environ.get("GATE_SCRIPT", Path(__file__).resolve().parents[1] / "protocol-stability-gate.sh"))


class GateComparisonTest(unittest.TestCase):
    """Protect comparable history and distinguish absent coverage from failures."""

    def compare(self, rows, unresolved=0, gap_window="post"):
        """Run the production comparison stage without Docker or production data."""
        code = SCRIPT.read_text().split("<<'PY' || comparison_result=$?\n", 1)[1].split("\nPY\n", 1)[0]
        fields = ["window", "request_path", "is_stream", "channel_id", "model_name", "upstream_model", "final_requests", "successes", "conversion_errors"]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with (root / "final.tsv").open("w") as f:
                w = csv.writer(f, delimiter="\t")
                w.writerow(fields)
                w.writerows(rows)
            with (root / "coverage.tsv").open("w") as f:
                w = csv.writer(f, delimiter="\t")
                w.writerow(fields[:6] + ["unresolved_requests"])
                for row in rows:
                    w.writerow(row[:6] + [unresolved if row[0] == gap_window else 0])
            result = subprocess.run(["python3", "-", str(root / "final.tsv"), str(root / "coverage.tsv"), str(root / "out.tsv"), "200", "10"], input=code, text=True, capture_output=True)
            output = (root / "out.tsv").read_text()
        return result, output

    def row(self, window, model="upstream", requests=100, successes=100, channel=1, conversion=0):
        """Create one explicit final-outcome cohort."""
        return [window, "/v1/messages", "f", channel, "client", model, requests, successes, conversion]

    def test_absent_protocols_are_not_observed_not_failed(self):
        result, report = self.compare([self.row("pre"), self.row("post")])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("not_observed", report)

    def test_legacy_error_history_is_merged_symmetrically(self):
        rows = [self.row("pre", requests=20, successes=20), self.row("pre", model="<unrecorded>", requests=80, successes=0), self.row("post", requests=100, successes=30)]
        result, report = self.compare(rows)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("legacy_client_model", report)
        self.assertIn("2000", report)

    def test_recorded_model_regression_is_not_hidden(self):
        rows = [self.row("pre"), self.row("post", successes=50), self.row("pre", model="second", successes=50), self.row("post", model="second")]
        self.assertEqual(self.compare(rows)[0].returncode, 1)

    def test_sparse_new_channel_keeps_coverage_gap(self):
        result, report = self.compare([self.row("pre"), self.row("post"), self.row("post", channel=2, requests=1, successes=1)])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("insufficient_samples", report)

    def test_no_comparable_traffic_does_not_pass(self):
        rows = [self.row("pre", requests=1, successes=1), self.row("post", requests=1, successes=1)]
        self.assertEqual(self.compare(rows)[0].returncode, 3)

    def test_unresolved_or_local_conversion_still_blocks(self):
        rows = [self.row("pre"), self.row("post")]
        self.assertEqual(self.compare(rows, unresolved=1)[0].returncode, 1)
        rows.append(self.row("post", channel=2, requests=1, successes=0, conversion=1))
        self.assertEqual(self.compare(rows)[0].returncode, 1)

    def test_single_success_difference_is_inconclusive(self):
        """The observed 3/25 to 2/24 change does not establish a regression."""
        result, report = self.compare([self.row("pre", requests=25, successes=3), self.row("post", requests=24, successes=2)])
        self.assertEqual(result.returncode, 3)
        self.assertIn("uncertain_rate_drop", report)

    def test_baseline_only_gap_is_inconclusive(self):
        """Missing old-version evidence is not a candidate integrity failure."""
        rows = [self.row("pre"), self.row("post")]
        result, report = self.compare(rows, unresolved=1, gap_window="pre")
        self.assertEqual(result.returncode, 3)
        self.assertIn("baseline_incomplete", report)

    def test_large_drop_remains_failure(self):
        """Strong regression evidence cannot be hidden as sampling noise."""
        result, report = self.compare([self.row("pre", requests=1000, successes=1000), self.row("post", requests=1000, successes=0)])
        self.assertEqual(result.returncode, 1)
        self.assertIn("supported_rate_drop", report)
