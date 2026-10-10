//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The daemon's lifecycle end to end (daemon stage 3B): every sandboxed loom
// below is a client of the sandbox's `loom serve`, which the first one
// starts and which outlives it.

// What a TUI shows while its daemon is away (bannerText in app/link.go), and
// what it says when it gives up on one (main.go).
const (
	// bannerStopping: the daemon said bye and finishes its jobs in flight.
	bannerStopping = "the loom daemon is stopping"
	// bannerWaiting: the daemon stopped; the TUI polls for one and starts
	// none, and ctrl+r starts one.
	bannerWaiting = "the loom daemon stopped: waiting for one to start (ctrl+r starts it)"
	// bannerReconnecting: the daemon was lost with no bye (a crash); the TUI
	// redials on a backoff and starts a daemon when none runs.
	bannerReconnecting = "lost the loom daemon: reconnecting"
	// replacedByNewer: a newer loom replaced the daemon, so an older TUI
	// exits rather than rejoin it.
	replacedByNewer = "the loom daemon was replaced by a newer loom"
)

// servingDaemon waits until a daemon serves the sandbox other than any of
// not (pids of daemons that must be gone by now), and returns its record.
func servingDaemon(t *testing.T, sb *devsandbox.Sandbox, not ...int) daemon.Record {
	t.Helper()
	var rec daemon.Record
	require.Eventually(t, func() bool {
		r, held := sb.Daemon()
		rec = r
		for _, pid := range not {
			if r.PID == pid {
				return false
			}
		}
		return held && r.IsDaemon()
	}, uiTimeout, 50*time.Millisecond, "no daemon serves the sandbox (last record: %+v)", rec)
	return rec
}

// alive reports whether process pid runs (a zombie does not).
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true // no /proc: the signal is all there is
	}
	// The state follows the command name, which is in parentheses.
	i := bytes.LastIndexByte(stat, ')')
	return i < 0 || i+2 >= len(stat) || stat[i+2] != 'Z'
}

