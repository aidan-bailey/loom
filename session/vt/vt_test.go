package vt

import "testing"

// nopEmulator is a compile-time check that the interface is satisfiable
// and a stand-in for tests that don't need real emulation.
type nopEmulator struct{}

func (nopEmulator) Write(p []byte) (int, error)          { return len(p), nil }
func (nopEmulator) Resize(cols, rows int)                {}
func (nopEmulator) Render() string                       { return "" }
func (nopEmulator) Cursor() Cursor                       { return Cursor{} }
func (nopEmulator) Title() string                        { return "" }
func (nopEmulator) SetBellFunc(f func())                 {}
func (nopEmulator) FocusReportingEnabled() bool          { return false }
func (nopEmulator) Close() error                         { return nil }
func (nopEmulator) ScrollbackLen() int                   { return 0 }
func (nopEmulator) RenderWindow(offset, rows int) string { return "" }
func (nopEmulator) SetScrollbackSize(n int)              {}

var _ Emulator = nopEmulator{}

func TestCursorZeroValue(t *testing.T) {
	var c Cursor
	if c.X != 0 || c.Y != 0 || c.Visible {
		t.Fatalf("zero Cursor should be {0,0,false}, got %+v", c)
	}
}
