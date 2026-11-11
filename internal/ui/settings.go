package ui

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/config"
)

// showSettings opens Preferences. Every change is saved at once; onClose runs after it closes so
// the window can apply new paths. activeRepo is the active project's repository; an added
// project gets its own group with the Packages tab switch.
func showSettings(parent gtk.Widgetter, activeRepo string, onClose func()) {
	dialog := adw.NewPreferencesDialog()
	dialog.SetTitle("Preferences")
	page := adw.NewPreferencesPage()
	dialog.Add(page)
	save := func() { _ = appConfig.Save() }
	defaults := config.Defaults()
	var resets []func() // Reset Defaults puts every field back to these

	// text adds a field; an emptied field never overwrites the stored value.
	text := func(group *adw.PreferencesGroup, title string, value *string, initial string, browse func(row *adw.EntryRow)) {
		row := adw.NewEntryRow()
		row.SetTitle(title)
		row.SetText(*value)
		row.ConnectChanged(func() {
			if v := strings.TrimSpace(row.Text()); v != "" {
				*value = v
				save()
			}
		})
		if browse != nil {
			button := gtk.NewButtonWithLabel("Browse...")
			button.SetVAlign(gtk.AlignCenter)
			button.AddCSSClass("flat")
			button.ConnectClicked(func() { browse(row) })
			row.AddSuffix(button)
		}
		group.Add(row)
		resets = append(resets, func() { row.SetText(initial) })
	}
	toggle := func(group *adw.PreferencesGroup, title string, value *bool, initial bool) {
		row := adw.NewSwitchRow()
		row.SetUseMarkup(false) // titles hold "&"
		row.SetTitle(title)
		row.SetActive(*value)
		row.NotifyProperty("active", func() {
			*value = row.Active()
			save()
		})
		group.Add(row)
		resets = append(resets, func() { row.SetActive(initial) })
	}

	repos := adw.NewPreferencesGroup()
	repos.SetTitle("Project Repositories")
	folder := func(row *adw.EntryRow) {
		chooser := gtk.NewFileDialog()
		chooser.SetTitle("Select Repository")
		chooser.SetInitialFolder(gio.NewFileForPath(row.Text()))
		chooser.SelectFolder(context(), windowOf(parent), func(result gio.AsyncResulter) {
			if dir, err := chooser.SelectFolderFinish(result); err == nil && dir != nil {
				row.SetText(dir.Path())
			}
		})
	}
	text(repos, "Rufin Repository Path", &appConfig.RufinRepoPath, defaults.RufinRepoPath, folder)
	text(repos, "Igloo Repository Path", &appConfig.IglooRepoPath, defaults.IglooRepoPath, folder)
	text(repos, "nocblue Repository Path", &appConfig.NocblueRepoPath, defaults.NocblueRepoPath, folder)
	page.Add(repos)

	if added(activeRepo) {
		project := adw.NewPreferencesGroup()
		project.SetTitle(glib.MarkupEscapeText(filepath.Base(activeRepo))) // group titles are markup
		row := adw.NewSwitchRow()
		row.SetTitle("Packages")
		row.SetActive(appConfig.HasPackages(activeRepo))
		row.NotifyProperty("active", func() {
			real := realPath(activeRepo)
			paths := slices.DeleteFunc(appConfig.PackageProjectPaths, func(p string) bool { return p == real })
			if row.Active() {
				paths = append(paths, real)
			}
			appConfig.PackageProjectPaths = paths
			save()
		})
		project.Add(row)
		page.Add(project)
		resets = append(resets, func() { row.SetActive(false) })
	}

	publishing := adw.NewPreferencesGroup()
	publishing.SetTitle("Publishing")
	publishing.SetDescription("Run in the publish worktree: generate before committing, check before pushing with the base branch as its argument.")
	text(publishing, "Generate Command", &appConfig.PublishGenerate, defaults.PublishGenerate, nil)
	text(publishing, "Check Command", &appConfig.PublishCheck, defaults.PublishCheck, nil)
	page.Add(publishing)

	options := adw.NewPreferencesGroup()
	options.SetTitle("Defaults")
	text(options, "Base Branch", &appConfig.DefaultBaseBranch, defaults.DefaultBaseBranch, nil)
	toggle(options, "Squash auto-merge by default", &appConfig.DefaultAutoMerge, defaults.DefaultAutoMerge)
	toggle(options, "Wrap diff & editor lines by default", &appConfig.WrapDiff, defaults.WrapDiff)
	page.Add(options)

	reset := adw.NewPreferencesGroup()
	resetRow := adw.NewButtonRow()
	resetRow.SetTitle("Reset Defaults")
	resetRow.ConnectActivated(func() {
		for _, r := range resets {
			r()
		}
		save()
	})
	reset.Add(resetRow)
	page.Add(reset)

	dialog.ConnectClosed(func() {
		save()
		onClose()
	})
	dialog.Present(parent)
}

// added reports whether repo is a project the user added, not one of the three built in.
func added(repo string) bool {
	if repo == "" || !slices.ContainsFunc(appConfig.ProjectPaths, func(p string) bool { return sameDir(p, repo) }) {
		return false
	}
	return !sameDir(repo, appConfig.RufinRepoPath) && !sameDir(repo, appConfig.IglooRepoPath) && !sameDir(repo, appConfig.NocblueRepoPath)
}
