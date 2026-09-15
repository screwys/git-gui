import os
import re
from typing import List
from PyQt6.QtCore import Qt, QUrl
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
    QScrollArea,
    QMessageBox,
    QFrame,
    QApplication,
)
from core.config import AppConfig
from core.git_manager import GitManager
from core.runner import ProcessRunner
from ui.components.file_list import FileListWidget
from ui.components.diff_viewer import DiffViewer
from ui.components.terminal_view import TerminalWidget
from ui.dialogs.confirm_preview_dialog import ConfirmPreviewDialog


class PublishPrTab(QWidget):
    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        self.runner = ProcessRunner(self)
        self.detected_pr_url = ""
        self._init_ui()
        self._wire_signals()
        self.refresh()

    def _init_ui(self):
        root_layout = QHBoxLayout(self)
        root_layout.setContentsMargins(6, 6, 6, 6)
        root_layout.setSpacing(8)

        self.main_splitter = QSplitter(Qt.Orientation.Horizontal)
        root_layout.addWidget(self.main_splitter)

        # ----------------- Left Panel -----------------
        self.left_splitter = QSplitter(Qt.Orientation.Vertical)

        # 1. Changes File List
        self.file_list = FileListWidget()
        self.left_splitter.addWidget(self.file_list)

        # 2. Form Scroll Area - fits its natural height (~240px) without vertical scrollbar
        self.form_scroll = QScrollArea()
        self.form_scroll.setWidgetResizable(True)
        self.form_scroll.setVerticalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAsNeeded)
        self.form_scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOff)
        self.form_scroll.setStyleSheet("QScrollArea { border: none; background: transparent; }")

        form_widget = QWidget()
        form_layout = QVBoxLayout(form_widget)
        form_layout.setContentsMargins(0, 0, 4, 0)
        form_layout.setSpacing(4)

        # Branch configuration group (compact 1-row layout)
        branch_grp = QGroupBox("Branch Options")
        branch_layout = QVBoxLayout(branch_grp)
        branch_layout.setContentsMargins(8, 4, 8, 4)
        branch_layout.setSpacing(4)

        h_branch = QHBoxLayout()
        h_branch.setSpacing(6)
        lbl_branch = QLabel("Branch:")
        lbl_branch.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.branch_input = QLineEdit()
        self.branch_input.setPlaceholderText("Derived from title, or current branch")
        h_branch.addWidget(lbl_branch)
        h_branch.addWidget(self.branch_input, 1)

        lbl_base = QLabel("Base:")
        lbl_base.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.base_input = QLineEdit(self.config.default_base_branch)
        self.base_input.setFixedWidth(90)
        h_branch.addWidget(lbl_base)
        h_branch.addWidget(self.base_input)
        branch_layout.addLayout(h_branch)

        form_layout.addWidget(branch_grp)

        # PR Content group (compact layout)
        pr_grp = QGroupBox("Pull Request Details")
        pr_layout = QVBoxLayout(pr_grp)
        pr_layout.setContentsMargins(8, 4, 8, 4)
        pr_layout.setSpacing(4)

        h_title_phase = QHBoxLayout()
        h_title_phase.setSpacing(6)
        lbl_title = QLabel("Title:")
        lbl_title.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.title_input = QLineEdit()
        self.title_input.setPlaceholderText("e.g. feat(ui): add modern PR and release GUI")
        h_title_phase.addWidget(lbl_title)
        h_title_phase.addWidget(self.title_input, 1)

        lbl_phase = QLabel("Phase:")
        lbl_phase.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.phase_input = QLineEdit()
        self.phase_input.setPlaceholderText("Defaults to TITLE")
        self.phase_input.setFixedWidth(110)
        h_title_phase.addWidget(lbl_phase)
        h_title_phase.addWidget(self.phase_input)
        pr_layout.addLayout(h_title_phase)

        self.body_input = QPlainTextEdit()
        self.body_input.setFixedHeight(50)
        self.body_input.setPlaceholderText("Description (optional markdown)...")
        pr_layout.addWidget(self.body_input)

        form_layout.addWidget(pr_grp)

        # Options group (compact 1-row check layout)
        flags_grp = QGroupBox("Options")
        flags_layout = QVBoxLayout(flags_grp)
        flags_layout.setContentsMargins(8, 4, 8, 4)
        flags_layout.setSpacing(3)

        h_checks = QHBoxLayout()
        h_checks.setSpacing(12)
        self.auto_merge_cb = QCheckBox("Squash auto-merge")
        self.auto_merge_cb.setChecked(self.config.default_auto_merge)
        h_checks.addWidget(self.auto_merge_cb)

        self.wait_cb = QCheckBox("Wait for CI checks (--wait)")
        self.wait_cb.setChecked(self.config.default_wait_ci)
        h_checks.addWidget(self.wait_cb)
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

        self.form_scroll.setWidget(form_widget)
        self.left_splitter.addWidget(self.form_scroll)

        # Stretch factors: Changes list grows (1), form stays at natural height (0)
        self.left_splitter.setStretchFactor(0, 1)
        self.left_splitter.setStretchFactor(1, 0)
        self.left_splitter.setSizes([500, 240])

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
        h_banner.addWidget(self.pr_banner_lbl)
        h_banner.addStretch()
        self.btn_open_pr = QPushButton("Open PR on GitHub")
        self.btn_open_pr.setFixedHeight(24)
        self.btn_open_pr.clicked.connect(self._open_detected_pr)
        h_banner.addWidget(self.btn_open_pr)
        right_layout.addWidget(self.pr_banner)

        right_splitter = QSplitter(Qt.Orientation.Vertical)

        self.diff_viewer = DiffViewer(self.config, self.git_mgr.repo_path)
        self.diff_viewer.file_saved.connect(self._on_file_saved)
        right_splitter.addWidget(self.diff_viewer)

        self.terminal = TerminalWidget()
        right_splitter.addWidget(self.terminal)

        right_splitter.setSizes([380, 260])
        right_layout.addWidget(right_splitter)

        self.main_splitter.addWidget(right_widget)
        self.main_splitter.setSizes([380, 620])

    def showEvent(self, event):
        super().showEvent(event)
        # Ensure initial distribution gives full natural height to form so no scrollbar appears
        total_h = self.left_splitter.height()
        if total_h > 300:
            self.left_splitter.setSizes([total_h - 240, 240])

    def _wire_signals(self):
        self.file_list.file_selected.connect(self._show_single_file_diff)
        self.file_list.view_diff_requested.connect(self._show_multi_file_diff)
        self.file_list.publish_requested.connect(self.run_publish)

        self.runner.output_received.connect(self._on_output_received)
        self.runner.started.connect(lambda: self.terminal.set_status("Running", "running"))
        self.runner.finished.connect(self._on_process_finished)
        self.runner.error_occurred.connect(lambda msg: self.terminal.set_status("Error", "failed"))
        self.terminal.stop_requested.connect(self.runner.stop)

    def _on_file_saved(self, rel_path: str):
        self.refresh()
        self._show_single_file_diff(rel_path)

    def _on_output_received(self, text: str):
        self.terminal.append_output(text)
        m = re.search(r"https://github\.com/[^/\s]+/[^/\s]+/pull/(\d+)", text)
        if m:
            url = m.group(0)
            pr_num = m.group(1)
            self.detected_pr_url = url
            self.pr_banner_lbl.setText(f"Pull Request #{pr_num}")
            self.pr_banner.setVisible(True)

    def _open_detected_pr(self):
        if self.detected_pr_url:
            QDesktopServices.openUrl(QUrl(self.detected_pr_url))

    def refresh(self):
        self.diff_viewer.repo_path = self.git_mgr.repo_path
        items = self.git_mgr.get_status_files()
        self.file_list.set_files(items)
        curr_branch = self.git_mgr.get_current_branch()
        if curr_branch and curr_branch != "main":
            self.branch_input.setPlaceholderText(f"Current: {curr_branch}")
        else:
            self.branch_input.setPlaceholderText("Derived from title (on main)")

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
        body = self.body_input.toPlainText().strip()
        phase = self.phase_input.text().strip()
        title = self.title_input.text().strip()

        selected_paths = self.file_list.get_selected_paths()
        all_paths = [i.path for i in self.file_list.items]
        partial_files = bool(selected_paths and len(selected_paths) < len(all_paths))

        args = []
        if base_branch:
            args.extend(["--base", base_branch])
        if branch:
            args.extend(["--branch", branch])

        env_vars = {}
        if body:
            args.extend(["--body", body])
            env_vars["RUFIN_ALLOW_PR_BODY"] = "1"

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

        if title:
            args.append(title)

        if partial_files:
            args.extend(selected_paths)

        details = [
            ("Branch Target", f"{branch or '(derived)'} → {base_branch}"),
            ("PR Title", title or "(derived from branch or commit)"),
            ("Files Included", f"{len(selected_paths)} of {len(all_paths)} files" if partial_files else f"All {len(all_paths)} files"),
            ("Squash Merge", "Enabled" if self.auto_merge_cb.isChecked() else "Disabled"),
            ("Wait CI Checks", "Yes" if self.wait_cb.isChecked() else "No"),
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
        popup.show()

        while popup.isVisible():
            QApplication.processEvents()

        if not popup.confirmed:
            return

        self.detected_pr_url = ""
        self.pr_banner.setVisible(False)
        self.terminal.clear_terminal()
        self.terminal.set_status("Publishing PR", "running")
        self.runner.start(script_path, args, cwd=self.git_mgr.repo_path, env_vars=env_vars)

    def _on_process_finished(self, exit_code: int):
        status = "success" if exit_code == 0 else "failed"
        self.terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        self.refresh()
