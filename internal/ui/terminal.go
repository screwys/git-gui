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

// Terminal is a tab's output notebook: the tab's own job on the Output page when it has one, the
// project's Commands page while the tab is shown, and a Terminal page for one-shot shell
// commands.
type Terminal struct {
	*jobPage // the tab's own job; nil for a tab without one
	Root     *gtk.Notebook

	shell      *Runner
	shellOut   *console
	shellPill  *gtk.Label
	shellStop  *gtk.Button
	shellSince time.Time
	shellTick  glib.SourceHandle

	OnShow func() // brings the notebook into view inside its tab
}

// NewTerminal builds a terminal for the tab job (repo, key). program names a helper whose runs
// started outside git-gui are observed.
func NewTerminal(repo, key, program string) *Terminal {
	t := newTerminal(repo, key+"-shell")
	t.jobPage = newJobPage(repo, key, program, "Task Output", key+"-shell")
	t.show = func() { t.Root.SetCurrentPage(t.Root.PageNum(t.jobPage.Root)) }
	t.Root.PrependPage(t.jobPage.Root, gtk.NewLabel("Output"))
	return t
}

// NewShellTerminal builds a terminal for a tab without a job of its own. Its shell slot is the
// project's, shared by every such tab.
func NewShellTerminal(repo string) *Terminal { return newTerminal(repo, commandsKey+"-shell") }

func newTerminal(repo, shellKey string) *Terminal {
	t := &Terminal{Root: gtk.NewNotebook(), shell: NewRunner(repo, shellKey, ""), shellOut: newConsole()}
	t.Root.AppendPage(t.shellPage(), gtk.NewLabel("Terminal"))
	s := t.shell
	shellPage := func() int { return t.Root.NPages() - 1 }
	s.OnOutput = func(text string, _ bool) { t.shellOut.Append(text) }
	s.OnStarted = func() { t.shellSince = time.Now(); t.Root.SetCurrentPage(shellPage()); t.refreshShell() }
	s.OnRestored = func(record daemon.Record, running, _ bool) {
		if running {
			t.shellSince = time.Unix(int64(record.StartedAt), 0)
			if t.jobPage == nil || !t.Runner.Running() {
				t.Root.SetCurrentPage(shellPage())
			}
		}
		t.refreshShell()
	}
	s.OnFinished = func(int, daemon.Record) { t.refreshShell() }
	s.OnError = func(message string) { t.shellOut.AppendStyled("[ERROR] "+message+"\n", "error") }
	return t
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
	box.Append(consoleControls(t.shellOut, nil, t.shellStop))
	return box
}

// Attach shows the slot's current job and shell job.
func (t *Terminal) Attach() {
	if t.jobPage != nil {
		t.Runner.Attach()
	}
	t.shell.Attach()
}

// host shows the project's Commands page in this terminal, after the tab's own Output page.
// from is the terminal that showed it before; the page stays selected if it was there.
func (t *Terminal) host(commands *jobPage, from *Terminal) {
	selected := false
	if from != nil {
		n := from.Root.PageNum(commands.Root)
		selected = from.Root.CurrentPage() == n
		from.Root.RemovePage(n)
	}
	position := 0
	if t.jobPage != nil {
		position = 1
	}
	t.Root.InsertPage(commands.Root, gtk.NewLabel("Commands"), position)
	if selected {
		t.Root.SetCurrentPage(position)
	}
	commands.show = func() {
		t.Root.SetCurrentPage(t.Root.PageNum(commands.Root))
		if t.OnShow != nil {
			t.OnShow()
		}
	}
}

func (t *Terminal) refreshShell() {
	s := t.shell
	t.shellStop.SetSensitive(s.Running())
	switch {
	case s.Running():
		setPill(t.shellPill, "Running for "+formatDuration(time.Since(t.shellSince)), "running")
		everySecond(&t.shellTick, func() bool {
			if s.Running() {
				t.refreshShell()
			}
			return s.Running()
		})
	case s.Record.ExitCode != nil && *s.Record.ExitCode == 0:
		setPill(t.shellPill, "Completed", "success")
	case s.Record.ExitCode != nil:
		setPill(t.shellPill, fmt.Sprintf("Exit %d", *s.Record.ExitCode), "failed")
	}
}

