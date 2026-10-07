package app

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
)

func TestOverviewData_GroupsFocusedFirstThenAlpha(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	mk := func(name, title string) *workspaceSlot {
		ws := testWS(core.WorkspaceParts{Ctx: &config.WorkspaceContext{Name: name}}, &session.Instance{Title: title, Status: session.Ready})
		return slotWith(ws, &workspaceSlot{list: fixtureList(t)})
	}
	focused := mk("focused", "f1")
	m := &home{
		spinner:  s,
		overview: ui.NewOverview(), // overviewData normalizes the cursor via fleetOrder
	}
	focusSlots(m, 1,
		mk("zebra", "z1"),
		focused,
		mk("apple", "a1"),
	)
	wireCore(t, m)
	d := m.overviewData()
	names := make([]string, len(d.Groups))
	for i, g := range d.Groups {
		names[i] = g.Name
	}
	// Focused first, then the rest alphabetical.
	assert.Equal(t, []string{"focused", "apple", "zebra"}, names)
}
