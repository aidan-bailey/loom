# Settings Menu Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an in-app settings overlay (opened with `S`) that makes every `config.Config` field — `DefaultProgram`, `AutoYes`, `DaemonPollInterval`, `BranchPrefix`, `Profiles`, and `ClaudeRemoteControl` — editable from the TUI, with changes applying live and persisting immediately.

**Architecture:** A new `stateSettings` app state hosts a composed `ui/overlay.SettingsOverlay` (main row list) that internally delegates to two nested sub-screens (`ProfilesManager`, `ClaudePreferences`) and an embedded `TextInputOverlay` for scalar text/int edits — no new top-level states beyond `stateSettings` itself. Every commit flows back through `app/state_settings.go`, which persists via the existing `config.SaveConfigTo` and refreshes the two `home` fields (`m.program`, `m.autoYes`) that shadow `appConfig` and would otherwise go stale. A new `config.Config.Mutate`/`GetBranchPrefix` pair closes a latent data race between the settings overlay (main goroutine) and `scriptHost.BranchPrefix()` (Lua dispatch goroutine). The daemon gets a per-tick config re-read so `DaemonPollInterval` changes reach the already-running daemon process without a restart.

**Tech Stack:** Go 1.23, Bubble Tea v2 (`charm.land/bubbletea/v2`), Lipgloss v2, gopher-lua (script engine), testify (tests).

**Spec:** `docs/superpowers/specs/2026-07-01-settings-menu-design.md`

---

## File Structure

| File | Responsibility |
|---|---|
| `config/config.go` (modify) | Add `mu sync.RWMutex`, `Mutate(fn func(*Config))`, `GetBranchPrefix() string`. |
| `app/app_scripts.go` (modify) | `scriptHost.BranchPrefix()` switches to the locked accessor; new `case script.SettingsIntent` in `handleScriptIntent`. |
| `ui/overlay/settingsOverlay.go` (new) | Main settings row list: navigation, Auto Yes toggle, scalar text-edit sub-mode, drill-in dispatch to Profiles/Claude Preferences. |
| `ui/overlay/profilesManager.go` (new) | Profiles CRUD sub-screen (add/edit-program/delete/set-default). |
| `ui/overlay/claudePreferences.go` (new) | Claude-specific preferences sub-screen (Remote Control toggle + blocked-auth hint). |
| `app/state_settings.go` (new) | `handleStateSettingsKey`: drives the overlay, persists on change, refreshes `m.program`/`m.autoYes`. |
| `app/intents.go` (modify) | `runOpenSettings` — builds and opens the overlay. |
| `app/overlay_host.go` (modify) | New `overlaySettings` kind + `settingsOverlay()` accessor. |
| `app/app.go` (modify) | New `stateSettings` state constant; dispatch + overlay-render switch cases. |
| `script/intent.go` (modify) | New `SettingsIntent` type. |
| `script/api_actions.go` (modify) | New `open_settings` Lua action. |
| `script/defaults.lua` (modify) | New `S` binding. |
| `keys/keys.go` (modify) | New `KeySettings` binding (help panel / menu-bar display). |
| `app/help.go` (modify) | New help-panel row. |
| `app/migration_parity_test.go` (modify) | New parity case for `S` → `SettingsIntent{}`. |
| `daemon/daemon.go` (modify) | `refreshPollInterval` helper + wiring into `RunDaemon`'s tick loop. |
| `USAGE.md`, `CLAUDE.md` (modify) | Document the `S` keybinding. |

---

## Task 1: Concurrency-safe `config.Config`

**Files:**
- Modify: `config/config.go`
- Test: `config/config_test.go`

- [ ] **Step 1: Write the failing race test**

Append to `config/config_test.go`:

```go
func TestConfigMutateIsRaceSafeWithGetBranchPrefix(t *testing.T) {
	cfg := DefaultConfig()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 1000; i++ {
			cfg.Mutate(func(c *Config) { c.BranchPrefix = fmt.Sprintf("user-%d/", i) })
		}
		close(done)
	}()

	for i := 0; i < 1000; i++ {
		_ = cfg.GetBranchPrefix()
	}
	<-done
}
```

Add `"fmt"` to the import block if not already present.

- [ ] **Step 2: Run test to verify it fails to compile**

Run: `go test ./config/... -run TestConfigMutateIsRaceSafeWithGetBranchPrefix -race`
Expected: FAIL — `cfg.Mutate` and `cfg.GetBranchPrefix` undefined.

- [ ] **Step 3: Add the mutex and locked accessors**

In `config/config.go`, add `"sync"` to the import block, then replace the `Config` struct definition with the version below (adds the `mu` field) and add the two new methods directly after it — placement relative to `RemoteControlEnabled` (which stays where it is, unchanged) doesn't matter, Go doesn't order methods by declaration position:

```go
// Config represents the application configuration
type Config struct {
	// mu guards mutation of every field below once the settings overlay
	// makes Config mutable at runtime. Before the settings overlay,
	// Config was loaded once and never mutated, so nothing raced on it.
	// scriptHost.BranchPrefix() (app/app_scripts.go) reads BranchPrefix
	// from the Lua dispatch goroutine — a tea.Cmd body running
	// concurrently with Update — while the settings overlay writes it
	// from the main goroutine. Mutate/GetBranchPrefix close that race
	// the same way config.State.mu already guards InstancesData/
	// HelpScreensSeen against the same class of race. Unexported, so
	// encoding/json skips it (same precedent as State.mu).
	mu sync.RWMutex

	// DefaultProgram is the default program to run in new instances
	DefaultProgram string `json:"default_program"`
	// AutoYes is a flag to automatically accept all prompts.
	AutoYes bool `json:"auto_yes"`
	// DaemonPollInterval is the interval (ms) at which the daemon polls sessions for autoyes mode.
	DaemonPollInterval int `json:"daemon_poll_interval"`
	// BranchPrefix is the prefix used for git branches created by the application.
	BranchPrefix string `json:"branch_prefix"`
	// Profiles is a list of named program profiles.
	Profiles []Profile `json:"profiles,omitempty"`
	// ClaudeRemoteControl controls whether new Claude sessions launch
	// with `--remote-control` (named after the session title). It is a
	// pointer so a config file predating this field (nil) is treated as
	// enabled rather than taking the bool zero value; only an explicit
	// false disables it. Read it through RemoteControlEnabled.
	ClaudeRemoteControl *bool `json:"claude_remote_control,omitempty"`
}

// Mutate runs fn with the write lock held. Callers outside this package
// (the settings overlay) use this instead of writing exported fields
// directly, so a concurrent GetBranchPrefix cannot observe a torn write.
// fn must not call GetBranchPrefix or Mutate itself (would deadlock).
func (c *Config) Mutate(fn func(*Config)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
}

// GetBranchPrefix returns BranchPrefix under a read lock. Use this
// instead of reading the field directly from any goroutine other than
// the one currently calling Mutate — in practice, scriptHost.BranchPrefix,
// which runs on the Lua dispatch goroutine.
func (c *Config) GetBranchPrefix() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.BranchPrefix
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test ./config/... -run TestConfigMutateIsRaceSafeWithGetBranchPrefix -race -v`
Expected: PASS, no race detected.

- [ ] **Step 5: Wire `scriptHost.BranchPrefix()` through the locked accessor**

In `app/app_scripts.go`, replace:

```go
// BranchPrefix implements script.Host.
func (s *scriptHost) BranchPrefix() string {
	if s.m.appConfig != nil {
		return s.m.appConfig.BranchPrefix
	}
	return ""
}
```

with:

```go
// BranchPrefix implements script.Host. Reads through the locked
// accessor because this runs on the Lua dispatch goroutine while the
// settings overlay can be mutating the same *Config concurrently on
// the main goroutine — see Config.Mutate's doc comment.
func (s *scriptHost) BranchPrefix() string {
	if s.m.appConfig != nil {
		return s.m.appConfig.GetBranchPrefix()
	}
	return ""
}
```

