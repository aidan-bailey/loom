package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var workNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func workRunning(id sched.JobID, desc string, started time.Duration) core.JobView {
	j := core.JobView{ID: id, State: sched.Running, Description: desc, OwnerTitle: "kalshi", OwnerWorkspace: "polyoxide"}
	j.Owner = 7
	j.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}
	j.Started = workNow.Add(-started)
	j.Submitted = j.Started.Add(-time.Minute)
	j.CPU = 3.2
	j.PeakRSSKB = 5_347_738
	return j
}

func workQueued(id sched.JobID, desc, waiting string, waited time.Duration) core.JobView {
	j := core.JobView{ID: id, State: sched.Queued, Description: desc, OwnerTitle: "wasm", OwnerWorkspace: "polyoxide", Waiting: waiting}
	j.Owner = 8
	j.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}
	j.Submitted = workNow.Add(-waited)
	return j
}

func workFinished(id sched.JobID, desc string, state sched.State, code int, ago, took time.Duration) core.JobView {
	j := core.JobView{ID: id, State: state, Description: desc, OwnerTitle: "wasm", OwnerWorkspace: "polyoxide"}
	j.Owner = 8
	j.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}
	j.Ended = workNow.Add(-ago)
	j.Started = j.Ended.Add(-took)
	j.Submitted = j.Started.Add(-time.Minute)
	j.Exit = &sched.Exit{Code: code, Ended: j.Ended}
	return j
}

// workBusy is the busy queue of the design's mock: two running jobs, two
// queued ones and two finished.
func workBusy() core.QueueView {
	q := core.QueueView{Dir: "/home/x/.loom/jobs"}
	q.Host = sched.Host{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}
	q.Policy.Caps = sched.Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}
	q.Claimed = sched.Need{CPUs: 10, MemoryGB: 12, IOSlots: 1}
	q.UsedCPU, q.UsedRSSKB = 11.2, 10_276_045
	r15 := workRunning(15, "go test ./...", 42*time.Second)
	r15.OwnerTitle, r15.OwnerWorkspace = "work-scheduler", "loom"
	r15.Claim = sched.Claim{CPUs: 4, MemoryGB: 4}
	r15.PeakRSSKB = 1_258_291
	q16 := workQueued(16, "release build", "needs 8 cores (4 free)", 2*time.Minute)
	q16.Claim = sched.Claim{CPUs: 8, MemoryGB: 12, IO: true}
	q17 := workQueued(17, "bench orderbook", "waits for job 16, which has waited past 10m", time.Minute)
	q17.Claim = sched.Claim{Exclusive: true}
	q.Jobs = []core.JobView{
		workRunning(14, "polyoxide tests", 190*time.Second),
		r15,
		q16,
		q17,
		workFinished(13, "clippy", sched.Done, 0, 2*time.Minute, 62*time.Second),
		workFinished(12, "cargo test", sched.Failed, 101, 10*time.Minute, 273*time.Second),
	}
	return q
}

func lineWith(t *testing.T, out, sub string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	t.Fatalf("no line holds %q in:\n%s", sub, out)
	return ""
}

func TestWorkRows_SectionsInOrderAndRecentCapped(t *testing.T) {
	var jobs []core.JobView
	for i := 1; i <= 12; i++ { // finished, newest first, as the queue view lists them
		jobs = append(jobs, workFinished(sched.JobID(100-i), "f", sched.Done, 0, time.Duration(i)*time.Minute, time.Second))
	}
	jobs = append(jobs, workQueued(30, "q30", "", time.Minute), workRunning(20, "r20", time.Minute), workQueued(31, "q31", "", time.Minute), workRunning(21, "r21", time.Minute))

	var ids []sched.JobID
	for _, j := range WorkRows(jobs) {
		ids = append(ids, j.ID)
	}
	assert.Equal(t, []sched.JobID{20, 21, 30, 31, 99, 98, 97, 96, 95, 94, 93, 92, 91, 90}, ids,
		"running, queued, then the newest ten finished, each section in the order the view gave")
}

