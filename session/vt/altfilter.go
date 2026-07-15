package vt

import (
	"bytes"
	"io"
)

// altScreenModes are the DEC private modes that switch to/from the
// alternate screen. A tmux client enters the alt screen at attach and
// never leaves, which would make x/vt accumulate scrollback in the
// (unreachable) alt-screen buffer — see the scrollback design doc,
// Amendment 1.
var altScreenModes = map[string]bool{"47": true, "1047": true, "1049": true}

// maxPendingSeq bounds the partial-sequence hold-back buffer; anything
// longer flushes through unfiltered rather than stalling the stream.
const maxPendingSeq = 64

// altScreenFilter is an io.Writer decorator that removes alt-screen
// enter/exit private-mode parameters from CSI ? ... h/l sequences and
// forwards everything else verbatim. It is stateful across Writes so
// sequences split between chunks are still recognized. Single-writer only
// (the tmux output pump); not safe for concurrent Write.
type altScreenFilter struct {
	dst  io.Writer
	pend []byte // partial escape sequence held back from the previous Write
}

// NewAltScreenFilter wraps dst, stripping alternate-screen mode switches
// from the byte stream. Install between the tmux output pump and the
// emulator.
func NewAltScreenFilter(dst io.Writer) io.Writer {
	return &altScreenFilter{dst: dst}
}

func (f *altScreenFilter) Write(p []byte) (int, error) {
	data := p
	if len(f.pend) > 0 {
		data = append(f.pend, p...) //nolint:gocritic // deliberate copy-join
		f.pend = nil
	}
	var out bytes.Buffer
	i := 0
	for i < len(data) {
		if data[i] != 0x1b {
			out.WriteByte(data[i])
			i++
			continue
		}
		seq, complete := scanEscape(data[i:])
		if !complete {
			if len(seq) <= maxPendingSeq {
				f.pend = append([]byte(nil), seq...)
				i += len(seq)
				continue
			}
			// Pathological: flush through rather than buffer forever.
			out.Write(seq)
			i += len(seq)
			continue
		}
		out.Write(rewritePrivateModeSeq(seq))
		i += len(seq)
	}
	if _, err := f.dst.Write(out.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

// scanEscape returns the escape sequence starting at data[0] == ESC and
// whether it is complete. Recognizes CSI (ESC [ ... final @-~) and OSC
// (ESC ] ... BEL or ESC \); anything else is treated as a two-byte
// sequence.
func scanEscape(data []byte) (seq []byte, complete bool) {
	if len(data) == 1 {
		return data, false
	}
	switch data[1] {
	case '[':
		for j := 2; j < len(data); j++ {
			if data[j] >= '@' && data[j] <= '~' {
				return data[:j+1], true
			}
		}
		return data, false
	case ']':
		for j := 2; j < len(data); j++ {
			if data[j] == 0x07 {
				return data[:j+1], true
			}
			if data[j] == 0x1b {
				if j+1 < len(data) && data[j+1] == '\\' {
					return data[:j+2], true
				}
				return data[:j], true // next ESC starts a new sequence
			}
		}
		return data, false
	default:
		return data[:2], true
	}
}

// rewritePrivateModeSeq removes alt-screen modes from a CSI ? ... h/l
// sequence, returning the sequence unchanged when it is not a private
// mode set/reset or contains no alt-screen modes. Returns nil when every
// parameter was an alt-screen mode.
func rewritePrivateModeSeq(seq []byte) []byte {
	// Shape: ESC [ ? params h|l
	if len(seq) < 5 || seq[1] != '[' || seq[2] != '?' {
		return seq
	}
	final := seq[len(seq)-1]
	if final != 'h' && final != 'l' {
		return seq
	}
	params := bytes.Split(seq[3:len(seq)-1], []byte(";"))
	kept := params[:0]
	removed := false
	for _, p := range params {
		if altScreenModes[string(p)] {
			removed = true
			continue
		}
		kept = append(kept, p)
	}
	if !removed {
		return seq
	}
	if len(kept) == 0 {
		return nil
	}
	out := append([]byte("\x1b[?"), bytes.Join(kept, []byte(";"))...)
	return append(out, final)
}
