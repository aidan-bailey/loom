# Git Merge Hotkey Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user press `m` while not attached to a pane, pick another session by its existing list index, and merge that session's branch into the currently-focused session's worktree.

**Architecture:** A new `m` binding in `script/defaults.lua` calls a new deferred Lua action (`cs.actions.merge_selected()`), which enqueues a `MergeSessionsIntent`. The app-side handler (`runMergeSelected`) reuses the existing `selectedNotBusyNotWorkspace` gate, checks the target worktree is clean, and opens a new picker overlay (`ui/overlay/mergePicker.go`, modeled on `WorkspacePicker`) listing eligible source sessions labeled with their *original* index from the main list. Selecting a row and pressing Enter runs `git merge <branch>` via a new `GitWorktree.Merge` method, following the exact `pushActionFor` convention (a `tea.Cmd` returning a plain `error`, caught by `Update()`'s generic `case error:` branch — no new notification channel).

**Tech Stack:** Go 1.23, Bubble Tea v2, gopher-lua (embedded Lua scripting), testify.

**Design doc:** `docs/superpowers/specs/2026-07-01-git-merge-hotkey-design.md` — read this first for the full rationale behind each decision below.

---

## Task 1: Extract a shared display-index helper in `ui/list.go`

The main session list numbers rows 1-based, with a workspace terminal at position 0 getting the number `0` instead of `1` (`ui/list.go:437-442`). The merge picker (Task 7) needs to label its rows with these exact same numbers so a user who saw "session 3" in the main list can type `3` and land on it. Extract the existing inline calculation into an exported helper so both call sites share one source of truth.

**Files:**
- Modify: `ui/list.go:437-446`
- Create: `ui/list_display_index_test.go`

- [ ] **Step 1: Write the failing test**

Create `ui/list_display_index_test.go`:

```go
package ui

import (
	"testing"

	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisplayIndex_NoWorkspaceTerminal(t *testing.T) {
	items := []*session.Instance{
		{Title: "a"},
		{Title: "b"},
		{Title: "c"},
	}
	assert.Equal(t, 1, DisplayIndex(items, 0))
	assert.Equal(t, 2, DisplayIndex(items, 1))
	assert.Equal(t, 3, DisplayIndex(items, 2))
}

func TestDisplayIndex_LeadingWorkspaceTerminalOffsetsTheRest(t *testing.T) {
	items := []*session.Instance{
		{Title: "root", IsWorkspaceTerminal: true},
		{Title: "a"},
		{Title: "b"},
	}
	assert.Equal(t, 0, DisplayIndex(items, 0), "workspace terminal is numbered 0")
	assert.Equal(t, 1, DisplayIndex(items, 1))
	assert.Equal(t, 2, DisplayIndex(items, 2))
}

func TestDisplayIndex_EmptyItems(t *testing.T) {
	require.NotPanics(t, func() {
		DisplayIndex(nil, 0)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./ui/ -run TestDisplayIndex -v`
Expected: FAIL — `undefined: DisplayIndex`

- [ ] **Step 3: Add the exported helper and use it at the existing call site**

In `ui/list.go`, add this function near `InstanceRenderer.Render` (just above it, around line 226):

```go
// DisplayIndex returns the 1-based number shown in the list UI for the
// item at position i in items. A leading workspace terminal (position
// 0) is numbered 0, not 1, so every other item's displayed number is
// offset by one relative to its slice position. Exported so callers
// that build a session-index picker from the same items (e.g. the
// merge picker) label rows with the exact number the user already saw
// in the main list.
func DisplayIndex(items []*session.Instance, i int) int {
	wsOffset := 0
	if len(items) > 0 && items[0].IsWorkspaceTerminal {
		wsOffset = 1
	}
	return i + 1 - wsOffset
}
```

Then replace the inline calculation at the render loop (around line 437-446):

```go
	// Render only the visible window of items. Workspace terminal at index 0
	// gets number 0, regular instances are numbered starting from 1.
	wsOffset := 0
	if len(l.items) > 0 && l.items[0].IsWorkspaceTerminal {
		wsOffset = 1
	}
	for i := startIdx; i < endIdx; i++ {
		item := l.items[i]
		num := i + 1 - wsOffset
		b.WriteString(l.renderer.Render(item, num, i == l.selectedIdx, len(l.repos) > 1))
```

with:

```go
	// Render only the visible window of items. See DisplayIndex for the
	// workspace-terminal numbering rule.
	for i := startIdx; i < endIdx; i++ {
		item := l.items[i]
		num := DisplayIndex(l.items, i)
		b.WriteString(l.renderer.Render(item, num, i == l.selectedIdx, len(l.repos) > 1))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./ui/ -run TestDisplayIndex -v`
Expected: PASS (all three subtests)

- [ ] **Step 5: Run the full ui package test suite to confirm no regression**

Run: `go test ./ui/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add ui/list.go ui/list_display_index_test.go
git commit -m "refactor(ui): extract DisplayIndex helper for list numbering

Shares the list's 1-based numbering rule (workspace terminal at
position 0 is numbered 0) with the upcoming merge-session picker."
```

---

## Task 2: Add `GitWorktree.Merge`

**Files:**
- Modify: `session/git/worktree_git.go`
- Create: `session/git/merge_test.go`

- [ ] **Step 1: Write the failing tests**

Create `session/git/merge_test.go`:

```go
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupMergeFixture builds a repo with two linked worktrees: target
// (branch "target-branch", checked out at the init commit) and source
// (branch "source-branch", one commit ahead that edits README.md).
// Each test then adds its own additional target-side commit (or none,
// for the fast-forward case) before calling target.Merge(sourceBranch)
// to exercise fast-forward / merge-commit / conflict outcomes.
func setupMergeFixture(t *testing.T) (target *GitWorktree, sourceBranch string) {
	t.Helper()
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0755))

	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi\n"), 0644))
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "init")

	targetPath := filepath.Join(tmpDir, "target")
	runGit(t, repoDir, "worktree", "add", "-b", "target-branch", targetPath)

	sourcePath := filepath.Join(tmpDir, "source")
	sourceBranch = "source-branch"
	runGit(t, repoDir, "worktree", "add", "-b", sourceBranch, sourcePath)
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "README.md"), []byte("hi\nfrom source\n"), 0644))
	runGit(t, sourcePath, "add", ".")
	runGit(t, sourcePath, "commit", "-m", "source edits README")

	target = NewGitWorktreeFromStorage(repoDir, targetPath, "target", "target-branch", "", true, tmpDir)
	return target, sourceBranch
}

// hasMergeHead reports whether worktreePath is mid-merge, robust to the
// linked-worktree ".git is a file pointing elsewhere" layout.
func hasMergeHead(worktreePath string) bool {
	return exec.Command("git", "-C", worktreePath, "rev-parse", "-q", "--verify", "MERGE_HEAD").Run() == nil
}

func TestMerge_FastForward(t *testing.T) {
	target, sourceBranch := setupMergeFixture(t)

	err := target.Merge(sourceBranch)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(target.worktreePath, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "hi\nfrom source\n", string(content), "fast-forward merge should bring in source's README edit")
	assert.False(t, hasMergeHead(target.worktreePath))
}

func TestMerge_CreatesMergeCommit(t *testing.T) {
	target, sourceBranch := setupMergeFixture(t)

	// Diverge target with a commit on a different file so the merge
	// can't fast-forward but also can't conflict.
	require.NoError(t, os.WriteFile(filepath.Join(target.worktreePath, "target-only.txt"), []byte("t"), 0644))
	runGit(t, target.worktreePath, "add", ".")
	runGit(t, target.worktreePath, "commit", "-m", "target-only change")

	err := target.Merge(sourceBranch)
	require.NoError(t, err)

	out, err := exec.Command("git", "-C", target.worktreePath, "log", "-1", "--pretty=%P").Output()
	require.NoError(t, err)
	parents := strings.Fields(strings.TrimSpace(string(out)))
	assert.Len(t, parents, 2, "expected a merge commit with two parents")
}

func TestMerge_ConflictLeavesMergeHeadAndReturnsError(t *testing.T) {
	target, sourceBranch := setupMergeFixture(t)

	// Diverge target by editing the SAME line of README.md that the
	// source-branch commit also touches, guaranteeing a real conflict.
	require.NoError(t, os.WriteFile(filepath.Join(target.worktreePath, "README.md"), []byte("hi\nfrom target\n"), 0644))
	runGit(t, target.worktreePath, "add", ".")
	runGit(t, target.worktreePath, "commit", "-m", "target edits README")

	err := target.Merge(sourceBranch)

	require.Error(t, err)
	assert.True(t, hasMergeHead(target.worktreePath), "conflicted merge must leave MERGE_HEAD in place — Merge must not auto-abort")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./session/git/ -run TestMerge -v`
Expected: FAIL with `target.Merge undefined (type *GitWorktree has no field or method Merge)`

- [ ] **Step 3: Implement `Merge`**

In `session/git/worktree_git.go`, add after `CommitChanges` (after line 192):

```go
// Merge runs `git merge <sourceBranch>` in the worktree's directory,
// bringing another session's branch into this one. On conflict or any
// other non-zero exit, the git error/output is returned as-is and the
// merge is left exactly as git leaves it (MERGE_HEAD + conflict
// markers on a real conflict) — callers must not run `merge --abort`
// automatically, so a conflicted merge stays available for the user
// (or the agent) to resolve and commit.
func (g *GitWorktree) Merge(sourceBranch string) error {
	if _, err := g.runGitCommand(g.worktreePath, "merge", sourceBranch); err != nil {
		return fmt.Errorf("failed to merge %s: %w", sourceBranch, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./session/git/ -run TestMerge -v`
Expected: PASS (all three)

- [ ] **Step 5: Run the full git package test suite to confirm no regression**

Run: `go test ./session/git/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add session/git/worktree_git.go session/git/merge_test.go
git commit -m "feat(git): add GitWorktree.Merge

Runs plain 'git merge <branch>' in the worktree, matching
PushChanges/CommitChanges conventions. Never auto-aborts on
conflict — a conflicted merge is left in place for the user or
agent to resolve."
```

---

## Task 3: Add `MergeSessionsIntent`

**Files:**
- Modify: `script/intent.go`
- Modify: `script/intent_test.go`

- [ ] **Step 1: Write the failing test**

In `script/intent_test.go`, add to the `var _ Intent = ...` list inside `TestIntentTypesImplementInterface`:

```go
	var _ Intent = MergeSessionsIntent{}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./script/ -run TestIntentTypesImplementInterface -v`
Expected: FAIL — `undefined: MergeSessionsIntent`

- [ ] **Step 3: Add the intent type**

In `script/intent.go`, add after `ToggleFileExplorerIntent` (after line 94):

```go
// MergeSessionsIntent asks the app to open the merge-session picker for
// the currently-selected instance. The picker itself (and the merge it
// performs on commit) run entirely as plain Go state-handler code once
// opened — this intent's only job is getting the picker on screen.
type MergeSessionsIntent struct{}
```

And add the matching marker-method line alongside the others (after line 107):

```go
func (MergeSessionsIntent) intent()      {}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./script/ -run TestIntentTypesImplementInterface -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add script/intent.go script/intent_test.go
git commit -m "feat(script): add MergeSessionsIntent"
```

---

## Task 4: Add `cs.actions.merge_selected()`

**Files:**
- Modify: `script/api_actions.go`
- Modify: `script/api_actions_test.go`

- [ ] **Step 1: Write the failing test**

In `script/api_actions_test.go`, add:

```go
func TestCsActionsMergeSelectedEnqueues(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("m", function() cs.actions.merge_selected() end)`))
	e.EndLoad()

	h := dispatchExpectYield(t, e, "m")
	_, ok := h.enqueued[0].(MergeSessionsIntent)
	assert.True(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./script/ -run TestCsActionsMergeSelectedEnqueues -v`
Expected: FAIL — `cs.actions.merge_selected` is nil / Lua error

- [ ] **Step 3: Register the action**

In `script/api_actions.go`, add inside `installDeferredActions` after the `toggle_file_explorer` registration (after line 255, before the closing `}` of the function):

```go
	actions.RawSetString("merge_selected", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, MergeSessionsIntent{})
	}))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./script/ -run TestCsActionsMergeSelectedEnqueues -v`
Expected: PASS

- [ ] **Step 5: Run the full script package test suite to confirm no regression**

Run: `go test ./script/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add script/api_actions.go script/api_actions_test.go
git commit -m "feat(script): wire cs.actions.merge_selected()"
```

---

## Task 5: Bind `m` in `defaults.lua`

**Files:**
- Modify: `script/defaults.lua`
- Modify: `script/loader_defaults_test.go`

- [ ] **Step 1: Write the failing test**

In `script/loader_defaults_test.go`, add `"m"` to the list of keys asserted in `TestEngineLoadsEmbeddedDefaults`:

```go
	for _, k := range []string{
		"up", "k", "down", "j", "d",
		"n", "N", "D", "p", "c", "r", "?", "q", "m",
		"W", "[", "l", "]", ";",
		"alt+a", "alt+t", "ctrl+a", "ctrl+t",
		"a", "t",
	} {
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./script/ -run TestEngineLoadsEmbeddedDefaults -v`
Expected: FAIL — `default binding missing for "m"`

- [ ] **Step 3: Add the binding**

In `script/defaults.lua`, add to the "Lifecycle" section (after the `c` binding, line 19):

```lua
cs.bind("m", function() cs.actions.merge_selected() end,           { help = "merge session" })
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./script/ -run TestEngineLoadsEmbeddedDefaults -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add script/defaults.lua script/loader_defaults_test.go
git commit -m "feat(script): bind m to merge_selected in default keymap"
```

---

## Task 6: Add `stateMergePicker` and the overlay plumbing

**Files:**
- Modify: `app/app.go`
- Modify: `app/overlay_host.go`

- [ ] **Step 1: Add the state constant**

In `app/app.go`, add to the `state` const block (after `stateFileExplorer`, around line 114):

```go
	// stateMergePicker is the state when the merge-session picker
	// overlay is displayed (opened by the 'm' key).
	stateMergePicker
```

- [ ] **Step 2: Add the overlay kind and typed accessor**

In `app/overlay_host.go`, add `overlayMergePicker` to the `overlayKind` const block (after `overlayFileExplorer`):

```go
	overlayMergePicker
```

Add the accessor after `fileExplorer()` (after line 90):

```go
// mergePicker returns the active MergePicker, or nil when a different
// overlay is active.
func (m *home) mergePicker() *overlay.MergePicker {
	if o, ok := m.activeOverlay.(*overlay.MergePicker); ok {
		return o
	}
	return nil
}
```

This will not compile yet — `overlay.MergePicker` doesn't exist until Task 7. That's expected; this task and Task 7 land together before the next `go build` checkpoint.

- [ ] **Step 3: Route the new state in `handleKeyPress`, menu-highlight suppression, and overlay placement**

In `app/app.go`, add `stateMergePicker` to the menu-highlighting suppression check (around line 1186):

```go
	if m.state == statePrompt || m.state == stateHelp || m.state == stateConfirm || m.state == stateWorkspace || m.state == stateQuickInteract || m.state == stateInlineAttach || m.state == stateFileExplorer || m.state == stateMergePicker {
		return nil, false
	}
```

Add a case to the `handleKeyPress` switch (after the `stateFileExplorer` case, around line 1230-1231):

```go
	case stateMergePicker:
		return handleStateMergePickerKey(m, msg)
```

Add `stateMergePicker` to the overlay-placement switch in `View()` (around line 2053):

```go
		case statePrompt, stateHelp, stateConfirm, stateWorkspace, stateMergePicker:
			return asView(overlay.PlaceOverlay(0, 0, m.activeOverlay.View(), mainView, true, true))
```

`handleStateMergePickerKey` doesn't exist yet (Task 8) — the package won't build until that task lands. Note this and move directly to Task 7.

- [ ] **Step 4: Note progress (no commit yet)**

This task's edits are inter-dependent with Tasks 7-8 and won't compile in isolation. Do not run `go build`/`go test` or commit here — proceed directly to Task 7, then Task 8, then build/test/commit all three together at the end of Task 8.

---

## Task 7: Build the `MergePicker` overlay

**Files:**
- Create: `ui/overlay/mergePicker.go`
- Create: `ui/overlay/mergePicker_test.go`

- [ ] **Step 1: Write the failing tests**

Create `ui/overlay/mergePicker_test.go`:

```go
package overlay

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func sampleRows() []MergePickerRow {
	return []MergePickerRow{
		{Index: 1, Title: "fix-auth", Branch: "u/fix-auth", Status: "Running"},
		{Index: 3, Title: "refactor-db", Branch: "u/refactor-db", Status: "Paused"},
		{Index: 4, Title: "docs", Branch: "u/docs", Status: "Ready"},
	}
}

func TestMergePickerNavigation(t *testing.T) {
	t.Run("starts at first row", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		assert.Equal(t, 0, p.cursor)
	})

	t.Run("moves down", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
		assert.Equal(t, 1, p.cursor)
	})

	t.Run("does not go below last row", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		for i := 0; i < 5; i++ {
			p.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
		}
		assert.Equal(t, 2, p.cursor)
	})

	t.Run("moves up", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
		assert.Equal(t, 0, p.cursor)
	})

	t.Run("does not go above first row", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
		assert.Equal(t, 0, p.cursor)
	})
}

