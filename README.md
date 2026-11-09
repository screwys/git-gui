# Git GUI

A Linux desktop app for committing, publishing pull requests and cutting releases in my
repositories. It is one Go binary: a GTK 4 and libadwaita window, plus `git-gui daemon`, a user
service that runs the app's commands and watches my open pull requests and releases.

## Features

- Project tabs with file selection, diffs and an editor. Rufin, Igloo and nocblue are built in,
  and **+** adds any Git repository.
- Rufin: publish PRs and releases through Rufin's own scripts, sync `main`, and build and install
  the Android app.
- Other projects: commit and push with a rebase onto the remote, tag and publish GitHub releases,
  and edit RPM, Brew and Nix package lists.
- Commands keep running when the window closes, and the window reattaches to them later. History,
  pause, resume and cancel included.
- Publish or release after a PR merges, even with the window closed. A publish can run the full
  pre-push checks while it waits.
- Your open PRs are watched: a PR that falls behind its base while auto-merge waits gets updated,
  and you get notifications for failed checks, conflicts, passing checks, merges, release workflow
  results and finished commands.

## Setup

Requires Linux with a systemd user session, Go, the GTK 4 and libadwaita development files
(`gtk4-devel` and `libadwaita-devel` on Fedora), Git and the GitHub CLI.

```bash
git clone https://github.com/screwys/git-gui.git
cd git-gui
go build -o git-gui .
gh auth login
./git-gui install
```

`git-gui install` copies the binary to `~/.local/bin/git-gui`, adds the app to your menu, and
enables and restarts the `git-gui-daemon` user service. Run the same two commands again after
updating. The first build takes several minutes because it compiles the GTK bindings.

Projects default to `~/Projects/Rufin`, `~/Projects/Igloo` and `~/Projects/nocblue`; change them
in **Preferences**.

## Files

Settings and window state are in `~/.config/git-gui`. Command logs and the daemon's state are in
`~/.local/state/git-gui`, and `journalctl --user -u git-gui-daemon` shows the daemon's log.
