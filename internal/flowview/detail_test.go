package flowview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boardRepo is a repo with a joined plan of three stories and its spec.
func boardRepo(t *testing.T) (repo, base, records, state string) {
	t.Helper()
	repo, base, records = branchRepo(t)
	commitFile(t, repo, specP, "# P design\n")
	commitFile(t, repo, planP, joined(specP, "", "1", "2", "3"))
	return repo, base, records, flowStateDir(t, repo)
}

func memo(t *testing.T, st *flowrun.State, key string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	st.Memo[key] = raw
}

func agentRec(t *testing.T, state, name, role, status string, job sched.JobID) {
	t.Helper()
	dir := filepath.Join(state, "agents")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, flowrun.SaveAgent(dir, &flowrun.AgentRecord{Schema: flowrun.AgentSchema, ID: name, Role: role, Status: status, Jobs: []flowrun.AgentRecordJob{{ID: job}}}))
}

func gateRec(t *testing.T, state, name string, verdict string, job sched.JobID) {
	t.Helper()
	dir := filepath.Join(state, "gates")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, flowrun.SaveGate(dir, &flowrun.GateRecord{Schema: flowrun.GateSchema, Name: name, Kind: "story",
		Steps: []flowrun.GateRecordStep{{Line: "RUN go test ./...", Run: true, Jobs: []sched.JobID{job}, Verdict: verdict}}}))
}

func TestDetail_TheBoard(t *testing.T) {
	repo, base, records, state := boardRepo(t)
	st := runState("r", flowrun.Parked,
		flowrun.Story{ID: "1", Developer: "intermediate-dev", Tier: "deep", Review: "full", Row: flowrun.Row{Status: "done", Commits: 7}},
		flowrun.Story{ID: "2", Developer: "junior-dev", Tier: "light", Review: "vouched", Row: flowrun.Row{Status: "done", Commits: 4}},
		flowrun.Story{ID: "3", Developer: "intermediate-dev", Tier: "deep", Review: "full", Row: flowrun.Row{Status: "developing"}})
	st.Agents = []string{"r-premise1", "r-s1-dev1", "r-s1-fix1", "r-s1-review1", "r-s2-dev1", "r-s2-review1", "r-s3-dev1"}
	st.Gates = []string{"r-s1-gates1", "r-s1-gates2", "r-s2-gates1"}
	st.Question = &flowrun.Question{Key: "blocked:r-s3-dev1", Kind: flowrun.KindBlocked, Story: "3", Text: "r-s3-dev1 is BLOCKED"}
	jobs := map[string]sched.JobID{"r-premise1": 1, "r-s1-dev1": 2, "r-s1-gates1": 3, "r-s1-fix1": 4, "r-s1-gates2": 5, "r-s1-review1": 6, "r-s2-dev1": 7, "r-s2-gates1": 8, "r-s2-review1": 9, "r-s3-dev1": 10}
	roles := map[string]string{"r-premise1": "premise-check", "r-s1-review1": "code-quality-reviewer", "r-s2-dev1": "junior-dev", "r-s2-review1": "story-reviewer"}
	for _, name := range st.Agents {
		status := "DONE"
		if name == "r-s3-dev1" {
			status = "BLOCKED"
		}
		role := roles[name]
		if role == "" {
			role = "intermediate-dev"
		}
		agentRec(t, state, name, role, status, jobs[name])
		memo(t, st, "agent:"+name, flowrun.Report{Status: status})
	}
	gateRec(t, state, "r-s1-gates1", "FAIL", jobs["r-s1-gates1"])
	memo(t, st, "gates:r-s1-gates1", flowrun.GateResult{Ended: "failed"})
	gateRec(t, state, "r-s1-gates2", "PASS", jobs["r-s1-gates2"])
	memo(t, st, "gates:r-s1-gates2", flowrun.GateResult{Ended: "passed"})
	gateRec(t, state, "r-s2-gates1", "PASS", jobs["r-s2-gates1"])
	memo(t, st, "gates:r-s2-gates1", flowrun.GateResult{Ended: "passed"})
	writeFile(t, state, "agents/r-s1-dev1/report.md", "STATUS: DONE\n")
	writeRun(t, state, st)

	d, err := Detail(repo, base, records, planP)
	require.NoError(t, err)
	require.Len(t, d.Stories, 3)
	assert.Equal(t, map[string]string{"dev": CellPassed, "gates": CellPassed, "review": CellPassed, "fix": CellPassed, "docs": CellSkipped}, d.Stories[0].Cells)
	assert.Equal(t, map[string]string{"dev": CellPassed, "gates": CellPassed, "review": CellPassed, "fix": CellSkipped, "docs": CellSkipped}, d.Stories[1].Cells)
	assert.Equal(t, map[string]string{"dev": CellAsks, "gates": CellTodo, "review": CellTodo, "fix": CellTodo, "docs": CellTodo}, d.Stories[2].Cells)
	require.NotNil(t, d.Premise)
	assert.Equal(t, map[string]string{"premise": CellPassed}, d.Premise.Cells)
	assert.Nil(t, d.Final, "no final step yet")
	require.NotNil(t, d.Question)
	assert.Equal(t, flowrun.KindBlocked, d.Asks)

	var names []string
	for _, s := range d.Stories[0].Steps {
		names = append(names, s.Name)
	}
	assert.Equal(t, []string{"r-s1-dev1", "r-s1-gates1", "r-s1-fix1", "r-s1-gates2", "r-s1-review1"}, names, "in the order they started")
	dev := d.Stories[0].Steps[0]
	assert.Equal(t, StepView{Name: "r-s1-dev1", Kind: "dev", K: 1, Role: "intermediate-dev", Status: "DONE", Jobs: []sched.JobID{2}, Report: true}, dev)
	gate := d.Stories[0].Steps[1]
	assert.Equal(t, "failed", gate.Status)
	assert.Equal(t, []GateLine{{Text: "go test ./...", Verdict: "FAIL", Job: 3}}, gate.Gates)
	assert.Equal(t, "spec §1", d.Stories[0].Reading)
	assert.Equal(t, 7, d.Stories[0].Row.Commits)
}

