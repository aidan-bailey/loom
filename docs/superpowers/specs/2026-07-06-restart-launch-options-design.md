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

Both prerequisites now exist. `Instance.Pause` already commits dirty
worktree changes before killing tmux, and `Instance.Resume` already
re-reads `i.Program` from scratch whenever the tmux session doesn't
exist (`startFreshWithRecovery`). So the remaining work is: recover a
`launchOptions` value (and the underlying bare program) from an
instance's composed `Program` string, and wire a UI entry point that
edits it before resuming.

## Goals

- A new keybinding, `R`, on a **Paused** instance opens the existing
  Session Launch Options modal, seeded with that instance's current
  launch options (reverse-parsed from its `Program` string).
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
- **A profile/program picker in the restart modal.** Same four
  toggles as creation; choosing a different underlying agent binary is
  unchanged (out of scope here as it was there).
- **Exact round-trip fidelity for hand-edited `Program` strings.**
  Reverse-parsing only recognizes the flag shapes loom's own code
  inserts. Anything else (a manually added flag, unusual quoting) is
  left untouched in the recovered base program and simply doesn't
  surface as a toggle — never a hard failure, worst case the user
  corrects one row in the modal.

## Design

### Reverse-parsing (`session/agent_restart.go`)

```go
// ParseLaunchOptions decodes a composed Program string back into the
// overlay.LaunchOptions that produced it, plus the underlying bare
// program (binary path/name and any *other* flags) applyLaunchOptions
// would need to recompose it from scratch. It is the symmetric decode
// of applyLaunchOptions: strips the "headroom wrap " prefix, then
// scans tokens for --remote-control[=name], --permission-mode <mode>,
// and --model <model>, removing each recognized flag (and its value
// token, where applicable) from the returned base program. Recomposing
// must start from a bare program — applyLaunchOptions's ApplyXFlag
// functions insert "right after parts[0]", so calling them again on an
// already-flagged string would insert --model after "headroom", or
// duplicate an existing --permission-mode. A token this doesn't
// recognize (e.g. a hand-added flag) is left in place in baseProgram
// and simply doesn't set the corresponding opts field — never an
// error.
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

- `session/agent_restart_test.go`: `TestParseLaunchOptions_RoundTrip`
  — for all 16 combinations of the four options,
  `ParseLaunchOptions(applyLaunchOptions(opts, authOK, "claude", "t"))`
  reproduces `opts` and a `baseProgram` of `"claude"`. Plus targeted
  cases: absolute-path base program preserved, unrecognized trailing
  flag left in `baseProgram` and ignored, empty/bare program.
- `app/state_restart_options_test.go` (new): `R` on a non-Paused
  instance is a no-op; confirm applies new options and drives
  Paused→Loading→Running; cancel leaves the instance `Paused` with
  `Program` unchanged and no overlay; blocked-RC-via-modal routes to
  the confirm dialog and its cancel branch also leaves the instance
  untouched (distinct from creation's kill-pending cancel).
- `app/state_launch_options_test.go`: extend to cover
  `pendingLaunchOptionsCancel` — creation flow's cancel still
  pops-and-kills; a stubbed restart-style cancel closure runs instead
  of the hardcoded behavior.
