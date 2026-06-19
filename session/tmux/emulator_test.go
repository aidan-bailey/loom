package tmux

import "testing"

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
