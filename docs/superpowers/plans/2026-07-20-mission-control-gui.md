# Mission Control GUI Implementation Plan (Phases 1–3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement phases 1–3 of `docs/superpowers/specs/2026-07-20-mission-control-gui-design.md`: a semantic theme system defaulting to Afterglow, a live mini-card rail with split-pane controls in focus mode, and a card-grid overview mode toggled with `tab`.

**Architecture:** A theme registry in `ui/theme.go` exposes semantic color-role vars rebuilt via registered hooks (package-init styles would otherwise capture pre-theme colors). A pure card component (`ui/card.go`) renders instances at three densities; the list becomes a rail of mini-cards, and a new `ui/overview.go` renders the card grid. A `viewMode` field on `home` (orthogonal to the `state` machine) gates View/key routing. New keys are Lua actions following the existing sync-primitive pattern (Host method → `deferModelMutation` → `installSyncActions` → `defaults.lua`).

**Tech Stack:** Go 1.23, Bubble Tea v2 (`charm.land/bubbletea/v2`), lipgloss v2 (+ `compat`), `github.com/charmbracelet/x/ansi` (already in go.mod), gopher-lua, testify.

---

## Required background (read before Task 1)

- **Build/test:** `CGO_ENABLED=0 go build -o loom` · `CGO_ENABLED=0 go test ./<pkg>/ -v` · race: `CC=clang CGO_ENABLED=1 go test -race ./...`. Format with `gofmt -w .`. Lint with `go vet ./...` (local golangci-lint is v2 and incompatible with the repo config — do NOT run it).
- **Concurrency rules (CLAUDE.md):** no model mutation from `tea.Cmd` goroutines. Script sync primitives record mutations via `scriptHost.deferModelMutation` (`app/app_scripts.go:152`) and are applied in `handleScriptDone`. `handleScriptDone` calls `m.instanceChanged()` unconditionally after applying deferred actions — new primitives get a refresh for free.
- **Key dispatch:** `app/state_default.go:handleStateDefaultKey` → `m.dispatchScript(key)` → Lua engine. Bindings live in `script/defaults.lua` (embedded). Adding a `script.Host` method requires updating: `script/host.go` (interface), `app/app_scripts.go` (impl), `script/api_actions.go:installSyncActions` (Lua glue), `script/host_fake_test.go` (test fake), `script/defaults.lua` (binding).
- **Slots:** `home.slots []workspaceSlot` keeps every activated workspace's list/storage/state loaded simultaneously (`app/app.go:141-153,267-270`). The focused slot's fields are copied onto `home` (`loadSlot`/`saveCurrentSlot`). Peer-workspace summaries come from `m.slots[i].list` — live data, no new plumbing.
- **Panes:** `Instance.Preview()` (`session/instance.go:746`) returns the live screen from the in-process emulator (read-locked; empty string for not-started/paused). Safe to call per-render.
- **Sizing:** lipgloss `.Width`/`.Height` are TOTAL box size (border-inclusive), and `.Height` is a min not a cap — always clamp rows (see `ui/split_pane.go:clampHeight`).
- **Spec deviation (approved rationale):** `[`/`]` currently cycle workspaces; this plan rebinds them to prev/next-waiting and moves workspace cycling to `{`/`}` (`l`/`;` remain). Overview grid nav is linear `j`/`k` over the attention-sorted order (no `h`/`l` columns; `l` stays workspace-prev).

## File structure

| File | Role |
|---|---|
| `ui/theme.go` (rewrite) | Theme struct, registry (afterglow/legacy), role vars, `ApplyTheme`, `RegisterThemeHook` |
| `ui/theme_test.go` (new) | registry/fallback/hook tests |
| `ui/card.go` + `ui/card_test.go` (new) | `CardData`, `CardDensity`, `RenderCard`, `BuildCardData`, `TailLines`, `SortForOverview`, `PeerSection` |
| `ui/overview.go` + `ui/overview_test.go` (new) | card-grid overview component |
| `ui/list.go` | rail rendering (cards replace rows), peer sections, `SelectedIdx` |
| `ui/split_pane.go` | adjustable ratio, hideable terminal, `AgentContentHeight` |
| `ui/consts.go` | rail layout consts |
| `config/config.go` | `Theme` field + `GetTheme` |
| `config/state.go` | `UIPrefs` + `AppState` getters/setters |
| `session/instance.go` | `statusChangedAt` + `StatusAge` |
| `script/host.go`, `script/api_actions.go`, `script/host_fake_test.go`, `script/defaults.lua` | new actions/bindings |
| `app/app.go` | theme at startup, `viewMode`, overview wiring, prefs load/save, peer sections |
| `app/app_scripts.go` | new Host impls, mode-aware cursor |
| `app/state_default.go` | overview key routing |
| Styles migration | `ui/{list,menu,diff,err,quick_input,workspace_tab_bar,preview,terminal,split_pane}.go`, `ui/overlay/*.go`, `app/{app,help}.go` |

---

# Phase 1 — Theme system + restyle

### Task 1: Theme registry in `ui/theme.go`

**Files:**
- Rewrite: `ui/theme.go`
- Create: `ui/theme_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// ui/theme_test.go
package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyTheme_KnownName(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	ok := ApplyTheme("legacy")
	assert.True(t, ok)
	assert.Equal(t, "legacy", CurrentThemeName())
}

func TestApplyTheme_UnknownFallsBackToDefault(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	ok := ApplyTheme("no-such-theme")
	assert.False(t, ok)
	assert.Equal(t, DefaultThemeName, CurrentThemeName())
}

func TestApplyTheme_EmptyIsDefault(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	ok := ApplyTheme("")
	assert.False(t, ok)
	assert.Equal(t, DefaultThemeName, CurrentThemeName())
}

func TestApplyTheme_ReassignsRoleVars(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	ApplyTheme(DefaultThemeName)
	afterglowAccent := Accent
	ApplyTheme("legacy")
	assert.NotEqual(t, afterglowAccent, Accent)
}

func TestRegisterThemeHook_RunsImmediatelyAndOnApply(t *testing.T) {
	defer ApplyTheme(DefaultThemeName)
	calls := 0
	RegisterThemeHook(func() { calls++ })
	assert.Equal(t, 1, calls, "hook must run once at registration")
	ApplyTheme("legacy")
	assert.Equal(t, 2, calls, "hook must re-run on ApplyTheme")
}

func TestThemeNames_SortedAndComplete(t *testing.T) {
	names := ThemeNames()
	assert.Equal(t, []string{"afterglow", "legacy"}, names)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'TestApplyTheme|TestRegisterThemeHook|TestThemeNames' -v`
Expected: FAIL (undefined: ApplyTheme, DefaultThemeName, …)

- [ ] **Step 3: Rewrite `ui/theme.go`**

Replace the entire file. The old vars (`BorderActive`, `TextDim`, `TitleAccent`, `HeaderAccent`, `KeyHighlight`, `TextPrimary`, `TextHint`, `SelectionBg`, `SelectionFg`, `DangerAccent`, `BorderMuted`, `OverlayBorder`, `OverlaySelectedFg`, `OverlayItemFg`, `OverlayHintFg`) are deleted; Tasks 3–4 migrate their references. This will not compile until Task 4 completes — Tasks 1–4 are committed together at the end of Task 4 (test Steps run per-task via `go test ./ui/ -run …` will also not link until then; that is expected — verify test failures by reading the code once, then verify passes at Task 4 Step 5).

```go
// Package-file ui/theme.go
package ui

import (
	"image/color"
	"sort"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

// Theme is a named palette of semantic color roles. Components never
// reference literal colors — they reference the package-level role vars
// below, which ApplyTheme reassigns. Styles built from roles at package
// init would capture stale values, so any package-level lipgloss.Style
// derived from a role must be (re)built inside a RegisterThemeHook
// callback (see rebuildListStyles in list.go for the pattern).
type Theme struct {
	Name string

	// Surfaces and text.
	Ground color.Color // full-bleed background (reserved; no style paints it yet)
	Panel  color.Color // raised-surface background (workspace-terminal rows, selected cards)
	Text   color.Color // primary foreground
	Dim    color.Color // secondary labels, hints
	Faint  color.Color // barely-there text (aux hints, disabled)
	Rule   color.Color // unfocused borders, separators

	// Accents. Attention is reserved for "an agent needs the user" —
	// nothing else may use it, so a calm screen has no loud color.
	Accent    color.Color // focus, activity, selected borders
	Attention color.Color // needs-input / bell — the ONLY loud color
	Highlight color.Color // key glyphs, scroll footers
	OK        color.Color // ready status, added lines
	Error     color.Color // errors, removed lines, destructive accents
	Workspace color.Color // workspace labels, workspace-terminal identity
	Info      color.Color // informational text, hunk headers, running tabs

	// Selection (list rows, pickers).
	SelectionBg color.Color
	SelectionFg color.Color
}

// DefaultThemeName is applied at startup when config names no theme (or
// an unknown one).
const DefaultThemeName = "afterglow"

var themes = map[string]Theme{
	// Palette lifted from Afterglow.tmTheme
	// (github.com/Yabatadesign/afterglow-theme).
	"afterglow": {
		Name:        "afterglow",
		Ground:      lipgloss.Color("#2E2E2E"),
		Panel:       lipgloss.Color("#333435"),
		Text:        lipgloss.Color("#d6d6d6"),
		Dim:         lipgloss.Color("#797979"),
		Faint:       lipgloss.Color("#505050"),
		Rule:        lipgloss.Color("#404040"),
		Accent:      lipgloss.Color("#6c99bb"),
		Attention:   lipgloss.Color("#e5b567"),
		Highlight:   lipgloss.Color("#FFC66D"),
		OK:          lipgloss.Color("#b4c973"),
		Error:       lipgloss.Color("#c45330"),
		Workspace:   lipgloss.Color("#a1617a"),
		Info:        lipgloss.Color("#6D9CBE"),
		SelectionBg: lipgloss.Color("#5A647E"),
		SelectionFg: lipgloss.Color("#FFFFFF"),
	},
	// The pre-theme-system look, preserved as an escape hatch. Minor
	// consolidations (documented in the design doc): HeaderAccent
	// #36CFC9→Accent, KeyHighlight #FFCC00→Highlight #FFD700, deleting
	// #cc6666→Error, recoverable #d19a66→Attention, menu ANSI 205/99→
	// Accent/Workspace, err #FF0000→Error, diff greens/reds/blues→
	// OK/Error/Info, file-explorer #7c7cff→Accent.
	"legacy": {
		Name:        "legacy",
		Ground:      compat.AdaptiveColor{Light: lipgloss.Color("#ffffff"), Dark: lipgloss.Color("#000000")},
		Panel:       compat.AdaptiveColor{Light: lipgloss.Color("#e8e0f0"), Dark: lipgloss.Color("#2d2640")},
		Text:        compat.AdaptiveColor{Light: lipgloss.Color("#1a1a1a"), Dark: lipgloss.Color("#dddddd")},
		Dim:         compat.AdaptiveColor{Light: lipgloss.Color("#999999"), Dark: lipgloss.Color("#666666")},
		Faint:       lipgloss.Color("#777777"),
		Rule:        compat.AdaptiveColor{Light: lipgloss.Color("#999999"), Dark: lipgloss.Color("#555555")},
		Accent:      compat.AdaptiveColor{Light: lipgloss.Color("#874BFD"), Dark: lipgloss.Color("#7D56F4")},
		Attention:   lipgloss.Color("#e5c07b"),
		Highlight:   lipgloss.Color("#FFD700"),
		OK:          lipgloss.Color("#51bd73"),
		Error:       lipgloss.Color("#de613e"),
		Workspace:   lipgloss.Color("#6c71c4"),
		Info:        lipgloss.Color("#7AA2F7"),
		SelectionBg: lipgloss.Color("#dde4f0"),
		SelectionFg: lipgloss.Color("#1a1a1a"),
	},
}

// current is the active theme; role vars below mirror its fields.
var current = themes[DefaultThemeName]

// Active color roles. Reassigned (main goroutine only: startup, or the
// settings overlay's Update-goroutine handler) by ApplyTheme.
var (
	Ground      = current.Ground
	Panel       = current.Panel
	Text        = current.Text
	Dim         = current.Dim
	Faint       = current.Faint
	Rule        = current.Rule
	Accent      = current.Accent
	Attention   = current.Attention
	Highlight   = current.Highlight
	OK          = current.OK
	ErrorColor  = current.Error
	Workspace   = current.Workspace
	Info        = current.Info
	SelectionBg = current.SelectionBg
	SelectionFg = current.SelectionFg
)

// themeHooks are style-rebuild callbacks. Each runs once at
// registration (so derived styles are never zero) and again on every
// ApplyTheme.
var themeHooks []func()

// RegisterThemeHook registers fn to rebuild theme-derived styles and
// invokes it immediately. Call from init() in the file that owns the
// styles. Main-goroutine only.
func RegisterThemeHook(fn func()) {
	themeHooks = append(themeHooks, fn)
	fn()
}

// ApplyTheme activates the named theme, reassigns every role var, and
// re-runs all registered hooks. Unknown or empty names activate the
// default theme and return false. Main-goroutine only.
func ApplyTheme(name string) bool {
	t, ok := themes[name]
	if !ok {
		t = themes[DefaultThemeName]
	}
	current = t
	Ground, Panel, Text, Dim, Faint, Rule = t.Ground, t.Panel, t.Text, t.Dim, t.Faint, t.Rule
	Accent, Attention, Highlight, OK, ErrorColor = t.Accent, t.Attention, t.Highlight, t.OK, t.Error
	Workspace, Info, SelectionBg, SelectionFg = t.Workspace, t.Info, t.SelectionBg, t.SelectionFg
	for _, fn := range themeHooks {
		fn()
	}
	return ok
}

// CurrentThemeName returns the active theme's name.
func CurrentThemeName() string { return current.Name }

// ThemeNames returns all registered theme names, sorted.
func ThemeNames() []string {
	names := make([]string, 0, len(themes))
	for n := range themes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
```

