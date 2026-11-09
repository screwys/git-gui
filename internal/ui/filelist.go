package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/git"
)

// FileList shows changed files with a checkbox on each selectable row, a select-all box and a
// file name filter. Checks stay until the user unticks them or the file leaves the list.
type FileList struct {
	Root     *gtk.Box
	Title    *gtk.Label
	Button   *gtk.Button // the tab's primary action; hidden when unused
	View     *gtk.Button
	list     *gtk.ListBox
	scroll   *gtk.ScrolledWindow
	all      *gtk.CheckButton
	filter   *gtk.SearchEntry
	count    *gtk.Label
	totals   *gtk.Label
	checkNew bool

	Files      []git.File
	selectable map[string]bool
	checked    map[string]bool
	checks     map[string]*gtk.CheckButton
	updating   bool

	OnActivate func(path string)    // a row was clicked
	OnView     func(paths []string) // View Diff
	OnChecked  func()               // the checked set changed
}

// NewFileList builds a list. With checkNew, new rows arrive checked while every row is checked.
func NewFileList(action string, checkNew bool) *FileList {
	f := &FileList{Root: gtk.NewBox(gtk.OrientationVertical, 4), list: gtk.NewListBox(), checkNew: checkNew,
		checked: map[string]bool{}, checks: map[string]*gtk.CheckButton{}, selectable: map[string]bool{}}
	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	f.Title = gtk.NewLabel("Changes (0)")
	f.Title.AddCSSClass("heading")
	f.count = gtk.NewLabel("")
	f.count.AddCSSClass("dim")
	f.totals = gtk.NewLabel("")
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	header.Append(f.Title)
	header.Append(f.count)
	header.Append(spacer)
	header.Append(f.totals)
	f.Root.Append(header)

	// The select-all box ticks or unticks the rows the filter shows.
	filterRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	filterRow.SetMarginStart(4)
	f.all = gtk.NewCheckButton()
	f.all.SetTooltipText("Select / Deselect All")
	f.all.ConnectToggled(func() {
		if f.updating {
			return
		}
		f.setVisible(f.all.Active())
	})
	f.filter = gtk.NewSearchEntry()
	f.filter.SetPlaceholderText("Filter files")
	f.filter.SetHExpand(true)
	f.filter.ConnectSearchChanged(func() {
		f.list.InvalidateFilter()
		f.syncAll()
	})
	filterRow.Append(f.all)
	filterRow.Append(f.filter)
	f.Root.Append(filterRow)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	f.Button = gtk.NewButtonWithLabel(action)
	f.Button.AddCSSClass("suggested-action")
	f.Button.SetVisible(action != "")
	f.View = gtk.NewButtonWithLabel("View Diff")
	f.View.ConnectClicked(func() {
		if f.OnView != nil {
			f.OnView(f.Checked())
		}
	})
	buttons.Append(f.Button)
	buttons.Append(f.View)
	f.Root.Append(buttons)
	f.list.SetSelectionMode(gtk.SelectionSingle)
	f.list.SetFilterFunc(func(row *gtk.ListBoxRow) bool {
		i := row.Index()
		return i < 0 || i >= len(f.Files) || f.shows(f.Files[i].Path)
	})
	f.list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if f.OnActivate != nil && row.Index() < len(f.Files) {
			f.OnActivate(f.Files[row.Index()].Path)
		}
	})
	f.list.SetActivateOnSingleClick(true)
	f.scroll = gtk.NewScrolledWindow()
	f.scroll.SetChild(f.list)
	f.scroll.SetVExpand(true)
	f.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	f.Root.Append(f.scroll)
	return f
}

// shows reports whether the filter lets path through: every word must appear in it, ignoring case.
func (f *FileList) shows(path string) bool {
	path = strings.ToLower(path)
	for _, word := range strings.Fields(strings.ToLower(f.filter.Text())) {
		if !strings.Contains(path, word) {
			return false
		}
	}
	return true
}

