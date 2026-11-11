package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

const rufinCOPR = "https://copr.fedorainfracloud.org/coprs/screwyy/rufin/"

var (
	ansiEscape  = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	pullURL     = regexp.MustCompile(`https://github\.com/[^/\s]+/[^/\s]+/pull/(\d+)`)
	releaseLink = regexp.MustCompile(`https://github\.com/[^/\s]+/[^/\s]+/releases/tag/(\S+)`)
	tableCells  = regexp.MustCompile(`\s{2,}`)
	// Lines of git-gui rufin-release: the release workflow published, and the packages followed.
	releasePublished = regexp.MustCompile(`^Published v\S+: https://`)
	packagesPassed   = "All package-manager publication workflows completed successfully."
	iglooDispatches  = map[string]string{
		"workflow run release.yml": "Release Gate", "workflow run container-release.yml": "Container Release",
		"workflow run android-release.yml": "Android Release", "workflow run windows-release.yml": "Windows Release",
	}
)

// ReleaseJobs is a release tab's "Jobs and Progress": the release's workflows and publications
// with quick links. GitHub status comes from git-gui-daemon, which queries the release this view
// follows and writes it to daemon.json; the tab's live output fills in links and run progress
// between those queries.
type ReleaseJobs struct {
	Root   *gtk.Box
	repo   string // real path
	kind   string // rufin, igloo or generic
	runner *Runner

	tagList  *gtk.StringList
	picker   *gtk.DropDown
	filling  bool // the picker is being filled, not chosen from
	sync     *gtk.Label
	syncNow  *gtk.Button
	coprLink *gtk.Button
	rerun    *gtk.Button
	cancel   *gtk.Button
	prLink   *gtk.Button
	flathub  *gtk.Button
	actions  *gtk.Button
	release  *gtk.Button
	grid     *gtk.Grid

	tag     string // the followed tag; empty follows the newest
	shown   string // the tag the rows describe
	applied int64  // the daemon query shown
	rows    []daemon.ReleaseRow
	runLine map[string]*regexp.Regexp

	prURL, flathubURL, releaseURL, repoURL string
	runID                                  int64
	runStatus                              string
	releaseTag                             string
	pending                                string

	OnWorkflowStarted func() // the release job started or followed a workflow run
	OnLocalWork       func() // the release job's local work resumed after remote workflows passed
	OnRemoteReady     func() // GitHub reported workflow runs for the release
}

