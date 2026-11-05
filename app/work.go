package app

import (
	"fmt"
	"runtime"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
)

// workGOOS is the OS the TUI runs on; a test sets it to see the Windows
// refusal.
var workGOOS = runtime.GOOS

// workScriptKeys are the keys of the work view that go to the script engine:
// w leaves the view, q quits and ? shows the help. A user can rebind them.
var workScriptKeys = map[string]bool{"w": true, "q": true, "?": true}

// workState is the work view's own state. The jobs are home.queue's: the
// view re-reads them on every use, so a drain that changes the queue
// changes the view.
type workState struct {
	// from is the mode w and esc return to: viewFocus or viewOverview.
	from viewMode
	// sel is the selected job, by ID (0: none yet). selIdx is its row when
	// last seen, which heals the selection when the job leaves the view.
	sel    sched.JobID
	selIdx int
	// tail is the selected job's last log lines, as the last read returned
	// them.
	tail ui.WorkTail
	// pager is the open log pager, nil when closed.
	pager *workPager
	// sig is what the last log read was made for (workDrained).
	sig workSig
	// seq is the sequence number of the last log read issued. Bubble Tea
	// runs Cmds concurrently, so reads can land out of order: a result
	// older than seq is dropped. It survives leaving the view, so a read
	// still in flight from an earlier visit cannot beat a new one.
	seq uint64
	// tailStat is the log's size and mtime as tail's last read found them,
	// so the next read of that log can be skipped while they hold.
	tailStat logStat
}

// workPager is the open log pager over one job's log.
type workPager struct {
	// id is the job whose log the pager shows.
	id sched.JobID
	// lines are the log's lines (its last workPagerBytes), and top is the
	// first one shown.
	lines []string
	top   int
	// loaded is set once a read landed; omitted says the log's start is not
	// in lines; err is why a read failed.
	loaded  bool
	omitted bool
	err     string
	// stat is the log's size and mtime as the last read found them.
	stat logStat
	// belief is set when the pager shows the job's belief (b), which a
	// ReadBeliefs reply fills, not its log: no log read touches it.
	belief bool
}

// workSig is what a log read was made for: the job shown, its state and
// its sampled usage (which the model publishes after each sample, so a
// change is a QueueChanged), and whether the pager or the tail was read.
type workSig struct {
	id    sched.JobID
	state sched.State
	rss   int64
	cpu   float64
	pager bool
}

// enterWorkView shows the work view over the mode it is entered from
// (focus or overview). The selection survives a visit; the tail, the pager
// and what the last read was for do not.
func (m *home) enterWorkView() {
	if workGOOS == "windows" {
		m.errBox.SetInfo(sched.ErrUnsupported.Error())
		return
	}
	m.work = workState{from: m.viewMode, sel: m.work.sel, selIdx: m.work.selIdx, seq: m.work.seq}
	m.viewMode = viewWork
}

// leaveWorkView returns to the mode the view was entered from. Entering
// overview again seeds its cursor, which heals one the queue's changes
// could not have moved.
func (m *home) leaveWorkView() {
	m.work.pager = nil
	if m.work.from == viewOverview {
		m.enterOverview()
		return
	}
	m.viewMode = viewFocus
}

// toggleWorkView is the w key: into the view from focus or overview, out of
// it to where it came from. The workbench has no way in.
func (m *home) toggleWorkView() {
	switch m.viewMode {
	case viewWork:
		m.leaveWorkView()
	case viewFocus, viewOverview:
		m.enterWorkView()
	}
}

// dropWorkMouse swallows mouse input while the work view is up, as overview
// does: the hit-tests describe the focus layout, which is not on screen. A
// release also drops a drag that was in flight when the view opened. It
// reports whether it took msg.
func (m *home) dropWorkMouse(msg tea.Msg) bool {
	if m.viewMode != viewWork {
		return false
	}
	switch msg.(type) {
	case tea.MouseWheelMsg, tea.MouseClickMsg, tea.MouseMotionMsg:
		return true
	case tea.MouseReleaseMsg:
		m.dragging = false
		m.splitPane.ClearSelections()
		return true
	}
	return false
}

// workSelected is the selected job, resolved from home.queue by its ID
// every time it is asked for. A selection whose job has left the view (a
// retention drop, a RECENT section that moved on) moves to the row that
// slid into its place, or the last row; false when the view lists no job.
func (m *home) workSelected() (core.JobView, bool) {
	rows := ui.WorkRows(m.queue.Jobs)
	i := slices.IndexFunc(rows, func(j core.JobView) bool { return j.ID == m.work.sel })
	if i < 0 {
		if len(rows) == 0 {
			m.work.sel, m.work.selIdx = 0, 0
			return core.JobView{}, false
		}
		i = min(m.work.selIdx, len(rows)-1)
		m.work.sel = rows[i].ID
	}
	m.work.selIdx = i
	return rows[i], true
}

