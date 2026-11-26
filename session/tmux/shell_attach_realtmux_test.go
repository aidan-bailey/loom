//go:build !windows

package tmux

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const realShellName = "loom_term_sa"

// startRealShell starts a detached `sh` session on a private server and
// attaches a ShellAttach to it at cols x rows.
func startRealShell(t *testing.T, tag string, cols, rows int) *ShellAttach {
	t.Helper()
	privateTmux(t, tag)
	newRawSession(t, realShellName, "sh")
	a, err := StartShellAttach(realShellName, cols, rows, MakePtyFactory())
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// clientSize is the size of the session's attached client as tmux reports
// it, "" when none is attached or tmux cannot answer.
func clientSize(name string) string {
	out, err := Command(context.Background(), "list-clients", "-t", SessionTarget(name), "-F", "#{client_width}x#{client_height}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// collected gathers what a ShellAttach reads, in the background, until its
// Read fails.
type collected struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func collect(a *ShellAttach) *collected {
	c := &collected{}
	go func() {
		p := make([]byte, 4096)
		for {
			n, err := a.Read(p)
			if n > 0 {
				c.mu.Lock()
				c.buf.Write(p[:n])
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *collected) contains(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Contains(c.buf.Bytes(), []byte(s))
}

// A shell attach follows a real session: it starts at its size, what is typed
// reaches the shell and its output comes back, and a resize reaches tmux.
func TestShellAttach_AttachesTypesReadsAndResizes_RealTmux(t *testing.T) {
	a := startRealShell(t, "sa1", 90, 20)
	out := collect(a)

	require.Eventually(t, func() bool { return clientSize(realShellName) == "90x20" }, 10*time.Second, 50*time.Millisecond,
		"tmux sees the client at the size it started with")

	_, err := a.Write([]byte("echo shellattach$((40+2))\r"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return out.contains("shellattach42") }, 10*time.Second, 50*time.Millisecond,
		"the shell ran the command (the typed line holds `$((`, so only the result reads 42)")

	require.NoError(t, a.Resize(100, 30))
	require.Eventually(t, func() bool { return clientSize(realShellName) == "100x30" }, 10*time.Second, 50*time.Millisecond,
		"tmux sees the resize")
}

// Close ends the attach client, and the shell, which is the TUI terminal
// pane's too, keeps running.
func TestShellAttach_CloseEndsTheClientNotTheSession_RealTmux(t *testing.T) {
	a := startRealShell(t, "sa2", 80, 24)
	require.Eventually(t, func() bool { return clientSize(realShellName) != "" }, 10*time.Second, 50*time.Millisecond,
		"fixture: the client attached")

	require.NoError(t, a.Close())

	require.Eventually(t, func() bool { return clientSize(realShellName) == "" }, 10*time.Second, 50*time.Millisecond,
		"the client is gone")
	assert.True(t, slices.Contains(serverSessions(t), realShellName), "and the session is still there")
}

// A pane scrolled past its screen gives its scrolled-off rows, in order, and
// not the visible ones.
func TestShellHistory_RowsAboveTheVisibleScreen_RealTmux(t *testing.T) {
	privateTmux(t, "sa3")
	newRawSession(t, "loom_term_sh", `sh -c 'i=1; while [ $i -le 60 ]; do echo row$i; i=$((i+1)); done; sleep 60'`)

	var got []byte
	require.Eventually(t, func() bool {
		var err error
		got, err = ShellHistory("loom_term_sh", internalexec.Default{})
		return err == nil && bytes.Contains(got, []byte("row30\r\n"))
	}, 10*time.Second, 50*time.Millisecond, "the rows scrolled off")

	assert.True(t, bytes.HasPrefix(got, []byte("row1\r\n")), "from the first row on")
	assert.True(t, bytes.HasSuffix(got, []byte("\r\n\x1b[0m")), "each row ended by CRLF, and an SGR reset closes the history")
	assert.NotContains(t, string(got), "row60", "the visible screen is not history")
}

// A pane that never scrolled has no history: tmux's own capture of that range
// would hand back the first visible row.
func TestShellHistory_NoHistoryIsEmpty_RealTmux(t *testing.T) {
	privateTmux(t, "sa4")
	newRawSession(t, "loom_term_sh", `sh -c 'echo only; sleep 60'`)
	require.Eventually(t, func() bool {
		out, err := Command(context.Background(), "capture-pane", "-p", "-t", PaneTarget("loom_term_sh")).Output()
		return err == nil && bytes.Contains(out, []byte("only"))
	}, 10*time.Second, 50*time.Millisecond, "fixture: the pane shows its one row")

	got, err := ShellHistory("loom_term_sh", internalexec.Default{})

	require.NoError(t, err)
	assert.Empty(t, got)
}
