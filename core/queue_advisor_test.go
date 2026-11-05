package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/sched/advisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wqPriorAnswer = `{"like":"","cpus":2,"memory_gb":3,"io":false,"duration_seconds":30,"note":"a tiny job"}`

// wqFakeClaude writes a claude that appends each call's stdin to calls,
// sleeps delay, and answers with the CLI's JSON result: success with
// answer as the structured output, or an error result when answer is "".
func wqFakeClaude(t *testing.T, answer string, delay time.Duration) (program, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	result := `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}`
	if answer != "" {
		result = `{"type":"result","subtype":"success","is_error":false,"result":"","num_turns":2,"total_cost_usd":0.0125,` +
			`"usage":{"input_tokens":5,"output_tokens":40},"modelUsage":{"claude-opus-5-5":{}},"structured_output":` + answer + `}`
	}
	script := "#!/bin/sh\n" +
		"cat >> '" + calls + "'\necho >> '" + calls + "'\n"
	if delay > 0 {
		script += "sleep " + strings.TrimSuffix(delay.String(), "s") + "\n"
	}
	script += "printf '%s\\n' '" + result + "'\n"
	program = filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	return program, calls
}

// wqAdvisedModel is a live model whose advisor runs a fake claude.
func wqAdvisedModel(t *testing.T, answer string, delay time.Duration) (*Model, string) {
	t.Helper()
	m := wqLiveModel(t)
	program, calls := wqFakeClaude(t, answer, delay)
	m.SetAdvisorForTest(program, 10*time.Second)
	return m, calls
}

// wqCalls is each call the fake claude got, one request per line.
func wqCalls(t *testing.T, calls string) []string {
	t.Helper()
	data, err := os.ReadFile(calls)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestAdvisor_AnUnseenCommandIsSizedByItsPrior(t *testing.T) {
	m, calls := wqAdvisedModel(t, wqPriorAnswer, 0)
	id := wqSubmit(t, m, "true")
	rec := wqRecord(t, m, id)
	assert.Equal(t, sched.SizingAsking, rec.Sizing)
	assert.Equal(t, sched.Queued, rec.State, "it waits for the prior")
	assert.Equal(t, "being sized (asking the agent)", m.Queue().Jobs[0].Waiting)
	flight := m.Queue().Agent.Flight
	assert.Equal(t, "prior", flight.Kind, "the call is in flight until the loop runs its job")
	assert.Equal(t, id, flight.Job)
	assert.Equal(t, "true", flight.Command)
	assert.False(t, flight.Started.IsZero())

	wqWait(t, "the prior", func() bool { wqPump(t, m); return wqRecord(t, m, id).Sizing == "" })
	agent := m.Queue().Agent
	assert.Zero(t, agent.Flight, "no call in flight")
	assert.Equal(t, AgentDay{Calls: 1, CostUSD: 0.0125}, agent.Today)
	logged := m.queue.advisor.calls
	require.Len(t, logged, 1, "the call is logged")
	c := logged[0]
	assert.Equal(t, advisor.OutcomeAnswered, c.Outcome)
	assert.Equal(t, advisor.KindPrior, c.Kind)
	assert.Equal(t, id, c.Job)
	assert.Equal(t, "true", c.Command)
	assert.Equal(t, advisor.DefaultModel, c.Model)
	assert.Equal(t, "claude-opus-5-5", c.Usage.Model)
	assert.Equal(t, 0.0125, c.Usage.CostUSD)
	assert.Equal(t, int64(40), c.Usage.OutputTokens)
	assert.Positive(t, c.Usage.DurationMS)
	assert.Contains(t, c.Answer, "a tiny job")
	saved, err := advisor.LoadCalls(m.queue.dir)
	require.NoError(t, err)
	assert.Len(t, saved, 1, "advisor.jsonl holds it")
	rec = wqRecord(t, m, id)
	assert.Equal(t, sched.Claim{CPUs: 2, MemoryGB: 3}, rec.Claim)
	assert.Equal(t, sched.SourceAgent, rec.Sources.Memory)
	assert.Empty(t, rec.AgentWhy)
	assert.NotEqual(t, sched.Queued, rec.State, "admitted once sized")
	p := m.queue.beliefOf(rec.Signature).Prior
	require.NotNil(t, p)
	assert.Equal(t, advisor.DefaultModel, p.Model)
	assert.Equal(t, "low", p.Effort)
	assert.Equal(t, "a tiny job", p.Note)
	got := wqCalls(t, calls)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], `"task":"prior"`)
	assert.Contains(t, got[0], `"argv":["true"]`)
	assert.NotContains(t, got[0], "PATH=", "the environment is filtered")
}

