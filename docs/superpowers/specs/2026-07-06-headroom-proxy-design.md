# Headroom Proxy (replacing Headroom Wrap's command-rewrite with an env var)

*Date: 2026-07-06 · Branch: `aidanb/headroom-proxy-changes`*

## Motivation

The existing Headroom Wrap feature (`docs/superpowers/specs/2026-07-05-headroom-wrap-design.md`)
rewrites a session's launch command from `claude ...` to `headroom wrap
claude ...`. In practice `headroom wrap claude` itself does nothing more
exotic than start Headroom's proxy and set `ANTHROPIC_BASE_URL` before
exec'ing the real `claude` binary (confirmed via `headroom wrap claude
--help`: "Sets ANTHROPIC_BASE_URL to route all Anthropic API calls
through Headroom."). Headroom's own `proxy --help` documents the same
thing as the intended integration point for any client: `ANTHROPIC_BASE_URL=http://localhost:8787 claude`.

Rewriting the command string to get there was expensive: every place
in Loom that parses `program` (adapter matching, recovery-flag
injection, remote-control auth probing) had to grow a "strip the
headroom-wrap prefix first" step (`session/agent/adapter.go`'s
`SplitHeadroomWrap`/`headroomWrapPrefix`, threaded into `claude.go`'s
`ApplyRecoveryFlag` and `session/remote_control_auth.go`). Setting an
environment variable on the tmux session instead means `program` is
never modified, so all of that stripping machinery can be deleted
rather than adapted.

This spec replaces Headroom Wrap's mechanism end to end. It is not
additive — `HeadroomWrap`/`BuildHeadroomWrapCommand`/`SplitHeadroomWrap`
and everything downstream of them go away, replaced by `HeadroomProxy`/
`HeadroomProxyEnv`/tmux per-session `-e` env vars.

## Goals

- Replace the `headroom wrap <tool>` command-rewrite with an
  `ANTHROPIC_BASE_URL=http://127.0.0.1:8787` environment variable set
  only on the spawned tmux session, leaving `Instance.Program` (and
  everything that parses it) untouched.
