import os
import re
from typing import List, Dict, Optional
from PyQt6.QtCore import Qt, QRect, QSize, pyqtSignal
from PyQt6.QtGui import (
    QPainter,
    QColor,
    QFont,
    QTextCursor,
    QSyntaxHighlighter,
    QTextCharFormat,
    QKeySequence,
    QShortcut,
)
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QPlainTextEdit,
    QPushButton,
    QLabel,
    QCheckBox,
    QMessageBox,
    QSizePolicy,
)
from core.config import AppConfig, ConfigManager
from ui.components.toast import ToastNotification


class LineNumberArea(QWidget):
    def __init__(self, editor: "DiffCodeEditor"):
        super().__init__(editor)
        self.code_editor = editor

    def sizeHint(self) -> QSize:
        return QSize(self.code_editor.line_number_area_width(), 0)

    def paintEvent(self, event):
        self.code_editor.line_number_area_paint_event(event)


class DiffSyntaxHighlighter(QSyntaxHighlighter):
    def __init__(self, parent=None):
        super().__init__(parent)
        self.is_diff_mode = True

        # Header lines (diff --git, index, +++, ---)
        self.hdr_fmt = QTextCharFormat()
        self.hdr_fmt.setForeground(QColor("#8b949e"))

        # Chunk header (@@ ... @@)
        self.chunk_fmt = QTextCharFormat()
        self.chunk_fmt.setForeground(QColor("#79c0ff"))
        self.chunk_fmt.setBackground(QColor(14, 29, 46, 180))
        self.chunk_fmt.setFontWeight(QFont.Weight.Bold)

        # Added lines (+)
        self.add_fmt = QTextCharFormat()
        self.add_fmt.setForeground(QColor("#7ee787"))
        self.add_fmt.setBackground(QColor(19, 46, 27, 180))

        # Deleted lines (-)
        self.del_fmt = QTextCharFormat()
        self.del_fmt.setForeground(QColor("#f85149"))
        self.del_fmt.setBackground(QColor(61, 23, 25, 180))

    def highlightBlock(self, text: str):
        if not self.is_diff_mode or not text:
            return
        if text.startswith("@@"):
            self.setFormat(0, len(text), self.chunk_fmt)
        elif text.startswith(("diff --git", "index ", "+++", "---")):
            self.setFormat(0, len(text), self.hdr_fmt)
        elif text.startswith("+"):
            self.setFormat(0, len(text), self.add_fmt)
        elif text.startswith("-"):
            self.setFormat(0, len(text), self.del_fmt)


