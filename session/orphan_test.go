package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	internalexec "github.com/aidan-bailey/loom/internal/exec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanizeBranchLeaf(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"aidanb/example-jupyter-notebook", "example jupyter notebook"},
		{"feature/add-tests", "add tests"},
		{"main", "main"},
		{"single-word", "single word"},
		// Multiple slashes — only the leaf matters.
		{"team/sub/feat-x", "feat x"},
		// No dashes — passes through.
		{"plain", "plain"},
		// Empty leaf (trailing slash) yields empty string. The orphan
		// overlay would render this as a blank title; acceptable
		// because the user still sees the branch name and worktree
		// path on the same row.
		{"foo/", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			assert.Equal(t, c.want, HumanizeBranchLeaf(c.in))
		})
	}
}

func TestStripTimestampSuffix(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantLeaf  string
		wantOK    bool
		wantStrip bool
	}{
		{
			name:     "standard hex suffix",
			in:       "example-jupyter-notebook_18acb35cb8ad6e5a",
			wantLeaf: "example-jupyter-notebook",
			wantOK:   true,
		},
		{
			name:     "branch contains underscore plus hex suffix",
			in:       "feature_x_18acb35cb8ad6e5a",
			wantLeaf: "feature_x",
			wantOK:   true,
		},
		{
			name:     "no underscore",
			in:       "main",
			wantLeaf: "main",
			wantOK:   false,
		},
		{
			name:     "underscore but trailing token isn't hex",
			in:       "feature_branch",
			wantLeaf: "feature_branch",
			wantOK:   false,
		},
		{
			name:     "leading underscore (treated as no separator)",
			in:       "_18acb35cb8ad6e5a",
			wantLeaf: "_18acb35cb8ad6e5a",
			wantOK:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotLeaf, gotOK := stripTimestampSuffix(c.in)
			assert.Equal(t, c.wantLeaf, gotLeaf)
			assert.Equal(t, c.wantOK, gotOK)
		})
	}
}

func TestIsHexString(t *testing.T) {
	assert.True(t, isHexString("18acb35cb8ad6e5a"))
	assert.True(t, isHexString("DEADBEEF"))
	assert.True(t, isHexString("0"))
	assert.False(t, isHexString(""))
	assert.False(t, isHexString("xyz"))
	assert.False(t, isHexString("18acg"))
	assert.False(t, isHexString("123-456"))
}

// withStubProbe swaps the package-level probeWorktreeRepo for a test
// fake that returns deterministic repo/SHA values, avoiding the need
// to set up real `git init` + `git worktree` fixtures in every test.
// Returns a cleanup func that restores the original probe.
func withStubProbe(t *testing.T) {
	t.Helper()
	prev := probeWorktreeRepo
	probeWorktreeRepo = func(wt string) (string, string, error) {
		return "/fake/repo", "deadbeef00000000", nil
	}
	t.Cleanup(func() { probeWorktreeRepo = prev })
}

// TestDiscoverOrphans_FiltersClaimed verifies the core contract: paths
// passed via claimedPaths are excluded from the result, while
// everything else under <configDir>/worktrees/ is returned with
// metadata populated.
func TestDiscoverOrphans_FiltersClaimed(t *testing.T) {
	withStubProbe(t)
	cfgDir := t.TempDir()
	worktreesDir := filepath.Join(cfgDir, "worktrees", "aidanb")
	require.NoError(t, os.MkdirAll(worktreesDir, 0o755))

	orphanPath := filepath.Join(worktreesDir, "example-jupyter-notebook_18acb35cb8ad6e5a")
	claimedPath := filepath.Join(worktreesDir, "readibility-analysis_18acb32c71e51ff4")
	require.NoError(t, os.Mkdir(orphanPath, 0o755))
	require.NoError(t, os.Mkdir(claimedPath, 0o755))

	claimed := map[string]bool{claimedPath: true}

	got, err := DiscoverOrphans(cfgDir, claimed, internalexec.Default{})
	require.NoError(t, err)
	require.Len(t, got, 1, "only the unclaimed worktree should be returned")

	cand := got[0]
	assert.Equal(t, orphanPath, cand.WorktreePath)
	assert.Equal(t, "aidanb/example-jupyter-notebook", cand.BranchName)
	assert.Equal(t, "example jupyter notebook", cand.Title)
}

