package web

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var jobT0 = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

func runningJob(id sched.JobID, owner core.InstanceID) core.JobView {
	return core.JobView{
		ID: id, State: sched.Running, Owner: owner, Description: "core tests",
		Argv: []string{"go", "test", "./core"}, Agent: "a1f0",
		Submitted: jobT0, Started: jobT0.Add(time.Minute),
		CPU: 3.5, RSSKB: 400_000, PeakRSSKB: 500_000,
	}
}

func doneJob(id sched.JobID, owner core.InstanceID, code int) core.JobView {
	return core.JobView{
		ID: id, State: sched.Done, Owner: owner, Argv: []string{"go", "vet"},
		Submitted: jobT0, Started: jobT0, Ended: jobT0.Add(time.Minute),
		Exit: &sched.Exit{Code: code, Ended: jobT0.Add(time.Minute)},
	}
}

func TestJobOf_MapsTheFieldsTheAppReads(t *testing.T) {
	assert.Equal(t, jobJSON{
		ID: 14, State: "running", Description: "core tests", Command: "go test ./core", Agent: "a1f0",
		Submitted: "2026-11-07T10:00:00Z", Started: "2026-11-07T10:01:00Z",
	}, jobOf(runningJob(14, 1)))

	failed := core.JobView{ID: 15, State: sched.Failed, Owner: 1, Failure: "start: no such file",
		Exit: &sched.Exit{Code: -1, Signal: "SIGKILL"}, CancelReason: "a person", Waiting: ""}
	assert.Equal(t, jobJSON{ID: 15, State: "failed", Failure: "start: no such file", CancelReason: "a person",
		Exit: &exitJSON{Code: -1, Signal: "SIGKILL"}}, jobOf(failed))

	queued := core.JobView{ID: 16, State: sched.Queued, Owner: 1, Waiting: "after 14"}
	assert.Equal(t, "after 14", jobOf(queued).Waiting)
	assert.Nil(t, jobOf(queued).Exit, "a job that has not ended has no exit")
}

func TestJobsOf_GroupsByOwnerInTheViewsOrder(t *testing.T) {
	q := core.QueueView{Jobs: []core.JobView{
		runningJob(14, 1),
		runningJob(12, 1),
		{ID: 16, State: sched.Queued, Owner: 1},
		{ID: 17, State: sched.Held, Owner: 2},
		runningJob(18, 0),
		doneJob(11, 1, 0),
	}}
	got := jobsOf(q)

	require.Len(t, got, 2, "a job with no owner is left out")
	var ids []sched.JobID
	for _, j := range got[1].jobs {
		ids = append(ids, j.ID)
	}
	assert.Equal(t, []sched.JobID{14, 12, 16, 11}, ids, "the view's order is kept")
	assert.Equal(t, "⚙ job 12 +1", got[1].label)
	assert.Equal(t, sched.JobID(12), got[1].focus)
	assert.Equal(t, "⚙ queued", got[2].label)
	assert.Zero(t, got[2].focus)
}

func TestJobsOf_KeepsAtMostTenFinishedJobsASession(t *testing.T) {
	q := core.QueueView{Jobs: []core.JobView{runningJob(100, 1)}}
	for id := sched.JobID(50); id > 38; id-- { // 12 finished, newest first
		q.Jobs = append(q.Jobs, doneJob(id, 1, 0))
	}
	got := jobsOf(q)[1].jobs
	require.Len(t, got, 1+maxFinishedJobs)
	assert.Equal(t, sched.JobID(100), got[0].ID, "a running job is never cut")
	assert.Equal(t, sched.JobID(41), got[len(got)-1].ID, "the oldest finished ones go")
}

