package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

// clipboardCopiedMsg reports the outcome of the local (atotto) clipboard write.
type clipboardCopiedMsg struct {
	n   int // runes copied
	err error
}

// clipboardFallbackCmd writes text to the local system clipboard via atotto,
// covering terminals that don't support OSC 52. Reports the rune count.
func clipboardFallbackCmd(text string) tea.Cmd {
	return func() tea.Msg {
		err := clipboard.WriteAll(text)
		return clipboardCopiedMsg{n: len([]rune(text)), err: err}
	}
}

// copyToClipboard copies text to the system clipboard via two paths:
// tea.SetClipboard emits OSC 52 (tunnels through SSH and works in OSC52-capable
// terminals) and atotto writes the local clipboard for terminals without OSC 52.
// Returns nil for empty text.
func copyToClipboard(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	return tea.Batch(tea.SetClipboard(text), clipboardFallbackCmd(text))
}
