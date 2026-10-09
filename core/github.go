package core

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
)

// ghInterval is the GitHub poller's own cadence. It rides the health
// tick but dispatches at most this often: each poll is two gh
// subprocesses plus a git fetch per open repo, all network-bound.
const ghInterval = 60 * time.Second

// ghPollBudget caps one entire poll — every repo, every gh call and
// fetch within it. github.Query budgets each gh subprocess separately
// (2 list calls plus one issue view per linked closed issue), so
// without an overall ceiling a repo with many linked issues could run
// far past ghInterval and stall every other workspace's refresh.
// Deliberately under ghInterval so a poll cannot outlive its own cadence.
const ghPollBudget = 45 * time.Second

// ghRecheckInterval is how often a poll re-runs github.CheckCLI after
// gh has reported unavailable. Re-probing a missing or unauthenticated
// gh every minute is wasteful, but never re-probing means a user who
// runs `gh auth login` after starting loom has GitHub state dead for
// the whole session.
const ghRecheckInterval = 5 * time.Minute

// ghWatchTTL is how long a repository a client asked the poll to cover
// (WatchGitHub) stays polled after the last ask: the issue picker asks on
// every open, so a picker in use keeps its repository polled, while one no
// client has asked about since stops costing gh calls every ghInterval for
// the rest of the daemon's life.
const ghWatchTTL = 15 * time.Minute

// ghWatch is a repository a client asked the poll to cover, and when one
// last asked.
type ghWatch struct {
	repo string
	at   time.Time
}

// ghAvailability is the cached result of github.CheckCLI. checkedAt
// anchors the ghRecheckInterval backoff on an unavailable result.
type ghAvailability struct {
	checked   bool
	ok        bool
	reason    string
	checkedAt time.Time
}

// ghResult carries one poll's result for every open repo, each keyed by
// its repository's canonical top level (ghRepoKey). errs holds repos
// whose query failed; they are dropped from ghState so a stale snapshot
// never keeps rendering. bases is the resolved base ref per repo, for
// parity. aliases maps each polled path (canonical) that lies inside
// another top level, a subdirectory of a repository, to that top level.
type ghResult struct {
	available ghAvailability
	snapshots map[string]github.Snapshot
	errs      map[string]error
	bases     map[string]string
	aliases   map[string]string
}

// ghPollRequest is the main-goroutine snapshot the poll Job works
// from. Everything it needs is copied here so the Job body touches no
// model state.
type ghPollRequest struct {
	repos  []string
	linked map[string][]int
	// configured is each repo's own config.BaseBranch. Keyed per repo
	// because every workspace has its own config.json — m.appConfig() is
	// whichever slot is focused, not a shared primary, so one string
	// applied across the batch would resolve non-focused repos against
	// the wrong workspace's setting.
	configured map[string]string
	check      bool // run CheckCLI first
}

// openedRepo is an opened workspace's repository and its own configured
// base branch.
type openedRepo struct{ path, base string }

