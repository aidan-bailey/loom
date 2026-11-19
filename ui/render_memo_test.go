package ui

import (
	"strings"
	"testing"
)

// The split pane is drawn on every frame, and most frames change neither
// pane: lipgloss measuring every grapheme of an unchanged pane again was
// most of an idle TUI's CPU. Each step reuses its last render while its
// inputs are unchanged, and must render exactly what a fresh pane would
// once any input changes.

// splitWith builds a split pane at w×h showing agent and terminal text.
func splitWith(w, h int, agent, terminal string) *SplitPane {
	sp := NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
	sp.SetSize(w, h)
	sp.agent.previewState = previewState{text: agent}
	sp.terminal.content = terminal
	return sp
}

func TestPreviewPane_ReusesItsRenderWhileNothingChanged(t *testing.T) {
	p := NewPreviewPane()
	p.SetSize(40, 10)
	p.previewState = previewState{text: "hello\nworld"}

	first := p.String()
	if second := p.String(); second != first {
		t.Fatalf("an unchanged pane rendered differently:\n%q\n%q", first, second)
	}
	if p.live.misses != 1 {
		t.Fatalf("an unchanged pane was rendered %d times, want 1", p.live.misses)
	}
}

func TestTerminalPane_ReusesItsRenderWhileNothingChanged(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetSize(40, 10)
	tp.content = "$ ls\nfile"

	first := tp.String()
	if second := tp.String(); second != first {
		t.Fatalf("an unchanged pane rendered differently:\n%q\n%q", first, second)
	}
	if tp.live.misses != 1 {
		t.Fatalf("an unchanged pane was rendered %d times, want 1", tp.live.misses)
	}
}

func TestSplitPane_ReusesItsBoxesWhileNothingChanged(t *testing.T) {
	sp := splitWith(60, 24, "agent says hi", "$ make")

	first := sp.String()
	if second := sp.String(); second != first {
		t.Fatalf("an unchanged split pane rendered differently")
	}
	for name, misses := range map[string]int{
		"agent box":    sp.agentBox.misses,
		"terminal box": sp.terminalBox.misses,
		"joined":       sp.joined.misses,
	} {
		if misses != 1 {
			t.Errorf("%s rendered %d times for two unchanged frames, want 1", name, misses)
		}
	}
}

// After each change, the memoized split pane renders what a fresh one in the
// same state renders.
func TestSplitPane_RendersAgainWhenAnInputChanges(t *testing.T) {
	cases := []struct {
		name   string
		change func(sp *SplitPane)
		fresh  func() *SplitPane
	}{
		{
			name:   "agent text",
			change: func(sp *SplitPane) { sp.agent.previewState = previewState{text: "agent moved on"} },
			fresh:  func() *SplitPane { return splitWith(60, 24, "agent moved on", "$ make") },
		},
		{
			name:   "terminal text",
			change: func(sp *SplitPane) { sp.terminal.content = "$ make test" },
			fresh:  func() *SplitPane { return splitWith(60, 24, "agent says hi", "$ make test") },
		},
		{
			name:   "size",
			change: func(sp *SplitPane) { sp.SetSize(50, 20) },
			fresh:  func() *SplitPane { return splitWith(50, 20, "agent says hi", "$ make") },
		},
		{
			name: "selection",
			change: func(sp *SplitPane) {
				sp.agent.BeginSelection(0, 0)
				sp.agent.ExtendSelection(0, 4)
			},
			fresh: func() *SplitPane {
				sp := splitWith(60, 24, "agent says hi", "$ make")
				sp.agent.BeginSelection(0, 0)
				sp.agent.ExtendSelection(0, 4)
				return sp
			},
		},
		{
			name:   "focus",
			change: func(sp *SplitPane) { sp.SetInlineAttach(true) },
			fresh: func() *SplitPane {
				sp := splitWith(60, 24, "agent says hi", "$ make")
				sp.SetInlineAttach(true)
				return sp
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp := splitWith(60, 24, "agent says hi", "$ make")
			before := sp.String()
			c.change(sp)
			got := sp.String()
			want := c.fresh().String()
			if got != want {
				t.Fatalf("after the change the pane rendered\n%s\nwant\n%s", got, want)
			}
			if got == before {
				t.Fatalf("the change left the frame as it was")
			}
		})
	}
}

// A live theme switch rebuilds the styles, not the panes' text: a render kept
// from before it would keep the old colors until the pane next changed.
func TestSplitPane_RendersAgainAfterAThemeSwitch(t *testing.T) {
	names := ThemeNames()
	if len(names) < 2 {
		t.Skip("one theme only")
	}
	start := CurrentThemeName()
	other := names[0]
	if other == start {
		other = names[1]
	}
	t.Cleanup(func() { ApplyTheme(start) })

	sp := splitWith(60, 24, "agent says hi", "$ make")
	_ = sp.String()
	ApplyTheme(other)

	got := sp.String()
	if want := splitWith(60, 24, "agent says hi", "$ make").String(); got != want {
		t.Fatalf("after a theme switch the pane rendered with the old theme")
	}
	if !strings.Contains(got, "agent says hi") {
		t.Fatalf("the pane lost its text: %q", got)
	}
}

// A box's own border takes the theme too: one whose text survives a theme
// switch unchanged is rendered again all the same.
func TestSplitPane_BoxRendersAgainAfterAThemeSwitch(t *testing.T) {
	names := ThemeNames()
	if len(names) < 2 {
		t.Skip("one theme only")
	}
	start := CurrentThemeName()
	other := names[0]
	if other == start {
		other = names[1]
	}
	t.Cleanup(func() { ApplyTheme(start) })

	sp := splitWith(60, 24, "", "")
	var memo renderMemo[paneBoxKey]
	_ = sp.renderPaneMemo(&memo, " Agent ", "same text", 5, true)
	ApplyTheme(other)

	got := sp.renderPaneMemo(&memo, " Agent ", "same text", 5, true)
	if memo.misses != 2 {
		t.Fatalf("a theme switch left the box as it was (%d renders)", memo.misses)
	}
	if want := sp.renderPane(" Agent ", "same text", 5, true); got != want {
		t.Fatalf("the box rendered after a theme switch differs from a fresh one")
	}
}
