import codecs
import json
import os
from pathlib import Path
import shlex
import time

from PyQt6.QtCore import QObject, QTimer, pyqtSignal
from core import daemon
from core.jobs import external_process, is_stopped, job_running, records
from core.notifications import send_desktop_notification


class ProcessRunner(QObject):
    output_received = pyqtSignal(str)
    started = pyqtSignal()
    finished = pyqtSignal(int)
    error_occurred = pyqtSignal(str)
    restored = pyqtSignal(dict)

    def __init__(self, parent=None):
        super().__init__(parent)
        self.key = "command"
        self.cwd = ""
        self.program = None
        self.record = {}
        self.path = None
        self._is_running = False
        self._offset = 0
        self.replaying_output = False
        self._replay_end = 0
        self._decoder = codecs.getincrementaldecoder("utf-8")("replace")
        self._stop_requested = False
        self._paused = False
        self.timer = QTimer(self)
        self.timer.setInterval(250)
        self.timer.timeout.connect(self._poll)

    @property
    def is_running(self):
        return self._is_running

    @property
    def is_paused(self):
        return self._paused

    def attach(self, cwd, key, program=None):
        if self._is_running:
            return
        old_path, old_record = self.path, self.record
        self.cwd, self.key, self.program = os.path.realpath(cwd), key, program
        found = records(self.cwd, key)
        active = next((entry for entry in found if job_running(entry[1])), None)
        external = None if active else external_process(cwd, program)
        if active:
            self.path, self.record = active
        elif external:
            self.path, self.record = None, external
        elif found:
            self.path, self.record = found[0]
        else:
            return
        if self.path == old_path and self.record == old_record:
            return
        if old_record:
            self.output_received.emit("\n[ATTACHED TO ANOTHER COMMAND]\n")
        self._is_running = job_running(self.record)
        self._paused = is_stopped(self.record.get("child", {}))
        self.reload_output()

    def reload_output(self):
        """Replay saved output without restarting the command."""
        if not self.record:
            return
        self._offset = 0
        self._decoder.reset()
        self._replay_end = (self.path.with_name("output.log").stat().st_size
                            if self.path and self.path.with_name("output.log").exists() else 0)
        self.replaying_output = True
        try:
            self.output_received.emit(f"$ {shlex.join(self.record['command'])}\n")
        finally:
            self.replaying_output = False
        if self.record.get("external"):
            self.output_received.emit("[OBSERVING] Helper started outside this session. Its output and exit code are unavailable.\n")
        self._read_output()
        self.restored.emit(dict(self.record, running=self._is_running, paused=self._paused))
        if self._is_running or self.path:
            self.timer.start()

    def start(self, program, arguments, cwd, env_vars=None):
        cwd = os.path.realpath(cwd or os.getcwd())
        if self._is_running or external_process(cwd, self.program):
            self.error_occurred.emit("A command is already running. Refresh to view it.")
            return
        try:
            answer = daemon.request("start", job=daemon.job_spec(cwd, self.key, [program, *arguments], env_vars))
        except daemon.DaemonError as error:
            self.error_occurred.emit(str(error))
            return
        self.path = Path(answer["job_path"])
        self.record = json.loads(self.path.read_text())
        self._offset = 0
        self._replay_end = 0
        self._decoder.reset()
        self._is_running = True
        self._paused = False
        self._stop_requested = False
        self.output_received.emit(f"$ {shlex.join(self.record['command'])}\n")
        self.started.emit()
        self.timer.start()

    def _read_output(self):
        if not self.path:
            return
        try:
            with self.path.with_name("output.log").open("rb") as output:
                output.seek(self._offset)
                remaining = self._replay_end - self._offset
                replaying = remaining > 0
                data = output.read(min(256 * 1024, remaining) if replaying else 256 * 1024)
                self._offset += len(data)
            if data:
                self.replaying_output = replaying
                try:
                    self.output_received.emit(self._decoder.decode(data))
                finally:
                    self.replaying_output = False
        except FileNotFoundError:
            pass

    def _poll(self):
        had_child = bool(self.record.get("child"))
        if self.path:
            self.record = json.loads(self.path.read_text())
        self._read_output()
        paused = is_stopped(self.record.get("child", {}))
        has_child = bool(self.record.get("child"))
        if paused != self._paused or has_child != had_child:
            self._paused = paused
            self.restored.emit(dict(self.record, running=self._is_running, paused=paused))
        if self._starting():
            return
        running = job_running(self.record)
        if running:
            if self._stop_requested:
                self.stop()
            return
        if self.path and self.path.with_name("output.log").exists():
            if self._offset < self.path.with_name("output.log").stat().st_size:
                return
        was_running = self._is_running
        self._is_running = False
        self.timer.stop()
        code = self.record.get("exit_code", -1)
        if was_running:
            if code == -1:
                self.output_received.emit("\n[ENDED] No exit status was recorded. Check Git and remote status before retrying.\n")
            finished_record = self.record
            self.finished.emit(code)
            if not finished_record.get("external"):
                status = "Stopped" if finished_record.get("stopped") else "Completed" if code == 0 else "Failed"
                send_desktop_notification(f"{status}: {self.key}",
                    f"{Path(finished_record['cwd']).name}: {shlex.join(finished_record['command'][:2])}",
                    urgency="normal" if code == 0 else "critical")

    def _starting(self):
        """The daemon wrote the record, and its supervisor has not recorded itself yet."""
        return (self.path is not None and "pid" not in self.record and "exit_code" not in self.record
                and time.time() - self.record.get("started_at", 0) < 30)

    def stop(self):
        if self._is_running and self.path and not self.record.get("external"):
            self.record = json.loads(self.path.read_text())
            if self._starting():
                self._stop_requested = True
                return
            self._stop_requested = False
            try:
                daemon.request("signal", job_path=str(self.path), action="stop")
            except daemon.DaemonError as error:
                self.error_occurred.emit(str(error))
                return
            self.output_received.emit("\n[STOPPING] Terminating the command and its child processes...\n")

    def toggle_pause(self):
        if not self._is_running or not self.path or self.record.get("external"):
            return
        try:
            daemon.request("signal", job_path=str(self.path), action="resume" if self._paused else "pause")
        except daemon.DaemonError as error:
            self.error_occurred.emit(str(error))
            return
        self._paused = not self._paused
        self.restored.emit(dict(self.record, running=True, paused=self._paused))
