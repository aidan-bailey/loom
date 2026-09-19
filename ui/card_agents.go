package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// SubagentRow is one live subagent or teammate shown on a card.
type SubagentRow struct {
	Name        string
	Description string
	Idle        bool
}

// agentSummary is the rail's count suffix, e.g. " · 3 agents (1 idle)".
// Empty when the card has no live agents.
func (d CardData) agentSummary() string {
	n := len(d.Subagents)
	if n == 0 {
		return ""
	}
	idle := 0
	for _, r := range d.Subagents {
		if r.Idle {
			idle++
		}
	}
	switch {
	case n == 1 && idle == 1:
		return " · 1 agent (idle)"
	case n == 1:
		return " · 1 agent"
	case idle == 0:
		return fmt.Sprintf(" · %d agents", n)
	default:
		return fmt.Sprintf(" · %d agents (%d idle)", n, idle)
	}
}

// agentNameMax caps the overview name column.
const agentNameMax = 14

// Row glyphs. ◦ is used rather than Claude's own ◯, which has
// East-Asian-ambiguous width and can render two columns wide.
const (
	agentGlyphWorking = "✻"
	agentGlyphIdle    = "◦"
)

// overviewTailN fills an overview card's tail slots. With live agents
// they show agent rows; otherwise the output tail. Always exactly want
// lines, so card height never changes.
func overviewTailN(d CardData, inner, want int) []string {
	dim := lipgloss.NewStyle().Foreground(Dim)
	var lines []string
	switch n := len(d.Subagents); {
	case n == 0:
		for _, l := range d.TailLines {
			lines = append(lines, dim.Render(truncate(l, inner)))
		}
	case n == 1:
		lines = append(lines, agentRow("└", d.Subagents[0], nameColumn(d.Subagents), inner))
		if len(d.TailLines) > 0 {
			lines = append(lines, dim.Render(truncate(d.TailLines[len(d.TailLines)-1], inner)))
		}
	case n == 2:
		col := nameColumn(d.Subagents)
		lines = append(lines,
			agentRow("├", d.Subagents[0], col, inner),
			agentRow("└", d.Subagents[1], col, inner))
	default:
		lines = append(lines,
			agentRow("├", d.Subagents[0], nameColumn(d.Subagents[:1]), inner),
			moreRow(d.Subagents[1:], inner))
	}
	for len(lines) < want {
		lines = append(lines, "")
	}
	return lines[:want]
}

// nameColumn is the width names are padded to: the longest shown, capped.
func nameColumn(rows []SubagentRow) int {
	w := 0
	for _, r := range rows {
		w = max(w, runewidth.StringWidth(r.Name))
	}
	return min(w, agentNameMax)
}

// agentRow renders "<tree> <glyph> <name>  <text>" within inner columns.
func agentRow(tree string, r SubagentRow, nameCol, inner int) string {
	glyph, glyphFg, text, textFg := agentGlyphWorking, OK, r.Description, Text
	if r.Idle {
		glyph, glyphFg, text, textFg = agentGlyphIdle, Dim, "idle", Dim
	}
	name := truncate(r.Name, nameCol)
	name += strings.Repeat(" ", max(0, nameCol-runewidth.StringWidth(name)))

	line := lipgloss.NewStyle().Foreground(Rule).Render(tree) + " " +
		lipgloss.NewStyle().Foreground(glyphFg).Render(glyph) + " " +
		lipgloss.NewStyle().Foreground(Text).Render(name)
	if text != "" {
		line += "  " + lipgloss.NewStyle().Foreground(textFg).Render(text)
	}
	return ansi.Truncate(line, inner, "…")
}

// moreRow summarizes the agents a card has no room for, e.g.
// "└ +3 more · 1 working · 2 idle", leaving out zero counts.
func moreRow(hidden []SubagentRow, inner int) string {
	working, idle := 0, 0
	for _, r := range hidden {
		if r.Idle {
			idle++
		} else {
			working++
		}
	}
	text := fmt.Sprintf("+%d more", len(hidden))
	if working > 0 {
		text += fmt.Sprintf(" · %d working", working)
	}
	if idle > 0 {
		text += fmt.Sprintf(" · %d idle", idle)
	}
	line := lipgloss.NewStyle().Foreground(Rule).Render("└") + " " +
		lipgloss.NewStyle().Foreground(Dim).Render(text)
	return ansi.Truncate(line, inner, "…")
}
