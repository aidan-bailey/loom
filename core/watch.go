package core

import (
	"time"

	"github.com/aidan-bailey/loom/log"
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

// storeWatch keeps a live workspace's poll result: the facts it read, and,
// when no read failed, the tick Decide made. A failed tick clears the last
// one's snapshot and actions: stale decisions are worse than none.
func (m *Model) storeWatch(st *watchState, res watchResult) {
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
	st.err = res.err
	if res.err != "" {
		st.snap, st.actions = nil, nil
		return
	}
	snap, actions := m.decideWatch(res)
	st.snap, st.actions, st.at = &snap, actions, snap.Now
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
		// Story 10's reply goes here, before any watchOf check, so a dry
		// result is answered even with no watch state, an off watcher or
		// an unserved workspace: when res.err is "", watchViewOf(res.ws,
		// actions) for the actions decideWatch(res) returns, else a refusal
		// naming res.err. It then returns.
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
