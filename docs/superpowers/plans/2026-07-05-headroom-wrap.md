# Headroom Wrap, Claude Model, and Launch Options Modal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `HeadroomWrap` config toggle (wraps any agent's launch command as `headroom wrap <program>`, mutually exclusive with Claude's Remote Control), a `ClaudeModel` setting (`--model` cycling through `default`/`sonnet`/`opus`/`haiku`), and a per-instance "Session Launch Options" modal shown right before a new session starts, letting the user override all four launch toggles (Remote Control, Permission Mode, Model, Headroom Wrap) for just that session.

**Architecture:** A new `overlay.LaunchOptions` value type (in `ui/overlay`, to avoid an import cycle back to `app`) replaces ad-hoc `*config.Config` reads in the launch-command composition functions in `app/remote_control.go`. A new `overlay.SessionLaunchOptions` component edits that value ephemerally; a new `stateLaunchOptions` app state and `app/state_launch_options.go` handler sit between title/prompt entry and instance start, invoking a closure (`home.pendingLaunchOptions`) that `state_new.go`/`state_prompt.go` stash before opening the modal.

**Tech Stack:** Go 1.23, Bubble Tea v2 (`charm.land/bubbletea/v2`), Lipgloss v2, testify.

**Spec:** `docs/superpowers/specs/2026-07-05-headroom-wrap-design.md`

---

## Notes for the implementer

- Run `go test ./...` from the repo root after every task; run `gofmt -w .` before every commit (CI enforces formatting).
- `rtk grep`/`grep` are interchangeable for searching this repo; either works.
- Two package-level type/struct decisions were pinned down during planning (not fully spelled out in the spec):
  - `LaunchOptions` (the four-toggle value type) lives in `ui/overlay`, not `app`, because `ui/overlay` is a leaf package `app` already imports — defining it in `app` and needing `ui/overlay.SessionLaunchOptions` to also know its shape would require a cycle.
  - `nextInList` (generalized from the existing `nextPermissionMode`) also lives in `ui/overlay`, shared by both `ClaudePreferences` and `SessionLaunchOptions`.
- No `state_new_test.go` / `state_prompt_test.go` / `state_launch_options_test.go` exist yet in `app/` — Tasks 8 and 9 create them fresh with focused coverage of the behavior being added, not exhaustive coverage of the whole file.
- The Session Launch Options modal reuses `ui.StateNewInstance` for the bottom menu bar (via `m.menu.SetState(ui.StateNewInstance)`) rather than adding a new `ui.MenuState`/`keys.KeyName` pair just for this screen's "enter: start" hint. The modal renders its own full hint line ("space toggle/cycle • enter start • esc cancel") inside its box, so the bottom menu bar showing "enter: submit name" during this state is a minor, pre-existing-style cosmetic approximation, not a functional gap — `SettingsOverlay`'s Claude Preferences sub-screen makes the same tradeoff (no dedicated menu state for its own enter/space/esc hints either).
- **Before shipping, verify `sonnet`/`opus`/`haiku` are the literal strings the installed Claude CLI's `--model` flag accepts as latest-resolving aliases** (vs. e.g. requiring a `claude-` prefix or different casing) — this was flagged as an open item in the spec and not independently confirmed during planning. If the real CLI expects different literals, update `config.ClaudeModels` (Task 1) before or shortly after implementing; every other task only depends on the list being *some* slice of strings, not on these specific values, so this is a cheap fix in one place if wrong.

---

### Task 1: Config layer — `HeadroomWrap` and `ClaudeModel`

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `config/config_test.go`, after `TestClaudePermissionModes` (currently ending at line 394):

```go
func TestHeadroomWrapEnabled(t *testing.T) {
	t.Run("nil (absent from config) defaults to disabled", func(t *testing.T) {
		cfg := &Config{}
		assert.False(t, cfg.HeadroomWrapEnabled())
	})

	t.Run("explicit true is enabled", func(t *testing.T) {
		cfg := &Config{HeadroomWrap: boolPtr(true)}
		assert.True(t, cfg.HeadroomWrapEnabled())
	})

	t.Run("explicit false is disabled", func(t *testing.T) {
		cfg := &Config{HeadroomWrap: boolPtr(false)}
		assert.False(t, cfg.HeadroomWrapEnabled())
	})

	t.Run("DefaultConfig disables it", func(t *testing.T) {
		assert.False(t, DefaultConfig().HeadroomWrapEnabled())
	})
}

func TestModel(t *testing.T) {
	t.Run("nil (absent from config) defaults to \"default\"", func(t *testing.T) {
		cfg := &Config{}
		assert.Equal(t, "default", cfg.Model())
	})

	t.Run("explicit value round-trips", func(t *testing.T) {
		cfg := &Config{ClaudeModel: stringPtr("opus")}
		assert.Equal(t, "opus", cfg.Model())
	})

	t.Run("DefaultConfig sets \"default\" explicitly", func(t *testing.T) {
		cfg := DefaultConfig()
		if assert.NotNil(t, cfg.ClaudeModel) {
			assert.Equal(t, "default", *cfg.ClaudeModel)
		}
		assert.Equal(t, "default", cfg.Model())
	})
}

func TestClaudeModels(t *testing.T) {
	assert.Equal(t, []string{"default", "sonnet", "opus", "haiku"}, ClaudeModels)
}
```

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `go test ./config/... -run 'TestHeadroomWrapEnabled|TestModel|TestClaudeModels' -v`
Expected: FAIL — `cfg.HeadroomWrapEnabled undefined`, `HeadroomWrap undefined`, `cfg.Model undefined`, `ClaudeModel undefined`, `ClaudeModels undefined`.

- [ ] **Step 3: Implement the config fields, accessors, and list**

In `config/config.go`, add two fields to the `Config` struct immediately after `ClaudePermissionMode` (currently the last field, line 108):

```go
	// ClaudePermissionMode is the --permission-mode value new Claude
	// sessions launch with. Unlike ClaudeRemoteControl, DefaultConfig
	// sets this explicitly to "default" rather than leaving it nil — nil
	// only occurs for a config.json predating this field, and is
	// treated identically to "default" (no flag injected; Claude's own
	// default applies). Read it through PermissionMode.
	ClaudePermissionMode *string `json:"claude_permission_mode,omitempty"`
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
	// Values are short CLI aliases (not versioned IDs) so the list
	// stays valid as new models ship without a code change. "default"
	// is a no-op — Claude's own default applies. Read it through Model.
	ClaudeModel *string `json:"claude_model,omitempty"`
}
```

Add `ClaudeModels` right after the existing `ClaudePermissionModes` var (line 113):

```go
// ClaudePermissionModes lists the values --permission-mode accepts, in
// the order the Claude Preferences screen cycles through them.
var ClaudePermissionModes = []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"}

// ClaudeModels lists the --model aliases the Claude Preferences and
// Session Launch Options screens cycle through. Short aliases, not
// versioned IDs, so this list doesn't need updating when new Claude
// models ship.
var ClaudeModels = []string{"default", "sonnet", "opus", "haiku"}
```

Add the two accessors right after `PermissionMode()` (currently lines 142-155):

```go
// HeadroomWrapEnabled reports whether new sessions should launch
// wrapped as `headroom wrap <program>`. Defaults to false when unset.
func (c *Config) HeadroomWrapEnabled() bool {
	return c.HeadroomWrap != nil && *c.HeadroomWrap
}

// Model returns the configured --model alias, defaulting to "default"
// when unset (nil). Unlocked for the same reason as PermissionMode.
func (c *Config) Model() string {
	if c.ClaudeModel == nil {
		return "default"
	}
	return *c.ClaudeModel
}
```

Update `DefaultConfig()` (lines 200-213) to set both new fields explicitly:

```go
	return &Config{
		DefaultProgram:     program,
		DaemonPollInterval: 1000,
		BranchPrefix: func() string {
			user, err := user.Current()
			if err != nil || user == nil || user.Username == "" {
				log.For("config").Error("get_current_user_failed", "err", err)
				return "session/"
			}
			return fmt.Sprintf("%s/", strings.ToLower(user.Username))
		}(),
		ClaudeRemoteControl:  boolPtr(true),
		ClaudePermissionMode: stringPtr("default"),
		HeadroomWrap:         boolPtr(false),
		ClaudeModel:          stringPtr("default"),
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./config/... -run 'TestHeadroomWrapEnabled|TestModel|TestClaudeModels' -v`
Expected: PASS

- [ ] **Step 5: Format and commit**

```bash
gofmt -w config/config.go config/config_test.go
git add config/config.go config/config_test.go
git commit -m "feat(config): add HeadroomWrap and ClaudeModel settings"
```

---

### Task 2: Adapter layer — `ApplyModelFlag`

