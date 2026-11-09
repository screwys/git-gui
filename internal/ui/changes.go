package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/git"
)

const commitPage = 200

// ChangesTab reviews a work tree read-only: its branch against a base, its uncommitted
// changes, or one commit of its history.
type ChangesTab struct {
	Root     *gtk.Box
	repo     string
	worktree *gtk.DropDown
	trees    []git.Worktree
	treeList *gtk.StringList
	scope    *gtk.DropDown
	options  *gtk.Box
	base     *gtk.Entry
	refs     *gtk.StringList
	refPop   *gtk.Popover
	include  *gtk.CheckButton
	history  *gtk.Box
	commits  []git.Commit
	logList  *gtk.StringList
	logPick  *gtk.SingleSelection
	more     *gtk.Button
	files    *FileList
	diff     *DiffView
	status   *gtk.Label

	path, shownScope string // what is loaded
	limit            int
	updating         bool // the tab is changing its own controls

	// The comparison on screen; ok is false when it failed.
	ok          bool
	left, right string
	listed      []git.File
	preview     []string
	shown       [2]string // title and text in the viewer, so a refresh keeps its scroll
	loadedBase  string
}

// NewChangesTab builds the tab for repo.
func NewChangesTab(repo string) *ChangesTab {
	c := &ChangesTab{repo: repo, limit: commitPage, files: NewFileList(false, ""), diff: NewDiffView(repo)}
	c.diff.ReadOnly = true
	c.diff.updateControls()
	c.Root = gtk.NewBox(gtk.OrientationVertical, 6)
	c.Root.SetMarginStart(6)
	c.Root.SetMarginEnd(6)
	c.Root.SetMarginTop(6)
	c.Root.SetMarginBottom(6)

	controls := gtk.NewBox(gtk.OrientationHorizontal, 6)
	c.treeList = gtk.NewStringList(nil)
	c.worktree = gtk.NewDropDown(c.treeList, nil)
	// Bare, prunable and missing work trees are listed but cannot be picked.
	c.worktree.SetFactory(labelFactory(func(item *gtk.ListItem, label *gtk.Label) {
		label.SetText(c.treeList.String(item.Position()))
		if i := int(item.Position()); i < len(c.trees) {
			usable := usableTree(c.trees[i])
			label.SetTooltipText(c.trees[i].Path)
			label.SetSensitive(usable)
			item.SetActivatable(usable)
			item.SetSelectable(usable)
		}
	}))
	c.worktree.SetSizeRequest(200, -1)
	c.worktree.SetHExpand(true)
	c.scope = gtk.NewDropDownFromStrings([]string{"Branch", "Working tree", "Commit"})
	refresh := gtk.NewButtonWithLabel("Refresh")
	controls.Append(gtk.NewLabel("Worktree"))
	controls.Append(c.worktree)
	controls.Append(gtk.NewLabel("View"))
	controls.Append(c.scope)
	controls.Append(refresh)
	c.Root.Append(controls)

	c.options = gtk.NewBox(gtk.OrientationHorizontal, 6)
	c.base = gtk.NewEntry()
	c.base.SetSizeRequest(180, -1)
	c.base.SetHExpand(true)
	refs := gtk.NewMenuButton()
	c.refPop = c.refPicker()
	refs.SetPopover(c.refPop)
	baseField := gtk.NewBox(gtk.OrientationHorizontal, 0)
	baseField.AddCSSClass("linked")
	baseField.SetHExpand(true)
	baseField.Append(c.base)
	baseField.Append(refs)
	c.include = gtk.NewCheckButtonWithLabel("Include uncommitted")
	c.include.SetActive(true)
	all := gtk.NewButtonWithLabel("All changes")
	c.options.Append(gtk.NewLabel("Base"))
	c.options.Append(baseField)
	c.options.Append(c.include)
	c.options.Append(all)
	c.Root.Append(c.options)

	left := gtk.NewBox(gtk.OrientationVertical, 6)
	left.SetMarginEnd(6)
	c.history = gtk.NewBox(gtk.OrientationVertical, 6)
	c.history.SetVExpand(true)
	c.logList = gtk.NewStringList(nil)
	c.logPick = gtk.NewSingleSelection(c.logList)
	log := gtk.NewColumnView(c.logPick)
	// The subject takes the remaining width and is shortened to fit, with the full text as tooltip.
	column := func(title string, subject bool, text func(git.Commit) string) {
		col := gtk.NewColumnViewColumn(title, labelFactory(func(item *gtk.ListItem, label *gtk.Label) {
			if i := int(item.Position()); i < len(c.commits) {
				label.SetText(text(c.commits[i]))
				if subject {
					label.SetEllipsize(3) // end
					label.SetTooltipText(c.commits[i].Subject)
				}
			}
		}))
		col.SetExpand(subject)
		col.SetResizable(true)
		log.AppendColumn(col)
	}
	column("Commit", false, func(commit git.Commit) string { return commit.Short })
	column("Date", false, func(commit git.Commit) string { return commit.Date })
	column("Subject", true, func(commit git.Commit) string { return commit.Subject })
	logScroll := gtk.NewScrolledWindow()
	logScroll.SetChild(log)
	logScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	logScroll.SetVExpand(true)
	c.more = gtk.NewButtonWithLabel("Load more commits")
	c.history.Append(logScroll)
	c.history.Append(c.more)
	left.Append(c.history)
	c.files.Root.SetVExpand(true)
	left.Append(c.files.Root)
	panes := gtk.NewPaned(gtk.OrientationHorizontal)
	panes.SetStartChild(left)
	panes.SetEndChild(c.diff.Root)
	panes.SetShrinkStartChild(false)
	panes.SetShrinkEndChild(false)
	panes.SetPosition(420)
	panes.SetVExpand(true)
	c.Root.Append(panes)
	c.status = gtk.NewLabel("")
	c.status.SetXAlign(0)
	c.status.SetWrap(true)
	c.Root.Append(c.status)

	c.worktree.NotifyProperty("selected", c.changed(c.Refresh))
	c.scope.NotifyProperty("selected", c.changed(c.Refresh))
	refresh.ConnectClicked(c.Refresh)
	c.base.ConnectActivate(c.applyBase)
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(c.applyBase)
	c.base.AddController(focus)
	c.include.ConnectToggled(c.changed(c.load))
	all.ConnectClicked(func() {
		c.files.ClearSelection()
		c.show(nil)
	})
	c.more.ConnectClicked(func() {
		c.limit += commitPage
		c.Refresh()
	})
	c.logPick.ConnectSelectionChanged(func(uint, uint) {
		if !c.updating {
			c.preview = nil
			c.load()
		}
	})
	c.files.OnActivate = func(path string) { c.show([]string{path}) }
	c.files.OnView = c.show
	c.setScopeControls()
	return c
}

