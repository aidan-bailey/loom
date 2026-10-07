# Locked Worktrees and Claude Temp-Dir Archiving Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan package by package: each package (A–D) is one task for the sub-skill. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Loom removes worktrees left locked by an interrupted `git worktree add`, and archives Claude Code's per-session temp dir (scratchpad, task output) instead of leaving it on tmpfs forever: zipped on pause, kill and a load-time sweep, restored on resume.

**Architecture:** Part 1 adds `git.RefuseLocked`, called only by the three paths that mean to delete a tree (orphan auto-clean, `GitWorktree.Cleanup` for Kill, `GitWorktree.Remove` for Pause). It removes a stale `initializing` lock and refuses every other lock with an actionable `*git.LockedError`. Part 2 adds a leaf package `session/claudetmp` (locate, archive, restore; no tmux/UI/app/config dependency), and `session/claude_tmp.go` wires it into `Instance.Pause`, `Kill`, `Resume` and a new `session.SweepClaudeTemp`. `app` queues the sweep from `reconcileOrphans` (every workspace-load path) and dispatches it on the health tick through a new `gateClaudeTmp` pollGate.

**Tech Stack:** Go 1.25 (`os.Root` incl. `Root.Symlink`/`MkdirAll`/`Chmod`), `archive/zip` + `compress/flate`, Bubble Tea v2, testify. Spec: [`docs/superpowers/specs/2026-10-05-worktree-locks-and-claude-temp-archive-design.md`](../specs/2026-10-05-worktree-locks-and-claude-temp-archive-design.md).

---

## Decisions

The spec is approved. These refine it where reading the code turned up something it did not cover. Every one fails closed; none changes what the user sees except decision 3.

