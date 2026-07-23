#!/usr/bin/env python3
# サイドバー表示の常駐ループ。pane ごとに context / quota トークンを注入する。
# context_ticker.sh (singleton ロック) から exec される前提。直接起動しないこと。
#
# 表示ルール (agent_status で切り替える):
#   working 中 pane: pac-man アニメーション (左→右へ約11秒で進みループ) + quota。
#     37% ᗧ··········ᗣ   ← 左端が消費済みコンテキスト%、ᗣ はゴースト
#   done/blocked pane: 「ctx 37%」+「5h: 56% · 1w: 72% left」(残量) の2行
#   done/working -> idle pane: 同じ静的表示を5分残してから非表示。
#   それ以外の idle/unknown pane: context / quota は非表示。
#
# データ源はローカルファイルのみ (API は呼ばない):
#   claude: ~/.claude/projects/*/<session-uuid>.jsonl の最後の assistant usage
#   codex:  ~/.codex/sessions/**/rollout-*.jsonl の最後の token_count イベント
#   quota:  update.sh が収集する STATE_DIR/{claude,codex}.json
import atexit
import glob
import json
import os
import select
import signal
import subprocess
import sys
import time

HERDR = os.environ.get("HERDR_BIN_PATH", "herdr")
STATE_DIR = os.environ.get("HERDR_PLUGIN_STATE_DIR") or os.path.expanduser(
    "~/.cache/herdr-agent-quota"
)
LOCKDIR = os.path.join(STATE_DIR, "ticker.lock")
PIDFILE = os.path.join(LOCKDIR, "pid")
SOURCE = "boooowy.agent-quota"

ANIM_TICK = 1  # working pane のアニメーション再生中
ACTIVE_TICK = 5  # 誰かが working の間の更新間隔(秒)
IDLE_TICK = 30  # 全員非 working のときの監視間隔(秒)
IDLE_EXIT_SEC = 600  # 全員非 working がこの秒数続いたら退場
IDLE_GRACE_SEC = 300  # 完了を確認して idle になった後も静的表示を残す時間
TICK_TTL_MS = "300000"  # ループ中のTTL: 収集待ちをまたいでも切れない5分
FINAL_TTL_MS = "900000"  # 退場時の最終report: done/blocked表示を15分残す
CELLS = 10  # pac-man トラックのドット数
COLLECT_INTERVAL = 60  # quota 収集(update.sh)を叩く間隔(秒)
TAIL_INIT_BYTES = 256 * 1024  # 初回に読む末尾バイト数(以降は増分のみ)
STYLE_LEVELS = ("normal", "caution", "warning", "danger")
MAX_QUOTA_SLOTS = 5
MAX_TOKEN_PATCHES_PER_REPORT = 16  # Herdr APIの1 reportあたりの上限
CONTEXT_STYLE_TOKENS = tuple(f"context_{level}" for level in STYLE_LEVELS)
QUOTA_STYLE_TOKENS = tuple(
    f"quota_{slot}_{level}"
    for slot in range(1, MAX_QUOTA_SLOTS + 1)
    for level in STYLE_LEVELS
)
ALL_DISPLAY_TOKENS = ("context", "quota") + CONTEXT_STYLE_TOKENS + QUOTA_STYLE_TOKENS

ASCII_MODE = os.environ.get("AGENT_QUOTA_ASCII") == "1" or os.path.exists(
    os.path.join(STATE_DIR, "ascii")
)


def render_pacman(pct, frame, animate):
    """working pane 用。例: 37% ᗧ··········ᗣ / 37%   ●········ᗣ。"""
    if ASCII_MODE:
        mouth_open, mouth_closed, dot, ghost = "C", "O", ".", "@"
    else:
        mouth_open, mouth_closed, dot, ghost = "ᗧ", "●", "·", "ᗣ"
    if animate:
        pos = frame % (CELLS + 1)  # 1秒tick = 1マス/秒 → 約11秒でループ
        pac = mouth_open if frame % 2 == 0 else mouth_closed
    else:
        pos, pac = 0, mouth_open
    shown = min(max(pct, 0), 999)
    return f"{shown:.0f}% {' ' * pos}{pac}{dot * (CELLS - pos)}{ghost}"


def ctx_text(pct):
    """done/blocked pane 用。例: ctx 37%"""
    return f"ctx {min(max(pct, 0), 999):.0f}%"