// waitExit waits up to timeout for process pid to end, reporting whether
// it did.
func waitExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for alive(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// exeOf is the executable process pid runs, "" where /proc can't say.
func exeOf(pid int) string {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return exe
}

// panePID is the pid of the process in the first pane of tmux session
// name on the sandbox's server.
func panePID(t *testing.T, sb *devsandbox.Sandbox, name string) int {
	t.Helper()
	out, err := tmux.CommandOnSocket(context.Background(), sb.Socket(),
		"display-message", "-p", "-t", tmux.PaneTarget(name), "#{pane_pid}").Output()
	require.NoError(t, err, "no pane for %s", name)
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	require.NoError(t, err, "no pane for %s (%q)", name, out)
	return pid
}

// agentPID is the pid of the agent of the session titled title.
func agentPID(t *testing.T, sb *devsandbox.Sandbox, title string) int {
	t.Helper()
	return panePID(t, sb, tmux.ToLoomTmuxName(title))
}

// requireReattached checks that the session titled title, which ran agent
// agent, is listed and running on that same agent: neither relaunched nor
// paused. It selects the session (the second row, after the workspace
// terminal) to see its pane.
func requireReattached(t *testing.T, sb *devsandbox.Sandbox, title string, agent int) {
	t.Helper()
	require.NoError(t, sb.WaitFor(title, uiTimeout))
	assert.Equal(t, agent, agentPID(t, sb, title), "the agent of %s was relaunched, not reattached", title)
	require.NoError(t, sb.SendKeys("j"))
	require.NoError(t, sb.WaitFor("Agent · dev/"+title, uiTimeout))
	require.NoError(t, sb.WaitFor("commands: work N", uiTimeout), "the pane shows %s's live agent", title)
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotContains(t, screen, "paused", "%s came back paused", title)
}

// requireKept checks that a TUI which was open on the session titled
// title, selected, still shows it selected after its daemon went and
// another served: listed with its pane (Agent · dev/<title>), on the same
// agent as before, and not paused. Unlike requireReattached it presses no
// key, since the selection is what is under test.
func requireKept(t *testing.T, sb *devsandbox.Sandbox, title string, agent int) {
	t.Helper()
	require.NoError(t, sb.WaitFor("Agent · dev/"+title, uiTimeout), "%s is still the selected row", title)
	require.NoError(t, sb.WaitFor("commands: work N", uiTimeout), "the pane shows %s's live agent", title)
	assert.Equal(t, agent, agentPID(t, sb, title), "the agent of %s was relaunched, not reattached", title)
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotContains(t, screen, "paused", "%s came back paused", title)
}

// waitOnline waits until the driver's banner is gone and the TUI says it
// joined a daemon again.
func waitOnline(t *testing.T, sb *devsandbox.Sandbox) {
	t.Helper()
	require.NoError(t, sb.WaitFor("reconnected to the loom daemon", uiTimeout))
	waitGone(t, sb, bannerReconnecting)
	waitGone(t, sb, bannerWaiting)
	waitGone(t, sb, bannerStopping)
}

// requireNoDaemonFor checks that no process takes the sandbox's lock for
// d: nothing started a daemon, the TUI waiting included.
func requireNoDaemonFor(t *testing.T, sb *devsandbox.Sandbox, d time.Duration) {
	t.Helper()
	require.Never(t, func() bool {
		_, held := sb.Daemon()
		return held
	}, d, 100*time.Millisecond, "a daemon started by itself")
}

// waitGone waits until text is no longer on the driver's screen.
func waitGone(t *testing.T, sb *devsandbox.Sandbox, text string) {
	t.Helper()
	var screen string
	require.Eventually(t, func() bool {
		s, err := sb.Screen(false)
		screen = s
		return err == nil && !strings.Contains(s, text)
	}, uiTimeout, 100*time.Millisecond, "%q is still on %s's screen:\n%s", text, sb.Driver(), screen)
}

// exitText waits for the driver's program to exit and returns what the
// pane shows, wrapped lines joined.
func exitText(t *testing.T, sb *devsandbox.Sandbox) string {
	t.Helper()
	require.Eventually(t, func() bool { return !sb.DriverRunning() }, uiTimeout, 100*time.Millisecond,
		"%s's program never exited", sb.Driver())
	text, err := sb.Text()
	require.NoError(t, err)
	return text
}

// runLoom runs argv (a loom build and its arguments) in the sandbox's
// environment and returns its combined output.
func runLoom(t *testing.T, sb *devsandbox.Sandbox, argv ...string) (string, error) {
	t.Helper()
	c := sb.Cmd(argv...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	require.NoError(t, c.Start())
	timer := time.AfterFunc(uiTimeout, func() { _ = c.Process.Kill() })
	defer timer.Stop()
	err := c.Wait()
	return out.String(), err
}

// buildLoom builds loom with ldflags into the sandbox's bin dir as name.
func buildLoom(t *testing.T, sb *devsandbox.Sandbox, name, ldflags string) string {
	t.Helper()
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	out := filepath.Join(sb.BinDir(), name)
	require.NoError(t, sb.BuildLoom(root, out, ldflags))
	return out
}

// startBuild starts the loom build bin in the driver and waits for its UI.
func startBuild(t *testing.T, sb *devsandbox.Sandbox, bin string) {
	t.Helper()
	require.NoError(t, sb.Start(devsandbox.StartOptions{Command: []string{bin, "--workspace", devsandbox.WorkspaceName}}))
	require.NoError(t, sb.WaitFor(devsandbox.WorkspaceName, uiTimeout))
}

func TestE2E_Daemon_SpawnsOnDemand(t *testing.T) {
	sb := newSandbox(t, "")
	_, held := sb.Daemon()
	require.False(t, held, "no daemon runs before the first loom")

	startLoom(t, sb)
	rec := servingDaemon(t, sb)
	info, err := os.Stat(rec.Socket)
	require.NoError(t, err, "the socket the lock records")
	assert.NotZero(t, info.Mode()&os.ModeSocket, "%s is not a socket", rec.Socket)
	nc, err := net.Dial("unix", rec.Socket)
	require.NoError(t, err, "the daemon answers on its socket")
	_ = nc.Close()

	assert.NotEqual(t, panePID(t, sb, sb.Driver()), rec.PID, "the daemon is a process of its own")
	if exe := exeOf(rec.PID); exe != "" {
		// /proc names resolved paths, and the TUI spawns the daemon by
		// os.Executable, which is resolved too: compare with the sandbox's
		// paths resolved, which a symlinked temp dir would otherwise fail.
		bin, globalDir := resolved(t, sb.LoomBin()), resolved(t, sb.GlobalDir())
		assert.Equal(t, bin, exe, "the TUI starts its own build as the daemon")
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", rec.PID))
		require.NoError(t, err)
		assert.Equal(t, bin+"\x00serve\x00", string(cmdline))
		cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", rec.PID))
		require.NoError(t, err)
		assert.Equal(t, globalDir, cwd, "the daemon keeps no client's working directory")
	}
}

// resolved is path with its symlinks resolved, as /proc names it.
func resolved(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return r
}

// A daemon killed outright leaves its socket file behind and its lock
// free; the next loom starts a new daemon there, which reattaches the
// sessions the dead one left running.
func TestE2E_Daemon_StaleSocket(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	old := servingDaemon(t, sb)
	require.NoError(t, sb.StopDriver(10*time.Second))

	require.NoError(t, syscall.Kill(old.PID, syscall.SIGKILL))
	require.True(t, waitExit(old.PID, uiTimeout))
	_, held := sb.Daemon()
	require.False(t, held, "the dead daemon's lock is free")
	_, err := os.Stat(old.Socket)
	require.NoError(t, err, "a killed daemon leaves its socket file")

	startLoom(t, sb)
	fresh := servingDaemon(t, sb, old.PID)
	assert.Equal(t, old.Socket, fresh.Socket, "the new daemon listens where the dead one did")
	requireReattached(t, sb, "keeper", agent)
}

// The daemon killed under an open TUI (a crash, so no bye): the TUI stays
// up under a banner, redials, starts a daemon when none runs, and joins it,
// keeping its tab, its selection and the agent it was showing; the agent
// keeps running in tmux throughout.
func TestE2E_Daemon_KilledUnderAnOpenTUIReconnects(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	tui := panePID(t, sb, sb.Driver())
	rec := servingDaemon(t, sb)

	require.NoError(t, syscall.Kill(rec.PID, syscall.SIGKILL))
	require.NoError(t, sb.WaitFor(bannerReconnecting, uiTimeout))
	fresh := servingDaemon(t, sb, rec.PID)
	assert.NotEqual(t, rec.PID, fresh.PID, "a new daemon serves")

	waitOnline(t, sb)
	assert.True(t, sb.DriverRunning(), "the TUI did not exit")
	assert.Equal(t, tui, panePID(t, sb, sb.Driver()), "the same TUI process is still running")
	requireKept(t, sb, "keeper", agent)
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotContains(t, screen, "panic")

	// The rejoined client takes requests again.
	createSession(t, sb, "after")
}

// Two TUIs on one daemon: a kill in one shows in the other, and a session
// one creates is listed in the other without taking it over (no inline
// attach: that is the creator's alone).
func TestE2E_Daemon_TwoTUIs(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "alpha")
	rec := servingDaemon(t, sb)

	second, err := sb.WithDriver("dev-driver-2")
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			screen, _ := second.Screen(false)
			t.Logf("second TUI's last screen:\n%s", screen)
		}
	})
	startLoom(t, second)
	require.NoError(t, second.WaitFor("alpha", uiTimeout))
	now, _ := sb.Daemon()
	assert.Equal(t, rec.PID, now.PID, "the second TUI joins the running daemon")

	// Kill alpha from the second TUI.
	require.NoError(t, second.SendKeys("j"))
	require.NoError(t, second.WaitFor("Agent · dev/alpha", uiTimeout))
	require.NoError(t, second.SendKeys("D"))
	require.NoError(t, second.WaitFor("Kill session 'alpha'?", uiTimeout))
	require.NoError(t, second.SendKeys("y"))
	waitGone(t, sb, "alpha")
	waitGone(t, second, "alpha")

	// Create beta in the first: it attaches there, and only there.
	require.NoError(t, sb.SendKeys("n"))
	require.NoError(t, sb.WaitFor("enter a name for the instance", uiTimeout))
	require.NoError(t, sb.SendText("beta"))
	require.NoError(t, sb.SendKeys("Enter"))
	require.NoError(t, sb.WaitFor("Session Launch Options", uiTimeout))
	require.NoError(t, sb.SendKeys("Enter"))
	require.NoError(t, sb.WaitFor("CAPTURING INPUT", uiTimeout))
	require.NoError(t, second.WaitFor("beta", uiTimeout))
	for range 10 {
		screen, err := second.Screen(false)
		require.NoError(t, err)
		require.NotContains(t, screen, "CAPTURING INPUT", "another TUI's create attached this one")
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, sb.SendKeys("C-q"))
}

