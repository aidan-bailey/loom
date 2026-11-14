package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusAge_StampsOnTransition(t *testing.T) {
	inst := &Instance{Title: "age-test", Status: Ready}
	assert.Equal(t, time.Duration(0), inst.StatusAge(), "zero before any transition")

	require.NoError(t, inst.TransitionTo(Loading))
	age := inst.StatusAge()
	assert.Greater(t, age, time.Duration(0))
	assert.Less(t, age, time.Second)

	// Self-transition must not restamp.
	time.Sleep(10 * time.Millisecond)
	before := inst.StatusAge()
	require.NoError(t, inst.TransitionTo(Loading))
	assert.GreaterOrEqual(t, inst.StatusAge(), before)
}

func TestInstance_ATurnStartsOnRunningButNotAfterAPrompt(t *testing.T) {
	inst := &Instance{Title: "turn", Status: Paused}
	assert.True(t, inst.TurnStart().IsZero(), "no turn seen in this process")

	require.NoError(t, inst.TransitionTo(Running))
	first := inst.TurnStart()
	require.False(t, first.IsZero())

	require.NoError(t, inst.TransitionTo(Prompting))
	require.NoError(t, inst.TransitionTo(Running))
	assert.Equal(t, first, inst.TurnStart(), "a prompt inside a turn doesn't split it")

	require.NoError(t, inst.TransitionTo(Ready))
	time.Sleep(time.Millisecond)
	require.NoError(t, inst.TransitionTo(Running))
	assert.True(t, inst.TurnStart().After(first), "a reply starts a new turn")
}

func TestInstance_KeepsTheMockupItIsGiven(t *testing.T) {
	inst := &Instance{Title: "mockup", Status: Running}
	at := time.Now()
	inst.SetMockup("layout.html", at)
	screen, changed := inst.Mockup()
	assert.Equal(t, "layout.html", screen)
	assert.Equal(t, at, changed)
	inst.SetMockup("", time.Time{})
	screen, _ = inst.Mockup()
	assert.Empty(t, screen)
}

func TestNewInstance_KeepsBaseRefAndPersistsNothingOfIt(t *testing.T) {
	sha := strings.Repeat("a", 40)

	inst, err := NewInstance(InstanceOptions{Title: "base-ref", Path: t.TempDir(), Program: "claude", BaseRef: sha})
	require.NoError(t, err)

	assert.Equal(t, sha, inst.BaseRef())
	data, err := json.Marshal(inst.ToInstanceData())
	require.NoError(t, err)
	assert.NotContains(t, string(data), sha, "BaseRef is consumed by the first start and never saved")
}

func TestNewInstance_BaseRefAndBranchAreExclusive(t *testing.T) {
	_, err := NewInstance(InstanceOptions{Title: "both", Path: t.TempDir(), Program: "claude", Branch: "feature", BaseRef: strings.Repeat("a", 40)})
	require.Error(t, err)
	assert.ErrorContains(t, err, "BaseRef")

	inst, err := NewInstance(InstanceOptions{Title: "branch-only", Path: t.TempDir(), Program: "claude", Branch: "feature"})
	require.NoError(t, err, "an existing branch alone is as before")
	assert.Empty(t, inst.BaseRef())
}

// TestStart_CutsTheBranchFromBaseRef: the first Start hands BaseRef to the
// worktree, so the session starts at the commit named while the repository's
// own main is ahead of it, and the branch loom created goes with the kill.
func TestStart_CutsTheBranchFromBaseRef(t *testing.T) {
	_, repoDir, branch := newStartFixture(t, "from-base")
	configDir := filepath.Join(filepath.Dir(repoDir), "config")
	first := gitIn(t, repoDir, "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "ahead.txt"), []byte("unpushed"), 0o644))
	gitIn(t, repoDir, "add", ".")
	gitIn(t, repoDir, "commit", "-q", "-m", "ahead")
	require.NotEqual(t, first, gitIn(t, repoDir, "rev-parse", "HEAD"), "precondition: main is ahead")

	inst, err := NewInstance(InstanceOptions{Title: "from-base", Path: repoDir, Program: "claude", ConfigDir: configDir, BaseRef: first})
	require.NoError(t, err)
	inst.SetBranchPrefix("loom-test/")
	srv := &fakeTmuxServer{}
	inst.setTmuxSession(tmux.NewSessionWithDeps("from-base", "claude", srv, srv.runner()))

	require.NoError(t, inst.Start(true))

	wt := inst.getGitWorktree().GetWorktreePath()
	assert.Equal(t, first, branchTip(t, repoDir, branch), "the branch was cut from BaseRef, not from main's tip")
	assert.Equal(t, first, gitIn(t, wt, "rev-parse", "HEAD"))
	assert.False(t, inst.ToInstanceData().Worktree.IsExistingBranch, "loom created the branch")
	require.NoError(t, inst.Kill())
	assert.Empty(t, branchTip(t, repoDir, branch), "so the kill deletes it")
}
