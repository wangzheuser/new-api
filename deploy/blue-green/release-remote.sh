#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RELEASE_ENV="${RELEASE_ENV:-$SCRIPT_DIR/release.env}"
SERVER_ENV="${SERVER_ENV:-$SCRIPT_DIR/server.env}"
COMPOSE_TEMPLATE="${COMPOSE_TEMPLATE:-$SCRIPT_DIR/docker-compose.slot.yml}"

usage() {
  cat <<'EOF'
Usage: release-remote.sh <self-check|status|backup|stage|gate|cutover|observe|finalize|rollback> [options]

Mutating production actions require:
  cutover  --execute   CONFIRM_CUTOVER=<release-id>
  finalize --execute  CONFIRM_FINALIZE=<release-id>
  rollback --execute  CONFIRM_ROLLBACK=<release-id>
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
    BACKUP_ROOT APP_NETWORK PROXY_NETWORK PROXY_ALIAS PROXY_CONTAINER PUBLIC_STATUS_URL \
    POSTGRES_CONTAINER POSTGRES_USER POSTGRES_DB REDIS_CONTAINER NGINX_ACCESS_LOG \
    BLUE_PORT BLUE_DATA_DIR BLUE_LOG_DIR BLUE_NODE_NAME BLUE_PROJECT BLUE_RUNTIME_ENV_FILE \
    GREEN_PORT GREEN_DATA_DIR GREEN_LOG_DIR GREEN_NODE_NAME GREEN_PROJECT GREEN_RUNTIME_ENV_FILE; do
    require_variable "$name"
  done
  RELEASE_DIR="$(cd "$(dirname "$RELEASE_ENV")" && pwd)"
  IMAGE_PATH="$RELEASE_DIR/$IMAGE_ARCHIVE"
  STATE_DIR="$RELEASE_DIR/state"
  mkdir -p "$STATE_DIR"
  chmod 700 "$STATE_DIR"
}

sha256_file() {
  sha256sum "$1" | awk '{print $1}'
}

container_ip() {
  docker inspect "$1" | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["NetworkSettings"]["Networks"].get(sys.argv[1],{}).get("IPAddress",""))' "$PROXY_NETWORK"
}

container_has_alias() {
  docker inspect "$1" | python3 -c 'import json,sys; c=json.load(sys.stdin)[0]; print("true" if sys.argv[2] in c["NetworkSettings"]["Networks"].get(sys.argv[1],{}).get("Aliases",[]) else "false")' "$PROXY_NETWORK" "$PROXY_ALIAS"
}

production_container() {
  local blue=false green=false
  blue="$(container_has_alias new-api-blue 2>/dev/null || true)"
  green="$(container_has_alias new-api-green 2>/dev/null || true)"
  if [[ "$blue" == true && "$green" != true ]]; then
    printf 'new-api-blue'
  elif [[ "$green" == true && "$blue" != true ]]; then
    printf 'new-api-green'
  else
    printf 'invalid_production_alias blue=%s green=%s\n' "$blue" "$green" >&2
    return 1
  fi
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

container_version() {
  local container="$1" port
  port="$(slot_value "$(slot_name "$container")" PORT)"
  curl -fsS --max-time 10 "http://127.0.0.1:$port/api/status" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["version"])'
}

proxy_version() {
  docker exec "$PROXY_CONTAINER" wget -qO- --timeout=10 "http://$PROXY_ALIAS:3000/api/status" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["version"])'
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
  local container="$1" i
  for i in $(seq 1 90); do
    [[ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container" 2>/dev/null || true)" == healthy ]] && return 0
    sleep 2
  done
  return 1
}

nginx_hash() {
  docker exec "$PROXY_CONTAINER" nginx -T 2>&1 | sha256sum | awk '{print $1}'
}

connect_with_retry() {
  local container="$1" ip="$2" alias="$3" i
  for i in $(seq 1 20); do
    if [[ -n "$ip" ]]; then
      docker network connect --ip "$ip" --alias "$alias" "$PROXY_NETWORK" "$container" >/dev/null 2>&1 || true
    else
      docker network connect --alias "$alias" "$PROXY_NETWORK" "$container" >/dev/null 2>&1 || true
    fi
    [[ -n "$(container_ip "$container")" ]] && return 0
    sleep 1
  done
  return 1
}

