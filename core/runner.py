import codecs
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import time
import uuid

from PyQt6.QtCore import QObject, QTimer, pyqtSignal
from core.jobs import external_process, is_alive, is_stopped, job_running, jobs_root, records, write_record
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
        self._child = None
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
        root = jobs_root()
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        slot = hashlib.sha256(f"{cwd}\0{self.key}".encode()).hexdigest()
        lock = (root / f"{slot}.lock").open("a")
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            lock.close()
            self.error_occurred.emit("This command is already running in another window.")
            return
        directory = root / uuid.uuid4().hex
        directory.mkdir(mode=0o700)
        self.path = directory / "job.json"
        self.record = {"cwd": cwd, "key": self.key, "command": [program, *arguments],
                       "started_at": time.time()}
        write_record(self.path, self.record)
        env = dict(os.environ, TERM="xterm-256color", FORCE_COLOR="1", PYTHONUNBUFFERED="1")
        env.update(env_vars or {})
        try:
            self._child = subprocess.Popen(
                [sys.executable, str(Path(__file__).with_name("jobs.py")), str(self.path), str(lock.fileno())],
                stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                env=env, start_new_session=True, pass_fds=(lock.fileno(),))
        except OSError as error:
            self.record.update(exit_code=127, finished_at=time.time())
            write_record(self.path, self.record)
            self.error_occurred.emit(str(error))
            self.finished.emit(127)
            return
        finally:
            lock.close()
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
        if self._child and self._child.poll() is None and "pid" not in self.record:
            return
        running = job_running(self.record)
        if running:
            if self._stop_requested:
                self.stop()
            return
        if self.path and self.path.with_name("output.log").exists():
            if self._offset < self.path.with_name("output.log").stat().st_size:
                return
        if self._child and self._child.poll() is None:
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

    def stop(self):
        if self._is_running and not self.record.get("external"):
            self._stop_requested = True
            if self.path:
                self.record = json.loads(self.path.read_text())
            if is_alive(self.record):
                try:
                    os.kill(self.record["pid"], signal.SIGTERM)
                except ProcessLookupError:
                    return
                self._stop_requested = False
                self.output_received.emit("\n[STOPPING] Terminating the command and its child processes...\n")
            elif is_alive(self.record.get("child", {})):
                try:
                    os.killpg(self.record["child"]["pid"], signal.SIGTERM)
                except ProcessLookupError:
                    return
                self._stop_requested = False

    def toggle_pause(self):
        if not self._is_running or self.record.get("external"):
            return
        if self.path:
            self.record = json.loads(self.path.read_text())
        child = self.record.get("child", {})
        if not is_alive(child):
            return
        signum = signal.SIGCONT if self._paused else signal.SIGSTOP
        try:
            os.killpg(child["pid"], signum)
        except ProcessLookupError:
            return
        self._paused = signum == signal.SIGSTOP
        self.restored.emit(dict(self.record, running=True, paused=self._paused))
