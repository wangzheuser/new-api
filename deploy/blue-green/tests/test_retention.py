"""Verify retention using real temporary files and a deterministic Docker boundary."""

import copy
import fcntl
import hashlib
import importlib.util
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("retention", Path(__file__).resolve().parents[1] / "retention.py")
retention = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(retention)


class RetentionTest(unittest.TestCase):
    """Protect production, other services, backup validity and repeatable deletion."""

    def setUp(self):
        """Use fake image metadata but exercise real checksums, locks and filesystem deletion."""
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.artifacts = self.root / "new-api-artifacts"
        self.backups = self.root / "new-api-backups"
        self.backups.mkdir()
        self.old = self.artifacts / "20260908T080000Z-aaaaaaaaaaaa"
        self.new = self.artifacts / "20260909T080000Z-bbbbbbbbbbbb"
        self.tags = {"old": "new-api:dev-old", "new": "new-api:dev-new"}
        self.images = []
        for directory, key, revision in ((self.old, "old", "a" * 40), (self.new, "new", "b" * 40)):
            (directory / "artifacts").mkdir(parents=True)
            (directory / "state").mkdir()
            (directory / "state" / "audit.txt").write_text("preserve historical evidence")
            data = dict(RELEASE_ID=directory.name, VERSION="dev-" + key, COMMIT_SHA=revision, IMAGE_TAG=self.tags[key])
            for field, name in (("IMAGE", "new-api-dev-"), ("DEFAULT_CLEAN_DIST", "default-clean-"), ("CLASSIC_CLEAN_DIST", "classic-clean-")):
                file = directory / "artifacts" / (name + key + ".tar.zst")
                file.write_bytes((field + key).encode())
                data[field + "_ARCHIVE"] = str(file.relative_to(directory))
                data[field + "_SHA256"] = hashlib.sha256(file.read_bytes()).hexdigest()
            (directory / "release.env").write_text("\n".join(f"{k}={v}" for k, v in data.items()))
            backup = self.backups / directory.name
            backup.mkdir()
            dump = backup / "postgresql.dump"
            dump.write_bytes(b"PGDMP-fixture")
            (backup / "postgresql.dump.sha256").write_text(hashlib.sha256(dump.read_bytes()).hexdigest() + "  postgresql.dump\n")
            self.images.append(dict(Id=key, RepoTags=[self.tags[key]], Config={"Labels": {"org.opencontainers.image.revision": revision}}))
        self.containers = [self.container("new-api-green", "new", True), self.container("new-api-blue", "old", False),
                           self.container("unrelated-service", "unrelated", False)]
        self.commands = []
        self.inventory_calls = 0
        self.drift = False
        self.fail_after_stop = False
        self.addCleanup(patch.stopall)
        patch.object(retention, "inventory", self.inventory).start()
        patch.object(retention, "inspect", self.inspect).start()
        patch.object(retention, "run", self.run_command).start()
        patch.object(retention, "public_version", self.public_version).start()

    def container(self, name, image, serving):
        """Represent the identities and references used by the actual deletion checks."""
        networks = {"app": {}}
        if serving:
            networks["proxy"] = {"Aliases": ["stable"]}
        return dict(Id=name + "-id", Name="/" + name, Image=image,
                    Config={"Image": self.tags.get(image, "other:latest")}, RestartCount=0,
                    State={"Running": True, "Health": {"Status": "healthy"}, "OOMKilled": False},
                    NetworkSettings={"Networks": networks})

    def inventory(self):
        """Inject a concurrent file change only after the deletion plan has been captured."""
        self.inventory_calls += 1
        if self.drift and self.inventory_calls == 2:
            (self.backups / self.old.name / "unexpected-file").write_text("concurrent change")
        return copy.deepcopy(self.containers), copy.deepcopy(self.images)

    def inspect(self, name):
        """Return a fresh snapshot, like Docker inspect."""
        return copy.deepcopy(next(c for c in self.containers if c["Name"] == "/" + name))

    def run_command(self, *args, **kwargs):
        """Model Docker stop/remove; no command can reach a real Docker daemon."""
        self.commands.append(args)
        if args[:3] == ("docker", "exec", "-i"):
            return "TABLE public fixture"
        if args[:4] == ("docker", "exec", "fixture-proxy", "wget"):
            return json.dumps({"data": {"version": self.public_version("")}})
        if args[:2] == ("docker", "stop"):
            if self.fail_after_stop:
                self.containers[0]["State"]["Health"]["Status"] = "unhealthy"
            return args[-1]
        if args[:2] == ("docker", "rm"):
            self.containers[:] = [c for c in self.containers if c["Id"] != args[-1]]
            return args[-1]
        if args[:3] == ("docker", "image", "rm"):
            self.images[:] = [i for i in self.images if not set(i["RepoTags"]) & set(args[3:])]
            return "removed"
        raise AssertionError(args)

    def cleanup(self, execute=True, rollback=False):
        """Call production retention with fixture scope and already-validated release decision."""
        return retention.cleanup(str(self.new), str(self.backups), "new-api-blue" if rollback else "new-api-green",
                                 "dev-old" if rollback else "dev-new", "proxy", "stable", "fixture-postgres",
                                 "dev-old", "rolled_back" if rollback else "passed", "https://fixture/api/status",
                                 "fixture-proxy", execute)

    def public_version(self, url):
        """Serve the actual fixture production version without opening a network connection."""
        return "dev-" + next(c["Image"] for c in self.containers if "proxy" in c["NetworkSettings"]["Networks"])

    def mutations(self):
        """Ignore read-only pg_restore inspection when checking fail-closed behavior."""
        return [c for c in self.commands if c[:2] != ("docker", "exec")]

    def test_cleanup_preserves_production_backup_assets_and_other_service(self):
        """Retain one image and backup, both current clean-dist files, and audit records."""
        result = self.cleanup()
        self.assertEqual(result["status"], "passed")
        self.assertFalse(result["rollback_available"])
        self.assertEqual([i["Id"] for i in self.images], ["new"])
        self.assertEqual([p.name for p in self.backups.iterdir() if p.is_dir()], [self.new.name])
        self.assertEqual(len(list((self.new / "artifacts").iterdir())), 3)
        self.assertEqual(list((self.old / "artifacts").iterdir()), [])
        self.assertTrue((self.old / "state/audit.txt").exists())
        self.assertEqual([c["Name"] for c in self.containers], ["/new-api-green", "/unrelated-service"])
        self.assertEqual(self.cleanup()["status"], "passed")
        self.assertEqual(sum(c[:2] == ("docker", "stop") for c in self.commands), 1)

    def test_rollback_keeps_actual_production_not_newest_image(self):
        """A newer failed candidate must not displace the restored production image."""
        self.containers[0]["NetworkSettings"]["Networks"].pop("proxy")
        self.containers[1]["NetworkSettings"]["Networks"]["proxy"] = {"Aliases": ["stable"]}
        result = self.cleanup(rollback=True)
        self.assertEqual(result["image_id"], "old")
        self.assertEqual([i["Id"] for i in self.images], ["old"])
        self.assertEqual(len(list((self.old / "artifacts").iterdir())), 3)
        self.assertEqual(list((self.new / "artifacts").iterdir()), [])

    def test_dry_run_never_removes_assets(self):
        """Planning may write its audit manifest, but cannot stop containers or delete files."""
        self.cleanup(execute=False)
        self.assertEqual(self.mutations(), [])
        self.assertTrue((self.backups / self.old.name).is_dir())
        self.assertEqual(len(self.images), 2)

    def test_invalid_backup_blocks_all_deletion(self):
        """Never delete an older recovery point before validating the retained one."""
        dump = self.backups / self.new.name / "postgresql.dump"
        dump.write_bytes(b"corrupt")
        with self.assertRaisesRegex(ValueError, "backup_checksum"):
            self.cleanup()
        self.assertEqual(self.mutations(), [])

    def test_invalid_archive_blocks_all_deletion(self):
        """The next build needs intact current clean-dist even if the runtime is healthy."""
        (self.new / "artifacts/default-clean-new.tar.zst").write_bytes(b"corrupt")
        with self.assertRaisesRegex(ValueError, "archive_checksum"):
            self.cleanup()
        self.assertEqual(self.mutations(), [])

    def test_foreign_image_tag_blocks_all_deletion(self):
        """Do not untag an image version shared with another repository."""
        self.images[0]["RepoTags"].append("another-service:retained")
        with self.assertRaisesRegex(ValueError, "shared_image_reference"):
            self.cleanup()
        self.assertEqual(self.mutations(), [])

    def test_shared_image_reference_blocks_all_deletion(self):
        """A different service's container reference prevents removal even when its name differs."""
        self.containers[2]["Image"] = "old"
        with self.assertRaisesRegex(ValueError, "shared_image_reference"):
            self.cleanup()
        self.assertEqual(self.mutations(), [])

    def test_production_drift_after_stop_preserves_old_container_and_images(self):
        """If production becomes unhealthy, leave the stopped standby recoverable."""
        self.fail_after_stop = True
        with self.assertRaisesRegex(ValueError, "production_changed"):
            self.cleanup()
        self.assertEqual(len(self.images), 2)
        self.assertFalse(any(c[:2] == ("docker", "rm") for c in self.commands))
        result = json.loads((self.new / "state/cleanup.result.json").read_text())
        self.assertEqual(result["status"], "failed")
        self.assertTrue(result["rollback_available"])

    def test_file_drift_blocks_all_deletion(self):
        """The plan must still describe the actual files when execution starts."""
        self.drift = True
        with self.assertRaisesRegex(ValueError, "files_changed"):
            self.cleanup()
        self.assertEqual(self.mutations(), [])

    def test_newer_release_or_symlink_blocks_deletion(self):
        """Do not remove a concurrently prepared release or traverse redirected directories."""
        later = self.artifacts / "20260910T080000Z-cccccccccccc"
        later.mkdir()
        with self.assertRaisesRegex(ValueError, "newer_release"):
            self.cleanup()
        later.rmdir()
        (self.old / "artifacts/escape").symlink_to(self.root)
        # A selected backup cannot contain links, including nested directory links.
        (self.backups / self.old.name / "escape").symlink_to(self.root)
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.cleanup()
        self.assertEqual(self.mutations(), [])

    def test_backup_lock_blocks_cleanup(self):
        """A real competing backup lock must block before any Docker mutation."""
        with (self.backups / ".backup.lock").open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaises(BlockingIOError):
                self.cleanup()
        self.assertEqual(self.mutations(), [])


