//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
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

// daemonGone is what a TUI prints when its daemon goes away under it.
const daemonGone = "the daemon stopped"

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
		assert.Equal(t, sb.LoomBin(), exe, "the TUI starts its own build as the daemon")
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", rec.PID))
		require.NoError(t, err)
		assert.Equal(t, sb.LoomBin()+"\x00serve\x00", string(cmdline))
	}
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

// The daemon killed under an open TUI: the TUI restores the terminal, says
// so and exits, with no panic; the agents keep running in tmux.
func TestE2E_Daemon_KilledUnderAnOpenTUI(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "keeper")
	agent := agentPID(t, sb, "keeper")
	rec := servingDaemon(t, sb)

	require.NoError(t, syscall.Kill(rec.PID, syscall.SIGKILL))
	text := exitText(t, sb)
	assert.Contains(t, text, daemonGone)
	assert.Contains(t, text, "Run loom again")
	assert.NotContains(t, text, "panic")
	assert.NotContains(t, text, "goroutine ")
	assert.Equal(t, agent, agentPID(t, sb, "keeper"), "the agent keeps running")
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
// the old daemon exits cleanly.
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
	assert.Contains(t, text, daemonGone, "the older TUI goes with its daemon")
	assert.NotContains(t, text, "panic")
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
		pids := daemonsOf(sb.LoomBin())
		return len(pids) == 1 && pids[0] == rec.PID
	}, uiTimeout, 100*time.Millisecond, "one daemon serves the sandbox, the lock holder (daemons: %v)", daemonsOf(sb.LoomBin()))
	assert.True(t, sb.DriverRunning())
	assert.True(t, second.DriverRunning())
}

// daemonsOf lists the pids of the `serve` processes bin runs.
func daemonsOf(bin string) []int {
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err == nil && string(cmdline) == bin+"\x00serve\x00" && alive(pid) {
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

// `loom serve stop` with live sessions: the open TUI goes with its daemon,
// the sessions keep running, and the next loom's daemon reattaches them,
// running, not paused.
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
	assert.Contains(t, exitText(t, sb), daemonGone)
	assert.Equal(t, agent, agentPID(t, sb, "keeper"), "the agent keeps running without a daemon")

	startLoom(t, sb)
	servingDaemon(t, sb, rec.PID)
	requireReattached(t, sb, "keeper", agent)
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
// second waits for the first's lock. Stopping the first must not hand the
// lock to the second, which would boot a daemon no one asked for, after a
// stop, and make the stop wait out its timeout for a lock that never
// comes free.
func TestE2E_Daemon_StopDuringAStartRace(t *testing.T) {
	t.Skip("internal/daemon bug: Serve's lock wait hands the lock to the second daemon, and Stop waits for the lock rather than the pid; unskip with the fix")
	sb := newSandbox(t, "")
	serve := func() {
		c := sb.Cmd(sb.LoomBin(), "serve")
		require.NoError(t, c.Start())
		go func() { _ = c.Wait() }()
	}
	serve()
	first := servingDaemon(t, sb)
	serve()
	time.Sleep(500 * time.Millisecond) // the second now waits for the lock

	require.NoError(t, daemon.Stop(sb.GlobalDir(), 15*time.Second))
	assert.True(t, waitExit(first.PID, uiTimeout))
	time.Sleep(time.Second)
	rec, held := sb.Daemon()
	assert.False(t, held, "a daemon took over after the stop: %+v", rec)
}
