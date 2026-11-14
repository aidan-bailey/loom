package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watchExec records each gh invocation (its argv and working
// directory) and answers it from fn. It is the watcher tests' fake
// for internalexec.Executor; the existing scriptedExec cannot answer a
// command twice differently.
type watchExec struct {
	calls []string
	dirs  []string
	fn    func(argv string) (string, error)
}

func (w *watchExec) Output(c *exec.Cmd) ([]byte, error) {
	argv := strings.Join(c.Args, " ")
	w.calls = append(w.calls, argv)
	w.dirs = append(w.dirs, c.Dir)
	out, err := w.fn(argv)
	return []byte(out), err
}

func (w *watchExec) Run(c *exec.Cmd) error {
	_, err := w.Output(c)
	return err
}

func (w *watchExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return w.Output(c) }

// answer is an fn that returns out for every command.
func answer(out string) func(string) (string, error) {
	return func(string) (string, error) { return out, nil }
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return string(b)
}

func TestWatchPRs_ParsesTheFixture(t *testing.T) {
	ex := &watchExec{fn: answer(readFixture(t, "watch_prs.json"))}
	prs, err := WatchPRs(context.Background(), "/repo", ex)
	require.NoError(t, err)
	require.Len(t, prs, 3)

	open := prs[0]
	assert.Equal(t, 101, open.Number)
	assert.Equal(t, "OPEN", open.State)
	assert.Equal(t, "loom/issue-7-fix-parser", open.HeadRefName)
	assert.Equal(t, "main", open.BaseRefName)
	assert.Equal(t, "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", open.HeadSHA)
	assert.Equal(t, "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1", open.BaseSHA)
	assert.Equal(t, "CONFLICTING", open.Mergeable)
	assert.Equal(t, "loom-bot", open.Author)
	assert.False(t, open.CrossRepo)
	assert.Equal(t, []int{7}, open.Closes)
	assert.Equal(t, "Fixes #7\n\nThe parser now accepts empty input.", open.Body)
	assert.Equal(t, "https://github.com/o/r/pull/101", open.URL)
	// FAILURE, TIMED_OUT and a StatusContext ERROR fail; SUCCESS,
	// CANCELLED and an unfinished check do not; the order is gh's.
	assert.Equal(t, []FailedCheck{
		{Name: "build", URL: "https://github.com/o/r/actions/runs/9001/job/7001?pr=101", JobID: 7001},
		{Name: "integration", URL: "https://github.com/o/r/runs/4242", JobID: 0},
		{Name: "ci/external", URL: "https://ci.example.com/builds/5", JobID: 0},
	}, open.Failing)

	merged := prs[1]
	assert.Equal(t, 102, merged.Number)
	assert.Equal(t, "MERGED", merged.State)
	assert.Equal(t, "someone-else", merged.Author)
	assert.True(t, merged.CrossRepo)
	assert.Equal(t, []int{7, 8}, merged.Closes)
	assert.Equal(t, "UNKNOWN", merged.Mergeable)
	assert.Empty(t, merged.Failing)

	closed := prs[2]
	assert.Equal(t, "CLOSED", closed.State)
	assert.Equal(t, "release", closed.BaseRefName)
	assert.Empty(t, closed.Closes)
	assert.Empty(t, closed.Failing, "a null rollup is no failing check")
}

func TestWatchPRs_ClosesHoldsOnlyThisReposIssues(t *testing.T) {
	ref := func(n int, owner, repo string) string {
		return fmt.Sprintf(`{"number": %d, "repository": {"name": %q, "owner": {"login": %q}}}`, n, repo, owner)
	}
	for name, refs := range map[string]string{
		"another owner":        ref(7, "other", "r") + "," + ref(8, "o", "r"),
		"another repo":         ref(7, "o", "elsewhere") + "," + ref(8, "o", "r"),
		"no repository named":  `{"number": 7},` + ref(8, "o", "r"),
		"case-insensitive too": ref(7, "other", "r") + "," + ref(8, "O", "R"),
	} {
		t.Run(name, func(t *testing.T) {
			js := `[{"number": 1, "state": "OPEN", "url": "https://github.com/o/r/pull/1", "closingIssuesReferences": [` + refs + `]}]`
			prs, err := WatchPRs(context.Background(), "/repo", &watchExec{fn: answer(js)})
			require.NoError(t, err)
			require.Len(t, prs, 1)
			assert.Equal(t, []int{8}, prs[0].Closes, "#7 is another repo's issue, not this PR's")
		})
	}
}

// scriptedExec-style substring matching hides the flags, so pin them:
// --author @me and --state all are what make the watcher's own merged
// PRs visible, and the field list is what fills every WatchPR field.
func TestWatchPRs_PassesLoadBearingFlagsInTheRepoDir(t *testing.T) {
	ex := &watchExec{fn: answer(`[]`)}
	prs, err := WatchPRs(context.Background(), "/repo", ex)
	require.NoError(t, err)
	assert.Empty(t, prs)
	require.Len(t, ex.calls, 1)
	assert.Equal(t, "gh pr list --author @me --state all --limit 100 --json "+
		"number,state,headRefName,baseRefName,headRefOid,baseRefOid,mergeable,author,"+
		"isCrossRepository,closingIssuesReferences,statusCheckRollup,body,url", ex.calls[0])
	assert.Equal(t, "/repo", ex.dirs[0])
}

func TestWatchPRs_FailsClosed(t *testing.T) {
	_, err := WatchPRs(context.Background(), "/repo", &watchExec{fn: func(string) (string, error) {
		return "", assert.AnError
	}})
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError, "a gh failure is returned, never an empty list")

	_, err = WatchPRs(context.Background(), "/repo", &watchExec{fn: answer(`{"not":"a list"}`)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing gh pr list")
}

func TestJobIDFromURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want int64
	}{
		{"an actions job", "https://github.com/o/r/actions/runs/9001/job/7001", 7001},
		{"an actions job with a query", "https://github.com/o/r/actions/runs/9001/job/7001?pr=101", 7001},
		{"a legacy check run", "https://github.com/o/r/runs/4242", 0},
		{"a run without a job", "https://github.com/o/r/actions/runs/9001", 0},
		{"an external status", "https://ci.example.com/builds/5", 0},
		{"empty", "", 0},
		{"a job id that overflows int64", "https://github.com/o/r/actions/runs/9001/job/99999999999999999999", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, jobIDFromURL(tc.url))
		})
	}
}

