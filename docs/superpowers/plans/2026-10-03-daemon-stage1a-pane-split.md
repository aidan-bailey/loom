# Loom Daemon Stage 1A: Pane Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan package by package: each package (A–D) is one task for the sub-skill. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Session lifecycle code never attaches to a tmux session. `session.Instance` holds a lifecycle-only `tmux.Session`, and the TUI renders every agent pane from attach clients it owns in a registry keyed by tmux session name. Nothing changes for the user.

**Architecture:** `session/tmux` gains `Session`, which launches, probes, types into and kills a tmux session with tmux commands and owns no PTY. `TmuxSession` embeds a `Session` and keeps the attach client (PTY, output pump, emulator); the terminal pane's shells keep using it end to end. `ui.PaneClients` holds one `TmuxSession` client per live agent session. Every pane render, scroll, cursor, mouse, paste and key forward goes through it via `ui.Pane`, and so does the status scrape that reads a pane's screen. `app` attaches clients when a workspace loads, when a start, resume or recover lands in a loaded slot, after a workspace terminal's restart, and when the health tick repairs one. It releases them on the health tick, on kill and pause, and on slot drops. Prompts go into the pane through `tmux load-buffer` + `paste-buffer`, and trust-prompt answers through `send-keys`. Both deliver the same bytes as the PTY write they replace.

**Tech Stack:** Go 1.25, Bubble Tea v2 (`charm.land/bubbletea/v2`), tmux ≥ 3.6, testify. Spec: [`docs/superpowers/specs/2026-10-03-loom-daemon-design.md`](../specs/2026-10-03-loom-daemon-design.md), stage 1. On 2026-10-03 stage 1 was split into 1A–1D; this plan is 1A.

---

## Why this stage exists

The daemon spec's stage 1 moves the model out of the TUI behind a `Core` boundary. In its end state the TUI attaches panes to tmux by session name. Today it doesn't: every pane reads through the instance's own `*tmux.TmuxSession`, and lifecycle code is what creates and attaches that object (`Start` → `Restore`, `EnsureRunning`, `Resume`, `CrashRestart`, `Restart`). A daemon that kept attaching its own clients would fight the TUI's clients, and any second TUI's, over the window size. So the display/lifecycle split comes first, before anything else moves.

Stage 1 is planned as four plans. Each one leaves the TUI working end to end:

| Plan | Scope |
|---|---|
| **1A (this)** | The TUI owns pane clients, attached by name. Lifecycle stops attaching and types keys through `send-keys`. |
| 1B | `core.Model`: workspaces, storage, reconcile, sweeps, ticks, gated jobs and completions move out of `home`, still called on the Update goroutine. `ui.List` mirrors core's order. |
| 1C | The `Core` interface, `InstanceView` and events; draft rows for creation flows; Lua lifecycle goes through `Core`. |
| 1D | The model runs on its own goroutine, over channels. |

Through stage 1 the model loads the TUI's open tabs, as today. Loading every registered workspace waits for the daemon (stage 3).

## Decisions

| # | Decision |
|---|---|
| 1 | **Split by embedding, not renaming.** The new `tmux.Session` is the lifecycle half. `TmuxSession` embeds `*Session` and is the attach client. Renaming `TmuxSession` to `Client` would touch about 115 references with no behavioural gain, so it waits for stage 4's cleanup. |
| 2 | **Text goes in through `load-buffer` + `paste-buffer -d -r`; keys through `send-keys <keys>`.** *Amended 2026-10-03 after Package A's review.* The plan first used `send-keys -l -- <text>`: a probe on tmux 3.7b gave byte-identical results to a PTY write for ordinary text. But it has two failure modes. tmux refuses a command over about 16 KiB (`command too long`), and it parses a trailing `;` as a command separator even after `--`, so `abc;` types `abc`. An issue-born prompt embeds the whole issue body, so it would be lost silently. `load-buffer -b <unique> -` (text on stdin) followed by `paste-buffer -d -r -b <unique> -t =<session>:` delivers raw bytes atomically with neither limit. The real-tmux parity test covers both falsifying cases. The A1 code blocks below still show the original `send-keys -l` version; the committed `Session.TypeText` (Package A's fix commit) supersedes them. |
| 3 | **The registry is keyed by tmux session name, with one client per session.** Today two open workspaces holding the same title each attach a client to their shared `loom_<title>` session. The registry attaches one. |
| 4 | **Attach on the Update goroutine, release off it.** Attaching there has precedent: `RepairPtmx` and `ResumePreview` already do. Releasing can't run there, because `PausePreview` waits for the output pump, which blocks in `tea.Program.Send` until Update returns. A client that `Replace` or `Retain` hands back is closed by `releaseClientsCmd`. |
| 5 | **Trust-prompt detection stays in the TUI's status scrape, and only the answer moves to `send-keys` (key names, unaffected by decision 2's amendment).** Detection needs the screen, which only a client has. A daemon-side launch watch that reads `capture-pane` is stage 3 work. |
| 6 | **Lua `inst:preview()` reads `capture-pane` after the flip,** which needs no client. Lua `send_keys`, `send_prompt` and `tap_enter` go through `send-keys` from A2. |
| 7 | **Every package leaves the TUI working.** Package A is internal to `session/tmux`. In Package B every display read goes through the registry, which falls back to the instance's own client, so behaviour is unchanged. Package C flips the instance to `tmux.Session` and the registry to its own clients, and wires every attach and release point. |
| 8 | **Status and hooks are untouched.** Pane events still drive hook scans and the status ladder. Making the ladder a display-only overlay is 1C; the daemon's hook-scan timer is stage 3. |

Out of scope:
- `Core`, `InstanceView` and the model goroutine (1B–1D).
- The terminal pane's `loom_term_*` shells. The TUI keeps creating them with `TmuxSession.Start`, and `Instance.Kill` and `Pause` already kill them by name (`CloseRelatedSession`).
- Renaming `TmuxSession` (stage 4).

## Packages

| Package | Delivers | Commit |
|---|---|---|
| **A** | `tmux.Session` (lifecycle, no PTY), `send-keys` input, attach clients by name | `refactor(tmux): split a lifecycle-only Session from the attach client` |
| **B** | `ui.PaneClients` + `ui.Pane`; every display read and input forward goes through them | `refactor(ui,app): read agent panes through PaneClients` |
| **C** | The flip: instances hold a `tmux.Session`; the app attaches and releases clients | `feat: session lifecycle never attaches; the TUI owns its pane clients` |
| **D** | CLAUDE.md, then full verification (suite, race, e2e, sandbox smoke) | `docs: CLAUDE.md for the pane split (daemon stage 1A)` |

A package is one unit of work: one implementer, one review, one commit (plus fixups the review asks for). Its numbered subsections (A1, A2, …) and their steps are checkpoints inside it, not commits. Do them in order: each builds on the last.

## Conventions for every package

- Run Go commands from the worktree root. Plain tests need `CGO_ENABLED=0`, e.g. `CGO_ENABLED=0 go test ./session/tmux/...`.
- Race detector: `CC=clang CGO_ENABLED=1 go test -race ./app/... ./ui/... ./session/...`.
- Format only tracked non-vendor files plus new ones: `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') <new files>`. Never run `gofmt -w .`, which rewrites `vendor/`.
- The local golangci-lint is v2 while the repo config is v1-shaped, so use `go vet ./...` instead.
- Commit messages end with the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Never run `./loom` directly. To see the TUI, use the `loom-dev` skill (`go run ./tools/loomdev …`).
- Real-tmux tests call `privateTmux(t, tag)` (session/tmux) or `isolateTmux(t)` (app). Nothing may reach the developer's tmux server, so a test that would run real tmux on the default server (the production executor with no private socket) is a bug.

## File structure

