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
  - PR title input and multi-line markdown description / body (automatically exports `RUFIN_ALLOW_PR_BODY=1`).
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
  - Persisted to `~/.config/rufin-git-gui/config.json`.
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

## Architecture & Extensibility

```
rufin-git-gui/
├── main.py                     # Main window, draggable top bar, tab host
├── core/
│   ├── config.py               # Settings manager (reads/writes ~/.config/rufin-git-gui/config.json)
│   ├── git_manager.py          # Git status, additions/deletions stats, diffs, branches, fetch, pull
│   └── runner.py               # Asynchronous QProcess runner with live streaming
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
