package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/require"
)

// setupPtmxDeadFixture builds a Running instance whose tmux session is alive
// (TmuxAlive true) but was constructed without ever calling Restore, leaving
// ptmx nil — the exact "session healthy, Loom's attach client gone" state
// produced by a failed reattach after full-screen attach.
func setupPtmxDeadFixture(t *testing.T) (*home, *session.Instance) {
	t.Helper()
	m := newTestHome(t)

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = m.list.AddInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Running))

	ts := tmux.NewTmuxSessionWithDeps("a", "claude", fakePtyFactory{t: t}, aliveCmdExecForTest())
	inst.SetTmuxSession(ts)
	require.False(t, inst.PtmxAlive(), "fixture precondition: ptmx must start dead")
	require.True(t, inst.TmuxAlive(), "fixture precondition: tmux session must read alive")

	return m, inst
}

// TestMetadataReadyMsg_RepairsDeadPtmx is the regression guard for the
// "PTY is not available" bug: a session that TmuxAlive reports as healthy
// but whose ptmx is nil was never retried by anything, forever. The
// metadata tick must now notice ptmxAlive=false and call RepairPtmx.
func TestMetadataReadyMsg_RepairsDeadPtmx(t *testing.T) {
	m, inst := setupPtmxDeadFixture(t)

	_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
		{instance: inst, tmuxAlive: true, ptmxAlive: false},
	}})

	require.True(t, inst.PtmxAlive(), "metadata tick should have repaired the dead ptmx")
}

// TestMetadataReadyMsg_SkipsRepairDuringFullScreenAttach guards against a
// race with an in-progress full-screen attach: PausePreview legitimately
// nils ptmx for the duration of tea.ExecProcess, and RepairPtmx racing that
// window would fight the foreground attach over the same tmux session.
func TestMetadataReadyMsg_SkipsRepairDuringFullScreenAttach(t *testing.T) {
	m, inst := setupPtmxDeadFixture(t)
	m.attachingInstance = inst

	_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
		{instance: inst, tmuxAlive: true, ptmxAlive: false},
	}})

	require.False(t, inst.PtmxAlive(), "repair must not run for the instance currently mid full-screen attach")
}