- [ ] **Step 6: Run the full config and app test suites**

Run: `go test ./config/... ./app/... -v 2>&1 | tail -40`
Expected: PASS (existing `TestCsActionsShowHelpEnqueues`-style and `scriptHost` tests unaffected).

- [ ] **Step 7: Commit**

```bash
git add config/config.go config/config_test.go app/app_scripts.go
git commit -m "$(cat <<'EOF'
fix(config): make Config safely mutable at runtime

The settings overlay (next commits) will mutate config.Config live
from the main goroutine while scriptHost.BranchPrefix() reads it from
the Lua dispatch goroutine — add a mutex plus a locked Mutate/
GetBranchPrefix pair, mirroring config.State's existing pattern, and
switch BranchPrefix() to the locked read.
EOF
)"
```

---

## Task 2: Main settings overlay (skeleton + Auto Yes)

**Files:**
- Create: `ui/overlay/settingsOverlay.go`
- Test: `ui/overlay/settingsOverlay_test.go`

- [ ] **Step 1: Write the failing navigation + toggle test**

Create `ui/overlay/settingsOverlay_test.go`:

```go
package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func newTestSettingsCfg() *config.Config {
	return &config.Config{
		DefaultProgram:     "claude",
		AutoYes:            false,
		DaemonPollInterval: 1000,
		BranchPrefix:       "aidan/",
	}
}

func TestSettingsOverlayNavigation(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	assert.Equal(t, 0, so.cursor)

	so.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.Equal(t, 1, so.cursor)

	so.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	assert.Equal(t, 0, so.cursor)
}

func TestSettingsOverlayNavigationDoesNotOverflow(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	for i := 0; i < 20; i++ {
		so.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	assert.Equal(t, int(settingsFieldCount)-1, so.cursor)

	for i := 0; i < 20; i++ {
		so.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	assert.Equal(t, 0, so.cursor)
}

func TestSettingsOverlayTogglesAutoYes(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	so.cursor = int(settingsFieldAutoYes)

	closed, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, closed)
	assert.True(t, changed)
	assert.True(t, cfg.AutoYes)

	closed, changed = so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, closed)
	assert.True(t, changed)
	assert.False(t, cfg.AutoYes)
}

func TestSettingsOverlayEscCloses(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	closed, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, changed)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./ui/overlay/... -run TestSettingsOverlay -v`
Expected: FAIL — `NewSettingsOverlay`, `settingsFieldCount`, `settingsFieldAutoYes` undefined.

- [ ] **Step 3: Implement the skeleton**

Create `ui/overlay/settingsOverlay.go`:

```go
package overlay

import (
	"fmt"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// settingsField indexes the rows of the main settings list, in display order.
type settingsField int

const (
	settingsFieldDefaultProgram settingsField = iota
	settingsFieldAutoYes
	settingsFieldDaemonPollInterval
	settingsFieldBranchPrefix
	settingsFieldProfiles
	settingsFieldClaudePreferences
	settingsFieldCount
)

func (f settingsField) label() string {
	switch f {
	case settingsFieldDefaultProgram:
		return "Default Program"
	case settingsFieldAutoYes:
		return "Auto Yes"
	case settingsFieldDaemonPollInterval:
		return "Daemon Poll Interval"
	case settingsFieldBranchPrefix:
		return "Branch Prefix"
	case settingsFieldProfiles:
		return "Profiles"
	case settingsFieldClaudePreferences:
		return "Claude Preferences"
	}
	return ""
}

// settingsMode distinguishes the main row list from a nested sub-screen
// or an in-place text edit. Only one is active at a time; HandleKeyPress
// proxies to whichever child is active rather than introducing a second
// top-level app state for "editing a field."
type settingsMode int

const (
	settingsBrowsing settingsMode = iota
	settingsEditingText
	settingsProfilesSub
	settingsClaudePrefsSub
)

// SettingsOverlay is the config.json editor: a vertical list of scalar
// fields plus two drill-in rows (Profiles, Claude Preferences). It owns
// no persistence — HandleKeyPress's second return value reports whether
// a field changed; the caller (app.handleStateSettingsKey) is
// responsible for config.SaveConfigTo and refreshing the home fields
// that shadow cfg (m.program, m.autoYes).
//
// authBlocked/authReason mirror session.RemoteControlAuth.Blocked()/
// Reason, passed as plain values rather than the session type so this
// package stays decoupled from session (matching every other overlay).
type SettingsOverlay struct {
	cfg         *config.Config
	authBlocked bool
	authReason  string

	cursor int
	width  int
	height int

	mode         settingsMode
	editing      *TextInputOverlay
	editingField settingsField

	profiles    *ProfilesManager
	claudePrefs *ClaudePreferences

	lastErr error
}

// NewSettingsOverlay creates the settings overlay over cfg.
func NewSettingsOverlay(cfg *config.Config, authBlocked bool, authReason string) *SettingsOverlay {
	return &SettingsOverlay{cfg: cfg, authBlocked: authBlocked, authReason: authReason, width: 60}
}

// HandleKeyPress processes one key press. closed reports whether the
// whole overlay should be dismissed (only true from browsing mode);
// changed reports whether cfg was mutated, so the caller knows to
// persist and refresh its shadow fields.
func (s *SettingsOverlay) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	s.lastErr = nil
	switch s.mode {
	case settingsEditingText:
		return s.handleEditingText(msg)
	case settingsProfilesSub:
		closedSub, ch := s.profiles.HandleKeyPress(msg)
		if closedSub {
			s.mode = settingsBrowsing
			s.profiles = nil
		}
		return false, ch
	case settingsClaudePrefsSub:
		closedSub, ch := s.claudePrefs.HandleKeyPress(msg)
		if closedSub {
			s.mode = settingsBrowsing
			s.claudePrefs = nil
		}
		return false, ch
	}

	switch msg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < int(settingsFieldCount)-1 {
			s.cursor++
		}
	case "esc", "q":
		return true, false
	case " ", "space", "enter":
		return s.activateRow()
	}
	return false, false
}

// activateRow runs the Enter/space action for the currently selected
// row. Only Auto Yes changes cfg synchronously here; the rest open a
// nested edit mode that reports its own change on a later HandleKeyPress.
func (s *SettingsOverlay) activateRow() (closed, changed bool) {
	switch settingsField(s.cursor) {
	case settingsFieldAutoYes:
		s.cfg.Mutate(func(c *config.Config) { c.AutoYes = !c.AutoYes })
		return false, true
	case settingsFieldDefaultProgram:
		s.startTextEdit(settingsFieldDefaultProgram, "Default Program", s.cfg.DefaultProgram)
	case settingsFieldBranchPrefix:
		s.startTextEdit(settingsFieldBranchPrefix, "Branch Prefix", s.cfg.BranchPrefix)
	case settingsFieldDaemonPollInterval:
		s.startTextEdit(settingsFieldDaemonPollInterval, "Daemon Poll Interval (ms)", fmt.Sprintf("%d", s.cfg.DaemonPollInterval))
	case settingsFieldProfiles:
		s.profiles = NewProfilesManager(s.cfg)
		s.profiles.SetWidth(s.width)
		s.mode = settingsProfilesSub
	case settingsFieldClaudePreferences:
		s.claudePrefs = NewClaudePreferences(s.cfg, s.authBlocked, s.authReason)
		s.claudePrefs.SetWidth(s.width)
		s.mode = settingsClaudePrefsSub
	}
	return false, false
}

func (s *SettingsOverlay) startTextEdit(field settingsField, title, value string) {
	s.mode = settingsEditingText
	s.editingField = field
	s.editing = NewTextInputOverlay(title, value)
	s.editing.SetSize(s.width, 3)
}

// handleEditingText forwards to the embedded TextInputOverlay and, on
// submit, applies the parsed value to cfg via Mutate. On cancel (Esc)
// no change is made. Either way control returns to browsing mode.
func (s *SettingsOverlay) handleEditingText(msg tea.KeyPressMsg) (closed, changed bool) {
	closedSub, _ := s.editing.HandleKeyPress(msg)
	if !closedSub {
		return false, false
	}
	submitted := s.editing.IsSubmitted()
	value := s.editing.GetValue()
	field := s.editingField
	s.mode = settingsBrowsing
	s.editing = nil
	if !submitted {
		return false, false
	}
	return false, s.applyTextEdit(field, value)
}

// TakeError returns and clears the last validation error (currently only
// possible from the Daemon Poll Interval field). Callers poll this after
// HandleKeyPress.
func (s *SettingsOverlay) TakeError() error {
	err := s.lastErr
	s.lastErr = nil
	return err
}

// HandleKey satisfies the Overlay interface. State handlers that need
// the changed signal call HandleKeyPress directly instead (mirrors
// WorkspacePicker.HandleKey/HandleKeyPress).
func (s *SettingsOverlay) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := s.HandleKeyPress(msg)
	return closed, nil
}

// SetSize satisfies the Overlay interface.
func (s *SettingsOverlay) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// View satisfies the Overlay interface.
func (s *SettingsOverlay) View() string {
	return s.Render()
}

var (
	settingsTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	settingsSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	settingsNormalStyle   = lipgloss.NewStyle().Foreground(ui.TextPrimary)
	settingsHintStyle     = lipgloss.NewStyle().Foreground(ui.TextHint)
)

// Render renders whichever mode is active: the main row list, an
// embedded text edit, or a nested sub-screen.
func (s *SettingsOverlay) Render() string {
	switch s.mode {
	case settingsEditingText:
		return s.editing.Render()
	case settingsProfilesSub:
		return s.profiles.Render()
	case settingsClaudePrefsSub:
		return s.claudePrefs.Render()
	}

	content := settingsTitleStyle.Render("Settings") + "\n\n"
	for i := settingsField(0); i < settingsFieldCount; i++ {
		cursor := "  "
		if int(i) == s.cursor {
			cursor = "> "
		}
		line := fmt.Sprintf("%s%-22s %s", cursor, i.label(), s.valueFor(i))
		if int(i) == s.cursor {
			content += settingsSelectedStyle.Render(line) + "\n"
		} else {
			content += settingsNormalStyle.Render(line) + "\n"
		}
	}
	content += "\n" + settingsHintStyle.Render("enter edit/open • esc close")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(s.width)
	return border.Render(content)
}

// valueFor renders the current display value for row f.
func (s *SettingsOverlay) valueFor(f settingsField) string {
	switch f {
	case settingsFieldDefaultProgram:
		return s.cfg.DefaultProgram
	case settingsFieldAutoYes:
		if s.cfg.AutoYes {
			return "[x]"
		}
		return "[ ]"
	case settingsFieldDaemonPollInterval:
		return fmt.Sprintf("%d ms", s.cfg.DaemonPollInterval)
	case settingsFieldBranchPrefix:
		return s.cfg.BranchPrefix
	case settingsFieldProfiles:
		return fmt.Sprintf("(%d) →", len(s.cfg.Profiles))
	case settingsFieldClaudePreferences:
		return "→"
	}
	return ""
}
```

