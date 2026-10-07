#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RELEASE_ENV="${RELEASE_ENV:-$SCRIPT_DIR/release.env}"
SERVER_ENV="${SERVER_ENV:-$SCRIPT_DIR/server.env}"
COMPOSE_TEMPLATE="${COMPOSE_TEMPLATE:-$SCRIPT_DIR/docker-compose.slot.yml}"

usage() {
  cat <<'EOF'
Usage: release-remote.sh <self-check|doctor|recover|status|backup|stage|gate|cutover|observe|finalize|rollback|cleanup> [options]

Mutating production actions require:
  cutover  --execute   CONFIRM_CUTOVER=<release-id>
  finalize --execute [--accept-inconclusive]  CONFIRM_FINALIZE=<release-id>
  rollback --execute  CONFIRM_ROLLBACK=<release-id>
  cleanup --execute   CONFIRM_CLEANUP=<release-id> (permanently retires old rollback assets)
  cleanup [--dry-run|--execute] [--accept-current] (explicit acceptance of a completed non-passing observation)

Observation gate:
  observe [--seconds N] [--interval N] defaults to 600 seconds / 30 seconds
  finalize requires a successful observation of at least 600 seconds
  --accept-inconclusive requires ACCEPT_INCONCLUSIVE=1 and a non-empty ACCEPT_REASON;
  it requires the candidate gate, public browser check, bounded hold and zero unexplained HTTP 5xx;
  it records an explicit release decision without rewriting observation.result
  an inconclusive observation requires ALLOW_INCONCLUSIVE_ROLLBACK=1 for an explicit rollback
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'missing_command=%s\n' "$1" >&2
    exit 1
  }
}

require_variable() {
  [[ -n "${!1:-}" ]] || {
    printf 'missing_variable=%s\n' "$1" >&2
    exit 1
  }
}

load_config() {
  [[ -r "$RELEASE_ENV" && -r "$SERVER_ENV" && -r "$COMPOSE_TEMPLATE" ]]
  # Both files are generated from trusted deployment inputs and contain no shell commands.
  # shellcheck disable=SC1090
  source "$RELEASE_ENV"
  # shellcheck disable=SC1090
  source "$SERVER_ENV"
  for name in RELEASE_ID COMMIT_SHA VERSION IMAGE_TAG IMAGE_ARCHIVE IMAGE_SHA256 \
    BACKUP_ROOT APP_NETWORK PROXY_NETWORK PROXY_CONTAINER PUBLIC_STATUS_URL \
    POSTGRES_CONTAINER POSTGRES_USER POSTGRES_DB REDIS_CONTAINER NGINX_ACCESS_LOG \
    BLUE_DATA_DIR BLUE_LOG_DIR BLUE_NODE_NAME BLUE_PROJECT BLUE_RUNTIME_ENV_FILE \
    GREEN_DATA_DIR GREEN_LOG_DIR GREEN_NODE_NAME GREEN_PROJECT GREEN_RUNTIME_ENV_FILE; do
    require_variable "$name"
  done
  RELEASE_DIR="$(cd "$(dirname "$RELEASE_ENV")" && pwd)"
  export APP_NETWORK
  export GATEWAY_CONTAINER="${GATEWAY_CONTAINER:-new-api-internal-gateway}"
  export GATEWAY_CONFIG="${GATEWAY_CONFIG:-/opt/docker_projects/new-api-internal-gateway/gateway.conf}"
  export INTERNAL_GATEWAY_URL="${INTERNAL_GATEWAY_URL:-http://127.0.0.1:19001}"
  IMAGE_PATH="$RELEASE_DIR/$IMAGE_ARCHIVE"
  STATE_DIR="$RELEASE_DIR/state"
  mkdir -p "$STATE_DIR"
  chmod 700 "$STATE_DIR"
}

sha256_file() {
  sha256sum "$1" | awk '{print $1}'
}

gateway_control() {
  python3 "$SCRIPT_DIR/gateway_control.py" "$@"
}

production_container() {
  gateway_control status | python3 -c 'import json,sys; print(json.load(sys.stdin)["slot"])'
}

other_container() {
  [[ "$1" == new-api-blue ]] && printf 'new-api-green' || printf 'new-api-blue'
}

slot_name() {
  printf '%s' "${1#new-api-}"
}

slot_value() {
  local slot="${1^^}" suffix="$2" name
  name="${slot}_${suffix}"
  printf '%s' "${!name}"
}

# Read the application network address without publishing a slot on the host.
container_url() {
  local ip
  ip="$(docker inspect "$1" | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["NetworkSettings"]["Networks"][sys.argv[1]]["IPAddress"])' "$APP_NETWORK")"
  [[ -n "$ip" ]]
  printf 'http://%s:3000' "$ip"
}

container_version() {
  docker exec "$1" wget -qO- --timeout=10 http://127.0.0.1:3000/api/status |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["version"])'
}

# Legacy standby containers must not reclaim the gateway's permanent port.
check_rollback_port() {
  docker inspect "$1" | python3 -c 'import json,sys; c=json.load(sys.stdin)[0]; assert not any(p.get("HostPort") == "19001" for ps in (c["HostConfig"].get("PortBindings") or {}).values() for p in (ps or [])), "legacy rollback slot owns gateway port; migrate it first"'
}

# Both stable entrances must reach the same release before a transition succeeds.
proxy_version() {
  local version gateway
  version="$(docker exec "$PROXY_CONTAINER" wget -qO- --timeout=10 "http://host.docker.internal:19001/api/status" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["version"])')" || return 1
  gateway="$(gateway_control status |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])')" || return 1
  [[ "$gateway" == "$version" ]] || return 1
  printf '%s' "$version"
}

public_version() {
  local i body status
  for i in $(seq 1 20); do
    body="$(mktemp)"
    status="$(curl -sS --max-time 15 -o "$body" -w '%{http_code}' "$PUBLIC_STATUS_URL" || true)"
    if [[ "$status" == 200 ]]; then
      python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["data"]["version"])' "$body"
      rm -f "$body"
      return 0
    fi
    rm -f "$body"
    [[ "$status" == 429 ]] || return 1
    sleep 5
  done
  return 1
}

