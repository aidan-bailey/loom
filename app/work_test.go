package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

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

func TestWorkView_AnOlderReadLandingLastIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 3)
	job := queueJob(14, sched.Running, "a")
	job.LogPath = path
	m := workHome(t, job)

	older := m.workDrained()()
	require.NotNil(t, older)
	writeLog(t, path, 5)
	newer := m.workTick()()

	// Bubble Tea may deliver the two results in either order.
	m.applyWorkLog(newer.(workLogMsg))
	require.Equal(t, 5, len(m.work.tail.Lines))
	m.applyWorkLog(older.(workLogMsg))
	assert.Len(t, m.work.tail.Lines, 5, "the older read, landing last, does not overwrite the newer")

	// In the other order the current one is applied over nothing newer.
	m2 := workHome(t, job)
	o := m2.workDrained()().(workLogMsg)
	n := m2.workTick()().(workLogMsg)
	m2.applyWorkLog(o)
	assert.Empty(t, m2.work.tail.Lines, "the older read is dropped even first: a newer one is in flight")
	m2.applyWorkLog(n)
	assert.Len(t, m2.work.tail.Lines, 5)
}

func TestWorkView_APagerReadAlsoDropsAnOlderOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 3)
	job := queueJob(14, sched.Running, "a")
	job.LogPath = path
	m := workHome(t, job)
	pressWork(t, m, "enter")
	older := m.workDrained()().(workLogMsg)
	writeLog(t, path, 6)
	newer := m.workTick()().(workLogMsg)
	m.applyWorkLog(newer)
	m.applyWorkLog(older)
	assert.Len(t, m.work.pager.lines, 6)
}

func TestWorkView_ASeqSurvivesLeavingTheView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 3)
	job := queueJob(14, sched.Running, "a")
	job.LogPath = path
	m := workHome(t, job)
	stale := m.workDrained()().(workLogMsg)
	m.leaveWorkView()
	m.enterWorkView()
	fresh := m.workDrained()().(workLogMsg)
	assert.Greater(t, fresh.seq, stale.seq, "a read of the new visit outranks one still in flight from the last")
}

func TestWorkView_AnUnchangedLogIsNotReadAgain(t *testing.T) {
	for _, pager := range []bool{false, true} {
		t.Run(map[bool]string{false: "tail", true: "pager"}[pager], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "output.log")
			writeLog(t, path, 3)
			job := queueJob(14, sched.Running, "a")
			job.LogPath = path
			m := workHome(t, job)
			if pager {
				pressWork(t, m, "enter")
			}
			lines := func() []string {
				if pager {
					return m.work.pager.lines
				}
				return m.work.tail.Lines
			}

			first := m.workDrained()().(workLogMsg)
			require.False(t, first.unchanged)
			m.applyWorkLog(first)
			require.Equal(t, []string{"line 1", "line 2", "line 3"}, lines())

			again := m.workTick()().(workLogMsg)
			assert.True(t, again.unchanged, "same size and mtime: nothing read")
			assert.Empty(t, again.lines)
			m.applyWorkLog(again)
			assert.Equal(t, []string{"line 1", "line 2", "line 3"}, lines(), "the lines are kept")

			writeLog(t, path, 5)
			grown := m.workTick()().(workLogMsg)
			assert.False(t, grown.unchanged, "a grown log is read again")
			m.applyWorkLog(grown)
			assert.Len(t, lines(), 5)

			// Same size, new mtime: rewritten in place.
			require.NoError(t, os.WriteFile(path, []byte("LINE 1\nLINE 2\nLINE 3\nLINE 4\nLINE 5\n"), 0o600))
			later := time.Now().Add(time.Hour)
			require.NoError(t, os.Chtimes(path, later, later))
			touched := m.workTick()().(workLogMsg)
			assert.False(t, touched.unchanged, "a changed mtime is read again")
		})
	}
}

func TestWorkView_AFreshPagerReadsAnUnchangedLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 3)
	job := queueJob(14, sched.Running, "a")
	job.LogPath = path
	m := workHome(t, job)
	pressWork(t, m, "enter")
	m.applyWorkLog(m.workDrained()().(workLogMsg))
	pressWork(t, m, "esc")
	pressWork(t, m, "enter")
	msg := m.workDrained()().(workLogMsg)
	assert.False(t, msg.unchanged, "a reopened pager holds no lines yet")
	m.applyWorkLog(msg)
	assert.Len(t, m.work.pager.lines, 3)
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

// workWatch is a home's Core that records the work view's requests instead
// of sending them.
type workWatch struct {
	core.Core
	cancels []workCancel
	fronts  []sched.JobID
	caps    []config.SchedulerSettings
	beliefs []sched.BeliefQuery
	reqs    []core.ReqID
}

// workCancel is a CancelJob the work view sent.
type workCancel struct {
	id   sched.JobID
	from core.CancelFrom
}

// watchWork puts a workWatch between m and its model.
func watchWork(t *testing.T, m *home) *workWatch {
	t.Helper()
	w := &workWatch{Core: m.core}
	testStacks.Store(core.Core(w), stackOf(m))
	t.Cleanup(func() { testStacks.Delete(core.Core(w)) })
	m.core = w
	return w
}

func (w *workWatch) SetSchedulerCaps(s config.SchedulerSettings, req core.ReqID) {
	w.caps = append(w.caps, s)
	w.reqs = append(w.reqs, req)
}

