# Race Condition Fixes — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix the Critical and High severity race conditions identified in the 2026-04-15 audit across `session/`, `app/`, `daemon/`, `config/`, `session/tmux/`, and `session/git/`, so that `go test -race ./...` passes and concurrent user actions can't corrupt state, delete wrong instances, or lose instance records.

**Architecture:** Eight self-contained phases, each a coherent correctness improvement that can ship independently. Phase order reflects blast-radius — earliest phases have the highest user-visible leverage and lowest refactoring cost. `Instance` gains a `sync.RWMutex`; disk writes become atomic via temp+rename; the daemon becomes read-only; transient lifecycle statuses (`Pausing`, `Pushing`, `Starting`) generalize the existing `Deleting` pattern; `tea.Cmd` closures carry workspace identity; the `session/git` package gains a per-repo mutex registry and a branch-name uniqueness suffix.

**Tech Stack:** Go 1.23, Bubble Tea (charmbracelet), testify/assert, sync.RWMutex, sync.Mutex, os.Rename + os.File.Sync for atomic writes.

**Out of scope for this plan:** A full daemon-without-PTY refactor (DAEMON-05), removing the full-screen-attach blocking call from the main goroutine (APP-13), and every Low/Speculative finding. Those are tracked in `docs/plans/2026-04-15-race-condition-fixes-followup.md` (to be created after this plan lands).

**Phase order:**
1. Atomic file writes
2. `Instance` mutex
3. Idempotent `Start` and `Kill`
4. Daemon becomes read-only
5. Transient lifecycle statuses (`Pausing`, `Pushing`, `Starting`)
6. Workspace-scoped tea.Cmd messages
7. Per-repo git mutex + branch-name uniqueness
8. Tmux attach cleanup (TMUX-04, TMUX-05)
9. Workspace registry file locking
10. Verification: race tests and full suite

Commit after every task. Each phase concludes with a "run everything" verification task.

---

## Phase 1 — Atomic file writes

Replace every `os.WriteFile(path, data, perm)` in the persistence layer with temp-file + fsync + rename so a SIGKILL or crash mid-write cannot leave zero-byte files. Fixes STORE-01, STORE-02, STORE-03, STORE-07.

### Task 1.1: Create the `AtomicWriteFile` helper test

**Files:**
- Create: `config/atomic_test.go`

**Step 1: Write the failing test**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAtomicWriteFile_WritesContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	err := AtomicWriteFile(path, []byte(`{"ok":true}`), 0644)
	assert.NoError(t, err)

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, string(data))
}

func TestAtomicWriteFile_LeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	assert.NoError(t, AtomicWriteFile(path, []byte(`x`), 0644))

	entries, err := os.ReadDir(dir)
	assert.NoError(t, err)
	assert.Len(t, entries, 1, "only the final file should remain")
	assert.Equal(t, "state.json", entries[0].Name())
}

func TestAtomicWriteFile_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	assert.NoError(t, os.WriteFile(path, []byte("old"), 0644))
	assert.NoError(t, AtomicWriteFile(path, []byte("new"), 0644))

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, "new", string(data))
}
```

**Step 2: Run the test to confirm it fails**

Run: `go test -run TestAtomicWriteFile -v ./config/`
Expected: FAIL with "AtomicWriteFile undefined"

---

### Task 1.2: Implement `AtomicWriteFile`

**Files:**
- Create: `config/atomic.go`

**Step 1: Write the helper**

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path via a temp file in the same directory,
// fsyncs it, and renames it into place. This guarantees readers see either the
// old contents or the new contents, never a truncated or empty file.
//
// The temp file is created with O_EXCL so concurrent writers don't share it.
// If the write or rename fails, the temp file is removed and the original
// file is left untouched.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}
	return nil
}
```

**Step 2: Run the tests and confirm they pass**

Run: `go test -run TestAtomicWriteFile -v ./config/`
Expected: PASS (3 tests)

**Step 3: Commit**

```bash
git add config/atomic.go config/atomic_test.go
git commit -m "feat(config): add AtomicWriteFile helper (temp+fsync+rename)"
```

---

### Task 1.3: Use `AtomicWriteFile` in `SaveStateTo`

**Files:**
- Modify: `config/state.go` (the `SaveStateTo` function)

**Step 1: Locate the existing `os.WriteFile` call in `SaveStateTo`**

Find the line that looks like `return os.WriteFile(statePath, data, 0644)` inside `SaveStateTo`.

**Step 2: Replace it with `AtomicWriteFile`**

Change `os.WriteFile(statePath, data, 0644)` to `AtomicWriteFile(statePath, data, 0644)`. Do the same in `SaveState` if it calls `os.WriteFile` directly rather than routing through `SaveStateTo`.

**Step 3: Verify the default-state bootstrap path**

Search `config/state.go` for any other `os.WriteFile` calls (e.g. the default-state creation in `LoadState` / `LoadStateFrom`). Replace each with `AtomicWriteFile`.

**Step 4: Run tests**

Run: `go test -v ./config/`
Expected: PASS

**Step 5: Commit**

```bash
git add config/state.go
git commit -m "fix(config): write state.json atomically to survive mid-write crashes

Addresses STORE-02. Without atomic rename, a SIGKILL during SaveState
left instances.json truncated, causing LoadState to silently fall back
to DefaultState() and orphan all worktrees/branches on disk."
```

---

### Task 1.4: Use `AtomicWriteFile` in `SaveConfigTo`

**Files:**
- Modify: `config/config.go` (the `SaveConfigTo` function and any other `os.WriteFile` callers)

**Step 1: Replace both write sites**

In `SaveConfigTo` and in the default-config bootstrap path inside `LoadConfig`, change `os.WriteFile` calls to `AtomicWriteFile`.

**Step 2: Run tests**

Run: `go test -v ./config/`
Expected: PASS

**Step 3: Commit**

```bash
git add config/config.go
git commit -m "fix(config): write config.json atomically

Addresses STORE-01."
```

---

### Task 1.5: Use `AtomicWriteFile` in `SaveWorkspaceRegistry`

**Files:**
- Modify: `config/workspace.go` (the `SaveWorkspaceRegistry` function)

**Step 1: Replace the write site**

Change the `os.WriteFile` call in `SaveWorkspaceRegistry` to `AtomicWriteFile`.

**Step 2: Run tests**

Run: `go test -v ./config/`
Expected: PASS

**Step 3: Commit**

```bash
git add config/workspace.go
git commit -m "fix(config): write workspaces.json atomically

Addresses STORE-03. A crash during UpdateLastUsed on startup would
leave the registry as a zero-byte file and fail future loads with
'failed to load workspace registry' fatal errors."
```

---

### Task 1.6: Use `AtomicWriteFile` in `LaunchDaemon`

**Files:**
- Modify: `daemon/daemon.go` (the `LaunchDaemon` function, PID file write)

**Step 1: Replace the write site**

Find the `os.WriteFile(pidFile, ...)` call in `LaunchDaemon` and change it to `config.AtomicWriteFile(pidFile, ...)` (the daemon package imports `config`; if not, add the import).

**Step 2: Run tests**

