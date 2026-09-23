#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  printf 'Usage: cleanup-local.sh --release-dir DIR [--dry-run|--execute]\n' >&2
  exit 2
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

field() {
  sed -n "s/^$1=//p" "$manifest"
}

release_dir=''
mode=--dry-run
while [[ $# -gt 0 ]]; do
  case "$1" in
    --release-dir) [[ $# -ge 2 ]] || usage; release_dir="$2"; shift 2 ;;
    --dry-run|--execute) mode="$1"; shift ;;
    *) usage ;;
  esac
done
[[ -n "$release_dir" ]] || usage

root="${LOCAL_RELEASE_ROOT:-$HOME/.cache/new-api-deploy/releases}"
root="$(cd -P "$root" && pwd)"
release_dir="$(cd -P "$release_dir" && pwd)"
[[ "$release_dir" == "$root"/* ]] || { printf 'cleanup_local_blocked=outside_release_root\n' >&2; exit 1; }
manifest="$release_dir/release.env"
[[ -f "$manifest" && ! -L "$manifest" ]] || { printf 'cleanup_local_blocked=manifest_missing\n' >&2; exit 1; }

commit="$(field COMMIT_SHA)"
image_tag="$(field IMAGE_TAG)"
image_id="$(field IMAGE_ID)"
builder="$(field BUILDER_NAME)"
[[ "$commit" =~ ^[0-9a-f]{40}$ && "${commit:0:12}" == "${release_dir##*/}" ]] || {
  printf 'cleanup_local_blocked=commit_mismatch\n' >&2; exit 1;
}
[[ "$image_tag" == "new-api:dev-${commit:0:12}-local-amd64" && "$image_id" =~ ^sha256:[0-9a-f]{64}$ ]] || {
  printf 'cleanup_local_blocked=image_identity\n' >&2; exit 1;
}
[[ "$builder" == "new-api-release-${commit:0:12}" ]] || {
  printf 'cleanup_local_blocked=builder_identity\n' >&2; exit 1;
}

files=()
for kind in IMAGE DEFAULT_CLEAN_DIST CLASSIC_CLEAN_DIST; do
  archive="$(field "${kind}_ARCHIVE")"
  checksum="$(field "${kind}_SHA256")"
  [[ "$archive" =~ ^artifacts/[a-zA-Z0-9._-]+\.tar\.zst$ && "$checksum" =~ ^[0-9a-f]{64}$ ]] || {
    printf 'cleanup_local_blocked=archive_manifest kind=%s\n' "$kind" >&2; exit 1;
  }
  file="$release_dir/$archive"
  [[ ! -L "$release_dir/artifacts" && ! -L "$file" ]] || {
    printf 'cleanup_local_blocked=symlink file=%s\n' "$file" >&2; exit 1;
  }
  if [[ -e "$file" ]]; then
    [[ -f "$file" && "$(sha256_file "$file")" == "$checksum" ]] || {
      printf 'cleanup_local_blocked=archive_checksum file=%s\n' "$file" >&2; exit 1;
    }
    files+=("$file")
  fi
done

docker info >/dev/null
if docker image inspect "$image_tag" >/dev/null 2>&1; then
  [[ "$(docker image inspect -f '{{.Id}}' "$image_tag")" == "$image_id" ]] || {
    printf 'cleanup_local_blocked=image_changed\n' >&2; exit 1;
  }
  has_image=1
else
  has_image=0
fi
if docker buildx inspect "$builder" >/dev/null 2>&1; then has_builder=1; else has_builder=0; fi

printf 'cleanup_local_plan release_dir=%s archives=%s image=%s builder=%s mode=%s\n' \
  "$release_dir" "${#files[@]}" "$has_image" "$has_builder" "$mode"
[[ "$mode" == --execute ]] || exit 0

# Docker refuses referenced images without --force; never affect other containers or tags.
if [[ "$has_image" == 1 ]]; then docker image rm "$image_tag"; fi
if [[ "$has_builder" == 1 ]]; then docker buildx rm "$builder"; fi
for file in "${files[@]}"; do rm -- "$file"; done
printf 'cleanup_local=passed release_dir=%s\n' "$release_dir"
