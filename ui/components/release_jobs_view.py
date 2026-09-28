import re
import json
import subprocess
from typing import Dict, Optional
from PyQt6.QtCore import Qt, QUrl, QThread, QTimer, pyqtSignal
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
    QComboBox,
)
from core.state import StateManager
from core.notifications import send_desktop_notification


class ReleaseFetchWorker(QThread):
    runs_fetched = pyqtSignal(dict)
    error = pyqtSignal(str)

    def __init__(self, repo_path: str, tag: str, include_flathub: bool, parent=None):
        super().__init__(parent)
        self.repo_path = repo_path
        self.tag = tag
        self.include_flathub = include_flathub

    def query(self, *args):
        result = subprocess.run(["gh", *args], cwd=self.repo_path,
                                capture_output=True, text=True, timeout=20)
        if result.returncode:
            raise RuntimeError(result.stderr.strip() or "GitHub returned no data")
        return json.loads(result.stdout)

    def run(self):
        try:
            repo = self.query("repo", "view", "--json", "url")["url"]
            releases = self.query("release", "list", "--limit", "30", "--json", "tagName,isDraft,name")
            prs = self.query("pr", "list", "--state", "open", "--limit", "100",
                             "--json", "headRefName,url,title")
            prep = {pr["headRefName"].removeprefix("release/"): pr for pr in prs
                    if pr["headRefName"].startswith("release/")}
            tags = list(dict.fromkeys([*prep, *(r["tagName"] for r in releases)]))
            tag = self.tag or next(iter(tags), "")
            runs = self.query("run", "list", "--limit", "100", "--json",
                              "workflowName,status,conclusion,url,displayTitle,headBranch") if tag else []
            flathub_pr = None
            flathub_error = ""
            if self.include_flathub and tag:
                try:
                    prs = self.query("pr", "list", "--repo", "flathub/io.github.screwys.Rufin",
                                     "--state", "all", "--head", f"release/{tag}", "--limit", "1",
                                     "--json", "number,title,url,state,headRefName")
                    flathub_pr = prs[0] if prs else None
                except Exception as error:
                    flathub_error = str(error)
            runs_map = {}
            for run in runs:
                title_tags = re.findall(r"v?\d+\.\d+\.\d+(?:[-.][0-9A-Za-z.-]+)?", run["displayTitle"])
                if run["headBranch"] != tag and tag not in title_tags:
                    continue
                name = {"Android Release APK": "Android Release", "Release": "Release Gate"}.get(
                    run["workflowName"], run["workflowName"])
                runs_map.setdefault(name, run)
            release = next((r for r in releases if r["tagName"] == tag), None)
            self.runs_fetched.emit({"latest_tag": tag, "tags": tags,
                "release_url": f"{repo}/releases/tag/{tag}" if release else "",
                "is_draft": release["isDraft"] if release else None,
                "pr_url": prep.get(tag, {}).get("url", ""), "runs_map": runs_map,
                "flathub_pr": flathub_pr, "flathub_error": flathub_error})
        except Exception as error:
            self.error.emit(str(error))



