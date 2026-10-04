package app

import (
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// Async lifecycle completions (start, recover, resume) run for seconds with
// the UI live: by the time their message arrives the user may have
// switched workspace, or closed the one the instance belongs to. Their
// handlers therefore act on the slot that owns the instance — stamped into
// the message at dispatch — and on the instance by identity, never on the
// focused slot's list, storage or selection. (Kill, pause and transition
// failures carry the instance and act by identity; the TUI's pane clients
// of a killed, paused or reverted instance are closed by the prune that
// follows each of them.) A completion whose owner was closed meanwhile
// attaches nothing (nothing displays it); one landing in a loaded slot
// attaches the instance's client (replacePane).
//
// Nor do they move the focused slot's selection while another flow is on
// screen (m.state != stateDefault): a creation flow, an inline attach or
// a prompt acts on the selection, so moving it would retarget the flow.

// dropPendingNew removes the open creation flow's pending instance from
// its list, by identity, and returns a Cmd that kills it off the Update
// goroutine; nil when no creation flow is open. Every creation-flow
// cancel path goes through it. Killing "the selection" or "the last row"
// instead could reach an unrelated session once a completion or removal
// had moved either mid-flow.
func (m *home) dropPendingNew() tea.Cmd {
	inst := m.pendingNew
	m.pendingNew = nil
	if inst == nil || inst.Started() {
		// A started instance is no longer pending (its start cleared
		// pendingNew first); never kill a live session from a cancel.
		return nil
	}
	if slot := m.slotHolding(inst); slot != nil {
		slot.ws.Remove(inst)
	}
	return backgroundKillCmd(inst)
}

// adoptIntoReopened swaps inst in for its reopened twin (reopenedTwin) and
// returns the reopened slot, or nil when inst's tmux session did not
// survive the reopen: a session already up when the reopen reconciled the
// Loading record was killed there (ActionKillAndPause), so the twin's
// record is the truth and inst stays with its closed owner. The probe runs
// a tmux subprocess, but only on this rare path.
func (m *home) adoptIntoReopened(twin *session.Instance, reopened *workspaceSlot, inst *session.Instance) *workspaceSlot {
	if !inst.Pane().TmuxAlive() {
		return nil
	}
	reopened.ws.Replace(twin, inst)
	return reopened
}

// reopenedTwin finds, for an instance whose owner slot was dropped while
// it started, the copy a reopened slot of the same workspace loaded from
// the record the start left behind. Reconcile turns that Loading record
// into a Paused instance with no attach client (ActionMarkPaused, or
// ActionKillAndPause if the session was already up), so the twin is
// matched on the record's identity: the same title, worktree path and
// (when both are known) branch — which rules out an unrelated
// same-titled session — plus Paused (a paused instance has no pane
// client). nil when there is none.
func (m *home) reopenedTwin(owner *workspaceSlot, inst *session.Instance) (*session.Instance, *workspaceSlot) {
	wt := inst.GetWorktreePath()
	if wt == "" {
		return nil, nil
	}
	for _, s := range m.openSlots() {
		if s.ws.Label() != owner.ws.Label() {
			continue
		}
		twin := s.list.GetInstanceByTitle(inst.Title)
		if twin == nil || twin == inst || twin.GetWorktreePath() != wt {
			continue
		}
		if b1, b2 := twin.GetBranch(), inst.GetBranch(); b1 != "" && b2 != "" && b1 != b2 {
			continue
		}
		if twin.Paused() {
			return twin, s
		}
	}
	return nil, nil
}

// owningSlot resolves the slot an async completion belongs to: the slot
// stamped at dispatch, or — for an unstamped message — the loaded slot
// whose list holds inst. nil when neither is known.
func (m *home) owningSlot(stamped *workspaceSlot, inst *session.Instance) *workspaceSlot {
	if stamped != nil {
		return stamped
	}
	return m.slotHolding(inst)
}

// startOwner is the slot an instance about to start belongs to, for
// stamping instanceStartedMsg: the loaded slot holding it, or the focused
// slot if none does. Resolved by identity, not assumed to be the focused
// slot — deferred script actions can change focus while a creation flow
// is open.
func (m *home) startOwner(inst *session.Instance) *workspaceSlot {
	if slot := m.slotHolding(inst); slot != nil {
		return slot
	}
	return m.workspaceSlot
}

// handleInstanceStarted applies an async Start's result to the slot that
// owns the instance (see the note above).
//
//   - Failure: the instance is removed from its owner's list by identity,
//     the owner saved, and the instance killed (worktree, tmux) off the
//     Update goroutine.
//   - Success: the owner is saved and the pending prompt (N flow) sent —
//     both belong to the instance, wherever it lives. Only when the owner
//     is the focused slot and no other flow is on screen does the UI
//     follow (select, inline attach); otherwise a notice says where it
//     started. A completion whose owner was closed meanwhile attaches
//     nothing (nothing displays it); one landing in a loaded slot
//     attaches the instance's client (replacePane).
//   - Owner closed and its workspace reopened meanwhile: the reopened
//     slot reconciled the record into a Paused twin, which the instance
//     replaces on success if its tmux session survived the reopen
//     (adoptIntoReopened); on failure the twin's record owns the worktree
//     and branch, so nothing is killed and nothing attaches.
func (m *home) handleInstanceStarted(msg instanceStartedMsg) tea.Cmd {
	inst := msg.instance
	owner := m.owningSlot(msg.slot, inst)
	if owner != nil && !m.core.IsLoaded(owner.ws) {
		if twin, reopened := m.reopenedTwin(owner, inst); twin != nil {
			if msg.err != nil {
				return m.handleError(msg.err)
			}
			if adopted := m.adoptIntoReopened(twin, reopened, inst); adopted != nil {
				owner = adopted
			}
		}
	}
	loaded := owner != nil && m.core.IsLoaded(owner.ws)

	if msg.err != nil {
		var saveErr tea.Cmd
		if owner != nil {
			owner.ws.Remove(inst)
			if err := m.core.Save(owner.ws); err != nil {
				saveErr = m.handleError(err)
			}
		}
		return tea.Batch(m.handleError(msg.err), saveErr, m.instanceChanged(), backgroundKillCmd(inst))
	}

	if owner != nil {
		if err := m.core.Save(owner.ws); err != nil {
			return m.handleError(err)
		}
	}

	if prompt := inst.Prompt(); prompt != "" {
		if err := inst.Pane().SendPrompt(prompt); err != nil {
			log.For("app").Error("send_prompt_failed", "err", err)
		}
		inst.SetPrompt("")
	}

	var attach tea.Cmd
	if loaded {
		attach = m.replacePane(inst)
	}

	switch {
	case owner == nil:
		// Unknown owner (unstamped, and no loaded slot holds it): only
		// the persistence-free parts above apply.
	case !loaded:
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s, %s", inst.Title, owner.ws.Label(), m.core.ClosedNote(owner.ws)))
	case owner != m.workspaceSlot:
		// A background slot's selection drives no open flow.
		owner.list.SelectInstance(inst)
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s", inst.Title, owner.ws.Label()))
	case m.state != stateDefault || !slices.Contains(m.list.GetInstances(), inst):
		// Another flow owns the screen and acts on the selection; leave
		// both alone. (The second test is a belt: the owner is stamped by
		// identity, so a focused owner holds inst.)
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

// handleResumeDone finishes a resume. The owner may have been closed while
// it ran. An instance no loaded slot holds is therefore swapped into a
// reopened copy of its workspace when there is one and its session
// survived the reopen (adoptIntoReopened). A completion whose owner was
// closed meanwhile attaches nothing (nothing displays it); one landing in
// a loaded slot attaches the instance's client (replacePane).
func (m *home) handleResumeDone(msg resumeDoneMsg) tea.Cmd {
	cmds := []tea.Cmd{tea.RequestWindowSize}
	if msg.notice != nil {
		cmds = append(cmds, m.handleError(msg.notice))
	}
	if inst := msg.instance; inst != nil && m.slotHolding(inst) == nil {
		var adopted *workspaceSlot
		if msg.slot != nil {
			if twin, reopened := m.reopenedTwin(msg.slot, inst); twin != nil {
				adopted = m.adoptIntoReopened(twin, reopened, inst)
			}
		}
		if adopted != nil {
			if err := m.core.Save(adopted.ws); err != nil {
				cmds = append(cmds, m.handleError(err))
			}
			m.errBox.SetInfo(fmt.Sprintf("%s resumed in %s", inst.Title, adopted.ws.Label()))
		}
	}
	if inst := msg.instance; inst != nil && m.slotHolding(inst) != nil {
		cmds = append(cmds, m.replacePane(inst))
	}
	return tea.Batch(append(cmds, m.instanceChanged())...)
}

// handleRecoverDone puts the adopted instance in its placeholder's row in
// the slot that owns it, by identity (see the note above) — in place, so
// the list order and the selection's row are unchanged. It is selected
// only where that can't retarget an open flow. A failure reverts the
// placeholder to Recoverable so the user can retry r. A completion whose
// owner was closed meanwhile attaches nothing (nothing displays it); one
// landing in a loaded slot attaches the instance's client (replacePane).
// An adoption whose owner was closed meanwhile is saved to the closed
// owner's storage unless the workspace has since been reopened (core.Model.Save
// skips a stale copy then).
// In that case nothing is lost: the adopted session keeps running on its
// worktree, which the reopened slot's orphan discovery re-offers as
// Recoverable, so r there adopts it again.
func (m *home) handleRecoverDone(msg recoverDoneMsg) tea.Cmd {
	owner := m.owningSlot(msg.slot, msg.placeholder)
	if msg.err != nil {
		// Put the row back into Recoverable so the user can retry r
		// (runRecoverSelected flipped it to Loading for the spinner).
		if msg.placeholder != nil {
			if terr := msg.placeholder.TransitionTo(session.Recoverable); terr != nil {
				log.For("app").Warn("recover.revert_failed", "title", msg.oldTitle, "err", terr)
			}
		}
		return m.handleError(fmt.Errorf("recover %s: %w", msg.oldTitle, msg.err))
	}

	loaded := owner != nil && m.core.IsLoaded(owner.ws)
	if owner != nil {
		if !owner.ws.Replace(msg.placeholder, msg.recovered) {
			owner.ws.Add(msg.recovered)
		}
		if owner != m.workspaceSlot || m.state == stateDefault {
			owner.list.SelectInstance(msg.recovered)
		}
		if err := m.core.Save(owner.ws); err != nil {
			log.For("app").Error("recover.save_failed", "title", msg.recovered.Title, "err", err)
		}
	}
	var attach tea.Cmd
	if loaded {
		attach = m.replacePane(msg.recovered)
	}
	// Recovery is otherwise invisible when fast — confirm it, and be
	// explicit about the degraded case where both the tmux session and
	// worktree were already gone and adoption could only mark the
	// record Paused (resume rebuilds the worktree from the branch).
	where := ""
	if owner != nil && owner != m.workspaceSlot {
		where = " in " + owner.ws.Label()
		if !loaded {
			where += ", " + m.core.ClosedNote(owner.ws)
		}
	}
	if msg.recovered.GetStatus() == session.Paused {
		m.errBox.SetInfo(fmt.Sprintf("Recovered '%s'%s as paused — its session and worktree were gone; branch preserved, press r to resume", msg.recovered.Title, where))
	} else {
		m.errBox.SetInfo(fmt.Sprintf("Recovered session '%s'%s", msg.recovered.Title, where))
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), attach)
}
