// Command git-gui is a desktop app for committing, publishing pull requests and cutting releases.
//
// Without arguments it opens the window. "git-gui install" installs the binary, menu entry and
// the daemon's user service. "git-gui daemon" is that always-on service: it runs commands and
// watches pull requests and releases. "supervise" is started by the daemon,
// "push" is the last step of a Commit & Push job, and "release" tags and publishes a release of a
// project. "publish", "sync" and "status" publish from a shared checkout, "rufin-release" releases
// Rufin, and "wait-pr" waits for the daemon's verdict on a PR.
package main

import (
	_ "embed"
	"os"

	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/tasks"
	"github.com/screwys/git-gui/internal/ui"
)

var (
	//go:embed io.github.screwys.GitGui.desktop
	desktopEntry []byte
	//go:embed io.github.screwys.GitGui.svg
	icon []byte
	//go:embed git-gui-daemon.service
	unit []byte
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "daemon":
			daemon.Run()
			return
		case "supervise":
			os.Exit(daemon.Supervise(os.Args[2]))
		case "push":
			os.Exit(tasks.Push(os.Args[2:]))
		case "wait-pr":
			os.Exit(tasks.WaitPR(os.Args[2:]))
		case "publish":
			os.Exit(tasks.Publish(os.Args[2:]))
		case "sync":
			os.Exit(tasks.Sync(os.Args[2:]))
		case "status":
			os.Exit(tasks.Status(os.Args[2:]))
		case "rufin-release":
			os.Exit(tasks.RufinRelease(os.Args[2:]))
		case "install":
			os.Exit(tasks.Install(tasks.Files{Desktop: desktopEntry, Icon: icon, Unit: unit}))
		case "release":
			os.Exit(tasks.Release(os.Args[2:]))
		}
	}
	os.Exit(ui.Run(os.Args))
}
