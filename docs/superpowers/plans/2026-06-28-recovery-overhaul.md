# Recovery Overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the blocking startup orphan-recovery modal with automatic cleanup of stale worktrees plus inline, decide-later "Recoverable" session entries, and run orphan handling on every workspace-load path so switching workspaces in-app surfaces recovered sessions without a restart.

**Architecture:** Orphan discovery moves into a single `reconcileOrphans` helper called from `activateWorkspace` and classic startup. Each discovered orphan is classified into two buckets: *clean leftovers* (dead tmux + no uncommitted changes) are auto-removed (`git worktree remove -f`, branch preserved), and *needs-review* orphans (uncommitted changes or live tmux) are added to the session list as a new ephemeral `Recoverable`-status instance. A non-blocking summary line reports what happened. The old overlay, state, and `pendingOrphans` plumbing are deleted.

**Tech Stack:** Go 1.23, Charmbracelet Bubble Tea v2 (`charm.land/bubbletea/v2`), lipgloss, tmux, git CLI, testify/assert. Build with `CGO_ENABLED=0 go build -o loom`. Race tests need `CC=clang CGO_ENABLED=1 go test -race ./...`.

**Spec:** [docs/superpowers/specs/2026-06-28-recovery-overhaul-design.md](../specs/2026-06-28-recovery-overhaul-design.md)

---

## Background the engineer needs

**What an "orphan" is:** a worktree directory under `<configDir>/worktrees/<user>/<branch>_<hex>/` that no loaded `Instance` references (state.json lost it, or a crash left it). `session.DiscoverOrphans` already finds these and returns `[]session.OrphanCandidate`.

