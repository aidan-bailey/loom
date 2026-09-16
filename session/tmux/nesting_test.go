package tmux

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckNesting(t *testing.T) {
	const tmuxEnv = "/tmp/tmux-1000/default,123,0"
	// sandboxTmuxEnv builds a $TMUX value naming a private socket, as every
	// pane of a server started with LOOM_TMUX_SOCKET=<socket> would see
	// (the server copies the launching client's environment).
	sandboxTmuxEnv := func(socket string) string {
		return "/tmp/tmux-1000/" + socket + ",456,0"
	}
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
		wantSession string // "" means no error
	}{
		{"not inside tmux", map[string]string{}, mustNotLookup, ""},
		{"inside a loom agent session", map[string]string{"TMUX": tmuxEnv}, lookup("loom_feature", nil), "loom_feature"},
		{"inside a loom terminal pane", map[string]string{"TMUX": tmuxEnv}, lookup("loom_term_feature", nil), "loom_term_feature"},
		{"inside a legacy session", map[string]string{"TMUX": tmuxEnv}, lookup("claudesquad_old", nil), "claudesquad_old"},
		{"inside unrelated tmux", map[string]string{"TMUX": tmuxEnv}, lookup("work", nil), ""},
		{"lookup fails", map[string]string{"TMUX": tmuxEnv}, lookup("", errors.New("no server")), ""},
		{"private socket differs from enclosing server", map[string]string{"TMUX": tmuxEnv, EnvTmuxSocket: "loomdev-x"}, mustNotLookup, ""},
		{
			"private socket matches enclosing server, loom session → refused",
			map[string]string{"TMUX": sandboxTmuxEnv("loomdev-x"), EnvTmuxSocket: "loomdev-x"},
			lookup("loom_feature", nil), "loom_feature",
		},
		{
			"private socket matches enclosing server, driver session → allowed",
			map[string]string{"TMUX": sandboxTmuxEnv("loomdev-x"), EnvTmuxSocket: "loomdev-x"},
			lookup("dev-driver", nil), "",
		},
		{
			`LOOM_TMUX_SOCKET="default" matches the host's default socket → refused`,
			map[string]string{"TMUX": "/run/user/1000/tmux-1000/default,789,0", EnvTmuxSocket: "default"},
			lookup("loom_feature", nil), "loom_feature",
		},
		{"explicit override", map[string]string{"TMUX": tmuxEnv, EnvAllowNested: "1"}, mustNotLookup, ""},
		{"override needs the value 1", map[string]string{"TMUX": tmuxEnv, EnvAllowNested: "yes"}, lookup("loom_feature", nil), "loom_feature"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			err := CheckNesting(getenv, tc.enclosing)
			if tc.wantSession == "" {
				assert.NoError(t, err)
				return
			}
			var nested *NestedError
			require.ErrorAs(t, err, &nested)
			assert.Equal(t, tc.wantSession, nested.Session)
			assert.Contains(t, err.Error(), tc.wantSession)
			assert.Contains(t, err.Error(), "go run ./tools/loomdev run")
			assert.Contains(t, err.Error(), EnvTmuxSocket)
		})
	}
}
