package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/watch"
)

// watchWriteBudget bounds one watcher job's gh and git calls from outside;
// each call also holds its own timeout (spec §7.1).
const watchWriteBudget = 3 * time.Minute

// watchTextCap is the most a nudge or a start prompt may hold, log tails
// included (spec §7.1).
const watchTextCap = 64 << 10

// watchLogJobs is how many failed jobs' log tails one ci nudge carries.
const watchLogJobs = 5

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
	text    string
	start   watchStartPlan
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
		case watch.ActSend:
			m.watchSend(ws, st, a)
		case watch.ActStart:
			m.watchStart(ws, st, a)
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
// refuses a busy one and checks HeldElsewhere. The precondition is checked
// first, since a refusal Kill makes with request ID 0 is only logged: a busy
// session is reported in the issue's status, leaves the row and the next tick
// decides again. A kill Kill accepted clears the kill's earlier error.
func (m *Model) watchKill(ws *Workspace, st *watchState, a watch.Action) {
	id := InstanceID(a.Session)
	inst := m.ownerInst(id)
	if inst == nil || !m.watchOwnsSession(ws, st, inst, a.Issue) {
		log.For("core").Warn("watch.kill_refused", "issue", a.Issue, "session", a.Session)
		setWatchActErr(st, a, "the session is not the watcher's own for the issue: nothing was killed")
		return
	}
	if err := precondition(opKill, inst); err != nil {
		log.For("core").Warn("watch.kill_refused", "issue", a.Issue, "session", a.Session, "err", err)
		setWatchActErr(st, a, "nothing was killed: "+err.Error())
		return
	}
	m.Kill(id, 0)
	clearWatchActErr(st, a)
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
	case watch.ActSend:
		err = m.finishWatchSend(r)
	case watch.ActStart:
		err = m.finishWatchStart(r)
	}
	if err != nil {
		log.For("core").Warn("watch.action_failed", "kind", string(r.act.Kind), "issue", r.act.Issue, "err", err)
		setWatchActErr(r.st, r.act, err.Error())
		return
	}
	if r.act.Kind != watch.ActSend { // sendWatchNudge cleared it before queueing
		clearWatchActErr(r.st, r.act)
	}
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

// watchLog is one failed job's log tail for a ci nudge, or why it could not be
// fetched.
type watchLog struct {
	name string
	tail string
	err  error
}

// cutTop is the last n bytes of s, starting on a rune boundary.
func cutTop(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// watchNudgeText is a nudge's message: head (Nudge.Text), each log's tail under
// its name, cut from the top to fit watchTextCap with the suffix, then suffix.
// A log that could not be fetched is named as such; once no room is left for a
// log it and the rest are left out.
func watchNudgeText(head string, logs []watchLog, suffix string) string {
	var b strings.Builder
	b.WriteString(head)
	for _, l := range logs {
		section := "\n\nLog of " + l.name + ":\n"
		if l.err != nil {
			note := section + "(the log could not be fetched: " + l.err.Error() + ")"
			if b.Len()+len(note)+len(suffix) <= watchTextCap {
				b.WriteString(note)
			}
			continue
		}
		room := watchTextCap - b.Len() - len(section) - len(suffix)
		if room <= 0 {
			break
		}
		b.WriteString(section)
		b.WriteString(cutTop(strings.TrimRight(l.tail, "\n"), room))
	}
	b.WriteString(suffix)
	return b.String()
}

// watchFailedJobs is the ci nudge's failed Actions jobs of PR a.PR: the
// checks with a job to read, at most watchLogJobs. Any other kind has none.
func watchFailedJobs(st *watchState, a watch.Action) []github.FailedCheck {
	if a.Nudge.Kind != "ci" {
		return nil
	}
	pr, _ := watchPROf(st.snap, a.PR)
	var out []github.FailedCheck
	for _, f := range pr.Failing {
		if f.JobID != 0 && len(out) < watchLogJobs {
			out = append(out, f)
		}
	}
	return out
}

// watchSend nudges the watcher's own Ready session a.Session about its own
// open PR a.PR. Every check comes first, so a refusal counts nothing; then the
// nudge is counted in memory (an issue-changed one also moves seen) and the
// file saved before anything is sent, so a failed send costs a count and is
// never left uncounted, and a failed save sends nothing. A ci nudge reads
// its failed jobs' log tails in a job and is sent when it returns; any other
// goes at once.
func (m *Model) watchSend(ws *Workspace, st *watchState, a watch.Action) {
	inst := m.ownerInst(InstanceID(a.Session))
	switch {
	case inst == nil || !m.watchOwnsSession(ws, st, inst, a.Issue):
		setWatchActErr(st, a, "the session is not the watcher's own for the issue: nothing was sent")
		return
	case inst.GetStatus() != session.Ready:
		setWatchActErr(st, a, fmt.Sprintf("the session is %s, not Ready: nothing was sent", inst.GetStatus()))
		return
	case !watchOwnsPR(st, a.Issue, a.PR):
		setWatchActErr(st, a, fmt.Sprintf("PR #%d is not the watcher's own open PR for #%d: nothing was sent", a.PR, a.Issue))
		return
	}
	undo := rememberNudge(st, a)
	n := st.mem.RecordNudge(a.PR, a.Nudge.Kind, a.Nudge.Key, time.Now())
	if a.Nudge.Kind == "issue-changed" {
		st.mem.SetSeen(a.Issue, a.Nudge.Key)
	}
	if err := watch.SaveMemory(st.memPath, st.mem); err != nil {
		undo() // nothing was sent: later ticks must not count a nudge that never went
		err = fmt.Errorf("record the nudge: %w: nothing was sent", err)
		setWatchActErr(st, a, err.Error())
		m.notifyErr(err)
		return
	}
	suffix := fmt.Sprintf("\n\n(nudge %d of %d; the next escalates to the owner)", n, st.cfg.NudgeLimit-1)
	failed := watchFailedJobs(st, a)
	if len(failed) == 0 {
		m.sendWatchNudge(st, a, inst, watchNudgeText(a.Nudge.Text, nil, suffix))
		return
	}
	key := watchActKey(a)
	st.inFlight[key] = true
	repoDir, r, lines, head := ws.ctx.RepoPath, m.watchActGH(), st.cfg.LogTail, a.Nudge.Text
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), watchWriteBudget)
		defer cancel()
		logs := make([]watchLog, 0, len(failed))
		for _, f := range failed {
			tail, err := github.FailedJobLogs(ctx, repoDir, f.JobID, lines, r)
			logs = append(logs, watchLog{name: f.Name, tail: tail, err: err})
		}
		return watchActionResult{ws: ws, st: st, key: key, act: a, text: watchNudgeText(head, logs, suffix)}
	})
}

