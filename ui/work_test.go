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
	// 148 columns: wide enough for the owner beside the source and ETA columns.
	out := ansi.Strip(RenderWork(148, 30, WorkData{Queue: workBusy(), Selected: 14, Tail: WorkTail{ID: 14, Lines: []string{"test orderbook::depth ... ok"}}, Now: workNow}))
	for _, want := range []string{
		"Work queue · fresco", "w/esc back",
		"CPU ██████████░░░░ 10/14 claimed · 11.2 in use",
		"MEM ████████░░░░░░ 12/22 GB claimed · 9.8 GB in use",
		"I/O ██░░", "1/2",
		"polyoxide tests", "kalshi · polyoxide", "4c/6G", "3m10s", "peak 5.1G",
		"go test ./...", "work-scheduler · loom", "0m42s", "peak 1.2G",
		"release build", "8c/12G io", "needs 8 cores (4 free)",
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
	exec := workFinished(34, "noexec", sched.Failed, 127, time.Minute, time.Second)
	exec.Exit.Error = `exec: "nope": executable file not found in $PATH`
	exec.Failure = exec.Exit.Error
	q.Jobs = []core.JobView{start, killed, cancelled, lost, exec}
	out := ansi.Strip(RenderWork(140, 30, WorkData{Queue: q, Now: workNow}))
	assert.Contains(t, lineWith(t, out, "noexec"), "✗ start", "an exec failure is a job that did not start, not an exit 127")
	assert.NotContains(t, lineWith(t, out, "noexec"), "✗ 127")
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

func TestRenderWorkPager_TitleShowsTheCommandAndFolder(t *testing.T) {
	job := workRunning(14, "polyoxide tests", time.Minute)
	job.Argv = []string{"cargo", "test", "--release"}
	job.Cwd = "/home/me/polyoxide"
	lines := []string{"a", "b"}

	out := ansi.Strip(RenderWorkPager(120, 12, WorkPagerData{Job: job, Lines: lines}))
	title := strings.Split(out, "\n")[0]
	assert.Contains(t, title, "job 14 · polyoxide tests · lines 1–2 of 2 · cargo test --release · in /home/me/polyoxide")
	assert.Contains(t, title, "esc back", "the hint keeps the right edge")

	job.Argv = []string{"echo", "a\x1b[31mred\x1b[0m\x07b\nc"}
	job.Cwd = "/d\x1b]0;evil\x07ir"
	raw := RenderWorkPager(120, 12, WorkPagerData{Job: job, Lines: lines})
	title = strings.Split(ansi.Strip(raw), "\n")[0]
	assert.Contains(t, title, "echo ared b c · in /dir")
	assert.NotContains(t, raw, "\x07")
	assert.NotContains(t, raw, "evil")
	assert.Equal(t, 12, lipgloss.Height(raw))

	job.Argv = []string{"cargo", "test", "--release", "--", strings.Repeat("x", 300)}
	for _, width := range []int{120, 70, 50, 30} {
		out := RenderWorkPager(width, 12, WorkPagerData{Job: job, Lines: lines})
		title := strings.Split(ansi.Strip(out), "\n")[0]
		assert.LessOrEqual(t, ansi.StringWidth(title), width, "the title is cut to the width at %d", width)
		assert.Equal(t, 12, lipgloss.Height(out))
	}
	narrow := strings.Split(ansi.Strip(RenderWorkPager(70, 12, WorkPagerData{Job: job, Lines: lines})), "\n")[0]
	assert.Contains(t, narrow, "…", "a command that does not fit is cut with an ellipsis")
	assert.Contains(t, narrow, "esc back", "and the hint is kept")

	bare := workRunning(15, "bare", time.Minute)
	assert.NotContains(t, ansi.Strip(RenderWorkPager(120, 12, WorkPagerData{Job: bare, Lines: lines})), "· in ")
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
		{"a cut head with one line keeps it", "10%\r50%\r100%", true, []string{"100%"}},
		{"a cut head with one line and a final newline keeps it", "10%\r50%\r100%\n", true, []string{"100%"}},
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

func TestRenderWork_ShowsEachClaimsSourceAndWhyLearningIsOff(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	q := core.QueueView{
		Host:     sched.Host{Name: "fresco"},
		Policy:   sched.Policy{Caps: sched.Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}},
		Learning: "beliefs.json could not be loaded: bad",
		Jobs: []core.JobView{
			{ID: 14, State: sched.Running, Description: "web-smoke", Started: now.Add(-time.Minute),
				Claim: sched.Claim{CPUs: 8, MemoryGB: 16, IO: true}, Learned: 5,
				Sources: sched.ClaimSources{CPUs: sched.SourceLearned, Memory: sched.SourceLearned, IO: sched.SourceLearned}},
			{ID: 17, State: sched.Queued, Description: "clippy", Submitted: now, Sizing: sched.SizingAsking, Waiting: "being sized (asking the agent)"},
		},
	}
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Now: now}))
	assert.Contains(t, out, "learning off: beliefs.json could not be loaded: bad")
	assert.Contains(t, out, "learned·5")
	assert.Contains(t, out, "sizing")

	q.Learning, q.Agent = "", core.AgentView{Why: "no Claude program is configured"}
	out = ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Now: now}))
	assert.Contains(t, out, "agent off: no Claude program is configured")
}

