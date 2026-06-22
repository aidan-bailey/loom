package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForwardWheel_WritesSGRToPTY verifies ForwardWheel writes the expected SGR
// mouse-wheel bytes to the attach PTY (in-process, no subprocess) so a TUI agent
// scrolls its own view.
func TestForwardWheel_WritesSGRToPTY(t *testing.T) {
	noop := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	ts := newTmuxSession("wheel", "prog", NewMockPtyFactory(t), noop)

	// Wire a temp file as the PTY so we can read back what ForwardWheel writes.
	path := filepath.Join(t.TempDir(), "ptmx")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer f.Close()
	ts.ptmx = f

	require.NoError(t, ts.ForwardWheel(true, 3))
	require.NoError(t, ts.ForwardWheel(false, 1))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("\x1b[<64;1;1M", 3)+"\x1b[<65;1;1M", string(data),
		"three wheel-up notches then one wheel-down, as SGR press sequences")
}

// TestForwardMouse_WritesSGR verifies click/drag/release encode to the expected
// SGR mouse bytes at the given cell.
func TestForwardMouse_WritesSGR(t *testing.T) {
	noop := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	ts := newTmuxSession("mouse", "prog", NewMockPtyFactory(t), noop)
	path := filepath.Join(t.TempDir(), "ptmx")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer f.Close()
	ts.ptmx = f

	require.NoError(t, ts.ForwardMouse(0, 5, 3, true))  // left press at col5,row3
	require.NoError(t, ts.ForwardMouse(32, 7, 3, true)) // left drag (motion)
	require.NoError(t, ts.ForwardMouse(0, 7, 3, false)) // left release

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "\x1b[<0;5;3M\x1b[<32;7;3M\x1b[<0;7;3m", string(data))
}

// TestPaste_WrapsBracketed verifies Paste wraps text in bracketed-paste markers.
func TestPaste_WrapsBracketed(t *testing.T) {
	noop := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	ts := newTmuxSession("paste", "prog", NewMockPtyFactory(t), noop)
	path := filepath.Join(t.TempDir(), "ptmx")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer f.Close()
	ts.ptmx = f

	require.NoError(t, ts.Paste("")) // empty is a no-op
	require.NoError(t, ts.Paste("a\nb"))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "\x1b[200~a\nb\x1b[201~", string(data))
}

// TestForwardWheel_NoPTY returns an error rather than panicking when unattached.
func TestForwardWheel_NoPTY(t *testing.T) {
	noop := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	ts := newTmuxSession("nopty", "prog", NewMockPtyFactory(t), noop)
	assert.Error(t, ts.ForwardWheel(true, 1))
}

// TestIsAlternateScreen maps tmux's #{alternate_on} output to the boolean.
func TestIsAlternateScreen(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"1\n", true},
		{"0\n", false},
		{"", false},
	}
	for _, tc := range cases {
		out := tc.out
		cmdExec := cmd_test.MockCmdExec{
			RunFunc:    func(*exec.Cmd) error { return nil },
			OutputFunc: func(*exec.Cmd) ([]byte, error) { return []byte(out), nil },
		}
		ts := newTmuxSession("alt", "prog", NewMockPtyFactory(t), cmdExec)
		assert.Equal(t, tc.want, ts.IsAlternateScreen(), "alternate_on=%q", tc.out)
	}
}
