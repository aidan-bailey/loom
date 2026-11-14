package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/watch"
)

// watchGitTimeout bounds the poll's one plain git read (origin's URL).
const watchGitTimeout = 10 * time.Second

// watchReadBudget bounds a poll job's gh reads together, as ghPollBudget
// bounds the GitHub poll: under watchInterval, so a poll cannot outlive its
// own cadence. The hook calls run after it, each under its own timeout.
const watchReadBudget = 45 * time.Second

// watchHookBudget bounds all of one poll's hook calls together, so a slow
// hook cannot hold a workspace's poll for cap times its timeout. A variable
// only so a test can spend it at once.
var watchHookBudget = 5 * time.Minute

// watchViewLimit is how many issues a poll views through gh besides the
// labelled ones: those a watcher session names first, then the lowest-numbered
// of those an open or an unsettled closed watcher PR names; the rest wait for
// a later tick (spec 7.1).
const watchViewLimit = 10

// watchFileLimit is the largest prompt template a poll reads from origin.
const watchFileLimit = 64 << 10

// watchEpoch is when this process started. The model sees no status
// transition made before it, so a session's StatusSince is zero until it
// changes status; a zero Since would make every session look stuck since the
// year 1, so Since is floored here and "stuck" counts from the daemon's start.
var watchEpoch = time.Now()

// watchPollRequest is what a poll job works from, copied on the loop
// (watchRequest): the job touches no model state.
type watchPollRequest struct {
	// ws is the workspace the poll is for: identity only, the job reads
	// nothing through it.
	ws  *Workspace
	dry bool
	req ReqID
	// name is the workspace's registered name.
	name string
	// repo is the repository's path, configDir the workspace's config dir
	// (the memory file and the watcher checkout live under it).
	repo      string
	configDir string
	// configured is the workspace's configured base branch, "" for the
	// repository's default; prefix its branch prefix.
	configured string
	prefix     string
	// viewer and slug are the cached gh login and owner/name, "" before the
	// first read.
	viewer string
	slug   string
	// loadMem asks the job to read the memory file: it is not loaded yet.
	loadMem bool
	// sessions are the workspace's sessions as of dispatch.
	sessions []watch.Session
	// settled holds the numbers of the PRs memory has settled, as of
	// dispatch; nil before the memory is loaded (the poll's own read then
	// stands in, loadMem).
	settled map[int]bool
}

// watchResult is one poll's reads, delivered to deliverWatch. err is why
// nothing can be decided from them ("" when every read held); the fields
// before snap are facts the loop keeps whether or not a decision follows.
type watchResult struct {
	ws  *Workspace
	dry bool
	req ReqID

	err string

	viewer   string
	slug     string
	memPath  string
	checkout string
	// mem is the memory file's contents, nil when this poll did not load it.
	mem *watch.Memory
	// base, baseSHA, startOK and startWhy are the fetch's outcome: baseSHA is
	// "" and startOK false when the fetch or the checkout refresh failed,
	// startWhy saying which.
	base     string
	baseSHA  string
	startOK  bool
	startWhy string
	// cfg, tmpl, prompt and resumePrompt are the config and prompts read from
	// origin; cfgOK is set when all of them read and parsed.
	cfg          watch.Config
	tmpl         watch.TitleTemplate
	prompt       string
	resumePrompt string
	cfgOK        bool

	// snap is the gh reads and the hook's answers; Now and Sessions are
	// filled in on the loop (decideWatch).
	snap watch.Snapshot
}

// watchExec is the executor the watcher runs git and gh with: the test seam
// when set (Options.GHExec), the production executor otherwise.
func (m *Model) watchExec() internalexec.Executor {
	if m.ghExec != nil {
		return m.ghExec
	}
	return internalexec.Default{}
}