// A newer loom replaces the running daemon with its own build; the
// sessions survive the swap, and a TUI of the older build still open on
// the old daemon waits for a daemon, finds the newer one and exits saying
// so, with no panic.
func TestE2E_Daemon_NewerBuildReplacesIt(t *testing.T) {
	sb := newSandbox(t, "")
	newer := buildLoom(t, sb, "loom-99", "-X main.version=99.0.0")
	startLoom(t, sb)
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	old := servingDaemon(t, sb)

	upgraded, err := sb.WithDriver("upgraded")
	require.NoError(t, err)
	startBuild(t, upgraded, newer)
	fresh := servingDaemon(t, sb, old.PID)
	assert.True(t, waitExit(old.PID, uiTimeout), "the older daemon was stopped")
	if exe := exeOf(fresh.PID); exe != "" {
		assert.Equal(t, newer, exe, "the newer loom serves with its own build")
	}
	serveLog, err := os.ReadFile(daemon.LogPath(sb.GlobalDir()))
	require.NoError(t, err)
	assert.Contains(t, string(serveLog), "msg=serve.stopped", "the older daemon stopped gracefully, saving first")

	text := exitText(t, sb)
	assert.Contains(t, text, replacedByNewer, "the older TUI exits when the newer daemon answers")
	assert.Contains(t, text, "run loom again")
	assert.NotContains(t, text, "panic")
	assert.NotContains(t, text, "goroutine ")
	assert.Equal(t, agent, agentPID(t, sb, "keeper"), "the agent keeps running")
	requireReattached(t, upgraded, "keeper", agent)
}

