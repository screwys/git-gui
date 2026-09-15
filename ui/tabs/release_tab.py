import os
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
    QGroupBox,
    QMessageBox,
)
from core.config import AppConfig
from core.git_manager import GitManager
from core.runner import ProcessRunner
from ui.components.terminal_view import TerminalWidget


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
        root_layout.setContentsMargins(4, 4, 4, 4)
        root_layout.setSpacing(6)

        splitter = QSplitter(Qt.Orientation.Horizontal)
        root_layout.addWidget(splitter)

        # ----------------- Left Panel: Form -----------------
        left_widget = QWidget()
        left_layout = QVBoxLayout(left_widget)
        left_layout.setContentsMargins(0, 2, 4, 2)
        left_layout.setSpacing(8)

        # Version Group (compact)
        ver_grp = QGroupBox("Release Version")
        ver_layout = QVBoxLayout(ver_grp)
        ver_layout.setContentsMargins(8, 8, 8, 8)
        ver_layout.setSpacing(4)

        h_ver = QHBoxLayout()
        h_ver.setSpacing(6)
        self.version_input = QLineEdit()
        self.version_input.setPlaceholderText("e.g. v0.15.5")
        h_ver.addWidget(self.version_input)

        btn_patch = QPushButton("+ Patch")
        btn_patch.setFixedHeight(28)
        btn_patch.clicked.connect(lambda: self._bump_version("patch"))
        h_ver.addWidget(btn_patch)

        btn_minor = QPushButton("+ Minor")
        btn_minor.setFixedHeight(28)
        btn_minor.clicked.connect(lambda: self._bump_version("minor"))
        h_ver.addWidget(btn_minor)

        ver_layout.addLayout(h_ver)

        self.latest_tag_lbl = QLabel("Latest tag: detecting...")
        self.latest_tag_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
        ver_layout.addWidget(self.latest_tag_lbl)

        left_layout.addWidget(ver_grp, 0)

        # Summary / Release Notes Group (expands to fill vertical space!)
        notes_grp = QGroupBox("Release Summary / Notes")
        notes_layout = QVBoxLayout(notes_grp)
        notes_layout.setContentsMargins(8, 8, 8, 8)
        notes_layout.setSpacing(4)

        self.summary_input = QPlainTextEdit()
        self.summary_input.setPlaceholderText(
            "Write the release description / changelog here...\n\n"
            "This will be recorded in the signed git tag and draft GitHub Release notes."
        )
        self.summary_input.setMinimumHeight(220)
        notes_layout.addWidget(self.summary_input, 1)

        left_layout.addWidget(notes_grp, 1)

        # Flags Group (compact)
        opts_grp = QGroupBox("Release Flags")
        opts_layout = QVBoxLayout(opts_grp)
        opts_layout.setContentsMargins(8, 8, 8, 8)
        opts_layout.setSpacing(3)

        self.push_cb = QCheckBox("Push tag, open PR, and stage assets (--push)")
        self.push_cb.setChecked(True)
        opts_layout.addWidget(self.push_cb)

        self.dry_run_cb = QCheckBox("Dry run tag generation (--dry-run)")
        self.dry_run_cb.setChecked(False)
        opts_layout.addWidget(self.dry_run_cb)

        self.replace_cb = QCheckBox("Replace existing failed tag (--replace)")
        self.replace_cb.setChecked(False)
        opts_layout.addWidget(self.replace_cb)

        self.skip_flathub_cb = QCheckBox("Skip Flathub PR opening (--skip-flathub)")
        self.skip_flathub_cb.setChecked(False)
        opts_layout.addWidget(self.skip_flathub_cb)

        self.skip_gh_release_cb = QCheckBox("Skip GitHub Release publication (--skip-github-release)")
        self.skip_gh_release_cb.setChecked(False)
        opts_layout.addWidget(self.skip_gh_release_cb)

        self.resume_cb = QCheckBox("Resume after release prep (--resume-after-prep)")
        self.resume_cb.setChecked(False)
        opts_layout.addWidget(self.resume_cb)

        left_layout.addWidget(opts_grp, 0)

        # Start Button
        self.btn_release = QPushButton("📦 Start Release")
        self.btn_release.setObjectName("primaryButton")
        self.btn_release.setFixedHeight(34)
        self.btn_release.clicked.connect(self.run_release)
        left_layout.addWidget(self.btn_release, 0)

        splitter.addWidget(left_widget)

        # ----------------- Right Panel: Terminal -----------------
        self.terminal = TerminalWidget()
        splitter.addWidget(self.terminal)

        splitter.setSizes([420, 600])

    def _wire_signals(self):
        self.runner.output_received.connect(self.terminal.append_output)
        self.runner.started.connect(lambda: self.terminal.set_status("Running Release...", "running"))
        self.runner.finished.connect(self._on_process_finished)
        self.runner.error_occurred.connect(lambda msg: self.terminal.set_status("Error", "failed"))
        self.terminal.stop_requested.connect(self.runner.stop)

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
            QMessageBox.warning(self, "Running", "A script is already running!")
            return

        script_path = self.config.resolve_release_script(self.git_mgr.repo_path)
        if not os.path.exists(script_path):
            QMessageBox.critical(
                self,
                "Script Not Found",
                f"Cannot find release script at:\n{script_path}\n\nYou can configure the script location in Settings (⚙).",
            )
            return

        version = self.version_input.text().strip()
        if not version:
            QMessageBox.warning(self, "Missing Version", "Please enter a release version (e.g. v0.15.5).")
            return

        summary = self.summary_input.toPlainText().strip()
        if not summary and not self.resume_cb.isChecked() and not self.replace_cb.isChecked():
            QMessageBox.warning(self, "Missing Summary", "Please enter a release summary / notes.")
            return

        args = []
        if self.push_cb.isChecked():
            args.append("--push")
        if self.dry_run_cb.isChecked():
            args.append("--dry-run")
        if self.replace_cb.isChecked():
            args.append("--replace")
        if self.skip_flathub_cb.isChecked():
            args.append("--skip-flathub")
        if self.skip_gh_release_cb.isChecked():
            args.append("--skip-github-release")
        if self.resume_cb.isChecked():
            args.append("--resume-after-prep")

        args.append(version)
        if summary:
            args.append(summary)

        if self.push_cb.isChecked() and not self.dry_run_cb.isChecked():
            reply = QMessageBox.question(
                self,
                "Confirm Release",
                f"Are you sure you want to release {version}?\nThis will create tags, open PRs, and push to origin.",
                QMessageBox.StandardButton.Yes | QMessageBox.StandardButton.No,
                QMessageBox.StandardButton.No,
            )
            if reply != QMessageBox.StandardButton.Yes:
                return

        self.terminal.clear_terminal()
        self.terminal.set_status(f"Releasing {version}...", "running")
        self.runner.start(script_path, args, cwd=self.git_mgr.repo_path)

    def _on_process_finished(self, exit_code: int):
        status = "success" if exit_code == 0 else "failed"
        self.terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        self.refresh()
