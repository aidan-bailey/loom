package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// StoreVersion is the version of jobs.json this build reads and writes.
const StoreVersion = 1

// Store is jobs.json: every job's record, and the counter for the next ID.
type Store struct {
	Version int      `json:"version"`
	NextID  JobID    `json:"next_id"`
	Jobs    []Record `json:"jobs"`
}

// ErrNewerStore is LoadStore's error for a jobs.json written by a newer loom.
var ErrNewerStore = errors.New("jobs.json is from a newer loom")

// LoadStore reads dir's jobs.json, where dir is <globalDir>/jobs. With no
// file it returns an empty store: version StoreVersion, NextID 1, no jobs. It
// never writes, and a file it cannot use is left as it is: an unreadable or
// invalid one is an error, and one with a newer version is an error wrapping
// ErrNewerStore, found before the rest is parsed, since a newer shape may not
// parse here. A job ID that is below 1 or repeated is invalid. NextID is
// raised past the highest ID on disk, so an ID is never reused.
func LoadStore(dir string) (Store, error) {
	path := filepath.Join(dir, StoreFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Store{Version: StoreVersion, NextID: 1}, nil
	}
	if err != nil {
		return Store{}, fmt.Errorf("read %s: %w", path, err)
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Store{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if head.Version > StoreVersion {
		return Store{}, fmt.Errorf("%s has version %d, this loom reads up to %d: %w", path, head.Version, StoreVersion, ErrNewerStore)
	}
	if head.Version < 1 {
		return Store{}, fmt.Errorf("%s has version %d: it must be at least 1", path, head.Version)
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return Store{}, fmt.Errorf("parse %s: %w", path, err)
	}
	seen := make(map[JobID]bool, len(s.Jobs))
	for _, r := range s.Jobs {
		if r.ID < 1 {
			return Store{}, fmt.Errorf("%s: job ID %d is below 1", path, r.ID)
		}
		if seen[r.ID] {
			return Store{}, fmt.Errorf("%s: job %d appears twice", path, r.ID)
		}
		seen[r.ID] = true
		s.NextID = max(s.NextID, r.ID+1)
	}
	s.NextID = max(s.NextID, 1)
	return s, nil
}

// SaveStore writes s as dir's jobs.json atomically (mode 0600), making dir
// (mode 0700) and its parents when they are absent. It stamps StoreVersion
// and writes no jobs as an empty list.
func SaveStore(dir string, s Store) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	s.Version = StoreVersion
	if s.Jobs == nil {
		s.Jobs = []Record{}
	}
	return saveJSON(filepath.Join(dir, StoreFile), s)
}
