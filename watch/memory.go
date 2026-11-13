package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
)

// MemoryFile is the watcher's memory, under the workspace's config dir
// ({repo}/.loom): what it has nudged, settled and escalated (spec §6).
const MemoryFile = "watch.json"

// memoryVersion is the version of watch.json this build reads and writes.
const memoryVersion = 1

// errMemoryNewer wraps LoadMemory's error for a file a newer loom wrote.
var errMemoryNewer = errors.New("watch.json is from a newer loom")

// Memory is watch.json: the counts the watcher must not lose, or it would
// re-nudge and re-escalate. Keys of PRs and Issues are GitHub numbers.
// Unknown top-level keys of the file are kept and written back. A Memory
// shares its maps with its copies: Clone before handing one to another
// goroutine.
type Memory struct {
	Version int                 `json:"version"`
	PRs     map[int]PRMemory    `json:"prs"`
	Issues  map[int]IssueMemory `json:"issues"`

	extra map[string]json.RawMessage
}

// PRMemory is what the watcher did on one PR. Nudges are keyed by kind and
// counted for the PR's life. Settled marks a PR closed unmerged that the
// watcher has already escalated for.
type PRMemory struct {
	Nudges  map[string]NudgeRecord `json:"nudges,omitempty"`
	Settled bool                   `json:"settled"`
}

// NudgeRecord is the nudges of one kind on one PR: how many, and the key and
// time of the last (the repeat guard's inputs, spec §4.3).
type NudgeRecord struct {
	N   int       `json:"n"`
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

// IssueMemory is what the watcher knows of one issue: whether it escalated,
// the fingerprint of the issue's body and trusted comments it last saw, and
// the reason of the escalation whose comment was posted (spec §4.4: a failed
// label is retried without commenting again), "" for none.
type IssueMemory struct {
	Escalated *Escalation `json:"escalated,omitempty"`
	Seen      string      `json:"seen,omitempty"`
	Commented string      `json:"commented,omitempty"`
}

// Escalation records when, and why, the watcher escalated an issue.
type Escalation struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// newMemory is the empty memory a missing file stands for.
func newMemory() Memory {
	return Memory{Version: memoryVersion, PRs: map[int]PRMemory{}, Issues: map[int]IssueMemory{}}
}

// LoadMemory reads the memory at path; with no file it returns empty memory.
// It never writes. A file that can't be read or decoded, has no version, or
// has a newer one than this build's (found before the rest is decoded,
// wrapping errMemoryNewer) is an error and is left as it is: acting without
// the counts would re-nudge and re-escalate.
func LoadMemory(path string) (Memory, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return newMemory(), nil
	}
	if err != nil {
		return Memory{}, fmt.Errorf("read %s: %w", path, err)
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Memory{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if head.Version > memoryVersion {
		return Memory{}, fmt.Errorf("%s has version %d, this loom reads up to %d: %w", path, head.Version, memoryVersion, errMemoryNewer)
	}
	if head.Version < 1 {
		return Memory{}, fmt.Errorf("%s has version %d: it must be at least 1", path, head.Version)
	}
	var m Memory
	if err := json.Unmarshal(data, &m); err != nil {
		return Memory{}, fmt.Errorf("parse %s: %w", path, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return Memory{}, fmt.Errorf("parse %s: %w", path, err)
	}
	for k, v := range top {
		if isMemoryKey(k) {
			continue
		}
		if m.extra == nil {
			m.extra = map[string]json.RawMessage{}
		}
		m.extra[k] = v
	}
	if m.PRs == nil {
		m.PRs = map[int]PRMemory{}
	}
	if m.Issues == nil {
		m.Issues = map[int]IssueMemory{}
	}
	return m, nil
}

// isMemoryKey reports whether k is one of the keys Memory decodes itself
// (encoding/json matches keys without regard to case).
func isMemoryKey(k string) bool {
	for _, known := range []string{"version", "prs", "issues"} {
		if strings.EqualFold(k, known) {
			return true
		}
	}
	return false
}

// SaveMemory writes m to path atomically (mode 0600, the directory made with
// mode 0700 when absent), through config.AtomicWriteFile. It stamps the
// version, writes {} for a nil map, and writes the unknown top-level keys
// LoadMemory found back unchanged.
func SaveMemory(path string, m Memory) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	prs, issues := m.PRs, m.Issues
	if prs == nil {
		prs = map[int]PRMemory{}
	}
	if issues == nil {
		issues = map[int]IssueMemory{}
	}
	top := make(map[string]json.RawMessage, len(m.extra)+3)
	for k, v := range m.extra {
		top[k] = v
	}
	for k, v := range map[string]any{"version": memoryVersion, "prs": prs, "issues": issues} {
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("encode %s: %w", MemoryFile, err)
		}
		top[k] = raw
	}
	data, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", MemoryFile, err)
	}
	return config.AtomicWriteFile(path, append(data, '\n'), 0o600)
}