// A doc pass has its cell, and a story gated again after it shows the kinds
// it went past without an attempt as skipped, whatever its status says (the
// doc pass's spec, §5.7).
func TestDetail_TheDocCell(t *testing.T) {
	repo, base, records, state := boardRepo(t)
	st := runState("r", flowrun.Running,
		flowrun.Story{ID: "1", Developer: "intermediate-dev", Tier: "deep", Review: "vouched", Row: flowrun.Row{Status: "gating"}},
		flowrun.Story{ID: "2", Developer: "intermediate-dev", Tier: "deep", Review: "vouched", Row: flowrun.Row{Status: "done"}})
	st.Docs = true
	st.Agents = []string{"r-s1-dev1", "r-s1-review1", "r-s1-docs1", "r-s2-dev1", "r-s2-review1"}
	st.Gates = []string{"r-s1-gates1", "r-s1-gates2", "r-s2-gates1"}
	roles := map[string]string{"r-s1-review1": "story-reviewer", "r-s1-docs1": "doc-writer", "r-s2-review1": "story-reviewer"}
	for i, name := range st.Agents {
		role := roles[name]
		if role == "" {
			role = "intermediate-dev"
		}
		agentRec(t, state, name, role, "DONE", sched.JobID(i+1))
		memo(t, st, "agent:"+name, flowrun.Report{Status: "DONE"})
	}
	for i, name := range st.Gates {
		gateRec(t, state, name, "PASS", sched.JobID(i+10))
		memo(t, st, "gates:"+name, flowrun.GateResult{Ended: "passed"})
	}
	writeRun(t, state, st)

	d, err := Detail(repo, base, records, planP)
	require.NoError(t, err)
	require.Len(t, d.Stories, 2, "a run's rows are its state's stories")
	assert.Equal(t, map[string]string{"dev": CellPassed, "gates": CellPassed, "review": CellPassed, "fix": CellSkipped, "docs": CellPassed}, d.Stories[0].Cells,
		"its gates after the doc pass say gating; the fix it never had is skipped, not todo")
	assert.Equal(t, CellSkipped, d.Stories[1].Cells["fix"])
	assert.Equal(t, CellSkipped, d.Stories[1].Cells["docs"], "a done story with no doc pass")
}

