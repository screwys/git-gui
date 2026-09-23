# Rufin Git GUI

A clean, fast, and extendable PyQt6 GUI designed for managing Git pull requests and releases with the Rufin scripts (`publish-pr.sh` and `release-rufin.sh`).

## Features

- **Changes & File Picker**:
  - Live status for modified, added, untracked, and deleted files.
  - Per-file additions/deletions stats badge (`+X -Y`).
  - Checkboxes to select individual files or "Select All".
  - Click any file to view its unified diff.
  - "View Diff" button to preview the combined diff of all selected files.
- **Publish PR Workflow**:
  - Configure topic branch and base branch (defaults to `main`).
  - Commit subject and multi-line PR description. The first subject names the PR; follow-up commits keep its title.
  - Restores the publish script's saved PR description after a failed push or restart. Clean retries publish existing commits; descriptions are sent only when edited.
  - Check-watcher phase label.
  - Toggles for squash auto-merge (`--no-auto-merge`), wait for CI (`--wait`), and i18n catalog options (`--include-i18n-catalogs`, `--sync-i18n-catalogs`).
- **Release Orchestrator Workflow**:
  - Automatically detects latest git tag and suggests next patch/minor version.
  - Expanding changelog / release summary markdown editor.
  - Flags for `--push`, `--dry-run`, `--replace`, `--skip-flathub`, `--skip-github-release`, and `--resume-after-prep`.
- **Live Terminal Monitor**:
  - Streams script execution in real-time with full ANSI color decoding and carriage-return handling.
  - Execution status pill (`Running`, `Success`, `Exit code`).
  - "Stop / Terminate" button to safely abort running scripts.
  - "Clear", "Copy", and "Auto-scroll" controls.
- **Settings & Configuration**:
  - Dedicated **⚙ Settings** dialog accessible directly from the top bar.
  - Configurable script paths (supports paths relative to repository or absolute paths).
  - Configurable workflow defaults (default base branch, auto-merge, CI wait).
  - Persisted to `~/.config/git-gui/config.json`.
  - Optional close to tray, with background completion and failure notifications.
- **Window Controls & Dragging**:
  - Draggable top bar with native Wayland/X11 system dragging (`startSystemMove`) and mouse tracking fallback.
  - Automatically obeys system GTK / Niri window decoration rules (shows only the circular close button on Niri / GNOME).
  - Double-click to toggle maximize / restore.
  - Quick Git controls: **⟳ Refresh**, **📥 Fetch**, and **⬇ Pull**.

## Running

Launch directly from anywhere:
```bash
rufin-git-gui
# Or targeting a specific repository directory:
rufin-git-gui /path/to/repo
```

You can also launch it from your application launcher / Noctalia via the installed `.desktop` entry.

To install or update the launcher for this checkout, run:

```bash
python3 install-desktop.py
```

The installer creates command links in `~/.local/bin` and installs the repository's
`rufin-git-gui.desktop` entry in your application menu, with paths to this checkout and its icon.
Run it again if you move the checkout. Python 3 and PyQt6 must be installed on the system.

## Background commands and recovery

Enable **Settings > Close to tray** to keep notifications active when you close the window.
Use **Open Git GUI** in the tray menu, or launch the app again, to show the same window.
**Quit** exits the GUI. If the desktop has no system tray, closing the window also exits the GUI.

Commands started by the app run in a separate Linux process session. Closing the window, quitting,
or restarting the GUI does not cancel them. Reopening attaches to running commands and restores
their saved output and exit status. **Stop** terminates the command's process group, including child
processes. Notifications require the GUI to remain open or in the tray.

**History** shows earlier commands and their recorded results. Logs and command records live in
`$XDG_STATE_HOME/rufin-git-gui/jobs`, or `~/.local/state/rufin-git-gui/jobs` by default. The terminal
keeps the latest 10,000 displayed lines; full logs remain on disk.

The app also detects Rufin publish and release helpers started elsewhere. Those helpers are shown
as observed processes. Their unsaved output and exit codes cannot be recovered, and their Stop
button is disabled. A disappeared process without a recorded result is reported as unknown.

On startup and refresh, PR discovery queries the repository instead of keeping an old selection.
Release monitoring reads GitHub release tags, drafts, release preparation PRs, and matching workflow
runs. Select a release in **Jobs and Progress** to inspect it. **Resume selected release** fills the
release form for that version; the usual confirmation is still required before execution.
Remote request failures remain visible, and unmatched workflow results are marked unknown.

Output tabs follow the current stage. Local preparation and pre-push checks show the terminal.
Available PR checks and dispatched release workflows show remote progress. Local failures return
to the log. Automatic selection happens when the stage changes, so repeated updates preserve a
tab you selected manually.

In tray mode, remote status checks run once a minute. Background monitoring does not scan the
working tree or generate diffs. Opening or refreshing a repository updates its changes and diffs.

## Architecture & Extensibility

```
rufin-git-gui/
├── main.py                     # Main window, draggable top bar, tab host
├── core/
│   ├── config.py               # Settings manager (reads/writes ~/.config/rufin-git-gui/config.json)
│   ├── git_manager.py          # Git status, additions/deletions stats, diffs, branches, fetch, pull
│   ├── runner.py               # Attach to commands and stream saved output
│   ├── jobs.py                 # Detached command supervisor and durable records
│   └── instance.py             # Single window activation through a local socket
├── ui/
│   ├── style.py                # Dark theme stylesheet
│   ├── dialogs/
│   │   └── settings_dialog.py  # Script locations and workflow defaults modal dialog
│   ├── components/
│   │   ├── file_list.py        # File list with checkboxes and diff stats
│   │   ├── diff_viewer.py      # Syntax-highlighted diff viewer
│   │   └── terminal_view.py    # ANSI-colored terminal console
│   └── tabs/
│       ├── publish_pr_tab.py   # PR publication workflow
│       └── release_tab.py      # Release workflow
└── rufin-git-gui               # Shell wrapper script
```

### Adding New Workflows / Tabs

To add a new workflow, create a widget in `ui/tabs/` and add it to `MainWindow`:
```python
new_tab = MyCustomScriptTab(self.git_mgr, self.config)
self.add_workflow_tab("🔧 My Tool", new_tab)
```
