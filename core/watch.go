package core

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/watch"
)

// watchInterval is the issue watcher's cadence. It rides the health tick
// but starts a poll at most this often (maybeWatch).
const watchInterval = 60 * time.Second

// watchState is the issue watcher's state for one workspace: what the last
// poll read and decided, and what the actions in flight hold. The loop owns
// it; a poll job never sees it (watchRequest copies what the job needs, and
// deliverWatch stores what it returns).
type watchState struct {
	// viewer is the gh login the watcher acts as, cached: read again only
	// after a failed read.
	viewer string
	// memPath is the memory file, <config dir>/watch.json.
	memPath string
	// mem is the memory, loaded once by a poll job and owned by the loop
	// from then on: the actions mutate it and save it (watch.SaveMemory).
	mem watch.Memory
	// memLoaded is set once mem holds the file's contents; a poll reloads
	// the file until then, so an unreadable file keeps the watcher stopped.
	memLoaded bool
	// cfg and tmpl are the last valid loom-watch.json read from
	// origin/<base>, and its title template.
	cfg  watch.Config
	tmpl watch.TitleTemplate
	// base is the base branch's name without "origin/", baseSHA what this
	// tick's fetch returned ("" when it failed).
	base    string
	baseSHA string
	// startOK is set when this tick's fetch and checkout refresh both
	// succeeded: only then may an ActStart run (Story 9 refuses one
	// otherwise).
	startOK bool
	// checkout is the watcher worktree, <config dir>/watch-checkout.
	checkout string
	// prompt and resumePrompt are the template texts read from
	// origin/<base>, unrendered.
	prompt       string
	resumePrompt string
	// repo is the repository's owner/name, read from origin's URL when the
	// config names a hook (it is the hook's LOOM_WATCH_REPO).
	repo string
	// snap is the last decided tick's snapshot, nil before the first and
	// after a tick that decided nothing.
	snap *watch.Snapshot
	// actions is that tick's Decide result, ActWait included.
	actions []watch.Action
	// at is when that tick decided.
	at time.Time
	// err is why the last tick decided nothing (config, memory, a read);
	// "" when it decided.
	err string
	// polling is set while a poll job is out.
	polling bool
	// inFlight holds the keys of actions whose job has not returned
	// (Story 9 owns the keys).
	inFlight map[string]bool
	// actErrs is each issue's last failed or refused action, by issue
	// number (Story 9 sets and clears it).
	actErrs map[int]string
}

// watchFor returns ws's watcher state, creating it on first use.
func (m *Model) watchFor(ws *Workspace) *watchState {
	if ws.watch == nil {
		ws.watch = &watchState{inFlight: map[string]bool{}, actErrs: map[int]string{}}
	}
	return ws.watch
}

// watchOf returns ws's watcher state when the model still serves ws and its
// watcher is on, else nil. A result that arrives for any other state (the
// workspace dropped, or switched off while the job ran) records nothing.
func (m *Model) watchOf(ws *Workspace) *watchState {
	if !m.isLoadedWS(ws) || ws.watch == nil || ws.cfg == nil || !ws.cfg.GetWatchEnabled() {
		return nil
	}
	return ws.watch
}

// decideWatch runs Decide on the loop for a poll's reads: the snapshot the
// job built, stamped with now and the workspace's sessions as they are now
// (not as they were at dispatch), against the memory the loop holds (the
// file's, when this poll loaded it). A tick whose fetch or checkout refresh
// failed starts nothing: each ActStart becomes an ActWait saying why, so
// rules 1 to 8 still act (spec 7.1). Only for a result with no err.
func (m *Model) decideWatch(res watchResult) (watch.Snapshot, []watch.Action) {
	snap := res.snap
	snap.Now = time.Now()
	snap.Sessions = m.watchSessions(res.ws)
	var mem watch.Memory
	if st := res.ws.watch; st != nil {
		mem = st.mem
	}
	if res.mem != nil {
		mem = *res.mem
	}
	actions := watch.Decide(snap, mem, res.cfg, res.tmpl)
	if !res.startOK {
		for i, a := range actions {
			if a.Kind == watch.ActStart {
				actions[i] = watch.Action{Kind: watch.ActWait, Issue: a.Issue, Reason: "no start this tick: " + res.startWhy}
			}
		}
	}
	return snap, actions
}

