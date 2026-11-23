package ui

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func TestConsoleAutoScroll(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// A private Broadway display keeps the test window off the user's desktop.
	broadwayd, err := exec.LookPath("gtk4-broadwayd")
	if err != nil {
		t.Skip("gtk4-broadwayd unavailable")
	}
	n := 100 + os.Getpid()%800
	display := ":" + strconv.Itoa(n)
	server := exec.Command(broadwayd, "--address", "127.0.0.1", "--port", strconv.Itoa(20000+n), display)
	if err := server.Start(); err != nil {
		t.Skip("gtk4-broadwayd failed:", err)
	}
	defer server.Process.Kill()
	socket := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "broadway"+strconv.Itoa(n)+".socket")
	for start := time.Now(); time.Since(start) < 5*time.Second; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(socket); err == nil {
			break
		}
	}
	t.Setenv("GDK_BACKEND", "broadway")
	t.Setenv("BROADWAY_DISPLAY", display)
	if !gtk.InitCheck() {
		t.Skip("GTK display unavailable")
	}
	c := newConsole()
	window := gtk.NewWindow()
	window.SetDefaultSize(600, 300)
	window.SetChild(c.scroll)
	window.Present()
	defer window.Destroy()
	settle := func() {
		t.Helper()
		// Wait for layout, scroll animations and incremental line validation to finish: follow
		// runs below validation's idle priority, so a fixed wait can end before it.
		quiet := time.Now()
		for start := time.Now(); time.Since(quiet) < 200*time.Millisecond && time.Since(start) < 10*time.Second; {
			if glib.MainContextDefault().Iteration(false) {
				quiet = time.Now()
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}
	bottom := func() {
		t.Helper()
		settle()
		adj := c.scroll.VAdjustment()
		if gap := adj.Upper() - adj.PageSize() - adj.Value(); math.Abs(gap) > 1 {
			t.Errorf("output stopped %.0f pixels above bottom", gap)
		}
	}
	settle()
	line := strings.Repeat("wrapped output ", 20) + "\n"
	c.Append(strings.Repeat(line, 800))
	bottom()
	window.SetDefaultSize(320, 300)
	bottom()
	c.view.SetWrapMode(gtk.WrapNone)
	bottom()
	c.view.SetWrapMode(gtk.WrapWordChar)
	bottom()
	c.scroll.SetVisible(false)
	c.Append(strings.Repeat(line, 200))
	settle()
	c.scroll.SetVisible(true)
	bottom()
	c.Append(strings.Repeat("trimmed output\n", maxConsoleLines))
	bottom()
	c.Append("more output after reaching the line limit\n")
	bottom()
	c.AutoScroll = false
	adj := c.scroll.VAdjustment()
	adj.SetValue(0)
	c.AppendStyled("manual scrolling\n", "info")
	settle()
	if adj.Value() > 1 {
		t.Error("auto-scroll moved the view while disabled")
	}
	c.AutoScroll = true
	c.follow()
	bottom()
	c.Clear()
	c.AppendStyled(strings.Repeat(line, 100), "info")
	bottom()
}
