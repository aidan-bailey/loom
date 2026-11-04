package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveSettings_WritesAppliesAndPublishes(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "") // the global workspace, whose program is the model's
	t.Setenv(config.EnvGlobalDir, ws.ctx.ConfigDir)
	m.SetWorkspacesForTest(ws)
	id := m.wsIDOf(ws)
	m.Sync()

	s := ws.cfg.Snapshot()
	s.DefaultProgram = "aider --yes"
	off, on := false, true
	s.ClaudeLoomContext = &off
	s.ClaudeSubagentTracking = &on
	dir := ws.ctx.ConfigDir
	session.SetLoomContextEnabled(dir, true)
	session.SetSubagentTrackingEnabled(dir, false)
	require.NoError(t, m.SaveSettings(id, s))

	assert.Equal(t, "aider --yes", m.program, "the accounts' program follows")
	assert.False(t, session.LoomContextEnabled(dir), "the workspace's loom-context toggle follows")
	assert.True(t, session.SubagentTrackingEnabled(dir), "the workspace's subagent-tracking toggle follows")
	data, err := os.ReadFile(filepath.Join(ws.ctx.ConfigDir, config.ConfigFileName))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"default_program": "aider --yes"`, "config.json is written")
	wc := workspacesEvent(m.Sync().Events)
	require.NotNil(t, wc, "the change republishes")
	assert.Equal(t, "aider --yes", wc.Views[0].Settings.DefaultProgram)

	s.DefaultProgram = "changed after"
	assert.Equal(t, "aider --yes", ws.cfg.GetProgram(), "the model keeps its own copy")
}

// The model's program (the accounts' Claude commands, orphan
// placeholders) is the global config's, as the daemon starts it: another
// workspace's save changes that workspace's own program, not the model's,
// and the global workspace's save changes it, however its dir is spelled.
func TestSaveSettings_OnlyTheGlobalWorkspaceSetsTheModelsProgram(t *testing.T) {
	global, a := storedWorkspace(t, ""), storedWorkspace(t, "a")
	link := filepath.Join(t.TempDir(), "global")
	require.NoError(t, os.Symlink(global.ctx.ConfigDir, link))
	t.Setenv(config.EnvGlobalDir, link)
	m := NewForTest(Options{Program: "claude"})
	m.SetWorkspacesForTest(global, a)

	s := a.cfg.Snapshot()
	s.DefaultProgram = "aider --yes"
	require.NoError(t, m.SaveSettings(m.wsIDOf(a), s))
	assert.Equal(t, "claude", m.program, "another workspace's save leaves the model's program alone")
	assert.Equal(t, "claude", m.ClaudeProgram())
	assert.Equal(t, "aider --yes", a.cfg.GetProgram(), "it changes its own")

	s = global.cfg.Snapshot()
	s.DefaultProgram = "claude --verbose"
	require.NoError(t, m.SaveSettings(m.wsIDOf(global), s))
	assert.Equal(t, "claude --verbose", m.program, "the global workspace's save sets it")
}

func TestSettingsRequests_RefuseAnUnknownWorkspace(t *testing.T) {
	m := NewForTest(Options{})
	assert.Error(t, m.SaveSettings(99, config.Settings{}))
	assert.Error(t, m.SetUIPrefs(99, config.UIPrefs{}))
	assert.Error(t, m.SetHelpScreensSeen(99, 1))
}

func TestSetUIPrefsAndHelpScreens_Persist(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(ws)
	id := m.wsIDOf(ws)

	prefs := config.UIPrefs{RailHidden: true, SplitRatios: map[string]float64{"x": 0.4}}
	require.NoError(t, m.SetUIPrefs(id, prefs))
	require.NoError(t, m.SetHelpScreensSeen(id, 5))
	prefs.SplitRatios["x"] = 0.9

	reloaded := config.LoadStateFrom(ws.ctx.ConfigDir)
	assert.True(t, reloaded.GetUIPrefs().RailHidden, "state.json holds the prefs")
	assert.InDelta(t, 0.4, reloaded.GetUIPrefs().SplitRatios["x"], 0, "the model kept its own copy")
	assert.Equal(t, uint32(5), reloaded.GetHelpScreensSeen())
}

// Saving one workspace's settings changes only its own sessions' flags:
// the session package keeps them per config dir, since every loaded
// workspace launches with its own config's.
func TestSaveSettings_SessionFlagsArePerWorkspace(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	m.SetWorkspacesForTest(a, b)
	session.SetLoomContextEnabled(b.ctx.ConfigDir, true)
	session.SetSubagentTrackingEnabled(b.ctx.ConfigDir, true)

	s := a.cfg.Snapshot()
	off := false
	s.ClaudeLoomContext, s.ClaudeSubagentTracking = &off, &off
	require.NoError(t, m.SaveSettings(m.wsIDOf(a), s))

	assert.False(t, session.LoomContextEnabled(a.ctx.ConfigDir))
	assert.False(t, session.SubagentTrackingEnabled(a.ctx.ConfigDir))
	assert.True(t, session.LoomContextEnabled(b.ctx.ConfigDir), "another workspace's toggle is untouched")
	assert.True(t, session.SubagentTrackingEnabled(b.ctx.ConfigDir), "another workspace's toggle is untouched")
}

// wqGlobalWorkspaceModel serves a stored global workspace over a model whose
// work queue is loaded in the same dir, so setSchedulerCaps (what another TUI's
// SetSchedulerCaps ends in) writes the config.json that SaveSettings saves.
func wqGlobalWorkspaceModel(t *testing.T) (*Model, *Workspace, string) {
	t.Helper()
	ws := storedWorkspace(t, "")
	dir := ws.ctx.ConfigDir
	m := wqModelAt(t, dir) // points LOOM_GLOBAL_DIR at dir
	m.SetWorkspacesForTest(ws)
	return m, ws, dir
}

// A hand edit of the scheduler block takes effect through refreshPolicy but
// never reaches the global workspace's ws.cfg: the next S-overlay save must not
// write ws.cfg's stale block over it.
func TestSaveSettings_AHandEditOfTheSchedulerBlockSurvivesAnOverlaySave(t *testing.T) {
	m, ws, dir := wqGlobalWorkspaceModel(t)
	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 4}))
	require.Equal(t, config.SchedulerSettings{CPUs: 4}, ws.cfg.GetScheduler())

	hand := `{"scheduler": {"cpus": 9, "keep_finished": 5}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(hand), 0o644))
	s := ws.cfg.Snapshot() // the overlay's copy still holds cpus 4
	s.DefaultProgram = "aider --yes"
	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), s))

	want := config.SchedulerSettings{CPUs: 9, KeepFinished: 5}
	saved := config.LoadConfigFrom(dir)
	assert.Equal(t, want, saved.GetScheduler(), "the hand edit is on disk still")
	assert.Equal(t, "aider --yes", saved.GetProgram(), "the rest of the save is written")
	assert.Equal(t, want, ws.cfg.GetScheduler(), "ws.cfg now holds the block the file has")
	assert.Equal(t, "aider --yes", ws.cfg.GetProgram())
}