func TestRenderWork_ShowsTheBarsTheSectionsAndTheTail(t *testing.T) {
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: workBusy(), Selected: 14, Tail: WorkTail{ID: 14, Lines: []string{"test orderbook::depth ... ok"}}, Now: workNow}))
	for _, want := range []string{
		"Work queue · fresco", "w/esc back",
		"CPU ██████████░░░░ 10/14 claimed · 11.2 in use",
		"MEM ████████░░░░░░ 12/22 GB claimed · 9.8 GB in use",
		"I/O ██░░", "1/2",
		"polyoxide tests", "kalshi · polyoxide", "4c 6G", "3m10s", "peak 5.1G",
		"go test ./...", "work-scheduler · loom", "0m42s", "peak 1.2G",
		"release build", "8c 12G io", "needs 8 cores (4 free)",
		"bench orderbook", "exclusive", "waits for job 16",
		"clippy", "✓ 0", "1m02s", "2m ago",
		"cargo test", "✗ 101", "4m33s", "10m ago",
		"── job 14 · tail", "test orderbook::depth ... ok",
	} {
		assert.Contains(t, out, want)
	}
	rest := out
	for _, section := range []string{"RUNNING", "QUEUED", "RECENT"} {
		i := strings.Index(rest, section)
		require.GreaterOrEqual(t, i, 0, section)
		rest = rest[i+len(section):]
	}
}

func TestRenderWork_TheMarkerFollowsTheSelectedJobsID(t *testing.T) {
	for _, id := range []sched.JobID{14, 15, 16} {
		out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: workBusy(), Selected: id, Now: workNow}))
		var marked []string
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "▸") {
				marked = append(marked, line)
			}
		}
		require.Len(t, marked, 1, "job %d", id)
		assert.Contains(t, marked[0], fmt.Sprintf(" %d ", id))
	}
}

func TestRenderWork_AWindowKeepsTheSelectedJobInView(t *testing.T) {
	q := core.QueueView{Dir: "/d"}
	q.Host = sched.Host{Name: "h", Cores: 4}
	q.Policy.Caps = sched.Caps{CPUs: 2, MemoryGB: 2, IOJobs: 1}
	for i := 1; i <= 20; i++ {
		q.Jobs = append(q.Jobs, workRunning(sched.JobID(i), fmt.Sprintf("d%02d", i), time.Minute))
	}
	last := ansi.Strip(RenderWork(120, 14, WorkData{Queue: q, Selected: 20, Now: workNow}))
	assert.Contains(t, last, "d20")
	assert.NotContains(t, last, "d01")
	first := ansi.Strip(RenderWork(120, 14, WorkData{Queue: q, Selected: 1, Now: workNow}))
	assert.Contains(t, first, "d01")
	assert.NotContains(t, first, "d20")
}

func TestRenderWork_FillsExactlyItsBox(t *testing.T) {
	busy := WorkData{Queue: workBusy(), Selected: 14, Tail: WorkTail{ID: 14, Lines: []string{"x"}}, Now: workNow}
	silent := WorkData{Queue: core.QueueView{}, Now: workNow}
	for _, size := range [][2]int{{120, 30}, {100, 24}, {60, 12}, {40, 8}} {
		for _, d := range []WorkData{busy, silent} {
			out := RenderWork(size[0], size[1], d)
			assert.Equal(t, size[1], lipgloss.Height(out), "height at %v", size)
			assert.LessOrEqual(t, lipgloss.Width(out), size[0], "width at %v", size)
		}
	}
}

func TestRenderWork_PausedShowsTheReasonInPlaceOfTheBars(t *testing.T) {
	q := workBusy()
	q.Paused = "scheduler.cpus is 2000: it must be 1–1024"
	q.Policy = sched.Policy{}
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Selected: 14, Now: workNow}))
	assert.Contains(t, out, "Admission paused: scheduler.cpus is 2000: it must be 1–1024")
	assert.NotContains(t, out, "claimed")
	assert.Contains(t, out, "polyoxide tests", "running jobs stay listed")
}

func TestRenderWork_NothingToShow(t *testing.T) {
	q := workBusy()
	q.Jobs = nil
	assert.Contains(t, ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Now: workNow})), "No jobs yet")
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: core.QueueView{}, Now: workNow}))
	assert.Contains(t, out, "has not reported its work queue yet")
	assert.NotContains(t, out, "No jobs yet", "a queue that never reported is not an empty one")
}

func TestRenderWork_TextAnAgentWroteCannotReachTheTerminal(t *testing.T) {
	q := workBusy()
	q.Jobs[0].Description = "x\x1b[2Jevil\nnewline\x07"
	q.Jobs[0].OwnerTitle = "t\x1b]52;c;AAAA\x07"
	q.Jobs[2].Waiting = "w\x1b[31mred"
	out := RenderWork(120, 30, WorkData{Queue: q, Selected: 14, Now: workNow})
	for _, injected := range []string{"\x1b[2J", "\x1b]52", "\x07", "\x1b[31mred"} {
		assert.NotContains(t, out, injected)
	}
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "evil newline", "the text stays, its controls are spaces")
	assert.Equal(t, 30, lipgloss.Height(out), "a newline in a description cannot add a row")
}

