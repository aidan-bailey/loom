package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/feedback"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fbPublished files a note, opens a thread for it and links the thread to
// repo#number as published, straight in m's inbox. It returns the thread.
func fbPublished(t *testing.T, m *Model, repo string, number int) feedback.ThreadID {
	t.Helper()
	now := time.Now()
	n := m.feedback.inbox.Add(feedback.Note{Kind: feedback.KindBug, Title: "t", Text: "x", At: now})
	id, err := m.feedback.inbox.OpenThread(n, "")
	require.NoError(t, err)
	require.NoError(t, m.feedback.inbox.Publish(id, repo, number, n, now))
	return id
}

func TestFeedbackView_IssueErrsAreCopiedAndOnlyForPublishedThreads(t *testing.T) {
	m := NewForTest(Options{})
	a := fbPublished(t, m, "o/r", 5)
	b := fbPublished(t, m, "o/r", 6)
	m.feedback.issueErrs = map[feedback.ThreadID]string{a: "gh issue view: exit status 1", b: "timeout"}

	v := m.Feedback()
	assert.Equal(t, map[feedback.ThreadID]string{a: "gh issue view: exit status 1", b: "timeout"}, v.IssueErrs)

	v.IssueErrs[a] = "edited"
	assert.Equal(t, "gh issue view: exit status 1", m.feedback.issueErrs[a], "the view is a copy of the model's map")

	c := v.Clone()
	c.IssueErrs[b] = "edited"
	assert.Equal(t, "timeout", v.IssueErrs[b], "Clone deep-copies the map")

	m.feedback.inbox.CloseIssue("o/r", 6, "closed by hand", time.Now(), time.Now())
	assert.Equal(t, map[feedback.ThreadID]string{a: "gh issue view: exit status 1"}, m.Feedback().IssueErrs,
		"a thread that is no longer published drops out")

	m.feedback.inbox.CloseIssue("o/r", 5, "closed by hand", time.Now(), time.Now())
	assert.Nil(t, m.Feedback().IssueErrs, "none left: nil, not an empty map")
	assert.Nil(t, FeedbackView{}.Clone().IssueErrs)
}

const fbOpen = `{"state":"OPEN","stateReason":""}`

// fbClosed is gh's answer for an issue closed with stateReason reason.
func fbClosed(reason string) string {
	return `{"state":"CLOSED","stateReason":"` + reason + `"}`
}

// fbArgv is the argv of the check of issue number in repo o/r.
func fbArgv(number int) string {
	return fmt.Sprintf("gh issue view %d -R o/r --json state,stateReason", number)
}

// fbIssueRig is a model with no workspace whose inbox holds one published
// thread per issue (the same issue twice is a fold), whose gh is a fake with
// no answers yet (fbScript), and whose feedback folder is dir.
func fbIssueRig(t *testing.T, issues ...feedbackIssueKey) (m *Model, gh *ghFakeExec, dir string, ids []feedback.ThreadID) {
	t.Helper()
	gh = &ghFakeExec{}
	m = NewForTest(Options{GHExec: gh})
	dir = t.TempDir()
	m.feedback.dir = dir
	for _, k := range issues {
		ids = append(ids, fbPublished(t, m, k.repo, k.number))
	}
	return m, gh, dir, ids
}

// fbScript replaces gh's answers for the feedback folder: issue number of
// repo o/r to its JSON answer, or "!" and a message for a failing call.
func fbScript(gh *ghFakeExec, dir string, answers map[int]string) {
	var list []ghFakeAnswer
	for n, out := range answers {
		a := ghFakeAnswer{substr: fmt.Sprintf("view %d -R o/r ", n), out: out}
		if msg, failed := strings.CutPrefix(out, "!"); failed {
			a.out, a.err = "", errors.New(msg)
		}
		list = append(list, a)
	}
	gh.answers = map[string][]ghFakeAnswer{dir: list}
}

