package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shellInst builds a started, Running session titled title whose worktree
// is dir.
func shellInst(t *testing.T, title, dir string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         title, Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: t.TempDir(), WorktreePath: dir, BranchName: "main", SessionName: title},
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	require.True(t, inst.Started(), "fixture precondition")
	require.Equal(t, dir, inst.GetWorktreePath(), "fixture precondition")
	return inst
}

// shellModel serves insts in one stored workspace.
func shellModel(t *testing.T, insts ...*session.Instance) *Model {
	t.Helper()
	ws := storedWorkspace(t, "a")
	for _, inst := range insts {
		ws.add(inst)
	}
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)
	m.Drain()
	return m
}

// shellName is the tmux name of title's worktree shell.
func shellName(title string) string {
	return tmux.ToLoomTmuxName(tmux.TerminalSessionName(title))
}

// shellAlive reports whether title's worktree shell runs on the test's
// private tmux server.
func shellAlive(t *testing.T, title string) bool {
	t.Helper()
	return tmux.Command(context.Background(), "has-session", "-t", tmux.SessionTarget(shellName(title))).Run() == nil
}

// shellOption is the value of a session option of title's shell.
func shellOption(t *testing.T, title, option string) string {
	t.Helper()
	out, err := tmux.Command(context.Background(), "show-options", "-v", "-t", tmux.PaneTarget(shellName(title)), option).Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// TestTerminalShell_IsTheEnvironmentsElseBinSh: the shell is the daemon's
// $SHELL, and /bin/sh when it has none.
func TestTerminalShell_IsTheEnvironmentsElseBinSh(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/zsh")
	assert.Equal(t, "/usr/bin/zsh", terminalShell())
	t.Setenv("SHELL", "")
	assert.Equal(t, "/bin/sh", terminalShell())
}

// A started, active session with a worktree gets its shell: the tmux
// session `term_<title>` in the worktree, named as the Reply says, with the
// options every client of it relies on. The title holds a space, a dot and
// a colon, which ToLoomTmuxName maps out so the name can be targeted
// exactly.
func TestEnsureTerminal_StartsTheShellInTheWorktree(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	const title = "fix 1.2: ui"
	killSessionAtEnd(t, tmux.TerminalSessionName(title))
	dir := t.TempDir()
	inst := shellInst(t, title, dir)
	m := shellModel(t, inst)

	m.EnsureTerminal(m.idOf(inst), 3)
	r := replyTo(runJobs(t, m), 3)

	require.NotNil(t, r, "the request is answered")
	require.NoError(t, r.Err)
	assert.Equal(t, shellName(title), r.Terminal)
	assert.True(t, tmux.ExactlyTargetable(r.Terminal), "Story 3's attach targets it exactly")
	assert.True(t, shellAlive(t, title))
	assert.Equal(t, canonical(t, dir), canonical(t, sessionPath(t, tmux.TerminalSessionName(title))), "it runs in the worktree")
	assert.Equal(t, "on", shellOption(t, title, "detach-on-destroy"), "a killed shell ends its attach client")
}

// A shell that already runs where the instance's own session starts, in the
// worktree or below it, is the instance's: the Reply names it and nothing
// is started over it.
func TestEnsureTerminal_AShellStartedInTheWorktreeIsTheInstancesOwn(t *testing.T) {
	for _, where := range []string{"the worktree", "below the worktree"} {
		t.Run(where, func(t *testing.T) {
			isolateTmux(t)
			t.Setenv("SHELL", "/bin/sh")
			dir := t.TempDir()
			start := dir
			if where == "below the worktree" {
				start = filepath.Join(dir, "sub")
				require.NoError(t, os.MkdirAll(start, 0o755))
			}
			startForeignSession(t, tmux.TerminalSessionName("web"), start)
			inst := shellInst(t, "web", dir)
			m := shellModel(t, inst)

			m.EnsureTerminal(m.idOf(inst), 3)
			r := replyTo(runJobs(t, m), 3)

			require.NotNil(t, r)
			require.NoError(t, r.Err)
			assert.Equal(t, shellName("web"), r.Terminal)
			assert.Equal(t, canonical(t, start), canonical(t, sessionPath(t, tmux.TerminalSessionName("web"))), "the shell is the one that was running")
		})
	}
}

// A session under the shell's name that started somewhere else (another
// workspace's same-titled session, or an agent whose title begins term_)
// is not the instance's: the request is refused, nothing is attached, and
// that session runs on.
func TestEnsureTerminal_ASessionStartedElsewhereIsARefusal(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	elsewhere := t.TempDir()
	startForeignSession(t, tmux.TerminalSessionName("web"), elsewhere)
	inst := shellInst(t, "web", t.TempDir())
	m := shellModel(t, inst)

	m.EnsureTerminal(m.idOf(inst), 3)
	r := replyTo(runJobs(t, m), 3)

	require.NotNil(t, r)
	require.Error(t, r.Err)
	assert.ErrorIs(t, r.Err, ErrRefused, "the web layer answers it 409")
	assert.Contains(t, r.Err.Error(), "another session's")
	assert.Empty(t, r.Terminal, "no name is handed out for a session that is not the instance's")
	assert.Equal(t, canonical(t, elsewhere), canonical(t, sessionPath(t, tmux.TerminalSessionName("web"))), "the other session runs on where it started")
}

// A tmux that cannot answer is a refusal, and loom starts and attaches
// nothing: neither the has-session probe going unanswered (tmux missing
// from the path) nor the listing that tells whose a live session is.
func TestEnsureTerminal_TmuxThatCannotAnswerIsARefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*exec.Cmd) error
		out  func(*exec.Cmd) ([]byte, error)
		want string
	}{
		{"the probe never answers",
			func(*exec.Cmd) error { return &exec.Error{Name: "tmux", Err: exec.ErrNotFound} },
			func(*exec.Cmd) ([]byte, error) { return nil, nil },
			"did not answer whether"},
		{"the listing never answers",
			func(*exec.Cmd) error { return nil }, // has-session: the session runs
			func(*exec.Cmd) ([]byte, error) { return nil, errors.New("timed out") },
			"did not answer whose"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			t.Setenv("SHELL", "/bin/sh")
			killSessionAtEnd(t, tmux.TerminalSessionName("web"))
			inst := shellInst(t, "web", t.TempDir())
			m := shellModel(t, inst)
			var ran []string
			m.SetExecForTest(cmd_test.MockCmdExec{
				RunFunc: func(c *exec.Cmd) error {
					ran = append(ran, cmd_test.TmuxSubcommand(c.Args))
					return tc.run(c)
				},
				OutputFunc: tc.out,
			})

			m.EnsureTerminal(m.idOf(inst), 3)
			r := replyTo(runJobs(t, m), 3)

			require.NotNil(t, r)
			assert.ErrorIs(t, r.Err, ErrRefused)
			assert.Contains(t, r.Err.Error(), tc.want)
			assert.Empty(t, r.Terminal)
			assert.NotContains(t, ran, "kill-session", "nothing is closed")
			assert.Empty(t, sessionPath(t, tmux.TerminalSessionName("web")), "nothing is started")
		})
	}
}

