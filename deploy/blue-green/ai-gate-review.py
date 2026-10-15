#!/usr/bin/env python3
"""Build and validate a bounded AI review for blue-green observation evidence."""

import argparse
import hashlib
import json
import sys
from collections import Counter
from pathlib import Path


VERDICTS = {"candidate_regression", "external_incident", "inconclusive", "pass"}
EVIDENCE_REFS = {
    "machine_facts",
    "observation.metrics",
    "observation.result",
    "gate.result",
    "bounded.result",
    "fingerprints",
    "protocol-comparison.tsv",
    "http-observations.json",
}
def parse_fields(path):
    """Read a shell-style result line without evaluating it."""
    if not path.is_file():
        return {}
    fields = {}
    for token in path.read_text().split():
        key, separator, value = token.partition("=")
        if separator:
            fields[key] = value
    return fields


def parse_metrics(path):
    """Read the first key/value line from observation metrics."""
    return parse_fields(path)


def log_fingerprints(path):
    """Extract fixed-category error counts from an application log."""
    if not path.is_file():
        return Counter()
    lines = path.read_text(errors="replace").splitlines()
    rejected_ids = set()
    canceled_ids = set()
    labels = {}
    requests = {}
    for line in lines:
        parts = [part.strip() for part in line.split("|", 6)]
        if line.startswith("[ERR]") and len(parts) >= 3:
            text = parts[-1]
            if "No available channel for model" in text or "channel has no compatible protocol route" in text:
                rejected_ids.add(parts[1])
            elif any(word in text.lower() for word in ("convert_request_failed", "conversion")):
                labels[parts[1]] = ("conversion_error", "request_conversion")
            elif "billing" in text.lower() and any(word in text.lower() for word in ("unsettled", "partial", "failed")):
                labels[parts[1]] = ("billing_unsettled", "billing_failure")
            elif "stream" in text.lower() and "error" in text.lower():
                labels[parts[1]] = ("stream_error", "stream_failure")
        if line.startswith("[INFO]") and len(parts) >= 3 and parts[-1] == "relay canceled by client":
            canceled_ids.add(parts[1])
        if line.startswith("[GIN]") and len(parts) == 7:
            try:
                requests[parts[2]] = (int(parts[3]), parts[6].split(None, 1)[1].split("?", 1)[0])
            except (ValueError, IndexError):
                continue
    counts = Counter()
    for request_id, (status, _path) in requests.items():
        if request_id in rejected_ids and status == 503:
            counts[("pre_upstream_rejected", "no_available_channel")] += 1
        elif request_id in canceled_ids:
            counts[("client_canceled", "client_canceled")] += 1
        elif request_id in labels:
            counts[labels[request_id]] += 1
        elif 400 <= status < 500:
            counts[("upstream_4xx", f"http_{status}")] += 1
        elif 500 <= status < 600:
            counts[("upstream_5xx", f"http_{status}")] += 1
    return counts


def parse_protocol_comparison(state_dir):
    """Return only aggregate protocol outcomes, without copying request data."""
    candidates = sorted(state_dir.glob("protocol-stability-*/comparison.tsv"))
    if not candidates:
        return {"available": False}
    path = candidates[-1]
    lines = path.read_text(errors="replace").splitlines()
    return {"available": True, "rows": max(0, len(lines) - 1), "file": "protocol-comparison.tsv"}


