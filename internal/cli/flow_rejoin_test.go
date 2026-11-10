package cli

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restartable lets a test stop the daemon under the commands it serves and
// start it again, as a restart to deploy a build does: the model, and so
// its jobs, carry on, while stop drops every connection Join made and Join
// fails until start. The test's own client (testDaemon.client) stays up.
type restartable struct {
	d     *testDaemon
	mu    sync.Mutex
	conns []net.Conn
	down  bool
	joins chan struct{} // a value for each Join that connected
}

// restartableDaemon makes d restartable from here on, with rejoin's backoff
// cut to milliseconds.
func restartableDaemon(t *testing.T, d *testDaemon) *restartable {
	t.Helper()
	r := &restartable{d: d, joins: make(chan struct{}, 64)}
	old := Join
	Join = func() (*rpc.Client, error) {
		r.mu.Lock()
		if r.down {
			r.mu.Unlock()
			return nil, errors.New("dial: no loom daemon listens (the test stopped it)")
		}
		a, b := net.Pipe()
		r.conns = append(r.conns, a)
		r.mu.Unlock()
		d.srv.Serve(a)
		c, err := rpc.Dial(b)
		if err == nil {
			r.joins <- struct{}{}
		}
		return c, err
	}
	first, most := rejoinFirst, rejoinMax
	rejoinFirst, rejoinMax = 10*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { Join, rejoinFirst, rejoinMax = old, first, most })
	return r
}

func (r *restartable) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.down = true
	for _, c := range r.conns {
		c.Close()
	}
	r.conns = nil
}

func (r *restartable) start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.down = false
}

// stopOnJoin stops the daemon once a command has joined it, then runs
// then (nil: nothing); the returned channel closes when that is done.
func (r *restartable) stopOnJoin(then func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-r.joins
		r.stop()
		if then != nil {
			then()
		}
	}()
	return done
}

// setRunStatus rewrites run id's state with status, as an engine does.
func setRunStatus(t *testing.T, state, id, status string) {
	t.Helper()
	path := filepath.Join(state, "runs", id+".json")
	st, err := flowrun.Load(path)
	require.NoError(t, err)
	st.Status = status
	require.NoError(t, flowrun.Save(path, st))
}

func TestFlowRunWait_FollowsTheRunAcrossADaemonRestart(t *testing.T) {
	d, state, _ := runFixture(t, runStory("1", "intermediate-dev"))
	engineArgv = func(string) ([]string, error) { return []string{"sh", "-c", "echo up; sleep 30"}, nil }
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	r := restartableDaemon(t, d)
	restarted := r.stopOnJoin(func() {
		setRunStatus(t, state, "plan", flowrun.Done) // the run ends while the daemon is away
		r.start()
	})

	out, errOut, err := runRun(t, "", "wait", "plan")
	<-restarted

	require.NoError(t, err, out)
	assert.Contains(t, out, "ended plan: done\n")
	assert.Contains(t, errOut, "loom: lost the loom daemon (")
	assert.Contains(t, errOut, "); joining again in ")
}

func TestFlowRunWait_GivesUpWhenTheDaemonStaysAway(t *testing.T) {
	d, _, _ := runFixture(t, runStory("1", "intermediate-dev"))
	engineArgv = func(string) ([]string, error) { return []string{"sh", "-c", "echo up; sleep 30"}, nil }
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	r := restartableDaemon(t, d)
	defer func(f time.Duration) { rejoinFor = f }(rejoinFor)
	rejoinFor = 300 * time.Millisecond
	stopped := r.stopOnJoin(nil)

	_, _, err = runRun(t, "", "wait", "plan")
	<-stopped

	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "the run goes on, but the loom daemon has been out of reach for 300ms")
	assert.ErrorContains(t, err, "(loom flow run wait plan follows it again)")
}

