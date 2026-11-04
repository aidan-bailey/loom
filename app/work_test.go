package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testQueue is the queue view of a host called fresco holding jobs.
func testQueue(jobs ...core.JobView) core.QueueView {
	q := core.QueueView{Dir: "/tmp/loom/jobs", Jobs: jobs}
	q.Host = sched.Host{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}
	q.Policy.Caps = sched.Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}
	return q
}

// queueJob is a job in state with the default claim, submitted a minute ago.
func queueJob(id sched.JobID, state sched.State, desc string) core.JobView {
	j := core.JobView{ID: id, State: state, Description: desc}
	j.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}
	j.Submitted = time.Now().Add(-time.Minute)
	if state != sched.Queued {
		j.Started = j.Submitted
	}
	if state.Finished() {
		j.Ended = time.Now()
	}
	return j
}

// setQueue publishes q to m as the model does: a QueueChanged event, which
// the TUI's applier puts in home.queue.
func setQueue(t *testing.T, m *home, q core.QueueView) {
	t.Helper()
	_ = m.drainCore() // the model's own first QueueChanged must not land after q
	_ = m.applyCoreEvent(core.QueueChanged{View: q})
	require.Equal(t, q.Dir, m.queue.Dir, "fixture: the event reached home.queue")
	require.Len(t, m.queue.Jobs, len(q.Jobs))
}

// workHome is a home sized like a terminal and showing the work view over
// the jobs, entered from the focus layout.
func workHome(t *testing.T, jobs ...core.JobView) *home {
	t.Helper()
	m := newTestHome(t)
	m.errBox.SetSize(400, 1)
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
	setQueue(t, m, testQueue(jobs...))
	m.enterWorkView()
	require.Equal(t, viewWork, m.viewMode)
	return m
}

// pressWork sends the key the way a terminal does, to the default state.
func pressWork(t *testing.T, m *home, key string) tea.Cmd {
	t.Helper()
	_, cmd := handleStateDefaultKey(m, keyFor(t, key))
	return cmd
}

// selectedWork is the ID of the job the work view has selected.
func selectedWork(t *testing.T, m *home) sched.JobID {
	t.Helper()
	j, ok := m.workSelected()
	require.True(t, ok, "the work view has a job selected")
	return j.ID
}

// writeLog writes a log of n lines, "line 1" to "line n".
func writeLog(t *testing.T, path string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))
}

func TestWorkView_EnterAndLeaveReturnToTheModeItCameFrom(t *testing.T) {
	for name, from := range map[string]viewMode{"focus": viewFocus, "overview": viewOverview} {
		t.Run(name, func(t *testing.T) {
			m := newTestHome(t)
			m.viewMode = from
			m.toggleWorkView()
			require.Equal(t, viewWork, m.viewMode)
			m.toggleWorkView()
			assert.Equal(t, from, m.viewMode, "w returns to the mode it came from")
		})
	}

	t.Run("workbench", func(t *testing.T) {
		m := newTestHome(t)
		m.viewMode = viewWorkbench
		m.toggleWorkView()
		assert.Equal(t, viewWorkbench, m.viewMode, "the workbench has no way in")
	})
}

func TestWorkView_IsNotPersisted(t *testing.T) {
	m := newTestHome(t)
	require.NoError(t, m.appState().SetUIPrefs(config.UIPrefs{ViewMode: "overview"}))
	m.syncWorkspaces()
	m.viewMode = viewOverview
	m.toggleWorkView()
	assert.Equal(t, "overview", m.appState().GetUIPrefs().ViewMode, "entering the view writes nothing")
	m.toggleWorkView()
	assert.Equal(t, "overview", m.appState().GetUIPrefs().ViewMode, "nor does leaving it")
}

