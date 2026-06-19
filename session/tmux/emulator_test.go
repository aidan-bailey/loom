package tmux

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session/vt"
	"github.com/stretchr/testify/require"
)

func TestNewEmulator_DefaultOnUnix(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "")
	if e := newEmulator(80, 24); e == nil {
		t.Fatal("unix default should produce a non-nil emulator")
	}
}

func TestNewEmulator_SnapshotKillSwitch(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "snapshot")
	if e := newEmulator(80, 24); e != nil {
		t.Fatal("LOOM_PANE_RENDERER=snapshot must force the nil (capture-pane) fallback")
	}
}

func TestRenderEmulator_NilWhenUnset(t *testing.T) {
	ts := NewTmuxSession("emu-nil", "prog")
	if _, ok := ts.RenderEmulator(); ok {
		t.Fatal("RenderEmulator must report ok=false when no emulator is wired")
	}
}

func TestRenderEmulator_ReadsWiredEmulator(t *testing.T) {
	ts := NewTmuxSession("emu-set", "prog")
	ts.stateMu.Lock()
	ts.emu = vt.NewXVT(80, 24)
	ts.stateMu.Unlock()
	_, _ = ts.emu.Write([]byte("hi"))
	s, ok := ts.RenderEmulator()
	if !ok || !containsText(s, "hi") {
		t.Fatalf("RenderEmulator should return the emulator screen; ok=%v s=%q", ok, s)
	}
}

func TestOutputPump_FeedsEmulator(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	ts := NewTmuxSession("emu-pump", "prog")
	ts.stateMu.Lock()
	ts.emu = vt.NewXVT(80, 24)
	ts.stateMu.Unlock()

	ts.startOutputPump(r)
	t.Cleanup(func() { ts.signalPumpStop(r); ts.waitPumpExit() })

	_, _ = w.WriteString("pumped-text")
	require.Eventually(t, func() bool {
		s, ok := ts.RenderEmulator()
		return ok && containsText(s, "pumped-text")
	}, time.Second, 10*time.Millisecond, "pump should write ptmx bytes into the emulator")
}

// TestRestore_BuildsEmulator_CloseTearsDown verifies the attach-lifecycle:
// Restore wires a fresh emulator on the new ptmx; Close tears it down.
func TestRestore_BuildsEmulator_CloseTearsDown(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "")
	noop := cmd_test.MockCmdExec{RunFunc: func(*exec.Cmd) error { return nil }}
	ts := newTmuxSession("emu-restore", "prog", NewMockPtyFactory(t), noop)

	require.NoError(t, ts.Restore())
	if _, ok := ts.RenderEmulator(); !ok {
		t.Fatal("Restore should wire an emulator on unix")
	}

	require.NoError(t, ts.Close())
	if _, ok := ts.RenderEmulator(); ok {
		t.Fatal("Close should tear down the emulator")
	}
}

func TestSetDetachedSize_ResizesEmulatorAndRecordsGeometry(t *testing.T) {
	ts := NewTmuxSession("emu-resize", "prog")
	ts.stateMu.Lock()
	ts.emu = vt.NewXVT(80, 24)
	ts.stateMu.Unlock()
	for i := 0; i < 20; i++ {
		_, _ = ts.emu.Write([]byte("line\r\n"))
	}
	// No ptmx wired, so updateWindowSize returns an error we ignore — the
	// emulator resize and geometry recording happen first regardless.
	_ = ts.SetDetachedSize(80, 10)

	ts.stateMu.Lock()
	gotCols, gotRows := ts.lastCols, ts.lastRows
	ts.stateMu.Unlock()
	if gotCols != 80 || gotRows != 10 {
		t.Fatalf("geometry not recorded: got %dx%d", gotCols, gotRows)
	}
	s, _ := ts.RenderEmulator()
	rows := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(rows) > 10 {
		t.Fatalf("emulator should be 10 rows after resize, got %d", len(rows))
	}
}

func TestRenderWindow_NilWhenUnset(t *testing.T) {
	ts := NewTmuxSession("rw-nil", "prog")
	if _, ok := ts.RenderWindow(0, 5); ok {
		t.Fatal("RenderWindow must report ok=false with no emulator wired")
	}
	if _, ok := ts.ScrollbackLen(); ok {
		t.Fatal("ScrollbackLen must report ok=false with no emulator wired")
	}
}

func TestRenderWindow_ReadsEmulator(t *testing.T) {
	ts := NewTmuxSession("rw-set", "prog")
	ts.stateMu.Lock()
	ts.emu = vt.NewXVT(20, 5)
	ts.stateMu.Unlock()
	for i := 0; i < 10; i++ {
		_, _ = ts.emu.Write([]byte("row\r\n"))
	}
	if n, ok := ts.ScrollbackLen(); !ok || n <= 0 {
		t.Fatalf("ScrollbackLen should be >0 after writing rows; n=%d ok=%v", n, ok)
	}
	if s, ok := ts.RenderWindow(0, 3); !ok || !containsText(s, "row") {
		t.Fatalf("RenderWindow should return content; ok=%v s=%q", ok, s)
	}
}

// containsText checks visible text presence; rendered output may carry SGR,
// but a substring check on plain ASCII is sufficient for these assertions.
func containsText(rendered, want string) bool {
	return strings.Contains(rendered, want)
}
