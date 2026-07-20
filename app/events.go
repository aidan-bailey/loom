package app

import (
	"time"

	"github.com/aidan-bailey/loom/session"
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
// recorded pending ratios and no tick is already in flight (mirrors
// maybeRedetect's dedupe). This is a THROTTLE, not a trailing-edge
// debounce: the tick is armed on the first keystroke of a burst and
// fires ratioSaveDelay later regardless of further keystrokes, so a
// continuous burst flushes mid-burst every 750ms (bounded write rate)
// rather than waiting for the burst to end. Called from handleScriptDone
// — the deferred script action that runs resizeSplit cannot return a
// tea.Cmd itself, so the tick is armed where Cmds flow. Must be called
// on the Update goroutine (pendingRatioSaves/ratioSaveArmed are
// unsynchronized).
func (m *home) maybeArmRatioSave() tea.Cmd {
	if len(m.pendingRatioSaves) == 0 || m.ratioSaveArmed {
		return nil
	}
	m.ratioSaveArmed = true
	return tea.Tick(ratioSaveDelay, func(time.Time) tea.Msg {
		return ratioSaveMsg{}
	})
}

// deadVerifiedMsg carries the background has-session probe triggered by a
// ptyDeadMsg. A dead attach PTY does not always mean a dead session (a
// failed reattach leaves the session alive), so the probe distinguishes
// pause-the-instance from repair-the-ptmx.
type deadVerifiedMsg struct {
	instance  *session.Instance
	tmuxAlive bool
	ptmxAlive bool
}

func verifyDeadCmd(inst *session.Instance) tea.Cmd {
	return func() tea.Msg {
		return deadVerifiedMsg{instance: inst, tmuxAlive: inst.TmuxAlive(), ptmxAlive: inst.PtmxAlive()}
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
