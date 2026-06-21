package app

import "testing"

func TestCopyToClipboard_Guard(t *testing.T) {
	if copyToClipboard("") != nil {
		t.Fatal("empty text must return a nil cmd")
	}
	if copyToClipboard("hello") == nil {
		t.Fatal("non-empty text must return a cmd")
	}
}

func TestClipboardFallbackCmd_RuneCount(t *testing.T) {
	// "héllo" is 5 runes / 6 bytes; the reported count must be runes. The
	// clipboard write itself may fail headlessly — that's fine, err is ignored.
	msg := clipboardFallbackCmd("héllo")()
	cc, ok := msg.(clipboardCopiedMsg)
	if !ok {
		t.Fatalf("expected clipboardCopiedMsg, got %T", msg)
	}
	if cc.n != 5 {
		t.Fatalf("rune count = %d, want 5", cc.n)
	}
}
