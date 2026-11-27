package publish

import (
	"errors"
	"fmt"
	"strings"
)

// A parked branch is a branch of finished commits taken out of the checkout: the files here lose
// its edits, so builds, other sessions' publishes and the diff leave it alone until it is unparked
// or published. Its bookmark and published ref keep it; sync leaves it where it is.

// park takes the branch's change out of the working copy's parents.
func (c Checkout) park(branch, change string) error {
	if !exists(c.Root, change+" & ::@") {
		return nil
	}
	if above, _ := names(c.Root, fmt.Sprintf("(%s:: ~ %s ~ @) & ::@", change, change)); len(above) > 0 {
		return fmt.Errorf("%s builds on %s; park or publish it first", strings.Join(above, ", "), branch)
	}
	if err := c.dropParent(change); err != nil {
		return err
	}
	if exists(c.Root, "@ & conflicts()") {
		paths := conflictPaths(c.Root, "@")
		_ = c.addParent(change)
		return fmt.Errorf("edits here build on %s's in %s; park them with it (git-gui publish --park --branch %s PATH...)", branch, strings.Join(paths, ", "), branch)
	}
	return nil
}

// dropParent rebases the working copy onto its parents without the change and what builds on it.
func (c Checkout) dropParent(change string) error {
	base, err := c.base()
	if err != nil {
		return err
	}
	others, err := ids(c.Root, fmt.Sprintf("heads(::@ ~ ::%s ~ @ ~ %s::)", base, change))
	if err != nil {
		return err
	}
	args := []string{"rebase", "-r", "@", "-o", base}
	for _, other := range others {
		args = append(args, "-o", other)
	}
	_, err = jjOut(c.Root, args...)
	return err
}

// Unpark brings a parked branch's edits back into the checkout in dir, so work on it continues
// here. Nothing changes when they conflict with the files here.
func Unpark(dir, branch string) error {
	c, err := Open(dir)
	if err != nil {
		return err
	}
	unlock, _ := c.Lock(true)
	defer unlock()
	if !exists(c.Root, bookmark(branch)) {
		return fmt.Errorf("no branch %s", branch)
	}
	if exists(c.Root, bookmark(branch)+" & ::@") {
		return errors.New(branch + " is already in this checkout")
	}
	change, err := jjOut(c.Root, "log", "--no-graph", "-r", bookmark(branch), "-T", "change_id")
	if err != nil {
		return err
	}
	if err := c.addParent(change); err != nil {
		return err
	}
	if exists(c.Root, "@ & conflicts()") {
		paths := conflictPaths(c.Root, "@")
		_ = c.dropParent(change)
		return fmt.Errorf("%s conflicts with the files here in %s; nothing changed", branch, strings.Join(paths, ", "))
	}
	return nil
}

// Parked lists the checkout's parked branches: recorded by a publish, never pushed and in no
// checkout.
func Parked(root, base string) []Branch {
	c, err := Open(root)
	if err != nil {
		return nil
	}
	// Only branches a publish recorded can be parked, so the rest of the bookmarks are skipped.
	refs, _ := gitOut(root, nil, "for-each-ref", "--format=%(refname)", publishedRefs)
	recorded := []string{"none()"}
	for _, ref := range strings.Fields(refs) {
		recorded = append(recorded, bookmark(strings.TrimPrefix(ref, publishedRefs)))
	}
	trunk := trunkOf(base)
	listed, _ := names(root, fmt.Sprintf("(%s) ~ ::%s ~ ::(@ | %s)", strings.Join(recorded, " | "), trunk, c.otherCopies()))
	var out []Branch
	for _, name := range listed {
		if b := branchOf(root, name, trunk); !b.Pushed {
			out = append(out, b)
		}
	}
	return out
}

// conflictPaths lists the files conflicted in revision.
func conflictPaths(root, revision string) []string {
	listed, _ := jjOut(root, "resolve", "--list", "-r", revision)
	var paths []string
	for _, line := range strings.Split(listed, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			paths = append(paths, fields[0])
		}
	}
	return paths
}
