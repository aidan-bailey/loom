package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/feedback"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/github"
)

// The issue poll (feedback triage spec §5): a thread published as an issue
// closes when the issue does.
const (
	// feedbackIssuesInterval is the poll's cadence, on the health tick.
	feedbackIssuesInterval = 5 * time.Minute
	// feedbackIssueChecks caps the issues one poll asks about.
	feedbackIssueChecks = 10
	// feedbackIssueBudget bounds one poll's gh calls together, as
	// ghPollBudget bounds the GitHub poll's: under feedbackIssuesInterval,
	// so a poll cannot outlive its own cadence.
	feedbackIssueBudget = 60 * time.Second
)

// feedbackIssueKey names an issue.
type feedbackIssueKey struct {
	repo   string
	number int
}

// feedbackKeyOf is the issue thread t links to. Its repository is lowercased:
// GitHub's names don't differ by case and the inbox compares them without it,
// so every thread linked to one issue, however spelled, shares one key.
func feedbackKeyOf(t feedback.Thread) feedbackIssueKey {
	return feedbackIssueKey{repo: strings.ToLower(t.Issue.Repo), number: t.Issue.Number}
}

// feedbackIssueAnswer is one check: the issue's state, or why gh couldn't say.
type feedbackIssueAnswer struct {
	key   feedbackIssueKey
	state github.IssueState
	err   error
}

// feedbackIssuesResult is a poll's outcome, delivered on the loop: the
// checks that ran, and when the poll began (each one's check time).
type feedbackIssuesResult struct {
	at      time.Time
	answers []feedbackIssueAnswer
}

// maybeCheckFeedbackIssues dispatches a poll of the published threads'
// issues when gateFeedbackIssues is due. Nothing when the inbox didn't load,
// the feedback folder is unknown, or no thread is published; it needs no
// workspace and no client, since each link names its repository.
func (m *Model) maybeCheckFeedbackIssues() bool {
	return m.dispatchGated(gateFeedbackIssues, time.Now(), func() Job {
		f := m.feedback
		if f.dir == "" || f.loadErr != nil {
			return nil
		}
		keys := m.feedbackIssuesToCheck()
		if len(keys) == 0 {
			return nil
		}
		dir, r := f.dir, m.watchExec()
		return func() any { return checkFeedbackIssues(dir, keys, r, feedbackIssueBudget) }
	})
}

// feedbackIssuesToCheck is the issues of the published threads, each once
// (threads folded into one issue share its check), the least recently
// checked first and at most feedbackIssueChecks of them. An issue never
// checked sorts first; ties keep the thread order. Loop-goroutine only.
func (m *Model) feedbackIssuesToCheck() []feedbackIssueKey {
	f := m.feedback
	var keys []feedbackIssueKey
	for _, t := range f.inbox.Published() {
		k := feedbackKeyOf(t)
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	slices.SortStableFunc(keys, func(a, b feedbackIssueKey) int {
		return f.issueChecked[a].Compare(f.issueChecked[b])
	})
	return keys[:min(len(keys), feedbackIssueChecks)]
}

// checkFeedbackIssues is the poll's job: IssueStateOf for each key, in dir,
// until budget is spent. A check that fails is an answer with its error; one
// the budget never reached, or cut short, is no answer (the issue was never
// asked about), so it is first in line next poll.
func checkFeedbackIssues(dir string, keys []feedbackIssueKey, r internalexec.Executor, budget time.Duration) feedbackIssuesResult {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	res := feedbackIssuesResult{at: time.Now()}
	for _, k := range keys {
		if ctx.Err() != nil {
			break
		}
		st, err := github.IssueStateOf(ctx, dir, k.repo, k.number, r)
		if err != nil && ctx.Err() != nil {
			break
		}
		res.answers = append(res.answers, feedbackIssueAnswer{key: k, state: st, err: err})
	}
	return res
}

// feedbackIssueCloseReason is the reason a closed issue gives its threads:
// `issue #N closed (<reason>)`, or `issue #N closed` when GitHub gave none.
func feedbackIssueCloseReason(number int, reason string) string {
	if reason == "" {
		return fmt.Sprintf("issue #%d closed", number)
	}
	return fmt.Sprintf("issue #%d closed (%s)", number, reason)
}

// deliverFeedbackIssues applies a poll. A CLOSED issue closes, in one
// Inbox.CloseIssue call, the threads linked to it before the poll began (a
// thread linked while the check ran waits for the next poll: the answer may
// predate its link), and the inbox is saved; a failed check changes no status and keeps its error on the issue's
// threads (an OPEN answer clears it). The revision moves only when something
// a client shows did, so a failure that repeats says nothing twice. Check
// times are remembered for the next poll's order.
func (m *Model) deliverFeedbackIssues(r feedbackIssuesResult) {
	f := m.feedback
	now := time.Now()
	linked := map[feedbackIssueKey][]feedback.ThreadID{}
	early := map[feedbackIssueKey][]feedback.ThreadID{} // those linked when the poll began
	published := map[feedback.ThreadID]bool{}
	for _, t := range f.inbox.Published() {
		k := feedbackKeyOf(t)
		linked[k] = append(linked[k], t.ID)
		if !t.Issue.At.After(r.at) {
			early[k] = append(early[k], t.ID)
		}
		published[t.ID] = true
	}
	if f.issueChecked == nil {
		f.issueChecked = map[feedbackIssueKey]time.Time{}
	}
	if f.issueErrs == nil {
		f.issueErrs = map[feedback.ThreadID]string{}
	}
	changed, closed := false, false
	for _, a := range r.answers {
		f.issueChecked[a.key] = r.at
		switch {
		case a.err != nil:
			log.For("core").Warn("feedback.issue_check_failed", "repo", a.key.repo, "issue", a.key.number, "err", a.err)
			for _, id := range early[a.key] {
				if f.issueErrs[id] != a.err.Error() {
					f.issueErrs[id] = a.err.Error()
					changed = true
				}
			}
		case a.state.Closed:
			reason := feedbackIssueCloseReason(a.key.number, a.state.Reason)
			ids := f.inbox.CloseIssue(a.key.repo, a.key.number, reason, r.at, now)
			for _, id := range ids {
				delete(published, id)
			}
			log.For("core").Info("feedback.issue_closed", "repo", a.key.repo, "issue", a.key.number, "reason", reason, "threads", len(ids))
			delete(f.issueChecked, a.key)
			closed = closed || len(ids) > 0
		default:
			for _, id := range early[a.key] {
				if _, ok := f.issueErrs[id]; ok {
					delete(f.issueErrs, id)
					changed = true
				}
			}
		}
	}
	// Forget what no published thread needs any more: the error of a thread
	// closed here or by the user, the check time of an issue no thread holds.
	for id := range f.issueErrs {
		if !published[id] {
			delete(f.issueErrs, id)
		}
	}
	for k := range f.issueChecked {
		if _, ok := linked[k]; !ok {
			delete(f.issueChecked, k)
		}
	}
	if closed {
		if err := m.saveFeedback(); err != nil {
			log.For("core").Error("feedback.save_failed", "err", err)
			m.notifyErr(fmt.Errorf("feedback inbox: %w; the change is kept in memory", err))
		}
	}
	if closed || changed {
		f.rev++
	}
}