func TestRenderWork_AnUndeliveredNoticeIsMarked(t *testing.T) {
	q := workBusy()
	a := workFinished(20, "alpha", sched.Done, 0, time.Minute, time.Second)
	a.Notice, a.NoticeWhy = sched.NoticeUndelivered, "no live loom mod"
	b := workFinished(21, "bravo", sched.Done, 0, time.Minute, time.Second)
	b.Notice = sched.NoticeSent
	q.Jobs = []core.JobView{a, b}
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Selected: 20, Now: workNow}))
	assert.Contains(t, lineWith(t, out, "alpha"), "not delivered")
	assert.NotContains(t, lineWith(t, out, "bravo"), "not delivered")
	assert.Contains(t, lineWith(t, out, "── job 20"), "message not delivered: no live loom mod")
}

func TestRenderWork_FailedAndCancelledJobsSayHow(t *testing.T) {
	q := workBusy()
	start := workFinished(30, "nostart", sched.Failed, 0, time.Minute, 0)
	start.Exit, start.Started, start.Failure = nil, time.Time{}, "its folder is gone"
	killed := workFinished(31, "killed", sched.Failed, -1, time.Minute, time.Second)
	killed.Exit.Signal = "SIGKILL"
	cancelled := workFinished(32, "stopped", sched.Cancelled, 0, time.Minute, time.Second)
	cancelled.CancelReason = "from the work view"
	lost := workFinished(33, "vanished", sched.Lost, 0, time.Minute, time.Second)
	lost.Exit = nil
	q.Jobs = []core.JobView{start, killed, cancelled, lost}
	out := ansi.Strip(RenderWork(140, 30, WorkData{Queue: q, Now: workNow}))
	assert.Contains(t, lineWith(t, out, "nostart"), "✗ start")
	assert.Contains(t, lineWith(t, out, "nostart"), "its folder is gone")
	assert.Contains(t, lineWith(t, out, "killed"), "✗ SIGKILL")
	assert.Contains(t, lineWith(t, out, "stopped"), "cancelled")
	assert.Contains(t, lineWith(t, out, "stopped"), "from the work view")
	assert.Contains(t, lineWith(t, out, "vanished"), "lost")
}

func TestRenderWork_ACancelInFlightSaysSo(t *testing.T) {
	q := workBusy()
	q.Jobs[0].CancelReason = "from the work view"
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Selected: 14, Now: workNow}))
	assert.Contains(t, lineWith(t, out, "polyoxide tests"), "cancelling")
	assert.NotContains(t, lineWith(t, out, "go test ./..."), "cancelling")
}

func TestRenderWork_TheTailSaysWhatItCannotShow(t *testing.T) {
	render := func(selected sched.JobID, tail WorkTail) string {
		return ansi.Strip(RenderWork(120, 30, WorkData{Queue: workBusy(), Selected: selected, Tail: tail, Now: workNow}))
	}
	assert.Contains(t, render(16, WorkTail{}), "(queued: no output yet)")
	assert.Contains(t, render(14, WorkTail{ID: 15, Lines: []string{"another job"}}), "(reading the log…)", "a tail read for another job is not shown")
	assert.NotContains(t, render(14, WorkTail{ID: 15, Lines: []string{"another job"}}), "another job")
	assert.Contains(t, render(14, WorkTail{ID: 14, Err: "permission denied"}), "cannot read the log: permission denied")
	assert.Contains(t, render(14, WorkTail{ID: 14}), "(no output yet)")

	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("l%d", i))
	}
	long := render(14, WorkTail{ID: 14, Lines: lines})
	assert.Contains(t, long, "l20")
	assert.Contains(t, long, "l13")
	assert.NotContains(t, long, "l12", "the tail keeps the last lines that fit")
}

func TestWorkReported(t *testing.T) {
	assert.False(t, WorkReported(core.QueueView{}))
	assert.True(t, WorkReported(core.QueueView{Host: sched.Host{Name: "fresco"}}))
	assert.True(t, WorkReported(core.QueueView{Dir: "/d"}))
	assert.True(t, WorkReported(core.QueueView{Jobs: []core.JobView{{ID: 1}}}))
}