This step also requires `applyTextEdit`, `ProfilesManager`, and `ClaudePreferences`, which don't exist yet — the package will not compile until Step 4 stubs them out. That's expected; proceed to Step 4 before running tests.

- [ ] **Step 4: Add a temporary `applyTextEdit` stub and minimal sub-screen stubs so the package compiles**

Add to `ui/overlay/settingsOverlay.go`, right after `handleEditingText`:

```go
// applyTextEdit is filled in fully in Task 4 (BranchPrefix/DaemonPollInterval
// validation). For now it only handles the case exercised by this task's
// tests: none — Auto Yes never reaches this path. Returns false always.
func (s *SettingsOverlay) applyTextEdit(field settingsField, value string) bool {
	return false
}
```

Create a minimal `ui/overlay/profilesManager.go` (fleshed out fully in Task 5 — the `HandleKeyPress` signature below is final and does not change, only its body does):

```go
package overlay

import (
	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
)

// ProfilesManager is fleshed out in Task 5.
type ProfilesManager struct {
	cfg   *config.Config
	width int
}

func NewProfilesManager(cfg *config.Config) *ProfilesManager { return &ProfilesManager{cfg: cfg} }
func (p *ProfilesManager) SetWidth(w int)                    { p.width = w }
func (p *ProfilesManager) Render() string                    { return "" }
func (p *ProfilesManager) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	if s := msg.String(); s == "esc" || s == "q" {
		return true, false
	}
	return false, false
}
```

Create a minimal `ui/overlay/claudePreferences.go` (fleshed out fully in Task 6 — same note: the signature below is final):

```go
package overlay

import (
	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
)

// ClaudePreferences is fleshed out in Task 6.
type ClaudePreferences struct {
	cfg         *config.Config
	authBlocked bool
	authReason  string
	width       int
}

func NewClaudePreferences(cfg *config.Config, authBlocked bool, authReason string) *ClaudePreferences {
	return &ClaudePreferences{cfg: cfg, authBlocked: authBlocked, authReason: authReason}
}
func (c *ClaudePreferences) SetWidth(w int) { c.width = w }
func (c *ClaudePreferences) Render() string { return "" }
func (c *ClaudePreferences) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	if s := msg.String(); s == "esc" || s == "q" {
		return true, false
	}
	return false, false
}
```

**Task 5 and Task 6 replace these two files entirely** (full CRUD/toggle behavior), but the type name, constructor signature, and `HandleKeyPress`/`SetWidth`/`Render` method signatures introduced here are final — later tasks change bodies, not signatures.

- [ ] **Step 5: Run test to verify it passes**

Run: `go build ./... && go test ./ui/overlay/... -run TestSettingsOverlay -v`
Expected: PASS for all four `TestSettingsOverlay*` tests; build succeeds.

- [ ] **Step 6: Commit**

```bash
git add ui/overlay/settingsOverlay.go ui/overlay/profilesManager.go ui/overlay/claudePreferences.go ui/overlay/settingsOverlay_test.go
git commit -m "$(cat <<'EOF'
feat(ui): add settings overlay skeleton with Auto Yes toggle

Vertical row list over config.Config (Default Program, Auto Yes,
Daemon Poll Interval, Branch Prefix, Profiles, Claude Preferences),
navigable with up/k/down/j, Esc to close. Only Auto Yes is wired to a
real toggle so far; Profiles/Claude Preferences are stubs completed in
later tasks.
EOF
)"
```

---

## Task 3: Entry point (keybinding → Lua → intent → overlay)

**Files:**
- Modify: `keys/keys.go`, `script/defaults.lua`, `script/intent.go`, `script/api_actions.go`, `app/app_scripts.go`, `app/app.go`, `app/overlay_host.go`, `app/help.go`, `app/migration_parity_test.go`
- Create: `app/state_settings.go`
- Modify: `app/intents.go`
- Test: `script/api_actions_test.go`, `app/migration_parity_test.go`

- [ ] **Step 1: Add the `KeySettings` binding**

In `keys/keys.go`, add `KeySettings` to the `const` block (after `KeyDirectAttachTerminal`):

```go
const (
	KeyUp KeyName = iota
	KeyDown
	KeyNew
	KeyKill
	KeyQuit
	KeySubmit
	KeySubmitName
	KeyCheckout
	KeyResume
	KeyPrompt
	KeyHelp
	KeyWorkspace
	KeyWorkspaceLeft
	KeyWorkspaceRight
	KeyFullScreenAttachAgent
	KeyFullScreenAttachTerminal
	KeyDiff
	KeyQuickInputAgent
	KeyQuickInputTerminal
	KeyDirectAttachAgent
	KeyDirectAttachTerminal
	KeySettings
)
```