func (w *workWatch) CancelJob(id sched.JobID, from core.CancelFrom, req core.ReqID) {
	w.cancels = append(w.cancels, workCancel{id: id, from: from})
	w.reqs = append(w.reqs, req)
}

func (w *workWatch) MoveJobToFront(id sched.JobID, req core.ReqID) {
	w.fronts = append(w.fronts, id)
	w.reqs = append(w.reqs, req)
}

func (w *workWatch) ReadBeliefs(q sched.BeliefQuery, req core.ReqID) {
	w.beliefs = append(w.beliefs, q)
	w.reqs = append(w.reqs, req)
}

func TestWorkView_BOpensTheSelectedJobsBelief(t *testing.T) {
	j := queueJob(14, sched.Running, "web-smoke")
	j.Signature = "3f9a1c2b7d10"
	m := workHome(t, j)
	w := watchWork(t, m)
	pressWork(t, m, "b")
	require.Len(t, w.beliefs, 1)
	assert.Equal(t, sched.BeliefQuery{ID: "3f9a1c2b7d10"}, w.beliefs[0])
	require.NotNil(t, m.work.pager)
	assert.True(t, m.work.pager.belief)

	b := sched.Belief{ID: "3f9a1c2b7d10", Signature: sched.Signature{Repo: "/src/polyoxide/.git", Folder: ".", Command: []string{"bash", "scripts/web-smoke.sh"}}}
	_ = m.handleReply(core.Reply{Req: w.reqs[0], Beliefs: sched.BeliefsPage{Beliefs: []sched.BeliefView{{Belief: b, RepoName: "polyoxide"}}}})
	screen := ansi.Strip(m.View().Content)
	assert.Contains(t, screen, "belief · job 14")
	assert.Contains(t, screen, "belief 3f9a1c2b7d10 · polyoxide · .")
	assert.Nil(t, m.workDrained(), "a log read never lands on a belief pager")
	assert.Nil(t, m.workTick())
}

func TestWorkView_BOnAJobWithoutABeliefSaysWhy(t *testing.T) {
	j := queueJob(14, sched.Running, "a")
	j.Unsized = "read /x/.git: denied"
	m := workHome(t, j)
	w := watchWork(t, m)
	pressWork(t, m, "b")
	assert.Empty(t, w.beliefs)
	assert.Nil(t, m.work.pager)
	assert.Contains(t, m.errBox.String(), "not sized: read /x/.git: denied")
}

// capsSent opens the form over one running job, types 2000 into the CPUs
// field and saves: the form is waiting for the model's answer to
// w.reqs[0].
func capsSent(t *testing.T) (*home, *workWatch) {
	t.Helper()
	m := workHome(t, queueJob(14, sched.Running, "a"))
	w := watchWork(t, m)
	pressWork(t, m, "e")
	require.Equal(t, stateWorkCaps, m.state)
	for _, key := range []string{"2", "0", "0", "0", "enter"} {
		_, _ = handleStateWorkCapsKey(m, keyFor(t, key))
	}
	require.Len(t, w.caps, 1)
	return m, w
}

func TestWorkCaps_EOpensTheFormOverTheViewWithTheConfiguredValues(t *testing.T) {
	q := testQueue(queueJob(14, sched.Running, "a"))
	q.Settings = config.SchedulerSettings{CPUs: 8, DefaultClaim: &config.ClaimSettings{CPUs: 2}}
	m := workHome(t)
	setQueue(t, m, q)

	assert.Nil(t, pressWork(t, m, "e"))
	require.Equal(t, stateWorkCaps, m.state)
	o := m.workCapsOverlay()
	require.NotNil(t, o)
	assert.Equal(t, overlay.WorkCapsValues{CPUs: 8, ClaimCPUs: 2}, o.Values())
	assert.Contains(t, ansi.Strip(o.Render()), "set · host default 14", "the form shows the host's defaults beside the set values")

	screen := ansi.Strip(m.View().Content)
	assert.Contains(t, screen, "Work queue caps · fresco", "the form is drawn over the view")
	assert.Contains(t, screen, "Work queue · fresco", "the view stays under it")
}

func TestWorkCaps_NothingToEditBeforeTheDaemonReports(t *testing.T) {
	m := workHome(t)
	setQueue(t, m, core.QueueView{})
	pressWork(t, m, "e")
	assert.Equal(t, stateDefault, m.state)
	assert.Contains(t, m.errBox.String(), "has not reported its work queue yet")
}

func TestWorkCaps_OfflineRefusesTheKey(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	m.link.state = linkWaiting
	cmd := pressWork(t, m, "e")
	assert.Nil(t, cmd)
	assert.Equal(t, stateDefault, m.state, "no form opens over a daemon that is gone")
	assert.Contains(t, m.errBox.String(), "the loom daemon is stopped: e needs it")
}

