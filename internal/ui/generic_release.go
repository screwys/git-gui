package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
	"github.com/screwys/git-gui/internal/tasks"
)

var exactVersion = regexp.MustCompile(`^(v?)(\d+)\.(\d+)\.(\d+)$`)

// releaseForm is the saved generic release form, state.json "project_release_form:<realpath>".
type releaseForm struct {
	Tag      string `json:"tag"`
	Target   string `json:"target"`
	Notes    string `json:"notes"`
	Push     bool   `json:"push"`
	GitHub   bool   `json:"github"`
	Workflow string `json:"workflow"`
}

// GenericReleaseTab tags a commit and publishes its GitHub release, through a release workflow
// of the repository or the GitHub CLI, for projects without a release helper.
type GenericReleaseTab struct {
	Root      *gtk.Paned
	repo      string
	stateKey  string
	out       *releaseOutput
	tag       *gtk.Entry
	latest    *gtk.Label
	latestTag string
	target    *gtk.Entry
	notes     *gtk.TextView
	push      *gtk.CheckButton
	github    *gtk.CheckButton
	publisher *gtk.DropDown
	workflows []tasks.Workflow // the publisher list's workflows, before "GitHub CLI"
	button    *gtk.Button
	followed  string // the job whose tag the jobs view was pointed at
}

// NewGenericReleaseTab builds the tab for repo.
func NewGenericReleaseTab(repo string) *GenericReleaseTab {
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		real = repo
	}
	t := &GenericReleaseTab{repo: repo, stateKey: "project_release_form:" + real, out: newReleaseOutput(repo, "generic", ""),
		workflows: tasks.ReleaseWorkflows(repo)}
	left := gtk.NewBox(gtk.OrientationVertical, 12)
	margins(left, 8)

	tag := group("Tag")
	t.tag = gtk.NewEntry()
	t.tag.SetPlaceholderText("v1.2.3")
	bumps := gtk.NewBox(gtk.OrientationHorizontal, 6)
	bumps.AddCSSClass("linked")
	for _, bump := range []string{"patch", "minor", "major"} {
		button := gtk.NewButtonWithLabel(strings.ToUpper(bump[:1]) + bump[1:])
		button.SetHExpand(true)
		button.ConnectClicked(func() { t.suggest(bump) })
		bumps.Append(button)
	}
	t.latest = gtk.NewLabel("")
	t.latest.SetXAlign(0)
	t.latest.AddCSSClass("dim")
	t.target = gtk.NewEntry()
	t.target.SetText("HEAD")
	tag.Append(t.tag)
	tag.Append(bumps)
	tag.Append(t.latest)
	tag.Append(row("Commit", t.target))
	left.Append(tag)

	notes := group("Release notes")
	t.notes = gtk.NewTextView()
	t.notes.SetWrapMode(gtk.WrapWordChar)
	notesScroll := gtk.NewScrolledWindow()
	notesScroll.SetChild(t.notes)
	notesScroll.SetMinContentHeight(160)
	notesScroll.SetVExpand(true)
	notesScroll.AddCSSClass("card")
	notes.Append(notesScroll)
	notes.SetVExpand(true)
	left.Append(notes)

	options := group("Options")
	t.push = gtk.NewCheckButtonWithLabel("Push tag to origin")
	t.push.SetActive(true)
	t.github = gtk.NewCheckButtonWithLabel("Create GitHub release")
	t.github.SetActive(true)
	var publishers []string
	for _, w := range t.workflows {
		publishers = append(publishers, "Workflow: "+w.Name)
	}
	t.publisher = gtk.NewDropDownFromStrings(append(publishers, "GitHub CLI"))
	options.Append(t.push)
	options.Append(t.github)
	options.Append(row("Publisher", t.publisher))
	left.Append(options)

	t.button = gtk.NewButtonWithLabel("Release")
	t.button.AddCSSClass("suggested-action")
	t.button.ConnectClicked(t.run)
	left.Append(t.button)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(left)
	t.Root = split(scroll, t.out.Root)

	t.readTags()
	form := releaseForm{Tag: t.tag.Text(), Target: "HEAD", Push: true, GitHub: true, Workflow: t.workflow()}
	config.Get(t.stateKey, &form)
	t.tag.SetText(form.Tag)
	t.target.SetText(form.Target)
	t.notes.Buffer().SetText(form.Notes)
	t.push.SetActive(form.Push)
	t.github.SetActive(form.GitHub)
	if i := slices.IndexFunc(t.workflows, func(w tasks.Workflow) bool { return w.File == form.Workflow }); i >= 0 {
		t.publisher.SetSelected(uint(i))
	} else if form.Workflow == "" {
		t.publisher.SetSelected(uint(len(t.workflows)))
	}
	t.publisher.SetSensitive(t.github.Active())

	t.tag.ConnectChanged(t.save)
	t.target.ConnectChanged(t.save)
	t.notes.Buffer().ConnectChanged(t.save)
	t.push.ConnectToggled(t.save)
	t.github.ConnectToggled(func() { t.publisher.SetSensitive(t.github.Active()); t.save() })
	t.publisher.NotifyProperty("selected", t.save)

	// A workflow publisher is remote work: its progress may show while the job waits for it.
	t.out.remoteWhileRunning = func() bool {
		return slices.ContainsFunc(t.out.term.Runner.Record.Command, func(arg string) bool { return strings.HasPrefix(arg, "--workflow=") })
	}
	term := t.out.term
	term.OnStarted = func() {
		t.out.started()
		t.button.SetSensitive(false)
		t.out.jobs.Sync()
	}
	term.OnRestored = t.restored
	term.OnFinished = func(code int, record daemon.Record) {
		t.button.SetSensitive(true)
		if code != 0 || record.Stopped {
			t.out.failed()
		}
		t.Refresh()
	}
	term.OnError = func(string) { t.out.failed() }
	t.Refresh()
	return t
}

