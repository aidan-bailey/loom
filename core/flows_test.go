package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/internal/flowview"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flowFixture is a model with a queue and one started session, its flow
// reader swapped to count its looks and answer sums.
type flowFixture struct {
	m     *Model
	inst  *session.Instance
	id    InstanceID
	looks int
	sums  []flowview.Summary
}

func newFlowFixture(t *testing.T) *flowFixture {
	t.Helper()
	swap(t, &flowLooksOn, true)
	f := &flowFixture{m: wqModel(t)}
	f.inst = activeInst(t, f.m, "owner")
	f.id = f.m.idOf(f.inst)
	f.sums = []flowview.Summary{{Plan: "docs/p.md", Stage: flowview.StageRun, Status: flowrun.Running, Run: "r", Engine: 7, Total: 2}}
	swap(t, &findFlows, func(worktree, base, records string) ([]flowview.Summary, error) {
		f.looks++
		assert.Equal(t, f.inst.GetWorktreePath(), worktree)
		assert.NotEmpty(t, base)
		return f.sums, nil
	})
	f.m.Drain()
	return f
}

// swap sets *v to with for the test.
func swap[T any](t *testing.T, v *T, with T) {
	t.Helper()
	old := *v
	*v = with
	t.Cleanup(func() { *v = old })
}

// pump runs the model's jobs and delivers their results until none is
// left, and returns the events.
func (f *flowFixture) pump(t *testing.T) []Event {
	t.Helper()
	var events []Event
	for range 20 {
		out := f.m.Drain()
		events = append(events, out.Events...)
		if len(out.Jobs) == 0 {
			return events
		}
		for _, j := range out.Jobs {
			f.m.Deliver(j())
		}
	}
	t.Fatal("the model kept queuing jobs")
	return nil
}

// job puts a record in the queue's store, as an admission would.
func (f *flowFixture) job(id sched.JobID, kind string, run sched.JobID, state sched.State) {
	f.m.queue.store.Jobs = append(f.m.queue.store.Jobs, sched.Record{ID: id, Order: int64(id), Owner: uint64(f.id), Kind: kind, Run: run, State: state})
}

func (f *flowFixture) setState(id sched.JobID, state sched.State) {
	f.m.queue.store.Jobs[f.m.queue.index(id)].State = state
}

func TestFlows_ARunJobsStateChangeTriggersALook(t *testing.T) {
	f := newFlowFixture(t)
	f.m.maybeFlowLooks()
	f.pump(t)
	assert.Equal(t, 1, f.looks, "first sight")
	require.Len(t, f.m.viewOf(f.inst).Flows, 1)
	assert.Equal(t, "docs/p.md", f.m.viewOf(f.inst).Flows[0].Plan)

	f.m.maybeFlowLooks()
	f.pump(t)
	assert.Equal(t, 1, f.looks, "nothing moved")

	f.job(7, sched.KindRun, 0, sched.Running)
	f.m.maybeFlowLooks()
	f.pump(t)
	assert.Equal(t, 2, f.looks, "the run's engine started")

	f.job(8, sched.KindAgent, 7, sched.Running)
	f.m.maybeFlowLooks()
	f.pump(t)
	assert.Equal(t, 3, f.looks, "a step started")

	f.setState(8, sched.Done)
	f.m.maybeFlowLooks()
	f.pump(t)
	assert.Equal(t, 4, f.looks, "a step ended")
}

func TestFlows_AJobWithoutRunOrRunKindTriggersNothing(t *testing.T) {
	f := newFlowFixture(t)
	f.m.maybeFlowLooks()
	f.pump(t)
	f.job(9, "", 0, sched.Running)
	f.m.maybeFlowLooks()
	f.pump(t)
	f.setState(9, sched.Done)
	f.m.maybeFlowLooks()
	f.pump(t)
	assert.Equal(t, 1, f.looks, "a build job of the session is no step of a run")
}

func TestFlows_ATriggerDuringALookRunsOneMore(t *testing.T) {
	f := newFlowFixture(t)
	f.m.wantFlowLook(f.inst)
	f.m.wantFlowLook(f.inst)
	f.m.wantFlowLook(f.inst)
	out := f.m.Drain()
	require.Len(t, out.Jobs, 1, "one look in flight")
	f.m.Deliver(out.Jobs[0]())
	f.pump(t)
	assert.Equal(t, 2, f.looks, "the triggers during it ran exactly one more")
}

func TestFlows_AStopTriggersALook(t *testing.T) {
	f := newFlowFixture(t)
	adoptLaunch(t, f.m, f.inst, "L1", time.Now().Add(-time.Minute))
	f.pump(t)
	before := f.looks
	f.m.Deliver(hookScanResults{results: []hookScanResult{{instance: f.inst, result: session.HookScanResult{
		LaunchID: "L1", Events: []hooks.Event{{Name: hooks.EventStop, At: time.Now()}},
	}}}})
	f.pump(t)
	assert.Equal(t, before+1, f.looks)
}

