package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched/advisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAgent(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.Local)
	page := core.AgentPage{
		State: core.AgentView{On: true, Model: "opus", Effort: "low", Wait: time.Minute, Pending: 2, Failures: 1,
			Flight: core.AgentFlight{Kind: "prior", Job: 17, Command: "cargo build --release", Started: now.Add(-12 * time.Second)},
			Today:  core.AgentDay{Calls: 5, Failed: 1, CostUSD: 0.0612}},
		Calls: []advisor.CallRecord{
			{At: now.Add(-time.Minute), Kind: advisor.KindPrior, Job: 16, Command: "go test ./...", Model: "opus", Effort: "low",
				Usage:   advisor.Usage{DurationMS: 6030, InputTokens: 2, CacheWriteTokens: 3435, OutputTokens: 180, CostUSD: 0.0285},
				Outcome: advisor.OutcomeAnswered, Answer: `{"like":"","cpus":4,"memory_gb":3,"io":false,"duration_seconds":60,"note":"runs the Go tests"}`},
			{At: now.Add(-2 * time.Minute), Kind: advisor.KindReconcile, Job: 14, Command: "bash scripts/web-smoke.sh", Model: "opus", Effort: "low",
				Usage: advisor.Usage{DurationMS: 4100, CostUSD: 0.009}, Outcome: advisor.OutcomeAnswered, Answer: `{"note":"a cold build"}`},
			{At: now.Add(-25 * time.Hour), Kind: advisor.KindPrior, Job: 3, Command: "make", Model: "opus", Effort: "low",
				Outcome: advisor.OutcomeSkipped, Error: "the agent's account is at 93% of its 5-hour limit"},
		},
		Kept: 143, Path: "/home/x/.loom/jobs/advisor.jsonl",
	}
	var out bytes.Buffer
	writeAgent(&out, page, now)
	text := out.String()
	for _, want := range []string{
		"agent: on · opus, low · a job waits up to 1m for it",
		"in flight: prior for job 17 (cargo build --release), 12s",
		"waiting: 2 calls",
		"failures in a row: 1",
		"today: 5 calls (1 failed), $0.06",
		"log: /home/x/.loom/jobs/advisor.jsonl, 143 calls",
	} {
		assert.Contains(t, text, want)
	}
	assert.Regexp(t, `(?m)^11:59 +prior +16 +go test \./\.\.\. +opus/low +6\.0s +3\.4k/180 +\$0\.029 +4c/3G ~1m: runs the Go tests$`, text)
	assert.Regexp(t, `(?m)^11:58 +reconcile +14 +bash scripts/web-smoke\.sh +opus/low +4\.1s +- +\$0\.009 +note: a cold build$`, text)
	assert.Regexp(t, `(?m)^11-04 11:00 +prior +3 +make +opus/low +- +- +- +skipped: the agent's account is at 93% of its 5-hour limit$`, text)

	out.Reset()
	writeAgent(&out, core.AgentPage{State: core.AgentView{Why: "no Claude program is configured"}, LogErr: "advisor.jsonl line 1: bad"}, now)
	assert.Contains(t, out.String(), "agent: off · no Claude program is configured")
	assert.Contains(t, out.String(), "today: no calls")
	assert.Contains(t, out.String(), "log: kept in memory only (advisor.jsonl line 1: bad)")
	assert.Contains(t, out.String(), "No calls yet.")
}

// agent reports through a daemon, as text and as JSON.
func TestJobAgent_ShowsTheAgentsState(t *testing.T) {
	serveDaemon(t, "true")
	out, _, err := runJob(t, "agent")
	require.NoError(t, err)
	assert.Contains(t, out, "agent: off · no Claude program is configured")
	assert.Contains(t, out, "advisor.jsonl, 0 calls")

	out, _, err = runJob(t, "agent", "--json")
	require.NoError(t, err)
	var page core.AgentPage
	require.NoError(t, json.Unmarshal([]byte(out), &page))
	assert.False(t, page.State.On)
	assert.Contains(t, page.Path, "advisor.jsonl")
}
