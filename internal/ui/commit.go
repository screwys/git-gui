package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

// CommitTab commits checked files and pushes them, rebasing onto origin first.
type CommitTab struct {
	Root     *gtk.Paned
	repo     string
	files    *FileList
	diff     *DiffView
	term     *Terminal
	summary  *gtk.Entry
	body     *gtk.TextView
	push     *gtk.CheckButton
	amend    *gtk.CheckButton
	skipHook *gtk.CheckButton
	working  []git.File
	subject  string // the last package-edit subject filled in
}

// NewCommitTab builds the tab for repo.
func NewCommitTab(repo string) *CommitTab {
	t := &CommitTab{repo: repo, files: NewFileList("Commit & Push", true), diff: NewDiffView(repo), term: NewTerminal(repo, "commit", "")}
	t.files.Button.SetTooltipText("Commit selected changes and push to origin")

	message := group("Commit Message")
	t.summary = gtk.NewEntry()
	t.summary.SetPlaceholderText("e.g. feed: add interactive media cards")
	t.body = gtk.NewTextView()
	t.body.SetWrapMode(gtk.WrapWordChar)
	bodyScroll := gtk.NewScrolledWindow()
	bodyScroll.SetChild(t.body)
	bodyScroll.SetMinContentHeight(60)
	bodyScroll.SetVExpand(true)
	bodyScroll.AddCSSClass("card")
	message.Append(row("Summary:", t.summary))
	message.Append(bodyScroll)
	message.SetVExpand(true)

	options := group("Options")
	t.push = gtk.NewCheckButtonWithLabel("Push to origin upon commit")
	t.push.SetActive(true)
	t.amend = gtk.NewCheckButtonWithLabel("Amend (--amend)")
	t.skipHook = gtk.NewCheckButtonWithLabel("Skip pre-push (--no-verify)")
	t.skipHook.SetTooltipText("Bypass git pre-push hook checks with --no-verify")
	options.Append(t.push)
	options.Append(t.amend)
	options.Append(t.skipHook)

	// The message and options share a pane below the file list, so a handle resizes the message.
	bottom := gtk.NewBox(gtk.OrientationVertical, 8)
	bottom.SetMarginTop(8)
	bottom.Append(message)
	bottom.Append(options)
	left := gtk.NewPaned(gtk.OrientationVertical)
	margins(left, 8)
	left.SetStartChild(t.files.Root)
	left.SetEndChild(bottom)
	left.SetShrinkStartChild(false)
	left.SetShrinkEndChild(false)
	left.SetResizeEndChild(false)

	right := gtk.NewPaned(gtk.OrientationVertical)
	margins(right, 8)
	right.SetStartChild(t.diff.Root)
	right.SetEndChild(t.term.Root)
	right.SetPosition(420)
	t.Root = split(left, right)

	t.files.OnActivate = func(path string) { t.showFiles([]string{path}) }
	t.files.OnView = func(paths []string) { t.showFiles(paths) }
	t.files.OnChecked = t.updateButton
	t.files.Button.ConnectClicked(t.run)
	t.push.ConnectToggled(t.updateButton)
	t.amend.ConnectToggled(func() {
		if t.amend.Active() && t.summary.Text() == "" {
			message, _ := git.Run(repo, "log", "-1", "--pretty=%B")
			title, rest, _ := strings.Cut(strings.TrimSpace(message), "\n")
			t.summary.SetText(title)
			t.body.Buffer().SetText(strings.TrimSpace(rest))
		}
		t.updateButton()
	})
	t.diff.OnSaved = func(path string) { t.Refresh(); t.showFiles([]string{path}) }
	t.term.OnStarted = t.updateButton
	t.term.OnRestored = func(daemon.Record, bool, bool) { t.updateButton() }
	t.term.OnFinished = func(code int, _ daemon.Record) {
		if code == 0 {
			t.summary.SetText("")
			t.body.Buffer().SetText("")
			t.amend.SetActive(false)
		}
		t.Refresh()
	}
	t.term.Attach()
	return t
}

// FillSubject puts a package edit's commit subject in Summary, unless the user wrote another.
func (t *CommitTab) FillSubject(subject string) {
	if text := t.summary.Text(); text == "" || text == t.subject {
		t.summary.SetText(subject)
	}
	t.subject = subject
	t.Refresh()
}

// Refresh reloads the working tree.
func (t *CommitTab) Refresh() {
	base := "HEAD"
	if git.Head(t.repo) == "" {
		base = git.EmptyTree
	}
	files, err := git.Compare(t.repo, base, git.WorkingTree, true)
	if err != nil {
		t.diff.ShowMessage("Status unavailable", err.Error())
		return
	}
	t.working = files
	t.files.SetFiles(files, nil)
	t.updateButton()
	switch current := t.diff.CurrentPath(); {
	case current != "" && t.listed(current):
		t.showFiles([]string{current})
	case len(t.files.Checked()) == 1:
		t.showFiles(t.files.Checked())
	case len(files) == 0:
		t.diff.ShowMessage("Clean working tree", "Clean working tree")
	default:
		t.showFiles(t.files.Checked())
	}
	t.term.Attach()
}