// openedRepos are the repositories of every opened workspace (one a client
// has shown since the model started), deduplicated, each with its own
// workspace's config.BaseBranch: every workspace has its own config.json,
// so one setting applied across the batch would resolve the others' repos
// against the wrong workspace's. An opened workspace with no repository
// (the global one) stands for the repositories its sessions run in
// (Instance.Path), each against that workspace's own base branch, and with
// no session it polls nothing. It no longer stands for the directory loom
// started in: the model runs in the daemon (stage 3B), whose working
// directory is wherever some client spawned it, which names nothing a
// client shows. The workspaces with a repository of their own come first,
// in the order the model serves them, then the global workspace's: a global
// session in a registered repository names that repository too (compared
// canonically), and the repository's own workspace, with its base branch,
// must win. Last come the repositories clients asked for within
// ghWatchTTL (WatchGitHub), against the global workspace's base branch, as
// the directory loom started in was before the daemon.
func (m *Model) openedRepos() []openedRepo {
	seen := map[string]bool{}
	var out []openedRepo
	add := func(ws *Workspace, repo string) {
		key := canonicalDir(repo)
		if repo == "" || seen[key] {
			return
		}
		seen[key] = true
		base := ""
		if ws != nil && ws.cfg != nil {
			base = ws.cfg.GetBaseBranch()
		}
		out = append(out, openedRepo{path: repo, base: base})
	}
	var global *Workspace
	for _, ws := range m.workspaces {
		if ws.opened && ws.ctx != nil && ws.ctx.RepoPath != "" {
			add(ws, ws.ctx.RepoPath)
		}
	}
	for _, ws := range m.workspaces {
		if ws.ctx == nil || ws.ctx.RepoPath == "" {
			if global == nil {
				global = ws
			}
			if ws.opened {
				for _, inst := range ws.insts {
					add(ws, inst.Path)
				}
			}
		}
	}
	for _, w := range m.ghWatched {
		if time.Since(w.at) < ghWatchTTL {
			add(global, w.repo)
		}
	}
	return out
}

// openRepoPaths lists the repository of every opened workspace
// (openedRepos).
func (m *Model) openRepoPaths() []string {
	var out []string
	for _, r := range m.openedRepos() {
		out = append(out, r.path)
	}
	return out
}

// baseBranchByRepo maps each opened repository to its own workspace's
// configured base branch (openedRepos).
func (m *Model) baseBranchByRepo() map[string]string {
	out := map[string]string{}
	for _, r := range m.openedRepos() {
		out[r.path] = r.base
	}
	return out
}

// linkedIssues lists the non-zero issue numbers of instances in repo,
// however either path is spelled (compared canonically).
func (m *Model) linkedIssues(repo string) []int {
	key := canonicalDir(repo)
	var out []int
	for _, inst := range m.allInstances() {
		if inst.IssueNumber() != 0 && canonicalDir(inst.Path) == key {
			out = append(out, inst.IssueNumber())
		}
	}
	return out
}

// ghRepoKey is the key path's GitHub state is kept under: its canonical
// spelling (canonicalDir), or, when the last poll found that inside
// another repository's top level (aliases: a subdirectory of it), that
// top level's. So a session or a client reaching a polled repository
// through a symlink, or from a subdirectory, finds its state.
func ghRepoKey(aliases map[string]string, path string) string {
	key := canonicalDir(path)
	if root, ok := aliases[key]; ok {
		return root
	}
	return key
}

// canonicalKeys re-keys a poll's per-repo map by canonical path
// (canonicalDir), which a poll's own keys already are.
func canonicalKeys[V any](in map[string]V) map[string]V {
	if in == nil {
		return nil
	}
	out := make(map[string]V, len(in))
	for k, v := range in {
		out[canonicalDir(k)] = v
	}
	return out
}

// maybeGHQuery dispatches a poll when one is due: gh not known
// unavailable, and gateGH due (none in flight, and ghInterval since the
// last dispatch). Loop goroutine only.
func (m *Model) maybeGHQuery() bool {
	// A gh that reported unavailable is re-probed on ghRecheckInterval
	// rather than never again.
	if m.ghAvailable.checked && !m.ghAvailable.ok && time.Since(m.ghAvailable.checkedAt) < ghRecheckInterval {
		return false
	}
	return m.dispatchGated(gateGH, time.Now(), func() Job {
		repos := m.openRepoPaths()
		if len(repos) == 0 {
			return nil
		}
		req := ghPollRequest{
			repos:      repos,
			linked:     map[string][]int{},
			configured: m.baseBranchByRepo(),
			check:      !m.ghAvailable.checked || !m.ghAvailable.ok,
		}
		for _, r := range repos {
			req.linked[r] = m.linkedIssues(r)
		}
		return ghPollJob(req, internalexec.Default{})
	})
}