func TestWorkView_RefusedOnWindows(t *testing.T) {
	m := newTestHome(t)
	m.errBox.SetSize(400, 1)
	prev := workGOOS
	workGOOS = "windows"
	t.Cleanup(func() { workGOOS = prev })

	m.toggleWorkView()
	assert.Equal(t, viewFocus, m.viewMode)
	assert.Contains(t, m.errBox.String(), sched.ErrUnsupported.Error())

	workGOOS = "linux"
	m.toggleWorkView()
	assert.Equal(t, viewWork, m.viewMode, "anywhere else it opens")
}

func TestWorkView_SelectionIsTheJobsIDNotItsRow(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(15, sched.Running, "b"), queueJob(16, sched.Queued, "c"))
	assert.Equal(t, sched.JobID(14), selectedWork(t, m), "the first row is selected on entry")
	pressWork(t, m, "j")
	require.Equal(t, sched.JobID(15), selectedWork(t, m))

	// A job starts ahead of it: job 15's row moves down and it stays selected.
	setQueue(t, m, testQueue(queueJob(20, sched.Running, "n"), queueJob(14, sched.Running, "a"), queueJob(15, sched.Running, "b"), queueJob(16, sched.Queued, "c")))
	assert.Equal(t, sched.JobID(15), selectedWork(t, m))

	// It leaves the view: the row that slid into its place is selected.
	setQueue(t, m, testQueue(queueJob(20, sched.Running, "n"), queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "c")))
	assert.Equal(t, sched.JobID(16), selectedWork(t, m))

	// Every job gone: nothing is selected, and a job that comes back is.
	setQueue(t, m, testQueue())
	_, ok := m.workSelected()
	assert.False(t, ok)
	setQueue(t, m, testQueue(queueJob(30, sched.Running, "z")))
	assert.Equal(t, sched.JobID(30), selectedWork(t, m))
}

func TestWorkView_JAndKWalkTheRowsAndStopAtTheEnds(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"), queueJob(12, sched.Done, "c"))
	var seen []sched.JobID
	for _, key := range []string{"j", "j", "j", "k", "down", "up", "up", "up"} {
		pressWork(t, m, key)
		seen = append(seen, selectedWork(t, m))
	}
	assert.Equal(t, []sched.JobID{16, 12, 12, 16, 12, 16, 14, 14}, seen)
}

func TestWorkView_EscLeavesForTheModeItCameFrom(t *testing.T) {
	t.Run("focus", func(t *testing.T) {
		m := workHome(t, queueJob(14, sched.Running, "a"))
		pressWork(t, m, "esc")
		assert.Equal(t, viewFocus, m.viewMode)
	})
	t.Run("overview", func(t *testing.T) {
		m := newTestHome(t)
		m.viewMode = viewOverview
		m.toggleWorkView()
		require.Equal(t, viewWork, m.viewMode)
		pressWork(t, m, "esc")
		assert.Equal(t, viewOverview, m.viewMode)
	})
}

func TestWorkView_KeysItDoesNotOwnDoNothing(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	for _, key := range []string{"n", "N", "I", "D", "d", "tab", "i", "a", "g", "G", "W", "S", "f", "r", "p"} {
		cmd := pressWork(t, m, key)
		assert.Nil(t, cmd, "%q must not reach the script engine from the work view", key)
		assert.Equal(t, stateDefault, m.state, key)
		assert.Equal(t, viewWork, m.viewMode, key)
	}
	for _, key := range []string{"q", "?"} {
		assert.NotNil(t, pressWork(t, m, key), "%q is a script key of the work view", key)
	}
}

