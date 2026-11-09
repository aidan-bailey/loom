package flowview

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	planP = "docs/superpowers/plans/2026-11-09-p.md"
	specP = "docs/superpowers/specs/2026-11-09-p-design.md"
)

// branchRepo is a repo whose main holds one commit, with a branch feat
// checked out at it; base is that commit.
func branchRepo(t *testing.T) (repo, base, records string) {
	t.Helper()
	repo = gitRepo(t)
	base = head(t, repo)
	git(t, repo, "checkout", "-q", "-b", "feat")
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte(".flow/\n"), 0o644))
	return repo, base, t.TempDir()
}

func runState(id string, status string, stories ...flowrun.Story) *flowrun.State {
	return &flowrun.State{Schema: flowrun.Schema, ID: id, Status: status, Plan: planP, Spec: specP, Base: "b0",
		Start: "2026-11-09T10:00:00Z", Stories: stories, Memo: map[string]json.RawMessage{}}
}

func TestFind_TheBranchsOwnPlansOnly(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "docs/superpowers/plans/2026-01-01-old.md", skeleton("docs/superpowers/specs/old-design.md", "1"))
	base := head(t, repo)
	git(t, repo, "checkout", "-q", "-b", "feat")
	commitFile(t, repo, specP, "# P design\n")
	commitFile(t, repo, planP, skeleton(specP, "1", "2"))
	git(t, repo, "checkout", "-q", "main")
	commitFile(t, repo, "docs/superpowers/plans/2026-11-08-other.md", skeleton("docs/superpowers/specs/other-design.md", "1"))
	commitFile(t, repo, "docs/superpowers/specs/2026-11-08-other-design.md", "# Other\n")
	git(t, repo, "checkout", "-q", "feat")
	git(t, repo, "merge", "-q", "--no-edit", "main")

	got, err := Find(repo, base, t.TempDir())
	require.NoError(t, err)
	require.Len(t, got, 1, "%+v", got)
	s := got[0]
	assert.Equal(t, planP, s.Plan)
	assert.Equal(t, specP, s.Spec)
	assert.Equal(t, "P design", s.Title)
	assert.Equal(t, StageSpec, s.Stage)
	assert.Equal(t, StatusDesigned, s.Status)
	assert.Equal(t, 2, s.Total)
	assert.Empty(t, s.Unreadable)
}

func TestFind_ASpecWithNoPlanIsAFlowInDesign(t *testing.T) {
	repo, base, records := branchRepo(t)
	writeFile(t, repo, specP, "# P design\n\nbeing written\n")
	got, err := Find(repo, base, records)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, Summary{Plan: specP, Spec: specP, Title: "P design", Stage: StageDesign, Status: StatusDesigning, Updated: got[0].Updated}, got[0])

	// A plan that names the spec without being a flow's (an inline plan)
	// ends the flow in design: the design went on without flow.
	writeFile(t, repo, "docs/superpowers/plans/2026-11-09-p.md", "# P plan\n\nSpec: `"+specP+"`\n")
	got, err = Find(repo, base, records)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFind_ASpecWhosePlanWasCommittedBeforeTheBaseIsNoFlow(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "docs/superpowers/plans/2026-11-09-p.md", "# P plan\n\nSpec: `"+specP+"`\n")
	base := head(t, repo)
	git(t, repo, "checkout", "-q", "-b", "feat")
	commitFile(t, repo, specP, "# P design\n")
	got, err := Find(repo, base, t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, got, "the plan that names the spec sits before the base, outside the branch's files")
}

func TestFind_ARunStateNamesItsPlanEvenOutsideTheDiff(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, specP, "# P design\n")
	commitFile(t, repo, planP, joined(specP, "", "1", "2"))
	base := head(t, repo)
	st := runState("r", flowrun.Running, flowrun.Story{ID: "1", Row: flowrun.Row{Status: "done"}}, flowrun.Story{ID: "2", Row: flowrun.Row{Status: "developing"}})
	st.Engine, st.Phase = 41, "story 2"
	writeRun(t, flowStateDir(t, repo), st)

	got, err := Find(repo, base, t.TempDir())
	require.NoError(t, err)
	require.Len(t, got, 1)
	s := got[0]
	assert.Equal(t, planP, s.Plan)
	assert.Equal(t, StageRun, s.Stage)
	assert.Equal(t, flowrun.Running, s.Status)
	assert.Equal(t, "r", s.Run)
	assert.Equal(t, int64(41), s.Engine)
	assert.Equal(t, 1, s.Done)
	assert.Equal(t, 2, s.Total)
	assert.Equal(t, "story 2", s.Phase)
}

// stageStates is a summary's flow's stage states, by name, from Detail.
func stageStates(t *testing.T, repo, base, records, plan string) map[string]Stage {
	t.Helper()
	d, err := Detail(repo, base, records, plan)
	require.NoError(t, err)
	out := map[string]Stage{}
	for _, s := range d.Stages {
		out[s.Name] = s
	}
	return out
}

