package publish

import (
	"fmt"
	"strings"
)

// Branch is one of the checkout's open branches: a parent of its working copy until it merges.
type Branch struct {
	Name   string
	Behind []string // the open branches it builds on
	Files  []string // the files its own commits change
	Pushed bool
}

// Queued reports a branch that waits for the branches below it to merge: git-gui-daemon publishes
// it once they do.
func (b Branch) Queued() bool { return !b.Pushed && len(b.Behind) > 0 }

// Branches lists the checkout's open branches.
func Branches(root, base string) []Branch {
	trunk := trunkOf(base)
	listed, _ := names(root, fmt.Sprintf("(%s..@) & bookmarks()", trunk))
	var out []Branch
	for _, name := range listed {
		behind, _ := names(root, fmt.Sprintf("(::%s ~ ::%s ~ %s) & bookmarks()", bookmark(name), trunk, bookmark(name)))
		changed, _ := files(root, own(name, behind, trunk))
		pushed := forkNumber(name) != 0 || exists(root, remote(name, "origin"))
		out = append(out, Branch{Name: name, Behind: behind, Files: changed, Pushed: pushed})
	}
	return out
}

// Queue lists the checkout's queued publishes by branch.
func Queue(root, base string) map[string]Branch {
	queue := map[string]Branch{}
	for _, b := range Branches(root, base) {
		if b.Queued() {
			queue[b.Name] = b
		}
	}
	return queue
}

// own is the revset of a branch's commits above the branches it builds on.
func own(branch string, behind []string, trunk string) string {
	below := []string{"none()"}
	for _, b := range behind {
		below = append(below, bookmark(b))
	}
	return fmt.Sprintf("(::%s ~ ::%s) ~ ::(%s)", bookmark(branch), trunk, strings.Join(below, " | "))
}

// Dequeue drops a queued publish; its edits stay in the checkout, uncommitted.
func Dequeue(root, base, branch string) error {
	trunk := trunkOf(base)
	behind, _ := names(root, fmt.Sprintf("(::%s ~ ::%s ~ %s) & bookmarks()", bookmark(branch), trunk, bookmark(branch)))
	if err := jjRun(root, "abandon", "--restore-descendants", own(branch, behind, trunk)); err != nil {
		return err
	}
	_ = gitRun(root, nil, "update-ref", "-d", publishedRefs+branch)
	return nil
}
