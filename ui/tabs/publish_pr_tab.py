import os
import re
import json
import subprocess
from typing import List, Optional
from PyQt6.QtCore import Qt, QUrl, QThread, pyqtSignal
from PyQt6.QtGui import QDesktopServices
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QSplitter,
    QLabel,
    QLineEdit,
    QPlainTextEdit,
    QCheckBox,
    QComboBox,
    QPushButton,
    QGroupBox,
    QMessageBox,
    QFrame,
)
from core.config import AppConfig
from core.git_manager import GitManager
from core.runner import ProcessRunner
from ui.components.activity_tabs import ActivityTabs
from core.state import StateManager
from ui.components.file_list import FileListWidget
from ui.components.diff_viewer import DiffViewer
from ui.components.terminal_view import TerminalWidget
from ui.components.pr_checks_view import PrChecksWidget
from ui.dialogs.confirm_preview_dialog import ConfirmPreviewDialog


class BranchPrDetectWorker(QThread):
    pr_found = pyqtSignal(dict)

    def __init__(self, repo_path: str, branch: str, parent=None):
        super().__init__(parent)
        self.repo_path = repo_path
        self.branch = branch

    def run(self):
        try:
            cmd = [
                "gh",
                "pr",
                "list",
                "--head",
                self.branch,
                "--state",
                "all",
                "--limit",
                "1",
                "--json",
                "number,title,body,url,state",
            ]
            res = subprocess.run(
                cmd,
                cwd=self.repo_path,
                capture_output=True,
                text=True,
                timeout=10,
            )
            if res.returncode == 0 and res.stdout.strip():
                items = json.loads(res.stdout)
                self.pr_found.emit(items[0] if items else {})
        except Exception:
            pass


class LatestPrDetectWorker(QThread):
    pr_found = pyqtSignal(dict)

    def __init__(self, repo_path: str, parent=None):
        super().__init__(parent)
        self.repo_path = repo_path

    def run(self):
        try:
            # First try author @me to get the user's latest PR
            cmd = [
                "gh",
                "pr",
                "list",
                "--state",
                "all",
                "--author",
                "@me",
                "--limit",
                "1",
                "--json",
                "number,title,url,state",
            ]
            res = subprocess.run(
                cmd,
                cwd=self.repo_path,
                capture_output=True,
                text=True,
                timeout=10,
            )
            if res.returncode == 0 and res.stdout.strip():
                items = json.loads(res.stdout)
                if items:
                    self.pr_found.emit(items[0])
                    return

            # Fallback to general latest PR
            cmd_all = [
                "gh",
                "pr",
                "list",
                "--state",
                "all",
                "--limit",
                "1",
                "--json",
                "number,title,url,state",
            ]
            res_all = subprocess.run(
                cmd_all,
                cwd=self.repo_path,
                capture_output=True,
                text=True,
                timeout=10,
            )
            if res_all.returncode == 0 and res_all.stdout.strip():
                items = json.loads(res_all.stdout)
                if items:
                    self.pr_found.emit(items[0])
        except Exception:
            pass