**Files:**
- Modify: `session/agent/adapter.go`
- Modify: `session/agent/claude.go`
- Modify: `session/agent/aider.go`
- Modify: `session/agent/gemini.go`
- Modify: `session/agent/default.go`
- Modify: `session/agent/adapter_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `session/agent/adapter_test.go`, after `TestClaudePermissionModeComposesWithRemoteControl` (currently ending at line 141):

```go
func TestClaudeModelFlag(t *testing.T) {
	c := Claude()

	cases := []struct {
		name    string
		program string
		model   string
		want    string
	}{
		{"plain", "claude", "sonnet", "claude --model sonnet"},
		{"preserves flags", "claude --permission-mode plan", "opus", "claude --model opus --permission-mode plan"},
		{"absolute path", "/usr/bin/claude", "haiku", "/usr/bin/claude --model haiku"},
		{"empty model is no-op", "claude --permission-mode plan", "", "claude --permission-mode plan"},
		{"\"default\" model is no-op", "claude --permission-mode plan", "default", "claude --permission-mode plan"},
		{"idempotent bare", "claude --model sonnet", "opus", "claude --model sonnet"},
		{"idempotent equals form", "claude --model=sonnet", "opus", "claude --model=sonnet"},
		{"empty program", "", "sonnet", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, c.ApplyModelFlag(tc.program, tc.model))
		})
	}
}

func TestClaudeModelComposesWithPermissionModeAndRemoteControl(t *testing.T) {
	c := Claude()
	withRC := c.ApplyRemoteControlFlag("claude", "my task")
	withPM := c.ApplyPermissionModeFlag(withRC, "acceptEdits")
	got := c.ApplyModelFlag(withPM, "opus")
	assert.Equal(t, "claude --model opus --permission-mode acceptEdits --remote-control my-task", got)
}

func TestNonClaudeAdaptersNoModelFlag(t *testing.T) {
	assert.Equal(t, "aider --model x", Aider().ApplyModelFlag("aider --model x", "opus"))
	assert.Equal(t, "gemini", Gemini().ApplyModelFlag("gemini", "opus"))
	assert.Equal(t, "codex --foo", Default().ApplyModelFlag("codex --foo", "opus"))
}
```

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `go test ./session/agent/... -run 'TestClaudeModelFlag|TestClaudeModelComposesWithPermissionModeAndRemoteControl|TestNonClaudeAdaptersNoModelFlag' -v`
Expected: FAIL — `c.ApplyModelFlag undefined` (method doesn't exist on the `Adapter` interface yet).

- [ ] **Step 3: Add `ApplyModelFlag` to the interface and every adapter**

In `session/agent/adapter.go`, add to the `Adapter` interface, right after `ApplyPermissionModeFlag` (line 73):

```go
	// ApplyPermissionModeFlag returns the program string with
	// "--permission-mode <mode>" inserted (e.g. "claude
	// --permission-mode acceptEdits"). mode == "" or "default" is a
	// no-op — Claude's own default already matches. Idempotent: if
	// --permission-mode is already present, the input is returned
	// unchanged. Returns the input unchanged for agents without a
	// permission-mode concept.
	ApplyPermissionModeFlag(program, mode string) string
	// ApplyModelFlag returns the program string with "--model <model>"
	// inserted (e.g. "claude --model opus"). model == "" or "default"
	// is a no-op — Claude's own default already matches. Idempotent: if
	// --model is already present, the input is returned unchanged.
	// Returns the input unchanged for agents without a model-selection
	// concept.
	ApplyModelFlag(program, model string) string
}
```

In `session/agent/claude.go`, add after `ApplyPermissionModeFlag` (end of file, after line 116):

```go

// ApplyModelFlag inserts "--model <model>" after "claude". model == ""
// or "default" is a no-op — Claude's own default already matches.
// Returns program unchanged if a --model flag is already present or if
// program is empty. model is expected to come from config.ClaudeModels,
// never free-typed user input, so no sanitization is applied.
func (claudeAdapter) ApplyModelFlag(program, model string) string {
	if model == "" || model == "default" {
		return program
	}
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}
	for _, p := range parts[1:] {
		if p == "--model" || strings.HasPrefix(p, "--model=") {
			return program
		}
	}
	return parts[0] + " --model " + model + strings.TrimPrefix(program, parts[0])
}
```

In `session/agent/aider.go`, add after `ApplyPermissionModeFlag` (end of file, after line 46):

```go

// ApplyModelFlag is a no-op for aider — model selection isn't exposed
// through this settings screen for non-Claude agents.
func (aiderAdapter) ApplyModelFlag(program, _ string) string {
	return program
}
```

In `session/agent/gemini.go`, add after `ApplyPermissionModeFlag` (end of file, after line 45):

```go

// ApplyModelFlag is a no-op for gemini — model selection isn't exposed
// through this settings screen for non-Claude agents.
func (geminiAdapter) ApplyModelFlag(program, _ string) string {
	return program
}
```

In `session/agent/default.go`, add after `ApplyPermissionModeFlag` (line 37):

```go
// ApplyModelFlag implements Adapter. The fallback adapter never
// modifies the program string, so unknown agents get no model flag.
func (defaultAdapter) ApplyModelFlag(program, _ string) string { return program }

```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./session/agent/... -v`
Expected: PASS (all tests in the package, including the pre-existing ones — confirms the interface addition didn't break any other adapter).

- [ ] **Step 5: Format and commit**

```bash
gofmt -w session/agent/
git add session/agent/
git commit -m "feat(agent): add ApplyModelFlag to the Adapter interface"
```

---

### Task 3: `session/agent_restart.go` — `BuildModelCommand` and `BuildHeadroomWrapCommand`

**Files:**
- Modify: `session/agent_restart.go`
- Modify: `session/agent_restart_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `session/agent_restart_test.go`, after `TestBuildPermissionModeCommand_Unknown` (currently ending at line 111):

```go

func TestBuildModelCommand_Claude(t *testing.T) {
	assert.Equal(t, "claude --model opus", BuildModelCommand("claude", "opus"))
}

func TestBuildModelCommand_ClaudeWithFlags(t *testing.T) {
	assert.Equal(t,
		"claude --model opus --permission-mode plan",
		BuildModelCommand("claude --permission-mode plan", "opus"),
	)
}

func TestBuildModelCommand_DefaultModelIsNoOp(t *testing.T) {
	assert.Equal(t, "claude --permission-mode plan", BuildModelCommand("claude --permission-mode plan", "default"))
	assert.Equal(t, "claude --permission-mode plan", BuildModelCommand("claude --permission-mode plan", ""))
}

func TestBuildModelCommand_Idempotent(t *testing.T) {
	assert.Equal(t,
		"claude --model opus",
		BuildModelCommand("claude --model opus", "sonnet"),
	)
}

func TestBuildModelCommand_Aider(t *testing.T) {
	assert.Equal(t, "aider --model gemma", BuildModelCommand("aider --model gemma", "opus"))
}

func TestBuildModelCommand_Unknown(t *testing.T) {
	assert.Equal(t, "codex", BuildModelCommand("codex", "opus"))
}

func TestBuildHeadroomWrapCommand_Claude(t *testing.T) {
	assert.Equal(t, "headroom wrap claude", BuildHeadroomWrapCommand("claude"))
}

func TestBuildHeadroomWrapCommand_ClaudeWithFlags(t *testing.T) {
	assert.Equal(t, "headroom wrap claude --model opus", BuildHeadroomWrapCommand("claude --model opus"))
}

func TestBuildHeadroomWrapCommand_Aider(t *testing.T) {
	assert.Equal(t, "headroom wrap aider --model gemma", BuildHeadroomWrapCommand("aider --model gemma"))
}

func TestBuildHeadroomWrapCommand_Idempotent(t *testing.T) {
	assert.Equal(t, "headroom wrap claude", BuildHeadroomWrapCommand("headroom wrap claude"))
}
```

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `go test ./session/... -run 'TestBuildModelCommand|TestBuildHeadroomWrapCommand' -v`
Expected: FAIL — `BuildModelCommand undefined`, `BuildHeadroomWrapCommand undefined`.

- [ ] **Step 3: Implement both functions**

Replace the full contents of `session/agent_restart.go`:

```go
package session

import (
	"strings"

	"github.com/aidan-bailey/loom/session/agent"
)

// defaultRegistry is the package-level adapter registry used by
// BuildRecoveryCommand and other call sites that don't have a scoped
// registry handy. A test can swap this out if needed.
var defaultRegistry = agent.DefaultRegistry()

// BuildRecoveryCommand modifies a program command string for crash
// recovery. The adapter registry decides whether and how the string is
// modified (e.g. "claude" → "claude --continue"). Unsupported agents
// are returned unchanged.
func BuildRecoveryCommand(program string) string {
	return defaultRegistry.Lookup(program).ApplyRecoveryFlag(program)
}

// BuildRemoteControlCommand modifies a program command string to launch
// the agent with its remote-control mode enabled, naming the remote
// session after sessionName. The adapter registry decides whether and
// how the string is modified (e.g. "claude" → "claude --remote-control
// <name>"). Idempotent, and a no-op for agents without a remote-control
// mode.
func BuildRemoteControlCommand(program, sessionName string) string {
	return defaultRegistry.Lookup(program).ApplyRemoteControlFlag(program, sessionName)
}

// BuildPermissionModeCommand modifies a program command string to
// launch with the given --permission-mode value. The adapter registry
// decides whether and how the string is modified. Idempotent, and a
// no-op for agents without a permission-mode concept or when mode is
// "" / "default".
func BuildPermissionModeCommand(program, mode string) string {
	return defaultRegistry.Lookup(program).ApplyPermissionModeFlag(program, mode)
}

// BuildModelCommand modifies a program command string to launch with
// the given --model value. The adapter registry decides whether and how
// the string is modified. Idempotent, and a no-op for agents without a
// model-selection concept or when model is "" / "default".
func BuildModelCommand(program, model string) string {
	return defaultRegistry.Lookup(program).ApplyModelFlag(program, model)
}

// BuildHeadroomWrapCommand prefixes program with "headroom wrap ",
// wrapping the agent invocation regardless of which adapter matches —
// Headroom's context compression works the same way for every agent, so
// this bypasses the adapter registry entirely. Idempotent: no-ops if
// program is already wrapped.
func BuildHeadroomWrapCommand(program string) string {
	parts := strings.Fields(program)
	if len(parts) >= 2 && parts[0] == "headroom" && parts[1] == "wrap" {
		return program
	}
	return "headroom wrap " + program
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./session/... -run 'TestBuildModelCommand|TestBuildHeadroomWrapCommand' -v`
Expected: PASS

- [ ] **Step 5: Format and commit**

```bash
gofmt -w session/agent_restart.go session/agent_restart_test.go
git add session/agent_restart.go session/agent_restart_test.go
git commit -m "feat(session): add BuildModelCommand and BuildHeadroomWrapCommand"
```

---

### Task 4: `ClaudePreferences` — generalize cycling, add Model and Headroom Wrap rows

**Files:**
- Modify: `ui/overlay/claudePreferences.go`
- Modify: `ui/overlay/claudePreferences_test.go`

- [ ] **Step 1: Write the failing tests**

Replace the full contents of `ui/overlay/claudePreferences_test.go`:

```go
package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func boolPtr(b bool) *bool { return &b }

func TestClaudePreferencesTogglesRemoteControl(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.True(t, cfg.RemoteControlEnabled(), "nil ClaudeRemoteControl defaults to enabled")

	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.RemoteControlEnabled())

	_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, changed)
	assert.True(t, cfg.RemoteControlEnabled())
}

