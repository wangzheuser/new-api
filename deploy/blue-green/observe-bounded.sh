#!/usr/bin/env bash
# Preserve the first decision; a second window diagnoses uncertainty, not a retry to pass.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_DIR="${SCRIPT_DIR}/state"
mkdir -p "$STATE_DIR"
umask 077
[[ ! -e "$STATE_DIR/bounded.result" ]] || { cat "$STATE_DIR/bounded.result"; exit 2; }
rc=0
bash "$SCRIPT_DIR/release-remote.sh" observe --seconds 600 --interval 30 > "$STATE_DIR/observe.log" 2>&1 || rc=$?
printf '%s\n' "$rc" > "$STATE_DIR/observe.exit"
if (( rc == 3 )) && [[ "${ADDITIONAL_DIAGNOSTIC_OBSERVATION:-0}" == 1 ]]; then
  mkdir "$STATE_DIR/first-observation"
  for file in "$STATE_DIR"/observation* "$STATE_DIR"/observe.log "$STATE_DIR"/observe.exit; do
    [[ ! -f "$file" ]] || cp "$file" "$STATE_DIR/first-observation/"
  done
  second_rc=0
  bash "$SCRIPT_DIR/release-remote.sh" observe --seconds 600 --interval 30 > "$STATE_DIR/additional-observe.log" 2>&1 || second_rc=$?
  cp "$STATE_DIR/observation.result" "$STATE_DIR/additional-observation.result"
  printf '%s\n' "$second_rc" > "$STATE_DIR/additional-observe.exit"
  if (( second_rc != 0 && second_rc != 3 )); then
    rc=$second_rc
  else
    # Never allow finalize to consume a later lucky pass after the planned comparison held.
    cp "$STATE_DIR/first-observation/observation.result" "$STATE_DIR/observation.result"
  fi
fi
case "$rc" in
  0) result=passed ;;
  3) result=evidence_inconclusive ;;
  *) result=failed ;;
esac
printf 'result=%s exit=%s\n' "$result" "$rc" | tee "$STATE_DIR/bounded.result"
exit "$rc"