func TestRenderWork_ShowsETAsOverdueMarksAndForecasts(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	q := core.QueueView{
		Host:   sched.Host{Name: "fresco"},
		Policy: sched.Policy{Caps: sched.Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}},
		Jobs: []core.JobView{
			{ID: 14, State: sched.Running, Description: "slow", Started: now.Add(-9 * time.Minute), Claim: sched.Claim{CPUs: 4, MemoryGB: 2},
				Usual: 2 * time.Minute, OverdueAfter: 7 * time.Minute},
			{ID: 15, State: sched.Running, Description: "fine", Started: now.Add(-70 * time.Second), Claim: sched.Claim{CPUs: 4, MemoryGB: 2},
				Usual: 2 * time.Minute, OverdueAfter: 7 * time.Minute},
			{ID: 16, State: sched.Queued, Description: "next", Submitted: now, Claim: sched.Claim{CPUs: 4, MemoryGB: 6},
				StartsAt: now.Add(3 * time.Minute), Waiting: "needs 4 cores (2 free)"},
		},
	}
	out := ansi.Strip(RenderWork(160, 30, WorkData{Queue: q, Now: now}))
	assert.Contains(t, out, "⚠ 9m00s/~2m")
	assert.Contains(t, out, "1m10s/~2m")
	assert.NotContains(t, out, "⚠ 1m10s")
	assert.Contains(t, out, "starts ~3m · needs 4 cores (2 free)")
}

func TestRenderWork_ShowsWhatTheAgentIsDoing(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	q := core.QueueView{Host: sched.Host{Name: "fresco"}, Policy: sched.Policy{Caps: sched.Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}}}
	render := func(a core.AgentView) string {
		q.Agent = a
		return ansi.Strip(RenderWork(160, 20, WorkData{Queue: q, Now: now}))
	}
	busy := core.AgentView{On: true, Pending: 2, Flight: core.AgentFlight{Kind: "prior", Job: 17, Started: now.Add(-12 * time.Second)},
		Today: core.AgentDay{Calls: 5, Failed: 1, CostUSD: 0.0612}}
	assert.Contains(t, render(busy), "agent: sizing job 17 (12s) · 2 waiting · today 5 calls (1 failed), $0.06")
	noting := core.AgentView{On: true, Flight: core.AgentFlight{Kind: "reconcile", Job: 14, Started: now.Add(-4 * time.Second)}}
	assert.Contains(t, render(noting), "agent: noting job 14 (4s)")
	assert.Contains(t, render(core.AgentView{On: true, Today: core.AgentDay{Calls: 1, CostUSD: 0.01}}), "agent: idle · today 1 call, $0.01")
	assert.NotContains(t, render(core.AgentView{On: true}), "agent:", "nothing before its first call of the day")
}

