# Emulator-Owned Scrollback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace per-frame `tmux capture-pane` scroll-back windowing with in-process scrollback from the embedded x/vt emulator, seeded once per attach, behind one `ScrollModel` shared by both panes.

**Architecture:** `vt.Emulator` gains `ScrollbackLen`/`RenderWindow`/`AltScreen`/`SetScrollbackSize` (x/vt already maintains the buffer). `TmuxSession` captures history-only seed rows once per `Restore` and exposes a `scrollSource` view. A new `ui.ScrollModel` owns offset/anchoring/wheel-damping/alt-screen routing; `PreviewPane` and `TerminalPane` delegate to it. The capture-pane windowing survives only as the no-emulator (snapshot/Windows) fallback, hardened.

**Tech Stack:** Go, vendored `github.com/charmbracelet/x/vt` (+ `ultraviolet` for `uv.Line.Render()`), tmux, Bubble Tea v2, testify.

**Spec:** `docs/superpowers/specs/2026-07-15-emulator-scrollback-design.md`

**Verification used throughout:**
- Unit: `CGO_ENABLED=0 go test ./<pkg>/ -run <Name> -v`
- Race: `CC=clang CGO_ENABLED=1 go test -race -count=1 ./<pkg>/`
- Lint stand-in: `gofmt -l . && CGO_ENABLED=0 go vet ./...` (local golangci-lint is v2, incompatible with repo config)

---

### Task 1: Validation spike — what does a tmux client stream push into x/vt scrollback?

The design's one unknown. Everything later assumes scroll-off lines (and only
those) accumulate. This task builds a skippable integration test that answers
it with a real tmux.

**Files:**
- Create: `session/vt/scrollback_integration_test.go`

- [ ] **Step 1: Write the integration test** (skips when tmux is unavailable)

```go
package vt

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

// startTmux launches a detached tmux session (private socket) running sh,
// attaches a client on a PTY, and pumps its output into a fresh emulator.
// Returns the socket name, session name, the emulator, and a cleanup func.
func startTmux(t *testing.T, cols, rows int) (sock, name string, emu Emulator, cleanup func()) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock = fmt.Sprintf("loomvt-%d", os.Getpid())
	name = fmt.Sprintf("sbspike-%d", time.Now().UnixNano())
	run := func(args ...string) {
		out, err := exec.Command("tmux", append([]string{"-L", sock}, args...)...).CombinedOutput()
		require.NoError(t, err, "tmux %v: %s", args, out)
	}
	run("new-session", "-d", "-x", fmt.Sprint(cols), "-y", fmt.Sprint(rows), "-s", name, "sh")
	run("set-option", "-t", name, "status", "off")

	emu = NewXVT(cols, rows)
	attach := exec.Command("tmux", "-L", sock, "attach-session", "-t", name)
	ptmx, err := pty.StartWithSize(attach, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				_, _ = emu.Write(buf[:n])
			}
			if rerr != nil {
				close(done)
				return
			}
		}
	}()
	cleanup = func() {
		_ = exec.Command("tmux", "-L", sock, "kill-server").Run()
		_ = ptmx.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		_ = emu.Close()
	}
	return sock, name, emu, cleanup
}

// sendShell types a command into the tmux session's shell.
func sendShell(t *testing.T, sock, name, cmd string) {
	t.Helper()
	out, err := exec.Command("tmux", "-L", sock, "send-keys", "-t", name, cmd, "Enter").CombinedOutput()
	require.NoError(t, err, "send-keys: %s", out)
}

// settle waits for pane output to go quiet (crude: fixed delay is fine for a spike).
func settle() { time.Sleep(500 * time.Millisecond) }

// TestScrollbackAccumulation_RealTmux answers the design spike:
//  1. lines scrolled off the top land in scrollback, in order;
//  2. `clear` does not multiply history;
//  3. detach/reattach repaints do not pollute scrollback.
func TestScrollbackAccumulation_RealTmux(t *testing.T) {
	sock, name, emu, cleanup := startTmux(t, 80, 10)
	defer cleanup()
	settle()

	// Emit 30 numbered lines through a 10-row screen → ≥20 must scroll off.
	sendShell(t, sock, name, `for i in $(seq 1 30); do echo "spikeline$i"; done`)
	settle()

	got := emu.ScrollbackLen()
	require.Greater(t, got, 15, "scroll-off lines must accumulate in scrollback")
	window := emu.RenderWindow(got, 5) // top of the buffer
	require.NotEmpty(t, window)

	// Full-buffer sanity: every scrollback line renders and early lines exist.
	all := emu.RenderWindow(emu.ScrollbackLen(), emu.ScrollbackLen()+10)
	require.Contains(t, stripANSI(all), "spikeline1")

	// `clear` must not multiply history.
	before := emu.ScrollbackLen()
	sendShell(t, sock, name, "clear")
	settle()
	after := emu.ScrollbackLen()
	require.LessOrEqual(t, after, before+10,
		"clear must not push more than one screen into scrollback (got %d -> %d)", before, after)

	// Redraw (refresh-client forces a repaint) must not pollute scrollback.
	before = emu.ScrollbackLen()
	out, err := exec.Command("tmux", "-L", sock, "refresh-client").CombinedOutput()
	require.NoError(t, err, "refresh-client: %s", out)
	settle()
	require.LessOrEqual(t, emu.ScrollbackLen(), before+1,
		"a client repaint must not push content into scrollback")
}
```

- [ ] **Step 2: Add the `stripANSI` helper to the same file**

```go
// stripANSI removes CSI/OSC escapes so Contains-assertions see plain text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI ... final byte @-~
			for i++; i < len(s) && (s[i] < '@' || s[i] > '~'); i++ {
			}
		case ']': // OSC ... BEL or ST
			for i++; i < len(s) && s[i] != 0x07 && s[i] != 0x1b; i++ {
			}
			if i+1 < len(s) && s[i] == 0x1b {
				i++ // consume the '\' of ST
			}
		}
	}
	return b.String()
}
```

