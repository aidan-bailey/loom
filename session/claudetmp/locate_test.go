package claudetmp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func evalSymlinks(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}

// TestEnvNameMatchesTestenv: testenv cannot import this package, so it
// spells the variable itself.
func TestEnvNameMatchesTestenv(t *testing.T) {
	assert.Equal(t, EnvTmpDir, testenv.EnvClaudeTmpDir)
}

// TestDirName_TheReportsExample is the encoding read from Claude 2.1.288.
func TestDirName_TheReportsExample(t *testing.T) {
	name, truncated := DirName("/tb/Source/Academia/kermit/.loom/worktrees/aidanb/tmp-analysis_18db9e2ddd36ba14")
	assert.False(t, truncated)
	assert.Equal(t, "-tb-Source-Academia-kermit--loom-worktrees-aidanb-tmp-analysis-18db9e2ddd36ba14", name)
}

// TestDirName_CountsUTF16Units: Claude's regex replaces UTF-16 code units,
// so a character outside the BMP becomes two dashes.
func TestDirName_CountsUTF16Units(t *testing.T) {
	name, _ := DirName("/a/é/😀")
	assert.Equal(t, "-a-----", name)
}

func TestDirName_TruncatesPast200(t *testing.T) {
	name, truncated := DirName("/" + strings.Repeat("x", 250))
	assert.True(t, truncated)
	assert.Equal(t, "-"+strings.Repeat("x", 199)+"-", name)

	name, truncated = DirName("/" + strings.Repeat("x", 199))
	assert.False(t, truncated, "exactly 200 characters is kept as is")
	assert.Len(t, name, 200)
}

func TestRoot_Precedence(t *testing.T) {
	uid := fmt.Sprintf("claude-%d", os.Getuid())
	bases := []string{t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()}
	for _, b := range bases {
		require.NoError(t, os.Mkdir(filepath.Join(b, uid), 0o700))
	}
	set := func(claude, tmpdir, tmp, temp string) {
		t.Setenv(EnvTmpDir, claude)
		t.Setenv("TMPDIR", tmpdir)
		t.Setenv("TMP", tmp)
		t.Setenv("TEMP", temp)
	}
	for i, vars := range [][4]string{
		{bases[0], bases[1], bases[2], bases[3]},
		{"", bases[1], bases[2], bases[3]},
		{"", "", bases[2], bases[3]},
		{"", "", "", bases[3]},
	} {
		set(vars[0], vars[1], vars[2], vars[3])
		root, ok := Root()
		assert.True(t, ok)
		assert.Equal(t, evalSymlinks(t, filepath.Join(bases[i], uid)), root, "case %d", i)
	}

	set("", "", "", "")
	root, ok := Root()
	want := filepath.Join("/tmp", uid)
	if ok {
		want = evalSymlinks(t, want)
	}
	assert.Equal(t, want, root, "with nothing set the root is under /tmp")
}

func TestRoot_SkipsRelativeValues(t *testing.T) {
	uid := fmt.Sprintf("claude-%d", os.Getuid())
	base := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(base, uid), 0o700))
	t.Setenv(EnvTmpDir, "relative/dir")
	t.Setenv("TMPDIR", base)
	t.Setenv("TMP", "")
	t.Setenv("TEMP", "")

	root, ok := Root()

	assert.True(t, ok)
	assert.Equal(t, evalSymlinks(t, filepath.Join(base, uid)), root, "a relative value is not Claude's base: skip to the next")
}

func TestRoot_ReportsAnAbsentRoot(t *testing.T) {
	t.Setenv(EnvTmpDir, t.TempDir())
	_, ok := Root()
	assert.False(t, ok)
}

// TestLocate_ThroughASymlinkedParentAfterTheLeafIsGone: loom stores the
// worktree path through ~/Source, a symlink to /tb/Source; Claude names
// its dir after the physical path; and by the time loom looks, the
// worktree itself is gone.
func TestLocate_ThroughASymlinkedParentAfterTheLeafIsGone(t *testing.T) {
	root := t.TempDir()
	real := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(real, "wt"), 0o755))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	stored := filepath.Join(link, "wt", "feat_18be000000000001")
	phys, _ := DirName(filepath.Join(evalSymlinks(t, real), "wt", "feat_18be000000000001"))
	require.NoError(t, os.Mkdir(filepath.Join(root, phys), 0o700))

	dir, ok := Locate(root, stored)

	require.True(t, ok)
	assert.Equal(t, filepath.Join(root, phys), dir)
}

func TestLocate_MatchesTheStoredForm(t *testing.T) {
	root := t.TempDir()
	stored := "/no/such/parent/feat_18be000000000001"
	name, _ := DirName(stored)
	require.NoError(t, os.Mkdir(filepath.Join(root, name), 0o700))

	dir, ok := Locate(root, stored)

	require.True(t, ok)
	assert.Equal(t, filepath.Join(root, name), dir)
}

// TestLocate_NeverMatchesATruncatedName: the tail of a truncated name is
// Claude's hash of the full path, so a dir that shares its first 200
// characters may belong to another, running session.
func TestLocate_NeverMatchesATruncatedName(t *testing.T) {
	root := t.TempDir()
	wt := "/" + strings.Repeat("w", 250)
	prefix, truncated := DirName(wt)
	require.True(t, truncated)
	require.NoError(t, os.Mkdir(filepath.Join(root, prefix+"abc123"), 0o700))

	_, ok := Locate(root, wt)
	assert.False(t, ok, "a prefix match could be another session's dir")
}

func TestLocate_IgnoresSymlinks(t *testing.T) {
	root := t.TempDir()
	wt := "/no/such/feat_18be000000000001"
	name, _ := DirName(wt)
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, name)))

	_, ok := Locate(root, wt)
	assert.False(t, ok, "a symlink is never one of Claude's dirs")
}

func TestWorktreePrefixesAndArchiveName(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	prefixes := WorktreePrefixes(filepath.Join(link, ".loom"))
	phys := encode(filepath.Join(evalSymlinks(t, real), ".loom", "worktrees")) + "-"
	require.Len(t, prefixes, 2, "resolved and as given differ through the symlink")
	assert.Contains(t, prefixes, phys)

	assert.Equal(t, "aidanb-67-18daaecd490cb896", ArchiveName(phys+"aidanb-67-18daaecd490cb896", prefixes))
	assert.Equal(t, "x", ArchiveName("--x", nil), "a leading - never survives: unzip reads it as a flag")
}
