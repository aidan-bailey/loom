package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaudeProgram writes an executable named claude that only sleeps: a
// launch of it counts as Claude (the adapter matches the basename), so
// remote control applies, without running the real CLI.
func fakeClaudeProgram(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nsleep 30\n"), 0o755))
	return p
}

// TestRegisterWorkspace_RecoverySummaryWinsOverRCOffLine pins the order
// of effects on a workspace load: the model's notices (here the workspace
// terminal's "remote control off" line) are applied right after the load
// (activateWorkspace drains them), so the recovery summary the caller
// shows afterwards replaces it on the error bar, as it did when the load
// set the line itself.
func TestRegisterWorkspace_RecoverySummaryWinsOverRCOffLine(t *testing.T) {
	for _, tc := range []struct {
		name      string
		instances string
		want      string
		notWant   string
	}{
		{"no recovery: the rc-off line shows", `[]`, "remote control off: not logged in", "Recovery:"},
		{"recovery: the summary replaces it",
			`[{"schema_version":99,"title":"future","worktree":{"worktree_path":"/tmp/wt-future"}}]`,
			"Recovery: 1 session record could not be read", "remote control off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			t.Setenv(config.EnvGlobalDir, t.TempDir()) // a registry of its own
			def := writeWorkspaceState(t, "rc-ws", tc.instances)
			require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&def), config.ConfigFileName),
				[]byte(`{"default_program":"`+fakeClaudeProgram(t)+`"}`), 0o644))
			reg, err := config.LoadWorkspaceRegistry()
			require.NoError(t, err)
			m := newRestoreHome(t, &recordingExec{})
			testModel(m).SetRegistryForTest(reg)
			m.core.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"})
			m.ctx = cancelledCtx()
			m.errBox.SetSize(400, 1)

			m.Update(registerWorkspaceMsg{name: def.Name, dir: def.Path})

			require.Len(t, m.slots, 1, "fixture: the workspace opened")
			require.NotNil(t, m.list.GetInstanceByTitle(def.Name), "fixture: its workspace terminal was created")
			assert.Contains(t, m.errBox.String(), tc.want)
			assert.NotContains(t, m.errBox.String(), tc.notWant)
		})
	}
}

// TestCheckSlotInvariant_SlotsMustMirrorTheModel: the slots are views
// over the model's workspaces, so slots that no longer show the model's
// tabs in order, or a classic slot that doesn't show the model's classic
// workspace, are a broken invariant even when focus is consistent.
func TestCheckSlotInvariant_SlotsMustMirrorTheModel(t *testing.T) {
	t.Run("tabs out of order", func(t *testing.T) {
		m := fleetHome(t)
		require.NoError(t, m.checkSlotInvariant())
		// Swap the views without telling the model, keeping focus on the
		// same slot so only the mirror is broken.
		m.slots[0], m.slots[1] = m.slots[1], m.slots[0]
		m.focusedSlot = 1

		err := m.checkSlotInvariant()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not show the model's tab")
	})
	t.Run("classic slot over another workspace", func(t *testing.T) {
		m := newTestHome(t)
		require.NoError(t, m.checkSlotInvariant())
		m.workspaceSlot.id = testModel(m).WorkspaceIDForTest(testWS(core.WorkspaceParts{}))

		err := m.checkSlotInvariant()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "classic workspace")
	})
}