Note the role var is `ErrorColor`, not `Error` — `ui.Error` would read like an error value at call sites.

- [ ] **Step 4: Do NOT run tests or commit yet** — `ui/` no longer compiles (old var references). Proceed to Task 2.

### Task 2: `Theme` config field

**Files:**
- Modify: `config/config.go` (Config struct ~line 74; `DefaultConfig`; accessor near `GetBranchPrefix`)
- Test: `config/config_test.go` (or create if absent — check with `ls config/*_test.go`)

- [ ] **Step 1: Write the failing test**

```go
func TestConfigTheme_DefaultAndAccessor(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, "afterglow", cfg.GetTheme())

	cfg.Mutate(func(c *Config) { c.Theme = "legacy" })
	assert.Equal(t, "legacy", cfg.GetTheme())

	// Pre-existing config files have no theme field: empty passes
	// through; ui.ApplyTheme treats "" as the default.
	var zero Config
	assert.Equal(t, "", zero.GetTheme())
}
```

Match the surrounding test file's package/imports; `Mutate` and `DefaultConfig` already exist.

- [ ] **Step 2: Run it** — `CGO_ENABLED=0 go test ./config/ -run TestConfigTheme -v` — Expected: FAIL (no field `Theme`)

- [ ] **Step 3: Implement**

In the `Config` struct, after `ClaudePermissionMode`:

```go
	// Theme names the active UI color theme (see ui.ThemeNames).
	// Empty (pre-existing config files) means the default theme.
	// Read through GetTheme.
	Theme string `json:"theme,omitempty"`
```

In `DefaultConfig()` add `Theme: "afterglow",`. Next to `GetBranchPrefix`:

```go
// GetTheme returns the configured UI theme name under the config lock
// (the settings overlay mutates Config at runtime).
func (c *Config) GetTheme() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Theme
}
```

- [ ] **Step 4: Run it** — same command — Expected: PASS
- [ ] **Step 5: Commit** — `git add config/ && git commit -m "feat(config): add Theme field with locked accessor"` (append the standard `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>` trailer to every commit in this plan)

### Task 3: Apply theme at startup

**Files:**
- Modify: `app/app.go` — `Run` (line ~59)

- [ ] **Step 1: Implement** — at the top of `func Run(...)`, before `newHome` is called:

```go
	// Activate the configured theme before any component renders.
	// Package-init styles are theme-hooked (ui.RegisterThemeHook), so
	// this rebuild-on-apply is what makes config-selected themes stick.
	themeName := ""
	if appConfig != nil {
		themeName = appConfig.GetTheme()
	}
	if !ui.ApplyTheme(themeName) && themeName != "" {
		log.For("ui").Warn("unknown_theme", "name", themeName, "fallback", ui.DefaultThemeName)
	}
```

(`ui` and `log` are already imported in app.go.)

- [ ] **Step 2: Do not commit yet** — still mid-migration; continues in Task 4.

### Task 4: Migrate all `ui/` package styles to roles + hooks

**Files:**
- Modify: `ui/list.go`, `ui/split_pane.go`, `ui/menu.go`, `ui/diff.go`, `ui/err.go`, `ui/quick_input.go`, `ui/workspace_tab_bar.go`, `ui/preview.go`, `ui/terminal.go`

Every package-level `lipgloss.Style` that bakes in a color moves into a per-file `rebuild<File>Styles()` registered via `init() { RegisterThemeHook(...) }`. The var declarations stay (as bare `var x lipgloss.Style` groups); assignments move into the rebuild func. Inline literals get replaced with role vars at the point of use.

- [ ] **Step 1: Convert `ui/list.go`** (full worked example — apply the same shape everywhere)

Replace lines 23–95 (the style var block) with:

```go
var (
	readyStyle, promptingStyle, addedLinesStyle, removedLinesStyle,
	pausedStyle, deletingStyle, recoverableStyle, deletingTitleStyle,
	deletingDescStyle, workspaceTerminalStyle, wtTitleStyle, wtDescStyle,
	wtSelectedTitleStyle, wtSelectedDescStyle, titleStyle, listDescStyle,
	selectedTitleStyle, selectedDescStyle, mainTitle lipgloss.Style
)

func init() { RegisterThemeHook(rebuildListStyles) }

func rebuildListStyles() {
	readyStyle = lipgloss.NewStyle().Foreground(OK)
	promptingStyle = lipgloss.NewStyle().Foreground(Attention)
	addedLinesStyle = lipgloss.NewStyle().Foreground(OK)
	removedLinesStyle = lipgloss.NewStyle().Foreground(ErrorColor)
	pausedStyle = lipgloss.NewStyle().Foreground(Dim)
	deletingStyle = lipgloss.NewStyle().Foreground(ErrorColor)
	recoverableStyle = lipgloss.NewStyle().Foreground(Attention)
	deletingTitleStyle = lipgloss.NewStyle().Padding(1, 1, 0, 1).Foreground(Dim)
	deletingDescStyle = lipgloss.NewStyle().Padding(0, 1, 1, 1).Foreground(Dim)
	workspaceTerminalStyle = lipgloss.NewStyle().Foreground(Workspace)
	wtTitleStyle = lipgloss.NewStyle().Padding(1, 1, 0, 1).Background(Panel).Foreground(Text)
	wtDescStyle = lipgloss.NewStyle().Padding(0, 1, 1, 1).Background(Panel).Foreground(Workspace)
	wtSelectedTitleStyle = lipgloss.NewStyle().Padding(1, 1, 0, 1).Background(SelectionBg).Foreground(SelectionFg)
	wtSelectedDescStyle = lipgloss.NewStyle().Padding(0, 1, 1, 1).Background(SelectionBg).Foreground(Workspace)
	titleStyle = lipgloss.NewStyle().Padding(1, 1, 0, 1).Foreground(Text)
	listDescStyle = lipgloss.NewStyle().Padding(0, 1, 1, 1).Foreground(Dim)
	selectedTitleStyle = lipgloss.NewStyle().Padding(1, 1, 0, 1).Background(SelectionBg).Foreground(SelectionFg)
	selectedDescStyle = lipgloss.NewStyle().Padding(0, 1, 1, 1).Background(SelectionBg).Foreground(SelectionFg)
	mainTitle = lipgloss.NewStyle().Background(Accent).Foreground(SelectionFg)
}
```

The `compat` import becomes unused in list.go — remove it. (`TextDim` refs at old lines 46/50 are covered by the block above.)

- [ ] **Step 2: Convert the remaining `ui/` files with these exact role mappings**

Same pattern per file (`init` + `rebuild<Name>Styles`). Mappings:

| File | Var | Old | New role |
|---|---|---|---|
| split_pane.go | `highlightColor` | `BorderActive` | delete var; use `Accent` directly in rebuild |
| split_pane.go | `dimBorderColor` | `BorderMuted` | delete var; use `Rule` directly |
| split_pane.go | `paneBodyBorder`/`focusedPaneBodyBorder`/`paneTitleStyle`/`focusedPaneTitleStyle`/`diffOverlayTitleStyle` | derive | rebuild func `rebuildSplitPaneStyles`; `buildTopBorder` (lines 604–610) uses `Rule`/`Accent` and the existing style vars |
| menu.go | `keyStyle` | adaptive grays | `Dim` |
| menu.go | `descStyle` | adaptive grays | `Dim` |
| menu.go | `sepStyle` | adaptive grays | `Rule` |
| menu.go | `actionGroupStyle` | ANSI 99 | `Workspace` |
| menu.go | `menuStyle` | ANSI 205 | `Accent` |
| diff.go | `AdditionStyle` | `#22c55e` | `OK` |
| diff.go | `DeletionStyle` | `#ef4444` | `ErrorColor` |
| diff.go | `HunkStyle` | `#0ea5e9` | `Info` |
| err.go | `errStyle` | `#FF0000` | `ErrorColor` |
| err.go | `infoStyle` | `#7AA2F7` | `Info` |
| quick_input.go | `quickInputHintStyle` | `#808080` | `Dim` |
| workspace_tab_bar.go | `wsActiveTabStyle`/`wsInactiveTabStyle` | `BorderActive`/`BorderMuted` | `Accent`/`Rule` |
| workspace_tab_bar.go | `wsPromptIndicator` | `#e5c07b` | `Attention` |
| workspace_tab_bar.go | `wsRunningIndicator` | `#61afef` | `Info` |
| workspace_tab_bar.go | `wsReadyIndicator` | `#51bd73` | `OK` |
| workspace_tab_bar.go | `wsLoadingIndicator` | `#c678dd` | `Workspace` |
| workspace_tab_bar.go | `wsPausedIndicator` | `#888888` | `Dim` |
| preview.go | `previewPaneStyle` | adaptive | `Text` |
| preview.go | `previewScrollFooterStyle` | `#FFD700` | `Highlight` |
| preview.go | inline styles at 191–206 | `#FFD700` | `Highlight` (build inline from role — inline uses are rebuilt per-render, so no hook needed) |
| terminal.go | `terminalPaneStyle` | adaptive | `Text` |
| terminal.go | `terminalFooterStyle` | `#FFD700` | `Highlight` |

