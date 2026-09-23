package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commitFile writes name into dir and commits it, so each helper call
// produces a distinct SHA the assertions can tell apart.
func commitFile(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0644))
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "add "+name)
}

func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	require.NoError(t, err, "rev-parse %s", ref)
	return strings.TrimSpace(string(out))
}

// newRepo creates a repo whose initial branch is defaultBranch, with one commit.
func newRepo(t *testing.T, defaultBranch string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(dir, 0755))
	runGit(t, dir, "init", "-b", defaultBranch)
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	commitFile(t, dir, "README.md", "hi")
	return dir
}

// newClone builds a source repo with defaultBranch plus extraBranches, pushes
// it through a bare "origin", and returns a clone — which is the only way to
// get realistic refs/remotes/origin/HEAD and origin-only branches.
func newClone(t *testing.T, defaultBranch string, extraBranches ...string) string {
	t.Helper()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	require.NoError(t, os.MkdirAll(src, 0755))
	runGit(t, src, "init", "-b", defaultBranch)
	runGit(t, src, "config", "user.email", "test@example.com")
	runGit(t, src, "config", "user.name", "Test")
	commitFile(t, src, "README.md", "hi")
	for _, b := range extraBranches {
		runGit(t, src, "checkout", "-b", b)
		commitFile(t, src, b+".txt", b)
		runGit(t, src, "checkout", defaultBranch)
	}
	runGit(t, tmp, "clone", "--bare", src, "origin.git")
	runGit(t, tmp, "clone", "origin.git", "clone")
	clone := filepath.Join(tmp, "clone")
	// A clone doesn't inherit src's local config, and CI has no global
	// identity, so tests that commit in the clone need their own.
	runGit(t, clone, "config", "user.email", "test@example.com")
	runGit(t, clone, "config", "user.name", "Test")
	return clone
}

func TestResolveBaseCommit_ConfiguredLocalBranch(t *testing.T) {
	repo := newRepo(t, "main")
	runGit(t, repo, "checkout", "-b", "develop")
	commitFile(t, repo, "dev.txt", "dev")
	runGit(t, repo, "checkout", "main")

	sha, name, err := ResolveBaseCommit(repo, "develop", nil)
	require.NoError(t, err)
	assert.Equal(t, "develop", name)
	assert.Equal(t, revParse(t, repo, "refs/heads/develop"), sha)
}

func TestResolveBaseCommit_ConfiguredFallsBackToOriginRef(t *testing.T) {
	// "release" exists only as a remote-tracking ref in the clone.
	clone := newClone(t, "main", "release")
	require.False(t, localBranchExists(t, clone, "release"), "fixture invalid: release should not be local")

	sha, name, err := ResolveBaseCommit(clone, "release", nil)
	require.NoError(t, err)
	assert.Equal(t, "origin/release", name)
	assert.Equal(t, revParse(t, clone, "refs/remotes/origin/release"), sha)
}

