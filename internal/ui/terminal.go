package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/daemon"
)

// Terminal shows one slot's job output with its status, and runs one-shot shell commands in a
// second "Terminal" page.
type Terminal struct {
	Root   *gtk.Notebook
	Runner *Runner
	out    *console
	pill   *gtk.Label
	pause  *gtk.Button
	cancel *gtk.Button

	label      string // the running state's text, such as "Publishing PR"
	since      time.Time
	ticker     glib.SourceHandle
	shell      *Runner
	shellOut   *console
	shellPill  *gtk.Label
	shellStop  *gtk.Button
	shellSince time.Time

	// Hooks for the owning tab.
	OnOutput   func(text string, replaying bool)
	OnStarted  func()
	OnFinished func(code int, record daemon.Record)
	OnRestored func(record daemon.Record, running, paused bool)
	OnError    func(message string)
	OnCancel   func() // replaces Cancel's default of stopping the job
}

// NewTerminal builds a terminal for (repo, key). program names a helper whose runs started
// outside git-gui are observed.
func NewTerminal(repo, key, program string) *Terminal {
	t := &Terminal{Root: gtk.NewNotebook(), Runner: NewRunner(repo, key, program), out: newConsole()}
	t.Root.AppendPage(t.outputPage(), gtk.NewLabel("Output"))
	t.shell = NewRunner(repo, key+"-shell", "")
	t.shellOut = newConsole()
	t.Root.AppendPage(t.shellPage(), gtk.NewLabel("Terminal"))
	t.wire()
	return t
}

func (t *Terminal) outputPage() gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	header.SetMarginStart(6)
	header.SetMarginEnd(6)
	header.SetMarginTop(4)
	title := gtk.NewLabel("Task Output")
	title.AddCSSClass("heading")
	t.pill = newPill()
	t.pill.SetVisible(false)
	t.pause = gtk.NewButtonWithLabel("Pause")
	t.pause.SetTooltipText("Freeze the running command and keep it available to resume")
	t.pause.SetSensitive(false)
	t.pause.ConnectClicked(func() { t.Runner.TogglePause() })
	t.cancel = gtk.NewButtonWithLabel("Cancel")
	t.cancel.SetTooltipText("Cancel the running command. Any commit already made stays on its branch.")
	t.cancel.AddCSSClass("destructive-action")
	t.cancel.SetSensitive(false)
	t.cancel.ConnectClicked(func() {
		if t.OnCancel != nil {
			t.OnCancel()
		} else {
			t.Runner.Stop()
		}
	})
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	header.Append(title)
	header.Append(t.pill)
	header.Append(spacer)
	header.Append(t.pause)
	header.Append(t.cancel)
	box.Append(header)
	box.Append(t.out.scroll)
	box.Append(t.controls(t.out, []gtk.Widgetter{t.menu()}))
	return box
}

func (t *Terminal) controls(c *console, extra []gtk.Widgetter) gtk.Widgetter {
	row := gtk.NewBox(gtk.OrientationHorizontal, 6)
	row.SetMarginStart(6)
	row.SetMarginEnd(6)
	row.SetMarginBottom(4)
	wrap := gtk.NewCheckButtonWithLabel("Wrap")
	wrap.SetActive(true)
	wrap.ConnectToggled(func() {
		if wrap.Active() {
			c.view.SetWrapMode(gtk.WrapWordChar)
		} else {
			c.view.SetWrapMode(gtk.WrapNone)
		}
	})
	scroll := gtk.NewCheckButtonWithLabel("Auto-scroll")
	scroll.SetActive(true)
	scroll.ConnectToggled(func() { c.AutoScroll = scroll.Active(); c.follow() })
	copyButton := gtk.NewButtonWithLabel("Copy")
	copyButton.ConnectClicked(func() { copyText(c.Text()) })
	clear := gtk.NewButtonWithLabel("Clear")
	clear.ConnectClicked(func() {
		c.Clear()
		if c == t.out && !t.Runner.Running() {
			t.pill.SetVisible(false)
		}
	})
	row.Append(wrap)
	row.Append(scroll)
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	row.Append(spacer)
	row.Append(copyButton)
	row.Append(clear)
	for _, w := range extra {
		row.Append(w)
	}
	return row
}

func (t *Terminal) menu() gtk.Widgetter {
	menu := gio.NewMenu()
	menu.Append("Reload log", "term.reload")
	menu.Append("History", "term.history")
	button := gtk.NewMenuButton()
	button.SetIconName("view-more-symbolic")
	button.SetMenuModel(menu)
	actions := gio.NewSimpleActionGroup()
	reload := gio.NewSimpleAction("reload", nil)
	reload.ConnectActivate(func(*glib.Variant) { t.out.Clear(); t.Runner.Reload() })
	history := gio.NewSimpleAction("history", nil)
	history.ConnectActivate(func(*glib.Variant) { t.showHistory() })
	actions.AddAction(reload)
	actions.AddAction(history)
	t.Root.InsertActionGroup("term", actions)
	return button
}

