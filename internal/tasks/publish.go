package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
	"github.com/screwys/git-gui/internal/publish"
)

const publishUsage = `Usage: git-gui publish [OPTIONS] [TITLE [PATH...]]

Publishes work in this jj checkout as a pull request without switching it. The edits of PATHs move
into a new commit with TITLE on the topic branch; with TITLE and no PATH, every uncommitted change
does. The branch stays a parent of the working copy until it merges, so the files here stay as they
are. The first TITLE names the PR, later ones only their commits. Without TITLE and PATH the
branch's existing commits are published. Later paths for an open PR go out as their new edits.

--pr publishes onto an open PR someone else opened, a fork's included when it allows edits by
maintainers. The checkout must already hold that PR's changes ("gh pr diff NUMBER | git apply"):
every file the PR changes goes out as it is here, besides PATHs, so a file put back to the base
leaves the PR. The PR keeps its title; TITLE names the commit and, on the first publish, the
squash commit. Without TITLE the PR is merged with the base, checked and set to auto-merge as it
is. A fork's PR is the branch "pr/NUMBER" here, which --branch also takes; "git-gui wait-pr
NUMBER" follows it like one of your own.

--park sets work aside: the commit goes onto the branch, which then leaves the checkout, so the
files here lose its edits until "git-gui unpark BRANCH" brings them back. Without TITLE and PATH it
parks the branch as it stands. "git-gui publish --branch BRANCH" publishes a parked branch.

Paths whose edits build on edits another open branch of this checkout made in the same files are
queued instead: their commit goes on top of that branch, the checks run now, and git-gui-daemon
publishes the branch once the branches below merge. Follow a queued branch with "git-gui wait-pr
BRANCH".

The project's generate command runs before committing and its check command before pushing, both
in the publish workspace on exactly the published commits.

Options:
  --base BRANCH     Base branch (default: the repository's default branch)
  --branch BRANCH   Topic branch (default: named after TITLE)
  --pr NUMBER       Publish onto this open PR's branch instead of a topic branch of your own
  --body TEXT       PR body; needs GIT_GUI_ALLOW_PR_BODY=1, only for text the user gave
  --body-file PATH  PR body read from PATH; the same rule applies
  --run COMMAND     Run COMMAND in the publish workspace and publish what it changes
  --full            Run the pre-push hook (the full gate) instead of the check command
  --instant         No generation and no checks
  --park            Commit onto the branch and take it out of the checkout, unpublished
  --no-auto-merge   Leave auto-merge off
  --merge-commit    Merge with a merge commit instead of squashing
  --print-branch    Print the branch it would publish and stop
`

