package core

import (
	"slices"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
)

// maxWorkspaceTerminalRestartFailures bounds how many consecutive metadata
// ticks the workspace-terminal auto-restart path (applyLiveness) will
// retry a dead tmux session before giving up and marking it Paused instead.
// Without this, a permanently broken Program (e.g. a stale command left
// over from a since-changed launch mechanism) restart-loops forever at
// tick cadence — 500ms on the snapshot path (tickInterval), so ~1.5s of thrash
// before this trips. Restart's own Start(true) blocks until the session is
// confirmed up before returning, so a genuinely successful restart should
// never even reach 2 consecutive misses; this is slack for one flaky
// blip, not a real recovery window.
const maxWorkspaceTerminalRestartFailures = 3

// tickInterval is the health tick's period: a slow belt-and-braces sweep
// in event mode (the emulator path), where status rides pane events, and
// the legacy 500ms on the snapshot path. The loop arms the next tick this
// long after the previous probe lands (Loop.armTick). Formerly the sleep
// in app's tickUpdateMetadataCmd, which keeps the same cadence for the
// TUI's own half.
func tickInterval() time.Duration {
	if tmux.EmulatorEnabled() {
		return 3 * time.Second
	}
	return 500 * time.Millisecond
}

// livenessSource names the path a liveness result reached applyLiveness
// by, for its logs: the health tick's probe or a pane's Dead event.
type livenessSource string

const (
	fromTick      livenessSource = "tick"
	fromDeadEvent livenessSource = "dead_event"
)

// ProbeResult is one instance's health probe: its tmux liveness and, for
// a live one, whether its diff refresh failed. Exported so the TUI's tests
// can deliver a HealthResult.
type ProbeResult struct {
	Instance *session.Instance
	TmuxLive tmux.Liveness
	DiffErr  error
}

// HealthResult is a health tick probe's result (Tick).
type HealthResult struct {
	Results []ProbeResult
}

// DeadVerified is the probe a pane's Dead event asks for (VerifyDead): a
// dead attach PTY does not always mean a dead session.
type DeadVerified struct {
	Instance *session.Instance
	TmuxLive tmux.Liveness
}

// tickInst runs the health tick's model half: it queues the probe of every
// active instance (liveness, parity, diff stats; its result is applied by
// deliverHealth, which ends with HealthChecked) and every background job
// that is due: the roster query, the hook scan, the GitHub poll, the
// accounts file check and the usage probe. selected are the instances
// SetSelection named (each client's selected row), whose full diff the
// probe refreshes. In event mode (the
// emulator path) the tick is a slow belt-and-braces sweep, because status
// rides pane events; on the snapshot path it keeps the legacy 500ms
// cadence. Formerly the lifecycle half of the tickUpdateMetadataMessage
// case.
func (m *Model) tickInst(selected []*session.Instance) {
	active := m.activeInstances()
	// Fan out I/O off the model's goroutine. A stalled tmux or git process
	// must not block it: the probe waits for its goroutines inside the job.
	m.spawnBackground(probeJob(active, selected, m.takeDirty(), m.ghBases))

	// One `claude agents --json` for the whole fleet (~100ms, off the
	// loop goroutine), on its OWN cadence rather than the tick's —
	// this tick runs at 500ms on the snapshot path, which would keep a
	// claude process alive most of the time. Claude reports its own
	// busy/idle/waiting state, which beats inferring it from pane text
	// (see rosterStatusFor). nil when not due, already in flight, or no
	// Claude agent is running.
	m.maybeRosterQuery(active)

	// Hook events: the backstop behind the output and quiet triggers
	// (see maybeHookScan). nil when not due, in flight, or no Claude
	// agent is live.
	m.maybeHookScan(active)

	// GitHub PR/issue state + base-branch fetch, on the poller's own
	// 60s cadence (see maybeGHQuery). nil when not due, in flight, or
	// gh is known unavailable.
	m.maybeGHQuery()

	// accounts.json, which another loom or a `loom account` run may have
	// changed: one stat per tick, a reread only when it moved.
	m.maybeReloadAccounts()

	// Account plan usage, on its own 2-minute cadence (see
	// maybeUsageProbe). nil when not due, in flight, or no extra
	// account is registered.
	m.maybeUsageProbe()

	// Claude temp-dir sweeps queued by workspace loads (see
	// requestClaudeTmpSweep). Nothing when none is queued or one runs.
	m.maybeClaudeTmpSweep()
}

// Tick runs the health tick's model half (tickInst), refreshing the full
// diff of every selected instance (SetSelection) that a loaded workspace
// holds. The loop's timer calls it.
func (m *Model) Tick() { m.tickInst(m.selectedInstances()) }

