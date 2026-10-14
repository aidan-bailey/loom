package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/tmux"
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

// A restored record has no launch ID until its first scan adopts the
// folder's: unknown (""), never the no-hooks sentinel.
func TestHookLaunch_BeforeTheFirstScanIsUnknown(t *testing.T) {
	inst, err := FromInstanceData(InstanceData{Title: "x", Status: Running, Program: "claude", IsWorkspaceTerminal: true}, t.TempDir())
	require.NoError(t, err)

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

// The first startup or resume names the launch's start; neither a later
// resume nor a /clear before it does.
func TestLaunchSessionStartedAt_TheFirstStartupOrResumeWins(t *testing.T) {
	t0 := time.Now().Add(-time.Minute).Round(0)
	start := func(source string, at time.Time) hooks.Event {
		return hooks.Event{Name: hooks.EventSessionStart, Source: source, At: at}
	}
	cases := []struct {
		name   string
		events []hooks.Event
		want   time.Time
	}{
		{"a later resume", []hooks.Event{start("startup", t0), start("resume", t0.Add(30*time.Second))}, t0},
		{"a clear first", []hooks.Event{start("clear", t0), start("startup", t0.Add(time.Second))}, t0.Add(time.Second)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := hooksInstance(t, "claude")
			inst.launchProgram(LaunchEnv{Program: "claude"}, true)
			launch, _ := inst.HookLaunch()

			require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: launch, Replayed: true, Events: tc.events}))

			at, ok := inst.LaunchSessionStartedAt()
			require.True(t, ok)
			assert.Equal(t, tc.want, at)
		})
	}
}

// A relaunch that skips the hooks (a user's own --settings) gets no mod
// either, even with the socket still set: resetHookLaunch drops the last
// launch's mod env, which named another launch.
func TestRecoveryLaunch_WithoutHooksDropsTheLastModEnv(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst := hooksInstance(t, "claude")
	_, env, err := inst.recoveryLaunch()
	require.NoError(t, err)
	require.Contains(t, env, "LOOM_MOD_SOCKET=/run/loom/x.mod.sock")

	inst.SetProgram("claude --settings /mine.json")
	_, env, err = inst.recoveryLaunch()

	require.NoError(t, err)
	assert.Nil(t, inst.modEnv())
	assert.NotContains(t, env, "LOOM_MOD_SOCKET=/run/loom/x.mod.sock")
}

// launchEnvs is the env (each -e entry) of every new-session srv saw: what
// each launch's Claude got.
func launchEnvs(srv *fakeTmuxServer) [][]string {
	var envs [][]string
	for _, argv := range srv.launchArgs() {
		var env []string
		for n := 0; n+1 < len(argv); n++ {
			if argv[n] == "-e" {
				env = append(env, argv[n+1])
			}
		}
		envs = append(envs, env)
	}
	return envs
}

// sessionDied makes srv answer that the session is gone, as after its agent
// exited, so the next launch can start it again.
func sessionDied(srv *fakeTmuxServer) {
	srv.mu.Lock()
	srv.created = false
	srv.mu.Unlock()
}

// relaunchTwice runs relaunch twice, each after the session died, and
// asserts each launch's Claude got the mod env naming that launch, never
// the one before it.
func relaunchTwice(t *testing.T, inst *Instance, srv *fakeTmuxServer, relaunch func() error) {
	t.Helper()
	var launches []string
	for range 2 {
		sessionDied(srv)
		require.NoError(t, relaunch())
		launch, hooked := inst.HookLaunch()
		require.True(t, hooked)
		launches = append(launches, launch)
	}

	envs := launchEnvs(srv)
	require.Len(t, envs, 2)
	require.NotEqual(t, launches[0], launches[1])
	for n, env := range envs {
		assert.Contains(t, env, "LOOM_LAUNCH="+launches[n], "launch %d", n+1)
		assert.Contains(t, env, "LOOM_MOD_SOCKET=/run/loom/x.mod.sock", "launch %d", n+1)
	}
}

// The workspace terminal's auto-restart hands each new Claude its own
// launch's mod env: read before launchProgram, it would name the previous
// launch, whose ID the daemon no longer knows.
func TestRestart_GivesEachLaunchItsOwnModEnv(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	srv := &fakeTmuxServer{}
	inst := &Instance{
		Title:               "restart-mod",
		Path:                t.TempDir(),
		program:             "claude",
		ConfigDir:           t.TempDir(),
		Status:              Running,
		IsWorkspaceTerminal: true,
	}
	inst.setTmuxSession(tmux.NewSessionWithDeps(inst.Title, "claude", srv, srv.runner()))
	inst.setStarted(true)

	relaunchTwice(t, inst, srv, inst.Restart)
}

func TestCrashRestart_GivesEachLaunchItsOwnModEnv(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst, srv := newCrashRecoveredInstance(t)
	inst.ConfigDir = t.TempDir()

	relaunchTwice(t, inst, srv, inst.CrashRestart)
}

// Every launch builds its command and env through launchCommand, which
// reads the mod env only after launchProgram. Start(true) builds its tmux
// session with no seam a test could launch through, so this pins by source
// that no site calls launchProgram on its own.
func TestLaunchProgram_OnlyLaunchCommandCallsIt(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	var callers []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		if n := strings.Count(string(src), ".launchProgram("); n > 0 {
			callers = append(callers, fmt.Sprintf("%s: %d", f, n))
		}
	}
	assert.Equal(t, []string{"subagent_hooks.go: 1"}, callers, "only launchCommand calls launchProgram")
}
