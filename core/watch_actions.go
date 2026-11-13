package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/watch"
)

// watchWriteBudget bounds one watcher job's gh and git calls from outside;
// each call also holds its own timeout (spec §7.1).
const watchWriteBudget = 3 * time.Minute

// watchActionResult is one watcher action's job result, delivered on the loop
// by deliverWatchAction. ws and st are identity only: a result for a state
// that is no longer the live one (the watcher was switched off, the workspace
// unloaded) records nothing. key is the in-flight key to release. err is a
// failed step, already worded for the issue's status; refused is a refusal's
// reason (a hook that declined, a title too wide), for the status too. posted
// is an escalation's comment job having posted its comment: its result leads
// to the comment's record and the label's job, not to the escalation's end.
type watchActionResult struct {
	ws      *Workspace
	st      *watchState
	key     string
	act     watch.Action
	err     error
	posted  bool
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
		case watch.ActClosePR:
			m.watchClosePR(ws, st, a)
		case watch.ActEscalate:
			m.watchEscalate(ws, st, a)
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
	var err error
	switch r.act.Kind {
	case watch.ActEscalate:
		err = m.finishWatchEscalate(r)
	}
	if err != nil {
		log.For("core").Warn("watch.action_failed", "kind", string(r.act.Kind), "issue", r.act.Issue, "err", err)
		setWatchActErr(r.st, r.act, err.Error())
		return
	}
	clearWatchActErr(r.st, r.act)
}

// watchActGH is the executor the watcher's gh calls run with: the test seam
// when set, the production one otherwise.
func (m *Model) watchActGH() internalexec.Executor {
	if m.ghExec != nil {
		return m.ghExec
	}
	return internalexec.Default{}
}

// watchPROf is the snapshot's PR number.
func watchPROf(snap *watch.Snapshot, number int) (github.WatchPR, bool) {
	for _, pr := range snap.PRs {
		if pr.Number == number {
			return pr, true
		}
	}
	return github.WatchPR{}, false
}

// watchOwnsPR reports whether number is the watcher's own open PR for issue:
// in the snapshot, open, and passing watch.OwnPR's four terms. The watcher
// closes no other PR.
func watchOwnsPR(st *watchState, issue, number int) bool {
	pr, ok := watchPROf(st.snap, number)
	return ok && pr.State == "OPEN" && watch.OwnPR(pr, issue, st.snap.Viewer, st.tmpl.BranchPattern(st.snap.Prefix, issue))
}

// watchClosePR closes the watcher's own PR a.PR with a.Reason as its comment
// (Decide wrote it), in a job.
func (m *Model) watchClosePR(ws *Workspace, st *watchState, a watch.Action) {
	if !watchOwnsPR(st, a.Issue, a.PR) {
		log.For("core").Warn("watch.close_refused", "issue", a.Issue, "pr", a.PR)
		setWatchActErr(st, a, fmt.Sprintf("PR #%d is not the watcher's own open PR for #%d: it was not closed", a.PR, a.Issue))
		return
	}
	key := watchActKey(a)
	st.inFlight[key] = true
	repoDir, r, comment := ws.ctx.RepoPath, m.watchActGH(), a.Reason
	if strings.TrimSpace(comment) == "" {
		comment = fmt.Sprintf("loom watcher: closing this PR, since issue #%d is settled.", a.Issue)
	}
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), watchWriteBudget)
		defer cancel()
		res := watchActionResult{ws: ws, st: st, key: key, act: a}
		if err := github.ClosePR(ctx, repoDir, a.PR, comment, r); err != nil {
			res.err = fmt.Errorf("close PR #%d: %w", a.PR, err)
		}
		return res
	})
}

// watchEscalationComment is the comment an escalation posts on the issue: the
// reason, the session and the PR when known, the nudges counted on the PR,
// and that the watcher is hands-off while the escalate label is on.
func watchEscalationComment(st *watchState, a watch.Action, title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "loom watcher: %s\n", a.Reason)
	if title != "" {
		fmt.Fprintf(&b, "\nSession: %s", title)
	}
	if a.PR != 0 {
		fmt.Fprintf(&b, "\nPR: #%d", a.PR)
		counts := st.mem.PRs[a.PR].Nudges
		var parts []string
		for _, kind := range slices.Sorted(maps.Keys(counts)) {
			parts = append(parts, fmt.Sprintf("%s %d", kind, counts[kind].N))
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "\nNudges sent on the PR: %s", strings.Join(parts, ", "))
		}
	}
	fmt.Fprintf(&b, "\n\nThe watcher takes no further action on this issue while the `%s` label is on it.", st.cfg.EscalateLabel)
	return b.String()
}

