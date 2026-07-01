package overlay

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func sampleRows() []MergePickerRow {
	return []MergePickerRow{
		{Index: 1, Title: "fix-auth", Branch: "u/fix-auth", Status: "Running"},
		{Index: 3, Title: "refactor-db", Branch: "u/refactor-db", Status: "Paused"},
		{Index: 4, Title: "docs", Branch: "u/docs", Status: "Ready"},
	}
}

func TestMergePickerNavigation(t *testing.T) {
	t.Run("starts at first row", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		assert.Equal(t, 0, p.cursor)
	})

	t.Run("moves down", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
		assert.Equal(t, 1, p.cursor)
	})

	t.Run("does not go below last row", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		for i := 0; i < 5; i++ {
			p.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
		}
		assert.Equal(t, 2, p.cursor)
	})

	t.Run("moves up", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
		assert.Equal(t, 0, p.cursor)
	})

	t.Run("does not go above first row", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
		assert.Equal(t, 0, p.cursor)
	})
}

func TestMergePickerDigitJump(t *testing.T) {
	t.Run("jumps to the row whose original index matches, not slice position", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		// Row at slice position 1 has Index 3 (index 2 was filtered out
		// upstream) — typing "3" must land there, not on slice position 3.
		p.HandleKeyPress(tea.KeyPressMsg{Code: '3', Text: "3"})
		assert.Equal(t, 1, p.cursor)
		row := p.SelectedRow()
		assert.Equal(t, "refactor-db", row.Title)
	})

	t.Run("typing an index with no matching row does not move the cursor", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: '2', Text: "2"})
		assert.Equal(t, 0, p.cursor)
	})
}

func TestMergePickerSelection(t *testing.T) {
	t.Run("enter commits with the highlighted row", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
		committed, canceled := p.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.True(t, committed)
		assert.False(t, canceled)
		assert.Equal(t, "refactor-db", p.SelectedRow().Title)
	})

	t.Run("esc commits as canceled", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		committed, canceled := p.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		assert.True(t, committed)
		assert.True(t, canceled)
	})

	t.Run("empty rows: enter commits as canceled-safe (nil selection)", func(t *testing.T) {
		p := NewMergePicker("current", nil)
		committed, _ := p.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.True(t, committed)
		assert.Nil(t, p.SelectedRow())
	})
}

func TestMergePickerRender_DoesNotPanic(t *testing.T) {
	p := NewMergePicker("current", sampleRows())
	p.SetSize(60, 0)
	assert.NotEmpty(t, p.View())

	empty := NewMergePicker("current", nil)
	assert.NotEmpty(t, empty.View())
}
