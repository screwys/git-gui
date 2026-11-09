package ui

import (
	"path/filepath"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

// tab is one subtab of a project.
type tab struct {
	label   string
	widget  gtk.Widgetter
	refresh func()
}

// project is one repository's set of subtabs.
type project struct {
	name, repo, kind string // kind is rufin, igloo or generic
	stateKey         string // where the selected subtab is saved
	notebook         *gtk.Notebook
	tabs             []tab
	dock             *Terminal // top-bar commands: fetch, pull, sync, Android
}

// Window is the main window.
type Window struct {
	*adw.ApplicationWindow
	title    *adw.WindowTitle
	projects []*project
	notebook *gtk.Notebook
	docks    *gtk.Stack
	reveal   *gtk.Revealer
	buttons  map[string]*gtk.Button
}

func newWindow(app *adw.Application) *Window {
	loadCSS()
	w := &Window{ApplicationWindow: adw.NewApplicationWindow(&app.Application), buttons: map[string]*gtk.Button{}}
	w.SetTitle("Git GUI")
	w.SetDefaultSize(1200, 780)
	w.SetSizeRequest(880, 560)

	header := adw.NewHeaderBar()
	w.title = adw.NewWindowTitle("Git GUI", "")
	header.SetTitleWidget(w.title)
	add := func(name, icon, label, tooltip string, start bool, run func()) {
		b := gtk.NewButton()
		if icon != "" {
			b.SetIconName(icon)
		} else {
			b.SetLabel(label)
		}
		b.SetTooltipText(tooltip)
		b.ConnectClicked(run)
		w.buttons[name] = b
		if start {
			header.PackStart(b)
		} else {
			header.PackEnd(b)
		}
	}
	add("settings", "emblem-system-symbolic", "", "Preferences", false, func() { w.openSettings() })
	add("pull", "", "Pull", "git pull --ff-only", false, func() { w.runTool("git", "pull", "--ff-only") })
	add("fetch", "", "Fetch", "git fetch --prune origin", false, func() { w.runTool("git", "fetch", "--prune", "origin") })
	add("sync", "", "Sync", "publish-pr.sh --sync: bring main up to origin/main, merging local edits", false,
		func() { w.runTool(appConfig.PublishScript(), "--sync") })
	add("refresh", "view-refresh-symbolic", "", "Reload changes, diffs, and branches", true, func() { w.refreshActive() })
	add("android", "phone-symbolic", "", "", true, func() {
		if w.active().kind == "igloo" {
			w.runTool("just", "build-android")
		} else {
			w.runTool("bash", "-c", "just build apk-dev && just install android")
		}
	})
	add("restart", "system-reboot-symbolic", "", "Restart Server (just restart)", true, func() { w.runTool("just", "restart") })

	w.notebook = gtk.NewNotebook()
	w.notebook.SetScrollable(true)
	plus := gtk.NewButtonFromIconName("list-add-symbolic")
	plus.SetTooltipText("Add project")
	plus.AddCSSClass("flat")
	plus.ConnectClicked(w.addProject)
	w.notebook.SetActionWidget(plus, gtk.PackEnd)

	w.docks = gtk.NewStack()
	w.reveal = gtk.NewRevealer()
	w.reveal.SetChild(w.docks)
	w.reveal.SetRevealChild(false)
	paned := gtk.NewPaned(gtk.OrientationVertical)
	paned.SetStartChild(w.notebook)
	paned.SetEndChild(w.reveal)
	paned.SetResizeEndChild(false)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(header)
	toolbar.SetContent(paned)
	w.SetContent(toolbar)

	w.addProjectView("Rufin", appConfig.RufinRepoPath, "rufin", "rufin_subtab")
	w.addProjectView("Igloo", appConfig.IglooRepoPath, "igloo", "igloo_subtab")
	w.addProjectView("nocblue", appConfig.NocblueRepoPath, "generic", "")
	for _, path := range appConfig.ProjectPaths {
		if !w.hasProject(path) {
			w.addProjectView(filepath.Base(path), path, "generic", "")
		}
	}
	var selected string
	config.Get("selected_project", &selected)
	for i, p := range w.projects {
		if selected == p.name || selected == p.repo {
			w.notebook.SetCurrentPage(i)
		}
	}
	w.notebook.ConnectSwitchPage(func(_ gtk.Widgetter, page uint) {
		if int(page) < len(w.projects) {
			p := w.projects[page]
			if p.kind == "generic" {
				config.Set("selected_project", p.repo)
			} else {
				config.Set("selected_project", p.name)
			}
			glib.IdleAdd(func() { w.refreshActive() })
		}
	})
	// The current subtab's working tree follows the disk while the window is shown.
	glib.TimeoutAdd(30000, func() bool {
		if w.IsActive() {
			p := w.active()
			if i := p.notebook.CurrentPage(); i >= 0 && i < len(p.tabs) && !p.dock.Runner.Running() {
				p.tabs[i].refresh()
			}
		}
		return true
	})
	w.refreshActive()
	return w
}

func (w *Window) addProjectView(name, repo, kind, stateKey string) *project {
	if stateKey == "" {
		real, _ := filepath.EvalSymlinks(repo)
		stateKey = "project_subtab:" + real
	}
	p := &project{name: name, repo: repo, kind: kind, stateKey: stateKey, notebook: gtk.NewNotebook()}
	switch kind {
	case "rufin":
		p.tabs = []tab{placeholder("Publish PR"), placeholder("Release"), placeholder("Changes")}
	case "igloo":
		commit := NewCommitTab(repo)
		p.tabs = []tab{{"Commit & Push", commit.Root, commit.Refresh}, placeholder("Release"), placeholder("Changes")}
	default:
		commit := NewCommitTab(repo)
		p.tabs = []tab{{"Commit & Push", commit.Root, commit.Refresh}}
		if name == "nocblue" || appConfig.HasPackages(repo) {
			p.tabs = append(p.tabs, placeholder("Packages"))
		}
		p.tabs = append(p.tabs, placeholder("Release"), placeholder("Changes"))
	}
	for _, t := range p.tabs {
		p.notebook.AppendPage(t.widget, gtk.NewLabel(t.label))
	}
	var saved string
	if config.Get(stateKey, &saved) {
		for i, t := range p.tabs {
			if t.label == saved {
				p.notebook.SetCurrentPage(i)
			}
		}
	}
	p.notebook.ConnectSwitchPage(func(_ gtk.Widgetter, page uint) {
		if int(page) < len(p.tabs) {
			config.Set(p.stateKey, p.tabs[page].label)
			glib.IdleAdd(func() { p.tabs[page].refresh() })
		}
	})
	p.dock = NewTerminal(repo, "git-tools", "")
	p.dock.Root.SetSizeRequest(-1, 220)
	p.dock.OnStarted = func() { w.reveal.SetRevealChild(true) }
	p.dock.OnRestored = func(_ daemon.Record, running, _ bool) {
		if running {
			w.reveal.SetRevealChild(true)
		}
	}
	p.dock.OnFinished = func(int, daemon.Record) {
		if w.active() == p {
			w.refreshActive()
		}
	}
	w.docks.AddNamed(p.dock.Root, repo)
	label := gtk.NewLabel(name)
	label.SetTooltipText(repo)
	w.notebook.AppendPage(p.notebook, label)
	w.projects = append(w.projects, p)
	p.dock.Attach()
	return p
}

func placeholder(label string) tab {
	page := adw.NewStatusPage()
	page.SetTitle(label)
	page.SetDescription("Not ported yet.")
	return tab{label, page, func() {}}
}

func (w *Window) active() *project {
	i := w.notebook.CurrentPage()
	if i < 0 || i >= len(w.projects) {
		return w.projects[0]
	}
	return w.projects[i]
}

// refreshActive updates the header for the active project and refreshes its current subtab.
func (w *Window) refreshActive() {
	p := w.active()
	if git.IsRepo(p.repo) {
		w.title.SetTitle(p.name)
		w.title.SetSubtitle("branch: " + git.CurrentBranch(p.repo))
	} else {
		w.title.SetTitle(p.name)
		w.title.SetSubtitle("not a git repo")
	}
	w.title.SetTooltipText("Repository path: " + p.repo)
	visible := map[string][]string{
		"rufin":   {"refresh", "android", "sync", "settings"},
		"igloo":   {"refresh", "restart", "android", "fetch", "pull", "settings"},
		"generic": {"refresh", "fetch", "pull", "settings"},
	}[p.kind]
	for name, b := range w.buttons {
		b.SetVisible(slices.Contains(visible, name))
	}
	if p.kind == "igloo" {
		w.buttons["android"].SetTooltipText("Build & Install Android App (just build-android)")
	} else {
		w.buttons["android"].SetTooltipText("Build & Install Android App (just build apk-dev, just install android)")
	}
	w.docks.SetVisibleChildName(p.repo)
	w.reveal.SetRevealChild(p.dock.Runner.Running() || w.reveal.RevealChild() && p.dock.Runner.Path != "")
	if i := p.notebook.CurrentPage(); i >= 0 && i < len(p.tabs) {
		p.tabs[i].refresh()
	}
	p.dock.Attach()
}

// runTool runs a top-bar command for the active project in its Git commands dock.
func (w *Window) runTool(command ...string) {
	p := w.active()
	w.reveal.SetRevealChild(true)
	if p.dock.Runner.Running() {
		return
	}
	p.dock.Clear()
	p.dock.Runner.Start(command, nil)
}

// addProject asks for a folder and adds its repository as a project.
func (w *Window) addProject() {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Add Project")
	dialog.SetInitialFolder(gio.NewFileForPath(filepath.Dir(w.active().repo)))
	dialog.SelectFolder(context(), &w.Window, func(result gio.AsyncResulter) {
		folder, err := dialog.SelectFolderFinish(result)
		if err != nil || folder == nil {
			return
		}
		top, err := git.TopLevel(folder.Path())
		if err != nil {
			alert(w, "Not a Git Repository", "Select a folder containing a Git working tree.")
			return
		}
		for i, p := range w.projects {
			if sameDir(p.repo, top) {
				w.notebook.SetCurrentPage(i)
				return
			}
		}
		appConfig.ProjectPaths = append(appConfig.ProjectPaths, top)
		_ = appConfig.Save()
		w.addProjectView(filepath.Base(top), top, "generic", "")
		w.notebook.SetCurrentPage(len(w.projects) - 1)
	})
}

// hasProject reports whether a repository already has a tab.
func (w *Window) hasProject(repo string) bool {
	for _, p := range w.projects {
		if sameDir(p.repo, repo) {
			return true
		}
	}
	return false
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// openSettings is filled in with the Settings step.
func (w *Window) openSettings() {}
