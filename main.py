#!/usr/bin/env python3
import sys
import os
import subprocess
from typing import List
from PyQt6.QtCore import Qt, QPointF, QSize, QRectF, QTimer, QThread
from PyQt6.QtGui import QMouseEvent, QPainter, QColor, QPen, QIcon
from PyQt6.QtWidgets import (
    QApplication,
    QMainWindow,
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QTabWidget,
    QTabBar,
    QStackedWidget,
    QFileDialog,
    QPushButton,
    QAbstractButton,
    QLabel,
    QStatusBar,
    QMessageBox,
    QFrame,
    QSystemTrayIcon,
    QMenu,
    QDockWidget,
)

from core.config import AppConfig, ConfigManager
from core.state import StateManager
from core.git_manager import GitManager
from core.runner import ProcessRunner
from core.instance import AppInstance
from ui.components.terminal_view import TerminalWidget
from ui.style import DARK_STYLESHEET
from ui.tabs.publish_pr_tab import PublishPrTab
from ui.tabs.release_tab import ReleaseTab
from ui.tabs.commit_tab import CommitTab
from ui.tabs.changes_tab import ChangesTab
from ui.tabs.igloo_release_tab import IglooReleaseTab
from ui.tabs.generic_release_tab import GenericReleaseTab
from ui.tabs.packages_tab import PackagesTab
from ui.dialogs.settings_dialog import SettingsDialog
from ui.components.icons import create_restart_icon, create_android_icon
from ui.components.rpm_package_catalog import RpmPackageCatalog


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
            painter.drawRoundedRect(QRectF(cx - side, cy - side, side * 2, side * 2), 1.0, 1.0)


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

        self.btn_rufin_android = QPushButton()
        self.btn_rufin_android.setIcon(create_android_icon())
        self.btn_rufin_android.setIconSize(QSize(16, 16))
        self.btn_rufin_android.setFixedSize(28, 24)
        self.btn_rufin_android.setToolTip("Build & Install Android App (just build apk-dev, just install android)")
        self.btn_rufin_android.clicked.connect(lambda: self.main_window._run_command(
            "bash", ["-c", "just build apk-dev && just install android"]))
        self.btn_rufin_android.setVisible(False)
        layout.addWidget(self.btn_rufin_android)

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

        self.btn_sync = QPushButton("Sync")
        self.btn_sync.setFixedHeight(24)
        self.btn_sync.setToolTip("publish-pr.sh --sync: bring main up to origin/main, merging local edits")
        self.btn_sync.clicked.connect(self.main_window._do_sync)
        self.btn_sync.setVisible(False)
        layout.addWidget(self.btn_sync)

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
        self.changes_tab = ChangesTab(self.git_mgr, self.config)
        self.tabs.addTab(self.changes_tab, "Changes")

        saved_tab = StateManager.get("rufin_subtab", "Publish PR")
        for index in range(self.tabs.count()):
            if self.tabs.tabText(index) == saved_tab:
                self.tabs.setCurrentIndex(index)
        self.tabs.currentChanged.connect(lambda idx: StateManager.update(rufin_subtab=self.tabs.tabText(idx)))

        layout.addWidget(self.tabs)

    def refresh(self):
        self.publish_tab.refresh()
        self.release_tab.refresh()
        if self.tabs.currentWidget() is self.changes_tab:
            self.changes_tab.refresh()


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
        self.commit_tab = CommitTab(self.git_mgr, self.config)
        self.release_tab = IglooReleaseTab(self.git_mgr, self.config)

        self.tabs.addTab(self.commit_tab, "Commit & Push")
        self.tabs.addTab(self.release_tab, "Release")
        self.changes_tab = ChangesTab(self.git_mgr, self.config)
        self.tabs.addTab(self.changes_tab, "Changes")

        saved_tab = StateManager.get("igloo_subtab", "Commit & Push")
        for index in range(self.tabs.count()):
            if self.tabs.tabText(index) == saved_tab:
                self.tabs.setCurrentIndex(index)
        self.tabs.currentChanged.connect(lambda idx: StateManager.update(igloo_subtab=self.tabs.tabText(idx)))

        layout.addWidget(self.tabs)

    def refresh(self):
        self.commit_tab.refresh()
        self.release_tab.refresh()
        if self.tabs.currentWidget() is self.changes_tab:
            self.changes_tab.refresh()