// rememberNudge captures what watchSend is about to change in memory (the PR's
// record of the nudge's kind and, for an issue-changed nudge, the issue's seen)
// and returns what puts it back, for a save that failed.
func rememberNudge(st *watchState, a watch.Action) (undo func()) {
	prior, hadNudge := st.mem.PRs[a.PR].Nudges[a.Nudge.Kind]
	_, hadPR := st.mem.PRs[a.PR]
	is, hadIssue := st.mem.Issues[a.Issue]
	return func() {
		if hadNudge {
			st.mem.PRs[a.PR].Nudges[a.Nudge.Kind] = prior
		} else if p, ok := st.mem.PRs[a.PR]; ok {
			delete(p.Nudges, a.Nudge.Kind)
			if !hadPR {
				delete(st.mem.PRs, a.PR)
			}
		}
		if a.Nudge.Kind != "issue-changed" {
			return
		}
		if hadIssue {
			st.mem.Issues[a.Issue] = is
		} else {
			delete(st.mem.Issues, a.Issue)
		}
	}
}

// finishWatchSend sends a ci nudge whose log tails are in, if its session is
// still the watcher's own and can take a message.
func (m *Model) finishWatchSend(r watchActionResult) error {
	inst := m.ownerInst(InstanceID(r.act.Session))
	if inst == nil || !m.watchOwnsSession(r.ws, r.st, inst, r.act.Issue) {
		return errors.New("the session is gone or no longer the watcher's own: the nudge was not sent")
	}
	if why := jobOwnerWhy(inst); why != "" {
		return fmt.Errorf("the nudge was not sent: %s", why)
	}
	m.sendWatchNudge(r.st, r.act, inst, r.text)
	return nil
}

// sendWatchNudge queues text for inst's Claude through the loom mod
// (OriginWatch). A nudge the mod can't take lands in the issue's status. The
// send's earlier error is forgotten first, so a refusal of a past tick does
// not outlive a nudge that is on its way, and a report enqueue makes at once
// (no mod) is not wiped.
func (m *Model) sendWatchNudge(st *watchState, a watch.Action, inst *session.Instance, text string) {
	clearWatchActErr(st, a)
	m.enqueue(inst, &modMessage{text: text, origin: OriginWatch, onUndelivered: func(why string) {
		setWatchActErr(st, a, "the nudge was not delivered: "+why)
	}})
}
