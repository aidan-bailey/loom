# The diff probe on a private index

Feedback thread 17 (`loom feedback show 17`). Design agreed with the owner on 2026-11-11.

## Problem

In a session worktree, `git merge --no-edit main` (107 commits) failed with "Another git process seems to be running in this repository" and "Merge with strategy ort failed". The merge result was left in the worktree and index, with no `MERGE_HEAD` and no commit, so a retried merge refused, and recovering took `git write-tree`, `commit-tree` and `update-ref` by hand.

The cause is loom's diff probe (`session/git/diff.go`, `session/git/patch.go`). It runs `git add -N .` on the worktree's real index, which takes `index.lock`, and `git diff <base>` can take the same lock briefly to refresh stat info. Any git command an agent starts while the probe holds the lock fails. Checking for a merge in progress first can't fix it, because the collision happens when the probe holds the lock at the moment the agent's command starts.

## Decision

The probe never touches the worktree's real index. It works on a private copy:

- **Where the copy lives:** in git's admin directory for the worktree (`git rev-parse --git-dir`, i.e. `<repo>/.git/worktrees/<name>/`), under a name of loom's own, e.g. `loom-diff-index`. Git ignores files it doesn't know there, and `git worktree remove` and prune delete the directory with it.
- **What the probe runs:** every probe command (`ls-files --others`, `add -N .`, `diff --shortstat`, `diff`, and `Patch`'s diff) runs with `GIT_INDEX_FILE` set to the private copy and `GIT_OPTIONAL_LOCKS=0`. The copy's own lock (`loom-diff-index.lock`) is git's to take; it touches nothing an agent uses.
- **Refreshing the copy:** copy the real index into the private file (write to a temp name, then rename) when the real index has changed since the last copy (size and mtime), and before the first probe. Git replaces the real index by rename, so a read sees a whole file. When the worktree has no real index yet, start from an empty private index.
- **Cost:** the copy is a few hundred KB to about 1 MB (this repo's worktree indexes are about 350 KB; prader-rs's up to 780 KB). Today's untracked-file TTL cache (`ensureUntrackedStaged`) depends on `add -N` making files tracked in the real index, which no longer happens. Rework it so a steady-state probe (nothing changed) stays about as cheap as today, and say in the commit how many subprocesses and copies a steady-state probe costs before and after.
- **Workspace terminals** (`DiffUncommitted`, `DiffUncommittedShortStat`, run in the user's own checkout) keep showing tracked files only, and run with `GIT_OPTIONAL_LOCKS=0` too, so they stop taking the opportunistic lock in the main checkout.
- **Intent-to-add entries** that earlier probes left in agents' real indexes are left alone: removing them would take the lock again.

## What stays the same

The diff still shows untracked, non-ignored files as new files, in `Diff`, `DiffShortStat` and `Patch`, with the same output (`SplitPatch` and the session/git rule "Parse only `Patch`'s output" still hold). Push (`git add .` then commit) is unchanged.

## Docs that change with it

Every live doc that says loom runs `git add -N .` in the worktree, and draws a rule from it:

- `session/loom_context_worktree.md` (every agent's system prompt). Keep "stage and commit by path, never `git commit -a`, `git add -A` or `git add .`" and "keep scratch files out of the worktree": agents' worktrees still hold old intent-to-add entries, and a scratch file still shows in the session's diff while it exists. Drop the claim that loom adds index entries, and the ` D` cleanup advice that follows from it, or reword it for the old entries.
- Root `CLAUDE.md` (the "Keep scratch files out of the worktree" rule), `session/CLAUDE.md` (the loom-context rule names "the diff probe's `git add -N .`"), `review/CLAUDE.md` (the `.crit/` rule's reason: keep the rule, since an untracked `.crit/` would still show in the diff), `docs/claude/testing-pitfalls.md`, `docs/claude/adding-an-enforce-test.md`, and the `docs/claude/INDEX.md` line that summarises the pitfall.
- Leave design history (`docs/superpowers/`) alone, and `flow/plugin/agents/drafter.md`'s `git add -N <new files>` (that's an agent staging its own files before `git commit -- <paths>`).

Keep the `CLAUDE.md` files within their budgets (`go run ./tools/claudemd -v`).

## Tests

- A probe in a worktree with untracked files leaves the real index byte-for-byte unchanged, and never creates `index.lock`.
- With `index.lock` held, as an agent's merge would hold it (create the file), `Diff`, `DiffShortStat` and `Patch` still succeed, still show the untracked file, and leave the lock file in place.
- Untracked files still appear, a deleted untracked file disappears from the next probe, an ignored file never appears, and a file the real index stages shows the same as today.
- The private copy follows the real index: a commit or `git add` in the worktree is reflected in the next probe.
- Consider an enforce test that no `add -N` runs in `session/git` without `GIT_INDEX_FILE`.
