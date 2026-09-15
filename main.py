#!/usr/bin/env python3
import sys
import os
import subprocess
from typing import List, Optional
from PyQt6.QtCore import Qt, QPointF, QSize
from PyQt6.QtGui import QMouseEvent, QPainter, QColor, QPen
from PyQt6.QtWidgets import (
    QApplication,
    QMainWindow,
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QTabWidget,
    QPushButton,
    QAbstractButton,
    QLabel,
    QStatusBar,
    QMessageBox,
    QFrame,
)

from core.config import AppConfig, ConfigManager
from core.git_manager import GitManager
from core.runner import ProcessRunner
from ui.style import DARK_STYLESHEET
from ui.tabs.publish_pr_tab import PublishPrTab
from ui.tabs.release_tab import ReleaseTab
from ui.tabs.igloo_commit_tab import IglooCommitTab
from ui.tabs.igloo_release_tab import IglooReleaseTab
from ui.dialogs.settings_dialog import SettingsDialog
from ui.components.icons import create_restart_icon, create_android_icon


def get_gtk_button_layout() -> List[str]:
    """Reads system GTK button layout preference (e.g. 'appmenu:close')."""
    try:
        res = subprocess.run(
            ["gsettings", "get", "org.gnome.desktop.wm.preferences", "button-layout"],
            capture_output=True,
            text=True,
            check=True,
        )
        val = res.stdout.strip().strip("'\"")
        if ":" in val:
            right_part = val.split(":")[-1]
            return [b.strip() for b in right_part.split(",") if b.strip()]
    except Exception:
        pass
    return ["close"]


class TitlebarButton(QAbstractButton):
    """GTK-styled vector-drawn titlebar button."""

    def __init__(self, role: str = "close", parent=None):
        super().__init__(parent)
        self.role = role
        self.setFixedSize(24, 24)
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self.setToolTip(role.capitalize())

    def paintEvent(self, event):
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)

        rect = self.rect()
        cx = rect.width() / 2.0
        cy = rect.height() / 2.0

        if self.underMouse() or self.isDown():
            if self.role == "close":
                bg_color = QColor("#a42325") if self.isDown() else QColor("#da3633")
            else:
                bg_color = QColor("#21262d") if self.isDown() else QColor("#30363d")
            painter.setBrush(bg_color)
            painter.setPen(Qt.PenStyle.NoPen)
            painter.drawEllipse(rect.adjusted(1, 1, -1, -1))

        icon_color = QColor("#ffffff") if self.underMouse() else QColor("#9ca3af")
        pen = QPen(icon_color)
        pen.setWidthF(1.5)
        pen.setCapStyle(Qt.PenCapStyle.RoundCap)
        painter.setPen(pen)

        if self.role == "close":
            r = 4.0
            painter.drawLine(QPointF(cx - r, cy - r), QPointF(cx + r, cy + r))
            painter.drawLine(QPointF(cx + r, cy - r), QPointF(cx - r, cy + r))
        elif self.role == "minimize":
            w = 4.5
            painter.drawLine(QPointF(cx - w, cy), QPointF(cx + w, cy))
        elif self.role == "maximize":
            side = 4.0
            painter.setBrush(Qt.BrushStyle.NoBrush)
            painter.drawRoundedRect(cx - side, cy - side, side * 2, side * 2, 1.0, 1.0)


