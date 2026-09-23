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
(applications / "rufin-git-gui.desktop").write_text(f'''[Desktop Entry]
Type=Application
Name=Rufin Git GUI
Comment=Manage pull requests, releases, and background commands
Exec="{launcher}"
Icon={root / 'git-gui.svg'}
Terminal=false
Categories=Development;RevisionControl;
StartupWMClass=rufin-git-gui
''')
print(f"Installed {applications / 'rufin-git-gui.desktop'}")
