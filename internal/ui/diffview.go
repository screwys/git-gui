package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pangocairo"
)

// lineInfo describes one shown line: its gutter number and where an edit there lands in the file.
type lineInfo struct {
	number   string
	kind     string // add, del, hunk, header, context or file
	fileLine int    // 0-based line in the file that an edit here applies to
	shift    int    // columns before the file text (the +/- prefix)
}

// DiffView shows a diff with line numbers, and edits the file when it covers one file on disk.
type DiffView struct {
	Root     *adw.ToastOverlay
	Repo     string
	ReadOnly bool // the Changes tab: no File mode

	view     *gtk.TextView
	buffer   *gtk.TextBuffer
	gutter   *gtk.DrawingArea
	title    *gtk.Label
	counts   *gtk.Label
	badge    *gtk.Label
	diffMode *gtk.ToggleButton
	fileMode *gtk.ToggleButton
	save     *gtk.Button
	reset    *gtk.Button
	wrap     *gtk.CheckButton

	lines    []lineInfo
	diffText string
	path     string // the single file shown, "" for several
	editing  bool   // File mode
	dirty    bool
	loading  bool
	saved    string // content before the last save, for Revert

	OnSaved func(path string)
}

// NewDiffView builds a viewer for files in repo.
func NewDiffView(repo string) *DiffView {
	d := &DiffView{Root: adw.NewToastOverlay(), Repo: repo, view: gtk.NewTextView()}
	d.buffer = d.view.Buffer()
	box := gtk.NewBox(gtk.OrientationVertical, 4)

	header := gtk.NewBox(gtk.OrientationHorizontal, 8)
	d.title = gtk.NewLabel("")
	d.title.AddCSSClass("monospace")
	d.title.SetEllipsize(3) // end
	d.title.SetXAlign(0)
	d.title.SetHExpand(true)
	d.counts = gtk.NewLabel("")
	d.badge = gtk.NewLabel("")
	d.badge.AddCSSClass("modified")
	header.Append(d.title)
	header.Append(d.badge)
	header.Append(d.counts)
	box.Append(header)

	controls := gtk.NewBox(gtk.OrientationHorizontal, 6)
	d.diffMode = gtk.NewToggleButtonWithLabel("Diff")
	d.fileMode = gtk.NewToggleButtonWithLabel("File")
	d.fileMode.SetGroup(d.diffMode)
	d.diffMode.SetActive(true)
	d.fileMode.SetSensitive(false)
	d.fileMode.ConnectToggled(func() {
		if d.loading {
			return
		}
		if d.fileMode.Active() {
			d.openFile(0, 0)
		} else {
			d.showDiffText()
		}
	})
	d.save = gtk.NewButtonWithLabel("Save")
	d.save.SetTooltipText("Save changes to file (Ctrl+S)")
	d.save.ConnectClicked(d.confirmSave)
	d.reset = gtk.NewButtonWithLabel("Reset")
	d.reset.SetTooltipText("Discard unsaved in-editor changes")
	d.reset.ConnectClicked(func() {
		if d.editing {
			d.openFile(0, 0)
		} else {
			d.showDiffText()
		}
	})
	d.wrap = gtk.NewCheckButtonWithLabel("Wrap")
	d.wrap.SetActive(appConfig.WrapDiff)
	d.applyWrap()
	d.wrap.ConnectToggled(func() {
		appConfig.WrapDiff = d.wrap.Active()
		_ = appConfig.Save()
		d.applyWrap()
	})
	copyButton := gtk.NewButtonWithLabel("Copy")
	copyButton.ConnectClicked(func() { copyText(d.text()) })
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	controls.Append(d.diffMode)
	controls.Append(d.fileMode)
	controls.Append(d.save)
	controls.Append(d.reset)
	controls.Append(spacer)
	controls.Append(d.wrap)
	controls.Append(copyButton)
	box.Append(controls)

	d.view.SetMonospace(true)
	d.view.AddCSSClass("diff")
	d.view.SetLeftMargin(6)
	d.view.SetEditable(false)
	d.createTags()
	d.gutter = gtk.NewDrawingArea()
	d.gutter.SetContentWidth(44)
	d.gutter.SetDrawFunc(d.drawGutter)
	d.view.SetGutter(gtk.TextWindowLeft, d.gutter)
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(d.view)
	scroll.SetVExpand(true)
	scroll.VAdjustment().ConnectValueChanged(func() { d.gutter.QueueDraw() })
	box.Append(scroll)
	d.buffer.ConnectChanged(func() {
		d.gutter.QueueDraw()
		if d.editing && !d.loading {
			d.dirty = true
			d.badge.SetText("● Modified")
		}
	})

	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(d.keyPressed)
	d.view.AddController(keys)
	d.Root.SetChild(box)
	d.updateControls()
	return d
}

