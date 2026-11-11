package tasks

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/git"
)

// WaitPR waits, without querying GitHub, until git-gui-daemon reports the PR failing or in
// conflict, or the PR leaves the open list. An agent runs it in the background after publishing,
// so it is woken only when something needs it. Exit status: 0 merged, 1 failing, 2 conflict,
// 3 closed, 4 unable to wait.
func WaitPR(args []string) int {
	if len(args) == 0 {
		fmt.Println("usage: git-gui wait-pr <number>  (in the repository's checkout)")
		return 4
	}
	number, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
	repo := git.RemoteSlug(".")
	if err != nil || repo == "" {
		fmt.Println("wait-pr: needs a PR number and a GitHub checkout")
		return 4
	}
	return waitPR(repo, number)
}

func waitPR(repo string, number int) int {
	// Statuses of earlier heads are stale: wait for the one this push made, or one the daemon's
	// branch update made after it.
	head := strings.TrimSpace(gh(repo, number, ".headRefOid"))
	for {
		state := client.State()
		if time.Since(time.Unix(state.Updated, 0)) > 5*time.Minute {
			fmt.Println("wait-pr: git-gui-daemon has not updated its state for 5 minutes; check systemctl --user status git-gui-daemon")
			return 4
		}
		listed := false
		for _, pr := range append(state.PRs, state.Leaving...) {
			if pr.Repo != repo || pr.Number != number {
				continue
			}
			listed = true
			if head != "" && pr.Head != head && (pr.Status == "failing" || pr.Status == "conflict") {
				break
			}
			head = pr.Head
			switch pr.Status {
			case "failing":
				fmt.Printf("#%d failing at %.10s: %s\n%s\n", number, head, strings.Join(pr.Failed, ", "), pr.URL)
				return 1
			case "conflict":
				fmt.Printf("#%d has a merge conflict with its base branch\n%s\n", number, pr.URL)
				return 2
			}
		}
		if !listed {
			switch strings.TrimSpace(gh(repo, number, ".state")) {
			case "MERGED":
				fmt.Printf("#%d merged\n", number)
				return 0
			case "CLOSED":
				fmt.Printf("#%d closed without merging\n", number)
				return 3
			}
		}
		time.Sleep(15 * time.Second)
	}
}

func gh(repo string, number int, field string) string {
	out, _ := exec.Command("gh", "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", "state,headRefOid", "--jq", field).Output()
	return string(out)
}
