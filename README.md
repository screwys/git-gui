# Git GUI

A Linux desktop app for committing, publishing pull requests and cutting releases in a handful
of my repositories, plus `prwatchd`, a small service that keeps an eye on my open pull requests.
The app is Python with PyQt6. The watcher is Go.

It is built around my own projects. Rufin gets tabs that drive Rufin's own publish and release
scripts. Igloo, nocblue and any repository you add get a commit tab, a release tab and, when they
keep package lists, a package editor. Other repositories work fine with the generic tabs. The
Rufin tabs expect Rufin's scripts.

## What it does

### Projects and changes

Each project has its own tab. Rufin, Igloo and nocblue are there by default, and **+** adds any
Git repository. The app remembers added projects and the last one you had open.

The file list shows modified, added, deleted and untracked files with their line counts. Click a
file to see its diff, or tick several and press **View Diff** to see them together. You can edit a
file from the diff view.

### Rufin

**Publish PR** runs Rufin's `publish-pr.sh`. Tick the files, write a commit subject and an
optional PR description, then publish. Options cover squash auto-merge, waiting for CI, the full
pre-push checks (`--full`) and translation catalogs. The first subject names the PR, and later
publishes to the same branch add commits under that title. The PR's checks appear next to the log.

A failed or cancelled publish keeps its form. **Resume PR** retries it and reuses a commit the
first run already made. **Publish after PR merges** takes a PR number and publishes once that PR
has merged. When its PR merges or closes, a saved publish is cleared and the form starts fresh.

**Release** runs `release-rufin.sh`. It suggests the next patch or minor version from the latest
tag, gives you a release notes editor, and passes `--push`, `--dry-run`, `--replace`,
`--skip-flathub`, `--skip-github-release` or `--resume-after-prep`. **Jobs and Progress** follows
the release's workflow runs, its Flathub PR and its COPR builds. **Resume selected release** fills
the form for a release you started earlier.

In the top bar, **Sync** runs `publish-pr.sh --sync` instead of Fetch and Pull. The Android button
builds the development APK and installs it on the connected phone (`just build apk-dev`, then
`just install android`).

### Other projects

**Commit & Push** commits the ticked files, fetches, rebases your commits onto the remote and
pushes. Git's autostash keeps your other edits. If the rebase stops on a conflict, resolve or abort
it before trying again. With nothing to commit, **Push** pushes the commits you already have.
There's an option to skip the pre-push hook.

**Release** creates an annotated tag at the commit you pick, pushes it and publishes a GitHub
release with your notes. Patch, Minor and Major suggest a version, and any valid tag name works.
The confirmation shows the exact commit, so commit version bumps first.

- Untick **Push tag to origin** to publish a release for a tag that's already on GitHub. That tag
  must point at the selected commit.
- Untick both options to only create the tag locally.
- A retry reuses a tag that already points at the same commit. A tag at another commit is never
  replaced.
- If the repository's tag workflow publishes releases itself (`gh release create` or
  `softprops/action-gh-release`), the app waits for that workflow and then sets your notes.
  Otherwise the GitHub CLI creates the release. You can change the publisher in the form.

**Packages** edits package lists. nocblue shows it by default. For other projects, turn it on in
Settings. Pick RPM, Brew or Nix and a source file, then add packages with the plus button or tick
entries and delete them. The tab shows the resulting diff and fills in a commit subject such as
`feat(packages): add postgres and postgres-server`, unless you already wrote one. Commit it from
**Commit & Push**.

The app finds package lists in BlueBuild recipes (DNF and rpm-ostree install and remove lists),
`Brewfile`s and plain lists such as `manifests/nix-profile.txt`. **Add source** registers another
file. RPM names autocomplete from your local DNF cache. It never downloads metadata, so run
`dnf makecache` if the cache is empty. In nocblue, editing a manifest also updates its copy under
`files/system/usr/share/nocblue/manifests`.

### Commands keep running

Every command the app starts runs in its own process session. Closing the window, quitting or
restarting the app leaves it running. When you open the app again, it reattaches and shows the
saved output and exit status.

- **Cancel** stops the command and its child processes.
- **Pause** freezes it and **Resume** continues from the same point.
- **History** lists earlier commands with their results.
- The terminal shows the last 10,000 lines. The full log stays on disk.

If you start Rufin's publish or release helper from a terminal, the app shows it as an observed
process. It can't recover that output or exit code, and it can't stop it.

### Tray and notifications

With **Settings > Close to tray**, closing the window keeps the app in the tray. It still notifies
you when a command finishes or fails, and it checks remote status once a minute. **Open Git GUI**
in the tray menu, or launching the app again, brings the window back. Without a system tray,
closing the window quits.

### prwatchd

`prwatchd` runs as a user service whether or not the app is open. Every 30 seconds it reads your
open pull requests on GitHub, across all repositories, with one search.

- A PR with auto-merge on, passing checks and a branch behind its base gets its branch updated on
  GitHub. Repositories that require up-to-date branches would otherwise leave auto-merge waiting.
- Failed checks and merge conflicts send one desktop notification per pushed commit.
- After a Rufin PR merges, it syncs your Rufin checkout with `publish-pr.sh --sync`.

## Requirements

- Linux with a systemd user session and a notification daemon (`notify-send`)
- Python 3 with PyQt6 and PyYAML (see `requirements.txt`)
- Git and the GitHub CLI, logged in with `gh auth login`
- Go, to build `prwatchd`
- DNF, only for RPM name completion

## Setup

```bash
git clone https://github.com/screwys/git-gui.git
cd git-gui
python3 -m pip install --user -r requirements.txt   # or your distribution's PyQt6 and PyYAML
gh auth login
python3 install-desktop.py
```

`install-desktop.py` links the `git-gui` command into `~/.local/bin`, adds the app to your
application menu, builds `prwatchd` into `~/.local/bin` and enables and restarts its user service.
Run it again after pulling updates or moving the checkout.

Start the app with `git-gui` or from your application menu. The default projects are expected in
`~/Projects/Rufin`, `~/Projects/Igloo` and `~/Projects/nocblue`; change them in **Settings**.
Settings also holds the Rufin script paths (relative to the
repository or absolute) and the defaults for base branch, auto-merge and waiting for CI.

## Where things live

| What | Where |
| --- | --- |
| Settings | `~/.config/git-gui/config.json` |
| Window and form state | `~/.config/git-gui/state.json` |
| Command logs and history | `~/.local/state/git-gui/jobs` |
| prwatchd state | `~/.local/state/git-gui/pr-watch.json` |
| prwatchd log | `journalctl --user -u prwatchd` |

The state paths follow `$XDG_STATE_HOME` when it's set.
