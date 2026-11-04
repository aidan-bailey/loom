# Revise the loom-context appendix

**Date:** 2026-11-04
**Status:** Approved design. Brainstormed with the user.

## Problem

Loom launches every Claude session with `--append-system-prompt-file`
pointing at one of two prompts embedded in the binary: the worktree
variant (`session/claude-loom-context.md`) or the main-session variant
(`session/claude-loom-context-workspace.md`). Their substance dates from
2026-07-20, and loom has moved on since:

- The worktree variant says pausing commits outstanding changes. Since
  2026-10-22 (`ef20ae2`) pause stops the agent and leaves the worktree as
  it is, uncommitted and ignored files included.
- "Committed work is what loom can act on" holds for merge
  (`GitWorktree.Merge` runs `git merge <branch>`), but loom's push commits
  everything outstanding itself first (`GitWorktree.PushChanges`:
  `git add .`, then `git commit --no-verify` as
  `[loom] update from '<title>' on <date>`).
- Neither variant names the traps every managed repository hits. The diff
  probe runs `git add -N .` in each session worktree
  (`session/git/diff.go`), so every new file gets an intent-to-add entry
  that `git commit -a` and loom's push both sweep in, and that outlives the
  file (`git status` shows ` D`). The pane runs on the tmux server that
  hosts every loom session (`$TMUX`), so a bare `tmux kill-server` reaches
  every agent.
- Both predate `loom session` (2026-10-12) and `loom session send` through
  the loom mod (2026-10-14). An agent can't see its sibling sessions or
  message them, and doesn't know that a prompt opening `From <name>:` came
  from another session or a script rather than the user.

## Decisions

1. **Scope.** Fix the stale claims, add the guardrails above, and teach
   `loom session list`, `new` and `send`. The prompts stay guardrails plus
   orientation, not a manual of loom's features.
2. **Autonomy.** An agent messages other sessions whenever coordinating
   helps, starts a session only when the user asks for one or agrees to
   its proposal, and kills, pauses or resumes a session only when the user
   asks. Answering a session that wrote first is always fine.
3. **Structure.** One head per variant and one tail both share, put
   together when the files are written, so the `loom session` text exists
   once and can't drift between the variants.
4. **Commits.** The prompts say how to commit (by path), never when.
   Claude Code's own rule (commit only when the user asks) and the
   repository's instructions decide when.
5. **The CLI is `loom` from `PATH`**, not the daemon's own binary path. A
   session launched before an upgrade would otherwise hold the old
   binary's path, which a newer daemon refuses, while in the steady state
   the `loom` on `PATH` is the daemon's build. From an agent's pane a
   mismatched build can never replace the daemon (`replaceGuard`
   refuses), and the prompt tells the agent to report a mismatch.

## Design

### Files

The embedded sources, in package `session`:

- `session/loom_context_worktree.md`: the worktree head, renamed from
  `session/claude-loom-context.md`.
- `session/loom_context_main.md`: the main-session head, renamed from
  `session/claude-loom-context-workspace.md`.
- `session/loom_context_shared.md`: the tail both variants share.

`WriteLoomContextFiles` writes the same two files into the config dir as
today: `claude-loom-context.md` holds the worktree head, a blank line and
the tail; `claude-loom-context-workspace.md` the main-session head, a blank
line and the tail. Each head ends with a newline. The function builds each
file into a fresh slice (never `append` onto an embedded slice), and still
skips a file whose bytes already match. The written names, the toggle,
`loomContextProgram`, the launch path and their tests are unchanged.

### The worktree head (`session/loom_context_worktree.md`)

