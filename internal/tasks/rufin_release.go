package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/git"
	"github.com/screwys/git-gui/internal/publish"
)

const rufinReleaseUsage = `Usage: git-gui rufin-release [--replace | --resume] [--dry-run] VERSION [SUMMARY]

Releases Rufin from its checkout. A new release publishes the preparation PR (version, MetaInfo,
packaging) and waits for it to merge, then tags the merge commit with the release notes. The tag
starts release.yml, which builds every package into a draft and publishes it. Afterwards the
Homebrew cask is updated and the Flathub PR is opened and merged.

  --resume    Continue a release: finished steps are skipped
  --replace   Move an unpublished release's tag to the newest main and start its workflow over
  --dry-run   Print the release notes and stop
`

const (
	flathubRepo  = "flathub/io.github.screwys.Rufin"
	homebrewRepo = "screwys/homebrew-tap"
)

var runURL = regexp.MustCompile(`https://github\.com/\S+/actions/runs/\d+`)

// RufinRelease is "git-gui rufin-release".
func RufinRelease(args []string) int {
	mode, dryRun := "new", false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "--replace", "--resume":
			mode = strings.TrimPrefix(arg, "--")
		case "--dry-run":
			dryRun = true
		case "-h", "--help":
			fmt.Print(rufinReleaseUsage)
			return 0
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) == 0 || len(positional) > 2 {
		fmt.Print(rufinReleaseUsage)
		return 2
	}
	version := "v" + strings.TrimPrefix(positional[0], "v")
	summary := ""
	if len(positional) == 2 {
		summary = strings.TrimSpace(positional[1])
	}
	r, err := newRufinRelease(version, summary)
	if err == nil {
		if dryRun {
			err = r.notes("origin/" + r.base)
		} else {
			err = r.run(mode)
		}
	}
	if err != nil {
		fmt.Println("Release stopped:", err)
		return 1
	}
	return 0
}

type rufinRelease struct {
	root, slug, base string
	version, plain   string
	summary          string
}

func newRufinRelease(version, summary string) (*rufinRelease, error) {
	c, err := publish.Open(".")
	if err != nil {
		return nil, err
	}
	slug := git.RemoteSlug(c.Root)
	if slug == "" {
		return nil, errors.New("origin is not a GitHub repository")
	}
	r := &rufinRelease{root: c.Root, slug: slug, base: config.Load().DefaultBaseBranch, version: version,
		plain: strings.TrimPrefix(version, "v"), summary: summary}
	return r, r.git("fetch", "--prune", "--force", "--tags", "origin")
}

func (r *rufinRelease) run(mode string) error {
	var target string
	var err error
	if mode == "replace" {
		target, err = r.replace()
	} else {
		target, err = r.prepare(mode == "resume")
	}
	if err != nil {
		return err
	}
	if err := r.tag(target, mode == "replace"); err != nil {
		return err
	}
	if err := r.followRelease(target); err != nil {
		return err
	}
	return r.publishPackages()
}

// prepare publishes the preparation PR and waits for it to merge, or finds it merged, and returns
// its merge commit.
func (r *rufinRelease) prepare(resume bool) (string, error) {
	branch := "release/" + r.version
	if merged := r.mergedPrep(branch); merged != "" {
		fmt.Printf("Preparation for %s merged as %.12s.\n", r.version, merged)
		return merged, nil
	}
	number := 0
	if out, _ := r.gh("pr", "list", "--repo", r.slug, "--head", branch, "--state", "open", "--json", "number", "--jq", ".[0].number // empty"); out != "" {
		fmt.Sscan(out, &number)
		fmt.Printf("Following the open preparation PR #%d.\n", number)
	} else {
		if r.summary == "" {
			return "", errors.New("a summary is needed to prepare " + r.version)
		}
		if resume {
			fmt.Println("No preparation PR found; preparing", r.version)
		}
		result, err := publish.Publish(r.root, publish.Options{
			Base: r.base, Branch: branch, Title: "release: publish prep for " + r.version, AutoMerge: true,
			Run:     "cargo run --locked -p xtask -- release prepare " + quote(r.plain) + " " + quote(r.summary),
			Instant: true, // xtask prepare writes every generated file it changes
		})
		if err != nil {
			return "", err
		}
		number = result.Number
	}
	fmt.Printf("Waiting for preparation PR #%d to merge...\n", number)
	switch waitPR(r.slug, number) {
	case 0:
	case 1, 2:
		return "", fmt.Errorf("preparation PR #%d needs a fix; resume the release once it is pushed", number)
	default:
		return "", fmt.Errorf("preparation PR #%d did not merge", number)
	}
	if err := r.git("fetch", "--prune", "origin"); err != nil {
		return "", err
	}
	merged := r.mergedPrep(branch)
	if merged == "" {
		return "", errors.New("could not find the preparation PR's merge commit")
	}
	return merged, nil
}

