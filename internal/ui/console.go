package ui

import (
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const maxConsoleLines = 10000

var ansiColours = map[int]string{
	30: "#6e7681", 31: "#ff7b72", 32: "#3fb950", 33: "#d29922", 34: "#58a6ff", 35: "#bc8cff", 36: "#39c5cf", 37: "#b1bac4",
	90: "#8b949e", 91: "#ffa198", 92: "#56d364", 93: "#e3b341", 94: "#79c0ff", 95: "#d2a8ff", 96: "#56d4dd", 97: "#f0f6fc",
}

// console is a read-only text view that renders command output: SGR bold and foreground
// colours, other escape sequences removed, and carriage returns redrawing the current line.
type console struct {
	view          *gtk.TextView
	buffer        *gtk.TextBuffer
	scroll        *gtk.ScrolledWindow
	tags          map[string]*gtk.TextTag
	bold          bool
	fg            int
	escape        string // an escape sequence split across chunks
	pendingCR     bool
	followPending bool
	AutoScroll    bool
}

func newConsole() *console {
	c := &console{view: gtk.NewTextView(), tags: map[string]*gtk.TextTag{}, AutoScroll: true}
	c.buffer = c.view.Buffer()
	c.view.SetEditable(false)
	c.view.SetCursorVisible(false)
	c.view.SetMonospace(true)
	c.view.SetWrapMode(gtk.WrapWordChar)
	c.view.AddCSSClass("console")
	c.view.SetLeftMargin(8)
	c.view.SetTopMargin(6)
	c.scroll = gtk.NewScrolledWindow()
	c.scroll.SetChild(c.view)
	c.scroll.SetVExpand(true)
	// Wrapped line heights keep changing during GTK's incremental layout.
	// Follow range changes and position corrections after resizing or showing a tab.
	c.scroll.VAdjustment().ConnectChanged(c.follow)
	c.scroll.VAdjustment().ConnectValueChanged(c.follow)
	table := c.buffer.TagTable()
	bold := gtk.NewTextTag("bold")
	bold.SetObjectProperty("weight", 700)
	table.Add(bold)
	c.tags["bold"] = bold
	for code, colour := range ansiColours {
		tag := gtk.NewTextTag("fg" + strconv.Itoa(code))
		tag.SetObjectProperty("foreground", colour)
		table.Add(tag)
		c.tags["fg"+strconv.Itoa(code)] = tag
	}
	for name, colour := range map[string]string{"info": "#58a6ff", "error": "#ff7b72"} {
		tag := gtk.NewTextTag(name)
		tag.SetObjectProperty("foreground", colour)
		table.Add(tag)
		c.tags[name] = tag
	}
	return c
}

// Append renders a chunk of command output.
func (c *console) Append(text string) {
	text = c.escape + text
	c.escape = ""
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			c.insert(run.String())
			run.Reset()
		}
	}
	for i := 0; i < len(text); i++ {
		switch ch := text[i]; ch {
		case '\x1b':
			end := sequenceEnd(text, i)
			if end < 0 {
				flush()
				c.escape = text[i:]
				return
			}
			if text[i+1] == '[' && text[end] == 'm' {
				flush()
				c.applySGR(text[i+2 : end])
			}
			i = end
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				continue
			}
			flush()
			c.pendingCR = true
		default:
			run.WriteByte(ch)
		}
	}
	flush()
	c.trim()
	c.follow()
}

// AppendStyled adds a line of app text, such as a schedule message, in a colour.
func (c *console) AppendStyled(text, tag string) {
	end := c.buffer.EndIter()
	offset := end.Offset()
	c.buffer.Insert(end, text)
	start := c.buffer.IterAtOffset(offset)
	c.buffer.ApplyTag(c.tags[tag], start, c.buffer.EndIter())
	c.trim()
	c.follow()
}

func (c *console) insert(text string) {
	if c.pendingCR {
		c.pendingCR = false
		if !strings.HasPrefix(text, "\n") {
			end := c.buffer.EndIter()
			start := c.buffer.EndIter()
			start.SetLineOffset(0)
			c.buffer.Delete(start, end)
		}
	}
	end := c.buffer.EndIter()
	offset := end.Offset()
	c.buffer.Insert(end, text)
	if !c.bold && c.fg == 0 {
		return
	}
	start := c.buffer.IterAtOffset(offset)
	stop := c.buffer.EndIter()
	if c.bold {
		c.buffer.ApplyTag(c.tags["bold"], start, stop)
	}
	if tag, ok := c.tags["fg"+strconv.Itoa(c.fg)]; ok {
		c.buffer.ApplyTag(tag, start, stop)
	}
}

func (c *console) applySGR(params string) {
	if params == "" {
		c.bold, c.fg = false, 0
		return
	}
	for _, part := range strings.Split(params, ";") {
		n, _ := strconv.Atoi(part)
		switch {
		case n == 0:
			c.bold, c.fg = false, 0
		case n == 1:
			c.bold = true
		case n == 22:
			c.bold = false
		case n == 39:
			c.fg = 0
		case (n >= 30 && n <= 37) || (n >= 90 && n <= 97):
			c.fg = n
		}
	}
}

// sequenceEnd is the index of an escape sequence's last byte, or -1 when the text ends first.
// CSI sequences end at a byte in @..~; OSC sequences end at BEL or ESC \.
func sequenceEnd(text string, start int) int {
	if start+1 >= len(text) {
		return -1
	}
	switch text[start+1] {
	case '[':
		for i := start + 2; i < len(text); i++ {
			if text[i] >= 0x40 && text[i] <= 0x7e {
				return i
			}
		}
		return -1
	case ']':
		for i := start + 2; i < len(text); i++ {
			if text[i] == '\x07' {
				return i
			}
			if text[i] == '\x1b' && i+1 < len(text) && text[i+1] == '\\' {
				return i + 1
			}
		}
		return -1
	}
	return start + 1
}

func (c *console) trim() {
	if extra := c.buffer.LineCount() - maxConsoleLines; extra > 0 {
		end, _ := c.buffer.IterAtLine(extra)
		c.buffer.Delete(c.buffer.StartIter(), end)
	}
}

func (c *console) follow() {
	if !c.AutoScroll || c.followPending {
		return
	}
	c.followPending = true
	// Wait until GTK finishes configuring the adjustment before setting its value.
	glib.IdleAdd(func() bool {
		c.followPending = false
		if c.AutoScroll {
			adj := c.scroll.VAdjustment()
			adj.SetValue(adj.Upper() - adj.PageSize())
		}
		return false
	})
}

// Clear empties the console.
func (c *console) Clear() {
	c.buffer.SetText("")
	c.bold, c.fg, c.escape, c.pendingCR = false, 0, "", false
}

// Text is the whole console content.
func (c *console) Text() string {
	return c.buffer.Text(c.buffer.StartIter(), c.buffer.EndIter(), false)
}
