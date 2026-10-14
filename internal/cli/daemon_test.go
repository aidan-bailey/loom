package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd"
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
	// srv serves the commands' connections; a test closes it to lose the
	// daemon under a command.
	srv *rpc.Server
}

// deadTmux is the daemon's executor: every tmux session is dead (has-session
// fails, a listing is empty, or fails with listErr), and every other
// command runs, so the boot's auth read reaches the test's fake Claude.
type deadTmux struct{ listErr error }

func isTmux(c *exec.Cmd) bool { return filepath.Base(c.Path) == "tmux" }

func (deadTmux) Run(c *exec.Cmd) error {
	if isTmux(c) {
		return &exec.ExitError{}
	}
	return internalexec.Default{}.Run(c)
}

func (d deadTmux) Output(c *exec.Cmd) ([]byte, error) {
	if isTmux(c) {
		return nil, d.listErr
	}
	return internalexec.Default{}.Output(c)
}

func (d deadTmux) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	if isTmux(c) {
		return nil, d.listErr
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
	return serveDaemonOn(t, program, deadTmux{})
}

// serveDaemonOn is serveDaemon with ex as the daemon's executor.
func serveDaemonOn(t *testing.T, program string, ex cmd.Executor) *testDaemon {
	t.Helper()
	requireIsolatedClaude(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMUX", "")
	t.Setenv(tmux.EnvTmuxSocket, fmt.Sprintf("loomclitest-%d", time.Now().UnixNano()))
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	model := core.New(core.Options{Registry: reg, Program: program, CmdExec: ex})
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
	return &testDaemon{loop: loop, client: c, srv: srv}
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

// serveDaemonLive is serveDaemon on a private real tmux server, killed
// once the daemon has stopped, so sessions really start: their agent is
// whatever program the workspace's config names (liveWorkspace).
func serveDaemonLive(t *testing.T, program string) *testDaemon {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	var sock string
	// Registered first so it runs last: after the loop has stopped.
	t.Cleanup(func() {
		if sock != "" {
			_ = tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run()
		}
	})
	d := serveDaemonOn(t, program, internalexec.Default{})
	sock = os.Getenv(tmux.EnvTmuxSocket)
	return d
}

// fakeAgent is a stand-in Claude, named claude so loom launches it as
// one: `auth status` reports a login (by authMethod, "claude.ai" for one
// remote control accepts), any other subcommand fails at once (the
// roster's `agents`, a usage probe's -p), and a launch, with flags or
// none, stays up until its tmux session ends.
func fakeAgent(t *testing.T, authMethod string) string {
	t.Helper()
	bin := t.TempDir()
	auth := `{"loggedIn":true,"authMethod":"` + authMethod + `","email":"you@example.com","subscriptionType":"max"}`
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  auth) printf '%s\\n' '" + auth + "'; exit 0;;\n" +
		"  -p) exit 1;;\n" +
		"  ''|-*) exec sleep 3600;;\n" +
		"esac\n" +
		"exit 1\n"
	path := filepath.Join(bin, "claude")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// liveWorkspace registers, before the daemon boots, a repository with one
// commit as name, whose config.json launches program. edit, when set,
// changes the config before it is written.
func liveWorkspace(t *testing.T, name, program string, edit func(*config.Config)) config.Workspace {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "loom test")
	git("config", "user.email", "test@example.invalid")
	git("config", "commit.gpgsign", "false")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".loom/\n"), 0o644))
	git("add", "-A")
	git("commit", "-q", "--no-verify", "-m", "init")
	ws := config.Workspace{Name: name, Path: dir}
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.Add(ws.Name, ws.Path))
	cfg := config.DefaultConfig()
	cfg.DefaultProgram = program
	if edit != nil {
		edit(cfg)
	}
	require.NoError(t, config.SaveConfigTo(cfg, config.WorkspaceConfigDir(&ws)))
	return ws
}

// view is the daemon's view of the session titled title in the workspace
// served as ws, as its client has it now.
func (d *testDaemon) view(t *testing.T, ws, title string) (core.InstanceView, bool) {
	t.Helper()
	for _, v := range d.client.Views(d.workspace(t, ws).ID) {
		if v.Title == title {
			return v, true
		}
	}
	return core.InstanceView{}, false
}

// tmuxHas reports whether the agent session titled title runs on the
// test's private tmux server.
func tmuxHas(title string) bool {
	return tmux.Command(context.Background(), "has-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(title))).Run() == nil
}