func TestClaudePreferencesCyclesPermissionMode(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.Equal(t, "default", cfg.PermissionMode())

	// Move focus down to the Permission Mode row.
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})

	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.Equal(t, "acceptEdits", cfg.PermissionMode())

	for _, want := range []string{"plan", "auto", "dontAsk", "bypassPermissions", "default"} {
		_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.True(t, changed)
		assert.Equal(t, want, cfg.PermissionMode())
	}
}

func TestClaudePreferencesCyclesModel(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.Equal(t, "default", cfg.Model())

	// Move focus down to the Model row (row 2).
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})

	for _, want := range []string{"sonnet", "opus", "haiku", "default"} {
		_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.True(t, changed)
		assert.Equal(t, want, cfg.Model())
	}
}

func TestClaudePreferencesHeadroomWrapExcludesRemoteControl(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.True(t, cfg.RemoteControlEnabled())

	// Move focus down to the Headroom Wrap row (row 3) and enable it.
	for i := 0; i < 3; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.True(t, cfg.HeadroomWrapEnabled())
	assert.False(t, cfg.RemoteControlEnabled(), "enabling Headroom Wrap must disable Remote Control")
}

func TestClaudePreferencesRemoteControlExcludesHeadroomWrap(t *testing.T) {
	cfg := &config.Config{HeadroomWrap: boolPtr(true), ClaudeRemoteControl: boolPtr(false)}
	cp := NewClaudePreferences(cfg, false, "")
	assert.True(t, cfg.HeadroomWrapEnabled())

	// Row 0 (Remote Control) is already focused by default.
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.True(t, cfg.RemoteControlEnabled())
	assert.False(t, cfg.HeadroomWrapEnabled(), "enabling Remote Control must disable Headroom Wrap")
}

func TestClaudePreferencesRowNavigationClamps(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")

	// Up from row 0 stays at row 0: toggles Remote Control, not any other row.
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.RemoteControlEnabled())

	// Down four times stays at row 3 (only four rows): toggles Headroom
	// Wrap, not any earlier row.
	for i := 0; i < 4; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.True(t, cfg.HeadroomWrapEnabled())
}

func TestClaudePreferencesRendersPermissionMode(t *testing.T) {
	mode := "plan"
	cfg := &config.Config{ClaudePermissionMode: &mode}
	cp := NewClaudePreferences(cfg, false, "")
	rendered := cp.Render()
	assert.Contains(t, rendered, "Permission Mode")
	assert.Contains(t, rendered, "plan")
}

func TestClaudePreferencesRendersModel(t *testing.T) {
	model := "opus"
	cfg := &config.Config{ClaudeModel: &model}
	cp := NewClaudePreferences(cfg, false, "")
	rendered := cp.Render()
	assert.Contains(t, rendered, "Model")
	assert.Contains(t, rendered, "opus")
}

func TestClaudePreferencesRendersHeadroomWrap(t *testing.T) {
	cfg := &config.Config{HeadroomWrap: boolPtr(true)}
	cp := NewClaudePreferences(cfg, false, "")
	rendered := cp.Render()
	assert.Contains(t, rendered, "Headroom Wrap")
	assert.Contains(t, rendered, "[x]")
}

func TestClaudePreferencesShowsBlockedHint(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, true, "not logged in — run `claude auth login`.")
	rendered := cp.Render()
	assert.Contains(t, rendered, "not logged in")
}

func TestClaudePreferencesEscCloses(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	closed, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, changed)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./ui/overlay/... -run TestClaudePreferences -v`
Expected: FAIL — compile errors (`cfg.HeadroomWrapEnabled undefined` no, that exists from Task 1; actual failures: `claudePrefsRowCount` still 2 so navigation/exclusivity assertions fail, `Model`/`Headroom Wrap` rows don't render). `TestClaudePreferencesRowNavigationClamps` fails because pressing 'j' 4 times still lands on row 1 today.

- [ ] **Step 3: Implement the 4-row ClaudePreferences**

Replace the full contents of `ui/overlay/claudePreferences.go`:

```go
package overlay

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ClaudePreferences is the Claude-specific preferences drill-in
// sub-screen. Structured as its own screen (rather than flat rows on
// the main settings list) so more Claude-adapter-specific preferences
// can be added later without growing that list — today it holds four
// rows: Remote Control, Permission Mode, Model, and Headroom Wrap.
//
// authBlocked/authReason mirror session.RemoteControlAuth.Blocked()/
// Reason, passed as plain values so this package stays decoupled from
// session (matching SettingsOverlay and every other overlay). They are
// a snapshot taken once at startup by the caller (m.rcAuth) — toggling
// Remote Control here does not re-probe auth; the existing
// session-creation-time gating (app/remote_control.go) already handles
// the incompatible-auth case once the toggle takes effect.
type ClaudePreferences struct {
	cfg         *config.Config
	authBlocked bool
	authReason  string
	width       int
	cursor      int
}

// claudePrefsRowCount is the number of navigable rows: Remote Control,
// Permission Mode, Model, and Headroom Wrap.
const claudePrefsRowCount = 4

// NewClaudePreferences creates the Claude Preferences sub-screen over cfg.
func NewClaudePreferences(cfg *config.Config, authBlocked bool, authReason string) *ClaudePreferences {
	return &ClaudePreferences{cfg: cfg, authBlocked: authBlocked, authReason: authReason, width: 60}
}

// SetWidth sets the render width.
func (c *ClaudePreferences) SetWidth(w int) { c.width = w }

// HandleKeyPress processes one key press. closed reports whether the
// sub-screen should return control to the parent SettingsOverlay;
// changed reports whether cfg was mutated.
func (c *ClaudePreferences) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	switch msg.String() {
	case "esc", "q":
		return true, false
	case "up", "k":
		if c.cursor > 0 {
			c.cursor--
		}
		return false, false
	case "down", "j":
		if c.cursor < claudePrefsRowCount-1 {
			c.cursor++
		}
		return false, false
	case " ", "space", "enter":
		switch c.cursor {
		case 0:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.RemoteControlEnabled()
				cc.ClaudeRemoteControl = &v
				if v {
					hw := false
					cc.HeadroomWrap = &hw
				}
			})
		case 1:
			c.cfg.Mutate(func(cc *config.Config) {
				next := nextInList(config.ClaudePermissionModes, cc.PermissionMode())
				cc.ClaudePermissionMode = &next
			})
		case 2:
			c.cfg.Mutate(func(cc *config.Config) {
				next := nextInList(config.ClaudeModels, cc.Model())
				cc.ClaudeModel = &next
			})
		case 3:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.HeadroomWrapEnabled()
				cc.HeadroomWrap = &v
				if v {
					rc := false
					cc.ClaudeRemoteControl = &rc
				}
			})
		}
		return false, true
	}
	return false, false
}

