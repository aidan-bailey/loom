package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestQuickInputBar_HandleKeyPress_Enter(t *testing.T) {
	bar := NewQuickInputBar(QuickInputTargetAgent)
	// Type some text first
	for _, r := range "yes" {
		bar.HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	action := bar.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, QuickInputSubmit, action)
	assert.Equal(t, "yes", bar.Value())
}

func TestQuickInputBar_HandleKeyPress_Escape(t *testing.T) {
	bar := NewQuickInputBar(QuickInputTargetAgent)
	for _, r := range "partial" {
		bar.HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	action := bar.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.Equal(t, QuickInputCancel, action)
}

func TestQuickInputBar_HandleKeyPress_Typing(t *testing.T) {
	bar := NewQuickInputBar(QuickInputTargetAgent)
	action := bar.HandleKeyPress(tea.KeyPressMsg{Code: 'h', Text: "h"})
	assert.Equal(t, QuickInputContinue, action)
	assert.Equal(t, "h", bar.Value())
}

func TestQuickInputBar_EmptyEnter(t *testing.T) {
	bar := NewQuickInputBar(QuickInputTargetAgent)
	action := bar.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
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
