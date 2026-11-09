# Git GUI

A Linux desktop app for committing, publishing pull requests and cutting releases in my
repositories. The window is Python with PyQt6. The work happens in `git-gui-daemon`, a Go user
service that runs the app's commands and watches my open pull requests.

## Features

- Project tabs with file selection, diffs and an editor. Rufin, Igloo and nocblue are built in,
  and **+** adds any Git repository.
- Rufin: publish PRs and releases through Rufin's own scripts, sync `main`, and build and install
  the Android app.
- nocblue: simple package management, with live rpm search
- Other projects: commit and push with a rebase onto the remote, tag and publish GitHub releases
- Commands keep running when the window closes, and the app reattaches to them later. History,
  pause, resume and cancel included.
- Publish or release after a PR merges, even with the window closed. A publish can run the full
  pre-push checks while it waits.
- Optional tray mode with notifications when commands finish or fail.
- Your open PRs are watched: a PR that falls behind its base while auto-merge waits gets updated,
  failed checks and merge conflicts raise notifications, and the Rufin checkout syncs after its PRs
  merge.

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

The installer adds the `git-gui` command and a menu entry, and builds and starts the
`git-gui-daemon` user service. Run it again after updating. Projects default to `~/Projects/Rufin`,
`~/Projects/Igloo` and `~/Projects/nocblue`; change them in **Settings**.

## Files

Settings and window state are in `~/.config/git-gui`. Command logs and the daemon's state are in
`~/.local/state/git-gui`, and `journalctl --user -u git-gui-daemon` shows the daemon's log.