// nextInList returns the value in list after current, wrapping from the
// last value back to the first. Falls back to list[0] if current isn't
// found (e.g. a value predating this list's current contents).
func nextInList(list []string, current string) string {
	for i, v := range list {
		if v == current {
			return list[(i+1)%len(list)]
		}
	}
	return list[0]
}

var (
	claudePrefsTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	claudePrefsRowStyle      = lipgloss.NewStyle().Foreground(ui.TextPrimary)
	claudePrefsSelectedStyle = lipgloss.NewStyle().Foreground(ui.TitleAccent).Bold(true)
	claudePrefsHintStyle     = lipgloss.NewStyle().Foreground(ui.TextHint)
	claudePrefsBlockedText   = lipgloss.NewStyle().Foreground(ui.DangerAccent)
)

// Render renders the sub-screen.
func (c *ClaudePreferences) Render() string {
	check := "[ ]"
	if c.cfg.RemoteControlEnabled() {
		check = "[x]"
	}
	rcCursor := "  "
	if c.cursor == 0 {
		rcCursor = "> "
	}
	rcRow := rcCursor + "Remote Control    " + check
	if c.authBlocked {
		rcRow += "  " + claudePrefsBlockedText.Render("(blocked: "+c.authReason+")")
	}
	if c.cursor == 0 {
		rcRow = claudePrefsSelectedStyle.Render(rcRow)
	} else {
		rcRow = claudePrefsRowStyle.Render(rcRow)
	}

	pmCursor := "  "
	if c.cursor == 1 {
		pmCursor = "> "
	}
	pmRow := pmCursor + "Permission Mode   < " + c.cfg.PermissionMode() + " >"
	if c.cursor == 1 {
		pmRow = claudePrefsSelectedStyle.Render(pmRow)
	} else {
		pmRow = claudePrefsRowStyle.Render(pmRow)
	}

	modelCursor := "  "
	if c.cursor == 2 {
		modelCursor = "> "
	}
	modelRow := modelCursor + "Model             < " + c.cfg.Model() + " >"
	if c.cursor == 2 {
		modelRow = claudePrefsSelectedStyle.Render(modelRow)
	} else {
		modelRow = claudePrefsRowStyle.Render(modelRow)
	}

	hwCheck := "[ ]"
	if c.cfg.HeadroomWrapEnabled() {
		hwCheck = "[x]"
	}
	hwCursor := "  "
	if c.cursor == 3 {
		hwCursor = "> "
	}
	hwRow := hwCursor + "Headroom Wrap     " + hwCheck
	if c.cursor == 3 {
		hwRow = claudePrefsSelectedStyle.Render(hwRow)
	} else {
		hwRow = claudePrefsRowStyle.Render(hwRow)
	}

	content := claudePrefsTitleStyle.Render("Claude Preferences") + "\n\n" +
		rcRow + "\n" +
		pmRow + "\n" +
		modelRow + "\n" +
		hwRow + "\n\n" +
		claudePrefsHintStyle.Render("up/down move • enter/space toggle/cycle • esc back")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(c.width)
	return border.Render(content)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./ui/overlay/... -run TestClaudePreferences -v`
Expected: PASS

- [ ] **Step 5: Format and commit**

```bash
gofmt -w ui/overlay/claudePreferences.go ui/overlay/claudePreferences_test.go
git add ui/overlay/claudePreferences.go ui/overlay/claudePreferences_test.go
git commit -m "feat(ui): add Model and Headroom Wrap rows to Claude Preferences"
```

---

### Task 5: New `SessionLaunchOptions` overlay + `LaunchOptions` type

**Files:**
- Create: `ui/overlay/sessionLaunchOptions.go`
- Create: `ui/overlay/sessionLaunchOptions_test.go`

- [ ] **Step 1: Write the failing tests**

Create `ui/overlay/sessionLaunchOptions_test.go`:

```go
package overlay

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestSessionLaunchOptionsTogglesRemoteControl(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default"}, false, "")

	_, confirmed := lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.False(t, confirmed)
	assert.False(t, lo.Options().RemoteControl)
}

func TestSessionLaunchOptionsCyclesPermissionModeAndModel(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{PermissionMode: "default", Model: "default"}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}) // row 1: Permission Mode
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "acceptEdits", lo.Options().PermissionMode)

	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}) // row 2: Model
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "sonnet", lo.Options().Model)
}

func TestSessionLaunchOptionsHeadroomWrapExcludesRemoteControl(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true}, false, "")

	for i := 0; i < 3; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // toggle Headroom Wrap on

	assert.True(t, lo.Options().HeadroomWrap)
	assert.False(t, lo.Options().RemoteControl, "enabling Headroom Wrap must disable Remote Control")
}

func TestSessionLaunchOptionsRemoteControlExcludesHeadroomWrap(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{HeadroomWrap: true}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // row 0: toggle Remote Control on

	assert.True(t, lo.Options().RemoteControl)
	assert.False(t, lo.Options().HeadroomWrap, "enabling Remote Control must disable Headroom Wrap")
}

func TestSessionLaunchOptionsRowNavigationClamps(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"}) // up from row 0 stays at row 0
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.False(t, lo.Options().RemoteControl)

	for i := 0; i < 5; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}) // clamps at row 3
	}
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, lo.Options().HeadroomWrap)
}

func TestSessionLaunchOptionsEnterConfirms(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{}, false, "")
	closed, confirmed := lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, closed)
	assert.True(t, confirmed)
}

func TestSessionLaunchOptionsEscCancels(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{}, false, "")
	closed, confirmed := lo.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, confirmed)
}

func TestSessionLaunchOptionsShowsBlockedHint(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{}, true, "not logged in")
	rendered := lo.Render()
	assert.Contains(t, rendered, "not logged in")
}

func TestSessionLaunchOptionsRendersAllFourRows(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true, PermissionMode: "plan", Model: "opus", HeadroomWrap: false}, false, "")
	rendered := lo.Render()
	assert.Contains(t, rendered, "Remote Control")
	assert.Contains(t, rendered, "Permission Mode")
	assert.Contains(t, rendered, "plan")
	assert.Contains(t, rendered, "Model")
	assert.Contains(t, rendered, "opus")
	assert.Contains(t, rendered, "Headroom Wrap")
}
```

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `go test ./ui/overlay/... -run TestSessionLaunchOptions -v`
Expected: FAIL — `NewSessionLaunchOptions undefined`, `LaunchOptions undefined`.

- [ ] **Step 3: Implement `SessionLaunchOptions`**

Create `ui/overlay/sessionLaunchOptions.go`:

```go
package overlay

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// LaunchOptions holds the four per-session launch toggles. Defined here
// (rather than in app) so it's usable both by SessionLaunchOptions
// (ephemeral, edited as a plain value) and by app's launch-command
// composition, without an import cycle back to app.
type LaunchOptions struct {
	RemoteControl  bool
	PermissionMode string
	Model          string
	HeadroomWrap   bool
}

// SessionLaunchOptions is the per-instance "Session Launch Options"
// modal shown right before a new session starts. Unlike
// ClaudePreferences, which edits *config.Config directly and persists
// on every change, this edits a local LaunchOptions value that the
// caller applies to just one instance — closing without saving
// anything to disk.
type SessionLaunchOptions struct {
	opts        LaunchOptions
	authBlocked bool
	authReason  string
	width       int
	cursor      int
}

// sessionLaunchOptionsRowCount is the number of navigable rows: Remote
// Control, Permission Mode, Model, and Headroom Wrap.
const sessionLaunchOptionsRowCount = 4

// NewSessionLaunchOptions creates the modal seeded with initial
// (typically the global config's current values).
func NewSessionLaunchOptions(initial LaunchOptions, authBlocked bool, authReason string) *SessionLaunchOptions {
	return &SessionLaunchOptions{opts: initial, authBlocked: authBlocked, authReason: authReason, width: 60}
}

// SetWidth sets the render width.
func (l *SessionLaunchOptions) SetWidth(w int) { l.width = w }

// Options returns the current (possibly edited) launch options.
func (l *SessionLaunchOptions) Options() LaunchOptions { return l.opts }

