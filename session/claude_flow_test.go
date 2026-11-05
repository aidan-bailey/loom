package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withFlow(t *testing.T, inst *Instance, exe string) {
	t.Helper()
	SetFlowEnabled(inst.ConfigDir, true)
	old := flowExecutable
	flowExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { SetFlowEnabled(inst.ConfigDir, false); flowExecutable = old })
}

func TestLaunchProgram_GivesAClaudeLaunchFlowBesideTheMod(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst := hooksInstance(t, "claude")
	withFlow(t, inst, "/opt/loom/bin/loom")

	got, env := inst.launchCommand(LaunchEnv{Program: "claude"}, true)

	dir := SubagentHooksDir(inst.ConfigDir, inst.Title)
	root := filepath.Join(dir, "flow")
	assert.Contains(t, got, "--plugin-dir '"+filepath.Join(root, "plugin")+"'")
	assert.Contains(t, got, "--plugin-dir '"+filepath.Join(dir, "mod")+"'", "the mod's plugin dir stays")
	assert.True(t, flow.IsRoot(root))
	assert.Contains(t, env, flow.EnvRoot+"="+root)
	settings, err := os.ReadFile(hooks.SettingsPath(dir))
	require.NoError(t, err)
	assert.Contains(t, string(settings), `"command": "'/opt/loom/bin/loom' flow guard"`)
}

func TestLaunchProgram_NoFlowWhenTheSettingIsOff(t *testing.T) {
	inst := hooksInstance(t, "claude")

	got, env := inst.launchCommand(LaunchEnv{Program: "claude"}, true)

	dir := SubagentHooksDir(inst.ConfigDir, inst.Title)
	assert.NotContains(t, got, filepath.Join(dir, "flow"))
	assert.NoDirExists(t, filepath.Join(dir, "flow"))
	for _, kv := range env {
		assert.NotContains(t, kv, flow.EnvRoot)
	}
	settings, err := os.ReadFile(hooks.SettingsPath(dir))
	require.NoError(t, err)
	assert.NotContains(t, string(settings), "flow guard")
}

func TestLaunchProgram_NoFlowWhenLoomCantNameItsBinary(t *testing.T) {
	inst := hooksInstance(t, "claude")
	withFlow(t, inst, "/opt/it's/loom")

	got, _ := inst.launchCommand(LaunchEnv{Program: "claude"}, true)

	assert.NotContains(t, got, "flow/plugin", "no guard, so no flow agents")
	assert.Nil(t, inst.flowEnv())
}

func TestLaunchProgram_NoFlowWithoutHooks(t *testing.T) {
	for _, program := range []string{"aider", "claude --settings /mine.json"} {
		inst := hooksInstance(t, program)
		withFlow(t, inst, "/opt/loom/bin/loom")

		got, _ := inst.launchCommand(LaunchEnv{Program: program}, true)

		assert.NotContains(t, got, "flow/plugin", program)
		assert.Nil(t, inst.flowEnv(), program)
	}
}

func TestLaunchProgram_ARelaunchWithFlowOffDropsTheFlowEnv(t *testing.T) {
	inst := hooksInstance(t, "claude")
	withFlow(t, inst, "/opt/loom/bin/loom")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	require.NotNil(t, inst.flowEnv())

	SetFlowEnabled(inst.ConfigDir, false)
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)

	assert.Nil(t, inst.flowEnv())
}
