package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/attention"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// atModel is a model whose classifier booted under a fresh global dir (its
// config.json holding cfg, when not ""), holding a started Claude session
// "s" on launch L1.
func atModel(t *testing.T, cfg string) (*Model, *session.Instance) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	if cfg != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(cfg), 0o600))
	}
	m := NewForTest(Options{})
	inst := atInst(t, m, "s")
	m.bootAttention()
	m.Drain()
	return m, inst
}

// atInst is another started Claude session m holds, on launch L1.
func atInst(t *testing.T, m *Model, title string) *session.Instance {
	t.Helper()
	inst := activeInst(t, m, title)
	adoptLaunch(t, m, inst, "L1", time.Now())
	m.Drain()
	return inst
}

// atClassifier points m's classifier at a fake claude that saves each
// call's stdin and CLAUDE_CONFIG_DIR under the returned dir and answers
// answer (the structured output's JSON), or exits 1 when answer is "".
func atClassifier(t *testing.T, m *Model, answer string) string {
	t.Helper()
	calls := t.TempDir()
	body := "echo boom >&2; exit 1"
	if answer != "" {
		body = `printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"x","structured_output":` + answer + `}'`
	}
	program := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\nn=$$\ncat > '" + calls + "'/stdin.$n\necho \"$CLAUDE_CONFIG_DIR\" > '" + calls + "'/env.$n\n" + body + "\n"
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	m.attention.program, m.attention.timeout = program, 5*time.Second
	return calls
}

// atScan delivers a scan of events on launch L1.
func atScan(m *Model, inst *session.Instance, events ...hooks.Event) {
	m.Deliver(hookScanResults{results: []hookScanResult{{instance: inst, result: session.HookScanResult{LaunchID: "L1", Events: events}}}})
}

// atStop delivers a scan of one parent Stop at at.
func atStop(m *Model, inst *session.Instance, at time.Time, msg string) {
	atScan(m, inst, hooks.Event{Name: hooks.EventStop, At: at, LastAssistantMessage: msg})
}

// atPrompt delivers a scan of the person's next prompt.
func atPrompt(m *Model, inst *session.Instance, at time.Time) {
	atScan(m, inst, hooks.Event{Name: hooks.EventUserPromptSubmit, At: at})
}

// atRun runs m's jobs, and the ones their deliveries queue, until none is
// left, and returns the events.
func atRun(t *testing.T, m *Model) []Event {
	t.Helper()
	var events []Event
	for range 20 {
		out := m.Drain()
		events = append(events, out.Events...)
		if len(out.Jobs) == 0 {
			return events
		}
		for _, job := range out.Jobs {
			m.Deliver(job())
		}
	}
	t.Fatal("the jobs never settled")
	return nil
}

// atCalls counts the calls the fake claude saw.
func atCalls(t *testing.T, dir string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "stdin.*"))
	require.NoError(t, err)
	return len(files)
}

// atLogged is the call log on disk.
func atLogged(t *testing.T, m *Model) []attention.CallRecord {
	t.Helper()
	calls, err := attention.LoadCalls(m.attention.dir)
	require.NoError(t, err)
	return calls
}

func TestAttention_AStopGetsItsVerdict(t *testing.T) {
	m, inst := atModel(t, "")
	calls := atClassifier(t, m, `{"verdict":"needs_you","gist":"choose A or B"}`)
	stop := time.Now()
	atStop(m, inst, stop, "Option A or B?")

	v := m.viewOf(inst)
	assert.Equal(t, session.Ready, v.Status)
	assert.True(t, v.VerdictPending, "a call waits or runs for the turn")
	assert.True(t, v.VerdictStop.Equal(stop))
	assert.False(t, v.NeedsYou())

	atRun(t, m)
	v = m.viewOf(inst)
	assert.False(t, v.VerdictPending)
	assert.Equal(t, attention.NeedsYou, v.Verdict)
	assert.Equal(t, "choose A or B", v.VerdictGist)
	assert.True(t, v.NeedsYou())
	require.Equal(t, 1, atCalls(t, calls))

	files, _ := filepath.Glob(filepath.Join(calls, "stdin.*"))
	stdin, err := os.ReadFile(files[0])
	require.NoError(t, err)
	assert.Contains(t, string(stdin), `"last_message":"Option A or B?"`)
	assert.Contains(t, string(stdin), `"title":"s"`)

	logged := atLogged(t, m)
	require.Len(t, logged, 1)
	assert.Equal(t, attention.OutcomeAnswered, logged[0].Outcome)
	assert.Equal(t, attention.NeedsYou, logged[0].Verdict)
	raw, err := os.ReadFile(filepath.Join(m.attention.dir, attention.CallsFile))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "Option A or B?", "the log holds no message")

	atPrompt(m, inst, stop.Add(time.Second))
	v = m.viewOf(inst)
	assert.Empty(t, v.Verdict, "a reply ends the verdict")
	assert.False(t, v.NeedsYou())
}

