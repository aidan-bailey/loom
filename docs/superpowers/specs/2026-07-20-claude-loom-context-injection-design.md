# Design: Inject loom environment context into Claude sessions

- **Date:** 2026-07-20
- **Status:** Approved (design); pending implementation plan
- **Branch:** `aidanb/loom-system-prompt`

## Summary

Loom launches every Claude session with `--append-system-prompt-file <path>`,
pointing at a loom-owned markdown file that tells the agent it is running inside
loom and how to avoid disrupting loom's git lifecycle. Two variants are shipped:
one for isolated **worktree sessions**, one for the **workspace terminal** (the
root-repo "main session"). On by default, loom-owned content, no-op for
non-Claude agents.

## Motivation

A Claude session launched by loom can already *observe* facts about its
environment (its cwd is under `.loom/worktrees/…`, its branch is
`<username>/<title>`) via Claude Code's own dynamic environment injection. What
it lacks is the **semantics and guardrails** those facts imply. Loom's core
invariants live outside the agent's view:

- Worktree sessions must stay pinned to their branch — loom's pause, resume, and
  merge operate on that branch and worktree, and desync if the agent switches
  branches, creates/removes worktrees, or rebases.
- Work moves between sessions via commits (pause commits outstanding work; merge
  pulls another session's committed work).
- Worktree/branch *creation* is loom's job — each spawned session gets its own.
  An agent that creates its own branches/worktrees bypasses loom, collides with
  the `<username>/<title>` scheme, and leaves untracked state.

The concrete win is preventing the agent from fighting the harness. A secondary
win is orienting the agent to the parallel, multi-agent nature of the workspace.

## Decisions (from brainstorming)

1. **Purpose / scope:** Guardrails **+ context** — lifecycle guardrails plus
   generic parallel-agent and commit-model orientation. Not a loom user manual
   (no feature/keybinding evangelism).
2. **Default:** **On.** `ClaudeLoomContext *bool` with **nil = enabled**,
   mirroring `Config.RemoteControlEnabled()`.
3. **Ownership:** **Loom-owned, fixed.** Loom writes/overwrites the content from
   embedded bytes; users wanting their own global instructions use `CLAUDE.md`
   or their own program flags. (A future "loom base + user append" extension is
   possible but out of scope.)
4. **Workspace terminals:** Receive a **tailored variant** (not the worktree
   prompt, and not nothing) that states the truth about the root repo and
   explicitly tells the agent **not to create worktrees or branches**.
5. **Branch-creation guardrail:** Applied to **both** prompts.

## Mechanism

### Why launch-time injection (not the per-session flag composition)

The existing four Claude flags (`--remote-control`, `--permission-mode`,
`--model`, `--effort`) are **per-session** options: composed into the persisted
`Instance.Program` at first launch via `applyLaunchOptions`
(`app/remote_control.go`) and round-tripped by `ParseLaunchOptions`.

Loom-context is different in two ways that both point away from that path:

- It is **global and static**, not a per-session choice — it does not belong in
  the `overlay.LaunchOptions` shape or the `ParseLaunchOptions` round-trip.
- Its gate ("is this a worktree session?") is a property of the **instance**,
  known cleanly only at launch. Composing at the app layer would force the gate
  into the two workspace-terminal creation sites and drag a global setting
  through per-session plumbing.

Instead, loom-context is applied at launch, in the same place `InstanceEnv`
already applies env-var launch tweaks (`session/instance.go`, where the
`TmuxSession` is constructed) — driven by a **global enabled flag** wired
app → session, exactly like the existing `tmux.SetNotifier` package-level hook.
Nothing is persisted per-instance, so **there is no `InstanceData` schema
change**.

### Components

- **Embedded content** (`session/loom_context.go`, package `session`):
  - `//go:embed claude-loom-context.md` → worktree variant bytes.
  - `//go:embed claude-loom-context-workspace.md` → workspace-terminal variant
    bytes.
  - `var loomContextEnabled atomic.Bool` + `SetLoomContextEnabled(bool)` — the
    global toggle; `atomic.Bool` keeps it race-safe (settings toggle writes on
    the main goroutine; launch reads it). Wired from config in `app.Run` and
    updated when the Claude Preferences toggle changes.
  - `WriteLoomContextFiles(configDir string) error` — writes each file to
    `{configDir}/…` when missing or when the on-disk bytes differ from the
    embedded bytes (so a loom upgrade that changes the prose refreshes the file
    automatically; no per-launch write when current).
  - `loomContextProgram(program, configDir string, isWorkspaceTerminal bool) string`
    — returns `program` unchanged when `!loomContextEnabled.Load()`; otherwise
    selects the workspace-terminal file when `isWorkspaceTerminal` else the
    worktree file, and returns `BuildLoomContextCommand(program, filepath.Join(configDir, name))`.

- **Adapter flag** (`session/agent/claude.go` + `adapter.go` interface):
  - New method `ApplyLoomContextFlag(program, filePath string) string`, mirroring
    the existing `Apply*Flag` methods: inserts `--append-system-prompt-file
    <filePath>` after the command token via `insertAfterCommand`. No-op impls in
    `aider.go`, `gemini.go`, `default.go`.
  - `session.BuildLoomContextCommand(program, filePath string)` wrapper
    (`session/agent_restart.go` or `session/loom_context.go`) — registry lookup,
    so it no-ops for non-Claude programs.

- **Launch site** (`session/instance.go`): before constructing the
  `TmuxSession`, replace `i.Program` with
  `loomContextProgram(i.Program, i.ConfigDir, i.IsWorkspaceTerminal)`. The result
  is passed to `tmux.NewTmuxSession`; `i.Program` itself is **not** mutated, so
  the flag is re-derived every launch (first launch, resume, crash-restart) and
  always reflects the current global toggle and content.

- **App wiring** (`app/app.go` and the Claude Preferences handler):
  `session.SetLoomContextEnabled(appConfig.LoomContextEnabled())` at startup and
  whenever the preference is toggled. `session.WriteLoomContextFiles(configDir)`
  runs on the **workspace-activation path** (not just once at startup), using the
  same `wsCtx.ConfigDir` each instance is later constructed with (`ConfigDir:
  wsCtx.ConfigDir`) — this guarantees the directory the launch reads from
  (`i.ConfigDir`) already holds the files, and the content-check makes the
  repeated call a no-op once current. (Loom's config dir is effectively the
  global loom home today, but keying the write to the activation path avoids a
  latent bug should config dirs ever diverge per workspace.)

### Coexistence with a user-supplied append-file

A user may already have their own `--append-system-prompt-file` in their
`DefaultProgram`/profile. `ApplyLoomContextFlag`'s idempotency check keys on
**loom's own path**, not the generic flag, so it still adds loom's file when a
*different* append-file is present (both flags passed). This relies on Claude
accepting repeated `--append-system-prompt-file` flags additively.

> **Pre-implementation verification task:** confirm `claude` accepts multiple
> `--append-system-prompt-file` flags and concatenates them (rather than
> last-wins). If it does not, fall back to skip-if-any-`--append-system-prompt-file`-present
> and document that a user append-file suppresses loom's context.

## The injected prompts

### A) Worktree sessions — `claude-loom-context.md`

```markdown
# Loom session context

You are running inside **loom**, a terminal UI that runs multiple coding agents
in parallel, each in its own isolated git worktree and tmux session. This note
explains the parts of your environment loom manages, so you don't inadvertently
disrupt them.

**Your workspace is a loom-managed worktree.** Your working directory is a git
worktree pinned to a branch loom created for this session (typically
`<username>/<session-title>`). Loom owns this worktree and branch's lifecycle —
creation, pausing, resuming, and merging between sessions.

**What this means for you:**

- **Stay on loom's branch.** Don't `git checkout`/`switch` to another branch,
  create new branches, create or remove worktrees, or rebase onto other
  branches. Loom identifies this session by its branch and worktree and assumes
  both stay put — its pause, resume, and merge operations desync if you change
  them. If parallel or branching work is needed, let the user spin up another
  loom session.
- **Commit normally.** Loom moves work between sessions via commits: pausing
  commits outstanding changes; merging pulls another session's committed work.
  Committed work is what loom can act on.
- **You may not be alone.** Other loom sessions may be running in sibling
  worktrees on their own branches against this same repo. Don't assume you're
  the only actor, and don't reach into other worktrees.

Everything else — editing files, running builds and tests, committing, diffing,
and viewing history within your branch — works exactly as normal.
```

### B) Workspace terminal — `claude-loom-context-workspace.md`

```markdown
# Loom session context

You are running inside **loom**, a terminal UI that runs multiple coding agents
in parallel. This is a loom workspace's **main session**: you're operating
directly in the workspace's **root git repository**, not an isolated worktree.

**What this means for you:**

- **Don't create git worktrees or branches.** Loom gives each agent session it
  spawns a dedicated worktree and branch of its own. If you create worktrees or
  branches yourself, you bypass loom and collide with how it tracks work — leave
  that to loom, and if parallel work is needed, suggest the user start a new
  loom session.
- **You may not be alone.** Other loom sessions may be running concurrently in
  sibling worktrees on their own branches off this repo. Don't assume you're the
  only actor, and don't reach into those worktrees.
- **No loom safety net here.** Unlike loom's worktree sessions, the main session
  can't be paused, resumed, or merged by loom — your uncommitted changes are the
  repository's actual working state.

Editing files, running builds and tests, committing, and viewing diffs/history
all work exactly as normal.
```

## Config

`config/config.go`:

- New field `ClaudeLoomContext *bool` (`json:"claude_loom_context,omitempty"`).
- Helper `LoomContextEnabled() bool { return c.ClaudeLoomContext == nil || *c.ClaudeLoomContext }`
  (nil = on, identical shape to `RemoteControlEnabled()`).
- `DefaultConfig` leaves the field `nil` so existing configs get the feature on
  after upgrade without a rewrite. The Claude Preferences toggle writes an
  explicit `*bool`.

## UI

Add one toggle row — "Loom context" (on/off) — to the Claude Preferences overlay
(`ui/overlay/claudePreferences.go`), alongside Remote Control / Permission Mode /
Model / Effort. Toggling writes the config `*bool` and calls
`session.SetLoomContextEnabled`. **Not** surfaced in per-session launch options —
it is a global preference.

## Testing

- **Adapter** (`session/agent/adapter_test.go`): `ApplyLoomContextFlag` inserts
  the flag after the command; idempotent on loom's own path; coexists with a
  different user-supplied `--append-system-prompt-file`; no-op for
  aider/gemini/default.
- **Config** (`config/config_test.go`): `LoomContextEnabled()` returns true for
  nil, true for `*true`, false for `*false`.
- **File writer:** writes when missing; rewrites when on-disk bytes are stale;
  no write when current.
- **Launch selection:** worktree instance → worktree file; workspace terminal →
  workspace file; disabled toggle → program unchanged; non-Claude program →
  program unchanged.

## Scope / non-goals

- No per-session interpolation — content is fully static (`<username>/<session-title>`
  in prompt A is illustrative text, not substituted). Claude sees its real branch
  via its own dynamic env injection.
- No user-customizable content in v1 (deferrable to a "loom base + optional user
  append file" extension if requested).
- No `InstanceData` / `SchemaVersion` change — the setting is global config, not
  persisted per-instance.
- aider / gemini / default agents unaffected (adapter no-ops).

## Open questions / risks

- **Repeated `--append-system-prompt-file` behavior** — see the verification task
  under *Coexistence*.
- **Toggle semantics on running sessions** — because the enabled flag is read at
  launch, toggling the preference affects the *next* launch of any session
  (including resume of a paused one). This is intentional (a global guardrail
  should reflect current preference) and differs from the snapshot-at-creation
  semantics of the per-session flags.
- **Content staleness** — the prose describes loom's lifecycle; if that lifecycle
  changes, the embedded files must be updated in lockstep, or they will mislead.
  The write-on-content-change logic keeps the on-disk copy in sync with the
  binary, but the burden of keeping the prose accurate is on future changes to
  loom's worktree/branch model.
