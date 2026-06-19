# Bubble Tea v2 Migration (Phase 0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate Loom from Bubble Tea v1.3.4 / Lip Gloss v1.0.0 / Bubbles v0.20.0 to their v2 releases with **zero user-visible behavior change**, as the enabling prerequisite (Phase 0) for the embedded-VT work in `docs/superpowers/specs/2026-06-19-native-terminal-experience-design.md`.

**Architecture:** v2's `/v2` modules cannot coexist with v1 in one compilable build, so this is an **atomic cut**: bump deps → rewrite imports → fix every broken API call → green build → fix tests → verify. There is no incremental-green path across the import boundary; the build is red from the moment `go.mod` changes until the last API edit lands. We therefore commit a behavior-capturing test pass *before* the cut, do the cut as one ordered sequence, and lean on the existing test suite (run directly via `Update()`, no `teatest`) plus a manual smoke of the risk hotspots to prove parity.

**Tech Stack:** Go 1.23 (toolchain 1.24.1), `CGO_ENABLED=0` builds, vendored deps (`vendor/`). Bubble Tea v2 (`charm.land/bubbletea/v2`), Lip Gloss v2 (`charm.land/lipgloss/v2`), Bubbles v2 (`charm.land/bubbles/v2/*`).

---

## Verified v2 API facts (the contract every code block below relies on)

Confirmed against the official `UPGRADE_GUIDE_V2.md` files and v2 source for each module:

- **Imports use the `charm.land` vanity domain**, not `github.com/charmbracelet`: `charm.land/bubbletea/v2` (pkg `tea`), `charm.land/lipgloss/v2` (pkg `lipgloss`), `charm.land/bubbles/v2/<sub>`.
- **`Model.View()` returns `tea.View`** (a struct), not `string`. Construct with `tea.NewView(s)`. Alt-screen and mouse move onto it: `v.AltScreen = true`, `v.MouseMode = tea.MouseModeCellMotion`. `tea.NewProgram` no longer takes `WithAltScreen()`/`WithMouseCellMotion()`.
- **Keys:** `tea.KeyMsg` is now an *interface*; presses arrive as `tea.KeyPressMsg{Code rune, Mod KeyMod, Text string}`. `msg.Type` and the `tea.KeyCtrlX`/`tea.KeyRunes` consts are **removed**. Named codes survive as runes (`tea.KeyEnter`, `tea.KeyEsc`, `tea.KeyTab`, `tea.KeyUp`, `tea.KeyF1`, `tea.KeySpace`, `tea.KeyBackspace`, …). `msg.String()` still returns `"ctrl+c"`, `"enter"`, `"esc"`, `"alt+a"`, `"shift+tab"` — **but space is now `"space"`, not `" "`**. Modifiers: `msg.Mod.Contains(tea.ModAlt|tea.ModCtrl|tea.ModShift)`.
- **Mouse:** split into `tea.MouseClickMsg` / `tea.MouseMotionMsg` / `tea.MouseReleaseMsg` / `tea.MouseWheelMsg`. Coordinates via `msg.Mouse()` → `Mouse{X, Y, Button, Mod}`. Wheel buttons are `tea.MouseWheelUp` / `tea.MouseWheelDown` (renamed from `tea.MouseButtonWheelUp/Down`). No `Action` gate — the wheel **is** its own message type.
- **Viewport:** `viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))`; `Width`/`Height`/`YOffset` are now methods, with `SetWidth`/`SetHeight`/`SetYOffset` setters. `HalfViewUp/Down` → `HalfPageUp/Down`. `SetContent`/`AtBottom`/`GotoTop`/`GotoBottom`/`ScrollPercent` retained. `LineUp/LineDown` are **uncertain** — we route line-scroll through the always-present `SetYOffset(YOffset()±1)` primitive.
- **TextInput:** `.Width` → `SetWidth()`; `.Prompt`/`.CharLimit` retained as fields.
- **TextArea:** `.ShowLineNumbers`/`.Prompt`/`.CharLimit` retained as fields; the focused cursor-line style moved from `ta.FocusedStyle.CursorLine` to `ta.Styles.Focused.CursorLine`. `textarea.Blink` retained.
- **Spinner:** `spinner.New`, `spinner.WithSpinner`, `spinner.MiniDot`, `m.spinner.Tick`, `spinner.TickMsg` all retained.
- **Lip Gloss:** `lipgloss.Color("#aabbcc")` still works and satisfies `Foreground(...)`. **`lipgloss.AdaptiveColor` is removed from the root package** → use `charm.land/lipgloss/v2/compat`.`AdaptiveColor{Light, Dark color.Color}` (fields now take `color.Color`, so wrap hex strings in `lipgloss.Color(...)`). `RoundedBorder()`, `JoinHorizontal`/`JoinVertical`, `Place`, `GetHorizontalFrameSize()` unchanged.
- **Commands:** `tea.Quit`/`tea.QuitMsg`/`tea.Batch`/`tea.Sequence`/`tea.Tick`/`tea.ExecProcess` unchanged. `tea.WindowSize()` (a `Cmd`) → **`tea.RequestWindowSize`** (itself a `Cmd`, i.e. `func() tea.Msg`) — drop the parens.

---

## File inventory (surface of the change)

`main.go` has **no** tea/lipgloss/bubbles references — untouched. Everything is under `app/` and `ui/`.

