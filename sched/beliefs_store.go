package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The registry's file and the version of it this build reads and writes.
const (
	BeliefsFile    = "beliefs.json"
	BeliefsVersion = 1
)

// ErrNewerBeliefs is LoadBeliefs' error for a beliefs.json a newer loom wrote.
var ErrNewerBeliefs = errors.New("beliefs.json is from a newer loom")

// LoadBeliefs reads dir's beliefs.json, where dir is <globalDir>/jobs. With
// no file it returns an empty registry. It never writes, and a file it
// cannot use is left as it is: an unreadable or invalid one is an error, a
// newer version an error wrapping ErrNewerBeliefs, found before the rest is
// parsed, and a belief whose ID is not its signature's, or that appears
// twice, makes the file invalid.
func LoadBeliefs(dir string) (Beliefs, error) {
	path := filepath.Join(dir, BeliefsFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Beliefs{Version: BeliefsVersion}, nil
	}
	if err != nil {
		return Beliefs{}, fmt.Errorf("read %s: %w", path, err)
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Beliefs{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if head.Version > BeliefsVersion {
		return Beliefs{}, fmt.Errorf("%s has version %d, this loom reads up to %d: %w", path, head.Version, BeliefsVersion, ErrNewerBeliefs)
	}
	if head.Version < 1 {
		return Beliefs{}, fmt.Errorf("%s has version %d: it must be at least 1", path, head.Version)
	}
	var bs Beliefs
	if err := json.Unmarshal(data, &bs); err != nil {
		return Beliefs{}, fmt.Errorf("parse %s: %w", path, err)
	}
	seen := make(map[SignatureID]bool, len(bs.Beliefs))
	for _, b := range bs.Beliefs {
		if b.ID != b.Signature.ID() {
			return Beliefs{}, fmt.Errorf("%s: belief %s does not match its signature", path, b.ID)
		}
		if seen[b.ID] {
			return Beliefs{}, fmt.Errorf("%s: belief %s appears twice", path, b.ID)
		}
		seen[b.ID] = true
	}
	return bs, nil
}

// SaveBeliefs writes bs as dir's beliefs.json atomically (mode 0600),
// making dir (mode 0700) when it is absent, and stamps BeliefsVersion.
func SaveBeliefs(dir string, bs Beliefs) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	bs.Version = BeliefsVersion
	if bs.Beliefs == nil {
		bs.Beliefs = []Belief{}
	}
	return saveJSON(filepath.Join(dir, BeliefsFile), bs)
}
