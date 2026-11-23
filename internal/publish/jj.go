package publish

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// The checkout is a colocated jj repository. Its working-copy commit, @, holds the uncommitted
// edits; its parents are the base branch and the changes published from this checkout that have
// not merged yet, so Git's HEAD is the base and "git status" lists both. Each published change
// carries its branch's bookmark. Every jj command that rewrites @ or one of its parents runs in
// that checkout, never from another workspace: a workspace rewritten from elsewhere goes stale,
// and edits made while it is stale do not survive the update.

func jjCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("jj", append([]string{"--no-pager", "--color=never"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "JJ_EDITOR=false")
	return cmd
}

// jjOut runs jj and returns its output; an error carries what jj printed.
func jjOut(dir string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := jjCmd(dir, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("jj %s: %s", args[0], message)
	}
	return strings.TrimSuffix(stdout.String(), "\n"), nil
}

// jjRun runs jj with its messages on the console.
func jjRun(dir string, args ...string) error {
	var stderr bytes.Buffer
	cmd := jjCmd(dir, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, &stderr
	err := cmd.Run()
	os.Stdout.Write(stderr.Bytes())
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(message, "Error: "); i >= 0 {
			message = message[i+len("Error: "):]
		}
		return fmt.Errorf("jj %s: %s", args[0], message)
	}
	return nil
}

// ids lists the change IDs of a revset, newest first.
func ids(dir, revset string) ([]string, error) {
	out, err := jjOut(dir, "log", "--no-graph", "-r", revset, "-T", `change_id ++ "\n"`)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func exists(dir, revset string) bool {
	found, err := ids(dir, revset)
	return err == nil && len(found) > 0
}

// commitID is the commit a revset names now.
func commitID(dir, revset string) (string, error) {
	return jjOut(dir, "log", "--no-graph", "-r", revset, "-T", "commit_id")
}

// files lists the files a revset's commits change together.
func files(dir, revset string) ([]string, error) {
	out, err := jjOut(dir, "diff", "--name-only", "-r", revset)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// newChange makes an empty change on parents without moving @ and returns its change ID.
func newChange(dir, message string, parents ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := jjCmd(dir, append([]string{"new", "--no-edit", "-m", message}, parents...)...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("jj new: %s", strings.TrimSpace(stderr.String()))
	}
	match := createdLine.FindStringSubmatch(stderr.String())
	if match == nil {
		return "", errors.New("jj new did not name the change it created")
	}
	found, err := ids(dir, match[1])
	if err != nil || len(found) != 1 {
		return "", errors.New("could not find the new change")
	}
	return found[0], nil
}

var createdLine = regexp.MustCompile(`Created new commit ([k-z]+) `)

// quote makes a jj string literal.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}

// fileset names paths relative to the repository root.
func fileset(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = "root-file:" + quote(filepath.ToSlash(p))
	}
	return out
}

// bookmark is the revset of a local bookmark.
func bookmark(name string) string { return "bookmarks(exact:" + quote(name) + ")" }

// remote is the revset of a branch on a remote.
func remote(name, remote string) string {
	return "remote_bookmarks(exact:" + quote(name) + ", remote=exact:" + quote(remote) + ")"
}

func trunkOf(base string) string { return remote(base, "origin") }
