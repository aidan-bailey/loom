package app

import (
	tea "charm.land/bubbletea/v2"
)

// keyMsgToBytes converts a Bubble Tea key press back into raw terminal
// bytes suitable for writing to a tmux PTY. Returns nil for unmappable
// keys. The output is byte-for-byte identical to the v1 implementation;
// app/keybytes_test.go pins that contract.
func keyMsgToBytes(msg tea.KeyPressMsg) []byte {
	// Alt modifier: prefix ESC, then the key's own bytes (Alt stripped).
	if msg.Mod.Contains(tea.ModAlt) {
		inner := keyMsgToBytes(tea.KeyPressMsg{
			Code: msg.Code,
			Text: msg.Text,
			Mod:  msg.Mod &^ tea.ModAlt,
		})
		if inner == nil {
			return nil
		}
		return append([]byte{0x1b}, inner...)
	}

	// Ctrl + letter → control byte (Ctrl+A=0x01 .. Ctrl+Z=0x1A).
	if msg.Mod.Contains(tea.ModCtrl) && msg.Code >= 'a' && msg.Code <= 'z' {
		return []byte{byte(msg.Code - 'a' + 1)}
	}

	// Special keys (with any modifiers) that map to escape sequences.
	if seq := keySequence(msg); seq != "" {
		return []byte(seq)
	}

	// Named control keys whose code is a single control byte.
	switch msg.Code {
	case tea.KeyEnter:
		return []byte{0x0D}
	case tea.KeyTab:
		return []byte{0x09}
	case tea.KeyEsc:
		return []byte{0x1B}
	case tea.KeyBackspace:
		return []byte{0x7F}
	case tea.KeySpace:
		return []byte{0x20}
	}

	// Printable text (runes), including multi-byte UTF-8.
	if msg.Text != "" {
		return []byte(msg.Text)
	}
	return nil
}

// keySequence maps navigation / function / modifier-arrow keys to their
// standard xterm escape sequences, honoring Shift/Ctrl modifiers that v2
// now carries on msg.Mod rather than as distinct key types.
func keySequence(msg tea.KeyPressMsg) string {
	shift := msg.Mod.Contains(tea.ModShift)
	ctrl := msg.Mod.Contains(tea.ModCtrl)
	switch msg.Code {
	case tea.KeyUp:
		switch {
		case shift:
			return "\x1b[1;2A"
		case ctrl:
			return "\x1b[1;5A"
		default:
			return "\x1b[A"
		}
	case tea.KeyDown:
		switch {
		case shift:
			return "\x1b[1;2B"
		case ctrl:
			return "\x1b[1;5B"
		default:
			return "\x1b[B"
		}
	case tea.KeyRight:
		switch {
		case shift:
			return "\x1b[1;2C"
		case ctrl:
			return "\x1b[1;5C"
		default:
			return "\x1b[C"
		}
	case tea.KeyLeft:
		switch {
		case shift:
			return "\x1b[1;2D"
		case ctrl:
			return "\x1b[1;5D"
		default:
			return "\x1b[D"
		}
	case tea.KeyTab:
		if shift {
			return "\x1b[Z" // shift+tab; plain tab → 0x09 in keyMsgToBytes
		}
		return ""
	case tea.KeyHome:
		return "\x1b[H"
	case tea.KeyEnd:
		return "\x1b[F"
	case tea.KeyPgUp:
		return "\x1b[5~"
	case tea.KeyPgDown:
		return "\x1b[6~"
	case tea.KeyDelete:
		return "\x1b[3~"
	case tea.KeyInsert:
		return "\x1b[2~"
	case tea.KeyF1:
		return "\x1bOP"
	case tea.KeyF2:
		return "\x1bOQ"
	case tea.KeyF3:
		return "\x1bOR"
	case tea.KeyF4:
		return "\x1bOS"
	case tea.KeyF5:
		return "\x1b[15~"
	case tea.KeyF6:
		return "\x1b[17~"
	case tea.KeyF7:
		return "\x1b[18~"
	case tea.KeyF8:
		return "\x1b[19~"
	case tea.KeyF9:
		return "\x1b[20~"
	case tea.KeyF10:
		return "\x1b[21~"
	case tea.KeyF11:
		return "\x1b[23~"
	case tea.KeyF12:
		return "\x1b[24~"
	}
	return ""
}