class RetentionStateMachineTest(unittest.TestCase):
    """Exercise shell admission and missing-slot behavior without contacting a server."""

    def invoke(self, action, observation="passed", elapsed=601, accept=False, confirmed=True, rollback=False):
        """Replace only infrastructure boundaries; keep actual shell admission branches."""
        script = Path(__file__).resolve().parents[1] / "release-remote.sh"
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "definitions.sh").write_text(script.read_text().split('ACTION="${1:-}"')[0])
            (root / "role-state.env").write_text("NEW=new-api-green\nOLD=new-api-blue\nOLD_VERSION=old\nOLD_IP=fixture\n")
            record = f"observation={observation} release_id=fixture production=new-api-green version=new requested_seconds=600 elapsed_seconds={elapsed}\n"
            (root / "observation.result").write_text(record)
            if rollback:
                (root / "rollback.result").write_text("rollback=passed release_id=fixture production=new-api-blue version=old\n")
            harness = '''source "$1/definitions.sh"
STATE_DIR="$1"; RELEASE_DIR="$1"; BACKUP_ROOT="$1/backups"; SCRIPT_DIR="$1"
RELEASE_ID=fixture; VERSION=new; OLD_VERSION=old; PROXY_NETWORK=proxy; PROXY_ALIAS=stable; POSTGRES_CONTAINER=fixture
PUBLIC_STATUS_URL=https://fixture/api/status; PROXY_CONTAINER=fixture-proxy
load_config() { :; }
production_container() { echo "$TEST_PRODUCTION"; }
container_version() { [[ "$1" == "$TEST_PRODUCTION" ]] && echo "$TEST_VERSION"; }
proxy_version() { echo "$TEST_VERSION"; }
public_version() { echo "$TEST_VERSION"; }
docker() { return 1; }
python3() { echo "retention_called $*"; }
if [[ "$TEST_ACTION" == cleanup ]]; then
 action_cleanup --execute ${TEST_ACCEPT:+--accept-current}
elif [[ "$TEST_ACTION" == rollback ]]; then
 action_rollback --dry-run
else
 action_status
fi
'''
            result = subprocess.run(["bash", "-c", harness, "test", str(root)], capture_output=True, text=True,
                                    env={**os.environ, "TEST_ACTION": action, "TEST_PRODUCTION": "new-api-blue" if rollback else "new-api-green",
                                         "TEST_VERSION": "old" if rollback else "new", "TEST_ACCEPT": "1" if accept else "",
                                         "CONFIRM_CLEANUP": "fixture" if confirmed else "wrong"})
            self.assertEqual((root / "observation.result").read_text(), record)
            return result

    def test_cleanup_admission_preserves_failed_evidence(self):
        """Exceptional retirement requires explicit acceptance, not merely a nonzero gate."""
        for state, elapsed, accept, confirmed, allowed in (
                ("passed", 601, False, True, True), ("passed", 601, False, False, False),
                ("failed", 601, False, True, False), ("failed", 601, True, True, True),
                ("inconclusive", 601, True, True, True), ("running", 601, True, True, False),
                ("failed", 599, True, True, False)):
            with self.subTest(state=state, elapsed=elapsed, accept=accept, confirmed=confirmed):
                result = self.invoke("cleanup", state, elapsed, accept, confirmed)
                self.assertEqual(result.returncode == 0, allowed, result.stderr)
                self.assertEqual("retention_called" in result.stdout, allowed)

    def test_verified_rollback_allows_retention_of_old_production(self):
        """A rollback terminal record admits cleanup even when candidate observation failed."""
        result = self.invoke("cleanup", "failed", elapsed=0, rollback=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("rolled_back", result.stdout)

    def test_missing_standby_is_normal_status(self):
        """A completed cleanup must not make the next release's status query fail."""
        result = self.invoke("status")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("candidate=new-api-blue candidate_state=absent candidate_version=absent", result.stdout)

    def test_retired_rollback_target_is_rejected(self):
        """The old role-state file alone must never advertise usable rollback assets."""
        result = self.invoke("rollback")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("rollback_blocked=assets_retired", result.stderr)

    def test_stage_creates_a_missing_slot(self):
        """The real stage action recreates a removed standby through Compose."""
        script = Path(__file__).resolve().parents[1] / "release-remote.sh"
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "definitions.sh").write_text(script.read_text().split('ACTION="${1:-}"')[0])
            (root / "image").write_text("fixture")
            harness = '''source "$1/definitions.sh"
STATE_DIR="$1"; IMAGE_PATH="$1/image"; IMAGE_SHA256=hash; COMMIT_SHA=revision; IMAGE_TAG=new-api:new
VERSION=new; APP_NETWORK=app; DATA_DIR="$1/data"; LOG_DIR="$1/log"; created="$1/created"
load_config() { :; }
sha256_file() { echo hash; }
production_container() { echo new-api-green; }
slot_value() { echo fixture; }
render_compose() { echo fixture; }
wait_healthy() { test -f "$created"; }
container_version() { test -f "$created" && echo new; }
docker() {
 if [[ "$1 $2" == 'image inspect' ]]; then
  if [[ "$4" == *Architecture* ]]; then echo linux/amd64; else echo revision; fi
 elif [[ "$1" == compose ]]; then touch "$created";
 elif [[ "$1" == inspect ]]; then echo '[{"NetworkSettings":{"Networks":{"app":{}}}}]';
 else return 1; fi
}
action_stage
'''
            result = subprocess.run(["bash", "-c", harness, "test", str(root)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("stage=passed production=new-api-green candidate=new-api-blue", result.stdout)
            self.assertTrue((root / "created").exists())

    def test_all_mutating_phases_honor_the_shared_release_lock(self):
        """A busy release lock blocks before any Docker action, including cleanup and staging."""
        script = Path(__file__).resolve().parents[1] / "release-remote.sh"
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            # macOS lacks the flock CLI; this shim applies the same real advisory lock to fd 9.
            shim = root / "flock"
            shim.write_text("#!/usr/bin/env python3\nimport fcntl,sys\nfcntl.flock(int(sys.argv[-1]),fcntl.LOCK_EX|fcntl.LOCK_NB)\n")
            shim.chmod(0o700)
            docker = root / "docker"
            docker.write_text(f"#!/bin/sh\ntouch '{root}/docker-called'\nexit 99\n")
            docker.chmod(0o700)
            config = root / "release.env"
            keys = ("RELEASE_ID COMMIT_SHA VERSION IMAGE_TAG IMAGE_ARCHIVE IMAGE_SHA256 BACKUP_ROOT APP_NETWORK "
                    "PROXY_NETWORK PROXY_ALIAS PROXY_CONTAINER PUBLIC_STATUS_URL POSTGRES_CONTAINER POSTGRES_USER "
                    "POSTGRES_DB REDIS_CONTAINER NGINX_ACCESS_LOG BLUE_PORT BLUE_DATA_DIR BLUE_LOG_DIR BLUE_NODE_NAME "
                    "BLUE_PROJECT BLUE_RUNTIME_ENV_FILE GREEN_PORT GREEN_DATA_DIR GREEN_LOG_DIR GREEN_NODE_NAME "
                    "GREEN_PROJECT GREEN_RUNTIME_ENV_FILE")
            config.write_text("\n".join(k + "=fixture" for k in keys.split()))
            (root / "server.env").write_text("")
            (root / "compose.yml").write_text("")
            with (root / "release.lock").open("a") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                for action in ("backup", "stage", "gate", "cutover", "observe", "finalize", "rollback", "cleanup"):
                    with self.subTest(action=action):
                        result = subprocess.run(["bash", str(script), action], text=True, capture_output=True,
                                                env={**os.environ, "PATH": str(root) + os.pathsep + os.environ["PATH"],
                                                     "RELEASE_ENV": str(config), "SERVER_ENV": str(root / "server.env"),
                                                     "COMPOSE_TEMPLATE": str(root / "compose.yml"), "CUTOVER_LOCK": str(root / "release.lock")})
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn("BlockingIOError", result.stderr)
                        self.assertFalse((root / "docker-called").exists())


if __name__ == "__main__":
    unittest.main()
