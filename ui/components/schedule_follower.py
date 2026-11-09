from PyQt6.QtCore import QObject, QTimer, pyqtSignal

from core import daemon

FINISHED = ("started", "failed", "closed", "cancelled")


class ScheduleFollower(QObject):
    """Ask git-gui-daemon to run a command after a PR merges, and report the schedule's progress."""
    changed = pyqtSignal(dict)

    def __init__(self, cwd, key, parent=None):
        super().__init__(parent)
        self.cwd, self.key = cwd, key
        self.schedule = {}
        self._id = ""
        self.timer = QTimer(self)
        self.timer.setInterval(2000)
        self.timer.timeout.connect(self._poll)

    @property
    def active(self):
        return bool(self.schedule) and self.schedule["status"] not in FINISHED

    def submit(self, number, job, publish=None, cancel_runs=None):
        """Raises daemon.DaemonError when the daemon refuses or is not running."""
        answer = daemon.request("schedule", schedule={
            "number": number, "job": job, "publish": publish, "cancel_runs": cancel_runs or [],
        })
        self._follow(answer["id"])

    def restore(self):
        """Follow this slot's unfinished schedule after the app restarts."""
        found = daemon.schedules(self.cwd, self.key)
        if found and found[0]["status"] not in FINISHED:
            self._follow(found[0]["id"])

    def cancel(self):
        if self._id:
            daemon.request("cancel", id=self._id)
            self._poll()

    def _follow(self, schedule_id):
        self._id, self.schedule = schedule_id, {}
        self._poll()
        self.timer.start()

    def _poll(self):
        found = next((s for s in daemon.schedules(self.cwd, self.key) if s["id"] == self._id), None)
        if found is None:
            return
        if found != self.schedule:
            self.schedule = found
            self.changed.emit(found)
        if found["status"] in FINISHED:
            self.timer.stop()
