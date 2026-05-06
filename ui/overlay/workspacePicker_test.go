package overlay

import (
	"github.com/aidan-bailey/loom/config"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestWorkspacePickerNavigation(t *testing.T) {
	workspaces := []config.Workspace{
		{Name: "alpha", Path: "/a"},
		{Name: "beta", Path: "/b"},
	}
	active := map[string]bool{"alpha": true}

	t.Run("starts at first item", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		assert.Equal(t, 0, p.cursor)
	})

	t.Run("moves down", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		assert.Equal(t, 1, p.cursor)
	})

	t.Run("does not go below last item", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		assert.Equal(t, 1, p.cursor)
	})

	t.Run("moves up", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		assert.Equal(t, 0, p.cursor)
	})

	t.Run("does not go above first item", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		assert.Equal(t, 0, p.cursor)
	})
}

func TestWorkspacePickerToggle(t *testing.T) {
	workspaces := []config.Workspace{
		{Name: "alpha", Path: "/a"},
		{Name: "beta", Path: "/b"},
	}
	active := map[string]bool{"alpha": true}

	t.Run("space toggles active state", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		// Alpha is active, toggle it off
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
		assert.False(t, committed)
		result := p.GetActiveWorkspaces()
		assert.Empty(t, result)
	})

	t.Run("enter toggles active state", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		// Move to beta and toggle it on
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
		assert.False(t, committed)
		result := p.GetActiveWorkspaces()
		assert.Len(t, result, 2)
	})

	t.Run("esc commits", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
		assert.True(t, committed)
	})

	t.Run("q commits", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		assert.True(t, committed)
	})

	t.Run("does not mutate input map", func(t *testing.T) {
		inputActive := map[string]bool{"alpha": true}
		p := NewWorkspacePicker(workspaces, inputActive, false)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
		assert.True(t, inputActive["alpha"])
		result := p.GetActiveWorkspaces()
		assert.Empty(t, result)
	})
}

func TestWorkspacePickerRender(t *testing.T) {
	workspaces := []config.Workspace{
		{Name: "alpha", Path: "/a"},
		{Name: "beta", Path: "/b"},
	}
	active := map[string]bool{"alpha": true}

	t.Run("renders with active workspace checked", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		output := p.Render()
		assert.Contains(t, output, "alpha")
		assert.Contains(t, output, "beta")
		assert.Contains(t, output, "[x]")
		assert.Contains(t, output, "[ ]")
	})

	t.Run("renders toggle help text", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		output := p.Render()
		assert.Contains(t, output, "toggle")
	})
}

func TestWorkspacePickerMidSessionGlobalRow(t *testing.T) {
	workspaces := []config.Workspace{
		{Name: "alpha", Path: "/a"},
		{Name: "beta", Path: "/b"},
	}
	active := map[string]bool{"alpha": true}

	t.Run("global row renders when allowGlobal=true", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, true)
		output := p.Render()
		assert.Contains(t, output, "Global (no workspace)")
	})

	t.Run("global row absent when allowGlobal=false", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, false)
		output := p.Render()
		assert.NotContains(t, output, "Global (no workspace)")
	})

	t.Run("can navigate to global row", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, true)
		// Navigate down past beta to global (cursor=2).
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		assert.Equal(t, 2, p.cursor)
		// Cursor cannot move past the global row.
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		assert.Equal(t, 2, p.cursor)
	})

	t.Run("space on global commits with empty active set", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, true)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
		assert.True(t, committed)
		assert.Empty(t, p.GetActiveWorkspaces())
	})

	t.Run("enter on global commits with empty active set", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active, true)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
		assert.True(t, committed)
		assert.Empty(t, p.GetActiveWorkspaces())
	})

	t.Run("global commit clears previously-toggled workspaces", func(t *testing.T) {
		// Two workspaces start active; user toggles a third on; then
		// picks Global. The commit must drop them all so the caller
		// can transition to global mode.
		bothActive := map[string]bool{"alpha": true, "beta": true}
		p := NewWorkspacePicker(workspaces, bothActive, true)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
		assert.True(t, committed)
		assert.Empty(t, p.GetActiveWorkspaces())
	})
}
