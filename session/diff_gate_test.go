package session

import (
	"github.com/aidan-bailey/loom/session/git"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestShouldRefreshDiff_Lifecycle covers the early-exit branches: not-started
// and Paused instances must never trigger a git subprocess, regardless of
// tmux activity.
func TestShouldRefreshDiff_Lifecycle(t *testing.T) {
	notStarted := &Instance{Title: "ns", Status: Ready}
	assert.False(t, notStarted.ShouldRefreshDiff(true, true))

	paused := &Instance{Title: "p", Status: Paused}
	paused.setStarted(true)
	assert.False(t, paused.ShouldRefreshDiff(true, true))
}

// TestShouldRefreshDiff_TmuxUpdate confirms the primary trigger: any tmux
// pane change requires a fresh diff fetch.
func TestShouldRefreshDiff_TmuxUpdate(t *testing.T) {
	inst := &Instance{Title: "t", Status: Running}
	inst.setStarted(true)
	inst.setDiffStats(&git.DiffStats{Added: 2, Removed: 1, Content: "diff"})

	assert.True(t, inst.ShouldRefreshDiff(true, false),
		"tmux change should always trigger a refresh")
	assert.True(t, inst.ShouldRefreshDiff(true, true))
}

// TestShouldRefreshDiff_FirstFetch verifies that an instance with no cached
// stats always refreshes on the first tick, even without tmux activity.
func TestShouldRefreshDiff_FirstFetch(t *testing.T) {
	inst := &Instance{Title: "t", Status: Running}
	inst.setStarted(true)

	assert.True(t, inst.ShouldRefreshDiff(false, false),
		"nil diff stats should trigger first-time fetch")
	assert.True(t, inst.ShouldRefreshDiff(false, true))
}

// TestShouldRefreshDiff_IdleSkip is the core optimisation: an idle instance
// with already-cached stats must skip the git call.
func TestShouldRefreshDiff_IdleSkip(t *testing.T) {
	inst := &Instance{Title: "t", Status: Running}
	inst.setStarted(true)

	// Cached short stats for a non-selected instance — list view only
	// needs counts, so no refresh until tmux changes.
	inst.setDiffStats(&git.DiffStats{Added: 3, Removed: 1})
	assert.False(t, inst.ShouldRefreshDiff(false, false),
		"non-selected idle instance with cached short stats should skip")

	// Cached full stats for a selected instance — content already present.
	inst.setDiffStats(&git.DiffStats{Added: 3, Removed: 1, Content: "diff"})
	assert.False(t, inst.ShouldRefreshDiff(false, true),
		"selected idle instance with cached full stats should skip")
}

// TestShouldRefreshDiff_SelectionUpgrade covers the short→full upgrade path
// that fires when the user selects an instance whose prior cache was
// short-stats only (Content==""). Empty diffs never trigger upgrade because
// short and full produce identical zero-state.
func TestShouldRefreshDiff_SelectionUpgrade(t *testing.T) {
	inst := &Instance{Title: "t", Status: Running}
	inst.setStarted(true)

	// Short-stats with real changes but no content — selecting this instance
	// requires a full diff fetch.
	inst.setDiffStats(&git.DiffStats{Added: 5, Removed: 2})
	assert.True(t, inst.ShouldRefreshDiff(false, true),
		"short-stats with changes should upgrade to full when selected")

	// Empty diff: short and full are equivalent (both zero/empty), so no
	// upgrade is needed even when wantFull=true.
	inst.setDiffStats(&git.DiffStats{})
	assert.False(t, inst.ShouldRefreshDiff(false, true),
		"empty short-stats does not require upgrade")
}
