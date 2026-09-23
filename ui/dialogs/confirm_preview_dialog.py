from typing import List, Tuple, Optional
from PyQt6.QtCore import Qt, QPoint, QEventLoop
from PyQt6.QtWidgets import (
    QWidget,
    QVBoxLayout,
    QHBoxLayout,
    QLabel,
    QPushButton,
    QPlainTextEdit,
)


class ConfirmPreviewDialog(QWidget):
    """
    Compact flyout / popup card anchored below the triggering button,
    dismisses automatically when clicking outside.
    Presents a clean, high-signal summary of what is about to be published.
    """

    def __init__(
        self,
        title: str,
        details: List[Tuple[str, str]],
        preview_text: Optional[str] = None,
        preview_text_label: str = "Body / Description:",
        action_button_text: str = "Confirm",
        anchor_widget: Optional[QWidget] = None,
        parent=None,
    ):
        # Popup window flag makes it behave like a sleek popup that dismisses on outside clicks
        super().__init__(parent, Qt.WindowType.Popup | Qt.WindowType.FramelessWindowHint)
        self.setAttribute(Qt.WidgetAttribute.WA_TranslucentBackground, False)
        self.confirmed = False
        self._loop = None
        self.setFixedWidth(380)

        self.setStyleSheet(
            "ConfirmPreviewDialog { "
            "  background-color: #161b22; "
            "  border: 1px solid #484f58; "
            "  border-radius: 8px; "
            "} "
            "QLabel { color: #e6edf3; font-size: 11px; } "
            "QPlainTextEdit { "
            "  background-color: #0b0e14; "
            "  border: 1px solid #30363d; "
            "  border-radius: 4px; "
            "  color: #c9d1d9; "
            "  font-size: 11px; "
            "  font-family: monospace; "
            "  padding: 4px; "
            "} "
        )
        self._init_ui(title, details, preview_text, preview_text_label, action_button_text)
        if anchor_widget:
            self._position_near(anchor_widget)

    def _init_ui(
        self,
        title: str,
        details: List[Tuple[str, str]],
        preview_text: Optional[str],
        preview_text_label: str,
        action_button_text: str,
    ):
        layout = QVBoxLayout(self)
        layout.setContentsMargins(12, 10, 12, 10)
        layout.setSpacing(8)

        # Header with title & close button
        h_header = QHBoxLayout()
        h_header.setSpacing(4)
        title_lbl = QLabel(title)
        title_lbl.setStyleSheet("font-size: 12px; font-weight: 700; color: #58a6ff;")
        h_header.addWidget(title_lbl)
        h_header.addStretch()

        btn_close = QPushButton("✕")
        btn_close.setFixedSize(16, 16)
        btn_close.setStyleSheet(
            "QPushButton { background: transparent; border: none; color: #8b949e; font-size: 11px; } "
            "QPushButton:hover { color: #ffffff; }"
        )
        btn_close.clicked.connect(self.close)
        h_header.addWidget(btn_close)
        layout.addLayout(h_header)

        # High-signal key-value rows
        for label, val in details:
            row = QHBoxLayout()
            row.setSpacing(6)
            lbl = QLabel(f"{label}:")
            lbl.setStyleSheet("color: #8b949e; font-size: 11px; min-width: 85px;")
            row.addWidget(lbl)

            v_lbl = QLabel(val)
            v_lbl.setStyleSheet("color: #e6edf3; font-weight: 500; font-size: 11px;")
            v_lbl.setWordWrap(True)
            row.addWidget(v_lbl, 1)
            layout.addLayout(row)

        # Preview text box (e.g. actual PR description or release notes) if provided
        if preview_text and preview_text.strip():
            lbl_preview = QLabel(preview_text_label)
            lbl_preview.setStyleSheet("color: #8b949e; font-size: 10px; margin-top: 2px;")
            layout.addWidget(lbl_preview)

            txt = QPlainTextEdit()
            txt.setReadOnly(True)
            txt.setPlainText(preview_text.strip())
            txt.setFixedHeight(75)
            layout.addWidget(txt)

        # Action Buttons
        h_actions = QHBoxLayout()
        h_actions.setSpacing(8)
        h_actions.addStretch()

        btn_cancel = QPushButton("Cancel")
        btn_cancel.setFixedHeight(24)
        btn_cancel.setStyleSheet("font-size: 11px; padding: 2px 10px;")
        btn_cancel.clicked.connect(self.close)
        h_actions.addWidget(btn_cancel)

        btn_confirm = QPushButton(action_button_text)
        btn_confirm.setObjectName("primaryButton")
        btn_confirm.setFixedHeight(24)
        btn_confirm.setStyleSheet("font-size: 11px; font-weight: 600; padding: 2px 14px;")
        btn_confirm.clicked.connect(self._on_confirm)
        h_actions.addWidget(btn_confirm)

        layout.addLayout(h_actions)

    def _on_confirm(self):
        self.confirmed = True
        self.close()

    def exec(self):
        self._loop = QEventLoop(self)
        self.show()
        self._loop.exec()
        self._loop.deleteLater()
        self._loop = None
        self.deleteLater()
        return self.confirmed

    def hideEvent(self, event):
        if self._loop:
            self._loop.quit()
        super().hideEvent(event)

    def _position_near(self, widget: QWidget):
        pt = widget.mapToGlobal(QPoint(0, widget.height() + 4))
        self.move(pt)