// NewReleaseJobs builds the view for repo. runner is the tab's release job, which the daemon keeps
// following while it runs.
func NewReleaseJobs(repo, kind string, runner *Runner) *ReleaseJobs {
	if real, err := filepath.EvalSymlinks(repo); err == nil {
		repo = real
	}
	v := &ReleaseJobs{Root: gtk.NewBox(gtk.OrientationVertical, 10), repo: repo, kind: kind, runner: runner,
		runLine: map[string]*regexp.Regexp{}}
	v.Root.SetMarginStart(10)
	v.Root.SetMarginEnd(10)
	v.Root.SetMarginTop(8)
	v.Root.SetMarginBottom(8)

	targets := gtk.NewBox(gtk.OrientationHorizontal, 8)
	v.tagList = gtk.NewStringList(nil)
	v.picker = gtk.NewDropDown(v.tagList, nil)
	v.picker.NotifyProperty("selected", func() {
		if i := v.picker.Selected(); !v.filling && i < v.tagList.NItems() {
			v.Follow(v.tagList.String(i))
		}
	})
	v.sync = gtk.NewLabel("Checking GitHub...")
	v.sync.SetWrap(true)
	v.sync.SetXAlign(0)
	v.sync.SetHExpand(true)
	v.sync.AddCSSClass("dim")
	targets.Append(gtk.NewLabel("Release:"))
	targets.Append(v.picker)
	targets.Append(v.sync)
	v.Root.Append(targets)

	bar := gtk.NewBox(gtk.OrientationHorizontal, 8)
	bar.AddCSSClass("toolbar")
	bar.AddCSSClass("card")
	// The links wrap in a narrow pane instead of widening it.
	links := adw.NewWrapBox()
	links.SetChildSpacing(8)
	links.SetLineSpacing(6)
	links.SetHExpand(true)
	quick := gtk.NewLabel("Quick Links:")
	quick.AddCSSClass("dim")
	quick.AddCSSClass("caption-heading")
	links.Append(quick)
	link := func(label string, url func() string) *gtk.Button {
		b := gtk.NewButtonWithLabel(label)
		b.ConnectClicked(func() { openURL(url()) })
		links.Append(b)
		return b
	}
	if kind == "rufin" {
		v.coprLink = link("COPR Builds", func() string { return rufinCOPR })
		v.prLink = link("Release PR", func() string { return v.prURL })
		v.flathub = link("Flathub PR", func() string { return v.flathubURL })
	} else {
		v.actions = link("GitHub Actions", func() string {
			if kind == "igloo" {
				return "https://github.com/screwys/Igloo/actions"
			}
			return v.repoURL + "/actions"
		})
	}
	v.release = link("GitHub Release", func() string { return v.releaseURL })
	if kind == "rufin" {
		// The release workflow's own controls: a failed job re-runs without rebuilding the others.
		v.rerun = gtk.NewButtonWithLabel("Re-run Failed Jobs")
		v.rerun.ConnectClicked(func() { v.runAction("rerun", "--failed") })
		v.cancel = gtk.NewButtonWithLabel("Cancel Run")
		v.cancel.ConnectClicked(func() { v.runAction("cancel") })
		links.Append(v.rerun)
		links.Append(v.cancel)
	}
	v.syncNow = gtk.NewButtonWithLabel("Sync Remote Status")
	v.syncNow.SetTooltipText("Query GitHub for latest release tag and workflow status")
	v.syncNow.SetVAlign(gtk.AlignStart)
	v.syncNow.ConnectClicked(v.Sync)
	bar.Append(links)
	bar.Append(v.syncNow)
	v.Root.Append(bar)

	v.grid = gtk.NewGrid()
	v.grid.SetColumnSpacing(16)
	v.grid.SetRowSpacing(8)
	v.grid.SetMarginStart(10)
	v.grid.SetMarginEnd(10)
	v.grid.SetMarginTop(8)
	v.grid.SetMarginBottom(8)
	table := gtk.NewScrolledWindow()
	table.SetChild(v.grid)
	table.SetVExpand(true)
	table.AddCSSClass("card")
	v.Root.Append(table)

	names := map[string][]string{"rufin": daemon.RufinRows, "igloo": daemon.IglooRows}[kind]
	for _, name := range names {
		v.rows = append(v.rows, daemon.ReleaseRow{Name: name, Status: "idle", Step: "—"})
	}
	v.redraw()
	v.load(client.State())
	onDaemonState(v.load)
	return v
}

// Follow shows tag; empty follows the newest release.
func (v *ReleaseJobs) Follow(tag string) {
	v.tag = tag
	v.Sync()
}

// Sync asks the daemon to query GitHub for the followed release now.
func (v *ReleaseJobs) Sync() {
	req := daemon.Request{Op: "follow_release", Cwd: v.repo, Kind: v.kind, Tag: v.tag, Pid: os.Getpid()}
	if v.runner != nil && v.runner.Running() {
		req.JobPath = v.runner.Path
	}
	if _, err := client.Request(req); err != nil {
		v.sync.SetText("Status unavailable: " + err.Error())
		return
	}
	v.sync.SetText("Checking GitHub...")
	v.syncNow.SetSensitive(false)
}

// Tag is the release shown, for resuming it.
func (v *ReleaseJobs) Tag() string { return v.releaseTag }

// PRURL is the release's preparation PR, when known.
func (v *ReleaseJobs) PRURL() string { return v.prURL }

// Reset forgets progress and links before a new release starts.
func (v *ReleaseJobs) Reset() {
	for i := range v.rows {
		v.rows[i] = daemon.ReleaseRow{Name: v.rows[i].Name, Status: "idle", Step: "—"}
	}
	v.resetLinks()
	v.pending = ""
	v.redraw()
}