// applyWatchFacts copies a poll's reads into st: the viewer, the paths, the
// fetch's outcome, and the memory and config when this poll read them. It
// leaves the tick's err, snapshot and actions to the caller.
func applyWatchFacts(st *watchState, res watchResult) {
	st.viewer, st.memPath, st.checkout = res.viewer, res.memPath, res.checkout
	st.base, st.baseSHA, st.startOK = res.base, res.baseSHA, res.startOK
	if res.slug != "" {
		st.repo = res.slug
	}
	if res.mem != nil {
		st.mem, st.memLoaded = *res.mem, true
	}
	if res.cfgOK {
		st.cfg, st.tmpl = res.cfg, res.tmpl
		st.prompt, st.resumePrompt = res.prompt, res.resumePrompt
	}
}

// storeWatch keeps a live workspace's poll result: the facts it read, and,
// when no read failed, the tick Decide made. A failed tick clears the last
// one's snapshot and actions: stale decisions are worse than none.
func (m *Model) storeWatch(st *watchState, res watchResult) {
	applyWatchFacts(st, res)
	st.err = res.err
	if res.err != "" {
		st.snap, st.actions = nil, nil
		return
	}
	snap, actions := m.decideWatch(res)
	st.snap, st.actions, st.at = &snap, actions, snap.Now
	pruneWatchActErrs(st)
}

// pruneWatchActErrs drops the error of every issue the tick's snapshot no
// longer holds and no watcher session or PR names: nothing is left to retry
// there, and a stale refusal would stay in status for good. An issue still
// named keeps its error until an action of that kind succeeds.
func pruneWatchActErrs(st *watchState) {
	snap := st.snap
	for n := range st.actErrs {
		if watchIssueNamed(st, snap, n) {
			continue
		}
		delete(st.actErrs, n)
	}
}

// watchIssueNamed reports whether snap holds issue n or a watcher session or
// PR names it.
func watchIssueNamed(st *watchState, snap *watch.Snapshot, n int) bool {
	if slices.ContainsFunc(snap.Issues, func(is github.WatchIssue) bool { return is.Number == n }) {
		return true
	}
	if slices.ContainsFunc(snap.Sessions, func(s watch.Session) bool { return s.Issue == n && watch.OwnSession(s, st.tmpl) }) {
		return true
	}
	return slices.ContainsFunc(snap.PRs, func(pr github.WatchPR) bool {
		return slices.Contains(pr.Closes, n) && watch.OwnPR(pr, n, snap.Viewer, st.tmpl.BranchPattern(snap.Prefix, n))
	})
}

// deliverWatch applies a poll result. The in-flight bit clears first,
// whatever the result is. A dry result is handled before any check of the
// workspace (its requester is waiting) and stores nothing. A result for a
// workspace the model no longer serves, or whose watcher is switched off,
// records nothing. Otherwise the tick is kept (storeWatch).
func (m *Model) deliverWatch(res watchResult) {
	if st := res.ws.watch; st != nil {
		st.polling = false
	}
	if res.dry {
		var snap watch.Snapshot
		var actions []watch.Action
		if res.err == "" {
			snap, actions = m.decideWatch(res)
		}
		m.answerWatchDry(res, snap, actions, res.err)
		return
	}
	st := m.watchOf(res.ws)
	if st == nil {
		log.For("watch").Debug("poll.dropped", "workspace", res.ws.Name())
		return
	}
	m.storeWatch(st, res)
	if res.err != "" {
		log.For("watch").Debug("poll.failed", "workspace", res.ws.Name(), "err", res.err)
	}
	if st.err == "" {
		m.dispatchWatchActions(res.ws, st, st.actions)
	}
}

