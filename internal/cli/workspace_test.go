package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolateWorkspaces gives the test a global dir of its own.
func isolateWorkspaces(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	t.Setenv(config.EnvHome, t.TempDir())
}

// gitRepo is a fresh git repository.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput()
	require.NoError(t, err, string(out))
	return dir
}

// registered registers a repository as name on disk, before the daemon
// boots, with records (a JSON array, "" for none) in its state.json.
func registered(t *testing.T, name, records string) config.Workspace {
	t.Helper()
	ws := config.Workspace{Name: name, Path: gitRepo(t)}
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.Add(ws.Name, ws.Path))
	if records != "" {
		dir := config.WorkspaceConfigDir(&ws)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.StateFileName),
			[]byte(`{"instances":`+records+`}`), 0o644))
	}
	return ws
}

func runWorkspace(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(func() { workspaceName = "" })
	var out bytes.Buffer
	WorkspaceCmd.SetOut(&out)
	WorkspaceCmd.SetErr(&out)
	WorkspaceCmd.SetArgs(args)
	err := WorkspaceCmd.Execute()
	return out.String(), err
}

// `workspace add` registers through the daemon, which serves the new
// workspace at once: a TUI can open it without the daemon rereading the
// registry.
func TestWorkspaceAdd_TheDaemonServesItAtOnce(t *testing.T) {
	isolateWorkspaces(t)
	d := serveDaemon(t, "true")
	repo := gitRepo(t)

	out, err := runWorkspace(t, "add", repo, "--name", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, `Workspace "ws-a" added at `+repo)
	assert.Contains(t, d.workspaces(t), "ws-a")
}

func TestWorkspaceAdd_RefusesADirectoryThatIsNoRepository(t *testing.T) {
	isolateWorkspaces(t)
	d := serveDaemon(t, "true")

	_, err := runWorkspace(t, "add", t.TempDir(), "--name", "ws-a")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a git repository")
	assert.NotContains(t, d.workspaces(t), "ws-a")
}

// `workspace list` shows the daemon's registry, the last used one marked.
func TestWorkspaceList_ShowsTheDaemonsRegistry(t *testing.T) {
	isolateWorkspaces(t)
	a := registered(t, "ws-a", "")
	registered(t, "ws-b", "")
	serveDaemon(t, "true")
	_, err := runWorkspace(t, "use", "ws-a")
	require.NoError(t, err)

	out, err := runWorkspace(t, "list")

	require.NoError(t, err)
	assert.Contains(t, out, "NAME")
	assert.Regexp(t, `ws-a\s+`+a.Path+`\s+\[last used\]`, out)
	assert.Contains(t, out, "ws-b")
}

func TestWorkspaceList_SaysWhenNoneIsRegistered(t *testing.T) {
	isolateWorkspaces(t)
	serveDaemon(t, "true")

	out, err := runWorkspace(t, "list")

	require.NoError(t, err)
	assert.Contains(t, out, "No workspaces registered")
}

// `workspace remove` has the daemon unregister the workspace, which it
// stops serving at once; an unknown name is refused.
func TestWorkspaceRemove_TheDaemonStopsServingIt(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	d := serveDaemon(t, "true")
	require.Contains(t, d.workspaces(t), "ws-a", "fixture: served from boot")

	out, err := runWorkspace(t, "remove", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, `Workspace "ws-a" removed`)
	assert.NotContains(t, d.workspaces(t), "ws-a")

	_, err = runWorkspace(t, "remove", "ws-a")
	assert.Error(t, err, "no longer registered")
}

// `workspace use` sets the daemon's last used workspace.
func TestWorkspaceUse_SetsTheLastUsed(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	d := serveDaemon(t, "true")

	out, err := runWorkspace(t, "use", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, `Default workspace set to "ws-a"`)
	require.NoError(t, d.client.FlushForTest())
	assert.Equal(t, "ws-a", d.client.Registry().LastUsed)

	_, err = runWorkspace(t, "use", "nope")
	assert.Error(t, err)
}

// `workspace rename` renames the served workspace in place: the same
// workspace, under the same ID, by its new name.
func TestWorkspaceRename_KeepsTheWorkspacesID(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	registered(t, "ws-b", "")
	d := serveDaemon(t, "true")
	before := d.workspace(t, "ws-a")

	out, err := runWorkspace(t, "rename", "ws-a", "ws-c")

	require.NoError(t, err)
	assert.Contains(t, out, `Workspace "ws-a" renamed to "ws-c"`)
	assert.Equal(t, before.ID, d.workspace(t, "ws-c").ID)
	assert.NotContains(t, d.workspaces(t), "ws-a")

	_, err = runWorkspace(t, "rename", "ws-c", "ws-b")
	assert.Error(t, err, "a taken name is refused")
}

// `workspace status` counts the sessions the daemon serves for the
// workspace, by name or by the working directory.
func TestWorkspaceStatus_CountsTheDaemonsSessions(t *testing.T) {
	isolateWorkspaces(t)
	a := registered(t, "ws-a", `[{"title":"alpha","status":3,"program":"true","worktree":{"worktree_path":"/tmp/wt-alpha"}},`+
		`{"title":"beta","status":3,"program":"true","worktree":{"worktree_path":"/tmp/wt-beta"}}]`)
	registered(t, "ws-b", "")
	serveDaemon(t, "true")

	out, err := runWorkspace(t, "status", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, `Workspace "ws-a" (`+a.Path+`): 2 instance(s)`)
	assert.Contains(t, out, "  - alpha")
	assert.Contains(t, out, "  - beta")

	out, err = runWorkspace(t, "status", "ws-b")
	require.NoError(t, err)
	assert.Contains(t, out, `Workspace "ws-b"`)
	assert.Contains(t, out, ": 0 instances")

	t.Chdir(a.Path)
	out, err = runWorkspace(t, "status")
	require.NoError(t, err)
	assert.True(t, strings.Contains(out, `Workspace "ws-a"`), "found by the working directory: %s", out)

	_, err = runWorkspace(t, "status", "nope")
	assert.Error(t, err)
}
