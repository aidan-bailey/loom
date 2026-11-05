package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The registry's file, and the newest version of it this build reads. It
// writes version 2 only while a belief's signature has a prerequisite part
// (dependent jobs spec §5.6), and 1 otherwise (fileVersion), so an older
// loom keeps reading a registry with none: it would compute such a belief's
// ID without the part and refuse the whole file.
const (
	BeliefsFile    = "beliefs.json"
	BeliefsVersion = 2
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
// making dir (mode 0700) when it is absent, and stamps its version
// (fileVersion).
func SaveBeliefs(dir string, bs Beliefs) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	bs.Version = bs.fileVersion()
	if bs.Beliefs == nil {
		bs.Beliefs = []Belief{}
	}
	return saveJSON(filepath.Join(dir, BeliefsFile), bs)
}

// fileVersion is the version bs is written as: 2 while a belief's signature
// has a prerequisite part, else 1.
func (bs Beliefs) fileVersion() int {
	for _, b := range bs.Beliefs {
		if b.After != "" {
			return 2
		}
	}
	return 1
}
