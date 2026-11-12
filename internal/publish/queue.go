package publish

import (
	"fmt"
	"slices"
	"strings"
)

// A queued publish is a commit at refs/git-gui/queue/<branch>. Its parent, the base, merges the
// checkout's HEAD with the branches whose edits it builds on, named in the base's message, so
// the commit's own diff is exactly the queued work.
const (
	queueRefs     = "refs/git-gui/queue/"
	behindTrailer = "Queued-behind: "
)

// Queued is one queued publish.
type Queued struct {
	Commit string
	Behind []string // the branches it waits for
	Files  []string // the files its own diff changes
}

// Queue lists the checkout's queued publishes by branch.
func Queue(root string) map[string]Queued {
	queue := map[string]Queued{}
	listed, _ := gitOut(root, nil, "for-each-ref", "--format=%(objectname) %(refname)", queueRefs)
	for _, line := range strings.Split(listed, "\n") {
		commit, ref, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		q := Queued{Commit: commit}
		message, _ := gitOut(root, nil, "log", "-1", "--format=%B", commit+"^")
		for _, line := range strings.Split(message, "\n") {
			if branch, ok := strings.CutPrefix(line, behindTrailer); ok {
				q.Behind = append(q.Behind, branch)
			}
		}
		files, _ := gitOut(root, nil, "diff", "--no-renames", "--name-only", "-z", commit+"^", commit)
		q.Files = splitZ(files)
		queue[strings.TrimPrefix(ref, queueRefs)] = q
	}
	return queue
}

// Dequeue drops a queued publish; its edits stay in the checkout.
func Dequeue(root, branch string) error {
	return gitRun(root, nil, "update-ref", "-d", queueRefs+branch)
}

// carrier is work that is published or queued but not yet on the base branch.
type carrier struct {
	branch, commit string // commit: the queue commit, or the open PR's remote branch
	number         int    // the open PR, 0 when only queued
	queued         bool
	behind, files  []string
}

func (c carrier) String() string {
	if c.number != 0 {
		return fmt.Sprintf("#%d", c.number)
	}
	return c.branch + " (queued)"
}

// carriers lists the user's open PRs and the queued publishes other than branch's own.
func carriers(root, slug, base, branch string, queue map[string]Queued) ([]carrier, error) {
	var open []struct {
		Number int    `json:"number"`
		Branch string `json:"headRefName"`
	}
	if err := ghJSON(root, &open, "pr", "list", "--repo", slug, "--author", "@me", "--state", "open", "--json", "number,headRefName"); err != nil {
		return nil, err
	}
	byBranch := map[string]*carrier{}
	var list []*carrier
	add := func(name string) *carrier {
		if byBranch[name] == nil {
			byBranch[name] = &carrier{branch: name}
			list = append(list, byBranch[name])
		}
		return byBranch[name]
	}
	for _, pr := range open {
		if pr.Branch == branch || !hasRef(root, "refs/remotes/origin/"+pr.Branch) {
			continue
		}
		c := add(pr.Branch)
		c.number, c.commit = pr.Number, "origin/"+pr.Branch
		files, _ := gitOut(root, nil, "diff", "--no-renames", "--name-only", "-z", "origin/"+base+"...origin/"+pr.Branch)
		c.files = splitZ(files)
	}
	for name, q := range queue {
		if name == branch {
			continue
		}
		// A queued follow-up of an open PR includes the PR: its base merged the PR's branch.
		c := add(name)
		c.commit, c.queued, c.behind = q.Commit, true, q.Behind
		for _, f := range q.Files {
			if !slices.Contains(c.files, f) {
				c.files = append(c.files, f)
			}
		}
	}
	out := make([]carrier, len(list))
	for i, c := range list {
		out[i] = *c
	}
	return out, nil
}

// plan decides how the paths of branch go out. Work that builds on edits of other carriers in
// the same files waits for them: behind lists them. Queued publishes waiting on branch, and those
// it does not build on, are subtracted from what it publishes, newest first. Paths that only
// hold an open PR's version are left out.
func plan(root, branch string, paths []string, all []carrier) (behind, subtract []carrier, kept []string) {
	byBranch := map[string]carrier{}
	for _, c := range all {
		byBranch[c.branch] = c
	}
	// Queued publishes that wait on branch, directly or through others, come after it.
	after := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, c := range all {
			if !after[c.branch] && slices.ContainsFunc(c.behind, func(b string) bool { return b == branch || after[b] }) {
				after[c.branch], changed = true, true
			}
		}
	}
	kept = slices.Clone(paths)
	for _, c := range all {
		if after[c.branch] {
			continue
		}
		edited, own := false, []string{}
		for _, path := range paths {
			if !slices.Contains(c.files, path) {
				continue
			}
			if hashFile(root, path) == blob(root, c.commit, path) {
				own = append(own, path)
			} else {
				edited = true
			}
		}
		switch {
		case edited:
			behind = append(behind, c)
		case !c.queued:
			kept = slices.DeleteFunc(kept, func(p string) bool { return slices.Contains(own, p) })
		}
	}
	// What branch builds on stays: the carriers it waits for and everything they wait for.
	before := map[string]bool{}
	var walk func(string)
	walk = func(name string) {
		if before[name] {
			return
		}
		before[name] = true
		for _, b := range byBranch[name].behind {
			walk(b)
		}
	}
	for _, c := range behind {
		walk(c.branch)
	}
	var rest []carrier
	for _, c := range all {
		if c.queued && !before[c.branch] && slices.ContainsFunc(c.files, func(f string) bool { return slices.Contains(kept, f) }) {
			rest = append(rest, c)
		}
	}
	// An entry is subtracted before the entries it waits for.
	for len(rest) > 0 {
		for i, c := range rest {
			waitedOn := slices.ContainsFunc(rest, func(o carrier) bool { return slices.Contains(o.behind, c.branch) })
			if !waitedOn || i == len(rest)-1 {
				subtract = append(subtract, c)
				rest = slices.Delete(rest, i, i+1)
				break
			}
		}
	}
	return behind, subtract, kept
}

// queueBase merges the checkout's HEAD, the branch's own remote and the carriers into one commit
// whose message names the carriers.
func queueBase(root, remote string, behind []carrier) (string, error) {
	current, err := gitOut(root, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	parents := []string{current}
	inputs := []string{}
	if remote != "" {
		inputs = append(inputs, remote)
	}
	for _, c := range behind {
		inputs = append(inputs, c.commit)
	}
	message := "git-gui queue base\n"
	for _, c := range behind {
		message += "\n" + behindTrailer + c.branch
	}
	var tree string
	for _, input := range inputs {
		commit, err := gitOut(root, nil, "rev-parse", "--verify", input+"^{commit}")
		if err != nil {
			return "", err
		}
		if tree, err = gitOut(root, nil, "merge-tree", "--write-tree", "--no-messages", current, commit); err != nil {
			return "", fmt.Errorf("%s does not merge cleanly with %s; publish after it merges", strings.TrimPrefix(input, "origin/"), "HEAD")
		}
		tree, _, _ = strings.Cut(tree, "\n")
		parents = append(parents, commit)
		args := []string{"commit-tree", tree, "-m", message}
		for _, p := range parents {
			args = append(args, "-p", p)
		}
		if current, err = gitOut(root, nil, args...); err != nil {
			return "", err
		}
		parents = []string{current}
	}
	return current, nil
}