func TestRenderWorkPager_ShowsTheWindowAndClampsTheTop(t *testing.T) {
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	job := workRunning(14, "polyoxide tests", time.Minute)
	body := WorkPagerBody(12)
	require.Equal(t, 11, body)

	out := ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job, Lines: lines, Top: 20}))
	assert.Equal(t, 12, lipgloss.Height(out))
	assert.Contains(t, out, "job 14 · polyoxide tests · lines 21–31 of 100")
	assert.Contains(t, out, "esc back")
	assert.Contains(t, out, "line 21")
	assert.Contains(t, out, "line 31")
	assert.NotContains(t, out, "line 20\n")
	assert.NotContains(t, out, "line 32")

	end := ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job, Lines: lines, Top: 1000}))
	assert.Contains(t, end, "lines 90–100 of 100", "a top past the end shows the last page")
	assert.Contains(t, end, "line 100")

	omitted := ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job, Lines: lines, Omitted: true}))
	assert.Contains(t, omitted, "earlier output not shown")
}

func TestRenderWorkPager_NothingToShow(t *testing.T) {
	job := workRunning(14, "polyoxide tests", time.Minute)
	assert.Contains(t, ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job})), "(no output yet)")
	failed := ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job, Err: "permission denied"}))
	assert.Contains(t, failed, "cannot read the log: permission denied")
	assert.Equal(t, 12, lipgloss.Height(failed))
	for _, size := range [][2]int{{80, 12}, {30, 5}, {20, 1}} {
		out := RenderWorkPager(size[0], size[1], WorkPagerData{Job: job, Lines: []string{strings.Repeat("x", 300)}})
		assert.Equal(t, size[1], lipgloss.Height(out), "height at %v", size)
		assert.LessOrEqual(t, lipgloss.Width(out), size[0], "a long line is cut to the width at %v", size)
	}
}

func TestLogLines(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		partial bool
		want    []string
	}{
		{"lines", "a\nb\n", false, []string{"a", "b"}},
		{"no final newline", "a\nb", false, []string{"a", "b"}},
		{"crlf", "a\r\nb\r\n", false, []string{"a", "b"}},
		{"a cut head drops its first line", "half\nfull\n", true, []string{"full"}},
		{"a whole head keeps its first line", "whole\nfull\n", false, []string{"whole", "full"}},
		{"colors", "\x1b[31mred\x1b[0m\n", false, []string{"red"}},
		{"a progress bar keeps its last state", "10%\r50%\r100%\ndone\n", false, []string{"100%", "done"}},
		{"tabs", "a\tb\n", false, []string{"a    b"}},
		{"controls", "bell\x07\n", false, []string{"bell"}},
		{"a blank line stays", "a\n\nb\n", false, []string{"a", "", "b"}},
		{"nothing", "", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, LogLines([]byte(tc.in), tc.partial))
		})
	}
}

func TestJobLabel(t *testing.T) {
	assert.Equal(t, `job 14 "polyoxide tests"`, JobLabel(core.JobView{ID: 14, Description: "polyoxide tests"}))
	assert.Equal(t, `job 3 "a b"`, JobLabel(core.JobView{ID: 3, Description: "a\x1b[2J\nb"}), "controls become spaces")
	long := JobLabel(core.JobView{ID: 3, Description: strings.Repeat("x", 100)})
	assert.Equal(t, `job 3 "`+strings.Repeat("x", 59)+`…"`, long, "a long description is cut to 60 columns")
}

func TestWorkFormatters(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		dur  string
		wait string
		ago  string
	}{
		{10 * time.Second, "0m10s", "<1m", "just now"},
		{42 * time.Second, "0m42s", "<1m", "just now"},
		{62 * time.Second, "1m02s", "1m", "1m ago"},
		{190 * time.Second, "3m10s", "3m", "3m ago"},
		{65 * time.Minute, "1h05m", "1h05m", "1h ago"},
		{72 * time.Hour, "72h00m", "72h00m", "3d ago"},
	} {
		assert.Equal(t, tc.dur, workDur(tc.d), "dur %v", tc.d)
		assert.Equal(t, tc.wait, workWait(tc.d), "wait %v", tc.d)
		assert.Equal(t, tc.ago, workAgo(tc.d), "ago %v", tc.d)
	}
	assert.Equal(t, "0m00s", workDur(-time.Second), "a clock that ran backwards shows zero")
	assert.Equal(t, "5.1G", workKB(5_347_738))
	assert.Equal(t, "640M", workKB(640<<10))
	assert.Equal(t, "12K", workKB(12))
	assert.Equal(t, "9.8 GB", workGB(10_276_045))
}
