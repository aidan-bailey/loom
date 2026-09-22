package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
	"sync"
	"time"
)

// ErrInstanceNotFound signals that a storage mutation (delete, update)
// referenced a title that is no longer persisted. Callers performing
// idempotent cleanup — e.g. the kill path, where Kill() has already
// destroyed tmux + worktree before DeleteInstance runs — can match it
// with errors.Is to distinguish "already gone" from a real write error.
var ErrInstanceNotFound = errors.New("instance not found")

// ErrStorageLoadFailed signals that a write was refused because the
// persisted instance payload could not be decoded as a whole (not a JSON
// array). Writing anyway would replace records this Storage never saw
// with whatever the caller holds — usually nothing. The latch clears on
// the next successful load, or on DeleteAllInstances (the explicit wipe).
var ErrStorageLoadFailed = errors.New("instance storage could not be read; refusing to overwrite it")

// CurrentSchemaVersion is the schema version written by the current
// binary. Any on-disk InstanceData with a lower SchemaVersion is routed
// through storage_migrate.go's Migrate before use.
const CurrentSchemaVersion = 6

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

	Program             string          `json:"program"`
	HeadroomProxy       bool            `json:"headroom_proxy,omitempty"`
	CacheTTL1h          bool            `json:"cache_ttl_1h,omitempty"`
	Worktree            GitWorktreeData `json:"worktree"`
	DiffStats           DiffStatsData   `json:"diff_stats"`
	IsWorkspaceTerminal bool            `json:"is_workspace_terminal"`
	// Issue is the GitHub issue number this session was started from
	// (0 = not linked). Set once at creation by the issue picker or the
	// #n prompt shorthand; read by the GitHub poller join.
	Issue int `json:"issue,omitempty"`
}

// GitWorktreeData represents the serializable data of a GitWorktree
type GitWorktreeData struct {
	RepoPath         string `json:"repo_path"`
	WorktreePath     string `json:"worktree_path"`
	SessionName      string `json:"session_name"`
	BranchName       string `json:"branch_name"`
	BaseCommitSHA    string `json:"base_commit_sha"`
	IsExistingBranch bool   `json:"is_existing_branch"`
	// StashRef is the commit SHA of a pending git stash created by
	// Instance.Pause for this worktree's uncommitted changes, or ""
	// if none is pending (nothing was dirty at pause time, or Resume
	// already applied and cleared it). Added in schema v3.
	StashRef string `json:"stash_ref,omitempty"`
}

// DiffStatsData represents the serializable data of a DiffStats
type DiffStatsData struct {
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Content string `json:"content"`
}

// Storage handles saving and loading instances using the state interface
type Storage struct {
	// mu serializes every Storage operation. Persistence runs from
	// tea.Cmd goroutines (pause/resume save, kill delete), which Bubble
	// Tea executes concurrently with each other and with Update — so two
	// saves, or a save racing a delete, can otherwise interleave their
	// read-modify-write of the backing store and corrupt s.unrecovered.
	// Held across the underlying state I/O; that I/O is fast (in-memory
	// state plus one AtomicWriteFile) and never re-enters Storage.
	mu sync.Mutex

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

	// undecodable holds records this binary cannot decode (corrupt, or
	// written by a newer loom — see MigrateAll): their original bytes plus
	// the two fields the sweeps and orphan discovery need, parsed once at
	// load. Replaced on every successful load, and appended verbatim to
	// every write so they survive a downgrade round-trip untouched. Never
	// deduped against live titles (callers guard new titles instead).
	undecodable []undecodableRecord
	// loadErr is the error from the last load when the payload as a whole
	// could not be decoded; nil after a successful one. While set, every
	// write is refused with ErrStorageLoadFailed.
	loadErr error
	// loaded reports whether any load has run. A write on a never-loaded
	// Storage loads first, so undecodable records (and loadErr) are known
	// before anything is written.
	loaded bool
}

// undecodableRecord is one record MigrateAll rejected: raw is written back
// verbatim; title and worktreePath are decoded best-effort (each on its
// own, so a garbled title doesn't cost the path) and "" when unreadable.
type undecodableRecord struct {
	raw          json.RawMessage
	title        string
	worktreePath string
}