// TestDiscoverOrphans_NoWorktreesDir is the happy path for a fresh
// configDir: returns an empty slice, no error. The app calls this on
// every startup including first launch, so absent-dir must be benign.
func TestDiscoverOrphans_NoWorktreesDir(t *testing.T) {
	got, err := DiscoverOrphans(t.TempDir(), nil, internalexec.Default{})
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestDiscoverOrphans_MultipleUsers covers the per-user grouping
// (worktrees/<user>/<branch>_<hex>) — branches from different users
// are independent and all unclaimed should surface.
func TestDiscoverOrphans_MultipleUsers(t *testing.T) {
	withStubProbe(t)
	cfgDir := t.TempDir()
	wt := filepath.Join(cfgDir, "worktrees")

	require.NoError(t, os.MkdirAll(filepath.Join(wt, "alice"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(wt, "bob"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(wt, "alice", "feat-a_dead0001"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(wt, "bob", "feat-b_dead0002"), 0o755))

	got, err := DiscoverOrphans(cfgDir, nil, internalexec.Default{})
	require.NoError(t, err)
	require.Len(t, got, 2)

	branches := []string{got[0].BranchName, got[1].BranchName}
	sort.Strings(branches)
	assert.Equal(t, []string{"alice/feat-a", "bob/feat-b"}, branches)
}

// TestDiscoverOrphans_SkipsNonDirectoryEntries makes sure stray files
// next to worktree subdirs (e.g. a .DS_Store, a stray log file)
// don't get classified as orphans.
func TestDiscoverOrphans_SkipsNonDirectoryEntries(t *testing.T) {
	withStubProbe(t)
	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "aidanb")
	require.NoError(t, os.MkdirAll(userDir, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(userDir, "valid_dead0001"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(userDir, ".DS_Store"), []byte("junk"), 0o644))

	got, err := DiscoverOrphans(cfgDir, nil, internalexec.Default{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "aidanb/valid", got[0].BranchName)
}

// TestDiscoverOrphans_NonHexSuffixDirRetainsRawName covers the
// fallback in stripTimestampSuffix: directories that don't match the
// loom timestamp convention still come back as orphans (with the raw
// dir name as branch) so the user can choose to skip them.
func TestDiscoverOrphans_NonHexSuffixDirRetainsRawName(t *testing.T) {
	withStubProbe(t)
	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "aidanb")
	require.NoError(t, os.MkdirAll(userDir, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(userDir, "not-a-loom-dir"), 0o755))

	got, err := DiscoverOrphans(cfgDir, nil, internalexec.Default{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "aidanb/not-a-loom-dir", got[0].BranchName)
}

// TestDiscoverOrphans_DropsUnrecoverable verifies the M6 fix: when
// the git probe fails (no repo, corrupt worktree), the candidate is
// dropped rather than surfaced with empty RepoPath/BaseCommitSHA. A
// surfaced-but-unrecoverable candidate would either confuse the user
// or, if recovered, write garbage into state.json.
func TestDiscoverOrphans_DropsUnrecoverable(t *testing.T) {
	prev := probeWorktreeRepo
	probeWorktreeRepo = func(wt string) (string, string, error) {
		return "", "", fmt.Errorf("not a git worktree")
	}
	t.Cleanup(func() { probeWorktreeRepo = prev })

	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "aidanb")
	require.NoError(t, os.MkdirAll(userDir, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(userDir, "ghost-branch_dead0001"), 0o755))

	got, err := DiscoverOrphans(cfgDir, nil, internalexec.Default{})
	require.NoError(t, err)
	assert.Empty(t, got, "candidates with failing git probes must be dropped")
}

// TestDiscoverOrphans_PopulatesBaseCommitSHA verifies the I1 fix: the
// recovered BaseCommitSHA flows from the probe through to the
// candidate, so applyOrphanRecovery's diff-stats baseline isn't empty.
func TestDiscoverOrphans_PopulatesBaseCommitSHA(t *testing.T) {
	prev := probeWorktreeRepo
	probeWorktreeRepo = func(wt string) (string, string, error) {
		return "/fake/repo", "abc123def456", nil
	}
	t.Cleanup(func() { probeWorktreeRepo = prev })

	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "aidanb")
	require.NoError(t, os.MkdirAll(userDir, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(userDir, "feat-x_dead0001"), 0o755))

	got, err := DiscoverOrphans(cfgDir, nil, internalexec.Default{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "/fake/repo", got[0].RepoPath)
	assert.Equal(t, "abc123def456", got[0].BaseCommitSHA)
}
