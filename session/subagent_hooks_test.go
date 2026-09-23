package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withTracking(t *testing.T, enabled bool) {
	t.Helper()
	prev := !subagentRowsHidden.Load()
	SetSubagentTrackingEnabled(enabled)
	t.Cleanup(func() { SetSubagentTrackingEnabled(prev) })
}

func hooksInstance(t *testing.T, program string) *Instance {
	t.Helper()
	return &Instance{Title: "hooks test", program: program, ConfigDir: t.TempDir(), Status: Running}
}

func settingsFlag(inst *Instance) string {
	return "--settings '" + hooks.SettingsPath(SubagentHooksDir(inst.ConfigDir, inst.Title)) + "'"
}

func TestSubagentHooksDir(t *testing.T) {
	assert.Equal(t, filepath.Join("/cfg", "hooks", "loom_hookstest"), SubagentHooksDir("/cfg", "hooks test"))
}

// A title may contain any printable text. Its folder must still be a
// direct child of the hooks root, or "fix/login" nests under "fix".
func TestSubagentHooksDir_SingleSegment(t *testing.T) {
	root := filepath.Join("/cfg", "hooks")
	for _, title := range []string{"fix/login", "a/../../b", "it's", "/"} {
		assert.Equal(t, root, filepath.Dir(SubagentHooksDir("/cfg", title)), title)
	}
	assert.Equal(t, filepath.Join(root, "loom_fix%2Flogin"), SubagentHooksDir("/cfg", "fix/login"))
	assert.Equal(t, filepath.Join(root, "loom_it%27s"), SubagentHooksDir("/cfg", "it's"))
}

func TestRemoveSubagentHooks_LeavesSlashSiblingAlone(t *testing.T) {
	cfg := t.TempDir()
	nested := SubagentHooksDir(cfg, "fix/login")
	require.NoError(t, os.MkdirAll(nested, 0o700))
	fix := &Instance{Title: "fix", program: "claude", ConfigDir: cfg, Status: Running}
	require.NoError(t, os.MkdirAll(SubagentHooksDir(cfg, fix.Title), 0o700))

	fix.removeSubagentHooks()

	assert.NoDirExists(t, SubagentHooksDir(cfg, fix.Title))
	assert.DirExists(t, nested, "removing fix's folder must not touch fix/login's")
}

func TestLaunchProgram_AddsHooksWhenLaunching(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")

	got := inst.launchProgram("claude", true)

	assert.Contains(t, got, settingsFlag(inst))
	assert.Equal(t, "claude", inst.Program(), "program is never rewritten")
	dir := SubagentHooksDir(inst.ConfigDir, inst.Title)
	stored, err := os.ReadFile(filepath.Join(dir, "launch-id"))
	require.NoError(t, err)
	assert.Equal(t, string(stored), inst.hookLaunchID)
	assert.False(t, inst.subagentWarm)
}

func TestLaunchProgram_ReattachLeavesFolderAlone(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.launchProgram("claude", true)
	launchID := inst.hookLaunchID
	kept := filepath.Join(hooks.EventsDir(SubagentHooksDir(inst.ConfigDir, inst.Title)), "1-1.ev")
	require.NoError(t, os.WriteFile(kept, []byte("{}"), 0o600))

	got := inst.launchProgram("claude", false)

	assert.NotContains(t, got, "--settings")
	assert.FileExists(t, kept)
	assert.Equal(t, launchID, inst.hookLaunchID)
}

func TestLaunchProgram_SkipsHooks(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		program string
		noDir   bool
	}{
		{"non-claude", true, "aider", false},
		{"user settings", true, "claude --settings /mine.json", false},
		{"no config dir", true, "claude", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTracking(t, tc.enabled)
			inst := hooksInstance(t, tc.program)
			if tc.noDir {
				inst.ConfigDir = ""
			}

			got := inst.launchProgram(tc.program, true)

			assert.Equal(t, strings.Count(tc.program, "--settings"), strings.Count(got, "--settings"))
			if inst.ConfigDir != "" {
				assert.NoDirExists(t, filepath.Join(inst.ConfigDir, "hooks"))
			}
			assert.Equal(t, noHooksLaunchID, inst.hookLaunchID, "a launch with no hooks still adopts the sentinel, so a stale result can never match")
		})
	}
}