func TestFlowGatesWait_FollowsTheRunAcrossADaemonRestart(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo up; sleep 1"}
	_, _, err := runFlow(t, "gates", "start", "--story", "abc")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	r := restartableDaemon(t, d)
	restarted := r.stopOnJoin(func() {
		d.waitFinished(t, 1) // the gate ends while the daemon is away
		r.start()
	})

	out, errOut, err := runFlow(t, "gates", "wait", "60")
	<-restarted

	require.NoError(t, err, out)
	assert.Contains(t, out, "1 PASS: echo up; sleep 1\n")
	assert.Contains(t, errOut, "loom: lost the loom daemon (")
}

func TestFlowGatesWait_StillAwayAtTheBoundIs75(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo up; sleep 30"}
	_, _, err := runFlow(t, "gates", "start", "--story", "abc")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	r := restartableDaemon(t, d)
	stopped := r.stopOnJoin(nil)

	out, _, err := runFlow(t, "gates", "wait", "1")
	<-stopped

	assert.Equal(t, exitStillRunning, flowExit(err), "call it again")
	assert.Contains(t, out, "story-abc: the run's jobs go on, but the loom daemon has been out of reach for ")
	assert.Contains(t, out, "(loom flow gates wait story-abc follows them again)\n")
}

func TestFlowWaitWait_FollowsTheJobAcrossADaemonRestart(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "build", "--", "sh", "-c", "echo up; sleep 1; exit 4")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	r := restartableDaemon(t, d)
	restarted := r.stopOnJoin(func() {
		d.waitFinished(t, 1)
		r.start()
	})

	out, errOut, err := runFlow(t, "wait", "wait", "build", "60")
	<-restarted

	assert.Equal(t, 4, flowExit(err), out)
	assert.Equal(t, "done build exit 4\n", out)
	assert.Contains(t, errOut, "loom: lost the loom daemon (")
}

func TestFlowWaitWait_StillAwayAtTheBoundIs75(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "build", "--", "sh", "-c", "echo up; sleep 30")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	r := restartableDaemon(t, d)
	stopped := r.stopOnJoin(nil)

	out, _, err := runFlow(t, "wait", "wait", "build", "1")
	<-stopped

	assert.Equal(t, exitStillRunning, flowExit(err))
	assert.Contains(t, out, "running build: job 1 goes on, but the loom daemon has been out of reach for ")
	assert.Contains(t, out, "(loom flow wait wait build follows it again)\n")
}

func TestFlowRunWait_TheLostLineNamesTheEnginesCause(t *testing.T) {
	runFixture(t, runStory("1", "intermediate-dev"))
	engineArgv = func(string) ([]string, error) {
		return []string{"sh", "-c", "echo starting; echo 'write runs/plan.json.tmp: no space left on device' >&2; echo; exit 1"}, nil
	}
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)

	out, _, err := runRun(t, "", "wait", "plan")

	assert.Equal(t, 1, exitCode(t, err), out)
	assert.Equal(t, "lost plan: the engine (job 1) ended failed (exit 1) without parking; its log ends: "+
		"write runs/plan.json.tmp: no space left on device; loom flow run resume plan carries on\n", out)
}

func TestFlowRunShow_ALostEngineIsLost(t *testing.T) {
	d, _, _ := runFixture(t, runStory("1", "intermediate-dev"))
	engineArgv = func(string) ([]string, error) { return []string{"sh", "-c", "echo 'disk full' >&2; exit 1"}, nil }
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	d.waitFinished(t, 1)

	out, _, err := runRun(t, "", "show", "plan")

	require.NoError(t, err)
	assert.Contains(t, out, "plan: lost, ")
	assert.Contains(t, out, "lost plan: the engine (job 1) ended failed (exit 1) without parking; its log ends: disk full; loom flow run resume plan carries on\n")
	assert.NotContains(t, out, "running")
}

