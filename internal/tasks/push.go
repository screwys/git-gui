// Package tasks holds the commands git-gui runs as jobs, started as "git-gui <task> ...".
package tasks

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Push brings the branch up to date with origin, rebasing local commits when origin moved, then
// pushes it. It returns the process exit status.
func Push(args []string) int {
	branch, skipHook := "", false
	for _, arg := range args {
		if arg == "--skip-hook" {
			skipHook = true
		} else {
			branch = arg
		}
	}
	if err := push(branch, skipHook); err != nil {
		fmt.Println("Push stopped:", err)
		fmt.Println("Your local commits are retained. If a rebase is in progress, resolve conflicts and run " +
			"'git rebase --continue', or use 'git rebase --abort'. Then retry Push.")
		return 1
	}
	return 0
}

func push(branch string, skipHook bool) error {
	if branch == "" {
		return errors.New("no branch given")
	}
	if err := run("git", "fetch", "origin"); err != nil {
		return err
	}
	remote := "refs/remotes/origin/" + branch
	if exec.Command("git", "show-ref", "--verify", "--quiet", remote).Run() == nil &&
		exec.Command("git", "merge-base", "--is-ancestor", remote, "HEAD").Run() != nil {
		fmt.Printf("Applying local commits on top of origin/%s...\n", branch)
		if err := run("git", "rebase", "--rebase-merges", "--autostash", remote); err != nil {
			return err
		}
		if out, _ := exec.Command("git", "diff", "--name-only", "--diff-filter=U").Output(); strings.TrimSpace(string(out)) != "" {
			return errors.New("restoring local changes caused conflicts. Resolve them before pushing")
		}
	}
	command := []string{"push"}
	if skipHook {
		command = append(command, "--no-verify")
	}
	return run("git", append(command, "origin", branch)...)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