func TestAdvisor_TwoJobsOfOneCommandShareOneCall(t *testing.T) {
	m, calls := wqAdvisedModel(t, wqPriorAnswer, 0)
	sub := wqJob(t, "true")
	id1, _, err := m.submitJob(sub)
	require.NoError(t, err)
	id2, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wqWait(t, "both sized", func() bool {
		wqPump(t, m)
		return wqRecord(t, m, id1).Sizing == "" && wqRecord(t, m, id2).Sizing == ""
	})
	assert.Len(t, wqCalls(t, calls), 1)
}

func TestAdvisor_AJobWaitsNoLongerThanWaitSeconds(t *testing.T) {
	m, _ := wqAdvisedModel(t, wqPriorAnswer, 3*time.Second)
	wqConfig(t, m, `{"agent": {"wait_seconds": 1}}`)
	id := wqSubmit(t, m, "true")
	require.Equal(t, sched.SizingAsking, wqRecord(t, m, id).Sizing)
	time.Sleep(1100 * time.Millisecond)
	m.admitJobs()
	rec := wqRecord(t, m, id)
	assert.Empty(t, rec.Sizing)
	assert.Equal(t, "the agent didn't answer in 1s", rec.AgentWhy)
	assert.Equal(t, sched.SourceDefault, rec.Sources.Memory)
	wqWait(t, "the late prior", func() bool { wqPump(t, m); return m.queue.beliefOf(rec.Signature).Prior != nil })
}

func TestAdvisor_AFailureBacksOffAndThreePauseIt(t *testing.T) {
	m, calls := wqAdvisedModel(t, "", 0)
	sub := wqJob(t, "true")
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wqWait(t, "the failure", func() bool { wqPump(t, m); return wqRecord(t, m, id).Sizing == "" })
	rec := wqRecord(t, m, id)
	assert.Contains(t, rec.AgentWhy, "the agent failed")
	assert.Contains(t, m.queue.beliefOf(rec.Signature).Advisor.FailedWhy, "error_during_execution")

	again, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Empty(t, wqRecord(t, m, again).Sizing, "the same command isn't asked again within 10 minutes")

	for _, argv := range [][]string{{"true", "1"}, {"true", "2"}} {
		id, _, err := m.submitJob(wqJob(t, argv...))
		require.NoError(t, err)
		wqWait(t, "a failure", func() bool { wqPump(t, m); return wqRecord(t, m, id).Sizing == "" })
	}
	assert.Len(t, wqCalls(t, calls), 3)
	assert.Contains(t, m.Queue().Agent.Why, "paused until")
	assert.False(t, m.Queue().Agent.PausedUntil.IsZero())
	assert.Equal(t, AgentDay{Calls: 3, Failed: 3}, m.Queue().Agent.Today)
	for _, c := range m.queue.advisor.calls {
		assert.Equal(t, advisor.OutcomeFailed, c.Outcome)
		assert.Contains(t, c.Error, "error_during_execution")
	}
	id, _, err = m.submitJob(wqJob(t, "true", "3"))
	require.NoError(t, err)
	assert.Empty(t, wqRecord(t, m, id).Sizing, "a paused advisor is not asked")
}

func TestAdvisor_TheUsageGuardSkipsTheCall(t *testing.T) {
	m, calls := wqAdvisedModel(t, wqPriorAnswer, 0)
	m.usage = map[string]accountUsage{account.DefaultName: {last: account.Usage{At: time.Now(), Available: true, FiveHour: &account.Window{Pct: 95}}}}
	id := wqSubmit(t, m, "true")
	rec := wqRecord(t, m, id)
	assert.Empty(t, rec.Sizing)
	assert.Equal(t, "the agent's account is at 95% of its 5-hour limit", rec.AgentWhy)
	assert.Empty(t, wqCalls(t, calls))
	require.Len(t, m.queue.advisor.calls, 1, "a dropped call is logged too")
	assert.Equal(t, advisor.OutcomeSkipped, m.queue.advisor.calls[0].Outcome)
	assert.Equal(t, "the agent's account is at 95% of its 5-hour limit", m.queue.advisor.calls[0].Error)
	assert.Zero(t, m.Queue().Agent.Today.Calls, "a dropped call didn't run")
}

func TestAdvisor_TheCallLogSurvivesARestartAndReadAgentPagesIt(t *testing.T) {
	dir := t.TempDir()
	m := wqModelAt(t, dir)
	program, _ := wqFakeClaude(t, wqPriorAnswer, 0)
	m.SetAdvisorForTest(program, 10*time.Second)
	t.Cleanup(func() { wqStop(t, m) })
	var ids []sched.JobID
	for _, argv := range [][]string{{"true", "1"}, {"true", "2"}, {"true", "3"}} {
		id, _, err := m.submitJob(wqJob(t, argv...))
		require.NoError(t, err)
		ids = append(ids, id)
		wqWait(t, "the prior", func() bool { wqPump(t, m); return wqRecord(t, m, id).Sizing == "" })
	}

	m.ReadAgent(2, 5)
	r := requestReply(t, m, 5)
	require.NoError(t, r.Err)
	require.Len(t, r.Agent.Calls, 2, "the limit")
	assert.Equal(t, ids[2], r.Agent.Calls[0].Job, "newest first")
	assert.Equal(t, 3, r.Agent.Kept)
	assert.Equal(t, filepath.Join(m.queue.dir, advisor.CallsFile), r.Agent.Path)
	assert.Empty(t, r.Agent.LogErr)

	again := wqModelAt(t, dir)
	assert.Len(t, again.queue.advisor.calls, 3, "loaded at boot")
}

