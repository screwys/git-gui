import re
from PyQt6.QtCore import Qt, pyqtSignal
from PyQt6.QtGui import (
    QFont,
    QColor,
    QTextCharFormat,
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


class AnsiColorParser:
    ANSI_ESCAPE = re.compile(r"\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])")

    COLOR_MAP = {
        "30": "#484f58",  # black/gray
        "31": "#f85149",  # red
        "32": "#3fb950",  # green
        "33": "#d29922",  # yellow
        "34": "#58a6ff",  # blue
        "35": "#bc8cff",  # magenta
        "36": "#39c5cf",  # cyan
        "37": "#b1bac4",  # white
        "90": "#6e7681",  # bright black
        "91": "#ff7b72",  # bright red
        "92": "#56d364",  # bright green
        "93": "#e3b341",  # bright yellow
        "94": "#79c0ff",  # bright blue
        "95": "#d2a8ff",  # bright magenta
        "96": "#56d4dd",  # bright cyan
        "97": "#f0f6fc",  # bright white
    }

    @classmethod
    def apply_ansi_text(cls, text_edit: QPlainTextEdit, text: str):
        cursor = text_edit.textCursor()
        cursor.movePosition(QTextCursor.MoveOperation.End)

        # Handle simple \r line rewrites
        if "\r" in text and "\n" not in text:
            cursor.select(QTextCursor.SelectionType.LineUnderCursor)
            cursor.removeSelectedText()
            text = text.replace("\r", "")

        pos = 0
        current_format = QTextCharFormat()
        current_format.setForeground(QColor("#c9d1d9"))

        for match in cls.ANSI_ESCAPE.finditer(text):
            start, end = match.span()
            if start > pos:
                chunk = text[pos:start]
                cursor.insertText(chunk, current_format)

            code_seq = match.group(0)
            if code_seq.startswith("\x1b[") and code_seq.endswith("m"):
                codes = code_seq[2:-1].split(";")
                for code in codes:
                    if code == "" or code == "0":
                        current_format = QTextCharFormat()
                        current_format.setForeground(QColor("#c9d1d9"))
                    elif code == "1":
                        current_format.setFontWeight(QFont.Weight.Bold)
                    elif code in cls.COLOR_MAP:
                        current_format.setForeground(QColor(cls.COLOR_MAP[code]))

            pos = end

        if pos < len(text):
            chunk = text[pos:]
            cursor.insertText(chunk, current_format)


class TerminalWidget(QWidget):
    stop_requested = pyqtSignal()

    def __init__(self, parent=None):
        super().__init__(parent)
        self.auto_scroll = True
        self._init_ui()

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(4)

        # Header Bar
        header = QHBoxLayout()
        header.setContentsMargins(4, 2, 4, 2)
        header.setSpacing(6)

        title = QLabel("Terminal Output")
        title.setStyleSheet("color: #e6edf3; font-weight: 600; font-size: 12px;")
        header.addWidget(title)

        self.status_badge = QLabel("Idle")
        self.status_badge.setStyleSheet(
            "background-color: #21262d; color: #8b949e; border: 1px solid #30363d; border-radius: 4px; padding: 2px 6px; font-size: 11px; font-weight: 500;"
        )
        header.addWidget(self.status_badge)

        header.addStretch()

        self.cb_autoscroll = QCheckBox("Auto-scroll")
        self.cb_autoscroll.setChecked(True)
        self.cb_autoscroll.toggled.connect(self._toggle_autoscroll)
        header.addWidget(self.cb_autoscroll)

        self.btn_copy = QPushButton("Copy")
        self.btn_copy.setFixedHeight(22)
        self.btn_copy.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_copy.clicked.connect(self._copy_all)
        header.addWidget(self.btn_copy)

        self.btn_clear = QPushButton("Clear")
        self.btn_clear.setFixedHeight(22)
        self.btn_clear.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        self.btn_clear.clicked.connect(self.clear_terminal)
        header.addWidget(self.btn_clear)

        self.btn_stop = QPushButton("Stop")
        self.btn_stop.setFixedHeight(22)
        self.btn_stop.setStyleSheet(
            "QPushButton { background-color: #3d1719; border: 1px solid #f85149; color: #ff7b72; font-size: 11px; padding: 1px 8px; font-weight: bold; } "
            "QPushButton:hover { background-color: #da3633; color: white; }"
        )
        self.btn_stop.setEnabled(False)
        self.btn_stop.clicked.connect(self.stop_requested.emit)
        header.addWidget(self.btn_stop)

        layout.addLayout(header)

        # Text Console
        self.console = QPlainTextEdit()
        self.console.setReadOnly(True)
        font = QFont("monospace", 10)
        font.setStyleHint(QFont.StyleHint.Monospace)
        self.console.setFont(font)
        self.console.setLineWrapMode(QPlainTextEdit.LineWrapMode.NoWrap)
        self.console.setVerticalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOn)
        self.console.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAsNeeded)

        self.console.setStyleSheet(
            "QPlainTextEdit { background-color: #0b0e14; color: #c9d1d9; border: 1px solid #30363d; border-radius: 6px; padding: 6px; font-family: monospace; }"
        )
        layout.addWidget(self.console)

    def _toggle_autoscroll(self, enabled: bool):
        self.auto_scroll = enabled

    def append_output(self, text: str):
        AnsiColorParser.apply_ansi_text(self.console, text)
        if self.auto_scroll:
            cursor = self.console.textCursor()
            cursor.movePosition(QTextCursor.MoveOperation.End)
            self.console.setTextCursor(cursor)
            self.console.ensureCursorVisible()

    def set_status(self, text: str, state: str = "idle"):
        self.status_badge.setText(text)
        if state == "running":
            self.status_badge.setStyleSheet(
                "background-color: rgba(56, 139, 253, 0.15); color: #58a6ff; border: 1px solid rgba(56, 139, 253, 0.4); border-radius: 4px; padding: 2px 6px; font-size: 11px; font-weight: 500;"
            )
            self.btn_stop.setEnabled(True)
        elif state == "success":
            self.status_badge.setStyleSheet(
                "background-color: rgba(46, 160, 67, 0.15); color: #3fb950; border: 1px solid rgba(46, 160, 67, 0.4); border-radius: 4px; padding: 2px 6px; font-size: 11px; font-weight: 500;"
            )
            self.btn_stop.setEnabled(False)
        elif state == "failed":
            self.status_badge.setStyleSheet(
                "background-color: rgba(248, 81, 73, 0.15); color: #f85149; border: 1px solid rgba(248, 81, 73, 0.4); border-radius: 4px; padding: 2px 6px; font-size: 11px; font-weight: 500;"
            )
            self.btn_stop.setEnabled(False)
        else:
            self.status_badge.setStyleSheet(
                "background-color: #21262d; color: #8b949e; border: 1px solid #30363d; border-radius: 4px; padding: 2px 6px; font-size: 11px; font-weight: 500;"
            )
            self.btn_stop.setEnabled(False)

    def clear_terminal(self):
        self.console.clear()

    def _copy_all(self):
        self.console.selectAll()
        self.console.copy()
        cursor = self.console.textCursor()
        cursor.movePosition(QTextCursor.MoveOperation.End)
        self.console.setTextCursor(cursor)