class DiffCodeEditor(QPlainTextEdit):
    editing_requested = pyqtSignal()

    def __init__(self, parent=None):
        super().__init__(parent)
        self.line_meta: List[Dict] = []
        self.is_diff_mode = True
        self.line_number_area = LineNumberArea(self)
        self.highlighter = DiffSyntaxHighlighter(self.document())

        self.blockCountChanged.connect(self.update_line_number_area_width)
        self.updateRequest.connect(self.update_line_number_area)

        font = QFont("monospace", 10)
        font.setStyleHint(QFont.StyleHint.Monospace)
        self.setFont(font)
        self.setReadOnly(False)
        self.setLineWrapMode(QPlainTextEdit.LineWrapMode.WidgetWidth)

        self.setVerticalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOn)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAsNeeded)

        self.setStyleSheet(
            "QPlainTextEdit { background-color: #0b0e14; color: #e6edf3; border: 1px solid #30363d; border-radius: 6px; padding: 2px; font-family: monospace; }"
        )
        self.update_line_number_area_width(0)

    def _prepare_edit(self):
        if self.is_diff_mode and not self.isReadOnly():
            self.editing_requested.emit()

    def keyPressEvent(self, event):
        if (event.text() and event.text().isprintable()) or event.key() in (
            Qt.Key.Key_Backspace, Qt.Key.Key_Delete, Qt.Key.Key_Return,
            Qt.Key.Key_Enter, Qt.Key.Key_Tab,
        ) or event.matches(QKeySequence.StandardKey.Cut):
            self._prepare_edit()
        super().keyPressEvent(event)

    def insertFromMimeData(self, source):
        self._prepare_edit()
        super().insertFromMimeData(source)

    def inputMethodEvent(self, event):
        if event.commitString() or event.preeditString():
            self._prepare_edit()
        super().inputMethodEvent(event)

    def line_number_area_width(self) -> int:
        digits = max(3, len(str(max(1, self.blockCount()))))
        space = 14 + self.fontMetrics().horizontalAdvance("9") * digits
        return max(space, 42)

    def update_line_number_area_width(self, _):
        self.setViewportMargins(self.line_number_area_width(), 0, 0, 0)

    def update_line_number_area(self, rect, dy):
        if dy:
            self.line_number_area.scroll(0, dy)
        else:
            self.line_number_area.update(0, rect.y(), self.line_number_area.width(), rect.height())
        if rect.contains(self.viewport().rect()):
            self.update_line_number_area_width(0)

    def resizeEvent(self, event):
        super().resizeEvent(event)
        cr = self.contentsRect()
        self.line_number_area.setGeometry(
            QRect(cr.left(), cr.top(), self.line_number_area_width(), cr.height())
        )

    def set_content(self, text: str, is_diff: bool = True):
        if self.is_diff_mode == is_diff and self.toPlainText() == text:
            return
        self.is_diff_mode = is_diff
        self.highlighter.is_diff_mode = is_diff
        self.setPlainText(text)
        if is_diff:
            self._fast_parse_diff_metadata(text)
        else:
            self.line_meta = []
            self.line_number_area.update()

    def _fast_parse_diff_metadata(self, text: str):
        """Lightning-fast O(N) line number mapping without touching QTextCursor."""
        lines = text.splitlines()
        self.line_meta = []
        old_no = 0
        new_no = 0

        for line in lines:
            if not line:
                self.line_meta.append({"type": "ctx", "num": None})
                continue
            first_char = line[0]
            if first_char == "@" and line.startswith("@@"):
                m = re.match(r"^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@", line)
                if m:
                    old_no = int(m.group(1))
                    new_no = int(m.group(2))
                self.line_meta.append({"type": "chunk", "num": None})
            elif first_char == "-" and not line.startswith("---"):
                self.line_meta.append({"type": "del", "num": old_no})
                old_no += 1
            elif first_char == "+" and not line.startswith("+++"):
                self.line_meta.append({"type": "add", "num": new_no})
                new_no += 1
            elif first_char == " ":
                self.line_meta.append({"type": "ctx", "num": new_no})
                old_no += 1
                new_no += 1
            else:
                self.line_meta.append({"type": "hdr", "num": None})

        self.line_number_area.update()

    def line_number_area_paint_event(self, event):
        painter = QPainter(self.line_number_area)
        painter.fillRect(event.rect(), QColor("#080a0f"))

        divider_x = self.line_number_area.width() - 1
        painter.setPen(QColor("#21262d"))
        painter.drawLine(divider_x, event.rect().top(), divider_x, event.rect().bottom())

        block = self.firstVisibleBlock()
        block_number = block.blockNumber()
        top = int(self.blockBoundingGeometry(block).translated(self.contentOffset()).top())
        bottom = top + int(self.blockBoundingRect(block).height())

        font = self.font()
        font.setPointSize(9)
        painter.setFont(font)

        gutter_width = self.line_number_area.width() - 8

        while block.isValid() and top <= event.rect().bottom():
            if block.isVisible() and bottom >= event.rect().top():
                if self.is_diff_mode:
                    meta = self.line_meta[block_number] if block_number < len(self.line_meta) else None
                    if meta and meta["num"] is not None:
                        line_str = str(meta["num"])
                        if meta["type"] == "del":
                            painter.setPen(QColor("#f85149"))
                        elif meta["type"] == "add":
                            painter.setPen(QColor("#3fb950"))
                        else:
                            painter.setPen(QColor("#6e7681"))

                        painter.drawText(
                            0, top, gutter_width, self.fontMetrics().height(),
                            Qt.AlignmentFlag.AlignRight, line_str
                        )
                    elif meta and meta["type"] == "chunk":
                        painter.setPen(QColor("#388bfd"))
                        painter.drawText(
                            0, top, gutter_width, self.fontMetrics().height(),
                            Qt.AlignmentFlag.AlignRight, "@@"
                        )
                else:
                    line_str = str(block_number + 1)
                    painter.setPen(QColor("#6e7681"))
                    painter.drawText(
                        0, top, gutter_width, self.fontMetrics().height(),
                        Qt.AlignmentFlag.AlignRight, line_str
                    )

            block = block.next()
            top = bottom
            bottom = top + int(self.blockBoundingRect(block).height())
            block_number += 1


