package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sweepTmux is a fake tmux for a reset's sweep: every listing answers
// listing (or listErr), and every kill-session is recorded.
type sweepTmux struct {
	listing string
	listErr error
	killed  []string
}

func (f *sweepTmux) Run(c *exec.Cmd) error {
	if slices.Contains(c.Args, "kill-session") {
		f.killed = append(f.killed, c.Args[len(c.Args)-1])
		return nil
	}
	// has-session: no session of that name.
	return &exec.ExitError{}
}
func (f *sweepTmux) Output(c *exec.Cmd) ([]byte, error)         { return []byte(f.listing), f.listErr }
func (f *sweepTmux) CombinedOutput(c *exec.Cmd) ([]byte, error) { return f.Output(c) }

// resetFixture is an opened workspace over a real repository, holding a
// Paused session x saved to its records, with a worktree on branch u/x in
// its worktrees dir, where loom puts it (worktrees/u/x_<stamp>: the branch
// prefix is a directory), on a model whose tmux is tmux: x's session runs
// in that worktree, and another session elsewhere.
type resetFixture struct {
	m        *Model
	ws       *Workspace
	repo, wt string
	tmux     *sweepTmux
}

func newResetFixture(t *testing.T) resetFixture {
	t.Helper()
	isolateTmux(t) // the terminal a reset's completion starts
	repo := gitRepo(t)
	def := config.Workspace{Name: "reset-r", Path: repo}
	cfgDir := config.WorkspaceConfigDir(&def)
	wt := filepath.Join(cfgDir, "worktrees", "u", "x_18d7")
	runGit(t, repo, "worktree", "add", "-q", "-b", "u/x", wt)
	killSessionAtEnd(t, def.Name)

	state := config.LoadStateFrom(cfgDir)
	storage, err := session.NewStorage(state, cfgDir)
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	cfg.DefaultProgram = fakeClaude(t)
	ws := NewWorkspace(WorkspaceParts{Ctx: config.WorkspaceContextFor(&def), Storage: storage, Config: cfg, State: state})
	ws.add(pausedInst(t, "x"))
	require.NoError(t, storage.SaveInstances(Persistable(ws.insts)))

	fake := &sweepTmux{listing: "loom_x\t" + wt + "\nloom_elsewhere\t" + t.TempDir() + "\n"}
	m := NewForTest(Options{Registry: &config.WorkspaceRegistry{Workspaces: []config.Workspace{def}}, CmdExec: fake})
	m.SetWorkspacesForTest(ws)
	m.Drain()
	return resetFixture{m: m, ws: ws, repo: repo, wt: wt, tmux: fake}
}

// runJobs runs every job m queued and delivers each result, returning the
// events they produced.
func runJobs(t *testing.T, m *Model) []Event {
	t.Helper()
	out := m.Drain()
	for _, job := range out.Jobs {
		m.Deliver(job())
	}
	return append(out.Events, m.Drain().Events...)
}

func replyTo(events []Event, req ReqID) *Reply {
	for _, e := range events {
		if r, ok := e.(Reply); ok && r.Req == req {
			return &r
		}
	}
	return nil
}

// A reset empties the served workspace at once, then its job deletes its
// records, kills only the sessions started in its roots and removes its
// worktrees and their branches; the Reply comes when it lands, and the
// opened workspace gets a fresh terminal. Nothing joins it meanwhile.
func TestReset_EmptiesTheWorkspaceAndSweepsOnlyItsSessions(t *testing.T) {
	f := newResetFixture(t)
	id := f.m.wsIDOf(f.ws)

	require.NoError(t, f.m.Reset(id, 7))

	assert.Empty(t, f.m.Views(id), "emptied at once")
	f.m.Create(id, NewInstance{Title: "y", Path: f.repo, Program: "true"}, 8)
	created := replyTo(f.m.out.Events, 8)
	require.NotNil(t, created)
	assert.ErrorIs(t, created.Err, ErrRefused, "nothing is created while the reset runs")
	assert.Nil(t, f.ws.terminal(), "nor a terminal")

	events := runJobs(t, f.m)

	reply := replyTo(events, 7)
	require.NotNil(t, reply, "the Reply arrives with the job")
	assert.NoError(t, reply.Err)
	assert.Equal(t, []string{"=loom_x"}, f.tmux.killed, "only its own session")
	assert.NoDirExists(t, f.wt)
	assert.False(t, f.hasBranch(t), "its branch is gone")
	term := f.ws.terminal()
	require.NotNil(t, term, "the opened workspace gets a fresh terminal")
	assert.True(t, term.Started())
	for _, rec := range savedRecords(t, f.ws.ctx.ConfigDir) {
		assert.NotEqual(t, "x", rec.Title, "its records are deleted")
	}
	assert.False(t, f.ws.resetting)
}

