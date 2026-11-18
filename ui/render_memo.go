package ui

// renderMemo keeps one render's output with the inputs it came from, so a
// view drawn again with the same inputs reuses it. lipgloss measures every
// grapheme of what it renders, and the split pane is drawn on every frame
// while most frames change neither pane: measuring unchanged panes again was
// most of an idle TUI's CPU. Every key carries themeGen, since a live theme
// switch rebuilds the styles without touching any pane's text.
type renderMemo[K comparable] struct {
	key   K
	out   string
	plain []string
	ok    bool
	// misses counts the real renders, for tests.
	misses int
}

// get returns the output kept for k, if the last render was of k.
func (m *renderMemo[K]) get(k K) (out string, plain []string, ok bool) {
	if m.ok && m.key == k {
		return m.out, m.plain, true
	}
	return "", nil, false
}

// put keeps out (and the plain lines rendered with it) as k's render.
func (m *renderMemo[K]) put(k K, out string, plain []string) {
	m.key, m.out, m.plain, m.ok = k, out, plain, true
	m.misses++
}

// liveKey is what a pane's live-tail render depends on: its text, size and
// selection.
type liveKey struct {
	text          string
	width, height int
	sel           selection
	theme         int
}

// paneBoxKey is what a split-pane box depends on (renderPane's inputs).
type paneBoxKey struct {
	title, content string
	innerHeight    int
	focused        bool
	width          int
	theme          int
}

// splitJoinKey is what the joined split pane depends on.
type splitJoinKey struct {
	agent, terminal string
	terminalHidden  bool
	height          int
}
