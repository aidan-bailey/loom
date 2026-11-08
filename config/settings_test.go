package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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
  "claude_1m_context": true,
  "scheduler": {
    "cpus": 14,
    "memory_gb": 24,
    "io_jobs": 2,
    "default_claim": {"cpus": 4, "memory_gb": 6},
    "starve_after_minutes": 10,
    "keep_finished": 50
  }
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

// savedConfigKeys saves cfg into a fresh dir and returns the top-level keys
// of the config.json it wrote.
func savedConfigKeys(t *testing.T, cfg *Config) map[string]json.RawMessage {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, SaveConfigTo(cfg, dir))
	data, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	require.NoError(t, err)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &keys))
	return keys
}

// TestSettings_SchedulerBlockRoundTrips: the "scheduler" block loads from a
// config.json and saves back to the same JSON, and a config without one saves
// none, so a user who never set it gets no new key.
func TestSettings_SchedulerBlockRoundTrips(t *testing.T) {
	const block = `{
  "cpus": 14,
  "memory_gb": 24,
  "io_jobs": 2,
  "default_claim": {"cpus": 4, "memory_gb": 6},
  "starve_after_minutes": 10,
  "keep_finished": 50
}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{"scheduler": `+block+`}`), 0o644))
	cfg := LoadConfigFrom(dir)
	assert.Equal(t, SchedulerSettings{
		CPUs:               14,
		MemoryGB:           24,
		IOJobs:             2,
		DefaultClaim:       &ClaimSettings{CPUs: 4, MemoryGB: 6},
		StarveAfterMinutes: 10,
		KeepFinished:       50,
	}, cfg.GetScheduler())
	assert.JSONEq(t, block, string(savedConfigKeys(t, cfg)["scheduler"]))

	absent := LoadConfigFrom(t.TempDir())
	assert.Equal(t, SchedulerSettings{}, absent.GetScheduler(), "an absent block is the zero value: every field takes the host's default")
	assert.NotContains(t, savedConfigKeys(t, absent), "scheduler")
}

// TestSettings_CloneCopiesTheSchedulerBlock: a clone's scheduler block and
// its default claim are its own; an absent block and an absent default claim
// stay absent.
func TestSettings_CloneCopiesTheSchedulerBlock(t *testing.T) {
	on := true
	s := Settings{Scheduler: &SchedulerSettings{CPUs: 8, DefaultClaim: &ClaimSettings{CPUs: 2, MemoryGB: 3}, Borrow: &on, Explore: &on}}
	c := s.Clone()
	c.Scheduler.CPUs = 1
	c.Scheduler.DefaultClaim.CPUs = 99
	c.Scheduler.DefaultClaim.MemoryGB = 99
	*c.Scheduler.Borrow = false
	*c.Scheduler.Explore = false
	assert.Equal(t, 8, s.Scheduler.CPUs)
	assert.Equal(t, ClaimSettings{CPUs: 2, MemoryGB: 3}, *s.Scheduler.DefaultClaim)
	assert.True(t, *s.Scheduler.Borrow, "the borrow switch is its own (memory borrowing spec §9)")
	assert.True(t, *s.Scheduler.Explore, "the explore switch is its own (exploration spec §5)")

	assert.Nil(t, Settings{}.Clone().Scheduler, "an absent block stays absent")
	noClaim := Settings{Scheduler: &SchedulerSettings{CPUs: 8}}.Clone()
	assert.Nil(t, noClaim.Scheduler.DefaultClaim, "an absent default claim stays absent")
}

// TestSettings_FeedbackBlockRoundTripsAndClones: the "feedback" block loads
// from a config.json and saves back to the same JSON, a config without one
// saves none, and a clone's grouper block is its own.
func TestSettings_FeedbackBlockRoundTripsAndClones(t *testing.T) {
	const block = `{"workspace": "loom", "grouper": {"enabled": false, "model": "haiku", "effort": "low"}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{"feedback": `+block+`}`), 0o644))
	cfg := LoadConfigFrom(dir)
	off := false
	assert.Equal(t, FeedbackSettings{Workspace: "loom", Grouper: &AgentSettings{Enabled: &off, Model: "haiku", Effort: "low"}}, cfg.Snapshot().GetFeedback())
	assert.JSONEq(t, block, string(savedConfigKeys(t, cfg)["feedback"]))

	absent := LoadConfigFrom(t.TempDir())
	assert.Equal(t, FeedbackSettings{}, absent.Snapshot().GetFeedback())
	assert.NotContains(t, savedConfigKeys(t, absent), "feedback")

	s := Settings{Feedback: &FeedbackSettings{Workspace: "a", Grouper: &AgentSettings{Enabled: &off, Model: "m"}}}
	c := s.Clone()
	c.Feedback.Workspace, c.Feedback.Grouper.Model = "b", "x"
	*c.Feedback.Grouper.Enabled = true
	assert.Equal(t, "a", s.Feedback.Workspace)
	assert.Equal(t, "m", s.Feedback.Grouper.Model)
	assert.False(t, *s.Feedback.Grouper.Enabled)
	assert.Nil(t, Settings{}.Clone().Feedback, "an absent block stays absent")
}

