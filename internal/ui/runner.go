package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/daemon"
)

// Runner follows the job of one command slot: it starts jobs through the daemon, replays saved
// output and reports progress on the main loop.
type Runner struct {
	Cwd, Key string
	Program  string // a helper started outside git-gui is observed when this runs it

	Path    string
	Record  daemon.Record
	outside *client.Outside

	running, paused bool
	replaying       bool
	offset          int64
	replayEnd       int64
	partial         []byte
	stopRequested   bool
	timer           glib.SourceHandle

	OnOutput   func(text string, replaying bool)
	OnStarted  func()
	OnFinished func(code int, record daemon.Record)
	OnRestored func(record daemon.Record, running, paused bool)
	OnError    func(message string)
}

// NewRunner returns a runner for one slot.
func NewRunner(cwd, key, program string) *Runner {
	return &Runner{Cwd: cwd, Key: key, Program: program}
}

func (r *Runner) Running() bool  { return r.running }
func (r *Runner) Paused() bool   { return r.paused }
func (r *Runner) External() bool { return r.outside != nil }

// Attach shows the slot's running job, else an observed outside helper, else its newest job.
func (r *Runner) Attach() {
	if r.running {
		return
	}
	entries := client.Records(r.Cwd, r.Key)
	var path string
	var record daemon.Record
	var outside *client.Outside
	for _, e := range entries {
		if client.Running(e.Record) {
			path, record = e.Path, e.Record
			break
		}
	}
	if path == "" {
		if outside = client.FindOutside(r.Program); outside == nil && len(entries) > 0 {
			path, record = entries[0].Path, entries[0].Record
		}
	}
	if path == "" && outside == nil {
		return
	}
	if path != "" && path == r.Path && r.outside == nil {
		return
	}
	if r.Path != "" || r.outside != nil {
		r.emit("\n[ATTACHED TO ANOTHER COMMAND]\n", false)
	}
	r.Path, r.Record, r.outside = path, record, outside
	if outside != nil {
		r.Record = daemon.Record{Cwd: outside.Cwd, Key: r.Key, Command: outside.Command, Pid: outside.Proc.Pid, Identity: outside.Proc.Identity}
	}
	r.running = client.Running(r.Record)
	r.paused = client.Paused(r.Record)
	r.Reload()
}

// Reload replays the attached job's output from the start.
func (r *Runner) Reload() {
	if r.Path == "" && r.outside == nil {
		return
	}
	r.offset, r.partial, r.replayEnd = 0, nil, 0
	if info, err := os.Stat(r.logPath()); err == nil {
		r.replayEnd = info.Size()
	}
	r.emit("$ "+shellJoin(r.Record.Command)+"\n", true)
	if r.outside != nil {
		r.emit("[OBSERVING] Helper started outside this session. Its output and exit code are unavailable.\n", false)
	}
	r.readOutput()
	if r.OnRestored != nil {
		r.OnRestored(r.Record, r.running, r.paused)
	}
	if r.running || r.Path != "" {
		r.startTimer()
	}
}

// Start runs a command in this slot through the daemon.
func (r *Runner) Start(command []string, extra map[string]string) {
	if r.running || client.FindOutside(r.Program) != nil {
		r.fail("A command is already running. Refresh to view it.")
		return
	}
	path, err := client.Start(client.Job(r.Cwd, r.Key, command, extra))
	if err != nil {
		r.fail(err.Error())
		return
	}
	record, _ := daemon.ReadRecord(path)
	r.Path, r.Record, r.outside = path, record, nil
	r.offset, r.replayEnd, r.partial = 0, 0, nil
	r.running, r.paused, r.stopRequested = true, false, false
	r.emit("$ "+shellJoin(command)+"\n", false)
	if r.OnStarted != nil {
		r.OnStarted()
	}
	r.startTimer()
}

// Stop ends the running job and its child processes.
func (r *Runner) Stop() {
	if !r.running || r.outside != nil || r.Path == "" {
		return
	}
	r.Record, _ = daemon.ReadRecord(r.Path)
	if r.starting() {
		r.stopRequested = true
		return
	}
	r.stopRequested = false
	if err := client.Signal(r.Path, "stop"); err != nil {
		r.fail(err.Error())
		return
	}
	r.emit("\n[STOPPING] Terminating the command and its child processes...\n", false)
}