func TestSetQueue_OnlyWhatTheAppReadsWakesTheFlusher(t *testing.T) {
	p := newProjection()
	p.setWorkspaces([]core.WorkspaceView{{ID: 1, Name: "toy"}})
	require.True(t, p.setViews(1, []core.InstanceView{fleetView(1, "mine", session.Running), fleetView(2, "other", session.Running)}))
	assert.Equal(t, []jobJSON{}, p.sessions[1][0].Jobs, "no queue yet: an empty list")

	q := core.QueueView{Jobs: []core.JobView{runningJob(14, 1)}, UsedCPU: 1}
	require.True(t, p.setQueue(q), "the first queue view")
	mine := p.sessions[1][0]
	require.Len(t, mine.Jobs, 1)
	assert.Equal(t, "⚙ job 14", mine.JobsLabel)
	assert.Equal(t, sched.JobID(14), mine.JobsFocus)
	assert.Empty(t, p.sessions[1][1].Jobs, "another session's list stays empty")

	sampled := core.QueueView{Jobs: []core.JobView{runningJob(14, 1)}, UsedCPU: 7, UsedRSSKB: 9}
	sampled.Jobs[0].CPU, sampled.Jobs[0].RSSKB, sampled.Jobs[0].PeakRSSKB = 8, 900_000, 950_000
	assert.False(t, p.setQueue(sampled), "a sample tick moves nothing the app reads")

	ended := core.QueueView{Jobs: []core.JobView{doneJob(14, 1, 0)}}
	assert.True(t, p.setQueue(ended), "a job that ended does")
	assert.Equal(t, jobState("done"), p.sessions[1][0].Jobs[0].State)
	assert.Empty(t, p.sessions[1][0].JobsLabel, "a finished job takes the badge away")

	moved := core.QueueView{Jobs: []core.JobView{doneJob(14, 2, 0)}}
	assert.True(t, p.setQueue(moved), "an owner change does")
	assert.Empty(t, p.sessions[1][0].Jobs)
	assert.Len(t, p.sessions[1][1].Jobs, 1)

	require.True(t, p.setViews(1, []core.InstanceView{fleetView(1, "mine", session.Ready), fleetView(2, "other", session.Ready)}))
	assert.Len(t, p.sessions[1][1].Jobs, 1, "new views keep the jobs")
}

func TestCleanLog(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"plain text", "ok\n", "ok\n"},
		{"colours", "\x1b[31mred\x1b[0m and \x1b[1;32mgreen\x1b[m\n", "red and green\n"},
		{"an OSC title ended by BEL", "\x1b]0;title\x07after", "after"},
		{"an OSC ended by ST", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"a lone escape pair", "a\x1b=b\x1b(Bc", "abc"},
		{"an escape cut at the end", "abc\x1b", "abc"},
		{"a progress bar keeps its last state", "10%\r50%\r100%\ndone\n", "100%\ndone\n"},
		{"CRLF is a line end", "a\r\nb\r\n", "a\nb\n"},
		{"invalid UTF-8", "ok \xff\n", "ok �\n"},
	} {
		assert.Equal(t, c.want, cleanLog([]byte(c.in)), c.name)
	}
}

func TestJobLogOf_MapsEveryField(t *testing.T) {
	assert.Equal(t,
		jobLogJSON{Text: "red\n", Start: 3, End: 15, Cut: true, Reset: true},
		jobLogOf(sched.LogRead{Data: []byte("\x1b[31mred\x1b[0m\n"), Start: 3, End: 15, Size: 40, Cut: true, Reset: true}))
}

