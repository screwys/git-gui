import os
import re
from typing import Optional
from PyQt6.QtCore import Qt
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
    QTabWidget,
    QMessageBox,
    QFrame,
)
from core.config import AppConfig
from core.git_manager import GitManager
from core.runner import ProcessRunner
from ui.components.terminal_view import TerminalWidget
from ui.components.release_jobs_view import ReleaseJobsWidget


class ReleaseTab(QWidget):
    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        self.runner = ProcessRunner(self)
        self._init_ui()
        self._wire_signals()
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

        # Mode Selection
        mode_grp = QGroupBox("Release Mode")
        mode_layout = QVBoxLayout(mode_grp)
        mode_layout.setContentsMargins(8, 8, 8, 8)
        mode_layout.setSpacing(6)

        self.mode_combo = QComboBox()
        self.mode_combo.addItem("New Release", "new")
        self.mode_combo.addItem("Replace Failed Release (--replace)", "replace")
        self.mode_combo.addItem("Resume Release (--resume-after-prep)", "resume")
        self.mode_combo.currentIndexChanged.connect(self._on_mode_changed)
        mode_layout.addWidget(self.mode_combo)

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
        self.btn_fetch_notes.setVisible(False)
        h_tag_info.addWidget(self.btn_fetch_notes)

        ver_layout.addLayout(h_tag_info)

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

        self.output_tabs = QTabWidget()
        self.jobs_view = ReleaseJobsWidget()
        self.terminal = TerminalWidget()

        self.output_tabs.addTab(self.jobs_view, "Jobs & Progress")
        self.output_tabs.addTab(self.terminal, "Terminal Log")
        right_layout.addWidget(self.output_tabs)

        splitter.addWidget(right_widget)
        splitter.setSizes([420, 640])

    def _wire_signals(self):
        self.runner.output_received.connect(self._on_output_received)
        self.runner.started.connect(lambda: self.terminal.set_status("Running", "running"))
        self.runner.finished.connect(self._on_process_finished)
        self.runner.error_occurred.connect(lambda msg: self.terminal.set_status("Error", "failed"))
        self.terminal.stop_requested.connect(self.runner.stop)

    def _on_mode_changed(self):
        mode = self.mode_combo.currentData()
        if mode == "new":
            self.mode_hint_lbl.setText("Syncs default branch, creates release prep PR, and publishes tag.")
            self.summary_input.setEnabled(True)
            self.notes_status_lbl.setText("Write release description and changelog:")
            self.btn_fetch_notes.setVisible(False)
            self.push_cb.setEnabled(True)
            self.dry_run_cb.setEnabled(True)
            self.btn_release.setText("Start Release")
        elif mode == "replace":
            self.mode_hint_lbl.setText("Replaces existing tag. You can reuse or modify the tag's release notes below.")
            self.summary_input.setEnabled(True)
            self.notes_status_lbl.setText("Release notes (editable; leave blank to reuse unchanged):")
            self.btn_fetch_notes.setVisible(True)
            self.push_cb.setEnabled(True)
            self.dry_run_cb.setEnabled(True)
            self.btn_release.setText("Replace & Release")
            self._fetch_notes_for_version()
        elif mode == "resume":
            self.mode_hint_lbl.setText("Resumes release asset staging and publication after release prep PR has merged.")
            self.summary_input.setEnabled(False)
            self.notes_status_lbl.setText("Release notes already committed; not needed for resume.")
            self.btn_fetch_notes.setVisible(False)
            self.push_cb.setEnabled(False)  # resume-after-prep implies push
            self.dry_run_cb.setEnabled(False)
            self.btn_release.setText("Resume Release")

    def _on_version_changed(self):
        mode = self.mode_combo.currentData()
        if mode == "replace":
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

    def refresh(self):
        latest = self.git_mgr.get_latest_tag()
        self.latest_tag_lbl.setText(f"Latest tag: {latest}")
        if not self.version_input.text().strip():
            self._suggest_next(latest)

    def _suggest_next(self, current_tag: str):
        match = re.match(r"^v?(\d+)\.(\d+)\.(\d+)", current_tag)
        if match:
            major, minor, patch = map(int, match.groups())
            self.version_input.setText(f"v{major}.{minor}.{patch + 1}")
        else:
            self.version_input.setText("v0.1.0")

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
        if self.runner.is_running:
            QMessageBox.warning(self, "Running", "A script is already running.")
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

        mode = self.mode_combo.currentData()
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

        # Confirm if pushing
        will_push = (mode == "resume") or (self.push_cb.isChecked() and not self.dry_run_cb.isChecked())
        if will_push:
            action_desc = "replace the failed tag and publish" if mode == "replace" else (
                "resume release staging for" if mode == "resume" else "release"
            )
            reply = QMessageBox.question(
                self,
                "Confirm Release",
                f"Are you sure you want to {action_desc} {version}?\nThis will create/push tags, open PRs, and dispatch workflows.",
                QMessageBox.StandardButton.Yes | QMessageBox.StandardButton.No,
                QMessageBox.StandardButton.No,
            )
            if reply != QMessageBox.StandardButton.Yes:
                return

        self.jobs_view.reset_state()
        self.terminal.clear_terminal()
        self.terminal.set_status(f"Running {version}", "running")
        self.output_tabs.setCurrentWidget(self.jobs_view)
        self.runner.start(script_path, args, cwd=self.git_mgr.repo_path)

    def _on_process_finished(self, exit_code: int):
        status = "success" if exit_code == 0 else "failed"
        self.terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        self.refresh()
