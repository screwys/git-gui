#!/usr/bin/env python3
"""Install the local launcher, desktop entry and PR watcher service for this checkout."""
import os
import subprocess
from pathlib import Path

root = Path(__file__).resolve().parent
bin_dir = Path.home() / ".local/bin"
bin_dir.mkdir(parents=True, exist_ok=True)
target = bin_dir / "git-gui"
if target.is_symlink():
    target.unlink()
target.symlink_to(root / "git-gui")
legacy = bin_dir / "rufin-git-gui"
if legacy.is_symlink() and legacy.resolve() in (
    root / "rufin-git-gui", root.with_name("rufin-git-gui") / "rufin-git-gui",
):
    legacy.unlink()

applications = Path(os.environ.get("XDG_DATA_HOME", Path.home() / ".local/share")) / "applications"
applications.mkdir(parents=True, exist_ok=True)
launcher = str(root / "git-gui").replace("\\", "\\\\").replace('"', '\\"').replace("`", "\\`").replace("$", "\\$").replace("%", "%%")
entry = (root / "git-gui.desktop").read_text()
entry = entry.replace("Exec=git-gui\n", f'Exec="{launcher}"\n')
entry = entry.replace("Icon=git-gui\n", f"Icon={root / 'git-gui.svg'}\n")
(applications / "git-gui.desktop").write_text(entry)
(applications / "rufin-git-gui.desktop").unlink(missing_ok=True)
print(f"Installed {applications / 'git-gui.desktop'}")

# prwatchd runs as an always-on user service; a rebuilt binary takes effect on restart.
subprocess.run(["go", "build", "-o", str(bin_dir / "prwatchd"), "."], cwd=root / "prwatchd", check=True)
units = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config")) / "systemd/user"
units.mkdir(parents=True, exist_ok=True)
unit = units / "prwatchd.service"
unit.unlink(missing_ok=True)
unit.symlink_to(root / "prwatchd/prwatchd.service")
subprocess.run(["systemctl", "--user", "daemon-reload"], check=True)
subprocess.run(["systemctl", "--user", "enable", "prwatchd.service"], check=True)
subprocess.run(["systemctl", "--user", "restart", "prwatchd.service"], check=True)
print("Installed and restarted prwatchd.service")