func (v *ReleaseJobs) resetLinks() {
	v.runID, v.runStatus = 0, ""
	v.prURL, v.flathubURL, v.releaseURL, v.releaseTag, v.shown = "", "", "", "", ""
}

// load shows the daemon's latest answer for this repository.
func (v *ReleaseJobs) load(state daemon.State) {
	var r *daemon.Release
	for _, candidate := range state.Releases {
		if candidate.Repo == v.repo {
			r = candidate
		}
	}
	if r == nil {
		return
	}
	if r.Checking {
		v.sync.SetText("Checking GitHub...")
	} else {
		v.sync.SetText(r.Sync)
	}
	v.syncNow.SetSensitive(!r.Checking)
	// The saved answer shows at startup whatever it followed; later only answers for this tag.
	if r.Updated == v.applied || v.applied != 0 && (r.Tag != v.tag || r.Kind != v.kind) {
		return
	}
	v.applied = r.Updated
	if r.Shown != v.shown && v.shown != "" {
		v.resetLinks()
	}
	v.shown, v.releaseTag = r.Shown, r.Shown
	v.rows = slices.Clone(r.Rows)
	v.releaseURL, v.flathubURL, v.repoURL = r.ReleaseURL, r.FlathubURL, r.RepoURL
	v.runID, v.runStatus = r.RunID, r.RunStatus
	if r.PRURL != "" {
		v.prURL = r.PRURL
	}
	tags := r.Tags
	if r.Shown != "" && !slices.Contains(tags, r.Shown) {
		tags = append([]string{r.Shown}, tags...)
	}
	v.filling = true
	v.tagList.Splice(0, v.tagList.NItems(), tags)
	v.picker.SetSelected(uint(max(0, slices.Index(tags, r.Shown))))
	v.filling = false
	v.redraw()
	for _, row := range r.Rows {
		if strings.Contains(row.URL, "/actions/runs/") {
			if v.OnRemoteReady != nil {
				v.OnRemoteReady()
			}
			break
		}
	}
}

func (v *ReleaseJobs) redraw() {
	if v.coprLink != nil {
		running := slices.Contains([]string{"queued", "in_progress", "waiting", "requested", "pending"}, v.runStatus)
		v.rerun.SetSensitive(v.runID != 0 && !running && v.runStatus != "success")
		v.cancel.SetSensitive(v.runID != 0 && running)
		v.prLink.SetSensitive(v.prURL != "")
		v.flathub.SetSensitive(v.flathubURL != "")
	}
	if v.actions != nil {
		v.actions.SetSensitive(v.kind == "igloo" || v.repoURL != "")
	}
	if v.releaseTag != "" {
		v.release.SetLabel("Release (" + v.releaseTag + ")")
	} else {
		v.release.SetLabel("GitHub Release")
	}
	v.release.SetSensitive(v.releaseURL != "")

	for child := v.grid.FirstChild(); child != nil; child = v.grid.FirstChild() {
		v.grid.Remove(child)
	}
	for column, title := range []string{"Workflow / Publication", "Status", "Current / Failed Step", "Action"} {
		header := gtk.NewLabel(title)
		header.SetXAlign(0)
		header.AddCSSClass("dim")
		header.AddCSSClass("caption-heading")
		v.grid.Attach(header, column, 0, 1, 1)
	}
	for i, row := range v.rows {
		name := gtk.NewLabel(row.Name)
		name.SetXAlign(0)
		text, state := releaseStatus(row.Status)
		status := newPill()
		setPill(status, text, state)
		status.SetHAlign(gtk.AlignStart)
		status.SetVAlign(gtk.AlignCenter)
		step := gtk.NewLabel(row.Step)
		step.SetXAlign(0)
		step.SetHExpand(true)
		step.SetEllipsize(pango.EllipsizeEnd)
		step.SetTooltipText(row.Step)
		step.AddCSSClass("dim")
		var action gtk.Widgetter
		if row.URL != "" {
			label := "Open Run"
			if row.Name == "Flathub PR" {
				label = "Open PR"
			}
			button := gtk.NewButtonWithLabel(label)
			button.AddCSSClass("flat")
			url := row.URL
			button.ConnectClicked(func() { openURL(url) })
			action = button
		} else {
			dash := gtk.NewLabel("—")
			dash.AddCSSClass("dim")
			action = dash
		}
		v.grid.Attach(name, 0, i+1, 1, 1)
		v.grid.Attach(status, 1, i+1, 1, 1)
		v.grid.Attach(step, 2, i+1, 1, 1)
		v.grid.Attach(action, 3, i+1, 1, 1)
	}
}