func (d *DiffView) createTags() {
	table := d.buffer.TagTable()
	add := func(name string, props map[string]any) {
		tag := gtk.NewTextTag(name)
		for k, v := range props {
			tag.SetObjectProperty(k, v)
		}
		table.Add(tag)
	}
	add("hunk", map[string]any{"foreground": "#58a6ff", "weight": 700, "paragraph-background": "rgba(56,139,253,0.12)"})
	add("header", map[string]any{"foreground": "#8b949e"})
	add("add", map[string]any{"foreground": "#3fb950", "paragraph-background": "rgba(46,160,67,0.15)"})
	add("del", map[string]any{"foreground": "#f85149", "paragraph-background": "rgba(248,81,73,0.15)"})
}

func (d *DiffView) applyWrap() {
	if d.wrap.Active() {
		d.view.SetWrapMode(gtk.WrapWordChar)
	} else {
		d.view.SetWrapMode(gtk.WrapNone)
	}
}

// ShowDiff shows diff text. path names the one file it covers, or "" for several.
func (d *DiffView) ShowDiff(title, text string, adds, dels int, path string) {
	if d.editing && d.dirty && path == d.path && path != "" {
		return // keep unsaved edits across a refresh of the same file
	}
	d.title.SetText(title)
	d.title.SetTooltipText(title)
	var parts []string
	if adds > 0 {
		parts = append(parts, fmt.Sprintf(`<span foreground="#3fb950">+%d</span>`, adds))
	}
	if dels > 0 {
		parts = append(parts, fmt.Sprintf(`<span foreground="#f85149">-%d</span>`, dels))
	}
	d.counts.SetMarkup(strings.Join(parts, " "))
	d.diffText, d.path = text, path
	if d.editing && path != "" && d.fileExists() {
		d.openFile(0, 0)
		return
	}
	d.showDiffText()
}

// ShowMessage shows a note instead of a diff.
func (d *DiffView) ShowMessage(title, message string) {
	d.ShowDiff(title, "", 0, 0, "")
	d.loading = true
	d.buffer.SetText(message)
	d.loading = false
	d.lines = nil
}

// CurrentPath is the single file shown, or "".
func (d *DiffView) CurrentPath() string { return d.path }

func (d *DiffView) showDiffText() {
	d.editing, d.dirty = false, false
	d.loading = true
	d.diffMode.SetActive(true)
	d.buffer.SetText("")
	d.lines = d.lines[:0]
	if strings.TrimSpace(d.diffText) == "" {
		d.buffer.SetText("(Clean working tree or no diff)")
		d.loading = false
		d.badge.SetText("")
		d.updateControls()
		return
	}
	oldLine, newLine := 0, 0
	for _, line := range strings.Split(strings.TrimSuffix(d.diffText, "\n"), "\n") {
		info := lineInfo{kind: "context", fileLine: newLine - 1, shift: 1}
		tag := ""
		switch {
		case strings.HasPrefix(line, "@@"):
			info.kind, info.number, tag = "hunk", "@@", "hunk"
			oldLine, newLine = hunkStart(line)
			info.fileLine = max(newLine-1, 0)
		case strings.HasPrefix(line, "diff --git"), strings.HasPrefix(line, "index "), strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"),
			strings.HasPrefix(line, "new file"), strings.HasPrefix(line, "deleted file"), strings.HasPrefix(line, "rename "), strings.HasPrefix(line, "similarity"), strings.HasPrefix(line, "Binary files"):
			info.kind, tag = "header", "header"
		case strings.HasPrefix(line, "+"):
			info.kind, info.number, tag = "add", strconv.Itoa(newLine), "add"
			info.fileLine = newLine - 1
			newLine++
		case strings.HasPrefix(line, "-"):
			info.kind, info.number, tag = "del", strconv.Itoa(oldLine), "del"
			info.fileLine = newLine - 1
			oldLine++
		default:
			info.number = strconv.Itoa(newLine)
			info.fileLine = newLine - 1
			oldLine++
			newLine++
		}
		end := d.buffer.EndIter()
		offset := end.Offset()
		if len(d.lines) > 0 {
			d.buffer.Insert(end, "\n")
			offset++
		}
		d.buffer.Insert(d.buffer.EndIter(), line)
		if tag != "" {
			d.buffer.ApplyTag(d.buffer.TagTable().Lookup(tag), d.buffer.IterAtOffset(offset), d.buffer.EndIter())
		}
		d.lines = append(d.lines, info)
	}
	d.loading = false
	d.badge.SetText("")
	d.updateControls()
}