// Two TUIs starting at once with no daemon may each start one: the second
// daemon finds the first's lock and exits, and both TUIs use the first.
func TestE2E_Daemon_TwoTUIsStartingAtOnce(t *testing.T) {
	sb := newSandbox(t, "")
	second, err := sb.WithDriver("dev-driver-2")
	require.NoError(t, err)
	require.NoError(t, sb.Start(devsandbox.StartOptions{}))
	require.NoError(t, second.Start(devsandbox.StartOptions{}))
	require.NoError(t, sb.WaitFor(devsandbox.WorkspaceName, uiTimeout))
	require.NoError(t, second.WaitFor(devsandbox.WorkspaceName, uiTimeout))
	rec := servingDaemon(t, sb)

	if exeOf(os.Getpid()) == "" {
		return // no /proc to count daemons by
	}
	require.Eventually(t, func() bool {
		pids := daemonsOf(sb.BinDir())
		return len(pids) == 1 && pids[0] == rec.PID
	}, uiTimeout, 100*time.Millisecond, "one daemon serves the sandbox, the lock holder (daemons: %v)", daemonsOf(sb.BinDir()))
	assert.True(t, sb.DriverRunning())
	assert.True(t, second.DriverRunning())
}

// daemonsOf lists the pids of the `loom serve` processes a build in one of
// dirs runs: a command line of a program there and "serve". A TUI spawns
// its daemon by its resolved path, a test by the sandbox's, so both
// spellings of each dir count, and so does a build deleted since its
// daemon started, by the path it ran.
func daemonsOf(dirs ...string) []int {
	want := map[string]bool{}
	for _, d := range dirs {
		want[filepath.Clean(d)] = true
		if r, err := filepath.EvalSymlinks(d); err == nil {
			want[r] = true
		}
	}
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimSuffix(string(cmdline), "\x00"), "\x00")
		if len(args) != 2 || args[1] != "serve" {
			continue
		}
		in := filepath.Dir(args[0])
		match := want[in]
		if r, err := filepath.EvalSymlinks(in); err == nil {
			match = match || want[r]
		}
		if match && alive(pid) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// An older loom refuses a newer daemon and leaves it running.
