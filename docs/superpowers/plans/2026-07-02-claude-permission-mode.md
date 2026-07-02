# Claude Permission Mode Setting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a persistent `ClaudePermissionMode` setting, editable from the Claude Preferences settings sub-screen, that injects Claude Code's `--permission-mode <mode>` flag into every newly-created Claude session.

**Architecture:** Follows the existing `ClaudeRemoteControl` pattern end to end: a `*string` config field with a locked accessor (`config/config.go`), a new `Adapter` interface method implemented per-agent (`session/agent/*.go`), a package-level builder function (`session/agent_restart.go`), an application-site helper wired into all four instance-creation call sites (`app/remote_control.go`, `app/app.go`, `app/state_new.go`, `app/state_prompt.go`), and a second row on the existing Claude Preferences overlay (`ui/overlay/claudePreferences.go`).

**Tech Stack:** Go 1.23, Bubble Tea v2 (`charm.land/bubbletea/v2`), lipgloss v2, testify/assert.

**Spec:** `docs/superpowers/specs/2026-07-02-claude-permission-mode-design.md`

---

### Task 1: Config field, accessor, and shared mode list

**Files:**
- Modify: `config/config.go:96-102` (Config struct), `config/config.go:167-187` (DefaultConfig), `config/config.go:189-191` (boolPtr)
- Test: `config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `config/config_test.go`, after `TestRemoteControlEnabled` (ends at line 365):

```go
func TestPermissionMode(t *testing.T) {
	t.Run("nil (absent from config) defaults to \"default\"", func(t *testing.T) {
		cfg := &Config{}
		assert.Equal(t, "default", cfg.PermissionMode())
	})

	t.Run("explicit value round-trips", func(t *testing.T) {
		cfg := &Config{ClaudePermissionMode: stringPtr("acceptEdits")}
		assert.Equal(t, "acceptEdits", cfg.PermissionMode())
	})

	t.Run("explicit \"default\" round-trips", func(t *testing.T) {
		cfg := &Config{ClaudePermissionMode: stringPtr("default")}
		assert.Equal(t, "default", cfg.PermissionMode())
	})

	t.Run("DefaultConfig sets \"default\" explicitly", func(t *testing.T) {
		cfg := DefaultConfig()
		if assert.NotNil(t, cfg.ClaudePermissionMode) {
			assert.Equal(t, "default", *cfg.ClaudePermissionMode)
		}
		assert.Equal(t, "default", cfg.PermissionMode())
	})
}