// watchEscalate starts issue a.Issue's escalation: the comment, in a job,
// unless memory already records that comment (a retry after a failed label:
// the recorded reason is this action's), and then the label (watchEscalateLabel).
// The comment's record is made on the loop once the comment is posted
// (finishWatchEscalate), before the label is tried; the alert and the
// escalation's record wait for the label, so a failed step records no
// escalation and the next tick decides again.
func (m *Model) watchEscalate(ws *Workspace, st *watchState, a watch.Action) {
	if c := st.mem.Issues[a.Issue].Commented; c != "" && c == a.Reason {
		m.watchEscalateLabel(ws, st, a)
		return
	}
	title := ""
	if inst := m.ownerInst(InstanceID(a.Session)); inst != nil {
		title = inst.Title
	}
	comment := watchEscalationComment(st, a, title)
	key := watchActKey(a)
	st.inFlight[key] = true
	repoDir, r := ws.ctx.RepoPath, m.watchActGH()
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), watchWriteBudget)
		defer cancel()
		res := watchActionResult{ws: ws, st: st, key: key, act: a}
		if err := github.CommentIssue(ctx, repoDir, a.Issue, comment, r); err != nil {
			res.err = fmt.Errorf("comment on #%d: %w", a.Issue, err)
			return res
		}
		res.posted = true
		return res
	})
}

// watchEscalateLabel adds the escalate label to issue a.Issue in a job (gh
// creates the label when it is absent), holding the action's key again: the
// result of the comment's job released it on the loop, and this runs in that
// same turn.
func (m *Model) watchEscalateLabel(ws *Workspace, st *watchState, a watch.Action) {
	key := watchActKey(a)
	st.inFlight[key] = true
	repoDir, r, label := ws.ctx.RepoPath, m.watchActGH(), st.cfg.EscalateLabel
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), watchWriteBudget)
		defer cancel()
		res := watchActionResult{ws: ws, st: st, key: key, act: a}
		if err := github.AddLabel(ctx, repoDir, a.Issue, label, r); err != nil {
			res.err = fmt.Errorf("label #%d %q: %w", a.Issue, label, err)
		}
		return res
	})
}

// finishWatchEscalate settles an escalation job that succeeded, on the loop.
// The comment's job (r.posted) leaves its record: memory notes the posted
// comment's reason and saves it before the label is tried, so a failed label
// is retried without commenting again; a record that can't be saved stops the
// escalation here, shown to the clients (the in-memory record still skips the
// comment on the next tick). The label's job leaves the escalation: the alert
// (a Notice to every client; the escalation also shows in the workspace's
// view, from memory), then the record, settling rule 5's PRs, and the save. A
// save that fails is shown to the clients too.
func (m *Model) finishWatchEscalate(r watchActionResult) error {
	a, st := r.act, r.st
	if r.posted {
		st.mem.RecordCommented(a.Issue, a.Reason)
		if err := watch.SaveMemory(st.memPath, st.mem); err != nil {
			err = fmt.Errorf("record the comment on #%d: %w", a.Issue, err)
			m.notifyErr(err)
			return err
		}
		m.watchEscalateLabel(r.ws, st, a)
		return nil
	}
	m.emit(Notice{Info: fmt.Sprintf("watcher (%s): issue #%d escalated to the owner: %s", r.ws.Label(), a.Issue, a.Reason)})
	st.mem.RecordEscalation(a.Issue, a.Reason, time.Now())
	if len(a.Settle) > 0 {
		st.mem.Settle(a.Settle)
	}
	if err := watch.SaveMemory(st.memPath, st.mem); err != nil {
		err = fmt.Errorf("record the escalation: %w", err)
		m.notifyErr(err)
		return err
	}
	return nil
}
