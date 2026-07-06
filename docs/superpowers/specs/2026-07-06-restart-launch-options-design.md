# Restarting a Paused Session with Different Launch Options

*Date: 2026-07-06 · Branch: `aidanb/headroom-wrapping`*

## Motivation

The Headroom Wrap design
(`docs/superpowers/specs/2026-07-05-headroom-wrap-design.md`) added a
"Session Launch Options" modal (Remote Control / Permission Mode /
Model / Headroom Wrap) shown once, at instance creation. It explicitly
deferred a follow-up: letting the user change those same options on an
*existing* session, noting it would need "reverse-parsing an
instance's current `Program` string back into a `launchOptions` value"
plus instance-lifecycle handling for tmux teardown/rebuild and
uncommitted work.

Both prerequisites now exist, though this spec also changes one of
them. `Instance.Resume` already re-reads `i.Program` from scratch
whenever the tmux session doesn't exist (`startFreshWithRecovery`). And
`Instance.Pause` already preserves uncommitted worktree changes before
killing tmux and removing the worktree — today via an auto-commit
(`"[loom] update from '<title>' on <time> (paused)"`). This spec
replaces that with a `git stash` instead: an auto-commit pollutes the
branch's real history with synthetic checkpoints, and once this spec
gives users a reason to pause/resume more often (to change launch
options), that pollution gets more visible. So the remaining work is:
recover a `launchOptions` value (and the underlying bare program) from
an instance's composed `Program` string, wire a UI entry point that
edits it before resuming, and switch Pause/Resume's uncommitted-work
handling from commit to stash.

Separately, this also adds a fifth launch option, **Effort** — the
real Claude CLI takes `--effort <level>` (`low`/`medium`/`high`/`xhigh`/`max`),
not currently exposed anywhere in loom. It doesn't exist yet even at
instance *creation*, so it's added end-to-end (Claude Preferences,
creation-time Session Launch Options, and this spec's restart flow)
rather than restart-only — otherwise a new session could never set
effort at all, only change it later.

## Goals

- `Instance.Pause` preserves uncommitted work (tracked and untracked)
  via `git stash` instead of an auto-commit; `Instance.Resume` restores
  it. No synthetic "(paused)" commits land on the branch's real history
  anymore.
- The existing `c`/"checkout" keybinding — which today's help text
  already describes as "commit changes locally and pause session"; it
  *is* `Instance.Pause`, just named after the side effect (the branch
  becomes checkoutable elsewhere) rather than the mechanism — is
  renamed to `s`/"stash" throughout, to match the mechanism it now
  actually uses.