// The overlay edits a copy taken when it opened and saves it on every change: a
// SetSchedulerCaps from another TUI made after the copy was taken must survive
// the copy's save.
func TestSaveSettings_AnOverlaysStaleCopyDoesNotRevertNewCaps(t *testing.T) {
	m, ws, dir := wqGlobalWorkspaceModel(t)
	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 4}))
	copyOfTheOverlay := ws.cfg.Snapshot() // taken as the overlay opens
	require.NotNil(t, copyOfTheOverlay.Scheduler)
	require.Equal(t, 4, copyOfTheOverlay.Scheduler.CPUs)

	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 8, MemoryGB: 12})) // another TUI
	copyOfTheOverlay.DefaultProgram = "aider --yes"
	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), copyOfTheOverlay))

	want := config.SchedulerSettings{CPUs: 8, MemoryGB: 12}
	assert.Equal(t, want, config.LoadConfigFrom(dir).GetScheduler(), "the newer caps are on disk still")
	assert.Equal(t, want, ws.cfg.GetScheduler())
	assert.Equal(t, 4, copyOfTheOverlay.Scheduler.CPUs, "the caller's copy is not changed")
}

// A file without the block, or no file at all, reads as no block: ws.cfg's
// stale block is not revived.
func TestSaveSettings_AMissingBlockOrFileReadsAsNoBlock(t *testing.T) {
	m, ws, dir := wqGlobalWorkspaceModel(t)
	path := filepath.Join(dir, config.ConfigFileName)
	ws.cfg.SetScheduler(config.SchedulerSettings{CPUs: 4}) // stale in memory
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o644))

	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), ws.cfg.Snapshot()))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"scheduler"`, "a block the file lacks is not written back")
	assert.Equal(t, config.SchedulerSettings{}, ws.cfg.GetScheduler())

	ws.cfg.SetScheduler(config.SchedulerSettings{CPUs: 4})
	require.NoError(t, os.Remove(path))
	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), ws.cfg.Snapshot()), "a missing file is no block, not an error")
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"scheduler"`)
	assert.Equal(t, config.SchedulerSettings{}, ws.cfg.GetScheduler())
}

