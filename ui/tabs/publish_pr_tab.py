import os
import re
import json
import subprocess
import sys
from pathlib import Path
from typing import List, Optional
from PyQt6.QtCore import Qt, QUrl, QThread, pyqtSignal, QTimer
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
    QSizePolicy,
)
from core.config import AppConfig
from core.git_manager import GitManager
from core.runner import ProcessRunner
from core.publish_checks import CHECK_INPUT, has_pending_edits, pending_inputs
from core.notifications import send_desktop_notification
from ui.components.commit_draft import CommitDraft
from ui.components.activity_tabs import ActivityTabs
from core.state import StateManager
from ui.components.file_list import FileListWidget
from ui.components.diff_viewer import DiffViewer
from ui.components.terminal_view import TerminalWidget
from ui.components.pr_checks_view import PrChecksWidget
from ui.dialogs.confirm_preview_dialog import ConfirmPreviewDialog
from ui.tabs.release_tab import ActivePrsFetchWorker, PrPollWorker


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
        self._branch_comparison = None
        self._working_tree_items = []
        self._submitted_title = ""
        self._submitted_body = ""
        self._output_pending = ""
        self._published_pr = ""
        self._is_waiting_for_pr = False
        self._waiting_pr_num = ""
        self._waiting_script_path = ""
        self._waiting_args = []
        self._waiting_env_vars = {}
        self._waiting_pending = {}
        self._waiting_check_inputs = None
        self._waiting_checks_running = False
        self._waiting_pr_merged = False
        self._pr_poll_timer = QTimer(self)
        self._pr_poll_timer.setInterval(10000)
        self._pr_poll_timer.timeout.connect(self._poll_waiting_pr)
        self._pr_poll_worker = None
        self._active_prs_worker = None

        self._init_ui()
        self._wire_signals()
        self.terminal.bind_runner(self.runner, self.git_mgr.repo_path, "publish", self.config.resolve_publish_script(self.git_mgr.repo_path))
        self._restore_pending_publish()
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
        self.branch_diff_error = QLabel("Branch diff unavailable")
        self.branch_diff_error.hide()
        self.file_list.layout().insertWidget(2, self.branch_diff_error)
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
        self.commit_draft = CommitDraft(self.title_input, self.body_input, self._draft_diff)
        h_title_phase.insertWidget(2, self.commit_draft)

        form_layout.addWidget(pr_grp)

        # Options group
        flags_grp = QGroupBox("Options")
        flags_layout = QVBoxLayout(flags_grp)
        flags_layout.setContentsMargins(8, 4, 8, 4)
        flags_layout.setSpacing(3)

        self.auto_merge_cb = QCheckBox("Squash auto-merge")
        self.auto_merge_cb.setChecked(self.config.default_auto_merge)
        flags_layout.addWidget(self.auto_merge_cb)

        self.wait_cb = QCheckBox("Wait CI checks (--wait)")
        self.wait_cb.setChecked(self.config.default_wait_ci)
        flags_layout.addWidget(self.wait_cb)

        self.skip_pre_push_cb = QCheckBox("Skip pre-push")
        self.skip_pre_push_cb.setChecked(False)
        self.skip_pre_push_cb.setToolTip("Skip local pre-push hook verification (RUFIN_SKIP_PRE_PUSH_HOOK=1)")
        flags_layout.addWidget(self.skip_pre_push_cb)

        # Thin row: Publish after PR merges
        h_pr_row = QHBoxLayout()
        h_pr_row.setSpacing(6)

        self.wait_pr_cb = QCheckBox("Publish after PR merges:")
        self.wait_pr_cb.setStyleSheet("font-size: 11px;")
        self.wait_pr_cb.toggled.connect(self._on_wait_pr_toggled)
        h_pr_row.addWidget(self.wait_pr_cb)

        self.pr_combo = QComboBox()
        self.pr_combo.setEditable(True)
        self.pr_combo.setInsertPolicy(QComboBox.InsertPolicy.NoInsert)
        self.pr_combo.lineEdit().setPlaceholderText("PR #")
        self.pr_combo.setMinimumWidth(85)
        self.pr_combo.setMaximumWidth(150)
        self.pr_combo.currentTextChanged.connect(self._on_pr_text_changed)
        h_pr_row.addWidget(self.pr_combo)

        self.wait_pr_status_lbl = QLabel("")
        self.wait_pr_status_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
        h_pr_row.addWidget(self.wait_pr_status_lbl)

        h_pr_row.addStretch()
        flags_layout.addLayout(h_pr_row)

        h_i18n = QHBoxLayout()
        h_i18n.setSpacing(6)
        lbl_i18n = QLabel("Catalogs:")
        lbl_i18n.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.i18n_combo = QComboBox()
        self.i18n_combo.setSizeAdjustPolicy(QComboBox.SizeAdjustPolicy.AdjustToMinimumContentsLengthWithIcon)
        self.i18n_combo.setMinimumContentsLength(16)
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
        self.left_splitter.setMinimumWidth(320)

        self.main_splitter.addWidget(self.left_splitter)

        # ----------------- Right Panel -----------------
        right_widget = QWidget()
        right_widget.setMinimumWidth(320)
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
        self.pr_banner_lbl.setSizePolicy(QSizePolicy.Policy.Ignored, QSizePolicy.Policy.Fixed)
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
        right_splitter.setCollapsible(0, False)

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
        self.main_splitter.setCollapsible(1, False)
        self.main_splitter.setSizes([380, 620])

    def _wire_signals(self):
        self.runner.started.connect(self._on_process_started)
        self.runner.restored.connect(self._on_process_restored)
        self.runner.error_occurred.connect(lambda message: self.bottom_tabs.show_activity("failed", self.terminal))
        self.checks_view.checks_updated.connect(self._focus_remote_checks)
        self.runner.started.connect(self._update_publish_button)
        self.runner.restored.connect(lambda record: self._update_publish_button())
        self.runner.finished.connect(lambda code: self._update_publish_button())
        self.file_list.file_selected.connect(self._show_single_file_diff)
        self.file_list.view_diff_requested.connect(self._show_multi_file_diff)
        self.file_list.publish_requested.connect(self._publish_button_clicked)

        self.runner.output_received.connect(self._on_output_received)
        self.runner.started.connect(lambda: self.terminal.set_status("Running", "running"))
        self.runner.finished.connect(self._on_process_finished)
        self.runner.error_occurred.connect(lambda msg: self.terminal.set_status("Error", "failed"))
        self.terminal.stop_requested.connect(self._stop_publish)
        self.branch_input.editingFinished.connect(self.refresh)
        self.branch_input.textChanged.connect(self._update_publish_button)
        self.base_input.textChanged.connect(self._update_publish_button)
        self.base_input.editingFinished.connect(self.refresh_working_tree)
        self.title_input.textChanged.connect(self._update_publish_button)
        self.body_input.textChanged.connect(self._update_publish_button)
        self.phase_input.textChanged.connect(self._update_publish_button)
        self.auto_merge_cb.toggled.connect(self._update_publish_button)
        self.wait_cb.toggled.connect(self._update_publish_button)
        self.skip_pre_push_cb.toggled.connect(self._update_publish_button)
        self.i18n_combo.currentIndexChanged.connect(self._update_publish_button)
        self.wait_pr_cb.toggled.connect(self._update_publish_button)

    def _restore_saved_pr(self):
        saved = StateManager.get("rufin_tracked_pr")
        if saved and isinstance(saved, dict) and saved.get("number"):
            self.detected_pr_number = str(saved["number"])
            self.detected_pr_url = saved.get("url", "")
            title = saved.get("title", "")
            state = saved.get("state", "")
            state_str = f" ({state.capitalize()})" if state else ""
            self.pr_banner_lbl.setText(f"Pull Request #{self.detected_pr_number}{state_str}: {title}")
            self.pr_banner_lbl.setToolTip(self.pr_banner_lbl.text())
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
        self.file_list.btn_publish.setText("Publishing…")
        pending = StateManager.get("rufin_publish_pending") or {}
        if pending.get("repo_path") == self.git_mgr.repo_path and self.runner.path:
            pending["job_id"] = self.runner.path.parent.name
            StateManager.update(rufin_publish_pending=pending)

    def _on_process_restored(self, record):
        pending = StateManager.get("rufin_publish_pending") or {}
        job_id = self.runner.path.parent.name if self.runner.path else None
        if pending.get("repo_path") == self.git_mgr.repo_path and pending.get("job_id") == job_id:
            if not record["running"] and record.get("exit_code") == 0 and not pending.get("checks_only"):
                StateManager.update(rufin_publish_pending=None)
                self.file_list.btn_publish.setText("Publish PR")
            else:
                self.file_list.btn_publish.setText("Resume PR" if not record["running"] else "Publish PR")
        if record["running"]:
            self.bottom_tabs.show_activity("local", self.terminal)
        elif record.get("exit_code") != 0 or record.get("stopped"):
            self.bottom_tabs.show_activity("failed", self.terminal)
        self._update_publish_button()

    def _restore_pending_publish(self):
        pending = StateManager.get("rufin_publish_pending")
        if not pending or pending.get("repo_path") != self.git_mgr.repo_path:
            return
        self._form_context = (self.git_mgr.repo_path, pending.get("branch", ""))
        self.branch_input.setText(pending.get("branch", ""))
        self.base_input.setText(pending.get("base", self.base_input.text()))
        self.title_input.setText(pending.get("title", ""))
        self.body_input.setPlainText(pending.get("body", ""))
        self.body_input.document().setModified(pending.get("body_modified", False))
        self.phase_input.setText(pending.get("phase", ""))
        self.auto_merge_cb.setChecked(pending.get("auto_merge", self.auto_merge_cb.isChecked()))
        self.wait_cb.setChecked(pending.get("wait", self.wait_cb.isChecked()))
        self.skip_pre_push_cb.setChecked(pending.get("skip_pre_push", False))
        index = self.i18n_combo.findData(pending.get("i18n", "none"))
        if index >= 0:
            self.i18n_combo.setCurrentIndex(index)

    def _publish_button_clicked(self):
        if self._is_waiting_for_pr:
            self._cancel_pr_wait()
            return
        if self.wait_pr_cb.isChecked():
            self.run_publish()
            return
        pending = StateManager.get("rufin_publish_pending") or {}
        if pending.get("repo_path") == self.git_mgr.repo_path and pending.get("job_id") == (self.runner.path.parent.name if self.runner.path else None):
            if self._pending_form_matches(pending):
                self.resume_publish(pending)
            else:
                self.run_publish()
        else:
            self.run_publish()

    def _get_selected_pr_number(self) -> Optional[str]:
        text = self.pr_combo.currentText().strip()
        if not text:
            return None
        idx = self.pr_combo.currentIndex()
        if idx >= 0 and self.pr_combo.itemText(idx) == text:
            data = self.pr_combo.itemData(idx)
            if data:
                return str(data)
        m = re.search(r"/pull/(\d+)", text)
        if m:
            return m.group(1)
        m = re.search(r"(?:^|[#\s])(\d+)\b", text)
        if m:
            return m.group(1)
        return None

    def _on_wait_pr_toggled(self, checked: bool):
        self._update_publish_button()

    def _on_pr_text_changed(self, text: str):
        pr_num = self._get_selected_pr_number()
        if pr_num and not self.wait_pr_cb.isChecked():
            self.wait_pr_cb.setChecked(True)
        elif not text.strip() and self.wait_pr_cb.isChecked():
            self.wait_pr_cb.setChecked(False)
        self._update_publish_button()

    def _fetch_active_prs(self):
        if self._active_prs_worker and self._active_prs_worker.isRunning():
            return
        self._active_prs_worker = ActivePrsFetchWorker(self.git_mgr.repo_path, self)
        self._active_prs_worker.prs_fetched.connect(self._on_active_prs_fetched)
        self._active_prs_worker.start()

    def _on_active_prs_fetched(self, prs: list):
        current_num = self._get_selected_pr_number()
        current_text = self.pr_combo.currentText().strip()

        self.pr_combo.blockSignals(True)
        self.pr_combo.clear()

        selected_index = -1
        for i, pr in enumerate(prs):
            num = str(pr.get("number", ""))
            title = pr.get("title", "")
            display = f"#{num} - {title}" if title else f"#{num}"
            self.pr_combo.addItem(display, userData=num)
            if current_num and current_num == num:
                selected_index = i

        if selected_index >= 0:
            self.pr_combo.setCurrentIndex(selected_index)
        elif current_text:
            self.pr_combo.setEditText(current_text)
        else:
            self.pr_combo.setEditText("")
            self.pr_combo.setCurrentIndex(-1)

        self.pr_combo.blockSignals(False)
        self._update_publish_button()

    def _update_publish_button(self, *_args):
        if self._is_waiting_for_pr:
            self.file_list.btn_publish.setText(f"Cancel (PR #{self._waiting_pr_num})")
            self.file_list.btn_publish.setEnabled(True)
            return

        pr_num = self._get_selected_pr_number()
        waiting = self.wait_pr_cb.isChecked() and bool(pr_num)
        if waiting:
            self.file_list.btn_publish.setText(f"Publish after PR #{pr_num} merges")
            self.file_list.btn_publish.setEnabled(True)
            return

        if self.runner.is_running:
            self.file_list.btn_publish.setText("Publishing…")
            self.file_list.btn_publish.setEnabled(False)
            return

        self.file_list.btn_publish.setEnabled(True)
        pending = StateManager.get("rufin_publish_pending") or {}
        is_saved_retry = (
            pending.get("repo_path") == self.git_mgr.repo_path
            and pending.get("job_id") == (self.runner.path.parent.name if self.runner.path else None)
            and self._pending_form_matches(pending)
        )
        self.file_list.btn_publish.setText("Resume PR" if is_saved_retry else "Publish PR")

    def _pending_form_matches(self, pending):
        return (
            self.branch_input.text().strip() == pending.get("branch", "")
            and self.base_input.text().strip() == pending.get("base", "")
            and self.title_input.text().strip() == pending.get("title", "")
            and self.body_input.toPlainText() == pending.get("body", "")
            and self.body_input.document().isModified() == pending.get("body_modified", False)
            and self.phase_input.text().strip() == pending.get("phase", "")
            and self.auto_merge_cb.isChecked() == pending.get("auto_merge")
            and self.wait_cb.isChecked() == pending.get("wait")
            and self.skip_pre_push_cb.isChecked() == pending.get("skip_pre_push")
            and self.i18n_combo.currentData() == pending.get("i18n")
        )

    def _start_publish_job(self, script_path, args, env_vars, pending, checks_only=False):
        self.detected_pr_url = ""
        self.detected_pr_number = ""
        self.pr_banner.setVisible(False)
        self.bottom_tabs.setCurrentWidget(self.terminal)
        self.terminal.clear_terminal()
        self.terminal.set_status("Publishing PR", "running")
        self._submitted_title = pending.get("title", "")
        self._submitted_body = pending.get("body", "")
        pending.pop("job_id", None)
        pending["checks_only"] = checks_only
        StateManager.update(rufin_publish_pending=pending)
        previous_path = self.runner.path
        self.runner.start(script_path, args, cwd=self.git_mgr.repo_path, env_vars=env_vars)
        if self.runner.path and self.runner.path != previous_path:
            pending["job_id"] = self.runner.path.parent.name
            StateManager.update(rufin_publish_pending=pending)
        elif self.runner.path == previous_path:
            self.file_list.btn_publish.setText("Publish PR")

    def resume_publish(self, pending):
        if self.runner.is_running:
            return
        script_path = self.config.resolve_publish_script(self.git_mgr.repo_path)
        if not os.path.exists(script_path):
            QMessageBox.critical(self, "Script Not Found", f"Cannot find publish script at:\n{script_path}")
            return
        branch_ref = f"refs/heads/{pending['branch']}"
        head = subprocess.run(["git", "rev-parse", "--verify", branch_ref], cwd=self.git_mgr.repo_path,
                              capture_output=True, text=True, check=False).stdout.strip()
        committed = False
        if head and pending.get("title") and head != pending.get("head_before"):
            base = pending.get("head_before", "")
            if subprocess.run(["git", "merge-base", "--is-ancestor", base, head],
                              cwd=self.git_mgr.repo_path, check=False).returncode == 0:
                subjects = subprocess.run(["git", "log", "--format=%s", f"{base}..{head}"],
                                          cwd=self.git_mgr.repo_path, capture_output=True,
                                          text=True, check=False).stdout.splitlines()
                committed = pending["title"] in subjects
            else:
                subject = subprocess.run(["git", "log", "-1", "--format=%s", branch_ref],
                                         cwd=self.git_mgr.repo_path, capture_output=True,
                                         text=True, check=False).stdout.strip()
                committed = subject == pending["title"]
        args = pending["retry_args"] if committed else pending["args"]
        popup = ConfirmPreviewDialog(
            title="Resume Pull Request",
            details=[
                ("Branch", pending.get("branch") or self.git_mgr.get_current_branch()),
                ("Commit", "Use the commit already on this branch" if args == pending["retry_args"] else pending.get("title") or "Saved changes"),
                ("Action", "Continue publishing this branch and run its checks again"),
            ],
            preview_text=pending.get("body") if pending.get("body_modified") else None,
            preview_text_label="PR Description Preview:",
            action_button_text="Resume PR",
            anchor_widget=self.file_list.btn_publish,
            parent=self,
        )
        if not popup.exec():
            return
        self._start_publish_job(script_path, args, pending.get("env", {}), pending)

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
        if self._waiting_checks_running:
            for line in text.splitlines():
                if line.startswith(CHECK_INPUT):
                    self._waiting_check_inputs = json.loads(line.removeprefix(CHECK_INPUT))
        for branch in re.findall(r"(?m)^Publishing branch: (.+)$", text):
            pending = StateManager.get("rufin_publish_pending") or {}
            job_id = self.runner.path.parent.name if self.runner.path else None
            if pending.get("repo_path") != self.git_mgr.repo_path or pending.get("job_id") != job_id:
                continue
            previous_branch = pending["branch"]
            pending["branch"] = branch
            for key in ("args", "retry_args"):
                args = pending[key]
                if "--branch" in args:
                    args[args.index("--branch") + 1] = branch
                else:
                    args[:0] = ["--branch", branch]
            StateManager.update(rufin_publish_pending=pending)
            if self.branch_input.text().strip() == previous_branch:
                self.branch_input.setText(branch)
        m = re.search(r"https://github\.com/[^/\s]+/[^/\s]+/pull/(\d+)", text)
        if m:
            url = m.group(0)
            pr_num = m.group(1)
            if re.search(rf"(?m)^(?:Published PR #\d+: )?{re.escape(url)}\s*$", text):
                self._published_pr = pr_num
            self.detected_pr_url = url
            self.detected_pr_number = pr_num
            self.pr_banner_lbl.setText(f"Pull Request #{pr_num}")
            self.pr_banner_lbl.setToolTip(self.pr_banner_lbl.text())
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
        self.refresh_working_tree()
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
        self._update_publish_button()
        self._fetch_active_prs()

    def refresh_working_tree(self):
        self.diff_viewer.repo_path = self.git_mgr.repo_path
        self.terminal.repo_path = self.git_mgr.repo_path
        self.checks_view.repo_path = self.git_mgr.repo_path

        self._working_tree_items = self.git_mgr.get_status_files()
        self._branch_comparison = None
        try:
            base = self.base_input.text().strip() or self.config.default_base_branch
            remote_base = f"origin/{base}"
            base_ref = remote_base if remote_base in self.git_mgr.get_comparison_refs() else base
            self._branch_comparison = self.git_mgr.branch_comparison(base_ref, True)
            items = self.git_mgr.get_comparison_files(self._branch_comparison)
            branch_paths = {item.path for item in items}
            items.extend(item for item in self._working_tree_items if item.path not in branch_paths)
            self.branch_diff_error.hide()
        except RuntimeError as error:
            self._branch_comparison = None
            items = self._working_tree_items
            self.branch_diff_error.setToolTip(str(error))
            self.branch_diff_error.show()
        self.file_list.set_files(items, selectable_paths=[item.path for item in self._working_tree_items])
        if self._branch_comparison is None:
            self.file_list.title_lbl.setText(f"Uncommitted ({len(items)})")

        preview_path = self.diff_viewer.current_rel_path
        if any(item.path == preview_path for item in items):
            self._show_single_file_diff(preview_path)
        elif items:
            self._show_multi_file_diff([item.path for item in items])
        else:
            self.diff_viewer.set_diff("", "No branch changes" if self._branch_comparison else "Clean working tree")

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
            self.pr_banner_lbl.setToolTip(self.pr_banner_lbl.text())
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

    def _preview_diff(self, paths: List[str]):
        if self._branch_comparison is None:
            return self.git_mgr.get_diff(paths)
        items = [item for item in self.file_list.items if item.path in paths]
        try:
            return self.git_mgr.get_comparison_diff(self._branch_comparison, items) or "No net branch changes"
        except RuntimeError as error:
            self.branch_diff_error.setToolTip(str(error))
            self.branch_diff_error.show()
            return str(error)

    def _show_single_file_diff(self, path: str):
        item = next((i for i in self.file_list.items if i.path == path), None)
        diff_text = self._preview_diff([path])
        adds = item.additions if item else 0
        dels = item.deletions if item else 0
        self.diff_viewer.set_diff(diff_text, path, adds, dels, rel_path=path)

    def _draft_diff(self):
        paths = self.file_list.get_selected_paths()
        return self.git_mgr.get_diff(paths) if paths else ""

    def _show_multi_file_diff(self, paths: List[str]):
        if self._branch_comparison and set(paths) == self.file_list.selectable_paths:
            paths = [item.path for item in self.file_list.items]
        selected_items = [i for i in self.file_list.items if i.path in paths]
        adds = sum(i.additions for i in selected_items)
        dels = sum(i.deletions for i in selected_items)
        if self._branch_comparison:
            diff_text = self._preview_diff(paths)
            self.diff_viewer.set_diff(diff_text, f"Branch Changes ({len(selected_items)} files)", adds, dels)
            return
        if len(paths) == len(self.file_list.items) and paths:
            diff_text = self.git_mgr.get_diff()
            self.diff_viewer.set_diff(diff_text, f"All Changes ({len(self.file_list.items)} files)", adds, dels, rel_path=None)
        elif not paths:
            self.diff_viewer.set_diff("", "No changes selected")
        else:
            diff_text = self.git_mgr.get_diff(paths)
            self.diff_viewer.set_diff(diff_text, f"{len(paths)} files selected", adds, dels, rel_path=None)

    def run_publish(self):
        if self._is_waiting_for_pr:
            self._cancel_pr_wait()
            return

        pr_num = None
        if self.wait_pr_cb.isChecked():
            pr_num = self._get_selected_pr_number()
            if not pr_num:
                QMessageBox.warning(
                    self,
                    "Missing PR Number",
                    "Please enter or select a PR number to wait for, or uncheck 'Publish after PR merges'.",
                )
                return

        if self.runner.is_running and not pr_num:
            reply = QMessageBox.question(
                self,
                "Publish in Progress",
                "A publish script is currently running. Do you want to stop it and start publishing?",
                QMessageBox.StandardButton.Yes | QMessageBox.StandardButton.No,
                QMessageBox.StandardButton.No,
            )
            if reply != QMessageBox.StandardButton.Yes:
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
        all_paths = [i.path for i in self._working_tree_items]
        partial_files = bool(selected_paths and len(selected_paths) < len(all_paths))
        if all_paths and not title:
            QMessageBox.warning(self, "Commit Subject", "Enter a subject for the changes being committed.")
            return

        if not branch:
            resolved = subprocess.run(
                [script_path, "--base", base_branch, "--print-branch", title],
                cwd=self.git_mgr.repo_path, capture_output=True, text=True, check=False,
            )
            if resolved.returncode != 0:
                QMessageBox.critical(self, "Publish Branch", resolved.stderr.strip())
                return
            branch = resolved.stdout.strip()

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

        retry_args = list(args)

        if title and (all_paths or not self.pr_title_label.text()):
            args.append(title)

        if partial_files:
            args.extend(selected_paths)

        details = [
            ("Branch Target", f"{branch or '(derived)'} → {base_branch}"),
            ("PR Title", self.pr_title_label.text().removeprefix("PR: ") or title or "(saved draft or existing PR)"),
            ("Commit", title if all_paths else "Retry existing commits"),
            ("Files Included", f"{len(selected_paths)} of {len(all_paths)} files" if partial_files else f"All {len(all_paths)} files"),
        ]
        if pr_num:
            details.append(("Wait for PR", f"#{pr_num} (auto-publish when merged)"))
        details.extend([
            ("Squash Merge", "Enabled" if self.auto_merge_cb.isChecked() else "Disabled"),
            ("Wait CI Checks", "Yes" if self.wait_cb.isChecked() else "No"),
            ("Skip Pre-push", "Yes" if skip_pre_push else "No"),
        ])
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

        self.branch_input.setText(branch)
        pending = {
            "repo_path": self.git_mgr.repo_path,
            "branch": branch,
            "base": base_branch,
            "title": title,
            "body": body,
            "body_modified": self.body_input.document().isModified(),
            "phase": phase,
            "auto_merge": self.auto_merge_cb.isChecked(),
            "wait": self.wait_cb.isChecked(),
            "skip_pre_push": skip_pre_push,
            "i18n": i18n_choice,
            "args": args,
            "retry_args": retry_args,
            "env": env_vars,
            "paths": selected_paths if partial_files else None,
            "head_before": subprocess.run(["git", "rev-parse", "HEAD"], cwd=self.git_mgr.repo_path,
                                           capture_output=True, text=True, check=False).stdout.strip(),
        }
        if pr_num:
            if self.runner.is_running:
                self.terminal.append_output("[*] Stopping running script to wait for PR...\n")
            self._stop_and_run(lambda: self._start_waiting_for_pr(pr_num, script_path, args, env_vars, pending))
        else:
            self._stop_and_run(lambda: self._start_publish_job(script_path, args, env_vars, pending))

    def _stop_and_run(self, callback):
        if not self.runner.is_running:
            callback()
            return

        def _on_finished(code):
            try:
                self.runner.finished.disconnect(_on_finished)
            except (TypeError, RuntimeError):
                pass
            callback()

        self.runner.finished.connect(_on_finished)
        self.runner.stop()

    def _start_waiting_for_pr(self, pr_num: str, script_path: str, args: list, env_vars: dict, pending: dict):
        self._is_waiting_for_pr = True
        self._waiting_pr_num = pr_num
        self._waiting_script_path = script_path
        self._waiting_args = args
        self._waiting_env_vars = env_vars
        self._waiting_pending = pending
        self._waiting_check_inputs = None
        self._waiting_checks_running = False
        self._waiting_pr_merged = False

        self.file_list.btn_publish.setEnabled(True)
        self._update_publish_button()
        self.wait_pr_status_lbl.setText(f"Waiting for #{pr_num}...")
        self.wait_pr_status_lbl.setStyleSheet("color: #e3b341; font-size: 11px;")

        self.bottom_tabs.setCurrentWidget(self.terminal)
        self.terminal.clear_terminal()
        self.terminal.set_status(f"Waiting for PR #{pr_num}", "running")
        self.terminal.append_output(f"[*] Publish scheduled: will run automatically once PR #{pr_num} merges.\n")
        self.terminal.append_output(f"[*] Polling GitHub PR #{pr_num}...\n")
        self.bottom_tabs.show_activity("local", self.terminal, force=True)

        self._pr_poll_timer.start(10000)
        if not pending["skip_pre_push"]:
            self._start_waiting_checks()
        self._poll_waiting_pr()

    def _start_waiting_checks(self):
        self._waiting_check_inputs = None
        self._waiting_checks_running = True
        command = str(Path(__file__).resolve().parents[2] / "core" / "publish_checks.py")
        self._start_publish_job(
            sys.executable, [command, self._waiting_script_path, json.dumps(self._waiting_pending)],
            self._waiting_env_vars, self._waiting_pending,
            checks_only=True,
        )
        if self.runner.is_running:
            self.terminal.set_status("Checking pending changes", "running")
        elif self._waiting_checks_running:
            self._waiting_checks_running = False
            self._fail_pr_wait("Pending checks could not start.")

    def _publish_when_ready(self, checks_passed=True):
        if not self._is_waiting_for_pr or self._waiting_checks_running or self.runner.is_running:
            return
        pending = self._waiting_pending
        args = self._waiting_args
        env_vars = dict(self._waiting_env_vars)
        if not pending["skip_pre_push"]:
            try:
                inputs = pending_inputs(self.git_mgr.repo_path, pending["branch"], pending["paths"])
                if inputs != self._waiting_check_inputs:
                    self.terminal.append_output("[*] Pending changes edited. Running checks again.\n")
                    self._start_waiting_checks()
                    return
                if not checks_passed:
                    self._fail_pr_wait("Pending checks failed. Publication stopped.")
                    return
                args = pending["args"] if has_pending_edits(self.git_mgr.repo_path, inputs) else pending["retry_args"]
            except (OSError, subprocess.CalledProcessError) as error:
                self._fail_pr_wait(f"Cannot read pending changes: {error}")
                return
            env_vars["RUFIN_SKIP_PRE_PUSH_HOOK"] = "1"
            env_vars["RUFIN_SKIP_CLIPPY_HOOK"] = "0"
            env_vars["RUFIN_PRECHECKED_HEAD"] = inputs[1]
        if not self._waiting_pr_merged:
            self.terminal.set_status(f"Waiting for PR #{self._waiting_pr_num}", "running")
            return
        self._pr_poll_timer.stop()
        self._is_waiting_for_pr = False
        self.terminal.append_output("[+] Starting publication after merge.\n")
        self._start_publish_job(self._waiting_script_path, args, env_vars, pending)

    def _fail_pr_wait(self, message):
        self._pr_poll_timer.stop()
        self._is_waiting_for_pr = False
        self.wait_pr_status_lbl.setText(message)
        self.terminal.append_output(f"[!] {message}\n")
        self.terminal.set_status(message, "failed")
        self.bottom_tabs.show_activity("failed", self.terminal)
        self._update_publish_button()

    def _poll_waiting_pr(self):
        if not self._is_waiting_for_pr:
            return
        self._publish_when_ready()
        if not self._is_waiting_for_pr or self._waiting_pr_merged:
            return
        if self._pr_poll_worker and self._pr_poll_worker.isRunning():
            return
        self._pr_poll_worker = PrPollWorker(self.git_mgr.repo_path, self._waiting_pr_num, self)
        self._pr_poll_worker.pr_status_fetched.connect(self._on_pr_status_fetched)
        self._pr_poll_worker.start()

    def _on_pr_status_fetched(self, data: dict):
        if not self._is_waiting_for_pr:
            return
        state = (data.get("state") or "").upper()
        pr_num = self._waiting_pr_num

        if state == "MERGED":
            self._waiting_pr_merged = True
            self.wait_pr_status_lbl.setText(f"PR #{pr_num} merged!")
            self.wait_pr_status_lbl.setStyleSheet("color: #3fb950; font-size: 11px;")
            self.terminal.append_output(f"\n[+] PR #{pr_num} has merged.\n")
            send_desktop_notification("PR Merged", f"PR #{pr_num} merged.")
            self._update_publish_button()
            self._publish_when_ready()
        elif state == "CLOSED":
            self._cancel_pr_wait()
            self._pr_poll_timer.stop()
            self._is_waiting_for_pr = False
            self.wait_pr_status_lbl.setText(f"PR #{pr_num} closed without merge")
            self.wait_pr_status_lbl.setStyleSheet("color: #f85149; font-size: 11px;")
            self.terminal.append_output(f"\n[!] PR #{pr_num} was closed without being merged. Publish aborted.\n")
            self.terminal.set_status(f"PR #{pr_num} Closed", "failed")
            self._update_publish_button()
            send_desktop_notification("Publish Aborted", f"PR #{pr_num} was closed without merging.")
        else:
            self.wait_pr_status_lbl.setText(f"PR #{pr_num} is OPEN (waiting)")
            self.wait_pr_status_lbl.setStyleSheet("color: #e3b341; font-size: 11px;")
            self.terminal.append_output(f"[*] PR #{pr_num} is {state}. Polling again in 10s...\n")

    def _cancel_pr_wait(self):
        self._pr_poll_timer.stop()
        self._is_waiting_for_pr = False
        if self._waiting_checks_running:
            self.runner.stop()
        self.wait_pr_status_lbl.setText("Wait cancelled")
        self.wait_pr_status_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
        if self._waiting_pr_num:
            self.terminal.append_output(f"\n[!] Cancelled waiting for PR #{self._waiting_pr_num}.\n")
        self.terminal.set_status("Cancelled", "normal")
        self._update_publish_button()

    def _stop_publish(self):
        if self._is_waiting_for_pr:
            self._cancel_pr_wait()
        else:
            self.runner.stop()

    def _on_process_finished(self, exit_code: int):
        pending = StateManager.get("rufin_publish_pending") or {}
        checks_only = (pending.get("checks_only") and self.runner.path
                       and pending.get("job_id") == self.runner.path.parent.name)
        if self._waiting_checks_running or checks_only:
            self._waiting_checks_running = False
            if self._is_waiting_for_pr:
                if self._waiting_check_inputs is None:
                    self._fail_pr_wait("Pending checks failed. Publication stopped.")
                else:
                    self._publish_when_ready(checks_passed=exit_code == 0 and not self.runner.record.get("stopped"))
            elif checks_only and not self._waiting_pr_num:
                self.terminal.set_status("Checks passed" if exit_code == 0 else "Checks failed",
                                         "success" if exit_code == 0 else "failed")
            return
        if self._is_waiting_for_pr:
            self._update_publish_button()
            return
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
        if exit_code == 0 and self._submitted_title == self.title_input.text().strip():
            self.title_input.clear()
        if exit_code == 0:
            StateManager.update(rufin_publish_pending=None)
            self.file_list.btn_publish.setText("Publish PR")
            if body == self._submitted_body:
                self.body_input.clear()
                self.body_input.document().setModified(False)
        self._update_publish_button()
        if self.window().isVisible():
            self.refresh()
