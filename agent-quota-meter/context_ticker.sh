#!/usr/bin/env bash
# context ticker の起動入口。plugin IDをまたぐsingleton lockの管理は
# context_ticker.py が担当する。
set -euo pipefail
cd "$(dirname "$0")"

export AGENT_QUOTA_RUNTIME_DIR="${AGENT_QUOTA_RUNTIME_DIR:-$HOME/.cache/herdr-agent-quota}"
exec python3 context_ticker.py
