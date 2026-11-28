package publish

import (
	"errors"
	"fmt"
	"strings"
)

// Parking stashes uncommitted edits: they move into a change of their own beside the working copy,
// under the bookmark park/NAME, and the files here lose them. Unpark puts them back as uncommitted
// edits on top of whatever the files hold by then.
const parkPrefix = "park/"

// Park takes the paths' uncommitted edits, or every uncommitted edit without paths, out of the
// checkout in dir and keeps them as name.
func Park(dir, name string, paths []string) error {
	c, err := Open(dir)
	if err != nil {
		return err
	}
	unlock, _ := c.Lock(true)
	defer unlock()
	root, stash := c.Root, bookmark(parkPrefix+name)
	if exists(root, stash) {
		return fmt.Errorf("%s is already parked; unpark it or pick another name", name)
	}
	if len(paths) == 0 {
		if paths, err = files(root, "@"); err != nil {
			return err
		}
	}
	if len(paths) == 0 {
		return errors.New("no uncommitted edits to park")
	}
	parents, err := ids(root, "parents(@)")
	if err != nil {
		return err
	}
	change, err := newChange(root, "park: "+name, parents...)
	if err != nil {
		return err
	}
	_, err = jjOut(root, append([]string{"squash", "--from", "@", "--into", change}, fileset(paths)...)...)
	if err == nil && exists(root, change+" & empty()") {
		err = errors.New("no uncommitted edits in those paths")
	}
	if err == nil {
		_, err = jjOut(root, "bookmark", "set", parkPrefix+name, "-r", change)
	}
	if err != nil {
		_, _ = jjOut(root, "squash", "-u", "--from", change, "--into", "@")
		return err
	}
	return nil
}

// Unpark puts the edits parked as name back into the checkout in dir, uncommitted. Nothing changes
// when they conflict with the files here.
func Unpark(dir, name string) error {
	c, err := Open(dir)
	if err != nil {
		return err
	}
	unlock, _ := c.Lock(true)
	defer unlock()
	root, stash := c.Root, bookmark(parkPrefix+name)
	if !exists(root, stash) {
		return fmt.Errorf("nothing is parked as %s", name)
	}
	parents, err := ids(root, "parents("+stash+")")
	if err != nil {
		return err
	}
	// On top of the files here, the parked change holds what unparking leaves in them.
	if _, err := jjOut(root, "rebase", "-r", stash, "-o", "@"); err != nil {
		return err
	}
	if exists(root, stash+" & conflicts()") {
		paths := conflictPaths(root, stash)
		back := []string{"rebase", "-r", stash}
		for _, p := range parents {
			back = append(back, "-o", p)
		}
		_, _ = jjOut(root, back...)
		return fmt.Errorf("%s conflicts with the files here in %s; nothing changed", name, strings.Join(paths, ", "))
	}
	change, err := jjOut(root, "log", "--no-graph", "-r", stash, "-T", "change_id")
	if err != nil {
		return err
	}
	_, _ = jjOut(root, "bookmark", "delete", "exact:"+parkPrefix+name)
	_, err = jjOut(root, "squash", "-u", "--from", change, "--into", "@")
	return err
}

// Parked lists the parked edits by name, with their files.
func Parked(root string) []Branch {
	listed, _ := names(root, "bookmarks(glob:"+quote(parkPrefix+"*")+")")
	var out []Branch
	for _, b := range listed {
		if name, ok := strings.CutPrefix(b, parkPrefix); ok {
			changed, _ := files(root, bookmark(b))
			out = append(out, Branch{Name: name, Files: changed})
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
