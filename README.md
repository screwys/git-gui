# Git GUI

A GTK4 command center GUI + daemon for git operations, extremely personal as is but you can likely bend it to your tastes. Main use case is being able to do git operations including releases from GUI, including basic file editing and actually watching CI; while also handling a lot of manual work for merges automatically. The daemon notifies about failed CIs.  Overall this is mainly for Rufin, since `master` is protected it updates PR branches automatically, watches CI, starts releases (the automation itself is inside Rufin repo) and it also helps for my other projects when I need to watch CI, put work on the main branch directly so it gives agents a systematic way to push and watch CIs without creating PRs.

## Features

- Project tabs with file selection, diffs and an editor. Rufin, Igloo and nocblue are built in,
  and **+** adds any Git repository.
- Rufin: publish PRs from one [jj](https://jj-vcs.github.io/jj/) checkout that several sessions
  share, without switching it; give a session a workspace of its own when its work overlaps
  another's; run the app from either; cut releases and follow every package to its store; sync
  `master`; build and install the Android app.
- Other projects: commit and push with a rebase onto the remote, tag and publish GitHub releases,
  and edit RPM, Brew and Nix package lists.
- Commands keep running when the window closes, and the window reattaches to them later. History,
  pause, resume and cancel included.
- A publish whose edits build on another open PR's edits in the same files queues behind it: its
  checks run at once, and it is published once that PR merges, even with the window closed. Fixes
  to the earlier PR leave the queued edits out. A release can also wait for a PR to merge.
- Work set aside for later is parked (`git-gui publish --park`): it goes onto its branch and
  leaves the checkout, so builds and other publishes no longer see it, until Unpark or
  `git-gui unpark` brings it back.
- Session workspaces (`git-gui workspace`, also Claude Code's WorktreeCreate and WorktreeRemove
  hooks) get copy-on-write copies of the build directory and the devel profile, so builds and
  Store versions in one never touch another's.
- A contributor's PR lands the same way: `git-gui publish --pr NUMBER` commits your rework onto
  their branch, fork included, so their PR is the one that merges.
- Your open PRs, and the PRs of others you published onto, are watched: a PR that falls behind its base while auto-merge waits gets updated,
  and you get notifications for failed checks, conflicts, passing checks, merges, release workflow
  results and finished commands.
- `git-gui wait-push` follows the checks of a commit pushed straight to a branch: it notifies you
  and exits once they fail or pass.

## Setup

Requires Linux with a systemd user session, Go, jj (for Rufin), the GTK 4 and libadwaita development files
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
