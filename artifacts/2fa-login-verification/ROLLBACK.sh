#!/bin/sh
set -eu
TARGET=${1:?target path is required}
BASELINE=${2:?baseline path is required}
cp "$BASELINE" "$TARGET"
shasum -a 256 "$TARGET"
