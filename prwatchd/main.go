// Command prwatchd watches the user's open GitHub pull requests.
//
// Every 30 seconds it reads them with one GraphQL search. A PR with auto-merge on, passing
// checks and a branch behind its base is updated on GitHub: rulesets that require
// up-to-date branches otherwise leave auto-merge waiting. Failed checks and merge conflicts
// raise one desktop notification per head commit. After a PR merges in a repository whose
// local checkout has a publish helper, that checkout is synced with the helper.
//
// The result is written to $XDG_STATE_HOME/git-gui/pr-watch.json, which git-gui reads.
package main

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

const interval = 30 * time.Second

const searchQuery = `query {
  search(query: "is:pr is:open author:@me archived:false", type: ISSUE, first: 50) {
    nodes {
      ... on PullRequest {
        number title url isDraft headRefName headRefOid mergeStateStatus
        autoMergeRequest { enabledAt }
        repository { nameWithOwner }
        commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
          __typename
          ... on CheckRun { name status conclusion }
          ... on StatusContext { context state }
        } } } } } }
      }
    }
  }
}`

const stateQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) { state } }
}`

var failedOutcomes = []string{"FAILURE", "ERROR", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED"}

// PR is one open pull request as written to the state file.
type PR struct {
	Repo   string   `json:"repo"`
	Number int      `json:"number"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Branch string   `json:"branch"`
	Status string   `json:"status"` // running, passing, behind, updating, failing or conflict
	Failed []string `json:"failed"`
}

// State is the file other programs read.
type State struct {
	Updated     int64    `json:"updated"`
	PRs         []PR     `json:"prs"`
	SyncPending []string `json:"sync_pending"`
	// Leaving holds PRs gone from the open search until GitHub reports them merged or closed.
	Leaving  []PR     `json:"leaving"`
	Notified []string `json:"notified"`
}

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
							Contexts struct {
								Nodes []struct {
									Typename   string `json:"__typename"`
									Name       string
									Status     string
									Conclusion string
									Context    string
									State      string
								}
							}
						}
					}
				}
			}
		}
	}
}

type watcher struct {
	client    *http.Client
	token     string
	statePath string
	checkouts map[string]string // "owner/name" -> local checkout with a publish helper
	helper    map[string]string // "owner/name" -> publish helper path
	state     State
	notified  map[string]bool
	syncError map[string]string
}

func main() {
	log.SetFlags(0)
	w := &watcher{client: &http.Client{Timeout: 30 * time.Second}, notified: map[string]bool{}, syncError: map[string]string{}}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	w.statePath = filepath.Join(stateHome, "git-gui", "pr-watch.json")
	if data, err := os.ReadFile(w.statePath); err == nil {
		_ = json.Unmarshal(data, &w.state)
	}
	for _, key := range w.state.Notified {
		w.notified[key] = true
	}
	w.loadCheckouts()
	for {
		if err := w.pass(context.Background()); err != nil {
			log.Print(err)
		}
		time.Sleep(interval)
	}
}