disconnect_if_connected() {
  [[ -z "$(container_ip "$1")" ]] || docker network disconnect "$PROXY_NETWORK" "$1"
}

render_compose() {
  local slot="$1" service="new-api-$1"
  export SLOT="$slot"
  export HOST_PORT="$(slot_value "$slot" PORT)"
  export DATA_DIR="$(slot_value "$slot" DATA_DIR)"
  export LOG_DIR="$(slot_value "$slot" LOG_DIR)"
  export NODE_NAME="$(slot_value "$slot" NODE_NAME)"
  export RUNTIME_ENV_FILE="$(slot_value "$slot" RUNTIME_ENV_FILE)"
  export APP_NETWORK RUNTIME_ENV_FILE IMAGE_TAG
  sed "s/^  new-api:/  $service:/" "$COMPOSE_TEMPLATE" |
    docker compose -p "$(slot_value "$slot" PROJECT)" -f - config
}

action_self_check() {
  for command in bash curl docker flock python3 sha256sum tar zstd; do
    require_command "$command"
  done
  bash -n "$0"
  load_config
  render_compose blue >/dev/null
  render_compose green >/dev/null
  printf 'self_check=passed release=%s version=%s\n' "$RELEASE_ID" "$VERSION"
}

action_status() {
  load_config
  local production candidate
  production="$(production_container)"
  candidate="$(other_container "$production")"
  printf 'production=%s production_version=%s candidate=%s candidate_state=%s candidate_version=%s\n' \
    "$production" "$(container_version "$production")" "$candidate" \
    "$(docker inspect -f '{{.State.Status}}' "$candidate")" \
    "$(container_version "$candidate" 2>/dev/null || printf stopped)"
}

action_backup() {
  load_config
  local backup_dir="$BACKUP_ROOT/$RELEASE_ID" dump="$BACKUP_ROOT/$RELEASE_ID/postgresql.dump"
  if [[ -r "$dump" && -r "$dump.sha256" && -s "$backup_dir/postgresql.restore-list.txt" ]] && \
    (cd "$backup_dir" && sha256sum -c postgresql.dump.sha256 >/dev/null); then
    printf 'backup=already-complete directory=%s\n' "$backup_dir"
    return
  fi
  umask 077
  mkdir -p "$backup_dir"
  chmod 700 "$backup_dir"
  local production
  production="$(production_container)"
  docker inspect "$production" > "$backup_dir/production-container.inspect.json"
  docker image inspect "$(docker inspect -f '{{.Image}}' "$production")" > "$backup_dir/production-image.inspect.json"
  docker network inspect "$APP_NETWORK" > "$backup_dir/app-network.inspect.json"
  docker network inspect "$PROXY_NETWORK" > "$backup_dir/proxy-network.inspect.json"
  docker exec "$PROXY_CONTAINER" nginx -T > "$backup_dir/nginx-config.txt" 2>&1
  sha256sum "$backup_dir/nginx-config.txt" > "$backup_dir/nginx-config.sha256"
  docker inspect "$production" | python3 -c 'import json,sys; env=json.load(sys.stdin)[0]["Config"]["Env"]; sensitive=("SECRET","PASSWORD","TOKEN","DSN","KEY","COOKIE"); print("\n".join(x.split("=",1)[0]+"=<redacted>" if any(k in x.split("=",1)[0].upper() for k in sensitive) else x for x in env))' > "$backup_dir/runtime-env.sanitized"
  docker exec "$POSTGRES_CONTAINER" pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc > "$dump"
  (cd "$backup_dir" && sha256sum postgresql.dump > postgresql.dump.sha256)
  docker exec -i "$POSTGRES_CONTAINER" pg_restore -l < "$dump" > "$backup_dir/postgresql.restore-list.txt"
  [[ -s "$backup_dir/postgresql.restore-list.txt" ]]
  (cd "$backup_dir" && sha256sum -c postgresql.dump.sha256 >/dev/null)
  find "$backup_dir" -type f -exec chmod 600 {} +
  # Keep only the newest verified backup, as required by the current retention policy.
  while IFS= read -r old; do
    [[ "$old" == "$backup_dir" ]] || rm -rf -- "$old"
  done < <(find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -printf '%T@|%p\n' | sort -t'|' -k1,1nr | cut -d'|' -f2-)
  [[ "$(find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d | wc -l)" -eq 1 ]]
  printf 'backup=passed directory=%s bytes=%s\n' "$backup_dir" "$(stat -c %s "$dump")"
}

