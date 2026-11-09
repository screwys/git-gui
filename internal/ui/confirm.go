package ui

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// detail is one "Label: value" row of a confirmation.
type detail struct{ label, value string }

// confirm asks before a form starts a job. It lists the details, shows an optional preview and
// calls run when the user picks the action.
func confirm(parent gtk.Widgetter, title string, details []detail, previewLabel, preview, action string, run func()) {
	var body []string
	for _, d := range details {
		body = append(body, "<b>"+glib.MarkupEscapeText(d.label)+":</b> "+glib.MarkupEscapeText(d.value))
	}
	dialog := adw.NewAlertDialog(title, "")
	dialog.SetBodyUseMarkup(true)
	dialog.SetBody(strings.Join(body, "\n"))
	if preview != "" {
		box := gtk.NewBox(gtk.OrientationVertical, 4)
		label := gtk.NewLabel(previewLabel)
		label.SetXAlign(0)
		label.AddCSSClass("dim")
		view := gtk.NewTextView()
		view.SetEditable(false)
		view.SetWrapMode(gtk.WrapWordChar)
		view.Buffer().SetText(preview)
		scroll := gtk.NewScrolledWindow()
		scroll.SetChild(view)
		scroll.SetMinContentHeight(75)
		scroll.SetMaxContentHeight(200)
		scroll.SetPropagateNaturalHeight(true)
		box.Append(label)
		box.Append(scroll)
		dialog.SetExtraChild(box)
	}
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("run", action)
	dialog.SetResponseAppearance("run", adw.ResponseSuggested)
	dialog.SetDefaultResponse("run")
	dialog.SetCloseResponse("cancel")
	dialog.ConnectResponse(func(response string) {
		if response == "run" {
			run()
		}
	})
	dialog.Present(parent)
}

// alert shows a message with one button.
func alert(parent gtk.Widgetter, heading, body string) {
	dialog := adw.NewAlertDialog(heading, body)
	dialog.AddResponse("ok", "OK")
	dialog.Present(parent)
}