def context_style_level(pct):
    """context使用率を通常/注意/警告/危険の4段階へ分類する。"""
    if pct < 60:
        return "normal"
    if pct < 80:
        return "caution"
    if pct < 90:
        return "warning"
    return "danger"


def quota_style_level(remaining):
    """quota残量を通常/注意/警告/危険の4段階へ分類する。"""
    if remaining >= 60:
        return "normal"
    if remaining >= 30:
        return "caution"
    if remaining >= 15:
        return "warning"
    return "danger"


def styled_token_args(prefix, active_level=None, value=None):
    """4段階tokenのうちactiveだけを設定し、残りを明示的に消す。"""
    args = []
    for level in STYLE_LEVELS:
        token = f"{prefix}_{level}"
        if level == active_level and value is not None:
            args += ["--token", f"{token}={value}"]
        else:
            args += ["--clear-token", token]
    return args


def clear_display_token_args():
    """legacy tokenを含む、このplugin所有の全表示tokenを消す。"""
    args = []
    for token in ALL_DISPLAY_TOKENS:
        args += ["--clear-token", token]
    return args


def display_mode(status):
    """agent_status ごとの基本表示。idle の猶予判定は別途行う。"""
    if status == "working":
        return "working"
    if status in ("done", "blocked"):
        return "static"
    return None


def next_display_state(previous_status, status, grace_deadline, now):
    """状態遷移から表示modeとidle猶予期限を返す。

    表示中のpaneでは done が一瞬で idle になることがあるため、working -> idle
    も完了相当として扱う。既にidleのpaneをticker起動時に拾っても猶予は開始しない。
    """
    if status == "idle":
        if previous_status in ("working", "done"):
            grace_deadline = now + IDLE_GRACE_SEC
        if grace_deadline is not None and now < grace_deadline:
            return "static", grace_deadline
        return None, None
    return display_mode(status), None


def remaining_ttl_ms(deadline, now):
    """更新のたびに猶予が延びないよう、期限までの残りTTLを返す。"""
    return str(max(1, int((deadline - now) * 1000)))


def render_quota_windows(windows):
    """quota windowをlegacy文字列と個別着色slotへ整形する。"""
    rendered = []
    for window in windows:
        try:
            remaining = min(max(100.0 - float(window["used_percent"]), 0.0), 100.0)
        except (KeyError, TypeError, ValueError):
            continue
        rendered.append(
            {
                "label": str(window.get("label") or "?"),
                "remaining": remaining,
                "level": quota_style_level(remaining),
            }
        )
    if not rendered:
        return None

    legacy = ", ".join(
        f"{window['label']} {window['remaining']:.0f}%" for window in rendered
    ) + " left"
    slots = []
    for index, window in enumerate(rendered[:MAX_QUOTA_SLOTS], start=1):
        value = f"{window['label']}: {window['remaining']:.0f}%"
        if index == min(len(rendered), MAX_QUOTA_SLOTS):
            value += " left"
        slots.append(
            {
                "slot": index,
                "level": window["level"],
                "value": value,
            }
        )
    return {"legacy": legacy, "slots": slots}


def quota_display(kind):
    """残量表示を読む。例: 5h: 56% · 1w: 72% left / 1m: 93% left"""
    try:
        with open(os.path.join(STATE_DIR, f"{kind}.json")) as fh:
            data = json.load(fh)
    except (OSError, ValueError):
        return None
    if not data.get("ok"):
        return None
    return render_quota_windows(data.get("windows") or [])


def quota_token_args(display):
    """legacy quotaと、windowごとの段階別token patchを返す。"""
    args = ["--token", f"quota={display['legacy']}"]
    slots = {slot["slot"]: slot for slot in display["slots"]}
    for index in range(1, MAX_QUOTA_SLOTS + 1):
        slot = slots.get(index)
        args += styled_token_args(
            f"quota_{index}",
            slot["level"] if slot else None,
            slot["value"] if slot else None,
        )
    return args


def split_report_args(args):
    """token patchをHerdr API上限ごとに分割し、TTL等は各reportへ複製する。"""
    patches = []
    common = []
    for index in range(0, len(args), 2):
        pair = args[index : index + 2]
        if len(pair) != 2:
            raise ValueError("report args must be option/value pairs")
        if pair[0] in ("--token", "--clear-token"):
            patches.append(pair)
        else:
            common += pair
    if not patches:
        return [common] if common else []
    chunks = []
    for start in range(0, len(patches), MAX_TOKEN_PATCHES_PER_REPORT):
        chunk = []
        for pair in patches[start : start + MAX_TOKEN_PATCHES_PER_REPORT]:
            chunk += pair
        chunks.append(chunk + common)
    return chunks