func TestFlowRunShow_ARunningEngineIsRunning(t *testing.T) {
	d, _, _ := runFixture(t, runStory("1", "intermediate-dev"))
	engineArgv = func(string) ([]string, error) { return []string{"sh", "-c", "echo up; sleep 30"}, nil }
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")

	out, _, err := runRun(t, "", "show", "plan")

	require.NoError(t, err)
	assert.Contains(t, out, "plan: running, ")
	assert.Contains(t, out, "engine: job 1\n")
	assert.NotContains(t, out, "lost")
}

// show names the last agent run's job and report, so a manager doesn't
// search the queue for them (feedback thread 18).
func TestFlowRunShow_NamesTheLastAgentRunsJobAndReport(t *testing.T) {
	_, state, _ := runFixture(t, runStory("2", "intermediate-dev"))
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)
	out, _, err := runRun(t, "", "wait", "plan")
	require.NoError(t, err, out)
	require.Contains(t, out, "parked plan: needs-context (story 2)")
	rec, err := flowrun.LoadAgent(filepath.Join(state, "agents"), "plan-s2-dev1")
	require.NoError(t, err)

	out, _, err = runRun(t, "", "show", "plan")

	require.NoError(t, err)
	report := filepath.Join(state, "agents", "plan-s2-dev1", "report.md")
	require.FileExists(t, report)
	assert.Contains(t, out, fmt.Sprintf("last agent run: plan-s2-dev1 (job %d, report %s)\n", rec.Current().ID, report))
}

// agentRunLine names a report only once the run has ended with one, and
// the bare name when it can't read the record.
func TestAgentRunLine(t *testing.T) {
	dir := t.TempDir()
	save := func(name, status, report string) {
		require.NoError(t, flowrun.SaveAgent(dir, &flowrun.AgentRecord{Schema: flowrun.AgentSchema, ID: name, Role: "story-reviewer",
			Jobs: []flowrun.AgentRecordJob{{ID: 7}, {ID: 9, Answer: "go on"}}, Status: status, Report: report}))
	}
	save("ended", "DONE", "VERDICT: APPROVED")
	save("running", flowrun.AgentRunning, "")
	save("failed", flowrun.AgentFailed, "")
	require.NoError(t, flowrun.SaveAgent(dir, &flowrun.AgentRecord{Schema: flowrun.AgentSchema, ID: "jobless", Role: "story-reviewer", Status: "DONE", Report: "x"}))

	assert.Equal(t, "ended (job 9, report "+filepath.Join(dir, "ended", "report.md")+")", agentRunLine(dir, "ended"))
	assert.Equal(t, "running (job 9)", agentRunLine(dir, "running"))
	assert.Equal(t, "failed (job 9)", agentRunLine(dir, "failed"), "a run that left no report names its job alone")
	assert.Equal(t, "missing", agentRunLine(dir, "missing"))
	assert.Equal(t, "jobless", agentRunLine(dir, "jobless"), "a record with no job is one it can't read")
}

func TestFlowRunShow_WithoutADaemonPrintsTheState(t *testing.T) {
	d, _, _ := runFixture(t, runStory("1", "intermediate-dev"))
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	d.waitFinished(t, 1)
	old := Join
	Join = func() (*rpc.Client, error) { return nil, errors.New("no daemon for the test") }
	t.Cleanup(func() { Join = old })

	out, _, err := runRun(t, "", "show", "plan")

	require.NoError(t, err)
	assert.Contains(t, out, "plan: running, ")
	assert.Contains(t, out, "engine: job 1\n")
}

func TestFlowAgentWait_FollowsTheRunAcrossADaemonRestart(t *testing.T) {
	d, state, _ := runFixture(t)
	t.Setenv("FAKE_SLOW", "1") // the developer takes a few seconds
	_, _, err := runAgent(t, "Build Story 1.", "run", "intermediate-dev")
	require.NoError(t, err)
	r := restartableDaemon(t, d)
	restarted := r.stopOnJoin(func() {
		d.waitFinished(t, 1) // the agent ends while the daemon is away
		r.start()
	})

	out, errOut, err := runAgent(t, "", "wait", "60")
	<-restarted

	require.NoError(t, err, out)
	assert.Equal(t, "done intermediate-dev-1: DONE\nreport: "+filepath.Join(state, "agents", "intermediate-dev-1", "report.md")+"\n", out)
	assert.Contains(t, errOut, "loom: lost the loom daemon (")
}