// A gate's fixer is a fix attempt before the review: the review, and the
// doc pass after it, are still to come, in a story's row and the final
// one.
func TestDetail_AGateFixLeavesTheReviewToCome(t *testing.T) {
	t.Run("a story", func(t *testing.T) {
		repo, base, records, state := boardRepo(t)
		st := runState("r", flowrun.Running,
			flowrun.Story{ID: "1", Developer: "intermediate-dev", Tier: "deep", Review: "vouched", Row: flowrun.Row{Status: "gating"}})
		st.Docs = true
		st.Agents = []string{"r-s1-dev1", "r-s1-fix1"}
		st.Gates = []string{"r-s1-gates1", "r-s1-gates2"}
		for i, name := range st.Agents {
			agentRec(t, state, name, "intermediate-dev", "DONE", sched.JobID(i+1))
			memo(t, st, "agent:"+name, flowrun.Report{Status: "DONE"})
		}
		gateRec(t, state, "r-s1-gates1", "FAIL", 10)
		memo(t, st, "gates:r-s1-gates1", flowrun.GateResult{Ended: "failed"})
		gateRec(t, state, "r-s1-gates2", "PASS", 11)
		memo(t, st, "gates:r-s1-gates2", flowrun.GateResult{Ended: "passed"})
		writeRun(t, state, st)

		d, err := Detail(repo, base, records, planP)
		require.NoError(t, err)
		require.Len(t, d.Stories, 1)
		assert.Equal(t, map[string]string{"dev": CellPassed, "gates": CellPassed, "review": CellTodo, "fix": CellPassed, "docs": CellTodo}, d.Stories[0].Cells)
	})
	t.Run("the final round", func(t *testing.T) {
		repo, base, records, state := boardRepo(t)
		st := runState("r", flowrun.Running,
			flowrun.Story{ID: "1", Developer: "intermediate-dev", Tier: "deep", Review: "vouched", Row: flowrun.Row{Status: "done"}})
		st.Agents = []string{"r-s1-dev1", "r-s1-review1", "r-final-fix1"}
		st.Gates = []string{"r-s1-gates1", "r-final-gates1"}
		for i, name := range st.Agents {
			role := "intermediate-dev"
			if name == "r-s1-review1" {
				role = "story-reviewer"
			}
			agentRec(t, state, name, role, "DONE", sched.JobID(i+1))
			memo(t, st, "agent:"+name, flowrun.Report{Status: "DONE"})
		}
		gateRec(t, state, "r-s1-gates1", "PASS", 10)
		memo(t, st, "gates:r-s1-gates1", flowrun.GateResult{Ended: "passed"})
		gateRec(t, state, "r-final-gates1", "FAIL", 11)
		memo(t, st, "gates:r-final-gates1", flowrun.GateResult{Ended: "failed"})
		writeRun(t, state, st)

		d, err := Detail(repo, base, records, planP)
		require.NoError(t, err)
		require.NotNil(t, d.Final)
		assert.Equal(t, CellTodo, d.Final.Cells["review"], "the anti-seam review hasn't run")
	})
}

