package ui

import (
	"fmt"
	"path/filepath"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/git"
)

// FileList shows changed files. Editable lists have checkboxes on selectable rows; the
// read-only list allows multi-select instead.
type FileList struct {
	Root     *gtk.Box
	Title    *gtk.Label
	Button   *gtk.Button // the tab's primary action; hidden when unused
	View     *gtk.Button
	list     *gtk.ListBox
	scroll   *gtk.ScrolledWindow
	all      *gtk.CheckButton
	totals   *gtk.Label
	editable bool

	Files      []git.File
	selectable map[string]bool
	checked    map[string]bool
	checks     map[string]*gtk.CheckButton
	updating   bool

	OnActivate func(path string)    // a row was clicked
	OnView     func(paths []string) // View Diff
	OnChecked  func()               // the checked set changed
}

// NewFileList builds a list; editable adds checkboxes and the select-all box.
func NewFileList(editable bool, action string) *FileList {
	f := &FileList{Root: gtk.NewBox(gtk.OrientationVertical, 4), list: gtk.NewListBox(), editable: editable,
		checked: map[string]bool{}, checks: map[string]*gtk.CheckButton{}, selectable: map[string]bool{}}
	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	f.Title = gtk.NewLabel("Changes (0)")
	f.Title.AddCSSClass("heading")
	f.totals = gtk.NewLabel("")
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	header.Append(f.Title)
	header.Append(spacer)
	header.Append(f.totals)
	if editable {
		f.all = gtk.NewCheckButton()
		f.all.SetTooltipText("Select / Deselect All")
		f.all.ConnectToggled(func() {
			if f.updating {
				return
			}
			for path := range f.selectable {
				f.checked[path] = f.all.Active()
			}
			f.syncChecks()
			if f.OnChecked != nil {
				f.OnChecked()
			}
		})
		header.Append(f.all)
	}
	f.Root.Append(header)
	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	f.Button = gtk.NewButtonWithLabel(action)
	f.Button.AddCSSClass("suggested-action")
	f.Button.SetVisible(action != "")
	f.View = gtk.NewButtonWithLabel("View Diff")
	f.View.ConnectClicked(func() {
		if f.OnView != nil {
			f.OnView(f.chosen())
		}
	})
	buttons.Append(f.Button)
	buttons.Append(f.View)
	f.Root.Append(buttons)
	if editable {
		f.list.SetSelectionMode(gtk.SelectionSingle)
	} else {
		f.list.SetSelectionMode(gtk.SelectionMultiple)
	}
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

// SetFiles replaces the rows. Only paths in selectable get a checkbox; nil makes every row
// selectable. Checked and selected rows survive; when every row was checked, new rows arrive
// checked.
func (f *FileList) SetFiles(files []git.File, selectable []string) {
	wasAll := f.allChecked()
	keepScroll := f.scroll.VAdjustment().Value()
	selected := map[string]bool{}
	for _, row := range f.list.SelectedRows() {
		if i := row.Index(); i < len(f.Files) {
			selected[f.Files[i].Path] = true
		}
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
		checked[path] = f.checked[path] || (wasAll && f.editable)
	}
	f.checked = checked
	f.checks = map[string]*gtk.CheckButton{}
	f.list.RemoveAll()
	adds, dels := 0, 0
	for _, file := range files {
		adds += file.Adds
		dels += file.Dels
		f.list.Append(f.row(file))
	}
	f.Title.SetText(fmt.Sprintf("Changes (%d)", len(files)))
	f.totals.SetMarkup(fmt.Sprintf(`<span foreground="#3fb950">+%d</span> <span foreground="#f85149">-%d</span>`, adds, dels))
	for i, file := range files {
		if selected[file.Path] {
			f.list.SelectRow(f.list.RowAtIndex(i))
		}
	}
	f.syncChecks()
	f.scroll.VAdjustment().SetValue(keepScroll)
}

func (f *FileList) row(file git.File) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.SetMarginStart(4)
	box.SetMarginEnd(4)
	if f.editable {
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
	}
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

func (f *FileList) syncChecks() {
	f.updating = true
	for path, check := range f.checks {
		check.SetActive(f.checked[path])
	}
	f.updating = false
	f.syncAll()
}

func (f *FileList) syncAll() {
	if f.all == nil {
		return
	}
	f.updating = true
	f.all.SetActive(f.allChecked())
	f.updating = false
}

func (f *FileList) allChecked() bool {
	if len(f.selectable) == 0 {
		return true
	}
	for path := range f.selectable {
		if !f.checked[path] {
			return false
		}
	}
	return true
}

// Checked lists the checked paths in list order.
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

// chosen is what View Diff shows: checked rows, or selected rows in a read-only list.
func (f *FileList) chosen() []string {
	if f.editable {
		return f.Checked()
	}
	var paths []string
	for _, row := range f.list.SelectedRows() {
		if i := row.Index(); i < len(f.Files) {
			paths = append(paths, f.Files[i].Path)
		}
	}
	return paths
}

// Selected lists the read-only list's selected paths.
func (f *FileList) Selected() []string { return f.chosen() }

// ClearSelection unselects every row.
func (f *FileList) ClearSelection() { f.list.UnselectAll() }
