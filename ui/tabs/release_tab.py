import os
import re
import json
import subprocess
import threading
from typing import Optional
from PyQt6.QtCore import Qt, QTimer, QThread, pyqtSignal
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
    QButtonGroup,
    QGroupBox,
    QMessageBox,
)
from core.config import AppConfig
from core.git_manager import GitManager
from core.runner import ProcessRunner
from core.notifications import send_desktop_notification
from ui.components.activity_tabs import ActivityTabs
from ui.components.terminal_view import TerminalWidget
from ui.components.release_jobs_view import ReleaseJobsWidget
from ui.dialogs.confirm_preview_dialog import ConfirmPreviewDialog


def get_repo_owner_name(repo_path: str) -> Optional[str]:
    try:
        p = subprocess.run(
            ["git", "-C", repo_path, "config", "--get", "remote.origin.url"],
            capture_output=True,
            text=True,
            timeout=5,
        )
        m = re.search(r"github\.com[:/]([^/]+/[^/\s]+?)(?:\.git)?$", p.stdout.strip())
        if m:
            return m.group(1)
    except Exception:
        pass
    return None


class ActivePrsFetchWorker(QThread):
    prs_fetched = pyqtSignal(list)

    def __init__(self, repo_path: str, parent=None):
        super().__init__(parent)
        self.repo_path = repo_path

    def run(self):
        try:
            cmd = [
                "gh",
                "pr",
                "list",
                "--state",
                "open",
                "--limit",
                "30",
                "--json",
                "number,title",
            ]
            repo = get_repo_owner_name(self.repo_path)
            if repo:
                cmd.extend(["--repo", repo])
            res = subprocess.run(
                cmd,
                cwd=self.repo_path,
                capture_output=True,
                text=True,
                timeout=10,
            )
            if res.returncode == 0 and res.stdout.strip():
                self.prs_fetched.emit(json.loads(res.stdout))
            else:
                self.prs_fetched.emit([])
        except Exception:
            self.prs_fetched.emit([])


class PrPollWorker(QThread):
    pr_status_fetched = pyqtSignal(dict)

    def __init__(self, repo_path: str, pr_number: str, parent=None):
        super().__init__(parent)
        self.repo_path = repo_path
        self.pr_number = pr_number

    def run(self):
        try:
            cmd = ["gh", "pr", "view", str(self.pr_number), "--json", "number,title,state"]
            repo = get_repo_owner_name(self.repo_path)
            if repo:
                cmd.extend(["--repo", repo])
            res = subprocess.run(
                cmd,
                cwd=self.repo_path,
                capture_output=True,
                text=True,
                timeout=15,
            )
            if res.returncode == 0 and res.stdout.strip():
                self.pr_status_fetched.emit(json.loads(res.stdout))
        except Exception:
            pass


