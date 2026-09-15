from PyQt6.QtCore import Qt, QTimer, pyqtSignal, QPropertyAnimation, QEasingCurve, QPoint
from PyQt6.QtGui import QColor
from PyQt6.QtWidgets import (
    QWidget,
    QFrame,
    QHBoxLayout,
    QLabel,
    QPushButton,
    QGraphicsOpacityEffect,
)


class ToastNotification(QFrame):
    action_clicked = pyqtSignal()
    dismissed = pyqtSignal()

    def __init__(self, parent: QWidget = None):
        super().__init__(parent)
        self.setVisible(False)
        self.timer = QTimer(self)
        self.timer.setSingleShot(True)
        self.timer.timeout.connect(self.hide_toast)

        self.setStyleSheet(
            "ToastNotification { "
            "  background-color: #161b22; "
            "  border: 1px solid #388bfd; "
            "  border-radius: 6px; "
            "  padding: 4px 8px; "
            "} "
            "QLabel { "
            "  color: #e6edf3; "
            "  font-size: 11px; "
            "  font-weight: 500; "
            "} "
            "QPushButton#toastAction { "
            "  background-color: #21262d; "
            "  border: 1px solid #30363d; "
            "  color: #58a6ff; "
            "  border-radius: 4px; "
            "  padding: 2px 10px; "
            "  font-size: 11px; "
            "  font-weight: 600; "
            "} "
            "QPushButton#toastAction:hover { "
            "  background-color: #30363d; "
            "  border-color: #58a6ff; "
            "  color: #79c0ff; "
            "} "
            "QPushButton#toastClose { "
            "  background: transparent; "
            "  border: none; "
            "  color: #8b949e; "
            "  font-size: 11px; "
            "  padding: 0 4px; "
            "} "
            "QPushButton#toastClose:hover { "
            "  color: #ffffff; "
            "}"
        )

        layout = QHBoxLayout(self)
        layout.setContentsMargins(10, 6, 8, 6)
        layout.setSpacing(10)

        self.label = QLabel()
        layout.addWidget(self.label)

        self.btn_action = QPushButton("Revert")
        self.btn_action.setObjectName("toastAction")
        self.btn_action.setFixedHeight(24)
        self.btn_action.clicked.connect(self._on_action)
        layout.addWidget(self.btn_action)

        self.btn_close = QPushButton("✕")
        self.btn_close.setObjectName("toastClose")
        self.btn_close.setFixedHeight(24)
        self.btn_close.clicked.connect(self.hide_toast)
        layout.addWidget(self.btn_close)

    def show_toast(self, message: str, action_text: str = "Revert", duration_ms: int = 8000):
        self.label.setText(message)
        if action_text:
            self.btn_action.setText(action_text)
            self.btn_action.setVisible(True)
        else:
            self.btn_action.setVisible(False)

        self.adjustSize()
        self.reposition()
        self.setVisible(True)
        self.raise_()

        if duration_ms > 0:
            self.timer.start(duration_ms)

    def reposition(self):
        if not self.parentWidget():
            return
        parent_rect = self.parentWidget().rect()
        w = self.width()
        h = self.height()
        # Position at bottom-right of parent with 16px margin
        x = parent_rect.width() - w - 24
        y = parent_rect.height() - h - 16
        self.move(max(10, x), max(10, y))

    def _on_action(self):
        self.timer.stop()
        self.action_clicked.emit()
        self.hide_toast()

    def hide_toast(self):
        self.timer.stop()
        self.setVisible(False)
        self.dismissed.emit()
