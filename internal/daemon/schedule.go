package daemon

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/screwys/git-gui/internal/publish"
)

// Schedule runs a command after a pull request merges, or, for a queued publish (Branch), once
// every branch its queue commit waits for merged.
type Schedule struct {
	ID     string  `json:"id"`
	Repo   string  `json:"repo"`
	Number int     `json:"number"`
	Branch string  `json:"branch,omitempty"`
	Job    JobSpec `json:"job"`
	// Status is waiting, started, failed, closed or cancelled.
	Status  string `json:"status"`
	Message string `json:"message"`
	JobPath string `json:"job_path"` // the started command
	Updated int64  `json:"updated"`
}

func (s *Schedule) done() bool {
	return slices.Contains([]string{"started", "failed", "closed", "cancelled"}, s.Status)
}

func (s *Schedule) set(status, message string) {
	s.Status, s.Message, s.Updated = status, message, time.Now().Unix()
}

func (d *daemon) activeSchedules() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.state.Schedules {
		if !s.done() {
			return true
		}
	}
	return false
}

// addSchedule replaces any schedule for the same command slot, or the same queued branch.
func (d *daemon) addSchedule(s *Schedule) error {
	if s.Repo == "" {
		s.Repo = repoSlug(s.Job.Cwd)
	}
	if s.Repo == "" || (s.Number == 0) == (s.Branch == "") {
		return fmt.Errorf("schedule needs a GitHub repository and a PR number or a queued branch")
	}
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	s.ID = fmt.Sprintf("%x", id)
	if s.Branch != "" {
		s.set("waiting", "Queued")
	} else {
		s.set("waiting", fmt.Sprintf("Waiting for #%d to merge", s.Number))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, other := range d.state.Schedules {
		if !other.done() && other.Job.Cwd == s.Job.Cwd && other.Job.Key == s.Job.Key && other.Branch == s.Branch {
			other.set("cancelled", "Replaced")
		}
	}
	d.state.Schedules = append(d.state.Schedules, s)
	return d.save()
}

// cancelSchedule cancels a schedule; a queued publish leaves the queue, its edits stay.
func (d *daemon) cancelSchedule(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.state.Schedules {
		if s.ID != id || s.done() {
			continue
		}
		if s.Branch != "" {
			if err := publish.Dequeue(s.Job.Cwd, s.Branch); err != nil {
				return err
			}
		}
		s.set("cancelled", "Cancelled")
	}
	return d.save()
}

// runSchedules advances every active schedule once.
func (d *daemon) runSchedules(ctx context.Context) {
	d.mu.Lock()
	active := []*Schedule{}
	kept := []*Schedule{}
	for _, s := range d.state.Schedules {
		if !s.done() {
			active = append(active, s)
		}
		if !s.done() || time.Since(time.Unix(s.Updated, 0)) < 24*time.Hour {
			kept = append(kept, s)
		}
	}
	d.state.Schedules = kept
	d.mu.Unlock()
	for _, s := range active {
		if s.Branch != "" {
			d.mu.Lock()
			d.advanceQueued(s)
			d.mu.Unlock()
			continue
		}
		state, err := d.prState(ctx, s.Repo, s.Number)
		if err != nil {
			log.Printf("%s#%d: read state: %v", s.Repo, s.Number, err)
		}
		d.mu.Lock()
		if !s.done() {
			d.advance(s, state)
		}
		d.mu.Unlock()
	}
}

func (d *daemon) advance(s *Schedule, prState string) {
	switch prState {
	case "CLOSED":
		s.set("closed", fmt.Sprintf("#%d closed without merging", s.Number))
		go notify("critical", fmt.Sprintf("#%d closed", s.Number), "The scheduled command will not run.")
	case "MERGED":
		d.startScheduled(s, fmt.Sprintf("#%d merged", s.Number))
	}
}

// advanceQueued starts a queued publish once no open PR or queued publish it waits for is left;
// the publish itself queues again if GitHub still lists one open.
func (d *daemon) advanceQueued(s *Schedule) {
	queue := publish.Queue(s.Job.Cwd)
	own, ok := queue[s.Branch]
	if !ok {
		s.set("cancelled", "No longer queued")
		return
	}
	var waiting []string
	for _, branch := range own.Behind {
		name := ""
		if _, queued := queue[branch]; queued {
			name = branch
		}
		for _, pr := range append(slices.Clone(d.state.PRs), d.state.Leaving...) {
			if pr.Repo == s.Repo && pr.Branch == branch {
				name = fmt.Sprintf("#%d", pr.Number)
			}
		}
		if name != "" {
			waiting = append(waiting, name)
		}
	}
	if len(waiting) > 0 {
		s.Message = "Waiting for " + strings.Join(waiting, ", ")
		return
	}
	d.startScheduled(s, "Queue cleared")
}

func (d *daemon) startScheduled(s *Schedule, reason string) {
	path, err := startJob(s.Job)
	if err == errBusy {
		s.Message = reason + "; waiting for the running command to finish"
		return
	}
	name := filepath.Base(s.Job.Command[0])
	if s.Branch != "" {
		name = "publishing " + s.Branch
	}
	if err != nil {
		s.set("failed", "Could not start: "+err.Error())
		go notify("critical", reason, "The scheduled command could not start.")
		return
	}
	s.JobPath = path
	s.set("started", reason+"; started "+name)
	if s.Branch == "" {
		go notify("normal", reason, "Started "+name+".")
	}
}