// racingExec is a tmux whose has-session first says no session runs and
// then, from Start's own check on, that one does: another client made the
// shell between the job's probe and its start. list is what `tmux ls` then
// prints: the sessions and where each started, "<name>\t<dir>\n" a line.
func racingExec(probes *int, ran *[]string, list string) cmd_test.MockCmdExec {
	noSession := exec.Command("sh", "-c", "exit 1").Run()
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			sub := cmd_test.TmuxSubcommand(c.Args)
			*ran = append(*ran, sub)
			if sub == "has-session" {
				*probes++
				if *probes == 1 {
					return noSession
				}
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return []byte(list), nil },
	}
}

// tmux.ErrSessionExists from the start means another client made the shell
// meanwhile: when it started in the worktree that is a success, the shell is
// named, and the job started and closed nothing.
func TestEnsureTerminal_AShellAnotherClientMadeMeanwhileIsASuccess(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	dir := t.TempDir()
	inst := shellInst(t, "web", dir)
	m := shellModel(t, inst)
	var probes int
	var ran []string
	m.SetExecForTest(racingExec(&probes, &ran, shellName("web")+"\t"+dir+"\n"))

	m.EnsureTerminal(m.idOf(inst), 3)
	r := replyTo(runJobs(t, m), 3)

	require.NotNil(t, r)
	require.NoError(t, r.Err)
	assert.Equal(t, shellName("web"), r.Terminal)
	assert.Equal(t, 2, probes, "the probe, then Start's own check")
	assert.NotContains(t, ran, "kill-session")
	assert.Empty(t, sessionPath(t, tmux.TerminalSessionName("web")), "no session was made by the job")
}

// The session another client put under the name meanwhile gets the same
// judgement as one that existed: started in another directory it is another
// session's, and the Reply is a refusal that names nothing.
func TestEnsureTerminal_ASessionAnotherClientMadeElsewhereMeanwhileIsARefusal(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	inst := shellInst(t, "web", t.TempDir())
	m := shellModel(t, inst)
	var probes int
	var ran []string
	m.SetExecForTest(racingExec(&probes, &ran, shellName("web")+"\t"+t.TempDir()+"\n"))

	m.EnsureTerminal(m.idOf(inst), 3)
	r := replyTo(runJobs(t, m), 3)

	require.NotNil(t, r)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.Contains(t, r.Err.Error(), "another session's")
	assert.Empty(t, r.Terminal, "the browser must not type into another worktree's shell")
	assert.Equal(t, 2, probes, "the probe, then Start's own check")
	assert.NotContains(t, ran, "kill-session")
}