- Loom does not start, stop, or otherwise manage the `headroom proxy`
  process. The user is responsible for having it running (e.g. via
  `headroom install`'s persistent deployment). If the proxy isn't
  reachable, `claude` inside the session fails the same way it would
  against any other unreachable `ANTHROPIC_BASE_URL` — Loom does not
  pre-validate.
- Scoped to Claude only. Aider (previously also supported by Headroom
  Wrap) is dropped — `ANTHROPIC_BASE_URL` is Anthropic-API-specific and
  Aider's provider routing is a different, unverified shape. Aider
  support can return later as its own toggle if requested.
- Rename the config field, its accessor, the `overlay.LaunchOptions`
  field, and both UI labels from "Headroom Wrap" to "Headroom Proxy" to
  match the new mechanism.
- Preserve the existing Remote-Control/Headroom exclusivity rule
  (enabling one still disables the other) even though the original
  technical conflict — command-string rewriting stomping on
  remote-control's binary detection — no longer applies. Kept as a
  deliberate, conservative choice, not a technical requirement.
- The toggle must survive pause/resume and crash recovery, since those
  paths create a brand new tmux session for the same instance.
- Delete `BuildHeadroomWrapCommand`, `headroomSupportedTools`,
  `SplitHeadroomWrap`, `headroomWrapPrefix`, and every "strip the
  headroom prefix" call site. Adapter matching, recovery-flag
  injection, and remote-control auth detection go back to parsing
  `program` directly with no wrapping-awareness at all.

## Non-goals

- No configurable proxy host/port. `http://127.0.0.1:8787` (Headroom's
  own default) is hardcoded. A user running the proxy on a different
  port has no UI for it yet — YAGNI for a first cut; add a config field
  later if someone asks.
- No health check / `headroom doctor` probing before launch. Consistent
  with the existing precedent (Headroom Wrap and Model never validated
  eligibility either) — if the proxy is down, `claude`'s own connection
  error is the feedback.
- No changes to the per-instance Session Launch Options modal's shape
  (still 4 rows) beyond the relabel — no new rows, no new navigation.
- No support for wrapping any agent other than Claude via this
  mechanism.

## Design

### `session/tmux`: per-session environment variables

`TmuxSession` gains an optional `env []string` (`"KEY=VALUE"` entries),
set at construction time via a variadic parameter so every existing
2-arg call site keeps compiling unchanged:

```go
func NewTmuxSession(name string, program string, env ...string) *TmuxSession {
	return newTmuxSession(name, program, env, MakePtyFactory(), internalexec.Default{})
}

func NewTmuxSessionWithDeps(name string, program string, env []string, ptyFactory PtyFactory, cmdExec internalexec.Executor) *TmuxSession {
	return newTmuxSession(name, program, env, ptyFactory, cmdExec)
}
```

`Start()` inserts `-e KEY=VALUE` pairs (one flag pair per entry) between
`-c workDir` and the trailing program argument:

```go
args := []string{"new-session", "-d", "-s", t.sanitizedName, "-c", workDir}
for _, e := range t.env {
	args = append(args, "-e", e)
}
args = append(args, t.program)
cmd := exec.CommandContext(startCtx, "tmux", args...)
```

`tmux new-session -e` is supported since tmux 3.2 (confirmed present in
3.6a on this machine) and scopes the variable to that one session
without touching the command tmux execs — so this doesn't interact with
anything that parses `t.program`.

### `session/agent_restart.go`: `HeadroomProxyEnv` replaces `BuildHeadroomWrapCommand`

```go
// HeadroomProxyURL is the base URL Loom points ANTHROPIC_BASE_URL at
// when the Headroom Proxy launch option is enabled — Headroom's own
// default proxy address (`headroom proxy`, port 8787). Loom does not
// start or manage the proxy process itself; the user is expected to
// have it running already (e.g. via `headroom install`'s persistent
// deployment).
const HeadroomProxyURL = "http://127.0.0.1:8787"

// HeadroomProxyEnv returns the tmux session environment variables
// needed to route program's API calls through Headroom's proxy. A
// no-op (nil) unless enabled and program resolves to Claude —
// ANTHROPIC_BASE_URL is Anthropic-API-specific, so setting it for any
// other agent wouldn't do anything useful.
func HeadroomProxyEnv(enabled bool, program string) []string {
	if !enabled || !IsClaudeProgram(program) {
		return nil
	}
	return []string{"ANTHROPIC_BASE_URL=" + HeadroomProxyURL}
}
```

`BuildHeadroomWrapCommand` and `headroomSupportedTools` are deleted
outright — nothing rewrites `program` for Headroom anymore.

### Deletions in `session/agent` (adapter matching goes back to plain parsing)

- `adapter.go`: delete `headroomWrapPrefix` and `SplitHeadroomWrap`.
  `firstField` drops its call into `SplitHeadroomWrap` and just does
  `strings.Fields(program)` directly:

  ```go
  func firstField(program string) string {
  	parts := strings.Fields(program)
  	if len(parts) == 0 {
  		return ""
  	}
  	return parts[0]
  }
  ```

- `claude.go`: `ApplyRecoveryFlag` drops its `SplitHeadroomWrap` call
  and the `prefix` it threaded through — back to the pre-Headroom-Wrap
  shape (parse `program` directly, no prefix to preserve). The comment
  block above `ApplyRemoteControlFlag` explaining why those three
  methods are "deliberately NOT headroom-wrap-aware" is deleted — with
  no command-rewriting left anywhere, the distinction it was drawing no
  longer exists.

- `session/remote_control_auth.go`: `DetectClaudeRemoteControlAuth`
  drops its `agent.SplitHeadroomWrap(program)` call, using
  `strings.Fields(program)` directly.

### `Instance`: new persisted `HeadroomProxy` field

Unlike the other three launch options, Headroom Proxy is never baked
into `Program` — it needs its own field so the four `tmux.NewTmuxSession`
call sites in `session/instance.go` know whether to pass the env:

```go
type Instance struct {
	...
	Program string
	// HeadroomProxy controls whether this instance's tmux session gets
	// ANTHROPIC_BASE_URL pointed at Headroom's proxy. Set once before
	// Start() (same convention as Program) and persisted so pause/resume
	// and crash recovery — which construct a brand new TmuxSession for
	// the same instance — still apply it.
	HeadroomProxy bool
	...
}
```

All four `tmux.NewTmuxSession(...)` call sites in `session/instance.go`
change to pass the resolved env:

```go
tmux.NewTmuxSession(i.Title, i.Program, session.HeadroomProxyEnv(i.HeadroomProxy, i.Program)...)
```

- `FromInstanceData` (constructing the placeholder `TmuxSession` for
  `Paused`/`Recoverable` instances)
- `Start`
- `startFreshWithRecovery`
- `CrashRestart`

`InstanceOptions` gains `HeadroomProxy bool`, copied through in
`NewInstance`. `Snapshot()`/`FromInstanceData()` carry
`HeadroomProxy` through to/from `InstanceData`.

### `InstanceData` schema bump (v2 → v3)

Per the contributor protocol in `session/storage_migrate.go`, adding a
field to `InstanceData` requires a schema bump even though a missing
bool defaults to `false` (the correct default) on its own:

```go
type InstanceData struct {
	...
	Program             string          `json:"program"`
	HeadroomProxy       bool            `json:"headroom_proxy,omitempty"`
	Worktree            GitWorktreeData `json:"worktree"`
	...
}
```

```go
const CurrentSchemaVersion = 3
```

```go
case 2:
	// v2 → v3: HeadroomProxy added. No payload changes needed — the
	// zero value (false) already matches the desired default for
	// pre-existing records — just stamp the version.
	data.SchemaVersion = 3
```

`cmd/migrationInstance` (the hand-mirrored type in
`cmd/workspace_migrate.go`) gets the matching field, and the JSON
fixture in `cmd/workspace_migrate_shape_test.go` gets a
`"headroom_proxy": false` (or `true`) entry — both are drift-guarded by
existing tests (`TestMigrationInstance_MirrorsInstanceData_JSON`,
`TestMigrationInstance_TypeDriftGuard`) that already fail loudly if
they're skipped.

`session/orphan.go`'s direct `InstanceData{...}` construction for
recovered orphans is left without an explicit `HeadroomProxy` — it
defaults to `false` (off), matching the existing precedent that
recovered orphans are inert placeholders, not fully-specified
instances.

### Config layer (`config/config.go`)

```go
// HeadroomProxy controls whether new Claude sessions launch with
// ANTHROPIC_BASE_URL pointed at Headroom's proxy (see
// session.HeadroomProxyEnv). A no-op for agents other than Claude.
// Loom does not start or manage the headroom proxy process itself —
// the user is expected to have it running separately. Defaults to off
// (DefaultConfig sets it explicitly to false) since it's opt-in.
// Mutually exclusive with ClaudeRemoteControl: enabling one disables
// the other, enforced in the Claude Preferences toggle handler, the
// Session Launch Options modal, and defensively again in
// applyLaunchOptions so a hand-edited config.json with both fields
// true still can't launch both at once. Read it through
// HeadroomProxyEnabled.
HeadroomProxy *bool `json:"headroom_proxy,omitempty"`
```

```go
func (c *Config) HeadroomProxyEnabled() bool {
	return c.HeadroomProxy != nil && *c.HeadroomProxy
}
```

`DefaultConfig()`: `HeadroomProxy: boolPtr(false)`.

An old `config.json` with `"headroom_wrap": true` from the prior
feature is simply ignored (unknown key to the new struct) — the new
field defaults to off. No migration needed since Headroom Wrap already
defaulted to off; a user who had it on picks it back up as "Headroom
Proxy" the next time they open Claude Preferences.

### Composition (`app/remote_control.go`)

`headroomWrapProgram` (the string-rewriter) is deleted.
`applyLaunchOptions` drops from four composition steps to three —
remote-control, permission-mode, model — since Headroom Proxy no
longer touches `program` at all:

```go
func applyLaunchOptions(opts overlay.LaunchOptions, auth session.RemoteControlAuth, program, title string) string {
	program = remoteControlProgram(effectiveRemoteControl(opts), auth, program, title)
	program = permissionModeProgram(opts.PermissionMode, program)
	program = modelProgram(opts.Model, program)
	return program
}
```

`effectiveRemoteControl` keeps the same exclusivity shape, renamed:

```go
func effectiveRemoteControl(opts overlay.LaunchOptions) bool {
	return opts.RemoteControl && !opts.HeadroomProxy
}
```

`launchOptionsFromConfig` reads `HeadroomProxy: cfg.HeadroomProxyEnabled()`.

Because `HeadroomProxy` no longer flows through `program`, the four
call sites that build an `Instance` need a second assignment alongside
the existing `Program = applyLaunchOptions(...)` line:

- `app/state_new.go` (`instance.Program = ...`): add
  `instance.HeadroomProxy = opts.HeadroomProxy` in the same `Sync`
  closure.
- `app/state_prompt.go` (`selected.Program = ...`): add
  `selected.HeadroomProxy = opts.HeadroomProxy` likewise.
- `app/app.go`'s two auto-created-workspace-terminal sites (construct
  `InstanceOptions{...}` directly, no modal): add
  `HeadroomProxy: wtOpts.HeadroomProxy` to the struct literal.