func TestLaunchProgram_UntrackedRelaunchClearsState(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.launchProgram("claude", true)
	oldLaunchID := inst.hookLaunchID
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: oldLaunchID, Replayed: true,
		Events: []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a1", TranscriptPath: "/p/s.jsonl"}},
		Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore"}}}))
	require.Len(t, inst.Subagents(), 1)

	// A user-supplied --settings is one of the launches that skip loom's hooks.
	inst.SetProgram("claude --settings /mine.json")
	inst.recoveryLaunch()

	assert.Empty(t, inst.Subagents())
	assert.False(t, inst.subagentWarm)
	assert.NoDirExists(t, SubagentHooksDir(inst.ConfigDir, inst.Title))
	assert.False(t, inst.ApplyHookScan(HookScanResult{LaunchID: oldLaunchID, Replayed: true,
		Events: []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a2", TranscriptPath: "/p/s2.jsonl"}}}),
		"a result carrying the previous launch's ID must never be adopted after a relaunch")
}

func TestLaunchProgram_RelaunchResetsWarmTracker(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.launchProgram("claude", true)
	firstID := inst.hookLaunchID
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: firstID, Replayed: true,
		Events: []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a1", TranscriptPath: "/p/s.jsonl"}},
		Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore"}}}))
	require.Len(t, inst.Subagents(), 1)

	inst.launchProgram("claude", true)

	assert.Empty(t, inst.Subagents())
	assert.False(t, inst.subagentWarm)
	assert.NotEqual(t, firstID, inst.hookLaunchID)
}

func TestRecoveryLaunch_AddsHooks(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.SetLaunchOptions("claude", true, true)

	launch, env := inst.recoveryLaunch()

	assert.Equal(t, InstanceEnv("claude --continue", true, true), env,
		"the env keys off the bare recovery program and the instance's launch toggles")
	assert.Contains(t, env, "ANTHROPIC_BASE_URL="+HeadroomProxyURL)
	assert.Contains(t, env, "ENABLE_PROMPT_CACHING_1H=1")
	assert.Contains(t, launch, settingsFlag(inst))
	assert.Contains(t, launch, "--continue")
}

// writeHookEvent drops a raw payload the way the hook command does.
func writeHookEvent(t *testing.T, inst *Instance, stem, payload string, mod time.Time) {
	t.Helper()
	path := filepath.Join(hooks.EventsDir(SubagentHooksDir(inst.ConfigDir, inst.Title)), stem+".json")
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o600))
	require.NoError(t, os.Chtimes(path, mod, mod))
}

// fakeTranscript creates a transcript path with one agent's sidecar.
func fakeTranscript(t *testing.T, agentID, meta string) string {
	t.Helper()
	root := t.TempDir()
	sub := filepath.Join(root, "sess", "subagents")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "agent-"+agentID+".meta.json"), []byte(meta), 0o600))
	return filepath.Join(root, "sess.jsonl")
}

func scanAndApply(t *testing.T, inst *Instance) bool {
	t.Helper()
	req, ok := inst.NextHookScan()
	require.True(t, ok)
	res, err := ScanHooks(req, time.Now())
	require.NoError(t, err)
	return inst.ApplyHookScan(res)
}

func TestSubagents_EndToEndAndRestart(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.launchProgram("claude", true)
	transcript := fakeTranscript(t, "amate-1", `{"agentType":"mate","name":"mate","description":"implement","taskKind":"in_process_teammate"}`)
	t0 := time.Now().Add(-time.Minute)
	writeHookEvent(t, inst, "1", fmt.Sprintf(`{"hook_event_name":"SubagentStart","agent_id":"amate-1","agent_type":"mate","transcript_path":%q}`, transcript), t0)
	writeHookEvent(t, inst, "2", `{"hook_event_name":"SubagentStop","agent_id":"amate-1"}`, t0.Add(time.Second))
	writeHookEvent(t, inst, "3", `{"hook_event_name":"TeammateIdle","teammate_name":"mate"}`, t0.Add(2*time.Second))

	require.True(t, scanAndApply(t, inst))
	want := []subagent.View{{Name: "mate", Description: "implement", Idle: true}}
	assert.Equal(t, want, inst.Subagents())

	// A new loom process restores the same instance with empty memory.
	restored := &Instance{Title: inst.Title, program: "claude", ConfigDir: inst.ConfigDir, Status: Running}
	req, ok := restored.NextHookScan()
	require.True(t, ok)
	assert.True(t, req.Cold)
	require.True(t, scanAndApply(t, restored))
	assert.Equal(t, inst.hookLaunchID, restored.hookLaunchID, "restored instance adopts the folder's launch ID")
	assert.Equal(t, want, restored.Subagents())

	req, _ = restored.NextHookScan()
	assert.False(t, req.Cold)
}