// commandsKey is the slot of the top-bar commands: fetch, pull, sync, Android and restart.
const commandsKey = "git-tools"

// newCommandsPage builds the page that shows a project's top-bar commands.
func newCommandsPage(repo string) *jobPage {
	return newJobPage(repo, commandsKey, "", "Project Commands", commandsKey+"-shell")
}

// jobPage shows one slot's job output with its status, pause and cancel.
type jobPage struct {
	Root    *gtk.Box
	Runner  *Runner
	out     *console
	pill    *gtk.Label
	pause   *gtk.Button
	cancel  *gtk.Button
	history []string // the slots History lists
	show    func()   // selects the page when a job starts or is found running

	label  string // the running state's text, such as "Publishing PR"
	since  time.Time
	ticker glib.SourceHandle

	// Hooks for the owning tab.
	OnOutput   func(text string, replaying bool)
	OnStarted  func()
	OnFinished func(code int, record daemon.Record)
	OnRestored func(record daemon.Record, running, paused bool)
	OnError    func(message string)
	OnCancel   func() // replaces Cancel's default of stopping the job
}

// newJobPage builds the page for (repo, key); History also lists the jobs of the other slots.
func newJobPage(repo, key, program, title string, other ...string) *jobPage {
	p := &jobPage{Runner: NewRunner(repo, key, program), out: newConsole(), history: append([]string{key}, other...)}
	p.Root = gtk.NewBox(gtk.OrientationVertical, 4)
	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	header.SetMarginStart(6)
	header.SetMarginEnd(6)
	header.SetMarginTop(4)
	heading := gtk.NewLabel(title)
	heading.AddCSSClass("heading")
	p.pill = newPill()
	p.pill.SetVisible(false)
	p.pause = gtk.NewButtonWithLabel("Pause")
	p.pause.SetTooltipText("Freeze the running command and keep it available to resume")
	p.pause.SetSensitive(false)
	p.pause.ConnectClicked(func() { p.Runner.TogglePause() })
	p.cancel = gtk.NewButtonWithLabel("Cancel")
	p.cancel.SetTooltipText("Cancel the running command. Any commit already made stays on its branch.")
	p.cancel.AddCSSClass("destructive-action")
	p.cancel.SetSensitive(false)
	p.cancel.ConnectClicked(func() {
		if p.OnCancel != nil {
			p.OnCancel()
		} else {
			p.Runner.Stop()
		}
	})
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	header.Append(heading)
	header.Append(p.pill)
	header.Append(spacer)
	header.Append(p.pause)
	header.Append(p.cancel)
	p.Root.Append(header)
	p.Root.Append(p.out.scroll)
	clear := func() {
		if !p.Runner.Running() {
			p.pill.SetVisible(false)
		}
	}
	p.Root.Append(consoleControls(p.out, clear, p.menu()))
	p.wire()
	return p
}

func (p *jobPage) menu() gtk.Widgetter {
	menu := gio.NewMenu()
	menu.Append("Reload log", "job.reload")
	menu.Append("History", "job.history")
	button := gtk.NewMenuButton()
	button.SetIconName("view-more-symbolic")
	button.SetMenuModel(menu)
	actions := gio.NewSimpleActionGroup()
	reload := gio.NewSimpleAction("reload", nil)
	reload.ConnectActivate(func(*glib.Variant) { p.out.Clear(); p.Runner.Reload() })
	history := gio.NewSimpleAction("history", nil)
	history.ConnectActivate(func(*glib.Variant) { showHistory(p.Root, p.Runner.Cwd, p.history) })
	actions.AddAction(reload)
	actions.AddAction(history)
	p.Root.InsertActionGroup("job", actions)
	return button
}

