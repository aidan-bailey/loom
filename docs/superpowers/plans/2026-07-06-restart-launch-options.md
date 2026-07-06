# Restarting a Paused Session with Different Launch Options — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a Paused instance be resumed with different launch options (Remote Control / Permission Mode / Model / Headroom Wrap / Effort), building on three prerequisites: switching Pause/Resume's uncommitted-work handling from an auto-commit to `git stash`, renaming the misleadingly-named "checkout" keybinding to "stash" to match, and adding a fifth launch option, Effort (`--effort <level>`).

**Architecture:** Four phases, each independently shippable. Phase 1 replaces `Instance.Pause`'s auto-commit with `git stash create --include-untracked` + `git stash store`, tracking the resulting SHA on the persisted `GitWorktreeData` so `Instance.Resume` can restore it by exact commit (never by stack position — `refs/stash` is shared across every worktree of a repo). Phase 2 is a pure rename (`checkout` → `stash`, key `c` → `s`) following from Phase 1's mechanism change. Phase 3 adds the Effort option end-to-end, mirroring the existing Model option exactly. Phase 4 adds `ParseLaunchOptions` (the reverse of `applyLaunchOptions`, living next to it in `app/remote_control.go` to avoid an import cycle through `ui/overlay`) and wires a new `R` keybinding that reverse-parses a Paused instance's `Program`, reuses the existing Session Launch Options modal, and resumes with the edited options.

**Tech Stack:** Go 1.23, Bubble Tea v2 (`charm.land/bubbletea/v2`), gopher-lua for the scripting layer, testify for assertions.

**Design doc:** `docs/superpowers/specs/2026-07-06-restart-launch-options-design.md`

---

## Phase 1 — Stash-based Pause/Resume

### Task 1: Add StashChanges/ApplyStash/DropStash to GitWorktree

**Files:**
- Modify: `session/git/worktree_git.go`
- Test: `session/git/worktree_git_test.go` (new file)

- [ ] **Step 1: Write the failing tests**

Create `session/git/worktree_git_test.go`:

```go
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStashTestRepo creates a real git repo with one commit and a
// GitWorktree pointed at its root (repoPath == worktreePath — no
// linked worktree needed to exercise stash create/store/apply/drop,
// which all operate on a plain working tree).
func newStashTestRepo(t *testing.T) *GitWorktree {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v failed: %s", args, string(out))
	}
	run("git", "init", "-b", "main")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1\n"), 0644))
	run("git", "add", ".")
	run("git", "commit", "-m", "init")

	return NewGitWorktreeFromStorage(dir, dir, "stash-test", "main", "", true, dir)
}

func TestStashChanges_TrackedAndUntracked(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0644))

	sha, err := gw.StashChanges("test pause stash")
	require.NoError(t, err)
	assert.NotEmpty(t, sha)

	// StashChanges must not touch the working tree — `git stash
	// create` only builds the commit object.
	tracked, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "v2\n", string(tracked))
	_, err = os.Stat(filepath.Join(dir, "untracked.txt"))
	assert.NoError(t, err, "untracked file must still be present after StashChanges")

	// The stash is visible via `git stash list` (stored, not just a
	// dangling commit) so a user can recover it manually if needed.
	out, err := exec.Command("git", "-C", dir, "stash", "list").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "test pause stash")
}

func TestStashChanges_CleanWorktreeReturnsEmpty(t *testing.T) {
	gw := newStashTestRepo(t)

	sha, err := gw.StashChanges("nothing to stash")
	require.NoError(t, err)
	assert.Empty(t, sha)

	out, err := exec.Command("git", "-C", gw.GetWorktreePath(), "stash", "list").CombinedOutput()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)))
}

func TestApplyStash_RestoresAndDrops(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0644))
	sha, err := gw.StashChanges("test pause stash")
	require.NoError(t, err)

	// Simulate Pause's worktree teardown: reset to what Setup() would
	// recreate on Resume (clean, at the commit the branch pointed to).
	require.NoError(t, exec.Command("git", "-C", dir, "checkout", "--", "tracked.txt").Run())
	require.NoError(t, os.Remove(filepath.Join(dir, "untracked.txt")))

	require.NoError(t, gw.ApplyStash(sha))

	tracked, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "v2\n", string(tracked))
	untracked, err := os.ReadFile(filepath.Join(dir, "untracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(untracked))

	out, err := exec.Command("git", "-C", dir, "stash", "list").CombinedOutput()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)), "ApplyStash must drop the entry after a clean apply")
}

func TestApplyStash_EmptyShaIsNoOp(t *testing.T) {
	gw := newStashTestRepo(t)
	assert.NoError(t, gw.ApplyStash(""))
}

func TestApplyStash_ConflictLeavesStashInPlace(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("stashed-version\n"), 0644))
	sha, err := gw.StashChanges("conflicting stash")
	require.NoError(t, err)

	// Reset to clean, then make a conflicting change so applying the
	// stash on top produces a conflict.
	require.NoError(t, exec.Command("git", "-C", dir, "checkout", "--", "tracked.txt").Run())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("different-version\n"), 0644))

	err = gw.ApplyStash(sha)
	assert.Error(t, err)

	out, err := exec.Command("git", "-C", dir, "stash", "list").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "conflicting stash", "a conflicting apply must not drop the stash entry")
}

