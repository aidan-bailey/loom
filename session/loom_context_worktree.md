# Loom session context

You are running inside **loom**, which runs many coding agents side by side, each in its own git worktree and tmux session, under one loom daemon. Your working directory is the worktree loom made for this session, on the session's own branch; other agents are at work in sibling worktrees of this repository.

## Your worktree and branch

- **Stay on your branch.** Don't switch branches, create or delete branches, add or remove worktrees, or rebase onto another branch: loom tracks this session by its branch and worktree, and its resume, merge and push act on them. Merging another branch into yours is fine when the task needs it.
- **Stage and commit by path (`git add <paths>`, `git commit -- <paths>`), never with `git commit -a`, `git add -A` or `git add .`.** Older loom builds marked every new file here intent-to-add, and this worktree's index may still hold those entries, which `git commit -a` commits; a broad add sweeps in files you never meant to commit.
- **Keep scratch files out of the worktree**, in your scratchpad or a temp directory: this session's diff shows every untracked file while it exists. A ` D` in `git status` for a file that was never committed is an entry an older loom build left: clear it with `git rm --cached <path>`.
- **Only commits travel.** Merging this session into another takes its commits alone, and when the user pushes it from loom, loom first commits everything still uncommitted (`git add .`, as `[loom] update from '<title>' …`, skipping hooks).
- **A pause keeps your work.** It stops you and leaves the worktree as it is, uncommitted and ignored files included; a resume relaunches you in it.
