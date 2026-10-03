package tmux

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clientSessions lists the session each client of the test's private
// server is attached to.
func clientSessions(t *testing.T) []string {
	t.Helper()
	out, err := Command(context.Background(), "list-clients", "-F", "#{client_session}").Output()
	if err != nil {
		return nil // no server: no clients
	}
	return strings.Fields(string(out))
}

// TestKilledSessionsClientExits_RealTmux: with tmux's detach-on-destroy
// off, a common global setting, a client whose session is destroyed is
// switched to another session instead of exiting. A pane client keyed by
// the dead session's name would then show the other session (possibly
// another agent's) and pass it inline-attach keys, and its pump would
// never read the EOF that tells the TUI the session is gone. loom sets
// detach-on-destroy on per session, which wins over the global setting:
// Session.Start for the sessions it launches, and Restore before attaching
// to one an older loom launched without it.
func TestKilledSessionsClientExits_RealTmux(t *testing.T) {
	for _, tc := range []struct {
		name   string
		launch func(t *testing.T) string // returns the session name
	}{
		{"launched by Session.Start", func(t *testing.T) string {
			s := NewSession("victim", "sh")
			require.NoError(t, s.Start(t.TempDir()))
			return s.SessionName()
		}},
		{"launched by an older loom", func(t *testing.T) string {
			name := ToLoomTmuxName("victim")
			newRawSession(t, name, "sh")
			return name
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			privateTmux(t, "dod")
			victim := tc.launch(t)
			out, err := Command(context.Background(), "set-option", "-g", "detach-on-destroy", "off").CombinedOutput()
			require.NoError(t, err, "set the global option: %s", out)

			client := NewAttachClient(victim, "sh")
			require.NoError(t, client.Restore())
			t.Cleanup(func() { _ = client.PausePreview() })
			require.Eventually(t, func() bool {
				return len(clientSessions(t)) == 1 && clientSessions(t)[0] == victim
			}, 3*time.Second, 20*time.Millisecond, "fixture: the client is attached to the victim")

			other := NewSession("other", "sh")
			require.NoError(t, other.Start(t.TempDir()))
			t.Cleanup(func() { _ = other.Close() })

			out, err = Command(context.Background(), "kill-session", "-t", SessionTarget(victim)).CombinedOutput()
			require.NoError(t, err, "kill the victim: %s", out)

			require.Eventually(t, func() bool { return !client.Attached() }, 3*time.Second, 20*time.Millisecond,
				"the client of a destroyed session must exit, so its pump reads EOF")
			assert.NotContains(t, clientSessions(t), other.SessionName(), "and never be switched to another session")
		})
	}
}