func TestMergePickerDigitJump(t *testing.T) {
	t.Run("jumps to the row whose original index matches, not slice position", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		// Row at slice position 1 has Index 3 (index 2 was filtered out
		// upstream) — typing "3" must land there, not on slice position 3.
		p.HandleKeyPress(tea.KeyPressMsg{Code: '3', Text: "3"})
		assert.Equal(t, 1, p.cursor)
		row := p.SelectedRow()
		assert.Equal(t, "refactor-db", row.Title)
	})

	t.Run("typing an index with no matching row does not move the cursor", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: '2', Text: "2"})
		assert.Equal(t, 0, p.cursor)
	})
}

func TestMergePickerSelection(t *testing.T) {
	t.Run("enter commits with the highlighted row", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		p.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
		committed, canceled := p.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.True(t, committed)
		assert.False(t, canceled)
		assert.Equal(t, "refactor-db", p.SelectedRow().Title)
	})

	t.Run("esc commits as canceled", func(t *testing.T) {
		p := NewMergePicker("current", sampleRows())
		committed, canceled := p.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		assert.True(t, committed)
		assert.True(t, canceled)
	})

	t.Run("empty rows: enter commits as canceled-safe (nil selection)", func(t *testing.T) {
		p := NewMergePicker("current", nil)
		committed, _ := p.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.True(t, committed)
		assert.Nil(t, p.SelectedRow())
	})
}

