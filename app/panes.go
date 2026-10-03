package app

import (
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// The TUI's pane clients (m.panes, ui.PaneClients) give every live agent
// tmux session one attach client. Everything that renders, scrolls,
// forwards input to or scrapes an agent pane goes through it. Session
// lifecycle never attaches one.
//
// Attach points, all on the Update goroutine:
//   - a workspace load: every active instance of the slot (ensureSlotPanes);
//   - a start, resume or recover landing in a loaded slot, a script's
//     inst:resume() (handleScriptDone), and a workspace terminal's
//     auto-restart: the session was (re)launched or reattached, so any
//     client from before was watching the session it replaced
//     (replacePane);
//   - the health tick's repair, when the session is alive but its client's
//     PTY is gone, a full-screen attach returning, and a failed kill or
//     pause reverting its instance to active (ensurePane).
//
// Only an active instance (activeInstance) gets a client: a paused,
// Recoverable, Loading or Deleting one has nothing to display, and an
// op in flight on it (a kill, a pause) closes its session.
//
// Release points: the health tick, kill, pause, a failed transition and
// every slot drop (prunePanes) release the clients of sessions no loaded
// instance is active on. A release closes the client's PTY off the Update
// goroutine (releaseClientsCmd): PausePreview waits for the client's
// output pump, which blocks in tea.Program.Send until Update returns. A
// client is closed only after Retain or Replace has removed it from the
// registry, so it is never re-attached or closed twice while it closes
// (Ensure builds a new one). Nothing else closes a registered client: a
// kill or pause ends the session and leaves its client to the prune that
// follows the completion.

// paneSnapshot resolves each instance's pane on the Update goroutine, for
// a Cmd that must not read the model.
func (m *home) paneSnapshot(insts []*session.Instance) map[*session.Instance]ui.Pane {
	out := make(map[*session.Instance]ui.Pane, len(insts))
	for _, inst := range insts {
		out[inst] = m.panes.For(inst)
	}
	return out
}

// livePaneNames is the set of tmux session names that should have a
// client: those of the active instances of every loaded slot.
func (m *home) livePaneNames() map[string]bool {
	names := make(map[string]bool)
	for _, inst := range m.activeInstances() {
		if name := inst.Pane().TmuxSessionName(); name != "" {
			names[name] = true
		}
	}
	return names
}

// ensurePane gives inst's session a client, re-attaching one whose PTY is
// gone (ui.PaneClients.Ensure). A no-op for an inactive instance.
func (m *home) ensurePane(inst *session.Instance) {
	name := inst.Pane().TmuxSessionName()
	if name == "" || !activeInstance(inst) {
		return
	}
	if err := m.panes.Ensure(name, inst.Pane().SessionProgram()); err != nil {
		log.For("app").Error("pane.attach_failed", "session", name, "err", err)
	}
}

// replacePane gives inst's session, just (re)launched, a fresh client. It
// returns a Cmd closing the client it replaced, or nil when there was none.
// A no-op for an inactive instance (a recover that could only mark its
// record Paused, say): the next prune closes any client of its name.
func (m *home) replacePane(inst *session.Instance) tea.Cmd {
	name := inst.Pane().TmuxSessionName()
	if name == "" || !activeInstance(inst) {
		return nil
	}
	old, err := m.panes.Replace(name, inst.Pane().SessionProgram())
	if err != nil {
		log.For("app").Error("pane.attach_failed", "session", name, "err", err)
	}
	if old == nil {
		return nil
	}
	return releaseClientsCmd(attachedClients([]*tmux.TmuxSession{old}))
}

// ensureSlotPanes gives every active instance of slot a client.
func (m *home) ensureSlotPanes(slot *workspaceSlot) {
	for _, inst := range slot.list.GetInstances() {
		m.ensurePane(inst)
	}
}

// prunePanes drops the clients of sessions no loaded instance is active
// on, and returns a Cmd closing them.
func (m *home) prunePanes() tea.Cmd {
	return releaseClientsCmd(attachedClients(m.panes.Retain(m.livePaneNames())))
}
