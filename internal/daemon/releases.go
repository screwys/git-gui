package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Release is the GitHub status of the release a project's jobs view shows. The window says which
// release to follow. The daemon queries it on every pass while it is in progress (a release job
// runs or a workflow is unfinished) or the window asked, every few minutes while the window lives
// otherwise, and not at all once it finished and the window is gone. It notifies when a workflow
// concludes.
type Release struct {
	Repo    string `json:"repo"` // the checkout, real path
	Kind    string `json:"kind"` // rufin, igloo or generic
	Tag     string `json:"tag"`  // the followed tag; empty follows the newest
	Window  *Proc  `json:"window,omitempty"`
	JobPath string `json:"job_path,omitempty"` // the release job the window shows

	Shown      string       `json:"shown"` // the tag the rows describe
	Tags       []string     `json:"tags"`
	RepoURL    string       `json:"repo_url"`
	ReleaseURL string       `json:"release_url"`
	PRURL      string       `json:"pr_url"` // the open release preparation PR
	FlathubURL string       `json:"flathub_url"`
	RunID      int64        `json:"run_id"` // Rufin's release.yml run for the tag, which the window can re-run or cancel
	RunStatus  string       `json:"run_status"`
	Rows       []ReleaseRow `json:"rows"`
	Sync       string       `json:"sync"`     // such as "Published release. Updated from GitHub."
	Checking   bool         `json:"checking"` // a query was asked for and has not answered
	// Seen holds each shown run's last conclusion, so a change notifies once, across restarts.
	Seen    map[string]string `json:"seen"`
	Updated int64             `json:"updated"` // the last successful query
}

// ReleaseRow is one workflow or publication of a release.
type ReleaseRow struct {
	Name   string `json:"name"`
	Status string `json:"status"` // a run's conclusion or status, a PR state, not_found or unknown
	URL    string `json:"url"`
	Step   string `json:"step"`
}

// RufinRows are the jobs of Rufin's release.yml and its publications, IglooRows the workflows of
// Igloo's release, in table order.
var (
	RufinRows = []string{"Draft", "Android", "Flatpak", "macOS", "Windows", "RPM", "AUR", "Nix", "Publish",
		"AUR publish", "Homebrew", "Flathub PR"}
	IglooRows = []string{"Release Gate", "Container Release", "Android Release", "Windows Release"}
)

// iglooWorkflows names Igloo's rows after its release workflows.
var iglooWorkflows = map[string]string{"Android Release APK": "Android Release", "Release": "Release Gate", "Windows": "Windows Release"}

// VersionTag matches a release version such as v0.15.5 or 1.2.0-rc.1.
var VersionTag = regexp.MustCompile(`v?\d+\.\d+\.\d+(?:[-.][0-9A-Za-z.-]+)?`)

var unfinishedRun = []string{"queued", "in_progress", "waiting", "requested", "pending"}

// idleRelease is how often a finished release the window shows is queried again.
const idleRelease = 5 * time.Minute