- New `Config.ClaudeEffort` field and `config.ClaudeEfforts` list
  (`"default"`, `"low"`, `"medium"`, `"high"`, `"xhigh"`, `"max"`),
  following the exact shape of `ClaudeModel`/`ClaudeModels`: `"default"`
  is a no-op (Claude's own default applies), editable as a fifth row in
  Claude Preferences, and added to `overlay.LaunchOptions` as a fifth
  field editable in the creation-time Session Launch Options modal.
- `Adapter.ApplyEffortFlag(program, effort string) string` — Claude
  inserts `--effort <level>` right after `parts[0]`, mirroring
  `ApplyModelFlag`/`ApplyPermissionModeFlag`; `aider`/`gemini`/`default`
  get a one-line no-op passthrough.
- A new keybinding, `R`, on a **Paused** instance opens the existing
  Session Launch Options modal, seeded with that instance's current
  launch options — now all five, Effort included — (reverse-parsed
  from its `Program` string).
- Confirming re-composes `Program` with the edited options and resumes
  the instance through the normal resume path (worktree setup,
  crash-recovery `--continue` injection, checkpoint save all
  unchanged).
- Canceling (Esc/ctrl+c) leaves the instance exactly as it was:
  Paused, `Program` unchanged, no tmux/worktree side effects.
- Remote-Control-blocked handling (`m.rcAuth`) behaves identically to
  the creation-time flow: if the new options would enable Remote
  Control but auth is blocked, a confirmation prompts to resume without
  it instead of silently failing.

## Non-goals

- **Running instances.** `R` only applies to `Paused` instances. A
  live session must be paused first (existing action) before its
  options can be changed — this avoids conflating "change options" with
  a silent kill-and-relaunch of a session the user didn't ask to stop.
- **Recoverable orphans.** Orphans surfaced inline (`session.Recoverable`)
  are out of scope; they go live only via the existing explicit recover
  action (`r`), which is a different code path (`ReconcileAndRestore`)
  than `Resume`.
- **Persisting per-instance options to `config.json`.** Same as the
  creation-time modal: purely ephemeral, scoped to that instance's
  `Program` string.
- **A profile/program picker in the restart modal.** Same five
  toggles as creation; choosing a different underlying agent binary is
  unchanged (out of scope here as it was there).
- **Auth/eligibility gating for Effort**, same precedent as Model:
  Claude is responsible for rejecting an invalid `--effort` value
  itself; loom doesn't pre-validate.
- **Exact round-trip fidelity for hand-edited `Program` strings.**
  Reverse-parsing only recognizes the flag shapes loom's own code
  inserts. Anything else (a manually added flag, unusual quoting) is
  left untouched in the recovered base program and simply doesn't
  surface as a toggle — never a hard failure, worst case the user
  corrects one row in the modal.
- **Automatic conflict resolution for a stash that no longer applies
  cleanly.** Shouldn't normally happen — nothing else commits to a
  paused instance's branch while it's parked (`IsBranchCheckedOut`
  already blocks resuming onto a branch someone switched to) — but if
  `git stash apply` ever does conflict, loom surfaces the error and
  leaves the conflict markers and the stash entry in place for the user
  to resolve manually, the same way git itself refuses to drop a stash
  that didn't apply cleanly.
- **Migrating already-Paused instances.** An instance paused before
  this change has no stash — its uncommitted work is already safely on
  the branch via the old auto-commit. Resume for those is unchanged
  (empty `StashRef` skips the apply step entirely).

## Design

### Pause/Resume: stash instead of commit (prerequisite)

**The hazard that shapes this:** `git stash`'s backing ref,
`refs/stash`, is a single stack shared by every worktree of the same
repository — not per-worktree. Verified directly: pushing a stash from
two different worktrees of one repo, `git stash list` shows both
entries, interleaved, from either worktree. Two loom instances (each a
worktree of the same parent repo) pausing around the same time would
share one stack, so a naive `git stash pop` ("apply whatever's on top")
on Resume could grab a *different* instance's stashed changes if it
was pushed in between. Everything below is built to avoid ever relying
on stack position.

- `session/git/worktree_git.go`: replace `CommitChanges` (called from
  `Instance.Pause`) with `StashChanges(message string) (sha string, err error)`:
  ```go
  // StashChanges snapshots the worktree's tracked and untracked changes
  // into a stash commit without touching the shared stash stack's
  // ordering semantics loom relies on: `git stash create` builds the
  // commit object without pushing it onto refs/stash at all, and the
  // returned SHA is what every later operation (store, apply, drop)
  // targets directly — never "top of stack", which could belong to a
  // different worktree of this repo by the time Resume runs.
  // `git stash store` then anchors that commit against gc (dangling
  // commits are prunable after gc.pruneExpire) and makes it visible via
  // `git stash list` for manual recovery if something goes wrong.
  // Returns "" (no error) if the worktree wasn't dirty.
  func (g *GitWorktree) StashChanges(message string) (string, error)
  ```
  Implementation: `git stash create --include-untracked -m <message>`
  (empty stdout ⇒ nothing to stash, matching `IsDirty`'s scope — tracked
  and untracked, same as today's `git add .` before commit) → if
  non-empty, `git stash store -m <message> <sha>` → return `sha`.
- `session/storage.go`: `GitWorktreeData` gains `StashRef string
  \`json:"stash_ref,omitempty"\`` — bump `CurrentSchemaVersion`, add the
  upgrade step in `session/storage_migrate.go:Migrate` (old records get
  `StashRef: ""`), update the fixture in
  `cmd/workspace_migrate_shape_test.go` (this repo's drift guard).
- `Instance.Pause`: call `StashChanges` instead of `IsDirty`+`CommitChanges`;
  persist the returned `sha` (empty string if nothing was dirty) onto
  the instance's `GitWorktreeData.StashRef` as part of the same
  `saveState` checkpoint Pause already does.
- `Instance.Resume`: after `gw.Setup()` recreates the worktree from the
  branch (clean — nothing was ever committed to it), if `StashRef != ""`:
  `git stash apply <StashRef>`. On a clean apply, find the matching
  `stash@{n}` (resolve each entry's SHA via `git rev-parse` and compare
  — `git stash list` doesn't print SHAs by default) and `git stash drop`
  it, then clear `StashRef`. On conflict/error, leave `StashRef` set and
  the stash entry in place, and surface the error through Resume's
  existing error-return path (same shape as today's "branch is checked
  out" / `ErrBranchGone` cases) — never silently drop a stash that
  didn't apply cleanly, mirroring `git stash pop`'s own safety
  behavior.

### Renaming "checkout" to "stash" (prerequisite)

`keys.KeyCheckout` (`c`) doesn't check anything out — it's the trigger
for `Instance.Pause` (`app/intents.go:runCheckoutSelectedOpts`, "the
parameterized pause path"), named after the side effect of pausing
(the branch becomes free to `git checkout` elsewhere) rather than the
mechanism. Now that the mechanism is `git stash`, the more direct name
is "stash." Pure rename, no behavior change beyond Pause's own stash
switch above — every identifier below keeps its existing shape, just
relabeled:

- `keys/keys.go`: `KeyCheckout` → `KeyStash`, key `"c"` → `"s"` (unused
  today — verified no existing binding claims bare `s`), help label
  `"checkout"` → `"stash"`.
- `app/intents.go`: `runCheckoutSelectedOpts` → `runStashSelectedOpts`.
- `app/help.go`: `helpTypeInstanceCheckout` → `helpTypeInstanceStash`,
  `checkoutCommandEntries` → `stashCommandEntries`; description strings
  "Checkout: commit changes locally and pause session" → "Stash: stash
  changes locally and pause session" (and the sibling variants in
  `generalHandoffEntries`/`instanceStartHandoffEntries`); the help
  screen's "Changes will be committed locally..." body text → "Changes
  will be stashed locally...".
- `script/intent.go`: `CheckoutIntent` → `StashIntent`.
- `script/api_actions.go`: Lua action `checkout_selected` →
  `stash_selected`.
- `app/app_scripts.go`: `script.CheckoutIntent` → `script.StashIntent`.
- `script/defaults.lua`: `cs.bind("c", ...)` → `cs.bind("s", ...)`,
  `checkout_selected` → `stash_selected`, help `"checkout"` → `"stash"`.
- `ui/menu.go`: `keys.KeyCheckout` → `keys.KeyStash` in the action-group
  slice.
- Docs reflecting *current* behavior — `CLAUDE.md`'s keybindings table,
  `USAGE.md`, `README.md` — updated to `s`/"stash". Historical spec/plan
  files under `docs/superpowers/plans/`, `docs/plans/`, and
  `CHANGELOG.md` are a record of past work and are not rewritten.
- No back-compat shim for scripts calling `cs.actions.checkout_selected`
  — this project has precedent for breaking script-facing renames
  outright (e.g. `feat!: remove the Auto Yes feature entirely`) rather
  than carrying dead aliases.

### Adding Effort as a launch option (prerequisite)

Mechanical addition following `Model`'s exact precedent, touching the
already-shipped headroom-wrap code rather than new files:

- `config/config.go`: `ClaudeEffort *string` field (nil ≡ `"default"`,
  same rationale as `ClaudePermissionMode`/`ClaudeModel` — a config.json
  predating this field must not suddenly inject a flag), `Effort()`
  accessor, `ClaudeEfforts = []string{"default", "low", "medium", "high", "xhigh", "max"}`.
- `session/agent/adapter.go`: new `ApplyEffortFlag(program, effort string) string`
  on `Adapter`. `session/agent/claude.go`: inserts `--effort <level>`
  right after `parts[0]`, no-op for `""`/`"default"`, idempotent —
  identical shape to `ApplyModelFlag`. `aider.go`/`gemini.go`/`default.go`:
  one-line no-op passthrough.
- `session/agent_restart.go`: `BuildEffortCommand(program, effort string) string`,
  alongside `BuildModelCommand`.
- `app/remote_control.go`: `overlay.LaunchOptions` gains `Effort string`;
  `effortProgram(effort, program string) string` alongside
  `modelProgram`; `launchOptionsFromConfig` reads `cfg.Effort()`;
  `applyLaunchOptions` composes it in the same run of flag-insertions as
  Permission Mode and Model (order among these three doesn't affect
  correctness — each inserts "right after parts[0]", so whichever runs
  last ends up closest to the binary name — only Headroom Wrap's
  outermost position is load-bearing).
- `ui/overlay/claudePreferences.go`: fifth row, `Effort < level >`,
  cycling `config.ClaudeEfforts` via the existing `nextInList` helper.
- `ui/overlay/sessionLaunchOptions.go`: fifth row in the creation-time
  modal, same cycling.

### Reverse-parsing (`session/agent_restart.go`)

```go
// ParseLaunchOptions decodes a composed Program string back into the
// overlay.LaunchOptions that produced it, plus the underlying bare
// program (binary path/name and any *other* flags) applyLaunchOptions
// would need to recompose it from scratch. It is the symmetric decode
// of applyLaunchOptions: strips the "headroom wrap " prefix, then
// scans tokens for --remote-control[=name], --permission-mode <mode>,
// --model <model>, and --effort <level>, removing each recognized flag
// (and its value token, where applicable) from the returned base
// program. Recomposing must start from a bare program —
// applyLaunchOptions's ApplyXFlag functions insert "right after
// parts[0]", so calling them again on an already-flagged string would
// insert --model after "headroom", or duplicate an existing
// --permission-mode. A token this doesn't recognize (e.g. a hand-added
// flag) is left in place in baseProgram and simply doesn't set the
// corresponding opts field — never an error.
func ParseLaunchOptions(program string) (opts overlay.LaunchOptions, baseProgram string)
```

Lives in `session` (not `app`) since it's the mechanical inverse of
`BuildHeadroomWrapCommand`/`BuildRemoteControlCommand`/etc. in the same
file, and needs no `app` package state.

### Trigger (`keys/keys.go`, `script/defaults.lua`)

New `KeyRestartWithOptions` bound to `R`, active only when
`GetSelectedInstance().GetStatus() == session.Paused` (mirrors the
existing `runResumeOrRecover` status check). Help screens get a row
next to "Resume a paused session."

### State/UI wiring (`app/state_launch_options.go`)

Reuses `stateLaunchOptions` and the `SessionLaunchOptions` overlay —
no new modal component. The one change: `handleStateLaunchOptionsKey`'s
cancel path currently always pops-and-kills the pending (not-yet-started)
instance, which is correct for instance *creation* but wrong for a
*restart* (Esc must leave the existing Paused instance untouched).

Fix: add a second stashed closure next to `m.pendingLaunchOptions`:

```go
// pendingLaunchOptionsCancel runs when the Session Launch Options
// modal is dismissed without confirming (Esc/ctrl+c). The creation
// flow (state_new.go/state_prompt.go) sets this to pop-and-kill the
// pending, not-yet-started instance. The restart flow
// (state_restart_options.go) sets it to a no-op dismiss, since the
// instance being edited already exists and must survive a cancel
// untouched.
pendingLaunchOptionsCancel func() (tea.Model, tea.Cmd)
```

`cancelLaunchOptions` calls `m.pendingLaunchOptionsCancel()` instead of
hardcoding pop-and-kill; both call sites that enter `stateLaunchOptions`
today set it to today's behavior, so creation is unchanged.

### Confirm flow (`app/state_restart_options.go`, new)

Triggering `R`:

1. Guard: selected instance must be `Paused` (silently no-op otherwise,
   same as pressing `r` on a non-Paused instance today).
2. `opts, base := session.ParseLaunchOptions(selected.Program)`.
3. Set `m.pendingLaunchOptions` to a closure that:
   - Recomposes: `selected.Program = applyLaunchOptions(newOpts, m.rcAuth, base, selected.Title)`.
   - Delegates to the existing `runResumeSelected(m)` — inherits the
     Paused→Loading transition, `Resume`'s worktree setup and
     `--continue` injection, and the storage checkpoint save.
4. Set `m.pendingLaunchOptionsCancel` to a closure that just dismisses
   the overlay and returns to `stateDefault` (no pop/kill).
5. Open `overlay.NewSessionLaunchOptions(opts, m.rcAuth.Blocked(), m.rcAuth.Reason)`,
   transition to `stateLaunchOptions`.

### Remote-Control-blocked handling

Identical shape to creation: if `m.remoteControlBlocked(effectiveRemoteControl(newOpts), selected.Program)`,
show `m.promptRemoteControlBlocked` instead of resuming directly. Its
confirm branch (resume without RC) calls the same recompose-then-resume
path with RC forced off; its cancel branch returns to the Paused
instance untouched (not the creation flow's kill-pending).

## Testing

- Rename sweep across existing tests referencing the old `checkout`
  naming — `app/migration_parity_test.go`, `app/app_scripts_dispatch_test.go`,
  `app/actions_test.go`, `script/intent_test.go`,
  `script/api_actions_test.go` — updated in place to the `Stash`/`stash`
  names, no new assertions needed (behavior is unchanged, only names).
- `session/git/worktree_git_test.go`: `TestStashChanges_TrackedAndUntracked`
  (both survive, `IsDirty` false immediately after — `stash create`
  doesn't touch the working tree, so assert the working tree is
  *unchanged*, not clean), `TestStashChanges_CleanWorktreeReturnsEmpty`,
  `TestStashChanges_ConcurrentWorktreesDoNotInterfere` (two
  `GitWorktree`s on the same repo each stash independently; each one's
  returned SHA applies its own changes, not the other's — the
  regression test for the shared-`refs/stash` hazard this design is
  built around).
- `session/instance_test.go`: `TestPauseResume_PreservesUncommittedWork`
  (tracked + untracked, round-trips through Pause→Resume with no
  auto-commit landing in `git log`); `TestPauseResume_CleanWorktreeNoOp`
  (no stash created/applied when there's nothing to preserve);
  `TestResume_StashApplyConflictSurfacesError` (conflicting apply
  returns an error, leaves `StashRef` set, doesn't drop the stash
  entry).
- `session/storage_migrate_test.go` (or wherever existing schema-bump
  tests live): old-schema record without `stash_ref` migrates to
  `StashRef: ""` cleanly. Update `cmd/workspace_migrate_shape_test.go`'s
  fixture for the new field.
- `config/config_test.go`: `TestEffort`, `TestClaudeEfforts` — mirroring
  `TestModel`/`TestClaudeModels`.
- `session/agent/adapter_test.go`: `TestClaudeEffortFlag` (insertion,
  idempotence, `""`/`"default"` no-op, composes with `--model`/
  `--permission-mode`), `TestNonClaudeAdaptersNoEffortFlag`.
- `session/agent_restart_test.go`: `TestBuildEffortCommand_*`, and
  `TestParseLaunchOptions_RoundTrip` — a representative cross-section
  of the five options (each enum's full value set at least once, each
  boolean both ways, at least one all-on and one all-off case)
  round-trips through `ParseLaunchOptions(applyLaunchOptions(opts, authOK, "claude", "t"))`
  back to `opts` and a `baseProgram` of `"claude"`. Plus targeted cases:
  absolute-path base program preserved, unrecognized trailing flag left
  in `baseProgram` and ignored, empty/bare program.
- `ui/overlay/claudePreferences_test.go` / `sessionLaunchOptions_test.go`:
  extend row navigation/cycling coverage to five rows.
- `app/state_restart_options_test.go` (new): `R` on a non-Paused
  instance is a no-op; confirm applies new options (including Effort)
  and drives Paused→Loading→Running; cancel leaves the instance
  `Paused` with `Program` unchanged and no overlay; blocked-RC-via-modal
  routes to the confirm dialog and its cancel branch also leaves the
  instance untouched (distinct from creation's kill-pending cancel).
- `app/state_launch_options_test.go`: extend to cover
  `pendingLaunchOptionsCancel` — creation flow's cancel still
  pops-and-kills; a stubbed restart-style cancel closure runs instead
  of the hardcoded behavior.