- **bubbletea importers (non-test, 21):** `app/app.go`, `app_scripts.go`, `help.go`, `intents.go`, `keybytes.go`, `state_confirm.go`, `state_default.go`, `state_file_explorer.go`, `state_inline_attach.go`, `state_new.go`, `state_orphan_recovery.go`, `state_prompt.go`, `state_quick_interact.go`, `state_workspace_picker.go`, `ui/quick_input.go`, `ui/overlay/{branchPicker,confirmationOverlay,file_explorer,iface,orphan_recovery,profilePicker,textInput,textOverlay,workspacePicker}.go`.
- **lipgloss importers (23):** all of `ui/*.go` + `ui/overlay/*.go` that style output (`list.go`, `menu.go`, `theme.go`, `preview.go`, `terminal.go`, `quick_input.go`, `err.go`, `diff.go`, `split_pane.go`, `workspace_tab_bar.go`, plus overlays).
- **bubbles importers:** `viewport` (`ui/preview.go`, `ui/terminal.go`, `ui/diff.go`, `ui/overlay/file_explorer.go`); `textinput` (`ui/quick_input.go`, `ui/overlay/file_explorer.go`); `textarea` (`ui/overlay/textInput.go`); `spinner` (`app/app.go`, `ui/list.go`); `key` (`keys/keys.go`).
- **test files that construct tea messages (must update in Task 11):** `app/keybytes_test.go`, `app/state_default_ctrlc_test.go`, `app/app_test.go`, `app/app_scripts_dispatch_test.go`, `app/state_default_scripts_test.go`, `app/migration_parity_test.go`, `app/script_dispatch_race_test.go`, `app/workspace_toggle_test.go`, `app/state_orphan_recovery_test.go`, `ui/overlay/orphan_recovery_test.go`, `ui/overlay/workspacePicker_test.go`, `ui/overlay/file_explorer_test.go`, `ui/overlay/iface_test.go`, `ui/quick_input_test.go`.

---

## Task 1: Baseline — confirm green at v1

**Files:** none (verification only)

- [ ] **Step 1: Confirm branch and clean tree**

Run: `git -C /tb/Source/Personal/loom/.loom/worktrees/aidanb/better-terminal-experience_18ba6f7b861675a2 status -sb && git branch --show-current`
Expected: branch `aidanb/better-terminal-experience`, working tree clean except the two committed docs.

- [ ] **Step 2: Baseline build + test (records the contract we must preserve)**

Run:
```bash
CGO_ENABLED=0 go build -o loom ./... 2>&1 | tail -20
go test ./app/... ./ui/... 2>&1 | tail -30
```
Expected: build succeeds; tests PASS. If anything is already red, stop and fix before migrating — the migration must not be blamed for a pre-existing failure.

- [ ] **Step 3: Note the keybytes contract**

`app/keybytes_test.go` asserts exact PTY byte output for every key class (Ctrl+A=0x01, Enter=0x0D, arrows=`\x1b[A`, Alt+a=`\x1b 'a'`, ShiftTab=`\x1b[Z`, F-keys, multibyte runes). **These byte expectations are the spec for `keyMsgToBytes` and must be identical after the cut.** No commit this task.

---

## Task 2: Dependency bump + vendor

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `vendor/` (regenerated)

- [ ] **Step 1: Pull the v2 modules**

Run:
```bash
cd /tb/Source/Personal/loom/.loom/worktrees/aidanb/better-terminal-experience_18ba6f7b861675a2
go get charm.land/bubbletea/v2@latest
go get charm.land/lipgloss/v2@latest
go get charm.land/bubbles/v2@latest
```
Expected: `go.mod` now requires the three `charm.land/.../v2` modules at concrete versions. (The old `github.com/charmbracelet/*` requires may linger until `go mod tidy` in Step 3.)

- [ ] **Step 2: Pin the versions in the plan record**

Run: `grep -E 'charm.land/(bubbletea|lipgloss|bubbles)' go.mod`
Expected: three lines with explicit versions. These are the pinned versions; note them in the commit message.

- [ ] **Step 3: Tidy + re-vendor**

Run:
```bash
go mod tidy
go mod vendor
```
Expected: `vendor/charm.land/...` trees appear; stale `vendor/github.com/charmbracelet/{bubbletea,lipgloss,bubbles}` are removed. Build is now **red** (imports still point at the old paths) — expected; do not commit yet.

> Companion libs stay on `github.com/...`: `muesli/ansi`, `muesli/reflow`, `muesli/termenv`, `mattn/go-runewidth`, `atotto/clipboard`. Do not rewrite those imports.

---

## Task 3: Repo-wide import-path rewrite

**Files:** every non-vendor `.go` file importing the three modules (see inventory).

- [ ] **Step 1: Rewrite the import strings**

Run (rewrites only first-party files; `vendor/` is excluded):
```bash
cd /tb/Source/Personal/loom/.loom/worktrees/aidanb/better-terminal-experience_18ba6f7b861675a2
grep -rl --include='*.go' 'charmbracelet/bubbletea\|charmbracelet/lipgloss\|charmbracelet/bubbles' . \
  | grep -v '/vendor/' \
  | xargs sed -i \
    -e 's#github.com/charmbracelet/bubbletea#charm.land/bubbletea/v2#g' \
    -e 's#github.com/charmbracelet/lipgloss#charm.land/lipgloss/v2#g' \
    -e 's#github.com/charmbracelet/bubbles/#charm.land/bubbles/v2/#g'
```

- [ ] **Step 2: Verify no first-party file references the old paths**

Run: `grep -rn --include='*.go' 'github.com/charmbracelet/\(bubbletea\|lipgloss\|bubbles\)' . | grep -v '/vendor/'`
Expected: **no output**. (The `tea`/`lipgloss` import aliases are preserved by the rewrite; only the path changed.)

- [ ] **Step 3: Build to surface the API errors**

Run: `CGO_ENABLED=0 go build ./... 2>&1 | tee /tmp/v2-build-errors.txt | tail -40`
Expected: a finite list of compile errors — these are exactly the API edits in Tasks 4–10. Keep `/tmp/v2-build-errors.txt` as the worklist. Still red; no commit.

---

