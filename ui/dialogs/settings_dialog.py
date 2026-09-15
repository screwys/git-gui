import os
from PyQt6.QtCore import Qt, QPoint
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
    QFrame,
)
from core.config import AppConfig, ConfigManager


class SettingsDialog(QDialog):
    def __init__(self, config: AppConfig, current_repo: str, parent=None):
        super().__init__(parent)
        self.config = config
        self.current_repo = current_repo
        # Make this a popup that closes when clicked outside
        self.setWindowFlags(Qt.WindowType.Popup | Qt.WindowType.FramelessWindowHint)
        self.setAttribute(Qt.WidgetAttribute.WA_TranslucentBackground, False)
        self.resize(520, 390)
        self.setStyleSheet(
            "SettingsDialog { background-color: #161b22; border: 1px solid #484f58; border-radius: 8px; } "
            "QGroupBox { background-color: #0d1117; border: 1px solid #30363d; border-radius: 6px; margin-top: 10px; padding: 10px; font-weight: 600; font-size: 11px; } "
            "QGroupBox::title { background-color: #0d1117; color: #8b949e; left: 8px; } "
            "QLineEdit { background-color: #161b22; border: 1px solid #30363d; color: #e6edf3; border-radius: 4px; padding: 4px 6px; } "
            "QLineEdit:focus { border-color: #58a6ff; }"
        )
        self._init_ui()
        self._load_values()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(14, 12, 14, 12)
        layout.setSpacing(10)

        # Header bar with close button
        header = QHBoxLayout()
        header.setSpacing(6)

        title_lbl = QLabel("Preferences")
        title_lbl.setStyleSheet("font-size: 13px; font-weight: 700; color: #58a6ff;")
        header.addWidget(title_lbl)

        header.addStretch()

        btn_close = QPushButton("✕")
        btn_close.setFixedSize(22, 22)
        btn_close.setStyleSheet(
            "QPushButton { background: transparent; border: none; color: #8b949e; font-size: 12px; font-weight: bold; } "
            "QPushButton:hover { background-color: #30363d; color: white; border-radius: 4px; }"
        )
        btn_close.clicked.connect(self.close)
        header.addWidget(btn_close)

        layout.addLayout(header)

        # ---------------- Scripts Group ----------------
        scripts_grp = QGroupBox("Script Locations")
        scripts_layout = QVBoxLayout(scripts_grp)
        scripts_layout.setContentsMargins(8, 8, 8, 8)
        scripts_layout.setSpacing(6)

        scripts_layout.addWidget(QLabel("Publish PR Script:"))
        h_pub = QHBoxLayout()
        h_pub.setSpacing(6)
        self.publish_script_edit = QLineEdit()
        self.publish_script_edit.setPlaceholderText(".local/scripts/publish-pr.sh")
        self.publish_script_edit.textChanged.connect(self._auto_save)
        h_pub.addWidget(self.publish_script_edit)

        btn_browse_pub = QPushButton("Browse...")
        btn_browse_pub.setFixedHeight(24)
        btn_browse_pub.clicked.connect(lambda: self._browse_file(self.publish_script_edit))
        h_pub.addWidget(btn_browse_pub)
        scripts_layout.addLayout(h_pub)

        scripts_layout.addWidget(QLabel("Release Script:"))
        h_rel = QHBoxLayout()
        h_rel.setSpacing(6)
        self.release_script_edit = QLineEdit()
        self.release_script_edit.setPlaceholderText(".local/scripts/release-rufin.sh")
        self.release_script_edit.textChanged.connect(self._auto_save)
        h_rel.addWidget(self.release_script_edit)

        btn_browse_rel = QPushButton("Browse...")
        btn_browse_rel.setFixedHeight(24)
        btn_browse_rel.clicked.connect(lambda: self._browse_file(self.release_script_edit))
        h_rel.addWidget(btn_browse_rel)
        scripts_layout.addLayout(h_rel)

        layout.addWidget(scripts_grp)

        # ---------------- Defaults Group ----------------
        defaults_grp = QGroupBox("Defaults")
        defaults_layout = QVBoxLayout(defaults_grp)
        defaults_layout.setContentsMargins(8, 8, 8, 8)
        defaults_layout.setSpacing(6)

        h_base = QHBoxLayout()
        lbl_base = QLabel("Base Branch:")
        lbl_base.setFixedWidth(90)
        self.base_branch_edit = QLineEdit()
        self.base_branch_edit.textChanged.connect(self._auto_save)
        h_base.addWidget(lbl_base)
        h_base.addWidget(self.base_branch_edit)
        defaults_layout.addLayout(h_base)

        self.auto_merge_cb = QCheckBox("Squash auto-merge by default")
        self.auto_merge_cb.toggled.connect(self._auto_save)
        defaults_layout.addWidget(self.auto_merge_cb)

        self.wait_ci_cb = QCheckBox("Wait for CI checks by default (--wait)")
        self.wait_ci_cb.toggled.connect(self._auto_save)
        defaults_layout.addWidget(self.wait_ci_cb)

        self.wrap_diff_cb = QCheckBox("Wrap diff & editor lines by default")
        self.wrap_diff_cb.toggled.connect(self._auto_save)
        defaults_layout.addWidget(self.wrap_diff_cb)

        layout.addWidget(defaults_grp)

        layout.addStretch()

        # Bottom row
        btns = QHBoxLayout()
        btn_reset = QPushButton("Reset Defaults")
        btn_reset.setFixedHeight(26)
        btn_reset.clicked.connect(self._reset_defaults)
        btns.addWidget(btn_reset)

        btns.addStretch()

        btn_done = QPushButton("Done")
        btn_done.setObjectName("primaryButton")
        btn_done.setFixedHeight(26)
        btn_done.clicked.connect(self.accept)
        btns.addWidget(btn_done)

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
        self.publish_script_edit.blockSignals(True)
        self.release_script_edit.blockSignals(True)
        self.base_branch_edit.blockSignals(True)
        self.auto_merge_cb.blockSignals(True)
        self.wait_ci_cb.blockSignals(True)
        self.wrap_diff_cb.blockSignals(True)

        self.publish_script_edit.setText(self.config.publish_pr_script)
        self.release_script_edit.setText(self.config.release_script)
        self.base_branch_edit.setText(self.config.default_base_branch)
        self.auto_merge_cb.setChecked(self.config.default_auto_merge)
        self.wait_ci_cb.setChecked(self.config.default_wait_ci)
        self.wrap_diff_cb.setChecked(self.config.wrap_diff)

        self.publish_script_edit.blockSignals(False)
        self.release_script_edit.blockSignals(False)
        self.base_branch_edit.blockSignals(False)
        self.auto_merge_cb.blockSignals(False)
        self.wait_ci_cb.blockSignals(False)
        self.wrap_diff_cb.blockSignals(False)

    def _reset_defaults(self):
        default = AppConfig()
        self.publish_script_edit.setText(default.publish_pr_script)
        self.release_script_edit.setText(default.release_script)
        self.base_branch_edit.setText(default.default_base_branch)
        self.auto_merge_cb.setChecked(default.default_auto_merge)
        self.wait_ci_cb.setChecked(default.default_wait_ci)
        self.wrap_diff_cb.setChecked(default.wrap_diff)
        self._auto_save()

    def _auto_save(self):
        pub = self.publish_script_edit.text().strip()
        rel = self.release_script_edit.text().strip()
        if pub:
            self.config.publish_pr_script = pub
        if rel:
            self.config.release_script = rel

        base = self.base_branch_edit.text().strip()
        if base:
            self.config.default_base_branch = base

        self.config.default_auto_merge = self.auto_merge_cb.isChecked()
        self.config.default_wait_ci = self.wait_ci_cb.isChecked()
        self.config.wrap_diff = self.wrap_diff_cb.isChecked()

        ConfigManager.save(self.config)

    def hideEvent(self, event):
        self._auto_save()
        super().hideEvent(event)
