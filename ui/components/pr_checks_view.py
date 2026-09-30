import json
import subprocess
from datetime import datetime, timezone
from typing import Optional
from PyQt6.QtCore import Qt, QTimer, QThread, pyqtSignal, QUrl
from PyQt6.QtGui import QDesktopServices, QColor
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QLabel,
    QPushButton,
    QTableWidget,
    QTableWidgetItem,
    QHeaderView,
    QProgressBar,
    QSizePolicy,
)
from core.notifications import send_desktop_notification
from core.state import StateManager


def parse_iso_timestamp(ts_str: Optional[str]) -> Optional[datetime]:
    if not ts_str:
        return None
    try:
        return datetime.fromisoformat(ts_str.replace("Z", "+00:00"))
    except Exception:
        return None


def format_check_duration(started_at: Optional[str], completed_at: Optional[str], conclusion: Optional[str]) -> str:
    if conclusion == "SKIPPED":
        return "—"

    t_start = parse_iso_timestamp(started_at)
    t_end = parse_iso_timestamp(completed_at)

    if t_start and t_end:
        secs = max(0, int((t_end - t_start).total_seconds()))
        if secs < 60:
            return f"{secs}s"
        mins = secs // 60
        rem_secs = secs % 60
        if mins < 60:
            return f"{mins}m {rem_secs}s"
        hours = mins // 60
        rem_mins = mins % 60
        return f"{hours}h {rem_mins}m"
    elif t_start:
        now = datetime.now(timezone.utc)
        secs = max(0, int((now - t_start).total_seconds()))
        if secs < 60:
            return f"{secs}s"
        mins = secs // 60
        rem_secs = secs % 60
        return f"{mins}m {rem_secs}s"
    return "—"


class ChecksFetchWorker(QThread):
    checks_fetched = pyqtSignal(dict)
    error = pyqtSignal(str)

    def __init__(self, repo_path: str, pr_number: str, parent=None):
        super().__init__(parent)
        self.repo_path = repo_path
        self.pr_number = pr_number

    def run(self):
        try:
            cmd = [
                "gh",
                "pr",
                "view",
                str(self.pr_number),
                "--json",
                "number,title,url,state,statusCheckRollup",
            ]
            res = subprocess.run(
                cmd,
                cwd=self.repo_path,
                capture_output=True,
                text=True,
                timeout=20,
            )
            if res.returncode == 0 and res.stdout.strip():
                data = json.loads(res.stdout)
                self.checks_fetched.emit(data)
            else:
                self.error.emit(res.stderr.strip() or "No data returned")
        except Exception as e:
            self.error.emit(str(e))


