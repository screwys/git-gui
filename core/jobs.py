"""Read the command records that git-gui-daemon writes."""
import json
import os
from pathlib import Path


def jobs_root():
    state = Path(os.environ.get("XDG_STATE_HOME", Path.home() / ".local/state"))
    root = state / "git-gui"
    legacy = state / "rufin-git-gui"
    if legacy.is_dir() and not root.exists():
        legacy.rename(root)
        # Commands started before the rename still use their original paths.
        legacy.symlink_to(root, target_is_directory=True)
    return root / "jobs"


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


def is_stopped(record):
    if not is_alive(record):
        return False
    try:
        fields = Path(f"/proc/{record['pid']}/stat").read_text().rsplit(")", 1)[1].split()
        return fields[0] in ("T", "t")
    except (OSError, IndexError):
        return False


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
