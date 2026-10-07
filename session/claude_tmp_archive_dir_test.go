package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/claudetmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setGlobalArchiveDir points this test's global config dir at a fresh one
// whose config.json sets claude_tmp_archive_dir to value.
func setGlobalArchiveDir(t *testing.T, value string) {
	t.Helper()
	global := t.TempDir()
	t.Setenv(config.EnvGlobalDir, global)
	require.NoError(t, os.WriteFile(filepath.Join(global, config.ConfigFileName),
		[]byte(`{"claude_tmp_archive_dir":"`+value+`"}`), 0o644))
}

func zipsIn(t *testing.T, dir string) []string {
	t.Helper()
	zips, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	require.NoError(t, err)
	return zips
}

// TestClaudeTmpArchiveDir: unset, a workspace's archives stay in its own
// config folder. Set, every workspace's go under the configured dir, each
// in its own subfolder, named after the config dir however it is spelled.
func TestClaudeTmpArchiveDir(t *testing.T) {
	t.Setenv(config.EnvGlobalDir, t.TempDir()) // no config.json: unset
	cfgA := filepath.Join(t.TempDir(), "repo-a", ".loom")
	cfgB := filepath.Join(t.TempDir(), "repo-b", ".loom")
	assert.Equal(t, claudetmp.ArchiveDir(cfgA), ClaudeTmpArchiveDir(cfgA))

	root := t.TempDir()
	setGlobalArchiveDir(t, root)
	a, b := ClaudeTmpArchiveDir(cfgA), ClaudeTmpArchiveDir(cfgB)
	assert.Equal(t, root, filepath.Dir(a), "under the configured dir")
	assert.Equal(t, root, filepath.Dir(b))
	assert.NotEqual(t, a, b, "two workspaces never share a folder")
	assert.NotContains(t, filepath.Base(a)[:1], "-", "no leading dash: awkward on a command line")

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(filepath.Dir(filepath.Dir(cfgA)), link))
	assert.Equal(t, a, ClaudeTmpArchiveDir(filepath.Join(link, "repo-a", ".loom")),
		"the same config dir through a symlink gets the same folder")
}

// TestClaudeTmpArchiveDir_AnUnusableSettingKeepsTheDefault: a relative
// value would archive relative to wherever loom runs, so it is ignored.
func TestClaudeTmpArchiveDir_AnUnusableSettingKeepsTheDefault(t *testing.T) {
	setGlobalArchiveDir(t, "relative/archives")
	cfg := t.TempDir()
	assert.Equal(t, claudetmp.ArchiveDir(cfg), ClaudeTmpArchiveDir(cfg))
}

// TestPause_ArchivesIntoTheConfiguredDirAndResumeRestores: Pause and
// Resume both follow the setting.
func TestPause_ArchivesIntoTheConfiguredDirAndResumeRestores(t *testing.T) {
	root := claudeRoot(t)
	setGlobalArchiveDir(t, t.TempDir())
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())

	require.NoError(t, inst.Pause(nil))

	require.Len(t, zipsIn(t, ClaudeTmpArchiveDir(inst.ConfigDir)), 1, "parked in the configured dir")
	assert.Empty(t, zipsIn(t, claudetmp.ArchiveDir(inst.ConfigDir)), "not in the workspace's own folder")

	require.NoError(t, inst.Resume(nil))
	assert.FileExists(t, filepath.Join(dir, "sess-1", "scratchpad", "notes.md"))
	assert.Empty(t, zipsIn(t, ClaudeTmpArchiveDir(inst.ConfigDir)), "a restored archive is deleted")
}

// TestResume_FindsAZipParkedBeforeTheSettingChanged: a session paused
// under the default location and resumed after claude_tmp_archive_dir was
// set still gets its scratchpad back.
func TestResume_FindsAZipParkedBeforeTheSettingChanged(t *testing.T) {
	root := claudeRoot(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir()) // unset while pausing
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())
	require.NoError(t, inst.Pause(nil))
	require.Len(t, zipsIn(t, claudetmp.ArchiveDir(inst.ConfigDir)), 1)

	setGlobalArchiveDir(t, t.TempDir())
	require.NoError(t, inst.Resume(nil))

	assert.FileExists(t, filepath.Join(dir, "sess-1", "scratchpad", "notes.md"))
	assert.Empty(t, zipsIn(t, claudetmp.ArchiveDir(inst.ConfigDir)), "restored from the old location and deleted")
}

// TestSweepClaudeTemp_ArchivesIntoTheConfiguredDir: the sweep follows the
// setting too.
func TestSweepClaudeTemp_ArchivesIntoTheConfiguredDir(t *testing.T) {
	noQuietPeriod(t)
	root := claudeRoot(t)
	setGlobalArchiveDir(t, t.TempDir())
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))

	require.Equal(t, 1, SweepClaudeTemp(cfg, nil, nil))

	assert.Len(t, zipsIn(t, ClaudeTmpArchiveDir(cfg)), 1)
	assert.NoDirExists(t, claudetmp.ArchiveDir(cfg))
}