wait_healthy() {
  local container="$1" i status
  # Online index creation can extend startup on large production tables.
  for i in $(seq 1 1800); do
    status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container" 2>/dev/null || true)"
    if [[ "$status" == healthy ]]; then
      printf 'candidate_health=healthy container=%s elapsed_seconds=%s\n' "$container" "$((i * 2))"
      return 0
    fi
    if ((i % 15 == 0)); then
      printf 'candidate_health=waiting container=%s status=%s elapsed_seconds=%s\n' "$container" "${status:-missing}" "$((i * 2))"
    fi
    sleep 2
  done
  printf 'candidate_health=timeout container=%s status=%s elapsed_seconds=3600\n' "$container" "${status:-missing}" >&2
  return 1
}

nginx_hash() {
  docker exec "$PROXY_CONTAINER" nginx -T 2>/dev/null | sha256sum | awk '{print $1}'
}

nginx_hash_matches() {
  local expected="$1" i
  for i in 1 2 3; do
    [[ "$(nginx_hash)" == "$expected" ]] && return 0
    [[ "$i" -eq 3 ]] || sleep 2
  done
  return 1
}

check_ingress() {
  local project="${NGINX_PROJECT_DIR:-/opt/docker_projects/nginx-proxy}" rendered
  # Hash equality alone cannot bless an unsafe historical baseline.
  rendered="$(cd "$project" && python3 tools/render-nginx-conf.py)"
  if ! grep -qx 'compare=identical' <<<"$rendered"; then
    printf 'ingress_blocked=config_source_drift\n' >&2
    return 1
  fi
  docker exec "$PROXY_CONTAINER" nginx -T 2>/dev/null | python3 -c '
import re,sys
s=sys.stdin.read()
assert not re.search(r"(?:proxy_pass\s+https?://|server\s+)new-api-(?:blue|green)(?=[:;/\s])",s), "legacy_application_upstream"
assert "proxy_pass http://host.docker.internal:19001;" in s, "stable_gateway_route_missing"
'
}

action_doctor() {
  load_config
  check_ingress
  gateway_control doctor
  [[ "$(proxy_version)" == "$(public_version)" ]]
  printf 'doctor=passed\n'
}

action_recover() {
  load_config
  [[ "${1:-}" == --execute && "${CONFIRM_RECOVER:-}" == "$RELEASE_ID" ]]
  gateway_control recover
  action_doctor
}

render_compose() {
  local slot="$1" service="new-api-$1"
  export SLOT="$slot"
  export DATA_DIR="$(slot_value "$slot" DATA_DIR)"
  export LOG_DIR="$(slot_value "$slot" LOG_DIR)"
  export NODE_NAME="$(slot_value "$slot" NODE_NAME)"
  export RUNTIME_ENV_FILE="$(slot_value "$slot" RUNTIME_ENV_FILE)"
  export APP_NETWORK RUNTIME_ENV_FILE IMAGE_TAG
  sed "s/^  new-api:/  $service:/" "$COMPOSE_TEMPLATE" |
    docker compose -p "$(slot_value "$slot" PROJECT)" -f - config
}

action_self_check() {
  for command in bash cmp curl docker flock python3 sha256sum tar zstd; do
    require_command "$command"
  done
  bash -n "$0"
  bash -n "$SCRIPT_DIR/protocol-stability-gate.sh"
  python3 -c 'import sys; compile(open(sys.argv[1]).read(), sys.argv[1], "exec")' "$SCRIPT_DIR/low-traffic-evidence.py"
  python3 -c 'import sys; compile(open(sys.argv[1]).read(), sys.argv[1], "exec")' "$SCRIPT_DIR/retention.py"
  python3 -c 'import sys; compile(open(sys.argv[1]).read(), sys.argv[1], "exec")' "$SCRIPT_DIR/gateway_control.py"
  load_config
  local slot
  for slot in blue green; do
    render_compose "$slot" | docker compose -f - config --format json |
      python3 -c 'import json,sys; c=json.load(sys.stdin); assert all(not s.get("ports") for s in c["services"].values()), "slot must not publish host ports"'
  done
  printf 'self_check=passed release=%s version=%s\n' "$RELEASE_ID" "$VERSION"
}

action_status() {
  load_config
  local production candidate candidate_state candidate_version observation=missing decision=missing
  production="$(production_container)"
  candidate="$(other_container "$production")"
  if candidate_state="$(docker inspect -f '{{.State.Status}}' "$candidate" 2>/dev/null)"; then
    candidate_version="$(container_version "$candidate" 2>/dev/null || printf stopped)"
  else
    # Docker may write a newline before failing for a missing container.
    candidate_state=absent
    candidate_version=absent
  fi
  printf 'production=%s production_version=%s candidate=%s candidate_state=%s candidate_version=%s\n' \
    "$production" "$(container_version "$production")" "$candidate" \
    "$candidate_state" "$candidate_version"
  if [[ -r "$STATE_DIR/observation.result" ]]; then
    observation="$(awk '{print $1}' "$STATE_DIR/observation.result")"
    observation="${observation#observation=}"
  fi
  if [[ -r "$STATE_DIR/decision.result" ]]; then
    decision="$(awk '{print $1}' "$STATE_DIR/decision.result")"
    decision="${decision#release_decision=}"
  fi
  printf 'observation=%s release_decision=%s\n' "$observation" "$decision"
}