func TestWorkCaps_SavingSendsTheFormsSettingsOnce(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	w := watchWork(t, m)
	pressWork(t, m, "e")
	require.Equal(t, stateWorkCaps, m.state)

	for _, key := range []string{"1", "2", "tab", "tab", "tab", "3", "enter", "enter"} {
		_, _ = handleStateWorkCapsKey(m, keyFor(t, key))
	}
	require.Len(t, w.caps, 1, "the second enter finds the save in flight")
	assert.Equal(t, config.SchedulerSettings{CPUs: 12, DefaultClaim: &config.ClaimSettings{CPUs: 3}}, w.caps[0])
	assert.NotZero(t, w.reqs[0], "the save carries a ReqID, so its answer finds the form")
	assert.True(t, m.workCapsOverlay().Saving())
	assert.Equal(t, stateWorkCaps, m.state, "the form stays until the model answers")
}

func TestWorkCaps_ARefusalIsShownInTheForm(t *testing.T) {
	m, w := capsSent(t)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("scheduler.cpus is 2000: it must be 1–1024")})

	assert.Equal(t, stateWorkCaps, m.state, "the form stays open")
	o := m.workCapsOverlay()
	require.NotNil(t, o)
	assert.False(t, o.Saving())
	assert.Contains(t, ansi.Strip(o.Render()), "scheduler.cpus is 2000: it must be 1–1024")

	_, _ = handleStateWorkCapsKey(m, keyFor(t, "enter"))
	assert.Len(t, w.caps, 2, "the form can be sent again")
}

func TestWorkCaps_AnAcceptedSaveClosesTheForm(t *testing.T) {
	m, w := capsSent(t)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0]})
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.workCapsOverlay())
	assert.Equal(t, viewWork, m.viewMode)
}

func TestWorkCaps_ARefusalAfterTheFormClosedIsAnError(t *testing.T) {
	m, w := capsSent(t)
	_, _ = handleStateWorkCapsKey(m, keyFor(t, "esc"))
	require.Equal(t, stateDefault, m.state)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("scheduler.cpus is 2000: it must be 1–1024")})
	assert.Contains(t, m.errBox.String(), "scheduler.cpus is 2000")
}

func TestWorkCaps_AnOldSavesAnswerDoesNotTouchANewForm(t *testing.T) {
	m, w := capsSent(t)
	_, _ = handleStateWorkCapsKey(m, keyFor(t, "esc"))
	pressWork(t, m, "e")
	fresh := m.workCapsOverlay()
	require.NotNil(t, fresh)

	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("scheduler.cpus is 2000: it must be 1–1024")})
	assert.Equal(t, stateWorkCaps, m.state)
	assert.NotContains(t, ansi.Strip(fresh.Render()), "scheduler.cpus is 2000")
	assert.Contains(t, m.errBox.String(), "scheduler.cpus is 2000", "the refusal reaches the user as an error instead")
}

func TestWorkCaps_ItsKeysAreNotDeferredByMenuHighlighting(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	pressWork(t, m, "e")
	require.Equal(t, stateWorkCaps, m.state)

	_, cmd := m.handleKeyPress(keyFor(t, "t"))
	assert.Nil(t, cmd, "t is a built-in binding: a modal state outside the exclusion list defers it")
	assert.False(t, m.keySent)
	typeBurstThroughHandleKeyPress(t, m, "1234")
	assert.Equal(t, 1234, m.workCapsOverlay().Values().CPUs, "a burst of digits types in order")
}

func TestWorkDefaults(t *testing.T) {
	got := workDefaults(sched.Host{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}, config.SchedulerSettings{})
	assert.Equal(t, overlay.WorkCapsValues{CPUs: 14, MemoryGB: 22, IOJobs: 2, ClaimCPUs: 4, ClaimMemoryGB: 6, StarveMinutes: 10, KeepFinished: 50}, got)

	noRAM := workDefaults(sched.Host{Name: "odd", Cores: 8}, config.SchedulerSettings{})
	assert.Equal(t, overlay.WorkCapsValues{CPUs: 6, IOJobs: 2, ClaimCPUs: 4, StarveMinutes: 10, KeepFinished: 50}, noRAM,
		"a host whose RAM could not be read has no memory default")
}

func TestWorkDefaults_ClaimFollowsTheConfiguredCaps(t *testing.T) {
	host := sched.Host{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}

	got := workDefaults(host, config.SchedulerSettings{CPUs: 2, MemoryGB: 3})
	assert.Equal(t, 2, got.ClaimCPUs, "the claim defaults to min(4, the configured cpus)")
	assert.Equal(t, 3, got.ClaimMemoryGB, "the claim defaults to min(6, the configured memory_gb)")
	assert.Equal(t, 14, got.CPUs, "a cap's own default is the host's")
	assert.Equal(t, 22, got.MemoryGB)

	// A claim half that is set does not hide the other half's default, nor
	// its own.
	got = workDefaults(host, config.SchedulerSettings{CPUs: 8, DefaultClaim: &config.ClaimSettings{CPUs: 6}})
	assert.Equal(t, 4, got.ClaimCPUs, "the default is what an empty field would take, not the set value")
	assert.Equal(t, 6, got.ClaimMemoryGB)
}

func TestWorkDefaults_UnreadableRAMWithMemoryConfigured(t *testing.T) {
	noRAM := sched.Host{Name: "odd", Cores: 8}
	s := config.SchedulerSettings{MemoryGB: 4}

	got := workDefaults(noRAM, s)
	assert.Equal(t, 4, got.ClaimMemoryGB, "min(6, the configured memory_gb) is in effect")
	assert.Equal(t, 0, got.MemoryGB, "memory_gb itself has no default without RAM to take it from")
	assert.Equal(t, 6, got.CPUs)
	assert.Equal(t, 4, got.ClaimCPUs)

	assert.Equal(t, 4, s.MemoryGB, "the published block is not edited")
}