## Task 4: Lip Gloss — `AdaptiveColor` → flatten-or-`compat`

**Files (AdaptiveColor sites):** `ui/theme.go`, `ui/list.go`, `ui/menu.go`, `ui/preview.go`, `ui/terminal.go`, `ui/quick_input.go`, `ui/err.go`, `ui/overlay/file_explorer.go`

The rule, applied per site:
1. **Identical `Light`/`Dark`** → replace the whole `lipgloss.AdaptiveColor{Light:"#x", Dark:"#x"}` with `lipgloss.Color("#x")` (no behavior change, no new import).
2. **Differing `Light`/`Dark`** → `compat.AdaptiveColor{Light: lipgloss.Color("#light"), Dark: lipgloss.Color("#dark")}` and add the import `"charm.land/lipgloss/v2/compat"` to that file.

- [ ] **Step 1: Flatten identical-value sites**

These are all identical and become `lipgloss.Color("#...")`:
- `ui/quick_input.go:33` `#808080`
- `ui/preview.go:13,125,229` `#1a1a1a`/`#dddddd` is **differing** (see Step 2); `#808080` at `:125,:229` identical → flatten. (`:13` differs — Step 2.)
- `ui/terminal.go:18` `#1a1a1a`/`#dddddd` **differs** (Step 2); `:21` `#808080` identical → flatten.
- `ui/menu.go:12,17,22` — inspect each; flatten any with identical Light/Dark.
- `ui/list.go:22,25,28,34,37,48,72,76,81,86` — all identical → flatten to `lipgloss.Color("#...")`.
- `ui/overlay/file_explorer.go:30` `#808080` identical → flatten.

Worked example (`ui/quick_input.go:33`):
```go
// BEFORE
quickInputHintStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"})
// AFTER
quickInputHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
```

- [ ] **Step 2: Convert genuinely-adaptive sites to `compat.AdaptiveColor`**

Differing-value sites (`Light != Dark`): `ui/theme.go:10,13,16`; `ui/list.go:52,53,57,58,62,63,67,68`; `ui/preview.go:13,100` (the `:100` block) ; `ui/terminal.go:18`; `ui/err.go:18`.

Add `"charm.land/lipgloss/v2/compat"` to each such file's imports, then:
```go
// BEFORE (ui/theme.go:10)
BorderActive = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
// AFTER
BorderActive = compat.AdaptiveColor{Light: lipgloss.Color("#874BFD"), Dark: lipgloss.Color("#7D56F4")}
```
Do the same for `BorderMuted` (`#999999`/`#555555`), `TextDim` (`#999999`/`#666666`), each `ui/list.go` selection background/foreground pair, the `ui/preview.go:100` adaptive block, `ui/terminal.go:18` (`#1a1a1a`/`#dddddd`), and `ui/err.go:18`.

- [ ] **Step 3: Build the `ui` package**

Run: `CGO_ENABLED=0 go build ./ui/... 2>&1 | grep -i adaptivecolor`
Expected: no `AdaptiveColor` errors remain (other v2 errors in `ui` are addressed in Tasks 5–6). If `compat.AdaptiveColor` fails to satisfy a `color.Color` parameter, confirm the exact `compat` symbol with `go doc charm.land/lipgloss/v2/compat.AdaptiveColor` and adjust. No commit (build still red overall).

---

## Task 5: Bubbles viewport — constructor + field→method

**Files:** `ui/preview.go`, `ui/terminal.go`, `ui/diff.go`, `ui/overlay/file_explorer.go`

- [ ] **Step 1: Constructors**

Replace each `viewport.New(0, 0)` (`preview.go:40`, `terminal.go:50`, `diff.go:39`, `file_explorer.go:76`):
```go
// BEFORE
viewport: viewport.New(0, 0),
// AFTER
viewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
```

- [ ] **Step 2: Width/Height field assignments and reads**

Anywhere the code assigns `vp.Width = w` / `vp.Height = h`, change to `vp.SetWidth(w)` / `vp.SetHeight(h)`. Anywhere it reads `vp.Width`/`vp.Height`, change to `vp.Width()`/`vp.Height()`. Known read sites: `ui/preview.go:116` (`p.viewport.Width`), `ui/overlay/file_explorer.go:136,139,202,214,222,226` (`.Height`/`.Width`). (Grep each file for `.viewport.Width`/`.viewport.Height` to catch all.)

- [ ] **Step 3: YOffset field→method (`ui/overlay/file_explorer.go:224,226`)**

```go
// BEFORE
if cursorLine < f.viewport.YOffset {
    f.viewport.SetYOffset(cursorLine)
} else if cursorLine >= f.viewport.YOffset+f.viewport.Height {
    f.viewport.SetYOffset(cursorLine - f.viewport.Height + 1)
}
// AFTER
if cursorLine < f.viewport.YOffset() {
    f.viewport.SetYOffset(cursorLine)
} else if cursorLine >= f.viewport.YOffset()+f.viewport.Height() {
    f.viewport.SetYOffset(cursorLine - f.viewport.Height() + 1)
}
```

- [ ] **Step 4: Half-page rename + line-scroll via SetYOffset**

`HalfViewUp()`/`HalfViewDown()` → `HalfPageUp()`/`HalfPageDown()` at `preview.go:274,286`, `diff.go:134,139`, `terminal.go:452,463`.

`LineUp(1)`/`LineDown(1)` (uncertain in v2) → confirmed-stable primitive at `preview.go:248,260`, `diff.go:124,129`, `terminal.go:428,439`:
```go
// BEFORE
p.viewport.LineUp(1)
// AFTER
p.viewport.SetYOffset(p.viewport.YOffset() - 1)

// BEFORE
p.viewport.LineDown(1)
// AFTER
p.viewport.SetYOffset(p.viewport.YOffset() + 1)
```
`GotoTop`/`GotoBottom`/`AtBottom`/`ScrollPercent`/`SetContent` are unchanged — leave them.