// HandleKeyPress processes one key press. closed reports whether the
// modal should close (either canceled or confirmed); confirmed
// distinguishes the two — the caller only applies Options() and starts
// the instance when confirmed is true.
func (l *SessionLaunchOptions) HandleKeyPress(msg tea.KeyPressMsg) (closed, confirmed bool) {
	switch msg.String() {
	case "esc", "q":
		return true, false
	case "enter":
		return true, true
	case "up", "k":
		if l.cursor > 0 {
			l.cursor--
		}
		return false, false
	case "down", "j":
		if l.cursor < sessionLaunchOptionsRowCount-1 {
			l.cursor++
		}
		return false, false
	case " ", "space":
		l.toggleCursor()
		return false, false
	}
	return false, false
}

// toggleCursor applies the toggle/cycle action for the focused row,
// enforcing the same Remote-Control/Headroom-Wrap exclusivity rule as
// ClaudePreferences.
func (l *SessionLaunchOptions) toggleCursor() {
	switch l.cursor {
	case 0:
		l.opts.RemoteControl = !l.opts.RemoteControl
		if l.opts.RemoteControl {
			l.opts.HeadroomWrap = false
		}
	case 1:
		l.opts.PermissionMode = nextInList(config.ClaudePermissionModes, l.opts.PermissionMode)
	case 2:
		l.opts.Model = nextInList(config.ClaudeModels, l.opts.Model)
	case 3:
		l.opts.HeadroomWrap = !l.opts.HeadroomWrap
		if l.opts.HeadroomWrap {
			l.opts.RemoteControl = false
		}
	}
}

var (
	sessionLaunchOptionsTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	sessionLaunchOptionsRowStyle      = lipgloss.NewStyle().Foreground(ui.TextPrimary)
	sessionLaunchOptionsSelectedStyle = lipgloss.NewStyle().Foreground(ui.TitleAccent).Bold(true)
	sessionLaunchOptionsHintStyle     = lipgloss.NewStyle().Foreground(ui.TextHint)
	sessionLaunchOptionsBlockedText   = lipgloss.NewStyle().Foreground(ui.DangerAccent)
)

// Render renders the modal.
func (l *SessionLaunchOptions) Render() string {
	row := func(idx int, label, value string) string {
		cursor := "  "
		if l.cursor == idx {
			cursor = "> "
		}
		line := cursor + label + value
		if idx == 0 && l.authBlocked {
			line += "  " + sessionLaunchOptionsBlockedText.Render("(blocked: "+l.authReason+")")
		}
		if l.cursor == idx {
			return sessionLaunchOptionsSelectedStyle.Render(line)
		}
		return sessionLaunchOptionsRowStyle.Render(line)
	}

	rcCheck := "[ ]"
	if l.opts.RemoteControl {
		rcCheck = "[x]"
	}
	hwCheck := "[ ]"
	if l.opts.HeadroomWrap {
		hwCheck = "[x]"
	}

	content := sessionLaunchOptionsTitleStyle.Render("Session Launch Options") + "\n\n" +
		row(0, "Remote Control    ", rcCheck) + "\n" +
		row(1, "Permission Mode   ", "< "+l.opts.PermissionMode+" >") + "\n" +
		row(2, "Model             ", "< "+l.opts.Model+" >") + "\n" +
		row(3, "Headroom Wrap     ", hwCheck) + "\n\n" +
		sessionLaunchOptionsHintStyle.Render("up/down move • space toggle/cycle • enter start • esc cancel")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(l.width)
	return border.Render(content)
}

// HandleKey satisfies the Overlay interface. State handlers that need
// the confirmed signal call HandleKeyPress directly instead (mirrors
// SettingsOverlay.HandleKey/HandleKeyPress).
func (l *SessionLaunchOptions) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := l.HandleKeyPress(msg)
	return closed, nil
}

// SetSize satisfies the Overlay interface.
func (l *SessionLaunchOptions) SetSize(width, _ int) {
	l.width = width
}

// View satisfies the Overlay interface.
func (l *SessionLaunchOptions) View() string {
	return l.Render()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./ui/overlay/... -run TestSessionLaunchOptions -v`
Expected: PASS

- [ ] **Step 5: Format and commit**

```bash
gofmt -w ui/overlay/sessionLaunchOptions.go ui/overlay/sessionLaunchOptions_test.go
git add ui/overlay/sessionLaunchOptions.go ui/overlay/sessionLaunchOptions_test.go
git commit -m "feat(ui): add SessionLaunchOptions per-instance modal"
```

---

### Task 6: `app/remote_control.go` — composition refactor and exclusivity

This is a signature refactor: `remoteControlProgram`/`permissionModeProgram` change from taking `*config.Config` to taking resolved primitives, `remoteControlBlocked` changes similarly, and two new functions (`modelProgram`, `headroomWrapProgram`) plus `launchOptionsFromConfig`/`applyLaunchOptions` are added. Because it's a signature break, the test file is rewritten alongside the production file rather than red-green TDD'd from scratch.

**Files:**
- Modify: `app/remote_control.go`
- Modify: `app/remote_control_test.go`
- Modify: `app/app.go:385`, `app/app.go:393`, `app/app.go:1690`, `app/app.go:1696` (the two workspace-terminal auto-create call sites)

- [ ] **Step 1: Replace `app/remote_control.go`**

Replace the full contents of `app/remote_control.go`:

```go
package app

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
)

// remoteControlProgram returns program with Claude's --remote-control flag
// (named after title) applied when enabled AND the detected auth can use
// it. It is a no-op when enabled is false, the auth is not confirmed OK
// (fail closed on Blocked/Unknown), or the program isn't Claude.
//
// Callers apply it to an instance's Program at first launch — once the
// title is known — so the rewritten command is persisted and later
// resume/crash restarts inherit the flag through BuildRecoveryCommand.
func remoteControlProgram(enabled bool, auth session.RemoteControlAuth, program, title string) string {
	if !enabled || !auth.OK() {
		return program
	}
	return session.BuildRemoteControlCommand(program, title)
}

// permissionModeProgram returns program with Claude's --permission-mode
// flag applied. No-op when the program isn't Claude
// (BuildPermissionModeCommand's registry lookup already no-ops for
// non-Claude adapters) or mode is "" / "default".
func permissionModeProgram(mode, program string) string {
	return session.BuildPermissionModeCommand(program, mode)
}

// modelProgram returns program with Claude's --model flag applied.
// No-op when the program isn't Claude or model is "" / "default".
func modelProgram(model, program string) string {
	return session.BuildModelCommand(program, model)
}

// headroomWrapProgram returns program wrapped as "headroom wrap
// <program>" when enabled. Agent-agnostic: applies regardless of which
// program is configured.
func headroomWrapProgram(enabled bool, program string) string {
	if !enabled {
		return program
	}
	return session.BuildHeadroomWrapCommand(program)
}

// launchOptionsFromConfig snapshots cfg's current global launch-option
// values into an overlay.LaunchOptions, the same shape edited by the
// Session Launch Options modal. Returns the zero value (all
// disabled/default) for a nil cfg — matching the effect the old
// cfg-nil guards in remoteControlProgram/permissionModeProgram had
// before this refactor.
func launchOptionsFromConfig(cfg *config.Config) overlay.LaunchOptions {
	if cfg == nil {
		return overlay.LaunchOptions{}
	}
	return overlay.LaunchOptions{
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
// forcibly disables RemoteControl here regardless of opts.RemoteControl:
// this is the authoritative enforcement of the exclusivity rule (the
// UI-level auto-disable in ClaudePreferences/SessionLaunchOptions is
// the good-UX layer on top, not the only guarantee — a hand-edited
// config.json with both fields true still can't launch both flags
// together).
func applyLaunchOptions(opts overlay.LaunchOptions, auth session.RemoteControlAuth, program, title string) string {
	if opts.HeadroomWrap {
		opts.RemoteControl = false
	}
	program = remoteControlProgram(opts.RemoteControl, auth, program, title)
	program = permissionModeProgram(opts.PermissionMode, program)
	program = modelProgram(opts.Model, program)
	program = headroomWrapProgram(opts.HeadroomWrap, program)
	return program
}

// remoteControlBlocked reports whether a launch of program should be
// interrupted to tell the user remote control can't work: the toggle is
// on (rcEnabled — either the global config or a per-instance override),
// the program is Claude, and auth was clearly determined incompatible.
func (m *home) remoteControlBlocked(rcEnabled bool, program string) bool {
	return rcEnabled && session.IsClaudeProgram(program) && m.rcAuth.Blocked()
}

// promptRemoteControlBlocked shows the "remote control unavailable" modal for
// a titled-but-unstarted instance. Confirm (y) runs startWithoutRC — which
// launches the session with no --remote-control flag; cancel (n/esc) aborts
// creation, popping and killing the pending instance the way Esc does. Both
// branches route their Cmd through pendingConfirmation so state_confirm.go
// dispatches it.
func (m *home) promptRemoteControlBlocked(startWithoutRC overlay.ConfirmationTask) tea.Cmd {
	m.state = stateConfirm
	m.pendingConfirmation = startWithoutRC

	msg := "Remote control unavailable: " + m.rcAuth.Reason +
		"\n\nStart this session without remote control?"
	co := overlay.NewConfirmationOverlay(msg)
	co.SetWidth(60)
	co.OnCancel = func() {
		// Swap in an abort task so cancel tears the pending instance down
		// (async, like the Esc path) instead of starting it.
		popped := m.list.PopSelectedForKill()
		m.menu.SetState(ui.StateDefault)
		m.pendingConfirmation = overlay.ConfirmationTask{Async: backgroundKillCmd(popped)}
	}
	m.setOverlay(co, overlayConfirmation)
	return nil
}
```

- [ ] **Step 2: Update the two workspace-terminal call sites in `app/app.go`**

Around line 385-396, replace:

```go
			if h.remoteControlBlocked(program) {
				// Non-interactive startup: fall back silently but leave an
				// info-style note (clears on the next status update).
				h.errBox.SetInfo("remote control off: " + h.rcAuth.Reason)
			}
			wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
				Title:               wtTitle,
				Path:                wsCtx.RepoPath,
				Program:             permissionModeProgram(appConfig, remoteControlProgram(appConfig, h.rcAuth, program, wtTitle)),
				IsWorkspaceTerminal: true,
				ConfigDir:           cfgDir,
			})
