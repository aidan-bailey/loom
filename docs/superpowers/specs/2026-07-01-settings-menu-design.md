# Settings Menu Design

*Date: 2026-07-01 · Branch: `aidanb/settings-menu`*

## Motivation

`config.json` (`config/config.go:73-90`) has no in-app editor — every field (`DefaultProgram`, `AutoYes`, `DaemonPollInterval`, `BranchPrefix`, `Profiles`, and the newly-merged `ClaudeRemoteControl`) can only be changed by hand-editing the file and restarting Loom. This adds a settings overlay that makes every one of those fields editable from the TUI, with changes taking effect immediately rather than requiring a restart.

## Goals

- Edit all `config.Config` fields from a new overlay: `DefaultProgram`, `AutoYes`, `DaemonPollInterval`, `BranchPrefix`, `Profiles` (full CRUD), `ClaudeRemoteControl`.
- Changes apply live in the running process and persist to disk immediately (no "restart to apply" for any field).
- Reachable via a new dedicated keybinding and from the `?` help screen.
- Claude-specific preferences (currently just Remote Control) live in their own drill-in subsection, separate from the general fields, so more Claude-specific settings can be added later without cluttering the main list.

## Non-goals

- No keybinding customization from the TUI — that stays exclusively in `~/.loom/scripts/*.lua` (`script/defaults.lua`).
- No new config fields beyond what already exists on `config.Config` after the `aidanb/remote-control-integration` merge.
- No change to *which* `config.json` is in play — the menu edits whatever `config.Config` is already resolved for the focused workspace (`m.appConfig`, sourced via `config.LoadConfigFrom(wsCtx.ConfigDir)`); workspace-scoping itself is unchanged.

## Design

### Entry point

Dispatch in Loom runs keys through the Lua engine (`app/app_scripts.go:dispatchScript`), so a new key is wired the same way `W` (workspace picker) is:

- `keys/keys.go`: new `KeySettings` `KeyName`, bound to `S` in `GlobalkeyBindings` (help-panel/menu-bar display only — mirrors `KeyWorkspace`).
- `script/defaults.lua`: `cs.bind("S", function() cs.actions.open_settings() end, { help = "settings" })`.
- `script/intent.go`: new `SettingsIntent struct{}` + `intent()` marker, alongside `WorkspacePickerIntent` (`script/intent.go:74-76`).
- `script/api_actions.go`: `actions.RawSetString("open_settings", ...)` enqueues `SettingsIntent{}` (pattern at `script/api_actions.go:230-232`).
- `app/app_scripts.go`: `case script.SettingsIntent: _, cmd = runOpenSettings(m)` in the intent switch (~`app/app_scripts.go:497`).
- `app/intents.go`: `runOpenSettings(m *home)` builds `overlay.NewSettingsOverlay(m.appConfig, m.rcAuth)`, calls `m.setOverlay(so, overlaySettings)` (new `overlayKind` in `app/overlay_host.go:17-23`), sets `m.state = stateSettings`.
- `app/help.go`: new row, `S — settings`.

### Main overlay & state

New `stateSettings` added to the `state` enum (`app/app.go:93-115`) and a `handleStateSettingsKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd)` handler added to the `handleKeyPress` switch, structurally identical to `handleStateWorkspaceKey` (`app/state_workspace_picker.go:14-50`).

New `ui/overlay/settingsOverlay.go`, modeled on `WorkspacePicker` (`ui/overlay/workspacePicker.go`): a vertical list with a cursor, `up/k`/`down/j` to navigate, `Esc`/`q` to close back to `stateDefault`.

```
> Default Program        claude
  Auto Yes               [ ]
  Daemon Poll Interval   1000 ms
  Branch Prefix          aidan/
  Profiles               (3)  →
  Claude Preferences          →
```

`SettingsOverlay` owns an internal sub-mode (browsing / editing-scalar / profiles-sub / claude-prefs-sub) and proxies `HandleKey` to whichever child is active — the same composition `TextInputOverlay` already uses for its embedded `profilePicker`/`branchPicker` (`ui/overlay/textInput.go:126-152`). No second top-level `state` is needed for the nested modes; only `stateSettings` is new.

### Editing scalar fields

- **Auto Yes** (bool): `Enter`/`space` toggles immediately, no confirm step.
- **Default Program**, **Branch Prefix** (string): `Enter` opens an embedded `TextInputOverlay` (`ui/overlay/textInput.go`) pre-filled via `NewTextInputOverlay(title, currentValue)`; its enter-button commits, `Esc` cancels back to browsing.
- **Daemon Poll Interval** (int): same text input; a non-numeric submission surfaces through the existing `m.errBox`/`handleError` path rather than a new validation mechanism.

### Profiles sub-screen

New `ui/overlay/profilesManager.go` — distinct from the existing `ui/overlay/profilePicker.go` (selection-only, embedded in the new-instance flow; left untouched). A list of `config.Profile{Name, Program}`:

- `n` — add (name then program, reusing the same `TextInputOverlay` multi-stop-focus pattern).
- `Enter`/`e` — edit selected.
- `d` — delete, routed through the existing `ui/overlay/confirmationOverlay.go`. Deleting the profile that is the current `DefaultProgram` is blocked with an error (`m.errBox`) — the user must change Default Program away from it first.
- a per-row marker/key sets a profile as default, writing `DefaultProgram = profile.Name`.
- `Esc` returns to the main settings list (not `stateDefault`).

### Claude Preferences sub-screen

New `ui/overlay/claudePreferences.go`. Entered from the "Claude Preferences →" row; `Esc` returns to the main settings list. Today it holds one row:

