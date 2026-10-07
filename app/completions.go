package app

import (
	"fmt"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// The model (core) applies async lifecycle completions by identity and on
// the workspace stamped at dispatch. What is left here depends on what the
// TUI shows: which slot is focused, and m.state. A completion never moves
// the focused slot's selection while another flow is on screen
// (m.state != stateDefault): a creation flow, an inline attach or a prompt
// acts on the selection, so moving it would retarget the flow.

// applyStarted follows a successful start in the view: the instance's
// client is attached when its owner is loaded, and the selection moves to
// it (focusing the agent pane in inline attach) only when its owner is the
// focused slot and no other flow is on screen; otherwise a notice says
// where it started. Formerly app.handleInstanceStarted's view half.
func (m *home) applyStarted(ev core.Started) tea.Cmd {
	owner := ev.Owner
	// The row, nil when no open slot shows the instance (its owner closed).
	v, holder := m.viewByID(ev.ID)
	var attach tea.Cmd
	if ev.Loaded {
		attach = m.replacePane(v)
	}
	switch {
	case owner == 0:
		// Unknown owner (unstamped, and no loaded workspace holds it):
		// only the model's half applies.
	case !ev.Loaded:
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s, %s", ev.Title, ev.OwnerLabel, ev.ClosedNote))
	case owner != m.id:
		// A background slot's selection drives no open flow.
		if s := m.slotFor(owner); s != nil {
			s.list.SelectID(ev.ID)
		}
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s", ev.Title, ev.OwnerLabel))
	case m.state != stateDefault || holder != m.workspaceSlot || !v.Active():
		// Another flow owns the screen and acts on the selection; leave
		// both alone. (The second test is a belt: the owner is stamped by
		// identity, so a focused owner holds inst.) Nor is an instance
		// attached unless it is what a start leaves behind (active:
		// started and Running, Ready or Prompting), the same test that
		// gives it a client (replacePane). Started waits for an N flow's
		// initial prompt to be sent, and the instance is already Running
		// then, so the user can confirm a kill (Deleting) or a pause
		// (Loading, then Paused) meanwhile. Inline attach, which looks the
		// selection up per key, would have them typing into a session
		// being torn down, and into the neighbouring one once a kill
		// removes it.
		m.errBox.SetInfo(fmt.Sprintf("%s started", ev.Title))
	default:
		m.list.SelectID(ev.ID)
		// Auto-focus agent pane and capture input
		m.setPaneFocus(ui.FocusAgent)
		m.splitPane.SetInlineAttach(true)
		m.state = stateInlineAttach
		m.menu.SetState(ui.StateInlineAttach)
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), attach)
}

// applyRecovered follows an adoption in the view: the recovered instance
// is selected where that can't retarget an open flow, its client attached
// when its owner is loaded, and the recovery confirmed, since it is
// otherwise invisible when fast. The degraded case is spelled out: when
// the tmux session and worktree were both already gone, adoption could only
// mark the record Paused, and resume rebuilds the worktree from the branch.
// Formerly app.handleRecoverDone's view half.
func (m *home) applyRecovered(ev core.Recovered) tea.Cmd {
	owner := ev.Owner
	if owner != 0 && (owner != m.id || m.state == stateDefault) {
		if s := m.slotFor(owner); s != nil {
			s.list.SelectID(ev.ID)
		}
	}
	var attach tea.Cmd
	if ev.Loaded {
		v, _ := m.viewByID(ev.ID)
		attach = m.replacePane(v)
	}
	where := ""
	if owner != 0 && owner != m.id {
		where = " in " + ev.OwnerLabel
		if !ev.Loaded {
			where += ", " + ev.ClosedNote
		}
	}
	if ev.Paused {
		m.errBox.SetInfo(fmt.Sprintf("Recovered '%s'%s as paused — its session and worktree were gone; branch preserved, press r to resume", ev.Title, where))
	} else {
		m.errBox.SetInfo(fmt.Sprintf("Recovered session '%s'%s", ev.Title, where))
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), attach)
}