class ReleaseJobsWidget(QWidget):
    workflow_started = pyqtSignal()
    local_work_started = pyqtSignal()
    remote_status_ready = pyqtSignal()
    DEFAULT_RUFIN_COPR = "https://copr.fedorainfracloud.org/coprs/screwyy/rufin/"

    def __init__(self, project_name: str = "Rufin", parent=None, repo_path=""):
        super().__init__(parent)
        self.project_name = project_name
        self.repo_path = repo_path
        self.selected_tag = ""
        self._remote_runs = {}
        self._output_pending = ""
        self.state_key = "igloo_release_state" if project_name == "Igloo" else "rufin_release_state"
        self.jobs: Dict[str, Dict[str, str]] = {}
        self.copr_url = self.DEFAULT_RUFIN_COPR
        self.copr_title = "COPR Builds"
        self.pr_url = ""
        self.flathub_url = ""
        self.release_url = ""
        self.release_tag = ""
        self._fetch_worker: Optional[ReleaseFetchWorker] = None

        self._init_project_jobs()
        self._init_ui()
        self._load_saved_state()
        self.poll_timer = QTimer(self)
        self.poll_timer.setInterval(15000)
        self.poll_timer.timeout.connect(self.sync_remote_status)
        self.poll_timer.start()
        QTimer.singleShot(0, self.sync_remote_status)

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
                "Flathub PR": {"status": "unknown", "url": "", "step": "No PR found for this release"},
            }

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(10, 8, 10, 8)
        layout.setSpacing(10)

        targets = QHBoxLayout()
        targets.addWidget(QLabel("Release:"))
        self.tag_picker = QComboBox()
        self.tag_picker.setMinimumContentsLength(12)
        self.tag_picker.activated.connect(self._select_tag)
        targets.addWidget(self.tag_picker)
        self.sync_label = QLabel("Checking GitHub...")
        self.sync_label.setWordWrap(True)
        targets.addWidget(self.sync_label, 1)
        layout.addLayout(targets)

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
            self.btn_copr = QPushButton(self.copr_title)
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

        self.btn_fetch_latest = QPushButton("Sync Remote Status")
        self.btn_fetch_latest.setFixedHeight(26)
        self.btn_fetch_latest.setStyleSheet("padding: 2px 10px; font-size: 11px;")
        self.btn_fetch_latest.setToolTip("Query GitHub for latest release tag and workflow status")
        self.btn_fetch_latest.clicked.connect(self.sync_remote_status)
        h_links.addWidget(self.btn_fetch_latest)

        layout.addWidget(links_box)

        # Workflows Table
        self.table = QTableWidget()
        self.table.setColumnCount(4)
        self.table.setHorizontalHeaderLabels(["Workflow / Publication", "Status", "Current / Failed Step", "Action"])
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
                btn = QPushButton("Open PR" if name == "Flathub PR" else "Open Run")
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
        if st in ("success", "passed"):
            item.setText("Passed")
            item.setForeground(QColor("#3fb950"))
        elif st == "merged":
            item.setText("Merged")
            item.setForeground(QColor("#3fb950"))
        elif st == "open":
            item.setText("Open")
            item.setForeground(QColor("#58a6ff"))
        elif st == "closed":
            item.setText("Closed")
            item.setForeground(QColor("#f85149"))
        elif st == "not_found":
            item.setText("Not found")
            item.setForeground(QColor("#8b949e"))
        elif st in ("running", "in_progress", "queued", "waiting", "pending"):
            item.setText("Running")
            item.setForeground(QColor("#58a6ff"))
        elif st in ("failure", "failed"):
            item.setText("Failed")
            item.setForeground(QColor("#f85149"))
        else:
            item.setText(status.replace("_", " ").capitalize())
            item.setForeground(QColor("#8b949e"))

    def _save_current_state(self):
        saved = {
            "jobs": self.jobs,
            "copr_url": self.copr_url,
            "copr_title": self.copr_title,
            "pr_url": self.pr_url,
            "flathub_url": self.flathub_url,
            "release_url": self.release_url,
            "release_tag": self.release_tag,
        }
        StateManager.update(**{self.state_key: saved})

    def _load_saved_state(self):
        data = StateManager.get(self.state_key)
        if not data or not isinstance(data, dict):
            return

        if "jobs" in data and isinstance(data["jobs"], dict):
            for k, v in data["jobs"].items():
                if k in self.jobs and isinstance(v, dict):
                    self.jobs[k] = v

        if "copr_url" in data and data["copr_url"]:
            self.copr_url = data["copr_url"]
            self.copr_title = data.get("copr_title", "COPR Builds")
            if self.btn_copr:
                self.btn_copr.setText(self.copr_title)

        if "pr_url" in data and data["pr_url"]:
            self.pr_url = data["pr_url"]
            if self.btn_pr:
                self.btn_pr.setEnabled(True)

        if "flathub_url" in data and data["flathub_url"]:
            self.flathub_url = data["flathub_url"]
            if self.btn_flathub:
                self.btn_flathub.setEnabled(True)

        if "release_url" in data and data["release_url"]:
            self.release_url = data["release_url"]
            self.release_tag = data.get("release_tag", "")
            btn_title = f"Release ({self.release_tag})" if self.release_tag else "GitHub Release"
            self.btn_gh_rel.setText(btn_title)
            self.btn_gh_rel.setEnabled(True)

        self._populate_table()

    def sync_remote_status(self):
        if self._fetch_worker and self._fetch_worker.isRunning():
            return
        self.btn_fetch_latest.setEnabled(False)
        self.sync_label.setText("Checking GitHub...")
        self._fetch_worker = ReleaseFetchWorker(self.repo_path, self.selected_tag, self.project_name == "Rufin", self)
        self._fetch_worker.error.connect(lambda error: self.sync_label.setText(f"Status unavailable: {error}"))
        self._fetch_worker.runs_fetched.connect(self._on_remote_runs_fetched)
        self._fetch_worker.finished.connect(lambda: self.btn_fetch_latest.setEnabled(True))
        self._fetch_worker.finished.connect(self._release_worker)
        self._fetch_worker.start()

    def _release_worker(self):
        worker = self.sender()
        if self._fetch_worker is worker:
            self._fetch_worker = None
        worker.deleteLater()
        if worker.tag != self.selected_tag:
            QTimer.singleShot(0, self.sync_remote_status)

    def _select_tag(self, index):
        self.track_version(self.tag_picker.itemText(index))

    def track_version(self, tag):
        self.selected_tag = tag
        self.sync_remote_status()

    def _on_remote_runs_fetched(self, result: dict):
        if self.selected_tag and result["latest_tag"] != self.selected_tag:
            return
        tag = result["latest_tag"]
        if tag != self.release_tag:
            self.reset_state()
        self.release_tag = tag
        self.tag_picker.blockSignals(True)
        self.tag_picker.clear()
        self.tag_picker.addItems(list(dict.fromkeys(([tag] if tag else []) + result["tags"])))
        self.tag_picker.setCurrentText(tag)
        self.tag_picker.blockSignals(False)
        self.release_url = result["release_url"]
        self.btn_gh_rel.setText(f"Release ({tag})" if tag else "GitHub Release")
        self.btn_gh_rel.setEnabled(bool(self.release_url))
        if result["pr_url"]:
            self.pr_url = result["pr_url"]
        if self.btn_pr:
            self.btn_pr.setEnabled(bool(self.pr_url))
        if self.btn_flathub:
            flathub_pr = result.get("flathub_pr")
            self.flathub_url = flathub_pr.get("url", "") if flathub_pr else ""
            self.btn_flathub.setEnabled(bool(self.flathub_url))
            if result.get("flathub_error"):
                self.jobs["Flathub PR"] = {
                    "status": "unknown", "url": "",
                    "step": f"Status unavailable: {result['flathub_error']}",
                }
            elif flathub_pr:
                self.jobs["Flathub PR"] = {
                    "status": flathub_pr["state"].lower(), "url": flathub_pr["url"],
                    "step": f"PR #{flathub_pr['number']}: {flathub_pr['title']}",
                }
            else:
                self.jobs["Flathub PR"] = {
                    "status": "not_found", "url": "",
                    "step": f"No PR found for release/{tag}" if tag else "No release tag selected",
                }
        state = "Draft release" if result["is_draft"] else "Published release" if self.release_url else "Release preparation" if tag else "No releases found"
        self.sync_label.setText(f"{state}. Updated from GitHub.")
        for name in self.jobs:
            if name == "Flathub PR":
                continue
            run = result["runs_map"].get(name)
            if run:
                previous = self._remote_runs.get(run["url"])
                conclusion = run.get("conclusion")
                if previous is not None and previous != conclusion and conclusion:
                    send_desktop_notification(f"{self.project_name} {tag}: {name}",
                        f"Workflow {conclusion.replace('_', ' ')}",
                        urgency="normal" if conclusion in ("success", "skipped", "neutral") else "critical")
                self._remote_runs[run["url"]] = conclusion
            self.jobs[name] = {"status": run.get("conclusion") or run["status"],
                               "url": run["url"], "step": run.get("conclusion") or run["status"]} if run else {
                                   "status": "unknown", "url": "", "step": "No matching recent run"}
        self._populate_table()
        self._save_current_state()

        if result["runs_map"]:
            self.remote_status_ready.emit()

    def reset_state(self):
        for k in self.jobs:
            self.jobs[k] = {"status": "idle", "url": "", "step": "—"}
        self.copr_url = self.DEFAULT_RUFIN_COPR
        self.copr_title = "COPR Builds"
        if self.btn_copr:
            self.btn_copr.setText(self.copr_title)
        if self.btn_pr:
            self.btn_pr.setEnabled(False)
        if self.btn_flathub:
            self.btn_flathub.setEnabled(False)
        self.pr_url = ""
        self.flathub_url = ""
        self.release_url = ""
        self.release_tag = ""
        self.btn_gh_rel.setText("GitHub Release")
        self.btn_gh_rel.setEnabled(False)
        self._populate_table()
        self._save_current_state()

    def process_output_chunk(self, text: str):
        self._output_pending += text
        if "\n" not in self._output_pending:
            return
        text, self._output_pending = self._output_pending.rsplit("\n", 1)
        text = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", text)
        changed = False

        # Match COPR build logs (Rufin)
        copr_match = re.search(r"https://copr\.fedorainfracloud\.org/coprs/screwyy/rufin/build/(\d+)/?", text)
        if copr_match and self.btn_copr:
            b_id = copr_match.group(1)
            self.copr_url = f"https://copr.fedorainfracloud.org/coprs/screwyy/rufin/build/{b_id}/"
            self.copr_title = f"COPR Build #{b_id}"
            self.btn_copr.setText(self.copr_title)
            changed = True

        copr_id_match = re.search(r"COPR build (\d+):", text)
        if copr_id_match and self.btn_copr and "screwyy/rufin/build" not in self.copr_url:
            b_id = copr_id_match.group(1)
            self.copr_url = f"https://copr.fedorainfracloud.org/coprs/screwyy/rufin/build/{b_id}/"
            self.copr_title = f"COPR Build #{b_id}"
            self.btn_copr.setText(self.copr_title)
            changed = True

        # Match PR URLs
        pr_match = re.search(r"https://github\.com/[^/\s]+/[^/\s]+/pull/(\d+)", text)
        if pr_match:
            full_url = pr_match.group(0)
            if "flathub" in full_url and self.btn_flathub:
                self.flathub_url = full_url
                self.btn_flathub.setEnabled(True)
                changed = True
            elif self.btn_pr:
                self.pr_url = full_url
                self.btn_pr.setEnabled(True)
                changed = True

        # Match GitHub Release URLs
        rel_match = re.search(r"https://github\.com/[^/\s]+/[^/\s]+/releases/tag/([^\s]+)", text)
        if rel_match:
            self.release_url = rel_match.group(0)
            self.release_tag = rel_match.group(1)
            self.btn_gh_rel.setText(f"Release ({self.release_tag})")
            self.btn_gh_rel.setEnabled(True)
            changed = True

        # Match workflow triggers or runs
        for key in self.jobs.keys():
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
            self._save_current_state()

        for line in text.splitlines():
            if "actions/runs/" in line and re.match(r"(?:Started |Reusing |Following replacement |- )", line):
                self.workflow_started.emit()
            elif line.strip() in (
                "All draft candidate workflows completed successfully.",
                "All package-manager publication workflows completed successfully.",
            ):
                self.local_work_started.emit()
