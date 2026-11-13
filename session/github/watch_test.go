package github

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

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