class PrChecksWidget(QWidget):
    """
    Displays real-time GitHub Actions checks for a Rufin PR.
    Polls check status periodically and sends desktop notifications if any check fails.
    Stateful: persists tracked PR and updates state.
    """

    checks_updated = pyqtSignal(dict)

    def __init__(self, repo_path: str = "", parent=None):
        super().__init__(parent)
        self.repo_path = repo_path
        self.pr_number: Optional[str] = None
        self.pr_title: str = ""
        self.pr_url: str = ""
        self.pr_state: str = ""
        self.notified_failed_checks = set()
        self._last_result = None
        self._worker: Optional[ChecksFetchWorker] = None
        self._generation = 0

        self.poll_timer = QTimer(self)
        self.poll_timer.setInterval(12000)  # poll every 12s
        self.poll_timer.timeout.connect(self.refresh_checks)

        self._init_ui()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(6, 6, 6, 6)
        layout.setSpacing(6)

        # Header summary bar
        header_bar = QHBoxLayout()
        header_bar.setSpacing(8)

        self.lbl_title = QLabel("No active PR detected")
        self.lbl_title.setSizePolicy(QSizePolicy.Policy.Ignored, QSizePolicy.Policy.Fixed)
        self.lbl_title.setStyleSheet("color: #e6edf3; font-weight: 600; font-size: 12px;")
        header_bar.addWidget(self.lbl_title, 1)

        self.status_pill = QLabel("Idle")
        self.status_pill.setStyleSheet(
            "background-color: #21262d; color: #8b949e; border: 1px solid #30363d; "
            "border-radius: 4px; padding: 2px 8px; font-size: 11px; font-weight: 600;"
        )
        header_bar.addWidget(self.status_pill)

        self.btn_refresh = QPushButton("Refresh")
        self.btn_refresh.setFixedHeight(24)
        self.btn_refresh.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_refresh.clicked.connect(self.refresh_checks)
        header_bar.addWidget(self.btn_refresh)

        self.btn_open_pr = QPushButton("Open PR")
        self.btn_open_pr.setFixedHeight(24)
        self.btn_open_pr.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_open_pr.clicked.connect(self._open_pr)
        self.btn_open_pr.setEnabled(False)
        header_bar.addWidget(self.btn_open_pr)

        layout.addLayout(header_bar)

        # Progress bar
        self.progress_bar = QProgressBar()
        self.progress_bar.setFixedHeight(4)
        self.progress_bar.setTextVisible(False)
        self.progress_bar.setStyleSheet(
            "QProgressBar { background-color: #21262d; border: none; border-radius: 2px; } "
            "QProgressBar::chunk { background-color: #388bfd; border-radius: 2px; }"
        )
        layout.addWidget(self.progress_bar)

        # Table of checks
        self.table = QTableWidget()
        self.table.setColumnCount(4)
        self.table.setHorizontalHeaderLabels(["Check / Workflow", "Status", "Duration", "Link"])
        self.table.horizontalHeader().setSectionResizeMode(0, QHeaderView.ResizeMode.Stretch)
        self.table.horizontalHeader().setSectionResizeMode(1, QHeaderView.ResizeMode.ResizeToContents)
        self.table.horizontalHeader().setSectionResizeMode(2, QHeaderView.ResizeMode.ResizeToContents)
        self.table.horizontalHeader().setSectionResizeMode(3, QHeaderView.ResizeMode.Fixed)
        self.table.setColumnWidth(3, 85)
        self.table.verticalHeader().setVisible(False)
        self.table.setAlternatingRowColors(True)
        self.table.setShowGrid(False)
        self.table.setSelectionBehavior(QTableWidget.SelectionBehavior.SelectRows)
        self.table.setStyleSheet(
            "QTableWidget { background-color: #0b0e14; border: 1px solid #30363d; border-radius: 6px; font-size: 11px; } "
            "QTableWidget::item { padding: 4px 6px; } "
            "QHeaderView::section { background-color: #161b22; color: #8b949e; border: none; padding: 4px; font-weight: 600; }"
        )
        layout.addWidget(self.table)

    def set_pr(self, pr_number: str, title: str = "", url: str = "", state: str = ""):
        self._generation += 1
        if self.pr_number != str(pr_number):
            self.notified_failed_checks.clear()
            self._last_result = None

        self.pr_number = str(pr_number)
        self.pr_title = title
        self.pr_url = url
        self.pr_state = state

        state_suffix = f" ({state.capitalize()})" if state else ""
        self.lbl_title.setText(f"PR #{self.pr_number}{state_suffix}: {self.pr_title}" if self.pr_title else f"PR #{self.pr_number}{state_suffix}")
        self.lbl_title.setToolTip(self.lbl_title.text())
        self.btn_open_pr.setEnabled(bool(self.pr_url))
        self.refresh_checks()
        if not self.poll_timer.isActive() and (not state or state.upper() == "OPEN"):
            self.poll_timer.start()

    def _open_pr(self):
        if self.pr_url:
            QDesktopServices.openUrl(QUrl(self.pr_url))

    def refresh_checks(self):
        if not self.pr_number:
            return
        if self._worker and self._worker.isRunning():
            return

        self._worker = ChecksFetchWorker(self.repo_path, self.pr_number, self)
        self._worker.generation = self._generation
        generation = self._generation
        self._worker.checks_fetched.connect(
            lambda data: self._on_checks_received(data) if generation == self._generation else None)
        self._worker.error.connect(self._on_fetch_error)
        self._worker.finished.connect(self._release_worker)
        self._worker.start()

    def _release_worker(self):
        worker = self.sender()
        if self._worker is worker:
            self._worker = None
        worker.deleteLater()
        if (worker.pr_number != self.pr_number or worker.generation != self._generation) and self.pr_number:
            QTimer.singleShot(0, self.refresh_checks)

    def _on_fetch_error(self, err: str):
        self.status_pill.setText("Status unavailable")
        self.status_pill.setToolTip(err)

    def clear_pr(self):
        self.pr_number = None
        self.poll_timer.stop()
        self.table.setRowCount(0)
        self.progress_bar.setValue(0)
        self.lbl_title.setText("No pull request for this branch")
        self.lbl_title.setToolTip("")
        self.btn_open_pr.setEnabled(False)
        self.status_pill.setText("No PR")

    def _on_checks_received(self, data: dict):
        if str(data.get("number")) != self.pr_number:
            return
        self.status_pill.setToolTip("")
        old_state = self.pr_state
        if data.get("state") == "MERGED" and old_state == "OPEN":
            send_desktop_notification(f"PR #{self.pr_number} merged", data.get("title", ""), urgency="normal")
        if not self.pr_title and data.get("title"):
            self.pr_title = data.get("title", "")
        if not self.pr_url and data.get("url"):
            self.pr_url = data.get("url", "")
            self.btn_open_pr.setEnabled(True)
        if data.get("state"):
            self.pr_state = data.get("state", "")

        state_suffix = f" ({self.pr_state.capitalize()})" if self.pr_state else ""
        self.lbl_title.setText(f"PR #{self.pr_number}{state_suffix}: {self.pr_title}" if self.pr_title else f"PR #{self.pr_number}{state_suffix}")
        self.lbl_title.setToolTip(self.lbl_title.text())

        # Persist updated tracked PR state
        StateManager.update(
            rufin_tracked_pr={
                "number": str(self.pr_number),
                "title": self.pr_title,
                "url": self.pr_url,
                "state": self.pr_state,
            }
        )

        checks_raw = data.get("statusCheckRollup", [])
        self.checks_updated.emit(data)
        if not checks_raw:
            self.table.setRowCount(0)
            self.progress_bar.setValue(0)
            self.status_pill.setText("No Checks Yet")
            self.status_pill.setStyleSheet(
                "background-color: #21262d; color: #8b949e; border: 1px solid #30363d; "
                "border-radius: 4px; padding: 2px 8px; font-size: 11px; font-weight: 600;"
            )
            return

        checks = []
        has_running = False
        has_failure = False
        completed_count = 0

        for item in checks_raw:
            name = item.get("name") or item.get("context") or "Unknown"
            status = item.get("status") or item.get("state") or "PENDING"
            conclusion = item.get("conclusion")
            url = item.get("detailsUrl") or item.get("targetUrl") or ""
            started_at = item.get("startedAt")
            completed_at = item.get("completedAt")

            is_completed = (status == "COMPLETED" or bool(conclusion) or status in ("SUCCESS", "FAILURE", "ERROR"))
            if is_completed:
                completed_count += 1
            else:
                has_running = True

            is_failure = conclusion in ("FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE") or status in ("FAILURE", "ERROR")
            if is_failure:
                has_failure = True
                if name not in self.notified_failed_checks:
                    self.notified_failed_checks.add(name)
                    send_desktop_notification(
                        title="Rufin CI Check Failed",
                        body=f"Check '{name}' failed on PR #{self.pr_number}.",
                        urgency="critical",
                    )
            else:
                self.notified_failed_checks.discard(name)

            duration_str = format_check_duration(started_at, completed_at, conclusion)

            checks.append({
                "name": name,
                "status": status,
                "conclusion": conclusion,
                "url": url,
                "duration": duration_str,
                "is_completed": is_completed,
                "is_failure": is_failure,
            })

        self.table.setRowCount(len(checks))
        for row, c in enumerate(checks):
            name_item = QTableWidgetItem(c["name"])
            name_item.setFlags(Qt.ItemFlag.ItemIsEnabled | Qt.ItemFlag.ItemIsSelectable)
            self.table.setItem(row, 0, name_item)

            status_text = "Pending"
            if c["is_failure"]:
                status_text = "Failed"
            elif c["conclusion"] == "SUCCESS" or c["status"] == "SUCCESS":
                status_text = "Passed"
            elif c["conclusion"] == "SKIPPED":
                status_text = "Skipped"
            elif c["status"] in ("IN_PROGRESS", "QUEUED"):
                status_text = "Running"

            status_item = QTableWidgetItem(status_text)
            status_item.setFlags(Qt.ItemFlag.ItemIsEnabled | Qt.ItemFlag.ItemIsSelectable)
            if c["is_failure"]:
                status_item.setForeground(QColor("#f85149"))
            elif "Passed" in status_text:
                status_item.setForeground(QColor("#3fb950"))
            elif "Running" in status_text:
                status_item.setForeground(QColor("#58a6ff"))
            else:
                status_item.setForeground(QColor("#8b949e"))
            self.table.setItem(row, 1, status_item)

            dur_item = QTableWidgetItem(c["duration"])
            dur_item.setFlags(Qt.ItemFlag.ItemIsEnabled | Qt.ItemFlag.ItemIsSelectable)
            dur_item.setForeground(QColor("#8b949e"))
            self.table.setItem(row, 2, dur_item)

            if c["url"]:
                btn_link = QPushButton("Open Run")
                btn_link.setFixedHeight(20)
                btn_link.setStyleSheet("font-size: 10px; padding: 0 4px;")
                run_url = c["url"]
                btn_link.clicked.connect(lambda checked, u=run_url: QDesktopServices.openUrl(QUrl(u)))
                self.table.setCellWidget(row, 3, btn_link)
            else:
                self.table.setItem(row, 3, QTableWidgetItem(""))

        total = len(checks)
        pct = int((completed_count / total) * 100) if total > 0 else 0
        self.progress_bar.setValue(pct)

        result = "failure" if has_failure else "running" if has_running else "success"
        if result == "success" and self._last_result in ("running", "failure"):
            send_desktop_notification(f"PR #{self.pr_number}: checks passed", self.pr_title, urgency="normal")
        self._last_result = result

        if has_failure:
            self.status_pill.setText("Checks Failed")
            self.status_pill.setStyleSheet(
                "background-color: rgba(248, 81, 73, 0.15); color: #f85149; border: 1px solid rgba(248, 81, 73, 0.4); "
                "border-radius: 4px; padding: 2px 8px; font-size: 11px; font-weight: 600;"
            )
        elif has_running:
            self.status_pill.setText(f"Running ({completed_count}/{total})")
            self.status_pill.setStyleSheet(
                "background-color: rgba(56, 139, 253, 0.15); color: #58a6ff; border: 1px solid rgba(56, 139, 253, 0.4); "
                "border-radius: 4px; padding: 2px 8px; font-size: 11px; font-weight: 600;"
            )
        else:
            self.status_pill.setText("All Checks Passed")
            self.status_pill.setStyleSheet(
                "background-color: rgba(46, 160, 67, 0.15); color: #3fb950; border: 1px solid rgba(46, 160, 67, 0.4); "
                "border-radius: 4px; padding: 2px 8px; font-size: 11px; font-weight: 600;"
            )
            if self.pr_state in ("MERGED", "CLOSED"):
                self.poll_timer.stop()
