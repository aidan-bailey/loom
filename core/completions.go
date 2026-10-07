package core

import (
	"fmt"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// Async lifecycle completions (start, recover, resume) run for seconds with
// the TUI live: by the time their result arrives the user may have
// switched workspace, or closed the one the instance belongs to. Their
// handlers therefore act on the workspace that owns the instance — stamped
// into the result at dispatch — and on the instance by identity, never on
// the focused workspace's list, storage or selection. (Kill, pause and
// transition failures carry the instance and act by identity; the TUI's
// pane clients of a killed, paused or reverted instance are closed by the
// prune that follows each of them, ClientsStale.) A completion whose owner
// was closed meanwhile attaches nothing (nothing displays it); one landing
// in a loaded workspace attaches the instance's client (the TUI's
// replacePane, on SessionLaunched, Started or Recovered).
//
// Nor do they move the focused workspace's selection while another flow is
// on screen: a creation flow, an inline attach or a prompt acts on the
// selection, so moving it would retarget the flow. The model has no focus,
// so that rule is the TUI's job (app.applyStarted, app.applyRecovered).

// StartResult is a start job's result (startInst): the instance, Start's
// error, and the workspace that owned it at dispatch.
type StartResult struct {
	Instance *session.Instance
	Owner    *Workspace
	Err      error
}

// ResumeResult is a resume that succeeded (resumeIfLoadingInst).
// Notice, when set, is what it found that the user must see: a stash it
// forgot or could not drop (session.Notice). A failed resume is an
// OpFailed.
type ResumeResult struct {
	Instance *session.Instance
	Owner    *Workspace
	Notice   error
}

// RecoverResult is a recover job's result (Recover). Placeholder is the
// Recoverable row it adopts, OldTitle its title; Recovered is the adopted
// instance, or Err why adoption failed.
type RecoverResult struct {
	Placeholder *session.Instance
	Owner       *Workspace
	OldTitle    string
	Recovered   *session.Instance
	Err         error
}

// KillResult is a finished kill (Kill): tmux, worktree and branch are
// gone, or best-effort gone with the failure logged. Notice is what the
// kill could not finish but the user must see (a stash entry it could
// not drop).
type KillResult struct {
	Instance *session.Instance
	Title    string
	Notice   error
}

// PauseResult is a finished pause (Pause).
type PauseResult struct {
	Title string
}

// OpFailed is a kill, discard, pause or resume that failed: the instance
// goes back to Previous so the user can retry.
type OpFailed struct {
	Instance *session.Instance
	Title    string
	Op       string
	Previous session.Status
	Err      error
}

// MergeResult is a merge job's result (Merge).
type MergeResult struct {
	Err error
}

// resumeSkipped is a resume whose job found the instance no longer
// Loading when it ran (resumeIfLoadingInst): something moved it after the
// caller's transition, and that move owns it now, so nothing is reverted.
// It is only logged, as the skip always was (no notice), and its Reply
// carries err, so a requester never reads a skip as a success.
type resumeSkipped struct{ err error }

// promptFailed is a prompt send that failed (SendPrompt).
type promptFailed struct{ err error }

// promptSent is a started instance's initial prompt, sent or (logged)
// failed (sendInitialPrompt). owner is the start's owner, as deliverStart
// resolved it.
type promptSent struct {
	inst  *session.Instance
	owner *Workspace
}

// adoptIntoReopened swaps inst in for its reopened twin (reopenedTwin) and
// returns the reopened workspace, or nil when inst's tmux session did not
// survive the reopen: a session already up when the reopen reconciled the
// Loading record was killed there (ActionKillAndPause), so the twin's
// record is the truth and inst stays with its closed owner. The probe runs
// a tmux subprocess, but only on this rare path.
func (m *Model) adoptIntoReopened(twin *session.Instance, reopened *Workspace, inst *session.Instance) *Workspace {
	if !inst.Pane().TmuxAlive() {
		return nil
	}
	reopened.replace(twin, inst)
	return reopened
}

// reopenedTwin finds, for an instance whose owner workspace was dropped
// while it started, the copy a reopened tab of the same workspace loaded
// from the record the start left behind. Reconcile turns that
// Loading record into a Paused instance with no attach client
// (ActionMarkPaused, or ActionKillAndPause if the session was already up),
// so the twin is matched on the record's identity: the same title,
// worktree path and (when both are known) branch — which rules out an
// unrelated same-titled session — plus Paused (a paused instance has no
// pane client). nil when there is none.
func (m *Model) reopenedTwin(owner *Workspace, inst *session.Instance) (*session.Instance, *Workspace) {
	wt := inst.GetWorktreePath()
	if wt == "" {
		return nil, nil
	}
	for _, s := range m.Loaded() {
		if s.Label() != owner.Label() {
			continue
		}
		twin := s.byTitle(inst.Title)
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

// owningWorkspace resolves the workspace an async completion belongs to:
// the workspace stamped at dispatch, or — for an unstamped result — the
// loaded workspace that holds inst. nil when neither is known.
func (m *Model) owningWorkspace(stamped *Workspace, inst *session.Instance) *Workspace {
	if stamped != nil {
		return stamped
	}
	return m.holding(inst)
}

// removeEverywhere removes inst (by identity) from every loaded
// workspace. Async op completions land on whatever workspace the TUI
// focuses at delivery time, which may not be the workspace that owns the
// instance — instance lists are in-memory until restart, so a missed
// removal would orphan the row (e.g. stuck in Deleting) with its backing
// resources already gone. Formerly app.removeInstanceEverywhere.
func (m *Model) removeEverywhere(inst *session.Instance) {
	if inst == nil {
		return
	}
	for _, ws := range m.Loaded() {
		ws.remove(inst)
	}
}

// deliverStart applies an async start's result to the workspace that owns
// the instance (see the note at the top of this file).
//
//   - Failure: the instance is removed from its owner by identity, the
//     owner saved, and the instance killed (worktree, tmux) off the
//     model's goroutine.
//   - Success: the owner is saved and the pending prompt (N flow) sent by
//     a job; both belong to the instance, wherever it lives. Started tells
//     the TUI, which attaches the instance's client when the owner is
//     loaded and moves its selection only when that can't retarget a flow.
//     With a prompt, Started waits for the send (deliverPromptSent): the
//     TUI's inline attach forwards keys to the agent, and a key typed
//     before the prompt's paste and Enter would join the prompt.
//   - Owner closed and its workspace reopened meanwhile: the reopened
//     workspace reconciled the record into a Paused twin, which the
//     instance replaces on success if its tmux session survived the
//     reopen (adoptIntoReopened); on failure the twin's record owns the
//     worktree and branch, so nothing is killed.
//
// Formerly app.handleInstanceStarted's model half.
func (m *Model) deliverStart(r StartResult) {
	inst := r.Instance
	owner := m.owningWorkspace(r.Owner, inst)
	if owner != nil && !m.IsLoaded(owner) {
		if twin, reopened := m.reopenedTwin(owner, inst); twin != nil {
			if r.Err != nil {
				m.notifyErr(r.Err)
				return
			}
			if adopted := m.adoptIntoReopened(twin, reopened, inst); adopted != nil {
				owner = adopted
			}
		}
	}
	loaded := m.IsLoaded(owner)

	if r.Err != nil {
		// The save's error first: the start's is the one the error bar
		// keeps, as when both were set in this order before.
		if owner != nil {
			owner.remove(inst)
			if err := m.Save(owner); err != nil {
				m.notifyErr(err)
			}
		}
		m.notifyErr(r.Err)
		m.emit(InstancesChanged{})
		m.spawn(killUnstarted(inst))
		return
	}

	if owner != nil {
		if err := m.Save(owner); err != nil {
			m.notifyErr(err)
			return
		}
	}
	if prompt := inst.Prompt(); prompt != "" {
		inst.SetPrompt("")
		m.spawn(sendInitialPrompt(inst, owner, prompt))
		return
	}
	m.emit(Started{ID: m.idOf(inst), Title: inst.Title, Owner: owner, Loaded: loaded})
}

// deliverPromptSent finishes a start whose initial prompt was sent first
// (deliverStart): only now does the TUI hear of it (Started). The owner may
// have closed while the prompt was sent, so whether it is still loaded is
// asked again.
func (m *Model) deliverPromptSent(r promptSent) {
	m.emit(Started{ID: m.idOf(r.inst), Title: r.inst.Title, Owner: r.owner, Loaded: m.IsLoaded(r.owner)})
}

// deliverResume finishes a resume. The owner may have been closed while
// it ran, so an instance no loaded workspace holds is swapped into a
// reopened copy of its workspace when there is one and its session
// survived the reopen (adoptIntoReopened). An instance a loaded workspace
// holds gets a fresh pane client (SessionLaunched); one nothing holds
// displays nothing and gets none. Formerly app.handleResumeDone.
func (m *Model) deliverResume(r ResumeResult) {
	m.notifyErr(r.Notice)
	if inst := r.Instance; inst != nil && m.holding(inst) == nil {
		var adopted *Workspace
		if r.Owner != nil {
			if twin, reopened := m.reopenedTwin(r.Owner, inst); twin != nil {
				adopted = m.adoptIntoReopened(twin, reopened, inst)
			}
		}
		if adopted != nil {
			if err := m.Save(adopted); err != nil {
				m.notifyErr(err)
			}
			m.notifyInfo(fmt.Sprintf("%s resumed in %s", inst.Title, adopted.Label()))
		}
	}
	if inst := r.Instance; inst != nil && m.holding(inst) != nil {
		m.emit(SessionLaunched{ID: m.idOf(inst)})
	}
	m.emit(InstancesChanged{Relayout: true})
}

// deliverRecover puts the adopted instance in its placeholder's row in
// the workspace that owns it, by identity, so the order and the
// selection's row are unchanged, and saves. A failure reverts the
// placeholder to Recoverable so the user can retry r. An adoption whose
// owner was closed meanwhile is saved to the closed owner's storage
// unless its workspace has been reopened (Save skips a stale copy then);
// nothing is lost either way: the adopted session keeps running on its
// worktree, which the reopened workspace's orphan discovery re-offers as
// Recoverable. Formerly app.handleRecoverDone's model half.
func (m *Model) deliverRecover(r RecoverResult) {
	owner := m.owningWorkspace(r.Owner, r.Placeholder)
	if r.Err != nil {
		// Put the row back into Recoverable so the user can retry r
		// (Recover flipped it to Loading for the spinner).
		if r.Placeholder != nil {
			if terr := r.Placeholder.TransitionTo(session.Recoverable); terr != nil {
				log.For("core").Warn("recover.revert_failed", "title", r.OldTitle, "err", terr)
			}
		}
		m.notifyErr(fmt.Errorf("recover %s: %w", r.OldTitle, r.Err))
		return
	}
	loaded := m.IsLoaded(owner)
	if owner != nil {
		if !owner.replace(r.Placeholder, r.Recovered) {
			owner.add(r.Recovered)
		}
		if err := m.Save(owner); err != nil {
			log.For("core").Error("recover.save_failed", "title", r.Recovered.Title, "err", err)
		}
	}
	m.emit(Recovered{ID: m.idOf(r.Recovered), Title: r.Recovered.Title, Owner: owner, Loaded: loaded,
		Paused: r.Recovered.GetStatus() == session.Paused})
}

// deliverKill removes a killed instance from every loaded workspace, by
// identity: the kill ran for seconds, and the workspace the TUI showed may
// have been switched or closed meanwhile, so a missed removal would leave
// the row stuck in Deleting with its resources already gone. The terminal
// pane's shell was already ended inside the kill's job (Instance.Kill closes
// it by name), and the TUI's prune after ClientsStale releases its client.
func (m *Model) deliverKill(r KillResult) {
	m.removeEverywhere(r.Instance)
	m.notifyErr(r.Notice)
	m.emit(InstancesChanged{})
	m.emit(ClientsStale{})
}

// deliverPause finishes a pause; Pause already stashed, killed the session
// and saved.
func (m *Model) deliverPause(PauseResult) {
	m.emit(InstancesChanged{})
	m.emit(ClientsStale{})
}

// deliverOpFailed reverts the instance of a failed kill, discard, pause or
// resume to its previous status. That status came from the same instance,
// so the reverse transition should always be allowed; if the state machine
// rejects it, log and leave the status as it is rather than mask a real
// bug. The result carries the instance itself, never a title: the
// workspace the TUI shows may have changed since the operation started.
func (m *Model) deliverOpFailed(r OpFailed) {
	if r.Instance != nil {
		if terr := r.Instance.TransitionTo(r.Previous); terr != nil {
			log.For("core").Warn("revert_transition_failed", "err", terr)
		}
		// A reverted kill or pause is active again: the TUI gives it back
		// a client, which a tick may have pruned while it was Deleting or
		// Loading. A no-op unless it is active (a reverted discard is
		// Recoverable, a reverted resume Paused).
		m.emit(Reactivated{ID: m.idOf(r.Instance)})
	}
	log.For("core").Error("op_failed", "op", r.Op, "title", r.Title, "err", r.Err)
	m.notifyErr(r.Err)
	m.emit(InstancesChanged{})
	m.emit(ClientsStale{})
}