func TestWatchIssues_ParsesSortsAndKeepsTheComments(t *testing.T) {
	ex := &watchExec{fn: answer(readFixture(t, "watch_issues.json"))}
	issues, err := WatchIssues(context.Background(), "/repo", "loom", ex)
	require.NoError(t, err)
	require.Len(t, issues, 3)
	assert.Equal(t, []int{9, 12, 14}, []int{issues[0].Number, issues[1].Number, issues[2].Number},
		"sorted by number, whatever order gh answered in")

	is := issues[1]
	assert.Equal(t, "Parser rejects empty input", is.Title)
	assert.Equal(t, "Parser rejects empty input.\nExpected: accept it.", is.Body)
	assert.Equal(t, "https://github.com/o/r/issues/12", is.URL)
	assert.Equal(t, []string{"loom", "bug"}, is.Labels)
	assert.False(t, is.Closed, "a listed open issue is not closed")
	require.Len(t, is.Comments, 2)
	assert.Equal(t, IssueComment{
		ID:          "IC_kwDOA1",
		Author:      "maintainer",
		Association: "OWNER",
		Body:        "Please also cover the CRLF case.",
		CreatedAt:   time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC),
	}, is.Comments[0])
	assert.Equal(t, "NONE", is.Comments[1].Association)
	assert.Equal(t, "drive-by", is.Comments[1].Author)

	assert.Empty(t, issues[0].Labels)
	assert.Empty(t, issues[0].Comments)
}

func TestWatchIssues_PassesLoadBearingFlagsInTheRepoDir(t *testing.T) {
	ex := &watchExec{fn: answer(`[]`)}
	_, err := WatchIssues(context.Background(), "/repo", "loom", ex)
	require.NoError(t, err)
	require.Len(t, ex.calls, 1)
	assert.Equal(t, "gh issue list --label loom --state open --search sort:created-asc --limit 51 "+
		"--json number,title,body,url,labels,comments", ex.calls[0])
	assert.Equal(t, "/repo", ex.dirs[0])
}