// watchSessions lists ws's sessions for the watcher, the workspace terminal
// left out. Loop-goroutine only.
func (m *Model) watchSessions(ws *Workspace) []watch.Session {
	var out []watch.Session
	for _, inst := range ws.insts {
		if inst.IsWorkspaceTerminal {
			continue
		}
		since := inst.StatusSince()
		if since.Before(watchEpoch) {
			since = watchEpoch
		}
		out = append(out, watch.Session{
			ID:     uint64(m.idOf(inst)),
			Title:  inst.Title,
			Status: inst.GetStatus().String(),
			Since:  since,
			Branch: inst.GetBranch(),
			Issue:  inst.IssueNumber(),
		})
	}
	return out
}

// watchRequest copies what a poll job for ws needs. ws must have a
// repository. Loop-goroutine only.
func (m *Model) watchRequest(ws *Workspace, dry bool, req ReqID) watchPollRequest {
	st := m.watchFor(ws)
	rq := watchPollRequest{
		ws:        ws,
		dry:       dry,
		req:       req,
		name:      ws.Name(),
		repo:      ws.ctx.RepoPath,
		configDir: ws.ctx.ConfigDir,
		viewer:    st.viewer,
		slug:      st.repo,
		loadMem:   !st.memLoaded,
		sessions:  m.watchSessions(ws),
	}
	if st.memLoaded {
		for n, p := range st.mem.PRs {
			if p.Settled {
				if rq.settled == nil {
					rq.settled = map[int]bool{}
				}
				rq.settled[n] = true
			}
		}
	}
	if ws.cfg != nil {
		rq.configured = ws.cfg.GetBaseBranch()
		rq.prefix = ws.cfg.GetBranchPrefix()
	}
	return rq
}

// watchPollJob runs one poll for rq: git, then gh, then the hook. Pure
// I/O; returns a single watchResult.
func watchPollJob(rq watchPollRequest, r internalexec.Executor) Job {
	return func() any {
		p := &watchPoll{rq: rq, r: r}
		return p.run()
	}
}

// watchPoll is one poll job's working state.
type watchPoll struct {
	rq  watchPollRequest
	r   internalexec.Executor
	res watchResult
	// rev is what the config and prompts are read at: the fetched commit,
	// or origin/<base> as it stands when the fetch failed.
	rev string
}

func (p *watchPoll) run() watchResult {
	p.res = watchResult{
		ws:       p.rq.ws,
		dry:      p.rq.dry,
		req:      p.rq.req,
		viewer:   p.rq.viewer,
		slug:     p.rq.slug,
		memPath:  filepath.Join(p.rq.configDir, watch.MemoryFile),
		checkout: filepath.Join(p.rq.configDir, watch.CheckoutDir),
	}
	if err := p.readGit(); err != nil {
		return p.fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), watchReadBudget)
	defer cancel()
	if err := p.readGitHub(ctx); err != nil {
		return p.fail(err)
	}
	p.runHooks()
	return p.res
}

// fail ends the poll: nothing is decided from what it read.
func (p *watchPoll) fail(err error) watchResult {
	p.res.err = err.Error()
	return p.res
}

// readGit is the poll's git half, in order: the base and its checkout, the
// config and prompts, the memory, origin's slug. The first fault ends it.
func (p *watchPoll) readGit() error {
	if err := p.syncBase(); err != nil {
		return err
	}
	if err := p.loadConfig(); err != nil {
		return err
	}
	if err := p.loadMemory(); err != nil {
		return err
	}
	return p.loadSlug()
}

// syncBase resolves the base branch, fetches it and refreshes the watcher
// checkout at the fetched commit. A failed fetch or refresh is not a fault:
// it only forbids a start this tick (spec 7.1), and the config is then read
// at origin/<base> as it stands.
func (p *watchPoll) syncBase() error {
	base := p.rq.configured
	if base == "" {
		_, name, err := git.ResolveBaseCommit(p.rq.repo, "", p.r)
		if err != nil {
			return fmt.Errorf("base branch: %w", err)
		}
		base = strings.TrimPrefix(name, "origin/")
		if base == "HEAD" {
			return errors.New("base branch: none found; set base_branch")
		}
	}
	p.res.base = base
	p.rev = "origin/" + base
	sha, err := git.FetchBranch(p.rq.repo, base, p.r)
	if err != nil {
		p.res.startWhy = fmt.Sprintf("git fetch origin %s failed: %v", base, err)
		return nil
	}
	p.res.baseSHA = sha
	p.rev = sha
	if err := git.SyncDetachedWorktree(p.rq.repo, p.res.checkout, sha, p.r); err != nil {
		p.res.startWhy = fmt.Sprintf("the watcher checkout could not be refreshed: %v", err)
		return nil
	}
	p.res.startOK = true
	return nil
}

