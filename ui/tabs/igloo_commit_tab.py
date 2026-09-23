import shlex
from typing import List
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
    QGroupBox,
    QMessageBox,
)
from core.config import AppConfig
from core.git_manager import GitManager
from core.runner import ProcessRunner
from ui.components.file_list import FileListWidget
from ui.components.diff_viewer import DiffViewer
from ui.components.terminal_view import TerminalWidget
from ui.dialogs.confirm_preview_dialog import ConfirmPreviewDialog


class IglooCommitTab(QWidget):
    """
    Standard commit and push workflow tab for Igloo repository.
    Supports staging selected files, writing commit messages/descriptions,
    amend, and atomic pushing to origin (with pre-push skip option).
    """

    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        self.runner = ProcessRunner(self)
        self._init_ui()
        self._wire_signals()
        self.terminal.bind_runner(self.runner, self.git_mgr.repo_path, "commit", None)
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
        self.file_list.btn_publish.setText("Commit & Push")
        self.file_list.btn_publish.setToolTip("Commit selected changes and push to origin")
        self.left_splitter.addWidget(self.file_list)

        # 2. Commit Options Form (Bottom, direct widget with NO scrollbar)
        form_widget = QWidget()
        form_layout = QVBoxLayout(form_widget)
        form_layout.setContentsMargins(0, 2, 0, 0)
        form_layout.setSpacing(4)

        # Commit Details Group
        commit_grp = QGroupBox("Commit Message")
        commit_layout = QVBoxLayout(commit_grp)
        commit_layout.setContentsMargins(8, 4, 8, 4)
        commit_layout.setSpacing(4)

        h_summary = QHBoxLayout()
        h_summary.setSpacing(6)
        lbl_summary = QLabel("Summary:")
        lbl_summary.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.summary_input = QLineEdit()
        self.summary_input.setPlaceholderText("e.g. feat(feed): add interactive media cards")
        h_summary.addWidget(lbl_summary)
        h_summary.addWidget(self.summary_input, 1)
        commit_layout.addLayout(h_summary)

        self.desc_input = QPlainTextEdit()
        self.desc_input.setFixedHeight(45)
        self.desc_input.setPlaceholderText("Extended description (optional markdown)...")
        commit_layout.addWidget(self.desc_input)

        form_layout.addWidget(commit_grp)

        # Options Group
        opts_grp = QGroupBox("Options")
        opts_layout = QVBoxLayout(opts_grp)
        opts_layout.setContentsMargins(8, 4, 8, 4)
        opts_layout.setSpacing(3)

        h_opts = QHBoxLayout()
        h_opts.setSpacing(10)
        self.push_cb = QCheckBox("Push to origin upon commit")
        self.push_cb.setChecked(True)
        self.push_cb.toggled.connect(self._on_push_toggled)
        h_opts.addWidget(self.push_cb)

        self.amend_cb = QCheckBox("Amend (--amend)")
        self.amend_cb.setChecked(False)
        self.amend_cb.toggled.connect(self._on_amend_toggled)
        h_opts.addWidget(self.amend_cb)

        self.skip_pre_push_cb = QCheckBox("Skip pre-push (--no-verify)")
        self.skip_pre_push_cb.setChecked(False)
        self.skip_pre_push_cb.setToolTip("Bypass git pre-push hook checks with --no-verify")
        h_opts.addWidget(self.skip_pre_push_cb)

        h_opts.addStretch()
        opts_layout.addLayout(h_opts)

        form_layout.addWidget(opts_grp)

        self.left_splitter.addWidget(form_widget)

        # Splitter sizing: Changes expands (1), form fits natural size (0)
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

        right_splitter = QSplitter(Qt.Orientation.Vertical)

        self.diff_viewer = DiffViewer(self.config, self.git_mgr.repo_path)
        self.diff_viewer.file_saved.connect(self._on_file_saved)
        right_splitter.addWidget(self.diff_viewer)

        self.terminal = TerminalWidget(repo_path=self.git_mgr.repo_path)
        right_splitter.addWidget(self.terminal)

        right_splitter.setSizes([380, 260])
        right_layout.addWidget(right_splitter)

        self.main_splitter.addWidget(right_widget)
        self.main_splitter.setSizes([380, 620])

    def _wire_signals(self):
        self.runner.started.connect(lambda: self.file_list.btn_publish.setEnabled(False))
        self.runner.restored.connect(lambda record: self.file_list.btn_publish.setEnabled(not record["running"]))
        self.runner.finished.connect(lambda code: self.file_list.btn_publish.setEnabled(True))
        self.file_list.file_selected.connect(self._show_single_file_diff)
        self.file_list.view_diff_requested.connect(self._show_multi_file_diff)
        self.file_list.publish_requested.connect(self.run_commit_and_push)

        self.runner.output_received.connect(self.terminal.append_output)
        self.runner.started.connect(lambda: self.terminal.set_status("Running", "running"))
        self.runner.finished.connect(self._on_process_finished)
        self.runner.error_occurred.connect(lambda msg: self.terminal.set_status("Error", "failed"))
        self.terminal.stop_requested.connect(self.runner.stop)

    def _on_push_toggled(self, checked: bool):
        if checked:
            self.file_list.btn_publish.setText("Commit & Push")
            self.skip_pre_push_cb.setEnabled(True)
        else:
            self.file_list.btn_publish.setText("Commit")
            self.skip_pre_push_cb.setEnabled(False)

    def _on_amend_toggled(self, checked: bool):
        if checked and not self.summary_input.text().strip():
            code, out, _ = self.git_mgr._run_git(["log", "-1", "--pretty=%B"])
            if code == 0 and out.strip():
                lines = out.strip().split("\n", 1)
                self.summary_input.setText(lines[0])
                if len(lines) > 1:
                    self.desc_input.setPlainText(lines[1].strip())

    def _on_file_saved(self, rel_path: str):
        self.refresh()
        self._show_single_file_diff(rel_path)

    def refresh(self):
        self.diff_viewer.repo_path = self.git_mgr.repo_path
        self.terminal.repo_path = self.git_mgr.repo_path
        items = self.git_mgr.get_status_files()
        self.file_list.set_files(items)

        selected_paths = self.file_list.get_selected_paths()
        if len(selected_paths) == 1:
            self._show_single_file_diff(selected_paths[0])
        elif items:
            self._show_multi_file_diff([i.path for i in items])
        else:
            self.diff_viewer.set_diff("", "Clean working tree")

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
            self.diff_viewer.set_diff(
                diff_text, f"All Changes ({len(self.file_list.items)} files)", adds, dels, rel_path=None
            )
        else:
            diff_text = self.git_mgr.get_diff(paths)
            self.diff_viewer.set_diff(
                diff_text, f"{len(paths)} files selected", adds, dels, rel_path=None
            )

    def run_commit_and_push(self):
        if self.runner.is_running:
            QMessageBox.warning(self, "Running", "A git operation is already in progress.")
            return

        summary = self.summary_input.text().strip()
        desc = self.desc_input.toPlainText().strip()
        is_amend = self.amend_cb.isChecked()
        skip_pre_push = self.skip_pre_push_cb.isChecked()

        if not summary and not is_amend:
            QMessageBox.warning(self, "Missing Summary", "Please enter a commit summary or title.")
            return

        selected_paths = self.file_list.get_selected_paths()
        all_paths = [i.path for i in self.file_list.items]
        if not selected_paths and not is_amend:
            QMessageBox.warning(self, "No Files Selected", "Please select at least one file to commit.")
            return

        branch = self.git_mgr.get_current_branch()
        will_push = self.push_cb.isChecked()

        if summary and desc:
            full_msg = f"{summary}\n\n{desc}"
        else:
            full_msg = summary

        details = [
            ("Current Branch", branch),
            ("Commit Title", summary or "(keep existing)"),
            ("Files Included", f"{len(selected_paths)} of {len(all_paths)} files" if len(selected_paths) < len(all_paths) else f"All {len(all_paths)} files"),
            ("Amend", "Yes" if is_amend else "No"),
            ("Push to Origin", f"Yes (origin/{branch})" if will_push else "No (local commit only)"),
        ]
        if will_push:
            details.append(("Skip Pre-push", "Yes (--no-verify)" if skip_pre_push else "No"))

        action_name = "Commit & Push" if will_push else "Commit"
        popup = ConfirmPreviewDialog(
            title=f"Igloo {action_name}",
            details=details,
            preview_text=desc if desc else None,
            preview_text_label="Extended Description:",
            action_button_text=action_name,
            anchor_widget=self.file_list.btn_publish,
            parent=self,
        )
        if not popup.exec():
            return

        commands = []
        if selected_paths:
            escaped_files = " ".join(shlex.quote(p) for p in selected_paths)
            commands.append(f"git add -- {escaped_files}")
        else:
            commands.append("git add -A")

        commit_args = ["git", "commit"]
        if is_amend:
            commit_args.append("--amend")
        if full_msg:
            commit_args.extend(["-m", full_msg])

        escaped_commit = " ".join(shlex.quote(a) for a in commit_args)
        commands.append(escaped_commit)

        if will_push:
            push_cmd = f"git push origin {shlex.quote(branch)}"
            if skip_pre_push:
                push_cmd = f"git push --no-verify origin {shlex.quote(branch)}"
            commands.append(push_cmd)

        full_cmd = " && ".join(commands)

        self.terminal.clear_terminal()
        self.terminal.set_status(f"Executing {action_name}", "running")
        self.runner.start("bash", ["-c", full_cmd], cwd=self.git_mgr.repo_path)

    def _on_process_finished(self, exit_code: int):
        status = "success" if exit_code == 0 else "failed"
        self.terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        if exit_code == 0:
            self.summary_input.clear()
            self.desc_input.clear()
            self.amend_cb.setChecked(False)
        if self.window().isVisible():
            self.refresh()