func TestWorkView_CtrlCStillQuits(t *testing.T) {
	m := workHome(t)
	cmd := pressWork(t, m, "ctrl+c")
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestWorkView_MouseIsDropped(t *testing.T) {
	m := workHome(t)
	mustAddInstance(t, m, "a")

	_, cmd := m.Update(tea.MouseClickMsg{X: 60, Y: 5, Button: tea.MouseLeft})
	assert.Nil(t, cmd)
	assert.False(t, m.dragging, "a click must not begin a drag over the hidden layout")
	_, cmd = m.Update(tea.MouseMotionMsg{X: 65, Y: 6, Button: tea.MouseLeft})
	assert.Nil(t, cmd)
	m.dragging = true
	_, cmd = m.Update(tea.MouseReleaseMsg{X: 65, Y: 6, Button: tea.MouseLeft})
	assert.Nil(t, cmd)
	assert.False(t, m.dragging, "a release drops a drag that was in flight")
	_, cmd = m.Update(tea.MouseWheelMsg{X: 60, Y: 5, Button: tea.MouseWheelDown})
	assert.Nil(t, cmd)
}

func TestWorkView_TheRailsBellWaitsForFocus(t *testing.T) {
	m := workHome(t)
	a := mustAddInstance(t, m, "a")
	selectIn(m, m.list, a)
	ring(m, a)

	_ = m.instanceChanged()
	assert.True(t, bell(m, a), "the focus layout is off screen: the bell is not attended")
	m.leaveWorkView()
	_ = m.instanceChanged()
	assert.False(t, bell(m, a), "back in focus, the selected row's bell clears")
}

func TestWorkView_ViewDrawsTheQueueInPlaceOfTheLayout(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "polyoxide tests"), queueJob(16, sched.Queued, "release build"))
	mustAddInstance(t, m, "rail-session")

	v := m.View()
	screen := ansi.Strip(v.Content)
	assert.Contains(t, screen, "Work queue · fresco")
	assert.Contains(t, screen, "polyoxide tests")
	assert.Contains(t, screen, "release build")
	assert.Contains(t, screen, "enter log")
	assert.NotContains(t, screen, "rail-session", "the rail is not drawn")
	assert.Nil(t, v.Cursor, "no pane cursor floats over the queue")
}

func TestWorkView_TheTailIsReadWhenTheSelectedJobChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 30)
	job := queueJob(14, sched.Running, "polyoxide tests")
	job.LogPath = path
	other := queueJob(15, sched.Running, "go test")
	m := workHome(t, job, other)

	cmd := m.workDrained()
	require.NotNil(t, cmd, "the view's first frame reads the selected job's tail")
	msg, ok := cmd().(workLogMsg)
	require.True(t, ok)
	assert.Equal(t, sched.JobID(14), msg.id)
	assert.False(t, msg.full)
	require.Len(t, msg.lines, workTailLines)
	assert.Equal(t, "line 15", msg.lines[0])
	assert.Equal(t, "line 30", msg.lines[workTailLines-1])
	m.applyWorkLog(msg)
	assert.Contains(t, ansi.Strip(m.View().Content), "line 30")
	assert.Nil(t, m.workDrained(), "nothing changed: no second read")

	// The model samples the job's usage and publishes it: a read.
	moved := job
	moved.RSSKB = 123
	setQueue(t, m, testQueue(moved, other))
	assert.NotNil(t, m.workDrained())
	assert.Nil(t, m.workDrained())

	// Its state changes (it ends): a read for the last lines.
	done := moved
	done.State, done.Ended = sched.Done, time.Now()
	setQueue(t, m, testQueue(done, other))
	assert.NotNil(t, m.workDrained())

	// Another job is selected: a read for it, and the old tail is not shown under it.
	pressWork(t, m, "k")
	require.Equal(t, sched.JobID(15), selectedWork(t, m))
	cmd = m.workDrained()
	require.NotNil(t, cmd)
	assert.Equal(t, sched.JobID(15), cmd().(workLogMsg).id)
	assert.NotContains(t, ansi.Strip(m.View().Content), "line 30", "job 14's tail is not shown under job 15")
}

func TestWorkView_UpdateReadsAfterItsDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 3)
	job := queueJob(14, sched.Running, "polyoxide tests")
	job.LogPath = path
	m := workHome(t, job)

	_, cmd := m.Update(coreWakeMsg{})
	var read *workLogMsg
	for _, msg := range runCmds(t, cmd) {
		if r, ok := msg.(workLogMsg); ok {
			read = &r
		}
	}
	require.NotNil(t, read, "an Update in the work view reads the tail it has not read")
	_, _ = m.Update(*read)
	assert.Equal(t, []string{"line 1", "line 2", "line 3"}, m.work.tail.Lines, "the message reaches the view")

	_, cmd = m.Update(coreWakeMsg{})
	for _, msg := range runCmds(t, cmd) {
		_, isRead := msg.(workLogMsg)
		assert.False(t, isRead, "nothing changed: no second read")
	}
}

func TestWorkView_TheTickRereadsARunningJobOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 3)
	running := queueJob(14, sched.Running, "a")
	running.LogPath = path
	finished := queueJob(12, sched.Done, "b")
	finished.LogPath = path
	m := workHome(t, running, queueJob(16, sched.Queued, "c"), finished)

	prev := tickUpdateMetadataCmd
	tickUpdateMetadataCmd = func() tea.Msg { return tickRearmed{} }
	t.Cleanup(func() { tickUpdateMetadataCmd = prev })
	reads := func() int {
		_, cmd := m.Update(tickUpdateMetadataMessage{})
		n := 0
		for _, msg := range runCmds(t, cmd) {
			if _, ok := msg.(workLogMsg); ok {
				n++
			}
		}
		return n
	}

	_ = m.workDrained()
	assert.Equal(t, 1, reads(), "a running job's log may have grown")
	pressWork(t, m, "j")
	_ = m.workDrained()
	assert.Equal(t, 0, reads(), "a queued job has no log")
	pressWork(t, m, "j")
	_ = m.workDrained()
	assert.Equal(t, 0, reads(), "a finished job's log has stopped growing")
}

func TestWorkView_ATailReadForAnotherJobIsDropped(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	m.applyWorkLog(workLogMsg{id: 99, lines: []string{"stale"}})
	assert.Equal(t, ui.WorkTail{}, m.work.tail)
	m.applyWorkLog(workLogMsg{id: 14, lines: []string{"fresh"}})
	assert.Equal(t, ui.WorkTail{ID: 14, Lines: []string{"fresh"}}, m.work.tail)
	m.applyWorkLog(workLogMsg{id: 14, err: "boom"})
	assert.Equal(t, ui.WorkTail{ID: 14, Err: "boom"}, m.work.tail)
}

func TestReadLogEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")

	data, partial, err := readLogEnd(path, 100)
	require.NoError(t, err, "a job with no log yet")
	assert.Empty(t, data)
	assert.False(t, partial)

	require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0o600))
	data, partial, err = readLogEnd(path, 100)
	require.NoError(t, err)
	assert.Equal(t, "0123456789", string(data))
	assert.False(t, partial)

	data, partial, err = readLogEnd(path, 4)
	require.NoError(t, err)
	assert.Equal(t, "6789", string(data), "the end of the log")
	assert.True(t, partial, "the head was cut")

	_, _, err = readLogEnd(filepath.Join(path, "below"), 4)
	assert.Error(t, err, "a path that cannot be a log is an error, not an empty log")
}

func TestWorkPager_OpensAtTheEndAndScrolls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 100)
	job := queueJob(14, sched.Running, "polyoxide tests")
	job.LogPath = path
	m := workHome(t, job)
	body := ui.WorkPagerBody(m.workHeight())
	maxTop := 100 - body

	assert.Nil(t, pressWork(t, m, "enter"))
	require.NotNil(t, m.work.pager)
	cmd := m.workDrained()
	require.NotNil(t, cmd, "opening the pager reads the log")
	read := cmd().(workLogMsg)
	assert.True(t, read.full)
	m.applyWorkLog(read)
	assert.Len(t, m.work.pager.lines, 100)
	assert.Equal(t, maxTop, m.work.pager.top, "a log opens at its end")

	pressWork(t, m, "k")
	assert.Equal(t, maxTop-1, m.work.pager.top)
	pressWork(t, m, "pgup")
	assert.Equal(t, maxTop-1-body, m.work.pager.top)
	pressWork(t, m, "g")
	assert.Equal(t, 0, m.work.pager.top)
	pressWork(t, m, "k")
	assert.Equal(t, 0, m.work.pager.top, "stops at the top")
	pressWork(t, m, "pgdown")
	assert.Equal(t, body, m.work.pager.top)
	pressWork(t, m, "G")
	assert.Equal(t, maxTop, m.work.pager.top)
	pressWork(t, m, "j")
	assert.Equal(t, maxTop, m.work.pager.top, "stops at the end")

	screen := ansi.Strip(m.View().Content)
	assert.Contains(t, screen, "job 14 · polyoxide tests · lines")
	assert.Contains(t, screen, "line 100")
	assert.Contains(t, screen, "esc back · j/k scroll")
}