**Why a new status:** the review-bucket orphans are surfaced inline. They have a worktree on disk but no live tmux and are *not* persisted to state.json (they're re-derived from disk each load). A new `session.Recoverable` status models exactly this. It behaves like `Paused` (inert: no PTY) and is kept out of state.json by `persistableInstances`.

**Key reuse points (verified):**
- `session.ReconcileAndRestore(data InstanceData, cfgDir string, cmdExec) (*Instance, error)` — `session/reconcile.go:94` — adopts an on-disk worktree into a live instance. The recover action uses this.
- `(*Instance).ToInstanceData() InstanceData` — `session/instance.go:225` — serializes an instance back to data (used to recover the placeholder).
- `(*GitWorktree).Remove()` runs `git worktree remove -f` and **keeps the branch** — `session/git/worktree_ops.go:213`. `Cleanup()` only deletes the branch when `!isExistingBranch` — `worktree_ops.go:191`. Orphan worktrees are built with `IsExistingBranch: true`, so the existing kill path preserves the branch — discard reuses `D` unchanged.
- `(*Instance).EnsureRunning()` — `session/instance.go:303` — no-op for `Paused()` and started instances; **must** also no-op for `Recoverable` (added in Task 5).
- `persistableInstances([]*Instance) []*Instance` — `app/app.go:1084` — filters `Ready`/`Deleting`; add `Recoverable` (Task 7).
- `ui.List` swap API: `RemoveInstanceByTitle(title)` (`ui/list.go:507`), `AddInstance(inst)()` (`:657`), `SelectInstance(target)` (`:694`).

**Inertness is mostly free:** `FromInstanceData` returns non-paused instances with `started=false` and no tmux object, so the metadata tick (`app.go:705`, filters on `inst.Started()`), every input guard (`!TmuxAlive()`/`!Started()`), and the daemon (never sees unpersisted instances) already skip `Recoverable`. The only real edits are `persistableInstances`, `EnsureRunning`, the agent preview placeholder, the resume gate, and list rendering.

**CRITICAL — status enum ordering:** `Status` is serialized to state.json as its integer `iota` value. `Recoverable` MUST be appended **after** `Deleting`. Never reorder existing values.

**Schema note:** Adding an enum *value* is not an `InstanceData` field change, and `Recoverable` is never persisted. Do **not** bump `CurrentSchemaVersion` and do **not** touch `cmd/workspace_migrate_shape_test.go`.

---

## File Structure

**Modify:**
- `session/instance.go` — add `Recoverable` status (enum + `String` + transitions); guard `EnsureRunning`.
- `session/orphan.go` — add `HasUncommittedChanges` field + `probeWorktreeDirty`; add `Disposition()`/`OrphanDisposition`; add `RemoveOrphanWorktree`; add `InstanceDataFromOrphan`.
- `app/app.go` — add `recoverySummary` type, `reconcileOrphans`, `claimedWorktreePaths`, `showRecoverySummary`, `recovery` field on `workspaceSlot`; wire into `activateWorkspace` + classic startup; filter `Recoverable` in `persistableInstances`; delete `recordOrphans`/`applyOrphanRecovery`/orphan fields/modal gate.
- `app/intents.go` — widen `selectedPausedNotWorkspace`; add `runRecoverSelected`; reword discard confirm.
- `app/app_scripts.go` — branch `ResumeIntent` to recover when selected is `Recoverable`.
- `app/state_default.go` (or wherever `handleKeyPress` dispatches state) — remove `stateOrphanRecovery` case.
- `app/overlay_host.go` — remove `overlayOrphanRecovery` + `orphanRecovery()`.
- `ui/preview.go` — add `Recoverable` placeholder case + scroll guard.
- `ui/terminal.go` — (optional) reword placeholder for `Recoverable`.
- `ui/list.go` — render `Recoverable` icon/style.
- `ui/err.go` — add info-style message (`SetInfo`).

**Delete:**
- `ui/overlay/orphan_recovery.go` + `ui/overlay/orphan_recovery_test.go`
- `app/state_orphan_recovery.go` + `app/state_orphan_recovery_test.go`

**Test:**
- `session/instance_test.go`, `session/orphan_test.go`, `session/git/...` (new removal test), `app/app_test.go`.

---

## Task 1: Add the `Recoverable` session status

**Files:**
- Modify: `session/instance.go:26-73`
- Test: `session/instance_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/instance_test.go`:

```go
func TestRecoverableStatus_StringAndTransitions(t *testing.T) {
	assert.Equal(t, "Recoverable", session.Recoverable.String())

	// Recover path: Recoverable -> Loading -> Running.
	assert.True(t, session.IsAllowedTransition(session.Recoverable, session.Loading))
	assert.True(t, session.IsAllowedTransition(session.Recoverable, session.Running))
	// Discard path: kill preAction does Recoverable -> Deleting.
	assert.True(t, session.IsAllowedTransition(session.Recoverable, session.Deleting))
	// No status transitions INTO Recoverable (only set at construction).
	assert.False(t, session.IsAllowedTransition(session.Paused, session.Recoverable))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./session/ -run TestRecoverableStatus_StringAndTransitions -v`
Expected: FAIL — `session.Recoverable` undefined (compile error).

- [ ] **Step 3: Add the enum value (AFTER Deleting), String case, and transitions**

In `session/instance.go`, append to the `const` block (after `Deleting`, line 39):

```go
	// Deleting is a transient status set immediately when the user confirms
	// deletion. Cleanup runs asynchronously; on failure the status reverts.
	Deleting
	// Recoverable is an orphaned worktree found on disk and surfaced inline
	// for the user to recover or discard. It has a worktree but no live tmux
	// and is never persisted to state.json (re-derived from disk each load).
	// Appended last so existing serialized Status ints are unchanged.
	Recoverable
)
```

Add to `String()` (before `default`, line 56):

```go
	case Deleting:
		return "Deleting"
	case Recoverable:
		return "Recoverable"
	default:
```

Add a row to `allowedTransitions` (after the `Deleting` row, line 72):

```go
	Deleting:    {Ready: true, Loading: true, Running: true, Prompting: true, Paused: true},
	Recoverable: {Loading: true, Running: true, Deleting: true},
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./session/ -run TestRecoverableStatus_StringAndTransitions -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add session/instance.go session/instance_test.go
git commit -m "feat(session): add Recoverable status for inline orphan recovery"
```

---

## Task 2: Detect uncommitted changes on orphan candidates

**Files:**
- Modify: `session/orphan.go:23-48` (struct), `:140-169` (buildOrphanCandidate)
- Test: `session/orphan_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/orphan_test.go`:

```go
func TestBuildOrphanCandidate_PopulatesDirtyFlag(t *testing.T) {
	origRepo := probeWorktreeRepo
	origDirty := probeWorktreeDirty
	t.Cleanup(func() { probeWorktreeRepo = origRepo; probeWorktreeDirty = origDirty })

	probeWorktreeRepo = func(string) (string, string, error) { return "/repo", "deadbeef", nil }
	probeWorktreeDirty = func(string) bool { return true }

	cand, ok := buildOrphanCandidate("/repo/worktrees/u/feature_abc123", "u", "feature_abc123", nil)
	assert.True(t, ok)
	assert.True(t, cand.HasUncommittedChanges)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./session/ -run TestBuildOrphanCandidate_PopulatesDirtyFlag -v`
Expected: FAIL — `probeWorktreeDirty` and `cand.HasUncommittedChanges` undefined.

- [ ] **Step 3: Add the field, the stubbable probe, and populate it**

In `session/orphan.go`, add to `OrphanCandidate` (after `HasLiveTmux`, line 47):

```go
	// HasLiveTmux reports whether a tmux session named
	// loom_<sanitized-title> is currently running. When true, recovery
	// can adopt the live PTY rather than spawning a new one.
	HasLiveTmux bool
	// HasUncommittedChanges reports whether the worktree has uncommitted
	// edits (git status --porcelain). True (conservatively, on probe
	// error) keeps an orphan in the needs-review bucket so auto-clean
	// never discards unsaved work.
	HasUncommittedChanges bool
}
```

Add a stubbable probe near `probeWorktreeRepo` (after line 71):

```go
// probeWorktreeDirty reports whether the worktree has uncommitted changes.
// Package-level var so tests stub it without a git fixture (mirrors
// probeWorktreeRepo). On any git error it returns true — the safe default
// that routes an unverifiable worktree to needs-review instead of auto-clean.
var probeWorktreeDirty = func(worktreePath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), orphanProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	return len(strings.TrimSpace(string(out))) > 0
}
```

Populate it in `buildOrphanCandidate` (in the returned struct, after `HasLiveTmux`, line 167):

```go
		Title:                 title,
		HasLiveTmux:           CheckTmuxAlive(title, cmdExec),
		HasUncommittedChanges: probeWorktreeDirty(worktreePath),
	}, true
```

- [ ] **Step 4: Run the new test and the full package**

Run: `go test ./session/ -run TestBuildOrphanCandidate_PopulatesDirtyFlag -v`
Expected: PASS

Run: `go test ./session/`
Expected: PASS. If a pre-existing discovery test now fails because the real `probeWorktreeDirty` runs on a non-git temp dir, stub it in that test's setup with `probeWorktreeDirty = func(string) bool { return false }` (and restore in cleanup), matching the existing `probeWorktreeRepo` stubbing pattern.

- [ ] **Step 5: Commit**

```bash
git add session/orphan.go session/orphan_test.go
git commit -m "feat(session): detect uncommitted changes on orphan candidates"
```

---

## Task 3: Classify orphans into clean vs needs-review

**Files:**
- Modify: `session/orphan.go` (add after the struct, ~line 53)
- Test: `session/orphan_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestOrphanCandidate_Disposition(t *testing.T) {
	clean := session.OrphanCandidate{}
	assert.Equal(t, session.DisposeClean, clean.Disposition())

	dirty := session.OrphanCandidate{HasUncommittedChanges: true}
	assert.Equal(t, session.DisposeReview, dirty.Disposition())

	live := session.OrphanCandidate{HasLiveTmux: true}
	assert.Equal(t, session.DisposeReview, live.Disposition())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./session/ -run TestOrphanCandidate_Disposition -v`
Expected: FAIL — `DisposeClean`/`DisposeReview`/`Disposition` undefined.

- [ ] **Step 3: Add the disposition enum and method**

In `session/orphan.go`, after the `OrphanCandidate` struct (line 48):

```go
// OrphanDisposition tells reconcileOrphans how to handle a candidate.
type OrphanDisposition int

const (
	// DisposeClean: dead tmux and no uncommitted changes — a stale
	// leftover. Auto-remove the worktree (branch preserved).
	DisposeClean OrphanDisposition = iota
	// DisposeReview: live tmux or uncommitted changes — surface inline
	// as a Recoverable entry for the user to recover or discard.
	DisposeReview
)

// Disposition buckets a candidate. Any signal of life (a running agent or
// unsaved edits) routes to review; everything else is safe to auto-clean.
func (c OrphanCandidate) Disposition() OrphanDisposition {
	if c.HasLiveTmux || c.HasUncommittedChanges {
		return DisposeReview
	}
	return DisposeClean
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./session/ -run TestOrphanCandidate_Disposition -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add session/orphan.go session/orphan_test.go
git commit -m "feat(session): classify orphans into clean vs needs-review"
```

---

## Task 4: Auto-clean worktree removal (branch-preserving)

**Files:**
- Modify: `session/orphan.go` (add helper near `readWorktreeHEAD`, ~line 260)
- Test: `session/orphan_removal_test.go` (new)

- [ ] **Step 1: Write the failing test (real git fixture)**

Create `session/orphan_removal_test.go`:

```go
package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func TestRemoveOrphanWorktree_RemovesDirKeepsBranch(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "init")
	git(t, repo, "branch", "feature")

	wt := filepath.Join(t.TempDir(), "feature_wt")
	git(t, repo, "worktree", "add", wt, "feature")
	require.DirExists(t, wt)

	err := RemoveOrphanWorktree(repo, wt)
	assert.NoError(t, err)
	assert.NoDirExists(t, wt)

	// Branch must survive.
	cmd := exec.Command("git", "-C", repo, "branch", "--list", "feature")
	out, _ := cmd.Output()
	assert.Contains(t, string(out), "feature")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./session/ -run TestRemoveOrphanWorktree_RemovesDirKeepsBranch -v`
Expected: FAIL — `RemoveOrphanWorktree` undefined.

- [ ] **Step 3: Implement the helper**

In `session/orphan.go`, after `readWorktreeHEAD` (line 260):

```go
// RemoveOrphanWorktree removes an orphaned worktree directory while
// preserving its branch (git worktree remove -f deletes the working tree
// but never the branch). Used to auto-clean stale leftovers during
// reconciliation. -f is required because the worktree may hold tracked
// edits we have already decided to discard, or git may consider it dirty.
func RemoveOrphanWorktree(repoPath, worktreePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), orphanProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "remove", "-f", worktreePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remove worktree %s: %w (%s)", worktreePath, err, strings.TrimSpace(string(out)))
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./session/ -run TestRemoveOrphanWorktree_RemovesDirKeepsBranch -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add session/orphan.go session/orphan_removal_test.go
git commit -m "feat(session): add branch-preserving orphan worktree removal"
```

---

## Task 5: Build InstanceData from a candidate + guard EnsureRunning

**Files:**
- Modify: `session/orphan.go` (add `InstanceDataFromOrphan`), `session/instance.go:303` (guard)
- Test: `session/orphan_test.go`, `session/instance_test.go`

- [ ] **Step 1: Write the failing tests**

In `session/orphan_test.go`:

```go
func TestInstanceDataFromOrphan_BuildsExistingBranchWorktree(t *testing.T) {
	cand := session.OrphanCandidate{
		WorktreePath:  "/cfg/worktrees/u/feature_abc",
		BranchName:    "u/feature",
		RepoPath:      "/repo",
		BaseCommitSHA: "deadbeef",
		Title:         "feature",
	}
	data := session.InstanceDataFromOrphan(cand, "claude")
	assert.Equal(t, "feature", data.Title)
	assert.Equal(t, "/repo", data.Path)
	assert.Equal(t, "u/feature", data.Branch)
	assert.Equal(t, "claude", data.Program)
	assert.Equal(t, session.CurrentSchemaVersion, data.SchemaVersion)
	assert.True(t, data.Worktree.IsExistingBranch)
	assert.Equal(t, "/cfg/worktrees/u/feature_abc", data.Worktree.WorktreePath)
	assert.Equal(t, "deadbeef", data.Worktree.BaseCommitSHA)
}
```

In `session/instance_test.go`:

```go
func TestEnsureRunning_NoOpForRecoverable(t *testing.T) {
	data := session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         "orphan",
		Path:          t.TempDir(),
		Branch:        "u/orphan",
		Status:        session.Recoverable,
		Worktree:      session.GitWorktreeData{RepoPath: t.TempDir(), WorktreePath: t.TempDir(), BranchName: "u/orphan", IsExistingBranch: true},
	}
	inst, err := session.FromInstanceData(data, t.TempDir())
	require.NoError(t, err)
	assert.NoError(t, inst.EnsureRunning()) // must not spawn a PTY
	assert.False(t, inst.Started())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./session/ -run 'TestInstanceDataFromOrphan_BuildsExistingBranchWorktree|TestEnsureRunning_NoOpForRecoverable' -v`
Expected: FAIL — `InstanceDataFromOrphan` undefined; `EnsureRunning` spawns/`Started()` true for Recoverable.

- [ ] **Step 3: Implement both**

In `session/orphan.go`, after `RemoveOrphanWorktree`:

```go
// InstanceDataFromOrphan reconstructs InstanceData for an orphan candidate.
// IsExistingBranch is always true so a later Kill (discard) removes only the
// worktree, never the branch. Status is left zero (Running); callers override
// it (Recoverable for the inline placeholder, Running for recovery).
func InstanceDataFromOrphan(cand OrphanCandidate, program string) InstanceData {
	now := time.Now()
	return InstanceData{
		SchemaVersion: CurrentSchemaVersion,
		Title:         cand.Title,
		Path:          cand.RepoPath,
		Branch:        cand.BranchName,
		CreatedAt:     now,
		UpdatedAt:     now,
		Program:       program,
		Worktree: GitWorktreeData{
			RepoPath:         cand.RepoPath,
			WorktreePath:     cand.WorktreePath,
			SessionName:      cand.Title,
			BranchName:       cand.BranchName,
			BaseCommitSHA:    cand.BaseCommitSHA,
			IsExistingBranch: true,
		},
	}
}
```

In `session/instance.go`, add the guard at the top of `EnsureRunning` (line 303):

```go
func (i *Instance) EnsureRunning() error {
	if i.GetStatus() == Recoverable {
		// An orphan surfaced inline; never auto-spawn its PTY. It goes
		// live only via the explicit recover action (ReconcileAndRestore).
		return nil
	}
	if i.Paused() {
		return nil
	}
	if i.isStarted() {
		return nil
	}
	return i.Start(false)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./session/ -run 'TestInstanceDataFromOrphan_BuildsExistingBranchWorktree|TestEnsureRunning_NoOpForRecoverable' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add session/orphan.go session/instance.go session/orphan_test.go session/instance_test.go
git commit -m "feat(session): orphan->InstanceData builder and Recoverable EnsureRunning guard"
```

---

## Task 6: Keep `Recoverable` out of state.json

**Files:**
- Modify: `app/app.go:1084-1094`
- Test: `app/app_test.go`

- [ ] **Step 1: Write the failing test**

Add to `app/app_test.go`:

```go
func TestPersistableInstances_ExcludesRecoverable(t *testing.T) {
	data := session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         "orphan", Path: t.TempDir(), Branch: "u/orphan",
		Status:   session.Recoverable,
		Worktree: session.GitWorktreeData{RepoPath: t.TempDir(), WorktreePath: t.TempDir(), BranchName: "u/orphan", IsExistingBranch: true},
	}
	inst, err := session.FromInstanceData(data, t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, persistableInstances([]*session.Instance{inst}))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/ -run TestPersistableInstances_ExcludesRecoverable -v`
Expected: FAIL — the Recoverable instance is returned (not filtered).

- [ ] **Step 3: Add `Recoverable` to the filter**

In `app/app.go`, update `persistableInstances` (line 1088) and its doc comment:

```go
// persistableInstances filters out instances whose state should not reach disk:
// Ready (mid-creation), Deleting (kill in progress), and Recoverable (an orphan
// surfaced inline; it is re-derived from disk each load and adopted only on
// explicit recovery, so persisting it would resurrect a never-confirmed entry).
func persistableInstances(instances []*session.Instance) []*session.Instance {
	var result []*session.Instance
	for _, inst := range instances {
		status := inst.GetStatus()
		if status == session.Ready || status == session.Deleting || status == session.Recoverable {
			continue
		}
		result = append(result, inst)
	}
	return result
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./app/ -run TestPersistableInstances_ExcludesRecoverable -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add app/app.go app/app_test.go
git commit -m "feat(app): exclude Recoverable instances from persistence"
```

---

## Task 7: Render `Recoverable` in the list + preview placeholder

**Files:**
- Modify: `ui/list.go` (status icon block, ~line 264-277, plus a style var), `ui/preview.go:159-190` (+ `:462` scroll guard)
- Verify: build + manual (UI rendering isn't unit-tested here).

- [ ] **Step 1: Add a Recoverable icon/style in `ui/list.go`**

Find the existing icon/style vars (search for `pausedIcon` and `pausedStyle`). Add alongside them:

```go
var recoverableIcon = "⟲"
var recoverableStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500"))
```

In the per-instance status switch (the non-workspace-terminal branch, after `case session.Paused:` at line 272):

```go
		case session.Paused:
			join = pausedStyle.Render(pausedIcon)
		case session.Recoverable:
			join = recoverableStyle.Render(recoverableIcon)
		case session.Deleting:
			join = deletingStyle.Render(deletingIcon)
```

- [ ] **Step 2: Add the agent-preview placeholder in `ui/preview.go`**

In `UpdateContent`, after the `case instance.GetStatus() == session.Paused:` block (around line 179), add:

```go
	case instance.GetStatus() == session.Recoverable:
		p.setFallbackState(lipgloss.JoinVertical(lipgloss.Center,
			"Recoverable session (found on disk).",
			"",
			"Press 'r' to recover it, or 'D' to discard.",
		))
		return nil
```

Extend the scroll guard at `ui/preview.go:463`:

```go
func (p *PreviewPane) setOffset(instance *session.Instance, off int) error {
	if instance != nil && (instance.GetStatus() == session.Paused || instance.GetStatus() == session.Recoverable) {
		return nil
	}
```

- [ ] **Step 3: (optional) reword the terminal placeholder in `ui/terminal.go`**

The terminal pane already shows "Instance is not started yet." for a Recoverable instance (via `!instance.Started()` at line 134), which is acceptable. To be explicit, add before that check (after line 131's Paused case):

```go
	if instance.GetStatus() == session.Recoverable {
		t.setFallbackState("Recoverable session. Press 'r' to recover.")
		return nil
	}
```

- [ ] **Step 4: Build**

Run: `CGO_ENABLED=0 go build -o loom`
Expected: builds clean.

- [ ] **Step 5: Commit**

```bash
git add ui/list.go ui/preview.go ui/terminal.go
git commit -m "feat(ui): render Recoverable status in list and preview"
```

---

## Task 8: The `reconcileOrphans` engine + summary type

**Files:**
- Modify: `app/app.go` — add `recoverySummary`, `claimedWorktreePaths`, `reconcileOrphans`, `showRecoverySummary`; add `recovery` field to `workspaceSlot` (line 124).
- Test: `app/app_test.go` (pure pieces).

- [ ] **Step 1: Write the failing test for the summary string**

```go
func TestRecoverySummary_String(t *testing.T) {
	assert.True(t, recoverySummary{}.empty())
	assert.Equal(t, "Recovery: cleaned 1 stale worktree", recoverySummary{cleaned: 1}.String())
	assert.Equal(t, "Recovery: cleaned 2 stale worktrees · 3 sessions need review (in list)",
		recoverySummary{cleaned: 2, review: 3}.String())
	assert.Equal(t, "Recovery: 1 session needs review (in list)", recoverySummary{review: 1}.String())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/ -run TestRecoverySummary_String -v`
Expected: FAIL — `recoverySummary` undefined.

- [ ] **Step 3: Implement the summary type and helpers**

Add to `app/app.go` (near `persistableInstances`):

```go
// recoverySummary tallies what a reconcileOrphans pass did, for the
// non-blocking one-line summary shown to the user.
type recoverySummary struct {
	cleaned int // stale worktrees auto-removed
	review  int // Recoverable entries added to the list
}

func (s recoverySummary) empty() bool { return s.cleaned == 0 && s.review == 0 }

func (s recoverySummary) String() string {
	plural := func(n int, one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	var parts []string
	if s.cleaned > 0 {
		parts = append(parts, "cleaned "+plural(s.cleaned, "stale worktree", "stale worktrees"))
	}
	if s.review > 0 {
		verb := "need"
		if s.review == 1 {
			verb = "needs"
		}
		parts = append(parts, fmt.Sprintf("%s %s review (in list)", plural(s.review, "session", "sessions"), verb))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Recovery: " + strings.Join(parts, " · ")
}
```

Add a `recovery` field to `workspaceSlot` (struct at line 124):

```go
type workspaceSlot struct {
	// ... existing fields (wsCtx, storage, appConfig, appState, list, splitPane) ...
	// recovery holds the orphan-reconcile summary from this slot's last
	// activation, surfaced once the slot becomes focused.
	recovery recoverySummary
}
```

- [ ] **Step 4: Implement `claimedWorktreePaths`, `reconcileOrphans`, `showRecoverySummary`**

Add to `app/app.go`:

```go
// claimedWorktreePaths returns the set of worktree paths already accounted
// for: live instances plus storage's unrecovered cache (records that failed
// reconcile but remain tracked in state.json). Orphan discovery skips these.
func claimedWorktreePaths(claimed []*session.Instance, storage *session.Storage) map[string]bool {
	paths := make(map[string]bool, len(claimed))
	for _, inst := range claimed {
		wt, err := inst.GetGitWorktree()
		if err != nil || wt == nil {
			continue
		}
		if p := wt.GetWorktreePath(); p != "" {
			paths[p] = true
		}
	}
	if storage != nil {
		for p := range storage.UnrecoveredWorktreePaths() {
			paths[p] = true
		}
	}
	return paths
}

// reconcileOrphans discovers orphaned worktrees for one workspace, auto-cleans
// stale leftovers, and adds inline Recoverable entries for orphans that need a
// human decision. It mutates list (adds Recoverable instances) and returns a
// summary for the caller to surface. Safe to run on any workspace-load path.
func (m *home) reconcileOrphans(cfgDir, program string, list *ui.List, storage *session.Storage, cmdExec cmd2.Executor) recoverySummary {
	var summary recoverySummary
	orphans, err := session.DiscoverOrphans(cfgDir, claimedWorktreePaths(list.GetInstances(), storage), cmdExec)
	if err != nil {
		log.For("app").Warn("orphan_discovery_failed", "cfg_dir", cfgDir, "err", err)
		return summary
	}
	for _, cand := range orphans {
		switch cand.Disposition() {
		case session.DisposeClean:
			if err := session.RemoveOrphanWorktree(cand.RepoPath, cand.WorktreePath); err != nil {
				log.For("app").Warn("orphan_autoclean_failed", "worktree", cand.WorktreePath, "err", err)
				continue
			}
			summary.cleaned++
		case session.DisposeReview:
			data := session.InstanceDataFromOrphan(cand, program)
			data.Status = session.Recoverable
			inst, err := session.FromInstanceData(data, cfgDir)
			if err != nil {
				log.For("app").Warn("orphan_placeholder_failed", "title", cand.Title, "err", err)
				continue
			}
			list.AddInstance(inst)()
			summary.review++
		}
	}
	return summary
}

// showRecoverySummary surfaces a reconcile summary on the error bar as a
// non-alarming info line. No-op when nothing happened.
func (m *home) showRecoverySummary(s recoverySummary) {
	if s.empty() {
		return
	}
	m.errBox.SetInfo(s.String())
}
```

> **Prerequisite:** `showRecoverySummary` calls `errBox.SetInfo`, which is added in Task 9 (`ui/err.go` — standalone and tiny). Apply Task 9's `ui/err.go` edit before building this task (do Task 9 first, or fold its err.go change into this commit). Then this task compiles and its commit builds green.

- [ ] **Step 5: Run the unit test + build**

Run: `go test ./app/ -run TestRecoverySummary_String -v`
Expected: PASS
Run: `CGO_ENABLED=0 go build -o loom`
Expected: builds clean (with the SetInfo/SetError note above resolved).

- [ ] **Step 6: Commit**

```bash
git add app/app.go app/app_test.go
git commit -m "feat(app): reconcileOrphans engine with auto-clean and inline review"
```

---

## Task 9: Info-style line on the error bar

**Files:**
- Modify: `ui/err.go`
- Test: `ui/err_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Create/append `ui/err_test.go`:

```go
package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrBox_InfoShownWhenNoError(t *testing.T) {
	b := NewErrBox()
	b.SetSize(80, 1)
	b.SetInfo("Recovery: cleaned 2 stale worktrees")
	assert.Contains(t, b.String(), "cleaned 2 stale worktrees")

	// An error takes precedence over info.
	b.SetError(errors.New("boom"))
	assert.Contains(t, b.String(), "boom")
	assert.False(t, strings.Contains(b.String(), "cleaned"))

	b.Clear()
	assert.NotContains(t, b.String(), "boom")
	assert.NotContains(t, b.String(), "cleaned")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./ui/ -run TestErrBox_InfoShownWhenNoError -v`
Expected: FAIL — `SetInfo` undefined.

- [ ] **Step 3: Add the info field, setter, neutral style, and render precedence**

In `ui/err.go`, extend the struct and styles:

```go
type ErrBox struct {
	height, width int
	err           error
	info          string
}

var errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
var infoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7"))
```

Add the setter and extend `Clear`:

```go
// SetInfo sets a non-error status line (e.g. the recovery summary). An active
// error takes precedence over info in String().
func (e *ErrBox) SetInfo(msg string) {
	e.info = msg
}

func (e *ErrBox) Clear() {
	e.err = nil
	e.info = ""
}
```

Update `String()` so info renders when there's no error:

```go
func (e *ErrBox) String() string {
	var msg string
	style := errStyle
	switch {
	case e.err != nil:
		msg = e.err.Error()
	case e.info != "":
		msg = e.info
		style = infoStyle
	}
	if msg != "" {
		lines := strings.Split(msg, "\n")
		msg = strings.Join(lines, "//")
		if runewidth.StringWidth(msg) > e.width-3 && e.width-3 >= 0 {
			msg = runewidth.Truncate(msg, e.width-3, "...")
		}
	}
	return lipgloss.Place(e.width, e.height, lipgloss.Center, lipgloss.Center, style.Render(msg))
}
```

If Task 8 used the `SetError` stand-in, switch `showRecoverySummary` back to `m.errBox.SetInfo(s.String())` now.

- [ ] **Step 4: Run test + build**

Run: `go test ./ui/ -run TestErrBox_InfoShownWhenNoError -v`
Expected: PASS
Run: `CGO_ENABLED=0 go build -o loom`
Expected: builds clean.

- [ ] **Step 5: Commit**

```bash
git add ui/err.go ui/err_test.go app/app.go
git commit -m "feat(ui): add info-style status line to the error bar"
```

---

## Task 10: Wire `reconcileOrphans` into every load path (new flow goes live)

**Files:**
- Modify: `app/app.go` — classic startup (361-380), `activateWorkspace` (after instances loop), `restoreSavedWorkspaces` (remove deferred loop, show focused summary), registration handler (remove recordOrphans, show summary), startup picker handler (`app/state_workspace_picker.go`), toggle (`applyWorkspaceToggle`).

This task switches all call sites from the old `recordOrphans` (which populates `pendingOrphans` → modal) to `reconcileOrphans`. After this, `pendingOrphans` is never populated, so the modal gate at line 453 stays dormant. The dead overlay code is deleted in Task 12.

- [ ] **Step 1: Classic startup — replace recordOrphans, drop the candidate exemption**

In `app/app.go`, replace the orphan block at lines 361-377. The new Recoverable instances are added to `h.list` by `reconcileOrphans`, so the `claimedTitles` loop below already exempts their tmux — the separate `pendingOrphans` exemption is no longer needed:

```go
		// Discover orphan worktrees (on disk but not in state.json),
		// auto-clean stale leftovers, and add inline Recoverable entries
		// for any with unsaved work or a live agent. Runs before
		// CleanupOrphanedSessions so a live recoverable's tmux (now a
		// list instance) is exempted by the claimedTitles loop below.
		startupRecovery = h.reconcileOrphans(cfgDir, program, h.list, storage, cmdExec)

		// Clean up orphaned tmux sessions from previous crashes
		claimedTitles := make(map[string]bool)
		for _, inst := range h.list.GetInstances() {
			claimedTitles[inst.Title] = true
		}
		if err := session.CleanupOrphanedSessions(claimedTitles, cmdExec); err != nil {
			log.For("app").Error("orphan_cleanup_failed", "err", err)
		}
```

Then, just before `return h, nil` at line 461, surface the summary:

```go
	h.showRecoverySummary(startupRecovery)
	return h, nil
```

Declare `var startupRecovery recoverySummary` near the top of `newHome` (right after `cmdExec := cmd2.MakeExecutor()`, line 325) so it survives to the return. The `=` assignment above sets it in classic mode; in restore mode it stays zero (a no-op for `showRecoverySummary`), and `restoreSavedWorkspaces` surfaces the focused slot's own summary instead.

- [ ] **Step 2: `activateWorkspace` — reconcile and stash on the slot**

In `app/app.go`, replace the "intentionally NOT done here" comment block (lines 1539-1545) with the reconcile call, and stash the result on the appended slot. After the instances loop and crash-restart loop, before constructing the workspace terminal is fine; simplest is to compute it just before the slot append (line 1620) so `list` is fully populated:

Replace the comment at 1539-1545 with:
```go
	// Orphan discovery runs here so every workspace-load path (startup
	// picker, mid-session toggle, restore, registration) surfaces
	// recovered sessions identically — no restart required.
```

Then change the slot append (line 1620) to capture the summary:
```go
	recovery := m.reconcileOrphans(wsCtx.ConfigDir, appConfig.GetProgram(), list, storage, cmdExec)
	m.slots = append(m.slots, workspaceSlot{
		wsCtx:     wsCtx,
		storage:   storage,
		appConfig: appConfig,
		appState:  state,
		list:      list,
		splitPane: splitPane,
		recovery:  recovery,
	})
	return nil
```

- [ ] **Step 3: `restoreSavedWorkspaces` — delete the deferred loop, show focused summary**

In `app/app.go`, remove the deferred orphan loop at lines 496-507 (the `cmdExec := ...` through the `recordOrphans` loop). `activateWorkspace` now handles it per slot. After `m.loadSlot(focused)` (line 526), add:

```go
	m.loadSlot(focused)
	m.updateTabBarStatuses()
	m.showRecoverySummary(m.slots[focused].recovery)
```

The restore path leaves `startupRecovery` (Step 1) zero and surfaces the focused slot's own summary here, so there's no double-show.

- [ ] **Step 4: Registration handler — drop recordOrphans, show summary**

In `app/app.go`, remove the recordOrphans block at lines 1008-1012. After `m.loadSlot(m.focusedSlot)` (line 1023), add:

```go
	m.loadSlot(m.focusedSlot)
	m.updateTabBarStatuses()
	m.showRecoverySummary(m.slots[m.focusedSlot].recovery)
```

- [ ] **Step 5: Startup picker handler — show summary**

In `app/state_workspace_picker.go`, after `m.loadSlot(0)` (line 34), add:

```go
		m.loadSlot(0)
		m.updateTabBarStatuses()
		m.showRecoverySummary(m.slots[0].recovery)
```

- [ ] **Step 6: Toggle handler — show focused summary**

In `app/app.go` `applyWorkspaceToggle`, after the final `loadSlot`/before `return tea.RequestWindowSize` (around line 1943), add:

```go
	m.showRecoverySummary(m.slots[m.focusedSlot].recovery)
```

(If the toggle path doesn't call `loadSlot` explicitly, add `m.showRecoverySummary(m.slots[m.focusedSlot].recovery)` after `m.tabBar.SetWorkspaces(...)`.)

- [ ] **Step 7: Build + full test + manual smoke**

Run: `CGO_ENABLED=0 go build -o loom && go test ./...`
Expected: builds; tests pass (the orphan overlay tests still pass — that code still exists, just isn't triggered).

Manual: create a fake orphan and confirm the new behavior:
```bash
# In a registered workspace's repo (replace <repo> and <cfg>):
#   <cfg> is ~/.loom (or the workspace's config dir)
git -C <repo> worktree add <cfg>/worktrees/$USER/throwaway_deadbeef -b $USER/throwaway
# Clean orphan (no changes): launch loom -> expect NO modal, an info line
#   "Recovery: cleaned 1 stale worktree", and the dir removed:
./loom    # then quit with q
ls <cfg>/worktrees/$USER/    # throwaway_deadbeef should be gone
```
Then a review orphan:
```bash
git -C <repo> worktree add <cfg>/worktrees/$USER/keep_deadbeef -b $USER/keep
echo dirty > <cfg>/worktrees/$USER/keep_deadbeef/UNSAVED.txt
./loom   # expect NO modal; a "⟲" Recoverable entry named "keep" in the list;
         # selecting it shows the "Press 'r' to recover" placeholder.
```

- [ ] **Step 8: Commit**

```bash
git add app/app.go app/state_workspace_picker.go
git commit -m "feat(app): run orphan reconcile on every workspace-load path"
```

---

## Task 11: Recover (`r`) and discard (`D`) actions for Recoverable

**Files:**
- Modify: `app/intents.go:51-57` (widen gate), add `runRecoverSelected` + reword discard confirm; `app/app_scripts.go:481-485` (branch ResumeIntent); `app/app.go` (add `recoverDoneMsg` handler).

- [ ] **Step 1: Widen the resume gate to include Recoverable**

In `app/intents.go`, update `selectedPausedNotWorkspace` (line 51) — keep the name but allow both statuses, since `r` now serves resume (Paused) and recover (Recoverable):

```go
// selectedResumableNotWorkspace gates the 'r' key: a Paused instance
// (resume → recreate worktree) or a Recoverable orphan (recover → adopt
// the on-disk worktree), never a workspace terminal.
func selectedResumableNotWorkspace(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.IsWorkspaceTerminal {
		return false
	}
	s := selected.GetStatus()
	return s == session.Paused || s == session.Recoverable
}
```

Find every caller before renaming (there should be exactly one, in `app/app_scripts.go`):

Run: `grep -rn 'selectedPausedNotWorkspace' app/ --include='*.go'`
Update each hit to `selectedResumableNotWorkspace` (the `ResumeIntent` dispatch is changed in the next step).

- [ ] **Step 2: Branch `ResumeIntent` to recover vs resume**

In `app/app_scripts.go`, replace the `ResumeIntent` case (lines 481-485):

```go
	case script.ResumeIntent:
		if !selectedResumableNotWorkspace(m) {
			break
		}
		if m.list.GetSelectedInstance().GetStatus() == session.Recoverable {
			_, cmd = runRecoverSelected(m)
		} else {
			_, cmd = runResumeSelected(m)
		}
```

- [ ] **Step 3: Implement `runRecoverSelected` and the done-message**

In `app/intents.go`, add (mirrors `runResumeSelected`'s goroutine pattern; the swap happens on the main goroutine in the message handler):

```go
// runRecoverSelected adopts the selected Recoverable orphan: it serializes
// the inline placeholder, flips the data to Running, and runs
// ReconcileAndRestore (which adopts the existing worktree and spawns tmux)
// off the UI goroutine. The list swap + persist happen in handleRecoverDone.
func runRecoverSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	cfgDir := m.configDir()
	data := selected.ToInstanceData()
	data.Status = session.Running
	oldTitle := selected.Title
	cmdExec := cmd2.MakeExecutor()
	recoverCmd := func() tea.Msg {
		inst, err := session.ReconcileAndRestore(data, cfgDir, cmdExec)
		if err != nil {
			return recoverDoneMsg{oldTitle: oldTitle, err: err}
		}
		return recoverDoneMsg{oldTitle: oldTitle, recovered: inst}
	}
	return m, recoverCmd
}
```

In `app/app.go`, define the message near `killInstanceMsg` (line 1253) and handle it near the `killInstanceMsg` case (line 908):

```go
// recoverDoneMsg is returned after a Recoverable orphan is adopted into a
// live instance off the UI goroutine. The handler swaps the inline
// placeholder for the recovered instance and persists.
type recoverDoneMsg struct {
	oldTitle  string
	recovered *session.Instance
	err       error
}
```

```go
	case recoverDoneMsg:
		if msg.err != nil {
			return m, m.handleError(fmt.Errorf("recover %s: %w", msg.oldTitle, msg.err))
		}
		m.list.RemoveInstanceByTitle(msg.oldTitle)
		m.list.AddInstance(msg.recovered)()
		m.list.SelectInstance(msg.recovered)
		if err := m.storage.SaveInstances(persistableInstances(m.list.GetInstances())); err != nil {
			log.For("app").Error("recover.save_failed", "title", msg.recovered.Title, "err", err)
		}
		return m, tea.Batch(tea.RequestWindowSize, m.instanceChanged())
```

- [ ] **Step 4: Reword the discard confirmation (D on a Recoverable)**

Discard reuses the existing kill path (it preserves the branch because the worktree is `IsExistingBranch: true`). Only the confirmation wording changes. In `app/intents.go` `runKillSelected` (line 148), adjust the message:

```go
func runKillSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	preAction, killAction := killActionFor(m, selected)
	message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
	if selected.GetStatus() == session.Recoverable {
		message = fmt.Sprintf("[!] Discard recoverable session '%s'? Uncommitted changes are lost; the branch is kept.", selected.Title)
	}
	return m, m.confirmTask(message, overlay.ConfirmationTask{
		Sync:  preAction,
		Async: killAction,
	})
}
```

- [ ] **Step 5: Build + manual verify recover and discard**

Run: `CGO_ENABLED=0 go build -o loom`
Expected: builds clean.

Manual (continuing from Task 10's "keep" review orphan):
```bash
./loom
# Select the "⟲ keep" entry, press 'r'. Expect it to become a live Running
# session (spinner, agent pane), and persist. Quit, relaunch: it loads as a
# normal session (no longer Recoverable, no modal).
```
Discard:
```bash
git -C <repo> worktree add <cfg>/worktrees/$USER/junk_deadbeef -b $USER/junk
echo x > <cfg>/worktrees/$USER/junk_deadbeef/UNSAVED.txt
./loom
# Select "⟲ junk", press 'D'. Confirm the "Discard recoverable session" prompt.
# Expect the worktree dir removed and the entry gone. Verify the branch survives:
git -C <repo> branch --list "$USER/junk"   # still listed
```

- [ ] **Step 6: Commit**

```bash
git add app/intents.go app/app_scripts.go app/app.go
git commit -m "feat(app): inline recover and discard actions for Recoverable orphans"
```

---

## Task 12: Delete the obsolete orphan-recovery overlay and plumbing

Now that the inline flow is live and `pendingOrphans` is never populated, remove the dead code. Build must stay green.

**Files:**
- Delete: `ui/overlay/orphan_recovery.go`, `ui/overlay/orphan_recovery_test.go`, `app/state_orphan_recovery.go`, `app/state_orphan_recovery_test.go`
- Modify: `app/app.go` (state enum, struct fields, modal gate, `recordOrphans`, `applyOrphanRecovery` + helpers, `handleKeyPress` dispatch), `app/overlay_host.go` (`overlayOrphanRecovery`, `orphanRecovery()`).

- [ ] **Step 1: Remove the startup modal gate**

In `app/app.go`, replace the orphan-overlay gate (lines 448-459) with a direct call:

```go
	// Surface the next startup overlay (workspace registration confirm or
	// the workspace picker). Orphans are now handled inline by
	// reconcileOrphans, so nothing preempts this.
	registerNextOverlay()
```

- [ ] **Step 2: Delete `recordOrphans`, `applyOrphanRecovery`, and their now-unused helpers**

In `app/app.go`, delete `recordOrphans` (lines 1705-1751), `applyOrphanRecovery` (1753-1835), and the helpers used only by it: `listForCfgDir` (1837-1854), `storageForCfgDir` (1856-1871), `programForCfgDir` (1873+). Before deleting each helper, confirm no other caller:

Run: `grep -rn 'listForCfgDir\|storageForCfgDir\|programForCfgDir\|recordOrphans\|applyOrphanRecovery' app/ --include='*.go'`
Expected after deletion: only the removed definitions are gone; **no remaining references**. If any helper is still referenced elsewhere, keep that one.

- [ ] **Step 3: Remove the home struct orphan fields**

In `app/app.go`, delete `pendingOrphans`, `orphanCfgDirs`, and `pendingStartupOverlay` (lines 257-273). Then:

Run: `grep -rn 'pendingOrphans\|orphanCfgDirs\|pendingStartupOverlay' app/ --include='*.go'`
Expected: no matches.

- [ ] **Step 4: Remove the state + overlay constants and accessor**

- In `app/app.go`, delete the `stateOrphanRecovery` constant (lines 115-119).
- In the state dispatch in `handleKeyPress` (search `case stateOrphanRecovery:`), remove that case and its call to `handleStateOrphanRecoveryKey`.
- In `app/overlay_host.go`, delete `overlayOrphanRecovery` (line 24) and the `orphanRecovery()` method (lines 93-101).

- [ ] **Step 5: Delete the overlay and state-handler files**

```bash
git rm ui/overlay/orphan_recovery.go ui/overlay/orphan_recovery_test.go \
       app/state_orphan_recovery.go app/state_orphan_recovery_test.go
```

- [ ] **Step 6: Build + full test**

Run: `CGO_ENABLED=0 go build -o loom && go test ./...`
Expected: builds clean; all tests pass. Fix any dangling references the compiler flags (e.g. a leftover `NewOrphanRecoveryPicker` import).

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor(app): remove obsolete orphan-recovery overlay and plumbing"
```

---

## Task 13: Final verification

- [ ] **Step 1: Format + lint**

Run: `gofmt -w . && golangci-lint run --timeout=3m --fast`
Expected: no diffs from gofmt; lint clean.

- [ ] **Step 2: Full test suite + race**

Run: `go test ./...`
Expected: PASS

Run: `CC=clang CGO_ENABLED=1 go test -race ./session/ ./app/`
Expected: PASS (no data races — the recover swap and save run on the main goroutine; `ReconcileAndRestore` runs in a Cmd goroutine and touches only its own new instance + disk).

- [ ] **Step 3: End-to-end manual matrix**

With a registered workspace, verify each path surfaces orphans without a modal and without a restart:
1. **Clean orphan → auto-clean:** create a clean orphan worktree, launch loom → info line "cleaned 1 stale worktree", dir gone, no modal.
2. **Review orphan → inline:** create a dirty orphan, launch → "⟲" entry, no modal; `r` recovers it live; relaunch shows it as a normal session.
3. **Discard:** create a dirty orphan, `D` → reworded confirm → worktree gone, branch kept.
4. **Workspace switch (the original bug):** with a second registered workspace holding an orphan, switch to it in-app (`W` / `[` `]`) → the orphan surfaces immediately (inline entry and/or info line) **without restarting loom**.
5. **No orphans:** launch with a clean state → no info line, no modal, normal startup.

- [ ] **Step 4: Update CLAUDE.md architecture notes**

In `CLAUDE.md`, update the `session/` and Gotchas sections to reflect: orphan recovery is now inline (no overlay); `Recoverable` status; `reconcileOrphans` runs in `activateWorkspace`. Remove the now-stale reference to the orphan-recovery picker overlay in the `ui/overlay/` list.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "docs: update CLAUDE.md for inline recovery; final cleanup"
```

---

## Self-review notes (for the implementer)

- **If `loadSlot` isn't called in a path you wired** (Task 10 steps 5-6), `m.slots[...].recovery` is still valid — it's set at activation. Show it after focus settles.
- **The `recovery` function-scope var in `newHome`** is only meaningful on the classic path; in restore mode it stays zero and `restoreSavedWorkspaces` surfaces the focused slot's field instead. Don't double-show.
- **Do not** add `Recoverable` to the metadata-tick filter or input guards — they already exclude it via `Started()`/`TmuxAlive()`. Adding redundant checks is noise.
- **Branch preservation** depends entirely on `IsExistingBranch: true` in `InstanceDataFromOrphan`. If a test shows a discarded branch disappearing, that flag regressed.
