# Remove Tab-Based Pane Selection — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remove `tab`, `i`, `enter`/`o`, and `shift+up`/`shift+down` keybindings, relying on `a`/`t`/`ctrl+a`/`ctrl+t` for all pane interaction.

**Architecture:** Remove dead key definitions, handlers, and UI state. Add an `inlineAttach` bool to `SplitPane` so focus highlighting only renders during inline attach. Reset focus to agent on ctrl+q exit so default state is deterministic. Simplify `O` to always attach to agent.

**Tech Stack:** Go, Bubble Tea, lipgloss

---

### Task 1: Remove key definitions from keys/keys.go

**Files:**
- Modify: `keys/keys.go:9-46` (const block), `keys/keys.go:49-79` (GlobalKeyStringsMap), `keys/keys.go:82-191` (GlobalkeyBindings)

**Step 1: Remove constants from the KeyName enum**

Remove these lines from the const block:

```go
// Remove these constants:
KeyTab        // line 20
KeyShiftUp    // line 29
KeyShiftDown  // line 30
KeyQuickInteract // line 36

// Also remove KeyEnter (line 12) — enter/o no longer triggers inline attach from stateDefault.
// KeySubmitName (line 21) stays — it's used by the new-instance overlay.
```

After removal, the const block should be:
```go
const (
	KeyUp KeyName = iota
	KeyDown
	KeyNew
	KeyKill
	KeyQuit
	KeyReview
	KeyPush
	KeySubmit

	KeySubmitName // SubmitName is a special keybinding for submitting the name of a new instance.

	KeyCheckout
	KeyResume
	KeyPrompt // New key for entering a prompt
	KeyHelp   // Key for showing help screen

	KeyWorkspace      // Key for switching workspaces
	KeyWorkspaceLeft  // Key for previous workspace tab
	KeyWorkspaceRight // Key for next workspace tab

	KeyFullScreenAttach // Key for full-screen attach (existing attach behavior)
	KeyDiff             // Key for toggling diff overlay

	KeyQuickInputAgent    // Key for quick input targeting agent pane
	KeyQuickInputTerminal // Key for quick input targeting terminal pane
	// ctrl+a/ctrl+t are only dispatched in stateDefault, so they don't conflict
	// with the textinput widget's ctrl+a (LineStart) binding in stateQuickInteract.
	KeyDirectAttachAgent    // Key for direct attach to agent pane
	KeyDirectAttachTerminal // Key for direct attach to terminal pane
)
```

**Step 2: Remove entries from GlobalKeyStringsMap**

Remove these entries:
```go
"shift+up":   KeyShiftUp,    // line 54
"shift+down": KeyShiftDown,  // line 55
"enter":      KeyEnter,      // line 57
"o":          KeyEnter,      // line 58
"tab":        KeyTab,        // line 62
"i":          KeyQuickInteract, // line 72
```

**Step 3: Remove entries from GlobalkeyBindings**

Remove these binding blocks:
```go
KeyShiftUp:       // lines 91-94
KeyShiftDown:     // lines 95-98
KeyEnter:         // lines 99-102
KeyTab:           // lines 131-134
KeyQuickInteract: // lines 154-157
```

**Step 4: Run tests to verify compilation**

Run: `cd /tb/Source/Personal/claude-squad/.claude-squad/worktrees/aidanb/remove-tab-selection_18a68ea2b47729e7 && CGO_ENABLED=0 go build ./keys/...`
Expected: Compilation errors in files that reference removed keys (app/app.go, ui/menu.go, etc.) — that's fine, we fix those next.

**Step 5: Commit**

Do NOT commit yet — wait until all files compile.

---

### Task 2: Clean up UI layer

**Files:**
- Modify: `ui/split_pane.go:45-57,101-108,245-246,252-269`
- Modify: `ui/quick_input.go:18-25,80-93`
- Modify: `ui/quick_input_test.go` (all tests)
- Modify: `ui/menu.go:47-56,131-166`

**Step 1: Remove ToggleFocus() and add inlineAttach to SplitPane**

In `ui/split_pane.go`:

Add `inlineAttach` field to struct (line 51):
```go
type SplitPane struct {
	agent    *PreviewPane
	terminal *TerminalPane
	diff     *DiffPane

	focusedPane  int
	inlineAttach bool
	diffVisible  bool

	height int
	width  int

	instance *session.Instance
}
```

Remove the `ToggleFocus()` method entirely (lines 101-108).

Add `SetInlineAttach` method (next to SetFocusedPane):
```go
// SetInlineAttach toggles whether inline-attach mode is active,
// controlling whether the focused-pane highlight is rendered.
func (s *SplitPane) SetInlineAttach(attached bool) {
	s.inlineAttach = attached
}
```

