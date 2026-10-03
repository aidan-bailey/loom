package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTypeTextMatchesPTYWrite_RealTmux pins what lets session lifecycle
// type into a session with no attach client. For the same text,
// Session.TypeText (load-buffer + paste-buffer) and PressKeys (send-keys)
// deliver exactly the bytes to the agent's terminal that a write to an
// attach client's PTY delivers (TmuxSession.SendKeys/TapEnter). The cases
// include the two that falsified `send-keys -l -- text`: an argument
// ending in ';', which tmux parsed as a command separator, and text over
// tmux's ~16 KiB command limit. Probed on tmux 3.7b on 2026-10-03; CI
// runs 3.6a.
func TestTypeTextMatchesPTYWrite_RealTmux(t *testing.T) {
	privateTmux(t, "tt")
	dir := t.TempDir()

	for i, tc := range []struct{ name, text string }{
		{"dashes quotes tab newline and UTF-8", "-leading dash -- \"quotes\" $HOME \\back\ttab ünïcødé 🙂\nsecond line"},
		{"trailing semicolon", "abc;"},
		{"escaped trailing semicolon", `abc\;`},
		{"lone semicolon", ";"},
		{"over 16 KiB", longTypedText()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.text + "\rD\r"

			viaTmux := byteRecorder(t, dir, fmt.Sprintf("tt-tmux-%d", i))
			s := NewSession(fmt.Sprintf("tt-tmux-%d", i), "cat")
			require.NoError(t, s.TypeText(tc.text))
			require.NoError(t, s.PressKeys("Enter"))
			require.NoError(t, s.PressKeys("D", "Enter"))

			viaPTY := byteRecorder(t, dir, fmt.Sprintf("tt-pty-%d", i))
			client := NewTmuxSession(fmt.Sprintf("tt-pty-%d", i), "cat")
			require.NoError(t, client.Restore())
			t.Cleanup(func() { _ = client.PausePreview() })
			waitForClient(t, ToLoomTmuxName(fmt.Sprintf("tt-pty-%d", i)))
			require.NoError(t, client.SendKeys(tc.text))
			require.NoError(t, client.TapEnter())
			require.NoError(t, client.SendKeys("D"))
			require.NoError(t, client.TapEnter())

			assertSameBytes(t, "TypeText", want, waitForBytes(t, viaTmux, len(want)))
			assertSameBytes(t, "PTY write", want, waitForBytes(t, viaPTY, len(want)))
		})
	}
}

// longTypedText is over 17 KiB, past the ~16 KiB tmux command limit that
// `send-keys -l` hit, with multi-byte runes and ';' throughout, and it
// ends in ';'.
func longTypedText() string {
	var b strings.Builder
	for b.Len() <= 17*1024 {
		b.WriteString("ünïcødé 🙂 line; with \"quotes\", a\ttab and $HOME\n")
	}
	b.WriteString("end;")
	return b.String()
}

// byteRecorder starts a session called ToLoomTmuxName(title) with a raw
// terminal whose program writes every byte it receives to a file, and
// returns that file.
func byteRecorder(t *testing.T, dir, title string) string {
	t.Helper()
	out := filepath.Join(dir, title+".bin")
	newRawSession(t, ToLoomTmuxName(title), "stty raw -echo; exec cat > "+out)
	waitForPaneCommand(t, ToLoomTmuxName(title), "cat")
	return out
}

// assertSameBytes fails unless got is want, reporting the first byte where
// they differ rather than diffing two long strings.
func assertSameBytes(t *testing.T, via, want, got string) {
	t.Helper()
	if got == want {
		return
	}
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	window := func(s string) string { return s[i:min(len(s), i+40)] }
	t.Errorf("%s delivered %d bytes, want %d; first difference at byte %d: got %q, want %q",
		via, len(got), len(want), i, window(got), window(want))
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