// TestStashDoesNotInterfereAcrossWorktrees is the regression guard for
// the design's core hazard: refs/stash is a single stack shared by
// every worktree of the same repo. Two GitWorktrees stashing around
// the same time must each apply their own changes, never the other's,
// regardless of push order.
func TestStashDoesNotInterfereAcrossWorktrees(t *testing.T) {
	base := t.TempDir()
	run := func(dir string, args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v failed: %s", args, string(out))
	}
	repo := filepath.Join(base, "repo")
	require.NoError(t, os.MkdirAll(repo, 0755))
	run(repo, "git", "init", "-b", "main")
	run(repo, "git", "config", "user.email", "test@example.com")
	run(repo, "git", "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f.txt"), []byte("base\n"), 0644))
	run(repo, "git", "add", ".")
	run(repo, "git", "commit", "-m", "init")

	wtA := filepath.Join(base, "wtA")
	wtB := filepath.Join(base, "wtB")
	run(repo, "git", "worktree", "add", "-b", "branch-a", wtA)
	run(repo, "git", "worktree", "add", "-b", "branch-b", wtB)

	gwA := NewGitWorktreeFromStorage(repo, wtA, "a", "branch-a", "", true, base)
	gwB := NewGitWorktreeFromStorage(repo, wtB, "b", "branch-b", "", true, base)

	require.NoError(t, os.WriteFile(filepath.Join(wtA, "f.txt"), []byte("a-changes\n"), 0644))
	shaA, err := gwA.StashChanges("from A")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(wtB, "f.txt"), []byte("b-changes\n"), 0644))
	shaB, err := gwB.StashChanges("from B")
	require.NoError(t, err)

	require.NoError(t, exec.Command("git", "-C", wtA, "checkout", "--", "f.txt").Run())
	require.NoError(t, exec.Command("git", "-C", wtB, "checkout", "--", "f.txt").Run())

	require.NoError(t, gwB.ApplyStash(shaB))
	contentB, err := os.ReadFile(filepath.Join(wtB, "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, "b-changes\n", string(contentB), "B must restore its own changes, not A's")

	require.NoError(t, gwA.ApplyStash(shaA))
	contentA, err := os.ReadFile(filepath.Join(wtA, "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, "a-changes\n", string(contentA), "A must restore its own changes, not B's")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./session/git/... -run 'TestStashChanges|TestApplyStash|TestStashDoesNotInterfere' -v`
Expected: FAIL — `gw.StashChanges undefined (type *GitWorktree has no field or method StashChanges)`, same for `ApplyStash`.

- [ ] **Step 3: Implement StashChanges, ApplyStash, DropStash**

In `session/git/worktree_git.go`, add after `CommitChanges` (which stays — `PushChanges` still uses it):

```go
// StashChanges snapshots the worktree's tracked and untracked changes
// into a stash commit without disturbing the shared stash stack's
// ordering: `git stash create` builds the commit object without
// pushing it onto refs/stash at all, so the returned SHA — never
// stack position — is what ApplyStash/DropStash target. `git stash
// store` then anchors that commit against gc (dangling commits are
// prunable after gc.pruneExpire) and makes it visible via `git stash
// list` for manual recovery. Returns "" (no error) if the worktree
// has nothing to stash, mirroring IsDirty's scope (tracked and
// untracked, via --include-untracked).
func (g *GitWorktree) StashChanges(message string) (string, error) {
	out, err := g.runGitCommand(g.worktreePath, "stash", "create", "--include-untracked", "-m", message)
	if err != nil {
		return "", fmt.Errorf("failed to create stash: %w", err)
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", nil
	}
	if _, err := g.runGitCommand(g.worktreePath, "stash", "store", "-m", message, sha); err != nil {
		return "", fmt.Errorf("failed to store stash: %w", err)
	}
	return sha, nil
}

// ApplyStash applies the stash commit sha onto the worktree, targeting
// the exact commit rather than stack position (stash@{0}) — refs/stash
// is shared across every worktree of this repo. A no-op for an empty
// sha. On a clean apply, the matching stash-list entry is dropped
// (best-effort; a failed drop is logged, not returned, since the
// restore itself already succeeded). On conflict, the stash entry is
// left in place — mirroring `git stash pop`'s own safety behavior —
// and the error is returned so the caller does not clear its
// reference to it.
func (g *GitWorktree) ApplyStash(sha string) error {
	if sha == "" {
		return nil
	}
	if _, err := g.runGitCommand(g.worktreePath, "stash", "apply", sha); err != nil {
		return fmt.Errorf("failed to apply stash: %w", err)
	}
	if err := g.DropStash(sha); err != nil {
		log.For("git").Warn("stash.drop_after_apply_failed", "err", err.Error())
	}
	return nil
}

// DropStash removes the stash-list entry matching sha, if present.
// No-op if sha is empty or no longer found (already dropped, or never
// stored). Resolves each entry's own commit hash via `git rev-parse`
// rather than assuming stack position, for the same reason ApplyStash
// does — refs/stash is shared across every worktree of this repo, so
// "top of stack" could belong to a different worktree by now. Runs
// against repoPath rather than worktreePath since refs/stash is a
// repo-level concept and the worktree directory may not exist at call
// time (e.g. after Kill has already removed it).
func (g *GitWorktree) DropStash(sha string) error {
	if sha == "" {
		return nil
	}
	list, err := g.runGitCommand(g.repoPath, "stash", "list", "--format=%gd")
	if err != nil {
		return fmt.Errorf("failed to list stash: %w", err)
	}
	for _, ref := range strings.Fields(list) {
		out, err := g.runGitCommand(g.repoPath, "rev-parse", ref)
		if err != nil {
			continue
		}
		if strings.TrimSpace(out) == sha {
			if _, err := g.runGitCommand(g.repoPath, "stash", "drop", ref); err != nil {
				return fmt.Errorf("failed to drop stash %s: %w", ref, err)
			}
			return nil
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./session/git/... -run 'TestStashChanges|TestApplyStash|TestStashDoesNotInterfere' -v`
Expected: PASS (all 6 new tests)

- [ ] **Step 5: Commit**

```bash
git add session/git/worktree_git.go session/git/worktree_git_test.go
git commit -m "feat(git): add StashChanges/ApplyStash/DropStash to GitWorktree"
```

### Task 2: Add a mutable stashRef field to GitWorktree

**Files:**
- Modify: `session/git/worktree.go`
- Test: `session/git/worktree_test.go` (existing or new — check first)

- [ ] **Step 1: Check for an existing worktree_test.go**

Run: `ls session/git/worktree_test.go 2>/dev/null && echo exists || echo "does not exist"`

If it doesn't exist, create it with the test below. If it exists, append the test function.

- [ ] **Step 2: Write the failing test**

```go
package git

import "testing"

import "github.com/stretchr/testify/assert"

func TestGitWorktree_StashRefGetSet(t *testing.T) {
	gw := NewGitWorktreeFromStorage("/repo", "/wt", "s", "b", "", true, "")
	assert.Empty(t, gw.GetStashRef())

	gw.SetStashRef("abc123")
	assert.Equal(t, "abc123", gw.GetStashRef())

	gw.SetStashRef("")
	assert.Empty(t, gw.GetStashRef())
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./session/git/... -run TestGitWorktree_StashRefGetSet -v`
Expected: FAIL — `gw.GetStashRef undefined`

- [ ] **Step 4: Add the field and accessors**

In `session/git/worktree.go`, add to the `GitWorktree` struct (after `isExistingBranch`):

```go
	// isExistingBranch is true if the branch existed before the session was created.
	// When true, the branch will not be deleted on cleanup.
	isExistingBranch bool
	// stashRef is the commit SHA of the stash Pause created for this
	// worktree's uncommitted changes, or "" if none is pending. Set by
	// Instance.Pause (via StashChanges), cleared by Instance.Resume
	// after a clean ApplyStash, or seeded from persisted
	// GitWorktreeData.StashRef when rehydrating a Paused instance from
	// disk (FromInstanceData, fromInstanceDataPaused).
	stashRef string
```

Add accessors after `GetBaseCommitSHA`:

```go
// GetStashRef returns the pending stash commit SHA, or "" if none.
func (g *GitWorktree) GetStashRef() string {
	return g.stashRef
}

// SetStashRef sets the pending stash commit SHA (or clears it, for
// "").
func (g *GitWorktree) SetStashRef(sha string) {
	g.stashRef = sha
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./session/git/... -run TestGitWorktree_StashRefGetSet -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add session/git/worktree.go session/git/worktree_test.go
git commit -m "feat(git): add a mutable StashRef field to GitWorktree"
```

### Task 3: Persist StashRef — schema bump, migration, fixtures

**Files:**
- Modify: `session/storage.go`
- Modify: `session/storage_migrate.go`
- Modify: `cmd/workspace_migrate.go`
- Test: `session/storage_migrate_test.go` (check for existing tests first)
- Test: `cmd/workspace_migrate_shape_test.go`

- [ ] **Step 1: Bump the schema version and add GitWorktreeData.StashRef**

In `session/storage.go`, change:

```go
const CurrentSchemaVersion = 2
```
to
```go
const CurrentSchemaVersion = 3
```

And in `GitWorktreeData`, add the field:

```go
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
```

- [ ] **Step 2: Add the migration case**

In `session/storage_migrate.go`, add a case before `default:`:

```go
		case 1:
			// v1 → v2: AutoYes removed. No payload changes needed —
			// unmarshal already dropped the field — just stamp the version.
			data.SchemaVersion = 2
		case 2:
			// v2 → v3: GitWorktreeData gained StashRef. No payload
			// changes needed — unmarshal already defaults a missing
			// field to "" — just stamp the version.
			data.SchemaVersion = 3
		default:
```

- [ ] **Step 3: Write the migration regression test**

Check first: `grep -n "func Test" session/storage_migrate_test.go 2>/dev/null || echo "file does not exist"`

If the file exists, add this test function to it; otherwise create `session/storage_migrate_test.go`:

```go
package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrate_V2RecordGetsEmptyStashRef(t *testing.T) {
	raw := []byte(`{
		"schema_version": 2,
		"title": "t",
		"path": "/p",
		"branch": "b",
		"status": 3,
		"program": "claude",
		"worktree": {
			"repo_path": "/r",
			"worktree_path": "/wt",
			"session_name": "t",
			"branch_name": "b",
			"base_commit_sha": "abc",
			"is_existing_branch": true
		}
	}`)

	data, err := Migrate(raw)
	require.NoError(t, err)
	assert.Equal(t, CurrentSchemaVersion, data.SchemaVersion)
	assert.Empty(t, data.Worktree.StashRef)

	// Round-trips cleanly through JSON at the new version too.
	out, err := json.Marshal(data)
	require.NoError(t, err)
	assert.NotContains(t, string(out), `"stash_ref"`, "omitempty must drop an empty StashRef")
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./session/... -run TestMigrate_V2RecordGetsEmptyStashRef -v`
Expected: PASS

- [ ] **Step 5: Update the cmd package's typed mirror (drift guard)**

In `cmd/workspace_migrate.go`, add the matching field to `migrationWorktreeData`:

```go
type migrationWorktreeData struct {
	RepoPath         string `json:"repo_path"`
	WorktreePath     string `json:"worktree_path"`
	SessionName      string `json:"session_name"`
	BranchName       string `json:"branch_name"`
	BaseCommitSHA    string `json:"base_commit_sha"`
	IsExistingBranch bool   `json:"is_existing_branch"`
	StashRef         string `json:"stash_ref,omitempty"`
}
```

- [ ] **Step 6: Update the fixture test**

In `cmd/workspace_migrate_shape_test.go`, add `"stash_ref": "def456"` to the `worktree` object in the JSON fixture inside `TestMigrationInstance_MirrorsInstanceData_JSON`:

```go
		"worktree": {
			"repo_path": "/r",
			"worktree_path": "/wt",
			"session_name": "t",
			"branch_name": "b",
			"base_commit_sha": "abc",
			"is_existing_branch": false,
			"stash_ref": "def456"
		},
```

- [ ] **Step 7: Run the full cmd and session test suites**

Run: `go test ./cmd/... ./session/... -v 2>&1 | tail -60`
Expected: PASS — `TestMigrationInstance_MirrorsInstanceData_JSON` and `TestMigrationInstance_TypeDriftGuard` both pass with the new field present on both sides.

- [ ] **Step 8: Commit**

```bash
git add session/storage.go session/storage_migrate.go session/storage_migrate_test.go cmd/workspace_migrate.go cmd/workspace_migrate_shape_test.go
git commit -m "feat(session): persist StashRef on GitWorktreeData, bump schema to v3"
```

### Task 4: Wire Instance.Pause to StashChanges

**Files:**
- Modify: `session/instance.go`
- Test: `session/instance_lifecycle_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/instance_lifecycle_test.go` (it already has `newTestPausableInstance`, a real-git-repo fixture — reuse it):

```go
// TestInstance_PauseStashesUncommittedWork is the stash-mechanism
// regression guard: Pause must preserve tracked and untracked changes
// via git stash (StashRef ends up set) instead of the old auto-commit
// (no new commit lands on the branch).
func TestInstance_PauseStashesUncommittedWork(t *testing.T) {
	inst := newTestPausableInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("uncommitted\n"), 0644))

	logBefore, err := exec.Command("git", "-C", dir, "log", "--oneline").CombinedOutput()
	require.NoError(t, err)

	require.NoError(t, inst.Pause(nil))

	assert.NotEmpty(t, gw.GetStashRef(), "Pause must record a StashRef when the worktree was dirty")

	// No new commit landed — Pause no longer auto-commits.
	logAfter, err := exec.Command("git", "-C", inst.Path, "log", "main", "--oneline").CombinedOutput()
	_ = logAfter // repo may differ; the real assertion is on the branch below
	branchLog, err := exec.Command("git", "-C", filepath.Dir(dir), "log", "pause-test-branch", "--oneline").CombinedOutput()
	require.NoError(t, err)
	assert.Equal(t, string(logBefore), string(branchLog), "Pause must not add a commit to the branch")
}

// TestInstance_PauseCleanWorktreeSetsNoStashRef ensures a clean pause
// doesn't fabricate a stash entry.
func TestInstance_PauseCleanWorktreeSetsNoStashRef(t *testing.T) {
	inst := newTestPausableInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)

	require.NoError(t, inst.Pause(nil))
	assert.Empty(t, gw.GetStashRef())
}
```

Add `"os/exec"` and `"path/filepath"` to the test file's imports if not already present (both already imported per the existing `newTestPausableInstance` helper).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./session/... -run 'TestInstance_PauseStashesUncommittedWork|TestInstance_PauseCleanWorktreeSetsNoStashRef' -v`
Expected: FAIL — `gw.GetStashRef()` returns empty even when dirty, because Pause still calls the old commit path.

- [ ] **Step 3: Replace the commit call with StashChanges**

In `session/instance.go`, in `Pause`, replace:

```go
	gw := i.getGitWorktree()
	ts := i.getTmuxSession()
	var errs []error

	// Check if there are any changes to commit
	if dirty, err := gw.IsDirty(); err != nil {
		errs = append(errs, fmt.Errorf("failed to check if worktree is dirty: %w", err))
	} else if dirty {
		// Commit changes locally (without pushing to GitHub)
		commitMsg := fmt.Sprintf("[loom] update from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
		if err := gw.CommitChanges(commitMsg); err != nil {
			errs = append(errs, fmt.Errorf("failed to commit changes: %w", err))
			// Return early if we can't commit changes to avoid corrupted state
			return i.combineErrors(errs)
		}
	}
```

with:

```go
	gw := i.getGitWorktree()
	ts := i.getTmuxSession()
	var errs []error

	// Stash any uncommitted changes (tracked and untracked) so Resume
	// can restore them without polluting the branch's real history
	// with a synthetic checkpoint commit.
	stashMsg := fmt.Sprintf("[loom] stash from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
	sha, err := gw.StashChanges(stashMsg)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to stash changes: %w", err))
		// Return early if we can't stash changes to avoid corrupted state
		return i.combineErrors(errs)
	}
	gw.SetStashRef(sha)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./session/... -run 'TestInstance_PauseStashesUncommittedWork|TestInstance_PauseCleanWorktreeSetsNoStashRef' -v`
Expected: PASS

- [ ] **Step 5: Run the full existing Pause/Resume test suite for regressions**

Run: `go test ./session/... -run 'TestInstance_Pause|TestInstance_Resume' -v`
Expected: PASS — `TestInstance_PausePropagatesSaveStateError` and `TestInstance_PauseHappyPathStillReturnsNil` (pre-existing) still pass with the new stash path.

- [ ] **Step 6: Commit**

```bash
git add session/instance.go session/instance_lifecycle_test.go
git commit -m "feat(session): stash uncommitted changes on Pause instead of auto-committing"
```

### Task 5: Wire Instance.Resume to ApplyStash

**Files:**
- Modify: `session/instance.go`
- Test: `session/instance_lifecycle_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/instance_lifecycle_test.go`:

```go
// TestInstance_ResumeRestoresStashedWork is the Pause→Resume round
// trip: uncommitted work stashed by Pause must reappear after Resume,
// and StashRef must be cleared on success.
func TestInstance_ResumeRestoresStashedWork(t *testing.T) {
	inst := newTestPausableInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("uncommitted\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("untracked\n"), 0644))
	require.NoError(t, inst.Pause(nil))
	require.NotEmpty(t, gw.GetStashRef())

	require.NoError(t, inst.Resume(nil))

	assert.Empty(t, gw.GetStashRef(), "Resume must clear StashRef after a clean apply")
	dirty, err := os.ReadFile(filepath.Join(gw.GetWorktreePath(), "dirty.txt"))
	require.NoError(t, err)
	assert.Equal(t, "uncommitted\n", string(dirty))
	untracked, err := os.ReadFile(filepath.Join(gw.GetWorktreePath(), "new.txt"))
	require.NoError(t, err)
	assert.Equal(t, "untracked\n", string(untracked))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./session/... -run TestInstance_ResumeRestoresStashedWork -v`
Expected: FAIL — the restored files are missing, since Resume doesn't yet call ApplyStash.

- [ ] **Step 3: Wire ApplyStash into Resume**

In `session/instance.go`, in `Resume`, after the `gw.Setup()` block and before the `ts.DoesSessionExist()` check, insert:

```go
	// Setup git worktree
	if err := gw.Setup(); err != nil {
		if errors.Is(err, git.ErrBranchGone) {
			return fmt.Errorf("branch %q was deleted externally — kill this instance (D) to clean up: %w", gw.GetBranchName(), err)
		}
		return fmt.Errorf("failed to setup git worktree: %w", err)
	}

	// Restore any changes stashed by Pause. A failed apply (e.g. a
	// conflict) is surfaced rather than silently dropped or retried —
	// mirrors git's own refusal to drop a stash that didn't apply
	// cleanly. StashRef stays set so the entry (and any conflict
	// markers left in the worktree) remain available for the user to
	// resolve manually.
	if sha := gw.GetStashRef(); sha != "" {
		if err := gw.ApplyStash(sha); err != nil {
			return fmt.Errorf("failed to restore stashed changes: %w", err)
		}
		gw.SetStashRef("")
	}

	// Check if tmux session still exists from pause, otherwise create new one
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./session/... -run TestInstance_ResumeRestoresStashedWork -v`
Expected: PASS

- [ ] **Step 5: Run the full session package test suite for regressions**

Run: `go test ./session/... -v 2>&1 | tail -80`
Expected: PASS — all existing Pause/Resume/reconcile tests unaffected.

- [ ] **Step 6: Commit**

```bash
git add session/instance.go session/instance_lifecycle_test.go
git commit -m "feat(session): restore stashed changes on Resume"
```

### Task 6: Thread StashRef through Snapshot / FromInstanceData / reconcile

**Files:**
- Modify: `session/instance.go`
- Modify: `session/reconcile.go`
- Test: `session/instance_lifecycle_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/instance_lifecycle_test.go`:

```go
// TestInstance_StashRefSurvivesSnapshotRoundTrip guards the disk
// round-trip: a Paused instance's StashRef must persist through
// ToInstanceData → FromInstanceData, or a restart of loom itself
// would lose track of a pending stash.
func TestInstance_StashRefSurvivesSnapshotRoundTrip(t *testing.T) {
	inst := newTestPausableInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)

	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("uncommitted\n"), 0644))
	require.NoError(t, inst.Pause(nil))
	wantSha := gw.GetStashRef()
	require.NotEmpty(t, wantSha)

	data := inst.ToInstanceData()
	assert.Equal(t, wantSha, data.Worktree.StashRef)

	restored, err := FromInstanceData(data, "")
	require.NoError(t, err)
	restoredGw, err := restored.GetGitWorktree()
	require.NoError(t, err)
	assert.Equal(t, wantSha, restoredGw.GetStashRef())
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./session/... -run TestInstance_StashRefSurvivesSnapshotRoundTrip -v`
Expected: FAIL — `data.Worktree.StashRef` is empty (Snapshot doesn't populate it yet), and even if it did, `FromInstanceData` doesn't set it on the rehydrated GitWorktree.

- [ ] **Step 3: Populate StashRef in Snapshot**

In `session/instance.go`, in `Snapshot`, add `StashRef` to the `GitWorktreeData` literal:

```go
	if i.gitWorktree != nil {
		data.Worktree = GitWorktreeData{
			RepoPath:         i.gitWorktree.GetRepoPath(),
			WorktreePath:     i.gitWorktree.GetWorktreePath(),
			SessionName:      i.Title,
			BranchName:       i.gitWorktree.GetBranchName(),
			BaseCommitSHA:    i.gitWorktree.GetBaseCommitSHA(),
			IsExistingBranch: i.gitWorktree.IsExistingBranch(),
			StashRef:         i.gitWorktree.GetStashRef(),
		}
	}
```

- [ ] **Step 4: Seed StashRef when rehydrating in FromInstanceData**

In `session/instance.go`, in `FromInstanceData`, replace:

```go
	// Workspace terminals don't use git worktrees
	if !data.IsWorkspaceTerminal {
		instance.setGitWorktree(git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
			data.Worktree.IsExistingBranch,
			configDir,
		))
	}
```

with:

```go
	// Workspace terminals don't use git worktrees
	if !data.IsWorkspaceTerminal {
		gw := git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
			data.Worktree.IsExistingBranch,
			configDir,
		)
		gw.SetStashRef(data.Worktree.StashRef)
		instance.setGitWorktree(gw)
	}
```

- [ ] **Step 5: Do the same in reconcile.go's fromInstanceDataPaused**

In `session/reconcile.go`, replace:

```go
	if !data.IsWorkspaceTerminal {
		instance.setGitWorktree(git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
			data.Worktree.IsExistingBranch,
			configDir,
		))
	}
```

with:

```go
	if !data.IsWorkspaceTerminal {
		gw := git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
			data.Worktree.IsExistingBranch,
			configDir,
		)
		gw.SetStashRef(data.Worktree.StashRef)
		instance.setGitWorktree(gw)
	}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./session/... -run TestInstance_StashRefSurvivesSnapshotRoundTrip -v`
Expected: PASS

- [ ] **Step 7: Run the full session suite**

Run: `go test ./session/... -v 2>&1 | tail -80`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add session/instance.go session/reconcile.go session/instance_lifecycle_test.go
git commit -m "feat(session): thread StashRef through Snapshot/FromInstanceData/reconcile"
```

### Task 7: Drop any leftover stash on Kill

**Files:**
- Modify: `session/instance.go`
- Test: `session/instance_lifecycle_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/instance_lifecycle_test.go`:

```go
// TestInstance_KillDropsLeftoverStash guards against a Paused
// instance's stash leaking on the shared refs/stash stack forever when
// the user kills it (D) instead of resuming.
func TestInstance_KillDropsLeftoverStash(t *testing.T) {
	inst := newTestPausableInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	repoPath := gw.GetRepoPath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("uncommitted\n"), 0644))
	require.NoError(t, inst.Pause(nil))
	require.NotEmpty(t, gw.GetStashRef())

	// Kill needs isStarted()==true, which Pause leaves it as (the
	// instance is Paused, not un-started).
	require.NoError(t, inst.Kill())

	out, err := exec.Command("git", "-C", repoPath, "stash", "list").CombinedOutput()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)), "Kill must drop a leftover stash, not leak it")
}
```

Add `"strings"` to the test file's imports if not already present.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./session/... -run TestInstance_KillDropsLeftoverStash -v`
Expected: FAIL — the stash entry is still listed after Kill.

- [ ] **Step 3: Drop the stash in Kill**

In `session/instance.go`, in `Kill`, replace:

```go
	// Then clean up git worktree (workspace terminals don't have one)
	if gitWT != nil && !isWorkspaceTerm {
		if err := gitWT.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup git worktree: %w", err))
		}
	}
```

with:

```go
	// Then clean up git worktree (workspace terminals don't have one)
	if gitWT != nil && !isWorkspaceTerm {
		// A Paused-then-killed instance may still have an unapplied
		// stash (never resumed). Drop it so it doesn't leak on the
		// shared refs/stash stack forever, referencing a branch that
		// Cleanup is about to delete.
		if sha := gitWT.GetStashRef(); sha != "" {
			if err := gitWT.DropStash(sha); err != nil {
				log.For("session").Warn("kill.stash_drop_failed", "err", err.Error())
			}
		}
		if err := gitWT.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup git worktree: %w", err))
		}
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./session/... -run TestInstance_KillDropsLeftoverStash -v`
Expected: PASS

- [ ] **Step 5: Run the full session suite**

Run: `go test ./session/... -v 2>&1 | tail -80`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add session/instance.go session/instance_lifecycle_test.go
git commit -m "fix(session): drop a Paused instance's leftover stash on Kill"
```

### Task 8: Full Phase 1 regression pass

**Files:** none (verification only)

- [ ] **Step 1: Run the whole repo test suite**

Run: `CGO_ENABLED=0 go test ./... 2>&1 | tail -80`
Expected: PASS across every package.

- [ ] **Step 2: Build**

Run: `CGO_ENABLED=0 go build -o /tmp/loom_phase1_build .`
Expected: builds cleanly.

- [ ] **Step 3: gofmt check**

Run: `gofmt -l session/ cmd/`
Expected: no output (nothing unformatted).

---

## Phase 2 — Rename "checkout" to "stash"

### Task 9: Rename the keybinding in keys/keys.go

**Files:**
- Modify: `keys/keys.go`

- [ ] **Step 1: Rename the constant and binding**

In `keys/keys.go`, change:

```go
	KeySubmitName
	KeyCheckout
	KeyMerge
```
to
```go
	KeySubmitName
	KeyStash
	KeyMerge
```

And change:

```go
	KeyCheckout: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "checkout"),
	),
```
to
```go
	KeyStash: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "stash"),
	),
