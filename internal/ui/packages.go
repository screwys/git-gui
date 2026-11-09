package ui

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/git"
	"github.com/screwys/git-gui/internal/packages"
)

// PackagesTab adds and removes packages in the repository's package lists.
type PackagesTab struct {
	Root      *gtk.Box
	repo      string
	nocblue   bool
	onSubject func(subject string)

	kind      *gtk.DropDown
	source    *gtk.DropDown
	labels    *gtk.StringList
	sources   []packages.Source // every source of the repository
	choices   []packages.Source // the current kind's sources, as listed in the picker
	filter    *gtk.SearchEntry
	list      *gtk.ListBox
	rows      []packageRow
	name      *gtk.Entry
	nameFocus *gtk.EventControllerFocus
	suggest   *suggestions
	add       *gtk.Button
	remove    *gtk.Button
	lookup    *gtk.Label
	diff      *DiffView
	status    *gtk.Label

	text     string           // the loaded source's content, to detect edits made elsewhere
	loaded   *packages.Source // the source the list shows
	updating bool             // the tab is setting its own controls
}

// packageRow is one entry; duplicates are told apart by occurrence so checks survive a reload.
type packageRow struct {
	row        *gtk.ListBoxRow
	check      *gtk.CheckButton
	name       string
	occurrence int
	index      int
}

// NewPackagesTab builds the tab for repo. nocblue also edits each manifest's runtime copy;
// onSubject receives a commit subject after every saved edit.
func NewPackagesTab(repo string, nocblue bool, onSubject func(subject string)) *PackagesTab {
	t := &PackagesTab{repo: repo, nocblue: nocblue, onSubject: onSubject, diff: NewDiffView("")}
	t.diff.ReadOnly = true
	t.diff.updateControls()
	t.Root = gtk.NewBox(gtk.OrientationVertical, 6)
	t.Root.SetMarginStart(6)
	t.Root.SetMarginEnd(6)
	t.Root.SetMarginTop(6)
	t.Root.SetMarginBottom(6)

	top := gtk.NewBox(gtk.OrientationHorizontal, 6)
	t.kind = gtk.NewDropDownFromStrings(packages.Kinds)
	t.labels = gtk.NewStringList(nil)
	t.source = gtk.NewDropDown(t.labels, nil)
	t.source.SetFactory(labelFactory(func(item *gtk.ListItem, label *gtk.Label) {
		label.SetText(t.labels.String(item.Position()))
		label.SetEllipsize(3) // end
	}))
	t.source.SetHExpand(true)
	addSource := gtk.NewButtonWithLabel("Add source")
	reload := gtk.NewButtonWithLabel("Reload")
	top.Append(t.kind)
	top.Append(t.source)
	top.Append(addSource)
	top.Append(reload)
	t.Root.Append(top)

	left := gtk.NewBox(gtk.OrientationVertical, 6)
	left.SetMarginEnd(6)
	t.filter = gtk.NewSearchEntry()
	t.filter.SetPlaceholderText("Filter packages")
	t.list = gtk.NewListBox()
	t.list.SetSelectionMode(gtk.SelectionNone)
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(t.list)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	scroll.AddCSSClass("card")
	addRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	t.name = gtk.NewEntry()
	t.name.SetHExpand(true)
	t.name.SetPlaceholderText("Package")
	t.nameFocus = gtk.NewEventControllerFocus()
	t.name.AddController(t.nameFocus)
	t.suggest = newSuggestions(t.name)
	t.add = gtk.NewButtonFromIconName("list-add-symbolic")
	t.add.SetTooltipText("Add package")
	t.remove = gtk.NewButtonFromIconName("user-trash-symbolic")
	t.remove.SetTooltipText("Remove selected packages")
	addRow.Append(t.name)
	addRow.Append(t.add)
	addRow.Append(t.remove)
	t.lookup = gtk.NewLabel("")
	t.lookup.SetXAlign(0)
	t.lookup.SetWrap(true)
	left.Append(t.filter)
	left.Append(scroll)
	left.Append(addRow)
	left.Append(t.lookup)
	panes := gtk.NewPaned(gtk.OrientationHorizontal)
	panes.SetStartChild(left)
	panes.SetEndChild(t.diff.Root)
	panes.SetShrinkStartChild(false)
	panes.SetShrinkEndChild(false)
	panes.SetPosition(400)
	panes.SetVExpand(true)
	t.Root.Append(panes)
	t.status = gtk.NewLabel("")
	t.status.SetXAlign(0)
	t.status.SetWrap(true)
	t.Root.Append(t.status)

	t.kind.NotifyProperty("selected", t.showSources)
	t.source.NotifyProperty("selected", func() {
		if !t.updating {
			t.loadSource()
		}
	})
	addSource.ConnectClicked(t.addSource)
	reload.ConnectClicked(func() {
		t.Refresh()
		if t.rpm() {
			rpmCatalog.load(true)
		}
	})
	t.filter.ConnectSearchChanged(t.applyFilter)
	t.name.ConnectActivate(t.addPackage)
	t.name.ConnectChanged(func() {
		t.updateLookup()
		if t.updating {
			return
		}
		t.completeRPM()
		t.suggest.show(t.rpm(), strings.TrimSpace(t.name.Text()))
	})
	t.nameFocus.ConnectLeave(t.suggest.hide)
	t.suggest.onPick = func(name string) {
		t.updating = true
		t.name.SetText(name)
		t.name.SetPosition(-1)
		t.updating = false
	}
	t.add.ConnectClicked(t.addPackage)
	t.remove.ConnectClicked(func() {
		var indexes []int
		for _, r := range t.rows {
			if r.check.Active() {
				indexes = append(indexes, r.index)
			}
		}
		if len(indexes) > 0 {
			t.save("", indexes)
		}
	})
	rpmCatalog.listeners = append(rpmCatalog.listeners, t.catalogChanged)
	t.Refresh()
	return t
}

