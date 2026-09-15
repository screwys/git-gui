DARK_STYLESHEET = """
QMainWindow, QWidget {
    background-color: #0d1117;
    color: #c9d1d9;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    font-size: 12px;
}

QTabWidget::pane {
    border: 1px solid #30363d;
    background-color: #161b22;
    border-radius: 6px;
}

QTabBar::tab {
    background-color: #161b22;
    color: #8b949e;
    padding: 6px 14px;
    border-top-left-radius: 6px;
    border-top-right-radius: 6px;
    border: 1px solid transparent;
    font-weight: 500;
    font-size: 12px;
}

QTabBar::tab:selected {
    background-color: #21262d;
    color: #f0f6fc;
    border-color: #30363d;
    border-bottom-color: #21262d;
}

QTabBar::tab:hover:!selected {
    color: #c9d1d9;
    background-color: #1c2128;
}

QLineEdit, QTextEdit, QPlainTextEdit {
    background-color: #0d1117;
    color: #c9d1d9;
    border: 1px solid #30363d;
    border-radius: 5px;
    padding: 4px 8px;
    selection-background-color: #1f6feb;
    font-size: 12px;
}

QLineEdit:focus, QTextEdit:focus, QPlainTextEdit:focus {
    border: 1px solid #58a6ff;
    outline: none;
}

QPushButton {
    background-color: #21262d;
    color: #c9d1d9;
    border: 1px solid #30363d;
    border-radius: 5px;
    padding: 4px 10px;
    font-weight: 500;
    font-size: 12px;
}

QPushButton:hover {
    background-color: #30363d;
    border-color: #8b949e;
}

QPushButton:pressed {
    background-color: #161b22;
}

QPushButton:disabled {
    background-color: #161b22;
    color: #484f58;
    border-color: #21262d;
}

QPushButton#primaryButton {
    background-color: #238636;
    color: #ffffff;
    border: 1px solid #2ea043;
    font-weight: 600;
}

QPushButton#primaryButton:hover {
    background-color: #2ea043;
}

QPushButton#primaryButton:pressed {
    background-color: #238636;
}

QPushButton#accentButton {
    background-color: #1f6feb;
    color: #ffffff;
    border: 1px solid #388bfd;
    font-weight: 600;
}

QPushButton#accentButton:hover {
    background-color: #388bfd;
}

QPushButton#windowControl {
    background: transparent;
    border: none;
    color: #8b949e;
    font-size: 13px;
    padding: 2px 8px;
    border-radius: 4px;
}

QPushButton#windowControl:hover {
    background-color: #30363d;
    color: #f0f6fc;
}

QPushButton#windowControlClose {
    background: transparent;
    border: none;
    color: #8b949e;
    font-size: 14px;
    font-weight: bold;
    padding: 2px 10px;
    border-radius: 4px;
}

QPushButton#windowControlClose:hover {
    background-color: #da3633;
    color: #ffffff;
}

QPushButton#windowControlClose:pressed {
    background-color: #b62324;
}

QComboBox {
    background-color: #21262d;
    color: #c9d1d9;
    border: 1px solid #30363d;
    border-radius: 5px;
    padding: 3px 8px;
    font-size: 12px;
}

QComboBox:hover {
    border-color: #8b949e;
}

QComboBox::drop-down {
    border: none;
}

QComboBox QAbstractItemView {
    background-color: #161b22;
    color: #c9d1d9;
    selection-background-color: #1f6feb;
    border: 1px solid #30363d;
}

QCheckBox {
    color: #c9d1d9;
    spacing: 6px;
    font-size: 12px;
}

QCheckBox::indicator {
    width: 14px;
    height: 14px;
    border: 1px solid #30363d;
    border-radius: 3px;
    background-color: #0d1117;
}

QCheckBox::indicator:checked {
    background-color: #1f6feb;
    border-color: #58a6ff;
}

QScrollBar:vertical {
    border: none;
    background-color: #0d1117;
    width: 8px;
    margin: 0;
}

QScrollBar::handle:vertical {
    background-color: #30363d;
    min-height: 20px;
    border-radius: 4px;
}

QScrollBar::handle:vertical:hover {
    background-color: #484f58;
}

QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {
    border: none;
    background: none;
}

QScrollBar:horizontal {
    border: none;
    background-color: #0d1117;
    height: 8px;
    margin: 0;
}

QScrollBar::handle:horizontal {
    background-color: #30363d;
    min-width: 20px;
    border-radius: 4px;
}

QScrollBar::handle:horizontal:hover {
    background-color: #484f58;
}

QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {
    border: none;
    background: none;
}

QSplitter::handle {
    background-color: #21262d;
}

QSplitter::handle:hover {
    background-color: #58a6ff;
}

QGroupBox {
    border: 1px solid #30363d;
    border-radius: 6px;
    margin-top: 8px;
    padding-top: 4px;
    font-weight: 600;
}

QGroupBox::title {
    subcontrol-origin: margin;
    left: 8px;
    padding: 0 4px;
    color: #8b949e;
    font-size: 11px;
}
"""
