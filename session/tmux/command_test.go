package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommand_DefaultServerAddsNoSocket(t *testing.T) {
	t.Setenv(EnvTmuxSocket, "")
	cmd := Command(context.Background(), "has-session", "-t=loom_x")
	assert.Equal(t, []string{"tmux", "-u", "has-session", "-t=loom_x"}, cmd.Args)
}

func TestCommand_PrivateSocketPrependsL(t *testing.T) {
	t.Setenv(EnvTmuxSocket, "loomdev-demo")
	cmd := Command(context.Background(), "ls")
	assert.Equal(t, []string{"tmux", "-u", "-L", "loomdev-demo", "ls"}, cmd.Args)
}

func TestCommandOnSocket_IgnoresEnv(t *testing.T) {
	t.Setenv(EnvTmuxSocket, "from-env")
	cmd := CommandOnSocket(context.Background(), "explicit", "ls")
	assert.Equal(t, []string{"tmux", "-u", "-L", "explicit", "ls"}, cmd.Args)
}

func TestCommandOnSocket_DoesNotMutateCallerArgs(t *testing.T) {
	args := make([]string, 1, 8) // spare capacity would expose an in-place append
	args[0] = "ls"
	_ = CommandOnSocket(context.Background(), "s", args...)
	assert.Equal(t, []string{"ls"}, args)
}

// TestCommand_ListingSurvivesANonUTF8Client_RealTmux pins -u. tmux prints a
// command-line client's output through utf8_sanitize, which turns every
// byte outside printable ASCII into '_', unless the client is UTF-8: $TMUX
// set (even empty) or a UTF-8 LC_ALL/LC_CTYPE/LANG. Outside tmux under no
// UTF-8 locale (a service, a bare SSH login, the Nix build sandbox) a
// "#{session_name}\t#{session_path}" listing came back as one field, and the
// held-name guard, finding no session of its name, killed another
// workspace's.
func TestCommand_ListingSurvivesANonUTF8Client_RealTmux(t *testing.T) {
	privateTmux(t, "u8")
	for _, v := range []string{"TMUX", "LC_ALL", "LC_CTYPE", "LANG"} {
		t.Setenv(v, "") // restores the variable when the test ends
		require.NoError(t, os.Unsetenv(v))
	}
	dir := filepath.Join(t.TempDir(), "café")
	require.NoError(t, os.Mkdir(dir, 0o755))
	out, err := Command(context.Background(), "new-session", "-d", "-s", "loom_u8", "-c", dir, "sleep 300").CombinedOutput()
	require.NoError(t, err, "%s", out)

	out, err = Command(context.Background(), "ls", "-F", "#{session_name}\t#{session_path}").Output()
	require.NoError(t, err)
	name, path, ok := strings.Cut(strings.TrimSpace(string(out)), "\t")
	require.True(t, ok, "the tab survives: %q", out)
	assert.Equal(t, "loom_u8", name)
	assert.Equal(t, "café", filepath.Base(path), "and so does a non-ASCII path")
}

func TestCommand_BoundToContext(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, Command(ctx, "ls").Run(), context.Canceled)
}

func TestEnclosingSessionName_AsksTheTmuxEnvServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	sock := fmt.Sprintf("loomtest-encl-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = CommandOnSocket(ctx, sock, "kill-server").Run() })
	require.NoError(t, CommandOnSocket(ctx, sock, "new-session", "-d", "-s", "loom_term_probe", "sleep 60").Run())
	path, err := CommandOnSocket(ctx, sock, "display-message", "-p", "-t", "loom_term_probe", "#{socket_path}").Output()
	require.NoError(t, err)
	pane, err := CommandOnSocket(ctx, sock, "display-message", "-p", "-t", "loom_term_probe", "#{pane_id}").Output()
	require.NoError(t, err)

	t.Setenv("TMUX", strings.TrimSpace(string(path))+",1,0")
	t.Setenv("TMUX_PANE", strings.TrimSpace(string(pane)))
	t.Setenv(EnvTmuxSocket, "ignored-by-design") // the question is about the enclosing server

	name, err := EnclosingSessionName()
	require.NoError(t, err)
	assert.Equal(t, "loom_term_probe", name)
}

