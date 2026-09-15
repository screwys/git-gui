from typing import List, Set
from PyQt6.QtCore import Qt, pyqtSignal, QPointF
from PyQt6.QtGui import QPainter, QColor, QPen
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QListWidget,
    QListWidgetItem,
    QAbstractButton,
    QPushButton,
    QLabel,
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


class FileRowWidget(QWidget):
    checked_toggled = pyqtSignal(bool)
    diff_clicked = pyqtSignal(str)

    def __init__(self, item: GitFileItem, is_checked: bool = True, parent=None):
        super().__init__(parent)
        self.item = item
        self._init_ui(is_checked)

    def _init_ui(self, is_checked: bool):
        layout = QHBoxLayout(self)
        layout.setContentsMargins(6, 2, 6, 2)
        layout.setSpacing(6)
        layout.setAlignment(Qt.AlignmentFlag.AlignVCenter)

        # Minimal vector checkbox
        self.checkbox = MinimalCheckBox(is_checked, self)
        self.checkbox.toggled.connect(self.checked_toggled.emit)
        layout.addWidget(self.checkbox)

        # Status badge (GitHub/Zed style badge)
        status_char = self.item.display_status
        self.status_lbl = QLabel(status_char)
        self.status_lbl.setFixedSize(16, 16)
        self.status_lbl.setAlignment(Qt.AlignmentFlag.AlignCenter)

        if status_char in ("A", "U"):
            color = "#3fb950"
            bg = "rgba(46, 160, 67, 0.18)"
            border = "rgba(46, 160, 67, 0.4)"
        elif status_char == "D":
            color = "#f85149"
            bg = "rgba(248, 81, 73, 0.18)"
            border = "rgba(248, 81, 73, 0.4)"
        elif status_char == "R":
            color = "#a371f7"
            bg = "rgba(163, 113, 247, 0.18)"
            border = "rgba(163, 113, 247, 0.4)"
        else:
            color = "#d29922"
            bg = "rgba(187, 128, 9, 0.18)"
            border = "rgba(187, 128, 9, 0.4)"

        self.status_lbl.setStyleSheet(
            f"color: {color}; background-color: {bg}; border: 1px solid {border}; border-radius: 3px; font-weight: bold; font-size: 10px; font-family: monospace;"
        )
        layout.addWidget(self.status_lbl)

        # Filename
        self.name_lbl = QLabel(self.item.filename)
        self.name_lbl.setStyleSheet("color: #e6edf3; font-weight: 500; font-size: 12px;")
        layout.addWidget(self.name_lbl)

        # Dirname (dimmed)
        if self.item.dirname != ".":
            self.dir_lbl = QLabel(self.item.dirname)
            self.dir_lbl.setStyleSheet("color: #7d8590; font-size: 11px;")
            layout.addWidget(self.dir_lbl)

        layout.addStretch()

        # Diff stat badge (+X -Y) - clickable button style to directly show diff
        stat_text = ""
        if self.item.additions > 0:
            stat_text += f"<span style='color: #3fb950;'>+{self.item.additions}</span> "
        if self.item.deletions > 0:
            stat_text += f"<span style='color: #f85149;'>-{self.item.deletions}</span>"
        if not stat_text:
            stat_text = "<span style='color: #484f58;'>0</span>"

        self.btn_stat = QPushButton()
        self.btn_stat.setText("")
        self.btn_stat.setCursor(Qt.CursorShape.PointingHandCursor)
        self.btn_stat.setToolTip(f"View diff for {self.item.filename}")
        self.btn_stat.setStyleSheet(
            "QPushButton { "
            "  background-color: transparent; "
            "  border: 1px solid transparent; "
            "  border-radius: 3px; "
            "  padding: 1px 4px; "
            "} "
            "QPushButton:hover { "
            "  background-color: #21262d; "
            "  border-color: #30363d; "
            "}"
        )
        btn_layout = QHBoxLayout(self.btn_stat)
        btn_layout.setContentsMargins(2, 0, 2, 0)
        lbl_stat = QLabel()
        lbl_stat.setTextFormat(Qt.TextFormat.RichText)
        lbl_stat.setText(stat_text)
        lbl_stat.setStyleSheet("font-size: 11px; font-family: monospace;")
        btn_layout.addWidget(lbl_stat)

        self.btn_stat.clicked.connect(lambda: self.diff_clicked.emit(self.item.path))
        layout.addWidget(self.btn_stat)


