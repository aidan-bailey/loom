package core

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// run executes the jobs of out and delivers their results, returning what
// the model produced after them.
func run(m *Model, out Out) Out {
	for _, j := range out.Jobs {
		m.Deliver(j())
	}
	return m.Drain()
}

func replies(out Out) []Reply {
	var rs []Reply
	for _, ev := range out.Events {
		if r, ok := ev.(Reply); ok {
			rs = append(rs, r)
		}
	}
	return rs
}

func TestRequests_AnUnknownIDIsRefused(t *testing.T) {
	m := NewForTest(Options{})
	m.Kill(99, 5)
	assert.Equal(t, []Reply{{Req: 5, ID: 99, Err: ErrNoSession}}, replies(m.Drain()))
	m.Kill(99, 0) // no reply wanted
	assert.Empty(t, replies(m.Drain()))
}

// TestKill_RepliesWhenItFinishes: an unstarted instance has no worktree, so
// the kill job refuses (OpFailed); the Reply comes when its result lands,
// not when the request is made.
func TestKill_RepliesWhenItFinishes(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.idOf(inst)

	m.Kill(id, 7)
	out := m.Drain()
	assert.Empty(t, replies(out), "no reply before the job finishes")
	assert.Equal(t, session.Deleting, inst.GetStatus(), "the pre-step ran")
	require.Len(t, out.Jobs, 1)

	rs := replies(run(m, out))
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(7), rs[0].Req)
	assert.Equal(t, id, rs[0].ID)
	assert.Error(t, rs[0].Err)
}

// TestPause_AFailedPauseRevertsToTheStatusItHad: Pause takes the job (and
// with it the status a failure reverts to) before it moves the session to
// Loading, as the TUI's path does, so a failed pause does not leave the
// session stuck in Loading.
func TestPause_AFailedPauseRevertsToTheStatusItHad(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x") // unstarted: Instance.Pause refuses it
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.idOf(inst)

	m.Pause(id, 6)
	out := m.Drain()
	assert.Equal(t, session.Loading, inst.GetStatus(), "the spinner shows at once")
	assert.Empty(t, replies(out))
	require.Len(t, out.Jobs, 1)

	rs := replies(run(m, out))
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(6), rs[0].Req)
	assert.Error(t, rs[0].Err)
	assert.Equal(t, session.Ready, inst.GetStatus(), "reverted to the status it had, not to Loading")
}

// TestResume_ShowsTheSpinnerAndRepliesOnlyWhenItFinishes: Resume moves the
// session to Loading at once and queues the resume; the Reply waits for it.
func TestResume_ShowsTheSpinnerAndRepliesOnlyWhenItFinishes(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Resume(m.idOf(inst), 8)
	out := m.Drain()
	assert.Equal(t, session.Loading, inst.GetStatus())
	assert.Len(t, out.Jobs, 1)
	assert.Empty(t, replies(out), "no reply before the resume finishes")
}

// TestRecover_RepliesWithTheAdoptedInstance: a Recover's Reply names the
// instance that replaced the placeholder, not the placeholder.
func TestRecover_RepliesWithTheAdoptedInstance(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	placeholder, adopted := pausedInst(t, "x"), pausedInst(t, "x")
	ws.Add(placeholder)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	pid := m.idOf(placeholder)

	m.spawn(m.track(4, pid, func() any {
		return RecoverResult{Placeholder: placeholder, Owner: ws, OldTitle: "x", Recovered: adopted}
	}))
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(4), rs[0].Req)
	assert.NoError(t, rs[0].Err)
	assert.Equal(t, m.idOf(adopted), rs[0].ID)
	assert.NotEqual(t, pid, rs[0].ID)
	assert.Same(t, adopted, ws.Instances()[0], "the adoption was applied before the reply")
}

func TestCreate_RepliesWithTheNewIDAndStarts(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Create(ws, NewInstance{Title: "new", Path: t.TempDir(), Program: "claude", Prompt: "hi", Start: true,
		Launch: launch.Options{BranchPrefix: "me/"}}, 3)
	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	require.NotZero(t, rs[0].ID)
	v, ok := m.View(rs[0].ID)
	require.True(t, ok)
	assert.Equal(t, "new", v.Title)
	assert.Equal(t, session.Loading, v.Status)
	assert.Len(t, out.Jobs, 1, "the start job")
}

func TestCreate_WithoutStartLeavesItUnstarted(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Create(ws, NewInstance{Title: "s", Path: t.TempDir(), Program: "claude"}, 4)
	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	v, _ := m.View(rs[0].ID)
	assert.False(t, v.Started)
	assert.Equal(t, session.Ready, v.Status)
	assert.Empty(t, out.Jobs)
}

func TestCreate_AWorkspaceNoLongerLoadedIsRefused(t *testing.T) {
	m := NewForTest(Options{})
	ws, closed := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Create(closed, NewInstance{Title: "s", Path: t.TempDir(), Program: "claude", Start: true}, 2)
	out := m.Drain()
	assert.Empty(t, out.Jobs)
	assert.Empty(t, closed.Instances())
	rs := replies(out)
	require.Len(t, rs, 1)
	assert.Zero(t, rs[0].ID)
	assert.ErrorContains(t, rs[0].Err, "no longer open")
}

func TestResumeWith_AppliesTheLaunchOptions(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.ResumeWith(m.idOf(inst), launch.Options{Model: "sonnet", Account: "default"}, "claude", 0)
	assert.Contains(t, inst.Program(), "sonnet")
	assert.Equal(t, session.Loading, inst.GetStatus())
	assert.Len(t, m.Drain().Jobs, 1)
}

func TestFetchIssue_RepliesWithTheIssue(t *testing.T) {
	r := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if strings.Contains(c.String(), "issue view") {
				return []byte(`{"number":12,"title":"Fix it","body":"do","url":"u","state":"OPEN","labels":[]}`), nil
			}
			return nil, errors.New("unexpected " + c.String())
		},
	}
	m := NewForTest(Options{})
	m.spawn(m.track(9, 0, fetchIssueJob("/repo", 12, r)))
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	assert.NoError(t, rs[0].Err)
	assert.Equal(t, 12, rs[0].Issue.Number)
	assert.Equal(t, "Fix it", rs[0].Issue.Title)
}

// TestIDTriggers_IgnoreAnUnknownID: the ID versions of the pane and tick
// triggers do nothing for an ID no loaded workspace holds (Tick still
// ticks, with no selection).
func TestIDTriggers_IgnoreAnUnknownID(t *testing.T) {
	m := NewForTest(Options{})
	m.PaneOutput(42)
	m.PaneQuiet(42)
	m.VerifyDead(42)
	assert.True(t, m.Drain().Empty())
	m.Tick(42)
	assert.NotEmpty(t, m.Drain().Jobs, "the probe is queued with no selection")
}
