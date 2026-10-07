package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readyInst builds a Running instance titled "a" on a mock tmux session
// (has-session answers alive; no tmux server contacted), held by m's
// classic workspace (app's addReadyInstance).
func readyInst(t *testing.T, m *Model) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	hold(m, inst)
	require.NoError(t, inst.TransitionTo(session.Running))
	inst.SetTmuxSession(tmux.NewSessionWithDeps("a", "true", fakePtyFactory{t: t}, aliveExec()))
	return inst
}

func TestGHQueryDispatchesOnFirstCall(t *testing.T) {
	m := NewForTest(Options{})
	require.True(t, m.maybeGHQuery())
	assert.True(t, m.gate(gateGH).inFlight)
}

func TestGHQueryThrottledWithinInterval(t *testing.T) {
	m := NewForTest(Options{})
	require.True(t, m.maybeGHQuery())
	m.gate(gateGH).inFlight = false
	assert.False(t, m.maybeGHQuery())
}

func TestGHQueryResumesAfterInterval(t *testing.T) {
	m := NewForTest(Options{})
	require.True(t, m.maybeGHQuery())
	m.gate(gateGH).inFlight = false
	m.gate(gateGH).last = time.Now().Add(-ghInterval - time.Second)
	assert.True(t, m.maybeGHQuery())
}

func TestGHQueryNotStackedWhileInFlight(t *testing.T) {
	m := NewForTest(Options{})
	require.True(t, m.maybeGHQuery())
	m.gate(gateGH).last = time.Now().Add(-ghInterval - time.Second)
	assert.False(t, m.maybeGHQuery())
}

func TestGHQueryDisabledWhenCLIUnavailable(t *testing.T) {
	m := NewForTest(Options{})
	m.ghAvailable = ghAvailability{checked: true, ok: false, reason: "no gh", checkedAt: time.Now()}
	assert.False(t, m.maybeGHQuery(), "a known-unavailable gh must not spawn subprocesses")
	assert.False(t, m.gate(gateGH).inFlight)
}

func TestGHQueryRechecksAfterBackoff(t *testing.T) {
	m := NewForTest(Options{})
	m.ghAvailable = ghAvailability{checked: true, ok: false, reason: "no gh", checkedAt: time.Now().Add(-ghRecheckInterval - time.Second)}
	assert.True(t, m.maybeGHQuery(), "an unavailable gh is re-probed after the backoff, not disabled forever")
}

func TestGHResultClearsInFlightAndReplacesWholesale(t *testing.T) {
	m := NewForTest(Options{})
	m.gate(gateGH).inFlight = true
	m.ghState = map[string]github.Snapshot{"/old": {}}

	m.Deliver(gatedResult{kind: gateGH, result: ghResult{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{"/repo": {PRs: map[string]github.PR{}}},
		errs:      map[string]error{"/old": errors.New("boom")},
	}})
	assert.False(t, m.gate(gateGH).inFlight)
	_, hasOld := m.ghState["/old"]
	assert.False(t, hasOld, "an errored repo is dropped, not retained")
	_, hasNew := m.ghState["/repo"]
	assert.True(t, hasNew)
}

func TestGHResultJoinsOntoInstances(t *testing.T) {
	m := NewForTest(Options{})
	inst := readyInst(t, m)
	inst.Branch = "u/b"
	inst.SetIssue(12)
	repo := inst.Path

	m.Deliver(ghResult{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{repo: {
			PRs:    map[string]github.PR{"u/b": {Number: 45, State: github.PROpen}},
			Issues: map[int]github.Issue{12: {Number: 12, Title: "Fix"}},
		}},
	})
	s := inst.GitHubState()
	assert.True(t, s.Known)
	assert.Equal(t, 45, s.PRNumber)
	assert.Equal(t, "Fix", s.IssueTitle)
}

func TestGHResultUnknownRepoLeavesStateUnknown(t *testing.T) {
	m := NewForTest(Options{})
	inst := readyInst(t, m)
	m.Deliver(ghResult{available: ghAvailability{checked: true, ok: true}, snapshots: map[string]github.Snapshot{}})
	assert.False(t, inst.GitHubState().Known)
}