**New**
| File | Responsibility |
|---|---|
| `session/tmux/session.go` | `Session`: launch, kill, liveness, `capture-pane`, `send-keys` input, trust-prompt answer |
| `session/tmux/attach.go` | `NewAttachClient`, `NewAttachClientWithDeps`, `TmuxSession.DetectStatus` |
| `session/tmux/session_test.go` | `Session` unit tests (`argvRecorder`) |
| `session/tmux/sendkeys_realtmux_test.go` | Byte parity of `send-keys` and a PTY write |
| `session/tmux/attach_test.go` | Attach-client and `DetectStatus` tests, plus a real-tmux attach by name |
| `ui/panes.go` | `PaneClients` (the registry) and `Pane` (one pane's display surface) |
| `ui/panes_test.go` | Registry and `Pane` tests |
| `app/panes.go` | `paneSnapshot`, `livePaneNames`, `ensurePane`, `replacePane`, `ensureSlotPanes`, `prunePanes` |
| `app/panes_test.go` | App pane lifecycle tests |
| `app/testpanes_test.go` | `testPanes`, `attachTestClient`, `clientOf`, `wirePanes` (Package C) |

**Modified**
| File | Change |
|---|---|
| `session/tmux/tmux.go` | `TmuxSession` embeds `*Session`, with `Start`/`Close` overrides; lifecycle methods moved out; `TapDAndEnter` deleted |
| `session/agent_pane.go` | Input goes through `send-keys`; lifecycle-only after the flip |
| `session/instance.go`, `session/reconcile.go`, `session/agent_restart.go` | Use `*tmux.Session`; nothing attaches |
| `ui/preview.go`, `ui/scroll.go`, `ui/cursor.go`, `ui/card.go`, `ui/list.go`, `ui/overview.go`, `ui/split_pane.go` | Read the agent pane through `PaneClients` |
| `app/app.go`, `app/app_init.go`, `app/events.go`, `app/workspaces.go`, `app/completions.go`, `app/intents.go`, `app/interact.go`, `app/state_inline_attach.go`, `app/overview.go`, `app/github.go` | Registry wiring, attach and release points |
| Tests in `session/`, `session/tmux/`, `ui/`, `app/` | Fixtures attach through the registry |
| `CLAUDE.md` | Architecture bullets and gotchas |

---

## Package A: `session/tmux` splits lifecycle from the attach client

`session/tmux` gains `Session`: launch, probe, type into and kill a session with tmux commands, no PTY. `TmuxSession` embeds it and stays the attach client. Prompts move to `load-buffer`/`paste-buffer` and trust-prompt answers to `send-keys` (see decision 2's amendment; A1's code shows the pre-review `send-keys -l` version), and a client can attach by name to a session it did not start. Outside `session/` the only change is how prompts and keys reach the agent (`send-keys`, byte-identical to the PTY write it replaces); `session.Instance` still holds a `TmuxSession` until Package C. One commit at the end.

### A1. `tmux.Session` types into a session without a PTY

**Files:**
- Create: `session/tmux/session.go`
- Create: `session/tmux/session_test.go`
- Create: `session/tmux/sendkeys_realtmux_test.go`

- [ ] **Step 1: Write the failing unit tests**

`session/tmux/session_test.go`:
```go
package tmux

import (
	"errors"
	"os/exec"
	"slices"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// argvRecorder is a tmux executor that records the argv of every command
// it runs. has-session answers "no such session" the first time it is
// asked and "alive" after that, which is the sequence Start's pre-launch
// check and post-launch poll expect. No tmux server is contacted.
type argvRecorder struct {
	mu     sync.Mutex
	probed bool
	fail   error // when set, every Run returns it
	runs   [][]string
}

func (r *argvRecorder) runner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.runs = append(r.runs, slices.Clone(c.Args))
			if r.fail != nil {
				return r.fail
			}
			if slices.Contains(c.Args, "has-session") && !r.probed {
				r.probed = true
				return errors.New("can't find session")
			}
			return nil
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.runs = append(r.runs, slices.Clone(c.Args))
			return []byte{}, nil
		},
	}
}

// ran returns the argv of every recorded `tmux <sub> …` command, in order.
func (r *argvRecorder) ran(sub string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]string
	for _, argv := range r.runs {
		if len(argv) > 1 && argv[1] == sub {
			out = append(out, argv)
		}
	}
	return out
}

func TestSession_TypeTextSendsLiteralKeys(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("typed", "claude", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.TypeText("-v fix it"))

	assert.Equal(t, [][]string{{"tmux", "send-keys", "-l", "-t", "=loom_typed:", "--", "-v fix it"}}, rec.ran("send-keys"),
		"literal, exactly targeted, and a leading dash is typed rather than parsed as a flag")
}

func TestSession_TypeTextEmptyRunsNothing(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("typed", "claude", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.TypeText(""))

	assert.Empty(t, rec.ran("send-keys"))
}

func TestSession_PressKeysNamesKeysInOrder(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("keys", "aider", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.PressKeys("D", "Enter"))

	assert.Equal(t, [][]string{{"tmux", "send-keys", "-t", "=loom_keys:", "D", "Enter"}}, rec.ran("send-keys"))
}

func TestSession_SendPromptTypesThenPressesEnter(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("prompt", "claude", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.SendPrompt("fix the login bug"))

	assert.Equal(t, [][]string{
		{"tmux", "send-keys", "-l", "-t", "=loom_prompt:", "--", "fix the login bug"},
		{"tmux", "send-keys", "-t", "=loom_prompt:", "Enter"},
	}, rec.ran("send-keys"))
}

func TestSession_TypeTextReportsTmuxFailure(t *testing.T) {
	rec := &argvRecorder{fail: errors.New("no server running")}
	s := NewSessionWithDeps("gone", "claude", NewMockPtyFactory(t), rec.runner())

	err := s.TypeText("x")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "loom_gone")
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `CGO_ENABLED=0 go test ./session/tmux -run TestSession_ -v`
Expected: FAIL to compile with `undefined: NewSessionWithDeps`.

- [ ] **Step 3: Write `session/tmux/session.go`**

```go
package tmux

import (
	"context"
	"fmt"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/agent"
)

// Session is one tmux session on the server as session lifecycle sees it:
// launched, probed, typed into and killed through tmux commands. It never
// attaches a client (no PTY, output pump or emulator), so a process that
// holds only Sessions renders no pane and takes no part in the window-size
// negotiation between attached clients. TmuxSession embeds one and adds
// the attach client the TUI renders from.
type Session struct {
	// sanitizedName is the tmux session name: ToLoomTmuxName of a title.
	sanitizedName string
	// program is the command line the session runs.
	program string
	// adapter is the agent adapter resolved from program: the
	// trust-prompt and pending-prompt patterns.
	adapter agent.Adapter
	// env holds "KEY=VALUE" entries applied to the session via
	// `new-session -e`, e.g. ANTHROPIC_BASE_URL under Headroom Proxy.
	// Scoped to this session; never touches program.
	env []string
	// ptyFactory runs `new-session`, which tmux is handed a terminal for.
	ptyFactory PtyFactory
	// cmdExec runs every other tmux command.
	cmdExec internalexec.Executor
}

// NewSession returns the Session for the instance titled name, wired to
// the production PTY factory and executor. Nothing runs until Start. env,
// if given, is applied to the session via `new-session -e`.
func NewSession(name, program string, env ...string) *Session {
	return newSanitizedSession(ToLoomTmuxName(name), program, MakePtyFactory(), internalexec.Default{}, env...)
}

// NewSessionWithDeps is NewSession with injected dependencies, so tests
// never spawn tmux or allocate a PTY.
func NewSessionWithDeps(name, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *Session {
	return newSanitizedSession(ToLoomTmuxName(name), program, ptyFactory, cmdExec, env...)
}

// newSanitizedSession is NewSessionWithDeps for a name that has already
// been through ToLoomTmuxName.
func newSanitizedSession(sanitizedName, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *Session {
	return &Session{
		sanitizedName: sanitizedName,
		program:       program,
		adapter:       adapterRegistry.Lookup(program),
		env:           env,
		ptyFactory:    ptyFactory,
		cmdExec:       cmdExec,
	}
}

// SessionName returns the tmux session name. It is the identity pane
// events (Notifier callbacks) carry and the key the TUI's pane clients use.
func (s *Session) SessionName() string {
	return s.sanitizedName
}

// TypeText types text into the session's active pane as literal keys
// (`send-keys -l`). tmux delivers exactly text's bytes to the program,
// the same bytes a write to an attach client's PTY delivers
// (TestSendKeysMatchesPTYWrite_RealTmux), so no client is needed. "--"
// ends tmux's option parsing, which means text that starts with '-' is
// typed rather than parsed. Empty text runs nothing.
func (s *Session) TypeText(text string) error {
	if text == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := Command(ctx, "send-keys", "-l", "-t", PaneTarget(s.sanitizedName), "--", text)
	if err := s.cmdExec.Run(cmd); err != nil {
		return fmt.Errorf("type into tmux session %s: %w", s.sanitizedName, err)
	}
	return nil
}

// PressKeys presses keys in order, each given as a tmux key name ("Enter",
// "D", "C-c"). An empty list runs nothing.
func (s *Session) PressKeys(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	args := append([]string{"send-keys", "-t", PaneTarget(s.sanitizedName)}, keys...)
	if err := s.cmdExec.Run(Command(ctx, args...)); err != nil {
		return fmt.Errorf("press %v in tmux session %s: %w", keys, s.sanitizedName, err)
	}
	return nil
}

// promptEnterDelay separates a prompt's text from the Enter that submits
// it, so the agent reads that Enter as a submit, not as a newline inside
// the prompt.
const promptEnterDelay = 100 * time.Millisecond

// SendPrompt types prompt into the session and submits it with Enter.
func (s *Session) SendPrompt(prompt string) error {
	if err := s.TypeText(prompt); err != nil {
		return err
	}
	time.Sleep(promptEnterDelay)
	return s.PressKeys("Enter")
}
```

- [ ] **Step 4: Run the unit tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session/tmux -run TestSession_ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Write the real-tmux parity test**

`session/tmux/sendkeys_realtmux_test.go`:
```go
package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendKeysMatchesPTYWrite_RealTmux pins what lets session lifecycle
// type into a session with no attach client. For the same text,
// `send-keys -l` (Session.TypeText/PressKeys) delivers exactly the bytes to
// the agent's terminal that a write to an attach client's PTY delivers
// (TmuxSession.SendKeys/TapEnter), including leading dashes, quotes, tabs,
// newlines and multi-byte UTF-8. Probed on tmux 3.7b on 2026-10-03; CI
// runs 3.6a.
func TestSendKeysMatchesPTYWrite_RealTmux(t *testing.T) {
	privateTmux(t, "sk")
	text := "-leading dash -- \"quotes\" $HOME \\back\ttab ünïcødé 🙂\nsecond line"
	want := text + "\rD\r"
	dir := t.TempDir()

	// recorder starts a session with a raw terminal whose program writes
	// every byte it receives to a file, and returns that file.
	recorder := func(title string) string {
		out := filepath.Join(dir, title+".bin")
		newRawSession(t, ToLoomTmuxName(title), "stty raw -echo; exec cat > "+out)
		waitForPaneCommand(t, ToLoomTmuxName(title), "cat")
		return out
	}

	viaKeys := recorder("sk-keys")
	s := NewSession("sk-keys", "cat")
	require.NoError(t, s.TypeText(text))
	require.NoError(t, s.PressKeys("Enter"))
	require.NoError(t, s.PressKeys("D", "Enter"))

	viaPTY := recorder("sk-pty")
	client := NewTmuxSession("sk-pty", "cat")
	require.NoError(t, client.Restore())
	t.Cleanup(func() { _ = client.PausePreview() })
	waitForClient(t, ToLoomTmuxName("sk-pty"))
	require.NoError(t, client.SendKeys(text))
	require.NoError(t, client.TapEnter())
	require.NoError(t, client.SendKeys("D"))
	require.NoError(t, client.TapEnter())

	assert.Equal(t, want, waitForBytes(t, viaKeys, len(want)), "send-keys")
	assert.Equal(t, want, waitForBytes(t, viaPTY, len(want)), "PTY write")
}

// waitForPaneCommand waits until the session's active pane runs command,
// so input is not typed into the shell before it execs.
func waitForPaneCommand(t *testing.T, name, command string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := Command(context.Background(), "display-message", "-p", "-t", PaneTarget(name), "#{pane_current_command}").Output()
		if strings.TrimSpace(string(out)) == command {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session %s never ran %s", name, command)
}

// waitForClient waits until an attach client is attached to the session.
// Bytes written to a client's PTY before it has put its terminal in raw
// mode would be cooked by the line discipline, which reads CR as NL.
func waitForClient(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := Command(context.Background(), "list-clients", "-t", SessionTarget(name), "-F", "#{client_tty}").Output()
		if strings.TrimSpace(string(out)) != "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no client ever attached to %s", name)
}

// waitForBytes waits until path holds at least n bytes, then returns them.
func waitForBytes(t *testing.T, path string, n int) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		if len(b) >= n || time.Now().After(deadline) {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```

- [ ] **Step 6: Run the parity test**

Run: `CGO_ENABLED=0 go test ./session/tmux -run TestSendKeysMatchesPTYWrite_RealTmux -v`
Expected: PASS, or SKIP where tmux is not installed. **If it fails on a byte mismatch, stop.** Decision 2 is falsified, and the plan goes back to the user.

- [ ] **Step 7: Format and vet**

```bash
gofmt -w session/tmux/session.go session/tmux/session_test.go session/tmux/sendkeys_realtmux_test.go
go vet ./session/tmux/
CGO_ENABLED=0 go test ./session/tmux/
```

### A2. `TmuxSession` embeds `Session`; prompts and trust answers go through `send-keys`

**Files:**
- Modify: `session/tmux/session.go`, `session/tmux/tmux.go`
- Modify: `session/agent_pane.go`
- Modify: `session/tmux/prompt_match_test.go`, `session/tmux/tmux_race_test.go`
- Test: `session/tmux/session_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `session/tmux/session_test.go`:
```go
func TestSession_StartLaunchesWithoutAttaching(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	rec := &argvRecorder{}
	s := NewSessionWithDeps("launch", "claude", ptyFactory, rec.runner())
	workdir := t.TempDir()

	require.NoError(t, s.Start(workdir))

	require.Len(t, ptyFactory.cmds, 1, "new-session only: no attach client")
	assert.Equal(t, []string{"tmux", "new-session", "-d", "-s", "loom_launch", "-c", workdir, "claude"}, ptyFactory.cmds[0].Args)
	assert.Len(t, rec.ran("set-option"), 3, "history-limit, mouse and status, as before")
	assert.Len(t, rec.ran("bind-key"), 1)
	assert.Empty(t, rec.ran("capture-pane"), "no seed capture: that belongs to an attach client")
}

func TestSession_CloseOnlyKills(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	rec := &argvRecorder{}
	s := NewSessionWithDeps("closing", "claude", ptyFactory, rec.runner())

	require.NoError(t, s.Close())

	assert.Equal(t, [][]string{{"tmux", "kill-session", "-t", "=loom_closing"}}, rec.runs)
	assert.Empty(t, ptyFactory.cmds)
}

func TestSession_DismissTrustPrompt(t *testing.T) {
	for _, tc := range []struct {
		name, program, screen string
		found                 bool
		keys                  []string
	}{
		{"claude taps enter", "claude", "Do you trust the files in this folder?\n❯ 1. Yes, proceed", true, []string{"Enter"}},
		{"aider answers D", "aider", "Open documentation url for more info? (Y)es/(N)o/(D)on't ask again", true, []string{"D", "Enter"}},
		{"no prompt on screen", "claude", "$ ls\nREADME.md", false, nil},
		{"an agent with no patterns", "bash", "Do you trust the files in this folder?", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &argvRecorder{}
			s := NewSessionWithDeps("trust", tc.program, NewMockPtyFactory(t), rec.runner())

			assert.Equal(t, tc.found, s.DismissTrustPrompt(tc.screen))

			if tc.keys == nil {
				assert.Empty(t, rec.ran("send-keys"))
				return
			}
			want := append([]string{"tmux", "send-keys", "-t", "=loom_trust:"}, tc.keys...)
			assert.Equal(t, [][]string{want}, rec.ran("send-keys"))
		})
	}
}

func TestSession_WithProgramEnv(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{}
	old := NewSessionWithDeps("with program env", "aider", ptyFactory, cmdExec, "A=1")

	got := old.WithProgramEnv("claude --model opus", []string{"CLAUDE_CONFIG_DIR=/acct/max-2"})

	require.NotSame(t, old, got)
	require.Equal(t, old.sanitizedName, got.sanitizedName)
	require.Equal(t, "claude --model opus", got.program)
	require.Equal(t, "claude", got.adapter.Name())
	require.Equal(t, "aider", old.adapter.Name(), "the original is untouched")
	require.Equal(t, []string{"A=1"}, old.env)
	require.Equal(t, []string{"CLAUDE_CONFIG_DIR=/acct/max-2"}, got.env)
	require.Same(t, ptyFactory, got.ptyFactory.(*MockPtyFactory))
	require.Equal(t, cmdExec, got.cmdExec)
	require.Equal(t, []string{"A=1"}, old.WithProgram("claude").env, "WithProgram carries the env over")
}
```

Replace `TestTrustPromptMatchesWrappedPattern` in `session/tmux/prompt_match_test.go`. The old version built a session on the production executor, which would now run a real `send-keys`:
```go
func TestTrustPromptMatchesWrappedPattern(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("trustwrap", "claude", NewMockPtyFactory(t), rec.runner())
	// Wrap splits the trust pattern mid-word; detection must still hit.
	content := "Do you trust the files in this fol\nder?\n❯ 1. Yes, proceed"

	require.True(t, s.DismissTrustPrompt(content),
		"a wrapped trust prompt must still be detected")
	assert.Equal(t, [][]string{{"tmux", "send-keys", "-t", "=loom_trustwrap:", "Enter"}}, rec.ran("send-keys"))
}
```
Add `"github.com/stretchr/testify/assert"` to that file's imports.

- [ ] **Step 2: Run them to verify they fail**

Run: `CGO_ENABLED=0 go test ./session/tmux -run 'TestSession_|TestTrustPrompt' -v`
Expected: FAIL to compile with `s.Start undefined (type *Session has no field or method Start)`.

- [ ] **Step 3: Make `TmuxSession` embed `*Session`**

In `session/tmux/tmux.go`:

a) Replace the doc comment above `type TmuxSession struct {` (it starts `// TmuxSession is a managed tmux session bound to a single instance.`) together with the struct's head, through the `cmdExec internalexec.Executor` field and the blank line after it, with the block below. Everything from `// Initialized by Start or Restore` down stays:
```go
// TmuxSession is an attach client of one tmux session. It holds a PTY
// running `tmux attach-session`, an output pump draining it, and (on the
// emulator path) an in-process emulator mirroring its screen, plus the
// embedded Session for the session itself. The TUI renders every pane from
// one. Session lifecycle holds only a Session and never attaches. The zero
// value is not usable: construct via NewTmuxSession or NewAttachClient (or
// their WithDeps variants in tests). Two goroutines touch the ptmx and
// monitor fields: the metadata fan-out (CaptureAndProcess/HasUpdated/
// keystroke injection) and the Update loop's attach lifecycle
// (Restore/PausePreview/Close). Both are therefore guarded by stateMu (see
// its doc).
type TmuxSession struct {
	*Session

```

b) Replace `newSanitizedTmuxSession`'s body:
```go
func newSanitizedTmuxSession(sanitizedName string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *TmuxSession {
	return &TmuxSession{
		Session: newSanitizedSession(sanitizedName, program, ptyFactory, cmdExec, env...),
		// monitor is always non-nil for the session's lifetime so HasUpdated
		// and CaptureAndProcess can read it without a guard. Restore reassigns
		// a fresh instance on every PTY attach, so the initial value is only
		// load-bearing for paused sessions (constructed without Restore).
		monitor: newStatusMonitor(),
		// Default geometry until the first SetDetachedSize; a fresh emulator in
		// Restore starts here so it is never zero-sized.
		lastCols: 80,
		lastRows: 24,
	}
}
```

c) Move these declarations **verbatim** from `tmux.go` to the end of `session.go`, taking their doc comments with them. For each method, change the receiver from `(t *TmuxSession)` to `(s *Session)` and every `t.` in its body to `s.`. Change nothing else.
   - `ErrSessionExists`
   - `pendingPrompt`
   - `CloseRelatedSession`
   - The liveness group: the `Liveness` type, its constants `LivenessDead`, `LivenessAlive` and `LivenessUnknown`, `livenessProbeTimeout`, `SetLivenessProbeTimeoutForTest`, `SessionLiveness` and `DoesSessionExist`. The comment block "DoesSessionExist reports whether the backing tmux session is still alive…" sits above `type Liveness`; move it along with the type.
   - `Env`
   - `CapturePaneContent`, `CaptureHistory` and `captureHistoryRowsOnly`
   - `IsAlternateScreen`
   - `FullScreenAttachCmd`

   Delete `TmuxSession.SessionName`, since `Session` already has one. Add `"errors"`, `"os/exec"`, `"slices"`, `"strings"` and `"github.com/aidan-bailey/loom/log"` to `session.go`'s imports as the moved code needs them, and drop any `tmux.go` import that no longer has a use. `go build` reports both.

d) Replace `TmuxSession.Start` with the two methods below. `Session.Start` is the old body without the final `Restore` block:
```go
// Start creates the tmux session, running program in workDir, and sets
// the options every client attached to it relies on: scroll-back history,
// mouse, no status line, and C-q to detach a full-screen attach. It
// attaches nothing; TmuxSession.Start does that for a caller that renders
// the session itself. If a session of the same name is already alive, Start
// refuses with ErrSessionExists before launching anything. Any other error
// may come after the program was launched, so the caller must not assume
// it is not running in workDir (see SessionLiveness).
func (s *Session) Start(workDir string) (err error) {
	t0 := time.Now()
	log.For("tmux").Debug("start.begin", "session", s.sanitizedName, "program", s.program, "workdir", workDir)
	defer func() {
		args := []any{"session", s.sanitizedName, "duration_ms", time.Since(t0).Milliseconds()}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		log.For("tmux").Debug("start.end", args...)
	}()

	// Check if the session already exists
	if s.DoesSessionExist() {
		return fmt.Errorf("%w: %s", ErrSessionExists, s.sanitizedName)
	}

	// Create a new detached tmux session and start the program in it.
	// tmuxStartTimeout allows the agent process's initial exec before tmux
	// returns control; tmux itself is quick, but the wrapped program may not be.
	startCtx, startCancel := context.WithTimeout(context.Background(), tmuxStartTimeout)
	defer startCancel()
	args := []string{"new-session", "-d", "-s", s.sanitizedName, "-c", workDir}
	for _, e := range s.env {
		args = append(args, "-e", e)
	}
	args = append(args, s.program)
	cmd := Command(startCtx, args...)

	ptmx, err := s.ptyFactory.Start(cmd)
	if err != nil {
		// Cleanup any partially created session if any exists.
		if s.DoesSessionExist() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), tmuxTimeout)
			cleanupCmd := Command(cleanupCtx, "kill-session", "-t", SessionTarget(s.sanitizedName))
			if cleanupErr := s.cmdExec.Run(cleanupCmd); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			cleanupCancel()
		}
		return fmt.Errorf("error starting tmux session: %w", err)
	}

	// The new-session ptmx only exists to launch the command; close it on
	// every exit path.
	defer ptmx.Close()

	// Poll for session existence with exponential backoff
	timeout := time.After(2 * time.Second)
	sleepDuration := 5 * time.Millisecond
	for !s.DoesSessionExist() {
		select {
		case <-timeout:
			if cleanupErr := s.Close(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			return fmt.Errorf("timed out waiting for tmux session %s: %v", s.sanitizedName, err)
		default:
			time.Sleep(sleepDuration)
			// Exponential backoff up to 50ms max
			if sleepDuration < 50*time.Millisecond {
				sleepDuration *= 2
			}
		}
	}

	// Set history limit to enable scrollback (default is 2000, we'll use 10000 for more history)
	histCtx, histCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	historyCmd := Command(histCtx, "set-option", "-t", PaneTarget(s.sanitizedName), "history-limit", "10000")
	if err := s.cmdExec.Run(historyCmd); err != nil {
		log.For("tmux").Warn("history_limit_failed", "session", s.sanitizedName, "err", err)
	}
	histCancel()

	// Enable mouse scrolling for the session
	mouseCtx, mouseCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	mouseCmd := Command(mouseCtx, "set-option", "-t", PaneTarget(s.sanitizedName), "mouse", "on")
	if err := s.cmdExec.Run(mouseCmd); err != nil {
		log.For("tmux").Warn("mouse_scroll_failed", "session", s.sanitizedName, "err", err)
	}
	mouseCancel()

	// Disable the tmux status bar. The detached attach stream the emulator
	// consumes includes the status line, but the pane preview must not — it
	// would consume a render row and shift content. tmux still owns the
	// session; only its chrome is hidden.
	statusCtx, statusCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	statusCmd := Command(statusCtx, "set-option", "-t", PaneTarget(s.sanitizedName), "status", "off")
	if err := s.cmdExec.Run(statusCmd); err != nil {
		log.For("tmux").Warn("status_off_failed", "session", s.sanitizedName, "err", err)
	}
	statusCancel()

	// Rebind Ctrl-Q to detach-client for full-screen attach. The default tmux
	// prefix is Ctrl-B + d; our users expect Ctrl-Q because inline attach has
	// always used it. This binding is server-wide, but claude-squad has always
	// assumed ownership of Ctrl-Q as its detach key.
	bindCtx, bindCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	bindCmd := Command(bindCtx, "bind-key", "-n", "C-q", "detach-client")
	if err := s.cmdExec.Run(bindCmd); err != nil {
		log.For("tmux").Warn("bind_cq_failed", "err", err)
	}
	bindCancel()

	return nil
}
```
In `tmux.go`, in place of the old `Start`:
```go
// Start launches the session (Session.Start) and attaches this client to
// it. The terminal pane starts its shells this way. Agent sessions are
// different: session.Instance launches one through its own Session, and
// the TUI then attaches a client to it by name (NewAttachClient).
func (t *TmuxSession) Start(workDir string) error {
	if err := t.Session.Start(workDir); err != nil {
		return err
	}
	if err := t.Restore(); err != nil {
		if cleanupErr := t.Close(); cleanupErr != nil {
			err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
		}
		return fmt.Errorf("error restoring tmux session: %w", err)
	}
	return nil
}
```

