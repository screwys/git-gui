package daemon

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// JobSpec is one command to run, with the environment git-gui was started in.
type JobSpec struct {
	Cwd     string            `json:"cwd"`
	Key     string            `json:"key"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env"`
}

// Proc identifies a process across PID reuse.
type Proc struct {
	Pid      int    `json:"pid"`
	Identity string `json:"identity"`
}

// Record is job.json, which git-gui reads to attach to a job and replay its output.
type Record struct {
	Cwd        string   `json:"cwd"`
	Key        string   `json:"key"`
	Command    []string `json:"command"`
	StartedAt  float64  `json:"started_at"`
	Pid        int      `json:"pid,omitempty"`
	Identity   string   `json:"identity,omitempty"`
	Child      *Proc    `json:"child,omitempty"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	FinishedAt float64  `json:"finished_at,omitempty"`
	Stopped    bool     `json:"stopped,omitempty"`
}

var errBusy = errors.New("this command is already running")

func JobsRoot() string { return filepath.Join(stateHome(), "git-gui", "jobs") }

func ReadRecord(path string) (Record, error) {
	var record Record
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &record)
	}
	return record, err
}

func writeRecord(path string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	tmp := strings.TrimSuffix(path, ".json") + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// processIdentity is the boot ID and start time, or "" for a missing or zombie process.
func ProcessIdentity(pid int) string {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	end := strings.LastIndexByte(string(stat), ')')
	fields := strings.Fields(string(stat)[end+1:])
	if len(fields) < 20 || fields[0] == "Z" {
		return ""
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19]
}

func Alive(p *Proc) bool { return p != nil && p.Identity != "" && ProcessIdentity(p.Pid) == p.Identity }

// JobRunning reports whether a job has not ended and its supervisor or command is alive.
func JobRunning(record Record) bool {
	return record.ExitCode == nil && (Alive(&Proc{record.Pid, record.Identity}) || Alive(record.Child))
}

// startJob writes the job record and starts its supervisor, unless the same command slot is busy.
func startJob(spec JobSpec) (string, error) {
	cwd, err := filepath.EvalSymlinks(spec.Cwd)
	if err != nil {
		return "", err
	}
	root := JobsRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	slot := sha256.Sum256([]byte(cwd + "\x00" + spec.Key))
	lock, err := os.OpenFile(filepath.Join(root, hex.EncodeToString(slot[:])+".lock"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return "", errBusy
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	dir := filepath.Join(root, hex.EncodeToString(id))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "job.json")
	record := Record{Cwd: cwd, Key: spec.Key, Command: spec.Command, StartedAt: float64(time.Now().UnixNano()) / 1e9}
	if err := writeRecord(path, record); err != nil {
		return "", err
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	supervisor := exec.Command(self, "supervise", path)
	supervisor.Env = jobEnv(spec.Env)
	// The supervisor holds the slot lock as fd 3 until the command ends.
	supervisor.ExtraFiles = []*os.File{lock}
	supervisor.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := supervisor.Start(); err != nil {
		code := 127
		record.ExitCode, record.FinishedAt = &code, float64(time.Now().UnixNano())/1e9
		_ = writeRecord(path, record)
		return "", err
	}
	go supervisor.Wait()
	return path, nil
}

func jobEnv(env map[string]string) []string {
	merged := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		merged[key] = value
	}
	for key, value := range env {
		merged[key] = value
	}
	merged["TERM"], merged["FORCE_COLOR"], merged["PYTHONUNBUFFERED"] = "xterm-256color", "1", "1"
	out := make([]string, 0, len(merged))
	for key, value := range merged {
		out = append(out, key+"="+value)
	}
	return out
}

// signalJob stops, pauses or resumes a job's command and its child processes.
func signalJob(path, action string) error {
	record, err := ReadRecord(path)
	if err != nil {
		return err
	}
	switch action {
	case "stop":
		if supervisor := (&Proc{record.Pid, record.Identity}); Alive(supervisor) {
			return syscall.Kill(record.Pid, syscall.SIGTERM)
		}
		if Alive(record.Child) {
			return syscall.Kill(-record.Child.Pid, syscall.SIGTERM)
		}
		return nil
	case "pause", "resume":
		if !Alive(record.Child) {
			return nil
		}
		signum := syscall.SIGSTOP
		if action == "resume" {
			signum = syscall.SIGCONT
		}
		return syscall.Kill(-record.Child.Pid, signum)
	}
	return fmt.Errorf("unknown action %q", action)
}

// supervise runs one job's command, writes its output to output.log and records how it ended.
func supervise(path string) int {
	record, err := ReadRecord(path)
	if err != nil {
		return 1
	}
	// Keep the slot lock (fd 3) here: the command and anything it leaves behind must not hold it.
	syscall.CloseOnExec(3)
	record.Pid = os.Getpid()
	record.Identity = ProcessIdentity(record.Pid)
	stops := make(chan os.Signal, 1)
	signal.Notify(stops, syscall.SIGTERM, syscall.SIGINT)
	_ = writeRecord(path, record)
	output, err := os.OpenFile(filepath.Join(filepath.Dir(path), "output.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 1
	}
	defer output.Close()

	code, stopped := 127, false
	child := exec.Command(record.Command[0], record.Command[1:]...)
	child.Dir, child.Stdout, child.Stderr = record.Cwd, output, output
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		fmt.Fprintf(output, "\n[ERROR] %v\n", err)
	} else {
		record.Child = &Proc{child.Process.Pid, ProcessIdentity(child.Process.Pid)}
		_ = writeRecord(path, record)
		done := make(chan struct{})
		go func() { _ = child.Wait(); close(done) }()
		var deadline <-chan time.Time
	wait:
		for {
			select {
			case <-stops:
				if !stopped {
					stopped = true
					_ = syscall.Kill(-child.Process.Pid, syscall.SIGTERM)
					deadline = time.After(5 * time.Second)
				}
			case <-deadline:
				_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
				<-done
				break wait
			case <-done:
				if !stopped || syscall.Kill(-child.Process.Pid, 0) != nil {
					break wait
				}
				// The command exited, but its process group is still stopping.
				done = make(chan struct{})
				go func() {
					for syscall.Kill(-child.Process.Pid, 0) == nil {
						time.Sleep(100 * time.Millisecond)
					}
					close(done)
				}()
			}
		}
		code = exitCode(child.ProcessState)
	}
	record.ExitCode, record.FinishedAt, record.Stopped = &code, float64(time.Now().UnixNano())/1e9, stopped
	label := "FINISHED"
	if stopped {
		label = "STOPPED"
	}
	fmt.Fprintf(output, "\n[%s] Exit %d\n", label, code)
	_ = writeRecord(path, record)
	status := "Completed"
	switch {
	case stopped:
		status = "Stopped"
	case code != 0:
		status = "Failed"
	}
	words := record.Command[:min(2, len(record.Command))]
	notify(status+": "+record.Key, filepath.Base(record.Cwd)+": "+strings.Join(words, " "))
	return 0
}

// exitCode follows Python's convention: a signal ends with minus its number.
func exitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return -int(status.Signal())
	}
	return state.ExitCode()
}
