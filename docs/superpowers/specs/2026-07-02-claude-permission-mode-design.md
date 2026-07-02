# Claude Permission Mode Setting

*Date: 2026-07-02 · Branch: `aidanb/claude-automode`*

## Motivation

Claude Code's CLI accepts `--permission-mode <mode>` to control how much a session can do without prompting (`default`, `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions`). Today the only way to use it in Loom is to hand-edit a profile's `Program` string (`config.Profile.Program`, `config/config.go:64-71`) and remember to keep it in sync everywhere. This adds a persistent, in-app setting — following the same pattern as `ClaudeRemoteControl` (`config/config.go:96-101`) — so every new Claude session launches with the user's preferred default mode without editing command strings by hand.

## Goals

- New `Config` field, `ClaudePermissionMode`, editable from the existing "Claude Preferences" settings sub-screen (`ui/overlay/claudePreferences.go`) alongside Remote Control.
- All six modes Claude Code supports are selectable: `default`, `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions`.
- New Claude sessions get `--permission-mode <mode>` injected into their launch command the same way `--remote-control` is today — once, at instance-creation time, persisted via the instance's stored `Program` string so pause/resume and crash recovery inherit it automatically.
- Non-Claude adapters (aider, gemini, default fallback) are unaffected — the flag is Claude-specific.

## Non-goals