class FileListWidget(QWidget):
    file_selected = pyqtSignal(str)
    selection_changed = pyqtSignal(list)
    view_diff_requested = pyqtSignal(list)
    publish_requested = pyqtSignal()

    def __init__(self, parent=None):
        super().__init__(parent)
        self.items: List[GitFileItem] = []
        self.checked_paths: Set[str] = set()
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
        self.title_lbl.setStyleSheet("color: #e6edf3; font-weight: 600; font-size: 12px;")
        header.addWidget(self.title_lbl)

        header.addStretch()

        # Publish PR button placed on top right next to View Diff
        self.btn_publish = QPushButton("Publish PR")
        self.btn_publish.setObjectName("primaryButton")
        self.btn_publish.setFixedHeight(22)
        self.btn_publish.setStyleSheet("font-size: 11px; padding: 1px 10px; font-weight: 600;")
        self.btn_publish.clicked.connect(self.publish_requested.emit)
        header.addWidget(self.btn_publish)

        self.btn_view_diff = QPushButton("View Diff")
        self.btn_view_diff.setFixedHeight(22)
        self.btn_view_diff.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_view_diff.clicked.connect(self._on_view_diff_clicked)
        header.addWidget(self.btn_view_diff)

        self.select_all_cb = MinimalCheckBox(True, self)
        self.select_all_cb.setToolTip("Select / Deselect All")
        self.select_all_cb.toggled.connect(self._on_select_all_toggled)
        header.addWidget(self.select_all_cb)

        layout.addLayout(header)

        # List Widget
        self.list_widget = QListWidget()
        self.list_widget.setStyleSheet(
            "QListWidget { background-color: #0b0e14; border: 1px solid #30363d; border-radius: 6px; } "
            "QListWidget::item { padding: 0px; border-bottom: 1px solid #161b22; } "
            "QListWidget::item:selected { background-color: #1c2128; } "
            "QListWidget::item:hover { background-color: #13171e; }"
        )
        self.list_widget.setVerticalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAsNeeded)
        self.list_widget.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOff)
        self.list_widget.itemClicked.connect(self._on_item_clicked)
        layout.addWidget(self.list_widget)

    def set_files(self, items: List[GitFileItem]):
        self._updating = True
        self.items = items
        self.list_widget.clear()

        total_adds = sum(i.additions for i in items)
        total_dels = sum(i.deletions for i in items)
        self.title_lbl.setText(f"Changes ({len(items)})")
        stat_suffix = ""
        if total_adds > 0 or total_dels > 0:
            stat_suffix = f" (+{total_adds} -{total_dels})"
        self.btn_view_diff.setText(f"View Diff{stat_suffix}")

        if not self.checked_paths:
            self.checked_paths = {i.path for i in items}
        else:
            current_paths = {i.path for i in items}
            self.checked_paths = self.checked_paths.intersection(current_paths)

        self.select_all_cb.blockSignals(True)
        self.select_all_cb.setChecked(len(self.checked_paths) == len(items) and len(items) > 0)
        self.select_all_cb.blockSignals(False)

        for item in items:
            list_item = QListWidgetItem(self.list_widget)
            list_item.setData(Qt.ItemDataRole.UserRole, item.path)

            row_widget = FileRowWidget(item, is_checked=(item.path in self.checked_paths))
            row_widget.checked_toggled.connect(
                lambda checked, p=item.path: self._on_row_checked(p, checked)
            )
            row_widget.diff_clicked.connect(self._on_individual_diff_clicked)

            list_item.setSizeHint(row_widget.sizeHint())
            self.list_widget.addItem(list_item)
            self.list_widget.setItemWidget(list_item, row_widget)

        self._updating = False

    def _on_individual_diff_clicked(self, path: str):
        for idx in range(self.list_widget.count()):
            item = self.list_widget.item(idx)
            if item.data(Qt.ItemDataRole.UserRole) == path:
                self.list_widget.setCurrentItem(item)
                break
        self.file_selected.emit(path)

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

    def _on_select_all_toggled(self, is_checked: bool):
        if self._updating:
            return
        self._updating = True
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
        paths = list(self.checked_paths) if self.checked_paths else [i.path for i in self.items]
        self.view_diff_requested.emit(paths)

    def get_selected_paths(self) -> List[str]:
        return list(self.checked_paths)