- [ ] **Step 5: Build the viewport-touching files**

Run: `CGO_ENABLED=0 go build ./ui/... 2>&1 | grep -i 'viewport\|YOffset\|HalfView\|LineUp\|LineDown'`
Expected: no viewport-related errors. No commit yet.

---

## Task 6: Bubbles textinput + textarea

**Files:** `ui/quick_input.go`, `ui/overlay/file_explorer.go`, `ui/overlay/textInput.go`

- [ ] **Step 1: textinput `.Width` → `SetWidth` (`ui/quick_input.go:76`, `ui/overlay/file_explorer.go:103`)**

```go
// BEFORE (quick_input.go:76)
q.textInput.Width = w - 4 // account for prompt and padding
// AFTER
q.textInput.SetWidth(w - 4) // account for prompt and padding
```
`ti.Prompt = "> "` (`quick_input.go:46`) and `ti.CharLimit = 256` (`:48`) are **unchanged** (fields retained).

- [ ] **Step 2: textarea focused cursor-line style (`ui/overlay/textInput.go:93`)**

The one breaking textarea line. `.ShowLineNumbers`, `.Prompt`, `.CharLimit` stay as fields; only the style path moves:
```go
// BEFORE (newTextarea, textInput.go:87-96)
ti := textarea.New()
ti.SetValue(initialValue)
ti.Focus()
ti.ShowLineNumbers = false
ti.Prompt = ""
ti.FocusedStyle.CursorLine = lipgloss.NewStyle()
ti.CharLimit = 0
ti.MaxHeight = 0
return ti
// AFTER
ti := textarea.New()
ti.SetValue(initialValue)
ti.Focus()
ti.ShowLineNumbers = false
ti.Prompt = ""
ti.Styles.Focused.CursorLine = lipgloss.NewStyle() // v2: blank the focused cursor-line highlight
ti.CharLimit = 0
ti.MaxHeight = 0
return ti
```
Confirm two things with `go doc charm.land/bubbles/v2/textarea.Model` before building: (a) that `Styles` is pre-populated by `New()` (it is — if not, prepend `ti.Styles = textarea.DefaultStyles(true)`); (b) that `MaxHeight` is still a field (if it moved to a setter, use `ti.SetMaxHeight(0)`). `textarea.Blink` (`textInput.go:115`) is unchanged.

- [ ] **Step 3: Build**

Run: `CGO_ENABLED=0 go build ./ui/... 2>&1 | grep -i 'textinput\|textarea\|CursorLine\|MaxHeight'`
Expected: none. No commit yet.

---

## Task 7: Overlay key-routing signature — `tea.KeyMsg` → `tea.KeyPressMsg`

**Files:** `ui/overlay/iface.go`, and every overlay `HandleKey`/key switch: `textInput.go`, `confirmationOverlay.go`, `workspacePicker.go`, `orphan_recovery.go`, `textOverlay.go`, `branchPicker.go`, `profilePicker.go`, `file_explorer.go`; plus `ui/quick_input.go:56`.

- [ ] **Step 1: Change the interface (`ui/overlay/iface.go:26`)**

```go
// BEFORE
HandleKey(msg tea.KeyMsg) (closed bool, cmd tea.Cmd)
// AFTER
HandleKey(msg tea.KeyPressMsg) (closed bool, cmd tea.Cmd)
```

- [ ] **Step 2: Change every implementer's signature**

For each overlay type, change the method receiver signature `HandleKey(msg tea.KeyMsg)` → `HandleKey(msg tea.KeyPressMsg)` (and any private key-switch helper that takes `tea.KeyMsg`). Same for `ui/quick_input.go:56` `HandleKeyPress(msg tea.KeyMsg)` → `HandleKeyPress(msg tea.KeyPressMsg)`.

- [ ] **Step 3: Rewrite `msg.Type` switches to `msg.Code` / `msg.String()`**

The transformation rule:
- `msg.Type == tea.KeyEsc|KeyEnter|KeyTab|KeyLeft|KeyRight|KeyUp|KeyDown|KeyHome|KeyEnd|KeyPgUp|KeyPgDown` → `msg.Code == tea.KeyEsc` (etc. — those codes survive).
- `msg.Type == tea.KeyCtrlP|KeyCtrlN|KeyCtrlQ` (consts removed) → `msg.String() == "ctrl+p"|"ctrl+n"|"ctrl+q"`.
- `case tea.KeyShiftTab` → `msg.String() == "shift+tab"`.
- `case tea.KeyRunes` / `string(msg.Runes)` → use `msg.Text` (a `string`); the "is this a typed character?" guard becomes `if msg.Text != ""`.
- `case tea.KeySpace` → `msg.Code == tea.KeySpace` (or `msg.String() == "space"`).

Worked example — `ui/quick_input.go:56-66`:
```go
// BEFORE
func (q *QuickInputBar) HandleKeyPress(msg tea.KeyMsg) QuickInputAction {
	switch msg.Type {
	case tea.KeyEnter:
		return QuickInputSubmit
	case tea.KeyEsc:
		return QuickInputCancel
	default:
		q.textInput, _ = q.textInput.Update(msg)
		return QuickInputContinue
	}
}
// AFTER
func (q *QuickInputBar) HandleKeyPress(msg tea.KeyPressMsg) QuickInputAction {
	switch msg.Code {
	case tea.KeyEnter:
		return QuickInputSubmit
	case tea.KeyEsc:
		return QuickInputCancel
	default:
		q.textInput, _ = q.textInput.Update(msg)
		return QuickInputContinue
	}
}
```
Worked example — branch filter accumulation (`ui/overlay/branchPicker.go:86`): `bp.filter += string(msg.Runes)` → `bp.filter += msg.Text`. The enclosing `case tea.KeyRunes:` becomes the `default:` arm guarded by `if msg.Text != ""`.

