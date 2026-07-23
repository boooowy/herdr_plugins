#!/usr/bin/env bash
# Codex の残量を ~/.codex/sessions の rate_limits スナップショットから読み取り、
# STATE_DIR/codex.json に書き出す。認証情報には一切触れない。
set -euo pipefail

STATE_DIR="${HERDR_PLUGIN_STATE_DIR:-$HOME/.cache/herdr-agent-quota}"
mkdir -p "$STATE_DIR"

python3 - "$STATE_DIR/codex.json" <<'PY'
import glob, json, os, sys, time

out_path = sys.argv[1]
result = {"agent": "codex", "ok": False, "windows": [], "collected_at": int(time.time())}


def window_label(minutes):
    if minutes is None:
        return "?"
    if minutes <= 330:
        return "5h"
    if abs(minutes - 10080) <= 120:
        return "1w"
    if minutes >= 40000:
        return "1m"
    if minutes < 2880:
        return f"{int(minutes / 60)}h"
    return f"{int(minutes / 1440)}d"


def dig(obj):
    if isinstance(obj, dict):
        if "rate_limits" in obj and isinstance(obj["rate_limits"], dict):
            return obj["rate_limits"]
        for v in obj.values():
            r = dig(v)
            if r is not None:
                return r
    elif isinstance(obj, list):
        for v in obj:
            r = dig(v)
            if r is not None:
                return r
    return None


files = sorted(
    glob.glob(os.path.expanduser("~/.codex/sessions/*/*/*/*.jsonl")),
    key=os.path.getmtime,
    reverse=True,
)

rl = None
asof = None
for f in files[:10]:
    last = None
    try:
        with open(f, encoding="utf-8", errors="replace") as fh:
            for line in fh:
                if '"rate_limits"' not in line:
                    continue
                try:
                    found = dig(json.loads(line))
                except Exception:
                    continue
                if found is not None:
                    last = found
    except OSError:
        continue
    if last is not None:
        rl = last
        asof = int(os.path.getmtime(f))
        break

if rl is None:
    result["error"] = "no rate_limits snapshot found in ~/.codex/sessions"
else:
    for key in ("primary", "secondary"):
        w = rl.get(key)
        if not isinstance(w, dict):
            continue
        pct = w.get("used_percent")
        if pct is None:
            continue
        result["windows"].append(
            {
                "label": window_label(w.get("window_minutes")),
                "used_percent": float(pct),
                "resets_at": w.get("resets_at"),
                "window_minutes": w.get("window_minutes"),
            }
        )
    result["ok"] = bool(result["windows"])
    result["asof"] = asof
    if not result["ok"]:
        result["error"] = "rate_limits snapshot had no usable windows"

tmp = out_path + ".tmp"
with open(tmp, "w") as fh:
    json.dump(result, fh, ensure_ascii=False)
os.replace(tmp, out_path)
PY