func TestAdvisor_ACallLogThatFailsToLoadIsKeptInMemoryOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs", advisor.CallsFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{nope\n"), 0o600))

	m := wqModelAt(t, dir)
	require.Error(t, m.queue.advisor.callsErr)
	program, _ := wqFakeClaude(t, wqPriorAnswer, 0)
	m.SetAdvisorForTest(program, 10*time.Second)
	t.Cleanup(func() { wqStop(t, m) })
	id := wqSubmit(t, m, "true")
	wqWait(t, "the prior", func() bool { wqPump(t, m); return wqRecord(t, m, id).Sizing == "" })

	assert.Len(t, m.queue.advisor.calls, 1, "kept in memory")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{nope\n", string(got), "never written")
	m.ReadAgent(0, 6)
	assert.Contains(t, requestReply(t, m, 6).Agent.LogErr, "advisor.jsonl")
}

func TestAdvisor_ASurpriseGetsANote(t *testing.T) {
	m, calls := wqAdvisedModel(t, `{"note":"a cold build after the sweep"}`, 0)
	sub := wqJob(t, "cargo", "build")
	sig := wqSigOf(t, sub.Argv, sub.Cwd)
	b := m.queue.beliefs.Ensure(sig, time.Now())
	b.AddRun(sched.Run{Outcome: sched.OutcomeDone, PeakKB: 1 << 20, WallSeconds: 60})
	t0 := time.Now().Add(-time.Minute)
	rec := sched.Record{
		ID: 50, Order: 50, Argv: sub.Argv, Cwd: sub.Cwd, Description: "build", State: sched.Done,
		Signature: sig.ID(), Claim: sched.Claim{CPUs: 4, MemoryGB: 2},
		Sources:   sched.ClaimSources{CPUs: sched.SourceDefault, Memory: sched.SourceLearned, IO: sched.SourceDefault},
		Submitted: t0, Started: t0, Ended: time.Now(), PeakRSSKB: 3 << 20, Exit: &sched.Exit{Code: 0},
	}
	m.queue.store.Jobs = append(m.queue.store.Jobs, rec)
	m.recordRun(rec)
	wqWait(t, "the note", func() bool { wqPump(t, m); return len(m.queue.beliefs.Find(sig.ID()).Notes) == 1 })
	assert.Equal(t, "a cold build after the sweep", m.queue.beliefs.Find(sig.ID()).Notes[0].Text)
	got := wqCalls(t, calls)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], `"surprise":"it peaked at 3.0 GB, above its 2 GB claim"`)

	m.recordRun(rec)
	wqPump(t, m)
	assert.Len(t, wqCalls(t, calls), 1, "one reconcile per belief in six hours")
}

func TestAdvisor_WithNoClaudeProgramNothingAsks(t *testing.T) {
	m := wqModel(t)
	id := wqSubmit(t, m, "true")
	assert.Empty(t, wqRecord(t, m, id).Sizing)
	assert.Equal(t, "no Claude program is configured", m.Queue().Agent.Why)
}

func TestAdvisor_ABootAsksAgainForAJobLeftAsking(t *testing.T) {
	dir := t.TempDir()
	m := wqModelAt(t, dir)
	program, calls := wqFakeClaude(t, wqPriorAnswer, 0)
	m.SetAdvisorForTest(program, 10*time.Second)
	id := wqSubmit(t, m, "true")
	require.Equal(t, sched.SizingAsking, wqRecord(t, m, id).Sizing)

	t.Setenv(config.EnvGlobalDir, dir)
	again := NewForTest(Options{})
	again.SetJobRunnerForTest(sched.TestRunner())
	again.SetAdvisorForTest(program, 10*time.Second)
	again.loadQueue(dir, wqHost)
	t.Cleanup(func() { wqStop(t, again) })
	wqWait(t, "the prior after the restart", func() bool { wqPump(t, again); return wqRecord(t, again, id).Sizing == "" })
	assert.Equal(t, sched.SourceAgent, wqRecord(t, again, id).Sources.Memory)
	assert.NotEmpty(t, wqCalls(t, calls))
}

func TestSetSchedulerCaps_KeepsTheAgentBlock(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"agent": {"enabled": false, "model": "sonnet"}}`)
	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 8}))
	data, err := os.ReadFile(filepath.Join(m.queue.cfgDir, config.ConfigFileName))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"model": "sonnet"`)
	assert.Contains(t, string(data), `"cpus": 8`)
}
