package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopChromeHeight_CountsTheStrip(t *testing.T) {
	m := newTestHome(t)
	m.accountStrip = ui.NewAccountStrip()
	withAccounts(t, m)
	m.refreshAccountViews()
	base := m.topChromeHeight()
	assert.Equal(t, m.tabBar.Height(), base, "no extra account: no strip row")

	withAccounts(t, m, "max-2")
	m.refreshAccountViews()
	assert.Equal(t, base+1, m.topChromeHeight())
}

// TestView_StripIsTheTopRowLeftAligned: the strip is narrower than the
// screen, and View joins its sections centered; it must still start at
// the left edge of the first row.
func TestView_StripIsTheTopRowLeftAligned(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.refreshAccountViews()
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})

	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	require.NotEmpty(t, lines)
	assert.True(t, strings.HasPrefix(lines[0], " *default"), "first row: %q", lines[0])
	assert.Len(t, lines, 40, "the strip row is taken out of the content height")
}
