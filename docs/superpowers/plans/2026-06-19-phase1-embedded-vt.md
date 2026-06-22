# Phase 1: Embedded VT Display Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drive pane *display* from an in-process terminal emulator (`charmbracelet/x/vt`) fed by the tmux-client PTY stream Loom already reads-and-discards, with **zero user-visible behavior change** — same content, lower latency (no `capture-pane` subprocess per frame), higher fidelity, and the foundation for Phase 2+ scroll/select/search.

**Architecture:** A new `session/vt` package defines a tiny `Emulator` interface with one real impl (`xvt`, wrapping `x/vt` under an `RWMutex`). `TmuxSession` gains an `emu` field; its output pump writes raw PTY bytes into the emulator instead of `io.Discard`; `Instance.Preview()` and the terminal pane source their string from `emu.Render()` instead of `capture-pane`. tmux stays the session owner (persistence, full-screen attach, prompt detection for the daemon are all unchanged). The fallback when no emulator is wired (Windows, or the `LOOM_PANE_RENDERER=snapshot` kill-switch) is `emu == nil` → the existing `capture-pane` path, byte-identical to today.

**Tech Stack:** Go 1.23 (`CGO_ENABLED=0` build, vendored deps, tests run `CGO_ENABLED=0`), Bubble Tea v2, new dep `github.com/charmbracelet/x/vt`.

---

## Verified facts (the contract this plan relies on)

