import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

from PyQt6.QtWidgets import QApplication
from core.config import AppConfig
from core.git_manager import GitManager
from core.state import StateManager
from ui.tabs.publish_pr_tab import PublishPrTab


class PublishRetryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])

    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.repo = Path(directory.name)
        self.git("init", "-q", "-b", "connect")
        self.git("config", "user.name", "Test")
        self.git("config", "user.email", "test@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        self.git("commit", "--allow-empty", "-qm", "feat: add Connect")
        self.body = 'My edited description.\n\n  `code`, $HOME and "quotes".\n'
        self.git("config", "branch.connect.rufin-pr-title", "feat: add Connect")
        self.git("config", "branch.connect.rufin-pr-body", self.body)
        for name in ("BranchPrDetectWorker.start", "LatestPrDetectWorker.start"):
            patcher = patch("ui.tabs.publish_pr_tab." + name)
            patcher.start()
            self.addCleanup(patcher.stop)
        patcher = patch.object(StateManager, "_cached_state", {})
        patcher.start()
        self.addCleanup(patcher.stop)
        patcher = patch.object(StateManager, "save")
        patcher.start()
        self.addCleanup(patcher.stop)
        script = self.repo / ".git" / "publish.sh"
        script.write_text('#!/bin/sh\ngit branch --show-current\n')
        script.chmod(0o755)
        self.tab = PublishPrTab(GitManager(str(self.repo)), AppConfig(publish_pr_script=str(script)))
        self.addCleanup(self.tab.deleteLater)

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.repo), *args], check=True, capture_output=True, text=True).stdout

    def publish_arguments(self):
        with patch("ui.tabs.publish_pr_tab.ConfirmPreviewDialog") as dialog, patch.object(self.tab.runner, "start") as start:
            dialog.return_value.isVisible.return_value = False
            dialog.return_value.confirmed = True
            self.tab.run_publish()
            return start.call_args

    def test_restart_restores_saved_body_and_clean_retry_omits_commit_and_body(self):
        self.assertEqual(self.tab.body_input.toPlainText(), self.body)
        self.assertFalse(self.tab.body_input.document().isModified())
        self.assertEqual(self.tab.pr_title_label.text(), "PR: feat: add Connect")
        self.tab.title_input.setText("feat: add Connect")
        args = self.publish_arguments().args[1]
        self.assertNotIn("--body", args)
        self.assertNotIn("feat: add Connect", args)

    def test_followup_commit_keeps_saved_description(self):
        (self.repo / "fix.txt").write_text("fix\n")
        self.tab.refresh()
        self.tab.title_input.setText("fix: correct filenames")
        args = self.publish_arguments().args[1]
        self.assertIn("fix: correct filenames", args)
        self.assertNotIn("--body", args)

    def test_existing_pr_body_is_displayed_without_resubmitting_it(self):
        self.git("config", "--unset", "branch.connect.rufin-pr-body")
        with patch.object(self.tab.checks_view, "set_pr"):
            self.tab._on_open_pr_detected({
                "number": 7, "title": "feat: add Connect", "body": "Edited on GitHub",
                "url": "https://github.com/example/repo/pull/7", "state": "OPEN",
            }, self.tab._form_context)
        self.assertEqual(self.tab.body_input.toPlainText(), "Edited on GitHub")
        self.assertNotIn("--body", self.publish_arguments().args[1])

    def test_explicit_body_edits_and_clearing_are_sent_exactly(self):
        for body in (self.body + "\nNew paragraph.\n", ""):
            self.tab.body_input.setPlainText(body)
            self.tab.body_input.document().setModified(True)
            call = self.publish_arguments()
            args = call.args[1]
            self.assertEqual(args[args.index("--body") + 1], body)
            self.assertEqual(call.kwargs["env_vars"]["RUFIN_ALLOW_PR_BODY"], "1")

    def test_failed_push_keeps_the_form_for_resuming(self):
        self.tab.title_input.setText("feat: add Connect")
        self.tab._submitted_title = "feat: add Connect"
        self.tab._submitted_body = self.body
        self.tab._on_process_finished(1)
        self.assertEqual(self.tab.title_input.text(), "feat: add Connect")
        self.assertEqual(self.tab.body_input.toPlainText(), self.body)

    def test_success_does_not_discard_edits_made_while_running(self):
        self.tab._submitted_title = "feat: add Connect"
        self.tab._submitted_body = self.body
        self.tab.title_input.setText("fix: another change")
        self.tab.body_input.insertPlainText("New text ")
        body = self.tab.body_input.toPlainText()
        self.tab._on_process_finished(0)
        self.assertEqual(self.tab.body_input.toPlainText(), body)
        self.assertTrue(self.tab.body_input.document().isModified())
        self.assertEqual(self.tab.title_input.text(), "fix: another change")
