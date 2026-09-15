import os
from PyQt6.QtCore import Qt
from PyQt6.QtWidgets import (
    QDialog,
    QVBoxLayout,
    QHBoxLayout,
    QGroupBox,
    QLabel,
    QLineEdit,
    QPushButton,
    QCheckBox,
    QFileDialog,
    QMessageBox,
)
from core.config import AppConfig, ConfigManager


class SettingsDialog(QDialog):
    def __init__(self, config: AppConfig, current_repo: str, parent=None):
        super().__init__(parent)
        self.config = config
        self.current_repo = current_repo
        self.setWindowTitle("Settings")
        self.resize(540, 380)
        self._init_ui()
        self._load_values()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(14, 14, 14, 14)
        layout.setSpacing(12)

        title_lbl = QLabel("Application Settings")
        title_lbl.setStyleSheet("font-size: 14px; font-weight: 700; color: #58a6ff;")
        layout.addWidget(title_lbl)

        # ---------------- Scripts Group ----------------
        scripts_grp = QGroupBox("Script Locations")
        scripts_layout = QVBoxLayout(scripts_grp)
        scripts_layout.setContentsMargins(10, 10, 10, 10)
        scripts_layout.setSpacing(6)

        scripts_layout.addWidget(QLabel("Publish PR Script:"))
        h_pub = QHBoxLayout()
        h_pub.setSpacing(6)
        self.publish_script_edit = QLineEdit()
        self.publish_script_edit.setPlaceholderText(".local/scripts/publish-pr.sh")
        h_pub.addWidget(self.publish_script_edit)

        btn_browse_pub = QPushButton("Browse...")
        btn_browse_pub.setFixedHeight(26)
        btn_browse_pub.clicked.connect(lambda: self._browse_file(self.publish_script_edit))
        h_pub.addWidget(btn_browse_pub)
        scripts_layout.addLayout(h_pub)

        scripts_layout.addWidget(QLabel("Release Script:"))
        h_rel = QHBoxLayout()
        h_rel.setSpacing(6)
        self.release_script_edit = QLineEdit()
        self.release_script_edit.setPlaceholderText(".local/scripts/release-rufin.sh")
        h_rel.addWidget(self.release_script_edit)

        btn_browse_rel = QPushButton("Browse...")
        btn_browse_rel.setFixedHeight(26)
        btn_browse_rel.clicked.connect(lambda: self._browse_file(self.release_script_edit))
        h_rel.addWidget(btn_browse_rel)
        scripts_layout.addLayout(h_rel)

        lbl_hint = QLabel("Relative paths are resolved against the active repository root.")
        lbl_hint.setStyleSheet("color: #7d8590; font-size: 11px;")
        scripts_layout.addWidget(lbl_hint)

        layout.addWidget(scripts_grp)

        # ---------------- Defaults Group ----------------
        defaults_grp = QGroupBox("Defaults")
        defaults_layout = QVBoxLayout(defaults_grp)
        defaults_layout.setContentsMargins(10, 10, 10, 10)
        defaults_layout.setSpacing(6)

        h_base = QHBoxLayout()
        lbl_base = QLabel("Base Branch:")
        lbl_base.setFixedWidth(100)
        self.base_branch_edit = QLineEdit()
        h_base.addWidget(lbl_base)
        h_base.addWidget(self.base_branch_edit)
        defaults_layout.addLayout(h_base)

        self.auto_merge_cb = QCheckBox("Enable squash auto-merge by default")
        defaults_layout.addWidget(self.auto_merge_cb)

        self.wait_ci_cb = QCheckBox("Wait for CI checks by default (--wait)")
        defaults_layout.addWidget(self.wait_ci_cb)

        layout.addWidget(defaults_grp)

        layout.addStretch()

        # ---------------- Button Bar ----------------
        btns = QHBoxLayout()
        btn_reset = QPushButton("Reset to Defaults")
        btn_reset.clicked.connect(self._reset_defaults)
        btns.addWidget(btn_reset)

        btns.addStretch()

        btn_cancel = QPushButton("Cancel")
        btn_cancel.clicked.connect(self.reject)
        btns.addWidget(btn_cancel)

        btn_save = QPushButton("Save")
        btn_save.setObjectName("primaryButton")
        btn_save.clicked.connect(self._save_settings)
        btns.addWidget(btn_save)

        layout.addLayout(btns)

    def _browse_file(self, target_edit: QLineEdit):
        start_dir = self.current_repo
        chosen, _ = QFileDialog.getOpenFileName(
            self, "Select Script", start_dir, "Shell Scripts (*.sh);;All Files (*)"
        )
        if chosen:
            try:
                rel = os.path.relpath(chosen, self.current_repo)
                if not rel.startswith(".."):
                    target_edit.setText(rel)
                    return
            except Exception:
                pass
            target_edit.setText(chosen)

    def _load_values(self):
        self.publish_script_edit.setText(self.config.publish_pr_script)
        self.release_script_edit.setText(self.config.release_script)
        self.base_branch_edit.setText(self.config.default_base_branch)
        self.auto_merge_cb.setChecked(self.config.default_auto_merge)
        self.wait_ci_cb.setChecked(self.config.default_wait_ci)

    def _reset_defaults(self):
        default = AppConfig()
        self.publish_script_edit.setText(default.publish_pr_script)
        self.release_script_edit.setText(default.release_script)
        self.base_branch_edit.setText(default.default_base_branch)
        self.auto_merge_cb.setChecked(default.default_auto_merge)
        self.wait_ci_cb.setChecked(default.default_wait_ci)

    def _save_settings(self):
        pub = self.publish_script_edit.text().strip()
        rel = self.release_script_edit.text().strip()

        if not pub or not rel:
            QMessageBox.warning(self, "Invalid Input", "Script paths cannot be empty.")
            return

        self.config.publish_pr_script = pub
        self.config.release_script = rel
        self.config.default_base_branch = self.base_branch_edit.text().strip() or "main"
        self.config.default_auto_merge = self.auto_merge_cb.isChecked()
        self.config.default_wait_ci = self.wait_ci_cb.isChecked()

        ConfigManager.save(self.config)
        self.accept()