func TestE2E_Daemon_OlderBuildRefuses(t *testing.T) {
	sb := newSandbox(t, "")
	newer := buildLoom(t, sb, "loom-99", "-X main.version=99.0.0")
	older := buildLoom(t, sb, "loom-0.0.1", "-X main.version=0.0.1")
	startBuild(t, sb, newer)
	rec := servingDaemon(t, sb)

	out, err := runLoom(t, sb, older, "--workspace", devsandbox.WorkspaceName)
	require.Error(t, err, "the older loom must refuse:\n%s", out)
	assert.Contains(t, out, "newer than this loom")
	assert.Contains(t, out, "upgrade loom")

	now, held := sb.Daemon()
	assert.True(t, held, "the newer daemon keeps running")
	assert.Equal(t, rec.PID, now.PID)
	assert.True(t, alive(rec.PID))
	assert.True(t, sb.DriverRunning(), "the newer TUI keeps running")
}

// `loom serve stop` with live sessions: the open TUI stays up, waiting for
// a daemon under its banner, the sessions keep running, and after the TUI
// quits the next loom's daemon reattaches them, running, not paused.
func TestE2E_Daemon_ServeStopKeepsTheSessions(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	rec := servingDaemon(t, sb)

	out, err := runLoom(t, sb, sb.LoomBin(), "serve", "stop")
	require.NoError(t, err, out)
	assert.Contains(t, out, "loom daemon stopped")
	_, held := sb.Daemon()
	assert.False(t, held, "serve stop returns once the daemon has let go of its lock")
	assert.True(t, waitExit(rec.PID, uiTimeout))
	require.NoError(t, sb.WaitFor(bannerWaiting, uiTimeout), "the TUI waits for a daemon")
	assert.True(t, sb.DriverRunning(), "the TUI does not exit with its daemon")
	assert.Equal(t, agent, agentPID(t, sb, "keeper"), "the agent keeps running without a daemon")

	// Quit the waiting TUI: the next loom starts the daemon.
	require.NoError(t, sb.StopDriver(10*time.Second))
	startLoom(t, sb)
	servingDaemon(t, sb, rec.PID)
	requireReattached(t, sb, "keeper", agent)
}

// `loom serve stop` under an open TUI, then ctrl+r: the TUI shows the
// stopped banner and refuses what needs the model, no process takes the lock
// of its own accord (a graceful stop is not followed by a respawn), and
// ctrl+r starts a daemon, which the TUI joins with its selection.
func TestE2E_Daemon_ServeStopWaitsAndCtrlRStartsOne(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	rec := servingDaemon(t, sb)

	out, err := runLoom(t, sb, sb.LoomBin(), "serve", "stop")
	require.NoError(t, err, out)
	assert.True(t, waitExit(rec.PID, uiTimeout))
	require.NoError(t, sb.WaitFor(bannerWaiting, uiTimeout))

	// Offline, a key that needs the model says so and opens nothing.
	require.NoError(t, sb.SendKeys("n"))
	require.NoError(t, sb.WaitFor("the loom daemon is stopped: n needs it", uiTimeout))
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotContains(t, screen, "enter a name for the instance")

	requireNoDaemonFor(t, sb, 3*time.Second)
	screen, err = sb.Screen(false)
	require.NoError(t, err)
	assert.Contains(t, screen, bannerWaiting, "still waiting after the poll found no daemon")

	require.NoError(t, sb.SendKeys("C-r"))
	fresh := servingDaemon(t, sb, rec.PID)
	assert.NotEqual(t, rec.PID, fresh.PID)
	waitOnline(t, sb)
	assert.True(t, sb.DriverRunning())
	requireKept(t, sb, "keeper", agent)

	// The rejoined client takes requests again.
	createSession(t, sb, "after")
}