action_backup() {
  load_config
  umask 077
  mkdir -p "$BACKUP_ROOT"
  local backup_lock_fd
  exec {backup_lock_fd}>"$BACKUP_ROOT/.backup.lock"
  # Serialize backups across releases so a timed-out caller cannot start a
  # second pg_dump while the original process is still running.
  flock "$backup_lock_fd"

  local backup_dir="$BACKUP_ROOT/$RELEASE_ID" dump="$BACKUP_ROOT/$RELEASE_ID/postgresql.dump"
  local exclusion_manifest="$backup_dir/postgresql.excluded-table-data.txt"
  local -a excluded_table_data=(
    public.logs
    public.conversation_logs
  )
  local -a exclude_table_data_args=()
  local qualified_table
  for qualified_table in "${excluded_table_data[@]}"; do
    exclude_table_data_args+=("--exclude-table-data=$qualified_table")
  done
  if [[ -r "$dump" && -r "$dump.sha256" && -s "$backup_dir/postgresql.restore-list.txt" ]] && \
    (cd "$backup_dir" && sha256sum -c postgresql.dump.sha256 >/dev/null) && \
    [[ -r "$exclusion_manifest" ]] && \
    cmp -s "$exclusion_manifest" <(printf '%s\n' "${excluded_table_data[@]}"); then
    printf 'backup=already-complete directory=%s\n' "$backup_dir"
    return
  fi
  mkdir -p "$backup_dir"
  chmod 700 "$backup_dir"
  local production
  production="$(production_container)"
  docker inspect "$production" > "$backup_dir/production-container.inspect.json"
  docker image inspect "$(docker inspect -f '{{.Image}}' "$production")" > "$backup_dir/production-image.inspect.json"
  docker network inspect "$APP_NETWORK" > "$backup_dir/app-network.inspect.json"
  docker network inspect "$PROXY_NETWORK" > "$backup_dir/proxy-network.inspect.json"
  docker exec "$PROXY_CONTAINER" nginx -T \
    > "$backup_dir/nginx-config.txt" \
    2> "$backup_dir/nginx-config.stderr.txt"
  printf '%s  nginx-config.txt\n' "$(sha256_file "$backup_dir/nginx-config.txt")" > "$backup_dir/nginx-config.sha256"
  docker inspect "$production" | python3 -c 'import json,sys; env=json.load(sys.stdin)[0]["Config"]["Env"]; sensitive=("SECRET","PASSWORD","TOKEN","DSN","KEY","COOKIE"); print("\n".join(x.split("=",1)[0]+"=<redacted>" if any(k in x.split("=",1)[0].upper() for k in sensitive) else x for x in env))' > "$backup_dir/runtime-env.sanitized"
  # Retain log table schemas while omitting their high-volume row data.
  docker exec "$POSTGRES_CONTAINER" pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc -Z1 \
    "${exclude_table_data_args[@]}" > "$dump"
  printf '%s\n' "${excluded_table_data[@]}" > "$exclusion_manifest"
  (cd "$backup_dir" && sha256sum postgresql.dump > postgresql.dump.sha256)
  docker exec -i "$POSTGRES_CONTAINER" pg_restore -l < "$dump" > "$backup_dir/postgresql.restore-list.txt"
  [[ -s "$backup_dir/postgresql.restore-list.txt" ]]
  while IFS=. read -r excluded_schema excluded_table; do
    if awk -v schema="$excluded_schema" -v table="$excluded_table" \
      '$4 == "TABLE" && $5 == "DATA" && $6 == schema && $7 == table { found = 1 } END { exit found ? 0 : 1 }' \
      "$backup_dir/postgresql.restore-list.txt"; then
      printf 'excluded_table_data_present=%s.%s\n' "$excluded_schema" "$excluded_table" >&2
      return 1
    fi
  done < "$exclusion_manifest"
  (cd "$backup_dir" && sha256sum -c postgresql.dump.sha256 >/dev/null)
  find "$backup_dir" -type f -exec chmod 600 {} +
  # Prior verified backups are rollback assets, not disposable files from this release.
  printf 'backup=passed directory=%s bytes=%s\n' "$backup_dir" "$(stat -c %s "$dump")"
}

