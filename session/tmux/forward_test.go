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
