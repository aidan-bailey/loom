# Loom session context

You are running inside **loom**, a terminal UI that runs multiple coding agents in parallel, each in its own isolated git worktree and tmux session. This note explains the parts of your environment loom manages, so you don't inadvertently disrupt them.

**Your workspace is a loom-managed worktree.** Your working directory is a git worktree pinned to a branch loom created for this session (typically `<username>/<session-title>`). Loom owns this worktree and branch's lifecycle — creation, pausing, resuming, and merging between sessions.

**What this means for you:**

- **Stay on loom's branch.** Don't `git checkout`/`switch` to another branch, create new branches, create or remove worktrees, or rebase onto other branches. Loom identifies this session by its branch and worktree and assumes both stay put — its pause, resume, and merge operations desync if you change them. If parallel or branching work is needed, let the user spin up another loom session.
- **Commit normally.** Loom moves work between sessions via commits: pausing commits outstanding changes; merging pulls another session's committed work. Committed work is what loom can act on.
- **You may not be alone.** Other loom sessions may be running in sibling worktrees on their own branches against this same repo. Don't assume you're the only actor, and don't reach into other worktrees.

**Heavy work goes through loom's work queue.** This machine is shared by many loom sessions. Run builds, full test suites and benchmarks with `loom job submit -d "<what>" -- <cmd>` and carry on: a `[loom] job N` message brings the result. Or use `loom job run -d "<what>" -- <cmd>` with Bash's `run_in_background` when you need the output to continue. A job gets 4 cores and 6 GB unless you claim more with `--cpus`/`--mem`, `--io` for full builds that write a lot, or `--exclusive` for benchmarks. Quick commands (`cargo check`, linters, git) run directly.

Everything else — editing files, committing, diffing, and viewing history within your branch — works exactly as normal.