// moveWorkSelection moves the selection dir rows (-1 or 1), stopping at the
// ends.
func (m *home) moveWorkSelection(dir int) {
	if _, ok := m.workSelected(); !ok {
		return
	}
	rows := ui.WorkRows(m.queue.Jobs)
	i := min(max(m.work.selIdx+dir, 0), len(rows)-1)
	m.work.sel, m.work.selIdx = rows[i].ID, i
}

// workShown is the job whose log the view reads: the pager's while it is
// open, else the selected one. A pager whose job is no longer in the queue
// closes, saying so.
func (m *home) workShown() (core.JobView, bool) {
	if p := m.work.pager; p != nil {
		if i := slices.IndexFunc(m.queue.Jobs, func(j core.JobView) bool { return j.ID == p.id }); i >= 0 {
			return m.queue.Jobs[i], true
		}
		m.work.pager = nil
		m.errBox.SetInfo(fmt.Sprintf("job %d is no longer listed", p.id))
	}
	return m.workSelected()
}

// workHeight is the rows the view has: the terminal less the top chrome,
// the status line and the error row.
func (m *home) workHeight() int { return max(1, m.lastHeight-m.topChromeHeight()-2) }

// workContent is the view's content for View: the pager over its job's log,
// or the queue with the selected job's tail.
func (m *home) workContent() string {
	job, ok := m.workShown()
	if p := m.work.pager; p != nil && ok {
		return ui.RenderWorkPager(m.lastWidth, m.workHeight(), ui.WorkPagerData{Job: job, Lines: p.lines, Top: p.top, Omitted: p.omitted, Err: p.err, Belief: p.belief})
	}
	return ui.RenderWork(m.lastWidth, m.workHeight(), ui.WorkData{Queue: m.queue, Selected: job.ID, Tail: m.work.tail, Now: time.Now()})
}

// workHint is the status line under the view.
func (m *home) workHint() string {
	if m.work.pager != nil {
		return "esc back · j/k scroll · pgup/pgdn page · g/G top/bottom"
	}
	return "w/esc back · enter log · b belief · x cancel · t to front · o open session · e caps · q quit"
}

// handleWorkKey is the work view's keys: j/k and the arrows move the
// selection, enter opens the pager on it, esc returns to the mode the view
// came from, and w, q and ? go to the script engine. Every other key does
// nothing, as in overview: it would act on the focus layout, which is not on
// screen.
func (m *home) handleWorkKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.work.pager != nil {
		m.handleWorkPagerKey(key)
		return m, nil
	}
	if m.workOfflineRefused(key) {
		return m, nil
	}
	switch key {
	case "esc":
		m.leaveWorkView()
		return m, m.instanceChanged()
	case "j", "down":
		m.moveWorkSelection(1)
	case "k", "up":
		m.moveWorkSelection(-1)
	case "enter":
		m.openWorkPager()
	case "b":
		m.openWorkBelief()
	case "x":
		return m, m.askCancelWorkJob()
	case "t":
		m.frontWorkJob()
	case "o":
		return m, m.focusWorkOwner()
	case "e":
		m.openWorkCaps()
	default:
		if workScriptKeys[key] {
			cmd, _ := m.dispatchScript(key)
			return m, cmd
		}
	}
	return m, nil
}

// openWorkPager opens the pager on the selected job. Its first read lands
// through workDrained.
func (m *home) openWorkPager() {
	job, ok := m.workSelected()
	if !ok {
		return
	}
	m.work.pager = &workPager{id: job.ID}
	m.work.sig = workSig{}
}

// openWorkBelief is b: the selected job's belief in the pager, read from
// the daemon (ReadBeliefs, smart scheduler spec §8). A job without a
// signature has none to show, and the info line says why.
func (m *home) openWorkBelief() {
	job, ok := m.workSelected()
	if !ok {
		return
	}
	if job.Signature == "" {
		why := "this job has no belief"
		if job.Unsized != "" {
			why = "not sized: " + job.Unsized
		}
		m.errBox.SetInfo(why)
		return
	}
	m.work.pager = &workPager{id: job.ID, belief: true}
	m.work.sig = workSig{}
	m.core.ReadBeliefs(sched.BeliefQuery{ID: job.Signature}, m.newReq(pendingReq{work: &pendingWork{belief: job.ID}}))
}

// handleWorkPagerKey scrolls the pager or closes it. Following is just
// being at the end: a read that finds the pager there keeps it there.
func (m *home) handleWorkPagerKey(key string) {
	p := m.work.pager
	body := ui.WorkPagerBody(m.workHeight())
	maxTop := max(0, len(p.lines)-body)
	switch key {
	case "esc":
		m.work.pager = nil
		m.work.sig = workSig{}
		return
	case "j", "down":
		p.top++
	case "k", "up":
		p.top--
	case "pgdown", "ctrl+d":
		p.top += body
	case "pgup", "ctrl+u":
		p.top -= body
	case "g", "home":
		p.top = 0
	case "G", "end":
		p.top = maxTop
	}
	p.top = min(max(p.top, 0), maxTop)
}

