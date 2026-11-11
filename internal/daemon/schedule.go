package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/screwys/git-gui/internal/publish"
)

const checkedInputs = "Checked PR inputs: "

// Schedule runs a command after a pull request merges.
type Schedule struct {
	ID      string        `json:"id"`
	Repo    string        `json:"repo"`
	Number  int           `json:"number"`
	Job     JobSpec       `json:"job"`
	Publish *PublishCheck `json:"publish,omitempty"` // run the full checks while waiting
	// Status is waiting, checking, checked, started, failed, closed or cancelled.
	Status  string   `json:"status"`
	Message string   `json:"message"`
	JobPath string   `json:"job_path"` // the checks job, then the started command
	Inputs  []string `json:"inputs,omitempty"`
	Updated int64    `json:"updated"`
}

// PublishCheck describes a publish whose full checks run before the PR it waits for merges.
type PublishCheck struct {
	Branch    string   `json:"branch"`
	Title     string   `json:"title"`
	Paths     []string `json:"paths"` // nil means every pending change
	Args      []string `json:"args"`
	RetryArgs []string `json:"retry_args"`
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

// addSchedule replaces any schedule for the same command slot.
func (d *daemon) addSchedule(s *Schedule) error {
	if s.Repo == "" {
		s.Repo = repoSlug(s.Job.Cwd)
	}
	if s.Repo == "" || s.Number == 0 {
		return fmt.Errorf("schedule needs a GitHub repository and a PR number")
	}
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	s.ID = fmt.Sprintf("%x", id)
	s.set("waiting", fmt.Sprintf("Waiting for #%d to merge", s.Number))
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, other := range d.state.Schedules {
		if !other.done() && other.Job.Cwd == s.Job.Cwd && other.Job.Key == s.Job.Key {
			d.cancelLocked(other)
		}
	}
	d.state.Schedules = append(d.state.Schedules, s)
	return d.save()
}

func (d *daemon) cancelSchedule(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.state.Schedules {
		if s.ID == id && !s.done() {
			d.cancelLocked(s)
		}
	}
	return d.save()
}

func (d *daemon) cancelLocked(s *Schedule) {
	if s.Status == "checking" {
		_ = signalJob(s.JobPath, "stop")
	}
	s.set("cancelled", "Cancelled")
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
	if s.Status == "checking" {
		record, err := ReadRecord(s.JobPath)
		if err != nil || JobRunning(record) {
			return
		}
		inputs := checksResult(s.JobPath)
		if record.ExitCode == nil || *record.ExitCode != 0 || record.Stopped || inputs == nil {
			s.set("failed", "Pending checks failed. Publication stopped.")
			go notify("critical", fmt.Sprintf("Publish after #%d stopped", s.Number), "The pending checks failed.")
			return
		}
		s.Inputs = inputs
		s.set("checked", fmt.Sprintf("Checks passed; waiting for #%d to merge", s.Number))
	}
	switch prState {
	case "CLOSED":
		s.set("closed", fmt.Sprintf("#%d closed without merging", s.Number))
		go notify("critical", fmt.Sprintf("#%d closed", s.Number), "The scheduled command will not run.")
		return
	case "MERGED":
	default:
		if s.Publish != nil && s.Status == "waiting" {
			d.startChecks(s)
		}
		return
	}

	command := s.Job.Command
	if s.Publish != nil {
		if s.Status != "checked" {
			d.startChecks(s)
			return
		}
		inputs, err := pendingInputs(s.Job.Cwd, s.Publish.Branch, s.Publish.Paths)
		if err != nil {
			s.set("failed", "Cannot read pending changes: "+err.Error())
			return
		}
		if !slices.Equal(inputs, s.Inputs) {
			s.Message = "Pending changes edited. Running checks again."
			d.startChecks(s)
			return
		}
		args := s.Publish.RetryArgs
		if edited(s.Job.Cwd, inputs) {
			args = s.Publish.Args
		}
		// The full gate already passed on these inputs.
		self, err := os.Executable()
		if err != nil {
			s.set("failed", err.Error())
			return
		}
		command = []string{self, "publish"}
		for _, arg := range args {
			if arg != "--full" {
				command = append(command, arg)
			}
		}
	}
	job := s.Job
	job.Command = command
	path, err := startJob(job)
	if err == errBusy {
		s.Message = fmt.Sprintf("#%d merged; waiting for the running command to finish", s.Number)
		return
	}
	if err != nil {
		s.set("failed", "Could not start: "+err.Error())
		go notify("critical", fmt.Sprintf("#%d merged", s.Number), "The scheduled command could not start.")
		return
	}
	s.JobPath = path
	s.set("started", fmt.Sprintf("#%d merged; started %s", s.Number, filepath.Base(command[0])))
	go notify("normal", fmt.Sprintf("#%d merged", s.Number), "Started "+filepath.Base(command[0])+".")
}

func (d *daemon) startChecks(s *Schedule) {
	self, err := os.Executable()
	if err != nil {
		s.set("failed", err.Error())
		return
	}
	request, _ := json.Marshal(s.Publish)
	job := s.Job
	job.Command = []string{self, "publish-checks", s.Job.Cwd, string(request)}
	path, err := startJob(job)
	if err == errBusy {
		return
	}
	if err != nil {
		s.set("failed", "Pending checks could not start: "+err.Error())
		return
	}
	s.JobPath, s.Inputs = path, nil
	s.set("checking", fmt.Sprintf("Checking pending changes; waiting for #%d", s.Number))
}

// checksResult reads the inputs a finished checks job printed.
func checksResult(jobPath string) []string {
	output, err := os.Open(filepath.Join(filepath.Dir(jobPath), "output.log"))
	if err != nil {
		return nil
	}
	defer output.Close()
	var inputs []string
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if line, ok := strings.CutPrefix(scanner.Text(), checkedInputs); ok {
			_ = json.Unmarshal([]byte(line), &inputs)
		}
	}
	return inputs
}