| # | Decision |
|---|---|
| 1 | **The lock helper is exported as `git.RefuseLocked(repo, wt, runner) error`** around the spec's `unlockStaleInit`: `session.RemoveOrphanWorktree` lives outside `session/git`. A respected lock returns a `*git.LockedError`, which matches `errors.Is(err, git.ErrWorktreeLocked)`, and its message names `git -C <repo> worktree unlock <path>`. |
| 2 | **`GitWorktree.cleanup` returns at a respected lock before deleting the branch or the title sidecar.** The spec only says it returns an error. Deleting the sidecar of a tree that stays would degrade a later orphan recovery to the lossy humanized title, and `branch -D` would fail on the checked-out branch anyway. |
| 3 | **`claudetmp.Restore` creates a missing root (mode 0700).** The spec says every operation no-ops when the root is absent, but `/tmp` is tmpfs on the reporting machine, so a reboot empties it, and a session paused across a reboot would never get its scratchpad back. Archive and sweep still no-op without a root. |
| 4 | **The sweep skips any name that also starts with another known config dir's encoded worktrees prefix.** The known dirs are every registered workspace plus the global dir. Claude's encoding is lossy: `/x/foo_bar/.loom` and `/x/foo-bar/.loom` encode identically. A workspace can also be registered inside another's worktrees dir. Without this check, workspace A's check 4 (which walks only A's disk) could archive workspace B's live session dir. `SweepClaudeTemp` therefore takes a third argument, `otherConfigDirs`. |
| 5 | **The sweep never takes a truncated name** (longer than `claudetmp.MaxDirName`, 200). Its tail is Claude's hash, so checks 2–4 cannot compare it. Pause and Kill still archive such dirs through `Locate`'s unique-prefix match. |
| 6 | **Check 4 walks every `worktrees*` directory in the config dir, not just `worktrees/`.** A look-alike such as `.loom/worktrees-backup/u/x_<ts>` passes checks 1 and 2: its encoding starts with `…-loom-worktrees-` and ends in a timestamp. Only an on-disk check can keep it if a session runs there. |
| 7 | **`Locate` requires exactly one match across both name forms** (physical and as-stored, exact or truncated prefix). Two matches are ambiguous, so it reports not found. |
| 8 | **The sweep dispatches on the next health tick (≤3 s) after a load queues it.** The load paths don't return Cmds uniformly (`loadSlotStorage` returns a summary). The claim set is snapshotted when the load queues the sweep, as the spec says. A session created in between is protected by check 4. |
| 9 | **`claudetmp.Archive` returns a `Result`** (bytes in, bytes out, cache bytes skipped) for the `claudetmp.archived` log line. **`ArchiveAs`** wraps it with the spec's demote-on-collision rule, and Pause, Kill and the sweep all go through it. |

Out of scope (the spec's non-goals): Claude's transcript dirs (`<config>/projects/`), files agents wrote straight into the temp root, temp dirs of cwds outside loom's worktrees (workspace terminals included), a `TMPDIR` set only inside a profile's program string, and archive pruning.

**Rollout note.** The first start after this lands sweeps the existing backlog: on the reporting machine, about 6 GB for one repository alone. The sweep compresses at `flate.BestSpeed` off the Update goroutine with one sweep in flight, so expect tens of seconds of background CPU per workspace, and disk usage under `<repo>/.loom/archive/claude-tmp/` grows by the compressed size.

## Packages

| Package | Delivers | Commit |
|---|---|---|
| **A** | `git.RefuseLocked` + `LockedError`; its three callers; `reconcileOrphans` logs locked orphans at debug | `fix(git): remove worktrees an interrupted add left locked` |
| **B** | `session/claudetmp` (locate, archive, restore), `testenv` isolates `CLAUDE_CODE_TMPDIR`, opt-in contract test | `feat(claudetmp): locate, archive and restore Claude's per-session temp dirs` |
| **C** | `session/claude_tmp.go`: archive in Pause and Kill, restore in Resume, `SweepClaudeTemp` | `feat(session): archive Claude's temp dir on pause and kill, restore on resume` |
| **D** | `gateClaudeTmp` + the queue in `reconcileOrphans` + health-tick dispatch, `loom debug`, CLAUDE.md, USAGE.md, full verification | `feat(app): sweep orphaned Claude temp dirs into archives at workspace load` |

A package is one unit of work: one implementer, one review, one commit (plus any fixups the review asks for). Its numbered subsections (A1, A2, …) are checkpoints inside it, not commits. Do them in order.

## Conventions for every package

- Run Go commands from the worktree root. Plain tests need `CGO_ENABLED=0`, e.g. `CGO_ENABLED=0 go test ./session/git/...`.
- Race detector: `CC=clang CGO_ENABLED=1 go test -race ./session/... ./app/...`.
- Format only tracked non-vendor files plus new ones: `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') <new files>`. Never `gofmt -w .` (it rewrites `vendor/`).
- The local golangci-lint is v2 while the repo config is v1-shaped: use `go vet ./...` instead. CI's revive `exported` rule requires a doc comment starting with the identifier's name on every exported identifier, methods included.
- Commit messages end with the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Never run `./loom` (the TUI) directly. `loom debug` is a plain CLI subcommand and safe to run with throwaway `LOOM_HOME`/`LOOM_GLOBAL_DIR`.
- **Never touch the real Claude temp root** (`/tmp/claude-<uid>`): it holds live sessions' scratchpads, including the implementer's own. Every test that needs a root sets `CLAUDE_CODE_TMPDIR` to a `t.TempDir()`. Nothing in this plan should ever run `SweepClaudeTemp`, `Archive` or `Restore` against an unset `CLAUDE_CODE_TMPDIR` outside a test binary.
- Never use bare `git stash` (the stash stack is shared across worktrees).

## File structure

**New**
| File | Responsibility |
|---|---|
| `session/git/worktree_lock.go` | `unlockStaleInit`, `RefuseLocked`, `LockedError`, `ErrWorktreeLocked` |
| `session/git/worktree_lock_test.go` | Lock helper + Cleanup/Remove/Setup lock tests (real git) |
| `session/claudetmp/locate.go` | `Root`, `encode`, `DirName`, `Name`, `Names`, `Locate`, `WorktreePrefixes`, `ArchiveName`, `MaxDirName`, `EnvTmpDir` |
| `session/claudetmp/archive.go` | `Manifest`, `Result`, `Archive`, `ArchiveAs`, `ArchiveDir`, `Parked`, `DirSize` |
| `session/claudetmp/restore.go` | `Restore` |
| `session/claudetmp/testmain_test.go` | log init |
| `session/claudetmp/locate_test.go`, `archive_test.go`, `archive_unix_test.go`, `restore_test.go` | Unit tests |
| `session/claude_tmp_realclaude_test.go` | Opt-in contract test against a real interactive Claude in a private tmux server (amendment B-1) |
| `session/claude_tmp.go` | `archiveClaudeTemp`, `archiveClaudeTempDir`, `restoreClaudeTemp`, `SweepClaudeTemp` |
| `session/claude_tmp_test.go` | Pause/Kill/Resume wiring tests and sweep tests |
| `app/claude_tmp.go` | `claudeTmpJob`, `requestClaudeTmpSweep`, `knownConfigDirs`, `maybeClaudeTmpSweep`, `claudeTmpSweepCmd` |
| `app/claude_tmp_test.go` | Queue/dispatch tests |

**Modified**
| File | Change |
|---|---|
| `session/git/worktree_ops.go` | `cleanup()` and `Remove()` call `RefuseLocked` |
| `session/orphan.go` | `RemoveOrphanWorktree` calls `RefuseLocked` |
| `session/orphan_removal_test.go` | Lock tests for the orphan path |
| `session/instance.go` | Pause/Kill archive, Resume restores |
| `session/notice.go` | Doc: a failed scratchpad restore is a Notice too |
| `app/app.go` | `reconcileOrphans`: locked orphan at debug; queue the sweep; health tick dispatches it; `claudeTmpPending` field |
| `app/pollgate.go` | `gateClaudeTmp` kind, name, interval comment, `redispatch` case |
| `app/recovery_test.go` | Locked-orphan reconcile test |
| `internal/testenv/testenv.go` (+ `_test.go`) | `CLAUDE_CODE_TMPDIR` isolation |
| `main.go` | `loom debug` prints the temp root and archive dir |
| `CLAUDE.md`, `USAGE.md` | Docs |

---

## Package A: Locked worktrees

> **Review amendment A-1 (2026-10-07, binding).** When a stale "initializing" lock could not be removed, `LockedError` must not say an add "may still be checking it out" or that loom will unlock it later. `RefuseLocked` passes the unlock failure on: add an `UnlockErr error` field to `LockedError`. With it set, the message reads: the tree is locked "initializing" by an interrupted `git worktree add`, loom could not unlock it (`<err>`), and run `git -C … worktree unlock …`. `Unwrap` returns `UnlockErr`, and `Is(ErrWorktreeLocked)` still holds. Format the staleness age in whole minutes ("6 minutes"), not "6m0s". Extend `TestUnlockStaleInit_FailedUnlockKeepsTheLock` and `TestLockedError_Message` to cover both.

**Files:** create `session/git/worktree_lock.go`, `session/git/worktree_lock_test.go`; modify `session/git/worktree_ops.go`, `session/orphan.go`, `session/orphan_removal_test.go`, `app/app.go`, `app/recovery_test.go`.

### A1. The helper (TDD)

- [ ] **Step 1: Write the failing tests** in `session/git/worktree_lock_test.go`. They reuse this package's existing test helpers: `setupTestRepoWithWorktree` and `runGit` (`worktree_ops_test.go`), `adminDir` and `hookRunner` (`worktree_inspect_test.go`), `lockInitializing` (`worktree_hardening_test.go`), and `branchExists` (`worktree_ops_test.go`).

```go
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backdateLock ages worktreePath's lock file past staleInitLockAge, as if
// the `git worktree add` that wrote it was killed long ago.
func backdateLock(t *testing.T, worktreePath string) {
	t.Helper()
	old := time.Now().Add(-staleInitLockAge() - time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(adminDir(t, worktreePath), "locked"), old, old))
}

// TestUnlockStaleInit_RemovesAStaleInitializingLock: the kermit case — a
// tree locked "initializing" for days by an add loom killed at its deadline.
func TestUnlockStaleInit_RemovesAStaleInitializingLock(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)

	state, reason, err := unlockStaleInit(repoDir, worktreePath, nil)

	require.NoError(t, err)
	assert.Equal(t, unlocked, state)
	assert.Equal(t, "initializing", reason)
	assert.NoFileExists(t, filepath.Join(adminDir(t, worktreePath), "locked"))
}

// TestUnlockStaleInit_KeepsAFreshInitializingLock: a lock younger than the
// add deadline may belong to an add still checking out.
func TestUnlockStaleInit_KeepsAFreshInitializingLock(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)

	state, reason, err := unlockStaleInit(repoDir, worktreePath, nil)

	require.NoError(t, err)
	assert.Equal(t, lockKept, state)
	assert.Equal(t, "initializing", reason)
	assert.FileExists(t, filepath.Join(adminDir(t, worktreePath), "locked"))
}

// TestUnlockStaleInit_KeepsAUserLock: age never unlocks a lock the user set.
func TestUnlockStaleInit_KeepsAUserLock(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	runGit(t, repoDir, "worktree", "lock", "--reason", "on a removable disk", worktreePath)
	backdateLock(t, worktreePath)

	state, reason, err := unlockStaleInit(repoDir, worktreePath, nil)

	require.NoError(t, err)
	assert.Equal(t, lockKept, state)
	assert.Equal(t, "on a removable disk", reason)
}

func TestUnlockStaleInit_NotLocked(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)

	state, reason, err := unlockStaleInit(repoDir, worktreePath, nil)

	require.NoError(t, err)
	assert.Equal(t, notLocked, state)
	assert.Empty(t, reason)
}

// TestUnlockStaleInit_GuttedTreeRunsNothing: git run inside a tree with no
// .git answers for whatever repo encloses it, so nothing runs there.
func TestUnlockStaleInit_GuttedTreeRunsNothing(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)
	lock := filepath.Join(adminDir(t, worktreePath), "locked")
	require.NoError(t, os.Remove(filepath.Join(worktreePath, ".git")))
	ran := false
	r := hookRunner{before: func(*exec.Cmd) error { ran = true; return nil }}

	state, _, err := unlockStaleInit(repoDir, worktreePath, r)

	require.NoError(t, err)
	assert.Equal(t, notLocked, state)
	assert.False(t, ran, "no git command runs inside a gutted tree")
	assert.FileExists(t, lock, "a gutted tree keeps its existing handling")
}

// TestCleanup_RemovesATreeWithAStaleInitializingLock: Kill.
func TestCleanup_RemovesATreeWithAStaleInitializingLock(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)
	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", false, configDir)

	require.NoError(t, gw.Cleanup())

	assert.NoDirExists(t, worktreePath)
	assert.False(t, branchExists(t, repoDir, branchName), "Kill deletes a branch the session created")
}

// TestRemove_RemovesATreeWithAStaleInitializingLock: Pause.
func TestRemove_RemovesATreeWithAStaleInitializingLock(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)
	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)

	require.NoError(t, gw.Remove())

	assert.NoDirExists(t, worktreePath)
	assert.True(t, branchExists(t, repoDir, branchName), "Pause keeps the branch")
}

// TestCleanupAndRemove_RefuseARespectedLock: a lock loom respects stops
// Kill and Pause before anything is deleted, with an error naming the fix.
func TestCleanupAndRemove_RefuseARespectedLock(t *testing.T) {
	locks := []struct {
		name   string
		lock   func(t *testing.T, repoDir, wt string)
		reason string
	}{
		{"fresh initializing", func(t *testing.T, _, wt string) { lockInitializing(t, wt) }, "initializing"},
		{"user lock", func(t *testing.T, repoDir, wt string) {
			runGit(t, repoDir, "worktree", "lock", "--reason", "on a removable disk", wt)
			backdateLock(t, wt)
		}, "on a removable disk"},
	}
	ops := []struct {
		name string
		run  func(*GitWorktree) error
	}{
		{"Cleanup", (*GitWorktree).Cleanup},
		{"Remove", (*GitWorktree).Remove},
	}
	for _, lk := range locks {
		for _, op := range ops {
			t.Run(lk.name+"/"+op.name, func(t *testing.T) {
				configDir, repoDir, wt, branch := setupTestRepoWithWorktree(t)
				lk.lock(t, repoDir, wt)
				require.NoError(t, os.WriteFile(WorktreeTitleSidecarPath(wt), []byte("sess"), 0o644))
				gw := NewGitWorktreeFromStorage(repoDir, wt, "sess", branch, "", false, configDir)

				err := op.run(gw)

				require.ErrorIs(t, err, ErrWorktreeLocked)
				assert.Contains(t, err.Error(), "reason: "+lk.reason)
				assert.Contains(t, err.Error(), "worktree unlock")
				assert.DirExists(t, wt)
				assert.True(t, branchExists(t, repoDir, branch), "the branch stays with its tree")
				assert.FileExists(t, WorktreeTitleSidecarPath(wt), "and so does its title sidecar")
			})
		}
	}
}

// TestSetup_KeepsAStaleLockOfALiveTree: the stale-lock unlock belongs to
// the deleting paths only. A rebuild never unlocks an intact tree, however
// old its lock: the lock is what keeps `remove -f` off its work.
func TestSetup_KeepsAStaleLockOfALiveTree(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "work.txt"), []byte("uncommitted\n"), 0o644))

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)
	require.Error(t, gw.Setup())

	assert.FileExists(t, filepath.Join(worktreePath, "work.txt"))
	assert.FileExists(t, filepath.Join(adminDir(t, worktreePath), "locked"))
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/git/ -run 'UnlockStaleInit|StaleInitializingLock|RespectedLock|StaleLockOfALiveTree'`
Expected: build failure (`undefined: staleInitLockAge`, `unlockStaleInit`, `unlocked`, `ErrWorktreeLocked`, …).

- [ ] **Step 3: Implement `session/git/worktree_lock.go`**

```go
package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
)

// lockState is what unlockStaleInit found, and did, about a worktree's lock.
type lockState int

const (
	// notLocked: no lock git would honour, or no .git at all (a gutted
	// tree keeps its existing handling).
	notLocked lockState = iota
	// unlocked: the "initializing" lock an interrupted `git worktree add`
	// leaves, old enough that no add can still be running; removed.
	unlocked
	// lockKept: a lock loom respects — a user's `git worktree lock`, or an
	// "initializing" lock young enough to belong to a running add.
	lockKept
)

// ErrWorktreeLocked matches (errors.Is) every *LockedError.
var ErrWorktreeLocked = errors.New("worktree is locked")

// LockedError is a removal refused because the worktree carries a lock
// loom respects. Its message names the command that lets loom remove it.
type LockedError struct {
	Repo, Path, Reason string
}

// Error implements error.
func (e *LockedError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "none given"
	}
	return fmt.Sprintf("worktree %s is locked (reason: %s); run `git -C %s worktree unlock %s` to let loom remove it",
		e.Path, reason, shellQuote(e.Repo), shellQuote(e.Path))
}

// Is makes errors.Is(err, ErrWorktreeLocked) hold for a *LockedError.
func (e *LockedError) Is(target error) bool { return target == ErrWorktreeLocked }

// shellQuote quotes s for a POSIX shell (see session.shellQuote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// staleInitLockAge is the age past which an "initializing" lock cannot
// belong to a running `git worktree add`: loom kills its own add at
// gitWorktreeAddTimeout, and the minute on top covers clock and timestamp
// slack. A func so tests that shorten gitWorktreeAddTimeout move it too.
func staleInitLockAge() time.Duration { return gitWorktreeAddTimeout + time.Minute }

// unlockStaleInit removes the "initializing" lock an interrupted `git
// worktree add` leaves on worktreePath once it is older than
// staleInitLockAge, and reports any other lock, with its reason, as
// lockKept. A tree with no .git is notLocked and nothing runs: git run
// inside a gutted tree under <repo>/.loom/worktrees answers for the
// enclosing repo. An error means the lock could not be checked (or, with
// lockKept, not removed).
func unlockStaleInit(repoPath, worktreePath string, r CommandRunner) (lockState, string, error) {
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		return notLocked, "", nil
	}
	r = defaultRunner(r)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := r.Output(internalexec.GitCommand(ctx, worktreePath, "rev-parse", "--absolute-git-dir"))
	if err != nil {
		return notLocked, "", fmt.Errorf("find the git dir of %s: %w", worktreePath, err)
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		return notLocked, "", fmt.Errorf("find the git dir of %s: git answered %q", worktreePath, gitDir)
	}
	lockPath := filepath.Join(gitDir, "locked")
	fi, err := os.Stat(lockPath)
	if errors.Is(err, fs.ErrNotExist) {
		return notLocked, "", nil
	}
	if err != nil {
		return notLocked, "", fmt.Errorf("check the lock of %s: %w", worktreePath, err)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return notLocked, "", fmt.Errorf("read the lock of %s: %w", worktreePath, err)
	}
	reason := strings.TrimSpace(string(data))
	// Loom's git runs with English messages, so its own lock text is literal.
	if reason != "initializing" || time.Since(fi.ModTime()) < staleInitLockAge() {
		return lockKept, reason, nil
	}
	uctx, ucancel := context.WithTimeout(context.Background(), gitTimeout)
	defer ucancel()
	if out, err := r.CombinedOutput(internalexec.GitCommand(uctx, repoPath, "worktree", "unlock", worktreePath)); err != nil {
		return lockKept, reason, fmt.Errorf("unlock %s: %s (%w)", worktreePath, strings.TrimSpace(string(out)), err)
	}
	log.WarnKV("git.worktree_stale_init_unlocked", "path", worktreePath, "locked_at", fi.ModTime().UTC().Format(time.RFC3339))
	return unlocked, reason, nil
}

// RefuseLocked readies worktreePath for deletion by a caller about to run
// `git worktree remove -f`, which refuses any locked tree: it removes a
// stale "initializing" lock (unlockStaleInit) and returns a *LockedError
// naming the remedy for any lock loom respects. nil means go ahead. A
// lock it could not check is logged and let through, since the remove
// still refuses a locked tree itself.
//
// Only the paths that mean to delete a tree call it — orphan auto-clean,
// Kill (cleanup) and Pause (Remove) — never clearWorktreePath: there an
// intact tree must stay protected by its lock from a rebuild's remove.
func RefuseLocked(repoPath, worktreePath string, r CommandRunner) error {
	state, reason, err := unlockStaleInit(repoPath, worktreePath, r)
	if err != nil {
		log.For("git").Debug("worktree.lock_check_failed", "path", worktreePath, "err", err.Error())
	}
	if state == lockKept {
		return &LockedError{Repo: repoPath, Path: worktreePath, Reason: reason}
	}
	return nil
}
```

- [ ] **Step 4: Wire `GitWorktree.cleanup` and `Remove`** in `session/git/worktree_ops.go`. In `cleanup`, replace the block

```go
	// Check if worktree path exists before attempting removal
	if _, err := os.Stat(g.worktreePath); err == nil {
		// Remove the worktree using git command
		if _, err := g.removeWorktree(); err != nil {
			errs = append(errs, err)
		}
```

with

```go
	// Check if worktree path exists before attempting removal
	if _, err := os.Stat(g.worktreePath); err == nil {
		// A lock loom respects stops the whole cleanup, before the branch
		// is deleted or the title sidecar dropped: the tree stays, and so
		// must what lets a later run recover it.
		if err := RefuseLocked(g.repoPath, g.worktreePath, g.runner); err != nil {
			return err
		}
		// Remove the worktree using git command
		if _, err := g.removeWorktree(); err != nil {
			errs = append(errs, err)
		}
```

and replace `Remove` with

```go
// Remove removes the worktree but keeps the branch. A lock loom respects
// refuses it with a *LockedError (see RefuseLocked).
func (g *GitWorktree) Remove() error {
	if err := RefuseLocked(g.repoPath, g.worktreePath, g.runner); err != nil {
		return err
	}
	// Remove the worktree using git command
	if _, err := g.removeWorktree(); err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	return nil
}
```

Leave `removeWorktree()` and `clearWorktreePath()` unchanged (spec: putting the unlock in the shared remove would strip the rebuild path's protection).

- [ ] **Step 5: Run the git tests**

Run: `CGO_ENABLED=0 go test ./session/git/...`
Expected: PASS (the new tests and every existing one; `TestRemove_NotBoundByTickTimeout` uses a non-existent path, so `RefuseLocked` runs no command there).

### A2. The orphan path (TDD)

- [ ] **Step 1: Write the failing tests**, appended to `session/orphan_removal_test.go` (add `"strings"`, `"time"` to its imports):

```go
// orphanWorktree makes a repo with branch "feature" checked out in a
// linked worktree, an orphan the auto-clean would remove.
func orphanWorktree(t *testing.T) (repo, wt string) {
	t.Helper()
	repo = t.TempDir()
	runGit(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")
	runGit(t, repo, "branch", "feature")
	wt = filepath.Join(t.TempDir(), "feature_wt")
	runGit(t, repo, "worktree", "add", wt, "feature")
	return repo, wt
}

// worktreeLockFile is the lock file of the linked worktree wt.
func worktreeLockFile(t *testing.T, wt string) string {
	t.Helper()
	gitfile, err := os.ReadFile(filepath.Join(wt, ".git"))
	require.NoError(t, err)
	return filepath.Join(strings.TrimSpace(strings.TrimPrefix(string(gitfile), "gitdir:")), "locked")
}

// TestRemoveOrphanWorktree_UnlocksAStaleInitializingLock: the kermit
// lubm-benchmark tree, which failed the sweep at every start.
func TestRemoveOrphanWorktree_UnlocksAStaleInitializingLock(t *testing.T) {
	repo, wt := orphanWorktree(t)
	lock := worktreeLockFile(t, wt)
	require.NoError(t, os.WriteFile(lock, []byte("initializing"), 0o644))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(lock, old, old))

	require.NoError(t, RemoveOrphanWorktree(repo, wt))
	assert.NoDirExists(t, wt)
}

func TestRemoveOrphanWorktree_KeepsAUserLockedTree(t *testing.T) {
	repo, wt := orphanWorktree(t)
	runGit(t, repo, "worktree", "lock", "--reason", "keep me", wt)

	err := RemoveOrphanWorktree(repo, wt)

	require.ErrorIs(t, err, git.ErrWorktreeLocked)
	assert.Contains(t, err.Error(), "keep me")
	assert.DirExists(t, wt)
}
```

And appended to `app/recovery_test.go` (add `"strings"`, `"time"` to its imports):

```go
// TestReconcileOrphans_Locks: a clean orphan left locked "initializing" by
// an add killed long ago is unlocked and cleaned; one the user locked is
// left alone and not counted as cleaned.
func TestReconcileOrphans_Locks(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")

	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "u")
	require.NoError(t, os.MkdirAll(userDir, 0o755))
	staleWT := filepath.Join(userDir, "stale_18be000000000001")
	keptWT := filepath.Join(userDir, "kept_18be000000000002")
	runGit(t, repo, "worktree", "add", "-b", "u/stale", staleWT)
	runGit(t, repo, "worktree", "add", "-b", "u/kept", keptWT)
	gitfile, err := os.ReadFile(filepath.Join(staleWT, ".git"))
	require.NoError(t, err)
	lock := filepath.Join(strings.TrimSpace(strings.TrimPrefix(string(gitfile), "gitdir:")), "locked")
	require.NoError(t, os.WriteFile(lock, []byte("initializing"), 0o644))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(lock, old, old))
	runGit(t, repo, "worktree", "lock", "--reason", "keep me", keptWT)

	sp := spinner.New()
	summary := (&home{}).reconcileOrphans(cfgDir, "true", ui.NewList(&sp), nil, cmd2.MakeExecutor())

	assert.Equal(t, 1, summary.cleaned, "only the stale-locked orphan is cleaned")
	assert.Zero(t, summary.review)
	assert.NoDirExists(t, staleWT)
	assert.DirExists(t, keptWT, "a lock the user set is respected")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/ -run RemoveOrphanWorktree && CGO_ENABLED=0 go test ./app/ -run TestReconcileOrphans_Locks`
Expected: FAIL. The stale-lock remove fails with `cannot remove a locked working tree`; the user-lock error is not `ErrWorktreeLocked`.

- [ ] **Step 3: Implement.** In `session/orphan.go`, append this paragraph to `RemoveOrphanWorktree`'s doc comment:

```go
//
// A stale "initializing" lock (an interrupted `git worktree add`) is
// removed first; any lock loom respects returns a *git.LockedError
// (errors.Is git.ErrWorktreeLocked) with the tree untouched.
```

and insert these lines as the first statements of its body, above the existing `// Not orphanProbeTimeout:` comment (the rest of the body stays as it is):

```go
	if err := git.RefuseLocked(repoPath, worktreePath, nil); err != nil {
		return err
	}
```

In `app/app.go` `reconcileOrphans`, replace

```go
			if err := session.RemoveOrphanWorktree(cand.RepoPath, cand.WorktreePath); err != nil {
				log.For("app").Warn("orphan_autoclean_failed", "worktree", cand.WorktreePath, "err", err)
				continue
			}
```

with

```go
			if err := session.RemoveOrphanWorktree(cand.RepoPath, cand.WorktreePath); err != nil {
				// A lock loom respects is the user's call and lasts across
				// starts: a debug line, not a warning at every load.
				if errors.Is(err, git.ErrWorktreeLocked) {
					log.For("app").Debug("orphan_autoclean_locked", "worktree", cand.WorktreePath, "err", err)
				} else {
					log.For("app").Warn("orphan_autoclean_failed", "worktree", cand.WorktreePath, "err", err)
				}
				continue
			}
```

(`errors` and `session/git` are already imported in `app/app.go`.)

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/... ./app/...`
Expected: PASS.

### A3. Verify and commit

- [ ] **Step 1:** `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/') session/git/worktree_lock.go session/git/worktree_lock_test.go` prints nothing; `go vet ./session/... ./app/...` is clean; `CC=clang CGO_ENABLED=1 go test -race ./session/git/... ./session/ -run 'Lock|Orphan'` passes.
- [ ] **Step 2: Commit**

```bash
git add session/git/worktree_lock.go session/git/worktree_lock_test.go session/git/worktree_ops.go \
  session/orphan.go session/orphan_removal_test.go app/app.go app/recovery_test.go
git commit -m "fix(git): remove worktrees an interrupted add left locked

A git worktree add killed mid-checkout leaves its tree locked
\"initializing\" forever, and git worktree remove -f refuses a locked tree,
so the orphan sweep failed on it at every start. RefuseLocked, called only
by the paths that mean to delete a tree (orphan auto-clean, Kill, Pause),
removes that lock once it is older than the add deadline and refuses any
other lock with an error naming git worktree unlock. A locked orphan is
logged at debug, not warned about at every load.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Package B: `session/claudetmp`

> **Review amendments B-2 to B-8 (2026-10-07, binding; from the Package B review; supersede the code below where they differ).**
> - **B-2 (Archive is all-or-nothing on src's name).** Once the zip is renamed into place (and the archive dir fsynced, B-6), Archive renames src to a tombstone `<dir of src>/.loom-trash-<pid>-<unix-nano hex>`: same directory, so the rename is atomic. It then `removeTree`s the tombstone. A tombstone it cannot fully delete (say root-owned files an agent's container left) is logged at warn (`claudetmp.trash_kept`, with its path), and Archive still succeeds: src's live name is gone, and no `Locate` or sweep prefix matches a `.loom-trash-` name. If the rename itself fails, Archive removes the zip it just wrote and returns an error with src untouched. If that removal also fails, the error says the zip is left at its path. So success means the zip is in place and src's name is gone; an error means src is intact under its name and no new zip exists. Tests: (a) after a successful Archive, neither src nor any `.loom-trash-*` remains beside it; (b) with src's parent made read-only (0555; `t.Skip` when `os.Geteuid() == 0`), Archive returns an error, src is intact, and neither the zip nor its `.partial` exists. Restore the parent's mode in `t.Cleanup`.
> - **B-3 (a truncated name never matches).** `Name.Matches` returns false for a `Truncated` name. Its tail is Claude's hash of the full path, so two sessions whose encodings share their first 200 characters can't be told apart, and `Locate` could otherwise hand Pause or Kill another, running session's dir. `Locate` and `Parked` therefore never return a truncated match, and such temp dirs are left alone, as the sweep already leaves them. Update the doc comments of `Name`, `Matches`, `Names`, `Locate` and `Parked`. Replace `TestLocate_TruncatedNameMatchesOnlyAUniquePrefix` with `TestLocate_NeverMatchesATruncatedName`: a dir `prefix+"abc123"` exists, and Locate reports not found. `Restore` still recreates exactly the manifest's `dir_name` (keep `TestRestore_UsesTheManifestsName`). This supersedes Decision 7's "exact or truncated prefix" and the spec's "a prefix must match exactly one directory".
> - **B-4 (the manifest is the last entry of its name).** `readManifest` takes the **last** `.loom-archive.json` entry, which Archive always writes last, and `extract` skips only that entry. A user file of that name at src's top level then round-trips as content and can't stand in for the manifest. Test: src holding a top-level `.loom-archive.json` of `{"dir_name":"-other"}` restores under its real name, with that file intact and no `-other` dir.
> - **B-5.** `Root` skips a non-absolute value of any of the four variables and moves on to the next one.
> - **B-6.** After renaming the zip into place, Archive fsyncs the archive dir (best-effort: open, `Sync`, `Close`, errors ignored).
> - **B-7.** `IsolateLoomDirs` creates the (empty) `claude-tmp` dir, so a real Claude launched by an opt-in test finds an existing parent. `Root()` stays absent, since `claude-tmp/claude-<uid>` is not created. Extend `TestIsolateLoomDirs` to assert the dir exists.
> - **B-8.** `Restore`'s error texts quote zip entry names with `%q`, so control characters can't reach the TUI.
> - **B-9 (a unique partial file per archive).** Two processes archiving the same dir (two looms on one workspace with different global dirs) would both open `<name>.zip.partial` with `O_TRUNC` and interleave their writes. The first to finish would rename that corrupt file into place and delete src. So Archive writes to its own temp file in the archive dir, `os.CreateTemp(dir, <zip base>+".*.partial")` (mode 0600; `CreateTemp` already creates 0600), and renames that into place. Keep the `openArchiveFile` test seam, adapted to the new call shape. `Parked` already ignores such names, since they contain ".". Update the doc comment and the tests that assert `zipPath+".partial"` is absent: assert no `*.partial` remains in the dir instead.

**Files:** create everything under `session/claudetmp/`; modify `internal/testenv/testenv.go`, `internal/testenv/testenv_test.go`.

The package imports only the standard library and `github.com/aidan-bailey/loom/log` (which has no loom dependencies), so it does not reach `config`, and `TestEveryConfigReachingPackageIsolatesLoomDirs` does not require a `TestMain` for it. Its tests set `CLAUDE_CODE_TMPDIR` themselves.

### B1. Locating (TDD)

- [ ] **Step 1: Write `session/claudetmp/testmain_test.go`**

```go
package claudetmp

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/log"
)

func TestMain(m *testing.M) {
	_ = log.Initialize("", false)
	code := m.Run()
	log.Close()
	os.Exit(code)
}
```

- [ ] **Step 2: Write the failing tests** in `session/claudetmp/locate_test.go`:

```go
package claudetmp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func evalSymlinks(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}

// TestEnvNameMatchesTestenv: testenv cannot import this package, so it
// spells the variable itself.
func TestEnvNameMatchesTestenv(t *testing.T) {
	assert.Equal(t, EnvTmpDir, testenv.EnvClaudeTmpDir)
}

// TestDirName_TheReportsExample is the encoding read from Claude 2.1.288.
func TestDirName_TheReportsExample(t *testing.T) {
	name, truncated := DirName("/tb/Source/Academia/kermit/.loom/worktrees/aidanb/tmp-analysis_18db9e2ddd36ba14")
	assert.False(t, truncated)
	assert.Equal(t, "-tb-Source-Academia-kermit--loom-worktrees-aidanb-tmp-analysis-18db9e2ddd36ba14", name)
}

// TestDirName_CountsUTF16Units: Claude's regex replaces UTF-16 code units,
// so a character outside the BMP becomes two dashes.
func TestDirName_CountsUTF16Units(t *testing.T) {
	name, _ := DirName("/a/é/😀")
	assert.Equal(t, "-a-----", name)
}

func TestDirName_TruncatesPast200(t *testing.T) {
	name, truncated := DirName("/" + strings.Repeat("x", 250))
	assert.True(t, truncated)
	assert.Equal(t, "-"+strings.Repeat("x", 199)+"-", name)

	name, truncated = DirName("/" + strings.Repeat("x", 199))
	assert.False(t, truncated, "exactly 200 characters is kept as is")
	assert.Len(t, name, 200)
}

func TestRoot_Precedence(t *testing.T) {
	uid := fmt.Sprintf("claude-%d", os.Getuid())
	bases := []string{t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()}
	for _, b := range bases {
		require.NoError(t, os.Mkdir(filepath.Join(b, uid), 0o700))
	}
	set := func(claude, tmpdir, tmp, temp string) {
		t.Setenv(EnvTmpDir, claude)
		t.Setenv("TMPDIR", tmpdir)
		t.Setenv("TMP", tmp)
		t.Setenv("TEMP", temp)
	}
	for i, vars := range [][4]string{
		{bases[0], bases[1], bases[2], bases[3]},
		{"", bases[1], bases[2], bases[3]},
		{"", "", bases[2], bases[3]},
		{"", "", "", bases[3]},
	} {
		set(vars[0], vars[1], vars[2], vars[3])
		root, ok := Root()
		assert.True(t, ok)
		assert.Equal(t, evalSymlinks(t, filepath.Join(bases[i], uid)), root, "case %d", i)
	}

	set("", "", "", "")
	root, ok := Root()
	want := filepath.Join("/tmp", uid)
	if ok {
		want = evalSymlinks(t, want)
	}
	assert.Equal(t, want, root, "with nothing set the root is under /tmp")
}

func TestRoot_ReportsAnAbsentRoot(t *testing.T) {
	t.Setenv(EnvTmpDir, t.TempDir())
	_, ok := Root()
	assert.False(t, ok)
}

// TestLocate_ThroughASymlinkedParentAfterTheLeafIsGone: loom stores the
// worktree path through ~/Source, a symlink to /tb/Source; Claude names
// its dir after the physical path; and by the time loom looks, the
// worktree itself is gone.
func TestLocate_ThroughASymlinkedParentAfterTheLeafIsGone(t *testing.T) {
	root := t.TempDir()
	real := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(real, "wt"), 0o755))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	stored := filepath.Join(link, "wt", "feat_18be000000000001")
	phys, _ := DirName(filepath.Join(evalSymlinks(t, real), "wt", "feat_18be000000000001"))
	require.NoError(t, os.Mkdir(filepath.Join(root, phys), 0o700))

	dir, ok := Locate(root, stored)

	require.True(t, ok)
	assert.Equal(t, filepath.Join(root, phys), dir)
}

