package core

import (
	"context"
	"errors"
	"fmt"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
)

// ReqID names a request so its Reply can find the requester. The client
// chooses it; 0 asks for no Reply.
type ReqID uint64

// ErrRefused marks every refusal: a request the model would not start (its
// session gone, its precondition failed, its transition refused). A
// Reply's Err matches it (errors.Is) exactly when the request was refused,
// which the model reports nowhere but in that Reply; a failure of the
// request's job does not match it, and the model has already shown that
// one in a Notice.
var ErrRefused = errors.New("request refused")

// refusedError is a refusal's error: its own message, matching ErrRefused
// and whatever it wraps.
type refusedError struct{ error }

func (e refusedError) Is(target error) bool { return target == ErrRefused }
func (e refusedError) Unwrap() error        { return e.error }

// ErrNoSession refuses a request naming an ID no loaded workspace holds:
// the session was removed, or its workspace closed, after the client last
// saw it. It matches ErrRefused.
var ErrNoSession error = refusedError{errors.New("no such session")}

// tracked carries a request's job result back to Deliver together with the
// request it answers.
type tracked struct {
	req    ReqID
	id     InstanceID
	result any
}

// issueResult is FetchIssue's job result.
type issueResult struct {
	issue github.Issue
	err   error
}

// track wraps job so its result reaches Deliver as a tracked result; nil
// for a nil job.
func (m *Model) track(req ReqID, id InstanceID, job Job) Job {
	if job == nil {
		return nil
	}
	return func() any { return tracked{req: req, id: id, result: job()} }
}

// deliverTracked handles a request's result as if it had arrived on its
// own, then answers the request (when it asked for a Reply).
func (m *Model) deliverTracked(t tracked) {
	m.causedBy(t.req, func() { m.Deliver(t.result) })
	if t.req == 0 {
		return
	}
	r := Reply{Req: t.req, ID: t.id}
	r.Err, r.Notice = outcome(t.result)
	switch res := t.result.(type) {
	case RecoverResult:
		if res.Err == nil && res.Recovered != nil {
			r.ID = m.idOf(res.Recovered)
		}
	case issueResult:
		r.Issue = res.issue
	}
	m.emit(r)
}

// outcome reads an operation's failure and notice out of its result.
func outcome(result any) (err, notice error) {
	switch r := result.(type) {
	case OpFailed:
		return r.Err, nil
	case KillResult:
		return nil, r.Notice
	case ResumeResult:
		return nil, r.Notice
	case RecoverResult:
		return r.Err, nil
	case MergeResult:
		return r.Err, nil
	case pushResult:
		return r.err, nil
	case promptFailed:
		return r.err, nil
	case issueResult:
		return r.err, nil
	case resumeSkipped:
		return r.err, nil
	}
	return nil, nil
}

// refuse answers a request the model won't start: with a Reply when one was
// asked for, else only in the log (as the TUI's own paths have always
// logged a refused transition). The Reply's Err matches ErrRefused.
func (m *Model) refuse(req ReqID, id InstanceID, err error) {
	if !errors.Is(err, ErrRefused) {
		err = refusedError{err}
	}
	if req == 0 {
		log.For("core").Info("request.refused", "id", uint64(id), "err", err)
		return
	}
	m.emit(Reply{Req: req, ID: id, Err: err})
}

// requestOp names an ID request for its precondition and its refusals
// ("kill x: …").
type requestOp string

const (
	opKill      requestOp = "kill"
	opPause     requestOp = "pause"
	opPush      requestOp = "push"
	opResume    requestOp = "resume"
	opRecover   requestOp = "recover"
	opMergeInto requestOp = "merge into"
	opMergeFrom requestOp = "merge from"
	opSend      requestOp = "send a prompt to"
)

