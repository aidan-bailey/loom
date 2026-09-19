package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

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

// ghRefreshMsg asks for an immediate poll on the next tick. Sent after
// a push, an issue-born session, and a workspace activation.
type ghRefreshMsg struct{}

// ghReadyMsg carries one poll's result for every open repo. errs holds
// repos whose query failed; they are dropped from ghState so a stale
// snapshot never keeps rendering. bases is the resolved base ref per
// repo, for parity.
type ghReadyMsg struct {
	available ghAvailability
	snapshots map[string]github.Snapshot
	errs      map[string]error
	bases     map[string]string
}

// ghPollRequest is the main-goroutine snapshot the poll Cmd works
// from. Everything it needs is copied here so the Cmd body touches no
// model state.
type ghPollRequest struct {
	repos  []string
	linked map[string][]int
	// configured is each repo's own config.BaseBranch. Keyed per repo
	// because every workspace has its own config.json — m.appConfig is
	// whichever slot is focused, not a shared primary, so one string
	// applied across the batch would resolve non-focused repos against
	// the wrong workspace's setting.
	configured map[string]string
	check      bool // run CheckCLI first
}

// openRepoPaths lists the repo path of every open workspace (or the
// classic single repo), deduplicated, in slot order.
func (m *home) openRepoPaths() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(m.slots) == 0 {
		add(m.repoPath())
		return out
	}
	for _, s := range m.slots {
		if s.wsCtx != nil {
			add(s.wsCtx.RepoPath)
		}
	}
	return out
}

// baseBranchByRepo maps each open repo to ITS OWN configured base
// branch. Classic mode has a single config; slot mode reads each
// slot's, since m.appConfig only ever reflects the focused slot.
func (m *home) baseBranchByRepo() map[string]string {
	out := map[string]string{}
	if len(m.slots) == 0 {
		if m.appConfig != nil {
			out[m.repoPath()] = m.appConfig.GetBaseBranch()
		}
		return out
	}
	for _, s := range m.slots {
		if s.wsCtx == nil || s.appConfig == nil {
			continue
		}
		out[s.wsCtx.RepoPath] = s.appConfig.GetBaseBranch()
	}
	return out
}

// allInstances returns every instance across open slots (or the
// classic list).
func (m *home) allInstances() []*session.Instance {
	if len(m.slots) == 0 {
		return m.list.GetInstances()
	}
	var out []*session.Instance
	for _, s := range m.slots {
		if s.list != nil {
			out = append(out, s.list.GetInstances()...)
		}
	}
	return out
}

// linkedIssues lists the non-zero issue numbers of instances in repo.
func (m *home) linkedIssues(repo string) []int {
	var out []int
	for _, inst := range m.allInstances() {
		if inst.Path == repo && inst.IssueNumber() != 0 {
			out = append(out, inst.IssueNumber())
		}
	}
	return out
}

// maybeGHQuery returns a poll Cmd when one is due: gh not known
// unavailable, none in flight, and ghInterval since the last dispatch.
// Arms neither field when it dispatches nothing — no Cmd means no
// ghReadyMsg to disarm them. Update goroutine only.
func (m *home) maybeGHQuery() tea.Cmd {
	// A gh that reported unavailable is re-probed on ghRecheckInterval
	// rather than never again.
	if m.ghAvailable.checked && !m.ghAvailable.ok && time.Since(m.ghAvailable.checkedAt) < ghRecheckInterval {
		return nil
	}
	if m.ghInFlight || time.Since(m.lastGHQuery) < ghInterval {
		return nil
	}
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
	m.ghInFlight = true
	m.lastGHQuery = time.Now()
	return ghPollCmd(req, internalexec.Default{})
}

// ghPollCmd runs one poll: an optional CLI check, then per repo a base
// resolve + fetch (always, even without gh) and the gh query (only
// when gh is available). Pure I/O; returns a single message. The
// whole poll is bounded by ghPollBudget so a repo with many linked
// closed issues cannot stall every other workspace's refresh.
func ghPollCmd(req ghPollRequest, r internalexec.Executor) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ghPollBudget)
		defer cancel()
		msg := ghReadyMsg{snapshots: map[string]github.Snapshot{}, errs: map[string]error{}, bases: map[string]string{}}
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

// handleGHReady applies a poll result: disarms the in-flight guard
// first (a miss latches the poller off), replaces ghState wholesale,
// and re-joins every instance.
func (m *home) handleGHReady(msg ghReadyMsg) {
	m.ghInFlight = false
	m.ghAvailable = msg.available
	for repo, err := range msg.errs {
		log.For("github").Debug("query_failed", "repo", repo, "err", err.Error())
	}
	m.ghState = msg.snapshots
	if msg.bases != nil {
		m.ghBases = msg.bases
	}
	m.applyGitHubState()
	if p := m.issuePicker(); p != nil {
		p.SetRows(m.issueRows())
		p.SetStatus(m.issuePickerStatus())
	}
}

// baseFor returns the resolved base ref for repo, or "" before the
// first poll resolved it (parity then stays unknown).
func (m *home) baseFor(repo string) string {
	return m.ghBases[repo]
}

// applyGitHubState joins ghState onto every instance. Cheap and pure,
// so it also runs when a link is set outside a poll (issue pick).
func (m *home) applyGitHubState() {
	for _, inst := range m.allInstances() {
		snap, known := m.ghState[inst.Path]
		inst.SetGitHubState(github.StateFor(snap, known, inst.GetBranch(), inst.IssueNumber()))
	}
}