func TestClaudePermissionModes(t *testing.T) {
	assert.Equal(t, []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"}, ClaudePermissionModes)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./config/... -run 'TestPermissionMode|TestClaudePermissionModes' -v`
Expected: FAIL — `cfg.PermissionMode` and `stringPtr` and `ClaudePermissionModes` undefined (compile error).

- [ ] **Step 3: Implement the config field, accessor, mode list, and helper**

In `config/config.go`, add the field to the `Config` struct right after `ClaudeRemoteControl` (line 101):

```go
	// ClaudeRemoteControl controls whether new Claude sessions launch
	// with `--remote-control` (named after the session title). It is a
	// pointer so a config file predating this field (nil) is treated as
	// enabled rather than taking the bool zero value; only an explicit
	// false disables it. Read it through RemoteControlEnabled.
	ClaudeRemoteControl *bool `json:"claude_remote_control,omitempty"`
	// ClaudePermissionMode is the --permission-mode value new Claude
	// sessions launch with. Unlike ClaudeRemoteControl, DefaultConfig
	// sets this explicitly to "default" rather than leaving it nil — nil
	// only occurs for a config.json predating this field, and is
	// treated identically to "default" (no flag injected; Claude's own
	// default applies). Read it through PermissionMode.
	ClaudePermissionMode *string `json:"claude_permission_mode,omitempty"`
}

// ClaudePermissionModes lists the values --permission-mode accepts, in
// the order the Claude Preferences screen cycles through them.
var ClaudePermissionModes = []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"}
```

(Note: the closing `}` of the `Config` struct moves down one field — don't duplicate it.)

Add the accessor right after `RemoteControlEnabled` (ends at line 129):

```go
// PermissionMode returns the configured --permission-mode value under a
// read lock, defaulting to "default" when unset (nil).
func (c *Config) PermissionMode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ClaudePermissionMode == nil {
		return "default"
	}
	return *c.ClaudePermissionMode
}
```

In `DefaultConfig()` (`config/config.go:167-187`), add the field to the returned `&Config{...}` literal, right after `ClaudeRemoteControl: boolPtr(true),`:

```go
		ClaudeRemoteControl:  boolPtr(true),
		ClaudePermissionMode: stringPtr("default"),
	}
}
```

Add `stringPtr` right after `boolPtr` (`config/config.go:189-191`):

```go
// stringPtr returns a pointer to s. Used for config fields whose absent
// (nil) state must be distinguished from the empty-string zero value.
func stringPtr(s string) *string { return &s }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./config/... -run 'TestPermissionMode|TestClaudePermissionModes' -v`
Expected: PASS

- [ ] **Step 5: Run the full config package test suite**

Run: `go test ./config/...`
Expected: PASS (no regressions from the struct-literal reformatting)

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): add ClaudePermissionMode setting"
```

---

### Task 2: Adapter interface method and per-agent implementations

**Files:**
- Modify: `session/agent/adapter.go:36-66` (interface), `session/agent/claude.go`, `session/agent/aider.go:37-40`, `session/agent/gemini.go:36-39`, `session/agent/default.go:30-32`
- Test: `session/agent/adapter_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `session/agent/adapter_test.go`, after `TestClaudeRemoteControlFlag` (ends at line 90):

```go
func TestClaudePermissionModeFlag(t *testing.T) {
	c := Claude()

	cases := []struct {
		name    string
		program string
		mode    string
		want    string
	}{
		{"plain", "claude", "acceptEdits", "claude --permission-mode acceptEdits"},
		{"preserves flags", "claude --model sonnet", "plan", "claude --permission-mode plan --model sonnet"},
		{"absolute path", "/usr/bin/claude", "bypassPermissions", "/usr/bin/claude --permission-mode bypassPermissions"},
		{"empty mode is no-op", "claude --model sonnet", "", "claude --model sonnet"},
		{"\"default\" mode is no-op", "claude --model sonnet", "default", "claude --model sonnet"},
		{"idempotent bare", "claude --permission-mode acceptEdits", "plan", "claude --permission-mode acceptEdits"},
		{"idempotent equals form", "claude --permission-mode=acceptEdits", "plan", "claude --permission-mode=acceptEdits"},
		{"empty program", "", "acceptEdits", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, c.ApplyPermissionModeFlag(tc.program, tc.mode))
		})
	}
}

func TestClaudePermissionModeComposesWithRemoteControl(t *testing.T) {
	// permission-mode and remote-control are applied by two independent
	// wrapper calls at instance-creation time (app/remote_control.go);
	// each must only ever touch its own flag name so composing them in
	// either order still produces one well-formed command.
	c := Claude()
	withRC := c.ApplyRemoteControlFlag("claude", "my task")
	assert.Equal(t, "claude --remote-control my-task", withRC)
	assert.Equal(t, "claude --permission-mode acceptEdits --remote-control my-task", c.ApplyPermissionModeFlag(withRC, "acceptEdits"))
}

func TestNonClaudeAdaptersNoPermissionMode(t *testing.T) {
	assert.Equal(t, "aider --model x", Aider().ApplyPermissionModeFlag("aider --model x", "acceptEdits"))
	assert.Equal(t, "gemini", Gemini().ApplyPermissionModeFlag("gemini", "acceptEdits"))
	assert.Equal(t, "codex --foo", Default().ApplyPermissionModeFlag("codex --foo", "acceptEdits"))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./session/agent/... -run 'TestClaudePermissionModeFlag|TestClaudePermissionModeComposesWithRemoteControl|TestNonClaudeAdaptersNoPermissionMode' -v`
Expected: FAIL — `ApplyPermissionModeFlag` undefined (compile error).

- [ ] **Step 3: Add the interface method**

In `session/agent/adapter.go`, add to the `Adapter` interface right after `ApplyRemoteControlFlag` (line 65):

```go
	ApplyRemoteControlFlag(program, sessionName string) string
	// ApplyPermissionModeFlag returns the program string with
	// "--permission-mode <mode>" inserted (e.g. "claude
	// --permission-mode acceptEdits"). mode == "" or "default" is a
	// no-op — Claude's own default already matches. Idempotent: if
	// --permission-mode is already present, the input is returned
	// unchanged. Returns the input unchanged for agents without a
	// permission-mode concept.
	ApplyPermissionModeFlag(program, mode string) string
}
```

- [ ] **Step 4: Implement the Claude adapter**

In `session/agent/claude.go`, add after `ApplyRemoteControlFlag` (ends at line 82):

```go
// ApplyPermissionModeFlag inserts "--permission-mode <mode>" after
// "claude". mode == "" or "default" is a no-op — Claude's own default
// already matches. Returns program unchanged if a --permission-mode
// flag is already present or if program is empty. mode is expected to
// come from config.ClaudePermissionModes, never free-typed user input,
// so no sanitization is applied (unlike ApplyRemoteControlFlag's
// sessionName).
func (claudeAdapter) ApplyPermissionModeFlag(program, mode string) string {
	if mode == "" || mode == "default" {
		return program
	}
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}
	for _, p := range parts[1:] {
		if p == "--permission-mode" || strings.HasPrefix(p, "--permission-mode=") {
			return program
		}
	}
	return parts[0] + " --permission-mode " + mode + strings.TrimPrefix(program, parts[0])
}
```

- [ ] **Step 5: Add no-op implementations to the other adapters**

In `session/agent/aider.go`, after `ApplyRemoteControlFlag` (ends at line 40):

```go
// ApplyPermissionModeFlag is a no-op for aider — it has no
// permission-mode equivalent.
func (aiderAdapter) ApplyPermissionModeFlag(program, _ string) string {
	return program
}
```

In `session/agent/gemini.go`, after `ApplyRemoteControlFlag` (ends at line 39):

```go
// ApplyPermissionModeFlag is a no-op for gemini — it has no
// permission-mode equivalent.
func (geminiAdapter) ApplyPermissionModeFlag(program, _ string) string {
	return program
}
```

In `session/agent/default.go`, after `ApplyRemoteControlFlag` (ends at line 32):

```go
// ApplyPermissionModeFlag implements Adapter. The fallback adapter
// never modifies the program string, so unknown agents get no
// permission-mode flag.
func (defaultAdapter) ApplyPermissionModeFlag(program, _ string) string { return program }
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./session/agent/... -v`
Expected: PASS — all tests including the new ones, and no compile errors from the interface change.

- [ ] **Step 7: Commit**

```bash
git add session/agent/adapter.go session/agent/claude.go session/agent/aider.go session/agent/gemini.go session/agent/default.go session/agent/adapter_test.go
git commit -m "feat(agent): add ApplyPermissionModeFlag to the Adapter interface"
```

---

### Task 3: BuildPermissionModeCommand builder function

**Files:**
- Modify: `session/agent_restart.go:20-28`
- Test: `session/agent_restart_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `session/agent_restart_test.go`, after `TestBuildRemoteControlCommand_Unknown` (ends at line 80):

```go
func TestBuildPermissionModeCommand_Claude(t *testing.T) {
	assert.Equal(t, "claude --permission-mode acceptEdits", BuildPermissionModeCommand("claude", "acceptEdits"))
}

func TestBuildPermissionModeCommand_ClaudeWithFlags(t *testing.T) {
	assert.Equal(t,
		"claude --permission-mode plan --model sonnet",
		BuildPermissionModeCommand("claude --model sonnet", "plan"),
	)
}

func TestBuildPermissionModeCommand_DefaultModeIsNoOp(t *testing.T) {
	assert.Equal(t, "claude --model sonnet", BuildPermissionModeCommand("claude --model sonnet", "default"))
	assert.Equal(t, "claude --model sonnet", BuildPermissionModeCommand("claude --model sonnet", ""))
}

func TestBuildPermissionModeCommand_Idempotent(t *testing.T) {
	assert.Equal(t,
		"claude --permission-mode plan",
		BuildPermissionModeCommand("claude --permission-mode plan", "acceptEdits"),
	)
}

func TestBuildPermissionModeCommand_Aider(t *testing.T) {
	assert.Equal(t, "aider --model gemma", BuildPermissionModeCommand("aider --model gemma", "acceptEdits"))
}

func TestBuildPermissionModeCommand_Unknown(t *testing.T) {
	assert.Equal(t, "codex", BuildPermissionModeCommand("codex", "acceptEdits"))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./session/... -run TestBuildPermissionModeCommand -v`
Expected: FAIL — `BuildPermissionModeCommand` undefined (compile error).

- [ ] **Step 3: Implement the builder function**

In `session/agent_restart.go`, add after `BuildRemoteControlCommand` (ends at line 28):

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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./session/... -run TestBuildPermissionModeCommand -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add session/agent_restart.go session/agent_restart_test.go
git commit -m "feat(session): add BuildPermissionModeCommand"
```

---

### Task 4: Wire permissionModeProgram into instance creation

**Files:**
- Modify: `app/remote_control.go:12-26` (new helper), `app/app.go:393`, `app/app.go:1696`, `app/state_new.go:59`, `app/state_prompt.go:59`
- Test: `app/remote_control_test.go` (create if it doesn't already exist — check first)

- [ ] **Step 1: Check for an existing remote_control test file**

Run: `ls app/remote_control_test.go 2>&1`

If it exists, read it first and add the new test function in the same style. If it doesn't exist, Step 2 creates it fresh.

- [ ] **Step 2: Write the failing test**

Create or append to `app/remote_control_test.go`:

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	"github.com/stretchr/testify/assert"
)

func TestPermissionModeProgram(t *testing.T) {
	t.Run("nil cfg is a no-op", func(t *testing.T) {
		assert.Equal(t, "claude --model sonnet", permissionModeProgram(nil, "claude --model sonnet"))
	})

	t.Run("default mode is a no-op", func(t *testing.T) {
		cfg := &config.Config{}
		assert.Equal(t, "claude --model sonnet", permissionModeProgram(cfg, "claude --model sonnet"))
	})

	t.Run("explicit mode is injected", func(t *testing.T) {
		mode := "acceptEdits"
		cfg := &config.Config{ClaudePermissionMode: &mode}
		assert.Equal(t, "claude --permission-mode acceptEdits --model sonnet", permissionModeProgram(cfg, "claude --model sonnet"))
	})

	t.Run("non-claude program is a no-op", func(t *testing.T) {
		mode := "acceptEdits"
		cfg := &config.Config{ClaudePermissionMode: &mode}
		assert.Equal(t, "aider --model gemma", permissionModeProgram(cfg, "aider --model gemma"))
	})
}
```

(If `app/remote_control_test.go` already exists with its own `package app` and imports, merge — don't duplicate the `package`/`import` block; just add the `TestPermissionModeProgram` function.)

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./app/... -run TestPermissionModeProgram -v`
Expected: FAIL — `permissionModeProgram` undefined (compile error).

- [ ] **Step 4: Implement the helper**

In `app/remote_control.go`, add after `remoteControlProgram` (ends at line 26):

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

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./app/... -run TestPermissionModeProgram -v`
Expected: PASS

- [ ] **Step 6: Wire the helper into all four instance-creation call sites**

In `app/app.go:393`, change:

```go
				Program:             remoteControlProgram(appConfig, h.rcAuth, program, wtTitle),
```

to:

```go
				Program:             permissionModeProgram(appConfig, remoteControlProgram(appConfig, h.rcAuth, program, wtTitle)),
```

In `app/app.go:1696`, change:

```go
			Program:             remoteControlProgram(appConfig, m.rcAuth, appConfig.GetProgram(), wtTitle),
```

to:

```go
			Program:             permissionModeProgram(appConfig, remoteControlProgram(appConfig, m.rcAuth, appConfig.GetProgram(), wtTitle)),
```

In `app/state_new.go:59`, change:

```go
				instance.Program = remoteControlProgram(m.appConfig, m.rcAuth, instance.Program, instance.Title)
```

to:

```go
				instance.Program = permissionModeProgram(m.appConfig, remoteControlProgram(m.appConfig, m.rcAuth, instance.Program, instance.Title))
```

In `app/state_prompt.go:59`, change:

```go
						selected.Program = remoteControlProgram(m.appConfig, m.rcAuth, selected.Program, selected.Title)
```

to:

```go
						selected.Program = permissionModeProgram(m.appConfig, remoteControlProgram(m.appConfig, m.rcAuth, selected.Program, selected.Title))
```

- [ ] **Step 7: Build and run the full app package test suite**

Run: `go build ./... && go test ./app/...`
Expected: PASS, no compile errors.

- [ ] **Step 8: Commit**

```bash
git add app/remote_control.go app/remote_control_test.go app/app.go app/state_new.go app/state_prompt.go
git commit -m "feat(app): apply Claude permission-mode flag at instance creation"
```

---

### Task 5: Claude Preferences UI row

**Files:**
- Modify: `ui/overlay/claudePreferences.go`
- Test: `ui/overlay/claudePreferences_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `ui/overlay/claudePreferences_test.go`, after `TestClaudePreferencesTogglesRemoteControl` (ends at line 24):

```go
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

func TestClaudePreferencesRowNavigationClamps(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")

	// Up from row 0 stays at row 0: toggles Remote Control, not Permission Mode.
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.RemoteControlEnabled())

	// Down twice stays at row 1 (only two rows): cycles Permission Mode, not Remote Control.
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.Equal(t, "acceptEdits", cfg.PermissionMode())
}

func TestClaudePreferencesRendersPermissionMode(t *testing.T) {
	mode := "plan"
	cfg := &config.Config{ClaudePermissionMode: &mode}
	cp := NewClaudePreferences(cfg, false, "")
	rendered := cp.Render()
	assert.Contains(t, rendered, "Permission Mode")
	assert.Contains(t, rendered, "plan")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./ui/overlay/... -run 'TestClaudePreferencesCyclesPermissionMode|TestClaudePreferencesRowNavigationClamps|TestClaudePreferencesRendersPermissionMode' -v`
Expected: FAIL — cursor/second-row behavior doesn't exist yet, so the toggled/cycled assertions fail (compiles fine, since `NewClaudePreferences`/`HandleKeyPress`/`Render` already exist — behavior is what's missing).

- [ ] **Step 3: Rewrite claudePreferences.go with a cursor and second row**

Replace the full contents of `ui/overlay/claudePreferences.go` with:

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
// can be added later without growing that list — today it holds two
// rows: Remote Control and Permission Mode.
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

// claudePrefsRowCount is the number of navigable rows: Remote Control
// and Permission Mode.
const claudePrefsRowCount = 2

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
			})
		case 1:
			c.cfg.Mutate(func(cc *config.Config) {
				next := nextPermissionMode(cc.PermissionMode())
				cc.ClaudePermissionMode = &next
			})
		}
		return false, true
	}
	return false, false
}