```

- [ ] **Step 2: Verify it compiles (other packages reference KeyCheckout — expected to break; fixed in later tasks)**

Run: `go build ./keys/... 2>&1`
Expected: PASS (this package alone compiles; downstream breakage is expected until Tasks 10-12 land — do not run `go build ./...` yet).

- [ ] **Step 3: Commit**

```bash
git add keys/keys.go
git commit -m "refactor(keys): rename KeyCheckout to KeyStash, key 'c' to 's'"
```

### Task 10: Rename in app/help.go

**Files:**
- Modify: `app/help.go`

- [ ] **Step 1: Rename the type and entry list**

In `app/help.go`, change:

```go
type helpTypeInstanceCheckout struct{}
```
to
```go
type helpTypeInstanceStash struct{}
```

Change every `keys.KeyCheckout` reference (three occurrences: `generalHandoffEntries`, `instanceStartHandoffEntries`, `checkoutCommandEntries`) to `keys.KeyStash`.

Rename `checkoutCommandEntries` to `stashCommandEntries` (its declaration and its one use inside `helpTypeInstanceCheckout.toContent`).

Update the description strings:

```go
	generalHandoffEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeySubmit}, desc: "Commit and push branch to github"},
		{bindings: []keys.KeyName{keys.KeyStash}, desc: "Stash: stash changes and pause session"},
		{bindings: []keys.KeyName{keys.KeyResume}, desc: "Resume a paused session"},
	}
```

```go
	instanceStartHandoffEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeyStash}, desc: "Stash this instance's branch"},
		{bindings: []keys.KeyName{keys.KeySubmit}, desc: "Push branch to GitHub to create a PR"},
	}
```

```go
	stashCommandEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeyStash}, desc: "Stash: stash changes locally and pause session"},
		{bindings: []keys.KeyName{keys.KeyResume}, desc: "Resume a paused session"},
	}
```

- [ ] **Step 2: Rename the toContent/mask methods**

Change:

```go
func (h helpTypeInstanceCheckout) toContent() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Checkout Instance"),
		"",
		"Changes will be committed locally. The branch name has been copied to your clipboard for you to checkout.",
		"",
		"Feel free to make changes to the branch and commit them. When resuming, the session will continue from where you left off.",
		"",
		headerStyle.Render("Commands:"),
		renderHelpSection(checkoutCommandEntries, 2),
	)
}
```
to
```go
func (h helpTypeInstanceStash) toContent() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Stash Instance"),
		"",
		"Changes will be stashed locally. The branch name has been copied to your clipboard for you to checkout.",
		"",
		"Feel free to make changes to the branch and commit them. When resuming, the session will continue from where you left off.",
		"",
		headerStyle.Render("Commands:"),
		renderHelpSection(stashCommandEntries, 2),
	)
}
```

And:

```go
func (h helpTypeInstanceCheckout) mask() uint32 {
	return 1 << 3
}
```
to
```go
func (h helpTypeInstanceStash) mask() uint32 {
	return 1 << 3
}
```

- [ ] **Step 2: Commit**

```bash
git add app/help.go
git commit -m "refactor(app): rename helpTypeInstanceCheckout to helpTypeInstanceStash"
```

### Task 11: Rename in the script package (intent, api_actions, defaults.lua)

**Files:**
- Modify: `script/intent.go`
- Modify: `script/api_actions.go`
- Modify: `script/defaults.lua`

- [ ] **Step 1: Rename CheckoutIntent**

In `script/intent.go`, change:

```go
// CheckoutIntent asks the app to check the selected instance's branch
// out into the root repo. Confirm gates the operation; Help opens the
// explanatory overlay instead of performing the checkout.
type CheckoutIntent struct{ Confirm, Help bool }
```
to
```go
// StashIntent asks the app to stash the selected instance's
// uncommitted changes and pause it, checking its branch out into the
// root repo. Confirm gates the operation; Help opens the explanatory
// overlay instead of performing the stash.
type StashIntent struct{ Confirm, Help bool }
```

Change `func (CheckoutIntent) intent()           {}` to `func (StashIntent) intent()              {}`.

- [ ] **Step 2: Rename the Lua action**

In `script/api_actions.go`, change:

```go
	actions.RawSetString("checkout_selected", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, CheckoutIntent{
			Confirm: optBool(L, "confirm", true),
			Help:    optBool(L, "help", true),
		})
	}))
```
to
```go
	actions.RawSetString("stash_selected", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, StashIntent{
			Confirm: optBool(L, "confirm", true),
			Help:    optBool(L, "help", true),
		})
	}))