// An invalid published block is when the form is opened to fix it: one
// field's fault takes only the defaults that depend on it.
func TestWorkDefaults_AnInvalidBlockKeepsTheDefaultsThatDoNotDependOnTheFault(t *testing.T) {
	host := sched.Host{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}
	normal := overlay.WorkCapsValues{CPUs: 14, MemoryGB: 22, IOJobs: 2, ClaimCPUs: 4, ClaimMemoryGB: 6, StarveMinutes: 10, KeepFinished: 50}

	assert.Equal(t, normal, workDefaults(host, config.SchedulerSettings{IOJobs: 100}),
		"io_jobs over its bound leaves every other default as on a plain host")
	assert.Equal(t, normal, workDefaults(host, config.SchedulerSettings{StarveAfterMinutes: -3, KeepFinished: 99999}))

	got := workDefaults(host, config.SchedulerSettings{CPUs: 5000})
	want := normal
	want.ClaimCPUs = 0 // the claim follows the cpus cap, which is the fault
	assert.Equal(t, want, got, "a fault in cpus takes the claim's cpu default, which follows it, and nothing else")

	got = workDefaults(host, config.SchedulerSettings{MemoryGB: 70000})
	want = normal
	want.ClaimMemoryGB = 0 // the claim's memory follows memory_gb, which is the fault
	assert.Equal(t, want, got, "a fault in memory_gb takes the claim's memory default, which follows it, and not its cores")

	got = workDefaults(host, config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{CPUs: 5000, MemoryGB: 3}})
	assert.Equal(t, normal, got, "a bad claim half hides neither a cap's default nor the other half's")
}

func TestWorkCaps_FormShowsTheDefaultInEffectForTheClaim(t *testing.T) {
	q := testQueue(queueJob(14, sched.Running, "a"))
	q.Settings = config.SchedulerSettings{CPUs: 2}
	m := workHome(t)
	setQueue(t, m, q)
	pressWork(t, m, "e")
	o := m.workCapsOverlay()
	require.NotNil(t, o)
	for _, l := range strings.Split(ansi.Strip(o.Render()), "\n") {
		if strings.Contains(l, "Default claim CPUs") {
			assert.Contains(t, l, "2", "an empty claim field takes min(4, the configured cpus)")
			assert.Contains(t, l, "host default")
			assert.NotContains(t, l, "4", "not the 4 of an unconfigured host")
			return
		}
	}
	t.Fatal("the form has no claim CPUs row")
}

func TestSchedulerSettingsRoundTrip(t *testing.T) {
	for _, s := range []config.SchedulerSettings{
		{},
		{CPUs: 8, MemoryGB: 16, IOJobs: 3, StarveAfterMinutes: 5, KeepFinished: 20},
		{DefaultClaim: &config.ClaimSettings{CPUs: 2, MemoryGB: 3}},
		{DefaultClaim: &config.ClaimSettings{MemoryGB: 3}},
	} {
		assert.Equal(t, s, schedulerSettings(capsValues(s)))
	}
	assert.Nil(t, schedulerSettings(overlay.WorkCapsValues{}).DefaultClaim, "a form with no claim edited leaves no claim block in config.json")
}

func TestWorkCancel_ActsOnTheJobNamedWhenTheKeyPassed(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "polyoxide tests"), queueJob(15, sched.Running, "go test"))
	w := watchWork(t, m)
	require.Equal(t, sched.JobID(14), selectedWork(t, m))

	assert.Nil(t, pressWork(t, m, "x"))
	require.Equal(t, stateConfirm, m.state)
	assert.Contains(t, ansi.Strip(m.confirmation().Render()), `Cancel job 14 "polyoxide tests"?`)
	assert.Empty(t, w.cancels, "nothing is sent before the answer")

	// The queue changes under the dialog: job 14 leaves the view, and the
	// selection moves to the job that took its row.
	setQueue(t, m, testQueue(queueJob(15, sched.Running, "go test")))
	require.Equal(t, sched.JobID(15), selectedWork(t, m))

	_, _ = handleStateConfirmKey(m, keyFor(t, "y"))
	require.Len(t, w.cancels, 1)
	assert.Equal(t, sched.JobID(14), w.cancels[0].id, "the job the dialog named, not the one selected now")
	assert.Equal(t, core.CancelFromView, w.cancels[0].from)
	assert.NotZero(t, w.reqs[0], "the cancel carries a ReqID, so a refusal reaches the user")
	assert.Equal(t, stateDefault, m.state)
	assert.Equal(t, viewWork, m.viewMode)
}

func TestWorkCancel_ACancelledDialogSendsNothing(t *testing.T) {
	for _, answer := range []string{"n", "esc"} {
		t.Run(answer, func(t *testing.T) {
			m := workHome(t, queueJob(14, sched.Running, "a"))
			w := watchWork(t, m)
			pressWork(t, m, "x")
			require.Equal(t, stateConfirm, m.state)
			_, _ = handleStateConfirmKey(m, keyFor(t, answer))
			assert.Empty(t, w.cancels)
			assert.Equal(t, stateDefault, m.state)
		})
	}
}

