package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckNesting: a loom that sweeps tmux sessions refuses inside one of
// loom's own sessions on the server it is about to manage, however that
// server is spelled, whatever LOOM_TMUX_SOCKET says; elsewhere it runs.
func TestCheckNesting(t *testing.T) {
	const (
		users   = "/tmp/tmux-1000/default"
		sandbox = "/tmp/tmux-1000/loomdev-x"
	)
	// A socket reached through a symlinked directory: tmux resolves its
	// socket dir, a daemon may not have.
	real := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(real, "default"), nil, 0o600))
	link := filepath.Join(t.TempDir(), "tmp")
	require.NoError(t, os.Symlink(real, link))

	lookup := func(name string, err error) func() (string, error) {
		return func() (string, error) { return name, err }
	}
	mustNotLookup := func() (string, error) {
		t.Fatal("enclosing-session lookup must not run")
		return "", nil
	}
	tests := []struct {
		name        string
		env         map[string]string
		enclosing   func() (string, error)
		server      string
		wantSession string // "" means no error
	}{
		{"not inside tmux", map[string]string{}, mustNotLookup, users, ""},
		{"inside a loom agent session", map[string]string{"TMUX": users + ",123,0"}, lookup("loom_feature", nil), users, "loom_feature"},
		{"inside a loom terminal pane", map[string]string{"TMUX": users + ",123,0"}, lookup("loom_term_feature", nil), users, "loom_term_feature"},
		{"inside a legacy session", map[string]string{"TMUX": users + ",123,0"}, lookup("claudesquad_old", nil), users, "claudesquad_old"},
		{"inside unrelated tmux", map[string]string{"TMUX": users + ",123,0"}, lookup("work", nil), users, ""},
		{"the server managed is another", map[string]string{"TMUX": users + ",123,0"}, mustNotLookup, sandbox, ""},
		{
			// The pin keeps the last daemon's server, the enclosing one,
			// whatever this environment's private socket names.
			"a private socket the pin ignores waves nothing through",
			map[string]string{"TMUX": users + ",123,0", EnvTmuxSocket: "loomdev-x"},
			lookup("loom_feature", nil), users, "loom_feature",
		},
		{
			"a sandbox's driver session, on the sandbox's server",
			map[string]string{"TMUX": sandbox + ",456,0", EnvTmuxSocket: "loomdev-x"},
			lookup("dev-driver", nil), sandbox, "",
		},
		{"the server spelled otherwise", map[string]string{"TMUX": "/tmp//tmux-1000/./default,123,0"}, lookup("loom_feature", nil), users, "loom_feature"},
		{"the server through a symlink", map[string]string{"TMUX": filepath.Join(real, "default") + ",123,0"},
			lookup("loom_feature", nil), filepath.Join(link, "default"), "loom_feature"},
		{"explicit override", map[string]string{"TMUX": users + ",123,0", EnvAllowNested: "1"}, mustNotLookup, users, ""},
		{"override needs the value 1", map[string]string{"TMUX": users + ",123,0", EnvAllowNested: "yes"}, lookup("loom_feature", nil), users, "loom_feature"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			err := CheckNesting(getenv, tc.enclosing, tc.server)
			if tc.wantSession == "" {
				assert.NoError(t, err)
				return
			}
			var nested *NestedError
			require.ErrorAs(t, err, &nested)
			assert.Equal(t, tc.wantSession, nested.Session)
			assert.Contains(t, err.Error(), tc.wantSession)
			assert.Contains(t, err.Error(), "go run ./tools/loomdev run")
			assert.NotContains(t, err.Error(), EnvTmuxSocket, "a private socket is no way past it")
		})
	}
}

// A session it can't name on the server it would manage (tmux timing out
// under load) may be one of loom's: the check fails closed, and says how
// to pass when it isn't.
func TestCheckNesting_FailsClosedWhenTheSessionCantBeNamed(t *testing.T) {
	const users = "/tmp/tmux-1000/default"
	getenv := func(k string) string { return map[string]string{"TMUX": users + ",123,0"}[k] }
	err := CheckNesting(getenv, func() (string, error) { return "", errors.New("timed out") }, users)
	var nested *NestedError
	require.ErrorAs(t, err, &nested)
	assert.ErrorContains(t, nested.Lookup, "timed out")
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, err.Error(), EnvAllowNested+"=1")
	assert.Contains(t, err.Error(), "go run ./tools/loomdev run")

	// Off that server, nothing is looked up.
	assert.NoError(t, CheckNesting(getenv, func() (string, error) {
		t.Fatal("enclosing-session lookup must not run")
		return "", nil
	}, "/tmp/tmux-1000/loomdev-x"))
}
