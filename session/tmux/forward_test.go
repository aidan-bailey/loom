package tmux

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForwardWheel_InjectsSGRWheelSequences verifies ForwardWheel injects the
// expected SGR mouse-wheel bytes via `tmux send-keys -l` so a TUI agent scrolls
// its own view.
func TestForwardWheel_InjectsSGRWheelSequences(t *testing.T) {
	var last *exec.Cmd
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(cmd *exec.Cmd) error { last = cmd; return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	ts := newTmuxSession("wheel", "prog", NewMockPtyFactory(t), cmdExec)

	require.NoError(t, ts.ForwardWheel(true, 3))
	require.NotNil(t, last)
	assert.Contains(t, last.Args, "send-keys")
	assert.Contains(t, last.Args, "-l")
	assert.Equal(t, strings.Repeat("\x1b[<64;1;1M", 3), last.Args[len(last.Args)-1],
		"three wheel-up notches as repeated SGR press sequences")

	require.NoError(t, ts.ForwardWheel(false, 1))
	assert.Equal(t, "\x1b[<65;1;1M", last.Args[len(last.Args)-1], "one wheel-down notch")
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
