package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
	"time"
)

// ErrInstanceNotFound signals that a storage mutation (delete, update)
// referenced a title that is no longer persisted. Callers performing
// idempotent cleanup — e.g. the kill path, where Kill() has already
// destroyed tmux + worktree before DeleteInstance runs — can match it
// with errors.Is to distinguish "already gone" from a real write error.
var ErrInstanceNotFound = errors.New("instance not found")

// CurrentSchemaVersion is the schema version written by the current
// binary. Any on-disk InstanceData with a lower SchemaVersion is routed
// through storage_migrate.go's Migrate before use.
const CurrentSchemaVersion = 1

// InstanceData represents the serializable data of an Instance.
//
// SchemaVersion is tracked for forward-compatible migrations. A missing
// field (zero) is interpreted as v0 (pre-versioning); migrations.go
// upgrades it to CurrentSchemaVersion at decode time.
type InstanceData struct {
	SchemaVersion int `json:"schema_version,omitempty"`

	Title     string    `json:"title"`
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	Status    Status    `json:"status"`
	Height    int       `json:"height"`
	Width     int       `json:"width"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	AutoYes   bool      `json:"auto_yes"`

	Program             string          `json:"program"`
	Worktree            GitWorktreeData `json:"worktree"`
	DiffStats           DiffStatsData   `json:"diff_stats"`
	IsWorkspaceTerminal bool            `json:"is_workspace_terminal"`
}

// GitWorktreeData represents the serializable data of a GitWorktree
type GitWorktreeData struct {
	RepoPath         string `json:"repo_path"`
	WorktreePath     string `json:"worktree_path"`
	SessionName      string `json:"session_name"`
	BranchName       string `json:"branch_name"`
	BaseCommitSHA    string `json:"base_commit_sha"`
	IsExistingBranch bool   `json:"is_existing_branch"`
}

// DiffStatsData represents the serializable data of a DiffStats
type DiffStatsData struct {
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Content string `json:"content"`
}

// Storage handles saving and loading instances using the state interface
type Storage struct {
	state     config.InstanceStorage
	configDir string

	// unrecovered holds raw InstanceData for records that failed
	// ReconcileAndRestore during the most recent LoadAndReconcile pass.
	// SaveInstances merges these back into the persisted payload so a
	// transient reconcile failure (tmux flake, bad data) does not
	// permanently delete the record from state.json — the next launch
	// gets another chance to reconcile. DeleteInstance and
	// DeleteAllInstances clear matching entries so a user-initiated
	// delete is not silently undone by the merge.
	unrecovered []InstanceData
}

// NewStorage creates a new storage instance.
// configDir is the workspace config directory injected into loaded instances.
func NewStorage(state config.InstanceStorage, configDir string) (*Storage, error) {
	return &Storage{
		state:     state,
		configDir: configDir,
	}, nil
}

// SaveInstances saves the list of instances to disk.
// Callers are responsible for filtering out instances that should not be
// persisted (e.g. Ready-but-not-yet-configured, Deleting) via
// persistableInstances at the call site. Filtering here on Instance.Started()
// is unsafe because Kill() flips started=false early (before tmux/worktree
// teardown), so a save during the kill window would silently drop the
// instance from disk and cause DeleteInstance to fail with ErrInstanceNotFound.
//
// Unrecovered records from the most recent LoadAndReconcile pass are
// appended to the payload (deduped by title — a live record always wins)
// so reconcile failures do not silently delete persisted state.
func (s *Storage) SaveInstances(instances []*Instance) error {
	data := make([]InstanceData, 0, len(instances)+len(s.unrecovered))
	liveTitles := make(map[string]struct{}, len(instances))
	for _, instance := range instances {
		snap := instance.ToInstanceData()
		liveTitles[snap.Title] = struct{}{}
		data = append(data, snap)
	}
	for _, d := range s.unrecovered {
		if _, collision := liveTitles[d.Title]; collision {
			continue
		}
		data = append(data, d)
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal instances: %w", err)
	}

	return s.state.SaveInstances(jsonData)
}

// LoadInstances loads the list of instances from disk
func (s *Storage) LoadInstances() ([]*Instance, error) {
	instancesData, err := MigrateAll(s.state.GetInstances())
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal instances: %w", err)
	}

	instances := make([]*Instance, len(instancesData))
	for i, data := range instancesData {
		instance, err := FromInstanceData(data, s.configDir)
		if err != nil {
			return nil, fmt.Errorf("failed to create instance %s: %w", data.Title, err)
		}
		instances[i] = instance
	}

	return instances, nil
}

// LoadAndReconcile loads instance data from disk and reconciles each instance
// against the live tmux/worktree state. Unlike LoadInstances, a single failing
// instance is logged and skipped rather than aborting the whole load. This is
// the correct entry point for any caller that can tolerate reconciliation side
// effects (killing orphan tmux sessions, marking instances paused).
//
// Records that fail ReconcileAndRestore are stashed in s.unrecovered so the
// next SaveInstances preserves them on disk. Previously such failures led
// to permanent data loss: the failed record was silently omitted from the
// live list, and the next save overwrote state.json with only the survivors.
func (s *Storage) LoadAndReconcile(cmdExec internalexec.Executor) ([]*Instance, error) {
	data, err := s.LoadInstanceData()
	if err != nil {
		return nil, err
	}
	titles := make([]string, 0, len(data))
	for _, d := range data {
		titles = append(titles, d.Title)
	}
	tmux.RenameLegacySessions(titles, cmdExec)
	instances := make([]*Instance, 0, len(data))
	s.unrecovered = s.unrecovered[:0]
	for _, d := range data {
		inst, err := ReconcileAndRestore(d, s.configDir, cmdExec)
		if err != nil {
			log.For("session").Error("reconcile_failed", "title", d.Title, "err", err, "action", "preserved_for_retry")
			s.unrecovered = append(s.unrecovered, d)
			continue
		}
		instances = append(instances, inst)
	}
	return instances, nil
}

// DeleteInstance removes an instance from storage.
// Operates on raw InstanceData so it does not construct live Instance objects
// (which would open tmux attach PTYs for every remaining running instance).
func (s *Storage) DeleteInstance(title string) error {
	data, err := s.LoadInstanceData()
	if err != nil {
		return fmt.Errorf("failed to load instances: %w", err)
	}

	found := false
	filtered := make([]InstanceData, 0, len(data))
	for _, d := range data {
		if d.Title == title {
			found = true
			continue
		}
		filtered = append(filtered, d)
	}

	if !found {
		return fmt.Errorf("%w: %s", ErrInstanceNotFound, title)
	}

	// Also drop any matching entry from the in-memory unrecovered cache so
	// a follow-up SaveInstances does not resurrect the just-deleted record.
	s.dropUnrecovered(title)

	return s.saveInstanceData(filtered)
}

// UpdateInstance replaces the persisted record for an existing instance.
// Uses the in-memory snapshot of the provided instance and the raw-data
// load path so the other stored entries are never reconstructed.
func (s *Storage) UpdateInstance(instance *Instance) error {
	data, err := s.LoadInstanceData()
	if err != nil {
		return fmt.Errorf("failed to load instances: %w", err)
	}

	snap := instance.ToInstanceData()
	found := false
	for i, d := range data {
		if d.Title == snap.Title {
			data[i] = snap
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("%w: %s", ErrInstanceNotFound, snap.Title)
	}

	return s.saveInstanceData(data)
}

// saveInstanceData marshals raw InstanceData and writes it through the
// underlying state, bypassing the live-Instance serialization path.
func (s *Storage) saveInstanceData(data []InstanceData) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal instances: %w", err)
	}
	return s.state.SaveInstances(jsonData)
}

// LoadInstanceData loads raw serialized instance data without constructing Instance objects.
// Used by reconciliation to inspect state before deciding how to restore.
// All records pass through Migrate so callers receive CurrentSchemaVersion data.
func (s *Storage) LoadInstanceData() ([]InstanceData, error) {
	data, err := MigrateAll(s.state.GetInstances())
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal instances: %w", err)
	}
	return data, nil
}

// DeleteAllInstances removes all stored instances
func (s *Storage) DeleteAllInstances() error {
	s.unrecovered = nil
	return s.state.DeleteAllInstances()
}

// UnrecoveredWorktreePaths returns the set of worktree paths currently
// held in the unrecovered cache. Orphan discovery uses this so a
// preserved-but-failed record's worktree is not also surfaced as an
// orphan candidate, which would let the user re-recover it under a
// different title and produce a duplicate state.json entry.
func (s *Storage) UnrecoveredWorktreePaths() map[string]bool {
	if len(s.unrecovered) == 0 {
		return nil
	}
	out := make(map[string]bool, len(s.unrecovered))
	for _, d := range s.unrecovered {
		if p := d.Worktree.WorktreePath; p != "" {
			out[p] = true
		}
	}
	return out
}

// dropUnrecovered removes any entry from the unrecovered cache whose
// Title matches the given title. Used by DeleteInstance so a
// user-initiated delete is not silently undone on the next save.
func (s *Storage) dropUnrecovered(title string) {
	if len(s.unrecovered) == 0 {
		return
	}
	filtered := s.unrecovered[:0]
	for _, d := range s.unrecovered {
		if d.Title == title {
			continue
		}
		filtered = append(filtered, d)
	}
	s.unrecovered = filtered
}
