# Git GUI

A Linux desktop app for committing, publishing pull requests and cutting releases in my
repositories, plus `prwatchd`, a service that watches my open pull requests. The app is Python
with PyQt6; the watcher is Go.

## Features

- Project tabs with file selection, diffs and an editor. Rufin, Igloo and nocblue are built in,
  and **+** adds any Git repository.
- Rufin: publish PRs and releases through Rufin's own scripts, sync `main`, and build and install
  the Android app.
- nocblue: simple package management, with live rpm search
- Other projects: commit and push with a rebase onto the remote, tag and publish GitHub releases
- Commands keep running when the window closes, and the app reattaches to them later. History,
  pause, resume and cancel included.
- Optional tray mode with notifications when commands finish or fail.
- `prwatchd` updates PRs that fall behind their base while auto-merge waits, notifies you of
  failed checks and merge conflicts, and syncs the Rufin checkout after its PRs merge.

## Setup

Requires Linux with a systemd user session, Python 3 with PyQt6 and PyYAML, Git, the GitHub CLI
and Go.

```bash
git clone https://github.com/screwys/git-gui.git
cd git-gui
python3 -m pip install --user -r requirements.txt
gh auth login
python3 install-desktop.py
```

The installer adds the `git-gui` command and a menu entry, and builds and starts the `prwatchd`
user service. Run it again after updating. Projects default to `~/Projects/Rufin`,
`~/Projects/Igloo` and `~/Projects/nocblue`; change them in **Settings**.

## Files

Settings and window state are in `~/.config/git-gui`. Command logs and `prwatchd`'s state are in
`~/.local/state/git-gui`, and `journalctl --user -u prwatchd` shows the watcher's log.