// The resets refused, changing nothing: an unknown workspace, a busy
// session, a reset already running, and a workspace whose load failed,
// whose records loom can't read.
func TestReset_Refusals(t *testing.T) {
	f := newResetFixture(t)
	id := f.m.wsIDOf(f.ws)
	x := f.ws.byTitle("x")

	assert.ErrorIs(t, f.m.Reset(99, 1), ErrRefused, "an unknown workspace")

	require.NoError(t, x.TransitionTo(session.Loading))
	err := f.m.Reset(id, 1)
	assert.ErrorIs(t, err, ErrRefused, "a busy session")
	assert.ErrorContains(t, err, "x is busy")
	require.NoError(t, x.TransitionTo(session.Paused))

	f.ws.resetting = true
	assert.ErrorIs(t, f.m.Reset(id, 1), ErrRefused, "a reset running")
	f.ws.resetting = false

	f.ws.loadErr = errors.New("unreadable")
	assert.ErrorIs(t, f.m.Reset(id, 1), ErrRefused, "a failed load")
	f.ws.loadErr = nil

	assert.Same(t, x, f.ws.byTitle("x"), "nothing changed")
	assert.Empty(t, f.m.Drain().Jobs, "and nothing ran")
	assert.DirExists(t, f.wt)
}

// A refused reset is also the Reply to its request, at once, as every
// request's refusal is (refuse): a client waiting on the Reply learns of
// it.
func TestReset_ARefusalIsTheReply(t *testing.T) {
	f := newResetFixture(t)

	err := f.m.Reset(99, 4)

	require.ErrorIs(t, err, ErrRefused)
	reply := replyTo(f.m.Drain().Events, 4)
	require.NotNil(t, reply, "the refusal is the Reply")
	assert.ErrorIs(t, reply.Err, ErrRefused)
	assert.Equal(t, err.Error(), reply.Err.Error())
}

// A sweep that fails stops the reset before the worktrees, which an agent
// may still be running in: the Reply carries the error, and the model shows
// it.
func TestReset_AFailedSweepIsTheReplysError(t *testing.T) {
	f := newResetFixture(t)
	f.tmux.listErr = errors.New("signal: killed")

	require.NoError(t, f.m.Reset(f.m.wsIDOf(f.ws), 7))
	events := runJobs(t, f.m)

	reply := replyTo(events, 7)
	require.NotNil(t, reply)
	require.Error(t, reply.Err)
	assert.Contains(t, reply.Err.Error(), "NOT removed")
	assert.DirExists(t, f.wt, "the worktree an agent may still use survives")
	shown := slices.ContainsFunc(events, func(e Event) bool {
		n, ok := e.(Notice)
		return ok && n.Err == reply.Err && n.Req == 7
	})
	assert.True(t, shown, "the model shows it to the client that asked")
}

// A reset whose first step fails deleted nothing: the workspace serves its
// records again, as on disk.
func TestReset_DeletingNothingServesTheRecordsAgain(t *testing.T) {
	f := newResetFixture(t)
	cfgDir := f.ws.ctx.ConfigDir
	require.NoError(t, os.Chmod(cfgDir, 0o500)) // state.json can't be replaced
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o755) })

	require.NoError(t, f.m.Reset(f.m.wsIDOf(f.ws), 7))
	events := runJobs(t, f.m)
	require.NoError(t, os.Chmod(cfgDir, 0o755))

	reply := replyTo(events, 7)
	require.NotNil(t, reply)
	require.Error(t, reply.Err)
	assert.NotNil(t, f.ws.byTitle("x"), "served again")
	assert.Empty(t, f.tmux.killed, "nothing swept")
}

// The direct path (`loom reset`, no daemon) runs the same steps over the
// workspace's records on disk.
func TestResetWorkspace_RunsEveryStep(t *testing.T) {
	f := newResetFixture(t)

	rep, err := ResetWorkspace(f.ws.ctx, nil, f.tmux)

	require.NoError(t, err)
	assert.Equal(t, ResetReport{Records: true, Swept: true, Sweep: session.SweepResult{Killed: 1, Unowned: 1}, Worktrees: true}, rep)
	assert.Empty(t, savedRecords(t, f.ws.ctx.ConfigDir))
	assert.NoDirExists(t, f.wt)
	assert.False(t, f.hasBranch(t), "the branch of a prefixed worktree is gone")
	assert.NoDirExists(t, filepath.Dir(f.wt), "and its prefix directory")
}

// hasBranch reports whether the fixture's branch u/x still exists.
func (f resetFixture) hasBranch(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", f.repo, "branch", "--list", "u/x").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out)) != ""
}
