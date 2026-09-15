from typing import List, Set
from PyQt6.QtCore import Qt, pyqtSignal
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QListWidget,
    QListWidgetItem,
    QCheckBox,
    QPushButton,
    QLabel,
)
from core.git_manager import GitFileItem


class FileRowWidget(QWidget):
    checked_toggled = pyqtSignal(bool)

    def __init__(self, item: GitFileItem, is_checked: bool = True, parent=None):
        super().__init__(parent)
        self.item = item
        self._init_ui(is_checked)

    def _init_ui(self, is_checked: bool):
        layout = QHBoxLayout(self)
        layout.setContentsMargins(6, 4, 6, 4)
        layout.setSpacing(8)

        # Checkbox
        self.checkbox = QCheckBox()
        self.checkbox.setChecked(is_checked)
        self.checkbox.toggled.connect(self.checked_toggled.emit)
        layout.addWidget(self.checkbox)

        # Status badge
        status_char = self.item.display_status
        self.status_lbl = QLabel(status_char)
        self.status_lbl.setFixedWidth(18)
        self.status_lbl.setAlignment(Qt.AlignmentFlag.AlignCenter)

        # Color based on status
        if status_char in ("A", "U"):
            st_color = "#22c55e"  # Green
            st_bg = "#14532d"
        elif status_char == "D":
            st_color = "#ef4444"  # Red
            st_bg = "#7f1d1d"
        elif status_char == "R":
            st_color = "#a855f7"  # Purple
            st_bg = "#581c87"
        else:
            st_color = "#eab308"  # Yellow/Orange for M
            st_bg = "#713f12"

        self.status_lbl.setStyleSheet(
            f"color: {st_color}; background-color: {st_bg}; border-radius: 3px; font-weight: bold; font-size: 11px; padding: 1px;"
        )
        layout.addWidget(self.status_lbl)

        # Filename (primary)
        self.name_lbl = QLabel(self.item.filename)
        self.name_lbl.setStyleSheet("color: #f4f4f5; font-weight: 500; font-size: 12px;")
        layout.addWidget(self.name_lbl)

        # Dirname (secondary / dim)
        if self.item.dirname != ".":
            self.dir_lbl = QLabel(self.item.dirname)
            self.dir_lbl.setStyleSheet("color: #71717a; font-size: 11px;")
            layout.addWidget(self.dir_lbl)

        layout.addStretch()

        # Diff stat badge (+X -Y)
        stat_text = ""
        if self.item.additions > 0:
            stat_text += f"<span style='color: #22c55e;'>+{self.item.additions}</span> "
        if self.item.deletions > 0:
            stat_text += f"<span style='color: #ef4444;'>-{self.item.deletions}</span>"
        if not stat_text:
            stat_text = "<span style='color: #71717a;'>±0</span>"

        self.stat_lbl = QLabel()
        self.stat_lbl.setTextFormat(Qt.TextFormat.RichText)
        self.stat_lbl.setText(stat_text)
        self.stat_lbl.setStyleSheet("font-size: 11px; font-family: monospace;")
        layout.addWidget(self.stat_lbl)


