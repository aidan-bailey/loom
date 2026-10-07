package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotTestScript binds two user keys: X hammers the host's read
// methods (plus send_terminal_keys, which reaches the split pane) and Y
// reports the selected instance's title through a notice.
const snapshotTestScript = `
cs.bind("X", function(ctx)
  for i = 1, 200 do
    local sel = ctx:selected()
    local all = ctx:instances()
    local dir = ctx:config_dir()
    if sel then pcall(sel.send_terminal_keys, sel, "x") end
  end
end)

cs.bind("Y", function(ctx)
  local sel = ctx:selected()
  ctx:notify(sel and sel:title() or "none")
end)
`

// newSnapshotTestHome builds a test home whose engine has
// snapshotTestScript loaded as a user script, with instances a, b and c
// in the list and a selected. The instances are never started, so no
// tmux session is involved.
func newSnapshotTestHome(t *testing.T) *home {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "snapshot.lua"), []byte(snapshotTestScript), 0o644))

	m := newTestHome(t)
	m.scripts = nil
	initScriptsIn(m, dir, false)
	require.True(t, m.scripts.HasAction("X"))
	require.True(t, m.scripts.HasAction("Y"))

	for _, title := range []string{"a", "b", "c"} {
		m.ws.Add(newSnapshotTestInstance(t, title))
	}
	m.syncViews()
	m.list.SetSelectedInstance(0)
	return m
}

func newSnapshotTestInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   title,
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	return inst
}

// TestScriptHost_ReadsDoNotRaceUpdate runs a user script's read-heavy
// handler in the dispatch Cmd goroutine while this goroutine plays the
// part of Update: mutating the list, swapping the embedded focused slot
// the way loadSlot does, and rewriting the focused slot's workspace (which
// carries its wsCtx). The host's reads must come from a snapshot taken in
// dispatchScript, not from the live model. Must pass under `go test -race`.
func TestScriptHost_ReadsDoNotRaceUpdate(t *testing.T) {
	m := newSnapshotTestHome(t)

	ctxA := &config.WorkspaceContext{ConfigDir: t.TempDir()}
	ctxB := &config.WorkspaceContext{ConfigDir: t.TempDir()}
	wsB := testWS(core.WorkspaceParts{Ctx: ctxB, Config: config.DefaultConfig()}, newSnapshotTestInstance(t, "other"))
	altList := fixtureList(t)
	altSplit := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	extra := newSnapshotTestInstance(t, "extra")
	reworkspace(t, m, m.workspaceSlot, func(p *core.WorkspaceParts) { p.Ctx = ctxA })
	slotA, wsA := m.workspaceSlot, m.ws
	slotB := &workspaceSlot{ws: wsB, list: altList, splitPane: altSplit}

	cmd, ok := m.dispatchScript("X")
	require.True(t, ok)

	done := make(chan tea.Msg)
	go func() { done <- cmd() }()

	var msg tea.Msg
	for i := 0; msg == nil; i++ {
		select {
		case msg = <-done:
		default:
			m.ws.Add(extra)
			m.syncViews()
			m.list.SetSelectedInstance(i % 4)
			m.ws.Remove(extra)
			if i%2 == 0 {
				m.workspaceSlot = slotB
				m.ws = wsB
			} else {
				m.workspaceSlot = slotA
				m.ws = wsA
			}
		}
	}

	res, ok := msg.(scriptDoneMsg)
	require.True(t, ok, "dispatch Cmd must produce a scriptDoneMsg, got %T", msg)
	assert.NoError(t, res.err)
	assert.Empty(t, res.notices)
}

// TestScriptHost_SelectionIsDispatchTimeSnapshot pins the snapshot
// semantics: ctx:selected() returns the instance selected when
// dispatchScript ran, even if Update moves the selection before the
// Cmd executes.
func TestScriptHost_SelectionIsDispatchTimeSnapshot(t *testing.T) {
	m := newSnapshotTestHome(t)

	cmd, ok := m.dispatchScript("Y")
	require.True(t, ok)
	m.list.SetSelectedInstance(1)

	done, ok := cmd().(scriptDoneMsg)
	require.True(t, ok)
	require.NoError(t, done.err)
	assert.Equal(t, []string{"a"}, done.notices)
}