func (r *rufinRelease) mergedPrep(branch string) string {
	out, _ := r.gh("pr", "list", "--repo", r.slug, "--head", branch, "--state", "merged", "--json", "mergeCommit",
		"--jq", ".[0].mergeCommit.oid // empty")
	return out
}

// replace makes way for a new tag at the newest main: an unpublished release's draft and runs go.
func (r *rufinRelease) replace() (string, error) {
	target, err := r.out("git", "rev-parse", "origin/"+r.base)
	if err != nil {
		return "", err
	}
	manifest, _ := r.out("git", "show", target+":Cargo.toml")
	if !strings.Contains(manifest, `version = "`+r.plain+`"`) {
		return "", fmt.Errorf("%s on origin/%s is not version %s", target[:12], r.base, r.plain)
	}
	if r.summary == "" {
		r.summary = tagSummary(r.root, r.version)
	}
	if draft, err := r.gh("release", "view", r.version, "--repo", r.slug, "--json", "isDraft", "--jq", ".isDraft"); err == nil {
		if draft != "true" {
			return "", fmt.Errorf("%s is published; a published release cannot be replaced", r.version)
		}
		r.cancelRuns()
		fmt.Println("Deleting the draft of", r.version)
		if _, err := r.gh("release", "delete", r.version, "--repo", r.slug, "--yes"); err != nil {
			return "", err
		}
	} else {
		r.cancelRuns()
	}
	return target, nil
}

func (r *rufinRelease) cancelRuns() {
	out, _ := r.gh("run", "list", "--repo", r.slug, "--workflow", "release.yml", "--branch", r.version,
		"--json", "databaseId,status", "--jq", `.[] | select(.status != "completed") | .databaseId`)
	for _, id := range strings.Fields(out) {
		fmt.Println("Cancelling release run", id)
		_, _ = r.gh("run", "cancel", id, "--repo", r.slug)
	}
}

// tag creates and pushes the signed release tag at target. A pushed tag there is kept.
func (r *rufinRelease) tag(target string, replace bool) error {
	ref := "refs/tags/" + r.version
	listed, _ := r.out("git", "ls-remote", "origin", ref, ref+"^{}")
	remote := ""
	for _, line := range strings.Split(listed, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && (fields[1] == ref+"^{}" || remote == "") {
			remote = fields[0]
		}
	}
	if remote == target {
		fmt.Printf("%s is already pushed at %.12s.\n", r.version, target)
		return nil
	}
	if remote != "" && !replace {
		return fmt.Errorf("%s is pushed at another commit; replace the release to move it", r.version)
	}
	if r.summary == "" {
		r.summary = tagSummary(r.root, r.version)
	}
	if r.summary == "" {
		return errors.New("a summary is needed for the tag of " + r.version)
	}
	cmd := exec.Command("cargo", "run", "--locked", "-q", "-p", "xtask", "--", "release", "notes", "--target", target, r.version, r.summary)
	cmd.Dir, cmd.Stderr = r.root, os.Stderr
	notes, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("release notes: %w", err)
	}
	fmt.Printf("Release notes\n\n%s\n", notes)
	tag := exec.Command("git", "tag", "--force", "--sign", "--cleanup=verbatim", "--file", "-", r.version, target)
	tag.Dir, tag.Stdin, tag.Stdout, tag.Stderr = r.root, strings.NewReader(string(notes)), os.Stdout, os.Stderr
	if err := tag.Run(); err != nil {
		return fmt.Errorf("git tag: %w", err)
	}
	push := []string{"push", "--no-verify", "origin", ref + ":" + ref}
	if replace {
		push = append(push, "--force")
	}
	return r.git(push...)
}

