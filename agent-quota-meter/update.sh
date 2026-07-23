#!/usr/bin/env bash
# レートリミット残量データの収集のみを行う(STATE_DIR/{claude,codex}.json に書き出す)。
# サイドバーへの表示(report-metadata)は context_ticker.py が担当する。
set -euo pipefail
cd "$(dirname "$0")"

STATE_DIR="${HERDR_PLUGIN_STATE_DIR:-$HOME/.cache/herdr-agent-quota}"
mkdir -p "$STATE_DIR"

# デバウンス: イベント多発時に60秒に1回へ間引く(--force で無視)
FORCE=0
[[ "${1:-}" == "--force" ]] && FORCE=1
STAMP="$STATE_DIR/last_update"
if [[ $FORCE -eq 0 && -f "$STAMP" ]]; then
  now=$(date +%s)
  last=$(stat -f %m "$STAMP" 2>/dev/null || stat -c %Y "$STAMP" 2>/dev/null || echo 0)
  if (( now - last < 60 )); then
    exit 0
  fi
fi
touch "$STAMP"

bash collect_codex.sh || true
bash collect_claude.sh || true