// hunkStart reads the old and new start lines of "@@ -a,b +c,d @@".
func hunkStart(line string) (int, int) {
	var a, c int
	fields := strings.Fields(line)
	if len(fields) >= 3 {
		a, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(fields[1], "-"), ",")[0])
		c, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(fields[2], "+"), ",")[0])
	}
	return a, c
}

func (d *DiffView) fileExists() bool {
	if d.path == "" || d.Repo == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(d.Repo, d.path))
	return err == nil && info.Mode().IsRegular()
}

// openFile switches to File mode with the cursor at line and column.
func (d *DiffView) openFile(line, column int) {
	data, err := os.ReadFile(filepath.Join(d.Repo, d.path))
	if err != nil {
		d.alert("Read Error", fmt.Sprintf("%s\n\n%v", d.path, err))
		return
	}
	d.loading = true
	d.editing, d.dirty = true, false
	d.fileMode.SetActive(true)
	d.buffer.SetText(strings.ToValidUTF8(string(data), "�"))
	d.lines = d.lines[:0]
	for i := range d.buffer.LineCount() {
		d.lines = append(d.lines, lineInfo{number: strconv.Itoa(i + 1), kind: "file", fileLine: i})
	}
	if iter, ok := d.buffer.IterAtLineOffset(line, column); ok {
		d.buffer.PlaceCursor(iter)
		d.view.ScrollToIter(iter, 0.1, false, 0, 0)
	}
	d.loading = false
	d.badge.SetText("")
	d.updateControls()
	d.view.GrabFocus()
}

// keyPressed turns typing into an edit of the file: Diff mode switches to File mode at the
// matching place, then applies the key there.
func (d *DiffView) keyPressed(keyval, _ uint, state gdk.ModifierType) bool {
	if state&gdk.ControlMask != 0 {
		if keyval == gdk.KEY_s && d.editing {
			d.confirmSave()
			return true
		}
		return false
	}
	if d.editing || d.ReadOnly || d.path == "" || !d.fileExists() {
		return false
	}
	text := ""
	switch keyval {
	case gdk.KEY_BackSpace, gdk.KEY_Delete:
	case gdk.KEY_Return, gdk.KEY_KP_Enter:
		text = "\n"
	case gdk.KEY_Tab:
		text = "\t"
	default:
		r := gdk.KeyvalToUnicode(keyval)
		if r == 0 || r < 32 {
			return false
		}
		text = string(rune(r))
	}
	cursor := d.buffer.IterAtMark(d.buffer.GetInsert())
	shown := cursor.Line()
	if shown >= len(d.lines) {
		return false
	}
	info := d.lines[shown]
	column := 0
	if info.kind == "context" || info.kind == "add" {
		column = max(cursor.LineOffset()-info.shift, 0)
	}
	d.openFile(info.fileLine, column)
	insert := d.buffer.IterAtMark(d.buffer.GetInsert())
	switch keyval {
	case gdk.KEY_BackSpace:
		if insert.BackwardChar() {
			end := d.buffer.IterAtMark(d.buffer.GetInsert())
			d.buffer.Delete(insert, end)
		}
	case gdk.KEY_Delete:
		end := d.buffer.IterAtMark(d.buffer.GetInsert())
		if end.ForwardChar() {
			d.buffer.Delete(insert, end)
		}
	default:
		d.buffer.Insert(insert, text)
	}
	return true
}

