package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSettings_ConfigJSONIsUnchanged: Config embeds Settings, and JSON
// flattens an embedded struct, so config.json keeps its format. A file
// with every field set loads and saves back to the same JSON.
func TestSettings_ConfigJSONIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	in := []byte(`{
  "default_program": "claude",
  "branch_prefix": "me/",
  "base_branch": "develop",
  "profiles": [
    {
      "name": "fast",
      "program": "claude --model haiku"
    }
  ],
  "claude_remote_control": false,
  "claude_loom_context": true,
  "claude_subagent_tracking": false,
  "claude_permission_mode": "plan",
  "theme": "legacy",
  "claude_tmp_archive_dir": "/tmp/archive",
  "headroom_proxy": true,
  "claude_model": "opus",
  "claude_effort": "high",
  "cache_ttl_1h": true,
  "claude_1m_context": true
}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), in, 0o644))
	cfg := LoadConfigFrom(dir)
	require.NoError(t, SaveConfigTo(cfg, dir))
	out, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	require.NoError(t, err)
	assert.JSONEq(t, string(in), string(out))
}

// TestSettings_CloneSharesNothing: a clone's profiles and pointer fields
// are its own.
func TestSettings_CloneSharesNothing(t *testing.T) {
	on, mode := true, "plan"
	s := Settings{
		Profiles:             []Profile{{Name: "a", Program: "x"}},
		ClaudeRemoteControl:  &on,
		ClaudePermissionMode: &mode,
	}
	c := s.Clone()
	c.Profiles[0].Program = "y"
	*c.ClaudeRemoteControl = false
	*c.ClaudePermissionMode = "auto"
	assert.Equal(t, "x", s.Profiles[0].Program)
	assert.True(t, *s.ClaudeRemoteControl)
	assert.Equal(t, "plan", *s.ClaudePermissionMode)
}

// TestConfig_SnapshotAndReplaceAreCopies: Snapshot hands out a copy the
// caller may change, and ReplaceSettings keeps none of its argument.
func TestConfig_SnapshotAndReplaceAreCopies(t *testing.T) {
	cfg := FromSettings(Settings{Profiles: []Profile{{Name: "a", Program: "x"}}})
	snap := cfg.Snapshot()
	snap.Profiles[0].Program = "changed"
	assert.Equal(t, "x", cfg.Snapshot().Profiles[0].Program, "a snapshot is a copy")

	next := Settings{DefaultProgram: "aider", Profiles: []Profile{{Name: "b", Program: "y"}}}
	cfg.ReplaceSettings(next)
	next.Profiles[0].Program = "changed"
	assert.Equal(t, "aider", cfg.GetProgram())
	assert.Equal(t, "y", cfg.Snapshot().Profiles[0].Program, "ReplaceSettings keeps a copy")
}

// TestFromSettings_OwnsItsCopy: a Config built from settings shares nothing
// with them.
func TestFromSettings_OwnsItsCopy(t *testing.T) {
	s := Settings{Profiles: []Profile{{Name: "a", Program: "x"}}}
	cfg := FromSettings(s)
	cfg.Mutate(func(c *Config) { c.Profiles[0].Program = "y" })
	assert.Equal(t, "x", s.Profiles[0].Program)
}
