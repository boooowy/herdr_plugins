#!/usr/bin/env bash
# 全イベントフックの入口。context ticker を(未起動なら)起こしてから、
# 既存の quota 更新(update.sh)へ渡す。引数(--force等)は update.sh へ素通し。
set -euo pipefail
cd "$(dirname "$0")"

nohup bash context_ticker.sh >/dev/null 2>&1 &

# 既に常駐している ticker を SIGUSR1 で叩いて sleep を即中断させ、
# pane の状態変化(working開始など)を待たずに即再描画させる。
RUNTIME_DIR="${AGENT_QUOTA_RUNTIME_DIR:-$HOME/.cache/herdr-agent-quota}"
pid=$(sed -n '1p' "$RUNTIME_DIR/ticker.lock/pid" 2>/dev/null || true)
[[ -n "$pid" ]] && kill -USR1 "$pid" 2>/dev/null || true

exec bash update.sh "$@"
