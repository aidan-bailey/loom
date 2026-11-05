package feedback

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aidan-bailey/loom/config"
)

// Version is the version of feedback.json this build reads and writes.
const Version = 1

// StoreFile is the inbox's file under <globalDir>/feedback.
const StoreFile = "feedback.json"

// ErrNewer is Load's error for a feedback.json a newer loom wrote.
var ErrNewer = errors.New("feedback.json is from a newer loom")

// Load reads dir's feedback.json; with none it returns Empty(). It never
// writes. An unreadable or invalid file, or one with a newer version (found
// before the rest is parsed, wrapping ErrNewer), is an error. A note or
// thread ID below 1 or repeated is invalid; the counters are raised past
// the highest IDs.
func Load(dir string) (Inbox, error) {
	path := filepath.Join(dir, StoreFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Empty(), nil
	}
	if err != nil {
		return Inbox{}, fmt.Errorf("read %s: %w", path, err)
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Inbox{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if head.Version > Version {
		return Inbox{}, fmt.Errorf("%s has version %d, this loom reads up to %d: %w", path, head.Version, Version, ErrNewer)
	}
	if head.Version < 1 {
		return Inbox{}, fmt.Errorf("%s has version %d: it must be at least 1", path, head.Version)
	}
	var in Inbox
	if err := json.Unmarshal(data, &in); err != nil {
		return Inbox{}, fmt.Errorf("parse %s: %w", path, err)
	}
	notes := map[NoteID]bool{}
	for _, n := range in.Notes {
		if n.ID < 1 || notes[n.ID] {
			return Inbox{}, fmt.Errorf("%s: note ID %d is below 1 or repeated", path, n.ID)
		}
		notes[n.ID] = true
		in.NextNote = max(in.NextNote, n.ID+1)
	}
	threads := map[ThreadID]bool{}
	for _, t := range in.Threads {
		if t.ID < 1 || threads[t.ID] {
			return Inbox{}, fmt.Errorf("%s: thread ID %d is below 1 or repeated", path, t.ID)
		}
		threads[t.ID] = true
		in.NextThread = max(in.NextThread, t.ID+1)
	}
	in.NextNote, in.NextThread = max(in.NextNote, 1), max(in.NextThread, 1)
	return in, nil
}

// Save writes in as dir's feedback.json atomically (mode 0600), making dir
// (mode 0700) when it is absent. It stamps Version.
func Save(dir string, in Inbox) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	in.Version = Version
	if in.Notes == nil {
		in.Notes = []Note{}
	}
	if in.Threads == nil {
		in.Threads = []Thread{}
	}
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", StoreFile, err)
	}
	return config.AtomicWriteFile(filepath.Join(dir, StoreFile), append(data, '\n'), 0o600)
}