- No confirmation/warning dialog for `bypassPermissions` or any other mode — the cycle-through row behaves identically for every value, consistent with the Remote Control checkbox having no confirmation today.
- No per-profile or per-instance override UI. The setting is a single global default, same scope as `ClaudeRemoteControl`. A user who wants a different mode for one profile can still hand-edit that profile's `Program` string — this feature doesn't change or interact with that path.
- No auth/eligibility gating (unlike Remote Control's `remoteControlBlocked`/`m.rcAuth` machinery). `--permission-mode` doesn't depend on a detected login state the way remote control does; if a chosen mode is unavailable for the user's account, Claude Code itself is responsible for rejecting or falling back.

## Design

### Config field

`config/config.go`, alongside `ClaudeRemoteControl`:

```go
// ClaudePermissionMode is the --permission-mode value new Claude
// sessions launch with. Unlike ClaudeRemoteControl, DefaultConfig sets
// this explicitly to "default" rather than leaving it nil — nil only
// occurs for a config.json predating this field, and is treated
// identically to "default" (no flag injected; Claude's own default
// applies). Read it through PermissionMode.
ClaudePermissionMode *string `json:"claude_permission_mode,omitempty"`
```

```go
// PermissionMode returns the configured --permission-mode value,
// defaulting to "default" when unset (nil). Deliberately unlocked, like
// RemoteControlEnabled — see the corrected note below.
func (c *Config) PermissionMode() string {
	if c.ClaudePermissionMode == nil {
		return "default"
	}
	return *c.ClaudePermissionMode
}
```

**Correction (found during implementation):** the first draft of this accessor took `c.mu.RLock()`, copying `GetBranchPrefix`'s pattern instead of `RemoteControlEnabled`'s. That deadlocks: the Claude Preferences cycle handler (below) calls `cc.PermissionMode()` *from inside* `cfg.Mutate(...)`, which already holds `c.mu` write-locked, and `sync.RWMutex` isn't reentrant. `GetBranchPrefix` is locked because it's read from the Lua dispatch goroutine (`scriptHost.BranchPrefix()`) concurrently with the main goroutine's `Mutate` calls; `PermissionMode` has no such caller — it's read only on the main goroutine, same as `RemoteControlEnabled`, so it stays unlocked.

`DefaultConfig()` (`config/config.go:167-187`) gains `ClaudePermissionMode: stringPtr("default")` next to `ClaudeRemoteControl: boolPtr(true)`, plus a `stringPtr` helper mirroring `boolPtr` (`config/config.go:189-191`).

The six valid values live as a shared slice so both the config layer and the UI cycle over the same list without duplicating it:

```go
// ClaudePermissionModes lists the values --permission-mode accepts, in
// the order the Claude Preferences screen cycles through them.
var ClaudePermissionModes = []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"}
```

### Adapter interface & Claude implementation

`session/agent/adapter.go`, new `Adapter` method alongside `ApplyRemoteControlFlag`:

```go
// ApplyPermissionModeFlag returns the program string with
// "--permission-mode <mode>" inserted (e.g. "claude --permission-mode
// acceptEdits"). mode == "" or "default" is a no-op — Claude's own
// default already matches. Idempotent: if --permission-mode is already
// present, the input is returned unchanged. Returns the input unchanged
// for agents without a permission-mode concept.
ApplyPermissionModeFlag(program, mode string) string
```

`session/agent/claude.go`, implemented with the same shape as `ApplyRemoteControlFlag` (`session/agent/claude.go:62-82`): fields-split, scan `parts[1:]` for an existing `--permission-mode`/`--permission-mode=` flag, early-return unchanged; otherwise insert `--permission-mode <mode>` right after `parts[0]`. No sanitization is needed — `mode` only ever comes from `ClaudePermissionModes`, never free-typed user input.

`aider.go`, `gemini.go`, `default.go` each get a one-line no-op matching their existing `ApplyRemoteControlFlag(program, _ string) string { return program }` (`session/agent/aider.go:38`, `session/agent/gemini.go:37`, `session/agent/default.go:32`).

### Application site

`session/agent_restart.go`, new function alongside `BuildRemoteControlCommand` (`session/agent_restart.go:20-28`):

```go
// BuildPermissionModeCommand modifies a program command string to
// launch with the given --permission-mode value. The adapter registry
// decides whether and how the string is modified. Idempotent, and a
// no-op for agents without a permission-mode concept or when mode is
// "" / "default".
func BuildPermissionModeCommand(program, mode string) string {
	return defaultRegistry.Lookup(program).ApplyPermissionModeFlag(program, mode)
}
```

`app/remote_control.go`, new helper next to `remoteControlProgram` (`app/remote_control.go:12-26`):

```go
// permissionModeProgram returns program with Claude's --permission-mode
// flag applied per cfg.PermissionMode(). No-op when cfg is nil or the
// program isn't Claude (BuildPermissionModeCommand's registry lookup
// already no-ops for non-Claude adapters).
func permissionModeProgram(cfg *config.Config, program string) string {
	if cfg == nil {
		return program
	}
	return session.BuildPermissionModeCommand(program, cfg.PermissionMode())
}
```

All four call sites of `remoteControlProgram` wrap that same call in `permissionModeProgram(cfg, ...)`, since permission mode applies wherever remote control does — new instances go through the same "apply Claude-specific launch flags once, at the point the title is known" moment:

- `app/app.go:393` — auto-created workspace terminal on non-interactive startup.
- `app/app.go:1696` — workspace terminal created on workspace switch.
- `app/state_new.go:59` — regular new instance (`n` key), inside the `startTask.Sync` callback.
- `app/state_prompt.go:59` — new instance with an initial prompt (`N` key / Shift+N flow), inside the `startTask.Sync` callback.

Each becomes, e.g. for `app/app.go:393`:

```go
Program: permissionModeProgram(appConfig, remoteControlProgram(appConfig, h.rcAuth, program, wtTitle)),
```

and for `app/state_new.go:59`:

```go
instance.Program = permissionModeProgram(m.appConfig, remoteControlProgram(m.appConfig, m.rcAuth, instance.Program, instance.Title))
```

Because both `Apply*Flag` implementations insert immediately after `parts[0]` (the bare `claude` token), composing them this way still produces a single well-formed command (e.g. `claude --permission-mode acceptEdits --remote-control my-title`) regardless of which wrapper runs first — each only scans for its own flag name, so the two never interfere. This mirrors the existing note on `remoteControlProgram` (`app/remote_control.go:18-20`): applying at first-launch time means the rewritten command is persisted on the instance and inherited by `BuildRecoveryCommand` on later resume/crash restarts, with no separate recovery-path wiring needed.

### Settings UI

`ui/overlay/claudePreferences.go` gains a second row and a focus cursor between the two rows (today there's exactly one row, so no navigation exists yet):

```
Claude Preferences

Remote Control    [x]
Permission Mode   < acceptEdits >

up/down move • enter/space toggle/cycle • esc back
```

- `ClaudePreferences` struct gains `cursor int` (0 = Remote Control, 1 = Permission Mode).
- `HandleKeyPress`: `up`/`k` and `down`/`j` move `cursor` between the two rows (clamped, no wraparound — consistent with `SettingsOverlay`'s own list navigation). `space`/`enter` on cursor 0 keeps today's toggle behavior; on cursor 1, it cycles `cfg.ClaudePermissionMode` to the next value in `config.ClaudePermissionModes` (wrapping from `bypassPermissions` back to `default`), written via the existing `cfg.Mutate(...)` pattern (`ui/overlay/claudePreferences.go:47-50`).
- `Render` shows a `›`/highlight on whichever row is focused (matching how `SettingsOverlay`'s own cursor row is rendered), plus the current `cfg.PermissionMode()` value in angle brackets on the second row.

### Testing

- `config/config_test.go`: `TestPermissionMode` mirroring `TestRemoteControlEnabled` (`config/config_test.go:346-365`) — nil defaults to `"default"`, `DefaultConfig()` sets `"default"` explicitly, explicit values round-trip.
- `session/agent/adapter_test.go`: `TestClaudePermissionModeFlag` mirroring `TestClaudeRemoteControlFlag` (`session/agent/adapter_test.go:65-99`) — insertion, idempotence (flag already present), `""`/`"default"` no-op, flag preserved alongside a pre-existing `--remote-control`.
- `session/agent_restart_test.go`: cases for `BuildPermissionModeCommand`, mirroring the existing `BuildRemoteControlCommand` cases (`session/agent_restart_test.go:59-72`).
- `ui/overlay/claudePreferences_test.go`: extend `TestClaudePreferencesTogglesRemoteControl` (`ui/overlay/claudePreferences_test.go:12-24`) coverage with row-navigation (cursor moves on up/down) and cycling (enter on row 1 advances through all six values and wraps).

## Open items to confirm during planning

- Exact key(s) for row navigation on the Claude Preferences screen — plain `up`/`down` only, or also `k`/`j` like `SettingsOverlay`'s main list (recommend matching `SettingsOverlay` for consistency).
- Whether `ClaudePermissionModes` lives in the `config` package (co-located with `PermissionMode()`) or a shared `agent` package constant the UI imports instead — either satisfies "UI and config agree on the list," pick whichever avoids an import cycle once file layout is finalized.