func TestMergePickerRender_DoesNotPanic(t *testing.T) {
	p := NewMergePicker("current", sampleRows())
	p.SetSize(60, 0)
	assert.NotEmpty(t, p.View())

	empty := NewMergePicker("current", nil)
	assert.NotEmpty(t, empty.View())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./ui/overlay/ -run TestMergePicker -v`
Expected: FAIL — `undefined: NewMergePicker` (package doesn't compile)

- [ ] **Step 3: Implement the overlay**

Create `ui/overlay/mergePicker.go`:

```go
package overlay

import (
	"fmt"
	"strconv"

	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// MergePickerRow is one selectable source session in the merge picker.
// Index is the session's original 1-based number from the main
// session list (ui.DisplayIndex) — NOT this row's position in the
// picker's own slice. Rows can have gaps (an ineligible session was
// filtered out upstream), which is deliberate: typing the digit the
// user already saw in the main list must land on the same session.
type MergePickerRow struct {
	Index  int
	Title  string
	Branch string
	Status string
}

// MergePicker lets the user choose which session's branch to merge
// into the currently-focused ("target") session. Deliberately decoupled
// from session.Instance (plain string/int fields only) so this package
// doesn't need to import session — the caller (app.runMergeSelected)
// re-resolves the chosen row back to an *session.Instance by its Index.
type MergePicker struct {
	targetTitle string
	rows        []MergePickerRow
	cursor      int
	width       int
	digitBuf    string
}

// NewMergePicker creates a merge picker for targetTitle (shown in the
// header) offering rows as the selectable sources.
func NewMergePicker(targetTitle string, rows []MergePickerRow) *MergePicker {
	return &MergePicker{targetTitle: targetTitle, rows: rows, width: 56}
}

// HandleKeyPress processes navigation, digit-jump, and selection keys.
// Returns (committed, canceled). committed=true means the overlay
// should close; when committed, canceled=true means the user backed
// out via Esc/q rather than picking a row (SelectedRow may still be
// non-nil in that case — callers must check canceled first).
func (p *MergePicker) HandleKeyPress(msg tea.KeyPressMsg) (bool, bool) {
	switch msg.String() {
	case "up", "k":
		p.digitBuf = ""
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		p.digitBuf = ""
		if p.cursor < len(p.rows)-1 {
			p.cursor++
		}
	case "enter":
		return true, false
	case "esc", "q":
		return true, true
	default:
		s := msg.String()
		if len(s) == 1 && s[0] >= '0' && s[0] <= '9' {
			p.digitBuf += s
			p.applyDigitBuf()
		}
	}
	return false, false
}

// applyDigitBuf jumps the cursor to the row whose Index matches the
// buffered digits. If the buffered value already exceeds every row's
// Index, no row can ever match by appending more digits, so the buffer
// resets to just the latest keystroke — this keeps a stray digit from
// locking out further typing.
func (p *MergePicker) applyDigitBuf() {
	n, err := strconv.Atoi(p.digitBuf)
	if err != nil {
		p.digitBuf = ""
		return
	}
	maxIndex := 0
	for i, r := range p.rows {
		if r.Index == n {
			p.cursor = i
			return
		}
		if r.Index > maxIndex {
			maxIndex = r.Index
		}
	}
	if n > maxIndex {
		p.digitBuf = p.digitBuf[len(p.digitBuf)-1:]
		p.applyDigitBuf()
	}
}

// SelectedRow returns the row currently highlighted, or nil if there
// are no rows.
func (p *MergePicker) SelectedRow() *MergePickerRow {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return nil
	}
	return &p.rows[p.cursor]
}

// HandleKey satisfies the Overlay interface.
func (p *MergePicker) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := p.HandleKeyPress(msg)
	return closed, nil
}