class ProjectView(QWidget):
    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None, *, nocblue=False, rpm_catalog=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        self.tabs = QTabWidget()
        self.commit_tab = CommitTab(git_mgr, config)
        self.tabs.addTab(self.commit_tab, "Commit & Push")
        self.packages_tab = PackagesTab(git_mgr, config, nocblue=nocblue, rpm_catalog=rpm_catalog)
        self.packages_tab.changed.connect(self._fill_package_commit)
        self.tabs.addTab(self.packages_tab, "Packages")
        self.release_tab = GenericReleaseTab(git_mgr)
        self.tabs.addTab(self.release_tab, "Release")
        self.changes_tab = ChangesTab(self.git_mgr, self.config)
        self.tabs.addTab(self.changes_tab, "Changes")
        self._package_subject = ""
        layout.addWidget(self.tabs)
        self._update_package_tab()
        state_key = f"project_subtab:{os.path.realpath(git_mgr.repo_path)}"
        saved_tab = StateManager.get(state_key, "Commit & Push")
        for index in range(self.tabs.count()):
            if self.tabs.tabText(index) == saved_tab and self.tabs.isTabVisible(index):
                self.tabs.setCurrentIndex(index)
        self.tabs.currentChanged.connect(
            lambda index: StateManager.update(**{state_key: self.tabs.tabText(index)}))

    def _update_package_tab(self):
        enabled = (self.packages_tab.nocblue or
                   os.path.realpath(self.git_mgr.repo_path) in self.config.package_project_paths)
        self.tabs.setTabVisible(self.tabs.indexOf(self.packages_tab), enabled)

    def _fill_package_commit(self, subject):
        edit = self.commit_tab.summary_input
        if not edit.text().strip() or edit.text() == self._package_subject:
            edit.setText(subject)
        self._package_subject = subject
        self.commit_tab.refresh_working_tree()

    def refresh(self):
        self.commit_tab.refresh()
        self._update_package_tab()
        if self.tabs.isTabVisible(self.tabs.indexOf(self.packages_tab)):
            self.packages_tab.refresh()
        self.release_tab.refresh()
        if self.tabs.currentWidget() is self.changes_tab:
            self.changes_tab.refresh()