func TestApplyHookScan_Gates(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.launchProgram("claude", true)
	start := hooks.Event{Name: hooks.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: "/p/s.jsonl"}
	meta := map[string]subagent.Meta{"a1": {AgentType: "Explore", Description: "d"}}

	assert.False(t, inst.ApplyHookScan(HookScanResult{}), "empty launch ID")
	assert.False(t, inst.ApplyHookScan(HookScanResult{LaunchID: "other", Replayed: true,
		Events: []hooks.Event{start}, Meta: meta}), "another launch")
	assert.False(t, inst.ApplyHookScan(HookScanResult{LaunchID: inst.hookLaunchID,
		Events: []hooks.Event{start}, Meta: meta}), "incremental result for a cold tracker")
	assert.Empty(t, inst.Subagents())

	assert.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: inst.hookLaunchID, Replayed: true,
		Events: []hooks.Event{start}, Meta: meta}))
	assert.Len(t, inst.Subagents(), 1)
}

// A tracked launch whose folder disappears mid-run (deleted by hand, or by
// another loom process's sweep) must not keep its last rows forever.
func TestForgetSubagentsWithoutHooks(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.launchProgram("claude", true)
	transcript := fakeTranscript(t, "a1", `{"agentType":"Explore","description":"map code"}`)
	writeHookEvent(t, inst, "1", fmt.Sprintf(`{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore","transcript_path":%q}`, transcript),
		time.Now().Add(-time.Second))
	require.True(t, scanAndApply(t, inst))
	require.Len(t, inst.Subagents(), 1)
	launchID := inst.hookLaunchID

	require.NoError(t, os.RemoveAll(SubagentHooksDir(inst.ConfigDir, inst.Title)))
	req, ok := inst.NextHookScan()
	require.True(t, ok)
	_, err := ScanHooks(req, time.Now())
	require.ErrorIs(t, err, hooks.ErrNoHooks)

	inst.ForgetSubagentsWithoutHooks()

	assert.Empty(t, inst.Subagents())
	assert.False(t, inst.subagentWarm)
	assert.Equal(t, launchID, inst.hookLaunchID, "the launch ID is kept, so another launch's folder is never adopted")
}

// ErrNoHooks is the normal state of an instance restored after a loom
// restart that has not adopted a launch ID yet (""), and of a launch
// without hooks (noHooksLaunchID). Neither can hold rows in practice, so
// the IDs are set by hand after the rows to make the guard observable.
func TestForgetSubagentsWithoutHooks_NoRealLaunchIDUntouched(t *testing.T) {
	for _, id := range []string{"", noHooksLaunchID} {
		inst := hooksInstance(t, "claude")
		require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true,
			Events: []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a1", TranscriptPath: "/p/s.jsonl"}},
			Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore"}}}))
		inst.hookLaunchID = id

		inst.ForgetSubagentsWithoutHooks()

		assert.Len(t, inst.Subagents(), 1, "launch ID %q", id)
		assert.True(t, inst.subagentWarm, "launch ID %q", id)
		assert.Equal(t, id, inst.hookLaunchID)
	}
}

func TestSubagents_HiddenWhenNotLive(t *testing.T) {
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true,
		Events: []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a1", TranscriptPath: "/p/s.jsonl"}},
		Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore"}}}))
	for _, st := range []Status{Paused, Recoverable, Deleting} {
		inst.Status = st
		assert.Nil(t, inst.Subagents(), st.String())
	}
	inst.Status = Prompting
	assert.Len(t, inst.Subagents(), 1)
}

func TestNextHookScan(t *testing.T) {
	aider := hooksInstance(t, "aider")
	_, ok := aider.NextHookScan()
	assert.False(t, ok)

	paused := hooksInstance(t, "claude")
	paused.Status = Paused
	_, ok = paused.NextHookScan()
	assert.False(t, ok)

	noDir := hooksInstance(t, "claude")
	noDir.ConfigDir = ""
	_, ok = noDir.NextHookScan()
	assert.False(t, ok)

	inst := hooksInstance(t, "claude")
	req, ok := inst.NextHookScan()
	require.True(t, ok)
	assert.Equal(t, SubagentHooksDir(inst.ConfigDir, inst.Title), req.Dir)
	assert.True(t, req.Cold)

	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true,
		Events: []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a1", TranscriptPath: "/p/s.jsonl"}}}))
	req, _ = inst.NextHookScan()
	assert.False(t, req.Cold)
	assert.Equal(t, []subagent.MetaRef{{AgentID: "a1", Path: "/p/s/subagents/agent-a1.meta.json"}}, req.MissingMeta)
}

