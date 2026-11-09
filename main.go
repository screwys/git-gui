// Command git-gui is a desktop app for committing, publishing pull requests and cutting releases.
//
// Without arguments it opens the window. "git-gui daemon" is the always-on user service that runs
// commands and watches pull requests; "supervise" and "publish-checks" are started by the daemon,
// and "push" is the last step of a Commit & Push job.
package main

import (
	"os"

	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/tasks"
	"github.com/screwys/git-gui/internal/ui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "daemon":
			daemon.Run()
			return
		case "supervise":
			os.Exit(daemon.Supervise(os.Args[2]))
		case "publish-checks":
			os.Exit(daemon.PublishChecks(os.Args[2], os.Args[3], os.Args[4]))
		case "push":
			os.Exit(tasks.Push(os.Args[2:]))
		}
	}
	os.Exit(ui.Run(os.Args))
}
