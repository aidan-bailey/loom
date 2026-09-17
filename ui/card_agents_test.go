package ui

import (
	"regexp"
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

// themeSGR matches the SGR sequences card styling emits itself: resets,
// bold, and truecolor foreground/background from the theme roles. What
// is left after removing them came from the card's text, not its chrome.
var themeSGR = regexp.MustCompile(`\x1b\[(?:(?:1|[34]8;2;\d+;\d+;\d+);?)*m`)

// Subagent names and descriptions come from metadata files the model
// writes, and the wait reason from Claude's roster, so each can carry
// terminal escapes (here an OSC 52 clipboard write) or a newline that
// would break the overview card's fixed height.
func TestBuildCardData_SanitizesAgentText(t *testing.T) {
	const osc52 = "\x1b]52;c;aGk=\x07pwn\nnext"
	inst, err := session.NewInstance(session.InstanceOptions{Title: "cd", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.True(t, inst.ApplySubagentScan(subagent.Result{
		LaunchID: "L", Replayed: true,
		Events: []subagent.Event{{Name: subagent.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: "/p/s.jsonl"}},
		Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore", Name: "\x1b[31mred", Description: osc52}},
	}))
	inst.SetWaitReason(osc52)
	require.NoError(t, inst.TransitionTo(session.Prompting))

	d := BuildCardData(inst, false, "", 0)
	require.Len(t, d.Subagents, 1)
	overview := renderOverviewCard(d, 60)
	rail := RenderCard(d, DensityRail, 60)

	for name, out := range map[string]string{"overview": overview, "rail": rail} {
		text := themeSGR.ReplaceAllString(out, "")
		assert.NotContains(t, text, "\x1b", name)
		assert.NotContains(t, text, "\x07", name)
	}
	assert.Len(t, strings.Split(overview, "\n"), overviewCardHeight)
	assert.Contains(t, plain(overview), "red")
	assert.Contains(t, plain(overview), "pwn next")
}

func TestSanitizeCardText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "Implement Task 3", "Implement Task 3"},
		{"unicode", "résumé ✻ 日本", "résumé ✻ 日本"},
		{"tab", "a\tb", "a b"},
		{"cr", "a\rb", "a b"},
		{"lf", "a\nb", "a b"},
		{"del", "a\x7fb", "a b"},
		{"nel", "a\u0085b", "a b"},
		{"bel", "a\x07b", "a b"},
		{"sgr", "\x1b[31mred\x1b[0m", "red"},
		{"osc 52", "\x1b]52;c;aGk=\x07pwn", "pwn"},
		{"invalid utf-8", "a\xffb", "ab"},
		{"c1 csi rune", "a\u009b31mb", "a 31mb"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, sanitizeCardText(tc.in), tc.name)
	}
}

func overviewLines(t *testing.T, d CardData, width int) []string {
	t.Helper()
	return strings.Split(plain(renderOverviewCard(d, width)), "\n")
}

func TestOverview_NoAgentsKeepsTail(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, TailLines: []string{"one", "two"}}
	lines := overviewLines(t, d, 50)
	assert.Contains(t, lines[4], "one")
	assert.Contains(t, lines[5], "two")
}

func TestOverview_OneAgentKeepsLastTailLine(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, TailLines: []string{"one", "two"},
		Subagents: []SubagentRow{{Name: "impl-t3", Description: "Implement Task 3"}}}
	lines := overviewLines(t, d, 50)
	assert.Contains(t, lines[4], "└ ✻ impl-t3  Implement Task 3")
	assert.Contains(t, lines[5], "two")
}

func TestOverview_TwoAgentsAligned(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, Subagents: []SubagentRow{
		{Name: "impl-t3", Description: "Implement Task 3"},
		{Name: "spec", Description: "Review spec", Idle: true},
	}}
	lines := overviewLines(t, d, 50)
	assert.Contains(t, lines[4], "├ ✻ impl-t3  Implement Task 3")
	assert.Contains(t, lines[5], "└ ◦ spec     idle")
	assert.NotContains(t, lines[5], "Review spec", "idle rows show 'idle'")
}

func TestOverview_ManyAgentsSummarized(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, Subagents: []SubagentRow{
		{Name: "a", Description: "first"},
		{Name: "b", Description: "second"},
		{Name: "c", Idle: true},
		{Name: "d", Idle: true},
	}}
	lines := overviewLines(t, d, 50)
	assert.Contains(t, lines[4], "├ ✻ a  first")
	assert.Contains(t, lines[5], "└ +3 more · 1 working · 2 idle")

	allIdle := CardData{Title: "db", Status: session.Running, Subagents: []SubagentRow{
		{Name: "a", Description: "first"}, {Name: "c", Idle: true}, {Name: "d", Idle: true},
	}}
	lines = overviewLines(t, allIdle, 50)
	assert.Contains(t, lines[5], "└ +2 more · 2 idle")
	assert.NotContains(t, lines[5], "working")
}

func TestOverview_LongNamesCapped(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, Subagents: []SubagentRow{
		{Name: strings.Repeat("n", 30), Description: "desc"},
		{Name: "b", Description: "other"},
	}}
	lines := overviewLines(t, d, 60)
	assert.Contains(t, lines[4], strings.Repeat("n", 13)+"…  desc")
	assert.Contains(t, lines[5], "└ ✻ b"+strings.Repeat(" ", 13)+"  other")
}
