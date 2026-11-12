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
- A `git-gui publish` or `git-gui rufin-release` started outside the window, by an agent, shows in
  its tab as an observed process, without output or exit code.
- Every tab has a terminal notebook. Top-bar commands (fetch, pull, sync, Android, restart) run in
  the project's `git-tools` slot and show on one Commands page that moves into the visible tab's
  terminal, so they never share a slot with the tab's own job. Changes and Packages have no job of
  their own and get a shell-only terminal on `git-tools-shell`.
- Every view that reads `daemon.json` registers with `onDaemonState`; the daemon replaces the file
  on every pass. Separate GIO monitors per view stopped delivering events, so keep one.
- The 30 s timer reloads only the working tree of the visible tab (`tab.local`). It must not call
  anything that queries GitHub.

## Rufin publishing

- `git-gui publish` (`internal/publish`) publishes from the shared checkout without switching it:
  the paths go out as a patch onto the topic branch in the worktree at
  `<git-common-dir>/git-gui/publish` (branch `git-gui-publish`), where the configured generate and
  check commands run, and the published files are written back. `git-gui sync` and the daemon's
  sync after merges take the same lock, `<git-common-dir>/git-gui-publish.lock`. `git-gui status`
  is the agents' session start: sync, failing PRs, dirty files open PRs already carry.
- `git-gui rufin-release` publishes the preparation PR with `--run "xtask release prepare"`, tags
  its merge commit with `xtask release notes`, follows Rufin's `release.yml`, then dispatches the
  Homebrew cask and lands the Flathub PR. Every step is skipped when already done, so Resume
  reruns the same command. The daemon shows `release.yml`'s jobs as the release rows.
- The form state of a publish lives in `state.json` as `rufin_publish_pending`. It is cleared when
  the publish completes or when PR discovery finds the branch's PR merged or closed.
- The merge queue (`internal/publish/queue.go`): a publish whose paths hold edits on top of another
  of the user's open PRs or of a queued publish, in files that one changes, queues instead of
  pushing. Its commit goes to `refs/git-gui/queue/<branch>` on a base commit that merges HEAD, the
  branch's own remote and those carriers; the base's `Queued-behind:` lines name them, so the
  commit's diff is the queued work alone. The checks run on that commit, then `git-gui publish`
  registers a daemon schedule with `branch` set, which reruns `git-gui publish --branch <branch>`
  once no carrier is an open PR or queued. That run queues again while GitHub still lists one open,
  otherwise it applies the queue commit's diff onto the base branch, generates, skips the checks and
  pushes. Any path publish takes back out, with `git apply -R --3way`, the edits of queued
  publishes it does not build on (those waiting on it, newest first); same-line edits stop it.
  Paths holding only an open PR's version are left out. Cancelling deletes the ref; the edits stay.
- **Release after PR merges** is a daemon schedule followed through `ScheduleFollower`.
- `--full` runs the pre-push hook before the push instead of through it, like the check command,
  so a queued publish runs the same gate.
- PR titles and bodies a failed publish saved in branch config (`rufin-pr-title`, `rufin-pr-body`)
  are read back as drafts.

## git-gui-daemon

- One GraphQL search (`is:pr is:open author:@me`) every 30 s, 10 s while a schedule is active, with
  the `gh auth token`. The token is read again after a 401 and never stored.
- Branch updates use `PUT /repos/{repo}/pulls/{n}/update-branch` with `expected_head_sha`, only for
  PRs with auto-merge, no failing checks and `mergeStateStatus == BEHIND`, also while checks run.
  A failing PR gets main merged in by its fix push instead.
- Notifications: failing and conflicting PRs once per head commit (keys persisted), checks passing,
  merges (unless a schedule announces it), schedule outcomes, release workflow conclusions and
  finished jobs. Failures are critical, the rest normal.
- Every PR that leaves the open search stays in `leaving` until GitHub reports it merged or closed.
  A Rufin merge syncs the checkout at `rufin_repo_path`, waiting while a publish holds its lock.
- Releases: the window calls `follow_release` with repo, kind, tag, its pid and the job path. The
  daemon polls a followed release every pass while a release job runs, a workflow is unfinished or
  the window asked, every 5 minutes while only the window lives, and stops once the runs finished
  and the window is gone.
- `daemon.json` is a contract with its readers (the window and Rufin's session startup report).
  Keep `updated`, `prs[].repo`, `number`, `title`, `url`, `branch`, `head`, `status`, `failed`
  and `checks`, `schedules[]` and `releases` stable. `git-gui wait-pr`, which agents run in the
  background after publishing, waits on `prs[]` and exits on failing, conflict, merged or closed;
  given a queued branch it follows `schedules[]` and the publish job's record until the PR opens. A PR's `status` is one of `running`, `passing`,
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