def report(pane_id, args):
    for chunk in split_report_args(args):
        subprocess.run(
            [HERDR, "pane", "report-metadata", pane_id, "--source", SOURCE]
            + chunk,
            check=False,
            capture_output=True,
            timeout=10,
        )


def herdr_agents():
    try:
        proc = subprocess.run(
            [HERDR, "agent", "list"], capture_output=True, text=True, timeout=10
        )
        if proc.returncode != 0:
            return None
        return json.loads(proc.stdout)["result"]["agents"]
    except Exception:
        return None


def tail_lines(path, st):
    """増分 tail-read。st["pos"] 以降の完全な行を bytes で返す。"""
    size = os.path.getsize(path)
    if size < st["pos"]:  # ファイルが縮んだ(作り直し等) → 読み直し
        st["pos"] = 0
        st.pop("usage", None)
        st.pop("used", None)
    if st["pos"] == 0 and size > TAIL_INIT_BYTES:
        st["pos"] = size - TAIL_INIT_BYTES  # 初回は末尾だけ(欠け行はparse失敗でskip)
    with open(path, "rb") as fh:
        fh.seek(st["pos"])
        chunk = fh.read()
    st["pos"] = size
    return chunk.splitlines()


# --- claude ---------------------------------------------------------------
# uuid -> {"path": str|None, "checked": ts}。未解決uuidのみ60秒ごとに再グロブ。
claude_paths = {}
claude_tails = {}  # path -> {"pos", "usage", "model"}


def claude_percent(uuid):
    now = time.time()
    ent = claude_paths.get(uuid)
    if ent is None or (ent["path"] is None and now - ent["checked"] >= 60):
        matches = glob.glob(os.path.expanduser(f"~/.claude/projects/*/{uuid}.jsonl"))
        path = max(matches, key=os.path.getmtime) if matches else None
        ent = {"path": path, "checked": now}
        claude_paths[uuid] = ent
    path = ent["path"]
    if not path:
        return None
    st = claude_tails.setdefault(path, {"pos": 0, "usage": None, "model": None})
    try:
        lines = tail_lines(path, st)
    except OSError:
        claude_paths.pop(uuid, None)
        claude_tails.pop(path, None)
        return None
    for line in lines:
        if b'"usage"' not in line:
            continue
        try:
            rec = json.loads(line)
        except ValueError:
            continue
        if rec.get("type") != "assistant" or rec.get("isSidechain"):
            continue  # サブエージェントの usage は本体コンテキストではない
        usage = (rec.get("message") or {}).get("usage") or {}
        if "input_tokens" in usage:
            st["usage"] = usage
            st["model"] = (rec.get("message") or {}).get("model")
    usage = st["usage"]
    if not usage:
        return None
    used = (
        (usage.get("input_tokens") or 0)
        + (usage.get("cache_creation_input_tokens") or 0)
        + (usage.get("cache_read_input_tokens") or 0)
    )
    return used / claude_window(st["model"]) * 100


# コンテキストウィンドウが 1M のモデル群 (Fable/Mythos/Sonnet 5/Opus 4.6+/Sonnet 4.6)。
# Haiku 4.5 や旧世代 (Sonnet 4.5, Opus 4.5 以前) は 200k。
WINDOW_1M_MARKERS = (
    "fable",
    "mythos",
    "sonnet-5",
    "opus-4-6",
    "opus-4-7",
    "opus-4-8",
    "sonnet-4-6",
)


def claude_window(model):
    m = (model or "").lower()
    if "[1m]" in m or any(k in m for k in WINDOW_1M_MARKERS):
        return 1_000_000
    return 200_000


# --- codex ----------------------------------------------------------------
codex_index = {"ts": 0.0, "map": {}}  # cwd -> rollout path (mtime最新)
codex_tails = {}  # path -> {"pos", "used", "window"}


