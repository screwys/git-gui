// Package packages reads package lists (BlueBuild recipes, Brewfiles and plain lists) and edits
// their entries in place, so the rest of each file keeps its layout and comments.
package packages

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/git"
)

// Kinds are the package managers a source can belong to, in the order the tab offers them.
var Kinds = []string{"RPM", "Brew", "Nix"}

// bundleEntry matches a Brewfile line such as `brew "name"`.
var bundleEntry = regexp.MustCompile(`^\s*(brew|cask|tap|mas|vscode|go|cargo|uv|whalebrew)\s+(?:"([^"\n]*)"|'([^'\n]*)')`)

// Source is one package list: a whole text file, or one list inside a YAML recipe.
type Source struct {
	Kind    string
	Path    string   // relative to the repository, with forward slashes
	Section []string // keys and list indexes down to the list in a YAML file
}

// Label is how the source picker names it.
func (s Source) Label() string {
	if len(s.Section) == 0 {
		return s.Path
	}
	return s.Path + " · " + strings.Join(s.Section, " / ")
}

// Equal reports whether two values name the same list.
func (s Source) Equal(o Source) bool {
	return s.Kind == o.Kind && s.Path == o.Path && slices.Equal(s.Section, o.Section)
}

func (s Source) brewfile() bool { return path.Base(s.Path) == "Brewfile" }

// Entry is one package in a list. Start and End span its whole lines in block lists and text
// files; flow lists are rewritten whole, so their entries have no span.
type Entry struct {
	Name       string
	Start, End int
}

// ErrChanged means the file changed on disk since it was read.
var ErrChanged = errors.New("source changed on disk")

// ReadText reads a file that must be UTF-8.
func ReadText(name string) (string, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not UTF-8 text", filepath.Base(name))
	}
	return string(data), nil
}

func isYAML(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".yml" || ext == ".yaml"
}

// Discover lists the repository's package sources: every list of a dnf or rpm-ostree module in
// recipes/, the known list files, and the sources the user added. Unreadable files are reported
// as "path: error" and skipped.
func Discover(repo string, configured []config.Source) ([]Source, []string) {
	var order []string
	kinds := map[string]string{}
	add := func(name, kind string) {
		if _, seen := kinds[name]; !seen {
			order = append(order, name)
		}
		kinds[name] = kind
	}
	var recipes []string
	_ = filepath.WalkDir(filepath.Join(repo, "recipes"), func(full string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && isYAML(full) {
			rel, _ := filepath.Rel(repo, full)
			recipes = append(recipes, filepath.ToSlash(rel))
		}
		return nil
	})
	// recipe.yml leads; the rest follow by path.
	key := func(name string) string {
		if path.Base(name) == "recipe.yml" {
			return "0" + name
		}
		return "1" + name
	}
	slices.SortFunc(recipes, func(a, b string) int { return strings.Compare(key(a), key(b)) })
	for _, r := range recipes {
		add(r, "RPM")
	}
	known := []struct{ file, kind string }{{"Brewfile", "Brew"}, {"brew.txt", "Brew"}, {"rpm.txt", "RPM"},
		{"rpm-packages.txt", "RPM"}, {"nix.txt", "Nix"}, {"nix-profile.txt", "Nix"}}
	for _, folder := range []string{".", "manifests", "packages", "restore/packages"} {
		for _, k := range known {
			name := path.Join(folder, k.file)
			if info, err := os.Stat(filepath.Join(repo, name)); err == nil && info.Mode().IsRegular() {
				add(name, k.kind)
			}
		}
	}
	for _, c := range configured {
		add(c.Path, c.Kind)
	}
	var found []Source
	var errs []string
	for _, name := range order {
		text, err := ReadText(filepath.Join(repo, name))
		if err == nil {
			var sources []Source
			if sources, err = SourcesForFile(kinds[name], name, text); err == nil {
				found = append(found, sources...)
				continue
			}
		}
		errs = append(errs, name+": "+err.Error())
	}
	return found, errs
}