// loadCheckouts reads git-gui's configuration for the checkout that publishes through a helper.
func (w *watcher) loadCheckouts() {
	w.checkouts, w.helper = map[string]string{}, map[string]string{}
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
	out, err := exec.Command("git", "-C", config.RufinRepoPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return
	}
	match := regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(\.git)?$`).FindStringSubmatch(strings.TrimSpace(string(out)))
	if match == nil {
		return
	}
	w.checkouts[match[1]] = config.RufinRepoPath
	w.helper[match[1]] = helper
}

func (w *watcher) pass(ctx context.Context) error {
	var result searchResult
	if err := w.graphql(ctx, searchQuery, nil, &result); err != nil {
		return err
	}
	open := map[string]bool{}
	prs := []PR{}
	for _, node := range result.Search.Nodes {
		if node.Number == 0 {
			continue
		}
		pr := PR{Repo: node.Repository.NameWithOwner, Number: node.Number, Title: node.Title, URL: node.URL, Branch: node.HeadRefName, Failed: []string{}}
		open[pr.key()] = true
		running := false
		for _, commit := range node.Commits.Nodes {
			if commit.Commit.StatusCheckRollup == nil {
				continue
			}
			for _, check := range commit.Commit.StatusCheckRollup.Contexts.Nodes {
				name, outcome := check.Name, check.Conclusion
				if check.Typename == "StatusContext" {
					name, outcome = check.Context, check.State
				}
				switch {
				case check.Typename == "CheckRun" && check.Status != "COMPLETED", outcome == "PENDING", outcome == "EXPECTED":
					running = true
				case slices.Contains(failedOutcomes, outcome) && !slices.Contains(pr.Failed, name):
					pr.Failed = append(pr.Failed, name)
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
			if err := w.updateBranch(ctx, pr, node.HeadRefOid); err != nil {
				log.Printf("%s: update branch: %v", pr.key(), err)
			} else {
				pr.Status = "updating"
				log.Printf("%s: updated its branch from its base", pr.key())
			}
		}
		if pr.Status == "failing" || pr.Status == "conflict" {
			key := fmt.Sprintf("%s:%s:%s", pr.key(), node.HeadRefOid, pr.Status)
			if !w.notified[key] {
				w.notified[key] = true
				detail := strings.Join(pr.Failed, ", ")
				if pr.Status == "conflict" {
					detail = "Merge conflict with its base branch"
				}
				notify(fmt.Sprintf("#%d %s: %s", pr.Number, pr.Status, pr.Title), detail)
			}
		}
		prs = append(prs, pr)
	}

	pending := map[string]bool{}
	for _, repo := range w.state.SyncPending {
		pending[repo] = true
	}
	leaving := []PR{}
	for _, previous := range append(w.state.PRs, w.state.Leaving...) {
		if open[previous.key()] || w.checkouts[previous.Repo] == "" {
			continue
		}
		owner, name, _ := strings.Cut(previous.Repo, "/")
		var closed struct {
			Repository struct{ PullRequest struct{ State string } }
		}
		vars := map[string]any{"owner": owner, "name": name, "number": previous.Number}
		err := w.graphql(ctx, stateQuery, vars, &closed)
		switch state := closed.Repository.PullRequest.State; {
		case err != nil:
			log.Printf("%s: read state: %v", previous.key(), err)
			leaving = append(leaving, previous)
		case state == "MERGED":
			pending[previous.Repo] = true
		case state != "CLOSED":
			leaving = append(leaving, previous)
		}
	}
	stillPending := []string{}
	for repo := range pending {
		if !w.sync(repo) {
			stillPending = append(stillPending, repo)
		}
	}

	for key := range w.notified {
		if prKey, _, _ := strings.Cut(key, ":"); !open[prKey] {
			delete(w.notified, key)
		}
	}
	w.state = State{Updated: time.Now().Unix(), PRs: prs, SyncPending: stillPending, Leaving: leaving, Notified: sortedKeys(w.notified)}
	return w.save()
}

func (p PR) key() string { return fmt.Sprintf("%s#%d", p.Repo, p.Number) }

// sync brings the checkout up to its remote base once the helper is free.
func (w *watcher) sync(repo string) bool {
	checkout := w.checkouts[repo]
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
	cmd := exec.CommandContext(ctx, w.helper[repo], "--sync")
	cmd.Dir = checkout
	out, err := cmd.CombinedOutput()
	if err == nil {
		delete(w.syncError, repo)
		log.Printf("%s: synced %s", repo, checkout)
		return true
	}
	message := strings.TrimSpace(string(out))
	if len(message) > 300 {
		message = message[len(message)-300:]
	}
	if w.syncError[repo] != message {
		w.syncError[repo] = message
		notify(repo+" not synced", message)
	}
	return false
}

func (w *watcher) updateBranch(ctx context.Context, pr PR, head string) error {
	body, _ := json.Marshal(map[string]string{"expected_head_sha": head})
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d/update-branch", pr.Repo, pr.Number)
	_, err := w.request(ctx, http.MethodPut, url, body)
	return err
}

func (w *watcher) graphql(ctx context.Context, query string, variables map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	data, err := w.request(ctx, http.MethodPost, "https://api.github.com/graphql", body)
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
func (w *watcher) request(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		if w.token == "" {
			out, err := exec.Command("gh", "auth", "token").Output()
			if err != nil {
				return nil, fmt.Errorf("gh auth token: %w", err)
			}
			w.token = strings.TrimSpace(string(out))
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+w.token)
		req.Header.Set("Accept", "application/vnd.github.merge-info-preview+json")
		resp, err := w.client.Do(req)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			w.token = ""
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

func (w *watcher) save() error {
	data, err := json.MarshalIndent(w.state, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(w.statePath), 0o700); err != nil {
		return err
	}
	tmp := w.statePath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.statePath)
}

func notify(summary, body string) {
	if err := exec.Command("notify-send", "--app-name=Git GUI", summary, body).Run(); err != nil {
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