Apply the rule to the remaining `switch msg.Type` blocks: `file_explorer.go:115-138`, `branchPicker.go:65-89`, `profilePicker.go:46-52`, `textInput.go:184-223`. The `switch msg.String()` blocks in `confirmationOverlay.go:45`, `workspacePicker.go:81`, `orphan_recovery.go:51` are **safe as-is** (string tokens preserved).

- [ ] **Step 4: Build the overlay package**

Run: `CGO_ENABLED=0 go build ./ui/... 2>&1 | grep -iE 'overlay|KeyMsg|msg.Type|msg.Runes'`
Expected: none. No commit yet.

---

## Task 8: Rewrite `app/keybytes.go` for v2 (highest-risk)

**Files:**
- Modify: `app/keybytes.go`
- Test: `app/keybytes_test.go` (rewritten in Task 11; the byte expectations are unchanged)

- [ ] **Step 1: Replace the whole file**

```go
package app

import (
	tea "charm.land/bubbletea/v2"
)

// keyMsgToBytes converts a Bubble Tea key press back into raw terminal
// bytes suitable for writing to a tmux PTY. Returns nil for unmappable
// keys. The output is byte-for-byte identical to the v1 implementation;
// app/keybytes_test.go pins that contract.
func keyMsgToBytes(msg tea.KeyPressMsg) []byte {
	// Alt modifier: prefix ESC, then the key's own bytes (Alt stripped).
	if msg.Mod.Contains(tea.ModAlt) {
		inner := keyMsgToBytes(tea.KeyPressMsg{
			Code: msg.Code,
			Text: msg.Text,
			Mod:  msg.Mod &^ tea.ModAlt,
		})
		if inner == nil {
			return nil
		}
		return append([]byte{0x1b}, inner...)
	}

	// Ctrl + letter → control byte (Ctrl+A=0x01 .. Ctrl+Z=0x1A).
	if msg.Mod.Contains(tea.ModCtrl) && msg.Code >= 'a' && msg.Code <= 'z' {
		return []byte{byte(msg.Code - 'a' + 1)}
	}

	// Special keys (with any modifiers) that map to escape sequences.
	if seq := keySequence(msg); seq != "" {
		return []byte(seq)
	}

	// Named control keys whose code is a single control byte.
	switch msg.Code {
	case tea.KeyEnter:
		return []byte{0x0D}
	case tea.KeyTab:
		return []byte{0x09}
	case tea.KeyEsc:
		return []byte{0x1B}
	case tea.KeyBackspace:
		return []byte{0x7F}
	case tea.KeySpace:
		return []byte{0x20}
	}

	// Printable text (runes), including multi-byte UTF-8.
	if msg.Text != "" {
		return []byte(msg.Text)
	}
	return nil
}

// keySequence maps navigation / function / modifier-arrow keys to their
// standard xterm escape sequences, honoring Shift/Ctrl modifiers that v2
// now carries on msg.Mod rather than as distinct key types.
func keySequence(msg tea.KeyPressMsg) string {
	shift := msg.Mod.Contains(tea.ModShift)
	ctrl := msg.Mod.Contains(tea.ModCtrl)
	switch msg.Code {
	case tea.KeyUp:
		switch {
		case shift:
			return "\x1b[1;2A"
		case ctrl:
			return "\x1b[1;5A"
		default:
			return "\x1b[A"
		}
	case tea.KeyDown:
		switch {
		case shift:
			return "\x1b[1;2B"
		case ctrl:
			return "\x1b[1;5B"
		default:
			return "\x1b[B"
		}
	case tea.KeyRight:
		switch {
		case shift:
			return "\x1b[1;2C"
		case ctrl:
			return "\x1b[1;5C"
		default:
			return "\x1b[C"
		}
	case tea.KeyLeft:
		switch {
		case shift:
			return "\x1b[1;2D"
		case ctrl:
			return "\x1b[1;5D"
		default:
			return "\x1b[D"
		}
	case tea.KeyTab:
		if shift {
			return "\x1b[Z" // shift+tab; plain tab → 0x09 in keyMsgToBytes
		}
		return ""
	case tea.KeyHome:
		return "\x1b[H"
	case tea.KeyEnd:
		return "\x1b[F"
	case tea.KeyPgUp:
		return "\x1b[5~"
	case tea.KeyPgDown:
		return "\x1b[6~"
	case tea.KeyDelete:
		return "\x1b[3~"
	case tea.KeyInsert:
		return "\x1b[2~"
	case tea.KeyF1:
		return "\x1bOP"
	case tea.KeyF2:
		return "\x1bOQ"
	case tea.KeyF3:
		return "\x1bOR"
	case tea.KeyF4:
		return "\x1bOS"
	case tea.KeyF5:
		return "\x1b[15~"
	case tea.KeyF6:
		return "\x1b[17~"
	case tea.KeyF7:
		return "\x1b[18~"
	case tea.KeyF8:
		return "\x1b[19~"
	case tea.KeyF9:
		return "\x1b[20~"
	case tea.KeyF10:
		return "\x1b[21~"
	case tea.KeyF11:
		return "\x1b[23~"
	case tea.KeyF12:
		return "\x1b[24~"
	}
	return ""
}
```

- [ ] **Step 2: Confirm the named-key codes exist**

Run: `go doc charm.land/bubbletea/v2 | grep -E 'KeyUp|KeyTab|KeyEnter|KeySpace|KeyBackspace|KeyF1|KeyDelete|KeyInsert|KeyPgUp'`
Expected: each listed as a constant. If any name differs (e.g. `KeyPgUp` vs `KeyPageUp`), adjust the `case` and the test in Task 11 together. (Build verification happens after Task 11 rewrites the test.)

