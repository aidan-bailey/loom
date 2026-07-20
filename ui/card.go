package ui

import (
	"cmp"
	"fmt"
	"image/color"
	"slices"
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

// PeerSection summarizes a non-focused workspace slot for the rail
// footer (live counts from that slot's list; selection stays scoped to
// the focused workspace until cross-workspace lands).
type PeerSection struct {
	Name      string
	Attention int // Prompting or bell-pending
	Running   int // Running/Loading
	Idle      int // everything else
}

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
// accent — the only loud signal in the UI. A Deleting instance never
// needs attention: a stale bell on a mid-kill card must not paint it
// gold or float it into the attention tier.
func (d CardData) NeedsAttention() bool {
	return d.Status != session.Deleting && (d.Status == session.Prompting || d.BellPending)
}

// BuildCardData snapshots inst into a CardData. spinnerFrame is the
// current spinner view (pass "" when unavailable). tailN caps the live
// tail; 0 skips the screen read entirely (DensityLine callers). The
// tail comes from Instance.EmulatorScreen — in-memory only, so calling
// this per visible card per frame forks no subprocesses; snapshot-path
// instances simply render their status label instead of a tail.
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
		if screen, ok := inst.EmulatorScreen(); ok {
			d.TailLines = TailLines(screen, tailN)
		}
	}
	return d
}

// TailLines returns the last n non-blank-tail lines of screen with ANSI
// styling stripped (card chrome owns the styling; embedded SGR would
// bleed). C0 controls survive ansi.Strip, so tabs are normalized to a
// single space and carriage returns dropped — emulator output never
// contains them today, but the helper is exported and generic. Returns
// nil for an effectively empty screen.
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
		l = ansi.Strip(l)
		l = strings.ReplaceAll(l, "\t", " ")
		l = strings.ReplaceAll(l, "\r", "")
		out = append(out, l)
	}
	return out
}

// accentColor returns the left-bar/border accent for a card:
// attention > selected > workspace-terminal > running (OK, the green
// border the approved mockups give active cards) > Rule.
func (d CardData) accentColor() color.Color {
	switch {
	case d.NeedsAttention():
		return Attention
	case d.Selected:
		return Accent
	case d.IsWorkspaceTerminal:
		return Workspace
	case d.Status == session.Running || d.Status == session.Loading:
		return OK
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

// truncate cuts s to at most width cells, appending an ellipsis when
// content is dropped (runewidth.Truncate reserves the tail's width
// itself, so the full budget is passed through).
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width <= 1 {
		return runewidth.Truncate(s, width, "")
	}
	return runewidth.Truncate(s, width, "…")
}

// RenderCard renders d at the given density and total width. DensityCard
// is rendered by Overview (overview.go) which owns border/grid layout;
// RenderCard handles DensityLine and DensityRail.
func RenderCard(d CardData, density CardDensity, width int) string {
	if width < 4 {
		width = 4
	}
	// Selected rail cards paint a solid Panel background. It has to
	// ride the inner styles: wrapping the assembled line in a
	// Background style does not survive the embedded SGR resets
	// (lipgloss does not re-inject the outer background after a reset,
	// leaving the bg only on the bar and padding — a striped look).
	solidBg := d.Selected && density == DensityRail
	barStyle := lipgloss.NewStyle().Foreground(d.accentColor())
	sepStyle := lipgloss.NewStyle()
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
	if solidBg {
		barStyle = barStyle.Background(Panel)
		sepStyle = sepStyle.Background(Panel)
		titleStyleC = titleStyleC.Background(Panel)
	}
	bar := barStyle.Render("▌")
	sep := sepStyle.Render(" ")

	prefix := fmt.Sprintf("%d. ", d.Index)
	inner := width - 2 // bar + space
	title := truncate(prefix+d.Title, inner)
	titleLine := bar + sep + titleStyleC.Render(title)

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
	secondStyle := lipgloss.NewStyle().Foreground(secondFg)
	if solidBg {
		secondStyle = secondStyle.Background(Panel)
	}
	secondLine := bar + sep + secondStyle.Render(truncate(second, inner))

	if solidBg {
		// Outer style only right-pads to full width (padding spaces
		// carry the background); the visible runs above own their own.
		pad := lipgloss.NewStyle().Background(Panel).Width(width)
		return pad.Render(titleLine) + "\n" + pad.Render(secondLine)
	}
	return titleLine + "\n" + secondLine
}

// SortForOverview returns display order (indices into items) for the
// overview grid and overview cursor movement: workspace terminal pinned
// first, then attention > running/loading > ready > paused/recoverable,
// stable by title within a tier. Deleting sorts last.
func SortForOverview(items []*session.Instance) []int {
	// Tiers are computed once up front so the sort sees a consistent
	// snapshot (status/bell are read under the instance lock) and each
	// instance is read exactly once instead of O(n log n) times.
	tiers := make([]int, len(items))
	for i, inst := range items {
		tiers[i] = overviewTier(inst)
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if c := cmp.Compare(tiers[a], tiers[b]); c != 0 {
			return c
		}
		return strings.Compare(items[a].Title, items[b].Title)
	})
	return order
}

// overviewTier maps an instance to its SortForOverview tier.
func overviewTier(inst *session.Instance) int {
	if inst.IsWorkspaceTerminal {
		return 0
	}
	st := inst.GetStatus()
	switch {
	case st == session.Deleting:
		// Checked before the bell: a stale bell on a mid-kill instance
		// must not float it into the attention tier.
		return 5
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