Run: `go test -v ./daemon/`
Expected: PASS (or skip if there are no tests; we'll add one in Phase 4)

**Step 3: Commit**

```bash
git add daemon/daemon.go
git commit -m "fix(daemon): write pidfile atomically

Addresses STORE-07."
```

---

### Task 1.7: Verify Phase 1

**Step 1: Run the full suite**

Run: `go test ./...`
Expected: PASS

**Step 2: Run the linter**

Run: `golangci-lint run --timeout=3m --fast`
Expected: no new findings

No commit; this is a checkpoint.

---

## Phase 2 — `Instance` mutex

Add `sync.RWMutex` to `session.Instance` and discipline every field read/write through it. Closes the single largest family of races (INST-01/02/07/11/12/13/16/19/22/25, APP-01/02/07/11, TMUX-03/11, STORE-16).

### Task 2.1: Write a failing concurrency test for `Instance.Status`

**Files:**
- Create: `session/instance_race_test.go`

**Step 1: Write the test**

```go
package session

import (
	"sync"
	"testing"
)

// TestInstance_ConcurrentStatusReadWrite exercises the race the audit
// identified between tick-worker goroutines and main-loop status writers.
// Must pass under `go test -race`.
func TestInstance_ConcurrentStatusReadWrite(t *testing.T) {
	inst := &Instance{Title: "race", Status: Ready}

	var wg sync.WaitGroup
	const n = 1000

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			inst.SetStatus(Running)
			inst.SetStatus(Paused)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = inst.GetStatus()
		}
	}()
	wg.Wait()
}
```

If `GetStatus()` doesn't exist yet, that's expected — we'll add it in Task 2.3.

**Step 2: Run the test to confirm it fails**

Run: `go test -race -run TestInstance_ConcurrentStatusReadWrite -v ./session/`
Expected: FAIL — either "GetStatus undefined" or (after adding the getter without a lock) a `DATA RACE` report on `inst.Status`.

---

### Task 2.2: Add `sync.RWMutex` to `Instance`

**Files:**
- Modify: `session/instance.go` (the `Instance` struct)

**Step 1: Add the mutex field**

Add `mu sync.RWMutex` as the last field of the `Instance` struct. Place it below the last existing private field (likely `started` or `diffStats`). Keep it unexported and off the JSON surface (struct tag `json:"-"` if needed, though unexported fields are not serialized by default).

**Step 2: Add the `sync` import if not present**

Make sure `import "sync"` is in the imports block.

**Step 3: Run the build**

Run: `go build ./...`
Expected: PASS

No commit yet — the mutex is unused. Commit with Task 2.3.

---

### Task 2.3: Add `GetStatus` and make `SetStatus` lock

**Files:**
- Modify: `session/instance.go` (the `SetStatus` method and new `GetStatus`)

**Step 1: Modify `SetStatus`**

Wrap the body in `i.mu.Lock()` / `defer i.mu.Unlock()`:

```go
func (i *Instance) SetStatus(s Status) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Status = s
}
```

**Step 2: Add `GetStatus`**

```go
// GetStatus returns the current status under a read lock.
func (i *Instance) GetStatus() Status {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.Status
}
```

**Step 3: Run the race test**

Run: `go test -race -run TestInstance_ConcurrentStatusReadWrite -v ./session/`
Expected: PASS

**Step 4: Run the full package tests**

Run: `go test -race -v ./session/`
Expected: PASS (or only pre-existing failures)

**Step 5: Commit**

```bash
git add session/instance.go session/instance_race_test.go
git commit -m "feat(session): add RWMutex to Instance, guard Status access

Introduces i.mu and uses it in SetStatus/GetStatus. Callers reading
i.Status directly are still racy and will be migrated in subsequent
commits."
```

---

### Task 2.4: Migrate all direct `inst.Status` reads to `GetStatus`

**Files (read callers to migrate):**
- `app/app.go` — grep for `\.Status ==` and `\.Status !=` and `\.Status\b` (every match that isn't a write)
- `session/instance.go` — the `Paused()` and `Started()` methods and any other internal readers
- `session/storage.go` — `ToInstanceData` reads `i.Status`
- `ui/list.go`, `ui/preview.go`, `ui/split_pane.go` — render paths
- `daemon/daemon.go`

**Step 1: Identify every read site**

Run: `grep -rn '\.Status\b' --include='*.go' . | grep -v '_test.go' | grep -v '= ' | grep -v 'Status =' | grep -v 'inst.Status{'`

Cross-reference with the audit's "Unguarded concurrent writers to `Status`" table.

**Step 2: Replace each `X.Status` read with `X.GetStatus()`**

Work package-by-package. Do not change writes (`X.Status = foo`) — those remain, they're already inside a mutator method we'll lock shortly, or they're going to be migrated to `SetStatus`.

After each package is done, run `go build ./...` to confirm no type/method errors.

**Step 3: Replace any direct `X.Status = Y` assignments with `X.SetStatus(Y)`**

Same grep but with `=` on the RHS. There should be very few outside of `SetStatus` itself.

**Step 4: Run tests with race detector**

Run: `go test -race ./...`
Expected: PASS (some races on other fields may still appear; those are addressed in Tasks 2.5–2.7)

**Step 5: Commit**

```bash
git add -A
git commit -m "refactor: route all Instance.Status access through Get/SetStatus

Closes the read side of the data race between metadata-tick goroutines
and main-loop status transitions (INST-01, APP-02)."
```

---

### Task 2.5: Guard `diffStats` access with the mutex

**Files:**
- Modify: `session/instance.go` (`UpdateDiffStats`, `UpdateDiffStatsShort`, `GetDiffStats`)

**Step 1: Write a failing race test**

Append to `session/instance_race_test.go`:

```go
// TestInstance_ConcurrentDiffStats reproduces INST-22: worker goroutines
// writing i.diffStats while the render path reads it.
func TestInstance_ConcurrentDiffStats(t *testing.T) {
	inst := &Instance{Title: "race"}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			inst.setDiffStatsForTest(&git.DiffStats{Added: i, Removed: i})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = inst.GetDiffStats()
		}
	}()
	wg.Wait()
}
```

Add the test helper in `instance.go` (kept unexported but package-visible):

```go
// setDiffStatsForTest is a package-private test helper that assigns diffStats
// under the instance mutex. Not exported.
func (i *Instance) setDiffStatsForTest(s *git.DiffStats) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.diffStats = s
}
```

Run the test with `-race`; expect FAIL until the next step.

**Step 2: Add locking to the diffStats methods**

In `UpdateDiffStats` / `UpdateDiffStatsShort`, bracket every `i.diffStats = ...` and every `i.Branch = ...` assignment with `i.mu.Lock()` / `i.mu.Unlock()`. Keep the I/O (git calls) outside the lock — acquire only around the assignments.

In `GetDiffStats`:

```go
func (i *Instance) GetDiffStats() *git.DiffStats {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.diffStats
}
```

**Step 3: Run the race test**

Run: `go test -race -run TestInstance_ConcurrentDiffStats -v ./session/`
Expected: PASS

**Step 4: Commit**

```bash
git add session/instance.go session/instance_race_test.go
git commit -m "fix(session): guard Instance.diffStats and Branch with mutex

Addresses INST-07, INST-22."
```

---

### Task 2.6: Guard `tmuxSession`, `gitWorktree`, and `started` with the mutex

**Files:**
- Modify: `session/instance.go`

**Step 1: Add lock helpers for the three fields**

Add unexported accessors used by the methods that touch them:

```go
func (i *Instance) getTmuxSession() *tmux.TmuxSession {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.tmuxSession
}

func (i *Instance) setTmuxSession(s *tmux.TmuxSession) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.tmuxSession = s
}

func (i *Instance) getGitWorktree() *git.GitWorktree {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.gitWorktree
}

func (i *Instance) setGitWorktree(w *git.GitWorktree) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.gitWorktree = w
}

func (i *Instance) isStarted() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.started
}

func (i *Instance) setStarted(v bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.started = v
}
```

**Step 2: Migrate every use inside `instance.go`**

In `Start`, `Kill`, `Pause`, `Resume`, `SendPrompt`, `CaptureAndProcessStatus`, `TmuxAlive`, `UpdateDiffStats*`, `Preview`, `GetContentHash`, replace direct field reads/writes with the accessors above. Importantly, inside these methods, prefer to **snapshot** the pointer into a local once (via `getTmuxSession()`), then operate on the local — do not call the getter repeatedly in a loop.

**Step 3: Migrate external readers**

Two external callers read these fields directly today:
- `app/app.go` — `selected.GetGitWorktree()` already exists; audit every call site that uses `.tmuxSession` or `.gitWorktree` directly on an instance. None should.
- `session/storage.go`'s `ToInstanceData` — calls `i.gitWorktree.GetBaseCommitSHA()`. Change to `i.getGitWorktree().GetBaseCommitSHA()`.

Grep to confirm no external direct accesses remain: `grep -rn '\.tmuxSession\b\|\.gitWorktree\b\|\.started\b' --include='*.go' . | grep -v _test.go | grep -v 'session/instance.go'`

**Step 4: Run tests with race detector**

Run: `go test -race ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add session/instance.go session/storage.go
git commit -m "fix(session): guard tmuxSession/gitWorktree/started with mutex

Addresses INST-12, INST-13, INST-16. ToInstanceData now reads git
worktree state under lock, closing the race between killAction and
main-loop SaveInstances (STORE-16)."
```

---

### Task 2.7: Snapshot `Instance` inside `ToInstanceData` under lock

**Files:**
- Modify: `session/instance.go` — add a `Snapshot` method that returns an `InstanceData` under `RLock`

**Step 1: Add a `Snapshot` method to `Instance`**

```go
// Snapshot returns a serialization-safe copy of the instance fields under
// an RLock. This is the only safe way to read every field at once from
// outside the main goroutine.
func (i *Instance) Snapshot() InstanceData {
	i.mu.RLock()
	defer i.mu.RUnlock()

	data := InstanceData{
		Title:      i.Title,
		Path:       i.Path,
		Branch:     i.Branch,
		Status:     i.Status,
		Height:     i.Height,
		Width:      i.Width,
		CreatedAt:  i.CreatedAt,
		UpdatedAt:  time.Now(),
		Program:    i.Program,
		AutoYes:    i.AutoYes,
		IsWorkspaceTerminal: i.IsWorkspaceTerminal,
		// ...copy every field ToInstanceData copies today
	}
	if i.diffStats != nil {
		data.DiffStats = *i.diffStats // copy by value
	}
	if i.gitWorktree != nil {
		data.BaseCommitSHA = i.gitWorktree.GetBaseCommitSHA()
		data.WorktreePath = i.gitWorktree.GetWorktreePath()
	}
	return data
}
```

(Verify every field `ToInstanceData` currently copies; match it exactly.)

**Step 2: Replace `ToInstanceData`'s body with a call to `Snapshot`**

```go
func (i *Instance) ToInstanceData() InstanceData {
	return i.Snapshot()
}
```

**Step 3: Run tests with race detector**

Run: `go test -race ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add session/instance.go
git commit -m "fix(session): serialize Instance fields under RLock in Snapshot

Addresses INST-11, STORE-16. storage.DeleteInstance runs in a Cmd
goroutine, and its call to instance.ToInstanceData() previously read
every field unlocked while the main loop mutated them."
```

---

### Task 2.8: Verify Phase 2

**Step 1: Run the full suite with `-race`**

Run: `go test -race ./...`
Expected: PASS

**Step 2: Run the linter**

Run: `golangci-lint run --timeout=3m --fast`
Expected: no new findings

No commit — checkpoint only.

---

## Phase 3 — Idempotent `Start` and `Kill`

Prevent double-Start (tmux leak) and double-Kill (use-after-free / nil-deref) by adding a single guard at the top of each and resetting lifecycle fields on Kill. Fixes INST-04, INST-05.

### Task 3.1: Write a failing test for double-Kill

**Files:**
- Create: `session/instance_lifecycle_test.go`

**Step 1: Write the test**

```go
package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInstance_KillIsIdempotent ensures Kill can be called safely twice
// (INST-05). The second call should be a no-op.
func TestInstance_KillIsIdempotent(t *testing.T) {
	inst := newTestStartedInstance(t)

	assert.NoError(t, inst.Kill())
	// Second Kill should not panic or return an error.
	assert.NoError(t, inst.Kill())

	assert.False(t, inst.isStarted(), "Kill should clear the started flag")
	assert.Nil(t, inst.getTmuxSession(), "Kill should nil out the tmux session")
}
```

`newTestStartedInstance` is a helper. If no similar helper exists in the package, stub it at the top of the file using the existing mock patterns (see `session/tmux/tmux_test.go` for the mock exec/PTY pattern).

**Step 2: Run the test to confirm it fails**

Run: `go test -run TestInstance_KillIsIdempotent -v ./session/`
Expected: FAIL — either panics on the second Kill or leaves `started=true` / non-nil tmuxSession.

---

### Task 3.2: Make `Kill` idempotent and reset fields

**Files:**
- Modify: `session/instance.go` — the `Kill` method

**Step 1: Add a guard at the top**

```go
func (i *Instance) Kill() error {
	i.mu.Lock()
	if !i.started {
		i.mu.Unlock()
		return nil
	}
	// Snapshot handles under lock, then release so we don't hold the lock
	// across git and tmux I/O.
	tmuxSess := i.tmuxSession
	gitWT := i.gitWorktree
	i.mu.Unlock()

	var errs []error
	if tmuxSess != nil {
		if err := tmuxSess.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if gitWT != nil {
		if err := gitWT.Cleanup(); err != nil {
			errs = append(errs, err)
		}
	}

	// Reset state regardless of cleanup errors — we don't want a second
	// Kill to retry and double-close.
	i.mu.Lock()
	i.started = false
	i.tmuxSession = nil
	i.gitWorktree = nil
	i.mu.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("kill: %v", errs)
	}
	return nil
}
```

**Step 2: Run the test**

Run: `go test -race -run TestInstance_KillIsIdempotent -v ./session/`
Expected: PASS

**Step 3: Commit**

```bash
git add session/instance.go session/instance_lifecycle_test.go
git commit -m "fix(session): make Kill idempotent, nil handles after cleanup

Addresses INST-05. Second calls to Kill (e.g. setup-error defer +
user kill, or a repeat of the killAction Cmd) now no-op instead of
double-closing tmux or re-running branch -D."
```

---

### Task 3.3: Write a failing test for double-Start

**Files:**
- Modify: `session/instance_lifecycle_test.go`

**Step 1: Write the test**

```go
// TestInstance_StartIsIdempotent verifies Start is a no-op on an
// already-started instance (INST-04).
func TestInstance_StartIsIdempotent(t *testing.T) {
	inst := newTestStartedInstance(t) // already started once

	// Capture current tmuxSession pointer.
	firstSession := inst.getTmuxSession()
	assert.NotNil(t, firstSession)

	// Second Start should not create a new tmux session.
	assert.NoError(t, inst.Start(true))
	assert.Same(t, firstSession, inst.getTmuxSession(),
		"Start should not replace the tmux session if already started")
}
```

**Step 2: Run to confirm failure**

Run: `go test -run TestInstance_StartIsIdempotent -v ./session/`
Expected: FAIL — second Start creates a fresh tmux session and leaks the first.

---

### Task 3.4: Make `Start` idempotent

**Files:**
- Modify: `session/instance.go` — the `Start` method

**Step 1: Add the guard at the top**

```go
func (i *Instance) Start(firstTimeSetup bool) error {
	i.mu.Lock()
	if i.started {
		i.mu.Unlock()
		return nil
	}
	i.mu.Unlock()

	// ... existing body ...
}
```

**Step 2: Ensure the setup-error defer nils fields**

In the existing defer inside `Start`:

```go
defer func() {
	if setupErr != nil {
		_ = i.Kill() // Kill now resets state per Task 3.2
	}
}()
```

Since Kill now properly resets `tmuxSession`, `gitWorktree`, and `started`, a retry on the same `*Instance` after a failed Start will now set up cleanly.

**Step 3: Run tests with race detector**

Run: `go test -race -v ./session/`
Expected: PASS

**Step 4: Commit**

```bash
git add session/instance.go session/instance_lifecycle_test.go
git commit -m "fix(session): make Start idempotent

Addresses INST-04. A second Start on the same Instance no longer
creates a duplicate tmux session that orphans the first."
```

---

### Task 3.5: Verify Phase 3

**Step 1: Run full suite with `-race`**

Run: `go test -race ./...`
Expected: PASS

No commit — checkpoint.

---

## Phase 4 — Daemon becomes read-only

Remove the daemon's writeback to `state.json` and its forced `AutoYes = true` assignment. Add periodic re-read of `instances.json` so new/deleted instances are observed. Fixes DAEMON-03, DAEMON-04, DAEMON-13.

### Task 4.1: Remove `storage.SaveInstances` on daemon shutdown

**Files:**
- Modify: `daemon/daemon.go` — the SIGTERM/SIGINT handler block

**Step 1: Delete the writeback**

Find the shutdown handler that calls `storage.SaveInstances(instances)` (around line 85 of the current file). Delete the line. Leave the close-stop-channel + wg.Wait() intact.

**Step 2: Add a comment documenting why**

```go
// NOTE: we do NOT call storage.SaveInstances here. The daemon is
// strictly a read-only client of state.json; the main app is the
// sole writer. Writing from here would clobber any concurrent
// writes by the main app (DAEMON-04).
```

**Step 3: Run the daemon build**

Run: `go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add daemon/daemon.go
git commit -m "fix(daemon): do not write state.json on shutdown

Addresses DAEMON-04. The daemon read state.json once at startup and
its stale snapshot silently deleted instances the main app created
during the daemon's lifetime."
```

---

### Task 4.2: Remove the `AutoYes = true` coercion

**Files:**
- Modify: `daemon/daemon.go`

**Step 1: Delete the line**

Find the loop that iterates loaded instances and assigns `instance.AutoYes = true`. Delete the assignment. The daemon only runs when global autoyes is configured, so per-instance `AutoYes` values should be respected as written by the main app.

**Step 2: Update the poll-loop condition**

In the poll loop, keep the existing `if !instance.AutoYes { continue }` guard (or add it if absent). This ensures the daemon skips instances the user has explicitly opted out.

**Step 3: Run build**

Run: `go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add daemon/daemon.go
git commit -m "fix(daemon): respect per-instance AutoYes instead of forcing true

Addresses DAEMON-13. Previously the daemon flipped AutoYes to true
for every loaded instance, overriding user intent."
```

---

### Task 4.3: Write a test for periodic instance reload

**Files:**
- Create: `daemon/daemon_reload_test.go`

**Step 1: Write the test**

```go
package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestReloadInstances_SeesFreshInstancesOnDisk verifies that
// reloadInstances picks up new entries written to state.json after
// startup (addresses DAEMON-03).
func TestReloadInstances_SeesFreshInstancesOnDisk(t *testing.T) {
	dir := t.TempDir()
	writeInitialStateJSON(t, dir, []string{"alpha"})

	insts, err := reloadInstances(dir)
	assert.NoError(t, err)
	assert.Len(t, insts, 1)
	assert.Equal(t, "alpha", insts[0].Title)

	writeInitialStateJSON(t, dir, []string{"alpha", "beta"})

	insts, err = reloadInstances(dir)
	assert.NoError(t, err)
	assert.Len(t, insts, 2)
}

// writeInitialStateJSON is a test helper that writes a minimal
// state.json matching the schema LoadInstances expects.
func writeInitialStateJSON(t *testing.T, dir string, titles []string) {
	t.Helper()
	// ... build a minimal state.json using config.State ...
	// Use config.AtomicWriteFile to write it.
}

// reloadInstances is the function-under-test we'll extract in Task 4.4.
```

The helper body is left as a stub — implementers will fill in the state.json shape by inspecting the existing `LoadInstances` / `config.State` types.

**Step 2: Run to confirm failure**

Run: `go test -run TestReloadInstances -v ./daemon/`
Expected: FAIL with "reloadInstances undefined".

---

### Task 4.4: Extract `reloadInstances` and call it on every tick

**Files:**
- Modify: `daemon/daemon.go`

**Step 1: Add `reloadInstances`**

```go
// reloadInstances reads state.json and returns the fresh instance set.
// Called every tick so the daemon observes instances added or removed
// by the main app (DAEMON-03).
func reloadInstances(configDir string) ([]*session.Instance, error) {
	storage, err := session.NewStorageFrom(configDir)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	return storage.LoadInstances()
}
```

**Step 2: Call it inside the poll loop**

Change the loop body so the first thing each iteration does is:

```go
for {
	instances, err := reloadInstances(configDir)
	if err != nil {
		log.ErrorLog.Printf("daemon reload failed: %v", err)
		// keep the previous list rather than bail — preserves
		// resilience against transient I/O errors
	}

	for _, instance := range instances {
		// existing poll body ...
	}

	select {
	case <-stopCh:
		return
	case <-ticker.C:
	}
}
```

Note: `FromInstanceData` calls `Start(false)` which is expensive and attaches a tmux PTY (a separate issue — DAEMON-05, out of scope here). For now, accept that this preserves existing behavior but with fresh instances per tick. The PTY-less refactor is the Phase 4 followup.

**Step 3: Run the test**

Run: `go test -run TestReloadInstances -v ./daemon/`
Expected: PASS

**Step 4: Run the full suite**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add daemon/daemon.go daemon/daemon_reload_test.go
git commit -m "feat(daemon): reload instances.json on every poll tick

Addresses DAEMON-03. The daemon now observes instances created or
deleted by the main app between ticks rather than operating on a
stale startup snapshot."
```

---

### Task 4.5: Verify Phase 4

**Step 1: Run full suite with race detector**

Run: `go test -race ./...`
Expected: PASS

---

## Phase 5 — Transient lifecycle statuses

Generalize the existing `Deleting` pattern to `Pausing`, `Pushing`, and `Starting`. The metadata tick filter widens to exclude all transient statuses, closing the race between tick fan-out goroutines and in-flight Cmds for pause/push/start. Fixes APP-07 (Pause race), parts of APP-08 (Push race), APP-09 (Start race).

### Task 5.1: Add the new status constants

**Files:**
- Modify: `session/instance.go` — the `Status` const block

**Step 1: Extend the enum**

```go
const (
	Running Status = iota
	Ready
	Loading
	Paused
	Prompting
	Deleting
	Pausing  // transient: Pause Cmd in flight
	Pushing  // transient: Push Cmd in flight
	Starting // transient: Start Cmd in flight (replaces Loading for main-loop guard)
)
```

Note: `Loading` already exists and is used for the new-instance flow. Keep `Loading` for the "UI has scheduled start but cmd hasn't begun" window; `Starting` represents "Cmd has begun". If this distinction is not needed, skip `Starting` and reuse `Loading`.

Decision for this plan: **reuse `Loading` for Start**, add only `Pausing` and `Pushing`. Revise the enum:

```go
const (
	Running Status = iota
	Ready
	Loading
	Paused
	Prompting
	Deleting
	Pausing
	Pushing
)
```

**Step 2: Build**

Run: `go build ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add session/instance.go
git commit -m "feat(session): add Pausing and Pushing transient statuses"
```

---

### Task 5.2: Render the new statuses in the list UI

**Files:**
- Modify: `ui/list.go` — icons, styles, and Render switch

**Step 1: Add icons + styles**

Mirror the `Deleting` pattern. A spinner-like glyph works; reuse the existing `deletingStyle` if visual consistency matters.

```go
const (
	pausingIcon = "⏸ "
	pushingIcon = "↑ "
)

var (
	pausingStyle = deletingStyle
	pushingStyle = deletingStyle
)
```

**Step 2: Add cases to the Render switch**

```go
case session.Pausing:
	join = pausingStyle.Render(pausingIcon)
case session.Pushing:
	join = pushingStyle.Render(pushingIcon)
```

**Step 3: Build**

Run: `go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add ui/list.go
git commit -m "feat(ui): render Pausing/Pushing transient statuses"
```

---

### Task 5.3: Widen the metadata tick filter

**Files:**
- Modify: `app/app.go` — the tick filter around line 407

**Step 1: Update the filter condition**

Find the filter inside the `tickUpdateMetadataMessage` handler (currently `inst.Status != session.Deleting`). Change to:

```go
switch inst.GetStatus() {
case session.Deleting, session.Pausing, session.Pushing, session.Loading:
	continue
}
```

**Step 2: Also update the apply-loop at around line 449-459 to not overwrite transient statuses**

Before `r.instance.SetStatus(session.Paused)` etc., check:

```go
cur := r.instance.GetStatus()
if cur == session.Deleting || cur == session.Pausing ||
	cur == session.Pushing || cur == session.Loading {
	continue // user-initiated transition in flight; don't overwrite
}
```

**Step 3: Build and test**

Run: `go test ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add app/app.go
git commit -m "fix(app): metadata tick skips instances in transient statuses

Closes the race where the apply-loop overwrote Deleting with Paused
(INST-05 scenario 2) and similar for Pausing/Pushing."
```

---

### Task 5.4: Set `Pausing` before dispatching `pauseAction`

**Files:**
- Modify: `app/app.go` — the pause confirmation flow around line 1142

**Step 1: Mirror the `Deleting` pattern**

In the confirmation callback that currently dispatches `pauseAction` directly, add a `pendingPreAction` that sets status to `Pausing`, captures the previous status, and returns a `pauseFailedMsg` on error:

```go
m.pendingPreAction = func() {
	previousStatus := selected.GetStatus()
	selected.SetStatus(session.Pausing)
	m.pauseRevert = func() { selected.SetStatus(previousStatus) }
}
// ... inside pauseAction, on error return pauseFailedMsg{title, err} ...
```

Add a `pauseFailedMsg` Update handler that calls `m.pauseRevert()` and nils it.

**Step 2: Test manually**

Run `go build ./...` and ensure the pause flow still works end-to-end — this is a UI change so `go test` coverage is limited.

**Step 3: Commit**

```bash
git add app/app.go
git commit -m "fix(app): set Pausing status immediately on pause confirm

Addresses APP-07. Mirrors the Deleting pattern from commit 354ff76."
```

---

### Task 5.5: Set `Pushing` before dispatching `pushAction`

**Files:**
- Modify: `app/app.go` — the push confirmation flow around line 1119

**Step 1: Apply the same pattern**

Same as Task 5.4 but for the push Cmd. Add `pushFailedMsg`, `pushRevert`, and set `Pushing` status in the pre-action.

**Step 2: Guard Kill against Pushing**

At `app/app.go` around line 1063 (the existing kill-guard against `Loading`), extend:

```go
if selected.GetStatus() == session.Pushing {
	m.setError("cannot kill while push is in progress")
	return nil
}
```

Reason: Kill during Push can `branch -D` a branch that git is mid-push on (APP-08).

**Step 3: Commit**

```bash
git add app/app.go
git commit -m "fix(app): set Pushing status during push, block kill during push

Addresses APP-08."
```

---

### Task 5.6: Verify Phase 5

Run: `go test -race ./...`
Expected: PASS

---

## Phase 6 — Workspace-scoped tea.Cmd messages

`killInstanceMsg`, `killFailedMsg`, `pauseFailedMsg`, `pushFailedMsg`, and `instanceStartedMsg` all currently mutate `m.list` without awareness of workspace switching. If the user switches workspace while a Cmd is in flight, the handler mutates the wrong list. Fixes APP-05, APP-06, APP-10.

### Task 6.1: Add a workspace identifier to every lifecycle message

**Files:**
- Modify: `app/app.go` — the message structs for kill/push/pause/start completion

**Step 1: Extend each struct**

```go
type killInstanceMsg struct {
	title         string
	workspaceName string
}

type killFailedMsg struct {
	title         string
	err           error
	workspaceName string
}

type pauseFailedMsg struct {
	title         string
	err           error
	workspaceName string
}

type pushFailedMsg struct {
	title         string
	err           error
	workspaceName string
}

type instanceStartedMsg struct {
	instance      *session.Instance
	err           error
	workspaceName string
}
```

**Step 2: Populate at dispatch time**

Wherever these messages are constructed (inside each Cmd closure), capture `m.currentWorkspace.Name` (or the equivalent) *into a local* before the closure, then set `workspaceName: ws` in the message.

For example:

```go
ws := m.currentWorkspaceName()
return func() tea.Msg {
	// ...
	return killInstanceMsg{title: title, workspaceName: ws}
}
```

**Step 3: Build**

Run: `go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add app/app.go
git commit -m "refactor(app): carry workspace name on lifecycle completion messages"
```

---

### Task 6.2: Add a `findSlotByWorkspace` helper

**Files:**
- Modify: `app/app.go` — add a helper near the slot-manipulation code

**Step 1: Write the helper**

```go
// findSlotByWorkspace returns the slot for a workspace name, or nil if
// the workspace is no longer active. Safe to call from message handlers
// that need to mutate the correct list regardless of current focus.
func (m *home) findSlotByWorkspace(name string) *workspaceSlot {
	for i := range m.slots {
		if m.slots[i] != nil && m.slots[i].workspaceName == name {
			return m.slots[i]
		}
	}
	return nil
}
```

Adjust the exact slot representation to match the code (`workspaceSlot` may be named differently; check the file).

**Step 2: Build**

Run: `go build ./...`
Expected: PASS

No commit yet.

---

### Task 6.3: Update lifecycle handlers to use `findSlotByWorkspace`

**Files:**
- Modify: `app/app.go` — `killInstanceMsg`, `killFailedMsg`, `instanceStartedMsg`, `pauseFailedMsg`, `pushFailedMsg` handlers

**Step 1: Each handler looks up the slot by name**

Example for `killInstanceMsg`:

```go
case killInstanceMsg:
	slot := m.findSlotByWorkspace(msg.workspaceName)
	if slot == nil {
		// workspace no longer active; nothing to update
		return m, nil
	}
	slot.list.RemoveInstanceByTitle(msg.title)
	slot.splitPane.CleanupTerminalForInstance(msg.title)
	return m, m.instanceChanged()
```

For `killFailedMsg`, iterate the slot's list to find the instance by title and revert its status. For `instanceStartedMsg`, use the captured workspace to find the right list before calling `SelectInstance`/`Kill`.

**Step 2: Run tests**

Run: `go test -race ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add app/app.go
git commit -m "fix(app): route lifecycle completions to the originating workspace

Addresses APP-05, APP-06, APP-10. Cmds that complete after the user
has switched workspace now find the correct slot instead of mutating
the currently focused list."
```

---

### Task 6.4: Verify Phase 6

Manual: switch workspaces during a slow kill/push/pause and confirm the result lands in the originating workspace's list. Run `go test -race ./...` — PASS.

---

## Phase 7 — Per-repo git mutex + branch-name uniqueness

`session/git` has no concurrency control, so two concurrent `worktree add` invocations race on `.git/index.lock`, and sanitized branch collisions can silently delete user branches. Fixes GIT-01, GIT-02, GIT-03, GIT-04.

### Task 7.1: Add a per-repo mutex registry

**Files:**
- Create: `session/git/repo_lock.go`
- Create: `session/git/repo_lock_test.go`

**Step 1: Write the test**

```go
package git

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepoLock_SerializesPerRepo(t *testing.T) {
	var shared int
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := lockRepo("/tmp/repo")
			defer unlock()
			// critical section
			x := shared
			x++
			shared = x
		}()
	}
	wg.Wait()
	assert.Equal(t, 100, shared, "repo lock must serialize increments")
}

