package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

const searchQuery = `query {
  search(query: "is:pr is:open author:@me archived:false", type: ISSUE, first: 50) {
    nodes {
      ... on PullRequest {
        number title url isDraft headRefName headRefOid mergeStateStatus
        autoMergeRequest { enabledAt }
        repository { nameWithOwner }
        commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
          __typename
          ... on CheckRun { name status conclusion detailsUrl startedAt completedAt }
          ... on StatusContext { context state targetUrl startedAt: createdAt }
        } } } } } }
      }
    }
  }
}`

const stateQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) { state } }
}`

var failedOutcomes = []string{"FAILURE", "ERROR", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED"}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(\.git)?$`)

// PR is one open pull request as written to the state file.
type PR struct {
	Repo   string   `json:"repo"`
	Number int      `json:"number"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Branch string   `json:"branch"`
	Head   string   `json:"head"`   // the head commit the status describes
	Status string   `json:"status"` // running, passing, behind, updating, failing or conflict
	Failed []string `json:"failed"`
	Checks []Check  `json:"checks"` // the head commit's checks, for git-gui's checks view
}

// Check is one check run or commit status of a PR's head commit.
type Check struct {
	Name string `json:"name"`
	// Status is a check run's (QUEUED, IN_PROGRESS, COMPLETED, ...); a commit status is
	// COMPLETED once it succeeded or failed and PENDING before.
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"` // SUCCESS, FAILURE, SKIPPED, ...; a commit status's state
	URL        string `json:"url,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`  // RFC 3339
	FinishedAt string `json:"finished_at,omitempty"` // RFC 3339
}

// CheckNode is a check as GitHub lists it, in GraphQL and in `gh pr view --json statusCheckRollup`.
type CheckNode struct {
	Typename    string `json:"__typename"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	DetailsURL  string `json:"detailsUrl"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
	Context     string `json:"context"`
	State       string `json:"state"`
	TargetURL   string `json:"targetUrl"`
}

// Check gives check runs and commit statuses one shape.
func (n CheckNode) Check() Check {
	if n.Typename != "StatusContext" {
		return Check{n.Name, n.Status, n.Conclusion, n.DetailsURL, n.StartedAt, n.CompletedAt}
	}
	c := Check{Name: n.Context, Status: "PENDING", URL: n.TargetURL, StartedAt: n.StartedAt}
	if n.State != "PENDING" && n.State != "EXPECTED" {
		c.Status, c.Conclusion = "COMPLETED", n.State
	}
	return c
}

func (p PR) key() string { return fmt.Sprintf("%s#%d", p.Repo, p.Number) }

type searchResult struct {
	Search struct {
		Nodes []struct {
			Number           int
			Title            string
			URL              string
			IsDraft          bool
			HeadRefName      string
			HeadRefOid       string
			MergeStateStatus string
			AutoMergeRequest *struct{}
			Repository       struct{ NameWithOwner string }
			Commits          struct {
				Nodes []struct {
					Commit struct {
						StatusCheckRollup *struct {
							Contexts struct{ Nodes []CheckNode }
						}
					}
				}
			}
		}
	}
}

// repoSlug returns "owner/name" for a checkout's GitHub origin.
func repoSlug(checkout string) string {
	out, err := exec.Command("git", "-C", checkout, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	match := githubRemote.FindStringSubmatch(strings.TrimSpace(string(out)))
	if match == nil {
		return ""
	}
	return match[1]
}

// loadCheckouts reads git-gui's configuration for the checkout that publishes through a helper.
func (d *daemon) loadCheckouts() {
	d.checkouts, d.helper = map[string]string{}, map[string]string{}
	var config struct {
		RufinRepoPath   string `json:"rufin_repo_path"`
		PublishPRScript string `json:"publish_pr_script"`
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "git-gui", "config.json"))
	if err != nil || json.Unmarshal(data, &config) != nil || config.RufinRepoPath == "" {
		return
	}
	helper := config.PublishPRScript
	if helper == "" {
		helper = ".local/scripts/publish-pr.sh"
	}
	if !filepath.IsAbs(helper) {
		helper = filepath.Join(config.RufinRepoPath, helper)
	}
	if slug := repoSlug(config.RufinRepoPath); slug != "" {
		d.checkouts[slug] = config.RufinRepoPath
		d.helper[slug] = helper
	}
}

