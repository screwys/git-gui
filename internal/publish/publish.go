// Package publish publishes work from a jj checkout as pull requests without switching it. The
// chosen files' edits move from the working copy into the branch's own change, which stays a
// parent of the working copy until it merges; the project's generate and check commands run on
// exactly that change in a separate publish workspace.
package publish

import (
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

// Options is one publish. Generate and Check are shell commands run in the publish workspace;
// Check gets the base ref as its last argument.
type Options struct {
	Base, Branch, Title string
	PR                  int      // publish onto this open PR's branch, a contributor's fork included
	Paths               []string // with Title and no paths: every uncommitted change
	Body                string
	BodySet             bool
	Run                 string // a command whose changes in the publish workspace are published
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

// pullRefs hold the heads of PRs someone else opened that were published onto from here; jj reads
// one as the branch "N@pull". A fork's PR is the bookmark "pr/N" here.
const pullRefs = "refs/remotes/pull/"

var (
	nonSlug    = regexp.MustCompile(`[^a-z0-9]+`)
	forkBranch = regexp.MustCompile(`^pr/([1-9][0-9]*)$`)
)

// ForkBranch names the branch that stands here for a contributor's fork PR.
func ForkBranch(number int) string { return fmt.Sprintf("pr/%d", number) }

func forkNumber(branch string) int {
	match := forkBranch.FindStringSubmatch(branch)
	if match == nil {
		return 0
	}
	n, _ := strconv.Atoi(match[1])
	return n
}

// Adopted lists the PRs someone else opened that were published onto from the checkout.
func Adopted(root string) []int {
	var numbers []int
	listed, _ := gitOut(root, nil, "for-each-ref", "--format=%(refname)", pullRefs)
	for _, ref := range strings.Fields(listed) {
		if n, _ := strconv.Atoi(strings.TrimPrefix(ref, pullRefs)); n > 0 {
			numbers = append(numbers, n)
		}
	}
	return numbers
}

// pull is a PR as "gh pr view" describes it.
type pull struct {
	State, HeadRefName, BaseRefName        string
	IsCrossRepository, MaintainerCanModify bool
	HeadRepository                         struct{ Name string }
	HeadRepositoryOwner                    struct{ Login string }
	AutoMergeRequest                       *struct{}
}

// Checkout is one jj workspace of the repository: the shared checkout or a session's own.
type Checkout struct {
	Root   string // this workspace's root
	Common string // the Git directory every workspace shares: the lock and the publish workspace
	Main   string // the shared checkout's root
}

// Open finds the checkout containing dir.
func Open(dir string) (Checkout, error) {
	root, err := jjOut(dir, "workspace", "root")
	if err != nil {
		return Checkout{}, fmt.Errorf("not a jj checkout: %w", err)
	}
	common, err := gitOut(root, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return Checkout{root, common, filepath.Dir(common)}, err
}

// Lock holds the repository's publish lock until the returned function runs. Without wait it
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

// Worktree is the publish workspace's path.
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

// ResolveBranch is the branch a publish targets: the given one, or one named after the title.
func ResolveBranch(root string, o Options) (string, error) {
	if o.Branch != "" {
		return o.Branch, checkRefName(o.Branch)
	}
	if o.Title == "" {
		return "", errors.New("a title or --branch is needed to publish")
	}
	branch := BranchFor(o.Title)
	return branch, checkRefName(branch)
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
	if err := c.fetch(); err != nil {
		return Result{}, err
	}
	if err := c.sync(o.Base); err != nil {
		fmt.Println(err)
	}
	slug := git.RemoteSlug(root)
	if slug == "" {
		return Result{}, errors.New("origin is not a GitHub repository")
	}

	// A PR number, or the branch standing for a fork's PR, publishes onto that PR's branch.
	var pr pull
	fork, pushed := false, false
	head := "" // the branch's last known head on GitHub
	if o.PR == 0 {
		o.PR = forkNumber(o.Branch)
	}
	if o.PR != 0 {
		if err := ghJSON(root, &pr, "pr", "view", strconv.Itoa(o.PR), "--repo", slug, "--json",
			"state,headRefName,baseRefName,isCrossRepository,maintainerCanModify,headRepository,headRepositoryOwner,autoMergeRequest"); err != nil {
			return Result{}, err
		}
		if pr.State != "OPEN" {
			return Result{}, fmt.Errorf("#%d is not open", o.PR)
		}
		o.Base, o.Branch = pr.BaseRefName, pr.HeadRefName
		if pr.IsCrossRepository {
			if !pr.MaintainerCanModify {
				return Result{}, fmt.Errorf("#%d does not allow edits by maintainers", o.PR)
			}
			o.Branch = ForkBranch(o.PR)
			// The ref the head is fetched into marks an adopted PR, which only a push makes it.
			ref := pullRefs + strconv.Itoa(o.PR)
			if !hasRef(root, ref) {
				defer func() {
					if !pushed {
						_ = gitRun(root, nil, "update-ref", "-d", ref)
					}
				}()
			}
			if err := gitRun(root, nil, "fetch", "-q", "origin", fmt.Sprintf("+refs/pull/%d/head:%s", o.PR, ref)); err != nil {
				return Result{}, err
			}
			head = remote(strconv.Itoa(o.PR), "pull")
			fork = true
		}
	}
	trunk := trunkOf(o.Base)

	if o.Title != "" && o.Paths == nil {
		if o.Paths, err = files(root, "@"); err != nil {
			return Result{}, err
		}
	}
	branch, err := ResolveBranch(root, o)
	if err != nil {
		return Result{}, err
	}
	fmt.Println("Publishing branch:", branch)
	if head == "" && exists(root, remote(branch, "origin")) {
		head = remote(branch, "origin")
		if !exists(root, bookmark(branch)) {
			if err := jjRun(root, "bookmark", "track", branch, "--remote", "origin"); err != nil {
				return Result{}, err
			}
		}
	}
	// A bookmark is this branch only when a publish recorded it; another one of the same name, such
	// as an old local branch, is replaced.
	local := exists(root, bookmark(branch)) && (hasRef(root, publishedRefs+branch) || head != "")
	// The checkout holds someone else's PR as it should merge: its files go out as they are here.
	if o.PR != 0 && head != "" && len(o.Paths) > 0 {
		theirs, err := files(root, fmt.Sprintf("::%s ~ ::%s", head, trunk))
		if err != nil {
			return Result{}, err
		}
		for _, f := range theirs {
			if !slices.Contains(o.Paths, f) {
				o.Paths = append(o.Paths, f)
			}
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
	if prTitle == "" && local {
		prTitle, _ = jjOut(root, "log", "--no-graph", "-r", "roots(::"+bookmark(branch)+" ~ ::"+trunk+")", "-T", `description.first_line() ++ "\n"`)
		prTitle, _, _ = strings.Cut(prTitle, "\n")
	}
	if o.PR == 0 {
		_ = gitRun(root, nil, "config", "--local", "branch."+branch+".rufin-pr-title", prTitle)
	}
	if o.BodySet {
		_ = gitRun(root, nil, "config", "--local", "branch."+branch+".rufin-pr-body", o.Body)
	} else if body, err := gitOut(root, nil, "config", "--local", "--get", "branch."+branch+".rufin-pr-body"); err == nil {
		o.Body, o.BodySet = body, true
	}

	// The change to publish: a new one holding the paths' edits, or the branch as it stands.
	base, err := c.base()
	if err != nil {
		return Result{}, err
	}
	var change string
	created := len(o.Paths) > 0 || o.Run != ""
	switch {
	case created:
		if o.Title == "" {
			return Result{}, errors.New("a title is needed to commit the requested changes")
		}
		parents := []string{base}
		switch {
		case o.PR != 0:
			parents = []string{head}
			if !exists(root, trunk+" & ::"+head) {
				parents = append(parents, trunk)
			}
		case local:
			parents = []string{bookmark(branch)}
		case head != "":
			parents = []string{head}
		}
		// Paths whose edits build on another of this checkout's open changes go on top of it.
		if o.PR == 0 && len(o.Paths) > 0 {
			below, err := ids(root, fmt.Sprintf("heads(((%s..@) ~ @ ~ ::(%s)) & files(%s))", trunk,
				strings.Join(parents, " | "), strings.Join(fileset(o.Paths), " | ")))
			if err != nil {
				return Result{}, err
			}
			if len(below) > 0 && parents[0] == base {
				parents = below
			} else {
				parents = append(parents, below...)
			}
		}
		if change, err = newChange(root, o.Title, parents...); err != nil {
			return Result{}, err
		}
	case local:
		change, err = jjOut(root, "log", "--no-graph", "-r", bookmark(branch), "-T", "change_id")
	case head != "" && o.PR != 0:
		// Someone else's PR as it is: merge the base into it when it lacks the base's commits.
		change = head
		if !exists(root, trunk+" & ::"+head) {
			change, err = newChange(root, fmt.Sprintf("Merge branch '%s' into %s", o.Base, pr.HeadRefName), head, trunk)
			if err == nil && exists(root, change+" & conflicts()") {
				_ = jjRun(root, "abandon", change)
				return Result{}, fmt.Errorf("could not merge origin/%s into %s; merge it by hand and publish again", o.Base, branch)
			}
		}
	default:
		return Result{}, fmt.Errorf("branch %s does not exist; give paths to create it", branch)
	}
	if err != nil {
		return Result{}, err
	}

	// The paths' edits move into the change, which becomes a parent of the working copy, so the
	// files here stay as they are.
	before, _ := commitID(root, "@")
	inCheckout := false
	if created && len(o.Paths) > 0 {
		if o.PR != 0 {
			// Someone else's PR goes out with its files exactly as they are here.
			if err := jjRun(root, append([]string{"restore", "--from", "@", "--into", change}, fileset(o.Paths)...)...); err != nil {
				_ = jjRun(root, "abandon", change)
				return Result{}, err
			}
			err = c.addParent(change)
		} else if err = c.addParent(change); err == nil {
			err = jjRun(root, append([]string{"squash", "--from", "@", "--into", change}, fileset(o.Paths)...)...)
		}
		if err == nil && exists(root, change+" & conflicts()") {
			err = fmt.Errorf("the requested paths do not apply onto %s; sync them and retry", branch)
		}
		if err != nil {
			_ = jjRun(root, "abandon", "--restore-descendants", change)
			return Result{}, err
		}
		inCheckout = true
		if exists(root, change+" & empty()") && o.Run == "" {
			_ = jjRun(root, "abandon", change)
			if !local {
				return Result{}, errors.New("no changes found for the requested paths")
			}
			// A publish that stopped after moving these edits: the branch already holds them.
			created = false
			if change, err = jjOut(root, "log", "--no-graph", "-r", bookmark(branch), "-T", "change_id"); err != nil {
				return Result{}, err
			}
		}
	}
	if !created {
		inCheckout = exists(root, change+" & ::@")
	}
	// The branch names the change from here on, so a publish that stops is resumed by running it
	// again.
	if err := jjRun(root, "bookmark", "set", branch, "-r", change, "--allow-backwards"); err != nil {
		return Result{}, err
	}
	c.record(branch)

	work, err := c.enterPublish(change)
	if err != nil {
		return Result{}, err
	}
	if o.Run != "" {
		if err := shell(work, nil, o.Run); err != nil {
			return Result{}, err
		}
	}
	if !o.Instant && o.Generate != "" {
		if err := shell(work, nil, o.Generate); err != nil {
			return Result{}, err
		}
	}
	// What the commands changed joins the change; one that was already published gets a commit of
	// its own.
	if _, err := jjOut(work, "util", "snapshot"); err != nil {
		return Result{}, err
	}
	if !exists(work, "@ & empty()") {
		if created {
			if err = jjRun(root, "squash", "--from", "publish@", "--into", change); err == nil {
				_, err = c.enterPublish(change)
			}
		} else if err = jjRun(work, "describe", "-m", "generated: refresh formatting and generated files"); err == nil {
			// The publish workspace moves off the new commit before the checkout builds on it.
			if change, err = jjOut(work, "log", "--no-graph", "-r", "@", "-T", "change_id"); err == nil {
				if _, err = c.enterPublish(change); err == nil && inCheckout {
					err = c.addParent(change)
				}
			}
		}
		if err != nil {
			return Result{}, err
		}
		if inCheckout {
			c.keepEdits(before)
		}
	}
	if !exists(root, fmt.Sprintf("::%s ~ ::%s", change, trunk)) {
		return Result{}, fmt.Errorf("no commits to publish on %s", branch)
	}
	if err := jjRun(root, "bookmark", "set", branch, "-r", change, "--allow-backwards"); err != nil {
		return Result{}, err
	}
	c.record(branch)
	result := Result{Branch: branch, Worktree: work}
	if o.PrepareOnly {
		fmt.Println("Prepared branch", branch, "in", work)
		return result, nil
	}

	// Work on top of other open branches waits for them to merge; the checks run now.
	below, err := names(root, fmt.Sprintf("(::%s ~ ::%s ~ %s) & bookmarks()", change, trunk, change))
	if err != nil {
		return result, err
	}
	if len(below) > 0 {
		checkBase := "origin/" + o.Base
		if heads, _ := commitID(root, fmt.Sprintf("heads(::%s & bookmarks() ~ %s)", change, change)); !strings.Contains(heads, "\n") && heads != "" {
			checkBase = heads
		}
		if err := runChecks(work, checkBase, branch, "", o); err != nil {
			return result, err
		}
		return queued(branch, below), nil
	}

	old := ""
	if head != "" {
		if !exists(root, head+" & ::"+change) {
			return result, fmt.Errorf("%s has commits this branch lacks; merge them and publish again", branch)
		}
		old, _ = commitID(root, head)
	}
	if err := runChecks(work, "origin/"+o.Base, branch, old, o); err != nil {
		return result, err
	}
	if !fork {
		err = jjRun(root, "git", "push", "--remote", "origin", "-b", "exact:"+branch)
	} else {
		// The fork is reached the way origin is, under its own owner and name.
		if err = jjRun(root, "sign", "-r", fmt.Sprintf("::%s ~ ::%s", change, head)); err == nil {
			var sha string
			sha, err = commitID(root, change)
			url, _ := gitOut(root, nil, "remote", "get-url", "--push", "origin")
			url = strings.Replace(url, slug, pr.HeadRepositoryOwner.Login+"/"+pr.HeadRepository.Name, 1)
			if err == nil {
				err = gitRun(root, nil, "push", "--no-verify", url, sha+":refs/heads/"+pr.HeadRefName)
			}
		}
	}
	if err != nil {
		return result, err
	}
	pushed = true
	c.record(branch)
	if o.PR != 0 {
		sha, _ := commitID(root, change)
		_ = gitRun(root, nil, "update-ref", pullRefs+strconv.Itoa(o.PR), sha)
	}

	number := o.PR
	if number == 0 {
		number = openPR(root, slug, branch)
	}
	if number == 0 {
		if err := ghRun(root, "pr", "create", "--repo", slug, "--base", o.Base, "--head", branch, "--title", prTitle, "--body", o.Body); err != nil {
			return result, err
		}
		number = openPR(root, slug, branch)
	} else if o.BodySet && o.PR == 0 {
		_ = ghRun(root, "pr", "edit", strconv.Itoa(number), "--repo", slug, "--body", o.Body)
	}
	if number == 0 {
		return result, fmt.Errorf("could not find the PR for %s", branch)
	}
	_ = gitRun(root, nil, "config", "--local", "--unset-all", "branch."+branch+".rufin-pr-title")
	_ = gitRun(root, nil, "config", "--local", "--unset-all", "branch."+branch+".rufin-pr-body")
	result.Number = number
	result.URL, _ = ghOut(root, "pr", "view", strconv.Itoa(number), "--repo", slug, "--json", "url", "--jq", ".url")
	// Someone else's PR keeps its title and body; the first title given here names its squash
	// commit instead.
	if o.AutoMerge && (o.PR == 0 || pr.AutoMergeRequest == nil) {
		merge := []string{"pr", "merge", strconv.Itoa(number), "--repo", slug, "--auto", "--squash"}
		switch {
		case o.MergeCommit:
			merge[len(merge)-1] = "--merge"
		case o.PR != 0 && o.Title != "":
			merge = append(merge, "--subject", fmt.Sprintf("%s (#%d)", o.Title, number))
		}
		if !fork {
			merge = append(merge, "--delete-branch")
		}
		if err := ghRun(root, merge...); err != nil {
			fmt.Println("Auto-merge not enabled:", err)
		}
	}
	fmt.Printf("Published PR #%d: %s\n", number, result.URL)
	return result, nil
}

func queued(branch string, behind []string) Result {
	fmt.Printf("Queued %s behind %s: git-gui-daemon publishes it once they merge.\n", branch, strings.Join(behind, ", "))
	return Result{Branch: branch, Queued: behind}
}

// fetch brings the remote branches up to date. A branch deleted on GitHub, as a merge deletes its
// head, loses its bookmark here too.
func (c Checkout) fetch() error {
	_, err := jjOut(c.Root, "git", "fetch", "--remote", "origin")
	return err
}

// base is the base commit the working copy stands on: its first parent.
func (c Checkout) base() (string, error) {
	parents, err := jjOut(c.Root, "log", "--no-graph", "-r", "@", "-T", `parents.map(|p| p.change_id()).join(" ")`)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(parents, " ")
	return first, nil
}

// addParent makes change a parent of the working copy, after its base and in place of any parent
// it builds on. An empty change, or one holding the working copy's files as they are, leaves the
// files here alone.
func (c Checkout) addParent(change string) error {
	base, err := c.base()
	if err != nil {
		return err
	}
	others, err := ids(c.Root, fmt.Sprintf("heads((parents(@) ~ %s) | %s)", base, change))
	if err != nil {
		return err
	}
	args := []string{"rebase", "-r", "@", "-o", base}
	for _, other := range others {
		args = append(args, "-o", other)
	}
	return jjRun(c.Root, args...)
}

// keepEdits gives back the working copy's own version of every file that formatting or
// generation changed underneath an edit made here: that edit stays, and the next publish of the
// file carries it.
func (c Checkout) keepEdits(before string) {
	if !exists(c.Root, "@ & conflicts()") {
		return
	}
	listed, _ := jjOut(c.Root, "resolve", "--list", "-r", "@")
	var paths []string
	for _, line := range strings.Split(listed, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			paths = append(paths, fields[0])
		}
	}
	if len(paths) > 0 {
		_ = jjRun(c.Root, append([]string{"restore", "--from", before, "--into", "@"}, fileset(paths)...)...)
	}
}

// enterPublish puts the publish workspace on an empty change on top of change.
func (c Checkout) enterPublish(change string) (string, error) {
	path := c.Worktree()
	if _, err := os.Stat(filepath.Join(path, ".jj")); err != nil {
		return path, AddWorkspace(c.Main, "publish", path, change, false)
	}
	// Rewriting the change from the checkout leaves this workspace stale; it holds no edits.
	_, _ = jjOut(path, "workspace", "update-stale")
	return path, jjRun(path, "new", change)
}

// record keeps the branch's head for sync: once the branch's bookmark is gone, sync takes its
// commits out of the checkout.
func (c Checkout) record(branch string) {
	if sha, err := commitID(c.Root, bookmark(branch)); err == nil {
		_ = gitRun(c.Root, nil, "update-ref", publishedRefs+branch, sha)
	}
}

// names lists the local bookmarks on a revset's commits.
func names(dir, revset string) ([]string, error) {
	out, err := jjOut(dir, "log", "--no-graph", "-r", revset, "-T", `local_bookmarks.map(|b| b.name()).join("\n") ++ "\n"`)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// runChecks runs the check command on the commits since base, or with Full the pre-push hook,
// the full gate. old is the branch's head on GitHub, if it has one.
func runChecks(work, base, branch, old string, o Options) error {
	var check *exec.Cmd
	switch {
	case o.Instant:
		return nil
	case o.Full:
		remote, _ := gitOut(work, nil, "remote", "get-url", "origin")
		head, _ := gitOut(work, nil, "rev-parse", "HEAD")
		if old == "" {
			old = strings.Repeat("0", len(head))
		}
		check = Capped(exec.Command("git", "hook", "run", "--ignore-missing", "pre-push", "--", "origin", remote), work, nil)
		check.Stdin = strings.NewReader(fmt.Sprintf("refs/heads/%s %s refs/heads/%s %s\n", branch, head, branch, old))
	case o.Check != "":
		check = Capped(exec.Command("sh", "-c", o.Check+` "$0"`, base), work, nil)
	default:
		return nil
	}
	if err := check.Run(); err != nil {
		return fmt.Errorf("checks failed: %w", err)
	}
	return nil
}

func openPR(root, slug, branch string) int {
	out, _ := ghOut(root, "pr", "list", "--repo", slug, "--head", branch, "--state", "open", "--json", "number", "--jq", ".[0].number // empty")
	n, _ := strconv.Atoi(out)
	return n
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

func hasRef(root, ref string) bool {
	_, err := gitOut(root, nil, "rev-parse", "-q", "--verify", ref)
	return err == nil
}

func gitOut(dir string, env []string, args ...string) (string, error) {
	out, err := gitCmd(dir, env, args...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}

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
