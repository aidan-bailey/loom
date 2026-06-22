# Phase 2: Live Scroll-While-Tailing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the agent/terminal panes' "scroll mode" mode-switch with a scroll *offset* into the in-process emulator's scrollback, so output keeps flowing while you're scrolled up, with a "N new lines / jump to bottom" affordance.

**Architecture:** The Phase-1 `x/vt` emulator already keeps 10k lines of scrollback automatically. Add `ScrollbackLen()` + `RenderWindow(fromBottom, rows)` to the `vt.Emulator` interface (rendering a windowed slice of `[scrollback | visible]` via `ultraviolet`'s own `Lines.Render()`), then replace each pane's `bubbles/viewport` + `isScrolling` machinery with a single `scrollOffset` integer. Scrolling adjusts the offset; the existing per-tick `UpdateContent` re-renders the window against the *current* scrollback, so live output accrues below while the view holds. The diff overlay (static content) keeps its viewport unchanged.

**Tech Stack:** Go 1.23, `CGO_ENABLED=0` build/tests (race tests `CC=clang CGO_ENABLED=1`), Bubble Tea v2, `charmbracelet/x/vt` + `charmbracelet/ultraviolet`.

---

## Verified API facts (the contract this plan relies on)

Confirmed against the vendored source:
- `(*xvt.Emulator).Scrollback() *xvt.Scrollback` (`emulator.go:451`), `ScrollbackLen() int` (`:457`), `CellAt(x,y) *uv.Cell` (`:153`, nil if OOB), `Width() int` (`:206`), `Height() int` (`:201`).
- `(*xvt.Scrollback).Len() int` (`scrollback.go:71`), `Line(i int) uv.Line` (`:103`, **index 0 = oldest**, nil if OOB). Scrollback grows automatically as the screen scrolls; `DefaultScrollbackSize = 10000`.
- `uv` = `github.com/charmbracelet/ultraviolet`: `type Line []Cell` (`buffer.go:29`), `type Lines []Line` (`:200`), `func (ls Lines) Render() string` (`:230`) — coalesces SGR per row, resets at line end, **trims trailing blank cells**, newline-joins rows. `uv.EmptyCell` is the blank-cell sentinel. A `nil` Line renders to `""` (blank row).
- **No `capture-pane -S -` is needed for history** — it lives in `emu.Scrollback()`. `CapturePaneContent()` (visible snapshot) stays for the no-emulator fallback + daemon.

Current code anchors (post-Phase-1):
- `session/vt/vt.go:16` `Emulator` interface; `session/vt/xvt.go:14` `xvtEmulator` (RWMutex `e.mu`, `e.term *xvt.Emulator`).
- `session/tmux/tmux.go`: `RenderEmulator() (string, bool)` (the guard pattern to mirror).
- `session/instance.go`: `Preview()` (live tail, emulator visible screen), `PreviewFullHistory()` (~`:1133`, capture-pane history — **to be retired**).
- `ui/preview.go`: `PreviewPane` struct (`:20`), `viewport.Model` field, `isScrolling`, `UpdateContent` (`:67`), `String()` (`:151`), `enterScrollMode` (`:220`), scroll methods (`:237+`), `ScrollPercent` (`:309`), `IsScrolling` (`:317`), `ResetToNormalMode` (`:322`).
- `ui/terminal.go`: `TerminalPane` (mutex `t.mu`, viewport, `isScrolling`); `UpdateContent` (`:87`), `String()` (`:342`), `enterScrollMode` (`:408`), scroll methods (`:427+`).
- `ui/diff.go`: `DiffPane` on `bubbles/viewport` — **unchanged**.
- `ui/split_pane.go`: scroll routers (`ScrollAgentUp/Down`, `ScrollTerminalUp/Down`, `ScrollDiffUp/Down`, `GotoTop/Bottom`, `IsAgentInScrollMode` reading `s.agent.isScrolling` at `:340`).
- `app/app.go:729` mouse-wheel handler; `app/state_default.go:23` Esc handling; `script/defaults.lua` scroll bindings → `app/app_scripts.go` `deferModelMutation`.

---

## File structure

- **Modify `session/vt/vt.go`** — add `ScrollbackLen()` + `RenderWindow()` to the interface.
- **Modify `session/vt/xvt.go`** — implement both on `xvtEmulator`.
- **Modify `session/vt/vt_test.go`** — extend the `nopEmulator` stub; **Modify `session/vt/xvt_test.go`** — golden tests.
- **Modify `session/tmux/tmux.go`** — `ScrollbackLen()`/`RenderWindow()` passthroughs.
- **Modify `session/instance.go`** — `ScrollbackLen()`/`RenderWindow()` delegators; retire `PreviewFullHistory` (final task).
- **Modify `ui/preview.go`** — offset model (drop viewport/isScrolling).
- **Modify `ui/terminal.go`** — offset model.
- **Modify `ui/split_pane.go`** — one field-read change.
- **Modify `ui/preview_test.go`, `ui/terminal_test.go`** — offset-model tests.
- **Unchanged:** `ui/diff.go`, `app/app.go` mouse handler, `app/state_default.go`, `script/defaults.lua` (bindings stable; bodies change underneath).

---

## Task 1: Emulator interface + `RenderWindow`/`ScrollbackLen` + golden tests

**Files:**
- Modify: `session/vt/vt.go`, `session/vt/xvt.go`, `session/vt/vt_test.go`
- Test: `session/vt/xvt_test.go`

- [ ] **Step 1: Write the failing golden tests**

```go
// append to session/vt/xvt_test.go
func writeLines(e Emulator, n int) {
	for i := 1; i <= n; i++ {
		_, _ = e.Write([]byte(fmt.Sprintf("line%d\r\n", i)))
	}
}

func TestXVT_ScrollbackGrows(t *testing.T) {
	e := NewXVT(20, 5)
	defer e.Close()
	writeLines(e, 30) // > height, forces lines into scrollback
	if e.(interface{ ScrollbackLen() int }).ScrollbackLen() == 0 {
		t.Fatal("scrollback should accrue after writing more lines than the screen height")
	}
}

func TestXVT_RenderWindow_Content(t *testing.T) {
	e := NewXVT(20, 5)
	defer e.Close()
	writeLines(e, 30)
	// fromBottom=0 -> the bottom `rows` lines (live tail region).
	bottom := stripSGR(e.(interface {
		RenderWindow(int, int) string
	}).RenderWindow(0, 3))
	if !strings.Contains(bottom, "line30") {
		t.Fatalf("window at bottom should include the newest line; got %q", bottom)
	}
	// Scroll up into history: a window further from the bottom shows older lines.
	up := stripSGR(e.(interface {
		RenderWindow(int, int) string
	}).RenderWindow(10, 3))
	if strings.Contains(up, "line30") {
		t.Fatalf("a scrolled-up window should not show the newest line; got %q", up)
	}
}

func TestXVT_RenderWindow_BlankPadding(t *testing.T) {
	e := NewXVT(20, 5)
	defer e.Close()
	writeLines(e, 3) // tiny buffer
	rw := e.(interface{ RenderWindow(int, int) string })
	// Far past the top -> leading blanks, never panics.
	got := rw.RenderWindow(1000, 4)
	if strings.Count(got, "\n") > 4 {
		t.Fatalf("window must be at most `rows` lines; got %q", got)
	}
	// rows < 1 -> empty.
	if rw.RenderWindow(0, 0) != "" {
		t.Fatal("RenderWindow(_,0) must return empty string")
	}
}
```
Add `"fmt"` to the test imports (alongside `strings`, `sync`, `testing`, `time`).

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/vt/ -run 'TestXVT_Scrollback|TestXVT_RenderWindow' 2>&1 | head`
Expected: FAIL — `ScrollbackLen`/`RenderWindow` undefined (the type assertions fail to compile).

- [ ] **Step 3: Add the interface methods**

In `session/vt/vt.go`, append to the `Emulator` interface (after `Close() error`):
```go
	// ScrollbackLen returns the number of lines in the emulator's scrollback
	// (history above the visible screen). Grows as output scrolls; capped at
	// the configured max (x/vt default 10000).
	ScrollbackLen() int

	// RenderWindow renders `rows` lines as an ANSI-styled string. The window's
	// bottom sits `fromBottom` lines above the bottom of the combined
	// [scrollback | visible-screen] buffer: fromBottom 0 = the bottom `rows`
	// lines (live tail). Indices outside the buffer render as blank lines.
	RenderWindow(fromBottom, rows int) string
```

- [ ] **Step 4: Implement on `xvtEmulator`**

In `session/vt/xvt.go`, add the `uv` import (`uv "github.com/charmbracelet/ultraviolet"`) and:
```go
func (e *xvtEmulator) ScrollbackLen() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.term.ScrollbackLen()
}

