// Package client is the window's side of the daemon: requests over its socket, its state file
// and the job records it writes.
package client

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/screwys/git-gui/internal/daemon"
)

// Request sends one request to the daemon and returns its answer.
func Request(req daemon.Request) (daemon.Response, error) {
	var resp daemon.Response
	conn, err := net.DialTimeout("unix", daemon.SocketPath(), 10*time.Second)
	if err != nil {
		return resp, fmt.Errorf("git-gui-daemon is not reachable (%v). Run install-desktop.py", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return resp, err
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return resp, err
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

// Job describes a command with the environment this window was started in, so it finds the
// user's tools, plus extra variables.
func Job(cwd, key string, command []string, extra map[string]string) daemon.JobSpec {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		k, v, _ := strings.Cut(entry, "=")
		env[k] = v
	}
	for k, v := range extra {
		env[k] = v
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	return daemon.JobSpec{Cwd: cwd, Key: key, Command: command, Env: env}
}

// Start starts a job and returns its job.json path.
func Start(job daemon.JobSpec) (string, error) {
	resp, err := Request(daemon.Request{Op: "start", Job: &job})
	return resp.JobPath, err
}

// Signal stops, pauses or resumes a job.
func Signal(jobPath, action string) error {
	_, err := Request(daemon.Request{Op: "signal", JobPath: jobPath, Action: action})
	return err
}

// State reads the daemon's state file.
func State() daemon.State {
	var state daemon.State
	if data, err := os.ReadFile(daemon.StatePath()); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	return state
}

// Schedules lists a command slot's schedules, newest first.
func Schedules(cwd, key string) []daemon.Schedule {
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	var found []daemon.Schedule
	for _, s := range State().Schedules {
		if s.Job.Cwd == cwd && s.Job.Key == key {
			found = append(found, *s)
		}
	}
	slices.SortFunc(found, func(a, b daemon.Schedule) int { return int(b.Updated - a.Updated) })
	return found
}

// Entry is one job record with its path.
type Entry struct {
	Path   string
	Record daemon.Record
}

// Records lists a slot's jobs, newest first.
func Records(cwd, key string) []Entry {
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	paths, _ := filepath.Glob(filepath.Join(daemon.JobsRoot(), "*", "job.json"))
	var entries []Entry
	for _, path := range paths {
		record, err := daemon.ReadRecord(path)
		if err == nil && record.Cwd == cwd && record.Key == key {
			entries = append(entries, Entry{path, record})
		}
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		switch {
		case a.Record.StartedAt > b.Record.StartedAt:
			return -1
		case a.Record.StartedAt < b.Record.StartedAt:
			return 1
		}
		return 0
	})
	return entries
}

// Running reports whether a job's supervisor or command is alive and it has not ended.
func Running(record daemon.Record) bool { return daemon.JobRunning(record) }

// Paused reports whether a job's command is stopped by a signal.
func Paused(record daemon.Record) bool {
	if record.Child == nil || !daemon.Alive(record.Child) {
		return false
	}
	state := procState(record.Child.Pid)
	return state == "T" || state == "t"
}

func procState(pid int) string {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// Outside is a helper process started outside git-gui, observed without its output.
type Outside struct {
	Proc    daemon.Proc
	Command []string
	Cwd     string
}

// FindOutside looks for one of the user's processes running "git-gui SUBCOMMAND", such as a
// publish an agent started from a terminal.
func FindOutside(subcommand string) *Outside {
	if subcommand == "" {
		return nil
	}
	self, _ := os.Executable()
	entries, _ := os.ReadDir("/proc")
	uid := os.Getuid()
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		if info, err := os.Stat(dir); err != nil || ownerUID(info) != uid {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(args) < 2 || args[1] != subcommand {
			continue
		}
		if exe, err := os.Readlink(filepath.Join(dir, "exe")); err != nil || exe != self {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
		if err != nil {
			continue
		}
		if identity := daemon.ProcessIdentity(pid); identity != "" {
			return &Outside{daemon.Proc{Pid: pid, Identity: identity}, args, cwd}
		}
	}
	return nil
}

func ownerUID(info os.FileInfo) int {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid)
	}
	return -1
}
