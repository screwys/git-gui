import re
from typing import Dict, Optional, List
from PyQt6.QtCore import Qt, QUrl
from PyQt6.QtGui import QDesktopServices, QColor
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QTableWidget,
    QTableWidgetItem,
    QHeaderView,
    QPushButton,
    QLabel,
    QFrame,
)


class ReleaseJobsWidget(QWidget):
    DEFAULT_RUFIN_COPR = "https://copr.fedorainfracloud.org/coprs/screwyy/rufin/"

    def __init__(self, project_name: str = "Rufin", parent=None):
        super().__init__(parent)
        self.project_name = project_name
        self.jobs: Dict[str, Dict[str, str]] = {}
        self.copr_url = self.DEFAULT_RUFIN_COPR
        self.pr_url = ""
        self.flathub_url = ""
        self.release_url = ""
        self._init_project_jobs()
        self._init_ui()

    def _init_project_jobs(self):
        if self.project_name == "Igloo":
            self.jobs = {
                "Release Gate": {"status": "idle", "url": "", "step": "—"},
                "Container Release": {"status": "idle", "url": "", "step": "—"},
                "Android Release": {"status": "idle", "url": "", "step": "—"},
                "Windows Release": {"status": "idle", "url": "", "step": "—"},
            }
        else:
            self.jobs = {
                "AUR": {"status": "idle", "url": "", "step": "—"},
                "Flatpak": {"status": "idle", "url": "", "step": "—"},
                "Nix": {"status": "idle", "url": "", "step": "—"},
                "macOS": {"status": "idle", "url": "", "step": "—"},
                "RPM": {"status": "idle", "url": "", "step": "—"},
                "Windows": {"status": "idle", "url": "", "step": "—"},
                "Homebrew": {"status": "idle", "url": "", "step": "—"},
            }

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(10, 8, 10, 8)
        layout.setSpacing(10)

        # Quick Links Bar
        links_box = QFrame()
        links_box.setStyleSheet(
            "QFrame { background-color: #161b22; border: 1px solid #30363d; border-radius: 6px; padding: 6px; }"
        )
        h_links = QHBoxLayout(links_box)
        h_links.setContentsMargins(4, 2, 4, 2)
        h_links.setSpacing(8)

        lbl_quick = QLabel("Quick Links:")
        lbl_quick.setStyleSheet("color: #8b949e; font-weight: 600; font-size: 11px;")
        h_links.addWidget(lbl_quick)

        if self.project_name == "Rufin":
            self.btn_copr = QPushButton("COPR Builds")
            self.btn_copr.setFixedHeight(26)
            self.btn_copr.setStyleSheet("padding: 2px 10px; font-size: 11px;")
            self.btn_copr.clicked.connect(lambda: self._open_url(self.copr_url))
            h_links.addWidget(self.btn_copr)

            self.btn_pr = QPushButton("Release PR")
            self.btn_pr.setFixedHeight(26)
            self.btn_pr.setEnabled(False)
            self.btn_pr.setStyleSheet("padding: 2px 10px; font-size: 11px;")
            self.btn_pr.clicked.connect(lambda: self._open_url(self.pr_url))
            h_links.addWidget(self.btn_pr)

            self.btn_flathub = QPushButton("Flathub PR")
            self.btn_flathub.setFixedHeight(26)
            self.btn_flathub.setEnabled(False)
            self.btn_flathub.setStyleSheet("padding: 2px 10px; font-size: 11px;")
            self.btn_flathub.clicked.connect(lambda: self._open_url(self.flathub_url))
            h_links.addWidget(self.btn_flathub)
        else:
            self.btn_copr = None
            self.btn_pr = None
            self.btn_flathub = None

            self.btn_actions = QPushButton("GitHub Actions")
            self.btn_actions.setFixedHeight(26)
            self.btn_actions.setStyleSheet("padding: 2px 10px; font-size: 11px;")
            self.btn_actions.clicked.connect(lambda: self._open_url("https://github.com/screwys/Igloo/actions"))
            h_links.addWidget(self.btn_actions)

        self.btn_gh_rel = QPushButton("GitHub Release")
        self.btn_gh_rel.setFixedHeight(26)
        self.btn_gh_rel.setEnabled(False)
        self.btn_gh_rel.setStyleSheet("padding: 2px 10px; font-size: 11px;")
        self.btn_gh_rel.clicked.connect(lambda: self._open_url(self.release_url))
        h_links.addWidget(self.btn_gh_rel)

        h_links.addStretch()
        layout.addWidget(links_box)

        # Workflows Table
        self.table = QTableWidget()
        self.table.setColumnCount(4)
        self.table.setHorizontalHeaderLabels(["Workflow", "Status", "Current / Failed Step", "Action"])
        self.table.horizontalHeader().setSectionResizeMode(0, QHeaderView.ResizeMode.ResizeToContents)
        self.table.horizontalHeader().setSectionResizeMode(1, QHeaderView.ResizeMode.ResizeToContents)
        self.table.horizontalHeader().setSectionResizeMode(2, QHeaderView.ResizeMode.Stretch)
        self.table.horizontalHeader().setSectionResizeMode(3, QHeaderView.ResizeMode.ResizeToContents)
        self.table.verticalHeader().setVisible(False)
        self.table.setEditTriggers(QTableWidget.EditTrigger.NoEditTriggers)
        self.table.setSelectionMode(QTableWidget.SelectionMode.NoSelection)
        self.table.setStyleSheet(
            "QTableWidget { background-color: #0b0e14; border: 1px solid #30363d; border-radius: 6px; } "
            "QHeaderView::section { background-color: #161b22; color: #8b949e; border: none; border-bottom: 1px solid #30363d; padding: 6px; font-size: 11px; font-weight: 600; } "
            "QTableWidget::item { padding: 4px; border-bottom: 1px solid #161b22; }"
        )
        layout.addWidget(self.table)

        self._populate_table()

    def _open_url(self, url: str):
        if url:
            QDesktopServices.openUrl(QUrl(url))

    def _populate_table(self):
        self.table.setRowCount(len(self.jobs))
        for row, (name, info) in enumerate(self.jobs.items()):
            # Name
            name_item = QTableWidgetItem(name)
            name_item.setTextAlignment(Qt.AlignmentFlag.AlignVCenter | Qt.AlignmentFlag.AlignLeft)
            name_item.setForeground(QColor("#e6edf3"))
            self.table.setItem(row, 0, name_item)

            # Status pill
            status_item = QTableWidgetItem()
            status_item.setTextAlignment(Qt.AlignmentFlag.AlignCenter)
            self._apply_status_style(status_item, info["status"])
            self.table.setItem(row, 1, status_item)

            # Step
            step_item = QTableWidgetItem(info["step"])
            step_item.setTextAlignment(Qt.AlignmentFlag.AlignVCenter | Qt.AlignmentFlag.AlignLeft)
            step_item.setForeground(QColor("#8b949e"))
            self.table.setItem(row, 2, step_item)

            # Action button
            if info["url"]:
                btn = QPushButton("Open Run")
                btn.setFixedHeight(22)
                btn.setStyleSheet("font-size: 11px; padding: 1px 8px;")
                url = info["url"]
                btn.clicked.connect(lambda checked, u=url: self._open_url(u))
                self.table.setCellWidget(row, 3, btn)
            else:
                lbl = QLabel("—")
                lbl.setAlignment(Qt.AlignmentFlag.AlignCenter)
                lbl.setStyleSheet("color: #484f58; font-size: 11px;")
                self.table.setCellWidget(row, 3, lbl)

    def _apply_status_style(self, item: QTableWidgetItem, status: str):
        st = status.lower()
        if st in ("success", "passed", "completed"):
            item.setText("Passed")
            item.setForeground(QColor("#3fb950"))
        elif st in ("running", "in_progress"):
            item.setText("Running")
            item.setForeground(QColor("#58a6ff"))
        elif st in ("failure", "failed"):
            item.setText("Failed")
            item.setForeground(QColor("#f85149"))
        else:
            item.setText("Idle")
            item.setForeground(QColor("#8b949e"))

    def reset_state(self):
        for k in self.jobs:
            self.jobs[k] = {"status": "idle", "url": "", "step": "—"}
        self.copr_url = self.DEFAULT_RUFIN_COPR
        if self.btn_copr:
            self.btn_copr.setText("COPR Builds")
        if self.btn_pr:
            self.btn_pr.setEnabled(False)
        if self.btn_flathub:
            self.btn_flathub.setEnabled(False)
        self.release_url = ""
        self.btn_gh_rel.setEnabled(False)
        self._populate_table()

    def process_output_chunk(self, text: str):
        changed = False

        # Match COPR build logs (Rufin)
        copr_match = re.search(r"https://copr\.fedorainfracloud\.org/coprs/screwyy/rufin/build/(\d+)/?", text)
        if copr_match and self.btn_copr:
            b_id = copr_match.group(1)
            self.copr_url = f"https://copr.fedorainfracloud.org/coprs/screwyy/rufin/build/{b_id}/"
            self.btn_copr.setText(f"COPR Build #{b_id}")

        copr_id_match = re.search(r"COPR build (\d+):", text)
        if copr_id_match and self.btn_copr and "screwyy/rufin/build" not in self.copr_url:
            b_id = copr_id_match.group(1)
            self.copr_url = f"https://copr.fedorainfracloud.org/coprs/screwyy/rufin/build/{b_id}/"
            self.btn_copr.setText(f"COPR Build #{b_id}")

        # Match PR URLs
        pr_match = re.search(r"https://github\.com/[^/\s]+/[^/\s]+/pull/(\d+)", text)
        if pr_match:
            full_url = pr_match.group(0)
            if "flathub" in full_url and self.btn_flathub:
                self.flathub_url = full_url
                self.btn_flathub.setEnabled(True)
            elif self.btn_pr:
                self.pr_url = full_url
                self.btn_pr.setEnabled(True)

        # Match GitHub Release URLs
        rel_match = re.search(r"https://github\.com/[^/\s]+/[^/\s]+/releases/tag/([^\s]+)", text)
        if rel_match:
            self.release_url = rel_match.group(0)
            self.btn_gh_rel.setEnabled(True)

        # Match workflow triggers or runs
        for key in self.jobs.keys():
            # e.g. "Started AUR: https://..." or "- AUR: https://..." or "container-release.yml"
            pattern = rf"(?:Started|Reusing|-)\s+{re.escape(key)}(?:\s+candidate\s+run)?:\s*(https://github\.com/[^\s]+/actions/runs/\d+)"
            m = re.search(pattern, text, re.IGNORECASE)
            if m:
                url = m.group(1)
                self.jobs[key]["url"] = url
                if self.jobs[key]["status"] == "idle":
                    self.jobs[key]["status"] = "running"
                changed = True

        # Igloo workflow dispatch matchers
        if self.project_name == "Igloo":
            if "workflow run release.yml" in text:
                self.jobs["Release Gate"]["status"] = "running"
                changed = True
            if "workflow run container-release.yml" in text:
                self.jobs["Container Release"]["status"] = "running"
                changed = True
            if "workflow run android-release.yml" in text:
                self.jobs["Android Release"]["status"] = "running"
                changed = True
            if "workflow run windows-release.yml" in text:
                self.jobs["Windows Release"]["status"] = "running"
                changed = True

        # Summary text matches
        if "All draft candidate workflows completed successfully." in text:
            for k in ["AUR", "Flatpak", "Nix", "macOS", "RPM", "Windows"]:
                if k in self.jobs:
                    self.jobs[k]["status"] = "passed"
            changed = True

        if "All package-manager publication workflows completed successfully." in text:
            for k in ["AUR", "Homebrew"]:
                if k in self.jobs:
                    self.jobs[k]["status"] = "passed"
            changed = True

        # Table rows parsing
        for line in text.splitlines():
            parts = [p.strip() for p in re.split(r"\s{2,}", line) if p.strip()]
            if len(parts) >= 3:
                wf_name = parts[0]
                if wf_name in self.jobs:
                    status_str = parts[2].lower()
                    if status_str in ("passed", "success", "completed"):
                        self.jobs[wf_name]["status"] = "passed"
                    elif status_str in ("running", "in_progress"):
                        self.jobs[wf_name]["status"] = "running"
                    elif status_str in ("failed", "failure"):
                        self.jobs[wf_name]["status"] = "failed"
                    if len(parts) >= 5:
                        self.jobs[wf_name]["step"] = parts[4]
                    changed = True

        if changed:
            self._populate_table()
