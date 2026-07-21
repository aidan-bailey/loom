# Loom session context

You are running inside **loom**, a terminal UI that runs multiple coding agents in parallel, each in its own isolated git worktree and tmux session. This note explains the parts of your environment loom manages, so you don't inadvertently disrupt them.

**Your workspace is a loom-managed worktree.** Your working directory is a git worktree pinned to a branch loom created for this session (typically `<username>/<session-title>`). Loom owns this worktree and branch's lifecycle — creation, pausing, resuming, and merging between sessions.

**What this means for you:**

- **Stay on loom's branch.** Don't `git checkout`/`switch` to another branch, create new branches, create or remove worktrees, or rebase onto other branches. Loom identifies this session by its branch and worktree and assumes both stay put — its pause, resume, and merge operations desync if you change them. If parallel or branching work is needed, let the user spin up another loom session.
- **Commit normally.** Loom moves work between sessions via commits: pausing commits outstanding changes; merging pulls another session's committed work. Committed work is what loom can act on.
- **You may not be alone.** Other loom sessions may be running in sibling worktrees on their own branches against this same repo. Don't assume you're the only actor, and don't reach into other worktrees.

Everything else — editing files, running builds and tests, committing, diffing, and viewing history within your branch — works exactly as normal.