// A shell that is gone when its start directory is read is never named:
// whether the probe found it alive (it ended before the listing) or Start
// found it (another client's shell ended before the listing), the Reply is a
// refusal to try again, and nothing is started or closed.
func TestEnsureTerminal_AShellThatEndedBeforeTheListingIsNotNamed(t *testing.T) {
	for _, tc := range []struct {
		name string
		exec func(probes *int, ran *[]string) cmd_test.MockCmdExec
	}{
		{"the probe found it", func(probes *int, ran *[]string) cmd_test.MockCmdExec {
			return cmd_test.MockCmdExec{
				RunFunc: func(c *exec.Cmd) error {
					*ran = append(*ran, cmd_test.TmuxSubcommand(c.Args))
					return nil // has-session: it runs
				},
				OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil }, // tmux ls: nothing listed
			}
		}},
		{"Start's own check found it", func(probes *int, ran *[]string) cmd_test.MockCmdExec {
			return racingExec(probes, ran, "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			t.Setenv("SHELL", "/bin/sh")
			killSessionAtEnd(t, tmux.TerminalSessionName("web"))
			inst := shellInst(t, "web", t.TempDir())
			m := shellModel(t, inst)
			var probes int
			var ran []string
			m.SetExecForTest(tc.exec(&probes, &ran))

			m.EnsureTerminal(m.idOf(inst), 3)
			r := replyTo(runJobs(t, m), 3)

			require.NotNil(t, r)
			assert.ErrorIs(t, r.Err, ErrRefused)
			assert.Contains(t, r.Err.Error(), "ended while loom looked at it")
			assert.Empty(t, r.Terminal, "a shell that no longer runs is not named")
			assert.NotContains(t, ran, "kill-session", "nothing is closed")
			assert.Empty(t, sessionPath(t, tmux.TerminalSessionName("web")), "nothing is started")
		})
	}
}

// shellRow is a started, active session titled "web" in dir, served by a
// model whose EnsureTerminal(…, 4) job has not run yet.
func shellRow(t *testing.T, dir string) (*Model, *session.Instance) {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	killSessionAtEnd(t, tmux.TerminalSessionName("web"))
	inst := shellInst(t, "web", dir)
	return shellModel(t, inst), inst
}

// ensureJob queues EnsureTerminal(…, 4) and returns its one job.
func ensureJob(t *testing.T, m *Model, inst *session.Instance) Job {
	t.Helper()
	m.EnsureTerminal(m.idOf(inst), 4)
	out := m.Drain()
	require.Len(t, out.Jobs, 1)
	return out.Jobs[0]
}

// The race the TUI's own comment records, closed for this path: a kill or a
// pause that begins (or finishes) while the job makes the shell must leave
// none behind. On delivery the model finds the row no longer started and
// active, closes the shell it made in a job of its own, and refuses.
func TestEnsureTerminal_AShellMadeForARowThatWentIsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, m *Model, inst *session.Instance)
	}{
		{"a kill begins", func(t *testing.T, m *Model, inst *session.Instance) {
			require.NoError(t, inst.TransitionTo(session.Deleting))
		}},
		{"a pause begins", func(t *testing.T, m *Model, inst *session.Instance) {
			require.NoError(t, inst.TransitionTo(session.Loading))
		}},
		{"the row is gone", func(t *testing.T, m *Model, inst *session.Instance) {
			m.removeEverywhere(inst)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			m, inst := shellRow(t, t.TempDir())
			result := ensureJob(t, m, inst)()
			require.True(t, shellAlive(t, "web"), "fixture: the job made the shell")
			tc.change(t, m, inst)

			m.Deliver(result)
			after := m.Drain()

			r := replyTo(after.Events, 4)
			require.NotNil(t, r)
			assert.ErrorIs(t, r.Err, ErrRefused)
			assert.Empty(t, r.Terminal, "no name is handed out for a shell that is being closed")
			require.Len(t, after.Jobs, 1, "the shell is closed by a job of its own")
			m.Deliver(after.Jobs[0]())
			assert.False(t, shellAlive(t, "web"), "no shell is left behind")
		})
	}
}

