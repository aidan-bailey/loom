package ui

import "testing"

func TestSplitPaneHitTest(t *testing.T) {
	sp := NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
	sp.SetSize(42, 24)
	ah := sp.agent.height
	if ah < 2 || sp.terminal.height < 2 {
		t.Fatalf("unexpected pane heights ah=%d th=%d", ah, sp.terminal.height)
	}

	// Agent first content row (y=1), col offset by the left border.
	if pane, row, col, ok := sp.HitTest(3, 1); !ok || pane != FocusAgent || row != 0 || col != 2 {
		t.Fatalf("agent top: pane=%d row=%d col=%d ok=%v", pane, row, col, ok)
	}
	// Agent last content row (y=ah).
	if pane, row, _, ok := sp.HitTest(1, ah); !ok || pane != FocusAgent || row != ah-1 {
		t.Fatalf("agent bottom: pane=%d row=%d ok=%v (ah=%d)", pane, row, ok, ah)
	}
	// Gap (agent bottom border + terminal title) is not selectable.
	if _, _, _, ok := sp.HitTest(1, ah+1); ok {
		t.Fatal("gap row must not hit a content pane")
	}
	// Terminal first content row (y=ah+3).
	if pane, row, col, ok := sp.HitTest(10, ah+3); !ok || pane != FocusTerminal || row != 0 || col != 9 {
		t.Fatalf("terminal top: pane=%d row=%d col=%d ok=%v", pane, row, col, ok)
	}
	// Left border column and past the right edge are not content.
	if _, _, _, ok := sp.HitTest(0, 1); ok {
		t.Fatal("left border must not hit content")
	}
	if _, _, _, ok := sp.HitTest(sp.agent.width+1, 1); ok {
		t.Fatal("past right edge must not hit content")
	}
	// Diff overlay disables selection hit-testing.
	sp.diffVisible = true
	if _, _, _, ok := sp.HitTest(3, 1); ok {
		t.Fatal("diff overlay must disable hit-testing")
	}
}

func TestPaneSelectionRoundTrip(t *testing.T) {
	p := NewPreviewPane()
	p.SetSize(40, 6)
	p.previewState = previewState{text: "hello world\nsecond line"}
	_ = p.String() // populates displayedPlain from the rendered content
	p.BeginSelection(0, 0)
	p.ExtendSelection(0, 5)
	if got := p.SelectedText(); got != "hello" {
		t.Fatalf("SelectedText = %q, want %q", got, "hello")
	}
	p.ClearSelection()
	if got := p.SelectedText(); got != "" {
		t.Fatalf("after clear SelectedText = %q, want empty", got)
	}
}