// Refresh finds the sources again and reloads the chosen one.
func (t *PackagesTab) Refresh() {
	var errs []string
	t.sources, errs = packages.Discover(t.repo, appConfig.PackageSources[realPath(t.repo)])
	t.showSources()
	if len(errs) > 0 {
		t.status.SetText("Could not read package sources.")
		t.status.SetTooltipText(strings.Join(errs, "\n"))
	}
}

func realPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

func (t *PackagesTab) current() (packages.Source, bool) {
	if i := int(t.source.Selected()); i < len(t.choices) {
		return t.choices[i], true
	}
	return packages.Source{}, false
}

func (t *PackagesTab) rpm() bool {
	source, ok := t.current()
	return ok && source.Kind == "RPM"
}

// showSources lists the current kind's sources, keeping the chosen one.
func (t *PackagesTab) showSources() {
	previous, had := t.current()
	kind := packages.Kinds[min(int(t.kind.Selected()), len(packages.Kinds)-1)]
	t.choices = nil
	var labels []string
	index := 0
	for _, s := range t.sources {
		if s.Kind == kind {
			if had && s.Equal(previous) {
				index = len(t.choices)
			}
			t.choices = append(t.choices, s)
			labels = append(labels, s.Label())
		}
	}
	t.updating = true
	t.labels.Splice(0, t.labels.NItems(), labels)
	if len(labels) > 0 {
		t.source.SetSelected(uint(index))
	}
	t.updating = false
	t.loadSource()
}

// loadSource shows the chosen source's entries and its diff. Checked entries survive a
// reload of the same source.
func (t *PackagesTab) loadSource() {
	source, ok := t.current()
	type identity struct {
		name       string
		occurrence int
	}
	checked := map[identity]bool{}
	if ok && t.loaded != nil && t.loaded.Equal(source) {
		for _, r := range t.rows {
			if r.check.Active() {
				checked[identity{r.name, r.occurrence}] = true
			}
		}
	}
	t.list.RemoveAll()
	t.rows = nil
	t.status.SetText("")
	t.status.SetTooltipText("")
	t.loaded = nil
	ready := false
	defer func() {
		// Set once at the end, so a focused name field keeps its focus across a reload.
		t.add.SetSensitive(ready)
		t.name.SetSensitive(ready)
	}()
	t.suggest.hide()
	t.updateLookup()
	t.updateRemove()
	if !ok {
		t.diff.ShowDiff("", "", 0, 0, "")
		t.status.SetText("Add a package source.")
		return
	}
	text, err := packages.ReadText(filepath.Join(t.repo, source.Path))
	if err != nil {
		t.status.SetText(err.Error())
		return
	}
	entries, err := packages.Entries(source, text)
	if err != nil {
		t.status.SetText(err.Error())
		return
	}
	t.loaded, t.text, ready = &source, text, true
	seen := map[string]int{}
	for i, e := range entries {
		r := packageRow{check: gtk.NewCheckButtonWithLabel(e.Name), name: e.Name, occurrence: seen[e.Name], index: i}
		seen[e.Name]++
		r.check.SetActive(checked[identity{r.name, r.occurrence}])
		r.check.SetTooltipText(e.Name)
		r.check.ConnectToggled(t.updateRemove)
		r.row = gtk.NewListBoxRow()
		r.row.SetChild(r.check)
		t.list.Append(r.row)
		t.rows = append(t.rows, r)
	}
	if source.Kind == "Nix" {
		t.name.SetPlaceholderText("Nix reference")
	} else {
		t.name.SetPlaceholderText("Package")
	}
	t.applyFilter()
	t.showDiff(source)
	t.updateRemove()
	t.completeRPM()
}

