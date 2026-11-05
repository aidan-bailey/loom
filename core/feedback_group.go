package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/feedback"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched/advisor"
)

// grouperRetryAfter and grouperPauseAfter are the advisor's
// (advisorRetryAfter, advisorPauseAfter): a failed note waits this long, and
// this many failures in a row pause the grouper (spec §5).
const (
	grouperRetryAfter = advisorRetryAfter
	grouperPauseAfter = advisorPauseAfter
)

// grouperDirName is the empty folder the call runs in, under the feedback
// dir: --restricted confines its read-only tools to it.
const grouperDirName = "grouper"

// groupInput is what a call's job needs: copies only.
type groupInput struct {
	note    feedback.NoteID
	offered []feedback.ThreadID
	call    advisor.Call
}

// groupResult is a call's outcome, delivered on the loop.
type groupResult struct {
	note          feedback.NoteID
	offered       []feedback.ThreadID
	model, effort string
	raw           json.RawMessage
	usage         advisor.Usage
	err           error
}

// maybeGroupFeedback dispatches a grouping call for the next due note
// (feedback.Inbox.Due) while the grouper is on: one call in flight at a
// time, through gateFeedback. Nothing when no note is due.
func (m *Model) maybeGroupFeedback() bool {
	now := time.Now()
	return m.dispatchGated(gateFeedback, now, func() Job {
		f := m.feedback
		n, ok := f.inbox.Due(now)
		if !ok {
			return nil
		}
		m.refreshFeedbackSettings(now, false)
		if m.grouperOff() != "" {
			return nil
		}
		req, offered := feedback.BuildRequest(f.inbox, n)
		msg, err := json.Marshal(req)
		if err != nil {
			log.For("core").Error("feedback.request_failed", "note", int(n.ID), "err", err)
			return nil
		}
		env, _ := m.accountEnv(f.settings.Account)
		in := groupInput{note: n.ID, offered: offered, call: advisor.Call{
			Program: m.grouperProgram(), Env: env, Dir: filepath.Join(f.dir, grouperDirName),
			Model: f.settings.Model, Effort: f.settings.Effort, Schema: feedback.Schema,
			SystemPrompt: feedback.SystemPrompt, Message: msg, Timeout: f.timeout,
		}}
		return func() any { return runGroup(in) }
	})
}

// runGroup is a call's job: it makes the empty folder and runs the CLI.
func runGroup(in groupInput) groupResult {
	res := groupResult{note: in.note, offered: in.offered, model: in.call.Model, effort: in.call.Effort}
	if err := os.MkdirAll(in.call.Dir, 0o700); err != nil {
		res.err = fmt.Errorf("make the grouper's folder: %w", err)
		return res
	}
	out, err := advisor.Run(in.call)
	res.raw, res.usage, res.err = out.Answer, out.Usage, err
	return res
}

// deliverFeedbackGroup files the note by the answer, or records the
// failure: ungrouped and retried after grouperRetryAfter, and after
// grouperPauseAfter in a row the grouper pauses, with a Notice for every
// client. It logs the call, saves, and dispatches the next.
func (m *Model) deliverFeedbackGroup(r groupResult) {
	f := m.feedback
	now := time.Now()
	rec := feedback.CallRecord{At: now, Note: r.note, Offered: slices.Clone(r.offered), Model: r.model, Effort: r.effort,
		Usage: r.usage, Answer: string(r.raw)}
	err := r.err
	var thread feedback.ThreadID
	if err == nil {
		var a feedback.Answer
		if a, err = feedback.ParseAnswer(r.raw); err == nil {
			thread, err = feedback.Apply(&f.inbox, r.note, a, r.offered)
		}
	}
	if err != nil {
		rec.Err = err.Error()
		f.inbox.Fail(r.note, err.Error(), now.Add(grouperRetryAfter))
		f.failures++
		log.For("core").Warn("feedback.group_failed", "note", int(r.note), "failures", f.failures, "err", err)
		if f.failures >= grouperPauseAfter && f.paused == "" {
			f.paused, f.pausedWith = err.Error(), f.settings
			m.notifyErr(fmt.Errorf("feedback grouper: paused after %d failed calls in a row: %w; notes stay ungrouped until the daemon restarts or feedback.grouper changes",
				grouperPauseAfter, err))
		}
	} else {
		f.failures, rec.Thread = 0, thread
	}
	if f.dir != "" {
		if lerr := feedback.AppendCall(f.dir, rec); lerr != nil {
			log.For("core").Warn("feedback.call_log_failed", "err", lerr)
		}
	}
	if serr := m.saveFeedback(); serr != nil {
		log.For("core").Error("feedback.save_failed", "err", serr)
		m.notifyErr(fmt.Errorf("feedback inbox: %w; the change is kept in memory", serr))
	}
	f.rev++
	m.maybeGroupFeedback()
}