// holdStashes makes every update of refs/stash in the sandbox's workspace
// repo take d: a pause stores the worktree's changes there, so a pause of a
// session with changes lasts at least that long. It installs a
// reference-transaction hook (git 2.28 and later), which blocks the update
// while it is prepared.
func holdStashes(t *testing.T, sb *devsandbox.Sandbox, d time.Duration) {
	t.Helper()
	hooks := filepath.Join(sb.RepoDir(), ".git", "hooks")
	require.NoError(t, os.MkdirAll(hooks, 0o755))
	script := fmt.Sprintf("#!/bin/sh\n[ \"$1\" = prepared ] || exit 0\n"+
		"while read -r old new ref; do\n  [ \"$ref\" = refs/stash ] && sleep %d\ndone\n", int(d.Seconds()))
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0o755))
}

// dirtyWorktree adds an untracked file to the worktree of the session
// titled title (<repo>/.loom/worktrees/<workspace>/<title>_<hash>, beside
// its .loom-title sidecar), so that pausing it has changes to stash.
func dirtyWorktree(t *testing.T, sb *devsandbox.Sandbox, title string) {
	t.Helper()
	trees, err := filepath.Glob(filepath.Join(sb.WorkspaceConfigDir(), "worktrees", "*", title+"_*"))
	require.NoError(t, err)
	var dirs []string
	for _, tree := range trees {
		if info, err := os.Stat(tree); err == nil && info.IsDir() {
			dirs = append(dirs, tree)
		}
	}
	require.Len(t, dirs, 1, "the worktree of %s", title)
	require.NoError(t, os.WriteFile(filepath.Join(dirs[0], "unsaved.txt"), []byte("work in progress\n"), 0o644))
}

// A pause in flight across `loom serve stop`: the stop waits for the pause,
// whose result the TUI receives before the connection closes (so it reads
// the session Paused while no daemon runs, and fails nothing as stranded),
// and the daemon saves before it exits, so a daemon started after reads the
// session Paused too, never stuck in Loading.
func TestE2E_Daemon_PauseAcrossAStop(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	rec := servingDaemon(t, sb)
	// The pause stores a stash, which this holds for 3s: the stop below
	// arrives with the pause in flight, not after it.
	const hold = 3 * time.Second
	dirtyWorktree(t, sb, "keeper")
	holdStashes(t, sb, hold)

	require.NoError(t, sb.SendKeys("s"))
	require.NoError(t, sb.WaitFor("Pause session 'keeper'?", uiTimeout))
	require.NoError(t, sb.SendKeys("y"))
	began := time.Now()
	out, err := runLoom(t, sb, sb.LoomBin(), "serve", "stop")
	require.NoError(t, err, out)
	assert.GreaterOrEqual(t, time.Since(began), hold/2, "serve stop waited for the pause in flight")
	assert.True(t, waitExit(rec.PID, uiTimeout))

	serveLog, err := os.ReadFile(daemon.LogPath(sb.GlobalDir()))
	require.NoError(t, err)
	assert.Contains(t, string(serveLog), "msg=serve.stopped", "the daemon stopped gracefully, saving first")
	assert.NotContains(t, string(serveLog), "serve.stopped_with_jobs_in_flight", "the stop waited out the pause")
	assert.True(t, waitExit(agent, uiTimeout), "the pause ended the agent")

	// The pause's result was published before the connection closed.
	require.NoError(t, sb.WaitFor(bannerWaiting, uiTimeout))
	require.NoError(t, sb.WaitFor("paused", uiTimeout), "the TUI read the pause finish before the daemon went")
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotContains(t, screen, "unavailable", "the pause was not failed as stranded")

	require.NoError(t, sb.SendKeys("C-r"))
	servingDaemon(t, sb, rec.PID)
	waitOnline(t, sb)
	require.NoError(t, sb.WaitFor("paused", uiTimeout), "the session reads Paused on the new daemon")
	screen, err = sb.Screen(false)
	require.NoError(t, err)
	assert.NotContains(t, screen, "panic")
}