func (t *PackagesTab) applyFilter() {
	query := strings.ToLower(t.filter.Text())
	for _, r := range t.rows {
		r.row.SetVisible(strings.Contains(strings.ToLower(r.name), query))
	}
}

func (t *PackagesTab) updateRemove() {
	n := 0
	for _, r := range t.rows {
		if r.check.Active() {
			n++
		}
	}
	t.remove.SetSensitive(n > 0)
	if n > 0 {
		t.remove.SetTooltipText(fmt.Sprintf("Remove %d selected packages", n))
	} else {
		t.remove.SetTooltipText("Remove selected packages")
	}
}

// showDiff shows the uncommitted changes of the source and its mirror.
func (t *PackagesTab) showDiff(source packages.Source) {
	paths := append([]string{source.Path}, packages.Mirrors(t.repo, source, t.nocblue)...)
	base := "HEAD"
	if git.Head(t.repo) == "" {
		base = git.EmptyTree
	}
	files, err := git.Compare(t.repo, base, git.WorkingTree, true)
	if err != nil {
		t.diff.ShowMessage(source.Path, err.Error())
		return
	}
	chosen := pick(files, paths)
	text := ""
	if len(chosen) > 0 {
		if text, err = git.Diff(t.repo, base, git.WorkingTree, chosen); err != nil {
			t.diff.ShowMessage(source.Path, err.Error())
			return
		}
	}
	adds, dels := totals(chosen)
	t.diff.ShowDiff(source.Path, text, adds, dels, "")
}

func (t *PackagesTab) addPackage() {
	name := strings.TrimSpace(t.name.Text())
	source, ok := t.current()
	if name == "" || !ok || t.loaded == nil {
		return
	}
	if strings.ContainsAny(name, "\r\n") {
		t.status.SetText("Enter one package at a time.")
		return
	}
	entries, _ := packages.Entries(source, t.text)
	if slices.ContainsFunc(entries, func(e packages.Entry) bool { return e.Name == packages.EntryName(source, name) }) {
		t.status.SetText("Package already listed.")
		return
	}
	if t.save(name, nil) {
		t.name.SetText("")
	}
}

// save writes an edit, then reloads and hands the commit tab a subject.
func (t *PackagesTab) save(add string, remove []int) bool {
	source, ok := t.current()
	if !ok {
		return false
	}
	err := packages.Save(t.repo, source, t.text, add, remove, packages.Mirrors(t.repo, source, t.nocblue))
	if errors.Is(err, packages.ErrChanged) {
		t.loadSource()
		t.status.SetText("Source changed. Select the package again.")
		return false
	}
	if err != nil {
		t.status.SetText(err.Error())
		return false
	}
	if len(remove) > 0 {
		// The removed rows' identities now name other entries.
		for _, r := range t.rows {
			r.check.SetActive(false)
		}
	}
	t.loadSource()
	if t.onSubject != nil {
		t.onSubject(packages.CommitSubject(t.repo, t.sources))
	}
	t.status.SetText("Saved.")
	return true
}

