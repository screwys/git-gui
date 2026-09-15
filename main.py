#!/usr/bin/env python3
import sys
import os
import subprocess
from typing import List
from PyQt6.QtCore import Qt, QPointF
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
    QFileDialog,
    QStatusBar,
    QMessageBox,
    QFrame,
)

from core.config import AppConfig, ConfigManager
from core.git_manager import GitManager
from ui.style import DARK_STYLESHEET
from ui.tabs.publish_pr_tab import PublishPrTab
from ui.tabs.release_tab import ReleaseTab
from ui.dialogs.settings_dialog import SettingsDialog


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
            painter.drawRoundedRect(
                cx - side, cy - side, side * 2, side * 2, 1.0, 1.0
            )


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
        self.app_title = QLabel("Rufin")
        self.app_title.setStyleSheet("font-weight: 800; font-size: 13px; color: #58a6ff; letter-spacing: 0.5px;")
        layout.addWidget(self.app_title)

        # Repo selector button
        self.repo_btn = QPushButton(os.path.basename(self.main_window.repo_path))
        self.repo_btn.setFixedHeight(24)
        self.repo_btn.setToolTip(f"Repository path: {self.main_window.repo_path}\nClick to switch repo")
        self.repo_btn.clicked.connect(self.main_window._choose_repo)
        layout.addWidget(self.repo_btn)

        # Branch indicator badge
        self.branch_badge = QLabel("branch: ...")
        self.branch_badge.setStyleSheet(
            "background-color: #21262d; color: #7ee787; border: 1px solid #30363d; border-radius: 4px; padding: 2px 8px; font-weight: 600; font-size: 11px;"
        )
        layout.addWidget(self.branch_badge)

        # Draggable middle space
        layout.addStretch()

        # Git Actions
        self.btn_refresh = QPushButton("Refresh")
        self.btn_refresh.setFixedHeight(24)
        self.btn_refresh.setToolTip("Reload changes, diffs, and branches")
        self.btn_refresh.clicked.connect(self.main_window.refresh_all)
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
        self.btn_settings.setToolTip("Configure script locations and default options")
        self.btn_settings.clicked.connect(self.main_window.open_settings)
        layout.addWidget(self.btn_settings)

        # Separator before window controls
        sep = QFrame()
        sep.setFrameShape(QFrame.Shape.VLine)
        sep.setFrameShadow(QFrame.Shadow.Sunken)
        sep.setStyleSheet("color: #30363d; max-height: 16px;")
        layout.addWidget(sep)

        # Follow GTK / Niri window button conventions
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


class MainWindow(QMainWindow):
    def __init__(self, default_repo: str = "/var/home/screwy/Projects/Rufin"):
        super().__init__()
        self.config: AppConfig = ConfigManager.load()
        self.repo_path = default_repo or self.config.repo_path
        self.git_mgr = GitManager(self.repo_path)
        self._init_ui()
        self.update_repo_display()

    def _init_ui(self):
        self.setWindowTitle("Rufin")
        self.resize(1200, 780)
        self.setMinimumSize(880, 560)

        central = QWidget()
        self.setCentralWidget(central)
        root_layout = QVBoxLayout(central)
        root_layout.setContentsMargins(6, 6, 6, 6)
        root_layout.setSpacing(6)

        # Custom draggable top bar with window controls
        self.top_bar = DraggableTopBar(self)
        root_layout.addWidget(self.top_bar)

        # Tab Widget
        self.tabs = QTabWidget()
        self.publish_tab = PublishPrTab(self.git_mgr, self.config)
        self.release_tab = ReleaseTab(self.git_mgr, self.config)

        self.tabs.addTab(self.publish_tab, "Publish PR")
        self.tabs.addTab(self.release_tab, "Release")

        root_layout.addWidget(self.tabs)

        # Status Bar
        self.status_bar = QStatusBar()
        self.setStatusBar(self.status_bar)
        self.status_bar.showMessage("Ready")

    def add_workflow_tab(self, name: str, widget: QWidget):
        """Allows trivially extending the GUI with new custom workflow tabs."""
        self.tabs.addTab(widget, name)

    def open_settings(self):
        dlg = SettingsDialog(self.config, self.repo_path, self)
        btn = self.top_bar.btn_settings
        btn_pos = btn.mapToGlobal(btn.rect().bottomRight())
        dlg.move(max(10, btn_pos.x() - dlg.width()), btn_pos.y() + 4)
        dlg.exec()
        self.publish_tab.config = self.config
        self.release_tab.config = self.config
        self.publish_tab.diff_viewer.config = self.config
        self.status_bar.showMessage("Preferences updated.", 2000)

    def _choose_repo(self):
        selected = QFileDialog.getExistingDirectory(self, "Select Git Repository", self.repo_path)
        if selected:
            self.repo_path = selected
            self.git_mgr.repo_path = selected
            self.top_bar.repo_btn.setText(os.path.basename(selected))
            self.top_bar.repo_btn.setToolTip(f"Repository path: {selected}")
            self.refresh_all()

    def update_repo_display(self):
        if not self.git_mgr.is_git_repo():
            self.top_bar.branch_badge.setText("not a git repo")
            self.top_bar.branch_badge.setStyleSheet(
                "color: #f85149; background-color: #21262d; border-radius: 4px; padding: 2px 8px;"
            )
            return

        branch = self.git_mgr.get_current_branch()
        self.top_bar.branch_badge.setText(f"branch: {branch}")
        self.top_bar.branch_badge.setStyleSheet(
            "background-color: #21262d; color: #7ee787; border: 1px solid #30363d; border-radius: 4px; padding: 2px 8px; font-weight: 600; font-size: 11px;"
        )

    def refresh_all(self):
        self.update_repo_display()
        self.publish_tab.refresh()
        self.release_tab.refresh()
        self.status_bar.showMessage("Refreshed repository state.", 3000)

    def _do_fetch(self):
        self.status_bar.showMessage("Fetching from origin...")
        self.top_bar.btn_fetch.setEnabled(False)
        code, msg = self.git_mgr.fetch()
        self.top_bar.btn_fetch.setEnabled(True)
        if code == 0:
            self.status_bar.showMessage("Fetch completed.", 4000)
            self.refresh_all()
        else:
            QMessageBox.warning(self, "Fetch Failed", f"Git fetch failed:\n{msg}")
            self.status_bar.showMessage("Fetch failed.", 4000)

    def _do_pull(self):
        self.status_bar.showMessage("Pulling latest changes...")
        self.top_bar.btn_pull.setEnabled(False)
        code, msg = self.git_mgr.pull()
        self.top_bar.btn_pull.setEnabled(True)
        if code == 0:
            self.status_bar.showMessage("Pull completed.", 4000)
            self.refresh_all()
        else:
            QMessageBox.warning(self, "Pull Failed", f"Git pull failed:\n{msg}")
            self.status_bar.showMessage("Pull failed.", 4000)


def main():
    app = QApplication(sys.argv)
    app.setStyleSheet(DARK_STYLESHEET)

    repo_dir = sys.argv[1] if len(sys.argv) > 1 else "/var/home/screwy/Projects/Rufin"
    window = MainWindow(default_repo=repo_dir)
    window.show()
    sys.exit(app.exec())


if __name__ == "__main__":
    main()
