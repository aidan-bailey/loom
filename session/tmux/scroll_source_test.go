package tmux

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/require"
)

// TestSeedHistory_CapturedOnRestore: Restore must run one history-only
// capture (`capture-pane ... -E -1`) and store the rows.
func TestSeedHistory_CapturedOnRestore(t *testing.T) {
	var sawHistoryOnly bool
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error { return nil },
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			s := cmd.String()
			if strings.Contains(s, "capture-pane") && strings.Contains(s, "-E -1") {
				sawHistoryOnly = true
				return []byte("old1\nold2\nold3"), nil
			}
			return []byte(""), nil
		},
	}
	ts := newTmuxSession("seedtest", "prog", NewMockPtyFactory(t), cmdExec)

	require.NoError(t, ts.Restore())
	require.True(t, sawHistoryOnly, "Restore must capture history-only seed")
	require.Equal(t, []string{"old1", "old2", "old3"}, ts.SeedHistory())
}

// TestSeedHistory_EmptyOnCaptureFailure: a failed seed capture must not
// fail Restore; SeedHistory is just empty.
func TestSeedHistory_EmptyOnCaptureFailure(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error { return nil },
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			if strings.Contains(cmd.String(), "capture-pane") {
				return nil, exec.ErrNotFound
			}
			return []byte(""), nil
		},
	}
	ts := newTmuxSession("seedfail", "prog", NewMockPtyFactory(t), cmdExec)

	require.NoError(t, ts.Restore())
	require.Empty(t, ts.SeedHistory())
}

// TestScrollAccessors_NoEmulator: without an emulator every accessor
// reports ok=false / zero values (snapshot path).
func TestScrollAccessors_NoEmulator(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "snapshot")
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return []byte(""), nil },
	}
	ts := newTmuxSession("noemu", "prog", NewMockPtyFactory(t), cmdExec)

	require.NoError(t, ts.Restore())

	_, ok := ts.ScrollbackLen()
	require.False(t, ok)
	_, ok = ts.RenderWindow(0, 10)
	require.False(t, ok)
}