// The other side: a row still started and active keeps its shell, and the
// Reply names it.
func TestEnsureTerminal_AShellMadeForAnActiveRowIsKept(t *testing.T) {
	isolateTmux(t)
	m, inst := shellRow(t, t.TempDir())
	result := ensureJob(t, m, inst)()

	m.Deliver(result)
	after := m.Drain()

	r := replyTo(after.Events, 4)
	require.NotNil(t, r)
	require.NoError(t, r.Err)
	assert.Equal(t, shellName("web"), r.Terminal)
	assert.Empty(t, after.Jobs, "nothing is closed")
	assert.True(t, shellAlive(t, "web"))
}

// A shell that existed before the request is never closed here, whatever
// became of the row: pause and kill own it. The Reply is still a refusal,
// since a kill or pause is closing that shell and nothing may attach to it.
func TestEnsureTerminal_AShellThatExistedIsNotClosedWhenTheRowGoes(t *testing.T) {
	isolateTmux(t)
	dir := t.TempDir()
	m, inst := shellRow(t, dir)
	startForeignSession(t, tmux.TerminalSessionName("web"), dir)
	result := ensureJob(t, m, inst)()
	require.NoError(t, inst.TransitionTo(session.Deleting))

	m.Deliver(result)
	after := m.Drain()

	r := replyTo(after.Events, 4)
	require.NotNil(t, r)
	assert.ErrorIs(t, r.Err, ErrRefused, "nothing attaches to a shell a kill is closing")
	assert.Empty(t, r.Terminal, "no name is handed out")
	assert.Empty(t, after.Jobs, "no close job: the job started nothing")
	assert.True(t, shellAlive(t, "web"), "the shell that was there runs on")
}

// A shell another client made between the job's probe and its start is no
// more the job's than one that existed: it is not closed, and the Reply is
// a refusal all the same.
func TestEnsureTerminal_AShellAnotherClientMadeIsNotClosedWhenTheRowGoes(t *testing.T) {
	isolateTmux(t)
	dir := t.TempDir()
	m, inst := shellRow(t, dir)
	var probes int
	var ran []string
	m.SetExecForTest(racingExec(&probes, &ran, shellName("web")+"\t"+dir+"\n"))
	result := ensureJob(t, m, inst)()
	require.NoError(t, inst.TransitionTo(session.Deleting))

	m.Deliver(result)
	after := m.Drain()

	r := replyTo(after.Events, 4)
	require.NotNil(t, r)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.Empty(t, r.Terminal)
	assert.Empty(t, after.Jobs)
	assert.NotContains(t, ran, "kill-session")
}

// The close job confirms the session still starts in the instance's
// worktree: a session another client put under the name meanwhile is left
// running, and nothing is killed.
func TestEnsureTerminal_TheCloseLeavesASessionStartedElsewhere(t *testing.T) {
	isolateTmux(t)
	m, inst := shellRow(t, t.TempDir())
	result := ensureJob(t, m, inst)()
	require.True(t, shellAlive(t, "web"), "fixture: the job made the shell")
	require.NoError(t, tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget(shellName("web"))).Run())
	elsewhere := t.TempDir()
	startForeignSession(t, tmux.TerminalSessionName("web"), elsewhere)
	require.NoError(t, inst.TransitionTo(session.Deleting))

	m.Deliver(result)
	after := m.Drain()
	require.Len(t, after.Jobs, 1)
	m.Deliver(after.Jobs[0]())

	assert.Equal(t, canonical(t, elsewhere), canonical(t, sessionPath(t, tmux.TerminalSessionName("web"))), "the other session runs on")
}

// A launch that failed after tmux may have made the shell leaves it
// possibly started: when its row went it is closed if it runs in the
// worktree, and the Reply keeps the job's own error; when the row is
// active it is left, and the error stands.
func TestEnsureTerminal_APossiblyStartedShellIsClosedWhenTheRowGoes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rowGone    bool
		wantClosed bool
	}{
		{"the row went", true, true},
		{"the row is active", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			dir := t.TempDir()
			m, inst := shellRow(t, dir)
			startForeignSession(t, tmux.TerminalSessionName("web"), dir) // what tmux made before the launch failed
			if tc.rowGone {
				require.NoError(t, inst.TransitionTo(session.Deleting))
			}

			m.Deliver(tracked{req: 6, id: m.idOf(inst), result: terminalResult{
				title: "web", home: inst.SessionHome(), possible: true, err: errors.New("open a terminal of web: boom"),
			}})
			after := m.Drain()

			r := replyTo(after.Events, 6)
			require.NotNil(t, r)
			assert.EqualError(t, r.Err, "open a terminal of web: boom", "the job's own error stands")
			assert.Empty(t, r.Terminal)
			if tc.wantClosed {
				require.Len(t, after.Jobs, 1)
				m.Deliver(after.Jobs[0]())
			} else {
				assert.Empty(t, after.Jobs)
			}
			assert.Equal(t, !tc.wantClosed, shellAlive(t, "web"))
		})
	}
}