def codex_session_map():
    now = time.time()
    if now - codex_index["ts"] < 60:
        return codex_index["map"]
    files = sorted(
        glob.glob(os.path.expanduser("~/.codex/sessions/*/*/*/rollout-*.jsonl")),
        key=os.path.getmtime,
        reverse=True,
    )[:30]
    mapping = {}
    for f in files:  # mtime降順なので、同一cwdは最初に見つけたものが最新
        try:
            with open(f, encoding="utf-8", errors="replace") as fh:
                meta = json.loads(fh.readline())
        except (OSError, ValueError):
            continue
        cwd = (meta.get("payload") or {}).get("cwd")
        if cwd and cwd not in mapping:
            mapping[cwd] = f
    codex_index["ts"] = now
    codex_index["map"] = mapping
    return mapping


def codex_percent(cwd):
    path = codex_session_map().get(cwd)
    if not path:
        return None
    st = codex_tails.setdefault(path, {"pos": 0, "used": None, "window": None})
    try:
        lines = tail_lines(path, st)
    except OSError:
        codex_tails.pop(path, None)
        return None
    for line in lines:
        if b'"token_count"' not in line:
            continue
        try:
            rec = json.loads(line)
        except ValueError:
            continue
        payload = rec.get("payload") or {}
        if payload.get("type") != "token_count":
            continue
        info = payload.get("info") or {}
        last = info.get("last_token_usage") or {}
        used = last.get("total_tokens") or last.get("input_tokens")
        if used:
            st["used"] = used
        if info.get("model_context_window"):
            st["window"] = info["model_context_window"]
    if not st["used"] or not st["window"]:
        return None
    return st["used"] / st["window"] * 100


# --- lifecycle ------------------------------------------------------------
def cleanup_lock():
    # ロックが自分のものである場合のみ片付ける(stale回収で奪われた後に壊さない)
    try:
        with open(PIDFILE) as fh:
            if int(fh.read().strip()) != os.getpid():
                return
    except (OSError, ValueError):
        return
    try:
        os.remove(PIDFILE)
        os.rmdir(LOCKDIR)
    except OSError:
        pass


def heartbeat():
    """pid を touch して生存を示す。ロックを奪われていたら False。"""
    try:
        with open(PIDFILE) as fh:
            if int(fh.read().strip()) != os.getpid():
                return False
        os.utime(PIDFILE, None)
        return True
    except (OSError, ValueError):
        return False


def gc_caches(agents):
    live_uuids = {
        (a.get("agent_session") or {}).get("value")
        for a in agents
        if a.get("agent") == "claude"
    }
    for uuid in list(claude_paths):
        if uuid not in live_uuids:
            path = claude_paths.pop(uuid)["path"]
            claude_tails.pop(path, None)
    live_codex = set(
        codex_index["map"].get(a.get("cwd")) for a in agents if a.get("agent") == "codex"
    )
    for path in list(codex_tails):
        if path not in live_codex:
            codex_tails.pop(path)


def interruptible_sleep(timeout, wake_fd):
    """timeout 秒だけ待つが、wake_fd に何か来たら即座に返る(SIGUSR1 割り込み用)。"""
    try:
        ready, _, _ = select.select([wake_fd], [], [], timeout)
    except InterruptedError:
        return
    if ready:
        try:
            os.read(wake_fd, 4096)  # パイプをドレイン
        except OSError:
            pass


