# Claude `[1m]` Long-Context Option

**Date:** 2026-08-21
**Status:** Approved design
**Verified against:** Claude Code 2.1.235 (`claude-code-2.1.235/bin/.claude-unwrapped`)

## Problem

Claude Code exposes its 1M-token context window as a suffix on the `--model`
value — `sonnet[1m]`, `opus[1m]`, `fable[1m]` — not as a separate flag. Loom
already launches Claude sessions with a configured `--model` alias, chosen from
`config.ClaudeModels` in two places (the global Claude Preferences overlay and
the per-session Session Launch Options overlay), but has no way to request the
long-context variant. Users who want it must hand-edit the program string or
drop out of loom's launch-options flow entirely.

### What `[1m]` actually is

Extracted from the shipped binary, `--model` accepts these suffixed forms:

```
sonnet[1m]   opus[1m]   fable[1m]   opusplan[1m]
claude-opus-5[1m]  claude-opus-4-8[1m]  claude-opus-4-7[1m]
claude-opus-4-6[1m]  claude-sonnet-4-6[1m]  sonnet-5[1m]  ...
```

Support is a per-model capability, not universal — the CLI gates the display
name on `r.context?.supports_1m_suffix`, and rejects unsupported pairings (for
example Sonnet 4.5 on Vertex returns 400 for the `context-1m` beta). There is
no `haiku[1m]` and no `default[1m]`.

Detection inside Claude Code is a plain case-insensitive regex on the model
string, disabled by one kill switch:

```js
function epe() { return K.CLAUDE_CODE_DISABLE_1M_CONTEXT }
function jw(e) { if (epe()) return !1; return /\[1m\]/i.test(e) }
```

`CLAUDE_CODE_DISABLE_1M_CONTEXT` only *disables*. There is no enabling
environment variable, so loom cannot route this through the
`tmux new-session -e` mechanism it uses for Headroom Proxy and Cache TTL 1h —
the suffix must go into the program string.

### The shell hazard this creates

`session/tmux/tmux.go` passes `t.program` to `tmux new-session` as a single
shell-command argument, which tmux executes via its `default-shell`. Square
brackets are glob syntax, and zsh's default `nomatch` makes an unmatched
pattern a fatal error rather than a literal:

```
zsh  -c 'claude --model sonnet[1m]'  → zsh:1: no matches found: sonnet[1m]  (exit 1)
bash -c 'claude --model sonnet[1m]'  → literal, works
sh   -c 'claude --model sonnet[1m]'  → literal, works
```

`ApplyModelFlag` currently inserts the value unquoted. Under zsh — the
`default-shell` on a stock Arch/CachyOS install and on the development machine
this was verified on — an unquoted `[1m]` kills the pane at launch. Under bash
it works. That shell-dependent split would surface as "loom randomly fails to
start sessions", so quoting is part of this feature, not a follow-up.

## Decision

Add `1M Context` as a free-standing boolean launch option. The suffix is
composed onto the model alias at launch time, and only for models that accept
it; for `default` and `haiku` the toggle is silently a no-op. `ApplyModelFlag`
single-quotes its value unconditionally, and `ParseLaunchOptions` gains a
tolerant decode that accepts quoted, unquoted, and suffixed forms.

Alternatives considered:

- **Extra entries in `ClaudeModels`** (`sonnet[1m]`, `opus[1m]`, …): no new
  config field or UI row, and invalid combinations are impossible by
  construction, but the cycle list grows to eight and `[1m]` stops being
  visibly orthogonal to model choice.
- **Free-text model field:** never goes stale and supports versioned IDs, but
  loses the no-typos guarantee and retires the "value comes from
  `config.ClaudeModels`, never free-typed, so no sanitization is applied"
  assumption the adapter comments currently rest on.
- **Gate the row / auto-promote the model** when the selection cannot take the
  suffix: rejected as cross-row coupling. Silently declining to append is
  simpler and cannot surprise the user by mutating a setting they did not
  touch.

## Architecture

### 1. Capability lives on the model — `config/config.go`

`ClaudeModels` becomes a struct list so the capability sits next to the alias
it describes, mirroring how Claude Code models it internally
(`context.supports_1m_suffix` is a field on the model, not a side table):

