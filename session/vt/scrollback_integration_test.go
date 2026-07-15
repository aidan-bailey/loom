package vt

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

// startTmux launches a detached tmux session (private socket) running sh,
// attaches a client on a PTY, and pumps its output into a fresh emulator.
// Returns the socket name, session name, the emulator, and a cleanup func.
func startTmux(t *testing.T, cols, rows int) (sock, name string, emu Emulator, cleanup func()) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock = fmt.Sprintf("loomvt-%d", os.Getpid())
	name = fmt.Sprintf("sbspike-%d", time.Now().UnixNano())
	run := func(args ...string) {
		out, err := exec.Command("tmux", append([]string{"-L", sock}, args...)...).CombinedOutput()
		require.NoError(t, err, "tmux %v: %s", args, out)
	}
	run("new-session", "-d", "-x", fmt.Sprint(cols), "-y", fmt.Sprint(rows), "-s", name, "sh")
	run("set-option", "-t", name, "status", "off")

	emu = NewXVT(cols, rows)
	// Mirror the production pump (session/tmux.startOutputPump): strip
	// alt-screen enter/exit before the emulator sees it, since a tmux
	// attach-session stream enters the alt screen immediately and never
	// leaves (Amendment 1). Without this the spike reproduces the original
	// falsified premise instead of validating the adopted mitigation.
	dest := NewAltScreenFilter(emu)
	attach := exec.Command("tmux", "-L", sock, "attach-session", "-t", name)
	ptmx, err := pty.StartWithSize(attach, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				_, _ = dest.Write(buf[:n])
			}
			if rerr != nil {
				close(done)
				return
			}
		}
	}()
	cleanup = func() {
		_ = exec.Command("tmux", "-L", sock, "kill-server").Run()
		_ = ptmx.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		_ = emu.Close()
	}
	return sock, name, emu, cleanup
}

// sendShell types a command into the tmux session's shell.
func sendShell(t *testing.T, sock, name, cmd string) {
	t.Helper()
	out, err := exec.Command("tmux", "-L", sock, "send-keys", "-t", name, cmd, "Enter").CombinedOutput()
	require.NoError(t, err, "send-keys: %s", out)
}

// settle waits for pane output to go quiet (crude: fixed delay is fine for a spike).
func settle() { time.Sleep(500 * time.Millisecond) }

// TestScrollbackAccumulation_RealTmux answers the design spike:
//  1. lines scrolled off the top land in scrollback, in order;
//  2. `clear` does not multiply history;
//  3. detach/reattach repaints do not pollute scrollback.
func TestScrollbackAccumulation_RealTmux(t *testing.T) {
	sock, name, emu, cleanup := startTmux(t, 80, 10)
	defer cleanup()
	settle()

	// Emit 30 numbered lines through a 10-row screen → ≥20 must scroll off.
	sendShell(t, sock, name, `for i in $(seq 1 30); do echo "spikeline$i"; done`)
	settle()

	got := emu.ScrollbackLen()
	require.Greater(t, got, 15, "scroll-off lines must accumulate in scrollback")
	window := emu.RenderWindow(got+10-1, 5) // top of the buffer (10 = screen rows)
	require.NotEmpty(t, window)

	// Full-buffer sanity: every scrollback line renders and early lines exist.
	all := emu.RenderWindow(emu.ScrollbackLen(), emu.ScrollbackLen()+10)
	require.Contains(t, stripANSI(all), "spikeline1")

	// `clear` must not multiply history.
	before := emu.ScrollbackLen()
	sendShell(t, sock, name, "clear")
	settle()
	after := emu.ScrollbackLen()
	require.LessOrEqual(t, after, before+10,
		"clear must not push more than one screen into scrollback (got %d -> %d)", before, after)

	// Redraw (refresh-client forces a repaint) must not pollute scrollback.
	before = emu.ScrollbackLen()
	out, err := exec.Command("tmux", "-L", sock, "refresh-client").CombinedOutput()
	require.NoError(t, err, "refresh-client: %s", out)
	settle()
	require.LessOrEqual(t, emu.ScrollbackLen(), before+1,
		"a client repaint must not push content into scrollback")
}

// stripANSI removes CSI/OSC escapes so Contains-assertions see plain text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI ... final byte @-~
			for i++; i < len(s) && (s[i] < '@' || s[i] > '~'); i++ {
			}
		case ']': // OSC ... BEL or ST
			for i++; i < len(s) && s[i] != 0x07 && s[i] != 0x1b; i++ {
			}
			if i+1 < len(s) && s[i] == 0x1b {
				i++ // consume the '\' of ST
			}
		}
	}
	return b.String()
}