// Publish is "git-gui publish": one publish of the checkout in the current directory.
func Publish(args []string) int {
	cfg := config.Load()
	o := publish.Options{Base: git.DefaultBranch("."), AutoMerge: true, Generate: cfg.PublishGenerate, Check: cfg.PublishCheck}
	printBranch := false
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		flag := args[0]
		args = args[1:]
		value := func() string {
			if len(args) == 0 {
				fmt.Println(flag, "needs a value")
				os.Exit(2)
			}
			v := args[0]
			args = args[1:]
			return v
		}
		switch flag {
		case "--base":
			o.Base = value()
		case "--branch":
			o.Branch = value()
		case "--pr":
			number, err := strconv.Atoi(strings.TrimPrefix(value(), "#"))
			if err != nil || number <= 0 {
				fmt.Println("--pr needs a PR number")
				return 2
			}
			o.PR = number
		case "--body":
			o.Body, o.BodySet = value(), true
		case "--body-file":
			data, err := os.ReadFile(value())
			if err != nil {
				fmt.Println(err)
				return 2
			}
			o.Body, o.BodySet = string(data), true
		case "--run":
			o.Run = value()
		case "--full":
			o.Full = true
		case "--instant":
			o.Instant = true
		case "--park":
			o.Park = true
		case "--no-auto-merge":
			o.AutoMerge = false
		case "--merge-commit":
			o.MergeCommit = true
		case "--print-branch":
			printBranch = true
		case "-h", "--help":
			fmt.Print(publishUsage)
			return 0
		default:
			fmt.Println("unknown option:", flag)
			fmt.Print(publishUsage)
			return 2
		}
	}
	if o.Full && o.Instant {
		fmt.Println("choose either --full or --instant")
		return 2
	}
	if len(args) > 0 {
		o.Title, args = args[0], args[1:]
	}
	if len(args) > 0 {
		o.Paths = args
	}
	if printBranch {
		if o.PR != 0 {
			fmt.Fprintln(os.Stderr, "--print-branch names topic branches, not a PR's")
			return 2
		}
		c, err := publish.Open(".")
		if err == nil {
			var branch string
			if branch, err = publish.ResolveBranch(c.Root, o); err == nil {
				fmt.Println(branch)
				return 0
			}
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	result, err := publish.Publish(".", o)
	if err != nil {
		fmt.Println("Publish stopped:", err)
		return 1
	}
	if len(result.Queued) > 0 {
		return enqueue(o, result.Branch)
	}
	return 0
}

// enqueue asks git-gui-daemon to publish the queued branch once what it waits for merged.
func enqueue(o publish.Options, branch string) int {
	c, err := publish.Open(".")
	self, _ := os.Executable()
	command := []string{self, "publish", "--base", o.Base, "--branch", branch}
	if o.Instant {
		command = append(command, "--instant")
	}
	if !o.AutoMerge {
		command = append(command, "--no-auto-merge")
	}
	if o.MergeCommit {
		command = append(command, "--merge-commit")
	}
	if err == nil {
		_, err = client.Request(daemon.Request{Op: "schedule", Schedule: &daemon.Schedule{
			Branch: branch, Job: client.Job(c.Root, "publish", command, nil)}})
	}
	if err != nil {
		fmt.Printf("Queued, but git-gui-daemon did not take it (%v); run git-gui publish --branch %s once it runs.\n", err, branch)
		return 1
	}
	fmt.Println("Follow it with: git-gui wait-pr", branch)
	return 0
}

// Sync is "git-gui sync": bring the checkout onto the remote base branch, carrying its edits.
func Sync([]string) int {
	if _, err := publish.Sync(".", git.DefaultBranch("."), true); err != nil {
		fmt.Println(err)
		return 1
	}
	return 0
}

// Unpark is "git-gui unpark BRANCH": a parked branch's edits come back into this checkout.
func Unpark(args []string) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Println("Usage: git-gui unpark BRANCH\n\nBrings the edits of a branch parked with \"git-gui publish --park\" back into this checkout.")
		return 2
	}
	if err := publish.Unpark(".", args[0]); err != nil {
		fmt.Println(err)
		return 1
	}
	fmt.Println("Unparked", args[0]+": its edits are back in this checkout, on the branch.")
	return 0
}

// Status is "git-gui status", the start of an agent session in a checkout: it syncs it, names
// PRs that need their session and what the checkout's open branches already carry.
func Status([]string) int {
	c, err := publish.Open(".")
	if err != nil {
		fmt.Println(err)
		return 0
	}
	root, base := c.Root, git.DefaultBranch(c.Root)
	shared := root == c.Main
	if shared {
		fmt.Println("\n# " + filepath.Base(root) + " checkout")
	} else {
		fmt.Printf("\n# %s workspace %s\n", filepath.Base(c.Main), filepath.Base(root))
		fmt.Println("This session has a workspace of its own: its files, build directory and devel profile are separate from the shared checkout's. Run the app from here.")
	}
	done := make(chan error, 1)
	var synced bool
	go func() {
		var err error
		synced, err = publish.Sync(root, base, false)
		done <- err
	}()
	select {
	case err := <-done:
		switch {
		case err != nil:
			fmt.Println("git-gui sync failed; the checkout stays on an older "+base+":", err)
		case !synced:
			fmt.Println("A publish is running, so the checkout was not synced; run git-gui sync before reading git status.")
		}
	case <-time.After(60 * time.Second):
		fmt.Println("git-gui sync is still running; run it again before reading git status.")
	}
	fmt.Printf("The checkout stands on %s; origin/%s is %s.\n", short(root, "HEAD"), base, short(root, "origin/"+base))

	state := client.State()
	slug := git.RemoteSlug(root)
	switch age := time.Since(time.Unix(state.Updated, 0)); {
	case state.Updated == 0:
		fmt.Println("No PR state from git-gui-daemon; check systemctl --user status git-gui-daemon.")
	case age > 5*time.Minute:
		fmt.Printf("git-gui-daemon's PR state is %d minutes old; check systemctl --user status git-gui-daemon.\n", int(age.Minutes()))
	}
	for _, pr := range state.PRs {
		if pr.Repo == slug && (pr.Status == "failing" || pr.Status == "conflict") {
			detail := strings.Join(pr.Failed, ", ")
			if detail == "" {
				detail = "merge conflict with " + base
			}
			fmt.Printf("PR #%d %s: %s (%s)\n", pr.Number, pr.Status, detail, pr.URL)
		}
	}

	for _, b := range publish.Branches(root, base) {
		files := strings.Join(b.Files, ", ")
		switch {
		case b.Queued():
			fmt.Printf("Queued as %s behind %s, published by git-gui-daemon (not foreign work): %s\n", b.Name, strings.Join(b.Behind, ", "), files)
		case b.Pushed:
			number := ""
			for _, pr := range state.PRs {
				if pr.Repo == slug && pr.Branch == b.Name {
					number = fmt.Sprintf(" #%d", pr.Number)
				}
			}
			fmt.Printf("Published as %s%s, awaiting merge (not foreign work): %s\n", b.Name, number, files)
		default:
			fmt.Printf("Prepared as %s, not pushed (git-gui publish --branch %s pushes it): %s\n", b.Name, b.Name, files)
		}
	}
	for _, b := range publish.Parked(root, base) {
		fmt.Printf("Parked as %s, out of the checkout (git-gui unpark %s brings it back): %s\n", b.Name, b.Name, strings.Join(b.Files, ", "))
	}
	dirty := strings.Fields(gitLine(root, "diff", "--name-only"))
	switch {
	case len(dirty) == 0:
		fmt.Println("No uncommitted edits.")
	case shared:
		fmt.Printf("Other sessions' work in progress (%d files). Publish every file your task touches whole, theirs included; never stop over them.\n", len(dirty))
	default:
		fmt.Printf("Uncommitted edits in this workspace: %d files.\n", len(dirty))
	}
	return 0
}