func (t *Terminal) shellPage() gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	header.SetMarginStart(6)
	header.SetMarginEnd(6)
	header.SetMarginTop(4)
	title := gtk.NewLabel("Command Runner")
	title.AddCSSClass("heading")
	t.shellPill = newPill()
	setPill(t.shellPill, "Ready", "idle")
	t.shellStop = gtk.NewButtonWithLabel("Stop")
	t.shellStop.SetSensitive(false)
	t.shellStop.ConnectClicked(func() { t.shell.Stop() })
	header.Append(title)
	header.Append(t.shellPill)
	box.Append(header)
	box.Append(t.shellOut.scroll)
	input := gtk.NewBox(gtk.OrientationHorizontal, 6)
	input.SetMarginStart(6)
	input.SetMarginEnd(6)
	prompt := gtk.NewLabel("$")
	prompt.AddCSSClass("console")
	entry := gtk.NewEntry()
	entry.SetPlaceholderText("Type command (e.g. git status, just restart, cargo test)...")
	entry.SetHExpand(true)
	run := gtk.NewButtonWithLabel("Run")
	start := func() {
		if command := entry.Text(); command != "" && !t.shell.Running() {
			entry.SetText("")
			t.shell.Start([]string{"bash", "-c", command}, nil)
		}
	}
	entry.ConnectActivate(start)
	run.ConnectClicked(start)
	input.Append(prompt)
	input.Append(entry)
	input.Append(run)
	box.Append(input)
	box.Append(t.controls(t.shellOut, []gtk.Widgetter{t.shellStop}))
	return box
}

func (t *Terminal) wire() {
	r := t.Runner
	r.OnOutput = func(text string, replaying bool) {
		t.out.Append(text)
		if t.OnOutput != nil {
			t.OnOutput(text, replaying)
		}
	}
	r.OnStarted = func() {
		t.since = time.Now()
		t.Root.SetCurrentPage(0)
		t.refreshPill()
		if t.OnStarted != nil {
			t.OnStarted()
		}
	}
	r.OnRestored = func(record daemon.Record, running, paused bool) {
		if running {
			t.since = time.Unix(int64(record.StartedAt), 0)
			t.Root.SetCurrentPage(0)
		}
		t.refreshPill()
		if t.OnRestored != nil {
			t.OnRestored(record, running, paused)
		}
	}
	r.OnFinished = func(code int, record daemon.Record) {
		t.refreshPill()
		if t.OnFinished != nil {
			t.OnFinished(code, record)
		}
	}
	r.OnError = func(message string) {
		t.out.AppendStyled("[ERROR] "+message+"\n", "error")
		setPill(t.pill, "Error", "failed")
		t.pill.SetVisible(true)
		if t.OnError != nil {
			t.OnError(message)
		}
	}
	s := t.shell
	s.OnOutput = func(text string, _ bool) { t.shellOut.Append(text) }
	s.OnStarted = func() { t.shellSince = time.Now(); t.Root.SetCurrentPage(1); t.refreshShell() }
	s.OnRestored = func(record daemon.Record, running, _ bool) {
		if running {
			t.shellSince = time.Unix(int64(record.StartedAt), 0)
			if !t.Runner.Running() {
				t.Root.SetCurrentPage(1)
			}
		}
		t.refreshShell()
	}
	s.OnFinished = func(int, daemon.Record) { t.refreshShell() }
	s.OnError = func(message string) { t.shellOut.AppendStyled("[ERROR] "+message+"\n", "error") }
}

// Attach shows the slot's current job and shell job.
func (t *Terminal) Attach() {
	t.Runner.Attach()
	t.shell.Attach()
}

// SetLabel names the running state, such as "Publishing PR"; empty means "Running for".
func (t *Terminal) SetLabel(label string) {
	t.label = label
	t.refreshPill()
}

// SetStatus shows a status that is not a job's, such as a schedule's progress.
func (t *Terminal) SetStatus(text, state string) {
	setPill(t.pill, text, state)
	t.pill.SetVisible(true)
}

// Info prints an app message in the console.
func (t *Terminal) Info(text string) { t.out.AppendStyled(text+"\n", "info") }

// Clear empties the console.
func (t *Terminal) Clear() { t.out.Clear() }