// gh lists newest first by default, so the limit would drop the oldest
// issues. WatchIssues asks for the oldest 51 and returns them sorted:
// the 51st is how Story 8 learns that more issues exist.
func TestWatchIssues_ReturnsAtMostOneMoreThanTheLimitOldestFirst(t *testing.T) {
	assert.Equal(t, 50, WatchIssueLimit)
	var raw []map[string]any
	for n := WatchIssueLimit + 3; n >= 1; n-- {
		raw = append(raw, map[string]any{
			"number": n, "title": "t", "body": "", "url": "u",
			"labels": []any{}, "comments": []any{},
		})
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)

	issues, err := WatchIssues(context.Background(), "/repo", "loom", &watchExec{fn: answer(string(b))})
	require.NoError(t, err)
	require.Len(t, issues, WatchIssueLimit+1)
	assert.Equal(t, 1, issues[0].Number)
	assert.Equal(t, WatchIssueLimit+1, issues[WatchIssueLimit].Number)
}

func TestWatchIssues_FailsClosed(t *testing.T) {
	_, err := WatchIssues(context.Background(), "/repo", "", &watchExec{fn: answer(`[]`)})
	require.Error(t, err, "an empty label would list every open issue")

	_, err = WatchIssues(context.Background(), "/repo", "loom", &watchExec{fn: func(string) (string, error) {
		return "", assert.AnError
	}})
	assert.ErrorIs(t, err, assert.AnError)

	_, err = WatchIssues(context.Background(), "/repo", "loom", &watchExec{fn: answer(`nope`)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing gh issue list")
}

func TestViewWatchIssue_ParsesAClosedIssueWithItsComments(t *testing.T) {
	ex := &watchExec{fn: answer(readFixture(t, "watch_issue_view.json"))}
	is, err := ViewWatchIssue(context.Background(), "/repo", 7, ex)
	require.NoError(t, err)
	assert.Equal(t, 7, is.Number)
	assert.Equal(t, "Fix the parser", is.Title)
	assert.True(t, is.Closed)
	assert.Equal(t, []string{"loom"}, is.Labels)
	require.Len(t, is.Comments, 1)
	assert.Equal(t, "MEMBER", is.Comments[0].Association)
	assert.Equal(t, "Fixed by #101.", is.Comments[0].Body)

	require.Len(t, ex.calls, 1)
	assert.Equal(t, "gh issue view 7 --json number,title,body,state,url,labels,comments", ex.calls[0])
	assert.Equal(t, "/repo", ex.dirs[0])
}

func TestViewWatchIssue_FailsClosed(t *testing.T) {
	_, err := ViewWatchIssue(context.Background(), "/repo", 7, &watchExec{fn: func(string) (string, error) {
		return "", assert.AnError
	}})
	assert.ErrorIs(t, err, assert.AnError)

	_, err = ViewWatchIssue(context.Background(), "/repo", 7, &watchExec{fn: answer(`[]`)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing gh issue view")
}

func TestViewer_ReturnsTheTrimmedLogin(t *testing.T) {
	ex := &watchExec{fn: answer("loom-bot\n")}
	login, err := Viewer(context.Background(), "/repo", ex)
	require.NoError(t, err)
	assert.Equal(t, "loom-bot", login)
	require.Len(t, ex.calls, 1)
	assert.Equal(t, "gh api user --jq .login", ex.calls[0])
	assert.Equal(t, "/repo", ex.dirs[0])
}

func TestViewer_FailsClosed(t *testing.T) {
	_, err := Viewer(context.Background(), "/repo", &watchExec{fn: answer("  \n")})
	require.Error(t, err, "an empty login would match no PR as the watcher's own, and must not pass for an answer")

	_, err = Viewer(context.Background(), "/repo", &watchExec{fn: func(string) (string, error) {
		return "", assert.AnError
	}})
	assert.ErrorIs(t, err, assert.AnError)
}

func TestFailedJobLogs_ReturnsTheLastLines(t *testing.T) {
	log := "a\nb\nc\nd\ne\nf\n"
	ex := &watchExec{fn: answer(log)}

	tail, err := FailedJobLogs(context.Background(), "/repo", 7001, 3, ex)
	require.NoError(t, err)
	assert.Equal(t, "d\ne\nf", tail)
	require.Len(t, ex.calls, 1)
	assert.Equal(t, "gh run view --job 7001 --log-failed", ex.calls[0])
	assert.Equal(t, "/repo", ex.dirs[0])

	whole, err := FailedJobLogs(context.Background(), "/repo", 7001, 100, &watchExec{fn: answer(log)})
	require.NoError(t, err)
	assert.Equal(t, "a\nb\nc\nd\ne\nf", whole, "a log shorter than lines comes back whole, without its trailing newline")
}

func TestFailedJobLogs_RefusesWhatItCannotFetch(t *testing.T) {
	ex := &watchExec{fn: answer("x\n")}
	_, err := FailedJobLogs(context.Background(), "/repo", 0, 5, ex)
	require.Error(t, err, "a check with no Actions job has no log to fetch")
	_, err = FailedJobLogs(context.Background(), "/repo", 7001, 0, ex)
	require.Error(t, err)
	assert.Empty(t, ex.calls, "neither refusal reaches gh")

	_, err = FailedJobLogs(context.Background(), "/repo", 7001, 5, &watchExec{fn: func(string) (string, error) {
		return "", assert.AnError
	}})
	assert.ErrorIs(t, err, assert.AnError)
}

// sleepExec runs the command's context as a real child that sleeps, so
// only the context's deadline can end it. Clearing Err matters where gh
// is not installed: CommandContext records the failed lookup there, and
// Start would refuse at once, making the test pass for nothing.
type sleepExec struct{}

func (sleepExec) Output(c *exec.Cmd) ([]byte, error) {
	c.Path = "/bin/sh"
	c.Args = []string{"sh", "-c", "exec sleep 5"}
	c.Err = nil
	return c.Output()
}

func (e sleepExec) Run(c *exec.Cmd) error {
	_, err := e.Output(c)
	return err
}

func (e sleepExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return e.Output(c) }

func TestGhWithTimeout_EndsTheChildAtItsOwnDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the child is /bin/sh and sleep, which Windows lacks")
	}
	start := time.Now()
	_, err := ghWithTimeout(context.Background(), sleepExec{}, t.TempDir(), 100*time.Millisecond, "run", "view")
	require.Error(t, err)
	assert.Less(t, time.Since(start), 3*time.Second, "the 100 ms deadline ends a 5 s child")
}

func TestFailedJobLogs_HasItsOwn30sTimeout(t *testing.T) {
	assert.Equal(t, 30*time.Second, failedLogsTimeout, "spec §7.1: longer than the 20 s every other call has")
	assert.Greater(t, failedLogsTimeout, ghTimeout)
}

func TestFailedJobLogs_EndsTheChildAtItsOwnDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the child is /bin/sh and sleep, which Windows lacks")
	}
	old := failedLogsTimeout
	failedLogsTimeout = 100 * time.Millisecond
	t.Cleanup(func() { failedLogsTimeout = old })
	start := time.Now()
	_, err := FailedJobLogs(context.Background(), t.TempDir(), 7001, 10, sleepExec{})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 3*time.Second, "FailedJobLogs ends its child at failedLogsTimeout, not at gh's 20 s")
}

