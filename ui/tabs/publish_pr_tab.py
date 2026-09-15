import os
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
    QComboBox,
    QPushButton,
    QGroupBox,
    QScrollArea,
    QMessageBox,
)
from core.config import AppConfig
from core.git_manager import GitManager
from core.runner import ProcessRunner
from ui.components.file_list import FileListWidget
from ui.components.diff_viewer import DiffViewer
from ui.components.terminal_view import TerminalWidget


class PublishPrTab(QWidget):
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

        self.main_splitter = QSplitter(Qt.Orientation.Horizontal)
        root_layout.addWidget(self.main_splitter)

        # ----------------- Left Panel -----------------
        left_widget = QWidget()
        left_layout = QVBoxLayout(left_widget)
        left_layout.setContentsMargins(0, 0, 0, 0)
        left_layout.setSpacing(6)

        # File List (compact height)
        self.file_list = FileListWidget()
        self.file_list.setFixedHeight(210)
        left_layout.addWidget(self.file_list)

        # Form Scroll Area
        form_scroll = QScrollArea()
        form_scroll.setWidgetResizable(True)
        form_scroll.setStyleSheet("QScrollArea { border: none; background: transparent; }")

        form_widget = QWidget()
        form_layout = QVBoxLayout(form_widget)
        form_layout.setContentsMargins(0, 2, 4, 2)
        form_layout.setSpacing(8)

        # Branch configuration group
        branch_grp = QGroupBox("Branch Options")
        branch_layout = QVBoxLayout(branch_grp)
        branch_layout.setContentsMargins(8, 8, 8, 8)
        branch_layout.setSpacing(4)

        # Topic branch row
        h_branch = QHBoxLayout()
        h_branch.setSpacing(6)
        lbl_branch = QLabel("Branch:")
        lbl_branch.setFixedWidth(50)
        self.branch_input = QLineEdit()
        self.branch_input.setPlaceholderText("Derived from title, or current branch")
        h_branch.addWidget(lbl_branch)
        h_branch.addWidget(self.branch_input)
        branch_layout.addLayout(h_branch)

        # Base branch row
        h_base = QHBoxLayout()
        h_base.setSpacing(6)
        lbl_base = QLabel("Base:")
        lbl_base.setFixedWidth(50)
        self.base_input = QLineEdit(self.config.default_base_branch)
        h_base.addWidget(lbl_base)
        h_base.addWidget(self.base_input)
        branch_layout.addLayout(h_base)

        form_layout.addWidget(branch_grp)

        # PR Content group
        pr_grp = QGroupBox("Pull Request Metadata")
        pr_layout = QVBoxLayout(pr_grp)
        pr_layout.setContentsMargins(8, 8, 8, 8)
        pr_layout.setSpacing(3)

        lbl_title = QLabel("Title (required for new PRs):")
        lbl_title.setStyleSheet("color: #8b949e; font-size: 11px;")
        pr_layout.addWidget(lbl_title)

        self.title_input = QLineEdit()
        self.title_input.setPlaceholderText("e.g. feat(ui): add modern PR and release GUI")
        pr_layout.addWidget(self.title_input)

        lbl_body = QLabel("Body / Description (optional):")
        lbl_body.setStyleSheet("color: #8b949e; font-size: 11px; margin-top: 4px;")
        pr_layout.addWidget(lbl_body)

        self.body_input = QPlainTextEdit()
        self.body_input.setFixedHeight(90)
        self.body_input.setPlaceholderText("Markdown description of the pull request...")
        pr_layout.addWidget(self.body_input)

        h_phase = QHBoxLayout()
        h_phase.setSpacing(6)
        lbl_phase = QLabel("Phase:")
        lbl_phase.setFixedWidth(50)
        lbl_phase.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.phase_input = QLineEdit()
        self.phase_input.setPlaceholderText("Defaults to TITLE")
        h_phase.addWidget(lbl_phase)
        h_phase.addWidget(self.phase_input)
        pr_layout.addLayout(h_phase)

        form_layout.addWidget(pr_grp)

        # Script Flags group
        flags_grp = QGroupBox("Publish Options")
        flags_layout = QVBoxLayout(flags_grp)
        flags_layout.setContentsMargins(8, 8, 8, 8)
        flags_layout.setSpacing(4)

        self.auto_merge_cb = QCheckBox("Enable squash auto-merge (--no-auto-merge if unchecked)")
        self.auto_merge_cb.setChecked(self.config.default_auto_merge)
        flags_layout.addWidget(self.auto_merge_cb)

        self.wait_cb = QCheckBox("Wait for CI checks and squash-merge (--wait)")
        self.wait_cb.setChecked(self.config.default_wait_ci)
        flags_layout.addWidget(self.wait_cb)

        h_i18n = QHBoxLayout()
        h_i18n.setSpacing(6)
        lbl_i18n = QLabel("i18n:")
        lbl_i18n.setStyleSheet("color: #8b949e; font-size: 11px;")
        self.i18n_combo = QComboBox()
        self.i18n_combo.addItem("Normal (Ignore changed .po catalogs)", "none")
        self.i18n_combo.addItem("Include prepared .po files (--include-i18n-catalogs)", "include")
        self.i18n_combo.addItem("Sync and merge all .po catalogs (--sync-i18n-catalogs)", "sync")
        h_i18n.addWidget(lbl_i18n)
        h_i18n.addWidget(self.i18n_combo)
        flags_layout.addLayout(h_i18n)

        form_layout.addWidget(flags_grp)

        # Publish Button
        self.btn_publish = QPushButton("🚀 Publish Pull Request")
        self.btn_publish.setObjectName("primaryButton")
        self.btn_publish.setFixedHeight(34)
        self.btn_publish.clicked.connect(self.run_publish)
        form_layout.addWidget(self.btn_publish)

        form_layout.addStretch()

        form_scroll.setWidget(form_widget)
        left_layout.addWidget(form_scroll)

        self.main_splitter.addWidget(left_widget)

        # ----------------- Right Panel (Splitter) -----------------
        right_splitter = QSplitter(Qt.Orientation.Vertical)

        self.diff_viewer = DiffViewer()
        right_splitter.addWidget(self.diff_viewer)

        self.terminal = TerminalWidget()
        right_splitter.addWidget(self.terminal)

        right_splitter.setSizes([380, 260])
        self.main_splitter.addWidget(right_splitter)

        self.main_splitter.setSizes([380, 620])

    def _wire_signals(self):
        self.file_list.file_selected.connect(self._show_single_file_diff)
        self.file_list.view_diff_requested.connect(self._show_multi_file_diff)

        self.runner.output_received.connect(self.terminal.append_output)
        self.runner.started.connect(lambda: self.terminal.set_status("Running...", "running"))
        self.runner.finished.connect(self._on_process_finished)
        self.runner.error_occurred.connect(lambda msg: self.terminal.set_status("Error", "failed"))
        self.terminal.stop_requested.connect(self.runner.stop)

    def refresh(self):
        items = self.git_mgr.get_status_files()
        self.file_list.set_files(items)
        curr_branch = self.git_mgr.get_current_branch()
        if curr_branch and curr_branch != "main":
            self.branch_input.setPlaceholderText(f"Current: {curr_branch}")
        else:
            self.branch_input.setPlaceholderText("Derived from title (on main)")

        if items:
            self._show_multi_file_diff([i.path for i in items])
        else:
            self.diff_viewer.set_diff("", "Diff Preview (Clean working tree)")

    def _show_single_file_diff(self, path: str):
        diff_text = self.git_mgr.get_diff([path])
        self.diff_viewer.set_diff(diff_text, f"Diff: {path}")

    def _show_multi_file_diff(self, paths: List[str]):
        if not paths:
            diff_text = self.git_mgr.get_diff()
            self.diff_viewer.set_diff(diff_text, "All Changes Diff")
        else:
            diff_text = self.git_mgr.get_diff(paths)
            self.diff_viewer.set_diff(diff_text, f"Diff ({len(paths)} files selected)")

    def run_publish(self):
        if self.runner.is_running:
            QMessageBox.warning(self, "Running", "A script is already running!")
            return

        script_path = self.config.resolve_publish_script(self.git_mgr.repo_path)
        if not os.path.exists(script_path):
            QMessageBox.critical(
                self,
                "Script Not Found",
                f"Cannot find publish script at:\n{script_path}\n\nYou can configure the script location in Settings (⚙).",
            )
            return

        args = []

        base_branch = self.base_input.text().strip()
        if base_branch:
            args.extend(["--base", base_branch])

        branch = self.branch_input.text().strip()
        if branch:
            args.extend(["--branch", branch])

        body = self.body_input.toPlainText().strip()
        env_vars = {}
        if body:
            args.extend(["--body", body])
            env_vars["RUFIN_ALLOW_PR_BODY"] = "1"

        phase = self.phase_input.text().strip()
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

        title = self.title_input.text().strip()
        if title:
            args.append(title)

        selected_paths = self.file_list.get_selected_paths()
        all_paths = [i.path for i in self.file_list.items]

        if selected_paths and len(selected_paths) < len(all_paths):
            args.extend(selected_paths)

        self.terminal.clear_terminal()
        self.terminal.set_status("Publishing PR...", "running")
        self.runner.start(script_path, args, cwd=self.git_mgr.repo_path, env_vars=env_vars)

    def _on_process_finished(self, exit_code: int):
        status = "success" if exit_code == 0 else "failed"
        self.terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        self.refresh()
