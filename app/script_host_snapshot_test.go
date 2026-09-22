package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	"charm.land/bubbles/v2/spinner"
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
		m.list.AddInstance(newSnapshotTestInstance(t, title))
	}
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
// part of Update: mutating the list and swapping m.list, m.splitPane and
// m.activeCtx the way loadSlot does. The host's reads must come from a
// snapshot taken in dispatchScript, not from the live model. Must pass
// under `go test -race`.
func TestScriptHost_ReadsDoNotRaceUpdate(t *testing.T) {
	m := newSnapshotTestHome(t)

	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	altList := ui.NewList(&sp)
	altList.AddInstance(newSnapshotTestInstance(t, "other"))
	altSplit := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	ctxA := &config.WorkspaceContext{ConfigDir: t.TempDir()}
	ctxB := &config.WorkspaceContext{ConfigDir: t.TempDir()}
	extra := newSnapshotTestInstance(t, "extra")

	cmd, ok := m.dispatchScript("X")
	require.True(t, ok)

	done := make(chan tea.Msg)
	go func() { done <- cmd() }()

	var msg tea.Msg
	for i := 0; msg == nil; i++ {
		select {
		case msg = <-done:
		default:
			m.list.AddInstance(extra)
			m.list.SetSelectedInstance(i % 4)
			m.list.RemoveInstance(extra)
			m.list, altList = altList, m.list
			m.splitPane, altSplit = altSplit, m.splitPane
			if i%2 == 0 {
				m.activeCtx = ctxA
			} else {
				m.activeCtx = ctxB
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
