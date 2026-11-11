package tasks

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/git"
	"github.com/screwys/git-gui/internal/publish"
)

const publishUsage = `Usage: git-gui publish [OPTIONS] [TITLE [PATH...]]

Publishes work in this checkout as a pull request without switching it. PATHs are committed with
TITLE onto the topic branch in the publish worktree; with TITLE and no PATH, every uncommitted
change is. The first TITLE names the PR, later ones only their commits. Without TITLE and PATH the
branch's existing commits are published. Later paths for an open PR go out as their difference
from it, and the published files here then match the PR.

The project's generate command runs before committing and its check command before pushing.

Options:
  --base BRANCH     Base branch (default from settings)
  --branch BRANCH   Topic branch (default: the current branch, or named after TITLE on the base)
  --body TEXT       PR body; needs GIT_GUI_ALLOW_PR_BODY=1, only for text the user gave
  --body-file PATH  PR body read from PATH; the same rule applies
  --run COMMAND     Run COMMAND in the publish worktree and publish what it changes
  --full            Run the pre-push hook (the full gate) instead of the check command
  --instant         No generation and no checks
  --prepare-only    Commit in the publish worktree, on git-gui-publish, without pushing
  --no-auto-merge   Leave auto-merge off
  --merge-commit    Merge with a merge commit instead of squashing
  --print-branch    Print the branch it would publish and stop
`

// Publish is "git-gui publish": one publish of the checkout in the current directory.
func Publish(args []string) int {
	cfg := config.Load()
	o := publish.Options{Base: cfg.DefaultBaseBranch, AutoMerge: true, Generate: cfg.PublishGenerate, Check: cfg.PublishCheck}
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
		case "--prepare-only":
			o.PrepareOnly = true
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
	if _, err := publish.Publish(".", o); err != nil {
		fmt.Println("Publish stopped:", err)
		return 1
	}
	return 0
}

// Sync is "git-gui sync": bring the base branch here up to origin, merging local edits.
func Sync([]string) int {
	if _, err := publish.Sync(".", config.Load().DefaultBaseBranch, true); err != nil {
		fmt.Println(err)
		return 1
	}
	return 0
}

// Status is "git-gui status", the start of an agent session in a shared checkout: it syncs the
// base branch, names PRs that need their session and the dirty files open PRs already carry.
func Status([]string) int {
	c, err := publish.Open(".")
	if err != nil {
		fmt.Println(err)
		return 0
	}
	root, base := c.Root, config.Load().DefaultBaseBranch
	fmt.Println("\n# " + filepath.Base(root) + " checkout")
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
			fmt.Println("git-gui sync failed; git status may list merged work:", err)
		case !synced:
			fmt.Println("A publish is running, so " + base + " was not synced; run git-gui sync before reading git status.")
		}
	case <-time.After(60 * time.Second):
		fmt.Println("git-gui sync is still running; run it again before reading git status.")
	}
	fmt.Printf("%s is at %s; origin/%s is %s.\n", base, short(root, "HEAD"), base, short(root, "origin/"+base))

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

	var dirty []string
	for _, list := range [][]string{{"diff", "--name-only", "-z", "HEAD"}, {"ls-files", "-z", "--others", "--exclude-standard"}} {
		for _, path := range strings.Split(gitLine(root, list...), "\x00") {
			if path != "" && !slices.Contains(dirty, path) {
				dirty = append(dirty, path)
			}
		}
	}
	if len(dirty) == 0 {
		fmt.Println("The checkout is clean.")
		return 0
	}
	published := map[string]int{}
	for _, pr := range state.PRs {
		if pr.Repo != slug || gitLine(root, "rev-parse", "-q", "--verify", "origin/"+pr.Branch) == "" {
			continue
		}
		for _, path := range dirty {
			if published[path] != 0 {
				continue
			}
			theirs := gitLine(root, "rev-parse", "-q", "--verify", "origin/"+pr.Branch+":"+path)
			if _, err := os.Stat(filepath.Join(root, path)); err == nil {
				if theirs != "" && gitLine(root, "hash-object", "--", path) == theirs {
					published[path] = pr.Number
				}
			} else if theirs == "" && gitLine(root, "rev-parse", "-q", "--verify", "HEAD:"+path) != "" {
				published[path] = pr.Number
			}
		}
	}
	byPR := map[int][]string{}
	others := 0
	for _, path := range dirty {
		if n := published[path]; n != 0 {
			byPR[n] = append(byPR[n], path)
		} else {
			others++
		}
	}
	for _, n := range slices.Sorted(maps.Keys(byPR)) {
		fmt.Printf("Published in #%d, awaiting merge (not foreign work): %s\n", n, strings.Join(byPR[n], ", "))
	}
	if others > 0 {
		fmt.Printf("Other sessions' work in progress (%d files). Publish every file your task touches whole, theirs included; never stop over them.\n", others)
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
