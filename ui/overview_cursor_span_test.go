package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A render cursor pointing into a block shorter than a card row (e.g. a
// collapsed group's 1-line header) must clamp its span to the block's
// own extent — an out-of-block span drives window() to a bogus offset.
// The app layer re-anchors such cursors, so this is defense in depth.
func TestCursorLineSpan_ClampsToShortBlock(t *testing.T) {
	o := NewOverview()
	o.SetSize(40, 6)
	blocks := []string{"▸ A · 3", "▾ B · 1\nrow\nrow"}
	d := OverviewData{Cursor: OverviewCursor{Group: 0, Item: 2}}

	top, bottom := o.cursorLineSpan(blocks, d)

	assert.Equal(t, 0, top, "span clamped to the collapsed block's only line")
	assert.Equal(t, 0, bottom)
}
