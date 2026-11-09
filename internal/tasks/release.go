package tasks

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Workflow is a tag-triggered GitHub Actions workflow that publishes the GitHub release itself.
type Workflow struct {
	File, Name string
}

var ghReleaseCreate = regexp.MustCompile(`\bgh\s+release\s+create\b`)

// ReleaseWorkflows lists the repository's workflows that run on pushed tags and create a release
// with softprops/action-gh-release or `gh release create`.
func ReleaseWorkflows(repo string) []Workflow {
	paths, _ := filepath.Glob(filepath.Join(repo, ".github", "workflows", "*"))
	slices.Sort(paths)
	var found []Workflow
	for _, path := range paths {
		if ext := filepath.Ext(path); ext != ".yml" && ext != ".yaml" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var workflow struct {
			Name string `yaml:"name"`
			On   any    `yaml:"on"`
			Jobs map[string]struct {
				Steps []map[string]any `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if yaml.Unmarshal(data, &workflow) != nil {
			continue
		}
		events, _ := workflow.On.(map[string]any)
		push, _ := events["push"].(map[string]any)
		if tags := push["tags"]; tags == nil || tags == "" || fmt.Sprint(tags) == "[]" || fmt.Sprint(tags) == "map[]" {
			continue
		}
		publishes := false
		for _, job := range workflow.Jobs {
			for _, step := range job.Steps {
				uses, run := fmt.Sprint(step["uses"]), fmt.Sprint(step["run"])
				if strings.HasPrefix(uses, "softprops/action-gh-release@") || ghReleaseCreate.MatchString(run) {
					publishes = true
				}
			}
		}
		if publishes {
			name := workflow.Name
			if name == "" {
				name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			}
			found = append(found, Workflow{filepath.Base(path), name})
		}
	}
	return found
}

// Release creates an annotated tag at a commit without touching the working tree, optionally
// pushes it and publishes its GitHub release. It returns the process exit status.
func Release(args []string) int {
	flags := flag.NewFlagSet("release", flag.ContinueOnError)
	tag := flags.String("tag", "", "tag to create")
	target := flags.String("target", "", "commit to tag")
	notes := flags.String("notes", "", "annotation and release notes")
	push := flags.Bool("push", false, "push the tag to origin")
	github := flags.Bool("github", false, "create the GitHub release")
	workflow := flags.String("workflow", "", "workflow file that publishes the release")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *tag == "" || *target == "" {
		fmt.Println("Release stopped: --tag and --target are required.")
		return 2
	}
	if err := release(*tag, *target, *notes, *push, *github, *workflow); err != nil {
		fmt.Println("Release stopped:", err)
		fmt.Println("Existing tags are retained. Check the terminal and remote status before retrying.")
		return 1
	}
	return 0
}

func release(tag, target, notes string, push, github bool, workflow string) error {
	if err := run("git", "check-ref-format", "refs/tags/"+tag); err != nil {
		return err
	}
	if strings.HasPrefix(tag, "-") {
		return errors.New("A tag cannot start with '-'.")
	}
	commit, err := output("git", "rev-parse", "--verify", "--end-of-options", target+"^{commit}")
	if err != nil {
		return err
	}
	ref := "refs/tags/" + tag
	if exec.Command("git", "show-ref", "--verify", "--quiet", ref).Run() == nil {
		tagged, err := output("git", "rev-parse", "--verify", ref+"^{commit}")
		if err != nil {
			return err
		}
		if tagged != commit {
			return fmt.Errorf("%s already points to another commit. Choose another tag.", tag)
		}
		fmt.Printf("Reusing %s at %s. Its annotation is unchanged.\n", tag, commit)
	} else {
		message := notes
		if message == "" {
			message = tag
		}
		if err := run("git", "tag", "--annotate", "--cleanup=verbatim", "--message", message, "--", tag, commit); err != nil {
			return err
		}
		fmt.Printf("Created %s at %s.\n", tag, commit)
	}
	if push {
		if err := run("git", "push", "--no-verify", "origin", ref+":"+ref); err != nil {
			return err
		}
	}
	if !github {
		return nil
	}
	if !push {
		listed, err := output("git", "ls-remote", "origin", ref, ref+"^{}")
		if err != nil {
			return err
		}
		remote := map[string]string{}
		for _, line := range strings.Split(listed, "\n") {
			if fields := strings.Fields(line); len(fields) == 2 {
				remote[fields[1]] = fields[0]
			}
		}
		// An annotated tag's peeled entry names its commit.
		remoteCommit, ok := remote[ref+"^{}"]
		if !ok {
			remoteCommit = remote[ref]
		}
		if remoteCommit != commit {
			return errors.New("Origin must have this tag at the selected commit. Push the tag or choose its commit.")
		}
	}
	if workflow != "" {
		return publishWithWorkflow(workflow, tag, commit, notes)
	}
	body := notes
	if body == "" {
		body = tag
	}
	return run("gh", "release", "create", tag, "--verify-tag", "--title", tag, "--notes", body)
}

// publishWithWorkflow waits for the tag's push run of the workflow, requires it to succeed and
// then sets the release notes.
func publishWithWorkflow(workflow, tag, commit, notes string) error {
	fmt.Printf("Waiting for %s for %s...\n", workflow, tag)
	type workflowRun struct {
		DatabaseID int64  `json:"databaseId"`
		HeadSha    string `json:"headSha"`
		URL        string `json:"url"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	deadline := time.Now().Add(120 * time.Second)
	var found *workflowRun
	for found == nil {
		out, err := output("gh", "run", "list", "--workflow", workflow, "--branch", tag, "--event", "push",
			"--limit", "5", "--json", "databaseId,headSha,url,status,conclusion")
		if err != nil {
			return err
		}
		var runs []workflowRun
		if err := json.Unmarshal([]byte(out), &runs); err != nil {
			return err
		}
		for i := range runs {
			if runs[i].HeadSha == commit {
				found = &runs[i]
				break
			}
		}
		if found != nil {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("No release workflow appeared. Check its tag filter in GitHub Actions, then retry.")
		}
		time.Sleep(3 * time.Second)
	}
	fmt.Println("Following", found.URL)
	status, conclusion := found.Status, found.Conclusion
	for status != "completed" {
		time.Sleep(3 * time.Second)
		out, err := output("gh", "api", fmt.Sprintf("repos/{owner}/{repo}/actions/runs/%d", found.DatabaseID))
		if err != nil {
			return err
		}
		var current struct{ Status, Conclusion string }
		if err := json.Unmarshal([]byte(out), &current); err != nil {
			return err
		}
		status, conclusion = current.Status, current.Conclusion
	}
	if conclusion != "success" {
		return fmt.Errorf("Release workflow ended with %s. Check GitHub Actions before retrying.", conclusion)
	}
	if err := run("gh", "release", "view", tag); err != nil {
		return err
	}
	if notes != "" {
		return run("gh", "release", "edit", tag, "--notes", notes)
	}
	return nil
}

// output runs a command and returns its stdout without the last newline; stderr goes to the log.
func output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}
