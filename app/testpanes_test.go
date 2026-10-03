package app

import (
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	"github.com/stretchr/testify/require"
)

// testPaneClients is the pane registry of the test that is running, and
// testInstanceClients the client each fixture instance was given. Every
// fixture that builds a home wires the registry in (wirePanes), and every
// fixture that attaches a client registers it here, so an instance built
// before its home still renders through it. Tests in this package run one
// at a time (none calls t.Parallel), so one registry at a time suffices.
var (
	testPaneClients     *ui.PaneClients
	testInstanceClients map[*session.Instance]*tmux.TmuxSession
)

// testPanes returns the running test's pane registry, creating it on first
// use. Its own clients (Ensure, Replace) attach through a fake PTY to an
// always-alive mock tmux, so no tmux server is reached.
func testPanes(t *testing.T) *ui.PaneClients {
	t.Helper()
	if testPaneClients == nil {
		p := ui.NewPaneClients()
		p.SetClientFactoryForTest(func(name, program string) *tmux.TmuxSession {
			return tmux.NewAttachClientWithDeps(name, program, fakePtyFactory{t: t}, aliveCmdExecForTest())
		})
		testPaneClients = p
		testInstanceClients = make(map[*session.Instance]*tmux.TmuxSession)
		t.Cleanup(func() { testPaneClients, testInstanceClients = nil, nil })
	}
	return testPaneClients
}

// attachTestClient registers, in the running test's registry, an attach
// client for inst's session. The client is attached through ptyFactory and
// runs its tmux commands on cmdExec, as ensurePane does at a load or a
// start. It returns the client.
func attachTestClient(t *testing.T, inst *session.Instance, ptyFactory tmux.PtyFactory, cmdExec cmd_test.MockCmdExec) *tmux.TmuxSession {
	t.Helper()
	panes := testPanes(t)
	c := tmux.NewAttachClientWithDeps(inst.Pane().TmuxSessionName(), inst.Pane().SessionProgram(), ptyFactory, cmdExec)
	require.NoError(t, c.Restore())
	panes.InjectForTest(c.SessionName(), c)
	testInstanceClients[inst] = c
	return c
}

// clientOf returns the client attachTestClient gave inst, even after the
// registry dropped it.
func clientOf(t *testing.T, inst *session.Instance) *tmux.TmuxSession {
	t.Helper()
	c := testInstanceClients[inst]
	require.NotNil(t, c, "fixture: %s was given no client", inst.Title)
	return c
}

// wirePanes points m, and every loaded slot's list and split pane, at the
// running test's registry, and returns m.
func wirePanes(t *testing.T, m *home) *home {
	t.Helper()
	m.panes = testPanes(t)
	for _, s := range m.openSlots() {
		if s.list != nil {
			s.list.SetPanes(m.panes)
		}
		if s.splitPane != nil {
			s.splitPane.SetPanes(m.panes)
		}
	}
	return m
}
