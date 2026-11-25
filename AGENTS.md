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

- Rufin is a colocated jj repository (`internal/publish/jj.go`). A checkout's working-copy
  commit `@` holds the uncommitted edits; its parents are the base (`master@origin`, the first
  parent, so Git's HEAD is the base) and the checkout's open branches, one bookmark each. Every jj
  command that rewrites `@` or a parent of it runs in that checkout: a workspace rewritten from
  another one goes stale, and edits made while it is stale do not survive `update-stale`. jj
  commands running at once in one workspace are safe.
- Repo config, in jj's repo config (`jj config edit --repo`): `trunk()` is `master@origin`,
  signing `drop` with `revsets.git-sign-on-push`, and `git.abandon-unreachable-commits = false`.
  With the default, a fetch that sees a merged branch deleted abandons its commits, rebasing other
  workspaces' working copies from the fetching one.
- `git-gui publish` makes the branch's next commit on the base, the branch, or the open branches
  whose edits the paths build on, adds it as a parent of `@`, and moves the paths' edits into it
  with `jj squash`, so the files here never change. The bookmark is set at once, so rerunning a
  stopped publish continues it. Generate and check run in the `publish` workspace at
  `<git-common-dir>/git-gui/publish`, on an empty child of the commit; what generation changes is
  squashed in from the checkout (`publish@`), and `keepEdits` gives back the checkout's version
  of a file whose edit made there conflicts with it. `--run` without paths leaves the commit out
  of `@`, as the release prep needs. `--pr N` builds on the PR's head (`N@pull` for a fork,
  fetched into `refs/remotes/pull/N`, which also marks the PR adopted) and restores the files from
  `@` whole. Pushes go through `jj git push`, which signs; a fork's goes to its URL with
  `git push` after `jj sign`.
- Queued publishes (`queue.go`) are bookmarks that are not pushed and have other open branches
  below them; the daemon's schedule reruns `git-gui publish --branch <branch>` once none of those
  is open. Cancelling abandons the branch's own commits with `--restore-descendants`, so the edits
  stay here.
- `refs/git-gui/published/<branch>` keeps each branch's head. `sync` (`sync.go`) fetches, then
  takes out every branch whose bookmark is gone: a merged one by rebasing what was built on it
  straight onto the base and abandoning it, a closed one with `--restore-descendants`; then it
  rebases `@` alone onto the base, leaving pushed branches where they are, and puts the shared
  checkout's detached HEAD back on `master` (Claude Code shows no diff without a branch). Its operations carry a
  tag in `operation.username`, so a sync that would leave a conflict reverts exactly its own
  operations with `jj op revert`, after an untagged snapshot. The daemon deletes the bookmark of a
  PR that merged or closed (`publish.Forget`), and a merge's `--delete-branch` makes the fetch
  delete it.
- Session workspaces (`workspace.go`, `git-gui workspace`) live in `.claude/worktrees/<name>`,
  which `.git/info/exclude` keeps out of the shared checkout, and get `cp --reflink` copies of
  `target/debug`, `android/.toolchains` and the devel profile (`.local/profile`, which Rufin's
  `just debug` uses). A link in place of the toolchains would be tracked, since the ignore rule
  names a directory, and publish would carry it. The build directory is copied, not shared:
  Cargo matches a checkout's crates to builds by relative path and file time, so a shared build
  directory reuses another checkout's builds. Rufin's `.claude/settings.local.json` points the
  WorktreeCreate and WorktreeRemove hooks at `git-gui workspace hook`. Each workspace syncs
  itself; another checkout's branch is left to its own sync.
- `git-gui status` is the agents' session start: sync, failing PRs, the checkout's open branches.
- `git-gui rufin-release` publishes the preparation PR with `--run "xtask release prepare"`, tags
  its merge commit with `xtask release notes`, follows Rufin's `release.yml`, then dispatches the
  Homebrew cask and lands the Flathub PR. Every step is skipped when already done, so Resume
  reruns the same command. The daemon shows `release.yml`'s jobs as the release rows.
- The form state of a publish lives in `state.json` as `rufin_publish_pending`. It is cleared when
  the publish completes or when PR discovery finds the branch's PR merged or closed.
- A fork's PR is the branch `pr/N` everywhere (publish, the queue, `daemon.json`, `wait-pr`). Its
  title and body are never edited: the first title is the squash subject passed to
  `gh pr merge --auto`.
- **Release after PR merges** is a daemon schedule followed through `ScheduleFollower`.
- `--full` runs the pre-push hook before the push instead of through it, like the check command,
  so a queued publish runs the same gate.
- PR titles and bodies a failed publish saved in branch config (`rufin-pr-title`, `rufin-pr-body`)
  are read back as drafts.

## git-gui-daemon

- One GraphQL query every 30 s, 10 s while a schedule is active, with the `gh auth token`: the
  user's open PRs (`is:pr is:open author:@me`) and, by number, the adopted PRs of the publishing
  checkout, which are watched like the user's own. The token is read again after a 401 and never stored.
- Branch updates use `PUT /repos/{repo}/pulls/{n}/update-branch` with `expected_head_sha`, only for
  PRs with auto-merge, no failing checks and `mergeStateStatus == BEHIND`, also while checks run.
  A failing PR gets main merged in by its fix push instead.
- Notifications: failing and conflicting PRs once per head commit (keys persisted), checks passing,
  merges (unless a schedule announces it), schedule outcomes, release workflow conclusions and
  finished jobs. Failures are critical, the rest normal.
- Every PR that leaves the open search stays in `leaving` until GitHub reports it merged or closed.
  A Rufin merge or close syncs the checkout at `rufin_repo_path` and its session workspaces,
  waiting while a publish holds the lock. Every pass snapshots them, so `jj op restore` can bring
  back an edit a stash, reset or checkout threw away.
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
