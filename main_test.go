package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRootCmd_VersionFlag exercises `loom --version`. The flag must
// print the same version string the `loom version` subcommand emits so
// users have a familiar one-shot affordance without remembering the
// subcommand spelling.
func TestRootCmd_VersionFlag(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"--version"})

	require.NoError(t, rootCmd.Execute())

	out := buf.String()
	assert.Contains(t, out, "loom version "+version,
		"--version must print the canonical version line")
	assert.Contains(t, out, "https://github.com/aidan-bailey/loom/releases/tag/v"+version,
		"--version must include the releases URL like the `loom version` subcommand does")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(out), "v"+version),
		"output must end with the version-tag URL, not stray content")
}

// isolateLoomEnv points every loom path and the tmux socket at throwaway
// locations so a missing guard cannot touch real state.
func isolateLoomEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	t.Setenv(tmux.EnvTmuxSocket, fmt.Sprintf("loomtest-none-%d", time.Now().UnixNano()))
	// reset (and serve) pin their tmux server for the rest of the process.
	t.Cleanup(func() { tmux.UseServer("") })
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	// pflag latches a bool flag's value across Execute() calls on the same
	// FlagSet — Parse only visits flags present in the new argv, so a prior
	// "--version" run (TestRootCmd_VersionFlag shares this package's single
	// rootCmd) would otherwise make every later Execute() short-circuit to
	// the version template before RunE ever runs. Reset it defensively; the
	// flag may not exist yet if this runs before rootCmd's first Execute().
	_ = rootCmd.Flags().Set("version", "false")
}

// stubNesting makes the nesting guard answer err, and returns the tmux
// servers it was asked about.
func stubNesting(t *testing.T, err error) *[]string {
	t.Helper()
	orig := nestingCheck
	var asked []string
	nestingCheck = func(server string) error {
		asked = append(asked, server)
		return err
	}
	t.Cleanup(func() { nestingCheck = orig })
	return &asked
}

func TestResetCmd_RefusesWhenNested(t *testing.T) {
	isolateLoomEnv(t)
	stubNesting(t, &tmux.NestedError{Session: "loom_term_x"})
	t.Cleanup(func() { resetForceFlag = false })

	rootCmd.SetArgs([]string{"reset", "--force"})
	err := rootCmd.Execute()

	var nested *tmux.NestedError
	require.ErrorAs(t, err, &nested)
	assert.Equal(t, "loom_term_x", nested.Session)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	require.NoError(t, w.Close())
	os.Stdout = orig
	return <-done
}

// startTmuxServer starts a tmux server listening at a socket of its own
// and returns the socket's path; the server is killed when the test ends.
func startTmuxServer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	dir, err := os.MkdirTemp("", "tx")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	require.NoError(t, exec.Command("tmux", "-f", os.DevNull, "-S", sock, "new-session", "-d", "-s", "keep", "sleep 60").Run())
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
	return sock
}

// lastDaemonOn leaves the record of a daemon that ran on the tmux server
// sock and has stopped, as `loom serve stop` leaves it: the lock free, the
// record still naming its server.
func lastDaemonOn(t *testing.T, sock string) {
	t.Helper()
	l, _, err := daemon.TryAcquire(os.Getenv(config.EnvGlobalDir), daemon.Record{PID: os.Getpid(), Build: "b", Tmux: sock})
	require.NoError(t, err)
	require.NoError(t, l.Close())
}

// TestServeCmd_GuardsTheServerItPins: `loom serve` decides nesting on the
// tmux server it will pin, the last daemon's while that server runs, not on
// the one this environment selects. A dev loom in a loom pane, with
// LOOM_TMUX_SOCKET naming another server, would otherwise start the user's
// daemon on the user's server while the user's daemon is down.
func TestServeCmd_GuardsTheServerItPins(t *testing.T) {
	isolateLoomEnv(t)
	sock := startTmuxServer(t)
	lastDaemonOn(t, sock)
	asked := stubNesting(t, &tmux.NestedError{Session: "loom_agent"})

	rootCmd.SetArgs([]string{"serve"})
	err := rootCmd.Execute()

	var nested *tmux.NestedError
	require.ErrorAs(t, err, &nested)
	assert.Equal(t, []string{sock}, *asked, "the server the daemon would pin")
	_, held := daemon.ReadRecord(os.Getenv(config.EnvGlobalDir))
	assert.False(t, held, "no daemon started")
}