- [ ] **Step 3: Run — expect compile FAILURE (RenderWindow/ScrollbackLen don't exist yet)**

Run: `CGO_ENABLED=0 go test ./session/vt/ -run TestScrollbackAccumulation -v`
Expected: compile error `emu.ScrollbackLen undefined`. That's the RED for Task 2. Leave this test in place; it goes green at the end of Task 2 and its assertions ARE the spike verdict.

- [ ] **Step 4: Commit the spike test (it may not compile yet — commit together with Task 2's interface if pre-commit hooks build; otherwise commit now)**

```bash
git add session/vt/scrollback_integration_test.go
git commit -m "test(vt): add tmux scrollback-accumulation spike (red until RenderWindow lands)"
```

**Decision gate (executes at the end of Task 2):** if the `clear`/repaint assertions FAIL against real tmux, stop and report — the mitigation (suppress scrollback pushes during the attach-repaint window) must be designed before Tasks 3+ proceed. Do not silently loosen the assertions.

---

### Task 2: `vt.Emulator` scrollback API

**Files:**
- Modify: `session/vt/vt.go` (interface)
- Modify: `session/vt/xvt.go` (implementation)
- Create: `session/vt/scrollback_test.go` (unit tests, no tmux needed)

- [ ] **Step 1: Write failing unit tests**

```go
package vt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// feed writes lines through the emulator as a program would print them.
func feed(t *testing.T, e Emulator, lines ...string) {
	t.Helper()
	for _, l := range lines {
		_, err := e.Write([]byte(l + "\r\n"))
		require.NoError(t, err)
	}
}

func TestScrollbackLen_CountsScrolledOffLines(t *testing.T) {
	e := NewXVT(20, 4)
	defer e.Close()
	require.Equal(t, 0, e.ScrollbackLen())
	feed(t, e, "l1", "l2", "l3", "l4", "l5", "l6")
	// 4-row screen, 6 lines printed + trailing prompt row: at least 2 scrolled off.
	require.GreaterOrEqual(t, e.ScrollbackLen(), 2)
}

func TestRenderWindow_OffsetZeroEqualsRender(t *testing.T) {
	e := NewXVT(20, 4)
	defer e.Close()
	feed(t, e, "a", "b")
	require.Equal(t, e.Render(), e.RenderWindow(0, 4))
}

func TestRenderWindow_WindowsIntoScrollback(t *testing.T) {
	e := NewXVT(20, 4)
	defer e.Close()
	feed(t, e, "l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8")
	sb := e.ScrollbackLen()
	require.Greater(t, sb, 0)
	top := e.RenderWindow(sb, 1) // the oldest line
	require.Contains(t, stripANSI(top), "l1")
	// A window one line up from live must end with the second-to-last row.
	w := e.RenderWindow(1, 4)
	require.Equal(t, 4, len(strings.Split(w, "\n")))
}

func TestRenderWindow_ClampsOutOfRange(t *testing.T) {
	e := NewXVT(20, 4)
	defer e.Close()
	feed(t, e, "x")
	require.NotPanics(t, func() {
		_ = e.RenderWindow(1<<30, 4)
		_ = e.RenderWindow(-5, 4)
		_ = e.RenderWindow(0, 0)
	})
	require.Equal(t, "", e.RenderWindow(0, 0))
}

func TestAltScreen_TracksMode1049(t *testing.T) {
	e := NewXVT(20, 4)
	defer e.Close()
	require.False(t, e.AltScreen())
	_, _ = e.Write([]byte("\x1b[?1049h"))
	require.True(t, e.AltScreen())
	// Output on the alt screen must not grow primary scrollback.
	before := e.ScrollbackLen()
	feed(t, e, "a1", "a2", "a3", "a4", "a5", "a6")
	require.Equal(t, before, e.ScrollbackLen())
	_, _ = e.Write([]byte("\x1b[?1049l"))
	require.False(t, e.AltScreen())
}

func TestSetScrollbackSize_CapsRetention(t *testing.T) {
	e := NewXVT(20, 2)
	defer e.Close()
	e.SetScrollbackSize(5)
	for i := 0; i < 30; i++ {
		feed(t, e, "line")
	}
	require.LessOrEqual(t, e.ScrollbackLen(), 5)
}
```

(`stripANSI` comes from Task 1's file — same package.)

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./session/vt/ -run 'TestScrollback|TestRenderWindow|TestAltScreen|TestSetScrollback' -v`
Expected: compile errors — methods undefined on `Emulator`.

- [ ] **Step 3: Extend the interface in `session/vt/vt.go`** (append inside `type Emulator interface`)

```go
	// ScrollbackLen returns the number of lines scrolled off the top of the
	// primary screen. Alt-screen output never accumulates (x/vt returns the
	// main screen's buffer regardless of the active screen).
	ScrollbackLen() int

	// RenderWindow returns exactly `rows` lines ending `offset` lines above
	// the live bottom, composed from scrollback + the visible screen, with
	// ANSI styles. offset 0 with rows == screen height is equivalent to
	// Render(). Out-of-range offset/rows are clamped; rows < 1 returns "".
	// Positions above the top of the buffer render as blank lines.
	RenderWindow(offset, rows int) string

	// AltScreen reports whether the inner app is on the alternate screen
	// (DEC private mode 47/1047/1049).
	AltScreen() bool

	// SetScrollbackSize caps scrollback retention in lines.
	SetScrollbackSize(n int)
```

- [ ] **Step 4: Implement on `xvtEmulator` in `session/vt/xvt.go`** (readers under RLock, mirroring Render; `strings` is already imported — add it if not)

```go
func (e *xvtEmulator) ScrollbackLen() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.term.ScrollbackLen()
}

func (e *xvtEmulator) AltScreen() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.term.IsAltScreen()
}

func (e *xvtEmulator) SetScrollbackSize(n int) {
	e.mu.Lock()
	e.term.SetScrollbackSize(n)
	e.mu.Unlock()
}

func (e *xvtEmulator) RenderWindow(offset, rows int) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if rows < 1 {
		return ""
	}
	screen := strings.Split(e.term.Render(), "\n")
	sb := e.term.Scrollback() // nil while on the alt screen
	sbLen := 0
	if sb != nil {
		sbLen = sb.Len()
	}
	total := sbLen + len(screen)
	if offset < 0 {
		offset = 0
	}
	if maxOff := total - rows; offset > maxOff {
		if maxOff < 0 {
			maxOff = 0
		}
		offset = maxOff
	}
	// Window is [top, top+rows) in a coordinate space where index 0 is the
	// oldest scrollback line and total-1 is the bottom screen row.
	top := total - offset - rows
	out := make([]string, rows)
	for i := 0; i < rows; i++ {
		idx := top + i
		switch {
		case idx < 0:
			out[i] = ""
		case idx < sbLen:
			out[i] = sb.Line(idx).Render()
		default:
			out[i] = screen[idx-sbLen]
		}
	}
	return strings.Join(out, "\n")
}
```

- [ ] **Step 5: Run the unit tests**

Run: `CGO_ENABLED=0 go test ./session/vt/ -v`
Expected: all PASS, including Task 1's `TestScrollbackAccumulation_RealTmux` (this is the spike verdict — see the decision gate in Task 1).

- [ ] **Step 6: Race check** (Write pump vs reader calls)

Run: `CC=clang CGO_ENABLED=1 go test -race -count=1 ./session/vt/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add session/vt/vt.go session/vt/xvt.go session/vt/scrollback_test.go session/vt/scrollback_integration_test.go
git commit -m "feat(vt): expose x/vt scrollback, windowed render, and alt-screen state"
```

---

### Task 3: Concurrent-access pin test for the new readers

**Files:**
- Modify: `session/vt/scrollback_test.go`

- [ ] **Step 1: Write the race-pin test**

```go
// TestRenderWindow_ConcurrentWithWrite pins the lock discipline: the pump
// goroutine Writes while the Update goroutine reads windows. Run with -race.
func TestRenderWindow_ConcurrentWithWrite(t *testing.T) {
	e := NewXVT(40, 8)
	defer e.Close()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = e.Write([]byte("concurrent line output\r\n"))
			}
		}
	}()
	for i := 0; i < 500; i++ {
		_ = e.RenderWindow(i%50, 8)
		_ = e.ScrollbackLen()
		_ = e.AltScreen()
	}
	close(stop)
	<-done
}
```

- [ ] **Step 2: Run under race**

Run: `CC=clang CGO_ENABLED=1 go test -race -count=1 ./session/vt/ -run TestRenderWindow_ConcurrentWithWrite -v`
Expected: PASS, no race report.

- [ ] **Step 3: Commit**

```bash
git add session/vt/scrollback_test.go
git commit -m "test(vt): pin RenderWindow/ScrollbackLen lock discipline under -race"
```

---

### Task 4: `TmuxSession` — history seed + scroll accessors

**Files:**
- Modify: `session/tmux/tmux.go`
- Create: `session/tmux/scroll_source_test.go`

- [ ] **Step 1: Write failing tests** (mock cmdExec pattern from existing tmux tests)

```go
package tmux

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSeedHistory_CapturedOnRestore: Restore must run one history-only
// capture (`capture-pane ... -S - -E -1`) and store the rows.
func TestSeedHistory_CapturedOnRestore(t *testing.T) {
	var sawHistoryOnly bool
	cmdExec := newMockExecForScroll(t, func(cmd *exec.Cmd) ([]byte, error) {
		s := cmd.String()
		if strings.Contains(s, "capture-pane") && strings.Contains(s, "-E -1") {
			sawHistoryOnly = true
			return []byte("old1\nold2\nold3"), nil
		}
		return []byte(""), nil
	})
	ts := NewTmuxSessionWithDeps("seedtest", "bash", newMockPty(t), cmdExec)
	require.NoError(t, ts.Start(t.TempDir()))
	defer func() { _ = ts.Close() }()

	require.True(t, sawHistoryOnly, "Restore must capture history-only seed")
	require.Equal(t, []string{"old1", "old2", "old3"}, ts.SeedHistory())
}