func TestWorkCancel_QueuedAndRunningJobsCanBeCancelledFinishedOnesCannot(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"), queueJob(12, sched.Done, "c"))
	w := watchWork(t, m)

	pressWork(t, m, "j") // job 16, queued
	pressWork(t, m, "x")
	require.Equal(t, stateConfirm, m.state)
	_, _ = handleStateConfirmKey(m, keyFor(t, "y"))
	require.Len(t, w.cancels, 1)
	assert.Equal(t, sched.JobID(16), w.cancels[0].id)

	pressWork(t, m, "j") // job 12, done
	cmd := pressWork(t, m, "x")
	assert.Nil(t, cmd)
	assert.Equal(t, stateDefault, m.state, "a finished job has nothing to cancel: no dialog")
	assert.Contains(t, m.errBox.String(), "job 12 has finished")
	assert.Len(t, w.cancels, 1)
}

func TestWorkCancel_NothingSelectedNothingAsked(t *testing.T) {
	m := workHome(t)
	assert.Nil(t, pressWork(t, m, "x"))
	assert.Equal(t, stateDefault, m.state)
}

func TestWorkFront_SendsTheSelectedQueuedJobsID(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"), queueJob(17, sched.Queued, "c"))
	w := watchWork(t, m)

	pressWork(t, m, "j")
	pressWork(t, m, "j") // job 17
	assert.Nil(t, pressWork(t, m, "t"))
	assert.Equal(t, []sched.JobID{17}, w.fronts)
	assert.NotZero(t, w.reqs[0])

	pressWork(t, m, "k")
	pressWork(t, m, "k") // job 14, running
	pressWork(t, m, "t")
	assert.Equal(t, []sched.JobID{17}, w.fronts, "a running job is not sent")
	assert.Contains(t, m.errBox.String(), "job 14 isn't waiting")
}

func TestWorkFront_AModelRefusalReachesTheUser(t *testing.T) {
	m := workHome(t, queueJob(16, sched.Queued, "b"))
	w := watchWork(t, m)
	pressWork(t, m, "t")
	require.Len(t, w.fronts, 1)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("job 16 isn't waiting")})
	assert.Contains(t, m.errBox.String(), "job 16 isn't waiting")
}

func TestWorkCancel_ARefusalReachesTheUser(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	w := watchWork(t, m)
	pressWork(t, m, "x")
	_, _ = handleStateConfirmKey(m, keyFor(t, "y"))
	require.Len(t, w.cancels, 1)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("job 14 has finished")})
	assert.Contains(t, m.errBox.String(), "job 14 has finished")
}

func TestWorkActions_OfflineRefusesTheKeysThatNeedTheModel(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"))
	w := watchWork(t, m)
	pressWork(t, m, "j") // job 16: t and x would both act
	m.link.state = linkWaiting
	for _, key := range []string{"x", "t", "o", "e"} {
		m.errBox.Clear()
		cmd := pressWork(t, m, key)
		assert.Nil(t, cmd, key)
		assert.Equal(t, stateDefault, m.state, "%q opens nothing offline", key)
		assert.Contains(t, m.errBox.String(), "the loom daemon is stopped: "+key+" needs it", key)
	}
	assert.Empty(t, w.cancels)
	assert.Empty(t, w.fronts)
	assert.Empty(t, w.caps)

	m.link.state = linkConnected
	m.errBox.Clear()
	pressWork(t, m, "t")
	assert.Equal(t, []sched.JobID{16}, w.fronts, "connected, the same key acts")
}

func TestWorkView_TheStatusLineListsTheKeys(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	assert.Contains(t, ansi.Strip(m.View().Content), "w/esc back · enter log · b belief · x cancel · t to front · o open session · e caps · q quit")
}

func TestFocusSlotInstance_FocusesByIdentity(t *testing.T) {
	m := fleetHome(t)
	f2 := m.slots[0].list.GetInstanceByTitle("f2")
	b1 := m.slots[1].list.GetInstanceByTitle("b1")
	require.NotNil(t, f2)
	require.NotNil(t, b1)

	idx, ok := m.slotShowing(b1.ID)
	assert.True(t, ok)
	assert.Equal(t, 1, idx)
	_, ok = m.slotShowing(core.InstanceID(9999))
	assert.False(t, ok)

	assert.False(t, m.focusSlotInstance(0, b1.ID), "the row is not in that slot: nothing changes")
	assert.Equal(t, 0, m.focusedSlot)
	assert.True(t, m.focusSlotInstance(0, f2.ID))
	assert.Equal(t, f2.ID, selID(m.list))
	assert.True(t, m.focusSlotInstance(1, b1.ID))
	assert.Equal(t, 1, m.focusedSlot)
	assert.Equal(t, b1.ID, selID(m.list))
	require.NoError(t, m.checkSlotInvariant())
}

func TestFocusSlotInstance_ClassicModeHasOneSlot(t *testing.T) {
	m := newTestHome(t)
	a := mustAddInstance(t, m, "a")
	id := idOf(m, a)
	idx, ok := m.slotShowing(id)
	assert.True(t, ok)
	assert.Equal(t, 0, idx)
	assert.True(t, m.focusSlotInstance(0, id))
	assert.Equal(t, id, selID(m.list))
	assert.False(t, m.focusSlotInstance(0, core.InstanceID(9999)))
}

