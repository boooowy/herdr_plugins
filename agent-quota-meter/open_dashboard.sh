#!/usr/bin/env bash
set -euo pipefail
HERDR="${HERDR_BIN_PATH:-herdr}"
exec "$HERDR" plugin pane open \
  --plugin ntj.agent-quota \
  --entrypoint dashboard \
  --placement overlay \
  --focus