func TestExpediteGitHubZeroesWindow(t *testing.T) {
	m := NewForTest(Options{})
	m.gate(gateGH).last = time.Now()
	m.ExpediteGitHub()
	assert.True(t, m.gateDue(gateGH, time.Now()), "a refresh makes the next tick poll")
}

func TestLinkedIssuesCollectsNonZero(t *testing.T) {
	m := NewForTest(Options{})
	a := readyInst(t, m)
	a.SetIssue(3)
	b, err := session.NewInstance(session.InstanceOptions{Title: "b", Path: a.Path, Program: "claude"})
	require.NoError(t, err)
	hold(m, b)
	assert.Equal(t, []int{3}, m.linkedIssues(a.Path))
}

// ghFakeCall records one subprocess invocation for assertions. repo is
// the target directory regardless of how the real command expresses
// it: git passes the repo via a "-C <path>" argv pair (no Cmd.Dir),
// while gh sets Cmd.Dir directly and carries no repo path in argv —
// ghRepoOfCmd normalizes both to the same key.
type ghFakeCall struct {
	repo string
	argv string
}

// ghFakeAnswer is one scripted response, matched by an argv substring
// within its repo's bucket, tried in order.
type ghFakeAnswer struct {
	substr string
	out    string
	err    error
}

// ghFakeExec answers git/gh invocations from a per-repo answer list
// and fails loudly on anything unscripted, so this test cannot pass
// vacuously on a command it never meant to exercise.
type ghFakeExec struct {
	answers map[string][]ghFakeAnswer
	calls   []ghFakeCall
}

func ghRepoOfCmd(c *exec.Cmd) string {
	if c.Dir != "" {
		return c.Dir
	}
	for i, a := range c.Args {
		if a == "-C" && i+1 < len(c.Args) {
			return c.Args[i+1]
		}
	}
	return ""
}

func (f *ghFakeExec) find(c *exec.Cmd) (string, error) {
	repo := ghRepoOfCmd(c)
	argv := strings.Join(c.Args, " ")
	f.calls = append(f.calls, ghFakeCall{repo: repo, argv: argv})
	for _, a := range f.answers[repo] {
		if strings.Contains(argv, a.substr) {
			return a.out, a.err
		}
	}
	return "", fmt.Errorf("unscripted command for repo %q: %s", repo, argv)
}

func (f *ghFakeExec) Run(c *exec.Cmd) error { _, err := f.find(c); return err }
func (f *ghFakeExec) Output(c *exec.Cmd) ([]byte, error) {
	out, err := f.find(c)
	return []byte(out), err
}
func (f *ghFakeExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return f.Output(c) }