// SourcesForFile is the file itself for a text list, or each install and remove list of the
// dnf and rpm-ostree modules in a YAML recipe.
func SourcesForFile(kind, name, text string) ([]Source, error) {
	if !isYAML(name) {
		return []Source{{Kind: kind, Path: name}}, nil
	}
	root, err := parse(text)
	if err != nil || root == nil {
		return nil, err
	}
	var found []Source
	var visit func(n *yaml.Node, section []string)
	visit = func(n *yaml.Node, section []string) {
		switch n.Kind {
		case yaml.MappingNode:
			if t := child(n, "type"); t != nil && t.Kind == yaml.ScalarNode && (t.Value == "dnf" || t.Value == "rpm-ostree") {
				for _, operation := range []string{"install", "remove"} {
					list := child(n, operation)
					address := append(slices.Clone(section), operation)
					if list != nil && list.Kind == yaml.MappingNode {
						list = child(list, "packages")
						address = append(address, "packages")
					}
					if list != nil && list.Kind == yaml.SequenceNode {
						found = append(found, Source{Kind: kind, Path: name, Section: address})
					}
				}
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				visit(n.Content[i+1], append(slices.Clone(section), n.Content[i].Value))
			}
		case yaml.SequenceNode:
			for i, item := range n.Content {
				visit(item, append(slices.Clone(section), strconv.Itoa(i)))
			}
		}
	}
	visit(root, nil)
	return found, nil
}

// parse returns the document's top node, or nil for an empty file.
func parse(text string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	return doc.Content[0], nil
}

func child(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// list finds the source's package list in a recipe.
func list(source Source, text string) (*yaml.Node, error) {
	n, err := parse(text)
	if err != nil {
		return nil, err
	}
	for _, key := range source.Section {
		var next *yaml.Node
		switch {
		case n == nil:
		case n.Kind == yaml.MappingNode:
			next = child(n, key)
		case n.Kind == yaml.SequenceNode:
			if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(n.Content) {
				next = n.Content[i]
			}
		}
		n = next
	}
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s has no package list at %s", source.Path, strings.Join(source.Section, " / "))
	}
	return n, nil
}

// Entries lists the packages of a source in file order.
func Entries(source Source, text string) ([]Entry, error) {
	if len(source.Section) > 0 {
		node, err := list(source, text)
		if err != nil {
			return nil, err
		}
		pos := newPositions(text)
		var entries []Entry
		for _, item := range node.Content {
			if node.Style&yaml.FlowStyle != 0 {
				entries = append(entries, Entry{Name: flowName(item)})
				continue
			}
			start, end := pos.itemLines(item, node.Column-1)
			name := item.Value
			if item.Kind != yaml.ScalarNode {
				name = strings.TrimSpace(text[pos.offset(item.Line, item.Column):end])
			}
			entries = append(entries, Entry{name, start, end})
		}
		return entries, nil
	}
	var entries []Entry
	for offset := 0; offset < len(text); {
		end := len(text)
		if i := strings.IndexByte(text[offset:], '\n'); i >= 0 {
			end = offset + i + 1
		}
		line := text[offset:end]
		if name := strings.TrimSpace(line); name != "" && !strings.HasPrefix(name, "#") {
			if source.brewfile() {
				if m := bundleEntry.FindStringSubmatch(line); m != nil {
					entries = append(entries, Entry{m[1] + " " + m[2] + m[3], offset, end})
				}
			} else {
				if loc := comment.FindStringIndex(name); loc != nil {
					name = strings.TrimSpace(name[:loc[0]])
				}
				entries = append(entries, Entry{name, offset, end})
			}
		}
		offset = end
	}
	return entries, nil
}

var comment = regexp.MustCompile(`\s+#`)

// flowName names an item of a flow list: its value, or its YAML for a nested collection.
func flowName(item *yaml.Node) string {
	if item.Kind == yaml.ScalarNode {
		return item.Value
	}
	out, _ := yaml.Marshal(item)
	return strings.TrimSpace(string(out))
}

// EntryName is how a typed name appears in the list: Brewfiles show a bare name as "brew name".
func EntryName(source Source, value string) string {
	if source.brewfile() {
		if m := bundleEntry.FindStringSubmatch(value); m != nil {
			return m[1] + " " + m[2] + m[3]
		}
		return "brew " + value
	}
	return value
}