action_stage() {
  load_config
  [[ -r "$IMAGE_PATH" ]]
  [[ "$(sha256_file "$IMAGE_PATH")" == "$IMAGE_SHA256" ]]
  zstd -t "$IMAGE_PATH" >/dev/null
  zstd -dc "$IMAGE_PATH" | docker load >/dev/null
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
  docker compose -p "$project" -f "$compose_file" up -d --force-recreate --no-deps "$service"
  wait_healthy "$candidate"
  [[ "$(container_version "$candidate")" == "$VERSION" ]]
  networks="$(docker inspect "$candidate" | python3 -c 'import json,sys; print(",".join(sorted(json.load(sys.stdin)[0]["NetworkSettings"]["Networks"])))')"
  [[ "$networks" == "$APP_NETWORK" ]]
  umask 077
  cat > "$STATE_DIR/stage.env" <<EOF
PRODUCTION=$production
CANDIDATE=$candidate
PRODUCTION_PORT=$(slot_value "$(slot_name "$production")" PORT)
CANDIDATE_PORT=$(slot_value "$slot" PORT)
EOF
  chmod 600 "$STATE_DIR/stage.env"
  printf 'stage=passed production=%s candidate=%s version=%s networks=%s\n' "$production" "$candidate" "$VERSION" "$networks"
}

