package core

import (
	"sync"
	"time"

	"github.com/aidan-bailey/loom/account"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// rosterResult carries one health tick's `claude agents --json` result
// back to the model. err set means the query failed (daemon
// down, CLI too old, unparseable output); the handler clears the roster so
// status detection falls back to pane content rather than acting on a
// snapshot that may be minutes stale.
type rosterResult struct {
	entries map[string]session.RosterEntry
	err     error
	// extra holds each non-default account's roster and extraErrs its
	// failed queries; entries/err stay the default account's.
	extra     map[string]map[string]session.RosterEntry
	extraErrs map[string]error
	// at is when the query started, which is when its answer was true.
	// Each instance's observation is stamped with it (see
	// session.Instance.ObserveRoster), so an answer from before a hook
	// event loses to that event however late it lands.
	at time.Time
}

// rosterQueryJob schedules the roster queries covering the whole fleet: one
// per account in use, since `claude agents --json` lists only the sessions
// of the config dir it runs under (dirs maps extra accounts to theirs).
// Returns nil when no active instance runs Claude, so a fleet of aider or
// shell sessions never pays for a Claude subprocess. The binary is taken
// from a live instance's Program rather than assumed to be "claude" on
// PATH, so absolute paths (a Nix store path, a version-pinned install)
// resolve to the same CLI the agents were launched with. The queries run in
// parallel inside the one Job, which still returns a single result.
func rosterQueryJob(active []*session.Instance, dirs map[string]string) Job {
	var program string
	accounts := map[string]bool{}
	for _, inst := range active {
		p := inst.Program()
		if !session.IsClaudeProgram(p) {
			continue
		}
		if program == "" {
			program = p
		}
		accounts[inst.Account()] = true
	}
	if program == "" {
		return nil
	}
	return func() any {
		msg := rosterResult{at: time.Now()}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for name := range accounts {
			var env []string
			if name != "" {
				dir, ok := dirs[name]
				if !ok {
					continue // a removed account: its instances get no opinion
				}
				env = account.EnvFor(dir)
			}
			wg.Add(1)
			go func(name string, env []string) {
				defer wg.Done()
				entries, err := session.QueryClaudeRosterEnv(program, env, internalexec.Default{})
				mu.Lock()
				defer mu.Unlock()
				if name == "" {
					msg.entries, msg.err = entries, err
					return
				}
				if msg.extra == nil {
					msg.extra = map[string]map[string]session.RosterEntry{}
					msg.extraErrs = map[string]error{}
				}
				if err != nil {
					msg.extraErrs[name] = err
				} else {
					msg.extra[name] = entries
				}
			}(name, env)
		}
		wg.Wait()
		return msg
	}
}

// rosterInterval is the roster's own polling cadence. It is deliberately
// NOT the health tick's: that tick fires every 500ms on the snapshot path,
// and a subprocess every 500ms would keep a claude process alive much of
// the time purely to poll status — on the one path whose capture-pane
// scraper is fully functional anyway. 3s matches the emulator-path tick,
// which is the cadence the roster was sized for. Events that need a
// sooner answer ask for one (deliverHookScan, maybeRosterQuerySoon).
const rosterInterval = 3 * time.Second

// maybeRosterQuery dispatches a roster query when gateRoster is due: none
// already in flight, and at least rosterInterval since the last dispatch.
// The in-flight guard matters because a slow or hung CLI is bounded only
// by claudeRosterTimeout (5s) — without it, ticks would stack concurrent
// subprocesses. Reports false when nothing should run, including when no
// Claude agent is present. Must be called on the Update goroutine.
func (m *Model) maybeRosterQuery(active []*session.Instance) bool {
	return m.dispatchGated(gateRoster, time.Now(), func() Job {
		return rosterQueryJob(active, m.accountDirs())
	})
}

// deliverRoster applies a roster query's result: it replaces the rosters
// and offers them to every active Claude instance (observeRoster).
func (m *Model) deliverRoster(msg rosterResult) {
	if msg.err != nil {
		// Debug, not warn: a missing daemon or an older CLI without
		// `agents --json` is a supported configuration, not a fault —
		// detection simply falls back to hooks and pane content.
		// Dropping the previous roster is deliberate; a stale snapshot
		// would keep driving transitions long after it stopped being
		// true, which is also why observeRoster voids every
		// roster-sourced status below.
		log.DebugKV("core.roster.query_failed", "err", msg.err.Error())
		m.roster = nil
	} else {
		m.roster = msg.entries
	}
	// Another account's failed query clears only its own entries: the
	// map is replaced wholesale and a failed account is absent from it.
	for name, err := range msg.extraErrs {
		log.DebugKV("core.roster.query_failed", "account", name, "err", err.Error())
	}
	m.rosterByAccount = msg.extra
	m.observeRoster(msg.at)
}

// rosterStatusFor returns the roster's answer for inst from m.roster, if
// it has one, along with Claude's stated reason for blocking (empty unless
// the status is Prompting, and even then only when the CLI named one).
// The bool is false when the roster has no opinion: a non-Claude agent, an empty or failed roster, no entry for
// this worktree (the join key is the directory Claude runs in), or a
// status string this build does not recognize. An extra account's session
// is joined against that account's roster only.
//
// The join is exact string equality on the path. Claude reports a
// symlink-resolved cwd, so a Loom config dir reached through a symlink
// (a dotfiles setup, say) simply produces no match and falls back — a
// silent degradation to the old behavior, never a wrong status.
func (m *Model) rosterStatusFor(inst *session.Instance) (session.Status, string, bool) {
	if inst == nil || !session.IsClaudeProgram(inst.Program()) {
		return session.Ready, "", false
	}
	roster := m.roster
	if acct := inst.Account(); acct != "" {
		roster = m.rosterByAccount[acct]
	}
	if len(roster) == 0 {
		return session.Ready, "", false
	}
	entry, ok := roster[inst.GetWorktreePath()]
	if !ok {
		return session.Ready, "", false
	}
	status, authoritative := entry.LoomStatus()
	return status, entry.WaitingFor, authoritative
}

// AdoptClaudeStatus is the single place a Claude session's reported status
// is applied to an instance. It returns the instance's merged hook and
// roster observation (see session.Instance.ClaudeStatus) and, as a side
// effect, records Claude's reason for blocking so the card can render it.
//
// The reason lives exactly as long as the reported wait: any other
// outcome clears it. Every status path (the TUI's statusDetectedMsg and
// snapshot scan, and deliverHealth) must go through here, and so must applyClaudeStatus;
// duplicating the set/clear at each call site is how they drift apart,
// which is the lockstep hazard called out in CLAUDE.md.
func (m *Model) AdoptClaudeStatus(inst *session.Instance) (session.Status, bool) {
	if inst == nil {
		return session.Ready, false
	}
	status, reason, ok := inst.ClaudeStatus()
	if ok && status == session.Prompting {
		inst.SetWaitReason(reason)
	} else {
		inst.SetWaitReason("")
	}
	return status, ok
}

// applyClaudeStatus moves inst to the status its hooks or the roster last
// reported, for a change that arrived outside the status paths: a hook
// scan or a roster answer. It does nothing for an instance the status
// pipelines may not drive (StatusEligible), and clears a stale wait reason
// when neither source has an opinion.
func (m *Model) applyClaudeStatus(inst *session.Instance) {
	if !StatusEligible(inst) {
		return
	}
	target, ok := m.AdoptClaudeStatus(inst)
	if !ok {
		return
	}
	if err := inst.TransitionTo(target); err != nil {
		log.For("core").Warn("claude_status.transition_failed", "instance", inst.Title, "to", target.String(), "err", err.Error())
	}
}

// observeRoster offers the roster in m.roster to every active Claude
// instance, stamped with at, and moves any whose status changed. An
// instance the roster has no opinion on (a failed query leaves m.roster
// nil) loses a roster-sourced status but keeps a hook-sourced one.
func (m *Model) observeRoster(at time.Time) {
	changed := false
	for _, inst := range m.ActiveInstances() {
		if !session.IsClaudeProgram(inst.Program()) {
			continue
		}
		status, reason, ok := m.rosterStatusFor(inst)
		if inst.ObserveRoster(status, reason, ok, at) {
			changed = true
			m.applyClaudeStatus(inst)
		}
	}
	if changed {
		m.emit(StatusesChanged{})
	}
}

// promptingRosterSpacing bounds how often a Prompting session's output
// can trigger a roster query (see PaneOutput). Answering a prompt makes
// output but fires no hook, and the roster reports busy at once.
const promptingRosterSpacing = 500 * time.Millisecond

// maybeRosterQuerySoon dispatches a roster query ahead of the roster's own
// cadence, but at most once per promptingRosterSpacing and never while one
// is in flight. A request() would not do: a Prompting session the roster
// has no opinion on would then query back to back for as long as it
// produces output.
func (m *Model) maybeRosterQuerySoon() bool {
	g := m.gate(gateRoster)
	if !g.due(time.Now(), promptingRosterSpacing) {
		return false
	}
	g.expedite()
	return m.maybeRosterQuery(m.ActiveInstances())
}

// StatusEligible reports whether the tick/event pipelines may drive this
// instance's status — the same guard set the health tick uses (Recoverable
// placeholders and Loading rows are owned by explicit flows; see the comment
// on ActiveInstances).
func StatusEligible(inst *session.Instance) bool {
	if inst == nil || !inst.Started() || inst.Paused() {
		return false
	}
	st := inst.GetStatus()
	return st != session.Deleting && st != session.Recoverable && st != session.Loading
}

// PaneOutputInst is the TUI's report that inst's pane produced output (a pane
// dirty event). Answering a permission prompt makes output (the dialog
// goes away) but fires no hook, and the roster reports busy at once, so a
// Prompting Claude session asks the roster soon. While Claude works its
// spinner keeps output flowing, so this also reads a UserPromptSubmit
// within hookScanInterval. Call it before the TUI's own status ladder
// moves inst: it reads the status the output arrived in.
func (m *Model) PaneOutputInst(inst *session.Instance) {
	// Answering a permission prompt makes output (the dialog goes
	// away) but fires no hook; the roster reports busy at once.
	if inst.GetStatus() == session.Prompting && session.IsClaudeProgram(inst.Program()) {
		m.maybeRosterQuerySoon()
	}
	// While Claude works, its spinner keeps output flowing, so this
	// reads a UserPromptSubmit within hookScanInterval.
	if inst.HooksLaunched() {
		m.maybeHookScan(m.ActiveInstances())
	}
}

// PaneQuietInst is the TUI's report that inst's pane went quiet (nil for a
// pane no instance owns). Stop and PermissionRequest arrive as output
// settles, often in a burst's last output, so it scans even inside
// hookScanInterval or while a scan is in flight: request().
func (m *Model) PaneQuietInst(inst *session.Instance) {
	if inst == nil || !inst.HooksLaunched() {
		return
	}
	// Stop and PermissionRequest arrive as output settles. This is
	// often a burst's last output, so it must scan even inside
	// hookScanInterval or while a scan is in flight: request().
	m.gate(gateHookScan).request()
	m.maybeHookScan(m.ActiveInstances())
}

// PaneOutput reports that id's pane produced output (PaneOutputInst).
func (m *Model) PaneOutput(id InstanceID) {
	if inst, _ := m.lookup(id); inst != nil {
		m.PaneOutputInst(inst)
	}
}

// PaneQuiet reports that id's pane went quiet (PaneQuietInst).
func (m *Model) PaneQuiet(id InstanceID) {
	if inst, _ := m.lookup(id); inst != nil {
		m.PaneQuietInst(inst)
	}
}