### UI (`ui/overlay/claudePreferences.go`, `ui/overlay/sessionLaunchOptions.go`)

Pure rename, no shape change:

- `overlay.LaunchOptions.HeadroomWrap` → `HeadroomProxy`.
- Row label "Headroom Wrap     " → "Headroom Proxy    " in both
  screens (kept at the same fixed width other labels use).
- `ClaudePreferences`'s cursor-3 handler: `cc.HeadroomWrap` →
  `cc.HeadroomProxy`, still zeroing `ClaudeRemoteControl` when turned
  on (and vice versa on cursor 0), same exclusivity behavior as today.
- `SessionLaunchOptions`'s cursor-3 handler: same rename, same
  exclusivity behavior in the local `opts` value.

## Testing

- `session/tmux`: new test(s) asserting `Start()` includes `-e
  KEY=VALUE` args for each entry in a session constructed with env vars,
  and that a session constructed without env vars produces the
  unchanged today's-args (regression guard for the variadic default).
- `session/agent_restart_test.go`: replace
  `TestBuildHeadroomWrapCommand_*` with `TestHeadroomProxyEnv_*` —
  disabled ⇒ nil, enabled+Claude ⇒ the one env entry, enabled+non-Claude
  ⇒ nil, enabled+Claude-with-flags (e.g. `"claude --model opus"`) ⇒
  still just the one env entry (env doesn't need to parse `program`
  beyond the adapter-match check).