type workflowRun struct {
	ID           int64  `json:"id"`
	WorkflowID   int64  `json:"workflow_id"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	URL          string `json:"html_url"`
	DisplayTitle string `json:"display_title"`
	HeadBranch   string `json:"head_branch"`
	CreatedAt    string `json:"created_at"`
}

const releaseQuery = `query($owner: String!, $name: String!, $generic: Boolean!) {
  repository(owner: $owner, name: $name) {
    url
    releases(first: 30, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { tagName isDraft } }
    pullRequests(states: OPEN, first: 100, orderBy: {field: CREATED_AT, direction: DESC}) @skip(if: $generic) {
      nodes { headRefName url }
    }
    refs(refPrefix: "refs/tags/", first: 100, orderBy: {field: TAG_COMMIT_DATE, direction: DESC}) @include(if: $generic) {
      nodes { name }
    }
  }
}`

const flathubQuery = `query($head: String!) {
  repository(owner: "flathub", name: "io.github.screwys.Rufin") {
    pullRequests(headRefName: $head, first: 1, orderBy: {field: CREATED_AT, direction: DESC}) {
      nodes { number title url state }
    }
  }
}`

// followRelease records which release a window's jobs view shows and asks for a query now.
func (d *daemon) followRelease(req Request) error {
	repo, err := filepath.EvalSymlinks(req.Cwd)
	if err != nil {
		return err
	}
	if !slices.Contains([]string{"rufin", "igloo", "generic"}, req.Kind) {
		return fmt.Errorf("unknown release kind %q", req.Kind)
	}
	d.mu.Lock()
	var r *Release
	for _, existing := range d.state.Releases {
		if existing.Repo == repo {
			r = existing
		}
	}
	if r == nil {
		r = &Release{Repo: repo}
		d.state.Releases = append(d.state.Releases, r)
	}
	r.Kind, r.Tag, r.JobPath, r.Checking = req.Kind, req.Tag, req.JobPath, true
	r.Window = &Proc{Pid: req.Pid, Identity: ProcessIdentity(req.Pid)}
	err = d.save()
	d.mu.Unlock()
	d.poke()
	return err
}

// pollReleases queries every release someone still waits for.
func (d *daemon) pollReleases(ctx context.Context) {
	d.mu.Lock()
	var due []Release
	for _, r := range d.state.Releases {
		if r.Checking || d.releaseActive(r) ||
			Alive(r.Window) && time.Since(time.Unix(r.Updated, 0)) >= idleRelease {
			due = append(due, *r)
		}
	}
	d.mu.Unlock()
	// Answer the window's requests first.
	slices.SortStableFunc(due, func(a, b Release) int {
		switch {
		case a.Checking && !b.Checking:
			return -1
		case b.Checking && !a.Checking:
			return 1
		}
		return 0
	})
	for _, asked := range due {
		result, err := d.queryRelease(ctx, asked)
		d.mu.Lock()
		for _, r := range d.state.Releases {
			// A follow that changed the tag meanwhile has poked the next pass.
			if r.Repo != asked.Repo || r.Tag != asked.Tag || r.Kind != asked.Kind {
				continue
			}
			r.Checking = false
			if err != nil {
				log.Printf("%s: release status: %v", r.Repo, err)
				r.Sync = "Status unavailable: " + err.Error()
				continue
			}
			d.notifyConclusions(r, result)
			r.Shown, r.Tags, r.RepoURL, r.ReleaseURL = result.Shown, result.Tags, result.RepoURL, result.ReleaseURL
			r.PRURL, r.FlathubURL, r.Rows, r.Sync = result.PRURL, result.FlathubURL, result.Rows, result.Sync
			r.Updated = time.Now().Unix()
		}
		if asked.Checking {
			// The window waits for this answer; the rest of the pass saves the others.
			if err := d.save(); err != nil {
				log.Print(err)
			}
		}
		d.mu.Unlock()
	}
}

// releaseActive reports a release in progress: its job or a scheduled release job runs, or one of
// its workflows is unfinished.
func (d *daemon) releaseActive(r *Release) bool {
	if jobAlive(r.JobPath) {
		return true
	}
	for _, s := range d.state.Schedules {
		if s.Job.Cwd == r.Repo && s.Job.Key == "release" && s.Status == "started" && jobAlive(s.JobPath) {
			return true
		}
	}
	for _, row := range r.Rows {
		if strings.Contains(row.URL, "/actions/runs/") && slices.Contains(unfinishedRun, row.Status) {
			return true
		}
	}
	return false
}

func jobAlive(path string) bool {
	if path == "" {
		return false
	}
	record, err := ReadRecord(path)
	return err == nil && JobRunning(record)
}

// notifyConclusions announces a shown run whose conclusion changed since the last query.
func (d *daemon) notifyConclusions(r *Release, result Release) {
	project := map[string]string{"rufin": "Rufin", "igloo": "Igloo"}[r.Kind]
	if project == "" {
		project = filepath.Base(r.Repo)
	}
	seen := map[string]string{}
	for _, row := range result.Rows {
		if !strings.Contains(row.URL, "/actions/runs/") {
			continue
		}
		conclusion := row.Status
		if slices.Contains(unfinishedRun, conclusion) {
			conclusion = ""
		}
		if previous, known := r.Seen[row.URL]; known && previous != conclusion && conclusion != "" {
			urgency := "critical"
			if slices.Contains([]string{"success", "skipped", "neutral"}, conclusion) {
				urgency = "normal"
			}
			go notify(urgency, fmt.Sprintf("%s %s: %s", project, result.Shown, row.Name),
				"Workflow "+strings.ReplaceAll(conclusion, "_", " "))
		}
		seen[row.URL] = conclusion
	}
	r.Seen = seen
}

// queryRelease reads a release's tags, workflow runs and publications from GitHub.
func (d *daemon) queryRelease(ctx context.Context, r Release) (Release, error) {
	slug := repoSlug(r.Repo)
	if slug == "" {
		return r, errors.New("origin is not a GitHub repository")
	}
	owner, name, _ := strings.Cut(slug, "/")
	generic := r.Kind == "generic"
	var info struct {
		Repository struct {
			URL      string
			Releases struct {
				Nodes []struct {
					TagName string
					IsDraft bool
				}
			}
			PullRequests struct {
				Nodes []struct{ HeadRefName, URL string }
			}
			Refs struct{ Nodes []struct{ Name string } }
		}
	}
	vars := map[string]any{"owner": owner, "name": name, "generic": generic}
	if err := d.graphql(ctx, releaseQuery, vars, &info); err != nil {
		return r, err
	}
	repo := info.Repository

	// Open release/<tag> branches are releases in preparation; they come first.
	prep := map[string]string{}
	var tags []string
	listed := map[string]bool{}
	add := func(tag string) {
		if !listed[tag] {
			listed[tag] = true
			tags = append(tags, tag)
		}
	}
	for _, pr := range repo.PullRequests.Nodes {
		if tag, ok := strings.CutPrefix(pr.HeadRefName, "release/"); ok {
			if _, dup := prep[tag]; !dup {
				prep[tag] = pr.URL
			}
			add(tag)
		}
	}
	for _, ref := range repo.Refs.Nodes {
		add(ref.Name)
	}
	for _, release := range repo.Releases.Nodes {
		add(release.TagName)
	}

	tag := r.Tag
	if tag == "" && len(tags) > 0 {
		tag = tags[0]
	}
	out := Release{Shown: tag, Tags: tags, RepoURL: repo.URL, PRURL: prep[tag]}

	// A tag's runs are those on the tag, and for Igloo also runs dispatched with the version in
	// their title.
	var runs []workflowRun
	workflows := map[int64]string{}
	if tag != "" {
		queries := []string{"branch=" + url.QueryEscape(tag) + "&per_page=100"}
		if r.Kind == "igloo" {
			queries = append(queries, "event=workflow_dispatch&per_page=30")
		}
		for _, query := range queries {
			var page struct {
				WorkflowRuns []workflowRun `json:"workflow_runs"`
			}
			if err := d.getJSON(ctx, "repos/"+slug+"/actions/runs?"+query, &page); err != nil {
				return r, err
			}
			runs = append(runs, page.WorkflowRuns...)
		}
		slices.SortStableFunc(runs, func(a, b workflowRun) int { return strings.Compare(b.CreatedAt, a.CreatedAt) })
		// Runs carry their own run name; rows are named after the workflow.
		var list struct {
			Workflows []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"workflows"`
		}
		if err := d.getJSON(ctx, "repos/"+slug+"/actions/workflows?per_page=100", &list); err != nil {
			return r, err
		}
		for _, w := range list.Workflows {
			workflows[w.ID] = w.Name
		}
	}

	if r.Kind == "rufin" {
		out.Rows = d.rufinRows(ctx, slug, tag, runs, workflows, &out)
	} else {
		d.workflowRows(r, tag, generic, runs, workflows, &out)
	}

	state := "No releases found"
	published := false
	for _, release := range repo.Releases.Nodes {
		if release.TagName != tag || tag == "" {
			continue
		}
		published = true
		out.ReleaseURL = repo.URL + "/releases/tag/" + tag
		state = "Published release"
		if release.IsDraft {
			state = "Draft release"
		}
	}
	if !published && tag != "" {
		state = "Release preparation"
		if generic {
			state = "Waiting for release"
		}
	}
	out.Sync = state
	if !generic {
		out.Sync += ". Updated from GitHub."
	}
	return out, nil
}

