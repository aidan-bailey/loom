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
		spinner:     s,
		list:        focused,
		focusedSlot: 1,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "zebra"}, list: mk("z1"), background: true},
			{wsCtx: &config.WorkspaceContext{Name: "focused"}, list: focused},
			{wsCtx: &config.WorkspaceContext{Name: "apple"}, list: mk("a1"), background: true},
		},
		fleetLoading:    map[string]bool{"loadingws": true},
		fleetLoadErrors: map[string]error{"errws": assertErr},
	}
	d := m.overviewData()
	names := make([]string, len(d.Groups))
	states := make([]ui.GroupState, len(d.Groups))
	for i, g := range d.Groups {
		names[i], states[i] = g.Name, g.State
	}
	// focused first, then alpha (apple, zebra), then extras alpha (errws, loadingws).
	assert.Equal(t, []string{"focused", "apple", "zebra", "errws", "loadingws"}, names)
	assert.Equal(t, ui.GroupError, states[3])
	assert.Equal(t, ui.GroupLoading, states[4])
}