def main():
    atexit.register(cleanup_lock)
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, lambda *_: sys.exit(0))

    # イベント(on_event.sh)から SIGUSR1 で叩かれたら sleep を即中断して再ポーリング。
    # set_wakeup_fd がシグナル番号を書き込むので、ハンドラ自体は no-op でよい。
    wake_r, wake_w = os.pipe()
    os.set_blocking(wake_w, False)
    signal.set_wakeup_fd(wake_w)
    signal.signal(signal.SIGUSR1, lambda *_: None)

    plugin_dir = os.path.dirname(os.path.abspath(__file__))
    last_working = time.time()
    last_collect = 0.0
    consecutive_failures = 0
    frame = 0
    reported_ctx = set()  # 直前までに $context を report した pane_id
    reported_quota = set()  # 直前までに $quota を report した pane_id
    cleared_hidden = set()  # idle/unknown で token clear 済みの pane_id
    previous_status = {}  # pane_id -> 直前に観測した agent_status
    idle_grace_deadlines = {}  # pane_id -> done/working -> idle 後の表示期限

    while True:
        agents = herdr_agents()
        if agents is None:
            consecutive_failures += 1
            if consecutive_failures >= 2:
                sys.exit(1)  # herdr server 消滅とみなして自滅
            time.sleep(ACTIVE_TICK)
            continue
        consecutive_failures = 0
        if not heartbeat():
            sys.exit(0)  # ロックを奪われた(stale回収された)

        now = time.time()
        collect_due = now - last_collect >= COLLECT_INTERVAL

        working = any(a.get("agent_status") == "working" for a in agents)
        if working:
            last_working = now
        finalize = not working and now - last_working >= IDLE_EXIT_SEC
        ttl = FINAL_TTL_MS if finalize else TICK_TTL_MS

        fast = working and not finalize  # working な pane があれば1秒tickでアニメ
        # アニメーション中(1秒tick)は毎tick working pane のみ更新し、
        # done/blocked pane とキャッシュ掃除は5秒に1回だけ行う
        full = (not fast) or (frame % 5 == 0)

        quotas = {k: quota_display(k) for k in ("claude", "codex")}

        for a in agents:
            pane_id = a.get("pane_id")
            kind = a.get("agent")
            if not pane_id or kind not in ("claude", "codex"):
                continue
            status = a.get("agent_status")
            mode, grace_deadline = next_display_state(
                previous_status.get(pane_id),
                status,
                idle_grace_deadlines.get(pane_id),
                now,
            )
            previous_status[pane_id] = status
            if grace_deadline is None:
                idle_grace_deadlines.pop(pane_id, None)
            else:
                idle_grace_deadlines[pane_id] = grace_deadline
            is_working = mode == "working"
            if mode is None:
                # ticker 再起動後も期限の残った token を確実に消すため、
                # ローカルの reported_* だけに依存せず hidden 初回は明示 clear する。
                if pane_id not in cleared_hidden:
                    report(pane_id, clear_display_token_args())
                    cleared_hidden.add(pane_id)
                reported_ctx.discard(pane_id)
                reported_quota.discard(pane_id)
                continue

            cleared_hidden.discard(pane_id)
            if not full and not is_working:
                continue

            pct = None
            if kind == "claude":
                uuid = (a.get("agent_session") or {}).get("value")
                if uuid:
                    pct = claude_percent(uuid)
            else:
                pct = codex_percent(a.get("cwd"))

            args = []
            set_any = False
            if pct is not None:
                if is_working:
                    value = render_pacman(pct, frame, True)
                else:
                    value = ctx_text(pct)
                args += ["--token", f"context={value}"]
                args += styled_token_args(
                    "context", context_style_level(pct), value
                )
                set_any = True
                reported_ctx.add(pane_id)
            elif pane_id in reported_ctx:
                args += ["--clear-token", "context"]
                args += styled_token_args("context")
                reported_ctx.discard(pane_id)

            quota = quotas.get(kind)
            if quota:
                args += quota_token_args(quota)
                set_any = True
                reported_quota.add(pane_id)
            elif pane_id in reported_quota:
                args += ["--clear-token", "quota"]
                for index in range(1, MAX_QUOTA_SLOTS + 1):
                    args += styled_token_args(f"quota_{index}")
                reported_quota.discard(pane_id)

            if set_any:
                pane_ttl = (
                    remaining_ttl_ms(grace_deadline, now)
                    if grace_deadline is not None
                    else ttl
                )
                args += ["--ttl-ms", pane_ttl]
            if args:
                report(pane_id, args)

        if full:
            live = {a.get("pane_id") for a in agents}
            reported_ctx &= live
            reported_quota &= live
            cleared_hidden &= live
            previous_status = {
                pane_id: status
                for pane_id, status in previous_status.items()
                if pane_id in live
            }
            idle_grace_deadlines = {
                pane_id: deadline
                for pane_id, deadline in idle_grace_deadlines.items()
                if pane_id in live
            }
            gc_caches(agents)

        if finalize:
            sys.exit(0)

        if collect_due:
            last_collect = now
            # 既存の表示を先にrefreshしてからquotaを収集する。収集完了後は
            # sleepせず再ループし、新しいquotaをすぐsidebarへ反映する。
            subprocess.run(
                ["bash", os.path.join(plugin_dir, "update.sh")],
                check=False,
                capture_output=True,
                timeout=120,
            )
            frame += 1
            continue

        frame += 1
        sleep_for = ANIM_TICK if fast else (ACTIVE_TICK if working else IDLE_TICK)
        if idle_grace_deadlines:
            next_deadline = min(idle_grace_deadlines.values())
            sleep_for = min(sleep_for, max(0, next_deadline - time.time()))
        interruptible_sleep(
            sleep_for, wake_r
        )


if __name__ == "__main__":
    main()