```go
// ClaudeModel pairs a --model alias with whether it accepts the [1m]
// long-context suffix.
type ClaudeModel struct {
    Alias      string
    Supports1M bool
}

var ClaudeModels = []ClaudeModel{
    {"default", false},
    {"sonnet", true},
    {"opus", true},
    {"fable", true},
    {"haiku", false},
}

// ClaudeModelAliases returns the aliases in cycle order.
func ClaudeModelAliases() []string

// ClaudeModelSupports1M reports whether alias accepts the [1m] suffix.
// Unknown aliases report false.
func ClaudeModelSupports1M(alias string) bool
```

`nextInList(list []string, current string)` and its six call sites are
unchanged — the two model sites pass `ClaudeModelAliases()` instead of
`ClaudeModels`. Keeping `nextInList` string-shaped avoids making it generic for
one caller while the permission-mode and effort lists stay plain `[]string`.

New global default, opt-in and defaulting off, following `CacheTTL1h`:

```go
// Claude1MContext controls whether new Claude sessions launch with the
// [1m] long-context suffix appended to their --model alias. Defaults to
// off (DefaultConfig sets it explicitly to false) since it is opt-in and
// bills against a larger context window. A no-op for agents other than
// Claude, and for model aliases that do not support the suffix. Read it
// through Context1MEnabled.
Claude1MContext *bool `json:"claude_1m_context,omitempty"`

func (c *Config) Context1MEnabled() bool  // nil => false
```

`LaunchOptions` gains `Context1M bool`, seeded from `cfg.Context1MEnabled()`
alongside the existing `Model` and `Effort` seeds.

### 2. Composition — `app/remote_control.go`

The suffix resolves in the app layer, beside its structural sibling
`effectiveRemoteControl`:

```go
// effectiveModel returns the --model value to launch with, appending the
// [1m] long-context suffix when the option is on and the selected alias
// accepts it. An alias that does not support the suffix (default, haiku)
// is returned unchanged rather than producing a value the CLI would
// reject.
func effectiveModel(opts overlay.LaunchOptions) string {
    if opts.Context1M && config.ClaudeModelSupports1M(opts.Model) {
        return opts.Model + "[1m]"
    }
    return opts.Model
}
```

`applyLaunchOptions` passes `effectiveModel(opts)` to `modelProgram`. The
`session/agent` adapter stays ignorant of 1M: it owns shell mechanics, the app
layer owns option semantics. Composition order (remote-control,
permission-mode, model, effort) is untouched.

### 3. Quoting — `session/agent/claude.go`

`ApplyModelFlag` emits `--model '<value>'` unconditionally, matching
`ApplyLoomContextFlag`, which already single-quotes for the same reason. The
existing idempotency guard is unaffected: it scans for a `--model` token or
`--model=` prefix before inserting, neither of which quoting changes.

Unconditional beats a needs-quoting predicate because the decoder must tolerate
both shapes regardless (see below), so conditional quoting buys nothing while
adding a predicate that can be wrong for the next special-cased value. The
adapter's "expected to come from `config.ClaudeModels`, never free-typed user
input, so no sanitization is applied" comment is updated: values are still
list-sourced, but are now quoted so shell metacharacters in an alias are inert.

### 4. Tolerant decode — `ParseLaunchOptions`

Every `Program` string already persisted in `instances.json` carries an
unquoted `--model opus`, and those records are decoded on every load through
`Storage.LoadAndReconcile`. The decoder must therefore accept both shapes
permanently, independent of what the encoder now emits. After the existing
`strings.Fields` split, the `--model` value is:

1. stripped of surrounding single quotes if present, then
2. split on a case-insensitive `[1m]` suffix into `opts.Model` and
   `opts.Context1M`.

Case-insensitive because Claude's own check is `/\[1m\]/i` and a hand-edited
`Program` is a supported input. `ParseLaunchOptions` is the only code in the tree that parses a composed
`--model` back out of a program string, so it is the only consumer that needs a
quoting-aware update; every other `--model` reference is a doc comment or the
encoder itself.

