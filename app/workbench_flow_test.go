package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/ui"
)

// TestWbScanMsg_NewFileTriggersLoad pins the follow pipeline's happy
// path: a scan result for the selected session with a not-yet-loaded
// path dispatches a load Cmd.
func TestWbScanMsg_NewFileTriggersLoad(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	_, cmd := m.Update(wbScanMsg{title: sel.Title, path: "/tmp/plan.md", mtime: time.Now()})
	assert.NotNil(t, cmd, "a new most-recent file must dispatch a load")
}

// TestWbScanMsg_IgnoredWhenPinnedEditingOrStale pins the three drop
// guards: stale titles (selection moved), pinned panes (follow off),
// and open editors all swallow the scan result.
func TestWbScanMsg_IgnoredWhenPinnedEditingOrStale(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)

	_, cmd := m.Update(wbScanMsg{title: "other", path: "/tmp/a.md", mtime: time.Now()})
	assert.Nil(t, cmd, "stale-title scan must be dropped")

	m.workbench.Markdown.SetFollowing(false)
	_, cmd = m.Update(wbScanMsg{title: sel.Title, path: "/tmp/a.md", mtime: time.Now()})
	assert.Nil(t, cmd, "pinned pane must ignore scans")
	m.workbench.Markdown.SetFollowing(true)

	m.workbench.Markdown.SetDocument("/tmp/a.md", "x", time.Now())
	require.True(t, m.workbench.Markdown.StartEdit())
	_, cmd = m.Update(wbScanMsg{title: sel.Title, path: "/tmp/b.md", mtime: time.Now()})
	assert.Nil(t, cmd, "an open editor must never be clobbered by follow")
}

// TestWbLoadMsg_AppliesDocument pins load delivery: the document lands
// in the markdown pane and follow mode is recorded.
func TestWbLoadMsg_AppliesDocument(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	now := time.Now()
	m.Update(wbLoadMsg{title: sel.Title, path: "/tmp/plan.md", raw: "# Plan\n", mtime: now, follow: true})
	assert.Equal(t, "/tmp/plan.md", m.workbench.Markdown.Path())
	assert.True(t, m.workbench.Markdown.Following())
}

// TestLoadMarkdownCmd_ReadsFile pins the load Cmd's disk read: raw
// content, mtime, and the follow flag ride back on wbLoadMsg.
func TestLoadMarkdownCmd_ReadsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(p, []byte("# Doc\n"), 0o644))
	msg := loadMarkdownCmd("sess", p, true)()
	lm, ok := msg.(wbLoadMsg)
	require.True(t, ok)
	assert.NoError(t, lm.err)
	assert.Equal(t, "# Doc\n", lm.raw)
	assert.True(t, lm.follow)
}

// TestWorkbench_FlushRatioKeysOffWorkbenchSession pins the review fix:
// the ratio persists under the session the workbench is showing, not
// whatever the list cursor happens to sit on at flush time.
func TestWorkbench_FlushRatioKeysOffWorkbenchSession(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	mustAddInstance(t, m, "b")
	m.list.SetSelectedInstance(0)
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)

	m.wbRatio = 0.7
	// Selection moves without the workbench retargeting (e.g. a raw
	// cursor move during teardown ordering).
	m.list.SetSelectedInstance(1)
	m.flushWorkbenchRatio()

	prefs := m.appState.GetUIPrefs()
	assert.Equal(t, 0.7, prefs.WorkbenchRatios["a"],
		"ratio must persist under the workbench's session title")
	_, hasB := prefs.WorkbenchRatios["b"]
	assert.False(t, hasB, "the merely-selected instance must not receive the ratio")
}

// TestWorkbench_DeadSelectionDropsToFocus pins the heal: killing the
// last instance while deep-diving lands in focus mode, not a dead panel.
func TestWorkbench_DeadSelectionDropsToFocus(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)

	m.list.RemoveInstanceByTitle("a")
	require.Nil(t, m.list.GetSelectedInstance())
	_ = m.instanceChanged()
	assert.Equal(t, viewFocus, m.viewMode,
		"workbench must not survive losing its instance")
	assert.False(t, m.splitPane.IsTerminalHidden(),
		"heal must restore the pre-entry terminal setting")
}