```

- [ ] **Step 3: Rename the default binding**

In `script/defaults.lua`, change:

```lua
cs.bind("c", function() cs.actions.checkout_selected{} end,         { help = "checkout" })
```
to
```lua
cs.bind("s", function() cs.actions.stash_selected{} end,            { help = "stash" })
```

- [ ] **Step 4: Commit**

```bash
git add script/intent.go script/api_actions.go script/defaults.lua
git commit -m "refactor(script): rename CheckoutIntent/checkout_selected to Stash"
```

### Task 12: Rename call sites in app/intents.go, app/app_scripts.go, ui/menu.go

**Files:**
- Modify: `app/intents.go`
- Modify: `app/app_scripts.go`
- Modify: `ui/menu.go`

- [ ] **Step 1: Rename the trigger function**

In `app/intents.go`, change:

```go
// runCheckoutSelectedOpts is the parameterized pause path. confirm
// gates the confirmation overlay; help gates the prerequisite help
// screen. Script callers use cs.actions.checkout_selected{confirm=,
// help=} to tune either. Combinations that skip the confirm still
// trigger the Loading transition synchronously so the spinner
// renders immediately.
func runCheckoutSelectedOpts(m *home, confirm, help bool) (tea.Model, tea.Cmd) {
```
to
```go
// runStashSelectedOpts is the parameterized pause path. confirm
// gates the confirmation overlay; help gates the prerequisite help
// screen. Script callers use cs.actions.stash_selected{confirm=,
// help=} to tune either. Combinations that skip the confirm still
// trigger the Loading transition synchronously so the spinner
// renders immediately.
func runStashSelectedOpts(m *home, confirm, help bool) (tea.Model, tea.Cmd) {
```

And inside that function, change `return m.showHelpScreen(helpTypeInstanceCheckout{}, startPause)` to `return m.showHelpScreen(helpTypeInstanceStash{}, startPause)`.

Update the doc comment on `selectedNotBusyNotWorkspace` (mentions "checkout"):

```go
// selectedNotBusyNotWorkspace gates lifecycle mutations (kill,
// submit, checkout): the selected instance must exist, must not be a
```
to
```go
// selectedNotBusyNotWorkspace gates lifecycle mutations (kill,
// submit, stash): the selected instance must exist, must not be a
```

- [ ] **Step 2: Rename the dispatch case**

In `app/app_scripts.go`, change:

```go
	case script.CheckoutIntent:
		if !selectedNotBusyNotWorkspace(m) {
			break
		}
		_, cmd = runCheckoutSelectedOpts(m, i.Confirm, i.Help)
```
to
```go
	case script.StashIntent:
		if !selectedNotBusyNotWorkspace(m) {
			break
		}
		_, cmd = runStashSelectedOpts(m, i.Confirm, i.Help)
```

- [ ] **Step 3: Rename in ui/menu.go**

In `ui/menu.go`, change:

```go
		if m.instance.GetStatus() == session.Paused {
			actionGroup = append(actionGroup, keys.KeyResume)
		} else {
			actionGroup = append(actionGroup, keys.KeyCheckout)
		}
```
to
```go
		if m.instance.GetStatus() == session.Paused {
			actionGroup = append(actionGroup, keys.KeyResume)
		} else {
			actionGroup = append(actionGroup, keys.KeyStash)
		}
```

And update the group-boundary comment:

```go
		{2, 4}, // Action group (submit, checkout/resume)
```
to
```go
		{2, 4}, // Action group (submit, stash/resume)
```

- [ ] **Step 4: Build the whole repo to confirm the rename is complete**

Run: `CGO_ENABLED=0 go build ./... 2>&1`
Expected: builds cleanly — no more references to `KeyCheckout`, `CheckoutIntent`, `runCheckoutSelectedOpts`, or `helpTypeInstanceCheckout` outside test files (fixed in Task 13).

- [ ] **Step 5: Commit**

```bash
git add app/intents.go app/app_scripts.go ui/menu.go
git commit -m "refactor(app,ui): rename checkout call sites to stash"
```

### Task 13: Update existing tests referencing the old names

**Files:**
- Modify: `app/migration_parity_test.go`
- Modify: `app/app_scripts_dispatch_test.go`
- Modify: `app/actions_test.go`
- Modify: `script/intent_test.go`
- Modify: `script/api_actions_test.go`

- [ ] **Step 1: app/migration_parity_test.go**

Change:
```go
		{"checkout_selected", "c", script.CheckoutIntent{Confirm: true, Help: true}},
```
to
```go
		{"stash_selected", "s", script.StashIntent{Confirm: true, Help: true}},
```

- [ ] **Step 2: app/app_scripts_dispatch_test.go**

Change the comment on `homeWithAppState` and `addReadyInstance` (both mention "checkout" in passing — update for accuracy):

```go
// homeWithAppState augments newTestHome with the appState dependency
// needed by intents that funnel through showHelpScreen (checkout,
// fullscreen_attach, show_help). Kept local to this file so other
// tests keep their minimal fixture.
```
to
```go
// homeWithAppState augments newTestHome with the appState dependency
// needed by intents that funnel through showHelpScreen (stash,
// fullscreen_attach, show_help). Kept local to this file so other
// tests keep their minimal fixture.
```

```go
// addReadyInstance attaches a Running instance so preconditions-gated
// intents (push/kill/checkout/attach/quick_input) have a valid
// selection. A mock TmuxSession is installed with a cmdExec that
```
to
```go
// addReadyInstance attaches a Running instance so preconditions-gated
// intents (push/kill/stash/attach/quick_input) have a valid
// selection. A mock TmuxSession is installed with a cmdExec that
```

Rename the test function:

```go
func TestHandleScriptIntentCheckout(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.CheckoutIntent{Confirm: true, Help: true},
	})
	// help=true opens the help screen first (unseen flag).
	assert.Equal(t, stateHelp, m.state)
}
```
to
```go
func TestHandleScriptIntentStash(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.StashIntent{Confirm: true, Help: true},
	})
	// help=true opens the help screen first (unseen flag).
	assert.Equal(t, stateHelp, m.state)
}
```

- [ ] **Step 3: app/actions_test.go**

Update the comment:
```go
// precondition that gates kill/submit/checkout. Loading/Deleting block;
```
to
```go
// precondition that gates kill/submit/stash. Loading/Deleting block;
```

- [ ] **Step 4: script/intent_test.go**

Change:
```go
var _ Intent = CheckoutIntent{Confirm: true, Help: true}
```
to
```go
var _ Intent = StashIntent{Confirm: true, Help: true}
```

- [ ] **Step 5: script/api_actions_test.go**

Rename both test functions and update their bodies:

```go
func TestCsActionsCheckoutSelectedDefaults(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("c", function() cs.actions.checkout_selected() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "c")
	intent := h.enqueued[0].(CheckoutIntent)
	assert.True(t, intent.Confirm)
	assert.True(t, intent.Help)
}

func TestCsActionsCheckoutSelectedAllowsOverrides(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("c", function() cs.actions.checkout_selected{confirm=false, help=false} end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "c")
	intent := h.enqueued[0].(CheckoutIntent)
	assert.False(t, intent.Confirm)
	assert.False(t, intent.Help)
}
```
to
```go
func TestCsActionsStashSelectedDefaults(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("s", function() cs.actions.stash_selected() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "s")
	intent := h.enqueued[0].(StashIntent)
	assert.True(t, intent.Confirm)
	assert.True(t, intent.Help)
}

func TestCsActionsStashSelectedAllowsOverrides(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("s", function() cs.actions.stash_selected{confirm=false, help=false} end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "s")
	intent := h.enqueued[0].(StashIntent)
	assert.False(t, intent.Confirm)
	assert.False(t, intent.Help)
}
```

- [ ] **Step 6: Run the full test suite**

Run: `CGO_ENABLED=0 go test ./... 2>&1 | tail -80`
Expected: PASS across every package.

- [ ] **Step 7: Commit**

```bash
git add app/migration_parity_test.go app/app_scripts_dispatch_test.go app/actions_test.go script/intent_test.go script/api_actions_test.go
git commit -m "test: rename checkout references to stash across the suite"
```

### Task 14: Update current-state docs (CLAUDE.md, USAGE.md, README.md)

**Files:**
- Modify: `CLAUDE.md`
- Modify: `USAGE.md`
- Modify: `README.md`

- [ ] **Step 1: CLAUDE.md**

Change the keybindings table row:
```
| `c` | Checkout branch |
```
to
```
| `s` | Stash & pause |
```

- [ ] **Step 2: USAGE.md**

Change:
```
8. Press `c` to checkout (pause) the session when done
```
to
```
8. Press `s` to stash (pause) the session when done
```

Change:
```
│  n new • N prompt • c checkout • r resume • p push • ? help • q     │
```
to
```
│  n new • N prompt • s stash • r resume • p push • ? help • q        │
```

Change:
```
**Running → Paused** (on checkout, `c`):
```
to
```
**Running → Paused** (on stash, `s`):
```

Change:
```
| `c` | Checkout — commit changes and pause session |
```
to
```
| `s` | Stash — stash changes and pause session |
```

- [ ] **Step 3: README.md**

Change:
```
- `c` - Checkout. Commits changes and pauses the session
```
to
```
- `s` - Stash. Stashes changes and pauses the session
```

Leave `- Review changes before applying them, checkout changes before pushing them` untouched — that "checkout" refers to `git checkout`, not this keybinding.

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md USAGE.md README.md
git commit -m "docs: update checkout references to stash in current-state docs"
```

### Task 15: Full Phase 2 regression pass

**Files:** none (verification only)

- [ ] **Step 1: Run the whole repo test suite**

Run: `CGO_ENABLED=0 go test ./... 2>&1 | tail -80`
Expected: PASS across every package.

- [ ] **Step 2: Grep for any remaining stray references**

Run: `grep -rn "KeyCheckout\|CheckoutIntent\|checkout_selected\|runCheckoutSelectedOpts\|helpTypeInstanceCheckout\|checkoutCommandEntries" --include="*.go" --include="*.lua" . | grep -v vendor`
Expected: no output.

- [ ] **Step 3: gofmt check**

Run: `gofmt -l app/ keys/ script/ ui/`
Expected: no output.

---

## Phase 3 — Add Effort as a launch option

### Task 16: Config layer — ClaudeEffort, ClaudeEfforts, Effort()

**Files:**
- Modify: `config/config.go`
- Test: `config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `config/config_test.go` (mirror whatever `TestModel`/`TestClaudeModels` look like nearby):

```go
func TestEffort(t *testing.T) {
	c := &Config{}
	assert.Equal(t, "default", c.Effort())

	low := "low"
	c.ClaudeEffort = &low
	assert.Equal(t, "low", c.Effort())
}