// RenderWindow renders `rows` combined (scrollback+visible) lines whose bottom
// is `fromBottom` lines above the bottom of the buffer. Holds the read lock so
// it is concurrent-safe against the output pump's Write (like Render/Cursor).
func (e *xvtEmulator) RenderWindow(fromBottom, rows int) string {
	if rows < 1 {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	sb := e.term.Scrollback()
	sbLen := 0
	if sb != nil {
		sbLen = sb.Len()
	}
	w := e.term.Width()
	h := e.term.Height()
	total := sbLen + h
	top := total - fromBottom - rows // combined index of the window's first row

	out := make(uv.Lines, 0, rows)
	for i := 0; i < rows; i++ {
		idx := top + i
		switch {
		case idx < 0 || idx >= total:
			out = append(out, nil) // blank row
		case idx < sbLen:
			out = append(out, sb.Line(idx)) // straight from scrollback (oldest=0)
		default:
			y := idx - sbLen // visible screen row
			line := make(uv.Line, 0, w)
			for x := 0; x < w; x++ {
				if c := e.term.CellAt(x, y); c != nil {
					line = append(line, *c)
				} else {
					line = append(line, uv.EmptyCell)
				}
			}
			out = append(out, line)
		}
	}
	return out.Render() // SGR-coalesced, trailing blanks trimmed, newline-joined
}
```

- [ ] **Step 5: Fix the `nopEmulator` test stub**

In `session/vt/vt_test.go`, add the two methods so `nopEmulator` still satisfies `Emulator`:
```go
func (nopEmulator) ScrollbackLen() int                 { return 0 }
func (nopEmulator) RenderWindow(fromBottom, rows int) string { return "" }
```

- [ ] **Step 6: Run tests + race**

Run: `CGO_ENABLED=0 go test ./session/vt/ -v 2>&1 | grep -E '^(--- (PASS|FAIL)|ok|FAIL)'`
Expected: all PASS. If a content assert mismatches, adjust the *assertion* to the real `Lines.Render()` shape (it trims trailing blanks), not the impl.
Run: `CC=clang CGO_ENABLED=1 go test -race ./session/vt/ 2>&1 | tail -3`
Expected: PASS, no races (RenderWindow holds the read lock).

- [ ] **Step 7: Commit**

```bash
git add session/vt/vt.go session/vt/xvt.go session/vt/vt_test.go session/vt/xvt_test.go
git commit -m "feat(vt): add RenderWindow + ScrollbackLen for windowed scrollback rendering"
```

---

## Task 2: `TmuxSession` + `Instance` passthroughs

**Files:**
- Modify: `session/tmux/tmux.go`, `session/instance.go`
- Test: `session/tmux/emulator_test.go`

- [ ] **Step 1: Write the failing test**

```go
// append to session/tmux/emulator_test.go
func TestRenderWindow_NilWhenUnset(t *testing.T) {
	ts := NewTmuxSession("rw-nil", "prog")
	if _, ok := ts.RenderWindow(0, 5); ok {
		t.Fatal("RenderWindow must report ok=false with no emulator wired")
	}
	if _, ok := ts.ScrollbackLen(); ok {
		t.Fatal("ScrollbackLen must report ok=false with no emulator wired")
	}
}

func TestRenderWindow_ReadsEmulator(t *testing.T) {
	ts := NewTmuxSession("rw-set", "prog")
	ts.stateMu.Lock()
	ts.emu = vt.NewXVT(20, 5)
	ts.stateMu.Unlock()
	for i := 0; i < 10; i++ {
		_, _ = ts.emu.Write([]byte("row\r\n"))
	}
	if n, ok := ts.ScrollbackLen(); !ok || n <= 0 {
		t.Fatalf("ScrollbackLen should be >0 after writing rows; n=%d ok=%v", n, ok)
	}
	if s, ok := ts.RenderWindow(0, 3); !ok || !containsText(s, "row") {
		t.Fatalf("RenderWindow should return content; ok=%v s=%q", ok, s)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestRenderWindow|TestScrollback' 2>&1 | head`
Expected: FAIL — `ts.RenderWindow`/`ts.ScrollbackLen` undefined.

- [ ] **Step 3: Add the passthroughs (`session/tmux/tmux.go`)**

Next to `RenderEmulator` (mirror its lock/nil-guard):
```go
// ScrollbackLen returns the emulator's scrollback line count, or (0,false) if
// no emulator is wired.
func (t *TmuxSession) ScrollbackLen() (int, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return 0, false
	}
	return emu.ScrollbackLen(), true
}

// RenderWindow renders `rows` combined lines `fromBottom` lines above the
// buffer bottom, or ("",false) if no emulator is wired.
func (t *TmuxSession) RenderWindow(fromBottom, rows int) (string, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return "", false
	}
	return emu.RenderWindow(fromBottom, rows), true
}
```

- [ ] **Step 4: Add `Instance` delegators (`session/instance.go`)**

Near `Preview()`/`PreviewFullHistory()`:
```go
// ScrollbackLen returns the agent pane emulator's scrollback length, or
// (0,false) if unavailable (no session / snapshot mode).
func (i *Instance) ScrollbackLen() (int, bool) {
	if !i.isStarted() || i.GetStatus() == Paused {
		return 0, false
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return 0, false
	}
	return ts.ScrollbackLen()
}

// RenderWindow renders a scrolled window of the agent pane, or ("",false) if
// unavailable.
func (i *Instance) RenderWindow(fromBottom, rows int) (string, bool) {
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", false
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return "", false
	}
	return ts.RenderWindow(fromBottom, rows)
}
```

- [ ] **Step 5: Run + commit**

Run: `CGO_ENABLED=0 go test ./session/... 2>&1 | grep -E 'FAIL|ok.*tmux|ok.*session\s'`
Expected: PASS.
```bash
git add session/tmux/tmux.go session/instance.go session/tmux/emulator_test.go
git commit -m "feat(session): expose ScrollbackLen/RenderWindow through TmuxSession and Instance"
```

---

## Task 3: PreviewPane offset model (agent pane)

**Files:**
- Modify: `ui/preview.go`
- Test: `ui/preview_test.go`

Replace `isScrolling`+`viewport` with a `scrollOffset` (lines-from-bottom; 0 = live tail). Keep all public method *names* (dispatch depends on them).

- [ ] **Step 1: Write the failing tests**

```go
// append to ui/preview_test.go (use the existing setupTestEnvironment harness;
// it runs in snapshot mode, so drive offset logic directly).
func TestPreviewPane_ScrollOffsetClamps(t *testing.T) {
	p := NewPreviewPane()
	p.SetSize(80, 10)
	// With no instance the offset stays 0 and methods are no-ops (nil-safe).
	_ = p.ScrollUp(nil)
	if p.IsScrolling() {
		t.Fatal("scroll on a nil instance must not enter scrolled state")
	}
}

func TestPreviewPane_GotoBottomResetsOffset(t *testing.T) {
	p := NewPreviewPane()
	p.scrollOffset = 7
	_ = p.GotoBottom(nil) // nil instance still resets to live tail
	if p.scrollOffset != 0 || p.IsScrolling() {
		t.Fatalf("GotoBottom must reset to live tail; offset=%d", p.scrollOffset)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/ -run TestPreviewPane_ 2>&1 | head`
Expected: FAIL — `p.scrollOffset`/`p.IsScrolling` not yet in the new shape (compile error or wrong behavior).

- [ ] **Step 3: Rework the struct + SetSize**

In `ui/preview.go`, replace the `isScrolling`/`viewport` fields with:
```go
	// scrollOffset is lines-from-bottom; 0 = live tail. Increasing scrolls up
	// into the emulator scrollback. Clamped to [0, ScrollbackLen()].
	scrollOffset int
	// scrollbackAtScrollStart is ScrollbackLen() captured when the offset left
	// 0, used to count "new lines below" without scanning the buffer.
	scrollbackAtScrollStart int
	// lastScrollbackLen caches ScrollbackLen() from the last UpdateContent so
	// ScrollPercent/footer are lock-free and consistent with the last render.
	lastScrollbackLen int
	// newLinesBelow is the live-output line count accrued since scrolling up.
	newLinesBelow int
```
Remove the `"github.com/charmbracelet/bubbles/v2/viewport"` import and `viewport.New(...)` in `NewPreviewPane`. In `SetSize`, drop the `viewport.SetWidth/SetHeight` calls; keep `p.width = width; p.height = maxHeight`.

- [ ] **Step 4: Rework UpdateContent (the scrolled branch)**

Keep the nil/Loading/Paused fallbacks and the instance-change reset (set `p.scrollOffset = 0` on title change). Replace the `isScrolling ? PreviewFullHistory : Preview` block with:
```go
	if p.scrollOffset == 0 {
		// Live tail: emulator visible screen (or capture-pane fallback).
		content, err := instance.Preview()
		if err != nil {
			return err
		}
		if len(content) == 0 && !instance.Started() {
			p.setFallbackState("Please enter a name for the instance.")
		} else {
			p.previewState = previewState{fallback: false, text: content}
		}
		p.newLinesBelow = 0
		return nil
	}

	// Scrolled: render a height-1 window at the offset (last row = footer).
	total, ok := instance.ScrollbackLen()
	if !ok {
		// No emulator (snapshot/Windows): scrolling unsupported -> live tail.
		p.scrollOffset = 0
		content, _ := instance.Preview()
		p.previewState = previewState{fallback: false, text: content}
		return nil
	}
	p.lastScrollbackLen = total
	if p.scrollOffset > total {
		p.scrollOffset = total
	}
	rows := p.height - 1
	if rows < 1 {
		rows = 1
	}
	window, _ := instance.RenderWindow(p.scrollOffset, rows)
	p.previewState = previewState{fallback: false, text: window}
	newBelow := total - p.scrollbackAtScrollStart
	if newBelow < 0 {
		newBelow = 0
	}
	p.newLinesBelow = newBelow
	return nil
```

- [ ] **Step 5: Rework scroll methods + String()**

Replace `enterScrollMode`/`ScrollUp`/`ScrollDown`/`GotoTop`/`GotoBottom`/`IsScrolling`/`ScrollPercent`/`ResetToNormalMode` with the offset versions:
```go
func (p *PreviewPane) ScrollUp(instance *session.Instance) error   { return p.scrollBy(instance, +1) }
func (p *PreviewPane) ScrollDown(instance *session.Instance) error { return p.scrollBy(instance, -1) }
func (p *PreviewPane) PageUp(instance *session.Instance) error     { return p.scrollBy(instance, +(p.height / 2)) }
func (p *PreviewPane) PageDown(instance *session.Instance) error   { return p.scrollBy(instance, -(p.height / 2)) }

func (p *PreviewPane) GotoTop(instance *session.Instance) error {
	maxOff := 0
	if instance != nil {
		if m, ok := instance.ScrollbackLen(); ok {
			maxOff = m
		}
	}
	return p.setOffset(instance, maxOff)
}
func (p *PreviewPane) GotoBottom(instance *session.Instance) error { return p.setOffset(instance, 0) }
func (p *PreviewPane) ResetToNormalMode(instance *session.Instance) error { return p.setOffset(instance, 0) }
func (p *PreviewPane) IsScrolling() bool { return p.scrollOffset > 0 }

func (p *PreviewPane) ScrollPercent() float64 {
	if p.scrollOffset <= 0 || p.lastScrollbackLen <= 0 {
		return 1.0
	}
	return 1.0 - float64(p.scrollOffset)/float64(p.lastScrollbackLen)
}

func (p *PreviewPane) scrollBy(instance *session.Instance, delta int) error {
	return p.setOffset(instance, p.scrollOffset+delta)
}

func (p *PreviewPane) setOffset(instance *session.Instance, off int) error {
	if instance != nil && instance.GetStatus() == session.Paused {
		return nil
	}
	maxOff := 0
	if instance != nil {
		if m, ok := instance.ScrollbackLen(); ok {
			maxOff = m
		}
	}
	if off < 0 {
		off = 0
	}
	if off > maxOff {
		off = maxOff
	}
	wasBottom := p.scrollOffset == 0
	p.scrollOffset = off
	if wasBottom && off > 0 {
		p.scrollbackAtScrollStart = maxOff // mark for the new-lines counter
	}
	return nil
}
```
In `String()`, drop the `isScrolling → viewport.View()` branch. The live-tail truncate/pad logic stays. The footer is added in Task 5 — for now the scrolled branch renders `p.previewState.text` straight (it is already `height-1` rows).

- [ ] **Step 6: Run + commit**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'TestPreview' -v 2>&1 | grep -E '^(--- (PASS|FAIL)|ok|FAIL)'`
Expected: PASS (incl. the existing `TestPreviewContentWithoutScrolling`, which runs in snapshot mode → offset stays 0 → live tail unchanged).
```bash
git add ui/preview.go ui/preview_test.go
git commit -m "feat(ui): live-scroll offset model for the agent pane (drop viewport mode-switch)"
```

---

## Task 4: TerminalPane offset model (terminal pane)

**Files:**
- Modify: `ui/terminal.go`
- Test: `ui/terminal_test.go`

Apply the identical transformation under the existing `t.mu`. The offset-0 path keeps the Phase-1 `RenderEmulator`→`CapturePaneContent` fallback; `SetSize` keeps the `SetDetachedSize` loop (tmux geometry) and drops only the viewport calls.

- [ ] **Step 1: Write the failing test**

```go
// append to ui/terminal_test.go
func TestTerminalPane_GotoBottomResetsOffset(t *testing.T) {
	tp := NewTerminalPane()
	tp.scrollOffset = 5
	_ = tp.GotoBottom()
	if tp.scrollOffset != 0 || tp.IsScrolling() {
		t.Fatalf("GotoBottom must reset to live tail; offset=%d", tp.scrollOffset)
	}
}
```
(Match `GotoBottom`'s actual signature in `ui/terminal.go` — if it takes no instance arg today, keep it argless; if it takes one, mirror the preview shape.)

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/ -run TestTerminalPane_ 2>&1 | head`
Expected: FAIL — `tp.scrollOffset`/`IsScrolling` not in the new shape.

- [ ] **Step 3: Implement the offset model**

- Struct: drop `viewport viewport.Model` and `isScrolling bool`; add `scrollOffset int`, `scrollbackAtScrollStart int`, `lastScrollbackLen int`, `newLinesBelow int` (all under `t.mu`). Remove the viewport import + `viewport.New`.
- `UpdateContent` (`ui/terminal.go:87`): when `scrollOffset == 0`, keep the existing `RenderEmulator`/`CapturePaneContent` path into `t.content` (set `t.newLinesBelow = 0`); when `> 0`, render `t.tmuxSession.RenderWindow(scrollOffset, height-1)` (and if `RenderWindow` returns `ok=false`, force `scrollOffset = 0` and use the live path).
- `String()` (`:342`): drop the `isScrolling → viewport.View()` branch; render `t.content`.
- Scroll methods (`:427+`): same offset bodies as preview (Task 3 Step 5), under `t.mu`, using `t.tmuxSession.ScrollbackLen()` for the max. Delete `enterScrollMode` (`:408`).
- `SetSize` (`:58`): keep the `SetDetachedSize` loop; drop `viewport.SetWidth/SetHeight`.

- [ ] **Step 4: Run + commit**

Run: `CGO_ENABLED=0 go test ./ui/ 2>&1 | grep -E 'FAIL|ok.*ui\s'`
Expected: PASS.
```bash
git add ui/terminal.go ui/terminal_test.go
git commit -m "feat(ui): live-scroll offset model for the terminal pane"
```

---

## Task 5: New-lines / jump-to-bottom footer affordance

**Files:**
- Modify: `ui/preview.go`, `ui/terminal.go`
- Test: `ui/preview_test.go`

`newLinesBelow` is already computed in `UpdateContent` (Tasks 3–4). Render a footer on the scrolled branch of `String()`.

- [ ] **Step 1: Write the failing test**

```go
// append to ui/preview_test.go
func TestPreviewPane_ScrolledFooterShowsNewLines(t *testing.T) {
	p := NewPreviewPane()
	p.SetSize(80, 10)
	p.scrollOffset = 3
	p.newLinesBelow = 5
	p.previewState = previewState{fallback: false, text: "some\ncontent"}
	out := p.String()
	if !strings.Contains(out, "5") || !strings.Contains(out, "jump to bottom") {
		t.Fatalf("scrolled footer should mention new lines + jump to bottom; got %q", out)
	}
}
```
Add `"strings"` to the test imports if absent.

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/ -run TestPreviewPane_ScrolledFooter 2>&1 | head`
Expected: FAIL — no footer text yet.

- [ ] **Step 3: Add the footer helper + wire into String()**

In `ui/preview.go`, add:
```go
func scrollFooter(newLines int) string {
	if newLines > 0 {
		return fmt.Sprintf("▼ %d new line(s) — Esc/End to jump to bottom", newLines)
	}
	return "▲ scrolled — Esc/End to jump to bottom"
}
```
In `String()`'s scrolled branch (when `p.scrollOffset > 0`):
```go
	footer := previewScrollFooterStyle.Render(scrollFooter(p.newLinesBelow))
	body := lipgloss.JoinVertical(lipgloss.Left, p.previewState.text, footer)
	return previewPaneStyle.Width(p.width).Render(body)
```
Define `previewScrollFooterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))` near the other pane styles. Add `"fmt"` import if absent. Mirror in `ui/terminal.go` (reuse/define a terminal footer style).

- [ ] **Step 4: Run + commit**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'TestPreviewPane|TestTerminalPane' -v 2>&1 | grep -E '^(--- (PASS|FAIL)|ok|FAIL)'`
Expected: PASS.
```bash
git add ui/preview.go ui/terminal.go ui/preview_test.go
git commit -m "feat(ui): jump-to-bottom footer with live new-line count while scrolled"
```

---

## Task 6: `SplitPane` field-read fix + dispatch verification

**Files:**
- Modify: `ui/split_pane.go`

- [ ] **Step 1: Fix the direct field read**

`ui/split_pane.go:340` `IsAgentInScrollMode` reads `s.agent.isScrolling` (now removed). Change it and its terminal twin to the method:
```go
func (s *SplitPane) IsAgentInScrollMode() bool    { return s.agent.IsScrolling() }
func (s *SplitPane) IsTerminalInScrollMode() bool { return s.terminal.IsScrolling() }
```
Grep for any other `.isScrolling` reference and route through `IsScrolling()`.

- [ ] **Step 2: Build + verify dispatch compiles**

Run: `CGO_ENABLED=0 go build ./... 2>&1 | tail -5 && echo OK`
Expected: clean build. The mouse handler (`app/app.go:729`), `defaults.lua` scroll bindings, and `app_scripts.go` `deferModelMutation` route to the same-named `Scroll*`/`Goto*`/`Reset*` methods — bodies changed underneath, signatures stable, so no edits there.

- [ ] **Step 3: Run + commit**

Run: `CGO_ENABLED=0 go test ./ui/ ./app/ 2>&1 | grep -E 'FAIL|ok'`
Expected: PASS.
```bash
git add ui/split_pane.go
git commit -m "refactor(ui): route SplitPane scroll-mode checks through IsScrolling()"
```

---

## Task 7: Esc / End jump-to-bottom

**Files:**
- Test: `app/state_default_test.go` (or the nearest existing app state test file)

The Esc path in `app/state_default.go:23-42` already calls `IsAgentInScrollMode()` → `ResetAgentToNormalMode()` (and the terminal twin), which now mean "offset>0" → "offset 0". `End` already binds to `scroll_bottom` → `GotoBottom`. **No code change expected** — this task locks the behavior with a test.

- [ ] **Step 1: Write the test**

```go
// in an app-package test: drive a home model whose split pane is scrolled,
// send Esc, assert it returns to live tail (offset 0). Use the existing
// app test harness (direct Update with tea.KeyPressMsg{Code: tea.KeyEsc}).
func TestEscJumpsToLiveTail(t *testing.T) {
	// ... construct home `m` with a started instance and a scrolled agent pane:
	//     m.splitPane scrolled so IsAgentInScrollMode() == true ...
	if !m.splitPane.IsAgentInScrollMode() {
		t.Skip("requires a scrolled agent pane fixture")
	}
	_, _ = handleStateDefaultKey(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.splitPane.IsAgentInScrollMode() {
		t.Fatal("Esc must return the agent pane to live tail")
	}
}
```
If a scrolled-pane fixture is impractical at the app level, instead unit-test `SplitPane`: scroll the agent pane, call `ResetAgentToNormalMode`, assert `!IsAgentInScrollMode()` — and document the manual smoke for the full Esc path.

- [ ] **Step 2: Run + commit**

Run: `CGO_ENABLED=0 go test ./app/ ./ui/ 2>&1 | grep -E 'FAIL|ok'`
Expected: PASS (no production change; test-only).
```bash
git add -A
git commit -m "test: lock Esc/End jump-to-live-tail behavior"
```

---

## Task 8: Diff overlay alignment check

**Files:**
- Test: `ui/diff_test.go` (or `ui/split_pane_test.go`)

The diff overlay (`ui/diff.go`) keeps its `bubbles/viewport` (static content, no live tail). It is already reached by the diff-first routing in `SplitPane` and the mouse handler. **No code change.** Lock that the agent/terminal offset rework didn't disturb diff scroll.

- [ ] **Step 1: Write the test**

```go
// scroll the diff pane and assert it still moves independently of the agent
// pane's offset (the diff has no scrollOffset; it uses its viewport).
func TestDiffScrollUnaffectedByPaneOffset(t *testing.T) {
	// construct a SplitPane with diff visible + content; ScrollDiffUp/Down;
	// assert ScrollPercent changes and the agent pane's scrollOffset is untouched.
}
```

- [ ] **Step 2: Run + commit**

Run: `CGO_ENABLED=0 go test ./ui/ 2>&1 | grep -E 'FAIL|ok'`
Expected: PASS.
```bash
git add -A
git commit -m "test: confirm diff overlay scroll is independent of pane live-scroll"
```

---

## Task 9: Retire the capture-pane history path (dead code)

**Files:**
- Modify: `session/instance.go`, `ui/preview.go`, `ui/terminal.go`, possibly `session/tmux/tmux.go`

History now comes from the emulator scrollback, so the synchronous full-history capture is dead.

- [ ] **Step 1: Confirm callers are gone**

Run:
```bash
grep -rn 'PreviewFullHistory\|enterScrollMode\|CapturePaneContentWithOptions' --include='*.go' . | grep -v '/vendor/'
```
Expected: the only remaining references are the *definitions* (and any tests). If `enterScrollMode`/`PreviewFullHistory`/the `isScrolling` branches were fully removed in Tasks 3–4, there are no live callers.

- [ ] **Step 2: Delete the dead code**

- Remove `Instance.PreviewFullHistory()` (`session/instance.go:~1133`).
- Remove any leftover `enterScrollMode` and `isScrolling` references (should already be gone).
- For `TmuxSession.CapturePaneContentWithOptions(...)`: if Step 1 shows zero non-test callers, delete it; **otherwise keep it.** Keep `CapturePaneContent()` and `RenderEmulator()` (live fallback + daemon).

- [ ] **Step 3: Build + full test**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./... 2>&1 | grep -E 'FAIL|^ok' | grep -v '^ok' ; echo "(empty above = all pass)"`
Expected: clean build, all pass.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor: retire capture-pane history path now that scrollback is in the emulator"
```

---

## Task 10: Full verification + manual smoke

**Files:** none (verification)

- [ ] **Step 1: Full automated gate**

Run:
```bash
gofmt -w session/ ui/ app/
CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go build -o loom .
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
```
Expected: build + binary OK, all tests pass, **0 data races** (the new `RenderWindow` takes the emulator read lock; verify it's clean against the pump's `Write`).

- [ ] **Step 2: Manual acceptance smoke (needs a real terminal)**

`./loom`, start an agent, and verify (these are the parts unit tests can't cover):
1. Scroll up (`shift+up` / `pgup` / wheel) — the window holds position.
2. **Live output keeps flowing while scrolled** — the `▼ N new lines` counter increments as the agent emits more; the visible window does **not** jump.
3. `Esc` and `End` both jump to the live tail; the counter clears.
4. **Resize the window while scrolled** — the window re-renders at the new height next tick, offset stays valid (clamped), no overflow past pane height.
5. Repeat for the terminal pane (`ctrl+t` then scroll).
6. Diff overlay (`d`) still scrolls with the same keys/wheel and shows its percent indicator.
7. `LOOM_PANE_RENDERER=snapshot ./loom` — scrolling is disabled gracefully (stays at live tail), no crash.

If all pass, Phase 2 is complete.

---

## Self-review notes

- **Spec coverage** (`2026-06-19-native-terminal-experience-design.md`, Phase 2 row): scroll decoupled from live tail (Tasks 3–4); jump-to-bottom / N-new-lines affordance (Task 5); kills the mode-switch (Tasks 3–4, 9); panes covered, diff keeps its scroll (Task 8). **Deviation, intentional & better than the spec:** the spec hedged "deep history reconciles to `capture-pane -S -`" — but the verified `x/vt` scrollback (10k lines, auto-grown) *is* the deep history, so capture-pane history is retired entirely (Task 9), not reconciled. `RenderWindow` uses a `fromBottom` convention (offset arithmetic in the emulator) rather than the spec-map's `topOffset` (arithmetic in the pane) — cleaner, one place to get wrong.
- **No placeholders** — `RenderWindow` is real, compilable code against the verified `x/vt`/`ultraviolet` API; the offset model shows full method bodies. Tasks 7–8 are explicitly test-only/no-code-change and say so.
- **Type consistency** — `ScrollbackLen() (int, bool)` and `RenderWindow(fromBottom, rows int) (string, bool)` at the Instance/TmuxSession layer; `int`/`string` (no bool) at the `vt.Emulator` layer; `scrollOffset`/`scrollbackAtScrollStart`/`lastScrollbackLen`/`newLinesBelow` and the `setOffset`/`scrollBy` helpers are named identically across preview and terminal.
- **Build-green per task** — Tasks 1–2 are additive (nothing calls the new methods yet); Tasks 3–8 swap call sites with the live-tail fallback intact; Task 9 deletes dead code only after the swaps. Every task compiles and tests on its own (unlike the Phase 0 atomic cut).
- **Risk flagged** — live-scroll-while-tailing and resize-while-scrolled are the must-verify manual cases (Task 10 Step 2); like Phase 1, the interactive behavior can't be fully unit-tested.