// TestGHPollJobResolvesPerRepoBaseAndBucketsErrors runs ghPollJob's
// closure directly (the earlier tests only ever check it is non-nil).
// It is the regression test for the per-repo BaseBranch bug: repo /a
// has its own configured base ("develop") while /b has none and must
// auto-detect — a shared single string across the batch would resolve
// /b against /a's setting instead. It also pins the wholesale-replace
// fail-closed contract: /b's failing gh query lands it in errs and
// out of snapshots without disturbing /a's successful result.
func TestGHPollJobResolvesPerRepoBaseAndBucketsErrors(t *testing.T) {
	fake := &ghFakeExec{answers: map[string][]ghFakeAnswer{
		"/a": {
			{substr: "rev-parse --verify --quiet refs/heads/develop", out: "shaaaaa1\n"},
			{substr: "pr list", out: "[]"},
			{substr: "issue list", out: "[]"},
		},
		"/b": {
			{substr: "symbolic-ref --short refs/remotes/origin/HEAD", err: errors.New("no origin HEAD")},
			{substr: "rev-parse --verify --quiet refs/heads/main", out: "shabbbbb2\n"},
			{substr: "pr list", err: errors.New("boom")},
		},
	}}

	req := ghPollRequest{
		repos:      []string{"/a", "/b"},
		linked:     map[string][]int{"/a": nil, "/b": nil},
		configured: map[string]string{"/a": "develop", "/b": ""},
	}

	msg, ok := ghPollJob(req, fake)().(ghResult)
	require.True(t, ok)

	// Assertion 1: each repo resolves against ITS OWN configured base
	// branch. /a's git argv must reference its configured "develop";
	// /b (unconfigured) must never see /a's setting leak onto it.
	var aResolvedDevelop, bMentionsDevelop bool
	for _, c := range fake.calls {
		if c.repo == "/a" && strings.Contains(c.argv, "refs/heads/develop") {
			aResolvedDevelop = true
		}
		if c.repo == "/b" && strings.Contains(c.argv, "develop") {
			bMentionsDevelop = true
		}
	}
	assert.True(t, aResolvedDevelop, "/a must resolve against its own configured base branch")
	assert.False(t, bMentionsDevelop, "/b has no configured base branch and must never see /a's leak onto it")
	assert.Equal(t, "develop", msg.bases["/a"])
	assert.Equal(t, "main", msg.bases["/b"], "/b auto-detects since it has no configured base")

	// Assertion 2: a failed gh query lands its repo in errs and out of
	// snapshots, without disturbing a sibling repo's successful result.
	_, aOK := msg.snapshots["/a"]
	assert.True(t, aOK, "/a's successful query must produce a snapshot")
	_, bOK := msg.snapshots["/b"]
	assert.False(t, bOK, "/b's failed query must not leave a snapshot")
	require.Error(t, msg.errs["/b"])
}

func TestBaseFor_ReadsGHBases(t *testing.T) {
	m := NewForTest(Options{})
	m.ghBases = map[string]string{"/r": "origin/main"}
	assert.Equal(t, "origin/main", m.baseFor("/r"))
	assert.Equal(t, "", m.baseFor("/other"))
}

// TestPush_ErrorPathReportsTheError: an unstarted instance has no
// worktree, so the job reports the error; a success instead expedites the
// GitHub poll (deliverPush). This pins that the two outcomes are
// distinguishable.
func TestPush_ErrorPathReportsTheError(t *testing.T) {
	m := NewForTest(Options{})
	inst := readyInst(t, m)
	msg := m.pushInst(inst)()
	r, isResult := msg.(pushResult)
	assert.True(t, isResult && r.err != nil, "no worktree on a test instance: expected an error, got %#v", msg)
}

func TestDeliverPush_SuccessExpeditesGitHubAndAnErrorIsANotice(t *testing.T) {
	m := NewForTest(Options{})
	m.gate(gateGH).last = time.Now()

	m.Deliver(pushResult{})
	assert.True(t, m.gateDue(gateGH, time.Now()), "a push makes the next tick poll")

	m.Deliver(pushResult{err: errors.New("rejected")})
	out := m.Drain()
	require.Len(t, out.Events, 1)
	assert.Equal(t, Notice{Err: errors.New("rejected")}, out.Events[0])
}

// TestOpenTabExpeditesGitHub: a newly opened workspace's repo was not in
// openRepoPaths until now, so OpenTab makes the next tick poll. The load
// runs on a mock executor; its workspace terminal (a fake claude) starts
// on the private tmux server TestMain sets up.
func TestOpenTabExpeditesGitHub(t *testing.T) {
	def := config.Workspace{Name: "gh-expedite", Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(`{"instances":[]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName),
		[]byte(`{"default_program":"`+fakeClaude(t)+`"}`), 0o644))
	t.Cleanup(func() {
		_ = tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(def.Name))).Run()
	})
	m := NewForTest(Options{Registry: &config.WorkspaceRegistry{}, CmdExec: aliveExec()})
	m.gate(gateGH).last = time.Now()
	require.False(t, m.gateDue(gateGH, time.Now()))

	_, err := m.openTabWS(def)
	require.NoError(t, err)

	assert.True(t, m.gateDue(gateGH, time.Now()), "a newly opened workspace's repo is polled on the next tick")
}
