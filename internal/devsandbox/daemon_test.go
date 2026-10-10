package devsandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDaemonEnv, set to a global dir, makes the test binary a fake daemon
// of that dir (runFakeDaemon) instead of running the tests, and
// fakeDaemonModeEnv picks how it behaves (the fake… modes).
const (
	fakeDaemonEnv     = "DEVSANDBOX_FAKE_DAEMON"
	fakeDaemonModeEnv = "DEVSANDBOX_FAKE_DAEMON_MODE"
)

const (
	// fakeStubborn ignores SIGTERM: a daemon that won't stop.
	fakeStubborn = "stubborn"
	// fakePreDaemon holds the lock with the record of a loom from before
	// the daemon (a pid, no socket, no build), which daemon.Stop refuses.
	fakePreDaemon = "predaemon"
)

// runFakeDaemon stands in for `loom serve`, as far as the sandbox sees
// one: it holds dir's lock with a daemon's record until SIGTERM, which is
// how daemon.Stop asks a daemon to go. mode varies that (the fake… modes).
func runFakeDaemon(dir, mode string) int {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	rec := daemon.Record{PID: os.Getpid(), Started: time.Now(), Build: "fake", Socket: filepath.Join(dir, "fake.sock")}
	if mode == fakePreDaemon {
		rec = daemon.Record{PID: os.Getpid(), Started: time.Now()}
	}
	lock, _, err := daemon.TryAcquire(dir, rec)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for mode != "" { // until killed
		time.Sleep(time.Hour)
	}
	<-sigs
	_ = lock.Close()
	return 0
}

// startFakeDaemon runs a fake daemon of sb's global dir and waits until it
// holds the lock. It is killed at cleanup if still running.
func startFakeDaemon(t *testing.T, sb *Sandbox) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	return startFakeDaemonAs(t, sb, exe, "")
}

// sandboxBuild copies the test binary into sb's bin dir as its loom, so a
// fake daemon run from there is a build of the sandbox's own.
func sandboxBuild(t *testing.T, sb *Sandbox) string {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	data, err := os.ReadFile(exe)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(sb.BinDir(), 0o755))
	require.NoError(t, os.WriteFile(sb.LoomBin(), data, 0o755))
	return sb.LoomBin()
}

// startFakeDaemonAs runs exe, the test binary or a copy of it, as a fake
// daemon of sb's global dir in mode, and waits until it holds the lock. It
// is killed at cleanup if still running.
func startFakeDaemonAs(t *testing.T, sb *Sandbox, exe, mode string) *exec.Cmd {
	t.Helper()
	// "serve": daemon.Stop signals only a process whose command line has it.
	cmd := exec.Command(exe, "serve")
	cmd.Env = append(os.Environ(), fakeDaemonEnv+"="+sb.GlobalDir(), fakeDaemonModeEnv+"="+mode)
	require.NoError(t, cmd.Start())
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	require.Eventually(t, func() bool {
		rec, held := sb.Daemon()
		return held && rec.PID == cmd.Process.Pid
	}, 10*time.Second, 20*time.Millisecond, "the fake daemon never took the lock")
	return cmd
}

// exited reports whether the process cmd started has ended.
func exited(cmd *exec.Cmd) bool {
	return errors.Is(cmd.Process.Signal(syscall.Signal(0)), os.ErrProcessDone)
}

func TestStopDaemon_StopsItAndToleratesNone(t *testing.T) {
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	require.NoError(t, sb.StopDaemon(), "no daemon running is fine")

	cmd := startFakeDaemon(t, sb)
	require.NoError(t, sb.StopDaemon())
	_, held := sb.Daemon()
	assert.False(t, held, "StopDaemon waits until the daemon has let go of its lock")
	assert.Eventually(t, func() bool { return exited(cmd) }, 5*time.Second, 20*time.Millisecond)
}

func TestStop_StopsTheDaemonButStopDriverKeepsIt(t *testing.T) {
	sb := driverSandbox(t)
	cmd := startFakeDaemon(t, sb)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo ready; exec cat"}}))
	require.NoError(t, sb.WaitFor("ready", 5*time.Second))

	require.NoError(t, sb.StopDriver(300*time.Millisecond))
	assert.False(t, sb.driverExists())
	_, held := sb.Daemon()
	assert.True(t, held, "StopDriver quits the TUI only, as a real quit does")

	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo again; exec cat"}}))
	require.NoError(t, sb.WaitFor("again", 5*time.Second))
	require.NoError(t, sb.Stop(300*time.Millisecond))
	assert.False(t, sb.driverExists())
	_, held = sb.Daemon()
	assert.False(t, held, "Stop ends the daemon too, so the next start boots a fresh one")
	assert.Eventually(t, func() bool { return exited(cmd) }, 5*time.Second, 20*time.Millisecond)
}

