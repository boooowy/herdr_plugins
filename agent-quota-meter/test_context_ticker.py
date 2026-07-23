import os
import tempfile
import time
import unittest
from unittest import mock

import context_ticker as ticker

from context_ticker import (
    IDLE_GRACE_SEC,
    context_style_level,
    next_display_state,
    quota_style_level,
    quota_token_args,
    remaining_ttl_ms,
    render_quota_windows,
    split_report_args,
    styled_token_args,
)


def option_pairs(args):
    return list(zip(args[::2], args[1::2]))


class StyleLevelTest(unittest.TestCase):
    def test_context_boundaries(self):
        self.assertEqual(context_style_level(59.99), "normal")
        self.assertEqual(context_style_level(60), "caution")
        self.assertEqual(context_style_level(79.99), "caution")
        self.assertEqual(context_style_level(80), "warning")
        self.assertEqual(context_style_level(89.99), "warning")
        self.assertEqual(context_style_level(90), "danger")

    def test_quota_boundaries(self):
        self.assertEqual(quota_style_level(60), "normal")
        self.assertEqual(quota_style_level(59.99), "caution")
        self.assertEqual(quota_style_level(30), "caution")
        self.assertEqual(quota_style_level(29.99), "warning")
        self.assertEqual(quota_style_level(15), "warning")
        self.assertEqual(quota_style_level(14.99), "danger")

    def test_only_active_style_token_is_set(self):
        pairs = option_pairs(styled_token_args("context", "warning", "ctx 82%"))

        self.assertIn(("--token", "context_warning=ctx 82%"), pairs)
        self.assertIn(("--clear-token", "context_normal"), pairs)
        self.assertIn(("--clear-token", "context_caution"), pairs)
        self.assertIn(("--clear-token", "context_danger"), pairs)


class QuotaRenderingTest(unittest.TestCase):
    def test_windows_keep_order_and_receive_individual_levels(self):
        display = render_quota_windows(
            [
                {"label": "5h", "used_percent": 67},
                {"label": "1w", "used_percent": 34},
            ]
        )

        self.assertEqual(display["legacy"], "5h 33%, 1w 66% left")
        self.assertEqual(
            display["slots"],
            [
                {"slot": 1, "level": "caution", "value": "5h: 33%"},
                {"slot": 2, "level": "normal", "value": "1w: 66% left"},
            ],
        )

    def test_remaining_percentage_is_clamped(self):
        display = render_quota_windows(
            [
                {"label": "low", "used_percent": 150},
                {"label": "high", "used_percent": -20},
            ]
        )

        self.assertEqual(display["slots"][0]["value"], "low: 0%")
        self.assertEqual(display["slots"][0]["level"], "danger")
        self.assertEqual(display["slots"][1]["value"], "high: 100% left")
        self.assertEqual(display["slots"][1]["level"], "normal")

    def test_invalid_windows_are_skipped(self):
        display = render_quota_windows(
            [{"label": "bad"}, {"label": "1m", "used_percent": 5}]
        )

        self.assertEqual(
            display["slots"],
            [{"slot": 1, "level": "normal", "value": "1m: 95% left"}],
        )

    def test_quota_args_set_each_active_slot_and_clear_other_levels(self):
        display = render_quota_windows(
            [
                {"label": "5h", "used_percent": 67},
                {"label": "1w", "used_percent": 34},
            ]
        )
        pairs = option_pairs(quota_token_args(display))

        self.assertIn(("--token", "quota=5h 33%, 1w 66% left"), pairs)
        self.assertIn(("--token", "quota_1_caution=5h: 33%"), pairs)
        self.assertIn(("--token", "quota_2_normal=1w: 66% left"), pairs)
        self.assertIn(("--clear-token", "quota_1_normal"), pairs)
        self.assertIn(("--clear-token", "quota_2_" + "danger"), pairs)
        self.assertIn(("--clear-token", "quota_5_warning"), pairs)


class ReportChunkingTest(unittest.TestCase):
    def test_token_patches_are_split_at_herdr_limit(self):
        args = []
        for index in range(21):
            args += ["--clear-token", f"token_{index}"]
        args += ["--ttl-ms", "300000"]

        chunks = split_report_args(args)

        self.assertEqual(len(chunks), 2)
        self.assertEqual(len(option_pairs(chunks[0][:-2])), 16)
        self.assertEqual(len(option_pairs(chunks[1][:-2])), 5)
        self.assertEqual(chunks[0][-2:], ["--ttl-ms", "300000"])
        self.assertEqual(chunks[1][-2:], ["--ttl-ms", "300000"])

    def test_malformed_report_args_are_rejected(self):
        with self.assertRaises(ValueError):
            split_report_args(["--clear-token"])


class TickerLockTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.original_paths = (
            ticker.STATE_DIR,
            ticker.RUNTIME_DIR,
            ticker.LOCKDIR,
            ticker.PIDFILE,
        )
        ticker.STATE_DIR = os.path.join(self.tempdir.name, "boooowy.agent-quota")
        ticker.RUNTIME_DIR = self.tempdir.name
        ticker.LOCKDIR = os.path.join(self.tempdir.name, "ticker.lock")
        ticker.PIDFILE = os.path.join(ticker.LOCKDIR, "pid")

    def tearDown(self):
        ticker.cleanup_lock()
        (
            ticker.STATE_DIR,
            ticker.RUNTIME_DIR,
            ticker.LOCKDIR,
            ticker.PIDFILE,
        ) = self.original_paths
        self.tempdir.cleanup()

    def write_lock(self, pid, owner=ticker.SOURCE, age=0):
        os.mkdir(ticker.LOCKDIR)
        with open(ticker.PIDFILE, "w") as fh:
            fh.write(f"{pid}\n{owner}\n")
        if age:
            old = time.time() - age
            os.utime(ticker.PIDFILE, (old, old))

    def test_new_lock_records_pid_and_plugin_id(self):
        self.assertTrue(ticker.acquire_lock())
        self.assertEqual(ticker.read_lock_record(), (os.getpid(), ticker.SOURCE))

    def test_same_plugin_does_not_start_twice(self):
        self.write_lock(os.getpid())

        self.assertFalse(ticker.acquire_lock())

    def test_different_plugin_is_stopped_and_replaced(self):
        old_pid = 12345
        self.write_lock(old_pid, "ntj.agent-quota")

        def fake_kill(pid, sig):
            self.assertEqual(pid, old_pid)
            if sig == ticker.signal.SIGTERM:
                os.remove(ticker.PIDFILE)
                os.rmdir(ticker.LOCKDIR)

        with mock.patch.object(ticker.os, "kill", side_effect=fake_kill):
            self.assertTrue(ticker.acquire_lock())
        self.assertEqual(ticker.read_lock_record(), (os.getpid(), ticker.SOURCE))

    def test_legacy_state_lock_is_stopped_before_shared_lock(self):
        legacy_lockdir = os.path.join(ticker.STATE_DIR, "ticker.lock")
        legacy_pidfile = os.path.join(legacy_lockdir, "pid")
        old_pid = 12345
        os.makedirs(legacy_lockdir)
        with open(legacy_pidfile, "w") as fh:
            fh.write(f"{old_pid}\n")

        def fake_kill(pid, sig):
            self.assertEqual(pid, old_pid)
            if sig == ticker.signal.SIGTERM:
                os.remove(legacy_pidfile)
                os.rmdir(legacy_lockdir)

        with mock.patch.object(ticker.os, "kill", side_effect=fake_kill):
            self.assertTrue(ticker.acquire_lock())
        self.assertFalse(os.path.exists(legacy_lockdir))
        self.assertEqual(ticker.read_lock_record(), (os.getpid(), ticker.SOURCE))

    def test_stale_lock_is_reclaimed_without_stopping_pid(self):
        self.write_lock(os.getpid(), "ntj.agent-quota", ticker.LOCK_STALE_SEC + 1)

        self.assertTrue(ticker.acquire_lock())
        self.assertEqual(ticker.read_lock_record(), (os.getpid(), ticker.SOURCE))


class IdleGraceTest(unittest.TestCase):
    def test_initial_idle_is_hidden(self):
        self.assertEqual(next_display_state(None, "idle", None, 100), (None, None))

    def test_done_to_idle_starts_static_grace(self):
        mode, deadline = next_display_state("done", "idle", None, 100)

        self.assertEqual(mode, "static")
        self.assertEqual(deadline, 100 + IDLE_GRACE_SEC)

    def test_working_to_idle_covers_unobserved_done(self):
        mode, deadline = next_display_state("working", "idle", None, 100)

        self.assertEqual(mode, "static")
        self.assertEqual(deadline, 100 + IDLE_GRACE_SEC)

    def test_repeated_idle_does_not_extend_grace(self):
        deadline = 100 + IDLE_GRACE_SEC

        self.assertEqual(
            next_display_state("idle", "idle", deadline, 200),
            ("static", deadline),
        )

    def test_grace_expires(self):
        deadline = 100 + IDLE_GRACE_SEC

        self.assertEqual(
            next_display_state("idle", "idle", deadline, deadline),
            (None, None),
        )

    def test_working_cancels_grace_and_restores_animation_mode(self):
        mode, deadline = next_display_state("idle", "working", 400, 200)

        self.assertEqual(mode, "working")
        self.assertIsNone(deadline)

    def test_blocked_to_idle_does_not_start_grace(self):
        self.assertEqual(
            next_display_state("blocked", "idle", None, 100),
            (None, None),
        )

    def test_remaining_ttl_uses_deadline(self):
        self.assertEqual(remaining_ttl_ms(400, 100), "300000")
        self.assertEqual(remaining_ttl_ms(400, 399.5), "500")


if __name__ == "__main__":
    unittest.main()