// changed runs f for user changes only, not for the tab filling its own controls.
func (c *ChangesTab) changed(f func()) func() {
	return func() {
		if !c.updating {
			f()
		}
	}
}

func usableTree(tree git.Worktree) bool {
	info, err := os.Stat(tree.Path)
	return !tree.Bare && !tree.Prunable && err == nil && info.IsDir()
}

func (c *ChangesTab) scopeName() string {
	return []string{"Branch", "Working tree", "Commit"}[min(int(c.scope.Selected()), 2)]
}

func (c *ChangesTab) setScopeControls() {
	c.options.SetVisible(c.scopeName() == "Branch")
	c.history.SetVisible(c.scopeName() == "Commit")
}

// refPicker lists every branch, remote branch and tag to pick as the base.
func (c *ChangesTab) refPicker() *gtk.Popover {
	c.refs = gtk.NewStringList(nil)
	view := gtk.NewListView(gtk.NewNoSelection(c.refs), labelFactory(func(item *gtk.ListItem, label *gtk.Label) {
		label.SetText(c.refs.String(item.Position()))
	}))
	view.SetSingleClickActivate(true)
	view.ConnectActivate(func(position uint) {
		c.base.SetText(c.refs.String(position))
		c.refPop.Popdown()
		c.applyBase()
	})
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(view)
	scroll.SetMaxContentHeight(360)
	scroll.SetMinContentWidth(240)
	scroll.SetPropagateNaturalHeight(true)
	popover := gtk.NewPopover()
	popover.SetChild(scroll)
	return popover
}