func TestStart_RestartStopsTheDaemon(t *testing.T) {
	sb := driverSandbox(t)
	cmd := startFakeDaemon(t, sb)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo first; exec cat"}, Restart: true}))
	require.NoError(t, sb.WaitFor("first", 5*time.Second))
	assert.Eventually(t, func() bool { return exited(cmd) }, 5*time.Second, 20*time.Millisecond,
		"a restart starts the dev loom afresh, daemon included")
}

func TestDown_StopsTheDaemonFirst(t *testing.T) {
	useTempBase(t)
	sb, err := Open(fmt.Sprintf("t%d", time.Now().UnixNano()))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(sb.RepoDir(), 0o755))
	cmd := startFakeDaemon(t, sb)
	require.NoError(t, sb.Down())
	assert.NoDirExists(t, sb.Dir)
	assert.Eventually(t, func() bool { return exited(cmd) }, 5*time.Second, 20*time.Millisecond,
		"a daemon must never outlive its sandbox")
}

// stopTimeout shortens the wait for a daemon to stop for one test.
func stopTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := daemonStopTimeout
	daemonStopTimeout = d
	t.Cleanup(func() { daemonStopTimeout = prev })
}

// A daemon that won't stop, or a loom from before the daemon holding the
// lock, stops Down with nothing removed, and its error names the way past
// it; ForceDown kills that process, a build of the sandbox's own, and
// removes the sandbox.
func TestDown_AProcessThatWontStopNeedsForce(t *testing.T) {
	stopTimeout(t, 300*time.Millisecond)
	for _, mode := range []string{fakeStubborn, fakePreDaemon} {
		t.Run(mode, func(t *testing.T) {
			useTempBase(t)
			sb, err := Open(fmt.Sprintf("t%d", time.Now().UnixNano()))
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(sb.RepoDir(), 0o755))
			cmd := startFakeDaemonAs(t, sb, sandboxBuild(t, sb), mode)

			err = sb.Down()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "loomdev down --force")
			assert.DirExists(t, sb.Dir, "nothing removed")
			assert.False(t, exited(cmd))

			require.NoError(t, sb.ForceDown())
			assert.NoDirExists(t, sb.Dir)
			assert.Eventually(t, func() bool { return exited(cmd) }, 5*time.Second, 20*time.Millisecond)
		})
	}
}

// ForceDown kills only a build of the sandbox's own: a process holding its
// lock from elsewhere (a stale record's reused pid, say) is left running,
// and the sandbox with it.
func TestForceDown_KillsNoProcessButTheSandboxsLoom(t *testing.T) {
	stopTimeout(t, 300*time.Millisecond)
	useTempBase(t)
	sb, err := Open(fmt.Sprintf("t%d", time.Now().UnixNano()))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(sb.RepoDir(), 0o755))
	sandboxBuild(t, sb) // a build is there, but the holder isn't it
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := startFakeDaemonAs(t, sb, exe, fakeStubborn)

	err = sb.ForceDown()
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("pid %d holds the sandbox's lock but runs no build in", cmd.Process.Pid))
	assert.DirExists(t, sb.Dir)
	assert.Never(t, func() bool { return exited(cmd) }, 300*time.Millisecond, 20*time.Millisecond,
		"a process not the sandbox's own is never killed")
}

// KillDaemon kills a build of the sandbox's own as a crash does, reports
// the record it killed, and refuses a holder from elsewhere or none.
func TestKillDaemon_KillsASandboxBuildAndNoOther(t *testing.T) {
	useTempBase(t)
	sb, err := Open(fmt.Sprintf("t%d", time.Now().UnixNano()))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(sb.GlobalDir(), 0o755))
	_, err = sb.KillDaemon()
	require.ErrorIs(t, err, ErrNoDaemon)

	exe, err := os.Executable()
	require.NoError(t, err)
	foreign := startFakeDaemonAs(t, sb, exe, "")
	rec, err := sb.KillDaemon()
	require.Error(t, err)
	assert.Equal(t, foreign.Process.Pid, rec.PID)
	assert.Contains(t, err.Error(), "runs no build in")
	assert.Never(t, func() bool { return exited(foreign) }, 300*time.Millisecond, 20*time.Millisecond)
	require.NoError(t, foreign.Process.Kill())
	require.Eventually(t, func() bool { _, held := sb.Daemon(); return !held }, 5*time.Second, 20*time.Millisecond)

	own := startFakeDaemonAs(t, sb, sandboxBuild(t, sb), "")
	rec, err = sb.KillDaemon()
	require.NoError(t, err)
	assert.Equal(t, own.Process.Pid, rec.PID)
	assert.Eventually(t, func() bool { return exited(own) }, 5*time.Second, 20*time.Millisecond)
	_, held := sb.Daemon()
	assert.False(t, held, "the lock of a killed daemon is free")
}

