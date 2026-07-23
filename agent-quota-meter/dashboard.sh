#!/usr/bin/env bash
# 残量ダッシュボード(overlayペイン)。30秒ごとに自動更新、q で閉じる。
set -euo pipefail
cd "$(dirname "$0")"

STATE_DIR="${HERDR_PLUGIN_STATE_DIR:-$HOME/.cache/herdr-agent-quota}"

render() {
  python3 - "$STATE_DIR" <<'PY'
import json, os, sys, time
from datetime import datetime

state_dir = sys.argv[1]
BAR = 24


def fmt_reset(v):
    if v is None:
        return "-"
    try:
        ts = float(v)
        if ts > 1e12:
            ts /= 1000
        return datetime.fromtimestamp(ts).strftime("%m/%d %H:%M")
    except (TypeError, ValueError):
        pass
    try:
        dt = datetime.fromisoformat(str(v))
        return dt.astimezone().strftime("%m/%d %H:%M")
    except ValueError:
        return str(v)


def bar(pct):
    filled = min(BAR, max(0, round(pct / 100 * BAR)))
    return "■" * filled + "□" * (BAR - filled)


print()
print("  Agent Quota Meter")
print("  " + "=" * 44)
for kind, title in (("claude", "Claude Code"), ("codex", "Codex")):
    print(f"\n  {title}")
    try:
        with open(os.path.join(state_dir, f"{kind}.json")) as fh:
            data = json.load(fh)
    except (OSError, ValueError):
        print("    (no data yet)")
        continue
    if not data.get("ok"):
        print(f"    取得失敗: {data.get('error', 'unknown')}")
        continue
    for w in data["windows"]:
        pct = w["used_percent"]
        label = str(w["label"]).ljust(8)
        print(f"    {label} {bar(pct)} {pct:5.1f}%  リセット: {fmt_reset(w.get('resets_at'))}")
    asof = data.get("asof") or data.get("collected_at")
    if asof:
        print(f"    (取得: {datetime.fromtimestamp(asof).strftime('%H:%M:%S')})")
print()
print("  [q] 閉じる / 30秒ごとに自動更新")
PY
}

while true; do
  bash update.sh --force >/dev/null 2>&1 || true
  clear
  render
  key=""
  read -r -s -t 30 -n 1 key || true
  if [[ "$key" == "q" || "$key" == "Q" ]]; then
    exit 0
  fi
done