// precondition checks inst against the gate the TUI puts on op today, and
// returns the refusal naming op, the session and why, or nil. Requests
// must check it before they change anything: TransitionTo is no guard,
// since every status may move to Loading, and some jobs assume what the
// gates ensure (a kill, push or merge needs a worktree, which a workspace
// terminal has none of). So the model refuses exactly what no key can do
// now. The gates are app/intents.go's:
//
//   - kill, pause, push and the merge target: selectedNotBusyNotWorkspace
//     (not a workspace terminal, not Loading or Deleting). That admits a
//     Paused session, and a Recoverable one, which a kill discards. A
//     merge target must also have started: runMergeSelected refuses one
//     whose worktree it can't get (GetGitWorktree), which an unstarted
//     one has none of.
//   - the merge source: mergeSourceRows (the same rule, per instance).
//   - resume: selectedResumableNotWorkspace for a Paused session (the TUI
//     routes a Recoverable one to recover), and selectedPausedNotWorkspace
//     for ResumeWith: Paused, not a workspace terminal.
//   - recover: selectedResumableNotWorkspace for a Recoverable one.
//   - send: the prompt overlay's, quick input's and review's send-time
//     check, not Paused and its tmux session alive, here as started and
//     not Paused. Liveness is a tmux subprocess, which the model's
//     goroutine must not run: a send to a dead session fails in its job
//     and replies with that error.
func precondition(op requestOp, inst *session.Instance) error {
	st := inst.GetStatus()
	why := ""
	switch op {
	case opKill, opPause, opPush, opMergeInto, opMergeFrom:
		switch {
		case inst.IsWorkspaceTerminal:
			why = "not allowed on a workspace terminal"
		case st == session.Loading || st == session.Deleting:
			why = fmt.Sprintf("the session is busy (%s)", st)
		case op == opMergeInto && !inst.Started():
			why = "the session has not started"
		}
	case opResume:
		switch {
		case inst.IsWorkspaceTerminal:
			why = "not allowed on a workspace terminal"
		case st != session.Paused:
			why = fmt.Sprintf("the session is not paused (%s)", st)
		}
	case opRecover:
		switch {
		case inst.IsWorkspaceTerminal:
			why = "not allowed on a workspace terminal"
		case st != session.Recoverable:
			why = fmt.Sprintf("the session is not recoverable (%s)", st)
		}
	case opSend:
		switch {
		case !inst.Started():
			why = "the session has not started"
		case st == session.Paused:
			why = "the session is paused"
		}
	default:
		why = "unknown request"
	}
	if why == "" {
		return nil
	}
	return fmt.Errorf("%s %s: %s", op, inst.Title, why)
}

// admit resolves id for op: the instance and the loaded workspace holding
// it, once op's precondition holds. Otherwise it refuses the request
// (ErrNoSession for an id no loaded workspace holds) and ok is false.
func (m *Model) admit(op requestOp, id InstanceID, req ReqID) (inst *session.Instance, ws *Workspace, ok bool) {
	inst, ws = m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return nil, nil, false
	}
	if err := precondition(op, inst); err != nil {
		m.refuse(req, id, err)
		return nil, nil, false
	}
	return inst, ws, true
}

// reply answers a request that finished at once.
func (m *Model) reply(req ReqID, r Reply) {
	if req == 0 {
		return
	}
	r.Req = req
	m.emit(r)
}

// Kill kills the session id: its pre-step moves it to Deleting at once (the
// spinner shows), and its job checks, kills and deletes the record
// (killInst), answering with a KillResult or OpFailed. A Recoverable
// session is discarded. Refused for a workspace terminal and a busy
// session (precondition), so a second Kill of the same session is refused
// while the first runs.
func (m *Model) Kill(id InstanceID, req ReqID) {
	inst, ws, ok := m.admit(opKill, id, req)
	if !ok {
		return
	}
	pre, job := m.killInst(ws, inst)
	pre()
	m.spawn(m.track(req, id, job))
}

