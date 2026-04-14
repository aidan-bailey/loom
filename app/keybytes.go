package app

import (
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// keyMsgToBytes converts a Bubble Tea KeyMsg back into raw terminal bytes
// suitable for writing to a tmux PTY. Returns nil for unmappable keys.
func keyMsgToBytes(msg tea.KeyMsg) []byte {
	// Handle Alt modifier: prefix with ESC, then the key's own bytes.
	if msg.Alt {
		inner := keyMsgToBytes(tea.KeyMsg{Type: msg.Type, Runes: msg.Runes})
		if inner == nil {
			return nil
		}
		return append([]byte{0x1b}, inner...)
	}

	// Control keys whose KeyType value IS the byte value (0x00-0x1F, 0x7F).
	// This covers Ctrl+A (0x01) through Ctrl+Z (0x1A), Enter (0x0D),
	// Tab (0x09), Escape (0x1B), and Backspace (0x7F).
	if int(msg.Type) >= 0 && int(msg.Type) <= 31 {
		return []byte{byte(msg.Type)}
	}
	if msg.Type == tea.KeyBackspace { // 127
		return []byte{0x7F}
	}

	// Special keys that map to escape sequences.
	if seq, ok := keyTypeSequences[msg.Type]; ok {
		return []byte(seq)
	}

	// Regular runes (including Space, which Bubble Tea reports as KeySpace
	// but still carries the rune).
	if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
		if len(msg.Runes) == 0 {
			if msg.Type == tea.KeySpace {
				return []byte{0x20}
			}
			return nil
		}
		buf := make([]byte, 0, len(msg.Runes)*utf8.UTFMax)
		for _, r := range msg.Runes {
			var encoded [utf8.UTFMax]byte
			n := utf8.EncodeRune(encoded[:], r)
			buf = append(buf, encoded[:n]...)
		}
		return buf
	}

	// Unknown / unmappable key.
	return nil
}

// keyTypeSequences maps Bubble Tea KeyTypes to their standard xterm escape
// sequences. Only keys that produce multi-byte escape sequences are listed
// here; control characters and runes are handled separately above.
var keyTypeSequences = map[tea.KeyType]string{
	// Arrow keys
	tea.KeyUp:    "\x1b[A",
	tea.KeyDown:  "\x1b[B",
	tea.KeyRight: "\x1b[C",
	tea.KeyLeft:  "\x1b[D",

	// Navigation
	tea.KeyHome:   "\x1b[H",
	tea.KeyEnd:    "\x1b[F",
	tea.KeyPgUp:   "\x1b[5~",
	tea.KeyPgDown: "\x1b[6~",
	tea.KeyDelete: "\x1b[3~",
	tea.KeyInsert: "\x1b[2~",

	// Modifier+arrow (xterm standard sequences)
	tea.KeyShiftTab:   "\x1b[Z",
	tea.KeyShiftUp:    "\x1b[1;2A",
	tea.KeyShiftDown:  "\x1b[1;2B",
	tea.KeyShiftRight: "\x1b[1;2C",
	tea.KeyShiftLeft:  "\x1b[1;2D",
	tea.KeyCtrlUp:     "\x1b[1;5A",
	tea.KeyCtrlDown:   "\x1b[1;5B",
	tea.KeyCtrlRight:  "\x1b[1;5C",
	tea.KeyCtrlLeft:   "\x1b[1;5D",

	// Function keys (standard xterm sequences)
	tea.KeyF1:  "\x1bOP",
	tea.KeyF2:  "\x1bOQ",
	tea.KeyF3:  "\x1bOR",
	tea.KeyF4:  "\x1bOS",
	tea.KeyF5:  "\x1b[15~",
	tea.KeyF6:  "\x1b[17~",
	tea.KeyF7:  "\x1b[18~",
	tea.KeyF8:  "\x1b[19~",
	tea.KeyF9:  "\x1b[20~",
	tea.KeyF10: "\x1b[21~",
	tea.KeyF11: "\x1b[23~",
	tea.KeyF12: "\x1b[24~",
}
