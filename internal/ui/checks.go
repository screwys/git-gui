package ui

import (
	"bytes"
	stdcontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

var failedConclusions = []string{"FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE"}

// trackedPR is state.json's rufin_tracked_pr; the Python version saved the number as text.
type trackedPR struct {
	Number string `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

// ChecksView shows one pull request's checks. The daemon watches the user's open PRs and keeps
// their checks in daemon.json, which the view reads whenever it changes. A PR the daemon does not
// list (someone else's, or merged or closed) is fetched once with gh, never polled.
type ChecksView struct {
	Root     *gtk.Box
	repo     string
	slug     string
	title    *gtk.Label
	pill     *gtk.Label
	open     *gtk.Button
	progress *gtk.ProgressBar
	grid     *gtk.Grid

	Number            int
	Title, URL, State string
	saved             trackedPR
	rows              int
	watched           bool // the daemon listed the PR at the last read
	fetches           int  // answers for an earlier PR or request are dropped
	elapsed           map[*gtk.Label]time.Time
	ticker            glib.SourceHandle

	OnChecks func(number int, checks []daemon.Check) // every result, for the output tab choice
}

// NewChecksView builds the view for repo's pull requests.
func NewChecksView(repo string) *ChecksView {
	v := &ChecksView{Root: gtk.NewBox(gtk.OrientationVertical, 6), repo: repo, slug: git.RemoteSlug(repo)}
	v.Root.SetMarginStart(6)
	v.Root.SetMarginEnd(6)
	v.Root.SetMarginTop(6)
	v.Root.SetMarginBottom(6)
	header := gtk.NewBox(gtk.OrientationHorizontal, 8)
	v.title = gtk.NewLabel("No active PR detected")
	v.title.AddCSSClass("heading")
	v.title.SetXAlign(0)
	v.title.SetHExpand(true)
	v.title.SetEllipsize(3)
	v.pill = newPill()
	refresh := gtk.NewButtonWithLabel("Refresh")
	refresh.ConnectClicked(v.Refresh)
	v.open = gtk.NewButtonWithLabel("Open PR")
	v.open.SetSensitive(false)
	v.open.ConnectClicked(func() { openURL(v.URL) })
	header.Append(v.title)
	header.Append(v.pill)
	header.Append(refresh)
	header.Append(v.open)
	v.Root.Append(header)
	v.progress = gtk.NewProgressBar()
	v.Root.Append(v.progress)

	v.grid = gtk.NewGrid()
	v.grid.SetColumnSpacing(12)
	v.grid.SetRowSpacing(4)
	for i, name := range []string{"Check / Workflow", "Status", "Duration", "Link"} {
		label := gtk.NewLabel(name)
		label.AddCSSClass("dim")
		label.SetXAlign(0)
		v.grid.Attach(label, i, 0, 1, 1)
	}
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(v.grid)
	scroll.SetVExpand(true)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	v.Root.Append(scroll)

	onDaemonState(func(daemon.State) { v.update(false) })
	return v
}

// SetPR shows a pull request; state is OPEN, MERGED, CLOSED or "" when unknown. A PR the daemon
// does not list is fetched when it becomes the shown one.
func (v *ChecksView) SetPR(number int, title, url, state string) {
	changed := number != v.Number
	if changed {
		v.watched, v.State = false, ""
		v.showChecks(nil)
		setPill(v.pill, "Idle", "idle")
	}
	v.Number, v.Title, v.URL = number, title, url
	v.setState(state)
	v.showHeader()
	v.update(changed)
}

// Clear shows that the branch has no pull request.
func (v *ChecksView) Clear() {
	v.fetches++
	v.Number, v.Title, v.URL, v.State, v.watched, v.saved = 0, "", "", "", false, trackedPR{}
	v.showChecks(nil)
	v.title.SetText("No pull request for this branch")
	v.title.SetTooltipText("")
	v.open.SetSensitive(false)
	setPill(v.pill, "No PR", "idle")
	config.Set("rufin_tracked_pr", nil)
}

// Refresh asks GitHub once for the shown PR.
func (v *ChecksView) Refresh() {
	if v.Number == 0 {
		return
	}
	v.fetches++
	fetch, number, repo := v.fetches, v.Number, v.repo
	go func() {
		var data struct {
			Title             string             `json:"title"`
			URL               string             `json:"url"`
			State             string             `json:"state"`
			StatusCheckRollup []daemon.CheckNode `json:"statusCheckRollup"`
		}
		err := ghJSON(repo, 20*time.Second, &data, "pr", "view", strconv.Itoa(number), "--json", "number,title,url,state,statusCheckRollup")
		glib.IdleAdd(func() {
			if fetch != v.fetches || number != v.Number {
				return
			}
			if err != nil {
				setPill(v.pill, "Status unavailable", "idle")
				v.pill.SetTooltipText(err.Error())
				return
			}
			checks := make([]daemon.Check, len(data.StatusCheckRollup))
			for i, node := range data.StatusCheckRollup {
				checks[i] = node.Check()
			}
			v.apply(data.Title, data.URL, data.State, checks)
		})
	}()
}

// update shows the daemon's state of the PR. Otherwise it asks GitHub once: when fetch is set,
// or when the daemon stopped listing the PR, so its merge or close shows.
func (v *ChecksView) update(fetch bool) {
	if v.Number == 0 {
		return
	}
	for _, pr := range client.State().PRs {
		if pr.Number == v.Number && strings.EqualFold(pr.Repo, v.slug) {
			v.watched = true
			v.apply(pr.Title, pr.URL, "OPEN", pr.Checks)
			return
		}
	}
	if fetch || v.watched {
		v.watched = false
		v.Refresh()
	}
}

// setState keeps a merge once known, so a stale OPEN does not reopen the PR.
func (v *ChecksView) setState(state string) {
	if !(v.State == "MERGED" && (state == "" || state == "OPEN")) {
		v.State = state
	}
}

func (v *ChecksView) apply(title, url, state string, checks []daemon.Check) {
	if title != "" {
		v.Title = title
	}
	if url != "" {
		v.URL = url
	}
	if state != "" {
		v.setState(state)
	}
	v.showHeader()
	v.pill.SetTooltipText("")
	v.showChecks(checks)
	if v.OnChecks != nil {
		v.OnChecks(v.Number, checks)
	}
}

func (v *ChecksView) showHeader() {
	text := "PR #" + strconv.Itoa(v.Number)
	if v.State != "" {
		text += " (" + capitalize(v.State) + ")"
	}
	if v.Title != "" {
		text += ": " + v.Title
	}
	v.title.SetText(text)
	v.title.SetTooltipText(text)
	v.open.SetSensitive(v.URL != "")
	if saved := (trackedPR{strconv.Itoa(v.Number), v.Title, v.URL, v.State}); saved != v.saved {
		v.saved = saved
		config.Set("rufin_tracked_pr", saved)
	}
}

// showChecks fills the table, progress and pill; nil empties the table only.
func (v *ChecksView) showChecks(checks []daemon.Check) {
	for ; v.rows > 0; v.rows-- {
		v.grid.RemoveRow(1)
	}
	v.elapsed = map[*gtk.Label]time.Time{}
	v.progress.SetFraction(0)
	if checks == nil {
		return
	}
	if len(checks) == 0 {
		setPill(v.pill, "No Checks Yet", "idle")
		return
	}
	completed, running, failed := 0, false, false
	for i, c := range checks {
		if c.Status == "COMPLETED" {
			completed++
		} else {
			running = true
		}
		status, class := "Pending", "dim"
		switch {
		case slices.Contains(failedConclusions, c.Conclusion):
			status, class, failed = "Failed", "deleted", true
		case c.Conclusion == "SUCCESS":
			status, class = "Passed", "added"
		case c.Conclusion == "SKIPPED":
			status = "Skipped"
		case c.Status == "IN_PROGRESS" || c.Status == "QUEUED":
			status, class = "Running", "accent"
		}
		name := gtk.NewLabel(c.Name)
		name.SetXAlign(0)
		name.SetHExpand(true)
		name.SetEllipsize(3)
		name.SetTooltipText(c.Name)
		state := gtk.NewLabel(status)
		state.SetXAlign(0)
		state.AddCSSClass(class)
		duration := gtk.NewLabel("—")
		duration.SetXAlign(0)
		duration.AddCSSClass("dim")
		start, end := parseTime(c.StartedAt), parseTime(c.FinishedAt)
		switch {
		case c.Conclusion == "SKIPPED" || start.IsZero():
		case !end.IsZero():
			duration.SetText(formatDuration(max(0, end.Sub(start))))
		case c.Status != "COMPLETED":
			duration.SetText(formatDuration(max(0, time.Since(start))))
			v.elapsed[duration] = start
		}
		v.grid.Attach(name, 0, i+1, 1, 1)
		v.grid.Attach(state, 1, i+1, 1, 1)
		v.grid.Attach(duration, 2, i+1, 1, 1)
		if c.URL != "" {
			link := gtk.NewButtonWithLabel("Open Run")
			link.AddCSSClass("flat")
			url := c.URL
			link.ConnectClicked(func() { openURL(url) })
			v.grid.Attach(link, 3, i+1, 1, 1)
		}
		v.rows++
	}
	v.progress.SetFraction(float64(completed) / float64(len(checks)))
	switch {
	case failed:
		setPill(v.pill, "Checks Failed", "failed")
	case running:
		setPill(v.pill, fmt.Sprintf("Running (%d/%d)", completed, len(checks)), "running")
	default:
		setPill(v.pill, "All Checks Passed", "success")
	}
	v.tick()
}

// tick advances running checks' elapsed times every second between daemon updates.
func (v *ChecksView) tick() {
	if v.ticker != 0 || len(v.elapsed) == 0 {
		return
	}
	v.ticker = glib.TimeoutAdd(1000, func() bool {
		if len(v.elapsed) == 0 {
			v.ticker = 0
			return false
		}
		for label, start := range v.elapsed {
			label.SetText(formatDuration(max(0, time.Since(start))))
		}
		return true
	})
}

func parseTime(text string) time.Time {
	t, err := time.Parse(time.RFC3339, text)
	if err != nil || t.Year() < 2000 { // gh reports unset times as year 1
		return time.Time{}
	}
	return t
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return s[:1] + strings.ToLower(s[1:])
}

// openURL opens a link in the browser.
func openURL(url string) {
	if url != "" {
		gtk.NewURILauncher(url).Launch(context(), nil, nil)
	}
}

// ghJSON runs a gh read in repo and decodes its JSON output.
func ghJSON(repo string, timeout time.Duration, out any, args ...string) error {
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return errors.New(message)
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("No data returned")
	}
	return json.Unmarshal(data, out)
}
