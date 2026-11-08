import os
import subprocess
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch

os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

from PyQt6.QtTest import QTest
from PyQt6.QtWidgets import QApplication

from core.config import AppConfig
from core.git_manager import GitManager
from core.publish_checks import pending_inputs
from core.state import StateManager
from ui.tabs.publish_pr_tab import PublishPrTab


class PublishWaitTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])

    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.repo = self.root / "work"
        self.repo.mkdir()
        self.git("init", "-q", "-b", "topic")
        self.git("config", "user.name", "Test")
        self.git("config", "user.email", "test@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        (self.repo / "source.txt").write_text("initial\n")
        self.git("add", ".")
        self.git("commit", "-qm", "feat: pending work")
        subprocess.run(["git", "init", "--bare", "-q", str(self.root / "origin.git")], check=True)
        self.git("remote", "add", "origin", str(self.root / "origin.git"))
        self.git("push", "-q", "origin", "topic")
        script = self.repo / ".git" / "publish.sh"
        script.write_text('''#!/usr/bin/env python3
import os, subprocess, sys, time
from pathlib import Path
args = sys.argv[1:]
prepare = args and args[0] == "--prepare-only"
if prepare:
    args.pop(0)
full = "--full" in args
branch = "topic"
while args and args[0].startswith("--"):
    option = args.pop(0)
    if option in ("--base", "--branch", "--body", "--phase"):
        value = args.pop(0)
        if option == "--branch":
            branch = value
if args:
    subject, *paths = args
    subprocess.run(["git", "add", "-A", "--", *(paths or ["."])], check=True)
    if subprocess.run(["git", "diff", "--cached", "--quiet"]).returncode:
        subprocess.run(["git", "commit", "-qm", subject], check=True)
while Path(".git/prepare-hold").exists():
    Path(".git/prepared").touch()
    time.sleep(0.01)
print("Publishing branch: " + branch, flush=True)
if prepare:
    print("Prepared PR worktree: " + os.getcwd(), flush=True)
else:
    subprocess.run(["git", "push", "-q", *([] if full else ["--no-verify"]), "origin", branch], check=True)
    Path(".git/published").write_text("full" if full else "light")
''')
        script.chmod(0o755)
        hook = self.repo / ".git" / "hooks" / "pre-push"
        hook.write_text('''#!/bin/sh
echo check >> .git/checks
while [ -f .git/hold ]; do sleep 0.01; done
if [ -f .git/fail ]; then exit 1; fi
''')
        hook.chmod(0o755)
        for target, value in (
            ("core.state.StateManager._cached_state", {}),
            ("core.state.StateManager.save", None),
            ("ui.tabs.publish_pr_tab.BranchPrDetectWorker.start", None),
            ("ui.tabs.publish_pr_tab.LatestPrDetectWorker.start", None),
            ("ui.tabs.publish_pr_tab.ActivePrsFetchWorker.start", None),
            ("ui.tabs.publish_pr_tab.PrPollWorker.start", None),
            ("ui.tabs.publish_pr_tab.send_desktop_notification", None),
            ("core.runner.send_desktop_notification", None),
        ):
            patcher = patch(target, value) if value is not None else patch(target)
            patcher.start()
            self.addCleanup(patcher.stop)
        patcher = patch.dict(os.environ, {"XDG_STATE_HOME": str(self.root / "state")})
        patcher.start()
        self.addCleanup(patcher.stop)
        self.tab = PublishPrTab(GitManager(str(self.repo)), AppConfig(publish_pr_script=str(script)))
        self.tab.runner.key = "publish"
        self.script = str(script)
        self.addCleanup(self.stop)

    def stop(self):
        self.tab._cancel_pr_wait()
        self.tab.runner.stop()
        self.until(lambda: not self.tab.runner.is_running)
        self.tab.deleteLater()

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.repo), *args], check=True,
                              capture_output=True, text=True).stdout

    def until(self, condition):
        deadline = time.monotonic() + 10
        while not condition() and time.monotonic() < deadline:
            QTest.qWait(20)
        self.assertTrue(condition(), self.tab.terminal.console.toPlainText())

    def schedule(self, paths=None, full=True, dirty=True):
        if dirty:
            (self.repo / "source.txt").write_text("pending\n")
        args = ["--branch", "topic", *(["--full"] if full else [])]
        retry_args = list(args)
        if dirty:
            args += ["feat: pending work", *(paths or [])]
        pending = {
            "repo_path": str(self.repo), "branch": "topic", "base": "main",
            "args": args, "retry_args": retry_args, "env": {}, "paths": paths,
            "title": "feat: pending work" if dirty else "", "body": "",
            "full": full,
        }
        self.tab._start_waiting_for_pr("7", self.script, args, {}, pending)

    def checks(self):
        path = self.repo / ".git" / "checks"
        return len(path.read_text().splitlines()) if path.exists() else 0

    def test_checks_finish_before_merge_and_publish_without_full(self):
        self.schedule()
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual(self.checks(), 1)
        self.assertFalse((self.repo / ".git/published").exists())
        self.tab._on_pr_status_fetched({"state": "MERGED"})
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual((self.repo / ".git/published").read_text(), "light")
        self.assertEqual(self.checks(), 1)

    def test_merge_during_checks_waits_for_success(self):
        (self.repo / ".git/hold").touch()
        self.schedule()
        self.until(lambda: self.checks() == 1)
        self.tab._on_pr_status_fetched({"state": "MERGED"})
        self.assertFalse((self.repo / ".git/published").exists())
        (self.repo / ".git/hold").unlink()
        self.until(lambda: (self.repo / ".git/published").exists())
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual(self.checks(), 1)

    def test_edits_after_success_rerun_checks_while_waiting(self):
        self.schedule()
        self.until(lambda: not self.tab.runner.is_running)
        (self.repo / "source.txt").write_text("edited\n")
        self.tab._poll_waiting_pr()
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual(self.checks(), 2)
        self.assertEqual(self.git("show", "HEAD:source.txt"), "edited\n")
        self.tab._on_pr_status_fetched({"state": "MERGED"})
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual(self.checks(), 2)

    def test_edits_during_checks_are_checked_again_before_publish(self):
        (self.repo / ".git/hold").touch()
        self.schedule()
        self.until(lambda: self.checks() == 1)
        (self.repo / "source.txt").write_text("edited during checks\n")
        self.tab._on_pr_status_fetched({"state": "MERGED"})
        (self.repo / ".git/hold").unlink()
        self.until(lambda: (self.repo / ".git/published").exists())
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual(self.checks(), 2)

    def test_failure_prevents_publication_after_merge(self):
        (self.repo / ".git/fail").touch()
        self.schedule()
        self.tab._on_pr_status_fetched({"state": "MERGED"})
        self.until(lambda: not self.tab.runner.is_running)
        self.assertFalse(self.tab._is_waiting_for_pr)
        self.assertFalse((self.repo / ".git/published").exists())
        self.assertIsNotNone(StateManager.get("rufin_publish_pending"))

    def test_cancel_stops_checks_and_prevents_late_merge_publication(self):
        (self.repo / ".git/hold").touch()
        self.schedule()
        self.until(lambda: self.checks() == 1)
        self.tab._stop_publish()
        self.tab._on_pr_status_fetched({"state": "MERGED"})
        self.until(lambda: not self.tab.runner.is_running)
        self.assertFalse((self.repo / ".git/published").exists())

    def test_focused_checks_publish_without_waiting_checks(self):
        self.schedule(full=False)
        self.assertEqual(self.checks(), 0)
        self.tab._on_pr_status_fetched({"state": "MERGED"})
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual((self.repo / ".git/published").read_text(), "light")
        self.assertEqual(self.checks(), 0)

    def test_selected_contents_detect_edits_without_touching_index(self):
        (self.repo / "source.txt").write_text("selected\n")
        (self.repo / "other.txt").write_text("unselected\n")
        self.git("add", "other.txt")
        staged = self.git("diff", "--cached", "--binary")
        first = pending_inputs(str(self.repo), "topic", ["source.txt"])
        (self.repo / "other.txt").write_text("changed unselected\n")
        self.assertEqual(first, pending_inputs(str(self.repo), "topic", ["source.txt"]))
        (self.repo / "source.txt").write_text("changed selected\n")
        self.assertNotEqual(first, pending_inputs(str(self.repo), "topic", ["source.txt"]))
        self.assertEqual(staged, self.git("diff", "--cached", "--binary"))

    def test_clean_scheduled_branch_picks_up_later_edits(self):
        self.schedule(dirty=False)
        self.until(lambda: not self.tab.runner.is_running)
        (self.repo / "source.txt").write_text("edited clean branch\n")
        self.tab._poll_waiting_pr()
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual(self.checks(), 2)
        self.assertEqual(self.git("show", "HEAD:source.txt"), "edited clean branch\n")

    def test_unselected_edits_do_not_rerun_or_enter_the_commit(self):
        (self.repo / "other.txt").write_text("unselected\n")
        self.schedule(paths=["source.txt"])
        self.until(lambda: not self.tab.runner.is_running)
        (self.repo / "other.txt").write_text("edited unselected\n")
        self.tab._poll_waiting_pr()
        self.assertFalse(self.tab.runner.is_running)
        self.assertEqual(self.checks(), 1)
        self.tab._on_pr_status_fetched({"state": "MERGED"})
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual(self.checks(), 1)
        self.assertEqual(self.git("ls-tree", "--name-only", "HEAD"), "source.txt\n")

    def test_selected_deletion_stays_readable_after_it_is_committed(self):
        (self.repo / "source.txt").unlink()
        before = pending_inputs(str(self.repo), "topic", ["source.txt"])
        self.git("add", "-A")
        self.git("commit", "-qm", "fix: remove source")
        after = pending_inputs(str(self.repo), "topic", ["source.txt"])
        self.assertEqual(before[2], after[2])

    def test_preparation_success_after_restart_keeps_the_pending_publish(self):
        self.schedule()
        self.until(lambda: not self.tab.runner.is_running)
        self.tab._is_waiting_for_pr = False
        self.tab._on_process_restored(dict(self.tab.runner.record, running=False))
        self.assertIsNotNone(StateManager.get("rufin_publish_pending"))
        self.assertFalse((self.repo / ".git/published").exists())

    def test_edits_during_preparation_are_included_before_checks(self):
        (self.repo / ".git/prepare-hold").touch()
        self.schedule()
        self.until(lambda: (self.repo / ".git/prepared").exists())
        (self.repo / "source.txt").write_text("edited during preparation\n")
        (self.repo / ".git/prepare-hold").unlink()
        self.until(lambda: not self.tab.runner.is_running)
        self.assertEqual(self.checks(), 1)
        self.assertEqual(self.git("show", "HEAD:source.txt"), "edited during preparation\n")

    def test_rescheduling_checks_keeps_the_selected_pr_wait(self):
        self.schedule()
        self.until(lambda: not self.tab.runner.is_running)
        self.tab._cancel_pr_wait()
        self.tab.wait_pr_cb.setChecked(True)
        with patch.object(self.tab, "_pending_form_matches", return_value=True), \
                patch.object(self.tab, "run_publish") as schedule, \
                patch.object(self.tab, "resume_publish") as resume:
            self.tab._publish_button_clicked()
        schedule.assert_called_once()
        resume.assert_not_called()

    def test_completion_notification_names_the_finished_checks(self):
        (self.repo / ".git/hold").touch()
        with patch("core.runner.send_desktop_notification") as notify:
            self.schedule()
            self.until(lambda: self.checks() == 1)
            self.tab._on_pr_status_fetched({"state": "MERGED"})
            (self.repo / ".git/hold").unlink()
            self.until(lambda: (self.repo / ".git/published").exists())
            self.until(lambda: not self.tab.runner.is_running)
        messages = [call.args[1] for call in notify.call_args_list]
        self.assertIn("publish_checks.py", messages[0])
        self.assertIn("publish.sh", messages[-1])
