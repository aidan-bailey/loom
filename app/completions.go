package app

import (
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// Async lifecycle completions (start, recover) run for seconds with the UI
// live: by the time their message arrives the user may have switched
// workspace, or closed the one the instance belongs to. Their handlers
// therefore act on the slot that owns the instance — stamped into the
// message at dispatch — and on the instance by identity, never on the
// focused slot's list, storage or selection. (Kill, pause, resume and
// transition failures already carry the instance and act by identity.)
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
	if inst == nil {
		return nil
	}
	if slot := m.slotHolding(inst); slot != nil {
		slot.list.RemoveInstance(inst)
	}
	return backgroundKillCmd(inst)
}

// reopenedTwin finds, for an instance whose owner slot was dropped while
// it started, the copy a reopened slot of the same workspace loaded from
// the record the start left behind: a same-titled instance, never
// started and with no attach client, in a loaded slot of that workspace.
// nil when there is none.
func (m *home) reopenedTwin(owner *workspaceSlot, inst *session.Instance) (*session.Instance, *workspaceSlot) {
	for _, s := range m.openSlots() {
		if slotLabel(s) != slotLabel(owner) {
			continue
		}
		if twin := s.list.GetInstanceByTitle(inst.Title); twin != nil && twin != inst && !twin.Started() && !twin.PtmxAlive() {
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

// slotHolding returns the loaded slot whose list holds inst (by identity),
// or nil.
func (m *home) slotHolding(inst *session.Instance) *workspaceSlot {
	if inst == nil {
		return nil
	}
	for _, slot := range m.openSlots() {
		if slices.Contains(slot.list.GetInstances(), inst) {
			return slot
		}
	}
	return nil
}

// slotLoaded reports whether slot is still part of the model: an open tab,
// or the classic/global slot.
func (m *home) slotLoaded(slot *workspaceSlot) bool {
	return slot != nil && slices.Contains(m.openSlots(), slot)
}

// slotLabel names slot's workspace for notices ("global" for none).
func slotLabel(slot *workspaceSlot) string {
	if slot.wsCtx == nil || slot.wsCtx.Name == "" {
		return "global"
	}
	return slot.wsCtx.Name
}

// saveSlot persists slot's list after an async completion changed it. A
// slot that is no longer loaded is saved only if no loaded slot holds the
// same workspace: such a slot reloaded state.json into its own, newer
// copy, which a save from the dropped slot's stale one would overwrite.
func (m *home) saveSlot(slot *workspaceSlot) error {
	if !m.slotLoaded(slot) {
		for _, s := range m.openSlots() {
			if slotLabel(s) == slotLabel(slot) {
				log.For("app").Warn("closed_slot_save_skipped", "workspace", slotLabel(slot), "reason", "workspace_reopened")
				return nil
			}
		}
	}
	return slot.storage.SaveInstances(persistableInstances(slot.list.GetInstances()))
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
//     started, and an owner closed meanwhile gets its preview client
//     released (releaseInstancesCmd).
//   - Owner closed and its workspace reopened meanwhile: the reopened
//     slot loaded the record as a never-started twin, which the instance
//     replaces on success; on failure the twin's record owns the worktree
//     and branch, so nothing is killed and only the preview goes.
func (m *home) handleInstanceStarted(msg instanceStartedMsg) tea.Cmd {
	inst := msg.instance
	owner := m.owningSlot(msg.slot, inst)
	if owner != nil && !m.slotLoaded(owner) {
		if twin, reopened := m.reopenedTwin(owner, inst); twin != nil {
			if msg.err != nil {
				return tea.Batch(m.handleError(msg.err), releaseInstancesCmd([]*session.Instance{inst}))
			}
			reopened.list.ReplaceInstance(twin, inst)
			owner = reopened
		}
	}
	loaded := m.slotLoaded(owner)

	if msg.err != nil {
		var saveErr tea.Cmd
		if owner != nil {
			owner.list.RemoveInstance(inst)
			if err := m.saveSlot(owner); err != nil {
				saveErr = m.handleError(err)
			}
		}
		return tea.Batch(m.handleError(msg.err), saveErr, m.instanceChanged(), backgroundKillCmd(inst))
	}

	var release tea.Cmd
	if !loaded {
		// Nothing displays it: the start attached a preview client that
		// would otherwise stay open until exit.
		release = releaseInstancesCmd([]*session.Instance{inst})
	}
	if owner != nil {
		if err := m.saveSlot(owner); err != nil {
			return tea.Batch(m.handleError(err), release)
		}
	}

	if inst.Prompt != "" {
		if err := inst.SendPrompt(inst.Prompt); err != nil {
			log.For("app").Error("send_prompt_failed", "err", err)
		}
		inst.Prompt = ""
	}

	switch {
	case owner == nil:
		// Unknown owner (unstamped, and no loaded slot holds it): only
		// the persistence-free parts above apply.
	case !loaded:
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s, which is no longer open", inst.Title, slotLabel(owner)))
	case owner != m.workspaceSlot:
		// A background slot's selection drives no open flow.
		owner.list.SelectInstance(inst)
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s", inst.Title, slotLabel(owner)))
	case m.state != stateDefault:
		// Another flow owns the screen and acts on the selection; leave
		// both alone.
		m.errBox.SetInfo(fmt.Sprintf("%s started", inst.Title))
	default:
		m.list.SelectInstance(inst)
		// Auto-focus agent pane and capture input
		m.setPaneFocus(ui.FocusAgent)
		m.splitPane.SetInlineAttach(true)
		m.state = stateInlineAttach
		m.menu.SetState(ui.StateInlineAttach)
	}

	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), release)
}

// handleRecoverDone puts the adopted instance in its placeholder's row in
// the slot that owns it, by identity (see the note above) — in place, so
// the list order and the selection's row are unchanged. It is selected
// only where that can't retarget an open flow. A failure reverts the
// placeholder to Recoverable so the user can retry r. An owner closed
// meanwhile still records the adoption, and gets the adopted instance's
// preview client released.
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

	loaded := m.slotLoaded(owner)
	var release tea.Cmd
	if !loaded {
		release = releaseInstancesCmd([]*session.Instance{msg.recovered})
	}
	if owner != nil {
		if !owner.list.ReplaceInstance(msg.placeholder, msg.recovered) {
			owner.list.AddInstance(msg.recovered)
		}
		if owner != m.workspaceSlot || m.state == stateDefault {
			owner.list.SelectInstance(msg.recovered)
		}
		if err := m.saveSlot(owner); err != nil {
			log.For("app").Error("recover.save_failed", "title", msg.recovered.Title, "err", err)
		}
	}
	// Recovery is otherwise invisible when fast — confirm it, and be
	// explicit about the degraded case where both the tmux session and
	// worktree were already gone and adoption could only mark the
	// record Paused (resume rebuilds the worktree from the branch).
	where := ""
	if owner != nil && owner != m.workspaceSlot {
		where = " in " + slotLabel(owner)
		if !loaded {
			where += ", which is no longer open"
		}
	}
	if msg.recovered.GetStatus() == session.Paused {
		m.errBox.SetInfo(fmt.Sprintf("Recovered '%s'%s as paused — its session and worktree were gone; branch preserved, press r to resume", msg.recovered.Title, where))
	} else {
		m.errBox.SetInfo(fmt.Sprintf("Recovered session '%s'%s", msg.recovered.Title, where))
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), release)
}
