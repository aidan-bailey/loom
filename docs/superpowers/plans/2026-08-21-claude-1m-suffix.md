# Claude `[1m]` Long-Context Option — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `1M Context` launch option that appends Claude Code's `[1m]` long-context suffix to the `--model` alias, for the models that accept it.

**Architecture:** The capability moves onto the model itself (`ClaudeModels` becomes a struct list with a `Supports1M` field). A new boolean rides in `LaunchOptions`, is resolved to a suffixed alias by `effectiveModel` in the app layer, and is decoded back out by `ParseLaunchOptions`. `ApplyModelFlag` starts single-quoting its value because `[` and `]` are zsh glob metacharacters and tmux runs the composed program string through a shell.

**Tech Stack:** Go 1.23, testify/assert, Bubble Tea overlays. Build with `CGO_ENABLED=0`.

**Design doc:** `docs/superpowers/specs/2026-08-21-claude-1m-suffix-design.md`

---

## Background an engineer needs before starting

**How a session's command line is built.** When loom starts a Claude session it composes one string, e.g. `claude --remote-control my-title --permission-mode plan --model opus --effort high`. That string is stored on the instance as `Program`, persisted to `~/.loom/instances.json`, and handed to `tmux new-session` as a **single shell-command argument** (`session/tmux/tmux.go:274`). tmux executes it via its `default-shell`.

**Why quoting matters.** Square brackets are glob syntax. zsh's default `nomatch` turns an unmatched pattern into a fatal error:

```
zsh  -c 'claude --model sonnet[1m]'  → zsh:1: no matches found: sonnet[1m]  (exit 1)
bash -c 'claude --model sonnet[1m]'  → runs fine (literal)
```

So an unquoted suffix kills the pane at launch on zsh and works on bash. Quoting is part of this feature, not a cleanup.

**The encode/decode pair.** `applyLaunchOptions` (encode) and `ParseLaunchOptions` (decode) in `app/remote_control.go` are symmetric — the decode exists so `R` (resume with different launch options) can re-open the modal pre-filled from an existing `Program`. Every `Program` already on disk has an **unquoted** `--model opus`, so the decoder must accept both shapes forever.

**Task ordering is load-bearing.** Tasks 4 → 5 → 6 are ordered so the tree builds and every test passes at each commit: the decoder learns to accept quotes *before* the encoder starts emitting them, and quoting lands *before* anything can emit a bracket.

**Commands used throughout:**

| Purpose | Command |
|---|---|
| Build | `CGO_ENABLED=0 go build -o loom` |
| Test one package | `CGO_ENABLED=0 go test ./config` |
| Test one function | `CGO_ENABLED=0 go test ./config -run TestName -v` |
| Full suite | `CGO_ENABLED=0 go test ./...` |
| Format (CI enforces) | `gofmt -w .` |

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `config/config.go` | Modify | `ClaudeModel` struct, `ClaudeModels`, `ClaudeModelAliases`, `ClaudeModelSupports1M`, `Claude1MContext` field, `Context1MEnabled` |
| `config/config_test.go` | Modify | Coverage for the above |
| `ui/overlay/sessionLaunchOptions.go` | Modify | `LaunchOptions.Context1M`; the new per-session row |
| `ui/overlay/claudePreferences.go` | Modify | The new global-default row |
| `app/remote_control.go` | Modify | `Context1M` seed, `effectiveModel`, `parseModelValue` |
| `app/remote_control_test.go` | Modify | Round-trip matrix |
| `session/agent/claude.go` | Modify | Quote the `--model` value |
| `session/agent/adapter_test.go` | Modify | Quoting expectations |
| `CLAUDE.md` | Modify | Document the new `config.json` field |

No new files. No new `Instance` field, so **no `CurrentSchemaVersion` bump and no `storage_migrate.go` step** — the suffix rides inside `Program`, which is already persisted.

---

### Task 1: Move the 1M capability onto the model

`ClaudeModels` becomes a struct list so the capability sits beside the alias it describes. Two accessors keep every existing consumer string-shaped, so `nextInList` and its six call sites do not change signature.

**Files:**
- Modify: `config/config.go:151-155`
- Modify: `config/config_test.go:477-479`
- Modify: `ui/overlay/sessionLaunchOptions.go:96`
- Modify: `ui/overlay/claudePreferences.go:81`

- [ ] **Step 1: Write the failing test**

In `config/config_test.go`, replace the existing `TestClaudeModels` (currently `assert.Equal(t, []string{"default", "sonnet", "opus", "fable", "haiku"}, ClaudeModels)`) with:

