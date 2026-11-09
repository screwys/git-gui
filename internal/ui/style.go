package ui

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Colours carry meaning everywhere: green added or passed, red deleted or failed, blue running,
// amber modified or waiting, grey idle, purple renamed.
const css = `
.pill { border-radius: 999px; padding: 2px 10px; font-size: 0.85em; font-weight: bold; }
.pill.idle { background: alpha(@view_fg_color, 0.1); color: alpha(@view_fg_color, 0.7); }
.pill.running { background: alpha(@accent_bg_color, 0.2); color: @accent_color; }
.pill.success { background: alpha(@success_bg_color, 0.2); color: @success_color; }
.pill.failed { background: alpha(@error_bg_color, 0.2); color: @error_color; }
.pill.waiting { background: alpha(@warning_bg_color, 0.2); color: @warning_color; }
.added { color: @success_color; }
.deleted { color: @error_color; }
.modified { color: @warning_color; }
.renamed { color: #c061cb; }
.untracked { color: alpha(@view_fg_color, 0.55); }
.status-letter { font-family: monospace; font-weight: bold; min-width: 1.2em; }
.console, .diff { font-family: monospace; font-size: 0.92em; }
.gutter { font-family: monospace; font-size: 0.92em; }
.dim { color: alpha(@view_fg_color, 0.55); }
`

func loadCSS() {
	provider := gtk.NewCSSProvider()
	provider.LoadFromString(css)
	gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
}

// setClasses replaces a widget's pill state class.
func setPill(label *gtk.Label, text, state string) {
	label.SetText(text)
	label.SetTooltipText(text)
	for _, c := range []string{"idle", "running", "success", "failed", "waiting"} {
		label.RemoveCSSClass(c)
	}
	label.AddCSSClass(state)
}

func newPill() *gtk.Label {
	label := gtk.NewLabel("Idle")
	label.AddCSSClass("pill")
	label.AddCSSClass("idle")
	return label
}

// statusClass is the CSS class for a file status letter.
func statusClass(status string) string {
	switch status {
	case "A":
		return "added"
	case "D":
		return "deleted"
	case "R":
		return "renamed"
	case "U":
		return "untracked"
	}
	return "modified"
}
