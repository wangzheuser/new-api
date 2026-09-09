"""Retire only new-api release assets after the shell state machine accepts cleanup."""

import fcntl
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import urllib.request
from pathlib import Path

RELEASE_NAME = re.compile(r"\d{8}T\d{6}Z-[a-f0-9]{12}")


def run(*args, **kwargs):
    """Capture command output without logging runtime configuration or credentials."""
    return subprocess.check_output(args, text=True, **kwargs).strip()


def inspect(name):
    """Read current container identity, not a historical slot snapshot."""
    return json.loads(run("docker", "inspect", name))[0]


def metadata(directory):
    """Parse generated release metadata as data, never as shell code."""
    return dict(line.split("=", 1) for line in (directory / "release.env").read_text().splitlines()
                if "=" in line and not line.startswith("#"))


def fingerprint(path):
    """Reject links and detect changes to every planned file before removing anything."""
    paths = [path, *sorted(path.rglob("*"))] if path.is_dir() else [path]
    result = []
    for item in paths:
        if item.is_symlink():
            raise ValueError("cleanup_blocked=symlink")
        stat = item.stat()
        result.append((str(item), stat.st_dev, stat.st_ino, stat.st_mode, stat.st_size, stat.st_mtime_ns))
    return result


def inventory():
    """Collect exact repository images and all container references before planning deletion."""
    ids = run("docker", "ps", "-aq").splitlines()
    containers = json.loads(run("docker", "inspect", *ids)) if ids else []
    ids = sorted(set(run("docker", "image", "ls", "new-api", "--quiet", "--no-trunc").splitlines()))
    images = json.loads(run("docker", "image", "inspect", *ids)) if ids else []
    return containers, images


def public_version(url):
    """Bound the public health check and verify version rather than HTTP status alone."""
    with urllib.request.urlopen(url, timeout=20) as response:
        return json.load(response)["data"]["version"]


