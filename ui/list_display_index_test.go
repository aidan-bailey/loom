package ui

import (
	"testing"

	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisplayIndex_NoWorkspaceTerminal(t *testing.T) {
	items := []*session.Instance{
		{Title: "a"},
		{Title: "b"},
		{Title: "c"},
	}
	assert.Equal(t, 1, DisplayIndex(items, 0))
	assert.Equal(t, 2, DisplayIndex(items, 1))
	assert.Equal(t, 3, DisplayIndex(items, 2))
}

func TestDisplayIndex_LeadingWorkspaceTerminalOffsetsTheRest(t *testing.T) {
	items := []*session.Instance{
		{Title: "root", IsWorkspaceTerminal: true},
		{Title: "a"},
		{Title: "b"},
	}
	assert.Equal(t, 0, DisplayIndex(items, 0), "workspace terminal is numbered 0")
	assert.Equal(t, 1, DisplayIndex(items, 1))
	assert.Equal(t, 2, DisplayIndex(items, 2))
}

func TestDisplayIndex_EmptyItems(t *testing.T) {
	require.NotPanics(t, func() {
		DisplayIndex(nil, 0)
	})
}