func TestFlows_AFailedLookKeepsTheLastSummaries(t *testing.T) {
	f := newFlowFixture(t)
	f.m.wantFlowLook(f.inst)
	f.pump(t)
	swap(t, &findFlows, func(string, string, string) ([]flowview.Summary, error) { return nil, errors.New("git broke") })
	f.m.wantFlowLook(f.inst)
	f.pump(t)
	assert.Len(t, f.m.viewOf(f.inst).Flows, 1, "a doubt changes nothing")
}

func TestFlows_ARunningRunWhoseEngineEndedIsLost(t *testing.T) {
	f := newFlowFixture(t)
	f.job(7, sched.KindRun, 0, sched.Running)
	f.m.wantFlowLook(f.inst)
	f.pump(t)
	assert.False(t, f.m.viewOf(f.inst).Flows[0].Lost, "its engine runs")
	f.setState(7, sched.Done)
	assert.True(t, f.m.viewOf(f.inst).Flows[0].Lost, "its engine ended")
	f.m.queue.store.Jobs = nil
	assert.True(t, f.m.viewOf(f.inst).Flows[0].Lost, "its engine left the queue")
	f.sums[0].Status = flowrun.Parked
	f.m.wantFlowLook(f.inst)
	f.pump(t)
	assert.False(t, f.m.viewOf(f.inst).Flows[0].Lost, "a parked run has no engine running")
}

// replyFor is the Reply naming req among events.
func replyFor(t *testing.T, events []Event, req ReqID) Reply {
	t.Helper()
	for _, e := range events {
		if r, ok := e.(Reply); ok && r.Req == req {
			return r
		}
	}
	t.Fatalf("no Reply for request %d in %v", req, events)
	return Reply{}
}

// boardFixture swaps the reader to a board whose story 1 has a finished
// developer (job 3) and a running reviewer (job 8), and the pricer to one
// that records its arguments.
func boardFixture(t *testing.T, f *flowFixture) *[][]string {
	t.Helper()
	board := flowview.FlowDetail{
		Summary: flowview.Summary{Plan: "docs/p.md", Spec: "docs/s-design.md", Run: "r", Status: flowrun.Running},
		Stories: []flowview.StoryView{{ID: "1", Steps: []flowview.StepView{
			{Name: "r-s1-dev1", Kind: "dev", K: 1, Role: "intermediate-dev", Status: "DONE", Session: "dsess", Jobs: []sched.JobID{3}},
			{Name: "r-s1-review1", Kind: "review", K: 1, Role: "code-quality-reviewer", Status: flowrun.AgentRunning, Jobs: []sched.JobID{8}},
		}}},
	}
	swap(t, &detailFlow, func(worktree, base, records, plan string) (flowview.FlowDetail, error) {
		assert.Equal(t, "docs/p.md", plan)
		return board, nil
	})
	swap(t, &flowHistory, func(string) ([]flowview.PastRun, error) { return nil, nil })
	swap(t, &flowRepoKey, func(string) (string, error) { return "-k", nil })
	var calls [][]string
	swap(t, &runFlowCosts, func(dir string, env []string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if args[0] == "design" {
			return []byte(`{"sessions": [{"session": "x"}], "total": {"usd": 3.14}}`), nil
		}
		return []byte(`{"run": "r", "agents": [{"name": "r-s1-dev1", "role": "intermediate-dev", "usd": 0.5}, {"name": "r-s1-review1", "role": "code-quality-reviewer", "usd": 0.25, "running": true}], "managers": [{"name": "manager m", "role": "manager", "usd": 1}], "total": {"usd": 1.75}}`), nil
	})
	f.job(3, sched.KindAgent, 0, sched.Done)
	f.job(8, sched.KindAgent, 0, sched.Running)
	f.m.queue.store.Jobs[f.m.queue.index(3)].Started = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	log := filepath.Join(f.m.queue.jobDir(8), sched.LogFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(log), 0o700))
	require.NoError(t, os.WriteFile(log, []byte(`{"type":"system","subtype":"init","session_id":"rsess"}`+"\n"+`{"type":"assistant"}`+"\n"), 0o600))
	return &calls
}

func TestReadFlow_AnswersTheBoardWithItsCosts(t *testing.T) {
	f := newFlowFixture(t)
	calls := boardFixture(t, f)
	f.m.ReadFlow(f.id, "docs/p.md", 1)
	r := replyFor(t, f.pump(t), 1)
	require.NoError(t, r.Err)
	assert.Equal(t, "r", r.Flow.Detail.Run)
	assert.InDelta(t, 1.75, r.Flow.Costs.Spent, 1e-9)
	assert.InDelta(t, 0.75, r.Flow.Costs.ByStory["1"], 1e-9)
	require.NotNil(t, r.Flow.Costs.Design)
	assert.InDelta(t, 3.14, *r.Flow.Costs.Design, 1e-9)
	assert.Equal(t, time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), r.Flow.Times[3].Started)
	require.NotEmpty(t, *calls)
	assert.Equal(t, []string{"run", "r", "--json", "--session", "r-s1-review1=rsess"}, (*calls)[0],
		"the running reviewer's session comes from its job's log; the developer's record has its own")

	f.m.ReadFlow(f.id, "docs/p.md", 2)
	require.InDelta(t, 1.75, replyFor(t, f.pump(t), 2).Flow.Costs.Spent, 1e-9)
	assert.Len(t, *calls, 3, "the second read prices nothing: the run's agents haven't moved (design is priced again while its plan isn't committed)")
}