// addSource adds a file the discovery does not know as a source of the current kind.
func (t *PackagesTab) addSource() {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Package source")
	dialog.SetInitialFolder(gio.NewFileForPath(t.repo))
	known := gtk.NewFileFilter()
	known.SetName("Package files")
	for _, pattern := range []string{"*.txt", "*.yml", "*.yaml", "Brewfile"} {
		known.AddPattern(pattern)
	}
	any := gtk.NewFileFilter()
	any.SetName("All files")
	any.AddPattern("*")
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(known.Object)
	filters.Append(any.Object)
	dialog.SetFilters(filters)
	dialog.Open(context(), windowOf(t.Root), func(result gio.AsyncResulter) {
		file, err := dialog.OpenFinish(result)
		if err != nil || file == nil {
			return
		}
		repo := realPath(t.repo)
		relative, err := filepath.Rel(repo, realPath(file.Path()))
		if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
			alert(t.Root, "Package source", "Select a file in this repository.")
			return
		}
		relative = filepath.ToSlash(relative)
		kind := packages.Kinds[min(int(t.kind.Selected()), len(packages.Kinds)-1)]
		text, err := packages.ReadText(file.Path())
		var found []packages.Source
		if err == nil {
			found, err = packages.SourcesForFile(kind, relative, text)
		}
		if err == nil && len(found) == 0 {
			err = errors.New("No package lists found in this recipe.")
		}
		if err != nil {
			alert(t.Root, "Package source", err.Error())
			return
		}
		configured := slices.DeleteFunc(slices.Clone(appConfig.PackageSources[repo]), func(s config.Source) bool { return s.Path == relative })
		appConfig.PackageSources[repo] = append(configured, config.Source{Kind: kind, Path: relative})
		_ = appConfig.Save()
		t.Refresh()
		for i, s := range t.choices {
			if s.Path == relative {
				t.source.SetSelected(uint(i))
				break
			}
		}
	})
}

// completeRPM starts the session's DNF lookup once a name is typed in an RPM source.
func (t *PackagesTab) completeRPM() {
	if t.rpm() && strings.TrimSpace(t.name.Text()) != "" {
		rpmCatalog.load(false)
	}
}

func (t *PackagesTab) catalogChanged() {
	t.updateLookup()
	if t.nameFocus.ContainsFocus() {
		t.suggest.show(t.rpm(), strings.TrimSpace(t.name.Text()))
	}
}

// updateLookup says whether the typed RPM name is in the cached DNF metadata.
func (t *PackagesTab) updateLookup() {
	name := strings.TrimSpace(t.name.Text())
	t.lookup.SetText("")
	t.lookup.SetTooltipText("")
	if name == "" || !t.rpm() {
		return
	}
	switch c := rpmCatalog; {
	case c.running:
		t.lookup.SetText("Reading cached DNF packages...")
	case c.err != "":
		t.lookup.SetText(c.err)
		t.lookup.SetTooltipText(c.details)
	case c.loaded && c.names[name]:
		t.lookup.SetText("Found in cached DNF metadata.")
	case c.loaded:
		t.lookup.SetText("Not found in cached DNF metadata.")
	}
}

// rpmCatalog is the package names in the cached DNF metadata. It is read once per app session,
// on first use, never downloads metadata, and every Packages tab shares it.
var rpmCatalog = &rpmNames{}

type rpmNames struct {
	names                      map[string]bool
	sorted, lower              []string
	loaded, attempted, running bool
	err, details               string
	listeners                  []func()
}

func (r *rpmNames) load(refresh bool) {
	if r.running || (r.attempted && !refresh) {
		return
	}
	r.attempted, r.running, r.err, r.details = true, true, "", ""
	r.changed()
	go func() {
		cmd := exec.Command("dnf", "--cacheonly", "--quiet", "repoquery", "--available", `--queryformat=%{name}\n`)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		glib.IdleAdd(func() {
			r.running = false
			r.details = strings.ToValidUTF8(strings.TrimSpace(stderr.String()), "�")
			var exit *exec.ExitError
			switch {
			case errors.As(err, &exit):
				r.err = "Cached DNF lookup failed."
			case err != nil:
				r.err, r.details = "Could not run DNF.", err.Error()
			default:
				r.names = map[string]bool{}
				for _, line := range strings.Split(strings.ToValidUTF8(stdout.String(), "�"), "\n") {
					if line = strings.TrimSpace(line); line != "" {
						r.names[line] = true
					}
				}
				r.sorted = slices.Sorted(maps.Keys(r.names))
				r.lower = make([]string, len(r.sorted))
				for i, name := range r.sorted {
					r.lower[i] = strings.ToLower(name)
				}
				r.loaded = true
				if len(r.names) == 0 {
					r.err = "No cached DNF packages."
				}
			}
			r.changed()
		})
	}()
}

