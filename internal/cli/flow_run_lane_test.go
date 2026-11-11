package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keyedTools is fakeRunTools with a key of its own, so each test's lanes
// sit apart under the shared test global dir.
type keyedTools struct {
	fakeRunTools
	key string
}

func (k keyedTools) Key() (string, error) { return k.key, nil }

// laneRepo is a repository with one commit, and a runFX on it.
func laneRepo(t *testing.T) (string, *runFX) {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	for _, f := range []string{"plan.md", "spec.md", "a.go", "b.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(repo, f), []byte("v1\n"), 0o600))
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	key := "-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	return repo, &runFX{cwd: repo, tools: keyedTools{key: key}, plan: "plan.md", spec: "spec.md"}
}

// commitIn writes body to file in dir and commits it, and is the commit.
func commitIn(t *testing.T, dir, file, body string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600))
	gitIn(t, dir, "commit", "-q", "-am", "edit "+file)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// runGit runs git args in dir, failing or not, as a fixture's own git.
func runGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
	return string(out), err
}

func TestRunFX_LaneMakesAWorktreeAtItsBaseAndReusesIt(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	commitIn(t, repo, "a.go", "v2\n")
	dir, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	assert.Equal(t, base, gitIn(t, dir, "rev-parse", "HEAD"))
	assert.NotContains(t, dir, repo, "a lane lives outside the repository")
	again, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	assert.Equal(t, dir, again)

	other := filepath.Join(filepath.Dir(dir), "r-s3")
	require.NoError(t, os.MkdirAll(other, 0o700))
	_, err = fx.Lane("r-s3", base)
	assert.ErrorContains(t, err, "is no worktree of this repository")
}

func TestRunFX_PickPicksInOrderAndTakesAnEarlierPartialPick(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	dir, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	l1 := commitIn(t, dir, "b.go", "lane 1\n")
	l2 := commitIn(t, dir, "b.go", "lane 2\n")
	onto := commitIn(t, repo, "a.go", "main\n")

	gitIn(t, repo, "cherry-pick", l1) // a crash after the first pick
	picked, err := fx.Pick([]string{l1, l2}, onto)
	require.NoError(t, err)
	require.Len(t, picked, 2)
	assert.Equal(t, picked[1], gitIn(t, repo, "rev-parse", "HEAD"))
	assert.Equal(t, "lane 2", gitIn(t, repo, "show", "HEAD:b.go"))

	again, err := fx.Pick([]string{l1, l2}, onto)
	require.NoError(t, err)
	assert.Equal(t, picked, again, "a pick asked again picks nothing twice")
}

func TestRunFX_APickThatConflictsAbortsBackToOnto(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	dir, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	l1 := commitIn(t, dir, "b.go", "lane\n")
	l2 := commitIn(t, dir, "a.go", "lane\n")
	onto := commitIn(t, repo, "a.go", "main\n")
	_, err = fx.Pick([]string{l1, l2}, onto)
	var conflict *flowrun.PickConflict
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, l2, conflict.Commit)
	assert.Equal(t, []string{"a.go"}, conflict.Files)
	assert.Equal(t, onto, gitIn(t, repo, "rev-parse", "HEAD"), "the whole pick is undone")
	assert.False(t, fx.picking())
}

func TestRunFX_PickAbortsAPickACrashLeftInProgress(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	dir, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	clash := commitIn(t, dir, "a.go", "lane\n")
	gitIn(t, dir, "reset", "-q", "--hard", base) // the clash stays an object, off the lane
	l1 := commitIn(t, dir, "b.go", "lane\n")
	onto := commitIn(t, repo, "a.go", "main\n")
	_, _ = runGit(repo, "cherry-pick", clash) // conflicts, and stays in progress
	require.True(t, fx.picking())
	picked, err := fx.Pick([]string{l1}, onto)
	require.NoError(t, err)
	require.Len(t, picked, 1)
	assert.False(t, fx.picking())
}

func TestRunFX_DropLaneRefusesADirtyLane(t *testing.T) {
	repo, fx := laneRepo(t)
	dir, err := fx.Lane("r-s2", gitIn(t, repo, "rev-parse", "HEAD"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o600))
	assert.Error(t, fx.DropLane(dir))
	assert.DirExists(t, dir)
	require.NoError(t, os.Remove(filepath.Join(dir, "stray.txt")))
	require.NoError(t, fx.DropLane(dir))
	assert.NoDirExists(t, dir)
}

func TestRunFX_OnlyPlanCommits(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	commitIn(t, repo, "spec.md", "v2\n")
	head := commitIn(t, repo, "plan.md", "v2\n")
	ok, err := fx.OnlyPlanCommits(base, head)
	require.NoError(t, err)
	assert.True(t, ok)
	code := commitIn(t, repo, "a.go", "v2\n")
	ok, err = fx.OnlyPlanCommits(base, code)
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = fx.OnlyPlanCommits(code, base)
	require.NoError(t, err)
	assert.False(t, ok, "a head that doesn't descend from base")
}