**`x/vt` API** (verified via `go doc github.com/charmbracelet/x/vt.Emulator`):
- `func NewEmulator(w, h int) *Emulator`
- `func (e *Emulator) Write(p []byte) (n int, err error)` — `io.Writer`; feeds raw ANSI/CSI/OSC bytes
- `func (e *Emulator) Resize(width, height int)`
- `func (e *Emulator) Render() string` — current **visible** screen as a styled ANSI string (drop-in for `capture-pane -p -e`)
- `func (e *Emulator) String() string` — plain text (no SGR)
- `func (e *Emulator) CursorPosition() uv.Position` — `uv` = `github.com/charmbracelet/ultraviolet`; `Position` has `.X`, `.Y`
- `func (e *Emulator) Close() error`
- `func (e *Emulator) IsAltScreen() bool`, `ScrollbackLen() int`, `Scrollback() *Scrollback` — for later phases; Phase 1 ignores them
- The plain `*Emulator` is **not** concurrency-safe; we guard with our own `sync.RWMutex` (do **not** rely on `x/vt`'s `SafeEmulator`).

**Loom code anchors** (verified against the working tree, post-v2-migration):
- `session/tmux/tmux.go`: `TmuxSession` struct fields `stateMu` (80), `ptmx` (86), `monitor` (88), `pumpMu`/`pumpDest`/`pumpDone`/`pumpCancel` (93-96), struct ends line 97; `startOutputPump` (370-397) with `t.pumpDest = io.Discard` at **373** and the pump loop writing `dest.Write(buf[:n])` at 388-390; `setPumpDest` (421-425); `signalPumpStop` (407-418); `Start` set-option block (244-268, `mouse on` ends at 257, `bind-key` starts at 259), `Start` calls `t.Restore()` at 270; `Restore` (312-337); `currentPtmx` (339+); `SetDetachedSize`/`updateWindowSize` (~687-705); `CapturePaneContent` (~718-733).
- `session/instance.go`: `Preview()` (659-668) returns `ts.CapturePaneContent()`; `PreviewFullHistory()` (1128-1137); `GetContentHash()`/`CaptureAndProcessStatus` (1139+) — these stay on `capture-pane`.
- `ui/terminal.go`: `TerminalPane.UpdateContent` calls `s.tmuxSession.CapturePaneContent()`.
- `app/app.go`: `Init` preview tick + `previewTickMsg` handling (~582-655); `instanceChanged` (~1098-1114). **Unchanged in Phase 1.**
- Test harness: `NewTmuxSession(name, program)` constructs a session; tmux tests are white-box (`package tmux`) and use `os.Pipe` as a ptmx stand-in (see `session/tmux/tmux_pump_test.go`).

---

## File structure

- **Create `session/vt/vt.go`** — the `Emulator` interface + `Cursor` struct. One responsibility: the display abstraction. No deps.
- **Create `session/vt/xvt.go`** — `xvtEmulator` wrapping `x/vt` under an `RWMutex`, plus `NewXVT`.
- **Create `session/vt/vt_test.go`** — interface compile-assertion + `Cursor` zero value.
- **Create `session/vt/xvt_test.go`** — golden tests for the real emulator.
- **Create `session/tmux/emulator_unix.go`** (`//go:build !windows`) — `newEmulator` factory returning `xvt` (or `nil` under the kill-switch).
- **Create `session/tmux/emulator_windows.go`** (`//go:build windows`) — `newEmulator` returning `nil`.
- **Modify `session/tmux/tmux.go`** — `emu`/`lastCols`/`lastRows` fields; `RenderEmulator` accessor; pump redirect; `Restore` build/teardown; `Start` status-off; `SetDetachedSize` resize; `Close`/`PausePreview` teardown.
- **Modify `session/tmux/tmux_pump_test.go`** (or a new `tmux_emulator_test.go`) — pump-redirect + resize wiring tests.
- **Modify `session/instance.go`** — `Preview()` sources from the emulator.
- **Modify `ui/terminal.go`** — terminal pane sources from the emulator.
- **Modify `go.mod`/`go.sum`/`vendor/`** — add `github.com/charmbracelet/x/vt`.

`app/app.go`, `ui/preview.go`, `ui/split_pane.go` are **unchanged** (they consume strings; only the string *source* moves).

---

## Task 1: `session/vt` package — interface + Cursor

**Files:**
- Create: `session/vt/vt.go`
- Test: `session/vt/vt_test.go`

- [ ] **Step 1: Write the failing test**

```go
// session/vt/vt_test.go
package vt

import "testing"

// nopEmulator is a compile-time check that the interface is satisfiable
// and a stand-in for tests that don't need real emulation.
type nopEmulator struct{}

func (nopEmulator) Write(p []byte) (int, error) { return len(p), nil }
func (nopEmulator) Resize(cols, rows int)       {}
func (nopEmulator) Render() string              { return "" }
func (nopEmulator) Cursor() Cursor              { return Cursor{} }
func (nopEmulator) Close() error                { return nil }

var _ Emulator = nopEmulator{}

func TestCursorZeroValue(t *testing.T) {
	var c Cursor
	if c.X != 0 || c.Y != 0 || c.Visible {
		t.Fatalf("zero Cursor should be {0,0,false}, got %+v", c)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/vt/ 2>&1 | head`
Expected: FAIL — `undefined: Emulator` / `undefined: Cursor` (package has no non-test source yet).

- [ ] **Step 3: Write the implementation**

```go
// session/vt/vt.go
// Package vt defines Loom's pane-display abstraction. An Emulator consumes
// raw PTY bytes from the tmux-client stream and renders the current visible
// screen as a string the UI prints verbatim. tmux remains the session owner;
// this is display only.
package vt

// Cursor is the visible cursor position in cells, 0-based, origin top-left.
type Cursor struct {
	X, Y    int
	Visible bool
}

// Emulator is the display surface for one pane. Implementations must be safe
// for one writer goroutine (the tmux output pump calling Write) concurrent
// with reader calls (Render/Cursor) from the Bubble Tea Update goroutine.
type Emulator interface {
	// Write feeds raw PTY bytes (ANSI/CSI/OSC/DCS) into the emulator. It
	// mirrors io.Writer so it can be the output pump's destination directly.
	Write(p []byte) (n int, err error)

	// Resize sets the emulator's screen geometry in cells.
	Resize(cols, rows int)

	// Render returns the current VISIBLE screen as a string with embedded
	// ANSI SGR sequences, sized to the last Resize. The UI prints this verbatim.
	Render() string

	// Cursor returns the current cursor position and visibility.
	Cursor() Cursor

	// Close releases emulator resources. Safe to call multiple times.
	Close() error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/vt/ -v 2>&1 | tail`
Expected: PASS (`TestCursorZeroValue`).

- [ ] **Step 5: Commit**

```bash
git add session/vt/vt.go session/vt/vt_test.go
git commit -m "feat(vt): add Emulator interface for pane display"
```

---

## Task 2: Add `x/vt` dependency + `xvt` impl + golden tests

**Files:**
- Modify: `go.mod`, `go.sum`, `vendor/`
- Create: `session/vt/xvt.go`
- Test: `session/vt/xvt_test.go`

- [ ] **Step 1: Add the dependency**

Run:
```bash
GOFLAGS=-mod=mod go get github.com/charmbracelet/x/vt@latest
GOFLAGS=-mod=mod go mod tidy
GOFLAGS=-mod=mod go mod vendor
```
Expected: `github.com/charmbracelet/x/vt` appears in `go.mod`; `vendor/github.com/charmbracelet/x/vt/` exists. (Most transitive deps — `ansi`, `ultraviolet`, `uniseg`, `x/sys` — are already present from Bubble Tea v2.)

- [ ] **Step 2: Write the failing golden tests**

```go
// session/vt/xvt_test.go
package vt

import (
	"strings"
	"testing"
)

func TestXVT_PlainText(t *testing.T) {
	e := NewXVT(20, 3)
	defer e.Close()
	_, _ = e.Write([]byte("hello"))
	got := stripSGR(e.Render())
	if !strings.HasPrefix(got, "hello") {
		t.Fatalf("expected screen to start with %q, got %q", "hello", got)
	}
}

func TestXVT_SGRColor(t *testing.T) {
	e := NewXVT(20, 1)
	defer e.Close()
	_, _ = e.Write([]byte("\x1b[1;32mhi\x1b[0m"))
	r := e.Render()
	if !strings.Contains(r, "hi") {
		t.Fatalf("rendered screen missing text: %q", r)
	}
	if !strings.Contains(r, "\x1b[") {
		t.Fatalf("rendered screen should carry SGR sequences for colored text: %q", r)
	}
}

func TestXVT_ClearAndHome(t *testing.T) {
	e := NewXVT(20, 3)
	defer e.Close()
	_, _ = e.Write([]byte("garbage\x1b[2J\x1b[Habc"))
	got := stripSGR(e.Render())
	firstLine := strings.SplitN(got, "\n", 2)[0]
	if !strings.HasPrefix(strings.TrimRight(firstLine, " "), "abc") {
		t.Fatalf("after clear+home, first line should be %q, got %q", "abc", firstLine)
	}
}

func TestXVT_ResizeRowCount(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	for i := 0; i < 20; i++ {
		_, _ = e.Write([]byte("line\r\n"))
	}
	e.Resize(80, 10)
	rows := strings.Split(strings.TrimRight(e.Render(), "\n"), "\n")
	if len(rows) > 10 {
		t.Fatalf("after Resize(80,10) visible screen should be <=10 rows, got %d", len(rows))
	}
}

func TestXVT_Deterministic(t *testing.T) {
	render := func() string {
		e := NewXVT(20, 2)
		defer e.Close()
		_, _ = e.Write([]byte("\x1b[33mwarn\x1b[0m\r\nok"))
		return e.Render()
	}
	if render() != render() {
		t.Fatal("same byte stream must produce identical Render() output")
	}
}

// stripSGR removes CSI SGR sequences so tests can assert on visible text.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./session/vt/ -run TestXVT 2>&1 | head`
Expected: FAIL — `undefined: NewXVT`.

- [ ] **Step 4: Write the implementation**

```go
// session/vt/xvt.go
package vt

import (
	"sync"

	xvt "github.com/charmbracelet/x/vt"
)

// xvtEmulator wraps charmbracelet/x/vt. x/vt's plain *Emulator is not safe for
// concurrent Write+Render, so we guard with our own RWMutex: Write/Resize/Close
// take the write lock; Render/Cursor take the read lock. The tmux output pump
// is the sole writer; the Bubble Tea Update goroutine is the reader.
type xvtEmulator struct {
	mu   sync.RWMutex
	term *xvt.Emulator
}

// NewXVT constructs a real terminal emulator sized to cols x rows.
func NewXVT(cols, rows int) Emulator {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return &xvtEmulator{term: xvt.NewEmulator(cols, rows)}
}

func (e *xvtEmulator) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.term.Write(p)
}

func (e *xvtEmulator) Resize(cols, rows int) {
	if cols < 1 || rows < 1 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.term.Resize(cols, rows)
}

func (e *xvtEmulator) Render() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.term.Render()
}

func (e *xvtEmulator) Cursor() Cursor {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p := e.term.CursorPosition()
	return Cursor{X: p.X, Y: p.Y, Visible: true}
}

func (e *xvtEmulator) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.term.Close()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session/vt/ -v 2>&1 | tail -20`
Expected: all PASS. If a test reveals x/vt's actual `Render()` shape differs (e.g. trailing padding), adjust the *assertion* (substring/row-count) — not the impl — to match real output, and record the observed shape in a comment.

- [ ] **Step 6: Race check + commit**

Run: `CC=clang CGO_ENABLED=1 go test -race ./session/vt/ 2>&1 | tail -5`
Expected: PASS, no races.
```bash
git add go.mod go.sum vendor session/vt/xvt.go session/vt/xvt_test.go
git commit -m "feat(vt): add x/vt-backed Emulator with golden tests"
```

---

## Task 3: Concurrent Write/Render race test for `xvt`

**Files:**
- Test: `session/vt/xvt_test.go`

- [ ] **Step 1: Add the race test**

```go
// append to session/vt/xvt_test.go
import "sync"

func TestXVT_ConcurrentWriteRender(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_, _ = e.Write([]byte("x"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = e.Render()
			_ = e.Cursor()
		}
	}()
	wg.Wait()
}
```
(Move the `import "sync"` into the existing import block rather than a second import statement.)

- [ ] **Step 2: Run under the race detector**

Run: `CC=clang CGO_ENABLED=1 go test -race ./session/vt/ -run TestXVT_ConcurrentWriteRender 2>&1 | tail -5`
Expected: PASS, no data race (proves the RWMutex serializes pump-write vs UI-read).

- [ ] **Step 3: Commit**

```bash
git add session/vt/xvt_test.go
git commit -m "test(vt): cover concurrent Write/Render under -race"
```

---

## Task 4: Platform `newEmulator` factory + `LOOM_PANE_RENDERER` kill-switch

**Files:**
- Create: `session/tmux/emulator_unix.go`, `session/tmux/emulator_windows.go`
- Test: `session/tmux/emulator_test.go`

- [ ] **Step 1: Write the failing test**

```go
// session/tmux/emulator_test.go
package tmux

import "testing"

func TestNewEmulator_DefaultOnUnix(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "")
	if e := newEmulator(80, 24); e == nil {
		t.Fatal("unix default should produce a non-nil emulator")
	}
}

func TestNewEmulator_SnapshotKillSwitch(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "snapshot")
	if e := newEmulator(80, 24); e != nil {
		t.Fatal("LOOM_PANE_RENDERER=snapshot must force the nil (capture-pane) fallback")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestNewEmulator 2>&1 | head`
Expected: FAIL — `undefined: newEmulator`.

- [ ] **Step 3: Write the implementations**

```go
// session/tmux/emulator_unix.go
//go:build !windows

package tmux

import (
	"os"

	"github.com/aidan-bailey/loom/session/vt"
)

// newEmulator builds the pane-display emulator for a new PTY attach. Returns
// nil to select the legacy capture-pane path: on the LOOM_PANE_RENDERER=snapshot
// kill-switch (A/B + emergency fallback). A nil emulator makes the output pump
// default to io.Discard and Preview/terminal source from capture-pane.
func newEmulator(cols, rows int) vt.Emulator {
	if os.Getenv("LOOM_PANE_RENDERER") == "snapshot" {
		return nil
	}
	return vt.NewXVT(cols, rows)
}
```

```go
// session/tmux/emulator_windows.go
//go:build windows

package tmux

import "github.com/aidan-bailey/loom/session/vt"

// newEmulator always returns nil on Windows: there is no usable detached ptmx
// stream to feed, so the pane keeps the capture-pane snapshot path (Phase 1 is
// a no-op on Windows).
func newEmulator(cols, rows int) vt.Emulator { return nil }
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestNewEmulator -v 2>&1 | tail`
Expected: PASS both.

- [ ] **Step 5: Commit**

```bash
git add session/tmux/emulator_unix.go session/tmux/emulator_windows.go session/tmux/emulator_test.go
git commit -m "feat(tmux): platform newEmulator factory with LOOM_PANE_RENDERER kill-switch"
```

---

## Task 5: `TmuxSession` emulator field + `RenderEmulator` accessor + geometry fields

**Files:**
- Modify: `session/tmux/tmux.go` (struct ~60-97; add accessor near `CapturePaneContent` ~718)
- Test: `session/tmux/emulator_test.go`

- [ ] **Step 1: Write the failing test**

```go
// append to session/tmux/emulator_test.go
func TestRenderEmulator_NilWhenUnset(t *testing.T) {
	ts := NewTmuxSession("emu-nil", "prog")
	if _, ok := ts.RenderEmulator(); ok {
		t.Fatal("RenderEmulator must report ok=false when no emulator is wired")
	}
}

func TestRenderEmulator_ReadsWiredEmulator(t *testing.T) {
	ts := NewTmuxSession("emu-set", "prog")
	ts.stateMu.Lock()
	ts.emu = vtNewXVTForTest(80, 24) // helper below
	ts.stateMu.Unlock()
	_, _ = ts.emu.Write([]byte("hi"))
	s, ok := ts.RenderEmulator()
	if !ok || !containsText(s, "hi") {
		t.Fatalf("RenderEmulator should return the emulator screen; ok=%v s=%q", ok, s)
	}
}
```

Add these test helpers to `emulator_test.go`:
```go
import (
	"strings"

	"github.com/aidan-bailey/loom/session/vt"
)

func vtNewXVTForTest(c, r int) vt.Emulator { return vt.NewXVT(c, r) }

func containsText(rendered, want string) bool {
	// rendered may carry SGR; a substring check on want (plain ASCII) is enough.
	return strings.Contains(rendered, want)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestRenderEmulator 2>&1 | head`
Expected: FAIL — `ts.emu undefined` and `ts.RenderEmulator undefined`.

- [ ] **Step 3: Add the fields and accessor**

In the struct, after `monitor *statusMonitor` (tmux.go:88), before the pump fields (93):
```go
	// emu is the in-process terminal emulator for pane DISPLAY (Phase 1). The
	// output pump writes raw ptmx bytes into it; Render reads the visible
	// screen for Preview/terminal content. nil selects the legacy capture-pane
	// path (Windows / LOOM_PANE_RENDERER=snapshot). Guarded by stateMu like
	// ptmx: Restore/Close/PausePreview swap or drop it while the preview path
	// reads it. The emulator's own RWMutex makes concurrent Write vs Render safe.
	emu vt.Emulator
	// lastCols/lastRows track the most recent pane geometry from SetDetachedSize
	// so a freshly built emulator in Restore starts at the correct size.
	lastCols int
	lastRows int
```

Add `"github.com/aidan-bailey/loom/session/vt"` to the import block. Initialize geometry defaults in `NewTmuxSession` (set `lastCols: 80, lastRows: 24` in the struct literal, or assign after construction).

Add the accessor near `CapturePaneContent` (~tmux.go:718):
```go
// RenderEmulator returns the current visible screen from the in-process
// emulator as an ANSI-styled string, or ("", false) if no emulator is wired
// (callers then fall back to CapturePaneContent).
func (t *TmuxSession) RenderEmulator() (string, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return "", false
	}
	return emu.Render(), true
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestRenderEmulator -v 2>&1 | tail`
Expected: PASS both.

- [ ] **Step 5: Commit**

```bash
git add session/tmux/tmux.go session/tmux/emulator_test.go
git commit -m "feat(tmux): add emulator field, geometry tracking, and RenderEmulator accessor"
```

---

## Task 6: Redirect the output pump into the emulator

**Files:**
- Modify: `session/tmux/tmux.go` (`startOutputPump` 370-376)
- Test: `session/tmux/emulator_test.go`

- [ ] **Step 1: Write the failing test** (white-box, `os.Pipe` as the ptmx, mirrors `tmux_pump_test.go`)

```go
// append to session/tmux/emulator_test.go
import (
	"os"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOutputPump_FeedsEmulator(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	ts := NewTmuxSession("emu-pump", "prog")
	ts.stateMu.Lock()
	ts.emu = vt.NewXVT(80, 24)
	ts.stateMu.Unlock()

	ts.startOutputPump(r)
	t.Cleanup(func() { ts.signalPumpStop(r); ts.waitPumpExit() })

	_, _ = w.WriteString("pumped-text")
	require.Eventually(t, func() bool {
		s, ok := ts.RenderEmulator()
		return ok && containsText(s, "pumped-text")
	}, time.Second, 10*time.Millisecond, "pump should write ptmx bytes into the emulator")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestOutputPump_FeedsEmulator 2>&1 | head`
Expected: FAIL — pump still defaults to `io.Discard`, emulator stays blank.

- [ ] **Step 3: Change the pump default destination**

Replace `startOutputPump`'s opening (tmux.go:370-376):
```go
func (t *TmuxSession) startOutputPump(ptmx *os.File) {
	ctx, cancel := context.WithCancel(context.Background())
	// Default the pump into the emulator so the visible screen stays current.
	// nil emu (Windows / snapshot kill-switch) keeps the legacy io.Discard drain.
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	var dest io.Writer = io.Discard
	if emu != nil {
		dest = emu
	}
	t.pumpMu.Lock()
	t.pumpDest = dest
	t.pumpCancel = cancel
	t.pumpMu.Unlock()
	t.pumpDone = make(chan struct{})
```
The goroutine body (378-396) is **unchanged** — it already does `dest := t.pumpDest; dest.Write(buf[:n])`, and `setPumpDest(os.Stdout)` for inline attach (421-425) still overrides it. (`stateMu` is released before `pumpMu` is taken, so the locks never nest.)

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestOutputPump' -v 2>&1 | tail`
Expected: PASS — both `TestOutputPump_FeedsEmulator` and the existing `TestOutputPump_UnblocksViaSetReadDeadline` (regression: the nil-emu path still drains).

- [ ] **Step 5: Commit**

```bash
git add session/tmux/tmux.go session/tmux/emulator_test.go
git commit -m "feat(tmux): pump ptmx output into the emulator (io.Discard fallback)"
```

---

## Task 7: Build/teardown the emulator across `Restore`, `Close`, `PausePreview`

**Files:**
- Modify: `session/tmux/tmux.go` (`Restore` 312-337; `Close` ~648; `PausePreview` ~621-638)
- Test: `session/tmux/emulator_test.go`

- [ ] **Step 1: Write the failing test** (uses a mock `PtyFactory`; follow the existing mock in the tmux test suite — `NewTmuxSessionWithDeps`)

```go
// append to session/tmux/emulator_test.go
// Restore builds a fresh emulator on the new ptmx; Close tears it down.
func TestRestore_BuildsEmulator_CloseTearsDown(t *testing.T) {
	ts, fake := newSessionWithFakePTY(t) // helper per existing mock pattern; fake feeds os.Pipe
	t.Setenv("LOOM_PANE_RENDERER", "") // ensure xvt path

	require.NoError(t, ts.Restore())
	if _, ok := ts.RenderEmulator(); !ok {
		t.Fatal("Restore should wire an emulator on unix")
	}

	require.NoError(t, ts.Close())
	if _, ok := ts.RenderEmulator(); ok {
		t.Fatal("Close should tear down the emulator")
	}
	_ = fake
}
```

> Implementation note: `newSessionWithFakePTY` must construct the session via the dependency-injecting constructor (`NewTmuxSessionWithDeps`) with a `PtyFactory` whose `Start` returns an `os.Pipe`-backed `*os.File`, mirroring the mock used elsewhere in `session/tmux/*_test.go`. Reuse that helper if it already exists; otherwise add it next to the existing mock factory.

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestRestore_BuildsEmulator 2>&1 | head`
Expected: FAIL — `Restore` doesn't build an emulator yet (`ok` is false).

- [ ] **Step 3: Build the emulator in `Restore`**

In `Restore` (tmux.go:312-337), tear down the old emulator alongside the old ptmx, and build the new one before `startOutputPump`:

```go
	t.stateMu.Lock()
	old := t.ptmx
	t.ptmx = nil
	oldEmu := t.emu
	t.emu = nil
	cols, rows := t.lastCols, t.lastRows
	t.stateMu.Unlock()
	if old != nil {
		t.signalPumpStop(old)
		_ = old.Close()
	}
	if oldEmu != nil {
		_ = oldEmu.Close()
	}
	t.waitPumpExit()

	ptmx, err := t.ptyFactory.Start(exec.Command("tmux", "attach-session", "-t", t.sanitizedName))
	if err != nil {
		return fmt.Errorf("error opening PTY: %w", err)
	}
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	emu := newEmulator(cols, rows)
	t.stateMu.Lock()
	t.ptmx = ptmx
	t.emu = emu
	t.monitor = newStatusMonitor()
	t.stateMu.Unlock()
	t.startOutputPump(ptmx) // now defaults pumpDest = emu (Task 6)
	return nil
```

- [ ] **Step 4: Tear down in `Close` and `PausePreview`**

In `Close` (~tmux.go:648) and `PausePreview` (~621-638), wherever the code snapshots-and-clears `ptmx` under `stateMu` to stop the pump, also snapshot-and-clear `emu` and `Close()` it outside the lock. Pattern (apply in both):
```go
	t.stateMu.Lock()
	p := t.ptmx
	t.ptmx = nil
	emu := t.emu
	t.emu = nil
	t.stateMu.Unlock()
	// ... existing pump-stop + p.Close() ...
	if emu != nil {
		_ = emu.Close()
	}
```
(`ResumePreview` re-runs `Restore`, which rebuilds the emulator — no extra change there.)

- [ ] **Step 5: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestRestore_BuildsEmulator|TestOutputPump' -v 2>&1 | tail`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add session/tmux/tmux.go session/tmux/emulator_test.go
git commit -m "feat(tmux): build emulator in Restore; tear down in Close/PausePreview"
```

---

## Task 8: Resize the emulator from `SetDetachedSize` (and record geometry)

**Files:**
- Modify: `session/tmux/tmux.go` (`SetDetachedSize` ~687-705)
- Test: `session/tmux/emulator_test.go`

- [ ] **Step 1: Write the failing test**

```go
// append to session/tmux/emulator_test.go
import "strings"

func TestSetDetachedSize_ResizesEmulatorAndRecordsGeometry(t *testing.T) {
	ts := NewTmuxSession("emu-resize", "prog")
	ts.stateMu.Lock()
	ts.emu = vt.NewXVT(80, 24)
	ts.stateMu.Unlock()
	for i := 0; i < 20; i++ {
		_, _ = ts.emu.Write([]byte("line\r\n"))
	}
	// No ptmx wired, so updateWindowSize returns an error we ignore — the
	// emulator resize and geometry recording happen first regardless.
	_ = ts.SetDetachedSize(80, 10)

	ts.stateMu.Lock()
	gotCols, gotRows := ts.lastCols, ts.lastRows
	ts.stateMu.Unlock()
	if gotCols != 80 || gotRows != 10 {
		t.Fatalf("geometry not recorded: got %dx%d", gotCols, gotRows)
	}
	s, _ := ts.RenderEmulator()
	rows := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(rows) > 10 {
		t.Fatalf("emulator should be 10 rows after resize, got %d", len(rows))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestSetDetachedSize_Resizes 2>&1 | head`
Expected: FAIL — geometry not recorded / emulator not resized.

- [ ] **Step 3: Update `SetDetachedSize`**

```go
func (t *TmuxSession) SetDetachedSize(width, height int) error {
	// Record geometry and resize the emulator first so its grid matches the
	// new pane before tmux repaints into the resized PTY. In-memory, cheap.
	t.stateMu.Lock()
	t.lastCols = width
	t.lastRows = height
	emu := t.emu
	t.stateMu.Unlock()
	if emu != nil {
		emu.Resize(width, height)
	}
	return t.updateWindowSize(width, height)
}
```
`updateWindowSize` is unchanged.

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestSetDetachedSize_Resizes -v 2>&1 | tail`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add session/tmux/tmux.go session/tmux/emulator_test.go
git commit -m "feat(tmux): resize emulator and record geometry in SetDetachedSize"
```

---

## Task 9: Disable the tmux status bar at `Start`

**Files:**
- Modify: `session/tmux/tmux.go` (`Start`, after the `mouse on` block ~257, before `bind-key` ~259)

- [ ] **Step 1: Add the `set-option status off` call**

The detached attach stream includes the status line; `capture-pane -p` never did. Hiding it keeps the emulator's screen identical to the old snapshot (no status row consuming a render line). Insert after `mouseCancel()` (tmux.go:257):
```go
	// Disable the tmux status bar. The detached attach stream the emulator
	// consumes includes the status line, but the pane preview must not — it
	// would consume a render row and shift content. tmux still owns the
	// session; only its chrome is hidden.
	statusCtx, statusCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	statusCmd := exec.CommandContext(statusCtx, "tmux", "set-option", "-t", t.sanitizedName, "status", "off")
	if err := t.cmdExec.Run(statusCmd); err != nil {
		log.For("tmux").Warn("status_off_failed", "session", t.sanitizedName, "err", err)
	}
	statusCancel()
```

- [ ] **Step 2: Build + run the tmux suite**

Run: `CGO_ENABLED=0 go test ./session/tmux/ 2>&1 | tail -5`
Expected: builds and existing tmux tests PASS (the status-off effect is verified visually in Task 11 — asserting a specific tmux subprocess call is low-value coupling, so no dedicated unit test).

- [ ] **Step 3: Commit**

```bash
git add session/tmux/tmux.go
git commit -m "feat(tmux): disable status bar so the emulator stream matches capture-pane"
```

---

## Task 10: Source the agent pane (`Instance.Preview`) from the emulator

**Files:**
- Modify: `session/instance.go` (`Preview` 659-668)
- Test: `session/instance_test.go` (or wherever Instance tests live) — optional unit; integration covered in Task 12

- [ ] **Step 1: Update `Preview()`**

```go
func (i *Instance) Preview() (string, error) {
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", nil
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return "", nil
	}
	// Phase 1: source the live screen from the in-process emulator (in-memory,
	// no subprocess). Fall back to capture-pane when no emulator is wired
	// (Windows / LOOM_PANE_RENDERER=snapshot).
	if s, ok := ts.RenderEmulator(); ok {
		return s, nil
	}
	return ts.CapturePaneContent()
}
```
`PreviewFullHistory()`, `GetContentHash()`, and `CaptureAndProcessStatus` are **unchanged** — scrollback and prompt/status detection stay on `capture-pane` in Phase 1.

- [ ] **Step 2: Build + run session tests**

Run: `CGO_ENABLED=0 go test ./session/ 2>&1 | tail -5`
Expected: builds and PASS (existing Preview tests, if any, still pass — emulator is nil in unit tests with no Restore, so they exercise the capture-pane fallback).

- [ ] **Step 3: Commit**

```bash
git add session/instance.go
git commit -m "feat(session): source agent-pane preview from the emulator with capture-pane fallback"
```

---

## Task 11: Source the terminal pane from the emulator

**Files:**
- Modify: `ui/terminal.go` (`TerminalPane.UpdateContent`, the `CapturePaneContent` call)

- [ ] **Step 1: Update the terminal pane content source**

Replace the single `CapturePaneContent` call in `TerminalPane.UpdateContent`:
```go
	rendered, ok := s.tmuxSession.RenderEmulator()
	if !ok {
		var err error
		rendered, err = s.tmuxSession.CapturePaneContent()
		if err != nil {
			return fmt.Errorf("terminal pane: failed to capture content: %w", err)
		}
	}
	content := rendered
```
Use `content` downstream exactly as before (line-truncation, styling, viewport are unchanged). Keep the receiver/variable names consistent with the surrounding method (the cached session field is `s.tmuxSession`; pick a local name that doesn't shadow the receiver).

- [ ] **Step 2: Build + run ui tests**

Run: `CGO_ENABLED=0 go test ./ui/... 2>&1 | tail -5`
Expected: builds and PASS.

- [ ] **Step 3: Commit**

```bash
git add ui/terminal.go
git commit -m "feat(ui): source terminal pane from the emulator with capture-pane fallback"
```

---

## Task 12: First-frame seed, parity, and the acceptance gate

**Files:**
- Modify: `session/tmux/tmux.go` (`Restore`, after `startOutputPump`)
- Verification only otherwise

- [ ] **Step 1: Force a first-frame repaint after attach**

A freshly built emulator is blank until bytes arrive. tmux redraws on client attach, but to guarantee a correct first frame even if tmux is quiescent, nudge a full repaint right after the pump starts in `Restore` (append before `return nil`):
```go
	// Nudge tmux to repaint the whole pane into the freshly attached client so
	// the new emulator fills immediately rather than after the next agent write.
	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	refreshCmd := exec.CommandContext(refreshCtx, "tmux", "refresh-client", "-t", t.sanitizedName)
	if err := t.cmdExec.Run(refreshCmd); err != nil {
		log.For("tmux").Debug("refresh_client_failed", "session", t.sanitizedName, "err", err)
	}
	refreshCancel()
```

- [ ] **Step 2: Full automated verification (the CI gates)**

Run:
```bash
gofmt -w session/ ui/
CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go build -o loom .
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
```
Expected: build clean, binary produced, all tests PASS, no races.

- [ ] **Step 3: Manual acceptance smoke (needs a real terminal)**

Run `./loom` against a throwaway workspace, start an agent, and verify against `tmux attach -t loom_<title>` in a second terminal:
1. **Parity** — colors, spinner animation, prompt boxes, and line wrapping at the pane width match the real tmux pane.
2. **No status bar** — the Loom pane shows no tmux status row (confirms Task 9).
3. **First frame** — on `n`/start and on resume (`r`), the pane is populated immediately, not blank for a tick (confirms Step 1).
4. **Resize** — resize the Loom window; the pane reflows without overflowing its height (confirms Task 8).
5. **Alt-screen** — when the agent enters its full-screen TUI, the Loom pane follows correctly (x/vt active-buffer tracking).
6. **Inline attach** — `ctrl+a`, type/Ctrl-C/arrows reach the agent; `ctrl+q` detaches and the pane resumes live rendering (confirms the `setPumpDest(os.Stdout)` override still composes with the emulator default).
7. **Kill-switch** — relaunch with `LOOM_PANE_RENDERER=snapshot ./loom`; the pane still renders (via capture-pane), proving the fallback path.

- [ ] **Step 4: Commit**

```bash
git add session/tmux/tmux.go
git commit -m "feat(tmux): repaint on attach for a correct first emulator frame"
```

---

## Self-review notes

- **Spec coverage** (`docs/superpowers/specs/2026-06-19-native-terminal-experience-design.md`, Phase 1 row): `Emulator` interface + impl (Tasks 1–2); redirect pump → emulator (Task 6); render the visible screen into both panes (Tasks 10–11); `set status off` (Task 9); sizing (Task 8); build/teardown across attach lifecycle (Task 7). **Deviations, intentional:** (a) the "snapshot impl" is replaced by a simpler `emu == nil` → capture-pane fallback, which is byte-identical legacy behavior and avoids a render-empty edge case — the kill-switch (Task 4) and Windows path both use it; (b) the spec's "event-driven `vtUpdatedMsg`" is **deferred** — Phase 1 keeps the existing 100ms tick, which is now near-free because it reads the in-memory emulator instead of forking `capture-pane`. Event-driven push is unblocked by this design and belongs in a later phase. (c) the capture-pane scrollback seed is replaced by a `refresh-client` repaint nudge (Task 12) — lighter and sufficient for the visible screen; deep scrollback is Phase 2.
- **No placeholders** — every code step shows real code; the `x/vt` API is verified (`go doc`), and the tmux before/after is anchored to verified line numbers. The one harness dependency (`newSessionWithFakePTY` in Task 7) references the existing mock-`PtyFactory` pattern in `session/tmux/*_test.go` rather than inventing one.
- **Type consistency** — `Emulator` (Write/Resize/Render/Cursor/Close), `NewXVT`, `newEmulator`, `RenderEmulator() (string, bool)`, and the `emu`/`lastCols`/`lastRows` fields are used identically across Tasks 1–12.
- **Risk ordering** — `session/vt` (Tasks 1–4) is fully isolated and testable before any tmux change; the tmux wiring (5–9) is guarded by white-box `os.Pipe`/mock-PTY tests; the source swaps (10–11) are one-liners with fallback; Task 12 is the integration/acceptance gate. The build stays green after every task (unlike the Phase 0 atomic cut).