And add its binding to `GlobalkeyBindings` (after `KeyWorkspace`'s entry):

```go
	KeySettings: key.NewBinding(
		key.WithKeys("S"),
		key.WithHelp("S", "settings"),
	),
```

- [ ] **Step 2: Add the `SettingsIntent` type**

In `script/intent.go`, add after `WorkspacePickerIntent`:

```go
// SettingsIntent opens the settings overlay for editing config.json fields.
type SettingsIntent struct{}
```

And add its marker method next to the others at the bottom of the file:

```go
func (SettingsIntent) intent() {}
```

- [ ] **Step 3: Add the `open_settings` Lua action**

In `script/api_actions.go`, add after the `open_workspace_picker` registration:

```go
	actions.RawSetString("open_settings", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, SettingsIntent{})
	}))
```

- [ ] **Step 4: Write the failing action test**

Add to `script/api_actions_test.go`, after `TestCsActionsOpenWorkspacePickerEnqueues`:

```go
func TestCsActionsOpenSettingsEnqueues(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("S", function() cs.actions.open_settings() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "S")
	_, ok := h.enqueued[0].(SettingsIntent)
	assert.True(t, ok)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./script/... -run TestCsActionsOpenSettingsEnqueues -v`
Expected: PASS.

- [ ] **Step 6: Bind `S` in the default keymap**

In `script/defaults.lua`, add next to the `W` binding:

```lua
cs.bind("S", function() cs.actions.open_settings() end,        { help = "settings" })
```

- [ ] **Step 7: Add the `stateSettings` app state and `overlaySettings` kind**

In `app/app.go`, add `stateSettings` to the `state` const block (after `stateFileExplorer`):

```go
	// stateSettings is the state when the settings overlay is displayed.
	stateSettings
)
```

In `app/overlay_host.go`, add `overlaySettings` to the `overlayKind` const block (after `overlayFileExplorer`):

```go
	overlaySettings
)
```

And add an accessor next to `fileExplorer()`:

```go
// settingsOverlay returns the active SettingsOverlay, or nil when a
// different overlay is active.
func (m *home) settingsOverlay() *overlay.SettingsOverlay {
	if o, ok := m.activeOverlay.(*overlay.SettingsOverlay); ok {
		return o
	}
	return nil
}
```

- [ ] **Step 8: Add `handleStateSettingsKey`**

Create `app/state_settings.go`:

```go
package app

import (
	"fmt"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
)

// handleStateSettingsKey drives the settings overlay. Every key press
// may report a field change; when it does, the change is persisted to
// disk and the two home fields that shadow appConfig (m.program,
// m.autoYes) are refreshed so new-instance creation picks up the new
// values immediately instead of using a stale cached copy.
func handleStateSettingsKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	so := m.settingsOverlay()
	if so == nil {
		return m, nil
	}

	closed, changed := so.HandleKeyPress(msg)
	if err := so.TakeError(); err != nil {
		return m, m.handleError(err)
	}

	if changed {
		if m.activeCtx != nil {
			if err := config.SaveConfigTo(m.appConfig, m.activeCtx.ConfigDir); err != nil {
				return m, m.handleError(fmt.Errorf("save settings: %w", err))
			}
		}
		m.program = m.appConfig.GetProgram()
		m.autoYes = m.appConfig.AutoYes
	}

	if closed {
		m.dismissOverlay()
		m.state = stateDefault
	}
	return m, nil
}
```

- [ ] **Step 9: Add `runOpenSettings`**

In `app/intents.go`, add next to `runOpenWorkspacePicker`:

```go
// runOpenSettings opens the settings overlay over the active workspace's
// config. authBlocked/authReason are passed as plain values (not
// session.RemoteControlAuth) to keep ui/overlay decoupled from session.
func runOpenSettings(m *home) (tea.Model, tea.Cmd) {
	if m.appConfig == nil {
		return m, m.handleError(fmt.Errorf("no configuration loaded"))
	}
	so := overlay.NewSettingsOverlay(m.appConfig, m.rcAuth.Blocked(), m.rcAuth.Reason)
	m.setOverlay(so, overlaySettings)
	m.state = stateSettings
	return m, nil
}
```

- [ ] **Step 10: Wire the intent and state dispatch**

In `app/app_scripts.go`, add to the `handleScriptIntent` switch (after `case script.WorkspacePickerIntent:`):

```go
	case script.SettingsIntent:
		_, cmd = runOpenSettings(m)
```

In `app/app.go`, add to `handleKeyPress`'s switch (after `case stateWorkspace:`):

```go
	case stateSettings:
		return handleStateSettingsKey(m, msg)
```

And add `stateSettings` to the overlay-render case in `View()` (the `switch m.state { case statePrompt, stateHelp, stateConfirm, stateWorkspace:` block):

```go
	case statePrompt, stateHelp, stateConfirm, stateWorkspace, stateSettings:
		return asView(overlay.PlaceOverlay(0, 0, m.activeOverlay.View(), mainView, true, true))
```

- [ ] **Step 11: Add the help-panel row**

In `app/help.go`, add to `generalOtherEntries` (after the `KeyWorkspace` entry):

```go
		{bindings: []keys.KeyName{keys.KeySettings}, desc: "Open settings"},
```

- [ ] **Step 12: Add the migration-parity test case**

In `app/migration_parity_test.go`, add to the `cases` table (after the `"workspace_picker"` row):

```go
		{"open_settings", "S", script.SettingsIntent{}},
```

- [ ] **Step 13: Run the end-to-end entry-point test**

Run: `go build ./... && go test ./app/... -run TestMigrationParity -v`
Expected: PASS, including the new `open_settings` subtest.

- [ ] **Step 14: Run the full test suite to catch any wiring mistakes**

Run: `go test ./... 2>&1 | tail -60`
Expected: PASS across all packages.

- [ ] **Step 15: Commit**

```bash
git add keys/keys.go script/intent.go script/api_actions.go script/api_actions_test.go script/defaults.lua app/app.go app/overlay_host.go app/state_settings.go app/intents.go app/app_scripts.go app/help.go app/migration_parity_test.go
git commit -m "$(cat <<'EOF'
feat(app): wire S to open the settings overlay

Adds the full dispatch chain — keybinding, Lua action, Intent,
stateSettings, and the state handler that persists changes via
config.SaveConfigTo and refreshes m.program/m.autoYes so they don't
shadow a stale appConfig.
EOF
)"
```

---

## Task 4: Scalar text-edit fields (Default Program, Branch Prefix, Daemon Poll Interval)

**Files:**
- Modify: `ui/overlay/settingsOverlay.go`
- Test: `ui/overlay/settingsOverlay_test.go`
- Test: `app/state_settings_test.go` (new)

- [ ] **Step 1: Write the failing text-edit tests**

Add to `ui/overlay/settingsOverlay_test.go`:

```go
// submitText opens the editor for field, clears whatever value it was
// pre-filled with, types text, and submits. The clear step matters:
// NewTextInputOverlay pre-fills the textarea with the field's current
// value and leaves the cursor at the end, so typed runes append rather
// than replace — without clearing first, editing "aidan/" to "team/"
// would actually produce "aidan/team/".
func submitText(t *testing.T, so *SettingsOverlay, field settingsField, text string) (closed, changed bool) {
	t.Helper()
	so.cursor = int(field)
	so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}) // open the edit
	for i := 0; i < 64; i++ {                              // clear the pre-filled value regardless of its length
		so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range text {
		so.HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}) // submit
}

func TestSettingsOverlayEditsBranchPrefix(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	_, changed := submitText(t, so, settingsFieldBranchPrefix, "team/")
	assert.True(t, changed)
	assert.Equal(t, "team/", cfg.BranchPrefix)
}

func TestSettingsOverlayEditsDefaultProgram(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	_, changed := submitText(t, so, settingsFieldDefaultProgram, "aider")
	assert.True(t, changed)
	assert.Equal(t, "aider", cfg.DefaultProgram)
}

func TestSettingsOverlayEditsDaemonPollInterval(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	_, changed := submitText(t, so, settingsFieldDaemonPollInterval, "2000")
	assert.True(t, changed)
	assert.Equal(t, 2000, cfg.DaemonPollInterval)
}

func TestSettingsOverlayRejectsNonNumericPollInterval(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	_, changed := submitText(t, so, settingsFieldDaemonPollInterval, "notanumber")
	assert.False(t, changed)
	assert.Equal(t, 1000, cfg.DaemonPollInterval, "invalid input must not overwrite the existing value")
	assert.Error(t, so.TakeError())
}

func TestSettingsOverlayEscCancelsTextEdit(t *testing.T) {
	cfg := newTestSettingsCfg()
	so := NewSettingsOverlay(cfg, false, "")
	so.cursor = int(settingsFieldBranchPrefix)
	so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	closed, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.False(t, closed, "Esc from a text edit returns to browsing, not to the caller")
	assert.False(t, changed)
	assert.Equal(t, "aidan/", cfg.BranchPrefix)
	assert.Equal(t, settingsBrowsing, so.mode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./ui/overlay/... -run TestSettingsOverlayEdits -v`
Expected: FAIL — `applyTextEdit` is still the Task 2 stub that always returns `false`.

- [ ] **Step 3: Implement `applyTextEdit` fully**

In `ui/overlay/settingsOverlay.go`, replace the Task 2 stub:

```go
func (s *SettingsOverlay) applyTextEdit(field settingsField, value string) bool {
	return false
}
```

with:

```go
// applyTextEdit parses and stores value for field. Returns whether cfg
// changed; on a parse failure it records the error via s.lastErr
// (polled by TakeError) and leaves cfg untouched.
func (s *SettingsOverlay) applyTextEdit(field settingsField, value string) bool {
	switch field {
	case settingsFieldDefaultProgram:
		s.cfg.Mutate(func(c *config.Config) { c.DefaultProgram = value })
		return true
	case settingsFieldBranchPrefix:
		s.cfg.Mutate(func(c *config.Config) { c.BranchPrefix = value })
		return true
	case settingsFieldDaemonPollInterval:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n <= 0 {
			s.lastErr = fmt.Errorf("daemon poll interval must be a positive integer, got %q", value)
			return false
		}
		s.cfg.Mutate(func(c *config.Config) { c.DaemonPollInterval = n })
		return true
	}
	return false
}
```

Add `"strconv"` and `"strings"` to the import block.

**Amendment discovered running this step's tests:** `TestSettingsOverlayEditsDefaultProgram`/`EditsDaemonPollInterval` still failed after the above — `TextInputOverlay.HandleKeyPress`'s Enter case only sets `Submitted = true` when focus is on its Enter button (`isEnterButton()`), reached via Tab. A plain `NewTextInputOverlay` starts with focus on the textarea, where Enter just inserts a newline — correct for the multi-line prompt overlay this widget was built for, wrong for these single-line fields. Fix `handleEditingText` (from Task 2, Step 3) to own Enter/Esc directly instead of depending on `IsSubmitted()`/`Canceled`:

```go
// handleEditingText owns Enter/Esc directly rather than relying on
// TextInputOverlay's Tab-to-focus-the-Enter-button convention (built
// for the multi-line prompt overlay, where Enter on the textarea must
// insert a newline). These are single-line fields: Enter submits
// whatever's in the textarea immediately, Esc cancels. Any other key
// (typed runes, backspace, arrows) is forwarded to the embedded widget.
func (s *SettingsOverlay) handleEditingText(msg tea.KeyPressMsg) (closed, changed bool) {
	switch msg.Code {
	case tea.KeyEnter:
		value := s.editing.GetValue()
		field := s.editingField
		s.mode = settingsBrowsing
		s.editing = nil
		return false, s.applyTextEdit(field, value)
	case tea.KeyEsc:
		s.mode = settingsBrowsing
		s.editing = nil
		return false, false
	}
	s.editing.HandleKeyPress(msg)
	return false, false
}
```

This replaces the Task 2 version of `handleEditingText` entirely. **The same defect applies to Task 5's `ProfilesManager`** (`handleAddingName`/`handleAddingProgram`/`handleEditingProgram` all use the same `closedSub`/`IsSubmitted()` pattern) — apply the equivalent Enter/Esc-owned fix there too rather than reusing the code as originally written below; Task 5's code blocks in this document have NOT been retroactively corrected, so treat every `p.input.HandleKeyPress(msg); ... IsSubmitted() ...` occurrence in Task 5 as needing the same rewrite.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./ui/overlay/... -run TestSettingsOverlay -v`
Expected: PASS for every `TestSettingsOverlay*` test, including the four new ones.

- [ ] **Step 5: Write the integration test proving `m.program`/`m.autoYes` refresh**

Create `app/state_settings_test.go`:

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestHomeWithActiveCtx extends newTestHome with a resolved
// activeCtx, since handleStateSettingsKey needs ConfigDir to persist.
func newTestHomeWithActiveCtx(t *testing.T) *home {
	t.Helper()
	m := newTestHome(t)
	m.activeCtx = &config.WorkspaceContext{ConfigDir: t.TempDir()}
	m.program = m.appConfig.DefaultProgram
	m.autoYes = m.appConfig.AutoYes
	return m
}

func TestHandleStateSettingsKeyRefreshesShadowFields(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	so := overlay.NewSettingsOverlay(m.appConfig, false, "")
	m.setOverlay(so, overlaySettings)
	m.state = stateSettings

	// Default Program starts at whatever DefaultConfig resolved (an
	// absolute path GetClaudeCommand found on PATH, not a short fixed
	// string); edit it to a distinct value and confirm m.program
	// follows. The textarea pre-fills with the current value and
	// leaves the cursor at the end, so the existing text must be
	// cleared before typing or "aider" would land appended to it.
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // open edit on row 0 (Default Program)
	for i := 0; i < 128; i++ {
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range "aider" {
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // submit

	assert.Equal(t, "aider", m.appConfig.DefaultProgram)
	assert.Equal(t, "aider", m.program, "m.program must be refreshed, not left stale")

	// Move to Auto Yes (row 1) and toggle it.
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, m.appConfig.AutoYes)
	assert.True(t, m.autoYes, "m.autoYes must be refreshed, not left stale")
}

func TestHandleStateSettingsKeyPersistsToDisk(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	so := overlay.NewSettingsOverlay(m.appConfig, false, "")
	m.setOverlay(so, overlaySettings)
	m.state = stateSettings

	handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"}) // Auto Yes row
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})  // toggle on

	reloaded := config.LoadConfigFrom(m.activeCtx.ConfigDir)
	require.NotNil(t, reloaded)
	assert.True(t, reloaded.AutoYes, "the toggle must be persisted immediately, not only in memory")
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./app/... -run TestHandleStateSettingsKey -v`
Expected: PASS.

- [ ] **Step 7: Run the full suite**

Run: `go build ./... && go test ./... 2>&1 | tail -40`
Expected: PASS everywhere.

- [ ] **Step 8: Commit**

```bash
git add ui/overlay/settingsOverlay.go ui/overlay/settingsOverlay_test.go app/state_settings_test.go
git commit -m "$(cat <<'EOF'
feat(ui): support text-edit fields in the settings overlay

Default Program, Branch Prefix, and Daemon Poll Interval now open an
embedded TextInputOverlay on Enter; submit commits via Config.Mutate,
Esc cancels back to browsing. Daemon Poll Interval rejects non-numeric
input via TakeError rather than silently corrupting the value.
Confirms m.program/m.autoYes are refreshed and the change is persisted
to disk immediately.
EOF
)"
```

---

## Task 5: Profiles sub-screen

**Files:**
- Create (replace Task 2 stub): `ui/overlay/profilesManager.go`
- Test: `ui/overlay/profilesManager_test.go`

- [ ] **Step 1: Write the failing tests**

Create `ui/overlay/profilesManager_test.go`:

```go
package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func newTestProfilesCfg() *config.Config {
	return &config.Config{
		DefaultProgram: "alpha",
		Profiles: []config.Profile{
			{Name: "alpha", Program: "claude"},
			{Name: "beta", Program: "aider --model gpt-4"},
		},
	}
}

func typeText(t *testing.T, pm *ProfilesManager, text string) {
	t.Helper()
	for _, r := range text {
		pm.HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestProfilesManagerAdd(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'n', Text: "n"})
	typeText(t, pm, "gamma")
	pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}) // submit name, move to program
	typeText(t, pm, "gemini")
	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}) // submit program

	assert.True(t, changed)
	assert.Len(t, cfg.Profiles, 3)
	assert.Equal(t, config.Profile{Name: "gamma", Program: "gemini"}, cfg.Profiles[2])
}

func TestProfilesManagerEditProgram(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 1 // beta

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'e', Text: "e"})
	// Clear the pre-filled value first.
	for range "aider --model gpt-4" {
		pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(t, pm, "aider --model gpt-5")
	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.True(t, changed)
	assert.Equal(t, "aider --model gpt-5", cfg.Profiles[1].Program)
}

func TestProfilesManagerSetDefault(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 1 // beta

	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, changed)
	assert.Equal(t, "beta", cfg.DefaultProgram)
}

func TestProfilesManagerDelete(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 1 // beta, not the default

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'd', Text: "d"})
	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: 'y', Text: "y"})

	assert.True(t, changed)
	assert.Len(t, cfg.Profiles, 1)
	assert.Equal(t, "alpha", cfg.Profiles[0].Name)
}

func TestProfilesManagerDeleteCancelled(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 1

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'd', Text: "d"})
	_, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: 'n', Text: "n"})

	assert.False(t, changed)
	assert.Len(t, cfg.Profiles, 2)
}

func TestProfilesManagerBlocksDeletingDefault(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	pm.cursor = 0 // alpha, the default

	pm.HandleKeyPress(tea.KeyPressMsg{Code: 'd', Text: "d"})
	assert.Len(t, cfg.Profiles, 2, "delete must be blocked before entering confirm mode")
	assert.Error(t, pm.TakeError())
}

func TestProfilesManagerEscClosesFromBrowsing(t *testing.T) {
	cfg := newTestProfilesCfg()
	pm := NewProfilesManager(cfg)
	closed, changed := pm.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, closed)
	assert.False(t, changed)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./ui/overlay/... -run TestProfilesManager -v`
Expected: FAIL to compile — `TakeError`, real `HandleKeyPress` behavior, `cursor` field don't exist yet (Task 2's stub only handles Esc/q).