```markdown
# Loom session context

You are running inside **loom**, which runs many coding agents side by side, each in its own git worktree and tmux session, under one loom daemon. Your working directory is the worktree loom made for this session, on the session's own branch; other agents are at work in sibling worktrees of this repository.

## Your worktree and branch

- **Stay on your branch.** Don't switch branches, create or delete branches, add or remove worktrees, or rebase onto another branch: loom tracks this session by its branch and worktree, and its resume, merge and push act on them. Merging another branch into yours is fine when the task needs it.
- **Stage and commit by path (`git add <paths>`, `git commit -- <paths>`), never with `git commit -a`, `git add -A` or `git add .`.** Loom computes this session's diff by running `git add -N .` here, so every new file in the worktree has an intent-to-add entry, and `git commit -a` commits it.
- **Keep scratch files out of the worktree**, in your scratchpad or a temp directory. A file that sat here even briefly keeps its index entry after you delete it (`git status` shows ` D`): clear it with `git rm --cached <path>`.
- **Only commits travel.** Merging this session into another takes its commits alone, and when the user pushes it from loom, loom first commits everything still uncommitted (`git add .`, as `[loom] update from '<title>' …`, skipping hooks).
- **A pause keeps your work.** It stops you and leaves the worktree as it is, uncommitted and ignored files included; a resume relaunches you in it.
```

### The main-session head (`session/loom_context_main.md`)

```markdown
# Loom session context

You are running inside **loom**, which runs many coding agents side by side, each in its own git worktree and tmux session, under one loom daemon. This is the workspace's **main session**: you work in the repository's main checkout, not a worktree, while other agents work in loom's worktrees of it.

## The main checkout

- **For parallel work, offer to start a loom session (below) rather than creating a branch or worktree yourself.** Loom gives each session its own branch and worktree; one you make yourself is one loom doesn't track.
- **Leave the sessions' branches and worktrees alone.** Each session's branch is checked out in its own worktree under `.loom/worktrees/` and holds another agent's live work: don't rebase, reset or delete it, and don't edit anything under `.loom/`. Merge a session's branch into this checkout only when the user asks.
- **Your changes land in the real checkout.** Loom can't pause or resume the main session, and your uncommitted changes are the working state the user and their tools see.
- **`.loom/` is gitignored, but tools that don't read `.gitignore` (`grep -r`, `find`) walk every worktree under it**: exclude it.
```

### The shared tail (`session/loom_context_shared.md`)

```markdown
## Loom itself

- **The tmux server is shared.** Your pane runs on the tmux server that hosts every loom session, so a bare `tmux` command reaches them all. Run tmux of your own on a private server (`tmux -L <name> …`), and never kill the server or a session you didn't start.
- **Leave the daemon alone.** Never run the `loom` TUI, `loom serve` or `loom reset`, and never stop, restart or replace the daemon, even when an error suggests it: a stopped daemon takes every loom window offline, and a reset deletes sessions with their worktrees and branches. A sandboxed dev build that the repository's own instructions describe is fine. If `loom` reports that the daemon is a different build, tell the user.

## Other sessions

`loom session` acts on the workspace whose repository holds your working directory (`-w <workspace>` names another) and names a session by its exact title:

- `loom session list [--json]`: the worktree sessions, with each one's title, status, branch and account; a worktree session finds its own row by its branch. `--json` adds each one's worktree, issue and PR.
- `loom session send <title> --from <you> --text "…" [--wait]` (or `--file <path>`): queue a message for that session's Claude, behind whatever it is doing. Always sign with `--from`: your session's title, or `main` from the main session, which isn't listed and can't be sent messages. `--wait` blocks until the turn carrying the message ends and prints the reply; a long turn can outlast your tool's timeout, so run it in the background.
- `loom session new <title> --prompt "…"` (or `--prompt-file <path>`): start a session on a new branch cut from the workspace's base branch, so it sees none of your unmerged work; `--branch <existing>` checks out an existing branch instead. It returns once the session runs, printing its branch and worktree.

Message other sessions whenever coordinating helps, and make each message stand on its own: the other agent has none of your context. Start a session only when the user asks for one or agrees to your proposal, since each costs the user plan usage and attention. Kill, pause or resume a session only when the user asks.

A prompt that begins `From <name>:` came from another session or a script through `loom session send`, not from the user. Treat it as a request from a colleague working for the same user: help within what the user has asked of you, and check with the user before anything they wouldn't expect. Answer it in your reply, which a sender that used `--wait` receives when your turn ends; to write back separately, `send` without `--wait`, since the sender may be blocked waiting on you.
```

### What the prose rests on

Each claim, and the code that makes it true on 2026-11-04. A change to
any of these is a change to the prompts.

