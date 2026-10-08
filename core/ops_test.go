package core

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
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
	ws.add(inst)
	m.SetWorkspacesForTest(ws, storedWorkspace(t, "b"))

	boom := errors.New("boom")
	m.Deliver(StartResult{Instance: inst, Owner: ws, Err: boom})

	out := m.Drain()
	assert.False(t, ws.holds(inst))
	require.NotEmpty(t, out.Events)
	assert.Equal(t, Notice{Err: boom}, out.Events[len(out.Events)-2])
	assert.Equal(t, InstancesChanged{}, out.Events[len(out.Events)-1])
	assert.Len(t, out.Jobs, 1, "the failed instance is killed off the model's goroutine")
}

// TestDeliverStart_SuccessSendsThePromptByJob: the N flow's prompt no
// longer blocks the caller; it is cleared at once (a later save never
// re-sends it) and typed by a job. The TUI hears of the start (Started)
// only once that job has sent it, as when the completion sent it inline
// before attaching: a key typed into the attached pane must not land
// ahead of the prompt.
func TestDeliverStart_SuccessSendsThePromptByJob(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	inst.SetPrompt("do the thing")
	ws.add(inst)
	m.SetWorkspacesForTest(ws, storedWorkspace(t, "b"))

	m.Deliver(StartResult{Instance: inst, Owner: ws})

	out := m.Drain()
	assert.Empty(t, inst.Prompt())
	require.Len(t, out.Jobs, 1)
	assert.Empty(t, out.Events, "no Started until the prompt is sent")

	// The fixture never started, so the send fails; the job logs that and
	// reports the start finished all the same.
	m.Deliver(out.Jobs[0]())
	assert.Equal(t, []Event{Started{ID: m.idOf(inst), Title: "x", Owner: m.wsIDOf(ws), OwnerLabel: "a"}},
		m.Drain().Events)
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
	assert.Equal(t, []Event{Reactivated{ID: m.idOf(inst)}, Notice{Err: err}, InstancesChanged{}, ClientsStale{}}, m.Drain().Events)
}

// TestKillUnstarted_KillsTheFailedStartOnly: a failed start leaves its
// owner by identity and its unstarted instance is killed by a job
// (killUnstarted); nothing else is touched. It replaces DropUnstarted's
// test: a creation flow's cancel has no instance to kill any more (drafts).
func TestKillUnstarted_KillsTheFailedStartOnly(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	pending, live := newInst(t, "pending"), pausedInst(t, "live")
	ws.add(pending)
	ws.add(live)
	m.SetWorkspacesForTest(ws)

	assert.NotNil(t, killUnstarted(pending), "an unstarted instance is killed by a job")
	m.Deliver(StartResult{Instance: pending, Owner: ws, Err: errors.New("boom")})
	assert.False(t, ws.holds(pending))
	assert.Len(t, m.Drain().Jobs, 1, "the failed start's kill, and no other")
	assert.True(t, ws.holds(live))
	assert.Nil(t, killUnstarted(nil))
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
	m.SetWorkspacesForTest(focused, peer)
	inst := newInst(t, "in-peer")
	require.NoError(t, inst.TransitionTo(session.Loading))
	peer.add(inst)
	assert.Same(t, peer, m.startOwner(inst, focused))

	loose, err := session.NewInstance(session.InstanceOptions{Title: "loose", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	assert.Same(t, focused, m.startOwner(loose, focused), "an instance no workspace holds falls back to the one the TUI shows")
}

// terminalKills puts inst on a mock tmux session that records the
// kill-session of its terminal pane's shell (loom_term_<title>), counting
// them in *kills and noting in *worktreeThere whether wtPath existed when
// the last one ran. No tmux server is contacted.
func terminalKills(t *testing.T, inst *session.Instance, wtPath string, kills *int, worktreeThere *bool) {
	t.Helper()
	target := tmux.SessionTarget(tmux.ToLoomTmuxName(tmux.TerminalSessionName(inst.Title)))
	rec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if slices.Contains(c.Args, "kill-session") && slices.Contains(c.Args, target) {
				*kills++
				_, err := os.Stat(wtPath)
				*worktreeThere = err == nil
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	inst.SetTmuxSession(tmux.NewSessionWithDeps(inst.Title, "claude", fakePtyFactory{t: t}, rec))
}

// TestKill_EndsTheTerminalShellAfterTheChecksAndBeforeTheWorktree pins
// where the terminal pane's shell ends in a kill's job, now that the TUI
// takes no step of its own there (its beforeKill, closing the shell, is
// gone): Instance.Kill ends it by name, never for a kill the checks
// refuse, and on a kill that proceeds, once, while the instance's worktree
// is still there.
func TestKill_EndsTheTerminalShellAfterTheChecksAndBeforeTheWorktree(t *testing.T) {
	refused := func(t *testing.T, inst *session.Instance, wantErr string) {
		t.Helper()
		m := NewForTest(Options{})
		ws := storedWorkspace(t, "a")
		ws.add(inst)
		m.SetWorkspacesForTest(ws)
		calls, there := 0, false
		terminalKills(t, inst, inst.GetWorktreePath(), &calls, &there)
		pre, job := m.killInst(ws, inst)
		pre()

		failed, ok := job().(OpFailed)
		require.True(t, ok, "the kill is refused")
		assert.ErrorContains(t, failed.Err, wantErr)
		assert.Zero(t, calls, "the terminal shell must not be ended for a refused kill")
	}

	t.Run("no worktree", func(t *testing.T) {
		// Never started: GetGitWorktree fails.
		refused(t, newInst(t, "unstarted"), "has not been started")
	})

	t.Run("branch checked out in the repository", func(t *testing.T) {
		repo := gitRepo(t)
		inst, err := session.FromInstanceData(session.InstanceData{
			Title: "main-session", Status: session.Paused, Program: "claude",
			Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: t.TempDir(), BranchName: "main", SessionName: "main-session"},
		}, t.TempDir())
		require.NoError(t, err)
		refused(t, inst, "currently checked out")
	})

	t.Run("a kill that proceeds", func(t *testing.T) {
		repo := gitRepo(t)
		inst := pausedWorktreeInst(t, repo, "victim", "victim-branch")
		wtPath := inst.GetWorktreePath()
		require.DirExists(t, wtPath, "fixture: the worktree exists")
		m := NewForTest(Options{})
		ws := storedWorkspace(t, "a")
		ws.add(inst)
		m.SetWorkspacesForTest(ws)

		calls, worktreeThere := 0, false
		terminalKills(t, inst, wtPath, &calls, &worktreeThere)
		pre, job := m.killInst(ws, inst)
		pre()

		_, ok := job().(KillResult)
		require.True(t, ok, "the kill proceeds")
		assert.Equal(t, 1, calls, "the terminal shell is ended once")
		assert.True(t, worktreeThere, "before the worktree is removed")
		assert.NoDirExists(t, wtPath, "and Instance.Kill removed it afterwards")
	})
}

// TestSendPrompt_FailureNamesTheSession: a failed send reaches the user
// after the overlay or bar that took the text has closed, so its notice
// says what was not sent, and to which session.
func TestSendPrompt_FailureNamesTheSession(t *testing.T) {
	m := NewForTest(Options{})
	inst := newInst(t, "x") // never started, so the send fails
	m.Deliver(m.sendPromptInst(inst, "hi")())

	events := m.Drain().Events
	require.Len(t, events, 1)
	n, ok := events[0].(Notice)
	require.True(t, ok, "a notice")
	assert.ErrorContains(t, n.Err, "prompt not sent to x: ")
}
