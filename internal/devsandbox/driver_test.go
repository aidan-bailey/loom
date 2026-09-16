package devsandbox

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func driverSandbox(t *testing.T) *Sandbox {
	t.Helper()
	requireTmux(t)
	useTempBase(t)
	sb, err := Open(fmt.Sprintf("t%d", time.Now().UnixNano()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sb.Down() })
	require.NoError(t, os.MkdirAll(sb.RepoDir(), 0o755))
	return sb
}

func TestShellJoin(t *testing.T) {
	assert.Equal(t, `'a b' 'it'\''s' 'plain'`, shellJoin([]string{"a b", "it's", "plain"}))
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, `'it'\''s'`, ShellQuote("it's"))
	assert.Equal(t, "''", ShellQuote(""))
}

func TestDriver_KeysReachThePane(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo ready; exec cat"}}))
	require.NoError(t, sb.WaitFor("ready", 5*time.Second))
	assert.True(t, sb.DriverRunning())

	require.NoError(t, sb.SendText("hello-driver"))
	require.NoError(t, sb.SendKeys("Enter"))
	require.NoError(t, sb.WaitFor("hello-driver", 5*time.Second))
}

func TestDriver_EnvReachesThePane(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo sock=$LOOM_TMUX_SOCKET; exec cat"}}))
	require.NoError(t, sb.WaitFor("sock="+sb.Socket(), 5*time.Second))
}

func TestDriver_StartIsIdempotentUnlessRestart(t *testing.T) {
	sb := driverSandbox(t)
	cmd := []string{"sh", "-c", "echo pid=$$; exec cat"}
	require.NoError(t, sb.Start(StartOptions{Command: cmd}))
	require.NoError(t, sb.WaitFor("pid=", 5*time.Second))
	first, err := sb.Screen(false)
	require.NoError(t, err)

	require.NoError(t, sb.Start(StartOptions{Command: cmd}))
	again, err := sb.Screen(false)
	require.NoError(t, err)
	assert.Equal(t, first, again, "a second Start must not replace a running driver")

	require.NoError(t, sb.Start(StartOptions{Command: cmd, Restart: true}))
	require.NoError(t, sb.WaitFor("pid=", 5*time.Second))
	restarted, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotEqual(t, first, restarted)
}

func TestDriver_SizeIsApplied(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Width: 100, Height: 30, Command: []string{"sh", "-c", "sleep 0.2; stty size; exec cat"}}))
	require.NoError(t, sb.WaitFor("30 100", 5*time.Second))
}

func TestDriver_LiveScreenExcludesScrolledContent(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Height: 5, Command: []string{"sh", "-c", "for i in 1 2 3 4 5 6 7 8; do echo line$i; done; exec cat"}}))
	require.NoError(t, sb.WaitFor("line8", 5*time.Second))
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotContains(t, screen, "line3", "a live pane's Screen must show only the current screen, not scrollback")
}

func TestDriver_DeadPaneKeptForDiagnosis(t *testing.T) {
	sb := driverSandbox(t)
	// The short sleep keeps the exit after remain-on-exit is applied.
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo boom; sleep 0.3; exit 3"}}))
	require.NoError(t, sb.WaitFor("boom", 5*time.Second))
	require.Eventually(t, func() bool { return !sb.DriverRunning() }, 5*time.Second, 50*time.Millisecond)
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.Contains(t, screen, "boom", "remain-on-exit keeps the crash output capturable")

	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo fresh; exec cat"}}))
	require.NoError(t, sb.WaitFor("fresh", 5*time.Second), "Start replaces a dead driver")
}

func TestDriver_StopUsesQuitKeyFirst(t *testing.T) {
	sb := driverSandbox(t)
	// Exits on the first byte it reads, like loom exiting on `q`.
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "stty -icanon -echo; echo ready; head -c1 >/dev/null"}}))
	require.NoError(t, sb.WaitFor("ready", 5*time.Second))
	start := time.Now()
	require.NoError(t, sb.Stop(5*time.Second))
	assert.Less(t, time.Since(start), 4*time.Second, "q should end the program well before the grace period")
	assert.False(t, sb.driverExists())
}

func TestDriver_StopKillsAStubbornProgram(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo ready; exec cat"}}))
	require.NoError(t, sb.WaitFor("ready", 5*time.Second))
	require.NoError(t, sb.Stop(300*time.Millisecond))
	assert.False(t, sb.driverExists())
	require.NoError(t, sb.Stop(time.Second), "stopping a stopped driver is a no-op")
}

func TestDriver_WaitForTimeoutCarriesScreen(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo visible; exec cat"}}))
	require.NoError(t, sb.WaitFor("visible", 5*time.Second))
	err := sb.WaitFor("never-there", 300*time.Millisecond)
	var timeout *WaitTimeoutError
	require.True(t, errors.As(err, &timeout))
	assert.Equal(t, "never-there", timeout.Text)
	assert.Contains(t, timeout.Screen, "visible")
}