action_stage() {
  load_config
  [[ -r "$IMAGE_PATH" ]]
  [[ "$(sha256_file "$IMAGE_PATH")" == "$IMAGE_SHA256" ]]
  if [[ "$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$IMAGE_TAG" 2>/dev/null || true)" != "$COMMIT_SHA" ]]; then
    zstd -t "$IMAGE_PATH" >/dev/null
    zstd -dc "$IMAGE_PATH" | docker load >/dev/null
  fi
  [[ "$(docker image inspect -f '{{.Os}}/{{.Architecture}}' "$IMAGE_TAG")" == linux/amd64 ]]
  [[ "$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$IMAGE_TAG")" == "$COMMIT_SHA" ]]

  local production candidate slot project service compose_file
  production="$(production_container)"
  candidate="$(other_container "$production")"
  slot="$(slot_name "$candidate")"
  project="$(slot_value "$slot" PROJECT)"
  service="new-api-$slot"
  compose_file="$STATE_DIR/$slot.compose.rendered.yml"
  render_compose "$slot" > "$compose_file"
  mkdir -p "$DATA_DIR" "$LOG_DIR"
  # Compose recreates the slot when its image or configuration changed; retries keep an already-correct candidate running.
  docker compose -p "$project" -f "$compose_file" up -d --no-deps --no-build "$service"
  wait_healthy "$candidate"
  [[ "$(container_version "$candidate")" == "$VERSION" ]]
  networks="$(docker inspect "$candidate" | python3 -c 'import json,sys; print(",".join(sorted(json.load(sys.stdin)[0]["NetworkSettings"]["Networks"])))')"
  [[ "$networks" == "$APP_NETWORK" ]]
  umask 077
  cat > "$STATE_DIR/stage.env" <<EOF
PRODUCTION=$production
CANDIDATE=$candidate
EOF
  chmod 600 "$STATE_DIR/stage.env"
  printf 'stage=passed production=%s candidate=%s version=%s networks=%s\n' "$production" "$candidate" "$VERSION" "$networks"
}

action_gate() {
  # Missing observation code must fail before production cutover.
  bash -n "$SCRIPT_DIR/protocol-stability-gate.sh"
  python3 -c 'import sys; compile(open(sys.argv[1]).read(), sys.argv[1], "exec")' "$SCRIPT_DIR/low-traffic-evidence.py"
  python3 -c 'import sys; compile(open(sys.argv[1]).read(), sys.argv[1], "exec")' "$SCRIPT_DIR/retention.py"
  load_config
  local candidate_url
  check_ingress
  gateway_control doctor >/dev/null
  # shellcheck disable=SC1090
  source "$STATE_DIR/stage.env"
  [[ "$(docker inspect -f '{{.State.Health.Status}}' "$CANDIDATE")" == healthy ]]
  [[ "$(docker inspect -f '{{.RestartCount}}' "$CANDIDATE")" == 0 ]]
  [[ "$(docker inspect -f '{{.State.OOMKilled}}' "$CANDIDATE")" == false ]]
  [[ "$(container_version "$CANDIDATE")" == "$VERSION" ]]
  docker inspect "$CANDIDATE" | python3 -c 'import json,sys; assert not json.load(sys.stdin)[0]["HostConfig"].get("PortBindings"), "slot must not publish host ports"'
  candidate_url="$(container_url "$CANDIDATE")"
  docker inspect "$CANDIDATE" | python3 -c 'import json,sys; n=json.load(sys.stdin)[0]["NetworkSettings"]["Networks"]; assert set(n)=={sys.argv[1]} and not (n[sys.argv[1]].get("IPAMConfig") or {}).get("IPv4Address"), "candidate_network_invalid"' "$APP_NETWORK"
  [[ "$(docker inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$CANDIDATE")" == "$COMMIT_SHA" ]]
  [[ "$(docker exec "$CANDIDATE" printenv NODE_NAME)" != "$(docker exec "$PRODUCTION" printenv NODE_NAME)" ]]
  for field in Memory MemorySwap PidsLimit NanoCpus; do
    [[ "$(docker inspect -f "{{.HostConfig.$field}}" "$CANDIDATE")" == "$(docker inspect -f "{{.HostConfig.$field}}" "$PRODUCTION")" ]]
  done
  docker exec "$CANDIDATE" getent hosts "$POSTGRES_CONTAINER" >/dev/null
  docker exec "$CANDIDATE" getent hosts "$REDIS_CONTAINER" >/dev/null
  curl --noproxy '*' -fsS --max-time 10 "$candidate_url/" -o "$STATE_DIR/candidate-index.html"
  grep -q 'id="root"' "$STATE_DIR/candidate-index.html"
  python3 -c 'import re,sys; print("\n".join(re.findall(r"(?:src|href)=\"(/static/[^\"?#]+)",open(sys.argv[1]).read())))' "$STATE_DIR/candidate-index.html" |
    while IFS= read -r asset; do [[ -z "$asset" ]] || curl --noproxy '*' -fsS --max-time 10 -o /dev/null "$candidate_url$asset"; done
  headers="$(curl --noproxy '*' -sS --max-time 10 -D - -o /dev/null "$candidate_url/static/deploy-missing-$RELEASE_ID.js")"
  grep -qE '^HTTP/[^ ]+ 404' <<<"$headers"
  grep -qiE '^Cache-Control:.*no-store' <<<"$headers"
  baseline_hash="$(awk '{print $1}' "$BACKUP_ROOT/$RELEASE_ID/nginx-config.sha256")"
  nginx_hash_matches "$baseline_hash"
  if docker logs --since 10m "$CANDIDATE" 2>&1 | grep -Eqi 'panic:|fatal|out of memory|connection refused'; then
    printf 'candidate_log_gate=failed\n' >&2
    exit 1
  fi
  printf 'gate=passed candidate=%s version=%s\n' "$CANDIDATE" "$VERSION" | tee "$STATE_DIR/gate.result"
}

action_cutover() {
  load_config
  local mode="${1:---dry-run}"
  # shellcheck disable=SC1090
  source "$STATE_DIR/stage.env"
  [[ "$(cat "$STATE_DIR/gate.result")" == "gate=passed candidate=$CANDIDATE version=$VERSION" ]]
  check_ingress
  if [[ "$(production_container)" == "$CANDIDATE" && "$(proxy_version)" == "$VERSION" ]]; then
    [[ -r "$STATE_DIR/role-state.env" ]]
    printf 'cutover=already-complete production=%s version=%s\n' "$CANDIDATE" "$VERSION"
    return
  fi
  [[ "$(production_container)" == "$PRODUCTION" ]]
  [[ "$(docker inspect -f '{{.State.Health.Status}}' "$CANDIDATE")" == healthy ]]
  [[ "$(container_version "$CANDIDATE")" == "$VERSION" ]]
  local production_version baseline_hash
  production_version="$(container_version "$PRODUCTION")"
  baseline_hash="$(awk '{print $1}' "$BACKUP_ROOT/$RELEASE_ID/nginx-config.sha256")"
  nginx_hash_matches "$baseline_hash"
  if [[ "$mode" == --dry-run ]]; then
    printf 'cutover_dry_run=passed production=%s candidate=%s production_version=%s target_version=%s\n' "$PRODUCTION" "$CANDIDATE" "$production_version" "$VERSION"
    return
  fi
  [[ "$mode" == --execute && "${CONFIRM_CUTOVER:-}" == "$RELEASE_ID" ]]
  local cutover_at cutover_epoch
  cutover_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  cutover_epoch="$(date +%s)"
  umask 077
  cat > "$STATE_DIR/role-state.env.pending" <<EOF
ROUTING_SCHEMA=2
OLD=$PRODUCTION
NEW=$CANDIDATE
OLD_VERSION=$production_version
NEW_VERSION=$VERSION
CUTOVER_AT=$cutover_at
CUTOVER_EPOCH=$cutover_epoch
EOF
  mv "$STATE_DIR/role-state.env.pending" "$STATE_DIR/role-state.env"
  chmod 600 "$STATE_DIR/role-state.env"
  restore_on_error() {
    local rc=$?
    trap - ERR INT TERM
    if gateway_control recover >/dev/null && gateway_control switch --slot "$PRODUCTION" --version "$production_version" &&
       [[ "$(proxy_version)" == "$production_version" && "$(public_version)" == "$production_version" ]]; then
      printf 'cutover_recovery=restored\n' >&2
    else
      printf 'cutover_recovery=failed standby_preserved=1\n' >&2
    fi
    printf 'cutover_failed rc=%s\n' "$rc" >&2
    exit 1
  }
  trap restore_on_error ERR INT TERM
  gateway_control switch --slot "$CANDIDATE" --version "$VERSION"
  [[ "$(proxy_version)" == "$VERSION" ]]
  [[ "$(public_version)" == "$VERSION" ]]
  nginx_hash_matches "$baseline_hash"
  trap - ERR INT TERM
  printf 'cutover=passed production=%s standby=%s version=%s\n' "$CANDIDATE" "$PRODUCTION" "$VERSION"
}

action_observe() {
  load_config
  local seconds=600 interval=30
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --seconds) seconds="$2"; shift 2 ;;
      --interval) interval="$2"; shift 2 ;;
      *) usage >&2; exit 2 ;;
    esac
  done
  if [[ ! "$seconds" =~ ^[1-9][0-9]*$ || ! "$interval" =~ ^[1-9][0-9]*$ ]]; then
    printf 'invalid_observation_timing seconds=%s interval=%s\n' "$seconds" "$interval" >&2
    exit 2
  fi
  # shellcheck disable=SC1090
  source "$STATE_DIR/role-state.env"
  local baseline_hash start end start_tick deadline now remaining sleep_seconds
  local observation_log baseline_log baseline_start
  local checks=0 sample_count=0 errors_5xx=0 elapsed_seconds=""
  local baseline_samples baseline_errors_5xx baseline_rate_bps current_rate_bps allowed_rate_bps allowed_errors_5xx
  # Initialize timing before installing the trap, including failures before the first check.
  start="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  start_tick="$SECONDS"
  observe_on_error() {
    local rc=$?
    trap - ERR
    printf 'observation=failed release_id=%s production=%s version=%s requested_seconds=%s elapsed_seconds=%s interval=%s checks=%s start=%s end=%s samples=%s errors_5xx=%s line=%s command=%s rc=%s\n' \
      "$RELEASE_ID" "$NEW" "$VERSION" "$seconds" "${elapsed_seconds:-$((SECONDS - start_tick))}" \
      "$interval" "$checks" "$start" "${end:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" \
      "$sample_count" "$errors_5xx" "$LINENO" "$BASH_COMMAND" "$rc" |
      tee "$STATE_DIR/observation.result" >&2
    exit "$rc"
  }
  trap observe_on_error ERR
  [[ "${CUTOVER_AT:-}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]
  [[ "${CUTOVER_EPOCH:-}" =~ ^[0-9]+$ ]]
  baseline_hash="$(awk '{print $1}' "$BACKUP_ROOT/$RELEASE_ID/nginx-config.sha256")"
  start="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  # Bash's elapsed-time counter prevents wall-clock corrections from shortening the window.
  start_tick="$SECONDS"
  deadline=$(( start_tick + seconds ))
  printf 'observation=running release_id=%s production=%s requested_seconds=%s interval=%s start=%s\n' \
    "$RELEASE_ID" "$NEW" "$seconds" "$interval" "$start" > "$STATE_DIR/observation.result"
  [[ "$(public_version)" == "$VERSION" ]]
  while true; do
    [[ "$(docker inspect -f '{{.State.Health.Status}}' "$NEW")" == healthy ]]
    [[ "$(docker inspect -f '{{.RestartCount}}' "$NEW")" == 0 ]]
    [[ "$(docker inspect -f '{{.State.OOMKilled}}' "$NEW")" == false ]]
    [[ "$(proxy_version)" == "$VERSION" ]]
    nginx_hash_matches "$baseline_hash"
    checks=$(( checks + 1 ))
    now="$SECONDS"
    (( now >= deadline )) && break
    remaining=$(( deadline - now ))
    sleep_seconds="$interval"
    (( sleep_seconds <= remaining )) || sleep_seconds="$remaining"
    sleep "$sleep_seconds"
  done
  [[ "$(public_version)" == "$VERSION" ]]
  end="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  elapsed_seconds=$(( SECONDS - start_tick ))
  (( elapsed_seconds >= seconds ))
  observation_log="$STATE_DIR/observation-app.log"
  docker logs --since "$start" --until "$(( $(date -d "$start" +%s) + seconds ))" "$NEW" > "$observation_log" 2>&1
  chmod 600 "$observation_log"
  baseline_log="$STATE_DIR/observation-baseline-app.log"
  baseline_start=$(( CUTOVER_EPOCH - seconds ))
  (( baseline_start > 0 ))
  docker logs --since "$baseline_start" --until "$CUTOVER_AT" "$OLD" > "$baseline_log" 2>&1
  chmod 600 "$baseline_log"
  # The shared Nginx access log contains unrelated virtual hosts. Compare only
  # application requests from the new slot with the immediately preceding
  # rollback-baseline window; tolerate at most a two-percentage-point increase.
  sample_count="$(grep -Ec '^\[GIN\].*\|[[:space:]]+[0-9]{3}[[:space:]]+\|' "$observation_log" || true)"
  # Empty natural traffic is handled by the explicit low-traffic evidence path below.
  errors_5xx="$(grep -Ec '^\[GIN\].*\|[[:space:]]+5[0-9][0-9][[:space:]]+\|' "$observation_log" || true)"
  baseline_samples="$(grep -Ec '^\[GIN\].*\|[[:space:]]+[0-9]{3}[[:space:]]+\|' "$baseline_log" || true)"
  # Preserve a zero baseline; it is not a measured zero-percent error rate.
  baseline_errors_5xx="$(grep -Ec '^\[GIN\].*\|[[:space:]]+5[0-9][0-9][[:space:]]+\|' "$baseline_log" || true)"
  baseline_rate_bps=$(( baseline_errors_5xx * 10000 / (baseline_samples > 0 ? baseline_samples : 1) ))
  current_rate_bps=$(( errors_5xx * 10000 / (sample_count > 0 ? sample_count : 1) ))
  allowed_rate_bps=$(( baseline_rate_bps + 200 ))
  # A finite request window cannot realize every basis-point rate exactly. Round
  # the allowed error count up so the fractional boundary does not reject one request.
  allowed_errors_5xx=$(( (sample_count * allowed_rate_bps + 9999) / 10000 ))
  printf 'baseline_samples=%s baseline_errors_5xx=%s baseline_rate_bps=%s samples=%s errors_5xx=%s current_rate_bps=%s allowed_rate_bps=%s allowed_errors_5xx=%s\n' \
    "$baseline_samples" "$baseline_errors_5xx" "$baseline_rate_bps" "$sample_count" "$errors_5xx" "$current_rate_bps" "$allowed_rate_bps" "$allowed_errors_5xx" \
    > "$STATE_DIR/observation.metrics"
  chmod 600 "$STATE_DIR/observation.metrics"
  # Shared-database business evidence is an additional gate, not proof of slot attribution.
  # Keep original HTTP failures intact; only separately reviewed exceptions affect regression counts.
  local protocol_result=0 settle_seconds=300 ready_epoch wait_seconds
  ready_epoch=$(( $(date -d "$start" +%s) + seconds + settle_seconds ))
  wait_seconds=$(( ready_epoch - $(date +%s) ))
  if (( wait_seconds > 0 )); then
    sleep "$wait_seconds"
  fi
  local protocol_args=() protocol_dir="$STATE_DIR/protocol-stability-$(date +%s)"
  local verified_pre=0 verified_post=0 actionable_errors actionable_baseline_errors
  if [[ -r "$STATE_DIR/verified-upstream-503.json" ]]; then
    protocol_args+=(--verified-upstream-503-file "$STATE_DIR/verified-upstream-503.json")
  fi
  if [[ -r "$STATE_DIR/synthetic-request-ids.txt" ]]; then
    protocol_args+=(--exclude-file "$STATE_DIR/synthetic-request-ids.txt")
  fi
  SERVER_ENV="$SERVER_ENV" bash "$SCRIPT_DIR/protocol-stability-gate.sh" \
    --cutover-epoch "$CUTOVER_EPOCH" --seconds "$seconds" \
    --drain-seconds "$(( $(date -d "$start" +%s) - CUTOVER_EPOCH ))" \
    --baseline-log "$baseline_log" --observation-log "$observation_log" \
    --settle-seconds "$settle_seconds" --output-dir "$protocol_dir" \
    "${protocol_args[@]}" || protocol_result=$?
  printf 'protocol_stability_rc=%s\n' "$protocol_result" >> "$STATE_DIR/observation.metrics"
  # Only reviewed, request-correlated external 503s are removed from regression counts.
  # Raw HTTP metrics and unsuccessful business outcomes remain available for audit.
  if [[ -r "$protocol_dir/verified-upstream-counts.txt" ]]; then
    read -r verified_pre verified_post < "$protocol_dir/verified-upstream-counts.txt"
    [[ "$verified_pre" =~ ^[0-9]+$ && "$verified_post" =~ ^[0-9]+$ ]]
  fi
  (( verified_pre <= baseline_errors_5xx && verified_post <= errors_5xx ))
  actionable_errors=$(( errors_5xx - verified_post ))
  actionable_baseline_errors=$(( baseline_errors_5xx - verified_pre ))
  (( baseline_samples >= verified_pre && sample_count >= verified_post ))
  local actionable_allowed_errors_5xx
  actionable_allowed_errors_5xx=0
  if (( baseline_samples > verified_pre )); then
    actionable_allowed_errors_5xx=$(( ((sample_count - verified_post) * (actionable_baseline_errors * 10000 / (baseline_samples - verified_pre) + 200) + 9999) / 10000 ))
  fi
  printf 'verified_upstream_503_pre=%s verified_upstream_503_post=%s actionable_errors_5xx=%s actionable_allowed_errors_5xx=%s\n' \
    "$verified_pre" "$verified_post" "$actionable_errors" "$actionable_allowed_errors_5xx" >> "$STATE_DIR/observation.metrics"
  local evidence_mode=natural_comparison
  if [[ "${ALLOW_LOW_TRAFFIC_RELEASE:-0}" == 1 ]] && (( protocol_result == 3 && errors_5xx == 0 )); then
    # This is delivery evidence, not a claim of statistical noninferiority.
    docker logs --since "$CUTOVER_AT" "$NEW" > "$STATE_DIR/candidate-probe-app.log" 2>&1
    chmod 600 "$STATE_DIR/candidate-probe-app.log"
    if python3 "$SCRIPT_DIR/low-traffic-evidence.py" "$STATE_DIR/public-business.json" \
      "$STATE_DIR/candidate-probe-app.log" "$protocol_dir" "${EXPECTED_POLICY_REJECTION_REASONS:-}" > "$STATE_DIR/low-traffic.result" 2>&1; then
      evidence_mode=verified_low_traffic
      protocol_result=0
    fi
  fi
  printf 'release_evidence=%s\n' "$evidence_mode" >> "$STATE_DIR/observation.metrics"
  if (( actionable_errors <= actionable_allowed_errors_5xx && protocol_result == 3 )); then
    # Evidence gaps are not candidate faults; callers must not finalize this state.
    trap - ERR
    printf 'observation=inconclusive release_id=%s production=%s version=%s requested_seconds=%s elapsed_seconds=%s checks=%s start=%s end=%s reason=protocol_evidence\n' \
      "$RELEASE_ID" "$NEW" "$VERSION" "$seconds" "$elapsed_seconds" "$checks" "$start" "$end" | tee "$STATE_DIR/observation.result"
    exit 3
  fi
  (( actionable_errors <= actionable_allowed_errors_5xx && protocol_result == 0 ))
  trap - ERR
  printf 'observation=passed evidence_mode=%s release_id=%s production=%s version=%s requested_seconds=%s elapsed_seconds=%s interval=%s checks=%s start=%s end=%s baseline_samples=%s baseline_errors_5xx=%s baseline_rate_bps=%s samples=%s errors_5xx=%s current_rate_bps=%s allowed_rate_bps=%s allowed_errors_5xx=%s\n' \
    "$evidence_mode" "$RELEASE_ID" "$NEW" "$VERSION" "$seconds" "$elapsed_seconds" "$interval" "$checks" "$start" "$end" \
    "$baseline_samples" "$baseline_errors_5xx" "$baseline_rate_bps" "$sample_count" "$errors_5xx" "$current_rate_bps" "$allowed_rate_bps" "$allowed_errors_5xx" |
    tee "$STATE_DIR/observation.result"
}

action_finalize() {
  load_config
  local mode=--dry-run accept_inconclusive=false
  local minimum_observe_seconds=600 observation_result observation_state field
  local observed_release="" observed_production="" observed_version=""
  local requested_seconds="" elapsed_seconds=""
  local decision_mode=statistical_pass accept_reason="" decision_plan
  for field in "$@"; do
    case "$field" in
      --dry-run|--execute) mode="$field" ;;
      --accept-inconclusive) accept_inconclusive=true ;;
      *) usage >&2; return 2 ;;
    esac
  done
  # shellcheck disable=SC1090
  source "$STATE_DIR/role-state.env"
  # Observation evidence is immutable; release disposition is recorded separately.
  if [[ ! -r "$STATE_DIR/observation.result" ]]; then
    printf 'finalize_blocked=observation_missing\n' >&2
    return 1
  fi
  observation_result="$(cat "$STATE_DIR/observation.result")"
  observation_state="${observation_result%% *}"
  case "$observation_state" in
    observation=passed)
      ;;
    observation=inconclusive)
      if [[ "$accept_inconclusive" != true || "${ACCEPT_INCONCLUSIVE:-0}" != 1 ]]; then
        printf 'finalize_blocked=observation_inconclusive acceptance_required=1\n' >&2
        return 1
      fi
      accept_reason="${ACCEPT_REASON:-}"
      if [[ -z "$accept_reason" || ${#accept_reason} -gt 256 || "$accept_reason" == *$'\n'* || "$accept_reason" == *$'\r'* ]]; then
        printf 'finalize_blocked=accept_reason_required max_length=256\n' >&2
        return 1
      fi
      # A missing comparison is acceptable only when the deterministic release checks passed.
      if [[ "$(cat "$STATE_DIR/gate.result" 2>/dev/null || true)" != "gate=passed candidate=$NEW version=$VERSION" ]] ||
        [[ "$(cat "$STATE_DIR/bounded.result" 2>/dev/null || true)" != 'result=evidence_inconclusive observation_exit=3 action=hold' ]] ||
        [[ ! -s "$STATE_DIR/public-browser.exit" ]] ||
        [[ "$(cat "$STATE_DIR/public-browser.exit")" != 0 ]] ||
        [[ -e "$STATE_DIR/public-browser.exit.pending" ]] ||
        [[ ! -s "$STATE_DIR/observation.metrics" ]] ||
        ! grep -qx 'protocol_stability_rc=3' "$STATE_DIR/observation.metrics" ||
        ! grep -Eq '(^|[[:space:]])actionable_errors_5xx=0 actionable_allowed_errors_5xx=[0-9]+$' "$STATE_DIR/observation.metrics"; then
        printf 'finalize_blocked=inconclusive_hard_gates\n' >&2
        return 1
      fi
      decision_mode=accepted_inconclusive
      ;;
    *)
      printf 'finalize_blocked=observation_failed\n' >&2
      return 1
      ;;
  esac
  for field in $observation_result; do
    case "$field" in
      release_id=*) observed_release="${field#*=}" ;;
      production=*) observed_production="${field#*=}" ;;
      version=*) observed_version="${field#*=}" ;;
      requested_seconds=*) requested_seconds="${field#*=}" ;;
      elapsed_seconds=*) elapsed_seconds="${field#*=}" ;;
    esac
  done
  if [[ "$observed_release" != "$RELEASE_ID" || "$observed_production" != "$NEW" || "$observed_version" != "$VERSION" ]]; then
    printf 'finalize_blocked=observation_identity_mismatch\n' >&2
    return 1
  fi
  if [[ ! "$requested_seconds" =~ ^[0-9]+$ || ! "$elapsed_seconds" =~ ^[0-9]+$ ]] ||
    (( requested_seconds < minimum_observe_seconds || elapsed_seconds < requested_seconds )); then
    printf 'finalize_blocked=observation_too_short minimum_seconds=%s requested_seconds=%s elapsed_seconds=%s\n' \
      "$minimum_observe_seconds" "$requested_seconds" "$elapsed_seconds" >&2
    return 1
  fi
  [[ "$(docker inspect -f '{{.State.Health.Status}}' "$NEW")" == healthy ]]
  [[ "$(production_container)" == "$NEW" ]]
  [[ "$(proxy_version)" == "$VERSION" ]]
  if [[ "$mode" == --dry-run ]]; then
    printf 'finalize_dry_run=passed decision=%s production=%s old=%s\n' "$decision_mode" "$NEW" "$OLD"
    return
  fi
  [[ "$mode" == --execute && "${CONFIRM_FINALIZE:-}" == "$RELEASE_ID" ]]
  decision_plan="$STATE_DIR/decision.plan"
  printf 'release_decision=%s state=planned release_id=%s production=%s version=%s reason=%s\n' \
    "$decision_mode" "$RELEASE_ID" "$NEW" "$VERSION" "${accept_reason:-statistical_observation_passed}" > "$decision_plan"
  chmod 600 "$decision_plan"
  # Do not kill in-flight SSE/WebSocket clients just because the observation window ended.
  gateway_control drain --seconds "${DRAIN_TIMEOUT_SECONDS:-60}"
  [[ "$(production_container)" == "$NEW" ]]
  # Preserve the manual standby stop across Docker daemon restarts.
  docker update --restart=unless-stopped "$OLD" >/dev/null
  [[ "$(docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$OLD")" == unless-stopped ]]
  docker stop --time 30 "$OLD" >/dev/null
  [[ "$(docker inspect -f '{{.State.Status}}' "$OLD")" == exited ]]
  local i
  for i in 1 2 3 4 5; do
    [[ "$(docker inspect -f '{{.State.Health.Status}}' "$NEW")" == healthy ]]
    [[ "$(docker inspect -f '{{.RestartCount}}' "$NEW")" == 0 ]]
    [[ "$(docker inspect -f '{{.State.OOMKilled}}' "$NEW")" == false ]]
    [[ "$(proxy_version)" == "$VERSION" ]]
    [[ "$(public_version)" == "$VERSION" ]]
    [[ "$i" -eq 5 ]] || sleep 5
  done
  # Deployment cleanup is manifest-scoped; never prune another service's build cache.
  # Keep both slot images and let the deployment runner remove its registered uploads.
  printf 'release_decision=%s state=finalized release_id=%s production=%s version=%s reason=%s\n' \
    "$decision_mode" "$RELEASE_ID" "$NEW" "$VERSION" "${accept_reason:-statistical_observation_passed}" > "$STATE_DIR/decision.result"
  chmod 600 "$STATE_DIR/decision.result"
  printf 'finalize=passed decision=%s production=%s old=%s old_state=exited old_restart_policy=unless-stopped version=%s docker_cleanup=manifest_required\n' \
    "$decision_mode" "$NEW" "$OLD" "$VERSION" | tee "$STATE_DIR/final.result"
}

