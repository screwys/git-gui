package ui

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

var (
	releaseVersion = regexp.MustCompile(`^` + daemon.VersionTag.String() + `$`)
	leadingVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)
	pullNumber     = regexp.MustCompile(`/pull/(\d+)`)
)

var rufinModes = []struct{ name, label, hint, notes string }{
	{"new", "New Release", "Syncs default branch, creates release prep PR, and publishes tag.",
		"Write release description and changelog:"},
	{"replace", "Replace", "Replaces existing tag. You can reuse or modify the tag's release notes below.",
		"Release notes (editable; leave blank to reuse unchanged):"},
	{"resume", "Resume", "Resumes release asset staging and publication after release prep PR has merged.",
		"Release notes already committed; not needed for resume."},
}

// RufinReleaseTab runs Rufin's release helper: a new release, a replacement of a tag, or the
// rest of a release after its preparation PR merged, now or once a PR merges.
type RufinReleaseTab struct {
	Root     *gtk.Paned
	repo     string
	out      *releaseOutput
	follower *ScheduleFollower
	picker   *PRPicker

	modes       *adw.ToggleGroup
	mode        string // new, replace or resume
	hint        *gtk.Label
	version     *gtk.Entry
	latest      *gtk.Label
	fetchNotes  *gtk.Button
	notesStatus *gtk.Label
	notes       *gtk.TextView
	push        *gtk.CheckButton
	dryRun      *gtk.CheckButton
	skipFlathub *gtk.CheckButton
	skipRelease *gtk.CheckButton
	button      *gtk.Button

	refilled string // the job whose command last refilled the form
	after    func() // starts the replacement once the stopped release ends
}

// NewRufinReleaseTab builds the tab for the Rufin checkout at repo.
func NewRufinReleaseTab(repo string) *RufinReleaseTab {
	t := &RufinReleaseTab{repo: repo, out: newReleaseOutput(repo, "rufin", appConfig.ReleaseHelper()),
		follower: NewScheduleFollower(repo, "release"), picker: NewPRPicker(repo, "Release after PR merges:")}
	left := gtk.NewBox(gtk.OrientationVertical, 12)
	left.SetMarginStart(8)
	left.SetMarginEnd(8)
	left.SetMarginTop(8)
	left.SetMarginBottom(8)

	mode := group("Release Mode")
	t.modes = adw.NewToggleGroup()
	t.modes.SetHAlign(gtk.AlignStart)
	for _, m := range rufinModes {
		toggle := adw.NewToggle()
		toggle.SetName(m.name)
		toggle.SetLabel(m.label)
		t.modes.Add(toggle)
	}
	t.hint = gtk.NewLabel("")
	t.hint.SetXAlign(0)
	t.hint.SetWrap(true)
	t.hint.AddCSSClass("dim")
	mode.Append(t.modes)
	mode.Append(t.hint)
	left.Append(mode)

	version := group("Release Version")
	t.version = gtk.NewEntry()
	t.version.SetPlaceholderText("e.g. v0.15.5")
	patch := gtk.NewButtonWithLabel("+ Patch")
	patch.ConnectClicked(func() { t.bump(false) })
	minor := gtk.NewButtonWithLabel("+ Minor")
	minor.ConnectClicked(func() { t.bump(true) })
	t.version.SetHExpand(true)
	versionRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	versionRow.Append(t.version)
	versionRow.Append(patch)
	versionRow.Append(minor)
	t.latest = gtk.NewLabel("Latest tag: detecting...")
	t.latest.SetXAlign(0)
	t.latest.SetHExpand(true)
	t.latest.AddCSSClass("dim")
	t.fetchNotes = gtk.NewButtonWithLabel("Fetch Tag Notes")
	t.fetchNotes.AddCSSClass("flat")
	t.fetchNotes.ConnectClicked(t.loadTagNotes)
	tagRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	tagRow.Append(t.latest)
	tagRow.Append(t.fetchNotes)
	resume := gtk.NewButtonWithLabel("Resume selected release")
	resume.SetTooltipText("Use the version selected in Jobs and Progress")
	resume.SetHAlign(gtk.AlignEnd)
	resume.ConnectClicked(t.resumeSelected)
	version.Append(versionRow)
	version.Append(tagRow)
	version.Append(t.picker.Root)
	version.Append(resume)
	left.Append(version)

	notes := group("Release Summary / Notes")
	t.notesStatus = gtk.NewLabel("")
	t.notesStatus.SetXAlign(0)
	t.notesStatus.AddCSSClass("dim")
	t.notes = gtk.NewTextView()
	t.notes.SetWrapMode(gtk.WrapWordChar)
	notesScroll := gtk.NewScrolledWindow()
	notesScroll.SetChild(t.notes)
	notesScroll.SetMinContentHeight(240)
	notesScroll.SetVExpand(true)
	notesScroll.AddCSSClass("card")
	notes.Append(t.notesStatus)
	notes.Append(notesScroll)
	notes.SetVExpand(true)
	left.Append(notes)

	options := group("Execution Options")
	t.push = gtk.NewCheckButtonWithLabel("Push tag, open PR, and stage assets (--push)")
	t.push.SetActive(true)
	t.dryRun = gtk.NewCheckButtonWithLabel("Dry run tag generation (--dry-run)")
	t.skipFlathub = gtk.NewCheckButtonWithLabel("Skip Flathub PR (--skip-flathub)")
	t.skipRelease = gtk.NewCheckButtonWithLabel("Skip GitHub Release publication (--skip-github-release)")
	for _, check := range []*gtk.CheckButton{t.push, t.dryRun, t.skipFlathub, t.skipRelease} {
		options.Append(check)
	}
	left.Append(options)

	t.button = gtk.NewButtonWithLabel("Start Release")
	t.button.AddCSSClass("suggested-action")
	t.button.ConnectClicked(t.run)
	left.Append(t.button)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(left)
	t.Root = gtk.NewPaned(gtk.OrientationHorizontal)
	t.Root.SetStartChild(scroll)
	t.Root.SetEndChild(t.out.Root)
	t.Root.SetPosition(420)

	t.modes.NotifyProperty("active-name", func() {
		if name := t.modes.ActiveName(); name != t.mode {
			t.setMode(name)
		}
	})
	t.version.ConnectChanged(func() {
		if t.mode == "replace" {
			t.loadTagNotes()
		}
	})
	t.picker.OnChanged = t.updateButton
	t.follower.OnChanged = t.scheduleChanged
	term := t.out.term
	term.OnOutput = t.out.feed
	term.OnStarted = func() { t.out.started(); t.updateButton() }
	term.OnRestored = t.restored
	term.OnFinished = t.finished
	term.OnError = func(string) { t.out.failed() }

	t.setMode("new")
	term.Attach()
	t.follower.Restore()
	t.Refresh()
	return t
}

