package core

import (
	"errors"
	"fmt"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// startOwner is the workspace an instance about to start or resume
// belongs to, for stamping its result: the loaded workspace holding it,
// or fallback (the workspace the TUI shows) if none does. Resolved by
// identity, not assumed to be the focused workspace — deferred script
// actions can change focus while a creation flow is open. Formerly
// app.startOwner.
func (m *Model) startOwner(inst *session.Instance, fallback *Workspace) *Workspace {
	if ws := m.Holding(inst); ws != nil {
		return ws
	}
	return fallback
}

// StartInst returns the job launching inst (Instance.Start(true)), reporting a
// StartResult stamped with its owner: the loaded workspace holding it,
// else fallback (the workspace the TUI shows). The owner is resolved now,
// by identity, not when the start lands, because a script's deferred
// action can change focus while a creation flow is open. Formerly
// app.startOwner and the creation flows' Async bodies.
func (m *Model) StartInst(inst *session.Instance, fallback *Workspace) Job {
	owner := m.startOwner(inst, fallback)
	return func() any {
		return StartResult{Instance: inst, Owner: owner, Err: inst.Start(true)}
	}
}

// KillInst returns the (synchronous pre-step, job) pair that both
// runKillSelected variants share. preAction flips the instance to
// Deleting; killAction handles I/O off the model's goroutine and returns
// a KillResult, or an OpFailed. ws is the workspace the TUI shows, whose
// storage holds the record. Formerly app.killActionFor.
//
// beforeKill, when set, runs in the job after the checks pass and before
// the kill: the TUI closes its terminal pane's shell for the instance
// there, which it can't hand to the model (the pane is the TUI's).
func (m *Model) KillInst(ws *Workspace, selected *session.Instance, beforeKill func()) (pre func(), job Job) {
	previousStatus := selected.GetStatus()
	title := selected.Title
	// The owning workspace's storage, captured here on the model's
	// goroutine: killAction runs for seconds in a job, and reading the
	// TUI's focused workspace there would race a focus change and, after a
	// workspace switch, reach another workspace's storage.
	storage := ws.storage

	preAction := func() {
		if err := selected.TransitionTo(session.Deleting); err != nil {
			log.For("core").Warn("kill.preaction_transition_failed", "err", err)
		}
	}

	killAction := func() any {
		worktree, err := selected.GetGitWorktree()
		if err != nil {
			return OpFailed{Instance: selected, Title: title, Op: "delete", Previous: previousStatus, Err: err}
		}
		if worktree == nil {
			// A started workspace terminal has none (it runs in the
			// repository itself). A job must never panic: it would take
			// the TUI down from its Cmd goroutine.
			return OpFailed{Instance: selected, Title: title, Op: "delete", Previous: previousStatus, Err: fmt.Errorf("instance %s has no worktree", title)}
		}

		checkedOut, err := worktree.IsBranchCheckedOut()
		if err != nil {
			return OpFailed{Instance: selected, Title: title, Op: "delete", Previous: previousStatus, Err: err}
		}

		if checkedOut {
			return OpFailed{
				Instance: selected,
				Title:    title,
				Op:       "delete",
				Previous: previousStatus,
				Err:      fmt.Errorf("instance %s is currently checked out", selected.Title),
			}
		}

		if beforeKill != nil {
			beforeKill()
		}

		// A notice is what the kill could not finish but the user must see
		// (a stash entry it could not drop); it reaches them whatever else
		// happened. A notice alone means the kill itself succeeded.
		var notice error
		if err := selected.Kill(); err != nil {
			if n, ok := session.NoticeIn(err); ok {
				notice = n
			}
			if _, only := session.OnlyNotice(err); !only {
				log.For("core").Error("kill.instance_kill_failed", "title", title, "err", err)
				// A discarded orphan whose cleanup failed must NOT vanish
				// from the list: the worktree is still on disk and would
				// silently reappear on the next workspace load. Keep the
				// row, revert to Recoverable, and show the error (notices
				// included) so the user can retry D.
				if previousStatus == session.Recoverable {
					return OpFailed{
						Instance: selected,
						Title:    title,
						Op:       "discard",
						Previous: previousStatus,
						Err:      fmt.Errorf("discard %s: %w", title, err),
					}
				}
			}
		}

		// Past this point tmux + worktree + branch are gone (or, for a
		// non-Recoverable kill, best-effort gone with the failure logged).
		// Reverting status on a storage error would leave a zombie in the
		// list (Ready/Running with no backing resources); return
		// KillResult regardless so the TUI matches reality.
		// ErrInstanceNotFound just means storage already agreed, so it's a
		// debug-level note rather than an error.
		if err := storage.DeleteInstance(selected.Title); err != nil {
			if errors.Is(err, session.ErrInstanceNotFound) {
				log.For("core").Debug("kill.storage_already_absent", "title", title)
			} else {
				log.For("core").Error("kill.storage_delete_failed", "title", title, "err", err)
			}
		}

		return KillResult{Instance: selected, Title: title, Notice: notice}
	}

	return preAction, killAction
}

// saveSnapshot returns a save of ws's instances for Pause and Resume, to
// run in their job. It snapshots the membership now, on the model's
// goroutine, because the instance list must not be read from the job;
// status filtering still happens at save time (Persistable), so the state
// changes Pause and Resume make themselves are captured. Formerly
// app.snapshotSaveFunc.
func (m *Model) saveSnapshot(ws *Workspace) func() error {
	storage := ws.storage
	snapshot := append([]*session.Instance(nil), ws.insts...)
	return func() error {
		return storage.SaveInstances(Persistable(snapshot))
	}
}

// PauseInst returns the job pausing selected (Instance.Pause: stash, kill the
// session, remove the worktree, save ws's instances through
// saveSnapshot), reporting a PauseResult, or an OpFailed reverting to the
// status selected has now; the caller moves it to Loading. ws is the
// workspace the TUI shows. beforePause, when set, runs first in the job:
// the TUI closes its terminal pane's shell for the instance there, as for
// KillInst's beforeKill. Formerly app.pauseActionFor.
func (m *Model) PauseInst(ws *Workspace, selected *session.Instance, beforePause func()) Job {
	previousStatus := selected.GetStatus()
	pauseTitle := selected.Title
	saveFunc := m.saveSnapshot(ws)
	return func() any {
		if beforePause != nil {
			beforePause()
		}
		if err := selected.Pause(saveFunc); err != nil {
			return OpFailed{Instance: selected, Title: pauseTitle, Op: "pause", Previous: previousStatus, Err: err}
		}
		return PauseResult{Title: pauseTitle}
	}
}

// ResumeInst moves the Paused inst to Loading, so the list shows the spinner
// while Resume's worktree and tmux setup runs, and returns the job
// resuming it (a ResumeResult, or OpFailed). The caller must have checked
// that inst is Paused and not a workspace terminal (the TUI's
// selectedResumableNotWorkspace; the Resume request's precondition):
// TransitionTo only validates the move against the state machine, and
// every status may move to Loading, so it guards nothing here. A failed
// resume reverts to Paused (resumeOutcome), which is right only for an
// instance that was Paused. If the transition is refused all the same,
// ResumeInst returns nil and nothing runs. ws is the workspace the TUI
// shows: the save goes to its storage, and it stamps the result when no
// loaded workspace holds inst. Formerly the core of app.runResumeSelected.
func (m *Model) ResumeInst(ws *Workspace, inst *session.Instance) Job {
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("resume.skipped", "err", err)
		return nil
	}
	return m.ResumeIfLoadingInst(ws, inst)
}

