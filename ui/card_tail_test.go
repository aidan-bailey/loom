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

// Claude Code attaches transient hint lines ("⎿  Tip: …") under the
// spinner while working. They are harness chrome, not conversation —
// the tail must surface the spinner/status line instead. Shape and
// bytes (U+23BF marker, NBSP before "Tip:") taken from a live pane.
func TestContentTailLines_SkipsTipUnderSpinner(t *testing.T) {
	screen := "(ctrl+b ctrl+b (twice) to run in background)\n" +
		"\n" +
		"· Meandering… (14m 50s · ↓ 75.3k tokens)\n" +
		"  ⎿  Tip: Dynamic workflows let Claude write a script that orchestrates many agents for you.\n" +
		"\n" +
		"────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────\n" +
		"   [Opus 4.8] fresco | prader-rs | main\n" +
		"   ▮▮▮▮▮ 25% | +1511/-3 lines\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n"

	assert.Equal(t,
		[]string{"· Meandering… (14m 50s · ↓ 75.3k tokens)"},
		ContentTailLines(screen, 1))
	assert.Equal(t,
		[]string{"(ctrl+b ctrl+b (twice) to run in background)", "· Meandering… (14m 50s · ↓ 75.3k tokens)"},
		ContentTailLines(screen, 2))
}

// Idle screens use a "※ Tip: …" variant above the input area — same
// rule, same skip.
func TestContentTailLines_SkipsIdleTipVariant(t *testing.T) {
	screen := "Last answer line\n" +
		"\n" +
		"※ Tip: press shift+enter to insert a newline\n" +
		"────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────\n" +
		"  ⏸ manual mode on · ← for agents\n"

	assert.Equal(t, []string{"Last answer line"}, ContentTailLines(screen, 1))
}

// A tip that wraps onto continuation lines (narrow pane) is one block:
// the indented wrap lines under the head are skipped with it.
func TestContentTailLines_SkipsWrappedTipBlock(t *testing.T) {
	screen := "✻ Working…\n" +
		"  ⎿  Tip: Dynamic workflows let Claude write a script that\n" +
		"     orchestrates many agents for you. Mention the keyword\n" +
		"     ultracode or ask Claude to use a workflow directly.\n" +
		"\n" +
		"────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────\n" +
		"  ⏸ manual mode on · ← for agents\n"

	assert.Equal(t, []string{"✻ Working…"}, ContentTailLines(screen, 2))
}

// Ordinary "⎿" tool-result lines are conversation content (they say what
// the agent just did) — only Tip lines are chrome.
func TestContentTailLines_KeepsToolResultLines(t *testing.T) {
	screen := "● Read(main.go)\n" +
		"  ⎿  Read 42 lines (ctrl+o to expand)\n" +
		"────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────\n" +
		"  ⏸ manual mode on · ← for agents\n"

	assert.Equal(t,
		[]string{"● Read(main.go)", "  ⎿  Read 42 lines (ctrl+o to expand)"},
		ContentTailLines(screen, 2))
}

// Claude Code's footer grew an agents strip below the mode line (one
// line per subagent). The bottom-delimiter scan must tolerate it — with
// three agents the footer is six non-blank lines, which overflowed the
// old allowance and dropped the whole screen back to the plain tail.
func TestContentTailLines_AllowsAgentsStripFooter(t *testing.T) {
	screen := "✻ Working…\n" +
		"────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────\n" +
		"   [Opus 4.8] fresco | prader-rs | aidanb/workflows-page\n" +
		"   ▮▮▮▮▮ 25% | +1511/-3 lines\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n" +
		"\n" +
		"  ● main\n" +
		"  ◯ general-purpose  Implement Task 1: WorkflowRunService\n" +
		"  ◯ general-purpose  Implement Task 2: run detail pane\n"

	assert.Equal(t, []string{"✻ Working…"}, ContentTailLines(screen, 1))
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
