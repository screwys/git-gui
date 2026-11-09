"""Talk to git-gui-daemon, which runs commands and watches pull requests for the GUI."""
import json
import os
from pathlib import Path
import socket


class DaemonError(Exception):
    pass


def socket_path():
    runtime = os.environ.get("XDG_RUNTIME_DIR") or f"/tmp/git-gui-{os.getuid()}"
    return os.path.join(runtime, "git-gui-daemon.sock")


def state_path():
    return Path(os.environ.get("XDG_STATE_HOME", Path.home() / ".local/state")) / "git-gui/daemon.json"


def request(op, **fields):
    """Send one request and return the daemon's answer, raising DaemonError on failure."""
    try:
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
            conn.settimeout(10)
            conn.connect(socket_path())
            conn.sendall(json.dumps({"op": op, **fields}).encode())
            conn.shutdown(socket.SHUT_WR)
            data = b"".join(iter(lambda: conn.recv(65536), b""))
    except OSError as error:
        raise DaemonError(f"git-gui-daemon is not reachable ({error}). Run install-desktop.py.") from error
    answer = json.loads(data or b"{}")
    if answer.get("error"):
        raise DaemonError(answer["error"])
    return answer


def job_spec(cwd, key, command, env_vars=None):
    """A command with the environment this app was started in, so it finds the user's tools."""
    return {"cwd": os.path.realpath(cwd), "key": key, "command": command,
            "env": dict(os.environ, **(env_vars or {}))}


def schedules(cwd, key):
    """The daemon's schedules for one command slot, newest first."""
    try:
        state = json.loads(state_path().read_text())
    except (OSError, ValueError):
        return []
    cwd = os.path.realpath(cwd)
    found = [s for s in state.get("schedules") or [] if s["job"]["cwd"] == cwd and s["job"]["key"] == key]
    return sorted(found, key=lambda s: s["updated"], reverse=True)
