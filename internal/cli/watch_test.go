package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runWatch runs `loom watch args…` on a fresh command tree (newWatchCmd), so
// no flag carries over from another test.
func runWatch(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newWatchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestWatchOnOff_SwitchTheNamedWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	registered(t, "ws-b", "")
	d := serveDaemon(t, "true")

	out, _, err := runWatch(t, "on", "-w", "ws-a")

	require.NoError(t, err)
	assert.Equal(t, "watch on: ws-a\n", out)
	assert.True(t, d.workspace(t, "ws-a").Watch.On, "the named workspace")
	assert.False(t, d.workspace(t, "ws-b").Watch.On, "another workspace")

	out, _, err = runWatch(t, "off", "-w", "ws-a")

	require.NoError(t, err)
	assert.Equal(t, "watch off: ws-a\n", out)
	assert.False(t, d.workspace(t, "ws-a").Watch.On)
}

func TestWatchOn_TheWorkingDirectoryPicksTheWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	ws := registered(t, "ws-a", "")
	d := serveDaemon(t, "true")
	t.Chdir(ws.Path)

	out, _, err := runWatch(t, "on")

	require.NoError(t, err)
	assert.Equal(t, "watch on: ws-a\n", out)
	assert.True(t, d.workspace(t, "ws-a").Watch.On)
}

func TestWatchOn_RefusesWhatNoWorkspaceHolds(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	d := serveDaemon(t, "true")

	_, _, err := runWatch(t, "on", "-w", "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `workspace "nope" not found`)

	t.Chdir(t.TempDir())
	_, _, err = runWatch(t, "on")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workspace matches current directory")

	assert.False(t, d.workspace(t, "ws-a").Watch.On, "a refused command switches nothing on")
}

func TestWatchOnOff_TakeNoArgumentsAndNoJSONFlag(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")

	_, _, err := runWatch(t, "on", "extra", "-w", "ws-a")
	require.Error(t, err)

	_, _, err = runWatch(t, "off", "--json", "-w", "ws-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --json")
}
