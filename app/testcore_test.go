package app

import (
	"reflect"
	"testing"

	"github.com/aidan-bailey/loom/config"
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
		ws.AddForTest(inst)
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
// rail and pane at the test's pane registry; wireCore installs ws in the
// model, names the slot after it and fills the rows.
func slotOver(t *testing.T, ws *core.Workspace) *workspaceSlot {
	t.Helper()
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	return slotWith(ws, &workspaceSlot{
		list:      fixtureList(t),
		splitPane: sp,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
	})
}

// fixtureWS holds each fixture slot's workspace, the model's object the
// slot shows, for tests that reach its instances or handles (ws). A slot
// holds only the workspace's ID and view; wireCore installs this object in
// the model and names the slot after it. Entries for slots wireCore saw
// go when their test ends.
var fixtureWS = map[*workspaceSlot]*core.Workspace{}

// wiredModel is the model the running test's last wireCore installed: a
// slot production code built during the test (activateWorkspace, say) has
// no fixtureWS entry, and ws resolves it through this model by ID. Tests
// in this package run one at a time.
var wiredModel *core.Model

// The model's handles of the workspace a slot shows, for tests that set up
// or check the model's own state (its config, state.json, context or
// storage). Production reads the slot's published view (info) and writes
// through requests; a test that changes a handle and then reads through
// the TUI calls m.syncWorkspaces() first, as production's writers do.

func (s *workspaceSlot) wsCtx() *config.WorkspaceContext {
	if ws := s.ws(); ws != nil {
		return ws.Ctx()
	}
	return nil
}

func (s *workspaceSlot) storage() *session.Storage {
	if ws := s.ws(); ws != nil {
		return ws.Storage()
	}
	return nil
}

func (s *workspaceSlot) appConfig() *config.Config {
	if ws := s.ws(); ws != nil {
		return ws.Config()
	}
	return nil
}

func (s *workspaceSlot) appState() config.AppState {
	if ws := s.ws(); ws != nil {
		return ws.State()
	}
	return nil
}

// slotWith records ws as the fixture slot s's workspace and returns s.
func slotWith(ws *core.Workspace, s *workspaceSlot) *workspaceSlot {
	fixtureWS[s] = ws
	return s
}

// ws is the model's workspace this slot shows, for tests: the fixture's
// (fixtureWS), else the wired model's workspace with the slot's ID. nil
// when neither knows the slot.
func (s *workspaceSlot) ws() *core.Workspace {
	if ws := fixtureWS[s]; ws != nil {
		return ws
	}
	if wiredModel != nil && s.id != 0 {
		return wiredModel.WorkspaceForTest(s.id)
	}
	return nil
}

// wireCore gives a fixture home the model production builds in newHome:
// with no tab open the focused slot's workspace is the classic one,
// otherwise the tabs are m.slots' workspaces in order. A slot with no
// workspace gets an empty one (and a list reading its rows, if it had
// none). It keeps a model the test installed (m.core set beforehand, e.g.
// with a registry, wrapped in testLoop), and a liveness probe it set (m.aliveProbe), else gives
// it fixtureAlive. It ends by filling every slot's view store from the
// model (syncViews). Call it after assembling the slots and before
// exercising m; a test that changes the model's instances afterwards calls
// m.syncViews() before reading them through the TUI.
func wireCore(t *testing.T, m *home) *home {
	t.Helper()
	if m.core == nil {
		m.core = testLoop(t, core.NewForTest(core.Options{}))
	}
	if m.aliveProbe == nil {
		m.aliveProbe = fixtureAlive(m)
	}
	wiredModel = testModel(m)
	t.Cleanup(func() { wiredModel = nil })
	slots := append([]*workspaceSlot{m.workspaceSlot}, m.slots...)
	for _, s := range slots {
		if s == nil {
			continue
		}
		if s.ws() == nil {
			fixtureWS[s] = testWS(core.WorkspaceParts{})
		} else {
			fixtureWS[s] = s.ws()
		}
		t.Cleanup(func() { delete(fixtureWS, s) })
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
		tabs = append(tabs, s.ws())
	}
	var classic *core.Workspace
	if m.workspaceSlot != nil {
		classic = m.ws()
	}
	testModel(m).SetWorkspacesForTest(classic, tabs)
	// Each slot is named after the workspace it shows, and holds its view.
	for _, s := range slots {
		if s == nil {
			continue
		}
		s.id = testModel(m).WorkspaceIDForTest(s.ws())
		if v, ok := testModel(m).Workspace(s.id); ok {
			s.info = v
		}
	}
	m.syncViews()
	return m
}

// fixtureAlive is a fixture home's tmux liveness probe (home.aliveProbe):
// the has-session answer of the tmux session of the loaded instance whose
// agent session has that name. Fixtures build those sessions on a mock
// executor (aliveCmdExecForTest, deadCmdExecForTest, …), so this is the
// answer the TUI got when it probed through the instance
// (AgentPane.TmuxAlive). A name no loaded instance's session has reads
// dead.
func fixtureAlive(m *home) func(string) bool {
	return func(name string) bool {
		if name == "" {
			return false
		}
		for _, s := range m.openSlots() {
			if s == nil || s.ws() == nil {
				continue
			}
			for _, inst := range s.ws().InstancesForTest() {
				if inst.Pane().TmuxSessionName() == name {
					return inst.Pane().TmuxAlive()
				}
			}
		}
		return false
	}
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
	if ws := slot.ws(); ws != nil {
		p = core.WorkspaceParts{Ctx: ws.Ctx(), Storage: ws.Storage(), Config: ws.Config(), State: ws.State()}
		insts = ws.InstancesForTest()
	}
	edit(&p)
	old := slot.list
	fixtureWS[slot] = testWS(p, insts...)
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
	return testModel(m).IDOfForTest(inst)
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

// shownStatus is inst's status as the TUI shows it: its row's, with the
// overlays (the pane ladder) applied, after rereading the view stores.
func shownStatus(t *testing.T, m *home, inst *session.Instance) session.Status {
	t.Helper()
	return rowOf(t, m, inst).Status
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
	return testModel(m).InstanceForTest(v.ID)
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
	return testModel(m).InstanceForTest(rows[len(rows)-1].ID)
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

// deliver hands m a core job's result as the runtime would (the loop
// delivers it, then wakes the TUI), returning the Cmd the wake's update
// produced (handler plus drained events).
func deliver(t *testing.T, m *home, result any) tea.Cmd {
	t.Helper()
	loopOf(m).DeliverForTest(result)
	_, cmd := m.Update(coreWakeMsg{})
	return cmd
}

// testLoop runs model on a loop that keeps its jobs for the test
// (core.StartForTest), stopped when the test ends.
func testLoop(t *testing.T, model *core.Model) core.Core {
	t.Helper()
	l := core.StartForTest(model)
	t.Cleanup(l.Stop)
	return l
}

// loopOf returns the home's loop, for its seams.
func loopOf(m *home) *core.Loop { return m.core.(*core.Loop) }

// testModel returns the home's model, for its seams. Its loop is idle
// between calls (core.StartForTest), so the test may reach it directly.
func testModel(m *home) *core.Model { return loopOf(m).ModelForTest() }

// applyDrain drains m's model as Update's drain does (drainCore), applying
// its events but dropping their Cmds.
func applyDrain(m *home) {
	for events := m.core.Sync(); len(events) > 0; events = m.core.Sync() {
		for _, ev := range events {
			_ = m.applyCoreEvent(ev)
		}
	}
}

// tickModel runs the model's health tick as its loop's timer would
// (TickForTest), then drains as a wake's Update does.
func tickModel(t *testing.T, m *home) tea.Cmd {
	t.Helper()
	loopOf(m).TickForTest()
	_, cmd := m.Update(coreWakeMsg{})
	return cmd
}

// requestJob drains m's model as Update's drain does (applyDrain) and
// returns the one job the model kept: what a request made outside an
// Update (a handler called directly) queued. Calling it runs the job on
// the test's goroutine and returns its result, undelivered.
func requestJob(t *testing.T, m *home) core.Job {
	t.Helper()
	applyDrain(m)
	jobs := loopOf(m).JobsForTest()
	require.Len(t, jobs, 1, "the request queued one job")
	return jobs[0]
}

// requestResults drains m's model as Update's drain does (applyDrain), runs
// every job the model kept, and returns their results, each with a
// request's tracking removed (core.UntrackedForTest), undelivered: what a
// request made outside an Update (a handler called directly) queued.
func requestResults(t *testing.T, m *home) []any {
	t.Helper()
	applyDrain(m)
	var results []any
	for _, job := range loopOf(m).JobsForTest() {
		results = append(results, core.UntrackedForTest(job()))
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

// pumpCore models the runtime's job loop: it runs cmd, then every Cmd it
// produces, serially in FIFO order, expanding tea.Batch and dropping every
// other message. Before each, it runs every job the model kept and
// delivers its result (deliver), whose Cmd joins the queue, so a job that
// follows another (a start's initial-prompt send) lands too. A
// tea.Sequence fails the test, since the order it promises is not
// modelled.
func pumpCore(t *testing.T, m *home, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; ; steps++ {
		require.Less(t, steps, 100, "core pump did not settle")
		if jobs := loopOf(m).JobsForTest(); len(jobs) > 0 {
			for _, job := range jobs {
				queue = append(queue, deliver(t, m, job()))
			}
			continue
		}
		if len(queue) == 0 {
			return
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if reflect.TypeOf(msg) == sequenceMsgType {
			t.Fatalf("pumpCore met a tea.Sequence, whose ordering it does not model")
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
		}
	}
}
