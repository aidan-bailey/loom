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

// ErrNoSession refuses a request naming an ID no loaded workspace holds:
// the session was removed, or its workspace closed, after the client last
// saw it.
var ErrNoSession = errors.New("no such session")

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
	m.Deliver(t.result)
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
	}
	return nil, nil
}

// refuse answers a request the model won't start: with a Reply when one was
// asked for, else only in the log (as the TUI's own paths have always
// logged a refused transition).
func (m *Model) refuse(req ReqID, id InstanceID, err error) {
	if req == 0 {
		log.For("core").Debug("request.refused", "id", uint64(id), "err", err)
		return
	}
	m.emit(Reply{Req: req, ID: id, Err: err})
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
// (KillInst), answering with a KillResult or OpFailed.
func (m *Model) Kill(id InstanceID, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	pre, job := m.KillInst(ws, inst, nil)
	pre()
	m.spawn(m.track(req, id, job))
}

// Pause pauses the session id: it moves to Loading at once (a refused
// transition is logged and the pause runs anyway, as the TUI's path always
// did), then the job stashes, kills the session and removes the worktree
// (PauseInst). PauseInst is called before the transition, as the TUI's
// path does, so a failed pause reverts to the status the session had, not
// to Loading.
func (m *Model) Pause(id InstanceID, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	job := m.PauseInst(ws, inst, nil)
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("pause.preaction_transition_failed", "err", err)
	}
	m.spawn(m.track(req, id, job))
}

// Resume resumes the Paused session id (see ResumeInst). A refused
// transition refuses the request.
func (m *Model) Resume(id InstanceID, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("resume.skipped", "err", err)
		m.refuse(req, id, fmt.Errorf("resume %s: %w", inst.Title, err))
		return
	}
	m.spawn(m.track(req, id, m.ResumeIfLoadingInst(ws, inst)))
}

// ResumeWith resumes the session id with new launch options (the R flow):
// the program recomposed from base (applyLaunch), then as Resume.
func (m *Model) ResumeWith(id InstanceID, opts launch.Options, base string, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	m.applyLaunch(inst, opts, base)
	if err := inst.TransitionTo(session.Loading); err != nil {
		log.For("core").Warn("resume.skipped", "err", err)
		m.refuse(req, id, fmt.Errorf("resume %s: %w", inst.Title, err))
		return
	}
	m.spawn(m.track(req, id, m.ResumeIfLoadingInst(ws, inst)))
}

// Recover adopts the Recoverable orphan id (RecoverInst). Its Reply names
// the adopted instance.
func (m *Model) Recover(id InstanceID, req ReqID) {
	inst, ws := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	job := m.RecoverInst(ws, inst)
	if job == nil {
		m.refuse(req, id, fmt.Errorf("recover %s: it is not recoverable", inst.Title))
		return
	}
	m.spawn(m.track(req, id, job))
}

// Merge merges source's branch into target's worktree (MergeInst).
func (m *Model) Merge(target, source InstanceID, req ReqID) {
	t, _ := m.lookup(target)
	s, _ := m.lookup(source)
	if t == nil || s == nil {
		m.refuse(req, target, ErrNoSession)
		return
	}
	m.spawn(m.track(req, target, m.MergeInst(t, s)))
}

// Push commits and pushes the session id's worktree (PushInst).
func (m *Model) Push(id InstanceID, req ReqID) {
	inst, _ := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	m.spawn(m.track(req, id, m.PushInst(inst)))
}

// SendPrompt types text into the session id's pane and presses Enter
// (SendPromptInst). A failure is a notice, and the Reply's Err.
func (m *Model) SendPrompt(id InstanceID, text string, req ReqID) {
	inst, _ := m.lookup(id)
	if inst == nil {
		m.refuse(req, id, ErrNoSession)
		return
	}
	m.spawn(m.track(req, id, m.SendPromptInst(inst, text)))
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

// Create builds an instance in ws from spec, adds it, and (with Start)
// configures and starts it: the owner is stamped now, and the start's
// completion is a StartResult as before. The Reply comes at once and names
// the new instance. A ws no longer loaded is refused: an instance added to
// it would be shown nowhere, and a start would run for it anyway.
func (m *Model) Create(ws *Workspace, spec NewInstance, req ReqID) {
	if !m.IsLoaded(ws) {
		m.refuse(req, 0, fmt.Errorf("create %s: its workspace is no longer open", spec.Title))
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
	ws.Add(inst)
	if spec.Issue != 0 {
		m.applyGitHubState()
	}
	id := m.idOf(inst)
	m.reply(req, Reply{ID: id})
	if spec.Start {
		m.spawn(m.StartInst(inst, ws))
	}
}

// applyLaunch records chosen launch options on inst: the program composed
// from base with the chosen account's remote-control auth, the env toggles,
// and the account. Formerly app.applyChosenLaunch.
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