// selectedInstances are the selected instances (SetSelection) a loaded
// workspace holds, in the order named.
func (m *Model) selectedInstances() []*session.Instance {
	var out []*session.Instance
	for _, id := range m.selected {
		if inst, _ := m.lookup(id); inst != nil {
			out = append(out, inst)
		}
	}
	return out
}

// probeJob fans out the per-instance I/O (tmux liveness, parity, git
// diffs) across goroutines and waits for all of them inside the job, so a
// stalled subprocess delays the next tick instead of freezing anything.
//
// Diff refresh is gated on pane output (Instance.ShouldRefreshDiff over
// the dirty set MarkOutput fills): an idle instance with no output does
// not run git on every tick. For N active instances with one active
// agent, the git fan-out drops from about N subprocesses per tick to one.
// On the snapshot path the TUI's status scan reports output through
// MarkOutput, so a change it sees refreshes the diff on the next tick.
// Formerly gatherMetadataCmd, minus the pane reads, which are the TUI's.
func probeJob(active []*session.Instance, selected []*session.Instance, dirty map[string]bool, bases map[string]string) Job {
	return func() any {
		results := make([]ProbeResult, len(active))
		var wg sync.WaitGroup
		for i, inst := range active {
			wg.Add(1)
			go func(idx int, instance *session.Instance) {
				defer wg.Done()
				r := &results[idx]
				r.Instance = instance
				r.TmuxLive = instance.Pane().TmuxLiveness()
				if r.TmuxLive != tmux.LivenessAlive {
					return
				}
				// Parity must not sit behind ShouldRefreshDiff: that gate
				// is about session output, but the base branch moves
				// without any session activity at all — "you are now N
				// behind main" is exactly the case where tmuxUpdated is
				// false. One local rev-list, no network.
				instance.UpdateParity(bases[instance.Path])

				wantFull := slices.Contains(selected, instance)
				if !instance.ShouldRefreshDiff(dirty[instance.Pane().TmuxSessionName()], wantFull) {
					return
				}
				if wantFull {
					r.DiffErr = instance.UpdateDiffStats()
				} else {
					r.DiffErr = instance.UpdateDiffStatsShort()
				}
			}(i, inst)
		}
		wg.Wait()
		return HealthResult{Results: results}
	}
}

// deliverHealth applies a health probe: dead sessions are paused (or a
// workspace terminal relaunched), and Claude's reported status applies to
// the live ones. Hook scans and roster answers already moved them when
// they landed; applying the report here again covers a move TransitionTo
// refused then. TransitionTo still validates, so an illegal transition is
// rejected rather than forced. The TUI's snapshot-path ladder never
// overrides a reported status (it asks adoptClaudeStatus first).
func (m *Model) deliverHealth(r HealthResult) {
	var aliveIDs []InstanceID
	for _, p := range r.Results {
		// The probe only probes active instances, but a kill, pause or
		// resume confirmed while it ran may have moved one to Deleting or
		// Loading since, and that flow owns it now. Skip liveness as well as
		// status: a dead answer would pause it (or relaunch a workspace
		// terminal) under the op, and an alive one would ask for a client
		// repair. A reported status would move it off Deleting or Loading,
		// reopening the busy gate and keeping a dying record persistable.
		if !statusEligible(p.Instance) {
			continue
		}
		if !m.applyLiveness(p.Instance, p.TmuxLive, fromTick) {
			continue
		}
		// Only a probe that answered alive asks for a client repair: an
		// inconclusive one (LivenessUnknown) says nothing about the
		// session, and applyLiveness left the instance untouched.
		if p.TmuxLive == tmux.LivenessAlive {
			aliveIDs = append(aliveIDs, m.idOf(p.Instance))
		}
		if target, authoritative := m.adoptClaudeStatus(p.Instance); authoritative {
			if err := p.Instance.TransitionTo(target); err != nil {
				log.For("core").Warn("tick.transition_failed", "instance", p.Instance.Title, "to", target.String(), "err", err.Error())
			}
		}
		if p.DiffErr != nil {
			log.For("core").Warn("diff_stats_update_failed", "err", p.DiffErr)
		}
	}
	m.emit(StatusesChanged{})
	m.emit(Alive{IDs: aliveIDs, Source: string(fromTick)})
	m.emit(HealthChecked{})
}

// verifyDeadInst returns the probe a pane's Dead event asks for, on inst's
// tmux session (a DeadVerified result). A dead attach PTY does not always
// mean a dead session: a failed reattach leaves the session alive, and a
// session relaunched under the same name leaves the old client's pump at
// EOF. The probe tells pause-the-instance from repair-the-client.
func (m *Model) verifyDeadInst(inst *session.Instance) Job {
	return func() any {
		return DeadVerified{Instance: inst, TmuxLive: inst.Pane().TmuxLiveness()}
	}
}

