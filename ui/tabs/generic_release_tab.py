import os
import re
import sys
from pathlib import Path

from PyQt6.QtCore import Qt
from PyQt6.QtWidgets import (
    QWidget, QVBoxLayout, QHBoxLayout, QFormLayout, QSplitter, QLabel,
    QLineEdit, QPlainTextEdit, QCheckBox, QPushButton, QGroupBox, QMessageBox, QComboBox,
)
from core.git_manager import GitManager
from core.runner import ProcessRunner
from core.release import release_workflows
from core.state import StateManager
from ui.components.activity_tabs import ActivityTabs
from ui.components.terminal_view import TerminalWidget
from ui.components.release_jobs_view import ReleaseJobsWidget
from ui.dialogs.confirm_preview_dialog import ConfirmPreviewDialog


class GenericReleaseTab(QWidget):
    def __init__(self, git_mgr: GitManager, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.runner = ProcessRunner(self)
        self.state_key = f"project_release_form:{os.path.realpath(git_mgr.repo_path)}"
        self.latest_tag = ""
        self._init_ui()
        self.refresh()
        saved = StateManager.get(self.state_key, {})
        self.version_input.setText(saved.get("tag", self.version_input.text()))
        self.target_input.setText(saved.get("target", "HEAD"))
        self.summary_input.setPlainText(saved.get("notes", ""))
        self.push_cb.setChecked(saved.get("push", True))
        self.github_cb.setChecked(saved.get("github", True))
        publisher_index = self.publisher_input.findData(saved.get("workflow", self.publisher_input.currentData()))
        if publisher_index >= 0:
            self.publisher_input.setCurrentIndex(publisher_index)
        self.publisher_input.setEnabled(self.github_cb.isChecked())
        self._wire_signals()
        self.terminal.bind_runner(self.runner, git_mgr.repo_path, "release", None)

    def _init_ui(self):
        layout = QHBoxLayout(self)
        layout.setContentsMargins(6, 6, 6, 6)
        splitter = QSplitter(Qt.Orientation.Horizontal)
        layout.addWidget(splitter)
        left = QWidget()
        form = QVBoxLayout(left)
        form.setContentsMargins(0, 2, 4, 2)
        form.setSpacing(8)

        version_group = QGroupBox("Tag")
        version_layout = QVBoxLayout(version_group)
        self.version_input = QLineEdit()
        self.version_input.setPlaceholderText("v1.2.3")
        version_layout.addWidget(self.version_input)
        bumps = QHBoxLayout()
        for bump in ("patch", "minor", "major"):
            button = QPushButton(bump.capitalize())
            button.clicked.connect(lambda checked, value=bump: self._suggest_tag(value))
            bumps.addWidget(button)
        version_layout.addLayout(bumps)
        self.latest_tag_lbl = QLabel()
        version_layout.addWidget(self.latest_tag_lbl)
        target_layout = QFormLayout()
        self.target_input = QLineEdit("HEAD")
        target_layout.addRow("Commit", self.target_input)
        version_layout.addLayout(target_layout)
        form.addWidget(version_group)

        notes_group = QGroupBox("Release notes")
        notes_layout = QVBoxLayout(notes_group)
        self.summary_input = QPlainTextEdit()
        self.summary_input.setMinimumHeight(160)
        notes_layout.addWidget(self.summary_input)
        form.addWidget(notes_group, 1)

        options_group = QGroupBox("Options")
        options_layout = QVBoxLayout(options_group)
        self.push_cb = QCheckBox("Push tag to origin")
        self.push_cb.setChecked(True)
        options_layout.addWidget(self.push_cb)
        self.github_cb = QCheckBox("Create GitHub release")
        self.github_cb.setChecked(True)
        options_layout.addWidget(self.github_cb)
        self.publisher_input = QComboBox()
        for filename, name in release_workflows(self.git_mgr.repo_path):
            self.publisher_input.addItem(f"Workflow: {name}", filename)
        self.publisher_input.addItem("GitHub CLI", "")
        publisher_layout = QFormLayout()
        publisher_layout.addRow("Publisher", self.publisher_input)
        options_layout.addLayout(publisher_layout)
        form.addWidget(options_group)
        self.btn_release = QPushButton("Release")
        self.btn_release.setObjectName("primaryButton")
        self.btn_release.setFixedHeight(32)
        form.addWidget(self.btn_release)
        splitter.addWidget(left)

        self.output_tabs = ActivityTabs()
        self.jobs_view = ReleaseJobsWidget(
            project_name=Path(self.git_mgr.repo_path).name,
            repo_path=self.git_mgr.repo_path, generic=True,
        )
        self.terminal = TerminalWidget(repo_path=self.git_mgr.repo_path)
        self.output_tabs.addTab(self.jobs_view, "Jobs and Progress")
        self.output_tabs.addTab(self.terminal, "Terminal Log")
        splitter.addWidget(self.output_tabs)
        splitter.setSizes([380, 640])

    def _wire_signals(self):
        self.btn_release.clicked.connect(self.run_release)
        self.version_input.textChanged.connect(self._save_form)
        self.target_input.textChanged.connect(self._save_form)
        self.summary_input.textChanged.connect(self._save_form)
        self.push_cb.toggled.connect(self._save_form)
        self.github_cb.toggled.connect(self._save_form)
        self.github_cb.toggled.connect(self.publisher_input.setEnabled)
        self.publisher_input.currentIndexChanged.connect(self._save_form)
        self.runner.output_received.connect(self.terminal.append_output)
        self.runner.started.connect(self._started)
        self.runner.restored.connect(self._restored)
        self.runner.finished.connect(self._finished)
        self.runner.error_occurred.connect(self._failed)
        self.terminal.stop_requested.connect(self.runner.stop)
        self.jobs_view.remote_status_ready.connect(self._remote_ready)

    def _save_form(self):
        StateManager.update(**{self.state_key: {
            "tag": self.version_input.text(), "target": self.target_input.text(),
            "notes": self.summary_input.toPlainText(), "push": self.push_cb.isChecked(),
            "github": self.github_cb.isChecked(),
            "workflow": self.publisher_input.currentData(),
        }})

    def refresh(self):
        self.terminal.repo_path = self.git_mgr.repo_path
        if self.jobs_view.repo_path != self.git_mgr.repo_path:
            self.state_key = f"project_release_form:{os.path.realpath(self.git_mgr.repo_path)}"
            self.jobs_view.repo_path = self.git_mgr.repo_path
            self.jobs_view.state_key = f"project_release_state:{os.path.realpath(self.git_mgr.repo_path)}"
            self.jobs_view.selected_tag = ""
            self.jobs_view.reset_state()
        code, out, _ = self.git_mgr._run_git(["tag", "--sort=-version:refname"])
        self.latest_tag = out.splitlines()[0] if code == 0 and out.strip() else ""
        self.latest_tag_lbl.setText(f"Latest tag: {self.latest_tag or 'None'}")
        if not self.version_input.text():
            self._suggest_tag("patch")

    def _suggest_tag(self, bump):
        match = re.fullmatch(r"(v?)(\d+)\.(\d+)\.(\d+)", self.latest_tag)
        if match:
            prefix = match[1]
            parts = [int(value) for value in match.groups()[1:]]
            index = {"major": 0, "minor": 1, "patch": 2}[bump]
            parts[index] += 1
            parts[index + 1:] = [0] * (2 - index)
            self.version_input.setText(prefix + ".".join(map(str, parts)))
        elif not self.latest_tag:
            self.version_input.setText("v0.1.0")

    def run_release(self):
        if self.runner.is_running:
            return
        tag = self.version_input.text().strip()
        target = self.target_input.text().strip() or "HEAD"
        code, _, error = self.git_mgr._run_git(["check-ref-format", f"refs/tags/{tag}"])
        if not tag or tag.startswith("-") or code:
            QMessageBox.warning(self, "Invalid tag", error.strip() or "Enter a valid Git tag.")
            return
        code, commit, error = self.git_mgr._run_git([
            "rev-parse", "--verify", "--end-of-options", f"{target}^{{commit}}"
        ])
        if code:
            QMessageBox.warning(self, "Invalid commit", error.strip())
            return
        commit = commit.strip()
        notes = self.summary_input.toPlainText().strip()
        push = self.push_cb.isChecked()
        github = self.github_cb.isChecked()
        workflow = self.publisher_input.currentData() if github else ""
        popup = ConfirmPreviewDialog(
            title=f"Release {tag}",
            details=[("Repository", self.git_mgr.repo_path), ("Tag", tag),
                     ("Commit", commit), ("Push tag", "Yes" if push else "No"),
                     ("Create GitHub release", "Yes" if github else "No"),
                     ("Publisher", self.publisher_input.currentText() if github else "None")],
            preview_text=notes or tag, preview_text_label="Release notes",
            action_button_text="Release", anchor_widget=self.btn_release, parent=self,
        )
        if not popup.exec():
            return
        helper = Path(__file__).resolve().parents[2] / "core" / "release.py"
        args = [str(helper), f"--tag={tag}", f"--target={commit}", f"--notes={notes}"]
        if push:
            args.append("--push")
        if github:
            args.append("--github")
        if workflow:
            args.append(f"--workflow={workflow}")
        self.terminal.clear_terminal()
        if push or github:
            self.jobs_view.track_version(tag)
        self.runner.start(sys.executable, args, cwd=self.git_mgr.repo_path)

    def _started(self):
        self.btn_release.setEnabled(False)
        self.terminal.set_status("Running", "running")
        self.output_tabs.show_activity("local", self.terminal, force=True)

    def _restored(self, record):
        self.btn_release.setEnabled(not record["running"])
        tag = next((arg.removeprefix("--tag=") for arg in record["command"]
                    if arg.startswith("--tag=")), "")
        if tag and ("--push" in record["command"] or "--github" in record["command"]):
            self.jobs_view.track_version(tag)
        if record["running"]:
            if self.output_tabs.activity != "remote":
                self.output_tabs.show_activity("local", self.terminal)
        elif record.get("exit_code") != 0 or record.get("stopped"):
            self.output_tabs.show_activity("failed", self.terminal)

    def _finished(self, code):
        self.btn_release.setEnabled(True)
        failed = code != 0 or self.runner.record.get("stopped")
        self.terminal.set_status(f"Exit {code}" if failed else "Completed", "failed" if failed else "success")
        if failed:
            self.output_tabs.show_activity("failed", self.terminal)
        else:
            self.jobs_view.sync_remote_status()
        self.refresh()

    def _failed(self, message):
        self.terminal.append_output(f"{message}\n")
        self.terminal.set_status("Error", "failed")
        self.output_tabs.show_activity("failed", self.terminal)

    def _remote_ready(self):
        workflow = any(arg.startswith("--workflow=") for arg in self.runner.record.get("command", []))
        if self.output_tabs.activity != "failed" and (not self.runner.is_running or workflow):
            self.output_tabs.show_activity("remote", self.jobs_view)