// nextPermissionMode returns the value config.ClaudePermissionModes
// after current, wrapping from the last value back to the first.
func nextPermissionMode(current string) string {
	modes := config.ClaudePermissionModes
	for i, m := range modes {
		if m == current {
			return modes[(i+1)%len(modes)]
		}
	}
	return modes[0]
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

	content := claudePrefsTitleStyle.Render("Claude Preferences") + "\n\n" +
		rcRow + "\n" +
		pmRow + "\n\n" +
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

Run: `go test ./ui/overlay/... -v -run ClaudePreferences`
Expected: PASS — all `ClaudePreferences*` tests, including the pre-existing `TestClaudePreferencesTogglesRemoteControl`, `TestClaudePreferencesShowsBlockedHint`, `TestClaudePreferencesEscCloses`.

- [ ] **Step 5: Run the full ui/overlay package test suite**

Run: `go test ./ui/overlay/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add ui/overlay/claudePreferences.go ui/overlay/claudePreferences_test.go
git commit -m "feat(ui): add Permission Mode row to Claude Preferences"
```

---

### Task 6: Full verification

**Files:** None (verification only).

- [ ] **Step 1: Format check**

Run: `gofmt -l .`
Expected: empty output. If any files are listed, run `gofmt -w .` and make a new commit (don't amend an earlier task's commit this late): `git add -u && git commit -m "style: gofmt"`.

- [ ] **Step 2: Full build**

Run: `CGO_ENABLED=0 go build -o /tmp/loom-permission-mode-check ./...`
Expected: exits 0, no errors.

- [ ] **Step 3: Full test suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 4: Race detector**

Run: `CC=clang CGO_ENABLED=1 go test -race ./... 2>&1 | tail -50` (fall back to `CC=gcc` if `clang` is unavailable; per `[[loom-race-detector-local]]` this repo builds with `CGO_ENABLED=0` by default so `-race` needs CGO explicitly enabled).
Expected: PASS, no `DATA RACE` reports. The `config.Config` mutex added in the prior settings-menu feature already covers `ClaudePermissionMode` since it's guarded by the same `Mutate`/`PermissionMode()` locked-accessor pair as every other field.

- [ ] **Step 5: Lint**

Run: `golangci-lint run --timeout=3m --fast`
Expected: no new findings introduced by this change (pre-existing findings unrelated to these files are not this task's concern).

- [ ] **Step 6: Report**

No commit for this task (verification only). Summarize: all tests pass, race-clean, lint-clean, build succeeds.