func TestAttention_AStaleAnswerIsDropped(t *testing.T) {
	m, inst := atModel(t, "")
	atClassifier(t, m, `{"verdict":"done","gist":"x"}`)
	stop := time.Now()
	atStop(m, inst, stop, "Done.")
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	atPrompt(m, inst, stop.Add(time.Second)) // the person typed before the answer
	m.Deliver(jobs[0]())

	assert.Empty(t, m.viewOf(inst).Verdict)
	logged := atLogged(t, m)
	require.Len(t, logged, 1)
	assert.Equal(t, attention.OutcomeDropped, logged[0].Outcome)
}

func TestAttention_ANewerStopReplacesAWaitingCall(t *testing.T) {
	m, inst := atModel(t, `{"attention":{"max_parallel":1}}`)
	other := atInst(t, m, "o")
	calls := atClassifier(t, m, `{"verdict":"done","gist":"x"}`)
	t0 := time.Now()
	atStop(m, other, t0, "first") // takes the one slot
	atStop(m, inst, t0.Add(time.Millisecond), "one")
	atStop(m, inst, t0.Add(2*time.Millisecond), "two")
	require.Len(t, m.attention.pending, 1, "one call per session waits")
	assert.True(t, m.attention.pending[0].stop.Equal(t0.Add(2*time.Millisecond)))

	atRun(t, m)
	assert.Equal(t, 2, atCalls(t, calls), "the replaced Stop never ran")
	assert.Equal(t, attention.Done, m.viewOf(inst).Verdict)
	assert.Equal(t, attention.Done, m.viewOf(other).Verdict)
}

func TestAttention_WhatQueuesNothing(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		m, inst := atModel(t, `{"attention":{"enabled":false}}`)
		atClassifier(t, m, `{"verdict":"done","gist":"x"}`)
		atStop(m, inst, time.Now(), "Done.")
		assert.False(t, m.viewOf(inst).VerdictPending)
		assert.Empty(t, m.Drain().Jobs)
	})
	t.Run("an empty message", func(t *testing.T) {
		m, inst := atModel(t, "")
		atClassifier(t, m, `{"verdict":"done","gist":"x"}`)
		atStop(m, inst, time.Now(), "")
		assert.False(t, m.viewOf(inst).VerdictPending)
	})
	t.Run("a Stop that leaves a subagent running", func(t *testing.T) {
		m, inst := atModel(t, "")
		atClassifier(t, m, `{"verdict":"done","gist":"x"}`)
		atScan(m, inst, hooks.Event{Name: hooks.EventStop, At: time.Now(), LastAssistantMessage: "waiting on the reviewer",
			Tasks: []hooks.Task{{Type: "subagent", Status: "running"}}})
		assert.Equal(t, session.Running, inst.GetStatus())
		assert.False(t, m.viewOf(inst).VerdictPending)
	})
	t.Run("the ordinary case still queues", func(t *testing.T) {
		m, inst := atModel(t, "")
		atClassifier(t, m, `{"verdict":"done","gist":"x"}`)
		atStop(m, inst, time.Now(), "Done.")
		assert.True(t, m.viewOf(inst).VerdictPending)
	})
}

func TestAttention_AHighUsageAccountIsSkipped(t *testing.T) {
	m, inst := atModel(t, "")
	calls := atClassifier(t, m, `{"verdict":"done","gist":"x"}`)
	m.usage = map[string]accountUsage{account.DefaultName: {last: account.Usage{At: time.Now(), FiveHour: &account.Window{Pct: 95}}}}
	atStop(m, inst, time.Now(), "Done.")
	atRun(t, m)
	assert.Equal(t, 0, atCalls(t, calls))
	assert.False(t, m.viewOf(inst).VerdictPending, "a skip clears the wait, so the push goes out")
	logged := atLogged(t, m)
	require.Len(t, logged, 1)
	assert.Equal(t, attention.OutcomeSkipped, logged[0].Outcome)
	assert.Contains(t, logged[0].Error, "5-hour")
}