func TestWorkOpen_FocusesTheOwnersSessionInItsWorkspace(t *testing.T) {
	m := fleetHome(t)
	m.errBox = ui.NewErrBox()
	m.errBox.SetSize(400, 1)
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
	b1 := m.slots[1].list.GetInstanceByTitle("b1")
	require.NotNil(t, b1)
	job := queueJob(14, sched.Running, "tests")
	job.Owner, job.OwnerTitle, job.OwnerWorkspace = b1.ID, "b1", "bpeer"
	job.OwnerWorkspaceID = 2 // any served ID: the workspace is already open, so the row is found by its InstanceID in the slots and no tab is opened
	setQueue(t, m, testQueue(job))
	m.enterWorkView()
	require.Equal(t, viewWork, m.viewMode)
	require.Equal(t, 0, m.focusedSlot)

	pressWork(t, m, "o")
	assert.Equal(t, viewFocus, m.viewMode, "o drops to the focus layout")
	assert.Equal(t, 1, m.focusedSlot, "the owner's workspace is focused")
	assert.Equal(t, b1.ID, selID(m.list), "and its session selected")
	require.NoError(t, m.checkSlotInvariant())
}

// ownerHome is a TUI built through the production startup over two
// registered, served workspaces, ws-a and ws-b. In classic mode none is a
// tab; in tabs mode ws-a is. ws-b holds one session, b1, and the work view
// shows a job b1 owns. The workspace IDs are returned with it.
func ownerHome(t *testing.T, tabs bool) (m *home, b1 core.InstanceID, bID core.WorkspaceID) {
	t.Helper()
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	m = newRestoreHome(t, &recordingExec{})
	a, b := preservedTerminalWorkspace(t, "ws-a"), preservedTerminalWorkspace(t, "ws-b")
	registerWorkspaces(t, m, a, b)
	if tabs {
		_, err := m.activateWorkspace(a)
		require.NoError(t, err)
		require.Equal(t, []string{"ws-a"}, m.slotNames())
	}
	bID, err := m.servedID("ws-b")
	require.NoError(t, err)
	_, err = testModel(m).Open(bID) // loaded in the model, shown in no tab
	require.NoError(t, err)
	inst := &session.Instance{Title: "b1", Status: session.Running}
	testModel(m).WorkspaceForTest(bID).AddForTest(inst)
	b1 = idOf(m, inst)
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m, b1, bID
}

// ownedJob is a running job owned by owner, a session of workspace ws.
func ownedJob(owner core.InstanceID, ws string, wsID core.WorkspaceID) core.JobView {
	job := queueJob(14, sched.Running, "tests")
	job.Owner, job.OwnerTitle, job.OwnerWorkspace, job.OwnerWorkspaceID = owner, "b1", ws, wsID
	return job
}

// openListOnDisk is the registry's open list as the next launch reads it.
func openListOnDisk(t *testing.T) []string {
	t.Helper()
	fresh, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	return fresh.OpenWorkspaces
}

func TestWorkOpen_OpensTheOwnersWorkspaceAsATabAndFocusesIt(t *testing.T) {
	for name, tabs := range map[string]bool{"classic mode": false, "tabs mode": true} {
		t.Run(name, func(t *testing.T) {
			m, b1, bID := ownerHome(t, tabs)
			setQueue(t, m, testQueue(ownedJob(b1, "ws-b", bID)))
			m.enterWorkView()
			require.Equal(t, viewWork, m.viewMode)
			require.Len(t, m.slots, map[bool]int{false: 0, true: 1}[tabs], "fixture: ws-b is no tab")

			cmd := pressWork(t, m, "o")
			drainCmd(cmd)

			wantNames := map[bool][]string{false: {"ws-b"}, true: {"ws-a", "ws-b"}}[tabs]
			assert.Equal(t, wantNames, m.slotNames(), "the owner's workspace is a new tab")
			assert.Equal(t, viewFocus, m.viewMode, "o drops to the focus layout")
			assert.Equal(t, "ws-b", m.name(), "the new tab is focused")
			assert.Equal(t, b1, selID(m.list), "with the owner selected")
			assert.Equal(t, wantNames, openListOnDisk(t), "and the open list persisted")
			require.NoError(t, m.checkSlotInvariant())
		})
	}
}

func TestWorkOpen_NoTabForTheGlobalWorkspace(t *testing.T) {
	m, _, _ := ownerHome(t, true)
	var globalID core.WorkspaceID
	for _, v := range m.core.Workspaces() {
		if v.Name == "" {
			globalID = v.ID
		}
	}
	require.NotZero(t, globalID, "fixture: the model serves the global workspace")
	g := &session.Instance{Title: "g1", Status: session.Running}
	testModel(m).WorkspaceForTest(globalID).AddForTest(g)
	setQueue(t, m, testQueue(ownedJob(idOf(m, g), "global", globalID)))
	m.enterWorkView()
	before := openListOnDisk(t)

	drainCmd(pressWork(t, m, "o"))

	assert.Equal(t, viewWork, m.viewMode, "the view stays open")
	assert.Equal(t, []string{"ws-a"}, m.slotNames(), "no tab named \"\"")
	assert.Equal(t, before, openListOnDisk(t))
	assert.Contains(t, m.errBox.String(), "job 14's session is in the global workspace, which no open tab shows")
	require.NoError(t, m.checkSlotInvariant())
}