func TestLocate_MatchesTheStoredForm(t *testing.T) {
	root := t.TempDir()
	stored := "/no/such/parent/feat_18be000000000001"
	name, _ := DirName(stored)
	require.NoError(t, os.Mkdir(filepath.Join(root, name), 0o700))

	dir, ok := Locate(root, stored)

	require.True(t, ok)
	assert.Equal(t, filepath.Join(root, name), dir)
}

func TestLocate_TruncatedNameMatchesOnlyAUniquePrefix(t *testing.T) {
	root := t.TempDir()
	wt := "/" + strings.Repeat("w", 250)
	prefix, truncated := DirName(wt)
	require.True(t, truncated)
	require.NoError(t, os.Mkdir(filepath.Join(root, prefix+"abc123"), 0o700))

	dir, ok := Locate(root, wt)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(root, prefix+"abc123"), dir)

	require.NoError(t, os.Mkdir(filepath.Join(root, prefix+"def456"), 0o700))
	_, ok = Locate(root, wt)
	assert.False(t, ok, "two candidates for one truncated name are ambiguous")
}

func TestLocate_IgnoresSymlinks(t *testing.T) {
	root := t.TempDir()
	wt := "/no/such/feat_18be000000000001"
	name, _ := DirName(wt)
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, name)))

	_, ok := Locate(root, wt)
	assert.False(t, ok, "a symlink is never one of Claude's dirs")
}

func TestWorktreePrefixesAndArchiveName(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	prefixes := WorktreePrefixes(filepath.Join(link, ".loom"))
	phys := encode(filepath.Join(evalSymlinks(t, real), ".loom", "worktrees")) + "-"
	require.Len(t, prefixes, 2, "resolved and as given differ through the symlink")
	assert.Contains(t, prefixes, phys)

	assert.Equal(t, "aidanb-67-18daaecd490cb896", ArchiveName(phys+"aidanb-67-18daaecd490cb896", prefixes))
	assert.Equal(t, "x", ArchiveName("--x", nil), "a leading - never survives: unzip reads it as a flag")
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/claudetmp/`
Expected: build failure (package has no non-test files; `testenv.EnvClaudeTmpDir` undefined).

- [ ] **Step 4: Add the testenv constant and isolation** in `internal/testenv/testenv.go`. Replace the const block and `IsolateLoomDirs`:

```go
// The variables IsolateLoomDirs sets. EnvHome and EnvGlobalDir mirror
// config.EnvHome and config.EnvGlobalDir, and EnvClaudeTmpDir mirrors
// claudetmp.EnvTmpDir: this package cannot import those, so their tests
// assert the names agree.
const (
	EnvHome      = "LOOM_HOME"
	EnvGlobalDir = "LOOM_GLOBAL_DIR"
	// EnvClaudeTmpDir is Claude Code's temp root override. loom archives
	// and sweeps the per-session dirs under that root, so no test may see
	// the developer's real one, which holds live sessions' scratchpads.
	EnvClaudeTmpDir = "CLAUDE_CODE_TMPDIR"
)

// IsolateLoomDirs points LOOM_HOME, LOOM_GLOBAL_DIR and CLAUDE_CODE_TMPDIR
// at fresh paths under one new temporary directory, for the rest of the
// process. None of them is created, so Claude's root reads as absent and
// every archive operation is a no-op unless a test makes one. Call it from
// TestMain before m.Run and run the returned cleanup after it. Tests that
// need directories of their own still t.Setenv over these; the rest are
// isolated by default.
func IsolateLoomDirs() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "loom-test-dirs")
	if err != nil {
		return nil, fmt.Errorf("create isolated loom dirs: %w", err)
	}
	for name, sub := range map[string]string{EnvHome: "home", EnvGlobalDir: "global", EnvClaudeTmpDir: "claude-tmp"} {
		if err := os.Setenv(name, filepath.Join(dir, sub)); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("set %s: %w", name, err)
		}
	}
	return func() { _ = os.RemoveAll(dir) }, nil
}
```

In `internal/testenv/testenv_test.go` `TestIsolateLoomDirs`, add `t.Setenv(EnvClaudeTmpDir, "before-claude-tmp")` next to the other two `t.Setenv` lines, and after the existing `assert.DirExists` add:

```go
	claudeTmp := os.Getenv(EnvClaudeTmpDir)
	assert.Equal(t, filepath.Dir(home), filepath.Dir(claudeTmp), "Claude's temp root is isolated under the same dir")
```

- [ ] **Step 5: Implement `session/claudetmp/locate.go`**

```go
// Package claudetmp finds, archives and restores the per-session temp
// directory Claude Code keeps outside the worktree:
// <root>/<encoded cwd>/<session id>/{scratchpad,tasks}/. Loom removes a
// session's worktree on pause and kill; this package keeps the temp dir
// (on tmpfs, often) from outliving it.
//
// The layout is Claude's own and undocumented, read from its 2.1.288
// bundle (docs/superpowers/specs/2026-10-05-worktree-locks-and-claude-temp-archive-design.md).
// TestRealClaude_TempDirLayout (package session) pins it. Every operation fails closed:
// what loom cannot match exactly, it leaves alone.
//
// No tmux, UI, app or config dependency.
package claudetmp

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// EnvTmpDir is Claude Code's own override for its temp root's parent.
const EnvTmpDir = "CLAUDE_CODE_TMPDIR"

// MaxDirName is the longest encoded cwd Claude uses as a name as is. A
// longer one becomes its first MaxDirName characters, "-", and a hash of
// the cwd that loom cannot compute.
const MaxDirName = 200

// Root returns Claude's temp root for this user, symlinks resolved, and
// whether it exists as a directory: the first of $CLAUDE_CODE_TMPDIR,
// $TMPDIR, $TMP and $TEMP that is set (else /tmp), joined with
// claude-<uid>. When it does not exist, the path returned is unresolved.
func Root() (string, bool) {
	base := "/tmp"
	for _, v := range []string{EnvTmpDir, "TMPDIR", "TMP", "TEMP"} {
		if s := os.Getenv(v); s != "" {
			base = s
			break
		}
	}
	root := filepath.Join(base, fmt.Sprintf("claude-%d", os.Getuid()))
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root, false
	}
	fi, err := os.Stat(resolved)
	return resolved, err == nil && fi.IsDir()
}