// Refresh reads the latest tag and the open PRs, reattaches the release job and re-queries GitHub.
func (t *RufinReleaseTab) Refresh() {
	latest := git.LatestTag(t.repo)
	if latest == "" {
		latest = "v0.1.0"
	}
	t.latest.SetText("Latest tag: " + latest)
	if strings.TrimSpace(t.version.Text()) == "" {
		t.version.SetText(nextVersion(latest, false))
	}
	t.picker.Refresh()
	t.out.term.Attach()
	t.out.jobs.Sync()
}

// nextVersion bumps the patch, or the minor with patch zeroed, of a vX.Y.Z version.
func nextVersion(current string, minor bool) string {
	m := leadingVersion.FindStringSubmatch(current)
	if m == nil {
		return "v0.1.0"
	}
	major, _ := strconv.Atoi(m[1])
	mid, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	if minor {
		return fmt.Sprintf("v%d.%d.0", major, mid+1)
	}
	return fmt.Sprintf("v%d.%d.%d", major, mid, patch+1)
}

func (t *RufinReleaseTab) bump(minor bool) {
	if current := strings.TrimSpace(t.version.Text()); leadingVersion.MatchString(current) {
		t.version.SetText(nextVersion(current, minor))
	}
}

func (t *RufinReleaseTab) setMode(mode string) {
	t.mode = mode
	t.modes.SetActiveName(mode)
	for _, m := range rufinModes {
		if m.name == mode {
			t.hint.SetText(m.hint)
			t.notesStatus.SetText(m.notes)
		}
	}
	t.notes.SetSensitive(mode != "resume")
	t.fetchNotes.SetVisible(mode == "replace")
	t.push.SetSensitive(mode != "resume")
	t.dryRun.SetSensitive(mode != "resume")
	if mode == "replace" {
		t.loadTagNotes()
	}
	t.updateButton()
}

// loadTagNotes fills the notes from the annotated tag of the version being replaced.
func (t *RufinReleaseTab) loadTagNotes() {
	version := strings.TrimSpace(t.version.Text())
	if version == "" {
		return
	}
	if notes := tagNotes(t.repo, version); notes != "" {
		t.notes.Buffer().SetText(notes)
		t.notesStatus.SetText("Loaded notes from tag " + version + " (editable):")
	} else {
		t.notesStatus.SetText("No existing tag notes found for " + version + ".")
	}
}