// TestWorkbench_FilesTabTopBottomRouting pins per-tab g/G routing on
// the files tab: G lands on the last entry, g back on the first.
func TestWorkbench_FilesTabTopBottomRouting(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	m.workbench.SetFiles("/tmp/root", []string{"a.md", "b.txt", "c.md"})
	m.workbench.SetTab(ui.WbTabFiles)

	_, _, handled := handleWorkbenchKey(m, wbKey("G"))
	require.True(t, handled)
	path, ok := m.workbench.FileUnderCursor()
	require.True(t, ok)
	assert.Equal(t, filepath.Join("/tmp/root", "c.md"), path)

	_, _, handled = handleWorkbenchKey(m, wbKey("g"))
	require.True(t, handled)
	path, ok = m.workbench.FileUnderCursor()
	require.True(t, ok)
	assert.Equal(t, filepath.Join("/tmp/root", "a.md"), path)
}

// TestWorkbench_TerminalKeysSwitchTab pins the review fix: the
// terminal-addressed keys flip the visible tab to terminal before the
// underlying attach/quick-input action dispatches.
func TestWorkbench_TerminalKeysSwitchTab(t *testing.T) {
	for _, k := range []string{"t", "ctrl+t", "alt+t"} {
		m := newWorkbenchTestHome(t)
		mustAddInstance(t, m, "a")
		_, _ = handleStateDefaultKey(m, wbKey("enter"))
		require.Equal(t, ui.WbTabMarkdown, m.workbench.Tab())

		var msg tea.KeyPressMsg
		switch k {
		case "ctrl+t":
			msg = tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
		case "alt+t":
			msg = tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt}
		default:
			msg = wbKey(k)
		}
		_, _, handled := handleWorkbenchKey(m, msg)
		assert.True(t, handled, "key %q must be intercepted in workbench", k)
		assert.Equal(t, ui.WbTabTerminal, m.workbench.Tab(),
			"key %q must switch the visible tab to terminal", k)
	}
}

// TestWorkbench_TabToOverviewPersistsViewMode pins persistence parity:
// hopping workbench→overview records "overview" the same way the
// focus-mode toggle does.
func TestWorkbench_TabToOverviewPersistsViewMode(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	_, _ = handleStateDefaultKey(m, wbKey("tab"))
	require.Equal(t, viewOverview, m.viewMode)
	assert.Equal(t, "overview", m.appState.GetUIPrefs().ViewMode)
}

// TestWorkbench_EnterDismissesDiffOverlay pins entry hygiene: a
// visible focus-mode diff overlay is dismissed on entering workbench
// (the workbench has its own diff tab).
func TestWorkbench_EnterDismissesDiffOverlay(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	m.splitPane.ToggleDiff()
	require.True(t, m.splitPane.IsDiffVisible())

	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)
	assert.False(t, m.splitPane.IsDiffVisible())
}

// TestSaveMarkdownCmd_CleanSaveWrites pins the happy path: no
// concurrent disk change, so the buffer lands on disk and the fresh
// mtime rides back for the new conflict baseline.
func TestSaveMarkdownCmd_CleanSaveWrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))
	info, err := os.Stat(p)
	require.NoError(t, err)

	msg := saveMarkdownCmd("s", p, "new", info.ModTime(), false)()
	sm, ok := msg.(wbSaveMsg)
	require.True(t, ok)
	assert.NoError(t, sm.err)
	assert.False(t, sm.conflict)
	got, _ := os.ReadFile(p)
	assert.Equal(t, "new", string(got))
	assert.False(t, sm.mtime.IsZero(), "fresh mtime must ride back as the new baseline")
}

// TestSaveMarkdownCmd_ConflictDetected pins the guard: disk newer than
// our loaded mtime and force off → nothing written, conflict flagged.
func TestSaveMarkdownCmd_ConflictDetected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))
	loaded := time.Now().Add(-time.Hour) // disk is newer than our load

	msg := saveMarkdownCmd("s", p, "new", loaded, false)()
	sm := msg.(wbSaveMsg)
	assert.True(t, sm.conflict)
	got, _ := os.ReadFile(p)
	assert.Equal(t, "old", string(got), "conflicted save must not write")
}

// TestSaveMarkdownCmd_ForceOverwrites pins the confirm path: force
// skips the mtime check entirely and writes.
func TestSaveMarkdownCmd_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))

	msg := saveMarkdownCmd("s", p, "new", time.Now().Add(-time.Hour), true)()
	sm := msg.(wbSaveMsg)
	assert.NoError(t, sm.err)
	assert.False(t, sm.conflict)
	got, _ := os.ReadFile(p)
	assert.Equal(t, "new", string(got))
}