// View satisfies the Overlay interface.
func (p *MergePicker) View() string {
	return p.Render()
}

// SetSize satisfies the Overlay interface. Only width is used; height
// is accepted but ignored, matching WorkspacePicker.
func (p *MergePicker) SetSize(width, _ int) {
	p.width = width
}

// Render renders the merge picker overlay.
func (p *MergePicker) Render() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	selectedStyle := lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	normalStyle := lipgloss.NewStyle().Foreground(ui.TextPrimary)
	hintStyle := lipgloss.NewStyle().Foreground(ui.TextHint)

	content := titleStyle.Render(fmt.Sprintf("Merge into '%s'", p.targetTitle)) + "\n\n"

	if len(p.rows) == 0 {
		content += normalStyle.Render("No other sessions available to merge") + "\n"
	}
	for i, r := range p.rows {
		cursor := "  "
		if i == p.cursor {
			cursor = "> "
		}
		line := fmt.Sprintf("%s%d. %s (%s) [%s]", cursor, r.Index, r.Title, r.Branch, r.Status)
		if i == p.cursor {
			content += selectedStyle.Render(line) + "\n"
		} else {
			content += normalStyle.Render(line) + "\n"
		}
	}

	content += "\n" + hintStyle.Render("type # to jump • enter merge • esc cancel")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(p.width)

	return border.Render(content)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./ui/overlay/ -run TestMergePicker -v`
Expected: PASS (all subtests)

- [ ] **Step 5: Run the full ui/overlay package test suite to confirm no regression**

Run: `go test ./ui/overlay/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add ui/overlay/mergePicker.go ui/overlay/mergePicker_test.go
git commit -m "feat(ui): add MergePicker overlay

