package app

import (
	"fmt"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
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
	inst, owner := ev.Instance, ev.Owner
	var attach tea.Cmd
	if ev.Loaded {
		attach = m.replacePane(inst)
	}
	switch {
	case owner == nil:
		// Unknown owner (unstamped, and no loaded workspace holds it):
		// only the model's half applies.
	case !ev.Loaded:
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s, %s", inst.Title, owner.Label(), m.core.ClosedNote(owner)))
	case owner != m.ws:
		// A background slot's selection drives no open flow.
		if s := m.slotFor(owner); s != nil {
			s.list.SelectInstance(inst)
		}
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s", inst.Title, owner.Label()))
	case m.state != stateDefault || !m.ws.Holds(inst) || inst.GetStatus() == session.Deleting:
		// Another flow owns the screen and acts on the selection; leave
		// both alone. (The second test is a belt: the owner is stamped by
		// identity, so a focused owner holds inst.) Nor is a Deleting
		// instance attached: a kill confirmed while Started was deferred
		// for the initial prompt's send (the instance is already Running
		// then, so kill is allowed) leaves the row Deleting, and inline
		// attach, which looks the selection up per key, would have the user
		// typing into the neighbouring session once the kill removes it.
		m.errBox.SetInfo(fmt.Sprintf("%s started", inst.Title))
	default:
		m.list.SelectInstance(inst)
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
	if owner != nil && (owner != m.ws || m.state == stateDefault) {
		if s := m.slotFor(owner); s != nil {
			s.list.SelectInstance(ev.Instance)
		}
	}
	var attach tea.Cmd
	if ev.Loaded {
		attach = m.replacePane(ev.Instance)
	}
	where := ""
	if owner != nil && owner != m.ws {
		where = " in " + owner.Label()
		if !ev.Loaded {
			where += ", " + m.core.ClosedNote(owner)
		}
	}
	if ev.Instance.GetStatus() == session.Paused {
		m.errBox.SetInfo(fmt.Sprintf("Recovered '%s'%s as paused — its session and worktree were gone; branch preserved, press r to resume", ev.Instance.Title, where))
	} else {
		m.errBox.SetInfo(fmt.Sprintf("Recovered session '%s'%s", ev.Instance.Title, where))
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), attach)
}

// dropPendingNew removes the open creation flow's pending instance from
// its workspace, by identity, and returns a Cmd that kills it off the
// Update goroutine (core's DropUnstarted); nil when no creation flow is
// open. Every creation-flow cancel path goes through it. Killing "the
// selection" or "the last row" instead could reach an unrelated session
// once a completion or removal had moved either mid-flow.
func (m *home) dropPendingNew() tea.Cmd {
	inst := m.pendingNew
	m.pendingNew = nil
	return coreCmd(m.core.DropUnstarted(inst))
}
