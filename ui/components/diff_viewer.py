import re
from typing import List, Dict, Optional
from PyQt6.QtCore import Qt, QRect, QSize
from PyQt6.QtGui import (
    QPainter,
    QColor,
    QFont,
    QTextFormat,
    QTextCursor,
    QSyntaxHighlighter,
    QTextCharFormat,
)
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QPlainTextEdit,
    QTextEdit,
    QPushButton,
    QLabel,
    QCheckBox,
)


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
        self.chunk_fmt = QTextCharFormat()
        self.chunk_fmt.setForeground(QColor("#79c0ff"))
        self.chunk_fmt.setFontWeight(QFont.Weight.Bold)

        self.hdr_fmt = QTextCharFormat()
        self.hdr_fmt.setForeground(QColor("#8b949e"))

        self.add_fmt = QTextCharFormat()
        self.add_fmt.setForeground(QColor("#e6edf3"))

        self.del_fmt = QTextCharFormat()
        self.del_fmt.setForeground(QColor("#e6edf3"))

    def highlightBlock(self, text: str):
        if not text:
            return
        if text.startswith("@@"):
            self.setFormat(0, len(text), self.chunk_fmt)
        elif text.startswith("diff --git") or text.startswith("index ") or text.startswith("+++") or text.startswith("---"):
            self.setFormat(0, len(text), self.hdr_fmt)
        elif text.startswith("+"):
            self.setFormat(0, len(text), self.add_fmt)
        elif text.startswith("-"):
            self.setFormat(0, len(text), self.del_fmt)


class DiffCodeEditor(QPlainTextEdit):
    def __init__(self, parent=None):
        super().__init__(parent)
        self.line_meta: List[Dict] = []
        self.line_number_area = LineNumberArea(self)
        self.highlighter = DiffSyntaxHighlighter(self.document())

        self.blockCountChanged.connect(self.update_line_number_area_width)
        self.updateRequest.connect(self.update_line_number_area)

        font = QFont("monospace", 10)
        font.setStyleHint(QFont.StyleHint.Monospace)
        self.setFont(font)
        self.setReadOnly(True)
        self.setLineWrapMode(QPlainTextEdit.LineWrapMode.NoWrap)

        self.setVerticalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOn)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAsNeeded)

        self.setStyleSheet(
            "QPlainTextEdit { background-color: #0b0e14; color: #e6edf3; border: 1px solid #30363d; border-radius: 6px; padding: 2px; font-family: monospace; }"
        )
        self.update_line_number_area_width(0)

    def line_number_area_width(self) -> int:
        digits = 4
        space = 16 + self.fontMetrics().horizontalAdvance("9") * digits
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

    def set_diff_content(self, diff_text: str):
        self.setPlainText(diff_text)
        self._parse_and_highlight()

    def _parse_and_highlight(self):
        lines = self.toPlainText().splitlines()
        self.line_meta = []

        old_no = 0
        new_no = 0
        extra_selections = []

        color_add_bg = QColor(19, 46, 27, 210)     # Muted dark green background #132e1b
        color_del_bg = QColor(61, 23, 25, 210)     # Muted dark red background #3d1719
        color_chunk_bg = QColor(14, 29, 46, 160)   # Muted dark cyan/blue for @@

        block = self.document().firstBlock()
        block_idx = 0

        for line in lines:
            meta = {"type": "hdr", "num": None}
            if line.startswith("@@"):
                m = re.match(r"^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@", line)
                if m:
                    old_no = int(m.group(1))
                    new_no = int(m.group(2))
                meta = {"type": "chunk", "num": None}
                sel = QTextEdit.ExtraSelection()
                sel.format.setBackground(color_chunk_bg)
                sel.format.setProperty(QTextFormat.Property.FullWidthSelection, True)
                sel.cursor = QTextCursor(block)
                extra_selections.append(sel)
            elif line.startswith("-") and not line.startswith("---"):
                meta = {"type": "del", "num": old_no}
                old_no += 1
                sel = QTextEdit.ExtraSelection()
                sel.format.setBackground(color_del_bg)
                sel.format.setProperty(QTextFormat.Property.FullWidthSelection, True)
                sel.cursor = QTextCursor(block)
                extra_selections.append(sel)
            elif line.startswith("+") and not line.startswith("+++"):
                meta = {"type": "add", "num": new_no}
                new_no += 1
                sel = QTextEdit.ExtraSelection()
                sel.format.setBackground(color_add_bg)
                sel.format.setProperty(QTextFormat.Property.FullWidthSelection, True)
                sel.cursor = QTextCursor(block)
                extra_selections.append(sel)
            elif line.startswith(" "):
                meta = {"type": "ctx", "num": new_no}
                old_no += 1
                new_no += 1

            self.line_meta.append(meta)
            block = block.next()
            block_idx += 1

        self.setExtraSelections(extra_selections)
        self.line_number_area.update()

    def line_number_area_paint_event(self, event):
        painter = QPainter(self.line_number_area)
        painter.fillRect(event.rect(), QColor("#080a0f"))

        # Vertical divider border
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
                meta = self.line_meta[block_number] if block_number < len(self.line_meta) else None
                if meta and meta["num"] is not None:
                    line_str = str(meta["num"])
                    if meta["type"] == "del":
                        painter.setPen(QColor("#f85149"))  # red line number
                    elif meta["type"] == "add":
                        painter.setPen(QColor("#3fb950"))  # green line number
                    else:
                        painter.setPen(QColor("#6e7681"))  # gray line number

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

            block = block.next()
            top = bottom
            bottom = top + int(self.blockBoundingRect(block).height())
            block_number += 1


