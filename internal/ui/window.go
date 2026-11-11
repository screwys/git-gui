package ui

import (
	"fmt"
	"os"
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
	local   func()    // the 30 s working-tree reload; nil when the tab has none
	term    *Terminal // where the project's Commands page shows while the tab is current
}

// project is one repository's set of subtabs.
type project struct {
	name, repo, kind string // kind is rufin, igloo or generic
	stateKey         string // where the selected subtab is saved
	notebook         *gtk.Notebook
	tabs             []tab
	commands         *jobPage  // top-bar commands: fetch, pull, sync, Android, restart
	host             *Terminal // the terminal showing commands
}

// Window is the main window.
type Window struct {
	*adw.ApplicationWindow
	building bool // projects are being (re)built; tab switches are not the user's
	title    *adw.WindowTitle
	projects []*project
	notebook *gtk.Notebook
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
		switch {
		case icon != "" && label != "":
			content := adw.NewButtonContent()
			content.SetIconName(icon)
			content.SetLabel(label)
			b.SetChild(content)
		case icon != "":
			b.SetIconName(icon)
		default:
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
	add("sync", "", "Sync", "git-gui sync: bring main up to origin/main, merging local edits", false, func() {
		if self, err := os.Executable(); err == nil {
			w.runTool(self, "sync")
		}
	})
	add("refresh", "view-refresh-symbolic", "", "Reload changes, diffs, and branches", true, func() { w.refreshActive() })
	add("android", "phone-symbolic", "Android", "", true, func() {
		if w.active().kind == "igloo" {
			w.runTool("just", "build-android")
		} else {
			w.runTool("bash", "-c", "just build apk-dev && just install android")
		}
	})
	add("restart", "system-reboot-symbolic", "Restart", "Restart Server (just restart)", true, func() { w.runTool("just", "restart") })

	w.notebook = gtk.NewNotebook()
	w.notebook.SetScrollable(true)
	plus := gtk.NewButtonFromIconName("list-add-symbolic")
	plus.SetTooltipText("Add project")
	plus.AddCSSClass("flat")
	plus.ConnectClicked(w.addProject)
	w.notebook.SetActionWidget(plus, gtk.PackEnd)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(header)
	toolbar.SetContent(w.notebook)
	w.SetContent(toolbar)

	w.buildProjects()
	w.notebook.ConnectSwitchPage(func(_ gtk.Widgetter, page uint) {
		if !w.building && int(page) < len(w.projects) {
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
			if i := p.notebook.CurrentPage(); i >= 0 && i < len(p.tabs) && p.tabs[i].local != nil && !p.commands.Runner.Running() {
				p.tabs[i].local()
			}
		}
		return true
	})
	w.refreshActive()
	return w
}

// buildProjects adds the built-in and added projects and selects the saved one.
func (w *Window) buildProjects() {
	w.building = true
	defer func() { w.building = false }()
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
}

// rebuildProjects replaces every project view, after Settings changed paths or tabs.
func (w *Window) rebuildProjects() {
	w.building = true
	for w.notebook.NPages() > 0 {
		w.notebook.RemovePage(-1)
	}
	w.projects = nil
	w.buildProjects()
}

func (w *Window) addProjectView(name, repo, kind, stateKey string) *project {
	if stateKey == "" {
		real, _ := filepath.EvalSymlinks(repo)
		stateKey = "project_subtab:" + real
	}
	p := &project{name: name, repo: repo, kind: kind, stateKey: stateKey, notebook: gtk.NewNotebook()}
	changes := NewChangesTab(repo)
	changesTab := withTerminal(repo, tab{"Changes", changes.Root, changes.Refresh, changes.Refresh, nil})
	switch kind {
	case "rufin":
		publish := NewPublishTab(repo)
		release := NewRufinReleaseTab(repo)
		p.tabs = []tab{{"Publish PR", publish.Root, publish.Refresh, publish.RefreshFiles, publish.term},
			{"Release", release.Root, release.Refresh, nil, release.out.term}, changesTab}
	case "igloo":
		commit := NewCommitTab(repo)
		release := NewIglooReleaseTab(repo)
		p.tabs = []tab{{"Commit & Push", commit.Root, commit.Refresh, commit.Refresh, commit.term},
			{"Release", release.Root, release.Refresh, nil, release.out.term}, changesTab}
	default:
		commit := NewCommitTab(repo)
		p.tabs = []tab{{"Commit & Push", commit.Root, commit.Refresh, commit.Refresh, commit.term}}
		if name == "nocblue" || appConfig.HasPackages(repo) {
			packages := NewPackagesTab(repo, name == "nocblue", commit.FillSubject)
			p.tabs = append(p.tabs, withTerminal(repo, tab{"Packages", packages.Root, packages.Refresh, nil, nil}))
		}
		release := NewGenericReleaseTab(repo)
		p.tabs = append(p.tabs, tab{"Release", release.Root, release.Refresh, nil, release.out.term}, changesTab)
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
			p.showCommands(p.tabs[page].term)
			glib.IdleAdd(func() { p.tabs[page].refresh() })
		}
	})
	p.commands = newCommandsPage(repo)
	p.commands.OnFinished = func(int, daemon.Record) {
		if w.active() == p {
			w.refreshActive()
		}
	}
	p.showCommands(p.tabs[max(0, p.notebook.CurrentPage())].term)
	label := gtk.NewLabel(name)
	label.SetTooltipText(repo)
	w.notebook.AppendPage(p.notebook, label)
	w.projects = append(w.projects, p)
	p.commands.Runner.Attach()
	return p
}

// withTerminal puts a terminal below a tab that has none of its own.
func withTerminal(repo string, t tab) tab {
	t.term = NewShellTerminal(repo)
	t.term.Root.SetSizeRequest(-1, 220)
	t.term.Root.SetMarginStart(6)
	t.term.Root.SetMarginEnd(6)
	t.term.Root.SetMarginBottom(6)
	paned := gtk.NewPaned(gtk.OrientationVertical)
	paned.SetStartChild(t.widget)
	paned.SetEndChild(t.term.Root)
	paned.SetResizeEndChild(false)
	paned.SetShrinkEndChild(false)
	t.widget = paned
	t.term.Attach()
	return t
}

// showCommands moves the Commands page into the terminal of the tab being shown.
func (p *project) showCommands(term *Terminal) {
	if p.host != term {
		term.host(p.commands, p.host)
		p.host = term
	}
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
	if i := p.notebook.CurrentPage(); i >= 0 && i < len(p.tabs) {
		p.showCommands(p.tabs[i].term)
		p.tabs[i].refresh()
	}
	p.commands.Runner.Attach()
}

// runTool runs a top-bar command for the active project on its Commands page.
func (w *Window) runTool(command ...string) {
	p := w.active()
	if p.commands.Runner.Running() {
		p.commands.show()
		return
	}
	p.commands.Clear()
	p.commands.Runner.Start(command, nil)
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

// openSettings edits the settings and rebuilds the projects when their paths or tabs changed.
func (w *Window) openSettings() {
	before := projectLayout()
	showSettings(w, w.active().repo, func() {
		if projectLayout() != before {
			w.rebuildProjects()
		}
		w.refreshActive()
	})
}

// projectLayout is what decides which projects and tabs exist.
func projectLayout() string {
	return fmt.Sprint(appConfig.RufinRepoPath, appConfig.IglooRepoPath, appConfig.NocblueRepoPath,
		appConfig.ProjectPaths, appConfig.PackageProjectPaths)
}
