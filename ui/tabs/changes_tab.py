import os

from PyQt6.QtCore import QSignalBlocker, Qt
from PyQt6.QtWidgets import (
    QCheckBox,
    QComboBox,
    QHBoxLayout,
    QHeaderView,
    QLabel,
    QPushButton,
    QSplitter,
    QTreeWidget,
    QTreeWidgetItem,
    QVBoxLayout,
    QWidget,
)

from core.config import AppConfig
from core.git_manager import GitManager
from ui.components.diff_viewer import DiffViewer
from ui.components.file_list import FileListWidget


class ChangesTab(QWidget):
    def __init__(self, git_mgr: GitManager, config: AppConfig, parent=None):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        self.review_mgr = GitManager(git_mgr.repo_path)
        self.comparison = None
        self._loaded_path = None
        self._loaded_scope = None
        self._preview_paths = []
        self._commit_limit = 200
        layout = QVBoxLayout(self)
        layout.setContentsMargins(6, 6, 6, 6)

        controls = QHBoxLayout()
        controls.addWidget(QLabel("Worktree"))
        self.worktree_combo = QComboBox()
        self.worktree_combo.setMinimumWidth(200)
        controls.addWidget(self.worktree_combo, 1)
        controls.addWidget(QLabel("View"))
        self.scope_combo = QComboBox()
        self.scope_combo.addItems(["Branch", "Working tree", "Commit"])
        controls.addWidget(self.scope_combo)
        self.btn_refresh = QPushButton("Refresh")
        controls.addWidget(self.btn_refresh)
        layout.addLayout(controls)

        options = QHBoxLayout()
        self.base_label = QLabel("Base")
        options.addWidget(self.base_label)
        self.base_combo = QComboBox()
        self.base_combo.setEditable(True)
        self.base_combo.setInsertPolicy(QComboBox.InsertPolicy.NoInsert)
        self.base_combo.setMinimumWidth(180)
        options.addWidget(self.base_combo, 1)
        self.include_cb = QCheckBox("Include uncommitted")
        self.include_cb.setChecked(True)
        options.addWidget(self.include_cb)
        self.btn_all = QPushButton("All changes")
        options.addWidget(self.btn_all)
        layout.addLayout(options)

        splitter = QSplitter(Qt.Orientation.Horizontal)
        left = QWidget()
        left_layout = QVBoxLayout(left)
        left_layout.setContentsMargins(0, 0, 0, 0)
        self.history = QTreeWidget()
        self.history.setHeaderLabels(["Commit", "Date", "Subject"])
        self.history.setRootIsDecorated(False)
        self.history.header().setSectionResizeMode(0, QHeaderView.ResizeMode.ResizeToContents)
        self.history.header().setSectionResizeMode(1, QHeaderView.ResizeMode.ResizeToContents)
        self.history.header().setSectionResizeMode(2, QHeaderView.ResizeMode.Stretch)
        left_layout.addWidget(self.history, 1)
        self.btn_more = QPushButton("Load more commits")
        left_layout.addWidget(self.btn_more)
        self.file_list = FileListWidget(read_only=True)
        left_layout.addWidget(self.file_list, 1)
        splitter.addWidget(left)
        self.diff_viewer = DiffViewer(config, git_mgr.repo_path)
        self.diff_viewer.btn_file_mode.hide()
        self.diff_viewer.editor.setReadOnly(True)
        splitter.addWidget(self.diff_viewer)
        splitter.setSizes([420, 720])
        splitter.setCollapsible(0, False)
        splitter.setCollapsible(1, False)
        layout.addWidget(splitter, 1)
        self.status = QLabel()
        self.status.setWordWrap(True)
        self.status.setTextFormat(Qt.TextFormat.PlainText)
        layout.addWidget(self.status)

        self.worktree_combo.currentIndexChanged.connect(self.refresh)
        self.scope_combo.currentIndexChanged.connect(self.refresh)
        self.base_combo.activated.connect(self._load_changes)
        self.base_combo.lineEdit().editingFinished.connect(self._load_changes)
        self.include_cb.toggled.connect(self._load_changes)
        self.btn_refresh.clicked.connect(self.refresh)
        self.btn_all.clicked.connect(self._show_all)
        self.btn_more.clicked.connect(self._load_more)
        self.history.currentItemChanged.connect(self._on_commit_changed)
        self.file_list.file_selected.connect(lambda path: self._show_diff([path]))
        self.file_list.view_diff_requested.connect(self._show_diff)
        self._set_scope_controls()

    def showEvent(self, event):
        super().showEvent(event)
        self.refresh()

    def _set_scope_controls(self):
        branch = self.scope_combo.currentText() == "Branch"
        commit = self.scope_combo.currentText() == "Commit"
        for widget in (self.base_label, self.base_combo, self.include_cb):
            widget.setVisible(branch)
        self.history.setVisible(commit)
        self.btn_more.setVisible(commit)

    def refresh(self, *_):
        self._set_scope_controls()
        try:
            path = self.worktree_combo.currentData() or self.git_mgr.repo_path
            with QSignalBlocker(self.worktree_combo):
                self.worktree_combo.clear()
                for tree in self.git_mgr.get_worktrees():
                    name = os.path.basename(tree.path)
                    branch = tree.branch or tree.head[:8] or "bare"
                    self.worktree_combo.addItem(f"{name}: {branch}", tree.path)
                    index = self.worktree_combo.count() - 1
                    self.worktree_combo.setItemData(index, tree.path, Qt.ItemDataRole.ToolTipRole)
                    self.worktree_combo.model().item(index).setEnabled(
                        not tree.bare and not tree.prunable and os.path.isdir(tree.path))
                index = self.worktree_combo.findData(path)
                if index < 0:
                    index = self.worktree_combo.findData(self.git_mgr.repo_path)
                self.worktree_combo.setCurrentIndex(index)
            path = self.worktree_combo.currentData()
            if not path:
                raise RuntimeError("No working tree available")
            self.review_mgr = GitManager(path)
            changed = path != self._loaded_path
            scope = self.scope_combo.currentText()
            if changed or scope != self._loaded_scope:
                self._preview_paths = []
            self._loaded_scope = scope
            base = self.base_combo.currentText()
            with QSignalBlocker(self.base_combo):
                self.base_combo.clear()
                self.base_combo.addItems(self.review_mgr.get_comparison_refs())
                if changed:
                    base = self.review_mgr.get_default_base(self.config.default_base_branch)
                self.base_combo.setCurrentText(base)
            if changed:
                self._commit_limit = 200
                with QSignalBlocker(self.history):
                    self.history.clear()
                self.file_list.set_files([])
                self._loaded_path = path
            self.diff_viewer.repo_path = path
            if self.scope_combo.currentText() == "Commit":
                self._load_history()
            self._load_changes()
        except RuntimeError as error:
            self._show_error(error)

    def refresh_working_tree(self):
        self.refresh()

    def _load_more(self):
        self._commit_limit += 200
        self.refresh()

    def _on_commit_changed(self, *_):
        self._preview_paths = []
        self._load_changes()

    def _show_all(self):
        self.file_list.list_widget.setCurrentItem(None)
        self.file_list.list_widget.clearSelection()
        self._show_diff([])

    def _load_history(self):
        current = self.history.currentItem()
        oid = current.data(0, Qt.ItemDataRole.UserRole) if current else None
        commits = self.review_mgr.get_commits(self._commit_limit)
        with QSignalBlocker(self.history):
            self.history.clear()
            selected = None
            for commit in commits:
                row = QTreeWidgetItem(self.history, [commit.short_oid, commit.date, commit.subject])
                row.setData(0, Qt.ItemDataRole.UserRole, commit.oid)
                row.setToolTip(2, commit.subject)
                if commit.oid == oid:
                    selected = row
            self.history.setCurrentItem(selected or self.history.topLevelItem(0))
        self.btn_more.setEnabled(len(commits) == self._commit_limit)

    def _load_changes(self, *_):
        try:
            scope = self.scope_combo.currentText()
            if scope == "Working tree":
                self.comparison = self.review_mgr.working_comparison()
                title = "Working tree"
            elif scope == "Branch":
                base = self.base_combo.currentText().strip()
                self.comparison = self.review_mgr.branch_comparison(base, self.include_cb.isChecked())
                title = f"{base} → {self.review_mgr.get_current_branch()}"
                if self.include_cb.isChecked():
                    title += " + uncommitted"
            else:
                row = self.history.currentItem()
                if row is None:
                    self.comparison = None
                    self.file_list.set_files([])
                    self.diff_viewer.set_diff("No commits", "Commit")
                    self.status.setText("No commits")
                    return
                self.comparison = self.review_mgr.commit_comparison(row.data(0, Qt.ItemDataRole.UserRole))
                title = f"{row.text(0)}: {row.text(2)}"
            items = self.review_mgr.get_comparison_files(self.comparison)
            self.file_list.set_files(items)
            self.status.setText(f"{title} · {len(items)} files · Base {self.comparison.left[:8]}")
            self.status.setToolTip(self.review_mgr.repo_path)
            preview_paths = [path for path in self._preview_paths if any(item.path == path for item in items)]
            self._show_diff(preview_paths)
        except RuntimeError as error:
            self._show_error(error)

    def _show_diff(self, paths):
        if self.comparison is None:
            return
        try:
            self._preview_paths = list(paths)
            items = [item for item in self.file_list.items if not paths or item.path in paths]
            diff = self.review_mgr.get_comparison_diff(self.comparison, items)
            title = paths[0] if len(paths) == 1 else "All changes" if not paths else "Selected changes"
            text = diff or "No changes"
            if text != self.diff_viewer.current_diff_text or title != self.diff_viewer.title_label.text():
                self.diff_viewer.set_diff(text, title,
                                          sum(item.additions for item in items),
                                          sum(item.deletions for item in items))
        except RuntimeError as error:
            self._show_error(error)

    def _show_error(self, error):
        self.comparison = None
        self.file_list.set_files([])
        self.diff_viewer.set_diff("Diff unavailable", "Diff unavailable")
        self.status.setText(str(error))