func TestList_ReportsTheDaemon(t *testing.T) {
	useTempBase(t)
	for _, name := range []string{"idle", "served"} {
		sb, err := Open(name)
		require.NoError(t, err)
		require.NoError(t, sb.saveMeta(&Meta{Name: name, Socket: sb.Socket(), CreatedAt: time.Now()}))
		require.NoError(t, os.MkdirAll(sb.GlobalDir(), 0o755))
	}
	served, err := Open("served")
	require.NoError(t, err)
	cmd := startFakeDaemon(t, served)

	infos, err := List()
	require.NoError(t, err)
	require.Len(t, infos, 2)
	assert.Nil(t, infos[0].Daemon, "no daemon holds idle's lock")
	require.NotNil(t, infos[1].Daemon)
	assert.Equal(t, cmd.Process.Pid, infos[1].Daemon.PID)
	assert.Equal(t, filepath.Join(served.GlobalDir(), "fake.sock"), infos[1].Daemon.Socket)
}

func TestWithDriver_ValidatesTheName(t *testing.T) {
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	assert.Equal(t, DriverSession, sb.Driver())
	for _, bad := range []string{"", "a:b", "a.b", "Upper", "loom_x", "claudesquad_x", "-lead"} {
		_, err := sb.WithDriver(bad)
		assert.Error(t, err, bad)
	}
	second, err := sb.WithDriver("second")
	require.NoError(t, err)
	assert.Equal(t, "second", second.Driver())
	assert.Equal(t, sb.Dir, second.Dir)
	assert.Equal(t, DriverSession, sb.Driver(), "the original keeps its driver")
}

func TestWithDriver_TwoDriversShareTheServer(t *testing.T) {
	sb := driverSandbox(t)
	second, err := sb.WithDriver("second")
	require.NoError(t, err)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo one; exec cat"}}))
	require.NoError(t, second.Start(StartOptions{Command: []string{"sh", "-c", "echo two; exec cat"}}))
	require.NoError(t, sb.WaitFor("one", 5*time.Second))
	require.NoError(t, second.WaitFor("two", 5*time.Second))

	require.NoError(t, second.SendText("typed-in-two"))
	require.NoError(t, second.SendKeys("Enter"))
	require.NoError(t, second.WaitFor("typed-in-two", 5*time.Second))
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotContains(t, screen, "typed-in-two", "keys go to their own driver")

	require.NoError(t, second.StopDriver(300*time.Millisecond))
	assert.False(t, second.DriverRunning())
	assert.True(t, sb.DriverRunning(), "stopping one driver leaves the other")
}

func TestText_JoinsWrappedLines(t *testing.T) {
	sb := driverSandbox(t)
	long := strings.Repeat("abcdefghij", 6)
	require.NoError(t, sb.Start(StartOptions{Width: 20, Height: 10, Command: []string{"sh", "-c", "echo " + long + "; exec cat"}}))
	require.NoError(t, sb.WaitFor("abcdefghij", 5*time.Second))
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	require.NotContains(t, screen, long, "the pane wraps the line")
	text, err := sb.Text()
	require.NoError(t, err)
	assert.Contains(t, text, long)
}

func TestCmd_RunsInTheSandbox(t *testing.T) {
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(sb.RepoDir(), 0o755))
	out, err := sb.Cmd("sh", "-c", `echo "$LOOM_GLOBAL_DIR"; pwd -P`).Output()
	require.NoError(t, err)
	repo, err := filepath.EvalSymlinks(sb.RepoDir())
	require.NoError(t, err)
	assert.Equal(t, sb.GlobalDir()+"\n"+repo+"\n", string(out))
}

func TestDown_RemovesTheSocketsLeftOutsideIt(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo ready; exec cat"}}))
	require.NoError(t, sb.WaitFor("ready", 5*time.Second))
	out, err := sb.runTmux("display-message", "-p", "#{socket_path}")
	require.NoError(t, err)
	tmuxSocket := strings.TrimSpace(out)
	require.FileExists(t, tmuxSocket)

	// A daemon killed outright: its lock is free, its record and socket
	// stay. The record is rewritten to put the socket outside the sandbox,
	// as a real daemon's is.
	cmd := startFakeDaemon(t, sb)
	require.NoError(t, cmd.Process.Kill())
	require.Eventually(t, func() bool { _, held := sb.Daemon(); return !held }, 5*time.Second, 20*time.Millisecond)
	daemonSocket := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", daemonSocket)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	rec, _ := sb.Daemon()
	rec.Socket = daemonSocket
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(daemon.LockPath(sb.GlobalDir()), data, 0o644))

	require.NoError(t, sb.Down())
	assert.NoFileExists(t, daemonSocket, "the dead daemon's socket")
	assert.NoFileExists(t, tmuxSocket, "the tmux server's socket")
}
