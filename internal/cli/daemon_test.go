package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/require"
)

// testDaemon is a model served in this process as the daemon serves it,
// which the commands join (Join): booted over the test's disk (its global
// dir's registries and workspaces), on a production loop (its jobs run,
// its probes publish), with every tmux session dead. client is a
// connection of the test's own, for reading what the daemon serves.
type testDaemon struct {
	loop   *core.Loop
	client *rpc.Client
}

// deadTmux is the daemon's executor: every tmux session is dead (has-session
// fails, a listing is empty), and every other command runs, so the boot's
// auth read reaches the test's fake Claude.
type deadTmux struct{}

func isTmux(c *exec.Cmd) bool { return filepath.Base(c.Path) == "tmux" }

func (deadTmux) Run(c *exec.Cmd) error {
	if isTmux(c) {
		return &exec.ExitError{}
	}
	return internalexec.Default{}.Run(c)
}

func (deadTmux) Output(c *exec.Cmd) ([]byte, error) {
	if isTmux(c) {
		return nil, nil
	}
	return internalexec.Default{}.Output(c)
}

func (deadTmux) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	if isTmux(c) {
		return nil, nil
	}
	return internalexec.Default{}.CombinedOutput(c)
}

// serveDaemon starts a testDaemon whose agent program is program (the
// accounts' Claude commands run with it) and points Join at it until the
// test ends. Call it once the test's disk is laid out: the model boots
// from it. It refuses a CLAUDE_CONFIG_DIR that is unset or under the
// developer's home (TestMain's isolation sets it, a fixture may set its
// own), and gives the test a HOME of its own: a main config dir the daemon
// can't learn from its Claude falls back to them, and an account linked
// against the developer's ~/.claude lets a test's write through a link
// land there.
func serveDaemon(t *testing.T, program string) *testDaemon {
	t.Helper()
	requireIsolatedClaude(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMUX", "")
	t.Setenv(tmux.EnvTmuxSocket, fmt.Sprintf("loomclitest-%d", time.Now().UnixNano()))
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	model := core.New(core.Options{Registry: reg, Program: program, CmdExec: deadTmux{}})
	model.Boot()
	loop := core.Start(model)
	srv := rpc.NewServer(loop)
	loop.Begin()
	dial := func() (*rpc.Client, error) {
		a, b := net.Pipe()
		srv.Serve(a)
		return rpc.Dial(b)
	}
	orig := Join
	Join = dial
	c, err := dial()
	require.NoError(t, err)
	t.Cleanup(func() {
		Join = orig
		c.Close()
		srv.Close()
		loop.Stop()
	})
	return &testDaemon{loop: loop, client: c}
}

// workspaces are the names of the workspaces the daemon serves, "" the
// global one, as its client has them now.
func (d *testDaemon) workspaces(t *testing.T) []string {
	t.Helper()
	require.NoError(t, d.client.FlushForTest())
	d.client.Sync()
	var names []string
	for _, v := range d.client.Workspaces() {
		names = append(names, v.Name)
	}
	return names
}

// workspace is the view of the workspace the daemon serves as name.
func (d *testDaemon) workspace(t *testing.T, name string) core.WorkspaceView {
	t.Helper()
	require.NoError(t, d.client.FlushForTest())
	d.client.Sync()
	for _, v := range d.client.Workspaces() {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("the daemon serves no workspace %q", name)
	return core.WorkspaceView{}
}

// requireIsolatedClaude fails the test unless CLAUDE_CONFIG_DIR names a
// dir outside the developer's ~/.claude and ~/.loom.
func requireIsolatedClaude(t *testing.T) {
	t.Helper()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	require.NotEmpty(t, dir, "CLAUDE_CONFIG_DIR is isolated (testenv.IsolateLoomDirs)")
	for _, real := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".loom")} {
		require.False(t, within(real, dir), "CLAUDE_CONFIG_DIR %s is under the real %s", dir, real)
	}
}

// within reports whether p is dir or lies beneath it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