func TestWorkPager_FollowsALogOnlyWhileAtItsEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 100)
	job := queueJob(14, sched.Running, "a")
	job.LogPath = path
	m := workHome(t, job)
	body := ui.WorkPagerBody(m.workHeight())
	pressWork(t, m, "enter")
	m.applyWorkLog(m.workDrained()().(workLogMsg))
	require.Equal(t, 100-body, m.work.pager.top)

	reread := func() { m.applyWorkLog(m.workTick()().(workLogMsg)) }
	writeLog(t, path, 120)
	reread()
	assert.Len(t, m.work.pager.lines, 120)
	assert.Equal(t, 120-body, m.work.pager.top, "at the end, the pager follows the log")

	pressWork(t, m, "g")
	writeLog(t, path, 130)
	reread()
	assert.Len(t, m.work.pager.lines, 130)
	assert.Equal(t, 0, m.work.pager.top, "scrolled away, it stays where it is")
}

func TestWorkPager_EscClosesItNotTheView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 5)
	job := queueJob(14, sched.Running, "a")
	job.LogPath = path
	m := workHome(t, job)
	pressWork(t, m, "enter")
	m.applyWorkLog(m.workDrained()().(workLogMsg))

	assert.Nil(t, pressWork(t, m, "esc"))
	assert.Nil(t, m.work.pager)
	assert.Equal(t, viewWork, m.viewMode, "esc closes the pager, not the view")
	cmd := m.workDrained()
	require.NotNil(t, cmd, "the tail is read again")
	assert.False(t, cmd().(workLogMsg).full)
	pressWork(t, m, "esc")
	assert.Equal(t, viewFocus, m.viewMode, "a second esc leaves the view")
}

func TestWorkPager_ClosesWhenItsJobLeavesTheQueue(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	pressWork(t, m, "enter")
	require.NotNil(t, m.work.pager)

	setQueue(t, m, testQueue())
	assert.Nil(t, m.workDrained())
	assert.Nil(t, m.work.pager)
	assert.Contains(t, m.errBox.String(), "job 14 is no longer listed")
}

func TestWorkPager_TakesOnlyItsOwnKeys(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(15, sched.Running, "b"))
	pressWork(t, m, "enter")
	for _, key := range []string{"q", "w", "x", "t", "o", "e", "n"} {
		assert.Nil(t, pressWork(t, m, key), "%q does nothing in the pager", key)
		require.NotNil(t, m.work.pager, key)
	}
	assert.Equal(t, sched.JobID(14), selectedWork(t, m), "scrolling is not selecting")
	pressWork(t, m, "j")
	assert.Equal(t, sched.JobID(14), selectedWork(t, m))
}

func TestWorkPager_AReadForAnotherJobIsDropped(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	pressWork(t, m, "enter")
	m.applyWorkLog(workLogMsg{id: 99, full: true, lines: []string{"stale"}})
	assert.Empty(t, m.work.pager.lines)
	m.applyWorkLog(workLogMsg{id: 14, full: true, err: "permission denied"})
	assert.Equal(t, "permission denied", m.work.pager.err)
	assert.Contains(t, ansi.Strip(m.View().Content), "cannot read the log: permission denied")
}

func TestWorkPager_NeedsAJob(t *testing.T) {
	m := workHome(t)
	assert.Nil(t, pressWork(t, m, "enter"))
	assert.Nil(t, m.work.pager)
}
