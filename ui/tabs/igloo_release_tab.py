import re
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
    QPushButton,
    QButtonGroup,
    QGroupBox,
    QMessageBox,
)
from core.config import AppConfig
from core.git_manager import GitManager
from core.runner import ProcessRunner
from ui.components.activity_tabs import ActivityTabs
from ui.components.terminal_view import TerminalWidget
from ui.components.release_jobs_view import ReleaseJobsWidget
from ui.dialogs.confirm_preview_dialog import ConfirmPreviewDialog


class IglooReleaseTab(QWidget):
    """
    Release tab for Igloo repository.
    Runs fast local release via 'just release <bump> "<summary>"' (or 'just release-local'),
    which prepares metadata, creates signed release tag, pushes tag to origin,
    and directly triggers release artifact workflows without running the slow full CI gate.
    """

    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        self.runner = ProcessRunner(self)
        self.current_bump = "patch"
        self._init_ui()
        self._wire_signals()
        self.terminal.bind_runner(self.runner, self.git_mgr.repo_path, "release", None)
        self.refresh()

    def _init_ui(self):
        root_layout = QHBoxLayout(self)
        root_layout.setContentsMargins(6, 6, 6, 6)
        root_layout.setSpacing(8)

        splitter = QSplitter(Qt.Orientation.Horizontal)
        root_layout.addWidget(splitter)

        # ----------------- Left Panel -----------------
        left_widget = QWidget()
        left_layout = QVBoxLayout(left_widget)
        left_layout.setContentsMargins(0, 2, 4, 2)
        left_layout.setSpacing(8)

        # Bump Selection: Compact segment buttons
        bump_grp = QGroupBox("Version Bump")
        bump_layout = QVBoxLayout(bump_grp)
        bump_layout.setContentsMargins(8, 8, 8, 8)
        bump_layout.setSpacing(6)

        h_bumps = QHBoxLayout()
        h_bumps.setSpacing(6)
        h_bumps.setAlignment(Qt.AlignmentFlag.AlignLeft)

        self.bump_group = QButtonGroup(self)
        self.bump_group.setExclusive(True)

        mode_style = (
            "QPushButton { "
            "  background-color: #0b0e14; "
            "  border: 1px solid #30363d; "
            "  color: #8b949e; "
            "  border-radius: 5px; "
            "  padding: 4px 14px; "
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

        self.btn_patch = QPushButton("Patch")
        self.btn_patch.setCheckable(True)
        self.btn_patch.setChecked(True)
        self.btn_patch.setFixedHeight(26)
        self.btn_patch.setStyleSheet(mode_style)
        self.bump_group.addButton(self.btn_patch, 0)
        h_bumps.addWidget(self.btn_patch)

        self.btn_minor = QPushButton("Minor")
        self.btn_minor.setCheckable(True)
        self.btn_minor.setFixedHeight(26)
        self.btn_minor.setStyleSheet(mode_style)
        self.bump_group.addButton(self.btn_minor, 1)
        h_bumps.addWidget(self.btn_minor)

        self.btn_major = QPushButton("Major")
        self.btn_major.setCheckable(True)
        self.btn_major.setFixedHeight(26)
        self.btn_major.setStyleSheet(mode_style)
        self.bump_group.addButton(self.btn_major, 2)
        h_bumps.addWidget(self.btn_major)

        h_bumps.addStretch()
        bump_layout.addLayout(h_bumps)

        left_layout.addWidget(bump_grp, 0)

        # Target Version Group
        ver_grp = QGroupBox("Target Version")
        ver_layout = QVBoxLayout(ver_grp)
        ver_layout.setContentsMargins(8, 8, 8, 8)
        ver_layout.setSpacing(4)

        h_ver = QHBoxLayout()
        h_ver.setSpacing(6)
        self.version_input = QLineEdit()
        self.version_input.setPlaceholderText("e.g. v3.8.1")
        ver_layout.addWidget(self.version_input)

        h_tag_info = QHBoxLayout()
        self.latest_tag_lbl = QLabel("Latest tag: detecting...")
        self.latest_tag_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
        h_tag_info.addWidget(self.latest_tag_lbl)
        h_tag_info.addStretch()
        ver_layout.addLayout(h_tag_info)

        left_layout.addWidget(ver_grp, 0)

        # Release Summary Group
        notes_grp = QGroupBox("Release Summary / Notes")
        notes_layout = QVBoxLayout(notes_grp)
        notes_layout.setContentsMargins(8, 8, 8, 8)
        notes_layout.setSpacing(4)

        self.notes_status_lbl = QLabel("Summary of changes (becomes first paragraph of GitHub release):")
        self.notes_status_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
        notes_layout.addWidget(self.notes_status_lbl)

        self.summary_input = QPlainTextEdit()
        self.summary_input.setPlaceholderText(
            "Write the release summary / changelog here...\n\n"
            "This will be recorded in the signed tag and GitHub Release notes."
        )
        self.summary_input.setMinimumHeight(240)
        notes_layout.addWidget(self.summary_input, 1)

        left_layout.addWidget(notes_grp, 1)

        # Execution Options
        opts_grp = QGroupBox("Execution Options")
        opts_layout = QVBoxLayout(opts_grp)
        opts_layout.setContentsMargins(8, 8, 8, 8)
        opts_layout.setSpacing(4)

        self.push_cb = QCheckBox("Publish release and dispatch artifacts (just release)")
        self.push_cb.setChecked(True)
        opts_layout.addWidget(self.push_cb)

        left_layout.addWidget(opts_grp, 0)

        # Start Release Button
        self.btn_release = QPushButton("Start Release")
        self.btn_release.setObjectName("primaryButton")
        self.btn_release.setFixedHeight(32)
        self.btn_release.clicked.connect(self.run_release)
        left_layout.addWidget(self.btn_release, 0)

        splitter.addWidget(left_widget)

        # ----------------- Right Panel -----------------
        right_widget = QWidget()
        right_layout = QVBoxLayout(right_widget)
        right_layout.setContentsMargins(0, 0, 0, 0)
        right_layout.setSpacing(0)

        self.output_tabs = ActivityTabs()
        self.jobs_view = ReleaseJobsWidget(project_name="Igloo", repo_path=self.git_mgr.repo_path)
        self.terminal = TerminalWidget(repo_path=self.git_mgr.repo_path)

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
        self.runner.started.connect(lambda: self.btn_release.setEnabled(False))
        self.runner.restored.connect(lambda record: self.btn_release.setEnabled(not record["running"]))
        self.runner.finished.connect(lambda code: self.btn_release.setEnabled(True))
        self.btn_patch.clicked.connect(lambda: self._set_bump("patch"))
        self.btn_minor.clicked.connect(lambda: self._set_bump("minor"))
        self.btn_major.clicked.connect(lambda: self._set_bump("major"))

        self.runner.output_received.connect(self._on_output_received)
        self.runner.started.connect(lambda: self.terminal.set_status("Running", "running"))
        self.runner.finished.connect(self._on_process_finished)
        self.runner.error_occurred.connect(lambda msg: self.terminal.set_status("Error", "failed"))
        self.terminal.stop_requested.connect(self.runner.stop)

    def _set_bump(self, bump: str):
        self.current_bump = bump
        latest = self.git_mgr.get_latest_tag()
        self._calculate_version(latest, bump)

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

    def refresh(self):
        self.terminal.repo_path = self.git_mgr.repo_path
        latest = self.git_mgr.get_latest_tag()
        self.latest_tag_lbl.setText(f"Latest tag: {latest}")
        self._calculate_version(latest, self.current_bump)

    def _calculate_version(self, current_tag: str, bump: str):
        match = re.search(r"v?(\d+)\.(\d+)\.(\d+)", current_tag)
        if match:
            major, minor, patch = map(int, match.groups())
            if bump == "patch":
                patch += 1
            elif bump == "minor":
                minor += 1
                patch = 0
            elif bump == "major":
                major += 1
                minor = 0
                patch = 0
            self.version_input.setText(f"v{major}.{minor}.{patch}")
        else:
            self.version_input.setText("v0.1.0")

    def run_release(self):
        if self.runner.is_running:
            QMessageBox.warning(self, "Running", "A script is already running.")
            return

        version = self.version_input.text().strip()
        if not version:
            QMessageBox.warning(self, "Missing Version", "Please enter a release version (e.g. v3.8.1).")
            return

        summary = self.summary_input.toPlainText().strip()
        if not summary:
            QMessageBox.warning(self, "Missing Summary", "Please enter a release summary / notes.")
            return

        will_push = self.push_cb.isChecked()
        recipe = "release" if will_push else "release-local"

        details = [
            ("Release Bump", self.current_bump.capitalize()),
            ("Target Version", version),
            ("Release Recipe", f"just {recipe}"),
            ("Push & Artifacts", "Yes (push tag, release, and build artifacts)" if will_push else "No (local signed tag only)"),
            ("Release Notes", f"{len(summary)} characters"),
        ]

        popup = ConfirmPreviewDialog(
            title=f"Igloo Release {version}",
            details=details,
            preview_text=summary,
            preview_text_label="Release Notes Preview:",
            action_button_text=f"Start Release ({version})",
            anchor_widget=self.btn_release,
            parent=self,
        )
        if not popup.exec():
            return

        self.jobs_view.reset_state()
        self.terminal.clear_terminal()
        self.terminal.set_status(f"Running {version}", "running")
        self.output_tabs.show_activity("local", self.terminal, force=True)

        # Delegate directly to just recipe as documented in AGENTS.md:
        # just release <patch|minor|major> "<user summary>"
        args = [
            recipe,
            self.current_bump,
            summary,
        ]
        self.runner.start("just", args, cwd=self.git_mgr.repo_path)

    def _on_process_finished(self, exit_code: int):
        if exit_code != 0 or self.runner.record.get("stopped"):
            self.output_tabs.show_activity("failed", self.terminal)
        else:
            self.jobs_view.sync_remote_status()
        status = "success" if exit_code == 0 else "failed"
        self.terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        if self.window().isVisible():
            self.refresh()
