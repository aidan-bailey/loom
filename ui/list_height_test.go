package ui

import (
	"fmt"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
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
			list := NewList(&sp)
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

func TestMaxVisibleItems_RailMath(t *testing.T) {
	_ = log.Initialize("", false)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	l := NewList(&sp)
	// height 20: (20 - RailHeaderLines) / RailCardLines = 6
	l.SetSize(30, 20)
	assert.Equal(t, 6, l.maxVisibleItems())
	// Peer sections consume bottom lines: 2 peers = blank + 2 lines.
	l.SetPeerSections([]PeerSection{{Name: "a"}, {Name: "b"}})
	assert.Equal(t, 5, l.maxVisibleItems())
	// Tiny heights clamp to 1.
	l.SetSize(30, 3)
	assert.Equal(t, 1, l.maxVisibleItems())
}

func TestListString_RendersRailCards(t *testing.T) {
	_ = log.Initialize("", false)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	l := NewList(&sp)
	l.SetWorkspaceName("loom")
	l.SetSize(40, 30)
	inst := &session.Instance{Title: "auth-refactor"}
	_ = inst.TransitionTo(session.Ready)
	l.AddInstance(inst)
	out := ansi.Strip(l.String())
	assert.Contains(t, out, "LOOM")          // section label, uppercased
	assert.Contains(t, out, "auth-refactor") // card title
	assert.Contains(t, out, "▌")             // accent bar
}

// TestListString_NeverExceedsHeight sweeps degenerate sizes: the rail
// must never emit more lines than its allocated height, or the
// alt-screen scrolls and clips the top of the TUI (see clampHeight).
func TestListString_NeverExceedsHeight(t *testing.T) {
	_ = log.Initialize("", false)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	for height := 1; height <= 8; height++ {
		for _, nPeers := range []int{0, 3, 5} {
			for _, nItems := range []int{0, 1, 5} {
				l := NewList(&sp)
				l.SetSize(30, height)
				l.SetWorkspaceName("loom")
				peers := make([]PeerSection, nPeers)
				for i := range peers {
					peers[i] = PeerSection{Name: fmt.Sprintf("peer-%d", i), Running: 1}
				}
				l.SetPeerSections(peers)
				for i := 0; i < nItems; i++ {
					inst := &session.Instance{Title: fmt.Sprintf("inst-%d", i)}
					_ = inst.TransitionTo(session.Ready)
					l.AddInstance(inst)
				}
				out := l.String()
				assert.LessOrEqual(t, len(strings.Split(out, "\n")), height,
					"height=%d peers=%d items=%d: rail output overflows", height, nPeers, nItems)
			}
		}
	}
}

func TestListString_RendersPeerSummaries(t *testing.T) {
	_ = log.Initialize("", false)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	l := NewList(&sp)
	l.SetWorkspaceName("loom")
	l.SetSize(40, 30)
	l.SetPeerSections([]PeerSection{{Name: "summa", Attention: 2, Running: 1, Idle: 3}})
	out := ansi.Strip(l.String())
	assert.Contains(t, out, "SUMMA")
	assert.Contains(t, out, "❯2") // attention count surfaced with its glyph
}