func TestDetail_RunningAndFailedCells(t *testing.T) {
	repo, base, records, state := boardRepo(t)
	st := runState("r", flowrun.Running,
		flowrun.Story{ID: "1", Row: flowrun.Row{Status: "developing"}},
		flowrun.Story{ID: "2", Row: flowrun.Row{Status: "gating"}},
		flowrun.Story{ID: "3", Row: flowrun.Row{Status: "done"}})
	st.Agents = []string{"r-s1-dev1", "r-s2-dev1"}
	st.Gates = []string{"r-s2-gates1", "r-final-gates1"}
	agentRec(t, state, "r-s1-dev1", "intermediate-dev", flowrun.AgentRunning, 1)
	agentRec(t, state, "r-s2-dev1", "intermediate-dev", "DONE", 2)
	memo(t, st, "agent:r-s2-dev1", flowrun.Report{Status: "DONE"})
	gateRec(t, state, "r-s2-gates1", "FAIL", 3)
	memo(t, st, "gates:r-s2-gates1", flowrun.GateResult{Ended: "failed"})
	gateRec(t, state, "r-final-gates1", "", 4)
	writeRun(t, state, st)

	d, err := Detail(repo, base, records, planP)
	require.NoError(t, err)
	assert.Equal(t, CellRunning, d.Stories[0].Cells["dev"])
	assert.Equal(t, CellTodo, d.Stories[0].Cells["gates"])
	assert.Equal(t, CellFailed, d.Stories[1].Cells["gates"])
	assert.Equal(t, CellPassed, d.Stories[1].Cells["dev"])
	assert.Equal(t, CellSkipped, d.Stories[2].Cells["dev"], "a kind the story went past without an attempt is skipped, not grey")
	assert.Equal(t, CellSkipped, d.Stories[2].Cells["review"])
	require.NotNil(t, d.Final)
	assert.Equal(t, map[string]string{"gates": CellRunning, "review": CellTodo, "fix": CellTodo}, d.Final.Cells)
	assert.Nil(t, d.Premise)
}

func TestDetail_AResumedAgentRunsWhateverItsEarlierReportSaid(t *testing.T) {
	repo, base, records, state := boardRepo(t)
	st := runState("r", flowrun.Running, flowrun.Story{ID: "1", Row: flowrun.Row{Status: "developing"}})
	st.Agents = []string{"r-s1-dev1"}
	agentRec(t, state, "r-s1-dev1", "intermediate-dev", flowrun.AgentRunning, 1)
	memo(t, st, "agent:r-s1-dev1", flowrun.Report{Status: "TURN_CAP"})
	writeRun(t, state, st)
	d, err := Detail(repo, base, records, planP)
	require.NoError(t, err)
	assert.Equal(t, CellRunning, d.Stories[0].Cells["dev"])
}

func TestDetail_AnUnreadableAgentRecordMarksItsStep(t *testing.T) {
	repo, base, records, state := boardRepo(t)
	st := runState("r", flowrun.Running, flowrun.Story{ID: "1", Row: flowrun.Row{Status: "reviewing"}}, flowrun.Story{ID: "2"})
	st.Agents = []string{"r-s1-dev1", "r-s1-review1"}
	writeFile(t, state, "agents/r-s1-dev1.json", "{")
	agentRec(t, state, "r-s1-review1", "story-reviewer", flowrun.AgentRunning, 2)
	writeRun(t, state, st)
	d, err := Detail(repo, base, records, planP)
	require.NoError(t, err)
	dev := d.Stories[0].Steps[1]
	assert.Equal(t, "r-s1-dev1", dev.Name, "a step with no job goes last")
	assert.Contains(t, dev.Unreadable, "r-s1-dev1.json")
	assert.Equal(t, CellTodo, d.Stories[0].Cells["dev"])
	assert.Equal(t, CellRunning, d.Stories[0].Cells["review"], "the rest of the board is drawn")
}

func TestDetail_BeforeARunTheStoriesAreThePlans(t *testing.T) {
	repo, base, records, _ := boardRepo(t)
	d, err := Detail(repo, base, records, planP)
	require.NoError(t, err)
	require.Len(t, d.Stories, 3)
	assert.Equal(t, "2", d.Stories[1].ID)
	assert.Equal(t, CellTodo, d.Stories[1].Cells["dev"])
	assert.Empty(t, d.Run)
}

func TestDetail_NoSuchFlow(t *testing.T) {
	repo, base, records, _ := boardRepo(t)
	_, err := Detail(repo, base, records, "docs/other.md")
	assert.ErrorContains(t, err, "no flow docs/other.md")
}
