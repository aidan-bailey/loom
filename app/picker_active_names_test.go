package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
)

func TestPickerActiveNames_ExcludesBackground(t *testing.T) {
	m := &home{
		focusedSlot: 0,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "fg"}},
			{wsCtx: &config.WorkspaceContext{Name: "bg"}, background: true},
		},
	}
	active := m.pickerActiveNames()
	assert.True(t, active["fg"], "foreground slot pre-checked")
	assert.False(t, active["bg"], "background slot NOT pre-checked (else opening the picker would promote it)")
	assert.Len(t, active, 1)
}
