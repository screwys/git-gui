package tasks

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

// WaitPush asks git-gui-daemon to follow the checks of a commit pushed straight to the current
// branch (HEAD unless given) and waits, without querying GitHub, until they fail or all pass. An
// agent runs it in the background after pushing. Exit status: 0 passed, 1 failing, 2 a newer
// push to the branch replaced it, 3 no checks ran, 4 unable to wait.
func WaitPush(args []string) int {
	commit := "HEAD"
	if len(args) > 0 {
		commit = args[0]
	}
	top, err := git.TopLevel(".")
	repo := git.RemoteSlug(".")
	branch, _ := git.Run(".", "symbolic-ref", "--short", "-q", "HEAD")
	head, headErr := git.Run(".", "rev-parse", "--verify", "-q", commit+"^{commit}")
	if err != nil || repo == "" || branch == "" || headErr != nil {
		fmt.Println("usage: git-gui wait-push [commit]  (on a branch of a GitHub checkout, after pushing)")
		return 4
	}
	if exec.Command("git", "merge-base", "--is-ancestor", head, "refs/remotes/origin/"+branch).Run() != nil {
		fmt.Printf("wait-push: %.10s is not on origin/%s; push it first\n", head, branch)
		return 4
	}
	if _, err := client.Request(daemon.Request{Op: "follow_push", Cwd: top, Branch: branch, Head: head}); err != nil {
		fmt.Println("wait-push:", err)
		return 4
	}
	for {
		state := client.State()
		if time.Since(time.Unix(state.Updated, 0)) > 5*time.Minute {
			fmt.Println("wait-push: git-gui-daemon has not updated its state for 5 minutes; check systemctl --user status git-gui-daemon")
			return 4
		}
		i := slices.IndexFunc(state.Pushes, func(p *daemon.Push) bool { return strings.EqualFold(p.Repo, repo) && p.Branch == branch })
		if i < 0 {
			fmt.Println("wait-push: git-gui-daemon stopped following", branch)
			return 4
		}
		switch p := state.Pushes[i]; {
		case p.Head != head:
			fmt.Printf("A newer push (%.10s) replaced %.10s on %s; wait for that one\n", p.Head, head, branch)
			return 2
		case p.Status == "failing":
			fmt.Printf("%s failing at %.10s: %s\n", branch, head, p.Title)
			for _, c := range p.Checks {
				if slices.Contains(p.Failed, c.Name) {
					fmt.Printf("  %s: %s\n", c.Name, c.URL)
				}
			}
			return 1
		case p.Status == "passing":
			fmt.Printf("%s passed at %.10s: %s\n", branch, head, p.Title)
			return 0
		case p.Status == "none":
			fmt.Printf("No checks ran for %.10s on %s\n", head, branch)
			return 3
		}
		time.Sleep(15 * time.Second)
	}
}
