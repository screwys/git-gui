from typing import List, Set
from PyQt6.QtCore import Qt, pyqtSignal, QPointF
from PyQt6.QtGui import QPainter, QColor, QPen
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QTreeWidget,
    QTreeWidgetItem,
    QHeaderView,
    QAbstractButton,
    QPushButton,
    QLabel,
    QSizePolicy,
)
from core.git_manager import GitFileItem


class MinimalCheckBox(QAbstractButton):
    def __init__(self, checked: bool = True, parent=None):
        super().__init__(parent)
        self.setCheckable(True)
        self.setChecked(checked)
        self.setFixedSize(16, 16)
        self.setCursor(Qt.CursorShape.PointingHandCursor)

    def paintEvent(self, event):
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)

        rect = self.rect().adjusted(1, 1, -1, -1)
        r = 3.0

        if self.isChecked():
            bg = QColor("#1f6feb") if not self.underMouse() else QColor("#388bfd")
            border = QColor("#58a6ff")
            painter.setBrush(bg)
            painter.setPen(border)
            painter.drawRoundedRect(rect, r, r)

            # Draw white checkmark
            pen = QPen(QColor("#ffffff"))
            pen.setWidthF(1.8)
            pen.setCapStyle(Qt.PenCapStyle.RoundCap)
            pen.setJoinStyle(Qt.PenJoinStyle.RoundJoin)
            painter.setPen(pen)

            p1 = QPointF(rect.left() + 3.2, rect.top() + 7.0)
            p2 = QPointF(rect.left() + 5.8, rect.top() + 10.2)
            p3 = QPointF(rect.left() + 11.2, rect.top() + 4.2)
            painter.drawLine(p1, p2)
            painter.drawLine(p2, p3)
        else:
            bg = QColor("#0d1117") if not self.underMouse() else QColor("#161b22")
            border = QColor("#30363d") if not self.underMouse() else QColor("#8b949e")
            painter.setBrush(bg)
            painter.setPen(border)
            painter.drawRoundedRect(rect, r, r)


