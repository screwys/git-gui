// Package git runs the quick Git reads the window needs. Anything long-running is a daemon job.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// EmptyTree is Git's tree with no files, the base of a repository without commits.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// WorkingTree as the right side of a comparison means the files on disk.
const WorkingTree = ""

// File is one changed path in a comparison.
type File struct {
	Path      string
	OldPath   string // set for renames
	Status    string // A, M, D, R, U (untracked)
	Adds      int
	Dels      int
	Binary    bool
	Committed bool // already committed on the branch; not selectable for a new commit
}

// Run runs git in repo and returns stdout without its last newline.
func Run(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return "", errors.New(message)
		}
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// IsRepo reports whether dir is inside a Git work tree.
func IsRepo(dir string) bool {
	out, err := Run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// TopLevel is the work tree root containing dir.
func TopLevel(dir string) (string, error) { return Run(dir, "rev-parse", "--show-toplevel") }

// Head is the current commit, or "" in a repository without commits.
func Head(repo string) string {
	out, _ := Run(repo, "rev-parse", "--verify", "-q", "HEAD")
	return out
}

// CurrentBranch is the branch name, else the short commit when detached, else "unknown".
func CurrentBranch(repo string) string {
	if out, err := Run(repo, "symbolic-ref", "--short", "-q", "HEAD"); err == nil && out != "" {
		return out
	}
	if out, err := Run(repo, "rev-parse", "--short", "HEAD"); err == nil {
		return out
	}
	return "unknown"
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(\.git)?$`)

// RemoteSlug is "owner/name" of origin on GitHub, or "".
func RemoteSlug(repo string) string {
	out, err := Run(repo, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	if m := githubRemote.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// Draft is the PR title and body Rufin's publish helper saved for a branch.
func Draft(repo, branch string) (title string, body string, hasBody bool) {
	title, _ = Run(repo, "config", "--local", "--get", "branch."+branch+".rufin-pr-title")
	body, err := Run(repo, "config", "--local", "--get", "branch."+branch+".rufin-pr-body")
	return title, body, err == nil
}

// LatestTag is the nearest tag reachable from HEAD, or "".
func LatestTag(repo string) string {
	out, _ := Run(repo, "describe", "--tags", "--abbrev=0")
	return out
}

// Tags lists tags, highest version first.
func Tags(repo string) []string {
	out, _ := Run(repo, "tag", "--sort=-version:refname")
	return lines(out)
}

// Refs lists local branches, remote branches and tags.
func Refs(repo string) []string {
	out, _ := Run(repo, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes", "refs/tags")
	return lines(out)
}

// HasRef reports whether ref names a commit.
func HasRef(repo, ref string) bool {
	_, err := Run(repo, "rev-parse", "--verify", "-q", ref+"^{commit}")
	return err == nil
}

// MergeBase is the best common ancestor of two commits.
func MergeBase(repo, a, b string) (string, error) { return Run(repo, "merge-base", a, b) }

// Compare lists the paths that differ between left and right. Right WorkingTree means the files on
// disk; includeUntracked then adds files Git does not track yet.
func Compare(repo, left, right string, includeUntracked bool) ([]File, error) {
	args := []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--find-renames", "-z"}
	refs := []string{left}
	if right != WorkingTree {
		refs = append(refs, right)
	}
	status, err := Run(repo, append(append(append([]string{}, args...), "--name-status"), refs...)...)
	if err != nil {
		return nil, err
	}
	numstat, err := Run(repo, append(append(append([]string{}, args...), "--numstat"), refs...)...)
	if err != nil {
		return nil, err
	}
	files := parseNameStatus(status)
	counts := parseNumstat(numstat)
	for i := range files {
		if c, ok := counts[files[i].Path]; ok {
			files[i].Adds, files[i].Dels, files[i].Binary = c.Adds, c.Dels, c.Binary
		}
	}
	if right == WorkingTree && includeUntracked {
		out, err := Run(repo, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, path := range strings.Split(strings.Trim(out, "\x00"), "\x00") {
			if path != "" {
				adds, binary := countLines(filepath.Join(repo, path))
				files = append(files, File{Path: path, Status: "U", Adds: adds, Binary: binary})
			}
		}
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// Diff is the unified diff of the given files between left and right; nil files means all.
func Diff(repo, left, right string, files []File) (string, error) {
	var tracked, untracked []string
	for _, f := range files {
		if f.Status == "U" {
			untracked = append(untracked, f.Path)
			continue
		}
		tracked = append(tracked, f.Path)
		if f.OldPath != "" {
			tracked = append(tracked, f.OldPath)
		}
	}
	var out strings.Builder
	if files == nil || len(tracked) > 0 {
		args := []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--find-renames", left}
		if right != WorkingTree {
			args = append(args, right)
		}
		if files != nil {
			args = append(append(args, "--"), tracked...)
		}
		text, err := Run(repo, args...)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
	}
	if files == nil && right == WorkingTree {
		listed, _ := Run(repo, "ls-files", "--others", "--exclude-standard", "-z")
		for _, path := range strings.Split(strings.Trim(listed, "\x00"), "\x00") {
			if path != "" {
				untracked = append(untracked, path)
			}
		}
	}
	for _, path := range untracked {
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(newFileDiff(repo, path))
	}
	return out.String(), nil
}

// newFileDiff shows an untracked file as added; Git exits 1 when the files differ.
func newFileDiff(repo, path string) string {
	cmd := exec.Command("git", "--literal-pathspecs", "diff", "--no-index", "--no-ext-diff", "--", "/dev/null", path)
	cmd.Dir = repo
	out, _ := cmd.Output()
	return strings.TrimSuffix(string(out), "\n")
}

type count struct {
	Adds, Dels int
	Binary     bool
}

func parseNameStatus(out string) []File {
	fields := strings.Split(strings.Trim(out, "\x00"), "\x00")
	var files []File
	for i := 0; i < len(fields); i++ {
		code := fields[i]
		if code == "" || i+1 >= len(fields) {
			continue
		}
		f := File{Status: code[:1], Path: fields[i+1]}
		i++
		if (code[0] == 'R' || code[0] == 'C') && i+1 < len(fields) {
			f.OldPath, f.Path = f.Path, fields[i+1]
			i++
		}
		if f.Status == "T" || f.Status == "C" {
			f.Status = "M"
		}
		files = append(files, f)
	}
	return files
}

// parseNumstat reads "adds\tdels\tpath" records; renames carry two extra NUL-separated paths.
func parseNumstat(out string) map[string]count {
	counts := map[string]count{}
	fields := strings.Split(strings.Trim(out, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) < 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(fields) {
			path = fields[i+2]
			i += 2
		}
		adds, err1 := strconv.Atoi(parts[0])
		dels, err2 := strconv.Atoi(parts[1])
		counts[path] = count{adds, dels, err1 != nil || err2 != nil}
	}
	return counts
}

// countLines counts a new file's lines; a NUL in its first 8000 bytes marks it binary.
func countLines(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return 0, true
	}
	n := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n, false
}

// Commit is one entry of the history list.
type Commit struct {
	Hash, Short, Date, Subject string
}

// Log lists the newest n commits reachable from HEAD.
func Log(repo string, n int) ([]Commit, error) {
	out, err := Run(repo, "log", fmt.Sprintf("-%d", n), "--format=%H%x00%h%x00%cs%x00%s", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range lines(out) {
		parts := strings.SplitN(line, "\x00", 4)
		if len(parts) == 4 {
			commits = append(commits, Commit{parts[0], parts[1], parts[2], parts[3]})
		}
	}
	return commits, nil
}

// Parent is a commit's first parent, or the empty tree for a root commit.
func Parent(repo, commit string) string {
	if out, err := Run(repo, "rev-parse", "--verify", "-q", commit+"^1"); err == nil {
		return out
	}
	return EmptyTree
}

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path, Branch, Head string
	Bare, Prunable     bool
}

// Worktrees lists the repository's work trees.
func Worktrees(repo string) []Worktree {
	out, err := Run(repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil
	}
	var trees []Worktree
	var current *Worktree
	for _, field := range strings.Split(out, "\x00") {
		key, value, _ := strings.Cut(field, " ")
		switch key {
		case "worktree":
			trees = append(trees, Worktree{Path: value})
			current = &trees[len(trees)-1]
		case "branch":
			current.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "HEAD":
			current.Head = value
		case "bare":
			current.Bare = true
		case "prunable":
			current.Prunable = true
		}
	}
	return trees
}

func lines(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}
