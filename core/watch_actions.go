package core

import (
	"fmt"
	"strings"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/watch"
)

// watchActionResult is one watcher action's job result, delivered on the loop
// by deliverWatchAction. ws and st are identity only: a result for a state
// that is no longer the live one (the watcher was switched off, the workspace
// unloaded) records nothing. key is the in-flight key to release. err is a
// failed step, already worded for the issue's status; refused is a refusal's
// reason (a hook that declined, a title too wide), for the status too.
type watchActionResult struct {
	ws      *Workspace
	st      *watchState
	key     string
	act     watch.Action
	err     error
	refused string
}

// watchActKey is the in-flight key of action a: its kind and the PR, session
// or issue it acts on, never the issue alone, since Decide emits one issue's
// close and kill (rule 2) or escalation, close and kill (rule 5) in one tick.
func watchActKey(a watch.Action) string {
	switch a.Kind {
	case watch.ActClosePR:
		return fmt.Sprintf("%s:%d", a.Kind, a.PR)
	case watch.ActSend:
		return fmt.Sprintf("%s:%d", a.Kind, a.Session)
	}
	return fmt.Sprintf("%s:%d", a.Kind, a.Issue)
}

// watchBusy reports whether action a's job is already out. A start is also
// busy while any start is out: the cap counts sessions, and the hook's start
// must see the previous start's session, which exists only once its job has
// been delivered.
func watchBusy(st *watchState, a watch.Action) bool {
	if st.inFlight[watchActKey(a)] {
		return true
	}
	if a.Kind != watch.ActStart {
		return false
	}
	prefix := string(watch.ActStart) + ":"
	for k := range st.inFlight {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// setWatchActErr records why action a did not happen, for the issue's status
// (watchState.actErrs), worded "<kind>: <msg>".
func setWatchActErr(st *watchState, a watch.Action, msg string) {
	if st.actErrs == nil {
		st.actErrs = map[int]string{}
	}
	st.actErrs[a.Issue] = string(a.Kind) + ": " + msg
}

// clearWatchActErr forgets the error of a's kind on its issue once an action
// of that kind succeeded; another kind's error stays.
func clearWatchActErr(st *watchState, a watch.Action) {
	if strings.HasPrefix(st.actErrs[a.Issue], string(a.Kind)+": ") {
		delete(st.actErrs, a.Issue)
	}
}

// dispatchWatchActions carries out the actions Decide returned for ws's
// decided tick, on the loop: kills through Model.Kill, closes, escalations,
// nudges and starts as jobs whose results come back as watchActionResult.
// It acts only on a decided tick (st.snap), with the memory loaded (an action
// that counts writes it), for a workspace the model still serves with the
// watcher on and a repository. An action whose job is already out is skipped
// (watchBusy), and nothing is retried: the next tick decides again.
func (m *Model) dispatchWatchActions(ws *Workspace, st *watchState, actions []watch.Action) {
	if st == nil || st.snap == nil || !st.memLoaded || ws == nil || ws.ctx == nil || ws.ctx.RepoPath == "" || m.watchOf(ws) != st {
		return
	}
	if st.inFlight == nil {
		st.inFlight = map[string]bool{}
	}
	for _, a := range actions {
		if a.Kind == watch.ActWait {
			continue
		}
		if watchBusy(st, a) {
			log.For("core").Debug("watch.action_in_flight", "kind", string(a.Kind), "issue", a.Issue)
			continue
		}
		switch a.Kind {
		case watch.ActKill:
			m.watchKill(ws, st, a)
		}
	}
}

// watchOwnsSession reports whether inst is the watcher's own session for
// issue: its title parses with the config's template to issue, its issue field
// is issue, and ws holds it. Anything else (a foreign session, a session in
// another workspace) is never killed or messaged.
func (m *Model) watchOwnsSession(ws *Workspace, st *watchState, inst *session.Instance, issue int) bool {
	n, _, ok := st.tmpl.Parse(inst.Title)
	return ok && n == issue && inst.IssueNumber() == issue && m.holding(inst) == ws
}

// watchKill kills the watcher's own session a.Session through Model.Kill, which
// refuses a busy one and checks HeldElsewhere; a refusal leaves the row and the
// next tick decides again.
func (m *Model) watchKill(ws *Workspace, st *watchState, a watch.Action) {
	id := InstanceID(a.Session)
	inst := m.ownerInst(id)
	if inst == nil || !m.watchOwnsSession(ws, st, inst, a.Issue) {
		log.For("core").Warn("watch.kill_refused", "issue", a.Issue, "session", a.Session)
		setWatchActErr(st, a, "the session is not the watcher's own for the issue: nothing was killed")
		return
	}
	m.Kill(id, 0)
}

// deliverWatchAction settles a watcher job's result on the loop: releases its
// key, and records a failure or a refusal in the issue's status. A result for
// a state that is no longer the live one records nothing.
func (m *Model) deliverWatchAction(r watchActionResult) {
	delete(r.st.inFlight, r.key)
	if m.watchOf(r.ws) != r.st {
		log.For("core").Info("watch.result_dropped", "kind", string(r.act.Kind), "issue", r.act.Issue)
		return
	}
	switch {
	case r.err != nil:
		log.For("core").Warn("watch.action_failed", "kind", string(r.act.Kind), "issue", r.act.Issue, "err", r.err)
		setWatchActErr(r.st, r.act, r.err.Error())
		return
	case r.refused != "":
		setWatchActErr(r.st, r.act, r.refused)
		return
	}
	clearWatchActErr(r.st, r.act)
}
