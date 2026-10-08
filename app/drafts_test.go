package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rowTitles is the titles of s's rows as its rail shows them.
func rowTitles(m *home, s *workspaceSlot) []string {
	var titles []string
	for _, v := range m.rowsOf(s) {
		titles = append(titles, v.Title)
	}
	return titles
}

// TestDraft_ShowsOnlyInItsOwnSlot: a draft is a row of the slot its flow
// opened in, and of no other, even once another slot is focused.
func TestDraft_ShowsOnlyInItsOwnSlot(t *testing.T) {
	m := fleetHome(t)
	m.viewMode = viewFocus
	owner, peer := m.slots[0], m.slots[1]
	require.Same(t, owner, m.workspaceSlot, "fixture: the first slot is focused")

	d := m.newDraft("pending", "", 0)

	assert.Equal(t, []string{"f1", "f2", "pending"}, rowTitles(m, owner))
	assert.Equal(t, []string{"b1"}, rowTitles(m, peer), "no other slot shows the draft")
	assert.Equal(t, core.InstanceID(0), selID(owner.list), "the draft's row is selected")

	m.loadSlot(1)
	assert.Equal(t, []string{"b1"}, rowTitles(m, m.workspaceSlot), "focusing another slot doesn't move the draft there")
	v, s := m.viewByID(0)
	require.NotNil(t, v)
	assert.Same(t, owner, s, "the draft is found in its own slot")
	assert.Equal(t, d.title, v.Title)
}

// TestConfirmDraft_SelectsTheCreatedRow: confirming sends Create; the new
// session's row replaces the draft's and is selected, and the model holds
// the session with the draft's title, prompt and composed program.
func TestConfirmDraft_SelectsTheCreatedRow(t *testing.T) {
	m := newTestHome(t)
	mustAddInstance(t, m, "a")
	d := m.newDraft("fresh", "do it", 0)
	d.path, d.program = t.TempDir(), "claude"

	m.confirmDraft(d, overlay.LaunchOptions{PermissionMode: "acceptEdits", Model: "default"})
	m.drainCore() // the Reply; the start's job is not run

	assert.Nil(t, m.draft)
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	assert.NotZero(t, sel.ID, "a session's row, not the draft's")
	assert.Equal(t, "fresh", sel.Title)
	assert.Equal(t, session.Loading, sel.Status, "the start is under way")
	inst := testModel(m).InstanceForTest(sel.ID)
	require.NotNil(t, inst)
	assert.Equal(t, "do it", inst.Prompt())
	assert.Equal(t, "claude --permission-mode acceptEdits", inst.Program())
	assert.Empty(t, m.pending, "the Reply was handled")
}

// TestCreateRefused_LeavesNoResidue: a draft whose workspace closed before
// the confirm is refused by the model; the user is told, and no row, draft
// or pending request is left behind.
func TestCreateRefused_LeavesNoResidue(t *testing.T) {
	m := newTestHome(t)
	m.errBox.SetSize(400, 1)
	before := m.list.NumInstances()
	d := m.newDraft("orphaned", "", 0)
	d.slot = slotWith(testWS(core.WorkspaceParts{}), &workspaceSlot{list: m.list}) // a workspace no longer loaded

	m.confirmDraft(d, overlay.LaunchOptions{})
	m.drainCore()

	assert.Contains(t, m.errBox.String(), "create orphaned")
	assert.Equal(t, before, m.list.NumInstances(), "no row is left")
	assert.Nil(t, m.draft)
	assert.Empty(t, m.pending)
	assert.Nil(t, m.list.GetInstanceByTitle("orphaned"))
}

// TestDraftsAndScripts_DefaultToTheTUIsProgram: a draft, and a script's
// ctx:new_instance, launch the program this TUI started with (-p, else the
// startup workspace's), which follows only this TUI's own settings saves:
// neither the focused workspace's config nor the model's program.
func TestDraftsAndScripts_DefaultToTheTUIsProgram(t *testing.T) {
	m := newTestHome(t)
	m.program = "tui-agent"
	require.NotEqual(t, m.program, m.settings().GetProgram(), "fixture: the workspace's program differs")

	assert.Equal(t, "tui-agent", m.newDraft("", "", 0).program)
	assert.Equal(t, "tui-agent", newScriptHost(m).DefaultProgram())
}