```
Remote Control    [x]   (blocked: not logged in — run `claude auth login`)
```

- Toggles `config.Config.ClaudeRemoteControl` (`config/config.go:84-97`, a `*bool` defaulting to enabled when nil): `Enter`/`space` sets it to `boolPtr(!cfg.RemoteControlEnabled())`.
- The blocked-auth hint is a straight read of `m.rcAuth` (`session.RemoteControlAuth`, cached once at startup in `app/app.go:172-175,314` via `session.DetectClaudeRemoteControlAuth`) — `m.rcAuth.Reason` when `m.rcAuth.Blocked()`. No new auth probing is introduced; the existing startup-time probe and the existing session-creation-time gating (`remoteControlBlocked`/the "start without remote control?" modal in `app/remote_control.go:28-61`) already handle the incompatible-auth case correctly once the toggle is flipped — toggling here only changes `RemoteControlEnabled()`, which `app/app.go:1655-1660` and `app/app.go:377-382` already read fresh at every new-session creation.
- This subsection exists specifically so more Claude-adapter-specific preferences can be added later without growing the main list — currently a single row, structured as its own screen rather than a flat toggle in the top-level list.

### Persistence and concurrency

Every committed edit (in any of the three screens) does two things:

1. Mutates `m.appConfig` in place. It is already `*config.Config` (`app/app.go:142`), so in-process readers see the change immediately — no pointer swap needed.
2. Calls the existing `config.SaveConfigTo(m.appConfig, m.activeCtx.ConfigDir)` (`config/config.go:233-245`, atomic write via `AtomicWriteFile`) to persist. Today this is only called during initialization; the settings overlay becomes its second caller.

**Required concurrency fix, in scope for this feature:** `config.Config` today is loaded once and never mutated after startup, so nothing races on it. `scriptHost.BranchPrefix()`/`DefaultProgram()` (`app/app_scripts.go:97-99`) read `m.appConfig.*` directly, and per the project's own audit these `Host` getters run on the Lua dispatch goroutine — a `tea.Cmd` body executing concurrently with `Update` (the same "tea.Cmd-goroutine race" class already fixed for `session.Storage` and `config.State`, see `config/state.go:56`). That read is safe today only because the field never changes underneath it; once the settings overlay makes `appConfig` genuinely mutable at runtime, it becomes a live, `-race`-detectable data race.

Fix: give `config.Config` the same treatment as `config.State` — an unexported `mu sync.RWMutex` field (unexported fields are skipped by `encoding/json`, exactly as `config.State.mu` already coexists with its JSON-tagged fields) plus locked accessor/mutator methods for every field the settings overlay writes. `scriptHost.BranchPrefix()` and `scriptHost.DefaultProgram()` switch from raw field reads to the locked accessors; call sites that only ever run on the main goroutine (view rendering, `GetProgram()`/`GetProfiles()` at instance-creation time, etc.) are unaffected and can keep reading fields directly, since `Update`/`View` never run concurrently with themselves.

### Daemon reload

The daemon (`daemon/daemon.go`) is a separate OS process (`daemon.LaunchDaemon`, spawned via `exec.Command`), not a goroutine — it reads `cfg.DaemonPollInterval` and `cfg.AutoYes` once at startup (`daemon/daemon.go:212-225`) and caches them locally; it does not observe later changes to the same `config.json` written by another process.

Fix, scoped to these two fields only (everything else in `config.Config` is read fresh by the TUI process itself and needs no daemon involvement): at the top of every tick, the daemon re-reads `config.LoadConfigFrom(configDir)` — the same call `session/git/worktree.go` already makes fresh on every worktree creation, so it's an established, cheap, atomic-write-safe pattern — and diffs the result against its cached copy. If `DaemonPollInterval` changed, the ticker is reset to the new duration; if `AutoYes` changed, the cached flag gating auto-confirm behavior is updated. No IPC or signal handling is introduced.

`ClaudeRemoteControl` needs no daemon-side change: it is read fresh from `m.appConfig` at every new-session creation inside the TUI process itself (`app/app.go:1655,1660`), never cached by the daemon.

## Testing

- `HandleKey`/state-transition tests for `SettingsOverlay`, `profilesManager`, and `claudePreferences`, in the style of existing overlay tests (e.g. `ui/overlay/workspacePicker_test.go` if present, or the pattern used for `ConfirmationOverlay`).
- A test for the daemon's poll-and-diff-reload logic (inject a fake `config.LoadConfigFrom` via the daemon's existing dependency-injection pattern, mirroring `NewTmuxSessionWithDeps`).
- A `config.Config` mutex regression test: concurrent goroutines calling a locked mutator and `scriptHost.BranchPrefix()`/`DefaultProgram()` under `go test -race`, to confirm the fix actually closes the gap rather than just adding a mutex that isn't exercised.
- Full-suite `CGO_ENABLED=1 go test -race ./...` before merge, given this touches the exact residual-race area already flagged in prior project audits.

## Open items to confirm during planning

- Exact styling for the "blocked" hint text on the Remote Control row (color, truncation if `m.rcAuth.Reason` is long).
- Whether `profilesManager`'s "set default" action is a dedicated key or reuses `Enter` with a second confirmation step.
- Whether `config.Config`'s locked accessors are generated per-field (`GetBranchPrefix`/`SetBranchPrefix`, etc.) or the overlay/scriptHost instead take a snapshot-then-swap approach; both satisfy the race fix, choose whichever is less boilerplate once the field list is finalized in implementation.
