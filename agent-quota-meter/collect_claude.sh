#!/usr/bin/env bash
# Claude Code の残量を OAuth usage API から取得し、STATE_DIR/claude.json に書き出す。
# 認証トークンは macOS Keychain / ~/.claude/.credentials.json から読むが、
# usage API の呼び出しのみに使い、標準出力・ログ・エラーメッセージには一切出さない。
set -euo pipefail

STATE_DIR="${HERDR_PLUGIN_STATE_DIR:-$HOME/.cache/herdr-agent-quota}"
mkdir -p "$STATE_DIR"

python3 - "$STATE_DIR/claude.json" <<'PY'
import json, os, subprocess, sys, time
import urllib.request
import urllib.error

out_path = sys.argv[1]
now = int(time.time())
result = {"agent": "claude", "ok": False, "windows": [], "collected_at": now, "attempted_at": now}

prev = None
try:
    with open(out_path) as fh:
        prev = json.load(fh)
except (OSError, ValueError):
    pass

if prev:
    # 成功データが5分以内なら再取得しない(usage APIを叩きすぎて429にならないように)
    if prev.get("ok") and now - prev.get("collected_at", 0) < 300:
        sys.exit(0)
    # 直近の失敗から10分間はリトライしない(429バックオフ)
    if not prev.get("ok") and now - prev.get("attempted_at", prev.get("collected_at", 0)) < 600:
        sys.exit(0)

LABELS = {
    "five_hour": "5h",
    "seven_day": "1w",
    "seven_day_opus": "1w(op)",
    "seven_day_sonnet": "1w(so)",
    "thirty_day": "1m",
    "monthly": "1m",
}


def load_credentials():
    # macOS: Keychain(初回はOSの許可ダイアログが出る) / Linux: 平文ファイル
    if sys.platform == "darwin":
        proc = subprocess.run(
            ["security", "find-generic-password", "-s", "Claude Code-credentials", "-w"],
            capture_output=True,
            text=True,
        )
        if proc.returncode != 0:
            raise RuntimeError("keychain item not found or access denied")
        return json.loads(proc.stdout.strip())
    path = os.path.expanduser("~/.claude/.credentials.json")
    with open(path) as fh:
        return json.load(fh)


def fail(msg):
    # 30分以内の成功データがあれば温存する(一時的な429等で表示を消さない)
    if prev and prev.get("ok") and now - prev.get("collected_at", 0) < 1800:
        prev["last_error"] = msg
        prev["attempted_at"] = now
        payload = prev
    else:
        result["error"] = msg
        payload = result
    tmp = out_path + ".tmp"
    with open(tmp, "w") as fh:
        json.dump(payload, fh, ensure_ascii=False)
    os.replace(tmp, out_path)
    sys.exit(0)


try:
    creds = load_credentials()
except Exception as e:
    fail(f"credentials unavailable: {type(e).__name__}")

oauth = creds.get("claudeAiOauth") or {}
token = oauth.get("accessToken")
if not token:
    fail("no accessToken in credentials")

expires_at = oauth.get("expiresAt")
if expires_at and expires_at / 1000 < time.time():
    fail("access token expired; open Claude Code once to refresh it")

req = urllib.request.Request(
    "https://api.anthropic.com/api/oauth/usage",
    headers={
        "Authorization": f"Bearer {token}",
        "anthropic-beta": "oauth-2025-04-20",
        "Content-Type": "application/json",
        "User-Agent": "ntj-agent-quota/0.1",
    },
)
try:
    with urllib.request.urlopen(req, timeout=15) as resp:
        data = json.loads(resp.read().decode("utf-8"))
except urllib.error.HTTPError as e:
    fail(f"usage API HTTP {e.code}")
except Exception as e:
    fail(f"usage API request failed: {type(e).__name__}: {e}")


def find_windows(obj, key_hint=None):
    """utilization を持つ dict を残量ウィンドウとみなして拾う(構造変化に強くする)"""
    found = []
    if isinstance(obj, dict):
        util = obj.get("utilization")
        if util is not None:
            found.append((key_hint, obj))
        else:
            for k, v in obj.items():
                found.extend(find_windows(v, k))
    elif isinstance(obj, list):
        for v in obj:
            found.extend(find_windows(v, key_hint))
    return found


for key, w in find_windows(data):
    util = w.get("utilization")
    try:
        pct = float(util)
    except (TypeError, ValueError):
        continue
    resets = w.get("resets_at") or w.get("resetsAt")
    result["windows"].append(
        {
            "label": LABELS.get(key, key or "?"),
            "used_percent": pct,
            "resets_at": resets,
        }
    )

# 全ウィンドウが1.0以下なら0-1スケールとみなして%に変換(API仕様変更への保険)
if result["windows"] and all(w["used_percent"] <= 1.0 for w in result["windows"]):
    for w in result["windows"]:
        w["used_percent"] *= 100

result["ok"] = bool(result["windows"])
if not result["ok"]:
    result["error"] = "usage API response had no utilization fields"
    result["response_keys"] = sorted(data.keys()) if isinstance(data, dict) else str(type(data))

tmp = out_path + ".tmp"
with open(tmp, "w") as fh:
    json.dump(result, fh, ensure_ascii=False)
os.replace(tmp, out_path)
PY