// releaseStatus is a row status's text and pill colour.
func releaseStatus(status string) (string, string) {
	switch s := strings.ToLower(status); s {
	case "success", "passed":
		return "Passed", "success"
	case "merged":
		return "Merged", "success"
	case "open":
		return "Open", "running"
	case "closed":
		return "Closed", "failed"
	case "not_found":
		return "Not found", "idle"
	case "running", "in_progress", "queued", "waiting", "pending":
		return "Running", "running"
	case "failure", "failed":
		return "Failed", "failed"
	case "":
		return "", "idle"
	default:
		s = strings.ReplaceAll(s, "_", " ")
		return strings.ToUpper(s[:1]) + s[1:], "idle"
	}
}

// Feed reads the release job's output. Live output also moves rows and stages; a finished job's
// saved output only restores its links.
func (v *ReleaseJobs) Feed(text string, live bool) {
	v.pending += text
	cut := strings.LastIndexByte(v.pending, '\n')
	if cut < 0 {
		return
	}
	block := ansiEscape.ReplaceAllString(v.pending[:cut], "")
	v.pending = v.pending[cut+1:]
	changed := false
	for _, line := range strings.Split(block, "\n") {
		if v.readLinks(line) {
			changed = true
		}
		if live && v.readProgress(line) {
			changed = true
		}
	}
	if changed {
		v.redraw()
	}
	if !live {
		return
	}
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "actions/runs/") && hasAnyPrefix(line, "Started ", "Reusing ", "Following replacement ", "- "):
			if v.OnWorkflowStarted != nil {
				v.OnWorkflowStarted()
			}
		case releasePublished.MatchString(trimmed) || trimmed == packagesPassed:
			if v.OnLocalWork != nil {
				v.OnLocalWork()
			}
		}
	}
}

func (v *ReleaseJobs) readLinks(line string) bool {
	changed := false
	if v.kind == "rufin" {
		if url := pullURL.FindString(line); url != "" {
			if strings.Contains(url, "flathub") {
				v.flathubURL = url
			} else {
				v.prURL = url
			}
			changed = true
		}
	}
	if m := releaseLink.FindStringSubmatch(line); m != nil {
		v.releaseURL, v.releaseTag = m[0], m[1]
		changed = true
	}
	return changed
}

// readProgress applies the release job's run announcements, summaries and status tables to the rows.
func (v *ReleaseJobs) readProgress(line string) bool {
	changed := false
	for i := range v.rows {
		row := &v.rows[i]
		pattern, ok := v.runLine[row.Name]
		if !ok {
			pattern = regexp.MustCompile(`(?i)(?:Started|Reusing|-)\s+` + regexp.QuoteMeta(row.Name) +
				`(?:\s+candidate\s+run)?:\s*(https://github\.com/\S+/actions/runs/\d+)`)
			v.runLine[row.Name] = pattern
		}
		if m := pattern.FindStringSubmatch(line); m != nil {
			row.URL = m[1]
			if row.Status == "idle" {
				row.Status = "running"
			}
			changed = true
		}
	}
	if v.kind == "igloo" {
		for marker, name := range iglooDispatches {
			if strings.Contains(line, marker) {
				changed = v.setStatus(name, "running") || changed
			}
		}
	}
	if strings.Contains(line, packagesPassed) {
		v.setStatus("Homebrew", "passed")
		changed = true
	}
	var cells []string
	for _, cell := range tableCells.Split(line, -1) {
		if cell = strings.TrimSpace(cell); cell != "" {
			cells = append(cells, cell)
		}
	}
	if len(cells) >= 3 {
		for i := range v.rows {
			if v.rows[i].Name != cells[0] {
				continue
			}
			switch strings.ToLower(cells[2]) {
			case "passed", "success", "completed":
				v.rows[i].Status = "passed"
			case "running", "in_progress":
				v.rows[i].Status = "running"
			case "failed", "failure":
				v.rows[i].Status = "failed"
			}
			if len(cells) >= 5 {
				v.rows[i].Step = cells[4]
			}
			changed = true
		}
	}
	return changed
}

