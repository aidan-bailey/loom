package app

import (
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// Pane events, Sent from pump/timer goroutines via tea.Program.Send and
// handled exclusively on the Update goroutine (no model mutation off it).

// paneDirtyMsg: a session's pane emitted output (coalesced ≤ ~60/s).
type paneDirtyMsg struct{ session string }

// paneQuietMsg: a session's pane settled (500ms with no output).
type paneQuietMsg struct{ session string }

// ptyDeadMsg: a session's output pump exited on a genuine PTY EOF.
type ptyDeadMsg struct{ session string }

// bellMsg: a session's pane rang BEL.
type bellMsg struct{ session string }

// instanceForSession resolves a tmux session name (as carried by pane
// events) to the owning instance across every workspace slot, or nil for
// terminal-pane sessions and unknown names.
func (m *home) instanceForSession(name string) *session.Instance {
	if name == "" {
		return nil
	}
	check := func(l *ui.List) *session.Instance {
		for _, inst := range l.GetInstances() {
			if inst.TmuxSessionName() == name {
				return inst
			}
		}
		return nil
	}
	if len(m.slots) > 0 {
		for i, slot := range m.slots {
			l := slot.list
			if i == m.focusedSlot {
				l = m.list
			}
			if inst := check(l); inst != nil {
				return inst
			}
		}
		return nil
	}
	return check(m.list)
}

// statusDetectedMsg carries one instance's settled-content detection result
// back to the Update goroutine (the detection itself runs in a tea.Cmd:
// in-process on the emulator path, but trust-prompt handling can write keys
// to the PTY, and the snapshot fallback shells out — neither belongs on the
// Update goroutine).
type statusDetectedMsg struct {
	instance  *session.Instance
	updated   bool
	hasPrompt bool
	err       error
}

func statusDetectCmd(inst *session.Instance) tea.Cmd {
	return func() tea.Msg {
		updated, hasPrompt, err := inst.CaptureAndProcessStatus()
		return statusDetectedMsg{instance: inst, updated: updated, hasPrompt: hasPrompt, err: err}
	}
}

// redetectMsg re-runs status detection for a session whose last detection
// could not settle the status. Two producers: an updated=true detection
// (content changed since the previous sample, so "settled vs still working"
// is undecidable from one sample) and a quiet event that landed while the
// instance was still Loading (dropped by statusEligible, and quiet never
// re-fires without new output). Without this follow-up the status ladder is
// one-shot per burst and an idle agent latches on Running forever.
type redetectMsg struct{ session string }

// redetectDelay matches tmux's quietDelay: the follow-up sample runs one
// settle-window after the inconclusive one, mirroring the legacy 500ms
// metadata poll's cadence.
const redetectDelay = 500 * time.Millisecond

// maybeRedetect arms a delayed one-shot re-detection for the given session,
// deduped so concurrent quiet events cannot stack parallel re-detect chains.
// Returns nil when one is already pending (or the name is empty). Must be
// called on the Update goroutine (redetectPending is unsynchronized).
func (m *home) maybeRedetect(sessionName string) tea.Cmd {
	if sessionName == "" || m.redetectPending[sessionName] {
		return nil
	}
	if m.redetectPending == nil {
		m.redetectPending = make(map[string]bool)
	}
	m.redetectPending[sessionName] = true
	return tea.Tick(redetectDelay, func(time.Time) tea.Msg {
		return redetectMsg{session: sessionName}
	})
}

// rosterReadyMsg carries one health tick's `claude agents --json` result
// back to the Update goroutine. err set means the query failed (daemon
// down, CLI too old, unparseable output); the handler clears the roster so
// status detection falls back to pane content rather than acting on a
// snapshot that may be minutes stale.
type rosterReadyMsg struct {
	entries map[string]session.RosterEntry
	err     error
}

// rosterQueryCmd schedules one roster query covering the whole fleet.
// Returns nil when no active instance runs Claude, so a fleet of aider or
// shell sessions never pays for a Claude subprocess. The binary is taken
// from a live instance's Program rather than assumed to be "claude" on
// PATH, so absolute paths (a Nix store path, a version-pinned install)
// resolve to the same CLI the agents were launched with.
func rosterQueryCmd(active []*session.Instance) tea.Cmd {
	var program string
	for _, inst := range active {
		if session.IsClaudeProgram(inst.Program) {
			program = inst.Program
			break
		}
	}
	if program == "" {
		return nil
	}
	return func() tea.Msg {
		entries, err := session.QueryClaudeRoster(program, internalexec.Default{})
		return rosterReadyMsg{entries: entries, err: err}
	}
}

// rosterInterval is the roster's own polling cadence. It is deliberately
// NOT the health tick's: that tick fires every 500ms on the snapshot path,
// and a ~380ms subprocess every 500ms keeps a claude process alive ~76% of
// the time purely to poll status — on the one path whose capture-pane
// scraper is fully functional anyway. 3s matches the emulator-path tick,
// which is the cadence the roster was sized for.
const rosterInterval = 3 * time.Second

// maybeRosterQuery returns a roster query when gateRoster is due: none
// already in flight, and at least rosterInterval since the last dispatch.
// The in-flight guard matters because a slow or hung CLI is bounded only
// by claudeRosterTimeout (5s) — without it, ticks would stack concurrent
// subprocesses. Returns nil when nothing should run, including when no
// Claude agent is present. Must be called on the Update goroutine.
func (m *home) maybeRosterQuery(active []*session.Instance) tea.Cmd {
	return m.dispatchGated(gateRoster, time.Now(), func() tea.Cmd {
		return rosterQueryCmd(active)
	})
}

// rosterStatusFor returns Claude's authoritative status for inst, if it
// published one, along with its stated reason for blocking (empty unless
// the status is Prompting, and even then only when the CLI named one).
// The bool is false whenever Loom must fall back to the pane-content
// ladder: a non-Claude agent, an empty or failed roster, no entry for
// this worktree (the join key is the directory Claude runs in), or a
// status string this build does not recognize.
//
// The join is exact string equality on the path. Claude reports a
// symlink-resolved cwd, so a Loom config dir reached through a symlink
// (a dotfiles setup, say) simply produces no match and falls back — a
// silent degradation to the old behavior, never a wrong status.
func (m *home) rosterStatusFor(inst *session.Instance) (session.Status, string, bool) {
	if inst == nil || len(m.roster) == 0 || !session.IsClaudeProgram(inst.Program) {
		return session.Ready, "", false
	}
	entry, ok := m.roster[inst.GetWorktreePath()]
	if !ok {
		return session.Ready, "", false
	}
	status, authoritative := entry.LoomStatus()
	return status, entry.WaitingFor, authoritative
}

// adoptRosterStatus is the single place the roster's answer is applied to
// an instance. It returns what rosterStatusFor decided and, as a side
// effect, records Claude's reason for blocking on the instance so the card
// can render it.
//
// The reason lives exactly as long as the roster-driven wait: any other
// outcome clears it. Both status paths (statusDetectedMsg and
// metadataReadyMsg) must go through here — duplicating the set/clear at
// each call site is how the two drift apart, which is the lockstep hazard
// called out in CLAUDE.md.
func (m *home) adoptRosterStatus(inst *session.Instance) (session.Status, bool) {
	if inst == nil {
		return session.Ready, false
	}
	target, reason, authoritative := m.rosterStatusFor(inst)
	if authoritative && target == session.Prompting {
		inst.SetWaitReason(reason)
	} else {
		inst.SetWaitReason("")
	}
	return target, authoritative
}

// ratioSaveMsg flushes the throttled split-ratio persistence: resizeSplit
// applies ratio changes in-memory per keystroke and only records the
// title→ratio pair in m.pendingRatioSaves; this message drains the map
// into a single mutateUIPrefs write. Without the throttle, key-repeat
// resize (~30/s) would fsync state.json per keystroke on the Update
// goroutine.
type ratioSaveMsg struct{}

// ratioSaveDelay bounds the state.json write rate during a resize burst
// (see maybeArmRatioSave).
const ratioSaveDelay = 750 * time.Millisecond

// maybeArmRatioSave arms the one-shot flush tick when resizeSplit has
// recorded pending ratios and no tick is already in flight (gateRatioSave
// has no interval, so it only dedupes). This is a THROTTLE, not a
// trailing-edge debounce: the tick is armed on the first keystroke of a
// burst and fires ratioSaveDelay later regardless of further keystrokes,
// so a continuous burst flushes mid-burst every 750ms (bounded write
// rate) rather than waiting for the burst to end. Called from
// handleScriptDone — the deferred script action that runs resizeSplit
// cannot return a tea.Cmd itself, so the tick is armed where Cmds flow.
// Must be called on the Update goroutine (pendingRatioSaves is
// unsynchronized).
func (m *home) maybeArmRatioSave() tea.Cmd {
	return m.dispatchGated(gateRatioSave, time.Now(), func() tea.Cmd {
		if len(m.pendingRatioSaves) == 0 {
			return nil
		}
		return tea.Tick(ratioSaveDelay, func(time.Time) tea.Msg {
			return ratioSaveMsg{}
		})
	})
}

// deadVerifiedMsg carries the background has-session probe triggered by a
// ptyDeadMsg. A dead attach PTY does not always mean a dead session (a
// failed reattach leaves the session alive), so the probe distinguishes
// pause-the-instance from repair-the-ptmx.
type deadVerifiedMsg struct {
	instance  *session.Instance
	tmuxLive  tmux.Liveness
	ptmxAlive bool
}

func verifyDeadCmd(inst *session.Instance) tea.Cmd {
	return func() tea.Msg {
		return deadVerifiedMsg{instance: inst, tmuxLive: inst.TmuxLiveness(), ptmxAlive: inst.PtmxAlive()}
	}
}

// statusEligible reports whether the tick/event pipelines may drive this
// instance's status — the same guard set the metadata tick uses (Recoverable
// placeholders and Loading rows are owned by explicit flows; see the comment
// on the tickUpdateMetadataMessage case).
func statusEligible(inst *session.Instance) bool {
	if inst == nil || !inst.Started() || inst.Paused() {
		return false
	}
	st := inst.GetStatus()
	return st != session.Deleting && st != session.Recoverable && st != session.Loading
}

// markDirty records that a session produced output since the last health
// tick — the tick uses this to gate diff-stat refreshes.
func (m *home) markDirty(sessionName string) {
	if m.dirtySessions == nil {
		m.dirtySessions = make(map[string]bool)
	}
	m.dirtySessions[sessionName] = true
}

// takeDirty returns the dirty-set and resets it for the next tick window.
func (m *home) takeDirty() map[string]bool {
	d := m.dirtySessions
	m.dirtySessions = make(map[string]bool)
	return d
}
