package core

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeliverStart_FailureRemovesSavesAndKills pins the failed start's
// order: the instance leaves its owner by identity, the start's error is
// the last notice (the error bar keeps it), the TUI repoints its panes,
// and the kill runs as a job.
func TestDeliverStart_FailureRemovesSavesAndKills(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws, storedWorkspace(t, "b")})

	boom := errors.New("boom")
	m.Deliver(StartResult{Instance: inst, Owner: ws, Err: boom})

	out := m.Drain()
	assert.False(t, ws.Holds(inst))
	require.NotEmpty(t, out.Events)
	assert.Equal(t, Notice{Err: boom}, out.Events[len(out.Events)-2])
	assert.Equal(t, InstancesChanged{}, out.Events[len(out.Events)-1])
	assert.Len(t, out.Jobs, 1, "the failed instance is killed off the model's goroutine")
}

// TestDeliverStart_SuccessSendsThePromptByJob: the N flow's prompt no
// longer blocks the caller; it is cleared at once (a later save never
// re-sends it) and typed by a job.
func TestDeliverStart_SuccessSendsThePromptByJob(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	inst.SetPrompt("do the thing")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Deliver(StartResult{Instance: inst, Owner: ws})

	out := m.Drain()
	assert.Empty(t, inst.Prompt())
	assert.Len(t, out.Jobs, 1)
	assert.Equal(t, []Event{Started{Instance: inst, Owner: ws, Loaded: true}}, out.Events)
}

// TestDeliverOpFailed_Reverts pins a failed resume's revert (Loading back
// to Paused) and its events.
func TestDeliverOpFailed_Reverts(t *testing.T) {
	m := NewForTest(Options{})
	inst := pausedInst(t, "x")
	require.NoError(t, inst.TransitionTo(session.Loading))
	err := errors.New("no")
	m.Deliver(OpFailed{Instance: inst, Title: "x", Op: "resume", Previous: session.Paused, Err: err})
	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.Equal(t, []Event{Reactivated{Instance: inst}, Notice{Err: err}, InstancesChanged{}, ClientsStale{}}, m.Drain().Events)
}

func TestDropUnstarted_RemovesAndKillsOnlyAnUnstartedInstance(t *testing.T) {
	m := NewForTest(Options{})
	ws := NewWorkspace(WorkspaceParts{})
	pending, live := newInst(t, "pending"), pausedInst(t, "live")
	ws.Add(pending)
	ws.Add(live)
	m.SetWorkspacesForTest(ws, nil)

	assert.NotNil(t, m.DropUnstarted(pending), "an unstarted instance is killed by a job")
	assert.False(t, ws.Holds(pending))
	assert.Nil(t, m.DropUnstarted(live), "a cancel never kills a started session")
	assert.True(t, ws.Holds(live))
	assert.Nil(t, m.DropUnstarted(nil))
}

// TestResumeOutcome: a resume that returns only a session.Notice succeeded
// with something to report; anything else that is an error failed.
func TestResumeOutcome(t *testing.T) {
	inst := &session.Instance{Title: "r"}
	owner := &Workspace{}

	done, ok := resumeOutcome(inst, "r", owner, nil).(ResumeResult)
	require.True(t, ok)
	assert.Nil(t, done.Notice)
	assert.Same(t, owner, done.Owner)

	n := session.NewNotice(errors.New("forgot stash abc"))
	done, ok = resumeOutcome(inst, "r", owner, n).(ResumeResult)
	require.True(t, ok, "a notice alone is a successful resume")
	assert.Equal(t, n, done.Notice)

	for _, err := range []error{
		errors.New("boom"),
		errors.Join(errors.New("boom"), session.NewNotice(errors.New("forgot stash abc"))),
	} {
		failed, ok := resumeOutcome(inst, "r", owner, err).(OpFailed)
		require.True(t, ok, "%v is a failure", err)
		assert.Equal(t, err, failed.Err, "the failure carries its notices to the error bar")
		assert.Equal(t, session.Paused, failed.Previous)
	}
}

// TestStartOwner_ResolvesByIdentity: the start is stamped with the
// workspace that holds the instance, not whichever one the TUI shows at
// confirm time.
func TestStartOwner_ResolvesByIdentity(t *testing.T) {
	m := NewForTest(Options{})
	focused, peer := storedWorkspace(t, "afocus"), storedWorkspace(t, "bpeer")
	m.SetWorkspacesForTest(nil, []*Workspace{focused, peer})
	inst := newInst(t, "in-peer")
	require.NoError(t, inst.TransitionTo(session.Loading))
	peer.Add(inst)
	assert.Same(t, peer, m.startOwner(inst, focused))

	loose, err := session.NewInstance(session.InstanceOptions{Title: "loose", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	assert.Same(t, focused, m.startOwner(loose, focused), "an instance no workspace holds falls back to the one the TUI shows")
}
