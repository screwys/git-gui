// Package daemon does git-gui's work that is not drawing the window.
//
// It runs commands under detached supervisors, so they outlive the window and the daemon.
// It watches the user's open pull requests: a passing PR whose branch is behind its base gets
// updated, failed checks and conflicts raise notifications, and a Rufin merge syncs the Rufin
// checkout. It follows the checks of commits pushed straight to a branch when git-gui wait-push
// asks. It holds commands scheduled to run after a PR merges, and publishes queued behind
// other work once that merged. git-gui talks to it over a Unix socket and reads its state
// file, $XDG_STATE_HOME/git-gui/daemon.json.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/screwys/git-gui/internal/publish"
)

// Supervise runs one job's command; the daemon starts it as "<binary> supervise <job.json>".
func Supervise(path string) int { return supervise(path) }

// Run serves git-gui until the process ends.
func Run() {
	log.SetFlags(0)
	d := &daemon{
		client:    &http.Client{Timeout: 30 * time.Second},
		statePath: StatePath(),
		notified:  map[string]bool{},
		syncError: map[string]string{},
		updates:   map[string]*branchUpdate{},
		wake:      make(chan struct{}, 1),
	}
	if data, err := os.ReadFile(d.statePath); err == nil {
		_ = json.Unmarshal(data, &d.state)
	}
	for _, key := range d.state.Notified {
		d.notified[key] = true
	}
	d.loadCheckouts()
	if err := d.serve(); err != nil {
		log.Fatal(err)
	}
	for {
		if err := d.pass(context.Background()); err != nil {
			log.Print(err)
		}
		interval := 30 * time.Second
		if d.activeSchedules() || d.followingPushes() {
			interval = 10 * time.Second
		}
		select {
		case <-time.After(interval):
		case <-d.wake:
		}
	}
}

// State is the file git-gui and other readers use.
type State struct {
	Updated     int64       `json:"updated"`
	PRs         []PR        `json:"prs"`
	Leaving     []PR        `json:"leaving"` // gone from the open search, not yet seen merged or closed
	SyncPending []string    `json:"sync_pending"`
	Notified    []string    `json:"notified"`
	Schedules   []*Schedule `json:"schedules"`
	Releases    []*Release  `json:"releases"` // what each project's release jobs view follows
	Pushes      []*Push     `json:"pushes"`   // pushed commits git-gui wait-push follows
}

type daemon struct {
	mu        sync.Mutex // guards state, notified and syncError
	client    *http.Client
	token     string
	statePath string
	checkouts map[string]string // "owner/name" -> the shared checkout git-gui publishes from
	base      string            // the branch synced after merges
	state     State
	notified  map[string]bool
	syncError map[string]string
	updates   map[string]*branchUpdate // by PR key; only watchPRs uses it
	wake      chan struct{}
}

func (d *daemon) pass(ctx context.Context) error {
	// Each pass records the checkouts' edits in jj's operation log, so an edit a stash, reset or
	// checkout throws away can be restored.
	for _, checkout := range d.checkouts {
		publish.Snapshot(checkout)
		for _, name := range publish.Workspaces(checkout) {
			publish.Snapshot(publish.WorkspacePath(checkout, name))
		}
	}
	watchErr := errors.Join(d.watchPRs(ctx), d.watchPushes(ctx))
	d.runSchedules(ctx)
	d.pollReleases(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state.Updated = time.Now().Unix()
	if err := d.save(); err != nil {
		return err
	}
	return watchErr
}

func (d *daemon) poke() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *daemon) save() error {
	data, err := json.MarshalIndent(d.state, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.statePath), 0o700); err != nil {
		return err
	}
	tmp := d.statePath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.statePath)
}

// StatePath is the state file the window and other readers use.
func StatePath() string { return filepath.Join(stateHome(), "git-gui", "daemon.json") }

func stateHome() string {
	if home := os.Getenv("XDG_STATE_HOME"); home != "" {
		return home
	}
	return filepath.Join(os.Getenv("HOME"), ".local", "state")
}
