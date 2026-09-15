from PyQt6.QtGui import QIcon, QPixmap, QPainter, QColor, QPen, QBrush
from PyQt6.QtCore import Qt, QPointF


def create_restart_icon() -> QIcon:
    pix = QPixmap(24, 24)
    pix.fill(Qt.GlobalColor.transparent)
    p = QPainter(pix)
    p.setRenderHint(QPainter.RenderHint.Antialiasing)

    # Circular arrow
    pen = QPen(QColor("#58a6ff"), 2.2)
    pen.setCapStyle(Qt.PenCapStyle.RoundCap)
    p.setPen(pen)
    p.drawArc(3, 3, 18, 18, 45 * 16, 270 * 16)

    # Arrow head
    brush = QBrush(QColor("#58a6ff"))
    p.setBrush(brush)
    p.setPen(Qt.PenStyle.NoPen)
    points = [QPointF(15, 2), QPointF(21, 5), QPointF(17, 9)]
    p.drawPolygon(points)
    p.end()
    return QIcon(pix)


def create_android_icon() -> QIcon:
    pix = QPixmap(24, 24)
    pix.fill(Qt.GlobalColor.transparent)
    p = QPainter(pix)
    p.setRenderHint(QPainter.RenderHint.Antialiasing)

    # Android head dome
    p.setBrush(QBrush(QColor("#3fb950")))
    p.setPen(Qt.PenStyle.NoPen)
    p.drawChord(4, 7, 16, 14, 0, 180 * 16)

    # Antennas
    pen = QPen(QColor("#3fb950"), 1.8)
    pen.setCapStyle(Qt.PenCapStyle.RoundCap)
    p.setPen(pen)
    p.drawLine(7, 8, 4, 4)
    p.drawLine(17, 8, 20, 4)

    # Eyes
    p.setBrush(QBrush(QColor("#0d1117")))
    p.setPen(Qt.PenStyle.NoPen)
    p.drawEllipse(8, 10, 2, 2)
    p.drawEllipse(14, 10, 2, 2)
    p.end()
    return QIcon(pix)