// fbIssuePoll runs one poll as the tick would: it makes the gate due,
// dispatches, runs the job and delivers its result.
func fbIssuePoll(t *testing.T, m *Model) {
	t.Helper()
	m.gate(gateFeedbackIssues).expedite()
	require.True(t, m.maybeCheckFeedbackIssues())
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	m.Deliver(jobs[0]())
}

// fbThreadOf is thread id as the model's view shows it.
func fbThreadOf(t *testing.T, m *Model, id feedback.ThreadID) feedback.Thread {
	t.Helper()
	th, ok := m.Feedback().Inbox.Thread(id)
	require.True(t, ok)
	return th
}

// fbSlowExec answers every call after delay with out and err, as a gh call
// the poll's budget ran out on would.
type fbSlowExec struct {
	delay time.Duration
	out   string
	err   error
	calls int
}

func (s *fbSlowExec) answer() ([]byte, error) {
	s.calls++
	time.Sleep(s.delay)
	return []byte(s.out), s.err
}

func (s *fbSlowExec) Run(c *exec.Cmd) error                      { _, err := s.answer(); return err }
func (s *fbSlowExec) Output(c *exec.Cmd) ([]byte, error)         { return s.answer() }
func (s *fbSlowExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return s.answer() }

func TestFeedbackIssues_ClosedIssueClosesItsThreads(t *testing.T) {
	m, gh, dir, ids := fbIssueRig(t, feedbackIssueKey{"o/r", 57}, feedbackIssueKey{"o/r", 57}, feedbackIssueKey{"o/r", 58})
	fbScript(gh, dir, map[int]string{57: fbClosed("COMPLETED"), 58: fbOpen})
	rev := m.feedback.rev

	fbIssuePoll(t, m)

	require.Len(t, gh.calls, 2, "one check per issue, though two threads link to #57")
	for _, c := range gh.calls {
		assert.Equal(t, dir, c.repo, "the check runs in the feedback folder")
	}
	for _, id := range ids[:2] {
		th := fbThreadOf(t, m, id)
		assert.Equal(t, feedback.Closed, th.Status, "thread %d", id)
		assert.Equal(t, "issue #57 closed (completed)", th.Closed.Reason)
		assert.False(t, th.Closed.At.IsZero())
	}
	assert.Equal(t, feedback.Published, fbThreadOf(t, m, ids[2]).Status, "#58 is still open")
	assert.Greater(t, m.feedback.rev, rev, "clients are told")

	disk, err := feedback.Load(dir)
	require.NoError(t, err)
	th, ok := disk.Thread(ids[0])
	require.True(t, ok)
	assert.Equal(t, feedback.Closed, th.Status, "saved")
	assert.Equal(t, "issue #57 closed (completed)", th.Closed.Reason)
}

func TestFeedbackIssues_TheCloseReason(t *testing.T) {
	for stateReason, want := range map[string]string{
		"COMPLETED":     "issue #7 closed (completed)",
		"NOT_PLANNED":   "issue #7 closed (not planned)",
		"DUPLICATE":     "issue #7 closed (duplicate)",
		"SOMETHING_NEW": "issue #7 closed (something new)",
		"":              "issue #7 closed",
		"two\nlines":    "issue #7 closed",
	} {
		m, gh, dir, ids := fbIssueRig(t, feedbackIssueKey{"o/r", 7})
		fbScript(gh, dir, map[int]string{7: `{"state":"CLOSED","stateReason":"` + strings.ReplaceAll(stateReason, "\n", `\n`) + `"}`})

		fbIssuePoll(t, m)

		th := fbThreadOf(t, m, ids[0])
		assert.Equal(t, feedback.Closed, th.Status, "an unfamiliar or missing reason still closes: %q", stateReason)
		assert.Equal(t, want, th.Closed.Reason, "%q", stateReason)
	}
}

