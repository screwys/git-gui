"""Durable command records and the detached command supervisor."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


def jobs_root():
    return Path(os.environ.get("XDG_STATE_HOME", Path.home() / ".local/state")) / "rufin-git-gui/jobs"


def write_record(path, record):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(record, indent=2))
    temporary.replace(path)


def process_identity(pid):
    try:
        fields = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
        if fields[0] == "Z":
            return None
        boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
        return f"{boot}:{fields[19]}"
    except (OSError, IndexError):
        return None


def is_alive(record):
    identity = record.get("identity")
    return bool(identity and process_identity(record["pid"]) == identity)


def job_running(record):
    return "exit_code" not in record and (is_alive(record) or is_alive(record.get("child", {})))


def records(cwd, key):
    root = jobs_root()
    if not root.exists():
        return []
    result = []
    for path in root.glob("*/job.json"):
        record = json.loads(path.read_text())
        if record["cwd"] == os.path.realpath(cwd) and record["key"] == key:
            result.append((path, record))
    return sorted(result, key=lambda entry: entry[1]["started_at"], reverse=True)


def external_process(cwd, program):
    """Observe an existing helper without claiming its output or exit status."""
    if not program:
        return None
    program = os.path.realpath(program)
    for path in Path("/proc").iterdir():
        if not path.name.isdigit():
            continue
        try:
            if path.stat().st_uid != os.getuid():
                continue
            args = path.joinpath("cmdline").read_bytes().decode().rstrip("\0").split("\0")
            directory = os.path.realpath(path / "cwd")
            candidates = args[:2] if args and os.path.basename(args[0]) in ("bash", "sh") else args[:1]
            if any(os.path.realpath(os.path.join(directory, arg)) == program for arg in candidates):
                identity = process_identity(int(path.name))
                if identity:
                    return {"pid": int(path.name), "identity": identity, "command": args,
                            "cwd": directory, "external": True}
        except (OSError, UnicodeError):
            continue
    return None


def supervise(path, lock_fd):
    record = json.loads(path.read_text())
    record.update(pid=os.getpid(), identity=process_identity(os.getpid()))
    child = None
    stopping = False

    def stop(signum, frame):
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    write_record(path, record)
    with path.with_name("output.log").open("ab", buffering=0) as output:
        try:
            child = subprocess.Popen(record["command"], cwd=record["cwd"],
                                     stdin=subprocess.DEVNULL, stdout=output, stderr=output,
                                     start_new_session=True)
            record["child"] = {"pid": child.pid, "identity": process_identity(child.pid)}
            write_record(path, record)
            deadline = None
            while True:
                code = child.poll()
                if stopping:
                    if deadline is None:
                        try:
                            os.killpg(child.pid, signal.SIGTERM)
                        except ProcessLookupError:
                            break
                        deadline = time.monotonic() + 5
                    elif time.monotonic() >= deadline:
                        try:
                            os.killpg(child.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        break
                    if code is not None:
                        try:
                            os.killpg(child.pid, 0)
                        except ProcessLookupError:
                            break
                elif code is not None:
                    break
                time.sleep(0.1)
            code = child.wait()
        except Exception as error:
            output.write(f"\n[ERROR] {error}\n".encode())
            code = 127
        record.update(exit_code=code, finished_at=time.time(), stopped=stopping)
        output.write(f"\n[{'STOPPED' if stopping else 'FINISHED'}] Exit {code}\n".encode())
        write_record(path, record)
    os.close(lock_fd)


if __name__ == "__main__":
    supervise(Path(sys.argv[1]), int(sys.argv[2]))
