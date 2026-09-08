"""Validate opt-in low-traffic delivery evidence without overriding regressions."""
import csv
import json
from pathlib import Path
import sys


def validate(probes_path, log_path, protocol_dir):
    """Require clean live outcomes and four finalized probes from the candidate."""
    root = Path(protocol_dir)
    with (root / 'comparison.tsv').open() as handle:
        comparisons = list(csv.DictReader(handle, delimiter='\t'))
    if not any(row['model_evidence'] == 'no_comparable_traffic' for row in comparisons):
        return False
    if any(row['result'] == 'failed' or
           (row['result'] == 'inconclusive' and row['reason'] != 'coverage_gap') for row in comparisons):
        return False
    with (root / 'final-request-success.tsv').open() as handle:
        for row in csv.DictReader(handle, delimiter='\t'):
            if row['window'] == 'post' and (int(row['successes']) != int(row['final_requests']) or int(row.get('conversion_errors', 0))):
                return False
    with (root / 'final-request-coverage.tsv').open() as handle:
        if any(int(row['unresolved_requests']) for row in csv.DictReader(handle, delimiter='\t')):
            return False
    http = json.loads((root / 'http-observations.json').read_text())
    if any(count and (not name.endswith(':http_200')) for name, count in http['post'].items()):
        return False
    observed = set()
    for line in Path(log_path).read_text().splitlines():
        if not line.startswith('[GIN]'):
            continue
        fields = [field.strip() for field in line.split('|')]
        if len(fields) >= 7 and fields[1] == 'relay' and fields[3] == '200':
            observed.add((fields[2], fields[-1].split('?', 1)[0]))
    probes = json.loads(Path(probes_path).read_text())
    expected = {(protocol, stream) for protocol in ('chat', 'responses') for stream in (False, True)}
    seen, request_ids = set(), set()
    if len(probes) != 4:
        return False
    for probe in probes:
        pair = (probe.get('protocol'), probe.get('stream'))
        if type(probe.get('stream')) is not bool or pair not in expected or pair in seen or probe.get('http') != 200 or not probe.get('terminal') or not probe.get('text', '').strip():
            return False
        final = [row for row in probe.get('final', []) if row.get('type') == 2]
        if len(final) != 1:
            return False
        row = final[0]
        state = row.get('stream_status') or {}
        if (pair[1] and not state) or (state and (state.get('status') != 'ok' or state.get('billing_finalization') != 'settled')):
            return False
        request_id = row.get('request_id')
        path = 'POST /v1/chat/completions' if pair[0] == 'chat' else 'POST /v1/responses'
        if not request_id or request_id in request_ids or (request_id, path) not in observed:
            return False
        seen.add(pair)
        request_ids.add(request_id)
    return seen == expected


if __name__ == '__main__':
    try:
        passed = validate(*sys.argv[1:])
    except (OSError, ValueError, KeyError, TypeError):
        passed = False
    print('low_traffic_evidence=' + ('passed probes=4' if passed else 'failed'))
    sys.exit(0 if passed else 1)