func TestFeedbackIssues_FailedCheckChangesNothing(t *testing.T) {
	m, gh, dir, ids := fbIssueRig(t, feedbackIssueKey{"o/r", 5}, feedbackIssueKey{"o/r", 6})
	fbScript(gh, dir, map[int]string{5: "!exit status 1", 6: fbOpen})
	rev := m.feedback.rev

	fbIssuePoll(t, m)

	for _, id := range ids {
		assert.Equal(t, feedback.Published, fbThreadOf(t, m, id).Status)
	}
	assert.Equal(t, map[feedback.ThreadID]string{ids[0]: "gh issue view: exit status 1"}, m.Feedback().IssueErrs)
	assert.Greater(t, m.feedback.rev, rev, "the error reaches the clients")
	_, err := os.Stat(filepath.Join(dir, feedback.StoreFile))
	assert.True(t, os.IsNotExist(err), "a failed check writes nothing")

	// The next poll checks again, and a CLOSED answer now closes the thread
	// and takes its error away.
	fbScript(gh, dir, map[int]string{5: fbClosed("NOT_PLANNED"), 6: fbOpen})
	fbIssuePoll(t, m)
	th := fbThreadOf(t, m, ids[0])
	assert.Equal(t, feedback.Closed, th.Status)
	assert.Equal(t, "issue #5 closed (not planned)", th.Closed.Reason)
	assert.Nil(t, m.Feedback().IssueErrs)
	assert.Empty(t, m.feedback.issueErrs, "the model forgets the closed thread's error too, not only the view")
}

func TestFeedbackIssues_ForgetsWhatAThreadClosedByHandLeft(t *testing.T) {
	m, gh, dir, ids := fbIssueRig(t, feedbackIssueKey{"o/r", 5}, feedbackIssueKey{"o/r", 6})
	fbScript(gh, dir, map[int]string{5: "!exit status 1", 6: fbOpen})
	fbIssuePoll(t, m)
	require.Contains(t, m.feedback.issueErrs, ids[0])
	require.Contains(t, m.feedback.issueChecked, feedbackIssueKey{"o/r", 5})

	require.NoError(t, m.feedback.inbox.Close(ids[0], "", time.Now()), "the user closes the thread between two polls")
	fbIssuePoll(t, m)

	assert.Empty(t, m.feedback.issueErrs, "its error goes at the next poll")
	assert.NotContains(t, m.feedback.issueChecked, feedbackIssueKey{"o/r", 5}, "and its check time")
	assert.Contains(t, m.feedback.issueChecked, feedbackIssueKey{"o/r", 6}, "a published thread's stays")
}

func TestFeedbackIssues_AnOpenIssueClearsTheError(t *testing.T) {
	m, gh, dir, ids := fbIssueRig(t, feedbackIssueKey{"o/r", 5})
	fbScript(gh, dir, map[int]string{5: "!timeout"})
	fbIssuePoll(t, m)
	require.Contains(t, m.Feedback().IssueErrs, ids[0])

	fbScript(gh, dir, map[int]string{5: fbOpen})
	fbIssuePoll(t, m)

	assert.Nil(t, m.Feedback().IssueErrs)
	assert.Equal(t, feedback.Published, fbThreadOf(t, m, ids[0]).Status)
}

func TestFeedbackIssues_ARepeatedFailureBumpsNoRevision(t *testing.T) {
	m, gh, dir, _ := fbIssueRig(t, feedbackIssueKey{"o/r", 5})
	fbScript(gh, dir, map[int]string{5: "!exit status 1"})
	fbIssuePoll(t, m)
	rev := m.feedback.rev

	fbIssuePoll(t, m)

	assert.Equal(t, rev, m.feedback.rev, "the same error again tells the clients nothing new")
}

func TestFeedbackIssues_AFailedSaveStillClosesInMemoryAndSaysSo(t *testing.T) {
	m, gh, dir, ids := fbIssueRig(t, feedbackIssueKey{"o/r", 5})
	bad := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(bad, nil, 0o600))
	m.feedback.dir = bad // a file where the feedback folder should be: Save fails
	fbScript(gh, bad, map[int]string{5: fbClosed("COMPLETED")})
	rev := m.feedback.rev

	fbIssuePoll(t, m)

	assert.Equal(t, feedback.Closed, fbThreadOf(t, m, ids[0]).Status, "closed in memory though it could not be written")
	assert.Greater(t, m.feedback.rev, rev, "the clients are told")
	notices, _ := noticesAndStarts(m.Drain().Events)
	require.Len(t, notices, 1)
	assert.ErrorContains(t, notices[0].Err, "feedback inbox: write feedback.json")
	assert.ErrorContains(t, notices[0].Err, "the change is kept in memory")
}