- [ ] **Step 3: Fix remaining references to deleted theme vars inside `ui/`**

Run: `go build ./ui/ 2>&1 | head -40` and chase errors. Known remaining `ui/`-internal references: none besides the files above (cursor.go/selection.go/scroll.go/overlay.go use no theme vars). `ui/overlay/` and `app/` still fail to build — that's Task 5.

- [ ] **Step 4: Run ui tests**

Run: `CGO_ENABLED=0 go test ./ui/ 2>&1 | tail -20`
Expected: compile succeeds for the package's own tests, and Task 1's theme tests PASS. Some rendering tests may assert on old colors — if any fail, update the expectation to the new role color (the structural assertions must not change).

- [ ] **Step 5: Commit** — `git add ui/ app/app.go && git commit -m "feat(ui): theme registry with role vars and rebuild hooks; migrate ui styles"` — NOTE: only if `go build ./ui/` passes; `./app` and `./ui/overlay` may still be red, that's fine for this commit only if the repo builds via `go build ./ui/`. If you prefer a green tree per commit, squash Tasks 4–5 into one commit at Task 5 Step 4.

### Task 5: Migrate `ui/overlay/` and `app/` styles

**Files:**
- Modify: `ui/overlay/settingsOverlay.go:220-225`, `ui/overlay/file_explorer.go:21-36`, plus every overlay file referencing deleted vars (`branchPicker.go`, `textInput.go`, `textOverlay.go`, `profilePicker.go`, `workspacePicker.go`, `mergePicker.go`, `profilesManager.go`, `claudePreferences.go`, `sessionLaunchOptions.go`, `confirmationOverlay.go`, `fuzzy.go` — find with grep)
- Modify: `app/app.go:36-41`, `app/help.go:238-241`

- [ ] **Step 1: Inventory the breakage**

Run: `grep -rn "ui\.\(BorderActive\|BorderMuted\|TextDim\|TitleAccent\|HeaderAccent\|KeyHighlight\|TextPrimary\|TextHint\|SelectionBg\|SelectionFg\|DangerAccent\|OverlayBorder\|OverlaySelectedFg\|OverlayItemFg\|OverlayHintFg\)" ui/overlay/ app/ | wc -l`

- [ ] **Step 2: Replace with roles**

Mapping for old-var references: `BorderActive`→`Accent` · `BorderMuted`→`Rule` · `TextDim`→`Dim` · `TitleAccent`→`Accent` · `HeaderAccent`→`Accent` · `KeyHighlight`→`Highlight` · `TextPrimary`→`Text` · `TextHint`→`Faint` · `SelectionBg`→`SelectionBg` · `SelectionFg`→`SelectionFg` · `DangerAccent`→`ErrorColor` · `OverlayBorder`→`Accent` · `OverlaySelectedFg`→`SelectionFg` · `OverlayItemFg`→`Text` · `OverlayHintFg`→`Faint`.

File-explorer literals: `#7c7cff`→`Accent`, `#808080`→`Dim`, `#ffffff`/`#3a3a5a`→`SelectionFg`/`SelectionBg`.

Every **package-level style var** touched here must move into a hook: overlay files register via `ui.RegisterThemeHook(...)` in their own `init()`; `app/app.go`'s `inlineAttachHintStyle`/`statusLineStyle` and `app/help.go`'s four styles likewise. Styles built inside functions (per-render) just reference roles directly, no hook.

Leave `ui/overlay/overlay.go`'s raw ANSI dim codes (lines 73–85) untouched — they implement overlay dimming, not palette; note them in the commit body as deliberate exclusion.

