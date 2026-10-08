package core

import (
	"context"
	"fmt"
	"os"
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

// ghAvailability is the cached result of github.CheckCLI. checkedAt
// anchors the ghRecheckInterval backoff on an unavailable result.
type ghAvailability struct {
	checked   bool
	ok        bool
	reason    string
	checkedAt time.Time
}

// ghResult carries one poll's result for every open repo. errs holds
// repos whose query failed; they are dropped from ghState so a stale
// snapshot never keeps rendering. bases is the resolved base ref per
// repo, for parity.
type ghResult struct {
	available ghAvailability
	snapshots map[string]github.Snapshot
	errs      map[string]error
	bases     map[string]string
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
// (the global one) stands for the directory loom was started in, as
// classic mode always polled. The workspaces with a repository of their own
// come first, in the order the model serves them, then the stand-ins: when
// loom starts in a registered repository's directory, the stand-in names
// that repository too (compared canonically), and the repository's own
// workspace, with its base branch, must win.
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
		if ws.cfg != nil {
			base = ws.cfg.GetBaseBranch()
		}
		out = append(out, openedRepo{path: repo, base: base})
	}
	for _, ws := range m.workspaces {
		if ws.opened && ws.ctx != nil && ws.ctx.RepoPath != "" {
			add(ws, ws.ctx.RepoPath)
		}
	}
	for _, ws := range m.workspaces {
		if ws.opened && (ws.ctx == nil || ws.ctx.RepoPath == "") {
			cwd, _ := os.Getwd()
			add(ws, cwd)
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

// linkedIssues lists the non-zero issue numbers of instances in repo.
func (m *Model) linkedIssues(repo string) []int {
	var out []int
	for _, inst := range m.allInstances() {
		if inst.Path == repo && inst.IssueNumber() != 0 {
			out = append(out, inst.IssueNumber())
		}
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

// ghPollJob runs one poll: an optional CLI check, then per repo a base
// resolve + fetch (always, even without gh) and the gh query (only
// when gh is available). Pure I/O; returns a single result. The
// whole poll is bounded by ghPollBudget so a repo with many linked
// closed issues cannot stall every other workspace's refresh.
func ghPollJob(req ghPollRequest, r internalexec.Executor) Job {
	return func() any {
		ctx, cancel := context.WithTimeout(context.Background(), ghPollBudget)
		defer cancel()
		msg := ghResult{snapshots: map[string]github.Snapshot{}, errs: map[string]error{}, bases: map[string]string{}}
		msg.available = ghAvailability{checked: true, ok: true, checkedAt: time.Now()}
		if req.check {
			if err := github.CheckCLI(r); err != nil {
				msg.available = ghAvailability{checked: true, ok: false, reason: err.Error(), checkedAt: time.Now()}
			}
		}
		for _, repo := range req.repos {
			if _, name, err := git.ResolveBaseCommit(repo, req.configured[repo], r); err == nil {
				msg.bases[repo] = name
				if ferr := git.FetchRef(repo, name, r); ferr != nil {
					log.For("github").Debug("base.fetch_failed", "repo", repo, "err", ferr.Error())
				}
			}
			if !msg.available.ok {
				continue
			}
			snap, err := github.Query(ctx, repo, req.linked[repo], r)
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
	m.ghState = msg.snapshots
	m.ghErrs = msg.errs
	if msg.bases != nil {
		m.ghBases = msg.bases
	}
	m.applyGitHubState()
	m.ghGen++
}

// baseFor returns the resolved base ref for repo, or "" before the
// first poll resolved it (parity then stays unknown).
func (m *Model) baseFor(repo string) string {
	return m.ghBases[repo]
}

// applyGitHubState joins ghState onto every instance. Cheap and pure,
// so it also runs when a link is set outside a poll (issue pick).
func (m *Model) applyGitHubState() {
	for _, inst := range m.allInstances() {
		snap, known := m.ghState[inst.Path]
		inst.SetGitHubState(github.StateFor(snap, known, inst.GetBranch(), inst.IssueNumber()))
	}
}

// GitHubSnapshot returns the latest poll's snapshot of repo, if the last
// poll of it succeeded.
func (m *Model) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	s, ok := m.ghState[repo]
	return s.Clone(), ok
}

// GitHubErr returns the last poll's error for repo, or nil.
func (m *Model) GitHubErr(repo string) error { return m.ghErrs[repo] }

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
