"""Switch the dedicated gateway without moving application network endpoints."""

import argparse
import fcntl
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import urllib.request


GATEWAY = os.environ.get("GATEWAY_CONTAINER", "new-api-internal-gateway")
CONFIG = Path(os.environ.get("GATEWAY_CONFIG", "/opt/docker_projects/new-api-internal-gateway/gateway.conf"))
TARGET = CONFIG.parent / "active/target.conf"
JOURNAL = TARGET.parent / "transaction.json"
URL = os.environ.get("INTERNAL_GATEWAY_URL", "http://127.0.0.1:19001").rstrip("/")
SLOTS = ("new-api-blue", "new-api-green")


def run(*args, **kwargs):
    return subprocess.check_output(args, text=True, stderr=subprocess.PIPE, **kwargs).strip()


def selected():
    match = re.fullmatch(r"set \$deployment_slot (new-api-(?:blue|green));\s*", TARGET.read_text())
    if not match:
        raise ValueError("gateway_target_invalid")
    return match[1]


def atomic_write(path, content):
    """The whole active directory is mounted, so rename is visible inside Nginx."""
    with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as file:
        temporary = Path(file.name)
        try:
            os.fchmod(file.fileno(), 0o644 if path == TARGET else 0o600)
            file.write(content)
            file.flush()
            os.fsync(file.fileno())
            os.replace(temporary, path)
            directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            temporary.unlink(missing_ok=True)


def probe():
    # Never send the internal status request through an inherited HTTP proxy.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(URL + "/api/status", timeout=5) as response:
        body = json.load(response)
        slot = response.headers.get("X-New-API-Slot")
    if body.get("success") is not True or slot not in SLOTS:
        raise ValueError("gateway_response_invalid")
    return {"slot": slot, "version": body["data"]["version"]}


def status():
    result = probe()
    if result["slot"] != selected():
        raise ValueError("gateway_disk_live_mismatch; run recover")
    if JOURNAL.exists() and json.loads(JOURNAL.read_text())["state"] in ("prepared", "restore_failed"):
        raise ValueError("gateway_transaction_incomplete; run recover")
    return result


def wait_for(slot, version, seconds=20):
    deadline = time.monotonic() + seconds
    while True:
        try:
            if probe() == {"slot": slot, "version": version}:
                return
        except (OSError, ValueError, KeyError):
            pass
        if time.monotonic() >= deadline:
            raise TimeoutError("gateway_takeover_not_verified")
        time.sleep(0.2)


def check_slot(slot):
    container = json.loads(run("docker", "inspect", slot))[0]
    networks = container["NetworkSettings"]["Networks"]
    network = os.environ["APP_NETWORK"]
    if (set(networks) != {network} or (networks[network].get("IPAMConfig") or {}).get("IPv4Address")
            or container["HostConfig"]["RestartPolicy"]["Name"] != "unless-stopped"
            or container["HostConfig"].get("PortBindings")):
        raise ValueError("slot_topology_invalid=" + slot)
    if not container["State"]["Running"] or container["State"].get("Health", {}).get("Status") != "healthy":
        raise ValueError("slot_not_healthy=" + slot)
    body = json.loads(run("docker", "exec", slot, "wget", "-qO-", "--timeout=5", "http://127.0.0.1:3000/api/status"))
    if body.get("success") is not True:
        raise ValueError("slot_status_invalid=" + slot)
    return {"id": container["Id"], "version": body["data"]["version"]}


def check_gateway():
    gateway = json.loads(run("docker", "inspect", GATEWAY))[0]
    network = os.environ["APP_NETWORK"]
    networks = gateway["NetworkSettings"]["Networks"]
    if set(networks) != {network} or (networks[network].get("IPAMConfig") or {}).get("IPv4Address"):
        raise ValueError("gateway_network_invalid")
    if not any(m["Destination"] == "/etc/nginx/active" and Path(m["Source"]).resolve() == TARGET.parent.resolve()
               and not m["RW"] for m in gateway["Mounts"]):
        raise ValueError("gateway_requires_readonly_directory_mount")
    run("docker", "exec", GATEWAY, "nginx", "-t")


def doctor():
    current = status()
    if check_slot(current["slot"])["version"] != current["version"]:
        raise ValueError("gateway_version_mismatch")
    check_gateway()
    return current


