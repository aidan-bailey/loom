package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendKeysMatchesPTYWrite_RealTmux pins what lets session lifecycle
// type into a session with no attach client. For the same text,
// `send-keys -l` (Session.TypeText/PressKeys) delivers exactly the bytes to
// the agent's terminal that a write to an attach client's PTY delivers
// (TmuxSession.SendKeys/TapEnter), including leading dashes, quotes, tabs,
// newlines and multi-byte UTF-8. Probed on tmux 3.7b on 2026-10-03; CI
// runs 3.6a.
func TestSendKeysMatchesPTYWrite_RealTmux(t *testing.T) {
	privateTmux(t, "sk")
	text := "-leading dash -- \"quotes\" $HOME \\back\ttab ünïcødé 🙂\nsecond line"
	want := text + "\rD\r"
	dir := t.TempDir()

	// recorder starts a session with a raw terminal whose program writes
	// every byte it receives to a file, and returns that file.
	recorder := func(title string) string {
		out := filepath.Join(dir, title+".bin")
		newRawSession(t, ToLoomTmuxName(title), "stty raw -echo; exec cat > "+out)
		waitForPaneCommand(t, ToLoomTmuxName(title), "cat")
		return out
	}

	viaKeys := recorder("sk-keys")
	s := NewSession("sk-keys", "cat")
	require.NoError(t, s.TypeText(text))
	require.NoError(t, s.PressKeys("Enter"))
	require.NoError(t, s.PressKeys("D", "Enter"))

	viaPTY := recorder("sk-pty")
	client := NewTmuxSession("sk-pty", "cat")
	require.NoError(t, client.Restore())
	t.Cleanup(func() { _ = client.PausePreview() })
	waitForClient(t, ToLoomTmuxName("sk-pty"))
	require.NoError(t, client.SendKeys(text))
	require.NoError(t, client.TapEnter())
	require.NoError(t, client.SendKeys("D"))
	require.NoError(t, client.TapEnter())

	assert.Equal(t, want, waitForBytes(t, viaKeys, len(want)), "send-keys")
	assert.Equal(t, want, waitForBytes(t, viaPTY, len(want)), "PTY write")
}

// waitForPaneCommand waits until the session's active pane runs command,
// so input is not typed into the shell before it execs.
func waitForPaneCommand(t *testing.T, name, command string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := Command(context.Background(), "display-message", "-p", "-t", PaneTarget(name), "#{pane_current_command}").Output()
		if strings.TrimSpace(string(out)) == command {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session %s never ran %s", name, command)
}

// waitForClient waits until an attach client is attached to the session.
// Bytes written to a client's PTY before it has put its terminal in raw
// mode would be cooked by the line discipline, which reads CR as NL.
func waitForClient(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := Command(context.Background(), "list-clients", "-t", SessionTarget(name), "-F", "#{client_tty}").Output()
		if strings.TrimSpace(string(out)) != "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no client ever attached to %s", name)
}

// waitForBytes waits until path holds at least n bytes, then returns them.
func waitForBytes(t *testing.T, path string, n int) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		if len(b) >= n || time.Now().After(deadline) {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