e) Split `Close`. Add to `session.go`:
```go
// Close kills the tmux session by exact name (see SessionTarget). Close
// runs on sessions that may already be dead, so with a prefix match,
// closing "api" after its agent exited would kill a live "api-v2".
func (s *Session) Close() error {
	log.For("tmux").Debug("close", "session", s.sanitizedName)
	killCtx, killCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer killCancel()
	cmd := Command(killCtx, "kill-session", "-t", SessionTarget(s.sanitizedName))
	if err := s.cmdExec.Run(cmd); err != nil {
		return fmt.Errorf("error killing tmux session: %w", err)
	}
	return nil
}
```
Replace `TmuxSession.Close` in `tmux.go`:
```go
// Close detaches this client (PTY, output pump, emulator) and then kills
// the session (Session.Close).
func (t *TmuxSession) Close() error {
	var errs []error

	t.stateMu.Lock()
	ptmx := t.ptmx
	t.ptmx = nil
	emu := t.emu
	t.emu = nil
	t.stateMu.Unlock()
	if ptmx != nil {
		t.signalPumpStop(ptmx)
		if err := ptmx.Close(); err != nil {
			errs = append(errs, fmt.Errorf("error closing PTY: %w", err))
		}
	}

	// Wait for pump goroutine to exit after PTY close.
	t.waitPumpExit()
	// Close the emulator after the pump has exited (no write-after-close).
	if emu != nil {
		_ = emu.Close()
	}

	if err := t.Session.Close(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	errMsg := "multiple errors occurred during cleanup:"
	for _, err := range errs {
		errMsg += "\n  - " + err.Error()
	}
	return errors.New(errMsg)
}
```

f) Replace `handleTrustPrompt` (in `tmux.go`) with `Session.DismissTrustPrompt` in `session.go`, and delete `TmuxSession.TapDAndEnter`:
```go
// DismissTrustPrompt scans content (the pane's screen) for the adapter's
// trust-prompt patterns. On a hit, it answers with the adapter's declared
// keys through send-keys. It reports whether a prompt was found; a failed
// answer is logged rather than returned.
func (s *Session) DismissTrustPrompt(content string) bool {
	normalized := normalizeForPatternMatch(content)
	for _, pattern := range s.adapter.TrustPromptPatterns() {
		if !strings.Contains(normalized, normalizeForPatternMatch(pattern)) {
			continue
		}
		var keys []string
		switch s.adapter.TrustPromptResponse() {
		case agent.TrustPromptTapEnter:
			keys = []string{"Enter"}
		case agent.TrustPromptTapDAndEnter:
			keys = []string{"D", "Enter"}
		default:
			return false
		}
		if err := s.PressKeys(keys...); err != nil {
			log.For("tmux").Error("trust_prompt.dismiss_failed", "agent", s.adapter.Name(), "err", err)
		}
		return true
	}
	return false
}
```
In `TmuxSession.CaptureAndProcess`, change `trustHandled = t.handleTrustPrompt(content)` to `trustHandled = t.DismissTrustPrompt(content)`. In the comment on `Session`'s `adapter` field, mention that `CaptureAndProcess` uses it via `DismissTrustPrompt` and `HasUpdated`.

g) Add to `session.go`:
```go
// WithProgram is WithProgramEnv with s's own env carried over.
func (s *Session) WithProgram(program string) *Session {
	return s.WithProgramEnv(program, slices.Clone(s.env))
}

// WithProgramEnv returns a new, unstarted Session for the same tmux
// session that runs program with env in place of s's program and env. It
// keeps s's PTY factory and executor and resolves its adapter from
// program, as NewSession does. s itself is unchanged: the caller Closes s
// and Starts the result. Instance.Restart uses it to relaunch a dead
// session with a freshly composed command and a freshly resolved env,
// since the account's CLAUDE_CONFIG_DIR in particular may have changed
// since s was built.
func (s *Session) WithProgramEnv(program string, env []string) *Session {
	return newSanitizedSession(s.sanitizedName, program, s.ptyFactory, s.cmdExec, env...)
}
```
Leave `TmuxSession.WithProgram` and `TmuxSession.WithProgramEnv` in `tmux.go` unchanged. They override the embedded versions, and `session.Instance` still calls them until Package C deletes them.

- [ ] **Step 4: Route `AgentPane` input through `send-keys`**

In `session/agent_pane.go`, replace `SendKeys`, `SendPrompt` and `TapEnter`:
```go
// SendKeys types keys into the agent's tmux session through send-keys,
// which needs no attach client.
func (p AgentPane) SendKeys(keys string) error {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return fmt.Errorf("cannot send keys to instance that has not been started or is paused")
	}
	return i.getTmuxSession().TypeText(keys)
}

// SendPrompt types prompt into the agent's tmux session and submits it,
// through send-keys.
func (p AgentPane) SendPrompt(prompt string) error {
	i := p.i
	if !i.isStarted() {
		return fmt.Errorf("instance not started")
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return fmt.Errorf("tmux session not initialized")
	}
	return ts.SendPrompt(prompt)
}

// TapEnter presses Enter in the tmux session when the instance is
// running, and does nothing otherwise. Exposed to Lua scripts as
// inst:tap_enter().
func (p AgentPane) TapEnter() {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return
	}
	if err := ts.PressKeys("Enter"); err != nil {
		log.For("session").Error("tap_enter_failed", "err", err)
	}
}
```
Remove the now-unused `"time"` import.

In `session/tmux/tmux_race_test.go`, update the two comments that say the trust prompt goes through `TapEnter`. Change "which read t.ptmx via the trust-prompt TapEnter and mutate t.monitor" to "answer the trust prompt through send-keys and mutate t.monitor", and change "TapEnter branch, which reads t.ptmx" to "the trust-prompt branch". The test itself is unchanged.

- [ ] **Step 5: Run the tests**

Run: `CGO_ENABLED=0 go test ./session/... && CGO_ENABLED=0 go test ./app/... ./ui/... ./script/...`
Expected: PASS. `TestStartTmuxSession` still sees `new-session` then `attach-session`, because `TmuxSession.Start` still attaches.

- [ ] **Step 6: Race**

Run: `CC=clang CGO_ENABLED=1 go test -race ./session/tmux/`
Expected: PASS.

- [ ] **Step 7: Format and vet**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
go vet ./...
```

### A3. Attach clients by session name, and their status scan

**Files:**
- Create: `session/tmux/attach.go`
- Test: `session/tmux/attach_test.go`

- [ ] **Step 1: Write the failing tests**

`session/tmux/attach_test.go`:
```go
package tmux

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// screenRunner is rec's executor with capture-pane answering screen. That
// is the snapshot path's view of the pane, used when there is no emulator.
func screenRunner(rec *argvRecorder, screen string) cmd_test.MockCmdExec {
	e := rec.runner()
	e.OutputFunc = func(c *exec.Cmd) ([]byte, error) {
		rec.mu.Lock()
		rec.runs = append(rec.runs, slices.Clone(c.Args))
		rec.mu.Unlock()
		return []byte(screen), nil
	}
	return e
}

func TestNewAttachClient_TakesTheSessionNameVerbatim(t *testing.T) {
	c := NewAttachClientWithDeps("loom_api", "claude", NewMockPtyFactory(t), cmd_test.MockCmdExec{})
	assert.Equal(t, "loom_api", c.SessionName(), "already a session name: never re-prefixed")
}

func TestNewAttachClient_RestoreAttachesByExactName(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	rec := &argvRecorder{}
	c := NewAttachClientWithDeps("loom_api", "claude", ptyFactory, rec.runner())

	require.NoError(t, c.Restore())
	t.Cleanup(func() { _ = c.PausePreview() })

	require.Len(t, ptyFactory.cmds, 1)
	assert.Equal(t, []string{"tmux", "attach-session", "-t", "=loom_api"}, ptyFactory.cmds[0].Args)
	assert.Empty(t, rec.ran("new-session"), "a client launches nothing")
	assert.True(t, c.PtmxAlive())
}

func TestDetectStatus_DefaultAdapterOnlyChecksForChange(t *testing.T) {
	rec := &argvRecorder{}
	c := NewAttachClientWithDeps("loom_sh", "bash", NewMockPtyFactory(t), screenRunner(rec, "Do you trust the files in this folder?"))

	updated, hasPrompt, err := c.DetectStatus()
	require.NoError(t, err)
	assert.True(t, updated, "the first scan sees new content")
	assert.False(t, hasPrompt)

	updated, _, err = c.DetectStatus()
	require.NoError(t, err)
	assert.False(t, updated, "unchanged content")
	assert.Empty(t, rec.ran("send-keys"), "an agent without patterns gets no trust answer")
}

func TestDetectStatus_AnswersTheTrustPrompt(t *testing.T) {
	rec := &argvRecorder{}
	c := NewAttachClientWithDeps("loom_cl", "claude", NewMockPtyFactory(t), screenRunner(rec, "Do you trust the files in this folder?\n❯ 1. Yes, proceed"))

	updated, _, err := c.DetectStatus()

	require.NoError(t, err)
	assert.True(t, updated)
	assert.Equal(t, [][]string{{"tmux", "send-keys", "-t", "=loom_cl:", "Enter"}}, rec.ran("send-keys"))
}

func TestDetectStatus_ReportsThePendingPrompt(t *testing.T) {
	rec := &argvRecorder{}
	c := NewAttachClientWithDeps("loom_cl", "claude", NewMockPtyFactory(t), screenRunner(rec, "  3. No, and tell Claude what to do differently"))

	_, hasPrompt, err := c.DetectStatus()

	require.NoError(t, err)
	assert.True(t, hasPrompt)
}

func TestDetectStatus_SurfacesACaptureFailure(t *testing.T) {
	rec := &argvRecorder{}
	e := rec.runner()
	e.OutputFunc = func(*exec.Cmd) ([]byte, error) { return nil, errors.New("no server running") }
	c := NewAttachClientWithDeps("loom_cl", "claude", NewMockPtyFactory(t), e)

	_, _, err := c.DetectStatus()

	require.Error(t, err)
}

