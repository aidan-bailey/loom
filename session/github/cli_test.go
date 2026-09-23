package github

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedExec answers each gh invocation by matching a substring of
// the joined argv. Unmatched commands fail, which keeps a test honest
// about exactly which commands it expects.
type scriptedExec struct {
	answers map[string]string // argv substring -> stdout
	errs    map[string]error
	calls   []string
}

func (s *scriptedExec) find(c *exec.Cmd) (string, error, bool) {
	argv := strings.Join(c.Args, " ")
	s.calls = append(s.calls, argv)
	for k, v := range s.answers {
		if strings.Contains(argv, k) {
			return v, s.errs[k], true
		}
	}
	for k, e := range s.errs {
		if strings.Contains(argv, k) {
			return "", e, true
		}
	}
	return "", errors.New("unexpected command: " + argv), false
}
func (s *scriptedExec) Run(c *exec.Cmd) error { _, err, _ := s.find(c); return err }
func (s *scriptedExec) Output(c *exec.Cmd) ([]byte, error) {
	out, err, _ := s.find(c)
	return []byte(out), err
}
func (s *scriptedExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return s.Output(c) }

const prListJSON = `[
 {"number":45,"headRefName":"u/b","state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"}]},
 {"number":40,"headRefName":"u/b","state":"CLOSED","isDraft":false,"reviewDecision":"","statusCheckRollup":[]},
 {"number":46,"headRefName":"u/c","state":"OPEN","isDraft":true,"reviewDecision":"","statusCheckRollup":[{"status":"IN_PROGRESS"}]}
]`

const issueListJSON = `[
 {"number":12,"title":"Fix it","state":"OPEN","url":"https://x/12","labels":[{"name":"bug"}]},
 {"number":13,"title":"Other","state":"OPEN","url":"https://x/13","labels":[]}
]`

func TestQuery_ParsesPRsAndIssues(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{
		"pr list":    prListJSON,
		"issue list": issueListJSON,
	}}
	snap, err := Query(context.Background(), "/repo", nil, ex)
	require.NoError(t, err)

	pr := snap.PRs["u/b"]
	assert.Equal(t, 45, pr.Number, "the open PR outranks the closed one on the same branch")
	assert.Equal(t, PROpen, pr.State)
	assert.Equal(t, ReviewApproved, pr.Review)
	assert.Equal(t, ChecksPassing, pr.Checks)
	assert.Equal(t, PRDraft, snap.PRs["u/c"].State)
	assert.Equal(t, ChecksPending, snap.PRs["u/c"].Checks)

	assert.Equal(t, "Fix it", snap.Issues[12].Title)
	assert.Equal(t, []string{"bug"}, snap.Issues[12].Labels)
	assert.False(t, snap.Issues[12].Closed)
	assert.False(t, snap.FetchedAt.IsZero())
}

func TestQuery_RunsInRepoDir(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{"pr list": `[]`, "issue list": `[]`}}
	_, err := Query(context.Background(), "/repo", nil, ex)
	require.NoError(t, err)
	// Every gh call is executed with Dir set (checked through argv
	// having no -R flag: gh infers the repo from cwd).
	for _, c := range ex.calls {
		assert.NotContains(t, c, " -R ")
	}
}

// The gh flags are invisible to scriptedExec's substring matching: the
// canned fixture comes back whatever flags were passed. --state all is
// what makes merged and closed PRs visible at all, and the --json field
// lists are what populate the folded values, so pin them here — a
// dropped flag must fail a test rather than silently empty the UI.
func TestQuery_PassesLoadBearingFlags(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{"pr list": `[]`, "issue list": `[]`}}
	_, err := Query(context.Background(), "/repo", nil, ex)
	require.NoError(t, err)
	require.Len(t, ex.calls, 2)
	assert.Contains(t, ex.calls[0], "pr list --state all --limit 200")
	assert.Contains(t, ex.calls[0], "--json number,headRefName,state,isDraft,reviewDecision,statusCheckRollup")
	assert.Contains(t, ex.calls[1], "issue list --state open --limit 200")
	assert.Contains(t, ex.calls[1], "--json number,title,state,url,labels")
}

func TestQuery_BackfillsClosedLinkedIssues(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{
		"pr list":       `[]`,
		"issue list":    issueListJSON,
		"issue view 99": `{"number":99,"title":"Done","state":"CLOSED","url":"https://x/99","labels":[]}`,
	}}
	snap, err := Query(context.Background(), "/repo", []int{12, 99}, ex)
	require.NoError(t, err)
	assert.True(t, snap.Issues[99].Closed)
	assert.Equal(t, "Done", snap.Issues[99].Title)
	// #12 was in the open list, so no view call for it.
	for _, c := range ex.calls {
		assert.NotContains(t, c, "issue view 12")
	}
}

func TestQuery_BackfillFailureIsNotFatal(t *testing.T) {
	ex := &scriptedExec{
		answers: map[string]string{"pr list": `[]`, "issue list": `[]`},
		errs:    map[string]error{"issue view 7": errors.New("not found")},
	}
	snap, err := Query(context.Background(), "/repo", []int{7}, ex)
	require.NoError(t, err)
	_, ok := snap.Issues[7]
	assert.False(t, ok)
}

func TestQuery_PRListErrorIsFatal(t *testing.T) {
	ex := &scriptedExec{
		answers: map[string]string{"issue list": `[]`},
		errs:    map[string]error{"pr list": errors.New("boom")},
	}
	_, err := Query(context.Background(), "/repo", nil, ex)
	assert.Error(t, err)
}

func TestQuery_BadJSONIsFatal(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{"pr list": `nope`, "issue list": `[]`}}
	_, err := Query(context.Background(), "/repo", nil, ex)
	assert.Error(t, err)
}

func TestView_ParsesBody(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{
		"issue view 12": `{"number":12,"title":"Fix it","body":"Steps:\n1. x","state":"OPEN","url":"https://x/12","labels":[{"name":"bug"},{"name":"p1"}]}`,
	}}
	is, err := View(context.Background(), "/repo", 12, ex)
	require.NoError(t, err)
	assert.Equal(t, "Steps:\n1. x", is.Body)
	assert.Equal(t, []string{"bug", "p1"}, is.Labels)
	assert.Equal(t, "https://x/12", is.URL)
	require.Len(t, ex.calls, 1)
	assert.Contains(t, ex.calls[0], "issue view 12 --json number,title,body,state,url,labels")
}

func TestCheckCLI_AuthFailure(t *testing.T) {
	// CheckCLI looks gh up on PATH before it reaches the injected runner.
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not installed")
	}
	ex := &scriptedExec{errs: map[string]error{"auth status": errors.New("not logged in")}}
	err := CheckCLI(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gh auth login")
}

func TestCheckCLI_OK(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not installed")
	}
	ex := &scriptedExec{answers: map[string]string{"auth status": ""}}
	assert.NoError(t, CheckCLI(ex))
}
