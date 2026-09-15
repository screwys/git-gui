import re
from PyQt6.QtCore import Qt, pyqtSignal
from PyQt6.QtGui import QTextCursor, QTextCharFormat, QColor, QFont
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QPlainTextEdit,
    QPushButton,
    QLabel,
    QCheckBox,
)


class TerminalWidget(QWidget):
    stop_requested = pyqtSignal()

    COLOR_MAP = {
        30: QColor("#27272a"),  # Black
        31: QColor("#ef4444"),  # Red
        32: QColor("#22c55e"),  # Green
        33: QColor("#eab308"),  # Yellow
        34: QColor("#3b82f6"),  # Blue
        35: QColor("#a855f7"),  # Magenta
        36: QColor("#06b6d4"),  # Cyan
        37: QColor("#f4f4f5"),  # White
        90: QColor("#71717a"),  # Bright Black / Gray
        91: QColor("#f87171"),  # Bright Red
        92: QColor("#4ade80"),  # Bright Green
        93: QColor("#fde047"),  # Bright Yellow
        94: QColor("#60a5fa"),  # Bright Blue
        95: QColor("#c084fc"),  # Bright Magenta
        96: QColor("#22d3ee"),  # Bright Cyan
        97: QColor("#ffffff"),  # Bright White
    }

    BG_COLOR_MAP = {
        40: QColor("#18181b"),
        41: QColor("#7f1d1d"),
        42: QColor("#14532d"),
        43: QColor("#713f12"),
        44: QColor("#1e3a8a"),
        45: QColor("#581c87"),
        46: QColor("#164e63"),
        47: QColor("#3f3f46"),
    }

    def __init__(self, parent=None):
        super().__init__(parent)
        self._init_ui()
        self.default_fg = QColor("#f4f4f5")
        self.default_bg = QColor("#09090b")
        self.current_format = QTextCharFormat()
        self.current_format.setForeground(self.default_fg)
        self.current_format.setFontFamily("monospace")
        self.current_format.setFontPointSize(10)
        self.ansi_regex = re.compile(r"\x1b\[([0-9;]*)m")

    def _init_ui(self):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(4)

        # Toolbar
        toolbar = QHBoxLayout()
        toolbar.setContentsMargins(6, 4, 6, 4)

        self.status_label = QLabel("Idle")
        self.status_label.setStyleSheet(
            "background-color: #27272a; color: #a1a1aa; border-radius: 4px; padding: 2px 8px; font-weight: bold; font-size: 11px;"
        )
        toolbar.addWidget(self.status_label)

        toolbar.addStretch()

        self.auto_scroll_cb = QCheckBox("Auto-scroll")
        self.auto_scroll_cb.setChecked(True)
        toolbar.addWidget(self.auto_scroll_cb)

        self.btn_copy = QPushButton("Copy")
        self.btn_copy.setFixedHeight(24)
        self.btn_copy.clicked.connect(self.copy_all)
        toolbar.addWidget(self.btn_copy)

        self.btn_clear = QPushButton("Clear")
        self.btn_clear.setFixedHeight(24)
        self.btn_clear.clicked.connect(self.clear_terminal)
        toolbar.addWidget(self.btn_clear)

        self.btn_stop = QPushButton("Stop")
        self.btn_stop.setFixedHeight(24)
        self.btn_stop.setEnabled(False)
        self.btn_stop.setStyleSheet("background-color: #7f1d1d; color: #fecaca; border: 1px solid #991b1b;")
        self.btn_stop.clicked.connect(self.stop_requested.emit)
        toolbar.addWidget(self.btn_stop)

        layout.addLayout(toolbar)

        # Text Console
        self.text_edit = QPlainTextEdit()
        self.text_edit.setReadOnly(True)
        self.text_edit.setMaximumBlockCount(10000)
        font = QFont("monospace", 10)
        font.setStyleHint(QFont.StyleHint.Monospace)
        self.text_edit.setFont(font)
        self.text_edit.setStyleSheet(
            "background-color: #09090b; color: #f4f4f5; border: 1px solid #27272a; border-radius: 6px; padding: 6px;"
        )
        layout.addWidget(self.text_edit)

    def set_status(self, text: str, state: str = "idle"):
        self.status_label.setText(text)
        if state == "running":
            self.status_label.setStyleSheet(
                "background-color: #1e3a8a; color: #93c5fd; border-radius: 4px; padding: 2px 8px; font-weight: bold; font-size: 11px;"
            )
            self.btn_stop.setEnabled(True)
        elif state == "success":
            self.status_label.setStyleSheet(
                "background-color: #14532d; color: #86efac; border-radius: 4px; padding: 2px 8px; font-weight: bold; font-size: 11px;"
            )
            self.btn_stop.setEnabled(False)
        elif state == "failed":
            self.status_label.setStyleSheet(
                "background-color: #7f1d1d; color: #fca5a5; border-radius: 4px; padding: 2px 8px; font-weight: bold; font-size: 11px;"
            )
            self.btn_stop.setEnabled(False)
        else:
            self.status_label.setStyleSheet(
                "background-color: #27272a; color: #a1a1aa; border-radius: 4px; padding: 2px 8px; font-weight: bold; font-size: 11px;"
            )
            self.btn_stop.setEnabled(False)

    def append_output(self, text: str):
        cursor = self.text_edit.textCursor()
        cursor.movePosition(QTextCursor.MoveOperation.End)

        # Handle carriage returns for in-place line updates (e.g. progress bars, git fetch)
        lines = text.split("\r")
        for i, chunk in enumerate(lines):
            if i > 0:
                # Remove last line
                cursor.movePosition(QTextCursor.MoveOperation.StartOfLine, QTextCursor.MoveMode.KeepAnchor)
                cursor.removeSelectedText()

            # Parse ANSI escape sequences in this chunk
            pos = 0
            for match in self.ansi_regex.finditer(chunk):
                raw_text = chunk[pos : match.start()]
                if raw_text:
                    cursor.insertText(raw_text, self.current_format)

                # Process ANSI codes
                code_str = match.group(1)
                codes = [int(c) for c in code_str.split(";") if c.isdigit()] if code_str else [0]
                self._apply_ansi_codes(codes)
                pos = match.end()

            remaining = chunk[pos:]
            if remaining:
                cursor.insertText(remaining, self.current_format)

        if self.auto_scroll_cb.isChecked():
            self.text_edit.verticalScrollBar().setValue(self.text_edit.verticalScrollBar().maximum())

    def _apply_ansi_codes(self, codes):
        if not codes or 0 in codes:
            self.current_format.setForeground(self.default_fg)
            self.current_format.setBackground(QColor(Qt.GlobalColor.transparent))
            self.current_format.setFontWeight(QFont.Weight.Normal)

        for code in codes:
            if code == 1:
                self.current_format.setFontWeight(QFont.Weight.Bold)
            elif code == 2:
                self.current_format.setForeground(QColor("#71717a"))
            elif code in self.COLOR_MAP:
                self.current_format.setForeground(self.COLOR_MAP[code])
            elif code in self.BG_COLOR_MAP:
                self.current_format.setBackground(self.BG_COLOR_MAP[code])

    def clear_terminal(self):
        self.text_edit.clear()

    def copy_all(self):
        self.text_edit.selectAll()
        self.text_edit.copy()
        cursor = self.text_edit.textCursor()
        cursor.movePosition(QTextCursor.MoveOperation.End)
        self.text_edit.setTextCursor(cursor)