// TestResetCmd_SweepsTheServerADaemonWouldPin: reset sweeps the tmux server
// the last daemon used while it runs, whatever this environment selects
// (LOOM_TMUX_SOCKET here names another), and decides nesting on it. Run
// after a `loom serve stop` from another environment, it found none of
// the workspace's sessions, then deleted the worktrees under live agents.
func TestResetCmd_SweepsTheServerADaemonWouldPin(t *testing.T) {
	isolateLoomEnv(t)
	t.Cleanup(func() { resetForceFlag = false })
	sock := startTmuxServer(t)
	lastDaemonOn(t, sock)
	asked := stubNesting(t, nil)
	wt := globalWorktree(t, "busy_18d7")
	fake := &resetTmux{listing: "loom_busy\t" + wt + "\n"}
	stubResetTmux(t, fake)

	_ = captureStdout(t, func() {
		rootCmd.SetArgs([]string{"reset", "--force"})
		require.NoError(t, rootCmd.Execute())
	})

	assert.Equal(t, []string{sock}, *asked, "the guard is decided on the server swept")
	require.NotEmpty(t, fake.commands)
	for _, args := range fake.commands {
		assert.True(t, slices.Equal([]string{"tmux", "-u", "-S", sock}, args[:4]), "on the last daemon's server: %q", args)
	}
	assert.Equal(t, []string{"=loom_busy"}, fake.killed)
}

func TestDebugCmd_ReportsIsolationKnobs(t *testing.T) {
	isolateLoomEnv(t)
	t.Setenv(tmux.EnvTmuxSocket, "loomdev-probe")
	stubNesting(t, nil)

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"debug"})
		require.NoError(t, rootCmd.Execute())
	})

	assert.Contains(t, out, "Tmux socket: loomdev-probe")
	server, err := tmux.ResolveServer()
	require.NoError(t, err)
	assert.Contains(t, out, "Tmux server: "+server)
	assert.Contains(t, out, "Global dir: "+os.Getenv(config.EnvGlobalDir))
	assert.Contains(t, out, "Daemon: not running")
	assert.Contains(t, out, "Daemon log: "+daemon.LogPath(os.Getenv(config.EnvGlobalDir)))
	assert.Contains(t, out, "Nesting guard: ok")
}

// TestDebugCmd_ReportsTheDaemon: debug names the daemon holding the global
// dir's lock, where it listens and the tmux server its TUIs use.
func TestDebugCmd_ReportsTheDaemon(t *testing.T) {
	isolateLoomEnv(t)
	stubNesting(t, nil)
	// A server no one runs: debug probes it (daemon.TmuxServer).
	server := filepath.Join(t.TempDir(), "tmux-1000", "default")
	holdLock(t, daemon.Record{PID: 4242, Socket: "/run/user/1000/loom/abc.sock", Build: "v0.13.1 ad199a3", Tmux: server})

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"debug"})
		require.NoError(t, rootCmd.Execute())
	})

	assert.Contains(t, out, "Daemon: pid 4242")
	assert.Contains(t, out, "socket /run/user/1000/loom/abc.sock, build v0.13.1 ad199a3, tmux server "+server+" (its TUIs use it)")
}

// TestDebugCmd_NamesTheServerADaemonWouldPin: the tmux server debug names
// is the one a daemon started now would use, the last daemon's while it
// runs, not the one this environment selects; the nesting guard is
// decided on it, as `loom serve`'s is.
func TestDebugCmd_NamesTheServerADaemonWouldPin(t *testing.T) {
	isolateLoomEnv(t)
	sock := startTmuxServer(t)
	lastDaemonOn(t, sock)
	asked := stubNesting(t, nil)

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"debug"})
		require.NoError(t, rootCmd.Execute())
	})

	assert.Contains(t, out, "Tmux server: "+sock+" (")
	assert.Equal(t, []string{sock}, *asked)
}

// holdLock holds the global dir's lock with rec, as a running daemon (or
// a loom from before it) would, until the test ends.
func holdLock(t *testing.T, rec daemon.Record) {
	t.Helper()
	l, _, err := daemon.TryAcquire(os.Getenv(config.EnvGlobalDir), rec)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
}

