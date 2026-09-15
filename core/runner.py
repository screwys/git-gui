import os
import sys
from typing import List, Dict, Optional
from PyQt6.QtCore import QObject, QProcess, pyqtSignal


class ProcessRunner(QObject):
    output_received = pyqtSignal(str)
    started = pyqtSignal()
    finished = pyqtSignal(int)  # exit_code
    error_occurred = pyqtSignal(str)

    def __init__(self, parent=None):
        super().__init__(parent)
        self.process: Optional[QProcess] = None
        self._is_running = False

    @property
    def is_running(self) -> bool:
        return self._is_running

    def start(
        self,
        program: str,
        arguments: List[str],
        cwd: str,
        env_vars: Optional[Dict[str, str]] = None,
    ):
        if self._is_running:
            self.error_occurred.emit("A process is already running.")
            return

        self.process = QProcess(self)
        self.process.setProgram(program)
        self.process.setArguments(arguments)
        self.process.setWorkingDirectory(cwd)

        # Configure environment
        env = self.process.processEnvironment().systemEnvironment()
        env.insert("TERM", "xterm-256color")
        env.insert("FORCE_COLOR", "1")
        env.insert("PYTHONUNBUFFERED", "1")
        if env_vars:
            for k, v in env_vars.items():
                env.insert(k, v)
        self.process.setProcessEnvironment(env)

        # Merge standard output and error channels so terminal output stays in exact chronological order
        self.process.setProcessChannelMode(QProcess.ProcessChannelMode.MergedChannels)

        self.process.readyReadStandardOutput.connect(self._on_ready_read)
        self.process.started.connect(self._on_started)
        self.process.finished.connect(self._on_finished)
        self.process.errorOccurred.connect(self._on_error)

        cmd_str = f"$ {program} " + " ".join(f'"{a}"' if " " in a else a for a in arguments)
        self.output_received.emit(f"\x1b[1;34m[RUNNING]\x1b[0m {cmd_str}\n\n")

        self.process.start()

    def _on_ready_read(self):
        if not self.process:
            return
        data = self.process.readAllStandardOutput().data().decode("utf-8", errors="replace")
        self.output_received.emit(data)

    def _on_started(self):
        self._is_running = True
        self.started.emit()

    def _on_finished(self, exit_code: int, exit_status: QProcess.ExitStatus):
        self._is_running = False
        if exit_code == 0:
            self.output_received.emit(f"\n\x1b[1;32m[FINISHED SUCCESS]\x1b[0m Process exited with code 0.\n")
        else:
            self.output_received.emit(f"\n\x1b[1;31m[PROCESS FAILED]\x1b[0m Process exited with code {exit_code}.\n")
        self.finished.emit(exit_code)

    def _on_error(self, error: QProcess.ProcessError):
        self._is_running = False
        msg = f"Process error: {error}"
        self.output_received.emit(f"\n\x1b[1;31m[ERROR]\x1b[0m {msg}\n")
        self.error_occurred.emit(msg)

    def stop(self):
        if self.process and self._is_running:
            self.output_received.emit("\n\x1b[1;33m[TERMINATING]\x1b[0m Sending termination signal...\n")
            self.process.terminate()
            if not self.process.waitForFinished(2000):
                self.process.kill()
            self._is_running = False