func (p *jobPage) wire() {
	r := p.Runner
	r.OnOutput = func(text string, replaying bool) {
		p.out.Append(text)
		if p.OnOutput != nil {
			p.OnOutput(text, replaying)
		}
	}
	r.OnStarted = func() {
		p.since = time.Now()
		if p.show != nil {
			p.show()
		}
		p.refreshPill()
		if p.OnStarted != nil {
			p.OnStarted()
		}
	}
	r.OnRestored = func(record daemon.Record, running, paused bool) {
		if running {
			p.since = time.Unix(int64(record.StartedAt), 0)
			if p.show != nil {
				p.show()
			}
		}
		p.refreshPill()
		if p.OnRestored != nil {
			p.OnRestored(record, running, paused)
		}
	}
	r.OnFinished = func(code int, record daemon.Record) {
		p.refreshPill()
		if p.OnFinished != nil {
			p.OnFinished(code, record)
		}
	}
	r.OnError = func(message string) {
		p.out.AppendStyled("[ERROR] "+message+"\n", "error")
		setPill(p.pill, "Error", "failed")
		p.pill.SetVisible(true)
		if p.OnError != nil {
			p.OnError(message)
		}
	}
}

// SetLabel names the running state, such as "Publishing PR"; empty means "Running for".
func (p *jobPage) SetLabel(label string) {
	p.label = label
	p.refreshPill()
}

// SetStatus shows a status that is not a job's, such as a schedule's progress.
func (p *jobPage) SetStatus(text, state string) {
	setPill(p.pill, text, state)
	p.pill.SetVisible(true)
}

// Info prints an app message in the console.
func (p *jobPage) Info(text string) { p.out.AppendStyled(text+"\n", "info") }

// Clear empties the console.
func (p *jobPage) Clear() { p.out.Clear() }

func (p *jobPage) refreshPill() {
	r := p.Runner
	p.pause.SetSensitive(r.Running() && !r.External() && r.Record.Child != nil)
	p.cancel.SetSensitive(r.Running() && !r.External())
	if r.Paused() {
		p.pause.SetLabel("Resume")
	} else {
		p.pause.SetLabel("Pause")
	}
	if r.Path == "" && !r.External() {
		return
	}
	p.pill.SetVisible(true)
	if r.Running() {
		elapsed := formatDuration(time.Since(p.since))
		switch {
		case r.External():
			setPill(p.pill, "Observing helper ("+elapsed+")", "running")
		case r.Paused():
			setPill(p.pill, "Paused ("+elapsed+")", "waiting")
		case p.label != "":
			setPill(p.pill, p.label+" ("+elapsed+")", "running")
		default:
			setPill(p.pill, "Running for "+elapsed, "running")
		}
		everySecond(&p.ticker, func() bool {
			if r.Running() {
				p.refreshPill()
			}
			return r.Running()
		})
		return
	}
	record := r.Record
	took := ""
	if record.FinishedAt > 0 && record.StartedAt > 0 {
		took = " in " + formatDuration(time.Duration((record.FinishedAt-record.StartedAt)*float64(time.Second)))
	}
	switch {
	case record.Stopped:
		setPill(p.pill, "Stopped"+took, "failed")
	case record.ExitCode == nil || *record.ExitCode == -1:
		setPill(p.pill, "Ended, exit status unknown", "failed")
	case *record.ExitCode == 0:
		setPill(p.pill, "Completed"+took, "success")
	default:
		setPill(p.pill, fmt.Sprintf("Exit %d%s", *record.ExitCode, took), "failed")
	}
}

// everySecond calls step every second until it returns false; a running timer is kept.
func everySecond(handle *glib.SourceHandle, step func() bool) {
	if *handle != 0 {
		return
	}
	*handle = glib.TimeoutAdd(1000, func() bool {
		if step() {
			return true
		}
		*handle = 0
		return false
	})
}

// consoleControls is the row under a console: wrap, auto-scroll, copy, clear and extra widgets.
// cleared runs after Clear.
func consoleControls(c *console, cleared func(), extra ...gtk.Widgetter) gtk.Widgetter {
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
		if cleared != nil {
			cleared()
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

// showHistory lists the earlier jobs of the slots and shows the end of a chosen log.
func showHistory(parent gtk.Widgetter, cwd string, keys []string) {
	var entries []client.Entry
	for _, key := range keys {
		entries = append(entries, client.Records(cwd, key)...)
	}
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
	dialog.Present(parent)
}
