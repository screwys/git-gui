package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Push is a commit pushed straight to a branch, followed on request (git-gui wait-push) until its
// checks finish. A newer push to the same branch replaces it.
type Push struct {
	Repo   string   `json:"repo"` // owner/name
	Branch string   `json:"branch"`
	Head   string   `json:"head"`
	Title  string   `json:"title"` // the commit's subject
	URL    string   `json:"url"`
	Status string   `json:"status"` // waiting, running, failing, passing or none
	Failed []string `json:"failed"`
	Checks []Check  `json:"checks"`
	Since  int64    `json:"since"` // when following began
}

const pushQuery = `p%d: repository(owner: %q, name: %q) { object(oid: %q) { ... on Commit { messageHeadline url ...checks } } }`

const (
	noChecksAfter = 3 * time.Minute // a commit without checks by then has none
	keepPush      = 24 * time.Hour  // how long wait-push can read a finished push
)

// done reports that every check finished, or none came.
func (p *Push) done() bool {
	running := slices.ContainsFunc(p.Checks, func(c Check) bool { return c.Status != "COMPLETED" })
	return p.Status == "passing" || p.Status == "none" || p.Status == "failing" && !running
}

// followPush starts following the pushed commit req.Head on req.Branch of the checkout req.Cwd.
// The entry is saved at once, so wait-push finds it before the next pass.
func (d *daemon) followPush(req Request) error {
	repo := repoSlug(req.Cwd)
	switch {
	case repo == "":
		return fmt.Errorf("%s has no GitHub origin", req.Cwd)
	case req.Branch == "" || req.Head == "":
		return fmt.Errorf("follow_push needs a branch and a commit")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	pushes := []*Push{}
	for _, p := range d.state.Pushes {
		if p.Repo == repo && p.Branch == req.Branch && p.Head == req.Head {
			return nil
		}
		if !(p.Repo == repo && p.Branch == req.Branch) && time.Since(time.Unix(p.Since, 0)) < keepPush {
			pushes = append(pushes, p)
		}
	}
	d.state.Pushes = append(pushes, &Push{Repo: repo, Branch: req.Branch, Head: req.Head, Status: "waiting",
		Failed: []string{}, Checks: []Check{}, Since: time.Now().Unix()})
	d.poke()
	return d.save()
}

// followingPushes reports a followed push whose checks have not finished.
func (d *daemon) followingPushes() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.ContainsFunc(d.state.Pushes, func(p *Push) bool { return !p.done() })
}

// watchPushes reads the checks of every followed push still running and notifies when they fail
// or all pass.
func (d *daemon) watchPushes(ctx context.Context) error {
	d.mu.Lock()
	var follow []Push
	for _, p := range d.state.Pushes {
		if !p.done() {
			follow = append(follow, *p)
		}
	}
	d.mu.Unlock()
	if len(follow) == 0 {
		return nil
	}
	query := ""
	for i, p := range follow {
		owner, name, _ := strings.Cut(p.Repo, "/")
		query += fmt.Sprintf(pushQuery, i, owner, name, p.Head) + "\n"
	}
	var result map[string]json.RawMessage
	if err := d.graphql(ctx, "query {\n"+query+"}\n"+checksFragment, nil, &result); err != nil {
		return err
	}
	var notices [][3]string
	d.mu.Lock()
	for i, followed := range follow {
		var node struct {
			Object *struct {
				MessageHeadline   string
				URL               string
				StatusCheckRollup *checkRollup
			}
		}
		if json.Unmarshal(result[fmt.Sprintf("p%d", i)], &node) != nil || node.Object == nil {
			continue
		}
		index := slices.IndexFunc(d.state.Pushes, func(p *Push) bool {
			return p.Repo == followed.Repo && p.Branch == followed.Branch && p.Head == followed.Head
		})
		if index < 0 {
			continue // a newer push replaced it meanwhile
		}
		p := d.state.Pushes[index]
		was := p.Status
		var running bool
		p.Title, p.URL = node.Object.MessageHeadline, node.Object.URL
		p.Checks, p.Failed, running = node.Object.StatusCheckRollup.read()
		switch {
		case len(p.Failed) > 0:
			p.Status = "failing"
		case running:
			p.Status = "running"
		case len(p.Checks) > 0:
			p.Status = "passing"
		case time.Since(time.Unix(p.Since, 0)) > noChecksAfter:
			p.Status = "none"
		}
		subject := fmt.Sprintf("%s %s %.7s", p.Repo, p.Branch, p.Head)
		switch {
		case p.Status == "failing" && was != "failing":
			notices = append(notices, [3]string{"critical", subject + ": checks failed", strings.Join(p.Failed, ", ") + "\n" + p.Title})
		case p.Status == "passing":
			notices = append(notices, [3]string{"normal", subject + ": checks passed", p.Title})
		}
	}
	d.mu.Unlock()
	for _, notice := range notices {
		notify(notice[0], notice[1], notice[2])
	}
	return nil
}
