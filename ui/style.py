DARK_STYLESHEET = """
QWidget {
    background-color: #0d1117;
    color: #e6edf3;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans", Helvetica, Arial, sans-serif;
    font-size: 12px;
}

QMainWindow, QDialog {
    background-color: #0d1117;
}

/* Tabs */
QTabWidget::pane {
    border: 1px solid #30363d;
    background-color: #0d1117;
    border-radius: 6px;
    top: -1px;
}

QTabBar::tab {
    background: transparent;
    color: #8b949e;
    padding: 7px 16px;
    margin-right: 4px;
    font-weight: 600;
    font-size: 12px;
    border-top-left-radius: 6px;
    border-top-right-radius: 6px;
    border-bottom: 2px solid transparent;
}

QTabBar::tab:selected {
    color: #e6edf3;
    border-bottom: 2px solid #1f6feb;
    background-color: #161b22;
}

QTabBar::tab:hover:!selected {
    color: #c9d1d9;
    background-color: #13171e;
}

/* Buttons */
QPushButton {
    background-color: #21262d;
    color: #c9d1d9;
    border: 1px solid #30363d;
    border-radius: 5px;
    padding: 4px 12px;
    font-weight: 500;
}

QPushButton:hover {
    background-color: #30363d;
    border-color: #8b949e;
    color: #ffffff;
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
    border: 1px solid #2ea043;
    color: #ffffff;
    font-weight: 600;
}

QPushButton#primaryButton:hover {
    background-color: #2ea043;
}

QPushButton#primaryButton:pressed {
    background-color: #1a7f37;
}

QPushButton#primaryButton:disabled {
    background-color: #161b22;
    border-color: #21262d;
    color: #484f58;
}

/* Text Inputs */
QLineEdit, QPlainTextEdit, QTextEdit {
    background-color: #0b0e14;
    border: 1px solid #30363d;
    border-radius: 5px;
    color: #e6edf3;
    padding: 5px 8px;
    selection-background-color: #1f6feb;
}

QLineEdit:focus, QPlainTextEdit:focus, QTextEdit:focus {
    border: 1px solid #58a6ff;
}

/* Group Boxes */
QGroupBox {
    border: 1px solid #30363d;
    border-radius: 6px;
    margin-top: 10px;
    font-weight: 600;
    color: #8b949e;
    padding-top: 10px;
    font-size: 11px;
}

QGroupBox::title {
    subcontrol-origin: margin;
    subcontrol-position: top left;
    left: 8px;
    padding: 0 4px;
    background-color: #0d1117;
}

/* Combo Box */
QComboBox {
    background-color: #0b0e14;
    border: 1px solid #30363d;
    border-radius: 5px;
    padding: 4px 8px;
    color: #e6edf3;
}

QComboBox:hover {
    border-color: #8b949e;
}

QComboBox::drop-down {
    border: none;
    width: 20px;
}

QComboBox QAbstractItemView {
    background-color: #161b22;
    border: 1px solid #30363d;
    selection-background-color: #1f6feb;
    color: #e6edf3;
}

/* Check Box */
QCheckBox {
    color: #e6edf3;
    spacing: 6px;
    font-size: 12px;
}

/* Splitter */
QSplitter::handle {
    background-color: #21262d;
}

QSplitter::handle:hover {
    background-color: #58a6ff;
}

QSplitter::handle:horizontal {
    width: 2px;
}

QSplitter::handle:vertical {
    height: 2px;
}

/* Scrollbars */
QScrollBar:vertical {
    border: none;
    background: #080a0f;
    width: 10px;
    margin: 0px;
}

QScrollBar::handle:vertical {
    background: #30363d;
    min-height: 24px;
    border-radius: 4px;
    margin: 1px;
}

QScrollBar::handle:vertical:hover {
    background: #58a6ff;
}

QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {
    height: 0px;
}

QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical {
    background: none;
}

QScrollBar:horizontal {
    border: none;
    background: #080a0f;
    height: 10px;
    margin: 0px;
}

QScrollBar::handle:horizontal {
    background: #30363d;
    min-width: 24px;
    border-radius: 4px;
    margin: 1px;
}

QScrollBar::handle:horizontal:hover {
    background: #58a6ff;
}

QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {
    width: 0px;
}

QScrollBar::add-page:horizontal, QScrollBar::sub-page:horizontal {
    background: none;
}

/* Status Bar */
QStatusBar {
    background-color: #0d1117;
    border-top: 1px solid #21262d;
    color: #8b949e;
    font-size: 11px;
    padding: 2px 6px;
}
"""