- [ ] **Step 3: Replace the stub with the full implementation**

Replace the entire contents of `ui/overlay/profilesManager.go`:

```go
package overlay

import (
	"fmt"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type profilesMode int

const (
	profilesBrowsing profilesMode = iota
	profilesAddingName
	profilesAddingProgram
	profilesEditingProgram
	profilesConfirmingDelete
)

// ProfilesManager is the Profiles drill-in sub-screen: add/edit-program/
// delete/set-default over cfg.Profiles. It operates on the raw
// cfg.Profiles slice directly (not config.Config.GetProfiles(), which
// synthesizes a row from DefaultProgram when Profiles is empty) — the
// Default Program row on the parent SettingsOverlay is the single
// source of truth for "what runs by default"; this screen only manages
// the named list. Renaming an existing profile isn't supported in v1 —
// only its program string — since that's the field users actually need
// to tweak (e.g. a model flag).
type ProfilesManager struct {
	cfg    *config.Config
	cursor int
	width  int

	mode        profilesMode
	input       *TextInputOverlay
	pendingName string

	lastErr error
}

// NewProfilesManager creates the Profiles sub-screen over cfg.
func NewProfilesManager(cfg *config.Config) *ProfilesManager {
	return &ProfilesManager{cfg: cfg, width: 60}
}

// SetWidth propagates the available width to any embedded text input.
func (p *ProfilesManager) SetWidth(w int) {
	p.width = w
	if p.input != nil {
		p.input.SetSize(w, 3)
	}
}

// TakeError returns and clears the last validation/precondition error
// (e.g. deleting the default profile). Callers poll this after
// HandleKeyPress.
func (p *ProfilesManager) TakeError() error {
	err := p.lastErr
	p.lastErr = nil
	return err
}

// HandleKeyPress processes one key press. closed reports whether the
// sub-screen should return control to the parent SettingsOverlay;
// changed reports whether cfg was mutated.
func (p *ProfilesManager) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	p.lastErr = nil
	switch p.mode {
	case profilesAddingName:
		return p.handleAddingName(msg)
	case profilesAddingProgram:
		return p.handleAddingProgram(msg)
	case profilesEditingProgram:
		return p.handleEditingProgram(msg)
	case profilesConfirmingDelete:
		return p.handleConfirmingDelete(msg)
	}

	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if len(p.cfg.Profiles) > 0 && p.cursor < len(p.cfg.Profiles)-1 {
			p.cursor++
		}
	case "esc", "q":
		return true, false
	case "n":
		p.mode = profilesAddingName
		p.input = NewTextInputOverlay("Profile name", "")
		p.input.SetSize(p.width, 3)
	case "e", "enter":
		if len(p.cfg.Profiles) == 0 {
			return false, false
		}
		p.mode = profilesEditingProgram
		p.input = NewTextInputOverlay("Program command", p.cfg.Profiles[p.cursor].Program)
		p.input.SetSize(p.width, 3)
	case " ", "space":
		if len(p.cfg.Profiles) == 0 {
			return false, false
		}
		name := p.cfg.Profiles[p.cursor].Name
		p.cfg.Mutate(func(c *config.Config) { c.DefaultProgram = name })
		return false, true
	case "d":
		if len(p.cfg.Profiles) == 0 {
			return false, false
		}
		if p.cfg.Profiles[p.cursor].Name == p.cfg.DefaultProgram {
			p.lastErr = fmt.Errorf("can't delete %q: it's the current Default Program — change that first", p.cfg.Profiles[p.cursor].Name)
			return false, false
		}
		p.mode = profilesConfirmingDelete
	}
	return false, false
}

func (p *ProfilesManager) handleAddingName(msg tea.KeyPressMsg) (closed, changed bool) {
	closedSub, _ := p.input.HandleKeyPress(msg)
	if !closedSub {
		return false, false
	}
	if !p.input.IsSubmitted() {
		p.mode = profilesBrowsing
		p.input = nil
		return false, false
	}
	p.pendingName = p.input.GetValue()
	p.mode = profilesAddingProgram
	p.input = NewTextInputOverlay("Program command", "")
	p.input.SetSize(p.width, 3)
	return false, false
}

func (p *ProfilesManager) handleAddingProgram(msg tea.KeyPressMsg) (closed, changed bool) {
	closedSub, _ := p.input.HandleKeyPress(msg)
	if !closedSub {
		return false, false
	}
	submitted := p.input.IsSubmitted()
	program := p.input.GetValue()
	name := p.pendingName
	p.mode = profilesBrowsing
	p.input = nil
	p.pendingName = ""
	if !submitted {
		return false, false
	}
	p.cfg.Mutate(func(c *config.Config) {
		c.Profiles = append(c.Profiles, config.Profile{Name: name, Program: program})
	})
	p.cursor = len(p.cfg.Profiles) - 1
	return false, true
}

func (p *ProfilesManager) handleEditingProgram(msg tea.KeyPressMsg) (closed, changed bool) {
	closedSub, _ := p.input.HandleKeyPress(msg)
	if !closedSub {
		return false, false
	}
	submitted := p.input.IsSubmitted()
	program := p.input.GetValue()
	idx := p.cursor
	p.mode = profilesBrowsing
	p.input = nil
	if !submitted {
		return false, false
	}
	p.cfg.Mutate(func(c *config.Config) { c.Profiles[idx].Program = program })
	return false, true
}

func (p *ProfilesManager) handleConfirmingDelete(msg tea.KeyPressMsg) (closed, changed bool) {
	switch msg.String() {
	case "y":
		idx := p.cursor
		p.cfg.Mutate(func(c *config.Config) {
			c.Profiles = append(c.Profiles[:idx], c.Profiles[idx+1:]...)
		})
		if p.cursor >= len(p.cfg.Profiles) && p.cursor > 0 {
			p.cursor--
		}
		p.mode = profilesBrowsing
		return false, true
	case "n", "esc":
		p.mode = profilesBrowsing
		return false, false
	}
	return false, false
}

var (
	profilesTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	profilesSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	profilesNormalStyle   = lipgloss.NewStyle().Foreground(ui.TextPrimary)
	profilesHintStyle     = lipgloss.NewStyle().Foreground(ui.TextHint)
)

// Render renders whichever mode is active.
func (p *ProfilesManager) Render() string {
	switch p.mode {
	case profilesAddingName, profilesAddingProgram, profilesEditingProgram:
		return p.input.Render()
	}

	content := profilesTitleStyle.Render("Profiles") + "\n\n"
	if len(p.cfg.Profiles) == 0 {
		content += profilesNormalStyle.Render("No profiles yet — press 'n' to add one") + "\n"
	}
	for i, prof := range p.cfg.Profiles {
		cursor := "  "
		if i == p.cursor {
			cursor = "> "
		}
		mark := "  "
		if prof.Name == p.cfg.DefaultProgram {
			mark = "* "
		}
		line := fmt.Sprintf("%s%s%-16s %s", cursor, mark, prof.Name, prof.Program)
		if i == p.cursor {
			content += profilesSelectedStyle.Render(line) + "\n"
		} else {
			content += profilesNormalStyle.Render(line) + "\n"
		}
	}
	if p.mode == profilesConfirmingDelete {
		content += "\n" + profilesHintStyle.Render(fmt.Sprintf("Delete %q? y/n", p.cfg.Profiles[p.cursor].Name))
	} else {
		content += "\n" + profilesHintStyle.Render("n add • e/enter edit • d delete • space set default • esc back")
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(p.width)
	return border.Render(content)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./... && go test ./ui/overlay/... -run TestProfilesManager -v`
