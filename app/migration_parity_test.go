package app

import (
	"testing"

	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationParity verifies that every retired Go hotkey now produces
// the same Intent via the Lua dispatch path as its pre-migration runXYZ
// handler would have. This is the single guard against defaults.lua
// drifting away from the legacy keymap.
//
// Sync primitives (j/k/d/[/]/;/l) don't emit intents — they mutate
// host state directly. Those have their own subtest below that observes
// the side-effect rather than a pendingIntent.
func TestMigrationParity(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want script.Intent
	}{
		{"quit", "q", script.QuitIntent{}},
		{"new_instance", "n", script.NewInstanceIntent{}},
		{"new_instance_prompt", "N", script.NewInstanceIntent{Prompt: true}},
		{"kill_selected", "D", script.KillSelectedIntent{Confirm: true}},
		{"push_selected", "p", script.PushSelectedIntent{Confirm: true}},
		{"checkout_selected", "c", script.CheckoutIntent{Confirm: true, Help: true}},
		{"resume_selected", "r", script.ResumeIntent{}},
		{"show_help", "?", script.ShowHelpIntent{}},
		{"workspace_picker", "W", script.WorkspacePickerIntent{}},
		{"fullscreen_attach_agent", "alt+a", script.FullscreenAttachIntent{Pane: script.AttachPaneAgent}},
		{"fullscreen_attach_terminal", "alt+t", script.FullscreenAttachIntent{Pane: script.AttachPaneTerminal}},
		{"inline_attach_agent", "ctrl+a", script.InlineAttachIntent{Pane: script.AttachPaneAgent}},
		{"inline_attach_terminal", "ctrl+t", script.InlineAttachIntent{Pane: script.AttachPaneTerminal}},
		{"quick_input_agent", "a", script.QuickInputIntent{Pane: script.AttachPaneAgent}},
		{"quick_input_terminal", "t", script.QuickInputIntent{Pane: script.AttachPaneTerminal}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestHome(t)

			cmd, ok := m.dispatchScript(tc.key)
			require.True(t, ok, "key %q must be bound by defaults.lua", tc.key)
			require.NotNil(t, cmd)

			msg, ok := cmd().(scriptDoneMsg)
			require.True(t, ok, "expected scriptDoneMsg, got %T", cmd())
			require.NoError(t, msg.err)
			require.Len(t, msg.pendingIntents, 1, "expected exactly one intent for key %q", tc.key)
			assert.Equal(t, tc.want, msg.pendingIntents[0].intent)
		})
	}
}

// TestMigrationParitySyncPrimitives covers the keys whose defaults.lua
// handler calls a sync primitive (cursor_up/down, toggle_diff,
// workspace_prev/next). These never yield — we assert the observable
// side-effect on h after dispatchScript runs.
func TestMigrationParitySyncPrimitives(t *testing.T) {
	t.Run("cursor_down_j", func(t *testing.T) {
		m := newTestHome(t)
		a := mustAddInstance(t, m, "a")
		b := mustAddInstance(t, m, "b")
		m.list.SelectInstance(a)

		runDispatch(t, m, "j")
		assert.Equal(t, b, m.list.GetSelectedInstance())
	})

	t.Run("cursor_down_down", func(t *testing.T) {
		m := newTestHome(t)
		a := mustAddInstance(t, m, "a")
		b := mustAddInstance(t, m, "b")
		m.list.SelectInstance(a)

		runDispatch(t, m, "down")
		assert.Equal(t, b, m.list.GetSelectedInstance())
	})

	t.Run("cursor_up_k", func(t *testing.T) {
		m := newTestHome(t)
		a := mustAddInstance(t, m, "a")
		b := mustAddInstance(t, m, "b")
		m.list.SelectInstance(b)

		runDispatch(t, m, "k")
		assert.Equal(t, a, m.list.GetSelectedInstance())
	})

	t.Run("cursor_up_up", func(t *testing.T) {
		m := newTestHome(t)
		a := mustAddInstance(t, m, "a")
		b := mustAddInstance(t, m, "b")
		m.list.SelectInstance(b)

		runDispatch(t, m, "up")
		assert.Equal(t, a, m.list.GetSelectedInstance())
	})

	t.Run("toggle_diff", func(t *testing.T) {
		m := newTestHome(t)
		before := m.splitPane.IsDiffVisible()

		runDispatch(t, m, "d")
		assert.NotEqual(t, before, m.splitPane.IsDiffVisible(), "d must toggle diff visibility")
	})

	// workspace_prev/_next are no-ops when there's only one slot (the
	// test fixture's default). The goal here is parity of the dispatch
	// wiring, so assert only that the key is bound and the primitive
	// runs without error. The underlying slot-switching behavior has
	// its own coverage in ui/ and script/.
	for _, key := range []string{"[", "l", "]", ";"} {
		t.Run("workspace_"+key, func(t *testing.T) {
			m := newTestHome(t)
			runDispatch(t, m, key)
		})
	}
}

// mustAddInstance creates a Ready instance with the given title and
// registers it on m.list. Keeps the parity test terse.
func mustAddInstance(t *testing.T, m *home, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   title,
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = m.list.AddInstance(inst)
	return inst
}

// runDispatch dispatches key through the script engine and drains the
// scriptDoneMsg, asserting no error. Sync-primitive subtests care about
// the side-effect on m, not the (empty) intent slice.
func runDispatch(t *testing.T, m *home, key string) {
	t.Helper()
	cmd, ok := m.dispatchScript(key)
	require.True(t, ok, "key %q must be bound by defaults.lua", key)
	require.NotNil(t, cmd)

	msg, ok := cmd().(scriptDoneMsg)
	require.True(t, ok)
	require.NoError(t, msg.err)
}