class ReleaseTab(QWidget):
    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        self.runner = ProcessRunner(self)
        self.current_mode = "new"
        self._is_waiting_for_pr = False
        self._waiting_pr_num = ""
        self._waiting_script_path = ""
        self._waiting_args = []
        self._waiting_version = ""
        self._pr_poll_timer = QTimer(self)
        self._pr_poll_timer.setInterval(12000)
        self._pr_poll_timer.timeout.connect(self._poll_waiting_pr)
        self._pr_poll_worker = None
        self._active_prs_worker = None
        self._init_ui()
        self._wire_signals()
        self.terminal.bind_runner(self.runner, self.git_mgr.repo_path, "release", self.config.resolve_release_script(self.git_mgr.repo_path))
        self.refresh()

    def _init_ui(self):
        root_layout = QHBoxLayout(self)
        root_layout.setContentsMargins(6, 6, 6, 6)
        root_layout.setSpacing(8)

        splitter = QSplitter(Qt.Orientation.Horizontal)
        root_layout.addWidget(splitter)

        # ----------------- Left Panel: Release Configuration -----------------
        left_widget = QWidget()
        left_layout = QVBoxLayout(left_widget)
        left_layout.setContentsMargins(0, 2, 4, 2)
        left_layout.setSpacing(8)

        # Mode Selection: Compact side-by-side selectable pills/tabs (auto-sized to text, not stretched)
        mode_grp = QGroupBox("Release Mode")
        mode_layout = QVBoxLayout(mode_grp)
        mode_layout.setContentsMargins(8, 8, 8, 8)
        mode_layout.setSpacing(6)

        h_mode_boxes = QHBoxLayout()
        h_mode_boxes.setSpacing(6)
        h_mode_boxes.setAlignment(Qt.AlignmentFlag.AlignLeft)

        self.mode_group = QButtonGroup(self)
        self.mode_group.setExclusive(True)

        mode_style = (
            "QPushButton { "
            "  background-color: #0b0e14; "
            "  border: 1px solid #30363d; "
            "  color: #8b949e; "
            "  border-radius: 5px; "
            "  padding: 4px 12px; "
            "  font-size: 11px; "
            "  font-weight: 500; "
            "} "
            "QPushButton:hover:!checked { "
            "  background-color: #161b22; "
            "  border-color: #484f58; "
            "  color: #e6edf3; "
            "} "
            "QPushButton:checked { "
            "  background-color: #16243b; "
            "  border: 1px solid #388bfd; "
            "  color: #79c0ff; "
            "  font-weight: 600; "
            "}"
        )

        self.btn_mode_new = QPushButton("New Release")
        self.btn_mode_new.setCheckable(True)
        self.btn_mode_new.setChecked(True)
        self.btn_mode_new.setFixedHeight(26)
        self.btn_mode_new.setStyleSheet(mode_style)
        self.mode_group.addButton(self.btn_mode_new, 0)
        h_mode_boxes.addWidget(self.btn_mode_new)

        self.btn_mode_replace = QPushButton("Replace")
        self.btn_mode_replace.setCheckable(True)
        self.btn_mode_replace.setFixedHeight(26)
        self.btn_mode_replace.setStyleSheet(mode_style)
        self.mode_group.addButton(self.btn_mode_replace, 1)
        h_mode_boxes.addWidget(self.btn_mode_replace)

        self.btn_mode_resume = QPushButton("Resume")
        self.btn_mode_resume.setCheckable(True)
        self.btn_mode_resume.setFixedHeight(26)
        self.btn_mode_resume.setStyleSheet(mode_style)
        self.mode_group.addButton(self.btn_mode_resume, 2)
        h_mode_boxes.addWidget(self.btn_mode_resume)

        h_mode_boxes.addStretch()
        mode_layout.addLayout(h_mode_boxes)

        self.mode_hint_lbl = QLabel("Syncs default branch, creates release prep PR, and publishes tag.")
        self.mode_hint_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
        mode_layout.addWidget(self.mode_hint_lbl)

        left_layout.addWidget(mode_grp, 0)

        # Version Group
        ver_grp = QGroupBox("Release Version")
        ver_layout = QVBoxLayout(ver_grp)
        ver_layout.setContentsMargins(8, 8, 8, 8)
        ver_layout.setSpacing(4)

        h_ver = QHBoxLayout()
        h_ver.setSpacing(6)
        self.version_input = QLineEdit()
        self.version_input.setPlaceholderText("e.g. v0.15.5")
        self.version_input.textChanged.connect(self._on_version_changed)
        h_ver.addWidget(self.version_input)

        self.btn_patch = QPushButton("+ Patch")
        self.btn_patch.setFixedHeight(26)
        self.btn_patch.clicked.connect(lambda: self._bump_version("patch"))
        h_ver.addWidget(self.btn_patch)

        self.btn_minor = QPushButton("+ Minor")
        self.btn_minor.setFixedHeight(26)
        self.btn_minor.clicked.connect(lambda: self._bump_version("minor"))
        h_ver.addWidget(self.btn_minor)

        ver_layout.addLayout(h_ver)

        h_tag_info = QHBoxLayout()
        self.latest_tag_lbl = QLabel("Latest tag: detecting...")
        self.latest_tag_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
        h_tag_info.addWidget(self.latest_tag_lbl)
        h_tag_info.addStretch()

        self.btn_fetch_notes = QPushButton("Fetch Tag Notes")
        self.btn_fetch_notes.setFixedHeight(22)
        self.btn_fetch_notes.setStyleSheet("font-size: 10px; padding: 1px 6px;")
        self.btn_fetch_notes.clicked.connect(self._fetch_notes_for_version)
        fetch_sp = self.btn_fetch_notes.sizePolicy()
        fetch_sp.setRetainSizeWhenHidden(True)
        self.btn_fetch_notes.setSizePolicy(fetch_sp)
        self.btn_fetch_notes.setVisible(False)
        h_tag_info.addWidget(self.btn_fetch_notes)

        ver_layout.addLayout(h_tag_info)

        # Thin row: Release after PR merges + Resume selected release
        h_pr_row = QHBoxLayout()
        h_pr_row.setSpacing(6)

        self.wait_pr_cb = QCheckBox("Release after PR merges:")
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

        self.btn_resume_selected = QPushButton("Resume selected release")
        self.btn_resume_selected.setToolTip("Use the version selected in Jobs and Progress")
        self.btn_resume_selected.setFixedHeight(24)
        self.btn_resume_selected.setStyleSheet("font-size: 11px; padding: 2px 8px;")
        self.btn_resume_selected.clicked.connect(self._resume_selected)
        h_pr_row.addWidget(self.btn_resume_selected)

        ver_layout.addLayout(h_pr_row)

        left_layout.addWidget(ver_grp, 0)

        # Summary / Release Notes Group (expands to fill space)
        notes_grp = QGroupBox("Release Summary / Notes")
        notes_layout = QVBoxLayout(notes_grp)
        notes_layout.setContentsMargins(8, 8, 8, 8)
        notes_layout.setSpacing(4)

        self.notes_status_lbl = QLabel("Write release description and changelog:")
        self.notes_status_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
        notes_layout.addWidget(self.notes_status_lbl)

        self.summary_input = QPlainTextEdit()
        self.summary_input.setPlaceholderText(
            "Write the release description / changelog here...\n\n"
            "This will be recorded in the signed git tag and draft GitHub Release notes."
        )
        self.summary_input.setMinimumHeight(240)
        notes_layout.addWidget(self.summary_input, 1)

        left_layout.addWidget(notes_grp, 1)

        # Options Group
        opts_grp = QGroupBox("Execution Options")
        opts_layout = QVBoxLayout(opts_grp)
        opts_layout.setContentsMargins(8, 8, 8, 8)
        opts_layout.setSpacing(3)

        self.push_cb = QCheckBox("Push tag, open PR, and stage assets (--push)")
        self.push_cb.setChecked(True)
        opts_layout.addWidget(self.push_cb)

        self.dry_run_cb = QCheckBox("Dry run tag generation (--dry-run)")
        self.dry_run_cb.setChecked(False)
        opts_layout.addWidget(self.dry_run_cb)

        self.skip_flathub_cb = QCheckBox("Skip Flathub PR (--skip-flathub)")
        self.skip_flathub_cb.setChecked(False)
        opts_layout.addWidget(self.skip_flathub_cb)

        self.skip_gh_release_cb = QCheckBox("Skip GitHub Release publication (--skip-github-release)")
        self.skip_gh_release_cb.setChecked(False)
        opts_layout.addWidget(self.skip_gh_release_cb)

        left_layout.addWidget(opts_grp, 0)

        # Start Button
        self.btn_release = QPushButton("Start Release")
        self.btn_release.setObjectName("primaryButton")
        self.btn_release.setFixedHeight(32)
        self.btn_release.clicked.connect(self.run_release)
        left_layout.addWidget(self.btn_release, 0)

        splitter.addWidget(left_widget)

        # ----------------- Right Panel: Jobs & Terminal Tabs -----------------
        right_widget = QWidget()
        right_layout = QVBoxLayout(right_widget)
        right_layout.setContentsMargins(0, 0, 0, 0)
        right_layout.setSpacing(0)

        self.output_tabs = ActivityTabs()
        self.jobs_view = ReleaseJobsWidget(repo_path=self.git_mgr.repo_path)
        self.terminal = TerminalWidget()

        self.output_tabs.addTab(self.jobs_view, "Jobs and Progress")
        self.output_tabs.addTab(self.terminal, "Terminal Log")
        right_layout.addWidget(self.output_tabs)

        splitter.addWidget(right_widget)
        splitter.setSizes([420, 640])

    def _wire_signals(self):
        self.runner.started.connect(self._on_process_started)
        self.runner.restored.connect(self._on_activity_restored)
        self.runner.error_occurred.connect(lambda message: self.output_tabs.show_activity("failed", self.terminal))
        self.jobs_view.workflow_started.connect(self._focus_workflow)
        self.jobs_view.local_work_started.connect(
            lambda: self.output_tabs.show_activity("local", self.terminal)
            if self.runner.is_running else None)
        self.jobs_view.remote_status_ready.connect(self._focus_remote_jobs)
        self.runner.started.connect(self._update_button_label)
        self.runner.restored.connect(self._restore_release)
        self.runner.restored.connect(lambda record: self._update_button_label())
        self.runner.finished.connect(lambda code: self._update_button_label())
        self.btn_mode_new.clicked.connect(lambda: self._set_mode("new"))
        self.btn_mode_replace.clicked.connect(lambda: self._set_mode("replace"))
        self.btn_mode_resume.clicked.connect(lambda: self._set_mode("resume"))

        self.runner.output_received.connect(self._on_output_received)
        self.runner.started.connect(lambda: self.terminal.set_status("Running", "running"))
        self.runner.finished.connect(self._on_process_finished)
        self.runner.error_occurred.connect(lambda msg: self.terminal.set_status("Error", "failed"))
        self.terminal.stop_requested.connect(self.runner.stop)

    def _restore_release(self, record):
        if not record["running"] and record.get("exit_code") == 0:
            return
        args = record["command"]
        for index, arg in enumerate(args):
            if re.fullmatch(r"v?\d+\.\d+\.\d+(?:[-.][0-9A-Za-z.-]+)?", arg):
                self.version_input.setText(arg)
                mode = "resume" if "--resume-after-prep" in args else "replace" if "--replace" in args else "new"
                self._set_mode(mode)
                self.push_cb.setChecked("--push" in args or mode == "resume")
                self.dry_run_cb.setChecked("--dry-run" in args)
                self.skip_flathub_cb.setChecked("--skip-flathub" in args)
                self.skip_gh_release_cb.setChecked("--skip-github-release" in args)
                if mode != "resume" and index + 1 < len(args):
                    self.summary_input.setPlainText(args[index + 1])
                self.jobs_view.track_version(arg)
                break

    def _set_mode(self, mode: str):
        self.current_mode = mode
        if mode == "new":
            self.btn_mode_new.setChecked(True)
            self.mode_hint_lbl.setText("Syncs default branch, creates release prep PR, and publishes tag.")
            self.summary_input.setEnabled(True)
            self.notes_status_lbl.setText("Write release description and changelog:")
            self.btn_fetch_notes.setVisible(False)
            self.push_cb.setEnabled(True)
            self.dry_run_cb.setEnabled(True)
        elif mode == "replace":
            self.btn_mode_replace.setChecked(True)
            self.mode_hint_lbl.setText("Replaces existing tag. You can reuse or modify the tag's release notes below.")
            self.summary_input.setEnabled(True)
            self.notes_status_lbl.setText("Release notes (editable; leave blank to reuse unchanged):")
            self.btn_fetch_notes.setVisible(True)
            self.push_cb.setEnabled(True)
            self.dry_run_cb.setEnabled(True)
            self._fetch_notes_for_version()
        elif mode == "resume":
            self.btn_mode_resume.setChecked(True)
            self.mode_hint_lbl.setText("Resumes release asset staging and publication after release prep PR has merged.")
            self.summary_input.setEnabled(False)
            self.notes_status_lbl.setText("Release notes already committed; not needed for resume.")
            self.btn_fetch_notes.setVisible(False)
            self.push_cb.setEnabled(False)
            self.dry_run_cb.setEnabled(False)
        self._update_button_label()

    def _on_version_changed(self):
        if self.current_mode == "replace":
            self._fetch_notes_for_version()

    def _fetch_notes_for_version(self):
        ver = self.version_input.text().strip()
        if not ver:
            return
        notes = self.git_mgr.get_tag_notes(ver)
        if notes:
            self.summary_input.setPlainText(notes)
            self.notes_status_lbl.setText(f"Loaded notes from tag {ver} (editable):")
        else:
            self.notes_status_lbl.setText(f"No existing tag notes found for {ver}.")

    def _on_output_received(self, text: str):
        self.terminal.append_output(text)
        self.jobs_view.process_output_chunk(text)

    def _on_process_started(self):
        self.output_tabs.show_activity("local", self.terminal, force=True)

    def _on_activity_restored(self, record):
        if record["running"]:
            if self.output_tabs.activity != "remote":
                self.output_tabs.show_activity("local", self.terminal)
        elif record.get("exit_code") != 0 or record.get("stopped"):
            self.output_tabs.show_activity("failed", self.terminal)

    def _focus_workflow(self):
        if self.output_tabs.activity != "failed":
            self.output_tabs.show_activity("remote", self.jobs_view)

    def _focus_remote_jobs(self):
        if not self.runner.is_running or self.output_tabs.activity == "remote":
            self._focus_workflow()

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

    def _update_button_label(self):
        if self._is_waiting_for_pr:
            self.btn_release.setText(f"Cancel Waiting (PR #{self._waiting_pr_num})")
            self.btn_release.setEnabled(True)
            return

        pr_num = self._get_selected_pr_number()
        waiting = self.wait_pr_cb.isChecked() and bool(pr_num)

        if self.current_mode == "resume":
            self.btn_release.setText(f"Resume after PR #{pr_num} merges" if waiting else "Resume Release")
        elif self.current_mode == "replace":
            self.btn_release.setText(f"Replace after PR #{pr_num} merges" if waiting else "Replace & Release")
        else:
            self.btn_release.setText(f"Release after PR #{pr_num} merges" if waiting else "Start Release")

        if self.runner.is_running and not waiting and self.current_mode != "replace":
            self.btn_release.setEnabled(False)
        else:
            self.btn_release.setEnabled(True)

    def _on_wait_pr_toggled(self, checked: bool):
        self._update_button_label()

    def _on_pr_text_changed(self, text: str):
        pr_num = self._get_selected_pr_number()
        if pr_num and not self.wait_pr_cb.isChecked():
            self.wait_pr_cb.setChecked(True)
        elif not text.strip() and self.wait_pr_cb.isChecked():
            self.wait_pr_cb.setChecked(False)
        self._update_button_label()

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
        self._update_button_label()

    def refresh(self):
        latest = self.git_mgr.get_latest_tag()
        self.latest_tag_lbl.setText(f"Latest tag: {latest}")
        if not self.version_input.text().strip():
            self._suggest_next(latest)
        self._fetch_active_prs()

    def _suggest_next(self, current_tag: str):
        match = re.match(r"^v?(\d+)\.(\d+)\.(\d+)", current_tag)
        if match:
            major, minor, patch = map(int, match.groups())
            self.version_input.setText(f"v{major}.{minor}.{patch + 1}")
        else:
            self.version_input.setText("v0.1.0")

    def _resume_selected(self):
        if self.jobs_view.release_tag:
            self.version_input.setText(self.jobs_view.release_tag)
            self._set_mode("resume")
        if getattr(self.jobs_view, "pr_url", None):
            m = re.search(r"/pull/(\d+)", self.jobs_view.pr_url)
            if m:
                pr_num = m.group(1)
                self.pr_combo.setEditText(f"#{pr_num}")
                self.wait_pr_cb.setChecked(True)
                self._update_button_label()

    def _bump_version(self, bump_type: str):
        v = self.version_input.text().strip()
        match = re.match(r"^v?(\d+)\.(\d+)\.(\d+)", v)
        if match:
            major, minor, patch = map(int, match.groups())
            if bump_type == "patch":
                patch += 1
            elif bump_type == "minor":
                minor += 1
                patch = 0
            self.version_input.setText(f"v{major}.{minor}.{patch}")

    def run_release(self):
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
                    "Please enter or select a PR number to wait for, or uncheck 'Release after PR merges'.",
                )
                return

        if self.runner.is_running and not pr_num:
            action_desc = "replace" if self.current_mode == "replace" else "restart"
            reply = QMessageBox.question(
                self,
                "Release in Progress",
                f"A release script is currently running. Do you want to stop it and {action_desc} the release?",
                QMessageBox.StandardButton.Yes | QMessageBox.StandardButton.No,
                QMessageBox.StandardButton.No,
            )
            if reply != QMessageBox.StandardButton.Yes:
                return

        script_path = self.config.resolve_release_script(self.git_mgr.repo_path)
        if not os.path.exists(script_path):
            QMessageBox.critical(
                self,
                "Script Not Found",
                f"Cannot find release script at:\n{script_path}\n\nYou can configure the script location in Settings.",
            )
            return

        version = self.version_input.text().strip()
        if not version:
            QMessageBox.warning(self, "Missing Version", "Please enter a release version (e.g. v0.15.5).")
            return

        mode = self.current_mode
        summary = self.summary_input.toPlainText().strip()

        if mode == "new" and not summary:
            QMessageBox.warning(self, "Missing Summary", "Please enter a release summary / notes.")
            return

        args = []

        if mode == "resume":
            args.append("--resume-after-prep")
        elif mode == "replace":
            args.append("--replace")
            if self.push_cb.isChecked():
                args.append("--push")
            if self.dry_run_cb.isChecked():
                args.append("--dry-run")
        else:
            # new release
            if self.push_cb.isChecked():
                args.append("--push")
            if self.dry_run_cb.isChecked():
                args.append("--dry-run")

        if self.skip_flathub_cb.isChecked():
            args.append("--skip-flathub")
        if self.skip_gh_release_cb.isChecked():
            args.append("--skip-github-release")

        args.append(version)
        if mode != "resume" and summary:
            args.append(summary)

        # Meaningful high-signal summary
        mode_label = "New Release" if mode == "new" else ("Replace Tag" if mode == "replace" else "Resume Release")
        details = [
            ("Release Mode", mode_label),
            ("Target Version", version),
        ]
        if pr_num:
            details.append(("Wait for PR", f"#{pr_num} (auto-start when merged)"))
        details.append(("Push Remote", "Yes" if (mode == "resume" or self.push_cb.isChecked()) else "No"))

        if self.dry_run_cb.isChecked() and mode != "resume":
            details.append(("Dry Run", "Yes"))
        if self.skip_flathub_cb.isChecked():
            details.append(("Flathub PR", "Skipped"))
        if self.skip_gh_release_cb.isChecked():
            details.append(("GitHub Release", "Skipped"))

        popup = ConfirmPreviewDialog(
            title=f"Release {version}",
            details=details,
            preview_text=summary if (mode != "resume" and summary) else None,
            preview_text_label="Release Notes Preview:",
            action_button_text="Confirm Release",
            anchor_widget=self.btn_release,
            parent=self,
        )
        if not popup.exec():
            return

        if mode == "replace":
            self.terminal.append_output(f"[*] Cancelling in-progress CI runs for {version}...\n")
            self._cancel_ci_runs(version)

        if pr_num:
            if self.runner.is_running:
                self.terminal.append_output("[*] Stopping running script to wait for PR...\n")
                self.runner.stop()
            self._start_waiting_for_pr(pr_num, script_path, args, version)
        else:
            self._stop_and_run(lambda: self._start_release_process(script_path, args, version))

    def _cancel_ci_runs(self, version: str):
        def _cancel():
            try:
                res = subprocess.run(
                    ["gh", "run", "list", "--limit", "50", "--json", "databaseId,displayTitle,status",
                     "--jq", f'.[] | select((.displayTitle == "Stage {version}" or .displayTitle == "{version}") and .status != "completed") | .databaseId'],
                    cwd=self.git_mgr.repo_path, capture_output=True, text=True, timeout=10
                )
                if res.returncode == 0:
                    for run_id in res.stdout.split():
                        subprocess.run(["gh", "run", "cancel", run_id], cwd=self.git_mgr.repo_path, timeout=5)
            except Exception:
                pass
        threading.Thread(target=_cancel, daemon=True).start()

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

    def _start_release_process(self, script_path: str, args: list, version: str):
        self.jobs_view.reset_state()
        self.terminal.clear_terminal()
        self.terminal.set_status(f"Running {version}", "running")
        self.output_tabs.show_activity("local", self.terminal, force=True)
        self.runner.start(script_path, args, cwd=self.git_mgr.repo_path)
        self.jobs_view.track_version(version)

    def _start_waiting_for_pr(self, pr_num: str, script_path: str, args: list, version: str):
        self._is_waiting_for_pr = True
        self._waiting_pr_num = pr_num
        self._waiting_script_path = script_path
        self._waiting_args = args
        self._waiting_version = version

        self.btn_release.setEnabled(True)
        self._update_button_label()
        self.wait_pr_status_lbl.setText(f"Waiting for #{pr_num}...")
        self.wait_pr_status_lbl.setStyleSheet("color: #e3b341; font-size: 11px;")

        self.terminal.clear_terminal()
        self.terminal.set_status(f"Waiting for PR #{pr_num}", "running")
        self.terminal.append_output(f"[*] Release scheduled for {version}.\n")
        self.terminal.append_output(f"[*] Polling GitHub PR #{pr_num}... Release will start automatically once merged.\n")
        self.output_tabs.show_activity("local", self.terminal, force=True)

        self._pr_poll_timer.start(10000)
        self._poll_waiting_pr()

    def _poll_waiting_pr(self):
        if not self._is_waiting_for_pr:
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
        version = self._waiting_version

        if state == "MERGED":
            self._pr_poll_timer.stop()
            self._is_waiting_for_pr = False
            self.wait_pr_status_lbl.setText(f"PR #{pr_num} merged!")
            self.wait_pr_status_lbl.setStyleSheet("color: #3fb950; font-size: 11px;")
            self.terminal.append_output(f"\n[+] PR #{pr_num} has merged! Starting release {version}...\n\n")
            send_desktop_notification("PR Merged", f"PR #{pr_num} merged. Starting release {version}...")
            self._update_button_label()
            if self.current_mode == "replace":
                self._cancel_ci_runs(version)
            self._stop_and_run(lambda: self._start_release_process(
                self._waiting_script_path, self._waiting_args, self._waiting_version
            ))
        elif state == "CLOSED":
            self._pr_poll_timer.stop()
            self._is_waiting_for_pr = False
            self.wait_pr_status_lbl.setText(f"PR #{pr_num} closed without merge")
            self.wait_pr_status_lbl.setStyleSheet("color: #f85149; font-size: 11px;")
            self.terminal.append_output(f"\n[!] PR #{pr_num} was closed without being merged. Release aborted.\n")
            self.terminal.set_status(f"PR #{pr_num} Closed", "failed")
            self._update_button_label()
            send_desktop_notification("Release Aborted", f"PR #{pr_num} was closed without merging.")
        else:
            self.wait_pr_status_lbl.setText(f"PR #{pr_num} is OPEN (waiting)")
            self.wait_pr_status_lbl.setStyleSheet("color: #e3b341; font-size: 11px;")
            self.terminal.append_output(f"[*] PR #{pr_num} is {state}. Polling again in 10s...\n")

    def _cancel_pr_wait(self):
        self._pr_poll_timer.stop()
        self._is_waiting_for_pr = False
        self.wait_pr_status_lbl.setText("Wait cancelled")
        self.wait_pr_status_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
        if self._waiting_pr_num:
            self.terminal.append_output(f"\n[!] Cancelled waiting for PR #{self._waiting_pr_num}.\n")
        self.terminal.set_status("Cancelled", "normal")
        self._update_button_label()

    def _on_process_finished(self, exit_code: int):
        if self._is_waiting_for_pr:
            self._update_button_label()
            return
        if exit_code != 0 or self.runner.record.get("stopped"):
            self.output_tabs.show_activity("failed", self.terminal)
        else:
            self.jobs_view.sync_remote_status()
        status = "success" if exit_code == 0 else "failed"
        self.terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        self._update_button_label()
        if self.window().isVisible():
            self.refresh()
