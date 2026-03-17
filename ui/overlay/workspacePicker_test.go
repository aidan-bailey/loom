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

	t.Run("starts at first item", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		ws := p.GetSelectedWorkspace()
		assert.NotNil(t, ws)
		assert.Equal(t, "alpha", ws.Name)
	})

	t.Run("moves down", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		ws := p.GetSelectedWorkspace()
		assert.NotNil(t, ws)
		assert.Equal(t, "beta", ws.Name)
	})

	t.Run("moves to global option", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		ws := p.GetSelectedWorkspace()
		assert.Nil(t, ws) // Global = nil
	})

	t.Run("does not go below global", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		ws := p.GetSelectedWorkspace()
		assert.Nil(t, ws) // Still on Global
	})

	t.Run("moves up", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		ws := p.GetSelectedWorkspace()
		assert.NotNil(t, ws)
		assert.Equal(t, "alpha", ws.Name)
	})

	t.Run("does not go above first item", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		ws := p.GetSelectedWorkspace()
		assert.NotNil(t, ws)
		assert.Equal(t, "alpha", ws.Name)
	})
}

func TestWorkspacePickerSelection(t *testing.T) {
	workspaces := []config.Workspace{
		{Name: "alpha", Path: "/a"},
	}

	t.Run("enter selects", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		selected, canceled := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
		assert.True(t, selected)
		assert.False(t, canceled)
	})

	t.Run("esc cancels", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		selected, canceled := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
		assert.False(t, selected)
		assert.True(t, canceled)
	})

	t.Run("q cancels", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		selected, canceled := p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		assert.False(t, selected)
		assert.True(t, canceled)
	})
}

func TestWorkspacePickerRender(t *testing.T) {
	workspaces := []config.Workspace{
		{Name: "alpha", Path: "/a"},
		{Name: "beta", Path: "/b"},
	}

	t.Run("renders with current workspace marked", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "alpha")
		output := p.Render()
		assert.Contains(t, output, "alpha")
		assert.Contains(t, output, "beta")
		assert.Contains(t, output, "Global")
		assert.Contains(t, output, "*") // current marker
	})

	t.Run("renders global marked when no current workspace", func(t *testing.T) {
		p := NewWorkspacePicker(workspaces, "")
		output := p.Render()
		assert.Contains(t, output, "Global")
	})
}