func TestKill_RemovesHooksFolder(t *testing.T) {
	withTracking(t, true)
	inst := newTestStartedInstance(t)
	inst.SetProgram("claude")
	inst.ConfigDir = t.TempDir()
	inst.launchProgram("claude", true)
	dir := SubagentHooksDir(inst.ConfigDir, inst.Title)
	require.DirExists(t, dir)

	require.NoError(t, inst.Kill())
	assert.NoDirExists(t, dir)
}

func TestSweepSubagentHooks(t *testing.T) {
	cfg := t.TempDir()
	for _, title := range []string{"claimed", "alive", "dead"} {
		require.NoError(t, os.MkdirAll(SubagentHooksDir(cfg, title), 0o700))
	}
	tmuxLs := func(out string, err error) cmd_test.MockCmdExec {
		return cmd_test.MockCmdExec{OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			require.Contains(t, c.Args, "list-sessions")
			return []byte(out), err
		}}
	}
	claimed := map[string]bool{"claimed": true}

	// tmux unreachable: nothing is removed on a guess.
	SweepSubagentHooks(cfg, claimed, tmuxLs("", errors.New("no server")))
	assert.DirExists(t, SubagentHooksDir(cfg, "dead"))

	SweepSubagentHooks(cfg, claimed, tmuxLs("loom_alive\nloom_term_x\n", nil))
	assert.DirExists(t, SubagentHooksDir(cfg, "claimed"))
	assert.DirExists(t, SubagentHooksDir(cfg, "alive"))
	assert.NoDirExists(t, SubagentHooksDir(cfg, "dead"))
}

func TestSweepSubagentHooks_SlashTitles(t *testing.T) {
	cfg := t.TempDir()
	for _, title := range []string{"fix/login", "other/live", "dead/one"} {
		require.NoError(t, os.MkdirAll(SubagentHooksDir(cfg, title), 0o700))
	}
	ls := cmd_test.MockCmdExec{OutputFunc: func(c *exec.Cmd) ([]byte, error) {
		return []byte("loom_fix/login\nloom_other/live\n"), nil
	}}

	SweepSubagentHooks(cfg, map[string]bool{"fix/login": true}, ls)

	assert.DirExists(t, SubagentHooksDir(cfg, "fix/login"), "claimed and alive")
	assert.DirExists(t, SubagentHooksDir(cfg, "other/live"), "unclaimed but its session is alive")
	assert.NoDirExists(t, SubagentHooksDir(cfg, "dead/one"), "unclaimed and dead")
}

func TestLaunchProgram_HooksInstalledWithTrackingOff(t *testing.T) {
	withTracking(t, false)
	inst := hooksInstance(t, "claude")

	got := inst.launchProgram("claude", true)

	assert.Contains(t, got, settingsFlag(inst), "hooks carry status and the session ID, not only subagent rows")
}

func TestSubagents_HiddenWhenTrackingOff(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true,
		Events: []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a1", TranscriptPath: "/p/s.jsonl"}},
		Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore"}}}))
	require.Len(t, inst.Subagents(), 1)

	withTracking(t, false)
	assert.Nil(t, inst.Subagents())
	withTracking(t, true)
	assert.Len(t, inst.Subagents(), 1, "the tracker kept running, so the rows return at once")
}

func TestRecoveryLaunch_ResumesRecordedConversation(t *testing.T) {
	inst := hooksInstance(t, "claude")
	const id = "8c634184-0fe5-4b62-b437-8f364eeeefcc"
	transcript := filepath.Join(t.TempDir(), id+".jsonl")
	require.NoError(t, os.WriteFile(transcript, nil, 0o600))
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventSessionStart, Source: "startup", SessionID: id, TranscriptPath: transcript, At: time.Now()},
	}}))

	launch, env := inst.recoveryLaunch()

	assert.Contains(t, launch, "--resume "+id)
	assert.NotContains(t, launch, "--continue")
	assert.Equal(t, InstanceEnv("claude --resume "+id, false, false), env)
	got, _ := inst.ClaudeSession()
	assert.Equal(t, id, got, "the relaunch's reset keeps the ID until its own SessionStart replaces it")
}
