package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
)

// Request is one call from git-gui. Each connection carries one request and one response.
type Request struct {
	Op       string    `json:"op"` // start, signal, schedule, cancel, cancel_runs or follow_release
	Job      *JobSpec  `json:"job,omitempty"`
	JobPath  string    `json:"job_path,omitempty"`
	Action   string    `json:"action,omitempty"` // stop, pause or resume
	Schedule *Schedule `json:"schedule,omitempty"`
	ID       string    `json:"id,omitempty"`
	Cwd      string    `json:"cwd,omitempty"`
	Titles   []string  `json:"titles,omitempty"`
	// follow_release: Cwd's jobs view shows Tag (empty for the newest) of a Kind release, from
	// window process Pid, with the release job at JobPath.
	Kind string `json:"kind,omitempty"`
	Tag  string `json:"tag,omitempty"`
	Pid  int    `json:"pid,omitempty"`
}

type Response struct {
	Error   string `json:"error,omitempty"`
	JobPath string `json:"job_path,omitempty"`
	ID      string `json:"id,omitempty"`
}

func SocketPath() string {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = filepath.Join(os.TempDir(), fmt.Sprintf("git-gui-%d", os.Getuid()))
		_ = os.MkdirAll(runtime, 0o700)
	}
	return filepath.Join(runtime, "git-gui-daemon.sock")
}

func (d *daemon) serve() error {
	path := SocketPath()
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Print(err)
				continue
			}
			go d.handle(conn)
		}
	}()
	return nil
}

func (d *daemon) handle(conn net.Conn) {
	defer conn.Close()
	var req Request
	var resp Response
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		resp.Error = err.Error()
	} else if err := d.apply(req, &resp); err != nil {
		resp.Error = err.Error()
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

func (d *daemon) apply(req Request, resp *Response) error {
	switch req.Op {
	case "start":
		if req.Job == nil || len(req.Job.Command) == 0 {
			return fmt.Errorf("start needs a command")
		}
		path, err := startJob(*req.Job)
		resp.JobPath = path
		return err
	case "signal":
		return signalJob(req.JobPath, req.Action)
	case "schedule":
		if req.Schedule == nil || len(req.Schedule.Job.Command) == 0 {
			return fmt.Errorf("schedule needs a command")
		}
		if err := d.addSchedule(req.Schedule); err != nil {
			return err
		}
		resp.ID = req.Schedule.ID
		d.poke()
		return nil
	case "cancel":
		return d.cancelSchedule(req.ID)
	case "cancel_runs":
		go cancelRuns(req.Cwd, req.Titles)
		return nil
	case "follow_release":
		return d.followRelease(req)
	}
	return fmt.Errorf("unknown op %q", req.Op)
}