class FileListWidget(QWidget):
    file_selected = pyqtSignal(str)  # emitted when a single file is clicked/selected
    selection_changed = pyqtSignal(list)  # list of checked file paths
    view_diff_requested = pyqtSignal(list)  # list of paths to view diff for

    def __init__(self, parent=None):
        super().__init__(parent)
        self.items: List[GitFileItem] = []
        self.checked_paths: Set[str] = set()
        self._updating = False
        self._init_ui()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(6)

        # Header
        header = QHBoxLayout()
        header.setContentsMargins(4, 2, 4, 2)

        self.title_lbl = QLabel("Changes (0)")
        self.title_lbl.setStyleSheet("color: #f4f4f5; font-weight: bold; font-size: 13px;")
        header.addWidget(self.title_lbl)

        header.addStretch()

        self.btn_view_diff = QPushButton("± View Diff")
        self.btn_view_diff.setFixedHeight(26)
        self.btn_view_diff.setStyleSheet("background-color: #27272a; color: #f4f4f5; border: 1px solid #3f3f46; border-radius: 4px; padding: 2px 8px; font-size: 11px;")
        self.btn_view_diff.clicked.connect(self._on_view_diff_clicked)
        header.addWidget(self.btn_view_diff)

        self.select_all_cb = QCheckBox()
        self.select_all_cb.setToolTip("Select / Deselect All")
        self.select_all_cb.setChecked(True)
        self.select_all_cb.stateChanged.connect(self._on_select_all_toggled)
        header.addWidget(self.select_all_cb)

        layout.addLayout(header)

        # List Widget
        self.list_widget = QListWidget()
        self.list_widget.setStyleSheet(
            "QListWidget { background-color: #12141a; border: 1px solid #27272a; border-radius: 6px; } "
            "QListWidget::item { padding: 0px; border-bottom: 1px solid #1c1f26; } "
            "QListWidget::item:selected { background-color: #1e293b; } "
            "QListWidget::item:hover { background-color: #182030; }"
        )
        self.list_widget.itemClicked.connect(self._on_item_clicked)
        layout.addWidget(self.list_widget)

    def set_files(self, items: List[GitFileItem]):
        self._updating = True
        self.items = items
        self.list_widget.clear()

        # Update title & total diff stat
        total_adds = sum(i.additions for i in items)
        total_dels = sum(i.deletions for i in items)
        self.title_lbl.setText(f"Changes ({len(items)})")
        self.btn_view_diff.setText(f"± View Diff +{total_adds} -{total_dels}")

        # Default all checked if paths were already checked or first load
        if not self.checked_paths:
            self.checked_paths = {i.path for i in items}
        else:
            current_paths = {i.path for i in items}
            self.checked_paths = self.checked_paths.intersection(current_paths)

        self.select_all_cb.setChecked(len(self.checked_paths) == len(items) and len(items) > 0)

        for item in items:
            list_item = QListWidgetItem(self.list_widget)
            list_item.setData(Qt.ItemDataRole.UserRole, item.path)

            row_widget = FileRowWidget(item, is_checked=(item.path in self.checked_paths))
            row_widget.checked_toggled.connect(
                lambda checked, p=item.path: self._on_row_checked(p, checked)
            )

            list_item.setSizeHint(row_widget.sizeHint())
            self.list_widget.addItem(list_item)
            self.list_widget.setItemWidget(list_item, row_widget)

        self._updating = False

    def _on_row_checked(self, path: str, checked: bool):
        if self._updating:
            return
        if checked:
            self.checked_paths.add(path)
        else:
            self.checked_paths.discard(path)

        self.select_all_cb.blockSignals(True)
        self.select_all_cb.setChecked(len(self.checked_paths) == len(self.items) and len(self.items) > 0)
        self.select_all_cb.blockSignals(False)

        self.selection_changed.emit(list(self.checked_paths))

    def _on_select_all_toggled(self, state):
        if self._updating:
            return
        self._updating = True
        is_checked = (state == Qt.CheckState.Checked.value or state == 2)
        if is_checked:
            self.checked_paths = {i.path for i in self.items}
        else:
            self.checked_paths.clear()

        for idx in range(self.list_widget.count()):
            item = self.list_widget.item(idx)
            row_widget = self.list_widget.itemWidget(item)
            if isinstance(row_widget, FileRowWidget):
                row_widget.checkbox.blockSignals(True)
                row_widget.checkbox.setChecked(is_checked)
                row_widget.checkbox.blockSignals(False)

        self._updating = False
        self.selection_changed.emit(list(self.checked_paths))

    def _on_item_clicked(self, list_item: QListWidgetItem):
        path = list_item.data(Qt.ItemDataRole.UserRole)
        if path:
            self.file_selected.emit(path)

    def _on_view_diff_clicked(self):
        # View diff for checked paths, or all paths if none checked
        paths = list(self.checked_paths) if self.checked_paths else [i.path for i in self.items]
        self.view_diff_requested.emit(paths)

    def get_selected_paths(self) -> List[str]:
        return list(self.checked_paths)