**Step 2: Modify String() to only highlight during inline attach**

In `ui/split_pane.go`, change lines 245-246 to:
```go
	showFocus := s.inlineAttach
	agentBox := s.renderPane(" Agent ", s.agent.String(), s.agent.height, showFocus && s.focusedPane == FocusAgent)
	terminalBox := s.renderPane(" Terminal ", s.terminal.String(), s.terminal.height, showFocus && s.focusedPane == FocusTerminal)
```

**Step 3: Remove QuickInputTargetFocused**

In `ui/quick_input.go`, change the const block (lines 21-25) to:
```go
const (
	QuickInputTargetAgent    QuickInputTarget = iota // always send to agent
	QuickInputTargetTerminal                         // always send to terminal
)
```

Remove the default case in `View()` (lines 88-89). The switch becomes:
```go
	switch q.Target {
	case QuickInputTargetAgent:
		hintText = "Enter to send to agent · Esc to cancel"
	case QuickInputTargetTerminal:
		hintText = "Enter to send to terminal · Esc to cancel"
	}
```

**Step 4: Update quick_input_test.go**

Replace all `QuickInputTargetFocused` with `QuickInputTargetAgent` (lines 11, 20, 27, 34, 41).

Remove the `QuickInputTargetFocused` test case from `TestQuickInputBar_ViewHintByTarget` (line 50). Update the remaining cases:
```go
func TestQuickInputBar_ViewHintByTarget(t *testing.T) {
	tests := []struct {
		target   QuickInputTarget
		contains string
	}{
		{QuickInputTargetAgent, "send to agent"},
		{QuickInputTargetTerminal, "send to terminal"},
	}
	for _, tt := range tests {
		bar := NewQuickInputBar(tt.target)
		bar.SetWidth(80)
		assert.Contains(t, bar.View(), tt.contains)
	}
}
```

**Step 5: Clean up menu.go**

In `ui/menu.go`:

Remove `focusedPane` field from Menu struct (line 52) and from NewMenu init (line 68).

Remove `SetFocusedPane` method entirely (lines 101-105).

In `addInstanceOptions()` (lines 131-166), update the action and system groups:

```go
func (m *Menu) addInstanceOptions() {
	if m.instance != nil && m.instance.Status == session.Loading {
		m.options = []keys.KeyName{keys.KeyNew, keys.KeyHelp, keys.KeyQuit}
		return
	}

	options := []keys.KeyName{keys.KeyNew}
	if !m.instance.IsWorkspaceTerminal {
		options = append(options, keys.KeyKill)
	}

	// Action group — direct pane targeting keys
	actionGroup := []keys.KeyName{}
	if !m.instance.IsWorkspaceTerminal {
		actionGroup = append(actionGroup, keys.KeySubmit)
		if m.instance.Status == session.Paused {
			actionGroup = append(actionGroup, keys.KeyResume)
		} else {
			actionGroup = append(actionGroup, keys.KeyCheckout)
		}
	}

	// System group
	systemGroup := []keys.KeyName{keys.KeyDiff, keys.KeyHelp, keys.KeyQuit}

	options = append(options, actionGroup...)
	options = append(options, systemGroup...)
	m.options = options
}
```

**Step 6: Verify UI package compiles**

Run: `cd /tb/Source/Personal/claude-squad/.claude-squad/worktrees/aidanb/remove-tab-selection_18a68ea2b47729e7 && CGO_ENABLED=0 go build ./ui/...`
Expected: May still fail due to app.go references — that's fine.

---

### Task 3: Update app.go handlers

**Files:**
- Modify: `app/app.go:804-817,854-865,960-1029,1128-1138,1196-1207,1232-1263`

**Step 1: Remove dead key handlers from handleKeyPress**

In the `switch name` block in `handleKeyPress`, remove these case blocks:

- `case keys.KeyShiftUp:` (lines 1020-1022)
- `case keys.KeyShiftDown:` (lines 1023-1025)
- `case keys.KeyTab:` (lines 1026-1029)
- `case keys.KeyEnter:` (lines 1128-1138)
- `case keys.KeyQuickInteract:` (lines 1196-1207)

**Step 2: Remove QuickInputTargetFocused routing**

In `stateQuickInteract` handler (around lines 854-865), remove the default case:

```go
switch m.quickInputBar.Target {
case ui.QuickInputTargetTerminal:
	err = m.splitPane.SendTerminalPrompt(text)
case ui.QuickInputTargetAgent:
	err = selected.SendPrompt(text)
}
```

**Step 3: Set inlineAttach on state transitions**