Lists eligible source sessions labeled with their original index
from the main list (not renumbered), supports arrow navigation and
direct digit-jump, and commits on Enter."
```

---

## Task 8: Add `runMergeSelected`, `mergeActionFor`, and the state handler

This task finishes the wiring started in Task 6 — after this task the app package builds and tests pass again.

**Files:**
- Modify: `app/intents.go`
- Modify: `app/app_scripts.go`
- Create: `app/state_mergepicker.go`
- Create: `app/merge_test.go`

- [ ] **Step 1: Write the failing tests**

Create `app/merge_test.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pausedInstanceWithRealWorktree builds a Paused instance backed by a
// real, on-disk git worktree. FromInstanceData sets started=true only
// for Paused instances (session/instance.go:283-286), so this is the
// lightest fixture that gives GetGitWorktree() a resolvable target
// without spinning up tmux (see the design doc's eligibility-rules
// verification note).
func pausedInstanceWithRealWorktree(t *testing.T, repoDir, title, branch string) *session.Instance {
	t.Helper()
	worktreePath := filepath.Join(t.TempDir(), title)
	runGit(t, repoDir, "worktree", "add", "-b", branch, worktreePath)

	data := session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         title,
		Path:          repoDir,
		Branch:        branch,
		Status:        session.Paused,
		Worktree: session.GitWorktreeData{
			RepoPath:         repoDir,
			WorktreePath:     worktreePath,
			SessionName:      title,
			BranchName:       branch,
			IsExistingBranch: true,
		},
	}
	inst, err := session.FromInstanceData(data, t.TempDir())
	require.NoError(t, err)
	return inst
}

func setupMergeRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "f"), []byte("x"), 0o644))
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-qm", "init")
	return repoDir
}

func TestRunMergeSelected_BlocksOnIneligibleTarget(t *testing.T) {
	m := newTestHome(t)
	// No selection at all — GetSelectedInstance returns nil, which fails
	// selectedNotBusyNotWorkspace immediately.
	_, cmd := runMergeSelected(m)
	require.NotNil(t, cmd, "expected an error Cmd")
	assert.Equal(t, stateDefault, m.state, "picker must not open for an ineligible target")
}

func TestRunMergeSelected_BlocksOnDirtyTarget(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	_ = m.list.AddInstance(target)
	m.list.SelectInstance(target)

	// Make the target worktree dirty.
	targetWT, err := target.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(targetWT.GetWorktreePath(), "dirty.txt"), []byte("wip"), 0o644))

	_, cmd := runMergeSelected(m)
	require.NotNil(t, cmd, "expected an error Cmd for a dirty target")
	assert.Equal(t, stateDefault, m.state, "picker must not open for a dirty target")
}

func TestRunMergeSelected_BlocksWhenNoEligibleSources(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	_ = m.list.AddInstance(target)
	m.list.SelectInstance(target)

	_, cmd := runMergeSelected(m)
	require.NotNil(t, cmd, "expected an error Cmd when there are no other sessions")
	assert.Equal(t, stateDefault, m.state)
}

func TestRunMergeSelected_OpensPickerWithEligibleSources(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")
	_ = m.list.AddInstance(target)
	_ = m.list.AddInstance(source)
	m.list.SelectInstance(target)

	_, cmd := runMergeSelected(m)
	assert.Nil(t, cmd)
	assert.Equal(t, stateMergePicker, m.state)

	mp := m.mergePicker()
	require.NotNil(t, mp)
	row := mp.SelectedRow()
	require.NotNil(t, row)
	assert.Equal(t, "source", row.Title, "the only eligible source should be pre-selected")
}

func TestMergeActionFor_MergesBranchIntoTarget(t *testing.T) {
	repoDir := setupMergeRepo(t)
	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")

	sourceWT, err := source.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sourceWT.GetWorktreePath(), "new.txt"), []byte("new"), 0o644))
	runGit(t, sourceWT.GetWorktreePath(), "add", ".")
	runGit(t, sourceWT.GetWorktreePath(), "commit", "-qm", "add new.txt")

	cmd := mergeActionFor(target, source)
	msg := cmd()
	assert.Nil(t, msg, "successful merge returns nil, matching pushActionFor's convention")

	targetWT, err := target.GetGitWorktree()
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(targetWT.GetWorktreePath(), "new.txt"))
	assert.NoError(t, statErr, "target worktree should now contain source's new file")
}
```

`GitWorktree.GetWorktreePath()` is defined at `session/git/worktree.go:203`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./app/ -run 'TestRunMergeSelected|TestMergeActionFor' -v`
Expected: FAIL — `undefined: runMergeSelected`, `undefined: mergeActionFor`, `undefined: stateMergePicker` (this last one should already exist from Task 6)

