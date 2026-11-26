# Loom web terminal, part 1: the daemon side — Implementation Plan

> **For agentic workers:** this plan runs supervised: `/flow:draft` writes the stories from this skeleton, then `/flow:run` runs them story by story. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a paired device's browser reach a session's worktree shell through the daemon: a host-side switch, a Core request that makes the shell, a pty attach client in the daemon, a WebSocket route, and a bare `terminal.html` that shows the shell, so the owner can check the transport on the iPhone before the app's panel is designed.

**Architecture:** The model gains a `terminal` flag in `web.json` (`Core.SetWebTerminal`, published in `WebView`, set from `loom web terminal` and a settings row) and `Core.EnsureTerminal`, which starts the session's `term_<title>` tmux shell as a job and answers with its tmux name. `session/tmux` gains `ShellAttach`, a `tmux attach` in a pty with no emulator or status state. The web layer gains a part that upgrades `GET /api/v1/sessions/{id}/terminal` to a WebSocket (`github.com/coder/websocket`), pumps the attach's bytes through the alt-screen filter with a bounded buffer, and ends its connections when the switch turns off, a device is revoked, the listener rebinds or the layer stops. The app's build gains a second HTML entry, `terminal.html`, served under a CSP of its own, holding xterm.js and the socket. No `rpc.Protocol` bump: two methods, one Reply field and one view field are added.

**Tech Stack:** Go (testify, `creack/pty`, `github.com/coder/websocket` v1.8.15, tmux 3.x on a private server in tests); TypeScript, Vite 8, Vitest with happy-dom, `@xterm/xterm` 6.0.0, `@xterm/addon-webgl` 0.19.0 and `@xterm/addon-fit` 0.11.0, each pinned exactly.

**Spec:** `docs/superpowers/specs/2026-11-26-web-terminal-design.md`

**Part:** 1 of 2

**Budget:** draft $52; run $28 (estimate 2026-11-26)

## File structure

The model (`core/`):

- `core/iface.go`, `core/loop_core.go` (changed: the two methods and their forwarders); `core/web.go`, `core/web_store.go` (changed: the flag, its setter, the file's key, the view's field); `core/terminal.go` (new: `EnsureTerminal`, its job and its delivery); `core/requests.go`, `core/model.go`, `core/events.go`, `core/events_json.go` (changed: the op, its precondition, the Reply's field on the wire).
- `core/rpc/methods_gen.go`, `docs/specs/protocol.md` (regenerated, never edited).
- `*_test.go` beside each.

tmux (`session/tmux/`):

- `shell_attach.go` (new: `ShellAttach`, `StartShellAttach`, `ShellHistory`), `shell_attach_test.go`, `shell_attach_realtmux_test.go`; `CLAUDE.md`, `README.md` (changed).

The host's switches:

- `internal/cli/web.go`, `internal/cli/web_test.go` (changed: `loom web terminal`, the status row).
- `ui/overlay/settingsOverlay.go`, `app/state_settings.go`, `app/intents.go`, `app/core_glue.go` and their tests (changed: the `Web Terminal` row).

The web layer (`internal/web/`):

- `fleet.go`, `stream.go` (changed: the snapshot's field, the `terminal` event).
- `terminal_conn.go` (new: one connection's frames, pump, pings and endings); `terminal.go` (new: the route, the limits, the part that ends connections); `routes.go`, `web.go` (changed: the route's entry, the part's start and stop).
- `static.go` (changed: `terminalCSP` on `/terminal.html`).
- `api.fields`, `ui/src/api/gen.ts`, `docs/specs/web-api.md` (regenerated).
- `go.mod`, `go.sum`, `vendor/` (the dependency).

The app (`internal/web/ui/`):

- `src/api/stream.ts`, `src/store/fleet.ts` (changed: the `terminal` event).
- `terminal.html` (new), `src/terminal/main.ts`, `src/terminal/socket.ts`, `src/terminal/terminal.css` (new); `vite.config.ts`, `package.json`, `pnpm-lock.yaml`, `public/sw.js` (changed).
- `internal/web/dist/` (rebuilt by every story that changes the app, committed).

Elsewhere: `tools/webshot/fixture/snapshot.json` (the new field); `e2e/web_terminal_test.go` (new); `USAGE.md`, `internal/web/README.md`, `internal/web/CLAUDE.md`, `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `docs/ARCHITECTURE.md` (the rules and docs).

## Names contract

Every name one story defines and another uses. A story may add names of its own; a name another story needs and this section lacks goes back to the manager.

**The switch (Story 1)**, in `core/`:

- `WebView` gains the field `Terminal bool` (Story 1), in `core/web.go`: true while the web terminal is on.
- The `core.Core` interface gains `SetWebTerminal(on bool) error` (Story 1), a request with no `rpc:` annotation, declared after `RevokeDevice`; `func (m *Model) SetWebTerminal(on bool) error` (Story 1) and the generated `func (c *Client) SetWebTerminal(on bool) error` (Story 1) answer it. It never changes `WebView.Rev`.
- `web.json` gains the key `terminal` (Story 1): a boolean, left out while false.

**The shell (Story 2)**, in `core/`:

- The `core.Core` interface gains `EnsureTerminal(id InstanceID, req ReqID)` (Story 2), a request with no `rpc:` annotation; the generated `func (c *Client) EnsureTerminal(id core.InstanceID, req core.ReqID)` (Story 2) sends it.
- `Reply` gains the field `Terminal string` (Story 2), in `core/events.go`: the shell's tmux session name, `tmux.ToLoomTmuxName(tmux.TerminalSessionName(title))`, set only on `EnsureTerminal`'s success.
- A refusal's `Reply.Err` matches `core.ErrRefused` (Story 2), so the web layer's `modelStatus` answers it 409; a session of the shell's name that did not start in the instance's worktree, and a tmux that cannot answer, are refusals too (Story 2), as is an instance that is gone or no longer started and active when the answer is delivered (Story 2). A shell another client made while the job ran is a success when it started in the instance's worktree (Story 2).

**The attach (Story 3)**, in `session/tmux/shell_attach.go`:

- `type ShellAttach struct` (opaque) (Story 3): one `tmux attach-session` client in a pty.
- `func StartShellAttach(name string, cols int, rows int, ptyFactory PtyFactory) (*ShellAttach, error)` (Story 3): `name` is the tmux session name as `Reply.Terminal` gives it; the pty is set to the given size as soon as the client has started, as the TUI's attach does, and tmux redraws to it.
- `func (a *ShellAttach) Read(p []byte) (int, error)` (Story 3): the client's raw output, unfiltered; `io.EOF` or an error once the client has ended (the session gone, or `Close`).
- `func (a *ShellAttach) Write(p []byte) (int, error)` (Story 3): input to the pane.
- `func (a *ShellAttach) Resize(cols int, rows int) error` (Story 3).
- `func (a *ShellAttach) Close() error` (Story 3): ends the client, never the session; a blocked `Read` returns; safe to call twice.
- `func ShellHistory(name string, cmdExec internalexec.Executor) ([]byte, error)` (Story 3): the rows above the pane's visible screen, at most `paneScrollbackLines`, with escapes, each row ended by `\r\n`, and a non-empty history ended by the SGR reset `\x1b[0m`; empty with no history.

**The host's switches (Story 4):**

- `loom web terminal on` and `loom web terminal off` (Story 4), printing `The web terminal is on.`, `The web terminal is off.`, `The web terminal is already on.` or `The web terminal is already off.`.
- `loom web status` gains the row `Terminal:` with `on` or `off` (Story 4).
- The settings row's label is `Web Terminal` (Story 4), its values `on (all workspaces)` and `off (all workspaces)`.

**The snapshot and the event (Story 5)**, in `internal/web/`:

- `snapshotJSON` gains the field `Terminal bool` tagged `json:"terminal"` (Story 5), in `fleet.go`; the generated `Snapshot.terminal: boolean` (Story 5), in `ui/src/api/gen.ts`.
- `type terminalStateJSON struct { Enabled bool }` (Story 5), in `fleet.go`, its field tagged `json:"enabled"`; the generated `interface TerminalState { enabled: boolean }` (Story 5).
- The stream event `terminal` with the payload `terminalStateJSON` (Story 5), sent to every stream when the switch changes; the app's store sets the snapshot's `terminal` from it (Story 5), in `ui/src/store/fleet.ts`.

**One connection (Story 6)**, in `internal/web/terminal_conn.go`:

- `type termShell interface { Read(p []byte) (int, error); Write(p []byte) (int, error); Resize(cols int, rows int) error; Close() error }` (Story 6): what a connection needs of an attach; `*tmux.ShellAttach` satisfies it.
- `type termOpen func(cols int, rows int) (termShell, []byte, error)` (Story 6): opens the attach at a size and returns the history to send before its output. The connection sends a non-empty history, then `rows` line feeds (neither with no history), then the `live` frame, before its pump starts and outside `termOutBuffer`, in binary frames of at most `termFrameMax`.
- `type termEnd struct { code websocket.StatusCode; reason string }` (Story 6): a close frame's code and reason.
- The endings (Story 6), each a `termEnd` variable: `termEndOff` (4001, `the terminal is off on this host`), `termEndRevoked` (4002, `this device is no longer paired`), `termEndSession` (4003, `the session was paused or killed`), `termEndExited` (4004, `the shell exited`), `termEndSlow` (4005, `the browser fell behind`), `termEndBadFrame` (4006, `a frame loom does not understand`), `termEndNoAttach` (4007, `loom could not attach to the shell`), `termEndStopping` (1001, `loom is stopping`).
- `type termConn struct` (opaque) (Story 6): one upgraded connection.
- `func recordHijack(w http.ResponseWriter) (http.ResponseWriter, func() net.Conn)` (Story 6): a writer to hand `websocket.Accept` in place of `w`, and a function that gives the connection `Accept` hijacked through it (nil before the hijack).
- `func newTermConn(ws *websocket.Conn, raw net.Conn, open termOpen, ended func() termEnd, recoverPanic func(where string)) *termConn` (Story 6): `raw` is the hijacked connection under `ws` (Story 7 passes `recordHijack`'s); the connection closes it outright when its close handshake has not finished `termCloseWait` after loom's close frame, so `run` returns within that bound whatever the browser sends or withholds. `ended` is asked once, when the attach's output ends by itself, for the ending to send (`termEndExited` or `termEndSession`); `recoverPanic` is deferred first in every goroutine the connection starts (Story 7 passes the layer's).
- `func (c *termConn) run(ctx context.Context) termEnd` (Story 6): serves the connection until it ends and returns the ending it sent; an ending with no close frame from loom (the browser left, a network error, a ping unanswered) is the zero `termEnd`. Before it returns it has closed the WebSocket and the attach, so the route closes neither.
- `func (c *termConn) end(e termEnd)` (Story 6): ends the connection with `e`; never blocks, safe from any goroutine and more than once, the first ending winning; called before `run`, it makes `run` send that ending at once and open no attach.
- The frames (Story 6): binary frames carry terminal bytes both ways; the app's text frame `{"resize":{"cols":120,"rows":40}}`, the first of which opens the attach; the daemon's text frame `{"state":"live"}` once attached.
- The bounds (Story 6), constants in `terminal_conn.go`: `termFrameMax = 64 << 10`, `termMinCols = 2`, `termMaxCols = 500`, `termMinRows = 1`, `termMaxRows = 200`, `termPingEvery = 20 * time.Second`, `termPongWait = 10 * time.Second`, `termOutBuffer = 1 << 20`, `termCloseWait = 5 * time.Second`. The frame cap is the connection's own: it lifts the library's read limit above `termFrameMax` and ends a longer frame with `termEndBadFrame`, never with the library's 1009.

**The route (Story 7)**, in `internal/web/terminal.go`:

- The route `GET /api/v1/sessions/{id}/terminal` (Story 7), a `page` entry with log `terminal`, client `terminal` and ok 101; the generated `terminalURL(id: number): string` (Story 7) on the app's `api`, in `ui/src/api/gen.ts`, which returns the path (`/api/v1/sessions/<id>/terminal`), as `mockupURL` does. The entry's doc says the route is a WebSocket upgrade.
- `const maxTerminalsPerDevice = 4` and `const maxTerminals = 16` (Story 7).
- The refusals before the upgrade (Story 7), each a JSON error: 403 `the request does not come from loom's origin`; 403 `the terminal is off on this host`; 429 `too many open terminals`; and the model's refusal as 409. It also answers 426 for a request that is not a WebSocket upgrade and 400 for a handshake without version 13 or one 16-byte key, both before anything else, 404 for an unknown session or a workspace terminal, 503 while loom is stopping, 504 when the model does not answer in time, and 500 for a reply that names no shell.
- The log lines (Story 7): `web.terminal_open` and `web.terminal_close`, each with `device` and `session`, the close with `reason`.

**The page (Story 8):**

- `const terminalCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"` (Story 8), in `internal/web/static.go`: set on `/terminal.html` and on no other path.
- The page `/terminal.html#<session id>` (Story 8), built from `internal/web/ui/terminal.html` and `internal/web/ui/src/terminal/main.ts`: it reads the session's id from the hash and connects to the URL Story 7's `terminalURL` builds for it, on the page's own host, `wss:` on an `https:` page and `ws:` otherwise.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): `core/iface.go`, `core/loop_core.go`, `core/rpc/methods_gen.go` and `docs/specs/protocol.md` as Story 1 leaves them (Story 2); `internal/web/ui/src/api/gen.ts`, `internal/web/CLAUDE.md` and `docs/claude/web-layer.md`'s "Details moved out of CLAUDE.md" section as Story 7 leaves them (Story 8); Story 6's test helpers in `internal/web/terminal_conn_test.go` as it leaves them (Story 7).

**For every story:** each test's `Fails without:` line names a change to the code that the test, as written, really catches when run. A guard is pinned on both sides: the case it spares and the ordinary case it must still act on. No test touches the real `~/.loom`, daemon or tmux server: a real-tmux test runs on the package's private-server helper with a short socket path. Builds, test runs, `pnpm` and `go generate` go through `loom job`.

**Design stage:** /flow:draft session 5633abf4-ee78-419f-af7d-422a36e2c2d9, 2026-11-26T13:09:46Z to 2026-11-26T14:48:19Z UTC: $51.57 logged, $51.82 with estimated thinking; 9571 plan lines, $5.39 per 1,000 lines; skeleton review $2.61; risk review $12.74; budget $52; forms: full 7 stories $31.87, brief 2 stories $4.36; loom flow roster 0e25f6995f89.

## Stories

- **Story 1** — The web terminal's switch in the model.

  `web.json` gains `terminal`, the model's web state and `WebView` carry it, and `Core.SetWebTerminal` sets it and writes the file, as the quick replies' setter does: refused while `web.json` is left alone, the state restored when the write fails, nothing written when the value does not change, and `WebView.Rev` never bumped, since a changed `Rev` rebinds the listener and ends every stream. The method is forwarded by the loop, generated into the wire client and written into the protocol reference.

  - **Developer:** intermediate-dev
  - **Files:** `core/iface.go`, `core/loop_core.go`, `core/web.go`, `core/web_store.go`, `core/web_test.go`, `core/web_store_test.go`, `core/state_views_test.go`, `core/rpc/methods_gen.go`, `core/rpc/client_test.go`, `docs/specs/protocol.md`
  - **Interfaces:** exposes `WebView.Terminal`, `Core.SetWebTerminal` and the `terminal` key to Stories 2, 4, 5 and 7; uses none
  - **Verify:** `CGO_ENABLED=0 go test ./core/ ./core/rpc/`
  - **Review:** deep
  - **Form:** full (a store write with a restore on failure, and the revision it must not bump)
  - **Reading:** spec §4, §5, §8; `docs/claude/adding-a-core-method.md`; `core/web.go:20-60,340-400`; `core/web_store.go:25-130,160-265,370-412`; `core/web_host.go:296-330`; `core/quick_replies_test.go`; `core/loop_core.go:205-220`

- **Story 2** — `EnsureTerminal`: the model makes a session's shell.

  A request naming an instance: admitted through a precondition that refuses an instance that is unstarted, paused, loading, being deleted, recoverable, a workspace terminal or without a worktree, then a job that starts `$SHELL` (else `/bin/sh`) in the worktree as the tmux session `tmux.TerminalSessionName(title)` when it does not exist, through the lifecycle's `tmux.Session` (`NewSession` and `Start`, which attach no client), and a Reply whose `Terminal` is the session's tmux name. A session of that name that exists is the instance's own only when it started in the instance's worktree, as pause and kill judge it: one held elsewhere, or a tmux that cannot answer, is a refusal. `tmux.ErrSessionExists` from the start is a success that started nothing; any other error after the launch leaves the shell possibly started. On delivery the model looks the instance up again by the id the result carries: when the job started a shell and the instance is gone or no longer started and active, it closes that shell by its exact name in a job of its own, which first confirms the session still starts in that worktree, and the Reply is an error; a possibly started shell counts as started when it is alive and starts there. A shell that existed before the request is never closed here. The Reply's new field crosses the wire.

  - **Developer:** intermediate-dev
  - **Files:** `core/terminal.go`, `core/terminal_test.go`, `core/iface.go`, `core/loop_core.go`, `core/requests.go`, `core/requests_test.go`, `core/model.go`, `core/events.go`, `core/events_json.go`, `core/rpc/methods_gen.go`, `core/rpc/wire_test.go`, `docs/specs/protocol.md`, `core/README.md`, `session/reconcile.go`, `session/reconcile_foreign_test.go`, `session/README.md`
  - **Interfaces:** exposes `Core.EnsureTerminal` and `Reply.Terminal` to Stories 7 and 9; uses Story 1's `core/iface.go`, `core/loop_core.go`, `core/rpc/methods_gen.go` and `docs/specs/protocol.md` as it leaves them
  - **Verify:** `CGO_ENABLED=0 go test ./core/ ./core/rpc/`
  - **Review:** deep
  - **Form:** full (a job racing a kill or pause, and a tmux session closed on delivery)
  - **Reading:** spec §9, §10, §11; `docs/claude/adding-a-core-method.md`; `core/job_log.go`; `core/requests.go:20-330`; `core/model.go:335-400,465-570`; `core/ops.go:30-130,195-300`; `core/completions.go:255-300`; `core/view.go:160-185`; `core/events.go:205-255`; `core/events_json.go:25-74`; `core/seams.go`; `ui/terminal.go:357-428`; `session/tmux/tmux.go:160-270`; `session/tmux/session.go:125-200,590-625`; `session/reconcile.go:200-320`; `session/CLAUDE.md`
  - **Drafts after:** Story 1

- **Story 3** — `ShellAttach`: a pty attach client with nothing else.

  `StartShellAttach` runs `tmux attach-session` on the exact target in a pty from the package's `PtyFactory`, sized as soon as the client has started, and returns a value that reads, writes, resizes and closes; it holds no emulator, monitor or status state, and `Close` ends the client and never the session. `ShellHistory` captures the rows above the visible screen, bounded by `paneScrollbackLines`. Tests run against a pty pair, and one real-tmux test on the private server attaches, types, reads the echo, resizes and finds the session alive after `Close`. The package's rules gain: `ShellAttach.Close` never closes the session.

  - **Developer:** intermediate-dev
  - **Files:** `session/tmux/shell_attach.go`, `session/tmux/shell_attach_test.go`, `session/tmux/shell_attach_realtmux_test.go`, `session/tmux/CLAUDE.md`, `session/tmux/README.md`
  - **Interfaces:** exposes `ShellAttach`, `StartShellAttach` and `ShellHistory` to Story 7; uses none
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./session/tmux/ && GOOS=windows CGO_ENABLED=0 go build ./...'`
  - **Review:** deep
  - **Form:** full (a pty, a child process and a read that must end on close)
  - **Reading:** spec §12, §13, §14; `session/tmux/CLAUDE.md`; `session/tmux/attach.go`; `session/tmux/pty.go`; `session/tmux/pty_unix.go`; `session/tmux/pty_windows.go`; `session/tmux/command.go:60-120`; `session/tmux/target.go`; `session/tmux/tmux.go:340-360,780-805`; `session/tmux/session.go:735-808`; `session/tmux/scrollback.go`; `session/tmux/attached_test.go:10-50`; `session/tmux/realtmux_helpers_test.go`; `session/tmux/attach_test.go:95-126`; `session/tmux/target_enforce_test.go`

- **Story 4** — The host's switches: `loom web terminal` and the settings row.

  `loom web terminal on|off` reads the view, says when nothing changes, else sends `SetWebTerminal` and prints the model's refusal as it stands; `loom web status` gains its `Terminal:` row. The TUI's settings gain a `Web Terminal` row after Job Borrowing, a global setting like it: Enter raises a request the app answers with `SetWebTerminal`, the row is seeded when the overlay opens and refreshed on `ModelChanged`. `USAGE.md` documents both.

  - **Developer:** intermediate-dev
  - **Files:** `internal/cli/web.go`, `internal/cli/web_test.go`, `ui/overlay/settingsOverlay.go`, `ui/overlay/settingsOverlay_test.go`, `app/state_settings.go`, `app/state_settings_test.go`, `app/intents.go`, `app/core_glue.go`, `internal/cli/README.md`, `internal/cli/CLAUDE.md`, `USAGE.md`
  - **Interfaces:** uses Story 1's `Core.SetWebTerminal` and `WebView.Terminal`; exposes `loom web terminal on` and its texts to Story 9
  - **Verify:** `CGO_ENABLED=0 go test ./internal/cli/ ./ui/overlay/ ./app/`
  - **Review:** light (it also shifts the Up-press counts of two existing test helpers, `editBuildSweep` and `toggleBorrow`, by the new row)
  - **Form:** brief (plumbing a flag through a command and a row that copy existing ones)
  - **Reading:** spec §6; `internal/cli/web.go:28-235`; `internal/cli/web_test.go:20-110,350-446`; `internal/cli/CLAUDE.md`; `ui/overlay/settingsOverlay.go:14-115,205-225,325-345,455-470`; `ui/overlay/settingsOverlay_test.go:190-232`; `app/state_settings.go`; `app/state_settings_test.go:170-270`; `app/intents.go:462-476`; `app/core_glue.go:160-185`; `USAGE.md:335-342,930-950,1405-1420`
  - **Drafts after:** Story 1

- **Story 5** — The snapshot's field and the `terminal` event.

  The stream's hub follows `WebView.Terminal`: the snapshot carries `terminal`, and a change is sent to every stream as the `terminal` event. The contract is regenerated (`gen.ts`, `web-api.md`, `api.fields`), the app's stream knows the event and its store sets the snapshot's flag from it, webshot's fixture snapshot gains the field, and the bundle is rebuilt. Nothing in the app draws it yet.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/fleet.go`, `internal/web/stream.go`, `internal/web/stream_test.go`, `internal/web/fleet_test.go`, `internal/web/api.fields`, `docs/specs/web-api.md`, `internal/web/README.md`, `internal/web/ui/src/api/gen.ts`, `internal/web/ui/src/api/stream.ts`, `internal/web/ui/src/api/stream.test.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/store/startFleet.test.ts`, `tools/webshot/fixture/snapshot.json`, `internal/web/dist/**`
  - **Interfaces:** exposes `snapshotJSON.Terminal`, `terminalStateJSON`, the `terminal` event and the store's flag to part 2; uses Story 1's `WebView.Terminal`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/webshot/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
  - **Review:** light
  - **Form:** brief (a field and an event added by the web layer's documented procedure)
  - **Reading:** spec §7; `docs/claude/web-layer.md`; `internal/web/fleet.go:170-230,520-545`; `internal/web/stream.go:105-200,355-390,520-570`; `internal/web/CLAUDE.md`; `internal/web/ui/src/api/stream.ts:1-60`; `internal/web/ui/src/store/fleet.ts`; `internal/web/ui/CLAUDE.md`
  - **Drafts after:** Story 1

- **Story 6** — One terminal connection: frames, pump, pings and endings.

  The dependency arrives (`go get github.com/coder/websocket@v1.8.15`, `go mod vendor`), and `termConn` serves one upgraded connection: the first resize frame opens the attach through `termOpen`, the history and `rows` line feeds go out first, outside the buffer and in frames of at most `termFrameMax`, then the attach's output through `vt.NewAltScreenFilter` as binary frames, with the app's binary frames written to the attach and later resizes applied. Output passes through a bounded buffer, so a browser that falls behind is ended (`termEndSlow`) and the attach is never blocked on the network. A bad size, an oversized frame, unreadable JSON or bytes before the first resize end it (`termEndBadFrame`); a failed attach ends it (`termEndNoAttach`); a ping unanswered for `termPongWait` drops it; the attach's own end asks `ended`; `end` from any goroutine wins once. Every path closes the attach. Tests run a real WebSocket pair over `httptest` against a fake `termShell`.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/terminal_conn.go`, `internal/web/terminal_conn_test.go`, `go.mod`, `go.sum`, `vendor/**`
  - **Interfaces:** exposes `termShell`, `termOpen`, `termEnd`, the endings, `termConn`, `newTermConn`, `run`, `end` and the bounds to Story 7; uses none
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ && CGO_ENABLED=0 go build ./... && GOOS=windows CGO_ENABLED=0 go build ./...'`
  - **Review:** deep
  - **Form:** full (concurrency between two pumps, a bounded buffer and an outside connection)
  - **Reading:** spec §13, §16, §17, §18, §19; `session/vt/altfilter.go`; `internal/web/stream.go:19-110,575-650`; `internal/web/CLAUDE.md`; `internal/web/web.go:30-130`

- **Story 7** — The route and the terminals' part.

  A `page` entry in the route table answers `GET /api/v1/sessions/{id}/terminal`: it checks for exactly one accepted `Origin` and the session (404 for an unknown one or a workspace terminal), reserves a slot under the part's lock (refused when the part is closed, the switch is off by the part's own copy, the device is unknown, or the counts are full), asks the model for the shell (`EnsureTerminal` through the layer's reply wait, a refusal answered 409), then upgrades with the library's own origin check off and runs a `termConn` whose `termOpen` is `tmux.ShellHistory` and `tmux.StartShellAttach` and whose `ended` asks the replica whether the session is still started and active. The part counts slots per device and in total, logs each open and close and never the bytes, and, as a sink, ends slots and connections with the right ending when the switch turns off or a device is revoked or paired again; a rebind and the layer's stop end them all, before the listener closes. A slot ended while its request waited closes with that ending as soon as it has upgraded, and every refusal releases its slot. Its goroutines recover panics as the layer's others do. The contract is regenerated, `internal/web/CLAUDE.md` makes room by moving supporting context of existing rules into `docs/claude/web-layer.md` and gains the route's rules, and the guide gains "adding a WebSocket route".

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/terminal.go`, `internal/web/terminal_test.go`, `internal/web/routes.go`, `internal/web/routes_test.go`, `internal/web/web.go`, `internal/web/server.go`, `internal/web/api.fields`, `docs/specs/web-api.md`, `internal/web/ui/src/api/gen.ts`, `internal/web/dist/**`, `internal/web/CLAUDE.md`, `internal/web/README.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `docs/ARCHITECTURE.md`
  - **Interfaces:** exposes the route and `api.terminalURL` to Stories 8 and 9; uses Story 2's `Core.EnsureTerminal` and `Reply.Terminal`, Story 3's `StartShellAttach` and `ShellHistory`, Story 1's `WebView.Terminal`, and Story 6's `termConn`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
  - **Review:** deep
  - **Form:** full (an origin check and an auth boundary on a route that executes commands, and connections ended from other goroutines)
  - **Reading:** spec §8, §15, §16, §18, §20, §21, §29, §30; `internal/web/CLAUDE.md`; `docs/claude/web-layer.md`; `internal/web/routes.go:20-250,340-390`; `internal/web/web.go:28-130,200-265`; `internal/web/server.go:24-215`; `internal/web/auth.go:20-100`; `internal/web/api.go:36-110`; `internal/web/reads.go:185-240`; `internal/web/mockup.go`; `internal/web/stream.go:105-250,310-330,420-545`
  - **Drafts after:** Story 6

- **Story 8** — The bare `terminal.html`.

  The app's build gains a second HTML entry whose script creates an xterm.js terminal with the WebGL addon loaded before `open()` (the DOM renderer when WebGL is unavailable or its context is lost) and the fit addon sizing it to the window, reads the session's id from the hash, opens the WebSocket, sends its size, writes binary frames into the terminal and its keys back in binary frames of at most 64 KiB (a paste is split), and shows a close's reason in the page; no reconnecting, no panel, no phone input. The web layer answers `/terminal.html`, and only it, with `terminalCSP`; the service worker passes a request for `/terminal.html` through to the network; the page carries no inline script and no `loom-build` meta. Go tests pin the header on that path and the app's on every other, and that nothing the app's entry loads holds xterm.js; Vitest covers the socket with a fake WebSocket. `internal/web/CLAUDE.md` and `internal/web/ui/CLAUDE.md` gain the rules.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/terminal.html`, `internal/web/ui/src/terminal/main.ts`, `internal/web/ui/src/terminal/socket.ts`, `internal/web/ui/src/terminal/socket.test.ts`, `internal/web/ui/src/terminal/terminal.css`, `internal/web/ui/vite.config.ts`, `internal/web/ui/package.json`, `internal/web/ui/pnpm-lock.yaml`, `internal/web/ui/public/sw.js`, `internal/web/static.go`, `internal/web/static_test.go`, `internal/web/static_budget_test.go`, `internal/web/dist/**`, `internal/web/CLAUDE.md`, `internal/web/ui/CLAUDE.md`, `internal/web/README.md`, `docs/claude/web-layer.md`
  - **Interfaces:** exposes `/terminal.html` and `terminalCSP` to part 2; uses Story 7's `api.terminalURL` and Story 6's frames and endings
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
  - **Review:** deep
  - **Form:** full (a CSP relaxed on one path only, and a service worker's fetch handler)
  - **Reading:** spec §17, §23, §24, §29; `internal/web/static.go`; `internal/web/static_test.go:95-160,215-250`; `internal/web/static_budget_test.go`; `internal/web/mockup.go:10-45`; `internal/web/ui/vite.config.ts`; `internal/web/ui/index.html`; `internal/web/ui/public/sw.js`; `internal/web/ui/src/styles/app.css:1-80`; `internal/web/ui/src/styles/fonts.css`; `internal/web/ui/scripts/check-dist.mjs`; `internal/web/ui/CLAUDE.md`
  - **Drafts after:** Story 7

- **Story 9** — End to end: a WebSocket client reaches a session's shell.

  One `e2e` test over a real daemon on a private tmux server: the web enabled on a loopback port, a device paired, a session started; with the switch off the route answers 403; after `loom web terminal on` a WebSocket client with the device's cookie and the layer's origin sends a resize, types an `echo` whose output differs from its own text (a marker computed by the shell) and a carriage return, and reads the marker back from the binary frames; the session's `term_` shell exists on the tmux server afterwards.

  - **Developer:** intermediate-dev
  - **Files:** `e2e/web_terminal_test.go`, `e2e/CLAUDE.md`
  - **Interfaces:** uses Story 4's `loom web terminal on`, Story 7's route, and Story 6's frames and vendored `github.com/coder/websocket`; exposes none
  - **Verify:** `CGO_ENABLED=0 go test -tags e2e ./e2e/ -run TestE2E_Web_Terminal`
  - **Review:** light
  - **Form:** full (owner, 2026-11-26: the test is the story's only code, and its marker, read loop and dial headers are traps)
  - **Reading:** spec Testing (End to end), §16, §17; `e2e/CLAUDE.md`; `e2e/web_test.go`; `e2e/e2e_test.go`
  - **Drafts after:** Story 7

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->

## Story 1: The web terminal's switch in the model

- **Developer:** intermediate-dev
- **Files:** `core/iface.go`, `core/loop_core.go`, `core/web.go`, `core/web_store.go`, `core/web_test.go`, `core/web_store_test.go`, `core/state_views_test.go`, `core/rpc/methods_gen.go`, `core/rpc/client_test.go`, `docs/specs/protocol.md`
- **Interfaces:** exposes `WebView.Terminal`, `Core.SetWebTerminal` and the `terminal` key to Stories 2, 4, 5 and 7; uses none
- **Verify:** `CGO_ENABLED=0 go test ./core/ ./core/rpc/`
- **Review:** deep
- **Form:** full (a store write with a restore on failure, and the revision it must not bump)

`web.json` gains a `terminal` key, the model's web state and `WebView` carry it, and `Core.SetWebTerminal` sets it and writes the file, as `SetQuickReplies` and `RevokeDevice` do: refused while `web.json` is left alone (even for the value already in force), the state put back when the write fails, nothing written when the value does not change, and `WebView.Rev` never bumped nor the layer's report cleared, since a changed `Rev` rebinds the listener and ends every stream. The switch works while the web is off. The method is declared on `core.Core` after `RevokeDevice`, forwarded by the loop, generated into the wire client by `go generate ./core/rpc` and written into the protocol reference by `-update`; neither generated file is edited by hand. No `rpc.Protocol` bump: a method and a field are added. Run every build and test command below through `loom job run -d "<what>" --` (wrap an env-prefixed command as `sh -c '…'`), as the session's rules say.

### Task 1.1: The state, the file's key and the view's field

**Files:**
- Modify: `core/web_store.go` (the `webState` struct at line 39, `view` at 104, the `webFile` struct at 119, `adopt`, `saveWeb`)
- Modify: `core/web.go` (`WebView`, lines 22-34)
- Test: `core/web_store_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `core/web_store_test.go`, after `TestSaveWeb_ASubSecondPauseSurvivesARestart` (any place among the `TestSaveWeb_`/`TestInitWeb_` tests works; they use only helpers the file already has):

```go
func TestInitWeb_LoadsTheTerminalSwitch(t *testing.T) {
	m := webModel(t, `{"version":1,"devices":[],"terminal":true}`)
	assert.True(t, m.Web().Terminal)

	m = webModel(t, `{"version":1,"devices":[]}`)
	assert.False(t, m.Web().Terminal, "a file without the key leaves it off")

	m = webModel(t, `{"version":1,"devices":[],"terminal":"yes"}`)
	assert.Contains(t, m.web.loadErr, "parse", "a value that is no boolean leaves the file alone")
	assert.False(t, m.Web().Terminal, "and the terminal stays off")
}

func TestSaveWeb_RoundTripsTheTerminalSwitchAndLeavesItOutWhileOff(t *testing.T) {
	m := webModel(t, webJSON)

	require.NoError(t, m.saveWeb())
	assert.NotContains(t, string(webFileBytes(t, m)), `"terminal"`, "off is left out of the file")

	m.web.terminal = true
	require.NoError(t, m.saveWeb())
	assert.Contains(t, string(webFileBytes(t, m)), `"terminal": true`)
	m2 := NewForTest(Options{})
	m2.initWeb()
	require.Empty(t, m2.web.loadErr)
	assert.True(t, m2.Web().Terminal, "the next boot reads it back")
}
```

Fails without: `TestInitWeb_LoadsTheTerminalSwitch`: the `w.terminal = f.Terminal` line in `adopt` removed (the first assertion reads false); `TestSaveWeb_RoundTripsTheTerminalSwitchAndLeavesItOutWhileOff`: `Terminal: w.terminal` removed from `saveWeb` (the file lacks the key), or `omitempty` dropped from the tag (an off switch writes `"terminal": false`).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestInitWeb_LoadsTheTerminalSwitch|TestSaveWeb_RoundTripsTheTerminalSwitch'`

Expected: the package does not build: `core/web_store_test.go: m.Web().Terminal undefined (type WebView has no field or method Terminal)` and `m.web.terminal undefined (type webState has no field or method terminal)`.

- [ ] **Step 3: Implement**

In `core/web.go`, in `WebView`, replace

```go
	QuickReplies []string
}
```

(the struct's last lines, which follow the comment "QuickReplies are the user's quick replies, nil while unset…") with

```go
	QuickReplies []string
	// Terminal is true while the web terminal is on: a paired device may
	// open a session's worktree shell (Core.SetWebTerminal). It is not part
	// of Rev: changing it never rebinds the listener.
	Terminal bool
}
```

In `core/web_store.go`, in the `webState` struct, after the line

```go
	quickReplies   []string         // the user's quick replies; nil while unset (the app offers DefaultQuickReplies)
```

add

```go
	terminal       bool             // the web terminal's switch; in web.json as "terminal", left out while off
```

In `view`, replace

```go
	v := WebView{Listen: w.listen, Origin: w.origin, Serving: w.serving, Addr: w.addr, Error: w.servingErr, Rev: w.rev, QuickReplies: slices.Clone(w.quickReplies)}
```

with

```go
	v := WebView{Listen: w.listen, Origin: w.origin, Serving: w.serving, Addr: w.addr, Error: w.servingErr, Rev: w.rev, QuickReplies: slices.Clone(w.quickReplies), Terminal: w.terminal}
```

In `webFile`, after

```go
	QuickReplies *[]string `json:"quick_replies,omitempty"`
```

add

```go
	// Terminal is the web terminal's switch, left out while off: a build
	// that does not know the key drops it when it rewrites the file, which
	// turns the terminal off.
	Terminal bool `json:"terminal,omitempty"`
```

In `adopt`, replace

```go
	w.listen, w.origin = f.Listen, f.Origin
```

with

```go
	w.listen, w.origin = f.Listen, f.Origin
	w.terminal = f.Terminal
```

In `saveWeb`, replace

```go
	f := webFile{Version: webFileVersion, Listen: w.listen, Origin: w.origin, Devices: []webFileDevice{}}
```

with

```go
	f := webFile{Version: webFileVersion, Listen: w.listen, Origin: w.origin, Devices: []webFileDevice{}, Terminal: w.terminal}
```

`ConfigureWeb` needs no change: it copies `*w` aside and writes through `saveWeb`, so the switch rides along, and a disable keeps it as it keeps the devices.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestInitWeb_|TestSaveWeb_|TestWebView_'`

Expected: `ok  	github.com/aidan-bailey/loom/core`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core): web.json and WebView carry the web terminal's switch" -- core/web.go core/web_store.go core/web_store_test.go
```

### Task 1.2: `Model.SetWebTerminal`

**Files:**
- Modify: `core/web.go` (a new method before `PairDevice`'s comment, `core/web.go:401`)
- Test: `core/web_test.go`, `core/state_views_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `core/web_test.go`, after `TestConfigureWeb_NeedsAGlobalDirWhenTheModelHasNoPath` and `TestConfigureWeb_AModelThatNeverBootedWritesUnderTheGlobalDir` (the file's `os` import and `webModel`, `webFileBytes`, `blockWebPath` helpers are in scope):

```go
func TestSetWebTerminal_SwitchesAndKeepsTheSwitchInWebJSON(t *testing.T) {
	m := webModel(t, webJSON)
	require.False(t, m.Web().Terminal, "fixture: off to begin with")

	require.NoError(t, m.SetWebTerminal(true))
	assert.True(t, m.Web().Terminal)
	assert.Contains(t, string(webFileBytes(t, m)), `"terminal": true`, "kept in web.json")
	m2 := NewForTest(Options{})
	m2.initWeb()
	assert.True(t, m2.Web().Terminal, "and read back by the next boot")

	require.NoError(t, m.SetWebTerminal(false))
	assert.False(t, m.Web().Terminal)
	assert.NotContains(t, string(webFileBytes(t, m)), `"terminal"`, "off is left out of the file")
	assert.Len(t, m.Web().Devices, 2, "the devices stay as they were")
}

// TestSetWebTerminal_NeverBumpsTheRevisionOrClearsWhatTheLayerReported: the
// layer rebinds and ends every stream when WebView.Rev changes, so the
// switch must leave it, and what the layer reported, alone.
func TestSetWebTerminal_NeverBumpsTheRevisionOrClearsWhatTheLayerReported(t *testing.T) {
	m := webModel(t, webJSON)
	m.web.rev = 7
	m.web.serving, m.web.addr = true, "127.0.0.1:7420"
	before := m.Web()

	require.NoError(t, m.SetWebTerminal(true))

	after := m.Web()
	assert.EqualValues(t, 7, after.Rev)
	assert.True(t, after.Serving)
	assert.Equal(t, "127.0.0.1:7420", after.Addr)
	before.Terminal = true
	assert.Equal(t, before, after, "nothing but the switch changed")

	require.NoError(t, m.SetWebTerminal(false))
	assert.EqualValues(t, 7, m.Web().Rev, "turning it off bumps nothing either")
}

func TestSetWebTerminal_TheValueInForceWritesNothing(t *testing.T) {
	m := webModel(t, webJSON)
	require.NoError(t, os.Remove(m.web.path), "fixture: the file is gone, so a write would bring it back")

	require.NoError(t, m.SetWebTerminal(false))
	assert.NoFileExists(t, m.web.path, "off to off writes nothing")

	require.NoError(t, m.SetWebTerminal(true))
	assert.FileExists(t, m.web.path, "a change writes")
	require.NoError(t, os.Remove(m.web.path))

	require.NoError(t, m.SetWebTerminal(true))
	assert.NoFileExists(t, m.web.path, "on to on writes nothing")
}

func TestSetWebTerminal_PutsTheSwitchBackWhenTheWriteFails(t *testing.T) {
	m := webModel(t, webJSON)
	before := m.Web()
	blockWebPath(t, m)

	require.Error(t, m.SetWebTerminal(true))
	assert.Equal(t, before, m.Web(), "a failed switch-on leaves it off")

	m.web.terminal = true
	require.Error(t, m.SetWebTerminal(false))
	assert.True(t, m.Web().Terminal, "and a failed switch-off leaves it on")
}

func TestSetWebTerminal_IsRefusedWhileWebJSONIsLeftAlone(t *testing.T) {
	m := webModel(t, `{not json`)

	err := m.SetWebTerminal(true)

	assert.ErrorContains(t, err, "left alone")
	assert.ErrorContains(t, err, "parse", "and says why")
	assert.False(t, m.Web().Terminal)
	assert.Equal(t, `{not json`, string(webFileBytes(t, m)), "the file is as it was")
	assert.ErrorContains(t, m.SetWebTerminal(false), "left alone", "the value in force is refused too, as ConfigureWeb refuses")
}

// TestSetWebTerminal_WorksWhileTheWebIsOff: a host sets the switch before it
// enables the web, and an enable afterwards keeps it.
func TestSetWebTerminal_WorksWhileTheWebIsOff(t *testing.T) {
	m := webModel(t, "")

	require.NoError(t, m.SetWebTerminal(true))
	assert.True(t, m.Web().Terminal)
	assert.Empty(t, m.Web().Listen, "the web is still off")
	assert.Contains(t, string(webFileBytes(t, m)), `"terminal": true`)

	require.NoError(t, m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	assert.True(t, m.Web().Terminal, "an enable keeps the switch")
	assert.Contains(t, string(webFileBytes(t, m)), `"terminal": true`, "in the file too")
}
```

Fails without: `TestSetWebTerminal_NeverBumpsTheRevisionOrClearsWhatTheLayerReported`: `w.rev++` and `w.serving, w.addr, w.servingErr = false, "", ""` added to `SetWebTerminal`, as `ConfigureWeb` has them; `TestSetWebTerminal_TheValueInForceWritesNothing`: the `if on == w.terminal { return nil }` line removed (the second and third writes happen); `TestSetWebTerminal_PutsTheSwitchBackWhenTheWriteFails`: the `w.terminal = prev` restore removed, or written as `w.terminal = true`; `TestSetWebTerminal_IsRefusedWhileWebJSONIsLeftAlone`: the `loadErr` check removed, or moved after the equality check (the `SetWebTerminal(false)` assertion then sees nil); `TestSetWebTerminal_WorksWhileTheWebIsOff`: a refusal added for `w.listen == ""`, or `saveWeb`'s `Terminal: w.terminal` removed (the enable's write drops the switch); `TestSetWebTerminal_SwitchesAndKeepsTheSwitchInWebJSON`: the `saveWeb` call removed.

Append to `core/state_views_test.go`, after `TestPublishState_WebChangesReachClients`:

```go
// TestPublishState_TheWebTerminalSwitchIsPublishedAndLeavesTheRevisionAlone:
// a change of the switch reaches every client in the model view, with the
// revision as it was, and the value in force publishes nothing.
func TestPublishState_TheWebTerminalSwitchIsPublishedAndLeavesTheRevisionAlone(t *testing.T) {
	m := webModel(t, webJSON)
	m.Sync()
	rev := m.Web().Rev
	published := func() (events int, v WebView) {
		evs := m.Sync().Events
		events, _, _ = stateEvents(evs)
		for _, ev := range evs {
			if mc, ok := ev.(ModelChanged); ok {
				v = mc.View.Web
			}
		}
		return events, v
	}

	require.NoError(t, m.SetWebTerminal(true))
	n, v := published()
	assert.Equal(t, 1, n, "turning it on is published")
	assert.True(t, v.Terminal)
	assert.Equal(t, rev, v.Rev, "the layer rebinds on a new revision, and this is not one")

	require.NoError(t, m.SetWebTerminal(true))
	n, _ = published()
	assert.Zero(t, n, "the value in force publishes nothing")

	require.NoError(t, m.SetWebTerminal(false))
	n, v = published()
	assert.Equal(t, 1, n, "turning it off is published")
	assert.False(t, v.Terminal)
}
```

Fails without: `TestPublishState_TheWebTerminalSwitchIsPublishedAndLeavesTheRevisionAlone`: `Terminal: w.terminal` removed from `view` (the published value stays false), or `w.rev++` added to `SetWebTerminal` (the revision assertion).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestSetWebTerminal_|TestPublishState_TheWebTerminal'`

Expected: the package does not build: `m.SetWebTerminal undefined (type *Model has no field or method SetWebTerminal)`.

- [ ] **Step 3: Implement**

In `core/web.go`, immediately before the comment that begins `// PairDevice makes the one-time code that pairs a device under name`, add:

```go
// SetWebTerminal turns the web terminal on or off and writes web.json
// (Core.SetWebTerminal): while it is on, a paired device may open a
// session's worktree shell through the web layer. It never touches the
// revision (WebView.Rev) or what the layer reported: the layer rebinds its
// listener and ends every stream when the revision changes, and only
// ConfigureWeb bumps it; the layer follows this switch through the published
// view instead. A value already in force writes nothing. It works while the
// web is off, like RevokeDevice, and is refused while web.json is left alone,
// the value in force included. When the write fails the switch is as it was.
// Loop goroutine only.
func (m *Model) SetWebTerminal(on bool) error {
	w := &m.web
	if w.loadErr != "" {
		return w.leftAlone()
	}
	if on == w.terminal {
		return nil
	}
	prev := w.terminal
	w.terminal = on
	if err := m.saveWeb(); err != nil {
		w.terminal = prev
		return fmt.Errorf("save web terminal: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestSetWebTerminal_|TestPublishState_|TestModelView_|TestConfigureWeb_|TestInitWeb_|TestSaveWeb_'`

Expected: `ok  	github.com/aidan-bailey/loom/core`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core): Model.SetWebTerminal writes the switch without touching the revision" -- core/web.go core/web_test.go core/state_views_test.go
```

### Task 1.3: `Core.SetWebTerminal`: the interface, the loop's forwarder, the wire

**Files:**
- Modify: `core/iface.go:165` (after `RevokeDevice`)
- Modify: `core/loop_core.go:223-226` (after `RevokeDevice`)
- Regenerate: `core/rpc/methods_gen.go` (`go generate ./core/rpc`), `docs/specs/protocol.md` (`-update`); never edited by hand
- Test: `core/rpc/client_test.go`

- [ ] **Step 1: Write the failing tests**

In `core/rpc/client_test.go`, after `TestWebRequests_ReachTheModelOverTheWire`, add (the file already imports `os`, `filepath` and `config` for `webModel`):

```go
// TestSetWebTerminal_ReachesTheModelOverTheWire: the switch is a request, so
// it is in the replica when it returns, and it leaves the revision alone.
func TestSetWebTerminal_ReachesTheModelOverTheWire(t *testing.T) {
	loop := core.StartForTest(webModel(t))
	c := pair(t, loop)
	require.False(t, c.Web().Terminal, "fixture: off to begin with")

	require.NoError(t, c.SetWebTerminal(true))
	assert.True(t, c.Web().Terminal, "the request's ModelChanged reached the replica before its reply")
	assert.Zero(t, c.Web().Rev, "and the revision did not move: the layer would rebind")

	require.NoError(t, c.SetWebTerminal(false))
	assert.False(t, c.Web().Terminal)
}

// TestSetWebTerminal_ARefusalCrossesTheWire: a model that left web.json alone
// refuses, and the client reads the reason.
func TestSetWebTerminal_ARefusalCrossesTheWire(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, core.WebFile), []byte(`{not json`), 0o600))
	model := core.NewForTest(core.Options{})
	model.InitWebForTest()
	c := pair(t, core.StartForTest(model))

	assert.ErrorContains(t, c.SetWebTerminal(true), "left alone")
	assert.False(t, c.Web().Terminal, "a refused request changes nothing")
}
```

Fails without: `TestSetWebTerminal_ReachesTheModelOverTheWire`: `Terminal: w.terminal` removed from `webState.view` (the replica reads false), or the loop's forwarder calling `m.SetWebTerminal(!on)`; `TestSetWebTerminal_ARefusalCrossesTheWire`: the `loadErr` check removed from `Model.SetWebTerminal` (the request succeeds).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./core/rpc/ -run 'TestSetWebTerminal_'`

Expected: the package does not build: `c.SetWebTerminal undefined (type *Client has no field or method SetWebTerminal)`.

- [ ] **Step 3: Implement**

In `core/iface.go`, after the line

```go
	RevokeDevice(name string) error
```

add

```go
	// SetWebTerminal turns the web terminal on or off and writes web.json.
	// It never bumps WebView.Rev, so the layer neither rebinds nor ends its
	// streams; the layer follows WebView.Terminal.
	SetWebTerminal(on bool) error
```

(the comment holds no `rpc:` word, so the method is a plain request).

In `core/loop_core.go`, after

```go
// RevokeDevice runs Model.RevokeDevice on the loop.
func (l *Loop) RevokeDevice(name string) error {
	return get(l, func(m *Model) error { return m.RevokeDevice(name) })
}
```

add

```go
// SetWebTerminal runs Model.SetWebTerminal on the loop.
func (l *Loop) SetWebTerminal(on bool) error {
	return get(l, func(m *Model) error { return m.SetWebTerminal(on) })
}
```

Regenerate the wire and the protocol reference (neither file is edited by hand):

```bash
go generate ./core/rpc
go test ./core/rpc -run TestProtocolReference -update
git diff --stat -- core/rpc/methods_gen.go docs/specs/protocol.md
```

The diff shows `core/rpc/methods_gen.go` gaining `SetWebTerminalParams{On bool}`, `SetWebTerminalResult`, the `kindRequest` table row after `RevokeDevice`, the server's dispatch case and `func (c *Client) SetWebTerminal(on bool) error`; and `docs/specs/protocol.md` gaining, after the `RevokeDevice` section,

```
### SetWebTerminal (request)

Params: `{"on":false}`

Result: `{}`
```

and the `Web` result's view ending `"QuickReplies":null,"Terminal":false}`.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./core/ ./core/rpc/`

Expected: `ok  	github.com/aidan-bailey/loom/core` and `ok  	github.com/aidan-bailey/loom/core/rpc`; among them `TestLoopForwardsEachMethodToItsNamesake`, `TestGenerated_IsFresh`, `TestMethods_CoverCore`, `TestProtocolReference` and `TestWire_EveryFieldSurvives` pass.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core,rpc): Core.SetWebTerminal, forwarded by the loop and generated into the wire" -- core/iface.go core/loop_core.go core/rpc/methods_gen.go core/rpc/client_test.go docs/specs/protocol.md
```

## Story 2: `EnsureTerminal`: the model makes a session's shell

- **Developer:** intermediate-dev
- **Files:** `core/terminal.go`, `core/terminal_test.go`, `core/iface.go`, `core/loop_core.go`, `core/requests.go`, `core/requests_test.go`, `core/model.go`, `core/events.go`, `core/events_json.go`, `core/rpc/methods_gen.go`, `core/rpc/wire_test.go`, `docs/specs/protocol.md`, `core/README.md`, `session/reconcile.go`, `session/reconcile_foreign_test.go`, `session/README.md`
- **Interfaces:** exposes `Core.EnsureTerminal` and `Reply.Terminal` to Stories 7 and 9; uses Story 1's `core/iface.go`, `core/loop_core.go`, `core/rpc/methods_gen.go` and `docs/specs/protocol.md` as it leaves them
- **Verify:** `CGO_ENABLED=0 go test ./core/ ./core/rpc/`
- **Review:** deep
- **Form:** full (a job racing a kill or pause, and a tmux session closed on delivery)

`Core.EnsureTerminal(id, req)` is a request that names an instance. It is admitted through a precondition that refuses an instance that is unstarted, paused, loading, being deleted, recoverable, a workspace terminal or without a worktree, and then a job makes the session's worktree shell: the tmux session `tmux.TerminalSessionName(title)`, running `$SHELL` (else `/bin/sh`, read in the daemon's environment) in the worktree. The job starts the shell through the lifecycle's `tmux.Session` (`NewSessionNamedWithDeps` and `Start`), which launches without attaching a client. A session of that name that already exists is the instance's own only when it started in the instance's worktree (`session.HeldElsewhere`, the rule pause and kill judge the shell by, asked through a new `session.HeldElsewhereListed` that also says whether the name was listed at all): one held elsewhere, a tmux that cannot answer, or a session that is no longer listed when its start directory is read (its shell ended meanwhile, so it is never named) is a refusal that matches `ErrRefused`, and nothing is attached. `tmux.ErrSessionExists` from the start means another client made the shell meanwhile, and the job answers as for a shell that existed (the same judgement, so a session another workspace put under the name meanwhile is refused), having started nothing; any other error after the launch leaves the shell possibly started, and the job says so. The Reply's new `Terminal` field is the shell's tmux name, `tmux.ToLoomTmuxName(tmux.TerminalSessionName(title))`, set only on success, and crosses the wire. On delivery the model looks the instance up again by the id the result carries: when the instance is gone or no longer started and active, the Reply is a refusal whoever made the shell, so nothing attaches to a shell a pause or kill is closing; and when the job started (or possibly started) that shell, the model also closes it by its exact name in a job of its own, which first confirms the session still starts in that worktree. A shell that existed before the request, or that another client made meanwhile, is never closed here: pause and kill own that. A last task brings `core/README.md` into line for this story and Story 1. The name is built by `ToLoomTmuxName`, so `tmux.ExactlyTargetable` accepts it, and `Session.Start` sets `detach-on-destroy on` on the shell's session, which Story 3's attach relies on; the first task's test pins both. This story's code holds none of the profile's landmine markers.

### Task 2.1: `Reply.Terminal` crosses the wire

**Files:**
- Modify: `core/events.go:217-250` (the `Reply` doc comment and struct)
- Modify: `core/events_json.go:46-74` (`replyJSON`, `Reply.MarshalJSON`, `Reply.UnmarshalJSON`)
- Regenerate: `docs/specs/protocol.md` (`go test ./core/rpc -run TestProtocolReference -update`); never edited by hand
- Test: `core/rpc/wire_test.go`

- [ ] **Step 1: Write the failing test**

In `core/rpc/wire_test.go`, after `TestEvents_RoundTrip` (before `func mustFrame`), add:

```go
// TestReply_TerminalSurvivesTheWire: EnsureTerminal answers in the Reply's
// Terminal, and a client that reads zero there has no shell to attach to.
// A Reply that carries none still reads none.
func TestReply_TerminalSurvivesTheWire(t *testing.T) {
	for _, want := range []string{"loom_term_web", ""} {
		f, err := encodeEvent(core.Reply{Req: 4, ID: 5, Terminal: want})
		require.NoError(t, err)
		line, err := json.Marshal(f)
		require.NoError(t, err)
		var back Frame
		require.NoError(t, json.Unmarshal(line, &back))
		got, err := decodeEvent(back)
		require.NoError(t, err)
		assert.Equal(t, want, got.(core.Reply).Terminal)
		assert.Equal(t, core.ReqID(4), got.(core.Reply).Req)
	}
}
```

Fails without: `TestReply_TerminalSurvivesTheWire`: `Terminal: r.Terminal` removed from `Reply.MarshalJSON`, or `Terminal: j.Terminal` removed from `Reply.UnmarshalJSON` (the first value reads back empty).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./core/rpc/ -run 'TestReply_TerminalSurvivesTheWire'`

Expected: the package does not build: `unknown field Terminal in struct literal of type core.Reply`.

- [ ] **Step 3: Implement**

In `core/events.go`, in the `Reply` doc comment, replace the line

```go
// Sweep SweepBuilds' report. Job is SubmitJob's new job (0 for every other request), whose
```

with

```go
// Sweep SweepBuilds' report, Terminal EnsureTerminal's shell (its tmux
// name). Job is SubmitJob's new job (0 for every other request), whose
```

and in the struct, after

```go
	// Watch is ReadWatch's answer.
	Watch WatchView
}
```

add the field, so it reads

```go
	// Watch is ReadWatch's answer.
	Watch WatchView
	// Terminal is EnsureTerminal's answer: the tmux name of the session's
	// shell, tmux.ToLoomTmuxName(tmux.TerminalSessionName(title)); empty on
	// every other reply and on a refusal.
	Terminal string
}
```

In `core/events_json.go`, in `replyJSON`, after

```go
	FlowDoc FlowDocView
	Watch   WatchView
}

// MarshalJSON encodes r with its errors as WireErrors.
```

add the field after a blank line (a blank line ends gofmt's alignment block, so no other line is re-padded), so it reads

```go
	FlowDoc FlowDocView
	Watch   WatchView

	Terminal string
}

// MarshalJSON encodes r with its errors as WireErrors.
```

In `Reply.MarshalJSON`, replace the tail of the long `return json.Marshal(replyJSON{…})` line,

```go
FlowDoc: r.FlowDoc, Watch: r.Watch})
```

with

```go
FlowDoc: r.FlowDoc, Watch: r.Watch, Terminal: r.Terminal})
```

and in `Reply.UnmarshalJSON`, replace the tail of the long `*r = Reply{…}` line,

```go
FlowDoc: j.FlowDoc, Watch: j.Watch}
```

with

```go
FlowDoc: j.FlowDoc, Watch: j.Watch, Terminal: j.Terminal}
```

Rewrite the protocol reference, whose two `Reply` examples (the event's, and the type's) gain `"Terminal":""` after `"Watch"`'s object:

```bash
go test ./core/rpc -run TestProtocolReference -update
git diff --stat -- docs/specs/protocol.md
```

The diff touches only those two lines of `docs/specs/protocol.md`.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./core/ ./core/rpc/`

Expected: `ok  	github.com/aidan-bailey/loom/core` and `ok  	github.com/aidan-bailey/loom/core/rpc`; among them `TestReply_TerminalSurvivesTheWire`, `TestWire_EveryFieldSurvives` and `TestProtocolReference` pass.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core,rpc): Reply.Terminal carries a session's shell name over the wire" -- core/events.go core/events_json.go core/rpc/wire_test.go docs/specs/protocol.md
```

### Task 2.2: `session.HeldElsewhereListed` says whether the name was listed

`HeldElsewhere` answers free both for a session that is the record's own and for no session at all, so a caller that goes on to name the session it found cannot tell a shell that ended since its probe from one that runs. `EnsureTerminal` (Task 2.3) must not name a shell that is gone, and needs the listing's own found or not found; this task exposes it from the one listing, as the verdict's second result, and `HeldElsewhere` becomes its wrapper.

**Files:**
- Modify: `session/reconcile.go:220-223` (`HeldElsewhere`'s body, and the new function after it)
- Modify: `session/README.md:33` (the `HeldElsewhere` bullet)
- Test: `session/reconcile_foreign_test.go`

- [ ] **Step 1: Write the failing test**

In `session/reconcile_foreign_test.go`, after `TestHeldElsewhere_OnlyTheRecordsOwnSessionIsFree` (after its last lines, `assert.Error(t, err, "an unreadable listing is no answer")` and `}`), add:

```go
// HeldElsewhereListed gives HeldElsewhere's verdict and whether a session of
// the name was listed at all, so a caller that names the session it finds
// can tell the record's own live session from no session.
func TestHeldElsewhereListed_SaysWhetherASessionOfTheNameWasListed(t *testing.T) {
	base := t.TempDir()
	wt := filepath.Join(base, "repo", ".loom", "worktrees", "api_1")
	home := SessionHome{Dir: wt}
	listed := func(list string, err error) cmd_test.MockCmdExec {
		return cmd_test.MockCmdExec{OutputFunc: func(*exec.Cmd) ([]byte, error) { return []byte(list), err }}
	}
	for _, tc := range []struct {
		name       string
		exec       cmd_test.MockCmdExec
		wantHeld   bool
		wantListed bool
	}{
		{"no such session", listed(listing("loom_other", wt), nil), false, false},
		{"no server", listed("", errors.New("no server running on /tmp/tmux-1/x")), false, false},
		{"its own, in the worktree", listed(listing("loom_api", wt), nil), false, true},
		{"its own, below the worktree", listed(listing("loom_api", filepath.Join(wt, "sub")), nil), false, true},
		{"started elsewhere", listed(listing("loom_api", filepath.Join(base, "other")), nil), true, true},
		{"no start directory", listed("loom_api\t\n", nil), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held, found, err := HeldElsewhereListed("api", home, tc.exec)
			require.NoError(t, err)
			assert.Equal(t, tc.wantHeld, held)
			assert.Equal(t, tc.wantListed, found)
		})
	}

	_, _, err := HeldElsewhereListed("api", home, listed("", errors.New("signal: killed")))
	assert.Error(t, err, "an unreadable listing is no answer")
}
```

Fails without: `TestHeldElsewhereListed_SaysWhetherASessionOfTheNameWasListed`: the second result returned as `true` or as `!held` (the "no such session" and "no server" cases, or "its own", read the wrong value), or the `err` from the listing dropped (the last assertion sees no error).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./session/ -run 'TestHeldElsewhereListed_'`

Expected: the package does not build: `undefined: HeldElsewhereListed`.

- [ ] **Step 3: Implement**

In `session/reconcile.go`, replace

```go
func HeldElsewhere(title string, home SessionHome, cmdExec internalexec.Executor) (bool, error) {
	dir, found, err := sessionStartDir(title, cmdExec)
	return heldElsewhere(tmux.ToLoomTmuxName(title), home, dir, found, err)
}
```

with

```go
func HeldElsewhere(title string, home SessionHome, cmdExec internalexec.Executor) (bool, error) {
	held, _, err := HeldElsewhereListed(title, home, cmdExec)
	return held, err
}

// HeldElsewhereListed is HeldElsewhere's verdict from one listing, and
// whether a session of the name was listed at all: free with listed false
// is no session (or no server), free with listed true is the record's own.
// A caller that goes on to name the session it found (Core.EnsureTerminal)
// needs the difference: a session that ended since the caller's probe is not
// one to name.
func HeldElsewhereListed(title string, home SessionHome, cmdExec internalexec.Executor) (held, listed bool, err error) {
	dir, found, err := sessionStartDir(title, cmdExec)
	held, err = heldElsewhere(tmux.ToLoomTmuxName(title), home, dir, found, err)
	return held, found, err
}
```

In `session/README.md`, in the `HeldElsewhere` bullet (line 33), after the sentence ending

```
or an error (a listing tmux left unanswered, a timeout under load: change nothing and ask again).
```

add the sentence

```
`HeldElsewhereListed` is the same check that also says whether any session of the name was listed, for a caller that names the live session it finds and must not name one that has gone (`Core.EnsureTerminal`).
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./session/ ./tools/claudemd/`

Expected: `ok  	github.com/aidan-bailey/loom/session` and `ok  	github.com/aidan-bailey/loom/tools/claudemd`; among them `TestHeldElsewhereListed_SaysWhetherASessionOfTheNameWasListed`, `TestHeldElsewhere_OnlyTheRecordsOwnSessionIsFree` and `TestRepo_ClaudeMDStructure` pass.

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor(session): HeldElsewhereListed says whether the name was listed" -- session/reconcile.go session/README.md session/reconcile_foreign_test.go
```

### Task 2.3: `Core.EnsureTerminal` admits, refuses and makes the shell

**Files:**
- Create: `core/terminal.go`, `core/terminal_test.go`
- Modify: `core/iface.go:98` (after `ReadJobLog`)
- Modify: `core/loop_core.go:110-112` (after `ReadJobLog`'s forwarder)
- Modify: `core/requests.go` (`opTerminal`, its precondition, `deliverTracked` and `outcome`)
- Modify: `core/model.go` (`Deliver`'s case list)
- Regenerate: `core/rpc/methods_gen.go` (`go generate ./core/rpc`), `docs/specs/protocol.md` (`-update`); never edited by hand
- Test: `core/requests_test.go`

- [ ] **Step 1: Write the failing tests**

Create `core/terminal_test.go`:

```go
package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shellInst builds a started, Running session titled title whose worktree
// is dir.
func shellInst(t *testing.T, title, dir string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         title, Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: t.TempDir(), WorktreePath: dir, BranchName: "main", SessionName: title},
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	require.True(t, inst.Started(), "fixture precondition")
	require.Equal(t, dir, inst.GetWorktreePath(), "fixture precondition")
	return inst
}

// shellModel serves insts in one stored workspace.
func shellModel(t *testing.T, insts ...*session.Instance) *Model {
	t.Helper()
	ws := storedWorkspace(t, "a")
	for _, inst := range insts {
		ws.add(inst)
	}
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)
	m.Drain()
	return m
}

// shellName is the tmux name of title's worktree shell.
func shellName(title string) string {
	return tmux.ToLoomTmuxName(tmux.TerminalSessionName(title))
}

// shellAlive reports whether title's worktree shell runs on the test's
// private tmux server.
func shellAlive(t *testing.T, title string) bool {
	t.Helper()
	return tmux.Command(context.Background(), "has-session", "-t", tmux.SessionTarget(shellName(title))).Run() == nil
}

// shellOption is the value of a session option of title's shell.
func shellOption(t *testing.T, title, option string) string {
	t.Helper()
	out, err := tmux.Command(context.Background(), "show-options", "-v", "-t", tmux.PaneTarget(shellName(title)), option).Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// TestTerminalShell_IsTheEnvironmentsElseBinSh: the shell is the daemon's
// $SHELL, and /bin/sh when it has none.
func TestTerminalShell_IsTheEnvironmentsElseBinSh(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/zsh")
	assert.Equal(t, "/usr/bin/zsh", terminalShell())
	t.Setenv("SHELL", "")
	assert.Equal(t, "/bin/sh", terminalShell())
}

// A started, active session with a worktree gets its shell: the tmux
// session `term_<title>` in the worktree, named as the Reply says, with the
// options every client of it relies on. The title holds a space, a dot and
// a colon, which ToLoomTmuxName maps out so the name can be targeted
// exactly.
func TestEnsureTerminal_StartsTheShellInTheWorktree(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	const title = "fix 1.2: ui"
	killSessionAtEnd(t, tmux.TerminalSessionName(title))
	dir := t.TempDir()
	inst := shellInst(t, title, dir)
	m := shellModel(t, inst)

	m.EnsureTerminal(m.idOf(inst), 3)
	r := replyTo(runJobs(t, m), 3)

	require.NotNil(t, r, "the request is answered")
	require.NoError(t, r.Err)
	assert.Equal(t, shellName(title), r.Terminal)
	assert.True(t, tmux.ExactlyTargetable(r.Terminal), "Story 3's attach targets it exactly")
	assert.True(t, shellAlive(t, title))
	assert.Equal(t, canonical(t, dir), canonical(t, sessionPath(t, tmux.TerminalSessionName(title))), "it runs in the worktree")
	assert.Equal(t, "on", shellOption(t, title, "detach-on-destroy"), "a killed shell ends its attach client")
}

// A shell that already runs where the instance's own session starts, in the
// worktree or below it, is the instance's: the Reply names it and nothing
// is started over it.
func TestEnsureTerminal_AShellStartedInTheWorktreeIsTheInstancesOwn(t *testing.T) {
	for _, where := range []string{"the worktree", "below the worktree"} {
		t.Run(where, func(t *testing.T) {
			isolateTmux(t)
			t.Setenv("SHELL", "/bin/sh")
			dir := t.TempDir()
			start := dir
			if where == "below the worktree" {
				start = filepath.Join(dir, "sub")
				require.NoError(t, os.MkdirAll(start, 0o755))
			}
			startForeignSession(t, tmux.TerminalSessionName("web"), start)
			inst := shellInst(t, "web", dir)
			m := shellModel(t, inst)

			m.EnsureTerminal(m.idOf(inst), 3)
			r := replyTo(runJobs(t, m), 3)

			require.NotNil(t, r)
			require.NoError(t, r.Err)
			assert.Equal(t, shellName("web"), r.Terminal)
			assert.Equal(t, canonical(t, start), canonical(t, sessionPath(t, tmux.TerminalSessionName("web"))), "the shell is the one that was running")
		})
	}
}

// A session under the shell's name that started somewhere else (another
// workspace's same-titled session, or an agent whose title begins term_)
// is not the instance's: the request is refused, nothing is attached, and
// that session runs on.
func TestEnsureTerminal_ASessionStartedElsewhereIsARefusal(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	elsewhere := t.TempDir()
	startForeignSession(t, tmux.TerminalSessionName("web"), elsewhere)
	inst := shellInst(t, "web", t.TempDir())
	m := shellModel(t, inst)

	m.EnsureTerminal(m.idOf(inst), 3)
	r := replyTo(runJobs(t, m), 3)

	require.NotNil(t, r)
	require.Error(t, r.Err)
	assert.ErrorIs(t, r.Err, ErrRefused, "the web layer answers it 409")
	assert.Contains(t, r.Err.Error(), "another session's")
	assert.Empty(t, r.Terminal, "no name is handed out for a session that is not the instance's")
	assert.Equal(t, canonical(t, elsewhere), canonical(t, sessionPath(t, tmux.TerminalSessionName("web"))), "the other session runs on where it started")
}

// A tmux that cannot answer is a refusal, and loom starts and attaches
// nothing: neither the has-session probe going unanswered (tmux missing
// from the path) nor the listing that tells whose a live session is.
func TestEnsureTerminal_TmuxThatCannotAnswerIsARefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*exec.Cmd) error
		out  func(*exec.Cmd) ([]byte, error)
		want string
	}{
		{"the probe never answers",
			func(*exec.Cmd) error { return &exec.Error{Name: "tmux", Err: exec.ErrNotFound} },
			func(*exec.Cmd) ([]byte, error) { return nil, nil },
			"did not answer whether"},
		{"the listing never answers",
			func(*exec.Cmd) error { return nil }, // has-session: the session runs
			func(*exec.Cmd) ([]byte, error) { return nil, errors.New("timed out") },
			"did not answer whose"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			t.Setenv("SHELL", "/bin/sh")
			killSessionAtEnd(t, tmux.TerminalSessionName("web"))
			inst := shellInst(t, "web", t.TempDir())
			m := shellModel(t, inst)
			var ran []string
			m.SetExecForTest(cmd_test.MockCmdExec{
				RunFunc: func(c *exec.Cmd) error {
					ran = append(ran, cmd_test.TmuxSubcommand(c.Args))
					return tc.run(c)
				},
				OutputFunc: tc.out,
			})

			m.EnsureTerminal(m.idOf(inst), 3)
			r := replyTo(runJobs(t, m), 3)

			require.NotNil(t, r)
			assert.ErrorIs(t, r.Err, ErrRefused)
			assert.Contains(t, r.Err.Error(), tc.want)
			assert.Empty(t, r.Terminal)
			assert.NotContains(t, ran, "kill-session", "nothing is closed")
			assert.Empty(t, sessionPath(t, tmux.TerminalSessionName("web")), "nothing is started")
		})
	}
}

// racingExec is a tmux whose has-session first says no session runs and
// then, from Start's own check on, that one does: another client made the
// shell between the job's probe and its start. list is what `tmux ls` then
// prints: the sessions and where each started, "<name>\t<dir>\n" a line.
func racingExec(probes *int, ran *[]string, list string) cmd_test.MockCmdExec {
	noSession := exec.Command("sh", "-c", "exit 1").Run()
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			sub := cmd_test.TmuxSubcommand(c.Args)
			*ran = append(*ran, sub)
			if sub == "has-session" {
				*probes++
				if *probes == 1 {
					return noSession
				}
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return []byte(list), nil },
	}
}

// tmux.ErrSessionExists from the start means another client made the shell
// meanwhile: when it started in the worktree that is a success, the shell is
// named, and the job started and closed nothing.
func TestEnsureTerminal_AShellAnotherClientMadeMeanwhileIsASuccess(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	dir := t.TempDir()
	inst := shellInst(t, "web", dir)
	m := shellModel(t, inst)
	var probes int
	var ran []string
	m.SetExecForTest(racingExec(&probes, &ran, shellName("web")+"\t"+dir+"\n"))

	m.EnsureTerminal(m.idOf(inst), 3)
	r := replyTo(runJobs(t, m), 3)

	require.NotNil(t, r)
	require.NoError(t, r.Err)
	assert.Equal(t, shellName("web"), r.Terminal)
	assert.Equal(t, 2, probes, "the probe, then Start's own check")
	assert.NotContains(t, ran, "kill-session")
	assert.Empty(t, sessionPath(t, tmux.TerminalSessionName("web")), "no session was made by the job")
}

// The session another client put under the name meanwhile gets the same
// judgement as one that existed: started in another directory it is another
// session's, and the Reply is a refusal that names nothing.
func TestEnsureTerminal_ASessionAnotherClientMadeElsewhereMeanwhileIsARefusal(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	inst := shellInst(t, "web", t.TempDir())
	m := shellModel(t, inst)
	var probes int
	var ran []string
	m.SetExecForTest(racingExec(&probes, &ran, shellName("web")+"\t"+t.TempDir()+"\n"))

	m.EnsureTerminal(m.idOf(inst), 3)
	r := replyTo(runJobs(t, m), 3)

	require.NotNil(t, r)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.Contains(t, r.Err.Error(), "another session's")
	assert.Empty(t, r.Terminal, "the browser must not type into another worktree's shell")
	assert.Equal(t, 2, probes, "the probe, then Start's own check")
	assert.NotContains(t, ran, "kill-session")
}

// A shell that is gone when its start directory is read is never named:
// whether the probe found it alive (it ended before the listing) or Start
// found it (another client's shell ended before the listing), the Reply is a
// refusal to try again, and nothing is started or closed.
func TestEnsureTerminal_AShellThatEndedBeforeTheListingIsNotNamed(t *testing.T) {
	for _, tc := range []struct {
		name string
		exec func(probes *int, ran *[]string) cmd_test.MockCmdExec
	}{
		{"the probe found it", func(probes *int, ran *[]string) cmd_test.MockCmdExec {
			return cmd_test.MockCmdExec{
				RunFunc: func(c *exec.Cmd) error {
					*ran = append(*ran, cmd_test.TmuxSubcommand(c.Args))
					return nil // has-session: it runs
				},
				OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil }, // tmux ls: nothing listed
			}
		}},
		{"Start's own check found it", func(probes *int, ran *[]string) cmd_test.MockCmdExec {
			return racingExec(probes, ran, "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			t.Setenv("SHELL", "/bin/sh")
			killSessionAtEnd(t, tmux.TerminalSessionName("web"))
			inst := shellInst(t, "web", t.TempDir())
			m := shellModel(t, inst)
			var probes int
			var ran []string
			m.SetExecForTest(tc.exec(&probes, &ran))

			m.EnsureTerminal(m.idOf(inst), 3)
			r := replyTo(runJobs(t, m), 3)

			require.NotNil(t, r)
			assert.ErrorIs(t, r.Err, ErrRefused)
			assert.Contains(t, r.Err.Error(), "ended while loom looked at it")
			assert.Empty(t, r.Terminal, "a shell that no longer runs is not named")
			assert.NotContains(t, ran, "kill-session", "nothing is closed")
			assert.Empty(t, sessionPath(t, tmux.TerminalSessionName("web")), "nothing is started")
		})
	}
}
```

Fails without: `TestTerminalShell_IsTheEnvironmentsElseBinSh`: the `/bin/sh` fallback removed from `terminalShell` (the second assertion reads an empty string); `TestEnsureTerminal_StartsTheShellInTheWorktree`: the job's `ts.Start(worktree)` removed (no session runs), or `Start` given the instance's path instead of its worktree (the start-directory assertion), or `terminalShell()` replaced by `session.Program()`; `TestEnsureTerminal_AShellStartedInTheWorktreeIsTheInstancesOwn`: the `held` verdict inverted in `ownShell` (both cases answer a refusal); `TestEnsureTerminal_ASessionStartedElsewhereIsARefusal`: the `held` case removed from `ownShell` (the Reply succeeds and names the other session's shell); `TestEnsureTerminal_TmuxThatCannotAnswerIsARefusal`: `tmux.LivenessUnknown` treated as dead in the job (the first case calls `Start`, which launches a real `new-session` through the pty whose `has-session` polls the mock never confirms, so after its wait the cleanup `Close` puts `kill-session` in `ran`: the Reply is an error that does not match `ErrRefused` and the `kill-session` assertion fails), or the `err` case removed from `ownShell` (the second case succeeds and names the session); `TestEnsureTerminal_AShellAnotherClientMadeMeanwhileIsASuccess`: the `errors.Is(err, tmux.ErrSessionExists)` case removed (the Reply is an error and `Terminal` empty); `TestEnsureTerminal_ASessionAnotherClientMadeElsewhereMeanwhileIsARefusal`: the `ErrSessionExists` case answering `res.name = name` without `ownShell` (the Reply names the other directory's shell); `TestEnsureTerminal_AShellThatEndedBeforeTheListingIsNotNamed`: the `!listed` case removed from `ownShell` (both cases name a shell no tmux lists).

In `core/requests_test.go`, in `TestRequests_RefuseWhatTheTUIRefuses`, after the case

```go
		{"read the diff of an unstarted session", func(f fixture) { f.m.ReadDiff(f.id["new"], 1) }, "read the diff of new: the session has not started"},
```

add (the fixtures `term`, `loading`, `deleting`, `paused`, `orphan`, `new` and `running` already exist; `running` is started with no worktree):

```go
		// A terminal has no key of its own in the TUI: its gate is
		// ensureSessionLocked's, a started, active row with a worktree.
		{"open a terminal of a workspace terminal", func(f fixture) { f.m.EnsureTerminal(f.id["term"], 1) }, "open a terminal of term: not allowed on a workspace terminal"},
		{"open a terminal of a loading session", func(f fixture) { f.m.EnsureTerminal(f.id["loading"], 1) }, "open a terminal of loading: the session is busy (Loading)"},
		{"open a terminal of a deleting session", func(f fixture) { f.m.EnsureTerminal(f.id["deleting"], 1) }, "open a terminal of deleting: the session is busy (Deleting)"},
		{"open a terminal of a paused session", func(f fixture) { f.m.EnsureTerminal(f.id["paused"], 1) }, "open a terminal of paused: the session is paused"},
		{"open a terminal of a recoverable session", func(f fixture) { f.m.EnsureTerminal(f.id["orphan"], 1) }, "open a terminal of orphan: the session is not running (Recoverable)"},
		{"open a terminal of an unstarted session", func(f fixture) { f.m.EnsureTerminal(f.id["new"], 1) }, "open a terminal of new: the session has not started"},
		{"open a terminal of a session with no worktree", func(f fixture) { f.m.EnsureTerminal(f.id["running"], 1) }, "open a terminal of running: the session has no worktree"},
```

Fails without: each of the seven cases: its `case` removed from `precondition`'s `opTerminal` branch (the request is admitted, so a job is queued and `assert.Empty(t, out.Jobs)` fails, and the Reply is not the refusal); the first also when the `IsWorkspaceTerminal` term is dropped (a workspace terminal's path is its repository, so no later term refuses it).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestEnsureTerminal_|TestTerminalShell_|TestRequests_RefuseWhatTheTUIRefuses'`

Expected: the package does not build: `m.EnsureTerminal undefined (type *Model has no field or method EnsureTerminal)` and `undefined: terminalShell`.

- [ ] **Step 3: Implement**

Create `core/terminal.go`:

```go
package core

import (
	"errors"
	"fmt"
	"os"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
)

// terminalResult is EnsureTerminal's job result.
type terminalResult struct {
	// title and home are the instance's, taken when the request was
	// admitted: a close job on delivery names the shell by them.
	title string
	home  session.SessionHome
	// name is the shell's tmux name, set when the shell is the instance's.
	name string
	// started: the job made the shell. possible: the launch failed after
	// tmux may have made it (err says how). Neither: the shell existed.
	started  bool
	possible bool
	// err is the refusal (a refusedError) or the failure.
	err error
}

// terminalShell is the program of a worktree shell: the daemon's $SHELL,
// else /bin/sh, as the TUI's terminal pane picks it.
func terminalShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// EnsureTerminal makes sure session id's worktree shell runs and answers
// with its tmux name in the Reply's Terminal: the tmux session
// tmux.TerminalSessionName(title), the one the TUI's terminal pane shares.
// Refused (precondition) unless the session is started and active with a
// worktree. The job starts the shell when no session has its name, through
// a tmux.Session, which attaches no client; a session of that name that
// started in the worktree is the instance's own and is left as it is, and
// one that did not is refused (see ensureTerminalJob).
func (m *Model) EnsureTerminal(id InstanceID, req ReqID) {
	inst, _, ok := m.admit(opTerminal, id, req)
	if !ok {
		return
	}
	title := inst.Title
	ts := tmux.NewSessionNamedWithDeps(shellSessionName(title), terminalShell(), tmux.MakePtyFactory(), m.executor())
	m.spawn(m.track(req, id, ensureTerminalJob(ts, title, inst.GetWorktreePath(), inst.SessionHome(), m.executor())))
}

// shellSessionName is the tmux name of the worktree shell of the instance
// titled title.
func shellSessionName(title string) string {
	return tmux.ToLoomTmuxName(tmux.TerminalSessionName(title))
}

// ensureTerminalJob is EnsureTerminal's job. It reads no model state: the
// session, the worktree and the home were captured when it was built.
//
//   - tmux that does not answer the has-session probe: a refusal, nothing
//     started.
//   - a session of the name that runs: the instance's own only when it
//     started in home (ownShell: fail-closed, so a listing that cannot be
//     read is a refusal too, and a session no longer listed is never
//     named). Nothing is started or closed.
//   - none: start the shell in worktree. tmux.ErrSessionExists means
//     another client made it first: the session now under the name gets
//     ownShell's judgement as one that existed, and nothing was started.
//     Any other error may come after the launch (the probe says), so the
//     result is possible, for the delivery to close if its row went.
func ensureTerminalJob(ts *tmux.Session, title, worktree string, home session.SessionHome, exec cmd2.Executor) Job {
	name := ts.SessionName()
	return func() any {
		res := terminalResult{title: title, home: home}
		switch ts.SessionLiveness() {
		case tmux.LivenessUnknown:
			res.err = refusedError{fmt.Errorf("open a terminal of %s: tmux did not answer whether its shell is running: try again", title)}
		case tmux.LivenessAlive:
			res.name, res.err = ownShell(title, name, home, exec)
		default:
			err := ts.Start(worktree)
			switch {
			case err == nil:
				res.name, res.started = name, true
			case errors.Is(err, tmux.ErrSessionExists):
				res.name, res.err = ownShell(title, name, home, exec)
			default:
				res.possible = ts.SessionLiveness() != tmux.LivenessDead
				res.err = fmt.Errorf("open a terminal of %s: %w", title, err)
				log.For("core").Warn("terminal.start_failed", "title", title, "possibly_started", res.possible, "err", err)
			}
		}
		return res
	}
}

// ownShell judges the session that holds the shell's name (name, the tmux
// name of title's shell) from one listing, and answers its name when it is
// the instance's own: started in home. It answers a refusal when another
// session holds the name (another workspace's same-titled session, or an
// agent whose title begins term_), when tmux cannot say whose it is, and
// when no session of the name is listed any more (the shell ended between
// the probe and the listing): a name that no longer runs is never handed
// out, and the caller asks again.
func ownShell(title, name string, home session.SessionHome, exec cmd2.Executor) (string, error) {
	held, listed, err := session.HeldElsewhereListed(tmux.TerminalSessionName(title), home, exec)
	switch {
	case err != nil:
		return "", refusedError{fmt.Errorf("open a terminal of %s: tmux did not answer whose the session %s is: %w", title, name, err)}
	case held:
		return "", refusedError{fmt.Errorf("open a terminal of %s: tmux session %s, which has its shell's name, was not started in its worktree: it is another session's", title, name)}
	case !listed:
		return "", refusedError{fmt.Errorf("open a terminal of %s: its shell ended while loom looked at it: try again", title)}
	}
	return name, nil
}
```

In `core/iface.go`, after the line

```go
	ReadJobLog(id InstanceID, job sched.JobID, after int64, req ReqID)
```

add (the comment holds no `rpc:` word, so the method is a plain request)

```go
	// EnsureTerminal makes sure session id's worktree shell runs (the tmux
	// session tmux.TerminalSessionName(title), shared with the TUI's terminal
	// pane) and answers with its tmux name in the Reply's Terminal. Refused
	// for a session that is not started and active with a worktree, for a
	// shell another session holds, and when tmux cannot say.
	EnsureTerminal(id InstanceID, req ReqID)
```

In `core/loop_core.go`, after

```go
func (l *Loop) ReadJobLog(id InstanceID, job sched.JobID, after int64, req ReqID) {
	l.do(func(m *Model) { m.ReadJobLog(id, job, after, req) })
}
```

add

```go
func (l *Loop) EnsureTerminal(id InstanceID, req ReqID) {
	l.do(func(m *Model) { m.EnsureTerminal(id, req) })
}
```

In `core/requests.go`, in the `requestOp` constants, after

```go
	opAnswer      requestOp = "answer"
```

add

```go
	opTerminal    requestOp = "open a terminal of"
```

In `precondition`'s doc comment, before the bullet

```go
//   - read the conversation and read the diff: no key reads them, so their
```

add

```go
//   - open a terminal: no key asks for one; the TUI's terminal pane makes its
//     shell for a started, active row with a worktree (ensureSessionLocked),
//     and a workspace terminal is not a session's shell.
```

In `precondition`'s switch, before

```go
	default:
		why = "unknown request"
```

add

```go
	case opTerminal:
		switch {
		case inst.IsWorkspaceTerminal:
			why = "not allowed on a workspace terminal"
		case st == session.Loading || st == session.Deleting:
			why = fmt.Sprintf("the session is busy (%s)", st)
		case st == session.Paused:
			why = "the session is paused"
		case st == session.Recoverable:
			why = fmt.Sprintf("the session is not running (%s)", st)
		case !inst.Started():
			why = "the session has not started"
		case inst.GetWorktreePath() == "":
			why = "the session has no worktree"
		}
```

In `deliverTracked`, after

```go
	case buildSweepResult:
		r.Sweep = res.report
```

add

```go
	case terminalResult:
		r.Terminal = res.name
```

and in `outcome`, after

```go
	case buildSweepResult:
		return r.err, nil
	case resumeSkipped:
```

add, between those two cases,

```go
	case terminalResult:
		return r.err, nil
```

In `core/model.go`, in `Deliver`, replace the line

```go
	case issueResult, turnsResult, diffResult, jobLogResult, flowsResult, flowDocResult:
```

with

```go
	case issueResult, turnsResult, diffResult, jobLogResult, flowsResult, flowDocResult, terminalResult:
```

Regenerate the wire and the protocol reference (neither file is edited by hand):

```bash
go generate ./core/rpc
go test ./core/rpc -run TestProtocolReference -update
git diff --stat -- core/rpc/methods_gen.go docs/specs/protocol.md
```

The diff shows `core/rpc/methods_gen.go` gaining `EnsureTerminalParams{ID core.InstanceID; Req core.ReqID}`, `EnsureTerminalResult`, the `kindRequest` table row after `ReadJobLog`, the server's dispatch case (which tags `Req` with the connection) and `func (c *Client) EnsureTerminal(id core.InstanceID, req core.ReqID)`; and `docs/specs/protocol.md` gaining, after the `ReadJobLog` section,

```
### EnsureTerminal (request)

Params: `{"id":0,"req":0}`

Result: `{}`
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./core/ ./core/rpc/`

Expected: `ok  	github.com/aidan-bailey/loom/core` and `ok  	github.com/aidan-bailey/loom/core/rpc`; among them `TestEnsureTerminal_…`, `TestTerminalShell_IsTheEnvironmentsElseBinSh`, `TestRequests_RefuseWhatTheTUIRefuses`, `TestLoopForwardsEachMethodToItsNamesake`, `TestGenerated_IsFresh`, `TestMethods_CoverCore`, `TestCoreIsValueTyped` and `TestProtocolReference` pass.

- [ ] **Step 5: Commit**

```bash
git add -N core/terminal.go core/terminal_test.go
git commit -m "feat(core,rpc): Core.EnsureTerminal makes a session's worktree shell" -- core/terminal.go core/terminal_test.go core/iface.go core/loop_core.go core/requests.go core/requests_test.go core/model.go core/rpc/methods_gen.go docs/specs/protocol.md
```

### Task 2.4: A shell made for a row that went is closed on delivery

**Files:**
- Modify: `core/terminal.go` (`recheckTerminal`, `closeShellJob`)
- Modify: `core/requests.go` (`deliverTracked`)
- Test: `core/terminal_test.go`

- [ ] **Step 1: Write the failing tests**

At the end of `core/terminal_test.go`, add:

```go
// shellRow is a started, active session titled "web" in dir, served by a
// model whose EnsureTerminal(…, 4) job has not run yet.
func shellRow(t *testing.T, dir string) (*Model, *session.Instance) {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	killSessionAtEnd(t, tmux.TerminalSessionName("web"))
	inst := shellInst(t, "web", dir)
	return shellModel(t, inst), inst
}

// ensureJob queues EnsureTerminal(…, 4) and returns its one job.
func ensureJob(t *testing.T, m *Model, inst *session.Instance) Job {
	t.Helper()
	m.EnsureTerminal(m.idOf(inst), 4)
	out := m.Drain()
	require.Len(t, out.Jobs, 1)
	return out.Jobs[0]
}

// The race the TUI's own comment records, closed for this path: a kill or a
// pause that begins (or finishes) while the job makes the shell must leave
// none behind. On delivery the model finds the row no longer started and
// active, closes the shell it made in a job of its own, and refuses.
func TestEnsureTerminal_AShellMadeForARowThatWentIsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		change func(t *testing.T, m *Model, inst *session.Instance)
	}{
		{"a kill begins", func(t *testing.T, m *Model, inst *session.Instance) {
			require.NoError(t, inst.TransitionTo(session.Deleting))
		}},
		{"a pause begins", func(t *testing.T, m *Model, inst *session.Instance) {
			require.NoError(t, inst.TransitionTo(session.Loading))
		}},
		{"the row is gone", func(t *testing.T, m *Model, inst *session.Instance) {
			m.removeEverywhere(inst)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			m, inst := shellRow(t, t.TempDir())
			result := ensureJob(t, m, inst)()
			require.True(t, shellAlive(t, "web"), "fixture: the job made the shell")
			tc.change(t, m, inst)

			m.Deliver(result)
			after := m.Drain()

			r := replyTo(after.Events, 4)
			require.NotNil(t, r)
			assert.ErrorIs(t, r.Err, ErrRefused)
			assert.Empty(t, r.Terminal, "no name is handed out for a shell that is being closed")
			require.Len(t, after.Jobs, 1, "the shell is closed by a job of its own")
			m.Deliver(after.Jobs[0]())
			assert.False(t, shellAlive(t, "web"), "no shell is left behind")
		})
	}
}

// The other side: a row still started and active keeps its shell, and the
// Reply names it.
func TestEnsureTerminal_AShellMadeForAnActiveRowIsKept(t *testing.T) {
	isolateTmux(t)
	m, inst := shellRow(t, t.TempDir())
	result := ensureJob(t, m, inst)()

	m.Deliver(result)
	after := m.Drain()

	r := replyTo(after.Events, 4)
	require.NotNil(t, r)
	require.NoError(t, r.Err)
	assert.Equal(t, shellName("web"), r.Terminal)
	assert.Empty(t, after.Jobs, "nothing is closed")
	assert.True(t, shellAlive(t, "web"))
}

// A shell that existed before the request is never closed here, whatever
// became of the row: pause and kill own it. The Reply is still a refusal,
// since a kill or pause is closing that shell and nothing may attach to it.
func TestEnsureTerminal_AShellThatExistedIsNotClosedWhenTheRowGoes(t *testing.T) {
	isolateTmux(t)
	dir := t.TempDir()
	m, inst := shellRow(t, dir)
	startForeignSession(t, tmux.TerminalSessionName("web"), dir)
	result := ensureJob(t, m, inst)()
	require.NoError(t, inst.TransitionTo(session.Deleting))

	m.Deliver(result)
	after := m.Drain()

	r := replyTo(after.Events, 4)
	require.NotNil(t, r)
	assert.ErrorIs(t, r.Err, ErrRefused, "nothing attaches to a shell a kill is closing")
	assert.Empty(t, r.Terminal, "no name is handed out")
	assert.Empty(t, after.Jobs, "no close job: the job started nothing")
	assert.True(t, shellAlive(t, "web"), "the shell that was there runs on")
}

// A shell another client made between the job's probe and its start is no
// more the job's than one that existed: it is not closed, and the Reply is
// a refusal all the same.
func TestEnsureTerminal_AShellAnotherClientMadeIsNotClosedWhenTheRowGoes(t *testing.T) {
	isolateTmux(t)
	dir := t.TempDir()
	m, inst := shellRow(t, dir)
	var probes int
	var ran []string
	m.SetExecForTest(racingExec(&probes, &ran, shellName("web")+"\t"+dir+"\n"))
	result := ensureJob(t, m, inst)()
	require.NoError(t, inst.TransitionTo(session.Deleting))

	m.Deliver(result)
	after := m.Drain()

	r := replyTo(after.Events, 4)
	require.NotNil(t, r)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.Empty(t, r.Terminal)
	assert.Empty(t, after.Jobs)
	assert.NotContains(t, ran, "kill-session")
}

// The close job confirms the session still starts in the instance's
// worktree: a session another client put under the name meanwhile is left
// running, and nothing is killed.
func TestEnsureTerminal_TheCloseLeavesASessionStartedElsewhere(t *testing.T) {
	isolateTmux(t)
	m, inst := shellRow(t, t.TempDir())
	result := ensureJob(t, m, inst)()
	require.True(t, shellAlive(t, "web"), "fixture: the job made the shell")
	require.NoError(t, tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget(shellName("web"))).Run())
	elsewhere := t.TempDir()
	startForeignSession(t, tmux.TerminalSessionName("web"), elsewhere)
	require.NoError(t, inst.TransitionTo(session.Deleting))

	m.Deliver(result)
	after := m.Drain()
	require.Len(t, after.Jobs, 1)
	m.Deliver(after.Jobs[0]())

	assert.Equal(t, canonical(t, elsewhere), canonical(t, sessionPath(t, tmux.TerminalSessionName("web"))), "the other session runs on")
}

// A launch that failed after tmux may have made the shell leaves it
// possibly started: when its row went it is closed if it runs in the
// worktree, and the Reply keeps the job's own error; when the row is
// active it is left, and the error stands.
func TestEnsureTerminal_APossiblyStartedShellIsClosedWhenTheRowGoes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rowGone    bool
		wantClosed bool
	}{
		{"the row went", true, true},
		{"the row is active", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			dir := t.TempDir()
			m, inst := shellRow(t, dir)
			startForeignSession(t, tmux.TerminalSessionName("web"), dir) // what tmux made before the launch failed
			if tc.rowGone {
				require.NoError(t, inst.TransitionTo(session.Deleting))
			}

			m.Deliver(tracked{req: 6, id: m.idOf(inst), result: terminalResult{
				title: "web", home: inst.SessionHome(), possible: true, err: errors.New("open a terminal of web: boom"),
			}})
			after := m.Drain()

			r := replyTo(after.Events, 6)
			require.NotNil(t, r)
			assert.EqualError(t, r.Err, "open a terminal of web: boom", "the job's own error stands")
			assert.Empty(t, r.Terminal)
			if tc.wantClosed {
				require.Len(t, after.Jobs, 1)
				m.Deliver(after.Jobs[0]())
			} else {
				assert.Empty(t, after.Jobs)
			}
			assert.Equal(t, !tc.wantClosed, shellAlive(t, "web"))
		})
	}
}
```

Fails without: `TestEnsureTerminal_AShellMadeForARowThatWentIsClosed`: `recheckTerminal` not called from `deliverTracked` (the Reply succeeds, no close job and the shell stays), or its `inst == nil ||` term removed (the "the row is gone" case sees a nil instance as active and panics or keeps the shell), or its `!activeInstance(inst)` term removed (the kill and pause cases keep the shell); `TestEnsureTerminal_AShellMadeForAnActiveRowIsKept`: the active test inverted (an active row's shell is closed); `TestEnsureTerminal_AShellThatExistedIsNotClosedWhenTheRowGoes`: the `if res.started || res.possible` guard around the close job removed (a close job is queued for a shell the job did not start), or the early return for an active row widened to every result with a name (the Reply names the shell a kill is closing: no `ErrRefused`); `TestEnsureTerminal_AShellAnotherClientMadeIsNotClosedWhenTheRowGoes`: the same guard removed, or the job marking an `ErrSessionExists` shell `started`; `TestEnsureTerminal_TheCloseLeavesASessionStartedElsewhere`: the `session.HeldElsewhere` check removed from `closeShellJob` (it kills the other session); `TestEnsureTerminal_APossiblyStartedShellIsClosedWhenTheRowGoes`: `|| res.possible` removed from the close guard (the shell stays when its row went), or the row test dropped (the shell is closed while its row is active).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestEnsureTerminal_'`

Expected: the new tests fail: `TestEnsureTerminal_AShellMadeForARowThatWentIsClosed` reports `Expected error to be ... ErrRefused` (the Reply succeeds), `TestEnsureTerminal_TheCloseLeavesASessionStartedElsewhere` and `…APossiblyStartedShellIsClosedWhenTheRowGoes` report `Should have 1 item(s), but has 0`, and the shell is still alive; the tests of Task 2.3 still pass.

- [ ] **Step 3: Implement**

In `core/terminal.go`, add after `ensureTerminalJob`:

```go
// recheckTerminal is EnsureTerminal's delivery check, run on the model's
// goroutine before the result is delivered: it looks the instance up again
// by the id the result carries (identity, not title or position), because a
// kill or pause that began while the job ran has taken its shell by name
// already, or will. When the instance is gone or no longer started and
// active the result becomes a refusal, whoever made the shell, so nothing
// attaches to a shell a pause or kill is closing (a refusal the job already
// made stands). When the job also made the shell (or may have), it is
// closed by a job of its own (closeShellJob). A shell that existed before
// the request, or was made by another client meanwhile, is never closed
// here: pause and kill own it.
func (m *Model) recheckTerminal(req ReqID, id InstanceID, res terminalResult) terminalResult {
	if inst, _ := m.lookup(id); inst != nil && activeInstance(inst) {
		return res
	}
	if res.started || res.possible {
		ts := tmux.NewSessionNamedWithDeps(shellSessionName(res.title), "", tmux.MakePtyFactory(), m.executor())
		m.causedBy(req, func() { m.spawn(closeShellJob(ts, res.title, res.home, m.executor())) })
	}
	if res.err == nil {
		res.err = refusedError{fmt.Errorf("open a terminal of %s: the session was paused or killed while its shell was being opened", res.title)}
	}
	res.name = ""
	return res
}

// closeShellJob is the job that closes the shell a request made for a row
// that went: a kill-session on its exact name (Session.Close), after
// confirming the session under the name still starts in the instance's
// worktree (session.HeldElsewhere). Another client may have replaced the
// shell meanwhile, and a session another workspace holds must not be
// killed; a listing that cannot be read closes nothing. It reads no model
// state.
func closeShellJob(ts *tmux.Session, title string, home session.SessionHome, exec cmd2.Executor) Job {
	return func() any {
		held, err := session.HeldElsewhere(tmux.TerminalSessionName(title), home, exec)
		if err != nil || held {
			log.For("core").Warn("terminal.close_skipped", "title", title, "held_elsewhere", held, "err", err)
			return nil
		}
		if err := ts.Close(); err != nil {
			log.For("core").Debug("terminal.close_failed", "title", title, "err", err)
		}
		return nil
	}
}
```

In `core/requests.go`, in `deliverTracked`, replace

```go
func (m *Model) deliverTracked(t tracked) {
	m.causedBy(t.req, func() { m.Deliver(t.result) })
```

with

```go
func (m *Model) deliverTracked(t tracked) {
	if res, ok := t.result.(terminalResult); ok {
		t.result = m.recheckTerminal(t.req, t.id, res)
	}
	m.causedBy(t.req, func() { m.Deliver(t.result) })
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./core/ ./core/rpc/`

Expected: `ok  	github.com/aidan-bailey/loom/core` and `ok  	github.com/aidan-bailey/loom/core/rpc`; among them every `TestEnsureTerminal_…` test, `TestCause_…` and `TestLoopForwardsEachMethodToItsNamesake` pass.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core): EnsureTerminal closes the shell it made for a row that went" -- core/terminal.go core/requests.go core/terminal_test.go
```

### Task 2.5: `core/README.md` names the terminal switch and `EnsureTerminal`

This brings `core/README.md` into line for Story 1's additions (`WebView.Terminal`, `SetWebTerminal`, the `terminal` key of `web.json`) and this story's (`EnsureTerminal`, `Reply.Terminal`, `core/terminal.go`) in one pass. The README holds facts, so the edits state what the code does and add no rule.

**Files:**
- Modify: `core/README.md:43` (the `web.go`, `web_store.go` row, and a new `terminal.go` row before the `view.go` row at line 45), `core/README.md:90` (the requests paragraph), `core/README.md:121` (`ModelChanged`), `core/README.md:190` (the `web.json` paragraph)

- [ ] **Step 1: Edit the README**

In `core/README.md`, in the `web.go`, `web_store.go` row, replace

```
`WebView`, `WebDevice`, `NotifySettings`
```

with

```
`WebView` (its `Terminal` the web terminal's switch), `WebDevice`, `NotifySettings`
```

and replace

```
the requests `ConfigureWeb`, `PairDevice` and `RevokeDevice`, and `web.json` (read, atomic write, the VAPID key) |
```

with

```
the requests `ConfigureWeb`, `SetWebTerminal`, `PairDevice` and `RevokeDevice`, and `web.json` (read, atomic write, the VAPID key, the terminal switch) |
```

Before the row that begins

```
| `view.go`, `views.go` |
```

add a row (the table's rows are one line each):

```
| `terminal.go` | `EnsureTerminal`: a session's worktree shell, the tmux session `tmux.TerminalSessionName(title)`, made by a job (`ensureTerminalJob`) that judges a session already under the name by where it started (`ownShell`), and checked again on delivery (`recheckTerminal`), which refuses for a row that went and closes a shell the job made for it (`closeShellJob`); the shell's tmux name is the Reply's `Terminal`. Tests: `core/terminal_test.go` |
```

In the requests paragraph (the one beginning `Every lifecycle action is a request`), replace

```
whose gates are the model's own since no key reads them. A non-zero `ReqID` is answered by a `Reply` (`Req`, `ID`, `Err`, `Notice`, `Issue`, `Turns`, `Diff`, `Mockup`, `Sweep`, `Flow`, `Flows`, `FlowDoc`, `Watch`):
```

with

```
whose gates are the model's own since no key reads them. `EnsureTerminal(id, req)` makes sure a session's worktree shell runs and answers with its tmux name (`core/terminal.go`): refused for a session not started and active, with no worktree, or a workspace terminal, and when a session under the shell's name started elsewhere or tmux cannot say whose it is. A non-zero `ReqID` is answered by a `Reply` (`Req`, `ID`, `Err`, `Notice`, `Issue`, `Turns`, `Diff`, `Mockup`, `Sweep`, `Flow`, `Flows`, `FlowDoc`, `Watch`, `Terminal`):
```

In the `ModelChanged` line, replace

```
`Web`, the web settings, devices and quick replies, and `BuildSweep`
```

with

```
`Web`, the web settings, devices, quick replies and terminal switch, and `BuildSweep`
```

In the `web.json` paragraph (under `## The web settings and devices`), replace

```
the quick replies, the VAPID key pair
```

with

```
the quick replies, the `terminal` switch (left out while off), the VAPID key pair
```

replace

```
`ConfigureWeb` and `RevokeDevice` refuse,
```

with

```
`ConfigureWeb`, `SetWebTerminal` and `RevokeDevice` refuse,
```

and after the sentence

```
`RevokeDevice(name)` deletes the device and its push subscription.
```

add

```
`SetWebTerminal(on)` sets the terminal switch (a paired device may open a session's worktree shell only while it is on) and writes the file: it works while the web is off, writes nothing when the value does not change, puts the old value back when the write fails, and never bumps `WebView.Rev`, which would rebind the listener and end every stream.
```

- [ ] **Step 2: Run the doc checks**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ -run TestRepo_ClaudeMDStructure`

Expected: `ok  	github.com/aidan-bailey/loom/tools/claudemd`; the README's paths and test names resolve (`core/terminal.go`, `core/terminal_test.go`).

- [ ] **Step 3: Commit**

```bash
git commit -m "docs(core): name the web terminal's switch and EnsureTerminal in the README" -- core/README.md
```

## Story 3: `ShellAttach`: a pty attach client with nothing else

- **Developer:** intermediate-dev
- **Files:** `session/tmux/shell_attach.go`, `session/tmux/shell_attach_test.go`, `session/tmux/shell_attach_realtmux_test.go`, `session/tmux/CLAUDE.md`, `session/tmux/README.md`
- **Interfaces:** exposes `ShellAttach`, `StartShellAttach` and `ShellHistory` to Story 7; uses none
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./session/tmux/ && GOOS=windows CGO_ENABLED=0 go build ./...'`
- **Review:** deep
- **Form:** full (a pty, a child process and a read that must end on close)

`StartShellAttach` runs `tmux attach-session` on the exact target (`SessionTarget`) in a pty from the package's `PtyFactory`, sets the pty's size as soon as the client has started (`setPtySize`, as the TUI's attach does; tmux redraws to it) and returns a `*ShellAttach` that reads, writes, resizes and closes. It holds no emulator, monitor, pump or status state, and `Close` ends the client (it closes the pty and kills the client process, since a blocking pty ends no pending read), never the session. `ShellHistory` captures the rows above the visible screen, bounded by `paneScrollbackLines` and ended by an SGR reset (`capture-pane -e` leaves the last row's colours on); it asks `#{history_size}` first, because `capture-pane -S -N -E -1` on a pane with no history prints the first visible row instead of nothing (checked on tmux 3.7c). Unit tests run against a real pseudo-terminal pair, and real-tmux tests on the private server attach, type, read the echo, resize and find the session alive after `Close`, and read a pane's history. The package's rules gain: `ShellAttach.Close` never closes the session. The code holds none of the profile's landmine markers. Run every build and test command below through `loom job run -d "<what>" --` (wrap an env-prefixed command as `sh -c '…'`), as the session's rules say.

### Task 3.1: `ShellAttach` and `StartShellAttach`

**Files:**
- Create: `session/tmux/shell_attach.go`
- Create: `session/tmux/shell_attach_test.go`

- [ ] **Step 1: Write the failing tests**

Create `session/tmux/shell_attach_test.go`. Its helpers are named apart from the package's other test helpers (`pairFactory`, `quietRunner`, `argvRecorder`, `MockPtyFactory`):

```go
//go:build !windows

package tmux

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/testpty"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shellPtyFactory hands StartShellAttach the master of a real pseudo-terminal
// pair, made pollable as Pty.Start's are, and keeps the slave, which stands
// in for the tmux client: what the test writes to it arrives as the client's
// output, and what the attach writes arrives on it as the client's input.
// A real pair has a real window size, which setPtySize sets and the test
// reads back from the slave.
type shellPtyFactory struct {
	t        *testing.T
	startErr error
	cmds     []*exec.Cmd
	slaves   []*os.File
}

func (f *shellPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	f.t.Cleanup(func() { _ = slave.Close() })
	polled, err := pollable(master)
	if err != nil {
		return nil, err
	}
	f.cmds = append(f.cmds, cmd)
	f.slaves = append(f.slaves, slave)
	return polled, nil
}

func (f *shellPtyFactory) Close() {}

// blockingPtyFactory hands StartShellAttach the master of a real pair as it
// comes, blocking (creack/pty takes its fd through os.File.Fd, which makes it
// so, and no pollable call here undoes that): the master Pty.Start falls back
// to where the poller refuses a PTY (Darwin), on which closing the file does
// not end a pending Read. A
// real child (`sleep`) stands in for the tmux client: it holds the slave as
// its stdio, the factory keeps no copy, so the master reads EIO once the child
// ends, and cmd.Process is set as pty.Start sets it. exited closes when the
// child has been reaped; child is the process, for a test that ends it itself.
type blockingPtyFactory struct {
	t      *testing.T
	exited chan struct{}
	child  *os.Process
}

func (f *blockingPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	child := exec.Command("sleep", "60")
	child.Stdin, child.Stdout, child.Stderr = slave, slave, slave
	if err := child.Start(); err != nil {
		_ = master.Close()
		_ = slave.Close()
		return nil, err
	}
	_ = slave.Close()
	cmd.Process = child.Process
	f.child = child.Process
	f.t.Cleanup(func() { _ = child.Process.Kill() })
	go func() {
		_ = child.Wait()
		close(f.exited)
	}()
	return master, nil
}

func (f *blockingPtyFactory) Close() {}

// fixedPtyFactory hands StartShellAttach one file it was given.
type fixedPtyFactory struct{ f *os.File }

func (p fixedPtyFactory) Start(*exec.Cmd) (*os.File, error) { return p.f, nil }
func (p fixedPtyFactory) Close()                            {}

// startShell attaches to "loom_term_demo" through a shellPtyFactory.
func startShell(t *testing.T, cols, rows int) (*ShellAttach, *shellPtyFactory) {
	t.Helper()
	f := &shellPtyFactory{t: t}
	a, err := StartShellAttach("loom_term_demo", cols, rows, f)
	require.NoError(t, err)
	require.Len(t, f.slaves, 1, "fixture: the attach started one client")
	t.Cleanup(func() { _ = a.Close() })
	return a, f
}

// readN reads n bytes from r, failing the test when they do not arrive
// within two seconds.
func readN(t *testing.T, r io.Reader, n int) string {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, n)
		_, err := io.ReadFull(r, buf)
		done <- result{buf, err}
	}()
	select {
	case res := <-done:
		require.NoError(t, res.err)
		return string(res.data)
	case <-time.After(2 * time.Second):
		t.Fatal("the bytes never arrived")
		return ""
	}
}

// ptySize is the window size the slave sees.
func ptySize(t *testing.T, slave *os.File) (cols, rows int) {
	t.Helper()
	ws, err := pty.GetsizeFull(slave)
	require.NoError(t, err)
	return int(ws.Cols), int(ws.Rows)
}

func TestStartShellAttach_RunsAttachSessionOnTheExactTarget(t *testing.T) {
	_, f := startShell(t, 80, 24)

	cmd := f.cmds[0]
	assert.Equal(t, []string{"tmux", "-u", "attach-session", "-t", "=loom_term_demo"}, cmd.Args,
		"the exact target, never a bare name that prefix-matches another session")
	require.NotEmpty(t, cmd.Env)
	assert.Equal(t, "TERM=xterm-256color", cmd.Env[len(cmd.Env)-1],
		"a daemon started by a service has no TERM, and tmux refuses a client without one")
}

func TestStartShellAttach_SizesThePTYAtOnce(t *testing.T) {
	_, f := startShell(t, 100, 30)

	cols, rows := ptySize(t, f.slaves[0])

	assert.Equal(t, 100, cols)
	assert.Equal(t, 30, rows)
}

func TestStartShellAttach_RefusesWhatItCannotTargetOrSize(t *testing.T) {
	for _, tc := range []struct {
		name       string
		session    string
		cols, rows int
	}{
		{"an empty name", "", 80, 24},
		{"a name with a colon", "loom_term_a:b", 80, 24},
		{"a name with a dot", "loom_term_a.b", 80, 24},
		{"no columns", "loom_term_demo", 0, 24},
		{"no rows", "loom_term_demo", 80, 0},
		{"negative columns", "loom_term_demo", -1, 24},
		{"more columns than a window size holds", "loom_term_demo", 1 << 16, 24},
		{"more rows than a window size holds", "loom_term_demo", 80, 1 << 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &shellPtyFactory{t: t}

			a, err := StartShellAttach(tc.session, tc.cols, tc.rows, f)

			require.Error(t, err)
			assert.Nil(t, a)
			assert.Empty(t, f.cmds, "no client was started")
		})
	}
}

func TestStartShellAttach_ReturnsAPTYFactoryError(t *testing.T) {
	boom := errors.New("no ptys left")
	f := &shellPtyFactory{t: t, startErr: boom}

	a, err := StartShellAttach("loom_term_demo", 80, 24, f)

	require.ErrorIs(t, err, boom)
	assert.Nil(t, a)
}

// A pty that refuses its size (a socket pair answers ENOTTY) ends the client
// rather than leaving one at a size nobody chose.
func TestStartShellAttach_ASizeThePTYRefusesEndsTheClient(t *testing.T) {
	attach, _ := testpty.Pair(t)

	a, err := StartShellAttach("loom_term_demo", 80, 24, fixedPtyFactory{attach})

	require.Error(t, err)
	assert.Nil(t, a)
	_, werr := attach.Write([]byte("x"))
	assert.ErrorIs(t, werr, os.ErrClosed, "the PTY was closed")
}

func TestShellAttach_ReadsTheClientsRawOutput(t *testing.T) {
	a, f := startShell(t, 80, 24)
	out := "\x1b[?1049h\x1b[31mred"

	_, err := f.slaves[0].Write([]byte(out))
	require.NoError(t, err)

	assert.Equal(t, out, readN(t, a, len(out)), "unfiltered: the alternate-screen switch is still there")
}

func TestShellAttach_WritesGoToTheClientsInput(t *testing.T) {
	a, f := startShell(t, 80, 24)

	n, err := a.Write([]byte("typed\n"))

	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, "typed\n", readN(t, f.slaves[0], 6))
}

func TestShellAttach_ResizeChangesThePTYAndRefusesABadSize(t *testing.T) {
	a, f := startShell(t, 80, 24)

	require.NoError(t, a.Resize(120, 40))
	cols, rows := ptySize(t, f.slaves[0])
	assert.Equal(t, 120, cols)
	assert.Equal(t, 40, rows)

	require.Error(t, a.Resize(0, 40))
	require.Error(t, a.Resize(120, -5))
	require.Error(t, a.Resize(1<<16, 40))
	cols, rows = ptySize(t, f.slaves[0])
	assert.Equal(t, 120, cols, "a refused size changes nothing")
	assert.Equal(t, 40, rows)
}

func TestShellAttach_CloseEndsABlockedReadAndIsSafeTwice(t *testing.T) {
	a, _ := startShell(t, 80, 24)
	read := make(chan error, 1)
	go func() {
		_, err := a.Read(make([]byte, 16))
		read <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the read block

	require.NoError(t, a.Close())

	select {
	case err := <-read:
		assert.ErrorIs(t, err, io.EOF, "a Read ended by Close reads EOF")
	case <-time.After(2 * time.Second):
		t.Fatal("Close left a Read blocked")
	}
	assert.NoError(t, a.Close(), "a second Close is safe")
	_, err := a.Write([]byte("x"))
	assert.Error(t, err, "nothing is written after Close")
	assert.Error(t, a.Resize(80, 24), "nor resized")
}

// Where the poller refuses a PTY the master is blocking, and closing it does
// not end a Read in progress: Close must end the client process, which closes
// the slave and so wakes the Read, and it must leave no client attached.
func TestShellAttach_CloseEndsTheClientProcessWhereTheMasterIsBlocking(t *testing.T) {
	f := &blockingPtyFactory{t: t, exited: make(chan struct{})}
	a, err := StartShellAttach("loom_term_demo", 80, 24, f)
	require.NoError(t, err)
	read := make(chan error, 1)
	go func() {
		_, err := a.Read(make([]byte, 16))
		read <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the read block

	require.NoError(t, a.Close())

	select {
	case <-f.exited:
	case <-time.After(2 * time.Second):
		t.Fatal("Close left the client process running")
	}
	select {
	case err := <-read:
		assert.ErrorIs(t, err, io.EOF, "the Read the client's end woke reads EOF")
	case <-time.After(2 * time.Second):
		t.Fatal("Close left a Read blocked on a blocking master")
	}
	assert.NoError(t, a.Close(), "a second Close is safe, the client already ended")
}

// A client that ended by itself has been reaped (Pty.Start waits on it), and
// Kill on a reaped process returns os.ErrProcessDone: the first Close is not
// an error for that.
func TestShellAttach_CloseOfAClientThatAlreadyEndedIsNotAnError(t *testing.T) {
	f := &blockingPtyFactory{t: t, exited: make(chan struct{})}
	a, err := StartShellAttach("loom_term_demo", 80, 24, f)
	require.NoError(t, err)
	require.NoError(t, f.child.Kill())
	select {
	case <-f.exited: // killed and reaped, before any Close
	case <-time.After(2 * time.Second):
		t.Fatal("the client did not end")
	}

	assert.NoError(t, a.Close(), "a client that already ended is not an error to Close")
}

// The client ending by itself (the session died, so tmux exits and its end of
// the pty closes) is an error to the reader, never a Read that returns nothing
// and no error.
func TestShellAttach_ReadReturnsAnErrorOnceTheClientEnds(t *testing.T) {
	a, f := startShell(t, 80, 24)
	read := make(chan error, 1)
	go func() {
		_, err := a.Read(make([]byte, 16))
		read <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the read block

	require.NoError(t, f.slaves[0].Close())

	select {
	case err := <-read:
		assert.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("a client that ended left a Read blocked")
	}
}
```

Fails without: `TestStartShellAttach_RunsAttachSessionOnTheExactTarget`: `SessionTarget(name)` replaced by `name` (the argv carries a bare name), or the `TERM=` entry left out; `TestStartShellAttach_SizesThePTYAtOnce`: the `setPtySize` call in `StartShellAttach` removed (the slave reads 0x0); `TestStartShellAttach_RefusesWhatItCannotTargetOrSize`: the `ExactlyTargetable` check removed (the colon and dot names start a client) or `checkShellSize` removed from `StartShellAttach` (a zero or oversized size starts one); `TestStartShellAttach_ReturnsAPTYFactoryError`: the factory's error swallowed or not wrapped with `%w`; `TestStartShellAttach_ASizeThePTYRefusesEndsTheClient`: the `ptmx.Close()` on a failed `setPtySize` removed (the write to the PTY succeeds); `TestShellAttach_ReadsTheClientsRawOutput`: `Read` wrapped in `vt.NewAltScreenFilter` (the `1049h` is dropped, so the read comes up short and times out); `TestShellAttach_WritesGoToTheClientsInput`: `Write` discarding its bytes; `TestShellAttach_ResizeChangesThePTYAndRefusesABadSize`: `Resize` a no-op (the size stays 80x24), or without `checkShellSize` (`Resize(0, 40)` sets zero columns and the last assertions read 0); `TestShellAttach_CloseEndsABlockedReadAndIsSafeTwice`: `Close` not closing the PTY (the read stays blocked), without the `sync.Once` (the second `Close` returns `os.ErrClosed`), or `Read` not mapping an ended read to `io.EOF` after `Close`; `TestShellAttach_CloseEndsTheClientProcessWhereTheMasterIsBlocking`: the `cmd.Process.Kill()` in `Close` removed (the `sleep` child is never ended: `exited` never closes and the Read stays blocked); `TestShellAttach_CloseOfAClientThatAlreadyEndedIsNotAnError`: the `os.ErrProcessDone` exemption in `endShellClient` removed (the first `Close` finds the reaped child, `Kill` returns `os.ErrProcessDone` and `Close` returns it); `TestShellAttach_ReadReturnsAnErrorOnceTheClientEnds`: `Read` turning an error into `0, nil` (the read does not return an error).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestStartShellAttach_|TestShellAttach_'`

Expected: the package does not build: `session/tmux/shell_attach_test.go: undefined: StartShellAttach` and `undefined: ShellAttach`.

- [ ] **Step 3: Implement**

Create `session/tmux/shell_attach.go`:

```go
package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// shellAttachTerm is the TERM a shell attach client runs under. A daemon
// started by a service has none, and tmux refuses a client without a usable
// one ("open terminal failed"); xterm.js, which draws the client's output,
// is an xterm-256color terminal.
const shellAttachTerm = "TERM=xterm-256color"

// maxShellDim is the largest column or row count a pty's window size holds
// (a uint16).
const maxShellDim = 1<<16 - 1

// ShellAttach is one `tmux attach-session` client in a pty, for the web
// layer's terminal (internal/web): the browser's keys go in through Write
// and the client's raw output comes out of Read. It is not a TmuxSession: it
// holds no emulator, monitor, pump or status state, and nothing here reads
// the pane but the caller. Close ends the client, never the session.
type ShellAttach struct {
	ptmx      *os.File
	cmd       *exec.Cmd // the client; its Process is nil for a factory that starts none (a fake)
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

// checkShellSize refuses a size a pty's window size cannot hold.
func checkShellSize(cols, rows int) error {
	if cols < 1 || rows < 1 || cols > maxShellDim || rows > maxShellDim {
		return fmt.Errorf("the size %dx%d is outside 1..%d", cols, rows, maxShellDim)
	}
	return nil
}

// endShellClient ends a client: it closes the pty, and kills the client
// process where the factory started one. Closing the pty is not enough where
// the master is blocking (Pty.Start falls back to one where the poller refuses
// a PTY, as on Darwin): Close then neither ends a Read in progress nor
// detaches the client, whose end closes the slave and wakes the Read. Process
// is os.Process, which kills the same way on every platform; a client that has
// already gone is not an error.
func endShellClient(ptmx *os.File, cmd *exec.Cmd) error {
	err := ptmx.Close()
	if cmd.Process != nil {
		if kerr := cmd.Process.Kill(); kerr != nil && !errors.Is(kerr, os.ErrProcessDone) {
			err = errors.Join(err, fmt.Errorf("kill the client: %w", kerr))
		}
	}
	return err
}

// StartShellAttach attaches a client to the tmux session called name (as
// Reply.Terminal gives it, ready for SessionTarget) in a pty from
// ptyFactory. The pty is set to cols x rows as soon as the client has
// started, as the TUI's attach does, and tmux redraws to it. A name that
// cannot be targeted exactly, or a size a pty cannot hold, starts nothing;
// a pty that refuses its size ends the client (endShellClient).
func StartShellAttach(name string, cols int, rows int, ptyFactory PtyFactory) (*ShellAttach, error) {
	if !ExactlyTargetable(name) {
		return nil, fmt.Errorf("shell attach: %q cannot be targeted exactly", name)
	}
	if err := checkShellSize(cols, rows); err != nil {
		return nil, fmt.Errorf("shell attach: %w", err)
	}
	cmd := Command(context.Background(), "attach-session", "-t", SessionTarget(name))
	cmd.Env = append(cmd.Environ(), shellAttachTerm)
	ptmx, err := ptyFactory.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("shell attach: open the PTY: %w", err)
	}
	if err := setPtySize(ptmx, cols, rows); err != nil {
		_ = endShellClient(ptmx, cmd)
		return nil, fmt.Errorf("shell attach: size the PTY: %w", err)
	}
	return &ShellAttach{ptmx: ptmx, cmd: cmd}, nil
}

// Read reads the client's raw output, unfiltered. It returns io.EOF once
// Close has ended it, and the PTY's own error once the client has ended by
// itself (the session is gone).
func (a *ShellAttach) Read(p []byte) (int, error) {
	n, err := a.ptmx.Read(p)
	if err != nil && a.closed.Load() {
		err = io.EOF
	}
	return n, err
}

// Write sends p to the client as input, which tmux passes to the pane.
func (a *ShellAttach) Write(p []byte) (int, error) {
	return a.ptmx.Write(p)
}

// Resize sets the pty's size, which tmux follows (its latest-client rule
// applies). It goes through setPtySize, so a Read in progress stays
// interruptible.
func (a *ShellAttach) Resize(cols int, rows int) error {
	if err := checkShellSize(cols, rows); err != nil {
		return fmt.Errorf("shell attach: %w", err)
	}
	return setPtySize(a.ptmx, cols, rows)
}

// Close ends the client (endShellClient: the pty closed and the client
// process killed), which detaches it and ends a Read in progress. It never
// closes the session: the shell is the TUI terminal pane's too, and a browser
// leaving must not end it. Safe to call twice.
func (a *ShellAttach) Close() error {
	a.closeOnce.Do(func() {
		a.closed.Store(true)
		a.closeErr = endShellClient(a.ptmx, a.cmd)
	})
	return a.closeErr
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestStartShellAttach_|TestShellAttach_'`

Expected: `ok  	github.com/aidan-bailey/loom/session/tmux`. Then `GOOS=windows CGO_ENABLED=0 go build ./...` prints nothing (the file uses `setPtySize`, which both platforms define, and `os.Process.Kill`, which the `os` package defines on both).

- [ ] **Step 5: Commit**

```bash
git add -N session/tmux/shell_attach.go session/tmux/shell_attach_test.go
git commit -m "feat(tmux): ShellAttach, a pty attach client with no emulator or status state" -- session/tmux/shell_attach.go session/tmux/shell_attach_test.go
```

### Task 3.2: `ShellHistory`

**Files:**
- Modify: `session/tmux/shell_attach.go` (the import block, and a function appended)
- Test: `session/tmux/shell_attach_test.go`

- [ ] **Step 1: Write the failing tests**

In `session/tmux/shell_attach_test.go`, replace the import block

```go
import (
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/testpty"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

with

```go
import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/internal/testpty"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

and append to the end of the file:

```go
// historyTmux answers ShellHistory's two tmux commands, display-message with
// size and capture-pane with rows, and records every argv.
type historyTmux struct {
	size       string // what display-message prints
	sizeErr    error
	rows       string // what capture-pane prints
	captureErr error
	argvs      [][]string
}

func (h *historyTmux) runner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{OutputFunc: func(c *exec.Cmd) ([]byte, error) {
		h.argvs = append(h.argvs, slices.Clone(c.Args))
		switch cmd_test.TmuxSubcommand(c.Args) {
		case "display-message":
			return []byte(h.size), h.sizeErr
		case "capture-pane":
			return []byte(h.rows), h.captureErr
		}
		return nil, fmt.Errorf("unexpected tmux command %v", c.Args)
	}}
}

// captures are the capture-pane argvs run so far.
func (h *historyTmux) captures() [][]string {
	var out [][]string
	for _, argv := range h.argvs {
		if cmd_test.TmuxSubcommand(argv) == "capture-pane" {
			out = append(out, argv)
		}
	}
	return out
}

func TestShellHistory_EndsEachRowWithCRLFAndKeepsEscapes(t *testing.T) {
	h := &historyTmux{size: "4\n", rows: "\x1b[31mone\x1b[0m\n\x1b[1mtwo\n"}

	got, err := ShellHistory("loom_term_demo", h.runner())

	require.NoError(t, err)
	assert.Equal(t, "\x1b[31mone\x1b[0m\r\n\x1b[1mtwo\r\n\x1b[0m", string(got),
		"each row ends in CRLF, and an SGR reset closes the history: capture-pane -e leaves the last row's colours on")
	require.Len(t, h.captures(), 1)
	assert.Equal(t, []string{"tmux", "-u", "capture-pane", "-p", "-e", "-S", "-4", "-E", "-1", "-t", "=loom_term_demo:"}, h.captures()[0],
		"the history rows only, with escapes, on the exact pane target")
}

func TestShellHistory_IsBoundedByTheScrollbackCap(t *testing.T) {
	for _, tc := range []struct{ size, from string }{
		{"2999\n", "-2999"},
		{"3000\n", "-3000"},
		{"5000\n", "-3000"},
	} {
		h := &historyTmux{size: tc.size, rows: "row\n"}

		_, err := ShellHistory("loom_term_demo", h.runner())

		require.NoError(t, err, tc.size)
		require.Len(t, h.captures(), 1, tc.size)
		argv := h.captures()[0]
		assert.Equal(t, tc.from, argv[slices.Index(argv, "-S")+1], "history_size %q", tc.size)
	}
}

// With no history `capture-pane -S -N -E -1` prints the first visible row, so
// the empty case must not ask tmux to capture at all.
func TestShellHistory_NoHistoryIsEmptyAndCapturesNothing(t *testing.T) {
	h := &historyTmux{size: "0\n", rows: "the visible row\n"}

	got, err := ShellHistory("loom_term_demo", h.runner())

	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, h.captures(), "no capture-pane ran")
}

func TestShellHistory_RefusesWhatItCannotRead(t *testing.T) {
	boom := errors.New("tmux fell over")
	for _, tc := range []struct {
		name string
		h    *historyTmux
	}{
		{"a gone session, which prints an empty size", &historyTmux{size: "\n", rows: "x\n"}},
		{"the size command failing", &historyTmux{sizeErr: boom}},
		{"a size that is no number", &historyTmux{size: "many\n"}},
		{"a negative size", &historyTmux{size: "-1\n"}},
		{"the capture failing", &historyTmux{size: "3\n", captureErr: boom}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShellHistory("loom_term_demo", tc.h.runner())

			require.Error(t, err)
			assert.Empty(t, got)
		})
	}
}

func TestShellHistory_RefusesAnUntargetableNameWithoutRunningTmux(t *testing.T) {
	h := &historyTmux{size: "3\n", rows: "x\n"}

	_, err := ShellHistory("loom_term_a:b", h.runner())

	require.Error(t, err)
	assert.Empty(t, h.argvs, "no tmux command ran")
}
```

Fails without: `TestShellHistory_EndsEachRowWithCRLFAndKeepsEscapes`: the `\n` to `\r\n` replacement removed, the trailing `\x1b[0m` left off (the history ends in the bold row's `\x1b[1m` state), `-e` dropped from the capture, or the pane target replaced by the bare name; `TestShellHistory_IsBoundedByTheScrollbackCap`: the `min(n, paneScrollbackLines)` removed (the last case asks `-S -5000`); `TestShellHistory_NoHistoryIsEmptyAndCapturesNothing`: the `n == 0` early return removed (the capture runs and the result is `the visible row\r\n`); `TestShellHistory_RefusesWhatItCannotRead`: the blank-size check removed (a gone session reads as no history, nil error), the `n < 0` check removed (`-1` asks `-S --1`), or an error from either command swallowed; `TestShellHistory_RefusesAnUntargetableNameWithoutRunningTmux`: the `ExactlyTargetable` check removed.

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestShellHistory_'`

Expected: the package does not build: `session/tmux/shell_attach_test.go: undefined: ShellHistory`.

- [ ] **Step 3: Implement**

In `session/tmux/shell_attach.go`, replace the import block

```go
import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)
```

with

```go
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)
```

and append to the end of the file:

```go
// ShellHistory returns the rows of the session called name above its visible
// screen, for the terminal's scrollback: at most paneScrollbackLines of them,
// with escapes (capture-pane -e), each row ended by "\r\n" so a terminal
// starts the next one in column 0, and the whole ended by an SGR reset
// (capture-pane -e leaves the last row's colours on). It is empty when the
// pane has no history.
// A gone session, a tmux that cannot answer and a size tmux does not report
// as a number are errors, never empty.
//
// It reads #{history_size} first rather than capturing `-S -<cap> -E -1`
// outright: on a pane with no history that range prints the first visible
// row instead of nothing, which the terminal would draw twice. A line that
// scrolls in between the two commands only moves the window by a row.
func ShellHistory(name string, cmdExec internalexec.Executor) ([]byte, error) {
	if !ExactlyTargetable(name) {
		return nil, fmt.Errorf("shell history: %q cannot be targeted exactly", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	sizeOut, err := cmdExec.Output(Command(ctx, "display-message", "-p", "-t", PaneTarget(name), "#{history_size}"))
	if err != nil {
		return nil, fmt.Errorf("shell history: read the history size: %w", err)
	}
	text := strings.TrimSpace(string(sizeOut))
	if text == "" {
		// A missing session prints empty formats and exits 0.
		return nil, fmt.Errorf("shell history: tmux reported no history size for %s: the session is gone", name)
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("shell history: tmux reported the history size %q", text)
	}
	if n == 0 {
		return nil, nil
	}
	n = min(n, paneScrollbackLines)
	out, err := cmdExec.Output(Command(ctx, "capture-pane", "-p", "-e", "-S", "-"+strconv.Itoa(n), "-E", "-1", "-t", PaneTarget(name)))
	if err != nil {
		return nil, fmt.Errorf("shell history: capture the history: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	// capture-pane -e ends each row with the escapes it needs but leaves the
	// last row's SGR state on; a reset keeps the live screen that follows the
	// history from inheriting it.
	return append(bytes.ReplaceAll(out, []byte("\n"), []byte("\r\n")), "\x1b[0m"...), nil
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestShellHistory_|TestStartShellAttach_|TestShellAttach_|TestTmuxTargetsAreExact|TestNoRawTmuxExec'`

Expected: `ok  	github.com/aidan-bailey/loom/session/tmux`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(tmux): ShellHistory reads the rows above a shell's visible screen" -- session/tmux/shell_attach.go session/tmux/shell_attach_test.go
```

### Task 3.3: Real-tmux tests

**Files:**
- Create: `session/tmux/shell_attach_realtmux_test.go`

- [ ] **Step 1: Write the tests**

Create `session/tmux/shell_attach_realtmux_test.go`. It uses `privateTmux` and `newRawSession` (`realtmux_helpers_test.go`), so it runs on a private server with a short socket path, and skips where tmux is not installed:

```go
//go:build !windows

package tmux

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const realShellName = "loom_term_sa"

// startRealShell starts a detached `sh` session on a private server and
// attaches a ShellAttach to it at cols x rows.
func startRealShell(t *testing.T, tag string, cols, rows int) *ShellAttach {
	t.Helper()
	privateTmux(t, tag)
	newRawSession(t, realShellName, "sh")
	a, err := StartShellAttach(realShellName, cols, rows, MakePtyFactory())
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// clientSize is the size of the session's attached client as tmux reports
// it, "" when none is attached or tmux cannot answer.
func clientSize(name string) string {
	out, err := Command(context.Background(), "list-clients", "-t", SessionTarget(name), "-F", "#{client_width}x#{client_height}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// collected gathers what a ShellAttach reads, in the background, until its
// Read fails.
type collected struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func collect(a *ShellAttach) *collected {
	c := &collected{}
	go func() {
		p := make([]byte, 4096)
		for {
			n, err := a.Read(p)
			if n > 0 {
				c.mu.Lock()
				c.buf.Write(p[:n])
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *collected) contains(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Contains(c.buf.Bytes(), []byte(s))
}

// A shell attach follows a real session: it starts at its size, what is typed
// reaches the shell and its output comes back, and a resize reaches tmux.
func TestShellAttach_AttachesTypesReadsAndResizes_RealTmux(t *testing.T) {
	a := startRealShell(t, "sa1", 90, 20)
	out := collect(a)

	require.Eventually(t, func() bool { return clientSize(realShellName) == "90x20" }, 10*time.Second, 50*time.Millisecond,
		"tmux sees the client at the size it started with")

	_, err := a.Write([]byte("echo shellattach$((40+2))\r"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return out.contains("shellattach42") }, 10*time.Second, 50*time.Millisecond,
		"the shell ran the command (the typed line holds `$((`, so only the result reads 42)")

	require.NoError(t, a.Resize(100, 30))
	require.Eventually(t, func() bool { return clientSize(realShellName) == "100x30" }, 10*time.Second, 50*time.Millisecond,
		"tmux sees the resize")
}

// Close ends the attach client, and the shell, which is the TUI terminal
// pane's too, keeps running.
func TestShellAttach_CloseEndsTheClientNotTheSession_RealTmux(t *testing.T) {
	a := startRealShell(t, "sa2", 80, 24)
	require.Eventually(t, func() bool { return clientSize(realShellName) != "" }, 10*time.Second, 50*time.Millisecond,
		"fixture: the client attached")

	require.NoError(t, a.Close())

	require.Eventually(t, func() bool { return clientSize(realShellName) == "" }, 10*time.Second, 50*time.Millisecond,
		"the client is gone")
	assert.True(t, slices.Contains(serverSessions(t), realShellName), "and the session is still there")
}

// A pane scrolled past its screen gives its scrolled-off rows, in order, and
// not the visible ones.
func TestShellHistory_RowsAboveTheVisibleScreen_RealTmux(t *testing.T) {
	privateTmux(t, "sa3")
	newRawSession(t, "loom_term_sh", `sh -c 'i=1; while [ $i -le 60 ]; do echo row$i; i=$((i+1)); done; sleep 60'`)

	var got []byte
	require.Eventually(t, func() bool {
		var err error
		got, err = ShellHistory("loom_term_sh", internalexec.Default{})
		return err == nil && bytes.Contains(got, []byte("row30\r\n"))
	}, 10*time.Second, 50*time.Millisecond, "the rows scrolled off")

	assert.True(t, bytes.HasPrefix(got, []byte("row1\r\n")), "from the first row on")
	assert.True(t, bytes.HasSuffix(got, []byte("\r\n\x1b[0m")), "each row ended by CRLF, and an SGR reset closes the history")
	assert.NotContains(t, string(got), "row60", "the visible screen is not history")
}

// A pane that never scrolled has no history: tmux's own capture of that range
// would hand back the first visible row.
func TestShellHistory_NoHistoryIsEmpty_RealTmux(t *testing.T) {
	privateTmux(t, "sa4")
	newRawSession(t, "loom_term_sh", `sh -c 'echo only; sleep 60'`)
	require.Eventually(t, func() bool {
		out, err := Command(context.Background(), "capture-pane", "-p", "-t", PaneTarget("loom_term_sh")).Output()
		return err == nil && bytes.Contains(out, []byte("only"))
	}, 10*time.Second, 50*time.Millisecond, "fixture: the pane shows its one row")

	got, err := ShellHistory("loom_term_sh", internalexec.Default{})

	require.NoError(t, err)
	assert.Empty(t, got)
}
```

Fails without: `TestShellAttach_AttachesTypesReadsAndResizes_RealTmux`: the `setPtySize` call removed from `StartShellAttach` (tmux reports the client at its default size, not `90x20`), `Write` or `Read` not wired to the PTY (no `shellattach42`), or `Resize` a no-op (never `100x30`); `TestShellAttach_CloseEndsTheClientNotTheSession_RealTmux`: `Close` leaving the PTY open (the client never goes) or killing the session (`serverSessions` no longer lists it); `TestShellHistory_RowsAboveTheVisibleScreen_RealTmux`: the `-E -1` bound dropped (the visible rows, `row60`, are included) the `\r\n` replacement removed, or the trailing reset left off; `TestShellHistory_NoHistoryIsEmpty_RealTmux`: the `n == 0` early return removed (the result is `only\r\n`).

- [ ] **Step 2: Run it to see it pass**

These tests exercise code written in tasks 3.1 and 3.2, so they pass on the first run; to see one red, make a mutation above (for instance comment out the `setPtySize` call in `StartShellAttach`, watch the first test time out waiting for `90x20`, then restore it).

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestShellAttach_|TestShellHistory_'`

Expected: `ok  	github.com/aidan-bailey/loom/session/tmux` (the real-tmux tests skip with `tmux not found in PATH` where tmux is not installed).

- [ ] **Step 3: Commit**

```bash
git add -N session/tmux/shell_attach_realtmux_test.go
git commit -m "test(tmux): ShellAttach and ShellHistory against a real tmux on a private server" -- session/tmux/shell_attach_realtmux_test.go
```

### Task 3.4: The package's rule and README

**Files:**
- Modify: `session/tmux/CLAUDE.md` (a rule added under "Session state")
- Modify: `session/tmux/README.md` (a row in the Files table, a paragraph)

- [ ] **Step 1: Add the rule**

In `session/tmux/CLAUDE.md`, immediately before the line that begins

```
- **Give each pump its own exit flag, and treat a client as usable only while `Attached` holds, never by `PtmxAlive`.**
```

add

```
- **`ShellAttach.Close` ends the attach client and never the session, and a `ShellAttach` holds no emulator, monitor, pump or status state.** The web terminal's shell is the TUI terminal pane's shell too (both name it `TerminalSessionName(title)`), so a browser tab closing or a phone locking must leave it running; a `Close` that killed the session ends the user's shell and its history, and a scan or an emulator here is a second reader of a pane the TUI's client owns. **Enforced** by `TestShellAttach_CloseEndsTheClientNotTheSession_RealTmux`; the missing state is **Convention** — a `ShellAttach` that grew a status scan would be two answerers of one prompt.
```

- [ ] **Step 2: Add the README's lines**

In `session/tmux/README.md`, in the Files table, after the row

```
| `attach.go` | `NewAttachClient`, `NewAttachClientWithDeps`, `DetectStatus` |
```

add

```
| `shell_attach.go` | `ShellAttach`, `StartShellAttach`, `ShellHistory`: the web terminal's pty attach client and its scrollback |
```

and immediately before the paragraph that begins ``A started program's environment is the session's own (`-e`)``, add

```
`ShellAttach` (`shell_attach.go`) is the web layer's client of a session's shell, and is not a `TmuxSession`: `StartShellAttach(name, cols, rows, factory)` runs `tmux attach-session` on `SessionTarget(name)` through the `PtyFactory`, sets the pty's size at once (`setPtySize`, as the TUI's attach does; tmux redraws to it) and returns a value with `Read` (the client's raw output, unfiltered), `Write`, `Resize` and `Close`. It holds no emulator, monitor, pump or status state. `Close` closes the pty and kills the client process (a blocking pty, where `Pty.Start` falls back to one, ends no pending `Read` by closing, and leaves the client attached), which detaches the client, and never the session (`TestShellAttach_CloseEndsTheClientProcessWhereTheMasterIsBlocking` pins the first half): the shell is the TUI terminal pane's too (`TerminalSessionName`), and a browser leaving must not end it (`TestShellAttach_CloseEndsTheClientNotTheSession_RealTmux`). It starts the client under `TERM=xterm-256color`, since a daemon started by a service has no `TERM` and tmux refuses a client without one. `ShellHistory(name, executor)` gives xterm.js its scrollback: the rows above the visible screen, at most `paneScrollbackLines`, with escapes, each row ended by `\r\n` and an SGR reset at the end. It asks `#{history_size}` first, because `capture-pane -S -N -E -1` on a pane with no history prints the first visible row instead of nothing (`TestShellHistory_NoHistoryIsEmpty_RealTmux`).
```

- [ ] **Step 3: Run the checks**

Run: `CGO_ENABLED=0 go test ./session/tmux/ ./tools/claudemd/` and `go run ./tools/claudemd -v`, then `GOOS=windows CGO_ENABLED=0 go build ./...`.

Expected: both tests `ok`; the lint reports no error for `session/tmux/CLAUDE.md` (the test names it cites exist) and `go build` prints nothing.

- [ ] **Step 4: Commit**

```bash
git commit -m "docs(tmux): ShellAttach.Close never closes the session; the README lists the attach and its history" -- session/tmux/CLAUDE.md session/tmux/README.md
```

## Story 4: The host's switches: `loom web terminal` and the settings row

- **Developer:** intermediate-dev
- **Files:** `internal/cli/web.go`, `internal/cli/web_test.go`, `ui/overlay/settingsOverlay.go`, `ui/overlay/settingsOverlay_test.go`, `app/state_settings.go`, `app/state_settings_test.go`, `app/intents.go`, `app/core_glue.go`, `internal/cli/README.md`, `internal/cli/CLAUDE.md`, `USAGE.md`
- **Interfaces:** uses Story 1's `Core.SetWebTerminal` and `WebView.Terminal`; exposes `loom web terminal on` and its texts to Story 9
- **Verify:** `CGO_ENABLED=0 go test ./internal/cli/ ./ui/overlay/ ./app/`
- **Review:** light (it also shifts the Up-press counts of two existing test helpers, `editBuildSweep` and `toggleBorrow`, by the new row)
- **Form:** brief (plumbing a flag through a command and a row that copy existing ones)

The web terminal's switch is set only on the host, never from the app. `loom web terminal on|off` reads `Core.Web()`, says so when the switch already has the value asked for and sends nothing, and otherwise sends Story 1's `Core.SetWebTerminal` and prints the model's refusal as it stands (the validation stays in the model, as `internal/cli/CLAUDE.md` requires of `loom web`); `loom web status` gains a `Terminal:` row. The TUI's settings gain a `Web Terminal` row after Job Borrowing, a global setting like it: Enter raises a request the overlay hands the app, the app answers it with `SetWebTerminal`, the row is seeded when the overlay opens and refreshed on `ModelChanged`. `USAGE.md` and `internal/cli/README.md` document both, and the `loom web` rule in `internal/cli/CLAUDE.md` names the fourth request the command sends. The story is brief: the plan holds no implementation code, each task names its tests and the behaviour, and the developer copies the shape of the commands and rows it names (`web disable`, the Job Borrowing row). Run every build and test command below through `loom job run -d "<what>" --` (wrap an env-prefixed command as `sh -c '…'`), as the session's rules say. No task touches a landmine marker.

### Task 4.1: `loom web terminal on|off` and the `Terminal:` status row

**Files:**
- Modify: `internal/cli/web.go` (`newWebCmd` at line 30, `writeWebStatus` at line 195; a new `newWebTerminalCmd` after `newWebDisableCmd`)
- Modify: `internal/cli/README.md` (the `web disable` and `web status` rows of the command table, lines 49 and 54)
- Test: `internal/cli/web_test.go`

**Uses:** Story 1's `Core.SetWebTerminal(on bool) error` and `WebView.Terminal bool`, through the `client` that embeds `core.Core` (`d.SetWebTerminal`, `d.Web()`). **Defines:** `loom web terminal on` and `loom web terminal off`, printing `The web terminal is on.`, `The web terminal is off.`, `The web terminal is already on.` or `The web terminal is already off.`, each followed by a newline and nothing else on stdout; and the `Terminal:` row of `loom web status`, `on` or `off`.

Behaviour:
- `newWebTerminalCmd` is the command `terminal` with the two subcommands `on` and `off` (each `cobra.NoArgs`, each calling `loomErrors(c)` first, as `disable` does). `newWebCmd` adds it after `newWebDisableCmd()`, and its `Short` text names `terminal` among the subcommands.
- Each subcommand runs `withDaemon`, reads `d.Web()`; when `w.Terminal` already equals the value asked for it prints the `already` line and sends nothing; otherwise it calls `d.SetWebTerminal(on)`, returns its error unchanged (never wrapped, never a check of its own: the model's `web.json is left alone: …` reaches the user word for word), and prints the `is on.` or `is off.` line.
- It works while the web is off (`w.Listen == ""`): the model allows it, so the command adds no refusal and no extra line.
- `writeWebStatus` prints one more row, `Terminal:` with `on` or `off` from `w.Terminal`, after the `Devices:` row, through the same `row` closure (the label `Terminal:` is 9 characters, which the closure's `%-9s` holds).
- `internal/cli/README.md`: the `web disable` row's neighbour gains a `web terminal` row (`Web()` read first, nothing sent when the value is in force, else `SetWebTerminal`), and the `web status` row names the `Terminal:` row (from `WebView.Terminal`, shown always).

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/web_test.go`, after `TestWebDisable_WhenOffChangesNothing` for the first six, and the last after `TestWebStatus_OffWithAnErrorWhenWebJSONIsLeftAlone`. They use only the helpers the file has (`runWeb`, `webDaemon`, `webView`, `isolateWorkspaces`, `serveDaemon`, `core.WebFile`):

- `TestWebTerminal_OnTurnsItOnAndSaysSo`: `webDaemon(t)`; `runWeb(t, "terminal", "on")` returns no error and stdout is exactly `"The web terminal is on.\n"`; `webView(t, d).Terminal` is true.
- `TestWebTerminal_OffTurnsItOffAndSaysSo`: `webDaemon(t)`; `require.NoError(t, d.loop.SetWebTerminal(true))`; `runWeb(t, "terminal", "off")` returns no error and stdout is exactly `"The web terminal is off.\n"`; `webView(t, d).Terminal` is false.
- `TestWebTerminal_OnWhenOnChangesNothing`: `webDaemon(t)`; run `terminal on` twice; the first prints `The web terminal is on.\n`, the second exactly `"The web terminal is already on.\n"`; the view stays on.
- `TestWebTerminal_OffWhenOffSendsNothing`: a daemon over an unreadable `web.json` (as `TestWebStatus_OffWithAnErrorWhenWebJSONIsLeftAlone` builds one: `isolateWorkspaces(t)`, write `not json` to `filepath.Join(dir, core.WebFile)` under `config.GetGlobalConfigDir()`, `serveDaemon(t, "true")`); `runWeb(t, "terminal", "off")` returns no error and prints exactly `"The web terminal is already off.\n"`. The model refuses every `SetWebTerminal` on such a file, even for the value in force, so a request sent would be an error: the assertion shows none was.
- `TestWebTerminal_PassesTheDaemonsRefusalOn`: the same daemon over an unreadable `web.json`; `runWeb(t, "terminal", "on")` returns an error whose text contains `web.json is left alone`, stdout is empty, and `webView(t, d).Terminal` is false (take the daemon from `serveDaemon`).
- `TestWebTerminal_WorksWhileTheWebIsOff`: `webDaemon(t)` with no `ConfigureWeb`; `terminal on` returns no error; the view has `Terminal` true and `Listen` empty (the web is still off).
- `TestWebStatus_ShowsTheTerminalSwitch`: `webDaemon(t)`; `status` stdout matches `Terminal:\s+off`; after `require.NoError(t, d.loop.SetWebTerminal(true))` it matches `Terminal:\s+on`; and with the web enabled (`d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com")`) the row is still there.

Fails without: `TestWebTerminal_OnTurnsItOnAndSaysSo`: the `d.SetWebTerminal(on)` call removed from the subcommand (the view stays off), or the printed text changed; `TestWebTerminal_OffTurnsItOffAndSaysSo`: the `off` subcommand sending `SetWebTerminal(true)` (the view stays on); `TestWebTerminal_OnWhenOnChangesNothing`: the `w.Terminal == on` check removed (the second run prints `The web terminal is on.`); `TestWebTerminal_OffWhenOffSendsNothing`: the same check removed (the request reaches the model, which refuses the file left alone, so the command errors); `TestWebTerminal_PassesTheDaemonsRefusalOn`: the error dropped, replaced by a message of the command's own, or a success line printed before the call (stdout not empty); `TestWebTerminal_WorksWhileTheWebIsOff`: a refusal added for `w.Listen == ""`; `TestWebStatus_ShowsTheTerminalSwitch`: the row removed, or written from a constant instead of `w.Terminal`.

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestWebTerminal_|TestWebStatus_ShowsTheTerminalSwitch'`

Expected: FAIL. The `TestWebTerminal_` tests fail with `unknown command "terminal" for "web"` and the status test fails on its `Terminal:\s+off` match.

- [ ] **Step 3: Implement**

Write `newWebTerminalCmd`, add it to `newWebCmd`, extend `writeWebStatus` and update `internal/cli/README.md`, as the behaviour above says. Do not touch the `web enable` or `disable` code, and do not make `terminal` or either subcommand bump `Rev`.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestWeb'`

Expected: `ok  	github.com/aidan-bailey/loom/internal/cli`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(cli): loom web terminal on|off and the Terminal row of web status" -- internal/cli/web.go internal/cli/web_test.go internal/cli/README.md
```

### Task 4.2: The `Web Terminal` settings row and its app wiring

**Files:**
- Modify: `ui/overlay/settingsOverlay.go` (the `settingsField` constants at line 17, `label` at line 33, the `SettingsOverlay` struct's `borrow` fields at line 106, the Enter switch at line 213, `SetBorrow` and `TakeBorrowRequest` at line 328, the value switch at line 462)
- Modify: `app/state_settings.go` (the `TakeBorrowRequest` branch at line 40)
- Modify: `app/intents.go` (`runOpenSettings`, the `so.SetBorrow(...)` line at 473)
- Modify: `app/core_glue.go` (the `core.ModelChanged` case at line 163)
- Test: `ui/overlay/settingsOverlay_test.go`, `app/state_settings_test.go`

**Uses:** Story 1's `Core.SetWebTerminal(on bool) error` and `WebView.Terminal bool` (`m.core.Web().Terminal`); the model publishes the switch in `core.ModelChanged`'s view, with `WebView.Rev` untouched, so the TUI's replica holds a change when the request returns and another client's change arrives as a `ModelChanged`. **Defines**, private to this story: the constant `settingsFieldWebTerminal` (between `settingsFieldBorrow` and `settingsFieldAccounts`, so the Accounts row is now last), `func (s *SettingsOverlay) SetWebTerminal(on bool)` and `func (s *SettingsOverlay) TakeWebTerminalRequest() (bool, bool)`; and the row, labelled `Web Terminal`, whose values are `on (all workspaces)` and `off (all workspaces)`.

Behaviour:
- The overlay: the row's label is `Web Terminal`; its value is `on (all workspaces)` while the overlay's `webTerminal` field is true and `off (all workspaces)` otherwise; Enter on it sets a pending request to the opposite of that field (as the Job Borrowing row's Enter does with `borrowReq`), returns `closed` false and `changed` false (the workspace's `config.json` is never saved for it, and `cfg` is never touched), and does not touch the borrow request. `SetWebTerminal` supplies the field; `TakeWebTerminalRequest` returns and clears the pending value, as `TakeBorrowRequest` does.
- `handleStateSettingsKey` (`app/state_settings.go`): after the Job Borrowing branch, `if on, ok := so.TakeWebTerminalRequest(); ok` calls `m.core.SetWebTerminal(on)`, appends `m.handleError(err)` to `cmds` on an error, and then reseeds the row from the model with `so.SetWebTerminal(m.core.Web().Terminal)` (the row shows what the model holds, never what was asked). The switch is global, so no workspace id is passed.
- `runOpenSettings` seeds the row: `so.SetWebTerminal(m.core.Web().Terminal)` next to the `SetBorrow` line.
- `applyCoreEvent`'s `core.ModelChanged` case refreshes an open overlay's row: inside its existing `if so := m.settingsOverlay(); so != nil` block, `so.SetWebTerminal(m.core.Web().Terminal)` next to the Build Sweep refresh.
- The row sits after Job Borrowing and before Accounts. The cursor tests' counted key presses change with it: `editBuildSweep` presses Down 20 times to clamp at the last row and then Up to reach Build Sweep, so it needs one more Up (three: Web Terminal, Job Borrowing, Build Sweep); `toggleBorrow` needs one more Up (two: Web Terminal, Job Borrowing). Their comments name the rows. Nothing else in `app/` counts rows from the end (`app/accounts_overlay_test.go` and `app/accounts_warnings_test.go` clamp at the last row, Accounts, which stays last).

- [ ] **Step 1: Write the failing tests**

In `ui/overlay/settingsOverlay_test.go`, after `TestSettingsOverlay_BorrowToggleIsARequestNotAWorkspaceSetting`, add a helper `rowLine(t, so, label)` beside `borrowLine` (the line of `so.Render()` that contains `label`, `t.Fatal` when none; `borrowLine` may call it) and:

- `TestSettingsOverlay_WebTerminalRowShowsTheGlobalSetting`: a fresh `NewSettingsOverlay(newTestSettingsCfg(), false, "")`; the `Web Terminal` line contains `off (all workspaces)`; after `so.SetWebTerminal(true)` it contains `on (all workspaces)`; and the Job Borrowing line is unchanged (still `off (all workspaces)`), so the two settings are separate.
- `TestSettingsOverlay_WebTerminalToggleIsARequestNotAWorkspaceSetting`: for each `from` in `false, true`: `cfg := newTestSettingsCfg()`, `so.SetWebTerminal(from)`, `so.cursor = int(settingsFieldWebTerminal)`, a `tea.KeyEnter` press returns `closed` false and `changed` false; `cfg` still equals `newTestSettingsCfg()` (`assert.Equal`); `TakeWebTerminalRequest` returns `(!from, true)` and then `(false, false)`; and `TakeBorrowRequest` returns `(false, false)`.
- `TestSettingsOverlay_WebTerminalRowSitsBetweenBorrowingAndAccounts`: `settingsFieldWebTerminal` equals `settingsFieldBorrow+1` and `settingsFieldAccounts` equals `settingsFieldWebTerminal+1`, and in `so.Render()` the lines holding `Job Borrowing`, `Web Terminal` and `Accounts` appear in that order (compare each line's index among the render's lines).

In `app/state_settings_test.go`, change `editBuildSweep` and `toggleBorrow` as the behaviour says, and after `TestSettingsBorrow_AQueueChangeRefreshesTheRow` add a helper `toggleWebTerminal(m *home)` (20 Down presses, one Up for Web Terminal, Enter, through `handleStateSettingsKey`, as `toggleBorrow` does) and `webTerminalLine(so)` (the line of `so.Render()` containing `Web Terminal`), and:

- `TestSettingsWebTerminal_ReachesTheModel`: `m := newTestHomeWithWsCtx(t)`; `so := openSettingsForTest(t, m)`; `toggleWebTerminal(m)`; `m.core.Web().Terminal` is true, `m.errBox.String()` is empty and the row's line contains `on (all workspaces)`; a second `toggleWebTerminal(m)` turns it off: `Terminal` false and the line contains `off (all workspaces)`. Add a `t.Cleanup` that sets the switch back off through `loopOf(m).SetWebTerminal(false)`.
- `TestSettingsWebTerminal_ShowsARefusalAndKeepsTheRow`: after `newTestHomeWithWsCtx` and `openSettingsForTest`, make the model's write fail: in `config.GetGlobalConfigDir()` remove `web.json` if there is one (`os.Remove`, ignoring a not-exist error) and create a directory of that name (`os.Mkdir`, so the atomic write cannot rename a file over it); `toggleWebTerminal(m)` then leaves `m.core.Web().Terminal` false, `m.errBox.String()` containing `save web terminal` (the model's own error text, Story 1's `SetWebTerminal`), and the row's line containing `off (all workspaces)`.
- `TestSettingsWebTerminal_OpeningTheOverlaySeedsTheRow`: for each of off and on, in a fresh home (`newTestHomeWithWsCtx(t)`): when on, `require.NoError(t, loopOf(m).SetWebTerminal(true))`, `_ = m.drainCore()` and a cleanup that sets it off again; then `runOpenSettings(m)` (the production path the key takes) and the `Web Terminal` line of `m.settingsOverlay().Render()` contains `off (all workspaces)` or `on (all workspaces)` as the model holds it.
- `TestSettingsWebTerminal_AChangeElsewhereRefreshesTheRow`: `so := openSettingsForTest(t, m)`; `require.NoError(t, loopOf(m).SetWebTerminal(true))`; `_ = m.drainCore()`; the row's line contains `on (all workspaces)`; then `SetWebTerminal(false)`, drain: it contains `off (all workspaces)`.

Fails without: `TestSettingsOverlay_WebTerminalRowShowsTheGlobalSetting`: the value switch's case missing or reading `s.borrow` (the Job Borrowing assertion or the `on` assertion fails); `TestSettingsOverlay_WebTerminalToggleIsARequestNotAWorkspaceSetting`: Enter setting `borrowReq` instead, or returning `changed` true, or writing `cfg`; `TestSettingsOverlay_WebTerminalRowSitsBetweenBorrowingAndAccounts`: the constant placed after `settingsFieldAccounts` or before `settingsFieldBorrow`; `TestSettingsWebTerminal_ReachesTheModel`: the `TakeWebTerminalRequest` branch removed from `handleStateSettingsKey` (the switch stays off), or the reseed removed (the row's line keeps `off`), or the request sending `SetWebTerminal(!on)`; `TestSettingsWebTerminal_ShowsARefusalAndKeepsTheRow`: the error branch's `handleError` removed (the errBox is empty), or the reseed written as `so.SetWebTerminal(on)` (the row would show `on` although the model refused); `TestSettingsWebTerminal_OpeningTheOverlaySeedsTheRow`: the seed removed from `runOpenSettings` (the on case shows `off`), or the seed written as a constant true (the off case shows `on`); `TestSettingsWebTerminal_AChangeElsewhereRefreshesTheRow`: the refresh removed from the `core.ModelChanged` case.

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./ui/overlay/ ./app/ -run 'WebTerminal|TestSettingsBorrow|TestSettingsBuildSweep'`

Expected: FAIL. `./ui/overlay/` does not build (`undefined: settingsFieldWebTerminal`, `so.SetWebTerminal undefined`, `so.TakeWebTerminalRequest undefined`); the `./app/` `TestSettingsWebTerminal_` tests fail because Enter on the row one above the last still toggles Job Borrowing.

- [ ] **Step 3: Implement**

Add the row to the overlay and the four app seams as the behaviour above says, copying the Job Borrowing row's shape (field, request pointer, setter, taker, label, Enter case, value case) and keeping the `ConfigureBorrow` branch exactly as it is. Update the two test helpers' counts and comments.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./ui/overlay/ ./app/`

Expected: `ok  	github.com/aidan-bailey/loom/ui/overlay` and `ok  	github.com/aidan-bailey/loom/app`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(app,overlay): a Web Terminal row in the settings" -- ui/overlay/settingsOverlay.go ui/overlay/settingsOverlay_test.go app/state_settings.go app/state_settings_test.go app/intents.go app/core_glue.go
```

### Task 4.3: `USAGE.md`

**Files:**
- Modify: `USAGE.md` (line 339 the `S` row of the keyboard table, the web section's paragraph that ends at line 942, and the Web Subcommands table at lines 1412-1418)

Behaviour (docs only, no test):
- The `S` row's list gains the new row: `Build Sweep, Job Borrowing and Web Terminal, which apply to all workspaces`.
- In the web section, after the paragraph that ends `…the settings, whether it serves and why not, its local origin and the device count.` (the one that begins `` `loom web devices` lists each device``), add a paragraph on the web terminal: `loom web terminal on` lets a paired device open a shell in a session's worktree (the tmux session `term_<title>` the TUI's terminal pane also uses), `loom web terminal off` closes every open terminal connection and refuses new ones; it is off by default, works while the web is off, and is set only on the host, with these commands or the `Web Terminal` row in Settings (`S`), never from the app, so a stolen device cookie cannot turn it on; it is stored as `terminal` in `web.json`, which a build older than this one drops when it rewrites the file, which turns the terminal off; `loom web status` shows it as `Terminal:`. Say that the app's terminal panel is not in this release if the panel is not yet documented elsewhere in the file (`grep -n 'terminal panel' USAGE.md`).
- The Web Subcommands table gains two rows after `web disable`: `` `web terminal on` `` (turn the web terminal on: a paired device may open a shell in a session's worktree; says so when it already is) and `` `web terminal off` `` (turn it off: open terminal connections close; says so when it already is), and the `web status` row ends `… the number of devices and the `Terminal:` switch`.

- [ ] **Step 1: Write the failing test**

There is no test for these lines beyond the repo's docs structure test. Run it first as the baseline.

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ -run TestRepo_ClaudeMDStructure`

Expected: `ok` (the docs are unchanged so far).

- [ ] **Step 2: Run it to see it fail**

Not applicable: a prose edit has no failing state. Check that the three places are still the old text: `grep -n 'Build Sweep and Job Borrowing\|^| `web disable`\|^| `web status`' USAGE.md` prints lines 339, 1413 and 1418.

- [ ] **Step 3: Implement**

Make the three edits above, in the file's own wording and with plain ASCII punctuation as the rest of the section.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ -run TestRepo_ClaudeMDStructure` and then `grep -n 'web terminal' USAGE.md`.

Expected: `ok`, and the grep lists the new paragraph and the two table rows.

- [ ] **Step 5: Commit**

```bash
git commit -m "docs(cli): USAGE.md documents loom web terminal and the Web Terminal row" -- USAGE.md
```

### Task 4.4: The `loom web` rule names `SetWebTerminal`

**Files:**
- Modify: `internal/cli/CLAUDE.md:27` (the rule that begins `Leave a web setting's and a device name's validation to the model`)

Behaviour (docs only, no test). `loom web` now sends a fourth request, so the rule that lists what it sends names it. Its meaning, its reason clause and its `Enforced` line stay as they are.
- In that one line replace the exact text ``sends `ConfigureWeb`, `PairDevice` and `RevokeDevice` and prints`` with ``sends `ConfigureWeb`, `PairDevice`, `RevokeDevice` and `SetWebTerminal` and prints``. Change nothing else in the file.
- The file is 19,978 of its 20,000 bytes today and the edit adds 18 (19,996 after), so no other word may be added to it: the budget lint fails past 20,000. `wc -c internal/cli/CLAUDE.md` before and after shows 19978 and 19996.

- [ ] **Step 1: Write the failing test**

There is no test for the wording. Run the docs lint as the baseline.

Run: `CGO_ENABLED=0 go test ./tools/claudemd/`

Expected: `ok  	github.com/aidan-bailey/loom/tools/claudemd`.

- [ ] **Step 2: Run it to see it fail**

Not applicable: a prose edit has no failing state. Check the old text is still there: `grep -c 'sends `ConfigureWeb`, `PairDevice` and `RevokeDevice` and prints' internal/cli/CLAUDE.md` prints `1`.

- [ ] **Step 3: Implement**

Make the one replacement above and nothing else.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/` and then `wc -c internal/cli/CLAUDE.md`.

Expected: `ok  	github.com/aidan-bailey/loom/tools/claudemd`, and `19996 internal/cli/CLAUDE.md` (at most 20000: if it prints more, an earlier story has grown the file, and the edit must then be shortened elsewhere in the same line rather than raising the budget).

- [ ] **Step 5: Commit**

```bash
git commit -m "docs(cli): the loom web rule names SetWebTerminal among the requests it sends" -- internal/cli/CLAUDE.md
```

## Story 5: The snapshot's field and the `terminal` event

- **Developer:** intermediate-dev
- **Files:** `internal/web/fleet.go`, `internal/web/stream.go`, `internal/web/stream_test.go`, `internal/web/fleet_test.go`, `internal/web/api.fields`, `docs/specs/web-api.md`, `internal/web/README.md`, `internal/web/ui/src/api/gen.ts`, `internal/web/ui/src/api/stream.ts`, `internal/web/ui/src/api/stream.test.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/store/startFleet.test.ts`, `tools/webshot/fixture/snapshot.json`, `internal/web/dist/**`
- **Interfaces:** exposes `snapshotJSON.Terminal`, `terminalStateJSON`, the `terminal` event and the store's flag to part 2; uses Story 1's `WebView.Terminal`
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/webshot/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
- **Review:** light
- **Form:** brief (a field and an event added by the web layer's documented procedure)

The stream's hub follows `WebView.Terminal`, which the model publishes in `core.ModelChanged`'s view: the snapshot carries `terminal`, and a change is sent to every stream as the `terminal` event, the way the quick replies are (a projection field, a published copy, a flush that sends what differs). Turning the switch never changes `WebView.Rev` (Story 1), so no stream ends. The contract is regenerated (`gen.ts`, `web-api.md`, `api.fields`), the app's stream knows the event and its store sets the snapshot's flag from it, webshot's fixture snapshot gains the field, and the bundle is rebuilt. Nothing in the app draws it yet. The story is brief: the plan holds no implementation code, each task names its tests and the behaviour, and the developer follows `docs/claude/web-layer.md`'s "Adding a stream event" and the quick replies' code it points at. Run every build, test, `go generate` and `pnpm` command below through `loom job run -d "<what>" --` (wrap an env-prefixed command as `sh -c '…'`), as the session's rules say. No task touches a landmine marker.

### Task 5.1: The projection, the snapshot's field, the event and the contract

**Files:**
- Modify: `internal/web/fleet.go` (`snapshotJSON` at line 178, the new `terminalStateJSON` after `quickRepliesJSON` at line 218, `projection` at line 442 and `setQuickReplies` at line 528)
- Modify: `internal/web/stream.go` (`hub` at line 109, `newHub`, `apply`'s `core.ModelChanged` case at line 190, `flush` at line 357, `snapshotLocked` at line 525, `streamEvents` at line 556)
- Modify: `internal/web/README.md` (the stream section, line 79)
- Regenerate, never edit: `internal/web/ui/src/api/gen.ts`, `docs/specs/web-api.md`, `internal/web/api.fields`
- Test: `internal/web/fleet_test.go`, `internal/web/stream_test.go`

**Uses:** Story 1's `WebView.Terminal bool`, which the hub reads as `ev.View.Web.Terminal` from `core.ModelChanged` (the same view it reads `ev.View.Web.QuickReplies` from), and `Loop.SetWebTerminal(on bool) error`, which the stream tests call as `e.loop.SetWebTerminal(...)` (the Loop forwards `Core` methods). **Defines**: `Terminal bool` tagged `json:"terminal"` on `snapshotJSON`; `type terminalStateJSON struct { Enabled bool }` in `fleet.go`, its field tagged `json:"enabled"` (no `omitempty`: an off switch is `{"enabled":false}`); the stream event `terminal` with that payload; and, private to the layer, the projection's `terminal bool` with `func (p *projection) setTerminal(on bool) bool`, and the hub's `pubTerminal bool`.

Behaviour:
- `projection` holds `terminal`, false at the start; `setTerminal(on)` stores it and reports whether it changed (false when `on` equals the held value), as `setQuickReplies` does.
- `hub.apply`'s `core.ModelChanged` case calls `h.fleet.setTerminal(ev.View.Web.Terminal)` beside its `setQuickReplies` call and sets `moved` when it reports a change, so the flusher wakes.
- `hub.flush` sends the `terminal` event, through `broadcastLocked("terminal", terminalStateJSON{Enabled: h.fleet.terminal})`, when `h.fleet.terminal` differs from `h.pubTerminal`, and records it, as it does for the quick replies. `newHub` starts `pubTerminal` false, which is the projection's start, so a layer whose first view says off sends nothing.
- `snapshotLocked` sets `Terminal: h.fleet.terminal`; a stream opened while the switch is on starts with `terminal` true, and may then be sent one redundant `terminal` event with the same value when the flush has not yet caught up, which the app handles by setting the same flag.
- `streamEvents()` lists `{"terminal", reflect.TypeFor[terminalStateJSON]()}` after `quick_replies`.
- `snapshotJSON`'s doc comment names the new field (the web terminal's switch). `terminalStateJSON` has a doc comment saying it is the `terminal` event's payload and that the snapshot has the same value.
- `internal/web/README.md`'s stream section: the snapshot's list gains `terminal` (whether the web terminal is on) and the events after it gain `terminal` (`{enabled}`, sent when the switch changes).
- Nothing else in the layer reads or writes the flag; `routes.go` and `web.go` are not touched (the terminals' part of Story 7 follows the switch itself).

- [ ] **Step 1: Write the failing tests**

In `internal/web/fleet_test.go`:
- Change `TestSnapshotJSON_HasTheContractsKeys`: its expected key list gains `"terminal"` (the `json.Marshal` of a zero `snapshotJSON` has the key because the tag has no `omitempty`).
- Add `TestTerminalStateJSON_NamesTheFlagEvenWhenOff` beside it: `json.Marshal(terminalStateJSON{Enabled: true})` is JSON-equal to `{"enabled":true}` and `terminalStateJSON{}` to `{"enabled":false}`.
- Add `TestProjection_SetTerminalReportsOnlyAChange` beside `TestResolvedQuickReplies`: `p := newProjection()`; `p.setTerminal(false)` reports false (nothing changed from the start); `p.setTerminal(true)` reports true and `p.terminal` is true; `p.setTerminal(true)` reports false; `p.setTerminal(false)` reports true and `p.terminal` is false.

In `internal/web/stream_test.go`:
- Add `TestStream_SendsTheTerminalSwitchWhenItChanges` after `TestStream_SendsTheQuickRepliesWhenTheyChange`, built the same way: `e := newStreamEnv(t, streamSession{title: "one", status: session.Running})`; `s := e.open(t, e.cookie)`; the snapshot has `Terminal` false (`unframe[snapshotJSON]`); `require.NoError(t, e.loop.SetWebTerminal(true))`; `unframe[terminalStateJSON](t, s.expect("terminal"))` is `{Enabled: true}`; a second stream opened now (`e.open(t, e.cookie)`) starts with a snapshot whose `Terminal` is true; `SetWebTerminal(true)` again sends nothing (`s.none(100 * time.Millisecond)`); `SetWebTerminal(false)` sends `terminal` with `Enabled` false, and its raw data is JSON-equal to `{"enabled": false}`; and the first stream is still open and still gets the next event, which shows the switch ended no stream (`require.NoError(t, e.loop.SetQuickReplies([]string{"ship it"}))` then `s.expect("quick_replies")`).
- Change `TestStreamEvents_NameThePayloadOfEveryEventTheStreamSends`: the expected names are `"snapshot", "sessions", "workspaces", "usage", "quick_replies", "terminal", "device", "notice", "ping", "bye"`, and it asserts `types["terminal"]` is `reflect.TypeFor[terminalStateJSON]()`.
- Change `TestStreamEvents_EveryFrameTheHubSendsIsInTheTableWithItsPayloadType`: after its `SetQuickReplies` call add `require.NoError(t, e.loop.SetWebTerminal(true))`, so the hub's `terminal` frame is among those the table is checked against.

Fails without: `TestSnapshotJSON_HasTheContractsKeys`: the `Terminal` field removed from `snapshotJSON`, or its tag changed; `TestTerminalStateJSON_NamesTheFlagEvenWhenOff`: `omitempty` added to the tag (the off case has no key), or the tag renamed; `TestProjection_SetTerminalReportsOnlyAChange`: `setTerminal` always reporting true (the repeated true and the first false report a change) or never storing the value; `TestStream_SendsTheTerminalSwitchWhenItChanges`: `setTerminal` missing from the `ModelChanged` case (no event), `snapshotLocked` not setting `Terminal` (the second stream's snapshot reads false), the flush comparing nothing so that a repeated value is sent again (the `s.none` assertion), or `pubTerminal` never recorded; `TestStreamEvents_NameThePayloadOfEveryEventTheStreamSends`: the `terminal` entry missing from `streamEvents()`, or placed elsewhere in the list; `TestStreamEvents_EveryFrameTheHubSendsIsInTheTableWithItsPayloadType`: the same entry missing (the hub sends `terminal`, which the table does not name).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestSnapshotJSON_|TestTerminalStateJSON_|TestProjection_SetTerminal|TestStream_SendsTheTerminal|TestStreamEvents_'`

Expected: the package does not build: `snapshotJSON` has no field `Terminal` (`unknown field Terminal`), `undefined: terminalStateJSON`, and `p.setTerminal undefined`.

- [ ] **Step 3: Implement**

Make the changes the behaviour lists in `fleet.go`, `stream.go` and `internal/web/README.md`. Then regenerate the contract: `loom job run -d "regenerate the web contract" -- sh -c 'CGO_ENABLED=0 go generate ./internal/web'`. It rewrites `internal/web/ui/src/api/gen.ts` (`Snapshot` gains `terminal: boolean`, a `TerminalState` interface `{ enabled: boolean }` appears and `StreamEvents` gains `terminal: TerminalState`), `docs/specs/web-api.md` (the stream section and the snapshot's type) and `internal/web/api.fields`, to which it appends the lines `Snapshot.terminal: boolean`, `TerminalState.enabled: boolean` and `event terminal data=TerminalState`; check the diff holds only those additions, and commit them with the source. The app's typecheck (`pnpm run typecheck`) fails between this task and Task 5.2, because `KNOWN` in `stream.ts`, the store's `applied` and webshot's fixture lack the new key; Task 5.2 fixes it, and no Go gate reads it.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./internal/web/... ./tools/webshot/`

Expected: `ok` for each package, `TestContract_GeneratedFilesAreFresh` and `TestContract_FieldsOnlyGrow` included.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the snapshot's terminal field and the terminal stream event" -- internal/web/fleet.go internal/web/fleet_test.go internal/web/stream.go internal/web/stream_test.go internal/web/README.md internal/web/api.fields docs/specs/web-api.md internal/web/ui/src/api/gen.ts
```

### Task 5.2: The app's stream, its store, the fixture and the bundle

**Files:**
- Modify: `internal/web/ui/src/api/stream.ts` (`KNOWN` at line 22)
- Modify: `internal/web/ui/src/store/fleet.ts` (`applied`, after the `quick_replies` case at line 85; `afterEvent`, its `case "quick_replies":` label at line 197)
- Modify: `tools/webshot/fixture/snapshot.json` (after `quick_replies`, line 545)
- Modify: `internal/web/dist/**` (rebuilt, never edited by hand)
- Test: `internal/web/ui/src/api/stream.test.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/store/startFleet.test.ts`

**Uses:** Task 5.1's generated `StreamEvents["terminal"]` (`TerminalState`, `{ enabled: boolean }`) and `Snapshot.terminal: boolean` in `gen.ts`. **Defines**: nothing the plan's other stories use beyond the contract: the store sets the snapshot's `terminal` from the `terminal` event.

Behaviour:
- `KNOWN` gains `terminal: true`, so the stream delivers the event to `onEvent` instead of ignoring it as an unknown name.
- The store's `applied` gains `case "terminal"`: through `change`, it returns the snapshot with `terminal` set to `event.data.enabled` (so, like the other cases, it does nothing before the first snapshot).
- The store's `afterEvent` (the second per-event switch, which `startFleet`'s `onEvent` runs after `apply`) gains `case "terminal":` among the labels that only `scheduleSave()` (`usage`, `quick_replies`, `device`), so a `terminal` event schedules the snapshot's save as a `quick_replies` event does, and a cold start draws the switch as last heard.
- `tools/webshot/fixture/snapshot.json` gains `"terminal": false` after `quick_replies` (the generated type makes the field required, and `internal/web/ui/src/fixtures.test.ts` holds the fixture to it). No screen draws it, so no screenshot changes.
- The bundle is rebuilt with `pnpm run build` and committed with the source; nothing else in `src/` changes beyond `stream.ts`, `fleet.ts`'s `applied` and `afterEvent`, and the tests.

- [ ] **Step 1: Write the failing tests**

In `internal/web/ui/src/api/stream.test.ts`, add a case after `reassembles an event split across chunks and skips comments, unknown events and bad JSON`, named `delivers the terminal event`: it opens a connection as that test does (`nextConnection`, `handlers`, `start`, `settle`), pushes `event: terminal\r\ndata: {"enabled":true}\r\n\r\n` and then `event: terminal\r\ndata: {"enabled":false}\r\n\r\n`, settles, and expects `events` to equal `[["terminal", { enabled: true }], ["terminal", { enabled: false }]]`.

In `internal/web/ui/src/store/startFleet.test.ts`, add after `saves at most once a second, the latest state`:
- `saves the snapshot after a terminal event`: `start()`; `handlers.onEvent("snapshot", fixtureSnapshot(NOW))`; advance 1000 ms (`await vi.advanceTimersByTimeAsync(1000)`) so the snapshot's own save fires, then `vi.mocked(saveSnapshot).mockClear()`; `handlers.onEvent("terminal", { enabled: true })`; advance 999 ms and expect `saveSnapshot` not called; advance 1 ms and expect it called once, and `store().snapshot?.terminal` is `true` (the saved snapshot is the store's, as in the test above: `toHaveBeenLastCalledWith(store().snapshot, NOW + 1000)`).

In `internal/web/ui/src/store/fleet.test.ts`, add after `replaces usage, quick replies and the device on their events`:
- `sets the snapshot's terminal flag from the terminal event`: after `store().apply("snapshot", fixtureSnapshot(NOW))` the snapshot's `terminal` is `false` (the fixture's); `store().apply("terminal", { enabled: true })` makes it `true`, and `apply("terminal", { enabled: false })` makes it `false` again; the rest of the snapshot (`quick_replies`, `usage`) is as the fixture has it.
- `ignores a terminal event before the first snapshot`: on a fresh store (as the file's other tests reset it), `store().apply("terminal", { enabled: true })` leaves `store().snapshot` null.

Fails without: `delivers the terminal event`: `terminal: true` removed from `KNOWN` (the stream drops the event, `events` is empty); `sets the snapshot's terminal flag from the terminal event`: the `case "terminal"` removed from `applied` (the flag stays false after the first event, and the type check fails first on the missing case), or the case reading `!event.data.enabled`; `ignores a terminal event before the first snapshot`: the case written without `change` (it would build a snapshot from nothing); `saves the snapshot after a terminal event`: `case "terminal":` removed from `afterEvent` (no save is scheduled, `saveSnapshot` is never called after the `mockClear`).

- [ ] **Step 2: Run it to see it fail**

Run: `loom job run -d "app tests for the terminal event" -- pnpm -C internal/web/ui exec vitest run src/api/stream.test.ts src/store/fleet.test.ts src/store/startFleet.test.ts`

Expected: FAIL: `delivers the terminal event` receives no events, `saves the snapshot after a terminal event` sees no save, and the store test's `terminal` stays `false` after the event (a type error shows too in `pnpm run typecheck`).

- [ ] **Step 3: Implement**

Add `terminal: true` to `KNOWN`, the `terminal` case to `applied` and to `afterEvent`'s `scheduleSave()` labels, and `"terminal": false` to the fixture, as the behaviour says. Then rebuild the bundle: `loom job run -d "rebuild the app bundle" -- pnpm -C internal/web/ui run build`.

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/webshot/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`, as three `loom job run` calls if the session's rules ask for one command per job.

Expected: the Go packages `ok`; `pnpm run check` passes (typecheck, lint, format check and Vitest, the two new cases among them); `dist:check` prints no difference.

- [ ] **Step 5: Commit**

Stage the rebuilt bundle's new files (the build renames hashed assets), then commit by path:

```bash
git add -N internal/web/dist
git commit -m "feat(web): the app follows the terminal switch from the snapshot and the stream" -- internal/web/ui/src/api/stream.ts internal/web/ui/src/api/stream.test.ts internal/web/ui/src/store/fleet.ts internal/web/ui/src/store/fleet.test.ts internal/web/ui/src/store/startFleet.test.ts tools/webshot/fixture/snapshot.json internal/web/dist
```

## Story 6: One terminal connection: frames, pump, pings and endings

- **Developer:** intermediate-dev
- **Files:** `internal/web/terminal_conn.go`, `internal/web/terminal_conn_test.go`, `go.mod`, `go.sum`, `vendor/**`
- **Interfaces:** exposes `termShell`, `termOpen`, `termEnd`, the endings, `termConn`, `newTermConn`, `run`, `end` and the bounds to Story 7; uses none
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ && CGO_ENABLED=0 go build ./... && GOOS=windows CGO_ENABLED=0 go build ./...'`
- **Review:** deep
- **Form:** full (concurrency between two pumps, a bounded buffer and an outside connection)

This story adds the WebSocket dependency (`github.com/coder/websocket` v1.8.15, vendored) and `termConn` in `internal/web/terminal_conn.go`, which serves one upgraded connection and knows nothing of routes, devices or tmux: it works through the `termShell` and `termOpen` it is given. The app's first `{"resize":…}` text frame opens the attach; the history, `rows` line feeds and a `{"state":"live"}` text frame go out; then the attach's output goes through `vt.NewAltScreenFilter` into a bounded queue that a sender goroutine writes as binary frames, so a browser that falls behind is ended (`termEndSlow`) and the attach is never blocked on the network. The connection's own frame cap lifts the library's read limit: the library's documented default is 32768 bytes, closing with 1009 (checked in its `read.go` at v1.8.15), so a 64 KiB frame would otherwise be refused by the library, not by loom; `run` turns the limit off and reads each frame through a `termFrameMax+1` limit of its own, and a longer frame ends with `termEndBadFrame` once its remainder is discarded in the reader under a bound (`termDrainWait`), because the library's `Close` would otherwise wait on a stalled sender's half-sent frame with no timeout and hold the connection's slot for good. The same `Close` also reads, with no deadline, whatever frame the browser is part-way through sending when loom's close frame goes out (or starts after it), and the library cannot bound that, so `run` bounds the close itself: `recordHijack` wraps the writer given to `websocket.Accept` and records the connection it hijacks, `newTermConn` takes that connection as `raw`, and `run` closes `raw` outright when the close has not finished `termCloseWait` after `run` started it (the close frame is its first write), so `run` returns within that bound on every ending, whatever the browser sends or withholds. Every ending is one `termEnd`, first one wins, and `run` closes the WebSocket and the attach before it returns. No library call is given `run`'s `ctx`, since the library closes its socket the moment a context it was given is cancelled, before the 1001 close frame could be written: `run` watches `ctx` itself and ends the connection with `termEndStopping`, and every library call takes a context of the connection's own, cancelled only after the close frame has gone out. Pings run from the start, but a ping is excused when the reader goroutine, the only one that reads a pong, was busy opening the attach or sending its history at any point while the ping was outstanding, so a slow link never drops a connection that is still being set up. No landmine marker appears in this story's code; every goroutine defers the `recoverPanic` it was given first. Builds, test runs and `go mod vendor` go through `loom job`; the commands below are what each job runs.

### Task 6.1: The dependency, the types and the connection's lifecycle

`run` waits for one ending and tears the connection down; nothing yet reads frames (Task 6.2 replaces the stub reader). This task adds the dependency, the contract's types, endings and bounds, `recordHijack`, `end`, and `run`'s closing of the socket (bounded by `termCloseWait`, closing the hijacked connection outright past it), the attach and its goroutines.

**Files:**
- Create: `internal/web/terminal_conn.go`
- Create: `internal/web/terminal_conn_test.go`
- Modify: `go.mod`, `go.sum`, `vendor/modules.txt`
- Create: `vendor/github.com/coder/websocket/**` (by `go mod vendor`)

- [ ] **Step 1: Write the failing test**

Create `internal/web/terminal_conn_test.go`. The helpers (the fake attach, the fake `termOpen`, and a real WebSocket pair over `httptest`) serve every later task of this story.

```go
package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// termShellFake is an attach the tests drive: feed is the output it produces
// (closing it ends the attach by itself, as a shell's exit does), and what the
// connection does to it is kept.
type termShellFake struct {
	feed   chan []byte
	closed chan struct{}
	once   sync.Once
	closes atomic.Int32

	// panicOnRead makes Read panic: set before the fake is shared.
	panicOnRead bool

	mu      sync.Mutex
	rest    []byte
	in      bytes.Buffer
	resizes [][2]int
}

func newTermShellFake() *termShellFake {
	return &termShellFake{feed: make(chan []byte, 1024), closed: make(chan struct{})}
}

func (f *termShellFake) Read(p []byte) (int, error) {
	if f.panicOnRead {
		panic("the attach's read panicked")
	}
	f.mu.Lock()
	if len(f.rest) > 0 {
		n := copy(p, f.rest)
		f.rest = f.rest[n:]
		f.mu.Unlock()
		return n, nil
	}
	f.mu.Unlock()
	select {
	case b, ok := <-f.feed:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, b)
		if n < len(b) {
			f.mu.Lock()
			f.rest = b[n:]
			f.mu.Unlock()
		}
		return n, nil
	case <-f.closed:
		return 0, io.ErrClosedPipe
	}
}

func (f *termShellFake) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.in.Write(p)
}

func (f *termShellFake) Resize(cols int, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, [2]int{cols, rows})
	return nil
}

func (f *termShellFake) Close() error {
	f.closes.Add(1)
	f.once.Do(func() { close(f.closed) })
	return nil
}

// input is what the connection has written to the attach so far.
func (f *termShellFake) input() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.in.String()
}

// sizes are the resizes the connection has applied to the attach so far.
func (f *termShellFake) sizes() [][2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]int(nil), f.resizes...)
}

// termOpenFake is a termOpen: it records each call and answers with its
// shell, its history, or its error. When gate is set, open waits for it to
// close after recording the call.
type termOpenFake struct {
	shell *termShellFake
	hist  []byte
	err   error
	gate  chan struct{}

	mu   sync.Mutex
	seen [][2]int
}

func newTermOpenFake() *termOpenFake { return &termOpenFake{shell: newTermShellFake()} }

func (o *termOpenFake) open(cols int, rows int) (termShell, []byte, error) {
	o.mu.Lock()
	o.seen = append(o.seen, [2]int{cols, rows})
	o.mu.Unlock()
	if o.gate != nil {
		<-o.gate
	}
	if o.err != nil {
		return nil, nil, o.err
	}
	return o.shell, o.hist, nil
}

// calls are the sizes open was called with.
func (o *termOpenFake) calls() [][2]int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([][2]int(nil), o.seen...)
}

// termRigOpts shape startTermConn: the open and ended a connection is given
// (defaults: a fresh termOpenFake, and termEndExited), the recoverPanic
// (default: none, so a panic fails the test binary), tune (runs on the
// handler's goroutine before run, to set the connection's private fields) and
// before (runs right before run).
type termRigOpts struct {
	open         termOpen
	ended        func() termEnd
	recoverPanic func(where string)
	tune         func(c *termConn)
	before       func(c *termConn)
	dial         *websocket.DialOptions
}

// termRig is a termConn serving the server side of a real WebSocket pair; the
// test is the browser, on client.
type termRig struct {
	client *websocket.Conn
	conn   *termConn
	cancel context.CancelFunc
	result chan termEnd
}

// termStarted is what a test server's handler hands the test once it has built
// its connection: the connection, and the cancel of the context it runs under.
type termStarted struct {
	conn   *termConn
	cancel context.CancelFunc
}

// newTermServer serves a termConn built from o on each upgrade of an httptest
// server: the handler hands the connection on startedCh, runs it, and reports
// run's answer on result. startTermConn dials it with the library's client;
// a test that must put bytes on the wire the client never sends dials it by
// hand. The connection is built as the route builds it, with the hijacked
// connection recordHijack gives.
func newTermServer(t *testing.T, o termRigOpts) (*httptest.Server, chan termStarted, chan termEnd) {
	t.Helper()
	if o.open == nil {
		o.open = newTermOpenFake().open
	}
	if o.ended == nil {
		o.ended = func() termEnd { return termEndExited }
	}
	if o.recoverPanic == nil {
		o.recoverPanic = func(string) {}
	}
	startedCh := make(chan termStarted, 1)
	result := make(chan termEnd, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw, hijacked := recordHijack(w)
		ws, err := websocket.Accept(rw, r, nil)
		if err != nil {
			return
		}
		c := newTermConn(ws, hijacked(), o.open, o.ended, o.recoverPanic)
		if o.tune != nil {
			o.tune(c)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		startedCh <- termStarted{c, cancel}
		if o.before != nil {
			o.before(c)
		}
		result <- c.run(ctx)
	}))
	t.Cleanup(srv.Close)
	return srv, startedCh, result
}

func startTermConn(t *testing.T, o termRigOpts) *termRig {
	t.Helper()
	srv, startedCh, result := newTermServer(t, o)
	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	client, _, err := websocket.Dial(dctx, "ws"+strings.TrimPrefix(srv.URL, "http"), o.dial)
	require.NoError(t, err)
	client.SetReadLimit(4 << 20)
	t.Cleanup(func() { _ = client.CloseNow() })
	select {
	case st := <-startedCh:
		t.Cleanup(st.cancel)
		return &termRig{client: client, conn: st.conn, cancel: st.cancel, result: result}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never built its connection")
		return nil
	}
}

// termFrame is one message the browser received.
type termFrame struct {
	typ  websocket.MessageType
	data []byte
}

// readUntilClose reads what the browser is sent until the connection ends,
// answering a close frame as a browser does (the library answers only while
// reading). It returns the messages, and the close frame when the end had one.
func readUntilClose(c *websocket.Conn) ([]termFrame, *websocket.CloseError, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var frames []termFrame
	for {
		typ, b, err := c.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				return frames, &ce, nil
			}
			return frames, nil, err
		}
		frames = append(frames, termFrame{typ, b})
	}
}

// finish drains the browser's side until the close, then waits for run's
// answer. The close must be a close frame.
func (r *termRig) finish(t *testing.T) (termEnd, []termFrame, websocket.CloseError) {
	t.Helper()
	frames, ce, err := readUntilClose(r.client)
	require.NoError(t, err, "the connection ended without a close frame")
	require.NotNil(t, ce)
	return waitTermEnd(t, r.result), frames, *ce
}

func waitTermEnd(t *testing.T, ch <-chan termEnd) termEnd {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return")
		return termEnd{}
	}
}

// waitTermEndWithin is waitTermEnd with the bound a test asserts: run must have
// returned within d.
func waitTermEndWithin(t *testing.T, ch <-chan termEnd, d time.Duration) termEnd {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(d):
		t.Fatalf("run did not return within %v", d)
		return termEnd{}
	}
}

func readTerm(t *testing.T, c *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, b, err := c.Read(ctx)
	require.NoError(t, err)
	return typ, b
}

func sendTerm(t *testing.T, c *websocket.Conn, typ websocket.MessageType, p []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Write(ctx, typ, p))
}

func resizeJSON(cols int, rows int) string {
	return fmt.Sprintf(`{"resize":{"cols":%d,"rows":%d}}`, cols, rows)
}

func sendResize(t *testing.T, c *websocket.Conn, cols int, rows int) {
	t.Helper()
	sendTerm(t, c, websocket.MessageText, []byte(resizeJSON(cols, rows)))
}

func expectLive(t *testing.T, c *websocket.Conn) {
	t.Helper()
	typ, b := readTerm(t, c)
	require.Equal(t, websocket.MessageText, typ)
	require.Equal(t, `{"state":"live"}`, string(b))
}

func TestTermEnds_AreTheContractsCodesAndReasons(t *testing.T) {
	for _, tc := range []struct {
		name   string
		got    termEnd
		code   websocket.StatusCode
		reason string
	}{
		{"off", termEndOff, 4001, "the terminal is off on this host"},
		{"revoked", termEndRevoked, 4002, "this device is no longer paired"},
		{"session", termEndSession, 4003, "the session was paused or killed"},
		{"exited", termEndExited, 4004, "the shell exited"},
		{"slow", termEndSlow, 4005, "the browser fell behind"},
		{"bad frame", termEndBadFrame, 4006, "a frame loom does not understand"},
		{"no attach", termEndNoAttach, 4007, "loom could not attach to the shell"},
		{"stopping", termEndStopping, 1001, "loom is stopping"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.code, tc.got.code)
			assert.Equal(t, tc.reason, tc.got.reason)
			// A close frame's reason may hold 123 bytes.
			assert.LessOrEqual(t, len(tc.got.reason), 123)
		})
	}
}

func TestTermConn_AnEndingIsACloseFrameWithItsCodeAndReason(t *testing.T) {
	rig := startTermConn(t, termRigOpts{})
	rig.conn.end(termEndOff)
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndOff, end)
	assert.Equal(t, websocket.StatusCode(4001), ce.Code)
	assert.Equal(t, "the terminal is off on this host", ce.Reason)
}

func TestTermConn_TheFirstEndingWinsFromAnyGoroutine(t *testing.T) {
	rig := startTermConn(t, termRigOpts{})
	rig.conn.end(termEndRevoked)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rig.conn.end(termEndOff)
			rig.conn.end(termEndSlow)
		}()
	}
	wg.Wait()
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndRevoked, end)
	assert.Equal(t, websocket.StatusCode(4002), ce.Code)
}

func TestTermConn_ACancelledContextEndsItAsStopping(t *testing.T) {
	rig := startTermConn(t, termRigOpts{})
	rig.cancel()
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndStopping, end)
	assert.Equal(t, websocket.StatusGoingAway, ce.Code)
	assert.Equal(t, "loom is stopping", ce.Reason)
}

func TestTermConn_AContextCancelledBeforeRunStartsStillSendsTheStoppingFrame(t *testing.T) {
	release := make(chan struct{})
	rig := startTermConn(t, termRigOpts{before: func(*termConn) { <-release }})
	rig.cancel() // run is handed a context that is already cancelled
	close(release)
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndStopping, end)
	assert.Equal(t, websocket.StatusGoingAway, ce.Code)
	assert.Equal(t, "loom is stopping", ce.Reason)
}

func TestTermConn_TheBrowserLeavingIsTheZeroEnding(t *testing.T) {
	for _, tc := range []struct {
		name  string
		leave func(c *websocket.Conn)
	}{
		{"with a close frame", func(c *websocket.Conn) { _ = c.Close(websocket.StatusNormalClosure, "") }},
		{"without one", func(c *websocket.Conn) { _ = c.CloseNow() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := startTermConn(t, termRigOpts{})
			tc.leave(rig.client)
			assert.Equal(t, termEnd{}, waitTermEnd(t, rig.result))
		})
	}
}

func TestTermConn_RunClosesTheAttachItHolds(t *testing.T) {
	sh := newTermShellFake()
	rig := startTermConn(t, termRigOpts{tune: func(c *termConn) { c.shell = sh }})
	rig.conn.end(termEndOff)
	rig.finish(t)
	assert.GreaterOrEqual(t, sh.closes.Load(), int32(1))
}

func TestTermConn_ABrowserThatNeverAnswersTheCloseFrameIsClosedOutrightAfterTheBound(t *testing.T) {
	rig := startTermConn(t, termRigOpts{tune: func(c *termConn) { c.closeWait = 200 * time.Millisecond }})
	// The browser reads nothing, so the library never answers loom's close
	// frame: on its own, the library's close handshake gives up only after 5 s.
	rig.conn.end(termEndOff)
	assert.Equal(t, termEndOff, waitTermEndWithin(t, rig.result, 2*time.Second),
		"run returns within the bound, with the ending it sent")
}

func TestRecordHijack_AcceptThroughItUpgradesAndGivesTheHijackedConnection(t *testing.T) {
	type seen struct{ before, after bool }
	got := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw, hijacked := recordHijack(w)
		before := hijacked() != nil
		ws, err := websocket.Accept(rw, r, nil)
		if err != nil {
			got <- seen{before, false}
			return
		}
		defer func() { _ = ws.CloseNow() }()
		raw := hijacked()
		got <- seen{before, raw != nil}
		if raw != nil {
			_ = raw.Close() // the connection under the WebSocket, closed outright
		}
	}))
	t.Cleanup(srv.Close)

	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	client, _, err := websocket.Dial(dctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err, "Accept given the wrapped writer upgrades")
	t.Cleanup(func() { _ = client.CloseNow() })
	assert.Equal(t, seen{before: false, after: true}, <-got,
		"nil before the hijack, the hijacked connection after it")

	// Closing the recorded connection drops the client with no close frame: it
	// is the one under the WebSocket, not some other.
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	_, _, err = client.Read(rctx)
	require.Error(t, err)
	assert.Equal(t, websocket.StatusCode(-1), websocket.CloseStatus(err), "a dropped connection, not a close frame")
}

func TestRecordHijack_AWriterThatCannotHijackFailsTheUpgradeAndGivesNoConnection(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rec := httptest.NewRecorder() // a ResponseRecorder cannot be hijacked
	rw, hijacked := recordHijack(rec)
	ws, err := websocket.Accept(rw, r, nil)
	require.Error(t, err)
	assert.Nil(t, ws)
	assert.Nil(t, hijacked())
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
```

Fails without: TestTermEnds_AreTheContractsCodesAndReasons: any ending's code or reason changed from the contract's, or a reason past 123 bytes.
Fails without: TestTermConn_AnEndingIsACloseFrameWithItsCodeAndReason: `closeSocket` sending no close frame (or `CloseNow` for every ending), or the wrong code.
Fails without: TestTermConn_TheFirstEndingWinsFromAnyGoroutine: the `sync.Once` in `end` removed (a second `close(c.done)` panics, or a later ending overwrites `c.ending`).
Fails without: TestTermConn_ACancelledContextEndsItAsStopping: `run`'s `<-ctx.Done()` case removed (`run` waits for an ending that never comes), or `run` handing `ctx` (or a context derived from it) to the reader instead of the connection's own `c.ioCtx` (the library closes the socket as the cancel lands, so the 1001 frame is never written and `finish` fails with `the connection ended without a close frame`).
Fails without: TestTermConn_AContextCancelledBeforeRunStartsStillSendsTheStoppingFrame: the same two changes; the cancel before `run` starts makes the failure deterministic, not a race.
Fails without: TestTermConn_TheBrowserLeavingIsTheZeroEnding: `readLoop`'s `leave` call on a read error removed (`run` never returns).
Fails without: TestTermConn_RunClosesTheAttachItHolds: the `c.closeShell()` call in `run` removed.
Fails without: TestTermConn_ABrowserThatNeverAnswersTheCloseFrameIsClosedOutrightAfterTheBound: the `case <-timer.C` branch of `closeSocket` removed or its `c.raw.Close()` call dropped (`run` then returns only when the library's own 5 s handshake gives up, past the 2 s the test allows), or `c.closeWait` not the timer's duration (the 5 s default).
Fails without: TestRecordHijack_AcceptThroughItUpgradesAndGivesTheHijackedConnection: `Hijack` of the wrapper not recording the connection (`after` is false), the wrapper not implementing `http.Hijacker` (`Accept` fails and the dial errors), or the function returning a connection other than the one `Accept` got (the client would not be dropped).
Fails without: TestRecordHijack_AWriterThatCannotHijackFailsTheUpgradeAndGivesNoConnection: the wrapper's `Hijack` swallowing the controller's error (`Accept` then reads a nil connection and panics, or reports success) instead of returning it.

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestTermEnds|TestTermConn'`

Expected: FAIL to build: `no required module provides package github.com/coder/websocket; to add it: go get github.com/coder/websocket`, and `undefined: termConn`, `undefined: newTermConn`, `undefined: recordHijack`, `undefined: termEnd` and the other new names.

- [ ] **Step 3: Implement**

First add the dependency (the module is imported by the file below, so `go get` records it as a direct requirement) and vendor it:

```bash
go get github.com/coder/websocket@v1.8.15
go mod vendor
```

`go.mod` gains `github.com/coder/websocket v1.8.15` in its `require` block, `go.sum` its two lines, `vendor/modules.txt` the module's entry (`# github.com/coder/websocket v1.8.15`, `## explicit; go 1.23`) and `vendor/github.com/coder/websocket/` appears. `vendor/` is committed and Nix builds from it, and nothing in it is edited by hand; Step 5 commits all of it.

Create `internal/web/terminal_conn.go`:

```go
package web

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// The bounds of one terminal connection.
const (
	// termFrameMax caps a frame from the app, and the size of each frame loom
	// sends of a history.
	termFrameMax = 64 << 10
	// The size the app may ask for.
	termMinCols = 2
	termMaxCols = 500
	termMinRows = 1
	termMaxRows = 200
	// termPingEvery is how often the connection is pinged, and termPongWait
	// how long a ping may go unanswered before the connection is dropped.
	termPingEvery = 20 * time.Second
	termPongWait  = 10 * time.Second
	// termOutBuffer bounds, in bytes, the output queued for a browser that is
	// slow to read.
	termOutBuffer = 1 << 20
	// termCloseWait bounds the close handshake: the library's Close reads
	// whatever frame the browser is sending with no deadline of its own, so a
	// connection whose close has not finished this long after run started it is
	// closed outright.
	termCloseWait = 5 * time.Second
	// termWriteTimeout bounds one write to the browser: a peer that stops
	// reading without closing would otherwise hold the sender forever.
	termWriteTimeout = 30 * time.Second
	// termDrainWait bounds how long the remainder of an oversized frame is
	// read and thrown away before the connection is ended: the library's Close
	// reads what is left of a half-read frame with no timeout of its own, so a
	// sender that stalls inside one would hold Close, and the connection's slot,
	// for good.
	termDrainWait = 5 * time.Second
)

// termShell is what a connection needs of an attach: the bytes the shell
// writes, the bytes typed into it, its size, and an end that never ends the
// shell itself. *tmux.ShellAttach satisfies it.
type termShell interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols int, rows int) error
	Close() error
}

// termOpen opens the attach at a size and returns the history to send before
// its output: the rows above the visible screen, each ended by \r\n, and a
// non-empty history also ends with the SGR reset \x1b[0m, as
// tmux.ShellHistory returns it.
type termOpen func(cols int, rows int) (termShell, []byte, error)

// termEnd is a close frame's code and reason. The zero termEnd is no close
// frame from loom: the browser left or the network failed.
type termEnd struct {
	code   websocket.StatusCode
	reason string
}

// The endings the app shows, by code. The spec's rule for part 2's app is that
// it reconnects after 1001, 4005 and an ending with no close frame, and after
// no other; part 1's page never reconnects.
var (
	termEndOff      = termEnd{code: 4001, reason: "the terminal is off on this host"}
	termEndRevoked  = termEnd{code: 4002, reason: "this device is no longer paired"}
	termEndSession  = termEnd{code: 4003, reason: "the session was paused or killed"}
	termEndExited   = termEnd{code: 4004, reason: "the shell exited"}
	termEndSlow     = termEnd{code: 4005, reason: "the browser fell behind"}
	termEndBadFrame = termEnd{code: 4006, reason: "a frame loom does not understand"}
	termEndNoAttach = termEnd{code: 4007, reason: "loom could not attach to the shell"}
	termEndStopping = termEnd{code: websocket.StatusGoingAway, reason: "loom is stopping"}
)

// termConn is one upgraded connection. run serves it until one ending, which
// end or a failure of its own decides, and then closes the WebSocket and the
// attach and waits for its goroutines.
type termConn struct {
	ws *websocket.Conn
	// raw is the connection under ws (recordHijack's), closed outright when the
	// close handshake outlasts closeWait. nil is allowed, for a test that needs
	// none: the handshake is then bounded only by the library's own timeouts.
	raw          net.Conn
	open         termOpen
	ended        func() termEnd
	recoverPanic func(where string)

	// The bounds, copied so a test may shrink them before run.
	pingEvery time.Duration
	pongWait  time.Duration
	outBuffer int
	drainWait time.Duration
	closeWait time.Duration

	// ioCtx is the context every call into the library takes: the connection's
	// own, never the caller's. The library closes its socket the moment a
	// context it was given is cancelled, so a library call given run's ctx would
	// close the socket before the close frame of a stopping connection could be
	// written. run cancels ioCtx (through ioCancel) only after that frame has
	// gone out.
	ioCtx    context.Context
	ioCancel context.CancelFunc

	// done closes when the ending is decided; ending is written once, before.
	done   chan struct{}
	once   sync.Once
	ending termEnd

	// shell is the attach once open has returned it; shellClosed is set by
	// closeShell, so an attach that opens after the ending is closed unused.
	mu          sync.Mutex
	shell       termShell
	shellClosed bool

	wg sync.WaitGroup
}

// recordHijack wraps w for websocket.Accept: Accept is handed the returned
// writer in place of w, and the returned function gives the connection Accept
// hijacked through it (nil before the hijack, and when it failed). The wrapper
// hijacks through http.NewResponseController, so a w that wraps the server's
// writer and unwraps to it is hijacked as the library itself would hijack it.
func recordHijack(w http.ResponseWriter) (http.ResponseWriter, func() net.Conn) {
	h := &hijackRecorder{ResponseWriter: w}
	return h, func() net.Conn {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.conn
	}
}

// hijackRecorder is the writer recordHijack returns: it satisfies the
// http.Hijacker websocket.Accept needs, and keeps what Hijack returns.
type hijackRecorder struct {
	http.ResponseWriter

	mu   sync.Mutex
	conn net.Conn
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(h.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()
	return conn, brw, nil
}

// newTermConn builds the connection for ws. raw is the connection under ws, as
// recordHijack gives it: run closes it outright when the close handshake has
// not finished termCloseWait after run started it, so run returns within that
// bound whatever the browser sends or withholds; nil is allowed, for a test
// that needs none. open is called with the app's first size. ended is asked
// once, when the attach's output ends by itself, for the ending to send.
// recoverPanic is the layer's: it is deferred first in every goroutine the
// connection starts.
func newTermConn(ws *websocket.Conn, raw net.Conn, open termOpen, ended func() termEnd, recoverPanic func(where string)) *termConn {
	ioCtx, ioCancel := context.WithCancel(context.Background())
	return &termConn{
		ws:           ws,
		raw:          raw,
		open:         open,
		ended:        ended,
		recoverPanic: recoverPanic,
		pingEvery:    termPingEvery,
		pongWait:     termPongWait,
		outBuffer:    termOutBuffer,
		drainWait:    termDrainWait,
		closeWait:    termCloseWait,
		ioCtx:        ioCtx,
		ioCancel:     ioCancel,
		done:         make(chan struct{}),
	}
}

// end ends the connection with e. It never blocks and may be called from any
// goroutine, before run too, and more than once: the first ending wins.
func (c *termConn) end(e termEnd) {
	c.once.Do(func() {
		c.ending = e
		close(c.done)
	})
}

// stopped reports whether an ending has been decided.
func (c *termConn) stopped() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// leave ends the connection with no close frame: the browser left or the
// network failed. It reads only the connection's own state: an ending already
// decided (run's stopping, an end from the route) stays, since the first one
// wins, and no library call fails on the caller's context, so a failure that
// reaches leave is never run's own stopping.
func (c *termConn) leave() {
	c.end(termEnd{})
}

// spawn runs f on a goroutine that run waits for. A panic in f recovers
// through the layer's recoverPanic, which stops the layer, and ends the
// connection with no close frame.
func (c *termConn) spawn(where string, f func()) {
	c.wg.Add(1)
	go func() {
		defer c.recoverPanic(where)
		defer c.wg.Done()
		finished := false
		defer func() {
			if !finished {
				c.end(termEnd{})
			}
		}()
		f()
		finished = true
	}()
}

// run serves the connection until it ends and returns the ending it sent: the
// zero termEnd when no close frame went out. Before it returns it has closed
// the attach and the WebSocket and its goroutines have returned.
//
// A cancelled ctx is exactly end(termEndStopping), and ctx goes no further
// than the select below: the library's calls take c.ioCtx, which is cancelled
// only once the close frame has been written. Whatever the browser sends or
// withholds, run returns within closeWait of starting the close (closeSocket).
func (c *termConn) run(ctx context.Context) termEnd {
	defer c.ioCancel()
	cctx := c.ioCtx
	c.ws.SetReadLimit(-1)
	c.spawn("terminal reader", func() { c.readLoop(cctx) })
	select {
	case <-c.done:
	case <-ctx.Done():
		c.end(termEndStopping)
		<-c.done
	}
	e := c.ending
	c.closeShell()
	c.closeSocket(e)
	c.ioCancel()
	c.wg.Wait()
	return e
}

// closeShell closes the attach, or marks it closed so one still opening is
// closed as soon as it is open.
func (c *termConn) closeShell() {
	c.mu.Lock()
	c.shellClosed = true
	sh := c.shell
	c.mu.Unlock()
	if sh != nil {
		// Closing a shell that already ended has nothing more to say.
		_ = sh.Close()
	}
}

// closeSocket sends e's close frame and closes the WebSocket; the zero
// ending closes with no frame. The close handshake's own errors (a peer that
// is already gone) change nothing: the connection is over either way.
//
// The handshake is bounded by closeWait. The library's Close reads, with no
// deadline, the rest of the frame the browser is part-way through sending and
// every data frame it sends after loom's close frame, so a browser that
// announces a large frame and stalls would hold Close, and the slot, for good.
// Close runs on a goroutine of its own, and when closeWait passes before it
// returns the hijacked connection is closed outright: every read and write
// on it then fails, Close returns, and so does every goroutine of the
// connection. With a nil raw the library's own timeouts are all there is.
func (c *termConn) closeSocket(e termEnd) {
	if e.code == 0 {
		_ = c.ws.CloseNow()
		return
	}
	closed := make(chan struct{})
	c.spawn("terminal close", func() {
		defer close(closed)
		_ = c.ws.Close(e.code, e.reason)
	})
	timer := time.NewTimer(c.closeWait)
	defer timer.Stop()
	select {
	case <-closed:
	case <-timer.C:
		if c.raw != nil {
			_ = c.raw.Close()
		}
		<-closed
	}
}

// readLoop is a stub that Task 6.2 replaces: it reads and drops every frame.
// ctx is the connection's own context.
func (c *termConn) readLoop(ctx context.Context) {
	for {
		if _, _, err := c.ws.Read(ctx); err != nil {
			c.leave()
			return
		}
	}
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'gofmt -l internal/web/terminal_conn.go internal/web/terminal_conn_test.go && CGO_ENABLED=0 go test ./internal/web/ -run "TestTermEnds|TestTermConn"'`

Expected: `gofmt -l` prints nothing (run `gofmt -w` on those two files if it names one), then `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/terminal_conn.go internal/web/terminal_conn_test.go vendor/github.com/coder
git commit -m "feat(web): add the websocket dependency and a terminal connection's lifecycle" -- internal/web/terminal_conn.go internal/web/terminal_conn_test.go go.mod go.sum vendor
```

### Task 6.2: Frames from the app: the first resize opens the attach

The reader goroutine's real body replaces Task 6.1's stub. The first text frame must be a size in bounds, and opens the attach through `termOpen`; later sizes resize it; binary frames are typed into it. The frame cap is the connection's own.

**Files:**
- Modify: `internal/web/terminal_conn.go`
- Test: `internal/web/terminal_conn_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/web/terminal_conn_test.go`, replace the import block

```go
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

with

```go
import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

and append:

```go
// dialRawTerm opens a TCP connection to srv and upgrades it by hand, so a test
// can put on the wire what the library's own client never sends, such as a
// frame header that announces more than follows. It returns the connection
// past the handshake.
func dialRawTerm(t *testing.T, srv *httptest.Server) net.Conn {
	t.Helper()
	nc, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })
	require.NoError(t, nc.SetDeadline(time.Now().Add(30*time.Second)))
	_, err = fmt.Fprintf(nc, "GET / HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\n"+
		"Connection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n\r\n", srv.Listener.Addr())
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(nc), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	return nc
}

// rawFrameHeader is the header of a masked client frame with FIN set and the
// opcode op (2 is binary), announcing n payload bytes in the 64-bit length
// form, with a mask key of zeros so the payload goes as it is.
func rawFrameHeader(op byte, n uint64) []byte {
	hdr := []byte{0x80 | op, 0x80 | 127}
	hdr = binary.BigEndian.AppendUint64(hdr, n)
	return append(hdr, 0, 0, 0, 0)
}

func TestTermConn_TheFirstResizeOpensTheAttachAtThatSizeAndLaterOnesResizeIt(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 120, 40)
	expectLive(t, rig.client)
	assert.Equal(t, [][2]int{{120, 40}}, fake.calls())

	sendResize(t, rig.client, 100, 30)
	sendTerm(t, rig.client, websocket.MessageBinary, []byte("ls"))
	sendTerm(t, rig.client, websocket.MessageBinary, []byte("\r"))
	waitUntil(t, "the attach to hear the resize and the keys", func() bool {
		return fake.shell.input() == "ls\r" && len(fake.shell.sizes()) == 1
	})
	assert.Equal(t, [][2]int{{100, 30}}, fake.shell.sizes())
	assert.Len(t, fake.calls(), 1, "a later resize resizes the attach; it never opens another")

	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_ABadFirstFrameEndsItAndOpensNoAttach(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   websocket.MessageType
		frame string
	}{
		{"bytes before the first resize", websocket.MessageBinary, "ls\r"},
		{"columns below the minimum", websocket.MessageText, resizeJSON(1, 24)},
		{"columns above the maximum", websocket.MessageText, resizeJSON(501, 24)},
		{"rows below the minimum", websocket.MessageText, resizeJSON(80, 0)},
		{"rows above the maximum", websocket.MessageText, resizeJSON(80, 201)},
		{"a negative size", websocket.MessageText, resizeJSON(-5, 24)},
		{"text that is not JSON", websocket.MessageText, "not json"},
		{"JSON that is not a resize", websocket.MessageText, `{}`},
		{"a null resize", websocket.MessageText, `{"resize":null}`},
		{"a field the frame does not take", websocket.MessageText, `{"resize":{"cols":80,"rows":24},"extra":1}`},
		{"a field the size does not take", websocket.MessageText, `{"resize":{"cols":80,"rows":24,"x":1}}`},
		{"a size that is a string", websocket.MessageText, `{"resize":{"cols":"80","rows":24}}`},
		{"a size that is not whole", websocket.MessageText, `{"resize":{"cols":80.5,"rows":24}}`},
		{"a second value after the resize", websocket.MessageText, resizeJSON(80, 24) + ` {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newTermOpenFake()
			rig := startTermConn(t, termRigOpts{open: fake.open})
			sendTerm(t, rig.client, tc.typ, []byte(tc.frame))
			end, _, ce := rig.finish(t)
			assert.Equal(t, termEndBadFrame, end)
			assert.Equal(t, websocket.StatusCode(4006), ce.Code)
			assert.Equal(t, "a frame loom does not understand", ce.Reason)
			assert.Empty(t, fake.calls(), "a bad first frame opens no attach")
		})
	}
}

func TestTermConn_EachBoundOfTheSizeIsInclusive(t *testing.T) {
	for _, size := range [][2]int{{2, 24}, {500, 24}, {80, 1}, {80, 200}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			fake := newTermOpenFake()
			rig := startTermConn(t, termRigOpts{open: fake.open})
			sendResize(t, rig.client, size[0], size[1])
			expectLive(t, rig.client)
			assert.Equal(t, [][2]int{size}, fake.calls())
			rig.conn.end(termEndOff)
			rig.finish(t)
		})
	}
}

func TestTermConn_ABadLaterFrameEndsItAndClosesTheAttach(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame string
	}{
		{"a size out of bounds", resizeJSON(501, 24)},
		{"text that is not a resize", `{"state":"live"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newTermOpenFake()
			rig := startTermConn(t, termRigOpts{open: fake.open})
			sendResize(t, rig.client, 80, 24)
			expectLive(t, rig.client)
			sendTerm(t, rig.client, websocket.MessageText, []byte(tc.frame))
			end, _, ce := rig.finish(t)
			assert.Equal(t, termEndBadFrame, end)
			assert.Equal(t, websocket.StatusCode(4006), ce.Code)
			assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
			assert.Empty(t, fake.shell.sizes(), "a refused size is never applied")
		})
	}
}

func TestTermConn_TheFrameCapIsTheConnectionsOwn(t *testing.T) {
	live := func(t *testing.T) (*termRig, *termOpenFake) {
		fake := newTermOpenFake()
		rig := startTermConn(t, termRigOpts{open: fake.open})
		sendResize(t, rig.client, 80, 24)
		expectLive(t, rig.client)
		return rig, fake
	}
	t.Run("a frame of exactly the cap is typed into the attach", func(t *testing.T) {
		rig, fake := live(t)
		sendTerm(t, rig.client, websocket.MessageBinary, bytes.Repeat([]byte("a"), termFrameMax))
		waitUntil(t, "the whole frame to reach the attach", func() bool {
			return len(fake.shell.input()) == termFrameMax
		})
		rig.conn.end(termEndOff)
		rig.finish(t)
	})
	for _, tc := range []struct {
		name string
		size int
	}{
		{"one byte over the cap", termFrameMax + 1},
		{"a frame far over it", 1 << 20},
	} {
		t.Run(tc.name+" is a bad frame, never the library's 1009", func(t *testing.T) {
			rig, fake := live(t)
			sendTerm(t, rig.client, websocket.MessageBinary, bytes.Repeat([]byte("a"), tc.size))
			end, _, ce := rig.finish(t)
			assert.Equal(t, termEndBadFrame, end)
			assert.Equal(t, websocket.StatusCode(4006), ce.Code)
			assert.Less(t, len(fake.shell.input()), tc.size, "an oversized frame is never typed in")
			assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
		})
	}
}

func TestTermConn_AnOversizedFrameThatStallsNeverLeavesRunBlocked(t *testing.T) {
	srv, _, result := newTermServer(t, termRigOpts{
		tune: func(c *termConn) { c.drainWait = 200 * time.Millisecond },
	})
	nc := dialRawTerm(t, srv)

	// A masked binary frame whose header announces 1 GiB, then a little more
	// than the cap of it, and then nothing: the sender stalls inside the frame
	// and keeps the TCP connection open.
	hdr := []byte{0x82, 0x80 | 127}
	hdr = binary.BigEndian.AppendUint64(hdr, 1<<30)
	hdr = append(hdr, 0, 0, 0, 0) // a mask key of zeros: the payload goes as it is
	_, err := nc.Write(hdr)
	require.NoError(t, err)
	_, err = nc.Write(bytes.Repeat([]byte("a"), termFrameMax+4096))
	require.NoError(t, err)

	start := time.Now()
	assert.Equal(t, termEnd{}, waitTermEnd(t, result),
		"the sender stalled, so no close frame could reach it")
	assert.Less(t, time.Since(start), 3*time.Second, "run returns within the drain bound, not when TCP gives up")
}

func TestTermConn_AFrameThatStallsAfterTheCloseFrameNeverLeavesRunBlocked(t *testing.T) {
	srv, _, result := newTermServer(t, termRigOpts{
		tune: func(c *termConn) { c.closeWait = 200 * time.Millisecond },
	})
	nc := dialRawTerm(t, srv)

	// A complete binary frame one byte over the cap: the reader discards it
	// whole and ends the connection with termEndBadFrame, and loom's close
	// frame goes out. The browser never answers it. It starts another frame
	// instead, announcing 1 GiB, sends a few bytes of it and stalls: the
	// library's Close reads it byte by byte with no deadline.
	over := bytes.Repeat([]byte("a"), termFrameMax+1)
	_, err := nc.Write(append(rawFrameHeader(2, uint64(len(over))), over...))
	require.NoError(t, err)
	_, err = nc.Write(append(rawFrameHeader(2, 1<<30), "abcd"...))
	require.NoError(t, err)

	assert.Equal(t, termEndBadFrame, waitTermEndWithin(t, result, 2*time.Second),
		"run returns within the close bound, with the ending it sent")
}

func TestTermConn_AnEndingFromOutsideDuringAFrameThatStallsNeverLeavesRunBlocked(t *testing.T) {
	srv, started, result := newTermServer(t, termRigOpts{
		tune: func(c *termConn) { c.closeWait = 200 * time.Millisecond },
	})
	nc := dialRawTerm(t, srv)
	var st termStarted
	select {
	case st = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never built its connection")
	}

	// Part of a frame that announces 1000 bytes: the reader is inside it. The
	// route then ends the connection from outside (a revoked device, a rebind),
	// the browser sends one more byte and stalls. The pauses put the events in
	// that order; the assertion holds in any order.
	_, err := nc.Write(append(rawFrameHeader(2, 1000), bytes.Repeat([]byte("a"), 10)...))
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	st.conn.end(termEndRevoked)
	time.Sleep(100 * time.Millisecond)
	_, err = nc.Write([]byte("a"))
	require.NoError(t, err)

	assert.Equal(t, termEndRevoked, waitTermEndWithin(t, result, 2*time.Second),
		"run returns within the close bound, with the ending it sent")
}

func TestTermConn_AFailedAttachEndsWithNoAttachAndTellsTheAppNoCause(t *testing.T) {
	fake := newTermOpenFake()
	fake.err = errors.New("tmux: no server running on /tmp/tmux-1000/default")
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndNoAttach, end)
	assert.Equal(t, websocket.StatusCode(4007), ce.Code)
	assert.Equal(t, "loom could not attach to the shell", ce.Reason)
}

func TestTermConn_AnEndingBeforeRunOpensNoAttach(t *testing.T) {
	fake := newTermOpenFake()
	sent := make(chan struct{})
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		before: func(c *termConn) {
			<-sent // the browser's resize is already on the wire
			c.end(termEndOff)
		},
	})
	sendResize(t, rig.client, 80, 24)
	close(sent)
	end, frames, ce := rig.finish(t)
	assert.Equal(t, termEndOff, end)
	assert.Equal(t, websocket.StatusCode(4001), ce.Code)
	assert.Empty(t, fake.calls(), "an ending decided before run opens nothing")
	assert.Empty(t, frames)
}

func TestTermConn_AnAttachThatOpensAfterTheEndingIsClosedUnused(t *testing.T) {
	fake := newTermOpenFake()
	fake.gate = make(chan struct{})
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	waitUntil(t, "open to be called", func() bool { return len(fake.calls()) == 1 })
	rig.conn.end(termEndOff)

	// The close frame goes out after run has closed the attach it holds (none
	// yet), so the open that returns now must close its own.
	frames, ce, err := readUntilClose(rig.client)
	require.NoError(t, err)
	require.NotNil(t, ce)
	close(fake.gate)
	assert.Equal(t, termEndOff, waitTermEnd(t, rig.result))
	assert.Equal(t, websocket.StatusCode(4001), ce.Code)
	assert.Empty(t, frames, "an ended connection says nothing of a late attach")
	assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
}
```

Fails without: TestTermConn_TheFirstResizeOpensTheAttachAtThatSizeAndLaterOnesResizeIt: `attach` opening at a different size than the frame's, a later resize opening a second attach, or binary frames not written to the attach.
Fails without: TestTermConn_ABadFirstFrameEndsItAndOpensNoAttach: the `sh == nil` bad-frame guard for binary bytes; each of the four bound comparisons in `parseResize` (one case each), `DisallowUnknownFields`, the `m.Resize == nil` check and the trailing-value check (the case that names each).
Fails without: TestTermConn_EachBoundOfTheSizeIsInclusive: any of the four bound comparisons in `parseResize` made exclusive (`<` to `<=` or `>` to `>=`).
Fails without: TestTermConn_ABadLaterFrameEndsItAndClosesTheAttach: size checking applied only to the first resize, or the shell not closed on a bad frame.
Fails without: TestTermConn_TheFrameCapIsTheConnectionsOwn: `c.ws.SetReadLimit(-1)` removed in `run` (the exactly-the-cap frame then fails with the library's 1009 and a size above 32768) or the `len(b) > termFrameMax` check in `readMessage` removed (the over-cap frames reach the attach).
Fails without: TestTermConn_AnOversizedFrameThatStallsNeverLeavesRunBlocked: the discard in `readMessage` removed, so the reader ends the connection with the frame half-read and `run`'s `Close` waits on the stalled sender for good (`run did not return`), or `time.AfterFunc(c.drainWait, cancel)` removed (the discard waits on the sender for good and nothing ends the connection: `run did not return`).
Fails without: TestTermConn_AFrameThatStallsAfterTheCloseFrameNeverLeavesRunBlocked: the `case <-timer.C` branch of `closeSocket` removed or its `c.raw.Close()` dropped (`Close` then waits on the 1 GiB frame for good: `run did not return within 2s`), or `newTermServer` passing a nil `raw`.
Fails without: TestTermConn_AnEndingFromOutsideDuringAFrameThatStallsNeverLeavesRunBlocked: the same two changes (`Close` waits for the reader's lock, then on the stalled frame, past the 2 s the test allows).
Fails without: TestTermConn_AFailedAttachEndsWithNoAttachAndTellsTheAppNoCause: the `err != nil` branch of `attach` ending with `termEndNoAttach`.
Fails without: TestTermConn_AnEndingBeforeRunOpensNoAttach: the `c.stopped()` check at the top of `attach`.
Fails without: TestTermConn_AnAttachThatOpensAfterTheEndingIsClosedUnused: the `shellClosed` check in `attach` (the late attach is stored in `c.shell` after `closeShell` ran and is never closed).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestTermConn'`

Expected: the new tests FAIL (the stub reader drops every frame): `expectLive`'s read times out with `context deadline exceeded`, and `finish` fails with `the connection ended without a close frame` (a bad frame ends nothing); the stalled oversized frame's test fails with `run did not return` after 15 seconds (the stub reads the frame with no bound), and the two close-bound tests fail with `run did not return within 2s` (the stub never ends the connection); the Task 6.1 tests still pass.

- [ ] **Step 3: Implement**

In `internal/web/terminal_conn.go`, replace the import block

```go
import (
	"bufio"
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)
```

with

```go
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)
```

and replace the whole stub

```go
// readLoop is a stub that Task 6.2 replaces: it reads and drops every frame.
// ctx is the connection's own context.
func (c *termConn) readLoop(ctx context.Context) {
	for {
		if _, _, err := c.ws.Read(ctx); err != nil {
			c.leave()
			return
		}
	}
}
```

with:

```go
// termLiveFrame is the text frame that tells the app the attach is open.
var termLiveFrame = []byte(`{"state":"live"}`)

// readLoop reads the app's frames until the connection ends. The first text
// frame must be a size and opens the attach; later sizes resize it; binary
// frames are typed into it. ctx is the connection's own context.
func (c *termConn) readLoop(ctx context.Context) {
	var sh termShell
	for {
		typ, b, over, err := c.readMessage(ctx)
		if err != nil {
			c.leave()
			return
		}
		if over {
			c.end(termEndBadFrame)
			return
		}
		switch typ {
		case websocket.MessageBinary:
			if sh == nil {
				c.end(termEndBadFrame)
				return
			}
			// A failed write or resize means the attach is gone: its output
			// ends too, and that ending says why.
			_, _ = sh.Write(b)
		case websocket.MessageText:
			cols, rows, ok := parseResize(b)
			if !ok {
				c.end(termEndBadFrame)
				return
			}
			if sh == nil {
				if sh = c.attach(ctx, cols, rows); sh == nil {
					return
				}
				continue
			}
			_ = sh.Resize(cols, rows)
		}
	}
}

// readMessage reads the app's next message under the frame cap. The library's
// own read limit is off (run lifts it), so the cap is this one: no more than
// termFrameMax+1 bytes are ever held, and a message that fills them is over
// the cap (over is true, with no bytes).
//
// An over-cap message is not abandoned half-read: its remainder is read and
// thrown away first, under termDrainWait, because the library's Close reads
// what is left of the frame in progress with no timeout, so a sender that
// stalled inside the frame would hold Close for good. Each message gets a
// context of its own for that: when the timer cancels it, the library closes
// the socket and the read fails, and so does the message (a zero ending: the
// sender stalled, so no close frame could reach it). ctx is the connection's
// own context.
func (c *termConn) readMessage(ctx context.Context) (typ websocket.MessageType, b []byte, over bool, err error) {
	mctx, cancel := context.WithCancel(ctx)
	defer cancel()
	typ, r, err := c.ws.Reader(mctx)
	if err != nil {
		return 0, nil, false, err
	}
	b, err = io.ReadAll(io.LimitReader(r, termFrameMax+1))
	if err != nil {
		return 0, nil, false, err
	}
	if len(b) <= termFrameMax {
		return typ, b, false, nil
	}
	stop := time.AfterFunc(c.drainWait, cancel)
	defer stop.Stop()
	if _, err := io.Copy(io.Discard, r); err != nil {
		return 0, nil, false, err
	}
	return typ, nil, true, nil
}

// parseResize reads the app's one control frame, {"resize":{"cols":n,"rows":n}},
// and refuses anything else (an unknown field, a second value, a size out of
// bounds).
func parseResize(b []byte) (cols int, rows int, ok bool) {
	var m struct {
		Resize *struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		} `json:"resize"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil || m.Resize == nil {
		return 0, 0, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return 0, 0, false
	}
	cols, rows = m.Resize.Cols, m.Resize.Rows
	if cols < termMinCols || cols > termMaxCols || rows < termMinRows || rows > termMaxRows {
		return 0, 0, false
	}
	return cols, rows, true
}

// attach opens the attach at the app's first size and tells the app it is
// live. It returns the attach, or nil when the connection is over: a failed
// open ends it with termEndNoAttach (the route's termOpen logs the cause; the
// app is told only that loom could not attach), and an attach that opens after
// the ending is closed unused.
func (c *termConn) attach(ctx context.Context, cols int, rows int) termShell {
	if c.stopped() {
		return nil
	}
	sh, hist, err := c.open(cols, rows)
	if err != nil {
		c.end(termEndNoAttach)
		return nil
	}
	c.mu.Lock()
	if c.shellClosed {
		c.mu.Unlock()
		_ = sh.Close()
		return nil
	}
	c.shell = sh
	c.mu.Unlock()
	if c.stopped() || !c.attached(ctx, sh, hist, rows) {
		return nil
	}
	return sh
}

// attached runs once the attach is open and held: it tells the app the
// terminal is live. Task 6.3 replaces this body with the history and the pumps.
func (c *termConn) attached(ctx context.Context, sh termShell, hist []byte, rows int) bool {
	if err := c.write(ctx, websocket.MessageText, termLiveFrame); err != nil {
		c.leave()
		return false
	}
	return true
}

// write sends one message to the browser under termWriteTimeout.
func (c *termConn) write(ctx context.Context, typ websocket.MessageType, p []byte) error {
	ctx, cancel := context.WithTimeout(ctx, termWriteTimeout)
	defer cancel()
	return c.ws.Write(ctx, typ, p)
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'gofmt -l internal/web/terminal_conn.go internal/web/terminal_conn_test.go && CGO_ENABLED=0 go test ./internal/web/ -run "TestTermEnds|TestTermConn"'`

Expected: `gofmt -l` prints nothing, then `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): read the app's frames and open the attach on its first resize" -- internal/web/terminal_conn.go internal/web/terminal_conn_test.go
```

### Task 6.3: The attach's output: history, the filter and the bounded buffer

After the attach opens, the history and `rows` line feeds go out as binary frames of at most `termFrameMax`, outside the buffer, then the `live` frame; then the attach's output goes through `vt.NewAltScreenFilter` into a queue bounded by `termOutBuffer` bytes, which a sender goroutine writes as binary frames. A full queue ends the connection with `termEndSlow`, and the pump never waits on the network.

**Files:**
- Modify: `internal/web/terminal_conn.go`
- Test: `internal/web/terminal_conn_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/web/terminal_conn_test.go`:

```go
// readBinaryN reads binary frames until n bytes have arrived.
func readBinaryN(t *testing.T, c *websocket.Conn, n int) []byte {
	t.Helper()
	var got []byte
	for len(got) < n {
		typ, b := readTerm(t, c)
		require.Equal(t, websocket.MessageBinary, typ)
		got = append(got, b...)
	}
	return got
}

func TestTermConn_TheHistoryGoesFirstThenRowsLineFeedsThenTheAttachsOutput(t *testing.T) {
	fake := newTermOpenFake()
	fake.hist = []byte("one\r\ntwo\r\n")
	fake.shell.feed <- []byte("early")
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)

	var before []byte
	for {
		typ, b := readTerm(t, rig.client)
		if typ == websocket.MessageText {
			assert.Equal(t, `{"state":"live"}`, string(b))
			break
		}
		before = append(before, b...)
	}
	assert.Equal(t, "one\r\ntwo\r\n"+strings.Repeat("\n", 24), string(before),
		"the history, then one line feed per row of the pane")
	assert.Equal(t, "early", string(readBinaryN(t, rig.client, 5)),
		"the output the attach made before the history went out comes after it")

	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_NoHistoryIsNoLineFeeds(t *testing.T) {
	fake := newTermOpenFake()
	fake.shell.feed <- []byte("x")
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client) // the very first message: nothing is sent before it
	assert.Equal(t, "x", string(readBinaryN(t, rig.client, 1)))
	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_ALongHistoryIsSentInFramesOfAtMostTheCap(t *testing.T) {
	fake := newTermOpenFake()
	fake.hist = bytes.Repeat([]byte("history line\r\n"), 12000) // about 164 KiB
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	var got []byte
	frames := 0
	for {
		typ, b := readTerm(t, rig.client)
		if typ == websocket.MessageText {
			break
		}
		frames++
		assert.LessOrEqual(t, len(b), termFrameMax)
		got = append(got, b...)
	}
	assert.Equal(t, string(fake.hist)+strings.Repeat("\n", 24), string(got))
	assert.GreaterOrEqual(t, frames, 3)
	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_TheOutputIsFilteredOfAlternateScreenSwitches(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)
	// A switch split across two reads, as the pty can deliver it.
	fake.shell.feed <- []byte("\x1b[?1049hhello \x1b[?10")
	fake.shell.feed <- []byte("49lworld")
	got := readBinaryN(t, rig.client, len("hello world"))
	assert.Equal(t, "hello world", string(got))
	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_OutputArrivesInOrder(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)
	var want strings.Builder
	for i := 0; i < 200; i++ {
		s := fmt.Sprintf("line %03d\r\n", i)
		want.WriteString(s)
		fake.shell.feed <- []byte(s)
	}
	assert.Equal(t, want.String(), string(readBinaryN(t, rig.client, want.Len())))
	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_ABrowserThatFallsBehindIsEndedAndNeverBlocksTheAttach(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		tune: func(c *termConn) { c.outBuffer = 64 << 10 },
	})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)

	// An attach that never waits for the browser...
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	go func() {
		for {
			select {
			case fake.shell.feed <- chunk:
			case <-fake.shell.closed:
				return
			}
		}
	}()
	// ...and a browser that reads, but slower than the attach writes.
	type outcome struct {
		ce  *websocket.CloseError
		err error
	}
	outcomes := make(chan outcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for {
			if _, _, err := rig.client.Read(ctx); err != nil {
				var ce websocket.CloseError
				if errors.As(err, &ce) {
					outcomes <- outcome{ce: &ce}
				} else {
					outcomes <- outcome{err: err}
				}
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	assert.Equal(t, termEndSlow, waitTermEnd(t, rig.result))
	select {
	case o := <-outcomes:
		require.NoError(t, o.err)
		assert.Equal(t, websocket.StatusCode(4005), o.ce.Code)
		assert.Equal(t, "the browser fell behind", o.ce.Reason)
	case <-time.After(20 * time.Second):
		t.Fatal("the browser never saw the ending")
	}
	assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
}

func TestOutQueue_HoldsAtMostItsLimitAndKeepsWhatItHolds(t *testing.T) {
	ctx := context.Background()
	q := newOutQueue(10)
	assert.True(t, q.push([]byte("12345")))
	assert.True(t, q.push([]byte("67890")), "exactly the limit fits")
	assert.False(t, q.push([]byte("x")), "one byte past the limit does not")

	got, err := q.pop(ctx)
	require.NoError(t, err)
	assert.Equal(t, "12345", string(got), "what the queue holds stays, in order")
	assert.True(t, q.push([]byte("abcde")), "a pop makes room")

	q.finish()
	var rest []string
	for {
		b, err := q.pop(ctx)
		if err != nil {
			assert.ErrorIs(t, err, io.EOF, "a finished queue ends only once it is empty")
			break
		}
		rest = append(rest, string(b))
	}
	assert.Equal(t, []string{"67890", "abcde"}, rest)
}

func TestOutQueue_AnEmptyQueueTakesAChunkLargerThanItsLimit(t *testing.T) {
	q := newOutQueue(4)
	assert.True(t, q.push([]byte("12345678")), "one read larger than the limit is no overflow")
	assert.False(t, q.push([]byte("x")))
}

func TestOutQueue_PushCopiesAndPopStopsOnItsContext(t *testing.T) {
	q := newOutQueue(100)
	src := []byte("abc")
	require.True(t, q.push(src))
	src[0] = 'X'
	got, err := q.pop(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "abc", string(got), "the caller may reuse its buffer")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = q.pop(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}
```

Fails without: TestTermConn_TheHistoryGoesFirstThenRowsLineFeedsThenTheAttachsOutput: the history sent after the pump starts (the pre-fed `early` would come first), the line feeds omitted or not `rows` of them, or the `live` frame sent before the history.
Fails without: TestTermConn_NoHistoryIsNoLineFeeds: the `len(hist) > 0` guard removed (the first message is then a binary frame of line feeds, not `live`).
Fails without: TestTermConn_ALongHistoryIsSentInFramesOfAtMostTheCap: `sendBinary` writing its argument as one frame instead of `termFrameMax` slices.
Fails without: TestTermConn_TheOutputIsFilteredOfAlternateScreenSwitches: `vt.NewAltScreenFilter` removed from `pumpLoop` (the escape sequences arrive).
Fails without: TestTermConn_OutputArrivesInOrder: the queue or sender reordering or dropping a chunk (a `pop` taking from the tail).
Fails without: TestTermConn_ABrowserThatFallsBehindIsEndedAndNeverBlocksTheAttach: `termSink.Write` blocking on the socket or ignoring a full queue (the connection is then never ended as slow and `run` does not return), or `termEndSlow` replaced by another ending.
Fails without: TestOutQueue_HoldsAtMostItsLimitAndKeepsWhatItHolds: `push`'s limit comparison changed (`>` to `>=` fails the exactly-the-limit case, removed fails the past-the-limit case), `finish` ending the queue before it is drained.
Fails without: TestOutQueue_AnEmptyQueueTakesAChunkLargerThanItsLimit: the `q.size > 0` term removed from `push`'s refusal (a lone large read would end every connection), or kept without the limit term (the second push would be taken).
Fails without: TestOutQueue_PushCopiesAndPopStopsOnItsContext: the copy in `push` removed (the caller's later write changes the queued bytes), or `pop` ignoring its context.

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestTermConn|TestOutQueue'`

Expected: FAIL to build: `undefined: newOutQueue`.

- [ ] **Step 3: Implement**

In `internal/web/terminal_conn.go`, replace the import block

```go
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)
```

with

```go
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/aidan-bailey/loom/session/vt"
)
```

Replace the function

```go
// attached runs once the attach is open and held: it tells the app the
// terminal is live. Task 6.3 replaces this body with the history and the pumps.
func (c *termConn) attached(ctx context.Context, sh termShell, hist []byte, rows int) bool {
	if err := c.write(ctx, websocket.MessageText, termLiveFrame); err != nil {
		c.leave()
		return false
	}
	return true
}
```

with:

```go
// attached runs once the attach is open and held. The history goes first,
// then one line feed per row (they push the history up into the browser's
// scrollback, where tmux's first draw would otherwise paint over it; with no
// history there is nothing to push), outside the output buffer and before the
// pump starts, then the live frame, then the attach's output.
func (c *termConn) attached(ctx context.Context, sh termShell, hist []byte, rows int) bool {
	if len(hist) > 0 {
		if !c.sendBinary(ctx, hist) || !c.sendBinary(ctx, bytes.Repeat([]byte("\n"), rows)) {
			return false
		}
	}
	if err := c.write(ctx, websocket.MessageText, termLiveFrame); err != nil {
		c.leave()
		return false
	}
	q := newOutQueue(c.outBuffer)
	c.spawn("terminal output", func() { c.sendLoop(ctx, q) })
	c.spawn("terminal attach", func() { c.pumpLoop(sh, q) })
	return true
}

// sendBinary writes p as binary frames of at most termFrameMax. It reports
// false once the connection is over, or a write failed.
func (c *termConn) sendBinary(ctx context.Context, p []byte) bool {
	for len(p) > 0 {
		if c.stopped() {
			return false
		}
		n := min(len(p), termFrameMax)
		if err := c.write(ctx, websocket.MessageBinary, p[:n]); err != nil {
			c.leave()
			return false
		}
		p = p[n:]
	}
	return true
}

// termReadChunk is how much of the attach's output one read takes.
const termReadChunk = 32 << 10

// errTermSlow is what the output sink tells the filter when the queue is full.
var errTermSlow = errors.New("the browser fell behind")

// termSink is where the filtered output goes: the connection's queue. It
// never waits on the network, so the pump never does.
type termSink struct {
	c *termConn
	q *outQueue
}

func (s termSink) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !s.q.push(p) {
		s.c.end(termEndSlow)
		return 0, errTermSlow
	}
	return len(p), nil
}

// pumpLoop reads the attach's output through the alternate-screen filter into
// the queue until the attach ends (by itself or by Close), then finishes the
// queue so the sender ends once it has sent what is queued.
func (c *termConn) pumpLoop(sh termShell, q *outQueue) {
	filter := vt.NewAltScreenFilter(termSink{c: c, q: q})
	buf := make([]byte, termReadChunk)
	for {
		n, err := sh.Read(buf)
		if n > 0 {
			if _, werr := filter.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			q.finish()
			return
		}
	}
}

// sendLoop writes the queued output to the browser, one binary frame at a
// time, until the queue is finished and empty or the connection ends.
func (c *termConn) sendLoop(ctx context.Context, q *outQueue) {
	for {
		chunk, err := q.pop(ctx)
		if err != nil {
			// io.EOF: the attach's output is over and all of it is sent. A
			// cancelled context is run tearing down. Task 6.4 asks why the
			// attach ended.
			return
		}
		if !c.sendBinary(ctx, chunk) {
			return
		}
	}
}

// outQueue is the output a browser has not yet been sent, bounded in bytes:
// push refuses a chunk that would pass the limit, and never waits.
type outQueue struct {
	limit int
	wake  chan struct{}

	mu     sync.Mutex
	chunks [][]byte
	size   int
	fin    bool
}

func newOutQueue(limit int) *outQueue {
	return &outQueue{limit: limit, wake: make(chan struct{}, 1)}
}

// push queues a copy of p, or reports false when it would pass the limit. An
// empty queue takes any chunk, so one read larger than the limit is no
// overflow.
func (q *outQueue) push(p []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.size > 0 && q.size+len(p) > q.limit {
		return false
	}
	q.chunks = append(q.chunks, append([]byte(nil), p...))
	q.size += len(p)
	q.signal()
	return true
}

// finish ends the queue: pop returns io.EOF once it is empty.
func (q *outQueue) finish() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.fin = true
	q.signal()
}

// signal wakes a waiting pop; the caller holds q.mu.
func (q *outQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// pop returns the oldest chunk, waiting for one. It returns io.EOF when the
// queue is finished and empty, and ctx's error when ctx ends first.
func (q *outQueue) pop(ctx context.Context) ([]byte, error) {
	for {
		q.mu.Lock()
		if len(q.chunks) > 0 {
			chunk := q.chunks[0]
			q.chunks = q.chunks[1:]
			q.size -= len(chunk)
			q.mu.Unlock()
			return chunk, nil
		}
		fin := q.fin
		q.mu.Unlock()
		if fin {
			return nil, io.EOF
		}
		select {
		case <-q.wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'gofmt -l internal/web/terminal_conn.go internal/web/terminal_conn_test.go && CGO_ENABLED=0 go test ./internal/web/ -run "TestTermEnds|TestTermConn|TestOutQueue"'`

Expected: `gofmt -l` prints nothing, then `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): send a terminal's history and filtered output through a bounded buffer" -- internal/web/terminal_conn.go internal/web/terminal_conn_test.go
```

### Task 6.4: Pings, the attach's own end, and every path closing the attach

The connection pings the browser every `termPingEvery` and drops one whose ping goes unanswered for `termPongWait` (the zero ending), except that no ping can fail while the reader goroutine, the only one that reads a pong, is busy opening the attach or sending its history: `attach` counts that in `busy`, and `pingLoop` sends no ping then and excuses a ping that was outstanding then. When the attach's output ends by itself, once the queue is sent, `ended` is asked once and its answer is the ending; an ending loom caused never asks it. Every ending path closes the attach, and a panic in any goroutine goes to `recoverPanic`.

**Files:**
- Modify: `internal/web/terminal_conn.go`
- Test: `internal/web/terminal_conn_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/web/terminal_conn_test.go`:

```go
func TestTermConn_ABrowserThatAnswersItsPingsStaysConnected(t *testing.T) {
	var pings atomic.Int32
	rig := startTermConn(t, termRigOpts{
		tune: func(c *termConn) {
			c.pingEvery = 20 * time.Millisecond
			c.pongWait = 500 * time.Millisecond
		},
		dial: &websocket.DialOptions{OnPingReceived: func(context.Context, []byte) bool {
			pings.Add(1)
			return true // the library answers it, while the browser reads
		}},
	})
	type closed struct {
		ce  *websocket.CloseError
		err error
	}
	done := make(chan closed, 1)
	go func() {
		_, ce, err := readUntilClose(rig.client)
		done <- closed{ce, err}
	}()

	select {
	case e := <-rig.result:
		t.Fatalf("a browser that answers its pings was dropped: %+v", e)
	case <-time.After(400 * time.Millisecond):
	}
	assert.GreaterOrEqual(t, pings.Load(), int32(3), "the connection pings every termPingEvery")

	rig.conn.end(termEndOff)
	assert.Equal(t, termEndOff, waitTermEnd(t, rig.result))
	d := <-done
	require.NoError(t, d.err)
	assert.Equal(t, websocket.StatusCode(4001), d.ce.Code)
}

func TestTermConn_APingTheBrowserNeverAnswersDropsItWithNoCloseFrame(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		tune: func(c *termConn) {
			c.pingEvery = 20 * time.Millisecond
			c.pongWait = 50 * time.Millisecond
		},
	})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)
	// From here the browser reads nothing, so the library never answers a ping.
	start := time.Now()
	assert.Equal(t, termEnd{}, waitTermEnd(t, rig.result))
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
}

func TestTermConn_NoPingFailsWhileTheAttachIsStillOpening(t *testing.T) {
	fake := newTermOpenFake()
	fake.gate = make(chan struct{})
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		tune: func(c *termConn) {
			c.pingEvery = 50 * time.Millisecond
			c.pongWait = 300 * time.Millisecond
		},
	})
	// A ping is outstanding when the attach starts to open: the first one goes
	// out at 50 ms and would fail at 350 ms, and the browser reads nothing.
	time.Sleep(100 * time.Millisecond)
	sendResize(t, rig.client, 80, 24)
	waitUntil(t, "open to be called", func() bool { return len(fake.calls()) == 1 })

	// The reader is inside open, so it reads no pong, and the browser is not
	// reading either, which answers none: far past pingEvery and pongWait.
	select {
	case e := <-rig.result:
		t.Fatalf("a ping failed while the attach was still opening: %+v", e)
	case <-time.After(900 * time.Millisecond):
	}

	close(fake.gate)
	expectLive(t, rig.client)
	rig.conn.end(termEndOff)
	end, _, _ := rig.finish(t)
	assert.Equal(t, termEndOff, end)
}

func TestTermConn_AnAttachThatEndsByItselfAsksEndedOnceAndSendsItsAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		then termEnd
		code websocket.StatusCode
	}{
		{"the session is still there", termEndExited, 4004},
		{"the session is gone", termEndSession, 4003},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newTermOpenFake()
			var asked atomic.Int32
			rig := startTermConn(t, termRigOpts{
				open: fake.open,
				ended: func() termEnd {
					asked.Add(1)
					return tc.then
				},
			})
			sendResize(t, rig.client, 80, 24)
			expectLive(t, rig.client)
			fake.shell.feed <- []byte("bye\r\n")
			close(fake.shell.feed) // the shell exits

			end, frames, ce := rig.finish(t)
			assert.Equal(t, tc.then, end)
			assert.Equal(t, tc.code, ce.Code)
			assert.Equal(t, tc.then.reason, ce.Reason)
			require.Len(t, frames, 1, "what the shell said last is sent before the ending")
			assert.Equal(t, "bye\r\n", string(frames[0].data))
			assert.Equal(t, int32(1), asked.Load())
			assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
		})
	}
}

func TestTermConn_AnEndingLoomCausesNeverAsksEnded(t *testing.T) {
	fake := newTermOpenFake()
	var asked atomic.Int32
	rig := startTermConn(t, termRigOpts{
		open:  fake.open,
		ended: func() termEnd { asked.Add(1); return termEndExited },
	})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)
	rig.conn.end(termEndRevoked)
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndRevoked, end)
	assert.Equal(t, websocket.StatusCode(4002), ce.Code)
	assert.Equal(t, int32(0), asked.Load(), "closing the attach ends its output, which is not the shell exiting")
}

func TestTermConn_EveryWayItEndsClosesTheAttach(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger func(t *testing.T, rig *termRig)
		want    termEnd
		frame   bool // the ending is a close frame
	}{
		{"end", func(t *testing.T, r *termRig) { r.conn.end(termEndRevoked) }, termEndRevoked, true},
		{"a cancelled context", func(t *testing.T, r *termRig) { r.cancel() }, termEndStopping, true},
		{"the browser leaving", func(t *testing.T, r *termRig) { _ = r.client.CloseNow() }, termEnd{}, false},
		{"a bad frame", func(t *testing.T, r *termRig) {
			sendTerm(t, r.client, websocket.MessageText, []byte("nonsense"))
		}, termEndBadFrame, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newTermOpenFake()
			rig := startTermConn(t, termRigOpts{open: fake.open})
			sendResize(t, rig.client, 80, 24)
			expectLive(t, rig.client)
			tc.trigger(t, rig)
			if tc.frame {
				end, _, _ := rig.finish(t)
				assert.Equal(t, tc.want, end)
			} else {
				assert.Equal(t, tc.want, waitTermEnd(t, rig.result))
			}
			assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
		})
	}
}

func TestTermConn_APanicInAGoroutineGoesToRecoverPanicAndEndsTheConnection(t *testing.T) {
	fake := newTermOpenFake()
	fake.shell.panicOnRead = true
	got := make(chan string, 4)
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		recoverPanic: func(where string) {
			if r := recover(); r != nil {
				got <- where
			}
		},
	})
	sendResize(t, rig.client, 80, 24)

	select {
	case where := <-got:
		assert.Equal(t, "terminal attach", where)
	case <-time.After(5 * time.Second):
		t.Fatal("the panic never reached recoverPanic")
	}
	// The connection ends with no close frame, and run returns.
	assert.Equal(t, termEnd{}, waitTermEnd(t, rig.result))
	assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
}
```

Fails without: TestTermConn_ABrowserThatAnswersItsPingsStaysConnected: the ping loop not started in `run` (the `pings` count stays 0), or a ping error that is not a timeout dropping the connection.
Fails without: TestTermConn_APingTheBrowserNeverAnswersDropsItWithNoCloseFrame: the ping loop not started (`run` never returns), or `c.pongWait` not bounding the ping (it waits forever), or the drop sending a close frame (the result is not the zero ending).
Fails without: TestTermConn_NoPingFailsWhileTheAttachIsStillOpening: the `before%2 == 1` skip in `pingLoop` removed (a ping sent while the reader is inside `open` goes unanswered and drops the connection), or the `c.busy.Load() != before` excuse removed (the ping outstanding when `open` began fails at 350 ms and drops it), or the `c.busy.Add(1)` pair removed from `attach` (neither term has anything to see).
Fails without: TestTermConn_AnAttachThatEndsByItselfAsksEndedOnceAndSendsItsAnswer: the `c.end(c.ended())` call in `sendLoop` removed (the connection never ends), `ended`'s answer replaced by a fixed ending (the second case), or the ending sent before the queued `bye` is flushed.
Fails without: TestTermConn_AnEndingLoomCausesNeverAsksEnded: the `!c.stopped()` term removed from `sendLoop`'s guard.
Fails without: TestTermConn_EveryWayItEndsClosesTheAttach: `closeShell` skipped on any path out of `run`.
Fails without: TestTermConn_APanicInAGoroutineGoesToRecoverPanicAndEndsTheConnection: `defer c.recoverPanic(where)` removed from `spawn` (the panic crashes the test binary), or the `finished` guard removed (`run` never returns, since nothing ends the connection).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestTermConn'`

Expected: the new tests FAIL: `a pings count of 0 is not >= 3`, `run did not return` (no ping loop, and `sendLoop` never ends the connection when the attach exits), and `expected: 1 actual: 0` for the asked count; the earlier tests still pass, and so does `TestTermConn_NoPingFailsWhileTheAttachIsStillOpening`, since there is no ping yet to fail (it goes red only against the mutations its `Fails without` line names).

- [ ] **Step 3: Implement**

In `internal/web/terminal_conn.go`, in `run`, replace

```go
	c.spawn("terminal reader", func() { c.readLoop(cctx) })
```

with

```go
	c.spawn("terminal reader", func() { c.readLoop(cctx) })
	c.spawn("terminal ping", func() { c.pingLoop(cctx) })
```

In `sendLoop`, replace

```go
		chunk, err := q.pop(ctx)
		if err != nil {
			// io.EOF: the attach's output is over and all of it is sent. A
			// cancelled context is run tearing down. Task 6.4 asks why the
			// attach ended.
			return
		}
```

with

```go
		chunk, err := q.pop(ctx)
		if err != nil {
			// io.EOF: the attach's output is over and all of it is sent. When
			// the connection is not already over, the attach ended by itself,
			// and the route says why (the shell exited, or the session went);
			// a cancelled context is run tearing down.
			if errors.Is(err, io.EOF) && !c.stopped() {
				c.end(c.ended())
			}
			return
		}
```

And add, after `sendLoop`:

```go
// pingLoop pings the browser every pingEvery and drops it, with no close
// frame, when a ping goes unanswered for pongWait. ctx is the connection's own
// context.
//
// The library reads a pong only while the reader goroutine is inside Reader,
// and that goroutine is busy for as long as the attach is opening and its
// history is going out (attach counts that, in c.busy, odd while it lasts):
// on a slow link that outlasts any pongWait, and a ping that failed for it
// would drop a connection that is only being set up, again on each retry. So
// a tick that finds the reader busy sends no ping, and a ping that fails is
// excused when the reader was busy at any point while it was outstanding (the
// count then differs from the one before the ping, or is odd).
func (c *termConn) pingLoop(ctx context.Context) {
	tick := time.NewTicker(c.pingEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-tick.C:
		}
		before := c.busy.Load()
		if before%2 == 1 {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, c.pongWait)
		err := c.ws.Ping(pctx)
		cancel()
		if err != nil {
			if c.busy.Load() != before {
				continue
			}
			c.leave()
			return
		}
	}
}
```

And in `attach`, count the reader as busy while the attach opens and its history goes out. Replace

```go
	if c.stopped() {
		return nil
	}
	sh, hist, err := c.open(cols, rows)
```

with

```go
	if c.stopped() {
		return nil
	}
	c.busy.Add(1)
	defer c.busy.Add(1)
	sh, hist, err := c.open(cols, rows)
```

In the `termConn` struct, replace

```go
	wg sync.WaitGroup
}
```

with

```go
	// busy counts the reader goroutine's trips into and out of attach: odd
	// while it opens the attach and sends its history, and so reads no pong.
	busy atomic.Int64

	wg sync.WaitGroup
}
```

and in the import block add `"sync/atomic"` after `"sync"`.

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'gofmt -l internal/web/terminal_conn.go internal/web/terminal_conn_test.go && CGO_ENABLED=0 go test ./internal/web/ -run "TestTermEnds|TestTermConn|TestOutQueue" && CC=clang CGO_ENABLED=1 go test -race ./internal/web/ -run "TestTermEnds|TestTermConn|TestOutQueue" && CGO_ENABLED=0 go build ./... && GOOS=windows CGO_ENABLED=0 go build ./...'`

Expected: `gofmt -l` prints nothing, each of the two test runs prints `ok  	github.com/aidan-bailey/loom/internal/web`, and the two builds print nothing. (Drop `CC=clang` where gcc exists.)

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): ping a terminal's browser, end on the attach's own exit, and close the attach on every path" -- internal/web/terminal_conn.go internal/web/terminal_conn_test.go
```

## Story 7: The route and the terminals' part

- **Developer:** intermediate-dev
- **Files:** `internal/web/terminal.go`, `internal/web/terminal_test.go`, `internal/web/routes.go`, `internal/web/routes_test.go`, `internal/web/web.go`, `internal/web/server.go`, `internal/web/api.fields`, `docs/specs/web-api.md`, `internal/web/ui/src/api/gen.ts`, `internal/web/dist/**`, `internal/web/CLAUDE.md`, `internal/web/README.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `docs/ARCHITECTURE.md`
- **Interfaces:** exposes the route and `api.terminalURL` to Stories 8 and 9; uses Story 2's `Core.EnsureTerminal` and `Reply.Terminal`, Story 3's `StartShellAttach` and `ShellHistory`, Story 1's `WebView.Terminal`, and Story 6's `termConn`
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
- **Review:** deep
- **Form:** full (an origin check and an auth boundary on a route that executes commands, and connections ended from other goroutines)

This story adds `GET /api/v1/sessions/{id}/terminal` and the part of the web layer that owns its connections. The route is one `page` entry in the table (ok 101, its doc says it is a WebSocket upgrade, not a page), so registration wraps it in `authed`; its handler (`Layer.terminal`) first refuses a request that is not a WebSocket upgrade (426: a method but GET, below HTTP/1.1, or without `Connection: upgrade` and `Upgrade: websocket`, since the mux's `GET` pattern also serves HEAD) and one whose handshake the library would refuse (400: not `Sec-WebSocket-Version: 13`, or not exactly one `Sec-WebSocket-Key` that decodes to 16 bytes), before the origin, the slot or the model, then checks for exactly one accepted `Origin` itself, since `guard` checks `Origin` only for a method but GET and HEAD, resolves the session (404 for an unknown one or a workspace terminal), **reserves a slot under the part's lock** (`terminals.reserve`: refused while the part is closed or no listener runs, while the switch is off by the part's own copy, for a device the part has not read, and past 4 slots a device or 16 in all), only then asks the model for the shell (`EnsureTerminal` through `readReply`'s wait, a refusal answered 409 with the model's text), upgrades with the library's own origin check off (handing `websocket.Accept` the writer Story 6's `recordHijack` gives in place of the response writer), and runs Story 6's `termConn`, whose `termOpen` reads `tmux.ShellHistory` and starts `tmux.StartShellAttach` and whose `ended` asks the replica whether the session is still started and active (4004 or 4003). A slot lives from the reservation to the handler's return, and every refusal releases it. The part is a `sink`: a `ModelChanged` that carries the switch off ends every slot with `termEndOff`, and a device that is gone from the list or paired again ends its slots with `termEndRevoked`; a rebind (`unbindLocked`, after the rule is cleared and before the listener closes) ends every slot with `termEndStopping`, and the layer's stop (`stopParts`) does the same, closes the part and waits for the connections to write their closing frames (`http.Server.Close` does not track a hijacked connection, so only the part can end them). A slot ended while its request waited for the model closes with that ending as soon as it has upgraded (`termSlot.attach` calls `termConn.end` before `run`, which Story 6's `end` supports). The goroutines the part starts defer the layer's `recoverPanic`, and the connection gets it as `newTermConn`'s fifth argument, after the hijacked connection `recordHijack` gives, which the connection closes outright when its close handshake outlasts `termCloseWait` (the library's close waits with no deadline on a frame the browser may never finish, and `http.Server.Close` does not track a hijacked connection). The route logs `web.terminal_open` and `web.terminal_close` (device, session, and the close's reason) through the part's logger and never the bytes. The contract is regenerated, the bundle rebuilt (the app's `api` object gains `terminalURL`), `internal/web/CLAUDE.md` frees room by moving supporting context of existing rules into `docs/claude/web-layer.md` and gains the route's rules, and the guide gains "Adding a WebSocket route". No landmine marker appears in this story's code. Run every build, test and `pnpm` command below through `loom job run -d "<what>" --` (wrap an env-prefixed command as `sh -c '…'`), as the session's rules say; the commands are what each job runs. The tests reuse Story 6's helpers in `internal/web/terminal_conn_test.go` (`termShellFake`, `termOpenFake`, `startTermConn`, `readUntilClose`, `sendResize`, `expectLive`, `readTerm`, `sendTerm`, `rawFrameHeader`) and Story 2's `Reply.Terminal`; the route tests script the model's answer by replacing the part's `ensure` seam, which hands the layer's `reqs` a `core.Reply` as the drain would.

### Task 7.1: The terminals' part

The part holds the slots, the caps, the switch's copy and the device list, and ends slots from its sink, `endAll` and `close`. It also holds `tmuxOpen`, the `termOpen` the route hands each connection. Nothing routes to it yet (Task 7.2 does).

**Files:**
- Create: `internal/web/terminal.go`
- Create: `internal/web/terminal_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/terminal_test.go`. The helpers (`newTestTerms`, `modelWith`, the log buffer and the pty fakes) serve every later task of this story.

```go
package web

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/core"
	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// termPaired is when the devices of these tests were paired.
var termPaired = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

// modelWith is the batch the drain hands the terminals' part when the model
// publishes the switch and the paired devices.
func modelWith(on bool, devices ...core.WebDevice) []core.Event {
	return []core.Event{core.ModelChanged{View: core.ModelView{Web: core.WebView{Terminal: on, Devices: devices}}}}
}

func pairedDev(name string, at time.Time) core.WebDevice {
	return core.WebDevice{Name: name, Paired: at}
}

// newTestTerms is a part on a layer that has a listener's origin rule and no
// client, so the model is never asked for anything. Its grace is short.
func newTestTerms(t *testing.T) *terminals {
	t.Helper()
	l := &Layer{}
	rule := originSet{{scheme: "http", host: "loom.test"}}
	l.rule.Store(&rule)
	tm := newTerminals(l)
	tm.grace = 100 * time.Millisecond
	return tm
}

// isOn is the part's copy of the switch.
func (tm *terminals) isOn() bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.on
}

// knows reports whether the part has read the device.
func (tm *terminals) knows(name string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	_, ok := tm.devices[name]
	return ok
}

// slotCount is how many slots are held now.
func (tm *terminals) slotCount() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return len(tm.slots)
}

// endedWith is the ending the slot was given, and whether it was given one.
func (s *termSlot) endedWith() (termEnd, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ending, s.ended
}

// logBuf is a log's destination that tests read.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// plainLog writes to buf with no timestamp, so a test compares whole lines.
func plainLog(buf *logBuf) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}}))
}

func TestTerminals_ASlotNeedsTheSwitchOnAndAKnownDevice(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(false, pairedDev("phone", termPaired)))
	_, err := tm.reserve("phone")
	assert.ErrorIs(t, err, errTermOff, "the switch is off")

	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err, "on, for a device the part has read")
	require.NotNil(t, slot)

	_, err = tm.reserve("stranger")
	var se *statusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, http.StatusServiceUnavailable, se.status, "a device the part has not read yet is asked to try again")
	assert.Contains(t, se.msg, "try again")
}

func TestTerminals_NoListenerRefusesASlot(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	_, err := tm.reserve("phone")
	require.NoError(t, err, "a listener runs")

	tm.l.rule.Store(nil)
	_, err = tm.reserve("phone")
	assert.ErrorIs(t, err, errTermsStopping, "a request that passed guard before the listener closed is not counted")
}

func TestTerminals_TheCapsCountSlotsPerDeviceAndInAll(t *testing.T) {
	tm := newTestTerms(t)
	names := []string{"a", "b", "c", "d", "e"}
	var devices []core.WebDevice
	for _, n := range names {
		devices = append(devices, pairedDev(n, termPaired))
	}
	tm.apply(modelWith(true, devices...))
	held := map[string][]*termSlot{}
	fill := func(name string) {
		for i := 0; i < maxTerminalsPerDevice; i++ {
			s, err := tm.reserve(name)
			require.NoError(t, err, "%s's slot %d", name, i+1)
			held[name] = append(held[name], s)
		}
	}

	fill("a")
	_, err := tm.reserve("a")
	assert.ErrorIs(t, err, errTermsFull, "a fifth slot for one device")
	fill("b")
	fill("c")
	fill("d")
	require.Equal(t, maxTerminals, tm.slotCount(), "fixture: four devices of four slots are 16 in all")

	_, err = tm.reserve("e")
	assert.ErrorIs(t, err, errTermsFull, "a device with none is refused at 16 in all")
	tm.release(held["d"][0])
	_, err = tm.reserve("e")
	assert.NoError(t, err, "a released slot is room for the next")
}

func TestTerminals_TheSwitchTurningOffEndsEverySlot(t *testing.T) {
	tm := newTestTerms(t)
	devices := []core.WebDevice{pairedDev("a", termPaired), pairedDev("b", termPaired)}
	tm.apply(modelWith(true, devices...))
	sa, err := tm.reserve("a")
	require.NoError(t, err)
	sb, err := tm.reserve("b")
	require.NoError(t, err)

	tm.apply(modelWith(true, devices...))
	_, endedA := sa.endedWith()
	_, endedB := sb.endedWith()
	assert.False(t, endedA || endedB, "a model change that leaves the switch on ends nothing")

	tm.apply(modelWith(false, devices...))
	for name, s := range map[string]*termSlot{"a": sa, "b": sb} {
		e, ended := s.endedWith()
		assert.True(t, ended, name)
		assert.Equal(t, termEndOff, e, name)
	}
}

func TestTerminals_ARevokedOrRepairedDeviceEndsItsSlotsOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after []core.WebDevice
	}{
		{"revoked", []core.WebDevice{pairedDev("keep", termPaired)}},
		{"paired again", []core.WebDevice{pairedDev("keep", termPaired), pairedDev("gone", termPaired.Add(time.Hour))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm := newTestTerms(t)
			tm.apply(modelWith(true, pairedDev("keep", termPaired), pairedDev("gone", termPaired)))
			gone, err := tm.reserve("gone")
			require.NoError(t, err)
			keep, err := tm.reserve("keep")
			require.NoError(t, err)

			tm.apply(modelWith(true, tc.after...))

			e, ended := gone.endedWith()
			assert.True(t, ended, "the device that is gone or paired again")
			assert.Equal(t, termEndRevoked, e)
			_, ended = keep.endedWith()
			assert.False(t, ended, "another device's slot stays")
		})
	}
}

func TestTerminals_ASlotEndedBeforeItsConnectionExistsEndsItWhenItAttaches(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)
	tm.apply(modelWith(false, pairedDev("phone", termPaired)))

	open := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: open.open, before: func(c *termConn) { slot.attach(c) }})
	end, _, ce := rig.finish(t)

	assert.Equal(t, termEndOff, end)
	assert.Equal(t, websocket.StatusCode(4001), ce.Code)
	assert.Empty(t, open.calls(), "a connection ended before it began opens no attach")
}

func TestTerminals_ASlotThatIsNotEndedLetsItsConnectionWork(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)
	tm.apply(modelWith(true, pairedDev("phone", termPaired), pairedDev("other", termPaired)))

	open := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: open.open, before: func(c *termConn) { slot.attach(c) }})
	sendResize(t, rig.client, 100, 30)
	expectLive(t, rig.client)
	assert.Equal(t, [][2]int{{100, 30}}, open.calls())
}

func TestTerminals_CloseEndsEverythingRefusesNewSlotsAndWaitsForTheHandlers(t *testing.T) {
	tm := newTestTerms(t)
	tm.grace = 5 * time.Second
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)
	rig := startTermConn(t, termRigOpts{before: func(c *termConn) { slot.attach(c) }})

	done := make(chan struct{})
	go func() {
		tm.close()
		close(done)
	}()
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndStopping, end)
	assert.Equal(t, websocket.StatusGoingAway, ce.Code)
	require.Never(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 150*time.Millisecond, 10*time.Millisecond, "close returned while a handler still held its slot")

	tm.release(slot)
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "close never returned after the last handler released")

	_, err = tm.reserve("phone")
	assert.ErrorIs(t, err, errTermsStopping, "a closed part counts nothing")
	tm.close() // a second close returns at once
}

func TestTerminals_CloseGivesUpOnAHandlerThatNeverReleases(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		tm.close()
		close(done)
	}()
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "close waited past its grace")
	e, ended := slot.endedWith()
	assert.True(t, ended)
	assert.Equal(t, termEndStopping, e)
}

func TestTerminals_EndAllLeavesThePartOpenForTheNextListener(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)

	tm.endAll(termEndStopping)

	e, ended := slot.endedWith()
	assert.True(t, ended)
	assert.Equal(t, termEndStopping, e)
	_, err = tm.reserve("phone")
	assert.NoError(t, err, "a rebind ends the old connections and the new listener takes new ones")
}

// failingPty is a pty factory that cannot make one, and keeps the commands
// it was asked to start.
type failingPty struct{ cmds []*exec.Cmd }

func (p *failingPty) Start(cmd *exec.Cmd) (*os.File, error) {
	p.cmds = append(p.cmds, cmd)
	return nil, errors.New("no pty here")
}

func (p *failingPty) Close() {}

// masterPty hands the attach the master of a real pty pair.
type masterPty struct{ f *os.File }

func (p masterPty) Start(*exec.Cmd) (*os.File, error) { return p.f, nil }
func (masterPty) Close()                              {}

func TestTerminalOpen_AFailedHistoryDoesNotStopTheAttach(t *testing.T) {
	tm := newTestTerms(t)
	buf := &logBuf{}
	tm.lg = plainLog(buf)
	tm.exec = cmd_test.MockCmdExec{OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, errors.New("tmux is not answering") }}
	ptys := &failingPty{}
	tm.ptys = ptys

	sh, hist, err := tm.tmuxOpen("loom_term_live")(120, 40)

	require.Error(t, err)
	assert.True(t, sh == nil, "a nil attach inside the interface would be a shell that is not there")
	assert.Nil(t, hist)
	require.Len(t, ptys.cmds, 1, "the attach was tried although the history was not read")
	args := ptys.cmds[0].Args
	assert.Equal(t, []string{"attach-session", "-t", "=loom_term_live"}, args[len(args)-3:], "on the exact target")
	assert.Contains(t, buf.String(), "web.terminal_history_failed")
	assert.Contains(t, buf.String(), "web.terminal_attach_failed", "the cause is logged: the connection tells the app only that it could not attach")
}

func TestTerminalOpen_ReturnsTheHistoryAndTheAttach(t *testing.T) {
	master, slave, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = slave.Close() })
	tm := newTestTerms(t)
	tm.exec = cmd_test.MockCmdExec{OutputFunc: func(c *exec.Cmd) ([]byte, error) {
		switch cmd_test.TmuxSubcommand(c.Args) {
		case "display-message":
			return []byte("2\n"), nil
		case "capture-pane":
			return []byte("one\ntwo\n"), nil
		}
		return nil, errors.New("an unexpected tmux command")
	}}
	tm.ptys = masterPty{master}

	sh, hist, err := tm.tmuxOpen("loom_term_live")(100, 30)

	require.NoError(t, err)
	require.NotNil(t, sh)
	t.Cleanup(func() { _ = sh.Close() })
	assert.Equal(t, "one\r\ntwo\r\n\x1b[0m", string(hist), "the rows above the screen, each ended by CR LF and the history closed by an SGR reset, as ShellHistory gives them")
}
```

Fails without: TestTerminals_ASlotNeedsTheSwitchOnAndAKnownDevice: the `!t.on` case of `reserve` removed (the first assertion finds no error), or the `t.devices[device]` lookup removed (the stranger gets a slot).
Fails without: TestTerminals_NoListenerRefusesASlot: `t.l.rule.Load() == nil` removed from `reserve` (a slot is granted with no listener).
Fails without: TestTerminals_TheCapsCountSlotsPerDeviceAndInAll: the `mine >= maxTerminalsPerDevice` term removed (a's fifth slot is granted), `len(t.slots) >= maxTerminals` removed (e is granted at 16), or `release` not deleting the slot (e is still refused after one is released).
Fails without: TestTerminals_TheSwitchTurningOffEndsEverySlot: the `!w.Terminal` ending removed from `apply` (nothing ends), or the ending applied to every model change (the unchanged-switch assertion finds both ended).
Fails without: TestTerminals_ARevokedOrRepairedDeviceEndsItsSlotsOnly: `setDevicesLocked` not ending a slot whose device is missing (revoked), `!paired.Equal(s.paired)` removed (paired again), or the ending applied to every slot ("another device's slot stays").
Fails without: TestTerminals_ASlotEndedBeforeItsConnectionExistsEndsItWhenItAttaches: the `if s.ended { c.end(s.ending) }` in `attach` removed (the connection waits for a resize that never comes and `finish` times out).
Fails without: TestTerminals_ASlotThatIsNotEndedLetsItsConnectionWork: `attach` ending the connection unconditionally (the resize never opens an attach).
Fails without: TestTerminals_CloseEndsEverythingRefusesNewSlotsAndWaitsForTheHandlers: `endLocked(termEndStopping)` removed from `close` (the connection is never ended), `t.closed = true` removed (the last `reserve` succeeds), or `t.handlers.Wait()` removed (close returns while a handler holds its slot).
Fails without: TestTerminals_CloseGivesUpOnAHandlerThatNeverReleases: the grace's `select` replaced by an unbounded `<-finished` (close never returns).
Fails without: TestTerminals_EndAllLeavesThePartOpenForTheNextListener: `endAll` setting `t.closed` (the last `reserve` is refused), or not ending the slots.
Fails without: TestTerminalOpen_AFailedHistoryDoesNotStopTheAttach: `tmuxOpen` returning at the history's error (no attach is tried), returning `a` (a typed nil) instead of `nil` on the attach's error (`sh == nil` is false), or not logging either cause.
Fails without: TestTerminalOpen_ReturnsTheHistoryAndTheAttach: `tmuxOpen` dropping the history (the empty string, not `"one\r\ntwo\r\n\x1b[0m"`), or discarding the attach.

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestTerminals_|TestTerminalOpen_'`

Expected: FAIL to build: `undefined: terminals`, `undefined: newTerminals`, `undefined: termSlot`, `undefined: errTermOff`, `undefined: errTermsFull`, `undefined: errTermsStopping`, `undefined: maxTerminalsPerDevice` and `undefined: maxTerminals`.

- [ ] **Step 3: Implement**

Create `internal/web/terminal.go`:

```go
package web

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
)

// The terminals' part of the web layer: the slots a terminal connection
// counts as from before the model is asked for its shell, the caps, the
// switch's copy and the device list its sink keeps, and what ends a slot.
// http.Server.Close does not track a hijacked connection, so the
// connections of a listener that closes or of a layer that stops are ended
// here and nowhere else.

const (
	// maxTerminalsPerDevice and maxTerminals cap the open connections of a
	// device and of the whole layer. They count connections, never shells.
	maxTerminalsPerDevice = 4
	maxTerminals          = 16

	// termCloseGrace bounds how long close waits for the connections to
	// write their closing frames.
	termCloseGrace = 2 * time.Second
)

// The refusals reserve gives, as the route answers them.
var (
	errTermsStopping = &statusError{http.StatusServiceUnavailable, "loom is stopping"}
	errTermOff       = &statusError{http.StatusForbidden, "the terminal is off on this host"}
	errTermsFull     = &statusError{http.StatusTooManyRequests, "too many open terminals"}
)

var (
	_ sink      = (*terminals)(nil)
	_ termShell = (*tmux.ShellAttach)(nil)
)

// termSlot is one connection's place, from the reservation to the handler's
// return. An ending the part gives it before its connection exists is kept,
// and handed to the connection when it attaches.
type termSlot struct {
	device string
	paired time.Time

	mu     sync.Mutex
	conn   *termConn
	ended  bool
	ending termEnd
}

// end gives the slot its ending, the first one winning, and passes it on to
// the connection when there is one. It never blocks.
func (s *termSlot) end(e termEnd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended, s.ending = true, e
	if s.conn != nil {
		s.conn.end(e)
	}
}

// attach gives the slot its connection, before the connection runs. An
// ending the slot already has ends the connection at once: termConn.end
// before run makes run send it and open no attach.
func (s *termSlot) attach(c *termConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = c
	if s.ended {
		c.end(s.ending)
	}
}

// terminals is the part: a sink following the switch and the devices, and
// the owner of every slot. Its mutex guards what follows it; end never
// blocks, so the sink, endAll and close hold it safely.
type terminals struct {
	l  *Layer
	lg *slog.Logger

	// ensure asks the model for a session's shell, newOpen makes the termOpen
	// of a shell by its tmux name, and exec and ptys are what tmuxOpen runs
	// tmux through. Tests replace them before any request.
	ensure  func(id core.InstanceID, req core.ReqID)
	newOpen func(name string) termOpen
	exec    internalexec.Executor
	ptys    tmux.PtyFactory

	// grace is how long close waits for the handlers.
	grace time.Duration

	// devices holds each paired device's pairing time as last published, and
	// handlers one count per slot.
	mu       sync.Mutex
	closed   bool
	on       bool
	devices  map[string]time.Time
	slots    map[*termSlot]struct{}
	handlers sync.WaitGroup
}

// newTerminals builds the part. It starts from the replica's switch and
// device list, so a request that comes before the drain's first batch is
// judged by what the model already published.
func newTerminals(l *Layer) *terminals {
	t := &terminals{
		l:       l,
		lg:      log.For("web", "part", "terminal"),
		exec:    internalexec.Default{},
		ptys:    tmux.MakePtyFactory(),
		grace:   termCloseGrace,
		devices: map[string]time.Time{},
		slots:   map[*termSlot]struct{}{},
	}
	t.newOpen = t.tmuxOpen
	if l.client != nil {
		t.ensure = l.client.EnsureTerminal
		w := l.client.Web()
		t.on = w.Terminal
		t.setDevicesLocked(w.Devices)
	}
	return t
}

// reserve counts a connection of device before the model is asked for its
// shell. It refuses (as a *statusError) while the part is closed or no
// listener runs (503), while the switch is off (403), for a device the part
// has not read (503, which the app retries) and at the caps (429). The
// caller must release the slot it gets.
func (t *terminals) reserve(device string) (*termSlot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.closed || t.l.rule.Load() == nil:
		return nil, errTermsStopping
	case !t.on:
		return nil, errTermOff
	}
	paired, ok := t.devices[device]
	if !ok {
		return nil, &statusError{http.StatusServiceUnavailable, errDeviceUnknown.Error()}
	}
	mine := 0
	for s := range t.slots {
		if s.device == device {
			mine++
		}
	}
	if mine >= maxTerminalsPerDevice || len(t.slots) >= maxTerminals {
		return nil, errTermsFull
	}
	s := &termSlot{device: device, paired: paired}
	t.slots[s] = struct{}{}
	t.handlers.Add(1)
	return s, nil
}

// release gives a slot back, once: the handler defers it.
func (t *terminals) release(s *termSlot) {
	t.mu.Lock()
	_, held := t.slots[s]
	delete(t.slots, s)
	t.mu.Unlock()
	if held {
		t.handlers.Done()
	}
}

// apply is the sink: a published model view carries the switch and the
// device list. A device that is gone, or there with another pairing time
// (revoked, then paired again under the same name), loses its slots to
// termEndRevoked; a switch that is off ends every slot with termEndOff.
func (t *terminals) apply(evs []core.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	for _, ev := range evs {
		m, ok := ev.(core.ModelChanged)
		if !ok {
			continue
		}
		w := m.View.Web
		t.on = w.Terminal
		t.setDevicesLocked(w.Devices)
		if !w.Terminal {
			t.endLocked(termEndOff)
		}
	}
}

// setDevicesLocked takes the published device list and ends the slots of a
// device that is no longer the one they began with. t.mu is held, or t is
// not shared yet.
func (t *terminals) setDevicesLocked(devices []core.WebDevice) {
	known := make(map[string]time.Time, len(devices))
	for _, d := range devices {
		known[d.Name] = d.Paired
	}
	t.devices = known
	for s := range t.slots {
		if paired, ok := known[s.device]; !ok || !paired.Equal(s.paired) {
			s.end(termEndRevoked)
		}
	}
}

// endAll ends every slot with e and leaves the part open: the layer calls it
// when the listener rebinds, since the connections it holds are the old
// listener's. It is safe on a nil part.
func (t *terminals) endAll(e termEnd) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endLocked(e)
}

// endLocked ends every slot with e. t.mu is held.
func (t *terminals) endLocked(e termEnd) {
	for s := range t.slots {
		s.end(e)
	}
}

// close refuses new slots, ends every slot as the layer stopping, and waits
// up to the grace for the handlers to return, so each connection has written
// its closing frame before the listener closes. It is safe on a nil part and
// called twice.
func (t *terminals) close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.endLocked(termEndStopping)
	t.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		defer t.l.recoverPanic("terminals")
		defer close(finished)
		t.handlers.Wait()
	}()
	select {
	case <-finished:
	case <-time.After(t.grace):
		t.lg.Warn("web.terminals_close_grace")
	}
}

// tmuxOpen is the termOpen for the tmux session called name: the history
// above the visible screen, then the attach at the browser's size. A history
// that cannot be read leaves the scrollback empty and is logged; an attach
// that cannot start is logged with its cause, which the connection does not
// tell the app, and returned.
func (t *terminals) tmuxOpen(name string) termOpen {
	return func(cols int, rows int) (termShell, []byte, error) {
		history, err := tmux.ShellHistory(name, t.exec)
		if err != nil {
			t.lg.Warn("web.terminal_history_failed", "shell", name, "err", err)
			history = nil
		}
		a, err := tmux.StartShellAttach(name, cols, rows, t.ptys)
		if err != nil {
			t.lg.Warn("web.terminal_attach_failed", "shell", name, "err", err)
			return nil, nil, err
		}
		return a, history, nil
	}
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `bash -c 'gofmt -l internal/web/terminal.go internal/web/terminal_test.go && CGO_ENABLED=0 go vet ./internal/web/ && CGO_ENABLED=0 go test ./internal/web/ -run "TestTerminals_|TestTerminalOpen_"'`

Expected: `gofmt -l` prints nothing (run `gofmt -w` on those two files if it names one), `go vet` is silent, then `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/terminal.go internal/web/terminal_test.go
git commit -m "feat(web): the terminals' part counts slots and ends them with the right ending" -- internal/web/terminal.go internal/web/terminal_test.go
```

### Task 7.2: The route

`Layer.terminal` is the handler, the table gains its entry, the layer starts the part (so the sink follows the model), and the contract is regenerated. The tests run the route over a real daemon stack (`apiEnv`) with the model's answer scripted.

**Files:**
- Modify: `internal/web/terminal.go` (the import block, and a handler appended)
- Modify: `internal/web/routes.go` (the `spec` comment, and the table's last entry)
- Modify: `internal/web/web.go` (the `Layer` struct and `startParts`)
- Modify: `internal/web/routes_test.go` (the table test)
- Regenerate: `internal/web/api.fields`, `docs/specs/web-api.md`, `internal/web/ui/src/api/gen.ts` (`go generate ./internal/web`; never edited by hand)
- Test: `internal/web/terminal_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/web/terminal_test.go`, replace the import block

```go
import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/core"
	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

with

```go
import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

then append the route tests and their helpers to the end of the file:

```go
// shellScript is the model's answer to a request for a shell, scripted: the
// tmux name it names (""  names none), an error it refuses with, and a gate
// that holds the request until release. It records which sessions were asked
// for and which shell names were opened.
type shellScript struct {
	mu    sync.Mutex
	name  string
	err   error
	gate  chan struct{}
	once  sync.Once
	asked []core.InstanceID
	named []string
}

func (s *shellScript) askedIDs() []core.InstanceID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.InstanceID(nil), s.asked...)
}

func (s *shellScript) opened() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.named...)
}

func (s *shellScript) refuseWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// hold makes the next requests wait, in the layer's send, until release.
func (s *shellScript) hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = make(chan struct{})
}

func (s *shellScript) release() {
	s.mu.Lock()
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		s.once.Do(func() { close(gate) })
	}
}

// scriptTerminals replaces the part's two seams: asking the model for a shell
// answers with a Reply as the drain would hand the layer's reqs, naming name,
// and opening the attach for that name goes through open.
func scriptTerminals(env *apiEnv, name string, open termOpen) *shellScript {
	s := &shellScript{name: name}
	terms := env.layer.terms
	terms.ensure = func(id core.InstanceID, req core.ReqID) {
		s.mu.Lock()
		s.asked = append(s.asked, id)
		gate, err, name := s.gate, s.err, s.name
		s.mu.Unlock()
		if gate != nil {
			<-gate
		}
		rep := core.Reply{Req: req, ID: id}
		if err != nil {
			rep.Err = err
		} else {
			rep.Terminal = name
		}
		env.layer.reqs.apply([]core.Event{rep})
	}
	terms.newOpen = func(n string) termOpen {
		s.mu.Lock()
		s.named = append(s.named, n)
		s.mu.Unlock()
		return open
	}
	return s
}

// newTermEnv is an apiEnv whose terminals' part has read the paired device.
// The switch is off.
func newTermEnv(t *testing.T, insts ...*session.Instance) *apiEnv {
	t.Helper()
	env := newAPIEnv(t, apiFixture{insts: insts})
	require.Eventually(t, func() bool { return env.layer.terms.knows("phone") }, 5*time.Second, 10*time.Millisecond, "the terminals' part never read the paired device")
	return env
}

// switchTerminal sets the web terminal's switch through the model, as the
// host's command does, and waits until the part has seen it.
func (e *apiEnv) switchTerminal(on bool) {
	e.t.Helper()
	require.NoError(e.t, e.connect(e.t).SetWebTerminal(on))
	require.Eventually(e.t, func() bool { return e.layer.terms.isOn() == on }, 5*time.Second, 10*time.Millisecond, "the terminals' part never saw the switch")
}

// termDialed is the outcome of a handshake: the connection when it upgraded,
// else the status and body the layer refused with.
type termDialed struct {
	conn   *websocket.Conn
	status int
	body   string
	err    error
}

// termDial makes the handshake of a session's terminal at addr as the app's
// page does: the device's cookie and the layer's origin. edit changes the
// headers first.
func termDial(addr string, cookie *http.Cookie, id core.InstanceID, edit func(h http.Header)) termDialed {
	h := http.Header{"Origin": {"http://" + addr}}
	if cookie != nil {
		h.Set("Cookie", (&http.Cookie{Name: cookie.Name, Value: cookie.Value}).String())
	}
	if edit != nil {
		edit(h)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/api/v1/sessions/%d/terminal", addr, id), &websocket.DialOptions{HTTPHeader: h})
	d := termDialed{conn: c, err: err}
	if resp != nil {
		d.status = resp.StatusCode
		if d.status != http.StatusSwitchingProtocols && resp.Body != nil {
			b, _ := io.ReadAll(resp.Body)
			d.body = string(b)
			_ = resp.Body.Close()
		}
	}
	return d
}

func (e *apiEnv) term(id core.InstanceID, edit func(h http.Header)) termDialed {
	return termDial(e.addr, e.cookie, id, edit)
}

// mustTerm opens a session's terminal and fails unless it upgraded.
func mustTerm(t *testing.T, env *apiEnv, id core.InstanceID) *websocket.Conn {
	t.Helper()
	got := env.term(id, nil)
	require.Equal(t, http.StatusSwitchingProtocols, got.status, "body: %q, err: %v", got.body, got.err)
	t.Cleanup(func() { _ = got.conn.CloseNow() })
	return got.conn
}

// requireClosed reads until the connection ends and checks it ended with a
// close frame of code and reason.
func requireClosed(t *testing.T, c *websocket.Conn, code websocket.StatusCode, reason string) {
	t.Helper()
	_, ce, err := readUntilClose(c)
	require.NoError(t, err, "the connection ended without a close frame")
	require.NotNil(t, ce)
	assert.Equal(t, code, ce.Code)
	assert.Equal(t, reason, ce.Reason)
}

// plainTerm makes a request of a session's terminal that is not a handshake,
// as a plain client would: the device's cookie, the given origin, and the
// headers edit leaves. It returns the status and the body.
func (e *apiEnv) plainTerm(method string, id core.InstanceID, origin string, edit func(h http.Header)) (int, string) {
	e.t.Helper()
	req, err := http.NewRequest(method, fmt.Sprintf("http://%s/api/v1/sessions/%d/terminal", e.addr, id), nil)
	require.NoError(e.t, err)
	req.Header.Set("Origin", origin)
	req.AddCookie(&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value})
	edit(req.Header)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(e.t, err)
	return resp.StatusCode, string(b)
}

// rawTerm writes the handshake of a session's terminal by hand on a new TCP
// connection, with proto on the request line and origin as the Origin, and
// returns the connection and the response read from it. It is for what the
// HTTP client never sends: a request below HTTP/1.1, and a frame header that
// announces more than follows. The other headers are right: the device's
// cookie, version 13 and a key of 16 bytes.
func (e *apiEnv) rawTerm(proto string, id core.InstanceID, origin string) (net.Conn, *http.Response) {
	e.t.Helper()
	nc, err := net.Dial("tcp", e.addr)
	require.NoError(e.t, err)
	e.t.Cleanup(func() { _ = nc.Close() })
	require.NoError(e.t, nc.SetDeadline(time.Now().Add(30*time.Second)))
	cookie := (&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value}).String()
	_, err = fmt.Fprintf(nc, "GET /api/v1/sessions/%d/terminal %s\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s\r\n"+
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n\r\n", id, proto, e.addr, origin, cookie)
	require.NoError(e.t, err)
	resp, err := http.ReadResponse(bufio.NewReader(nc), nil)
	require.NoError(e.t, err)
	return nc, resp
}

func TestTerminal_ARequestThatIsNotAnUpgradeIs426BeforeAnythingElse(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	const (
		notUpgrade   = "this route is a WebSocket upgrade"
		badHandshake = "this route needs WebSocket version 13 and one Sec-WebSocket-Key of 16 bytes"
		key          = "dGhlIHNhbXBsZSBub25jZQ=="
	)
	headers := func(kv ...string) func(h http.Header) {
		return func(h http.Header) {
			for i := 0; i < len(kv); i += 2 {
				h.Set(kv[i], kv[i+1])
			}
		}
	}
	// handshake sets a whole handshake's headers, then kv over them (a "" value
	// removes the header).
	handshake := func(kv ...string) func(h http.Header) {
		return func(h http.Header) {
			headers("Connection", "Upgrade", "Upgrade", "websocket", "Sec-WebSocket-Version", "13", "Sec-WebSocket-Key", key)(h)
			for i := 0; i < len(kv); i += 2 {
				if kv[i+1] == "" {
					h.Del(kv[i])
				} else {
					h.Set(kv[i], kv[i+1])
				}
			}
		}
	}
	plain := func(method string, edit func(h http.Header)) func(origin string) (int, string) {
		return func(origin string) (int, string) { return env.plainTerm(method, id, origin, edit) }
	}
	http10 := func(origin string) (int, string) {
		nc, resp := env.rawTerm("HTTP/1.0", id, origin)
		defer func() { _ = resp.Body.Close(); _ = nc.Close() }()
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(b)
	}

	for _, tc := range []struct {
		name    string
		status  int
		refusal string // "" for a HEAD, which has no body
		do      func(origin string) (int, string)
	}{
		{"a plain GET", http.StatusUpgradeRequired, notUpgrade, plain(http.MethodGet, headers())},
		{"no Upgrade header", http.StatusUpgradeRequired, notUpgrade, plain(http.MethodGet, headers("Connection", "Upgrade"))},
		{"no Connection header", http.StatusUpgradeRequired, notUpgrade, plain(http.MethodGet, headers("Upgrade", "websocket"))},
		{"another protocol", http.StatusUpgradeRequired, notUpgrade, plain(http.MethodGet, headers("Connection", "Upgrade", "Upgrade", "h2c"))},
		{"a HEAD with the upgrade headers", http.StatusUpgradeRequired, "", plain(http.MethodHead, headers("Connection", "Upgrade", "Upgrade", "websocket"))},
		{"HTTP/1.0 with the whole handshake", http.StatusUpgradeRequired, notUpgrade, http10},
		{"no Sec-WebSocket-Version", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Version", ""))},
		{"version 8", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Version", "8"))},
		{"no Sec-WebSocket-Key", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Key", ""))},
		{"two keys", http.StatusBadRequest, badHandshake, plain(http.MethodGet, func(h http.Header) { handshake()(h); h.Add("Sec-WebSocket-Key", key) })},
		{"a key that is not base64", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Key", "not base64!"))},
		{"a key of 15 bytes", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Key", "AAAAAAAAAAAAAAAAAAAA"))},
	} {
		// First with the switch off and an origin that is not loom's, which
		// the origin and the switch would each refuse 403, then with both
		// right, which only the upgrade and handshake checks can refuse before
		// the slot and the model.
		for _, st := range []struct {
			on     bool
			origin string
		}{{false, "https://evil.example"}, {true, "http://" + env.addr}} {
			env.switchTerminal(st.on)
			status, body := tc.do(st.origin)
			require.Equal(t, tc.status, status, "%s, switch %v, origin %s", tc.name, st.on, st.origin)
			if tc.refusal != "" {
				assert.Equal(t, tc.refusal, apiErrorOf(t, []byte(body)), "%s, switch %v", tc.name, st.on)
			}
		}
	}
	assert.Empty(t, script.askedIDs(), "the model was never asked for a shell")
	assert.Zero(t, env.layer.terms.slotCount(), "no slot was taken")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "terminal", result: "426 " + notUpgrade})
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "terminal", result: "400 " + badHandshake})

	mustTerm(t, env, id) // a handshake is still upgraded
}

func TestTerminal_AnOriginThatIsNotLoomsIsRefusedFirst(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	const refusal = "the request does not come from loom's origin"

	for _, on := range []bool{true, false} {
		env.switchTerminal(on)
		for _, tc := range []struct {
			name string
			edit func(h http.Header)
		}{
			{"none", func(h http.Header) { h.Del("Origin") }},
			{"another site", func(h http.Header) { h.Set("Origin", "https://evil.example") }},
			{"two", func(h http.Header) { h.Add("Origin", "http://"+env.addr) }},
			{"the origin with a path", func(h http.Header) { h.Set("Origin", "http://"+env.addr+"/x") }},
		} {
			got := env.term(id, tc.edit)
			assert.Equal(t, http.StatusForbidden, got.status, "%s, switch %v", tc.name, on)
			assert.Equal(t, refusal, apiErrorOf(t, []byte(got.body)), "%s, switch %v: the origin is checked before the switch", tc.name, on)
		}
	}
	assert.Empty(t, script.askedIDs(), "the model was never asked for a shell")
	assert.Zero(t, env.layer.terms.slotCount())

	env.switchTerminal(true)
	mustTerm(t, env, id) // the layer's own origin is accepted
}

func TestTerminal_TheSwitchOffIsRefused403(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)

	got := env.term(id, nil)
	assert.Equal(t, http.StatusForbidden, got.status)
	assert.Equal(t, "the terminal is off on this host", apiErrorOf(t, []byte(got.body)))
	assert.Empty(t, script.askedIDs(), "the model was not asked")
	assert.Zero(t, env.layer.terms.slotCount())
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "terminal", session: id, result: "403 the terminal is off on this host"})

	env.switchTerminal(true)
	mustTerm(t, env, id)
}

func TestTerminal_AnUnknownSessionOrAWorkspaceTerminalIs404(t *testing.T) {
	term, err := session.NewInstance(session.InstanceOptions{Title: "term", Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	env := newTermEnv(t, term, apiInstance(t, "live", session.Running))
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)

	for name, id := range map[string]core.InstanceID{"a workspace terminal": env.ids["term"], "an unknown session": 999999} {
		got := env.term(id, nil)
		assert.Equal(t, http.StatusNotFound, got.status, name)
		assert.Equal(t, "no such session", apiErrorOf(t, []byte(got.body)), name)
	}
	assert.Empty(t, script.askedIDs(), "the model was not asked")
	assert.Zero(t, env.layer.terms.slotCount())
	mustTerm(t, env, env.ids["live"]) // the session the app lists is served
}

func TestTerminal_TheModelsRefusalIs409AndFreesTheSlot(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	script.refuseWith(errors.New("the session is paused"))

	for i := 0; i <= maxTerminalsPerDevice; i++ {
		got := env.term(id, nil)
		require.Equal(t, http.StatusConflict, got.status, "refusal %d: body %q", i+1, got.body)
		assert.Equal(t, "the session is paused", apiErrorOf(t, []byte(got.body)), "the model's own text")
		require.Zero(t, env.layer.terms.slotCount(), "a refusal releases its slot")
	}
	assert.Len(t, script.askedIDs(), maxTerminalsPerDevice+1, "every refusal came from the model, none from the cap")

	script.refuseWith(nil)
	mustTerm(t, env, id)
}

func TestTerminal_AnInstanceTheModelHasNoShellForIs409(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "fresh", session.Running))
	id := env.ids["fresh"]
	env.switchTerminal(true)

	got := env.term(id, nil)

	assert.Equal(t, http.StatusConflict, got.status, "the model refuses an instance that was never started: body %q", got.body)
	assert.NotEmpty(t, apiErrorOf(t, []byte(got.body)))
	assert.Zero(t, env.layer.terms.slotCount())
}

func TestTerminal_AReplyWithoutAShellIs500(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	scriptTerminals(env, "", newTermOpenFake().open)
	env.switchTerminal(true)

	got := env.term(id, nil)

	assert.Equal(t, http.StatusInternalServerError, got.status)
	assert.Equal(t, "loom's reply named no shell", apiErrorOf(t, []byte(got.body)))
	assert.Zero(t, env.layer.terms.slotCount())
}

func TestTerminal_PastTheLimitIs429BeforeTheModelIsAsked(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)

	var conns []*websocket.Conn
	for i := 0; i < maxTerminalsPerDevice; i++ {
		conns = append(conns, mustTerm(t, env, id))
	}
	got := env.term(id, nil)
	assert.Equal(t, http.StatusTooManyRequests, got.status)
	assert.Equal(t, "too many open terminals", apiErrorOf(t, []byte(got.body)))
	assert.Len(t, script.askedIDs(), maxTerminalsPerDevice, "the model was not asked for the refused one")

	require.NoError(t, conns[0].Close(websocket.StatusNormalClosure, ""))
	require.Eventually(t, func() bool { return env.layer.terms.slotCount() == maxTerminalsPerDevice-1 }, 5*time.Second, 10*time.Millisecond, "a connection that left never gave its slot back")
	mustTerm(t, env, id)
}

func TestTerminal_BytesGoBothWaysToTheShellTheModelNamed(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	open := newTermOpenFake()
	script := scriptTerminals(env, "loom_term_live", open.open)
	env.switchTerminal(true)

	c := mustTerm(t, env, id)
	sendResize(t, c, 120, 40)
	expectLive(t, c)
	assert.Equal(t, []core.InstanceID{id}, script.askedIDs(), "the model was asked for this session's shell")
	assert.Equal(t, []string{"loom_term_live"}, script.opened(), "the attach is on the name the model gave")
	assert.Equal(t, [][2]int{{120, 40}}, open.calls(), "at the browser's size")

	sendTerm(t, c, websocket.MessageBinary, []byte("echo hi\r"))
	require.Eventually(t, func() bool { return open.shell.input() == "echo hi\r" }, 5*time.Second, 10*time.Millisecond, "the browser's bytes never reached the shell")
	open.shell.feed <- []byte("hi\r\n")
	typ, b := readTerm(t, c)
	assert.Equal(t, websocket.MessageBinary, typ)
	assert.Contains(t, string(b), "hi")
}

func TestTerminal_OpenAndCloseAreLoggedWithoutTheBytes(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	open := newTermOpenFake()
	scriptTerminals(env, "loom_term_live", open.open)
	buf := &logBuf{}
	env.layer.terms.lg = plainLog(buf)
	env.switchTerminal(true)

	c := mustTerm(t, env, id)
	sendResize(t, c, 80, 24)
	expectLive(t, c)
	sendTerm(t, c, websocket.MessageBinary, []byte("hunter2\r"))
	open.shell.feed <- []byte("a secret in the output\r\n")
	readTerm(t, c)
	require.NoError(t, c.Close(websocket.StatusNormalClosure, ""))
	require.Eventually(t, func() bool { return env.layer.terms.slotCount() == 0 }, 5*time.Second, 10*time.Millisecond)

	want := fmt.Sprintf("level=INFO msg=web.terminal_open device=phone session=%d\nlevel=INFO msg=web.terminal_close device=phone session=%d reason=\"the browser left\"\n", id, id)
	assert.Equal(t, want, buf.String(), "each open and close, with the device, the session and the reason, and nothing else")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "terminal", session: id, result: "101 ok"})
}

func TestTerminal_ASlotEndedWhileTheModelWorksClosesWhenItUpgrades(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	open := newTermOpenFake()
	script := scriptTerminals(env, "loom_term_live", open.open)
	env.switchTerminal(true)
	script.hold()

	dialed := make(chan termDialed, 1)
	go func() { dialed <- env.term(id, nil) }()
	require.Eventually(t, func() bool {
		return env.layer.terms.slotCount() == 1 && len(script.askedIDs()) == 1
	}, 5*time.Second, 10*time.Millisecond, "the request never reached the model")

	env.switchTerminal(false)
	script.release()

	var got termDialed
	select {
	case got = <-dialed:
	case <-time.After(10 * time.Second):
		t.Fatal("the request never answered")
	}
	require.Equal(t, http.StatusSwitchingProtocols, got.status, "body %q, err %v", got.body, got.err)
	t.Cleanup(func() { _ = got.conn.CloseNow() })
	requireClosed(t, got.conn, 4001, "the terminal is off on this host")
	assert.Empty(t, open.calls(), "the ended connection opened no attach")
}

func TestTerminal_TheAttachEndingAsksTheReplica(t *testing.T) {
	for _, tc := range []struct {
		name   string
		build  func(t *testing.T) *session.Instance
		active bool
		want   termEnd
	}{
		{"started and active", func(t *testing.T) *session.Instance { inst, _ := apiWithWorktree(t, "live"); return inst }, true, termEndExited},
		{"never started", func(t *testing.T) *session.Instance { return apiInstance(t, "live", session.Running) }, false, termEndSession},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTermEnv(t, tc.build(t))
			id := env.ids["live"]
			require.Equal(t, tc.active, env.view(id).Active(), "fixture")
			open := newTermOpenFake()
			scriptTerminals(env, "loom_term_live", open.open)
			env.switchTerminal(true)

			c := mustTerm(t, env, id)
			sendResize(t, c, 80, 24)
			expectLive(t, c)
			close(open.shell.feed) // the attach ends by itself, as a shell's exit or a killed session does

			requireClosed(t, c, tc.want.code, tc.want.reason)
		})
	}
}

func TestTerminal_APanicInTheAttachStopsTheLayer(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	open := newTermOpenFake()
	open.shell.panicOnRead = true
	scriptTerminals(env, "loom_term_live", open.open)
	env.switchTerminal(true)

	c := mustTerm(t, env, id)
	sendResize(t, c, 80, 24) // opens the attach, whose output goroutine panics
	select {
	case <-env.layer.done:
	case <-time.After(10 * time.Second):
		t.Fatal("a panic in the connection's goroutine did not stop the layer: it was not recovered through the layer's recoverPanic")
	}
}

func TestTerminal_ABrowserThatStallsMidFrameCannotHoldItsSlot(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	nc, resp := env.rawTerm("HTTP/1.1", id, "http://"+env.addr)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	require.Equal(t, 1, env.layer.terms.slotCount())

	// Part of a frame that announces 1000 bytes: the connection's reader is
	// inside it, where the library's close handshake would wait for the rest
	// with no deadline. The browser then stalls, and the switch turns off.
	_, err := nc.Write(append(rawFrameHeader(2, 1000), bytes.Repeat([]byte("a"), 10)...))
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	env.switchTerminal(false)

	// The connection closes the hijacked socket outright termCloseWait after
	// its close frame, so the test takes about that long.
	require.Eventually(t, func() bool { return env.layer.terms.slotCount() == 0 }, termCloseWait+5*time.Second, 50*time.Millisecond,
		"the slot was never given back: the hijacked connection was not closed outright termCloseWait after the close frame")
}
```

Fails without: TestTerminal_ARequestThatIsNotAnUpgradeIs426BeforeAnythingElse: the `isWebSocketUpgrade` check removed from `terminal` (the second state of each 426 row reaches the model, `askedIDs` is not empty, and the library answers its own 426 only after the slot is taken), moved after the origin check or after `reserve` (the first state answers the origin's or the switch's 403), the method term dropped (the HEAD row reaches the model), the `Upgrade: websocket` term dropped (the "another protocol" row does), the `Connection` term dropped (the "no Connection header" row does), or the `r.ProtoAtLeast(1, 1)` term dropped (the HTTP/1.0 row answers 403 in its first state and reaches the model in its second, where the library's own 426 text answers); the `validHandshake` check removed, or moved after the origin check or after `reserve` (every 400 row reaches the model in the second state or answers 403 in the first, and `askedIDs` is not empty), the version term dropped (the two version rows), the `len(keys) != 1` term dropped (the "no Sec-WebSocket-Key" and "two keys" rows), or the decoding or the 16-byte length dropped (the "not base64" and "15 bytes" rows).
Fails without: TestTerminal_AnOriginThatIsNotLoomsIsRefusedFirst: the `len(origins) != 1` term of the origin check changed to `len(origins) == 0` (the "two" case passes the check), `rule.allowsOrigin` replaced by the host check (the "another site" and "with a path" cases pass), or the check moved after the switch's (the switch-off rows answer the switch's text).
Fails without: TestTerminal_TheSwitchOffIsRefused403: `reserve` not checking `t.on` (the first request is upgraded), or the refusal not going through `l.refuse` (the log assertion).
Fails without: TestTerminal_AnUnknownSessionOrAWorkspaceTerminalIs404: `l.readSession` replaced by `l.sessionFromPath` (the workspace terminal gets a shell), or the session resolved after `reserve` (the model is asked for the unknown one).
Fails without: TestTerminal_TheModelsRefusalIs409AndFreesTheSlot: the `defer t.release(slot)` removed (the fifth refusal is a 429 and `slotCount` stays above zero), or `errStatus` replaced by a fixed status (the status or the text).
Fails without: TestTerminal_AnInstanceTheModelHasNoShellForIs409: the handler answering the model's refusal with any status but 409, or ignoring `reply.Err`.
Fails without: TestTerminal_AReplyWithoutAShellIs500: the `reply.Terminal == ""` check removed (the attach would be opened on the name "").
Fails without: TestTerminal_PastTheLimitIs429BeforeTheModelIsAsked: `reserve` moved after the model's wait (`askedIDs` counts five), or the per-device cap removed (the fifth upgrades).
Fails without: TestTerminal_BytesGoBothWaysToTheShellTheModelNamed: `t.newOpen(reply.Terminal)` given another name, `newTermConn` given a different `termOpen`, or the session id sent to `ensure` not the path's.
Fails without: TestTerminal_OpenAndCloseAreLoggedWithoutTheBytes: either log line removed or given another key, an attribute added to either (the whole-log comparison), or the `l.record(a, http.StatusSwitchingProtocols, "ok")` removed.
Fails without: TestTerminal_ASlotEndedWhileTheModelWorksClosesWhenItUpgrades: `slot.attach(conn)` removed or run after `conn.run` (the connection is never told, so it opens an attach and `requireClosed` times out), or `reserve` called after the model's wait (the switch turning off ends no slot).
Fails without: TestTerminal_TheAttachEndingAsksTheReplica: `shellEnded` answering `termEndExited` for every session (the "never started" row), `termEndSession` for every one (the "started and active" row), or not reading the replica's `Active()`.
Fails without: TestTerminal_APanicInTheAttachStopsTheLayer: `l.recoverPanic` replaced by a no-op in `newTermConn`'s fifth argument (the layer keeps running and the wait times out).
Fails without: TestTerminal_ABrowserThatStallsMidFrameCannotHoldItsSlot: `hijacked()` replaced by `nil` in `newTermConn`'s second argument, or `websocket.Accept` handed `w` instead of the writer `recordHijack` gives (the connection has no socket to close, the library's close waits on the half frame, and the slot count stays 1 past the bound).

In `internal/web/routes_test.go`, in `TestRoutes_TheTableHoldsExactlyTheRoutesOfTheAPI`, replace the line

```go
		"GET /api/v1/sessions/{id}/mockup mockup",
```

with

```go
		"GET /api/v1/sessions/{id}/mockup mockup",
		"GET /api/v1/sessions/{id}/terminal terminal",
```

and before `func TestRoutes_TheOpenEntryIsServedToARequestWithNoCookie(t *testing.T) {` add

```go
func TestRoutes_TheTerminalIsAPageEntryForAWebSocket(t *testing.T) {
	rt := routeOf(t, &Layer{}, http.MethodGet, "/api/v1/sessions/{id}/terminal")
	assert.True(t, rt.page, "the generated client builds its URL and no more: the app opens the WebSocket itself")
	assert.Equal(t, "terminal", rt.spec.client)
	assert.Equal(t, "terminal", rt.spec.log)
	assert.Equal(t, http.StatusSwitchingProtocols, rt.spec.ok)
	assert.False(t, rt.spec.open, "a terminal is for a paired device")
	assert.Contains(t, rt.spec.doc, "WebSocket upgrade", "the generated reference prints every page entry as an iframe's HTML: the doc must say what this one is")
	for _, status := range []string{"426", "403", "404", "429", "503", "409", "504", "500"} {
		assert.Contains(t, rt.spec.doc, status, "the doc lists every answer the handler gives before the upgrade")
	}
	assert.Contains(t, rt.spec.doc, "400 for a handshake", "the doc lists the 400 (the status alone is contained in the close codes 4001 to 4007)")
	assert.Nil(t, rt.resp)
	assert.Nil(t, rt.query)
	assert.Nil(t, rt.body)
}
```

Fails without: TestRoutes_TheTableHoldsExactlyTheRoutesOfTheAPI: the table's `terminal` entry removed (the want list has a route the table lacks).
Fails without: TestRoutes_TheTerminalIsAPageEntryForAWebSocket: the entry declared with `typed(...)` (`rt.page` false), another `ok`, `open: true`, a doc that does not say it is a WebSocket upgrade, or a doc that leaves out one of the statuses the handler answers with (the 400 by its phrase `400 for a handshake`).

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestTerminal_|TestRoutes_TheTable|TestRoutes_TheTerminal'`

Expected: FAIL to build: `env.layer.terms undefined (type *Layer has no field or method terms)`, and once that is declared, `no route of the table has this client name` / `the table has no such route: GET /api/v1/sessions/{id}/terminal`.

- [ ] **Step 3: Implement**

In `internal/web/terminal.go`, replace the import block

```go
import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
)
```

with

```go
import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/coder/websocket"
)
```

and append to the end of the file:

```go
// terminal answers GET /api/v1/sessions/{id}/terminal: a WebSocket to the
// session's worktree shell. It runs behind authed, and guard does not check
// the Origin of a GET, so the route checks it itself: exactly one Origin
// header that the layer's origin rule accepts, which is why the library's own
// origin check is off. Everything that can be refused is refused before the
// upgrade, as JSON, and first of all a request that is not a WebSocket upgrade
// (426: not a GET, below HTTP/1.1, or without the Connection and Upgrade
// headers) or whose handshake Accept would refuse (400: not version 13, or not
// exactly one Sec-WebSocket-Key of 16 bytes), each before the origin, the slot
// and the model: then the origin (403), the session (404), the slot (the
// switch off 403, the caps 429, a stopping layer or an unread device 503) and
// the model (409 with its words, 504 when it did not answer, 500 when its
// reply named no shell). The slot is reserved before the model is asked for the
// shell, and released when this handler returns. After Accept the connection
// is the library's: its endings are close frames (termEnd), and the bytes are
// never logged.
func (l *Layer) terminal(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "terminal"}
	if !isWebSocketUpgrade(r) {
		w.Header().Set("Upgrade", "websocket")
		l.refuse(w, a, http.StatusUpgradeRequired, "this route is a WebSocket upgrade")
		return
	}
	if !validHandshake(r) {
		w.Header().Set("Sec-WebSocket-Version", "13")
		l.refuse(w, a, http.StatusBadRequest, "this route needs WebSocket version 13 and one Sec-WebSocket-Key of 16 bytes")
		return
	}
	rule := l.rule.Load()
	if origins := r.Header.Values("Origin"); rule == nil || len(origins) != 1 || !rule.allowsOrigin(origins[0]) {
		l.refuse(w, a, http.StatusForbidden, "the request does not come from loom's origin")
		return
	}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	t := l.terms
	slot, err := t.reserve(device)
	if err != nil {
		status, msg := errStatus(err)
		l.refuse(w, a, status, msg)
		return
	}
	defer t.release(slot)
	reply, err := l.readReply(r.Context(), device, func(req core.ReqID) { t.ensure(id, req) })
	if err != nil {
		status, msg := errStatus(err)
		l.refuse(w, a, status, msg)
		return
	}
	if reply.Terminal == "" {
		l.refuse(w, a, http.StatusInternalServerError, "loom's reply named no shell")
		return
	}
	// Accept is handed the writer recordHijack gives, which keeps the
	// connection Accept hijacks: the connection closes it outright when its
	// close handshake outlasts termCloseWait, since the library's close waits
	// with no deadline on a frame the browser may never finish and
	// http.Server.Close does not track a hijacked connection.
	rw, hijacked := recordHijack(w)
	ws, err := websocket.Accept(rw, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		// The handshake was checked above and the origin check is off, so the
		// one failure left to Accept is a connection it cannot hijack, which
		// it has answered 500 itself.
		l.record(a, http.StatusInternalServerError, "the connection could not be hijacked: "+err.Error())
		return
	}
	conn := newTermConn(ws, hijacked(), t.newOpen(reply.Terminal), l.shellEnded(id), l.recoverPanic)
	slot.attach(conn)
	l.record(a, http.StatusSwitchingProtocols, "ok")
	t.lg.Info("web.terminal_open", "device", device, "session", uint64(id))
	// The request's context is not used after Accept: the part ends the
	// connection, and run returns when it ends.
	end := conn.run(context.Background())
	t.lg.Info("web.terminal_close", "device", device, "session", uint64(id), "reason", closeReason(end))
}

// isWebSocketUpgrade reports whether r asks for a WebSocket upgrade: a GET
// (the mux's GET pattern also serves HEAD) at HTTP/1.1 or above, whose
// Connection header lists upgrade and whose Upgrade header lists websocket,
// each as a token and in any case.
func isWebSocketUpgrade(r *http.Request) bool {
	return r.Method == http.MethodGet && r.ProtoAtLeast(1, 1) &&
		headerHasToken(r.Header, "Connection", "upgrade") && headerHasToken(r.Header, "Upgrade", "websocket")
}

// validHandshake reports whether r's handshake is one websocket.Accept takes:
// Sec-WebSocket-Version 13 and exactly one Sec-WebSocket-Key that decodes to
// 16 bytes. The route asks before the origin, the slot and the model, so
// Accept's own 400 never answers after the model has started a shell.
func validHandshake(r *http.Request) bool {
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return false
	}
	keys := r.Header.Values("Sec-WebSocket-Key")
	if len(keys) != 1 {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keys[0]))
	return err == nil && len(raw) == 16
}

// headerHasToken reports whether any value of the header key lists token, in
// a comma-separated list, ignoring case.
func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// closeReason is the reason a close is logged with: the ending's own, or that
// the browser left when loom sent no close frame (the zero termEnd).
func closeReason(e termEnd) string {
	if e == (termEnd{}) {
		return "the browser left"
	}
	return e.reason
}

// shellEnded is the connection's `ended` for session id, asked once when the
// attach's output ends by itself: a session that is still started and active
// (the replica's Active) lost only its shell, which exited; any other was
// paused or killed. It reads the replica and is cheap.
func (l *Layer) shellEnded(id core.InstanceID) func() termEnd {
	return func() termEnd {
		if v, ok := l.client.View(id); ok && v.Active() {
			return termEndExited
		}
		return termEndSession
	}
}
```

In `internal/web/routes.go`, replace the `spec` struct's comment line

```go
	ok     int    // the success status: 200, 201 or 204
```

with

```go
	ok     int    // the success status: 200, 201 or 204, or 101 for a WebSocket upgrade (a page entry)
```

and replace the table's last entry

```go
		page(l, spec{
			method: http.MethodGet, path: "/api/v1/sessions/{id}/mockup", log: "mockup", client: "mockup", ok: http.StatusOK,
			doc: "The page the session's brainstorm companion serves now, as HTML under `mockupCSP` with `Cache-Control: no-store`, for the app's iframe. A refusal (409: no companion, or a page refused) or a timeout (504) is a small HTML page that says why, under the same policy.",
		}, l.readMockup),
	}
}
```

with

```go
		page(l, spec{
			method: http.MethodGet, path: "/api/v1/sessions/{id}/mockup", log: "mockup", client: "mockup", ok: http.StatusOK,
			doc: "The page the session's brainstorm companion serves now, as HTML under `mockupCSP` with `Cache-Control: no-store`, for the app's iframe. A refusal (409: no companion, or a page refused) or a timeout (504) is a small HTML page that says why, under the same policy.",
		}, l.readMockup),
		page(l, spec{
			method: http.MethodGet, path: "/api/v1/sessions/{id}/terminal", log: "terminal", client: "terminal", ok: http.StatusSwitchingProtocols,
			doc: "A WebSocket upgrade, not a page, to the session's worktree shell, which the model starts when it is not there: binary frames carry terminal bytes both ways, the app's first text frame `{\"resize\":{\"cols\":n,\"rows\":n}}` opens the attach and loom answers `{\"state\":\"live\"}`. Refused before the upgrade, as JSON: 426 for a request that is not a WebSocket upgrade (a method but GET, below HTTP/1.1, or without the `Connection` and `Upgrade` headers), 400 for a handshake without version 13 or without exactly one 16-byte `Sec-WebSocket-Key`, 403 without exactly one accepted `Origin` or while the web terminal is off, 404 for no such session, 429 past 4 terminals a device or 16 in all, 503 while loom is stopping, before it has read the device or when the daemon cannot take the request, 409 with the model's words, 504 when loom did not answer in time and 500 when its reply named no shell. An ending is a close frame: 4001 off, 4002 unpaired, 4003 session paused or killed, 4004 shell exited, 4005 browser fell behind, 4006 bad frame, 4007 no attach, 1001 loom stopping.",
		}, l.terminal),
	}
}
```

In `internal/web/web.go`, replace

```go
	push *pusher

	// onAction, when set,
```

with

```go
	push *pusher

	// terms is the terminals' part: the slots and connections of the web
	// terminal (terminal.go).
	terms *terminals

	// onAction, when set,
```

and, in `startParts`, replace

```go
	l.push = newPusher(l)
	l.addSink(l.push)
}
```

with

```go
	l.push = newPusher(l)
	l.addSink(l.push)
	l.terms = newTerminals(l)
	l.addSink(l.terms)
}
```

Then regenerate the contract: `go generate ./internal/web`. It appends `GET /api/v1/sessions/{id}/terminal ok=101 page` to `internal/web/api.fields`, adds `terminalURL(id: number): string;` after `mockupURL` in `Api` and ``terminalURL: (id) => `/api/v1/sessions/${id}/terminal`,`` after the `mockupURL` line of its implementation in `internal/web/ui/src/api/gen.ts`, and a `### \`GET /api/v1/sessions/{id}/terminal\`` section (the doc above, `- Auth: the device cookie`, and the generator's `- Page:` line) after the mockup's in `docs/specs/web-api.md`. Check with `git diff --stat -- internal/web/api.fields docs/specs/web-api.md internal/web/ui/src/api/gen.ts`: three files, additions only. The generator's `- Page: HTML for the app's iframe` line is worded for the mockup; the terminal's doc, printed above it, says what the route is.

- [ ] **Step 4: Run them to see them pass**

Run: `bash -c 'gofmt -l internal/web/ && CGO_ENABLED=0 go vet ./internal/web/... && CGO_ENABLED=0 go test ./internal/web/...'`

Expected: `gofmt -l` prints nothing, `go vet` is silent, and the package passes, `TestContract_GeneratedFilesAreFresh` and `TestContract_FieldsOnlyGrow` included: `ok  	github.com/aidan-bailey/loom/internal/web` and `ok  	github.com/aidan-bailey/loom/internal/web/internal/gen`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the terminal route reserves a slot, asks the model for the shell and upgrades" -- internal/web/terminal.go internal/web/terminal_test.go internal/web/routes.go internal/web/routes_test.go internal/web/web.go internal/web/api.fields docs/specs/web-api.md internal/web/ui/src/api/gen.ts
```

### Task 7.3: A rebind and the layer's stop end the connections

The sink already ends slots for the switch and for a device (Task 7.1); this task wires the other two endings: the listener's rebind (`unbindLocked`) and the layer's stop (`stopParts`). The tests run the switch and revocation through the route too, since only the route shows a close frame reaching a browser.

**Files:**
- Modify: `internal/web/server.go` (`unbindLocked`)
- Modify: `internal/web/web.go` (`stopParts`)
- Test: `internal/web/terminal_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/web/terminal_test.go`:

```go
func TestTerminal_TheSwitchTurningOffEndsAnOpenConnection(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	c := mustTerm(t, env, env.ids["live"])

	env.switchTerminal(false)

	requireClosed(t, c, 4001, "the terminal is off on this host")
}

func TestTerminal_ARevokedDeviceEndsItsConnection(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	c := mustTerm(t, env, env.ids["live"])

	require.NoError(t, env.connect(t).RevokeDevice("phone"))

	requireClosed(t, c, 4002, "this device is no longer paired")
}

func TestTerminal_ARebindEndsTheConnectionsAndTheNextListenerTakesNew(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	c := mustTerm(t, env, id)

	newAddr := freeLoopbackAddr(t)
	require.NoError(t, env.connect(t).ConfigureWeb(newAddr, "http://"+newAddr))

	requireClosed(t, c, 1001, "loom is stopping")
	env.waitWeb(t, func(v core.WebView) bool { return v.Serving && v.Addr == newAddr })
	got := termDial(newAddr, env.cookie, id, nil)
	require.Equal(t, http.StatusSwitchingProtocols, got.status, "the part stays open for the next listener: body %q, err %v", got.body, got.err)
	t.Cleanup(func() { _ = got.conn.CloseNow() })
}

func TestTerminal_TheLayersStopEndsTheConnections(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.layer.terms.grace = 30 * time.Second // longer than the test: only the handlers' return can end close's wait
	env.switchTerminal(true)
	c := mustTerm(t, env, env.ids["live"])

	// The connection's close handshake finishes only while the browser reads,
	// so the browser holds off and the layer stops on a goroutine.
	stopped := make(chan struct{})
	go func() {
		env.layer.Close()
		close(stopped)
	}()

	require.Eventually(t, func() bool {
		slot, err := env.layer.terms.reserve("phone")
		if err == nil {
			env.layer.terms.release(slot)
		}
		return err == errTermsStopping
	}, 5*time.Second, 5*time.Millisecond, "the part never refused a slot while the layer stopped")
	assert.NotNil(t, env.layer.rule.Load(), "the listener's rule is still set: the refusal is the part's own")
	select {
	case <-stopped:
		t.Fatal("the layer's stop returned before the connection wrote its closing frame: close did not wait for the handlers")
	case <-time.After(300 * time.Millisecond):
	}
	assert.Equal(t, 1, env.layer.terms.slotCount(), "the handler still holds its slot")

	requireClosed(t, c, 1001, "loom is stopping") // the browser reads, and the handshake finishes
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the layer's stop never returned")
	}
	assert.Zero(t, env.layer.terms.slotCount(), "close waited for the handler to give its slot back")
}
```

Fails without: TestTerminal_TheSwitchTurningOffEndsAnOpenConnection: the sink not registered in `startParts` (the connection stays open and `requireClosed` times out), or the `!w.Terminal` ending removed from `apply`.
Fails without: TestTerminal_ARevokedDeviceEndsItsConnection: `setDevicesLocked` not ending the slots of a device that left the list.
Fails without: TestTerminal_ARebindEndsTheConnectionsAndTheNextListenerTakesNew: `l.terms.endAll(termEndStopping)` removed from `unbindLocked` (the connection survives the old listener, a hijacked connection `http.Server.Close` does not track, and `requireClosed` times out), or `endAll` replaced by `close` (the next listener's request is refused 503).
Fails without: TestTerminal_TheLayersStopEndsTheConnections: `defer l.terms.close()` removed from `stopParts` (the test fails at once, not after the `Eventually`'s 5 s: `shutListener` clears the rule, so `reserve` still gives `errTermsStopping` within milliseconds, but the rule assertion finds the rule cleared and the early-return check finds `Layer.Close` already returned while the browser has not read), `close` not waiting for the handlers (`Layer.Close` returns within the 300 ms the browser holds off, and the slot count is still 1), `closed` not set by `close` (the part keeps reserving while the rule is set, and the `Eventually` times out), or `close` not ending the slots (the browser never gets a close frame).

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestTerminal_TheSwitchTurningOff|TestTerminal_ARevoked|TestTerminal_ARebind|TestTerminal_TheLayersStop'`

Expected: `TestTerminal_ARebindEndsTheConnectionsAndTheNextListenerTakesNew` FAILs after the read's 15 s: `the connection ended without a close frame` (the old listener's closing leaves the hijacked connection open), and `TestTerminal_TheLayersStopEndsTheConnections` FAILs at once, not after 5 s (`shutListener` clears the rule, so `reserve` gives `errTermsStopping` within milliseconds even though nothing closes the part): on `the listener's rule is still set: the refusal is the part's own`, and on `the layer's stop returned before the connection wrote its closing frame: close did not wait for the handlers`; the switch and revocation tests already pass, since the sink ends their slots.

- [ ] **Step 3: Implement**

In `internal/web/server.go`, replace

```go
// unbindLocked closes the running listener and every connection on it. The
// rule goes first, so a request on a connection about to close is refused.
// l.bindMu is held.
func (l *Layer) unbindLocked() {
	l.rule.Store(nil)
	if l.srv != nil {
```

with

```go
// unbindLocked closes the running listener and every connection on it. The
// rule goes first, so a request on a connection about to close is refused
// (and a terminal slot is no longer reserved: reserve reads the rule), then
// the terminals' part ends every slot as the listener stopping, since
// http.Server.Close does not track a hijacked connection. l.bindMu is held;
// endAll never blocks.
func (l *Layer) unbindLocked() {
	l.rule.Store(nil)
	l.terms.endAll(termEndStopping)
	if l.srv != nil {
```

In `internal/web/web.go`, replace

```go
	// Deferred, so a panic in an earlier part's close cannot leave the sender
	// pushing after the layer stopped.
	defer l.push.close()
	l.hub.close()
}
```

with

```go
	// Deferred, so a panic in an earlier part's close cannot leave the sender
	// pushing after the layer stopped. The terminals' part closes after the
	// streams hear their bye and before the listener closes: it ends every
	// connection as loom stopping and waits for their closing frames.
	defer l.push.close()
	defer l.terms.close()
	l.hub.close()
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `bash -c 'gofmt -l internal/web/ && CGO_ENABLED=0 go vet ./internal/web/... && CGO_ENABLED=0 go test ./internal/web/... && CC=clang CGO_ENABLED=1 go test -race ./internal/web/ -run "TestTerminal|TestTerminals|TestRoutes_"'`

Expected: `gofmt -l` prints nothing, `go vet` is silent, both runs print `ok  	github.com/aidan-bailey/loom/internal/web` (and the gen package), and the race run reports no data race (drop `CC=clang` where gcc exists).

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): a rebind and the layer's stop end the terminal connections" -- internal/web/server.go internal/web/web.go internal/web/terminal_test.go
```

### Task 7.4: The app's bundle

The generated client gained `terminalURL`, and `internal/web/dist` is the app's built bundle, committed and embedded; `pnpm run dist:check` fails until it is rebuilt.

**Files:**
- Modify: `internal/web/dist/**` (rebuilt by `pnpm run build`; never edited by hand)

- [ ] **Step 1: Run the check to see it fail**

Run: `pnpm -C internal/web/ui run dist:check`

Expected: FAIL, naming the entry chunk that differs from the build: `differs from the build: internal/web/dist/assets/<entry>.js` (the bundle holds `gen.ts`'s `api` object, which now has `terminalURL`).

- [ ] **Step 2: Rebuild the bundle**

Run: `pnpm -C internal/web/ui run build`

Expected: Vite prints its asset table and exits 0; `git status --short internal/web/dist` lists the entry chunk (and `index.html` and `sw.js` when their content hash changed) as modified, and a new hashed file where the old one was replaced.

- [ ] **Step 3: Run the checks to see them pass**

Run: `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/...'`

Expected: `check` passes (typecheck, lint, format, Vitest, `node --check public/sw.js`), `dist:check` prints its success line, and the Go tests pass.

- [ ] **Step 4: Commit**

```bash
git add -N internal/web/dist
git commit -m "build(web): rebuild the bundle for the terminal route's client" -- internal/web/dist
```

### Task 7.5: `internal/web/CLAUDE.md` makes room and gains the route's rules

The file is 19,987 of 20,000 bytes, so the rules this story adds (and Story 8's, which follows) need room. Supporting context moves out of existing rules into `docs/claude/web-layer.md`, and no rule's meaning changes: every rule keeps what it requires, why in a clause, what breaks, and its **Enforced** or **Convention**. Each cut below is either moved verbatim to the guide's new "Details moved out of CLAUDE.md" section or already says the same in the guide (the last one).

**Files:**
- Modify: `internal/web/CLAUDE.md` (the rules quoted below, and two rules appended)
- Modify: `docs/claude/web-layer.md` (a section added before `## Traps`)
- Test: `tools/claudemd` (`go test ./tools/claudemd/`)

- [ ] **Step 1: Add the two rules**

Append after the last rule of `internal/web/CLAUDE.md`, the line ending

```
never logging a credential is **Convention** — a credential in a file that outlives the pairing.
```

these two lines:

```
- **Check `Origin` in the terminal route itself, refuse while the switch is off, and count a connection from before the model is asked for the shell (`terminals.reserve`).** `guard` checks `Origin` only for a method but GET and HEAD, and an upgrade is a GET; a slot counted once the shell exists lets a burst past the caps. Skipping one runs a shell for another origin, for a host that turned the terminal off, or past the caps. **Enforced** by `TestTerminal_AnOriginThatIsNotLoomsIsRefusedFirst`, `TestTerminal_TheSwitchOffIsRefused403` and `TestTerminals_TheCapsCountSlotsPerDeviceAndInAll`.
- **End terminal slots and connections only from the terminals' part (`terminals.apply`, `endAll`, `close`) when the switch turns off, a device is revoked or paired again, the listener rebinds or the layer stops, and never log a terminal's bytes.** `http.Server.Close` does not track a hijacked connection, so a shell would stay open on the old listener or past its device's pairing. **Enforced** by `TestTerminals_TheSwitchTurningOffEndsEverySlot`, `TestTerminals_ARevokedOrRepairedDeviceEndsItsSlotsOnly` and `TestTerminal_ARebindEndsTheConnectionsAndTheNextListenerTakesNew`; the layer's stop by `TestTerminal_TheLayersStopEndsTheConnections`; never logging the bytes is **Convention** — a password in a file that outlives the session.
```

- [ ] **Step 2: Run the lint to see it fail**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ -run TestRepo_ClaudeMDStructure`

Expected: FAIL: `internal/web/CLAUDE.md: 21,322 bytes, over the 20000-byte budget: move context into a docs/claude guide` (the file's size after the two lines).

- [ ] **Step 3: Move the context**

In `internal/web/CLAUDE.md`, make each of these replacements (each quoted text occurs once). They are the rules for the guard (11), the mockup (12), the rebind (15), the panic stop (17), the route table (21), the refused action (22), the read route (23), notices (24), the stream (25), the push triggers (26), the escalations (27), the push sender (28), the message tree (32) and the contract (34).

Each replacement is a block to find and the block that takes its place; the find text occurs once in the file.

Rule 11 (the guard), replace

```
(DNS rebinding; never bare `localhost` or an IP, whose cookie reaches every local port), 403 for any method but GET and HEAD without one of those origins as its `Origin` header (a cross-site post), puts
```

with

```
(DNS rebinding; never bare `localhost` or an IP), 403 for any method but GET and HEAD without one of those origins as its `Origin` header, puts
```

Rule 12 (the mockup), replace

```
and call `/api/v1` with the device cookie, and the route is only an iframe's `src`, so a JSON error would show as raw text in the app's frame.
```

with

```
and call `/api/v1` with the device cookie, and a JSON error would show as raw text in the app's iframe.
```

Rule 15 (the rebind), replace

```
so a disable and an enable that one `Sync` coalesces into a single `ModelChanged` (the settings already bound) still rebind, a retry with the very values that failed still binds, and the layer's own report does not end every open stream by rebinding.
```

with

```
so a coalesced disable and enable still rebind and a retry with the very values that failed still binds.
```

Rule 17 (the panic stop), replace

```
; `fail` returns at once and runs the one stop `Close` also runs, on a goroutine of its own, so a part may call it from the goroutine the stop waits for. **Enforced**
```

with

```
 (`fail` returns at once, so a part may call it from the goroutine the stop waits for). **Enforced**
```

Rule 21 (the route table), replace

```
in `authed`, the adapter owns decoding, statuses and the log line, and the generator reads
```

with

```
in `authed`, and the generator reads
```

Rule 22 (a refused action), replace

```
withholds what it allows; the app's buttons only follow the session's `status` and `group`. **Enforced**
```

with

```
withholds what it allows. **Enforced**
```

Rule 23 (a read route), replace

```
for `reqKeep` (up to `maxReqs`), so
```

with

```
for `reqKeep`, so
```

Rule 24 (notices), replace

```
loses it silently; the daemon also hands a notice tied to the web client's request to its other clients, or keeps it for the next TUI, since the phone may never see it. **Enforced**
```

with

```
loses it silently. **Enforced**
```

Rule 25 (the stream), replace

```
ping every `pingEvery` (20 s), give the stream no write timeout over its whole life (each event's write is bounded at `streamWriteTimeout`, 30 s),
```

with

```
ping every `pingEvery`, give the stream no write timeout over its whole life (each event's write is bounded at `streamWriteTimeout`),
```

Rule 26 (the push triggers), replace

```
Within one Prompting stretch push again only for a non-empty reason or tool summary that differs from the last non-empty one pushed, since a cleared or returning label is the same prompt and each push alerts the phone again. Push a turn's stop verdict (`VerdictStop`) at most once, never for a turn that ended before the workspace's baseline: `needs_you` as waiting, `done` as done (under the device's finished toggle), and a finished push the Running→Ready edge owed only for `stopped` or no verdict. A daemon restart
```

with

```
Within one Prompting stretch push again only for a changed non-empty label, and push a turn's stop verdict (`VerdictStop`) at most once, never for a turn that ended before the baseline. A daemon restart
```

Rule 27 (the escalations), replace

```
An escalation is a `Watch.Escalations` entry the workspace's last projection lacked, identified by its issue and recorded time (`escalationKey`); the memory loads on the watcher's first poll, so a baseline taken on a projection with `Watch.Loaded` false would push
```

with

```
The memory loads on the watcher's first poll, so a baseline taken while `Watch.Loaded` is false would push
```

Rule 28 (the push sender), replace

```
with a 10 s timeout, at most 30 pushes a minute per device, a queue of at most 100, and none while
```

with

```
with a timeout, a per-device rate cap, a bounded queue, and none while
```

and replace

```
the sender goroutine and the 10 s timeout are **Convention**
```

with

```
the sender goroutine and the timeout are **Convention**
```

Rule 32 (the message tree), replace

```
in Go.** `GET /api/v1/sessions/{id}/message` stays but no screen draws the last message, so a screen that does must draw its tree too. Claude's text can hold markup;
```

with

```
in Go.** Claude's text can hold markup;
```

Rule 34 (the contract), replace

```
`api.fields` holds a line per shipped field, route, event and union value and the generator only appends; removing one means deleting its line by hand in the same commit, which a review sees.
```

with

```
Removing a line of `api.fields` (the generator only appends) is a hand edit a review sees.
```

The guide's "Changing the JSON the app reads" already says the same as the last, so that one moves nothing.

In `docs/claude/web-layer.md`, insert before the line `## Traps` this section, which holds what the cuts above removed:

```markdown
## Details moved out of CLAUDE.md

What `internal/web/CLAUDE.md` once spelled out inside a rule, kept here so the rule stays short. Each bullet names the rule it belongs to.

- **The guard:** a bare `localhost` or an IP is never an accepted Host because its cookie reaches every local port; the cross-site post `guard` refuses is a method but GET and HEAD without an accepted `Origin`.
- **The mockup route:** it is only an iframe's `src`, so a JSON error would show as raw text in the app's frame; its failures are pages (`writePage`).
- **The rebind:** every `ConfigureWeb` bumps `Rev` and nothing else does, so a disable and an enable that one `Sync` coalesces into a single `ModelChanged` (the settings already bound) still rebind, a retry with the very values that failed still binds, and the layer's own report does not end every open stream by rebinding.
- **The panic stop:** `fail` returns at once and runs the one stop `Close` also runs, on a goroutine of its own, so a part may call it from the goroutine the stop waits for.
- **The route table:** the adapter owns decoding, statuses and the log line of a typed handler.
- **A refused action:** the app's buttons only follow the session's `status` and `group`.
- **A read route:** up to `maxReqs` tracked requests are kept for `reqKeep` each.
- **Notices:** the daemon also hands a notice tied to the web client's request to its other clients, or keeps it for the next TUI, since the phone may never see it.
- **The stream:** the ping is every `pingEvery` (20 s) and each event's write is bounded at `streamWriteTimeout` (30 s).
- **Push triggers:** within one Prompting stretch the layer pushes again only for a non-empty reason or tool summary that differs from the last non-empty one pushed, since a cleared or returning label is the same prompt and each push alerts the phone again. A turn's stop verdict (`VerdictStop`) is pushed at most once and never for a turn that ended before the workspace's baseline: `needs_you` as waiting, `done` as done (under the device's finished toggle), and a finished push the Running→Ready edge owed only for `stopped` or no verdict.
- **Escalations:** an escalation is a `Watch.Escalations` entry the workspace's last projection lacked, identified by its issue and recorded time (`escalationKey`).
- **The push sender:** the timeout is 10 s, at most 30 pushes a minute per device, and the queue holds at most 100.
- **The message route:** `GET /api/v1/sessions/{id}/message` stays but no screen draws the last message, so a screen that does must draw its tree too.
```

- [ ] **Step 4: Run the lint to see it pass**

Run: `bash -c 'go run ./tools/claudemd -v | grep internal/web/CLAUDE.md && CGO_ENABLED=0 go test ./tools/claudemd/'`

Expected: the first command prints `internal/web/CLAUDE.md: 37/100 lines, 19975/20000 bytes` (25 bytes under its budget once the two rules are in: Story 8's rule must fit that or move more context first), and the tests print `ok  	github.com/aidan-bailey/loom/tools/claudemd`; every test name the new rules cite resolves.

- [ ] **Step 5: Commit**

```bash
git commit -m "docs(web): free room in the package rules and add the terminal route's rules" -- internal/web/CLAUDE.md docs/claude/web-layer.md
```

### Task 7.6: The guide gains "Adding a WebSocket route"

**Files:**
- Modify: `docs/claude/web-layer.md` (a section added before `## Adding a stream event`, and the line of the trap on a web config file that fails to load, which names the refused requests)
- Modify: `docs/claude/INDEX.md:28` (the guide's summary)
- Test: `tools/claudemd` (`go test ./tools/claudemd/`)

- [ ] **Step 1: Write the section**

In `docs/claude/web-layer.md`, insert before the line `## Adding a stream event` this section:

```markdown
## Adding a WebSocket route

The terminal route (`internal/web/terminal.go`, `terminal_conn.go`) is the pattern: a route that upgrades, so its handler is hand-served and nothing of the typed adapter applies.

1. **The entry.** `page(l, spec{...}, handler)` with `ok: http.StatusSwitchingProtocols`, a `log` name and a `client` name; registration wraps it in `authed` as any closed route. Its `doc` must say it is a WebSocket upgrade: the generator prints every page entry as "HTML for the app's iframe", and the doc, printed above that line, is what tells a reader otherwise. The generated client builds only `<client>URL` (a path); the app makes the `ws:` or `wss:` URL from its own host.
2. **Refuse before the upgrade, as JSON.** The handler is a `deviceHandler`, behind `authed` and not behind an origin check: `guard` checks `Origin` only for a method but GET and HEAD, and an upgrade is a GET. Refuse a request that is not a WebSocket upgrade first of all, 426 (`isWebSocketUpgrade`: a GET at HTTP/1.1 or above with `Connection: upgrade` and `Upgrade: websocket`; the mux's `GET` pattern also serves HEAD), then one whose handshake the library would refuse, 400 (`validHandshake`: version 13 and exactly one `Sec-WebSocket-Key` that decodes to 16 bytes), so that neither takes a slot or asks the model for anything: `websocket.Accept` checks the handshake only after the slot is taken and the model has started a shell. Check `rule.allowsOrigin` on exactly one `Origin` header yourself, and call `websocket.Accept` with `InsecureSkipVerify: true` because the layer's rule is the stricter one. Answer every refusal with `l.refuse` before `Accept`: after it the connection is the library's and no status can be sent.
3. **Count before the work.** Reserve a slot under the part's lock before asking the model for anything (`terminals.reserve`), and release it in a `defer`: a connection counted only once its shell exists lets a burst past the caps while the model works, and a refusal that forgets the release leaks a slot until a restart. The reservation refuses while the part is closed or no listener runs, while the switch is off by the part's own copy (kept by its sink from `ModelChanged`), for a device the part has not read, and at the caps.
4. **End connections from the part, never from the listener.** `http.Server.Close` does not track a hijacked connection. The part is a sink (the switch off and a device revoked or paired again), `unbindLocked` calls its `endAll` after the rule is cleared and before the listener closes (a rebind), and `stopParts` calls its `close` (the layer's stop). A slot ended before its connection exists keeps the ending and hands it to the connection when it attaches.
5. **Run the connection with `context.Background()`, not the request's** (`websocket.Accept`'s documentation: the request's context is not safe to use afterwards), and pass the layer's `recoverPanic` into every goroutine the connection starts.
6. **Record the connection you hijack, and hand it to the connection.** Give `websocket.Accept` the writer `recordHijack(w)` returns and pass `hijacked()`, the connection Accept took, to `newTermConn`: the library's close handshake reads with no deadline whatever frame the browser is part-way through sending, and `http.Server.Close` does not track a hijacked connection, so only the connection closing that socket outright (`termCloseWait` after its close frame) frees a slot whose browser stalled.
7. **Log the open and the close** (device, session, reason) under the part's logger, never the bytes.
8. **Tests.** The connection runs over a real WebSocket pair on `httptest` against a fake attach (`startTermConn` in `terminal_conn_test.go`); the route runs over `apiEnv` with the part's `ensure` and `newOpen` seams replaced (`scriptTerminals` in `terminal_test.go`), so no test reaches tmux or the model's jobs. Pin each refusal and its both sides, and each ending as a close frame a client reads.
9. **The contract.** `go generate ./internal/web`, then `pnpm -C internal/web/ui run build`: the bundle holds `gen.ts`'s `api` object, so `pnpm run dist:check` fails until it is rebuilt.
```

The terminal's switch is one more request that a `web.json` that fails to load refuses. In `docs/claude/web-layer.md`, in the trap that begins `- **A` and ends `fails to load is never overwritten.**` (in `## Traps`), replace the exact text

```
`ConfigureWeb` and `RevokeDevice` refuse, `PairDevice` answers that the web is off
```

with

```
`ConfigureWeb`, `RevokeDevice` and `SetWebTerminal` refuse, `PairDevice` answers that the web is off
```

(the old text occurs once: `grep -c 'and .RevokeDevice. refuse, .PairDevice. answers' docs/claude/web-layer.md` prints 1 before the edit and 0 after.) The guide's new section is a topic its row in `docs/claude/INDEX.md` summarises, so in the row of `web-layer.md` (line 28) replace the exact text

```
a stream event (from a sink, in the snapshot too), or a push kind (from the projection's transitions)
```

with

```
a stream event (from a sink, in the snapshot too), a WebSocket route (a `page` entry whose handler refuses before the upgrade, reserves a slot before the model is asked, and has its connections ended by a part, never by the listener), or a push kind (from the projection's transitions)
```

(the old text occurs once in the file.)

- [ ] **Step 2: Run the lint**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/`

Expected: `ok  	github.com/aidan-bailey/loom/tools/claudemd`. The guide is not a `CLAUDE.md`, so no budget applies; the lint checks its links and paths and the test names it cites (none here).

- [ ] **Step 3: Commit**

```bash
git commit -m "docs(web): the guide gains adding a WebSocket route" -- docs/claude/web-layer.md docs/claude/INDEX.md
```

### Task 7.7: The package's README and the architecture notes

**Files:**
- Modify: `internal/web/README.md` (two table rows, and a section before `## Push`)
- Modify: `docs/ARCHITECTURE.md` (a bullet before `- **Frozen environment.**`, and the web config file's row of the persistent-state table, line 121)
- Test: `tools/claudemd` (`go test ./tools/claudemd/`)

- [ ] **Step 1: Write the documents**

In `internal/web/README.md`, insert before the table row that begins `| \`static.go\` |` these two rows:

```markdown
| `terminal.go` | the terminals' part (`terminals`: the slots a connection counts as from before the model is asked for its shell, the caps, the switch's copy and device list its sink keeps, `endAll` for a rebind and `close` for the layer's stop) and the terminal route's handler (`Layer.terminal`, `Layer.shellEnded`); `tmuxOpen` is the `termOpen` over `tmux.ShellHistory` and `tmux.StartShellAttach` |
| `terminal_conn.go` | one upgraded terminal connection (`termConn`): its frames, the output pump through the alt-screen filter and a bounded buffer, pings and the endings (`termEnd`) |
```

and insert before the line `## Push` this section:

```markdown
## The terminal

`GET /api/v1/sessions/{id}/terminal` upgrades to a WebSocket onto the session's worktree shell (the tmux session `tmux.TerminalSessionName(title)`, which the TUI shares). It is off until the host turns it on (`loom web terminal on`, or the `Web Terminal` row in the TUI's settings; no route under `/api/v1` changes it), and it is a `page` entry in the table, so the generated client builds only its URL.

Before the upgrade the route answers JSON: 426 for a request that is not a WebSocket upgrade (a method but GET, below HTTP/1.1, or without the `Connection` and `Upgrade` headers) and 400 for a handshake without `Sec-WebSocket-Version: 13` or without exactly one `Sec-WebSocket-Key` that decodes to 16 bytes, both checked first, before the origin, the slot and the model; 403 unless there is exactly one `Origin` header that the origin set accepts (`guard` checks it only for a method but GET and HEAD) and 403 while the switch is off; 404 for an unknown session or a workspace terminal; 429 past 4 connections a device or 16 in all (`maxTerminalsPerDevice`, `maxTerminals`), counted from before the model is asked, so a slot is reserved (`terminals.reserve`) and released when the handler returns; 503 while the layer is stopping or the part has not read the device yet; and the model's refusal as 409 (`EnsureTerminal`: an instance that is unstarted, paused, loading, being deleted, recoverable, a workspace terminal or without a worktree; a tmux session of the shell's name held elsewhere, that is, not started in the instance's worktree; a tmux that cannot answer; a shell that ended while loom looked at it; and a session that was paused or killed while its shell was being opened), 504 when it did not answer in time and 500 when its reply named no shell. It never logs the bytes: only `web.terminal_open` and `web.terminal_close`, with the device, the session and the close's reason.

Frames: binary frames carry terminal bytes both ways. The app's first text frame `{"resize":{"cols":120,"rows":40}}` opens the attach at that size (2 to 500 columns, 1 to 200 rows); loom answers `{"state":"live"}` once attached, after the history above the visible screen. A frame from the app is at most 64 KiB. Loom pings every 20 s and drops a connection whose ping goes unanswered for 10 s, and a browser that falls 1 MiB behind is ended rather than blocking tmux. An ending is a close frame with a code and a reason: 4001 the terminal is off on this host, 4002 this device is no longer paired, 4003 the session was paused or killed, 4004 the shell exited, 4005 the browser fell behind, 4006 a frame loom does not understand, 4007 loom could not attach to the shell, and 1001 loom is stopping (the daemon stopping or the listener rebinding). The switch turning off, a device revoked or paired again, a rebind and the layer's stop end the connections they touch through the terminals' part, since `http.Server.Close` does not track a hijacked connection. The route records the connection it hijacks (`recordHijack` wraps the writer given to `websocket.Accept`) and hands it to the connection, which closes it outright when its close handshake has not finished 5 s (`termCloseWait`) after loom's close frame: the library's close reads with no deadline whatever frame the browser is part-way through sending, and a stalled browser would otherwise hold its slot for good.
```

In `docs/ARCHITECTURE.md`, insert before the line that begins `- **Frozen environment.**` this bullet:

```markdown
- **Attach clients.** A tmux session can have many clients, and tmux sizes its window to the most recently active one. Each TUI attaches its own pane clients (the agent's pane and a worktree's terminal pane). The daemon's web layer attaches a client of its own for each open web terminal (`tmux.ShellAttach`, a `tmux attach-session` in a pty with no emulator, monitor or status state), to the session's worktree shell, which the TUI's terminal pane shares: `Core.EnsureTerminal` makes the shell as a job when it does not exist, the web layer's terminals part counts and ends the connections, and `ShellAttach.Close` ends the client and never the session. Using the shell in a browser resizes it to the browser until it is used in the TUI again.
```

and in the `web.json` row of the persistent-state table (line 121), make two replacements. The first adds the key:

```
its VAPID key pair, and each paired device
```

becomes

```
its VAPID key pair, its `terminal` switch (the browser terminal; absent means off), and each paired device
```

The second adds the refusal:

```
`loom web enable` and `revoke <name>` refuse
```

becomes

```
`loom web enable`, `revoke <name>` and `terminal` refuse
```

Each old text occurs once in the file (`grep -c` prints 1 for each before the edit).

- [ ] **Step 2: Run the lint**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ ./internal/web/...`

Expected: `ok  	github.com/aidan-bailey/loom/tools/claudemd` and the web packages pass; the README's paths and test names resolve (the lint checks links, paths and test names, not the prose).

- [ ] **Step 3: Commit**

```bash
git commit -m "docs(web): document the terminal route and the daemon's attach clients" -- internal/web/README.md docs/ARCHITECTURE.md
```

## Story 8: The bare `terminal.html`

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/terminal.html`, `internal/web/ui/src/terminal/main.ts`, `internal/web/ui/src/terminal/socket.ts`, `internal/web/ui/src/terminal/socket.test.ts`, `internal/web/ui/src/terminal/terminal.css`, `internal/web/ui/vite.config.ts`, `internal/web/ui/package.json`, `internal/web/ui/pnpm-lock.yaml`, `internal/web/ui/public/sw.js`, `internal/web/static.go`, `internal/web/static_test.go`, `internal/web/static_budget_test.go`, `internal/web/dist/**`, `internal/web/CLAUDE.md`, `internal/web/ui/CLAUDE.md`, `internal/web/README.md`, `docs/claude/web-layer.md`
- **Interfaces:** exposes `/terminal.html` and `terminalCSP` to part 2; uses Story 7's `api.terminalURL` and Story 6's frames and endings
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
- **Review:** deep
- **Form:** full (a CSP relaxed on one path only, and a service worker's fetch handler)

The app's build gains a second HTML entry, `terminal.html`, whose script (`src/terminal/main.ts`) is the only one that imports xterm.js (`@xterm/xterm` 6.0.0, with `@xterm/addon-webgl` 0.19.0 loaded before `open()` and `@xterm/addon-fit` 0.11.0, each pinned exactly). It reads the session's id from the hash, opens the WebSocket at `api.terminalURL(id)` on the page's own host (`wss:` on an `https:` page, `ws:` otherwise), sends its size as the first frame, writes the daemon's binary frames into the terminal and sends its keys back in binary frames of at most 64 KiB (the daemon ends a longer frame with 4006 and the app does not reconnect after that, so a paste is split), and shows a close's reason in the page; there is no reconnecting, no panel and no phone input. The socket is a module of its own (`socket.ts`: a small state machine over an injected WebSocket) that Vitest covers with a fake. The web layer answers `/terminal.html`, and only it, with `terminalCSP` (xterm.js writes `<style>` elements at run time, which the app's `default-src 'self'` policy drops; only `style-src` is relaxed, scripts stay `'self'`), the new app's service worker passes a request for `/terminal.html` through to the network (it would answer the cached shell otherwise), and the page carries no inline script, no `style` attribute and no `loom-build` meta, since `buildStaticAssets` fills the build only into `index.html` and `sw.js`. Go tests pin the header on that path and the app's on every other, the page's markup, and that nothing the app's first load fetches holds xterm.js. `internal/web/CLAUDE.md` makes room (it has 25 bytes free once Story 7's rules are in) by moving more supporting context into `docs/claude/web-layer.md`, then gains the `terminalCSP` rule; `internal/web/ui/CLAUDE.md` gains the xterm rule. No landmine marker appears in this story's code. Run every build, test and `pnpm` command below through `loom job run -d "<what>" --` (wrap an env-prefixed command as `bash -c '…'`), as the session's rules say; the commands are what each job runs. A paired device on the classic app has the classic worker, which answers a navigation to `/terminal.html` with the classic shell (that worker is frozen, and changing it is not this story's): the owner opens the page from the new app.

### Task 8.1: The socket module

`socket.ts` holds everything about the page's socket that has logic: the frame the page opens with, what the daemon's text frames mean, the 64 KiB split of input, the size bounds, the text a close shows, and the URL and session id the page derives from its location. It imports nothing (so Vitest runs it with no DOM beyond happy-dom's events), and `main.ts` (Task 8.2) is its only user.

**Files:**
- Create: `internal/web/ui/src/terminal/socket.ts`, `internal/web/ui/src/terminal/socket.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/terminal/socket.test.ts`:

```ts
import { beforeEach, describe, expect, it } from "vitest";
import {
  FRAME_MAX,
  binaryBytes,
  describeClose,
  openSocket,
  sessionIdFromHash,
  socketURL,
  textBytes,
  type Closed,
  type Size,
  type SocketState,
  type TerminalSocket,
  type WireSocket,
} from "./socket";

const SOCKET_URL = "ws://loom.test/api/v1/sessions/7/terminal";

// A WebSocket the test drives: it records what the page sends and plays the daemon's frames.
class FakeSocket implements WireSocket {
  static made: FakeSocket[] = [];
  binaryType: BinaryType = "blob";
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  sent: (string | Uint8Array)[] = [];
  closedWith: number | undefined = undefined;

  constructor(readonly url: string) {
    FakeSocket.made.push(this);
  }

  static last(): FakeSocket {
    const ws = FakeSocket.made.at(-1);
    if (ws === undefined) throw new Error("openSocket made no socket");
    return ws;
  }

  send(data: string | Uint8Array): void {
    this.sent.push(data);
  }

  close(code?: number): void {
    this.closedWith = code;
  }

  open(): void {
    this.onopen?.(new Event("open"));
  }

  say(data: string | ArrayBuffer): void {
    this.onmessage?.(new MessageEvent("message", { data }));
  }

  end(code: number, reason: string): void {
    this.onclose?.(new CloseEvent("close", { code, reason }));
  }
}

interface Rig {
  ws: FakeSocket;
  sock: TerminalSocket;
  states: { state: SocketState; closed: Closed | undefined }[];
  data: Uint8Array[];
}

function rig(size: () => Size = () => ({ cols: 120, rows: 40 })): Rig {
  const states: Rig["states"] = [];
  const data: Uint8Array[] = [];
  const sock = openSocket(
    SOCKET_URL,
    size,
    {
      state: (state, closed) => {
        states.push({ state, closed });
      },
      data: (bytes) => {
        data.push(bytes);
      },
    },
    (url) => new FakeSocket(url),
  );
  return { ws: FakeSocket.last(), sock, states, data };
}

// A rig whose socket is open and whose daemon has said it is live.
function liveRig(): Rig {
  const r = rig();
  r.ws.open();
  r.ws.say('{"state":"live"}');
  return r;
}

// What the page sent after its first frame, binary frames only.
function inputFrames(ws: FakeSocket): Uint8Array[] {
  return ws.sent.slice(1).filter((frame): frame is Uint8Array => typeof frame !== "string");
}

beforeEach(() => {
  FakeSocket.made = [];
});

describe("openSocket", () => {
  it("sends the size as its first frame, read when the socket opens", () => {
    let size: Size = { cols: 80, rows: 24 };
    const { ws } = rig(() => size);
    expect(ws.binaryType).toBe("arraybuffer");
    expect(ws.sent).toEqual([]);

    size = { cols: 132, rows: 50 };
    ws.open();
    expect(ws.sent).toEqual(['{"resize":{"cols":132,"rows":50}}']);
  });

  it("is live only when the daemon says so", () => {
    const { ws, states } = rig();
    expect(states).toEqual([{ state: "connecting", closed: undefined }]);

    ws.open();
    expect(states.map((s) => s.state)).toEqual(["connecting"]);

    for (const text of ['{"state":"other"}', "not json", "{}", "null", '"live"']) ws.say(text);
    expect(states.map((s) => s.state)).toEqual(["connecting"]);

    ws.say('{"state":"live"}');
    expect(states.map((s) => s.state)).toEqual(["connecting", "live"]);
  });

  it("hands each binary frame to the terminal as bytes, before and after live", () => {
    const { ws, data } = rig();
    ws.open();
    ws.say(new Uint8Array([104, 105]).buffer);
    ws.say('{"state":"live"}');
    ws.say(new Uint8Array([10, 13]).buffer);
    expect(data.map((b) => Array.from(b))).toEqual([
      [104, 105],
      [10, 13],
    ]);
  });

  it("ends closed with the close's code and reason and sends nothing after", () => {
    const { ws, sock, states } = liveRig();
    ws.end(4003, "the session was paused or killed");
    expect(states.at(-1)).toEqual({
      state: "closed",
      closed: { code: 4003, reason: "the session was paused or killed" },
    });

    const before = ws.sent.length;
    expect(sock.send(new Uint8Array([97]))).toBe(false);
    sock.resize(100, 30);
    expect(ws.sent.length).toBe(before);

    ws.end(1006, "");
    expect(states.filter((s) => s.state === "closed")).toHaveLength(1);
  });

  it("sends nothing before the socket has opened", () => {
    const { ws, sock } = rig();
    expect(sock.send(new Uint8Array([97]))).toBe(false);
    sock.resize(100, 30);
    expect(ws.sent).toEqual([]);

    ws.open();
    expect(ws.sent).toEqual(['{"resize":{"cols":120,"rows":40}}']);
    expect(sock.send(new Uint8Array([97]))).toBe(true);
    expect(inputFrames(ws)).toEqual([new Uint8Array([97])]);
  });

  it("splits input into binary frames of at most 64 KiB", () => {
    const { ws, sock } = liveRig();
    const big = new Uint8Array(150_000).map((_, i) => i % 251);
    expect(sock.send(big)).toBe(true);

    const frames = inputFrames(ws);
    expect(frames.map((f) => f.length)).toEqual([FRAME_MAX, FRAME_MAX, 150_000 - 2 * FRAME_MAX]);
    const joined = new Uint8Array(150_000);
    let at = 0;
    for (const frame of frames) {
      joined.set(frame, at);
      at += frame.length;
    }
    expect(joined).toEqual(big);
  });

  it("sends exactly 64 KiB as one frame and one byte more as two", () => {
    const exact = liveRig();
    exact.sock.send(new Uint8Array(FRAME_MAX));
    expect(inputFrames(exact.ws).map((f) => f.length)).toEqual([FRAME_MAX]);

    const over = liveRig();
    over.sock.send(new Uint8Array(FRAME_MAX + 1));
    expect(inputFrames(over.ws).map((f) => f.length)).toEqual([FRAME_MAX, 1]);

    const empty = liveRig();
    expect(empty.sock.send(new Uint8Array(0))).toBe(true);
    expect(inputFrames(empty.ws)).toEqual([]);
  });

  it("keeps every size inside the daemon's bounds", () => {
    const { ws, sock } = liveRig();
    sock.resize(120, 40);
    sock.resize(1000, 1000);
    sock.resize(1, 0);
    sock.resize(Number.NaN, 120.7);
    expect(ws.sent.slice(1)).toEqual([
      '{"resize":{"cols":120,"rows":40}}',
      '{"resize":{"cols":500,"rows":200}}',
      '{"resize":{"cols":2,"rows":1}}',
      '{"resize":{"cols":2,"rows":120}}',
    ]);
  });

  it("clamps the first frame's size too", () => {
    const { ws } = rig(() => ({ cols: 9999, rows: -4 }));
    ws.open();
    expect(ws.sent).toEqual(['{"resize":{"cols":500,"rows":1}}']);
  });

  it("closes with a normal close, ends closed with code 1000 and sends nothing after", () => {
    const { ws, sock, states } = liveRig();
    sock.close();
    expect(ws.closedWith).toBe(1000);
    expect(states.at(-1)).toEqual({ state: "closed", closed: { code: 1000, reason: "" } });

    // The browser may later report 1006 for a close it was asked for (a page frozen into the
    // back-forward cache): the state stays as `close()` left it.
    ws.end(1006, "");
    expect(states.at(-1)).toEqual({ state: "closed", closed: { code: 1000, reason: "" } });
    expect(states.filter((s) => s.state === "closed")).toHaveLength(1);

    const before = ws.sent.length;
    expect(sock.send(new Uint8Array([97]))).toBe(false);
    expect(ws.sent.length).toBe(before);
  });

  it("ends closed with code 1000 when closed before the socket has opened", () => {
    const { ws, sock, states } = rig();
    sock.close();
    expect(ws.closedWith).toBe(1000);
    expect(states.at(-1)).toEqual({ state: "closed", closed: { code: 1000, reason: "" } });

    // `pagehide` before the open: the browser reports 1006 for the connection it abandoned.
    ws.end(1006, "");
    expect(states.at(-1)).toEqual({ state: "closed", closed: { code: 1000, reason: "" } });
    expect(states.filter((s) => s.state === "closed")).toHaveLength(1);
  });
});

describe("input bytes", () => {
  it("encodes typed text as UTF-8", () => {
    expect(Array.from(textBytes("aé"))).toEqual([97, 0xc3, 0xa9]);
  });

  it("reads xterm's binary string one byte a character", () => {
    expect(Array.from(binaryBytes("\x00\xffa"))).toEqual([0, 255, 97]);
  });
});

describe("socketURL", () => {
  it("is wss on an https page and ws otherwise, on the page's own host", () => {
    const path = "/api/v1/sessions/7/terminal";
    expect(socketURL(path, { protocol: "https:", host: "loom.example.com" })).toBe(
      "wss://loom.example.com/api/v1/sessions/7/terminal",
    );
    expect(socketURL(path, { protocol: "http:", host: "127.0.0.1:5670" })).toBe(
      "ws://127.0.0.1:5670/api/v1/sessions/7/terminal",
    );
  });
});

describe("sessionIdFromHash", () => {
  it.each([
    ["#7", 7],
    ["#1234", 1234],
    ["", null],
    ["#", null],
    ["#0", null],
    ["#abc", null],
    ["#7x", null],
    ["#-3", null],
    ["#/s/7", null],
    ["#99999999999999999999", null],
  ])("reads %j as %j", (hash, want) => {
    expect(sessionIdFromHash(hash)).toBe(want);
  });
});

describe("describeClose", () => {
  it("shows the daemon's reason as it is", () => {
    expect(describeClose({ code: 4001, reason: "the terminal is off on this host" })).toBe(
      "the terminal is off on this host",
    );
  });

  it("says a close the page asked for itself was its own, and to reload", () => {
    const text = describeClose({ code: 1000, reason: "" });
    expect(text).toContain("Reload");
    expect(text).not.toContain("refused");
    expect(text).not.toContain("loom web terminal on");
  });

  it("says a close with no reason from the other side was a refusal or a loss, and names the likely causes", () => {
    const text = describeClose({ code: 1006, reason: "" });
    expect(text).toContain("1006");
    expect(text).toContain("refused or lost");
    expect(text).toContain("loom web terminal on");
    expect(text).toContain("not running");
    expect(text).toContain("too many terminals");
    expect(text).not.toContain("Reload");
  });
});
```

Fails without: sends the size as its first frame, read when the socket opens: `size()` read when `openSocket` is called instead of in `onopen` (80x24 goes out), or `onopen` not sending it (no frame).
Fails without: is live only when the daemon says so: `live` set by `onopen`, `isLive` accepting any JSON or any text, or its `try/catch` removed (`not json` throws).
Fails without: hands each binary frame to the terminal as bytes, before and after live: `onmessage` ignoring an `ArrayBuffer`, or dropping a frame that arrives before `live` (the history comes first).
Fails without: ends closed with the close's code and reason and sends nothing after: `opened` not cleared by `onclose` (the `send` returns true), the code or reason not passed to the handler, or `set` letting a second close through.
Fails without: sends nothing before the socket has opened: the `opened` guard removed from `send` or `resize` (a frame goes out before the size).
Fails without: splits input into binary frames of at most 64 KiB: the `subarray` loop replaced by one `ws.send(bytes)` (a single 150,000-byte frame), or `FRAME_MAX` changed.
Fails without: sends exactly 64 KiB as one frame and one byte more as two: the loop's `at < bytes.length` written `<=` (an empty extra frame after the exact one), or a chunk size of `FRAME_MAX - 1` or `FRAME_MAX + 1`.
Fails without: keeps every size inside the daemon's bounds: `clamp` removed from `resizeFrame` (1000x1000 goes out, which the daemon ends with 4006), a bound changed, or a non-finite value not mapped to the lower bound.
Fails without: clamps the first frame's size too: the first frame built without `resizeFrame`.
Fails without: closes with a normal close, ends closed with code 1000 and sends nothing after: `close()` not calling `ws.close(1000)`, `opened` not cleared there, or `close()` not calling `set("closed", { code: 1000, reason: "" })` (the state stays live, or the later 1006 `onclose` makes it closed with 1006).
Fails without: ends closed with code 1000 when closed before the socket has opened: `close()` leaving the state to the browser's `onclose` (the state is still connecting, then closed with 1006), or the `set` guard in `close()` skipped when the socket was never open.
Fails without: encodes typed text as UTF-8: `textBytes` using `charCodeAt` (`é` becomes one byte).
Fails without: reads xterm's binary string one byte a character: `binaryBytes` using `TextEncoder` (`\xff` becomes two bytes).
Fails without: is wss on an https page and ws otherwise, on the page's own host: the protocol test inverted or dropped, or `host` replaced by `hostname`.
Fails without: the `sessionIdFromHash` rows: the pattern loosened to accept `#7x` or `#-3`, the `id > 0` term removed (`#0`), or `Number.isSafeInteger` removed (`#99999999999999999999`).
Fails without: shows the daemon's reason as it is: `describeClose` always answering the fallback.
Fails without: says a close the page asked for itself was its own, and to reload: the `code === 1000` branch of `describeClose` removed (a normal close is blamed on the host), or its text losing "Reload".
Fails without: says a close with no reason from the other side was a refusal or a loss, and names the likely causes: the fallback losing the code, "refused or lost", the `loom web terminal on` hint, "not running" or "too many terminals", or the `code === 1000` branch taken for every empty reason (the 1006 text says "Reload").

- [ ] **Step 2: Run it to see it fail**

Run: `loom job run -d "socket tests, expecting a load failure" -- pnpm -C internal/web/ui exec vitest run src/terminal/socket.test.ts`

Expected: FAIL: the suite does not load, `Failed to load url ./socket (resolved id: ./socket) in …/src/terminal/socket.test.ts. Does the file exist?`.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/terminal/socket.ts`:

```ts
// The terminal page's socket (spec §17): one WebSocket to the daemon's terminal route, as a small
// state machine with no reconnecting (part 2's panel adds that). The first frame the page sends is
// its size; text frames carry JSON control messages and binary frames carry terminal bytes both
// ways. A binary frame the page sends is at most FRAME_MAX bytes, because the daemon ends a longer
// one with 4006 and the app does not reconnect after that.

/** The longest frame the daemon accepts from the page (`termFrameMax` in internal/web/terminal_conn.go). */
export const FRAME_MAX = 64 * 1024;

// The sizes the daemon accepts (`termMinCols` and its neighbours in terminal_conn.go).
const MIN_COLS = 2;
const MAX_COLS = 500;
const MIN_ROWS = 1;
const MAX_ROWS = 200;

/** The part of a WebSocket this module uses, so a test can stand in for one. */
export interface WireSocket {
  binaryType: BinaryType;
  onopen: ((ev: Event) => void) | null;
  onmessage: ((ev: MessageEvent) => void) | null;
  onclose: ((ev: CloseEvent) => void) | null;
  onerror: ((ev: Event) => void) | null;
  send(data: string | Uint8Array): void;
  close(code?: number): void;
}

export type SocketState = "connecting" | "live" | "closed";

export interface Size {
  cols: number;
  rows: number;
}

/** A close frame's code and reason; a connection that dropped with none has code 1006 and "". */
export interface Closed {
  code: number;
  reason: string;
}

export interface Handlers {
  /** "connecting" at once, "live" when the daemon says it attached, then "closed" with the close. */
  state(state: SocketState, closed?: Closed): void;
  /** Each binary frame the daemon sends: the shell's output, its history first. */
  data(bytes: Uint8Array): void;
}

export interface TerminalSocket {
  /** Sends input in frames of at most FRAME_MAX; false while the socket is not open. */
  send(bytes: Uint8Array): boolean;
  /** Tells the daemon the terminal's new size; nothing while the socket is not open. */
  resize(cols: number, rows: number): void;
  /** Ends the connection with a normal close. */
  close(): void;
}

function clamp(value: number, low: number, high: number): number {
  return Math.min(high, Math.max(low, Number.isFinite(value) ? Math.trunc(value) : low));
}

/** The text frame that tells the daemon the size, inside its bounds. */
export function resizeFrame(size: Size): string {
  const cols = clamp(size.cols, MIN_COLS, MAX_COLS);
  const rows = clamp(size.rows, MIN_ROWS, MAX_ROWS);
  return JSON.stringify({ resize: { cols, rows } });
}

// isLive reports whether a text frame is the daemon's {"state":"live"}.
function isLive(text: string): boolean {
  try {
    const frame: unknown = JSON.parse(text);
    return typeof frame === "object" && frame !== null && "state" in frame && frame.state === "live";
  } catch {
    return false;
  }
}

/**
 * Opens the terminal route at url. The first frame is the size, which `size` gives when the socket
 * opens, so a window resized meanwhile is not sent stale. `make` builds the WebSocket (a test
 * passes a fake).
 */
export function openSocket(
  url: string,
  size: () => Size,
  on: Handlers,
  make: (url: string) => WireSocket = (u) => new WebSocket(u),
): TerminalSocket {
  const ws = make(url);
  ws.binaryType = "arraybuffer";
  let state: SocketState = "connecting";
  let opened = false;

  const set = (next: SocketState, closed?: Closed): void => {
    if (state === "closed" || next === state) return;
    state = next;
    on.state(next, closed);
  };
  on.state("connecting");

  ws.onopen = () => {
    ws.send(resizeFrame(size()));
    opened = true;
  };
  ws.onmessage = (ev) => {
    const data: unknown = ev.data;
    if (typeof data === "string") {
      if (isLive(data)) set("live");
      return;
    }
    if (data instanceof ArrayBuffer) on.data(new Uint8Array(data));
  };
  ws.onclose = (ev) => {
    opened = false;
    set("closed", { code: ev.code, reason: ev.reason });
  };
  ws.onerror = () => {
    // A close event follows an error and carries the code.
  };

  return {
    send(bytes) {
      if (!opened) return false;
      for (let at = 0; at < bytes.length; at += FRAME_MAX) {
        ws.send(bytes.subarray(at, at + FRAME_MAX));
      }
      return true;
    },
    resize(cols, rows) {
      if (!opened) return;
      ws.send(resizeFrame({ cols, rows }));
    },
    close() {
      opened = false;
      // The close code is the only sign the page closed the socket itself, and the browser may
      // report 1006 for it later (before the open, or from the back-forward cache): the state
      // is closed here, with code 1000, and the later `onclose` changes nothing.
      set("closed", { code: 1000, reason: "" });
      ws.close(1000);
    },
  };
}

const encoder = new TextEncoder();

/** What xterm.js's onData gives (typed or pasted text) as the bytes to send. */
export function textBytes(text: string): Uint8Array {
  return encoder.encode(text);
}

/** What xterm.js's onBinary gives (a string of one byte a character) as the bytes to send. */
export function binaryBytes(text: string): Uint8Array {
  const bytes = new Uint8Array(text.length);
  for (let i = 0; i < text.length; i++) bytes[i] = text.charCodeAt(i) & 0xff;
  return bytes;
}

/** The socket's URL: the route's path on the page's own host, wss on an https page. */
export function socketURL(path: string, loc: { protocol: string; host: string }): string {
  return `${loc.protocol === "https:" ? "wss:" : "ws:"}//${loc.host}${path}`;
}

/** The session id in a `#<id>` hash, or null when the hash is anything else. */
export function sessionIdFromHash(hash: string): number | null {
  const match = hash.match(/^#(\d+)$/);
  if (match === null) return null;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}

/**
 * The text the page shows when the socket closes: the daemon's reason; else, for the normal close
 * the page asked for itself (`close()` from `pagehide`, which Safari may restore from its
 * back-forward cache), that it did, and to reload; else that the connection was refused or lost.
 * A refusal before the upgrade (for example 403, 404, 409, 426, 429, 500, 503 or 504; the list is
 * of examples, not the whole route's) reaches the browser as code 1006 with no reason, so the
 * causes are named as possible, never asserted.
 */
export function describeClose(closed: Closed): string {
  if (closed.reason !== "") return closed.reason;
  if (closed.code === 1000) {
    return "This terminal was closed from this page. Reload the page to open it again.";
  }
  return (
    `The connection was refused or lost (code ${closed.code}). ` +
    "The terminal may be off on this host (loom web terminal on), the session may not be running, " +
    "or too many terminals may be open."
  );
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/terminal && pnpm -C internal/web/ui exec vitest run src/terminal/socket.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint'`, through `loom job run -d "socket tests, typecheck and lint"`.

Expected: Prettier rewrites at most whitespace in the two files (it is run so the later `format:check` has nothing to say), Vitest prints `Tests  27 passed (27)` for `socket.test.ts`, and the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/terminal/socket.ts internal/web/ui/src/terminal/socket.test.ts
git commit -m "feat(web): the terminal page's socket module" -- internal/web/ui/src/terminal/socket.ts internal/web/ui/src/terminal/socket.test.ts
```

### Task 8.2: The page, its build entry and what pins it

The page is a second HTML entry of the Vite build. Its tests are written first and fail because the bundle has no `terminal.html`: the page's markup, the licences the build writes for the new packages, and a guard that xterm.js stays in the terminal page's files and out of everything the app's first load fetches.

**Files:**
- Create: `internal/web/ui/terminal.html`, `internal/web/ui/src/terminal/main.ts`, `internal/web/ui/src/terminal/terminal.css`
- Modify: `internal/web/ui/package.json` (three dependencies), `internal/web/ui/pnpm-lock.yaml` (written by pnpm), `internal/web/ui/vite.config.ts` (the build's input)
- Modify: `internal/web/dist/**` (rebuilt, never edited by hand)
- Test: `internal/web/static_test.go`, `internal/web/static_budget_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/web/static_test.go`, replace

```go
	for _, pkg := range []string{"react", "react-dom", "@fontsource-variable/inter", "@fontsource-variable/jetbrains-mono"} {
```

with

```go
	for _, pkg := range []string{"react", "react-dom", "@fontsource-variable/inter", "@fontsource-variable/jetbrains-mono", "@xterm/xterm", "@xterm/addon-webgl", "@xterm/addon-fit"} {
```

and, after the function

```go
func TestStatic_NextIndexHasNoInlineScriptOrStyle(t *testing.T) {
	require.Empty(t, staticInlineMarkup(string(nextStaticAssets(t)["/index.html"].body)))
}
```

add

```go
// The terminal's document is its own entry of the build: a bare page whose script is the only
// one that imports xterm.js.
func TestStatic_TheTerminalPageIsABareShellOfItsOwn(t *testing.T) {
	assets := nextStaticAssets(t)
	page, ok := assets["/terminal.html"]
	require.True(t, ok, "the bundle has no /terminal.html: is it an input of vite.config.ts?")
	require.Equal(t, "text/html; charset=utf-8", page.ctype)
	body := string(page.body)

	require.Empty(t, staticInlineMarkup(body), "the page's policy keeps scripts to 'self' and the markup sets no style attribute")
	require.NotContains(t, body, "loom-build", "buildStaticAssets fills the build only into index.html and sw.js")
	require.NotContains(t, body, "__LOOM_", "a placeholder nothing fills")
	require.Contains(t, body, `<div id="terminal"></div>`, "main.ts mounts the terminal there")
	require.Contains(t, body, `id="status"`, "main.ts shows the connection's state there")
	for _, ref := range staticAttrRef.FindAllStringSubmatch(body, -1) {
		require.Contains(t, assets, ref[1], "terminal.html references a file the bundle lacks")
	}

	pageFiles, err := firstLoadFiles(body)
	require.NoError(t, err, "terminal.html names no entry script")
	appFiles, err := firstLoadFiles(string(assets["/index.html"].body))
	require.NoError(t, err)
	require.NotContains(t, pageFiles, appFiles[0], "the terminal page does not run the app's entry")
}
```

In `internal/web/static_budget_test.go`, add after `TestCheckLazyViews_TheSettingsChunkIsRequiredAndKeptOutOfTheEntry` (the file's last function):

```go
// xtermMarker is a string only xterm.js's core holds (the class of the textarea it creates), which
// a minified bundle keeps.
const xtermMarker = "xterm-helper-textarea"

// checkXtermScope fails when xterm.js is in a file the app's first load fetches (app), in any
// script terminal.html does not load (page), or in none of the page's own files: a guard that
// finds nothing would pass for ever.
func checkXtermScope(assets map[string]staticAsset, page, app []string) error {
	holds := func(url string) bool {
		a, ok := assets[url]
		return ok && bytes.Contains(a.body, []byte(xtermMarker))
	}
	for _, url := range app {
		if holds(url) {
			return fmt.Errorf("%s, which index.html loads on every launch, holds xterm.js: only the terminal page's script may import it", url)
		}
	}
	var stray []string
	for url := range assets {
		if strings.HasSuffix(url, ".js") && holds(url) && !slices.Contains(page, url) {
			stray = append(stray, url)
		}
	}
	slices.Sort(stray)
	if len(stray) > 0 {
		return fmt.Errorf("xterm.js is in %s, which terminal.html does not load", strings.Join(stray, ", "))
	}
	for _, url := range page {
		if holds(url) {
			return nil
		}
	}
	return fmt.Errorf("none of terminal.html's files (%s) holds xterm.js: is the marker %q still in its build?", strings.Join(page, ", "), xtermMarker)
}

func TestDist_XtermLoadsOnlyInTheTerminalPage(t *testing.T) {
	assets := nextStaticAssets(t)
	page, ok := assets["/terminal.html"]
	require.True(t, ok, "the bundle has no /terminal.html: is it an input of vite.config.ts?")
	pageFiles, err := firstLoadFiles(string(page.body))
	require.NoError(t, err)
	appFiles, err := firstLoadFiles(string(assets["/index.html"].body))
	require.NoError(t, err)
	require.NoError(t, checkXtermScope(assets, pageFiles, appFiles))
}

func TestCheckXtermScope_FailsOnACopyOutsideTheTerminalPageAndOnNone(t *testing.T) {
	const xterm = "var a=\"" + xtermMarker + "\";"
	build := func(overrides map[string]string) map[string]staticAsset {
		files := map[string]string{
			"/assets/terminal-a.js":  xterm,
			"/assets/shared-b.js":    "export const x = 1;",
			"/assets/index-c.js":     "import './shared-b.js';",
			"/assets/Lazy-d.js":      "export const y = 2;",
			"/assets/terminal-e.css": "." + xtermMarker + "{}",
		}
		for url, body := range overrides {
			files[url] = body
		}
		assets := map[string]staticAsset{}
		for url, body := range files {
			assets[url] = staticAsset{body: []byte(body)}
		}
		return assets
	}
	page := []string{"/assets/terminal-a.js", "/assets/shared-b.js"}
	app := []string{"/assets/index-c.js", "/assets/shared-b.js"}

	require.NoError(t, checkXtermScope(build(nil), page, app), "xterm.js only in the terminal page's script, and a stylesheet naming its class, is the clean case")

	err := checkXtermScope(build(map[string]string{"/assets/index-c.js": xterm}), page, app)
	require.ErrorContains(t, err, "/assets/index-c.js", "the app's entry holds it")

	err = checkXtermScope(build(map[string]string{"/assets/shared-b.js": xterm}), page, app)
	require.ErrorContains(t, err, "/assets/shared-b.js", "a chunk both pages load holds it, and index.html preloads it")

	err = checkXtermScope(build(map[string]string{"/assets/Lazy-d.js": xterm}), page, app)
	require.ErrorContains(t, err, "/assets/Lazy-d.js", "a lazy chunk the terminal page does not load holds it")

	err = checkXtermScope(build(map[string]string{"/assets/terminal-a.js": "var a=1;"}), page, app)
	require.ErrorContains(t, err, "none of terminal.html's files", "the page's own files hold none: the marker is gone from the build")
}
```

Fails without: TestStatic_TheTerminalPageIsABareShellOfItsOwn: `terminal.html` missing from the bundle (no entry in `vite.config.ts`), an inline `<script>` or a `style=` attribute in it (`staticInlineMarkup`), the `loom-build` meta or a `__LOOM_` placeholder in it, the `#terminal` or `#status` element renamed, a script or link naming a file the build lacks, or the page loading the app's entry script.
Fails without: TestStatic_NextTreeBuilds: the build not writing `@xterm/xterm`, `@xterm/addon-webgl` or `@xterm/addon-fit` into `THIRD_PARTY.txt` (the page not importing them).
Fails without: TestDist_XtermLoadsOnlyInTheTerminalPage: `main.ts`'s xterm imports moved into a module the app's entry imports (a copy in the first load), or `terminal.html` not importing xterm.js at all (the marker is in none of its files).
Fails without: TestCheckXtermScope_FailsOnACopyOutsideTheTerminalPageAndOnNone: the app loop removed from `checkXtermScope` (the entry and shared-chunk cases pass), the stray loop removed (the lazy-chunk case passes), the `.js` suffix test removed (the clean case's stylesheet fails it), or the last return replaced by `nil` (the empty page passes).

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "terminal page tests, expecting failures" -- bash -c 'CGO_ENABLED=0 go test ./internal/web/ -run "TestStatic_TheTerminalPage|TestStatic_NextTreeBuilds|TestDist_Xterm|TestCheckXtermScope"'`

Expected: FAIL: `TestStatic_TheTerminalPageIsABareShellOfItsOwn` and `TestDist_XtermLoadsOnlyInTheTerminalPage` with `the bundle has no /terminal.html: is it an input of vite.config.ts?`, and `TestStatic_NextTreeBuilds` with `THIRD_PARTY.txt lacks @xterm/xterm`; `TestCheckXtermScope_FailsOnACopyOutsideTheTerminalPageAndOnNone` passes (it reads no bundle).

- [ ] **Step 3: Implement**

Add the three packages, pinned exactly: `loom job run -d "add the xterm packages" -- pnpm -C internal/web/ui add --save-exact @xterm/xterm@6.0.0 @xterm/addon-webgl@0.19.0 @xterm/addon-fit@0.11.0`. In `internal/web/ui/package.json`'s `dependencies`, between `"@tanstack/react-router": …` and `"class-variance-authority": …`, this adds

```json
    "@xterm/addon-fit": "0.11.0",
    "@xterm/addon-webgl": "0.19.0",
    "@xterm/xterm": "6.0.0",
```

(no `^`), and `pnpm-lock.yaml` changes with them; commit both as pnpm wrote them.

Create `internal/web/ui/terminal.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="theme-color" content="#0f1012" />
    <meta name="color-scheme" content="dark" />
    <title>Loom terminal</title>
  </head>
  <body>
    <div id="terminal"></div>
    <p id="status" role="status" hidden></p>
    <noscript>Loom needs JavaScript.</noscript>
    <script type="module" src="/src/terminal/main.ts"></script>
  </body>
</html>
```

Create `internal/web/ui/src/terminal/terminal.css`:

```css
/* The terminal page (spec §23): the terminal fills the window, and a status line sits over its
   bottom edge while the socket connects or after it closed. Colours come from the app's tokens
   (styles/app.css, which main.ts imports); the font is the stack of app.css's --font-mono. */
html,
body {
  height: 100%;
  overflow: hidden;
}

#terminal {
  position: fixed;
  inset: 0;
  background: var(--background);
}

#status {
  position: fixed;
  right: 0;
  bottom: 0;
  left: 0;
  margin: 0;
  padding: 6px 12px;
  background: var(--card);
  color: var(--attention);
  font-family: "JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, monospace;
}

#status[hidden] {
  display: none;
}
```

Create `internal/web/ui/src/terminal/main.ts`:

```ts
// The terminal's document (spec §23): xterm.js on a session's worktree shell, over the daemon's
// WebSocket. It is the only script that imports xterm.js, and the web layer serves its page under
// terminalCSP, which lets xterm.js write the <style> elements the app's policy drops. Part 1
// ships it bare: no reconnecting, no panel and no phone input.
import "@xterm/xterm/css/xterm.css";
import "../styles/app.css";
import "./terminal.css";

import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import { Terminal } from "@xterm/xterm";

import { api } from "../api/request";
import {
  binaryBytes,
  describeClose,
  openSocket,
  sessionIdFromHash,
  socketURL,
  textBytes,
} from "./socket";

// The stack of app.css's --font-mono, which has no variable at run time.
const FONT = '"JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, monospace';
const FONT_SIZE = 13;

// token reads one of the app's colour tokens, for xterm.js's theme.
function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// webglAvailable reports whether the browser gives a WebGL2 context. The WebGL addon throws from
// open() when it cannot get one, so it is loaded only when this says it can; a context lost later
// is handled by the addon's own event.
function webglAvailable(): boolean {
  const gl = document.createElement("canvas").getContext("webgl2");
  if (gl === null) return false;
  gl.getExtension("WEBGL_lose_context")?.loseContext();
  return true;
}

function element(id: string): HTMLElement {
  const el = document.getElementById(id);
  if (el === null) throw new Error(`terminal.html has no #${id}`);
  return el;
}

function start(host: HTMLElement, say: (text: string) => void, session: number): void {
  const term = new Terminal({
    cursorBlink: true,
    fontFamily: FONT,
    fontSize: FONT_SIZE,
    scrollback: 10000,
    theme: {
      background: token("--background"),
      foreground: token("--foreground"),
      cursor: token("--attention"),
      selectionBackground: token("--input"),
    },
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  // Loaded before open(): the addon waits for it (the spike's item 1 measured the fewest style
  // violations this way). The DOM renderer serves when WebGL is unavailable or its context is
  // lost, and terminalCSP allows its styles.
  if (webglAvailable()) {
    const webgl = new WebglAddon();
    webgl.onContextLoss(() => {
      webgl.dispose();
    });
    term.loadAddon(webgl);
  }
  term.open(host);
  fit.fit();

  const url = socketURL(api.terminalURL(session), window.location);
  const sock = openSocket(url, () => ({ cols: term.cols, rows: term.rows }), {
    state: (state, closed) => {
      if (state === "connecting") {
        say("Connecting…");
      } else if (state === "live") {
        say("");
        term.focus();
      } else {
        say(describeClose(closed ?? { code: 1006, reason: "" }));
      }
    },
    data: (bytes) => {
      term.write(bytes);
    },
  });
  term.onData((text) => {
    sock.send(textBytes(text));
  });
  term.onBinary((text) => {
    sock.send(binaryBytes(text));
  });
  term.onResize(({ cols, rows }) => {
    sock.resize(cols, rows);
  });
  new ResizeObserver(() => {
    fit.fit();
  }).observe(host);
  window.addEventListener("pagehide", () => {
    sock.close();
  });
}

function main(): void {
  const host = element("terminal");
  const status = element("status");
  const say = (text: string): void => {
    status.textContent = text;
    status.hidden = text === "";
  };

  const session = sessionIdFromHash(window.location.hash);
  if (session === null) {
    say("Open this page as /terminal.html#<session id>.");
    return;
  }
  say("Loading…");
  // xterm.js measures a cell when it opens: wait for the font, or the grid is sized for the
  // fallback face.
  void document.fonts
    .load(`${FONT_SIZE}px "JetBrains Mono Variable"`)
    .catch(() => [])
    .then(() => {
      start(host, say, session);
    })
    .catch((err: unknown) => {
      say(`The terminal failed to start: ${err instanceof Error ? err.message : "unknown error"}`);
    });
}

main();
```

In `internal/web/ui/vite.config.ts`, add above `export default defineConfig({`:

```ts
// The two documents of the build: the app's (index.html) and the terminal's (terminal.html), whose
// script is the only one that imports xterm.js, so the app's first load never holds it.
const page = (name: string): string => decodeURIComponent(new URL(name, import.meta.url).pathname);

```

and replace

```ts
    rolldownOptions: {
      output: {
```

with

```ts
    rolldownOptions: {
      input: { index: page("index.html"), terminal: page("terminal.html") },
      output: {
```

Then format, check the types and rebuild the bundle: `loom job run -d "format, typecheck and build the app" -- bash -c 'pnpm -C internal/web/ui exec prettier --write terminal.html src/terminal vite.config.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui run build'`. Vite builds both documents: `git status --short internal/web/dist` lists `terminal.html` and a `terminal-<hash>.js` entry script (and the xterm stylesheet, folded into a CSS file) as new, `index.html` and `THIRD_PARTY.txt` as modified, and the app's own chunks as unchanged unless a shared module moved. If the build names the entry differently, the tests read it from the page, not by name.

- [ ] **Step 4: Run them to see them pass**

Run: `bash -c 'CGO_ENABLED=0 go test ./internal/web/... && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`, as three `loom job run` calls.

Expected: the Go packages `ok` (the new tests, `TestDist_FirstLoadIsWithinBudget` with an `index.html` whose first load is unchanged, `TestDist_LazyViewsAreChunksOfTheirOwn`, `TestDist_TheSkinBundlesOnlyTheLatinFonts` with the same two fonts, and `TestStatic_NextServiceWorkerCarriesTheBuildAndTheShell` with `/terminal.html` in the shell list); `check` passes (typecheck, lint, format, the socket's Vitest, `node --check public/sw.js`); `dist:check` prints `internal/web/dist matches the build.` If `TestDist_XtermLoadsOnlyInTheTerminalPage` fails with `none of terminal.html's files … holds xterm.js`, read the built page's script for a string only xterm.js has and change `xtermMarker` to it (the detection table in `TestCheckXtermScope_…` uses the constant, so it follows).

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/terminal.html internal/web/ui/src/terminal/main.ts internal/web/ui/src/terminal/terminal.css internal/web/dist
git commit -m "feat(web): the bare terminal page, a second entry of the app's build" -- internal/web/ui/terminal.html internal/web/ui/src/terminal/main.ts internal/web/ui/src/terminal/terminal.css internal/web/ui/package.json internal/web/ui/pnpm-lock.yaml internal/web/ui/vite.config.ts internal/web/static_test.go internal/web/static_budget_test.go internal/web/dist
```

### Task 8.3: `terminalCSP` on `/terminal.html` and on no other path

`guard` sets the app's policy on every answer before any handler runs, and a handler may replace it (the mockup does). `serveTrees` answers every static file, so it sets `terminalCSP` for the one URL `/terminal.html`.

**Files:**
- Modify: `internal/web/static.go` (the new constants above `serveTrees`, and `serveTrees` at lines 257-285)
- Test: `internal/web/static_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/web/static_test.go`, after the function `TestStatic_TheTerminalPageIsABareShellOfItsOwn` (Task 8.2), add:

```go
// The terminal page alone is answered under terminalCSP; guard's policy stays on every other
// answer, the page's own script included.
func TestStatic_TheTerminalPageAloneGetsItsOwnPolicy(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)

	assets := nextStaticAssets(t)
	appFiles, err := firstLoadFiles(string(assets["/index.html"].body))
	require.NoError(t, err)
	pageFiles, err := firstLoadFiles(string(assets["/terminal.html"].body))
	require.NoError(t, err)

	newApp := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: appCookie, Value: "next"}) }
	for _, tc := range []struct {
		name   string
		mutate []func(*http.Request)
	}{
		{"a device on the classic app", nil},
		{"a device on the new app", []func(*http.Request){newApp}},
	} {
		resp := httpDo(t, addr, http.MethodGet, terminalPage, "", tc.mutate...)
		require.Equal(t, http.StatusOK, resp.StatusCode, tc.name)
		require.Contains(t, resp.Header.Get("Content-Type"), "text/html", tc.name)
		require.Equal(t, terminalCSP, resp.Header.Get("Content-Security-Policy"), tc.name)
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"), tc.name+": guard's other headers stay")
		require.Contains(t, bodyText(t, resp), `<div id="terminal"></div>`, tc.name)
	}

	for _, url := range []string{"/", "/index.html", "/sw.js", "/manifest.webmanifest", "/icons/icon-192.png", appFiles[0], pageFiles[0]} {
		resp := httpDo(t, addr, http.MethodGet, url, "", newApp)
		require.Equal(t, http.StatusOK, resp.StatusCode, url)
		require.Equal(t, contentSecurityPolicy, resp.Header.Get("Content-Security-Policy"), url+" keeps the app's policy")
	}
}

// terminalCSP relaxes style-src and nothing else: xterm.js writes <style> elements at run time,
// while scripts, connections and frames stay under default-src 'self'.
func TestStatic_TheTerminalPolicyLoosensOnlyStyles(t *testing.T) {
	require.Equal(t, "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'", terminalCSP)

	directives := map[string]string{}
	for _, d := range strings.Split(terminalCSP, "; ") {
		name, value, _ := strings.Cut(d, " ")
		directives[name] = value
	}
	require.Equal(t, "'self'", directives["default-src"], "scripts and connections fall to default-src")
	require.Equal(t, "'self' 'unsafe-inline'", directives["style-src"])
	for name, value := range directives {
		if name != "style-src" {
			require.NotContains(t, value, "unsafe", name+" stays strict")
		}
	}
	require.NotContains(t, directives, "script-src", "no directive of its own for scripts")
	require.Equal(t, "'self'", directives["frame-ancestors"], "part 2's panel frames the page from the app's own origin")
}
```

Fails without: TestStatic_TheTerminalPageAloneGetsItsOwnPolicy: the header not set for `terminalPage` (the first loop answers the app's policy), or set for every URL (the second loop's `/` answers `terminalCSP`), or set only for the tree the cookie picks (the classic-app row).
Fails without: TestStatic_TheTerminalPolicyLoosensOnlyStyles: `'unsafe-inline'` added to `default-src` or any directive but `style-src`, a `script-src` directive added, or `frame-ancestors` changed.

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "terminal policy tests, expecting a build failure" -- bash -c 'CGO_ENABLED=0 go test ./internal/web/ -run "TestStatic_TheTerminalP"'`

Expected: FAIL to build: `undefined: terminalPage` and `undefined: terminalCSP`.

- [ ] **Step 3: Implement**

In `internal/web/static.go`, above the comment that begins `// serveTrees answers url from the tree treeOf picks`, add:

```go
// terminalPage is the one URL answered under terminalCSP: the terminal's document
// (internal/web/ui/terminal.html), whose script runs xterm.js.
const terminalPage = "/terminal.html"

// terminalCSP is the policy of terminalPage, and of no other path: the app's own, which is
// default-src 'self' with strict framing, except that style-src allows inline styles, because
// xterm.js appends <style> elements and sets style attributes at run time and the app's policy
// drops them. Scripts and connections stay under default-src 'self'. frame-ancestors is 'self' so
// the app's panel can frame the page (part 2), and no other origin can.
const terminalCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"

```

In the same file, replace

```go
// is down. A tree that failed to build holds no assets, so no other request
// reaches it.
func serveTrees(trees [2]staticTreeBuild, url string) http.HandlerFunc {
	shell := isShellFile(url)
	return func(w http.ResponseWriter, r *http.Request) {
```

with

```go
// is down. A tree that failed to build holds no assets, so no other request
// reaches it. The answer for terminalPage replaces guard's policy with
// terminalCSP; every other URL keeps guard's.
func serveTrees(trees [2]staticTreeBuild, url string) http.HandlerFunc {
	shell := isShellFile(url)
	policy := ""
	if url == terminalPage {
		policy = terminalCSP
	}
	return func(w http.ResponseWriter, r *http.Request) {
```

and replace

```go
		if !ok {
			http.NotFound(w, r)
			return
		}
		a.serve(w, r)
	}
}
```

with

```go
		if !ok {
			http.NotFound(w, r)
			return
		}
		if policy != "" {
			w.Header().Set("Content-Security-Policy", policy)
		}
		a.serve(w, r)
	}
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `loom job run -d "web package tests" -- bash -c 'CGO_ENABLED=0 go test ./internal/web/...'`

Expected: `ok  	github.com/aidan-bailey/loom/internal/web` and its subpackages; `TestMux_ServesTheAppBehindTheGuard` still sees `contentSecurityPolicy` on `/`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): answer the terminal page, and only it, under terminalCSP" -- internal/web/static.go internal/web/static_test.go
```

### Task 8.4: The service worker passes `/terminal.html` through

The new app's worker answers every navigation, a frame's included, with the cached shell (`/`), so a load of `/terminal.html` on a controlled page would get the app, not the terminal, and never reach the network where the server sets its policy. Its `fetch` handler returns early for that path; the page's script and styles still come from the cache by their paths.

**Files:**
- Modify: `internal/web/ui/public/sw.js` (the constants at line 10 and the `fetch` handler at lines 46-54)
- Modify: `internal/web/dist/**` (rebuilt: `dist/sw.js` is a copy of it)
- Test: `internal/web/static_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/web/static_test.go`, after the function `TestStatic_EachWorkerFetchesItsOwnShell`, add:

```go
// The new app's worker answers a navigation with the cached shell, so the terminal's document
// must be let through to the network before it answers anything: the server answers it under
// terminalCSP, and the shell would replace it.
func TestStatic_NextWorkerPassesTheTerminalPageThrough(t *testing.T) {
	code := swCode(string(nextStaticAssets(t)["/sw.js"].body))

	require.Contains(t, code, `const TERMINAL_PAGE = "`+terminalPage+`";`, "the worker's path is the server's")
	pass := strings.Index(code, "if (url.pathname === TERMINAL_PAGE) return;")
	answer := strings.Index(code, "event.respondWith(")
	require.NotEqual(t, -1, pass, "the worker has no pass-through for the terminal page")
	require.NotEqual(t, -1, answer)
	require.Less(t, pass, answer, "the pass-through comes before the worker answers from the cache")

	// The ordinary case still holds: a navigation is the cached shell, any other file its own entry.
	require.Contains(t, code, `const key = req.mode === "navigate" ? "/" : url.pathname;`)
	require.Contains(t, code, "cache.match(key)")
}
```

Fails without: TestStatic_NextWorkerPassesTheTerminalPageThrough: the `if (url.pathname === TERMINAL_PAGE) return;` line removed or moved after `event.respondWith(`, `TERMINAL_PAGE` set to another path than `terminalPage`, or the shell's `key` line changed.

- [ ] **Step 2: Run it to see it fail**

Run: `loom job run -d "worker test, expecting a failure" -- bash -c 'CGO_ENABLED=0 go test ./internal/web/ -run TestStatic_NextWorkerPassesTheTerminalPageThrough'`

Expected: FAIL: `Should contain: const TERMINAL_PAGE = "/terminal.html";`.

- [ ] **Step 3: Implement**

In `internal/web/ui/public/sw.js`, replace

```js
const SHELL_FILES = ["/", "/index.html", "/sw.js", "/manifest.webmanifest"];
```

with

```js
const SHELL_FILES = ["/", "/index.html", "/sw.js", "/manifest.webmanifest"];
// The terminal's document is served by the network under a policy of its own (terminalCSP in
// internal/web/static.go), never from the cached shell.
const TERMINAL_PAGE = "/terminal.html";
```

and replace

```js
  if (url.origin !== self.location.origin || url.pathname.startsWith("/api/")) return;
  const key = req.mode === "navigate" ? "/" : url.pathname;
```

with

```js
  if (url.origin !== self.location.origin || url.pathname.startsWith("/api/")) return;
  // Without respondWith the browser asks the network; the page's script and styles are other
  // paths and still come from the cache below.
  if (url.pathname === TERMINAL_PAGE) return;
  const key = req.mode === "navigate" ? "/" : url.pathname;
```

Rebuild the bundle, whose `sw.js` is a copy: `loom job run -d "rebuild the app bundle" -- pnpm -C internal/web/ui run build`; `git status --short internal/web/dist` lists `sw.js` as modified and nothing else.

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'CGO_ENABLED=0 go test ./internal/web/... && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`, as three `loom job run` calls.

Expected: the Go packages `ok`; `check` passes, its last step `node --check public/sw.js` included; `dist:check` prints `internal/web/dist matches the build.`

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the new app's worker lets the terminal page through to the network" -- internal/web/ui/public/sw.js internal/web/static_test.go internal/web/dist
```

### Task 8.5: The rules and the docs

`internal/web/CLAUDE.md` is 19,975 of 20,000 bytes once Story 7's rules are in (25 free): its `terminalCSP` rule needs about 260 bytes more than the mockup rule it extends, so this task first moves more supporting context out of rules into the guide's "Details moved out of CLAUDE.md" section (which Story 7's Task 7.5 added), changing no rule's meaning, and then rewrites the mockup rule to cover both pages. After it the file is 19,961 bytes, 39 under its budget.

**Files:**
- Modify: `internal/web/CLAUDE.md` (the rules quoted below)
- Modify: `internal/web/ui/CLAUDE.md` (a rule appended)
- Modify: `internal/web/README.md` (the `static.go` row, a paragraph)
- Modify: `docs/claude/web-layer.md` (bullets added to the section Story 7's Task 7.5 made)
- Test: `tools/claudemd` (`go test ./tools/claudemd/`)

- [ ] **Step 1: Make room, then add the rule**

In `internal/web/CLAUDE.md`, make each of these replacements (each quoted text occurs once, and none touches what Story 7's Task 7.5 replaced). They cut context out of the rules for the cookie (9), the host report (16), the sinks (18), the cookie's token (19), the daemon's stop (20) and the tree (31).

Rule 9, replace

```
(Secure, HttpOnly, SameSite=Strict, Path=/, the lifetime `cookieMaxAge`)
```

with

```
(Secure, HttpOnly, SameSite=Strict, Path=/)
```

Rule 16, replace

```
**Enforced** by `TestApplyWeb_OnlyWhatTheLayerDidIsReportedOff`, which calls `applyWeb` with a recording host; `TestApplyWeb_AWebJSONTheModelLeftAloneKeepsItsReason` pins the same through a real model.
```

with

```
**Enforced** by `TestApplyWeb_OnlyWhatTheLayerDidIsReportedOff` and `TestApplyWeb_AWebJSONTheModelLeftAloneKeepsItsReason`.
```

Rule 18, replace

```
stalls the drain, so the client's queues grow and every part stops
```

with

```
stalls the drain and every part stops
```

Rule 19, replace

```
makes every paired phone a stranger, every route answering 401 with nothing logged.
```

with

```
makes every paired phone a stranger.
```

Rule 20, replace

```
, and no request of a phone reaches a loop that is quiescing.
```

with

```
.
```

Rule 31, replace

```
Both apps share one origin: a shell cached
```

with

```
A shell cached
```

Then the mockup rule becomes the rule for both pages (the second quoted text is the rule's end as Story 7's Task 7.5 leaves it). Replace

```
**Replace `guard`'s Content-Security-Policy only on the mockup route (`internal/web/mockup.go`), only with `mockupCSP`, and answer that route's failures with `writePage`, never as JSON.**
```

with

```
**Replace `guard`'s Content-Security-Policy only on the mockup route (`internal/web/mockup.go`) with `mockupCSP`, answering its failures with `writePage` never as JSON, and only on `/terminal.html` (`serveTrees`, `internal/web/static.go`) with `terminalCSP`.**
```

and replace

```
in the app's iframe. **Enforced** by `TestMockup_ServesThePageSandboxed` and `TestMockup_ARefusalIsAPageThatSaysWhy`.
```

with

```
in the app's iframe; `terminalCSP` loosens only `style-src`, for the runtime styles xterm.js writes, so on another path it would let any inline style in. **Enforced** by `TestMockup_ServesThePageSandboxed`, `TestMockup_ARefusalIsAPageThatSaysWhy` and `TestStatic_TheTerminalPageAloneGetsItsOwnPolicy`.
```

In `docs/claude/web-layer.md`, directly after the bullet that begins `- **The message route:**` (the last bullet of the section `## Details moved out of CLAUDE.md`), insert these bullets, which hold what the cuts removed:

```markdown
- **The device cookie:** its lifetime is `cookieMaxAge`.
- **Reporting to the host:** `TestApplyWeb_OnlyWhatTheLayerDidIsReportedOff` calls `applyWeb` with a recording host; `TestApplyWeb_AWebJSONTheModelLeftAloneKeepsItsReason` pins the same through a real model.
- **The sinks:** a sink that blocks stalls the drain, so the client's queues grow.
- **The cookie's token:** hashing the cookie's text on one side and the bytes on the other makes every route answer 401 with nothing logged.
- **The daemon's stop:** closing the layer first also keeps every request of a phone from reaching a loop that is quiescing.
- **The trees:** both apps share one origin.
```

In `internal/web/ui/CLAUDE.md`, append after its last rule (the one that begins `- **Bind a document \`keydown\` listener only through \`usePaletteKey\``) this line:

```
- **Import xterm.js and its addons (`@xterm/xterm`, `@xterm/addon-webgl`, `@xterm/addon-fit`) only from the script of `terminal.html` (`internal/web/ui/src/terminal/main.ts`); that document is the one place a runtime `<style>` is allowed.** xterm.js appends `<style>` elements at run time that the app's `default-src 'self'` policy drops, so it runs in a document of its own that the web layer serves under `terminalCSP`, and an import from the app's entry would put it in every phone launch, running under the policy that blanks it. **Enforced** by `TestDist_XtermLoadsOnlyInTheTerminalPage`, which fails on xterm.js in a file the app's first load fetches or in any script the terminal page does not load; a runtime style elsewhere is **Convention** — a view that is unstyled only under the app's policy.
```

In `internal/web/README.md`, replace the `static.go` row's end

```
and each tree's shell list into its `sw.js`, as they are served |
```

with

```
and each tree's shell list into its `sw.js`, as they are served, and `terminalCSP` on `/terminal.html` alone (`serveTrees`) |
```

and insert before the line `## The new app's session views` this paragraph:

```markdown
The terminal's document, `/terminal.html`, is the second HTML entry of the build (`internal/web/ui/terminal.html`, an input of `vite.config.ts`) and the one place xterm.js runs. Its script (`src/terminal/main.ts`) makes a terminal with the WebGL addon loaded before `open()` (the DOM renderer serves when WebGL is unavailable or its context is lost) and the fit addon, reads the session's id from the hash (`/terminal.html#<id>`), opens the WebSocket at `api.terminalURL(id)` on the page's own host (`wss:` on an `https:` page), sends its size as the first frame (`src/terminal/socket.ts`), writes the daemon's binary frames into the terminal, sends its keys back in binary frames of at most 64 KiB (a paste is split, since the daemon ends a longer frame with 4006), and shows a close's reason in the page, with no reconnecting, no panel and no phone input. xterm.js appends `<style>` elements at run time, which the app's policy drops, so `routeStatic` answers `/terminal.html`, and only it, with `terminalCSP` (`style-src` allows `'unsafe-inline'`, scripts stay `'self'`, and `frame-ancestors 'self'` lets the app frame it); the new app's worker passes a navigation to it through to the network, which it would answer with the cached shell otherwise; and the page carries no `loom-build` meta, since the build is filled only into `index.html` and `sw.js`. `TestDist_XtermLoadsOnlyInTheTerminalPage` keeps xterm.js out of the app's first load. A device still on the classic app has the classic worker, which answers a navigation to `/terminal.html` with the classic shell, so the page is opened from the new app.
```

- [ ] **Step 2: Run the lint**

Run: `loom job run -d "claudemd lint and budgets" -- bash -c 'go run ./tools/claudemd -v | grep "internal/web" ; CGO_ENABLED=0 go test ./tools/claudemd/'`

Expected: the verbose listing shows `internal/web/CLAUDE.md` at 19,961 of 20,000 bytes (39 under) and `internal/web/ui/CLAUDE.md` well under its budgets, and the tests print `ok  	github.com/aidan-bailey/loom/tools/claudemd`: every test name the new rules cite resolves. If the first shows `internal/web/CLAUDE.md` over budget, Story 7's rules took more than its draft counted: move one more clause of a rule that no replacement above touches into the guide's section the same way.

- [ ] **Step 3: Run the story's gates**

Run: `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`, as three `loom job run` calls.

Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git commit -m "docs(web): the terminal page's rules, with room made in the package rules" -- internal/web/CLAUDE.md internal/web/ui/CLAUDE.md internal/web/README.md docs/claude/web-layer.md
```

## Story 9: End to end: a WebSocket client reaches a session's shell

- **Developer:** intermediate-dev
- **Files:** `e2e/web_terminal_test.go`, `e2e/CLAUDE.md`
- **Interfaces:** uses Story 4's `loom web terminal on`, Story 7's route, and Story 6's frames and vendored `github.com/coder/websocket`; exposes none
- **Verify:** `CGO_ENABLED=0 go test -tags e2e ./e2e/ -run TestE2E_Web_Terminal`
- **Review:** light
- **Form:** full (owner, 2026-11-26: the test is the story's only code, and its marker, read loop and dial headers are traps)

One `e2e` test over a real daemon on a private tmux server, written the way `e2e/web_test.go` drives the web layer: the web enabled on a loopback port, a device paired with the code `loom web pair` prints, a session created through the API. With the switch off the route answers 401 to a request with no cookie and 403 to the paired device; after `loom web terminal on` a WebSocket client with the device's cookie and the layer's origin sends a resize, types an `echo` whose output differs from its own text (a marker the shell computes, `LOOM$((20+22))` against `LOOM42`) and a carriage return, and reads the marker back from the binary frames; the session's `term_` shell is on the tmux server afterwards and stays there once the connection is closed. The test pins `SHELL=/bin/sh` first, because the daemon starts the shell from its own `$SHELL`, inherited from the first `loom` command the test runs. It dials with `github.com/coder/websocket`, which Story 6 vendors. No landmine marker appears in this story's code. Run the e2e command through `loom job run -d "<what>" --` (wrap it as `bash -c '…'`), as the session's rules say: the suite builds loom into its sandbox and runs the daemon there, never the user's.

### Task 9.1: The test

**Files:**
- Create: `e2e/web_terminal_test.go`

- [ ] **Step 1: Write the test**

The e2e package has the helpers this uses: `newSandbox`, `webCommand`, `freeLoopbackAddr`, `awaitListening`, `pairingCode`, `webCookie`, `webClient` (`pairWith`, `stream`, `call`), `awaitEvent`, `sessionsListing`, `hasSession`, `paneText` and `uiTimeout` (`e2e/web_test.go`, `e2e/clients_test.go`, `e2e/e2e_test.go`).

Create `e2e/web_terminal_test.go`:

```go
//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The web terminal end to end: a paired device's WebSocket reaches a session's worktree shell
// through a real daemon, as the app's terminal.html does. The route is refused while the host's
// switch is off and works once `loom web terminal on` turns it on.

// terminalEscapes matches the terminal escape sequences tmux draws around the shell's text, so
// the test reads the text alone: CSI sequences with any parameter and intermediate bytes (private
// markers `<`, `=`, `>` and `?` included), OSC strings ended by BEL or ST, charset selects and the
// single-character escapes.
var terminalEscapes = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78M]")

// terminalDialTimeout bounds one dial, well under the retry's window, so a slow answer from the
// model costs one attempt and not the whole window.
const terminalDialTimeout = 5 * time.Second

// terminalDial opens the terminal route of session id as the page does: with the device's cookie
// (none when token is empty) and the layer's origin. A refusal before the upgrade comes back as its
// status and body, with no connection.
func terminalDial(t *testing.T, addr, token string, id uint64) (conn *websocket.Conn, status int, body string) {
	t.Helper()
	header := http.Header{"Origin": {"http://" + addr}}
	if token != "" {
		header.Set("Cookie", (&http.Cookie{Name: webCookie, Value: token}).String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalDialTimeout)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/api/v1/sessions/%d/terminal", addr, id), &websocket.DialOptions{HTTPHeader: header})
	if resp != nil {
		status = resp.StatusCode
		if status != http.StatusSwitchingProtocols && resp.Body != nil {
			raw, _ := io.ReadAll(resp.Body)
			body = string(raw)
			_ = resp.Body.Close()
		}
	}
	if err == nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
		conn.SetReadLimit(1 << 20) // the daemon's history frames are up to 64 KiB; the library's default is 32 KiB
	}
	return conn, status, body
}

// awaitTerminalLive reads frames until the daemon's text frame says the attach is live, which
// follows the shell's history.
func awaitTerminalLive(ctx context.Context, t *testing.T, conn *websocket.Conn) {
	t.Helper()
	for {
		typ, data, err := conn.Read(ctx)
		require.NoError(t, err, "the terminal ended before it said it was live")
		if typ == websocket.MessageText {
			require.JSONEq(t, `{"state":"live"}`, string(data))
			return
		}
	}
}

// readTerminalUntil collects binary frames until their text, escapes removed, holds want, and
// returns that text.
func readTerminalUntil(ctx context.Context, t *testing.T, conn *websocket.Conn, want string) string {
	t.Helper()
	var raw bytes.Buffer
	text := ""
	for !strings.Contains(text, want) {
		typ, data, err := conn.Read(ctx)
		require.NoError(t, err, "no %q in the terminal's output: %q", want, text)
		if typ == websocket.MessageBinary {
			raw.Write(data)
			text = terminalEscapes.ReplaceAllString(raw.String(), "")
		}
	}
	return text
}

// A session is created over the API, and its terminal is refused while the switch is off: 401
// without the device's cookie, 403 with it. `loom web terminal on` opens it, and a client types a
// command into the shell and reads the shell's own output back. The shell is the tmux session
// term_<title>, which closing the connection leaves running.
func TestE2E_Web_TerminalReachesASessionsShell(t *testing.T) {
	// The daemon starts the shell from its own $SHELL, which it inherits from the first loom
	// command of the test: a developer's fish or zsh would change the prompt and the arithmetic.
	t.Setenv("SHELL", "/bin/sh")
	sb := newSandbox(t, "")
	addr := freeLoopbackAddr(t)
	web := &webClient{t: t, origin: "http://" + addr, client: &http.Client{Timeout: 45 * time.Second}}
	loomWeb := webCommand(t, sb)

	loomWeb("enable", "--listen", addr, "--origin", web.origin)
	awaitListening(t, addr)
	code := pairingCode.FindString(loomWeb("pair", "--name", "e2e-terminal"))
	require.NotEmpty(t, code, "loom web pair printed no code")
	web.pairWith(code)

	// A session, started over the API.
	events := web.stream()
	snapshot := awaitEvent(t, events, "snapshot", nil)
	var fleet struct {
		Workspaces []struct {
			ID   uint64 `json:"id"`
			Name string `json:"name"`
		} `json:"workspaces"`
	}
	require.NoError(t, json.Unmarshal([]byte(snapshot.Data), &fleet))
	var ws uint64
	for _, w := range fleet.Workspaces {
		if w.Name == devsandbox.WorkspaceName {
			ws = w.ID
		}
	}
	require.NotZero(t, ws, "the snapshot lists the workspace %s: %s", devsandbox.WorkspaceName, snapshot.Data)
	status, body := web.call(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": ws, "title": "webterm"})
	require.Equal(t, http.StatusCreated, status, string(body))
	var created struct {
		ID uint64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotZero(t, created.ID)
	awaitEvent(t, events, "sessions", sessionsListing(ws, "webterm", true, nil))
	require.Eventually(t, func() bool { return hasSession(sb, "webterm") }, uiTimeout, 100*time.Millisecond, "the agent's tmux session never started")
	require.Eventually(t, func() bool {
		return strings.Contains(paneText(t, sb, tmux.ToLoomTmuxName("webterm")), "commands: work N")
	}, uiTimeout, 100*time.Millisecond, "the fake agent came up")
	shell := tmux.TerminalSessionName("webterm")
	require.False(t, hasSession(sb, shell), "no shell exists before a terminal is opened")

	// The switch is off by default: the cookie is checked first (401), then the switch (403), and
	// the model is never asked for a shell.
	_, status, body = terminalDial(t, addr, "", created.ID)
	assert.Equal(t, http.StatusUnauthorized, status, "a request with no device cookie is refused before anything else: %s", body)
	_, status, body = terminalDial(t, addr, web.token, created.ID)
	assert.Equal(t, http.StatusForbidden, status, "the paired device is refused while the switch is off: %s", body)
	var refusal struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &refusal), body)
	assert.Equal(t, "the terminal is off on this host", refusal.Error)
	require.False(t, hasSession(sb, shell), "a refused request made no shell")

	// On: the same request upgrades. The model may still be loading the session, which it refuses
	// with a 409, so the dial is retried until it opens.
	out := loomWeb("terminal", "on")
	require.Contains(t, out, "The web terminal is on.")
	// A plain loop in the test's own goroutine: no dial outlives the window, so nothing writes
	// lastStatus or lastBody after the failure message reads them.
	var conn *websocket.Conn
	lastStatus, lastBody := 0, ""
	for deadline := time.Now().Add(uiTimeout); conn == nil; {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never opened: the last answer was %d %q", lastStatus, lastBody)
		}
		conn, lastStatus, lastBody = terminalDial(t, addr, web.token, created.ID)
		if conn == nil {
			time.Sleep(250 * time.Millisecond)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), uiTimeout)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"resize":{"cols":100,"rows":30}}`)))
	awaitTerminalLive(ctx, t, conn)

	// The typed command is echoed back as typed, so the marker is one only the shell can make:
	// the arithmetic is in the command and its result is in the output.
	require.NoError(t, conn.Write(ctx, websocket.MessageBinary, []byte("echo LOOM$((20+22))\r")))
	text := readTerminalUntil(ctx, t, conn, "LOOM42")
	assert.Contains(t, text, "LOOM42")
	assert.True(t, hasSession(sb, shell), "the session's term_ shell is on the tmux server")

	// The browser leaving ends the attach and never the shell.
	require.NoError(t, conn.CloseNow())
	require.Never(t, func() bool { return !hasSession(sb, shell) }, 2*time.Second, 100*time.Millisecond, "the shell outlives its connection")
	assert.True(t, hasSession(sb, "webterm"), "the agent's own session is untouched")
}
```

Fails without: TestE2E_Web_TerminalReachesASessionsShell: the `loomWeb("terminal", "on")` call removed (every dial stays 403 and the retry loop ends with `the last answer was 403`), the device's cookie left off the dial in the retry loop (401, never an upgrade), the `Origin` header dropped from `terminalDial` (the route's origin refusal, 403, never an upgrade), the `SHELL` pin removed on a machine whose `$SHELL` is fish (`$((…))` prints no `LOOM42` and the read times out with `no "LOOM42" in the terminal's output`), or the first dial's assertions inverted (a route open while the switch is off, or one that answers the paired device 401, fails the two refusal checks). The marker is computed on purpose: with `echo LOOM42` the pty's echo of the typed text would satisfy the wait and a connection that reaches no shell would pass.

- [ ] **Step 2: Run it to see it pass**

Run: `loom job run -d "e2e web terminal" -- bash -c 'CGO_ENABLED=0 go test -tags e2e ./e2e/ -run TestE2E_Web_Terminal'`

Expected: `ok  	github.com/aidan-bailey/loom/e2e` after the sandbox builds loom (a minute or so). It passes at once because Stories 1 to 7 are in the tree: this story's code is the test, so Step 3 shows it can fail. A `SKIP` (`tmux not installed`) means the machine cannot run the suite.

- [ ] **Step 3: Check that it can fail**

Delete the line `out := loomWeb("terminal", "on")` and the `require.Contains(t, out, …)` line under it, and run Step 2's command again.

Expected: FAIL: `the terminal never opened: the last answer was 403 "{\"error\":\"the terminal is off on this host\"}"` after the retry's 30 s. Restore the two lines and run it once more to see it pass.

- [ ] **Step 4: Commit**

```bash
git add -N e2e/web_terminal_test.go
git commit -m "test(e2e): a web terminal client reaches a session's shell" -- e2e/web_terminal_test.go
```

### Task 9.2: `e2e/CLAUDE.md` lists the file and its rule

`e2e/CLAUDE.md` names what each e2e file covers and the rules a test of this package must follow; the new file adds a line to the first and one rule to the second.

**Files:**
- Modify: `e2e/CLAUDE.md` (the coverage line after the `e2e/web_test.go` line, and a rule after the last one)
- Test: `tools/claudemd` (`go test ./tools/claudemd/`)

- [ ] **Step 1: Add the line and the rule**

In `e2e/CLAUDE.md`, after the line that begins `` `e2e/web_test.go` covers the web layer over HTTP `` (it ends `…and sees bare `localhost` and the IP literal refused.`), insert this line:

```
`e2e/web_terminal_test.go` covers the web terminal over a WebSocket, as the app's `terminal.html` uses it: with the switch off the route answers 401 to a request with no cookie and 403 to a paired device, `loom web terminal on` opens it, and a client with the device's cookie and the layer's origin sends a resize, types a command whose output the shell computes and reads that output back in binary frames, with the session's `term_` shell still on the tmux server after the connection closes.
```

and after the rule that begins `- **Put a stub \`gh\` first on \`PATH\``, insert this rule:

```
- **Pin `SHELL=/bin/sh` before the test's first `loom` command, and prove a terminal's output with a marker the shell computes.** The daemon starts a session's terminal shell from its own `$SHELL`, which it inherits from the first command that starts it (`sb.Cmd` copies the test's environment), so a developer's fish or zsh changes the prompt and the arithmetic; and the typed command is echoed as typed, so a marker sent verbatim passes on the echo with a dead shell. **Enforced** by `TestE2E_Web_TerminalReachesASessionsShell`, which sends `echo LOOM$((20+22))` and waits for `LOOM42`.
```

- [ ] **Step 2: Run the lint**

Run: `loom job run -d "claudemd lint" -- bash -c 'CGO_ENABLED=0 go test ./tools/claudemd/'`

Expected: `ok  	github.com/aidan-bailey/loom/tools/claudemd`: the file's size is within its budgets and the test name the rule cites resolves.

- [ ] **Step 3: Run the story's gate**

Run: `loom job run -d "e2e web terminal" -- bash -c 'CGO_ENABLED=0 go test -tags e2e ./e2e/ -run TestE2E_Web_Terminal'`

Expected: `ok  	github.com/aidan-bailey/loom/e2e`.

- [ ] **Step 4: Commit**

```bash
git commit -m "docs(e2e): list the web terminal test and the SHELL pin its marker needs" -- e2e/CLAUDE.md
```