class DiffViewer(QWidget):
    file_saved = pyqtSignal(str)

    def __init__(self, config: Optional[AppConfig] = None, repo_path: str = "", parent=None):
        super().__init__(parent)
        self.config = config or AppConfig()
        self.repo_path = repo_path
        self.current_rel_path: Optional[str] = None
        self.current_diff_text = ""
        self.view_mode = "diff"  # "diff" or "file"
        self._is_dirty = False
        self._revert_backup_content: Optional[str] = None
        self._revert_path: Optional[str] = None
        self._init_ui()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(4)

        # Header bar
        header = QHBoxLayout()
        header.setContentsMargins(4, 2, 4, 2)
        header.setSpacing(8)

        self.title_label = QLabel("No changes selected")
        self.title_label.setSizePolicy(QSizePolicy.Policy.Ignored, QSizePolicy.Policy.Fixed)
        self.title_label.setStyleSheet("color: #e6edf3; font-weight: 600; font-size: 12px; font-family: monospace;")
        header.addWidget(self.title_label, 1)

        self.stats_label = QLabel("")
        self.stats_label.setStyleSheet("font-size: 11px; font-family: monospace;")
        header.addWidget(self.stats_label)

        self.dirty_badge = QLabel("")
        self.dirty_badge.setStyleSheet("color: #d29922; font-size: 11px; font-weight: bold;")
        header.addWidget(self.dirty_badge)

        layout.addLayout(header)
        controls = QHBoxLayout()
        controls.setContentsMargins(4, 0, 4, 2)
        controls.setSpacing(4)

        # Mode toggles
        self.btn_diff_mode = QPushButton("Diff")
        self.btn_diff_mode.setFixedHeight(22)
        self.btn_diff_mode.setCheckable(True)
        self.btn_diff_mode.setChecked(True)
        self.btn_diff_mode.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_diff_mode.clicked.connect(self._switch_to_diff_mode)
        controls.addWidget(self.btn_diff_mode)

        self.btn_file_mode = QPushButton("File")
        self.btn_file_mode.setFixedHeight(22)
        self.btn_file_mode.setCheckable(True)
        self.btn_file_mode.setChecked(False)
        self.btn_file_mode.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_file_mode.clicked.connect(self._switch_to_file_mode)
        controls.addWidget(self.btn_file_mode)

        # Save button
        self.btn_save = QPushButton("Save")
        self.btn_save.setFixedHeight(22)
        self.btn_save.setStyleSheet(
            "QPushButton { background-color: #238636; border: 1px solid #2ea043; color: white; font-size: 11px; padding: 1px 8px; font-weight: bold; } "
            "QPushButton:hover { background-color: #2ea043; }"
        )
        self.btn_save.setToolTip("Save changes to file (Ctrl+S)")
        self.btn_save.clicked.connect(self.save_current)
        controls.addWidget(self.btn_save)

        # Reset button
        self.btn_reset = QPushButton("Reset")
        self.btn_reset.setFixedHeight(22)
        self.btn_reset.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_reset.setToolTip("Discard unsaved in-editor changes")
        self.btn_reset.clicked.connect(self.reset_current)
        controls.addWidget(self.btn_reset)
        controls.addStretch()

        # Wrap checkbox
        self.wrap_cb = QCheckBox("Wrap")
        self.wrap_cb.setChecked(self.config.wrap_diff)
        self.wrap_cb.toggled.connect(self._on_wrap_toggled)
        controls.addWidget(self.wrap_cb)

        self.btn_copy = QPushButton("Copy")
        self.btn_copy.setFixedHeight(22)
        self.btn_copy.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_copy.clicked.connect(self.copy_all)
        controls.addWidget(self.btn_copy)

        layout.addLayout(controls)

        # Editor
        self.editor = DiffCodeEditor()
        self.editor.editing_requested.connect(self._start_file_edit)
        wrap_mode = QPlainTextEdit.LineWrapMode.WidgetWidth if self.config.wrap_diff else QPlainTextEdit.LineWrapMode.NoWrap
        self.editor.setLineWrapMode(wrap_mode)
        self.editor.document().contentsChanged.connect(self._on_editor_modified)
        layout.addWidget(self.editor)

        # Toast overlay for post-save revert
        self.toast = ToastNotification(self)
        self.toast.action_clicked.connect(self._revert_saved_file)

        # Shortcut Ctrl+S
        shortcut = QShortcut(QKeySequence("Ctrl+S"), self)
        shortcut.activated.connect(self.save_current)

    def resizeEvent(self, event):
        super().resizeEvent(event)
        self.toast.reposition()

    def _on_wrap_toggled(self, checked: bool):
        mode = QPlainTextEdit.LineWrapMode.WidgetWidth if checked else QPlainTextEdit.LineWrapMode.NoWrap
        self.editor.setLineWrapMode(mode)
        self.config.wrap_diff = checked
        ConfigManager.save(self.config)

    def _on_editor_modified(self):
        if not self._is_dirty:
            self._is_dirty = True
            self.dirty_badge.setText("● Modified")

    def set_diff(self, diff_text: str, title: str = "Diff Preview", additions: int = 0, deletions: int = 0, rel_path: Optional[str] = None):
        same_file = self.current_rel_path == rel_path
        self.current_rel_path = rel_path
        self.current_diff_text = diff_text
        self.title_label.setText(title)
        self.title_label.setToolTip(title)

        stat_text = ""
        if additions > 0:
            stat_text += f"<span style='color: #3fb950;'>+{additions}</span> "
        if deletions > 0:
            stat_text += f"<span style='color: #f85149;'>-{deletions}</span>"
        self.stats_label.setText(stat_text)

        has_file = bool(rel_path and self.repo_path and os.path.exists(os.path.join(self.repo_path, rel_path)))
        self.btn_file_mode.setEnabled(has_file)

        if self.view_mode == "file" and has_file:
            if not (same_file and self._is_dirty):
                self._load_file_content()
        else:
            self._switch_to_diff_mode()

    def _switch_to_diff_mode(self):
        self.view_mode = "diff"
        self.btn_diff_mode.setChecked(True)
        self.btn_file_mode.setChecked(False)
        self.btn_save.hide()
        self.btn_reset.hide()
        self.editor.blockSignals(True)
        self.editor.set_content(self.current_diff_text if self.current_diff_text else "(Clean working tree or no diff)", is_diff=True)
        self.editor.blockSignals(False)
        self._is_dirty = False
        self.dirty_badge.setText("")

    def _switch_to_file_mode(self):
        if not self.current_rel_path or not self._load_file_content():
            return
        self.view_mode = "file"
        self.btn_diff_mode.setChecked(False)
        self.btn_file_mode.setChecked(True)
        self.btn_save.show()
        self.btn_reset.show()

    def _start_file_edit(self):
        if not self.btn_file_mode.isEnabled() or not self.current_rel_path:
            return
        cursor = self.editor.textCursor()

        def file_position(position):
            block = self.editor.document().findBlock(position)
            index = block.blockNumber()
            column = position - block.position()
            meta = self.editor.line_meta[index] if index < len(self.editor.line_meta) else None
            if meta and meta["type"] in ("ctx", "add") and meta["num"] is not None:
                return meta["num"] - 1, max(0, column - 1)
            for following in self.editor.line_meta[index + 1:]:
                if following["type"] in ("ctx", "add") and following["num"] is not None:
                    return following["num"] - 1, 0
            return self.editor.blockCount(), 0

        anchor = file_position(cursor.anchor())
        position = file_position(cursor.position())
        self._switch_to_file_mode()
        if self.view_mode != "file":
            return
        cursor = self.editor.textCursor()
        for (line, column), mode in (
            (anchor, QTextCursor.MoveMode.MoveAnchor),
            (position, QTextCursor.MoveMode.KeepAnchor),
        ):
            block = self.editor.document().findBlockByNumber(line)
            if not block.isValid():
                block = self.editor.document().lastBlock()
                column = len(block.text())
            cursor.setPosition(block.position() + min(column, len(block.text())), mode)
        self.editor.setTextCursor(cursor)

    def _load_file_content(self):
        if not self.current_rel_path or not self.repo_path:
            return
        full_path = os.path.join(self.repo_path, self.current_rel_path)
        if not os.path.isfile(full_path):
            return
        try:
            with open(full_path, "r", encoding="utf-8", errors="replace") as f:
                content = f.read()
            self.editor.blockSignals(True)
            self.editor.set_content(content, is_diff=False)
            self.editor.blockSignals(False)
            self._is_dirty = False
            self.dirty_badge.setText("")
            return True
        except Exception as e:
            QMessageBox.warning(self, "Read Error", f"Could not read file {full_path}:\n{e}")

    def reset_current(self):
        """Discards current in-memory edits and resets to the on-disk/original state."""
        if self.view_mode == "file":
            self._load_file_content()
            self._is_dirty = False
            self.dirty_badge.setText("")
        else:
            self._switch_to_diff_mode()

    def save_current(self):
        if not self.current_rel_path or not self.repo_path:
            self._is_dirty = False
            self.dirty_badge.setText("")
            return

        if self.view_mode != "file" or not self._is_dirty:
            return
        if QMessageBox.question(
            self, "Save file", f"Save changes to {self.current_rel_path}?",
            QMessageBox.StandardButton.Save | QMessageBox.StandardButton.Cancel,
            QMessageBox.StandardButton.Cancel,
        ) != QMessageBox.StandardButton.Save:
            return

        full_path = os.path.join(self.repo_path, self.current_rel_path)
        try:
            if os.path.exists(full_path):
                with open(full_path, "r", encoding="utf-8", errors="replace") as f:
                    self._revert_backup_content = f.read()

            content = self.editor.toPlainText()
            with open(full_path, "w", encoding="utf-8") as f:
                f.write(content)
            self._revert_path = full_path

            self._is_dirty = False
            self.dirty_badge.setText("Saved")
            self.file_saved.emit(self.current_rel_path)

            self.toast.show_toast(
                "File saved",
                action_text="Revert",
                duration_ms=8000,
            )
        except Exception as e:
            QMessageBox.critical(self, "Save Failed", f"Could not save {full_path}:\n{e}")

    def _revert_saved_file(self):
        if not self._revert_path or self._revert_backup_content is None:
            return
        full_path = self._revert_path
        try:
            with open(full_path, "w", encoding="utf-8") as f:
                f.write(self._revert_backup_content)
            self._revert_backup_content = None
            self._revert_path = None
            if self.current_rel_path and full_path == os.path.join(self.repo_path, self.current_rel_path):
                self._load_file_content()
                self._is_dirty = False
                self.dirty_badge.setText("Reverted")
            self.file_saved.emit(os.path.relpath(full_path, self.repo_path))
        except Exception as e:
            QMessageBox.critical(self, "Revert Failed", f"Could not revert {full_path}:\n{e}")

    def copy_all(self):
        self.editor.selectAll()
        self.editor.copy()
        cursor = self.editor.textCursor()
        cursor.movePosition(QTextCursor.MoveOperation.Start)
        self.editor.setTextCursor(cursor)