func TestWorkOpen_NoSecondSlotForAWorkspaceAlreadyOpen(t *testing.T) {
	t.Run("tabs mode", func(t *testing.T) {
		m, _, _ := ownerHome(t, true)
		aID := m.slots[0].id
		// The owner is in ws-a, which a slot shows, but its rail lacks the row.
		setQueue(t, m, testQueue(ownedJob(core.InstanceID(9999), "ws-a", aID)))
		m.enterWorkView()
		before := openListOnDisk(t)

		drainCmd(pressWork(t, m, "o"))

		assert.Equal(t, viewWork, m.viewMode, "the view stays open")
		assert.Equal(t, []string{"ws-a"}, m.slotNames(), "no second slot for ws-a")
		assert.Equal(t, before, openListOnDisk(t))
		assert.Contains(t, m.errBox.String(), "job 14's session isn't in workspace ws-a")
		require.NoError(t, m.checkSlotInvariant())
	})
	t.Run("classic mode", func(t *testing.T) {
		isolateTmux(t)
		t.Setenv(config.EnvGlobalDir, t.TempDir())
		reg, err := config.LoadWorkspaceRegistry()
		require.NoError(t, err)
		a := preservedTerminalWorkspace(t, "ws-a")
		require.NoError(t, reg.Add(a.Name, a.Path))
		m := startupHome(t, &recordingExec{}, reg, "ws-a", "")
		m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
		aID, err := m.servedID("ws-a")
		require.NoError(t, err)
		require.Empty(t, m.slots, "fixture: classic mode")
		require.Equal(t, aID, m.workspaceSlot.id, "fixture: the classic slot shows ws-a")
		// The owner is in ws-a, which the classic slot shows, but its rail
		// lacks the row.
		setQueue(t, m, testQueue(ownedJob(core.InstanceID(9999), "ws-a", aID)))
		m.enterWorkView()
		before := openListOnDisk(t)

		drainCmd(pressWork(t, m, "o"))

		assert.Equal(t, viewWork, m.viewMode, "the view stays open")
		assert.Empty(t, m.slots, "no tab opened for the workspace the classic slot shows")
		assert.Equal(t, before, openListOnDisk(t))
		assert.Contains(t, m.errBox.String(), "job 14's session isn't in workspace ws-a")
		require.NoError(t, m.checkSlotInvariant())
	})
}

func TestWorkOpen_SaysWhyItCannot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*core.JobView)
		want   string
	}{
		{"no owner", func(j *core.JobView) {}, "job 14 has no owner"},
		{"an owner that is not served (no workspace ID)", func(j *core.JobView) { j.Owner = 77 }, "job 14's session isn't served"},
		{"an owner whose workspace the model no longer serves", func(j *core.JobView) {
			j.Owner, j.OwnerTitle, j.OwnerWorkspace, j.OwnerWorkspaceID = 77, "g", "ghost", 77
		}, "serves no such workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := queueJob(14, sched.Running, "tests")
			tc.mutate(&job)
			m := workHome(t, job)
			pressWork(t, m, "o")
			assert.Equal(t, viewWork, m.viewMode, "the view stays")
			assert.Contains(t, m.errBox.String(), tc.want)
		})
	}
}

func TestToggleWorkView_ScriptHostEntersAndLeaves(t *testing.T) {
	m := newTestHome(t)
	toggle := func() {
		host := newScriptHost(m)
		host.ToggleWorkView()
		_, _, actions := host.drain()
		require.Len(t, actions, 1, "ToggleWorkView must defer exactly one model mutation")
		_ = m.handleScriptDone(scriptDoneMsg{pendingActions: actions})
	}

	toggle()
	assert.Equal(t, viewWork, m.viewMode)
	toggle()
	assert.Equal(t, viewFocus, m.viewMode)

	m.state = stateSettings
	toggle()
	assert.Equal(t, viewFocus, m.viewMode, "a dialog that opened meanwhile keeps the layout under it")
}

func TestWKey_TogglesTheWorkViewThroughTheScriptEngine(t *testing.T) {
	m := newTestHome(t)
	press := func(key string) {
		t.Helper()
		_, cmd := handleStateDefaultKey(m, keyFor(t, key))
		require.NotNil(t, cmd, "%q dispatches to the script engine", key)
		msg, ok := cmd().(scriptDoneMsg)
		require.True(t, ok)
		require.NoError(t, msg.err)
		_ = m.handleScriptDone(msg)
	}

	press("w")
	assert.Equal(t, viewWork, m.viewMode, "w enters from focus")
	press("w")
	assert.Equal(t, viewFocus, m.viewMode, "and leaves")

	m.viewMode = viewOverview
	press("w")
	assert.Equal(t, viewWork, m.viewMode, "w enters from overview")
	press("w")
	assert.Equal(t, viewOverview, m.viewMode, "and returns to it")
}

func TestWKey_TheWorkbenchDropsIt(t *testing.T) {
	m := newTestHome(t)
	m.viewMode = viewWorkbench
	_, cmd := handleStateDefaultKey(m, keyFor(t, "w"))
	assert.Nil(t, cmd)
	assert.Equal(t, viewWorkbench, m.viewMode)
}

