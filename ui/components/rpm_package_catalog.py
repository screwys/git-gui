from PyQt6.QtCore import QObject, QProcess, QStringListModel, pyqtSignal


class RpmPackageCatalog(QObject):
    """Share one on-demand DNF cache lookup across project tabs."""

    changed = pyqtSignal()

    def __init__(self, parent=None):
        super().__init__(parent)
        self.model = QStringListModel(self)
        self.names = set()
        self.loaded = False
        self.attempted = False
        self.error = ""
        self.details = ""
        self.process = QProcess(self)
        self.process.finished.connect(self._finished)
        self.process.errorOccurred.connect(self._error)

    def load(self, *, refresh=False):
        if self.process.state() != QProcess.ProcessState.NotRunning or (self.attempted and not refresh):
            return
        self.attempted = True
        self.error = ""
        self.details = ""
        self.process.start("dnf", [
            "--cacheonly", "--quiet", "repoquery", "--available", "--queryformat=%{name}\\n",
        ])
        self.changed.emit()

    def _error(self, error):
        if error == QProcess.ProcessError.FailedToStart:
            self.error = "Could not run DNF."
            self.details = self.process.errorString()
            self.changed.emit()

    def _finished(self, code, status):
        self.details = bytes(self.process.readAllStandardError()).decode("utf-8", "replace").strip()
        output = bytes(self.process.readAllStandardOutput()).decode("utf-8", "replace")
        if code == 0 and status == QProcess.ExitStatus.NormalExit:
            self.names = {line.strip() for line in output.splitlines() if line.strip()}
            self.model.setStringList(sorted(self.names))
            self.loaded = True
            self.error = "" if self.names else "No cached DNF packages."
        else:
            self.error = "Cached DNF lookup failed."
        self.changed.emit()