func TestClaudeEfforts(t *testing.T) {
	assert.Equal(t, []string{"default", "low", "medium", "high", "xhigh", "max"}, ClaudeEfforts)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./config/... -run 'TestEffort|TestClaudeEfforts' -v`
Expected: FAIL — `c.Effort undefined`, `ClaudeEfforts undefined`.

- [ ] **Step 3: Add the field, accessor, and list**

In `config/config.go`, add the field to `Config` (after `ClaudeModel`):

```go
	// ClaudeModel is the --model value new Claude sessions launch with.
	// Values are short CLI aliases (not versioned IDs) so the list
	// stays valid as new models ship without a code change. "default"
	// is a no-op — Claude's own default applies. Read it through Model.
	ClaudeModel *string `json:"claude_model,omitempty"`
	// ClaudeEffort is the --effort value new Claude sessions launch
	// with. "default" is a no-op — Claude's own default applies. Read
	// it through Effort.
	ClaudeEffort *string `json:"claude_effort,omitempty"`
```

Add `ClaudeEfforts` next to `ClaudeModels`:

```go
// ClaudeModels lists the --model aliases the Claude Preferences and
// Session Launch Options screens cycle through. Short aliases, not
// versioned IDs, so this list doesn't need updating when new Claude
// models ship.
var ClaudeModels = []string{"default", "sonnet", "opus", "haiku"}

// ClaudeEfforts lists the --effort values the Claude Preferences and
// Session Launch Options screens cycle through, matching what
// `claude --help` documents for --effort.
var ClaudeEfforts = []string{"default", "low", "medium", "high", "xhigh", "max"}
```

Add the accessor after `Model()`:

```go
// Effort returns the configured --effort value, defaulting to
// "default" when unset. Unlocked for the same reason as
// PermissionMode/Model.
func (c *Config) Effort() string {
	if c.ClaudeEffort == nil {
		return "default"
	}
	return *c.ClaudeEffort
}
```

Add to `DefaultConfig()`:

```go
	return &Config{
		DefaultProgram:     program,
		DaemonPollInterval: 1000,
		BranchPrefix: func() string {
			user, err := user.Current()
			if err != nil || user == nil || user.Username == "" {
				log.For("config").Error("get_current_user_failed", "err", err)
				return "session/"
			}
			return fmt.Sprintf("%s/", strings.ToLower(user.Username))
		}(),
		ClaudeRemoteControl:  boolPtr(true),
		ClaudePermissionMode: stringPtr("default"),
		HeadroomWrap:         boolPtr(false),
		ClaudeModel:          stringPtr("default"),
		ClaudeEffort:         stringPtr("default"),
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./config/... -run 'TestEffort|TestClaudeEfforts' -v`
Expected: PASS

- [ ] **Step 5: Run the full config suite**

Run: `go test ./config/... -v 2>&1 | tail -40`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): add ClaudeEffort/ClaudeEfforts/Effort()"
```

### Task 17: Adapter layer — ApplyEffortFlag

**Files:**
- Modify: `session/agent/adapter.go`
- Modify: `session/agent/claude.go`
- Modify: `session/agent/aider.go`
- Modify: `session/agent/gemini.go`
- Modify: `session/agent/default.go`
- Test: `session/agent/adapter_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `session/agent/adapter_test.go`:

```go
func TestClaudeEffortFlag(t *testing.T) {
	c := Claude()
	assert.Equal(t, "claude --effort high", c.ApplyEffortFlag("claude", "high"))
	assert.Equal(t, "claude", c.ApplyEffortFlag("claude", ""))
	assert.Equal(t, "claude", c.ApplyEffortFlag("claude", "default"))
	assert.Equal(t, "claude --effort high", c.ApplyEffortFlag("claude --effort high", "max"), "idempotent: existing flag wins")
	assert.Equal(t, "claude --effort low --model opus", c.ApplyEffortFlag("claude --model opus", "low"))
}

func TestNonClaudeAdaptersNoEffortFlag(t *testing.T) {
	for _, a := range []Adapter{Aider(), Gemini(), Default()} {
		assert.Equal(t, "aider --model gemma", a.ApplyEffortFlag("aider --model gemma", "high"), a.Name())
	}
}
```

(Adjust the `TestNonClaudeAdaptersNoEffortFlag` program string per adapter if the existing `TestNonClaudeAdaptersNoRecovery`-style tests in this file use per-adapter program strings — check that test for the exact pattern before finalizing; mirror its shape.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./session/agent/... -run 'TestClaudeEffortFlag|TestNonClaudeAdaptersNoEffortFlag' -v`
Expected: FAIL — `ApplyEffortFlag` doesn't exist on the `Adapter` interface yet, so this won't even compile.

- [ ] **Step 3: Add ApplyEffortFlag to the Adapter interface**

In `session/agent/adapter.go`, add to the `Adapter` interface after `ApplyModelFlag`:

```go
	// ApplyModelFlag returns the program string with "--model <model>"
	// inserted (e.g. "claude --model opus"). model == "" or "default"
	// is a no-op — Claude's own default already matches. Idempotent: if
	// --model is already present, the input is returned unchanged.
	// Returns the input unchanged for agents without a model-selection
	// concept.
	ApplyModelFlag(program, model string) string
	// ApplyEffortFlag returns the program string with "--effort
	// <level>" inserted (e.g. "claude --effort high"). effort == "" or
	// "default" is a no-op — Claude's own default already matches.
	// Idempotent: if --effort is already present, the input is
	// returned unchanged. Returns the input unchanged for agents
	// without an effort-level concept.
	ApplyEffortFlag(program, effort string) string
}
```

- [ ] **Step 4: Implement it in claude.go**

In `session/agent/claude.go`, add after `ApplyModelFlag`:

```go
// ApplyModelFlag inserts "--model <model>" after "claude". model == ""
// or "default" is a no-op — Claude's own default already matches.
// Returns program unchanged if a --model flag is already present or if
// program is empty. model is expected to come from config.ClaudeModels,
// never free-typed user input, so no sanitization is applied.
func (claudeAdapter) ApplyModelFlag(program, model string) string {
	if model == "" || model == "default" {
		return program
	}
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}
	for _, p := range parts[1:] {
		if p == "--model" || strings.HasPrefix(p, "--model=") {
			return program
		}
	}
	return parts[0] + " --model " + model + strings.TrimPrefix(program, parts[0])
}

// ApplyEffortFlag inserts "--effort <level>" after "claude". effort ==
// "" or "default" is a no-op — Claude's own default already matches.
// Returns program unchanged if a --effort flag is already present or
// if program is empty. effort is expected to come from
// config.ClaudeEfforts, never free-typed user input, so no
// sanitization is applied.
func (claudeAdapter) ApplyEffortFlag(program, effort string) string {
	if effort == "" || effort == "default" {
		return program
	}
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}
	for _, p := range parts[1:] {
		if p == "--effort" || strings.HasPrefix(p, "--effort=") {
			return program
		}
	}
	return parts[0] + " --effort " + effort + strings.TrimPrefix(program, parts[0])
}
```

- [ ] **Step 5: Add no-op implementations to aider.go, gemini.go, default.go**

In `session/agent/aider.go`, add after `ApplyModelFlag`:

```go
// ApplyEffortFlag is a no-op for aider — effort levels aren't exposed
// through this settings screen for non-Claude agents.
func (aiderAdapter) ApplyEffortFlag(program, _ string) string {
	return program
}
```

In `session/agent/gemini.go`, add after `ApplyModelFlag`:

```go
// ApplyEffortFlag is a no-op for gemini — effort levels aren't exposed
// through this settings screen for non-Claude agents.
func (geminiAdapter) ApplyEffortFlag(program, _ string) string {
	return program
}
```

In `session/agent/default.go`, add after `ApplyModelFlag`:

```go
// ApplyEffortFlag implements Adapter. The fallback adapter never
// modifies the program string, so unknown agents get no effort flag.
func (defaultAdapter) ApplyEffortFlag(program, _ string) string {
	return program
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./session/agent/... -v 2>&1 | tail -60`
Expected: PASS — including the new tests and every pre-existing adapter test (interface satisfied by all four adapters).

- [ ] **Step 7: Commit**

```bash
git add session/agent/adapter.go session/agent/claude.go session/agent/aider.go session/agent/gemini.go session/agent/default.go session/agent/adapter_test.go
git commit -m "feat(agent): add ApplyEffortFlag to the Adapter interface"
```

### Task 18: BuildEffortCommand wrapper

**Files:**
- Modify: `session/agent_restart.go`
- Test: `session/agent_restart_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `session/agent_restart_test.go`:

```go
func TestBuildEffortCommand_Claude(t *testing.T) {
	assert.Equal(t, "claude --effort high", BuildEffortCommand("claude", "high"))
}

func TestBuildEffortCommand_Default(t *testing.T) {
	assert.Equal(t, "claude", BuildEffortCommand("claude", "default"))
}

func TestBuildEffortCommand_Unknown(t *testing.T) {
	assert.Equal(t, "codex", BuildEffortCommand("codex", "high"))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./session/... -run TestBuildEffortCommand -v`
Expected: FAIL — `BuildEffortCommand undefined`.

- [ ] **Step 3: Implement it**

In `session/agent_restart.go`, add after `BuildModelCommand`:

```go
// BuildEffortCommand modifies a program command string to launch with
// the given --effort value. The adapter registry decides whether and
// how the string is modified. Idempotent, and a no-op for agents
// without an effort-level concept or when effort is "" / "default".
func BuildEffortCommand(program, effort string) string {
	return defaultRegistry.Lookup(program).ApplyEffortFlag(program, effort)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./session/... -run TestBuildEffortCommand -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add session/agent_restart.go session/agent_restart_test.go
git commit -m "feat(session): add BuildEffortCommand"
```

### Task 19: Add Effort to overlay.LaunchOptions and the Session Launch Options modal

**Files:**
- Modify: `ui/overlay/sessionLaunchOptions.go`
- Test: `ui/overlay/sessionLaunchOptions_test.go`

- [ ] **Step 1: Write the failing test**

Check the existing test file first: `grep -n "^func Test" ui/overlay/sessionLaunchOptions_test.go`. Add a new test alongside the existing ones:

```go
func TestSessionLaunchOptions_EffortRowCycles(t *testing.T) {
	l := NewSessionLaunchOptions(LaunchOptions{}, false, "")
	// Move to row 4 (Effort): down x4 from row 0.
	for i := 0; i < 4; i++ {
		l.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	l.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "low", l.Options().Effort)
	l.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "medium", l.Options().Effort)
}
```

(Check the existing test file's imports/helpers for the exact `tea.KeyPressMsg` construction style used elsewhere in this file and match it.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./ui/overlay/... -run TestSessionLaunchOptions_EffortRowCycles -v`
Expected: FAIL — cursor never reaches row 4 (`sessionLaunchOptionsRowCount` is still 4, so `down` clamps at row 3), and `Options().Effort` doesn't exist.

- [ ] **Step 3: Add Effort to LaunchOptions and wire the fifth row**

In `ui/overlay/sessionLaunchOptions.go`, change:

```go
// LaunchOptions holds the four per-session launch toggles. Defined here
// (rather than in app) so it's usable both by SessionLaunchOptions
// (ephemeral, edited as a plain value) and by app's launch-command
// composition, without an import cycle back to app.
type LaunchOptions struct {
	RemoteControl  bool
	PermissionMode string
	Model          string
	HeadroomWrap   bool
}
```
to
```go
// LaunchOptions holds the five per-session launch toggles. Defined
// here (rather than in app) so it's usable both by
// SessionLaunchOptions (ephemeral, edited as a plain value) and by
// app's launch-command composition, without an import cycle back to
// app.
type LaunchOptions struct {
	RemoteControl  bool
	PermissionMode string
	Model          string
	HeadroomWrap   bool
	Effort         string
}
```

Change:
```go
// sessionLaunchOptionsRowCount is the number of navigable rows: Remote
// Control, Permission Mode, Model, and Headroom Wrap.
const sessionLaunchOptionsRowCount = 4
```
to
```go
// sessionLaunchOptionsRowCount is the number of navigable rows: Remote
// Control, Permission Mode, Model, Headroom Wrap, and Effort.
const sessionLaunchOptionsRowCount = 5
```

Add a case to `toggleCursor`:

```go
func (l *SessionLaunchOptions) toggleCursor() {
	switch l.cursor {
	case 0:
		l.opts.RemoteControl = !l.opts.RemoteControl
		if l.opts.RemoteControl {
			l.opts.HeadroomWrap = false
		}
	case 1:
		l.opts.PermissionMode = nextInList(config.ClaudePermissionModes, l.opts.PermissionMode)
	case 2:
		l.opts.Model = nextInList(config.ClaudeModels, l.opts.Model)
	case 3:
		l.opts.HeadroomWrap = !l.opts.HeadroomWrap
		if l.opts.HeadroomWrap {
			l.opts.RemoteControl = false
		}
	case 4:
		l.opts.Effort = nextInList(config.ClaudeEfforts, l.opts.Effort)
	}
}
```

Add the row to `Render`:

```go
	content := sessionLaunchOptionsTitleStyle.Render("Session Launch Options") + "\n\n" +
		row(0, "Remote Control    ", rcCheck) + "\n" +
		row(1, "Permission Mode   ", "< "+l.opts.PermissionMode+" >") + "\n" +
		row(2, "Model             ", "< "+l.opts.Model+" >") + "\n" +
		row(3, "Headroom Wrap     ", hwCheck) + "\n" +
		row(4, "Effort            ", "< "+l.opts.Effort+" >") + "\n\n" +
		sessionLaunchOptionsHintStyle.Render("up/down move • space toggle/cycle • enter start • esc cancel")
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./ui/overlay/... -run TestSessionLaunchOptions_EffortRowCycles -v`
Expected: PASS

- [ ] **Step 5: Run the full overlay suite for regressions**

Run: `go test ./ui/overlay/... -v 2>&1 | tail -60`
Expected: PASS — existing row-navigation tests still pass (they only exercised rows 0-3, unaffected by the new row 4).

- [ ] **Step 6: Commit**

```bash
git add ui/overlay/sessionLaunchOptions.go ui/overlay/sessionLaunchOptions_test.go
git commit -m "feat(ui): add Effort as a fifth Session Launch Options row"
```

### Task 20: Add Effort to Claude Preferences

**Files:**
- Modify: `ui/overlay/claudePreferences.go`
- Test: `ui/overlay/claudePreferences_test.go`

- [ ] **Step 1: Write the failing test**

Check the existing test file first: `grep -n "^func Test" ui/overlay/claudePreferences_test.go`. Add:

```go
func TestClaudePreferences_EffortRowCycles(t *testing.T) {
	cfg := &config.Config{}
	c := NewClaudePreferences(cfg, false, "")
	for i := 0; i < 4; i++ {
		c.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	c.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "low", cfg.Effort())
}
```

(Match the existing file's exact construction pattern for `config.Config` and `tea.KeyPressMsg` — check `TestClaudePreferences` or similar nearby tests before finalizing.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./ui/overlay/... -run TestClaudePreferences_EffortRowCycles -v`
Expected: FAIL — cursor clamps at row 3 (`claudePrefsRowCount` still 4), no case 4 in the switch.

- [ ] **Step 3: Add the fifth row**

In `ui/overlay/claudePreferences.go`, change:

```go
// ClaudePreferences is the Claude-specific preferences drill-in
// sub-screen. Structured as its own screen (rather than flat rows on
// the main settings list) so more Claude-adapter-specific preferences
// can be added later without growing that list — today it holds four
// rows: Remote Control, Permission Mode, Model, and Headroom Wrap.
```
to
```go
// ClaudePreferences is the Claude-specific preferences drill-in
// sub-screen. Structured as its own screen (rather than flat rows on
// the main settings list) so more Claude-adapter-specific preferences
// can be added later without growing that list — today it holds five
// rows: Remote Control, Permission Mode, Model, Headroom Wrap, and
// Effort.
```

Change:
```go
// claudePrefsRowCount is the number of navigable rows: Remote Control,
// Permission Mode, Model, and Headroom Wrap.
const claudePrefsRowCount = 4
```
to
```go
// claudePrefsRowCount is the number of navigable rows: Remote Control,
// Permission Mode, Model, Headroom Wrap, and Effort.
const claudePrefsRowCount = 5
```

Add a case to `HandleKeyPress`'s switch:

```go
		case 3:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.HeadroomWrapEnabled()
				cc.HeadroomWrap = &v
				if v {
					rc := false
					cc.ClaudeRemoteControl = &rc
				}
			})
		case 4:
			c.cfg.Mutate(func(cc *config.Config) {
				next := nextInList(config.ClaudeEfforts, cc.Effort())
				cc.ClaudeEffort = &next
			})
		}
```

Add the render row after `hwRow` in `Render`:

```go
	effortCursor := "  "
	if c.cursor == 4 {
		effortCursor = "> "
	}
	effortRow := effortCursor + "Effort            < " + c.cfg.Effort() + " >"
	if c.cursor == 4 {
		effortRow = claudePrefsSelectedStyle.Render(effortRow)
	} else {
		effortRow = claudePrefsRowStyle.Render(effortRow)
	}

	content := claudePrefsTitleStyle.Render("Claude Preferences") + "\n\n" +
		rcRow + "\n" +
		pmRow + "\n" +
		modelRow + "\n" +
		hwRow + "\n" +
		effortRow + "\n\n" +
		claudePrefsHintStyle.Render("up/down move • enter/space toggle/cycle • esc back")
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./ui/overlay/... -run TestClaudePreferences_EffortRowCycles -v`
Expected: PASS

- [ ] **Step 5: Run the full overlay suite**

Run: `go test ./ui/overlay/... -v 2>&1 | tail -60`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add ui/overlay/claudePreferences.go ui/overlay/claudePreferences_test.go
git commit -m "feat(ui): add Effort as a fifth Claude Preferences row"
```

### Task 21: Compose Effort into applyLaunchOptions

**Files:**
- Modify: `app/remote_control.go`
- Test: `app/remote_control_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `app/remote_control_test.go`:

```go
func TestEffortProgram(t *testing.T) {
	assert.Equal(t, "claude --effort high", effortProgram("high", "claude"))
	assert.Equal(t, "claude", effortProgram("default", "claude"))
	assert.Equal(t, "claude", effortProgram("", "claude"))
}

func TestApplyLaunchOptions_ComposesEffort(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	opts := overlay.LaunchOptions{Model: "opus", Effort: "high"}
	got := applyLaunchOptions(opts, authOK, "claude", "t")
	assert.Contains(t, got, "--effort high")
	assert.Contains(t, got, "--model opus")
}
```

(Match the existing `TestApplyLaunchOptions` test's exact structure/table shape in this file before finalizing — mirror its style rather than introducing a new pattern.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./app/... -run 'TestEffortProgram|TestApplyLaunchOptions_ComposesEffort' -v`
Expected: FAIL — `effortProgram` doesn't exist, and `applyLaunchOptions`'s output doesn't contain `--effort` since it isn't composed yet.

- [ ] **Step 3: Add effortProgram and wire it into launchOptionsFromConfig / applyLaunchOptions**

In `app/remote_control.go`, add after `modelProgram`:

```go
// modelProgram returns program with Claude's --model flag applied.
// No-op when the program isn't Claude or model is "" / "default".
func modelProgram(model, program string) string {
	return session.BuildModelCommand(program, model)
}

// effortProgram returns program with Claude's --effort flag applied.
// No-op when the program isn't Claude or effort is "" / "default".
func effortProgram(effort, program string) string {
	return session.BuildEffortCommand(program, effort)
}
```

Update `launchOptionsFromConfig`:

```go
func launchOptionsFromConfig(cfg *config.Config) overlay.LaunchOptions {
	if cfg == nil {
		return overlay.LaunchOptions{}
	}
	return overlay.LaunchOptions{
		RemoteControl:  cfg.RemoteControlEnabled(),
		PermissionMode: cfg.PermissionMode(),
		Model:          cfg.Model(),
		HeadroomWrap:   cfg.HeadroomWrapEnabled(),
		Effort:         cfg.Effort(),
	}
}
```

Update `applyLaunchOptions`:

```go
// applyLaunchOptions composes program in order: remote-control,
// permission-mode, model, effort, then headroom-wrap last —
// headroom-wrap must be outermost so the earlier steps still see the
// bare agent name at parts[0] when deciding how to modify the string.
// effectiveRemoteControl is the authoritative enforcement of the
// Remote-Control/Headroom-Wrap exclusivity rule (the UI-level
// auto-disable in ClaudePreferences/SessionLaunchOptions is the
// good-UX layer on top, not the only guarantee).
func applyLaunchOptions(opts overlay.LaunchOptions, auth session.RemoteControlAuth, program, title string) string {
	program = remoteControlProgram(effectiveRemoteControl(opts), auth, program, title)
	program = permissionModeProgram(opts.PermissionMode, program)
	program = modelProgram(opts.Model, program)
	program = effortProgram(opts.Effort, program)
	program = headroomWrapProgram(opts.HeadroomWrap, program)
	return program
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./app/... -run 'TestEffortProgram|TestApplyLaunchOptions_ComposesEffort' -v`
Expected: PASS

- [ ] **Step 5: Run the full app suite for regressions**

Run: `go test ./app/... -v 2>&1 | tail -100`
Expected: PASS — existing `TestApplyLaunchOptions*` tests still pass (Effort defaults to "" in table entries that don't set it, which is a no-op).

- [ ] **Step 6: Commit**

```bash
git add app/remote_control.go app/remote_control_test.go
git commit -m "feat(app): compose Effort into applyLaunchOptions"
```

### Task 22: Full Phase 3 regression pass

**Files:** none (verification only)

- [ ] **Step 1: Run the whole repo test suite**

Run: `CGO_ENABLED=0 go test ./... 2>&1 | tail -80`
Expected: PASS across every package.

- [ ] **Step 2: Manual smoke check of the two modals**

Run: `CGO_ENABLED=0 go build -o /tmp/loom_phase3_build . && /tmp/loom_phase3_build`

In the running TUI: open Settings (`S`) → Claude Preferences, confirm the fifth "Effort" row appears and cycles through default/low/medium/high/xhigh/max with space. Press `n` to start a new instance, confirm the Session Launch Options modal also shows the fifth Effort row. Quit with `q`.

---

## Phase 4 — Restart with different launch options

### Task 23: ParseLaunchOptions (reverse of applyLaunchOptions)

**Important package-placement note:** `ParseLaunchOptions` returns an
`overlay.LaunchOptions`, which rules out putting it in `session`
alongside the other `Build*Command` helpers — `ui/overlay` imports
`ui` (for theme colors), and `ui` (`ui/menu.go`, `ui/list.go`, etc.)
imports `session`, so `session → ui/overlay → ui → session` would be
an import cycle. `applyLaunchOptions` (the encode direction this
decodes) already lives in `app/remote_control.go` for the same reason
— its symmetric decode counterpart belongs right next to it. It only
needs `agent.SplitHeadroomWrap` (a pure string function in the leaf
package `session/agent`, which nothing else here imports transitively)
for the headroom-wrap-prefix step — no adapter registry lookup is
needed for parsing, since it just scans literal flag tokens.

**Files:**
- Modify: `app/remote_control.go`
- Test: `app/remote_control_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `app/remote_control_test.go`:

```go
func TestParseLaunchOptions_RoundTrip(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	cases := []struct {
		name string
		opts overlay.LaunchOptions
	}{
		{"all default", overlay.LaunchOptions{PermissionMode: "default", Model: "default", Effort: "default"}},
		{"remote control on", overlay.LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default", Effort: "default"}},
		{"permission mode", overlay.LaunchOptions{PermissionMode: "acceptEdits", Model: "default", Effort: "default"}},
		{"model", overlay.LaunchOptions{PermissionMode: "default", Model: "opus", Effort: "default"}},
		{"effort", overlay.LaunchOptions{PermissionMode: "default", Model: "default", Effort: "high"}},
		{"headroom wrap", overlay.LaunchOptions{PermissionMode: "default", Model: "default", Effort: "default", HeadroomWrap: true}},
		{"all on (RC forced off by exclusivity)", overlay.LaunchOptions{RemoteControl: true, PermissionMode: "acceptEdits", Model: "opus", Effort: "high", HeadroomWrap: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			composed := applyLaunchOptions(tc.opts, authOK, "claude", "my-title")
			gotOpts, gotBase := ParseLaunchOptions(composed)
			assert.Equal(t, "claude", gotBase)
			// effectiveRemoteControl forces RemoteControl off when
			// HeadroomWrap is on, so the round-trip must reflect the
			// composed reality, not the original (possibly
			// self-contradictory) input.
			wantRC := tc.opts.RemoteControl && !tc.opts.HeadroomWrap
			assert.Equal(t, wantRC, gotOpts.RemoteControl)
			assert.Equal(t, tc.opts.PermissionMode, gotOpts.PermissionMode)
			assert.Equal(t, tc.opts.Model, gotOpts.Model)
			assert.Equal(t, tc.opts.Effort, gotOpts.Effort)
			assert.Equal(t, tc.opts.HeadroomWrap, gotOpts.HeadroomWrap)
		})
	}
}

func TestParseLaunchOptions_AbsolutePathBaseProgram(t *testing.T) {
	opts, base := ParseLaunchOptions("headroom wrap claude --model sonnet --permission-mode auto")
	assert.Equal(t, "claude", base)
	assert.Equal(t, "sonnet", opts.Model)
	assert.Equal(t, "auto", opts.PermissionMode)
	assert.True(t, opts.HeadroomWrap)
}

func TestParseLaunchOptions_UnrecognizedFlagLeftInBase(t *testing.T) {
	opts, base := ParseLaunchOptions("claude --some-other-flag value --model opus")
	assert.Equal(t, "opus", opts.Model)
	assert.Contains(t, base, "--some-other-flag value")
	assert.NotContains(t, base, "--model")
}

func TestParseLaunchOptions_EmptyProgram(t *testing.T) {
	opts, base := ParseLaunchOptions("")
	assert.Equal(t, overlay.LaunchOptions{}, opts)
	assert.Equal(t, "", base)
}
```

(`overlay`, `session`, and testify are already imported by this file per the existing `TestApplyLaunchOptions`/`TestEffortProgram` tests from earlier tasks — no new imports needed here.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./app/... -run TestParseLaunchOptions -v`
Expected: FAIL — `ParseLaunchOptions undefined`.

- [ ] **Step 3: Implement ParseLaunchOptions**

In `app/remote_control.go`, add `"github.com/aidan-bailey/loom/session/agent"` and `"strings"` to the imports, then add after `applyLaunchOptions`:

```go
// ParseLaunchOptions decodes a composed Program string back into the
// overlay.LaunchOptions that produced it, plus the underlying bare
// program (binary path/name and any *other* flags) applyLaunchOptions
// would need to recompose it from scratch. It is the symmetric decode
// of applyLaunchOptions: strips the "headroom wrap " prefix, then
// scans tokens for --remote-control[=name], --permission-mode <mode>,
// --model <model>, and --effort <level>, removing each recognized flag
// (and its value token, where applicable) from the returned base
// program. Recomposing must start from a bare program —
// applyLaunchOptions's ApplyXFlag functions insert "right after
// parts[0]", so calling them again on an already-flagged string would
// insert --model after "headroom", or duplicate an existing
// --permission-mode. A token this doesn't recognize (e.g. a hand-added
// flag) is left in place in baseProgram and simply doesn't set the
// corresponding opts field — never an error.
func ParseLaunchOptions(program string) (opts overlay.LaunchOptions, baseProgram string) {
	prefix, rest := agent.SplitHeadroomWrap(program)
	opts.HeadroomWrap = prefix != ""

	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return opts, ""
	}

	kept := []string{parts[0]}
	for i := 1; i < len(parts); i++ {
		switch {
		case parts[i] == "--remote-control":
			opts.RemoteControl = true
			// May be followed by a session-name value token, or may
			// stand alone (Claude auto-generates a name). Only
			// consume the next token if it doesn't look like another
			// flag.
			if i+1 < len(parts) && !strings.HasPrefix(parts[i+1], "--") {
				i++
			}
		case strings.HasPrefix(parts[i], "--remote-control="):
			opts.RemoteControl = true
		case parts[i] == "--permission-mode" && i+1 < len(parts):
			opts.PermissionMode = parts[i+1]
			i++
		case parts[i] == "--model" && i+1 < len(parts):
			opts.Model = parts[i+1]
			i++
		case parts[i] == "--effort" && i+1 < len(parts):
			opts.Effort = parts[i+1]
			i++
		default:
			kept = append(kept, parts[i])
		}
	}
	return opts, strings.Join(kept, " ")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./app/... -run TestParseLaunchOptions -v`
Expected: PASS

- [ ] **Step 5: Run the full app suite for regressions**

Run: `go test ./app/... -v 2>&1 | tail -100`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add app/remote_control.go app/remote_control_test.go
git commit -m "feat(app): add ParseLaunchOptions, the reverse of applyLaunchOptions"
```

### Task 24: KeyRestartWithOptions — key, intent, dispatch

**Files:**
- Modify: `keys/keys.go`
- Modify: `script/intent.go`
- Modify: `script/api_actions.go`
- Modify: `script/defaults.lua`
- Modify: `app/app_scripts.go`
- Test: `app/migration_parity_test.go`, `script/intent_test.go`, `script/api_actions_test.go`

- [ ] **Step 1: Add the KeyName and binding**

In `keys/keys.go`, add to the const block after `KeyResume`:

```go
	KeyStash
	KeyMerge
	KeyResume
	KeyRestartWithOptions
	KeyPrompt
```

Add the binding after `KeyResume`'s entry:

```go
	KeyResume: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "resume"),
	),
	KeyRestartWithOptions: key.NewBinding(
		key.WithKeys("R"),
		key.WithHelp("R", "resume with options"),
	),
```

- [ ] **Step 2: Add the Intent**

In `script/intent.go`, add after `ResumeIntent`:

```go
// ResumeIntent asks the app to resume the selected paused instance.
type ResumeIntent struct{}

// RestartWithOptionsIntent asks the app to open the Session Launch
// Options modal for the selected Paused instance, seeded from its
// current (reverse-parsed) launch options, so the user can change them
// before resuming.
type RestartWithOptionsIntent struct{}
```

Add `func (RestartWithOptionsIntent) intent()    {}` next to `func (ResumeIntent) intent()              {}`.

- [ ] **Step 3: Add the Lua action**

In `script/api_actions.go`, add after `resume_selected`:

```go
	actions.RawSetString("resume_selected", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, ResumeIntent{})
	}))

	actions.RawSetString("restart_with_options_selected", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, RestartWithOptionsIntent{})
	}))
