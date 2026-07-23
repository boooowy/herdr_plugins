#!/usr/bin/env bash
# context ticker の singleton 起動。mkdir ロック(flock は macOS 標準に無い)で
# 多重起動を防ぎ、死んだプロセスのロックは回収する。実体は context_ticker.py。
set -euo pipefail
cd "$(dirname "$0")"

STATE_DIR="${HERDR_PLUGIN_STATE_DIR:-$HOME/.cache/herdr-agent-quota}"
mkdir -p "$STATE_DIR"
LOCKDIR="$STATE_DIR/ticker.lock"

if ! mkdir "$LOCKDIR" 2>/dev/null; then
  pid=$(cat "$LOCKDIR/pid" 2>/dev/null || true)
  hb=$(stat -f %m "$LOCKDIR/pid" 2>/dev/null || stat -c %Y "$LOCKDIR/pid" 2>/dev/null || echo 0)
  now=$(date +%s)
  # pid が生きていてハートビート(毎tickのtouch)が2分以内なら健在
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null && (( now - hb < 120 )); then
    exit 0
  fi
  # stale 回収。競合して負けたら退散(勝者が起動している)
  rm -rf "$LOCKDIR"
  mkdir "$LOCKDIR" 2>/dev/null || exit 0
fi

echo $$ > "$LOCKDIR/pid"
# exec で PID は変わらないため pid ファイルはそのまま有効。
# ロックの後片付けは context_ticker.py 側の atexit/シグナルハンドラが行う。
exec python3 context_ticker.py