def build(state_dir):
    """Build the minimal, redacted evidence package for an AI reviewer."""
    observation = parse_fields(state_dir / "observation.result")
    metrics = parse_metrics(state_dir / "observation.metrics")
    gate = (state_dir / "gate.result").read_text().strip() if (state_dir / "gate.result").is_file() else ""
    public_browser = (state_dir / "public-browser.exit").read_text().strip() if (state_dir / "public-browser.exit").is_file() else ""
    pre = log_fingerprints(state_dir / "observation-baseline-app.log")
    post = log_fingerprints(state_dir / "observation-app.log")
    keys = sorted(set(pre) | set(post))
    fingerprints = []
    for category, label in keys:
        pre_count, post_count = pre[(category, label)], post[(category, label)]
        fingerprints.append({
            "category": category,
            "label": label,
            "pre_count": pre_count,
            "post_count": post_count,
            "candidate_added": pre_count == 0 and post_count > 0,
            "candidate_increased": post_count > pre_count,
            "fingerprint": hashlib.sha256(f"{category}:{label}".encode()).hexdigest()[:16],
        })
    new_errors = [item for item in fingerprints if item["candidate_added"]]
    increased_errors = [
        item for item in fingerprints
        if item["candidate_increased"] and item["category"] != "pre_upstream_rejected"
    ]
    shared_rejections = any(
        item["category"] == "pre_upstream_rejected" and item["pre_count"] > 0 and item["post_count"] > 0
        for item in fingerprints
    )
    hard_gate = gate == "gate=passed" or gate.startswith("gate=passed ")
    protocol_result = metrics.get("protocol_stability_rc", "")
    hard_error_categories = {"conversion_error", "stream_error", "billing_unsettled"}
    hard_error = any(item["category"] in hard_error_categories and item["post_count"] > 0 for item in fingerprints)
    eligible = (
        observation.get("observation") in {"failed", "inconclusive"}
        and protocol_result == "3"
        and hard_gate
        and public_browser == "0"
        and not (state_dir / "public-browser.exit.pending").exists()
        and not new_errors
        and not increased_errors
        and shared_rejections
        and not hard_error
    )
    return {
        "schema_version": 1,
        "machine_facts": {
            "observation_state": observation.get("observation", "missing"),
            "protocol_result": int(protocol_result) if protocol_result.isdigit() else None,
            "hard_gate_passed": hard_gate,
            "public_browser_passed": public_browser == "0" and not (state_dir / "public-browser.exit.pending").exists(),
            "metrics": {
                key: metrics[key]
                for key in ("baseline_samples", "baseline_errors_5xx", "samples", "errors_5xx",
                            "allowed_errors_5xx", "actionable_errors_5xx", "actionable_allowed_errors_5xx")
                if key in metrics
            },
            "protocol_comparison": parse_protocol_comparison(state_dir),
        },
        "fingerprints": fingerprints,
        "eligibility": {
            "external_incident": eligible,
            "candidate_new_error_fingerprints": len(new_errors),
        },
        "evidence_refs": ["machine_facts", "fingerprints", "observation.metrics", "observation.result", "gate.result"],
        "review_instructions": (
            "Return JSON only with verdict, confidence, reason, evidence_refs, and required_action. "
            "Never override a hard gate or infer success from missing evidence."
        ),
    }


def validate(input_path, verdict_path):
    """Validate an AI verdict against immutable machine evidence."""
    evidence = json.loads(input_path.read_text())
    verdict = json.loads(verdict_path.read_text())
    if not isinstance(verdict, dict):
        raise ValueError("verdict must be an object")
    if verdict.get("verdict") not in VERDICTS:
        raise ValueError("invalid verdict")
    confidence = verdict.get("confidence")
    if isinstance(confidence, bool) or not isinstance(confidence, (int, float)) or not 0 <= confidence <= 1:
        raise ValueError("confidence must be between 0 and 1")
    reason = verdict.get("reason")
    if not isinstance(reason, str) or not reason.strip() or len(reason) > 1024 or "\n" in reason or "\r" in reason:
        raise ValueError("reason is required and must be one line")
    refs = verdict.get("evidence_refs")
    if not isinstance(refs, list) or not refs or any(ref not in EVIDENCE_REFS for ref in refs):
        raise ValueError("evidence_refs must reference known evidence")
    state = evidence["machine_facts"]["observation_state"]
    if verdict["verdict"] == "pass" and state != "passed":
        raise ValueError("pass cannot override a non-passing observation")
    if verdict["verdict"] == "external_incident" and not evidence["eligibility"]["external_incident"]:
        raise ValueError("external_incident is not supported by machine evidence")
    if verdict["verdict"] == "external_incident" and confidence < 0.8:
        raise ValueError("external_incident confidence must be at least 0.8")
    required_action = verdict.get("required_action")
    if required_action not in {"rollback", "hold", "finalize"}:
        raise ValueError("invalid required_action")
    return {
        "schema_version": 1,
        "validation": "passed",
        "verdict": verdict["verdict"],
        "confidence": confidence,
        "reason": reason.strip(),
        "evidence_refs": refs,
        "required_action": required_action,
    }


def main():
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    build_parser = subparsers.add_parser("build")
    build_parser.add_argument("--state-dir", required=True, type=Path)
    build_parser.add_argument("--output", required=True, type=Path)
    validate_parser = subparsers.add_parser("validate")
    validate_parser.add_argument("--input", required=True, type=Path)
    validate_parser.add_argument("--verdict-file", required=True, type=Path)
    validate_parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.command == "build":
            result = build(args.state_dir)
        else:
            result = validate(args.input, args.verdict_file)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
        print("ai_review=passed command=" + args.command)
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(f"ai_review=blocked reason={error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
