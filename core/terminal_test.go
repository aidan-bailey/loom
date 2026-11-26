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
