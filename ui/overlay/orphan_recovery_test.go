package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/session"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func newTestCandidates() []session.OrphanCandidate {
	return []session.OrphanCandidate{
		{
			WorktreePath: "/tmp/wt/aidanb/example-jupyter-notebook_dead0001",
			BranchName:   "aidanb/example-jupyter-notebook",
			Title:        "example jupyter notebook",
			HasLiveTmux:  true,
		},
		{
			WorktreePath: "/tmp/wt/aidanb/stale-feature_dead0002",
			BranchName:   "aidanb/stale-feature",
			Title:        "stale feature",
			HasLiveTmux:  false,
		},
	}
}

func TestOrphanRecoveryPickerDefaultsToLiveTmux(t *testing.T) {
	p := NewOrphanRecoveryPicker(newTestCandidates())
	// Live tmux candidate defaults to recover; dead one defaults to skip.
	assert.True(t, p.recover[0])
	assert.False(t, p.recover[1])
}

func TestOrphanRecoveryPickerNavigation(t *testing.T) {
	p := NewOrphanRecoveryPicker(newTestCandidates())
	assert.Equal(t, 0, p.cursor)

	p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	assert.Equal(t, 1, p.cursor)

	// Past end is clamped.
	p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	assert.Equal(t, 1, p.cursor)

	p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	assert.Equal(t, 0, p.cursor)

	// Above start is clamped.
	p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	assert.Equal(t, 0, p.cursor)
}

func TestOrphanRecoveryPickerToggle(t *testing.T) {
	p := NewOrphanRecoveryPicker(newTestCandidates())
	// Cursor at index 0 starts as recover=true.
	assert.True(t, p.recover[0])

	committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	assert.False(t, committed, "space toggles, does not commit")
	assert.False(t, p.recover[0], "space flips the cursor's recover bit")

	committed, _ = p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, committed, "enter behaves like space")
	assert.True(t, p.recover[0], "enter flips it back")
}

func TestOrphanRecoveryPickerSelectAll(t *testing.T) {
	p := NewOrphanRecoveryPicker(newTestCandidates())
	// Mixed initial state: index 0 = true, index 1 = false.
	committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	assert.False(t, committed)
	for _, v := range p.recover {
		assert.True(t, v)
	}
}

func TestOrphanRecoveryPickerSkipAll(t *testing.T) {
	p := NewOrphanRecoveryPicker(newTestCandidates())
	committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	assert.False(t, committed)
	for _, v := range p.recover {
		assert.False(t, v)
	}
}

func TestOrphanRecoveryPickerCommit(t *testing.T) {
	p := NewOrphanRecoveryPicker(newTestCandidates())

	committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
	assert.True(t, committed)

	p2 := NewOrphanRecoveryPicker(newTestCandidates())
	committed2, _ := p2.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	assert.True(t, committed2)
}

func TestOrphanRecoveryPickerSelected(t *testing.T) {
	p := NewOrphanRecoveryPicker(newTestCandidates())

	selected := p.Selected()
	assert.Len(t, selected, 1, "only the live-tmux candidate is selected by default")
	assert.Equal(t, "aidanb/example-jupyter-notebook", selected[0].BranchName)

	skipped := p.Skipped()
	assert.Len(t, skipped, 1)
	assert.Equal(t, "aidanb/stale-feature", skipped[0].BranchName)
}

func TestOrphanRecoveryPickerRender(t *testing.T) {
	p := NewOrphanRecoveryPicker(newTestCandidates())
	out := p.Render()
	// Title and surfaced metadata must appear so the user can audit.
	assert.Contains(t, out, "Recover orphan worktrees")
	assert.Contains(t, out, "example jupyter notebook")
	assert.Contains(t, out, "aidanb/example-jupyter-notebook")
	assert.Contains(t, out, "stale-feature_dead0002")
	assert.Contains(t, out, "tmux alive")
}

func TestOrphanRecoveryPickerEmptyCandidates(t *testing.T) {
	// Defensive: 0 candidates shouldn't crash. The app shouldn't open
	// the overlay in this case, but a no-op render is the safe
	// fallback.
	p := NewOrphanRecoveryPicker(nil)
	assert.NotPanics(t, func() {
		_ = p.Render()
	})
	assert.Empty(t, p.Selected())
	assert.Empty(t, p.Skipped())
}

// Compile-time check that OrphanRecoveryPicker satisfies the Overlay
// interface — same pattern used in iface_test.go for other overlays.
var _ Overlay = (*OrphanRecoveryPicker)(nil)
