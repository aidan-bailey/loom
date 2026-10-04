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

// openRepoPaths lists the repo path of every open workspace (or the
// classic single repo), deduplicated, in slot order.
func (m *Model) openRepoPaths() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(m.tabs) == 0 {
		cwd, _ := os.Getwd()
		add(cwd)
		return out
	}
	for _, ws := range m.tabs {
		if ws.ctx != nil {
			add(ws.ctx.RepoPath)
		}
	}
	return out
}

// baseBranchByRepo maps each open repo to ITS OWN configured base
// branch. Classic mode has a single config; slot mode reads each
// slot's, since m.appConfig() only ever reflects the focused slot.
func (m *Model) baseBranchByRepo() map[string]string {
	out := map[string]string{}
	if len(m.tabs) == 0 {
		if m.classic != nil && m.classic.cfg != nil {
			cwd, _ := os.Getwd()
			out[cwd] = m.classic.cfg.GetBaseBranch()
		}
		return out
	}
	for _, ws := range m.tabs {
		if ws.ctx == nil || ws.cfg == nil {
			continue
		}
		out[ws.ctx.RepoPath] = ws.cfg.GetBaseBranch()
	}
	return out
}

// linkedIssues lists the non-zero issue numbers of instances in repo.
func (m *Model) linkedIssues(repo string) []int {
	var out []int
	for _, inst := range m.Instances() {
		if inst.Path == repo && inst.IssueNumber() != 0 {
			out = append(out, inst.IssueNumber())
		}
	}
	return out
}

// maybeGHQuery dispatches a poll when one is due: gh not known
// unavailable, and gateGH due (none in flight, and ghInterval since the
// last dispatch). Update goroutine only.
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
// re-joins every instance.
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
	m.emit(GitHubChanged{})
}

// baseFor returns the resolved base ref for repo, or "" before the
// first poll resolved it (parity then stays unknown).
func (m *Model) baseFor(repo string) string {
	return m.ghBases[repo]
}

// applyGitHubState joins ghState onto every instance. Cheap and pure,
// so it also runs when a link is set outside a poll (issue pick).
func (m *Model) applyGitHubState() {
	for _, inst := range m.Instances() {
		snap, known := m.ghState[inst.Path]
		inst.SetGitHubState(github.StateFor(snap, known, inst.GetBranch(), inst.IssueNumber()))
	}
}

// ApplyGitHubState joins the latest poll's state onto every instance, for
// a link set outside a poll (an issue pick).
func (m *Model) ApplyGitHubState() { m.applyGitHubState() }

// GitHubSnapshot returns the latest poll's snapshot of repo, if the last
// poll of it succeeded.
func (m *Model) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	s, ok := m.ghState[repo]
	return s, ok
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

// Push returns the job committing and pushing inst's worktree, reporting
// a pushResult: an error becomes a notice, a success expedites the GitHub
// poll so the PR badge follows. Formerly app.pushActionFor.
func (m *Model) Push(inst *session.Instance) Job {
	selected := inst
	return func() any {
		commitMsg := fmt.Sprintf("[loom] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
		worktree, err := selected.GetGitWorktree()
		if err != nil {
			return pushResult{err: err}
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