- [ ] **Step 3: Implement `runMergeSelected`, `mergeSourceRows`, `instanceByDisplayIndex`, and `mergeActionFor`**

In `app/intents.go`, add near the end of the file (after `runToggleFileExplorer` and its helpers):

```go
// -- Merge --

// runMergeSelected opens the merge-picker overlay for the currently
// focused session, following the same precondition-then-open shape as
// runOpenWorkspacePicker. Reuses selectedNotBusyNotWorkspace — the same
// gate push/kill/checkout already use — rather than inventing bespoke
// eligibility rules; see
// docs/superpowers/specs/2026-07-01-git-merge-hotkey-design.md.
func runMergeSelected(m *home) (tea.Model, tea.Cmd) {
	if !selectedNotBusyNotWorkspace(m) {
		return m, m.handleError(fmt.Errorf("no session selected, or the selection can't be merged into"))
	}
	target := m.list.GetSelectedInstance()

	worktree, err := target.GetGitWorktree()
	if err != nil {
		return m, m.handleError(fmt.Errorf("merge: %w", err))
	}
	dirty, err := worktree.IsDirty()
	if err != nil {
		return m, m.handleError(fmt.Errorf("merge: failed to check worktree status: %w", err))
	}
	if dirty {
		return m, m.handleError(fmt.Errorf("session '%s' has uncommitted changes — commit or stash before merging", target.Title))
	}

	rows := mergeSourceRows(m.list.GetInstances(), target)
	if len(rows) == 0 {
		return m, m.handleError(fmt.Errorf("no other sessions available to merge into '%s'", target.Title))
	}

	m.setOverlay(overlay.NewMergePicker(target.Title, rows), overlayMergePicker)
	m.state = stateMergePicker
	return m, nil
}

// mergeSourceRows builds the picker's row list: every instance in
// items passing the same eligibility filter as the target
// (selectedNotBusyNotWorkspace's rules, applied per-instance) except
// the target itself, labeled with its original position in items
// (ui.DisplayIndex) so a typed digit matches what's on-screen in the
// main list.
func mergeSourceRows(items []*session.Instance, target *session.Instance) []overlay.MergePickerRow {
	var rows []overlay.MergePickerRow
	for i, inst := range items {
		if inst == target || inst.IsWorkspaceTerminal {
			continue
		}
		status := inst.GetStatus()
		if status == session.Loading || status == session.Deleting {
			continue
		}
		rows = append(rows, overlay.MergePickerRow{
			Index:  ui.DisplayIndex(items, i),
			Title:  inst.Title,
			Branch: inst.GetBranch(),
			Status: status.String(),
		})
	}
	return rows
}

// instanceByDisplayIndex returns the instance whose ui.DisplayIndex
// position (the same number rendered in the main list) matches idx, or
// nil if none matches. Re-scans live data rather than caching a
// pointer from when the picker opened, so a background reconcile that
// changed the list between open and commit can't hand back a stale
// instance.
func instanceByDisplayIndex(items []*session.Instance, idx int) *session.Instance {
	for i, inst := range items {
		if ui.DisplayIndex(items, i) == idx {
			return inst
		}
	}
	return nil
}

// mergeActionFor returns the tea.Cmd that performs the actual git
// merge once the user commits a selection in the picker. Mirrors
// pushActionFor: returns nil on success (silent, matching push's
// convention of treating "no error" as sufficient feedback) or the
// wrapped git error, which Update()'s case error: branch surfaces via
// m.handleError.
func mergeActionFor(target, source *session.Instance) tea.Cmd {
	return func() tea.Msg {
		worktree, err := target.GetGitWorktree()
		if err != nil {
			return fmt.Errorf("merge: %w", err)
		}
		if err := worktree.Merge(source.GetBranch()); err != nil {
			return err
		}
		return nil
	}
}
```

Create `app/state_mergepicker.go`:

```go
package app

import (
	tea "charm.land/bubbletea/v2"
)

// handleStateMergePickerKey drives the merge-picker overlay opened by
// runMergeSelected. On commit it either cancels (Esc — no git command
// runs) or hands the chosen source instance to mergeActionFor, which
// runs the actual git merge as a tea.Cmd. This is where the Lua
// coroutine's involvement ends for good — everything past
// runMergeSelected's yield-and-resume is plain Go state-handler code,
// the same as stateWorkspace/stateConfirm.
func handleStateMergePickerKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	mp := m.mergePicker()
	if mp == nil {
		return m, nil
	}
	committed, canceled := mp.HandleKeyPress(msg)
	if !committed {
		return m, nil
	}

	target := m.list.GetSelectedInstance()
	row := mp.SelectedRow()
	m.dismissOverlay()
	m.state = stateDefault

	if canceled || row == nil || target == nil {
		return m, nil
	}
	source := instanceByDisplayIndex(m.list.GetInstances(), row.Index)
	if source == nil {
		return m, nil
	}
	return m, mergeActionFor(target, source)
}
```