```

- [ ] **Step 4: Add the default binding**

In `script/defaults.lua`, add after `resume_selected`:

```lua
cs.bind("r", function() cs.actions.resume_selected() end,           { help = "resume" })
cs.bind("R", function() cs.actions.restart_with_options_selected() end, { help = "resume with options" })
```

- [ ] **Step 5: Write the failing gate test**

Add a standalone test to `app/app_scripts_dispatch_test.go`. This task only wires the gate + a stub handler (the real handler lands in Task 26), so only the gate's reject path is testable here — `selectedPausedNotWorkspace` returning false for a non-Paused instance:

```go
func TestHandleScriptIntentRestartWithOptions_NotPausedIsNoOp(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m) // Running, not Paused

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.RestartWithOptionsIntent{},
	})
	assert.Equal(t, stateDefault, m.state)
}
```

(The Paused-case test — confirming the gate *admits* a Paused instance and the modal actually opens — belongs in Task 26 alongside the real handler; testing it here against the stub would require deliberately committing a known-failing test, which this plan avoids.)

- [ ] **Step 6: Run the test to verify it fails**

Run: `go test ./app/... -run TestHandleScriptIntentRestartWithOptions_NotPausedIsNoOp -v`
Expected: FAIL — `script.RestartWithOptionsIntent` and `m.handleScriptIntent`'s case for it don't exist yet, so this doesn't compile.

- [ ] **Step 7: Add the dispatch case (stub gate only — real handler comes in Task 26)**

In `app/app_scripts.go`, add after the `script.ResumeIntent` case:

```go
	case script.ResumeIntent:
		if !selectedResumableNotWorkspace(m) {
			break
		}
		_, cmd = runResumeOrRecover(m)
	case script.RestartWithOptionsIntent:
		if !selectedPausedNotWorkspace(m) {
			break
		}
		_, cmd = runRestartWithOptionsSelected(m)
