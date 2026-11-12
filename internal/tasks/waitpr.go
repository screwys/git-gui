package tasks

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

// WaitPR waits, without querying GitHub, until git-gui-daemon reports the PR failing or in
// conflict, or the PR leaves the open list. An agent runs it in the background after publishing,
// so it is woken only when something needs it. A queued branch is followed until its publish
// opens the PR. Exit status: 0 merged, 1 failing, 2 conflict or a queued publish that failed,
// 3 closed or dequeued, 4 unable to wait.
func WaitPR(args []string) int {
	repo := git.RemoteSlug(".")
	if len(args) == 0 || repo == "" {
		fmt.Println("usage: git-gui wait-pr <number or queued branch>  (in the repository's checkout)")
		return 4
	}
	number, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
	if err != nil {
		if number = waitQueued(repo, args[0]); number <= 0 {
			return -number
		}
	}
	return waitPR(repo, number)
}

// waitQueued follows a queued branch's schedule and returns its PR once published, or minus the
// exit status.
func waitQueued(repo, branch string) int {
	for {
		state := client.State()
		if time.Since(time.Unix(state.Updated, 0)) > 5*time.Minute {
			fmt.Println("wait-pr: git-gui-daemon has not updated its state for 5 minutes; check systemctl --user status git-gui-daemon")
			return -4
		}
		var latest *daemon.Schedule
		for _, s := range state.Schedules {
			if s.Repo == repo && s.Branch == branch && (latest == nil || s.Updated >= latest.Updated) {
				latest = s
			}
		}
		for _, pr := range state.PRs {
			if pr.Repo == repo && pr.Branch == branch && (latest == nil || latest.Status != "waiting") {
				return pr.Number
			}
		}
		switch {
		case latest == nil:
			if n, err := strconv.Atoi(strings.TrimSpace(ghHead(repo, branch))); err == nil {
				return n
			}
			fmt.Printf("wait-pr: %s is neither queued nor an open PR\n", branch)
			return -4
		case latest.Status == "cancelled":
			fmt.Printf("%s: %s\n", branch, latest.Message)
			return -3
		case latest.Status == "started":
			record, err := daemon.ReadRecord(latest.JobPath)
			if err == nil && !daemon.JobRunning(record) && (record.ExitCode == nil || *record.ExitCode != 0) {
				fmt.Printf("The queued publish of %s failed; its output is %s\n", branch,
					filepath.Join(filepath.Dir(latest.JobPath), "output.log"))
				return -2
			}
			if err == nil && !daemon.JobRunning(record) {
				if n, err := strconv.Atoi(strings.TrimSpace(ghHead(repo, branch))); err == nil {
					return n
				}
			}
		}
		time.Sleep(15 * time.Second)
	}
}

func ghHead(repo, branch string) string {
	out, _ := exec.Command("gh", "pr", "list", "--repo", repo, "--head", branch, "--state", "open", "--json", "number", "--jq", ".[0].number // empty").Output()
	return string(out)
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