// applyBase compares against a typed or picked base once it is complete.
func (c *ChangesTab) applyBase() {
	if strings.TrimSpace(c.base.Text()) != c.loadedBase {
		c.load()
	}
}

// Refresh reloads the work trees, refs and history, then the comparison.
func (c *ChangesTab) Refresh() {
	c.setScopeControls()
	want := c.repo
	if i := int(c.worktree.Selected()); i < len(c.trees) {
		want = c.trees[i].Path
	}
	c.updating = true
	c.trees = git.Worktrees(c.repo)
	labels := make([]string, len(c.trees))
	index := -1
	for i, tree := range c.trees {
		head := tree.Branch
		if head == "" {
			head = tree.Head[:min(8, len(tree.Head))]
		}
		if head == "" {
			head = "bare"
		}
		labels[i] = filepath.Base(tree.Path) + ": " + head
		if index < 0 && sameDir(tree.Path, want) {
			index = i
		}
	}
	for i, tree := range c.trees {
		if index < 0 && sameDir(tree.Path, c.repo) {
			index = i
		}
	}
	// Spliced every time: a work tree can become unusable without its label changing.
	c.treeList.Splice(0, c.treeList.NItems(), labels)
	if index >= 0 {
		c.worktree.SetSelected(uint(index))
	}
	c.updating = false
	if index < 0 {
		c.fail("No working tree available")
		return
	}
	path, scope := c.trees[index].Path, c.scopeName()
	changed := path != c.path
	if changed || scope != c.shownScope {
		c.preview = nil
	}
	c.shownScope = scope
	refs := git.Refs(path)
	setStrings(c.refs, refs)
	c.diff.Repo = path
	if changed {
		c.path = path
		c.base.SetText(defaultBase(path, refs))
		c.limit = commitPage
		c.commits = nil
		c.updating = true
		setStrings(c.logList, nil)
		c.updating = false
		c.files.SetFiles(nil, nil)
	}
	if scope == "Commit" && !c.loadHistory() {
		return
	}
	c.load()
}

