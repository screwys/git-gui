import os
from pathlib import Path

import yaml
from PyQt6.QtCore import Qt, pyqtSignal
from PyQt6.QtWidgets import (
    QAbstractItemView,
    QComboBox,
    QFileDialog,
    QHBoxLayout,
    QLabel,
    QLineEdit,
    QListWidget,
    QListWidgetItem,
    QMessageBox,
    QPushButton,
    QSplitter,
    QVBoxLayout,
    QWidget,
)

from core.config import ConfigManager
from core.packages import (
    commit_subject,
    discover_sources,
    edit_entries,
    entries,
    entry_name,
    read_text,
    sources_for_file,
)
from ui.components.diff_viewer import DiffViewer


class PackagesTab(QWidget):
    changed = pyqtSignal(str)

    def __init__(self, git_mgr, config, parent=None, *, nocblue=False):
        super().__init__(parent)
        self.git_mgr = git_mgr
        self.config = config
        self.nocblue = nocblue
        self.sources = []
        self.text = ""
        self._init_ui()
        self.refresh()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        row = QHBoxLayout()
        self.kind = QComboBox()
        self.kind.addItems(["RPM", "Brew", "Nix"])
        self.kind.currentTextChanged.connect(self._show_sources)
        row.addWidget(self.kind)
        self.source = QComboBox()
        self.source.setSizeAdjustPolicy(QComboBox.SizeAdjustPolicy.AdjustToMinimumContentsLengthWithIcon)
        self.source.setMinimumContentsLength(20)
        self.source.currentIndexChanged.connect(self._load_source)
        row.addWidget(self.source, 1)
        add_source = QPushButton("Add source")
        add_source.clicked.connect(self._add_source)
        row.addWidget(add_source)
        reload = QPushButton("Reload")
        reload.clicked.connect(self.refresh)
        row.addWidget(reload)
        layout.addLayout(row)

        splitter = QSplitter(Qt.Orientation.Horizontal)
        left = QWidget()
        left_layout = QVBoxLayout(left)
        left_layout.setContentsMargins(0, 0, 0, 0)
        self.search = QLineEdit()
        self.search.setPlaceholderText("Filter packages")
        self.search.textChanged.connect(self._filter)
        left_layout.addWidget(self.search)
        self.packages = QListWidget()
        self.packages.setSelectionMode(QAbstractItemView.SelectionMode.ExtendedSelection)
        self.packages.itemSelectionChanged.connect(self._update_remove)
        left_layout.addWidget(self.packages, 1)
        add_row = QHBoxLayout()
        self.name = QLineEdit()
        self.name.setPlaceholderText("Package")
        self.name.returnPressed.connect(self._add_package)
        add_row.addWidget(self.name, 1)
        self.add = QPushButton("Add")
        self.add.clicked.connect(self._add_package)
        add_row.addWidget(self.add)
        left_layout.addLayout(add_row)
        self.remove = QPushButton("Remove selected")
        self.remove.clicked.connect(self._remove_packages)
        left_layout.addWidget(self.remove)
        splitter.addWidget(left)
        self.diff = DiffViewer(self.config)
        splitter.addWidget(self.diff)
        splitter.setStretchFactor(0, 1)
        splitter.setStretchFactor(1, 2)
        layout.addWidget(splitter, 1)
        self.status = QLabel()
        self.status.setWordWrap(True)
        layout.addWidget(self.status)

    def refresh(self):
        configured = self.config.package_sources.get(os.path.realpath(self.git_mgr.repo_path), [])
        self.sources, errors = discover_sources(self.git_mgr.repo_path, configured)
        self._show_sources()
        if errors:
            self.status.setText("Could not read package sources.")
            self.status.setToolTip("\n".join(errors))

    def _show_sources(self):
        previous = self.source.currentData()
        self.source.blockSignals(True)
        self.source.clear()
        for source in self.sources:
            if source.kind == self.kind.currentText():
                self.source.addItem(source.label, source)
        for index in range(self.source.count()):
            if self.source.itemData(index) == previous:
                self.source.setCurrentIndex(index)
                break
        self.source.blockSignals(False)
        self._load_source()

    def _load_source(self):
        self.packages.clear()
        self.status.clear()
        self.status.setToolTip("")
        source = self.source.currentData()
        self.add.setEnabled(False)
        self.name.setEnabled(False)
        self._update_remove()
        if source is None:
            self.diff.set_diff("", "")
            self.status.setText("Add a package source.")
            return
        try:
            self.text = read_text(Path(self.git_mgr.repo_path) / source.path)
            for index, entry in enumerate(entries(source, self.text)):
                item = QListWidgetItem(entry.name)
                item.setToolTip(entry.name)
                item.setData(Qt.ItemDataRole.UserRole, index)
                self.packages.addItem(item)
            self.add.setEnabled(True)
            self.name.setEnabled(True)
            self.name.setPlaceholderText("Nix reference" if source.kind == "Nix" else "Package")
            self._filter()
            self._show_diff(source)
        except (OSError, ValueError, KeyError, StopIteration, yaml.YAMLError) as error:
            self.status.setText(str(error))

    def _filter(self):
        query = self.search.text().casefold()
        for index in range(self.packages.count()):
            item = self.packages.item(index)
            item.setHidden(query not in item.text().casefold())
            if item.isHidden():
                item.setSelected(False)

    def _update_remove(self):
        self.remove.setEnabled(bool(self.packages.selectedItems()))

    def _add_source(self):
        repo = Path(self.git_mgr.repo_path)
        path, _ = QFileDialog.getOpenFileName(
            self, "Package source", str(repo), "Package files (*.txt *.yml *.yaml Brewfile);;All files (*)")
        if not path:
            return
        try:
            relative = Path(path).resolve().relative_to(repo.resolve()).as_posix()
        except ValueError:
            QMessageBox.warning(self, "Package source", "Select a file in this repository.")
            return
        kind = self.kind.currentText()
        try:
            sources = sources_for_file(kind, relative, read_text(path))
            if not sources:
                raise ValueError("No package lists found in this recipe.")
        except (OSError, ValueError, yaml.YAMLError) as error:
            QMessageBox.warning(self, "Package source", str(error))
            return
        key = os.path.realpath(self.git_mgr.repo_path)
        configured = self.config.package_sources.setdefault(key, [])
        configured[:] = [entry for entry in configured if entry["path"] != relative]
        configured.append({"kind": kind, "path": relative})
        ConfigManager.save(self.config)
        self.refresh()
        for index in range(self.source.count()):
            if self.source.itemData(index).path == relative:
                self.source.setCurrentIndex(index)
                break

    def _add_package(self):
        name = self.name.text().strip()
        if not name:
            return
        if "\n" in name or "\r" in name:
            self.status.setText("Enter one package at a time.")
            return
        source = self.source.currentData()
        if source is None:
            return
        if any(entry.name == entry_name(source, name) for entry in entries(source, self.text)):
            self.status.setText("Package already listed.")
            return
        if self._save(add=name):
            self.name.clear()

    def _remove_packages(self):
        selected = [item.data(Qt.ItemDataRole.UserRole) for item in self.packages.selectedItems()]
        if selected:
            self._save(remove=selected)

    def _mirrors(self, source):
        if self.nocblue and source.path.startswith("manifests/"):
            mirror = Path("files/system/usr/share/nocblue") / source.path
            if (Path(self.git_mgr.repo_path) / mirror).is_file():
                return [mirror.as_posix()]
        return []

    def _save(self, **change):
        source = self.source.currentData()
        repo = Path(self.git_mgr.repo_path)
        try:
            # Refresh before editing so an external edit is never overwritten.
            if read_text(repo / source.path) != self.text:
                self._load_source()
                self.status.setText("Source changed. Select the package again.")
                return False
            updated = edit_entries(source, self.text, **change)
            paths = [source.path] + self._mirrors(source)
            originals = {path: read_text(repo / path) for path in paths}
            written = []
            try:
                for path in paths:
                    content = updated
                    if path != source.path and change.get("remove"):
                        # Mirror selections use names because their line order can differ.
                        names = {entries(source, self.text)[index].name for index in change["remove"]}
                        indexes = [index for index, entry in enumerate(entries(source, originals[path])) if entry.name in names]
                        content = edit_entries(source, originals[path], remove=indexes)
                    elif path != source.path:
                        content = originals[path]
                        if change["add"] not in {entry.name for entry in entries(source, content)}:
                            content = edit_entries(source, content, **change)
                    (repo / path).write_bytes(content.encode("utf-8"))
                    written.append(path)
            except OSError:
                for path in written:
                    (repo / path).write_bytes(originals[path].encode("utf-8"))
                raise
            self._load_source()
            self.changed.emit(self._commit_subject())
            self.status.setText("Saved.")
            return True
        except (OSError, ValueError, KeyError, StopIteration, yaml.YAMLError) as error:
            self.status.setText(str(error))
            return False

    def _commit_subject(self):
        added, removed = set(), set()
        for source in self.sources:
            code, before, _ = self.git_mgr._run_git(["show", f"HEAD:{source.path}"])
            try:
                old = {entry.name for entry in entries(source, before)} if code == 0 else set()
                new = {entry.name for entry in entries(source, read_text(Path(self.git_mgr.repo_path) / source.path))}
                added.update(new - old)
                removed.update(old - new)
            except (OSError, ValueError, KeyError, IndexError, StopIteration, yaml.YAMLError):
                continue
        return commit_subject(added, removed)

    def _show_diff(self, source):
        paths = [source.path] + self._mirrors(source)
        self.diff.set_diff(self.git_mgr.get_diff(paths), source.path)