func TestWKey_WorksOffline(t *testing.T) {
	m := newTestHome(t)
	m.link.state = linkWaiting
	for _, want := range []viewMode{viewWork, viewFocus} {
		_, cmd := handleStateDefaultKey(m, keyFor(t, "w"))
		require.NotNil(t, cmd, "w only moves the TUI: the offline gate lets it through")
		msg, ok := cmd().(scriptDoneMsg)
		require.True(t, ok)
		_ = m.handleScriptDone(msg)
		assert.Equal(t, want, m.viewMode)
	}
}

func TestWorkView_TheQueueStaysReadableOffline(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"))
	m.link.state = linkWaiting
	pressWork(t, m, "j")
	assert.Equal(t, sched.JobID(16), selectedWork(t, m), "the last replica can still be walked")
	pressWork(t, m, "enter")
	assert.NotNil(t, m.work.pager, "and its logs opened")
	pressWork(t, m, "esc")
	pressWork(t, m, "esc")
	assert.Equal(t, viewFocus, m.viewMode, "esc leaves")
}

// twoTabsWorkPrefs opens tabs ws-a and ws-b with ws-a focused and ws-a's
// persisted view mode empty (focus), and gives ws-b the persisted view mode
// bPrefs, as a user who left that tab in overview did.
func twoTabsWorkPrefs(t *testing.T, bPrefs string) *home {
	t.Helper()
	m, _ := twoTabsWithPendingRatio(t)
	m.loadSlot(1)
	m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = bPrefs })
	m.loadSlot(0)
	require.Equal(t, "ws-a", m.name())
	require.Equal(t, bPrefs, m.slots[1].uiPrefs().ViewMode, "fixture: ws-b's prefs hold the view mode")
	require.Equal(t, viewFocus, m.viewMode, "fixture: ws-a's prefs choose focus")
	return m
}

func TestWorkView_StaysOpenAcrossASlotLoad(t *testing.T) {
	cases := []struct {
		name     string
		bPrefs   string
		cameFrom viewMode
		want     viewMode
	}{
		{"prefs choose overview", "overview", viewFocus, viewOverview},
		{"prefs choose focus", "", viewOverview, viewFocus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			load := map[string]func(m *home){
				"loadSlot":      func(m *home) { m.loadSlot(1) },
				"closing a tab": func(m *home) { _, err := m.deactivateWorkspace("ws-a"); require.NoError(t, err) },
			}
			for path, run := range load {
				t.Run(path, func(t *testing.T) {
					m := twoTabsWorkPrefs(t, tc.bPrefs)
					m.viewMode = tc.cameFrom
					m.enterWorkView()
					require.Equal(t, viewWork, m.viewMode)
					m.work.sel, m.work.selIdx = 14, 2
					m.work.pager = &workPager{id: 14}

					run(m)

					require.Equal(t, "ws-b", m.name(), "fixture: the other slot is focused")
					assert.Equal(t, viewWork, m.viewMode, "a slot load must not close the work view")
					assert.Equal(t, tc.want, m.work.from, "w and esc return to the mode the loaded slot's prefs choose")
					assert.Equal(t, sched.JobID(14), m.work.sel, "the selection is the job's, not the slot's")
					assert.Equal(t, 2, m.work.selIdx)
					require.NotNil(t, m.work.pager, "the pager stays open")
					assert.Equal(t, sched.JobID(14), m.work.pager.id)

					m.toggleWorkView()
					assert.Equal(t, tc.want, m.viewMode, "w returns to the mode the slot's prefs chose")
				})
			}
		})
	}
}

func TestApplyUIPrefs_OutsideTheWorkViewStillChoosesTheMode(t *testing.T) {
	for _, from := range []viewMode{viewFocus, viewOverview, viewWorkbench} {
		for prefs, want := range map[string]viewMode{"overview": viewOverview, "": viewFocus} {
			m := newTestHome(t)
			require.NoError(t, m.appState().SetUIPrefs(config.UIPrefs{ViewMode: prefs}))
			m.syncWorkspaces()
			m.viewMode = from
			m.applyUIPrefs()
			assert.Equal(t, want, m.viewMode, "from mode %d with prefs %q", from, prefs)
		}
	}
}

// heldWorkJob is a job held after job after.
func heldWorkJob(id, after sched.JobID, desc string) core.JobView {
	j := queueJob(id, sched.Held, desc)
	j.Started, j.After = time.Time{}, after
	return j
}

func TestWorkCancelPrompt_NamesTheJobsHeldAfterIt(t *testing.T) {
	jobs := []core.JobView{
		queueJob(14, sched.Running, "build"),
		heldWorkJob(15, 14, "tests"),
		heldWorkJob(16, 15, "bench"),
		heldWorkJob(17, 99, "other"),
	}
	assert.Equal(t, `Cancel job 14 "build" and jobs 15 and 16 that wait on it?`, cancelPrompt(jobs[0], jobs))
	assert.Equal(t, `Cancel job 15 "tests" and job 16 that waits on it?`, cancelPrompt(jobs[1], jobs))
	assert.Equal(t, `Cancel job 16 "bench"?`, cancelPrompt(jobs[2], jobs))
}

func TestWorkFront_AHeldJobCanMoveToo(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), heldWorkJob(16, 14, "b"))
	w := watchWork(t, m)
	pressWork(t, m, "j") // job 16, held
	assert.Nil(t, pressWork(t, m, "t"))
	assert.Equal(t, []sched.JobID{16}, w.fronts)
}