// startWatchPoll dispatches one poll job for ws: false when the model does
// not serve ws, ws has no repository, or a poll is already out. The tick's
// poll is the model's own job (spawnBackground); a dry run is a request's
// follow-on (spawn), so Loop.Quiesce waits for it; its watchResult carries
// req and the reply is answered through res.req (the caller, ReadWatch, runs
// outside causedBy, so the job is not wrapped as caused). Loop-goroutine only.
func (m *Model) startWatchPoll(ws *Workspace, dry bool, req ReqID) bool {
	if !m.isLoadedWS(ws) || ws.ctx == nil || ws.ctx.RepoPath == "" {
		return false
	}
	st := m.watchFor(ws)
	if st.polling {
		return false
	}
	st.polling = true
	job := watchPollJob(m.watchRequest(ws, dry, req), m.watchExec())
	if dry {
		m.spawn(job)
	} else {
		m.spawnBackground(job)
	}
	return true
}

// maybeWatch starts a poll for every served workspace that has a repository
// and the watcher switched on, when gateWatch is due: at most every
// watchInterval. A workspace whose poll is still out is skipped, and nothing
// is armed unless a job was dispatched. Loop goroutine only.
func (m *Model) maybeWatch() bool {
	now := time.Now()
	if !m.gateDue(gateWatch, now) {
		return false
	}
	dispatched := false
	for _, ws := range m.Loaded() {
		if ws.ctx == nil || ws.ctx.RepoPath == "" || ws.cfg == nil || !ws.cfg.GetWatchEnabled() {
			continue
		}
		if ws.loadErr != nil || ws.resetting {
			continue
		}
		if m.startWatchPoll(ws, false, 0) {
			dispatched = true
		}
	}
	if dispatched {
		m.gate(gateWatch).last = now
	}
	return dispatched
}

// watchSummaryEscalations is how many escalations a WorkspaceView's
// WatchSummary carries: the newest.
const watchSummaryEscalations = 20

// watchEscalations lists the issues memory holds an escalation for,
// newest first by time, a tie by issue number; nil for none. The slice is
// a fresh copy.
func watchEscalations(mem watch.Memory) []WatchEscalation {
	var out []WatchEscalation
	for n, im := range mem.Issues {
		if im.Escalated != nil {
			out = append(out, WatchEscalation{Issue: n, Reason: im.Escalated.Reason, At: im.Escalated.At})
		}
	}
	slices.SortFunc(out, func(a, b WatchEscalation) int {
		return cmp.Or(b.At.Compare(a.At), cmp.Compare(a.Issue, b.Issue))
	})
	return out
}

// watchSummaryOf is ws's WatchSummary: empty while its switch is off, and
// Loaded only once a poll has loaded memory into the state. The state is
// the loop's, so this runs on the loop (wsViewOf).
func watchSummaryOf(ws *Workspace) WatchSummary {
	if ws.cfg == nil || !ws.cfg.GetWatchEnabled() {
		return WatchSummary{}
	}
	sum := WatchSummary{On: true}
	if st := ws.watch; st != nil {
		sum.Loaded, sum.Err = st.memLoaded, st.err
		sum.Escalations = watchEscalations(st.mem)
		if len(sum.Escalations) > watchSummaryEscalations {
			sum.Escalations = sum.Escalations[:watchSummaryEscalations]
		}
	}
	return sum
}

// watchViewOf is ws's WatchView, built on the loop from its watchState and
// actions: a dry run's, or ws.watch.actions for a status read. A workspace
// whose switch is off, or that holds no state yet, answers On alone.
func (m *Model) watchViewOf(ws *Workspace, actions []watch.Action) WatchView {
	return m.watchViewFrom(ws, ws.watch, actions)
}

// watchViewFrom is watchViewOf over the state st: ws.watch for a status
// read, a dry run's copy with the fresh poll laid over it for a dry run.
func (m *Model) watchViewFrom(ws *Workspace, st *watchState, actions []watch.Action) WatchView {
	on := ws.cfg != nil && ws.cfg.GetWatchEnabled()
	if !on || st == nil {
		return WatchView{On: on}
	}
	v := WatchView{
		On: true, Err: st.err, Viewer: st.viewer,
		Actions:     cloneWatchActions(actions),
		Escalations: watchEscalations(st.mem),
		Issues:      watchIssueViews(st, actions),
	}
	if st.snap != nil {
		v.Considered, v.Unconsidered = st.snap.Considered, st.snap.Unconsidered
	}
	return v
}

