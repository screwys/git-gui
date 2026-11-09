package ui

import (
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/daemon"
)

// daemonState calls its listeners on the main loop after git-gui-daemon rewrites daemon.json,
// which it replaces on every pass, instead of each view polling the file.
var daemonState struct {
	monitor   gio.FileMonitorrer
	listeners []func(daemon.State)
	queued    bool
}

func onDaemonState(listener func(daemon.State)) {
	s := &daemonState
	if s.monitor == nil {
		monitor, err := gio.NewFileForPath(daemon.StatePath()).MonitorFile(context(), gio.FileMonitorNone)
		if err != nil {
			return
		}
		s.monitor = monitor
		gio.BaseFileMonitor(monitor).ConnectChanged(func(_, _ gio.Filer, _ gio.FileMonitorEvent) {
			if !s.queued {
				s.queued = true
				glib.IdleAdd(func() {
					s.queued = false
					state := client.State()
					for _, f := range s.listeners {
						f(state)
					}
				})
			}
		})
	}
	s.listeners = append(s.listeners, listener)
}