// TestSettings_AttentionBlockRoundTripsAndClones: the "attention" block loads
// from a config.json and saves back to the same JSON, a config without one
// saves none, and a clone's enabled flag is its own.
func TestSettings_AttentionBlockRoundTripsAndClones(t *testing.T) {
	const block = `{"enabled": false, "model": "haiku", "effort": "low", "max_parallel": 2, "wait_seconds": 20}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{"attention": `+block+`}`), 0o644))
	cfg := LoadConfigFrom(dir)
	off := false
	assert.Equal(t, AttentionSettings{Enabled: &off, Model: "haiku", Effort: "low", MaxParallel: 2, WaitSeconds: 20}, cfg.Snapshot().GetAttention())
	assert.JSONEq(t, block, string(savedConfigKeys(t, cfg)["attention"]))

	absent := LoadConfigFrom(t.TempDir())
	assert.Equal(t, AttentionSettings{}, absent.Snapshot().GetAttention())
	assert.NotContains(t, savedConfigKeys(t, absent), "attention")

	s := Settings{Attention: &AttentionSettings{Enabled: &off, Model: "m"}}
	c := s.Clone()
	c.Attention.Model = "x"
	*c.Attention.Enabled = true
	assert.Equal(t, "m", s.Attention.Model)
	assert.False(t, *s.Attention.Enabled)
	assert.Nil(t, Settings{}.Clone().Attention, "an absent block stays absent")
}

// TestSettings_GetSchedulerReturnsACopy: the getter hands out a block the
// caller may change, and the zero value when there is none.
func TestSettings_GetSchedulerReturnsACopy(t *testing.T) {
	assert.Equal(t, SchedulerSettings{}, Settings{}.GetScheduler())

	s := Settings{Scheduler: &SchedulerSettings{KeepFinished: 5, DefaultClaim: &ClaimSettings{CPUs: 2}}}
	got := s.GetScheduler()
	got.KeepFinished = 99
	got.DefaultClaim.CPUs = 99
	assert.Equal(t, 5, s.Scheduler.KeepFinished)
	assert.Equal(t, 2, s.Scheduler.DefaultClaim.CPUs)
}

// TestConfig_SchedulerGetAndSetKeepCopies: SetScheduler keeps a copy of its
// argument, GetScheduler hands out a copy, and a snapshot's block is its own.
func TestConfig_SchedulerGetAndSetKeepCopies(t *testing.T) {
	cfg := FromSettings(Settings{})
	assert.Equal(t, SchedulerSettings{}, cfg.GetScheduler())

	in := SchedulerSettings{CPUs: 6, DefaultClaim: &ClaimSettings{CPUs: 2, MemoryGB: 4}}
	cfg.SetScheduler(in)
	in.CPUs = 99
	in.DefaultClaim.MemoryGB = 99
	want := SchedulerSettings{CPUs: 6, DefaultClaim: &ClaimSettings{CPUs: 2, MemoryGB: 4}}
	got := cfg.GetScheduler()
	assert.Equal(t, want, got, "SetScheduler keeps a copy")

	got.DefaultClaim.CPUs = 99
	assert.Equal(t, 2, cfg.GetScheduler().DefaultClaim.CPUs, "GetScheduler hands out a copy")

	snap := cfg.Snapshot()
	snap.Scheduler.DefaultClaim.CPUs = 77
	assert.Equal(t, want, cfg.GetScheduler(), "a snapshot's block is its own")
}

// TestConfig_SchedulerIsRaceSafe: SetScheduler and GetScheduler run under the
// config lock and share nothing. Only a -race run can fail it.
func TestConfig_SchedulerIsRaceSafe(t *testing.T) {
	cfg := FromSettings(Settings{})
	var wg sync.WaitGroup
	for i := 1; i <= 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 1; j <= 200; j++ {
				cfg.SetScheduler(SchedulerSettings{CPUs: i, DefaultClaim: &ClaimSettings{CPUs: 1, MemoryGB: j}})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if got := cfg.GetScheduler(); got.DefaultClaim != nil {
					got.DefaultClaim.CPUs++ // a copy: this races only if it is shared
				}
			}
		}()
	}
	wg.Wait()
}

func TestValidateBuildSweepAfterHours(t *testing.T) {
	for _, ok := range []int{0, 1, 12, MaxBuildSweepAfterHours} {
		assert.NoError(t, ValidateBuildSweepAfterHours(ok), ok)
	}
	for _, bad := range []int{-1, MaxBuildSweepAfterHours + 1} {
		assert.Error(t, ValidateBuildSweepAfterHours(bad), bad)
	}
}

func TestSettings_FlowEnabledDefaultsOnAndCloneCopiesIt(t *testing.T) {
	assert.True(t, Settings{}.FlowEnabled())
	off := false
	s := Settings{Flow: &off}
	assert.False(t, s.FlowEnabled())
	c := s.Clone()
	*s.Flow = true
	assert.False(t, c.FlowEnabled(), "Clone deep-copies the pointer")
}