func TestFeedbackIssues_ClosesOnlyThreadsLinkedWhenThePollBegan(t *testing.T) {
	m, _, _, ids := fbIssueRig(t, feedbackIssueKey{"o/r", 5}, feedbackIssueKey{"o/r", 5})
	began := time.Now()
	for i := range m.feedback.inbox.Threads {
		th := &m.feedback.inbox.Threads[i]
		switch th.ID {
		case ids[0]:
			th.Issue.At = began.Add(-time.Second)
		case ids[1]:
			th.Issue.At = began.Add(time.Second) // linked while the check ran
		}
	}

	m.Deliver(feedbackIssuesResult{at: began, answers: []feedbackIssueAnswer{
		{key: feedbackIssueKey{"o/r", 5}, state: github.IssueState{Closed: true, Reason: "completed"}},
	}})

	assert.Equal(t, feedback.Closed, fbThreadOf(t, m, ids[0]).Status, "linked before the poll began")
	assert.Equal(t, feedback.Published, fbThreadOf(t, m, ids[1]).Status, "the answer predates this link: the next poll asks")
}

func TestFeedbackIssues_ARepositoryKeyIgnoresCase(t *testing.T) {
	m, gh, dir, ids := fbIssueRig(t, feedbackIssueKey{"O/R", 5}, feedbackIssueKey{"o/r", 5})
	fbScript(gh, dir, map[int]string{5: fbClosed("COMPLETED")})

	fbIssuePoll(t, m)

	require.Len(t, gh.calls, 1, "one check for the two spellings of one repository")
	for _, id := range ids {
		assert.Equal(t, feedback.Closed, fbThreadOf(t, m, id).Status, "thread %d", id)
	}
}

func TestFeedbackIssues_ACheckTheBudgetCutShortIsDropped(t *testing.T) {
	keys := []feedbackIssueKey{{"o/r", 1}, {"o/r", 2}}

	// The budget ends the first call with an error: the issue was never
	// asked about, so no answer, and the second key is not tried.
	ex := &fbSlowExec{delay: 50 * time.Millisecond, err: errors.New("signal: killed")}
	res := checkFeedbackIssues("/fb", keys, ex, 10*time.Millisecond)
	assert.Empty(t, res.answers)
	assert.Equal(t, 1, ex.calls)

	// A call that answered is an answer, though its budget ran out meanwhile.
	ex = &fbSlowExec{delay: 50 * time.Millisecond, out: fbOpen}
	res = checkFeedbackIssues("/fb", keys, ex, 10*time.Millisecond)
	require.Len(t, res.answers, 1)
	assert.NoError(t, res.answers[0].err)
	assert.Equal(t, 1, ex.calls, "but the spent budget starts no second call")

	// The same failure with budget to spare is the issue's error.
	ex = &fbSlowExec{err: errors.New("exit status 1")}
	res = checkFeedbackIssues("/fb", keys, ex, time.Minute)
	require.Len(t, res.answers, 2)
	assert.Error(t, res.answers[0].err)
}

func TestFeedbackIssues_ChecksTenLeastRecentlyCheckedFirst(t *testing.T) {
	var keys []feedbackIssueKey
	answers := map[int]string{}
	for n := 1; n <= 12; n++ {
		keys = append(keys, feedbackIssueKey{"o/r", n})
		answers[n] = fbOpen
	}
	m, gh, dir, _ := fbIssueRig(t, keys...)
	fbScript(gh, dir, answers)

	fbIssuePoll(t, m)
	require.Len(t, gh.calls, feedbackIssueChecks, "at most 10 checks a poll")
	assert.Equal(t, fbArgv(1), gh.calls[0].argv)
	assert.Equal(t, fbArgv(10), gh.calls[9].argv)

	gh.calls = nil
	fbIssuePoll(t, m)
	require.Len(t, gh.calls, feedbackIssueChecks)
	assert.Equal(t, []string{fbArgv(11), fbArgv(12), fbArgv(1)},
		[]string{gh.calls[0].argv, gh.calls[1].argv, gh.calls[2].argv},
		"the two never checked go first, then the oldest check")
}