- `session/agent/adapter_test.go`: delete `SplitHeadroomWrap` coverage;
  confirm `firstField`/`basenameMatch` behavior is unchanged for plain
  (unwrapped) programs.
- `session/agent/adapter_test.go` (Claude adapter cases): confirm
  `ApplyRecoveryFlag` behavior is unchanged now that it no longer
  special-cases a headroom-wrap prefix (there's nothing left to strip).
- `session/remote_control_auth_test.go`: drop any
  wrapped-program-specific cases (`"headroom wrap claude"` inputs) —
  `DetectClaudeRemoteControlAuth` never sees a wrapped string anymore.
- `config/config_test.go`: rename `TestHeadroomWrapEnabled` →
  `TestHeadroomProxyEnabled`, same coverage shape (nil ⇒ false,
  explicit true/false).
- `app/remote_control_test.go`: rename/rewrite
  `TestApplyLaunchOptions_*`'s Headroom cases — assert `applyLaunchOptions`
  no longer touches `program` for Headroom Proxy at all (only
  remote-control/permission-mode/model composition remains); assert
  `effectiveRemoteControl`'s exclusivity math using the renamed field.
- `session/instance_lifecycle_test.go`/`session/instance_load_test.go`
  (wherever `Snapshot`/`FromInstanceData` are covered today): round-trip
  `HeadroomProxy` through
  Snapshot→FromInstanceData; assert `Start()`/`startFreshWithRecovery`/
  `CrashRestart` construct their `TmuxSession` with the env from
  `HeadroomProxyEnv(i.HeadroomProxy, i.Program)`.
- `cmd/workspace_migrate_shape_test.go`: add `"headroom_proxy"` to the
  fixture JSON; the existing drift-guard tests fail the build if
  `migrationInstance` isn't updated to match.
- `session/storage_migrate_test.go` (wherever v1→v2 is covered today):
  add a v2→v3 case — a v2 record with no `headroom_proxy` key migrates
  to v3 with `HeadroomProxy == false`.
- `ui/overlay/claudePreferences_test.go`,
  `ui/overlay/sessionLaunchOptions_test.go`: rename Headroom Wrap
  assertions/labels to Headroom Proxy; exclusivity behavior coverage
  unchanged in shape.

## Open items to confirm during planning

- Exact test file/function names for the new `session/tmux` `-e` flag
  coverage — depends on how `Start()`'s existing tests are structured
  (likely alongside whatever already asserts the `tmux new-session`
  arg list, if such a test exists — otherwise this is new coverage).
- Whether `session/storage_migrate_test.go` (or equivalent) already has
  a table-driven shape that a v2→v3 case slots into, or whether it
  needs a new test function.
