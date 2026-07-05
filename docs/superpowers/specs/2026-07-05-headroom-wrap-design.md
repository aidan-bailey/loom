# Headroom Wrap, Claude Model, and Per-Instance Launch Options

*Date: 2026-07-05 · Branch: `aidanb/headroom-support`*

## Motivation

Loom already lets a user set two global Claude launch preferences —
`ClaudeRemoteControl` and `ClaudePermissionMode` (`config/config.go:96-108`,
edited from the "Claude Preferences" settings sub-screen,
`ui/overlay/claudePreferences.go`). This adds two more launch-time
capabilities:

- **Headroom Wrap** — wraps the entire launch command as `headroom wrap
  <program>`, so the Headroom context-compression tool sits in front of
  whatever agent is running. Unlike the two existing settings, this is
  agent-agnostic (it wraps Claude, Aider, Codex, or Amp identically) and
  incompatible with Claude's `--remote-control`: Headroom manages the
  process's stdio itself, so the two can't be layered.
- **Claude Model** — sets `--model <alias>` on Claude sessions, cycling
  through short CLI aliases (`sonnet`/`opus`/`haiku`) rather than pinned
  version IDs, so the setting doesn't go stale as new models ship.

Because Headroom Wrap can't coexist with Remote Control, and because
forcing every session to inherit whatever the *global* toggle state
happens to be is limiting once there's a real choice to make, this also
adds a small per-instance override: a modal shown right before a new
session starts, seeded from the global config, letting the user adjust
these four launch options for just that one session.

A follow-up feature — restarting an *existing* session to change its
launch options — was raised during this design and deliberately
deferred to its own spec (see "Non-goals" below); it reuses the pieces
built here but touches instance lifecycle (tmux teardown/rebuild,
uncommitted-work handling), which is a different scope.

## Goals

- New `Config` fields `HeadroomWrap` and `ClaudeModel`, editable from the
  existing Claude Preferences screen alongside Remote Control and
  Permission Mode (now four rows).
