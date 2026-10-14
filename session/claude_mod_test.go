package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withModSocket names a mod socket for the test, as the daemon does at boot.
func withModSocket(t *testing.T, path string) {
	t.Helper()
	SetModSocket(path)
	t.Cleanup(func() { SetModSocket("") })
}

func TestLaunchProgram_GivesAClaudeLaunchTheMod(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst := hooksInstance(t, "claude")

	got := inst.launchProgram(LaunchEnv{Program: "claude"}, true)

	mod := filepath.Join(SubagentHooksDir(inst.ConfigDir, inst.Title), "mod")
	assert.Contains(t, got, "--plugin-dir '"+mod+"'")
	assert.Contains(t, got, settingsFlag(inst), "the hooks still come too")
	assert.FileExists(t, filepath.Join(mod, "hooks", "register.ts"))
	launch, hooked := inst.HookLaunch()
	require.True(t, hooked)
	assert.Equal(t, []string{"LOOM_MOD_SOCKET=/run/loom/x.mod.sock", "LOOM_LAUNCH=" + launch}, inst.modEnv())
}

func TestLaunchProgram_NoModWithoutASocket(t *testing.T) {
	withModSocket(t, "")
	inst := hooksInstance(t, "claude")

	got := inst.launchProgram(LaunchEnv{Program: "claude"}, true)

	assert.NotContains(t, got, "--plugin-dir")
	assert.Nil(t, inst.modEnv())
	assert.NoDirExists(t, filepath.Join(SubagentHooksDir(inst.ConfigDir, inst.Title), "mod"))
	_, hooked := inst.HookLaunch()
	assert.True(t, hooked, "the hooks don't depend on the mod")
}

func TestLaunchProgram_NoModWithoutHooks(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	for _, program := range []string{"aider", "claude --settings /mine.json"} {
		inst := hooksInstance(t, program)

		got := inst.launchProgram(LaunchEnv{Program: program}, true)

		assert.NotContains(t, got, "--plugin-dir", program)
		assert.Nil(t, inst.modEnv(), program)
		launch, hooked := inst.HookLaunch()
		assert.Equal(t, noHooksLaunchID, launch, program)
		assert.False(t, hooked, program)
	}
}

// A relaunch that goes without the mod (the socket went away) drops the
// previous launch's mod environment, so no new session names a socket for
// another launch.
func TestLaunchProgram_ARelaunchForgetsTheLastModEnv(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst := hooksInstance(t, "claude")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	require.NotNil(t, inst.modEnv())

	SetModSocket("")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)

	assert.Nil(t, inst.modEnv())
}

// A reattach (launching false) keeps the live launch's mod environment.
func TestLaunchProgram_AReattachKeepsTheModEnv(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst := hooksInstance(t, "claude")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	want := inst.modEnv()

	inst.launchProgram(LaunchEnv{Program: "claude"}, false)

	assert.Equal(t, want, inst.modEnv())
}

func TestHookLaunch_BeforeTheFirstScanIsUnknown(t *testing.T) {
	inst := &Instance{Title: "x", program: "claude"}
	launch, hooked := inst.HookLaunch()
	assert.Equal(t, "", launch)
	assert.False(t, hooked)
}

// The mod's grace runs from the launch's first SessionStart: startup or
// resume, never a later /clear, and a new launch forgets it.
func TestLaunchSessionStartedAt(t *testing.T) {
	inst := hooksInstance(t, "claude")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	launch, _ := inst.HookLaunch()
	_, ok := inst.LaunchSessionStartedAt()
	require.False(t, ok, "nothing reported yet")

	t0 := time.Now().Add(-time.Minute).Round(0)
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: launch, Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventSessionStart, Source: "startup", At: t0},
		{Name: hooks.EventSessionStart, Source: "clear", At: t0.Add(30 * time.Second)},
	}}))
	at, ok := inst.LaunchSessionStartedAt()
	require.True(t, ok)
	assert.Equal(t, t0, at)

	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	_, ok = inst.LaunchSessionStartedAt()
	assert.False(t, ok, "a new launch has not started yet")
}

// The launch sites build the env after the program: pin the order the mod
// env relies on (it reads the launch ID launchProgram minted).
func TestInstanceEnv_CallSitesAppendTheModEnv(t *testing.T) {
	src, err := os.ReadFile("instance.go")
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(src), "InstanceEnv(env), i.modEnv()..."), "instance.go's two launch sites")
	src, err = os.ReadFile("subagent_hooks.go")
	require.NoError(t, err)
	assert.Contains(t, string(src), "append(InstanceEnv(le), i.modEnv()...)")
}