// TestWbSaveMsg_SuccessExitsEditMode pins save delivery: a successful
// save adopts the content as the new baseline and drops the pane back
// to the rendered view.
func TestWbSaveMsg_SuccessExitsEditMode(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	md := m.workbench.Markdown
	md.SetDocument("/tmp/doc.md", "old", time.Now())
	require.True(t, md.StartEdit())

	m.Update(wbSaveMsg{title: sel.Title, path: "/tmp/doc.md", content: "new", mtime: time.Now()})
	assert.False(t, md.Editing(), "successful save drops back to the rendered view")
	assert.False(t, md.EditDirty(), "saved content becomes the new baseline")
}

// TestWbSaveMsg_StalePathDropped pins the retarget guard: a save
// result for a document the pane is no longer showing must not clobber
// the current document's baseline.
func TestWbSaveMsg_StalePathDropped(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	md := m.workbench.Markdown
	md.SetDocument("/tmp/other.md", "current", time.Now())

	m.Update(wbSaveMsg{title: sel.Title, path: "/tmp/doc.md", content: "new", mtime: time.Now()})
	assert.Equal(t, "/tmp/other.md", md.Path(), "stale save must not retarget the pane")
}

// TestWbSaveMsg_ConflictOpensConfirm pins the conflict surface: the
// message flips the app into the confirmation state with a force-save
// queued rather than silently overwriting.
func TestWbSaveMsg_ConflictOpensConfirm(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)

	m.Update(wbSaveMsg{title: sel.Title, path: "/tmp/doc.md", content: "new", conflict: true})
	assert.Equal(t, stateConfirm, m.state, "conflict must ask before overwriting")
	assert.NotNil(t, m.pendingConfirmation.Async, "confirm must carry the force save")
}

// TestWbSaveMsg_ConflictDroppedWhenConfirmAlreadyOpen carries forward the
// Task 10 review fix: a save conflict must never clobber an already-open
// confirmation (e.g. the esc discard-edit confirm) — otherwise the user's
// next "yes" press would silently execute a force overwrite instead of
// the discard they actually confirmed.
func TestWbSaveMsg_ConflictDroppedWhenConfirmAlreadyOpen(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)

	md := m.workbench.Markdown
	md.SetDocument("/tmp/doc.md", "old", time.Now())
	require.True(t, md.StartEdit())
	md.HandleEditKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.True(t, md.EditDirty(), "edit must be dirty to trigger the discard confirm")

	_, _ = handleStateDefaultKey(m, wbKey("esc"))
	require.Equal(t, stateConfirm, m.state, "esc on a dirty edit must open the discard confirm")
	require.Nil(t, m.pendingConfirmation.Async, "discard confirm carries only a Sync step")
	require.NotNil(t, m.pendingConfirmation.Sync, "discard confirm must have a pending Sync step")
	require.True(t, md.Editing(), "edit stays open until the user actually confirms")

	m.Update(wbSaveMsg{title: sel.Title, path: "/tmp/other.md", content: "new", conflict: true})

	assert.Equal(t, stateConfirm, m.state,
		"the open discard confirm must survive an incoming save conflict")
	assert.Nil(t, m.pendingConfirmation.Async,
		"the conflict must not smuggle a force-save Async step into the open confirm")
	assert.True(t, md.Editing(),
		"the pending discard confirm must remain armed, not silently resolved")
}

// TestWbLoadMsg_FollowWhilePinnedDropped pins the Task 9 review fix: a
// stale in-flight follow load must not clobber (and un-pin) a document
// the user just pinned.
func TestWbLoadMsg_FollowWhilePinnedDropped(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	md := m.workbench.Markdown
	md.SetDocument("/tmp/pinned.md", "pinned", time.Now())
	md.SetFollowing(false)

	m.Update(wbLoadMsg{title: sel.Title, path: "/tmp/follow.md", raw: "x", mtime: time.Now(), follow: true})
	assert.Equal(t, "/tmp/pinned.md", md.Path(), "stale follow load must not clobber a pinned document")
	assert.False(t, md.Following(), "stale follow load must not un-pin the pane")
}

// TestWbLoadMsg_ErrorWhileEditingKeepsPane pins the Task 9 review fix:
// a load error landing while the editor is open must not clear the
// pane out from under the edit buffer.
func TestWbLoadMsg_ErrorWhileEditingKeepsPane(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	md := m.workbench.Markdown
	md.SetDocument("/tmp/doc.md", "old", time.Now())
	require.True(t, md.StartEdit())

	m.Update(wbLoadMsg{title: sel.Title, path: "/tmp/doc.md", follow: true, err: os.ErrNotExist})
	assert.True(t, md.Editing(), "load error must not cancel an open editor")
	assert.Equal(t, "/tmp/doc.md", md.Path(), "load error must not clear the pane under an open editor")
}