// loseTheReplyOnce makes the first submission of a gate whose description
// starts with prefix reach the daemon, which queues it, and then lose the
// daemon before its reply arrives, as a restart mid-submit does; the
// daemon is back for the command's next join. It returns that job's ID.
func loseTheReplyOnce(t *testing.T, r *restartable, prefix string) *sched.JobID {
	t.Helper()
	var queued sched.JobID
	var once sync.Once
	old := submitFlowJob
	submitFlowJob = func(c *client, sub sched.Submit) (sched.JobID, error) {
		lose := false
		if strings.HasPrefix(sub.Description, prefix) {
			once.Do(func() { lose = true })
		}
		id, err := old(c, sub)
		if !lose || err != nil {
			return id, err
		}
		queued = id
		r.stop()
		require.Eventually(t, func() bool { return c.Err() != nil }, 5*time.Second, 5*time.Millisecond, "the command saw the daemon go")
		r.start()
		return 0, errors.New("the reply was lost")
	}
	t.Cleanup(func() { submitFlowJob = old })
	return &queued
}

// gatesNamed counts the daemon's jobs whose description starts with prefix.
func gatesNamed(d *testDaemon, prefix string) int {
	n := 0
	for _, j := range d.jobs() {
		if strings.HasPrefix(j.Description, prefix) {
			n++
		}
	}
	return n
}

// TestFlowGatesWait_ARestartMidRequeueQueuesNoGateTwice: a final run's wait
// queues the gates after a failure again; a restart that loses the reply to
// one of those submits must not leave that job behind while the wait,
// joined again, queues the gate a second time.
func TestFlowGatesWait_ARestartMidRequeueQueuesNoGateTwice(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo one; exit 3", "RUN echo two", "RUN echo three"}
	_, _, err := runFlow(t, "gates", "start", "--final")
	require.NoError(t, err)
	d.waitFinished(t, 1)
	r := restartableDaemon(t, d)
	queued := loseTheReplyOnce(t, r, "gate 2/3:")

	out, errOut, err := runFlow(t, "gates", "wait", "60")

	assert.Equal(t, 1, flowExit(err), out)
	assert.Contains(t, errOut, "loom: lost the loom daemon (")
	assert.Contains(t, out, "2 PASS: echo two\n")
	assert.Contains(t, out, "3 PASS: echo three\n")
	assert.Equal(t, 2, gatesNamed(d, "gate 2/3:"), "gate 2 ran in two generations, not three")
	assert.Equal(t, 2, gatesNamed(d, "gate 3/3:"))
	run := readTestRun(t, conf, "final")
	assert.Equal(t, *queued, run.Steps[1].Job(), "the job queued before the loss is the gate's")
	assert.Equal(t, *queued, d.job(run.Steps[2].Job()).After, "and the gate after it runs after it")
}

// TestFlowGatesStart_ARestartMidChainQueuesNoGateTwice: a start that loses
// the daemon partway through its chain joins again and queues the rest, the
// gates queued before the loss included once.
func TestFlowGatesStart_ARestartMidChainQueuesNoGateTwice(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo one", "RUN echo two"}
	r := restartableDaemon(t, d)
	queued := loseTheReplyOnce(t, r, "gate 1/2:")

	out, errOut, err := runFlow(t, "gates", "start", "--final")

	require.NoError(t, err, errOut)
	assert.Contains(t, errOut, "loom: lost the loom daemon (")
	assert.Equal(t, fmt.Sprintf("started final: 2 steps, jobs %d, %d\n", *queued, *queued+1), out)
	assert.Len(t, d.jobs(), 2, "no gate was queued twice")
	run := readTestRun(t, conf, "final")
	assert.Equal(t, *queued, d.job(run.Steps[1].Job()).After)
}