func TestFeedbackIssues_TheBudgetEndsTheChecks(t *testing.T) {
	gh := &ghFakeExec{}
	keys := []feedbackIssueKey{{"o/r", 1}, {"o/r", 2}}

	res := checkFeedbackIssues("/fb", keys, gh, 0)
	assert.Empty(t, res.answers, "a spent budget starts no check")
	assert.Empty(t, gh.calls)

	res = checkFeedbackIssues("/fb", keys, gh, time.Minute)
	require.Len(t, res.answers, 2, "inside the budget every issue is asked about")
	assert.Len(t, gh.calls, 2)
}

func TestFeedbackIssues_DispatchesOnlyForAPublishedThreadInALoadedInbox(t *testing.T) {
	// An open thread is no reason to ask.
	m := NewForTest(Options{})
	m.feedback.dir = t.TempDir()
	n := m.feedback.inbox.Add(feedback.Note{Kind: feedback.KindBug, Title: "t", Text: "x", At: time.Now()})
	_, err := m.feedback.inbox.OpenThread(n, "")
	require.NoError(t, err)
	assert.False(t, m.maybeCheckFeedbackIssues(), "no thread is published")
	assert.True(t, m.gate(gateFeedbackIssues).last.IsZero(), "nothing was dispatched, so nothing is armed")

	m, _, _, _ = fbIssueRig(t, feedbackIssueKey{"o/r", 5})
	m.feedback.dir = ""
	assert.False(t, m.maybeCheckFeedbackIssues(), "no feedback folder")

	m.feedback.dir = t.TempDir()
	m.feedback.loadErr = errors.New("feedback.json did not parse")
	assert.False(t, m.maybeCheckFeedbackIssues(), "an inbox that did not load")
	assert.True(t, m.gate(gateFeedbackIssues).last.IsZero())

	m.feedback.loadErr = nil
	assert.True(t, m.maybeCheckFeedbackIssues(), "a published thread in a loaded inbox")
}

func TestFeedbackIssues_OneInFlightEveryFiveMinutes(t *testing.T) {
	assert.Equal(t, 5*time.Minute, feedbackIssuesInterval)
	assert.Equal(t, feedbackIssuesInterval, gateIntervals[gateFeedbackIssues])
	assert.Equal(t, 10, feedbackIssueChecks)
	assert.Equal(t, 60*time.Second, feedbackIssueBudget)
	assert.Equal(t, "feedback_issues", gateFeedbackIssues.String())

	m, gh, dir, _ := fbIssueRig(t, feedbackIssueKey{"o/r", 5})
	fbScript(gh, dir, map[int]string{5: fbOpen})

	require.True(t, m.maybeCheckFeedbackIssues())
	assert.Len(t, m.bgJobs, 1, "the tick's poll is background work")
	assert.Empty(t, m.out.Jobs, "and no request's follow-on")
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	assert.False(t, m.maybeCheckFeedbackIssues(), "one poll at a time")

	m.Deliver(jobs[0]())
	assert.False(t, m.gate(gateFeedbackIssues).inFlight, "the delivery disarms the gate")
	assert.False(t, m.maybeCheckFeedbackIssues(), "inside the interval")

	m.gate(gateFeedbackIssues).last = time.Now().Add(-feedbackIssuesInterval)
	assert.True(t, m.maybeCheckFeedbackIssues(), "five minutes on")
}

func TestTickInst_ChecksTheFeedbackIssues(t *testing.T) {
	m, gh, dir, _ := fbIssueRig(t, feedbackIssueKey{"o/r", 5})
	fbScript(gh, dir, map[int]string{5: fbOpen})

	m.tickInst(nil)

	assert.True(t, m.gate(gateFeedbackIssues).inFlight, "the health tick starts the issue poll")
}
