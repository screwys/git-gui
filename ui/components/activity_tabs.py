from PyQt6.QtWidgets import QTabWidget


class ActivityTabs(QTabWidget):
    """Select useful output once per stage, leaving manual choices alone between stages."""

    def __init__(self, parent=None):
        super().__init__(parent)
        self.activity = None

    def show_activity(self, activity, widget, force=False):
        if force or activity != self.activity:
            self.activity = activity
            self.setCurrentWidget(widget)