// newUndecodableRecord parses the best-effort metadata of a rejected record.
func newUndecodableRecord(raw json.RawMessage) undecodableRecord {
	rec := undecodableRecord{raw: raw}
	var t struct {
		Title string `json:"title"`
	}
	if json.Unmarshal(raw, &t) == nil {
		rec.title = t.Title
	}
	var w struct {
		Worktree struct {
			WorktreePath string `json:"worktree_path"`
		} `json:"worktree"`
	}
	if json.Unmarshal(raw, &w) == nil {
		rec.worktreePath = w.Worktree.WorktreePath
	}
	return rec
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
// so reconcile failures do not silently delete persisted state. Records
// this binary cannot decode follow them verbatim (see writeLocked), and
// the save is refused with ErrStorageLoadFailed while the payload as a
// whole is unreadable.
func (s *Storage) SaveInstances(instances []*Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return s.writeLocked(data)
}

// LoadInstances loads the list of instances from disk. Records this
// binary cannot decode are skipped (and preserved on the next write).
func (s *Storage) LoadInstances() ([]*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	instancesData, err := s.loadInstanceDataLocked()
	if err != nil {
		return nil, err
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
// Records that cannot be decoded at all are likewise skipped here and
// preserved verbatim (UndecodableCount); only a payload that is not a JSON
// array fails the load, which also latches every write shut until a later
// load succeeds.
func (s *Storage) LoadAndReconcile(cmdExec internalexec.Executor) ([]*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadInstanceDataLocked()
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

// UnrecoveredTitles returns the titles of records that failed the last
// LoadAndReconcile pass. These are preserved on disk and retried on the
// next load, but never appear in the live list — callers use this to
// tell the user they exist at all.
func (s *Storage) UnrecoveredTitles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	titles := make([]string, 0, len(s.unrecovered))
	for _, d := range s.unrecovered {
		titles = append(titles, d.Title)
	}
	return titles
}

// PreservedTitles returns the titles of every record preserved on disk but
// absent from the live list: the unrecovered cache, plus each undecodable
// record's title, decoded best-effort (a missing, empty or unreadable title
// is skipped). Title-keyed sweeps — the server-wide orphan tmux sweep and
// the subagent hooks sweep — must spare these, or a preserved record keeps
// its JSON but loses its still-running agent (e.g. a newer loom's session
// after a downgrade, or a record whose reconcile failed transiently).
func (s *Storage) PreservedTitles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	titles := make([]string, 0, len(s.unrecovered)+len(s.undecodable))
	for _, d := range s.unrecovered {
		titles = append(titles, d.Title)
	}
	for _, rec := range s.undecodable {
		if rec.title != "" {
			titles = append(titles, rec.title)
		}
	}
	return titles
}

// UndecodableCount returns how many persisted records the last successful
// load could not decode. Like unrecovered records they are preserved on
// disk but never appear in the live list, so callers surface the count.
func (s *Storage) UndecodableCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.undecodable)
}

// DeleteInstance removes an instance from storage.
// Operates on raw InstanceData so it does not construct live Instance objects
// (which would open tmux attach PTYs for every remaining running instance).
func (s *Storage) DeleteInstance(title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadForWriteLocked()
	if err != nil {
		return err
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

	return s.writeLocked(filtered)
}

// UpdateInstance replaces the persisted record for an existing instance.
// Uses the in-memory snapshot of the provided instance and the raw-data
// load path so the other stored entries are never reconstructed.
func (s *Storage) UpdateInstance(instance *Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadForWriteLocked()
	if err != nil {
		return err
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

	return s.writeLocked(data)
}

// writeLocked is the single choke point for every write of the instance
// payload (SaveInstances, and the raw-InstanceData path behind
// DeleteInstance/UpdateInstance, which never builds live Instances). It
// refuses with ErrStorageLoadFailed when the persisted payload could not
// be decoded as a whole (loading first if nothing has, so a never-loaded
// Storage learns that before it writes), then writes data followed by
// every undecodable record verbatim. Caller must hold s.mu.
func (s *Storage) writeLocked(data []InstanceData) error {
	if !s.loaded {
		if _, err := s.loadForWriteLocked(); err != nil {
			return err
		}
	}
	if s.loadErr != nil {
		return fmt.Errorf("%w: %w", ErrStorageLoadFailed, s.loadErr)
	}
	payload := make([]json.RawMessage, 0, len(data)+len(s.undecodable))
	for _, d := range data {
		raw, err := json.Marshal(d)
		if err != nil {
			return fmt.Errorf("failed to marshal instance %s: %w", d.Title, err)
		}
		payload = append(payload, raw)
	}
	for _, rec := range s.undecodable {
		payload = append(payload, rec.raw)
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal instances: %w", err)
	}
	return s.state.SaveInstances(jsonData)
}

// loadForWriteLocked loads the payload on behalf of a write — the
// read-modify-write in DeleteInstance/UpdateInstance, or writeLocked's
// first-write load. A load failure there is the write being refused, so
// it wraps ErrStorageLoadFailed alongside the cause, matching writeLocked's
// own refusal: every write path's refusal satisfies errors.Is. Caller must
// hold s.mu.
func (s *Storage) loadForWriteLocked() ([]InstanceData, error) {
	data, err := s.loadInstanceDataLocked()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStorageLoadFailed, err)
	}
	return data, nil
}

