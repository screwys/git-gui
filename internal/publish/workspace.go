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

// profileBases are the XDG base folders a devel profile spreads over, with their defaults, and
// the name the workspace's .local/profile gives each.
var profileBases = []struct{ env, fallback, name string }{
	{"XDG_CONFIG_HOME", ".config", "config"},
	{"XDG_DATA_HOME", ".local/share", "data"},
	{"XDG_CACHE_HOME", ".cache", "cache"},
	{"XDG_STATE_HOME", ".local/state", "state"},
}

// profileName is the devel app's folder under each base.
const profileName = "rufin.devel"

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
	home, _ := os.UserHomeDir()
	for _, b := range profileBases {
		base := os.Getenv(b.env)
		if base == "" {
			base = filepath.Join(home, b.fallback)
		}
		destination := filepath.Join(path, ".local", "profile", b.name, profileName)
		if err := copyTree(filepath.Join(base, profileName), destination); err != nil {
			_ = os.MkdirAll(destination, 0o755)
		}
	}
	return nil
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
