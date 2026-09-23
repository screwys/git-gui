#!/usr/bin/env python3
"""Install the local launcher and desktop entry for this checkout."""
import os
from pathlib import Path

root = Path(__file__).resolve().parent
bin_dir = Path.home() / ".local/bin"
bin_dir.mkdir(parents=True, exist_ok=True)
for name in ("rufin-git-gui", "git-gui"):
    target = bin_dir / name
    if target.is_symlink():
        target.unlink()
    target.symlink_to(root / name)

applications = Path(os.environ.get("XDG_DATA_HOME", Path.home() / ".local/share")) / "applications"
applications.mkdir(parents=True, exist_ok=True)
launcher = str(root / "rufin-git-gui").replace("\\", "\\\\").replace('"', '\\"').replace("`", "\\`").replace("$", "\\$").replace("%", "%%")
entry = (root / "rufin-git-gui.desktop").read_text()
entry = entry.replace("Exec=rufin-git-gui\n", f'Exec="{launcher}"\n')
entry = entry.replace("Icon=git-gui\n", f"Icon={root / 'git-gui.svg'}\n")
(applications / "rufin-git-gui.desktop").write_text(entry)
print(f"Installed {applications / 'rufin-git-gui.desktop'}")