func TestRepoLock_DifferentReposDoNotBlock(t *testing.T) {
	unlockA := lockRepo("/tmp/a")
	defer unlockA()

	done := make(chan struct{})
	go func() {
		unlockB := lockRepo("/tmp/b")
		defer unlockB()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("different repos should not block each other")
	}
}
```

Run: `go test -race -run TestRepoLock -v ./session/git/`
Expected: FAIL with "lockRepo undefined".

**Step 2: Implement `lockRepo`**

```go
// repo_lock.go
package git

import "sync"

var (
	repoLocksMu sync.Mutex
	repoLocks   = map[string]*sync.Mutex{}
)

// lockRepo acquires an exclusive lock for the given repository path and
// returns an unlock function. Different repos are independent; the same
// repo serializes callers.
func lockRepo(repoPath string) func() {
	repoLocksMu.Lock()
	mu, ok := repoLocks[repoPath]
	if !ok {
		mu = &sync.Mutex{}
		repoLocks[repoPath] = mu
	}
	repoLocksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}
```

**Step 3: Run test**

Run: `go test -race -v ./session/git/`
Expected: PASS

**Step 4: Commit**

```bash
git add session/git/repo_lock.go session/git/repo_lock_test.go
git commit -m "feat(git): per-repo mutex registry for serializing mutations"
```

---

### Task 7.2: Hold the repo lock across every mutating operation

**Files:**
- Modify: `session/git/worktree_ops.go` (`Setup`, `Cleanup`, `Remove`, `Prune`, `CleanupWorktrees`)
- Modify: `session/git/worktree_git.go` (`CommitChanges`, `PushChanges`)
- Modify: `session/git/worktree_branch.go` (`FetchBranches`)

**Step 1: Wrap each function's body**

```go
func (g *GitWorktree) Setup() error {
	defer lockRepo(g.repoPath)()
	// ... existing body ...
}
```

Apply to every mutating git call that targets `g.repoPath`. Read-only commands (`Diff`, `IsDirty`, `CurrentBranch`) do not need the lock at this layer — they get a per-worktree lock in Task 7.3.

**Step 2: Run tests**

Run: `go test -race ./session/git/`
Expected: PASS

**Step 3: Commit**

```bash
git add session/git/
git commit -m "fix(git): serialize repo-level mutations via lockRepo

