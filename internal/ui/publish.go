package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/screwys/git-gui/internal/client"
	"github.com/screwys/git-gui/internal/config"
	"github.com/screwys/git-gui/internal/daemon"
	"github.com/screwys/git-gui/internal/git"
)

const pendingKey = "rufin_publish_pending"

var (
	catalogIDs    = []string{"none", "include", "sync"}
	catalogLabels = []string{"Normal (Ignore changed .po catalogs)", "Include prepared .po files (--include-i18n-catalogs)",
		"Sync and merge all .po catalogs (--sync-i18n-catalogs)"}
	prURL         = regexp.MustCompile(`https://github\.com/[^/\s]+/[^/\s]+/pull/(\d+)`)
	publishedLine = regexp.MustCompile(`^Published PR #\d+: (\S+)$`)
)

// publishForm is the part of a saved publish the form shows; Resume needs the form unchanged.
type publishForm struct {
	Branch       string `json:"branch"`
	Base         string `json:"base"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	BodyModified bool   `json:"body_modified"`
	Phase        string `json:"phase"`
	AutoMerge    bool   `json:"auto_merge"`
	Wait         bool   `json:"wait"`
	Full         bool   `json:"full"`
	Instant      bool   `json:"instant"`
	I18n         string `json:"i18n"`
}

// pendingPublish is state.json's rufin_publish_pending: a publish until it completes or its PR
// ends, so a failed or interrupted one can resume after a restart.
type pendingPublish struct {
	RepoPath string `json:"repo_path"`
	publishForm
	Args       []string          `json:"args"`
	RetryArgs  []string          `json:"retry_args"` // without the title and paths: the commit exists
	Env        map[string]string `json:"env"`
	Paths      []string          `json:"paths"` // null means every uncommitted change
	HeadBefore string            `json:"head_before"`
	JobID      string            `json:"job_id,omitempty"`
	ChecksOnly bool              `json:"checks_only"` // the job is a scheduled publish's full checks
}

// PublishTab publishes a Rufin branch as a pull request through the publish helper, at once or
// after another PR merges, and shows the branch's PR and its checks.
type PublishTab struct {
	Root     *gtk.Paned
	repo     string
	files    *FileList
	diff     *DiffView
	term     *Terminal
	checks   *ChecksView
	bottom   *gtk.Notebook
	schedule *ScheduleFollower
	picker   *PRPicker

	branch, base, title, phase *gtk.Entry
	body                       *gtk.TextView
	prLabel, branchError       *gtk.Label
	autoMerge, waitCI, full    *gtk.CheckButton
	instant                    *gtk.CheckButton
	catalogs                   *gtk.DropDown
	banner                     *gtk.Box
	bannerText                 *gtk.Label

	working    []git.File // uncommitted changes, the selectable rows
	shown      []git.File // every row: the branch's changes since its base, then the rest
	left       string     // the comparison's base commit; "" when the branch comparison failed
	context    string     // the branch the banner, checks and body describe
	draftTitle string
	lookups    int // drops PR lookups that a newer one replaced
	stage      string
	published  int    // the PR the running job published
	partial    string // output after the last newline
	lastBranch string
	lastBase   string
	submitted  publishForm
}

// NewPublishTab builds the tab for the Rufin checkout at repo.
func NewPublishTab(repo string) *PublishTab {
	t := &PublishTab{repo: repo, files: NewFileList("Publish PR", true), diff: NewDiffView(repo),
		term: NewTerminal(repo, "publish", appConfig.PublishScript()), checks: NewChecksView(repo),
		schedule: NewScheduleFollower(repo, "publish"), picker: NewPRPicker(repo, "Publish after PR merges:")}
	left := gtk.NewBox(gtk.OrientationVertical, 8)
	margins(left, 8)
	left.Append(t.files.Root)
	t.branchError = gtk.NewLabel("Branch diff unavailable")
	t.branchError.AddCSSClass("deleted")
	t.branchError.SetXAlign(0)
	t.branchError.SetVisible(false)
	left.Append(t.branchError)

	branchGroup := group("Branch Options")
	t.branch = gtk.NewEntry()
	t.base = gtk.NewEntry()
	t.base.SetText(appConfig.DefaultBaseBranch)
	t.base.SetWidthChars(8)
	branchRow := row("Branch:", t.branch)
	branchRow.Append(gtk.NewLabel("Base:"))
	branchRow.Append(t.base)
	branchGroup.Append(branchRow)
	left.Append(branchGroup)

	details := group("Pull Request Details")
	t.prLabel = gtk.NewLabel("")
	t.prLabel.SetXAlign(0)
	t.prLabel.SetWrap(true)
	t.prLabel.SetVisible(false)
	t.title = gtk.NewEntry()
	t.title.SetPlaceholderText("e.g. ui/player: add a sleep timer")
	t.title.SetTooltipText("The first commit names the PR. Follow-up commits keep the PR title. Leave empty to retry existing commits.")
	t.phase = gtk.NewEntry()
	t.phase.SetPlaceholderText("Defaults to TITLE")
	t.phase.SetWidthChars(12)
	titleRow := row("Commit:", t.title)
	titleRow.Append(gtk.NewLabel("Phase:"))
	titleRow.Append(t.phase)
	t.body = gtk.NewTextView()
	t.body.SetWrapMode(gtk.WrapWordChar)
	t.body.SetTopMargin(6)
	t.body.SetLeftMargin(6)
	bodyScroll := gtk.NewScrolledWindow()
	bodyScroll.SetChild(t.body)
	bodyScroll.SetMinContentHeight(50)
	bodyScroll.AddCSSClass("card")
	// GtkTextView has no placeholder; a label shows through while the body is empty.
	placeholder := gtk.NewLabel("Description (optional markdown)...")
	placeholder.AddCSSClass("dim")
	placeholder.SetHAlign(gtk.AlignStart)
	placeholder.SetVAlign(gtk.AlignStart)
	placeholder.SetMarginStart(8)
	placeholder.SetMarginTop(6)
	placeholder.SetCanTarget(false)
	bodyBox := gtk.NewOverlay()
	bodyBox.SetChild(bodyScroll)
	bodyBox.AddOverlay(placeholder)
	details.Append(t.prLabel)
	details.Append(titleRow)
	details.Append(bodyBox)
	left.Append(details)

	options := group("Options")
	t.autoMerge = gtk.NewCheckButtonWithLabel("Squash auto-merge")
	t.autoMerge.SetActive(appConfig.DefaultAutoMerge)
	t.waitCI = gtk.NewCheckButtonWithLabel("Wait CI checks (--wait)")
	t.waitCI.SetActive(appConfig.DefaultWaitCI)
	t.full = gtk.NewCheckButtonWithLabel("Full checks (--full)")
	t.full.SetTooltipText("Run the whole pre-push gate instead of the focused checks")
	t.catalogs = gtk.NewDropDownFromStrings(catalogLabels)
	options.Append(t.autoMerge)
	options.Append(t.waitCI)
	t.instant = gtk.NewCheckButtonWithLabel("No checks (--instant)")
	t.instant.SetTooltipText("Commit and publish without formatting, generation or local checks")
	// The helper takes one check mode.
	for _, pair := range [][2]*gtk.CheckButton{{t.full, t.instant}, {t.instant, t.full}} {
		pair[0].ConnectToggled(func() {
			if pair[0].Active() {
				pair[1].SetActive(false)
			}
		})
	}
	options.Append(t.full)
	options.Append(t.instant)
	options.Append(t.picker.Root)
	options.Append(row("Catalogs:", t.catalogs))
	left.Append(options)

	right := gtk.NewBox(gtk.OrientationVertical, 6)
	right.SetMarginStart(8)
	right.SetMarginTop(8)
	right.SetMarginEnd(8)
	t.banner = gtk.NewBox(gtk.OrientationHorizontal, 6)
	t.banner.AddCSSClass("card")
	t.banner.SetVisible(false)
	t.bannerText = gtk.NewLabel("")
	t.bannerText.AddCSSClass("accent")
	t.bannerText.SetXAlign(0)
	t.bannerText.SetHExpand(true)
	t.bannerText.SetEllipsize(3)
	t.bannerText.SetMarginStart(8)
	viewChecks := gtk.NewButtonWithLabel("View Checks")
	viewChecks.AddCSSClass("flat")
	viewChecks.ConnectClicked(func() { t.bottom.SetCurrentPage(0) })
	openPR := gtk.NewButtonWithLabel("Open PR on GitHub")
	openPR.AddCSSClass("flat")
	openPR.ConnectClicked(func() { openURL(t.checks.URL) })
	t.banner.Append(t.bannerText)
	t.banner.Append(viewChecks)
	t.banner.Append(openPR)
	right.Append(t.banner)
	t.bottom = gtk.NewNotebook()
	t.bottom.AppendPage(t.checks.Root, gtk.NewLabel("PR Checks"))
	t.bottom.AppendPage(t.term.Root, gtk.NewLabel("Terminal Log"))
	t.term.OnShow = func() { t.bottom.SetCurrentPage(1) }
	rightSplit := gtk.NewPaned(gtk.OrientationVertical)
	rightSplit.SetStartChild(t.diff.Root)
	rightSplit.SetEndChild(t.bottom)
	rightSplit.SetPosition(340)
	rightSplit.SetVExpand(true)
	right.Append(rightSplit)
	t.Root = split(left, right)

	t.files.OnActivate = func(path string) { t.showFiles([]string{path}) }
	t.files.OnView = func(chosen []string) {
		if selectable := t.files.Selectable(); t.left != "" && len(chosen) == len(selectable) &&
			!slices.ContainsFunc(chosen, func(p string) bool { return !slices.Contains(selectable, p) }) {
			// Every uncommitted file checked previews the whole branch.
			chosen = paths(t.shown)
		}
		t.showFiles(chosen)
	}
	t.files.OnChecked = t.updateButton
	t.files.Button.ConnectClicked(t.clicked)
	t.diff.OnSaved = func(path string) { t.RefreshFiles(); t.showFiles([]string{path}) }
	whenEdited(t.branch, &t.lastBranch, t.Refresh)
	whenEdited(t.base, &t.lastBase, t.RefreshFiles)
	t.lastBase = strings.TrimSpace(t.base.Text())
	for _, entry := range []*gtk.Entry{t.branch, t.base, t.title, t.phase} {
		entry.ConnectChanged(t.updateButton)
	}
	t.body.Buffer().ConnectChanged(func() {
		placeholder.SetVisible(t.body.Buffer().CharCount() == 0)
		t.updateButton()
	})
	for _, check := range []*gtk.CheckButton{t.autoMerge, t.waitCI, t.full, t.instant} {
		check.ConnectToggled(t.updateButton)
	}
	t.catalogs.NotifyProperty("selected", t.updateButton)
	t.picker.OnChanged = t.updateButton
	t.schedule.OnChanged = t.scheduleChanged
	t.checks.OnChecks = func(number int, checks []daemon.Check) {
		if len(checks) > 0 && t.stage != "failed" && (!t.term.Runner.Running() || number == t.published) {
			t.showStage("remote", false)
		}
	}
	t.term.OnStarted = t.started
	t.term.OnRestored = t.restored
	t.term.OnFinished = t.finished
	t.term.OnOutput = t.output
	t.term.OnError = func(string) { t.showStage("failed", false) }
	t.term.OnCancel = func() {
		if t.schedule.Active() {
			t.cancelSchedule()
		} else {
			t.term.Runner.Stop()
		}
	}

	t.restoreForm()
	var saved trackedPR
	if config.Get("rufin_tracked_pr", &saved) {
		if number, err := strconv.Atoi(saved.Number); err == nil && number > 0 {
			t.showBanner(bannerTitle(number, saved.State, saved.Title), "")
			t.checks.SetPR(number, saved.Title, saved.URL, saved.State)
		}
	}
	// The saved PR stays until discovery for this branch replaces it.
	t.context = t.currentBranch()
	t.term.Attach()
	t.schedule.Restore()
	t.Refresh()
	return t
}

// whenEdited calls done when the user finishes editing entry, by Enter or by leaving it, with
// text that differs from last.
func whenEdited(entry *gtk.Entry, last *string, done func()) {
	finish := func() {
		if text := strings.TrimSpace(entry.Text()); text != *last {
			*last = text
			done()
		}
	}
	entry.ConnectActivate(finish)
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(finish)
	entry.AddController(focus)
}

// Refresh reloads the files, the branch's draft, its PR and the open PR list.
func (t *PublishTab) Refresh() {
	t.term.Runner.Program = appConfig.PublishScript()
	t.RefreshFiles()
	branch := t.currentBranch()
	t.lastBranch = strings.TrimSpace(t.branch.Text())
	if branch != t.context {
		t.context = branch
		t.clearPR()
		if !t.body.Buffer().Modified() {
			t.setBody("", false)
		}
	}
	title, body, hasBody := git.Draft(t.repo, branch)
	t.draftTitle = title
	t.setPRLabel(title)
	if hasBody && !t.body.Buffer().Modified() {
		t.setBody(body, false)
	}
	t.discover(branch)
	t.updateButton()
	t.picker.Refresh()
	t.term.Attach()
}

// RefreshFiles lists the branch's changes since its base, committed or not, and the uncommitted
// ones that can be checked.
func (t *PublishTab) RefreshFiles() {
	head := "HEAD"
	if git.Head(t.repo) == "" {
		head = git.EmptyTree
	}
	working, err := git.Compare(t.repo, head, git.WorkingTree, true)
	if err != nil {
		t.diff.ShowMessage("Status unavailable", err.Error())
		return
	}
	t.working, t.shown, t.left = working, working, ""
	base := t.baseBranch()
	if git.HasRef(t.repo, "origin/"+base) {
		base = "origin/" + base
	}
	left, err := git.MergeBase(t.repo, base, "HEAD")
	var branchFiles []git.File
	if err == nil {
		branchFiles, err = git.Compare(t.repo, left, git.WorkingTree, true)
	}
	if err == nil {
		// A file the branch changed and the working tree put back still counts as uncommitted.
		t.left, t.shown = left, branchFiles
		for _, f := range working {
			if !slices.ContainsFunc(branchFiles, func(b git.File) bool { return b.Path == f.Path }) {
				t.shown = append(t.shown, f)
			}
		}
		t.branchError.SetVisible(false)
	} else {
		t.branchError.SetTooltipText(err.Error())
		t.branchError.SetVisible(true)
	}
	t.files.SetFiles(t.shown, paths(working))
	if t.left == "" {
		t.files.Title.SetText(fmt.Sprintf("Uncommitted (%d)", len(t.shown)))
	}
	switch current := t.diff.CurrentPath(); {
	case current != "" && slices.Contains(paths(t.shown), current):
		t.showFiles([]string{current})
	case len(t.shown) > 0:
		t.showFiles(paths(t.shown))
	case t.left != "":
		t.diff.ShowMessage("No branch changes", "No branch changes")
	default:
		t.diff.ShowMessage("Clean working tree", "Clean working tree")
	}
	t.updateButton()
}

func paths(files []git.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

// showFiles previews files against the branch's base, or against HEAD without a comparison.
func (t *PublishTab) showFiles(chosenPaths []string) {
	chosen := pick(t.shown, chosenPaths)
	left := t.left
	if left == "" {
		left = "HEAD"
		if git.Head(t.repo) == "" {
			left = git.EmptyTree
		}
	}
	title := ""
	switch {
	case len(chosen) == 1:
		title = chosen[0].Path
	case t.left != "":
		title = fmt.Sprintf("Branch Changes (%d files)", len(chosen))
	case len(chosen) == 0:
		t.diff.ShowMessage("No changes selected", "")
		return
	case len(chosen) == len(t.shown):
		title = fmt.Sprintf("All Changes (%d files)", len(chosen))
	default:
		title = fmt.Sprintf("%d files selected", len(chosen))
	}
	text := ""
	if len(chosen) > 0 {
		var err error
		if text, err = git.Diff(t.repo, left, git.WorkingTree, chosen); err != nil {
			t.branchError.SetTooltipText(err.Error())
			t.branchError.SetVisible(t.left != "")
			t.diff.ShowMessage(title, err.Error())
			return
		}
	}
	if text == "" && t.left != "" {
		t.diff.ShowMessage(title, "No net branch changes")
		return
	}
	adds, dels := totals(chosen)
	path := ""
	if len(chosen) == 1 {
		path = chosen[0].Path
	}
	t.diff.ShowDiff(title, text, adds, dels, path)
}

// discover looks up the branch's PR; on the base branch, the user's latest PR.
func (t *PublishTab) discover(branch string) {
	onBase := branch == t.baseBranch()
	if onBase {
		t.branch.SetPlaceholderText("Derived from title (on main)")
	} else {
		t.branch.SetPlaceholderText("Current: " + branch)
	}
	t.lookups++
	lookup, repo := t.lookups, t.repo
	go func() {
		var prs []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			URL    string `json:"url"`
			State  string `json:"state"`
		}
		var err error
		if onBase {
			err = ghJSON(repo, 10*time.Second, &prs, "pr", "list", "--state", "all", "--author", "@me", "--limit", "1", "--json", "number,title,url,state")
			if err == nil && len(prs) == 0 {
				err = ghJSON(repo, 10*time.Second, &prs, "pr", "list", "--state", "all", "--limit", "1", "--json", "number,title,url,state")
			}
		} else {
			err = ghJSON(repo, 10*time.Second, &prs, "pr", "list", "--head", branch, "--state", "all", "--limit", "1", "--json", "number,title,body,url,state")
		}
		glib.IdleAdd(func() {
			if lookup != t.lookups || branch != t.context {
				return
			}
			switch {
			case err != nil:
				t.clearPR()
				t.showBanner("PR status unavailable", err.Error())
			case len(prs) == 0:
				t.clearPR()
			default:
				pr := prs[0]
				if p, ok := t.pending(); ok && (pr.State == "MERGED" || pr.State == "CLOSED") && p.Branch == branch &&
					!t.term.Runner.Running() && !t.schedule.Active() {
					// The saved publish ended with its PR; the form returns to new work.
					t.completePending(p)
					t.Refresh()
					return
				}
				// The base branch's latest PR is shown for its checks; it is not this branch's PR, so
				// it does not name the PR or fill the body.
				if !onBase {
					t.setPRLabel(pr.Title)
					if _, _, hasBody := git.Draft(t.repo, branch); !hasBody && !t.body.Buffer().Modified() {
						t.setBody(pr.Body, false)
					}
				}
				t.showBanner(bannerTitle(pr.Number, pr.State, pr.Title), "")
				t.checks.SetPR(pr.Number, pr.Title, pr.URL, pr.State)
			}
		})
	}()
}

func bannerTitle(number int, state, title string) string {
	text := fmt.Sprintf("Pull Request #%d", number)
	if state != "" {
		text += " (" + capitalize(state) + ")"
	}
	if title != "" {
		text += ": " + title
	}
	return text
}

func (t *PublishTab) showBanner(text, tooltip string) {
	if tooltip == "" {
		tooltip = text
	}
	t.bannerText.SetText(text)
	t.bannerText.SetTooltipText(tooltip)
	t.banner.SetVisible(true)
}

// clearPR forgets the branch's PR; a saved draft still names it.
func (t *PublishTab) clearPR() {
	t.banner.SetVisible(false)
	t.checks.Clear()
	t.setPRLabel(t.draftTitle)
}

func (t *PublishTab) setPRLabel(title string) {
	t.prLabel.SetText("PR: " + title)
	t.prLabel.SetVisible(title != "")
}

// prTitle is the PR's known title, or "".
func (t *PublishTab) prTitle() string {
	if !t.prLabel.Visible() {
		return ""
	}
	return strings.TrimPrefix(t.prLabel.Text(), "PR: ")
}

func (t *PublishTab) currentBranch() string {
	if branch := strings.TrimSpace(t.branch.Text()); branch != "" {
		return branch
	}
	return git.CurrentBranch(t.repo)
}

func (t *PublishTab) baseBranch() string {
	if base := strings.TrimSpace(t.base.Text()); base != "" {
		return base
	}
	return appConfig.DefaultBaseBranch
}

func (t *PublishTab) bodyText() string {
	buffer := t.body.Buffer()
	return buffer.Text(buffer.StartIter(), buffer.EndIter(), false)
}

// setBody fills the body; text the app loads counts as unedited, so it is not sent.
func (t *PublishTab) setBody(text string, modified bool) {
	t.body.Buffer().SetText(text)
	t.body.Buffer().SetModified(modified)
}

func (t *PublishTab) form() publishForm {
	return publishForm{
		Branch: strings.TrimSpace(t.branch.Text()), Base: strings.TrimSpace(t.base.Text()),
		Title: strings.TrimSpace(t.title.Text()), Body: t.bodyText(), BodyModified: t.body.Buffer().Modified(),
		Phase: strings.TrimSpace(t.phase.Text()), AutoMerge: t.autoMerge.Active(), Wait: t.waitCI.Active(),
		Full: t.full.Active(), Instant: t.instant.Active(), I18n: catalogIDs[min(int(t.catalogs.Selected()), len(catalogIDs)-1)],
	}
}

func (t *PublishTab) restoreForm() {
	p, ok := t.pending()
	if !ok {
		return
	}
	t.branch.SetText(p.Branch)
	if p.Base != "" {
		t.base.SetText(p.Base)
		t.lastBase = p.Base
	}
	t.title.SetText(p.Title)
	t.setBody(p.Body, p.BodyModified)
	t.phase.SetText(p.Phase)
	t.autoMerge.SetActive(p.AutoMerge)
	t.waitCI.SetActive(p.Wait)
	t.full.SetActive(p.Full)
	t.instant.SetActive(p.Instant)
	if i := slices.Index(catalogIDs, p.I18n); i >= 0 {
		t.catalogs.SetSelected(uint(i))
	}
}

func (t *PublishTab) pending() (pendingPublish, bool) {
	var p pendingPublish
	return p, config.Get(pendingKey, &p) && p.RepoPath == t.repo
}

// jobID names the slot's shown job the way rufin_publish_pending records it.
func (t *PublishTab) jobID() string {
	if t.term.Runner.Path == "" {
		return ""
	}
	return filepath.Base(filepath.Dir(t.term.Runner.Path))
}

// resumable reports a saved publish for the shown job that the form still describes.
func (t *PublishTab) resumable() (pendingPublish, bool) {
	p, ok := t.pending()
	return p, ok && p.JobID == t.jobID() && p.publishForm == t.form()
}

func (t *PublishTab) completePending(p pendingPublish) {
	config.Set(pendingKey, nil)
	if strings.TrimSpace(t.branch.Text()) == p.Branch {
		t.branch.SetText("")
	}
	if strings.TrimSpace(t.title.Text()) == p.Title {
		t.title.SetText("")
	}
	if t.bodyText() == p.Body {
		t.setBody("", false)
	}
}

func (t *PublishTab) updateButton() {
	b := t.files.Button
	b.SetSensitive(true)
	number, _ := t.picker.Number()
	_, resumable := t.resumable()
	switch {
	case t.schedule.Active():
		b.SetLabel(fmt.Sprintf("Cancel (PR #%d)", t.schedule.Schedule.Number))
	case t.picker.Waiting():
		b.SetLabel(fmt.Sprintf("Publish after PR #%d merges", number))
	case t.term.Runner.Running():
		b.SetLabel("Publishing…")
		b.SetSensitive(false)
	case resumable:
		b.SetLabel("Resume PR")
	default:
		b.SetLabel("Publish PR")
	}
}

// showStage picks the output page once per stage: the terminal for local work and failures, the
// checks for remote progress. A page the user picked stays until the stage changes.
func (t *PublishTab) showStage(stage string, force bool) {
	if force || stage != t.stage {
		t.stage = stage
		if stage == "remote" {
			t.bottom.SetCurrentPage(0)
		} else {
			t.bottom.SetCurrentPage(1)
		}
	}
}

func (t *PublishTab) clicked() {
	switch p, resumable := t.resumable(); {
	case t.schedule.Active():
		t.cancelSchedule()
	case resumable && !t.picker.Wait.Active():
		t.resume(p)
	default:
		t.publish()
	}
}

func (t *PublishTab) helper() (string, bool) {
	helper := appConfig.PublishScript()
	if _, err := os.Stat(helper); err != nil {
		alert(t.Root, "Script Not Found", fmt.Sprintf("Cannot find publish script at:\n%s\n\nYou can configure the script location in Settings.", helper))
		return "", false
	}
	return helper, true
}

func (t *PublishTab) publish() {
	number := 0
	if t.picker.Wait.Active() {
		n, ok := t.picker.Number()
		if !ok {
			alert(t.Root, "Missing PR Number", "Please enter or select a PR number to wait for, or uncheck 'Publish after PR merges'.")
			return
		}
		number = n
	}
	helper, ok := t.helper()
	if !ok {
		return
	}
	f := t.form()
	f.Base = t.baseBranch()
	checked := t.files.Checked()
	partial := len(checked) > 0 && len(checked) < len(t.working)
	if len(t.working) > 0 && f.Title == "" {
		alert(t.Root, "Commit Subject", "Enter a subject for the changes being committed.")
		return
	}
	if f.Branch == "" {
		cmd := exec.Command(helper, "--base", f.Base, "--print-branch", f.Title)
		cmd.Dir = t.repo
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			alert(t.Root, "Publish Branch", strings.TrimSpace(stderr.String()))
			return
		}
		f.Branch = strings.TrimSpace(string(out))
	}

	args := []string{"--base", f.Base}
	if f.Branch != "" {
		args = append(args, "--branch", f.Branch)
	}
	env := map[string]string{}
	if f.BodyModified {
		args = append(args, "--body", f.Body)
		env["RUFIN_ALLOW_PR_BODY"] = "1"
	}
	if f.Full {
		args = append(args, "--full")
	}
	if f.Instant {
		args = append(args, "--instant")
	}
	if f.Phase != "" {
		args = append(args, "--phase", f.Phase)
	}
	if !f.AutoMerge {
		args = append(args, "--no-auto-merge")
	}
	if f.Wait {
		args = append(args, "--wait")
	}
	switch f.I18n {
	case "include":
		args = append(args, "--include-i18n-catalogs")
	case "sync":
		args = append(args, "--sync-i18n-catalogs")
	}
	retry := slices.Clone(args)
	prTitle := t.prTitle()
	if f.Title != "" && (len(t.working) > 0 || prTitle == "") {
		args = append(args, f.Title)
	}
	var selected []string
	if partial {
		args = append(args, checked...)
		selected = checked
	}

	shownTitle, commit := f.Title, "Retry existing commits"
	if prTitle != "" {
		shownTitle = prTitle
	} else if shownTitle == "" {
		shownTitle = "(saved draft or existing PR)"
	}
	if len(t.working) > 0 {
		commit = f.Title
	}
	files := fmt.Sprintf("All %d files", len(t.working))
	if partial {
		files = fmt.Sprintf("%d of %d files", len(checked), len(t.working))
	}
	details := []detail{{"Branch Target", f.Branch + " → " + f.Base}, {"PR Title", shownTitle}, {"Commit", commit}, {"Files Included", files}}
	if number > 0 {
		details = append(details, detail{"Wait for PR", fmt.Sprintf("#%d (auto-publish when merged)", number)})
	}
	localChecks := "Focused"
	if f.Full {
		localChecks = "Full"
	} else if f.Instant {
		localChecks = "None"
	}
	squash := "Disabled"
	if f.AutoMerge {
		squash = "Enabled"
	}
	details = append(details, detail{"Squash Merge", squash}, detail{"Wait CI Checks", yesNo(f.Wait)}, detail{"Local Checks", localChecks})
	if f.I18n != "none" {
		details = append(details, detail{"I18n Catalogs", catalogLabels[slices.Index(catalogIDs, f.I18n)]})
	}
	confirm(t.Root, "Publish Pull Request", details, "PR Description Preview:", f.Body, "Publish PR", func() {
		t.branch.SetText(f.Branch)
		head, _ := git.Run(t.repo, "rev-parse", "HEAD")
		p := pendingPublish{RepoPath: t.repo, publishForm: f, Args: args, RetryArgs: retry, Env: env, Paths: selected, HeadBefore: head}
		if number > 0 {
			t.scheduleAfter(number, helper, p)
		} else {
			t.start(helper, p.Args, p)
		}
	})
}

// resume continues a saved publish, reusing its commit when the branch already has it.
func (t *PublishTab) resume(p pendingPublish) {
	if t.term.Runner.Running() {
		return
	}
	helper, ok := t.helper()
	if !ok {
		return
	}
	ref := "refs/heads/" + p.Branch
	head, _ := git.Run(t.repo, "rev-parse", "--verify", ref)
	committed := false
	if head != "" && p.Title != "" && head != p.HeadBefore {
		if _, err := git.Run(t.repo, "merge-base", "--is-ancestor", p.HeadBefore, head); err == nil {
			subjects, _ := git.Run(t.repo, "log", "--format=%s", p.HeadBefore+".."+head)
			committed = slices.Contains(strings.Split(subjects, "\n"), p.Title)
		} else {
			subject, _ := git.Run(t.repo, "log", "-1", "--format=%s", ref)
			committed = subject == p.Title
		}
	}
	args := p.Args
	if committed {
		args = p.RetryArgs
	}
	commit := p.Title
	switch {
	case slices.Equal(args, p.RetryArgs):
		commit = "Use the commit already on this branch"
	case commit == "":
		commit = "Saved changes"
	}
	branch := p.Branch
	if branch == "" {
		branch = git.CurrentBranch(t.repo)
	}
	preview := ""
	if p.BodyModified {
		preview = p.Body
	}
	details := []detail{{"Branch", branch}, {"Commit", commit}, {"Action", "Continue publishing this branch and run its checks again"}}
	confirm(t.Root, "Resume Pull Request", details, "PR Description Preview:", preview, "Resume PR", func() {
		t.start(helper, args, p)
	})
}

// start runs the helper now and saves the publish, so it can resume.
func (t *PublishTab) start(helper string, args []string, p pendingPublish) {
	t.banner.SetVisible(false)
	t.term.Clear()
	t.term.SetLabel("Publishing PR")
	t.submitted = p.publishForm
	p.JobID, p.ChecksOnly = "", false
	config.Set(pendingKey, p)
	t.term.Runner.Start(append([]string{helper}, args...), p.Env)
}

// scheduleAfter asks the daemon to publish once PR number merges, with the full checks first
// when they are on.
func (t *PublishTab) scheduleAfter(number int, helper string, p pendingPublish) {
	var publish *daemon.PublishCheck
	if p.Full {
		publish = &daemon.PublishCheck{Helper: helper, Branch: p.Branch, Title: p.Title, Paths: p.Paths, Args: p.Args, RetryArgs: p.RetryArgs}
	}
	job := client.Job(t.repo, "publish", append([]string{helper}, p.Args...), p.Env)
	t.term.Clear()
	t.showStage("local", true)
	if err := t.schedule.Submit(number, job, publish, nil); err != nil {
		t.term.Info("[!] " + err.Error())
		t.term.SetStatus("Not scheduled", "failed")
		return
	}
	config.Set(pendingKey, p)
	t.term.Info(fmt.Sprintf("[*] Publish scheduled: git-gui-daemon starts it once PR #%d merges.", number))
	t.updateButton()
}

func (t *PublishTab) cancelSchedule() {
	if err := t.schedule.Cancel(); err != nil {
		t.term.Info("[!] " + err.Error())
	}
}

// scheduleChanged follows a scheduled publish: its checks job, then the publish.
func (t *PublishTab) scheduleChanged(s daemon.Schedule) {
	t.picker.ShowSchedule(s)
	t.term.Info("[*] " + s.Message)
	if (s.Status == "checking" || s.Status == "started") && s.JobPath != "" {
		if p, ok := t.pending(); ok {
			p.JobID, p.ChecksOnly = filepath.Base(filepath.Dir(s.JobPath)), s.Status == "checking"
			config.Set(pendingKey, p)
		}
		if !t.term.Runner.Running() {
			t.term.SetLabel(s.Message)
			t.term.Attach()
		}
	}
	state := scheduleState(s.Status)
	t.term.SetStatus(s.Message, state)
	if state == "failed" {
		t.showStage("failed", false)
	}
	t.updateButton()
}

func (t *PublishTab) started() {
	t.published, t.partial = 0, ""
	t.showStage("local", true)
	if p, ok := t.pending(); ok {
		p.JobID = t.jobID()
		config.Set(pendingKey, p)
	}
	t.updateButton()
}

func (t *PublishTab) restored(record daemon.Record, running, _ bool) {
	if p, ok := t.pending(); ok && p.JobID == t.jobID() && !running && !p.ChecksOnly && record.ExitCode != nil && *record.ExitCode == 0 {
		// The publish completed while the window was closed.
		t.completePending(p)
		t.Refresh()
	}
	if running {
		t.showStage("local", false)
	}
	t.updateButton()
}

func (t *PublishTab) finished(code int, record daemon.Record) {
	p, ok := t.pending()
	if ok && p.ChecksOnly && p.JobID == t.jobID() {
		// The daemon decides what follows the checks; the schedule reports it.
		if code == 0 {
			t.term.SetStatus("Checks passed", "success")
		} else {
			t.term.SetStatus("Checks failed", "failed")
		}
		t.updateButton()
		return
	}
	if t.schedule.Active() {
		t.updateButton()
		return
	}
	if code != 0 || record.Stopped {
		t.showStage("failed", false)
	} else {
		t.checks.Refresh()
	}
	body := t.bodyText()
	_, draftBody, hasDraftBody := git.Draft(t.repo, t.currentBranch())
	if (code == 0 && body == t.submitted.Body) || (hasDraftBody && draftBody == body) {
		t.body.Buffer().SetModified(false)
	}
	if code == 0 {
		if t.submitted.Title == strings.TrimSpace(t.title.Text()) {
			t.title.SetText("")
		}
		if ok {
			t.completePending(p)
		}
		if body == t.submitted.Body {
			t.setBody("", false)
		}
	}
	t.Refresh()
}

// output follows the helper's live output: the branch it publishes and the PR it opens.
func (t *PublishTab) output(text string, replaying bool) {
	if replaying && !t.term.Runner.Running() {
		return
	}
	t.partial += text
	end := strings.LastIndexByte(t.partial, '\n')
	if end < 0 {
		return
	}
	lines := strings.Split(t.partial[:end], "\n")
	t.partial = t.partial[end+1:]
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if branch, ok := strings.CutPrefix(line, "Publishing branch: "); ok {
			t.publishingBranch(branch)
		}
		m := prURL.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		number, _ := strconv.Atoi(m[1])
		if published := publishedLine.FindStringSubmatch(line); line == m[0] || published != nil && published[1] == m[0] {
			t.published = number
		}
		t.showBanner(fmt.Sprintf("Pull Request #%d", number), "")
		title := t.prTitle()
		if title == "" {
			title = strings.TrimSpace(t.title.Text())
		}
		t.checks.SetPR(number, title, m[0], "OPEN")
	}
}

// publishingBranch records the branch the helper chose, so a resume targets it.
func (t *PublishTab) publishingBranch(branch string) {
	p, ok := t.pending()
	if !ok || p.JobID != t.jobID() {
		return
	}
	previous := p.Branch
	p.Branch = branch
	for _, args := range []*[]string{&p.Args, &p.RetryArgs} {
		if i := slices.Index(*args, "--branch"); i >= 0 && i+1 < len(*args) {
			(*args)[i+1] = branch
		} else {
			*args = append([]string{"--branch", branch}, *args...)
		}
	}
	config.Set(pendingKey, p)
	if strings.TrimSpace(t.branch.Text()) == previous {
		t.branch.SetText(branch)
	}
}
