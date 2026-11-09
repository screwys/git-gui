package ui

import (
	stdcontext "context"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
)

func context() stdcontext.Context { return stdcontext.Background() }

// copyText puts text on the clipboard.
func copyText(text string) {
	gdk.DisplayGetDefault().Clipboard().SetText(text)
}