// ResumeIfLoadingInst returns the job of a resume whose Loading transition the
// caller makes itself (the restart-with-options flow's confirmation). The
// save and the owner are taken now; the job resumes only if inst is
// Loading when it runs (the caller's transition may have failed, e.g. a
// concurrent reconcile flip landed the instance somewhere that transition
// can't legally proceed from; the caller's confirmation step can't
// otherwise tell the job to skip), and reports a resumeSkipped otherwise.
// Formerly app.runRestartWithOptionsSelected's Async body.
func (m *Model) ResumeIfLoadingInst(ws *Workspace, inst *session.Instance) Job {
	saveFunc := m.saveSnapshot(ws)
	title := inst.Title
	owner := m.startOwner(inst, ws)
	return func() any {
		if st := inst.GetStatus(); st != session.Loading {
			return resumeSkipped{err: fmt.Errorf("resume skipped: %s is no longer loading (%s)", title, st)}
		}
		return resumeOutcome(inst, title, owner, inst.Resume(saveFunc))
	}
}

// resumeOutcome turns Resume's error into its result. A Notice alone
// means the resume succeeded with something to report, which
// deliverResume shows; anything else failed. Formerly app.resumeResult.
func resumeOutcome(inst *session.Instance, title string, owner *Workspace, err error) any {
	if n, ok := session.OnlyNotice(err); ok {
		return ResumeResult{Instance: inst, Owner: owner, Notice: n}
	}
	if err != nil {
		return OpFailed{Instance: inst, Title: title, Op: "resume", Previous: session.Paused, Err: err}
	}
	return ResumeResult{Instance: inst, Owner: owner}
}