// Edit adds one name or removes the entries at the given indexes, changing nothing else.
func Edit(source Source, text, add string, remove []int) (string, error) {
	current, err := Entries(source, text)
	if err != nil {
		return "", err
	}
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	dash := 0 // the column of a block list's dashes
	if len(source.Section) > 0 {
		node, err := list(source, text)
		if err != nil {
			return "", err
		}
		if node.Style&yaml.FlowStyle != 0 {
			// A flow list is rewritten in flow style; the text around it stays.
			rewritten := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
			for i, item := range node.Content {
				if !slices.Contains(remove, i) {
					dropComments(item)
					rewritten.Content = append(rewritten.Content, item)
				}
			}
			if add != "" {
				rewritten.Content = append(rewritten.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: add})
			}
			out, err := yaml.Marshal(rewritten)
			if err != nil {
				return "", err
			}
			start := newPositions(text).offset(node.Line, node.Column)
			return text[:start] + strings.TrimSpace(string(out)) + text[flowEnd(text, start):], nil
		}
		dash = node.Column - 1
		if add != "" {
			at := current[len(current)-1].End
			prefix := ""
			if !strings.HasSuffix(text[:at], "\n") {
				prefix = newline
			}
			return text[:at] + prefix + strings.Repeat(" ", dash) + "- " + quote(add) + newline + text[at:], nil
		}
	} else if add != "" {
		if source.brewfile() && !bundleEntry.MatchString(add) {
			add = "brew " + quote(add)
		}
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += newline
		}
		return text + add + newline, nil
	}
	remove = slices.Sorted(slices.Values(remove))
	for k := len(remove) - 1; k >= 0; k-- {
		entry := current[remove[k]]
		replacement := ""
		if len(source.Section) > 0 && len(remove) == len(current) && k == 0 {
			// An emptied block list becomes an empty flow list, indented past its key.
			replacement = strings.Repeat(" ", dash+2) + "[]" + newline
		}
		text = text[:entry.Start] + replacement + text[entry.End:]
	}
	return text, nil
}

// quote writes a name as a JSON string, which YAML and Brewfiles both read.
func quote(value string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(value)
	return strings.TrimSuffix(b.String(), "\n")
}

// dropComments keeps a rewritten flow list on one line.
func dropComments(n *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, c := range n.Content {
		dropComments(c)
	}
}

// positions turns the parser's 1-based lines and character columns into byte offsets.
type positions struct {
	text   string
	starts []int // byte offset of each line
}

func newPositions(text string) positions {
	p := positions{text: text, starts: []int{0}}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			p.starts = append(p.starts, i+1)
		}
	}
	return p
}

func (p positions) offset(line, column int) int {
	at := p.starts[line-1]
	for range column - 1 {
		_, size := utf8.DecodeRuneInString(p.text[at:])
		at += size
	}
	return at
}

func (p positions) lineEnd(line int) int {
	if line < len(p.starts) {
		return p.starts[line]
	}
	return len(p.text)
}

// itemLines spans a block list item's lines: from its first line through the last line of its
// content, which is more indented than the list's dash column. Comment and blank lines after
// the content belong to whatever follows.
func (p positions) itemLines(item *yaml.Node, dash int) (int, int) {
	last := item.Line
	for line := item.Line + 1; line <= len(p.starts); line++ {
		text := strings.TrimRight(p.text[p.starts[line-1]:p.lineEnd(line)], "\r\n")
		trimmed := strings.TrimLeft(text, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(text)-len(trimmed) <= dash {
			break
		}
		last = line
	}
	return p.starts[item.Line-1], p.lineEnd(last)
}

// flowEnd is the offset just past the bracket closing the flow collection that opens at start.
// The parser has already accepted the text, so only quotes and comments need care.
func flowEnd(text string, start int) int {
	depth, atNode := 0, true
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '[' || c == '{':
			depth++
			atNode = true
			continue
		case c == ']' || c == '}':
			if depth--; depth == 0 {
				return i + 1
			}
		case c == ',' || c == ':':
			atNode = true
			continue
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			continue
		case c == '#' && (text[i-1] == ' ' || text[i-1] == '\t' || text[i-1] == '\n'):
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		case atNode && c == '"':
			for i++; i < len(text) && text[i] != '"'; i++ {
				if text[i] == '\\' {
					i++
				}
			}
		case atNode && c == '\'':
			for i++; i < len(text); i++ {
				if text[i] == '\'' {
					if i+1 < len(text) && text[i+1] == '\'' {
						i++
						continue
					}
					break
				}
			}
		}
		atNode = false
	}
	return len(text)
}