// loadConfig reads and parses loom-watch.json and the two prompt templates
// at p.rev.
func (p *watchPoll) loadConfig() error {
	data, err := git.ShowFile(p.rq.repo, p.rev, watch.ConfigFile, p.r)
	switch {
	case errors.Is(err, git.ErrNoSuchPath):
		return fmt.Errorf("no %s on origin/%s", watch.ConfigFile, p.res.base)
	case err != nil:
		return fmt.Errorf("read %s on origin/%s: %w", watch.ConfigFile, p.res.base, err)
	}
	cfg, err := watch.ParseConfig(data)
	if err != nil {
		return fmt.Errorf("%s: %w", watch.ConfigFile, err)
	}
	tmpl, err := watch.ParseTitleTemplate(cfg.Title)
	if err != nil {
		return fmt.Errorf("%s: title: %w", watch.ConfigFile, err)
	}
	if vars := tmpl.Vars(); len(vars) > 0 && !hasHook(cfg) {
		return fmt.Errorf("%s: title uses {%s} but the config names no hook to supply it", watch.ConfigFile, vars[0])
	}
	prompt, err := p.readPrompt(cfg.Session.Prompt)
	if err != nil {
		return err
	}
	resume := prompt
	if cfg.Session.ResumePrompt != cfg.Session.Prompt {
		if resume, err = p.readPrompt(cfg.Session.ResumePrompt); err != nil {
			return err
		}
	}
	p.res.cfg, p.res.tmpl = cfg, tmpl
	p.res.prompt, p.res.resumePrompt = prompt, resume
	p.res.cfgOK = true
	return nil
}

// readPrompt reads one prompt template at p.rev, at most watchFileLimit.
func (p *watchPoll) readPrompt(path string) (string, error) {
	data, err := git.ShowFile(p.rq.repo, p.rev, path, p.r)
	switch {
	case errors.Is(err, git.ErrNoSuchPath):
		return "", fmt.Errorf("prompt %s: not on origin/%s", path, p.res.base)
	case err != nil:
		return "", fmt.Errorf("prompt %s: %w", path, err)
	case len(data) > watchFileLimit:
		return "", fmt.Errorf("prompt %s: %d bytes is over the 64 KiB limit", path, len(data))
	}
	return string(data), nil
}

// loadMemory reads the memory file when the loop has not loaded it yet. An
// unreadable, undecodable or newer file is a fault and is left as it is:
// acting without the counts would re-nudge and re-escalate.
func (p *watchPoll) loadMemory() error {
	if !p.rq.loadMem {
		return nil
	}
	mem, err := watch.LoadMemory(p.res.memPath)
	if err != nil {
		return fmt.Errorf("memory %s: %w", p.res.memPath, err)
	}
	p.res.mem = &mem
	return nil
}

// hasHook reports whether cfg names a hook command.
func hasHook(cfg watch.Config) bool {
	return cfg.Hook != nil && len(cfg.Hook.Command) > 0
}

// loadSlug reads origin's owner/name, which only a hook needs (its
// LOOM_WATCH_REPO), once.
func (p *watchPoll) loadSlug() error {
	if p.res.slug != "" || !hasHook(p.res.cfg) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), watchGitTimeout)
	defer cancel()
	out, err := p.r.Output(internalexec.GitCommand(ctx, p.rq.repo, "remote", "get-url", "origin"))
	if err != nil {
		return fmt.Errorf("origin remote: %w", err)
	}
	url := strings.TrimSpace(string(out))
	slug := repoSlug(url)
	if slug == "" {
		return fmt.Errorf("origin remote %q does not name owner/name", url)
	}
	p.res.slug = slug
	return nil
}