// A global config.json that can't be parsed (or read) refuses the save with the
// error and changes nothing: not the file, not ws.cfg, not the model's program.
func TestSaveSettings_AnUnreadableGlobalConfigRefusesTheSave(t *testing.T) {
	refused := func(t *testing.T, m *Model, ws *Workspace) error {
		t.Helper()
		before, programBefore := ws.cfg.GetProgram(), m.program
		s := ws.cfg.Snapshot()
		s.DefaultProgram = "aider --yes"

		err := m.SaveSettings(m.wsIDOf(ws), s)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "save settings")
		assert.Equal(t, before, ws.cfg.GetProgram(), "ws.cfg is not replaced")
		assert.Equal(t, programBefore, m.program)
		return err
	}

	t.Run("unparseable", func(t *testing.T) {
		m, ws, dir := wqGlobalWorkspaceModel(t)
		path := filepath.Join(dir, config.ConfigFileName)
		broken := `{"scheduler": {"cpus": `
		require.NoError(t, os.WriteFile(path, []byte(broken), 0o644))

		refused(t, m, ws)

		data, err := os.ReadFile(path)
		require.NoError(t, err, "the file is not quarantined")
		assert.Equal(t, broken, string(data), "the file is left as it was")
	})
	t.Run("unreadable", func(t *testing.T) {
		m, ws, dir := wqGlobalWorkspaceModel(t)
		path := filepath.Join(dir, config.ConfigFileName)
		require.NoError(t, os.Mkdir(path, 0o755)) // reading a directory fails

		refused(t, m, ws)

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.True(t, info.IsDir(), "the path is left as it was")
	})
}

// Only the global workspace's save is guarded: another workspace's config.json
// carries no scheduler block of the queue's, and its save writes what it holds.
// The global config.json is not read, let alone written.
func TestSaveSettings_ANonGlobalWorkspaceSavesWhatItHolds(t *testing.T) {
	globalDir := t.TempDir()
	m := wqModelAt(t, globalDir)
	hand := `{"scheduler": {"cpus": 9}}`
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, config.ConfigFileName), []byte(hand), 0o644))
	a := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(a)
	a.cfg.SetScheduler(config.SchedulerSettings{CPUs: 3})

	s := a.cfg.Snapshot()
	s.DefaultProgram = "aider --yes"
	require.NoError(t, m.SaveSettings(m.wsIDOf(a), s))

	assert.Equal(t, config.SchedulerSettings{CPUs: 3}, config.LoadConfigFrom(a.ctx.ConfigDir).GetScheduler(), "it writes the block it was handed")
	assert.Equal(t, config.SchedulerSettings{CPUs: 3}, a.cfg.GetScheduler())
	data, err := os.ReadFile(filepath.Join(globalDir, config.ConfigFileName))
	require.NoError(t, err)
	assert.Equal(t, hand, string(data), "the global config.json is untouched")
}

// TestSaveSettings_KeepsTheBuildSweepThreshold: the threshold is
// ConfigureBuildSweep's to change; a client's copy carrying another value
// writes nothing of it.
func TestSaveSettings_KeepsTheBuildSweepThreshold(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(ws)
	s := ws.cfg.Snapshot()
	s.BuildSweepAfterHours = 99
	s.BranchPrefix = "y/"

	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), s))

	assert.Equal(t, "y/", ws.cfg.Snapshot().BranchPrefix, "fixture: the save landed")
	assert.Zero(t, ws.cfg.Snapshot().BuildSweepAfterHours)
}