func (d *DiffView) updateControls() {
	d.fileMode.SetVisible(!d.ReadOnly)
	d.fileMode.SetSensitive(d.fileExists() || d.editing)
	d.save.SetVisible(d.editing)
	d.reset.SetVisible(d.editing)
	d.view.SetEditable(d.editing)
	d.view.SetCursorVisible(d.editing || (!d.ReadOnly && d.path != ""))
	d.gutter.QueueDraw()
}

func (d *DiffView) confirmSave() {
	if !d.editing || d.path == "" {
		return
	}
	dialog := adw.NewAlertDialog("Save file", "Save changes to "+d.path+"?")
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("save", "Save")
	dialog.SetResponseAppearance("save", adw.ResponseSuggested)
	dialog.SetDefaultResponse("cancel")
	dialog.SetCloseResponse("cancel")
	dialog.ConnectResponse(func(response string) {
		if response == "save" {
			d.write()
		}
	})
	dialog.Present(d.Root)
}

func (d *DiffView) write() {
	full := filepath.Join(d.Repo, d.path)
	before, _ := os.ReadFile(full)
	if err := os.WriteFile(full, []byte(d.text()), 0o644); err != nil {
		d.alert("Save Failed", fmt.Sprintf("%s\n\n%v", d.path, err))
		return
	}
	d.saved, d.dirty = string(before), false
	d.badge.SetText("Saved")
	toast := adw.NewToast("File saved")
	toast.SetButtonLabel("Revert")
	toast.SetTimeout(8)
	path := d.path
	toast.ConnectButtonClicked(func() { d.revert(path) })
	d.Root.AddToast(toast)
	if d.OnSaved != nil {
		d.OnSaved(path)
	}
}

func (d *DiffView) revert(path string) {
	if err := os.WriteFile(filepath.Join(d.Repo, path), []byte(d.saved), 0o644); err != nil {
		d.alert("Revert Failed", fmt.Sprintf("%s\n\n%v", path, err))
		return
	}
	if d.path == path && d.editing {
		d.openFile(0, 0)
	}
	d.badge.SetText("Reverted")
	if d.OnSaved != nil {
		d.OnSaved(path)
	}
}

func (d *DiffView) alert(heading, body string) {
	dialog := adw.NewAlertDialog(heading, body)
	dialog.AddResponse("ok", "OK")
	dialog.Present(d.Root)
}

func (d *DiffView) text() string {
	return d.buffer.Text(d.buffer.StartIter(), d.buffer.EndIter(), false)
}

// drawGutter paints the line numbers of the visible lines beside the text.
func (d *DiffView) drawGutter(_ *gtk.DrawingArea, cr *cairo.Context, width, _ int) {
	if len(d.lines) == 0 {
		return
	}
	widest := 3
	for _, info := range d.lines {
		widest = max(widest, len(info.number))
	}
	layout := d.gutter.CreatePangoLayout(strings.Repeat("0", widest))
	charWidth, _ := layout.PixelSize()
	if want := charWidth + 12; want != d.gutter.ContentWidth() {
		d.gutter.SetContentWidth(want)
	}
	visible := d.view.VisibleRect()
	top, _ := d.view.IterAtLocation(visible.X(), visible.Y())
	bottomY := visible.Y() + visible.Height()
	colours := map[string][3]float64{"add": {0.25, 0.73, 0.31}, "del": {0.97, 0.32, 0.29}, "hunk": {0.35, 0.65, 1}}
	for iter := top; ; {
		line := iter.Line()
		y, _ := d.view.LineYrange(iter)
		if y > bottomY || line >= len(d.lines) {
			break
		}
		info := d.lines[line]
		if info.number != "" {
			_, wy := d.view.BufferToWindowCoords(gtk.TextWindowLeft, 0, y)
			c, ok := colours[info.kind]
			if !ok {
				c = [3]float64{0.55, 0.58, 0.62}
			}
			cr.SetSourceRGB(c[0], c[1], c[2])
			text := d.gutter.CreatePangoLayout(info.number)
			w, _ := text.PixelSize()
			cr.MoveTo(float64(width-w-6), float64(wy))
			pangocairo.ShowLayout(cr, text)
		}
		if !iter.ForwardLine() {
			break
		}
	}
}