// VerifyDead queues the probe a pane's Dead event asks for on id's session
// (verifyDeadInst); its result is a DeadVerified as before.
func (m *Model) VerifyDead(id InstanceID) {
	if inst, _ := m.lookup(id); inst != nil {
		m.spawnBackground(m.verifyDeadInst(inst))
	}
}

// deliverDeadVerified applies a Dead event's probe like a tick's, for one
// instance.
func (m *Model) deliverDeadVerified(r DeadVerified) {
	if !statusEligible(r.Instance) {
		return
	}
	alive := m.applyLiveness(r.Instance, r.TmuxLive, fromDeadEvent)
	m.emit(StatusesChanged{})
	if alive && r.TmuxLive == tmux.LivenessAlive {
		m.emit(Alive{IDs: []InstanceID{m.idOf(r.Instance)}, Source: string(fromDeadEvent)})
	}
	m.emit(InstancesChanged{})
}

// applyLiveness reacts to one instance's health-probe result: dead tmux →
// pause (or restart a workspace terminal, with the existing circuit
// breaker). A live session's client repair is the TUI's: deliverHealth
// and deliverDeadVerified report the live ones in an Alive event. It
// returns false when the instance was found dead (so callers can stop
// treating it as running) or is no longer in any loaded workspace. source
// names the path the result came from, for the logs. Must run on the
// loop goroutine.
func (m *Model) applyLiveness(inst *session.Instance, tmuxLive tmux.Liveness, source livenessSource) bool {
	if m.holding(inst) == nil {
		// The probe was taken before inst's workspace was dropped. A
		// workspace-terminal restart here would relaunch one nothing
		// displays. Drop the result.
		return false
	}
	if tmuxLive == tmux.LivenessUnknown {
		// The probe never got an answer, which says nothing about the
		// session — under load it is simply what a starved subprocess
		// looks like. Acting on it would pause a healthy agent, and
		// because that same load starves every instance's probe at once,
		// it would do so across the whole fleet simultaneously. Leave
		// the instance untouched; the next tick re-probes.
		log.For("core").Debug("tick.tmux_probe_inconclusive", "title", inst.Title, "source", source)
		return true
	}
	if tmuxLive != tmux.LivenessAlive {
		if inst.IsWorkspaceTerminal {
			if failures := inst.RecordRestartFailure(); failures >= maxWorkspaceTerminalRestartFailures {
				// The session died again immediately after every
				// recent Restart (e.g. a permanently broken Program
				// string) — restarting further would just loop
				// forever at tick cadence. Give up like a regular
				// instance would. RestartWithOptions/Resume are both
				// gated off for workspace terminals (see
				// selectedPausedNotWorkspace/selectedResumableNotWorkspace
				// in intents.go), so recovering today means killing
				// this instance (a fresh one is auto-created from
				// current config on next workspace activation) or
				// fixing Program on disk and relaunching Loom.
				log.For("core").Error("workspace_terminal.restart_circuit_tripped", "title", inst.Title, "consecutive_failures", failures)
				if err := inst.TransitionTo(session.Paused); err != nil {
					log.For("core").Warn("tick.transition_failed", "instance", inst.Title, "to", "Paused", "err", err.Error())
				}
				return false
			}
			log.For("core").Warn("workspace_terminal.tmux_died_restarting", "title", inst.Title, "source", source)
			if err := inst.Restart(); err != nil {
				log.For("core").Error("workspace_terminal.restart_failed", "title", inst.Title, "err", err)
				return false
			}
			m.emit(SessionLaunched{ID: m.idOf(inst)})
			return false
		}
		log.For("core").Warn("tick.tmux_gone_marking_paused", "title", inst.Title, "source", source)
		if err := inst.TransitionTo(session.Paused); err != nil {
			log.For("core").Warn("tick.transition_failed", "instance", inst.Title, "to", "Paused", "err", err.Error())
		}
		return false
	}
	inst.ResetRestartFailures()
	return true
}

// MarkOutput records that a session produced output since the last health
// tick — the tick uses this to gate diff-stat refreshes.
func (m *Model) MarkOutput(sessionName string) {
	if m.dirtySessions == nil {
		m.dirtySessions = make(map[string]bool)
	}
	m.dirtySessions[sessionName] = true
}

// takeDirty returns the dirty-set and resets it for the next tick window.
func (m *Model) takeDirty() map[string]bool {
	d := m.dirtySessions
	m.dirtySessions = make(map[string]bool)
	return d
}