```

This references `selectedPausedNotWorkspace` and `runRestartWithOptionsSelected`, which don't exist yet — add minimal stubs in `app/intents.go` for now so the package compiles (Task 26 replaces the stub body with the real implementation):

```go
// selectedPausedNotWorkspace gates the 'R' key (restart with
// different launch options): only a Paused instance qualifies —
// unlike selectedResumableNotWorkspace, Recoverable orphans are
// excluded (they go live only via the explicit recover action, a
// different code path than Resume).
func selectedPausedNotWorkspace(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.IsWorkspaceTerminal {
		return false
	}
	return selected.GetStatus() == session.Paused
}

// runRestartWithOptionsSelected is implemented in Task 26 of the
// restart-launch-options plan; this stub only exists so the dispatch
// wiring compiles in the interim.
func runRestartWithOptionsSelected(m *home) (tea.Model, tea.Cmd) {
	return m, nil
}
```

- [ ] **Step 8: Run the test to verify it passes**

Run: `go test ./app/... -run TestHandleScriptIntentRestartWithOptions_NotPausedIsNoOp -v`
Expected: PASS.

- [ ] **Step 9: Update script/intent_test.go and api_actions_test.go for the new intent**

In `script/intent_test.go`, add near the other `var _ Intent = ...` lines:

```go
var _ Intent = RestartWithOptionsIntent{}
```

In `script/api_actions_test.go`, add:

```go
func TestCsActionsRestartWithOptionsSelected(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("R", function() cs.actions.restart_with_options_selected() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "R")
	_ = h.enqueued[0].(RestartWithOptionsIntent)
}
```

- [ ] **Step 10: Run the script package tests**

Run: `go test ./script/... -v 2>&1 | tail -60`
Expected: PASS.

- [ ] **Step 11: Build the whole repo**

Run: `CGO_ENABLED=0 go build ./... 2>&1`
Expected: builds cleanly — the stub satisfies the dispatch wiring so nothing downstream breaks.

- [ ] **Step 12: Commit**

```bash
git add keys/keys.go script/intent.go script/api_actions.go script/defaults.lua app/app_scripts.go app/intents.go app/app_scripts_dispatch_test.go script/intent_test.go script/api_actions_test.go
git commit -m "feat(app,script): wire the R keybinding to a stub RestartWithOptionsIntent handler"
```

### Task 25: pendingLaunchOptionsCancel — generalize the cancel path

**Files:**
- Modify: `app/app.go`
- Modify: `app/state_launch_options.go`
- Modify: `app/state_new.go`
- Modify: `app/state_prompt.go`
- Test: `app/state_launch_options_test.go` (check for existing tests first)

- [ ] **Step 1: Write the failing test**

Check first: `grep -n "^func Test" app/state_launch_options_test.go 2>/dev/null || echo "file does not exist"`. Add (or create the file with) this test:

```go
package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestCancelLaunchOptions_UsesStashedCancelClosure(t *testing.T) {
	m := newTestHome(t)
	called := false
	m.pendingLaunchOptionsCancel = func() (tea.Model, tea.Cmd) {
		called = true
		return m, nil
	}
	m.pendingLaunchOptions = func(overlayLaunchOpts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		t.Fatal("must not run the confirm closure on cancel")
		return m, nil
	}

	m.cancelLaunchOptions()

	assert.True(t, called, "cancelLaunchOptions must run the stashed pendingLaunchOptionsCancel closure")
	assert.Nil(t, m.pendingLaunchOptions)
	assert.Nil(t, m.pendingLaunchOptionsCancel)
}
```

Add `"github.com/aidan-bailey/loom/ui/overlay"` to imports. (Check `newTestHome`'s signature in an existing test file first — used throughout the app test suite already, e.g. `app/state_new_test.go`.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./app/... -run TestCancelLaunchOptions_UsesStashedCancelClosure -v`
Expected: FAIL — `m.pendingLaunchOptionsCancel` doesn't exist on `home` yet.

- [ ] **Step 3: Add the field**

In `app/app.go`, add after `pendingLaunchOptions`:

```go
	// pendingLaunchOptions holds the compose-and-start closure for a
	// not-yet-started instance while stateLaunchOptions is active.
	// state_new.go/state_prompt.go stash it (capturing the instance and
	// any prompt-flow-specific data like selectedBranch) right before
	// opening the Session Launch Options modal; handleStateLaunchOptionsKey
	// invokes it with the user's chosen overlay.LaunchOptions on confirm,
	// then clears it. nil outside that window.
	pendingLaunchOptions func(overlay.LaunchOptions) (tea.Model, tea.Cmd)

	// pendingLaunchOptionsCancel runs when the Session Launch Options
	// modal is dismissed without confirming (Esc/ctrl+c). The creation
	// flow (state_new.go/state_prompt.go) sets this to pop-and-kill the
	// pending, not-yet-started instance. The restart flow
	// (runRestartWithOptionsSelected) sets it to a no-op dismiss, since
	// the instance being edited already exists and must survive a
	// cancel untouched. nil outside the stateLaunchOptions window.
	pendingLaunchOptionsCancel func() (tea.Model, tea.Cmd)
```

- [ ] **Step 4: Refactor cancelLaunchOptions to use it**

In `app/state_launch_options.go`, replace `cancelLaunchOptions`:

```go
// cancelLaunchOptions pops and kills the pending, not-yet-started
// instance and returns to stateDefault — the same shape as
// handleStateNewKey's Esc/ctrl+c handling.
func (m *home) cancelLaunchOptions() (tea.Model, tea.Cmd) {
	popped := m.list.PopSelectedForKill()
	m.pendingLaunchOptions = nil
	m.dismissOverlay()
	m.state = stateDefault
	m.instanceChanged()
	return m, tea.Batch(
		tea.Sequence(
			tea.RequestWindowSize,
			func() tea.Msg {
				m.menu.SetState(ui.StateDefault)
				return nil
			},
		),
		backgroundKillCmd(popped),
	)
}
```

with:

```go
// cancelLaunchOptions dismisses the Session Launch Options modal
// without confirming and runs whatever pendingLaunchOptionsCancel the
// opening flow stashed — pop-and-kill the pending instance for
// creation (killPendingLaunchOptionsCancel), or a no-op dismiss for
// restart (runRestartWithOptionsSelected).
func (m *home) cancelLaunchOptions() (tea.Model, tea.Cmd) {
	cancel := m.pendingLaunchOptionsCancel
	m.pendingLaunchOptions = nil
	m.pendingLaunchOptionsCancel = nil
	m.dismissOverlay()
	if cancel == nil {
		m.state = stateDefault
		return m, nil
	}
	return cancel()
}

// killPendingLaunchOptionsCancel is the creation flow's
// pendingLaunchOptionsCancel: pop and kill the pending, not-yet-started
// instance and return to stateDefault — the same shape as
// handleStateNewKey's Esc/ctrl+c handling.
func (m *home) killPendingLaunchOptionsCancel() (tea.Model, tea.Cmd) {
	popped := m.list.PopSelectedForKill()
	m.state = stateDefault
	m.instanceChanged()
	return m, tea.Batch(
		tea.Sequence(
			tea.RequestWindowSize,
			func() tea.Msg {
				m.menu.SetState(ui.StateDefault)
				return nil
			},
		),
		backgroundKillCmd(popped),
	)
}
```

- [ ] **Step 5: Wire the creation flow to set it**

In `app/state_new.go`, right after `m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) { ... }`'s closing brace (before `m.state = stateLaunchOptions`), add:

```go
		m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
			startTask := overlay.ConfirmationTask{
				Sync: func() {
					instance.Program = applyLaunchOptions(opts, m.rcAuth, instance.Program, instance.Title)
					_ = instance.TransitionTo(session.Loading)
					m.newInstanceFinalizer()
					m.promptAfterName = false
					m.state = stateDefault
					m.menu.SetState(ui.StateDefault)
				},
				Async: tea.Batch(tea.RequestWindowSize, func() tea.Msg {
					err := instance.Start(true)
					return instanceStartedMsg{
						instance:        instance,
						err:             err,
						promptAfterName: false,
					}
				}),
			}

			if m.remoteControlBlocked(effectiveRemoteControl(opts), instance.Program) {
				return m, m.promptRemoteControlBlocked(startTask)
			}
			return m, tea.Batch(startTask.Run(), m.instanceChanged())
		}
		m.pendingLaunchOptionsCancel = m.killPendingLaunchOptionsCancel
		m.state = stateLaunchOptions
```

In `app/state_prompt.go`, make the same addition right before `m.state = stateLaunchOptions`:

```go
				m.pendingLaunchOptionsCancel = m.killPendingLaunchOptionsCancel
				m.state = stateLaunchOptions
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./app/... -run TestCancelLaunchOptions_UsesStashedCancelClosure -v`
Expected: PASS

- [ ] **Step 7: Run the full app suite for regressions**

Run: `go test ./app/... -v 2>&1 | tail -120`
Expected: PASS — `TestPromptFlowEndToEndComposesRealClosure` and `TestPromptFlowRemoteControlBlockedViaModalPromptsConfirm` (creation flow) still pass unchanged, since `killPendingLaunchOptionsCancel` reproduces the old inline behavior exactly.

