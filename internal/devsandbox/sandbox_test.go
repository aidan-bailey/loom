package devsandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaseDir_XDGThenHomeFallback(t *testing.T) {
	base := useTempBase(t)
	got, err := BaseDir()
	require.NoError(t, err)
	assert.Equal(t, base, got)

	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	got, err = BaseDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".local", "state", "loom-dev"), got)
}

func TestDefaultName(t *testing.T) {
	for in, want := range map[string]string{
		"aidanb/dev-env":       "dev-env",
		"main":                 "main",
		"feature/Fancy Thing!": "fancy-thing",
		"a/_hidden":            "hidden",
		"x/..":                 "default",
		"":                     "default",
	} {
		assert.Equal(t, want, DefaultName(in), in)
	}
}

func TestOpen_ValidatesNames(t *testing.T) {
	useTempBase(t)
	for _, bad := range []string{"", "../evil", "Bad", "-lead", "a/b"} {
		_, err := Open(bad)
		assert.Error(t, err, bad)
	}
}

func TestOpen_PathsAndEnv(t *testing.T) {
	base := useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "demo"), sb.Dir)
	assert.Equal(t, "loomdev-demo", sb.Socket())
	assert.Equal(t, filepath.Join(sb.Dir, "repo", ".loom"), sb.WorkspaceConfigDir())
	assert.Equal(t, filepath.Join(sb.Dir, "bin", "personas"), sb.PersonaDir())
	assert.Equal(t, []string{
		"LOOM_TMUX_SOCKET=loomdev-demo",
		"LOOM_GLOBAL_DIR=" + filepath.Join(sb.Dir, "global"),
		"LOOM_HOME=" + filepath.Join(sb.Dir, "home"),
	}, sb.Env())
	env := sb.Environ()
	assert.Equal(t, sb.Env(), env[len(env)-3:], "overlay must come last so it wins")
}

func TestDown_RefusesOutsideBase(t *testing.T) {
	useTempBase(t)
	victim := t.TempDir()
	sb := &Sandbox{Name: "demo", Dir: victim}
	require.Error(t, sb.Down())
	assert.DirExists(t, victim)
}

func TestDown_RemovesSandbox(t *testing.T) {
	useTempBase(t)
	// Unique name: Down kills tmux server loomdev-<name>, and socket names
	// are shared with any real sandbox on this machine.
	sb, err := Open(fmt.Sprintf("t%d", time.Now().UnixNano()))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(sb.Dir, "repo"), 0o755))
	require.NoError(t, sb.Down())
	assert.NoDirExists(t, sb.Dir)
}

func TestList_ReportsSandboxesWithMeta(t *testing.T) {
	base := useTempBase(t)
	for _, name := range []string{"beta", "alpha"} {
		sb, err := Open(name)
		require.NoError(t, err)
		require.NoError(t, sb.saveMeta(&Meta{Name: name, Socket: sb.Socket(), CreatedAt: time.Now()}))
	}
	require.NoError(t, os.WriteFile(filepath.Join(base, "stray.txt"), nil, 0o644))

	infos, err := List()
	require.NoError(t, err)
	require.Len(t, infos, 2)
	assert.Equal(t, "alpha", infos[0].Name)
	assert.Equal(t, "beta", infos[1].Name)
	assert.False(t, infos[0].ServerAlive)
	require.NotNil(t, infos[0].Meta)
	assert.Equal(t, "loomdev-alpha", infos[0].Meta.Socket)
}

func TestList_MissingBaseIsEmpty(t *testing.T) {
	useTempBase(t)
	infos, err := List()
	require.NoError(t, err)
	assert.Empty(t, infos)
}

func TestTailLogs_LastLinesPerFile(t *testing.T) {
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	path := sb.LogFiles()[0]
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644))
	out := sb.TailLogs(2)
	assert.Contains(t, out, "==> "+path+" <==\ntwo\nthree\n")
	assert.NotContains(t, out, "one")
}