`opts.Context1M` joins the round-tripped group
(`RemoteControl`, `PermissionMode`, `Model`, `Effort`) rather than the group
the doc comment lists as needing external seeding (`HeadroomProxy`,
`CacheTTL1h`) — see §6.

### 5. UI — one new row in each overlay

`1M Context` is inserted directly after `Model` in both
`ui/overlay/sessionLaunchOptions.go` and `ui/overlay/claudePreferences.go`,
rendered as an `[x]`/`[ ]` checkbox like Remote Control, Headroom Proxy, and
Cache TTL (1h):

```
Session Launch Options

  Remote Control    [x]
  Permission Mode   < default >
  Model             < sonnet >
> 1M Context        [x]              → --model 'sonnet[1m]'
  Headroom Proxy    [ ]
  Effort            < xhigh >
  Cache TTL (1h)    [ ]
```

`sessionLaunchOptionsRowCount` goes 6→7 and `claudePrefsRowCount` 7→8, and the
positional `case` indices after Model shift by one in each overlay's
`toggleCursor`. Adjacency is deliberate: the row modifies the Model value, so
appending it at the bottom to avoid renumbering would misrepresent the
relationship. The renumber is mechanical and covered by existing overlay tests.

No cross-row coupling is added. Unlike the Remote Control / Headroom Proxy
pair, 1M Context never forces another row's value, and no row disables it.

### 6. No schema migration

Because `[1m]` rides inside the `--model` value, it is already contained in
`Program`, which `InstanceData` persists today. No new `Instance` field is
required, so `CurrentSchemaVersion` is not bumped, `session/storage_migrate.go`
gains no step, and the `cmd/workspace_migrate_shape_test.go` fixture is
unchanged. `HeadroomProxy` and `CacheTTL1h` needed their own fields precisely
because they never touch `Program`.

The `config.json` addition is an additive optional field; configs predating it
decode with `Claude1MContext == nil`, read as off.

### 7. Accepted asymmetry

`{Model: "haiku", Context1M: true}` composes to `--model 'haiku'`, so decoding
that program on resume (`R`) yields `Context1M: false` and the checkbox
reverts. This is correct rather than a defect: the state was never meaningful,
nothing was silently dropped from the launched session, and the global
`config.json` default is unaffected, so newly created sessions still seed the
option on. Documented here so it is not later filed as a round-trip bug.

## Testing

- **`session/agent/adapter_test.go`** — `ApplyModelFlag` quotes its value;
  quoting composes with the remote-control, permission-mode, and effort flags;
  an existing `--model` or `--model=` still short-circuits.
- **`app/remote_control_test.go`** — round-trip matrix:

  | Input | Composed | Decoded |
  |---|---|---|
  | `{sonnet, 1m:on}` | `--model 'sonnet[1m]'` | `{sonnet, 1m:on}` |
  | `{opus, 1m:off}` | `--model 'opus'` | `{opus, 1m:off}` |
  | legacy `--model opus` on disk | — | `{opus, 1m:off}` |
  | legacy `--model sonnet[1m]` hand-edited | — | `{sonnet, 1m:on}` |
  | `--model 'sonnet[1M]'` | — | `{sonnet, 1m:on}` |
  | `{haiku, 1m:on}` | `--model 'haiku'` | `{haiku, 1m:off}` (§7) |
  | `{default, 1m:on}` | no `--model` flag | `{default, 1m:off}` |

- **`config/config_test.go`** — `ClaudeModels` contents, `ClaudeModelAliases`
  order, `ClaudeModelSupports1M` including an unknown alias, `Context1MEnabled`
  nil/true/false, and the `DefaultConfig` value.
- **`ui/overlay/sessionLaunchOptions_test.go`, `claudePreferences_test.go`** —
  row counts, cursor bounds, the shifted `case` indices, and that toggling 1M
  leaves every other option untouched.

## Out of scope

- `opusplan[1m]` — `opusplan` is not in `ClaudeModels` today.
- Versioned IDs (`claude-opus-5[1m]`); `ClaudeModels` is deliberately aliases
  only so it survives new model releases without a code change.
- Surfacing `CLAUDE_CODE_DISABLE_1M_CONTEXT`.
- Per-model context-window display or auto-compact tuning.