```

with:

```go
			if h.remoteControlBlocked(appConfig.RemoteControlEnabled(), program) {
				// Non-interactive startup: fall back silently but leave an
				// info-style note (clears on the next status update).
				h.errBox.SetInfo("remote control off: " + h.rcAuth.Reason)
			}
			wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
				Title:               wtTitle,
				Path:                wsCtx.RepoPath,
				Program:             applyLaunchOptions(launchOptionsFromConfig(appConfig), h.rcAuth, program, wtTitle),
				IsWorkspaceTerminal: true,
				ConfigDir:           cfgDir,
			})
```

Around line 1690-1698, replace:

```go
		if m.remoteControlBlocked(appConfig.GetProgram()) {
			m.errBox.SetInfo("remote control off: " + m.rcAuth.Reason)
		}
		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             permissionModeProgram(appConfig, remoteControlProgram(appConfig, m.rcAuth, appConfig.GetProgram(), wtTitle)),
			IsWorkspaceTerminal: true,
			ConfigDir:           wsCtx.ConfigDir,
		})
```

with:

```go
		if m.remoteControlBlocked(appConfig.RemoteControlEnabled(), appConfig.GetProgram()) {
			m.errBox.SetInfo("remote control off: " + m.rcAuth.Reason)
		}
		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             applyLaunchOptions(launchOptionsFromConfig(appConfig), m.rcAuth, appConfig.GetProgram(), wtTitle),
			IsWorkspaceTerminal: true,
			ConfigDir:           wsCtx.ConfigDir,
		})
```

- [ ] **Step 3: Replace `app/remote_control_test.go`**

Replace the full contents of `app/remote_control_test.go`:

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
)

func boolPtrTest(b bool) *bool { return &b }

func stringPtrTest(s string) *string { return &s }

func TestRemoteControlProgram(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	authBlocked := session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	authUnknown := session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}

	t.Run("enabled + auth OK rewrites claude", func(t *testing.T) {
		assert.Equal(t, "claude --remote-control fix-bug", remoteControlProgram(true, authOK, "claude", "fix bug"))
	})

	t.Run("auth Blocked leaves program untouched (fail closed)", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(true, authBlocked, "claude", "task"))
	})

	t.Run("auth Unknown leaves program untouched (fail closed)", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(true, authUnknown, "claude", "task"))
	})

	t.Run("disabled leaves program untouched even when auth OK", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(false, authOK, "claude", "task"))
	})

	t.Run("non-claude program is a no-op even when enabled + auth OK", func(t *testing.T) {
		assert.Equal(t, "aider --model x", remoteControlProgram(true, authOK, "aider --model x", "task"))
	})
}

func TestRemoteControlBlocked(t *testing.T) {
	blocked := session.RemoteControlAuth{State: session.RemoteControlAuthBlocked}
	ok := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	unknown := session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}

	cases := []struct {
		name      string
		rcEnabled bool
		auth      session.RemoteControlAuth
		program   string
		want      bool
	}{
		{"enabled + claude + blocked", true, blocked, "claude", true},
		{"enabled + claude + ok", true, ok, "claude", false},
		{"enabled + claude + unknown", true, unknown, "claude", false},
		{"enabled + non-claude + blocked", true, blocked, "aider", false},
		{"disabled + claude + blocked", false, blocked, "claude", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &home{rcAuth: tc.auth}
			assert.Equal(t, tc.want, m.remoteControlBlocked(tc.rcEnabled, tc.program))
		})
	}
}

func TestPermissionModeProgram(t *testing.T) {
	t.Run("default mode is a no-op", func(t *testing.T) {
		assert.Equal(t, "claude --model sonnet", permissionModeProgram("default", "claude --model sonnet"))
	})

	t.Run("explicit mode is injected", func(t *testing.T) {
		assert.Equal(t, "claude --permission-mode acceptEdits --model sonnet", permissionModeProgram("acceptEdits", "claude --model sonnet"))
	})

	t.Run("non-claude program is a no-op", func(t *testing.T) {
		assert.Equal(t, "aider --model gemma", permissionModeProgram("acceptEdits", "aider --model gemma"))
	})
}

func TestModelProgram(t *testing.T) {
	t.Run("default model is a no-op", func(t *testing.T) {
		assert.Equal(t, "claude --permission-mode plan", modelProgram("default", "claude --permission-mode plan"))
	})

	t.Run("explicit model is injected", func(t *testing.T) {
		assert.Equal(t, "claude --model sonnet --permission-mode plan", modelProgram("sonnet", "claude --permission-mode plan"))
	})

	t.Run("non-claude program is a no-op", func(t *testing.T) {
		assert.Equal(t, "aider --model gemma", modelProgram("sonnet", "aider --model gemma"))
	})
}

func TestHeadroomWrapProgram(t *testing.T) {
	t.Run("disabled is a no-op", func(t *testing.T) {
		assert.Equal(t, "claude --model sonnet", headroomWrapProgram(false, "claude --model sonnet"))
	})

	t.Run("enabled wraps the whole command", func(t *testing.T) {
		assert.Equal(t, "headroom wrap claude --model sonnet", headroomWrapProgram(true, "claude --model sonnet"))
	})

	t.Run("agent-agnostic: wraps non-claude programs too", func(t *testing.T) {
		assert.Equal(t, "headroom wrap aider --model gemma", headroomWrapProgram(true, "aider --model gemma"))
	})
}

func TestLaunchOptionsFromConfig(t *testing.T) {
	t.Run("nil cfg returns zero value", func(t *testing.T) {
		assert.Equal(t, overlay.LaunchOptions{}, launchOptionsFromConfig(nil))
	})

	t.Run("populated cfg maps every field", func(t *testing.T) {
		got := launchOptionsFromConfig(config.DefaultConfig())
		assert.Equal(t, overlay.LaunchOptions{
			RemoteControl:  true,
			PermissionMode: "default",
			Model:          "default",
			HeadroomWrap:   false,
		}, got)
	})

	t.Run("threads through explicit overrides", func(t *testing.T) {
		cfg := &config.Config{
			ClaudeRemoteControl:  boolPtrTest(false),
			ClaudePermissionMode: stringPtrTest("plan"),
			ClaudeModel:          stringPtrTest("opus"),
			HeadroomWrap:         boolPtrTest(true),
		}
		assert.Equal(t, overlay.LaunchOptions{
			RemoteControl:  false,
			PermissionMode: "plan",
			Model:          "opus",
			HeadroomWrap:   true,
		}, launchOptionsFromConfig(cfg))
	})
}

func TestApplyLaunchOptions(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}

	t.Run("stacks remote-control, permission-mode, and model", func(t *testing.T) {
		opts := overlay.LaunchOptions{RemoteControl: true, PermissionMode: "acceptEdits", Model: "opus", HeadroomWrap: false}
		got := applyLaunchOptions(opts, authOK, "claude", "my task")
		assert.Equal(t, "claude --model opus --permission-mode acceptEdits --remote-control my-task", got)
	})

	t.Run("headroom wrap is applied last, outermost", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "acceptEdits", Model: "opus", HeadroomWrap: true}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "headroom wrap claude --model opus --permission-mode acceptEdits", got)
	})

	t.Run("headroom wrap forcibly disables remote control even if both are true", func(t *testing.T) {
		opts := overlay.LaunchOptions{RemoteControl: true, HeadroomWrap: true}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "headroom wrap claude", got)
	})

	t.Run("all defaults/disabled is a no-op", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "default", Model: "default"}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "claude", got)
	})
}
```

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./app/... -run 'TestRemoteControl|TestPermissionModeProgram|TestModelProgram|TestHeadroomWrapProgram|TestLaunchOptionsFromConfig|TestApplyLaunchOptions' -v`
Expected: PASS. `go build ./...` must succeed (confirms the two `app/app.go` call sites compile against the new signatures).

- [ ] **Step 5: Format and commit**

```bash
gofmt -w app/remote_control.go app/remote_control_test.go app/app.go
git add app/remote_control.go app/remote_control_test.go app/app.go
git commit -m "refactor(app): compose launch options via overlay.LaunchOptions"
```

---

### Task 7: `stateLaunchOptions` state, `overlayLaunchOptions` kind, and the state handler

**Files:**
- Modify: `app/overlay_host.go`
- Modify: `app/app.go`
- Create: `app/state_launch_options.go`

- [ ] **Step 1: Add the overlay kind and accessor**

In `app/overlay_host.go`, add `overlayLaunchOptions` to the const block:

```go
const (
	overlayNone overlayKind = iota
	overlayTextInput
	overlayText
	overlayConfirmation
	overlayWorkspacePicker
	overlayWorkspacePickerStartup
	overlayFileExplorer
	overlaySettings
	overlayMergePicker
	overlayLaunchOptions
)
```

Add the accessor at the end of the file, after `mergePicker()`:

```go