- Headroom Wrap works for every agent program, not just Claude.
- Headroom Wrap and Remote Control are mutually exclusive: enabling one
  disables the other, enforced both in the settings UI (so config.json
  never has both `true`) and defensively at launch-composition time (so
  a hand-edited config.json can't produce both flags either).
- Claude Model cycles `default`/`sonnet`/`opus`/`haiku`, injecting
  `--model <alias>` the same way Permission Mode injects
  `--permission-mode`. No-op for non-Claude adapters.
- A new "Session Launch Options" modal appears after title entry (both
  the `n` plain-new-instance flow and the `N` new-with-prompt flow),
  pre-filled from the current global config, letting the user toggle
  Remote Control / cycle Permission Mode / cycle Model / toggle Headroom
  Wrap for just that session before it starts. Esc cancels the whole
  new-instance flow, same as canceling title entry does today.
- Overriding launch options for one session never writes back to
  `config.json` — it's purely ephemeral, scoped to that instance's
  `Program` string.

## Non-goals

- **Restarting an existing session to change its launch options** is
  explicitly out of scope for this spec. It depends on reverse-parsing
  an instance's current `Program` string back into a `launchOptions`
  value and on new instance-lifecycle machinery (tmux teardown/rebuild,
  deciding what happens to uncommitted work) that creation-time
  composition doesn't need. To be brainstormed as its own spec once the
  `launchOptions`/`SessionLaunchOptions` pieces below exist to build on.
- No profile/program picker in the new modal — it only covers the four
  launch-flag toggles. Choosing a different program/profile for a new
  instance is unchanged (already possible via the profile picker inside
  the `N` prompt flow; the plain `n` flow has no program picker today
  and this spec doesn't add one).
- No confirmation dialog for any specific Headroom Wrap or Model value —
  same precedent as Remote Control's checkbox and Permission Mode's
  cycle, which have no confirmation today.
- No auth/eligibility gating for Headroom Wrap or Model, unlike Remote
  Control's `remoteControlBlocked`/`rcAuth` machinery. If Headroom isn't
  installed or a model alias is invalid, the wrapped/flagged command
  itself is responsible for failing — Loom doesn't pre-validate either.
- Headroom Wrap does not get an `Adapter` interface method. Because it's
  agent-agnostic (confirmed during brainstorming), a single top-level
  string-prefix function covers every program; adding a per-adapter
  method would be unused abstraction.

## Design

### Config layer (`config/config.go`)

Two new fields, alongside the existing two:

```go
// HeadroomWrap controls whether new sessions launch wrapped as
// `headroom wrap <program>`, regardless of agent. Unlike
// ClaudeRemoteControl, this defaults to off (DefaultConfig sets it
// explicitly to false) since it's an opt-in wrapper, not a
// backward-compatible default. Mutually exclusive with
// ClaudeRemoteControl: enabling one disables the other, enforced in
// the Claude Preferences toggle handler, the Session Launch Options
// modal, and defensively again in applyLaunchOptions so a
// hand-edited config.json with both fields true still can't launch
// both flags at once. Read it through HeadroomWrapEnabled.
HeadroomWrap *bool `json:"headroom_wrap,omitempty"`

// ClaudeModel is the --model value new Claude sessions launch with.
// Values are short CLI aliases (not versioned IDs) so the list stays
// valid as new models ship without a code change. "default" is a
// no-op — Claude's own default applies. Read it through Model.
ClaudeModel *string `json:"claude_model,omitempty"`
```

```go
func (c *Config) HeadroomWrapEnabled() bool {
	return c.HeadroomWrap != nil && *c.HeadroomWrap
}

func (c *Config) Model() string {
	if c.ClaudeModel == nil {
		return "default"
	}
	return *c.ClaudeModel
}
```

Both are unlocked, same rationale as `PermissionMode()`
(`config/config.go:142-149`): read only from the main goroutine,
including from inside the Claude Preferences `cfg.Mutate(...)` callback,
which isn't reentrant.

`ClaudeModels` lives next to `ClaudePermissionModes`:

```go
// ClaudeModels lists the --model aliases the Claude Preferences and
// Session Launch Options screens cycle through. Short aliases, not
// versioned IDs, so this list doesn't need updating when new Claude
// models ship.
var ClaudeModels = []string{"default", "sonnet", "opus", "haiku"}
```

`DefaultConfig()` gains `HeadroomWrap: boolPtr(false)` and
`ClaudeModel: stringPtr("default")`.

### Adapter layer — Model only

`--model` is Claude-specific, so it follows the exact shape of
`ApplyPermissionModeFlag` (`session/agent/claude.go:102-116`): new
`Adapter.ApplyModelFlag(program, model string) string` method
(`session/agent/adapter.go`), no-op for `""`/`"default"`, idempotent if
`--model` is already present, otherwise inserted right after `parts[0]`.
`aider.go`, `gemini.go`, `default.go` each get a one-line no-op
passthrough matching their existing `ApplyPermissionModeFlag`.

New wrapper in `session/agent_restart.go`, alongside
`BuildPermissionModeCommand`:

```go
func BuildModelCommand(program, model string) string {
	return defaultRegistry.Lookup(program).ApplyModelFlag(program, model)
}
```

**Headroom Wrap gets no adapter method.** It's a plain prefix,
independent of which agent is running:

```go
// BuildHeadroomWrapCommand prefixes program with "headroom wrap ",
// applied after every agent-specific flag so earlier steps still see
// the bare program name (e.g. "claude") when deciding how to modify
// the string. Idempotent: no-ops if program is already wrapped.
func BuildHeadroomWrapCommand(program string) string {
	parts := strings.Fields(program)
	if len(parts) >= 2 && parts[0] == "headroom" && parts[1] == "wrap" {
		return program
	}
	return "headroom wrap " + program
}
```

### Composition & mutual exclusivity (`app/remote_control.go`)

Today, `remoteControlProgram`/`permissionModeProgram` each take
`*config.Config` and are called inline at four sites (`app/app.go:393`,
`app/app.go:1696`, `app/state_new.go:59`, `app/state_prompt.go:59`).
Adding two more flags *and* per-instance overrides on top of that shape
means either duplicating cfg-vs-override branching at each site, or
centralizing it once. This centralizes it:

```go
type launchOptions struct {
	RemoteControl  bool
	PermissionMode string
	Model          string
	HeadroomWrap   bool
}

func launchOptionsFromConfig(cfg *config.Config) launchOptions {
	if cfg == nil {
		return launchOptions{}
	}
	return launchOptions{
		RemoteControl:  cfg.RemoteControlEnabled(),
		PermissionMode: cfg.PermissionMode(),
		Model:          cfg.Model(),
		HeadroomWrap:   cfg.HeadroomWrapEnabled(),
	}
}

// applyLaunchOptions composes program in order: remote-control,
// permission-mode, model, then headroom-wrap last — headroom-wrap must
// be outermost so the earlier three steps still see the bare agent
// name at parts[0] when deciding how to modify the string. HeadroomWrap
// forcibly disables RemoteControl here regardless of
// opts.RemoteControl: this is the authoritative enforcement of the
// exclusivity rule. The UI-level auto-disable (Claude Preferences,
// Session Launch Options) is the good-UX layer on top — it keeps
// config.json and the modal's own state from ever showing both as on —
// but this is what guarantees a hand-edited config.json with both
// fields true still can't launch both flags together.
func applyLaunchOptions(opts launchOptions, auth session.RemoteControlAuth, program, title string) string {
	if opts.HeadroomWrap {
		opts.RemoteControl = false
	}
	program = remoteControlProgram(opts.RemoteControl, auth, program, title)
	program = permissionModeProgram(opts.PermissionMode, program)
	program = modelProgram(opts.Model, program)
	program = headroomWrapProgram(opts.HeadroomWrap, program)
	return program
}
```

`remoteControlProgram`/`permissionModeProgram` change signature to take
the resolved primitive instead of `*config.Config` (nil-handling moves
into `launchOptionsFromConfig`); new `modelProgram(model, program
string) string` and `headroomWrapProgram(enabled bool, program string)
string` mirror them. `remoteControlBlocked` changes from reading
`m.appConfig.RemoteControlEnabled()` directly to taking an `rcEnabled
bool` parameter, so it reflects whichever `launchOptions` value is
actually about to be applied (global default or per-instance override),
not always the global default.

Call sites:

- `app/app.go:393`, `app/app.go:1696` (auto-created workspace
  terminals, no modal): `applyLaunchOptions(launchOptionsFromConfig(appConfig), auth, program, title)`.
- `app/state_new.go`, `app/state_prompt.go`: now feed the `launchOptions`
  produced by the new modal (see below) instead of
  `launchOptionsFromConfig(m.appConfig)` directly.

### Global settings UI (`ui/overlay/claudePreferences.go`)

Two new rows; `claudePrefsRowCount` becomes `4`:

```
Claude Preferences

Remote Control    [ ]
Permission Mode   < acceptEdits >
Model             < sonnet >
Headroom Wrap     [x]

up/down move • enter/space toggle/cycle • esc back
```

- Cursor 0 (Remote Control): unchanged toggle, plus forces `HeadroomWrap`
  off in the same `cfg.Mutate` closure.
- Cursor 1 (Permission Mode): unchanged cycling.
- Cursor 2 (Model): cycles `cfg.ClaudeModel` through `config.ClaudeModels`,
  same shape as Permission Mode's cycling.
- Cursor 3 (Headroom Wrap): toggle, plus forces `ClaudeRemoteControl`
  off in the same closure.

`nextPermissionMode` generalizes into a shared helper, since Model now
needs identical wrap-around cycling:

```go
// nextInList returns the value in list after current, wrapping from
// the last value back to the first. Falls back to list[0] if current
// isn't found (e.g. a value predating this list's addition of it).
func nextInList(list []string, current string) string {
	for i, v := range list {
		if v == current {
			return list[(i+1)%len(list)]
		}
	}
	return list[0]
}
```

### Per-instance modal (`ui/overlay/sessionLaunchOptions.go`, new)

Same four-row shape, but editing a local `launchOptions` value instead
of `*config.Config` — ephemeral, never written back to disk. Needs a
**confirm** action distinct from toggle/cycle, since this is a one-shot
dialog rather than a persistent settings panel:

```
Session Launch Options

Remote Control    [x]
Permission Mode   < default >
Model             < default >
Headroom Wrap     [ ]

up/down move • space toggle/cycle • enter start • esc cancel
```

- `up`/`k`, `down`/`j`: move cursor across the four rows (clamped, no
  wraparound — matches `SettingsOverlay`'s own list navigation).
- `space`: toggle/cycle the focused row, same exclusivity rule as
  Claude Preferences (toggling Headroom Wrap on forces Remote Control
  off in the local value, and vice versa).
- `enter`: confirm regardless of cursor position — signals the caller
  to proceed with `applyLaunchOptions(opts, ...)` and start the
  instance.
- `esc`/`ctrl+c`: cancel the whole new-instance flow — same
  pop-and-kill-pending-instance behavior as today's Esc-from-title-entry
  (`app/state_new.go:92-106`).

### State wiring

New `stateLaunchOptions` + `app/state_launch_options.go`
(`handleStateLaunchOptionsKey`, mirroring `state_new.go`'s shape).

- `app/state_new.go`'s `!promptAfterName` Enter branch
  (`app/state_new.go:54-79` today) stops composing+starting directly.
  Instead: `m.state = stateLaunchOptions`, overlay set to
  `overlay.NewSessionLaunchOptions(launchOptionsFromConfig(m.appConfig), m.rcAuth.Blocked(), m.rcAuth.Reason)`.
- `app/state_prompt.go`'s finalize branch (`app/state_prompt.go:44-81`
  today, the "instance not started yet" arm) makes the same change:
  transitions to `stateLaunchOptions` instead of composing+starting
  inline.
- `handleStateLaunchOptionsKey` looks up the pending instance the same
  way `state_new.go` does today (`m.list.GetInstances()[m.list.NumInstances()-1]`
  — it's always the last list entry while mid-creation). On non-confirm
  keys it delegates to the overlay's `HandleKeyPress`. On confirm, it
  runs the same `startTask`/`remoteControlBlocked`/
  `promptRemoteControlBlocked` sequence that lives inline in
  `state_new.go`/`state_prompt.go` today, using
  `applyLaunchOptions(overlayOpts, ...)` instead of the old direct
  `permissionModeProgram(remoteControlProgram(...))` calls. On
  Esc/ctrl+c, pops and kills the pending instance identically to
  `state_new.go`'s existing Esc/ctrl+c handling.

Both the plain (`n`) and with-prompt (`N`) flows converge on this one
shared confirm-launch-options state right before start, regardless of
which path collected the title/prompt.

## Testing

- `config/config_test.go`: `TestHeadroomWrapEnabled`, `TestModel`,
  `TestClaudeModels` — mirroring `TestRemoteControlEnabled`/
  `TestPermissionMode`/`TestClaudePermissionModes`
  (`config/config_test.go:346-410`).
- `session/agent/adapter_test.go`: `TestClaudeModelFlag` (insertion,
  idempotence, `""`/`"default"` no-op, composes with existing
  `--permission-mode`/`--remote-control`), `TestNonClaudeAdaptersNoModelFlag`.
- `session/agent_restart_test.go`: `TestBuildModelCommand_*`,
  `TestBuildHeadroomWrapCommand_*` (wraps, idempotent-if-already-wrapped).
- `app/remote_control_test.go`: `TestApplyLaunchOptions_*` covering each
  flag independently, all four stacked, and the defensive exclusivity
  clamp (`opts.RemoteControl == true` and `opts.HeadroomWrap == true`
  together still produces no `--remote-control` in the output).
- `ui/overlay/claudePreferences_test.go`: extend row navigation/cycling
  coverage to 4 rows; new exclusivity assertions (toggling Headroom Wrap
  on turns Remote Control off in `cfg` and vice versa).
- `ui/overlay/sessionLaunchOptions_test.go` (new): row nav, toggle/cycle
  via `space`, confirm via `enter` reported distinctly from
  toggle/cycle, exclusivity rule, Esc/ctrl+c reported as cancel.
- `app/state_launch_options_test.go` (new): confirming composes and
  starts using the modal's `opts` (not global config) — e.g. global
  Remote Control on but per-instance override off ⇒ resulting `Program`
  has no `--remote-control`. Update `state_new_test.go`/
  `state_prompt_test.go` since Enter on title/prompt entry now lands on
  `stateLaunchOptions` instead of starting immediately.

## Open items to confirm during planning

- Verify `sonnet`/`opus`/`haiku` are the literal strings the installed
  Claude CLI's `--model` flag accepts as latest-resolving aliases (vs.
  e.g. requiring a `claude-` prefix) before locking in `ClaudeModels` —
  adjust the list to match actual CLI behavior if not.
- Exact `overlay`-kind and `ui.State*` constant names for the new modal
  (mirroring existing naming like `overlayTextInput`/`ui.StatePrompt`) —
  pick whatever's most consistent once the surrounding enum blocks are
  in front of us.
- Whether `nextInList`'s fallback-to-`list[0]` behavior (for a value
  that predates a list's current contents) needs a test case for
  `ClaudeModels` specifically, or whether `ClaudePermissionModes`'
  existing coverage of that path is enough given they'll share the
  helper.
