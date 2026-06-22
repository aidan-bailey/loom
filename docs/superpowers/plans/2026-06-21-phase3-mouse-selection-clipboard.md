# Phase 3 — Mouse selection + clipboard Implementation Plan

> REQUIRED SUB-SKILL: executing-plans. Steps use checkbox (`- [ ]`) tracking.

**Goal:** Drag-select text in either pane (and the diff overlay) and copy it to the system clipboard — including over SSH — plus click-to-focus a pane. No "attach" required.

**Architecture:** Selection operates on the panes' already-rendered text lines (the `Emulator` interface only exposes `Render() string`, not cells). Each pane tracks a selection range in content coordinates `(row, col)` over its displayed lines and renders a reverse-video highlight. `SplitPane` computes each pane's on-screen content rectangle at render time; `app.go` hit-tests mouse coordinates against those rectangles and drives selection begin/extend/end. Copy uses `tea.SetClipboard` (OSC 52, tunnels SSH) batched with an `atotto/clipboard` fallback.

**Tech stack:** Bubble Tea v2 mouse messages (`MouseClickMsg`/`MouseMotionMsg`/`MouseReleaseMsg`), `tea.SetClipboard`, `github.com/atotto/clipboard` (already a dep), lipgloss v2.

**Design notes / decisions:**
- Drag-select and wheel-forward (the Phase-2.5 TUI scroll forwarding) are different gestures, so they coexist. For a TUI agent, selection works over the visible screen the emulator renders.
- Selection is plain-text: the highlighted region is shown reverse-video on the stripped text; original SGR colors inside the selection are dropped (acceptable, matches most terminals' selection rendering).
- Click without drag = click-to-focus the pane (sets `focusedPane`). It does NOT route keystrokes (that's Phase 5).
- Mouse mode is already `tea.MouseModeCellMotion` (app.go ~2051), which delivers motion events for drag.

---

## Geometry (content rectangle per pane)

Right split starts at `X = listWidth`. `s.width` is the split width. Each pane box: row 0 is the title border line; the body is bordered left/right/bottom (no top), so:
- agent content: `originX = listWidth + 1`, `originY = 1`, `w = s.width - 2`, `h = agent.height`
- terminal content: `originX = listWidth + 1`, `originY = agent.height + 3` (1 title + agent.height + 1 bottom border + 1 terminal title), `w = s.width - 2`, `h = terminal.height`
- diff overlay (when visible): single pane, `originX = listWidth + 1`, `originY = 1`, `w = s.width - 2`, `h = s.height - 1`

These are computed in `SplitPane` at render and exposed via a hit-test method so app.go never recomputes lipgloss frame math.

---

## Task 1: Clipboard copy command

**Files:** Create `app/clipboard.go`; Test `app/clipboard_test.go`

- [ ] `copyToClipboard(text string) tea.Cmd` returns `tea.Batch(tea.SetClipboard(text), <atotto cmd>)` where the atotto cmd runs `clipboard.WriteAll(text)` in a `tea.Cmd` and returns a `clipboardCopiedMsg{n int, err error}`. Empty text → `nil` cmd.
- [ ] Test: empty string returns nil; non-empty returns non-nil batch. (atotto WriteAll may fail headless — the cmd must not panic; assert the msg carries the rune count.)

## Task 2: Selection model + text extraction (pure)

**Files:** Create `ui/selection.go`; Test `ui/selection_test.go`

- [ ] `type selection struct { active bool; anchorRow, anchorCol, curRow, curCol int }` with `normalized() (r0,c0,r1,c1 int)` ordering anchor/cursor top-to-bottom, left-to-right.
- [ ] `func extractSelection(plainLines []string, sel selection) string`: returns the selected substring across lines (first line from c0, middle lines whole, last line to c1), joined by `\n`, operating on rune indices. Out-of-range cols clamp to line bounds.
- [ ] `func highlightLine(plain string, fromCol, toCol int) string`: returns the line with `[fromCol,toCol)` wrapped in reverse-video SGR (`\x1b[7m`…`\x1b[27m`), rune-indexed, clamped.
- [ ] Tests: single-line range; multi-line range; full-line (middle) selection; clamping past EOL; empty selection → "".

## Task 3: Pane selection state + highlighted render

**Files:** `ui/preview.go`, `ui/terminal.go` (and `ui/diff.go` if trivial); Test additions

- [ ] Each pane gains `sel selection` and `SetSelection(sel)`/`ClearSelection()`/`SelectedText() string`. `SelectedText` extracts from the pane's current *plain* displayed lines.
- [ ] Each pane exposes `displayedLines() []string` — the plain (ANSI-stripped) lines currently shown (live tail or windowed). Add an exported `PlainLines() []string` used by `SelectedText` and by `SplitPane` for extraction.
- [ ] In `String()`, when `sel.active` and this pane is the selection target, overlay the highlight via `highlightLine` on the affected rows before the final style render.
- [ ] Test: a pane with known content + a selection renders reverse-video on the right rows; `SelectedText` returns the expected substring.

## Task 4: SplitPane hit-test + content rectangles

**Files:** `ui/split_pane.go`; Test `ui/split_pane_test.go`

- [ ] `type PaneHit struct { Pane int; Row, Col int }` and `func (s *SplitPane) HitTest(x, y int) (PaneHit, bool)` mapping absolute screen coords to a pane + content `(row,col)` using the geometry above (accounting for `diffVisible`). Returns false when outside any content rect or in the list panel (caller checks `listWidth`).
- [ ] `func (s *SplitPane) SetSelection(pane int, sel selection)` / `ClearSelections()` / `SelectedText() string` delegate to the panes.
- [ ] Test: coordinates inside agent/terminal/diff map to the right pane + offsets; coordinates on a border or outside return false.

## Task 5: app.go mouse selection dispatch

**Files:** `app/app.go`; Test (where feasible)

- [ ] Handle `tea.MouseClickMsg` (button left): if `X < listWidth` ignore; else `HitTest` → set `focusedPane` (click-to-focus) and begin a selection anchor (store pending in `home`).
- [ ] Handle `tea.MouseMotionMsg` while a left-drag is active: `HitTest` (clamp to the anchor's pane) → extend selection; apply to the pane via `SetSelection`.
- [ ] Handle `tea.MouseReleaseMsg`: if the selection is non-empty, `SelectedText()` → `copyToClipboard(...)`; keep the highlight briefly or clear on next click. A click with no drag clears any existing selection (and just focuses).
- [ ] Add `clipboardCopiedMsg` handling: log/notify the copied rune count; surface errors via the existing notify path.

## Task 6: Verify + tune

- [ ] `gofmt`, `CGO_ENABLED=0 go build ./...`, full `go test ./...`, `-race` on `ui`/`app`.
- [ ] Manual: `nix run .` — drag-select in terminal pane, agent pane, diff overlay; paste elsewhere (incl. over SSH); confirm click-to-focus. Iterate on coordinate offsets and highlight fidelity.
