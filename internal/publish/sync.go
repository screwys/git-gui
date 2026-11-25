package publish

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/git-gui/internal/git"
)

// publishedRefs keep each branch's head as publish last left it. Once the branch's bookmark is
// gone, as when its merge deletes it on GitHub, sync takes the branch's commits out of the
// checkout.
const publishedRefs = "refs/git-gui/published/"

// Sync brings the checkout onto the remote base branch, carrying its edits along. Without wait it
// does nothing while a publish runs.
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
	if err := c.fetch(); err != nil {
		return false, err
	}
	return true, c.sync(base)
}

// sync moves the checkout onto the base. Branches that left go first: a merged branch's commits
// are already on the base, so what was built on them moves onto it directly; a closed branch's
// edits stay here uncommitted. Nothing changes when the result would conflict.
func (c Checkout) sync(base string) error {
	root, trunk := c.Root, trunkOf(base)
	// Edits so far are snapshotted on their own, so undoing this sync's operations keeps them.
	if _, err := jjOut(root, "util", "snapshot"); err != nil {
		return err
	}
	start, err := jjOut(root, "op", "log", "--no-graph", "--limit", "1", "-T", "id")
	if err != nil {
		return err
	}
	tag := fmt.Sprintf("git-gui-sync-%d-%d", os.Getpid(), time.Now().UnixNano())
	left, err := c.dropLeft(trunk, tag)
	// The working copy moves onto the base; open branches stay where they were pushed.
	if first, ferr := c.base(); err == nil && ferr == nil && !exists(root, first+" & "+trunk) {
		var others []string
		if others, err = ids(root, "parents(@) ~ ::"+trunk); err == nil {
			args := []string{"rebase", "-r", "@", "-o", trunk}
			for _, other := range others {
				args = append(args, "-o", other)
			}
			err = c.mutate(tag, args...)
		}
	}
	if err == nil {
		if conflicted, _ := ids(root, fmt.Sprintf("(%s..@) & conflicts()", trunk)); len(conflicted) > 0 {
			listed, _ := jjOut(root, "resolve", "--list", "-r", conflicted[len(conflicted)-1])
			var paths []string
			for _, line := range strings.Split(listed, "\n") {
				if fields := strings.Fields(line); len(fields) > 0 {
					paths = append(paths, fields[0])
				}
			}
			err = fmt.Errorf("local edits conflict with origin/%s: %s; %s not synced", base, strings.Join(paths, ", "), base)
		}
	}
	if err != nil {
		c.revert(start, tag)
		return err
	}
	for _, branch := range left {
		_ = gitRun(root, nil, "update-ref", "-d", publishedRefs+branch)
	}
	if exists(root, bookmark(base)) {
		_, _ = jjOut(root, "bookmark", "set", base, "-r", trunk, "--allow-backwards")
	}
	c.attachHead(base)
	return nil
}

// attachHead puts the shared checkout's Git HEAD back on the base branch, which jj detaches when
// the working copy moves onto a new base: Claude Code takes a session's diff base from the
// checkout's branch and shows no changes without one. A workspace leaves it, since the branch is
// the shared checkout's.
func (c Checkout) attachHead(base string) {
	if c.Root != c.Main {
		return
	}
	head, _ := gitOut(c.Root, nil, "rev-parse", "-q", "--verify", "HEAD")
	branch, _ := gitOut(c.Root, nil, "rev-parse", "-q", "--verify", "refs/heads/"+base)
	if head != "" && head == branch {
		_ = gitRun(c.Root, nil, "symbolic-ref", "HEAD", "refs/heads/"+base)
	}
}

// mutate runs a jj command tagged as this sync's, so a failed sync reverts exactly its own
// operations and none of other workspaces' meanwhile.
func (c Checkout) mutate(tag string, args ...string) error {
	return jjRun(c.Root, append([]string{"--config", "operation.username=" + tag}, args...)...)
}

func (c Checkout) revert(start, tag string) {
	listed, _ := jjOut(c.Root, "op", "log", "--no-graph", "--limit", "200", "-T",
		`id ++ " " ++ if(user.starts_with(`+quote(tag+"@")+`) && !description.starts_with("snapshot working copy"), "ours", "other") ++ "\n"`)
	for _, line := range strings.Split(listed, "\n") {
		id, whose, _ := strings.Cut(line, " ")
		if id == start {
			return
		}
		if whose == "ours" {
			_ = jjRun(c.Root, "op", "revert", id)
		}
	}
}

