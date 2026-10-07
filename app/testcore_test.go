package app

import (
	"reflect"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/require"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
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

// fixtureRows is a fixture list's source: the rows of the slot holding the
// list, as its home shows them (rowsOf, overlays applied), once wireCore
// has bound it to the slot and the home; none before that, so a selection
// is made after wireCore.
type fixtureRows struct {
	m *home
	s *workspaceSlot
}

// Rows implements ui.InstanceSource.
func (r *fixtureRows) Rows() []core.InstanceView {
	if r.m == nil || r.s == nil {
		return nil
	}
	return r.m.rowsOf(r.s)
}

// fixtureSources holds each fixture list's source, by list, for wireCore
// to bind to the slot showing the list and its home. Tests in this
// package run one at a time; each entry goes when its test ends.
var fixtureSources = map[*ui.List]*fixtureRows{}

// fixtureList builds a rail for a fixture slot assembled before its home:
// it shows the rows of the slot it ends up in (fixtureRows) once wireCore
// has run.
func fixtureList(t *testing.T) *ui.List {
	t.Helper()
	src := &fixtureRows{}
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	l := ui.NewList(&s, src)
	fixtureSources[l] = src
	t.Cleanup(func() { delete(fixtureSources, l) })
	return l
}

// slotOver builds a fixture slot view over ws: a rail reading its rows,
// plus the split pane and workbench every slot needs. wirePanes points the
// rail and pane at the test's pane registry; wireCore fills the rows.
func slotOver(t *testing.T, ws *core.Workspace) *workspaceSlot {
	t.Helper()
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	return &workspaceSlot{
		ws:        ws,
		list:      fixtureList(t),
		splitPane: sp,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
	}
}

// wireCore gives a fixture home the model production builds in newHome:
// with no tab open the focused slot's workspace is the classic one,
// otherwise the tabs are m.slots' workspaces in order. A slot with no
// workspace gets an empty one (and a list reading its rows, if it had
// none). It keeps a model the test installed (m.core set beforehand, e.g.
// with a registry), and ends by filling every slot's view store from the
// model (syncViews). Call it after assembling the slots and before
// exercising m; a test that changes the model's instances afterwards calls
// m.syncViews() before reading them through the TUI.
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
			s.list = ui.NewList(&m.spinner, slotRows{m, s})
			s.list.SetPanes(m.panes)
		}
		if src := fixtureSources[s.list]; src != nil {
			src.m, src.s = m, s
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
	m.syncViews()
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
	slot.list = ui.NewList(&m.spinner, slotRows{m, slot})
	slot.list.SetPanes(m.panes)
	wireCore(t, m)
	if old != nil {
		slot.list.SetWorkspaceName(old.WorkspaceName())
		slot.list.SetSize(old.Size())
		if sel := old.GetSelectedInstance(); sel != nil {
			slot.list.SelectID(sel.ID)
		}
	}
}

// idOf is inst's ID in m's model, assigned on first use: what the TUI's
// rows, messages and events name it by.
func idOf(m *home, inst *session.Instance) core.InstanceID {
	return m.core.IDForTest(inst)
}

// rowOf rereads m's view stores (syncViews) and returns inst's row, which
// an open slot must show.
func rowOf(t *testing.T, m *home, inst *session.Instance) *core.InstanceView {
	t.Helper()
	m.syncViews()
	v, _ := m.viewByID(idOf(m, inst))
	if v == nil {
		t.Fatalf("fixture: no open slot shows %q", inst.Title)
	}
	return v
}

// selectIn rereads m's view stores (syncViews) and selects inst's row in
// list, as SelectInstance did by identity.
func selectIn(m *home, list *ui.List, inst *session.Instance) {
	m.syncViews()
	list.SelectID(idOf(m, inst))
}

// instByTitle returns the instance whose row in list is titled title,
// through the model; nil when list shows none.
func instByTitle(m *home, list *ui.List, title string) *session.Instance {
	v := list.GetInstanceByTitle(title)
	if v == nil {
		return nil
	}
	return m.core.InstanceOf(v.ID)
}

// titleID is the ID of list's row titled title, 0 when it shows none.
func titleID(list *ui.List, title string) core.InstanceID {
	if v := list.GetInstanceByTitle(title); v != nil {
		return v.ID
	}
	return 0
}

// lastInst is the instance of the focused list's last row, through the
// model: the one a creation flow appended.
func lastInst(m *home) *session.Instance {
	rows := m.list.GetInstances()
	return m.core.InstanceOf(rows[len(rows)-1].ID)
}

// selID is the ID of list's selected row, 0 when nothing is selected.
func selID(list *ui.List) core.InstanceID {
	if v := list.GetSelectedInstance(); v != nil {
		return v.ID
	}
	return 0
}

// listIDs is list's rows' IDs, in order.
func listIDs(list *ui.List) []core.InstanceID {
	var ids []core.InstanceID
	for _, v := range list.GetInstances() {
		ids = append(ids, v.ID)
	}
	return ids
}

// ring sets inst's bell, the TUI's overlay a pane's BEL sets (bellMsg).
func ring(m *home, inst *session.Instance) {
	if m.bells == nil {
		m.bells = make(map[core.InstanceID]bool)
	}
	m.bells[idOf(m, inst)] = true
}

// bell reports whether inst's bell is set (home.bells).
func bell(m *home, inst *session.Instance) bool { return m.bells[idOf(m, inst)] }

// editRCAuth edits the model's default-account remote-control auth: the
// assignments fixtures made to the fields of home's old rcAuth.
func editRCAuth(m *home, edit func(*session.RemoteControlAuth)) {
	a := m.core.RCAuth()
	edit(&a)
	m.core.SetRCAuth(a)
}

// deliver hands m a core job's result as the runtime would, returning the
// Cmd the update produced (handler plus drained events).
func deliver(t *testing.T, m *home, result any) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(coreResultMsg{msg: result})
	return cmd
}

// requestResults drains m's model as Update's drain does (drainCore),
// applying its events but dropping their Cmds, and runs its jobs as the
// runtime would, off the drain. It returns their results, each with a
// request's tracking removed (core.UntrackedForTest), without delivering
// them: what a request made outside an Update (a handler called directly)
// queued.
func requestResults(t *testing.T, m *home) []any {
	t.Helper()
	var results []any
	for out := m.core.Sync(); !out.Empty(); out = m.core.Sync() {
		for _, ev := range out.Events {
			_ = m.applyCoreEvent(ev)
		}
		for _, job := range out.Jobs {
			results = append(results, core.UntrackedForTest(job()))
		}
	}
	return results
}

// sequenceMsgType is the type of the message a tea.Sequence of two or
// more Cmds produces. bubbletea keeps it unexported, so it is taken from a
// throwaway Sequence (one Cmd alone would come back as itself).
var sequenceMsgType = reflect.TypeOf(tea.Sequence(
	func() tea.Msg { return nil },
	func() tea.Msg { return nil },
)())

// pumpCore models only the core-result feedback loop of the runtime: it
// runs cmd, then every Cmd it produces, serially in FIFO order, expanding
// tea.Batch and handing each coreResultMsg back through Update, whose Cmd
// joins the queue, so a core job that follows another (a start's
// initial-prompt send) lands too. Every other message is dropped, and a
// tea.Sequence is not expanded: meeting one fails the test, since the
// order it promises is not modelled.
func pumpCore(t *testing.T, m *home, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		require.Less(t, steps, 100, "core pump did not settle")
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if reflect.TypeOf(msg) == sequenceMsgType {
			t.Fatalf("pumpCore met a tea.Sequence, whose ordering it does not model")
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case coreResultMsg:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}