Addresses GIT-01 (index.lock collisions), GIT-08 (prune vs add),
GIT-09 (CleanupWorktrees race), GIT-12 (fetch vs worktree add)."
```

---

### Task 7.3: Add per-worktree mutex for diff/status operations

**Files:**
- Modify: `session/git/worktree.go` — add `mu sync.Mutex` to `GitWorktree`
- Modify: `session/git/diff.go` (`Diff`, `DiffShortStat`, `DiffUncommitted*`)
- Modify: `session/git/worktree_git.go` (`IsDirty`, `CommitChanges`, `PushChanges`)

**Step 1: Add the field**

```go
type GitWorktree struct {
	// ... existing fields ...
	mu sync.Mutex
}
```

**Step 2: Wrap Diff and IsDirty bodies**

```go
func (g *GitWorktree) Diff() (*DiffStats, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// ... existing body ...
}
```

Do the same for `DiffShortStat`, `IsDirty`, `CommitChanges`, `PushChanges`. Since `CommitChanges` and `PushChanges` also mutate at the repo level, they hold both locks: `lockRepo(g.repoPath)` (already added in Task 7.2) and `g.mu`. Acquire `g.mu` first to keep ordering consistent across the package and avoid deadlock.

**Step 3: Run tests**

Run: `go test -race ./session/git/`
Expected: PASS

**Step 4: Commit**

```bash
git add session/git/
git commit -m "fix(git): per-worktree mutex for diff and status I/O