func TestResolveBaseCommit_ConfiguredMissingIsAnError(t *testing.T) {
	repo := newRepo(t, "main")

	_, _, err := ResolveBaseCommit(repo, "nope", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

func TestResolveBaseCommit_AutoDetectsOriginHEAD(t *testing.T) {
	// Clone's origin/HEAD points at trunk; neither main nor master exists,
	// so only origin/HEAD detection can find it.
	clone := newClone(t, "trunk")
	runGit(t, clone, "checkout", "-b", "feature/noise")
	commitFile(t, clone, "noise.txt", "noise")

	sha, name, err := ResolveBaseCommit(clone, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "trunk", name)
	assert.Equal(t, revParse(t, clone, "refs/heads/trunk"), sha)
}

// TestResolveBaseCommit_AutoIgnoresCheckedOutBranch is the regression this
// whole feature exists for: with a feature branch checked out in the root
// repo, the base must still be main — not HEAD.
func TestResolveBaseCommit_AutoIgnoresCheckedOutBranch(t *testing.T) {
	repo := newRepo(t, "main")
	runGit(t, repo, "checkout", "-b", "feature/noise")
	commitFile(t, repo, "noise.txt", "noise")

	sha, name, err := ResolveBaseCommit(repo, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "main", name)
	assert.Equal(t, revParse(t, repo, "refs/heads/main"), sha)
	assert.NotEqual(t, revParse(t, repo, "HEAD"), sha, "must not fall through to HEAD")
}

func TestResolveBaseCommit_AutoFallsBackToMaster(t *testing.T) {
	repo := newRepo(t, "master")
	runGit(t, repo, "checkout", "-b", "feature/noise")
	commitFile(t, repo, "noise.txt", "noise")

	sha, name, err := ResolveBaseCommit(repo, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "master", name)
	assert.Equal(t, revParse(t, repo, "refs/heads/master"), sha)
}

// TestResolveBaseCommit_AutoFallsBackToHEAD preserves today's behaviour for
// repos with no remote and no main/master.
func TestResolveBaseCommit_AutoFallsBackToHEAD(t *testing.T) {
	repo := newRepo(t, "trunk")

	sha, name, err := ResolveBaseCommit(repo, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "HEAD", name)
	assert.Equal(t, revParse(t, repo, "HEAD"), sha)
}

func TestResolveBaseCommit_EmptyRepoReportsNoCommits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(dir, 0755))
	runGit(t, dir, "init", "-b", "main")

	_, _, err := ResolveBaseCommit(dir, "", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoCommits)
	assert.Contains(t, err.Error(), "initial commit")
}

func localBranchExists(t *testing.T, dir, branch string) bool {
	t.Helper()
	return exec.Command("git", "-C", dir, "show-ref", "--verify", "refs/heads/"+branch).Run() == nil
}

// writeConfig drops a partial config.json into configDir. LoadConfigFrom
// unmarshals over DefaultConfig, so only the fields under test need setting.
func writeConfig(t *testing.T, configDir, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(configDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, config.ConfigFileName), []byte(body), 0644))
}

// TestSetup_CutsFromDefaultBranchNotHEAD is the end-to-end version of the
// regression: with a feature branch checked out in the root repo, a new
// session's worktree must still start from main, and its diff baseline must
// be main's commit rather than the feature branch's.
func TestSetup_CutsFromDefaultBranchNotHEAD(t *testing.T) {
	repo := newRepo(t, "main")
	mainSHA := revParse(t, repo, "refs/heads/main")
	runGit(t, repo, "checkout", "-b", "feature/noise")
	commitFile(t, repo, "noise.txt", "noise")
	noiseSHA := revParse(t, repo, "HEAD")
	require.NotEqual(t, mainSHA, noiseSHA)

	configDir := filepath.Join(t.TempDir(), "config")
	tree, _, err := NewGitWorktree(repo, "base test", configDir)
	require.NoError(t, err)
	require.NoError(t, tree.Setup())
	t.Cleanup(func() { _ = tree.Cleanup() })

	assert.Equal(t, mainSHA, tree.GetBaseCommitSHA(), "diff baseline must be the default branch")
	assert.Equal(t, mainSHA, revParse(t, tree.GetWorktreePath(), "HEAD"), "worktree must start at the default branch")
}

func TestSetup_CutsFromConfiguredBaseBranch(t *testing.T) {
	repo := newRepo(t, "main")
	runGit(t, repo, "checkout", "-b", "develop")
	commitFile(t, repo, "dev.txt", "dev")
	developSHA := revParse(t, repo, "refs/heads/develop")
	runGit(t, repo, "checkout", "main")

	configDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, configDir, `{"base_branch":"develop"}`)

	tree, _, err := NewGitWorktree(repo, "configured base", configDir)
	require.NoError(t, err)
	require.NoError(t, tree.Setup())
	t.Cleanup(func() { _ = tree.Cleanup() })

	assert.Equal(t, developSHA, tree.GetBaseCommitSHA())
	assert.Equal(t, developSHA, revParse(t, tree.GetWorktreePath(), "HEAD"))
}

func TestSetup_ConfiguredBaseBranchMissingFails(t *testing.T) {
	repo := newRepo(t, "main")
	configDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, configDir, `{"base_branch":"no-such-branch"}`)

	tree, _, err := NewGitWorktree(repo, "missing base", configDir)
	require.NoError(t, err)
	err = tree.Setup()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-branch")
}