func (r *rpmNames) changed() {
	for _, f := range r.listeners {
		f()
	}
}

// suggestions pops up the catalog names containing the typed text below the name field. Up and
// Down move through them, Enter or a click takes one, Escape closes the list.
type suggestions struct {
	entry   *gtk.Entry
	popover *gtk.Popover
	names   *gtk.StringList
	pick    *gtk.SingleSelection
	view    *gtk.ListView
	onPick  func(name string)
}

// suggestionLimit keeps a short query from filling the list with most of the catalog.
const suggestionLimit = 200

func newSuggestions(entry *gtk.Entry) *suggestions {
	s := &suggestions{entry: entry, names: gtk.NewStringList(nil)}
	s.pick = gtk.NewSingleSelection(s.names)
	s.pick.SetAutoselect(false)
	s.pick.SetCanUnselect(true)
	s.view = gtk.NewListView(s.pick, labelFactory(func(item *gtk.ListItem, label *gtk.Label) {
		label.SetText(s.names.String(item.Position()))
	}))
	s.view.SetSingleClickActivate(true)
	s.view.ConnectActivate(func(position uint) { s.take(position) })
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(s.view)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetMaxContentHeight(10 * 30)
	scroll.SetPropagateNaturalHeight(true)
	s.popover = gtk.NewPopover()
	s.popover.SetChild(scroll)
	s.popover.SetParent(entry)
	s.popover.SetPosition(gtk.PosBottom)
	s.popover.SetHasArrow(false)
	// Typing stays in the field while the list is open.
	s.popover.SetAutohide(false)
	s.popover.SetCanFocus(false)
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(s.key)
	entry.AddController(keys)
	return s
}

// show lists the matches for query when the RPM catalog is ready, else closes the list.
func (s *suggestions) show(rpm bool, query string) {
	c := rpmCatalog
	if !rpm || query == "" || !c.loaded || c.err != "" {
		s.hide()
		return
	}
	query = strings.ToLower(query)
	var matches []string
	for i, lower := range c.lower {
		if strings.Contains(lower, query) {
			if matches = append(matches, c.sorted[i]); len(matches) == suggestionLimit {
				break
			}
		}
	}
	if len(matches) == 0 {
		s.hide()
		return
	}
	s.names.Splice(0, s.names.NItems(), matches)
	s.pick.SetSelected(gtk.INVALID_LIST_POSITION)
	s.popover.SetSizeRequest(s.entry.Width(), -1)
	s.popover.Popup()
}

func (s *suggestions) hide() { s.popover.Popdown() }

func (s *suggestions) take(position uint) {
	if position < s.names.NItems() {
		s.onPick(s.names.String(position))
	}
	s.hide()
}

func (s *suggestions) key(keyval, _ uint, _ gdk.ModifierType) bool {
	if !s.popover.Visible() {
		return false
	}
	n, selected := s.names.NItems(), s.pick.Selected()
	switch keyval {
	case gdk.KEY_Down, gdk.KEY_Up:
		next := uint(0)
		switch {
		case keyval == gdk.KEY_Down && selected != gtk.INVALID_LIST_POSITION:
			next = min(selected+1, n-1)
		case keyval == gdk.KEY_Up && selected != gtk.INVALID_LIST_POSITION && selected > 0:
			next = selected - 1
		case keyval == gdk.KEY_Up:
			next = n - 1
		}
		s.pick.SetSelected(next)
		s.view.ScrollTo(next, gtk.ListScrollNone, nil)
		return true
	case gdk.KEY_Return, gdk.KEY_KP_Enter:
		if selected == gtk.INVALID_LIST_POSITION {
			s.hide()
			return false
		}
		s.take(selected)
		return true
	case gdk.KEY_Escape:
		s.hide()
		return true
	}
	return false
}

// windowOf is the window holding widget, the parent of file choosers.
func windowOf(widget gtk.Widgetter) *gtk.Window {
	if root := gtk.BaseWidget(widget).Root(); root != nil {
		if w, ok := root.CastType(gtk.GTypeWindow).(*gtk.Window); ok {
			return w
		}
	}
	return nil
}