func TestFind_TheStages(t *testing.T) {
	stem := strings.TrimSuffix(filepath.Base(planP), ".md")
	t.Run("design in progress", func(t *testing.T) {
		repo, base, records := branchRepo(t)
		writeFile(t, repo, specP, "# P design\n")
		writeFile(t, repo, planP, skeleton(specP, "1"))
		got, err := Find(repo, base, records)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, StatusDesigning, got[0].Status)
		st := stageStates(t, repo, base, records, planP)
		assert.Equal(t, StateNow, st[StageDesign].State)
		assert.Equal(t, DocSpec, st[StageDesign].Doc)
		assert.Equal(t, StateDone, st[StageSpec].State)
		assert.Equal(t, DocSkeleton, st[StageSpec].Doc)
	})
	t.Run("drafting", func(t *testing.T) {
		repo, base, records := branchRepo(t)
		commitFile(t, repo, specP, "# P design\n")
		commitFile(t, repo, planP, skeleton(specP, "1", "2"))
		writeFile(t, repo, ".flow/drafts/"+stem+"/S1.md", "## Story 1\n")
		writeFile(t, repo, ".flow/drafts/"+stem+"/STATE.md", "# State\n\n**Phase:** drafting batch 2\n")
		got, err := Find(repo, base, records)
		require.NoError(t, err)
		require.Len(t, got, 1)
		s := got[0]
		assert.Equal(t, StageDraft, s.Stage)
		assert.Equal(t, StatusDrafting, s.Status)
		assert.Equal(t, 1, s.Drafted)
		assert.Equal(t, 2, s.Total)
		assert.Equal(t, "drafting batch 2", s.Phase)
		st := stageStates(t, repo, base, records, planP)
		assert.Equal(t, StateDone, st[StageDesign].State)
		assert.Equal(t, Stage{Name: StageDraft, State: StateNow, Detail: "1 of 2 drafted · drafting batch 2", Doc: DocDraftsState}, st[StageDraft])
	})
	t.Run("drafted by /flow:draft", func(t *testing.T) {
		repo, base, records := branchRepo(t)
		commitFile(t, repo, specP, "# P design\n")
		commitFile(t, repo, planP, joined(specP, "/flow:draft session s1, … : $12.05 logged, $12.16 with estimated thinking; 500 plan lines.", "1"))
		got, err := Find(repo, base, records)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, StatusDrafted, got[0].Status)
		st := stageStates(t, repo, base, records, planP)
		assert.Equal(t, Stage{Name: StageDraft, State: StateDone, Detail: "by /flow:draft", Doc: DocPlan}, st[StageDraft])
	})
	t.Run("written by hand", func(t *testing.T) {
		repo, base, records := branchRepo(t)
		commitFile(t, repo, specP, "# P design\n")
		commitFile(t, repo, planP, strings.Replace(joined(specP, "", "1"), joinMarker+"\n", "", 1))
		st := stageStates(t, repo, base, records, planP)
		assert.Equal(t, Stage{Name: StageDraft, State: StateDone, Detail: "written by hand", Doc: DocPlan}, st[StageDraft])
		assert.Equal(t, DocPlan, st[StageSpec].Doc, "a plan with no marker has no skeleton apart from it")
	})
	t.Run("run parked", func(t *testing.T) {
		repo, base, records := branchRepo(t)
		commitFile(t, repo, specP, "# P design\n")
		commitFile(t, repo, planP, joined(specP, "", "1"))
		st := runState("r", flowrun.Parked, flowrun.Story{ID: "1"})
		st.Question = &flowrun.Question{Key: "blocked:r-s1-dev1", Kind: flowrun.KindBlocked, Story: "1", Text: "BLOCKED"}
		writeRun(t, flowStateDir(t, repo), st)
		got, err := Find(repo, base, records)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, flowrun.Parked, got[0].Status)
		assert.Equal(t, flowrun.KindBlocked, got[0].Asks)
		assert.Equal(t, StateNow, stageStates(t, repo, base, records, planP)[StageRun].State)
	})
	t.Run("run done", func(t *testing.T) {
		repo, base, records := branchRepo(t)
		commitFile(t, repo, specP, "# P design\n")
		commitFile(t, repo, planP, joined(specP, "", "1"))
		writeRun(t, flowStateDir(t, repo), runState("r", flowrun.Done, flowrun.Story{ID: "1", Row: flowrun.Row{Status: "done"}}))
		assert.Equal(t, Stage{Name: StageRun, State: StateDone, Detail: flowrun.Done}, stageStates(t, repo, base, records, planP)[StageRun])
	})
	t.Run("supervised, then recorded", func(t *testing.T) {
		repo, base, records := branchRepo(t)
		commitFile(t, repo, specP, "# P design\n")
		commitFile(t, repo, planP, joined(specP, "", "1", "2"))
		writeFile(t, flowStateDir(t, repo), "agent-reports/supervisor-"+stem+".md", "# Supervisor\n\n**Phase:** story 2 reviewing\n")
		got, err := Find(repo, base, records)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, StageRun, got[0].Stage)
		assert.Equal(t, StatusSupervised, got[0].Status)
		assert.Equal(t, "story 2 reviewing", got[0].Phase)

		key, err := RepoKey(repo)
		require.NoError(t, err)
		writeFile(t, records, key+"/2026-11-10-p.md", "# Run: p\n")
		got, err = Find(repo, base, records)
		require.NoError(t, err)
		assert.Equal(t, StageRecord, got[0].Stage)
		assert.Equal(t, flowrun.Done, got[0].Status)
		st := stageStates(t, repo, base, records, planP)
		assert.Equal(t, StateDone, st[StageRun].State)
		assert.Equal(t, Stage{Name: StageRecord, State: StateDone, Detail: "2026-11-10-p.md", Doc: DocRecord}, st[StageRecord])
	})
	t.Run("a missing spec", func(t *testing.T) {
		repo, base, records := branchRepo(t)
		commitFile(t, repo, planP, skeleton(specP, "1"))
		st := stageStates(t, repo, base, records, planP)
		assert.Equal(t, StateUnreadable, st[StageSpec].State)
		assert.Contains(t, st[StageSpec].Detail, "does not exist")
		assert.Equal(t, StateNow, st[StageDesign].State, "the design isn't done while its spec isn't committed")
	})
}

