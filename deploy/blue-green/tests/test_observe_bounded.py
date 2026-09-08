"""Exercise bounded observation without real time or production access."""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import unittest


class BoundedObservationTest(unittest.TestCase):
    """An uncertain first decision must not be converted to a lucky pass."""

    def test_inconclusive_does_not_repeat_without_opt_in(self):
        """The default window is predeclared; lack of traffic is not cured by rerunning."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            shutil.copyfile(Path(__file__).resolve().parents[1]/"observe-bounded.sh", root/"observe-bounded.sh")
            (root/"release-remote.sh").write_text('echo call >> "'+str(root)+'/calls"; echo observation=inconclusive > "'+str(root)+'/state/observation.result"; exit 3')
            result = subprocess.run(["bash",str(root/"observe-bounded.sh")], capture_output=True, text=True,
                                    env={**os.environ, "ADDITIONAL_DIAGNOSTIC_OBSERVATION":"0"})
            self.assertEqual(result.returncode,3)
            self.assertEqual((root/"calls").read_text(),"call\n")
            self.assertFalse((root/"state/first-observation").exists())

    def test_bounded_decisions(self):
        """Cover immediate pass/fail and each possible second-window outcome."""
        for first, second, expected, calls in ((0, 0, 0, 1), (1, 0, 1, 1),
                                                (3, 0, 3, 2), (3, 3, 3, 2), (3, 1, 1, 2)):
            with self.subTest(first=first, second=second), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                shutil.copyfile(Path(__file__).resolve().parents[1]/'observe-bounded.sh', root/'observe-bounded.sh')
                (root/'release-remote.sh').write_text(f'''#!/bin/bash
cd "$(dirname "$0")"
count=$(cat count 2>/dev/null || echo 0)
echo $((count+1)) > count
rc={first}
[[ "$count" == 0 ]] || rc={second}
case "$rc" in 0) result=passed;; 3) result=inconclusive;; *) result=failed;; esac
echo "observation=$result" > state/observation.result
exit "$rc"
''')
                result = subprocess.run(['bash', str(root/'observe-bounded.sh')], capture_output=True, text=True, env={**os.environ, 'ADDITIONAL_DIAGNOSTIC_OBSERVATION':'1'})
                self.assertEqual(result.returncode, expected, result.stderr)
                self.assertEqual(int((root/'count').read_text()), calls)
                if first == 3 and second != 1:
                    self.assertIn('inconclusive', (root/'state/observation.result').read_text())
                if calls == 2:
                    self.assertTrue((root/'state/first-observation/observation.result').exists())
