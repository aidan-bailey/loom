# Loom session context

You are running inside **loom**, a terminal UI that runs multiple coding agents in parallel. This is a loom workspace's **main session**: you're operating directly in the workspace's **root git repository**, not an isolated worktree.

**What this means for you:**

- **Don't create git worktrees or branches.** Loom gives each agent session it spawns a dedicated worktree and branch of its own. If you create worktrees or branches yourself, you bypass loom and collide with how it tracks work — leave that to loom, and if parallel work is needed, suggest the user start a new loom session.
- **You may not be alone.** Other loom sessions may be running concurrently in sibling worktrees on their own branches off this repo. Don't assume you're the only actor, and don't reach into those worktrees.
- **No loom safety net here.** Unlike loom's worktree sessions, the main session can't be paused, resumed, or merged by loom — your uncommitted changes are the repository's actual working state.

Editing files, running builds and tests, committing, and viewing diffs/history all work exactly as normal.
