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
cd prwatchd && gofmt -l . && go vet ./... && go build -o /dev/null .
```

After changing `prwatchd`, run `python3 install-desktop.py` so the running service uses the new
binary.

## Layout

```
main.py                     Main window, top bar, project tabs, Fetch/Pull/Sync, Android button
git-gui                     Launcher script
install-desktop.py          Launcher link, desktop entry, prwatchd build and user service
core/
  config.py                 AppConfig, stored in ~/.config/git-gui/config.json
  state.py                  UI and form state, stored in ~/.config/git-gui/state.json
  git_manager.py            Status, diff stats, diffs, branch comparison, PR drafts
  jobs.py                   Detached command supervisor and durable job records
  runner.py                 Attaches to jobs and replays their saved output
  instance.py               Single window activation through a local socket
  push.py                   Fetch and rebase before a normal push
  release.py                Release tags without touching the working tree
  packages.py               Reads and edits package lists without rewriting recipes
  publish_checks.py         Full pre-push checks while a publish waits for another PR
  notifications.py          notify-send wrapper
ui/tabs/                    publish_pr_tab (Rufin), release_tab (Rufin), igloo_release_tab,
                            commit_tab, generic_release_tab, packages_tab, changes_tab
ui/components/              File list, diff viewer, terminal, PR checks, release jobs,
                            RPM catalog, output tab selection, toast, icons
ui/dialogs/                 Settings and confirmation dialogs
prwatchd/                   Go PR watcher (standard library only) and its systemd unit
tests/                      unittest suites, Qt tests run offscreen
```

## Commands

- `core/jobs.py` starts every command in its own process session under a small supervisor, so
  commands outlive the GUI. Records and logs live in `$XDG_STATE_HOME/git-gui/jobs`. The runner
  attaches to running jobs and replays saved output for finished ones.
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
- **Publish after PR merges** with full checks runs `core/publish_checks.py` while waiting. It
  prepares the commit with `--prepare-only` and runs the pre-push hook. If the inputs are unchanged
  when the PR merges, the publish runs without `--full`, so the gate does not run twice.
- PR titles and bodies saved by `publish-pr.sh` in branch config are read back as drafts.

## prwatchd

- One GraphQL search (`is:pr is:open author:@me`) every 30 s with the `gh auth token`. The token is
  read again after a 401 and never stored.
- Branch updates use `PUT /repos/{repo}/pulls/{n}/update-branch` with `expected_head_sha`, only for
  PRs with auto-merge, completed passing checks and `mergeStateStatus == BEHIND`.
- Notifications are keyed by repository, PR, head commit and status and persisted in the state
  file, so a restart does not repeat them.
- Sync applies to the checkout at `rufin_repo_path` in git-gui's config, using its publish helper.
  It waits while the helper's `.local/publish-checkout.lock` is held and retries on the next pass.
- `pr-watch.json` is a contract with its readers (git-gui and Rufin's session startup report).
  Keep `updated` and `prs[].repo`, `number`, `title`, `url`, `branch`, `status` and `failed`
  stable. `status` is one of `running`, `passing`, `behind`, `updating`, `failing` or `conflict`.

## Packages

- RPM completion runs `dnf --cacheonly repoquery` once per app session, on first use, and shares
  the result across project tabs. It never downloads metadata.
- In nocblue, a manifest edit also rewrites its runtime copy under
  `files/system/usr/share/nocblue/manifests`.