class DraggableTopBar(QWidget):
    def __init__(self, main_window):
        super().__init__(main_window)
        self.main_window = main_window
        self.drag_position = None
        self.setFixedHeight(36)
        self.setStyleSheet(
            "DraggableTopBar { background-color: #161b22; border-bottom: 1px solid #30363d; border-top-left-radius: 8px; border-top-right-radius: 8px; }"
        )
        self._init_ui()

    def _init_ui(self):
        layout = QHBoxLayout(self)
        layout.setContentsMargins(10, 0, 8, 0)
        layout.setSpacing(8)
        layout.setAlignment(Qt.AlignmentFlag.AlignVCenter)

        # App Title
        self.app_title = QLabel("Git GUI")
        self.app_title.setStyleSheet(
            "font-weight: 800; font-size: 13px; color: #58a6ff; letter-spacing: 0.5px;"
        )
        layout.addWidget(self.app_title)

        # Active Repo indicator badge
        self.repo_badge = QLabel("Rufin")
        self.repo_badge.setStyleSheet(
            "background-color: #21262d; color: #c9d1d9; border: 1px solid #30363d; border-radius: 4px; padding: 2px 8px; font-weight: 600; font-size: 11px;"
        )
        layout.addWidget(self.repo_badge)

        # Branch indicator badge
        self.branch_badge = QLabel("branch: ...")
        self.branch_badge.setStyleSheet(
            "background-color: #21262d; color: #7ee787; border: 1px solid #30363d; border-radius: 4px; padding: 2px 8px; font-weight: 600; font-size: 11px;"
        )
        layout.addWidget(self.branch_badge)

        layout.addStretch()

        # Igloo Action Buttons (Restart & Build Android)
        self.igloo_tools_widget = QWidget()
        h_tools = QHBoxLayout(self.igloo_tools_widget)
        h_tools.setContentsMargins(0, 0, 0, 0)
        h_tools.setSpacing(6)

        self.btn_just_restart = QPushButton()
        self.btn_just_restart.setIcon(create_restart_icon())
        self.btn_just_restart.setIconSize(QSize(16, 16))
        self.btn_just_restart.setFixedSize(28, 24)
        self.btn_just_restart.setToolTip("Restart Server (just restart)")
        self.btn_just_restart.clicked.connect(lambda: self.main_window.run_igloo_just("restart"))
        h_tools.addWidget(self.btn_just_restart)

        self.btn_just_android = QPushButton()
        self.btn_just_android.setIcon(create_android_icon())
        self.btn_just_android.setIconSize(QSize(16, 16))
        self.btn_just_android.setFixedSize(28, 24)
        self.btn_just_android.setToolTip("Build & Install Android App (just build-android)")
        self.btn_just_android.clicked.connect(lambda: self.main_window.run_igloo_just("build-android"))
        h_tools.addWidget(self.btn_just_android)

        self.igloo_tools_widget.setVisible(False)
        layout.addWidget(self.igloo_tools_widget)

        # Git Actions
        self.btn_refresh = QPushButton("Refresh")
        self.btn_refresh.setFixedHeight(24)
        self.btn_refresh.setToolTip("Reload changes, diffs, and branches")
        self.btn_refresh.clicked.connect(self.main_window.refresh_active_repo)
        layout.addWidget(self.btn_refresh)

        self.btn_fetch = QPushButton("Fetch")
        self.btn_fetch.setFixedHeight(24)
        self.btn_fetch.setToolTip("git fetch --prune origin")
        self.btn_fetch.clicked.connect(self.main_window._do_fetch)
        layout.addWidget(self.btn_fetch)

        self.btn_pull = QPushButton("Pull")
        self.btn_pull.setFixedHeight(24)
        self.btn_pull.setToolTip("git pull --ff-only")
        self.btn_pull.clicked.connect(self.main_window._do_pull)
        layout.addWidget(self.btn_pull)

        # Settings button
        self.btn_settings = QPushButton("Settings")
        self.btn_settings.setFixedHeight(24)
        self.btn_settings.setToolTip("Configure repository locations and options")
        self.btn_settings.clicked.connect(self.main_window.open_settings)
        layout.addWidget(self.btn_settings)

        # Separator before window controls
        sep = QFrame()
        sep.setFrameShape(QFrame.Shape.VLine)
        sep.setFrameShadow(QFrame.Shadow.Sunken)
        sep.setStyleSheet("color: #30363d; max-height: 16px;")
        layout.addWidget(sep)

        # GTK Window buttons
        buttons = get_gtk_button_layout()
        if "minimize" in buttons:
            btn_min = TitlebarButton("minimize", self)
            btn_min.clicked.connect(self.main_window.showMinimized)
            layout.addWidget(btn_min)

        if "maximize" in buttons:
            btn_max = TitlebarButton("maximize", self)
            btn_max.clicked.connect(self._toggle_maximize)
            layout.addWidget(btn_max)

        if "close" in buttons:
            btn_close = TitlebarButton("close", self)
            btn_close.clicked.connect(self.main_window.close)
            layout.addWidget(btn_close)

    def _toggle_maximize(self):
        if self.main_window.isMaximized():
            self.main_window.showNormal()
        else:
            self.main_window.showMaximized()

    def mousePressEvent(self, event: QMouseEvent):
        if event.button() == Qt.MouseButton.LeftButton:
            handle = self.main_window.windowHandle()
            if handle and hasattr(handle, "startSystemMove") and handle.startSystemMove():
                event.accept()
                return
            self.drag_position = event.globalPosition().toPoint() - self.main_window.frameGeometry().topLeft()
            event.accept()
        else:
            super().mousePressEvent(event)

    def mouseMoveEvent(self, event: QMouseEvent):
        if event.buttons() == Qt.MouseButton.LeftButton and self.drag_position is not None:
            self.main_window.move(event.globalPosition().toPoint() - self.drag_position)
            event.accept()
        else:
            super().mouseMoveEvent(event)

    def mouseReleaseEvent(self, event: QMouseEvent):
        self.drag_position = None
        super().mouseReleaseEvent(event)

    def mouseDoubleClickEvent(self, event: QMouseEvent):
        if event.button() == Qt.MouseButton.LeftButton:
            self._toggle_maximize()
            event.accept()
        else:
            super().mouseDoubleClickEvent(event)


