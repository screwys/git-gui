# Working in this repository

- Work directly on `main`; do not create a branch unless the user asks for one.
- After making edits, run the relevant checks, commit the changes, and push them to `origin/main`.
- If `origin/main` advanced, fetch and rebase before pushing. 
- Write scoped subjects, `area: description`, where the area is the part of the code that changed (`daemon:`,
  `ui/publish:`, `packages:`).
- The repository is public. Keep the README user-facing and put implementation notes here.

## Checks

```bash
gofmt -l . ; go vet ./... ; go test ./...
go build -o git-gui . && ./git-gui install
```

gotk4 prints cgo `free` warnings while building; filter them with
`grep -v "cgo-generated\|^# github.com/diamondburned"`. `git-gui install`
restarts the daemon; running commands keep going because each has its own supervisor process and
the unit uses `KillMode=process`.

To look at the window without a desktop, run it under Broadway:
`gtk4-broadwayd --port 8099 :9 &` then `GDK_BACKEND=broadway BROADWAY_DISPLAY=:9 ./git-gui` and
open http://localhost:8099. A second instance needs its own session bus (`dbus-run-session`),
because the app ID is single-instance. Never click buttons that commit, push, publish or release
while testing against real repositories.

## Window and daemon

- The window draws and sends requests. Anything that watches GitHub in the background, runs a
  command or must work with the window closed belongs to the daemon. The window never sends
  notifications; the daemon does.
- The window starts commands with `start` over `$XDG_RUNTIME_DIR/git-gui-daemon.sock`, sending its
  own environment so commands find the user's tools. The daemon starts `git-gui supervise`, a
  separate process in its own session that runs the command, holds the slot's lock (one job per
  repo and key), writes `job.json` and `output.log` under `$XDG_STATE_HOME/git-gui/jobs` and sends
  the finished notification. The runner attaches to running jobs and replays saved output. Stop,
  pause and resume are `signal` requests.
- Rufin publish or release helpers started outside the app show as observed processes, without
  output or exit codes.
- Every tab has a terminal notebook. Top-bar commands (fetch, pull, sync, Android, restart) run in
  the project's `git-tools` slot and show on one Commands page that moves into the visible tab's
  terminal, so they never share a slot with the tab's own job. Changes and Packages have no job of
  their own and get a shell-only terminal on `git-tools-shell`.
- Every view that reads `daemon.json` registers with `onDaemonState`; the daemon replaces the file
  on every pass. Separate GIO monitors per view stopped delivering events, so keep one.
- The 30 s timer reloads only the working tree of the visible tab (`tab.local`). It must not call
  anything that queries GitHub.

## Rufin publishing

- The form state of a publish lives in `state.json` as `rufin_publish_pending`. It is cleared when
  the publish completes or when PR discovery finds the branch's PR merged or closed.
- **Publish after PR merges** and **Release after PR merges** are daemon schedules followed through
  `ScheduleFollower`. With full checks, the schedule runs `git-gui publish-checks` as a job:
  `--prepare-only`, then the pre-push hook inside a capped `systemd-run` scope. Unchanged inputs at
  merge time publish without `--full`; changed inputs run the checks again.
- PR titles and bodies saved by `publish-pr.sh` in branch config are read back as drafts.

## git-gui-daemon

- One GraphQL search (`is:pr is:open author:@me`) every 30 s, 10 s while a schedule is active, with
  the `gh auth token`. The token is read again after a 401 and never stored.
- Branch updates use `PUT /repos/{repo}/pulls/{n}/update-branch` with `expected_head_sha`, only for
  PRs with auto-merge, completed passing checks and `mergeStateStatus == BEHIND`.
- Notifications: failing and conflicting PRs once per head commit (keys persisted), checks passing,
  merges (unless a schedule announces it), schedule outcomes, release workflow conclusions and
  finished jobs. Failures are critical, the rest normal.
- Every PR that leaves the open search stays in `leaving` until GitHub reports it merged or closed.
  A Rufin merge syncs the checkout at `rufin_repo_path` with its publish helper, waiting while
  `.local/publish-checkout.lock` is held.
- Releases: the window calls `follow_release` with repo, kind, tag, its pid and the job path. The
  daemon polls a followed release every pass while a release job runs, a workflow is unfinished or
  the window asked, every 5 minutes while only the window lives, and stops once the runs finished
  and the window is gone.
- `daemon.json` is a contract with its readers (the window and Rufin's session startup report).
  Keep `updated`, `prs[].repo`, `number`, `title`, `url`, `branch`, `head`, `status`, `failed`
  and `checks`, `schedules[]` and `releases` stable. `git-gui wait-pr`, which agents run in the
  background after publishing, waits on `prs[]` and exits on failing, conflict, merged or closed. A PR's `status` is one of `running`, `passing`,
  `behind`, `updating`, `failing` or `conflict`.

## Releases for other projects

- Release tags are annotated and created at the commit the user confirms. A retry reuses a tag at
  the same commit; a tag at another commit is never replaced.
- Unticking **Push tag to origin** publishes a release for a tag already on GitHub, which must
  point at the selected commit. Unticking both options only creates the tag locally.
- A tag workflow that runs `gh release create` or `softprops/action-gh-release` is detected and
  selected as the publisher; the job waits for it, then sets the notes. Otherwise `gh` creates the
  release.

## Packages

- Package lists come from BlueBuild recipes (DNF and rpm-ostree install and remove lists),
  `Brewfile`s and plain lists such as `manifests/nix-profile.txt`. Edits keep the file's layout
  and fill a subject such as `packages: add postgres and postgres-server` unless the user
  wrote one. `go test ./internal/packages` covers the edits and the mirror restore.
- RPM completion runs `dnf --cacheonly repoquery` once per app session, on first use, and shares
  the result across project tabs. It never downloads metadata.
- In nocblue, a manifest edit also rewrites its runtime copy under
  `files/system/usr/share/nocblue/manifests`, and restores both files if a write fails.