// tagNotes is an annotated tag's message without its signature or generated changelog.
func tagNotes(repo, tag string) string {
	raw, err := git.Run(repo, "cat-file", "tag", "refs/tags/"+tag)
	if err != nil {
		return ""
	}
	_, message, _ := strings.Cut(raw, "\n\n")
	var kept []string
	for _, line := range strings.Split(message, "\n") {
		if strings.HasPrefix(line, "-----BEGIN PGP SIGNATURE-----") || strings.HasPrefix(line, "## Changelog") {
			break
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// resumeSelected resumes the release shown in Jobs and Progress, after its preparation PR merges.
func (t *RufinReleaseTab) resumeSelected() {
	if tag := t.out.jobs.Tag(); tag != "" {
		t.version.SetText(tag)
		t.setMode("resume")
	}
	if m := pullNumber.FindStringSubmatch(t.out.jobs.PRURL()); m != nil {
		n, _ := strconv.Atoi(m[1])
		t.picker.SetNumber(n)
		t.picker.Wait.SetActive(true)
	}
}

func (t *RufinReleaseTab) updateButton() {
	if t.follower.Active() {
		t.button.SetLabel(fmt.Sprintf("Cancel Waiting (PR #%d)", t.follower.Schedule.Number))
		t.button.SetSensitive(true)
		return
	}
	n, _ := t.picker.Number()
	waiting := t.picker.Waiting()
	labels := map[string][2]string{
		"new":     {"Start Release", "Release after PR #%d merges"},
		"replace": {"Replace & Release", "Replace after PR #%d merges"},
		"resume":  {"Resume Release", "Resume after PR #%d merges"},
	}[t.mode]
	if waiting {
		t.button.SetLabel(fmt.Sprintf(labels[1], n))
	} else {
		t.button.SetLabel(labels[0])
	}
	t.button.SetSensitive(!t.out.term.Runner.Running() || waiting || t.mode == "replace")
}

func (t *RufinReleaseTab) run() {
	term := t.out.term
	if t.follower.Active() {
		if err := t.follower.Cancel(); err != nil {
			term.Info("[!] " + err.Error())
		}
		t.updateButton()
		return
	}
	number := 0
	if t.picker.Wait.Active() {
		n, ok := t.picker.Number()
		if !ok {
			alert(t.Root, "Missing PR Number", "Please enter or select a PR number to wait for, or uncheck 'Release after PR merges'.")
			return
		}
		number = n
	}
	helper := appConfig.ReleaseHelper()
	if _, err := os.Stat(helper); err != nil {
		alert(t.Root, "Script Not Found", "Cannot find release script at:\n"+helper+"\n\nYou can configure the script location in Settings.")
		return
	}
	version := strings.TrimSpace(t.version.Text())
	if version == "" {
		alert(t.Root, "Missing Version", "Please enter a release version (e.g. v0.15.5).")
		return
	}
	buffer := t.notes.Buffer()
	notes := strings.TrimSpace(buffer.Text(buffer.StartIter(), buffer.EndIter(), false))
	if t.mode == "new" && notes == "" {
		alert(t.Root, "Missing Summary", "Please enter a release summary / notes.")
		return
	}

	var args []string
	switch t.mode {
	case "resume":
		args = append(args, "--resume-after-prep")
	case "replace":
		args = append(args, "--replace")
		fallthrough
	default:
		if t.push.Active() {
			args = append(args, "--push")
		}
		if t.dryRun.Active() {
			args = append(args, "--dry-run")
		}
	}
	if t.skipFlathub.Active() {
		args = append(args, "--skip-flathub")
	}
	if t.skipRelease.Active() {
		args = append(args, "--skip-github-release")
	}
	args = append(args, version)
	if t.mode != "resume" && notes != "" {
		args = append(args, notes)
	}
	command := append([]string{helper}, args...)

	if term.Runner.Running() && number == 0 {
		action := "restart"
		if t.mode == "replace" {
			action = "replace"
		}
		dialog := adw.NewAlertDialog("Release in Progress",
			"A release script is currently running. Do you want to stop it and "+action+" the release?")
		dialog.AddResponse("no", "No")
		dialog.AddResponse("yes", "Yes")
		dialog.SetResponseAppearance("yes", adw.ResponseDestructive)
		dialog.SetDefaultResponse("no")
		dialog.SetCloseResponse("no")
		dialog.ConnectResponse(func(response string) {
			if response == "yes" {
				t.confirm(command, version, notes, number)
			}
		})
		dialog.Present(t.Root)
		return
	}
	t.confirm(command, version, notes, number)
}

func (t *RufinReleaseTab) confirm(command []string, version, notes string, number int) {
	mode := t.mode
	label := map[string]string{"new": "New Release", "replace": "Replace Tag", "resume": "Resume Release"}[mode]
	details := []detail{{"Release Mode", label}, {"Target Version", version}}
	if number != 0 {
		details = append(details, detail{"Wait for PR", fmt.Sprintf("#%d (auto-start when merged)", number)})
	}
	details = append(details, detail{"Push Remote", yesNo(mode == "resume" || t.push.Active())})
	if t.dryRun.Active() && mode != "resume" {
		details = append(details, detail{"Dry Run", "Yes"})
	}
	if t.skipFlathub.Active() {
		details = append(details, detail{"Flathub PR", "Skipped"})
	}
	if t.skipRelease.Active() {
		details = append(details, detail{"GitHub Release", "Skipped"})
	}
	preview := notes
	if mode == "resume" {
		preview = ""
	}
	// A replacement cancels the old tag's unfinished runs once, right before its helper starts.
	var cancelRuns []string
	if mode == "replace" {
		cancelRuns = []string{"Stage " + version, version}
	}
	confirm(t.Root, "Release "+version, details, "Release Notes Preview:", preview, "Confirm Release", func() {
		if number != 0 {
			t.schedule(number, command, version, cancelRuns)
			return
		}
		start := func() { t.start(command, version, cancelRuns) }
		if t.out.term.Runner.Running() {
			t.after = start
			t.out.term.Runner.Stop()
			return
		}
		start()
	})
}

func (t *RufinReleaseTab) start(command []string, version string, cancelRuns []string) {
	term := t.out.term
	t.out.jobs.Reset()
	term.Clear()
	term.SetLabel("Running " + version)
	if cancelRuns != nil {
		term.Info("[*] Cancelling in-progress CI runs for " + version + "...")
		if _, err := client.Request(daemon.Request{Op: "cancel_runs", Cwd: t.repo, Titles: cancelRuns}); err != nil {
			term.Info("[!] " + err.Error())
		}
	}
	term.Runner.Start(command, nil)
	t.out.jobs.Follow(version)
}

// schedule hands the release to the daemon, which starts it once PR number merges and cancels
// the replaced tag's runs then.
func (t *RufinReleaseTab) schedule(number int, command []string, version string, cancelRuns []string) {
	term := t.out.term
	term.Clear()
	t.out.started()
	if err := t.follower.Submit(number, client.Job(t.repo, "release", command, nil), nil, cancelRuns); err != nil {
		term.Info("[!] " + err.Error())
		term.SetStatus("Not scheduled", "failed")
		return
	}
	term.Info(fmt.Sprintf("[*] Release %s scheduled: git-gui-daemon starts it once PR #%d merges.", version, number))
	t.updateButton()
}

func (t *RufinReleaseTab) scheduleChanged(s daemon.Schedule) {
	term := t.out.term
	t.picker.ShowSchedule(s)
	term.Info("[*] " + s.Message)
	if s.Status == "started" && !term.Runner.Running() {
		t.out.jobs.Reset()
		term.Attach()
	}
	switch s.Status {
	case "failed", "closed":
		term.SetStatus(s.Message, "failed")
	case "cancelled":
		term.SetStatus("Cancelled", "idle")
	case "started":
	default:
		term.SetStatus(s.Message, "running")
	}
	t.updateButton()
}

// restored refills the form from a running or failed release job, once per job.
func (t *RufinReleaseTab) restored(record daemon.Record, running, _ bool) {
	key := t.out.term.Runner.Path
	if key == "" {
		key = strconv.Itoa(record.Pid)
	}
	if key != t.refilled {
		t.refilled = key
		if running || record.ExitCode == nil || *record.ExitCode != 0 {
			t.refill(record.Command, running)
		}
	}
	t.out.restored(running)
	t.updateButton()
}

func (t *RufinReleaseTab) refill(command []string, running bool) {
	for i, arg := range command {
		if !releaseVersion.MatchString(arg) {
			continue
		}
		mode := "new"
		switch {
		case slices.Contains(command, "--resume-after-prep"):
			mode = "resume"
		case slices.Contains(command, "--replace"):
			mode = "replace"
		}
		t.version.SetText(arg)
		t.setMode(mode)
		t.push.SetActive(slices.Contains(command, "--push") || mode == "resume")
		t.dryRun.SetActive(slices.Contains(command, "--dry-run"))
		t.skipFlathub.SetActive(slices.Contains(command, "--skip-flathub"))
		t.skipRelease.SetActive(slices.Contains(command, "--skip-github-release"))
		if mode != "resume" && i+1 < len(command) {
			t.notes.Buffer().SetText(command[i+1])
		}
		if running {
			t.out.term.SetLabel("Running " + arg)
		}
		t.out.jobs.Follow(arg)
		return
	}
}

func (t *RufinReleaseTab) finished(code int, record daemon.Record) {
	if t.follower.Active() {
		t.updateButton()
		return
	}
	if code != 0 || record.Stopped {
		t.out.failed()
	}
	t.updateButton()
	t.Refresh()
	if after := t.after; after != nil {
		t.after = nil
		after()
	}
}
