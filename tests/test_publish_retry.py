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
from core.jobs import jobs_root, write_record
from ui.tabs.publish_pr_tab import BranchPrDetectWorker, LatestPrDetectWorker, PublishPrTab


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
        for name in ("BranchPrDetectWorker.start", "LatestPrDetectWorker.start", "ActivePrsFetchWorker.start"):
            patcher = patch("ui.tabs.publish_pr_tab." + name)
            patcher.start()
            self.addCleanup(patcher.stop)
        patcher = patch("ui.components.pr_checks_view.ChecksFetchWorker.start")
        patcher.start()
        self.addCleanup(patcher.stop)
        patcher = patch.dict(os.environ, {"XDG_STATE_HOME": str(self.repo / ".git" / "state")})
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

    def test_full_checks_pass_the_full_flag(self):
        self.tab.full_checks_cb.setChecked(True)
        self.assertIn("--full", self.publish_arguments().args[1])

    def save_job(self, code=1, output="", pending=None):
        directory = jobs_root() / "saved"
        directory.mkdir(parents=True)
        record = {
            "cwd": str(self.repo), "key": "publish", "command": ["publish.sh"],
            "started_at": 1, "finished_at": 2, "exit_code": code,
        }
        write_record(directory / "job.json", record)
        (directory / "output.log").write_text(output)
        if pending is not None:
            saved = {
                "base": "main", "auto_merge": self.tab.auto_merge_cb.isChecked(),
                "wait": self.tab.wait_cb.isChecked(), "full": False, "i18n": "none",
                **pending, "repo_path": str(self.repo), "job_id": directory.name,
            }
            StateManager.update(rufin_publish_pending=saved)
        return record

    def test_old_failed_log_does_not_replace_current_pr_or_select_terminal(self):
        current = {"number": 8, "title": "Current work", "state": "OPEN"}
        self.tab._on_open_pr_detected(current, self.tab._form_context)
        self.tab.bottom_tabs.setCurrentWidget(self.tab.checks_view)
        self.save_job(output="old output\n" * 30000 + "https://github.com/example/repo/pull/7\n")
        self.tab.runner.attach(str(self.repo), "publish")
        while self.tab.runner.timer.isActive():
            self.tab.runner._poll()
        self.assertEqual(self.tab.detected_pr_number, "8")
        self.assertEqual(StateManager.get("rufin_tracked_pr")["number"], "8")
        self.assertIs(self.tab.bottom_tabs.currentWidget(), self.tab.checks_view)
        self.assertIn("/pull/7", self.tab.terminal.console.toPlainText())
        self.assertIn("Exit 1", self.tab.terminal.status_badge.text())

    def test_success_while_closed_releases_the_restored_branch(self):
        self.save_job(code=0, pending={
            "branch": "old-topic", "title": "feat: old work", "body": "Old description",
        })
        self.tab._restore_pending_publish()
        self.tab.runner.attach(str(self.repo), "publish")
        self.tab.runner._poll()
        self.assertIsNone(StateManager.get("rufin_publish_pending"))
        self.assertEqual(self.tab.branch_input.text(), "")
        self.assertEqual(self.tab.title_input.text(), "")
        self.assertEqual(self.tab._form_context[1], "connect")
        self.assertEqual(self.tab.body_input.toPlainText(), self.body)

    def test_failed_saved_publish_keeps_retry_form_without_forcing_terminal(self):
        self.save_job(pending={"branch": "old-topic", "title": "feat: old work", "body": "Retry"})
        self.tab._restore_pending_publish()
        self.tab.runner.attach(str(self.repo), "publish")
        self.tab.runner._poll()
        self.assertEqual(self.tab.branch_input.text(), "old-topic")
        self.assertEqual(self.tab.body_input.toPlainText(), "Retry")
        self.assertEqual(self.tab.file_list.btn_publish.text(), "Resume PR")
        self.assertIs(self.tab.bottom_tabs.currentWidget(), self.tab.checks_view)

    def test_branch_change_clears_previous_pr_before_lookup_finishes(self):
        self.tab._on_open_pr_detected({"number": 7, "state": "OPEN"}, self.tab._form_context)
        self.git("switch", "-qc", "new-work")
        self.tab.refresh()
        self.assertEqual(self.tab.detected_pr_number, "")
        self.assertIsNone(self.tab.checks_view.pr_number)
        self.assertIsNone(StateManager.get("rufin_tracked_pr"))

    def test_lookup_failure_clears_old_selection_and_reports_error(self):
        self.tab._on_open_pr_detected({"number": 7, "state": "OPEN"}, self.tab._form_context)
        self.tab._on_pr_lookup_error("Connection failed", self.tab._form_context)
        self.assertEqual(self.tab.detected_pr_number, "")
        self.assertEqual(self.tab.pr_banner_lbl.text(), "PR status unavailable")
        self.assertEqual(self.tab.pr_banner_lbl.toolTip(), "Connection failed")

    def test_lookup_failure_from_previous_branch_is_ignored(self):
        self.tab._on_open_pr_detected({"number": 8, "state": "OPEN"}, self.tab._form_context)
        self.tab._on_pr_lookup_error("Old failure", (str(self.repo), "old-topic"))
        self.assertEqual(self.tab.detected_pr_number, "8")

    def test_empty_latest_lookup_clears_previous_pr(self):
        self.tab._on_open_pr_detected({"number": 7, "state": "OPEN"}, self.tab._form_context)
        worker = LatestPrDetectWorker(str(self.repo))
        worker.pr_found.connect(lambda data: self.tab._on_latest_pr_detected(data, self.tab._form_context))
        result = subprocess.CompletedProcess([], 0, stdout="[]", stderr="")
        with patch("ui.tabs.publish_pr_tab.subprocess.run", return_value=result):
            worker.run()
        self.assertEqual(self.tab.detected_pr_number, "")

    def test_lookup_workers_report_errors(self):
        for worker in (BranchPrDetectWorker(str(self.repo), "connect"), LatestPrDetectWorker(str(self.repo))):
            with self.subTest(worker=type(worker).__name__):
                errors = []
                worker.error.connect(errors.append)
                result = subprocess.CompletedProcess([], 1, stdout="", stderr="Connection failed")
                with patch("ui.tabs.publish_pr_tab.subprocess.run", return_value=result):
                    worker.run()
                self.assertEqual(errors, ["Connection failed"])

    def test_merge_notification_is_not_repeated_by_old_open_metadata(self):
        checks = self.tab.checks_view
        data = {"number": 7, "state": "MERGED", "title": "Finished work", "statusCheckRollup": []}
        with patch("ui.components.pr_checks_view.send_desktop_notification") as notify:
            checks.set_pr("7", state="OPEN")
            checks._on_checks_received(data)
            self.assertEqual(notify.call_count, 1)
            checks.set_pr("7", state="OPEN")
            checks._on_checks_received(data)
            self.assertEqual(notify.call_count, 1)
        self.assertEqual(checks.pr_state, "MERGED")

    def test_restart_does_not_announce_a_historical_merge(self):
        self.save_job(code=0, output="https://github.com/example/repo/pull/7\n")
        StateManager.update(rufin_tracked_pr={"number": "7", "state": "MERGED"})
        self.tab._restore_saved_pr()
        with patch("ui.components.pr_checks_view.send_desktop_notification") as notify:
            self.tab.runner.attach(str(self.repo), "publish")
            self.tab.runner._poll()
            self.tab.checks_view._on_checks_received({"number": 7, "state": "MERGED"})
            notify.assert_not_called()
        self.assertEqual(StateManager.get("rufin_tracked_pr")["state"], "MERGED")

    def test_closed_pr_can_be_reopened(self):
        checks = self.tab.checks_view
        checks.set_pr("7", state="CLOSED")
        checks.set_pr("7", state="OPEN")
        self.assertEqual(checks.pr_state, "OPEN")

    def test_old_check_lookup_error_does_not_replace_new_pr_status(self):
        checks = self.tab.checks_view
        checks.set_pr("7", state="OPEN")
        worker = checks._worker
        checks.clear_pr()
        checks.set_pr("8", state="OPEN")
        worker.error.emit("Old PR lookup failed")
        self.assertNotEqual(checks.status_pill.text(), "Status unavailable")

    def test_completed_publish_keeps_a_new_branch_and_draft_entered_while_running(self):
        self.save_job(code=0, pending={"branch": "old-topic", "title": "Old", "body": "Old"})
        self.tab.branch_input.setText("new-work")
        self.tab.title_input.setText("New work")
        self.tab.body_input.setPlainText("New description")
        self.tab.body_input.document().setModified(True)
        self.tab.runner.attach(str(self.repo), "publish")
        self.tab.runner._poll()
        self.assertEqual(self.tab.branch_input.text(), "new-work")
        self.assertEqual(self.tab.title_input.text(), "New work")
        self.assertEqual(self.tab.body_input.toPlainText(), "New description")
        self.assertTrue(self.tab.body_input.document().isModified())
