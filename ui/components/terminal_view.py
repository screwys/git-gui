import re
from typing import Optional
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
    QLineEdit,
    QPushButton,
    QLabel,
    QCheckBox,
    QTabWidget,
)
from core.runner import ProcessRunner


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
    command_executed = pyqtSignal(str)

    def __init__(self, repo_path: str = "", parent=None):
        super().__init__(parent)
        self.repo_path = repo_path
        self.auto_scroll = True
        self.interactive_runner = ProcessRunner(self)
        self._history = []
        self._history_idx = 0
        self._init_ui()
        self._wire_interactive()

    def _init_ui(self):
        root_layout = QVBoxLayout(self)
        root_layout.setContentsMargins(0, 0, 0, 0)
        root_layout.setSpacing(0)

        self.tabs = QTabWidget()
        self.tabs.setStyleSheet(
            "QTabWidget::tab-bar { alignment: left; } "
            "QTabBar::tab { font-size: 11px; padding: 4px 12px; margin-right: 2px; }"
        )

        # Tab 1: Task Output (Build / Release / Script logs)
        output_tab = QWidget()
        out_layout = QVBoxLayout(output_tab)
        out_layout.setContentsMargins(0, 4, 0, 0)
        out_layout.setSpacing(4)

        # Header Bar for Task Output
        header = QHBoxLayout()
        header.setContentsMargins(4, 2, 4, 2)
        header.setSpacing(6)

        title = QLabel("Task Output")
        title.setStyleSheet("color: #e6edf3; font-weight: 600; font-size: 11px;")
        header.addWidget(title)

        self.status_badge = QLabel("Idle")
        self.status_badge.setStyleSheet(
            "background-color: #21262d; color: #8b949e; border: 1px solid #30363d; border-radius: 4px; padding: 2px 6px; font-size: 11px; font-weight: 500;"
        )
        header.addWidget(self.status_badge)

        header.addStretch()

        self.cb_wrap = QCheckBox("Wrap")
        self.cb_wrap.setChecked(True)
        self.cb_wrap.toggled.connect(self._toggle_wrap)
        header.addWidget(self.cb_wrap)

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

        out_layout.addLayout(header)

        # Output console (Wrap by default)
        self.console = QPlainTextEdit()
        self.console.setReadOnly(True)
        font = QFont("monospace", 10)
        font.setStyleHint(QFont.StyleHint.Monospace)
        self.console.setFont(font)
        self.console.setLineWrapMode(QPlainTextEdit.LineWrapMode.WidgetWidth)
        self.console.setVerticalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOn)
        self.console.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAsNeeded)
        self.console.setStyleSheet(
            "QPlainTextEdit { background-color: #0b0e14; color: #c9d1d9; border: 1px solid #30363d; border-radius: 6px; padding: 6px; font-family: monospace; }"
        )
        out_layout.addWidget(self.console)
        self.tabs.addTab(output_tab, "Output")

        # Tab 2: Interactive Terminal (Command runner)
        shell_tab = QWidget()
        shell_layout = QVBoxLayout(shell_tab)
        shell_layout.setContentsMargins(0, 4, 0, 0)
        shell_layout.setSpacing(4)

        shell_header = QHBoxLayout()
        shell_header.setContentsMargins(4, 2, 4, 2)
        shell_header.setSpacing(6)

        shell_title = QLabel("Interactive Command Runner")
        shell_title.setStyleSheet("color: #e6edf3; font-weight: 600; font-size: 11px;")
        shell_header.addWidget(shell_title)

        self.shell_status_badge = QLabel("Ready")
        self.shell_status_badge.setStyleSheet(
            "background-color: #21262d; color: #7ee787; border: 1px solid #30363d; border-radius: 4px; padding: 2px 6px; font-size: 11px; font-weight: 500;"
        )
        shell_header.addWidget(self.shell_status_badge)

        shell_header.addStretch()

        self.cb_shell_wrap = QCheckBox("Wrap")
        self.cb_shell_wrap.setChecked(True)
        self.cb_shell_wrap.toggled.connect(self._toggle_shell_wrap)
        shell_header.addWidget(self.cb_shell_wrap)

        btn_shell_clear = QPushButton("Clear")
        btn_shell_clear.setFixedHeight(22)
        btn_shell_clear.setStyleSheet("font-size: 11px; padding: 1px 8px;")
        btn_shell_clear.clicked.connect(self._clear_shell)
        shell_header.addWidget(btn_shell_clear)

        self.btn_shell_stop = QPushButton("Stop")
        self.btn_shell_stop.setFixedHeight(22)
        self.btn_shell_stop.setStyleSheet(
            "QPushButton { background-color: #3d1719; border: 1px solid #f85149; color: #ff7b72; font-size: 11px; padding: 1px 8px; font-weight: bold; } "
            "QPushButton:hover { background-color: #da3633; color: white; }"
        )
        self.btn_shell_stop.setEnabled(False)
        self.btn_shell_stop.clicked.connect(self.interactive_runner.stop)
        shell_header.addWidget(self.btn_shell_stop)

        shell_layout.addLayout(shell_header)

        # Shell Console
        self.shell_console = QPlainTextEdit()
        self.shell_console.setReadOnly(True)
        self.shell_console.setFont(font)
        self.shell_console.setLineWrapMode(QPlainTextEdit.LineWrapMode.WidgetWidth)
        self.shell_console.setVerticalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOn)
        self.shell_console.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAsNeeded)
        self.shell_console.setStyleSheet(
            "QPlainTextEdit { background-color: #0b0e14; color: #c9d1d9; border: 1px solid #30363d; border-radius: 6px; padding: 6px; font-family: monospace; }"
        )
        shell_layout.addWidget(self.shell_console)

        # Shell Input Row
        input_row = QHBoxLayout()
        input_row.setContentsMargins(2, 2, 2, 2)
        input_row.setSpacing(6)

        prompt_lbl = QLabel("$")
        prompt_lbl.setStyleSheet("color: #58a6ff; font-weight: bold; font-family: monospace; font-size: 13px;")
        input_row.addWidget(prompt_lbl)

        self.cmd_input = QLineEdit()
        self.cmd_input.setPlaceholderText("Type command (e.g. git status, just restart, cargo test)...")
        self.cmd_input.setStyleSheet("font-family: monospace; font-size: 11px;")
        self.cmd_input.returnPressed.connect(self._run_custom_command)
        input_row.addWidget(self.cmd_input, 1)

        self.btn_run = QPushButton("Run")
        self.btn_run.setFixedHeight(26)
        self.btn_run.setStyleSheet("font-size: 11px; font-weight: 600; padding: 2px 12px;")
        self.btn_run.clicked.connect(self._run_custom_command)
        input_row.addWidget(self.btn_run)

        shell_layout.addLayout(input_row)

        self.tabs.addTab(shell_tab, "Terminal")
        root_layout.addWidget(self.tabs)

    def _wire_interactive(self):
        self.interactive_runner.output_received.connect(self._on_shell_output)
        self.interactive_runner.started.connect(self._on_shell_started)
        self.interactive_runner.finished.connect(self._on_shell_finished)
        self.interactive_runner.error_occurred.connect(lambda msg: self._on_shell_output(f"\n[Error: {msg}]\n"))

    def _toggle_wrap(self, enabled: bool):
        mode = QPlainTextEdit.LineWrapMode.WidgetWidth if enabled else QPlainTextEdit.LineWrapMode.NoWrap
        self.console.setLineWrapMode(mode)

    def _toggle_shell_wrap(self, enabled: bool):
        mode = QPlainTextEdit.LineWrapMode.WidgetWidth if enabled else QPlainTextEdit.LineWrapMode.NoWrap
        self.shell_console.setLineWrapMode(mode)

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
            self.tabs.setCurrentIndex(0)
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

    def _clear_shell(self):
        self.shell_console.clear()

    def _run_custom_command(self):
        cmd = self.cmd_input.text().strip()
        if not cmd:
            return
        if self.interactive_runner.is_running:
            return

        self._history.append(cmd)
        self._history_idx = len(self._history)
        self.cmd_input.clear()

        # Print command header in console
        cursor = self.shell_console.textCursor()
        cursor.movePosition(QTextCursor.MoveOperation.End)
        fmt = QTextCharFormat()
        fmt.setForeground(QColor("#58a6ff"))
        fmt.setFontWeight(QFont.Weight.Bold)
        cursor.insertText(f"\n$ {cmd}\n", fmt)
        self.shell_console.setTextCursor(cursor)
        self.shell_console.ensureCursorVisible()

        cwd = self.repo_path if self.repo_path else None
        self.interactive_runner.start("bash", ["-c", cmd], cwd=cwd)
        self.command_executed.emit(cmd)

    def _on_shell_started(self):
        self.btn_shell_stop.setEnabled(True)
        self.shell_status_badge.setText("Running")
        self.shell_status_badge.setStyleSheet(
            "background-color: rgba(56, 139, 253, 0.15); color: #58a6ff; border: 1px solid rgba(56, 139, 253, 0.4); border-radius: 4px; padding: 2px 6px; font-size: 11px; font-weight: 500;"
        )

    def _on_shell_output(self, text: str):
        AnsiColorParser.apply_ansi_text(self.shell_console, text)
        cursor = self.shell_console.textCursor()
        cursor.movePosition(QTextCursor.MoveOperation.End)
        self.shell_console.setTextCursor(cursor)
        self.shell_console.ensureCursorVisible()

    def _on_shell_finished(self, exit_code: int):
        self.btn_shell_stop.setEnabled(False)
        status_text = "Completed" if exit_code == 0 else f"Exit {exit_code}"
        color = "#3fb950" if exit_code == 0 else "#f85149"
        bg = "rgba(46, 160, 67, 0.15)" if exit_code == 0 else "rgba(248, 81, 73, 0.15)"
        self.shell_status_badge.setText(status_text)
        self.shell_status_badge.setStyleSheet(
            f"background-color: {bg}; color: {color}; border: 1px solid {color}; border-radius: 4px; padding: 2px 6px; font-size: 11px; font-weight: 500;"
        )
