#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'EOF'
Usage:
  build-local.sh self-check
  build-local.sh prepare --commit SHA --previous-default PATH --previous-classic PATH [--output DIR] [--force]

PATH may be a clean dist directory or a .tar.zst created by this script.
EOF
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'missing_command=%s\n' "$1" >&2
    exit 1
  }
}

extract_clean_dist() {
  local source="$1" target="$2"
  mkdir -p "$target"
  if [[ -d "$source" ]]; then
    test -r "$source/index.html"
    cp -R "$source"/. "$target"/
  elif [[ "$source" == *.tar.zst && -f "$source" ]]; then
    zstd -t "$source" >/dev/null
    zstd -dc "$source" | tar -xf - -C "$target" --strip-components=1
    test -r "$target/index.html"
  else
    printf 'invalid_clean_dist=%s\n' "$source" >&2
    exit 1
  fi
}

wait_for_status() {
  local container="$1"
  local i
  for i in $(seq 1 60); do
    if docker exec "$container" wget -qO- http://127.0.0.1:3000/api/status >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

if [[ "${1:-}" == self-check ]]; then
  for command in bash bun docker git go python3 tar zstd; do
    require_command "$command"
  done
  docker info >/dev/null
  GO_BIN="${GO_BIN:-$(go env GOROOT)/bin/go}"
  printf 'self_check=passed bash=%s bun=%s go=%s docker=%s zstd=%s\n' \
    "${BASH_VERSION}" "$(bun --version)" "$("$GO_BIN" version | awk '{print $3}')" \
    "$(docker version --format '{{.Server.Version}}')" "$(zstd --version | awk '{print $2}')"
  exit 0
fi

[[ "${1:-}" == prepare ]] || {
  usage >&2
  exit 2
}
shift

COMMIT=''
PREVIOUS_DEFAULT=''
PREVIOUS_CLASSIC=''
OUTPUT=''
FORCE=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --commit) COMMIT="$2"; shift 2 ;;
    --previous-default) PREVIOUS_DEFAULT="$2"; shift 2 ;;
    --previous-classic) PREVIOUS_CLASSIC="$2"; shift 2 ;;
    --output) OUTPUT="$2"; shift 2 ;;
    --force) FORCE=1; shift ;;
    *) usage >&2; exit 2 ;;
  esac
done

for command in bun docker git go python3 tar zstd; do
  require_command "$command"
done
GO_BIN="${GO_BIN:-$(go env GOROOT)/bin/go}"
[[ -x "$GO_BIN" ]]

REPO="$(git rev-parse --show-toplevel)"
COMMIT="$(git -C "$REPO" rev-parse "${COMMIT}^{commit}")"
REMOTE_COMMIT="$(git -C "$REPO" rev-parse origin/dev)"
[[ "$COMMIT" == "$REMOTE_COMMIT" ]] || {
  printf 'commit_not_on_origin_dev commit=%s origin_dev=%s\n' "$COMMIT" "$REMOTE_COMMIT" >&2
  exit 1
}
[[ -n "$PREVIOUS_DEFAULT" && -n "$PREVIOUS_CLASSIC" ]] || {
  usage >&2
  exit 2
}

SHORT_SHA="${COMMIT:0:12}"
VERSION="dev-${SHORT_SHA}-local-amd64"
OUTPUT="${OUTPUT:-$HOME/.cache/new-api-deploy/releases/$SHORT_SHA}"
ARTIFACTS="$OUTPUT/artifacts"
MANIFEST="$OUTPUT/release.env"

if [[ "$FORCE" -eq 0 && -r "$MANIFEST" ]]; then
  # Reuse only a fully verified release for this exact commit.
  # shellcheck disable=SC1090
  source "$MANIFEST"
  if [[ "${COMMIT_SHA:-}" == "$COMMIT" && -r "$OUTPUT/$IMAGE_ARCHIVE" ]] && \
    [[ "$(sha256_file "$OUTPUT/$IMAGE_ARCHIVE")" == "${IMAGE_SHA256:-}" ]]; then
    printf 'prepare=already-complete release_dir=%s version=%s\n' "$OUTPUT" "$VERSION"
    exit 0
  fi
fi