// encode is Claude's encoding of a path as a directory name: every
// character outside [a-zA-Z0-9] becomes "-". Claude replaces UTF-16 code
// units, so a character outside the Basic Multilingual Plane becomes "--".
// The result is ASCII, so its byte length is its length to Claude.
func encode(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// DirName returns the directory name Claude gives a session whose cwd is
// physicalPath (symlinks already resolved: Claude uses its physical cwd).
// Past MaxDirName characters it returns the part loom can compute, the
// first MaxDirName characters and "-", with truncated set.
func DirName(physicalPath string) (name string, truncated bool) {
	n := encode(physicalPath)
	if len(n) <= MaxDirName {
		return n, false
	}
	return n[:MaxDirName] + "-", true
}

// Name is one directory name Claude may have used for a cwd. A Truncated
// name is a prefix: the real one continues with Claude's hash.
type Name struct {
	Value     string
	Truncated bool
}

// Matches reports whether dirName is the directory n names.
func (n Name) Matches(dirName string) bool {
	if !n.Truncated {
		return dirName == n.Value
	}
	return len(dirName) > len(n.Value) && strings.HasPrefix(dirName, n.Value)
}

// Names returns the names Claude may have used for a session whose cwd was
// path: the encoding of its physical path first (see physical), then of
// the path exactly as given, when that differs.
func Names(path string) []Name {
	var out []Name
	for _, p := range []string{physical(path), filepath.Clean(path)} {
		v, trunc := DirName(p)
		if n := (Name{Value: v, Truncated: trunc}); !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// physical resolves path's symlinks as far as it exists: the deepest
// existing ancestor is resolved and the missing components appended. A
// worktree is usually gone by the time loom looks for its temp dir, but
// its parents are not. (session.canonicalPath does the same; this package
// cannot import session.)
func physical(path string) string {
	p := filepath.Clean(path)
	rest := ""
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Clean(path)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// Locate returns Claude's temp dir under root for a session whose cwd was
// worktreePath. Exactly one directory may match one of Names(worktreePath);
// none or several report not found. Symlinks never match.
func Locate(root, worktreePath string) (string, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	names := Names(worktreePath)
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, n := range names {
			if n.Matches(e.Name()) {
				found = append(found, e.Name())
				break
			}
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return filepath.Join(root, found[0]), true
}

// WorktreePrefixes returns the prefix every temp dir name of configDir's
// sessions starts with: its worktrees directory, encoded, plus the "-" a
// separator encodes to. Resolved first, then as given when that differs.
func WorktreePrefixes(configDir string) []string {
	var out []string
	for _, d := range []string{physical(configDir), filepath.Clean(configDir)} {
		if p := encode(filepath.Join(d, "worktrees")) + "-"; !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// ArchiveName is the archive file name, without ".zip", for the temp dir
// dirName: dirName less the first of prefixes it starts with, and less any
// leading "-", which unzip would read as a flag.
func ArchiveName(dirName string, prefixes []string) string {
	name := dirName
	for _, p := range prefixes {
		if len(dirName) > len(p) && strings.HasPrefix(dirName, p) {
			name = dirName[len(p):]
			break
		}
	}
	if trimmed := strings.TrimLeft(name, "-"); trimmed != "" {
		return trimmed
	}
	return "session"
}
```

- [ ] **Step 6: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/claudetmp/ ./internal/testenv/`
Expected: PASS.

### B2. Archiving (TDD)

- [ ] **Step 1: Write the failing tests** in `session/claudetmp/archive_test.go`:

```go
package claudetmp

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTree lays out files (slash paths relative to dir) with contents.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
}

// zipEntries opens zipPath for the rest of the test and indexes its entries.
func zipEntries(t *testing.T, zipPath string) map[string]*zip.File {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	t.Cleanup(func() { zr.Close() })
	out := map[string]*zip.File{}
	for _, f := range zr.File {
		out[f.Name] = f
	}
	return out
}

func readZipEntry(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	require.NoError(t, err)
	defer rc.Close()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(data)
}

func readZipManifest(t *testing.T, zipPath string) Manifest {
	t.Helper()
	f := zipEntries(t, zipPath)[ManifestName]
	require.NotNil(t, f, "every archive carries a manifest")
	var m Manifest
	require.NoError(t, json.Unmarshal([]byte(readZipEntry(t, f)), &m))
	return m
}

func TestArchive_SkipsTaggedCachesKeepsMalformedTags(t *testing.T) {
	src := filepath.Join(t.TempDir(), "proj")
	tag := cacheDirSignature + "\n# cargo\n"
	writeTree(t, src, map[string]string{
		"s1/scratchpad/notes.md":            "notes",
		"s1/scratchpad/target/CACHEDIR.TAG": tag,
		"s1/scratchpad/target/debug/big":    strings.Repeat("x", 1000),
		"s1/scratchpad/fake/CACHEDIR.TAG":   "Signature: not the real one",
		"s1/scratchpad/fake/keep.txt":       "kept",
	})
	zipPath := filepath.Join(t.TempDir(), "archive", "proj.zip")

	res, err := Archive(src, zipPath, Manifest{Worktree: "/wt", Reason: "pause"})
	require.NoError(t, err)

	entries := zipEntries(t, zipPath)
	assert.Contains(t, entries, "s1/scratchpad/notes.md")
	assert.Contains(t, entries, "s1/scratchpad/fake/keep.txt", "a malformed tag is ordinary content")
	assert.NotContains(t, entries, "s1/scratchpad/target/debug/big")
	assert.NotContains(t, entries, "s1/scratchpad/target/CACHEDIR.TAG")
	m := readZipManifest(t, zipPath)
	assert.Equal(t, "proj", m.DirName)
	assert.Equal(t, src, m.Source)
	assert.Equal(t, "/wt", m.Worktree)
	assert.Equal(t, "pause", m.Reason)
	assert.False(t, m.ArchivedAt.IsZero())
	require.Len(t, m.SkippedCaches, 1)
	assert.Equal(t, "s1/scratchpad/target", m.SkippedCaches[0].Path)
	assert.EqualValues(t, 1000+len(tag), m.SkippedCaches[0].Bytes)
	assert.Equal(t, m.SkippedCaches[0].Bytes, res.CacheBytes)
	assert.NoDirExists(t, src, "the source goes once the zip is in place")
	fi, err := os.Stat(zipPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "scratchpads can hold secrets")
	assert.Equal(t, fi.Size(), res.BytesOut)
	dfi, err := os.Stat(filepath.Dir(zipPath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dfi.Mode().Perm())
}

// TestArchive_StoresSymlinksAsLinks: following a link could archive $HOME.
func TestArchive_StoresSymlinksAsLinks(t *testing.T) {
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"secret": "do not archive"})
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})
	require.NoError(t, os.Symlink(outside, filepath.Join(src, "s1", "scratchpad", "home")))
	zipPath := filepath.Join(t.TempDir(), "proj.zip")

	_, err := Archive(src, zipPath, Manifest{})
	require.NoError(t, err)

	entries := zipEntries(t, zipPath)
	link := entries["s1/scratchpad/home"]
	require.NotNil(t, link)
	assert.NotZero(t, link.Mode()&fs.ModeSymlink)
	assert.Equal(t, outside, readZipEntry(t, link))
	for name := range entries {
		assert.NotContains(t, name, "secret")
	}
	assert.FileExists(t, filepath.Join(outside, "secret"), "deleting the source never follows a link")
}

// TestArchive_DeletesReadOnlyDirectories: Go's module cache makes its own
// directories read-only, which a plain RemoveAll refuses.
func TestArchive_DeletesReadOnlyDirectories(t *testing.T) {
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/gomod/pkg@v1/file.go": "package p"})
	ro := []string{filepath.Join(src, "s1", "gomod", "pkg@v1"), filepath.Join(src, "s1", "gomod")}
	for _, d := range ro {
		require.NoError(t, os.Chmod(d, 0o555))
	}
	t.Cleanup(func() {
		for _, d := range ro {
			_ = os.Chmod(d, 0o755)
		}
	})

	_, err := Archive(src, filepath.Join(t.TempDir(), "proj.zip"), Manifest{})

	require.NoError(t, err)
	assert.NoDirExists(t, src)
}

// failingFile fails every write once more than left bytes have gone through.
type failingFile struct {
	*os.File
	left int
}

func (f *failingFile) Write(p []byte) (int, error) {
	if len(p) > f.left {
		return 0, errors.New("disk full")
	}
	f.left -= len(p)
	return f.File.Write(p)
}

func TestArchive_WriteFailureLeavesTheSourceAlone(t *testing.T) {
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/big": strings.Repeat("y", 1<<20)})
	orig := openArchiveFile
	openArchiveFile = func(name string) (archiveFile, error) {
		f, err := orig(name)
		if err != nil {
			return nil, err
		}
		return &failingFile{File: f.(*os.File), left: 100}, nil
	}
	t.Cleanup(func() { openArchiveFile = orig })
	zipPath := filepath.Join(t.TempDir(), "proj.zip")

	_, err := Archive(src, zipPath, Manifest{})

	require.Error(t, err)
	assert.NoFileExists(t, zipPath)
	assert.NoFileExists(t, zipPath+".partial")
	assert.FileExists(t, filepath.Join(src, "s1", "scratchpad", "big"))
}

func TestArchiveAs_DemotesAParkedZip(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "feat-18be000000000001.zip"), []byte("earlier"), 0o600))
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})

	zipPath, _, err := ArchiveAs(dir, "feat-18be000000000001", src, Manifest{})

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "feat-18be000000000001.zip"), zipPath)
	demoted, err := filepath.Glob(filepath.Join(dir, "feat-18be000000000001.*Z.zip"))
	require.NoError(t, err)
	require.Len(t, demoted, 1)
	data, err := os.ReadFile(demoted[0])
	require.NoError(t, err)
	assert.Equal(t, "earlier", string(data), "the earlier zip becomes a permanent archive")
	assert.Contains(t, zipEntries(t, zipPath), "s1/scratchpad/a.txt")
}

func TestParked_FindsOnlyTheUndemotedZip(t *testing.T) {
	dir := t.TempDir()
	wt := "/r/.loom/worktrees/u/feat_18be000000000001"
	prefixes := WorktreePrefixes("/r/.loom")
	n, _ := DirName(wt)
	name := ArchiveName(n, prefixes)
	require.Equal(t, "u-feat-18be000000000001", name)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".20261005T132900Z.zip"), nil, 0o600))

	_, ok := Parked(dir, wt, prefixes)
	assert.False(t, ok, "a demoted archive is never restored")

	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".zip"), nil, 0o600))
	got, ok := Parked(dir, wt, prefixes)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(dir, name+".zip"), got)
}
```

And `session/claudetmp/archive_unix_test.go`:

```go
//go:build unix

package claudetmp

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestArchive_SkipsFIFOs: opening a FIFO would block the archive forever.
func TestArchive_SkipsFIFOs(t *testing.T) {
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})
	require.NoError(t, syscall.Mkfifo(filepath.Join(src, "s1", "pipe"), 0o600))
	zipPath := filepath.Join(t.TempDir(), "proj.zip")

	_, err := Archive(src, zipPath, Manifest{})

	require.NoError(t, err)
	entries := zipEntries(t, zipPath)
	assert.Contains(t, entries, "s1/scratchpad/a.txt")
	assert.NotContains(t, entries, "s1/pipe")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/claudetmp/ -run 'Archive|Parked'`
Expected: build failure (`undefined: Archive`, `cacheDirSignature`, …).

- [ ] **Step 3: Implement `session/claudetmp/archive.go`**

```go
package claudetmp

import (
	"archive/zip"
	"compress/flate"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ManifestName is the archive entry describing where its content came from.
const ManifestName = ".loom-archive.json"

// Manifest is an archive's ManifestName entry. Restore recreates the
// directory under DirName exactly, which covers a truncated name whose
// hash loom cannot compute.
type Manifest struct {
	DirName       string         `json:"dir_name"`
	Source        string         `json:"source"`
	Worktree      string         `json:"worktree"`
	Reason        string         `json:"reason"`
	ArchivedAt    time.Time      `json:"archived_at"`
	SkippedCaches []SkippedCache `json:"skipped_caches,omitempty"`
}

// SkippedCache is a cache directory Archive left out (slash path relative
// to the archived dir) and the bytes it held.
type SkippedCache struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// Result is what one Archive did, for the log.
type Result struct {
	BytesIn    int64 // regular-file bytes archived
	BytesOut   int64 // the zip's size
	CacheBytes int64 // bytes in the cache dirs left out
}

// cacheDirSignature opens every valid CACHEDIR.TAG
// (https://bford.info/cachedir/): cargo's target/, among others.
const cacheDirSignature = "Signature: 8a477f597d28d172789f06886806bc55"

// ArchiveDir is where configDir's archives live.
func ArchiveDir(configDir string) string {
	return filepath.Join(configDir, "archive", "claude-tmp")
}

// archiveFile is what Archive writes the zip through.
type archiveFile interface {
	io.Writer
	Sync() error
	Close() error
}

// openArchiveFile opens the .partial file Archive writes, truncating a
// leftover from a crash. A var so a test can make writing fail half-way.
var openArchiveFile = func(name string) (archiveFile, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
}

// Archive zips src into zipPath (mode 0600, its dir 0700) and then deletes
// src.
//
// Symlinks are stored as links and never followed (following one could
// archive $HOME). Sockets, FIFOs and devices are skipped, and so is every
// directory holding a valid CACHEDIR.TAG; m.SkippedCaches lists those.
// m.DirName, m.Source and m.ArchivedAt are filled in when empty.
//
// The zip is written to zipPath+".partial" and renamed into place only
// once complete and synced; src is deleted only after that rename. On any
// error before it, the partial file is removed and src left as it was. A
// src that cannot be deleted after the rename returns an error with the
// zip in place.
func Archive(src, zipPath string, m Manifest) (Result, error) {
	if m.DirName == "" {
		m.DirName = filepath.Base(src)
	}
	if m.Source == "" {
		m.Source = src
	}
	if m.ArchivedAt.IsZero() {
		m.ArchivedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o700); err != nil {
		return Result{}, fmt.Errorf("create the archive dir: %w", err)
	}
	partial := zipPath + ".partial"
	f, err := openArchiveFile(partial)
	if err != nil {
		return Result{}, fmt.Errorf("create %s: %w", partial, err)
	}
	fail := func(err error) (Result, error) {
		_ = f.Close()
		_ = os.Remove(partial)
		return Result{}, err
	}
	var res Result
	zw := zip.NewWriter(f)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	if err := addTree(zw, src, &m, &res); err != nil {
		return fail(fmt.Errorf("archive %s: %w", src, err))
	}
	if err := addManifest(zw, m); err != nil {
		return fail(fmt.Errorf("write the manifest: %w", err))
	}
	if err := zw.Close(); err != nil {
		return fail(fmt.Errorf("finish %s: %w", partial, err))
	}
	if err := f.Sync(); err != nil {
		return fail(fmt.Errorf("sync %s: %w", partial, err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(partial)
		return Result{}, fmt.Errorf("close %s: %w", partial, err)
	}
	if err := os.Rename(partial, zipPath); err != nil {
		_ = os.Remove(partial)
		return Result{}, fmt.Errorf("move %s into place: %w", zipPath, err)
	}
	if fi, err := os.Stat(zipPath); err == nil {
		res.BytesOut = fi.Size()
	}
	if err := removeTree(src); err != nil {
		return res, fmt.Errorf("archived to %s, but could not delete %s: %w", zipPath, src, err)
	}
	return res, nil
}

// addTree adds src's content to zw, named by slash paths relative to src.
// WalkDir never follows symlinks.
func addTree(zw *zip.Writer, src string, m *Manifest, res *Result) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == src {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			hdr := &zip.FileHeader{Name: name, Method: zip.Store, Modified: info.ModTime()}
			hdr.SetMode(mode)
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			_, err = io.WriteString(w, target)
			return err
		case mode.IsDir():
			if isCacheDir(p) {
				n := treeSize(p)
				m.SkippedCaches = append(m.SkippedCaches, SkippedCache{Path: name, Bytes: n})
				res.CacheBytes += n
				return fs.SkipDir
			}
			hdr := &zip.FileHeader{Name: name + "/", Modified: info.ModTime()}
			hdr.SetMode(mode)
			_, err := zw.CreateHeader(hdr)
			return err
		case mode.IsRegular():
			return addFile(zw, p, name, info, res)
		default: // sockets, FIFOs, devices
			return nil
		}
	})
}

// addFile adds the regular file p. It checks the opened file is still the
// one WalkDir saw, so a file swapped for a symlink since is never followed.
func addFile(zw *zip.Writer, p, name string, info fs.FileInfo, res *Result) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !os.SameFile(info, fi) {
		return fmt.Errorf("%s changed while being archived", p)
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = name
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	n, err := io.Copy(w, f)
	res.BytesIn += n
	return err
}

// addManifest writes m as the archive's ManifestName entry.
func addManifest(zw *zip.Writer, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	hdr := &zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: m.ArchivedAt}
	hdr.SetMode(0o600)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// isCacheDir reports whether dir holds a regular CACHEDIR.TAG that starts
// with the spec's signature. A malformed tag is ordinary content.
func isCacheDir(dir string) bool {
	tag := filepath.Join(dir, "CACHEDIR.TAG")
	if fi, err := os.Lstat(tag); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(tag)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(cacheDirSignature))
	if _, err := io.ReadFull(f, buf); err != nil {
		return false
	}
	return string(buf) == cacheDirSignature
}

// treeSize sums the regular files under dir, for the log.
func treeSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// removeTree deletes dir. A tree with read-only directories (Go's module
// cache makes its own so) refuses RemoveAll, so on failure it gives the
// owner full permission on every directory and tries once more. WalkDir
// visits a directory before reading it, so the chmod lands in time.
func removeTree(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			if info, err := d.Info(); err == nil {
				_ = os.Chmod(p, info.Mode().Perm()|0o700)
			}
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// ArchiveAs archives src (see Archive) as <dir>/<name>.zip and returns
// that path. An existing <name>.zip, parked by a pause whose restore later
// failed, is first renamed to <name>.<UTC timestamp>.zip: a permanent
// archive that no resume restores.
func ArchiveAs(dir, name, src string, m Manifest) (string, Result, error) {
	zipPath := filepath.Join(dir, name+".zip")
	if _, err := os.Lstat(zipPath); err == nil {
		if err := os.Rename(zipPath, demotedPath(dir, name, time.Now())); err != nil {
			return zipPath, Result{}, fmt.Errorf("move the earlier %s aside: %w", zipPath, err)
		}
	}
	res, err := Archive(src, zipPath, m)
	return zipPath, res, err
}

// demotedPath is a free <dir>/<name>.<UTC timestamp>[-N].zip.
func demotedPath(dir, name string, now time.Time) string {
	stamp := now.UTC().Format("20060102T150405Z")
	p := filepath.Join(dir, fmt.Sprintf("%s.%s.zip", name, stamp))
	for n := 2; ; n++ {
		if _, err := os.Lstat(p); err != nil {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s.%s-%d.zip", name, stamp, n))
	}
}

// Parked returns the zip a pause parked for the session whose cwd was
// worktreePath: <dir>/<name>.zip, where name is ArchiveName of one of
// Names(worktreePath). Demoted archives carry a "." in their name and
// never match; a truncated name must match exactly one zip.
func Parked(dir, worktreePath string, prefixes []string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var wants []Name
	for _, n := range Names(worktreePath) {
		wants = append(wants, Name{Value: ArchiveName(n.Value, prefixes), Truncated: n.Truncated})
	}
	var found []string
	for _, e := range entries {
		base, ok := strings.CutSuffix(e.Name(), ".zip")
		if !ok || strings.Contains(base, ".") || !e.Type().IsRegular() {
			continue
		}
		for _, w := range wants {
			if w.Matches(base) {
				found = append(found, e.Name())
				break
			}
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return filepath.Join(dir, found[0]), true
}

// DirSize sums the sizes of the regular files directly in dir: the archive
// dir's total, for the log.
func DirSize(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil {
			n += info.Size()
		}
	}
	return n
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/claudetmp/`
Expected: PASS.

### B3. Restoring (TDD)

- [ ] **Step 1: Write the failing tests** in `session/claudetmp/restore_test.go`:

```go
package claudetmp

import (
	"archive/zip"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// archived archives a fresh temp dir named dirName under root and returns
// its path (now gone) and the zip.
func archived(t *testing.T, root, dirName string, files map[string]string) (src, zipPath string) {
	t.Helper()
	src = filepath.Join(root, dirName)
	writeTree(t, src, files)
	zipPath = filepath.Join(t.TempDir(), "a.zip")
	_, err := Archive(src, zipPath, Manifest{})
	require.NoError(t, err)
	require.NoDirExists(t, src)
	return src, zipPath
}

func TestRestore_RoundTripsContentModesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "-wt-feat-18be000000000001")
	writeTree(t, src, map[string]string{
		"s1/scratchpad/run.sh": "#!/bin/sh\n",
		"s1/tasks/out.log":     "log",
	})
	require.NoError(t, os.Chmod(filepath.Join(src, "s1", "scratchpad", "run.sh"), 0o755))
	require.NoError(t, os.Symlink("run.sh", filepath.Join(src, "s1", "scratchpad", "latest")))
	tasks := filepath.Join(src, "s1", "tasks")
	require.NoError(t, os.Chmod(tasks, 0o555))
	t.Cleanup(func() { _ = os.Chmod(tasks, 0o755) })
	zipPath := filepath.Join(t.TempDir(), "feat.zip")
	_, err := Archive(src, zipPath, Manifest{})
	require.NoError(t, err)
	require.NoDirExists(t, src)

	require.NoError(t, Restore(zipPath, root))

	assert.NoFileExists(t, zipPath, "a restored archive is deleted")
	data, err := os.ReadFile(filepath.Join(tasks, "out.log"))
	require.NoError(t, err)
	assert.Equal(t, "log", string(data))
	fi, err := os.Stat(filepath.Join(src, "s1", "scratchpad", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
	target, err := os.Readlink(filepath.Join(src, "s1", "scratchpad", "latest"))
	require.NoError(t, err)
	assert.Equal(t, "run.sh", target)
	fi, err = os.Stat(tasks)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o555), fi.Mode().Perm())
	assert.NoFileExists(t, filepath.Join(root, ManifestName), "the manifest is not restored")
}

// TestRestore_UsesTheManifestsName: a truncated name carries Claude's hash,
// which loom cannot compute, so only the manifest knows it.
func TestRestore_UsesTheManifestsName(t *testing.T) {
	root := t.TempDir()
	long := "-" + strings.Repeat("w", 199) + "-abc123"
	src, zipPath := archived(t, root, long, map[string]string{"s1/scratchpad/a": "a"})

	require.NoError(t, Restore(zipPath, root))
	assert.FileExists(t, filepath.Join(src, "s1", "scratchpad", "a"))
}

func TestRestore_RefusesAnExistingTarget(t *testing.T) {
	root := t.TempDir()
	src, zipPath := archived(t, root, "-wt-x", map[string]string{"s1/scratchpad/a": "a"})
	require.NoError(t, os.Mkdir(src, 0o700)) // Claude started again before the restore

	require.Error(t, Restore(zipPath, root))
	assert.FileExists(t, zipPath, "a refused restore keeps the archive")
	assert.NoFileExists(t, filepath.Join(src, "s1", "scratchpad", "a"))
}

// TestRestore_CreatesAMissingRoot: /tmp is often tmpfs, emptied by a reboot.
func TestRestore_CreatesAMissingRoot(t *testing.T) {
	_, zipPath := archived(t, t.TempDir(), "-wt-x", map[string]string{"s1/scratchpad/a": "a"})
	root := filepath.Join(t.TempDir(), "claude-1000")

	require.NoError(t, Restore(zipPath, root))
	assert.FileExists(t, filepath.Join(root, "-wt-x", "s1", "scratchpad", "a"))
	fi, err := os.Stat(root)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
}

type handEntry struct {
	name, body string
	symlink    bool
}

// handZip writes an archive by hand, the way a crafted one could look: a
// manifest naming dirName, then entries.
func handZip(t *testing.T, dirName string, entries []handEntry) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "hand.zip")
	f, err := os.Create(zipPath)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	m, err := json.Marshal(Manifest{DirName: dirName})
	require.NoError(t, err)
	w, err := zw.Create(ManifestName)
	require.NoError(t, err)
	_, err = w.Write(m)
	require.NoError(t, err)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name}
		if e.symlink {
			hdr.SetMode(fs.ModeSymlink | 0o777)
		} else {
			hdr.SetMode(0o644)
		}
		w, err := zw.CreateHeader(hdr)
		require.NoError(t, err)
		_, err = io.WriteString(w, e.body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	return zipPath
}

func TestRestore_RejectsEscapes(t *testing.T) {
	outside := t.TempDir()
	for _, tc := range []struct {
		name    string
		dirName string
		entries []handEntry
	}{
		{"dot-dot entry", "-wt-x", []handEntry{{name: "../x", body: "evil"}}},
		{"absolute entry", "-wt-x", []handEntry{{name: "/x", body: "evil"}}},
		{"file beneath an outside symlink", "-wt-x", []handEntry{
			{name: "link", body: outside, symlink: true},
			{name: "link/x", body: "evil"},
		}},
		{"manifest naming a path", "../x", []handEntry{{name: "a", body: "a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "root")
			require.NoError(t, os.Mkdir(root, 0o700))
			zipPath := handZip(t, tc.dirName, tc.entries)

			require.Error(t, Restore(zipPath, root))

			assert.NoDirExists(t, filepath.Join(root, "-wt-x"))
			assert.FileExists(t, zipPath, "a failed restore keeps the archive")
			staging, _ := filepath.Glob(filepath.Join(root, ".loom-restore-*"))
			assert.Empty(t, staging, "the staging dir is removed")
			assert.NoFileExists(t, filepath.Join(parent, "x"))
			assert.NoFileExists(t, filepath.Join(outside, "x"))
		})
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/claudetmp/ -run Restore`
Expected: build failure (`undefined: Restore`).

- [ ] **Step 3: Implement `session/claudetmp/restore.go`**

```go
package claudetmp

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/aidan-bailey/loom/log"
)

// Restore recreates the temp dir an archive holds under root, named by the
// manifest's exact dir_name, then deletes the zip. root is created (0700)
// when missing: /tmp is often tmpfs, which a reboot empties.
//
// It refuses when that directory already exists. Entries are extracted into
// a fresh sibling through an os.Root, which rejects "..", absolute paths and
// escapes through symlinks, and the sibling is renamed into place only once
// complete. On any error the sibling is removed and the zip kept.
func Restore(zipPath, root string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", zipPath, err)
	}
	err = restore(&zr.Reader, root)
	_ = zr.Close()
	if err != nil {
		return err
	}
	if err := os.Remove(zipPath); err != nil {
		log.For("claudetmp").Warn("claudetmp.restored_zip_kept", "zip", zipPath, "err", err.Error())
	}
	return nil
}

// restore is Restore with the zip open.
func restore(zr *zip.Reader, root string) error {
	m, err := readManifest(zr)
	if err != nil {
		return err
	}
	if !fs.ValidPath(m.DirName) || m.DirName == "." || strings.ContainsAny(m.DirName, `/\`) {
		return fmt.Errorf("the archive's manifest names an invalid directory %q", m.DirName)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", root, err)
	}
	target := filepath.Join(root, m.DirName)
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("%s already exists", target)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %s: %w", target, err)
	}
	staging, err := os.MkdirTemp(root, ".loom-restore-")
	if err != nil {
		return fmt.Errorf("create a staging dir under %s: %w", root, err)
	}
	if err := extract(zr, staging); err != nil {
		_ = removeTree(staging)
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		_ = removeTree(staging)
		return fmt.Errorf("move the restored dir into place at %s: %w", target, err)
	}
	return nil
}

// readManifest decodes the archive's ManifestName entry.
func readManifest(zr *zip.Reader) (Manifest, error) {
	var m Manifest
	for _, f := range zr.File {
		if f.Name != ManifestName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return m, fmt.Errorf("read the archive's manifest: %w", err)
		}
		defer rc.Close()
		if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&m); err != nil {
			return m, fmt.Errorf("parse the archive's manifest: %w", err)
		}
		return m, nil
	}
	return m, fmt.Errorf("the archive has no %s", ManifestName)
}

// extract writes zr's entries, all but the manifest, under dir through an
// os.Root, so no entry can land outside it. Modes are applied last and
// deepest first, so a read-only directory does not block its own content.
func extract(zr *zip.Reader, dir string) error {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	type pendingMode struct {
		name string
		perm fs.FileMode
	}
	var modes []pendingMode
	for _, f := range zr.File {
		if f.Name == ManifestName {
			continue
		}
		name := strings.TrimSuffix(f.Name, "/")
		if !fs.ValidPath(name) || name == "." {
			return fmt.Errorf("archive entry %q is not a relative path inside the archive", f.Name)
		}
		if parent := path.Dir(name); parent != "." {
			if err := r.MkdirAll(parent, 0o700); err != nil {
				return fmt.Errorf("restore %s: %w", f.Name, err)
			}
		}
		mode := f.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			target, err := readEntry(f, 4096)
			if err != nil {
				return fmt.Errorf("restore %s: %w", f.Name, err)
			}
			if err := r.Symlink(string(target), name); err != nil {
				return fmt.Errorf("restore %s: %w", f.Name, err)
			}
		case mode.IsDir():
			if err := r.MkdirAll(name, 0o700); err != nil {
				return fmt.Errorf("restore %s: %w", f.Name, err)
			}
			modes = append(modes, pendingMode{name, mode.Perm()})
		case mode.IsRegular():
			if err := writeEntry(r, f, name); err != nil {
				return fmt.Errorf("restore %s: %w", f.Name, err)
			}
			modes = append(modes, pendingMode{name, mode.Perm()})
		}
	}
	for i := len(modes) - 1; i >= 0; i-- {
		if err := r.Chmod(modes[i].name, modes[i].perm); err != nil {
			return fmt.Errorf("restore the mode of %s: %w", modes[i].name, err)
		}
	}
	return nil
}

// writeEntry writes the regular-file entry f as name inside r.
func writeEntry(r *os.Root, f *zip.File, name string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, rc); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// readEntry reads at most limit bytes of entry f: a symlink's target.
func readEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/claudetmp/`
Expected: PASS. If `TestRestore_RejectsEscapes/file beneath an outside symlink` passes for the wrong reason (no error), check `os.Root.MkdirAll` and `OpenFile` behaviour on an escaping symlink with a quick probe. Both must refuse. Fix `extract` rather than the test.

### B4. The contract test, then verify and commit

> **Amendment B-1 (2026-10-07, binding; supersedes Step 1's code and Step 2's command below).** A headless `claude -p` is never given a scratchpad (verified on 2.1.292: it creates an empty `claude-<uid>/` and no per-cwd dir), so the `-p` test below cannot observe the layout. The committed contract test is `TestRealClaude_TempDirLayout` in **`session/claude_tmp_realclaude_test.go`** (package `session`). It drives an interactive `claude --model haiku` on a private tmux server with the pattern and `waitForRealClaude` helper of `session/claude_hooks_realclaude_test.go`, sets `CLAUDE_CODE_TMPDIR` for the pane, has it write `probe.txt` into its scratchpad, and asserts that `claudetmp.Locate` finds the dir and that the dir is named after the physical cwd. Run it with `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/ -run TestRealClaude_TempDirLayout -v`. `session/claudetmp/realclaude_test.go` does not exist. The layout itself was confirmed on a live interactive session: `<root>/-tb-Source-…-claude-temp-18dc24bd53dc1133/<session id>/{scratchpad,tasks}`, physical path.

- [ ] **Step 1: Write `session/claudetmp/realclaude_test.go`**

```go
package claudetmp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_TempDirLayout pins the layout this package assumes —
// Claude's temp root and its encoding of the physical cwd — against a real
// `claude -p` on haiku, run in a cwd reached through a symlinked parent.
// It costs a few cents and leaves a transcript in ~/.claude/projects, so it
// only runs with LOOM_TEST_REAL_CLAUDE=1, never in CI.
func TestRealClaude_TempDirLayout(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlinked parents and claude-<uid> roots are POSIX")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}

	tmp := t.TempDir()
	real := filepath.Join(t.TempDir(), "real")
	require.NoError(t, os.MkdirAll(filepath.Join(real, "repo", "wt_18be000000000001"), 0o700))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	cwd := filepath.Join(link, "repo", "wt_18be000000000001") // the path loom would store

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, "claude", "-p", "--model", "haiku", "--allowedTools", "Write",
		"Write the word ok to a file named probe.txt in your scratchpad directory. Reply with only the absolute path you wrote.")
	c.Dir = cwd
	// An explicit Env keeps os/exec from setting PWD to Dir: under tmux the
	// agent's shell sees its physical cwd, and so must claude here.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PWD=") && !strings.HasPrefix(kv, EnvTmpDir+"=") {
			c.Env = append(c.Env, kv)
		}
	}
	c.Env = append(c.Env, EnvTmpDir+"="+tmp)
	out, err := c.CombinedOutput()
	require.NoError(t, err, string(out))

	t.Setenv(EnvTmpDir, tmp)
	root, ok := Root()
	require.True(t, ok, "claude created no temp root under %s (it said %q)", tmp, out)
	dir, ok := Locate(root, cwd)
	require.True(t, ok, "Locate found no temp dir for %s under %s (claude said %q)", cwd, root, out)
	want, _ := DirName(physical(cwd))
	assert.Equal(t, want, filepath.Base(dir), "Claude names the dir after its physical cwd")
	matches, _ := filepath.Glob(filepath.Join(dir, "*", "scratchpad", "probe.txt"))
	assert.Len(t, matches, 1, "the scratchpad sits at <dir>/<session id>/scratchpad (claude said %q)", out)
}
```

- [ ] **Step 2: Run the contract test once, if `claude` is installed and logged in** (a few cents):

Run: `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/claudetmp/ -run TestRealClaude -v`
Expected: PASS. If it fails on the scratchpad assertion but Locate succeeds, Claude did not write the file (check the quoted output). Adjust the prompt or flags, not the layout code. If Locate fails, the layout has drifted from the spec: stop and report it to the user before building on it.

- [ ] **Step 3: Verify.** `CGO_ENABLED=0 go test ./session/claudetmp/ ./internal/testenv/` passes; `go vet ./session/claudetmp/ ./internal/testenv/` is clean; `gofmt -l` on the new files prints nothing; `CC=clang CGO_ENABLED=1 go test -race ./session/claudetmp/` passes.
- [ ] **Step 4: Commit**

```bash
git add session/claudetmp internal/testenv/testenv.go internal/testenv/testenv_test.go
git commit -m "feat(claudetmp): locate, archive and restore Claude's per-session temp dirs

Claude Code keeps each session's scratchpad and task output under
<root>/<encoded cwd>/, named after the physical cwd, and nothing removes
it. claudetmp finds that dir (both path forms, failing closed on any
ambiguity), zips it without following symlinks or keeping CACHEDIR.TAG
caches, deletes the source only after the zip is in place, and restores
it through os.Root. testenv points CLAUDE_CODE_TMPDIR at a throwaway dir
so no test reaches the real root.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Package C: Session wiring

> **Review amendments C-1 to C-2 (2026-10-07, binding; supersede C1 step 4 and the C2 code where they differ).**
> - **C-1 (Pause archives before it marks the instance Paused).** The archive runs inside the pause Cmd. Once the status reads Paused, `runResumeSelected` can start a resume, which would find src still in place, skip the restore, launch Claude in it, and then have the archive delete what the resumed session writes. So `Pause` calls the archive immediately after the worktree removal and prune succeed, while the status is still Loading, and before `TransitionTo(Paused)`. It still never runs when Pause returns early with an error. Crash safety holds: a crash mid-archive leaves the saved record Running (with `StashRef` when there was a stash), reconcile's `CrashRestart` fails on the missing tree and marks it Paused, and Resume then finds either the zip or src. Route both call sites through a package var, `var archiveClaudeTempFn = archiveClaudeTemp`, so a test can wrap it. Test: wrap it to record `inst.GetStatus()` at call time, and assert the status is not Paused.
> - **C-2 (one sweep warning per dir per loom run).** `SweepClaudeTemp` remembers (a package-level `sync.Map` keyed by the dir's full path) every dir it failed to archive in this process. Later sweeps in the same process skip it with a debug line, so a dir that can never be archived (an unreadable subdir, say) costs one warning per run, not one per workspace load. A restart retries. Test: a dir that fails (make the archive dir path a file, as `TestArchiveFailure_FailsNeitherPauseNorKill` does) is attempted once across two sweeps. Count attempts through `archiveClaudeTempFn` if the sweep uses it, or another seam you add.
> - **C-3 (Kill: an answered "no such session" is success).** `tmux.Session.Close` returns an error for a session that is already dead, so today every Kill of a Paused or tick-Paused instance takes the error branch: it logs `kill.instance_kill_failed` at Error and skips the archive. When `Close` fails, `Kill` asks `SessionLiveness()`. `LivenessDead` (tmux answered: the session is gone) is not an error: log it at debug and carry on, exactly as Pause does. Any other answer keeps the error, so the instance is restored for a retry and nothing is archived. Fix the comment above Kill's archive call to match. Tests: (a) kill-session fails and has-session answers "can't find session", so Kill returns nil and archives the dir; (b) kill-session fails and the liveness probe can't answer (Unknown, i.e. a has-session error that isn't "no such session"), so Kill returns an error, the instance is restored, and the dir stays. Use `fakeTmuxServer`'s `failKill` and `probe`.
> - **C-4 (pin the sweep's fail-closed guards).** Rewrite `TestSweepClaudeTemp_NeverTakesATruncatedName` so the config dir is short (its prefix well under 200 characters) and the worktree path is long (e.g. a 180-character user dir under `worktrees/`), making the full name exceed 200. Only the length guard can then reject it, and removing that guard must fail the test. Add tests showing `worktreeOnDisk` fails closed: an unreadable `worktrees/u` (chmod 000; `t.Skip` when `os.Geteuid() == 0`; restore the mode in `t.Cleanup`) leaves a candidate unarchived. Do the same for an unreadable config dir.
> - **C-5 (`worktreeOnDisk` fails closed at its depth limit).** When the depth limit stops a descent into a directory that is neither a worktree (timestamp suffix) nor a git root, return true (treat it as on disk). A branch prefix with five or more slashes would otherwise hide a live worktree from check 4. Test: a live worktree nested deeper than `maxOrphanScanDepth` keeps its dir.
> - **C-6 (a quiet period before the sweep takes a dir).** Defence in depth for live sessions that check 4 cannot see (an unregistered colliding workspace used by a loom with another global dir, or an agent that outlived a Kill whose tmux close timed out): the sweep skips a candidate when the dir, or any entry within its top three levels (`<dir>`, `<dir>/<session id>`, `<dir>/<session id>/{scratchpad,tasks}` and their direct children), has an mtime within `sweepQuietPeriod` (a package var, 24h). Use `Lstat`, not `Stat`, and count an error as recent (fail closed). The cost is that an orphan's dir is archived a day after it goes quiet; Pause and Kill are unaffected. Existing sweep tests set `sweepQuietPeriod = 0` (save and restore it) or backdate their fixtures. New test: with the default period, a freshly written unclaimed dir is kept, and the same dir backdated past the period is archived. Add a sixth numbered check to `SweepClaudeTemp`'s doc comment.
> - **C-7.** `SweepClaudeTemp` ignores empty entries in `otherConfigDirs`, since `WorktreePrefixes("")` would resolve the process's cwd.

**Files:** create `session/claude_tmp.go`, `session/claude_tmp_test.go`; modify `session/instance.go`, `session/notice.go`.

### C1. Archive and restore glue, wired into Pause, Kill and Resume (TDD)

- [ ] **Step 1: Write the failing tests** in `session/claude_tmp_test.go`. They reuse `newTestPausableInstance`, `newTestStartedInstance` (`instance_lifecycle_test.go`) and `newTickPausedInstance` (`resume_inplace_test.go`). The shared fixture leaves `ConfigDir` empty on purpose: setting it there would change every fixture launch (hooks, loom context). These tests set it themselves with `withConfigDir`.

```go
package session

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/session/claudetmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeRoot points Claude's temp root at a fresh dir for this test and
// returns it, resolved.
func claudeRoot(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv(claudetmp.EnvTmpDir, base)
	root := filepath.Join(base, fmt.Sprintf("claude-%d", os.Getuid()))
	require.NoError(t, os.Mkdir(root, 0o700))
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return resolved
}

// claudeTempDir creates the temp dir Claude keeps for a session in wt,
// named after wt's physical path, with a scratchpad note.
func claudeTempDir(t *testing.T, root, wt string) string {
	t.Helper()
	dir := filepath.Join(root, claudetmp.Names(wt)[0].Value)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sess-1", "scratchpad"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sess-1", "scratchpad", "notes.md"), []byte("plan"), 0o600))
	return dir
}

// withConfigDir sets the instance's ConfigDir to the one its fixture
// worktree lives in (<configDir>/worktrees/<leaf>), as production does.
func withConfigDir(inst *Instance) {
	inst.ConfigDir = filepath.Dir(filepath.Dir(inst.getGitWorktree().GetWorktreePath()))
}

func archivedZips(t *testing.T, inst *Instance) []string {
	t.Helper()
	zips, err := filepath.Glob(filepath.Join(claudetmp.ArchiveDir(inst.ConfigDir), "*.zip"))
	require.NoError(t, err)
	return zips
}

func manifestOf(t *testing.T, zipPath string) claudetmp.Manifest {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == claudetmp.ManifestName {
			rc, err := f.Open()
			require.NoError(t, err)
			defer rc.Close()
			data, err := io.ReadAll(rc)
			require.NoError(t, err)
			var m claudetmp.Manifest
			require.NoError(t, json.Unmarshal(data, &m))
			return m
		}
	}
	t.Fatalf("%s has no manifest", zipPath)
	return claudetmp.Manifest{}
}

func TestPause_ArchivesClaudeTempAndResumeRestoresIt(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	wt := inst.getGitWorktree().GetWorktreePath()
	dir := claudeTempDir(t, root, wt)

	require.NoError(t, inst.Pause(nil))

	assert.NoDirExists(t, dir, "Pause archives the temp dir and removes it")
	zips := archivedZips(t, inst)
	require.Len(t, zips, 1)
	m := manifestOf(t, zips[0])
	assert.Equal(t, "pause", m.Reason)
	assert.Equal(t, wt, m.Worktree)

	require.NoError(t, inst.Resume(nil))

	data, err := os.ReadFile(filepath.Join(dir, "sess-1", "scratchpad", "notes.md"))
	require.NoError(t, err, "Resume restores the scratchpad before the agent launches")
	assert.Equal(t, "plan", string(data))
	assert.Empty(t, archivedZips(t, inst), "a restored archive is deleted")
}

// TestRelaunchInPlace_RestoresClaudeTemp: the other launch path. The agent
// exited on its own, the worktree is intact, and a zip is parked.
func TestRelaunchInPlace_RestoresClaudeTemp(t *testing.T) {
	root := claudeRoot(t)
	inst, srv := newTickPausedInstance(t)
	withConfigDir(inst)
	wt := inst.getGitWorktree().GetWorktreePath()
	dir := claudeTempDir(t, root, wt)
	archiveClaudeTemp(inst.ConfigDir, wt, "pause")
	require.NoDirExists(t, dir)

	require.NoError(t, inst.Resume(nil))

	assert.FileExists(t, filepath.Join(dir, "sess-1", "scratchpad", "notes.md"))
	assert.Len(t, srv.launchArgs(), 1, "the agent was relaunched in place")
}

func TestKill_ArchivesClaudeTemp(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())

	require.NoError(t, inst.Kill())

	assert.NoDirExists(t, dir)
	zips := archivedZips(t, inst)
	require.Len(t, zips, 1)
	assert.Equal(t, "kill", manifestOf(t, zips[0]).Reason)
}

// TestKill_OfAPausedSessionKeepsItsParkedZip: the dir went at pause, so
// the parked zip simply stays as the session's archive.
func TestKill_OfAPausedSessionKeepsItsParkedZip(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())
	require.NoError(t, inst.Pause(nil))
	parked := archivedZips(t, inst)
	require.Len(t, parked, 1)

	require.NoError(t, inst.Kill())

	assert.Equal(t, parked, archivedZips(t, inst))
}

// TestPause_DemotesAnEarlierParkedZip: a zip parked by an earlier pause
// whose restore failed becomes a permanent, timestamped archive.
func TestPause_DemotesAnEarlierParkedZip(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())
	name := claudetmp.ArchiveName(filepath.Base(dir), claudetmp.WorktreePrefixes(inst.ConfigDir))
	adir := claudetmp.ArchiveDir(inst.ConfigDir)
	require.NoError(t, os.MkdirAll(adir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(adir, name+".zip"), []byte("earlier"), 0o600))

	require.NoError(t, inst.Pause(nil))

	demoted, err := filepath.Glob(filepath.Join(adir, name+".*.zip"))
	require.NoError(t, err)
	require.Len(t, demoted, 1)
	data, err := os.ReadFile(demoted[0])
	require.NoError(t, err)
	assert.Equal(t, "earlier", string(data))
	assert.Equal(t, "pause", manifestOf(t, filepath.Join(adir, name+".zip")).Reason)
}