```go
func TestClaudeModels(t *testing.T) {
	assert.Equal(t, []ClaudeModel{
		{"default", false},
		{"sonnet", true},
		{"opus", true},
		{"fable", true},
		{"haiku", false},
	}, ClaudeModels)
}

func TestClaudeModelAliases(t *testing.T) {
	assert.Equal(t, []string{"default", "sonnet", "opus", "fable", "haiku"}, ClaudeModelAliases())
}

func TestClaudeModelSupports1M(t *testing.T) {
	assert.True(t, ClaudeModelSupports1M("sonnet"))
	assert.True(t, ClaudeModelSupports1M("opus"))
	assert.True(t, ClaudeModelSupports1M("fable"))
	assert.False(t, ClaudeModelSupports1M("haiku"))
	assert.False(t, ClaudeModelSupports1M("default"))
	// An alias this build has never heard of must not get the suffix.
	assert.False(t, ClaudeModelSupports1M("some-future-model"))
	assert.False(t, ClaudeModelSupports1M(""))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=0 go test ./config -run 'TestClaudeModel' -v`

Expected: compile failure — `undefined: ClaudeModel`, `undefined: ClaudeModelAliases`, `undefined: ClaudeModelSupports1M`.

- [ ] **Step 3: Write the implementation**

In `config/config.go`, replace the `ClaudeModels` declaration and its comment:

```go
// ClaudeModel pairs a --model alias with whether it accepts Claude
// Code's [1m] long-context suffix. The capability lives on the model
// rather than in a parallel list so the two can't drift; this mirrors
// how Claude Code itself models it (supports_1m_suffix is a field on
// the model, not a side table).
type ClaudeModel struct {
	Alias      string
	Supports1M bool
}

// ClaudeModels lists the --model aliases the Claude Preferences and
// Session Launch Options screens cycle through. Short aliases, not
// versioned IDs, so this list doesn't need updating when new Claude
// models ship.
var ClaudeModels = []ClaudeModel{
	{"default", false},
	{"sonnet", true},
	{"opus", true},
	{"fable", true},
	{"haiku", false},
}

// ClaudeModelAliases returns just the aliases, in cycle order. The
// overlays cycle with nextInList, which stays []string-shaped so the
// permission-mode and effort lists keep sharing it unchanged.
func ClaudeModelAliases() []string {
	aliases := make([]string, len(ClaudeModels))
	for i, m := range ClaudeModels {
		aliases[i] = m.Alias
	}
	return aliases
}

// ClaudeModelSupports1M reports whether alias accepts the [1m]
// long-context suffix. An unknown alias reports false: declining to
// append is a silent no-op, whereas appending to a model that rejects
// it kills the pane at launch.
func ClaudeModelSupports1M(alias string) bool {
	for _, m := range ClaudeModels {
		if m.Alias == alias {
			return m.Supports1M
		}
	}
	return false
}
```

- [ ] **Step 4: Fix the two call sites the type change breaks**

In `ui/overlay/sessionLaunchOptions.go` line 96, change:

```go
		l.opts.Model = nextInList(config.ClaudeModels, l.opts.Model)
```

to:

```go
		l.opts.Model = nextInList(config.ClaudeModelAliases(), l.opts.Model)
```

In `ui/overlay/claudePreferences.go` line 81, change:

```go
				next := nextInList(config.ClaudeModels, cc.Model())
```

to:

```go
				next := nextInList(config.ClaudeModelAliases(), cc.Model())
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./config ./ui/overlay`

Expected: PASS for both packages.

- [ ] **Step 6: Verify the whole tree still builds**

Run: `CGO_ENABLED=0 go build -o loom && CGO_ENABLED=0 go test ./...`

Expected: build succeeds, all packages PASS. (This catches any other consumer of `ClaudeModels` the grep missed.)

- [ ] **Step 7: Commit**

```bash
gofmt -w .
git add config/config.go config/config_test.go ui/overlay/sessionLaunchOptions.go ui/overlay/claudePreferences.go
git commit -m "refactor(config): move the 1M capability onto ClaudeModels"
```

---

### Task 2: Add the `Claude1MContext` global default

Opt-in and defaulting off, following the `CacheTTL1h` precedent exactly.