class DiffViewer(QWidget):
    def __init__(self, parent=None):
        super().__init__(parent)
        self._init_ui()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(4)

        # Header bar matching image 2: breadcrumb and +add -del
        header = QHBoxLayout()
        header.setContentsMargins(4, 2, 4, 2)
        header.setSpacing(8)

        self.file_icon = QLabel("📄")
        self.file_icon.setStyleSheet("color: #7d8590; font-size: 11px;")
        # Keep icon subtle without emoji feel
        self.file_icon.setText("")
        self.file_icon.setFixedWidth(4)
        header.addWidget(self.file_icon)

        self.title_label = QLabel("No changes selected")
        self.title_label.setStyleSheet("color: #e6edf3; font-weight: 600; font-size: 12px; font-family: monospace;")
        header.addWidget(self.title_label)

        self.stats_label = QLabel("")
        self.stats_label.setStyleSheet("font-size: 11px; font-family: monospace;")
        header.addWidget(self.stats_label)

        header.addStretch()

        self.wrap_cb = QCheckBox("Wrap")
        self.wrap_cb.setChecked(False)
        self.wrap_cb.toggled.connect(self._toggle_wrap)
        header.addWidget(self.wrap_cb)

        self.btn_copy = QPushButton("Copy")
        self.btn_copy.setFixedHeight(22)
        self.btn_copy.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_copy.clicked.connect(self.copy_all)
        header.addWidget(self.btn_copy)

        layout.addLayout(header)

        # Editor
        self.editor = DiffCodeEditor()
        layout.addWidget(self.editor)

    def _toggle_wrap(self, checked: bool):
        mode = QPlainTextEdit.LineWrapMode.WidgetWidth if checked else QPlainTextEdit.LineWrapMode.NoWrap
        self.editor.setLineWrapMode(mode)

    def set_diff(self, diff_text: str, title: str = "Diff Preview", additions: int = 0, deletions: int = 0):
        self.title_label.setText(title)
        stat_text = ""
        if additions > 0:
            stat_text += f"<span style='color: #3fb950;'>+{additions}</span> "
        if deletions > 0:
            stat_text += f"<span style='color: #f85149;'>-{deletions}</span>"
        self.stats_label.setText(stat_text)
        self.editor.set_diff_content(diff_text if diff_text else "(Clean working tree or no diff)")

    def copy_all(self):
        self.editor.selectAll()
        self.editor.copy()
        cursor = self.editor.textCursor()
        cursor.movePosition(QTextCursor.MoveOperation.Start)
        self.editor.setTextCursor(cursor)