mkdir -p "$ARTIFACTS" "$OUTPUT/logs"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/new-api-release-${SHORT_SHA}.XXXXXX")"
SMOKE_CONTAINER="new-api-release-smoke-${SHORT_SHA}"
cleanup() {
  docker rm -f "$SMOKE_CONTAINER" >/dev/null 2>&1 || true
  if [[ "${KEEP_RELEASE_WORKDIR:-0}" != 1 && "$WORK" == "${TMPDIR:-/tmp}"/new-api-release-* ]]; then
    rm -rf "$WORK"
  else
    printf 'release_workdir=%s\n' "$WORK"
  fi
}
trap cleanup EXIT

DEFAULT_TREE="$WORK/default"
CLASSIC_TREE="$WORK/classic"
PREVIOUS_DEFAULT_DIR="$WORK/previous-default"
PREVIOUS_CLASSIC_DIR="$WORK/previous-classic"
RUNTIME_CONTEXT="$WORK/runtime"
mkdir -p "$DEFAULT_TREE" "$CLASSIC_TREE" "$RUNTIME_CONTEXT"

git -C "$REPO" archive "$COMMIT" | tar -xf - -C "$DEFAULT_TREE"
git -C "$REPO" archive "$COMMIT" | tar -xf - -C "$CLASSIC_TREE"
printf '%s' "$VERSION" > "$DEFAULT_TREE/VERSION"
printf '%s' "$VERSION" > "$CLASSIC_TREE/VERSION"
extract_clean_dist "$PREVIOUS_DEFAULT" "$PREVIOUS_DEFAULT_DIR"
extract_clean_dist "$PREVIOUS_CLASSIC" "$PREVIOUS_CLASSIC_DIR"

build_default() {
  cd "$DEFAULT_TREE/web"
  bun install --frozen-lockfile
  cd default
  bun run typecheck
  DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION="$VERSION" bun run build
}

build_classic() {
  cd "$CLASSIC_TREE/web"
  bun install --filter ./classic --frozen-lockfile
  cd classic
  bun run test
  VITE_REACT_APP_VERSION="$VERSION" bun run build
}

start_epoch="$(date +%s)"
build_default >"$OUTPUT/logs/default-build.log" 2>&1 &
default_pid=$!
build_classic >"$OUTPUT/logs/classic-build.log" 2>&1 &
classic_pid=$!
default_status=0
classic_status=0
wait "$default_pid" || default_status=$?
wait "$classic_pid" || classic_status=$?
if [[ "$default_status" -ne 0 || "$classic_status" -ne 0 ]]; then
  printf 'frontend_build_failed default=%s classic=%s logs=%s\n' "$default_status" "$classic_status" "$OUTPUT/logs" >&2
  exit 1
fi
printf 'frontend_build_seconds=%s\n' "$(( $(date +%s) - start_epoch ))"

# Archive clean outputs before adding previous-production assets.
DEFAULT_CLEAN_DIST_ARCHIVE="artifacts/default-clean-${SHORT_SHA}.tar.zst"
CLASSIC_CLEAN_DIST_ARCHIVE="artifacts/classic-clean-${SHORT_SHA}.tar.zst"
tar -C "$DEFAULT_TREE/web/default" -cf - dist | zstd -T0 -3 -q -o "$OUTPUT/$DEFAULT_CLEAN_DIST_ARCHIVE"
tar -C "$CLASSIC_TREE/web/classic" -cf - dist | zstd -T0 -3 -q -o "$OUTPUT/$CLASSIC_CLEAN_DIST_ARCHIVE"

mkdir -p "$DEFAULT_TREE/web/classic/dist"
cp -R "$CLASSIC_TREE/web/classic/dist"/. "$DEFAULT_TREE/web/classic/dist"/
(cd "$DEFAULT_TREE/web/default" && bun run assets:merge-previous -- "$PREVIOUS_DEFAULT_DIR")
(cd "$DEFAULT_TREE/web/classic" && bun run assets:merge-previous -- "$PREVIOUS_CLASSIC_DIR")
(cd "$DEFAULT_TREE/web/default" && bun run assets:verify-recovery -- "$VERSION")
(cd "$DEFAULT_TREE/web/classic" && bun run assets:verify-recovery -- "$VERSION")
(cd "$DEFAULT_TREE/web/default" && node --test scripts/merge-previous-assets.test.mjs)