// defaultBase is origin's copy of the configured base branch, else the local one, else what
// origin/HEAD points to.
func defaultBase(path string, refs []string) string {
	branch := appConfig.DefaultBaseBranch
	originHead, _ := git.Run(path, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	for _, ref := range []string{"origin/" + branch, branch, originHead} {
		if ref != "" && slices.Contains(refs, ref) {
			return ref
		}
	}
	return branch
}

// loadHistory lists the newest commits, keeping the selected one.
func (c *ChangesTab) loadHistory() bool {
	selected := ""
	if i := int(c.logPick.Selected()); i < len(c.commits) {
		selected = c.commits[i].Hash
	}
	commits, err := git.Log(c.path, c.limit)
	if err != nil && git.Head(c.path) != "" {
		c.fail(err.Error())
		return false
	}
	c.updating = true
	c.commits = commits
	hashes := make([]string, len(commits))
	index := 0
	for i, commit := range commits {
		hashes[i] = commit.Hash
		if commit.Hash == selected {
			index = i
		}
	}
	setStrings(c.logList, hashes)
	if len(commits) > 0 {
		c.logPick.SetSelected(uint(index))
	}
	c.updating = false
	c.more.SetSensitive(len(commits) == c.limit)
	return true
}

// load compares according to the view and shows the result.
func (c *ChangesTab) load() {
	var title string
	untracked := false
	switch c.scopeName() {
	case "Working tree":
		c.left, c.right, untracked, title = git.Head(c.path), git.WorkingTree, true, "Working tree"
		if c.left == "" {
			c.left = git.EmptyTree
		}
	case "Branch":
		base := strings.TrimSpace(c.base.Text())
		c.loadedBase = base
		baseCommit, err := git.Run(c.path, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
		if err != nil {
			c.fail(err.Error())
			return
		}
		head, err := git.Run(c.path, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
		if err != nil {
			c.fail(err.Error())
			return
		}
		if c.left, err = git.MergeBase(c.path, baseCommit, head); err != nil {
			c.fail(err.Error())
			return
		}
		c.right, title = head, base+" → "+git.CurrentBranch(c.path)
		if c.include.Active() {
			c.right, untracked = git.WorkingTree, true
			title += " + uncommitted"
		}
	default:
		i := int(c.logPick.Selected())
		if i >= len(c.commits) {
			c.ok = false
			c.listed = nil
			c.files.SetFiles(nil, nil)
			c.showMessage("Commit", "No commits")
			c.status.SetText("No commits")
			return
		}
		commit := c.commits[i]
		c.left, c.right, title = git.Parent(c.path, commit.Hash), commit.Hash, commit.Short+": "+commit.Subject
	}
	files, err := git.Compare(c.path, c.left, c.right, untracked)
	if err != nil {
		c.fail(err.Error())
		return
	}
	c.ok, c.listed = true, files
	c.files.SetFiles(files, nil)
	c.status.SetText(fmt.Sprintf("%s · %d files · Base %s", title, len(files), c.left[:min(8, len(c.left))]))
	c.status.SetTooltipText(c.path)
	var keep []string
	for _, path := range c.preview {
		if slices.ContainsFunc(files, func(f git.File) bool { return f.Path == path }) {
			keep = append(keep, path)
		}
	}
	c.show(keep)
}

// show diffs the given listed paths, or everything when there are none.
func (c *ChangesTab) show(paths []string) {
	if !c.ok {
		return
	}
	c.preview = paths
	chosen := c.listed
	if len(paths) > 0 {
		chosen = pick(c.listed, paths)
	}
	var want []git.File // nil diffs the whole comparison
	if len(paths) > 0 {
		want = chosen
	}
	text, err := git.Diff(c.path, c.left, c.right, want)
	if err != nil {
		c.fail(err.Error())
		return
	}
	title := "Selected changes"
	switch len(paths) {
	case 0:
		title = "All changes"
	case 1:
		title = paths[0]
	}
	if strings.TrimSpace(text) == "" {
		c.showMessage(title, "No changes")
		return
	}
	if c.shown != [2]string{title, text} {
		adds, dels := totals(chosen)
		c.diff.ShowDiff(title, text, adds, dels, "")
		c.shown = [2]string{title, text}
	}
}

func (c *ChangesTab) showMessage(title, message string) {
	if c.shown != [2]string{title, message} {
		c.diff.ShowMessage(title, message)
		c.shown = [2]string{title, message}
	}
}

func (c *ChangesTab) fail(message string) {
	c.ok = false
	c.listed = nil
	c.files.SetFiles(nil, nil)
	c.showMessage("Diff unavailable", "Diff unavailable")
	c.status.SetText(message)
}

// setStrings replaces a list's strings only when they changed, so a periodic refresh keeps
// the list's scroll position and open popups.
func setStrings(list *gtk.StringList, values []string) {
	n := list.NItems()
	if int(n) == len(values) {
		same := true
		for i, v := range values {
			same = same && list.String(uint(i)) == v
		}
		if same {
			return
		}
	}
	list.Splice(0, n, values)
}

// labelFactory makes list rows of one label; bind fills the label for its item. Column view
// cells are list items too.
func labelFactory(bind func(item *gtk.ListItem, label *gtk.Label)) *gtk.ListItemFactory {
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(object *glib.Object) {
		label := gtk.NewLabel("")
		label.SetXAlign(0)
		object.CastType(gtk.GTypeListItem).(*gtk.ListItem).SetChild(label)
	})
	factory.ConnectBind(func(object *glib.Object) {
		item := object.CastType(gtk.GTypeListItem).(*gtk.ListItem)
		bind(item, item.Child().(*gtk.Label))
	})
	return &factory.ListItemFactory
}