// ghPollJob runs one poll: an optional CLI check, then per repository a
// base resolve + fetch (always, even without gh) and the gh query (only
// when gh is available). The paths asked for are grouped by their
// checkout's top level (git.RepoRoot, canonical; a path git can't place
// stands for itself), so a repository reached through a symlink or from a
// subdirectory is polled once, under its top level, against the first
// path's configured base branch, for the issues every path links. Pure
// I/O; returns a single result. The whole poll is bounded by ghPollBudget
// so a repo with many linked closed issues cannot stall every other
// workspace's refresh.
func ghPollJob(req ghPollRequest, r internalexec.Executor) Job {
	return func() any {
		ctx, cancel := context.WithTimeout(context.Background(), ghPollBudget)
		defer cancel()
		msg := ghResult{snapshots: map[string]github.Snapshot{}, errs: map[string]error{}, bases: map[string]string{}, aliases: map[string]string{}}
		msg.available = ghAvailability{checked: true, ok: true, checkedAt: time.Now()}
		if req.check {
			if err := github.CheckCLI(r); err != nil {
				msg.available = ghAvailability{checked: true, ok: false, reason: err.Error(), checkedAt: time.Now()}
			}
		}
		type polled struct {
			configured string
			linked     []int
		}
		var roots []string
		byRoot := map[string]*polled{}
		for _, repo := range req.repos {
			key := canonicalDir(repo)
			root := key
			if top, err := git.RepoRoot(repo, r); err == nil && top != "" {
				root = canonicalDir(top)
			}
			if root != key {
				msg.aliases[key] = root
			}
			p, ok := byRoot[root]
			if !ok {
				p = &polled{configured: req.configured[repo]}
				byRoot[root] = p
				roots = append(roots, root)
			}
			for _, n := range req.linked[repo] {
				if !slices.Contains(p.linked, n) {
					p.linked = append(p.linked, n)
				}
			}
		}
		for _, repo := range roots {
			p := byRoot[repo]
			if _, name, err := git.ResolveBaseCommit(repo, p.configured, r); err == nil {
				msg.bases[repo] = name
				if ferr := git.FetchRef(repo, name, r); ferr != nil {
					log.For("github").Debug("base.fetch_failed", "repo", repo, "err", ferr.Error())
				}
			}
			if !msg.available.ok {
				continue
			}
			snap, err := github.Query(ctx, repo, p.linked, r)
			if err != nil {
				msg.errs[repo] = err
				continue
			}
			msg.snapshots[repo] = snap
		}
		return msg
	}
}

// deliverGH applies a poll result: replaces ghState wholesale and
// re-joins every instance. It is the one place the GitHub state changes,
// so it bumps ghGen, which republishes GitHubChanged.
func (m *Model) deliverGH(msg ghResult) {
	m.ghAvailable = msg.available
	for repo, err := range msg.errs {
		log.For("github").Debug("query_failed", "repo", repo, "err", err.Error())
	}
	m.ghState = canonicalKeys(msg.snapshots)
	m.ghErrs = canonicalKeys(msg.errs)
	m.ghAliases = canonicalKeys(msg.aliases)
	if msg.bases != nil {
		m.ghBases = canonicalKeys(msg.bases)
	}
	m.applyGitHubState()
	m.ghGen++
}

// baseFor returns the resolved base ref for repo, or "" before the
// first poll resolved it (parity then stays unknown).
func (m *Model) baseFor(repo string) string {
	return m.ghBases[ghRepoKey(m.ghAliases, repo)]
}

// applyGitHubState joins ghState onto every instance, by its path's key
// (ghRepoKey). Cheap and pure, so it also runs when a link is set outside
// a poll (issue pick).
func (m *Model) applyGitHubState() {
	for _, inst := range m.allInstances() {
		snap, known := m.ghState[ghRepoKey(m.ghAliases, inst.Path)]
		inst.SetGitHubState(github.StateFor(snap, known, inst.GetBranch(), inst.IssueNumber()))
	}
}