func TestResume_RestoreFailureIsANoticeAndTheLaunchProceeds(t *testing.T) {
	claudeRoot(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	require.NoError(t, inst.Pause(nil))
	wt := inst.getGitWorktree().GetWorktreePath()
	name := claudetmp.ArchiveName(claudetmp.Names(wt)[0].Value, claudetmp.WorktreePrefixes(inst.ConfigDir))
	adir := claudetmp.ArchiveDir(inst.ConfigDir)
	require.NoError(t, os.MkdirAll(adir, 0o700))
	zipPath := filepath.Join(adir, name+".zip")
	require.NoError(t, os.WriteFile(zipPath, []byte("not a zip"), 0o600))

	err := inst.Resume(nil)

	n, ok := OnlyNotice(err)
	require.True(t, ok, "a failed restore never fails the resume: %v", err)
	assert.Contains(t, n.Error(), "couldn't restore Claude's scratchpad")
	assert.Contains(t, n.Error(), zipPath)
	assert.Equal(t, Running, inst.GetStatus())
	assert.FileExists(t, zipPath, "the archive is kept")
}

func TestArchiveFailure_FailsNeitherPauseNorKill(t *testing.T) {
	for _, op := range []struct {
		name string
		run  func(*Instance) error
	}{
		{"pause", func(i *Instance) error { return i.Pause(nil) }},
		{"kill", (*Instance).Kill},
	} {
		t.Run(op.name, func(t *testing.T) {
			root := claudeRoot(t)
			inst := newTestPausableInstance(t)
			withConfigDir(inst)
			dir := claudeTempDir(t, root, inst.getGitWorktree().GetWorktreePath())
			// A file where the archive dir must go makes every archive fail.
			require.NoError(t, os.MkdirAll(filepath.Join(inst.ConfigDir, "archive"), 0o700))
			require.NoError(t, os.WriteFile(claudetmp.ArchiveDir(inst.ConfigDir), []byte("in the way"), 0o600))

			require.NoError(t, op.run(inst))
			assert.DirExists(t, dir, "a failed archive leaves the temp dir where it was")
		})
	}
}

// TestKill_SkipsWorkspaceTerminals: they run in the repository root, which
// the user's own Claude sessions share.
func TestKill_SkipsWorkspaceTerminals(t *testing.T) {
	root := claudeRoot(t)
	inst := newTestStartedInstance(t)
	inst.Path = t.TempDir()
	inst.ConfigDir = t.TempDir()
	dir := claudeTempDir(t, root, inst.Path)

	require.NoError(t, inst.Kill())

	assert.DirExists(t, dir)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/ -run 'ClaudeTemp|ArchiveFailure|ParkedZip|RestoreFailure|SkipsWorkspaceTerminals'`
Expected: build failure (`undefined: archiveClaudeTemp`).

- [ ] **Step 3: Implement the glue** in `session/claude_tmp.go`:

```go
package session

import (
	"fmt"
	"path/filepath"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/claudetmp"
)

// archiveClaudeTemp zips Claude's temp dir for the session whose worktree
// was worktreePath into configDir's archive and deletes it (see claudetmp).
// Best-effort: a failure is logged and the dir stays where it is, for a
// resume to find in place or a later sweep to retry. A no-op when Claude's
// root or the dir is absent, so non-Claude programs need no special case.
func archiveClaudeTemp(configDir, worktreePath, reason string) {
	if configDir == "" || worktreePath == "" {
		return
	}
	root, ok := claudetmp.Root()
	if !ok {
		return
	}
	src, ok := claudetmp.Locate(root, worktreePath)
	if !ok {
		return
	}
	_ = archiveClaudeTempDir(configDir, src, worktreePath, reason)
}

// archiveClaudeTempDir archives src, a temp dir already found, as
// <archive dir>/<name>.zip (claudetmp.ArchiveAs) and logs the outcome.
func archiveClaudeTempDir(configDir, src, worktreePath, reason string) error {
	dir := claudetmp.ArchiveDir(configDir)
	name := claudetmp.ArchiveName(filepath.Base(src), claudetmp.WorktreePrefixes(configDir))
	zipPath, res, err := claudetmp.ArchiveAs(dir, name, src, claudetmp.Manifest{Worktree: worktreePath, Reason: reason})
	lg := log.For("claudetmp")
	if err != nil {
		lg.Warn("claudetmp.archive_failed", "source", src, "zip", zipPath, "reason", reason, "err", err.Error())
		return err
	}
	lg.Info("claudetmp.archived", "source", src, "zip", zipPath, "reason", reason,
		"bytes_in", res.BytesIn, "bytes_out", res.BytesOut, "cache_bytes_skipped", res.CacheBytes,
		"archive_dir_bytes", claudetmp.DirSize(dir))
	return nil
}

// restoreClaudeTemp puts back the temp dir a pause archived for the
// session whose worktree is worktreePath, before the agent launches in it.
// A no-op when no zip is parked for it, or when the dir is already there
// (a pause whose archive failed left it in place). A failed restore
// returns what the user must hear; the zip is kept and the launch goes
// ahead without the scratchpad.
func restoreClaudeTemp(configDir, worktreePath string) error {
	if configDir == "" || worktreePath == "" {
		return nil
	}
	zipPath, ok := claudetmp.Parked(claudetmp.ArchiveDir(configDir), worktreePath, claudetmp.WorktreePrefixes(configDir))
	if !ok {
		return nil
	}
	root, exists := claudetmp.Root()
	if exists {
		if _, found := claudetmp.Locate(root, worktreePath); found {
			return nil
		}
	}
	lg := log.For("claudetmp")
	if err := claudetmp.Restore(zipPath, root); err != nil {
		lg.Warn("claudetmp.restore_failed", "zip", zipPath, "worktree", worktreePath, "err", err.Error())
		return fmt.Errorf("couldn't restore Claude's scratchpad: %w; archive kept at %s", err, zipPath)
	}
	lg.Info("claudetmp.restored", "zip", zipPath, "worktree", worktreePath)
	return nil
}
```

- [ ] **Step 4: Wire Pause** (`session/instance.go`). At the end of `Pause`, replace

```go
	if saveState != nil {
		if err := saveState(); err != nil {
			return fmt.Errorf("pause checkpoint save: %w", err)
		}
	}
	return nil
}
```

with

```go
	if saveState != nil {
		if err := saveState(); err != nil {
			return fmt.Errorf("pause checkpoint save: %w", err)
		}
	}
	// After the checkpoint, so a crash during this slow step leaves the
	// instance Paused with its temp dir either intact or zipped; Resume
	// restores it from either.
	archiveClaudeTemp(i.ConfigDir, gw.GetWorktreePath(), "pause")
	return nil
}
```

- [ ] **Step 5: Wire Kill.** At the end of `Kill`, replace the final `return NewNotice(notices...)` with

```go
	// Only once everything else is gone: a kill that failed restored the
	// instance above, which still claims its dir, and an agent whose tmux
	// session would not close may still be writing to it.
	if gitWT != nil && !isWorkspaceTerm {
		archiveClaudeTemp(i.ConfigDir, gitWT.GetWorktreePath(), "kill")
	}
	return NewNotice(notices...)
