package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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

// workTailLines is how many of a log's last lines the tail keeps.
const workTailLines = 16

// workTailBytes is how much of a log's end the tail reads.
const workTailBytes = 64 << 10

// workPagerBytes is how much of a log's end the pager reads: a longer log
// shows its last part, and says so. The queue keeps no cap on a log's size,
// and the TUI must not read a gigabyte into memory.
const workPagerBytes = 4 << 20

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
	m.work = workState{from: m.viewMode, sel: m.work.sel, selIdx: m.work.selIdx}
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
		return ui.RenderWorkPager(m.lastWidth, m.workHeight(), ui.WorkPagerData{Job: job, Lines: p.lines, Top: p.top, Omitted: p.omitted, Err: p.err})
	}
	return ui.RenderWork(m.lastWidth, m.workHeight(), ui.WorkData{Queue: m.queue, Selected: job.ID, Tail: m.work.tail, Now: time.Now()})
}

// workHint is the status line under the view.
func (m *home) workHint() string {
	if m.work.pager != nil {
		return "esc back · j/k scroll · pgup/pgdn page · g/G top/bottom"
	}
	return "w/esc back · enter log · e caps · q quit"
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

// workLogMsg is a log read's result: a job's tail, or all the pager can
// show of it.
type workLogMsg struct {
	id      sched.JobID
	full    bool
	lines   []string
	omitted bool
	err     string
}

// workLogCmd reads the end of the log at path for job id. Everything it
// reads is passed in: the Cmd runs off the Update goroutine.
func workLogCmd(id sched.JobID, path string, full bool) tea.Cmd {
	limit := int64(workTailBytes)
	if full {
		limit = workPagerBytes
	}
	return func() tea.Msg {
		msg := workLogMsg{id: id, full: full}
		data, partial, err := readLogEnd(path, limit)
		if err != nil {
			msg.err = err.Error()
			return msg
		}
		lines := ui.LogLines(data, partial)
		if !full && len(lines) > workTailLines {
			lines = lines[len(lines)-workTailLines:]
		}
		msg.lines, msg.omitted = lines, partial
		return msg
	}
}

// readLogEnd reads the last limit bytes of the file at path; partial says
// the file's start was left out. A file that does not exist is an empty log
// (a queued job has none yet), not an error.
func readLogEnd(path string, limit int64) (data []byte, partial bool, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	start := int64(0)
	if st.Size() > limit {
		start, partial = st.Size()-limit, true
	}
	data = make([]byte, st.Size()-start)
	n, err := f.ReadAt(data, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	return data[:n], partial, nil
}

// applyWorkLog takes a log read's result. A tail read lands only for the
// job selected now, and a pager read only for the job the pager shows, so
// a read that was overtaken by a key press shows under no other job. The
// pager keeps its place unless it was at the end, where it follows.
func (m *home) applyWorkLog(msg workLogMsg) {
	if !msg.full {
		sel, ok := m.workSelected()
		if m.work.pager != nil || !ok || sel.ID != msg.id {
			return
		}
		m.work.tail = ui.WorkTail{ID: msg.id, Lines: msg.lines, Err: msg.err}
		return
	}
	p := m.work.pager
	if p == nil || p.id != msg.id {
		return
	}
	p.err = msg.err
	if msg.err != "" {
		return
	}
	body := ui.WorkPagerBody(m.workHeight())
	atEnd := !p.loaded || p.top >= max(0, len(p.lines)-body)
	oldTop := p.top
	p.lines, p.omitted, p.loaded = msg.lines, msg.omitted, true
	maxTop := max(0, len(p.lines)-body)
	if atEnd {
		p.top = maxTop
	} else {
		p.top = min(oldTop, maxTop)
	}
}

// workDrained is Update's step after the model's events were applied
// (drainCore): it reads the log of the job the view shows when that job, its
// state or its sampled usage differ from what the last read was for. The
// queue changes only through QueueChanged, so this is the read on
// QueueChanged; a key that moves the selection or opens the pager changes
// the signature too.
func (m *home) workDrained() tea.Cmd {
	if m.viewMode != viewWork {
		return nil
	}
	job, ok := m.workShown()
	if !ok {
		m.work.sig = workSig{}
		return nil
	}
	sig := workSig{id: job.ID, state: job.State, rss: job.RSSKB, cpu: job.CPU, pager: m.work.pager != nil}
	if sig == m.work.sig {
		return nil
	}
	m.work.sig = sig
	return workLogCmd(job.ID, job.LogPath, sig.pager)
}

// workTick is the health tick's read: a running job's log grows between
// samples, so it is read again. A queued job has no log and a finished
// job's has stopped growing.
func (m *home) workTick() tea.Cmd {
	job, ok := m.workShown()
	if !ok || job.State != sched.Running {
		return nil
	}
	return workLogCmd(job.ID, job.LogPath, m.work.pager != nil)
}

// workModelKeys are the work view's keys that need the model. Offline they
// are refused with the info line the offline gate uses: the gate lets t and
// e through, being TUI-only keys in the focus layout.
var workModelKeys = map[string]bool{"e": true}

// workOfflineRefused refuses a key that needs the model while the TUI is
// offline, saying so on the info line.
func (m *home) workOfflineRefused(key string) bool {
	if !workModelKeys[key] || !m.offline() {
		return false
	}
	m.errBox.SetInfo(fmt.Sprintf("the loom daemon is %s: %s needs it", m.link.state.word(), key))
	return true
}

// pendingWork is a request of the work view waiting for its Reply. form is
// the caps form that sent a SetSchedulerCaps, whose Reply it answers; nil
// for the other requests.
type pendingWork struct {
	form *overlay.WorkCapsOverlay
}

// workReplied routes a work view request's Reply. The model's answer to a
// caps save goes to the form that sent it while that form is still the one
// open: a refusal into the form, an acceptance closing it. An answer for a
// form that has since closed, or been replaced, and every other Reply's
// error goes to the error line.
func (m *home) workReplied(p *pendingWork, r core.Reply) tea.Cmd {
	if o := p.form; o != nil && o == m.workCapsOverlay() {
		if r.Err != nil {
			o.SetError(r.Err.Error())
			return nil
		}
		m.dismissOverlay()
		m.state = stateDefault
		return nil
	}
	if r.Err != nil {
		return m.handleError(r.Err)
	}
	return nil
}

// openWorkCaps opens the caps form over the view, on the scheduler block as
// the model last published it. Before the daemon has reported its queue
// there is no host to edit caps for.
func (m *home) openWorkCaps() {
	q := m.queue
	if !ui.WorkReported(q) {
		m.errBox.SetInfo("the daemon has not reported its work queue yet")
		return
	}
	host := overlay.WorkCapsHost{Name: q.Host.Name, Cores: q.Host.Cores, RAMBytes: q.Host.RAMBytes}
	o := overlay.NewWorkCapsOverlay(host, workDefaults(q.Host), capsValues(q.Settings), q.Paused)
	o.SetSize(int(float32(m.lastWidth)*ui.OverlayWidthPercent), int(float32(m.lastHeight)*ui.OverlayHeightPercent))
	m.setOverlay(o, overlayWorkCaps)
	m.state = stateWorkCaps
}

// handleStateWorkCapsKey drives the caps form. Esc closes it; Enter sends
// its values as SetSchedulerCaps and holds the form until the Reply
// (workReplied), which carries the model's refusal back into it.
func handleStateWorkCapsKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	o := m.workCapsOverlay()
	if o == nil {
		return m, nil
	}
	closed, submit := o.HandleKeyPress(msg)
	switch {
	case closed:
		m.dismissOverlay()
		m.state = stateDefault
	case submit:
		o.SetSaving(true)
		m.core.SetSchedulerCaps(schedulerSettings(o.Values()), m.newReq(pendingReq{work: &pendingWork{form: o}}))
	}
	return m, nil
}

// workDefaults is the host's default for each field of the form, from the
// same function the model resolves its caps with. A host whose RAM could
// not be read has no memory default (the model refuses a block that does
// not set one), so those two fields stay 0 and the others keep theirs.
func workDefaults(h sched.Host) overlay.WorkCapsValues {
	p, err := sched.Resolve(config.SchedulerSettings{}, h)
	noMemory := err != nil
	if noMemory {
		p, err = sched.Resolve(config.SchedulerSettings{MemoryGB: 1}, h)
		if err != nil {
			return overlay.WorkCapsValues{}
		}
	}
	v := overlay.WorkCapsValues{
		CPUs:          p.Caps.CPUs,
		MemoryGB:      p.Caps.MemoryGB,
		IOJobs:        p.Caps.IOJobs,
		ClaimCPUs:     p.DefaultClaim.CPUs,
		ClaimMemoryGB: p.DefaultClaim.MemoryGB,
		StarveMinutes: int(p.StarveAfter / time.Minute),
		KeepFinished:  p.KeepFinished,
	}
	if noMemory {
		v.MemoryGB, v.ClaimMemoryGB = 0, 0
	}
	return v
}

// capsValues is the scheduler block as the form's numbers: 0 for a field the
// block leaves on the default.
func capsValues(s config.SchedulerSettings) overlay.WorkCapsValues {
	v := overlay.WorkCapsValues{CPUs: s.CPUs, MemoryGB: s.MemoryGB, IOJobs: s.IOJobs, StarveMinutes: s.StarveAfterMinutes, KeepFinished: s.KeepFinished}
	if s.DefaultClaim != nil {
		v.ClaimCPUs, v.ClaimMemoryGB = s.DefaultClaim.CPUs, s.DefaultClaim.MemoryGB
	}
	return v
}

// schedulerSettings is the scheduler block the form's numbers make. A form
// that edited no claim field leaves the block without a default claim.
func schedulerSettings(v overlay.WorkCapsValues) config.SchedulerSettings {
	s := config.SchedulerSettings{CPUs: v.CPUs, MemoryGB: v.MemoryGB, IOJobs: v.IOJobs, StarveAfterMinutes: v.StarveMinutes, KeepFinished: v.KeepFinished}
	if v.ClaimCPUs != 0 || v.ClaimMemoryGB != 0 {
		s.DefaultClaim = &config.ClaimSettings{CPUs: v.ClaimCPUs, MemoryGB: v.ClaimMemoryGB}
	}
	return s
}