func TestTailLines(t *testing.T) {
	assert.Equal(t, "c", tailLines("a\nb\nc\n", 1))
	assert.Equal(t, "b\nc", tailLines("a\r\nb\nc\r\n", 2), "a log's own trailing newlines are not lines")
	assert.Equal(t, "b\nc", tailLines("a\r\nb\r\nc\r\n", 2), "a CRLF log loses every line's \\r, not only the last")
	assert.Equal(t, "a", tailLines("a", 5))
	assert.Equal(t, "", tailLines("", 5))
}

func TestClosePR_PassesTheNumberAndTheComment(t *testing.T) {
	ex := &watchExec{fn: answer("")}
	require.NoError(t, ClosePR(context.Background(), "/repo", 101, "Closing: the issue is closed.", ex))
	require.Len(t, ex.calls, 1)
	assert.Equal(t, "gh pr close 101 --comment Closing: the issue is closed.", ex.calls[0])
	assert.Equal(t, "/repo", ex.dirs[0])
}

func TestClosePR_WithoutAComment(t *testing.T) {
	ex := &watchExec{fn: answer("")}
	require.NoError(t, ClosePR(context.Background(), "/repo", 101, "", ex))
	assert.Equal(t, []string{"gh pr close 101"}, ex.calls, "an empty comment is no --comment flag")
}

func TestClosePR_ReturnsTheFailure(t *testing.T) {
	err := ClosePR(context.Background(), "/repo", 101, "c", &watchExec{fn: func(string) (string, error) {
		return "", assert.AnError
	}})
	assert.ErrorIs(t, err, assert.AnError)
}

func TestCommentIssue_PassesTheBody(t *testing.T) {
	ex := &watchExec{fn: answer("")}
	require.NoError(t, CommentIssue(context.Background(), "/repo", 7, "stuck: out of nudges", ex))
	require.Len(t, ex.calls, 1)
	assert.Equal(t, "gh issue comment 7 --body stuck: out of nudges", ex.calls[0])
	assert.Equal(t, "/repo", ex.dirs[0])

	err := CommentIssue(context.Background(), "/repo", 7, "b", &watchExec{fn: func(string) (string, error) {
		return "", assert.AnError
	}})
	assert.ErrorIs(t, err, assert.AnError)
}