func (t *CommitTab) listed(path string) bool {
	for _, f := range t.working {
		if f.Path == path {
			return true
		}
	}
	return false
}

func (t *CommitTab) showFiles(paths []string) {
	chosen := pick(t.working, paths)
	base := "HEAD"
	if git.Head(t.repo) == "" {
		base = git.EmptyTree
	}
	text, err := git.Diff(t.repo, base, git.WorkingTree, chosen)
	if err != nil {
		t.diff.ShowMessage("Diff unavailable", err.Error())
		return
	}
	adds, dels := totals(chosen)
	switch {
	case len(chosen) == 1:
		t.diff.ShowDiff(chosen[0].Path, text, adds, dels, chosen[0].Path)
	case len(chosen) == 0:
		t.diff.ShowMessage("No changes selected", "")
	case len(chosen) == len(t.working):
		t.diff.ShowDiff(fmt.Sprintf("All Changes (%d files)", len(chosen)), text, adds, dels, "")
	default:
		t.diff.ShowDiff(fmt.Sprintf("%d files selected", len(chosen)), text, adds, dels, "")
	}
}

func (t *CommitTab) updateButton() {
	t.skipHook.SetSensitive(t.push.Active())
	label := "Commit"
	if t.push.Active() {
		label = "Push"
		if len(t.working) > 0 || t.amend.Active() {
			label = "Commit & Push"
		}
	}
	t.files.Button.SetLabel(label)
	t.files.Button.SetSensitive(!t.term.Runner.Running())
}

func (t *CommitTab) run() {
	parent := t.Root
	if t.term.Runner.Running() {
		alert(parent, "Running", "A git operation is already in progress.")
		return
	}
	checked := t.files.Checked()
	summary := strings.TrimSpace(t.summary.Text())
	buffer := t.body.Buffer()
	description := strings.TrimSpace(buffer.Text(buffer.StartIter(), buffer.EndIter(), false))
	amend, push := t.amend.Active(), t.push.Active()
	if len(checked) > 0 && summary == "" && !amend {
		alert(parent, "Missing Summary", "Please enter a commit summary or title.")
		return
	}
	if len(checked) == 0 && !amend && (len(t.working) > 0 || !push) {
		alert(parent, "No Files Selected", "Please select at least one file to commit.")
		return
	}
	branch := git.CurrentBranch(t.repo)
	var steps []string
	committing := len(checked) > 0 || amend
	if committing {
		if len(checked) > 0 {
			steps = append(steps, shellJoin(append([]string{"git", "add", "--"}, checked...)))
		} else {
			steps = append(steps, "git add -A")
		}
		commit := []string{"git", "commit"}
		if amend {
			commit = append(commit, "--amend")
		}
		if summary != "" {
			message := summary
			if description != "" {
				message += "\n\n" + description
			}
			commit = append(commit, "-m", message)
		} else {
			commit = append(commit, "--no-edit")
		}
		steps = append(steps, shellJoin(commit))
	}
	if push {
		self, _ := os.Executable()
		command := []string{self, "push", branch}
		if t.skipHook.Active() {
			command = append(command, "--skip-hook")
		}
		steps = append(steps, shellJoin(command))
	}
	action := t.files.Button.Label()
	title := summary
	switch {
	case title == "" && committing:
		title = "(keep existing)"
	case !committing:
		title = "Push existing commits"
	}
	files := fmt.Sprintf("%d of %d files", len(checked), len(t.working))
	if len(checked) == len(t.working) {
		files = fmt.Sprintf("All %d files", len(checked))
	}
	pushing := "No (local commit only)"
	if push {
		pushing = "Yes (origin/" + branch + ")"
	}
	details := []detail{{"Current Branch", branch}, {"Commit Title", title}, {"Files Included", files},
		{"Amend", yesNo(amend)}, {"Push to Origin", pushing}}
	if push {
		details = append(details, detail{"Skip Pre-push", yesNo(t.skipHook.Active())})
	}
	confirm(parent, filepath.Base(t.repo)+" "+action, details, "Extended Description:", description, action, func() {
		t.term.Clear()
		t.term.SetLabel("Executing " + action)
		t.term.Runner.Start([]string{"bash", "-c", strings.Join(steps, " && ")}, nil)
	})
}

func pick(files []git.File, paths []string) []git.File {
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	var chosen []git.File
	for _, f := range files {
		if want[f.Path] {
			chosen = append(chosen, f)
		}
	}
	return chosen
}

func totals(files []git.File) (int, int) {
	adds, dels := 0, 0
	for _, f := range files {
		adds += f.Adds
		dels += f.Dels
	}
	return adds, dels
}

func yesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

// group is a titled section of a form.
func group(title string) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	label := gtk.NewLabel(title)
	label.AddCSSClass("heading")
	label.SetXAlign(0)
	box.Append(label)
	return box
}

// row puts a label before a field.
func row(label string, field gtk.Widgetter) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	l := gtk.NewLabel(label)
	l.SetXAlign(0)
	box.Append(l)
	if w, ok := field.(interface{ SetHExpand(bool) }); ok {
		w.SetHExpand(true)
	}
	box.Append(field)
	return box
}