class MainWindow(QMainWindow):
    def __init__(self):
        super().__init__()
        self.config: AppConfig = ConfigManager.load()
        self.rufin_git_mgr = GitManager(self.config.rufin_repo_path)
        self.igloo_git_mgr = GitManager(self.config.igloo_repo_path)
        self.nocblue_git_mgr = GitManager(self.config.nocblue_repo_path)
        self.rpm_catalog = RpmPackageCatalog(self)
        self.runner = ProcessRunner(self)
        self._init_ui()
        self._wire_signals()
        self.update_repo_display()
        self._quitting = False
        self.tray = QSystemTrayIcon(self.windowIcon(), self)
        self.tray.setToolTip("Git GUI")
        menu = QMenu(self)
        menu.addAction("Open Git GUI", self.present)
        menu.addSeparator()
        menu.addAction("Quit", self.quit_app)
        self.tray.setContextMenu(menu)
        self.tray.activated.connect(lambda reason: self.present() if reason in (
            QSystemTrayIcon.ActivationReason.Trigger, QSystemTrayIcon.ActivationReason.DoubleClick) else None)
        self.tray.messageClicked.connect(self.present)
        self.tray.show()
        self.quit_timer = QTimer(self)
        self.quit_timer.setInterval(100)
        self.quit_timer.timeout.connect(self._finish_quit)
        self.repo_refresh_timer = QTimer(self)
        self.repo_refresh_timer.setInterval(30000)
        self.repo_refresh_timer.timeout.connect(self._refresh_visible_working_tree)
        self.repo_refresh_timer.start()

    def present(self):
        self._quitting = False
        self.quit_timer.stop()
        self.showNormal() if self.isMinimized() else self.show()
        self.raise_()
        self.activateWindow()
        self.repo_refresh_timer.start()
        self._set_monitor_interval(False)
        self.refresh_active_repo()

    def _set_monitor_interval(self, background):
        checks = self.rufin_view.publish_tab.checks_view
        checks.poll_timer.setInterval(60000 if background else 12000)
        if checks.pr_number and checks.pr_state == "OPEN":
            checks.poll_timer.start()
        for index in range(self.project_tabs.count()):
            jobs = self.project_tabs.widget(index).release_tab.jobs_view
            jobs.poll_timer.setInterval(60000 if background else 15000)
            jobs.poll_timer.start()

    def closeEvent(self, event):
        event.ignore()
        if self.config.close_to_tray and QSystemTrayIcon.isSystemTrayAvailable():
            self.hide()
            self.repo_refresh_timer.stop()
            self._set_monitor_interval(True)
        else:
            self.quit_app()

    def quit_app(self):
        self._quitting = True
        self.hide()
        self.repo_refresh_timer.stop()
        # Read-only GitHub workers finish naturally before their Qt owners are destroyed.
        self.rufin_view.publish_tab.checks_view.poll_timer.stop()
        for index in range(self.project_tabs.count()):
            self.project_tabs.widget(index).release_tab.jobs_view.poll_timer.stop()
        self.quit_timer.start()
        self._finish_quit()

    def _finish_quit(self):
        if self._quitting and not any(worker.isRunning() for worker in self.findChildren(QThread)):
            self.tray.hide()
            QApplication.instance().quit()

    def _init_ui(self):
        self.setWindowTitle("Git GUI")
        self.setWindowIcon(QIcon(os.path.join(os.path.dirname(__file__), "git-gui.svg")))
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

        # Project selector and add button share a row above the project views.
        self.project_tabs = QStackedWidget()
        self.project_bar = QTabBar()
        self.project_bar.setExpanding(False)
        self.project_bar.setStyleSheet(
            "QTabBar::tab { font-weight: 600; font-size: 12px; padding: 6px 16px; margin-right: 4px; }"
        )

        self.rufin_view = RufinView(self.rufin_git_mgr, self.config)
        self.igloo_view = IglooView(self.igloo_git_mgr, self.config)
        self.nocblue_view = ProjectView(self.nocblue_git_mgr, self.config, nocblue=True, rpm_catalog=self.rpm_catalog)

        self.project_tabs.addWidget(self.rufin_view)
        self.project_tabs.addWidget(self.igloo_view)
        self.project_tabs.addWidget(self.nocblue_view)
        self.project_bar.addTab("Rufin")
        self.project_bar.addTab("Igloo")
        self.project_bar.addTab("nocblue")
        self.btn_add_project = QPushButton("+")
        self.btn_add_project.setFixedSize(24, 24)
        self.btn_add_project.setStyleSheet("QPushButton { padding: 0; font-size: 18px; }")
        self.btn_add_project.setToolTip("Add project")
        self.btn_add_project.setAccessibleName("Add project")
        self.btn_add_project.clicked.connect(self.add_project)
        project_header = QHBoxLayout()
        project_header.setContentsMargins(0, 0, 0, 0)
        project_header.setSpacing(4)
        project_header.addWidget(self.project_bar)
        project_header.addWidget(self.btn_add_project)
        project_header.addStretch()
        root_layout.addLayout(project_header)
        self.project_bar.currentChanged.connect(self.project_tabs.setCurrentIndex)
        self.project_tabs.currentChanged.connect(self.project_bar.setCurrentIndex)
        for path in self.config.project_paths:
            self._add_project_view(path)
        saved_proj = StateManager.get("selected_project", "Rufin")
        for index in range(self.project_tabs.count()):
            view = self.project_tabs.widget(index)
            project = self.project_bar.tabText(index) if index < 2 else view.git_mgr.repo_path
            if saved_proj == project or (view is self.nocblue_view and saved_proj == "nocblue"):
                self.project_tabs.setCurrentIndex(index)
                break
        self.project_tabs.currentChanged.connect(self._on_project_changed)

        root_layout.addWidget(self.project_tabs)

        # Status Bar
        self.status_bar = QStatusBar()
        self.setStatusBar(self.status_bar)
        self.status_bar.showMessage("Ready")
        self.git_terminal = TerminalWidget()
        self.git_dock = QDockWidget("Git commands", self)
        self.git_dock.setWidget(self.git_terminal)
        self.addDockWidget(Qt.DockWidgetArea.BottomDockWidgetArea, self.git_dock)
        self.git_dock.hide()
        self.git_terminal.stop_requested.connect(self.runner.stop)
        self.git_terminal.bind_runner(self.runner, self.rufin_git_mgr.repo_path, "git-tools")

    def _wire_signals(self):
        self.runner.restored.connect(
            lambda record: self.git_dock.show()
            if record["running"] else None)
        self.runner.output_received.connect(self._on_runner_output)
        self.runner.started.connect(lambda: self.status_bar.showMessage("Running task...", 3000))
        self.runner.finished.connect(self._on_runner_finished)

    def _on_runner_output(self, text: str):
        self.git_terminal.append_output(text)

    def _on_runner_finished(self, exit_code: int):
        status = "success" if exit_code == 0 else "failed"
        self.git_terminal.set_status("Completed" if exit_code == 0 else f"Exit {exit_code}", status)
        if self.isVisible():
            self.refresh_active_repo()
        self.status_bar.showMessage(f"Task completed (Exit {exit_code}).", 3000)

    def run_igloo_just(self, command: str):
        tab = self.igloo_view.commit_tab
        if tab.runner.is_running:
            QMessageBox.warning(self, "Running", "An Igloo command is already executing.")
            return
        self.project_tabs.setCurrentWidget(self.igloo_view)
        self.igloo_view.tabs.setCurrentWidget(tab)
        tab.terminal.clear_terminal()
        tab.runner.start("just", [command], cwd=self.igloo_git_mgr.repo_path)

    @property
    def active_git_mgr(self) -> GitManager:
        return self.project_tabs.currentWidget().git_mgr

    @property
    def active_repo_name(self) -> str:
        return self.project_bar.tabText(self.project_tabs.currentIndex())

    def _add_project_view(self, path: str):
        for index in range(self.project_tabs.count()):
            view = self.project_tabs.widget(index)
            if os.path.realpath(view.git_mgr.repo_path) == os.path.realpath(path):
                return view
        view = ProjectView(GitManager(path), self.config, rpm_catalog=self.rpm_catalog)
        index = self.project_tabs.addWidget(view)
        self.project_bar.addTab(os.path.basename(os.path.normpath(path)))
        self.project_bar.setTabToolTip(index, path)
        return view

    def add_project(self):
        path = QFileDialog.getExistingDirectory(
            self, "Add Project", os.path.dirname(self.active_git_mgr.repo_path))
        if not path:
            return
        mgr = GitManager(path)
        code, root, _ = mgr._run_git(["rev-parse", "--show-toplevel"])
        if code != 0:
            QMessageBox.warning(self, "Not a Git Repository", "Select a folder containing a Git working tree.")
            return
        path = root.strip()
        for index in range(self.project_tabs.count()):
            view = self.project_tabs.widget(index)
            if os.path.realpath(view.git_mgr.repo_path) == os.path.realpath(path):
                self.project_tabs.setCurrentIndex(index)
                return
        self.config.project_paths.append(path)
        ConfigManager.save(self.config)
        view = self._add_project_view(path)
        self.project_tabs.setCurrentWidget(view)

    def _on_project_changed(self, index: int):
        proj_name = self.active_repo_name if index < 2 else self.active_git_mgr.repo_path
        StateManager.update(selected_project=proj_name)
        self.update_repo_display()
        self.refresh_active_repo()

    def open_settings(self):
        view = self.project_tabs.currentWidget()
        project_path = view.git_mgr.repo_path if isinstance(view, ProjectView) and view is not self.nocblue_view else None
        dlg = SettingsDialog(self.config, self, project_path=project_path)
        btn = self.top_bar.btn_settings
        btn_pos = btn.mapToGlobal(btn.rect().bottomRight())
        dlg.move(max(10, btn_pos.x() - dlg.width()), btn_pos.y() + 4)
        dlg.exec()

        # Update paths if changed
        self.rufin_git_mgr.repo_path = self.config.rufin_repo_path
        self.igloo_git_mgr.repo_path = self.config.igloo_repo_path
        self.nocblue_git_mgr.repo_path = self.config.nocblue_repo_path

        self.rufin_view.config = self.config
        self.igloo_view.config = self.config
        self.rufin_view.publish_tab.diff_viewer.config = self.config
        for index in range(1, self.project_tabs.count()):
            self.project_tabs.widget(index).commit_tab.diff_viewer.config = self.config
        self.rufin_view.release_tab.jobs_view.repo_path = self.config.rufin_repo_path
        self.igloo_view.release_tab.jobs_view.repo_path = self.config.igloo_repo_path
        self.rufin_view.publish_tab.runner.program = self.config.resolve_publish_script()
        self.rufin_view.release_tab.runner.program = self.config.resolve_release_script()

        self.update_repo_display()
        self.refresh_active_repo()
        self.status_bar.showMessage("Preferences updated.", 2000)

    def update_repo_display(self):
        mgr = self.active_git_mgr
        name = self.active_repo_name
        is_rufin = self.project_tabs.currentWidget() is self.rufin_view
        is_igloo = self.project_tabs.currentWidget() is self.igloo_view

        self.top_bar.repo_badge.setText(name)
        self.top_bar.repo_badge.setToolTip(f"Repository path: {mgr.repo_path}")
        self.top_bar.igloo_tools_widget.setVisible(is_igloo)
        self.top_bar.btn_rufin_android.setVisible(is_rufin)
        self.top_bar.btn_sync.setVisible(is_rufin)
        self.top_bar.btn_fetch.setVisible(not is_rufin)
        self.top_bar.btn_pull.setVisible(not is_rufin)

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
        view = self.project_tabs.currentWidget()
        view.refresh()
        for index in range(view.tabs.count()):
            tab = view.tabs.widget(index)
            if hasattr(tab, "runner"):
                tab.runner.attach(self.active_git_mgr.repo_path, tab.runner.key, tab.runner.program)
        if hasattr(view, "release_tab"):
            view.release_tab.jobs_view.sync_remote_status()
        self.status_bar.showMessage(f"Refreshed {self.active_repo_name} repository state.", 3000)

    def _refresh_visible_working_tree(self):
        if not self.isVisible():
            return
        view = self.project_tabs.currentWidget()
        tab = view.tabs.currentWidget()
        if hasattr(tab, "refresh_working_tree"):
            runners = [self.runner] + [view.tabs.widget(index).runner for index in range(view.tabs.count())
                                      if hasattr(view.tabs.widget(index), "runner")]
            if any(runner.is_running for runner in runners):
                return
            self.update_repo_display()
            tab.refresh_working_tree()

    def _do_fetch(self):
        self._run_command("git", ["fetch", "--prune", "origin"])

    def _do_pull(self):
        self._run_command("git", ["pull", "--ff-only"])

    def _do_sync(self):
        self._run_command(self.config.resolve_publish_script(), ["--sync"])

    def _run_command(self, program, args):
        if self.runner.is_running:
            self.git_dock.show()
            return
        self.git_terminal.repo_path = self.active_git_mgr.repo_path
        self.git_terminal.clear_terminal()
        self.git_terminal.set_status("Running", "running")
        self.git_dock.show()
        self.runner.start(program, args, cwd=self.active_git_mgr.repo_path)


def main():
    app = QApplication(sys.argv)
    app.setApplicationName("Git GUI")
    app.setDesktopFileName("git-gui")
    app.setQuitOnLastWindowClosed(False)
    instance = AppInstance()
    if not instance.acquire():
        return
    app.setStyleSheet(DARK_STYLESHEET)

    window = MainWindow()
    instance.on_activation(window.present)
    window.show()
    sys.exit(app.exec())


if __name__ == "__main__":
    main()