```

- [ ] **Step 6: Wire Resume.** In the `resumeRelaunchInPlace` case, replace

```go
		return withNotices(i.finishResume(saveState, ts, gw), note)
```

with

```go
		// Before finishResume launches the agent, so Claude finds its
		// scratchpad. (Evaluated first: Go evaluates call arguments in
		// order, so this must not move into withNotices' argument list
		// after finishResume.)
		tmpNote := restoreClaudeTemp(i.ConfigDir, gw.GetWorktreePath())
		return withNotices(i.finishResume(saveState, ts, gw), note, tmpNote)
```

At the end of the rebuild path, replace

```go
	return withNotices(i.finishResume(saveState, ts, gw), notes...)
}
```

with

```go
	// Before finishResume launches the agent, so Claude finds its scratchpad.
	notes = append(notes, restoreClaudeTemp(i.ConfigDir, gw.GetWorktreePath()))
	return withNotices(i.finishResume(saveState, ts, gw), notes...)
}
```

`NewNotice` joins with `errors.Join`, which drops nil, so a nil `tmpNote` adds nothing. Update `Resume`'s doc comment's last paragraph to read: "A resume that succeeded but forgot a stash no longer in `git stash list`, restored one it then could not drop, or could not restore Claude's archived scratchpad, returns a Notice saying so (see OnlyNotice); a failed one includes those notices in its error."

- [ ] **Step 7: Update `session/notice.go`'s doc.** In the `Notice` comment, change "a pending stash it forgot because it was no longer in `git stash list`, or a stash entry it could not drop" to "a pending stash it forgot because it was no longer in `git stash list`, a stash entry it could not drop, or Claude's archived scratchpad it could not restore".

- [ ] **Step 8: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/`
Expected: PASS, the existing Pause/Kill/Resume tests included (their roots are the testenv's absent throwaway dir, so the archive no-ops).

### C2. The sweep (TDD)

- [ ] **Step 1: Write the failing tests**, appended to `session/claude_tmp_test.go` (add `"strings"` to its imports):

```go
// sweepWorkspace is a config dir with a worktrees/u dir, under parent.
func sweepWorkspace(t *testing.T, parent, repoName string) (cfg, wtDir string) {
	t.Helper()
	cfg = filepath.Join(parent, repoName, ".loom")
	wtDir = filepath.Join(cfg, "worktrees", "u")
	require.NoError(t, os.MkdirAll(wtDir, 0o755))
	return cfg, wtDir
}

func TestSweepClaudeTemp_ArchivesOnlyWhatNoSessionOwns(t *testing.T) {
	root := claudeRoot(t)
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")

	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))
	pausedWT := filepath.Join(wtDir, "paused_18be000000000002")
	paused := claudeTempDir(t, root, pausedWT)
	liveWT := filepath.Join(wtDir, "live_18be000000000003")
	require.NoError(t, os.Mkdir(liveWT, 0o755)) // started after the claim snapshot
	live := claudeTempDir(t, root, liveWT)
	backupWT := filepath.Join(cfg, "worktrees-backup", "u", "x_18be000000000004")
	require.NoError(t, os.MkdirAll(backupWT, 0o755))
	lookalike := claudeTempDir(t, root, backupWT)
	noStamp := claudeTempDir(t, root, filepath.Join(cfg, "worktrees-old"))
	otherCfg, otherWT := sweepWorkspace(t, t.TempDir(), "other")
	other := claudeTempDir(t, root, filepath.Join(otherWT, "gone_18be000000000005"))

	n := SweepClaudeTemp(cfg, map[string]bool{pausedWT: true}, []string{cfg, otherCfg})

	assert.Equal(t, 1, n)
	assert.NoDirExists(t, gone, "unclaimed, nothing on disk: archived")
	assert.DirExists(t, paused, "claimed")
	assert.DirExists(t, live, "its worktree is on disk")
	assert.DirExists(t, lookalike, "a look-alike dir that is on disk")
	assert.DirExists(t, noStamp, "no worktree timestamp")
	assert.DirExists(t, other, "another workspace's prefix")
	zips, err := filepath.Glob(filepath.Join(claudetmp.ArchiveDir(cfg), "*.zip"))
	require.NoError(t, err)
	require.Len(t, zips, 1)
	assert.Equal(t, "u-gone-18be000000000001.zip", filepath.Base(zips[0]))
	assert.Equal(t, "sweep", manifestOf(t, zips[0]).Reason)
}

// TestSweepClaudeTemp_BothPathForms: loom stores paths through a symlink,
// Claude names dirs after the physical path.
func TestSweepClaudeTemp_BothPathForms(t *testing.T) {
	root := claudeRoot(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	cfg, wtDir := sweepWorkspace(t, link, "repo") // stored through the link

	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))
	claimedWT := filepath.Join(wtDir, "kept_18be000000000002")
	kept := claudeTempDir(t, root, claimedWT)
	require.True(t, strings.HasPrefix(filepath.Base(gone), claudetmp.WorktreePrefixes(cfg)[0]), "named by the physical path")

	n := SweepClaudeTemp(cfg, map[string]bool{claimedWT: true}, nil)

	assert.Equal(t, 1, n)
	assert.NoDirExists(t, gone)
	assert.DirExists(t, kept, "claimed by its stored path")
}

// TestSweepClaudeTemp_ACollidingWorkspaceStopsTheSweep: foo_bar and
// foo-bar encode alike, so a name could be either workspace's.
func TestSweepClaudeTemp_ACollidingWorkspaceStopsTheSweep(t *testing.T) {
	root := claudeRoot(t)
	parent := t.TempDir()
	cfg, wtDir := sweepWorkspace(t, parent, "my_proj")
	twin, _ := sweepWorkspace(t, parent, "my-proj")
	gone := claudeTempDir(t, root, filepath.Join(wtDir, "gone_18be000000000001"))

	assert.Zero(t, SweepClaudeTemp(cfg, nil, []string{cfg, twin}))
	assert.DirExists(t, gone)

	assert.Equal(t, 1, SweepClaudeTemp(cfg, nil, []string{cfg}), "its own entry in the list is skipped")
	assert.NoDirExists(t, gone)
}

// TestSweepClaudeTemp_NeverTakesATruncatedName: past 200 characters the
// name ends in Claude's hash, which no check can compare.
func TestSweepClaudeTemp_NeverTakesATruncatedName(t *testing.T) {
	root := claudeRoot(t)
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), strings.Repeat("d", 150))
	prefix, truncated := claudetmp.DirName(filepath.Join(wtDir, "gone_18be000000000001"))
	require.True(t, truncated)
	dir := filepath.Join(root, prefix+"18be000000000001") // a hash that even looks like a timestamp
	require.NoError(t, os.Mkdir(dir, 0o700))

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.DirExists(t, dir)
}

func TestSweepClaudeTemp_NoRootIsANoOp(t *testing.T) {
	t.Setenv(claudetmp.EnvTmpDir, t.TempDir()) // no claude-<uid> inside
	cfg, _ := sweepWorkspace(t, t.TempDir(), "repo")

	assert.Zero(t, SweepClaudeTemp(cfg, nil, nil))
	assert.NoDirExists(t, claudetmp.ArchiveDir(cfg))
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/ -run SweepClaudeTemp`
Expected: build failure (`undefined: SweepClaudeTemp`).

- [ ] **Step 3: Implement**, appended to `session/claude_tmp.go` (add `"os"` and `"strings"` to its imports):

```go
// SweepClaudeTemp archives (reason "sweep") the Claude temp dirs of
// configDir's sessions that no longer exist: orphans the auto-clean just
// removed, sessions an older loom killed, and the backlog from before loom
// archived anything. It returns how many it archived.
//
// A dir under Claude's root is taken only when all of these hold:
//  1. Its name starts with configDir's encoded worktrees prefix (resolved
//     or as given) and with no other known config dir's: Claude's encoding
//     is lossy (foo_bar and foo-bar share a prefix), and a workspace can be
//     registered inside another's worktrees dir. otherConfigDirs lists
//     every known config dir; configDir itself may be among them.
//  2. Its last "-" segment is a plausible worktree timestamp suffix, and
//     the name is not truncated (past claudetmp.MaxDirName its tail is a
//     hash nothing here can compare).
//  3. No path in claimed encodes to it.
//  4. Checked again just before it is archived: no directory under
//     configDir's worktrees dirs (worktreeOnDisk) encodes to it.
//
// A live session's dir cannot be taken: its worktree exists before its
// Claude creates the dir, so it fails check 4 even when another loom
// started it after claimed was snapshotted. Paused sessions are claimed.
func SweepClaudeTemp(configDir string, claimed map[string]bool, otherConfigDirs []string) int {
	if configDir == "" {
		return 0
	}
	root, ok := claudetmp.Root()
	if !ok {
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		log.For("claudetmp").Debug("claudetmp.sweep_read_failed", "root", root, "err", err.Error())
		return 0
	}
	own := claudetmp.WorktreePrefixes(configDir)
	self := canonicalPath(configDir)
	var foreign []string
	for _, d := range otherConfigDirs {
		if canonicalPath(d) != self {
			foreign = append(foreign, claudetmp.WorktreePrefixes(d)...)
		}
	}
	var claimedNames []claudetmp.Name
	for p := range claimed {
		claimedNames = append(claimedNames, claudetmp.Names(p)...)
	}
	archived := 0
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !sweepableClaudeTemp(name, own, foreign) || matchesAnyName(claimedNames, name) {
			continue
		}
		if worktreeOnDisk(configDir, name) {
			continue
		}
		if archiveClaudeTempDir(configDir, filepath.Join(root, name), "", "sweep") == nil {
			archived++
		}
	}
	if archived > 0 {
		log.For("claudetmp").Info("claudetmp.sweep_done", "config_dir", configDir, "archived", archived)
	}
	return archived
}

// sweepableClaudeTemp is SweepClaudeTemp's checks 1 and 2 on a dir name.
func sweepableClaudeTemp(name string, own, foreign []string) bool {
	if len(name) > claudetmp.MaxDirName || !hasAnyPrefix(name, own) || hasAnyPrefix(name, foreign) {
		return false
	}
	return looksLikeTimestampSuffix(name[strings.LastIndex(name, "-")+1:])
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func matchesAnyName(names []claudetmp.Name, dirName string) bool {
	for _, n := range names {
		if n.Matches(dirName) {
			return true
		}
	}
	return false
}

// worktreeOnDisk is SweepClaudeTemp's check 4: whether some directory under
// one of configDir's worktrees dirs encodes to name. That is worktrees/ and
// any look-alike beside it (worktrees-backup/, worktrees.old/), whose
// sessions' names start with the same prefix. It walks the way
// DiscoverOrphans does — through prefix dirs, never into a worktree — and
// fails closed: a directory it cannot read counts as a match.
func worktreeOnDisk(configDir, name string) bool {
	top, err := os.ReadDir(configDir)
	if err != nil {
		return !os.IsNotExist(err)
	}
	var walk func(dir string, depth int) bool
	walk = func(dir string, depth int) bool {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return true
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if matchesAnyName(claudetmp.Names(p), name) {
				return true
			}
			if _, isWorktree := stripTimestampSuffix(e.Name()); isWorktree || isGitWorktreeRoot(p) {
				continue
			}
			if depth < maxOrphanScanDepth && walk(p, depth+1) {
				return true
			}
		}
		return false
	}
	for _, e := range top {
		if e.IsDir() && strings.HasPrefix(e.Name(), "worktrees") && walk(filepath.Join(configDir, e.Name()), 0) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/`
Expected: PASS.

### C3. Verify and commit

- [ ] **Step 1:** `go vet ./session/...` is clean; `gofmt -l` on the touched files prints nothing; `CC=clang CGO_ENABLED=1 go test -race ./session/...` passes.
- [ ] **Step 2: Commit**

```bash
git add session/claude_tmp.go session/claude_tmp_test.go session/instance.go session/notice.go
git commit -m "feat(session): archive Claude's temp dir on pause and kill, restore on resume

Pause archives the session's Claude temp dir after its Paused checkpoint,
Kill after everything else is gone, both best-effort. Resume restores the
parked zip before the agent launches, on both launch paths, and a failed
restore is a Notice, not a failed resume. SweepClaudeTemp archives the
dirs of sessions that no longer exist, taking only names no other known
workspace could own, no record claims, and no directory on disk encodes.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Package D: App sweep, `loom debug`, docs, verification

> **Doc amendment D-1 (2026-10-07, binding; follows B-2, B-3, C-1 and C-2).** In D3's CLAUDE.md text:
> - In the `session/claudetmp/` Key Packages bullet, after "truncated past 200 characters with a hash loom can't compute", add ", so a truncated name never matches and such dirs are left alone". After "the source deleted only after the rename", add "(renamed to a `.loom-trash-*` tombstone, then deleted)".
> - In the "Claude's temp dirs are archived" gotcha, change "Pause archives it after the Paused checkpoint is saved" to "Pause archives it after the worktree is removed but before it marks the instance Paused (a resume can't start while the archive runs)". Change "`Locate` fails closed on zero or several matches" to "`Locate` fails closed on zero or several matches and never matches a truncated name". After "one in flight", add "; a dir that fails to archive is skipped for the rest of that loom run".
> - **D-2 (follows C-3, C-6 and the review's Lua note).** In the same gotcha:
>   - After "Kill archives it once `Cleanup` and everything else succeeded", add "(a tmux session tmux confirms is already gone counts as closed, as in Pause)".
>   - Add the quiet period to the sweep's list of conditions: "nothing in its top three levels changed in the last 24 hours (`sweepQuietPeriod`)".
>   - Add "Lua `inst:pause()`/`inst:resume()` run under the script engine's lock, so key dispatch waits for the archive or restore too."

**Files:** create `app/claude_tmp.go`, `app/claude_tmp_test.go`; modify `app/app.go`, `app/pollgate.go`, `main.go`, `CLAUDE.md`, `USAGE.md`.

### D1. The gate and the queue (TDD)

- [ ] **Step 1: Write the failing tests** in `app/claude_tmp_test.go`:

```go
package app

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClaudeTmpSweep_QueuedByEveryLoadPath: both workspace-load paths run
// reconcileOrphans, which queues a sweep of the loaded config dir.
func TestClaudeTmpSweep_QueuedByEveryLoadPath(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())

	t.Run("multi-tab restore", func(t *testing.T) {
		ws := preservedTerminalWorkspace(t, "ws-sweep")
		m := newRestoreHome(&listingExec{})
		m.restoreSavedWorkspaces([]config.Workspace{ws})

		require.Len(t, m.slots, 1)
		assert.Contains(t, m.claudeTmpPending, config.WorkspaceConfigDir(&ws))
		assert.NotNil(t, m.maybeClaudeTmpSweep(), "the next health tick dispatches it")
	})

	t.Run("classic startup", func(t *testing.T) {
		ws := preservedTerminalWorkspace(t, "ws-classic")
		rec := &listingExec{}
		m := newRestoreHome(rec)
		m.wsCtx = config.WorkspaceContextFor(&ws)
		state := config.LoadStateFrom(m.wsCtx.ConfigDir)
		storage, err := session.NewStorage(state, m.wsCtx.ConfigDir)
		require.NoError(t, err)
		m.appState, m.storage = state, storage

		_, err = m.loadStartupStorage(rec, true)
		require.NoError(t, err)

		assert.Contains(t, m.claudeTmpPending, m.wsCtx.ConfigDir)
	})
}

