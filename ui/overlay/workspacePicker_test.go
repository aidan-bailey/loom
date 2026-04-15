package overlay

import (
	"claude-squad/config"
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
		p := NewWorkspacePicker(workspaces, active)
		assert.Equal(t, 0, p.cursor)
	})

	t.Run("moves down", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		assert.Equal(t, 1, p.cursor)
	})

	t.Run("does not go below last item", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		assert.Equal(t, 1, p.cursor)
	})

	t.Run("moves up", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active)
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		assert.Equal(t, 0, p.cursor)
	})

	t.Run("does not go above first item", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active)
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
		p := NewWorkspacePicker(workspaces, active)
		// Alpha is active, toggle it off
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
		assert.False(t, committed)
		result := p.GetActiveWorkspaces()
		assert.Empty(t, result)
	})

	t.Run("enter toggles active state", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active)
		// Move to beta and toggle it on
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
		assert.False(t, committed)
		result := p.GetActiveWorkspaces()
		assert.Len(t, result, 2)
	})

	t.Run("esc commits", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active)
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
		assert.True(t, committed)
	})

	t.Run("q commits", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active)
		committed, _ := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		assert.True(t, committed)
	})

	t.Run("does not mutate input map", func(t *testing.T) {
		inputActive := map[string]bool{"alpha": true}
		p := NewWorkspacePicker(workspaces, inputActive)
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
		p := NewWorkspacePicker(workspaces, active)
		output := p.Render()
		assert.Contains(t, output, "alpha")
		assert.Contains(t, output, "beta")
		assert.Contains(t, output, "[x]")
		assert.Contains(t, output, "[ ]")
	})

	t.Run("renders toggle help text", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, active)
		output := p.Render()
		assert.Contains(t, output, "toggle")
	})
}