// Pause pauses the session id: it moves to Loading at once (a refused
// transition is logged and the pause runs anyway, as the TUI's path always
// did), then the job stashes, kills the session and removes the worktree
// (pauseInst). pauseInst is called before the transition, as the TUI's
// path does, so a failed pause reverts to the status the session had, not
// to Loading. Refused for a workspace terminal and a busy session
// (precondition).
func (m *Model) Pause(id InstanceID, req ReqID) {
	inst, ws, ok := m.admit(opPause, id, req)
	if !ok {
		return
	}
	job := m.pauseInst(ws, inst)
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("pause.preaction_transition_failed", "err", err)
	}
	m.spawn(m.track(req, id, job))
}

// Resume resumes the session id, which must be Paused and not a workspace
// terminal (precondition; a Recoverable one is Recover's): it moves to
// Loading at once and the job resumes it (resumeIfLoadingInst), answering
// with a ResumeResult, or an OpFailed reverting to Paused. The transition
// can't be refused from Paused; if it were, the request would be refused
// too.
func (m *Model) Resume(id InstanceID, req ReqID) {
	inst, ws, ok := m.admit(opResume, id, req)
	if !ok {
		return
	}
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("resume.skipped", "err", err)
		m.refuse(req, id, fmt.Errorf("resume %s: %w", inst.Title, err))
		return
	}
	m.spawn(m.track(req, id, m.resumeIfLoadingInst(ws, inst)))
}

// ResumeWith resumes the session id with new launch options (the R flow):
// the program recomposed from base (applyLaunch), then as Resume. Its
// precondition is Resume's, checked before the options are applied, so a
// refused request changes nothing.
func (m *Model) ResumeWith(id InstanceID, opts launch.Options, base string, req ReqID) {
	inst, ws, ok := m.admit(opResume, id, req)
	if !ok {
		return
	}
	m.applyLaunch(inst, opts, base)
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("resume.skipped", "err", err)
		m.refuse(req, id, fmt.Errorf("resume %s: %w", inst.Title, err))
		return
	}
	m.spawn(m.track(req, id, m.resumeIfLoadingInst(ws, inst)))
}

// Recover adopts the orphan id, which must be Recoverable (precondition):
// recoverInst moves it to Loading and its job adopts the worktree,
// answering with a RecoverResult. Its Reply names the adopted instance,
// which replaces id's row.
func (m *Model) Recover(id InstanceID, req ReqID) {
	inst, ws, ok := m.admit(opRecover, id, req)
	if !ok {
		return
	}
	job := m.recoverInst(ws, inst)
	if job == nil {
		m.refuse(req, id, fmt.Errorf("recover %s: its transition was refused", inst.Title))
		return
	}
	m.spawn(m.track(req, id, job))
}

// Merge merges source's branch into target's worktree (mergeInst). Each
// must pass its precondition, and they must differ, as the merge picker
// lists neither a busy session nor the target among the sources. The
// Reply names target.
func (m *Model) Merge(target, source InstanceID, req ReqID) {
	t, _, ok := m.admit(opMergeInto, target, req)
	if !ok {
		return
	}
	s, _ := m.lookup(source)
	if s == nil {
		m.refuse(req, target, ErrNoSession)
		return
	}
	if s == t {
		m.refuse(req, target, fmt.Errorf("merge %s: a session can't be merged into itself", t.Title))
		return
	}
	if err := precondition(opMergeFrom, s); err != nil {
		m.refuse(req, target, err)
		return
	}
	m.spawn(m.track(req, target, m.mergeInst(t, s)))
}

// Push commits and pushes the session id's worktree (pushInst). Refused
// for a workspace terminal and a busy session (precondition).
func (m *Model) Push(id InstanceID, req ReqID) {
	inst, _, ok := m.admit(opPush, id, req)
	if !ok {
		return
	}
	m.spawn(m.track(req, id, m.pushInst(inst)))
}