// SetFiles replaces the rows. Only paths in selectable get a checkbox; nil makes every row
// selectable. Checked and selected rows survive; with checkNew and every row checked, new
// rows arrive checked.
func (f *FileList) SetFiles(files []git.File, selectable []string) {
	wasAll := f.checkNew && f.allChecked(false)
	keepScroll := f.scroll.VAdjustment().Value()
	selected := ""
	if row := f.list.SelectedRow(); row != nil && row.Index() < len(f.Files) {
		selected = f.Files[row.Index()].Path
	}
	f.Files = files
	f.selectable = map[string]bool{}
	if selectable == nil {
		for _, file := range files {
			f.selectable[file.Path] = true
		}
	} else {
		for _, path := range selectable {
			f.selectable[path] = true
		}
	}
	checked := map[string]bool{}
	for path := range f.selectable {
		checked[path] = f.checked[path] || wasAll
	}
	f.checked = checked
	f.checks = map[string]*gtk.CheckButton{}
	f.list.RemoveAll()
	adds, dels := 0, 0
	for i, file := range files {
		adds += file.Adds
		dels += file.Dels
		f.list.Append(f.row(file))
		if file.Path == selected {
			f.list.SelectRow(f.list.RowAtIndex(i))
		}
	}
	f.Title.SetText(fmt.Sprintf("Changes (%d)", len(files)))
	f.totals.SetMarkup(fmt.Sprintf(`<span foreground="#3fb950">+%d</span> <span foreground="#f85149">-%d</span>`, adds, dels))
	f.syncChecks()
	f.scroll.VAdjustment().SetValue(keepScroll)
}

func (f *FileList) row(file git.File) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.SetMarginStart(4)
	box.SetMarginEnd(4)
	check := gtk.NewCheckButton()
	if f.selectable[file.Path] {
		path := file.Path
		check.ConnectToggled(func() {
			if f.updating {
				return
			}
			f.checked[path] = check.Active()
			f.syncAll()
			if f.OnChecked != nil {
				f.OnChecked()
			}
		})
		f.checks[file.Path] = check
	} else {
		check.SetSensitive(false)
		check.SetOpacity(0)
	}
	box.Append(check)
	letter := gtk.NewLabel(file.Status)
	letter.AddCSSClass("status-letter")
	letter.AddCSSClass(statusClass(file.Status))
	name := gtk.NewLabel(filepath.Base(file.Path))
	name.SetXAlign(0)
	dir := filepath.Dir(file.Path)
	if dir == "." {
		dir = ""
	}
	where := gtk.NewLabel(dir)
	where.AddCSSClass("dim")
	where.SetXAlign(0)
	where.SetHExpand(true)
	where.SetEllipsize(2) // middle
	box.SetTooltipText(file.Path)
	counts := gtk.NewLabel("")
	switch {
	case file.Binary:
		counts.SetText("bin")
		counts.AddCSSClass("dim")
	case file.Dels > 0:
		counts.SetMarkup(fmt.Sprintf(`<span foreground="#3fb950">+%d</span> <span foreground="#f85149">-%d</span>`, file.Adds, file.Dels))
	default:
		counts.SetMarkup(fmt.Sprintf(`<span foreground="#3fb950">+%d</span>`, file.Adds))
	}
	box.Append(letter)
	box.Append(name)
	box.Append(where)
	box.Append(counts)
	return box
}

// setVisible ticks or unticks every selectable row the filter shows.
func (f *FileList) setVisible(on bool) {
	for path := range f.selectable {
		if f.shows(path) {
			f.checked[path] = on
		}
	}
	f.syncChecks()
	if f.OnChecked != nil {
		f.OnChecked()
	}
}

func (f *FileList) syncChecks() {
	f.updating = true
	for path, check := range f.checks {
		check.SetActive(f.checked[path])
	}
	f.updating = false
	f.syncAll()
}

// syncAll sets the select-all box from the shown rows and counts the checked ones.
func (f *FileList) syncAll() {
	some := false
	for path := range f.selectable {
		some = some || (f.checked[path] && f.shows(path))
	}
	f.updating = true
	all := f.allChecked(true)
	f.all.SetActive(all)
	f.all.SetInconsistent(some && !all)
	f.updating = false
	if n := len(f.Checked()); n > 0 {
		f.count.SetText(fmt.Sprintf("%d selected", n))
	} else {
		f.count.SetText("")
	}
}

// allChecked reports whether every selectable row, or every one the filter shows, is checked.
func (f *FileList) allChecked(shown bool) bool {
	for path := range f.selectable {
		if !f.checked[path] && (!shown || f.shows(path)) {
			return false
		}
	}
	return true
}

// Checked lists the checked paths in list order, including rows the filter hides.
func (f *FileList) Checked() []string {
	var paths []string
	for _, file := range f.Files {
		if f.checked[file.Path] {
			paths = append(paths, file.Path)
		}
	}
	return paths
}

// Selectable lists the paths that can be checked.
func (f *FileList) Selectable() []string {
	var paths []string
	for _, file := range f.Files {
		if f.selectable[file.Path] {
			paths = append(paths, file.Path)
		}
	}
	return paths
}