// TestStateWriters_RefuseWhileTheDaemonRuns: reset and `workspace migrate`
// write state.json themselves, which a running daemon would overwrite with
// its own sessions (and they its): they refuse, touching nothing, while a
// daemon (or a loom from before it) holds the lock.
func TestStateWriters_RefuseWhileTheDaemonRuns(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"reset", []string{"reset", "--force"}},
		{"workspace migrate", []string{"workspace", "migrate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateLoomEnv(t)
			stubNesting(t, nil)
			t.Cleanup(func() { resetForceFlag = false })
			wt := globalWorktree(t, "busy_18d7")
			fake := &resetTmux{listing: "loom_busy\t" + wt + "\n"}
			stubResetTmux(t, fake)
			holdLock(t, daemon.Record{PID: 4242, Socket: "/run/user/1000/loom/abc.sock", Build: "v0.13.1"})

			rootCmd.SetArgs(tc.args)
			err := rootCmd.Execute()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "the loom daemon is running: stop it first (`loom serve stop`)")
			assert.Empty(t, fake.killed)
			assert.FileExists(t, filepath.Join(wt, "work.txt"))
		})
	}

	t.Run("a loom from before the daemon", func(t *testing.T) {
		isolateLoomEnv(t)
		stubNesting(t, nil)
		t.Cleanup(func() { resetForceFlag = false })
		holdLock(t, daemon.Record{PID: 3713275, TTY: "/dev/pts/2"})

		rootCmd.SetArgs([]string{"reset", "--force"})
		err := rootCmd.Execute()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "a loom is running (pid 3713275 on /dev/pts/2")
	})
}

// resetTmux is a fake tmux for reset: the sweep's "ls" answers listing (or
// listErr), and every kill-session is recorded and answered with killErr.
// It records every command's arguments.
type resetTmux struct {
	listing  string
	listErr  error
	killErr  error
	killed   []string
	commands [][]string
}

func (r *resetTmux) Run(c *exec.Cmd) error {
	r.commands = append(r.commands, c.Args)
	if slices.Contains(c.Args, "kill-session") {
		r.killed = append(r.killed, c.Args[len(c.Args)-1])
		return r.killErr
	}
	return nil
}
func (r *resetTmux) Output(c *exec.Cmd) ([]byte, error) {
	r.commands = append(r.commands, c.Args)
	return []byte(r.listing), r.listErr
}
func (r *resetTmux) CombinedOutput(c *exec.Cmd) ([]byte, error) { return r.Output(c) }

func stubResetTmux(t *testing.T, fake *resetTmux) {
	t.Helper()
	orig := resetExecutor
	resetExecutor = func() cmd2.Executor { return fake }
	t.Cleanup(func() { resetExecutor = orig })
}

// globalWorktree lays out a worktree directory under the global config
// dir, which a global reset removes.
func globalWorktree(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv(config.EnvGlobalDir), "worktrees", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "work.txt"), []byte("agent output\n"), 0o644))
	return dir
}

// TestResetCmd_StopsBeforeWorktreesWhenSweepFails is the regression for
// c7c7b3d's sweep, which swallowed every error: a loaded tmux that timed
// out on "ls" (or a kill that failed) left this workspace's agents
// running, and reset then deleted the worktrees and branches under them.
func TestResetCmd_StopsBeforeWorktreesWhenSweepFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		tmux *resetTmux
	}{
		{"listing timed out", &resetTmux{listErr: errors.New("signal: killed")}},
		{"a kill failed", &resetTmux{killErr: errors.New("exit status 1")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateLoomEnv(t)
			stubNesting(t, nil)
			t.Cleanup(func() { resetForceFlag = false })
			wt := globalWorktree(t, "busy_18d7")
			if tc.tmux.listErr == nil {
				tc.tmux.listing = "loom_busy\t" + wt + "\n"
			}
			stubResetTmux(t, tc.tmux)

			var err error
			out := captureStdout(t, func() {
				rootCmd.SetArgs([]string{"reset", "--force"})
				err = rootCmd.Execute()
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "NOT removed")
			assert.FileExists(t, filepath.Join(wt, "work.txt"), "the worktree an agent may still use must survive")
			assert.NotContains(t, out, "Worktrees have been cleaned up")
		})
	}
}

// TestResetCmd_ReportsSweepCounts: reset says what it killed and what it
// left running, since sessions outside the workspace are spared.
func TestResetCmd_ReportsSweepCounts(t *testing.T) {
	isolateLoomEnv(t)
	stubNesting(t, nil)
	t.Cleanup(func() { resetForceFlag = false })
	wt := globalWorktree(t, "stale_18d7")
	fake := &resetTmux{listing: "loom_stale\t" + wt + "\n" +
		"loom_elsewhere\t" + t.TempDir() + "\n"}
	stubResetTmux(t, fake)

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"reset", "--force"})
		require.NoError(t, rootCmd.Execute())
	})

	assert.Equal(t, []string{"=loom_stale"}, fake.killed)
	assert.Contains(t, out, "1 killed, 1 left running (started outside this workspace)")
	assert.Contains(t, out, "Worktrees have been cleaned up")
	assert.NoDirExists(t, wt)
}
