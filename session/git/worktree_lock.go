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

// initializingLock is the lock reason `git worktree add` writes while it
// checks a tree out, and leaves behind when it is killed mid-checkout.
const initializingLock = "initializing"

// ErrWorktreeLocked matches (errors.Is) every *LockedError.
var ErrWorktreeLocked = errors.New("worktree is locked")

// LockedError is a removal refused because the worktree carries a lock
// loom respects. Its message names the command that lets loom remove it.
// Repo is not printed (the command runs from the tree itself); it is for
// callers.
type LockedError struct {
	Repo, Path, Reason string
	// UnlockErr is why loom could not remove a stale "initializing" lock it
	// had found; nil for a lock loom respects without trying to remove it.
	UnlockErr error
}

// Error implements error. The error bar this ends up in cuts a long
// message off, so the reason and the remedy come first and the worktree is
// named once, inside the command: `git -C <path> worktree unlock .`, which
// takes one path because the tree is where it runs (a LockedError only
// arises for a tree that has its .git). The reason is collapsed onto one
// line (a `git worktree lock --reason` may span several). An
// "initializing" lock is worded differently: the add that wrote it may
// still be checking the tree out, so the message does not invite
// unlocking it until the lock is old enough that loom does so by itself
// (staleInitLockAge). When loom found it that old and could not remove it
// (UnlockErr), no add is running and loom will not retry by itself: the
// message says what failed and leaves the unlock to the user.
func (e *LockedError) Error() string {
	reason := oneLine(e.Reason)
	if reason == "" {
		reason = "none given"
	}
	unlock := fmt.Sprintf("`git -C %s worktree unlock .`", shellQuote(e.Path))
	switch {
	case e.UnlockErr != nil:
		return fmt.Sprintf("worktree locked (reason: %s) by an interrupted `git worktree add`; loom could not unlock it (%s): %s",
			reason, oneLine(e.UnlockErr.Error()), unlock)
	case reason == initializingLock:
		return fmt.Sprintf("worktree locked (reason: %s) by a `git worktree add` that may still be checking it out; loom unlocks it by itself after %s, or if no add is running: %s",
			reason, minutesText(staleInitLockAge()), unlock)
	}
	return fmt.Sprintf("worktree locked (reason: %s); unlock it with %s to let loom remove it", reason, unlock)
}

// Is makes errors.Is(err, ErrWorktreeLocked) hold for a *LockedError.
func (e *LockedError) Is(target error) bool { return target == ErrWorktreeLocked }

// Unwrap returns the failure to remove a stale lock, nil when there is none.
func (e *LockedError) Unwrap() error { return e.UnlockErr }

// oneLine collapses every run of whitespace in s, newlines included, to a
// single space, so text git wrote over several lines stays on one.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// minutesText tells d in whole minutes, rounded up so "older than N
// minutes" never overstates the age: "1 minute", "6 minutes".
func minutesText(d time.Duration) string {
	n := int((d + time.Minute - 1) / time.Minute)
	if n == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", n)
}

// shellQuote quotes s for a POSIX shell (see session.shellQuote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// staleInitLockAge is the age past which an "initializing" lock cannot
// belong to a running `git worktree add`. Loom kills its own add at
// gitWorktreeAddTimeout, and the call does not return until git's checkout
// child has exited (Wait blocks on the output pipe the child inherited), so
// an add loom started is gone, not lingering, by that deadline; the minute
// on top covers clock and timestamp slack. A func so tests that shorten
// gitWorktreeAddTimeout move it too.
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
	if reason != initializingLock || time.Since(fi.ModTime()) < staleInitLockAge() {
		return lockKept, reason, nil
	}
	uctx, ucancel := context.WithTimeout(context.Background(), gitTimeout)
	defer ucancel()
	if out, err := r.CombinedOutput(internalexec.GitCommand(uctx, repoPath, "worktree", "unlock", worktreePath)); err != nil {
		return lockKept, reason, fmt.Errorf("git worktree unlock: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	log.For("git").Warn("git.worktree_stale_init_unlocked", "path", worktreePath, "locked_at", fi.ModTime().UTC().Format(time.RFC3339))
	return unlocked, reason, nil
}

// RefuseLocked readies worktreePath for deletion by a caller about to run
// `git worktree remove -f`, which refuses any locked tree: it removes a
// stale "initializing" lock (unlockStaleInit) and returns a *LockedError
// naming the remedy for any lock loom respects, or one it found stale but
// could not remove (logged at Warn: an anomaly the user may need to act
// on; the error carries the failure as UnlockErr). nil means go ahead. A
// lock it could not check at all is logged at Debug and let through,
// since the remove still refuses a locked tree itself.
//
// Only the paths that mean to delete a tree call it — orphan auto-clean,
// Kill (cleanup, which also serves CleanupFailedStart) and Pause (Remove)
// — never clearWorktreePath: there an intact tree must stay protected by
// its lock from a rebuild's remove.
func RefuseLocked(repoPath, worktreePath string, r CommandRunner) error {
	state, reason, err := unlockStaleInit(repoPath, worktreePath, r)
	if err != nil {
		if state == lockKept {
			log.For("git").Warn("worktree.stale_unlock_failed", "path", worktreePath, "err", err.Error())
		} else {
			log.For("git").Debug("worktree.lock_check_failed", "path", worktreePath, "err", err.Error())
		}
	}
	if state == lockKept {
		// An error alongside lockKept is a stale lock that would not come off.
		return &LockedError{Repo: repoPath, Path: worktreePath, Reason: reason, UnlockErr: err}
	}
	return nil
}
