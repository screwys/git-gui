// Package config reads and writes git-gui's settings (config.json) and window state (state.json).
// Both files keep the format the Python version wrote, so existing settings carry over.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Config is ~/.config/git-gui/config.json.
type Config struct {
	RufinRepoPath       string              `json:"rufin_repo_path"`
	IglooRepoPath       string              `json:"igloo_repo_path"`
	NocblueRepoPath     string              `json:"nocblue_repo_path"`
	PublishPRScript     string              `json:"publish_pr_script"`
	ReleaseScript       string              `json:"release_script"`
	DefaultBaseBranch   string              `json:"default_base_branch"`
	DefaultAutoMerge    bool                `json:"default_auto_merge"`
	DefaultWaitCI       bool                `json:"default_wait_ci"`
	WrapDiff            bool                `json:"wrap_diff"`
	ProjectPaths        []string            `json:"project_paths"`
	PackageSources      map[string][]Source `json:"package_sources"`
	PackageProjectPaths []string            `json:"package_project_paths"`
}

// Source is a package list the user added for a repository.
type Source struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

func dir() string { return filepath.Join(home(), ".config", "git-gui") }

func home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

// Defaults are the settings of a fresh install.
func Defaults() Config {
	projects := filepath.Join(home(), "Projects")
	return Config{
		RufinRepoPath:     filepath.Join(projects, "Rufin"),
		IglooRepoPath:     filepath.Join(projects, "Igloo"),
		NocblueRepoPath:   filepath.Join(projects, "nocblue"),
		PublishPRScript:   ".local/scripts/publish-pr.sh",
		ReleaseScript:     ".local/scripts/release-rufin.sh",
		DefaultBaseBranch: "main",
		DefaultAutoMerge:  true,
		WrapDiff:          true,
		ProjectPaths:      []string{},
		PackageSources:    map[string][]Source{},
	}
}

// Load reads config.json over the defaults and writes it back when it was missing.
func Load() Config {
	c := Defaults()
	data, err := os.ReadFile(filepath.Join(dir(), "config.json"))
	if err != nil {
		_ = c.Save()
		return c
	}
	_ = json.Unmarshal(data, &c)
	if c.PackageSources == nil {
		c.PackageSources = map[string][]Source{}
	}
	return c
}

// Save writes config.json.
func (c Config) Save() error { return writeJSON(filepath.Join(dir(), "config.json"), c) }

// PublishScript is the Rufin publish helper's absolute path.
func (c Config) PublishScript() string { return c.resolve(c.PublishPRScript) }

// ReleaseHelper is the Rufin release helper's absolute path.
func (c Config) ReleaseHelper() string { return c.resolve(c.ReleaseScript) }

func (c Config) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(c.RufinRepoPath, path)
}

// HasPackages reports whether an added project shows the Packages tab.
func (c Config) HasPackages(repo string) bool {
	for _, p := range c.PackageProjectPaths {
		if same(p, repo) {
			return true
		}
	}
	return false
}

func same(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// State is ~/.config/git-gui/state.json: which tabs were open and saved forms.
type State struct {
	mu     sync.Mutex
	values map[string]json.RawMessage
}

var state = &State{}

// Get decodes a saved value into out and reports whether it existed.
func Get(key string, out any) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.load()
	raw, ok := state.values[key]
	if !ok || string(raw) == "null" {
		return false
	}
	return json.Unmarshal(raw, out) == nil
}

// Set saves a value; nil removes it.
func Set(key string, value any) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.load()
	if value == nil {
		delete(state.values, key)
	} else if raw, err := json.Marshal(value); err == nil {
		state.values[key] = raw
	}
	_ = writeJSON(filepath.Join(dir(), "state.json"), state.values)
}

func (s *State) load() {
	if s.values != nil {
		return
	}
	s.values = map[string]json.RawMessage{}
	if data, err := os.ReadFile(filepath.Join(dir(), "state.json")); err == nil {
		_ = json.Unmarshal(data, &s.values)
	}
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
