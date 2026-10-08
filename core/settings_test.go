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
	ws := storedWorkspace(t, "a")
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
