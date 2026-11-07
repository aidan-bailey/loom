package sched

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
)

// ReadLogEnd reads the last limit bytes of the file at path; partial says
// the file's start was left out. A file that does not exist is an empty log
// (a queued job has none yet), not an error.
func ReadLogEnd(path string, limit int64) (data []byte, partial bool, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	start := int64(0)
	if st.Size() > limit {
		start, partial = st.Size()-limit, true
	}
	data, err = readAt(f, start, st.Size()-start)
	if err != nil {
		return nil, false, err
	}
	return data, partial, nil
}

// LogRead is a read of a job's output.log: Data is the file's bytes from
// Start to End, and Size the file's size when it was read. Cut says the
// read began past the file's start, moved forward to a line's start; Reset
// says the offset asked for lay past the file's end (the file was
// replaced), so the read is a fresh tail.
type LogRead struct {
	Data  []byte
	Start int64
	End   int64
	Size  int64
	Cut   bool
	Reset bool
}

// ReadLog reads the file at path: its tail when after < 0 (the last limit
// bytes, cut forward past the first newline when that is not the whole
// file), or else the bytes from after, at most limit of them, so a read
// that stops short of the end continues from End. An after past the file's
// size reads a fresh tail with Reset. A file that does not exist is an
// empty read, not an error.
func ReadLog(path string, after, limit int64) (LogRead, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return LogRead{}, nil
	}
	if err != nil {
		return LogRead{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return LogRead{}, err
	}
	size := st.Size()
	r := LogRead{Size: size}
	if after > size {
		after, r.Reset = -1, true
	}
	if after >= 0 {
		n := min(size-after, limit)
		if r.Data, err = readAt(f, after, n); err != nil {
			return LogRead{}, err
		}
		r.Start = after
		r.End = after + int64(len(r.Data))
		return r, nil
	}
	start := max(0, size-limit)
	data, err := readAt(f, start, size-start)
	if err != nil {
		return LogRead{}, err
	}
	if start > 0 {
		r.Cut = true
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
			start += int64(i + 1)
		}
	}
	r.Data, r.Start, r.End = data, start, start+int64(len(data))
	return r, nil
}

// readAt reads n bytes of f from off, fewer when the file ends first.
func readAt(f *os.File, off, n int64) ([]byte, error) {
	data := make([]byte, n)
	got, err := f.ReadAt(data, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return data[:got], nil
}