Expected: PASS for all seven tests.

- [ ] **Step 5: Run the full overlay suite**

Run: `go test ./ui/overlay/... -v 2>&1 | tail -60`
Expected: PASS, no regressions in `TestSettingsOverlay*`.

- [ ] **Step 6: Commit**

```bash
git add ui/overlay/profilesManager.go ui/overlay/profilesManager_test.go
git commit -m "$(cat <<'EOF'
feat(ui): implement the Profiles CRUD sub-screen

Add/edit-program/delete/set-default over cfg.Profiles, all committed
via Config.Mutate. Deleting the profile that's the current Default
Program is blocked with an error rather than silently orphaning it;
delete otherwise requires an inline y/n confirmation.
EOF
)"
```

---

## Task 6: Claude Preferences sub-screen

**Files:**
- Create (replace Task 2 stub): `ui/overlay/claudePreferences.go`
- Test: `ui/overlay/claudePreferences_test.go`
- Modify: `app/intents.go` (already passes `m.rcAuth` since Task 3 — verify here)

- [ ] **Step 1: Write the failing tests**

Create `ui/overlay/claudePreferences_test.go`:

```go
package overlay

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

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

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./ui/overlay/... -run TestClaudePreferences -v`
Expected: FAIL — Task 2's stub always returns `false, false` from `HandleKeyPress` and an empty `Render()`.