// workModelKeys are the work view's keys that need the model. Offline they
// are refused with the info line the offline gate uses: the gate lets t and
// e through, being TUI-only keys in the focus layout.
var workModelKeys = map[string]bool{"x": true, "t": true, "o": true, "e": true, "b": true}

// workOfflineRefused refuses a key that needs the model while the TUI is
// offline, saying so on the info line.
func (m *home) workOfflineRefused(key string) bool {
	if !workModelKeys[key] || !m.offline() {
		return false
	}
	m.errBox.SetInfo(fmt.Sprintf("the loom daemon is %s: %s needs it", m.link.state.word(), key))
	return true
}

// askCancelWorkJob is x: a confirm for cancelling the selected job. The job
// is taken now, named in the prompt by its ID, and the cancel goes to that
// ID whatever is selected when the answer comes: the queue may change under
// the dialog, and the model re-validates (a job that has finished by then
// is refused). A finished job has nothing to cancel, so it asks nothing.
func (m *home) askCancelWorkJob() tea.Cmd {
	job, ok := m.workSelected()
	if !ok {
		return nil
	}
	if job.State.Finished() {
		m.errBox.SetInfo(fmt.Sprintf("job %d has finished", job.ID))
		return nil
	}
	id := job.ID
	return m.confirmTask("Cancel "+ui.JobLabel(job)+"?", overlay.ConfirmationTask{Sync: func() {
		m.core.CancelJob(id, core.CancelFromView, m.newReq(pendingReq{work: &pendingWork{}}))
	}})
}

// frontWorkJob is t: the selected queued job goes to the front of the line.
// Only a queued job can; the model re-validates it.
func (m *home) frontWorkJob() {
	job, ok := m.workSelected()
	if !ok {
		return
	}
	if job.State != sched.Queued {
		m.errBox.SetInfo(fmt.Sprintf("job %d isn't waiting", job.ID))
		return
	}
	m.core.MoveJobToFront(job.ID, m.newReq(pendingReq{work: &pendingWork{}}))
}

// focusWorkOwner is o: it leaves the view for the focus layout on the
// selected job's owner. The row is found by the owner's InstanceID in the
// open slots; when none shows it, the owner's workspace is opened as a tab
// first, by the ID the job carries (openTab(job.OwnerWorkspaceID)), never
// by the workspace's label, which is for display only. An owner the model
// does not serve has no workspace ID (0), and the key says so.
func (m *home) focusWorkOwner() tea.Cmd {
	job, ok := m.workSelected()
	if !ok {
		return nil
	}
	switch {
	case job.Owner == 0:
		m.errBox.SetInfo(fmt.Sprintf("job %d has no owner", job.ID))
		return nil
	case job.OwnerWorkspaceID == 0:
		m.errBox.SetInfo(fmt.Sprintf("job %d's session isn't served", job.ID))
		return nil
	}
	var opened tea.Cmd
	idx, found := m.slotShowing(job.Owner)
	if !found {
		// The global workspace has a served ID but is never a tab (the TUI
		// shows it as classic mode), so opening one would persist a tab
		// named "" in the open list. A slot already showing the workspace
		// is opened no second time: a slot shows one workspace for its life.
		if m.isGlobalWorkspace(job.OwnerWorkspaceID) {
			m.errBox.SetInfo(fmt.Sprintf("job %d's session is in the global workspace, which no open tab shows", job.ID))
			return nil
		}
		if slices.ContainsFunc(m.openSlots(), func(s *workspaceSlot) bool { return s.id == job.OwnerWorkspaceID }) {
			m.errBox.SetInfo(fmt.Sprintf("job %d's session isn't in workspace %s", job.ID, job.OwnerWorkspace))
			return nil
		}
		cmd, err := m.openTab(job.OwnerWorkspaceID)
		if err != nil {
			return m.handleError(err)
		}
		opened = cmd
		m.persistOpenList()
		idx, found = m.slotShowing(job.Owner)
	}
	if !found || !m.focusSlotInstance(idx, job.Owner) {
		m.errBox.SetInfo(fmt.Sprintf("job %d's session isn't in workspace %s", job.ID, job.OwnerWorkspace))
		return opened
	}
	m.work.pager = nil
	m.viewMode = viewFocus
	m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "" })
	m.persistFocusedWorkspace()
	return tea.Batch(opened, m.instanceChanged())
}

// isGlobalWorkspace reports whether id is the global workspace, as the
// model's published workspaces name it.
func (m *home) isGlobalWorkspace(id core.WorkspaceID) bool {
	return slices.ContainsFunc(m.core.Workspaces(), func(v core.WorkspaceView) bool { return v.ID == id && v.Name == "" })
}