// GitHubSnapshot returns the latest poll's snapshot of repo, if the last
// poll of it succeeded; repo may be any spelling of it (ghRepoKey).
func (m *Model) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	s, ok := m.ghState[ghRepoKey(m.ghAliases, repo)]
	return s.Clone(), ok
}

// GitHubErr returns the last poll's error for repo, or nil; repo may be
// any spelling of it (ghRepoKey).
func (m *Model) GitHubErr(repo string) error { return m.ghErrs[ghRepoKey(m.ghAliases, repo)] }

// GitHubUnavailable reports that gh was checked and found unusable
// (missing, or not logged in).
func (m *Model) GitHubUnavailable() bool { return m.ghAvailable.checked && !m.ghAvailable.ok }

// GitHubUnavailableReason is why gh was found unusable (CheckCLI's
// error), for the issue picker's refusal.
func (m *Model) GitHubUnavailableReason() string { return m.ghAvailable.reason }

// ExpediteGitHub makes the next tick poll GitHub at once (an in-flight
// poll still lands first): after a push, an issue-born session, a newly
// opened workspace.
func (m *Model) ExpediteGitHub() { m.gate(gateGH).expedite() }

// WatchGitHub adds repo to the repositories the poll covers, for
// ghWatchTTL from now, and polls at the next tick (ExpediteGitHub) unless
// the poll has an answer for it already. A client's issue picker asks for
// its repository on every open: in global mode, a TUI started in a
// repository no opened workspace or global session names, which the poll
// would otherwise never cover. A repository watched already (compared
// canonically) is renewed, and one the poll has answered for (a snapshot
// or an error) is not polled again at once, so a picker opened again costs
// no gh calls. A path that is not absolute is not watched. One an opened
// workspace's poll covers already, under any spelling or from a
// subdirectory, is polled once (openedRepos, ghPollJob), and the client
// finds its state by the path it asked with (ghRepoKey). Expired watches
// are dropped here.
func (m *Model) WatchGitHub(repo string) {
	now := time.Now()
	m.ghWatched = slices.DeleteFunc(m.ghWatched, func(w ghWatch) bool { return now.Sub(w.at) >= ghWatchTTL })
	if filepath.IsAbs(repo) {
		key := canonicalDir(repo)
		if i := slices.IndexFunc(m.ghWatched, func(w ghWatch) bool { return canonicalDir(w.repo) == key }); i >= 0 {
			m.ghWatched[i].at = now
		} else {
			m.ghWatched = append(m.ghWatched, ghWatch{repo: repo, at: now})
		}
	}
	key := ghRepoKey(m.ghAliases, repo)
	if _, answered := m.ghState[key]; !answered && m.ghErrs[key] == nil {
		m.ExpediteGitHub()
	}
}

// pushResult is a push job's result (Push): err is the commit or push
// failure, nil on success.
type pushResult struct{ err error }

// pushInst returns the job committing and pushing inst's worktree, reporting
// a pushResult: an error becomes a notice, a success expedites the GitHub
// poll so the PR badge follows. Formerly app.pushActionFor.
func (m *Model) pushInst(inst *session.Instance) Job {
	selected := inst
	return func() any {
		commitMsg := fmt.Sprintf("[loom] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
		worktree, err := selected.GetGitWorktree()
		if err != nil {
			return pushResult{err: err}
		}
		if worktree == nil {
			// A started workspace terminal has none; a job must never
			// panic.
			return pushResult{err: fmt.Errorf("push: %s has no worktree", selected.Title)}
		}
		if err = worktree.PushChanges(commitMsg, true); err != nil {
			return pushResult{err: err}
		}
		return pushResult{}
	}
}

// deliverPush reports a failed push, or expedites the GitHub poll after
// a successful one.
func (m *Model) deliverPush(r pushResult) {
	if r.err != nil {
		m.notifyErr(r.err)
		return
	}
	m.ExpediteGitHub()
}
