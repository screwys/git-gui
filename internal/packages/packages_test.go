package packages

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const recipe = `modules:
  - type: dnf
    install:
      packages:
        # Desktop
        - niri   # compositor
        - "ghostty"

    remove:
    - old
  - type: rpm-ostree
    install: [a, 'b c'] # flow
  - {type: dnf, remove: ["é", b], install: [x]}
`

func names(t *testing.T, source Source, text string) []string {
	t.Helper()
	entries, err := Entries(source, text)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestEditKeepsLayout(t *testing.T) {
	sources, err := SourcesForFile("RPM", "recipes/recipe.yml", recipe)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, s := range sources {
		labels = append(labels, s.Label())
	}
	want := []string{
		"recipes/recipe.yml · modules / 0 / install / packages",
		"recipes/recipe.yml · modules / 0 / remove",
		"recipes/recipe.yml · modules / 1 / install",
		"recipes/recipe.yml · modules / 2 / install",
		"recipes/recipe.yml · modules / 2 / remove",
	}
	if !slices.Equal(labels, want) {
		t.Fatalf("sources %q", labels)
	}
	packages, remove, flow, inline := sources[0], sources[1], sources[2], sources[3]
	if got := names(t, packages, recipe); !slices.Equal(got, []string{"niri", "ghostty"}) {
		t.Fatalf("entries %q", got)
	}

	cases := []struct {
		name   string
		source Source
		text   string
		add    string
		remove []int
		want   string
	}{
		{"block add", packages, recipe, "foot", nil,
			strings.Replace(recipe, `        - "ghostty"`+"\n", `        - "ghostty"`+"\n"+`        - "foot"`+"\n", 1)},
		{"block remove keeps comments", packages, recipe, "", []int{0},
			strings.Replace(recipe, "        - niri   # compositor\n", "", 1)},
		{"block remove all", packages, recipe, "", []int{1, 0},
			strings.Replace(recipe, "        - niri   # compositor\n        - \"ghostty\"\n", "          []\n", 1)},
		{"indentless remove all", remove, recipe, "", []int{0},
			strings.Replace(recipe, "    remove:\n    - old\n", "    remove:\n      []\n", 1)},
		{"flow add", flow, recipe, "d", nil,
			strings.Replace(recipe, "[a, 'b c'] # flow", "[a, 'b c', d] # flow", 1)},
		{"flow remove", flow, recipe, "", []int{0},
			strings.Replace(recipe, "[a, 'b c'] # flow", "['b c'] # flow", 1)},
		{"flow after wide characters", inline, recipe, "y", nil,
			strings.Replace(recipe, "install: [x]}", "install: [x, y]}", 1)},
		{"text keeps CRLF", Source{Kind: "RPM", Path: "rpm.txt"}, "a\r\nb # note\r\nc", "d", nil, "a\r\nb # note\r\nc\r\nd\r\n"},
		{"text remove", Source{Kind: "RPM", Path: "rpm.txt"}, "a\r\nb # note\r\nc", "", []int{1}, "a\r\nc"},
		{"Brewfile bare name", Source{Kind: "Brew", Path: "Brewfile"}, "tap \"homebrew/core\"\n# tools\ncask 'firefox'\n", "jq", nil,
			"tap \"homebrew/core\"\n# tools\ncask 'firefox'\nbrew \"jq\"\n"},
	}
	for _, c := range cases {
		got, err := Edit(c.source, c.text, c.add, c.remove)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		// Every edit must leave a file that still parses.
		if _, err := Entries(c.source, got); err != nil {
			t.Errorf("%s: result does not parse: %v", c.name, err)
		}
	}
	brew := Source{Kind: "Brew", Path: "Brewfile"}
	if got := names(t, brew, "tap \"homebrew/core\"\n# tools\ncask 'firefox'\n"); !slices.Equal(got, []string{"tap homebrew/core", "cask firefox"}) {
		t.Errorf("Brewfile entries %q", got)
	}
	if got := names(t, Source{Kind: "RPM", Path: "rpm.txt"}, "a\r\nb # note\r\nc"); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("text entries %q", got)
	}
}

func TestSaveMirrorsAndRestores(t *testing.T) {
	repo := t.TempDir()
	source := Source{Kind: "RPM", Path: "manifests/native-optional.txt"}
	mirror := "files/system/usr/share/nocblue/manifests/native-optional.txt"
	write := func(name, text string) {
		full := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	write(source.Path, "a\nb\n")
	write(mirror, "b\na\nc\n")
	mirrors := Mirrors(repo, source, true)
	if !slices.Equal(mirrors, []string{mirror}) {
		t.Fatalf("mirrors %q", mirrors)
	}

	// The mirror loses the same name even though its order differs, and an add already there
	// is skipped.
	if err := Save(repo, source, "a\nb\n", "", []int{0}, mirrors); err != nil {
		t.Fatal(err)
	}
	if read(source.Path) != "b\n" || read(mirror) != "b\nc\n" {
		t.Fatalf("after remove: %q %q", read(source.Path), read(mirror))
	}
	if err := Save(repo, source, "b\n", "c", nil, mirrors); err != nil {
		t.Fatal(err)
	}
	if read(source.Path) != "b\nc\n" || read(mirror) != "b\nc\n" {
		t.Fatalf("after add: %q %q", read(source.Path), read(mirror))
	}

	// A file edited on disk since it was shown is never overwritten.
	if err := Save(repo, source, "stale\n", "d", nil, mirrors); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale save: %v", err)
	}

	// A failed mirror write puts the source back.
	if os.Geteuid() == 0 {
		t.Skip("root writes read-only files")
	}
	if err := os.Chmod(filepath.Join(repo, mirror), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := Save(repo, source, "b\nc\n", "d", nil, mirrors); err == nil {
		t.Fatal("save into a read-only mirror succeeded")
	}
	if read(source.Path) != "b\nc\n" || read(mirror) != "b\nc\n" {
		t.Fatalf("after failed save: %q %q", read(source.Path), read(mirror))
	}
}