def switch(slot, version, rollback=False):
    if rollback:
        # A failed release may no longer serve /api/status. Explicit rollback
        # trusts its durable selection and immutable image label, not a live probe.
        check_gateway()
        if JOURNAL.exists() and json.loads(JOURNAL.read_text())["state"] in ("prepared", "restore_failed"):
            raise ValueError("gateway_transaction_incomplete; run recover")
        old_slot = selected()
        container = json.loads(run("docker", "inspect", old_slot))[0]
        old_version = container["Config"]["Labels"]["org.opencontainers.image.version"]
        if not old_version:
            raise ValueError("rollback_source_version_missing")
        old = {"slot": old_slot, "version": old_version}
        old_identity = {"id": container["Id"]}
    else:
        old = doctor()
        old_identity = check_slot(old["slot"])
    new = check_slot(slot)
    if new["version"] != version:
        raise ValueError("candidate_version_mismatch")
    if not rollback and old == {"slot": slot, "version": version}:
        return {"state": "already_complete", **old}
    config = CONFIG.read_text()
    include = "include /etc/nginx/active/target.conf;"
    if config.count(include) != 1:
        raise ValueError("gateway_include_invalid")
    target = f"set $deployment_slot {slot};\n"
    # Validate the complete candidate before changing the durable selection.
    run("docker", "exec", "-i", GATEWAY, "sh", "-c", "cat > /tmp/new-api-gateway-candidate.conf",
        input=config.replace(include, target.strip()))
    run("docker", "exec", GATEWAY, "nginx", "-t", "-c", "/tmp/new-api-gateway-candidate.conf")
    transaction = {"state": "prepared", "old": old, "old_id": old_identity["id"],
                   "new": {"slot": slot, "version": version}, "new_id": new["id"]}
    atomic_write(JOURNAL, json.dumps(transaction, indent=2) + "\n")
    try:
        atomic_write(TARGET, target)
        run("docker", "exec", GATEWAY, "nginx", "-t")
        run("docker", "exec", GATEWAY, "nginx", "-s", "reload")
        wait_for(slot, version)
    except Exception:
        # Leave an incomplete journal if restoration also fails. Never report success from reload alone.
        try:
            restore(transaction)
        except Exception:
            transaction["state"] = "restore_failed"
            atomic_write(JOURNAL, json.dumps(transaction, indent=2) + "\n")
            raise
        raise
    transaction["state"] = "switched"
    atomic_write(JOURNAL, json.dumps(transaction, indent=2) + "\n")
    return {"state": "switched", "slot": slot, "version": version}


def restore(transaction):
    old = transaction["old"]
    actual = check_slot(old["slot"])
    if actual != {"id": transaction["old_id"], "version": old["version"]}:
        raise ValueError("recovery_old_identity_changed")
    atomic_write(TARGET, f"set $deployment_slot {old['slot']};\n")
    run("docker", "exec", GATEWAY, "nginx", "-t")
    run("docker", "exec", GATEWAY, "nginx", "-s", "reload")
    wait_for(old["slot"], old["version"])
    transaction["state"] = "restored"
    atomic_write(JOURNAL, json.dumps(transaction, indent=2) + "\n")


def recover():
    if not JOURNAL.exists():
        return status()
    transaction = json.loads(JOURNAL.read_text())
    if transaction["state"] in ("prepared", "restore_failed"):
        restore(transaction)
    return status()


def drain(seconds):
    """Keep standby alive until every pre-reload Nginx worker finishes its clients."""
    deadline = time.monotonic() + seconds
    while "worker process is shutting down" in run("docker", "top", GATEWAY):
        if time.monotonic() >= deadline:
            raise TimeoutError("gateway_drain_timeout; standby_preserved")
        time.sleep(1)
    return {"drain": "passed"}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("status", "doctor", "switch", "rollback", "recover", "drain"))
    parser.add_argument("--slot", choices=SLOTS)
    parser.add_argument("--version")
    parser.add_argument("--seconds", type=int, default=60)
    args = parser.parse_args()
    if args.action in ("switch", "rollback") and (not args.slot or not args.version):
        parser.error("switch/rollback requires --slot and --version")
    if args.seconds < 0:
        parser.error("seconds must be nonnegative")
    with (TARGET.parent / ".switch.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.action in ("switch", "rollback"):
            result = switch(args.slot, args.version, rollback=args.action == "rollback")
        elif args.action == "drain":
            result = drain(args.seconds)
        else:
            result = globals()[args.action]()
        print(json.dumps(result, sort_keys=True))
