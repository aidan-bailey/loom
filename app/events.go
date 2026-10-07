package app

import (
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
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

// statusDetectedMsg carries one instance's settled-content detection result
// back to the Update goroutine (the detection itself runs in a tea.Cmd:
// in-process on the emulator path, but answering a trust prompt runs
// `tmux send-keys`, and the snapshot fallback shells out to capture-pane —
// neither subprocess belongs on the Update goroutine). id and title name
// the instance scanned.
type statusDetectedMsg struct {
	id        core.InstanceID
	title     string
	updated   bool
	hasPrompt bool
	err       error
}

// statusDetectCmd scans v's pane once, off the Update goroutine. A pane
// with no client has no screen to scan, so there is no Cmd: its zero
// result would read as "settled, no prompt" and move a working agent to
// Ready, and with no message the re-detection chain ends there. The next
// attach and its output resume detection.
func statusDetectCmd(v core.InstanceView, pane ui.Pane) tea.Cmd {
	if pane.Client() == nil {
		return nil
	}
	id, title := v.ID, v.Title
	return func() tea.Msg {
		updated, hasPrompt, err := pane.DetectStatus()
		return statusDetectedMsg{id: id, title: title, updated: updated, hasPrompt: hasPrompt, err: err}
	}
}

// redetectMsg re-runs status detection for a session whose last detection
// could not settle the status. Two producers: an updated=true detection
// (content changed since the previous sample, so "settled vs still working"
// is undecidable from one sample) and a quiet event that landed while the
// instance was still Loading (dropped as inactive, InstanceView.Active, and quiet never
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
// recorded pending ratios and no tick is already in flight
// (ratioTickArmed). This is a THROTTLE, not a
// trailing-edge debounce: the tick is armed on the first keystroke of a
// burst and fires ratioSaveDelay later regardless of further keystrokes,
// so a continuous burst flushes mid-burst every 750ms (bounded write
// rate) rather than waiting for the burst to end. Called from
// handleScriptDone — the deferred script action that runs resizeSplit
// cannot return a tea.Cmd itself, so the tick is armed where Cmds flow.
// Must be called on the Update goroutine (pendingRatioSaves is
// unsynchronized).
func (m *home) maybeArmRatioSave() tea.Cmd {
	if m.ratioTickArmed || len(m.pendingRatioSaves) == 0 {
		return nil
	}
	m.ratioTickArmed = true
	return tea.Tick(ratioSaveDelay, func(time.Time) tea.Msg { return ratioSaveMsg{} })
}

// snapshotStatusMsg carries the snapshot path's status scan back to Update.
type snapshotStatusMsg struct{ results []snapshotStatus }

// snapshotStatus is one pane's scan: whether its content changed and
// whether it shows a prompt. id and title name the instance scanned.
type snapshotStatus struct {
	id        core.InstanceID
	title     string
	updated   bool
	hasPrompt bool
	err       error
}

// snapshotScan returns a Cmd scanning, off the Update goroutine, the
// screen of every active instance whose pane client has no emulator (the
// snapshot path: LOOM_PANE_RENDERER=snapshot, or Windows); nil when there
// is none, or a scan is still running. With the emulator, quiet events
// drive the ladder (statusDetectedMsg), and a pane with no client has no
// screen to scan and no opinion. Formerly gatherMetadataCmd's capture.
func (m *home) snapshotScan() tea.Cmd {
	if m.snapshotScanning {
		return nil
	}
	type target struct {
		id    core.InstanceID
		title string
		pane  ui.Pane
	}
	var targets []target
	for _, v := range m.activeViews() {
		if pane := m.panes.For(&v); pane.Client() != nil && !pane.HasEmulator() {
			targets = append(targets, target{v.ID, v.Title, pane})
		}
	}
	if len(targets) == 0 {
		return nil
	}
	m.snapshotScanning = true
	return func() tea.Msg {
		results := make([]snapshotStatus, len(targets))
		var wg sync.WaitGroup
		for i, t := range targets {
			wg.Add(1)
			go func(i int, t target) {
				defer wg.Done()
				r := &results[i]
				r.id, r.title = t.id, t.title
				r.updated, r.hasPrompt, r.err = t.pane.DetectStatus()
			}(i, t)
		}
		wg.Wait()
		return snapshotStatusMsg{results: results}
	}
}