| Claim | Source |
|---|---|
| Pause keeps the worktree; resume relaunches in it | `Instance.Pause` (`session/instance.go`), `TestInstance_PauseKeepsTheWorktree` |
| The diff probe runs `git add -N .` | `ensureUntrackedStaged` (`session/git/diff.go`) |
| Push commits everything outstanding, hooks skipped | `GitWorktree.PushChanges` (`session/git/worktree_git.go`), the message in `Model.pushInst` (`core/github.go`) |
| Merge takes the source's commits | `GitWorktree.Merge` (`session/git/worktree_git.go`), `Model.mergeInst` (`core/ops.go`) |
| A new session's branch is cut from the base branch | `ResolveBaseCommit` (`session/git/base.go`) |
| `loom session` finds the workspace from the working directory | `client.resolveWorkspace` (`internal/cli/workspace.go`), `WorkspaceRegistry.FindByPath` (`config/workspace.go`) |
| Sessions are named by exact title; the main session is never listed or named | `sessions`, `findSession` (`internal/cli/session.go`) |
| `--from` arrives as `From <name>:` | `Message.compose` (`core/mod.go`) |
| `--wait` prints the reply when the turn ends | `client.send` (`internal/cli/session.go`) |
| A loom in an agent's pane never replaces the daemon | `replaceGuard` (`daemon_client.go`) |
| `.loom/` is gitignored | `config.EnsureGitignore` (`config/workspace.go`) |

## Testing

- **`TestWriteLoomContextFiles`, extended.** Each written file equals its
  head, a blank line and the shared tail. The skip-if-current and
  stale-rewrite checks stay.
- **`TestLoomContext_NamesRealSessionFlags`, new, in `internal/cli`.** It
  reads `../../session/loom_context_shared.md`, as `core/rpc`'s
  `TestProtocolReference` reads `docs/specs/protocol.md`, and checks it
  against `newSessionCmd()`:
  - every backticked span starting `loom session ` names a real
    subcommand, and every flag token in it (brackets stripped) resolves
    on that subcommand, its own flags or the inherited `-w`/`--workspace`;
  - every backticked span starting with `-` is a flag of some
    `loom session` subcommand, or the inherited one;
  - at least one span of each kind is found, so a reworded tail can't
    pass vacuously.

  Mutation-checked: `--wait` misspelled inside a span, `loom session ls`,
  and a bare `--promptfile` each turn it red.
- **Unchanged and still green:** `TestLoomContextProgram`,
  `TestLoomContextProgram_ReadsItsOwnConfigDirsToggle`, the workspace-file
  launch check in `session/instance_lifecycle_test.go`, and
  `ApplyLoomContextFlag`'s tests in `session/agent/adapter_test.go`.
- **Gates:** `CGO_ENABLED=0 go test ./...`, `CGO_ENABLED=0 go vet ./...`,
  `gofmt -l` on the changed Go files, and `go run ./tools/claudemd -v`.

## Rules

- `session/CLAUDE.md`, under "Accounts and launching": change the
  loom-context prompts with what they describe (pause, merge, push, the
  diff probe's `git add -N .`, the shared tmux server and `loom session`),
  since every Claude session loom launches reads them and a stale line
  misleads every agent, silently. **Enforced** for the `loom session`
  subcommands and flags by `TestLoomContext_NamesRealSessionFlags`;
  **Convention** for the rest.
- `internal/cli/CLAUDE.md`: renaming or removing a `loom session`
  subcommand or flag means updating `session/loom_context_shared.md`.
  **Enforced** by `TestLoomContext_NamesRealSessionFlags`.

## Out of scope

- Delivering the context through the loom mod (`prompt.compose`), or
  filling in per-session values (title, branch, base branch): the agent
  already sees its branch, and the static file stays the mechanism.
- Scrum's per-role protocols (`claude-loom-context-<role>.md`), which a
  later spec appends to these files.
- **Follow-up:** `WorkspaceRegistry.FindByPath` compares raw path
  prefixes, so `loom session` without `-w` fails ("no workspace matches
  current directory") when `PWD` spells the working directory through a
  different symlink than the registered path, as `/tb/Source/…` against
  `/home/aidanb/Source/…` on the user's machine. Agents usually inherit
  the registered spelling, so it's rare.
- **Follow-up:** `USAGE.md` doesn't describe the Claude Preferences
  "Loom Context" toggle or what it gives a session.