// Assumption 1 of the daemon spec: a daemon a TUI started keeps running
// after that TUI exits and its tmux session is killed, and the next loom
// joins it.
func TestE2E_Daemon_OutlivesItsTUI(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	rec := servingDaemon(t, sb)
	tui := panePID(t, sb, sb.Driver())

	require.NoError(t, tmux.CommandOnSocket(context.Background(), sb.Socket(),
		"kill-session", "-t", tmux.SessionTarget(sb.Driver())).Run())
	require.True(t, waitExit(tui, uiTimeout), "the TUI outlived its tmux session")

	// Give a signal that reached the daemon time to end it.
	time.Sleep(500 * time.Millisecond)
	require.True(t, alive(rec.PID), "the daemon died with its TUI's tmux session")
	now, held := sb.Daemon()
	require.True(t, held)
	assert.Equal(t, rec.PID, now.PID)
	nc, err := net.Dial("unix", rec.Socket)
	require.NoError(t, err, "the daemon still answers")
	_ = nc.Close()

	startLoom(t, sb)
	again, _ := sb.Daemon()
	assert.Equal(t, rec.PID, again.PID, "the next loom joins the daemon rather than starting one")
}

// A rebuild of the sandbox (`loomdev build`) at the same version and
// commit replaces the sandbox daemon at the next start: only the
// executable tells the builds apart (here, the ldflags Go records in it).
func TestE2E_Daemon_RebuildReplacesIt(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	old := servingDaemon(t, sb)
	require.NoError(t, sb.StopDriver(10*time.Second))

	buildLoom(t, sb, filepath.Base(sb.LoomBin()), "-X main.e2eRebuild=1")
	startLoom(t, sb)
	servingDaemon(t, sb, old.PID)
	assert.True(t, waitExit(old.PID, uiTimeout), "the previous build's daemon was stopped")
	requireReattached(t, sb, "keeper", agent)
}

// Two daemons started at once, as two TUIs starting together can: the
// second finds the first's lock and stands down. It must not wait for the
// lock and take it when the first stops, booting a daemon no one asked
// for, after a stop, while the stop waits out its timeout.
func TestE2E_Daemon_StopDuringAStartRace(t *testing.T) {
	sb := newSandbox(t, "")
	// serve starts a daemon writing its output to out (nil for none) and
	// returns a channel that delivers its exit; it is killed at cleanup,
	// however the test ends.
	serve := func(out io.Writer) <-chan error {
		c := sb.Cmd(sb.LoomBin(), "serve")
		c.Stdout, c.Stderr = out, out
		require.NoError(t, c.Start())
		exited := make(chan error, 1)
		go func() { exited <- c.Wait() }()
		t.Cleanup(func() {
			_ = c.Process.Kill()
			waitExit(c.Process.Pid, 5*time.Second)
		})
		return exited
	}
	serve(nil)
	first := servingDaemon(t, sb)
	var out bytes.Buffer
	secondExited := serve(&out)
	select {
	case err := <-secondExited:
		require.NoError(t, err, "the second daemon stands down cleanly:\n%s", out.String())
		assert.Contains(t, out.String(), daemon.ErrRunning.Error())
	case <-time.After(uiTimeout):
		t.Fatalf("the second daemon is still running: it must stand down at once, not wait for the lock")
	}

	require.NoError(t, daemon.Stop(sb.GlobalDir(), 15*time.Second))
	assert.True(t, waitExit(first.PID, uiTimeout))
	rec, held := sb.Daemon()
	assert.False(t, held, "a daemon took over after the stop: %+v", rec)
}
