package rpc

import (
	"os"
	"os/exec"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registered is a fixture workspace over def's config dir, as the model
// serves a registered workspace (with no repository, so no terminal).
func registered(t *testing.T, def config.Workspace) *core.Workspace {
	t.Helper()
	dir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	state := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(state, dir)
	require.NoError(t, err)
	return core.NewWorkspace(core.WorkspaceParts{
		Ctx:     &config.WorkspaceContext{Name: def.Name, ConfigDir: dir},
		Storage: storage, Config: config.DefaultConfig(), State: state,
	})
}

// The workspace requests a CLI client makes, over the wire: a rename's and
// an unregister's effects are in the replica when each returns, a refusal
// still matches ErrRefused, and a reset's outcome arrives as its Reply,
// naming the client's own request.
func TestWorkspaceRequests_ThroughTheClient(t *testing.T) {
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	reg := &config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "a", Path: t.TempDir()}, {Name: "b", Path: t.TempDir()},
	}}
	require.NoError(t, config.SaveWorkspaceRegistry(reg))
	noTmux := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return &exec.ExitError{} },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	model := core.NewForTest(core.Options{Registry: reg, CmdExec: noTmux})
	model.SetWorkspacesForTest(registered(t, reg.Workspaces[0]), registered(t, reg.Workspaces[1]))
	loop := core.StartForTest(model)
	c := pair(t, loop)
	ids := map[string]core.WorkspaceID{}
	for _, v := range c.Workspaces() {
		ids[v.Name] = v.ID
	}

	require.NoError(t, c.RenameWorkspace("a", "c"))
	v, ok := c.Workspace(ids["a"])
	require.True(t, ok)
	assert.Equal(t, "c", v.Name, "renamed in place, under the same ID")
	assert.Equal(t, "c", c.Registry().Workspaces[0].Name)

	assert.ErrorIs(t, c.Unregister("nope"), core.ErrRefused)
	require.NoError(t, c.Unregister("b"))
	assert.False(t, c.IsLoaded(ids["b"]), "no longer served")

	require.NoError(t, c.Reset(ids["a"], 5))
	for _, j := range loop.JobsForTest() {
		loop.DeliverForTest(j())
	}
	require.NoError(t, c.FlushForTest())
	var reply *core.Reply
	for _, ev := range c.Sync() {
		if r, ok := ev.(core.Reply); ok && r.Req == 5 {
			reply = &r
		}
	}
	require.NotNil(t, reply, "the reset's Reply")
	assert.NoError(t, reply.Err)
	assert.ErrorIs(t, c.Reset(99, 6), core.ErrRefused)
}