---

## Task 9: `app/` state handlers — key dispatch + `tea.WindowSize`

**Files:** `app/state_default.go`, `state_new.go`, `state_inline_attach.go`, `state_prompt.go`, `state_confirm.go`, `state_file_explorer.go`, `state_orphan_recovery.go`, `state_quick_interact.go`, `state_workspace_picker.go`, `app/help.go`, `app/intents.go`, `app/app_scripts.go`

- [ ] **Step 1: Change every per-state handler signature**

`handleKeyPress` and each `handleState*Key(m *home, msg tea.KeyMsg)` → `(m *home, msg tea.KeyPressMsg)`. (Grep `func handle.*KeyMsg` in `app/`.)

- [ ] **Step 2: `msg.Type` rewrites (the breaking comparisons)**

- `app/state_default.go:23`:
```go
// BEFORE
if msg.Type == tea.KeyEsc {
// AFTER
if msg.Code == tea.KeyEsc {
```
- `app/state_inline_attach.go:24`:
```go
// BEFORE
if msg.Type == tea.KeyCtrlQ {
// AFTER
if msg.String() == "ctrl+q" {
```
- `app/state_new.go:34-90`:
```go
// BEFORE
switch msg.Type {
case tea.KeyEnter:
	...
case tea.KeyRunes:
	if runewidth.StringWidth(instance.Title) >= 32 { ... }
	if err := instance.SetTitle(instance.Title + string(msg.Runes)); err != nil { ... }
case tea.KeyBackspace:
	...
case tea.KeySpace:
	if err := instance.SetTitle(instance.Title + " "); err != nil { ... }
case tea.KeyEsc:
	...
}
// AFTER
switch msg.Code {
case tea.KeyEnter:
	...
case tea.KeyBackspace:
	...
case tea.KeySpace:
	if err := instance.SetTitle(instance.Title + " "); err != nil { ... }
case tea.KeyEsc:
	...
default:
	// printable text (was tea.KeyRunes)
	if msg.Text == "" {
		break
	}
	if runewidth.StringWidth(instance.Title) >= 32 {
		return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
	}
	if err := instance.SetTitle(instance.Title + msg.Text); err != nil {
		return m, m.handleError(err)
	}
}
```
> `msg.String() == "ctrl+c"` checks (`state_default.go:20`, `state_new.go:17`, `state_prompt.go:17`) and `m.dispatchScript(msg.String())` (`state_default.go:45`) stay **as-is** — tokens preserved.

- [ ] **Step 3: `tea.WindowSize()` → `tea.RequestWindowSize` repo-wide**

Run: `grep -rn 'tea.WindowSize()' --include='*.go' app/ ui/ | grep -v '/vendor/'`
For each hit (incl. `state_new.go:22,50,70,97`, `state_inline_attach.go:20,29`, and any in `app.go`/`intents.go`), drop the parens:
```go
// BEFORE
return m, tea.WindowSize()
// AFTER
return m, tea.RequestWindowSize
```
Confirm the symbol: `go doc charm.land/bubbletea/v2.RequestWindowSize` (a `Cmd`, i.e. `func() tea.Msg`). If it's spelled differently, adjust all sites together.

---

## Task 10: `app/app.go` — program, mouse, dispatch, View

**Files:** `app/app.go`

- [ ] **Step 1: Program options (`app/app.go:75-79`)**

```go
// BEFORE
p := tea.NewProgram(
	h,
	tea.WithAltScreen(),
	tea.WithMouseCellMotion(), // Mouse scroll
)
// AFTER
p := tea.NewProgram(h) // alt-screen + mouse mode now set on the tea.View (see View())
```

- [ ] **Step 2: Top-level key dispatch (`app/app.go:805-806`)**

