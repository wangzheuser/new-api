#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_ENV="${SERVER_ENV:-$SCRIPT_DIR/server.env}"

usage() {
  cat <<'EOF'
Usage: protocol-stability-gate.sh --cutover-epoch EPOCH --output-dir DIR [options]

Options:
  --baseline-log FILE   Previous slot access/application log for the pre window.
  --observation-log FILE Current slot log for the post window; use with --baseline-log.
  --channel-id ID     Restrict touched requests to this channel; default all channels.
  --seconds N          Equal pre/post measurement duration, default 10800.
  --drain-seconds N    Ignore the initial post-cutover cache drain, default 30.
  --settle-seconds N   Wait allowance for final request logs, default 300.
  --max-drop-bps N     Maximum success-rate drop in basis points, default 200.
  --min-requests N     Minimum final requests required per protocol/stream/model/channel cohort, default 10.
  --exclude-file FILE  File containing one synthetic request ID per line; repeatable.

The gate counts one globally final, non-intermediate log per request ID. It also
records channel-attempt errors separately so retries do not reduce final success.
Cohorts use the first touched attempt; final outcomes are global across channels.
Unknown historical upstream models are labeled <unrecorded>, never reconstructed.
EOF
}

require_value() {
  [[ $# -ge 2 && -n "$2" ]] || {
    printf 'missing_value=%s\n' "$1" >&2
    exit 2
  }
}

channel_id="0"
cutover_epoch=""
output_dir=""
seconds=10800
drain_seconds=30
settle_seconds=300
max_drop_bps=200
min_requests=10
exclude_files=()
baseline_log=""
observation_log=""

while [[ $# -gt 0 ]]; do
  case "$1" in
  --baseline-log | --observation-log)
    require_value "$@"
    if [[ "$1" == --baseline-log ]]; then baseline_log="$2"; else observation_log="$2"; fi
    shift 2
    ;;
  --channel-id)
    require_value "$@"
    channel_id="$2"
    shift 2
    ;;
  --cutover-epoch)
    require_value "$@"
    cutover_epoch="$2"
    shift 2
    ;;
  --output-dir)
    require_value "$@"
    output_dir="$2"
    shift 2
    ;;
  --seconds)
    require_value "$@"
    seconds="$2"
    shift 2
    ;;
  --drain-seconds)
    require_value "$@"
    drain_seconds="$2"
    shift 2
    ;;
  --settle-seconds)
    require_value "$@"
    settle_seconds="$2"
    shift 2
    ;;
  --max-drop-bps)
    require_value "$@"
    max_drop_bps="$2"
    shift 2
    ;;
  --min-requests)
    require_value "$@"
    min_requests="$2"
    shift 2
    ;;
  --exclude-file)
    require_value "$@"
    exclude_files+=("$2")
    shift 2
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    printf 'unknown_option=%s\n' "$1" >&2
    usage >&2
    exit 2
    ;;
  esac
done

for value in "$channel_id" "$cutover_epoch" "$seconds" "$drain_seconds" "$settle_seconds" "$max_drop_bps" "$min_requests"; do
  [[ "$value" =~ ^[0-9]+$ ]] || {
    printf 'invalid_numeric_value=%s\n' "$value" >&2
    exit 2
  }
done
((seconds > 0 && min_requests > 0 && max_drop_bps <= 10000)) || {
  printf 'invalid_measurement_limits=1\n' >&2
  exit 2
}
[[ -n "$output_dir" ]] || {
  printf 'missing_value=--output-dir\n' >&2
  exit 2
}
[[ -r "$SERVER_ENV" ]] || {
  printf 'missing_server_env=%s\n' "$SERVER_ENV" >&2
  exit 1
}

# server.env is generated from trusted deployment inputs and contains no shell commands.
# shellcheck disable=SC1090
source "$SERVER_ENV"
for name in POSTGRES_CONTAINER POSTGRES_USER POSTGRES_DB; do
  [[ -n "${!name:-}" ]] || {
    printf 'missing_variable=%s\n' "$name" >&2
    exit 1
  }
done

now_epoch="$(date +%s)"
post_start=$((cutover_epoch + drain_seconds))
post_end=$((post_start + seconds))
required_epoch=$((post_end + settle_seconds))
if ((now_epoch < required_epoch)); then
  printf 'gate_not_ready=1 elapsed=%s required=%s remaining=%s\n' \
    "$((now_epoch - cutover_epoch))" "$((required_epoch - cutover_epoch))" "$((required_epoch - now_epoch))" >&2
  exit 3
