# review

The review comment model vendored from kevindutra/crit: comments and their YAML store under `.crit/` in the worktree, the code-review session manifest, and agent-prompt composition (`ComposePrompt`). `review/gitdiff/` parses per-file changed-line maps with go-gitdiff (`git -C <dir>`), separately from `session/git`'s worktree lifecycle. The pane that shows reviews is `ui/review/`.

## Rules when modifying this package

- **Keep crit's MIT attribution in `NOTICE.md` describing the code.** `review/`, `review/gitdiff/` and `ui/review/` derive from kevindutra/crit at a pinned commit, and the notice lists the substantial modifications; a file copied from upstream, or a rewrite that changes what loom took, updates that section. **Convention** — a licence notice that no longer matches the code.
- **Thread every path through the explicit worktree root, never the process's working directory.** The daemon runs in the global dir and a TUI wherever it started, so an upstream-style relative path reads or writes another worktree's `.crit/`, or none; `review/gitdiff` passes `git -C <dir>`. **Enforced** by `TestReviewPath_RootedAndStable` (`review/paths_test.go`) and `TestChangedFiles_RunsAgainstDirNotCwd` (`review/gitdiff/diff_test.go`).
- **Keep `.crit/` self-gitignored** (`EnsureDirs` writes its own `.gitignore`). Loom's diff probe runs `git add -N .` in every worktree and the agent commits from it, so without it review YAML shows in the session's diff and lands in its commits. **Enforced** by `TestEnsureDirs_WritesSelfIgnoringGitignore` and `TestStore_InvisibleToGitStatus` (`review/store_test.go`).

## Pointers

- [`../ui/CLAUDE.md`](../ui/CLAUDE.md) — the review pane's rules (`ui/review`).
- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where this package sits.