func (d *daemon) getJSON(ctx context.Context, path string, out any) error {
	data, err := d.request(ctx, http.MethodGet, "https://api.github.com/"+path, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// flathubRow is the state of Rufin's Flathub PR for the release.
func (d *daemon) flathubRow(ctx context.Context, tag string, out *Release) ReleaseRow {
	row := ReleaseRow{Name: "Flathub PR", Status: "not_found", Step: "No release tag selected"}
	if tag == "" {
		return row
	}
	var result struct {
		Repository struct {
			PullRequests struct {
				Nodes []struct {
					Number            int
					Title, URL, State string
				}
			}
		}
	}
	if err := d.graphql(ctx, flathubQuery, map[string]any{"head": "release/" + tag}, &result); err != nil {
		return ReleaseRow{Name: row.Name, Status: "unknown", Step: "Status unavailable: " + err.Error()}
	}
	prs := result.Repository.PullRequests.Nodes
	if len(prs) == 0 {
		row.Step = "No PR found for release/" + tag
		return row
	}
	out.FlathubURL = prs[0].URL
	return ReleaseRow{Name: row.Name, Status: strings.ToLower(prs[0].State), URL: prs[0].URL,
		Step: fmt.Sprintf("PR #%d: %s", prs[0].Number, prs[0].Title)}
}

// workflowRows are a generic or Igloo release's rows: the newest run of each workflow for the tag.
func (d *daemon) workflowRows(r Release, tag string, generic bool, runs []workflowRun, workflows map[int64]string, out *Release) {
	matched := map[string]ReleaseRow{}
	var order []string
	for _, run := range runs {
		if run.HeadBranch != tag && (generic || !slices.Contains(VersionTag.FindAllString(run.DisplayTitle, -1), tag)) {
			continue
		}
		row := workflows[run.WorkflowID]
		if renamed, ok := iglooWorkflows[row]; ok && r.Kind == "igloo" {
			row = renamed
		}
		if _, ok := matched[row]; ok {
			continue
		}
		status := run.Conclusion
		if status == "" {
			status = run.Status
		}
		matched[row] = ReleaseRow{Name: row, Status: status, URL: run.URL, Step: status}
		order = append(order, row)
	}
	names := order
	if r.Kind == "igloo" {
		names = IglooRows
	}
	for _, row := range names {
		if found, ok := matched[row]; ok {
			out.Rows = append(out.Rows, found)
		} else {
			out.Rows = append(out.Rows, ReleaseRow{Name: row, Status: "unknown", Step: "No matching recent run"})
		}
	}
}

// rufinRows are the jobs of the tag's release.yml run, grouped by the workflow they call, then
// the Homebrew run that followed it and the Flathub PR.
func (d *daemon) rufinRows(ctx context.Context, slug, tag string, runs []workflowRun, workflows map[int64]string, out *Release) []ReleaseRow {
	rows := map[string]*ReleaseRow{}
	var release *workflowRun
	for i, run := range runs {
		if run.HeadBranch == tag && workflows[run.WorkflowID] == "Release" {
			release = &runs[i]
			break
		}
	}
	if release != nil {
		out.RunID, out.RunStatus = release.ID, release.Status
		if release.Status == "completed" {
			out.RunStatus = release.Conclusion
		}
		var page struct {
			Jobs []struct {
				Name, Status, Conclusion string
				URL                      string `json:"html_url"`
				Steps                    []struct{ Name, Status, Conclusion string }
			}
		}
		if err := d.getJSON(ctx, fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", slug, release.ID), &page); err != nil {
			log.Printf("%s: release jobs: %v", slug, err)
		}
		for _, job := range page.Jobs {
			group, _, _ := strings.Cut(job.Name, " / ")
			row := rows[group]
			if row == nil {
				row = &ReleaseRow{Name: group, Status: "success", URL: job.URL, Step: "Completed"}
				rows[group] = row
			}
			switch {
			case job.Status != "completed":
				if row.Status != "failure" {
					row.Status, row.URL, row.Step = job.Status, job.URL, job.Name
					for _, step := range job.Steps {
						if step.Status == "in_progress" {
							row.Step = step.Name
						}
					}
				}
			case !slices.Contains([]string{"success", "skipped", "neutral"}, job.Conclusion):
				row.Status, row.URL, row.Step = "failure", job.URL, "Failed: "+job.Name
				for _, step := range job.Steps {
					if step.Conclusion == "failure" {
						row.Step = "Failed: " + step.Name
						break
					}
				}
			}
		}
	}

	homebrew := ReleaseRow{Name: "Homebrew", Status: "not_found", Step: "Runs after the release publishes"}
	if release != nil {
		var page struct {
			WorkflowRuns []workflowRun `json:"workflow_runs"`
		}
		if err := d.getJSON(ctx, "repos/screwys/homebrew-tap/actions/runs?event=workflow_dispatch&per_page=5", &page); err == nil {
			for _, run := range page.WorkflowRuns {
				if run.CreatedAt >= release.CreatedAt {
					status := run.Conclusion
					if status == "" {
						status = run.Status
					}
					homebrew = ReleaseRow{Name: "Homebrew", Status: status, URL: run.URL, Step: status}
					break
				}
			}
		}
	}
	rows["Homebrew"] = &homebrew
	flathub := d.flathubRow(ctx, tag, out)
	rows["Flathub PR"] = &flathub

	var shown []ReleaseRow
	for _, name := range RufinRows {
		if row := rows[name]; row != nil {
			shown = append(shown, *row)
		} else {
			shown = append(shown, ReleaseRow{Name: name, Status: "unknown", Step: "No job yet"})
		}
	}
	return shown
}