go_start="$(date +%s)"
(cd "$DEFAULT_TREE" && "$GO_BIN" test ./...) >"$OUTPUT/logs/go-test.log" 2>&1
(cd "$DEFAULT_TREE" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOEXPERIMENT=greenteagc \
  "$GO_BIN" build -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$VERSION'" -o "$RUNTIME_CONTEXT/new-api")
printf 'go_test_build_seconds=%s\n' "$(( $(date +%s) - go_start ))"

cp "$DEFAULT_TREE/LICENSE" "$DEFAULT_TREE/NOTICE" "$DEFAULT_TREE/THIRD-PARTY-LICENSES.md" "$RUNTIME_CONTEXT/"
IMAGE_TAG="new-api:$VERSION"
docker buildx build \
  --platform linux/amd64 \
  --target runtime-local \
  --build-arg "RELEASE_VERSION=$VERSION" \
  --build-arg "RELEASE_REVISION=$COMMIT" \
  --load \
  -f "$DEFAULT_TREE/Dockerfile" \
  -t "$IMAGE_TAG" \
  "$RUNTIME_CONTEXT" >"$OUTPUT/logs/docker-build.log" 2>&1

[[ "$(docker image inspect -f '{{.Os}}/{{.Architecture}}' "$IMAGE_TAG")" == linux/amd64 ]]
[[ "$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$IMAGE_TAG")" == "$COMMIT" ]]

docker run -d --rm --platform linux/amd64 --name "$SMOKE_CONTAINER" "$IMAGE_TAG" >/dev/null
if ! wait_for_status "$SMOKE_CONTAINER"; then
  docker logs "$SMOKE_CONTAINER" >&2
  exit 1
fi
status_json="$(docker exec "$SMOKE_CONTAINER" wget -qO- http://127.0.0.1:3000/api/status)"
status_version="$(printf '%s' "$status_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["version"])')"
[[ "$status_version" == "$VERSION" ]]
docker exec "$SMOKE_CONTAINER" wget -qO- http://127.0.0.1:3000/ | grep -q 'id="root"'
docker rm -f "$SMOKE_CONTAINER" >/dev/null

IMAGE_ARCHIVE="artifacts/new-api-${VERSION}.tar.zst"
docker save "$IMAGE_TAG" | zstd -T0 -3 -q -o "$OUTPUT/$IMAGE_ARCHIVE"
zstd -t "$OUTPUT/$IMAGE_ARCHIVE" >/dev/null
IMAGE_SHA256="$(sha256_file "$OUTPUT/$IMAGE_ARCHIVE")"
DEFAULT_CLEAN_DIST_SHA256="$(sha256_file "$OUTPUT/$DEFAULT_CLEAN_DIST_ARCHIVE")"
CLASSIC_CLEAN_DIST_SHA256="$(sha256_file "$OUTPUT/$CLASSIC_CLEAN_DIST_ARCHIVE")"
IMAGE_ID="$(docker image inspect -f '{{.Id}}' "$IMAGE_TAG")"
RELEASE_ID="${RELEASE_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$SHORT_SHA}"

umask 077
cat > "$MANIFEST" <<EOF
RELEASE_SCHEMA=1
RELEASE_ID=$RELEASE_ID
COMMIT_SHA=$COMMIT
SHORT_SHA=$SHORT_SHA
VERSION=$VERSION
ARCH=linux/amd64
IMAGE_TAG=$IMAGE_TAG
IMAGE_ID=$IMAGE_ID
IMAGE_ARCHIVE=$IMAGE_ARCHIVE
IMAGE_SHA256=$IMAGE_SHA256
DEFAULT_CLEAN_DIST_ARCHIVE=$DEFAULT_CLEAN_DIST_ARCHIVE
DEFAULT_CLEAN_DIST_SHA256=$DEFAULT_CLEAN_DIST_SHA256
CLASSIC_CLEAN_DIST_ARCHIVE=$CLASSIC_CLEAN_DIST_ARCHIVE
CLASSIC_CLEAN_DIST_SHA256=$CLASSIC_CLEAN_DIST_SHA256
GO_VERSION=$("$GO_BIN" version | awk '{print $3}')
BUN_VERSION=$(bun --version)
EOF
chmod 600 "$MANIFEST"
printf 'prepare=passed release_dir=%s version=%s image_sha256=%s\n' "$OUTPUT" "$VERSION" "$IMAGE_SHA256"