Addresses GIT-05, GIT-06, GIT-07. Prevents concurrent add -N .
calls from different tick goroutines from colliding on the
per-worktree index."
```

---

### Task 7.4: Add uniqueness suffix to branch names

**Files:**
- Modify: `session/git/worktree.go` — the branch-name computation (currently `{prefix}{sanitized}`)
- Modify: `session/git/util.go` — `sanitizeBranchName`

**Step 1: Reject empty sanitized names**

In `NewGitWorktree` (or wherever branch name is built), after sanitizing:

```go
sanitized := sanitizeBranchName(title)
if sanitized == "" {
	return nil, fmt.Errorf("title %q sanitizes to empty branch name", title)
}
```

**Step 2: Add a short nano suffix**

Mirror the worktree-path suffix:

```go
branch := fmt.Sprintf("%s%s-%d",
	cfg.BranchPrefix, sanitized, time.Now().UnixNano()%1_000_000)
```

Six-digit nano suffix gives enough collision resistance without making branches unreadable.

**Step 3: Write a test**

```go
func TestNewGitWorktree_EmptyTitleRejected(t *testing.T) {
	_, err := NewGitWorktree("/tmp/repo", "日本語", cfg)
	assert.Error(t, err, "CJK-only titles must be rejected before branch collision")
}

