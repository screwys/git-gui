// Package publish publishes work from one shared checkout as pull requests without switching it.
// The chosen files are committed onto the topic branch in a separate worktree, where the
// project's generate and check commands run on exactly what is published; the published
// versions then come back to the checkout.
package publish

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/screwys/git-gui/internal/git"
)

// Options is one publish. Generate and Check are shell commands run in the publish worktree;
// Check gets the base ref as its last argument.
type Options struct {
	Base, Branch, Title string
	Paths               []string // with Title and no paths: every uncommitted change
	Body                string
	BodySet             bool
	Run                 string // a command whose changes in the worktree are published
	Generate, Check     string
	Full, Instant       bool
	PrepareOnly         bool
	AutoMerge           bool
	MergeCommit         bool
}

// Result is what a publish made. Queued names what a queued publish waits for.
type Result struct {
	Branch   string
	Worktree string
	Number   int
	URL      string
	Queued   []string
}

// AllowBodyEnv must be 1 for a publish that sets the PR body: bodies are the user's own text.
const AllowBodyEnv = "GIT_GUI_ALLOW_PR_BODY"

const worktreeBranch = "git-gui-publish"

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Checkout is the shared checkout's state: its root, Git directory and lock.
type Checkout struct {
	Root, Common string
}

// Open finds the checkout containing dir.
func Open(dir string) (Checkout, error) {
	root, err := gitOut(dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return Checkout{}, err
	}
	common, err := gitOut(root, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return Checkout{root, common}, err
}

// Lock holds the checkout's publish lock until the returned function runs. Without wait it
// reports false when another publish holds it.
func (c Checkout) Lock(wait bool) (func(), bool) {
	file, err := os.OpenFile(filepath.Join(c.Common, "git-gui-publish.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, false
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	if syscall.Flock(int(file.Fd()), how) != nil {
		file.Close()
		return func() {}, false
	}
	return func() { file.Close() }, true
}

// Worktree is the publish worktree's path.
func (c Checkout) Worktree() string { return filepath.Join(c.Common, "git-gui", "publish") }

// BranchFor names the topic branch from the title before its first colon.
// For example, "ui/library: ..." becomes "ui-library".
func BranchFor(title string) string {
	name, _, scoped := strings.Cut(title, ":")
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	if slug == "" {
		slug = "update"
	}
	if !scoped {
		return "chore/" + slug
	}
	return slug
}

// ResolveBranch is the branch a publish targets: the given one, the current branch, or one named
// after the title on the base branch.
func ResolveBranch(root string, o Options) (string, error) {
	if o.Branch != "" {
		return o.Branch, checkRefName(o.Branch)
	}
	current, _ := gitOut(root, nil, "branch", "--show-current")
	if current == "" || current == o.Base {
		if o.Title == "" {
			return "", fmt.Errorf("a title is needed to publish from %s", o.Base)
		}
		branch := BranchFor(o.Title)
		return branch, checkRefName(branch)
	}
	return current, nil
}

func checkRefName(branch string) error {
	if _, err := gitOut(".", nil, "check-ref-format", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("branch name is invalid: %s", branch)
	}
	return nil
}

// Publish commits and pushes the work, opens or updates its PR and enables auto-merge.
func Publish(dir string, o Options) (Result, error) {
	c, err := Open(dir)
	if err != nil {
		return Result{}, err
	}
	if o.BodySet && os.Getenv(AllowBodyEnv) != "1" {
		return Result{}, fmt.Errorf("refusing a PR body; set %s=1 only for text the user gave", AllowBodyEnv)
	}
	unlock, _ := c.Lock(true)
	defer unlock()
	root := c.Root
	if err := gitRun(root, nil, "fetch", "--prune", "origin"); err != nil {
		return Result{}, err
	}
	if err := c.sync(o.Base); err != nil {
		fmt.Println(err)
	}
	slug := git.RemoteSlug(root)
	if slug == "" {
		return Result{}, errors.New("origin is not a GitHub repository")
	}

	if o.Title != "" && o.Paths == nil {
		o.Paths, err = dirty(root)
		if err != nil {
			return Result{}, err
		}
	}
	branch, err := ResolveBranch(root, o)
	if err != nil {
		return Result{}, err
	}
	fmt.Println("Publishing branch:", branch)
	current, _ := gitOut(root, nil, "branch", "--show-current")
	remoteBranch := hasRef(root, "refs/remotes/origin/"+branch)

	// Paths whose edits build on another open PR's or queued publish's edits in the same files
	// queue behind them; a publish of a queued branch without paths publishes its queue commit
	// once what it waits for merged.
	queue := Queue(root)
	own, isQueued := queue[branch]
	final := isQueued && len(o.Paths) == 0 && o.Run == ""
	var behind, subtract []carrier
	switch {
	case final:
		all, err := carriers(root, slug, o.Base, branch, queue)
		if err != nil {
			return Result{}, err
		}
		for _, c := range all {
			if slices.Contains(own.Behind, c.branch) {
				behind = append(behind, c)
			}
		}
		if len(behind) > 0 {
			return queued(branch, behind), nil
		}
		if o.Title == "" {
			o.Title, _ = gitOut(root, nil, "log", "-1", "--format=%s", own.Commit)
		}
	case len(o.Paths) > 0 && !o.PrepareOnly:
		for _, f := range own.Files {
			if !slices.Contains(o.Paths, f) {
				o.Paths = append(o.Paths, f)
			}
		}
		all, err := carriers(root, slug, o.Base, branch, queue)
		if err != nil {
			return Result{}, err
		}
		behind, subtract, o.Paths = plan(root, branch, o.Paths, all)
		if len(o.Paths) == 0 {
			return Result{}, errors.New("the requested paths hold only other open PRs' edits")
		}
	}

	// The PR title is the first title given; it survives failed pushes in branch config.
	prTitle, _ := gitOut(root, nil, "config", "--local", "--get", "branch."+branch+".rufin-pr-title")
	if prTitle == "" {
		prTitle, _ = ghOut(root, "pr", "list", "--repo", slug, "--head", branch, "--state", "open", "--json", "title", "--jq", ".[0].title // empty")
	}
	if prTitle == "" {
		prTitle = o.Title
	}
	if prTitle == "" {
		prTitle, _ = gitOut(root, nil, "log", "-1", "--format=%s")
	}
	_ = gitRun(root, nil, "config", "--local", "branch."+branch+".rufin-pr-title", prTitle)
	if o.BodySet {
		_ = gitRun(root, nil, "config", "--local", "branch."+branch+".rufin-pr-body", o.Body)
	} else if body, err := gitOut(root, nil, "config", "--local", "--get", "branch."+branch+".rufin-pr-body"); err == nil {
		o.Body, o.BodySet = body, true
	}
	title := o.Title
	if title == "" {
		title = prTitle
	}

	// Paths go out as their difference from what the branch already has here: an open PR's files
	// were written back, so later paths carry only the new edits.
	var patch []byte
	var snapshot string
	start := "origin/" + o.Base
	switch {
	case final:
		if remoteBranch {
			start = "origin/" + branch
		}
		if patch, err = gitBytes(root, "diff", "--binary", own.Commit+"^", own.Commit); err != nil {
			return Result{}, err
		}
	case len(o.Paths) > 0 || o.Run != "":
		patchBase := "HEAD"
		switch {
		case len(behind) > 0:
			remote := ""
			if remoteBranch {
				remote = "origin/" + branch
			}
			if patchBase, err = queueBase(root, remote, behind); err != nil {
				return Result{}, err
			}
			start = patchBase
		case current == branch:
			start = "HEAD"
		case remoteBranch:
			patchBase, start = "origin/"+branch, "origin/"+branch
		}
		if len(o.Paths) > 0 {
			if snapshot, err = snapshotTree(root, patchBase, o.Paths); err != nil {
				return Result{}, err
			}
			if patch, err = gitBytes(root, "diff", "--binary", patchBase, snapshot); err != nil {
				return Result{}, err
			}
			if len(patch) == 0 && o.Run == "" {
				return Result{}, errors.New("no changes found for the requested paths")
			}
		}
	case hasRef(root, "refs/heads/"+branch):
		start = "refs/heads/" + branch
	case remoteBranch:
		start = "origin/" + branch
	default:
		return Result{}, fmt.Errorf("branch %s does not exist; give paths to create it", branch)
	}
	startCommit, err := gitOut(root, nil, "rev-parse", "--verify", start+"^{commit}")
	if err != nil {
		return Result{}, err
	}
	work, err := c.enterWorktree(startCommit)
	if err != nil {
		return Result{}, err
	}
	env := CheckEnv(root)

	// A queue commit stays on its base; the final publish merges the base branch.
	if _, err := gitOut(work, nil, "merge-base", "--is-ancestor", "origin/"+o.Base, "HEAD"); err != nil && len(behind) == 0 {
		// A merge keeps the branch's commits and signatures as they are.
		if err := gitRun(work, nil, "merge", "-q", "--no-edit", "origin/"+o.Base); err != nil {
			_ = gitRun(work, nil, "merge", "--abort")
			return Result{}, fmt.Errorf("could not merge origin/%s into %s; merge it by hand and publish again", o.Base, branch)
		}
	}
	runStart, _ := gitOut(work, nil, "rev-parse", "HEAD")
	if len(patch) > 0 {
		apply := exec.Command("git", "apply", "--3way", "--binary")
		apply.Dir, apply.Stdin, apply.Stdout, apply.Stderr = work, bytes.NewReader(patch), os.Stdout, os.Stderr
		if apply.Run() != nil {
			return Result{}, fmt.Errorf("the requested paths do not apply onto %s; sync them and retry", branch)
		}
	}
	// Queued publishes' edits in these files are theirs: take them back out.
	var subtracted []string
	for _, c := range subtract {
		files := slices.DeleteFunc(slices.Clone(c.files), func(f string) bool { return !slices.Contains(o.Paths, f) })
		reverse, err := gitBytes(root, append([]string{"diff", "--binary", c.commit + "^", c.commit, "--"}, files...)...)
		if err != nil || len(reverse) == 0 {
			continue
		}
		apply := exec.Command("git", "apply", "-R", "--3way", "--binary")
		apply.Dir, apply.Stdin, apply.Stdout, apply.Stderr = work, bytes.NewReader(reverse), os.Stdout, os.Stderr
		if apply.Run() != nil {
			return Result{}, fmt.Errorf("these edits change the same lines in %s as %s, queued behind other work; cancel its queued publish in Git GUI to publish both here", strings.Join(files, ", "), c.branch)
		}
		subtracted = append(subtracted, files...)
	}
	if len(patch) > 0 {
		_ = gitRun(work, nil, "reset", "-q")
	}
	if o.Run != "" {
		if err := shell(work, env, o.Run); err != nil {
			return Result{}, err
		}
	}
	if !o.Instant && o.Generate != "" {
		if err := shell(work, env, o.Generate); err != nil {
			return Result{}, err
		}
	}
	changed, _ := gitOut(work, nil, "status", "--porcelain")
	fromWork := len(o.Paths) > 0 || o.Run != "" || final
	switch {
	case changed != "" && fromWork && o.Title != "":
		_ = gitRun(work, nil, "add", "-A")
		if err := gitRun(work, nil, "commit", "-q", "-m", o.Title); err != nil {
			return Result{}, err
		}
	case changed != "" && fromWork:
		return Result{}, errors.New("a title is needed to commit the requested changes")
	case changed != "":
		_ = gitRun(work, nil, "add", "-A")
		if err := gitRun(work, nil, "commit", "-q", "-m", "generated: refresh formatting and generated files"); err != nil {
			return Result{}, err
		}
	}
	if count, _ := gitOut(work, nil, "rev-list", "--count", "origin/"+o.Base+"..HEAD"); count == "0" {
		return Result{}, fmt.Errorf("no commits to publish on %s", branch)
	}
	runEnd, _ := gitOut(work, nil, "rev-parse", "HEAD")
	result := Result{Branch: branch, Worktree: work}
	if o.PrepareOnly {
		fmt.Println("Prepared PR worktree:", work)
		return result, nil
	}
	if len(behind) > 0 {
		if runEnd == runStart {
			return result, errors.New("no changes found for the requested paths")
		}
		// The checks look at the queued commit alone; CI checks it on the base branch.
		if err := runChecks(work, runStart, branch, o, env); err != nil {
			return result, err
		}
		if err := gitRun(root, nil, "update-ref", queueRefs+branch, runEnd); err != nil {
			return result, err
		}
		writeBack(root, snapshot, runStart, runEnd, subtracted)
		return queued(branch, behind), nil
	}

	if remoteBranch {
		if _, err := gitOut(work, nil, "merge-base", "--is-ancestor", "origin/"+branch, "HEAD"); err != nil {
			return result, fmt.Errorf("origin/%s has commits this branch lacks; merge them and publish again", branch)
		}
	}
	// A queue commit's checks already ran; what merging changed is CI's.
	if !final {
		if err := runChecks(work, "origin/"+o.Base, branch, o, env); err != nil {
			return result, err
		}
	}
	if err := gitRun(work, nil, "push", "--no-verify", "origin", "HEAD:refs/heads/"+branch); err != nil {
		return result, err
	}

	number := openPR(root, slug, branch)
	if number == 0 {
		if err := ghRun(root, "pr", "create", "--repo", slug, "--base", o.Base, "--head", branch, "--title", prTitle, "--body", o.Body); err != nil {
			return result, err
		}
		number = openPR(root, slug, branch)
	} else if o.BodySet {
		_ = ghRun(root, "pr", "edit", strconv.Itoa(number), "--repo", slug, "--body", o.Body)
	}
	if number == 0 {
		return result, fmt.Errorf("could not find the PR for %s", branch)
	}
	_ = gitRun(root, nil, "config", "--local", "--unset-all", "branch."+branch+".rufin-pr-title")
	_ = gitRun(root, nil, "config", "--local", "--unset-all", "branch."+branch+".rufin-pr-body")
	result.Number = number
	result.URL, _ = ghOut(root, "pr", "view", strconv.Itoa(number), "--repo", slug, "--json", "url", "--jq", ".url")
	if o.AutoMerge {
		method := "--squash"
		if o.MergeCommit {
			method = "--merge"
		}
		if err := ghRun(root, "pr", "merge", strconv.Itoa(number), "--repo", slug, "--auto", method, "--delete-branch"); err != nil {
			fmt.Println("Auto-merge not enabled:", err)
		}
	}

	if !branchCheckedOut(root, branch) {
		_ = gitRun(root, nil, "update-ref", "refs/heads/"+branch, runEnd)
	}
	if snapshot != "" {
		writeBack(root, snapshot, runStart, runEnd, subtracted)
	}
	if isQueued {
		_ = Dequeue(root, branch)
	}
	fmt.Printf("Published PR #%d: %s\n", number, result.URL)
	return result, nil
}

func queued(branch string, behind []carrier) Result {
	names := make([]string, len(behind))
	for i, c := range behind {
		names[i] = c.String()
	}
	fmt.Printf("Queued %s behind %s: git-gui-daemon publishes it once they merge.\n", branch, strings.Join(names, ", "))
	return Result{Branch: branch, Queued: names}
}

// runChecks runs the check command on the commits since base, or with Full the pre-push hook,
// the full gate.
func runChecks(work, base, branch string, o Options, env []string) error {
	var check *exec.Cmd
	switch {
	case o.Instant:
		return nil
	case o.Full:
		remote, _ := gitOut(work, nil, "remote", "get-url", "origin")
		head, _ := gitOut(work, nil, "rev-parse", "HEAD")
		old, err := gitOut(work, nil, "rev-parse", "-q", "--verify", "origin/"+branch)
		if err != nil {
			old = strings.Repeat("0", len(head))
		}
		check = Capped(exec.Command("git", "hook", "run", "--ignore-missing", "pre-push", "--", "origin", remote), work, env)
		check.Stdin = strings.NewReader(fmt.Sprintf("refs/heads/%s %s refs/heads/%s %s\n", branch, head, branch, old))
	case o.Check != "":
		check = Capped(exec.Command("sh", "-c", o.Check+` "$0"`, base), work, env)
	default:
		return nil
	}
	if err := check.Run(); err != nil {
		return fmt.Errorf("checks failed: %w", err)
	}
	return nil
}

// Sync brings the checkout's base branch up to origin, merging local edits into the files the
// new commits touch. Without wait it does nothing while a publish runs.
func Sync(dir, base string, wait bool) (bool, error) {
	c, err := Open(dir)
	if err != nil {
		return false, err
	}
	unlock, locked := c.Lock(wait)
	defer unlock()
	if !locked {
		return false, nil
	}
	if err := gitRun(c.Root, nil, "fetch", "--prune", "origin"); err != nil {
		return false, err
	}
	return true, c.sync(base)
}

// sync moves the base branch here to origin. Every file the incoming commits touch takes the merged
// version, with a local edit merged in from the version it was made on; nothing changes when any
// of them conflicts.
func (c Checkout) sync(base string) error {
	root, target := c.Root, "origin/"+base
	if current, _ := gitOut(root, nil, "branch", "--show-current"); current != base {
		return fmt.Errorf("not on %s; not syncing", base)
	}
	head, _ := gitOut(root, nil, "rev-parse", "HEAD")
	if want, _ := gitOut(root, nil, "rev-parse", target); head == want {
		return nil
	}
	if _, err := gitOut(root, nil, "merge-base", "--is-ancestor", "HEAD", target); err != nil {
		return fmt.Errorf("%s has commits that %s does not; not syncing", base, target)
	}
	listed, err := gitOut(root, nil, "diff", "--no-renames", "--name-only", "-z", "HEAD", target)
	if err != nil {
		return err
	}
	type write struct {
		path string
		data []byte
		exec bool
	}
	var writes []write
	var removals, conflicts []string
	for _, path := range splitZ(listed) {
		local := hashFile(root, path)
		ours := blob(root, "HEAD", path)
		theirs := blob(root, target, path)
		switch {
		case local == theirs:
			continue
		case local == ours && theirs == "":
			removals = append(removals, path)
			continue
		}
		var merged []byte
		switch {
		case local == ours:
			merged, _ = gitBytes(root, "cat-file", "blob", theirs)
		case local != "" && theirs != "":
			if merged, err = mergeLocal(root, path, target, theirs); err != nil {
				conflicts = append(conflicts, path)
				continue
			}
		default:
			conflicts = append(conflicts, path)
			continue
		}
		mode, _ := gitOut(root, nil, "ls-tree", target, "--", path)
		writes = append(writes, write{path, merged, strings.HasPrefix(mode, "100755")})
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("local edits conflict with %s: %s; %s not synced", target, strings.Join(conflicts, ", "), base)
	}
	for _, w := range writes {
		full := filepath.Join(root, w.path)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, w.data, 0o644); err != nil {
			return err
		}
		if w.exec {
			_ = os.Chmod(full, 0o755)
		}
	}
	for _, path := range removals {
		_ = os.Remove(filepath.Join(root, path))
	}
	return gitRun(root, nil, "reset", "-q", "--mixed", target)
}

// mergeLocal merges target's version of path into the local copy, from the version the local
// copy differs from least: the one it was edited from, such as a copy a PR published from here.
func mergeLocal(root, path, target, theirs string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "git-gui-sync-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	full := filepath.Join(root, path)
	commits, _ := gitOut(root, nil, "rev-list", "HEAD.."+target, "--", path)
	best, bestSize := "", -1
	for _, commit := range append([]string{"HEAD"}, strings.Fields(commits)...) {
		candidate := blob(root, commit, path)
		if candidate == "" {
			continue
		}
		data, _ := gitBytes(root, "cat-file", "blob", candidate)
		file := filepath.Join(dir, "candidate")
		_ = os.WriteFile(file, data, 0o600)
		stat, _ := exec.Command("git", "diff", "--no-index", "--numstat", "--", file, full).Output()
		fields, size := strings.Fields(string(stat)), 0
		for _, field := range fields[:min(2, len(fields))] {
			n, _ := strconv.Atoi(field)
			size += n
		}
		if bestSize < 0 || size < bestSize {
			best, bestSize = candidate, size
		}
	}
	baseFile, theirsFile := filepath.Join(dir, "base"), filepath.Join(dir, "theirs")
	baseData, _ := gitBytes(root, "cat-file", "blob", best)
	theirsData, _ := gitBytes(root, "cat-file", "blob", theirs)
	_ = os.WriteFile(baseFile, baseData, 0o600)
	_ = os.WriteFile(theirsFile, theirsData, 0o600)
	return exec.Command("git", "merge-file", "-q", "-p", full, baseFile, theirsFile).Output()
}

// enterWorktree points the publish worktree at start, clean.
func (c Checkout) enterWorktree(start string) (string, error) {
	path := c.Worktree()
	if _, err := os.Stat(path); err != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		_ = gitRun(c.Root, nil, "worktree", "prune")
		return path, gitRun(c.Root, nil, "worktree", "add", "-q", "-B", worktreeBranch, path, start)
	}
	if err := gitRun(path, nil, "switch", "-q", "-f", "-C", worktreeBranch, start); err != nil {
		return "", err
	}
	return path, gitRun(path, nil, "clean", "-fdq")
}

// snapshotTree is base's tree with the paths as they are in the checkout, built in a scratch index.
func snapshotTree(root, base string, paths []string) (string, error) {
	dir, err := os.MkdirTemp("", "git-gui-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}
	if err := gitRun(root, env, "read-tree", base); err != nil {
		return "", err
	}
	if err := gitRun(root, env, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return "", err
	}
	return gitOut(root, env, "write-tree")
}

// writeBack puts the published versions of files formatting or generation changed back here. A
// file edited here since the snapshot keeps its edit, and so does one that still holds a queued
// publish's edits; the next sync merges them.
func writeBack(root, snapshot, from, to string, keep []string) {
	listed, _ := gitOut(root, nil, "diff", "--no-renames", "--name-only", "-z", from, to)
	for _, path := range splitZ(listed) {
		if slices.Contains(keep, path) || hashFile(root, path) != blob(root, snapshot, path) {
			continue
		}
		full := filepath.Join(root, path)
		published := blob(root, to, path)
		if published == "" {
			_ = os.Remove(full)
			continue
		}
		data, err := gitBytes(root, "cat-file", "blob", published)
		if err == nil {
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			_ = os.WriteFile(full, data, 0o644)
		}
	}
}

// dirty lists every modified, deleted and untracked path.
func dirty(root string) ([]string, error) {
	changed, err := gitOut(root, nil, "diff", "--no-renames", "--name-only", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	untracked, err := gitOut(root, nil, "ls-files", "--others", "--exclude-standard", "-z")
	paths := append(splitZ(changed), splitZ(untracked)...)
	if paths == nil {
		paths = []string{}
	}
	return paths, err
}

func openPR(root, slug, branch string) int {
	out, _ := ghOut(root, "pr", "list", "--repo", slug, "--head", branch, "--state", "open", "--json", "number", "--jq", ".[0].number // empty")
	n, _ := strconv.Atoi(out)
	return n
}

func branchCheckedOut(root, branch string) bool {
	list, _ := gitOut(root, nil, "worktree", "list", "--porcelain")
	return strings.Contains(list+"\n", "branch refs/heads/"+branch+"\n")
}

// CheckEnv lets a Cargo project's commands in the publish worktree share the checkout's builds.
func CheckEnv(root string) []string {
	if _, err := os.Stat(filepath.Join(root, "Cargo.toml")); err == nil && os.Getenv("CARGO_TARGET_DIR") == "" {
		return []string{"CARGO_TARGET_DIR=" + filepath.Join(root, "target")}
	}
	return nil
}

// Capped runs cmd in dir inside a user scope with memory and CPU limits, so heavy checks slow
// down instead of freezing the desktop.
func Capped(cmd *exec.Cmd, dir string, env []string) *exec.Cmd {
	if path, err := exec.LookPath("systemd-run"); err == nil {
		args := append([]string{"--user", "--scope", "--quiet", "-p", "MemoryHigh=12G", "-p", "MemoryMax=16G",
			"-p", "MemorySwapMax=0", "-p", "CPUWeight=10", "-p", "IOWeight=10", "--"}, cmd.Args...)
		cmd = exec.Command(path, args...)
	}
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

func shell(dir string, env []string, command string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	return nil
}

func hashFile(root, path string) string {
	if info, err := os.Stat(filepath.Join(root, path)); err != nil || info.IsDir() {
		return ""
	}
	hash, _ := gitOut(root, nil, "hash-object", "--", path)
	return hash
}

func blob(root, rev, path string) string {
	hash, _ := gitOut(root, nil, "rev-parse", "-q", "--verify", rev+":"+path)
	return hash
}

func hasRef(root, ref string) bool {
	_, err := gitOut(root, nil, "rev-parse", "-q", "--verify", ref)
	return err == nil
}

func splitZ(s string) []string {
	var out []string
	for _, part := range strings.Split(s, "\x00") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func gitOut(dir string, env []string, args ...string) (string, error) {
	out, err := gitCmd(dir, env, args...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}

func gitBytes(dir string, args ...string) ([]byte, error) { return gitCmd(dir, nil, args...).Output() }

func gitRun(dir string, env []string, args ...string) error {
	cmd := gitCmd(dir, env, args...)
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitCmd(dir string, env []string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Stderr = dir, os.Stderr
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd
}

func ghOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir, cmd.Stderr = dir, os.Stderr
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func ghJSON(dir string, out any, args ...string) error {
	text, err := ghOut(dir, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(text), out)
}

func ghRun(dir string, args ...string) error {
	cmd := exec.Command("gh", args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh %s: %w", strings.Join(args[:min(2, len(args))], " "), err)
	}
	return nil
}