// cloneWatchActions copies actions, with each Settle list its own.
func cloneWatchActions(actions []watch.Action) []watch.Action {
	out := slices.Clone(actions)
	for i := range out {
		out[i].Settle = slices.Clone(out[i].Settle)
	}
	return out
}

// watchIssueViews lists, in number order, every issue the last snapshot
// holds, every issue one of the watcher's own sessions or PRs names, every
// issue the actions name and every issue a failed action (st.actErrs, which
// is Failed) names. A session or PR counts only when the
// watcher owns it (watch.OwnSession, watch.OwnPR), as Decide does; a
// foreign one never shows. With no snapshot only the actions' issues are
// listed, as unknown.
func watchIssueViews(st *watchState, actions []watch.Action) []WatchIssueView {
	numbers := map[int]bool{}
	state := map[int]string{}
	waiting := map[int]string{}
	for _, a := range actions {
		if a.Issue > 0 {
			numbers[a.Issue] = true
		}
		if _, seen := waiting[a.Issue]; a.Kind == watch.ActWait && !seen {
			waiting[a.Issue] = a.Reason
		}
	}
	for n := range st.actErrs {
		if n > 0 {
			numbers[n] = true
		}
	}
	sessionIDs := map[int]uint64{}
	sessions := map[int]string{}
	prs := map[int]int{}
	if snap := st.snap; snap != nil {
		for _, is := range snap.Issues {
			numbers[is.Number] = true
			state[is.Number] = "open"
			if is.Closed {
				state[is.Number] = "closed"
			}
		}
		for _, s := range snap.Sessions {
			if s.Issue <= 0 || !watch.OwnSession(s, st.tmpl) {
				continue
			}
			numbers[s.Issue] = true
			if id, ok := sessionIDs[s.Issue]; !ok || s.ID < id {
				sessionIDs[s.Issue], sessions[s.Issue] = s.ID, s.Title
			}
		}
		for _, pr := range snap.PRs {
			for _, n := range pr.Closes {
				if !watch.OwnPR(pr, n, snap.Viewer, st.tmpl.BranchPattern(snap.Prefix, n)) {
					continue
				}
				numbers[n] = true
				if pr.State == "OPEN" && (prs[n] == 0 || pr.Number < prs[n]) {
					prs[n] = pr.Number
				}
			}
		}
		for n, why := range snap.HookFailed {
			if _, ok := waiting[n]; !ok {
				waiting[n] = why
			}
		}
	}
	out := make([]WatchIssueView, 0, len(numbers))
	for n := range numbers {
		iv := WatchIssueView{Number: n, State: "unknown", Session: sessions[n], PR: prs[n], Waiting: waiting[n], Failed: st.actErrs[n]}
		if s, ok := state[n]; ok {
			iv.State = s
		}
		if pm := st.mem.PRs[iv.PR]; iv.PR != 0 {
			for kind, rec := range pm.Nudges {
				if rec.N > 0 {
					if iv.Nudges == nil {
						iv.Nudges = map[string]int{}
					}
					iv.Nudges[kind] = rec.N
				}
			}
		}
		iv.Escalated = st.mem.Issues[n].Escalated != nil
		out = append(out, iv)
	}
	slices.SortFunc(out, func(a, b WatchIssueView) int { return cmp.Compare(a.Number, b.Number) })
	return out
}

