package ui

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

var finishedSchedule = []string{"started", "failed", "closed", "cancelled"}

// ScheduleFollower asks the daemon to run a command after a PR merges and reports the
// schedule's progress as the daemon rewrites daemon.json.
type ScheduleFollower struct {
	cwd, key  string
	Schedule  daemon.Schedule
	id        string
	listening bool
	OnChanged func(daemon.Schedule)
}

// NewScheduleFollower follows schedules of one command slot.
func NewScheduleFollower(cwd, key string) *ScheduleFollower {
	return &ScheduleFollower{cwd: cwd, key: key}
}

// Active reports an unfinished schedule.
func (f *ScheduleFollower) Active() bool {
	return f.Schedule.ID != "" && !slices.Contains(finishedSchedule, f.Schedule.Status)
}

// Submit schedules job to run once PR number merges.
func (f *ScheduleFollower) Submit(number int, job daemon.JobSpec) error {
	resp, err := client.Request(daemon.Request{Op: "schedule", Schedule: &daemon.Schedule{Number: number, Job: job}})
	if err != nil {
		return err
	}
	f.follow(resp.ID)
	return nil
}

// Restore follows the slot's unfinished schedule after the window opens.
func (f *ScheduleFollower) Restore() {
	if found := client.Schedules(f.cwd, f.key); len(found) > 0 && !slices.Contains(finishedSchedule, found[0].Status) {
		f.follow(found[0].ID)
	}
}

// Cancel cancels the followed schedule.
func (f *ScheduleFollower) Cancel() error {
	if f.id == "" {
		return nil
	}
	_, err := client.Request(daemon.Request{Op: "cancel", ID: f.id})
	f.poll()
	return err
}

func (f *ScheduleFollower) follow(id string) {
	f.id, f.Schedule = id, daemon.Schedule{}
	f.poll()
	if !f.listening {
		f.listening = true
		onDaemonState(func(daemon.State) {
			if f.id != "" {
				f.poll()
			}
		})
	}
}

// poll reports changes; false once the schedule finished.
func (f *ScheduleFollower) poll() bool {
	for _, s := range client.Schedules(f.cwd, f.key) {
		if s.ID != f.id {
			continue
		}
		if s.Status != f.Schedule.Status || s.Message != f.Schedule.Message || s.JobPath != f.Schedule.JobPath {
			f.Schedule = s
			if f.OnChanged != nil {
				f.OnChanged(s)
			}
		}
		return !slices.Contains(finishedSchedule, s.Status)
	}
	return true
}

// scheduleState is the pill and label state of a schedule status.
func scheduleState(status string) string {
	switch status {
	case "started":
		return "success"
	case "failed", "closed":
		return "failed"
	case "cancelled":
		return "idle"
	}
	return "waiting"
}

// PRPicker is the "... after PR merges:" row: a checkbox, an editable PR list and the
// schedule's status.
type PRPicker struct {
	Root      *gtk.Box
	Wait      *gtk.CheckButton
	combo     *gtk.ComboBoxText
	Status    *gtk.Label
	repo      string
	OnChanged func()
}

var prNumber = regexp.MustCompile(`(?:^#?(\d+)$)|(?:/pull/(\d+))|(?:^#(\d+) - )`)

// NewPRPicker builds the row; label is such as "Publish after PR merges:".
func NewPRPicker(repo, label string) *PRPicker {
	p := &PRPicker{Root: gtk.NewBox(gtk.OrientationVertical, 2), Wait: gtk.NewCheckButtonWithLabel(label),
		combo: gtk.NewComboBoxTextWithEntry(), Status: gtk.NewLabel(""), repo: repo}
	entry := p.entry()
	entry.SetPlaceholderText("PR #")
	entry.SetWidthChars(18)
	p.Status.AddCSSClass("dim")
	p.Status.SetXAlign(0)
	p.Status.SetWrap(true)
	// The status gets its own line so a narrow form doesn't cut it to "…".
	line := gtk.NewBox(gtk.OrientationHorizontal, 6)
	line.Append(p.Wait)
	line.Append(p.combo)
	p.Root.Append(line)
	p.Root.Append(p.Status)
	entry.ConnectChanged(func() {
		_, ok := p.Number()
		switch {
		case ok && !p.Wait.Active():
			p.Wait.SetActive(true)
		case strings.TrimSpace(entry.Text()) == "" && p.Wait.Active():
			p.Wait.SetActive(false)
		}
		p.changed()
	})
	p.Wait.ConnectToggled(p.changed)
	return p
}

func (p *PRPicker) entry() *gtk.Entry { return p.combo.Child().(*gtk.Entry) }

func (p *PRPicker) changed() {
	if p.OnChanged != nil {
		p.OnChanged()
	}
}

// Number is the chosen PR: a listed item, "#N", "N" or a URL with /pull/N.
func (p *PRPicker) Number() (int, bool) {
	m := prNumber.FindStringSubmatch(strings.TrimSpace(p.entry().Text()))
	for _, group := range m[min(1, len(m)):] {
		if n, err := strconv.Atoi(group); err == nil {
			return n, true
		}
	}
	return 0, false
}

// Waiting reports a ticked box with a PR chosen.
func (p *PRPicker) Waiting() bool {
	_, ok := p.Number()
	return p.Wait.Active() && ok
}

// SetNumber fills in a PR and ticks the box.
func (p *PRPicker) SetNumber(n int) { p.entry().SetText("#" + strconv.Itoa(n)) }

// ShowSchedule shows a schedule's message in its status colour.
func (p *PRPicker) ShowSchedule(s daemon.Schedule) {
	p.Status.SetText(s.Message)
	p.Status.SetTooltipText(s.Message)
	for _, c := range []string{"dim", "added", "deleted", "modified"} {
		p.Status.RemoveCSSClass(c)
	}
	p.Status.AddCSSClass(map[string]string{"success": "added", "failed": "deleted", "idle": "dim", "waiting": "modified"}[scheduleState(s.Status)])
}

// Refresh reloads the open PR list in the background.
func (p *PRPicker) Refresh() {
	repo := p.repo
	go func() {
		args := []string{"pr", "list", "--state", "open", "--limit", "30", "--json", "number,title"}
		if slug := git.RemoteSlug(repo); slug != "" {
			args = append(args, "--repo", slug)
		}
		cmd := exec.Command("gh", args...)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			return
		}
		var prs []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		}
		if json.Unmarshal(out, &prs) != nil {
			return
		}
		glib.IdleAdd(func() {
			text := p.entry().Text()
			p.combo.RemoveAll()
			for _, pr := range prs {
				p.combo.AppendText("#" + strconv.Itoa(pr.Number) + " - " + pr.Title)
			}
			p.entry().SetText(text)
		})
	}()
}
