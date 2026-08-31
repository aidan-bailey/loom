package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/require"
)

// historyCmdExec returns a mock executor that serves 200 lines of history
// for `capture-pane -S` and the bottom 24 for the plain visible capture, so
// a pane has real content to scroll into.
func historyCmdExec() cmd_test.MockCmdExec {
	sessionCreated := false
	return cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			s := cmd.String()
			if strings.Contains(s, "has-session") {
				if sessionCreated {
					return nil
				}
				return fmt.Errorf("session does not exist")
			}
			if strings.Contains(s, "new-session") {
				sessionCreated = true
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			s := cmd.String()
			if strings.Contains(s, "capture-pane") {
				lo := 177
				if strings.Contains(s, "-S") {
					lo = 1
				}
				var b strings.Builder
				for i := lo; i <= 200; i++ {
					fmt.Fprintf(&b, "histline%d\n", i)
				}
				return []byte(strings.TrimRight(b.String(), "\n")), nil
			}
			return []byte(""), nil
		},
	}
}

// TestSplitPane_WheelScrollRerendersAgentImmediately pins the mouse-wheel
// contract: ScrollAgentUp/Down are the only thing the wheel handler calls,
// so they must leave the pane's displayed text reflecting the new offset
// on their own. Before this was enforced the offset moved but the text
// only refreshed on the next output event — the pane looked frozen until
// the agent happened to print something (or the user poked it with a key).
func TestSplitPane_WheelScrollRerendersAgentImmediately(t *testing.T) {
	setup := setupTestEnvironment(t, historyCmdExec())
	defer setup.cleanupFn()

	sp := NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
	sp.SetSize(120, 40)
	sp.SetInstance(setup.instance)
	require.NoError(t, sp.UpdateAgent(setup.instance))
	live := sp.agent.previewState.text
	require.Contains(t, live, "histline200")

	for i := 0; i < 60; i++ {
		sp.ScrollAgentUp()
	}
	require.True(t, sp.IsAgentInScrollMode())
	scrolled := sp.agent.previewState.text
	require.NotEqual(t, live, scrolled, "wheel scroll must re-render the agent pane without waiting for output")
	require.NotContains(t, scrolled, "histline200")

	for i := 0; i < 60; i++ {
		sp.ScrollAgentDown()
	}
	require.False(t, sp.IsAgentInScrollMode())
	require.Contains(t, sp.agent.previewState.text, "histline200",
		"scrolling back to the tail must restore the live view immediately")
}