class FileListWidget(QWidget):
    file_selected = pyqtSignal(str)
    selection_changed = pyqtSignal(list)
    view_diff_requested = pyqtSignal(list)
    publish_requested = pyqtSignal()

    def __init__(self, parent=None):
        super().__init__(parent)
        self.items: List[GitFileItem] = []
        self.checked_paths: Set[str] = set()
        self._select_all_on_refresh = True
        self._updating = False
        self._init_ui()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(4)

        # Header
        header = QHBoxLayout()
        header.setContentsMargins(4, 2, 4, 2)
        header.setSpacing(6)

        self.title_lbl = QLabel("Changes (0)")
        self.title_lbl.setSizePolicy(QSizePolicy.Policy.Ignored, QSizePolicy.Policy.Fixed)
        self.title_lbl.setStyleSheet("color: #e6edf3; font-weight: 600; font-size: 12px;")
        header.addWidget(self.title_lbl, 1)

        self.stats_lbl = QLabel()
        self.stats_lbl.setStyleSheet("font-size: 11px; font-family: monospace;")
        header.addWidget(self.stats_lbl)

        self.select_all_cb = MinimalCheckBox(True, self)
        self.select_all_cb.setToolTip("Select / Deselect All")
        self.select_all_cb.toggled.connect(self._on_select_all_toggled)
        header.addWidget(self.select_all_cb)
        layout.addLayout(header)

        actions = QHBoxLayout()
        actions.setContentsMargins(4, 0, 4, 2)
        actions.setSpacing(6)

        self.btn_publish = QPushButton("Publish PR")
        self.btn_publish.setObjectName("primaryButton")
        self.btn_publish.setFixedHeight(22)
        self.btn_publish.setStyleSheet("font-size: 11px; padding: 1px 10px; font-weight: 600;")
        self.btn_publish.clicked.connect(self.publish_requested.emit)
        actions.addWidget(self.btn_publish)

        self.btn_view_diff = QPushButton("View Diff")
        self.btn_view_diff.setFixedHeight(22)
        self.btn_view_diff.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_view_diff.clicked.connect(self._on_view_diff_clicked)
        actions.addWidget(self.btn_view_diff)
        actions.addStretch()
        layout.addLayout(actions)

        # List Widget
        self.list_widget = QTreeWidget()
        self.list_widget.setColumnCount(4)
        self.list_widget.setHeaderHidden(True)
        self.list_widget.setRootIsDecorated(False)
        self.list_widget.setTextElideMode(Qt.TextElideMode.ElideMiddle)
        self.list_widget.header().setStretchLastSection(False)
        for column in (0, 2, 3):
            self.list_widget.header().setSectionResizeMode(column, QHeaderView.ResizeMode.ResizeToContents)
        self.list_widget.header().setSectionResizeMode(1, QHeaderView.ResizeMode.Stretch)
        self.list_widget.setStyleSheet(
            "QTreeWidget { background-color: #0b0e14; border: 1px solid #30363d; border-radius: 6px; } "
            "QTreeWidget::item { padding: 2px; border-bottom: 1px solid #161b22; } "
            "QTreeWidget::item:selected { background-color: #1c2128; } "
            "QTreeWidget::item:hover { background-color: #13171e; }"
        )
        self.list_widget.setVerticalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAsNeeded)
        self.list_widget.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOff)
        self.list_widget.itemClicked.connect(self._on_item_clicked)
        self.list_widget.itemChanged.connect(self._on_row_checked)
        layout.addWidget(self.list_widget)

    def set_files(self, items: List[GitFileItem]):
        self._updating = True
        current = self.list_widget.currentItem()
        current_path = current.data(0, Qt.ItemDataRole.UserRole) if current else None
        scroll = self.list_widget.verticalScrollBar().value()
        self.items = items
        self.list_widget.clear()

        total_adds = sum(i.additions for i in items)
        total_dels = sum(i.deletions for i in items)
        self.title_lbl.setText(f"Changes ({len(items)})")
        self.stats_lbl.setText(
            f"<span style='color: #3fb950;'>+{total_adds}</span> "
            f"<span style='color: #f85149;'>-{total_dels}</span>"
        )

        if self._select_all_on_refresh:
            self.checked_paths = {i.path for i in items}
        else:
            current_paths = {i.path for i in items}
            self.checked_paths = self.checked_paths.intersection(current_paths)
        if items:
            self._select_all_on_refresh = len(self.checked_paths) == len(items)

        self.select_all_cb.blockSignals(True)
        self.select_all_cb.setChecked(len(self.checked_paths) == len(items) and len(items) > 0)
        self.select_all_cb.blockSignals(False)

        for item in items:
            path_text = item.filename if item.dirname == "." else f"{item.filename}  {item.dirname}"
            list_item = QTreeWidgetItem(self.list_widget, [
                item.display_status, path_text,
                f"+{item.additions}" if item.additions else ("" if item.deletions else "0"),
                f"-{item.deletions}" if item.deletions else "",
            ])
            list_item.setData(0, Qt.ItemDataRole.UserRole, item.path)
            list_item.setToolTip(1, item.path)
            list_item.setCheckState(0, Qt.CheckState.Checked if item.path in self.checked_paths else Qt.CheckState.Unchecked)
            status_color = {"A": "#3fb950", "U": "#3fb950", "D": "#f85149", "R": "#a371f7"}.get(item.display_status, "#d29922")
            list_item.setForeground(0, QColor(status_color))
            for column, color in ((2, "#3fb950"), (3, "#f85149")):
                list_item.setForeground(column, QColor(color))
                list_item.setTextAlignment(column, Qt.AlignmentFlag.AlignRight | Qt.AlignmentFlag.AlignVCenter)
            if item.path == current_path:
                self.list_widget.setCurrentItem(list_item)

        self.list_widget.verticalScrollBar().setValue(scroll)
        self._updating = False

    def _on_row_checked(self, item: QTreeWidgetItem, column: int):
        if self._updating or column != 0:
            return
        path = item.data(0, Qt.ItemDataRole.UserRole)
        checked = item.checkState(0) == Qt.CheckState.Checked
        if checked:
            self.checked_paths.add(path)
        else:
            self.checked_paths.discard(path)

        self.select_all_cb.blockSignals(True)
        self.select_all_cb.setChecked(len(self.checked_paths) == len(self.items) and len(self.items) > 0)
        self.select_all_cb.blockSignals(False)
        if self.items:
            self._select_all_on_refresh = len(self.checked_paths) == len(self.items)

        self.selection_changed.emit(list(self.checked_paths))

    def _on_select_all_toggled(self, is_checked: bool):
        if self._updating:
            return
        self._select_all_on_refresh = is_checked
        self._updating = True
        if is_checked:
            self.checked_paths = {i.path for i in self.items}
        else:
            self.checked_paths.clear()

        for idx in range(self.list_widget.topLevelItemCount()):
            item = self.list_widget.topLevelItem(idx)
            item.setCheckState(0, Qt.CheckState.Checked if is_checked else Qt.CheckState.Unchecked)

        self._updating = False
        self.selection_changed.emit(list(self.checked_paths))

    def _on_item_clicked(self, list_item: QTreeWidgetItem, column: int):
        if column == 0:
            return
        path = list_item.data(0, Qt.ItemDataRole.UserRole)
        if path:
            self.file_selected.emit(path)

    def _on_view_diff_clicked(self):
        self.view_diff_requested.emit(list(self.checked_paths))

    def get_selected_paths(self) -> List[str]:
        return list(self.checked_paths)