func (t *Terminal) refreshPill() {
	r := t.Runner
	t.pause.SetSensitive(r.Running() && !r.External() && r.Record.Child != nil)
	t.cancel.SetSensitive(r.Running() && !r.External())
	if r.Paused() {
		t.pause.SetLabel("Resume")
	} else {
		t.pause.SetLabel("Pause")
	}
	if r.Path == "" && !r.External() {
		return
	}
	t.pill.SetVisible(true)
	if r.Running() {
		elapsed := formatDuration(time.Since(t.since))
		switch {
		case r.External():
			setPill(t.pill, "Observing helper ("+elapsed+")", "running")
		case r.Paused():
			setPill(t.pill, "Paused ("+elapsed+")", "waiting")
		case t.label != "":
			setPill(t.pill, t.label+" ("+elapsed+")", "running")
		default:
			setPill(t.pill, "Running for "+elapsed, "running")
		}
		t.tick()
		return
	}
	record := r.Record
	took := ""
	if record.FinishedAt > 0 && record.StartedAt > 0 {
		took = " in " + formatDuration(time.Duration((record.FinishedAt-record.StartedAt)*float64(time.Second)))
	}
	switch {
	case record.Stopped:
		setPill(t.pill, "Stopped"+took, "failed")
	case record.ExitCode == nil || *record.ExitCode == -1:
		setPill(t.pill, "Ended, exit status unknown", "failed")
	case *record.ExitCode == 0:
		setPill(t.pill, "Completed"+took, "success")
	default:
		setPill(t.pill, fmt.Sprintf("Exit %d%s", *record.ExitCode, took), "failed")
	}
}

func (t *Terminal) refreshShell() {
	s := t.shell
	t.shellStop.SetSensitive(s.Running())
	switch {
	case s.Running():
		setPill(t.shellPill, "Running for "+formatDuration(time.Since(t.shellSince)), "running")
		t.tick()
	case s.Record.ExitCode != nil && *s.Record.ExitCode == 0:
		setPill(t.shellPill, "Completed", "success")
	case s.Record.ExitCode != nil:
		setPill(t.shellPill, fmt.Sprintf("Exit %d", *s.Record.ExitCode), "failed")
	}
}

// tick redraws running durations every second while a job runs.
func (t *Terminal) tick() {
	if t.ticker != 0 {
		return
	}
	t.ticker = glib.TimeoutAdd(1000, func() bool {
		if !t.Runner.Running() && !t.shell.Running() {
			t.ticker = 0
			return false
		}
		if t.Runner.Running() {
			t.refreshPill()
		}
		if t.shell.Running() {
			t.refreshShell()
		}
		return true
	})
}

// showHistory lists the slot's earlier jobs and shows the end of a chosen log.
func (t *Terminal) showHistory() {
	entries := append(client.Records(t.Runner.Cwd, t.Runner.Key), client.Records(t.shell.Cwd, t.shell.Key)...)
	dialog := adw.NewDialog()
	dialog.SetTitle("Command history")
	dialog.SetContentWidth(850)
	dialog.SetContentHeight(550)
	labels := make([]string, len(entries))
	for i, e := range entries {
		status := "Exit unknown"
		switch {
		case client.Running(e.Record):
			status = "Running"
		case e.Record.Stopped:
			status = "Stopped"
		case e.Record.ExitCode != nil:
			status = fmt.Sprintf("Exit %d", *e.Record.ExitCode)
		}
		started := time.Unix(int64(e.Record.StartedAt), 0).Format("2006-01-02 15:04")
		labels[i] = started + " | " + status + " | " + shellJoin(e.Record.Command)
	}
	picker := gtk.NewDropDownFromStrings(labels)
	view := newConsole()
	view.AutoScroll = false
	show := func() {
		view.Clear()
		index := int(picker.Selected())
		if index >= len(entries) {
			view.Append("No output recorded.")
			return
		}
		data, err := os.ReadFile(filepath.Join(filepath.Dir(entries[index].Path), "output.log"))
		if err != nil || len(data) == 0 {
			view.Append("No output recorded.")
			return
		}
		view.Append(string(data[max(0, len(data)-1<<20):]))
	}
	picker.NotifyProperty("selected", show)
	open := gtk.NewButtonWithLabel("Open saved log files")
	open.ConnectClicked(func() {
		gtk.NewFileLauncher(gio.NewFileForPath(daemon.JobsRoot())).Launch(context(), nil, nil)
	})
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	box.SetMarginBottom(12)
	box.Append(picker)
	box.Append(view.scroll)
	box.Append(open)
	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(adw.NewHeaderBar())
	toolbar.SetContent(box)
	dialog.SetChild(toolbar)
	show()
	dialog.Present(t.Root)
}