// repoSlug returns the last two path parts of a remote URL, without a
// trailing ".git": owner/name for "git@github.com:owner/name.git" and
// "https://github.com/owner/name". "" when the URL has fewer than two.
func repoSlug(remote string) string {
	remote = strings.TrimSuffix(strings.TrimSuffix(remote, "/"), ".git")
	parts := strings.FieldsFunc(remote, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

// ownPR reports whether pr is the watcher's PR for issue n (spec §2: all
// four terms, decided by watch.OwnPR).
func (p *watchPoll) ownPR(pr github.WatchPR, n int) bool {
	return watch.OwnPR(pr, n, p.res.viewer, p.res.tmpl.BranchPattern(p.rq.prefix, n))
}

// readGitHub is the poll's gh half: the viewer (once), the PRs, the
// labelled issues, and the views of the issues the watcher's sessions and
// live PRs name (viewNamed). The first failed read ends the poll: ownership or
// the issue set is unknown, so nothing is decided.
func (p *watchPoll) readGitHub(ctx context.Context) error {
	if p.res.viewer == "" {
		viewer, err := github.Viewer(ctx, p.rq.repo, p.r)
		if err != nil {
			return fmt.Errorf("gh viewer: %w", err)
		}
		p.res.viewer = strings.TrimSpace(viewer)
	}
	prs, err := github.WatchPRs(ctx, p.rq.repo, p.r)
	if err != nil {
		return fmt.Errorf("gh pr list: %w", err)
	}
	issues, err := github.WatchIssues(ctx, p.rq.repo, p.res.cfg.Label, p.r)
	if err != nil {
		return fmt.Errorf("gh issue list: %w", err)
	}
	snap := &p.res.snap
	snap.Viewer, snap.Prefix, snap.PRs = p.res.viewer, p.rq.prefix, prs
	if len(issues) > github.WatchIssueLimit {
		issues = issues[:github.WatchIssueLimit]
		snap.Unconsidered = 1
	}
	snap.Considered = len(issues)
	all := append(slices.Clone(issues), p.viewNamed(ctx, prs, issues)...)
	slices.SortFunc(all, func(a, b github.WatchIssue) int { return cmp.Compare(a.Number, b.Number) })
	snap.Issues = all
	return nil
}

// prSettled reports whether memory holds PR number as settled: the memory
// this poll read, else the loop's as of dispatch.
func (p *watchPoll) prSettled(number int) bool {
	if p.res.mem != nil {
		return p.res.mem.PRs[number].Settled
	}
	return p.rq.settled[number]
}

// viewNamed views the issues the label query did not return that still have
// something to act on (spec 4.1): one a watcher session names, one an open
// watcher PR names, or one a closed-unmerged watcher PR not yet settled in
// memory names. An issue named only by merged or settled PRs has nothing left
// and is not viewed, so old merged PRs never crowd out a newer issue. At most
// watchViewLimit: issues with a watcher session first, then lowest number
// first. A failed view skips that issue for this tick.
func (p *watchPoll) viewNamed(ctx context.Context, prs []github.WatchPR, have []github.WatchIssue) []github.WatchIssue {
	listed := map[int]bool{}
	for _, is := range have {
		listed[is.Number] = true
	}
	withSession := map[int]bool{}
	named := map[int]bool{}
	for _, s := range p.rq.sessions {
		if s.Issue > 0 && watch.OwnSession(s, p.res.tmpl) {
			withSession[s.Issue] = true
			named[s.Issue] = true
		}
	}
	for _, pr := range prs {
		live := pr.State == "OPEN" || (pr.State == "CLOSED" && !p.prSettled(pr.Number))
		if !live {
			continue
		}
		for _, n := range pr.Closes {
			if p.ownPR(pr, n) {
				named[n] = true
			}
		}
	}
	order := slices.Sorted(maps.Keys(named))
	slices.SortStableFunc(order, func(a, b int) int {
		switch {
		case withSession[a] && !withSession[b]:
			return -1
		case !withSession[a] && withSession[b]:
			return 1
		}
		return cmp.Compare(a, b)
	})

	var out []github.WatchIssue
	views := 0
	for _, n := range order {
		if listed[n] {
			continue
		}
		if views == watchViewLimit {
			break
		}
		views++
		is, err := github.ViewWatchIssue(ctx, p.rq.repo, n, p.r)
		if err != nil {
			log.For("watch").Debug("issue_view_failed", "workspace", p.rq.name, "issue", n, "err", err.Error())
			continue
		}
		out = append(out, is)
	}
	return out
}

// runHooks calls the hook's nudges point once for each issue whose watcher
// session is Ready and which has an open watcher PR, in issue order, all
// under watchHookBudget. A failure is that issue's HookFailed and gives it
// no nudge this tick; nothing else changes.
func (p *watchPoll) runHooks() {
	cfg := p.res.cfg
	if !hasHook(cfg) {
		return
	}
	ordered := slices.Clone(p.rq.sessions)
	slices.SortFunc(ordered, func(a, b watch.Session) int { return cmp.Compare(a.ID, b.ID) })
	var sessions []watch.HookSession
	ready := map[int]watch.HookSession{}
	for _, s := range ordered {
		if s.Issue <= 0 || !watch.OwnSession(s, p.res.tmpl) {
			continue
		}
		_, vars, _ := p.res.tmpl.Parse(s.Title)
		hs := watch.HookSession{Title: s.Title, Issue: s.Issue, Branch: s.Branch, Status: s.Status, Vars: vars}
		sessions = append(sessions, hs)
		if _, ok := ready[s.Issue]; !ok && s.Status == session.Ready.String() {
			ready[s.Issue] = hs
		}
	}
	var prs []watch.HookPR
	open := map[int]github.WatchPR{}
	for _, pr := range p.res.snap.PRs {
		if pr.State != "OPEN" {
			continue
		}
		for _, n := range pr.Closes {
			if !p.ownPR(pr, n) {
				continue
			}
			prs = append(prs, watch.HookPR{Number: pr.Number, Issue: n, Branch: pr.HeadRefName, Body: pr.Body})
			if cur, ok := open[n]; !ok || pr.Number < cur.Number {
				open[n] = pr
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), watchHookBudget)
	defer cancel()
	snap := &p.res.snap
	for _, is := range snap.Issues {
		hs, haveSession := ready[is.Number]
		pr, havePR := open[is.Number]
		if is.Closed || !haveSession || !havePR {
			continue
		}
		if ctx.Err() != nil {
			p.hookFailed(is.Number, "the hook's time budget for this tick is spent")
			continue
		}
		in := watch.HookInput{
			Workspace: p.rq.name,
			Repo:      p.res.slug,
			Base:      p.res.base,
			BaseSHA:   p.res.baseSHA,
			Issue:     is,
			PR:        &watch.HookPR{Number: pr.Number, Issue: is.Number, Branch: pr.HeadRefName, Body: pr.Body},
			Sessions:  sessions,
			PRs:       prs,
			Session:   &hs,
			HeadSHA:   pr.HeadSHA,
		}
		nudges, err := watch.RunNudges(ctx, *cfg.Hook, p.res.checkout, nil, in)
		if err != nil {
			p.hookFailed(is.Number, err.Error())
			continue
		}
		if len(nudges) > 0 {
			if snap.HookNudges == nil {
				snap.HookNudges = map[int][]watch.Nudge{}
			}
			snap.HookNudges[is.Number] = nudges
		}
	}
}

// hookFailed records why issue got no hook nudges this tick.
func (p *watchPoll) hookFailed(issue int, why string) {
	if p.res.snap.HookFailed == nil {
		p.res.snap.HookFailed = map[int]string{}
	}
	p.res.snap.HookFailed[issue] = why
}