// Mirrors are the copies an edit must also change: in nocblue, a manifest's runtime copy.
func Mirrors(repo string, source Source, nocblue bool) []string {
	if !nocblue || !strings.HasPrefix(source.Path, "manifests/") {
		return nil
	}
	mirror := path.Join("files/system/usr/share/nocblue", source.Path)
	if info, err := os.Stat(filepath.Join(repo, mirror)); err == nil && info.Mode().IsRegular() {
		return []string{mirror}
	}
	return nil
}

// Save applies an edit to the source, whose text was read as before, and to its mirrors.
// It returns ErrChanged when the file changed on disk since. If a write fails, the files
// already written get their old content back.
func Save(repo string, source Source, before, add string, remove []int, mirrors []string) error {
	current, err := ReadText(filepath.Join(repo, source.Path))
	if err != nil {
		return err
	}
	if current != before {
		return ErrChanged
	}
	updated, err := Edit(source, before, add, remove)
	if err != nil {
		return err
	}
	entries, _ := Entries(source, before)
	removed := map[string]bool{}
	for _, i := range remove {
		removed[entries[i].Name] = true
	}
	paths := append([]string{source.Path}, mirrors...)
	originals := make([]string, len(paths))
	contents := make([]string, len(paths))
	for i, name := range paths {
		if originals[i], err = ReadText(filepath.Join(repo, name)); err != nil {
			return err
		}
		if i == 0 {
			contents[i] = updated
			continue
		}
		// Mirrors are matched by name because their order can differ.
		theirs, err := Entries(source, originals[i])
		if err != nil {
			return err
		}
		var indexes []int
		listed := false
		for j, e := range theirs {
			if removed[e.Name] {
				indexes = append(indexes, j)
			}
			listed = listed || e.Name == EntryName(source, add)
		}
		contents[i] = originals[i]
		switch {
		case add != "" && !listed:
			contents[i], err = Edit(source, originals[i], add, nil)
		case len(indexes) > 0:
			contents[i], err = Edit(source, originals[i], "", indexes)
		}
		if err != nil {
			return err
		}
	}
	for i, name := range paths {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(contents[i]), 0o644); err != nil {
			for j := range i {
				_ = os.WriteFile(filepath.Join(repo, paths[j]), []byte(originals[j]), 0o644)
			}
			return err
		}
	}
	return nil
}

// CommitSubject describes what the sources gained and lost against HEAD, such as
// "packages: add a and b".
func CommitSubject(repo string, sources []Source) string {
	added, removed := map[string]bool{}, map[string]bool{}
	for _, source := range sources {
		now, err := ReadText(filepath.Join(repo, source.Path))
		if err != nil {
			continue
		}
		newEntries, err := Entries(source, now)
		if err != nil {
			continue
		}
		old := map[string]bool{}
		if before, err := git.Run(repo, "show", "HEAD:"+source.Path); err == nil {
			oldEntries, err := Entries(source, before)
			if err != nil {
				continue
			}
			for _, e := range oldEntries {
				old[e.Name] = true
			}
		}
		current := map[string]bool{}
		for _, e := range newEntries {
			current[e.Name] = true
			if !old[e.Name] {
				added[e.Name] = true
			}
		}
		for name := range old {
			if !current[name] {
				removed[name] = true
			}
		}
	}
	return Subject(added, removed)
}

// Subject words a commit subject for the added and removed names.
func Subject(added, removed map[string]bool) string {
	switch {
	case len(added) > 0 && len(removed) > 0:
		return "packages: add " + describe(added) + " and remove " + describe(removed)
	case len(added) > 0:
		return "packages: add " + describe(added)
	case len(removed) > 0:
		return "packages: remove " + describe(removed)
	}
	return ""
}

// describe lists names as "a, b and c": Brewfile kinds "brew " drop, and a flake reference
// keeps the last dotted part after "#".
func describe(names map[string]bool) string {
	set := map[string]bool{}
	for name := range names {
		name = strings.TrimPrefix(name, "brew ")
		if i := strings.LastIndex(name, "#"); i >= 0 {
			parts := strings.Split(name[i+1:], ".")
			name = parts[len(parts)-1]
		}
		set[strings.Join(strings.Fields(name), " ")] = true
	}
	labels := slices.Sorted(maps.Keys(set))
	if len(labels) <= 2 {
		return strings.Join(labels, " and ")
	}
	return strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1]
}