```go
// BEFORE
case tea.KeyMsg:
	return m.handleKeyPress(msg)
// AFTER
case tea.KeyPressMsg:
	return m.handleKeyPress(msg)
```
(`tea.KeyReleaseMsg` is not handled — releases simply don't match this case and are ignored, preserving v1 behavior.)

- [ ] **Step 3: Mouse handler (`app/app.go:733-789`)**

```go
// BEFORE
case tea.MouseMsg:
	if msg.Action == tea.MouseActionPress {
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if m.listWidth > 0 && msg.X < m.listWidth {
				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.list.Up()
				case tea.MouseButtonWheelDown:
					m.list.Down()
				}
				return m, m.instanceChanged()
			}
			selected := m.list.GetSelectedInstance()
			if selected == nil || selected.GetStatus() == session.Paused {
				return m, nil
			}
			switch {
			case m.splitPane.IsDiffVisible():
				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.splitPane.ScrollDiffUp()
				case tea.MouseButtonWheelDown:
					m.splitPane.ScrollDiffDown()
				}
			case msg.Y <= m.agentBottomY:
				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.splitPane.ScrollAgentUp()
				case tea.MouseButtonWheelDown:
					m.splitPane.ScrollAgentDown()
				}
			default:
				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.splitPane.ScrollTerminalUp()
				case tea.MouseButtonWheelDown:
					m.splitPane.ScrollTerminalDown()
				}
			}
		}
	}
	return m, nil
// AFTER
case tea.MouseWheelMsg:
	mouse := msg.Mouse()
	if mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown {
		if m.listWidth > 0 && mouse.X < m.listWidth {
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.list.Up()
			case tea.MouseWheelDown:
				m.list.Down()
			}
			return m, m.instanceChanged()
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.GetStatus() == session.Paused {
			return m, nil
		}
		switch {
		case m.splitPane.IsDiffVisible():
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.splitPane.ScrollDiffUp()
			case tea.MouseWheelDown:
				m.splitPane.ScrollDiffDown()
			}
		case mouse.Y <= m.agentBottomY:
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.splitPane.ScrollAgentUp()
			case tea.MouseWheelDown:
				m.splitPane.ScrollAgentDown()
			}
		default:
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.splitPane.ScrollTerminalUp()
			case tea.MouseWheelDown:
				m.splitPane.ScrollTerminalDown()
			}
		}
	}
	return m, nil
```

- [ ] **Step 4: `View() string` → `View() tea.View` (`app/app.go:2042-2097`)**

Wrap all three return paths and set alt-screen + mouse once. Introduce a local helper at the top of `View()`:
```go
// AFTER — signature + helper
func (m *home) View() tea.View {
	asView := func(content string) tea.View {
		v := tea.NewView(content)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}
	// ... existing body unchanged ...
```
Then change each `return` of a string to wrap it:
```go
// BEFORE (:2086)
return lipgloss.Place(m.lastWidth, m.lastHeight, lipgloss.Center, lipgloss.Center, m.activeOverlay.View())
// AFTER
return asView(lipgloss.Place(m.lastWidth, m.lastHeight, lipgloss.Center, lipgloss.Center, m.activeOverlay.View()))

// BEFORE (:2092)
return overlay.PlaceOverlay(0, 0, m.activeOverlay.View(), mainView, true, true)
// AFTER
return asView(overlay.PlaceOverlay(0, 0, m.activeOverlay.View(), mainView, true, true))

// BEFORE (:2096)
return mainView
// AFTER
return asView(mainView)
```

- [ ] **Step 5: Init — optional, leave as-is for parity**

`app/app.go:586-595` `Init()` stays `Init() tea.Cmd` returning the same `tea.Batch`. (Background-color detection for the LightDark color path is a later refinement, not part of this parity migration — the `compat.AdaptiveColor` path from Task 4 preserves current behavior without it.)

- [ ] **Step 6: Full build**

Run:
```bash
gofmt -w .
CGO_ENABLED=0 go build ./... 2>&1 | tail -40
```
Expected: **green build.** If errors remain, they are residual sites from Tasks 4–9 — fix using the same rules, then rebuild. Do not proceed until the build is clean.

- [ ] **Step 7: Commit the cut (first green commit)**

```bash
git add -A
git commit -m "chore(deps): migrate to Bubble Tea v2 / Lip Gloss v2 / Bubbles v2

Atomic cut to charm.land/*/v2: View()->tea.View with alt-screen+mouse on
the view, KeyPressMsg dispatch, split mouse messages, viewport field->method,
AdaptiveColor->compat/flatten, keybytes.go re-encoder rewritten for the new
Key model (byte output unchanged). No intended behavior change.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 11: Update tests for v2 message types

**Files:** the 14 test files listed in the inventory.

- [ ] **Step 1: Rewrite `app/keybytes_test.go` (the parity guard) — same byte expectations, v2 construction**

Replace every `tea.KeyMsg{Type:..., Runes:..., Alt:...}` with a `tea.KeyPressMsg`. The expected `[]byte` values are **unchanged**. Key conversions:
```go
// runes
tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}      → tea.KeyPressMsg{Code: 'a', Text: "a"}
// named control keys
tea.KeyMsg{Type: tea.KeyEnter}                          → tea.KeyPressMsg{Code: tea.KeyEnter}
tea.KeyMsg{Type: tea.KeyBackspace}                      → tea.KeyPressMsg{Code: tea.KeyBackspace}
tea.KeyMsg{Type: tea.KeyTab}                            → tea.KeyPressMsg{Code: tea.KeyTab}
tea.KeyMsg{Type: tea.KeyEsc}                            → tea.KeyPressMsg{Code: tea.KeyEsc}
tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}      → tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
tea.KeyMsg{Type: tea.KeySpace}                          → tea.KeyPressMsg{Code: tea.KeySpace}
// ctrl
tea.KeyMsg{Type: tea.KeyCtrlA}                          → tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
tea.KeyMsg{Type: tea.KeyCtrlC}                          → tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
// nav / fn (table: field `keyType tea.KeyType` → `code rune`, build {Code: tt.code})
tea.KeyMsg{Type: tea.KeyUp}                             → tea.KeyPressMsg{Code: tea.KeyUp}
// modifier arrows / shift+tab
tea.KeyMsg{Type: tea.KeyShiftTab}                       → tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
tea.KeyMsg{Type: tea.KeyShiftUp}                        → tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}
tea.KeyMsg{Type: tea.KeyCtrlRight}                      → tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}
// alt
tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: true} → tea.KeyPressMsg{Code: 'a', Text: "a", Mod: tea.ModAlt}
tea.KeyMsg{Type: tea.KeyUp, Alt: true}                  → tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}
// multibyte rune
tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'日'}}     → tea.KeyPressMsg{Code: '日', Text: "日"}
// the "unknown" case
tea.KeyMsg{Type: tea.KeyType(9999)}                     → tea.KeyPressMsg{Code: 0xF0000} // unmapped, no Text → nil
```
Update the two table types (`TestKeyMsgToBytes_ArrowKeys`, `_FunctionKeys`): change the struct field `keyType tea.KeyType` to `code rune` and `msg := tea.KeyPressMsg{Code: tt.code}`.

- [ ] **Step 2: Run the parity test FIRST**

Run: `go test ./app/ -run TestKeyMsgToBytes -v 2>&1 | tail -40`
Expected: **all PASS** with identical bytes. This proves the `keybytes.go` rewrite (Task 8) preserved the PTY contract. If any case differs, the bug is in `keyMsgToBytes`/`keySequence`, not the test — fix the implementation.

- [ ] **Step 3: Update the remaining test files**

Apply the same `tea.KeyMsg{Type:...}` → `tea.KeyPressMsg{Code:...}` conversion in: `app/state_default_ctrlc_test.go` (ctrl+c → `{Code:'c', Mod: tea.ModCtrl}`), `app/app_scripts_dispatch_test.go`, `app/state_default_scripts_test.go`, `app/migration_parity_test.go`, `app/script_dispatch_race_test.go`, `app/workspace_toggle_test.go`, `app/state_orphan_recovery_test.go`, `ui/overlay/orphan_recovery_test.go`, `ui/overlay/workspacePicker_test.go`, `ui/overlay/file_explorer_test.go`, `ui/overlay/iface_test.go`, `ui/quick_input_test.go`. `tea.QuitMsg` assertions (`app/app_test.go:704,760`; `app_scripts_dispatch_test.go:82-96`) are **unchanged** (`tea.QuitMsg` retained). Where a test passes a `tea.KeyMsg` to a `HandleKey`/`HandleKeyPress` now typed `tea.KeyPressMsg`, construct a `tea.KeyPressMsg` directly.

- [ ] **Step 4: Full suite**

Run: `go test ./... 2>&1 | tail -40`
Expected: all packages PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w .
git add -A
git commit -m "test: update tea message construction for Bubble Tea v2

Rewrite KeyMsg{Type,Runes,Alt} -> KeyPressMsg{Code,Text,Mod} across the
suite; keybytes byte-parity assertions unchanged.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 12: Lint, race, and CI-parity verification

**Files:** none (verification)

- [ ] **Step 1: gofmt gate (CI enforces)**

Run: `gofmt -l . | grep -v '/vendor/'`
Expected: no output. If files are listed, `gofmt -w .` them and amend the previous commit.

- [ ] **Step 2: Lint (CI uses golangci-lint v1.60.1)**

Run: `golangci-lint run --timeout=3m --fast 2>&1 | tail -40`
Expected: clean. Fix any new findings (likely unused imports from the AdaptiveColor flattening, or `ineffassign` from the mouse refactor).

- [ ] **Step 3: Race suite (per project memory: clang + CGO required locally)**

Run: `CC=clang CGO_ENABLED=1 go test -race ./... 2>&1 | tail -40`
Expected: PASS, no data races. (CI also runs `-race`.)

- [ ] **Step 4: CI build form**

Run: `CGO_ENABLED=0 go build -o loom 2>&1 | tail -5 && ls -la loom`
Expected: a `loom` binary is produced.

---

## Task 13: Manual smoke of the risk hotspots

**Files:** none (the parity proof a test suite can't give)

Run `./loom` against a throwaway workspace and verify each, in order. A failure here means a behavior regression the unit tests didn't catch.

- [ ] **Step 1: Render + alt-screen.** Launch — full-screen alt buffer, no flicker, list left + split panes right. (Guards the `View()→tea.View`/`AltScreen` change.)
- [ ] **Step 2: Mouse wheel routing.** Scroll over the list, the agent pane, the terminal pane, and the diff overlay (`d`) — each scrolls the surface under the cursor. (Guards the mouse rewrite + that `MouseMode` is set on the View; if nothing scrolls, the View lost `MouseMode`.)
- [ ] **Step 3: Lua keymap dispatch.** Exercise the CLAUDE.md keybindings — `n`, `N`, `d`, `D`, `W`, `[`/`]`/`l`/`;` (workspace tabs), `a`/`t` (quick input), `c`, `p`. Each fires the right action. (Guards `dispatchScript(msg.String())` token stability.)
- [ ] **Step 4: Inline attach key forwarding (the keybytes payoff).** `ctrl+a` into the agent pane; type text, arrows, Tab, Enter, **Ctrl+C** (must interrupt the agent), an Alt+key; confirm each reaches the agent. `ctrl+q` detaches cleanly. Repeat with `ctrl+t` for the terminal pane. (Guards `keyMsgToBytes`.)
- [ ] **Step 5: Overlays + text entry.** `N` → type a title (incl. a space and a multibyte char), Backspace, Enter; open the help (`?`), a confirmation (`D`), the workspace picker (`W`); file explorer scroll. Esc dismisses. (Guards the overlay `HandleKey`/`msg.Type` rewrites and textarea styles.)
- [ ] **Step 6: Full-screen attach + editor.** `alt+a` full-screen attach → `ctrl+q` returns to a correct alt-screen TUI with mouse still working; open `$EDITOR` from the file explorer and return. (Guards `tea.ExecProcess` + that alt-screen/mouse restore via `View()`.)
- [ ] **Step 7: Live ticks.** Start an instance; the spinner animates during Loading, the preview refreshes on agent output, and status transitions (Running/Ready) occur. (Guards `spinner.Tick`/`TickMsg` + the tick scheduling.)

If all seven pass, Phase 0 is complete and the codebase is ready for the embedded-VT work (Phase 1 of the spec).

---

## Self-review notes

- **Spec coverage:** This plan implements Phase 0 of `2026-06-19-native-terminal-experience-design.md` only. Phases 1–5 (embedded VT, scroll, selection, search, focus-to-interact) are deliberately out of scope and get their own plans.
- **Parity, not features:** every change targets behavioral equivalence. The one user-observable risk surfaces (keys, mouse, dispatch, attach) each have a dedicated manual check in Task 13 backed by a unit test where one exists (`keybytes_test.go` is the strongest).
- **`go doc` confirmation steps are actions, not placeholders:** three spots carry genuine v2-version uncertainty (`textarea.MaxHeight` field-vs-setter, `RequestWindowSize` spelling, named-key const spellings). Each has an exact `go doc` command and a specified fallback, so the engineer resolves them deterministically rather than guessing.
- **Atomicity is acknowledged:** Tasks 2–10 leave the build red by design; the first green commit is Task 10 Step 7. This is unavoidable because `/v2` and v1 modules cannot coexist.
