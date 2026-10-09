# session/git

Git worktree operations: each session's worktree under `<configDir>/worktrees/` on a `{username}/{session_title}` branch, its setup, inspection, diff stats, parity, push, stashes, locks and cleanup. Facts in the `git` section of [`../README.md`](../README.md).

## Rules when modifying this package

### Intact trees

- **Call a tree intact only when `.git` is present and `git rev-parse` confirms a linked worktree of this repository, and classify by the `.git` entry first.** A directory without `.git` is gutted, which resume rebuilds after moving the leftovers aside; git run inside one walks up and answers for the enclosing repo, so without the check the top-level comparison (`sameFile`) only reports it unverified and resume refuses what a rebuild would serve. `--show-toplevel` must be the path itself, `--git-common-dir` must match the repo's and `--git-dir` differ from it, and a tree still locked `initializing` is not intact. **Enforced** by `TestInspectTree` (its "gutted inside the repo it belongs to" case), `TestInspectTree_OnlyThisRepositorysLinkedWorktreeIsIntact`, `TestInspectTree_InterruptedWorktreeAdd` and `TestInspectTree_DanglingGitfile`.
- **Report a path that can't be stat'ed or a timed-out probe as unverified, never as another repository's tree or as gutted.** Resume refuses an unverified tree; reading it as gutted rebuilds it with `remove -f` and deletes uncommitted work. **Enforced** by `TestInspectTree_StatFailureIsNotAnotherRepository` and `TestInspectTree_TimeoutIsReportedAsSuch`.
- **Never send an intact or unverified tree into `Setup`; its callers are the guard.** `clearWorktreePath` runs `git worktree remove -f` on whatever sits at the path, which deletes an unlocked, intact, dirty tree without a word; it refuses only when `.git` survives the remove (a locked tree), and moves a gutted tree's leftovers aside. `decideResume` sends only an absent or gutted tree to a rebuild, and `CrashRestart` refuses a non-intact one. **Convention** — uncommitted work deleted silently; `TestSetup_RefusesToDeleteLiveWorktree` covers only a locked tree, and `TestSetup_PreservesGuttedWorktreeContents` the move-aside. Recovery and triage: [`../../docs/claude/incident-triage.md`](../../docs/claude/incident-triage.md)

### Locks

- **On the deleting paths (`RefuseLocked`, from `GitWorktree.Cleanup`/`Remove` and orphan auto-clean), unlock only an `initializing` lock older than `gitWorktreeAddTimeout` plus a minute.** Any other lock, a user's or a young `initializing` one, gets a `*git.LockedError` naming `git worktree unlock` before cleanup touches anything, branch and title sidecar included. **Enforced** by the `TestUnlockStaleInit_…` tests and `TestCleanupAndRemove_RefuseARespectedLock`.
- **Have `clearWorktreePath` (Setup, rebuild) unlock a lock, of any kind, only when `nothingLiveAt` holds** (absent, not a directory, or no `.git`; a path that can't be stat'ed counts as live). A tree with its `.git` may hold work, and its lock is what stops the single `remove -f` from deleting it. **Enforced** by `TestSetup_KeepsTheLockOfALiveTree`, `TestSetup_KeepsAStaleLockOfALiveTree` and `TestSetup_UnreadableTreeKeepsItsLock`.
- **Run `worktree add` under its own `gitWorktreeAddTimeout` (`addWorktree`), never the tick's short budget.** A kill mid-checkout leaves the tree locked `initializing`, which makes `remove -f` refuse it, prune skip it and `worktree add` refuse the path. **Enforced** by `TestWorktreeAdd_NotBoundByTickTimeout` and `TestWorktreeAdd_HasItsOwnDeadline`.

### Stashes

- **Have `DropStash` verify the SHA git reports dropping, store a wrong entry back, and retry.** `refs/stash` is shared by every worktree of the repo and `git stash drop` takes only a shifting stash@{N} position, so a blind drop deletes another session's stash; a failed store-back is retried once, then named in the error with the `git stash store` that restores it. **Enforced** by `TestDropStash_ConcurrentPushKeepsOtherEntry`, `TestDropStash_StoreBackIsRetried` and `TestDropStash_StoreBackFailureNamesTheCommit`.
- **Have `StashListed` fail, never answer "not listed", when the list can't be read.** Callers forget a stash that isn't listed, and a misread forgets the only copy of work. **Enforced** by `TestStashListed_LookupFailureIsNeverAbsence` and `TestStashListed_ListFailureIsAnError`.
- **Return `ErrStashNotDropped` from `ApplyStash` when the apply worked but the drop didn't.** The caller clears the reference and reports the leftover entry as a notice instead of failing the resume. **Enforced** by `TestApplyStash_DropFailureIsReported`.

### Branches and sidecars

- **Write the `.loom-title` sidecar beside the worktree directory, never inside it, and remove it in `Cleanup`.** Inside, it would pollute `git status`; orphan discovery needs it to recover the exact title and tmux session name, since branch-derived titles are lossy. **Enforced** by `TestWorktreeTitleSidecarPath` and `TestSetup_WritesTitleSidecar`.
- **Delete on a failed setup only the branch this setup created.** A reused title checks out a branch an earlier session left behind, with its work. **Enforced** by `TestSetupNewWorktree_DeletesNoBranch` and, in `session`, `TestStart_FailedStartKeepsAPreexistingBranch`.
- **Classify git failures by their English stderr only through commands built by `internalexec.GitCommand`.** It forces git's message language to C; a command built otherwise returns the user's locale and `isBranchAbsentErr`/`isWorktreeAbsentErr` misclassify silently. **Enforced** by `TestNoRawGitGhExec` (`internal/exec/command_enforce_test.go`).

## Pointers

- [`../CLAUDE.md`](../CLAUDE.md) — resume decisions and failed-start cleanup built on these operations.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
