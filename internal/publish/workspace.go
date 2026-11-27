package publish

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// A session workspace is a jj workspace of its own for a session isolated from the shared
// checkout. It lives where Claude Code keeps the worktrees it isolates sessions in, and gets its
// own copies of the build directory and the devel profile: Cargo matches a checkout's crates to
// their builds by path within the checkout and file times, so checkouts sharing one build
// directory use each other's builds, and a schema change in one workspace's build must not
// upgrade the Store the others open. The copies share their data with the originals until
// either side changes it.

// WorkspaceDir holds the session workspaces, relative to the shared checkout.
const WorkspaceDir = ".claude/worktrees"

// profileScript is the project's own script that copies its devel profile to the folder it is
// given, relative to the shared checkout: where that profile lives is the project's knowledge.
const profileScript = ".local/scripts/devel_profile.py"

// ownLocal are the .local folders each workspace keeps for itself; the rest link to the shared
// checkout's.
var ownLocal = []string{"build", "artifacts", "tmp", "profile"}

// WorkspacePath is where the session workspace name lives.
func WorkspacePath(main, name string) string { return filepath.Join(main, WorkspaceDir, name) }

// AddWorkspace makes a workspace at path whose working copy is an empty change on revision. A
// session's workspace also gets a copy of the devel profile.
func AddWorkspace(main, name, path, revision string, profile bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := jjRun(main, "workspace", "add", "--name", name, "-r", revision, path); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(main, "target", "debug"), filepath.Join(path, "target", "debug")); err != nil {
		fmt.Fprintln(os.Stderr, "The build directory was not copied, so the first build starts from scratch:", err)
	}
	if entries, err := os.ReadDir(filepath.Join(main, ".local")); err == nil {
		_ = os.MkdirAll(filepath.Join(path, ".local"), 0o755)
		for _, entry := range entries {
			if !slices.Contains(ownLocal, entry.Name()) {
				_ = os.Symlink(filepath.Join(main, ".local", entry.Name()), filepath.Join(path, ".local", entry.Name()))
			}
		}
	}
	for _, dir := range ownLocal[:3] {
		_ = os.MkdirAll(filepath.Join(path, ".local", dir), 0o755)
	}
	// The Android toolchains are a directory the ignore rules name as one, so they are copied.
	_ = copyTree(filepath.Join(main, "android", ".toolchains"), filepath.Join(path, "android", ".toolchains"))
	if _, err := os.Lstat(filepath.Join(main, ".claude", "settings.local.json")); err == nil {
		_ = os.MkdirAll(filepath.Join(path, ".claude"), 0o755)
		_ = os.Symlink(filepath.Join(main, ".claude", "settings.local.json"), filepath.Join(path, ".claude", "settings.local.json"))
	}
	if !profile {
		return nil
	}
	// The folder exists either way: without it the workspace would run on the shared profile.
	destination := filepath.Join(path, ".local", "profile")
	if out, err := exec.Command("python3", filepath.Join(main, profileScript), destination).CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, "The devel profile was not copied, so the workspace starts with an empty one:", strings.TrimSpace(string(out)), err)
	}
	return os.MkdirAll(destination, 0o755)
}

// RemoveWorkspace removes a session workspace and its files. Its unpublished changes stay in the
// repository.
func RemoveWorkspace(main, name string) error {
	if err := jjRun(main, "workspace", "forget", name); err != nil {
		return err
	}
	if err := os.RemoveAll(WorkspacePath(main, name)); err != nil {
		return err
	}
	return gitRun(main, nil, "worktree", "prune")
}

// Workspaces lists the session workspaces by name.
func Workspaces(main string) []string {
	listed, _ := jjOut(main, "workspace", "list", "-T", `name ++ "\n"`)
	var out []string
	for _, name := range strings.Fields(listed) {
		if name != "default" && name != "publish" {
			out = append(out, name)
		}
	}
	return out
}

// copyTree copies a directory with copy-on-write, or not at all.
func copyTree(from, to string) error {
	if info, err := os.Stat(from); err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a directory", from)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("cp", "-a", "--reflink=always", from, to).CombinedOutput(); err != nil {
		_ = os.RemoveAll(to)
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}