// TestAttachClient_RendersASessionItDidNotStart_RealTmux: the TUI's client
// attaches by name to a session that lifecycle launched through a Session.
// No object is shared between the two.
func TestAttachClient_RendersASessionItDidNotStart_RealTmux(t *testing.T) {
	privateTmux(t, "ac")
	if !EmulatorEnabled() {
		t.Skip("emulator disabled (LOOM_PANE_RENDERER=snapshot)")
	}
	s := NewSession("ac", "sh -c 'echo attached-by-name; exec sleep 60'")
	require.NoError(t, s.Start(t.TempDir()))
	t.Cleanup(func() { _ = s.Close() })

	c := NewAttachClient(s.SessionName(), "sh")
	require.NoError(t, c.Restore())
	t.Cleanup(func() { _ = c.PausePreview() })

	deadline := time.Now().Add(5 * time.Second)
	for {
		if screen, ok := c.RenderEmulator(); ok && strings.Contains(screen, "attached-by-name") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the client never rendered the session's output")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `CGO_ENABLED=0 go test ./session/tmux -run 'TestNewAttachClient|TestDetectStatus|TestAttachClient' -v`
Expected: FAIL to compile with `undefined: NewAttachClientWithDeps`.

- [ ] **Step 3: Write `session/tmux/attach.go`**

```go
package tmux

import internalexec "github.com/aidan-bailey/loom/internal/exec"

// NewAttachClient returns an unattached client for the existing session
// named sessionName, which the TUI then Restores. sessionName is already a
// tmux session name, as Session.SessionName returns, not a title. program
// only selects the agent adapter for the client's status scan
// (DetectStatus); a client launches nothing. Since session lifecycle never
// attaches, this is how the TUI gets into a session lifecycle launched.
func NewAttachClient(sessionName, program string) *TmuxSession {
	return newSanitizedTmuxSession(sessionName, program, MakePtyFactory(), internalexec.Default{})
}

// NewAttachClientWithDeps is NewAttachClient with injected dependencies,
// for tests.
func NewAttachClientWithDeps(sessionName, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor) *TmuxSession {
	return newSanitizedTmuxSession(sessionName, program, ptyFactory, cmdExec)
}

// DetectStatus scans the pane's current screen once for the status ladder.
// It reports whether the screen changed since the last scan and whether it
// shows the agent's pending-input prompt, and it answers the agent's trust
// prompt through send-keys. An agent with no adapter patterns gets only
// the change check. err reports a failed capture on the snapshot path; it
// never means "no change".
func (t *TmuxSession) DetectStatus() (updated, hasPrompt bool, err error) {
	if t.adapter.Name() == "default" {
		updated, hasPrompt = t.HasUpdated()
		return updated, hasPrompt, nil
	}
	_, updated, hasPrompt, _, err = t.CaptureAndProcess()
	return updated, hasPrompt, err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session/tmux -run 'TestNewAttachClient|TestDetectStatus|TestAttachClient' -v`
Expected: PASS. The real-tmux test SKIPs without tmux.

- [ ] **Step 5: Package A checkpoint and commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') session/tmux/attach.go session/tmux/attach_test.go
go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./session/...
git add -A session/
git commit -m "refactor(tmux): split a lifecycle-only Session from the attach client

TmuxSession embeds a Session that launches, probes, types into and kills
a tmux session with tmux commands. Prompts, Lua send_keys/tap_enter and
trust-prompt answers go through send-keys (byte-identical to a PTY
write), and NewAttachClient attaches by name to a session it did not
start.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
Expected: every package PASSes before the commit.

---

## Package B: The TUI reads every agent pane through `ui.PaneClients`

A registry of attach clients keyed by tmux session name (`ui.PaneClients`), and a `ui.Pane` per instance that every display read, input forward and status scrape goes through, in `ui` and `app` alike. `PaneClients.For` falls back to the instance's own client while nothing is registered, so behaviour is unchanged until Package C. One commit at the end.

### B1. `ui.PaneClients` and `ui.Pane`

**Files:**
- Create: `ui/panes.go`
- Test: `ui/panes_test.go`

- [ ] **Step 1: Write the failing tests**

`ui/panes_test.go`:
```go
package ui

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// devNullPty is a tmux.PtyFactory whose PTYs are /dev/null: attaching runs
// no tmux client, and the output pump hits EOF at once. starts records
// each command it was asked to start.
type devNullPty struct {
	mu     *sync.Mutex
	starts *[]string
}

func (f devNullPty) Start(c *exec.Cmd) (*os.File, error) {
	f.mu.Lock()
	*f.starts = append(*f.starts, strings.Join(c.Args, " "))
	f.mu.Unlock()
	return os.OpenFile(os.DevNull, os.O_RDWR, 0)
}

func (devNullPty) Close() {}

// aliveRunner answers every tmux command with success and empty output.
func aliveRunner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
}

// newTestPaneClients returns a registry whose clients attach through
// devNullPty, along with a func reporting the recorded PTY starts.
func newTestPaneClients(t *testing.T) (*PaneClients, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var starts []string
	p := NewPaneClients()
	p.SetClientFactoryForTest(func(name, program string) *tmux.TmuxSession {
		return tmux.NewAttachClientWithDeps(name, program, devNullPty{mu: &mu, starts: &starts}, aliveRunner())
	})
	return p, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), starts...)
	}
}

// runningInstance is a started, Running instance (no tmux contacted).
func runningInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst
}

func TestPaneClients_NilRegistryHasNoClients(t *testing.T) {
	var p *PaneClients
	assert.Nil(t, p.Get("loom_a"))
	assert.False(t, p.Alive("loom_a"))
	assert.NoError(t, p.Ensure("loom_a", "claude"))
	assert.Nil(t, p.Retain(nil))
	p.SetDefaultSize(80, 24)
}

func TestPaneClients_EnsureAttachesOnce(t *testing.T) {
	p, starts := newTestPaneClients(t)
	p.SetDefaultSize(100, 30)

	require.NoError(t, p.Ensure("loom_a", "claude"))
	c := p.Get("loom_a")
	require.NotNil(t, c)
	t.Cleanup(func() { _ = c.PausePreview() })
	assert.True(t, c.PtmxAlive())

	require.NoError(t, p.Ensure("loom_a", "claude"))
	assert.Same(t, c, p.Get("loom_a"))
	// ui's TestMain runs tmux on a private socket, so argv may begin
	// "tmux -L <sock>"; the attach itself is the suffix.
	require.Len(t, starts(), 1, "one attach")
	assert.True(t, strings.HasSuffix(starts()[0], "attach-session -t =loom_a"), "by exact name: %q", starts()[0])
}

func TestPaneClients_EnsureReattachesAPausedClient(t *testing.T) {
	p, starts := newTestPaneClients(t)
	require.NoError(t, p.Ensure("loom_a", "claude"))
	c := p.Get("loom_a")
	require.NoError(t, c.PausePreview()) // as a full-screen attach does

	require.NoError(t, p.Ensure("loom_a", "claude"))
	t.Cleanup(func() { _ = c.PausePreview() })

	assert.Same(t, c, p.Get("loom_a"), "the same client, re-attached")
	assert.True(t, c.PtmxAlive())
	assert.Len(t, starts(), 2)
}

func TestPaneClients_ReplaceHandsBackTheOldClient(t *testing.T) {
	p, _ := newTestPaneClients(t)
	require.NoError(t, p.Ensure("loom_a", "claude"))
	old := p.Get("loom_a")

	got, err := p.Replace("loom_a", "claude")
	require.NoError(t, err)
	fresh := p.Get("loom_a")
	t.Cleanup(func() { _ = old.PausePreview(); _ = fresh.PausePreview() })

	assert.Same(t, old, got)
	assert.NotSame(t, old, fresh)
	assert.True(t, fresh.PtmxAlive())
	assert.True(t, old.PtmxAlive(), "closing the old one is the caller's, off the Update goroutine")
}

func TestPaneClients_RetainDropsTheUnwanted(t *testing.T) {
	p, _ := newTestPaneClients(t)
	require.NoError(t, p.Ensure("loom_a", "claude"))
	require.NoError(t, p.Ensure("loom_b", "claude"))
	b := p.Get("loom_b")
	t.Cleanup(func() { _ = p.Get("loom_a").PausePreview(); _ = b.PausePreview() })

	dropped := p.Retain(map[string]bool{"loom_a": true})

	assert.Equal(t, []*tmux.TmuxSession{b}, dropped)
	assert.Nil(t, p.Get("loom_b"))
	assert.NotNil(t, p.Get("loom_a"))
	assert.True(t, b.PtmxAlive(), "closing it is the caller's")
}

func TestPaneClients_ForGuardsTheInstance(t *testing.T) {
	p, _ := newTestPaneClients(t)
	assert.Nil(t, p.For(nil).Client())

	unstarted, err := session.NewInstance(session.InstanceOptions{Title: "new", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	assert.Nil(t, p.For(unstarted).Client(), "not started: no pane")

	paused, err := session.FromInstanceData(session.InstanceData{Title: "paused", Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true}, t.TempDir())
	require.NoError(t, err)
	assert.Nil(t, p.For(paused).Client(), "paused: no pane")

	inst := runningInstance(t, "live")
	name := inst.Pane().TmuxSessionName()
	// Stage 1A transition (until Package C): with nothing registered, the
	// instance's own client is the pane.
	assert.Same(t, inst.TmuxSession(), p.For(inst).Client())

	require.NoError(t, p.Ensure(name, "claude"))
	t.Cleanup(func() { _ = p.Get(name).PausePreview() })
	assert.Same(t, p.Get(name), p.For(inst).Client(), "the registered client wins")
}

func TestPane_ZeroValueIsInert(t *testing.T) {
	var pane Pane
	s, err := pane.Preview()
	assert.NoError(t, err)
	assert.Empty(t, s)
	_, ok := pane.EmulatorScreen()
	assert.False(t, ok)
	_, ok = pane.CaptureHistory()
	assert.False(t, ok)
	assert.False(t, pane.IsAlternateScreen())
	_, ok = pane.CursorState()
	assert.False(t, ok)
	_, ok = pane.PaneTitle()
	assert.False(t, ok)
	assert.False(t, pane.HasEmulator())
	assert.False(t, pane.PtmxAlive())
	assert.NoError(t, pane.SetPreviewSize(80, 24))
	assert.Error(t, pane.SendKeysRaw([]byte("x")), "dropping keys silently would hide a dead inline attach")
	assert.NoError(t, pane.Paste("x"))
	assert.NoError(t, pane.ForwardWheel(true, 1))
	assert.NoError(t, pane.ForwardMouse(0, 1, 1, true))
	pane.ForwardFocus(true)
	assert.Nil(t, pane.GetContentHash())
	updated, hasPrompt, err := pane.DetectStatus()
	assert.False(t, updated)
	assert.False(t, hasPrompt)
	assert.NoError(t, err)
	_, ok = pane.scrollSource()
	assert.False(t, ok)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `CGO_ENABLED=0 go test ./ui -run 'TestPaneClients|TestPane_' -v`
Expected: FAIL to compile with `undefined: NewPaneClients`.

- [ ] **Step 3: Write `ui/panes.go`**

```go
package ui

import (
	"errors"
	"fmt"
	"sync"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/session/vt"
)

// PaneClients holds the TUI's attach clients: one *tmux.TmuxSession per
// live agent tmux session, keyed by the session's name. Each client is
// attached by name rather than owned by the session's instance. Every
// pane render, scroll, cursor, mouse, paste and key forward goes through
// a client here (Pane), and so does the status scrape that reads a pane's
// screen. That is why session lifecycle never needs a client of its own.
//
// Safe for concurrent use: Get and For may be called from Cmd goroutines.
// Ensure and Replace attach (a PTY spawn and a capture-pane), and they run
// on the Update goroutine as the attach paths before them did. A client
// that Replace or Retain hands back must be closed off the Update
// goroutine: PausePreview waits for the client's output pump, which blocks
// in tea.Program.Send until Update returns. Every method is nil-receiver
// safe; a nil registry holds no clients.
type PaneClients struct {
	mu      sync.Mutex
	clients map[string]*tmux.TmuxSession
	// cols, rows is the agent pane size new clients attach at
	// (SetDefaultSize); 0 before the first layout.
	cols, rows int
	// newClient builds an unattached client for a session name.
	newClient func(sessionName, program string) *tmux.TmuxSession
}

// NewPaneClients returns an empty registry whose clients attach through
// the production PTY factory and executor.
func NewPaneClients() *PaneClients {
	return &PaneClients{
		clients:   make(map[string]*tmux.TmuxSession),
		newClient: tmux.NewAttachClient,
	}
}

// SetClientFactoryForTest replaces how new clients are built, so that a
// test's Ensure/Replace attach through a fake PTY. Test-only: the name and
// doc comment are guardrails; nothing about the method enforces test-only
// use.
func (p *PaneClients) SetClientFactoryForTest(f func(sessionName, program string) *tmux.TmuxSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.newClient = f
}

// InjectForTest registers c as sessionName's client, as Ensure would have.
// Test-only, like SetClientFactoryForTest.
func (p *PaneClients) InjectForTest(sessionName string, c *tmux.TmuxSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[sessionName] = c
}

// Get returns sessionName's client, or nil.
func (p *PaneClients) Get(sessionName string) *tmux.TmuxSession {
	if p == nil || sessionName == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clients[sessionName]
}

// Alive reports whether sessionName has a client with an open PTY.
func (p *PaneClients) Alive(sessionName string) bool {
	c := p.Get(sessionName)
	return c != nil && c.PtmxAlive()
}

// For returns inst's pane: its session's client here when inst is started
// and not paused, else the zero Pane. Until the instance stops attaching
// its own client (daemon stage 1A, Package C), a session with nothing
// registered falls back to that client, so every pane renders exactly as
// before.
func (p *PaneClients) For(inst *session.Instance) Pane {
	if inst == nil || !inst.Started() || inst.Paused() {
		return Pane{}
	}
	if c := p.Get(inst.Pane().TmuxSessionName()); c != nil {
		return Pane{c: c}
	}
	return Pane{c: inst.TmuxSession()}
}

// SetDefaultSize records the agent pane size that clients attach at.
func (p *PaneClients) SetDefaultSize(cols, rows int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cols, p.rows = cols, rows
}

// Ensure gives sessionName a client attached at the default size. If the
// session has no client, Ensure builds and attaches one. If its client's
// PTY is gone (a failed attach, or one paused for a full-screen attach),
// Ensure re-attaches it. A client with an open PTY is left alone. program
// selects a new client's agent adapter for its status scan. The client
// stays registered even when attaching fails, so a later Ensure retries
// it. Update goroutine only.
func (p *PaneClients) Ensure(sessionName, program string) error {
	if p == nil || sessionName == "" {
		return nil
	}
	p.mu.Lock()
	c := p.clients[sessionName]
	if c == nil {
		c = p.newClient(sessionName, program)
		p.clients[sessionName] = c
	}
	cols, rows := p.cols, p.rows
	p.mu.Unlock()
	if c.PtmxAlive() {
		return nil
	}
	sized := cols > 0 && rows > 0
	if sized {
		// No PTY yet, so this only records the geometry Restore builds the
		// emulator at. Its "PTY is not available" error is expected.
		_ = c.SetDetachedSize(cols, rows)
	}
	if err := c.Restore(); err != nil {
		return fmt.Errorf("attach to tmux session %s: %w", sessionName, err)
	}
	if sized {
		if err := c.SetDetachedSize(cols, rows); err != nil {
			log.For("ui").Debug("pane.resize_failed", "session", sessionName, "err", err.Error())
		}
	}
	return nil
}

// Replace gives sessionName a fresh client. It is for a session just
// (re)launched under that name, whose previous client was watching the
// session it replaced. The old client, if any, is returned for the caller
// to close off the Update goroutine. Update goroutine only.
func (p *PaneClients) Replace(sessionName, program string) (*tmux.TmuxSession, error) {
	if p == nil || sessionName == "" {
		return nil, nil
	}
	p.mu.Lock()
	old := p.clients[sessionName]
	delete(p.clients, sessionName)
	p.mu.Unlock()
	return old, p.Ensure(sessionName, program)
}

// Retain drops every client whose session name is not in keep and returns
// them for the caller to close off the Update goroutine.
func (p *PaneClients) Retain(keep map[string]bool) []*tmux.TmuxSession {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var dropped []*tmux.TmuxSession
	for name, c := range p.clients {
		if !keep[name] {
			dropped = append(dropped, c)
			delete(p.clients, name)
		}
	}
	return dropped
}

// Pane is one agent pane's display surface. It covers its attach client's
// screen, scroll-back and cursor, input forwarded through the client's
// PTY, and the status scrape that reads the screen. The zero value has no
// client: the instance is not started, is paused, or has none attached
// yet. It answers every read with a zero value and every forward with nil,
// just as the instance's own guards did for an instance with no live pane.
type Pane struct{ c *tmux.TmuxSession }

// errNoClient is what SendKeysRaw returns for a pane with no client, so
// inline attach logs a dropped key rather than losing it silently.
var errNoClient = errors.New("pane has no attach client")

// Client returns the pane's attach client, or nil.
func (p Pane) Client() *tmux.TmuxSession { return p.c }

// Preview returns the pane's visible screen: the emulator's, or
// capture-pane's on the snapshot path. It is empty, with no error, when
// there is no live session.
func (p Pane) Preview() (string, error) {
	if p.c == nil || !p.c.DoesSessionExist() {
		return "", nil
	}
	if s, ok := p.c.RenderEmulator(); ok {
		return s, nil
	}
	return p.c.CapturePaneContent()
}

// EmulatorScreen returns the emulator's visible screen without running a
// subprocess, which makes it cheap enough to call for every visible card
// each frame. ok is false without an emulator (snapshot path) or a client.
func (p Pane) EmulatorScreen() (string, bool) {
	if p.c == nil {
		return "", false
	}
	return p.c.RenderEmulator()
}

// CaptureHistory returns the pane's tmux buffer (scroll-back plus screen)
// for windowing on the snapshot path, or ("", false).
func (p Pane) CaptureHistory() (string, bool) {
	if p.c == nil || !p.c.DoesSessionExist() {
		return "", false
	}
	return p.c.CaptureHistory()
}

// IsAlternateScreen reports whether the agent is a full-screen TUI on the
// alternate screen. Such an agent's scroll-back must be forwarded as wheel
// events.
func (p Pane) IsAlternateScreen() bool {
	if p.c == nil || !p.c.DoesSessionExist() {
		return false
	}
	return p.c.IsAlternateScreen()
}

// CursorState returns the pane's live cursor, or ok=false.
func (p Pane) CursorState() (vt.Cursor, bool) {
	if p.c == nil {
		return vt.Cursor{}, false
	}
	return p.c.CursorState()
}

// PaneTitle returns the window title the agent set via OSC, or ok=false.
func (p Pane) PaneTitle() (string, bool) {
	if p.c == nil {
		return "", false
	}
	return p.c.PaneTitle()
}

// HasEmulator reports whether the pane renders through the in-process
// emulator, the event-driven path.
func (p Pane) HasEmulator() bool { return p.c != nil && p.c.HasEmulator() }

// PtmxAlive reports whether the pane's client has an open PTY.
func (p Pane) PtmxAlive() bool { return p.c != nil && p.c.PtmxAlive() }

// SetPreviewSize resizes the pane's client, and with it the session's
// window. Without a client it does nothing; Ensure attaches at the default
// size.
func (p Pane) SetPreviewSize(width, height int) error {
	if p.c == nil {
		return nil
	}
	return p.c.SetDetachedSize(width, height)
}

// SendKeysRaw writes raw key bytes through the client's PTY. Inline attach
// uses it.
func (p Pane) SendKeysRaw(b []byte) error {
	if p.c == nil {
		return errNoClient
	}
	return p.c.SendKeysRaw(b)
}

// Paste sends text as a bracketed paste. Without a live session it does
// nothing.
func (p Pane) Paste(text string) error {
	if p.c == nil || !p.c.DoesSessionExist() {
		return nil
	}
	return p.c.Paste(text)
}

// ForwardWheel forwards n wheel events so a TUI agent scrolls its own
// view. Without a live session it does nothing.
func (p Pane) ForwardWheel(up bool, n int) error {
	if p.c == nil || !p.c.DoesSessionExist() {
		return nil
	}
	return p.c.ForwardWheel(up, n)
}

// ForwardMouse forwards one SGR mouse event at (col,row). Without a live
// session it does nothing.
func (p Pane) ForwardMouse(cb, col, row int, press bool) error {
	if p.c == nil || !p.c.DoesSessionExist() {
		return nil
	}
	return p.c.ForwardMouse(cb, col, row, press)
}

// ForwardFocus forwards a focus in/out event when the agent has asked for
// them. It is best-effort: a failure is logged.
func (p Pane) ForwardFocus(in bool) {
	if p.c == nil {
		return
	}
	if err := p.c.ForwardFocus(in); err != nil {
		log.For("ui").Warn("forward_focus_failed", "session", p.c.SessionName(), "err", err)
	}
}

// GetContentHash returns the hash of the screen that the last status scan
// saw.
func (p Pane) GetContentHash() []byte {
	if p.c == nil {
		return nil
	}
	return p.c.GetContentHash()
}

// DetectStatus runs one status scan of the pane's screen (see
// tmux.TmuxSession.DetectStatus). Without a client there is no screen to
// scan, so it gives no opinion.
func (p Pane) DetectStatus() (updated, hasPrompt bool, err error) {
	if p.c == nil {
		return false, false, nil
	}
	return p.c.DetectStatus()
}

// scrollSource adapts the pane's client to the scroll state machine. ok
// is false when there is no client.
func (p Pane) scrollSource() (scrollSource, bool) {
	if p.c == nil {
		return nil, false
	}
	return p.c, true
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./ui -run 'TestPaneClients|TestPane_' -v`
Expected: PASS.

- [ ] **Step 5: Race, format, vet**

```bash
CC=clang CGO_ENABLED=1 go test -race ./ui -run 'TestPaneClients|TestPane_'
gofmt -w ui/panes.go ui/panes_test.go
go vet ./ui/
```

### B2. Route every agent-pane display read and input through `ui.Pane`

Behaviour is unchanged: `For` still falls back to the instance's own client.

**Files:**
- Modify: `ui/preview.go`, `ui/scroll.go`, `ui/cursor.go`, `ui/card.go`, `ui/list.go`, `ui/overview.go`, `ui/split_pane.go`
- Create: `app/panes.go`
- Modify: `app/app.go`, `app/app_init.go`, `app/workspaces.go`, `app/events.go`, `app/completions.go`, `app/interact.go`, `app/state_inline_attach.go`, `app/overview.go`
- Modify tests: `ui/card_account_test.go`, `ui/card_agents_test.go`, `ui/card_github_test.go`, `ui/card_message_test.go`

- [ ] **Step 1: `ui/scroll.go`**

Delete `scrollSourceFor`; `Pane.scrollSource` replaces it. Drop the `session` import if nothing else in the file uses it. In the doc of the `scrollSource` interface, keep "Implemented by *tmux.TmuxSession".

- [ ] **Step 2: `ui/preview.go`**

Add this field to the `PreviewPane` struct, after `src`:
```go
	// panes resolves the shown instance's attach client (SetPanes); nil
	// renders every instance as having none.
	panes *PaneClients
```
Add this method after `NewPreviewPane`:
```go
// SetPanes sets the registry the pane reads its instance's attach client
// from.
func (p *PreviewPane) SetPanes(panes *PaneClients) { p.panes = panes }
```
Then replace:
- In `liveTail`: `content, err := instance.Pane().Preview()` becomes `content, err := p.panes.For(instance).Preview()`.
- In `UpdateContent`: `if src, srcOK := scrollSourceFor(instance); srcOK {` becomes `if src, srcOK := p.panes.For(instance).scrollSource(); srcOK {`.
- In `updateContentSnapshotScrolled`: `hist, ok := instance.Pane().CaptureHistory()` becomes `hist, ok := p.panes.For(instance).CaptureHistory()`.
- In `emulatorScroll`: `src, ok := scrollSourceFor(instance)` becomes `src, ok := p.panes.For(instance).scrollSource()`.
- In each of `ScrollUp`, `ScrollDown`, `PageUp`, `PageDown`, `GotoTop` and `GotoBottom`, rewrite the alt-screen fallback as below (shown for `ScrollUp`). Each method keeps its own `ForwardWheel` arguments: `true, 1`, `false, 1`, `true, agentPageNotches`, `false, agentPageNotches`, `true, 30` and `false, 30` respectively. Drop the `instance != nil` guard, since `For(nil)` is the zero `Pane`.
```go
	// Snapshot path: probe tmux directly (rare path, no TTL cache).
	if pane := p.panes.For(instance); pane.IsAlternateScreen() {
		return pane.ForwardWheel(true, 1)
	}
```

- [ ] **Step 3: `ui/split_pane.go` and `ui/cursor.go`**

In `SplitPane`, add this field after `instance *session.Instance`:
```go
	// panes is the attach-client registry the agent pane and the cursor read.
	panes *PaneClients
```
and this method after `Instance()`:
```go
// SetPanes sets the registry the agent pane and the hardware cursor read
// attach clients from.
func (s *SplitPane) SetPanes(panes *PaneClients) {
	s.panes = panes
	s.agent.SetPanes(panes)
}
```
In `ui/cursor.go`, `c, have = instance.Pane().CursorState()` becomes `c, have = s.panes.For(instance).CursorState()`.

- [ ] **Step 4: `ui/card.go`, `ui/list.go`, `ui/overview.go`**

`BuildCardData` now takes the pane. Replace its doc and signature, and the read of the tail:
```go
// BuildCardData snapshots inst into a CardData. pane is inst's pane
// (PaneClients.For), and the live tail is read from its in-memory emulator
// screen, so calling this per visible card per frame forks no
// subprocesses. Instances on the snapshot path render their status label
// instead of a tail. spinnerFrame is the current spinner view ("" when
// unavailable). tailN caps the tail; 0 skips the screen read entirely
// (DensityLine callers). When Claude's last message is current and the
// session is not working, the tail is the end of that message instead,
// on either path.
func BuildCardData(inst *session.Instance, pane Pane, selected bool, spinnerFrame string, tailN int) CardData {
```
```go
		} else if screen, ok := pane.EmulatorScreen(); ok {
```
In `ui/list.go`, add this field to `List` after `peers`:
```go
	// panes is the attach-client registry the cards' tails and preview
	// sizing go through (SetPanes).
	panes *PaneClients
```
and this method after `NewList`:
```go
// SetPanes sets the registry the list reads its instances' attach clients
// from.
func (l *List) SetPanes(panes *PaneClients) { l.panes = panes }
```
Replace `SetSessionPreviewSize`'s body:
```go
func (l *List) SetSessionPreviewSize(width, height int) (err error) {
	// New clients attach at this size too (PaneClients.Ensure).
	l.panes.SetDefaultSize(width, height)
	for i, item := range l.items {
		if !item.Started() || item.Paused() || !item.Pane().TmuxAlive() {
			continue
		}

		if innerErr := l.panes.For(item).SetPreviewSize(width, height); innerErr != nil {
			err = errors.Join(
				err, fmt.Errorf("could not set preview size for instance %d: %v", i, innerErr))
		}
	}
	return
}
```
In `List.String`, `d := BuildCardData(l.items[i], i == l.selectedIdx, spinnerFrame, 1)` becomes `d := BuildCardData(l.items[i], l.panes.For(l.items[i]), i == l.selectedIdx, spinnerFrame, 1)`.

In `ui/overview.go`, add this field to `OverviewData`:
```go
	// Panes resolves each card's attach client for its tail.
	Panes *PaneClients
```
In `renderGroupGrid`, `cd := BuildCardData(g.Items[idx], selected, d.Spinner, overviewCardTailLines+1)` becomes `cd := BuildCardData(g.Items[idx], d.Panes.For(g.Items[idx]), selected, d.Spinner, overviewCardTailLines+1)`.

Update the eight `BuildCardData` calls in `ui/card_account_test.go`, `ui/card_agents_test.go`, `ui/card_github_test.go` and `ui/card_message_test.go` by inserting `Pane{}` as the second argument, e.g. `BuildCardData(inst, Pane{}, false, "", 0)`.

- [ ] **Step 5: Run the ui tests**

Run: `CGO_ENABLED=0 go test ./ui/...`
Expected: PASS. A pane without a registry falls back to the instance's own client.

- [ ] **Step 6: `app`: the registry and its wiring**

In `app/app.go`, add this field to the `home` struct after `cmdExec`:
```go
	// panes holds the TUI's attach clients, one per live agent tmux session
	// (ui.PaneClients). Everything that renders an agent pane, scrolls it,
	// forwards input to it or scrapes its screen for status goes through
	// it. It is shared by every slot's list and split pane, and is never
	// nil after newHome.
	panes *ui.PaneClients
```
In `newHome` in `app/app_init.go`, add `panes: ui.NewPaneClients(),` to the `h := &home{…}` literal. Right after the literal, add:
```go
	sp.SetPanes(h.panes)
```
and change `h.list = ui.NewList(&h.spinner)` to:
```go
	h.list = ui.NewList(&h.spinner)
	h.list.SetPanes(h.panes)
```
In `activateWorkspace` in `app/workspaces.go`, add `list.SetPanes(m.panes)` after `list := ui.NewList(&m.spinner)`, and add `splitPane.SetPanes(m.panes)` after `splitPane := ui.NewSplitPane(…)`. In `enterGlobalMode`, build the global list before the `global := &workspaceSlot{…}` literal:
```go
	globalList := ui.NewList(&m.spinner)
	globalList.SetPanes(m.panes)
```
and use `list: globalList,` in the literal. In `overviewData` in `app/overview.go`, add `Panes: m.panes` to both returned `ui.OverviewData` literals.

- [ ] **Step 7: `app`: display reads go through the pane**

Create `app/panes.go`:
```go
package app

import (
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
)

// paneSnapshot resolves each instance's pane on the Update goroutine, for
// use by a Cmd that must not read the model.
func (m *home) paneSnapshot(insts []*session.Instance) map[*session.Instance]ui.Pane {
	out := make(map[*session.Instance]ui.Pane, len(insts))
	for _, inst := range insts {
		out[inst] = m.panes.For(inst)
	}
	return out
}
```
In `app/events.go`, replace `statusDetectCmd` and `verifyDeadCmd`:
```go
func statusDetectCmd(inst *session.Instance, pane ui.Pane) tea.Cmd {
	return func() tea.Msg {
		updated, hasPrompt, err := pane.DetectStatus()
		return statusDetectedMsg{instance: inst, updated: updated, hasPrompt: hasPrompt, err: err}
	}
}
```
```go
func verifyDeadCmd(inst *session.Instance, pane ui.Pane) tea.Cmd {
	return func() tea.Msg {
		return deadVerifiedMsg{instance: inst, tmuxLive: inst.Pane().TmuxLiveness(), ptmxAlive: pane.PtmxAlive()}
	}
}
```
In `app/app.go`:
- `paneQuietMsg`: `return m, tea.Batch(scan, statusDetectCmd(inst))` becomes `return m, tea.Batch(scan, statusDetectCmd(inst, m.panes.For(inst)))`.
- `redetectMsg`: `return m, statusDetectCmd(inst)` becomes `return m, statusDetectCmd(inst, m.panes.For(inst))`.
- `ptyDeadMsg`: `cmds = append(cmds, verifyDeadCmd(inst))` becomes `cmds = append(cmds, verifyDeadCmd(inst, m.panes.For(inst)))`.
- `previewTickMsg`: `currentHash = selected.Pane().GetContentHash()` becomes `currentHash = m.panes.For(selected).GetContentHash()`.
- `tickUpdateMetadataMessage`: `cmds = append(cmds, gatherMetadataCmd(active, selected, m.takeDirty(), m.ghBases))` becomes `cmds = append(cmds, gatherMetadataCmd(active, selected, m.takeDirty(), m.ghBases, m.paneSnapshot(active)))`.
- `instanceChanged`: `prev.Pane().ForwardFocus(false)` becomes `m.panes.For(prev).ForwardFocus(false)`, and `selected.Pane().ForwardFocus(true)` becomes `m.panes.For(selected).ForwardFocus(true)`.
- `forwardFocus`: `selected.Pane().ForwardFocus(in)` becomes `m.panes.For(selected).ForwardFocus(in)`.
- `windowTitle`: `if t, ok := sel.Pane().PaneTitle(); ok {` becomes `if t, ok := m.panes.For(sel).PaneTitle(); ok {`.
- `gatherMetadataCmd` gets a new signature and reads through the pane:
```go
func gatherMetadataCmd(active []*session.Instance, selected *session.Instance, dirty map[string]bool, bases map[string]string, panes map[*session.Instance]ui.Pane) tea.Cmd {
```
```go
				r.tmuxLive = instance.Pane().TmuxLiveness()
				if r.tmuxLive != tmux.LivenessAlive {
					return
				}
				pane := panes[instance]
				r.ptmxAlive = pane.PtmxAlive()

				// Event-mode instances get status from quiet events, so the
				// subprocess scan only remains for the snapshot path. With
				// no client there is no screen to scan, and no opinion.
				r.emulatorDriven = pane.Client() == nil || pane.HasEmulator()
				if !r.emulatorDriven {
					r.updated, r.hasPrompt, r.captureErr = pane.DetectStatus()
				}
```
In `reopenedTwin` in `app/completions.go`, `if twin.Paused() && !twin.Pane().PtmxAlive() {` becomes `if twin.Paused() && !m.panes.For(twin).PtmxAlive() {`. `For` gives a paused twin the zero pane, which gives the same answer as the old read for a twin that reconcile never attached.

In `forwardClickToFocused` in `app/interact.go`, replace the two `selected.Pane().ForwardMouse(…)` calls with:
```go
		pane := m.panes.For(selected)
		_ = pane.ForwardMouse(0, col+1, row+1, true)
		_ = pane.ForwardMouse(0, col+1, row+1, false)
```
In `pasteToFocused`, `_ = selected.Pane().Paste(text)` becomes `_ = m.panes.For(selected).Paste(text)`.

In `app/state_inline_attach.go`, `err = selected.Pane().SendKeysRaw(b)` becomes `err = m.panes.For(selected).SendKeysRaw(b)`.

- [ ] **Step 8: Build, vet, test**

Run: `go build ./... && go vet ./... && CGO_ENABLED=0 go test ./ui/... ./app/... ./session/...`
Expected: PASS.

Then run: `git grep -n -E '\.Pane\(\)\.(Preview|EmulatorScreen|CaptureHistory|IsAlternateScreen|CursorState|PaneTitle|HasEmulator|SetPreviewSize|SendKeysRaw|Paste|ForwardWheel|ForwardMouse|ForwardFocus|HasUpdated|GetContentHash|CaptureAndProcessStatus|PtmxAlive)\(' -- '*.go' ':!*_test.go' ':!vendor'`
Expected: only `script/userdata_instance.go`'s `Preview`, which is Lua and becomes lifecycle-side in Package C.

- [ ] **Step 9: Package B checkpoint and commit**

```bash
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./app/... ./ui/...
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') ui/panes.go ui/panes_test.go app/panes.go
git add -A ui/ app/
git commit -m "refactor(ui,app): read agent panes through PaneClients

ui.PaneClients holds one attach client per tmux session name, and every
pane render, scroll, cursor, mouse/paste/key forward and status scrape
goes through ui.Pane. Until the instance stops attaching, For falls back
to the instance's own client.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Package C: The flip. Instances hold a `tmux.Session`, and the TUI attaches and releases its own clients

After this package, `session.Instance` attaches nothing. The registry builds its own clients by name. The app attaches them at every load, completion, restart and repair, and releases them on the health tick, on kill and pause, and on every slot drop. This is the risky package, so its checkpoints run in order — session, then ui, then app — and a failure points at one layer. It ends in one commit, made only once the whole suite is green.

**Files:**
- Modify: `session/instance.go`, `session/reconcile.go`, `session/agent_pane.go`, `session/agent_restart.go`, `session/tmux/tmux.go`
- Modify: `ui/panes.go`
- Modify: `app/panes.go`, `app/github.go`, `app/app.go`, `app/app_init.go`, `app/workspaces.go`, `app/completions.go`, `app/intents.go`
- Create: `app/panes_test.go`, `app/testpanes_test.go`
- Modify tests: listed in C2, C3 and C7

### Amendments (binding; added 2026-10-03 after the reviews of Packages A and B)

These amend the steps below. Where a step's text disagrees with an amendment, the amendment wins.

1. **Package A changed the input API.** `Session.TypeText` uses `load-buffer` + `paste-buffer`, and errors carry tmux's stderr. `SetCmdExecForTest` lives on `*Session`. The parity test is now `TestTypeTextMatchesPTYWrite_RealTmux`. None of this changes a step below, but it is the API you build on.
2. **A client's adapter comes from the program its session runs, not from `inst.Program()`.** `R` on a paused instance whose tmux session is still alive reattaches the old session: `Resume` keeps the old `tmux.Session`, while `i.Program()` already names the new program. A client built from `inst.Program()` would scan that session with the wrong trust-prompt and pending-prompt patterns.
   - In C1, add to `session/tmux/session.go`:
     ```go
     // Program returns the command line the session was launched with.
     func (s *Session) Program() string { return s.program }
     ```
   - Add to `session/agent_pane.go`:
     ```go
     // SessionProgram returns the command line the agent's tmux session was
     // launched with ("" without a session). A pane client's status scan must
     // use it: after a reattach it can differ from Instance.Program.
     func (p AgentPane) SessionProgram() string {
     	ts := p.i.getTmuxSession()
     	if ts == nil {
     		return ""
     	}
     	return ts.Program()
     }
     ```
   - In C3, `ensurePane` and `replacePane` pass `inst.Pane().SessionProgram()` where the plan shows `inst.Program()`.
   - In C6, `attachTestClient` builds its client with `inst.Pane().SessionProgram()`.
   - Add a unit test in `session/` that a reattaching resume keeps the old program in `SessionProgram()` while `Program()` reports the new one.
3. **Registry tests the flip relies on.** Add these to `ui/panes_test.go` in C2:
   - A client whose attach fails stays registered, and a later `Ensure` retries it. Use a PTY factory whose first `Start` errors.
   - `Retain` followed by `Ensure` for the same name builds a *new* client, never the released one.
   - A `-race` test: `For`/`Get` from several goroutines while the test goroutine runs `Ensure`/`Retain`.
4. **`reopenedTwin`** (`app/completions.go`): `&& !m.panes.For(twin).PtmxAlive()` is always true, because `For` never gives a paused twin a client. Drop the conjunct, so the rule is twin `Paused()`. In its doc, "plus Paused and unattached" becomes "plus Paused (a paused instance has no pane client)".
5. **Doc fixes in `ui/panes.go`:** "Every method is nil-receiver safe" becomes "Every method except the …ForTest helpers is nil-receiver safe". `ui/preview.go`'s `panes` field comment becomes true once the fallback is gone; leave it.
6. **Close released clients in parallel.** On creack/pty the attach PTY's fd is blocking, so `PausePreview` of a client whose session is still live waits the full 2s `pumpWaitTimeout`. A client of a dead session gets EIO at once. C releases clients more often than before, so `releaseClientsCmd` (`app/workspaces.go`) must close its clients concurrently, one goroutine each with a `sync.WaitGroup`, and return once all are closed. N live clients then cost about 2s, not N×2s. Keep every `PausePreview`/`Close` of a client with a live pump off the Update goroutine: `Replace`'s old client always goes through `releaseClientsCmd`. Making the PTY pollable, so a release takes milliseconds, is a separate follow-up, not part of this plan.

### C1. Session: the instance holds a `tmux.Session`

- [ ] **Step 1: `session/instance.go`**

- The field becomes `tmuxSession *tmux.Session`. `getTmuxSession` and `setTmuxSession` change to `*tmux.Session`.
- In `FromInstanceData`, `tmux.NewTmuxSession(` becomes `tmux.NewSession(` with the same arguments.
- In `Start`, `ts = tmux.NewTmuxSession(i.Title, launchProgram, InstanceEnv(env)...)` becomes `ts = tmux.NewSession(i.Title, launchProgram, InstanceEnv(env)...)`. Replace the launch block (`if !firstTimeSetup { … } else if i.IsWorkspaceTerminal { … } else { … }`) with:
```go
	switch {
	case !firstTimeSetup:
		// The session already runs (a reconciled record). Session lifecycle
		// attaches no client (the TUI attaches its own, by name), so there
		// is nothing to do beyond marking the instance started below.
	case i.IsWorkspaceTerminal:
		// Workspace terminal: start tmux directly in root repo, no worktree
		if err := ts.Start(i.Path); err != nil {
			setupErr = fmt.Errorf("failed to start workspace terminal session: %w", err)
			return setupErr
		}
	default:
		// Setup git worktree first
		if err := gw.Setup(); err != nil {
			setupErr = fmt.Errorf("failed to setup git worktree: %w", err)
			return setupErr
		}

		// Create new session
		if err := ts.Start(gw.GetWorktreePath()); err != nil {
			setupErr = i.failedStartCleanup(ts, gw, err)
			return setupErr
		}
	}
```
- In `Restart`, change the comment `// Already dead; this only releases the PTY, emulator and pump.` to `// Already dead: Close only makes sure nothing holds the name before Start.`
- `EnsureRunning`'s doc becomes:
```go
// EnsureRunning marks a restored instance whose tmux session is running
// as started (Start(false)). It attaches nothing (the TUI attaches its own
// client to the session by name) and is a no-op for paused, Recoverable
// and already-started instances.
```
- `failedStartCleanup(ts *tmux.TmuxSession, …)` becomes `failedStartCleanup(ts *tmux.Session, …)`.
- `TmuxSession()` now returns `*tmux.Session`. Its doc becomes `// TmuxSession returns the instance's tmux session (lifecycle only: launch, probe, kill, send-keys), or nil if the instance has not been started. The app takes a full-screen attach command from it.`
- The signature becomes `finishResume(saveState func() error, ts *tmux.Session, gw *git.GitWorktree)`. Replace the first two cases of its `switch` with:
```go
	case tmux.LivenessAlive:
		// The session is running. Reattaching is the TUI's: it attaches its
		// own client by name when the resume lands.
	case tmux.LivenessDead:
		// Close kills by exact name, so this cannot reach another session —
		// and it must run before the new session under the same name exists.
		if err := ts.Close(); err != nil {
			log.For("session").Debug("resume_close_dead_session", "err", err.Error())
		}
		if err := i.startFreshWithRecovery(gw); err != nil {
			return err
		}
```
- `var newRecoverySession = tmux.NewTmuxSession` becomes `var newRecoverySession = tmux.NewSession`.
- `SetTmuxSession(session *tmux.TmuxSession)` becomes `SetTmuxSession(session *tmux.Session)`.

- [ ] **Step 2: `session/reconcile.go` and `session/agent_restart.go`**

In `fromInstanceDataPaused`, `tmux.NewTmuxSession(` becomes `tmux.NewSession(`. In its doc, "creates a TmuxSession object but does not connect" becomes "creates its tmux.Session (lifecycle only; nothing ever connects to it)". In `ReconcileAndRestore`'s `ActionRestore` case, replace the comment inside `if err := instance.EnsureRunning(); err != nil {` with:
```go
			// EnsureRunning touches no tmux (it attaches nothing), so this
			// fails only on bad data or an unresolvable account. Return the
			// error rather than masking it with a crash-restart:
			// LoadAndReconcile stashes the raw record in the unrecovered
			// cache (storage.go) so it survives in state.json and is retried
			// on the next launch.
```
In `session/agent_restart.go`, the comment that mentions `tmux.NewTmuxSession's variadic env parameter` now names `tmux.NewSession's`.

- [ ] **Step 3: `session/agent_pane.go`: the lifecycle surface only**

Replace the file with:
```go
package session

import (
	"fmt"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
)

// AgentPane is what session lifecycle does with an instance's agent pane
// without an attach client: liveness probes, the session name, a
// capture-pane read of the screen, and typing keys and prompts through
// send-keys. Rendering the pane and forwarding input through a PTY belong
// to the TUI's client (ui.Pane), which the instance never holds. Each
// method keeps its guard: started and not paused for the screen read and
// keys (SendPrompt checks only started), and only "has a tmux session" for
// the liveness probes.
//
// It is a cheap value wrapper around the *Instance: take one with
// inst.Pane() at the call site rather than storing it.
type AgentPane struct{ i *Instance }

// Pane returns the agent pane's lifecycle surface. It is never nil; each
// of its methods applies its own guard.
func (i *Instance) Pane() AgentPane { return AgentPane{i: i} }

// Preview returns the pane's visible screen through capture-pane (no
// client needed). It is empty, not an error, when the instance is not
// started, is paused, or its session is gone. Used by Lua's inst:preview().
func (p AgentPane) Preview() (string, error) {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", nil
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return "", nil
	}
	return ts.CapturePaneContent()
}

// SendKeys types keys into the agent's tmux session through send-keys.
func (p AgentPane) SendKeys(keys string) error {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return fmt.Errorf("cannot send keys to instance that has not been started or is paused")
	}
	return i.getTmuxSession().TypeText(keys)
}

// SendPrompt types prompt into the agent's tmux session and submits it,
// through send-keys.
func (p AgentPane) SendPrompt(prompt string) error {
	i := p.i
	if !i.isStarted() {
		return fmt.Errorf("instance not started")
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return fmt.Errorf("tmux session not initialized")
	}
	return ts.SendPrompt(prompt)
}

// TapEnter presses Enter in the tmux session when the instance is
// running; otherwise it does nothing. Exposed to Lua scripts as
// inst:tap_enter().
func (p AgentPane) TapEnter() {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return
	}
	if err := ts.PressKeys("Enter"); err != nil {
		log.For("session").Error("tap_enter_failed", "err", err)
	}
}

// TmuxAlive reports whether the tmux session is alive: a sanity check
// before acting on the pane.
func (p AgentPane) TmuxAlive() bool {
	ts := p.i.getTmuxSession()
	if ts == nil {
		return false
	}
	return ts.DoesSessionExist()
}

// TmuxLiveness reports the session's liveness, distinguishing "tmux
// answered no" from "the probe never got an answer". Callers that change
// an instance's state on a negative must use this rather than TmuxAlive,
// which collapses both cases into false.
func (p AgentPane) TmuxLiveness() tmux.Liveness {
	ts := p.i.getTmuxSession()
	if ts == nil {
		return tmux.LivenessDead
	}
	return ts.SessionLiveness()
}

// TmuxSessionName returns the tmux session name backing this instance, or
// "" when it has no session. Pane events and the TUI's pane clients are
// keyed by it.
func (p AgentPane) TmuxSessionName() string {
	ts := p.i.getTmuxSession()
	if ts == nil {
		return ""
	}
	return ts.SessionName()
}
```

- [ ] **Step 4: `session/tmux/tmux.go`**

Delete `TmuxSession.WithProgram` and `TmuxSession.WithProgramEnv`; `Session`'s versions remain. In `session/tmux/tmux_test.go`, delete `TestWithProgram` and `TestWithProgramEnv`, which `TestSession_WithProgramEnv` covers.

- [ ] **Step 5: Session test migration**

- In `session/instance_lifecycle_test.go`, `session/start_cleanup_test.go` and `session/stash_notice_test.go`, every `tmux.NewTmuxSessionWithDeps(` becomes `tmux.NewSessionWithDeps(` with the same arguments.
- Also in `session/instance_lifecycle_test.go`:
  - Delete `TestInstance_KillBoundedWithStuckPump`. `Kill` no longer closes a PTY or waits on an output pump, because the session holds neither. The bounded pump wait itself stays pinned at the client level (`TestPumpWaitBoundedOnClose` and its siblings in `session/tmux`).
  - The `SetCmdExecForTest` calls in `TestInstance_PauseClosesTerminalPaneSession` and `TestInstance_KillClosesTerminalPaneSession` keep compiling: Package A's fix moved `SetCmdExecForTest` onto `Session`.
- In `session/resume_inplace_test.go`:
  - Both `newRecoverySession = func(name, program string, env ...string) *tmux.TmuxSession {` overrides become `… *tmux.Session {`, returning `tmux.NewSessionWithDeps(name, program, srv, srv.runner(), env...)`.
  - Delete the `failAttach` field and the `attach-session` branch of `fakeTmuxServer.Start`. Nothing attaches now.
  - Replace `TestResume_RelaunchReleasesTheDeadSession` with:
```go
// TestResume_RelaunchClosesTheDeadSession: relaunching kills the dead
// session first, by exact name (tmux prefix-matches a bare -t, and the
// session is gone), and then replaces the session object.
func TestResume_RelaunchClosesTheDeadSession(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	old := inst.getTmuxSession()

	require.NoError(t, resumeLikeApp(t, inst))

	assert.True(t, srv.ran("kill-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(inst.Title))),
		"the dead session must be closed, by exact name")
	assert.NotSame(t, old, inst.getTmuxSession())
}
```

- [ ] **Step 6: Checkpoint: session builds and passes on its own**

Run: `go build ./session/... && go vet ./session/... && CGO_ENABLED=0 go test ./session/...`
Expected: PASS. `ui` and `app` do not build yet; C2 and C3 fix them.

Then run: `git grep -n -E 'Restore\(\)|PausePreview|ResumePreview|attach-session' -- 'session/*.go' ':!session/tmux' ':!*_test.go'`
Expected: no output. Nothing in `session` attaches anymore.

### C2. ui: no fallback

- [ ] **Step 1: `ui/panes.go`**

Replace `For`:
```go
// For returns inst's pane: its session's client here, when inst is started
// and not paused; otherwise the zero Pane.
func (p *PaneClients) For(inst *session.Instance) Pane {
	if inst == nil || !inst.Started() || inst.Paused() {
		return Pane{}
	}
	return Pane{c: p.Get(inst.Pane().TmuxSessionName())}
}
```
In `ui/panes_test.go` `TestPaneClients_ForGuardsTheInstance`, replace the two lines under `// Stage 1A transition (until Package C)…` with:
```go
	assert.Nil(t, p.For(inst).Client(), "nothing attached: no pane")
```

- [ ] **Step 2: ui test migration**

In `ui/preview_test.go`, `setupTestEnvironment` builds the lifecycle session and a registry holding a client for it:
- Add `panes *PaneClients` to `testSetup`.
- Replace
```go
	tmuxSession := tmux.NewTmuxSessionWithDeps(sessionName, "bash", ptyFactory, cmdExec)
	instance.SetTmuxSession(tmuxSession)
```
  with
```go
	instance.SetTmuxSession(tmux.NewSessionWithDeps(sessionName, "bash", ptyFactory, cmdExec))
```
- After the `require.NoError(t, err)` that follows `instance.Start(true)`, add:
```go
	// The TUI's attach client, by name, as the app's ensurePane builds it.
	panes := NewPaneClients()
	client := tmux.NewAttachClientWithDeps(tmux.ToLoomTmuxName(sessionName), "bash", ptyFactory, cmdExec)
	require.NoError(t, client.Restore())
	panes.InjectForTest(client.SessionName(), client)
```
  and set `panes: panes,` in the returned `testSetup`.
- In every test that calls `setupTestEnvironment` and builds a `PreviewPane`, add `p.SetPanes(setup.panes)` right after `p := NewPreviewPane()`. Find them with `git grep -n -A4 'setupTestEnvironment(' -- ui/preview_test.go`.
- `require.True(t, setup.instance.Pane().IsAlternateScreen(), …)` becomes `require.True(t, setup.panes.For(setup.instance).IsAlternateScreen(), …)`.

- [ ] **Step 3: Checkpoint**

Run: `go build ./ui/... && CGO_ENABLED=0 go test ./ui/...`
Expected: PASS.

### C3. app: the pane lifecycle helpers

- [ ] **Step 1: `activeInstance`**

In `app/github.go`, extract the predicate and use it:
```go
// activeInstance reports whether the background jobs may touch inst (see
// activeInstances).
func activeInstance(inst *session.Instance) bool {
	st := inst.GetStatus()
	return inst.Started() && !inst.Paused() && st != session.Deleting && st != session.Recoverable && st != session.Loading
}
```
```go
func (m *home) activeInstances() []*session.Instance {
	var active []*session.Instance
	for _, inst := range m.allInstances() {
		if activeInstance(inst) {
			active = append(active, inst)
		}
	}
	return active
}
```

- [ ] **Step 2: `app/panes.go`**

Replace the file (B2 created it with `paneSnapshot` only) with:
```go
package app

import (
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// The TUI's pane clients (m.panes, ui.PaneClients) give every live agent
// tmux session one attach client. Everything that renders, scrolls,
// forwards input to or scrapes an agent pane goes through it. Session
// lifecycle never attaches one.
//
// Attach points, all on the Update goroutine:
//   - a workspace load: every active instance of the slot (ensureSlotPanes);
//   - a start, resume or recover landing in a loaded slot, and a workspace
//     terminal's auto-restart: the session was (re)launched, so any client
//     from before was watching the session it replaced (replacePane);
//   - the health tick's repair, when the session is alive but its client's
//     PTY is gone, and a full-screen attach returning (ensurePane).
//
// Release points: the health tick, kill, pause, a failed transition and
// every slot drop (prunePanes) release the clients of sessions no loaded
// instance is active on. A release closes the client's PTY off the Update
// goroutine (releaseClientsCmd): PausePreview waits for the client's
// output pump, which blocks in tea.Program.Send until Update returns. A
// released client is never re-attached: Retain removes it first, and
// Ensure builds a new one.

// paneSnapshot resolves each instance's pane on the Update goroutine, for
// a Cmd that must not read the model.
func (m *home) paneSnapshot(insts []*session.Instance) map[*session.Instance]ui.Pane {
	out := make(map[*session.Instance]ui.Pane, len(insts))
	for _, inst := range insts {
		out[inst] = m.panes.For(inst)
	}
	return out
}

// livePaneNames is the set of tmux session names that should have a
// client: those of the active instances of every loaded slot.
func (m *home) livePaneNames() map[string]bool {
	names := make(map[string]bool)
	for _, inst := range m.activeInstances() {
		if name := inst.Pane().TmuxSessionName(); name != "" {
			names[name] = true
		}
	}
	return names
}

// ensurePane gives inst's session a client, re-attaching one whose PTY is
// gone (ui.PaneClients.Ensure).
func (m *home) ensurePane(inst *session.Instance) {
	name := inst.Pane().TmuxSessionName()
	if name == "" {
		return
	}
	if err := m.panes.Ensure(name, inst.Program()); err != nil {
		log.For("app").Error("pane.attach_failed", "session", name, "err", err)
	}
}

// replacePane gives inst's session, just (re)launched, a fresh client. It
// returns a Cmd closing the client it replaced, or nil when there was none.
func (m *home) replacePane(inst *session.Instance) tea.Cmd {
	name := inst.Pane().TmuxSessionName()
	if name == "" {
		return nil
	}
	old, err := m.panes.Replace(name, inst.Program())
	if err != nil {
		log.For("app").Error("pane.attach_failed", "session", name, "err", err)
	}
	if old == nil {
		return nil
	}
	return releaseClientsCmd(attachedClients([]*tmux.TmuxSession{old}))
}

// ensureSlotPanes gives every active instance of slot a client.
func (m *home) ensureSlotPanes(slot *workspaceSlot) {
	for _, inst := range slot.list.GetInstances() {
		if activeInstance(inst) {
			m.ensurePane(inst)
		}
	}
}

// prunePanes drops the clients of sessions no loaded instance is active
// on, and returns a Cmd closing them.
func (m *home) prunePanes() tea.Cmd {
	return releaseClientsCmd(attachedClients(m.panes.Retain(m.livePaneNames())))
}
```

### C4. app: the attach points

- [ ] **Step 1: Workspace loads**

In `app/app_init.go`'s `loadSlotStorage`, add this immediately before the final `return recovery, nil`:
```go
	m.ensureSlotPanes(slot)
```
In `app/workspaces.go`'s `activateWorkspace`, add this immediately after the `m.slots = append(m.slots, &workspaceSlot{…})` statement:
```go
	m.ensureSlotPanes(m.slots[len(m.slots)-1])
```

- [ ] **Step 2: Completions**

In `app/completions.go`:
- `handleInstanceStarted`: in the reopened-twin failure branch, `return tea.Batch(m.handleError(msg.err), releaseInstancesCmd([]*session.Instance{inst}))` becomes `return m.handleError(msg.err)`. Delete the `var release tea.Cmd` / `if !loaded { release = releaseInstancesCmd(…) }` block. `return tea.Batch(m.handleError(err), release)` becomes `return m.handleError(err)`. After the prompt is sent (the `if prompt := inst.Prompt(); …` block) and before the `switch`, add:
```go
	var attach tea.Cmd
	if loaded {
		attach = m.replacePane(inst)
	}
```
  The final return becomes `return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), attach)`.
- `handleResumeDone`: delete the `else` branch that appends `releaseInstancesCmd([]*session.Instance{inst})`, keeping the `if adopted != nil { … }` block with no `else`. After the `if inst := msg.instance; inst != nil && m.slotHolding(inst) == nil { … }` block, add:
```go
	if inst := msg.instance; inst != nil && m.slotHolding(inst) != nil {
		cmds = append(cmds, m.replacePane(inst))
	}
```
- `handleRecoverDone`: delete the `var release …` / `if !loaded { … }` block. After the `if owner != nil { … }` block that replaces the placeholder, add:
```go
	var attach tea.Cmd
	if loaded {
		attach = m.replacePane(msg.recovered)
	}
```
  The final return becomes `return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), attach)`.
- In the `reopenedTwin` check, `if twin.Paused() && !m.panes.For(twin).PtmxAlive() {` (from B2) stays as it is.
- Rewrite the package comment at the top of the file and the docs of `handleInstanceStarted`, `handleResumeDone` and `handleRecoverDone`. Wherever an owner closed meanwhile "attaching a preview client" or being "released" is mentioned, say instead: "A completion whose owner was closed meanwhile attaches nothing (nothing displays it); one landing in a loaded slot attaches the instance's client (replacePane)." Keep every other sentence.

- [ ] **Step 3: `applyLiveness`: repair and restart**

In `app/app.go`, change `applyLiveness`'s signature and the first line of its doc to:
```go
// applyLiveness reacts to one instance's health-probe result: dead tmux →
// pause (or restart a workspace terminal, with the existing circuit
// breaker); live tmux but no open attach client → re-attach it. It returns
// false when the instance was found dead (so callers can stop treating it
// as running) or is no longer in any loaded slot, plus a Cmd closing a
// client that a restart replaced. Must run on the Update goroutine.
func (m *home) applyLiveness(inst *session.Instance, tmuxLive tmux.Liveness, ptmxAlive bool) (alive bool, release tea.Cmd) {
```
In its first comment, "The probe was taken before inst's slot was dropped. Its attach client has been (or is being) released by releaseSlotCmd, …", change `released by releaseSlotCmd` to `released by prunePanes`. Each `return false` becomes `return false, nil` and `return true` becomes `return true, nil`. The exceptions are the workspace-terminal restart:
```go
			log.For("app").Warn("workspace_terminal.tmux_died_restarting", "title", inst.Title)
			if err := inst.Restart(); err != nil {
				log.For("app").Error("workspace_terminal.restart_failed", "title", inst.Title, "err", err)
				return false, nil
			}
			return false, m.replacePane(inst)
```
and the repair at the end:
```go
	if !ptmxAlive && inst != m.attachingInstance {
		// The session exists but its attach client is gone (e.g. a reattach
		// failed after full-screen attach returned). Nothing else ever
		// retries this, so self-heal here: the same shape as the
		// workspace-terminal restart above, but at the client layer.
		log.For("app").Warn("tick.ptmx_dead_repairing", "title", inst.Title)
		m.ensurePane(inst)
	}
	return true, nil
```
Update its callers in `Update`:
- `deadVerifiedMsg`:
```go
	case deadVerifiedMsg:
		if !statusEligible(msg.instance) {
			return m, nil
		}
		_, release := m.applyLiveness(msg.instance, msg.tmuxLive, msg.ptmxAlive)
		m.updateTabBarStatuses()
		return m, tea.Batch(m.instanceChanged(), release)
```
- `metadataReadyMsg`: declare `var releases []tea.Cmd` before the loop and open the loop with:
```go
		for _, r := range msg.results {
			alive, release := m.applyLiveness(r.instance, r.tmuxLive, r.ptmxAlive)
			releases = append(releases, release)
			if !alive {
				continue
			}
```
  End the case with `return m, tea.Batch(append(releases, tickUpdateMetadataCmd)...)`.

- [ ] **Step 4: Full-screen attach**

`startFullScreenAttachMsg` (whole case):
```go
	case startFullScreenAttachMsg:
		// Resolve the session to attach in the foreground, and the client
		// whose preview PTY must let go of it for the duration.
		var attach *exec.Cmd
		var preview *tmux.TmuxSession
		switch msg.target {
		case attachTargetAgent:
			if s := msg.instance.TmuxSession(); s != nil {
				attach = s.FullScreenAttachCmd()
				preview = m.panes.For(msg.instance).Client()
			}
		case attachTargetTerminal:
			if ts := m.splitPane.TerminalTmuxSession(); ts != nil {
				attach, preview = ts.FullScreenAttachCmd(), ts
			}
		}
		if attach == nil {
			return m, m.handleError(fmt.Errorf("no tmux session available for attach"))
		}
		// Close the preview PTY so the foreground tmux attach owns the tty.
		if preview != nil {
			if err := preview.PausePreview(); err != nil {
				return m, m.handleError(err)
			}
		}
		inst := msg.instance
		m.attachingInstance = inst
		return m, tea.ExecProcess(attach, func(err error) tea.Msg {
			return attachDoneMsg{instance: inst, err: err}
		})
```
Add `"os/exec"` to `app/app.go`'s imports.

In `attachDoneMsg`, replace the agent block (`if ts := msg.instance.TmuxSession(); ts != nil { … ResumePreview … }`) with:
```go
		// tea.ExecProcess has restored the terminal. Re-attach the agent's
		// client so live capture resumes. A failure is logged inside
		// ensurePane, and the metadata tick's repair retries it once
		// attachingInstance is cleared below.
		if msg.instance != nil {
			m.ensurePane(msg.instance)
		}
```
The terminal pane's `ResumePreview` block is unchanged.

### C5. app: the release points

- [ ] **Step 1: Health tick, kill, pause, failed transitions**

`tickUpdateMetadataMessage`: right after `m.errBox.ExpireIfDue(time.Now())`, add:
```go
		// Close the clients of sessions that stopped being active since the
		// last tick (paused, killed, exited, or their slot closed).
		prune := m.prunePanes()
```
and change the `var cmds []tea.Cmd` that follows to `cmds := []tea.Cmd{prune}`.

`killInstanceMsg`:
```go
	case killInstanceMsg:
		// … (comment unchanged)
		m.removeInstanceEverywhere(msg.inst)
		if msg.notice != nil {
			return m, tea.Batch(m.handleError(msg.notice), m.instanceChanged(), m.prunePanes())
		}
		return m, tea.Batch(m.instanceChanged(), m.prunePanes())
```
`pauseInstanceMsg`:
```go
	case pauseInstanceMsg:
		// Terminal session was already closed inside pauseAction off the update
		// goroutine. Nothing I/O-blocking to do here.
		return m, tea.Batch(m.instanceChanged(), m.prunePanes())
```
`transitionFailedMsg`: the resume no longer attaches before its checkpoint save, so the per-instance release and its comment block go:
```go
	case transitionFailedMsg:
		// Revert instance status on failed background op (kill/pause/resume).
		// previousStatus came from this same instance, so the reverse
		// transition should always be allowed; if the state machine rejects
		// it, log and leave the status as-is rather than masking a real bug.
		// The message carries the instance pointer: like killInstanceMsg, the
		// focused m.list may have been swapped since the op started.
		if msg.inst != nil {
			if terr := msg.inst.TransitionTo(msg.previousStatus); terr != nil {
				log.For("app").Warn("revert_transition_failed", "err", terr)
			}
		}
		log.For("app").Error("op_failed", "op", msg.op, "title", msg.title, "err", msg.err)
		return m, tea.Batch(m.handleError(msg.err), m.instanceChanged(), m.prunePanes())
```

In `app/intents.go`, close the TUI's client before the session goes, as the session's own `Close` used to:
- In `killActionFor`, next to `splitPane, storage := m.splitPane, m.storage`, add:
```go
	panes, paneName := m.panes, selected.Pane().TmuxSessionName()
```
  and immediately before `if err := selected.Kill(); err != nil {`, add:
```go
		// Close the TUI's attach client first: one left on a killed session
		// reads a dead PTY until the next prune.
		if c := panes.Get(paneName); c != nil {
			if err := c.PausePreview(); err != nil {
				log.For("app").Warn("kill.pane_close_failed", "title", title, "err", err)
			}
		}
```
- In `pauseActionFor`, next to `splitPane := m.splitPane`, add `panes, paneName := m.panes, selected.Pane().TmuxSessionName()`. Immediately before `if err := selected.Pause(saveFunc); err != nil {`, add:
```go
		// Close the TUI's attach client first, as kill does. If the pause
		// aborts (the session survived), the health tick's repair
		// re-attaches it.
		if c := panes.Get(paneName); c != nil {
			if err := c.PausePreview(); err != nil {
				log.For("app").Warn("pause.pane_close_failed", "title", pauseTitle, "err", err)
			}
		}
```

- [ ] **Step 2: Slot drops**

In `app/workspaces.go`:
- `releaseSlotCmd` keeps only the terminal pane's clients:
```go
// releaseSlotCmd returns a Cmd that releases the attach clients a dropped
// slot's terminal pane holds on each loom_term_* shell it has shown (the
// shells keep running). The agent panes' clients belong to the registry,
// which every drop site prunes (prunePanes). Returns nil when nothing is
// attached.
func releaseSlotCmd(slot *workspaceSlot) tea.Cmd {
	if slot == nil || slot.splitPane == nil {
		return nil
	}
	return releaseClientsCmd(attachedClients(slot.splitPane.Terminal().DetachAll()))
}
```
- Delete `releaseInstancesCmd`. Move the explanation of why the close runs off Update, which used to live on `releaseInstancesCmd`, into `releaseClientsCmd`'s doc: "PausePreview waits — up to the pump-exit timeout, per session — for the output pump to exit, and the pump delivers pane events through tea.Program.Send, which blocks until Update returns."
- `activateWorkspace`, inside `if len(m.slots) == 1 { … }` after `m.loadSlot(0)`: `release = releaseSlotCmd(classic)` becomes `release = tea.Batch(releaseSlotCmd(classic), m.prunePanes())`.
- `deactivateWorkspace`: `return releaseSlotCmd(slot), nil` becomes `return tea.Batch(releaseSlotCmd(slot), m.prunePanes()), nil`.
- `enterGlobalMode`: `cmds := []tea.Cmd{tea.RequestWindowSize, m.instanceChanged(), staleTerminals}` becomes `cmds := []tea.Cmd{tea.RequestWindowSize, m.instanceChanged(), staleTerminals, m.prunePanes()}`. In its final loop, replace
```go
		if slot == carried {
			cmds = append(cmds, releaseInstancesCmd(slot.list.GetInstances()))
			continue
		}
```
  with
```go
		if slot == carried {
			continue // its panes live on; prunePanes closes its agents' clients
		}
```

- [ ] **Step 3: Checkpoint: everything builds**

Run: `go build ./... && go vet ./...`
Expected: production code builds. The app tests compile once C6 is done.

### C6. app tests

- [ ] **Step 1: The test registry**

Create `app/testpanes_test.go`:
```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	"github.com/stretchr/testify/require"
)

// testPaneClients is the pane registry of the test that is running, and
// testInstanceClients the client each fixture instance was given. Every
// fixture that builds a home wires the registry in (wirePanes), and every
// fixture that attaches a client registers it here, so an instance built
// before its home still renders through it. Tests in this package run one
// at a time (none calls t.Parallel), so one registry at a time suffices.
var (
	testPaneClients     *ui.PaneClients
	testInstanceClients map[*session.Instance]*tmux.TmuxSession
)

// testPanes returns the running test's pane registry, creating it on first
// use. Its own clients (Ensure, Replace) attach through a fake PTY to an
// always-alive mock tmux, so no tmux server is reached.
func testPanes(t *testing.T) *ui.PaneClients {
	t.Helper()
	if testPaneClients == nil {
		p := ui.NewPaneClients()
		p.SetClientFactoryForTest(func(name, program string) *tmux.TmuxSession {
			return tmux.NewAttachClientWithDeps(name, program, fakePtyFactory{t: t}, aliveCmdExecForTest())
		})
		testPaneClients = p
		testInstanceClients = make(map[*session.Instance]*tmux.TmuxSession)
		t.Cleanup(func() { testPaneClients, testInstanceClients = nil, nil })
	}
	return testPaneClients
}

// attachTestClient registers, in the running test's registry, an attach
// client for inst's session. The client is attached through ptyFactory and
// runs its tmux commands on cmdExec, as ensurePane does at a load or a
// start. It returns the client.
func attachTestClient(t *testing.T, inst *session.Instance, ptyFactory tmux.PtyFactory, cmdExec cmd_test.MockCmdExec) *tmux.TmuxSession {
	t.Helper()
	panes := testPanes(t)
	c := tmux.NewAttachClientWithDeps(inst.Pane().TmuxSessionName(), inst.Program(), ptyFactory, cmdExec)
	require.NoError(t, c.Restore())
	panes.InjectForTest(c.SessionName(), c)
	testInstanceClients[inst] = c
	return c
}

// clientOf returns the client attachTestClient gave inst, even after the
// registry dropped it.
func clientOf(t *testing.T, inst *session.Instance) *tmux.TmuxSession {
	t.Helper()
	c := testInstanceClients[inst]
	require.NotNil(t, c, "fixture: %s was given no client", inst.Title)
	return c
}

// wirePanes points m, and every loaded slot's list and split pane, at the
// running test's registry, and returns m.
func wirePanes(t *testing.T, m *home) *home {
	t.Helper()
	m.panes = testPanes(t)
	for _, s := range m.openSlots() {
		if s.list != nil {
			s.list.SetPanes(m.panes)
		}
		if s.splitPane != nil {
			s.splitPane.SetPanes(m.panes)
		}
	}
	return m
}
```
Wire the home fixtures to it:
- `app/actions_test.go` `newTestHome`: `return h` becomes `return wirePanes(t, h)`.
- `app/overview_cursor_test.go` `fleetHome`: `return m` becomes `return wirePanes(t, m)`.
- `app/workspace_restore_test.go` `restoreModeHome`: `return m, statePath` becomes `return wirePanes(t, m), statePath`.

- [ ] **Step 2: New tests for the pane lifecycle**

Create `app/panes_test.go`:
```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnsureSlotPanes_AttachesActiveInstances: a workspace load attaches a
// client to every active session of the slot, and none to a paused one.
func TestEnsureSlotPanes_AttachesActiveInstances(t *testing.T) {
	m := newTestHome(t)
	live := liveInstance(t, "a-live")
	paused := liveInstance(t, "a-paused")
	require.NoError(t, paused.TransitionTo(session.Paused))
	m.list.AddInstance(live)
	m.list.AddInstance(paused)
	m.panes.Retain(nil) // the fixtures came with clients; start from none

	m.ensureSlotPanes(m.workspaceSlot)

	assert.True(t, m.panes.Alive(live.Pane().TmuxSessionName()))
	assert.Nil(t, m.panes.Get(paused.Pane().TmuxSessionName()), "a paused session gets no client")
}

// TestReplacePane_ClosesTheOldClientOffUpdate: a relaunched session gets a
// fresh client, and the old one is closed by the returned Cmd, not on the
// Update goroutine.
func TestReplacePane_ClosesTheOldClientOffUpdate(t *testing.T) {
	m := newTestHome(t)
	inst := liveInstance(t, "relaunched")
	m.list.AddInstance(inst)
	old := clientOf(t, inst)

	cmd := m.replacePane(inst)

	fresh := m.panes.Get(inst.Pane().TmuxSessionName())
	require.NotNil(t, fresh)
	assert.NotSame(t, old, fresh)
	assert.True(t, fresh.PtmxAlive())
	assert.True(t, old.PtmxAlive(), "closing it waits for the Cmd")
	drainCmd(cmd)
	assert.False(t, old.PtmxAlive())
}

// TestFullScreenAttach_PausesAndRestoresThePaneClient: the foreground
// attach takes the session from the client and gives it back.
func TestFullScreenAttach_PausesAndRestoresThePaneClient(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	inst := liveInstance(t, "fs")
	m.list.AddInstance(inst)
	name := inst.Pane().TmuxSessionName()
	require.True(t, m.panes.Alive(name))

	_, cmd := m.Update(startFullScreenAttachMsg{instance: inst, target: attachTargetAgent})
	require.NotNil(t, cmd, "the foreground attach runs as an ExecProcess")
	assert.False(t, m.panes.Alive(name), "the client lets go of the session for the foreground attach")
	assert.Same(t, inst, m.attachingInstance)

	_, _ = m.Update(attachDoneMsg{instance: inst})
	assert.True(t, m.panes.Alive(name), "and re-attaches when it returns")
	assert.Nil(t, m.attachingInstance)
}
```

- [ ] **Step 3: Migrate the fixtures**

Each instance fixture gets the lifecycle `Session`, plus a client wherever the old fixture attached one:
- `app/status_redetect_test.go` `startedInstanceWithProgram`:
```go
	inst.SetTmuxSession(tmux.NewSessionWithDeps(title, program, runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec))
	require.NoError(t, inst.Start(true))
	attachTestClient(t, inst, runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec)
	return inst
```
- `app/preview_tick_test.go` `startedInstanceWithHistoryTitled`: the same three lines, with program `"bash"`. In `TestPreviewTickRerendersScrolledAgent`, `inst.Pane().HasUpdated()` becomes `clientOf(t, inst).HasUpdated()` and `inst.Pane().GetContentHash()` becomes `clientOf(t, inst).GetContentHash()`.
- `app/slot_release_test.go`:
```go
// liveInstance builds a started, Running instance whose tmux session has a
// TUI attach client (a fake PTY) in the test's registry, as a workspace
// load leaves a live session. No tmux server is contacted.
func liveInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	// Paused data comes back started; its session is swapped for a
	// mock-backed one and the instance moved to Running.
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	inst.SetTmuxSession(tmux.NewSessionWithDeps(title, "claude", fakePtyFactory{t: t}, aliveCmdExecForTest()))
	require.NoError(t, inst.TransitionTo(session.Running))
	require.True(t, attachTestClient(t, inst, fakePtyFactory{t: t}, aliveCmdExecForTest()).PtmxAlive(), "fixture: the client is attached")
	return inst
}
```
```go
// assertReleased checks that dropped instances' clients leave the registry
// at once, keep their PTY until the returned Cmd runs and lose it once it
// has, and that the instances are no longer reachable.
func assertReleased(t *testing.T, m *home, cmd tea.Cmd, dropped ...*session.Instance) {
	t.Helper()
	for _, inst := range dropped {
		assert.Nil(t, m.panes.Get(inst.Pane().TmuxSessionName()), "%s: still registered after the drop", inst.Title)
		assert.True(t, clientOf(t, inst).PtmxAlive(), "%s: released on the Update goroutine; must wait for the Cmd", inst.Title)
	}
	drainCmd(cmd)
	refs := referencedInstances(m)
	for _, inst := range dropped {
		assert.False(t, clientOf(t, inst).PtmxAlive(), "%s: attach client still open after the drop", inst.Title)
		assert.NotContains(t, refs, inst, "%s: still reachable from the model", inst.Title)
	}
	require.NoError(t, m.checkSlotInvariant())
}
```
  Replace `TestReleaseInstancesCmd_OnlyAttachedLiveInstances` with:
```go
// TestPrunePanes_ReleasesOnlyInactiveSessions: prunePanes drops and closes,
// off the Update goroutine, the clients of sessions no loaded instance is
// active on, and leaves the rest attached.
func TestPrunePanes_ReleasesOnlyInactiveSessions(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	keep, gone := liveInstance(t, "keep"), liveInstance(t, "gone")
	m.list.AddInstance(keep)
	m.list.AddInstance(gone)
	require.NoError(t, gone.TransitionTo(session.Paused))
	assert.Nil(t, releaseSlotCmd(nil))

	cmd := m.prunePanes()
	require.NotNil(t, cmd)
	assert.True(t, clientOf(t, gone).PtmxAlive(), "building the Cmd must not close anything")
	assert.Nil(t, cmd(), "the release reports nothing back to Update")

	assert.False(t, clientOf(t, gone).PtmxAlive())
	assert.Nil(t, m.panes.Get(gone.Pane().TmuxSessionName()))
	assert.True(t, m.panes.Alive(keep.Pane().TmuxSessionName()))
}
```
  In `TestDroppedSlot_StaleProbeDoesNotReattach`, `require.False(t, live.Pane().PtmxAlive())` becomes `require.False(t, clientOf(t, live).PtmxAlive())`, and the final assertion becomes `assert.Nil(t, m.panes.Get(live.Pane().TmuxSessionName()), "a dropped instance must not be re-attached")`.
- `app/metadata_ptmx_repair_test.go` `setupPtmxDeadFixture`: `ts := tmux.NewTmuxSessionWithDeps(…)` becomes `ts := tmux.NewSessionWithDeps("a", "claude", fakePtyFactory{t: t}, aliveCmdExecForTest())`. The precondition becomes `require.False(t, m.panes.Alive(inst.Pane().TmuxSessionName()), "fixture precondition: no client attached")`. In both tests, `inst.Pane().PtmxAlive()` becomes `m.panes.Alive(inst.Pane().TmuxSessionName())`. The assertions keep their meaning: repaired, or not repaired mid full-screen attach.
- `app/liveness_unknown_test.go`: both `alive := m.applyLiveness(…)` become `alive, _ := m.applyLiveness(…)`, and `assert.False(t, inst.Pane().PtmxAlive(), …)` becomes `assert.False(t, m.panes.Alive(inst.Pane().TmuxSessionName()), …)`.
- In these files, `tmux.NewTmuxSessionWithDeps(` becomes `tmux.NewSessionWithDeps(`:
  - `app/workspace_terminal_restart_circuit_test.go`
  - `app/app_scripts_dispatch_test.go` (`addReadyInstance`)
  - `app/state_inline_attach_terminal_death_test.go` (`agentTs` only; `deadTermTs` stays a `TmuxSession`)
  - `app/workbench_review_test.go` (`aliveTmuxSessionForTest`, which now returns `*tmux.Session`)
- `app/flow_selection_test.go`, `startedWorktreeInstance`: build the session with `tmux.NewSessionWithDeps(…)`, drop the `RepairPtmx`/`PtmxAlive` lines, and fix its doc ("its preview client attached" becomes "no client: a start attaches none"). In `TestInstanceStarted_OwnerReopened`, with `late := tmux.ToLoomTmuxName("late")`:
  - "success…": `assert.True(t, m.panes.Alive(late), "it is displayed again, so it gets a client")`
  - "failure…": `assert.Nil(t, m.panes.Get(late), "nothing attaches a failed start")`
  - "a namesake…": `assert.Nil(t, m.panes.Get(late), "the start stays with its closed owner, so nothing attaches it")`
  - "a session the reopen killed…": `assert.Nil(t, m.panes.Get(late), "nothing attaches the dead start")`
- `app/async_owner_test.go`:
  - `TestResumeDone_AfterOwnerDropped`: the resumed instance has no client now. Use `resumed := liveInstance(t, "resumed")`, then `m.panes.Retain(nil)` to model "nothing attached it". After the `Update`, replace `assertReleased(t, m, cmd, resumed)` with `drainCmd(cmd); assert.Nil(t, m.panes.Get(resumed.Pane().TmuxSessionName()), "nothing displays it, so nothing attaches it")`. Retitle its doc: "the owner tab was closed while a resume ran; nothing displays the instance, so its completion attaches nothing".
  - `TestResumeDone_OwnerReopened`: `assert.True(t, m.panes.Alive(resumed.Pane().TmuxSessionName()), "displayed again, so it gets a client")`.
  - Replace `TestResumeFailed_AfterOwnerDroppedReleasesPreview` with:
```go
// TestResumeFailed_RevertsAndLeavesNoClient: a resume whose checkpoint save
// failed comes back as transitionFailedMsg and is reverted to Paused, so
// the user can retry. A paused session keeps no client, whether or not its
// owner is still loaded.
func TestResumeFailed_RevertsAndLeavesNoClient(t *testing.T) {
	isolateTmux(t)
	failedResume := func(inst *session.Instance) transitionFailedMsg {
		return transitionFailedMsg{inst: inst, title: inst.Title, op: "resume", previousStatus: session.Paused,
			err: errors.New("resume checkpoint save: disk full")}
	}
	for _, ownerClosed := range []bool{true, false} {
		m, _, _ := ownerTestHome(t)
		owner := m.workspaceSlot
		if ownerClosed {
			drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}}))
		}
		resumed := liveInstance(t, "resumed")
		owner.list.AddInstance(resumed)

		_, cmd := m.Update(failedResume(resumed))
		drainCmd(cmd)

		assert.Equal(t, session.Paused, resumed.GetStatus(), "reverted, so the user can retry (owner closed: %v)", ownerClosed)
		assert.Nil(t, m.panes.Get(resumed.Pane().TmuxSessionName()), "a paused session keeps no client (owner closed: %v)", ownerClosed)
		assert.False(t, clientOf(t, resumed).PtmxAlive(), "and the one it had is closed (owner closed: %v)", ownerClosed)
	}
}
```
- `app/workspace_restore_test.go`, around line 495: `assert.True(t, live.Pane().PtmxAlive(), "the same instance stays attached")` becomes `assert.Same(t, clientOf(t, live), m.panes.Get(live.Pane().TmuxSessionName()), "the same client stays attached")`.

Then run `CGO_ENABLED=0 go test ./app/... 2>&1 | grep -E '^(---|FAIL|ok)'`. A failure that remains is one of two things:
- a test building a bare `&home{…}` that renders or attaches an agent pane: add `wirePanes(t, m)` after it;
- a fixture that attached through the old `Instance.Start` or `RepairPtmx`: give it `attachTestClient`.

No assertion's meaning may change beyond the rewrites listed above. If one would have to, stop and report it.

### C7. Package C checkpoint and commit

- [ ] **Step 1: Full suite and race**

```bash
go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./app/... ./ui/... ./session/... ./script/...
```
Expected: all PASS.

- [ ] **Step 2: Format and commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') app/panes_test.go app/testpanes_test.go
git add -A session/ ui/ app/
git commit -m "feat: session lifecycle never attaches; the TUI owns its pane clients

session.Instance holds a lifecycle-only tmux.Session. The TUI attaches
one client per live agent session by name (ui.PaneClients) at loads,
completions, restarts and repairs, and releases them on the health tick,
kill, pause and slot drops.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Package D: Docs and verification

CLAUDE.md catches up with the split (its own commit), then the whole change is verified end to end.

### D1. CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: The `session/` bullet**

Replace
`Its own API is lifecycle and state; agent-pane I/O and probes (screen/scroll-back reads, keys, paste, mouse/focus forwarding, status probes, tmux/PTY liveness) live on the \`AgentPane\` value returned by \`Instance.Pane()\` (\`agent_pane.go\`), e.g. \`inst.Pane().SendKeys(…)\`.`
with
`Its own API is lifecycle and state. Its tmux session is a lifecycle-only \`tmux.Session\` that never attaches a client (see the pane-client gotcha). Whatever lifecycle does to the agent's pane without a client lives on the \`AgentPane\` value returned by \`Instance.Pane()\` (\`agent_pane.go\`): liveness probes, the session name, typing keys and prompts through \`send-keys\`, and a capture-pane \`Preview\`. For example, \`inst.Pane().SendPrompt(…)\`. Rendering the pane and forwarding input through a PTY go through the TUI's client (\`ui.Pane\`, from \`ui.PaneClients\`).`

- [ ] **Step 2: The `session/tmux/` bullet**

Replace its first sentence, `Tmux session management. Creates/attaches terminal sessions, captures pane content, detects prompts (surfaces a \`Prompting\` status so the user knows an instance needs attention), sends keystrokes.`, with:
`Tmux session management. \`Session\` (\`session.go\`) is a session as lifecycle sees it: \`Start\` launches without attaching and \`Close\` kills. It also covers liveness probes, capture-pane, \`DismissTrustPrompt\`, and \`TypeText\`/\`SendPrompt\` (text through \`load-buffer\` + \`paste-buffer -d -r\`, raw bytes with no length limit; never \`send-keys -l\`, which caps a command at about 16 KiB and parses a trailing \`;\`) plus \`PressKeys\` (key names through \`send-keys\`), pinned against a PTY write by a real-tmux parity test. \`TmuxSession\` embeds a \`Session\` and is an attach client. \`NewAttachClient\` attaches by name to a session it did not start (\`attach.go\`). \`NewTmuxSession\` plus \`Start\` both launch and attach, and only the terminal pane's shells use that path. Prompt detection (\`DetectStatus\`) surfaces a \`Prompting\` status so the user knows an instance needs attention.`

- [ ] **Step 3: Gotchas**

- In the `FromInstanceData` gotcha, replace `Callers that need a live PTY must call \`inst.EnsureRunning()\` explicitly (see \`session/reconcile.go\`).` with `\`inst.EnsureRunning()\` only marks a restored instance whose session runs as started. Nothing in \`session\` attaches a PTY (see the pane-client gotcha).`
- Add this new gotcha right after "**Pane updates are event-driven, not polled.**":

`- **Session lifecycle never attaches a tmux client; the TUI's pane clients do.** \`session.Instance\` holds a \`tmux.Session\` (launch, probe, \`send-keys\`, kill) and never a PTY. The TUI holds one attach client per live agent session in \`ui.PaneClients\` (\`home.panes\`), keyed by tmux session name, so two open workspaces sharing a title share one client. Every pane render, scroll, cursor, mouse event, paste and inline-attach key, and the status scrape too, goes through \`m.panes.For(inst)\` (\`ui.Pane\`). Never call a display method on anything the instance holds. Attach points live in \`app/panes.go\`. \`ensureSlotPanes\` runs at every workspace load. \`replacePane\` runs when a start, resume or recover lands in a loaded slot, and after a workspace terminal's auto-restart, because the old client was watching the session it replaced. \`ensurePane\` handles the health tick's repair and a returning full-screen attach. Releases go through \`prunePanes\`, which \`Retain\`s the active instances' session names. It runs on the health tick, on kill and pause, after a failed transition and at every slot drop. Attaches run on Update; releases close the PTY off it (\`releaseClientsCmd\`). A released client is never re-attached: \`Retain\` removes it first, and \`Ensure\` builds a new one. A completion whose owner was closed attaches nothing. This split is daemon stage 1A: a \`loom serve\` that attached its own clients would fight the TUI's over the window size.`

- In the focused-slot gotcha, replace `Dropping a slot (closing a tab, the classic slot giving way to the first tab, \`enterGlobalMode\` dropping the tabs or a classic workspace slot) must release its instances' preview attach clients, or reopening the workspace double-attaches every live session: each drop site returns \`releaseSlotCmd\`, which also detaches the attach clients its terminal pane holds on \`loom_term_*\` shells` with `Dropping a slot (closing a tab, the classic slot giving way to the first tab, \`enterGlobalMode\` dropping the tabs or a classic workspace slot) must release the clients it no longer needs. Each drop site returns two Cmds: \`prunePanes\`, which closes the pane clients of sessions no loaded instance is active on, and \`releaseSlotCmd\`, which detaches the attach clients its terminal pane holds on \`loom_term_*\` shells`. In the same gotcha, replace `so a stale tick can't \`RepairPtmx\` a released one` with `so a stale tick can't re-attach a released one`.
- In the destructive-actions gotcha, replace the sentence that starts `An instance whose owner was closed while it started or resumed has attached a preview client after \`releaseSlotCmd\` ran, so its completion releases it (a resume whose checkpoint save failed after attaching too: \`transitionFailedMsg\` releases an instance no loaded slot holds, before reverting it to Paused) — unless its workspace was reopened meanwhile:` with `A start or resume whose owner was closed meanwhile attaches no pane client, since nothing displays it, unless its workspace was reopened meanwhile:`. Keep the rest of that sentence (`then it swaps itself in for the reopened slot's copy of its record…`).

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: CLAUDE.md for the pane split (daemon stage 1A)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### D2. Verification

- [ ] **Step 1: Static checks and the full suite**

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')   # expect no output
go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
```
Expected: no gofmt output, and every package PASSes.

- [ ] **Step 2: End-to-end suite**

Run: `go test -tags e2e ./e2e/...`
Expected: PASS, including `TestE2E_SessionSurvivesRestart` (reattach after a restart now goes through the registry) and `TestE2E_FakeClaudeHooksDriveStatus`.

- [ ] **Step 3: Sandbox smoke (loom-dev skill)**

Run `go run ./tools/loomdev up`, then `start`, then confirm each of the following. Capture a `shot` wherever a check names one:
1. A new session (`n`, fake agent) renders both its pane and its card tail.
2. **One client per agent session:** `go run ./tools/loomdev env` gives the socket. `tmux -L <socket> list-clients -F '#{session_name}'` should list each `loom_<title>` exactly once.
3. Inline attach (`i`): typed keys echo in the agent. Leave with `ctrl+q`.
4. Full-screen attach (`alt+a`) and back (`C-q`): the pane renders again.
5. `N` with a prompt: the prompt arrives (via send-keys) and the session leaves Ready.
6. Pause (`s`), then resume (`r`): the pane renders the relaunched agent.
7. Kill (`D`): the session's client disappears from `list-clients`.
8. Open a second workspace (`W`), switch tabs with `l`/`;`, then close it. Its sessions' clients leave `list-clients` within one health tick (3s).

Finish with `go run ./tools/loomdev down`.

- [ ] **Step 4: Report**

Report to the user the test totals, the e2e result, the outcome of the eight smoke checks, and any deviation from this plan with its reason.