// pendingInputs names the publish's contents without touching the checkout's index:
// HEAD, the local topic branch (empty when absent) and the tree of the selected changes.
func pendingInputs(repo, branch string, paths []string) ([]string, error) {
	head, err := git(repo, nil, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	branchHead, _ := git(repo, nil, "rev-parse", "--verify", "refs/heads/"+branch)
	dir, err := os.MkdirTemp("", "git-gui-index-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	index := filepath.Join(dir, "index")
	if source, err := git(repo, nil, "rev-parse", "--path-format=absolute", "--git-path", "index"); err == nil {
		// Keep Git's stat cache so unchanged files do not need hashing.
		_ = copyFile(source, index)
	}
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := git(repo, env, "read-tree", "--reset", head); err != nil {
		return nil, err
	}
	if paths == nil {
		_, err = git(repo, env, "add", "-A", "--", ".")
	} else if len(paths) > 0 {
		var listed string
		listed, err = git(repo, env, append([]string{"--literal-pathspecs", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, paths...)...)
		if matched := slices.Compact(slices.Sorted(slices.Values(strings.Split(strings.Trim(listed, "\x00"), "\x00")))); err == nil && listed != "" {
			_, err = git(repo, env, append([]string{"--literal-pathspecs", "add", "-A", "--"}, matched...)...)
		}
	}
	if err != nil {
		return nil, err
	}
	tree, err := git(repo, env, "write-tree")
	if err != nil {
		return nil, err
	}
	return []string{head, branchHead, tree}, nil
}

func edited(repo string, inputs []string) bool {
	tree, err := git(repo, nil, "rev-parse", inputs[0]+"^{tree}")
	return err != nil || inputs[2] != tree
}

// publishChecks prepares the publish without pushing and runs the pre-push hook on it.
func publishChecks(repo, request string) int {
	var pc PublishCheck
	if err := json.Unmarshal([]byte(request), &pc); err != nil {
		fmt.Println("Checks stopped:", err)
		return 1
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Println("Checks stopped:", err)
		return 1
	}
	branch := pc.Branch
	var inputs []string
	prepared := ""
	for {
		var err error
		inputs, err = pendingInputs(repo, branch, pc.Paths)
		if err != nil {
			fmt.Println("Checks stopped:", err)
			return 1
		}
		args := pc.RetryArgs
		if edited(repo, inputs) {
			args = pc.Args
			if slices.Equal(args, pc.RetryArgs) {
				// A clean branch can gain edits after publication was scheduled.
				title := pc.Title
				if title == "" {
					title, _ = git(repo, nil, "log", "-1", "--format=%s", "refs/heads/"+branch)
				}
				args = append(slices.Clone(args), title)
			}
		}
		args = slices.Clone(args)
		if i := slices.Index(args, "--branch"); i >= 0 && i+1 < len(args) {
			args[i+1] = branch
		}
		cmd := exec.Command(self, append([]string{"publish", "--prepare-only"}, args...)...)
		cmd.Dir, cmd.Stderr = repo, os.Stderr
		stdout, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err != nil {
			fmt.Println("Checks stopped:", err)
			return 1
		}
		prepared = ""
		lines := bufio.NewScanner(io.TeeReader(stdout, os.Stdout))
		for lines.Scan() {
			if value, ok := strings.CutPrefix(lines.Text(), "Prepared PR worktree: "); ok {
				prepared = value
			} else if value, ok := strings.CutPrefix(lines.Text(), "Publishing branch: "); ok {
				branch = value
			}
		}
		if err := cmd.Wait(); err != nil {
			return exitCode(cmd.ProcessState)
		}
		if prepared == "" {
			fmt.Println("Checks stopped: the publish did not report a prepared worktree.")
			return 1
		}
		after, err := pendingInputs(repo, branch, pc.Paths)
		if err == nil && slices.Equal(after, inputs) {
			break
		}
		fmt.Println("Pending changes edited during preparation. Preparing them again.")
	}
	encoded, _ := json.Marshal(inputs)
	fmt.Println(checkedInputs + string(encoded))

	remote, _ := git(prepared, nil, "remote", "get-url", "origin")
	head, _ := git(prepared, nil, "rev-parse", "HEAD")
	oldHead := strings.Repeat("0", len(head))
	if listed, _ := git(prepared, nil, "ls-remote", "origin", "refs/heads/"+branch); listed != "" {
		oldHead = strings.Fields(listed)[0]
	}
	hook := publish.Capped(exec.Command("git", "hook", "run", "--ignore-missing", "pre-push", "--", "origin", remote), prepared, publish.CheckEnv(repo))
	hook.Stdin = strings.NewReader(fmt.Sprintf("refs/heads/%s %s refs/heads/%s %s\n", branch, head, branch, oldHead))
	if err := hook.Run(); err != nil {
		return exitCode(hook.ProcessState)
	}
	return 0
}

func git(repo string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.Output()
	return strings.TrimSuffix(string(out), "\n"), err
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600)
}
