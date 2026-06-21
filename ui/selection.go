package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// selection is a text range over a pane's displayed (plain) lines, in content
// coordinates: row indexes the displayed lines, col is a rune index into a line.
// The range is half-open on columns: [c0, c1).
type selection struct {
	active                               bool
	anchorRow, anchorCol, curRow, curCol int
}

// normalized returns the selection bounds ordered top-to-bottom, left-to-right.
func (s selection) normalized() (r0, c0, r1, c1 int) {
	r0, c0, r1, c1 = s.anchorRow, s.anchorCol, s.curRow, s.curCol
	if r1 < r0 || (r1 == r0 && c1 < c0) {
		r0, c0, r1, c1 = r1, c1, r0, c0
	}
	return
}

// empty reports whether the selection covers no cells (inactive or zero-width).
func (s selection) empty() bool {
	return !s.active || (s.anchorRow == s.curRow && s.anchorCol == s.curCol)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// extractSelection returns the selected text across plainLines for sel, joined
// by newlines. Rune-indexed; out-of-range positions clamp to line bounds.
func extractSelection(plainLines []string, sel selection) string {
	if sel.empty() || len(plainLines) == 0 {
		return ""
	}
	r0, c0, r1, c1 := sel.normalized()
	r0 = clampInt(r0, 0, len(plainLines)-1)
	r1 = clampInt(r1, 0, len(plainLines)-1)
	if r1 < r0 {
		return ""
	}
	var b strings.Builder
	for r := r0; r <= r1; r++ {
		runes := []rune(plainLines[r])
		start, end := 0, len(runes)
		if r == r0 {
			start = clampInt(c0, 0, len(runes))
		}
		if r == r1 {
			end = clampInt(c1, 0, len(runes))
		}
		if start > end {
			start = end
		}
		b.WriteString(string(runes[start:end]))
		if r < r1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderWithSelection takes the final on-screen lines of a pane and a selection,
// and returns (display, plain): `plain` is each line ANSI-stripped (for text
// extraction and hit math), and `display` is the lines to render — identical to
// the input except selected rows are reverse-highlighted on their stripped text.
// When sel is empty, display is the input unchanged.
func renderWithSelection(lines []string, sel selection) (display, plain []string) {
	plain = make([]string, len(lines))
	for i, l := range lines {
		plain[i] = ansi.Strip(l)
	}
	if sel.empty() {
		return lines, plain
	}
	r0, c0, r1, c1 := sel.normalized()
	display = make([]string, len(lines))
	copy(display, lines)
	for r := r0; r <= r1; r++ {
		if r < 0 || r >= len(lines) {
			continue
		}
		from, to := 0, len([]rune(plain[r]))
		if r == r0 {
			from = c0
		}
		if r == r1 {
			to = c1
		}
		display[r] = highlightLine(plain[r], from, to)
	}
	return display, plain
}

// highlightLine wraps the rune range [fromCol, toCol) of plain in reverse-video
// SGR. Clamps to the line; returns plain unchanged for an empty range. The
// highlighted region is rendered plain + reversed (existing SGR is not parsed).
func highlightLine(plain string, fromCol, toCol int) string {
	runes := []rune(plain)
	n := len(runes)
	fromCol = clampInt(fromCol, 0, n)
	toCol = clampInt(toCol, 0, n)
	if fromCol >= toCol {
		return plain
	}
	return string(runes[:fromCol]) + "\x1b[7m" + string(runes[fromCol:toCol]) + "\x1b[27m" + string(runes[toCol:])
}
