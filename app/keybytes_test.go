package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestKeyMsgToBytes_Runes(t *testing.T) {
	assert.Equal(t, []byte("a"), keyMsgToBytes(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	assert.Equal(t, []byte("5"), keyMsgToBytes(tea.KeyPressMsg{Code: '5', Text: "5"}))
	assert.Equal(t, []byte("/"), keyMsgToBytes(tea.KeyPressMsg{Code: '/', Text: "/"}))
}

func TestKeyMsgToBytes_Enter(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyEnter}
	assert.Equal(t, []byte{0x0D}, keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_Backspace(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyBackspace}
	assert.Equal(t, []byte{0x7F}, keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_Tab(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyTab}
	assert.Equal(t, []byte{0x09}, keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_Escape(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyEsc}
	assert.Equal(t, []byte{0x1B}, keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_Space(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	assert.Equal(t, []byte{0x20}, keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_ArrowKeys(t *testing.T) {
	tests := []struct {
		name     string
		code     rune
		expected []byte
	}{
		{"Up", tea.KeyUp, []byte("\x1b[A")},
		{"Down", tea.KeyDown, []byte("\x1b[B")},
		{"Left", tea.KeyLeft, []byte("\x1b[D")},
		{"Right", tea.KeyRight, []byte("\x1b[C")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tea.KeyPressMsg{Code: tt.code}
			assert.Equal(t, tt.expected, keyMsgToBytes(msg))
		})
	}
}

func TestKeyMsgToBytes_CtrlKeys(t *testing.T) {
	// Ctrl+A = 0x01
	assert.Equal(t, []byte{0x01}, keyMsgToBytes(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}))
	// Ctrl+C = 0x03
	assert.Equal(t, []byte{0x03}, keyMsgToBytes(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}))
}

func TestKeyMsgToBytes_HomeEnd(t *testing.T) {
	assert.Equal(t, []byte("\x1b[H"), keyMsgToBytes(tea.KeyPressMsg{Code: tea.KeyHome}))
	assert.Equal(t, []byte("\x1b[F"), keyMsgToBytes(tea.KeyPressMsg{Code: tea.KeyEnd}))
}

func TestKeyMsgToBytes_Delete(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyDelete}
	assert.Equal(t, []byte("\x1b[3~"), keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_PageUpDown(t *testing.T) {
	assert.Equal(t, []byte("\x1b[5~"), keyMsgToBytes(tea.KeyPressMsg{Code: tea.KeyPgUp}))
	assert.Equal(t, []byte("\x1b[6~"), keyMsgToBytes(tea.KeyPressMsg{Code: tea.KeyPgDown}))
}

func TestKeyMsgToBytes_FunctionKeys(t *testing.T) {
	tests := []struct {
		name     string
		code     rune
		expected []byte
	}{
		{"F1", tea.KeyF1, []byte("\x1bOP")},
		{"F2", tea.KeyF2, []byte("\x1bOQ")},
		{"F5", tea.KeyF5, []byte("\x1b[15~")},
		{"F12", tea.KeyF12, []byte("\x1b[24~")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tea.KeyPressMsg{Code: tt.code}
			assert.Equal(t, tt.expected, keyMsgToBytes(msg))
		})
	}
}

func TestKeyMsgToBytes_AltKey(t *testing.T) {
	// Alt+a should produce ESC followed by 'a'
	msg := tea.KeyPressMsg{Code: 'a', Text: "a", Mod: tea.ModAlt}
	assert.Equal(t, []byte{0x1B, 'a'}, keyMsgToBytes(msg))

	// Alt+arrow should produce ESC + arrow sequence
	msg = tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}
	assert.Equal(t, []byte("\x1b\x1b[A"), keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_MultiByteRune(t *testing.T) {
	// "日" is a 3-byte UTF-8 character (U+65E5)
	msg := tea.KeyPressMsg{Code: '日', Text: "日"}
	assert.Equal(t, []byte("日"), keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_Insert(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyInsert}
	assert.Equal(t, []byte("\x1b[2~"), keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_ShiftTab(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	assert.Equal(t, []byte("\x1b[Z"), keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_ModifierArrows(t *testing.T) {
	assert.Equal(t, []byte("\x1b[1;2A"), keyMsgToBytes(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}))
	assert.Equal(t, []byte("\x1b[1;5C"), keyMsgToBytes(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}))
}

func TestKeyMsgToBytes_SpaceNoRunes(t *testing.T) {
	// KeySpace with no Text should still produce 0x20
	msg := tea.KeyPressMsg{Code: tea.KeySpace}
	assert.Equal(t, []byte{0x20}, keyMsgToBytes(msg))
}

func TestKeyMsgToBytes_Unknown(t *testing.T) {
	// A key with no mapping and no printable text should return nil.
	msg := tea.KeyPressMsg{Code: 0xF0000}
	assert.Nil(t, keyMsgToBytes(msg))
}