// launchOptionsOverlay returns the active SessionLaunchOptions, or nil
// when a different overlay is active.
func (m *home) launchOptionsOverlay() *overlay.SessionLaunchOptions {
	if o, ok := m.activeOverlay.(*overlay.SessionLaunchOptions); ok {
		return o
	}
	return nil
}
```

- [ ] **Step 2: Add the `stateLaunchOptions` state and `pendingLaunchOptions` field**

In `app/app.go`, add to the `state` const block (currently lines 92-119), right before the closing `)`:

```go
	// stateMergePicker is the state when the merge-session picker
	// overlay is displayed (opened by the 'm' key).
	stateMergePicker
	// stateLaunchOptions is the state when the Session Launch Options
	// modal is displayed, between title/prompt entry and actually
	// starting a new instance.
	stateLaunchOptions
)
```

Add a field to the `home` struct, right after `promptAfterName bool` (currently lines 157-158):

```go
	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool

	// pendingLaunchOptions holds the compose-and-start closure for a
	// not-yet-started instance while stateLaunchOptions is active.
	// state_new.go/state_prompt.go stash it (capturing the instance and
	// any prompt-flow-specific data like selectedBranch) right before
	// opening the Session Launch Options modal; handleStateLaunchOptionsKey
	// invokes it with the user's chosen overlay.LaunchOptions on confirm,
	// then clears it. nil outside that window.
	pendingLaunchOptions func(overlay.LaunchOptions) (tea.Model, tea.Cmd)
```

- [ ] **Step 3: Wire dispatch, menu suppression, and render**

In `app/app.go`'s `handleKeyPress` switch (currently lines 1256-1279), add a case:

```go
	case stateMergePicker:
		return handleStateMergePickerKey(m, msg)
	case stateLaunchOptions:
		return handleStateLaunchOptionsKey(m, msg)
	default:
```

In `handleMenuHighlighting` (currently line 1227), add `stateLaunchOptions` to the suppression list:

```go
	if m.state == statePrompt || m.state == stateHelp || m.state == stateConfirm || m.state == stateWorkspace || m.state == stateQuickInteract || m.state == stateInlineAttach || m.state == stateFileExplorer || m.state == stateMergePicker || m.state == stateLaunchOptions {
```

In the render dispatch (currently line 2104), add `stateLaunchOptions` to the overlay-placement switch:

```go
		switch m.state {
		case statePrompt, stateHelp, stateConfirm, stateWorkspace, stateSettings, stateMergePicker, stateLaunchOptions:
			return asView(overlay.PlaceOverlay(0, 0, m.activeOverlay.View(), mainView, true, true))
		}
```

- [ ] **Step 4: Implement the state handler**

Create `app/state_launch_options.go`:

```go
package app

import (
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// handleStateLaunchOptionsKey runs while the Session Launch Options
// modal is active — shown after title (and, for the N flow, prompt)
// entry, right before a new instance actually starts. Confirming
// (enter) hands the chosen overlay.LaunchOptions to whichever closure
// state_new.go/state_prompt.go stashed in m.pendingLaunchOptions before
// opening this modal; canceling (esc/ctrl+c) pops and kills the
// pending, not-yet-started instance, mirroring handleStateNewKey's own
// cancel path.
func handleStateLaunchOptionsKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m.cancelLaunchOptions()
	}

	lo := m.launchOptionsOverlay()
	if lo == nil {
		return m, nil
	}

	closed, confirmed := lo.HandleKeyPress(msg)
	if !closed {
		return m, nil
	}

	if !confirmed {
		return m.cancelLaunchOptions()
	}

	opts := lo.Options()
	pending := m.pendingLaunchOptions
	m.pendingLaunchOptions = nil
	m.dismissOverlay()
	if pending == nil {
		m.state = stateDefault
		return m, nil
	}
	return pending(opts)
}

// cancelLaunchOptions pops and kills the pending, not-yet-started
// instance and returns to stateDefault — the same shape as
// handleStateNewKey's Esc/ctrl+c handling.
func (m *home) cancelLaunchOptions() (tea.Model, tea.Cmd) {
	popped := m.list.PopSelectedForKill()
	m.pendingLaunchOptions = nil
	m.dismissOverlay()
	m.state = stateDefault
	m.instanceChanged()
	return m, tea.Batch(
		tea.Sequence(
			tea.RequestWindowSize,
			func() tea.Msg {
				m.menu.SetState(ui.StateDefault)
				return nil
			},
		),
		backgroundKillCmd(popped),
	)
}
```

- [ ] **Step 5: Build to confirm the wiring compiles**

Run: `go build ./...`
Expected: SUCCESS. There's no behavior to unit-test yet in isolation — `handleStateLaunchOptionsKey` is only reachable once state_new.go/state_prompt.go set `m.pendingLaunchOptions` and `m.state = stateLaunchOptions` in Task 8. This task's tests come with Task 8, which exercises this handler directly.

- [ ] **Step 6: Format and commit**

```bash
gofmt -w app/overlay_host.go app/app.go app/state_launch_options.go
git add app/overlay_host.go app/app.go app/state_launch_options.go
git commit -m "feat(app): add stateLaunchOptions and the Session Launch Options handler"
```

---

### Task 8: Wire `state_new.go` and `state_prompt.go` into the modal, and test the whole flow

**Files:**
- Modify: `app/state_new.go`
- Modify: `app/state_prompt.go`
- Create: `app/state_launch_options_test.go`
- Create: `app/state_new_test.go`
- Create: `app/state_prompt_test.go`

- [ ] **Step 1: Wire `state_new.go`**

In `app/state_new.go`, replace the `case tea.KeyEnter:` block's finalize branch (currently lines 53-79, everything after the `promptAfterName` `if` block):

```go
		// Show the Session Launch Options modal, seeded from the global
		// config, before actually starting. Confirming there runs the
		// closure stashed below (compose Program with the chosen
		// overrides, then Start) via handleStateLaunchOptionsKey.
		m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
			startTask := overlay.ConfirmationTask{
				Sync: func() {
					instance.Program = applyLaunchOptions(opts, m.rcAuth, instance.Program, instance.Title)
					_ = instance.TransitionTo(session.Loading)
					m.newInstanceFinalizer()
					m.promptAfterName = false
					m.state = stateDefault
					m.menu.SetState(ui.StateDefault)
				},
				Async: tea.Batch(tea.RequestWindowSize, func() tea.Msg {
					err := instance.Start(true)
					return instanceStartedMsg{
						instance:        instance,
						err:             err,
						promptAfterName: false,
					}
				}),
			}

			if m.remoteControlBlocked(opts.RemoteControl, instance.Program) {
				return m, m.promptRemoteControlBlocked(startTask)
			}
			return m, tea.Batch(startTask.Run(), m.instanceChanged())
		}
		m.state = stateLaunchOptions
		m.setOverlay(overlay.NewSessionLaunchOptions(launchOptionsFromConfig(m.appConfig), m.rcAuth.Blocked(), m.rcAuth.Reason), overlayLaunchOptions)
		m.menu.SetState(ui.StateNewInstance)
		return m, tea.RequestWindowSize
