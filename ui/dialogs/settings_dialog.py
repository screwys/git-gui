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
)
from core.config import AppConfig, ConfigManager


class SettingsDialog(QDialog):
    def __init__(self, config: AppConfig, parent=None):
        super().__init__(parent)
        self.config = config
        self.setWindowFlags(Qt.WindowType.Popup | Qt.WindowType.FramelessWindowHint)
        self.setAttribute(Qt.WidgetAttribute.WA_TranslucentBackground, False)
        self.resize(540, 440)
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

        # ---------------- Repositories Group ----------------
        repos_grp = QGroupBox("Project Repositories")
        repos_layout = QVBoxLayout(repos_grp)
        repos_layout.setContentsMargins(8, 8, 8, 8)
        repos_layout.setSpacing(6)

        # Rufin path
        repos_layout.addWidget(QLabel("Rufin Repository Path:"))
        h_rufin = QHBoxLayout()
        self.rufin_path_edit = QLineEdit()
        self.rufin_path_edit.textChanged.connect(self._auto_save)
        h_rufin.addWidget(self.rufin_path_edit)

        btn_browse_rufin = QPushButton("Browse...")
        btn_browse_rufin.setFixedHeight(24)
        btn_browse_rufin.clicked.connect(lambda: self._browse_dir(self.rufin_path_edit))
        h_rufin.addWidget(btn_browse_rufin)
        repos_layout.addLayout(h_rufin)

        # Igloo path
        repos_layout.addWidget(QLabel("Igloo Repository Path:"))
        h_igloo = QHBoxLayout()
        self.igloo_path_edit = QLineEdit()
        self.igloo_path_edit.textChanged.connect(self._auto_save)
        h_igloo.addWidget(self.igloo_path_edit)

        btn_browse_igloo = QPushButton("Browse...")
        btn_browse_igloo.setFixedHeight(24)
        btn_browse_igloo.clicked.connect(lambda: self._browse_dir(self.igloo_path_edit))
        h_igloo.addWidget(btn_browse_igloo)
        repos_layout.addLayout(h_igloo)

        layout.addWidget(repos_grp)

        # ---------------- Scripts Group ----------------
        scripts_grp = QGroupBox("Rufin Scripts")
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
        btn_browse_pub.clicked.connect(lambda: self._browse_file(self.publish_script_edit, self.config.rufin_repo_path))
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
        btn_browse_rel.clicked.connect(lambda: self._browse_file(self.release_script_edit, self.config.rufin_repo_path))
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

    def _browse_dir(self, target_edit: QLineEdit):
        chosen = QFileDialog.getExistingDirectory(self, "Select Repository", target_edit.text())
        if chosen:
            target_edit.setText(chosen)

    def _browse_file(self, target_edit: QLineEdit, start_dir: str):
        chosen, _ = QFileDialog.getOpenFileName(
            self, "Select Script", start_dir, "Shell Scripts (*.sh);;All Files (*)"
        )
        if chosen:
            try:
                rel = os.path.relpath(chosen, start_dir)
                if not rel.startswith(".."):
                    target_edit.setText(rel)
                    return
            except Exception:
                pass
            target_edit.setText(chosen)

    def _load_values(self):
        self.rufin_path_edit.blockSignals(True)
        self.igloo_path_edit.blockSignals(True)
        self.publish_script_edit.blockSignals(True)
        self.release_script_edit.blockSignals(True)
        self.base_branch_edit.blockSignals(True)
        self.auto_merge_cb.blockSignals(True)
        self.wait_ci_cb.blockSignals(True)
        self.wrap_diff_cb.blockSignals(True)

        self.rufin_path_edit.setText(self.config.rufin_repo_path)
        self.igloo_path_edit.setText(self.config.igloo_repo_path)
        self.publish_script_edit.setText(self.config.publish_pr_script)
        self.release_script_edit.setText(self.config.release_script)
        self.base_branch_edit.setText(self.config.default_base_branch)
        self.auto_merge_cb.setChecked(self.config.default_auto_merge)
        self.wait_ci_cb.setChecked(self.config.default_wait_ci)
        self.wrap_diff_cb.setChecked(self.config.wrap_diff)

        self.rufin_path_edit.blockSignals(False)
        self.igloo_path_edit.blockSignals(False)
        self.publish_script_edit.blockSignals(False)
        self.release_script_edit.blockSignals(False)
        self.base_branch_edit.blockSignals(False)
        self.auto_merge_cb.blockSignals(False)
        self.wait_ci_cb.blockSignals(False)
        self.wrap_diff_cb.blockSignals(False)

    def _reset_defaults(self):
        default = AppConfig()
        self.rufin_path_edit.setText(default.rufin_repo_path)
        self.igloo_path_edit.setText(default.igloo_repo_path)
        self.publish_script_edit.setText(default.publish_pr_script)
        self.release_script_edit.setText(default.release_script)
        self.base_branch_edit.setText(default.default_base_branch)
        self.auto_merge_cb.setChecked(default.default_auto_merge)
        self.wait_ci_cb.setChecked(default.default_wait_ci)
        self.wrap_diff_cb.setChecked(default.wrap_diff)
        self._auto_save()

    def _auto_save(self):
        rufin = self.rufin_path_edit.text().strip()
        igloo = self.igloo_path_edit.text().strip()
        if rufin:
            self.config.rufin_repo_path = rufin
            self.config.repo_path = rufin
        if igloo:
            self.config.igloo_repo_path = igloo

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
