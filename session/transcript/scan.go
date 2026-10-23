package transcript

import (
	"bytes"
	"io"
)

// chunk is how much of the file a read takes at a time.
const chunk = 64 << 10

// lastLineEnd is the offset just past the last newline among r's first
// size bytes, looked for within scanBudget of the end; 0 when there is none.
func lastLineEnd(r io.ReaderAt, size int64) (int64, error) {
	buf := make([]byte, chunk)
	for pos := size; pos > 0 && size-pos < scanBudget; {
		n := min(int64(chunk), pos)
		b := buf[:n]
		if _, err := r.ReadAt(b, pos-n); err != nil && err != io.EOF {
			return 0, err
		}
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			return pos - n + int64(i) + 1, nil
		}
		pos -= n
	}
	return 0, nil
}

// scanBack calls yield with each line of r's [from, to), newest first: its
// offset, and its bytes without the newline (nil for a line longer than
// lineLimit). from is the start of a line, and to sits just past a newline.
// It stops when yield returns false, at from, or once budget bytes have
// been read, and reports whether every line of the range was yielded.
func scanBack(r io.ReaderAt, from, to, budget int64, yield func(off int64, data []byte) bool) (bool, error) {
	if to <= from {
		return true, nil
	}
	pos := to - 1      // the newline at to-1 ends the newest line
	var parts [][]byte // the line in progress, from the chunks read before this one, latest first
	size := int64(0)   // the length of parts, kept or not
	read := int64(0)
	buf := make([]byte, chunk)
	for pos > from {
		n := min(int64(chunk), pos-from, budget-read)
		if n <= 0 {
			return false, nil
		}
		b := buf[:n]
		if _, err := r.ReadAt(b, pos-n); err != nil && err != io.EOF {
			return false, err
		}
		read += n
		base := pos - n
		end := len(b)
		for i := len(b) - 1; i >= 0; i-- {
			if b[i] != '\n' {
				continue
			}
			if !yield(base+int64(i)+1, line(b[i+1:end], parts, size)) {
				return false, nil
			}
			parts, size, end = nil, 0, i
		}
		size += int64(end)
		if end > 0 && size <= lineLimit {
			parts = append(parts, append([]byte(nil), b[:end]...))
		}
		pos = base
	}
	yield(from, line(nil, parts, size))
	return true, nil
}

// line joins a line's first bytes (head, from the chunk being read) and the
// rest of it (parts, latest first, size bytes in all), or returns nil when
// the whole is longer than lineLimit.
func line(head []byte, parts [][]byte, size int64) []byte {
	if int64(len(head))+size > lineLimit {
		return nil
	}
	out := make([]byte, 0, len(head)+int(size))
	out = append(out, head...)
	for i := len(parts) - 1; i >= 0; i-- {
		out = append(out, parts[i]...)
	}
	return out
}