func (d *daemon) watchPRs(ctx context.Context) error {
	var result searchResult
	if err := d.graphql(ctx, searchQuery, nil, &result); err != nil {
		return err
	}
	d.mu.Lock()
	previous := append(append([]PR{}, d.state.PRs...), d.state.Leaving...)
	d.mu.Unlock()

	before := map[string]string{}
	for _, pr := range previous {
		before[pr.key()] = pr.Status
	}
	open := map[string]bool{}
	prs := []PR{}
	var notices [][3]string
	for _, node := range result.Search.Nodes {
		if node.Number == 0 {
			continue
		}
		pr := PR{Repo: node.Repository.NameWithOwner, Number: node.Number, Title: node.Title, URL: node.URL, Branch: node.HeadRefName, Head: node.HeadRefOid,
			Failed: []string{}, Checks: []Check{}}
		open[pr.key()] = true
		running := false
		for _, commit := range node.Commits.Nodes {
			if commit.Commit.StatusCheckRollup == nil {
				continue
			}
			for _, raw := range commit.Commit.StatusCheckRollup.Contexts.Nodes {
				check := raw.Check()
				pr.Checks = append(pr.Checks, check)
				switch {
				case check.Status != "COMPLETED":
					running = true
				case slices.Contains(failedOutcomes, check.Conclusion) && !slices.Contains(pr.Failed, check.Name):
					pr.Failed = append(pr.Failed, check.Name)
				}
			}
		}
		switch {
		case node.MergeStateStatus == "DIRTY":
			pr.Status = "conflict"
		case len(pr.Failed) > 0:
			pr.Status = "failing"
		case running:
			pr.Status = "running"
		case node.MergeStateStatus == "BEHIND":
			pr.Status = "behind"
		default:
			pr.Status = "passing"
		}
		if pr.Status == "behind" && node.AutoMergeRequest != nil && !node.IsDraft {
			if err := d.updateBranch(ctx, pr, node.HeadRefOid); err != nil {
				log.Printf("%s: update branch: %v", pr.key(), err)
			} else {
				pr.Status = "updating"
				log.Printf("%s: updated its branch from its base", pr.key())
			}
		}
		if pr.Status == "failing" || pr.Status == "conflict" {
			key := fmt.Sprintf("%s:%s:%s", pr.key(), node.HeadRefOid, pr.Status)
			detail := strings.Join(pr.Failed, ", ")
			if pr.Status == "conflict" {
				detail = "Merge conflict with its base branch"
			}
			d.mu.Lock()
			fresh := !d.notified[key]
			d.notified[key] = true
			d.mu.Unlock()
			if fresh {
				notices = append(notices, [3]string{"critical", fmt.Sprintf("#%d %s: %s", pr.Number, pr.Status, pr.Title), detail})
			}
		}
		// Checks that were running or failing have all passed.
		if was := before[pr.key()]; (was == "running" || was == "failing") && slices.Contains([]string{"passing", "behind", "updating"}, pr.Status) {
			notices = append(notices, [3]string{"normal", fmt.Sprintf("#%d: checks passed", pr.Number), pr.Title})
		}
		prs = append(prs, pr)
	}
	for _, notice := range notices {
		notify(notice[0], notice[1], notice[2])
	}

	leaving := []PR{}
	merged := map[string]bool{}
	for _, gone := range previous {
		if open[gone.key()] {
			continue
		}
		state, err := d.prState(ctx, gone.Repo, gone.Number)
		switch {
		case err != nil:
			log.Printf("%s: read state: %v", gone.key(), err)
			leaving = append(leaving, gone)
		case state == "MERGED":
			if !d.scheduledAfter(gone.Repo, gone.Number) {
				notify("normal", fmt.Sprintf("#%d merged", gone.Number), gone.Title)
			}
			if d.checkouts[gone.Repo] != "" {
				merged[gone.Repo] = true
			}
		case state != "CLOSED":
			leaving = append(leaving, gone)
		}
	}

	d.mu.Lock()
	for _, repo := range d.state.SyncPending {
		merged[repo] = true
	}
	for key := range d.notified {
		if prKey, _, _ := strings.Cut(key, ":"); !open[prKey] {
			delete(d.notified, key)
		}
	}
	d.state.PRs, d.state.Leaving = prs, leaving
	d.state.Notified = sortedKeys(d.notified)
	d.mu.Unlock()

	pending := []string{}
	for repo := range merged {
		if !d.sync(repo) {
			pending = append(pending, repo)
		}
	}
	d.mu.Lock()
	d.state.SyncPending = pending
	d.mu.Unlock()
	return nil
}

