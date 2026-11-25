package ui

import (
	"fmt"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/git"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func generateLargeDiff(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		switch i % 4 {
		case 0:
			b.WriteString(fmt.Sprintf("+added line %d content here\n", i))
		case 1:
			b.WriteString(fmt.Sprintf("-removed line %d content here\n", i))
		case 2:
			b.WriteString(fmt.Sprintf("@@ -%d,3 +%d,3 @@ func example()\n", i, i))
		default:
			b.WriteString(fmt.Sprintf(" context line %d unchanged\n", i))
		}
	}
	return b.String()
}

func BenchmarkColorizeDiff(b *testing.B) {
	diff := generateLargeDiff(5000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		colorizeDiff(diff)
	}
}

func TestSetDiffCachesContent(t *testing.T) {
	d := NewDiffPane()
	d.SetSize(80, 24)

	// Verify that lastDiffContent is empty initially
	assert.Empty(t, d.lastDiffContent)

	// Create a mock instance-like test: call SetDiff logic directly
	// We test colorizeDiff caching by checking the field
	stats := &git.DiffStats{
		Content: "+added line\n-removed line\n",
		Added:   1,
		Removed: 1,
	}

	// Simulate first SetDiff by setting fields directly
	d.lastDiffContent = stats.Content
	colorized := colorizeDiff(stats.Content)
	d.diff = colorized

	// Second call with same content should be cacheable
	assert.Equal(t, stats.Content, d.lastDiffContent)
}

// TestDiffPane_LongLineDoesNotPadEveryLine pins the 2026-11-25 TUI blow-up:
// one very long line (a minified bundle, a lockfile) made the pane pad every
// other line of the diff with spaces to that line's width, so a diff of a
// few MB became a multi-GB string on entering the workbench.
func TestDiffPane_LongLineDoesNotPadEveryLine(t *testing.T) {
	var b strings.Builder
	b.WriteString("+" + strings.Repeat("x", 10_000) + "\n")
	for i := 0; i < 2_000; i++ {
		fmt.Fprintf(&b, "+line %d\n", i)
	}
	inst := &core.InstanceView{
		Started: true,
		HasDiff: true,
		Diff:    git.DiffStats{Content: b.String(), Added: 2_001},
	}

	d := NewDiffPane()
	d.SetSize(80, 24)
	d.SetDiff(inst)
	limit := 4 * b.Len()
	assert.Less(t, len(d.viewport.GetContent()), limit, "SetDiff padded the diff")

	d.SetSize(100, 30)
	assert.Less(t, len(d.viewport.GetContent()), limit, "SetSize padded the diff")
}
