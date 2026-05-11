package overlay

import (
	"fmt"
	"strings"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// OrphanRecoveryPicker is the startup overlay that lets the user pick
// which orphan worktrees to recover (re-add to state.json) and which
// to skip (leave on disk, untouched).
//
// Default per row: "recover" when a live tmux session is detected
// (HasLiveTmux=true) — those are the obviously-recoverable cases where
// no agent state has been lost. Inert worktrees default to "skip" so a
// machine with stale paused branches doesn't auto-revive everything on
// startup.
type OrphanRecoveryPicker struct {
	candidates []session.OrphanCandidate
	// recover[i] reports whether candidates[i] is currently marked for
	// recovery. Mutated by space/enter; read by Selected() after
	// commit.
	recover []bool
	cursor  int
	width   int
}

// NewOrphanRecoveryPicker constructs a recovery overlay. The slice
// passed in is treated as immutable — callers can re-use it.
func NewOrphanRecoveryPicker(candidates []session.OrphanCandidate) *OrphanRecoveryPicker {
	recover := make([]bool, len(candidates))
	for i, c := range candidates {
		recover[i] = c.HasLiveTmux
	}
	return &OrphanRecoveryPicker{
		candidates: candidates,
		recover:    recover,
		width:      70,
	}
}

// HandleKeyPress processes navigation and toggle keys. Returns
// (committed, _). committed=true means the overlay should close and
// the caller should consume Selected().
func (p *OrphanRecoveryPicker) HandleKeyPress(msg tea.KeyMsg) (bool, bool) {
	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.candidates)-1 {
			p.cursor++
		}
	case " ", "enter":
		if p.cursor >= 0 && p.cursor < len(p.recover) {
			p.recover[p.cursor] = !p.recover[p.cursor]
		}
	case "a":
		// 'a' = recover all, mirroring shell-style "select all" in
		// fzf-like pickers. Saves keystrokes when the user trusts
		// every orphan on the list.
		for i := range p.recover {
			p.recover[i] = true
		}
	case "n":
		// 'n' = skip all (none).
		for i := range p.recover {
			p.recover[i] = false
		}
	case "esc", "q":
		return true, false
	}
	return false, false
}

// HandleKey satisfies the Overlay interface.
func (p *OrphanRecoveryPicker) HandleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	closed, _ := p.HandleKeyPress(msg)
	return closed, nil
}

// View satisfies the Overlay interface.
func (p *OrphanRecoveryPicker) View() string {
	return p.Render()
}

// SetSize satisfies the Overlay interface. Width is the only
// dimension the picker honors; height is fixed by content length.
func (p *OrphanRecoveryPicker) SetSize(width, _ int) {
	if width > 0 {
		p.width = width
	}
}

// SetWidth sets the overlay width.
func (p *OrphanRecoveryPicker) SetWidth(width int) {
	if width > 0 {
		p.width = width
	}
}

// Selected returns the orphan candidates marked for recovery at the
// time of commit. Use this after HandleKeyPress reports closed=true.
func (p *OrphanRecoveryPicker) Selected() []session.OrphanCandidate {
	out := make([]session.OrphanCandidate, 0, len(p.candidates))
	for i, c := range p.candidates {
		if p.recover[i] {
			out = append(out, c)
		}
	}
	return out
}

// Skipped is the complement of Selected — orphans the user chose not
// to recover. Useful for telemetry and for the "did you mean to
// delete these?" follow-up flow we may add later.
func (p *OrphanRecoveryPicker) Skipped() []session.OrphanCandidate {
	out := make([]session.OrphanCandidate, 0, len(p.candidates))
	for i, c := range p.candidates {
		if !p.recover[i] {
			out = append(out, c)
		}
	}
	return out
}

// Render produces the overlay's text. Each candidate gets three lines:
// a checkbox + title row, the branch, and the worktree path. The tmux
// liveness flag is shown next to the title since that's the strongest
// recovery-confidence signal.
func (p *OrphanRecoveryPicker) Render() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	selectedStyle := lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	normalStyle := lipgloss.NewStyle().Foreground(ui.TextPrimary)
	hintStyle := lipgloss.NewStyle().Foreground(ui.TextHint)
	tmuxAliveStyle := lipgloss.NewStyle().Foreground(ui.HeaderAccent)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Recover orphan worktrees"))
	b.WriteString("\n")
	b.WriteString(hintStyle.Render(fmt.Sprintf("%d worktree(s) on disk are not in state.json.", len(p.candidates))))
	b.WriteString("\n\n")

	for i, c := range p.candidates {
		cursor := "  "
		if i == p.cursor {
			cursor = "> "
		}
		check := "[ ]"
		if p.recover[i] {
			check = "[x]"
		}

		titleLine := fmt.Sprintf("%s%s %s", cursor, check, c.Title)
		if c.HasLiveTmux {
			titleLine += "  " + tmuxAliveStyle.Render("● tmux alive")
		}
		branchLine := fmt.Sprintf("      branch:   %s", c.BranchName)
		pathLine := fmt.Sprintf("      worktree: %s", c.WorktreePath)

		if i == p.cursor {
			b.WriteString(selectedStyle.Render(titleLine))
		} else {
			b.WriteString(normalStyle.Render(titleLine))
		}
		b.WriteString("\n")
		b.WriteString(hintStyle.Render(branchLine))
		b.WriteString("\n")
		b.WriteString(hintStyle.Render(pathLine))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(hintStyle.Render("space toggle • a recover all • n skip all • esc done"))

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(p.width)

	return border.Render(b.String())
}