// RecoverInst moves the Recoverable placeholder inst to Loading, for the
// spinner, and returns the job adopting its orphan: it serializes the
// placeholder, flips the record to Running and runs
// session.ReconcileAndRestore (adopting the worktree, spawning tmux),
// reporting a RecoverResult owned by ws, the workspace showing inst. The
// caller must have checked that inst is Recoverable (the TUI's
// runResumeOrRecover; the Recover request's precondition): TransitionTo
// guards nothing here, since every status may move to Loading. nil when
// the transition is refused all the same. Formerly the core of
// app.runRecoverSelected.
func (m *Model) RecoverInst(ws *Workspace, inst *session.Instance) Job {
	cfgDir := ""
	if ws.ctx != nil {
		cfgDir = ws.ctx.ConfigDir
	}
	data := inst.ToInstanceData()
	data.Status = session.Running
	oldTitle := inst.Title
	cmdExec := cmd2.MakeExecutor()

	// Show the spinner while ReconcileAndRestore does its blocking
	// tmux/worktree probing — same shape as Resume. deliverRecover
	// reverts to Recoverable on failure.
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("recover.skipped", "err", err)
		return nil
	}
	return func() any {
		recovered, err := session.ReconcileAndRestore(data, cfgDir, cmdExec)
		return RecoverResult{Placeholder: inst, Owner: ws, OldTitle: oldTitle, Recovered: recovered, Err: err}
	}
}

// MergeInst returns the job performing the actual git merge of source's
// branch into target once the user commits a selection in the merge
// picker. Mirrors push: its MergeResult carries no error on success
// (silent, matching push's convention of treating "no error" as
// sufficient feedback) or the wrapped git error, which Deliver surfaces
// as a notice. Formerly app.mergeActionFor.
func (m *Model) MergeInst(target, source *session.Instance) Job {
	return func() any {
		worktree, err := target.GetGitWorktree()
		if err != nil {
			return MergeResult{Err: fmt.Errorf("merge: %w", err)}
		}
		if worktree == nil {
			// A started workspace terminal has none; a job must never
			// panic.
			return MergeResult{Err: fmt.Errorf("merge: %s has no worktree", target.Title)}
		}
		if err := worktree.Merge(source.GetBranch()); err != nil {
			return MergeResult{Err: err}
		}
		return MergeResult{}
	}
}

// DropUnstarted removes a creation flow's never-started instance from the
// workspace holding it, by identity, and returns the job killing it
// (worktree, tmux) off the model's goroutine; nil when inst is nil or has
// started (a started instance is no longer pending; never kill a live
// session from a cancel). Formerly the core of app.dropPendingNew.
func (m *Model) DropUnstarted(inst *session.Instance) Job {
	if inst == nil || inst.Started() {
		return nil
	}
	if ws := m.Holding(inst); ws != nil {
		ws.Remove(inst)
	}
	return killUnstarted(inst)
}

// killUnstarted returns the job running the blocking Kill of an instance
// already removed from every list (an aborted creation flow, a failed
// start); a failure is only logged. Formerly app.backgroundKillCmd.
func killUnstarted(inst *session.Instance) Job {
	if inst == nil {
		return nil
	}
	return func() any {
		if err := inst.Kill(); err != nil {
			log.For("core").Error("background_instance_kill_failed", "err", err)
		}
		return nil
	}
}

// SendPromptInst returns the job typing prompt into inst's agent pane and
// pressing Enter (Pane().SendPrompt: load-buffer, paste-buffer, a 100ms
// pause, Enter: three tmux subprocesses that must not block the TUI). A
// failure comes back as a notice naming the session: it arrives after the
// overlay or bar that took the text has closed, so the user must resend.
// For text the user sends to a running session: the prompt overlay, the
// quick input bar, a workbench review.
func (m *Model) SendPromptInst(inst *session.Instance, prompt string) Job {
	return func() any {
		if err := inst.Pane().SendPrompt(prompt); err != nil {
			return promptFailed{err: fmt.Errorf("prompt not sent to %s: %w", inst.Title, err)}
		}
		return nil
	}
}

// sendInitialPrompt is the start completion's send of an N flow's
// prompt. A failure is logged, as it was when the completion sent it
// on the TUI's goroutine. Either way it reports promptSent, whose
// delivery tells the TUI the start finished (Started): only after the
// prompt, as before, so no key the user types into the attached pane
// can land ahead of it.
func sendInitialPrompt(inst *session.Instance, owner *Workspace, prompt string) Job {
	return func() any {
		if err := inst.Pane().SendPrompt(prompt); err != nil {
			log.For("core").Error("send_prompt_failed", "err", err)
		}
		return promptSent{inst: inst, owner: owner}
	}
}