func TestAttention_EachCallRunsAsItsSessionsAccount(t *testing.T) {
	m, inst := atModel(t, "")
	withAccounts(t, m, "work")
	inst.SetAccount("work")
	calls := atClassifier(t, m, `{"verdict":"done","gist":"x"}`)
	atStop(m, inst, time.Now(), "Done.")
	atRun(t, m)
	files, _ := filepath.Glob(filepath.Join(calls, "env.*"))
	require.Len(t, files, 1)
	env, err := os.ReadFile(files[0])
	require.NoError(t, err)
	assert.Contains(t, string(env), "work", "the account's config dir")
}

func TestAttention_ThreeFailuresPauseWithOneNotice(t *testing.T) {
	m, inst := atModel(t, "")
	atClassifier(t, m, "")
	t0 := time.Now()
	var events []Event
	for i := range attentionPauseAfter {
		atStop(m, inst, t0.Add(time.Duration(i)*time.Second), "Done.")
		events = append(events, atRun(t, m)...)
		assert.False(t, m.viewOf(inst).VerdictPending, "a failure clears the wait")
	}
	notices := 0
	for _, ev := range events {
		if n, ok := ev.(Notice); ok && n.Err != nil && strings.Contains(n.Err.Error(), "stop classifier: paused") {
			notices++
		}
	}
	assert.Equal(t, 1, notices)
	assert.Contains(t, m.attentionOff(time.Now()), "paused until")
	atStop(m, inst, t0.Add(time.Minute), "Done.")
	assert.False(t, m.viewOf(inst).VerdictPending, "a paused classifier queues nothing")
}

func TestAttention_TheCallLogAnswersAReplayedStop(t *testing.T) {
	m, inst := atModel(t, "")
	calls := atClassifier(t, m, `{"verdict":"needs_you","gist":"pick"}`)
	stop := time.Now().Add(-time.Hour).Truncate(time.Second)
	atStop(m, inst, stop, "A or B?")
	atRun(t, m)
	require.Equal(t, 1, atCalls(t, calls))

	// A restart: a new model over the same global dir replays the Stop. A
	// new process's instance knows no last message yet; an empty replay
	// stands in for that, since the fixture's instance carries m's.
	m2 := NewForTest(Options{})
	hold(m2, inst)
	m2.bootAttention()
	m2.attention.program = m.attention.program
	replay := func(events ...hooks.Event) {
		m2.Deliver(hookScanResults{results: []hookScanResult{{instance: inst, result: session.HookScanResult{
			LaunchID: "L1", Replayed: true, Events: events,
		}}}})
	}
	replay()
	_, _, current := inst.LastStop()
	require.False(t, current)
	replay(hooks.Event{Name: hooks.EventStop, At: stop, LastAssistantMessage: "A or B?"})
	atRun(t, m2)
	assert.Equal(t, 1, atCalls(t, calls), "no second call")
	assert.Equal(t, attention.NeedsYou, m2.viewOf(inst).Verdict)
}

func TestAttention_ACallThatWaitedTooLongIsSkipped(t *testing.T) {
	m, inst := atModel(t, `{"attention":{"max_parallel":1,"wait_seconds":5}}`)
	other := atInst(t, m, "o")
	atClassifier(t, m, `{"verdict":"done","gist":"x"}`)
	atStop(m, other, time.Now(), "first") // holds the slot
	atStop(m, inst, time.Now(), "second")
	require.Len(t, m.attention.pending, 1)
	m.attention.pending[0].queued = time.Now().Add(-10 * time.Second)
	m.tickAttention()
	assert.Empty(t, m.attention.pending)
	assert.False(t, m.viewOf(inst).VerdictPending, "the skip clears the wait")
	logged := atLogged(t, m)
	require.Len(t, logged, 1)
	assert.Equal(t, attention.OutcomeSkipped, logged[0].Outcome)
}

func TestInstanceView_NeedsYou(t *testing.T) {
	for _, tc := range []struct {
		status  session.Status
		verdict string
		want    bool
	}{
		{session.Prompting, "", true},
		{session.Ready, attention.NeedsYou, true},
		{session.Ready, attention.Done, false},
		{session.Ready, attention.Waiting, false},
		{session.Ready, attention.Stopped, false},
		{session.Ready, "", false},
		{session.Running, attention.NeedsYou, false},
	} {
		assert.Equal(t, tc.want, InstanceView{Status: tc.status, Verdict: tc.verdict}.NeedsYou(), "%v %q", tc.status, tc.verdict)
	}
}
