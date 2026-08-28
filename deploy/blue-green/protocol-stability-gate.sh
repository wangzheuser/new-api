#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_ENV="${SERVER_ENV:-$SCRIPT_DIR/server.env}"

usage() {
  cat <<'EOF'
Usage: protocol-stability-gate.sh --channel-id ID --cutover-epoch EPOCH --output-dir DIR [options]

Options:
  --seconds N          Equal pre/post measurement duration, default 10800.
  --drain-seconds N    Ignore the initial post-cutover cache drain, default 30.
  --settle-seconds N   Wait allowance for final request logs, default 300.
  --max-drop-bps N     Maximum success-rate drop in basis points, default 200.
  --min-requests N     Minimum final requests required per protocol, default 10.
  --exclude-file FILE  File containing one synthetic request ID per line; repeatable.

The gate counts one globally final, non-intermediate log per request ID. It also
records channel-attempt errors separately so retries do not reduce final success.
EOF
}

require_value() {
  [[ $# -ge 2 && -n "$2" ]] || {
    printf 'missing_value=%s\n' "$1" >&2
    exit 2
  }
}

channel_id=""
cutover_epoch=""
output_dir=""
seconds=10800
drain_seconds=30
settle_seconds=300
max_drop_bps=200
min_requests=10
exclude_files=()

while [[ $# -gt 0 ]]; do
  case "$1" in
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
  docker exec -i "$POSTGRES_CONTAINER" sh -lc \
    'psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -A -P pager=off -P footer=off -F "$(printf "\t")"'
}

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

run_psql >"$output_dir/final-request-success.tsv" <<SQL
WITH windows(name, start_epoch, end_epoch) AS (
  VALUES
    ('pre', $pre_start::bigint, $pre_end::bigint),
    ('post', $post_start::bigint, $post_end::bigint)
), touched AS (
  SELECT DISTINCT
    w.name,
    w.start_epoch,
    w.end_epoch,
    l.request_id,
    l.other::jsonb->>'request_path' AS request_path
  FROM windows w
  JOIN logs l ON l.created_at >= w.start_epoch AND l.created_at < w.end_epoch
  WHERE l.channel_id = $channel_id
    AND l.request_id <> ''
    AND l.request_id <> ALL ($excluded_sql)
    AND l.other::jsonb->>'request_path' IN ('/v1/messages', '/v1/responses')
), ranked AS (
  SELECT
    t.name,
    t.request_path,
    l.type,
    l.is_stream,
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
  count(*) AS final_requests,
  count(*) FILTER (WHERE type = 2) AS successes,
  count(*) FILTER (WHERE type = 5) AS failures,
  round(10000.0 * count(*) FILTER (WHERE type = 2) / NULLIF(count(*), 0))::bigint AS success_bps
FROM ranked
WHERE request_rank = 1
GROUP BY name, request_path, is_stream
ORDER BY name, request_path, is_stream;
SQL

run_psql >"$output_dir/channel-attempt-errors.tsv" <<SQL
WITH windows(name, start_epoch, end_epoch) AS (
  VALUES
    ('pre', $pre_start::bigint, $pre_end::bigint),
    ('post', $post_start::bigint, $post_end::bigint)
)
SELECT
  w.name AS window,
  l.other::jsonb->>'request_path' AS request_path,
  l.is_stream,
  COALESCE(l.is_intermediate, false) AS is_intermediate,
  COALESCE((l.other::jsonb->>'status_code')::int, 0) AS status_code,
  count(*) AS attempts
FROM windows w
JOIN logs l ON l.created_at >= w.start_epoch AND l.created_at < w.end_epoch
WHERE l.channel_id = $channel_id
  AND l.type = 5
  AND l.request_id <> ALL ($excluded_sql)
  AND l.other::jsonb->>'request_path' IN ('/v1/messages', '/v1/responses')
GROUP BY w.name, request_path, l.is_stream, COALESCE(l.is_intermediate, false), status_code
ORDER BY w.name, request_path, l.is_stream, is_intermediate, status_code;
SQL

run_psql >"$output_dir/final-request-coverage.tsv" <<SQL
WITH windows(name, start_epoch, end_epoch) AS (
  VALUES
    ('pre', $pre_start::bigint, $pre_end::bigint),
    ('post', $post_start::bigint, $post_end::bigint)
), touched AS (
  SELECT DISTINCT
    w.name,
    w.start_epoch,
    w.end_epoch,
    l.request_id,
    l.other::jsonb->>'request_path' AS request_path
  FROM windows w
  JOIN logs l ON l.created_at >= w.start_epoch AND l.created_at < w.end_epoch
  WHERE l.channel_id = $channel_id
    AND l.request_id <> ''
    AND l.request_id <> ALL ($excluded_sql)
    AND l.other::jsonb->>'request_path' IN ('/v1/messages', '/v1/responses')
), finalized AS (
  SELECT DISTINCT t.name, t.request_path, t.request_id
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
  count(*) AS touched_requests,
  count(f.request_id) AS finalized_requests,
  count(*) - count(f.request_id) AS unresolved_requests
FROM touched t
LEFT JOIN finalized f
  ON f.name = t.name AND f.request_path = t.request_path AND f.request_id = t.request_id
GROUP BY t.name, t.request_path
ORDER BY t.name, t.request_path;
SQL

comparison_result=0
python3 - "$output_dir/final-request-success.tsv" "$output_dir/final-request-coverage.tsv" "$output_dir/comparison.tsv" "$max_drop_bps" "$min_requests" <<'PY' || comparison_result=$?
import csv
import sys

source, coverage_source, target = sys.argv[1], sys.argv[2], sys.argv[3]
max_drop_bps, min_requests = int(sys.argv[4]), int(sys.argv[5])
rows = {}
with open(source, encoding="utf-8") as handle:
    for row in csv.DictReader(handle, delimiter="\t"):
        path = row["request_path"].strip()
        window = row["window"].strip()
        item = rows.setdefault((window, path), {"requests": 0, "successes": 0})
        item["requests"] += int(row["final_requests"])
        item["successes"] += int(row["successes"])

coverage = {}
with open(coverage_source, encoding="utf-8") as handle:
    for row in csv.DictReader(handle, delimiter="\t"):
        coverage[(row["window"].strip(), row["request_path"].strip())] = int(row["unresolved_requests"])

failed = False
with open(target, "w", encoding="utf-8", newline="") as handle:
    writer = csv.writer(handle, delimiter="\t")
    writer.writerow(["request_path", "pre_requests", "pre_success_bps", "post_requests", "post_success_bps", "drop_bps", "unresolved_requests", "result"])
    for path in ("/v1/messages", "/v1/responses"):
        pre = rows.get(("pre", path), {"requests": 0, "successes": 0})
        post = rows.get(("post", path), {"requests": 0, "successes": 0})
        pre_bps = round(10000 * pre["successes"] / pre["requests"]) if pre["requests"] else 0
        post_bps = round(10000 * post["successes"] / post["requests"]) if post["requests"] else 0
        drop_bps = pre_bps - post_bps
        unresolved = coverage.get(("pre", path), 0) + coverage.get(("post", path), 0)
        passed = pre["requests"] >= min_requests and post["requests"] >= min_requests and drop_bps <= max_drop_bps and unresolved == 0
        failed = failed or not passed
        writer.writerow([path, pre["requests"], pre_bps, post["requests"], post_bps, drop_bps, unresolved, "passed" if passed else "failed"])
sys.exit(1 if failed else 0)
PY

if ((comparison_result == 0)); then
  printf 'result=passed gate=protocol_stability channel_id=%s cutover_epoch=%s\n' "$channel_id" "$cutover_epoch" >"$output_dir/gate.result"
else
  printf 'result=failed gate=protocol_stability channel_id=%s cutover_epoch=%s\n' "$channel_id" "$cutover_epoch" >"$output_dir/gate.result"
fi

(cd "$output_dir" && sha256sum context.txt final-request-success.tsv final-request-coverage.tsv channel-attempt-errors.tsv comparison.tsv gate.result >SHA256SUMS)
cat "$output_dir/gate.result"
exit "$comparison_result"