func gitLine(root string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, _ := cmd.Output()
	return strings.TrimSuffix(string(out), "\n")
}

func short(root, rev string) string { return gitLine(root, "rev-parse", "--short", rev) }

const workspaceUsage = `Usage: git-gui workspace add NAME | remove NAME | list | hook

Session workspaces of the jj checkout here, under .claude/worktrees: each has its own files, a
copy-on-write copy of the build directory and of the devel profile, and its own working-copy
change on the base branch. "add" prints the workspace's path last. "remove" keeps its unpublished
changes in the repository. "hook" answers Claude Code's WorktreeCreate and WorktreeRemove hooks
from the event on standard input.
`

// Workspace is "git-gui workspace".
func Workspace(args []string) int {
	if len(args) == 0 {
		fmt.Print(workspaceUsage)
		return 2
	}
	// A hook's event names the checkout it runs for.
	var event struct {
		HookEventName string `json:"hook_event_name"`
		Name          string `json:"name"`
		Cwd           string `json:"cwd"`
		WorktreePath  string `json:"worktree_path"`
	}
	if args[0] == "hook" {
		if err := json.NewDecoder(os.Stdin).Decode(&event); err != nil {
			fmt.Fprintln(os.Stderr, "workspace hook: reading the event:", err)
			return 1
		}
		if event.Cwd != "" {
			_ = os.Chdir(event.Cwd)
		}
	}
	c, err := publish.Open(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	base := git.DefaultBranch(c.Main)
	add := func(name string) int {
		if !workspaceName.MatchString(name) || name == "default" || name == "publish" {
			fmt.Fprintln(os.Stderr, "workspace names are letters, digits, '.', '_' and '-':", name)
			return 2
		}
		path := publish.WorkspacePath(c.Main, name)
		// Messages go to standard error: the hook reads the path from standard output.
		stdout := os.Stdout
		os.Stdout = os.Stderr
		err := publish.AddWorkspace(c.Main, name, path, "remote_bookmarks(exact:"+strconv.Quote(base)+", remote=exact:\"origin\")", true)
		os.Stdout = stdout
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(path)
		return 0
	}
	remove := func(name string) int {
		if err := publish.RemoveWorkspace(c.Main, name); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	switch {
	case args[0] == "add" && len(args) == 2:
		return add(args[1])
	case args[0] == "remove" && len(args) == 2:
		return remove(filepath.Base(args[1]))
	case args[0] == "list":
		for _, name := range publish.Workspaces(c.Main) {
			fmt.Println(name, publish.WorkspacePath(c.Main, name))
		}
		return 0
	case args[0] == "hook":
		switch event.HookEventName {
		case "WorktreeCreate":
			return add(nonName.ReplaceAllString(event.Name, "-"))
		case "WorktreeRemove":
			return remove(filepath.Base(event.WorktreePath))
		}
		fmt.Fprintln(os.Stderr, "workspace hook: unexpected event", event.HookEventName)
		return 1
	}
	fmt.Print(workspaceUsage)
	return 2
}

var (
	workspaceName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	nonName       = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)