// followRelease waits for release.yml on the tag to finish.
func (r *rufinRelease) followRelease(target string) error {
	type run struct {
		ID         int64  `json:"databaseId"`
		HeadSha    string `json:"headSha"`
		URL        string `json:"url"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	var found *run
	for deadline := time.Now().Add(3 * time.Minute); ; time.Sleep(10 * time.Second) {
		out, err := r.gh("run", "list", "--repo", r.slug, "--workflow", "release.yml", "--branch", r.version,
			"--event", "push", "--limit", "5", "--json", "databaseId,headSha,url,status,conclusion")
		if err != nil {
			return err
		}
		var runs []run
		_ = json.Unmarshal([]byte(out), &runs)
		for i := range runs {
			if runs[i].HeadSha == target {
				found = &runs[i]
				break
			}
		}
		if found != nil {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("release.yml did not start for " + r.version)
		}
	}
	fmt.Println("Following", found.URL)
	for found.Status != "completed" {
		time.Sleep(30 * time.Second)
		out, err := r.gh("run", "view", fmt.Sprint(found.ID), "--repo", r.slug, "--json", "status,conclusion")
		if err == nil {
			_ = json.Unmarshal([]byte(out), found)
		}
	}
	if found.Conclusion != "success" {
		return fmt.Errorf("release.yml ended with %s; re-run its failed jobs from Jobs and Progress, then resume", found.Conclusion)
	}
	fmt.Println("Published", r.version+":", "https://github.com/"+r.slug+"/releases/tag/"+r.version)
	return nil
}

// publishPackages updates the Homebrew cask and lands the Flathub update.
func (r *rufinRelease) publishPackages() error {
	out, err := r.gh("workflow", "run", "update-cask.yml", "--repo", homebrewRepo, "--ref", "main", "-f", "tag="+r.version)
	homebrew := runURL.FindString(out)
	if err == nil && homebrew == "" {
		err = errors.New("could not find the Homebrew run")
	}
	if err == nil {
		fmt.Println("Started Homebrew:", homebrew)
	}
	flathubErr := r.flathub()
	if err == nil {
		err = waitRun(homebrewRepo, homebrew[strings.LastIndex(homebrew, "/")+1:])
	}
	if err != nil {
		return fmt.Errorf("Homebrew: %w", err)
	}
	if flathubErr != nil {
		return fmt.Errorf("Flathub: %w", flathubErr)
	}
	fmt.Println("All package-manager publication workflows completed successfully.")
	return nil
}

func waitRun(repo, id string) error {
	for {
		out, err := exec.Command("gh", "run", "view", id, "--repo", repo, "--json", "status,conclusion", "--jq", `.status + " " + .conclusion`).Output()
		if fields := strings.Fields(string(out)); err == nil && len(fields) > 0 && fields[0] == "completed" {
			if len(fields) < 2 || fields[1] != "success" {
				return fmt.Errorf("run %s did not succeed", id)
			}
			return nil
		}
		time.Sleep(30 * time.Second)
	}
}

// flathub opens the Flathub PR with the tag's manifest and merges it once Flathub's build passes.
func (r *rufinRelease) flathub() error {
	branch, title := "release/"+r.version, "Update Rufin to "+r.version
	dir, err := os.MkdirTemp("", "git-gui-flathub-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	clone := filepath.Join(dir, "flathub")
	if err := r.git("clone", "-q", "https://github.com/"+flathubRepo+".git", clone); err != nil {
		return err
	}
	in := func(args ...string) error { return r.git(append([]string{"-C", clone}, args...)...) }
	if err := in("switch", "-q", "-C", branch, "origin/master"); err != nil {
		return err
	}
	files := map[string]string{
		"packaging/flatpak/io.github.screwys.Rufin.flathub.json": "io.github.screwys.Rufin.json",
		"packaging/flatpak/cargo-sources.json":                   "cargo-sources.json",
		"packaging/flatpak/update-generated-sources.flathub.yml": ".github/workflows/update-generated-sources.yml",
	}
	for from, to := range files {
		data, err := exec.Command("git", "-C", r.root, "show", r.version+":"+from).Output()
		if err != nil {
			continue
		}
		path := filepath.Join(clone, to)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	update := exec.Command("cargo", "run", "--locked", "-q", "-p", "xtask", "--", "release", "update-flathub-manifest",
		"--manifest", filepath.Join(clone, "io.github.screwys.Rufin.json"), r.version)
	update.Dir, update.Stdout, update.Stderr = r.root, os.Stdout, os.Stderr
	if err := update.Run(); err != nil {
		return err
	}
	if changed, _ := r.out("git", "-C", clone, "status", "--porcelain"); changed != "" {
		if err := in("add", "-A"); err != nil {
			return err
		}
		if err := in("commit", "-q", "-m", title); err != nil {
			return err
		}
		if err := in("push", "-q", "--force-with-lease", "origin", "HEAD:refs/heads/"+branch); err != nil {
			return err
		}
	}
	state, _ := r.gh("pr", "list", "--repo", flathubRepo, "--head", branch, "--state", "all", "--json", "number,state", "--jq", `.[0] | "\(.number) \(.state)"`)
	if strings.HasSuffix(state, " MERGED") {
		fmt.Println("Flathub already has", r.version)
		return nil
	}
	number, _, _ := strings.Cut(state, " ")
	if !strings.HasSuffix(state, " OPEN") {
		if _, err := r.gh("pr", "create", "--repo", flathubRepo, "--base", "master", "--head", branch, "--title", title, "--body", ""); err != nil {
			return err
		}
		number, _ = r.gh("pr", "list", "--repo", flathubRepo, "--head", branch, "--state", "open", "--json", "number", "--jq", ".[0].number")
	}
	url, _ := r.gh("pr", "view", number, "--repo", flathubRepo, "--json", "url", "--jq", ".url")
	fmt.Println("Flathub PR:", url)
	// Flathub's build takes a while before the PR can merge.
	deadline := time.Now().Add(2 * time.Hour)
	for {
		out, _ := r.gh("pr", "view", number, "--repo", flathubRepo, "--json", "state,mergeStateStatus", "--jq", `.state + " " + .mergeStateStatus`)
		switch {
		case strings.HasPrefix(out, "MERGED"):
			fmt.Println("Flathub PR merged.")
			return nil
		case out == "OPEN CLEAN":
			if _, err := r.gh("pr", "merge", number, "--repo", flathubRepo, "--merge", "--delete-branch"); err == nil {
				fmt.Println("Flathub PR merged.")
				return nil
			}
		case strings.HasPrefix(out, "CLOSED"):
			return errors.New("the Flathub PR was closed")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the Flathub PR could not merge within 2 hours: %s", out)
		}
		fmt.Println("Flathub PR not mergeable yet:", out)
		time.Sleep(time.Minute)
	}
}

// notes prints the release notes the tag would get at target.
func (r *rufinRelease) notes(target string) error {
	if r.summary == "" {
		r.summary = tagSummary(r.root, r.version)
	}
	cmd := exec.Command("cargo", "run", "--locked", "-q", "-p", "xtask", "--", "release", "notes", "--target", target, r.version, r.summary)
	cmd.Dir, cmd.Stdout, cmd.Stderr = r.root, os.Stdout, os.Stderr
	return cmd.Run()
}

// tagSummary is the summary in an annotated tag's message: what precedes its changelog and
// signature.
func tagSummary(root, tag string) string {
	raw, err := exec.Command("git", "-C", root, "cat-file", "tag", "refs/tags/"+tag).Output()
	if err != nil {
		return ""
	}
	_, message, _ := strings.Cut(string(raw), "\n\n")
	var kept []string
	for _, line := range strings.Split(message, "\n") {
		if strings.HasPrefix(line, "-----BEGIN PGP SIGNATURE-----") || strings.HasPrefix(line, "## Changelog") {
			break
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func (r *rufinRelease) git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = r.root, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}

func (r *rufinRelease) gh(args ...string) (string, error) { return r.out("gh", args...) }

func (r *rufinRelease) out(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stderr = r.root, os.Stderr
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