In `app/app_scripts.go`, add a case to `handleScriptIntent`'s switch (after the `script.ToggleFileExplorerIntent` case, around line 527):

```go
	case script.MergeSessionsIntent:
		_, cmd = runMergeSelected(m)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./app/ -run 'TestRunMergeSelected|TestMergeActionFor' -v`
Expected: PASS

- [ ] **Step 5: Build and test the whole app package (this is the first point Tasks 6-8 compile together)**

Run: `go build ./...`
Expected: success

Run: `go test ./app/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add app/intents.go app/app_scripts.go app/state_mergepicker.go app/merge_test.go app/app.go app/overlay_host.go
git commit -m "feat(app): wire the merge-session picker end to end

Adds runMergeSelected (opens the picker, reusing
selectedNotBusyNotWorkspace and an IsDirty check), the
stateMergePicker state handler, and mergeActionFor — the tea.Cmd
that runs the actual git merge, mirroring pushActionFor's plain-error
convention (no new notification channel)."
```

---

## Task 9: Add migration-parity coverage for `m`

**Files:**
- Modify: `app/migration_parity_test.go`

- [ ] **Step 1: Write the failing test case**

In `app/migration_parity_test.go`, add to the `cases` slice in `TestMigrationParity`:

```go
		{"merge_selected", "m", script.MergeSessionsIntent{}},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/ -run TestMigrationParity -v`

This should actually already pass once Tasks 3-8 are in place (there's no prior Go-only `m` handler to have drifted from) — run it to confirm the dispatch wiring is correct end-to-end. If it fails, it means `m`'s dispatch chain (defaults.lua → merge_selected → MergeSessionsIntent) is broken somewhere in Tasks 4-5.

- [ ] **Step 3: Run the full app package test suite**

Run: `go test ./app/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add app/migration_parity_test.go
git commit -m "test(app): add merge_selected to the dispatch parity suite"
```

---

## Task 10: Add `KeyMerge` to the help-panel key table

**Files:**
- Modify: `keys/keys.go`

- [ ] **Step 1: Add the KeyName and binding**

In `keys/keys.go`, add `KeyMerge` to the `KeyName` const block (after `KeyCheckout`, around line 28):

```go
	KeyMerge
```

Add the binding to `GlobalkeyBindings` (after `KeyCheckout`'s entry, around line 108):

```go
	KeyMerge: key.NewBinding(
		key.WithKeys("m"),
		key.WithHelp("m", "merge session"),
	),
```

- [ ] **Step 2: Run the keys package tests**

Run: `go test ./keys/...`
Expected: PASS

- [ ] **Step 3: Run the app package tests once more (menu-highlighting reads GlobalkeyBindings via KeyForString)**

Run: `go test ./app/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add keys/keys.go
git commit -m "feat(keys): add KeyMerge for the help-panel listing"
```

---

## Task 11: Update documentation

**Files:**
- Modify: `USAGE.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add the row to CLAUDE.md's keybindings table**

In `CLAUDE.md`, add a row to the "TUI Keybindings" table (after the `c` row):

```markdown
| `m` | Merge another session's branch into the current one |
```

- [ ] **Step 2: Add the row to USAGE.md's keybindings table**

In `USAGE.md`, add a row to the same table this file already has (after the `c` row, near line 209):

```markdown
| `m` | Merge another session's branch into the current one |
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md USAGE.md
git commit -m "docs: document the m (merge session) keybinding"
```

---

## Task 12: Full verification pass

**Files:** none (verification only)

- [ ] **Step 1: gofmt**

Run: `gofmt -l .`
Expected: no output (no files need formatting). If any files are listed, run `gofmt -w .` and review the diff before committing.

- [ ] **Step 2: Full build**

Run: `CGO_ENABLED=0 go build -o loom`
Expected: success

- [ ] **Step 3: Full test suite**

Run: `go test -v ./...`
Expected: PASS across all packages

- [ ] **Step 4: Race detector**

Run: `CC=clang CGO_ENABLED=1 go test -race ./...` (use `CC=gcc` if clang isn't installed)
Expected: PASS, no data race reports. This exercises the new `stateMergePicker` field and overlay pointer under `-race`, per the design doc's testing note.

- [ ] **Step 5: Lint**

Run: `golangci-lint run --timeout=3m --fast`
Expected: no new findings attributable to this change

- [ ] **Step 6: Manual smoke test**

Start loom against a real repo with at least two sessions running (`loom`), press `m` on a session with at least one other eligible session, confirm the picker opens, type a digit matching another session's list number, press Enter, and confirm the branch merges (check `git log` in that session's worktree). Then confirm `esc` cancels cleanly, and that a dirty worktree blocks the picker with a visible error message.

- [ ] **Step 7: Fix any findings, otherwise this task is done (no commit needed if nothing changed)**

If any step above required a fix, commit it separately with a message describing what was fixed (e.g., `fix(lint): ...`).
