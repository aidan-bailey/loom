package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// limitLog is an agent's stream-json log that stopped on window's usage
// limit, resetting at resets.
func limitLog(window string, resets time.Time) string {
	return `{"type":"system","subtype":"init","session_id":"s1"}` + "\n" +
		fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[]},"api_error_status":429,"api_error":"usage_limit_reached","api_error_params":{"rate_limit_info":{"status":"rejected","rateLimitType":%q,"resetsAt":%d}}}`, window, resets.Unix()) + "\n" +
		`{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"api_error":"usage_limit_reached","result":"You've hit your limit","session_id":"s1"}` + "\n"
}

// runningAgent seeds agent job id of account, running.
func runningAgent(t *testing.T, m *Model, id sched.JobID, account string) {
	t.Helper()
	now := time.Now()
	wqSeed(t, m, sched.Record{ID: id, Order: int64(id), Kind: sched.KindAgent, AccountKey: account,
		Argv: []string{"loom", "flow", "agent-exec"}, Cwd: "/work", State: sched.Running,
		Submitted: now.Add(-time.Minute), Started: now.Add(-time.Minute)})
}

// endAgent ends agent job id in state, with log written to its output.log
// only after the end: what the daemon learns, it reads off the loop.
func endAgent(t *testing.T, m *Model, id sched.JobID, state sched.State, log string) {
	t.Helper()
	m.finishJob(m.queue.index(id), state, time.Now())
	assert.Empty(t, m.queue.limits, "nothing is read on the loop")
	require.NoError(t, os.WriteFile(filepath.Join(m.queue.jobDir(id), sched.LogFile), []byte(log), 0o600))
	wqPump(t, m)
}

func TestJobQueue_AnAgentFailedOnAUsageLimitHoldsItsAccount(t *testing.T) {
	m := wqModel(t)
	resets := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	runningAgent(t, m, 1, "/acc/a")

	endAgent(t, m, 1, sched.Failed, limitLog("five_hour", resets))

	require.Contains(t, m.queue.limits, "/acc/a")
	assert.Equal(t, "five_hour", m.queue.limits["/acc/a"].Window)
	assert.True(t, resets.Equal(m.queue.limits["/acc/a"].Resets))

	sub := wqJob(t, "sh", "-c", "exit 0")
	sub.Kind = sched.KindAgent
	sub.Env = append(sub.Env, "CLAUDE_CONFIG_DIR=/acc/a")
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.Queued, wqRecord(t, m, id).State, "the account's next agent waits")
	assert.Equal(t, "waits for the account's usage limit (5-hour, resets "+sched.ResetText(resets, time.Now())+")", m.queue.waiting[id])

	other := wqJob(t, "sh", "-c", "exit 0")
	other.Kind = sched.KindAgent
	other.Env = append(other.Env, "CLAUDE_CONFIG_DIR=/acc/b")
	oid, _, err := m.submitJob(other)
	require.NoError(t, err)
	assert.Equal(t, sched.Running, wqRecord(t, m, oid).State, "another account's agent starts")
}

func TestJobQueue_OnlyAUsageLimitAheadHoldsAnAccount(t *testing.T) {
	cases := []struct {
		name  string
		state sched.State
		log   string
	}{
		{"a plain failure", sched.Failed, `{"type":"result","is_error":true,"api_error_status":500,"result":"API Error"}` + "\n"},
		{"a limit whose reset has passed", sched.Failed, limitLog("five_hour", time.Now().Add(-2*time.Hour))},
		{"a limit with no reset", sched.Failed, `{"type":"result","is_error":true,"api_error_status":429,"api_error":"usage_limit_reached","result":"limit"}` + "\n"},
		{"an agent that ended done", sched.Done, limitLog("five_hour", time.Now().Add(2*time.Hour))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := wqModel(t)
			runningAgent(t, m, 1, "/acc/a")
			endAgent(t, m, 1, tc.state, tc.log)
			assert.Empty(t, m.queue.limits)
		})
	}
}

func TestJobQueue_ABuildJobsLogIsNotReadForALimit(t *testing.T) {
	m := wqModel(t)
	now := time.Now()
	wqSeed(t, m, sched.Record{ID: 1, Order: 1, Argv: []string{"cargo", "test"}, Cwd: "/work", State: sched.Running,
		Submitted: now, Started: now, AccountKey: ""})
	endAgent(t, m, 1, sched.Failed, limitLog("five_hour", now.Add(2*time.Hour)))
	assert.Empty(t, m.queue.limits)
}