// TestUseServer_PinsEveryCommand: once a server is pinned, every Command
// reaches it by its socket's path, whatever LOOM_TMUX_SOCKET says; an
// explicit socket (CommandOnSocket) still reaches its own, and "" lifts
// the pin.
func TestUseServer_PinsEveryCommand(t *testing.T) {
	ctx := context.Background()
	t.Setenv(EnvTmuxSocket, "from-env")
	UseServer("/run/user/1000/tmux-1000/default")
	t.Cleanup(func() { UseServer("") })

	assert.Equal(t, []string{"tmux", "-u", "-S", "/run/user/1000/tmux-1000/default", "ls"}, Command(ctx, "ls").Args)
	assert.Equal(t, []string{"tmux", "-u", "-S", "/run/user/1000/tmux-1000/default", "attach-session", "-t", "=loom_x"},
		NewTmuxSession("x", "sh").FullScreenAttachCmd().Args, "the full-screen attach too")
	assert.Equal(t, []string{"tmux", "-u", "-L", "explicit", "ls"}, CommandOnSocket(ctx, "explicit", "ls").Args)

	UseServer("")
	assert.Equal(t, []string{"tmux", "-u", "-L", "from-env", "ls"}, Command(ctx, "ls").Args)
}

// TestResolveServer: the socket path is the one tmux itself would use in
// this environment: LOOM_TMUX_SOCKET's socket, else $TMUX's server, else
// the default socket, in tmux-<uid> under an absolute $TMUX_TMPDIR, else
// under /tmp.
func TestResolveServer(t *testing.T) {
	dir := func(base string) string { return filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid())) }
	for _, tc := range []struct {
		name                 string
		socket, tmux, tmpdir string
		want                 string
	}{
		{"a private socket", "loomdev-x", "/tmp/tmux-1000/default,1,0", "/run/user/1000", filepath.Join(dir("/run/user/1000"), "loomdev-x")},
		{"a private socket, no tmpdir", "loomdev-x", "", "", filepath.Join(dir("/tmp"), "loomdev-x")},
		{"inside tmux", "", "/run/user/1000/tmux-1000/work,4242,0", "/elsewhere", "/run/user/1000/tmux-1000/work"},
		{"the default socket", "", "", "/run/user/1000", filepath.Join(dir("/run/user/1000"), "default")},
		{"a relative tmpdir is ignored, as tmux does", "", "", "rel", filepath.Join(dir("/tmp"), "default")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvTmuxSocket, tc.socket)
			t.Setenv("TMUX", tc.tmux)
			t.Setenv("TMUX_TMPDIR", tc.tmpdir)
			got, err := ResolveServer()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("a $TMUX naming no server", func(t *testing.T) {
		t.Setenv(EnvTmuxSocket, "")
		t.Setenv("TMUX", "garbage")
		_, err := ResolveServer()
		assert.Error(t, err)
	})
}

// TestResolveServer_IsTheServerTmuxUses_RealTmux: the resolved path is the
// socket of the server a plain `tmux -L` reaches, and a command pinned to
// it reaches that server with no LOOM_TMUX_SOCKET at all.
func TestResolveServer_IsTheServerTmuxUses_RealTmux(t *testing.T) {
	privateTmux(t, "rs")
	newRawSession(t, "loom_probe", "sleep 60")
	out, err := Command(context.Background(), "display-message", "-p", "-t", PaneTarget("loom_probe"), "#{socket_path}").Output()
	require.NoError(t, err)

	path, err := ResolveServer()
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	UseServer(path)
	t.Cleanup(func() { UseServer("") })
	t.Setenv(EnvTmuxSocket, "")
	assert.NoError(t, Command(context.Background(), "has-session", "-t", SessionTarget("loom_probe")).Run())
}