// dropLeft takes this checkout's branches whose bookmark is gone out of it and returns them.
func (c Checkout) dropLeft(trunk, tag string) ([]string, error) {
	root := c.Root
	listed, _ := gitOut(root, nil, "for-each-ref", "--format=%(objectname) %(refname)", publishedRefs)
	slug := git.RemoteSlug(root)
	var left []string
	for _, line := range strings.Split(listed, "\n") {
		sha, ref, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		branch := strings.TrimPrefix(ref, publishedRefs)
		if exists(root, bookmark(branch)) {
			continue
		}
		// Its own commits: those not on the base and not below another branch.
		chain := fmt.Sprintf("(::%s ~ ::%s) ~ ::(bookmarks() & ::%s)", sha, trunk, sha)
		switch {
		case !exists(root, "present("+sha+")") || !exists(root, chain):
		case exists(root, sha+" & ::@"):
			var err error
			if c.merged(slug, branch) {
				err = c.dropMerged(chain, trunk, tag)
			} else {
				err = c.mutate(tag, "abandon", "--restore-descendants", chain)
			}
			if err != nil {
				return left, err
			}
		case exists(root, sha+" & ::("+c.otherCopies()+")"):
			continue // another checkout's branch: that checkout's sync takes it out
		default:
			if err := c.mutate(tag, "abandon", chain); err != nil {
				return left, err
			}
		}
		left = append(left, branch)
	}
	return left, nil
}

// dropMerged moves what this checkout built on a merged branch onto the base, where the branch's
// edits now are, and abandons the branch's commits.
func (c Checkout) dropMerged(chain, trunk, tag string) error {
	for range 50 {
		kids, err := ids(c.Root, fmt.Sprintf("(children(%s) ~ (%s)) & ::@", chain, chain))
		if err != nil || len(kids) == 0 {
			break
		}
		others, err := ids(c.Root, fmt.Sprintf("heads(parents(%s) ~ (%s) ~ ::%s)", kids[0], chain, trunk))
		if err != nil {
			return err
		}
		args := []string{"rebase", "-s", kids[0], "-o", trunk}
		for _, other := range others {
			args = append(args, "-o", other)
		}
		if err := c.mutate(tag, args...); err != nil {
			return err
		}
	}
	return c.mutate(tag, "abandon", chain)
}

// merged reports whether the branch's PR merged.
func (c Checkout) merged(slug, branch string) bool {
	if n := forkNumber(branch); n != 0 {
		state, _ := ghOut(c.Root, "pr", "view", strconv.Itoa(n), "--repo", slug, "--json", "state", "--jq", ".state")
		return state == "MERGED"
	}
	count, _ := ghOut(c.Root, "pr", "list", "--repo", slug, "--head", branch, "--state", "merged", "--json", "number", "--jq", "length")
	return count != "" && count != "0"
}

// otherCopies is the revset of the other session workspaces' working copies.
func (c Checkout) otherCopies() string {
	own, _ := jjOut(c.Root, "log", "--no-graph", "-r", "@", "-T", "commit_id")
	listed, _ := jjOut(c.Root, "workspace", "list", "-T", `name ++ " " ++ target.commit_id() ++ "\n"`)
	revsets := []string{"none()"}
	for _, line := range strings.Split(listed, "\n") {
		name, commit, ok := strings.Cut(line, " ")
		if ok && name != "publish" && commit != own {
			revsets = append(revsets, commit)
		}
	}
	return strings.Join(revsets, " | ")
}

// Snapshot records the checkout's edits in jj's operation log, so an edit lost to a stash, reset
// or checkout can be restored with "jj op restore".
func Snapshot(root string) { _, _ = jjOut(root, "util", "snapshot") }

// Forget drops a branch whose PR merged or closed, so the next sync takes its commits out of the
// checkout; for someone else's PR also its head here. GitHub's branch stays as it is.
func Forget(root, branch string, number int) {
	if exists(root, bookmark(branch)) {
		_, _ = jjOut(root, "bookmark", "delete", "exact:"+branch)
	}
	if number != 0 && hasRef(root, pullRefs+strconv.Itoa(number)) {
		_ = gitRun(root, nil, "update-ref", "-d", pullRefs+strconv.Itoa(number))
	}
}