// TogglePause freezes or resumes the running job.
func (r *Runner) TogglePause() {
	if !r.running || r.outside != nil || r.Path == "" {
		return
	}
	action := "pause"
	if r.paused {
		action = "resume"
	}
	if err := client.Signal(r.Path, action); err != nil {
		r.fail(err.Error())
		return
	}
	r.paused = !r.paused
	if r.OnRestored != nil {
		r.OnRestored(r.Record, true, r.paused)
	}
}

// starting reports a record the daemon wrote before its supervisor recorded itself.
func (r *Runner) starting() bool {
	return r.Path != "" && r.Record.Pid == 0 && r.Record.ExitCode == nil &&
		time.Since(time.Unix(int64(r.Record.StartedAt), 0)) < 30*time.Second
}

func (r *Runner) startTimer() {
	if r.timer != 0 {
		return
	}
	r.timer = glib.TimeoutAdd(250, func() bool {
		if r.poll() {
			return true
		}
		r.timer = 0
		return false
	})
}

// poll advances output and status once; false ends polling.
func (r *Runner) poll() bool {
	hadChild := r.Record.Child != nil
	if r.Path != "" {
		if record, err := daemon.ReadRecord(r.Path); err == nil {
			r.Record = record
		}
	}
	r.readOutput()
	paused := client.Paused(r.Record)
	if paused != r.paused || (r.Record.Child != nil) != hadChild {
		r.paused = paused
		if r.OnRestored != nil {
			r.OnRestored(r.Record, r.running, paused)
		}
	}
	if r.starting() {
		return true
	}
	if r.outside != nil {
		if daemon.Alive(&r.outside.Proc) {
			return true
		}
	} else if client.Running(r.Record) {
		if r.stopRequested {
			r.Stop()
		}
		return true
	}
	if r.offset < r.logSize() {
		return true
	}
	wasRunning := r.running
	r.running = false
	if wasRunning {
		code := -1
		if r.Record.ExitCode != nil {
			code = *r.Record.ExitCode
		}
		if code == -1 {
			r.emit("\n[ENDED] No exit status was recorded. Check Git and remote status before retrying.\n", false)
		}
		r.outside = nil
		if r.OnFinished != nil {
			r.OnFinished(code, r.Record)
		}
	}
	// The hook may have started or attached the next job; this timer follows it.
	return r.running || r.offset < r.logSize()
}

func (r *Runner) logPath() string {
	if r.Path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(r.Path), "output.log")
}

func (r *Runner) logSize() int64 {
	if info, err := os.Stat(r.logPath()); err == nil {
		return info.Size()
	}
	return 0
}

// readOutput emits up to 256 KiB of new output; saved output replays in such steps.
func (r *Runner) readOutput() {
	if r.Path == "" {
		return
	}
	file, err := os.Open(r.logPath())
	if err != nil {
		return
	}
	defer file.Close()
	if _, err := file.Seek(r.offset, io.SeekStart); err != nil {
		return
	}
	data, _ := io.ReadAll(io.LimitReader(file, 256*1024))
	if len(data) == 0 {
		return
	}
	r.offset += int64(len(data))
	data = append(r.partial, data...)
	cut := len(data)
	for cut > 0 && !utf8.Valid(data[:cut]) && len(data)-cut < 4 {
		cut--
	}
	if cut == 0 {
		cut = len(data)
	}
	r.partial = append([]byte(nil), data[cut:]...)
	r.emit(strings.ToValidUTF8(string(data[:cut]), "�"), r.offset <= r.replayEnd)
}

func (r *Runner) emit(text string, replaying bool) {
	if r.OnOutput != nil {
		r.OnOutput(text, replaying)
	}
}

func (r *Runner) fail(message string) {
	if r.OnError != nil {
		r.OnError(message)
	}
}

// shellJoin quotes a command the way a shell user would type it.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && strings.IndexFunc(arg, func(c rune) bool {
			return !(c == '-' || c == '_' || c == '.' || c == '/' || c == '=' || c == ':' || c == '+' || c == ',' || c == '@' ||
				(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'))
		}) < 0 {
			quoted[i] = arg
		} else {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}

// formatDuration is "12s", "3m 04s" or "1h 02m 03s".
func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %02dm %02ds", s/3600, s/60%60, s%60)
}