// The answers of `gh label list --json name` as probed live: every key
// of a label object, of which AddLabel reads only name. Both lists hold
// near misses of "stuck" (a longer name, one that contains it), so that
// only an exact comparison tells them from the label.
const (
	labelListWithStuck = `[
  {"color": "ededed", "description": "", "id": "LA_kwDOA9", "name": "stuck-old"},
  {"color": "b60205", "description": "Needs a human", "id": "LA_kwDOA8", "name": "stuck"},
  {"color": "0e8a16", "description": "", "id": "LA_kwDOA7", "name": "unstuck"}
]`
	labelListWithoutStuck = `[
  {"color": "ededed", "description": "", "id": "LA_kwDOA9", "name": "stuck-old"},
  {"color": "0e8a16", "description": "", "id": "LA_kwDOA7", "name": "unstuck"}
]`
)

// labelExec answers gh label list with list and listErr, gh label
// create with createErr and gh issue edit with editErr.
func labelExec(list string, listErr, createErr, editErr error) *watchExec {
	return &watchExec{fn: func(argv string) (string, error) {
		switch {
		case strings.HasPrefix(argv, "gh label list"):
			return list, listErr
		case strings.HasPrefix(argv, "gh label create"):
			return "", createErr
		}
		return "", editErr
	}}
}

func TestAddLabel_AddsAnExistingLabelWithoutCreatingIt(t *testing.T) {
	ex := labelExec(labelListWithStuck, nil, nil, nil)
	require.NoError(t, AddLabel(context.Background(), "/repo", 7, "stuck", ex))
	assert.Equal(t, []string{
		"gh label list --json name --limit 1000",
		"gh issue edit 7 --add-label stuck",
	}, ex.calls)
	for _, d := range ex.dirs {
		assert.Equal(t, "/repo", d)
	}
}

func TestAddLabel_CreatesAMissingLabelThenAddsIt(t *testing.T) {
	ex := labelExec(labelListWithoutStuck, nil, nil, nil)
	require.NoError(t, AddLabel(context.Background(), "/repo", 7, "stuck", ex))
	assert.Equal(t, []string{
		"gh label list --json name --limit 1000",
		"gh label create stuck",
		"gh issue edit 7 --add-label stuck",
	}, ex.calls, "no --force: a label made in the meantime keeps its colour")
	for _, d := range ex.dirs {
		assert.Equal(t, "/repo", d)
	}
}

func TestAddLabel_AnEmptyRepositoryHasNoLabelToMatch(t *testing.T) {
	ex := labelExec(`[]`, nil, nil, nil)
	require.NoError(t, AddLabel(context.Background(), "/repo", 7, "stuck", ex))
	assert.Equal(t, []string{
		"gh label list --json name --limit 1000",
		"gh label create stuck",
		"gh issue edit 7 --add-label stuck",
	}, ex.calls)
}

func TestAddLabel_AFailedListChangesNothing(t *testing.T) {
	ex := labelExec("", assert.AnError, nil, nil)
	err := AddLabel(context.Background(), "/repo", 7, "stuck", ex)
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, []string{"gh label list --json name --limit 1000"}, ex.calls,
		"no create and no add after a failed list")

	ex = labelExec("not json", nil, nil, nil)
	require.Error(t, AddLabel(context.Background(), "/repo", 7, "stuck", ex))
	assert.Len(t, ex.calls, 1, "an unparsable list is no list: no create and no add")
}

func TestAddLabel_AFailedCreateIsNotAdded(t *testing.T) {
	ex := labelExec(labelListWithoutStuck, nil, assert.AnError, nil)
	err := AddLabel(context.Background(), "/repo", 7, "stuck", ex)
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, []string{
		"gh label list --json name --limit 1000",
		"gh label create stuck",
	}, ex.calls, "no add after a failed create")
}

func TestAddLabel_AFailedAddIsAnError(t *testing.T) {
	ex := labelExec(labelListWithStuck, nil, nil, assert.AnError)
	err := AddLabel(context.Background(), "/repo", 7, "stuck", ex)
	require.ErrorIs(t, err, assert.AnError)
	assert.Len(t, ex.calls, 2, "one list, one add, no retry")
}
