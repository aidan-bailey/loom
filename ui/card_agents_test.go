package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/subagent"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agents(idle ...bool) []SubagentRow {
	rows := make([]SubagentRow, len(idle))
	for i, isIdle := range idle {
		rows[i] = SubagentRow{Name: "agent", Description: "task", Idle: isIdle}
	}
	return rows
}

func TestAgentSummary(t *testing.T) {
	cases := []struct {
		rows []SubagentRow
		want string
	}{
		{nil, ""},
		{agents(false), " · 1 agent"},
		{agents(true), " · 1 agent (idle)"},
		{agents(false, false, false), " · 3 agents"},
		{agents(false, true, false), " · 3 agents (1 idle)"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, CardData{Subagents: tc.rows}.agentSummary())
	}
}

func TestRenderCard_RailAgentsReplaceTail(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Running, Spinner: "✻",
		TailLines: []string{"waiting for agents"}, Subagents: agents(false, true, false)}
	out := plain(RenderCard(d, DensityRail, 60))
	assert.Contains(t, out, "✻ working · 3 agents (1 idle)")
	assert.NotContains(t, out, "waiting for agents")
}

func TestRenderCard_RailAttentionKeepsReasonAndCount(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Prompting, WaitReason: "sandbox request",
		StatusAge: 4 * time.Minute, Subagents: agents(false, false)}
	out := plain(RenderCard(d, DensityRail, 60))
	assert.Contains(t, out, "❯ sandbox request · 4m · 2 agents")
}

func TestRenderCard_RailWithoutAgentsShowsTail(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Running, Spinner: "✻",
		TailLines: []string{"compiling"}}
	out := plain(RenderCard(d, DensityRail, 60))
	assert.Contains(t, out, "compiling")
	assert.NotContains(t, out, "agent")
}

func TestRenderCard_RailCountCutBeforeStatus(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Running, Spinner: "✻",
		Subagents: agents(false, true, false)}
	out := plain(RenderCard(d, DensityRail, 16))
	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, ansi.StringWidth(l), 16)
	}
	assert.Contains(t, out, "✻ working")
}

func TestBuildCardData_CopiesSubagents(t *testing.T) {
	inst, err := session.NewInstance(session.InstanceOptions{Title: "cd", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.True(t, inst.ApplySubagentScan(subagent.Result{
		LaunchID: "L", Replayed: true,
		Events: []subagent.Event{{Name: subagent.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: "/p/s.jsonl"}},
		Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore", Description: "map code"}},
	}))

	d := BuildCardData(inst, false, "", 0)
	assert.Equal(t, []SubagentRow{{Name: "Explore", Description: "map code"}}, d.Subagents)
}
