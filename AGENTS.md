# Working in this repository

- This is Git GUI, a separate project from Rufin. Do not apply Rufin's publish workflow here.
- Work directly on `main`; do not create a branch unless the user asks for one.
- After making edits, run the relevant checks, commit the changes, and push them to `origin/main`.
- If `origin/main` advanced, fetch and rebase before pushing. Do not force-push.
- Keep commits focused and use Conventional Commit subjects.
- The repository is public. Keep the README user-facing and put implementation notes here.

## Checks

```bash
python3 -m unittest discover -s tests
cd daemon && gofmt -l . && go vet ./... && go build -o /dev/null .
```

After changing the daemon, run `python3 install-desktop.py` so the running service uses the new
binary. Commands that are running keep going: each has its own supervisor process, and the unit
uses `KillMode=process`.

## Layout

```
main.py                     Main window, top bar, project tabs, Fetch/Pull/Sync, Android button
git-gui                     Launcher script
install-desktop.py          Launcher link, desktop entry, daemon build and user service
core/
  config.py                 AppConfig, stored in ~/.config/git-gui/config.json
  state.py                  UI and form state, stored in ~/.config/git-gui/state.json
  git_manager.py            Status, diff stats, diffs, branch comparison, PR drafts
  daemon.py                 Socket client for git-gui-daemon and reader of its state
  jobs.py                   Reads job records
  runner.py                 Starts jobs through the daemon, attaches and replays their output
  instance.py               Single window activation through a local socket
  push.py                   Fetch and rebase before a normal push
  release.py                Release tags without touching the working tree
  packages.py               Reads and edits package lists without rewriting recipes
  notifications.py          notify-send wrapper
ui/tabs/                    publish_pr_tab (Rufin), release_tab (Rufin), igloo_release_tab,
                            commit_tab, generic_release_tab, packages_tab, changes_tab
ui/components/              File list, diff viewer, terminal, PR checks, release jobs,
                            RPM catalog, output tab selection, schedule follower, toast, icons
ui/dialogs/                 Settings and confirmation dialogs
daemon/                     git-gui-daemon (Go, standard library only) and its systemd unit
tests/                      unittest suites, Qt tests run offscreen
```

## Commands

- The GUI asks git-gui-daemon to start a command (`start` over `$XDG_RUNTIME_DIR/git-gui-daemon.sock`)
  with its own environment, so commands find the user's tools. The daemon starts
  `git-gui-daemon supervise`, a separate process in its own session that runs the command, holds
  the command slot's lock and writes `job.json` and `output.log` under
  `$XDG_STATE_HOME/git-gui/jobs`. The runner attaches to running jobs and replays saved output.
  Stop, pause and resume are `signal` requests.
- Running Rufin publish or release helpers started outside the app show as observed processes. Their
  output and exit codes can't be recovered. A process that disappears without a record is reported
  as unknown.
- Output tabs follow the stage (`ui/components/activity_tabs.py`): local preparation and checks show
  the terminal, PR checks and dispatched workflows show remote progress, local failures return to
  the log. Automatic selection only happens on stage changes, so a tab the user picked stays.

## Polling

- PR checks: every 12 s while visible, every 60 s in the tray.
- Release jobs: every 15 s while visible, every 60 s in the tray.
- Working tree: every 30 s while visible and idle. Background monitoring never scans the working
  tree or builds diffs.
- PR discovery runs on startup and refresh instead of trusting a saved selection.

## Rufin publishing

- The form state of a publish lives in `state.json` as `rufin_publish_pending`. It is cleared when
  the publish completes or when branch detection finds the branch's PR merged or closed.
- **Publish after PR merges** and **Release after PR merges** are daemon schedules. The tabs follow
  them through `ScheduleFollower`, which reads `daemon.json`.
- With full checks, the schedule runs `git-gui-daemon publish-checks` as a job: `--prepare-only`,
  then the pre-push hook inside a capped `systemd-run` scope. If the inputs are unchanged when the PR
  merges, the publish runs without `--full`, so the gate does not run twice. Changed inputs run the
  checks again.
- PR titles and bodies saved by `publish-pr.sh` in branch config are read back as drafts.

## git-gui-daemon

- One GraphQL search (`is:pr is:open author:@me`) every 30 s with the `gh auth token`. The token is
  read again after a 401 and never stored.
- Branch updates use `PUT /repos/{repo}/pulls/{n}/update-branch` with `expected_head_sha`, only for
  PRs with auto-merge, completed passing checks and `mergeStateStatus == BEHIND`.
- Notifications are keyed by repository, PR, head commit and status and persisted in the state
  file, so a restart does not repeat them.
- A PR that leaves the open search stays in `leaving` until GitHub reports it merged or closed,
  so a failed lookup is retried instead of missing a merge.
- Sync applies to the checkout at `rufin_repo_path` in git-gui's config, using its publish helper.
  It waits while the helper's `.local/publish-checkout.lock` is held and retries on the next pass.
- Schedules are checked every 10 s while one is active. A schedule's `status` is `waiting`,
  `checking`, `checked`, `started`, `failed`, `closed` or `cancelled`. A new schedule for the same
  command slot cancels the old one. Finished schedules are dropped after a day.
- `daemon.json` is a contract with its readers (git-gui and Rufin's session startup report).
  Keep `updated`, `prs[].repo`, `number`, `title`, `url`, `branch`, `status` and `failed`, and
  `schedules[]` stable. A PR's `status` is one of `running`, `passing`, `behind`, `updating`,
  `failing` or `conflict`.

## Releases for other projects

- Release tags are annotated and created at the commit the user confirms. A retry reuses a tag at
  the same commit; a tag at another commit is never replaced.
- Unticking **Push tag to origin** publishes a release for a tag already on GitHub, which must
  point at the selected commit. Unticking both options only creates the tag locally.
- A tag workflow that runs `gh release create` or `softprops/action-gh-release` is detected and
  selected as the publisher; the app waits for it, then sets the notes. Otherwise `gh` creates
  the release.

## Packages

- Package lists come from BlueBuild recipes (DNF and rpm-ostree install and remove lists),
  `Brewfile`s and plain lists such as `manifests/nix-profile.txt`. Edits keep the file's layout
  and fill a subject such as `feat(packages): add postgres and postgres-server` unless the user
  wrote one.

- RPM completion runs `dnf --cacheonly repoquery` once per app session, on first use, and shares
  the result across project tabs. It never downloads metadata.
- In nocblue, a manifest edit also rewrites its runtime copy under
  `files/system/usr/share/nocblue/manifests`.
