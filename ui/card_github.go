package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/aidan-bailey/loom/session/github"
)

// Theme-derived styles must be hook-built (see ui/theme.go): init-time
// styles would capture pre-ApplyTheme colors.
var (
	ghDimStyle  lipgloss.Style
	ghOKStyle   lipgloss.Style
	ghErrStyle  lipgloss.Style
	ghTextStyle lipgloss.Style
)

func init() { RegisterThemeHook(rebuildGitHubStyles) }

func rebuildGitHubStyles() {
	ghDimStyle = lipgloss.NewStyle().Foreground(Dim)
	ghOKStyle = lipgloss.NewStyle().Foreground(OK)
	ghErrStyle = lipgloss.NewStyle().Foreground(ErrorColor)
	ghTextStyle = lipgloss.NewStyle().Foreground(Text)
}

// checksGlyph is the one-character CI summary; "" for none.
func checksGlyph(c github.Checks) string {
	switch c {
	case github.ChecksPending:
		return ghDimStyle.Render("●")
	case github.ChecksPassing:
		return ghOKStyle.Render("✓")
	case github.ChecksFailing:
		return ghErrStyle.Render("✗")
	default:
		return ""
	}
}

// githubBadge renders the overview PR badge: "PR#45 open ✓". Empty
// when unknown or no PR. Merged PRs dim the whole badge.
func githubBadge(s github.State) string {
	if !s.Known || !s.HasPR {
		return ""
	}
	label := fmt.Sprintf("PR#%d ", s.PRNumber)
	var word string
	style := ghTextStyle
	switch {
	case s.PRState == github.PRMerged:
		word, style = "merged", ghDimStyle
	case s.PRState == github.PRClosed:
		word, style = "closed", ghDimStyle
	case s.PRState == github.PRDraft:
		word, style = "draft", ghDimStyle
	case s.Review == github.ReviewApproved:
		word, style = "✓approved", ghOKStyle
	case s.Review == github.ReviewChangesRequested:
		word, style = "✗changes", ghErrStyle
	default:
		word = "open"
	}
	out := style.Render(label + word)
	if g := checksGlyph(s.Checks); g != "" {
		out += " " + g
	}
	return out
}

// parityToken renders "↑N ↓M" (or "✓ even"); "" when unknown.
func parityToken(d CardData) string {
	if !d.HasParity {
		return ""
	}
	if d.Ahead == 0 && d.Behind == 0 {
		return ghDimStyle.Render("✓ even")
	}
	return ghOKStyle.Render(fmt.Sprintf("↑%d", d.Ahead)) + " " + ghDimStyle.Render(fmt.Sprintf("↓%d", d.Behind))
}

// prGlyph is the rail's one-character PR state.
func prGlyph(s github.State) string {
	switch {
	case s.PRState == github.PRMerged:
		return "⇄"
	case s.PRState == github.PRDraft:
		return "○"
	case s.Review == github.ReviewApproved:
		return "✓"
	case s.Review == github.ReviewChangesRequested:
		return "✗"
	default:
		return "●"
	}
}

// railGitHubToken is the compact right-side token for rail cards:
// "#12 · PR ⇄✓ ↓4". Only actionable parts appear: the issue number,
// the PR state+checks, and "behind" when nonzero.
func railGitHubToken(d CardData) string {
	var parts []string
	if d.GitHub.IssueNumber != 0 {
		parts = append(parts, ghDimStyle.Render(fmt.Sprintf("#%d", d.GitHub.IssueNumber)))
	}
	if d.GitHub.Known && d.GitHub.HasPR {
		parts = append(parts, ghDimStyle.Render("PR "+prGlyph(d.GitHub))+checksGlyph(d.GitHub.Checks))
	}
	tok := strings.Join(parts, ghDimStyle.Render(" · "))
	if d.HasParity && d.Behind > 0 {
		if tok != "" {
			tok += " "
		}
		tok += ghDimStyle.Render(fmt.Sprintf("↓%d", d.Behind))
	}
	return tok
}

// issueLine renders the overview's issue line; "" when unlinked.
func issueLine(d CardData, inner int) string {
	if d.GitHub.IssueNumber == 0 {
		return ""
	}
	text := fmt.Sprintf("#%d %s", d.GitHub.IssueNumber, d.GitHub.IssueTitle)
	if d.GitHub.IssueClosed {
		return ghDimStyle.Render(truncate("✓ "+text, inner))
	}
	return ghTextStyle.Render(truncate(text, inner))
}

// finished reports whether the card's work is done from GitHub's
// point of view (merged PR or closed issue), which dims meta lines.
func (d CardData) finished() bool {
	return d.GitHub.Known && ((d.GitHub.HasPR && d.GitHub.PRState == github.PRMerged) || d.GitHub.IssueClosed)
}