func TestNewGitWorktree_DistinctTitlesGetDistinctBranches(t *testing.T) {
	// Two instances with the same sanitized base must get different branches.
	a, err := NewGitWorktree("/tmp/repo", "Fix bug!", cfg)
	assert.NoError(t, err)
	// Small sleep to ensure distinct nano suffixes if system clock is coarse.
	time.Sleep(time.Microsecond)
	b, err := NewGitWorktree("/tmp/repo", "Fix bug?", cfg)
	assert.NoError(t, err)
	assert.NotEqual(t, a.branchName, b.branchName)
}
```

**Step 4: Run tests**

Run: `go test -v ./session/git/`
Expected: PASS

**Step 5: Commit**

```bash
git add session/git/
git commit -m "fix(git): branch uniqueness suffix + reject empty sanitized names

Addresses GIT-03, GIT-17. Distinct titles can no longer collide on
a shared branch, and unsanitizable titles (CJK only) fail fast
instead of silently producing empty-suffix branches."
```

---

### Task 7.5: Guard `Cleanup`'s `branch -D` against non-owned branches

**Files:**
- Modify: `session/git/worktree.go` — add `createdByUs` flag
- Modify: `session/git/worktree_ops.go` — `setupNewWorktree` and `Cleanup`

**Step 1: Add the flag**

```go
type GitWorktree struct {
	// ...
	createdByUs bool
}
```

**Step 2: Set it only after a successful `worktree add`**

At the end of `setupNewWorktree`, after `worktree add -b` succeeds:

```go
g.createdByUs = true
```

**Step 3: Guard Cleanup's `branch -D`**

```go
if g.createdByUs {
	if err := runGitCommand(g.repoPath, "branch", "-D", g.branchName); err != nil {
		// log but don't fail — branch may already be gone
	}
}
```

**Step 4: Build and test**

Run: `go test -v ./session/git/`
Expected: PASS

**Step 5: Commit**

```bash
git add session/git/
git commit -m "fix(git): only delete branches this instance actually created

