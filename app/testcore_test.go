package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	"charm.land/bubbles/v2/spinner"
)

// testWS builds a fixture workspace from its handles (any may be nil, as
// in a bare test home) holding insts, added in order.
func testWS(parts core.WorkspaceParts, insts ...*session.Instance) *core.Workspace {
	ws := core.NewWorkspace(parts)
	for _, inst := range insts {
		ws.Add(inst)
	}
	return ws
}

// slotOver builds a fixture slot view over ws: a rail reading it, plus the
// split pane and workbench every slot needs. wirePanes points the rail and
// pane at the test's pane registry.
func slotOver(ws *core.Workspace) *workspaceSlot {
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return &workspaceSlot{
		ws:        ws,
		list:      ui.NewList(&s, ws),
		splitPane: sp,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
	}
}

// wireCore gives a fixture home the model production builds in newHome:
// with no tab open the focused slot's workspace is the classic one,
// otherwise the tabs are m.slots' workspaces in order. A slot with no
// workspace gets an empty one (and a list reading it, if it had none). It
// keeps a model the test installed (m.core set beforehand, e.g. with a
// registry). Call it after assembling the slots and before exercising m.
func wireCore(t *testing.T, m *home) *home {
	t.Helper()
	if m.core == nil {
		m.core = core.NewForTest(core.Options{})
	}
	for _, s := range append([]*workspaceSlot{m.workspaceSlot}, m.slots...) {
		if s == nil {
			continue
		}
		if s.ws == nil {
			s.ws = testWS(core.WorkspaceParts{})
		}
		if s.list == nil {
			s.list = ui.NewList(&m.spinner, s.ws)
		}
	}
	var tabs []*core.Workspace
	for _, s := range m.slots {
		tabs = append(tabs, s.ws)
	}
	var classic *core.Workspace
	if m.workspaceSlot != nil {
		classic = m.ws
	}
	m.core.SetWorkspacesForTest(classic, tabs)
	return m
}

// reworkspace rebuilds slot's workspace with its handles edited by edit,
// keeping its instances in order, and gives the slot a fresh rail over it
// (the selection, workspace name and size kept). It stands in for the
// handle assignments fixtures made when the slot owned its handles: a
// core.Workspace's handles are fixed for its lifetime. It re-installs the
// model's mirror (wireCore).
func reworkspace(t *testing.T, m *home, slot *workspaceSlot, edit func(*core.WorkspaceParts)) {
	t.Helper()
	var p core.WorkspaceParts
	var insts []*session.Instance
	if slot.ws != nil {
		p = core.WorkspaceParts{Ctx: slot.ws.Ctx(), Storage: slot.ws.Storage(), Config: slot.ws.Config(), State: slot.ws.State()}
		insts = slot.ws.Instances()
	}
	edit(&p)
	old := slot.list
	slot.ws = testWS(p, insts...)
	slot.list = ui.NewList(&m.spinner, slot.ws)
	slot.list.SetPanes(m.panes)
	if old != nil {
		slot.list.SetWorkspaceName(old.WorkspaceName())
		slot.list.SetSize(old.Size())
		if sel := old.GetSelectedInstance(); sel != nil {
			slot.list.SelectInstance(sel)
		}
	}
	wireCore(t, m)
}

// editRCAuth edits the model's default-account remote-control auth: the
// assignments fixtures made to the fields of home's old rcAuth.
func editRCAuth(m *home, edit func(*session.RemoteControlAuth)) {
	a := m.core.RCAuth()
	edit(&a)
	m.core.SetRCAuth(a)
}