def cleanup(release, backups, production, version, network, alias, postgres, old_version, decision,
            public_url, proxy, execute):
    """Plan and apply one-version retention without touching data volumes or other services."""
    release, backups = Path(release), Path(backups)
    root = release.parent
    if (root.name != "new-api-artifacts" or backups.name != "new-api-backups"
            or not RELEASE_NAME.fullmatch(release.name)
            or production not in ("new-api-blue", "new-api-green")
            or decision not in ("passed", "rolled_back", "accepted_by_user")):
        raise ValueError("cleanup_blocked=invalid_scope")
    for path in (release, root, backups):
        if path.is_symlink() or path.absolute() != path.resolve():
            raise ValueError("cleanup_blocked=unsafe_root")
    with (backups / ".backup.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        releases = sorted(p for p in root.iterdir() if RELEASE_NAME.fullmatch(p.name))
        if releases[-1] != release:
            raise ValueError("cleanup_blocked=newer_release_exists")
        containers, images = inventory()
        current = next(c for c in containers if c["Name"] == "/" + production)
        expected = (current["Id"], current["Image"])

        def verify_production():
            """Keep the serving container and its proxy ownership unchanged throughout cleanup."""
            c = inspect(production)
            if ((c["Id"], c["Image"]) != expected or not c["State"]["Running"]
                    or c["State"].get("Health", {}).get("Status") != "healthy"
                    or c["State"].get("OOMKilled") or c["RestartCount"] != 0
                    or alias not in c["NetworkSettings"]["Networks"].get(network, {}).get("Aliases", [])):
                raise ValueError("cleanup_blocked=production_changed_or_unhealthy")
            internal = json.loads(run("docker", "exec", proxy, "wget", "-qO-", "--timeout=10",
                                      f"http://{alias}:3000/api/status"))["data"]["version"]
            if internal != version or public_version(public_url) != version:
                raise ValueError("cleanup_blocked=serving_version_mismatch")

        verify_production()
        current_image = next(i for i in images if i["Id"] == current["Image"])
        revision = current_image["Config"].get("Labels", {}).get("org.opencontainers.image.revision")
        matching = [p for p in releases if not p.is_symlink() and (p / "release.env").is_file()
                    and metadata(p).get("COMMIT_SHA") == revision
                    and metadata(p).get("VERSION") == version]
        if not revision or not matching:
            raise ValueError("cleanup_blocked=production_artifacts_missing")
        retained = matching[-1]
        config = metadata(retained)
        if config.get("IMAGE_TAG") != current["Config"]["Image"]:
            raise ValueError("cleanup_blocked=image_tag_mismatch")
        # Current clean-dist is required by the next build; retain both themes, not runtime-dist.
        keep_files = []
        for key in ("IMAGE", "DEFAULT_CLEAN_DIST", "CLASSIC_CLEAN_DIST"):
            file = retained / config[key + "_ARCHIVE"]
            if file.parent != retained / "artifacts" or file.resolve() != file.absolute():
                raise ValueError("cleanup_blocked=unsafe_archive")
            digest_key = "IMAGE_SHA256" if key == "IMAGE" else key + "_SHA256"
            if hashlib.sha256(file.read_bytes()).hexdigest() != config[digest_key]:
                raise ValueError("cleanup_blocked=archive_checksum")
            keep_files.append(file)
        backup_dirs = sorted(p for p in backups.iterdir() if RELEASE_NAME.fullmatch(p.name))
        if not backup_dirs or any(p.is_symlink() or not p.is_dir() for p in backup_dirs):
            raise ValueError("cleanup_blocked=invalid_backups")
        latest = backup_dirs[-1]
        if latest.name > release.name:
            raise ValueError("cleanup_blocked=newer_backup_exists")
        dump = latest / "postgresql.dump"
        fingerprint(latest)
        checksum = (latest / "postgresql.dump.sha256").read_text().split()
        if len(checksum) != 2 or checksum[1] != "postgresql.dump" or hashlib.sha256(dump.read_bytes()).hexdigest() != checksum[0]:
            raise ValueError("cleanup_blocked=backup_checksum")
        with dump.open("rb") as source:
            if not run("docker", "exec", "-i", postgres, "pg_restore", "-l", stdin=source):
                raise ValueError("cleanup_blocked=backup_restore_list")
        standby = next((c for c in containers if c["Name"] in ("/new-api-blue", "/new-api-green") and c != current), None)
        if standby:
            standby_version = metadata(release)["VERSION"] if decision == "rolled_back" else old_version
            if network in standby["NetworkSettings"]["Networks"] or standby["Config"]["Image"] != "new-api:" + standby_version:
                raise ValueError("cleanup_blocked=standby_changed")
        retire = [i for i in images if i["Id"] != current["Image"]]
        for image in retire:
            if (not image.get("RepoTags") or any(not t.startswith("new-api:") for t in image["RepoTags"])
                    or any(c["Image"] == image["Id"] and c != standby for c in containers)):
                raise ValueError("cleanup_blocked=shared_image_reference")
        remove = list(backup_dirs[:-1])
        for directory in releases:
            if directory.is_symlink() or (directory / "artifacts").is_symlink():
                raise ValueError("cleanup_blocked=symlink")
            for pattern in ("new-api-*.tar.zst*", "default-clean-*.tar.zst*", "classic-clean-*.tar.zst*"):
                for file in (directory / "artifacts").glob(pattern):
                    if file not in keep_files and not any(str(file) == str(k) + ".sha256" for k in keep_files):
                        remove.append(file)
        snapshots = {str(p): fingerprint(p) for p in remove + keep_files + [latest]}
        plan = dict(release_id=release.name, decision=decision, production=production, version=version,
                    production_id=current["Id"], image_id=current["Image"], retained_release=str(retained),
                    retained_backup=str(latest), standby_id=standby["Id"] if standby else None,
                    removed_images=[i["Id"] for i in retire], removed_paths=[str(p) for p in remove])
        state = release / "state"
        if state.is_symlink() or any((state / name).is_symlink() for name in
                                    ("cleanup.plan.json", "cleanup.result.json", "cleanup.result.json.pending")):
            raise ValueError("cleanup_blocked=unsafe_state")
        state.mkdir(mode=0o700, exist_ok=True)
        (state / "cleanup.plan.json").write_text(json.dumps(plan, indent=2))
        if not execute:
            print("cleanup_dry_run=passed")
            return plan
        result = dict(plan, status="failed", rollback_available=True)
        try:
            verify_production()
            live_containers, live_images = inventory()
            if (sorted((c["Id"], c["Name"], c["Image"]) for c in live_containers)
                    != sorted((c["Id"], c["Name"], c["Image"]) for c in containers)
                    or sorted((i["Id"], sorted(i.get("RepoTags") or [])) for i in live_images)
                    != sorted((i["Id"], sorted(i.get("RepoTags") or [])) for i in images)):
                raise ValueError("cleanup_blocked=inventory_changed")
            if any(fingerprint(Path(p)) != snapshot for p, snapshot in snapshots.items()):
                raise ValueError("cleanup_blocked=files_changed")
            if standby:
                run("docker", "stop", "--time", "30", standby["Id"])
                verify_production()
                run("docker", "rm", standby["Id"])
            # Once retirement starts, cleanup failure must not initiate an application rollback.
            result["rollback_available"] = False
            for path in keep_files + [latest]:
                if fingerprint(path) != snapshots[str(path)]:
                    raise ValueError("cleanup_blocked=retained_assets_changed")
            for image in retire:
                run("docker", "image", "rm", *image["RepoTags"])
            for path in remove:
                if fingerprint(path) != snapshots[str(path)]:
                    raise ValueError("cleanup_blocked=files_changed")
                shutil.rmtree(path) if path.is_dir() else path.unlink()
            verify_production()
            remaining_containers, remaining_images = inventory()
            if (any(i["Id"] != current["Image"] for i in remaining_images)
                    or any(c["Name"] in ("/new-api-blue", "/new-api-green") and c["Id"] != current["Id"] for c in remaining_containers)
                    or sorted(p for p in backups.iterdir() if RELEASE_NAME.fullmatch(p.name)) != [latest]):
                raise ValueError("cleanup_failed=retention_postcondition")
            result["status"] = "passed"
            print("cleanup=passed images=1 backups=1 rollback_available=false")
            return result
        finally:
            pending = state / "cleanup.result.json.pending"
            pending.write_text(json.dumps(result, indent=2))
            pending.replace(state / "cleanup.result.json")


if __name__ == "__main__":
    os.umask(0o077)
    *arguments, mode = sys.argv[1:]
    if mode not in ("--dry-run", "--execute"):
        raise SystemExit("invalid cleanup mode")
    if mode == "--execute" and os.environ.get("CONFIRM_CLEANUP") != Path(arguments[0]).name:
        raise SystemExit("cleanup confirmation missing")
    cleanup(*arguments, execute=mode == "--execute")