func TestWorkOwner_NamesTheSubagent(t *testing.T) {
	assert.Equal(t, "no owner", workOwner(core.JobView{}))
	assert.Equal(t, "lto build · loom", workOwner(core.JobView{Owner: 3, OwnerTitle: "lto build", OwnerWorkspace: "loom"}))
	assert.Equal(t, "lto build › ab91bd4 · loom", workOwner(core.JobView{Owner: 3, OwnerTitle: "lto build", OwnerWorkspace: "loom", Agent: "ab91bd4b710780552"}))
	assert.Equal(t, "lto build › ab91bd4", workOwner(core.JobView{Owner: 3, OwnerTitle: "lto build", Agent: "ab91bd4b710780552"}))
}

func TestWorkClaim_ShowsSpareCores(t *testing.T) {
	j := core.JobView{Claim: sched.Claim{CPUs: 6, MemoryGB: 3}, Spare: sched.Claim{CPUs: 2, MemoryGB: 1}}
	assert.Equal(t, "4+2c/3G", workClaim(j))
	j.AboveCap = true
	assert.Equal(t, "4+2c/3G!", workClaim(j))
	assert.Equal(t, "agent", workClaim(core.JobView{Kind: sched.KindAgent}), "an agent job claims nothing")
}

func TestWorkRows_HeldJobsListAmongTheQueued(t *testing.T) {
	held := workQueued(32, "tests", "after job 20 (running)", time.Minute)
	held.State, held.After = sched.Held, 20
	var ids []sched.JobID
	for _, j := range WorkRows([]core.JobView{workRunning(20, "build", time.Minute), held, workQueued(30, "q30", "", time.Minute)}) {
		ids = append(ids, j.ID)
	}
	assert.Equal(t, []sched.JobID{20, 32, 30}, ids, "a held job sits among the queued, where the view's order puts it")
}

func TestRenderWork_AHeldJobSaysWhatItRunsAfter(t *testing.T) {
	held := workQueued(32, "run tests", "after job 14 (running)", time.Minute)
	held.State, held.After = sched.Held, 14
	q := workBusy()
	q.Jobs = append([]core.JobView{q.Jobs[0], q.Jobs[1], held}, q.Jobs[2:]...)

	out := ansi.Strip(RenderWork(148, 30, WorkData{Queue: q, Selected: 32, Now: workNow}))

	assert.Contains(t, lineWith(t, out, "run tests"), "after job 14 (running)")
	assert.Less(t, strings.Index(out, "QUEUED"), strings.Index(out, "run tests"), "listed under QUEUED")
	assert.Less(t, strings.Index(out, "run tests"), strings.Index(out, "RECENT"))
	assert.Contains(t, out, "(held: no output yet)")
}

// A hold below its claim shows in the running job's row and in the bars,
// which then draw what is held (past-peak holds spec §7).
func TestRenderWork_ShowsHoldsBelowTheirClaims(t *testing.T) {
	q := workBusy()
	q.Held = sched.Need{CPUs: 7, MemoryGB: 8, IOSlots: 1}
	for i := range q.Jobs {
		if q.Jobs[i].ID == 14 {
			q.Jobs[i].Holds = sched.Need{CPUs: 1, MemoryGB: 2}
		}
	}
	out := ansi.Strip(RenderWork(148, 30, WorkData{Queue: q, Now: workNow}))
	for _, want := range []string{
		"CPU ███████░░░░░░░ 7/14 held · 10 claimed · 11.2 in use",
		"MEM █████░░░░░░░░░ 8/22 GB held · 12 claimed · 9.8 GB in use",
		"peak 5.1G  holds 1c/2G",
	} {
		assert.Contains(t, out, want)
	}

	q.Held = q.Claimed
	out = ansi.Strip(RenderWork(148, 30, WorkData{Queue: q, Now: workNow}))
	assert.Contains(t, out, "10/14 claimed · 11.2 in use", "holds at their claims read as claims")
	assert.NotContains(t, out, "held ·")
}
