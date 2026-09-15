from typing import List, Dict, Tuple
from PyQt6.QtCore import Qt
from PyQt6.QtWidgets import (
    QDialog,
    QVBoxLayout,
    QHBoxLayout,
    QLabel,
    QPushButton,
    QFrame,
    QScrollArea,
    QWidget,
)


class ConfirmPreviewDialog(QDialog):
    """
    A preview modal shown before executing PR publication or Release.
    Provides a clear, itemized breakdown of what will be performed, target branches/tags,
    selected files, flags, and requires confirmation.
    """

    def __init__(
        self,
        title: str,
        subtitle: str,
        command_preview: str,
        details: List[Tuple[str, str]],
        action_button_text: str = "Confirm & Proceed",
        parent=None,
    ):
        super().__init__(parent)
        self.setWindowTitle(title)
        self.setWindowFlags(Qt.WindowType.Dialog | Qt.WindowType.FramelessWindowHint)
        self.setAttribute(Qt.WidgetAttribute.WA_TranslucentBackground, False)
        self.resize(540, 420)
        self.setStyleSheet(
            "ConfirmPreviewDialog { "
            "  background-color: #161b22; "
            "  border: 1px solid #484f58; "
            "  border-radius: 8px; "
            "} "
            "QLabel { color: #e6edf3; font-size: 12px; } "
            "QFrame#cmdFrame { "
            "  background-color: #0b0e14; "
            "  border: 1px solid #30363d; "
            "  border-radius: 6px; "
            "  padding: 8px; "
            "} "
            "QLabel#cmdLabel { "
            "  color: #79c0ff; "
            "  font-family: monospace; "
            "  font-size: 11px; "
            "} "
            "QScrollArea { border: none; background: transparent; } "
        )
        self._init_ui(title, subtitle, command_preview, details, action_button_text)

    def _init_ui(
        self,
        title: str,
        subtitle: str,
        command_preview: str,
        details: List[Tuple[str, str]],
        action_button_text: str,
    ):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(18, 16, 18, 16)
        layout.setSpacing(12)

        # Header
        h_header = QHBoxLayout()
        title_lbl = QLabel(title)
        title_lbl.setStyleSheet("font-size: 14px; font-weight: 700; color: #58a6ff;")
        h_header.addWidget(title_lbl)
        h_header.addStretch()

        btn_close = QPushButton("✕")
        btn_close.setFixedSize(20, 20)
        btn_close.setStyleSheet(
            "QPushButton { background: transparent; border: none; color: #8b949e; font-size: 12px; } "
            "QPushButton:hover { color: #ffffff; }"
        )
        btn_close.clicked.connect(self.reject)
        h_header.addWidget(btn_close)
        layout.addLayout(h_header)

        if subtitle:
            sub_lbl = QLabel(subtitle)
            sub_lbl.setStyleSheet("color: #8b949e; font-size: 11px;")
            layout.addWidget(sub_lbl)

        # Command Preview Box
        cmd_frame = QFrame()
        cmd_frame.setObjectName("cmdFrame")
        cmd_layout = QVBoxLayout(cmd_frame)
        cmd_layout.setContentsMargins(8, 6, 8, 6)
        cmd_layout.setSpacing(2)

        cmd_title = QLabel("Command Preview:")
        cmd_title.setStyleSheet("color: #8b949e; font-size: 10px; font-weight: 600; text-transform: uppercase;")
        cmd_layout.addWidget(cmd_title)

        cmd_lbl = QLabel(command_preview)
        cmd_lbl.setObjectName("cmdLabel")
        cmd_lbl.setWordWrap(True)
        cmd_layout.addWidget(cmd_lbl)
        layout.addWidget(cmd_frame)

        # Details list inside scroll
        scroll = QScrollArea()
        scroll.setWidgetResizable(True)
        scroll_content = QWidget()
        scroll_layout = QVBoxLayout(scroll_content)
        scroll_layout.setContentsMargins(0, 4, 4, 4)
        scroll_layout.setSpacing(6)

        for label, val in details:
            row = QHBoxLayout()
            row.setSpacing(8)
            lbl = QLabel(f"{label}:")
            lbl.setStyleSheet("color: #8b949e; font-weight: 600; font-size: 11px; min-width: 100px;")
            lbl.setAlignment(Qt.AlignmentFlag.AlignTop | Qt.AlignmentFlag.AlignLeft)
            row.addWidget(lbl)

            v_lbl = QLabel(val)
            v_lbl.setStyleSheet("color: #e6edf3; font-size: 11px;")
            v_lbl.setWordWrap(True)
            row.addWidget(v_lbl, 1)

            scroll_layout.addLayout(row)

        scroll_layout.addStretch()
        scroll.setWidget(scroll_content)
        layout.addWidget(scroll, 1)

        # Bottom actions
        h_actions = QHBoxLayout()
        h_actions.setSpacing(10)

        btn_cancel = QPushButton("Cancel")
        btn_cancel.setFixedHeight(28)
        btn_cancel.setStyleSheet("font-size: 11px; padding: 2px 14px;")
        btn_cancel.clicked.connect(self.reject)
        h_actions.addWidget(btn_cancel)

        h_actions.addStretch()

        btn_confirm = QPushButton(action_button_text)
        btn_confirm.setObjectName("primaryButton")
        btn_confirm.setFixedHeight(28)
        btn_confirm.setStyleSheet("font-size: 11px; font-weight: bold; padding: 2px 16px;")
        btn_confirm.clicked.connect(self.accept)
        h_actions.addWidget(btn_confirm)

        layout.addLayout(h_actions)
