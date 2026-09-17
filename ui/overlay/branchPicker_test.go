package overlay

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBranchPicker_NewBranchLabelReflectsBase pins that the "new branch" row
// names the branch the session will actually be cut from. It used to claim
// "from HEAD", which stopped being true once the base became configurable.
func TestBranchPicker_NewBranchLabelReflectsBase(t *testing.T) {
	bp := NewBranchPicker()
	bp.SetWidth(60)

	// Before the async resolve lands, no base is claimed at all.
	assert.Contains(t, bp.Render(), "New branch")
	assert.NotContains(t, bp.Render(), "from")

	bp.SetBaseBranchName("develop")
	assert.Contains(t, bp.Render(), "New branch (from develop)")
}

// TestBranchPicker_NewBranchSentinelSurvivesRelabel guards the subtle part:
// the label is display-only, so selecting the row must still report "" (cut a
// new branch) rather than a branch literally named after the label.
func TestBranchPicker_NewBranchSentinelSurvivesRelabel(t *testing.T) {
	bp := NewBranchPicker()
	bp.SetBaseBranchName("main")
	bp.SetResults([]string{"feature/x"}, bp.GetFilterVersion())

	require.Equal(t, "", bp.GetSelectedBranch(), "cursor 0 is the new-branch row")

	rendered := bp.Render()
	assert.Contains(t, rendered, "New branch (from main)")
	assert.True(t, strings.Contains(rendered, "feature/x"))
}