class PublishPrTab(QWidget):
    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        self.runner = ProcessRunner(self)
        self.detected_pr_url = ""
        self.detected_pr_number = ""
        self._detect_worker: Optional[BranchPrDetectWorker] = None
        self._latest_worker: Optional[LatestPrDetectWorker] = None
        self._form_context = None
        self._submitted_title = ""
        self._submitted_body = ""
        self._output_pending = ""
        self._published_pr = ""

        self._init_ui()
        self._wire_signals()
        self.terminal.bind_runner(self.runner, self.git_mgr.repo_path, "publish", self.config.resolve_publish_script(self.git_mgr.repo_path))
        self._restore_saved_pr()
        self.refresh()

    def _init_ui(self):
        root_layout = QHBoxLayout(self)
        root_layout.setContentsMargins(6, 6, 6, 6)
        root_layout.setSpacing(8)

        self.main_splitter = QSplitter(Qt.Orientation.Horizontal)
        root_layout.addWidget(self.main_splitter)

        # ----------------- Left Panel -----------------
        self.left_splitter = QSplitter(Qt.Orientation.Vertical)

        # 1. Changes File List (Top, expands to take space)
        self.file_list = FileListWidget()
        self.left_splitter.addWidget(self.file_list)

        # 2. Form Widget (Bottom, fits without any scrollbar)
        form_widget = QWidget()
        form_layout = QVBoxLayout(form_widget)
        form_layout.setContentsMargins(0, 2, 0, 0)
        form_layout.setSpacing(4)

        # Branch configuration group
        branch_grp = QGroupBox("Branch Options")
        branch_layout = QVBoxLayout(branch_grp)
        branch_layout.setContentsMargins(8, 4, 8, 4)
        branch_layout.setSpacing(4)

        h_branch = QHBoxLayout()
        h_branch.setSpacing(6)
        lbl_branch = QLabel("Branch:")
        lbl_branch.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.branch_input = QLineEdit()
        self.branch_input.setPlaceholderText("Derived from title (on main)")
        h_branch.addWidget(lbl_branch)
        h_branch.addWidget(self.branch_input, 1)

        lbl_base = QLabel("Base:")
        lbl_base.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.base_input = QLineEdit(self.config.default_base_branch)
        self.base_input.setFixedWidth(80)
        h_branch.addWidget(lbl_base)
        h_branch.addWidget(self.base_input)
        branch_layout.addLayout(h_branch)

        form_layout.addWidget(branch_grp)

        # PR Content group
        pr_grp = QGroupBox("Pull Request Details")
        pr_layout = QVBoxLayout(pr_grp)
        pr_layout.setContentsMargins(8, 4, 8, 4)
        pr_layout.setSpacing(4)

        h_title_phase = QHBoxLayout()
        h_title_phase.setSpacing(6)
        self.pr_title_label = QLabel()
        self.pr_title_label.setWordWrap(True)
        self.pr_title_label.hide()
        pr_layout.addWidget(self.pr_title_label)

        lbl_title = QLabel("Commit:")
        lbl_title.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.title_input = QLineEdit()
        self.title_input.setPlaceholderText("e.g. feat(ui): add modern PR and release GUI")
        self.title_input.setToolTip("The first commit names the PR. Follow-up commits keep the PR title. Leave empty to retry existing commits.")
        h_title_phase.addWidget(lbl_title)
        h_title_phase.addWidget(self.title_input, 1)

        lbl_phase = QLabel("Phase:")
        lbl_phase.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.phase_input = QLineEdit()
        self.phase_input.setPlaceholderText("Defaults to TITLE")
        self.phase_input.setFixedWidth(100)
        h_title_phase.addWidget(lbl_phase)
        h_title_phase.addWidget(self.phase_input)
        pr_layout.addLayout(h_title_phase)

        self.body_input = QPlainTextEdit()
        self.body_input.setFixedHeight(45)
        self.body_input.setPlaceholderText("Description (optional markdown)...")
        pr_layout.addWidget(self.body_input)

        form_layout.addWidget(pr_grp)

        # Options group
        flags_grp = QGroupBox("Options")
        flags_layout = QVBoxLayout(flags_grp)
        flags_layout.setContentsMargins(8, 4, 8, 4)
        flags_layout.setSpacing(3)

        h_checks = QHBoxLayout()
        h_checks.setSpacing(10)
        self.auto_merge_cb = QCheckBox("Squash auto-merge")
        self.auto_merge_cb.setChecked(self.config.default_auto_merge)
        h_checks.addWidget(self.auto_merge_cb)

        self.wait_cb = QCheckBox("Wait CI checks (--wait)")
        self.wait_cb.setChecked(self.config.default_wait_ci)
        h_checks.addWidget(self.wait_cb)

        self.skip_pre_push_cb = QCheckBox("Skip pre-push")
        self.skip_pre_push_cb.setChecked(False)
        self.skip_pre_push_cb.setToolTip("Skip local pre-push hook verification (RUFIN_SKIP_PRE_PUSH_HOOK=1)")
        h_checks.addWidget(self.skip_pre_push_cb)

        h_checks.addStretch()
        flags_layout.addLayout(h_checks)

        h_i18n = QHBoxLayout()
        h_i18n.setSpacing(6)
        lbl_i18n = QLabel("Catalogs:")
        lbl_i18n.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.i18n_combo = QComboBox()
        self.i18n_combo.addItem("Normal (Ignore changed .po catalogs)", "none")
        self.i18n_combo.addItem("Include prepared .po files (--include-i18n-catalogs)", "include")
        self.i18n_combo.addItem("Sync and merge all .po catalogs (--sync-i18n-catalogs)", "sync")
        h_i18n.addWidget(lbl_i18n)
        h_i18n.addWidget(self.i18n_combo, 1)
        flags_layout.addLayout(h_i18n)

        form_layout.addWidget(flags_grp)

        self.left_splitter.addWidget(form_widget)

        # Splitter behavior: file_list expands (stretch 1), form stays at natural height (stretch 0)
        self.left_splitter.setStretchFactor(0, 1)
        self.left_splitter.setStretchFactor(1, 0)
        self.left_splitter.setCollapsible(0, False)
        self.left_splitter.setCollapsible(1, False)

        self.main_splitter.addWidget(self.left_splitter)

        # ----------------- Right Panel -----------------
        right_widget = QWidget()
        right_layout = QVBoxLayout(right_widget)
        right_layout.setContentsMargins(0, 0, 0, 0)
        right_layout.setSpacing(6)

        # Banner for detected PR URL
        self.pr_banner = QFrame()
        self.pr_banner.setStyleSheet(
            "QFrame { background-color: #161b22; border: 1px solid #30363d; border-radius: 6px; padding: 4px; }"
        )
        self.pr_banner.setVisible(False)
        h_banner = QHBoxLayout(self.pr_banner)
        h_banner.setContentsMargins(8, 2, 8, 2)
        self.pr_banner_lbl = QLabel("Pull Request opened:")
        self.pr_banner_lbl.setStyleSheet("color: #58a6ff; font-weight: 500;")
        h_banner.addWidget(self.pr_banner_lbl, 1)

        self.btn_view_checks = QPushButton("View Checks")
        self.btn_view_checks.setFixedHeight(24)
        self.btn_view_checks.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_view_checks.clicked.connect(self._focus_checks_tab)
        h_banner.addWidget(self.btn_view_checks)

        self.btn_open_pr = QPushButton("Open PR on GitHub")
        self.btn_open_pr.setFixedHeight(24)
        self.btn_open_pr.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_open_pr.clicked.connect(self._open_detected_pr)
        h_banner.addWidget(self.btn_open_pr)
        right_layout.addWidget(self.pr_banner)

        right_splitter = QSplitter(Qt.Orientation.Vertical)

        # Upper: Diff Viewer
        self.diff_viewer = DiffViewer(self.config, self.git_mgr.repo_path)
        self.diff_viewer.file_saved.connect(self._on_file_saved)
        right_splitter.addWidget(self.diff_viewer)

        # Lower: Tab widget with Checks & CI and Terminal Log
        self.bottom_tabs = ActivityTabs()
        self.bottom_tabs.setStyleSheet(
            "QTabWidget::tab-bar { alignment: left; } "
            "QTabBar::tab { font-size: 11px; padding: 4px 12px; margin-right: 2px; }"
        )

        self.checks_view = PrChecksWidget(repo_path=self.git_mgr.repo_path)
        self.terminal = TerminalWidget(repo_path=self.git_mgr.repo_path)

        self.bottom_tabs.addTab(self.checks_view, "PR Checks")
        self.bottom_tabs.addTab(self.terminal, "Terminal Log")
        right_splitter.addWidget(self.bottom_tabs)

        right_splitter.setSizes([340, 300])
        right_layout.addWidget(right_splitter)

        self.main_splitter.addWidget(right_widget)
        self.main_splitter.setSizes([380, 620])

    def _wire_signals(self):
        self.runner.started.connect(self._on_process_started)
        self.runner.restored.connect(self._on_process_restored)
        self.runner.error_occurred.connect(lambda message: self.bottom_tabs.show_activity("failed", self.terminal))
        self.checks_view.checks_updated.connect(self._focus_remote_checks)
        self.runner.started.connect(lambda: self.file_list.btn_publish.setEnabled(False))
        self.runner.restored.connect(lambda record: self.file_list.btn_publish.setEnabled(not record["running"]))
        self.runner.finished.connect(lambda code: self.file_list.btn_publish.setEnabled(True))
        self.file_list.file_selected.connect(self._show_single_file_diff)
        self.file_list.view_diff_requested.connect(self._show_multi_file_diff)
        self.file_list.publish_requested.connect(self.run_publish)

        self.runner.output_received.connect(self._on_output_received)
        self.runner.started.connect(lambda: self.terminal.set_status("Running", "running"))
        self.runner.finished.connect(self._on_process_finished)
        self.runner.error_occurred.connect(lambda msg: self.terminal.set_status("Error", "failed"))
        self.terminal.stop_requested.connect(self.runner.stop)
        self.branch_input.editingFinished.connect(self.refresh)

    def _restore_saved_pr(self):
        saved = StateManager.get("rufin_tracked_pr")
        if saved and isinstance(saved, dict) and saved.get("number"):
            self.detected_pr_number = str(saved["number"])
            self.detected_pr_url = saved.get("url", "")
            title = saved.get("title", "")
            state = saved.get("state", "")
            state_str = f" ({state.capitalize()})" if state else ""
            self.pr_banner_lbl.setText(f"Pull Request #{self.detected_pr_number}{state_str}: {title}")
            self.pr_banner.setVisible(True)
            self.checks_view.set_pr(self.detected_pr_number, title=title, url=self.detected_pr_url, state=state)

    def _on_file_saved(self, rel_path: str):
        self.refresh()
        self._show_single_file_diff(rel_path)

    def _focus_checks_tab(self):
        self.bottom_tabs.setCurrentWidget(self.checks_view)

    def _on_process_started(self):
        self._published_pr = ""
        self._output_pending = ""
        self.bottom_tabs.show_activity("local", self.terminal, force=True)

    def _on_process_restored(self, record):
        if record["running"]:
            self.bottom_tabs.show_activity("local", self.terminal)
        elif record.get("exit_code") != 0 or record.get("stopped"):
            self.bottom_tabs.show_activity("failed", self.terminal)

    def _focus_remote_checks(self, data):
        if not data.get("statusCheckRollup") or self.bottom_tabs.activity == "failed":
            return
        if self.runner.is_running and self._published_pr != str(data["number"]):
            return
        self.bottom_tabs.show_activity("remote", self.checks_view)

    def _on_output_received(self, text: str):
        self.terminal.append_output(text)
        self._output_pending += text
        if "\n" not in self._output_pending:
            return
        text, self._output_pending = self._output_pending.rsplit("\n", 1)
        m = re.search(r"https://github\.com/[^/\s]+/[^/\s]+/pull/(\d+)", text)
        if m:
            url = m.group(0)
            pr_num = m.group(1)
            if re.search(rf"(?m)^(?:Published PR #\d+: )?{re.escape(url)}\s*$", text):
                self._published_pr = pr_num
            self.detected_pr_url = url
            self.detected_pr_number = pr_num
            self.pr_banner_lbl.setText(f"Pull Request #{pr_num}")
            self.pr_banner.setVisible(True)
            self.checks_view.set_pr(pr_num, url=url, state="OPEN")
            StateManager.update(
                rufin_tracked_pr={
                    "number": pr_num,
                    "title": self.pr_title_label.text().removeprefix("PR: ") or self.title_input.text().strip(),
                    "url": url,
                    "state": "OPEN",
                }
            )

    def _open_detected_pr(self):
        if self.detected_pr_url:
            QDesktopServices.openUrl(QUrl(self.detected_pr_url))

    def refresh(self):
        self.diff_viewer.repo_path = self.git_mgr.repo_path
        self.terminal.repo_path = self.git_mgr.repo_path
        self.checks_view.repo_path = self.git_mgr.repo_path

        items = self.git_mgr.get_status_files()
        self.file_list.set_files(items)
        curr_branch = self.branch_input.text().strip() or self.git_mgr.get_current_branch()
        context = (self.git_mgr.repo_path, curr_branch)
        if context != self._form_context and not self.body_input.document().isModified():
            self.body_input.clear()
            self.body_input.document().setModified(False)
        self._form_context = context
        draft = self.git_mgr.get_pr_draft(curr_branch)
        self.pr_title_label.setText(f"PR: {draft['title']}" if draft.get("title") else "")
        self.pr_title_label.setVisible(bool(draft.get("title")))
        if "body" in draft and not self.body_input.document().isModified():
            self.body_input.setPlainText(draft["body"])
            self.body_input.document().setModified(False)
        self._detect_pr(curr_branch)

        selected_paths = self.file_list.get_selected_paths()
        if len(selected_paths) == 1:
            self._show_single_file_diff(selected_paths[0])
        elif items:
            self._show_multi_file_diff([i.path for i in items])
        else:
            self.diff_viewer.set_diff("", "Clean working tree")

    def _detect_pr(self, curr_branch):
        if curr_branch and curr_branch != (self.base_input.text().strip() or self.config.default_base_branch):
            self.branch_input.setPlaceholderText(f"Current: {curr_branch}")
            # Detect open PR for topic branch
            if not self._detect_worker or not self._detect_worker.isRunning():
                self._detect_worker = BranchPrDetectWorker(self.git_mgr.repo_path, curr_branch, self)
                context = self._form_context
                self._detect_worker.context = context
                self._detect_worker.pr_found.connect(lambda data, context=context: self._on_open_pr_detected(data, context))
                self._detect_worker.finished.connect(self._release_detect_worker)
                self._detect_worker.start()
        else:
            self.branch_input.setPlaceholderText("Derived from title (on main)")
            # Saved selection is a hint; discover current work on every refresh.
            if not self._latest_worker or not self._latest_worker.isRunning():
                self._latest_worker = LatestPrDetectWorker(self.git_mgr.repo_path, self)
                context = self._form_context
                self._latest_worker.context = context
                self._latest_worker.pr_found.connect(lambda data, context=context: self._on_latest_pr_detected(data, context))
                self._latest_worker.finished.connect(self._release_detect_worker)
                self._latest_worker.start()

    def _on_latest_pr_detected(self, data, context):
        if context == self._form_context:
            self._on_open_pr_detected(data)

    def _release_detect_worker(self):
        worker = self.sender()
        if self._detect_worker is worker:
            self._detect_worker = None
        if self._latest_worker is worker:
            self._latest_worker = None
        worker.deleteLater()
        if worker.context != self._form_context:
            self._detect_pr(self._form_context[1])

    def _on_open_pr_detected(self, pr_data: dict, context=None):
        if context is not None and context != self._form_context:
            return
        if not pr_data:
            self.detected_pr_number = ""
            self.detected_pr_url = ""
            self.pr_banner.hide()
            self.checks_view.clear_pr()
            StateManager.update(rufin_tracked_pr=None)
            return
        pr_num = str(pr_data.get("number", ""))
        title = pr_data.get("title", "")
        url = pr_data.get("url", "")
        state = pr_data.get("state", "")
        if context is not None:
            self.pr_title_label.setText(f"PR: {title}")
            self.pr_title_label.setVisible(True)
            draft = self.git_mgr.get_pr_draft(context[1])
            if "body" not in draft and not self.body_input.document().isModified():
                self.body_input.setPlainText(pr_data.get("body", ""))
                self.body_input.document().setModified(False)
        if pr_num:
            self.detected_pr_number = pr_num
            self.detected_pr_url = url
            state_str = f" ({state.capitalize()})" if state else ""
            self.pr_banner_lbl.setText(f"Pull Request #{pr_num}{state_str}: {title}")
            self.pr_banner.setVisible(True)
            self.checks_view.set_pr(pr_num, title=title, url=url, state=state)
            StateManager.update(
                rufin_tracked_pr={
                    "number": pr_num,
                    "title": title,
                    "url": url,
                    "state": state,
                }
            )

    def _show_single_file_diff(self, path: str):
        diff_text = self.git_mgr.get_diff([path])
        item = next((i for i in self.file_list.items if i.path == path), None)
        adds = item.additions if item else 0
        dels = item.deletions if item else 0
        self.diff_viewer.set_diff(diff_text, path, adds, dels, rel_path=path)

    def _show_multi_file_diff(self, paths: List[str]):
        selected_items = [i for i in self.file_list.items if i.path in paths]
        adds = sum(i.additions for i in selected_items)
        dels = sum(i.deletions for i in selected_items)
        if not paths or len(paths) == len(self.file_list.items):
            diff_text = self.git_mgr.get_diff()
            self.diff_viewer.set_diff(diff_text, f"All Changes ({len(self.file_list.items)} files)", adds, dels, rel_path=None)
        else:
            diff_text = self.git_mgr.get_diff(paths)
            self.diff_viewer.set_diff(diff_text, f"{len(paths)} files selected", adds, dels, rel_path=None)

    def run_publish(self):
        if self.runner.is_running:
            QMessageBox.warning(self, "Running", "A script is already running.")
            return

        script_path = self.config.resolve_publish_script(self.git_mgr.repo_path)
        if not os.path.exists(script_path):
            QMessageBox.critical(
                self,
                "Script Not Found",
                f"Cannot find publish script at:\n{script_path}\n\nYou can configure the script location in Settings.",
            )
            return

        base_branch = self.base_input.text().strip() or self.config.default_base_branch
        branch = self.branch_input.text().strip()
        body = self.body_input.toPlainText()
        phase = self.phase_input.text().strip()
        title = self.title_input.text().strip()
        skip_pre_push = self.skip_pre_push_cb.isChecked()

        selected_paths = self.file_list.get_selected_paths()
        all_paths = [i.path for i in self.file_list.items]
        partial_files = bool(selected_paths and len(selected_paths) < len(all_paths))
        if all_paths and not title:
            QMessageBox.warning(self, "Commit Subject", "Enter a subject for the changes being committed.")
            return

        args = []
        if base_branch:
            args.extend(["--base", base_branch])
        if branch:
            args.extend(["--branch", branch])

        env_vars = {}
        if self.body_input.document().isModified():
            args.extend(["--body", body])
            env_vars["RUFIN_ALLOW_PR_BODY"] = "1"

        if skip_pre_push:
            env_vars["RUFIN_SKIP_PRE_PUSH_HOOK"] = "1"

        if phase:
            args.extend(["--phase", phase])

        if not self.auto_merge_cb.isChecked():
            args.append("--no-auto-merge")

        if self.wait_cb.isChecked():
            args.append("--wait")

        i18n_choice = self.i18n_combo.currentData()
        if i18n_choice == "include":
            args.append("--include-i18n-catalogs")
        elif i18n_choice == "sync":
            args.append("--sync-i18n-catalogs")

        if title and (all_paths or not self.pr_title_label.text()):
            args.append(title)

        if partial_files:
            args.extend(selected_paths)

        details = [
            ("Branch Target", f"{branch or '(derived)'} → {base_branch}"),
            ("PR Title", self.pr_title_label.text().removeprefix("PR: ") or title or "(saved draft or existing PR)"),
            ("Commit", title if all_paths else "Retry existing commits"),
            ("Files Included", f"{len(selected_paths)} of {len(all_paths)} files" if partial_files else f"All {len(all_paths)} files"),
            ("Squash Merge", "Enabled" if self.auto_merge_cb.isChecked() else "Disabled"),
            ("Wait CI Checks", "Yes" if self.wait_cb.isChecked() else "No"),
            ("Skip Pre-push", "Yes" if skip_pre_push else "No"),
        ]
        if i18n_choice != "none":
            details.append(("I18n Catalogs", self.i18n_combo.currentText()))

        popup = ConfirmPreviewDialog(
            title="Publish Pull Request",
            details=details,
            preview_text=body if body else None,
            preview_text_label="PR Description Preview:",
            action_button_text="Publish PR",
            anchor_widget=self.file_list.btn_publish,
            parent=self,
        )
        if not popup.exec():
            return

        self.detected_pr_url = ""
        self.detected_pr_number = ""
        self.pr_banner.setVisible(False)
        self.bottom_tabs.setCurrentWidget(self.terminal)
        self.terminal.clear_terminal()
        self.terminal.set_status("Publishing PR", "running")
        self._submitted_title = title if all_paths else ""
        self._submitted_body = body
        self.runner.start(script_path, args, cwd=self.git_mgr.repo_path, env_vars=env_vars)

    def _on_process_finished(self, exit_code: int):
        if exit_code != 0 or self.runner.record.get("stopped"):
            self.bottom_tabs.show_activity("failed", self.terminal)
        else:
            self.checks_view.refresh_checks()
        status = "success" if exit_code == 0 else "failed"
        self.terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        draft = self.git_mgr.get_pr_draft(self.branch_input.text().strip() or self.git_mgr.get_current_branch())
        body = self.body_input.toPlainText()
        if (exit_code == 0 and body == self._submitted_body) or draft.get("body") == body:
            self.body_input.document().setModified(False)
        if self._submitted_title == self.title_input.text().strip() and (exit_code == 0 or self.git_mgr.get_commit_subject() == self._submitted_title):
            self.title_input.clear()
        if exit_code == 0 and body == self._submitted_body:
            self.body_input.clear()
            self.body_input.document().setModified(False)
        if self.window().isVisible():
            self.refresh()