fi
pre_end="$cutover_epoch"
pre_start=$((pre_end - seconds))

excluded_values=()
for file in "${exclude_files[@]}"; do
  [[ -r "$file" ]] || {
    printf 'missing_exclude_file=%s\n' "$file" >&2
    exit 1
  }
  while IFS= read -r request_id; do
    request_id="${request_id//$'\r'/}"
    [[ -z "$request_id" ]] && continue
    [[ "$request_id" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || {
      printf 'invalid_excluded_request_id=%s\n' "$request_id" >&2
      exit 1
    }
    excluded_values+=("'$request_id'")
  done < "$file"
done
excluded_sql="ARRAY[]::text[]"
if ((${#excluded_values[@]} > 0)); then
  excluded_sql="ARRAY[$(IFS=,; printf '%s' "${excluded_values[*]}")]::text[]"
fi

umask 077
mkdir "$output_dir"
chmod 700 "$output_dir"

run_psql() {
  docker exec -i "$POSTGRES_CONTAINER" psql -X -v ON_ERROR_STOP=1 \
    -U "$POSTGRES_USER" -d "$POSTGRES_DB" -A -P pager=off -P footer=off -F $'\t'
}

# Bind shared database records to the observed slot request IDs when supplied.
app_filter_sql="true"
app_requests_sql="app_requests(period, request_id, request_path, status_code, client_canceled) AS
  (SELECT ''::text, ''::text, ''::text, 0, false WHERE false),"
if [[ -n "$baseline_log" || -n "$observation_log" ]]; then
  [[ -r "$baseline_log" && -r "$observation_log" ]]
  python3 - "$baseline_log" "$observation_log" "$output_dir" "${exclude_files[@]}" <<'PYAPP'
import collections
import json
from pathlib import Path
import re
import sys

baseline, observation, target, *exclude_files = sys.argv[1:]
excluded = {line.strip() for file in exclude_files for line in Path(file).read_text().splitlines() if line.strip()}
values = []
summary = {}
for window, filename in (("pre", baseline), ("post", observation)):
    lines = Path(filename).read_text().splitlines()
    canceled = set()
    for line in lines:
        parts = line.split("|", 2)
        if line.startswith("[INFO]") and len(parts) == 3 and parts[2].strip() == "relay canceled by client":
            canceled.add(parts[1].strip())
    counts = collections.Counter()
    requests = {}
    for line in lines:
        if not line.startswith("[GIN]"):
            continue
        parts = [part.strip() for part in line.split("|", 6)]
        if len(parts) != 7:
            raise ValueError("malformed GIN log")
        method, path = parts[6].split(None, 1)
        path = path.split("?", 1)[0]
        if re.fullmatch(r"/v1(beta)?/models/[^/]+:(streamGenerateContent|generateContent)", path):
            path = "/gemini/content"
        if method != "POST" or path not in ("/v1/messages", "/v1/responses", "/v1/chat/completions", "/gemini/content"):
            continue
        request_id, status = parts[2], int(parts[3])
        if request_id in excluded:
            continue
        if not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", request_id):
            raise ValueError("missing or invalid relay request ID")
        requests[request_id] = (path, status)
    for request_id, (path, status) in requests.items():
        is_canceled = request_id in canceled
        values.append(f"('{window}','{request_id}','{path}',{status},{str(is_canceled).lower()})")
        counts[f"{path}:http_{status}"] += 1
        if is_canceled:
            counts[f"{path}:client_canceled"] += 1
    summary[window] = dict(counts)
if values:
    sql = "app_requests(period, request_id, request_path, status_code, client_canceled) AS (VALUES " + ",".join(values) + "),"
else:
    sql = "app_requests(period, request_id, request_path, status_code, client_canceled) AS (SELECT ''::text, ''::text, ''::text, 0, false WHERE false),"
Path(target, "http-observations.sql").write_text(sql)
Path(target, "http-observations.json").write_text(json.dumps(summary, indent=2) + "\n")
PYAPP
  app_requests_sql="$(cat "$output_dir/http-observations.sql")"
  app_filter_sql="EXISTS (SELECT 1 FROM app_requests a WHERE a.period = w.name AND a.request_id = l.request_id)"
fi

cat >"$output_dir/context.txt" <<EOF
channel_id=$channel_id
cutover_epoch=$cutover_epoch
pre_start=$pre_start
pre_end=$pre_end
post_start=$post_start
post_end=$post_end
seconds=$seconds
drain_seconds=$drain_seconds
settle_seconds=$settle_seconds
max_drop_bps=$max_drop_bps
min_requests=$min_requests
excluded_request_ids=${#excluded_values[@]}
EOF

# Compare protocol identities, not query strings or Gemini model URL spellings.
request_path_sql="CASE WHEN split_part(l.other::jsonb->>'request_path', '?', 1)
  ~ '^/v1(beta)?/models/[^/]+:(streamGenerateContent|generateContent)$'
  THEN '/gemini/content' ELSE split_part(l.other::jsonb->>'request_path', '?', 1) END"
protocol_filter_sql="($request_path_sql) IN ('/v1/messages', '/v1/responses', '/v1/chat/completions', '/gemini/content')"
# Do not reconstruct historical upstream models from mutable channel mappings.
upstream_model_sql="COALESCE(NULLIF(l.other::jsonb->>'upstream_model_name', ''),
  NULLIF(l.other::jsonb#>>'{admin_info,context_fallback,upstream_model}', ''),
  CASE WHEN l.type = 2 AND (l.other::jsonb->>'is_model_mapped') IS DISTINCT FROM 'true'
    THEN NULLIF(l.model_name, '') END, '<unrecorded>')"

run_psql >"$output_dir/final-request-success.tsv" <<SQL
WITH $app_requests_sql windows(name, start_epoch, end_epoch) AS (
  VALUES
    ('pre', $pre_start::bigint, $pre_end::bigint),
    ('post', $post_start::bigint, $post_end::bigint)
), touched AS (
  SELECT DISTINCT ON (l.request_id)
    w.name,
    w.start_epoch,
    w.end_epoch,
    l.request_id,
    $request_path_sql AS request_path,
    l.is_stream,
    l.channel_id,
    l.model_name,
    $upstream_model_sql AS upstream_model
  FROM windows w
  JOIN logs l ON l.created_at >= w.start_epoch AND l.created_at < w.end_epoch
  WHERE (0 = $channel_id OR l.channel_id = $channel_id)
    AND l.request_id <> ''
    AND l.request_id <> ALL ($excluded_sql)
    AND $protocol_filter_sql
    AND $app_filter_sql
  ORDER BY l.request_id, l.created_at, l.id
), ranked AS (
  SELECT
    t.name,
    t.request_path,
    t.is_stream,
    t.channel_id,
    t.model_name,
    t.upstream_model,
    l.type,
    l.other::jsonb AS details,
    row_number() OVER (
      PARTITION BY t.name, t.request_id
      ORDER BY l.created_at DESC, l.id DESC
    ) AS request_rank
  FROM touched t
  JOIN logs l ON l.request_id = t.request_id
  WHERE COALESCE(l.is_intermediate, false) = false
    AND l.created_at >= t.start_epoch
    AND l.created_at < t.end_epoch + $settle_seconds
    AND l.type IN (2, 5)
)
SELECT
  name AS window,
  request_path,
  is_stream,
  channel_id,
  model_name,
  upstream_model,
  count(*) AS final_requests,
  count(*) FILTER (WHERE successful IS TRUE) AS successes,
  count(*) FILTER (WHERE successful IS NOT TRUE) AS failures,
  count(*) FILTER (WHERE details->'stream_status'->>'end_reason' = 'client_gone') AS client_canceled,
  count(*) FILTER (WHERE details->>'status_code' = '429') AS rate_limited,
  count(*) FILTER (WHERE details->>'error_code' = 'convert_request_failed') AS conversion_errors,
  count(*) FILTER (WHERE details->'stream_status'->>'status' = 'error') AS stream_errors,
  count(*) FILTER (WHERE successful IS NOT TRUE AND NULLIF(details->'admin_info'->>'upstream_error', '') IS NOT NULL) AS original_error_recorded,
  round(10000.0 * count(*) FILTER (WHERE successful IS TRUE) / NULLIF(count(*), 0))::bigint AS success_bps
FROM (
  SELECT *, type = 2 AND (
    NOT is_stream OR (
      details->'stream_status'->>'status' = 'ok'
      AND COALESCE(details->'stream_status'->>'billing_finalization', '') <> 'settled_partial'
      AND COALESCE(details->'stream_status'->>'terminal_status', '') <> 'failed'
    )
  ) AS successful
  FROM ranked WHERE request_rank = 1
) final
GROUP BY name, request_path, is_stream, channel_id, model_name, upstream_model
ORDER BY name, request_path, is_stream, channel_id, model_name, upstream_model;
SQL

run_psql >"$output_dir/channel-attempt-errors.tsv" <<SQL
WITH $app_requests_sql windows(name, start_epoch, end_epoch) AS (
  VALUES
    ('pre', $pre_start::bigint, $pre_end::bigint),
    ('post', $post_start::bigint, $post_end::bigint)
)
SELECT
  w.name AS window,
  $request_path_sql AS request_path,
  l.is_stream,
  COALESCE(l.is_intermediate, false) AS is_intermediate,
  COALESCE((l.other::jsonb->>'status_code')::int, 0) AS status_code,
  count(*) AS attempts
FROM windows w
JOIN logs l ON l.created_at >= w.start_epoch AND l.created_at < w.end_epoch
WHERE (0 = $channel_id OR l.channel_id = $channel_id)
  AND l.type = 5
  AND l.request_id <> ALL ($excluded_sql)
  AND $protocol_filter_sql
  AND $app_filter_sql
GROUP BY w.name, request_path, l.is_stream, COALESCE(l.is_intermediate, false), status_code
ORDER BY w.name, request_path, l.is_stream, is_intermediate, status_code;
SQL

run_psql >"$output_dir/final-request-coverage.tsv" <<SQL
WITH $app_requests_sql windows(name, start_epoch, end_epoch) AS (
  VALUES
    ('pre', $pre_start::bigint, $pre_end::bigint),
    ('post', $post_start::bigint, $post_end::bigint)
), touched AS (
  SELECT DISTINCT ON (l.request_id)
    w.name,
    w.start_epoch,
    w.end_epoch,
    l.request_id,
    $request_path_sql AS request_path,
    l.is_stream,
    l.channel_id,
    l.model_name,
    $upstream_model_sql AS upstream_model
  FROM windows w
  JOIN logs l ON l.created_at >= w.start_epoch AND l.created_at < w.end_epoch
  WHERE (0 = $channel_id OR l.channel_id = $channel_id)
    AND l.request_id <> ''
    AND l.request_id <> ALL ($excluded_sql)
    AND $protocol_filter_sql
    AND $app_filter_sql
  ORDER BY l.request_id, l.created_at, l.id
), finalized AS (
  SELECT DISTINCT t.name, t.request_path, t.is_stream, t.channel_id, t.model_name, t.upstream_model, t.request_id
  FROM touched t
  JOIN logs l ON l.request_id = t.request_id
  WHERE COALESCE(l.is_intermediate, false) = false
    AND l.created_at >= t.start_epoch
    AND l.created_at < t.end_epoch + $settle_seconds
    AND l.type IN (2, 5)
)
SELECT
  t.name AS window,
  t.request_path,
  t.is_stream,
  t.channel_id,
  t.model_name,
  t.upstream_model,
  count(*) AS touched_requests,
  count(f.request_id) AS finalized_requests,
  count(*) FILTER (WHERE f.request_id IS NULL AND NOT EXISTS (
    SELECT 1 FROM app_requests a
    WHERE a.period = t.name AND a.request_id = t.request_id AND a.client_canceled
  )) AS unresolved_requests
FROM touched t
LEFT JOIN finalized f
  ON f.name = t.name AND f.request_path = t.request_path AND f.request_id = t.request_id
  AND f.is_stream = t.is_stream AND f.channel_id = t.channel_id
  AND f.model_name = t.model_name AND f.upstream_model = t.upstream_model
GROUP BY t.name, t.request_path, t.is_stream, t.channel_id, t.model_name, t.upstream_model
UNION ALL
-- An HTTP 200 with neither a final DB record nor explicit cancellation is not success.
SELECT a.period, a.request_path, NULL::boolean, 0, '<unrecorded>', '<unrecorded>',
  count(*), 0::bigint, count(*)
FROM app_requests a
WHERE NOT a.client_canceled
  AND (a.status_code BETWEEN 200 AND 299 OR a.status_code >= 500)
  AND NOT EXISTS (SELECT 1 FROM touched t WHERE t.name = a.period AND t.request_id = a.request_id)
GROUP BY a.period, a.request_path
ORDER BY 1, 2, 3, 4, 5, 6;
SQL

comparison_result=0
python3 - "$output_dir/final-request-success.tsv" "$output_dir/final-request-coverage.tsv" "$output_dir/comparison.tsv" "$max_drop_bps" "$min_requests" <<'PY' || comparison_result=$?
import csv
import sys

source, coverage_source, target = sys.argv[1], sys.argv[2], sys.argv[3]
max_drop_bps, min_requests = int(sys.argv[4]), int(sys.argv[5])
# Keep cohort dimensions intact: aggregation can hide a stream/model regression.
cohort_fields = ("request_path", "is_stream", "channel_id", "model_name", "upstream_model")
rows = {}
with open(source, encoding="utf-8") as handle:
    for row in csv.DictReader(handle, delimiter="\t"):
        cohort = tuple(row[name].strip() for name in cohort_fields)
        rows[(row["window"].strip(), cohort)] = {
            "requests": int(row["final_requests"]), "successes": int(row["successes"])}

coverage = {}
with open(coverage_source, encoding="utf-8") as handle:
    for row in csv.DictReader(handle, delimiter="\t"):
        cohort = tuple(row[name].strip() for name in cohort_fields)
        coverage[(row["window"].strip(), cohort)] = int(row["unresolved_requests"])

cohorts = {key[1] for key in rows} | {key[1] for key in coverage}
# Missing protocols are explicit evidence gaps, not implicitly successful paths.
for path in ("/v1/messages", "/v1/responses", "/v1/chat/completions", "/gemini/content"):
    if not any(cohort[0] == path for cohort in cohorts):
        cohorts.add((path, "<missing>", "", "", ""))
failed = False
with open(target, "w", encoding="utf-8", newline="") as handle:
    writer = csv.writer(handle, delimiter="\t")
    writer.writerow([*cohort_fields, "pre_requests", "pre_success_bps", "post_requests", "post_success_bps", "drop_bps", "unresolved_requests", "model_evidence", "result"])
    for cohort in sorted(cohorts):
        pre = rows.get(("pre", cohort), {"requests": 0, "successes": 0})
        post = rows.get(("post", cohort), {"requests": 0, "successes": 0})
        pre_bps = round(10000 * pre["successes"] / pre["requests"]) if pre["requests"] else 0
        post_bps = round(10000 * post["successes"] / post["requests"]) if post["requests"] else 0
        drop_bps = pre_bps - post_bps
        unresolved = coverage.get(("pre", cohort), 0) + coverage.get(("post", cohort), 0)
        # Compare exact ratios at the threshold; rounded display values are not a gate.
        sufficient = pre["requests"] >= min_requests and post["requests"] >= min_requests
        model_evidence = "missing" if cohort[4] in ("", "<unrecorded>") else "recorded"
        passed = sufficient and unresolved == 0 and model_evidence == "recorded" and (
            10000 * (pre["successes"] * post["requests"] - post["successes"] * pre["requests"])
            <= max_drop_bps * pre["requests"] * post["requests"])
        failed = failed or not passed
        writer.writerow([*cohort, pre["requests"], pre_bps, post["requests"], post_bps, drop_bps, unresolved, model_evidence, "passed" if passed else "failed"])
sys.exit(1 if failed else 0)
PY

if ((comparison_result == 0)); then
  printf 'result=passed gate=protocol_stability channel_id=%s cutover_epoch=%s\n' "$channel_id" "$cutover_epoch" >"$output_dir/gate.result"
else
  printf 'result=failed gate=protocol_stability channel_id=%s cutover_epoch=%s\n' "$channel_id" "$cutover_epoch" >"$output_dir/gate.result"
fi

(cd "$output_dir" && sha256sum context.txt final-request-success.tsv final-request-coverage.tsv channel-attempt-errors.tsv comparison.tsv gate.result >SHA256SUMS
  if [[ -f http-observations.json ]]; then sha256sum http-observations.json http-observations.sql >>SHA256SUMS; fi)
cat "$output_dir/gate.result"
exit "$comparison_result"
