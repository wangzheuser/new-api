import hashlib
import os
import pathlib
import subprocess
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "cleanup-local.sh"
COMMIT = "a" * 40
IMAGE_ID = "sha256:" + "b" * 64


class LocalCleanupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.releases = self.root / "releases"
        self.release = self.releases / COMMIT[:12]
        (self.release / "artifacts").mkdir(parents=True)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        docker = self.bin / "docker"
        docker.write_text(
            '#!/usr/bin/env bash\n'
            'printf "%s\\n" "$*" >> "$DOCKER_CALLS"\n'
            '[[ "$*" != info || "${DOCKER_OFFLINE:-0}" != 1 ]] || exit 1\n'
            '[[ "$*" != "image rm "* || "${DOCKER_FAIL_RM:-0}" != 1 ]] || exit 1\n'
            'case "$*" in\n'
            '  "image inspect -f {{.Id}} "*) echo "$EXPECTED_IMAGE_ID" ;;\n'
            'esac\n'
        )
        docker.chmod(0o755)
        self.calls = self.root / "docker-calls"
        fields = [
            f"COMMIT_SHA={COMMIT}",
            f"IMAGE_TAG=new-api:dev-{COMMIT[:12]}-local-amd64",
            f"IMAGE_ID={IMAGE_ID}",
            f"BUILDER_NAME=new-api-release-{COMMIT[:12]}",
        ]
        self.archives = []
        for kind in ("IMAGE", "DEFAULT_CLEAN_DIST", "CLASSIC_CLEAN_DIST"):
            archive = f"artifacts/{kind.lower()}.tar.zst"
            path = self.release / archive
            path.write_bytes(kind.encode())
            self.archives.append(path)
            fields.extend((
                f"{kind}_ARCHIVE={archive}",
                f"{kind}_SHA256={hashlib.sha256(path.read_bytes()).hexdigest()}",
            ))
        (self.release / "release.env").write_text("\n".join(fields) + "\n")

    def run_cleanup(self, mode="--execute", directory=None, fail_rm=False, offline=False):
        env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}",
                   LOCAL_RELEASE_ROOT=str(self.releases), DOCKER_CALLS=str(self.calls),
                   EXPECTED_IMAGE_ID=IMAGE_ID, DOCKER_FAIL_RM="1" if fail_rm else "0",
                   DOCKER_OFFLINE="1" if offline else "0")
        return subprocess.run(
            ["bash", str(SCRIPT), "--release-dir", str(directory or self.release), mode],
            env=env, text=True, capture_output=True,
        )

    def test_dry_run_preserves_files(self):
        result = self.run_cleanup("--dry-run")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(all(path.exists() for path in self.archives))
        self.assertNotIn("image rm", self.calls.read_text() if self.calls.exists() else "")

    def test_execute_removes_only_release_resources(self):
        other = self.root / "other"
        other.write_text("preserve")
        result = self.run_cleanup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(all(not path.exists() for path in self.archives))
        self.assertTrue((self.release / "release.env").exists())
        self.assertEqual(other.read_text(), "preserve")
        self.assertIn("image rm new-api:dev-", self.calls.read_text())
        self.assertIn("buildx rm new-api-release-", self.calls.read_text())
        self.assertEqual(self.run_cleanup().returncode, 0)

    def test_checksum_mismatch_blocks_every_deletion(self):
        self.archives[1].write_bytes(b"changed")
        result = self.run_cleanup()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("archive_checksum", result.stderr)
        self.assertNotIn("image rm", self.calls.read_text() if self.calls.exists() else "")
        self.assertTrue(all(path.exists() for path in self.archives))

    def test_outside_root_is_rejected(self):
        result = self.run_cleanup(directory=self.root)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("outside_release_root", result.stderr)

    def test_image_removal_failure_preserves_archives(self):
        result = self.run_cleanup(fail_rm=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(all(path.exists() for path in self.archives))
        self.assertNotIn("buildx rm", self.calls.read_text())

    def test_docker_unavailable_preserves_archives(self):
        result = self.run_cleanup(offline=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(all(path.exists() for path in self.archives))


if __name__ == "__main__":
    unittest.main()
