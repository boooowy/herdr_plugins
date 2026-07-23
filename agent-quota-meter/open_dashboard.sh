#!/usr/bin/env bash
set -euo pipefail
HERDR="${HERDR_BIN_PATH:-herdr}"
exec "$HERDR" plugin pane open \
  --plugin boooowy.agent-quota \
  --entrypoint dashboard \
  --placement overlay \
  --focus