// LoadInstanceData loads raw serialized instance data without constructing Instance objects.
// Used by reconciliation to inspect state before deciding how to restore.
// All records pass through Migrate so callers receive CurrentSchemaVersion data;
// records that fail it are omitted here and preserved on the next write.
func (s *Storage) LoadInstanceData() ([]InstanceData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadInstanceDataLocked()
}

// loadInstanceDataLocked is the unlocked core of LoadInstanceData. Callers
// that already hold s.mu (LoadInstances, LoadAndReconcile, and every write
// path via loadForWriteLocked) use this to avoid re-entering the mutex.
//
// Every load refreshes the write-safety state: a payload that is not a JSON
// array sets loadErr (latching writes shut); a successful load clears it
// and replaces the undecodable set that writes carry forward.
func (s *Storage) loadInstanceDataLocked() ([]InstanceData, error) {
	s.loaded = true
	data, skipped, err := MigrateAll(s.state.GetInstances())
	if err != nil {
		s.loadErr = err
		return nil, fmt.Errorf("failed to unmarshal instances: %w", err)
	}
	s.loadErr = nil
	s.undecodable = make([]undecodableRecord, 0, len(skipped))
	for _, raw := range skipped {
		s.undecodable = append(s.undecodable, newUndecodableRecord(raw))
	}
	return data, nil
}

// DeleteAllInstances removes all stored instances. It is the explicit wipe
// (`loom reset`), so it is allowed even while a failed load has latched
// ordinary writes shut, and it drops everything preserved on disk:
// unrecovered and undecodable records and the latch itself. The next write
// re-reads the (now empty) backing store first, as on a fresh Storage.
func (s *Storage) DeleteAllInstances() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unrecovered = nil
	s.undecodable = nil
	s.loadErr = nil
	s.loaded = false
	return s.state.DeleteAllInstances()
}

// PreservedWorktreePaths returns the set of worktree paths held by records
// that are preserved on disk but absent from the live list: the
// unrecovered cache, plus undecodable records (their worktree path decoded
// best-effort; a record whose path can't be read is skipped). Orphan
// discovery uses this so a preserved record's worktree is not also
// surfaced as an orphan candidate, which would let the user re-recover it
// under a different title and produce a duplicate state.json entry.
func (s *Storage) PreservedWorktreePaths() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.unrecovered) == 0 && len(s.undecodable) == 0 {
		return nil
	}
	out := make(map[string]bool, len(s.unrecovered)+len(s.undecodable))
	for _, d := range s.unrecovered {
		if p := d.Worktree.WorktreePath; p != "" {
			out[p] = true
		}
	}
	for _, rec := range s.undecodable {
		if rec.worktreePath != "" {
			out[rec.worktreePath] = true
		}
	}
	return out
}

// dropUnrecovered removes any entry from the unrecovered cache whose
// Title matches the given title. Used by DeleteInstance so a
// user-initiated delete is not silently undone on the next save.
// The caller must hold s.mu (DeleteInstance does).
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
