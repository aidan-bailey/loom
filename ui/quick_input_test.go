package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestQuickInputBar_HandleKeyPress_Enter(t *testing.T) {
	bar := NewQuickInputBar(QuickInputTargetAgent)
	// Type some text first
	bar.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("yes")})
	action := bar.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, QuickInputSubmit, action)
	assert.Equal(t, "yes", bar.Value())
}

func TestQuickInputBar_HandleKeyPress_Escape(t *testing.T) {
	bar := NewQuickInputBar(QuickInputTargetAgent)
	bar.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("partial")})
	action := bar.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, QuickInputCancel, action)
}

func TestQuickInputBar_HandleKeyPress_Typing(t *testing.T) {
	bar := NewQuickInputBar(QuickInputTargetAgent)
	action := bar.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	assert.Equal(t, QuickInputContinue, action)
	assert.Equal(t, "h", bar.Value())
}

func TestQuickInputBar_EmptyEnter(t *testing.T) {
	bar := NewQuickInputBar(QuickInputTargetAgent)
	action := bar.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, QuickInputSubmit, action)
	assert.Equal(t, "", bar.Value())
}

func TestQuickInputBar_Height(t *testing.T) {
	bar := NewQuickInputBar(QuickInputTargetAgent)
	assert.Equal(t, 2, bar.Height())
}

func TestQuickInputBar_ViewHintByTarget(t *testing.T) {
	tests := []struct {
		target   QuickInputTarget
		contains string
	}{
		{QuickInputTargetAgent, "send to agent"},
		{QuickInputTargetTerminal, "send to terminal"},
	}
	for _, tt := range tests {
		bar := NewQuickInputBar(tt.target)
		bar.SetWidth(80)
		assert.Contains(t, bar.View(), tt.contains)
	}
}