Addresses GIT-04. A failed Setup no longer deletes pre-existing user
branches that we never successfully attached to."
```

---

### Task 7.6: Verify Phase 7

Run: `go test -race ./...`
Expected: PASS

---

## Phase 8 — Tmux attach cleanup

Fix the two highest-severity tmux issues: the spurious "abnormal exit" error printed on every Ctrl+Q detach (TMUX-04), and the stdin-reader goroutine leak across Attach/Detach cycles (TMUX-05).

### Task 8.1: Add a `detaching` flag to distinguish normal detach from PTY death

**Files:**
- Modify: `session/tmux/tmux.go` — `TmuxSession` struct and Detach/abnormal monitor

**Step 1: Add the flag**

```go
type TmuxSession struct {
	// ... existing fields ...
	detaching atomic.Bool
}
```

Import `sync/atomic` if not present.

**Step 2: Set it at the top of Detach/DetachSafely**

```go
func (t *TmuxSession) Detach() {
	t.detaching.Store(true)
	defer t.detaching.Store(false)
	// ... existing body ...
}
```

**Step 3: Check it in the abnormal monitor**

In the monitor goroutine around line 394, change the select to:

```go
select {
case <-t.ctx.Done():
	// normal detach
	return
default:
	if t.detaching.Load() {
		// pump exited because Detach closed the PTY — not abnormal
		return
	}
	// genuinely abnormal exit
	fmt.Fprintln(os.Stderr, "Error: Session terminated without detaching...")
	// ... rest of existing abnormal-path cleanup ...
}
```

**Step 4: Build**

Run: `go build ./...`
Expected: PASS

**Step 5: Manual test**

Attach (`enter` or `O`), Ctrl+Q to detach. The spurious error should no longer appear.

**Step 6: Commit**

```bash
git add session/tmux/tmux.go
git commit -m "fix(tmux): suppress spurious abnormal-exit message on normal detach

Addresses TMUX-04. Every Ctrl+Q previously printed 'Error: Session
terminated without detaching' because the monitor couldn't tell a
normal PTY close from a process death."
```

---

### Task 8.2: Track the stdin reader in the WaitGroup and make it cancellable

**Files:**
- Modify: `session/tmux/tmux.go` — the `Attach` stdin-reader goroutine around line 415

**Step 1: Capture locals at goroutine start**

At the point the goroutine spawns, capture the ctx, wg, and ptmx into locals so the goroutine doesn't race on field reassignment:

```go
ctx := t.ctx
ptmx := t.ptmx
wg := t.wg

wg.Add(1)
go func() {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		// Check cancellation before each read.
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Use a short read deadline so we observe ctx.Done without
		// indefinitely blocking on stdin.
		_ = os.Stdin.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		nr, err := os.Stdin.Read(buf)
		if err != nil {
			if os.IsTimeout(err) {
				continue
			}
			return
		}
		// ... existing Ctrl+Q detection and ptmx.Write ...
	}
}()
```

Note: `os.Stdin.SetReadDeadline` only works if stdin is a character device that supports it (typical TTY). On platforms where it fails, the reader falls back to the blocking behavior — acceptable since platforms that hit the leak scenario also support deadlines.

**Step 2: Remove the `t.ctx = nil` / `t.wg = nil` assignments in Detach defer**

Search for the Detach defer that nils these fields. Delete the nil assignments — they cause the stdin reader to race on a nil ctx. The fields will be overwritten on the next Attach.

**Step 3: Run the build**

Run: `go build ./...`
Expected: PASS

**Step 4: Manual test**

Attach → Ctrl+Q → Attach again → type keys. All keystrokes should reach the new session; none should be lost or logged as "nuked".

**Step 5: Commit**

```bash
git add session/tmux/tmux.go
git commit -m "fix(tmux): track stdin reader in wg, use read deadlines for cancellation

