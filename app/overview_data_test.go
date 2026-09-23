package app

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
)

func TestOverviewData_GroupsFocusedFirstThenAlpha(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	mk := func(title string) *ui.List {
		l := ui.NewList(&s)
		l.AddInstance(&session.Instance{Title: title, Status: session.Ready})
		return l
	}
	focused := mk("f1")
	m := &home{
		spinner:  s,
		overview: ui.NewOverview(), // overviewData normalizes the cursor via fleetOrder
	}
	focusSlots(m, 1,
		&workspaceSlot{wsCtx: &config.WorkspaceContext{Name: "zebra"}, list: mk("z1")},
		&workspaceSlot{wsCtx: &config.WorkspaceContext{Name: "focused"}, list: focused},
		&workspaceSlot{wsCtx: &config.WorkspaceContext{Name: "apple"}, list: mk("a1")},
	)
	d := m.overviewData()
	names := make([]string, len(d.Groups))
	for i, g := range d.Groups {
		names[i] = g.Name
	}
	// Focused first, then the rest alphabetical.
	assert.Equal(t, []string{"focused", "apple", "zebra"}, names)
}
