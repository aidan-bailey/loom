package core

import (
	"time"

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
