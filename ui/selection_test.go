package ui

import "testing"

func TestExtractSelection_SingleLine(t *testing.T) {
	lines := []string{"hello world"}
	sel := selection{active: true, anchorRow: 0, anchorCol: 0, curRow: 0, curCol: 5}
	if got := extractSelection(lines, sel); got != "hello" {
		t.Fatalf("single-line = %q, want %q", got, "hello")
	}
}

func TestExtractSelection_ReversedAnchor(t *testing.T) {
	lines := []string{"hello world"}
	// Drag right-to-left: anchor after cursor; normalized() must reorder.
	sel := selection{active: true, anchorRow: 0, anchorCol: 11, curRow: 0, curCol: 6}
	if got := extractSelection(lines, sel); got != "world" {
		t.Fatalf("reversed = %q, want %q", got, "world")
	}
}

func TestExtractSelection_MultiLine(t *testing.T) {
	lines := []string{"abcdef", "ghijkl", "mnopqr"}
	// from row0 col3 .. row2 col3 -> "def" + whole "ghijkl" + "mno"
	sel := selection{active: true, anchorRow: 0, anchorCol: 3, curRow: 2, curCol: 3}
	want := "def\nghijkl\nmno"
	if got := extractSelection(lines, sel); got != want {
		t.Fatalf("multi-line = %q, want %q", got, want)
	}
}

func TestExtractSelection_ClampsPastEOL(t *testing.T) {
	lines := []string{"hi"}
	sel := selection{active: true, anchorRow: 0, anchorCol: 0, curRow: 0, curCol: 999}
	if got := extractSelection(lines, sel); got != "hi" {
		t.Fatalf("clamped = %q, want %q", got, "hi")
	}
}

func TestExtractSelection_Empty(t *testing.T) {
	lines := []string{"hello"}
	if got := extractSelection(lines, selection{}); got != "" {
		t.Fatalf("inactive selection = %q, want empty", got)
	}
	zero := selection{active: true, anchorRow: 1, anchorCol: 2, curRow: 1, curCol: 2}
	if got := extractSelection(lines, zero); got != "" {
		t.Fatalf("zero-width selection = %q, want empty", got)
	}
}

func TestHighlightLine(t *testing.T) {
	got := highlightLine("hello", 1, 4)
	want := "h\x1b[7mell\x1b[27mo"
	if got != want {
		t.Fatalf("highlight = %q, want %q", got, want)
	}
	if highlightLine("hello", 3, 3) != "hello" {
		t.Fatal("empty range must return the line unchanged")
	}
	// Clamp past EOL.
	if got := highlightLine("hi", 0, 999); got != "\x1b[7mhi\x1b[27m" {
		t.Fatalf("clamped highlight = %q", got)
	}
}