Addresses TMUX-05 and TMUX-07. The stdin reader no longer leaks
across Attach/Detach cycles, so repeated attach sessions don't
compete for stdin bytes. Detach no longer nils fields that the old
reader might still read (TMUX-07)."
```

---

### Task 8.3: Verify Phase 8

Run: `go test ./...`
Expected: PASS

---

## Phase 9 — Workspace registry file locking

`workspace add/remove/rename/migrate` and the running TUI both do load-modify-save on `workspaces.json`. Without a file lock, changes are lost. Fixes STORE-04, STORE-11.

### Task 9.1: Add a `flock`-based lock helper

**Files:**
- Create: `config/flock_unix.go`
- Create: `config/flock_windows.go`

**Step 1: Unix implementation**

```go
//go:build !windows

package config

import (
	"fmt"
	"os"
	"syscall"
)

// LockFile acquires an exclusive advisory lock on the given path,
// creating it if necessary. Returns an unlock function that closes
// the file and releases the lock.
func LockFile(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return func() error {
		defer f.Close()
		return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}, nil
}
```

**Step 2: Windows stub**

```go
//go:build windows

package config

import "os"

// LockFile on Windows uses LockFileEx (TODO). For now, use a simple
// open-exclusive approach — this is best-effort and should be replaced
// with golang.org/x/sys/windows LockFileEx on a later pass.
func LockFile(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	return func() error { return f.Close() }, nil
}
```

**Step 3: Commit**

```bash
git add config/flock_unix.go config/flock_windows.go
git commit -m "feat(config): add LockFile helper (flock on Unix, stub on Windows)"
```

---

### Task 9.2: Use `LockFile` around workspace registry load-modify-save

**Files:**
- Modify: `config/workspace.go` — `Add`, `Remove`, `Rename`, `UpdateLastUsed`

**Step 1: Wrap each mutator**

```go
func (r *WorkspaceRegistry) Add(name, path string) error {
	unlock, err := LockFile(workspaceLockPath(r.configDir))
	if err != nil {
		return err
	}
	defer unlock()

	// Re-read from disk under lock to observe concurrent edits.
	fresh, err := LoadWorkspaceRegistryFrom(r.configDir)
	if err != nil {
		return err
	}
	*r = *fresh

	// ... existing Add body: append to r.Workspaces ...

	return SaveWorkspaceRegistry(r)
}
```

Apply to `Remove`, `Rename`, `UpdateLastUsed`. The lock file is a dedicated `workspaces.json.lock` (not the JSON file itself, so reads don't block).

**Step 2: Run tests**

Run: `go test -v ./config/`
Expected: PASS

**Step 3: Commit**

```bash
git add config/workspace.go
git commit -m "fix(config): lock workspaces.json during load-modify-save

Addresses STORE-04. Concurrent 'workspace remove' from CLI and
'UpdateLastUsed' from the TUI no longer clobber each other."
```

---

### Task 9.3: Refuse `workspace migrate` while a TUI is running

**Files:**
- Modify: `cmd/workspace.go` — `workspaceMigrateCmd`
- Modify: `config/workspace.go` — export `workspaceLockPath`

**Step 1: Try-lock before migration**

At the start of `workspaceMigrateCmd`, try to acquire `workspace_migrate.lock` non-blocking. If it fails, print a friendly message and exit:

```go
unlock, err := LockFileNonBlocking(filepath.Join(configDir, "workspace_migrate.lock"))
if err != nil {
	return fmt.Errorf("another workspace migration is in progress; try again later")
}
defer unlock()
```

Implement `LockFileNonBlocking` in `flock_unix.go` using `LOCK_EX|LOCK_NB`.

**Step 2: Hold the registry lock for the whole migration**

Inside the migrate command, also acquire the registry lock (from Task 9.2) across the full migration.

**Step 3: Commit**

```bash
git add cmd/workspace.go config/
git commit -m "fix(cmd): serialize workspace migrate behind a lock

Addresses STORE-11."
```

---

### Task 9.4: Verify Phase 9

Manual: Open the TUI in terminal A. In terminal B, run `claude-squad workspace add ./foo` then `claude-squad workspace remove foo` repeatedly while the TUI is focused. The registry should reflect the final CLI state; no "ghost" workspace should appear.

Run: `go test ./...`
Expected: PASS

---

## Phase 10 — Verification

### Task 10.1: Add a targeted race test exercising the kill-during-tick scenario

**Files:**
- Create: `app/race_test.go`

**Step 1: Write an integration-style test**

```go
package app

import (
	"sync"
	"testing"
	"time"

	"claude-squad/session"
)

// TestKillDuringMetadataTick reproduces APP-01: a kill Cmd that
// overlaps with an in-flight tick fan-out goroutine. Runs under -race.
func TestKillDuringMetadataTick(t *testing.T) {
	// Skip unless a real git repo is available in a temp dir.
	// Set up: create an Instance (maybe a mocked one), spawn a
	// tick-like goroutine that calls instance.GetStatus() / GetDiffStats()
	// in a loop, and concurrently call instance.SetStatus(Deleting)
	// and instance.Kill() on the same Instance.
	inst := &session.Instance{Title: "race-test", Status: session.Running}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = inst.GetStatus()
				_ = inst.GetDiffStats()
			}
		}
	}()

	// Let the tick-analog run briefly.
	time.Sleep(10 * time.Millisecond)
	inst.SetStatus(session.Deleting)
	// (We don't call inst.Kill() here because it requires real tmux
	// and worktree handles; the SetStatus write under lock is enough
	// to exercise the race site.)

	close(stop)
	wg.Wait()
}
```

**Step 2: Run**

Run: `go test -race -run TestKillDuringMetadataTick -v ./app/`
Expected: PASS

**Step 3: Commit**

```bash
git add app/race_test.go
git commit -m "test(app): cover kill-during-tick race path under -race"
```

---

### Task 10.2: Run the whole suite with the race detector

Run: `go test -race ./...`
Expected: PASS

If any race is reported, open the relevant audit finding and address it before moving on.

---

### Task 10.3: Lint and format

Run: `gofmt -w .`
Run: `golangci-lint run --timeout=3m --fast`
Expected: clean

---

### Task 10.4: Final commit

If anything needed reformatting:

```bash
git add -A
git commit -m "chore: gofmt after race-condition fix phases"
```

---

## Appendix: Findings not addressed in this plan

The audit identified 91 findings. This plan fixes ~30 Critical/High severity issues. The remainder — Medium/Low/Speculative — are deferred to a follow-up plan. Key deferred items:

- **DAEMON-05** (daemon PTY double-attach): requires a PTY-less refactor of the daemon's tmux interface (use `tmux send-keys` / `tmux capture-pane` via exec). Significant scope.
- **APP-13** (full-screen attach blocks main goroutine): requires rethinking the attach dismissal flow into a tea.Cmd.
- **TMUX-01** (`prevOutputHash` data race): partially addressed by the Phase 2 Instance mutex, but `TmuxSession` itself still has unguarded fields.
- **STORE-12** (`claude-squad reset` vs running TUI): needs a TUI lockfile.
- **GIT-15** (git commands have no timeout/context): wrap every `exec.Command` in `exec.CommandContext`.
- Most Low/Speculative findings are either defense-in-depth improvements or currently-unreachable hazards.

When ready to tackle these, create `docs/plans/<date>-race-condition-fixes-followup.md` and repeat the pattern.