func TestAPI_JobLogRefusesBadPathsAndTheModelsRefusals(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "fresh", session.Ready)}})
	base := fmt.Sprintf("/api/v1/sessions/%d/jobs", env.ids["fresh"])

	for _, c := range []struct{ path, want string }{
		{base + "/0/log", `job "0": want a job id`},
		{base + "/x/log", `job "x": want a job id`},
		{base + "/7/log?after=-1", `after "-1": want a byte offset`},
		{base + "/7/log?after=a", `after "a": want a byte offset`},
	} {
		status, out := env.do(http.MethodGet, c.path, nil)
		assert.Equal(t, http.StatusBadRequest, status, c.path)
		assert.Equal(t, c.want, apiErrorOf(t, out), c.path)
	}

	status, out := env.do(http.MethodGet, "/api/v1/sessions/999999/jobs/7/log", nil)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "no such session", apiErrorOf(t, out))

	status, out = env.do(http.MethodGet, base+"/7/log", nil)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "read a job log of fresh: job 7 is no longer in the queue", apiErrorOf(t, out))
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "job_log", session: env.ids["fresh"], result: "409 read a job log of fresh: job 7 is no longer in the queue"})
}

func TestJobOf_MapsTheControls(t *testing.T) {
	j := runningJob(14, 1)
	j.PausedAt, j.PauseReclaimed, j.Forced = jobT0, true, true
	j.Control, j.ControlErr = sched.ControlResume, "pause failed: no bus"
	out := jobOf(j)
	assert.True(t, out.Paused)
	assert.True(t, out.PauseReclaimed)
	assert.True(t, out.Forced)
	assert.Equal(t, jobControlKind("resume"), out.Control)
	assert.Equal(t, "pause failed: no bus", out.ControlError)
	assert.False(t, out.Parked)
	assert.True(t, jobOf(core.JobView{ID: 15, State: sched.Queued, Parked: true}).Parked)
	assert.False(t, jobOf(runningJob(16, 1)).Paused)
}

func TestAPI_JobControlAsksTheModelForTheSessionsJob(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "fresh", session.Ready)}})
	base := fmt.Sprintf("/api/v1/sessions/%d/jobs", env.ids["fresh"])

	status, out := env.do(http.MethodPost, base+"/7/nap", nil)
	assert.Equal(t, http.StatusNotFound, status, "an action the app may not ask for")
	assert.Equal(t, "no such action", apiErrorOf(t, out))
	status, out = env.do(http.MethodPost, base+"/x/park", nil)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, `job "x": want a job id`, apiErrorOf(t, out))
	status, _ = env.do(http.MethodPost, "/api/v1/sessions/999999/jobs/7/park", nil)
	assert.Equal(t, http.StatusNotFound, status)

	for action, want := range map[string]core.JobAction{"start": core.JobStart, "park": core.JobPark, "unpark": core.JobUnpark, "pause": core.JobPause, "resume": core.JobResume} {
		status, out = env.do(http.MethodPost, base+"/7/"+action, nil)
		assert.Equal(t, http.StatusConflict, status, action)
		assert.Contains(t, apiErrorOf(t, out), "no job 7", action)
		env.rb.mu.Lock()
		last := env.rb.controls[len(env.rb.controls)-1]
		env.rb.mu.Unlock()
		assert.Equal(t, core.JobControl{Job: 7, Owner: env.ids["fresh"], Action: want}, last, "scoped to the session in the path")
	}
}

func TestJobOf_EveryStateAndControlIsInItsUnion(t *testing.T) {
	u := unions()
	for _, st := range []sched.State{sched.Held, sched.Queued, sched.Running, sched.Done, sched.Failed, sched.Cancelled, sched.Lost} {
		out := jobOf(core.JobView{ID: 1, State: st})
		assert.True(t, slices.Contains(u[reflect.TypeFor[jobState]()].values, string(out.State)), "state %q", out.State)
	}
	for _, c := range []sched.ControlKind{sched.ControlPause, sched.ControlResume, ""} {
		out := jobOf(core.JobView{ID: 1, State: sched.Running, Control: c})
		assert.True(t, slices.Contains(u[reflect.TypeFor[jobControlKind]()].values, string(out.Control)), "control %q", out.Control)
		assert.Equal(t, jobControlKind(c), out.Control)
	}
	assert.Equal(t, jobControlKind(""), jobOf(core.JobView{ID: 1}).Control, "an unset control is empty")
}