func (v *ReleaseJobs) setStatus(name, status string) bool {
	for i := range v.rows {
		if v.rows[i].Name == name {
			v.rows[i].Status = status
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// releaseOutput is a release tab's right side, Jobs and Progress and the Terminal Log. The page
// follows the release's stage: local work shows the log, remote workflows show the jobs, a failure
// returns to the log. It only switches when the stage changes, so a page the user picked stays.
type releaseOutput struct {
	Root  *gtk.Notebook
	jobs  *ReleaseJobs
	term  *Terminal
	stage string // local, remote or failed
	// remoteWhileRunning lets GitHub progress take the page while the job still runs.
	remoteWhileRunning func() bool
}

func newReleaseOutput(repo, kind, program string) *releaseOutput {
	o := &releaseOutput{Root: gtk.NewNotebook(), term: NewTerminal(repo, "release", program)}
	o.jobs = NewReleaseJobs(repo, kind, o.term.Runner)
	o.Root.AppendPage(o.jobs.Root, gtk.NewLabel("Jobs and Progress"))
	o.Root.AppendPage(o.term.Root, gtk.NewLabel("Terminal Log"))
	o.term.OnShow = func() { o.Root.SetCurrentPage(1) }
	o.jobs.OnWorkflowStarted = func() {
		if o.stage != "failed" {
			o.show("remote", false)
		}
	}
	o.jobs.OnLocalWork = func() {
		if o.term.Runner.Running() {
			o.show("local", false)
		}
	}
	o.jobs.OnRemoteReady = func() {
		running := o.term.Runner.Running()
		if o.stage != "failed" && (!running || o.stage == "remote" || o.remoteWhileRunning != nil && o.remoteWhileRunning()) {
			o.show("remote", false)
		}
	}
	return o
}

func (o *releaseOutput) show(stage string, force bool) {
	if force || stage != o.stage {
		o.stage = stage
		if stage == "remote" {
			o.Root.SetCurrentPage(0)
		} else {
			o.Root.SetCurrentPage(1)
		}
	}
}

// started shows the log for a job that just started, even when the stage was already local.
func (o *releaseOutput) started() { o.show("local", true) }

// restored shows the log for a running job found at attach, unless remote work is shown.
func (o *releaseOutput) restored(running bool) {
	if running && o.stage != "remote" {
		o.show("local", false)
	}
}

func (o *releaseOutput) failed() { o.show("failed", false) }

// feed hands the job's output to the jobs view; replayed output of a finished job is history.
func (o *releaseOutput) feed(text string, replaying bool) {
	o.jobs.Feed(text, !replaying || o.term.Runner.Running())
}

// runAction re-runs or cancels the release workflow run shown, then asks for its new state.
func (v *ReleaseJobs) runAction(action string, flags ...string) {
	id, repo := v.runID, git.RemoteSlug(v.repo)
	if id == 0 || repo == "" {
		return
	}
	v.rerun.SetSensitive(false)
	v.cancel.SetSensitive(false)
	go func() {
		args := append([]string{"run", action, strconv.FormatInt(id, 10), "--repo", repo}, flags...)
		out, err := exec.Command("gh", args...).CombinedOutput()
		glib.IdleAdd(func() {
			if err != nil {
				v.sync.SetText(strings.TrimSpace(string(out)))
			}
			v.Sync()
		})
	}()
}