func TestReadFlow_PricesAgainOnlyWhenTheAgentsMovedOrTimePassed(t *testing.T) {
	f := newFlowFixture(t)
	calls := boardFixture(t, f)
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	swap(t, &flowNow, func() time.Time { return now })
	runs := func() int {
		n := 0
		for _, c := range *calls {
			if c[0] == "run" {
				n++
			}
		}
		return n
	}
	f.m.ReadFlow(f.id, "docs/p.md", 1)
	f.pump(t)
	f.m.ReadFlow(f.id, "docs/p.md", 2)
	f.pump(t)
	assert.Equal(t, 1, runs())
	now = now.Add(flowPriceEvery)
	f.m.ReadFlow(f.id, "docs/p.md", 3)
	f.pump(t)
	assert.Equal(t, 2, runs(), "an agent runs, and flowPriceEvery has passed")
}

func TestReadFlow_APricerFailureIsTheCostsError(t *testing.T) {
	f := newFlowFixture(t)
	boardFixture(t, f)
	swap(t, &runFlowCosts, func(string, []string, ...string) ([]byte, error) { return nil, errors.New("agent_costs: boom") })
	f.m.ReadFlow(f.id, "docs/p.md", 1)
	r := replyFor(t, f.pump(t), 1)
	require.NoError(t, r.Err, "the board still answers")
	assert.Equal(t, "cost unavailable: agent_costs: boom", r.Flow.Costs.Error)
	assert.Zero(t, r.Flow.Costs.Spent)
	assert.Nil(t, r.Flow.Costs.Design)
}

func TestReadFlow_ASummarysSpentIsTheLastPricing(t *testing.T) {
	f := newFlowFixture(t)
	boardFixture(t, f)
	f.m.wantFlowLook(f.inst)
	f.pump(t)
	assert.False(t, f.m.viewOf(f.inst).Flows[0].Priced)
	f.m.ReadFlow(f.id, "docs/p.md", 1)
	f.pump(t)
	s := f.m.viewOf(f.inst).Flows[0]
	assert.True(t, s.Priced)
	assert.InDelta(t, 1.75, s.Spent, 1e-9)
}

func TestReadFlows_EverySessionsFlowsAndTheHistory(t *testing.T) {
	f := newFlowFixture(t)
	swap(t, &flowHistory, func(string) ([]flowview.PastRun, error) {
		return []flowview.PastRun{{Key: "-k", File: "2026-11-06-p.md", Stories: 4, USD: 10.08, HasUSD: true}}, nil
	})
	f.m.wantFlowLook(f.inst)
	f.pump(t)
	f.m.ReadFlows(1)
	r := replyFor(t, f.pump(t), 1)
	require.NoError(t, r.Err)
	require.Len(t, r.Flows.Live, 1)
	assert.Equal(t, f.id, r.Flows.Live[0].Instance)
	assert.Equal(t, "owner", r.Flows.Live[0].Title)
	assert.Equal(t, "docs/p.md", r.Flows.Live[0].Flow.Plan)
	require.Len(t, r.Flows.History, 1)
}

func TestReadFlowDoc_AnswersTheDocumentOrTheRefusal(t *testing.T) {
	f := newFlowFixture(t)
	swap(t, &readFlowDoc, func(worktree, base, records, plan, doc string) (flowview.Doc, error) {
		if doc != flowview.DocSpec {
			return flowview.Doc{}, errors.New("\"" + doc + "\" is no document of a flow")
		}
		return flowview.Doc{Path: "docs/s.md", Text: "# S\n"}, nil
	})
	f.m.ReadFlowDoc(f.id, "docs/p.md", "spec", 1)
	r := replyFor(t, f.pump(t), 1)
	require.NoError(t, r.Err)
	assert.Equal(t, FlowDocView{Path: "docs/s.md", Text: "# S\n"}, r.FlowDoc)
	f.m.ReadFlowDoc(f.id, "docs/p.md", "../etc/passwd", 2)
	r = replyFor(t, f.pump(t), 2)
	require.Error(t, r.Err)
	assert.True(t, strings.Contains(r.Err.Error(), "read a flow document of"), r.Err.Error())
}

func TestInitSession(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "output.log")
	require.NoError(t, os.WriteFile(p, []byte("not json\n"+`{"type":"system","subtype":"init","session_id":"S"}`+"\n"), 0o600))
	assert.Equal(t, "S", initSession(p))
	require.NoError(t, os.WriteFile(p, []byte(`{"type":"assistant","session_id":"X"}`+"\n"), 0o600))
	assert.Empty(t, initSession(p), "only the init line names it")
	assert.Empty(t, initSession(filepath.Join(dir, "missing")))
}
