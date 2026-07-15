package tmux

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Prompt patterns are matched against rendered pane content, which the
// terminal may wrap mid-word at the pane width and interleave with SGR
// escapes (statusContent carries them on both the emulator and the
// capture-pane -e path). Matching must survive both, or a waiting agent
// settles to Ready instead of Prompting on narrow panes / styled prompts.

func TestPendingPromptMatchesWrappedPattern(t *testing.T) {
	ts := NewTmuxSession("promptwrap", "claude")
	// Pane narrower than the pattern: wrap splits it mid-word.
	content := "Do you want to make this edit?\n❯ 1. Yes\n  3. No, and tell Claude what to do differ\nently"
	require.True(t, ts.pendingPrompt(content),
		"a prompt wrapped at the pane width must still be detected")
}

func TestPendingPromptMatchesStyledPattern(t *testing.T) {
	ts := NewTmuxSession("promptstyle", "claude")
	content := "  3. No, and tell \x1b[1mClaude\x1b[0m what to do differently"
	require.True(t, ts.pendingPrompt(content),
		"SGR escapes inside the pattern must not defeat matching")
}

func TestPendingPromptNoFalsePositiveOnPlainOutput(t *testing.T) {
	ts := NewTmuxSession("promptnone", "claude")
	require.False(t, ts.pendingPrompt("$ ls\nREADME.md\n$ "))
}

func TestTrustPromptMatchesWrappedPattern(t *testing.T) {
	ts := NewTmuxSession("trustwrap", "claude")
	// Wrap splits the trust pattern mid-word; detection must still hit.
	// Dismissal fails (no PTY in this test) but handleTrustPrompt still
	// reports the prompt as found.
	content := "Do you trust the files in this fol\nder?\n❯ 1. Yes, proceed"
	require.True(t, ts.handleTrustPrompt(content),
		"a wrapped trust prompt must still be detected")
}