func TestFind_AnUnreadableRunStateSaysWhy(t *testing.T) {
	repo, base, records := branchRepo(t)
	commitFile(t, repo, specP, "# P design\n")
	commitFile(t, repo, planP, joined(specP, "", "1"))
	state := flowStateDir(t, repo)
	writeFile(t, state, "runs/r.json", `{"schema": 2, "plan": "`+planP+`"}`)
	writeFile(t, state, "runs/torn.json", `{`)
	got, err := Find(repo, base, records)
	require.NoError(t, err)
	require.Len(t, got, 2)
	byPlan := map[string]Summary{}
	for _, s := range got {
		byPlan[s.Plan] = s
	}
	assert.Contains(t, byPlan[planP].Unreadable, "schema 2")
	assert.Equal(t, StageRun, byPlan[planP].Stage)
	torn := byPlan[filepath.Join(".git", "flow", "runs", "torn.json")]
	assert.NotEmpty(t, torn.Unreadable, "a run state that names no plan is listed, never dropped")
}

func TestFind_TwoRunsOfOnePlanShowTheNewest(t *testing.T) {
	repo, base, records := branchRepo(t)
	commitFile(t, repo, specP, "# P design\n")
	commitFile(t, repo, planP, joined(specP, "", "1"))
	old := runState("r1", flowrun.Stopped, flowrun.Story{ID: "1"})
	old.Start = "2026-11-01T10:00:00Z"
	writeRun(t, flowStateDir(t, repo), old)
	writeRun(t, flowStateDir(t, repo), runState("r2", flowrun.Running, flowrun.Story{ID: "1"}))
	got, err := Find(repo, base, records)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "r2", got[0].Run)
	d, err := Detail(repo, base, records, planP)
	require.NoError(t, err)
	assert.Equal(t, []OtherRun{{ID: "r1", Status: flowrun.Stopped, Start: "2026-11-01T10:00:00Z"}}, d.Others)
}

func TestFind_LeavesTheWorktreeAsItWas(t *testing.T) {
	repo, base, records := branchRepo(t)
	commitFile(t, repo, specP, "# P design\n")
	commitFile(t, repo, planP, joined(specP, "", "1"))
	stem := strings.TrimSuffix(filepath.Base(planP), ".md")
	writeFile(t, repo, ".flow/drafts/"+stem+"/STATE.md", "**Phase:** x\n")
	writeRun(t, flowStateDir(t, repo), runState("r", flowrun.Running, flowrun.Story{ID: "1"}))
	snapshot := func() (string, map[string]time.Time) {
		files := map[string]time.Time{}
		require.NoError(t, filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if info, err := d.Info(); err == nil {
				files[p] = info.ModTime()
			}
			return nil
		}))
		return git(t, repo, "--no-optional-locks", "status", "--porcelain", "--ignored"), files
	}
	status, files := snapshot()
	_, err := Find(repo, base, records)
	require.NoError(t, err)
	_, err = Detail(repo, base, records, planP)
	require.NoError(t, err)
	status2, files2 := snapshot()
	assert.Equal(t, status, status2)
	assert.Equal(t, files, files2)
}
