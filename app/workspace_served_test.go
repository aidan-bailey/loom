package app

import (
	"net"
	"slices"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// otherClient is a second client of st's daemon, as a `loom workspace`
// command is, closed when the test ends.
func otherClient(t *testing.T, st daemonStack) *rpc.Client {
	t.Helper()
	a, b := net.Pipe()
	st.srv.Serve(a)
	c, err := rpc.Dial(b)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// servedAs is the ID of the workspace st's daemon serves as name, as the
// TUI's client has it.
func servedAs(t *testing.T, st daemonStack, name string) core.WorkspaceID {
	t.Helper()
	for _, v := range st.c.Workspaces() {
		if v.Name == name {
			return v.ID
		}
	}
	t.Fatalf("the daemon serves no workspace %q", name)
	return 0
}

// TestWorkspacesChanged_ClosesATabNoLongerServed: a workspace unregistered
// while the TUI runs (`loom workspace remove` in another process) leaves the
// published workspaces, and its tab closes, with a note, rather than stay
// a tab every request on is refused; the TUI goes to global mode when it
// was the only one shown.
func TestWorkspacesChanged_ClosesATabNoLongerServed(t *testing.T) {
	t.Run("one of two tabs", func(t *testing.T) {
		isolateTmux(t)
		twoTabs(t)
		m, st := linkedHome(t, "")
		require.Equal(t, []string{"ws-a", "ws-b"}, m.slotNames())
		b := servedAs(t, st, "ws-b")

		require.NoError(t, otherClient(t, st).Unregister("ws-b"))
		cmd := wake(t, m, func() bool { return !st.c.IsLoaded(b) })
		runCmds(t, cmd)

		assert.Equal(t, []string{"ws-a"}, m.slotNames())
		assert.Equal(t, "ws-a", m.name())
		assert.Contains(t, m.errBox.String(), "ws-b is no longer registered; closed its tab")
		assert.NoError(t, m.checkSlotInvariant())
	})

	t.Run("the only tab", func(t *testing.T) {
		isolateTmux(t)
		globalDisk(t)
		ws := writeWorkspaceState(t, "ws-b", "["+pausedRecord("other", "2026-10-01T11:00:00Z")+"]")
		savedRegistry(t, []string{"ws-b"}, "ws-b", ws)
		m, st := linkedHome(t, "")
		require.Equal(t, []string{"ws-b"}, m.slotNames())
		b := servedAs(t, st, "ws-b")

		require.NoError(t, otherClient(t, st).Unregister("ws-b"))
		cmd := wake(t, m, func() bool { return !st.c.IsLoaded(b) })
		runCmds(t, cmd)

		assert.Empty(t, m.slots, "global mode")
		assert.Empty(t, m.name())
		assert.Contains(t, m.errBox.String(), "ws-b is no longer registered")
		assert.NoError(t, m.checkSlotInvariant())
	})
}

// TestWorkspacesChanged_ARenamedTabTakesItsNewName: a workspace renamed
// while the TUI runs (`loom workspace rename` in another process) keeps its
// ID, so its tab stays, and everything the TUI keys on its name follows:
// the tab bar, the rail's label, the open list it persists, and a saved tab
// it failed to open (failedOpen).
func TestWorkspacesChanged_ARenamedTabTakesItsNewName(t *testing.T) {
	isolateTmux(t)
	twoTabs(t)
	x := writeWorkspaceState(t, "ws-x", "[]")
	savedRegistry(t, []string{"ws-a", "ws-b"}, "ws-a", x)
	m, st := linkedHome(t, "")
	require.Equal(t, []string{"ws-a", "ws-b"}, m.slotNames())
	m.failedOpen = []string{"ws-x"}
	other := otherClient(t, st)
	bID := servedAs(t, st, "ws-b")

	require.NoError(t, other.RenameWorkspace("ws-b", "ws-c"))
	require.NoError(t, other.RenameWorkspace("ws-x", "ws-y"))
	cmd := wake(t, m, func() bool {
		v, ok := st.c.Workspace(bID)
		return ok && v.Name == "ws-c" && slices.ContainsFunc(st.c.Workspaces(), func(v core.WorkspaceView) bool { return v.Name == "ws-y" })
	})
	runCmds(t, cmd)

	assert.Equal(t, []string{"ws-a", "ws-c"}, m.slotNames(), "the same tab, by its new name")
	assert.Contains(t, m.tabBar.String(), "ws-c")
	assert.NotContains(t, m.tabBar.String(), "ws-b")
	assert.Equal(t, "ws-c", m.slots[1].list.WorkspaceName())
	assert.Equal(t, []string{"ws-y"}, m.failedOpen)
	assert.Equal(t, []string{"ws-a", "ws-c", "ws-y"}, m.openList())
	assert.NoError(t, m.checkSlotInvariant())
}