**Files:**
- Modify: `config/config.go` (struct field near `CacheTTL1h`; accessor near `CacheTTL1hEnabled`; `DefaultConfig`)
- Modify: `config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `config/config_test.go`:

```go
func TestContext1MEnabled(t *testing.T) {
	t.Run("nil is off", func(t *testing.T) {
		cfg := &Config{}
		assert.False(t, cfg.Context1MEnabled())
	})
	t.Run("explicit true", func(t *testing.T) {
		cfg := &Config{Claude1MContext: boolPtr(true)}
		assert.True(t, cfg.Context1MEnabled())
	})
	t.Run("explicit false", func(t *testing.T) {
		cfg := &Config{Claude1MContext: boolPtr(false)}
		assert.False(t, cfg.Context1MEnabled())
	})
	t.Run("default config is off", func(t *testing.T) {
		cfg := DefaultConfig()
		if assert.NotNil(t, cfg.Claude1MContext) {
			assert.False(t, *cfg.Claude1MContext)
		}
		assert.False(t, cfg.Context1MEnabled())
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=0 go test ./config -run TestContext1MEnabled -v`

Expected: compile failure — `unknown field Claude1MContext`, `cfg.Context1MEnabled undefined`.

- [ ] **Step 3: Write the implementation**

In `config/config.go`, add the field immediately after the `CacheTTL1h` field, inside the `Config` struct:

```go
	// Claude1MContext controls whether new Claude sessions launch with
	// the [1m] long-context suffix appended to their --model alias
	// (e.g. "sonnet[1m]"). A no-op for agents other than Claude, and
	// for aliases that don't accept the suffix (see
	// ClaudeModelSupports1M). Defaults to off (DefaultConfig sets it
	// explicitly to false) since it's opt-in. Read it through
	// Context1MEnabled.
	Claude1MContext *bool `json:"claude_1m_context,omitempty"`
```

Add the accessor immediately after `CacheTTL1hEnabled`:

```go
// Context1MEnabled reports whether new Claude sessions should launch
// with the [1m] long-context suffix on their --model alias. Defaults to
// false when unset. Unlocked for the same reason as PermissionMode.
func (c *Config) Context1MEnabled() bool {
	return c.Claude1MContext != nil && *c.Claude1MContext
}
```

In `DefaultConfig`, add the line after `CacheTTL1h`:

```go
		CacheTTL1h:           boolPtr(false),
		Claude1MContext:      boolPtr(false),
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `CGO_ENABLED=0 go test ./config -run TestContext1MEnabled -v`

Expected: PASS (all four subtests).

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add config/config.go config/config_test.go
git commit -m "feat(config): add the Claude1MContext global default"
```

---

### Task 3: Carry the option through `LaunchOptions`

Field plus seed only — no composition yet, so nothing can emit a bracket before quoting lands in Task 5.

**Files:**
- Modify: `ui/overlay/sessionLaunchOptions.go:16-23` (the `LaunchOptions` struct)
- Modify: `app/remote_control.go` (`launchOptionsFromConfig`)
- Modify: `app/remote_control_test.go`

- [ ] **Step 1: Write the failing test**

Append to `app/remote_control_test.go`:

```go
func TestLaunchOptionsFromConfig_SeedsContext1M(t *testing.T) {
	t.Run("on", func(t *testing.T) {
		cfg := &config.Config{Claude1MContext: boolPtrTest(true)}
		assert.True(t, launchOptionsFromConfig(cfg).Context1M)
	})
	t.Run("off", func(t *testing.T) {
		cfg := &config.Config{Claude1MContext: boolPtrTest(false)}
		assert.False(t, launchOptionsFromConfig(cfg).Context1M)
	})
	t.Run("unset", func(t *testing.T) {
		assert.False(t, launchOptionsFromConfig(&config.Config{}).Context1M)
	})
	t.Run("nil config", func(t *testing.T) {
		assert.False(t, launchOptionsFromConfig(nil).Context1M)
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=0 go test ./app -run TestLaunchOptionsFromConfig_SeedsContext1M -v`

Expected: compile failure — `Context1M undefined (type overlay.LaunchOptions has no field or method Context1M)`.

- [ ] **Step 3: Write the implementation**

In `ui/overlay/sessionLaunchOptions.go`, add the field to `LaunchOptions` directly after `Model` (adjacency matters — it modifies the `Model` value):

```go
type LaunchOptions struct {
	RemoteControl  bool
	PermissionMode string
	Model          string
	// Context1M requests Claude's [1m] long-context suffix on Model.
	// Applied only when Model accepts it (see
	// config.ClaudeModelSupports1M); otherwise silently ignored.
	Context1M      bool
	HeadroomProxy  bool
	Effort         string
	CacheTTL1h     bool
}
```

In `app/remote_control.go`, add the seed to `launchOptionsFromConfig`'s returned literal, after `Model`:

```go
	return overlay.LaunchOptions{
		RemoteControl:  cfg.RemoteControlEnabled(),
		PermissionMode: cfg.PermissionMode(),
		Model:          cfg.Model(),
		Context1M:      cfg.Context1MEnabled(),
		HeadroomProxy:  cfg.HeadroomProxyEnabled(),
		Effort:         cfg.Effort(),
		CacheTTL1h:     cfg.CacheTTL1hEnabled(),
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `CGO_ENABLED=0 go test ./app -run TestLaunchOptionsFromConfig_SeedsContext1M -v`

Expected: PASS (all four subtests).

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add ui/overlay/sessionLaunchOptions.go app/remote_control.go app/remote_control_test.go
git commit -m "feat(app): carry Context1M through LaunchOptions"
```

---

### Task 4: Teach the decoder to accept quotes and the suffix

The decoder becomes a **superset** of what it accepted before, so this commit changes no existing behavior. It must land before Task 5 or the round-trip breaks mid-plan.

**Files:**
- Modify: `app/remote_control.go` (the `--model` case in `ParseLaunchOptions`, ~line 151; new helper)
- Modify: `app/remote_control_test.go`

- [ ] **Step 1: Write the failing test**

Append to `app/remote_control_test.go`:

```go
func TestParseModelValue(t *testing.T) {
	cases := []struct {
		name      string
		tok       string
		wantModel string
		want1M    bool
	}{
		{"bare", "opus", "opus", false},
		{"quoted", "'opus'", "opus", false},
		{"bare suffixed", "sonnet[1m]", "sonnet", true},
		{"quoted suffixed", "'sonnet[1m]'", "sonnet", true},
		{"uppercase suffix", "'sonnet[1M]'", "sonnet", true},
		{"mixed-case suffix", "opus[1M]", "opus", true},
		{"suffix only is not a suffix", "[1m]", "[1m]", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotModel, got1M := parseModelValue(tc.tok)
			assert.Equal(t, tc.wantModel, gotModel)
			assert.Equal(t, tc.want1M, got1M)
		})
	}
}

// A Program written by an older loom has an unquoted --model and no
// suffix. Those records are decoded on every load via
// Storage.LoadAndReconcile, so the decoder must keep accepting them.
func TestParseLaunchOptions_LegacyUnquotedModel(t *testing.T) {
	opts, base := ParseLaunchOptions("claude --model opus --effort high")
	assert.Equal(t, "claude", base)
	assert.Equal(t, "opus", opts.Model)
	assert.False(t, opts.Context1M)
	assert.Equal(t, "high", opts.Effort)
}

func TestParseLaunchOptions_HandEditedSuffix(t *testing.T) {
	opts, base := ParseLaunchOptions("claude --model sonnet[1m]")
	assert.Equal(t, "claude", base)
	assert.Equal(t, "sonnet", opts.Model)
	assert.True(t, opts.Context1M)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=0 go test ./app -run 'TestParseModelValue|TestParseLaunchOptions_LegacyUnquotedModel|TestParseLaunchOptions_HandEditedSuffix' -v`

Expected: compile failure — `undefined: parseModelValue`.

- [ ] **Step 3: Write the implementation**

In `app/remote_control.go`, change the `--model` case inside `ParseLaunchOptions` from:

```go
		case parts[i] == "--model" && i+1 < len(parts):
			opts.Model = parts[i+1]
			i++
```

to:

```go
		case parts[i] == "--model" && i+1 < len(parts):
			opts.Model, opts.Context1M = parseModelValue(parts[i+1])
			i++
```

Add the helper below `ParseLaunchOptions`:

```go
// parseModelValue decodes a --model token into its alias and whether
// Claude's [1m] long-context suffix was present.
//
// It is deliberately more permissive than what applyLaunchOptions
// emits. Single quotes are stripped because ApplyModelFlag now quotes
// its value, while every Program persisted before that change has none,
// and those records are decoded on every load through
// Storage.LoadAndReconcile — so both shapes must be accepted
// permanently. The suffix match is case-insensitive to mirror Claude
// Code's own /\[1m\]/i test, so a hand-edited Program decodes the same
// way loom's own output does.
//
// A token that is nothing but the suffix is returned unchanged rather
// than yielding an empty alias, so a degenerate input can't silently
// turn into "no --model flag".
func parseModelValue(tok string) (model string, context1M bool) {
	if len(tok) >= 2 && strings.HasPrefix(tok, "'") && strings.HasSuffix(tok, "'") {
		tok = tok[1 : len(tok)-1]
	}
	const suffix = "[1m]"
	if len(tok) > len(suffix) && strings.EqualFold(tok[len(tok)-len(suffix):], suffix) {
		return tok[:len(tok)-len(suffix)], true
	}
	return tok, false
}
```

`strings` is already imported in this file.

- [ ] **Step 4: Run the test to verify it passes**

Run: `CGO_ENABLED=0 go test ./app -run 'TestParseModelValue|TestParseLaunchOptions' -v`

Expected: PASS, including the pre-existing `TestParseLaunchOptions_RoundTrip` — the decoder is a superset, so nothing regresses.

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add app/remote_control.go app/remote_control_test.go
git commit -m "feat(app): decode quoted and [1m]-suffixed --model values"
```

---

### Task 5: Single-quote the `--model` value

**Files:**
- Modify: `session/agent/claude.go:136`
- Modify: `session/agent/adapter_test.go:149-171`

- [ ] **Step 1: Update the test to the new expectations**

In `session/agent/adapter_test.go`, replace the `cases` slice inside `TestClaudeModelFlag` with:

```go
	cases := []struct {
		name    string
		program string
		model   string
		want    string
	}{
		{"plain", "claude", "sonnet", "claude --model 'sonnet'"},
		{"preserves flags", "claude --permission-mode plan", "opus", "claude --model 'opus' --permission-mode plan"},
		{"absolute path", "/usr/bin/claude", "haiku", "/usr/bin/claude --model 'haiku'"},
		{"empty model is no-op", "claude --permission-mode plan", "", "claude --permission-mode plan"},
		{"\"default\" model is no-op", "claude --permission-mode plan", "default", "claude --permission-mode plan"},
		{"idempotent bare", "claude --model sonnet", "opus", "claude --model sonnet"},
		{"idempotent quoted", "claude --model 'sonnet'", "opus", "claude --model 'sonnet'"},
		{"idempotent equals form", "claude --model=sonnet", "opus", "claude --model=sonnet"},
		{"empty program", "", "sonnet", ""},
		// Brackets are zsh glob metacharacters and tmux runs the composed
		// program through a shell, so the suffix must reach Claude quoted
		// or the pane dies at launch with "no matches found".
		{"1m suffix is quoted", "claude", "sonnet[1m]", "claude --model 'sonnet[1m]'"},
	}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/agent -run TestClaudeModelFlag -v`

Expected: FAIL — e.g. `expected: "claude --model 'sonnet'"` / `actual: "claude --model sonnet"`.

- [ ] **Step 3: Write the implementation**

In `session/agent/claude.go`, change the final line of `ApplyModelFlag` from:

```go
	return insertAfterCommand(program, "--model "+model)
```

to:

```go
	return insertAfterCommand(program, "--model '"+model+"'")
```

Update the doc comment above `ApplyModelFlag`, replacing the sentence that reads `model is expected to come from config.ClaudeModels, never free-typed user input, so no sanitization is applied.` with:

```go
// The value is single-quoted because tmux runs the composed program
// string through a shell (session/tmux/tmux.go passes it to
// new-session as one argument), and the [1m] long-context suffix
// contains square brackets — glob metacharacters that zsh, with its
// default nomatch, treats as a fatal error rather than a literal. model
// still comes from config.ClaudeModels rather than free-typed input;
// the quoting makes any metacharacter in an alias inert regardless.
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session/agent ./app`

Expected: PASS for both. `./app` matters here — it proves the Task 4 decoder handles the newly quoted output, closing the round trip.

- [ ] **Step 5: Verify the quoting actually defeats zsh**

Run:

```bash
zsh -c "echo claude --model 'sonnet[1m]'"
```

Expected: prints `claude --model sonnet[1m]` and exits 0 (contrast with the unquoted form, which fails with `no matches found`).

- [ ] **Step 6: Commit**

```bash
gofmt -w .
git add session/agent/claude.go session/agent/adapter_test.go
git commit -m "fix(agent): single-quote the --model value so [1m] survives zsh"
```

---

### Task 6: Compose the suffix

**Files:**
- Modify: `app/remote_control.go` (`applyLaunchOptions`, ~line 92-98; new `effectiveModel`)
- Modify: `app/remote_control_test.go`

- [ ] **Step 1: Write the failing test**

Append to `app/remote_control_test.go`:

```go
func TestEffectiveModel(t *testing.T) {
	cases := []struct {
		name string
		opts overlay.LaunchOptions
		want string
	}{
		{"off", overlay.LaunchOptions{Model: "sonnet"}, "sonnet"},
		{"on, supported", overlay.LaunchOptions{Model: "sonnet", Context1M: true}, "sonnet[1m]"},
		{"on, opus", overlay.LaunchOptions{Model: "opus", Context1M: true}, "opus[1m]"},
		{"on, fable", overlay.LaunchOptions{Model: "fable", Context1M: true}, "fable[1m]"},
		{"on, haiku is a no-op", overlay.LaunchOptions{Model: "haiku", Context1M: true}, "haiku"},
		{"on, default is a no-op", overlay.LaunchOptions{Model: "default", Context1M: true}, "default"},
		{"on, unknown alias is a no-op", overlay.LaunchOptions{Model: "future", Context1M: true}, "future"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, effectiveModel(tc.opts))
		})
	}
}

func TestApplyLaunchOptions_Context1M(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	t.Run("supported model gets a quoted suffix", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "default", Model: "sonnet", Context1M: true, Effort: "default"}
		assert.Equal(t, "claude --model 'sonnet[1m]'", applyLaunchOptions(opts, authOK, "claude", "t"))
	})
	t.Run("unsupported model is left bare", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "default", Model: "haiku", Context1M: true, Effort: "default"}
		assert.Equal(t, "claude --model 'haiku'", applyLaunchOptions(opts, authOK, "claude", "t"))
	})
	t.Run("default model emits no flag at all", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "default", Model: "default", Context1M: true, Effort: "default"}
		assert.Equal(t, "claude", applyLaunchOptions(opts, authOK, "claude", "t"))
	})
}
```

Also extend the existing `TestParseLaunchOptions_RoundTrip` `cases` slice with three entries, and add one assertion to its subtest body. Add to `cases`:

```go
		{"1m on", overlay.LaunchOptions{PermissionMode: "default", Model: "sonnet", Context1M: true, Effort: "default"}},
		{"1m on with everything", overlay.LaunchOptions{RemoteControl: true, PermissionMode: "acceptEdits", Model: "opus", Context1M: true, Effort: "high"}},
		{"1m off explicitly", overlay.LaunchOptions{PermissionMode: "default", Model: "opus", Context1M: false, Effort: "default"}},
```

And add this line to the subtest body, after the `gotOpts.Model` assertion:

```go
			assert.Equal(t, tc.opts.Context1M, gotOpts.Context1M)
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./app -run 'TestEffectiveModel|TestApplyLaunchOptions_Context1M|TestParseLaunchOptions_RoundTrip' -v`

Expected: compile failure — `undefined: effectiveModel`.

- [ ] **Step 3: Write the implementation**

In `app/remote_control.go`, add `effectiveModel` directly below `effectiveRemoteControl`:

```go
// effectiveModel returns the --model value to launch with, appending
// Claude's [1m] long-context suffix when the option is on and the
// selected alias accepts it.
//
// An alias that doesn't support the suffix (default, haiku, or anything
// this build doesn't recognize) is returned unchanged rather than
// producing a value Claude would reject. That makes the toggle a silent
// no-op for those models, which is deliberate: the alternative failure
// is a pane that dies at launch. One consequence is documented in the
// design doc — {haiku, Context1M: true} composes to a bare "haiku", so
// re-decoding that Program yields Context1M false and the checkbox
// reverts on resume. The state was never meaningful, and the global
// config default is unaffected.
func effectiveModel(opts overlay.LaunchOptions) string {
	if opts.Context1M && config.ClaudeModelSupports1M(opts.Model) {
		return opts.Model + "[1m]"
	}
	return opts.Model
}
```

Then change the model line in `applyLaunchOptions` from:

```go
	program = modelProgram(opts.Model, program)
```

to:

```go
	program = modelProgram(effectiveModel(opts), program)
```

`config` is already imported in this file.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./app -v`

Expected: PASS, including the extended round-trip matrix.

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add app/remote_control.go app/remote_control_test.go
git commit -m "feat(app): compose the [1m] suffix onto supported models"
```

---

### Task 7: Add the row to Session Launch Options

Inserted at index 3, directly after Model, which shifts the three rows below it by one.

**Files:**
- Modify: `ui/overlay/sessionLaunchOptions.go:39-42` (row count), `:88-105` (`toggleCursor`), `:141-160` (render)
- Modify: `ui/overlay/sessionLaunchOptions_test.go`

- [ ] **Step 1: Write the failing test**

Append to `ui/overlay/sessionLaunchOptions_test.go`:

```go
func TestSessionLaunchOptions_Context1MRow(t *testing.T) {
	t.Run("space on row 3 toggles Context1M", func(t *testing.T) {
		l := NewSessionLaunchOptions(LaunchOptions{Model: "sonnet"}, false, "")
		l.cursor = 3
		l.toggleCursor()
		assert.True(t, l.opts.Context1M)
		l.toggleCursor()
		assert.False(t, l.opts.Context1M)
	})

	t.Run("toggling 1M leaves every other option alone", func(t *testing.T) {
		before := LaunchOptions{
			RemoteControl:  true,
			PermissionMode: "plan",
			Model:          "opus",
			HeadroomProxy:  false,
			Effort:         "high",
			CacheTTL1h:     true,
		}
		l := NewSessionLaunchOptions(before, false, "")
		l.cursor = 3
		l.toggleCursor()
		assert.Equal(t, before.RemoteControl, l.opts.RemoteControl)
		assert.Equal(t, before.PermissionMode, l.opts.PermissionMode)
		assert.Equal(t, before.Model, l.opts.Model)
		assert.Equal(t, before.HeadroomProxy, l.opts.HeadroomProxy)
		assert.Equal(t, before.Effort, l.opts.Effort)
		assert.Equal(t, before.CacheTTL1h, l.opts.CacheTTL1h)
	})

	t.Run("rows below Model shifted down by one", func(t *testing.T) {
		l := NewSessionLaunchOptions(LaunchOptions{Effort: "default"}, false, "")
		l.cursor = 4
		l.toggleCursor()
		assert.True(t, l.opts.HeadroomProxy)

		l = NewSessionLaunchOptions(LaunchOptions{Effort: "default"}, false, "")
		l.cursor = 5
		l.toggleCursor()
		assert.Equal(t, "low", l.opts.Effort)

		l = NewSessionLaunchOptions(LaunchOptions{Effort: "default"}, false, "")
		l.cursor = 6
		l.toggleCursor()
		assert.True(t, l.opts.CacheTTL1h)
	})

	t.Run("render shows the row and its state", func(t *testing.T) {
		l := NewSessionLaunchOptions(LaunchOptions{Model: "sonnet", Context1M: true}, false, "")
		assert.Contains(t, l.Render(), "1M Context")
	})

	t.Run("row count", func(t *testing.T) {
		assert.Equal(t, 7, sessionLaunchOptionsRowCount)
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/overlay -run TestSessionLaunchOptions_Context1MRow -v`

Expected: FAIL — row count is 6 not 7; cursor 3 toggles `HeadroomProxy` instead of `Context1M`.

- [ ] **Step 3: Write the implementation**

Change the row-count constant and its comment:

```go
// sessionLaunchOptionsRowCount is the number of navigable rows: Remote
// Control, Permission Mode, Model, 1M Context, Headroom Proxy, Effort,
// and Cache TTL (1h).
const sessionLaunchOptionsRowCount = 7
```

In `toggleCursor`, replace cases 3-5 with cases 3-6:

```go
	case 3:
		l.opts.Context1M = !l.opts.Context1M
	case 4:
		l.opts.HeadroomProxy = !l.opts.HeadroomProxy
		if l.opts.HeadroomProxy {
			l.opts.RemoteControl = false
		}
	case 5:
		l.opts.Effort = nextInList(config.ClaudeEfforts, l.opts.Effort)
	case 6:
		l.opts.CacheTTL1h = !l.opts.CacheTTL1h
	}
```

In `Render`, add the checkbox variable beside the existing ones:

```go
	ctxCheck := "[ ]"
	if l.opts.Context1M {
		ctxCheck = "[x]"
	}
```

and rewrite the `content` row list:

```go
	content := sessionLaunchOptionsTitleStyle.Render("Session Launch Options") + "\n\n" +
		row(0, "Remote Control    ", rcCheck) + "\n" +
		row(1, "Permission Mode   ", "< "+l.opts.PermissionMode+" >") + "\n" +
		row(2, "Model             ", "< "+l.opts.Model+" >") + "\n" +
		row(3, "1M Context        ", ctxCheck) + "\n" +
		row(4, "Headroom Proxy    ", hwCheck) + "\n" +
		row(5, "Effort            ", "< "+l.opts.Effort+" >") + "\n" +
		row(6, "Cache TTL (1h)    ", cacheCheck) + "\n\n" +
		sessionLaunchOptionsHintStyle.Render("up/down move • space toggle/cycle • enter start • esc cancel")
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./ui/overlay -v`

Expected: PASS, including the pre-existing overlay tests.

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add ui/overlay/sessionLaunchOptions.go ui/overlay/sessionLaunchOptions_test.go
git commit -m "feat(ui): add the 1M Context row to Session Launch Options"
```

---

### Task 8: Add the row to Claude Preferences

Same position, same shift. This overlay writes through `cfg.Mutate` and renders each row explicitly rather than through a `row()` helper.

**Files:**
- Modify: `ui/overlay/claudePreferences.go:33-36` (row count), `:63-107` (toggle switch), `:176-247` (render)
- Modify: `ui/overlay/claudePreferences_test.go`

- [ ] **Step 1: Write the failing test**

Append to `ui/overlay/claudePreferences_test.go`:

```go
func TestClaudePreferences_Context1MRow(t *testing.T) {
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}

	t.Run("row count", func(t *testing.T) {
		assert.Equal(t, 8, claudePrefsRowCount)
	})

	t.Run("enter on row 3 toggles Claude1MContext", func(t *testing.T) {
		cfg := &config.Config{}
		cp := NewClaudePreferences(cfg, false, "")
		cp.cursor = 3
		_, changed := cp.HandleKeyPress(enter)
		assert.True(t, changed)
		assert.True(t, cfg.Context1MEnabled())
		cp.HandleKeyPress(enter)
		assert.False(t, cfg.Context1MEnabled())
	})

	t.Run("rows below Model shifted down by one", func(t *testing.T) {
		cfg := &config.Config{}
		cp := NewClaudePreferences(cfg, false, "")
		cp.cursor = 4
		cp.HandleKeyPress(enter)
		assert.True(t, cfg.HeadroomProxyEnabled())

		cfg = &config.Config{}
		cp = NewClaudePreferences(cfg, false, "")
		cp.cursor = 5
		cp.HandleKeyPress(enter)
		assert.Equal(t, "low", cfg.Effort())

		cfg = &config.Config{}
		cp = NewClaudePreferences(cfg, false, "")
		cp.cursor = 6
		cp.HandleKeyPress(enter)
		assert.True(t, cfg.CacheTTL1hEnabled())

		cfg = &config.Config{}
		cp = NewClaudePreferences(cfg, false, "")
		cp.cursor = 7
		cp.HandleKeyPress(enter)
		// Loom Context defaults to enabled, so one toggle turns it off.
		assert.False(t, cfg.LoomContextEnabled())
	})

	t.Run("render shows the row", func(t *testing.T) {
		cp := NewClaudePreferences(&config.Config{}, false, "")
		assert.Contains(t, cp.Render(), "1M Context")
	})
}
```

`cursor` is unexported but the test lives in `package overlay`, so setting it
directly is how these tests target a specific row without simulating `j`
presses. `tea`, `config`, `assert`, and a `boolPtr` helper are already imported
or declared in this file.

- [ ] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/overlay -run TestClaudePreferences_Context1MRow -v`

Expected: FAIL — row count is 7 not 8; cursor 3 toggles Headroom Proxy.

- [ ] **Step 3: Write the implementation**

Change the row-count constant and its comment to list eight rows ending in Loom Context:

```go
const claudePrefsRowCount = 8
```

In the `case " ", "space", "enter":` switch, insert a new `case 3` and renumber the four below it:

```go
		case 3:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.Context1MEnabled()
				cc.Claude1MContext = &v
			})
		case 4:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.HeadroomProxyEnabled()
				cc.HeadroomProxy = &v
				if v {
					rc := false
					cc.ClaudeRemoteControl = &rc
				}
			})
		case 5:
			c.cfg.Mutate(func(cc *config.Config) {
				next := nextInList(config.ClaudeEfforts, cc.Effort())
				cc.ClaudeEffort = &next
			})
		case 6:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.CacheTTL1hEnabled()
				cc.CacheTTL1h = &v
			})
		case 7:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.LoomContextEnabled()
				cc.ClaudeLoomContext = &v
			})
		}
```

In `Render`, add this block immediately after the `modelRow` block:

```go
	ctxCheck := "[ ]"
	if c.cfg.Context1MEnabled() {
		ctxCheck = "[x]"
	}
	ctxCursor := "  "
	if c.cursor == 3 {
		ctxCursor = "> "
	}
	ctxRow := ctxCursor + "1M Context        " + ctxCheck
	if c.cursor == 3 {
		ctxRow = claudePrefsSelectedStyle.Render(ctxRow)
	} else {
		ctxRow = claudePrefsRowStyle.Render(ctxRow)
	}
```

Then bump the cursor index in each of the four blocks below it — `hwRow` 3→4, `effortRow` 4→5, `cacheRow` 5→6, `loomRow` 6→7 (each appears twice per block: once selecting the cursor glyph, once choosing the style). Finally add `ctxRow` to the `content` concatenation between `modelRow` and `hwRow`:

```go
	content := claudePrefsTitleStyle.Render("Claude Preferences") + "\n\n" +
		rcRow + "\n" +
		pmRow + "\n" +
		modelRow + "\n" +
		ctxRow + "\n" +
		hwRow + "\n" +
		effortRow + "\n" +
		cacheRow + "\n" +
		loomRow + "\n\n" +
		claudePrefsHintStyle.Render("up/down move • enter/space toggle/cycle • esc back")
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./ui/overlay -v`

Expected: PASS, including the pre-existing Claude Preferences tests.

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add ui/overlay/claudePreferences.go ui/overlay/claudePreferences_test.go
git commit -m "feat(ui): add the 1M Context row to Claude Preferences"
```

---

### Task 9: Document the option and verify the whole feature

**Files:**
- Modify: `CLAUDE.md` (the `config.json` bullet under "Persistent State")

- [ ] **Step 1: Update CLAUDE.md**

In the `config.json` bullet, append to the list of documented fields, immediately after the `ClaudePermissionMode` entry:

```
`Claude1MContext` (`*bool`, default off — appends Claude's `[1m]` long-context suffix to the launched `--model` alias, e.g. `sonnet[1m]`; a no-op for `default`, `haiku`, and any alias whose `config.ClaudeModel.Supports1M` is false, read via `Config.Context1MEnabled()`; the composed value is single-quoted because tmux runs the program string through a shell and `[`/`]` are zsh glob metacharacters)
```

- [ ] **Step 2: Run the full test suite**

Run: `CGO_ENABLED=0 go test ./...`

Expected: all packages PASS.

- [ ] **Step 3: Run the race detector**

Run: `CC=clang CGO_ENABLED=1 go test -race ./...`

Expected: PASS with no race reports. (The repo defaults `CGO_ENABLED=0`, which disables `-race`; `CC=clang` is needed where gcc is absent.)

- [ ] **Step 4: Verify formatting and vet**

Run: `gofmt -l . && CGO_ENABLED=0 go vet ./...`

Expected: `gofmt -l` prints nothing; `go vet` reports nothing.

- [ ] **Step 5: Verify the built binary end-to-end**

Run:

```bash
CGO_ENABLED=0 go build -o loom
```

Then start loom, press `S` → Claude Preferences, confirm a `1M Context` row sits directly below `Model` and toggles. Press `N` to create a session, confirm the same row appears in Session Launch Options, set Model to `sonnet` and 1M Context to `[x]`, start the session, and confirm the agent pane launches (does **not** die with `no matches found`). Then verify the composed command:

```bash
tmux list-panes -a -F '#{pane_start_command}' | grep -- --model
```

Expected: a line containing `--model 'sonnet[1m]'`. Inside the running Claude session, `/status` should report the 1M context window.

- [ ] **Step 6: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document the Claude1MContext config field"
```

---

## Verification checklist

- [ ] `1M Context` appears directly below `Model` in both overlays and toggles independently of every other row.
- [ ] `sonnet`/`opus`/`fable` + 1M compose to `--model 'sonnet[1m]'` and friends.
- [ ] `haiku` + 1M composes to `--model 'haiku'`; `default` + 1M emits no `--model` flag.
- [ ] A session started with 1M on actually launches under zsh.
- [ ] `R` on a 1M session re-opens the modal with the checkbox still set.
- [ ] A pre-existing `instances.json` with `--model opus` still loads and decodes to `{opus, 1m:off}`.
- [ ] `CGO_ENABLED=0 go test ./...` and `CC=clang CGO_ENABLED=1 go test -race ./...` both pass.
