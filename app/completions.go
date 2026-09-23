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
//     follow (select, then the prompt overlay or inline attach); a
//     background owner just selects it and says so, and an owner closed
//     meanwhile gets its preview client released (releaseInstancesCmd).
func (m *home) handleInstanceStarted(msg instanceStartedMsg) tea.Cmd {
	inst := msg.instance
	owner := m.owningSlot(msg.slot, inst)
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

	if !msg.promptAfterName && inst.Prompt != "" {
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
		owner.list.SelectInstance(inst)
		m.errBox.SetInfo(fmt.Sprintf("%s started in %s", inst.Title, slotLabel(owner)))
	default:
		m.list.SelectInstance(inst)
		if m.state != stateDefault {
			// Another flow (an overlay, an inline attach) owns the
			// screen now; don't yank it away.
			break
		}
		if msg.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.setOverlay(m.newPromptOverlay(), overlayTextInput)
		} else {
			// Auto-focus agent pane and capture input
			m.setPaneFocus(ui.FocusAgent)
			m.splitPane.SetInlineAttach(true)
			m.state = stateInlineAttach
			m.menu.SetState(ui.StateInlineAttach)
		}
	}

	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), release)
}

// handleRecoverDone swaps a recovered orphan's placeholder for the
// adopted instance in the slot that owns it, by identity (see the note
// above). A failure reverts the placeholder to Recoverable so the user
// can retry r. An owner closed meanwhile still records the adoption, and
// gets the adopted instance's preview client released.
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
		owner.list.RemoveInstance(msg.placeholder)
		owner.list.AddInstance(msg.recovered)
		owner.list.SelectInstance(msg.recovered)
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