// TestSeedHistory_EmptyOnCaptureFailure: a failed seed capture must not
// fail Restore; SeedHistory is just empty.
func TestSeedHistory_EmptyOnCaptureFailure(t *testing.T) {
	cmdExec := newMockExecForScroll(t, func(cmd *exec.Cmd) ([]byte, error) {
		if strings.Contains(cmd.String(), "capture-pane") {
			return nil, exec.ErrNotFound
		}
		return []byte(""), nil
	})
	ts := NewTmuxSessionWithDeps("seedfail", "bash", newMockPty(t), cmdExec)
	require.NoError(t, ts.Start(t.TempDir()))
	defer func() { _ = ts.Close() }()
	require.Empty(t, ts.SeedHistory())
}

// TestScrollAccessors_NoEmulator: without an emulator every accessor
// reports ok=false / zero values (snapshot path).
func TestScrollAccessors_NoEmulator(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "snapshot")
	cmdExec := newMockExecForScroll(t, func(cmd *exec.Cmd) ([]byte, error) { return []byte(""), nil })
	ts := NewTmuxSessionWithDeps("noemu", "bash", newMockPty(t), cmdExec)
	require.NoError(t, ts.Start(t.TempDir()))
	defer func() { _ = ts.Close() }()

	_, ok := ts.ScrollbackLen()
	require.False(t, ok)
	_, ok = ts.RenderWindow(0, 10)
	require.False(t, ok)
	_, ok = ts.EmuAltScreen()
	require.False(t, ok)
}
```

Add the two small helpers at the bottom of the same file, modeled on the
existing test scaffolding in this package (reuse the package's existing mock
PTY factory if one exists — check `tmux_test.go` first and use that instead
of writing a new one; the shapes below are the fallback):

```go
func newMockExecForScroll(t *testing.T, output func(*exec.Cmd) ([]byte, error)) cmd_test.MockCmdExec {
	sessionCreated := false
	return cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			s := cmd.String()
			if strings.Contains(s, "has-session") {
				if sessionCreated {
					return nil
				}
				return fmt.Errorf("session does not exist")
			}
			if strings.Contains(s, "new-session") {
				sessionCreated = true
			}
			return nil
		},
		OutputFunc: output,
	}
}
```

(imports: `fmt`, `github.com/aidan-bailey/loom/cmd/cmd_test`.)

`newMockPty`: `session/tmux`'s existing tests already have a mock `PtyFactory`
— grep `PtyFactory` in `session/tmux/*_test.go` and reuse it under this name.
Only if none exists, add this one (it mirrors `app`'s `runningPtyFactory`:
run the command through the mock exec so `new-session` flips the
session-created flag, hand back /dev/null):

```go
type scrollTestPty struct {
	t       *testing.T
	cmdExec cmd_test.MockCmdExec
}

func (f scrollTestPty) Start(cmd *exec.Cmd) (*os.File, error) {
	h, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		f.t.Fatalf("scrollTestPty: /dev/null: %v", err)
	}
	_ = f.cmdExec.Run(cmd)
	return h, nil
}
func (f scrollTestPty) Close() {}

func newMockPty(t *testing.T, cmdExec cmd_test.MockCmdExec) PtyFactory {
	return scrollTestPty{t: t, cmdExec: cmdExec}
}
```

(If you use this fallback, `newMockPty(t)` in the tests becomes
`newMockPty(t, cmdExec)` — thread the same mock exec both places.)

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestSeedHistory|TestScrollAccessors' -v`
Expected: compile errors — `SeedHistory`/`ScrollbackLen`/`RenderWindow`/`EmuAltScreen` undefined.

- [ ] **Step 3: Implement on `TmuxSession`**

Add the field (next to `emu` in the struct, guarded by `stateMu`):

```go
	// seedHistory holds pre-attach scrollback rows captured once per
	// Restore (history-only: capture-pane -S - -E -1, so it can never
	// overlap what the emulator mirrors post-attach). Immutable after
	// assignment; guarded by stateMu.
	seedHistory []string
```

In `Restore()`, BEFORE the attach-session PTY is spawned (before the
`attach-session` exec / `ptyFactory.Start` call — capture-first means a
millisecond gap of lost lines, never duplicates):

```go
	// Seed pre-attach history. Rows that scroll off between this capture
	// and the attach are lost from scroll-back (tiny window, accepted by
	// design) — the alternative, capturing after attach, would duplicate
	// rows the emulator also observed.
	if seed, ok := t.captureHistoryRowsOnly(); ok {
		t.stateMu.Lock()
		t.seedHistory = seed
		t.stateMu.Unlock()
	} else {
		log.For("tmux").Warn("seed_history_capture_failed", "session", t.sanitizedName)
	}
```

New methods (bottom of tmux.go, near CaptureHistory):

```go
// captureHistoryRowsOnly captures the pane's HISTORY rows (excluding the
// visible screen) with ANSI styles: capture-pane -S - -E -1. Row -1 is the
// last history line in tmux's coordinate space (0 = first visible row).
// Returns (nil, true) when the pane simply has no history yet.
func (t *TmuxSession) captureHistoryRowsOnly() ([]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-e", "-S", "-", "-E", "-1", "-t", t.sanitizedName)
	output, err := t.cmdExec.Output(cmd)
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimRight(string(output), "\n")
	if trimmed == "" {
		return nil, true
	}
	return strings.Split(trimmed, "\n"), true
}

// SeedHistory returns the pre-attach history rows captured at the last
// Restore. Callers must treat the slice as immutable.
func (t *TmuxSession) SeedHistory() []string {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	return t.seedHistory
}

// ScrollbackLen returns the emulator's scrollback line count; ok=false on
// the snapshot path (no emulator).
func (t *TmuxSession) ScrollbackLen() (int, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return 0, false
	}
	return emu.ScrollbackLen(), true
}

// RenderWindow renders `rows` lines ending `offset` lines above the live
// bottom from the emulator's scrollback + screen; ok=false without an
// emulator.
func (t *TmuxSession) RenderWindow(offset, rows int) (string, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return "", false
	}
	return emu.RenderWindow(offset, rows), true
}

// EmuAltScreen reports the emulator-tracked alternate-screen state;
// ok=false without an emulator (callers fall back to the tmux probe).
func (t *TmuxSession) EmuAltScreen() (alt bool, ok bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return false, false
	}
	return emu.AltScreen(), true
}
```

Note the lock rule: snapshot `t.emu` under `stateMu`, release, then call the
emulator (its methods take the xvt lock; holding `stateMu` across them is
forbidden by the repo's lock rules).

- [ ] **Step 4: Run the tests**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -v`
Expected: new tests PASS, all existing tmux tests still PASS. (If existing
mocks now see an extra `capture-pane -E -1` Output call during Start/Restore,
their `OutputFunc` fallthrough already returns `("", nil)` — verify none
assert on exact call counts; fix any that do by tolerating the seed capture.)

- [ ] **Step 5: Race check**

Run: `CC=clang CGO_ENABLED=1 go test -race -count=1 ./session/tmux/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add session/tmux/tmux.go session/tmux/scroll_source_test.go
git commit -m "feat(tmux): seed pre-attach history on Restore and expose emulator scroll accessors"
```

---

### Task 5: `ui.ScrollModel` — the one shared scroll state machine

**Files:**
- Create: `ui/scroll.go`
- Create: `ui/scroll_test.go`

- [ ] **Step 1: Write failing tests against a fake source**

```go
package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeScrollSource simulates an emulator-backed session: a seed, a growing
// scrollback, and a fixed-height screen.
type fakeScrollSource struct {
	seed      []string
	sb        []string // scrollback lines, oldest first
	screen    []string // visible rows
	alt       bool
	ok        bool
	forwarded []int // +1 per wheel-up, -1 per wheel-down (n times)
}

func (f *fakeScrollSource) SeedHistory() []string { return f.seed }
func (f *fakeScrollSource) ScrollbackLen() (int, bool) {
	if !f.ok {
		return 0, false
	}
	return len(f.sb), true
}
func (f *fakeScrollSource) RenderWindow(offset, rows int) (string, bool) {
	if !f.ok {
		return "", false
	}
	buf := append(append([]string{}, f.sb...), f.screen...)
	total := len(buf)
	if offset > total-rows {
		offset = max(total-rows, 0)
	}
	top := total - offset - rows
	out := make([]string, rows)
	for i := range out {
		if idx := top + i; idx >= 0 && idx < total {
			out[i] = buf[idx]
		}
	}
	return strings.Join(out, "\n"), true
}
func (f *fakeScrollSource) EmuAltScreen() (bool, bool) { return f.alt, f.ok }
func (f *fakeScrollSource) ForwardWheel(up bool, n int) error {
	d := -n
	if up {
		d = n
	}
	f.forwarded = append(f.forwarded, d)
	return nil
}

func newFake() *fakeScrollSource {
	return &fakeScrollSource{
		seed:   []string{"seedA", "seedB", "seedC"},
		sb:     []string{"sb1", "sb2", "sb3", "sb4"},
		screen: []string{"scr1", "scr2", "scr3"},
		ok:     true,
	}
}

func TestScrollModel_LiveTailByDefault(t *testing.T) {
	var m ScrollModel
	require.False(t, m.IsScrolling())
	_, live, ok := m.Window(newFake(), 3)
	require.True(t, ok)
	require.True(t, live, "offset 0 must report live tail")
}

func TestScrollModel_WindowsScrollbackThenSeed(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 2) // 2 above bottom
	w, live, ok := m.Window(f, 3)
	require.True(t, ok)
	require.False(t, live)
	require.Equal(t, "sb3\nsb4\nscr1", w)

	// Scroll past the emulator span into the seed.
	// Logical buffer: [seedA seedB seedC sb1 sb2 sb3 sb4 scr1 scr2 scr3],
	// offset 6 → window rows are indexes 1..3.
	m.ScrollBy(f, 4)
	w, _, _ = m.Window(f, 3)
	require.Equal(t, "seedB\nseedC\nsb1", w)
}

func TestScrollModel_ClampsAtSeedTop(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.GotoTop(f)
	w, _, _ := m.Window(f, 3)
	require.Equal(t, "seedA\nseedB\nseedC", w)
	// One more up-tick stays pinned.
	m.ScrollBy(f, 1)
	w, _, _ = m.Window(f, 3)
	require.Equal(t, "seedA\nseedB\nseedC", w)
}

func TestScrollModel_AnchorsWhenScrollbackGrows(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 2)
	w1, _, _ := m.Window(f, 3)
	// Two lines scroll off the screen into scrollback (output arrived).
	f.sb = append(f.sb, "scr1", "scr2")
	f.screen = []string{"scr3", "new1", "new2"}
	w2, _, _ := m.Window(f, 3)
	require.Equal(t, w1, w2, "content under the cursor must stay put as output accrues")
	require.Equal(t, 2, m.NewLinesBelow(), "footer must count lines accrued below")
}

func TestScrollModel_ScrollDownReturnsToLive(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 2)
	m.ScrollBy(f, -2)
	_, live, _ := m.Window(f, 3)
	require.True(t, live)
	require.Equal(t, 0, m.NewLinesBelow())
}

func TestScrollModel_ResetOnResizeAndSwitch(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 3)
	m.Reset()
	require.False(t, m.IsScrolling())
}

func TestScrollModel_AltScreenForwardsWheel(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.alt = true
	// Damped: wheelEventsPerNotch events per forwarded notch.
	for i := 0; i < wheelEventsPerNotch; i++ {
		require.NoError(t, m.ScrollUp(f))
	}
	require.Equal(t, []int{1}, f.forwarded)
	require.False(t, m.IsScrolling(), "alt-screen scroll must not move the window offset")
}

func TestScrollModel_NoEmulatorReportsNotOK(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.ok = false
	m.ScrollBy(f, 2)
	_, _, ok := m.Window(f, 3)
	require.False(t, ok, "no emulator → caller must use the snapshot fallback")
}

func TestScrollModel_ScrollPercent(t *testing.T) {
	var m ScrollModel
	f := newFake()
	require.Equal(t, 1.0, m.ScrollPercent(f, 3))
	m.GotoTop(f)
	_, _, _ = m.Window(f, 3)
	require.Equal(t, 0.0, m.ScrollPercent(f, 3))
}
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./ui/ -run TestScrollModel -v`
Expected: compile errors — `ScrollModel`, `scrollSource` undefined.

- [ ] **Step 3: Implement `ui/scroll.go`**

```go
package ui

// scrollSource is the pane-agnostic view of one session's scroll data: an
// immutable pre-attach seed, the emulator's growing scrollback + screen,
// alt-screen state, and wheel forwarding for alt-screen TUIs. Implemented
// by *tmux.TmuxSession. Every accessor's ok=false means "no emulator" —
// the caller falls back to snapshot capture-pane windowing.
type scrollSource interface {
	SeedHistory() []string
	ScrollbackLen() (int, bool)
	RenderWindow(offset, rows int) (string, bool)
	EmuAltScreen() (alt bool, ok bool)
	ForwardWheel(up bool, n int) error
}

// ScrollModel owns pane scroll state for the emulator path: the
// lines-from-bottom offset, output anchoring, the new-lines-below footer
// count, wheel damping, and alt-screen routing. Both panes embed one.
// All methods run on the Update goroutine (no locking; TerminalPane's own
// mutex wraps its calls).
type ScrollModel struct {
	// offset is lines-from-bottom into seed+scrollback+screen; 0 = live tail.
	offset int
	// lastSb anchors the view: scrollback growth while scrolled bumps
	// offset by the same amount so content stays put. Cell-line counts are
	// width-stable, so there is no re-wrap drift (unlike capture-pane rows).
	lastSb int
	// baselineSb is the scrollback length when the gesture started; the
	// footer's new-lines count is the growth since then.
	baselineSb int
	// wheelAccum dampens forwarded wheel notches on alt-screen TUIs.
	wheelAccum int
}

// Window renders `rows` lines at the current offset. live=true means the
// pane is at the live tail (offset 0) and the caller should render its
// normal live view instead. ok=false means no emulator (snapshot fallback).
// Window also performs the per-render anchoring bump and clamping, so it
// must be called exactly once per render pass.
//
// GotoTop reuses the package's existing scrollToTopOffset sentinel
// (ui/preview.go); Window clamps it to the real top.
func (m *ScrollModel) Window(src scrollSource, rows int) (window string, live bool, ok bool) {
	sbLen, sbOK := src.ScrollbackLen()
	if !sbOK {
		return "", false, false
	}
	if m.offset > 0 {
		// Anchor: new scroll-off lines appeared below the window; bump the
		// offset so content under the cursor stays put. Cell-line counts
		// are width-stable, so there is no re-wrap drift.
		if grown := sbLen - m.lastSb; grown > 0 {
			m.offset += grown
		}
	}
	m.lastSb = sbLen

	if rows < 1 {
		rows = 1
	}
	seed := src.SeedHistory()
	// The emulator screen is sized to the pane, so its row count is ≥ the
	// scrolled view's rows (view reserves a footer line); using `rows` for
	// the screen span keeps the clamp conservative — one row of slack at
	// the very top, never an out-of-range window.
	total := len(seed) + sbLen + rows
	if maxOff := total - rows; m.offset > maxOff {
		m.offset = maxOff
	}
	if m.offset <= 0 {
		m.offset = 0
		m.baselineSb = 0
		return "", true, true
	}

	// Compose over the logical buffer [seed | scrollback+screen]:
	// idx 0 = oldest seed line, total-1 = bottom screen row. A window has
	// at most one seed→emulator boundary, so emit seed rows until the
	// boundary, then take the rest in one RenderWindow call.
	top := total - m.offset - rows
	seedLen := len(seed)
	lines := make([]string, 0, rows)
	for i := 0; i < rows; {
		idx := top + i
		if idx < seedLen {
			if idx >= 0 {
				lines = append(lines, seed[idx])
			} else {
				lines = append(lines, "") // blank-pad above the top
			}
			i++
			continue
		}
		// Emulator region: the composed window's bottom sits m.offset
		// lines above live, and RenderWindow shares that coordinate
		// space (total - top - rows == m.offset), so pass it through.
		w, wok := src.RenderWindow(m.offset, rows-i)
		if !wok {
			return "", false, false
		}
		lines = append(lines, strings.Split(w, "\n")...)
		break
	}
	return strings.Join(lines, "\n"), false, true
}
```

Remaining methods:

```go
// ScrollBy moves the offset by delta (positive = up into history).
// The gesture baseline is captured on the first move away from the tail.
func (m *ScrollModel) ScrollBy(src scrollSource, delta int) {
	sbLen, ok := src.ScrollbackLen()
	if !ok {
		return
	}
	if m.offset == 0 && delta > 0 {
		m.baselineSb = sbLen
		m.lastSb = sbLen
	}
	m.offset += delta
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset == 0 {
		m.baselineSb = 0
	}
}

// GotoTop pins the view to the oldest line (clamped in Window).
// scrollToTopOffset is the existing sentinel in ui/preview.go.
func (m *ScrollModel) GotoTop(src scrollSource) { m.ScrollBy(src, scrollToTopOffset) }

// Reset returns to the live tail (resize, instance switch, Esc/End).
func (m *ScrollModel) Reset() {
	m.offset = 0
	m.baselineSb = 0
	m.lastSb = 0
	m.wheelAccum = 0
}

// NewLinesBelow is the footer count: scroll-off lines accrued since the
// gesture began.
func (m *ScrollModel) NewLinesBelow() int {
	if m.offset <= 0 {
		return 0
	}
	if n := m.lastSb - m.baselineSb; n > 0 {
		return n
	}
	return 0
}

// IsScrolling reports whether the pane is away from the live tail.
func (m *ScrollModel) IsScrolling() bool { return m.offset > 0 }

// ScrollPercent maps the offset to [0,1]; 1.0 = live tail.
func (m *ScrollModel) ScrollPercent(src scrollSource, rows int) float64 {
	if m.offset <= 0 {
		return 1.0
	}
	sbLen, ok := src.ScrollbackLen()
	if !ok {
		return 1.0
	}
	maxOff := len(src.SeedHistory()) + sbLen
	if maxOff <= 0 {
		return 1.0
	}
	p := 1.0 - float64(m.offset)/float64(maxOff)
	if p < 0 {
		return 0
	}
	return p
}

// ScrollUp/ScrollDown route one wheel/key tick: alt-screen TUIs get damped
// forwarded wheel events; everything else moves the window.
func (m *ScrollModel) ScrollUp(src scrollSource) error   { return m.route(src, true, 1) }
func (m *ScrollModel) ScrollDown(src scrollSource) error { return m.route(src, false, 1) }

// PageUp/PageDown move by half the given pane height (or forward a burst).
func (m *ScrollModel) PageUp(src scrollSource, height int) error {
	if m.altScreen(src) {
		return src.ForwardWheel(true, agentPageNotches)
	}
	m.ScrollBy(src, +(height / 2))
	return nil
}
func (m *ScrollModel) PageDown(src scrollSource, height int) error {
	if m.altScreen(src) {
		return src.ForwardWheel(false, agentPageNotches)
	}
	m.ScrollBy(src, -(height / 2))
	return nil
}

func (m *ScrollModel) altScreen(src scrollSource) bool {
	alt, ok := src.EmuAltScreen()
	return ok && alt
}

func (m *ScrollModel) route(src scrollSource, up bool, delta int) error {
	if m.altScreen(src) {
		return m.forwardWheelDamped(src, up)
	}
	if up {
		m.ScrollBy(src, +delta)
	} else {
		m.ScrollBy(src, -delta)
	}
	return nil
}

// forwardWheelDamped forwards one notch per wheelEventsPerNotch
// same-direction events (accumulator resets on direction change) — moved
// verbatim from PreviewPane.
func (m *ScrollModel) forwardWheelDamped(src scrollSource, up bool) error {
	if m.wheelAccum != 0 && (m.wheelAccum > 0) != up {
		m.wheelAccum = 0
	}
	if up {
		m.wheelAccum++
		if m.wheelAccum >= wheelEventsPerNotch {
			m.wheelAccum = 0
			return src.ForwardWheel(true, 1)
		}
		return nil
	}
	m.wheelAccum--
	if m.wheelAccum <= -wheelEventsPerNotch {
		m.wheelAccum = 0
		return src.ForwardWheel(false, 1)
	}
	return nil
}
```

(`wheelEventsPerNotch` and `agentPageNotches` already exist in `ui/preview.go`
— same package, no move needed yet.)

- [ ] **Step 4: Run the tests, iterate on the index math until green**

Run: `CGO_ENABLED=0 go test ./ui/ -run TestScrollModel -v`
Expected: PASS. The boundary tests (`WindowsScrollbackThenSeed`,
`ClampsAtSeedTop`) are the ones that catch off-by-ones — trust them over the
prose.

- [ ] **Step 5: Commit**

```bash
git add ui/scroll.go ui/scroll_test.go
git commit -m "feat(ui): add ScrollModel — shared emulator-backed pane scroll state"
```

---

### Task 6: `PreviewPane` on `ScrollModel`

**Files:**
- Modify: `ui/preview.go`
- Modify: `ui/split_pane.go` (only if method signatures shift — check call sites)
- Test: existing `ui/` + `app/` suites are the regression net (they exercise the snapshot fallback, which keeps the old windowing)

- [ ] **Step 1: Rewire `PreviewPane`**

Replace the scroll-state fields (`scrollOffset`, `scrollStarting`,
`totalAtScrollStart`, `lastTotal`, `newLinesBelow`, `altScreen`,
`altScreenChecked`, `wheelAccum`) with:

```go
	// scroll owns offset/anchoring/wheel state on the emulator path.
	scroll ScrollModel
	// snapFallback carries the legacy capture-pane windowing state, used
	// only when the instance has no emulator (snapshot / Windows).
	snapFallback snapshotScroll
```

where `snapshotScroll` is the OLD field set extracted verbatim into a small
struct (same file):

```go
// snapshotScroll is the legacy capture-pane windowing state, kept only for
// the no-emulator path. See ScrollModel for the emulator path.
type snapshotScroll struct {
	offset             int
	starting           bool
	totalAtScrollStart int
	lastTotal          int
	newLinesBelow      int
}
```

- [ ] **Step 2: Route `UpdateContent`**

In `UpdateContent`, the scrolled branch becomes: try the emulator path first,
fall back to the legacy windowing.

```go
	if !p.scroll.IsScrolling() && p.snapFallback.offset == 0 {
		return p.liveTail(instance)
	}

	rows := p.height - 1
	if rows < 1 {
		rows = 1
	}
	if src, ok := scrollSourceFor(instance); ok {
		w, live, ok := p.scroll.Window(src, rows)
		if ok {
			if live {
				return p.liveTail(instance)
			}
			p.previewState = previewState{fallback: false, text: w}
			return nil
		}
	}
	return p.updateContentSnapshotScrolled(instance, rows)
```

with the helper (shared by both panes — put it in `ui/scroll.go`):

```go
// scrollSourceFor adapts an instance's tmux session to scrollSource.
// ok=false when the instance has no live session.
func scrollSourceFor(instance *session.Instance) (scrollSource, bool) {
	if instance == nil {
		return nil, false
	}
	ts := instance.TmuxSession()
	if ts == nil {
		return nil, false
	}
	return ts, true
}
```

`updateContentSnapshotScrolled` is the CURRENT scrolled branch of
`UpdateContent` moved into a method operating on `p.snapFallback` instead of
the deleted fields, with one behavior change (spec §5): when
`total < lastTotal` (shrink) reset to live tail instead of drifting:

```go
	case p.snapFallback.lastTotal > 0 && total < p.snapFallback.lastTotal:
		// Buffer shrank (clear-history / alt-screen flip / re-wrap):
		// the anchor is meaningless — snap back to live.
		p.snapFallback = snapshotScroll{}
		return p.liveTail(instance)
```

- [ ] **Step 3: Route the scroll methods**

`ScrollUp/ScrollDown/PageUp/PageDown/GotoTop/GotoBottom/ResetToNormalMode/
IsScrolling/ScrollPercent` delegate: emulator present → `p.scroll`;
otherwise the existing snapshot behavior operating on `p.snapFallback`.
Delete `isAgentTUI`, `forwardWheel`, `forwardWheelDamped` and the
`altScreen`/TTL fields — on the emulator path `ScrollModel` handles routing
via `EmuAltScreen`; on the snapshot path keep ONE direct
`instance.IsAlternateScreen()` call (no cache — it's a per-keystroke probe
only on the legacy path):

```go
func (p *PreviewPane) ScrollUp(instance *session.Instance) error {
	if src, ok := scrollSourceFor(instance); ok {
		if _, emuOK := src.ScrollbackLen(); emuOK {
			return p.scroll.ScrollUp(src)
		}
	}
	// Snapshot path: probe tmux directly (rare path, no TTL cache).
	if instance.IsAlternateScreen() {
		return instance.ForwardWheel(true, 1)
	}
	p.snapshotScrollBy(instance, +1)
	return nil
}
```

(and symmetrically for the other five; `SetSize` additionally calls
`p.scroll.Reset()` + zeroes `p.snapFallback` when the width changes — spec §4
resize rule. Instance switch in `UpdateContent` calls both resets too.)

- [ ] **Step 4: Run the full ui + app suites**

Run: `CGO_ENABLED=0 go test ./ui/ ./app/`
Expected: PASS. `TestPaneDirtyRerendersScrolledAgent` and
`TestPreviewTickRerendersScrolledAgent` build their instances under
`LOOM_PANE_RENDERER=snapshot` (no emulator), so they exercise the fallback
path unchanged — if they fail, the fallback extraction broke behavior; fix
the extraction, not the tests.

- [ ] **Step 5: Add one emulator-path pane test**

In `ui/scroll_test.go`:

```go
// TestPreviewPane_EmulatorScrollPathWindows: with a scrollSource-capable
// session the pane must window in-process (no CaptureHistory subprocess).
// Constructing a full Instance with an emulator is heavy; this pins the
// routing decision instead: ScrollModel engaged → snapshot state untouched.
func TestPreviewPane_ScrollRoutingPrefersEmulator(t *testing.T) {
	p := NewPreviewPane()
	p.SetSize(40, 10)
	f := newFake()
	p.scroll.ScrollBy(f, 3)
	w, live, ok := p.scroll.Window(f, 9)
	require.True(t, ok)
	require.False(t, live)
	require.NotEmpty(t, w)
	require.Zero(t, p.snapFallback.offset, "emulator path must not touch snapshot state")
}
```

Run: `CGO_ENABLED=0 go test ./ui/ -run TestPreviewPane_ScrollRouting -v` → PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/preview.go ui/scroll.go ui/scroll_test.go ui/split_pane.go
git commit -m "feat(ui): drive agent-pane scrolling from emulator scrollback"
```

---

### Task 7: `TerminalPane` on `ScrollModel` (+ the two fallback hardenings)

**Files:**
- Modify: `ui/terminal.go`
- Modify: `ui/scroll_test.go` (routing test for the terminal pane)

- [ ] **Step 1: Rewire scroll state**

Replace `scrollOffset`/`scrollStarting`/`totalAtScrollStart`/`lastTotal`/
`newLinesBelow` in the `TerminalPane` struct with:

```go
	// scroll owns offset/anchoring/wheel state on the emulator path;
	// snapFallback carries the legacy capture-pane windowing state for the
	// no-emulator path. Both guarded by t.mu (ScrollModel is unlocked by
	// design; this pane's mutex is its guard).
	scroll       ScrollModel
	snapFallback snapshotScroll
```

(`snapshotScroll` is defined in Task 6.) The scrolled branch of
`UpdateContent` routes exactly like the preview pane, with the cached
session as the source directly (`*tmux.TmuxSession` implements
`scrollSource`):

```go
	if !t.scroll.IsScrolling() && t.snapFallback.offset == 0 {
		// ... existing live-tail branch unchanged ...
	}

	rows := t.height - 1
	if rows < 1 {
		rows = 1
	}
	w, live, ok := t.scroll.Window(s, rows)
	if ok {
		if live {
			// ... existing live-tail render (RenderEmulator/CapturePaneContent) ...
			return nil
		}
		t.fallback = false
		t.content = w
		t.newLinesBelowRender = t.scroll.NewLinesBelow() // see String() note below
		return nil
	}
	// No emulator → legacy capture-pane windowing on t.snapFallback,
	// with the Step 2 locking fix and the shrink reset from Task 6.
```

`String()`'s footer read (`t.newLinesBelow`) switches to a small
`newLinesBelowRender int` field written by `UpdateContent` (both paths), so
the render function keeps reading one field regardless of path.

- [ ] **Step 2: Fix the lock-across-subprocess violation on the fallback**

The legacy scrolled branch must capture WITHOUT holding `t.mu` (audit
finding 2). Restructure `UpdateContent`'s fallback:

```go
	// Snapshot the session handle under the lock, capture unlocked, then
	// re-lock to apply. Never hold t.mu across the tmux subprocess.
	s := t.currentSessionLocked()
	t.mu.Unlock()
	hist, hok := s.CaptureHistory()
	t.mu.Lock()
```

(The method already holds `t.mu` via `defer`; the unlock/relock pair goes
around only the capture. Re-validate `t.currentTitle` hasn't changed after
re-locking — if it has, discard the capture and return nil: a stale capture
for a switched-away instance must not overwrite the new instance's view.)

- [ ] **Step 3: Give the terminal pane alt-screen routing (audit finding 5)**

`ScrollUp/ScrollDown/PageUp/PageDown/GotoTop` currently just move the
offset. Route them like Task 6's methods: emulator path → `t.scroll` (which
handles alt-screen via `EmuAltScreen`); snapshot path → direct
`s.IsAlternateScreen()` probe → `s.ForwardWheel(...)`, else move
`t.snapFallback`. Note these methods take no instance parameter today — they
resolve the session via `t.currentSessionLocked()`; keep that.

- [ ] **Step 4: Routing test**

```go
// TestTerminalPane_AltScreenScrollForwardsWheel: a full-screen TUI in the
// terminal pane must receive wheel events instead of a frozen window.
func TestTerminalPane_ScrollModelAltRouting(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.alt = true
	for i := 0; i < wheelEventsPerNotch; i++ {
		require.NoError(t, m.ScrollUp(f))
	}
	require.NotEmpty(t, f.forwarded, "alt-screen terminal scroll must forward wheel events")
}
```

Run: `CGO_ENABLED=0 go test ./ui/ -run TestTerminalPane_ScrollModelAltRouting -v` → PASS.

- [ ] **Step 5: Full suites + race**

Run: `CGO_ENABLED=0 go test ./ui/ ./app/ && CC=clang CGO_ENABLED=1 go test -race -count=1 ./ui/ ./app/`
Expected: PASS. The race run matters here — Step 2 changed the locking.

- [ ] **Step 6: Commit**

```bash
git add ui/terminal.go ui/scroll_test.go
git commit -m "feat(ui): terminal pane on ScrollModel; fix lock-across-capture and alt-screen scroll"
```

---

### Task 8: Cleanup + docs

**Files:**
- Modify: `ui/preview.go` (dead constants: `agentScrollTTL` if unreferenced)
- Modify: `CLAUDE.md` (pane-renderer + gotchas sections)
- Modify: `docs/superpowers/specs/2026-07-15-emulator-scrollback-design.md` (status → Implemented)

- [ ] **Step 1: Sweep dead code**

Run: `CGO_ENABLED=0 go vet ./... && grep -rn "agentScrollTTL\|altScreenChecked\|isAgentTUI" ui/ app/ session/`
Expected: no remaining references (delete any stragglers, including now-unused
imports; `gofmt -l .` must stay empty).

- [ ] **Step 2: Update CLAUDE.md**

In the `LOOM_PANE_RENDERER` env-var paragraph and the `session/tmux/` +
event-driven-panes gotcha sections: replace the stale
"`RenderWindow`/`ScrollbackLen` back live-scroll" claim with the real
architecture — scroll-back is emulator-owned (`vt.Emulator.RenderWindow`,
seeded once per attach from `capture-pane -S - -E -1`), one shared
`ui.ScrollModel`, capture-pane windowing only on the snapshot path.

- [ ] **Step 3: Full verification**

Run: `gofmt -l . && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... && CC=clang CGO_ENABLED=1 go test -race -count=1 ./session/... ./ui/ ./app/`
Expected: all clean/PASS.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore: remove capture-pane scroll path remnants; document emulator scrollback"
```

---

### Task 9: Live verification

- [ ] **Step 1: Build and run against a real agent**

```bash
CGO_ENABLED=0 go build -o loom && ./loom
```

Manual script: start an instance, generate >2 screens of output, wheel-up
(must be instant, no capture-pane in `ps`), watch output accrue (footer
counts, view anchored), Esc to jump back, resize while scrolled (resets to
tail), open vim in the terminal pane and wheel (forwards, doesn't freeze),
restart Loom against the same tmux session and confirm pre-restart output is
reachable (seed).

- [ ] **Step 2: Report results honestly; fix or file anything that misbehaves before declaring done**
