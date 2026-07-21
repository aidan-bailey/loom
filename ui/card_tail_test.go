package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Current Claude Code UI: input area delimited by full-width rules, with
// a multi-line footer (statusline, context meter, mode line) below. The
// tail must come from the content above the input area, not the footer.
func TestContentTailLines_SkipsClaudeRuleChrome(t *testing.T) {
	screen := "Earlier output line\n" +
		"✻ Running tests… (esc to interrupt)\n" +
		"\n" +
		"────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────\n" +
		"   [Opus 4.8] fresco | kermit | master ~1\n" +
		"   ▮▮▮▮▮▮ 12%\n" +
		"  ⏸ manual mode on · ← for agents\n"

	assert.Equal(t,
		[]string{"Earlier output line", "✻ Running tests… (esc to interrupt)"},
		ContentTailLines(screen, 2))
	assert.Equal(t,
		[]string{"✻ Running tests… (esc to interrupt)"},
		ContentTailLines(screen, 1), "rail cards take a 1-line tail")
}

// Legacy Claude Code UI: rounded-box input area with a shortcut line
// below. Same rule — tail comes from above the box.
func TestContentTailLines_SkipsLegacyBoxChrome(t *testing.T) {
	screen := "Last answer line\n" +
		"╭──────────────╮\n" +
		"│ >            │\n" +
		"╰──────────────╯\n" +
		"  ? for shortcuts\n"

	assert.Equal(t, []string{"Last answer line"}, ContentTailLines(screen, 1))
}

// A dialog rendered inside the input area (permission prompt) is the
// informative content — show its interior instead of skipping it.
func TestContentTailLines_ShowsDialogInterior(t *testing.T) {
	screen := "Some output\n" +
		"────────────────────────────────\n" +
		" Do you want to make this edit?\n" +
		" ❯ 1. Yes\n" +
		"   2. No\n" +
		"────────────────────────────────\n" +
		"  ⏸ manual mode on · ← for agents\n"

	assert.Equal(t,
		[]string{" ❯ 1. Yes", "   2. No"},
		ContentTailLines(screen, 2))
}

// Blank lines between content blocks carry no signal on a 2-line card —
// the content path takes the last n NON-blank lines (seen live: a blank
// between the last message and the "✶ Working…" spinner line ate half
// the tail).
func TestContentTailLines_SkipsInteriorBlankLines(t *testing.T) {
	screen := "line one\n" +
		"\n" +
		"✻ Working…\n" +
		"────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────\n" +
		"  ⏸ manual mode on · ← for agents\n"

	assert.Equal(t, []string{"line one", "✻ Working…"}, ContentTailLines(screen, 2))
}

// No recognizable chrome (plain shells, aider, loading screens): behave
// exactly like the plain tail so nothing regresses.
func TestContentTailLines_NoChromeFallsBackToPlainTail(t *testing.T) {
	screen := "line a\nline b\n❯\n"
	assert.Equal(t, TailLines(screen, 2), ContentTailLines(screen, 2))
	assert.Equal(t, []string{"line b", "❯"}, ContentTailLines(screen, 2))
}

// A bottom delimiter with no matching top delimiter within range (e.g. a
// stray horizontal rule in output) must not eat the screen — fall back.
func TestContentTailLines_UnmatchedRuleFallsBack(t *testing.T) {
	screen := "line a\n────────────\nline b\nline c\n"
	assert.Equal(t, TailLines(screen, 2), ContentTailLines(screen, 2))
}