// answerWatchDry answers the requester of a dry run (res.req) once its poll
// is delivered: with a Reply whose Watch is the view of snap and actions,
// what the next tick would see and Decide would take, built wholly from this
// poll (its viewer, memory, config, snapshot and counts; never the stored
// tick's err or snapshot), or with a refusal when the poll failed (failure
// is the text a delivered non-dry result stores in watchState.err, "" when
// it decided) or the watcher was switched off while it ran. It stores
// nothing and dispatches nothing: a dry run takes no action.
func (m *Model) answerWatchDry(res watchResult, snap watch.Snapshot, actions []watch.Action, failure string) {
	if failure != "" {
		m.refuse(res.req, 0, fmt.Errorf("dry run: %s", failure))
		return
	}
	st := m.watchOf(res.ws)
	if st == nil {
		m.refuse(res.req, 0, errors.New("dry run: the watcher was switched off while the poll ran"))
		return
	}
	// The view reads a copy of the stored state with this poll laid over it:
	// the stored tick's err and snapshot never reach it, and the stored state
	// itself is left as it was. The copy shares the stored maps, which the
	// view only reads.
	dry := *st
	applyWatchFacts(&dry, res)
	dry.err, dry.snap, dry.actions = "", &snap, actions
	m.reply(res.req, Reply{Watch: m.watchViewFrom(res.ws, &dry, actions)})
}

// SetWatch switches the issue watcher on or off for workspace id
// (Core.SetWatch): the switch is written to the workspace's own
// config.json (Settings.Watch), and the poll gate reads it on its next
// tick. It refuses a workspace the model does not serve, one with no
// repository (the global workspace has none to watch) and a save that
// fails, which leaves the in-memory switch as it was. Turning it on
// expedites the poll gate and polls at once (maybeWatch); turning it off
// stops the watcher, and its sessions keep running untouched.
func (m *Model) SetWatch(id WorkspaceID, on bool, req ReqID) {
	ws := m.wsLookup(id)
	if ws == nil || ws.cfg == nil {
		m.refuse(req, 0, fmt.Errorf("set watch: workspace %d is not served", id))
		return
	}
	if ws.ctx == nil || ws.ctx.RepoPath == "" {
		m.refuse(req, 0, fmt.Errorf("set watch: workspace %s has no repository to watch", ws.Label()))
		return
	}
	dir := settingsDir(ws)
	if dir == "" {
		m.refuse(req, 0, fmt.Errorf("set watch: workspace %s has no config dir to write to", ws.Label()))
		return
	}
	var before *bool
	ws.cfg.Mutate(func(c *config.Config) { before, c.Watch = c.Watch, &on })
	if err := config.SaveConfigTo(ws.cfg, dir); err != nil {
		ws.cfg.Mutate(func(c *config.Config) { c.Watch = before })
		m.refuse(req, 0, fmt.Errorf("set watch: %w", err))
		return
	}
	log.For("core").Info("watch.switch", "workspace", ws.Label(), "on", on)
	if on {
		// A switch-on polls at once, not at the next cadence.
		m.gate(gateWatch).expedite()
		m.maybeWatch()
	}
	m.reply(req, Reply{})
}

// ReadWatch answers with the watcher's state for workspace id (Core.ReadWatch):
// the Reply's Watch. A status read is answered at once from the last
// tick's actions. A dry run starts a poll whose delivery answers it with
// the actions the next tick would take, none taken (answerWatchDry), and
// is refused while a poll is already out. A workspace whose switch is off
// answers On false for either, and polls nothing.
func (m *Model) ReadWatch(id WorkspaceID, dryRun bool, req ReqID) {
	ws := m.wsLookup(id)
	if ws == nil || ws.cfg == nil {
		m.refuse(req, 0, fmt.Errorf("read watch: workspace %d is not served", id))
		return
	}
	if !ws.cfg.GetWatchEnabled() {
		m.reply(req, Reply{Watch: m.watchViewOf(ws, nil)})
		return
	}
	if !dryRun {
		var actions []watch.Action
		if st := ws.watch; st != nil {
			actions = st.actions
		}
		m.reply(req, Reply{Watch: m.watchViewOf(ws, actions)})
		return
	}
	if !m.startWatchPoll(ws, true, req) {
		m.refuse(req, 0, errors.New("dry run: a watcher poll is already running; try again when it ends"))
	}
}
