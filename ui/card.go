package ui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// CardDensity selects how much of an instance a card shows.
type CardDensity int

const (
	// DensityLine is a one-line row (height-starved fallback).
	DensityLine CardDensity = iota
	// DensityRail is the focus-mode rail mini-card: title line + one
	// tail/status line, with a left accent bar.
	DensityRail
	// DensityCard is the overview card: bordered box with metadata and
	// a multi-line tail (rendered by Overview in overview.go).
	DensityCard
)

// Rail layout constants (used by List height math).
const (
	// RailCardLines is lines per DensityRail card incl. trailing gap.
	RailCardLines = 3
	// RailHeaderLines is the section-label header at the top of the rail.
	RailHeaderLines = 2
)

// CardData is the render-ready view-model for one instance card. It is
// plain data so RenderCard stays a pure, table-testable function; build
// it from a live instance with BuildCardData.
type CardData struct {
	Title               string
	Index               int // 1-based display number (0 for workspace terminal)
	Status              session.Status
	IsWorkspaceTerminal bool
	BellPending         bool
	Selected            bool
	Branch              string
	DiffAdded           int
	DiffRemoved         int
	HasDiff             bool
	TailLines           []string
	StatusAge           time.Duration // 0 = unknown/not applicable
	Spinner             string        // current spinner frame for Running/Loading
}

// NeedsAttention reports whether this card should carry the Attention
// accent — the only loud signal in the UI.
func (d CardData) NeedsAttention() bool {
	return d.Status == session.Prompting || d.BellPending
}

// BuildCardData snapshots inst into a CardData. spinnerFrame is the
// current spinner view (pass "" when unavailable). tailN caps the live
// tail; 0 skips the Preview read entirely (DensityLine callers).
func BuildCardData(inst *session.Instance, selected bool, spinnerFrame string, tailN int) CardData {
	d := CardData{
		Title:               inst.Title,
		Status:              inst.GetStatus(),
		IsWorkspaceTerminal: inst.IsWorkspaceTerminal,
		BellPending:         inst.BellPending(),
		Selected:            selected,
		Branch:              inst.GetBranch(),
		StatusAge:           inst.StatusAge(),
		Spinner:             spinnerFrame,
	}
	if stat := inst.GetDiffStats(); stat != nil && stat.Error == nil && !stat.IsEmpty() {
		d.HasDiff, d.DiffAdded, d.DiffRemoved = true, stat.Added, stat.Removed
	}
	if tailN > 0 {
		if screen, err := inst.Preview(); err == nil {
			d.TailLines = TailLines(screen, tailN)
		}
	}
	return d
}

// TailLines returns the last n non-blank-tail lines of screen with ANSI
// styling stripped (card chrome owns the styling; embedded SGR would
// bleed). Returns nil for an effectively empty screen.
func TailLines(screen string, n int) []string {
	if screen == "" {
		return nil
	}
	lines := strings.Split(screen, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	if end == 0 {
		return nil
	}
	start := end - n
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, end-start)
	for _, l := range lines[start:end] {
		out = append(out, ansi.Strip(l))
	}
	return out
}

// accentColor returns the left-bar/border accent for a card.
func (d CardData) accentColor() color.Color {
	switch {
	case d.NeedsAttention():
		return Attention
	case d.Selected:
		return Accent
	case d.IsWorkspaceTerminal:
		return Workspace
	default:
		return Rule
	}
}

// statusLabel is the human status phrase for the second line / card
// corner: "❯ awaiting input · 4m", "✻ working", "paused · 3d", …
func (d CardData) statusLabel() string {
	age := formatAge(d.StatusAge)
	switch d.Status {
	case session.Prompting:
		if age != "" {
			return "❯ awaiting input · " + age
		}
		return "❯ awaiting input"
	case session.Running, session.Loading:
		return d.Spinner + " working"
	case session.Ready:
		if age != "" {
			return "✓ idle " + age
		}
		return "✓ idle"
	case session.Paused:
		if age != "" {
			return "paused · " + age
		}
		return "paused"
	case session.Recoverable:
		return "⟲ recoverable"
	case session.Deleting:
		return "✕ deleting"
	}
	return ""
}

// formatAge renders a duration as a compact age ("40s", "4m", "1h12m",
// "3d"); "" for zero/negative.
func formatAge(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// truncate cuts s to width cells with an ellipsis.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return runewidth.Truncate(s, width, "")
	}
	return runewidth.Truncate(s, width-1, "…")
}

// RenderCard renders d at the given density and total width. DensityCard
// is rendered by Overview (overview.go) which owns border/grid layout;
// RenderCard handles DensityLine and DensityRail.
func RenderCard(d CardData, density CardDensity, width int) string {
	if width < 4 {
		width = 4
	}
	bar := lipgloss.NewStyle().Foreground(d.accentColor()).Render("▌")
	titleFg := Text
	if d.NeedsAttention() {
		titleFg = Attention
	}
	if d.IsWorkspaceTerminal && !d.NeedsAttention() {
		titleFg = Workspace
	}
	titleStyleC := lipgloss.NewStyle().Foreground(titleFg)
	if d.Selected {
		titleStyleC = titleStyleC.Bold(true)
	}

	prefix := fmt.Sprintf("%d. ", d.Index)
	inner := width - 2 // bar + space
	title := truncate(prefix+d.Title, inner)
	titleLine := bar + " " + titleStyleC.Render(title)

	if density == DensityLine {
		return titleLine
	}

	// Second line: attention prompt beats tail beats status label.
	second := d.statusLabel()
	secondFg := Dim
	if d.NeedsAttention() {
		secondFg = Attention
	} else if len(d.TailLines) > 0 {
		second = d.TailLines[len(d.TailLines)-1]
	}
	secondLine := bar + " " + lipgloss.NewStyle().Foreground(secondFg).Render(truncate(second, inner))

	// Selected rail cards get a Panel background across both lines.
	if d.Selected {
		bgStyle := lipgloss.NewStyle().Background(Panel).Width(width)
		return bgStyle.Render(titleLine) + "\n" + bgStyle.Render(secondLine)
	}
	return titleLine + "\n" + secondLine
}

// SortForOverview returns display order (indices into items) for the
// overview grid and overview cursor movement: workspace terminal pinned
// first, then attention > running/loading > ready > paused/recoverable,
// stable by title within a tier. Deleting sorts last.
func SortForOverview(items []*session.Instance) []int {
	tier := func(inst *session.Instance) int {
		if inst.IsWorkspaceTerminal {
			return 0
		}
		st := inst.GetStatus()
		switch {
		case st == session.Prompting || inst.BellPending():
			return 1
		case st == session.Running || st == session.Loading:
			return 2
		case st == session.Ready:
			return 3
		case st == session.Paused || st == session.Recoverable:
			return 4
		default: // Deleting
			return 5
		}
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sortSliceStable(order, func(a, b int) bool {
		ta, tb := tier(items[a]), tier(items[b])
		if ta != tb {
			return ta < tb
		}
		return items[a].Title < items[b].Title
	})
	return order
}

// sortSliceStable is a tiny stable insertion sort — n is small (session
// counts), and keeping it local avoids importing sort here.
func sortSliceStable(order []int, less func(a, b int) bool) {
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && less(order[j], order[j-1]); j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
}
