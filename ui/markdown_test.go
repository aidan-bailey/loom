package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMDPane() *MarkdownPane {
	p := NewMarkdownPane()
	p.SetSize(60, 20)
	return p
}

func TestMarkdownPane_StartsFollowingAndEmpty(t *testing.T) {
	p := newTestMDPane()
	assert.True(t, p.Following())
	assert.Contains(t, p.View(), "no markdown", "empty state must say so")
}

func TestMarkdownPane_RendersHeadingAndScrolls(t *testing.T) {
	p := newTestMDPane()
	var b strings.Builder
	b.WriteString("# Title\n\n")
	for i := 0; i < 100; i++ {
		b.WriteString("line\n\n")
	}
	p.SetDocument("/tmp/x/plan.md", b.String(), time.Now())

	top := p.View()
	assert.Contains(t, top, "Title")
	assert.Contains(t, top, "plan.md", "header shows the basename")

	p.ScrollBottom()
	assert.NotEqual(t, top, p.View(), "scrolling must change the window")
	p.ScrollTop()
	assert.Equal(t, top, p.View())
}

func TestMarkdownPane_PathChangeResetsScroll_SamePathPreservesIt(t *testing.T) {
	p := newTestMDPane()
	long := strings.Repeat("para\n\n", 200)
	p.SetDocument("/tmp/a.md", long, time.Now())
	p.ScrollBottom()
	require.NotEqual(t, 0, p.Scroll())

	p.SetDocument("/tmp/a.md", long+"tail\n", time.Now())
	assert.NotEqual(t, 0, p.Scroll(), "same-path reload keeps scroll")

	p.SetDocument("/tmp/b.md", long, time.Now())
	assert.Equal(t, 0, p.Scroll(), "new path resets scroll")
}

func TestMarkdownPane_TruncatesHugeDocs(t *testing.T) {
	p := newTestMDPane()
	p.SetDocument("/tmp/big.md", strings.Repeat("a", MarkdownMaxBytes+100), time.Now())
	p.ScrollBottom()
	assert.Contains(t, p.View(), "truncated")
}

func TestMarkdownPane_ReRendersOnThemeChange(t *testing.T) {
	ApplyTheme("afterglow")
	p := newTestMDPane()
	p.SetDocument("/tmp/x.md", "# Hi\n", time.Now())
	before := p.View()
	ApplyTheme("legacy")
	t.Cleanup(func() { ApplyTheme("afterglow") })
	assert.NotEqual(t, before, p.View(), "stale generation must trigger re-render")
}