- [ ] **Step 3: Build & test everything**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./... 2>&1 | tail -20`
Expected: build PASS; failing tests only where color expectations changed — fix expectations, never structure.

- [ ] **Step 4: Commit** — `git commit -am "feat(ui): migrate overlay and app styles to theme roles"`

### Task 6: Theme row in the settings overlay

**Files:**
- Modify: `ui/overlay/settingsOverlay.go`
- Test: `ui/overlay/settingsOverlay_test.go`

- [ ] **Step 1: Write the failing test** (match existing test file style)

```go
func TestSettingsOverlay_ThemeRowCycles(t *testing.T) {
	defer ui.ApplyTheme(ui.DefaultThemeName)
	cfg := config.DefaultConfig() // Theme: "afterglow"
	s := NewSettingsOverlay(cfg, false, "")

	// Move cursor to the Theme row (last row).
	for i := 0; i < int(settingsFieldCount)-1; i++ {
		s.HandleKeyPress(keyMsg("down"))
	}
	closed, changed := s.HandleKeyPress(keyMsg("enter"))
	assert.False(t, closed)
	assert.True(t, changed)
	assert.Equal(t, "legacy", cfg.GetTheme())
	assert.Equal(t, "legacy", ui.CurrentThemeName())

	// Cycles back around.
	_, changed = s.HandleKeyPress(keyMsg("enter"))
	assert.True(t, changed)
	assert.Equal(t, "afterglow", cfg.GetTheme())
}
```

If the existing test file has no `keyMsg` helper, add one: `func keyMsg(s string) tea.KeyPressMsg` — check how sibling overlay tests construct key presses (`grep -n "KeyPressMsg{" ui/overlay/*_test.go`) and copy that idiom.

- [ ] **Step 2: Run it** — `CGO_ENABLED=0 go test ./ui/overlay/ -run TestSettingsOverlay_ThemeRow -v` — Expected: FAIL

- [ ] **Step 3: Implement**

1. Add `settingsFieldTheme` to the enum before `settingsFieldCount`; `label()` returns `"Theme"`.
2. In `activateRow()`:

```go
	case settingsFieldTheme:
		names := ui.ThemeNames()
		cur := s.cfg.GetTheme()
		if cur == "" {
			cur = ui.DefaultThemeName
		}
		idx := 0
		for i, n := range names {
			if n == cur {
				idx = i
			}
		}
		next := names[(idx+1)%len(names)]
		s.cfg.Mutate(func(c *config.Config) { c.Theme = next })
		ui.ApplyTheme(next)
		return false, true
```

3. In `valueFor()`: `case settingsFieldTheme: if v := s.cfg.GetTheme(); v != "" { return v }; return ui.DefaultThemeName`.

The caller (`handleStateSettingsKey`) already persists config on `changed=true` — verify by reading `app/state_settings.go`; no app change needed.

- [ ] **Step 4: Run it** — Expected: PASS. Also run the full overlay package: `CGO_ENABLED=0 go test ./ui/overlay/`
- [ ] **Step 5: Commit** — `git commit -am "feat(settings): theme selector row with live apply"`
- [ ] **Step 6: Manual smoke (end of phase 1)** — `CGO_ENABLED=0 go build -o loom && ./loom` in a scratch repo: confirm afterglow colors everywhere, `S` → Theme row cycles legacy/afterglow live, restart persists choice.

---

# Phase 2 — Cards, rail, split controls

### Task 7: `Instance.StatusAge`

**Files:**
- Modify: `session/instance.go` (struct field near other unexported fields; `TransitionTo` at line ~432)
- Test: `session/instance_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestStatusAge_StampsOnTransition(t *testing.T) {
	inst := &Instance{Title: "age-test", Status: Ready}
	assert.Equal(t, time.Duration(0), inst.StatusAge(), "zero before any transition")

	require.NoError(t, inst.TransitionTo(Loading))
	age := inst.StatusAge()
	assert.Greater(t, age, time.Duration(0))
	assert.Less(t, age, time.Second)

	// Self-transition must not restamp.
	time.Sleep(10 * time.Millisecond)
	before := inst.StatusAge()
	require.NoError(t, inst.TransitionTo(Loading))
	assert.GreaterOrEqual(t, inst.StatusAge(), before)
}
```

Check how instance tests construct instances (`grep -n "func TestTransition" session/*_test.go`) and mirror; if bare `&Instance{}` doesn't satisfy invariants, use the same constructor the transition tests use.

- [ ] **Step 2: Run it** — `CGO_ENABLED=0 go test ./session/ -run TestStatusAge -v` — Expected: FAIL (no method StatusAge)

- [ ] **Step 3: Implement**

Field (near the other unexported, non-serialized fields of `Instance`):

```go
	// statusChangedAt is when Status last changed (in-memory only, not
	// serialized — ages reset on restart, which is acceptable for the
	// "waiting 4m" card labels it feeds).
	statusChangedAt time.Time
```

In `TransitionTo`, after `i.Status = to` (line ~442): `i.statusChangedAt = time.Now()`. (This is the only pointer-receiver write to `Status` in the codebase — verified via grep.)

Accessor:

```go
// StatusAge returns how long the instance has been in its current
// status, or 0 when no transition has been observed this process.
func (i *Instance) StatusAge() time.Duration {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.statusChangedAt.IsZero() {
		return 0
	}
	return time.Since(i.statusChangedAt)
}
```

- [ ] **Step 4: Run it** — Expected: PASS
- [ ] **Step 5: Commit** — `git commit -am "feat(session): track status age for card wait labels"`

### Task 8: Card component (`ui/card.go`) — pure rendering

**Files:**
- Create: `ui/card.go`, `ui/card_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// ui/card_test.go
package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func plain(s string) string { return ansi.Strip(s) }

func TestTailLines_TrimsBlanksAndStripsANSI(t *testing.T) {
	screen := "one\n\x1b[31mtwo\x1b[0m\nthree\n\n   \n"
	assert.Equal(t, []string{"two", "three"}, TailLines(screen, 2))
	assert.Equal(t, []string{"one", "two", "three"}, TailLines(screen, 5))
	assert.Nil(t, TailLines("", 3))
	assert.Nil(t, TailLines("\n \n", 3))
}

func TestRenderCard_RailShowsTitleAndTail(t *testing.T) {
	d := CardData{Title: "auth-refactor", Index: 1, Status: session.Running,
		TailLines: []string{"✻ running tests"}, Spinner: "⠋"}
	out := plain(RenderCard(d, DensityRail, 30))
	lines := strings.Split(out, "\n")
	assert.Len(t, lines, 2)
	assert.Contains(t, lines[0], "auth-refactor")
	assert.Contains(t, lines[1], "✻ running tests")
	for _, l := range lines {
		assert.LessOrEqual(t, ansi.StringWidth(l), 30)
	}
}

func TestRenderCard_LineDensityIsOneLine(t *testing.T) {
	d := CardData{Title: "auth-refactor", Index: 2, Status: session.Ready}
	out := plain(RenderCard(d, DensityLine, 30))
	assert.NotContains(t, out, "\n")
	assert.Contains(t, out, "auth-refactor")
}

func TestRenderCard_PromptingShowsWaitAge(t *testing.T) {
	d := CardData{Title: "db-migration", Index: 1, Status: session.Prompting,
		StatusAge: 4 * time.Minute}
	out := plain(RenderCard(d, DensityRail, 40))
	assert.Contains(t, out, "4m")
}

func TestRenderCard_TruncatesLongTitles(t *testing.T) {
	d := CardData{Title: strings.Repeat("x", 60), Index: 1, Status: session.Ready}
	out := plain(RenderCard(d, DensityRail, 20))
	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, ansi.StringWidth(l), 20)
	}
}

func TestSortForOverview_AttentionFirstStable(t *testing.T) {
	mk := func(title string, st session.Status) *session.Instance {
		return &session.Instance{Title: title, Status: st}
	}
	items := []*session.Instance{
		mk("d-paused", session.Paused),
		mk("b-running", session.Running),
		mk("a-prompting", session.Prompting),
		mk("c-ready", session.Ready),
		mk("e-prompting", session.Prompting),
	}
	order := SortForOverview(items)
	titles := make([]string, len(order))
	for i, idx := range order {
		titles[i] = items[idx].Title
	}
	assert.Equal(t, []string{"a-prompting", "e-prompting", "b-running", "c-ready", "d-paused"}, titles)
}

func TestSortForOverview_WorkspaceTerminalPinnedFirst(t *testing.T) {
	items := []*session.Instance{
		{Title: "wt", Status: session.Running, IsWorkspaceTerminal: true},
		{Title: "a", Status: session.Prompting},
	}
	order := SortForOverview(items)
	assert.Equal(t, 0, order[0], "workspace terminal stays pinned at display position 0")
}
```

- [ ] **Step 2: Run** — `CGO_ENABLED=0 go test ./ui/ -run 'TestTailLines|TestRenderCard|TestSortForOverview' -v` — Expected: FAIL

- [ ] **Step 3: Implement `ui/card.go`**

```go
package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// CardDensity selects how much of an instance a card shows.
type CardDensity int

const (
	// DensityLine is a one-line row (height-starved fallback).
	DensityLine CardDensity = iota
	// DensityRail is the focus-mode rail mini-card: title line + one
	// tail/status line, with a left accent bar.
	DensityRail
	// DensityCard is the overview card: bordered box with metadata and
	// a multi-line tail (rendered by Overview in overview.go).
	DensityCard
)

// Rail layout constants (used by List height math).
const (
	// RailCardLines is lines per DensityRail card incl. trailing gap.
	RailCardLines = 3
	// RailHeaderLines is the section-label header at the top of the rail.
	RailHeaderLines = 2
)

// CardData is the render-ready view-model for one instance card. It is
// plain data so RenderCard stays a pure, table-testable function; build
// it from a live instance with BuildCardData.
type CardData struct {
	Title               string
	Index               int // 1-based display number (0 for workspace terminal)
	Status              session.Status
	IsWorkspaceTerminal bool
	BellPending         bool
	Selected            bool
	Branch              string
	DiffAdded           int
	DiffRemoved         int
	HasDiff             bool
	TailLines           []string
	StatusAge           time.Duration // 0 = unknown/not applicable
	Spinner             string        // current spinner frame for Running/Loading
}

// NeedsAttention reports whether this card should carry the Attention
// accent — the only loud signal in the UI.
func (d CardData) NeedsAttention() bool {
	return d.Status == session.Prompting || d.BellPending
}

// BuildCardData snapshots inst into a CardData. spinnerFrame is the
// current spinner view (pass "" when unavailable). tailN caps the live
// tail; 0 skips the Preview read entirely (DensityLine callers).
func BuildCardData(inst *session.Instance, selected bool, spinnerFrame string, tailN int) CardData {
	d := CardData{
		Title:               inst.Title,
		Status:              inst.GetStatus(),
		IsWorkspaceTerminal: inst.IsWorkspaceTerminal,
		BellPending:         inst.BellPending(),
		Selected:            selected,
		Branch:              inst.GetBranch(),
		StatusAge:           inst.StatusAge(),
		Spinner:             spinnerFrame,
	}
	if stat := inst.GetDiffStats(); stat != nil && stat.Error == nil && !stat.IsEmpty() {
		d.HasDiff, d.DiffAdded, d.DiffRemoved = true, stat.Added, stat.Removed
	}
	if tailN > 0 {
		if screen, err := inst.Preview(); err == nil {
			d.TailLines = TailLines(screen, tailN)
		}
	}
	return d
}

// TailLines returns the last n non-blank-tail lines of screen with ANSI
// styling stripped (card chrome owns the styling; embedded SGR would
// bleed). Returns nil for an effectively empty screen.
func TailLines(screen string, n int) []string {
	if screen == "" {
		return nil
	}
	lines := strings.Split(screen, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	if end == 0 {
		return nil
	}
	start := end - n
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, end-start)
	for _, l := range lines[start:end] {
		out = append(out, ansi.Strip(l))
	}
	return out
}

// accentColor returns the left-bar/border accent for a card.
func (d CardData) accentColor() interface{ RGBA() (r, g, b, a uint32) } {
	switch {
	case d.NeedsAttention():
		return Attention
	case d.Selected:
		return Accent
	case d.IsWorkspaceTerminal:
		return Workspace
	default:
		return Rule
	}
}

// statusLabel is the human status phrase for the second line / card
// corner: "❯ awaiting input · 4m", "✻ working", "paused · 3d", …
func (d CardData) statusLabel() string {
	age := formatAge(d.StatusAge)
	switch d.Status {
	case session.Prompting:
		if age != "" {
			return "❯ awaiting input · " + age
		}
		return "❯ awaiting input"
	case session.Running, session.Loading:
		return d.Spinner + " working"
	case session.Ready:
		if age != "" {
			return "✓ idle " + age
		}
		return "✓ idle"
	case session.Paused:
		if age != "" {
			return "paused · " + age
		}
		return "paused"
	case session.Recoverable:
		return "⟲ recoverable"
	case session.Deleting:
		return "✕ deleting"
	}
	return ""
}

// formatAge renders a duration as a compact age ("40s", "4m", "1h12m",
// "3d"); "" for zero/negative.
func formatAge(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// truncate cuts s to width cells with an ellipsis.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return runewidth.Truncate(s, width, "")
	}
	return runewidth.Truncate(s, width-1, "…")
}

// RenderCard renders d at the given density and total width. DensityCard
// is rendered by Overview (overview.go) which owns border/grid layout;
// RenderCard handles DensityLine and DensityRail.
func RenderCard(d CardData, density CardDensity, width int) string {
	if width < 4 {
		width = 4
	}
	bar := lipgloss.NewStyle().Foreground(d.accentColor()).Render("▌")
	titleFg := Text
	if d.NeedsAttention() {
		titleFg = Attention
	}
	if d.IsWorkspaceTerminal && !d.NeedsAttention() {
		titleFg = Workspace
	}
	titleStyleC := lipgloss.NewStyle().Foreground(titleFg)
	if d.Selected {
		titleStyleC = titleStyleC.Bold(true)
	}

	prefix := fmt.Sprintf("%d. ", d.Index)
	inner := width - 2 // bar + space
	title := truncate(prefix+d.Title, inner)
	titleLine := bar + " " + titleStyleC.Render(title)

	if density == DensityLine {
		return titleLine
	}

	// Second line: attention prompt beats tail beats status label.
	second := d.statusLabel()
	secondFg := Dim
	if d.NeedsAttention() {
		secondFg = Attention
	} else if len(d.TailLines) > 0 {
		second = d.TailLines[len(d.TailLines)-1]
	}
	secondLine := bar + " " + lipgloss.NewStyle().Foreground(secondFg).Render(truncate(second, inner))

	// Selected rail cards get a Panel background across both lines.
	if d.Selected {
		bgStyle := lipgloss.NewStyle().Background(Panel).Width(width)
		return bgStyle.Render(titleLine) + "\n" + bgStyle.Render(secondLine)
	}
	return titleLine + "\n" + secondLine
}

// SortForOverview returns display order (indices into items) for the
// overview grid and overview cursor movement: workspace terminal pinned
// first, then attention > running/loading > ready > paused/recoverable,
// stable by title within a tier. Deleting sorts last.
func SortForOverview(items []*session.Instance) []int {
	tier := func(inst *session.Instance) int {
		if inst.IsWorkspaceTerminal {
			return 0
		}
		st := inst.GetStatus()
		switch {
		case st == session.Prompting || inst.BellPending():
			return 1
		case st == session.Running || st == session.Loading:
			return 2
		case st == session.Ready:
			return 3
		case st == session.Paused || st == session.Recoverable:
			return 4
		default: // Deleting
			return 5
		}
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sortSliceStable(order, func(a, b int) bool {
		ta, tb := tier(items[a]), tier(items[b])
		if ta != tb {
			return ta < tb
		}
		return items[a].Title < items[b].Title
	})
	return order
}

// sortSliceStable is sort.SliceStable without importing sort here twice
// (kept tiny for testability).
func sortSliceStable(order []int, less func(a, b int) bool) {
	// simple stable insertion sort — n is small (session counts)
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && less(order[j], order[j-1]); j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
}
```

Note: `CardData.accentColor` returns `color.Color`-shaped interface inline to avoid importing `image/color` — if `go vet` complains, import `image/color` and type it `color.Color` (preferred; do that directly).

- [ ] **Step 4: Run** — same command — Expected: PASS
- [ ] **Step 5: Commit** — `git commit -am "feat(ui): card component with densities, tails, and overview sort"`

> **AMENDMENT (2026-07-20, during execution):** The RenderCard code above has two defects fixed in commit `6670e23` (review-driven; the fixed code in `ui/card.go` supersedes this plan text): (1) the selected-card Panel background must be applied on the *inner* styles — lipgloss v2 does not re-inject an outer background after an embedded SGR reset, so wrapping pre-styled lines renders striped; (2) `runewidth.Truncate` already reserves the ellipsis width, so pass `width`, not `width-1`. Additionally `sortSliceStable` was replaced by `slices.SortStableFunc` over precomputed tiers, and `TailLines` sanitizes C0 controls. Later tasks referencing card.go should treat the committed code as authoritative.

### Task 9: Rail rendering in `List`

**Files:**
- Modify: `ui/list.go` (`String()`, `maxVisibleItems()`, add `SelectedIdx`, `SetPeerSections`)
- Modify: `ui/consts.go` (nothing removed; rail consts already in card.go)
- Test: `ui/list_height_test.go` (update math), `ui/list.go`-related tests

- [ ] **Step 1: Read the existing list tests** — `ui/list_height_test.go`, `ui/list_page_nav_test.go`, `ui/list_display_index_test.go` — to see the exact fixtures. Nav/display-index tests must keep passing unchanged; height tests change with the new per-item height.

- [ ] **Step 2: Write the failing tests** (append to `ui/list_height_test.go`)

```go
func TestMaxVisibleItems_RailMath(t *testing.T) {
	l := NewList(&spinner.Model{})
	// height 20: (20 - RailHeaderLines) / RailCardLines = 6
	l.SetSize(30, 20)
	assert.Equal(t, 6, l.maxVisibleItems())
	// Peer sections consume bottom lines: 2 peers = blank + 2 lines.
	l.SetPeerSections([]PeerSection{{Name: "a"}, {Name: "b"}})
	assert.Equal(t, 5, l.maxVisibleItems())
	// Tiny heights clamp to 1.
	l.SetSize(30, 3)
	assert.Equal(t, 1, l.maxVisibleItems())
}

func TestListString_RendersRailCards(t *testing.T) {
	l := NewList(&spinner.Model{})
	l.SetWorkspaceName("loom")
	l.SetSize(40, 30)
	inst := &session.Instance{Title: "auth-refactor", Status: session.Ready}
	l.AddInstance(inst)
	out := ansi.Strip(l.String())
	assert.Contains(t, out, "LOOM")          // section label, uppercased
	assert.Contains(t, out, "auth-refactor") // card title
	assert.Contains(t, out, "▌")             // accent bar
}

func TestListString_RendersPeerSummaries(t *testing.T) {
	l := NewList(&spinner.Model{})
	l.SetWorkspaceName("loom")
	l.SetSize(40, 30)
	l.SetPeerSections([]PeerSection{{Name: "summa", Attention: 2, Running: 1, Idle: 3}})
	out := ansi.Strip(l.String())
	assert.Contains(t, out, "SUMMA")
	assert.Contains(t, out, "2") // attention count surfaced
}
```

Fix imports as needed (`ansi` from `github.com/charmbracelet/x/ansi`, `session`, `spinner`).

- [ ] **Step 3: Run** — `CGO_ENABLED=0 go test ./ui/ -run 'TestMaxVisible|TestListString' -v` — Expected: FAIL

- [ ] **Step 4: Implement**

Add to list.go:

```go
// PeerSection summarizes a non-focused workspace slot for the rail
// footer (live counts from that slot's list; selection stays scoped to
// the focused workspace until cross-workspace lands).
type PeerSection struct {
	Name      string
	Attention int // Prompting or bell-pending
	Running   int // Running/Loading
	Idle      int // everything else
}

// SetPeerSections sets the peer-workspace summaries rendered under the rail.
func (l *List) SetPeerSections(peers []PeerSection) { l.peers = peers }

// SelectedIdx returns the current selection index (for jump helpers).
func (l *List) SelectedIdx() int { return l.selectedIdx }

// peerLines is the vertical budget the peer footer consumes.
func (l *List) peerLines() int {
	if len(l.peers) == 0 {
		return 0
	}
	return len(l.peers) + 1 // blank separator + one line per peer
}
```

Add field `peers []PeerSection` to the `List` struct.

Replace `maxVisibleItems`:

```go
// maxVisibleItems returns how many rail cards fit: header (2 lines) +
// RailCardLines per item, minus the peer footer.
func (l *List) maxVisibleItems() int {
	n := (l.height - RailHeaderLines - l.peerLines()) / RailCardLines
	if n < 1 {
		n = 1
	}
	return n
}
```

Replace `String()`'s body: keep `ensureSelectedVisible`, window math, and scroll-arrow logic, but render:

1. Section header: `workspaceLabelStyle.Render(" " + strings.ToUpper(titleText) + arrow)` on line 1, blank line 2 — where `workspaceLabelStyle` is a hook-built style `lipgloss.NewStyle().Foreground(Workspace)` (add to `rebuildListStyles`). Drop `mainTitle`'s `lipgloss.Place` block.
2. Per item: `RenderCard(BuildCardData(item, i == l.selectedIdx, l.renderer.spinner.View(), 1), DensityRail, l.width)` followed by one blank line (except after the last). Note `Render`'s old row pipeline (`InstanceRenderer.Render`) stays for now — delete it plus its styles ONLY if nothing else references it (`grep -rn "renderer.Render\|InstanceRenderer" ui/ app/` — the merge picker uses `DisplayIndex`, keep that function). Pass `DisplayIndex(l.items, i)` as `CardData.Index` — extend `BuildCardData` with an `index int` param or set `d.Index` after the call (set after the call: `d := BuildCardData(...); d.Index = num`).
3. Peer footer: blank line, then per peer one line: `workspaceLabelStyle.Render(strings.ToUpper(p.Name))` + counts — attention count styled `Attention` (only when > 0), running styled `Dim` (`fmt.Sprintf(" ❯%d ✻%d ·%d", p.Attention, p.Running, p.Idle)`, omitting zero segments; keep it simple and truncate to width).
4. Keep the final `lipgloss.Place(l.width, l.height, ...)` wrapper.

- [ ] **Step 5: Fix the pre-existing height tests** — the old `3 + 5N` layout formula in `list_height_test.go` comments/expectations changes to `RailHeaderLines + RailCardLines*N + peerLines`. Update expectations; `list_page_nav_test.go` uses `maxVisibleItems` indirectly and should pass unchanged — verify.

- [ ] **Step 6: Run the whole ui package + build** — `CGO_ENABLED=0 go test ./ui/ && CGO_ENABLED=0 go build ./...` — Expected: PASS
- [ ] **Step 7: Commit** — `git commit -am "feat(ui): render session list as live mini-card rail with peer summaries"`

### Task 10: Peer sections fed from slots

**Files:**
- Modify: `app/app.go` — find `updateTabBarStatuses` (`grep -n "func (m \*home) updateTabBarStatuses" app/app.go`)

- [ ] **Step 1: Implement**

Add alongside `updateTabBarStatuses`:

```go
// refreshPeerSections rebuilds the rail's peer-workspace summaries from
// the non-focused slots' live lists. Main-goroutine only (reads slot
// lists, which are only mutated there).
func (m *home) refreshPeerSections() {
	if len(m.slots) <= 1 {
		m.list.SetPeerSections(nil)
		return
	}
	peers := make([]ui.PeerSection, 0, len(m.slots)-1)
	for i, slot := range m.slots {
		if i == m.focusedSlot {
			continue
		}
		p := ui.PeerSection{Name: slot.wsCtx.Name}
		for _, inst := range slot.list.GetInstances() {
			st := inst.GetStatus()
			switch {
			case st == session.Prompting || inst.BellPending():
				p.Attention++
			case st == session.Running || st == session.Loading:
				p.Running++
			default:
				p.Idle++
			}
		}
		peers = append(peers, p)
	}
	m.list.SetPeerSections(peers)
}
```

Call `m.refreshPeerSections()` at the end of `updateTabBarStatuses` (every caller of the tab refresh then updates the rail too), and at the end of `loadSlot`.

- [ ] **Step 2: Build + run app tests** — `CGO_ENABLED=0 go test ./app/ 2>&1 | tail -5` — Expected: PASS
- [ ] **Step 3: Commit** — `git commit -am "feat(app): feed live peer-workspace summaries to the rail"`

### Task 11: `UIPrefs` persistence

**Files:**
- Modify: `config/state.go`
- Test: `config/state_test.go` (append; check filename with `ls config/*_test.go`)

- [ ] **Step 1: Write the failing test**

```go
func TestUIPrefs_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := LoadStateFrom(dir)
	p := UIPrefs{ViewMode: "overview", RailHidden: true, TerminalHidden: true,
		SplitRatios: map[string]float64{"auth": 0.8}}
	require.NoError(t, s.SetUIPrefs(p))

	s2 := LoadStateFrom(dir)
	got := s2.GetUIPrefs()
	assert.Equal(t, p, got)

	// Returned map is a copy — caller mutation must not leak back.
	got.SplitRatios["auth"] = 0.1
	assert.Equal(t, 0.8, s2.GetUIPrefs().SplitRatios["auth"])
}
```

- [ ] **Step 2: Run** — `CGO_ENABLED=0 go test ./config/ -run TestUIPrefs -v` — Expected: FAIL

- [ ] **Step 3: Implement**

```go
// UIPrefs is per-workspace UI layout state (view mode, rail/terminal
// visibility, per-session split ratios). Stored in state.json, so each
// workspace keeps its own layout.
type UIPrefs struct {
	ViewMode       string             `json:"view_mode,omitempty"`
	RailHidden     bool               `json:"rail_hidden,omitempty"`
	TerminalHidden bool               `json:"terminal_hidden,omitempty"`
	SplitRatios    map[string]float64 `json:"split_ratios,omitempty"`
}

func (p UIPrefs) clone() UIPrefs {
	out := p
	if p.SplitRatios != nil {
		out.SplitRatios = make(map[string]float64, len(p.SplitRatios))
		for k, v := range p.SplitRatios {
			out.SplitRatios[k] = v
		}
	}
	return out
}
```

Add `UI UIPrefs \`json:"ui"\`` to the `State` struct (after `InstancesData`). Extend the `AppState` interface:

```go
	// GetUIPrefs returns a copy of the persisted UI layout prefs.
	GetUIPrefs() UIPrefs
	// SetUIPrefs replaces and persists the UI layout prefs.
	SetUIPrefs(p UIPrefs) error
```

Implement on `*State` (same shape as `SetHelpScreensSeen`):

```go
func (s *State) GetUIPrefs() UIPrefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.UI.clone()
}

func (s *State) SetUIPrefs(p UIPrefs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UI = p.clone()
	dir, err := s.resolveDirLocked()
	if err != nil {
		return err
	}
	return s.saveToLocked(dir)
}
```

- [ ] **Step 4: Fix AppState fakes** — `grep -rln "GetHelpScreensSeen" --include="*_test.go" . | grep -v config/` and add the two methods to every fake implementing `config.AppState` (return `config.UIPrefs{}` / `nil`). Then `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./config/ ./session/ ./app/ 2>&1 | tail -5` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -am "feat(config): persist UI layout prefs in state.json"`

### Task 12: Adjustable/hideable split pane

**Files:**
- Modify: `ui/split_pane.go` (`SetSize`, `String`, `HitTest`, new methods), `app/app.go` (`updateHandleWindowSizeEvent` hit-test anchor)
- Test: `ui/split_pane_ratio_test.go` (new)

- [ ] **Step 1: Write the failing tests**

```go
// ui/split_pane_ratio_test.go
package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestSplit() *SplitPane {
	return NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
}

func TestSplitPane_DefaultRatioIs70(t *testing.T) {
	s := newTestSplit()
	s.SetSize(80, 44) // chrome 4 → available 40
	assert.Equal(t, 28, s.agent.height)
	assert.Equal(t, 12, s.terminal.height)
}

func TestSplitPane_AdjustRatioClampsAndRelayouts(t *testing.T) {
	s := newTestSplit()
	s.SetSize(80, 44)
	s.AdjustAgentRatio(0.10) // 0.8
	assert.Equal(t, 32, s.agent.height)
	for i := 0; i < 20; i++ {
		s.AdjustAgentRatio(0.10)
	}
	assert.InDelta(t, 0.9, s.AgentRatio(), 0.001, "clamped high")
	for i := 0; i < 40; i++ {
		s.AdjustAgentRatio(-0.10)
	}
	assert.InDelta(t, 0.2, s.AgentRatio(), 0.001, "clamped low")
}

func TestSplitPane_TerminalHiddenGivesAgentEverything(t *testing.T) {
	s := newTestSplit()
	s.SetTerminalHidden(true)
	s.SetSize(80, 44) // one pane chrome = 2 → available 42
	assert.Equal(t, 42, s.agent.height)
	assert.Equal(t, 0, s.terminal.height)

	// HitTest never resolves to the hidden terminal.
	_, _, _, ok := s.HitTest(5, s.agent.height+3)
	assert.False(t, ok)

	// AgentContentHeight matches for the app's mouse anchor.
	assert.Equal(t, 42, s.AgentContentHeight())
}
```

- [ ] **Step 2: Run** — `CGO_ENABLED=0 go test ./ui/ -run TestSplitPane_ -v` — Expected: FAIL

- [ ] **Step 3: Implement**

Add fields to `SplitPane`: `agentRatio float64` and `terminalHidden bool`. New methods:

```go
// AgentRatio returns the agent pane's height share (default
// SplitAgentPercent when unset).
func (s *SplitPane) AgentRatio() float64 {
	if s.agentRatio == 0 {
		return SplitAgentPercent
	}
	return s.agentRatio
}

// SetAgentRatio sets the agent share (clamped to [0.2, 0.9]) and
// re-lays-out at the current size.
func (s *SplitPane) SetAgentRatio(r float64) {
	if r < 0.2 {
		r = 0.2
	}
	if r > 0.9 {
		r = 0.9
	}
	s.agentRatio = r
	if s.width > 0 && s.height > 0 {
		s.SetSize(s.width, s.height)
	}
}

// AdjustAgentRatio nudges the split by delta and returns the new ratio.
func (s *SplitPane) AdjustAgentRatio(delta float64) float64 {
	s.SetAgentRatio(s.AgentRatio() + delta)
	return s.AgentRatio()
}

// SetTerminalHidden shows/hides the terminal pane; hidden gives the
// agent pane the full height.
func (s *SplitPane) SetTerminalHidden(h bool) {
	s.terminalHidden = h
	if s.width > 0 && s.height > 0 {
		s.SetSize(s.width, s.height)
	}
}

// IsTerminalHidden reports whether the terminal pane is hidden.
func (s *SplitPane) IsTerminalHidden() bool { return s.terminalHidden }

// AgentContentHeight is the agent pane's inner height — the app's
// mouse hit-test anchor mirrors layout through this instead of
// duplicating the math.
func (s *SplitPane) AgentContentHeight() int { return s.agent.height }
```

In `SetSize`, replace the fixed math (lines ~108–116):

```go
	panes := 2
	if s.terminalHidden {
		panes = 1
	}
	paneChrome := panes * (1 + bodyBorderV)
	availableHeight := max(height-paneChrome, 0)

	agentHeight := availableHeight
	terminalHeight := 0
	if !s.terminalHidden {
		agentHeight = int(float64(availableHeight) * s.AgentRatio())
		terminalHeight = availableHeight - agentHeight
	}
```

In `String()`, when `s.terminalHidden`, render only `agentBox` (skip `terminalBox` in the JoinVertical). In `HitTest`, before the terminal-region check: `if s.terminalHidden { return 0, 0, 0, false }` after the agent check.

In `app/app.go:updateHandleWindowSizeEvent`, replace the duplicated anchor math (lines ~638–646) with:

```go
	m.listWidth = listWidth
	m.agentBottomY = m.tabBar.Height() + 1 + m.splitPane.AgentContentHeight()
```

(`m.splitPane.SetSize` ran two lines above, so the accessor is current.)

- [ ] **Step 4: Run** — `CGO_ENABLED=0 go test ./ui/ ./app/ 2>&1 | tail -5` — Expected: PASS (pane_border_test and split_pane_scroll_indicator_test must stay green)
- [ ] **Step 5: Commit** — `git commit -am "feat(ui): adjustable agent/terminal split with hideable terminal pane"`

### Task 13: Rail toggle + prefs load/apply in the app

**Files:**
- Modify: `app/app.go` (`home` fields, `updateHandleWindowSizeEvent`, `View`, `instanceChanged`, startup)

- [ ] **Step 1: Implement rail hide + prefs plumbing**

1. Add `railHidden bool` to `home` (near `listWidth`).
2. In `updateHandleWindowSizeEvent` (line ~620): `listWidth := int(float32(msg.Width) * ui.ListWidthPercent); if m.railHidden { listWidth = 0 }`.
3. In `View()` (line ~2446): `listView := ""; if !m.railHidden { listView = m.list.String() }` then `lipgloss.JoinHorizontal(lipgloss.Top, listView, rightContent)` — JoinHorizontal with an empty string yields just the right content; verify visually in the smoke test.
4. Prefs helpers on `home`:

```go
// applyUIPrefs pushes persisted layout prefs onto the components.
// Called after a slot is loaded/focused and at startup.
func (m *home) applyUIPrefs() {
	p := m.appState.GetUIPrefs()
	m.railHidden = p.RailHidden
	m.splitPane.SetTerminalHidden(p.TerminalHidden)
	if sel := m.list.GetSelectedInstance(); sel != nil {
		if r, ok := p.SplitRatios[sel.Title]; ok {
			m.splitPane.SetAgentRatio(r)
		}
	}
	if m.lastWidth > 0 {
		m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: m.lastWidth, Height: m.lastHeight})
	}
}

// mutateUIPrefs applies fn to a copy of the prefs and persists; save
// errors are logged, not surfaced (layout prefs are best-effort).
func (m *home) mutateUIPrefs(fn func(*config.UIPrefs)) {
	p := m.appState.GetUIPrefs()
	fn(&p)
	if err := m.appState.SetUIPrefs(p); err != nil {
		log.For("app").Warn("ui_prefs_save_failed", "err", err)
	}
}
```

5. Call `m.applyUIPrefs()` at the end of `loadSlot` and once at startup after the initial workspace activation (find where `loadSlot(focused)` is called in the startup path at line ~597 — the `applyUIPrefs` inside `loadSlot` covers it).
6. In `instanceChanged()` (line ~1554), near the top, re-apply the selected instance's ratio: `if sel := m.list.GetSelectedInstance(); sel != nil { if r, ok := m.appState.GetUIPrefs().SplitRatios[sel.Title]; ok { m.splitPane.SetAgentRatio(r) } }` — guard with a `m.appState != nil` check (tests may construct bare homes).

- [ ] **Step 2: Build + tests** — `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./app/ 2>&1 | tail -5`
- [ ] **Step 3: Commit** — `git commit -am "feat(app): rail visibility + persisted layout prefs plumbing"`

### Task 14: New Lua actions — rail/terminal/split/jump

**Files:**
- Modify: `script/host.go`, `script/api_actions.go`, `script/host_fake_test.go`, `script/defaults.lua`, `app/app_scripts.go`, `app/app.go` (jump helper), `ui/list.go` (nothing further), plus `app/help.go` key help if it lists workspace keys (`grep -n '"\["' app/help.go`)

- [ ] **Step 1: Write the failing engine test** (append to `script/api_actions_test.go`, mirroring an existing sync-action test — read one first)

```go
func TestSyncActions_FleetPrimitivesReachHost(t *testing.T) {
	e, h := newTestEngineWithFake(t) // use the file's existing constructor helper
	mustLoadString(t, e, `
		cs.bind("]", function() cs.actions.next_waiting() end)
		cs.bind("[", function() cs.actions.prev_waiting() end)
		cs.bind("\\", function() cs.actions.toggle_rail() end)
		cs.bind("T", function() cs.actions.toggle_terminal_pane() end)
		cs.bind("ctrl+up", function() cs.actions.resize_split_up() end)
		cs.bind("ctrl+down", function() cs.actions.resize_split_down() end)
	`)
	for _, key := range []string{"]", "[", "\\", "T", "ctrl+up", "ctrl+down"} {
		_, err := e.Dispatch(context.Background(), key, h)
		require.NoError(t, err, key)
	}
	assert.Equal(t, 1, h.nextWaitingCalls)
	assert.Equal(t, 1, h.prevWaitingCalls)
	assert.Equal(t, 1, h.toggleRailCalls)
	assert.Equal(t, 1, h.toggleTerminalPaneCalls)
	assert.Equal(t, 1, h.resizeSplitUpCalls)
	assert.Equal(t, 1, h.resizeSplitDownCalls)
}
```

Adapt helper names (`newTestEngineWithFake`, `mustLoadString`) to whatever the existing tests actually use — read `script/api_actions_test.go` first and copy its scaffolding exactly; add the counter fields to `script/host_fake_test.go`'s fake.

- [ ] **Step 2: Run** — `CGO_ENABLED=0 go test ./script/ -run TestSyncActions_Fleet -v` — Expected: FAIL

- [ ] **Step 3: Implement**

1. `script/host.go` — extend the interface after the list-navigation block:

```go
	// Fleet primitives — rail/terminal-pane visibility, split resize,
	// and attention jumps. Deferred model mutations like the scroll
	// primitives above.
	NextWaiting()
	PrevWaiting()
	ToggleRail()
	ToggleTerminalPane()
	ResizeSplitUp()
	ResizeSplitDown()
```

2. `script/api_actions.go:installSyncActions` — six entries in the established shape (`actions.RawSetString("next_waiting", L.NewFunction(func(L *lua.LState) int { if e.curHost != nil { e.curHost.NextWaiting() }; return 0 }))`, etc.).
3. `script/host_fake_test.go` — counter fields + methods.
4. `app/app_scripts.go` — implementations:

```go
// NextWaiting implements script.Host.
func (s *scriptHost) NextWaiting() {
	s.deferModelMutation(func(m *home) { m.jumpWaiting(1) })
}

// PrevWaiting implements script.Host.
func (s *scriptHost) PrevWaiting() {
	s.deferModelMutation(func(m *home) { m.jumpWaiting(-1) })
}

// ToggleRail implements script.Host.
func (s *scriptHost) ToggleRail() {
	s.deferModelMutation(func(m *home) {
		m.railHidden = !m.railHidden
		m.mutateUIPrefs(func(p *config.UIPrefs) { p.RailHidden = m.railHidden })
		m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: m.lastWidth, Height: m.lastHeight})
	})
}

// ToggleTerminalPane implements script.Host.
func (s *scriptHost) ToggleTerminalPane() {
	s.deferModelMutation(func(m *home) {
		hidden := !m.splitPane.IsTerminalHidden()
		m.splitPane.SetTerminalHidden(hidden)
		m.mutateUIPrefs(func(p *config.UIPrefs) { p.TerminalHidden = hidden })
		m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: m.lastWidth, Height: m.lastHeight})
	})
}

// ResizeSplitUp implements script.Host (divider up = agent shrinks).
func (s *scriptHost) ResizeSplitUp() {
	s.deferModelMutation(func(m *home) { m.resizeSplit(-0.05) })
}

// ResizeSplitDown implements script.Host.
func (s *scriptHost) ResizeSplitDown() {
	s.deferModelMutation(func(m *home) { m.resizeSplit(+0.05) })
}
```

5. `app/app.go` — the helpers:

```go
// jumpWaiting moves the selection to the next/prev instance needing
// attention (Prompting or bell), wrapping around. No-op when none.
func (m *home) jumpWaiting(dir int) {
	items := m.list.GetInstances()
	n := len(items)
	if n == 0 {
		return
	}
	start := m.list.SelectedIdx()
	for i := 1; i <= n; i++ {
		idx := ((start+dir*i)%n + n) % n
		inst := items[idx]
		if inst.GetStatus() == session.Prompting || inst.BellPending() {
			m.list.SetSelectedInstance(idx)
			return
		}
	}
}

// resizeSplit adjusts the agent/terminal ratio and persists it for the
// selected instance.
func (m *home) resizeSplit(delta float64) {
	r := m.splitPane.AdjustAgentRatio(delta)
	if sel := m.list.GetSelectedInstance(); sel != nil {
		title := sel.Title
		m.mutateUIPrefs(func(p *config.UIPrefs) {
			if p.SplitRatios == nil {
				p.SplitRatios = map[string]float64{}
			}
			p.SplitRatios[title] = r
		})
	}
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: m.lastWidth, Height: m.lastHeight})
}
```

6. `script/defaults.lua` — replace the Workspace block and add a Fleet block:

```lua
-- Workspace
cs.bind("W", function() cs.actions.open_workspace_picker() end, { help = "workspace" })
cs.bind("{", function() cs.actions.workspace_prev() end,        { help = "prev ws" })
cs.bind("l", function() cs.actions.workspace_prev() end)
cs.bind("}", function() cs.actions.workspace_next() end,        { help = "next ws" })
cs.bind(";", function() cs.actions.workspace_next() end)

-- Fleet: attention jumps and layout
cs.bind("]", function() cs.actions.next_waiting() end,          { help = "next waiting" })
cs.bind("[", function() cs.actions.prev_waiting() end,          { help = "prev waiting" })
cs.bind("\\", function() cs.actions.toggle_rail() end,          { help = "toggle rail" })
cs.bind("T", function() cs.actions.toggle_terminal_pane() end,  { help = "toggle terminal" })
cs.bind("ctrl+up",   function() cs.actions.resize_split_up() end,   { help = "split up" })
cs.bind("ctrl+down", function() cs.actions.resize_split_down() end, { help = "split down" })
```

- [ ] **Step 4: Run** — `CGO_ENABLED=0 go test ./script/ ./app/ ./ui/ 2>&1 | tail -5` — Expected: PASS
- [ ] **Step 5: Commit** — `git commit -am "feat(keys): attention jumps, rail/terminal toggles, split resize via Lua actions"`
- [ ] **Step 6: Manual smoke (end of phase 2)** — build & run with 2+ sessions: rail shows mini-cards with live tails; `\` hides rail; `T` hides terminal; `ctrl+up/down` resizes and survives restart; `]` jumps to a prompting session.

---

# Phase 3 — Overview mode

### Task 15: Overview component (`ui/overview.go`)

**Files:**
- Create: `ui/overview.go`, `ui/overview_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// ui/overview_test.go
package ui

import (
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func testOverviewData() OverviewData {
	items := []*session.Instance{
		{Title: "auth-refactor", Status: session.Running},
		{Title: "db-migration", Status: session.Prompting},
	}
	return OverviewData{
		ActiveName:  "loom",
		Items:       items,
		Order:       SortForOverview(items),
		SelectedIdx: 0,
	}
}

func TestOverview_RendersGroupHeaderAndCards(t *testing.T) {
	o := NewOverview()
	o.SetSize(120, 40)
	out := ansi.Strip(o.Render(testOverviewData()))
	assert.Contains(t, out, "LOOM · 2")
	assert.Contains(t, out, "db-migration")
	assert.Contains(t, out, "auth-refactor")
	assert.Contains(t, out, "awaiting input")
}

func TestOverview_ColumnsScaleWithWidth(t *testing.T) {
	assert.Equal(t, 1, overviewColumns(59))
	assert.Equal(t, 2, overviewColumns(120))
	assert.Equal(t, 3, overviewColumns(200))
}

func TestOverview_CollapseHidesCards(t *testing.T) {
	o := NewOverview()
	o.SetSize(120, 40)
	o.ToggleCollapse("loom")
	out := ansi.Strip(o.Render(testOverviewData()))
	assert.Contains(t, out, "▸ LOOM · 2")
	assert.NotContains(t, out, "db-migration")
}

func TestOverview_RendersPeersAsDimHeaders(t *testing.T) {
	o := NewOverview()
	o.SetSize(120, 40)
	d := testOverviewData()
	d.Peers = []PeerSection{{Name: "summa", Attention: 1, Running: 2, Idle: 0}}
	out := ansi.Strip(o.Render(d))
	assert.Contains(t, out, "SUMMA · 3")
}

func TestOverview_NeverExceedsHeight(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, 12)
	out := o.Render(testOverviewData())
	assert.LessOrEqual(t, len(strings.Split(out, "\n")), 12)
}
```

- [ ] **Step 2: Run** — `CGO_ENABLED=0 go test ./ui/ -run TestOverview_ -v` — Expected: FAIL

- [ ] **Step 3: Implement `ui/overview.go`**

```go
package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
)

// overviewCardTailLines is the live-tail depth on overview cards.
const overviewCardTailLines = 2

// OverviewData is everything Render needs, assembled by the app on the
// Update goroutine each frame (same pattern as List reading instances).
type OverviewData struct {
	ActiveName  string
	Items       []*session.Instance
	Order       []int // display order (indices into Items), from SortForOverview
	SelectedIdx int   // list index of the selected instance
	Peers       []PeerSection
	Spinner     string
}

// Overview renders the fleet-triage card grid: the active workspace's
// instances as bordered cards under a collapsible group header, peer
// workspaces as dimmed count headers (live selection stays scoped to
// the active workspace until cross-workspace lands).
type Overview struct {
	width, height int
	collapsed     map[string]bool
	rowOffset     int // first visible card row (scroll window)
}

// NewOverview constructs an empty overview component.
func NewOverview() *Overview {
	return &Overview{collapsed: make(map[string]bool)}
}

// SetSize sets the render bounds.
func (o *Overview) SetSize(w, h int) { o.width, o.height = w, h }

// ToggleCollapse flips a group's collapsed state (case-insensitive name).
func (o *Overview) ToggleCollapse(name string) {
	key := strings.ToLower(name)
	o.collapsed[key] = !o.collapsed[key]
}

// IsCollapsed reports a group's collapsed state.
func (o *Overview) IsCollapsed(name string) bool {
	return o.collapsed[strings.ToLower(name)]
}

// overviewColumns maps width to a 1–3 column grid.
func overviewColumns(width int) int {
	cols := width / 60
	if cols < 1 {
		cols = 1
	}
	if cols > 3 {
		cols = 3
	}
	return cols
}

// Render draws the overview. Height is hard-clamped.
func (o *Overview) Render(d OverviewData) string {
	if o.width == 0 || o.height == 0 {
		return ""
	}
	var b strings.Builder

	header := fmt.Sprintf("▾ %s · %d", strings.ToUpper(d.ActiveName), len(d.Items))
	collapsed := o.IsCollapsed(d.ActiveName)
	if collapsed {
		header = "▸" + header[len("▾"):]
	}
	wsStyle := lipgloss.NewStyle().Foreground(Workspace)
	b.WriteString(wsStyle.Render(header) + "\n")

	if !collapsed && len(d.Items) > 0 {
		b.WriteString(o.renderGrid(d))
	}

	if len(d.Peers) > 0 {
		dim := lipgloss.NewStyle().Foreground(Dim)
		b.WriteString("\n")
		for _, p := range d.Peers {
			total := p.Attention + p.Running + p.Idle
			line := fmt.Sprintf("▸ %s · %d", strings.ToUpper(p.Name), total)
			if p.Attention > 0 {
				line += lipgloss.NewStyle().Foreground(Attention).Render(fmt.Sprintf("  ❯%d waiting", p.Attention))
			}
			b.WriteString(dim.Render(line) + "\n")
		}
	}
	return clampHeight(lipgloss.Place(o.width, o.height, lipgloss.Left, lipgloss.Top, b.String()), o.height)
}

// renderGrid lays cards out in rows of overviewColumns, windowed so the
// selected card's row is always visible.
func (o *Overview) renderGrid(d OverviewData) string {
	cols := overviewColumns(o.width)
	cardW := (o.width - (cols - 1)) / cols

	cards := make([]string, 0, len(d.Order))
	selRow := 0
	for pos, idx := range d.Order {
		cd := BuildCardData(d.Items[idx], idx == d.SelectedIdx, d.Spinner, overviewCardTailLines)
		cd.Index = DisplayIndex(d.Items, idx)
		cards = append(cards, renderOverviewCard(cd, cardW))
		if idx == d.SelectedIdx {
			selRow = pos / cols
		}
	}

	rows := make([]string, 0, (len(cards)+cols-1)/cols)
	for i := 0; i < len(cards); i += cols {
		end := i + cols
		if end > len(cards) {
			end = len(cards)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(cards[i:end])...))
	}

	// Window rows so selRow stays visible within the height budget
	// (header + peers consumed elsewhere; approximate one card row's
	// height from its rendered line count).
	rowH := 1
	if len(rows) > 0 {
		rowH = len(strings.Split(rows[0], "\n"))
	}
	budget := o.height - 1 - peerBudget(d.Peers)
	visRows := budget / rowH
	if visRows < 1 {
		visRows = 1
	}
	if selRow < o.rowOffset {
		o.rowOffset = selRow
	}
	if selRow >= o.rowOffset+visRows {
		o.rowOffset = selRow - visRows + 1
	}
	if o.rowOffset > len(rows)-visRows {
		o.rowOffset = len(rows) - visRows
	}
	if o.rowOffset < 0 {
		o.rowOffset = 0
	}
	end := o.rowOffset + visRows
	if end > len(rows) {
		end = len(rows)
	}
	return strings.Join(rows[o.rowOffset:end], "\n")
}

func peerBudget(peers []PeerSection) int {
	if len(peers) == 0 {
		return 0
	}
	return len(peers) + 1
}

func joinWithGap(cards []string) []string {
	out := make([]string, 0, len(cards)*2)
	for i, c := range cards {
		if i > 0 {
			out = append(out, " ")
		}
		out = append(out, c)
	}
	return out
}

// renderOverviewCard renders one DensityCard box: border colored by
// attention/selection, title + status corner, branch + diff meta, rule,
// live tail.
func renderOverviewCard(d CardData, width int) string {
	inner := width - 2 // border columns
	if inner < 10 {
		inner = 10
	}
	borderColor := d.accentColor()

	titleFg := Text
	if d.NeedsAttention() {
		titleFg = Attention
	}
	title := lipgloss.NewStyle().Foreground(titleFg).Bold(d.Selected).Render(truncate(d.Title, inner-14))
	status := lipgloss.NewStyle().Foreground(statusFgFor(d)).Render(d.statusLabel())
	top := spreadLine(title, status, inner)

	dim := lipgloss.NewStyle().Foreground(Dim)
	meta := ""
	if d.HasDiff {
		meta = lipgloss.NewStyle().Foreground(OK).Render(fmt.Sprintf("+%d", d.DiffAdded)) + " " +
			lipgloss.NewStyle().Foreground(ErrorColor).Render(fmt.Sprintf("−%d", d.DiffRemoved))
	}
	mid := spreadLine(dim.Render(truncate(d.Branch, inner-12)), meta, inner)

	rule := lipgloss.NewStyle().Foreground(Rule).Render(strings.Repeat("─", inner))

	tails := make([]string, 0, overviewCardTailLines)
	for _, l := range d.TailLines {
		tails = append(tails, dim.Render(truncate(l, inner)))
	}
	for len(tails) < overviewCardTailLines {
		tails = append(tails, "")
	}

	content := strings.Join(append([]string{top, mid, rule}, tails...), "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(width).
		Render(content)
}

func statusFgFor(d CardData) interface{ RGBA() (r, g, b, a uint32) } {
	switch {
	case d.NeedsAttention():
		return Attention
	case d.Status == session.Running || d.Status == session.Loading:
		return Accent
	default:
		return Dim
	}
}

// spreadLine left-aligns l and right-aligns r within width cells.
func spreadLine(l, r string, width int) string {
	gap := width - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 1 {
		gap = 1
	}
	return l + strings.Repeat(" ", gap) + r
}
```

(As in Task 8: type the two color-returning helpers as `color.Color` with an `image/color` import.)

- [ ] **Step 4: Run** — Expected: PASS
- [ ] **Step 5: Commit** — `git commit -am "feat(ui): overview card-grid component with grouping and collapse"`

### Task 16: `viewMode` wiring — toggle, View branch, key routing

**Files:**
- Modify: `app/app.go` (`home` fields, `View`, `updateHandleWindowSizeEvent`, startup), `app/app_scripts.go` (ToggleOverview + mode-aware cursor), `app/state_default.go` (overview routing), `script/host.go` + `script/api_actions.go` + `script/host_fake_test.go` (one more action), `script/defaults.lua` (tab binding)

- [ ] **Step 1: Model + component wiring**

1. `app/app.go`:

```go
// viewMode selects the top-level presentation: focus (rail + panes) or
// overview (fleet card grid). Orthogonal to the state machine — only
// stateDefault key routing and View branch on it.
type viewMode int

const (
	viewFocus viewMode = iota
	viewOverview
)
```

Add to `home`: `viewMode viewMode` and `overview *ui.Overview`; init `overview: ui.NewOverview()` in `newHome`. Restore at startup inside `applyUIPrefs`: `if p.ViewMode == "overview" { m.viewMode = viewOverview } else { m.viewMode = viewFocus }`.

2. `updateHandleWindowSizeEvent`: add `m.overview.SetSize(msg.Width, contentHeight)` next to the splitPane/list sizing.
3. `View()`: replace the `listAndPreview` assembly with:

```go
	var mainContent string
	if m.viewMode == viewOverview && m.state != stateFileExplorer {
		mainContent = m.overview.Render(m.overviewData())
	} else {
		listView := ""
		if !m.railHidden {
			listView = m.list.String()
		}
		var rightContent string
		if m.state == stateFileExplorer && m.activeOverlay != nil {
			rightContent = m.activeOverlay.View()
		} else {
			rightContent = m.splitPane.String()
		}
		mainContent = lipgloss.JoinHorizontal(lipgloss.Top, listView, rightContent)
	}
```

and use `mainContent` where `listAndPreview` was used. Status line becomes mode-aware:

```go
		hint := "tab overview · ] next waiting · \\ rail · ? help · q quit"
		if m.viewMode == viewOverview {
			hint = "enter focus · ] next waiting · z collapse · tab focus · ? help"
		}
		statusLine := statusLineStyle.Render(hint)
```

`attachCursor` must not place a hardware cursor in overview mode: add `if m.viewMode == viewOverview { return }` at its top.

4. Data assembly on `home`:

```go
// overviewData snapshots the focused workspace + peers for the overview
// render. Update-goroutine only.
func (m *home) overviewData() ui.OverviewData {
	items := m.list.GetInstances()
	name := ""
	if m.activeCtx != nil {
		name = m.activeCtx.Name
	}
	var peers []ui.PeerSection
	for i, slot := range m.slots {
		if i == m.focusedSlot {
			continue
		}
		peers = append(peers, m.peerSectionFor(slot))
	}
	return ui.OverviewData{
		ActiveName:  name,
		Items:       items,
		Order:       ui.SortForOverview(items),
		SelectedIdx: m.list.SelectedIdx(),
		Peers:       peers,
		Spinner:     m.spinner.View(),
	}
}
```

Refactor Task 10's `refreshPeerSections` to extract the per-slot count loop into `peerSectionFor(slot workspaceSlot) ui.PeerSection` so both callers share it.

- [ ] **Step 2: `toggle_overview` action**

Host interface += `ToggleOverview()`; fake += counter; `installSyncActions` += entry; `defaults.lua` (Fleet block) += `cs.bind("tab", function() cs.actions.toggle_overview() end, { help = "overview" })`. Implementation:

```go
// ToggleOverview implements script.Host.
func (s *scriptHost) ToggleOverview() {
	s.deferModelMutation(func(m *home) {
		if m.viewMode == viewOverview {
			m.viewMode = viewFocus
		} else {
			m.viewMode = viewOverview
		}
		mode := ""
		if m.viewMode == viewOverview {
			mode = "overview"
		}
		m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = mode })
	})
}
```

- [ ] **Step 3: Mode-aware cursor + overview key routing**

1. `app/app_scripts.go` — change `CursorUp`/`CursorDown` to route through a helper:

```go
// CursorUp implements script.Host.
func (s *scriptHost) CursorUp() {
	s.deferModelMutation(func(m *home) { m.moveCursor(-1) })
}

// CursorDown implements script.Host.
func (s *scriptHost) CursorDown() {
	s.deferModelMutation(func(m *home) { m.moveCursor(1) })
}
```

2. `app/app.go`:

```go
// moveCursor advances the selection by dir: list order in focus mode,
// attention-sorted display order in overview mode.
func (m *home) moveCursor(dir int) {
	if m.viewMode != viewOverview {
		if dir < 0 {
			m.list.Up()
		} else {
			m.list.Down()
		}
		return
	}
	items := m.list.GetInstances()
	if len(items) == 0 {
		return
	}
	order := ui.SortForOverview(items)
	pos := 0
	for p, idx := range order {
		if idx == m.list.SelectedIdx() {
			pos = p
			break
		}
	}
	for i := 1; i <= len(order); i++ {
		np := pos + dir*i
		if np < 0 || np >= len(order) {
			return // no wrap in the grid
		}
		if items[order[np]].GetStatus() != session.Deleting {
			m.list.SetSelectedInstance(order[np])
			return
		}
	}
}
```

3. `app/state_default.go` — insert overview routing after the ctrl+c check and before the Esc block:

```go
	if m.viewMode == viewOverview {
		switch msg.String() {
		case "enter":
			m.viewMode = viewFocus
			m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "" })
			return m, m.instanceChanged()
		case "z":
			if m.activeCtx != nil {
				m.overview.ToggleCollapse(m.activeCtx.Name)
			}
			return m, nil
		case "esc":
			m.viewMode = viewFocus
			m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "" })
			return m, m.instanceChanged()
		}
		if !overviewKeyAllowed[msg.String()] {
			return m, nil
		}
	}
```

with, at file scope:

```go
// overviewKeyAllowed whitelists script-dispatched keys in overview mode.
// Everything else (attach, quick input, scroll, diff, file explorer) is
// focus-mode-only and no-ops here rather than acting on an invisible pane.
var overviewKeyAllowed = map[string]bool{
	"j": true, "k": true, "up": true, "down": true,
	"]": true, "[": true, "tab": true,
	"n": true, "N": true, "D": true, "r": true, "R": true,
	"q": true, "?": true, "W": true, "S": true,
	"{": true, "}": true, "l": true, ";": true,
	"K": true, "J": true, "g": true, "G": true,
}
```

(The existing Esc diff/scroll-mode handling stays where it is — it only runs in focus mode because the overview branch returns first.)

- [ ] **Step 4: Engine test for tab binding** — extend Task 14's script test with `"tab"` → `toggleOverviewCalls`, following the same pattern.

- [ ] **Step 5: Run everything** — `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./... 2>&1 | tail -10` — Expected: PASS
- [ ] **Step 6: Commit** — `git commit -am "feat(app): overview view mode with tab toggle, sorted grid cursor, and key whitelist"`

### Task 17: Regression pass — race, docs-critical suites

**Files:** none new

- [ ] **Step 1: Full suite** — `CGO_ENABLED=0 go test ./... 2>&1 | tail -15` — all green.
- [ ] **Step 2: Race detector** — `CC=clang CGO_ENABLED=1 go test -race ./... 2>&1 | tail -15` — all green (pins that card tails/overview only touch read-locked instance accessors).
- [ ] **Step 3: Vet + format** — `go vet ./... && gofmt -l .` — vet clean, gofmt output empty.
- [ ] **Step 4: Pinned regression suites explicitly** — `CGO_ENABLED=0 go test ./ui/ -run 'TestScroll|TestPaneBorder|TestSelection' -v && CGO_ENABLED=0 go test ./app/ -run 'TestStatusRedetect|Redetect' -v` — all green.
- [ ] **Step 5: Commit any fixes** — `git commit -am "test: regression fixes for mission-control UI"` (skip if no changes).

### Task 18: Documentation

**Files:**
- Modify: `CLAUDE.md` (TUI Keybindings table + a Gotchas bullet), `USAGE.md` (keys/mode docs — find the keybinding section with `grep -n "Keybind\|keybind" USAGE.md`), `app/help.go` (in-app help text — `grep -n "workspace" app/help.go` to find the key list)

- [ ] **Step 1: Update CLAUDE.md keybindings table** — add/replace rows:

```
| `tab` | Toggle overview (fleet card grid) / focus mode |
| `]` / `[` | Jump to next/prev agent waiting for input |
| `\` | Toggle the session rail |
| `T` | Show/hide the terminal pane |
| `ctrl+up` / `ctrl+down` | Resize the agent/terminal split |
| `z` | (overview) collapse/expand workspace group |
| `l`/`{`, `;`/`}` | Previous/next workspace tab |
```

(remove the old `l`/`[`, `;`/`]` row). Add a Gotchas bullet:

```
- **Theme-derived styles must be hook-built.** Any package-level `lipgloss.Style` using a `ui` color role must be constructed inside a `ui.RegisterThemeHook` callback, not in a var initializer — init-time styles capture pre-`ApplyTheme` colors and go stale when the settings overlay switches themes live. Roles only, no literal colors (see `ui/theme.go`).
```

Also update the Persistent State section: `config.json` gains `Theme`; `state.json` gains the `ui` prefs block (view mode, rail/terminal visibility, per-session split ratios).

- [ ] **Step 2: Update USAGE.md and app/help.go** with the same key changes (match each file's existing phrasing/format).
- [ ] **Step 3: Commit** — `git commit -am "docs: mission-control keys, theme system, ui prefs"`
- [ ] **Step 4: Final manual smoke** — build, run with 6+ sessions across 2 workspaces:
  - overview (`tab`): cards grouped under the active workspace, peers as dimmed headers with counts; prompting card gold with wait age; `j`/`k` walk the sorted grid; `enter` focuses; `z` collapses; mode survives restart.
  - focus: rail cards show live tails; `]` cycles waiting agents; `\`/`T`/`ctrl+arrows` behave and persist.
  - themes: afterglow default, `S`→Theme→legacy restores the old look live.

---

## Self-review checklist (ran at authoring time)

- Spec coverage: theme registry+roles+hooks (T1–5), settings row (T6), status ages (T7), card densities Line/Rail (T8) and Card (T15), rail + peers (T9–10), prefs (T11), split controls (T12–13), Lua actions + `]`/`[` rebind (T14), overview grid/sort/collapse (T15), viewMode/tab/whitelist/mode-aware cursor (T16), regression+race (T17), docs (T18). Deviations called out in "Required background": `{`/`}` rebind, linear grid nav, overview-as-viewMode (not engine per-mode keymaps).
- Deferred to the phase-4 spec (per design doc): cross-workspace selection/focus, live cross-workspace groups beyond count headers.
- Type consistency: `ErrorColor` (not `Error`) everywhere; `PeerSection` lives in `ui/card.go` and is reused by list, overview, and app; `RailCardLines`/`RailHeaderLines` shared by list math and its tests; `CardData.Index` set post-`BuildCardData` in both call sites.
