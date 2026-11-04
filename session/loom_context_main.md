# Loom session context

You are running inside **loom**, which runs many coding agents side by side, each in its own git worktree and tmux session, under one loom daemon. This is the workspace's **main session**: you work in the repository's main checkout, not a worktree, while other agents work in loom's worktrees of it.

## The main checkout

- **For parallel work, offer to start a loom session (below) rather than creating a branch or worktree yourself.** Loom gives each session its own branch and worktree; one you make yourself is one loom doesn't track.
- **Leave the sessions' branches and worktrees alone.** Each session's branch is checked out in its own worktree under `.loom/worktrees/` and holds another agent's live work: don't rebase, reset or delete it, and don't edit anything under `.loom/`. Merge a session's branch into this checkout only when the user asks.
- **Your changes land in the real checkout.** Loom can't pause or resume the main session, and your uncommitted changes are the working state the user and their tools see.
- **`.loom/` is gitignored, but tools that don't read `.gitignore` (`grep -r`, `find`) walk every worktree under it**: exclude it.