action_rollback() {
  load_config
  local mode="${1:---dry-run}"
  if [[ "$mode" == --dry-run ]]; then
    if [[ -r "$STATE_DIR/role-state.env" ]]; then
      # shellcheck disable=SC1090
      source "$STATE_DIR/role-state.env"
    else
      # Before cutover, derive the rollback target from the verified stage state.
      # shellcheck disable=SC1090
      source "$STATE_DIR/stage.env"
      OLD="$PRODUCTION"
      NEW="$CANDIDATE"
      OLD_VERSION="$(container_version "$OLD")"
    fi
    [[ -n "$OLD_VERSION" ]]
    if ! docker inspect "$OLD" >/dev/null 2>&1 ||
      ! docker image inspect "$(docker inspect -f '{{.Image}}' "$OLD")" >/dev/null 2>&1; then
      printf 'rollback_blocked=assets_retired\n' >&2
      return 1
    fi
    check_rollback_port "$OLD"
    printf 'rollback_dry_run=passed old=%s old_version=%s\n' "$OLD" "$OLD_VERSION"
    return
  fi
  # shellcheck disable=SC1090
  source "$STATE_DIR/role-state.env"
  [[ "$mode" == --execute && "${CONFIRM_ROLLBACK:-}" == "$RELEASE_ID" ]]
  if [[ -r "$STATE_DIR/observation.result" ]]; then
    local observation_result
    observation_result="$(cat "$STATE_DIR/observation.result")"
    if [[ "$observation_result" == observation=inconclusive\ * && "${ALLOW_INCONCLUSIVE_ROLLBACK:-0}" != 1 ]]; then
      printf 'rollback_blocked=observation_inconclusive\n' >&2
      return 2
    fi
  fi
  # Retention cleanup may have intentionally retired this release's rollback target.
  if ! docker inspect "$OLD" >/dev/null 2>&1 ||
    ! docker image inspect "$(docker inspect -f '{{.Image}}' "$OLD")" >/dev/null 2>&1; then
    printf 'rollback_blocked=assets_retired\n' >&2
    return 1
  fi
  rollback_error() {
    local rc=$?
    trap - ERR INT TERM
    # The controller owns rollback of its routing transaction. Do not send traffic
    # back to a failed candidate merely because a later public probe failed.
    printf 'rollback_failed rc=%s both_slots_preserved=1 inspect_gateway_transaction=1\n' "$rc" >&2
    exit 1
  }
  check_rollback_port "$OLD"
  check_ingress
  trap rollback_error ERR INT TERM
  docker start "$OLD" >/dev/null
  wait_healthy "$OLD"
  gateway_control rollback --slot "$OLD" --version "$OLD_VERSION"
  local i internal public
  for i in $(seq 1 12); do
    internal="$(proxy_version 2>/dev/null || true)"
    public="$(public_version 2>/dev/null || true)"
    [[ "$internal" == "$OLD_VERSION" && "$public" == "$OLD_VERSION" ]] && break
    sleep 5
  done
  [[ "$internal" == "$OLD_VERSION" && "$public" == "$OLD_VERSION" ]]
  trap - ERR INT TERM
  printf 'rollback=passed release_id=%s production=%s version=%s\n' "$RELEASE_ID" "$OLD" "$OLD_VERSION" |
    tee "$STATE_DIR/rollback.result"
}