// TestClaudeTmpSweep_OneInFlightAndALoadQueuesAnother: a load while a
// sweep runs gets exactly one more pass once it lands.
func TestClaudeTmpSweep_OneInFlightAndALoadQueuesAnother(t *testing.T) {
	sp := spinner.New()
	m := &home{}
	m.requestClaudeTmpSweep(t.TempDir(), ui.NewList(&sp), nil)
	first := m.maybeClaudeTmpSweep()
	require.NotNil(t, first)
	assert.Empty(t, m.claudeTmpPending, "dispatching takes the queue")

	m.requestClaudeTmpSweep(t.TempDir(), ui.NewList(&sp), nil)
	assert.Nil(t, m.maybeClaudeTmpSweep(), "at most one sweep in flight")

	msg, ok := first().(gatedMsg)
	require.True(t, ok)
	_, again := m.deliverGated(msg)
	require.NotNil(t, again, "the queued load gets one more pass")
	assert.Empty(t, m.claudeTmpPending)
	assert.Nil(t, m.maybeClaudeTmpSweep(), "and only one")
}

// TestClaudeTmpSweep_SnapshotsTheClaimSet: the claim set is taken when the
// load queues the sweep, on the Update goroutine.
func TestClaudeTmpSweep_SnapshotsTheClaimSet(t *testing.T) {
	cfg := t.TempDir()
	data := session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         "paused",
		Path:          t.TempDir(),
		Branch:        "u/paused",
		Status:        session.Paused,
		Worktree: session.GitWorktreeData{
			RepoPath:     t.TempDir(),
			WorktreePath: "/wt/paused_18be000000000001",
			BranchName:   "u/paused",
		},
	}
	inst, err := session.FromInstanceData(data, cfg)
	require.NoError(t, err)
	sp := spinner.New()
	list := ui.NewList(&sp)
	list.AddInstance(inst)
	m := &home{}

	m.requestClaudeTmpSweep(cfg, list, nil)

	assert.True(t, m.claudeTmpPending[cfg].claimed["/wt/paused_18be000000000001"])
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./app/ -run ClaudeTmpSweep`
Expected: build failure (`m.claudeTmpPending undefined`, `requestClaudeTmpSweep undefined`).

- [ ] **Step 3: Add the gate kind** in `app/pollgate.go`. In the `gateKind` const block, add before `numGateKinds`:

```go
	// gateClaudeTmp keeps one Claude temp-dir sweep in flight
	// (maybeClaudeTmpSweep).
	gateClaudeTmp
