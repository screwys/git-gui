from PyQt6.QtCore import Qt
from PyQt6.QtGui import (
    QSyntaxHighlighter,
    QTextCharFormat,
    QColor,
    QFont,
    QTextCursor,
)
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QPlainTextEdit,
    QPushButton,
    QLabel,
    QCheckBox,
)


class DiffHighlighter(QSyntaxHighlighter):
    def __init__(self, parent=None):
        super().__init__(parent)
        self.formats = {}
        self._init_formats()

    def _init_formats(self):
        # Added line
        fmt_add = QTextCharFormat()
        fmt_add.setForeground(QColor("#4ade80"))
        fmt_add.setBackground(QColor("#052e16"))
        self.formats["add"] = fmt_add

        # Deleted line
        fmt_del = QTextCharFormat()
        fmt_del.setForeground(QColor("#f87171"))
        fmt_del.setBackground(QColor("#450a0a"))
        self.formats["del"] = fmt_del

        # Chunk line @@
        fmt_chunk = QTextCharFormat()
        fmt_chunk.setForeground(QColor("#38bdf8"))
        fmt_chunk.setFontWeight(QFont.Weight.Bold)
        fmt_chunk.setBackground(QColor("#0c2d48"))
        self.formats["chunk"] = fmt_chunk

        # Header lines
        fmt_hdr = QTextCharFormat()
        fmt_hdr.setForeground(QColor("#94a3b8"))
        fmt_hdr.setFontWeight(QFont.Weight.Bold)
        self.formats["header"] = fmt_hdr

    def highlightBlock(self, text: str):
        if not text:
            return

        if text.startswith("+++") or text.startswith("---"):
            self.setFormat(0, len(text), self.formats["header"])
        elif text.startswith("+"):
            self.setFormat(0, len(text), self.formats["add"])
        elif text.startswith("-"):
            self.setFormat(0, len(text), self.formats["del"])
        elif text.startswith("@@"):
            self.setFormat(0, len(text), self.formats["chunk"])
        elif text.startswith("diff --git") or text.startswith("index ") or text.startswith("new file mode") or text.startswith("deleted file mode"):
            self.setFormat(0, len(text), self.formats["header"])


class DiffViewer(QWidget):
    def __init__(self, parent=None):
        super().__init__(parent)
        self._init_ui()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(4)

        # Header bar
        header = QHBoxLayout()
        header.setContentsMargins(6, 4, 6, 4)

        self.title_label = QLabel("Diff Preview")
        self.title_label.setStyleSheet("color: #e4e4e7; font-weight: bold; font-size: 12px;")
        header.addWidget(self.title_label)

        header.addStretch()

        self.wrap_cb = QCheckBox("Wrap")
        self.wrap_cb.setChecked(False)
        self.wrap_cb.toggled.connect(self._toggle_wrap)
        header.addWidget(self.wrap_cb)

        self.btn_copy = QPushButton("Copy")
        self.btn_copy.setFixedHeight(24)
        self.btn_copy.clicked.connect(self.copy_all)
        header.addWidget(self.btn_copy)

        layout.addLayout(header)

        # Editor
        self.text_edit = QPlainTextEdit()
        self.text_edit.setReadOnly(True)
        font = QFont("monospace", 10)
        font.setStyleHint(QFont.StyleHint.Monospace)
        self.text_edit.setFont(font)
        self.text_edit.setLineWrapMode(QPlainTextEdit.LineWrapMode.NoWrap)
        self.text_edit.setStyleSheet(
            "background-color: #0f1117; color: #e4e4e7; border: 1px solid #27272a; border-radius: 6px; padding: 6px;"
        )
        self.highlighter = DiffHighlighter(self.text_edit.document())
        layout.addWidget(self.text_edit)

    def _toggle_wrap(self, checked: bool):
        mode = QPlainTextEdit.LineWrapMode.WidgetWidth if checked else QPlainTextEdit.LineWrapMode.NoWrap
        self.text_edit.setLineWrapMode(mode)

    def set_diff(self, diff_text: str, title: str = "Diff Preview"):
        self.title_label.setText(title)
        self.text_edit.setPlainText(diff_text if diff_text else "(No differences or clean working tree)")
        self.text_edit.moveCursor(QTextCursor.MoveOperation.Start)

    def copy_all(self):
        self.text_edit.selectAll()
        self.text_edit.copy()
        self.text_edit.moveCursor(QTextCursor.MoveOperation.Start)