action_gate() {
  load_config
  # shellcheck disable=SC1090
  source "$STATE_DIR/stage.env"
  [[ "$(docker inspect -f '{{.State.Health.Status}}' "$CANDIDATE")" == healthy ]]
  [[ "$(docker inspect -f '{{.RestartCount}}' "$CANDIDATE")" == 0 ]]
  [[ "$(docker inspect -f '{{.State.OOMKilled}}' "$CANDIDATE")" == false ]]
  [[ "$(container_version "$CANDIDATE")" == "$VERSION" ]]
  [[ -z "$(container_ip "$CANDIDATE")" ]]
  [[ "$(docker inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$CANDIDATE")" == "$COMMIT_SHA" ]]
  [[ "$(docker exec "$CANDIDATE" printenv NODE_NAME)" != "$(docker exec "$PRODUCTION" printenv NODE_NAME)" ]]
  for field in Memory MemorySwap PidsLimit NanoCpus; do
    [[ "$(docker inspect -f "{{.HostConfig.$field}}" "$CANDIDATE")" == "$(docker inspect -f "{{.HostConfig.$field}}" "$PRODUCTION")" ]]
  done
  docker exec "$CANDIDATE" getent hosts "$POSTGRES_CONTAINER" >/dev/null
  docker exec "$CANDIDATE" getent hosts "$REDIS_CONTAINER" >/dev/null
  curl -fsS --max-time 10 "http://127.0.0.1:$CANDIDATE_PORT/" -o "$STATE_DIR/candidate-index.html"
  grep -q 'id="root"' "$STATE_DIR/candidate-index.html"
  python3 -c 'import re,sys; print("\n".join(re.findall(r"(?:src|href)=\"(/static/[^\"?#]+)",open(sys.argv[1]).read())))' "$STATE_DIR/candidate-index.html" |
    while IFS= read -r asset; do [[ -z "$asset" ]] || curl -fsS --max-time 10 -o /dev/null "http://127.0.0.1:$CANDIDATE_PORT$asset"; done
  headers="$(curl -sS --max-time 10 -D - -o /dev/null "http://127.0.0.1:$CANDIDATE_PORT/static/deploy-missing-$RELEASE_ID.js")"
  grep -qE '^HTTP/[^ ]+ 404' <<<"$headers"
  grep -qiE '^Cache-Control:.*no-store' <<<"$headers"
  baseline_hash="$(awk '{print $1}' "$BACKUP_ROOT/$RELEASE_ID/nginx-config.sha256")"
  [[ "$(nginx_hash)" == "$baseline_hash" ]]
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
  [[ -s "$STATE_DIR/gate.result" ]]
  if [[ "$(container_has_alias "$CANDIDATE")" == true && "$(proxy_version)" == "$VERSION" ]]; then
    printf 'cutover=already-complete production=%s version=%s\n' "$CANDIDATE" "$VERSION"
    return
  fi
  [[ "$(production_container)" == "$PRODUCTION" ]]
  [[ "$(docker inspect -f '{{.State.Health.Status}}' "$CANDIDATE")" == healthy ]]
  [[ "$(container_version "$CANDIDATE")" == "$VERSION" ]]
  [[ -z "$(container_ip "$CANDIDATE")" ]]
  local production_ip production_version baseline_hash
  production_ip="$(container_ip "$PRODUCTION")"
  production_version="$(container_version "$PRODUCTION")"
  baseline_hash="$(awk '{print $1}' "$BACKUP_ROOT/$RELEASE_ID/nginx-config.sha256")"
  [[ -n "$production_ip" && "$(nginx_hash)" == "$baseline_hash" ]]
  if [[ "$mode" == --dry-run ]]; then
    printf 'cutover_dry_run=passed production=%s candidate=%s production_version=%s target_version=%s\n' "$PRODUCTION" "$CANDIDATE" "$production_version" "$VERSION"
    return
  fi
  [[ "$mode" == --execute && "${CONFIRM_CUTOVER:-}" == "$RELEASE_ID" ]]
  exec 9>"${CUTOVER_LOCK:-/var/lock/new-api-cutover.lock}"
  flock -n 9
  umask 077
  cat > "$STATE_DIR/role-state.env" <<EOF
OLD=$PRODUCTION
NEW=$CANDIDATE
OLD_IP=$production_ip
OLD_VERSION=$production_version
NEW_VERSION=$VERSION
NETWORK=$PROXY_NETWORK
ALIAS=$PROXY_ALIAS
EOF
  chmod 600 "$STATE_DIR/role-state.env"
  local switched=0
  restore_on_error() {
    local rc=$?
    if [[ "$switched" -ne 1 ]]; then
      disconnect_if_connected "$CANDIDATE" || true
      disconnect_if_connected "$PRODUCTION" || true
      connect_with_retry "$PRODUCTION" "$production_ip" "$PROXY_ALIAS" || true
    fi
    printf 'cutover_failed line=%s command=%s rc=%s\n' "$LINENO" "$BASH_COMMAND" "$rc" >&2
    exit "$rc"
  }
  trap restore_on_error ERR
  disconnect_if_connected "$CANDIDATE"
  disconnect_if_connected "$PRODUCTION"
  connect_with_retry "$CANDIDATE" "$production_ip" "$PROXY_ALIAS"
  # Only production joins the proxy network. A physical slot name may equal the
  # stable alias, so connecting standby would make Docker DNS resolve both slots.
  [[ "$(container_ip "$CANDIDATE")" == "$production_ip" && -z "$(container_ip "$PRODUCTION")" ]]
  [[ "$(proxy_version)" == "$VERSION" ]]
  [[ "$(public_version)" == "$VERSION" ]]
  [[ "$(nginx_hash)" == "$baseline_hash" ]]
  switched=1
  trap - ERR
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
  # shellcheck disable=SC1090
  source "$STATE_DIR/role-state.env"
  local baseline_hash start end iterations i baseline_lines end_lines sample_count errors_5xx
  baseline_hash="$(awk '{print $1}' "$BACKUP_ROOT/$RELEASE_ID/nginx-config.sha256")"
  start="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  baseline_lines="$(docker exec "$PROXY_CONTAINER" sh -c "wc -l < '$NGINX_ACCESS_LOG'")"
  [[ "$(public_version)" == "$VERSION" ]]
  iterations=$(( seconds / interval ))
  (( iterations > 0 )) || iterations=1
  for i in $(seq 1 "$iterations"); do
    [[ "$(docker inspect -f '{{.State.Health.Status}}' "$NEW")" == healthy ]]
    [[ "$(docker inspect -f '{{.RestartCount}}' "$NEW")" == 0 ]]
    [[ "$(docker inspect -f '{{.State.OOMKilled}}' "$NEW")" == false ]]
    [[ "$(proxy_version)" == "$VERSION" ]]
    [[ "$(nginx_hash)" == "$baseline_hash" ]]
    [[ "$i" -eq "$iterations" ]] || sleep "$interval"
  done
  [[ "$(public_version)" == "$VERSION" ]]
  end="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  end_lines="$(docker exec "$PROXY_CONTAINER" sh -c "wc -l < '$NGINX_ACCESS_LOG'")"
  (( end_lines >= baseline_lines ))
  sample_count=$(( end_lines - baseline_lines ))
  (( sample_count > 0 ))
  errors_5xx="$(docker exec "$PROXY_CONTAINER" sh -c "tail -n '$sample_count' '$NGINX_ACCESS_LOG'" | grep -Ec '" 5[0-9][0-9] ' || true)"
  [[ "$errors_5xx" -eq 0 ]]
  printf 'observation=passed start=%s end=%s version=%s samples=%s errors_5xx=%s\n' "$start" "$end" "$VERSION" "$sample_count" "$errors_5xx" | tee "$STATE_DIR/observation.result"
}

action_finalize() {
  load_config
  local mode="${1:---dry-run}"
  # shellcheck disable=SC1090
  source "$STATE_DIR/role-state.env"
  [[ "$(docker inspect -f '{{.State.Health.Status}}' "$NEW")" == healthy ]]
  [[ "$(proxy_version)" == "$VERSION" ]]
  if [[ "$mode" == --dry-run ]]; then
    printf 'finalize_dry_run=passed production=%s old=%s\n' "$NEW" "$OLD"
    return
  fi
  [[ "$mode" == --execute && "${CONFIRM_FINALIZE:-}" == "$RELEASE_ID" ]]
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
  printf 'finalize=passed production=%s old=%s old_state=exited version=%s\n' "$NEW" "$OLD" "$VERSION" | tee "$STATE_DIR/final.result"
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
      OLD_IP="$(container_ip "$OLD")"
      OLD_VERSION="$(container_version "$OLD")"
    fi
    [[ -n "$OLD_IP" && -n "$OLD_VERSION" ]]
    printf 'rollback_dry_run=passed old=%s old_version=%s\n' "$OLD" "$OLD_VERSION"
    return
  fi
  # shellcheck disable=SC1090
  source "$STATE_DIR/role-state.env"
  [[ "$mode" == --execute && "${CONFIRM_ROLLBACK:-}" == "$RELEASE_ID" ]]
  exec 9>"${CUTOVER_LOCK:-/var/lock/new-api-cutover.lock}"
  flock -n 9
  local current_ip
  current_ip="$(container_ip "$NEW")"
  restore_new_on_error() {
    local rc=$?
    disconnect_if_connected "$OLD" || true
    disconnect_if_connected "$NEW" || true
    connect_with_retry "$NEW" "$current_ip" "$PROXY_ALIAS" || true
    printf 'rollback_failed line=%s command=%s rc=%s\n' "$LINENO" "$BASH_COMMAND" "$rc" >&2
    exit "$rc"
  }
  trap restore_new_on_error ERR
  docker start "$OLD" >/dev/null
  wait_healthy "$OLD"
  disconnect_if_connected "$NEW"
  disconnect_if_connected "$OLD"
  connect_with_retry "$OLD" "$OLD_IP" "$PROXY_ALIAS"
  local i internal public
  for i in $(seq 1 12); do
    internal="$(proxy_version 2>/dev/null || true)"
    public="$(public_version 2>/dev/null || true)"
    [[ "$internal" == "$OLD_VERSION" && "$public" == "$OLD_VERSION" ]] && break
    sleep 5
  done
  [[ "$internal" == "$OLD_VERSION" && "$public" == "$OLD_VERSION" ]]
  trap - ERR
  printf 'rollback=passed production=%s version=%s\n' "$OLD" "$OLD_VERSION"
}

ACTION="${1:-}"
shift || true
case "$ACTION" in
  self-check) action_self_check "$@" ;;
  status) action_status "$@" ;;
  backup) action_backup "$@" ;;
  stage) action_stage "$@" ;;
  gate) action_gate "$@" ;;
  cutover) action_cutover "$@" ;;
  observe) action_observe "$@" ;;
  finalize) action_finalize "$@" ;;
  rollback) action_rollback "$@" ;;
  *) usage >&2; exit 2 ;;
esac