class RufinView(QWidget):
    """Container widget for Rufin tabs."""

    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(0)

        self.tabs = QTabWidget()
        self.publish_tab = PublishPrTab(self.git_mgr, self.config)
        self.release_tab = ReleaseTab(self.git_mgr, self.config)

        self.tabs.addTab(self.publish_tab, "Publish PR")
        self.tabs.addTab(self.release_tab, "Release")

        layout.addWidget(self.tabs)

    def refresh(self):
        self.publish_tab.refresh()
        self.release_tab.refresh()


class IglooView(QWidget):
    """Container widget for Igloo tabs."""

    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(0)

        self.tabs = QTabWidget()
        self.commit_tab = IglooCommitTab(self.git_mgr, self.config)
        self.release_tab = IglooReleaseTab(self.git_mgr, self.config)

        self.tabs.addTab(self.commit_tab, "Commit & Push")
        self.tabs.addTab(self.release_tab, "Release")

        layout.addWidget(self.tabs)

    def refresh(self):
        self.commit_tab.refresh()
        self.release_tab.refresh()


class MainWindow(QMainWindow):
    def __init__(self):
        super().__init__()
        self.config: AppConfig = ConfigManager.load()
        self.rufin_git_mgr = GitManager(self.config.rufin_repo_path)
        self.igloo_git_mgr = GitManager(self.config.igloo_repo_path)
        self.runner = ProcessRunner(self)
        self._init_ui()
        self._wire_signals()
        self.update_repo_display()

    def _init_ui(self):
        self.setWindowTitle("Git GUI")
        self.resize(1200, 780)
        self.setMinimumSize(880, 560)

        central = QWidget()
        self.setCentralWidget(central)
        root_layout = QVBoxLayout(central)
        root_layout.setContentsMargins(6, 6, 6, 6)
        root_layout.setSpacing(6)

        # Custom draggable top bar
        self.top_bar = DraggableTopBar(self)
        root_layout.addWidget(self.top_bar)

        # Main Repository Tabs: Rufin & Igloo side-by-side
        self.project_tabs = QTabWidget()
        self.project_tabs.setStyleSheet(
            "QTabWidget::tab-bar { alignment: left; } "
            "QTabBar::tab { font-weight: 600; font-size: 12px; padding: 6px 16px; margin-right: 4px; }"
        )

        self.rufin_view = RufinView(self.rufin_git_mgr, self.config)
        self.igloo_view = IglooView(self.igloo_git_mgr, self.config)

        self.project_tabs.addTab(self.rufin_view, "Rufin")
        self.project_tabs.addTab(self.igloo_view, "Igloo")
        self.project_tabs.currentChanged.connect(self._on_project_changed)

        root_layout.addWidget(self.project_tabs)

        # Status Bar
        self.status_bar = QStatusBar()
        self.setStatusBar(self.status_bar)
        self.status_bar.showMessage("Ready")

    def _wire_signals(self):
        self.runner.output_received.connect(self._on_runner_output)
        self.runner.started.connect(lambda: self.status_bar.showMessage("Running task...", 3000))
        self.runner.finished.connect(self._on_runner_finished)

    def _on_runner_output(self, text: str):
        # Route just output to Igloo commit tab terminal
        self.igloo_view.commit_tab.terminal.append_output(text)

    def _on_runner_finished(self, exit_code: int):
        status = "success" if exit_code == 0 else "failed"
        self.igloo_view.commit_tab.terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        self.status_bar.showMessage(f"Task completed (Exit {exit_code}).", 3000)

    def run_igloo_just(self, command: str):
        if self.runner.is_running:
            QMessageBox.warning(self, "Running", "A command is already executing.")
            return
        self.igloo_view.commit_tab.terminal.clear_terminal()
        self.igloo_view.commit_tab.terminal.set_status(f"just {command}", "running")
        self.status_bar.showMessage(f"Executing: just {command}...")
        self.runner.start("just", [command], cwd=self.igloo_git_mgr.repo_path)

    @property
    def active_git_mgr(self) -> GitManager:
        if self.project_tabs.currentIndex() == 1:
            return self.igloo_git_mgr
        return self.rufin_git_mgr

    @property
    def active_repo_name(self) -> str:
        return "Igloo" if self.project_tabs.currentIndex() == 1 else "Rufin"

    def _on_project_changed(self, index: int):
        self.update_repo_display()
        self.refresh_active_repo()

    def open_settings(self):
        dlg = SettingsDialog(self.config, self)
        btn = self.top_bar.btn_settings
        btn_pos = btn.mapToGlobal(btn.rect().bottomRight())
        dlg.move(max(10, btn_pos.x() - dlg.width()), btn_pos.y() + 4)
        dlg.exec()

        # Update paths if changed
        self.rufin_git_mgr.repo_path = self.config.rufin_repo_path
        self.igloo_git_mgr.repo_path = self.config.igloo_repo_path

        self.rufin_view.config = self.config
        self.igloo_view.config = self.config
        self.rufin_view.publish_tab.diff_viewer.config = self.config
        self.igloo_view.commit_tab.diff_viewer.config = self.config

        self.update_repo_display()
        self.refresh_active_repo()
        self.status_bar.showMessage("Preferences updated.", 2000)

    def update_repo_display(self):
        mgr = self.active_git_mgr
        name = self.active_repo_name
        is_igloo = bool(self.project_tabs.currentIndex() == 1)

        self.top_bar.repo_badge.setText(name)
        self.top_bar.repo_badge.setToolTip(f"Repository path: {mgr.repo_path}")
        self.top_bar.igloo_tools_widget.setVisible(is_igloo)

        if not mgr.is_git_repo():
            self.top_bar.branch_badge.setText("not a git repo")
            self.top_bar.branch_badge.setStyleSheet(
                "color: #f85149; background-color: #21262d; border-radius: 4px; padding: 2px 8px;"
            )
            return

        branch = mgr.get_current_branch()
        self.top_bar.branch_badge.setText(f"branch: {branch}")
        self.top_bar.branch_badge.setStyleSheet(
            "background-color: #21262d; color: #7ee787; border: 1px solid #30363d; border-radius: 4px; padding: 2px 8px; font-weight: 600; font-size: 11px;"
        )

    def refresh_active_repo(self):
        self.update_repo_display()
        if self.project_tabs.currentIndex() == 1:
            self.igloo_view.refresh()
        else:
            self.rufin_view.refresh()
        self.status_bar.showMessage(f"Refreshed {self.active_repo_name} repository state.", 3000)

    def _do_fetch(self):
        mgr = self.active_git_mgr
        name = self.active_repo_name
        self.status_bar.showMessage(f"Fetching {name} from origin...")
        self.top_bar.btn_fetch.setEnabled(False)
        code, msg = self.active_git_mgr.fetch()
        self.top_bar.btn_fetch.setEnabled(True)
        if code == 0:
            self.status_bar.showMessage(f"Fetch completed for {name}.", 4000)
            self.refresh_active_repo()
        else:
            QMessageBox.warning(self, "Fetch Failed", f"Git fetch failed for {name}:\n{msg}")
            self.status_bar.showMessage("Fetch failed.", 4000)

    def _do_pull(self):
        mgr = self.active_git_mgr
        name = self.active_repo_name
        self.status_bar.showMessage(f"Pulling {name} latest changes...")
        self.top_bar.btn_pull.setEnabled(False)
        code, msg = self.active_git_mgr.pull()
        self.top_bar.btn_pull.setEnabled(True)
        if code == 0:
            self.status_bar.showMessage(f"Pull completed for {name}.", 4000)
            self.refresh_active_repo()
        else:
            QMessageBox.warning(self, "Pull Failed", f"Git pull failed for {name}:\n{msg}")
            self.status_bar.showMessage("Pull failed.", 4000)


def main():
    app = QApplication(sys.argv)
    app.setStyleSheet(DARK_STYLESHEET)

    window = MainWindow()
    window.show()
    sys.exit(app.exec())


if __name__ == "__main__":
    main()