- [ ] **Step 3: Replace the stub with the full implementation**

Replace the entire contents of `ui/overlay/claudePreferences.go`:

```go
package overlay

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ClaudePreferences is the Claude-specific preferences drill-in
// sub-screen. Structured as its own screen (rather than a flat row on
// the main settings list) so more Claude-adapter-specific preferences
// can be added later without growing that list — today it holds one
// row.
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
}

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
	case " ", "space", "enter":
		c.cfg.Mutate(func(cc *config.Config) {
			v := !cc.RemoteControlEnabled()
			cc.ClaudeRemoteControl = &v
		})
		return false, true
	}
	return false, false
}

var (
	claudePrefsTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	claudePrefsRowStyle    = lipgloss.NewStyle().Foreground(ui.TextPrimary)
	claudePrefsHintStyle   = lipgloss.NewStyle().Foreground(ui.TextHint)
	claudePrefsBlockedText = lipgloss.NewStyle().Foreground(ui.DangerAccent)
)

// Render renders the sub-screen.
func (c *ClaudePreferences) Render() string {
	check := "[ ]"
	if c.cfg.RemoteControlEnabled() {
		check = "[x]"
	}
	row := claudePrefsRowStyle.Render("Remote Control    " + check)
	if c.authBlocked {
		row += "  " + claudePrefsBlockedText.Render("(blocked: "+c.authReason+")")
	}

	content := claudePrefsTitleStyle.Render("Claude Preferences") + "\n\n" +
		row + "\n\n" +
		claudePrefsHintStyle.Render("enter/space toggle • esc back")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(c.width)
	return border.Render(content)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./... && go test ./ui/overlay/... -run TestClaudePreferences -v`
Expected: PASS for all three tests.

- [ ] **Step 5: Verify `runOpenSettings` already threads `m.rcAuth` correctly**

Open `app/intents.go` and confirm `runOpenSettings` (added in Task 3, Step 9) reads:

```go
so := overlay.NewSettingsOverlay(m.appConfig, m.rcAuth.Blocked(), m.rcAuth.Reason)
```

No change needed if so — this step is a verification checkpoint, not an edit. If the signature drifted, fix the call to match `NewSettingsOverlay(cfg *config.Config, authBlocked bool, authReason string)`.

- [ ] **Step 6: Manually verify end-to-end via the migration-parity-style harness**

