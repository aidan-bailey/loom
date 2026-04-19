package ui

import (
	"claude-squad/log"
	"claude-squad/session"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

// TestListRenderDimensions verifies that the list's String() output
// does not exceed its allocated width and height. Exceeding either
// dimension causes the Bubble Tea alt-screen to scroll, cutting off
// the top of the TUI.
func TestListRenderDimensions(t *testing.T) {
	_ = log.Initialize("", false)

	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))

	// Create instances with various statuses to test all branches.
	mkInstance := func(title string, status session.Status, isWT bool) *session.Instance {
		inst := &session.Instance{
			Title:               title,
			IsWorkspaceTerminal: isWT,
		}
		_ = inst.TransitionTo(status)
		return inst
	}

	instances := []*session.Instance{
		mkInstance("Workspace Terminal", session.Running, true),
		mkInstance("fix-auth-bug", session.Running, false),
		mkInstance("add-logging", session.Paused, false),
		mkInstance("refactor-db", session.Prompting, false),
		mkInstance("long-title-that-might-overflow-the-allocated-width", session.Ready, false),
	}

	// Test a range of terminal widths that users commonly have.
	for _, termWidth := range []int{80, 100, 120, 150, 160, 200, 240} {
		termHeight := 40
		listWidth := int(float32(termWidth) * ListWidthPercent)
		paneWidth := termWidth - listWidth
		contentHeight := termHeight - 2 // no tab bar

		t.Run(fmt.Sprintf("termWidth_%d", termWidth), func(t *testing.T) {
			list := NewList(&sp, false)
			list.SetSize(listWidth, contentHeight)

			for _, inst := range instances {
				list.AddInstance(inst)()
			}

			output := list.String()
			lines := strings.Split(output, "\n")
			outputHeight := len(lines)

			maxLineWidth := 0
			for _, line := range lines {
				w := ansi.StringWidth(line)
				if w > maxLineWidth {
					maxLineWidth = w
				}
			}

			assert.LessOrEqual(t, outputHeight, contentHeight,
				"termWidth=%d listWidth=%d: list output height %d exceeds allocated %d",
				termWidth, listWidth, outputHeight, contentHeight)

			assert.LessOrEqual(t, maxLineWidth, listWidth,
				"termWidth=%d listWidth=%d: list output width %d exceeds allocated %d",
				termWidth, listWidth, maxLineWidth, listWidth)

			// Also verify the horizontally-joined width doesn't exceed the terminal.
			// Create a dummy right pane to simulate the join.
			dummyRight := lipgloss.Place(paneWidth, contentHeight,
				lipgloss.Left, lipgloss.Top, "")
			joined := lipgloss.JoinHorizontal(lipgloss.Top, output, dummyRight)
			joinedLines := strings.Split(joined, "\n")

			joinedMaxWidth := 0
			for _, line := range joinedLines {
				w := ansi.StringWidth(line)
				if w > joinedMaxWidth {
					joinedMaxWidth = w
				}
			}
			assert.LessOrEqual(t, joinedMaxWidth, termWidth,
				"termWidth=%d: joined width %d exceeds terminal width %d",
				termWidth, joinedMaxWidth, termWidth)
			assert.LessOrEqual(t, len(joinedLines), contentHeight,
				"termWidth=%d: joined height %d exceeds content height %d",
				termWidth, len(joinedLines), contentHeight)
		})
	}
}
