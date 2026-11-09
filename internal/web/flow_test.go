package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/internal/flowview"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pastRecord = `# Run: 2026-11-06-p

- Plan: docs/superpowers/plans/2026-11-06-p.md (4 stories)
- Start: 2026-11-06T13:10:52Z, End: 2026-11-06T15:10:55Z
- Ended: stopped

<!-- agent-costs {"total": {"usd": 10.08}} -->
`

func TestAPI_FlowsListsLiveAndPast(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})
	records := filepath.Join(os.Getenv(config.EnvGlobalDir), "flow", "records", "-k")
	require.NoError(t, os.MkdirAll(records, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(records, "2026-11-06-p.md"), []byte(pastRecord), 0o644))

	got := env.readAnswer("/api/v1/flows")
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	assert.JSONEq(t, `{"live": [], "past": [{"key": "-k", "file": "2026-11-06-p.md", "stem": "2026-11-06-p",
		"plan": "docs/superpowers/plans/2026-11-06-p.md", "stories": 4, "ended": "stopped", "end": "2026-11-06T15:10:55Z",
		"usd": 10.08, "unreadable": ""}]}`, string(got.body))
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "flows", result: "200 ok"})
}

func TestAPI_AFlowRouteNeedsItsPlanAndItsDoc(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})
	base := fmt.Sprintf("/api/v1/sessions/%d/flow", env.ids["one"])
	for _, path := range []string{base, base + "?plan=", base + "/doc?plan=p.md", base + "/doc?doc=spec"} {
		status, out := env.do(http.MethodGet, path, nil)
		assert.Equal(t, http.StatusBadRequest, status, path)
		assert.NotEmpty(t, apiErrorOf(t, out), path)
	}
	assert.Empty(t, env.loop.JobsForTest(), "a refused query reaches no model")
}

func TestAPI_AFlowOfASessionWithNoWorktreeIsTheModelsRefusal(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})
	status, out := env.do(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/flow?plan=p.md", env.ids["one"]), nil)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "read a flow of one: the session has no worktree to read", apiErrorOf(t, out))
	status, out = env.do(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/flow/doc?plan=p.md&doc=spec", env.ids["one"]), nil)
	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, apiErrorOf(t, out), "read a flow document of one")
}

func TestFlowOf_TheBoardAsJSON(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	spent, projected := 4.71, 9.80
	view := core.FlowView{
		Detail: flowview.FlowDetail{
			Summary: flowview.Summary{Plan: "docs/p.md", Stage: flowview.StageRun, Status: flowrun.Parked, Run: "r", Done: 1, Total: 2, Asks: flowrun.KindBlocked},
			Stages:  []flowview.Stage{{Name: flowview.StageDesign, State: flowview.StateDone, Detail: "P design", Doc: flowview.DocSpec}},
			Stories: []flowview.StoryView{{ID: "1", Developer: "intermediate-dev", Tier: "deep", Review: "full", Line: 120, End: 300,
				Row:   flowrun.Row{Status: "done", Commits: 7, Findings: [3]int{0, 0, 1}},
				Cells: map[string]string{"dev": flowview.CellPassed, "gates": flowview.CellPassed, "review": flowview.CellPassed, "fix": flowview.CellSkipped},
				Steps: []flowview.StepView{
					{Name: "r-s1-dev1", Kind: "dev", K: 1, Role: "intermediate-dev", Status: "DONE", Jobs: []sched.JobID{3}, Report: true},
					{Name: "r-s1-gates1", Kind: "gates", K: 1, Status: "running", Jobs: []sched.JobID{4}, Gates: []flowview.GateLine{{Text: "go test ./...", Job: 4}}},
				}}},
			Question: &flowrun.Question{Key: "blocked:r-s2-dev1", Kind: flowrun.KindBlocked, Story: "2", Text: "BLOCKED"},
			Kept:     []flowrun.Finding{{Severity: "Minor", File: "a.go", Line: 3, Fix: "tidy it"}},
		},
		Costs: flowview.Costs{Spent: spent, ByStory: map[string]float64{"1": 1.41}, Projection: &projected},
		Times: map[sched.JobID]core.JobTimes{3: {Started: t0, Ended: t0.Add(14 * time.Minute)}, 4: {Started: t0.Add(15 * time.Minute)}},
	}
	raw, err := json.Marshal(flowOf(view, false))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	summary := got["summary"].(map[string]any)
	assert.Equal(t, "parked", summary["status"])
	assert.Equal(t, "blocked-decision", summary["asks"])
	assert.InDelta(t, 4.71, summary["spent"], 1e-9)
	story := got["stories"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"dev": "passed", "gates": "passed", "review": "passed", "fix": "skipped"}, story["cells"])
	steps := story["steps"].([]any)
	dev := steps[0].(map[string]any)
	assert.Equal(t, "2026-11-09T10:00:00Z", dev["started"])
	assert.Equal(t, "2026-11-09T10:14:00Z", dev["ended"])
	gate := steps[1].(map[string]any)
	assert.Equal(t, "", gate["ended"], "a step whose latest job runs has no end")
	assert.Equal(t, []any{map[string]any{"text": "go test ./...", "verdict": "", "job": float64(4)}}, gate["gates"])
	assert.Equal(t, []any{"Minor a.go:3 tidy it"}, got["kept"])
	assert.Equal(t, "blocked:r-s2-dev1", got["question"].(map[string]any)["key"])
	costs := got["costs"].(map[string]any)
	assert.InDelta(t, 9.80, costs["projection"], 1e-9)
	assert.Equal(t, []any{}, costs["unpriced"])

	lost, err := json.Marshal(flowOf(view, true))
	require.NoError(t, err)
	assert.Contains(t, string(lost), `"status":"lost"`)
}

func TestSessionJSON_CarriesTheFlows(t *testing.T) {
	v := core.InstanceView{ID: 4, Title: "s", Flows: []core.FlowSummary{
		{Summary: flowview.Summary{Plan: "docs/a.md", Stage: flowview.StageRun, Status: flowrun.Running, Done: 2, Total: 5}, Lost: true, Spent: 4.71, Priced: true},
		{Summary: flowview.Summary{Plan: "docs/b-design.md", Stage: flowview.StageDesign, Status: flowview.StatusDesigning}},
	}}
	s := sessionOf(1, v)
	require.Len(t, s.Flows, 2)
	assert.Equal(t, "lost", s.Flows[0].Status)
	require.NotNil(t, s.Flows[0].Spent)
	assert.InDelta(t, 4.71, *s.Flows[0].Spent, 1e-9)
	assert.Nil(t, s.Flows[1].Spent, "not priced is null, never 0")
	assert.Equal(t, []flowSummaryJSON{}, sessionOf(1, core.InstanceView{}).Flows, "an empty list for none")
}

func TestBuildTreeLimit_HeadingsCarryTheirLines(t *testing.T) {
	text := "# Title\n\nsome text\n\n## Two\n\n```\n## not a heading\n```\n\n### Three\n"
	blocks, cut := buildTreeLimit(text, 1<<20, 1000, true)
	assert.False(t, cut)
	var lines []int
	for _, b := range blocks {
		if b.T == "h" {
			lines = append(lines, b.Line)
		}
	}
	assert.Equal(t, []int{1, 5, 11}, lines)
	plain, _ := buildTree(text)
	assert.Zero(t, plain[0].Line, "a message's tree carries no lines")
	_, cut = buildTreeLimit(text, 1<<20, 2, true)
	assert.True(t, cut, "past its node bound the tree says it is cut")
}