```

In `String()`, add `case gateClaudeTmp: return "claude_tmp"`. In the `gateIntervals` comment block, after the `gateAccountsRefresh` line, add `// gateClaudeTmp stays 0 too: sweeps run when a workspace loads.` In `redispatch`, add:

```go
	case gateClaudeTmp:
		return m.maybeClaudeTmpSweep()
```

- [ ] **Step 4: Add the field.** In `app/app.go`'s `home` struct, right after the `gates [numGateKinds]pollGate` field:

```go
	// claudeTmpPending holds the Claude temp-dir sweeps workspace loads
	// queued (requestClaudeTmpSweep), keyed by config dir, until the
	// health tick dispatches them. Update-goroutine only.
	claudeTmpPending map[string]claudeTmpJob
```

- [ ] **Step 5: Implement `app/claude_tmp.go`**

```go
package app

import (
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
)

// claudeTmpJob is one workspace's queued Claude temp-dir sweep, with its
// claim set and the known config dirs as they stood when it was queued.
type claudeTmpJob struct {
	cfgDir  string
	claimed map[string]bool
	others  []string
}

// requestClaudeTmpSweep queues a sweep of cfgDir's Claude temp dirs
// (session.SweepClaudeTemp), snapshotting the claim set (list plus the
// records storage preserves) here, on the Update goroutine.
// reconcileOrphans calls it, so every workspace-load path queues one, after
// the orphan auto-clean removed its worktrees. The health tick dispatches
// it; a request made while a sweep runs gets one more pass when it lands.
func (m *home) requestClaudeTmpSweep(cfgDir string, list *ui.List, storage *session.Storage) {
	if cfgDir == "" {
		return
	}
	if m.claudeTmpPending == nil {
		m.claudeTmpPending = map[string]claudeTmpJob{}
	}
	m.claudeTmpPending[cfgDir] = claudeTmpJob{
		cfgDir:  cfgDir,
		claimed: claimedWorktreePaths(list.GetInstances(), storage),
		others:  m.knownConfigDirs(),
	}
	m.gate(gateClaudeTmp).request()
}

// knownConfigDirs lists every registered workspace's config dir and the
// global one, so a sweep can leave alone any name another workspace could
// own (see session.SweepClaudeTemp).
func (m *home) knownConfigDirs() []string {
	var dirs []string
	if m.registry != nil {
		for i := range m.registry.Workspaces {
			if ws := &m.registry.Workspaces[i]; ws.Path != "" {
				dirs = append(dirs, config.WorkspaceConfigDir(ws))
			}
		}
	}
	if dir, err := config.GetGlobalConfigDir(); err == nil {
		dirs = append(dirs, dir)
	}
	return dirs
}

// maybeClaudeTmpSweep dispatches the queued sweeps when gateClaudeTmp is
// due (none in flight). nil when nothing is queued. Update goroutine only.
func (m *home) maybeClaudeTmpSweep() tea.Cmd {
	return m.dispatchGated(gateClaudeTmp, time.Now(), func() tea.Cmd {
		if len(m.claudeTmpPending) == 0 {
			return nil
		}
		jobs := slices.Collect(maps.Values(m.claudeTmpPending))
		m.claudeTmpPending = nil
		return claudeTmpSweepCmd(jobs)
	})
}

// claudeTmpSweepCmd runs the sweeps off the Update goroutine. Each logs
// what it archived and nothing comes back to apply, so the message is nil
// (deliverGated still disarms the gate).
func claudeTmpSweepCmd(jobs []claudeTmpJob) tea.Cmd {
	return func() tea.Msg {
		for _, j := range jobs {
			session.SweepClaudeTemp(j.cfgDir, j.claimed, j.others)
		}
		return nil
	}
}
```

- [ ] **Step 6: Queue from `reconcileOrphans` and dispatch from the health tick** (`app/app.go`). In `reconcileOrphans`, insert just before the `// Preserved records may come back on a later load` comment:

```go
	// Claude's temp dirs of sessions that are gone, the worktrees just
	// auto-cleaned included: archived off the Update goroutine once the
	// next health tick dispatches the sweep.
	m.requestClaudeTmpSweep(cfgDir, list, storage)
```

In the health tick, right after the `maybeUsageProbe` block:

```go
		// Claude temp-dir sweeps queued by workspace loads (see
		// requestClaudeTmpSweep). nil when none is queued or one runs.
		if sweep := m.maybeClaudeTmpSweep(); sweep != nil {
			cmds = append(cmds, sweep)
		}
```

- [ ] **Step 7: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./app/`
Expected: PASS, the existing `reconcileOrphans` tests included (they run `reconcileOrphans` on a zero `home`, which `requestClaudeTmpSweep` handles).

### D2. `loom debug`

- [ ] **Step 1:** In `main.go`, add `"github.com/aidan-bailey/loom/session/claudetmp"` to the imports, and in `debugCmd`'s `RunE`, right after the `Tmux socket:` line:

```go
			if root, ok := claudetmp.Root(); ok {
				fmt.Printf("Claude temp root: %s\n", root)
			} else {
				fmt.Printf("Claude temp root: %s (absent)\n", root)
			}
			fmt.Printf("Claude temp archives: %s (a workspace's: <repo>/.loom/archive/claude-tmp)\n", claudetmp.ArchiveDir(wsCtx.ConfigDir))
```

- [ ] **Step 2: Check it**

Run: `CGO_ENABLED=0 go build -o "$SCRATCH/loom-dbg" . && LOOM_HOME="$SCRATCH/h" LOOM_GLOBAL_DIR="$SCRATCH/g" "$SCRATCH/loom-dbg" debug`, with `SCRATCH` set to a fresh scratch directory (never `/tmp` directly).
Expected: the output includes `Claude temp root: /tmp/claude-<uid>` and `Claude temp archives: $SCRATCH/g/archive/claude-tmp (…)`.

### D3. Docs

- [ ] **Step 1: CLAUDE.md, Key Packages.** After the `session/github/` bullet, add:

```markdown
- **`session/claudetmp/`** — Claude Code's per-session temp dir, `<root>/<encoded cwd>/<session id>/{scratchpad,tasks}`, where root is the first of `$CLAUDE_CODE_TMPDIR`/`$TMPDIR`/`$TMP`/`$TEMP` (else `/tmp`) plus `claude-<uid>`. `Root`, `DirName`/`Names` (Claude's encoding: every UTF-16 unit outside `[a-zA-Z0-9]` becomes `-`, truncated past 200 characters with a hash loom can't compute), `Locate`, `Archive`/`ArchiveAs` (zip; symlinks stored as links; `CACHEDIR.TAG` dirs skipped; the source deleted only after the rename), `Parked`, `Restore` (through `os.Root`). No tmux, UI, app or config dependency. `session/claude_tmp.go` wires it into Pause, Kill, Resume and `SweepClaudeTemp`.
```

- [ ] **Step 2: CLAUDE.md, Gotchas.** After the "Paused does not mean the worktree is gone" bullet, add two bullets:

```markdown
- **Only the deleting paths unlock a worktree.** `git worktree remove -f` refuses any locked tree, and an `add` killed mid-checkout leaves its tree locked `initializing` forever. `git.RefuseLocked` is called by orphan auto-clean (`RemoveOrphanWorktree`) and by `GitWorktree.Cleanup`/`Remove` (Kill, Pause). It removes that lock only once it is older than `gitWorktreeAddTimeout` plus a minute (loom kills its own add at that deadline). Any other lock, a user's or a young `initializing` one, gets a `*git.LockedError` (`errors.Is(err, git.ErrWorktreeLocked)`) naming `git worktree unlock`, returned before anything is touched, branch and title sidecar included. `reconcileOrphans` logs a locked orphan at debug and does not count it cleaned. `clearWorktreePath` (Setup, rebuild) deliberately keeps its own `nothingLiveAt` rule: an intact tree must never reach `remove -f`, and if one does, its lock is what stops it.
- **Claude's temp dirs are archived, not deleted, and found by physical path.** Claude keeps each session's scratchpad and task output under `<root>/<encoded cwd>/` (tmpfs on most Linux machines). Before loom archived it, that dir outlived every removed worktree. Pause archives it after the Paused checkpoint is saved. Kill archives it once `Cleanup` and everything else succeeded (a failed kill keeps the instance claimed). Both are best-effort. Resume restores `<name>.zip` before the agent launches, on the rebuild and relaunch-in-place paths only (a reattach launches nothing); a failed restore is a `Notice`, never a failed resume, and `Restore` creates a missing root (a reboot empties tmpfs). Archives live in `<configDir>/archive/claude-tmp/` (dir 0700, files 0600) and are never pruned. `<name>.zip` is the only file a resume restores; a Pause, Kill or sweep that finds one already there demotes it to `<name>.<UTC stamp>.zip`. Claude names the dir after its **physical** cwd, so `claudetmp.Names` encodes both the resolved path and the stored one, and `Locate` fails closed on zero or several matches. `SweepClaudeTemp` is queued by `reconcileOrphans` on every workspace load (claim set snapshotted then) and dispatched by the health tick on `gateClaudeTmp`, one in flight. It takes a dir only when all of these hold: its name carries this config dir's encoded worktrees prefix and no other known config dir's (the encoding is lossy: `foo_bar` and `foo-bar` collide); it ends in a worktree timestamp and is not truncated; no record claims it; and, rechecked just before archiving, no directory under the config dir's `worktrees*` dirs encodes to it. A live session's worktree exists before its Claude creates the dir, so a live dir always fails that last check. `internal/testenv.IsolateLoomDirs` points `CLAUDE_CODE_TMPDIR` at a throwaway dir so no test reaches the real root. Opt-in contract test (an interactive Claude in a private tmux server, since `claude -p` gets no scratchpad): `LOOM_TEST_REAL_CLAUDE=1 go test ./session -run TestRealClaude_TempDirLayout` (a few cents).
```

- [ ] **Step 3: CLAUDE.md, Persistent State.** After the `hooks/` entry, add:

```markdown
- `archive/claude-tmp/` — zips of Claude's per-session temp dirs (scratchpad, task output, minus `CACHEDIR.TAG` caches): one `<name>.zip` per paused session (restored and deleted on resume), plus permanent archives from kills, sweeps and demoted parks; never pruned by loom
```

- [ ] **Step 4: USAGE.md, Session Lifecycle.** In **Running → Paused**, add item `6. Claude's temp dir for the session (its scratchpad and task output under /tmp/claude-<uid>/) is zipped into the archive and removed — see [Claude Temp-Dir Archives](#claude-temp-dir-archives)`. In **Paused → Running**, add `6. Claude's archived scratchpad is restored before the agent starts`. In **Running/Paused → Killed**, add `5. Claude's temp dir is archived (a paused session's archive simply stays)`.

- [ ] **Step 5: USAGE.md, Session Recovery.** After the "Stale leftovers" bullet, add:

```markdown
- **Locked worktrees** are left alone: a worktree you locked with `git worktree lock` is never auto-cleaned (or removed by pause or kill, which tell you to run `git worktree unlock`). A worktree left locked "initializing" by an interrupted `git worktree add` is unlocked and cleaned once the lock is older than six minutes.
```

- [ ] **Step 6: USAGE.md, new subsection** after **Subagent Tracking**:

```markdown
### Claude Temp-Dir Archives

Claude Code keeps a temp directory for every session, outside the worktree: `/tmp/claude-<uid>/<the session's directory, encoded>/<session id>/`, holding its scratchpad and background-task output (`$CLAUDE_CODE_TMPDIR`, `$TMPDIR`, `$TMP` or `$TEMP` move it). On many Linux systems `/tmp` is in RAM, and nothing removes these directories when a session ends, so loom archives them:

- **Pause** zips the session's temp dir and removes it; **resume** puts it back before the agent starts. If the restore fails, you see why, the archive is kept, and the session starts without it.
- **Kill** zips it and removes it.
- **At every workspace load**, loom sweeps the temp dirs of its sessions that no longer exist (worktrees it auto-cleaned, sessions killed before this feature) into the archive.
- Archives live in the workspace's loom folder: `<repo>/.loom/archive/claude-tmp/` for a registered workspace, otherwise `~/.loom/archive/claude-tmp/`. `loom debug` prints both the temp root and the archive folder.
- Directories marked with a valid `CACHEDIR.TAG` (cargo's `target/`, for example) are left out; the archive's `.loom-archive.json` lists what was skipped and how big it was. Symlinks are stored as links.
- Loom never deletes an archive. Prune `archive/claude-tmp/` by hand when you no longer need them. To look inside one: `unzip -l <file>.zip`.
```

### D4. Full verification, then commit

- [ ] **Step 1: Build, test, vet, format**

```bash
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test ./...
go vet ./...
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/') app/claude_tmp.go app/claude_tmp_test.go
CC=clang CGO_ENABLED=1 go test -race ./session/... ./app/...
```

Expected: build and every test pass; vet clean; gofmt prints nothing; race passes.

- [ ] **Step 2: End-to-end smoke suite** (needs tmux): `go test -tags e2e ./e2e/...`. Expected: PASS.

- [ ] **Step 3: Check the real temp root was never touched by the tests.** `ls /tmp/claude-$(id -u)` must still list the implementer's own session dir, and no `archive/claude-tmp` should have appeared under this repo's `.loom/`: `ls .loom/archive 2>/dev/null` prints nothing, or the dir doesn't exist. If either check fails, a test escaped its isolation. Find it before committing.

- [ ] **Step 4: Commit**

```bash
git add app/claude_tmp.go app/claude_tmp_test.go app/app.go app/pollgate.go main.go CLAUDE.md USAGE.md
git commit -m "feat(app): sweep orphaned Claude temp dirs into archives at workspace load

reconcileOrphans queues a sweep of the loaded workspace's Claude temp
dirs, with its claim set, on every load path; the health tick dispatches
it on its own gate, one in flight, so the first start clears the backlog
in the background. loom debug prints the temp root and archive dir;
CLAUDE.md and USAGE.md cover locks, archives and where they live.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Spec coverage

| Spec item | Where |
|---|---|
| `unlockStaleInit` (no `.git` → notLocked; `rev-parse --absolute-git-dir`; stale `initializing` → unlock; else kept) | A1 |
| Callers: `RemoveOrphanWorktree` (sentinel), `cleanup` (Kill), `Remove` (Pause) with the remedy message | A1 step 4, A2 |
| `reconcileOrphans`: debug log, not counted cleaned | A2 |
| `removeWorktree`/`clearWorktreePath` unchanged; intact locked tree still refused | A1 step 4, `TestSetup_KeepsAStaleLockOfALiveTree` |
| `Root`, `DirName`, `Locate` (deepest existing ancestor; both forms; unique prefix) | B1 |
| Archive layout (0700 dir, 0600 file, name without prefix or leading `-`, manifest) | B2 |
| `Archive` (no symlink following, skip special files, CACHEDIR.TAG, BestSpeed, `.partial` + O_TRUNC, fsync, rename, delete after, chmod retry, cleanup on error) | B2 |
| `Restore` (manifest name, refuse existing, `os.Root`, modes + symlinks, rename, delete zip) | B3 (+ decision 3) |
| File-name lifecycle (demote on collision; parked zip stays on kill) | B2 `ArchiveAs`/`Parked`, C1 tests |
| Pause after checkpoint; Kill after Cleanup, workspace terminals skipped; Resume both launch paths, reattach skipped, Notice on failure | C1 |
| Sweep: gated Cmd after `reconcileOrphans` on every load path, claim snapshot, one in flight, queued pass, checks 1–4 | C2, D1 (+ decisions 4–6, 8) |
| Logging `claudetmp.archived` / `archive_failed` / `restore_failed`; `loom debug` | C1, D2 |
| Docs (CLAUDE.md gotcha + Persistent State; USAGE.md) | D3 |
| Testing: testenv isolation; git, claudetmp, session, app tests; contract test | A1–A2, B1–B4, C1–C2, D1 |