Add a temporary throwaway test to confirm the full chain from `S` down to the Claude Preferences toggle (delete after confirming — this step exists to catch integration mistakes the unit tests above can't, since they construct `SettingsOverlay`/`ClaudePreferences` directly rather than through `runOpenSettings`):

Run this as an ad hoc check (do not commit a file for it — verify inline, e.g. via `go run` a small `_ = ` snippet, or temporarily paste into `app/state_settings_test.go` and remove after passing):

```go
func TestSettingsDrillsIntoClaudePreferences(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	_, _ = runOpenSettings(m)

	so := m.settingsOverlay()
	require.NotNil(t, so)

	// Row 5 is Claude Preferences.
	for i := 0; i < 5; i++ {
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // drill in
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // toggle remote control off

	assert.False(t, m.appConfig.RemoteControlEnabled())
}
```

This does not need to be a throwaway — keep it as a permanent addition to `app/state_settings_test.go` if it passes; it is a genuine integration test, not scaffolding. Add `"github.com/aidan-bailey/loom/session"` to that file's imports.

Run: `go test ./app/... -run TestSettingsDrillsIntoClaudePreferences -v`
Expected: PASS.

- [ ] **Step 7: Run the full suite**

Run: `go build ./... && go test ./... 2>&1 | tail -60`
Expected: PASS everywhere.

- [ ] **Step 8: Commit**

```bash
git add ui/overlay/claudePreferences.go ui/overlay/claudePreferences_test.go app/state_settings_test.go
git commit -m "$(cat <<'EOF'
feat(ui): implement the Claude Preferences sub-screen

Single Remote Control toggle for now, structured as its own drill-in
screen so more Claude-adapter-specific settings can be added later.
Shows the cached startup-time auth-blocked hint (m.rcAuth) without any
new auth probing; toggling only changes RemoteControlEnabled(), which
existing session-creation-time gating already reads fresh.
EOF
)"
```

---

## Task 7: Daemon poll-interval reload

**Files:**
- Modify: `daemon/daemon.go`
- Test: `daemon/daemon_reload_test.go`

- [ ] **Step 1: Write the failing test**

Add to `daemon/daemon_reload_test.go`:

```go
func TestRefreshPollInterval_PicksUpChange(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DaemonPollInterval = 1000
	require.NoError(t, config.SaveConfigTo(cfg, dir))

	got := refreshPollInterval(dir, 999*time.Millisecond)
	assert.Equal(t, 1000*time.Millisecond, got)

	cfg.DaemonPollInterval = 2500
	require.NoError(t, config.SaveConfigTo(cfg, dir))

	got = refreshPollInterval(dir, 1000*time.Millisecond)
	assert.Equal(t, 2500*time.Millisecond, got)
}

func TestRefreshPollInterval_FallsBackOnMissingConfig(t *testing.T) {
	dir := t.TempDir() // no config.json written
	got := refreshPollInterval(dir, 1500*time.Millisecond)
	// LoadConfigFrom falls back to DefaultConfig() (1000ms) when no file
	// exists, so the fallback path here is really "non-positive value",
	// not "missing file" — assert the actual DefaultConfig behavior.
	assert.Equal(t, time.Duration(config.DefaultConfig().DaemonPollInterval)*time.Millisecond, got)
}
```

Add `"time"` and `"github.com/stretchr/testify/require"` to the test file's imports if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./daemon/... -run TestRefreshPollInterval -v`
Expected: FAIL — `refreshPollInterval` undefined.

- [ ] **Step 3: Implement `refreshPollInterval` and wire it into the tick loop**

In `daemon/daemon.go`, add after `reloadInstanceData`:

```go
// refreshPollInterval re-reads configDir's config.json and returns the
// poll interval it specifies, or fallback if the config can't be loaded
// or specifies a non-positive interval. Called every tick so a
// settings-menu edit to DaemonPollInterval reaches the already-running
// daemon process without a restart — the daemon has no other way to
// observe a config.json written by a different process.
func refreshPollInterval(configDir string, fallback time.Duration) time.Duration {
	cfg := config.LoadConfigFrom(configDir)
	if cfg == nil || cfg.DaemonPollInterval <= 0 {
		return fallback
	}
	return time.Duration(cfg.DaemonPollInterval) * time.Millisecond
}
```

Then in `RunDaemon`'s tick loop, change:

```go
			for {
				fresh, err := reloadInstanceData(configDir)
				if err != nil {
					if everyN.ShouldLog() {
						dlog.Warn("daemon.reload_failed", "err", err.Error())
					}
				} else {
					syncTracked(tracked, fresh, configDir, everyN)
				}
```

to:

```go
			for {
				pollInterval = refreshPollInterval(configDir, pollInterval)

				fresh, err := reloadInstanceData(configDir)
				if err != nil {
					if everyN.ShouldLog() {
						dlog.Warn("daemon.reload_failed", "err", err.Error())
					}
				} else {
					syncTracked(tracked, fresh, configDir, everyN)
				}
```

`pollInterval` is the existing closure-captured variable declared before the goroutine (`daemon/daemon.go:225`); reassigning it here means the existing `ticker.Reset(pollInterval)` call later in the same loop iteration picks up the new value on the very next tick. No other change to the loop is needed.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./daemon/... -run TestRefreshPollInterval -v`
Expected: PASS.

- [ ] **Step 5: Run the full daemon suite**

Run: `go test ./daemon/... -v 2>&1 | tail -60`
Expected: PASS, no regression in `daemon_sync_test.go`/`daemon_parallel_test.go`/etc.

- [ ] **Step 6: Commit**

```bash
git add daemon/daemon.go daemon/daemon_reload_test.go
git commit -m "$(cat <<'EOF'
feat(daemon): reload DaemonPollInterval every tick

The daemon is a separate OS process that previously cached
DaemonPollInterval once at startup. refreshPollInterval re-reads
config.json each tick (same pattern reloadInstanceData already uses
for state.json) so a settings-menu edit reaches the running daemon
without a restart.
EOF
)"
```

---

## Task 8: Documentation and final verification

**Files:**
- Modify: `USAGE.md`, `CLAUDE.md`

- [ ] **Step 1: Add the keybinding to `CLAUDE.md`**

In `CLAUDE.md`'s `## TUI Keybindings` table, add a row after `| \`W\` | Workspace picker |`:

```markdown
| `S` | Open settings |
```

- [ ] **Step 2: Add the keybinding to `USAGE.md`**

In `USAGE.md`'s keybinding table, add a row after `| \`W\` | Open workspace picker |`:

```markdown
| `S` | Open settings (edit config.json: Default Program, Auto Yes, Daemon Poll Interval, Branch Prefix, Profiles, Claude Preferences) |
```

- [ ] **Step 3: Format and lint**

Run: `gofmt -l .`
Expected: no output (nothing to format). If files are listed, run `gofmt -w .` and re-check.

Run: `golangci-lint run --timeout=3m --fast`
Expected: no new findings introduced by this feature's files.

- [ ] **Step 4: Full test suite, including race detector**

Run: `go test ./... 2>&1 | tail -80`
Expected: PASS across every package.

Run: `CGO_ENABLED=1 go test -race ./config/... ./app/... ./ui/overlay/... ./daemon/... ./script/... 2>&1 | tail -80`
Expected: PASS, no race detected — this specifically exercises Task 1's fix under the conditions it was designed for (concurrent `Mutate` + `GetBranchPrefix`/`scriptHost.BranchPrefix`).

- [ ] **Step 5: Manual smoke test**

Run: `CGO_ENABLED=0 go build -o loom && ./loom`

In the running TUI:
1. Press `S` — the settings overlay opens showing all six rows.
2. Navigate with `j`/`k`, toggle Auto Yes with `Enter`, confirm the `[x]`/`[ ]` flips.
3. Edit Branch Prefix, confirm the new value renders on the row after submit.
4. Drill into Profiles (`Enter` on that row), add a profile with `n`, confirm it appears in the list, delete it with `d` + `y`.
5. Drill into Claude Preferences, toggle Remote Control, confirm the checkbox flips.
6. Press `Esc` from the main list to close; press `S` again to confirm the overlay reopens with the edited values still in place.
7. Quit (`q`) and re-open Loom; press `S` again and confirm every edit persisted across restart (reading straight from `config.json` in the workspace's config dir is an acceptable substitute for this check if faster: `cat <configDir>/config.json`).

- [ ] **Step 6: Commit**

```bash
git add USAGE.md CLAUDE.md
git commit -m "$(cat <<'EOF'
docs: document the settings menu keybinding

EOF
)"
```

---

## Self-Review Notes

- **Spec coverage:** every spec section has a task — entry point (Task 3), main overlay + scalar edits (Tasks 2, 4), Profiles (Task 5), Claude Preferences (Task 6), persistence/concurrency (Tasks 1, 3, 4), daemon reload (Task 7), the in-process staleness correction (Task 3 Step 8, Task 4 Step 5 test), docs (Task 8).
- **Type consistency:** `settingsField`/`settingsMode`/`profilesMode` enums and `SettingsOverlay`/`ProfilesManager`/`ClaudePreferences` method signatures (`HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool)`) are introduced once in Task 2 and reused verbatim through Tasks 4–6; `NewSettingsOverlay(cfg *config.Config, authBlocked bool, authReason string)`'s signature is fixed in Task 2 and never changes at its Task 3/6 call sites.
- **Placeholder scan:** the only intentionally temporary code is the Task 2 Step 4 stub files, which are explicitly replaced in full by Tasks 5–6 — flagged inline as such, not left as a silent gap.