When entering inline attach (ctrl+a handler, line ~1148; ctrl+t handler, line ~1160):
```go
m.splitPane.SetInlineAttach(true)
```

When exiting inline attach (ctrl+q handler, line ~813):
```go
m.splitPane.SetInlineAttach(false)
m.splitPane.SetFocusedPane(ui.FocusAgent) // reset to deterministic default
```

Also in the guard at the top of the stateInlineAttach block (line ~807-809, when selected is nil/paused/dead):
```go
m.splitPane.SetInlineAttach(false)
m.splitPane.SetFocusedPane(ui.FocusAgent)
m.state = stateDefault
m.menu.SetState(ui.StateDefault)
return m, tea.WindowSize()
```

**Step 4: Simplify O (full-screen attach) to always attach to agent**

Replace the `KeyFullScreenAttach` handler (lines 1232-1263) with:
```go
case keys.KeyFullScreenAttach:
	if m.list.NumInstances() == 0 {
		return m, nil
	}
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || selected.Status == session.Loading || !selected.TmuxAlive() {
		return m, nil
	}
	m.showHelpScreen(helpTypeInstanceAttach{}, func() {
		ch, err := m.list.Attach()
		if err != nil {
			m.handleError(err)
			return
		}
		<-ch
		m.state = stateDefault
	})
	return m, nil
```

**Step 5: Remove menu.SetFocusedPane calls**

Remove the call `m.menu.SetFocusedPane(...)` — this was only in the (now removed) KeyTab handler.

**Step 6: Build the entire project**

Run: `CGO_ENABLED=0 go build -o /dev/null .`
Expected: PASS (clean build)

**Step 7: Commit**

```bash
git add keys/keys.go ui/split_pane.go ui/quick_input.go ui/quick_input_test.go ui/menu.go app/app.go
git commit -m "refactor: remove tab, i, enter/o, shift+up/down keybindings

Direct pane targeting via a/t/ctrl+a/ctrl+t replaces indirect
focus-then-act patterns. Focus highlight only renders during
inline attach. O always attaches to agent."
```

---

### Task 4: Update tests

**Files:**
- Modify: `app/app_test.go:437-457,505`

**Step 1: Remove TestScrollDoesNotTriggerInstanceChanged**

Delete the entire test (lines 437-457) — it tests removed shift+up/down handlers.

**Step 2: Verify TestAutoFocusAgentAfterInstanceStart still passes**

This test (lines 466-507) verifies auto-focus into inline attach with agent pane — still valid. The assertion on line 505 (`stateInlineAttach`) and line 506 (`FocusAgent`) are unchanged.

**Step 3: Run all tests**

Run: `cd /tb/Source/Personal/claude-squad/.claude-squad/worktrees/aidanb/remove-tab-selection_18a68ea2b47729e7 && go test ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add app/app_test.go
git commit -m "test: remove scroll handler test for removed keybindings"
```

---

### Task 5: Update documentation

**Files:**
- Modify: `CLAUDE.md` (keybinding table, lines ~88-101)
- Modify: `USAGE.md` (multiple sections)

**Step 1: Update CLAUDE.md keybinding table**

Remove these rows from the TUI Keybindings table:
```
| `enter`/`o` | Inline attach (interactive preview) |
| `i` | Quick input bar (send to focused pane) |
| `tab` | Switch focus (agent/terminal) |
| `shift+up`/`shift+down` | Scroll focused pane |
```

**Step 2: Update USAGE.md**

- Line 55-57: Remove references to `Tab` switching and `Enter` attaching in the quick-start section
- Line 107: Remove "Cycle between tabs with `Tab`. Scroll content with `Shift+↑` / `Shift+↓`."
- Line 111: Update terminal section — remove "Press `Enter` to attach", mention `ctrl+a`/`ctrl+t` instead
- Lines 195-203: Remove `Enter`/`o`, `Tab`, `Shift+↑`/`Shift+↓`, `Esc` (exit scroll mode) from keybinding table
- Lines 221-231: In the prompt overlay section, `Tab` cycling between focus areas is a Bubble Tea textinput feature, NOT our custom tab — leave it alone
- Lines 284-285, 293-295, 300: Update workflow examples to use `ctrl+a`/`ctrl+t` instead of Enter/Tab

**Step 3: Commit**

```bash
git add CLAUDE.md USAGE.md
git commit -m "docs: update keybinding references for removed keys"
```

---

### Task 6: Final verification

**Step 1: Run full test suite**

Run: `go test -v ./...`
Expected: All PASS

**Step 2: Build binary**

Run: `CGO_ENABLED=0 go build -o claude-squad`
Expected: Clean build

**Step 3: Run linter**

Run: `golangci-lint run --timeout=3m --fast`
Expected: No new warnings