// scheduledAfter reports a schedule waiting on the PR; its own notice announces the merge.
func (d *daemon) scheduledAfter(repo string, number int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.state.Schedules {
		if !s.done() && s.Repo == repo && s.Number == number {
			return true
		}
	}
	return false
}

func (d *daemon) prState(ctx context.Context, repo string, number int) (string, error) {
	owner, name, _ := strings.Cut(repo, "/")
	var result struct {
		Repository struct{ PullRequest struct{ State string } }
	}
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	err := d.graphql(ctx, stateQuery, vars, &result)
	return result.Repository.PullRequest.State, err
}

// sync brings the checkout up to its remote base once the helper is free.
func (d *daemon) sync(repo string) bool {
	checkout := d.checkouts[repo]
	lock, err := os.OpenFile(filepath.Join(checkout, ".local", "publish-checkout.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	busy := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
	lock.Close()
	if busy {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.helper[repo], "--sync")
	cmd.Dir = checkout
	out, err := cmd.CombinedOutput()
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		delete(d.syncError, repo)
		log.Printf("%s: synced %s", repo, checkout)
		return true
	}
	message := strings.TrimSpace(string(out))
	if len(message) > 300 {
		message = message[len(message)-300:]
	}
	if d.syncError[repo] != message {
		d.syncError[repo] = message
		go notify("critical", repo+" not synced", message)
	}
	return false
}

func (d *daemon) updateBranch(ctx context.Context, pr PR, head string) error {
	body, _ := json.Marshal(map[string]string{"expected_head_sha": head})
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d/update-branch", pr.Repo, pr.Number)
	_, err := d.request(ctx, http.MethodPut, url, body)
	return err
}

func (d *daemon) graphql(ctx context.Context, query string, variables map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	data, err := d.request(ctx, http.MethodPost, "https://api.github.com/graphql", body)
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		return errors.New(envelope.Errors[0].Message)
	}
	return json.Unmarshal(envelope.Data, out)
}

// request sends an authenticated GitHub API request, fetching the gh token again after a 401.
func (d *daemon) request(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		if d.token == "" {
			out, err := exec.Command("gh", "auth", "token").Output()
			if err != nil {
				return nil, fmt.Errorf("gh auth token: %w", err)
			}
			d.token = strings.TrimSpace(string(out))
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+d.token)
		req.Header.Set("Accept", "application/vnd.github.merge-info-preview+json")
		resp, err := d.client.Do(req)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			d.token = ""
			continue
		}
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("%s %s: %s %s", method, url, resp.Status, bytes.TrimSpace(data))
		}
		return data, nil
	}
}

// notify sends a desktop notification; urgency is low, normal or critical.
func notify(urgency, summary, body string) {
	if err := exec.Command("notify-send", "--app-name=Git GUI", "--urgency="+urgency, summary, body).Run(); err != nil {
		log.Printf("notify-send: %v", err)
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