// Refresh reads the latest tag, reattaches the release job and re-queries GitHub.
func (t *GenericReleaseTab) Refresh() {
	t.readTags()
	t.out.term.Attach()
	t.out.jobs.Sync()
}

// readTags shows the highest tag and suggests the next patch for an empty tag field.
func (t *GenericReleaseTab) readTags() {
	t.latestTag = ""
	if tags := git.Tags(t.repo); len(tags) > 0 {
		t.latestTag = tags[0]
	}
	latest := t.latestTag
	if latest == "" {
		latest = "None"
	}
	t.latest.SetText("Latest tag: " + latest)
	if t.tag.Text() == "" {
		t.suggest("patch")
	}
}

// suggest bumps an exact (v)X.Y.Z latest tag, keeping its prefix and zeroing the lower parts.
func (t *GenericReleaseTab) suggest(bump string) {
	m := exactVersion.FindStringSubmatch(t.latestTag)
	if m == nil {
		if t.latestTag == "" {
			t.tag.SetText("v0.1.0")
		}
		return
	}
	parts := [3]int{}
	for i := range parts {
		parts[i], _ = strconv.Atoi(m[i+2])
	}
	index := map[string]int{"major": 0, "minor": 1, "patch": 2}[bump]
	parts[index]++
	for i := index + 1; i < 3; i++ {
		parts[i] = 0
	}
	t.tag.SetText(fmt.Sprintf("%s%d.%d.%d", m[1], parts[0], parts[1], parts[2]))
}

// workflow is the chosen publisher's workflow file, or "" for the GitHub CLI.
func (t *GenericReleaseTab) workflow() string {
	if i := int(t.publisher.Selected()); i < len(t.workflows) {
		return t.workflows[i].File
	}
	return ""
}

func (t *GenericReleaseTab) save() {
	buffer := t.notes.Buffer()
	config.Set(t.stateKey, releaseForm{Tag: t.tag.Text(), Target: t.target.Text(),
		Notes: buffer.Text(buffer.StartIter(), buffer.EndIter(), false), Push: t.push.Active(),
		GitHub: t.github.Active(), Workflow: t.workflow()})
}

func (t *GenericReleaseTab) run() {
	if t.out.term.Runner.Running() {
		return
	}
	tag := strings.TrimSpace(t.tag.Text())
	target := strings.TrimSpace(t.target.Text())
	if target == "" {
		target = "HEAD"
	}
	if _, err := git.Run(t.repo, "check-ref-format", "refs/tags/"+tag); tag == "" || strings.HasPrefix(tag, "-") || err != nil {
		message := "Enter a valid Git tag."
		if err != nil && !strings.HasPrefix(err.Error(), "exit status") {
			message = err.Error()
		}
		alert(t.Root, "Invalid tag", message)
		return
	}
	commit, err := git.Run(t.repo, "rev-parse", "--verify", "--end-of-options", target+"^{commit}")
	if err != nil {
		alert(t.Root, "Invalid commit", err.Error())
		return
	}
	buffer := t.notes.Buffer()
	notes := strings.TrimSpace(buffer.Text(buffer.StartIter(), buffer.EndIter(), false))
	push, github := t.push.Active(), t.github.Active()
	workflow, publisher := "", "None"
	if github {
		workflow = t.workflow()
		publisher = "GitHub CLI"
		if workflow != "" {
			publisher = "Workflow: " + t.workflows[t.publisher.Selected()].Name
		}
	}
	preview := notes
	if preview == "" {
		preview = tag
	}
	details := []detail{{"Repository", t.repo}, {"Tag", tag}, {"Commit", commit}, {"Push tag", yesNo(push)},
		{"Create GitHub release", yesNo(github)}, {"Publisher", publisher}}
	confirm(t.Root, "Release "+tag, details, "Release notes", preview, "Release", func() {
		self, _ := os.Executable()
		command := []string{self, "release", "--tag=" + tag, "--target=" + commit, "--notes=" + notes}
		if push {
			command = append(command, "--push")
		}
		if github {
			command = append(command, "--github")
		}
		if workflow != "" {
			command = append(command, "--workflow="+workflow)
		}
		t.out.term.Clear()
		if push || github {
			t.out.jobs.Follow(tag)
		}
		t.out.term.Runner.Start(command, nil)
	})
}

// restored points the jobs view at a job's tag when it pushed or published one, once per job.
func (t *GenericReleaseTab) restored(record daemon.Record, running, _ bool) {
	t.button.SetSensitive(!running)
	if path := t.out.term.Runner.Path; path != t.followed {
		t.followed = path
		tag := ""
		for _, arg := range record.Command {
			if value, ok := strings.CutPrefix(arg, "--tag="); ok {
				tag = value
			}
		}
		if tag != "" && (slices.Contains(record.Command, "--push") || slices.Contains(record.Command, "--github")) {
			t.out.jobs.Follow(tag)
		}
	}
	switch {
	case running:
		t.out.restored(true)
	case record.Stopped || record.ExitCode == nil || *record.ExitCode != 0:
		t.out.failed()
	}
}
