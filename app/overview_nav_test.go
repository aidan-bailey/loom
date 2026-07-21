package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestOverviewEnter_FocusesCursorSlotThenFocusMode(t *testing.T) {
	m := fleetHome(t)
	m.scripts = nil // enter is handled before script dispatch; engine unused here
	m.overviewCursor = overviewCursor{slot: 1, inst: 0}

	_, _ = handleStateDefaultKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, viewFocus, m.viewMode)
	assert.Equal(t, 1, m.focusedSlot, "enter committed the cross-workspace cursor")
}
