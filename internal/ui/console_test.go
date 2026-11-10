package ui

import (
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func TestConsoleAutoScroll(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
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
		// Allow layout, scroll animations and incremental line validation to finish.
		until := time.Now().Add(400 * time.Millisecond)
		for time.Now().Before(until) {
			glib.MainContextDefault().Iteration(false)
			time.Sleep(time.Millisecond)
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
