package main

import (
	"context"
	"io"
	"os"
	"time"
)

// followLogs streams bytes appended to files (tail -F style, starting at the
// current end) until ctx is canceled. A file that shrinks is treated as
// rotated and read from the start.
func followLogs(ctx context.Context, w io.Writer, files []string, poll time.Duration) error {
	offsets := make(map[string]int64, len(files))
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			offsets[f] = st.Size()
		}
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, f := range files {
				offsets[f] = copyNew(w, f, offsets[f])
			}
		}
	}
}

func copyNew(w io.Writer, path string, offset int64) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return offset
	}
	if st.Size() < offset {
		offset = 0
	}
	if st.Size() == offset {
		return offset
	}
	fh, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer func() { _ = fh.Close() }()
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	n, _ := io.Copy(w, fh)
	return offset + n
}
