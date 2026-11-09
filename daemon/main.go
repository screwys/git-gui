// Command git-gui-daemon does git-gui's work that is not drawing the window.
//
// It runs commands under detached supervisors, so they outlive the window and the daemon.
// It watches the user's open pull requests: a passing PR whose branch is behind its base gets
// updated, failed checks and conflicts raise notifications, and a Rufin merge syncs the Rufin
// checkout. It holds commands scheduled to run after a PR merges, and for publishes it can run
// the full checks while it waits. git-gui talks to it over a Unix socket and reads its state
// file, $XDG_STATE_HOME/git-gui/daemon.json.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "supervise":
			os.Exit(supervise(os.Args[2]))
		case "publish-checks":
			os.Exit(publishChecks(os.Args[2], os.Args[3], os.Args[4]))
		}
		log.Fatalf("unknown command %q", os.Args[1])
	}
	d := &daemon{
		client:    &http.Client{Timeout: 30 * time.Second},
		statePath: filepath.Join(stateHome(), "git-gui", "daemon.json"),
		notified:  map[string]bool{},
		syncError: map[string]string{},
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
		if d.activeSchedules() {
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
}

type daemon struct {
	mu        sync.Mutex // guards state, notified and syncError
	client    *http.Client
	token     string
	statePath string
	checkouts map[string]string // "owner/name" -> local checkout with a publish helper
	helper    map[string]string // "owner/name" -> publish helper path
	state     State
	notified  map[string]bool
	syncError map[string]string
	wake      chan struct{}
}

func (d *daemon) pass(ctx context.Context) error {
	watchErr := d.watchPRs(ctx)
	d.runSchedules(ctx)
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

func stateHome() string {
	if home := os.Getenv("XDG_STATE_HOME"); home != "" {
		return home
	}
	return filepath.Join(os.Getenv("HOME"), ".local", "state")
}