- [ ] **Step 8: Commit**

```bash
git add app/app.go app/state_launch_options.go app/state_new.go app/state_prompt.go app/state_launch_options_test.go
git commit -m "refactor(app): generalize the Session Launch Options cancel path"
```

### Task 26: promptRestartRemoteControlBlocked and the real runRestartWithOptionsSelected

**Files:**
- Modify: `app/remote_control.go`
- Modify: `app/intents.go`
- Test: `app/app_scripts_dispatch_test.go`
- Test: `app/state_restart_options_test.go` (new)

- [ ] **Step 1: Write the failing tests**

First, add the Paused-case dispatch test that Task 24 deliberately deferred (testing the stub there would have meant committing a known-failing test) to `app/app_scripts_dispatch_test.go`, alongside the existing `TestHandleScriptIntentRestartWithOptions_NotPausedIsNoOp`:

```go
func TestHandleScriptIntentRestartWithOptions(t *testing.T) {
	m := homeWithAppState(t)
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	_ = m.list.AddInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Running))
	require.NoError(t, inst.TransitionTo(session.Paused))

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.RestartWithOptionsIntent{},
	})
	assert.Equal(t, stateLaunchOptions, m.state)
}
```

Then add the richer set of tests to a new file `app/state_restart_options_test.go`:

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPausedInstanceHome(t *testing.T) (*home, *session.Instance) {
	t.Helper()
	m := homeWithAppState(t)
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "restart-me",
		Path:    t.TempDir(),
		Program: "headroom wrap claude --model sonnet --permission-mode auto",
	})
	require.NoError(t, err)
	_ = m.list.AddInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Running))
	require.NoError(t, inst.TransitionTo(session.Paused))
	return m, inst
}

func TestRunRestartWithOptionsSelected_OpensModalSeededFromProgram(t *testing.T) {
	m, _ := newPausedInstanceHome(t)

	runRestartWithOptionsSelected(m)

	assert.Equal(t, stateLaunchOptions, m.state)
	lo := m.launchOptionsOverlay()
	require.NotNil(t, lo)
	assert.Equal(t, "sonnet", lo.Options().Model)
	assert.Equal(t, "auto", lo.Options().PermissionMode)
	assert.True(t, lo.Options().HeadroomWrap)
}

func TestRunRestartWithOptionsSelected_ConfirmRecomposesProgramAndResumes(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, cmd := pending(overlay.LaunchOptions{PermissionMode: "default", Model: "opus", Effort: "default"})

	assert.Contains(t, inst.Program, "--model opus")
	assert.NotContains(t, inst.Program, "headroom wrap", "unchecking Headroom Wrap must drop the prefix")
	assert.Equal(t, stateDefault, m.state)
	assert.Equal(t, session.Loading, inst.GetStatus())
	require.NotNil(t, cmd) // the Resume Cmd — not invoked here, just asserting it's returned
}

func TestRunRestartWithOptionsSelected_CancelLeavesInstanceUntouched(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	originalProgram := inst.Program
	runRestartWithOptionsSelected(m)

	_, _ = m.cancelLaunchOptions()

	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.Equal(t, originalProgram, inst.Program)
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.launchOptionsOverlay())
}

func TestRunRestartWithOptionsSelected_BlockedRemoteControlPromptsConfirm(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, _ = pending(overlay.LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default", Effort: "default"})

	assert.Equal(t, stateConfirm, m.state)
	assert.NotNil(t, m.pendingConfirmation.Async)
	// Instance must not have been touched yet — only the confirm
	// dialog is up.
	assert.Equal(t, session.Paused, inst.GetStatus())
}

func TestRunRestartWithOptionsSelected_BlockedRemoteControlCancelLeavesInstanceUntouched(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	originalProgram := inst.Program
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, _ = pending(overlay.LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default", Effort: "default"})
	require.Equal(t, stateConfirm, m.state)

	co := m.confirmation()
	require.NotNil(t, co)
	co.OnCancel()

	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.Equal(t, originalProgram, inst.Program)
	assert.Equal(t, stateDefault, m.state)
}
```

(Check `m.confirmation()`'s exact return type/`OnCancel` field access against how `app/remote_control_test.go`'s existing `TestRemoteControlBlockedAgreesWithComposedCommandWhenHeadroomWrapForcesRCOff`-style tests, or `overlay.ConfirmationOverlay`'s definition, expose `OnCancel` — match the established pattern for triggering it in a test.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./app/... -run 'TestRunRestartWithOptionsSelected|TestHandleScriptIntentRestartWithOptions$' -v`
Expected: FAIL — the stub `runRestartWithOptionsSelected` from Task 24 returns `(m, nil)` without opening anything.

- [ ] **Step 3: Add promptRestartRemoteControlBlocked**

In `app/remote_control.go`, add after `promptRemoteControlBlocked`:

```go
// promptRestartRemoteControlBlocked shows the "remote control
// unavailable" modal for the restart-with-options flow.
// resumeWithoutRC is the SAME task that would have run directly if
// auth weren't blocked — remoteControlProgram already omits
// --remote-control when auth isn't OK regardless of the enabled flag,
// so confirming here just proceeds with the composition the caller
// was always going to apply. Unlike promptRemoteControlBlocked
// (creation flow, which pops/kills the pending not-yet-started
// instance on cancel), cancel here just returns to stateDefault — the
// already-existing Paused instance is untouched.
func (m *home) promptRestartRemoteControlBlocked(resumeWithoutRC overlay.ConfirmationTask) tea.Cmd {
	m.state = stateConfirm
	m.pendingConfirmation = resumeWithoutRC
	msg := "Remote control unavailable: " + m.rcAuth.Reason + "\n\nResume this session without remote control?"
	co := overlay.NewConfirmationOverlay(msg)
	co.SetWidth(60)
	co.OnCancel = func() {
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
	}
	m.setOverlay(co, overlayConfirmation)
	return nil
}
```

- [ ] **Step 4: Replace the stub with the real runRestartWithOptionsSelected**

In `app/intents.go`, replace the stub added in Task 24:

```go
// runRestartWithOptionsSelected is implemented in Task 26 of the
// restart-launch-options plan; this stub only exists so the dispatch
// wiring compiles in the interim.
func runRestartWithOptionsSelected(m *home) (tea.Model, tea.Cmd) {
	return m, nil
}
```

with:

```go
// runRestartWithOptionsSelected opens the Session Launch Options modal
// for the selected Paused instance, seeded from its current
// (reverse-parsed) launch options. Confirming re-composes Program
// against the recovered base program and resumes through the same
// Loading-transition/Resume/save-checkpoint shape runResumeSelected
// uses directly; canceling leaves the instance Paused and untouched
// (pendingLaunchOptionsCancel, not the creation flow's pop-and-kill).
func runRestartWithOptionsSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	opts, base := ParseLaunchOptions(selected.Program)

	m.pendingLaunchOptions = func(newOpts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		resumeTitle := selected.Title
		resumeTask := overlay.ConfirmationTask{
			Sync: func() {
				selected.Program = applyLaunchOptions(newOpts, m.rcAuth, base, selected.Title)
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				if err := selected.TransitionTo(session.Loading); err != nil {
					log.For("app").Warn("resume.skipped", "err", err)
				}
			},
			Async: func() tea.Msg {
				saveFunc := func() error {
					return m.storage.SaveInstances(persistableInstances(m.list.GetInstances()))
				}
				if err := selected.Resume(saveFunc); err != nil {
					return transitionFailedMsg{title: resumeTitle, op: "resume", previousStatus: session.Paused, err: err}
				}
				return resumeDoneMsg{}
			},
		}
		if m.remoteControlBlocked(effectiveRemoteControl(newOpts), selected.Program) {
			return m, m.promptRestartRemoteControlBlocked(resumeTask)
		}
		return m, tea.Batch(resumeTask.Run(), m.instanceChanged())
	}
	m.pendingLaunchOptionsCancel = func() (tea.Model, tea.Cmd) {
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, nil
	}
	m.state = stateLaunchOptions
	m.setOverlay(overlay.NewSessionLaunchOptions(opts, m.rcAuth.Blocked(), m.rcAuth.Reason), overlayLaunchOptions)
	m.menu.SetState(ui.StateNewInstance)
	return m, tea.RequestWindowSize
}
```

`selectedPausedNotWorkspace` (added in Task 24) is already the real, final implementation — nothing further to change there in this task.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./app/... -run 'TestRunRestartWithOptionsSelected|TestHandleScriptIntentRestartWithOptions$' -v`
Expected: PASS — all six new tests from this task.

- [ ] **Step 6: Run the full app suite for regressions**

Run: `go test ./app/... -v 2>&1 | tail -150`
Expected: PASS across the whole package.

- [ ] **Step 7: Commit**

```bash
git add app/remote_control.go app/intents.go app/state_restart_options_test.go app/app_scripts_dispatch_test.go
git commit -m "feat(app): implement restart-with-options — reverse-parse, seed modal, resume"
```

### Task 27: Update help screens and current-state docs for the new keybinding

**Files:**
- Modify: `app/help.go`
- Modify: `CLAUDE.md`
- Modify: `USAGE.md`

- [ ] **Step 1: Add a help entry**

In `app/help.go`, add to `generalHandoffEntries` (right after the `KeyResume` entry) and to any per-instance handoff entry list that lists Resume:

```go
	generalHandoffEntries = []helpEntry{
		{bindings: []keys.KeyName{keys.KeySubmit}, desc: "Commit and push branch to github"},
		{bindings: []keys.KeyName{keys.KeyStash}, desc: "Stash: stash changes and pause session"},
		{bindings: []keys.KeyName{keys.KeyResume}, desc: "Resume a paused session"},
		{bindings: []keys.KeyName{keys.KeyRestartWithOptions}, desc: "Resume a paused session with different launch options"},
	}
```

- [ ] **Step 2: Update CLAUDE.md's keybindings table**

Add a row after `| \`r\` | Resume paused instance |`:

```
| `R` | Resume paused instance with different launch options |
```

- [ ] **Step 3: Update USAGE.md's keybinding reference table**

Add a row alongside the existing `r` / resume row, matching that table's format.

- [ ] **Step 4: Run the full test suite one more time**

Run: `CGO_ENABLED=0 go test ./... 2>&1 | tail -80`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add app/help.go CLAUDE.md USAGE.md
git commit -m "docs: document the R (resume with options) keybinding"
```

### Task 28: Full Phase 4 / final regression pass

**Files:** none (verification only)

- [ ] **Step 1: Run the whole repo test suite**

Run: `CGO_ENABLED=0 go test ./... 2>&1 | tail -100`
Expected: PASS across every package.

- [ ] **Step 2: Race detector pass**

Run: `CGO_ENABLED=1 go test -race ./session/... ./app/... 2>&1 | tail -100`
(Use `CC=clang` if `gcc` is unavailable, per this repo's CLAUDE.md.)
Expected: PASS — no new data races introduced by the mutable `GitWorktree.stashRef` field or the new `pendingLaunchOptionsCancel` field (both follow the same "owned by the Cmd goroutine currently holding the pointer" discipline as existing fields).

- [ ] **Step 3: gofmt and lint**

Run: `gofmt -l .`
Expected: no output.

Run: `golangci-lint run --timeout=3m --fast`
Expected: no new findings.

- [ ] **Step 4: Manual end-to-end smoke test**

Run: `CGO_ENABLED=0 go build -o /tmp/loom_final_build . && /tmp/loom_final_build`

In the running TUI:
1. Create a new instance (`n`), confirm the Session Launch Options modal shows five rows including Effort, start it.
2. Press `s` to stash/pause it. Confirm `git stash list` in that worktree's repo shows an entry (before the worktree is removed) — or just confirm the instance shows `Paused` in the list.
3. Press `R` on the Paused instance. Confirm the Session Launch Options modal opens seeded with its current options.
4. Change Model to a different value, press Enter. Confirm the instance transitions Paused → Loading → Running, and that any uncommitted work from before pausing is present in the resumed worktree.
5. Quit with `q`.

---

## Notes for the implementing engineer

- Every task's Commit step uses a single-line, present-tense, conventional-commit-style message matching this repo's existing history (`git log --oneline` for style reference).
- `CGO_ENABLED=0` is this repo's default build mode (race detector needs `CGO_ENABLED=1 CC=clang`, per `CLAUDE.md`); use it for every `go build`/plain `go test` invocation in this plan.
- Where a step says "check the existing test file first" or "match the existing pattern," it means: read the named file before writing the new test, and follow its established construction helpers/style rather than introducing a new one — this repo's convention throughout (see `CLAUDE.md`'s Code Conventions section).
- Phase boundaries (1/2/3/4) are the natural pause points if a review checkpoint is wanted mid-plan — each phase's final regression-pass task leaves the repo in a fully working, fully tested state.
