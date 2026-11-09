package tasks

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AppID names the desktop entry and icon; GTK uses it as the window's app ID.
const AppID = "io.github.screwys.GitGui"

// Files are the desktop entry, icon and user unit that Install writes.
type Files struct {
	Desktop, Icon, Unit []byte
}

// Install puts this binary at ~/.local/bin/git-gui, adds the menu entry and icon, and enables
// and restarts the daemon's user service. Running commands keep going: each has its own
// supervisor and the unit only stops the daemon itself.
func Install(files Files) int {
	if err := install(files); err != nil {
		fmt.Println("Install stopped:", err)
		return 1
	}
	return 0
}

func install(files Files) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	data := envDir("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	configDir := envDir("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	binary := filepath.Join(home, ".local", "bin", "git-gui")
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	if self != binary {
		if err := copyExecutable(self, binary); err != nil {
			return err
		}
	}
	desktop := strings.ReplaceAll(string(files.Desktop), "Exec=git-gui", "Exec="+binary)
	writes := map[string][]byte{
		filepath.Join(data, "applications", AppID+".desktop"):                     []byte(desktop),
		filepath.Join(data, "icons", "hicolor", "scalable", "apps", AppID+".svg"): files.Icon,
		filepath.Join(configDir, "systemd", "user", "git-gui-daemon.service"):     files.Unit,
	}
	for path, content := range writes {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		// The unit path may still be a link into an old checkout.
		_ = os.Remove(path)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
		fmt.Println("Installed", path)
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "git-gui-daemon.service"}, {"restart", "git-gui-daemon.service"}} {
		if err := run("systemctl", append([]string{"--user"}, args...)...); err != nil {
			return err
		}
	}
	fmt.Println("Installed", binary, "and restarted git-gui-daemon.service")
	return nil
}

// copyExecutable replaces dest by renaming a full copy over it, so a running copy keeps working.
func copyExecutable(from, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".git-gui-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

func envDir(name, fallback string) string {
	if dir := os.Getenv(name); dir != "" {
		return dir
	}
	return fallback
}