// SendPrompt types text into the session id's pane and presses Enter
// (sendPromptInst). A failure is a notice, and the Reply's Err; a success
// replies with none. Refused for a session not started or Paused
// (precondition).
func (m *Model) SendPrompt(id InstanceID, text string, req ReqID) {
	inst, _, ok := m.admit(opSend, id, req)
	if !ok {
		return
	}
	m.spawn(m.track(req, id, m.sendPromptInst(inst, text)))
}

// NewInstance describes an instance to create (Create).
type NewInstance struct {
	Title   string
	Path    string // the repository it works in
	Program string // the base program; Launch is composed onto it
	Prompt  string // sent once it has started
	Branch  string // the branch picker's choice; "" for a new branch
	Issue   int    // the GitHub issue it was made from, 0 for none
	// Launch is the Session Launch Options choice. Its account and branch
	// prefix are recorded on the instance; the rest is composed onto
	// Program with the account's remote-control auth (applyLaunch). Only
	// with Start.
	Launch launch.Options
	// Start starts it at once (the creation flows). Without it the
	// instance is added unstarted (a script's ctx:new_instance).
	Start bool
}

// createWS builds an instance in ws from spec, adds it, and (with Start)
// configures and starts it: the owner is stamped now, and the start's
// completion is a StartResult as before. The Reply comes at once and names
// the new instance. A ws the model does not serve (an unknown ID) is
// refused.
func (m *Model) createWS(ws *Workspace, spec NewInstance, req ReqID) {
	if !m.isLoadedWS(ws) {
		m.refuse(req, 0, fmt.Errorf("create %s: its workspace is not served", spec.Title))
		return
	}
	cfgDir := ""
	if ws.ctx != nil {
		cfgDir = ws.ctx.ConfigDir
	}
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:     spec.Title,
		Path:      spec.Path,
		Program:   spec.Program,
		Prompt:    spec.Prompt,
		Branch:    spec.Branch,
		ConfigDir: cfgDir,
	})
	if err != nil {
		m.refuse(req, 0, err)
		return
	}
	if spec.Issue != 0 {
		inst.SetIssue(spec.Issue)
	}
	if spec.Start {
		m.applyLaunch(inst, spec.Launch, spec.Program)
		// Always recorded, edited or not, so branch composition has a single
		// source of truth instead of a re-read of config.json in git.
		inst.SetBranchPrefix(spec.Launch.BranchPrefix)
		if err := inst.TransitionTo(session.Loading); err != nil {
			log.For("core").Warn("create.transition_failed", "title", spec.Title, "err", err)
		}
	}
	ws.add(inst)
	if spec.Issue != 0 {
		m.applyGitHubState()
	}
	id := m.idOf(inst)
	m.reply(req, Reply{ID: id})
	if spec.Start {
		m.causedBy(req, func() { m.spawn(m.startInst(inst, ws)) })
	}
}

// applyLaunch records chosen launch options on inst: the program composed
// from base with the chosen account's remote-control auth, the env toggles,
// and the account. Moved from app's applyChosenLaunch (deleted in stage 1C
// package C).
func (m *Model) applyLaunch(inst *session.Instance, opts launch.Options, base string) {
	inst.SetLaunchOptions(launch.Compose(opts, m.RCAuthFor(opts.Account), base, inst.Title), opts.HeadroomProxy, opts.CacheTTL1h)
	inst.SetAccount(opts.Account)
}

// FetchIssue reads issue n of repo through gh (github.View) in a job and
// answers with the Reply's Issue (or Err).
func (m *Model) FetchIssue(repo string, n int, req ReqID) {
	m.spawn(m.track(req, 0, fetchIssueJob(repo, n, internalexec.Default{})))
}

// fetchIssueJob is FetchIssue's job, with its executor injectable for tests.
func fetchIssueJob(repo string, n int, r internalexec.Executor) Job {
	return func() any {
		is, err := github.View(context.Background(), repo, n, r)
		return issueResult{issue: is, err: err}
	}
}