```

(The `if m.promptAfterName { ... return ... }` block right above this, and the rest of the function below `case tea.KeyBackspace:` onward, are unchanged.)

- [ ] **Step 2: Wire `state_prompt.go`**

In `app/state_prompt.go`, replace the `if !selected.Started() { ... }` block (currently lines 44-81):

```go
			if !selected.Started() {
				// Shift+N flow: instance not started yet — set branch, then
				// show the Session Launch Options modal before starting.
				if selectedBranch != "" {
					selected.SetSelectedBranch(selectedBranch)
				}
				if selectedProgram != "" {
					selected.Program = selectedProgram
				}
				selected.Prompt = prompt

				m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
					startTask := overlay.ConfirmationTask{
						Sync: func() {
							selected.Program = applyLaunchOptions(opts, m.rcAuth, selected.Program, selected.Title)
							_ = selected.TransitionTo(session.Loading)
							m.newInstanceFinalizer()
							m.dismissOverlay()
							m.state = stateDefault
							m.menu.SetState(ui.StateDefault)
						},
						Async: tea.Batch(tea.RequestWindowSize, func() tea.Msg {
							err := selected.Start(true)
							return instanceStartedMsg{
								instance:        selected,
								err:             err,
								promptAfterName: false,
								selectedBranch:  selectedBranch,
							}
						}),
					}

					if m.remoteControlBlocked(opts.RemoteControl, selected.Program) {
						return m, m.promptRemoteControlBlocked(startTask)
					}
					return m, tea.Batch(startTask.Run(), m.instanceChanged())
				}
				m.state = stateLaunchOptions
				m.setOverlay(overlay.NewSessionLaunchOptions(launchOptionsFromConfig(m.appConfig), m.rcAuth.Blocked(), m.rcAuth.Reason), overlayLaunchOptions)
				m.menu.SetState(ui.StateNewInstance)
				return m, tea.RequestWindowSize
			}
```

- [ ] **Step 3: Run a build to check for compile errors**

Run: `go build ./...`
Expected: SUCCESS.

- [ ] **Step 4: Write the state_launch_options tests**

Create `app/state_launch_options_test.go`:

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPendingLaunchOptionsHome builds a *home with one not-yet-started
// instance in the list, the Session Launch Options modal open, and
// m.pendingLaunchOptions wired the same way state_new.go's finalize
// branch wires it — capturing instance.Program/Title so confirming
// composes and stashes the result on instance.Program without actually
// invoking Start() (which would need a real git worktree + tmux).
func newPendingLaunchOptionsHome(t *testing.T, initial overlay.LaunchOptions) (*home, *session.Instance) {
	t.Helper()
	m := newTestHome(t)
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:     "my task",
		Path:      t.TempDir(),
		Program:   "claude",
		ConfigDir: t.TempDir(),
	})
	require.NoError(t, err)
	m.newInstanceFinalizer = m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)

	m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		instance.Program = applyLaunchOptions(opts, m.rcAuth, instance.Program, instance.Title)
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, nil
	}
	m.state = stateLaunchOptions
	m.setOverlay(overlay.NewSessionLaunchOptions(initial, m.rcAuth.Blocked(), m.rcAuth.Reason), overlayLaunchOptions)
	m.menu.SetState(ui.StateNewInstance)

	return m, instance
}

func TestHandleStateLaunchOptionsKeyConfirmComposesAndClearsPending(t *testing.T) {
	m, instance := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{PermissionMode: "acceptEdits", Model: "default"})

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, "claude --permission-mode acceptEdits", instance.Program)
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.pendingLaunchOptions)
}

func TestHandleStateLaunchOptionsKeyTogglesBeforeConfirm(t *testing.T) {
	m, instance := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{PermissionMode: "default", Model: "default"})

	// Move to Model row (row 2), cycle it, then confirm.
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, "claude --model sonnet", instance.Program)
}

func TestHandleStateLaunchOptionsKeyEscCancelsAndKillsPendingInstance(t *testing.T) {
	m, _ := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{})
	before := m.list.NumInstances()

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEsc})

	assert.Equal(t, before-1, m.list.NumInstances())
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.pendingLaunchOptions)
}

func TestHandleStateLaunchOptionsKeyCtrlCCancels(t *testing.T) {
	m, _ := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{})
	before := m.list.NumInstances()

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	assert.Equal(t, before-1, m.list.NumInstances())
}
```

- [ ] **Step 5: Run the state_launch_options tests**

Run: `go test ./app/... -run TestHandleStateLaunchOptionsKey -v`
Expected: PASS

- [ ] **Step 6: Write the state_new integration test**

Create `app/state_new_test.go`:

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPendingTitleEntryHome mirrors what runNewInstance (app/intents.go)
// does when 'n' is pressed: append a blank, unstarted instance and
// enter stateNew — without needing the full repoPath()/configDir()
// plumbing runNewInstance itself depends on.
func newPendingTitleEntryHome(t *testing.T) *home {
	t.Helper()
	m := newTestHome(t)
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:     "",
		Path:      t.TempDir(),
		Program:   m.appConfig.DefaultProgram,
		ConfigDir: t.TempDir(),
	})
	require.NoError(t, err)
	m.newInstanceFinalizer = m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.state = stateNew
	m.menu.SetState(ui.StateNewInstance)
	return m
}

func TestHandleStateNewKeyEnterOpensLaunchOptionsInsteadOfStartingImmediately(t *testing.T) {
	m := newPendingTitleEntryHome(t)

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateLaunchOptions, m.state)
	require.NotNil(t, m.pendingLaunchOptions)
	assert.NotNil(t, m.launchOptionsOverlay())
}
```

- [ ] **Step 7: Run the state_new test**

Run: `go test ./app/... -run TestHandleStateNewKeyEnterOpensLaunchOptions -v`
Expected: PASS

- [ ] **Step 8: Write the state_prompt integration test**

Create `app/state_prompt_test.go`:

```go
package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleStatePromptKeySubmitOpensLaunchOptionsInsteadOfStartingImmediately(t *testing.T) {
	m := newPendingTitleEntryHome(t)
	m.promptAfterName = true

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // -> statePrompt, opens prompt overlay
	require.Equal(t, statePrompt, m.state)

	ti := m.textInput()
	require.NotNil(t, ti)
	for _, r := range "do the thing" {
		handleStatePromptKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	// Initial focus is the textarea (index 0); shift+tab wraps backward
	// to the last stop (the Enter button) regardless of how many stops
	// the branch/profile pickers add.
	handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateLaunchOptions, m.state)
	require.NotNil(t, m.pendingLaunchOptions)
	assert.NotNil(t, m.launchOptionsOverlay())
}
```

- [ ] **Step 9: Run the state_prompt test**

Run: `go test ./app/... -run TestHandleStatePromptKeySubmitOpensLaunchOptions -v`
Expected: PASS

- [ ] **Step 10: Run the full app package test suite**

Run: `go test ./app/... -v`
Expected: PASS — no regressions in any other `app` package test (settings, merge, recovery, script dispatch, etc.).

- [ ] **Step 11: Format and commit**

```bash
gofmt -w app/state_new.go app/state_prompt.go app/state_launch_options_test.go app/state_new_test.go app/state_prompt_test.go
git add app/state_new.go app/state_prompt.go app/state_launch_options_test.go app/state_new_test.go app/state_prompt_test.go
git commit -m "feat(app): show Session Launch Options before starting a new instance"
```

---

### Task 9: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Format the whole repo**

Run: `gofmt -l .`
Expected: empty output (no files need formatting). If any file is listed, run `gofmt -w .` and re-check.

- [ ] **Step 2: Build everything**

Run: `CGO_ENABLED=0 go build -o /tmp/loom-build-check ./...`
Expected: SUCCESS, no errors.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: PASS across every package — `config`, `session`, `session/agent`, `ui/overlay`, `app`, and everything else unaffected by this change.

- [ ] **Step 4: Run the race detector on the touched packages**

Run: `CC=clang CGO_ENABLED=1 go test -race ./app/... ./ui/overlay/... ./session/... ./config/...`
(Use `CC=gcc` if `clang` isn't available.)
Expected: PASS, no data races — this change introduces a new closure field (`home.pendingLaunchOptions`) read/written only on the main goroutine via key-press handlers, so no new races are expected, but this confirms it.

- [ ] **Step 5: Lint**

Run: `golangci-lint run --timeout=3m --fast`
Expected: no new findings introduced by this change.

- [ ] **Step 6: Manual smoke test**

Run `CGO_ENABLED=0 go build -o loom && ./loom` in a scratch git repo, then:
1. Press `S` to open Settings, drill into "Claude Preferences" — confirm four rows render (Remote Control, Permission Mode, Model, Headroom Wrap), `space`/`enter` cycles Model and toggles Headroom Wrap, and enabling Headroom Wrap visibly flips Remote Control's checkbox off (and vice versa).
2. Press `esc` back out, then `n` to create a new instance, type a title, press Enter — confirm the "Session Launch Options" modal appears (not immediate start), pre-filled from the config values just set in step 1.
3. In the modal, toggle a row with `space` and confirm with `enter` — confirm the instance starts and its Program (visible via `loom debug` or the instance's tmux session command) reflects the modal's choices, not the raw global config.
4. Press `esc` from the modal on a fresh `n` flow — confirm the pending instance is discarded (not left behind in the list).

- [ ] **Step 7: Report results**

No commit for this task — it's verification only. If any step fails, return to the relevant task, fix, and re-run this task's steps from the top.
