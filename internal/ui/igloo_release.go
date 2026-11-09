package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

var versionNumbers = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

// IglooReleaseTab runs Igloo's `just release <bump> <summary>`, which tags, publishes the release
// and dispatches its artifact workflows, or `just release-local` for a local signed tag only.
type IglooReleaseTab struct {
	Root    *gtk.Paned
	repo    string
	out     *releaseOutput
	bumps   *adw.ToggleGroup
	target  *gtk.Label // the version the recipe computes; it takes the bump, not a version
	latest  *gtk.Label
	notes   *gtk.TextView
	publish *gtk.CheckButton
	button  *gtk.Button
}

// NewIglooReleaseTab builds the tab for the Igloo checkout at repo.
func NewIglooReleaseTab(repo string) *IglooReleaseTab {
	t := &IglooReleaseTab{repo: repo, out: newReleaseOutput(repo, "igloo", "")}
	left := gtk.NewBox(gtk.OrientationVertical, 12)
	margins(left, 8)

	bump := group("Version Bump")
	t.bumps = adw.NewToggleGroup()
	t.bumps.SetHAlign(gtk.AlignStart)
	for _, name := range []string{"patch", "minor", "major"} {
		toggle := adw.NewToggle()
		toggle.SetName(name)
		toggle.SetLabel(strings.ToUpper(name[:1]) + name[1:])
		t.bumps.Add(toggle)
	}
	t.bumps.SetActiveName("patch")
	bump.Append(t.bumps)
	left.Append(bump)

	target := group("Target Version")
	t.target = gtk.NewLabel("")
	t.target.SetXAlign(0)
	t.target.SetSelectable(true)
	t.target.AddCSSClass("title-4")
	t.latest = gtk.NewLabel("Latest tag: detecting...")
	t.latest.SetXAlign(0)
	t.latest.AddCSSClass("dim")
	target.Append(t.target)
	target.Append(t.latest)
	left.Append(target)

	notes := group("Release Summary / Notes")
	status := gtk.NewLabel("Summary of changes (becomes first paragraph of GitHub release):")
	status.SetXAlign(0)
	status.SetWrap(true)
	status.AddCSSClass("dim")
	t.notes = gtk.NewTextView()
	t.notes.SetWrapMode(gtk.WrapWordChar)
	notesScroll := gtk.NewScrolledWindow()
	notesScroll.SetChild(t.notes)
	notesScroll.SetMinContentHeight(240)
	notesScroll.SetVExpand(true)
	notesScroll.AddCSSClass("card")
	notes.Append(status)
	notes.Append(notesScroll)
	notes.SetVExpand(true)
	left.Append(notes)

	options := group("Execution Options")
	t.publish = gtk.NewCheckButtonWithLabel("Publish release and dispatch artifacts (just release)")
	t.publish.SetActive(true)
	options.Append(t.publish)
	left.Append(options)

	t.button = gtk.NewButtonWithLabel("Start Release")
	t.button.AddCSSClass("suggested-action")
	t.button.ConnectClicked(t.run)
	left.Append(t.button)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(left)
	t.Root = split(scroll, t.out.Root)

	t.bumps.NotifyProperty("active-name", t.computeVersion)
	term := t.out.term
	term.OnOutput = t.out.feed
	term.OnStarted = func() {
		t.out.started()
		t.button.SetSensitive(false)
		// The daemon keeps following the release while its job runs.
		t.out.jobs.Sync()
	}
	term.OnRestored = func(_ daemon.Record, running, _ bool) {
		t.out.restored(running)
		t.button.SetSensitive(!running)
	}
	term.OnFinished = func(code int, record daemon.Record) {
		t.button.SetSensitive(true)
		if code != 0 || record.Stopped {
			t.out.failed()
		}
		t.Refresh()
	}
	term.OnError = func(string) { t.out.failed() }

	term.Attach()
	t.Refresh()
	return t
}

// Refresh recomputes the target version, reattaches the release job and re-queries GitHub.
func (t *IglooReleaseTab) Refresh() {
	t.computeVersion()
	t.out.term.Attach()
	t.out.jobs.Sync()
}

// computeVersion bumps the latest tag the way the release recipe will.
func (t *IglooReleaseTab) computeVersion() {
	latest := git.LatestTag(t.repo)
	if latest == "" {
		latest = "v0.1.0"
	}
	t.latest.SetText("Latest tag: " + latest)
	m := versionNumbers.FindStringSubmatch(latest)
	if m == nil {
		t.target.SetText("v0.1.0")
		return
	}
	parts := [3]int{}
	for i := range parts {
		parts[i], _ = strconv.Atoi(m[i+1])
	}
	switch t.bumps.ActiveName() {
	case "major":
		parts = [3]int{parts[0] + 1, 0, 0}
	case "minor":
		parts = [3]int{parts[0], parts[1] + 1, 0}
	default:
		parts[2]++
	}
	t.target.SetText(fmt.Sprintf("v%d.%d.%d", parts[0], parts[1], parts[2]))
}

func (t *IglooReleaseTab) run() {
	if t.out.term.Runner.Running() {
		alert(t.Root, "Running", "A script is already running.")
		return
	}
	buffer := t.notes.Buffer()
	summary := strings.TrimSpace(buffer.Text(buffer.StartIter(), buffer.EndIter(), false))
	if summary == "" {
		alert(t.Root, "Missing Summary", "Please enter a release summary / notes.")
		return
	}
	version, bump := t.target.Text(), t.bumps.ActiveName()
	recipe, artifacts := "release", "Yes (push tag, release, and build artifacts)"
	if !t.publish.Active() {
		recipe, artifacts = "release-local", "No (local signed tag only)"
	}
	details := []detail{{"Release Bump", strings.ToUpper(bump[:1]) + bump[1:]}, {"Target Version", version},
		{"Release Recipe", "just " + recipe}, {"Push & Artifacts", artifacts},
		{"Release Notes", fmt.Sprintf("%d characters", utf8.RuneCountInString(summary))}}
	confirm(t.Root, "Igloo Release "+version, details, "Release Notes Preview:", summary, "Start Release ("+version+")", func() {
		term := t.out.term
		t.out.jobs.Reset()
		term.Clear()
		term.SetLabel("Running " + version)
		term.Runner.Start([]string{"just", recipe, bump, summary}, nil)
	})
}