# Cleanup acceptance is separate from statistical acceptance; never rewrite observation evidence.
action_cleanup() {
  load_config
  local mode=--dry-run accept=false production version observation field
  local observed_release="" observed_production="" observed_version="" elapsed=0 requested=0 decision
  for field in "$@"; do
    case "$field" in
      --dry-run|--execute) mode="$field" ;;
      --accept-current) accept=true ;;
      *) usage >&2; return 2 ;;
    esac
  done
  source "$STATE_DIR/role-state.env"
  production="$(production_container)"
  version="$(container_version "$production")"
  if [[ "$production" == "$NEW" && "$version" == "$VERSION" ]]; then
    observation="$(cat "$STATE_DIR/observation.result")"
    for field in $observation; do
      case "$field" in
        release_id=*) observed_release="${field#*=}" ;;
        production=*) observed_production="${field#*=}" ;;
        version=*) observed_version="${field#*=}" ;;
        elapsed_seconds=*) elapsed="${field#*=}" ;;
        requested_seconds=*) requested="${field#*=}" ;;
      esac
    done
    [[ "$observed_release" == "$RELEASE_ID" && "$observed_production" == "$production" && "$observed_version" == "$version" ]]
    [[ "$elapsed" =~ ^[0-9]+$ && "$requested" =~ ^[0-9]+$ ]]
    (( requested >= 600 && elapsed >= requested ))
    decision=passed
    if [[ "$observation" != observation=passed\ * ]]; then
      [[ "$accept" == true && ( "$observation" == observation=failed\ * || "$observation" == observation=inconclusive\ * ) ]]
      decision=accepted_by_user
    fi
  elif [[ "$production" == "$OLD" && "$version" == "$OLD_VERSION" ]]; then
    [[ "$(cat "$STATE_DIR/rollback.result")" == "rollback=passed release_id=$RELEASE_ID production=$OLD version=$OLD_VERSION" ]]
    decision=rolled_back
  else
    printf 'cleanup_blocked=production_identity_mismatch\n' >&2
    return 1
  fi
  [[ "$(proxy_version)" == "$version" && "$(public_version)" == "$version" ]]
  [[ "$mode" != --execute || "${CONFIRM_CLEANUP:-}" == "$RELEASE_ID" ]]
  if [[ "$mode" == --execute ]]; then
    gateway_control drain --seconds "${DRAIN_TIMEOUT_SECONDS:-60}"
  fi
  python3 "$SCRIPT_DIR/retention.py" "$RELEASE_DIR" "$BACKUP_ROOT" "$production" "$version" \
    "$APP_NETWORK" "$POSTGRES_CONTAINER" "$OLD_VERSION" "$decision" \
    "$PUBLIC_STATUS_URL" "$mode"
}

ACTION="${1:-}"
shift || true
# All state-changing phases share one lock, including observation and asset retirement.
case "$ACTION" in
  backup|stage|gate|cutover|observe|finalize|rollback|cleanup|recover)
    load_config
    exec 9>"${CUTOVER_LOCK:-/var/lock/new-api-cutover.lock}"
    flock -n 9
    ;;
esac
case "$ACTION" in
  self-check) action_self_check "$@" ;;
  doctor) action_doctor "$@" ;;
  recover) action_recover "$@" ;;
  status) action_status "$@" ;;
  backup) action_backup "$@" ;;
  stage) action_stage "$@" ;;
  gate) action_gate "$@" ;;
  cutover) action_cutover "$@" ;;
  observe) action_observe "$@" ;;
  finalize) action_finalize "$@" ;;
  rollback) action_rollback "$@" ;;
  cleanup) action_cleanup "$@" ;;
  *) usage >&2; exit 2 ;;
esac
