// Package ui is git-gui's GTK window. It draws state and sends requests to the daemon.
package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/tasks"
)

// appConfig is the settings every view reads; Settings edits it and saves it.
var appConfig config.Config

// Run opens the window and returns the exit status.
func Run(args []string) int {
	appConfig = config.Load()
	app := adw.NewApplication(tasks.AppID, 0)
	var window *Window
	app.ConnectActivate(func() {
		// A second launch raises the open window.
		if window == nil {
			window = newWindow(app)
		}
		window.Present()
	})
	return app.Run(args)
}
