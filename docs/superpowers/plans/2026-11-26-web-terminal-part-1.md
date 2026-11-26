# Loom web terminal, part 1: the daemon side — Implementation Plan

> **For agentic workers:** this plan runs supervised: `/flow:draft` writes the stories from this skeleton, then `/flow:run` runs them story by story. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a paired device's browser reach a session's worktree shell through the daemon: a host-side switch, a Core request that makes the shell, a pty attach client in the daemon, a WebSocket route, and a bare `terminal.html` that shows the shell, so the owner can check the transport on the iPhone before the app's panel is designed.

**Architecture:** The model gains a `terminal` flag in `web.json` (`Core.SetWebTerminal`, published in `WebView`, set from `loom web terminal` and a settings row) and `Core.EnsureTerminal`, which starts the session's `term_<title>` tmux shell as a job and answers with its tmux name. `session/tmux` gains `ShellAttach`, a `tmux attach` in a pty with no emulator or status state. The web layer gains a part that upgrades `GET /api/v1/sessions/{id}/terminal` to a WebSocket (`github.com/coder/websocket`), pumps the attach's bytes through the alt-screen filter with a bounded buffer, and ends its connections when the switch turns off, a device is revoked, the listener rebinds or the layer stops. The app's build gains a second HTML entry, `terminal.html`, served under a CSP of its own, holding xterm.js and the socket. No `rpc.Protocol` bump: two methods, one Reply field and one view field are added.

**Tech Stack:** Go (testify, `creack/pty`, `github.com/coder/websocket` v1.8.15, tmux 3.x on a private server in tests); TypeScript, Vite 8, Vitest with happy-dom, `@xterm/xterm` 6.0.0 and `@xterm/addon-webgl` 0.19.0.

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
- A refusal's `Reply.Err` matches `core.ErrRefused` (Story 2), so the web layer's `modelStatus` answers it 409.

**The attach (Story 3)**, in `session/tmux/shell_attach.go`:

- `type ShellAttach struct` (opaque) (Story 3): one `tmux attach-session` client in a pty.
- `func StartShellAttach(name string, cols int, rows int, ptyFactory PtyFactory) (*ShellAttach, error)` (Story 3): `name` is the tmux session name as `Reply.Terminal` gives it; the pty has the given size before tmux draws.
- `func (a *ShellAttach) Read(p []byte) (int, error)` (Story 3): the client's raw output, unfiltered; `io.EOF` or an error once the client has ended (the session gone, or `Close`).
- `func (a *ShellAttach) Write(p []byte) (int, error)` (Story 3): input to the pane.
- `func (a *ShellAttach) Resize(cols int, rows int) error` (Story 3).
- `func (a *ShellAttach) Close() error` (Story 3): ends the client, never the session; a blocked `Read` returns; safe to call twice.
- `func ShellHistory(name string, cmdExec internalexec.Executor) ([]byte, error)` (Story 3): the rows above the pane's visible screen, at most `paneScrollbackLines`, with escapes, each row ended by `\r\n`; empty with no history.

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
- `type termOpen func(cols int, rows int) (termShell, []byte, error)` (Story 6): opens the attach at a size and returns the history to send before its output.
- `type termEnd struct { code websocket.StatusCode; reason string }` (Story 6): a close frame's code and reason.
- The endings (Story 6), each a `termEnd` variable: `termEndOff` (4001, `the terminal is off on this host`), `termEndRevoked` (4002, `this device is no longer paired`), `termEndSession` (4003, `the session was paused or killed`), `termEndExited` (4004, `the shell exited`), `termEndSlow` (4005, `the browser fell behind`), `termEndBadFrame` (4006, `a frame loom does not understand`), `termEndNoAttach` (4007, `loom could not attach to the shell`), `termEndStopping` (1001, `loom is stopping`).
- `type termConn struct` (opaque) (Story 6): one upgraded connection.
- `func newTermConn(ws *websocket.Conn, open termOpen, ended func() termEnd) *termConn` (Story 6): `ended` is asked once, when the attach's output ends by itself, for the ending to send (`termEndExited` or `termEndSession`).
- `func (c *termConn) run(ctx context.Context) termEnd` (Story 6): serves the connection until it ends and returns the ending it sent; an ending with no close frame from loom (the browser left, a network error) is the zero `termEnd`.
- `func (c *termConn) end(e termEnd)` (Story 6): ends the connection with `e`; never blocks, safe from any goroutine and more than once, the first ending winning.
- The frames (Story 6): binary frames carry terminal bytes both ways; the app's text frame `{"resize":{"cols":120,"rows":40}}`, the first of which opens the attach; the daemon's text frame `{"state":"live"}` once attached.
- The bounds (Story 6), constants in `terminal_conn.go`: `termFrameMax = 64 << 10`, `termMinCols = 2`, `termMaxCols = 500`, `termMinRows = 1`, `termMaxRows = 200`, `termPingEvery = 20 * time.Second`, `termPongWait = 10 * time.Second`, `termOutBuffer = 1 << 20`.

**The route (Story 7)**, in `internal/web/terminal.go`:

- The route `GET /api/v1/sessions/{id}/terminal` (Story 7), a `page` entry with log `terminal`, client `terminal` and ok 101; the generated `terminalURL(id: number): string` (Story 7) on the app's `api`, in `ui/src/api/gen.ts`.
- `const maxTerminalsPerDevice = 4` and `const maxTerminals = 16` (Story 7).
- The refusals before the upgrade (Story 7), each a JSON error: 403 `the request does not come from loom's origin`; 403 `the terminal is off on this host`; 429 `too many open terminals`; and the model's refusal as 409.
- The log lines (Story 7): `web.terminal_open` and `web.terminal_close`, each with `device` and `session`, the close with `reason`.

**The page (Story 8):**

- `const terminalCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"` (Story 8), in `internal/web/static.go`: set on `/terminal.html` and on no other path.
- The page `/terminal.html#<session id>` (Story 8), built from `internal/web/ui/terminal.html` and `internal/web/ui/src/terminal/main.ts`: it reads the session's id from the hash and connects to the URL Story 7's `terminalURL` builds for it, on the page's own host, `wss:` on an `https:` page and `ws:` otherwise.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): `core/iface.go`, `core/loop_core.go`, `core/rpc/methods_gen.go` and `docs/specs/protocol.md` as Story 1 leaves them (Story 2); `internal/web/routes.go` and `internal/web/web.go` as Story 5 leaves them (Story 7); `internal/web/ui/src/api/gen.ts` as Story 7 leaves it (Story 8).

**For every story:** each test's `Fails without:` line names a change to the code that the test, as written, really catches when run. A guard is pinned on both sides: the case it spares and the ordinary case it must still act on. No test touches the real `~/.loom`, daemon or tmux server: a real-tmux test runs on the package's private-server helper with a short socket path. Builds, test runs, `pnpm` and `go generate` go through `loom job`.

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

  A request naming an instance: admitted through a precondition that refuses an instance that is unstarted, paused, loading, being deleted, recoverable, a workspace terminal or without a worktree, then a job that starts `$SHELL` (else `/bin/sh`) in the worktree as the tmux session `tmux.TerminalSessionName(title)` when it does not exist, and a Reply whose `Terminal` is the session's tmux name. On delivery the model looks the instance up again by the id the result carries: when the job started a shell and the instance is gone or no longer started and active, it closes that shell by its exact name in a job of its own and the Reply is an error. A shell that existed before the request is never closed here. The Reply's new field crosses the wire.

  - **Developer:** intermediate-dev
  - **Files:** `core/terminal.go`, `core/terminal_test.go`, `core/iface.go`, `core/loop_core.go`, `core/requests.go`, `core/requests_test.go`, `core/model.go`, `core/events.go`, `core/events_json.go`, `core/rpc/methods_gen.go`, `core/rpc/wire_test.go`, `docs/specs/protocol.md`
  - **Interfaces:** exposes `Core.EnsureTerminal` and `Reply.Terminal` to Stories 7 and 9; uses Story 1's `core/iface.go`, `core/loop_core.go`, `core/rpc/methods_gen.go` and `docs/specs/protocol.md` as it leaves them
  - **Verify:** `CGO_ENABLED=0 go test ./core/ ./core/rpc/`
  - **Review:** deep
  - **Form:** full (a job racing a kill or pause, and a tmux session closed on delivery)
  - **Reading:** spec §9, §10, §11; `docs/claude/adding-a-core-method.md`; `core/job_log.go`; `core/requests.go:20-330`; `core/model.go:335-400,465-570`; `core/ops.go:30-130,195-300`; `core/completions.go:255-300`; `core/view.go:160-185`; `core/events.go:205-255`; `core/events_json.go:25-80`; `core/seams.go`; `ui/terminal.go:357-428`; `session/tmux/tmux.go:160-270`; `session/tmux/session.go:590-600`; `session/reconcile.go:262-305`
  - **Drafts after:** Story 1

- **Story 3** — `ShellAttach`: a pty attach client with nothing else.

  `StartShellAttach` runs `tmux attach-session` on the exact target in a pty from the package's `PtyFactory`, sized before tmux draws, and returns a value that reads, writes, resizes and closes; it holds no emulator, monitor or status state, and `Close` ends the client and never the session. `ShellHistory` captures the rows above the visible screen, bounded by `paneScrollbackLines`. Tests run against a pty pair, and one real-tmux test on the private server attaches, types, reads the echo, resizes and finds the session alive after `Close`. The package's rules gain: `ShellAttach.Close` never closes the session.

  - **Developer:** intermediate-dev
  - **Files:** `session/tmux/shell_attach.go`, `session/tmux/shell_attach_test.go`, `session/tmux/shell_attach_realtmux_test.go`, `session/tmux/CLAUDE.md`, `session/tmux/README.md`
  - **Interfaces:** exposes `ShellAttach`, `StartShellAttach` and `ShellHistory` to Story 7; uses none
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./session/tmux/ && GOOS=windows CGO_ENABLED=0 go build ./...'`
  - **Review:** deep
  - **Form:** full (a pty, a child process and a read that must end on close)
  - **Reading:** spec §12, §13, §14; `session/tmux/CLAUDE.md`; `session/tmux/attach.go`; `session/tmux/pty.go`; `session/tmux/pty_unix.go`; `session/tmux/pty_windows.go`; `session/tmux/command.go:60-120`; `session/tmux/target.go`; `session/tmux/tmux.go:340-360,780-805`; `session/tmux/session.go:735-815`; `session/tmux/scrollback.go`; `session/tmux/attached_test.go:10-50`; `session/tmux/realtmux_helpers_test.go`; `session/tmux/attach_test.go:95-140`; `session/tmux/target_enforce_test.go`

- **Story 4** — The host's switches: `loom web terminal` and the settings row.

  `loom web terminal on|off` reads the view, says when nothing changes, else sends `SetWebTerminal` and prints the model's refusal as it stands; `loom web status` gains its `Terminal:` row. The TUI's settings gain a `Web Terminal` row after Job Borrowing, a global setting like it: Enter raises a request the app answers with `SetWebTerminal`, the row is seeded when the overlay opens and refreshed on `ModelChanged`. `USAGE.md` documents both.

  - **Developer:** intermediate-dev
  - **Files:** `internal/cli/web.go`, `internal/cli/web_test.go`, `ui/overlay/settingsOverlay.go`, `ui/overlay/settingsOverlay_test.go`, `app/state_settings.go`, `app/state_settings_test.go`, `app/intents.go`, `app/core_glue.go`, `USAGE.md`
  - **Interfaces:** uses Story 1's `Core.SetWebTerminal` and `WebView.Terminal`; exposes none
  - **Verify:** `CGO_ENABLED=0 go test ./internal/cli/ ./ui/overlay/ ./app/`
  - **Review:** light
  - **Form:** brief (plumbing a flag through a command and a row that copy existing ones)
  - **Reading:** spec §6; `internal/cli/web.go:28-235`; `internal/cli/web_test.go:20-110,350-446`; `internal/cli/CLAUDE.md`; `ui/overlay/settingsOverlay.go:14-115,205-225,325-345,455-470`; `ui/overlay/settingsOverlay_test.go:190-235`; `app/state_settings.go`; `app/state_settings_test.go:170-275`; `app/intents.go:462-476`; `app/core_glue.go:160-185`; `USAGE.md:335-342,930-950,1405-1420`
  - **Drafts after:** Story 1

- **Story 5** — The snapshot's field and the `terminal` event.

  The stream's hub follows `WebView.Terminal`: the snapshot carries `terminal`, and a change is sent to every stream as the `terminal` event. The contract is regenerated (`gen.ts`, `web-api.md`, `api.fields`), the app's stream knows the event and its store sets the snapshot's flag from it, webshot's fixture snapshot gains the field, and the bundle is rebuilt. Nothing in the app draws it yet.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/fleet.go`, `internal/web/stream.go`, `internal/web/stream_test.go`, `internal/web/fleet_test.go`, `internal/web/api.fields`, `docs/specs/web-api.md`, `internal/web/README.md`, `internal/web/ui/src/api/gen.ts`, `internal/web/ui/src/api/stream.ts`, `internal/web/ui/src/api/stream.test.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/fleet.test.ts`, `tools/webshot/fixture/snapshot.json`, `internal/web/dist/**`
  - **Interfaces:** exposes `snapshotJSON.Terminal`, `terminalStateJSON`, the `terminal` event and the store's flag to part 2; uses Story 1's `WebView.Terminal`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/webshot/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
  - **Review:** light
  - **Form:** brief (a field and an event added by the web layer's documented procedure)
  - **Reading:** spec §7; `docs/claude/web-layer.md`; `internal/web/fleet.go:170-230,520-545`; `internal/web/stream.go:105-200,355-390,520-570`; `internal/web/CLAUDE.md`; `internal/web/ui/src/api/stream.ts:1-60`; `internal/web/ui/src/store/fleet.ts`; `internal/web/ui/CLAUDE.md`
  - **Drafts after:** Story 1

- **Story 6** — One terminal connection: frames, pump, pings and endings.

  The dependency arrives (`go get github.com/coder/websocket@v1.8.15`, `go mod vendor`), and `termConn` serves one upgraded connection: the first resize frame opens the attach through `termOpen`, the history goes out first, then the attach's output through `vt.NewAltScreenFilter` as binary frames, with the app's binary frames written to the attach and later resizes applied. Output passes through a bounded buffer, so a browser that falls behind is ended (`termEndSlow`) and the attach is never blocked on the network. A bad size, an oversized frame, unreadable JSON or bytes before the first resize end it (`termEndBadFrame`); a failed attach ends it (`termEndNoAttach`); a ping unanswered for `termPongWait` drops it; the attach's own end asks `ended`; `end` from any goroutine wins once. Every path closes the attach. Tests run a real WebSocket pair over `httptest` against a fake `termShell`.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/terminal_conn.go`, `internal/web/terminal_conn_test.go`, `go.mod`, `go.sum`, `vendor/**`
  - **Interfaces:** exposes `termShell`, `termOpen`, `termEnd`, the endings, `termConn`, `newTermConn`, `run`, `end` and the bounds to Story 7; uses none
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ && CGO_ENABLED=0 go build ./... && GOOS=windows CGO_ENABLED=0 go build ./...'`
  - **Review:** deep
  - **Form:** full (concurrency between two pumps, a bounded buffer and an outside connection)
  - **Reading:** spec §13, §16, §17, §18, §19; `session/vt/altfilter.go`; `internal/web/stream.go:19-110,575-650`; `internal/web/CLAUDE.md`; `internal/web/web.go:30-130`

- **Story 7** — The route and the terminals' part.

  A `page` entry in the route table answers `GET /api/v1/sessions/{id}/terminal`: it checks for exactly one accepted `Origin`, the switch, the session (404 for an unknown one or a workspace terminal) and the limits, asks the model for the shell (`EnsureTerminal` through the layer's reply wait, a refusal answered 409), then upgrades with the library's own origin check off and runs a `termConn` whose `termOpen` is `tmux.ShellHistory` and `tmux.StartShellAttach` and whose `ended` asks the replica whether the session is still started and active. The part counts connections per device and in total, logs each open and close and never the bytes, and, as a sink, ends connections with the right ending when the switch turns off or a device is revoked or re-paired; a rebind and the layer's stop end them all, before the listener closes. Its goroutines recover panics as the layer's others do. The contract is regenerated, `internal/web/CLAUDE.md` makes room by moving supporting context of existing rules into `docs/claude/web-layer.md` and gains the route's rules, and the guide gains "adding a WebSocket route".

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/terminal.go`, `internal/web/terminal_test.go`, `internal/web/routes.go`, `internal/web/web.go`, `internal/web/server.go`, `internal/web/api.fields`, `docs/specs/web-api.md`, `internal/web/ui/src/api/gen.ts`, `internal/web/dist/**`, `internal/web/CLAUDE.md`, `internal/web/README.md`, `docs/claude/web-layer.md`, `docs/ARCHITECTURE.md`
  - **Interfaces:** exposes the route and `api.terminalURL` to Stories 8 and 9; uses Story 2's `Core.EnsureTerminal` and `Reply.Terminal`, Story 3's `StartShellAttach` and `ShellHistory`, Story 5's `routes.go` and `web.go` as it leaves them, and Story 6's `termConn`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
  - **Review:** deep
  - **Form:** full (an origin check and an auth boundary on a route that executes commands, and connections ended from other goroutines)
  - **Reading:** spec §8, §15, §16, §18, §20, §21, §29, §30; `internal/web/CLAUDE.md`; `docs/claude/web-layer.md`; `internal/web/routes.go:20-250,340-390`; `internal/web/web.go:28-130,200-265`; `internal/web/server.go:24-215`; `internal/web/auth.go:20-100`; `internal/web/api.go:36-110`; `internal/web/reads.go:185-240`; `internal/web/mockup.go`; `internal/web/stream.go:105-250,310-330,420-545`
  - **Drafts after:** Story 6

- **Story 8** — The bare `terminal.html`.

  The app's build gains a second HTML entry whose script creates an xterm.js terminal with the WebGL addon loaded before `open()` (the DOM renderer when WebGL is unavailable or its context is lost), reads the session's id from the hash, opens the WebSocket, sends its size, writes binary frames into the terminal and its keys back, and shows a close's reason in the page; no reconnecting, no panel, no phone input. The web layer answers `/terminal.html`, and only it, with `terminalCSP`; the service worker passes a request for `/terminal.html` through to the network; the page carries no inline script and no `loom-build` meta. Go tests pin the header on that path and the app's on every other, and that nothing the app's entry loads holds xterm.js; Vitest covers the socket with a fake WebSocket. `internal/web/CLAUDE.md` and `internal/web/ui/CLAUDE.md` gain the rules.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/terminal.html`, `internal/web/ui/src/terminal/main.ts`, `internal/web/ui/src/terminal/socket.ts`, `internal/web/ui/src/terminal/socket.test.ts`, `internal/web/ui/src/terminal/terminal.css`, `internal/web/ui/vite.config.ts`, `internal/web/ui/package.json`, `internal/web/ui/pnpm-lock.yaml`, `internal/web/ui/public/sw.js`, `internal/web/static.go`, `internal/web/static_test.go`, `internal/web/static_budget_test.go`, `internal/web/dist/**`, `internal/web/CLAUDE.md`, `internal/web/ui/CLAUDE.md`, `internal/web/README.md`
  - **Interfaces:** exposes `/terminal.html` and `terminalCSP` to Story 9 and part 2; uses Story 7's `api.terminalURL` and Story 6's frames and endings
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/... ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
  - **Review:** deep
  - **Form:** full (a CSP relaxed on one path only, and a service worker's fetch handler)
  - **Reading:** spec §17, §23, §24, §29; `internal/web/static.go`; `internal/web/static_test.go:95-160,215-250`; `internal/web/static_budget_test.go`; `internal/web/mockup.go:10-45`; `internal/web/ui/vite.config.ts`; `internal/web/ui/index.html`; `internal/web/ui/public/sw.js`; `internal/web/ui/src/styles/app.css:1-80`; `internal/web/ui/src/styles/fonts.css`; `internal/web/ui/scripts/check-dist.mjs`; `internal/web/ui/CLAUDE.md`
  - **Drafts after:** Story 7

- **Story 9** — End to end: a WebSocket client reaches a session's shell.

  One `e2e` test over a real daemon on a private tmux server: the web enabled on a loopback port, a device paired, a session started; with the switch off the route answers 403; after `SetWebTerminal(true)` a WebSocket client with the device's cookie and the layer's origin sends a resize, types `echo` of a marker and a carriage return, and reads the marker back in a binary frame; the session's `term_` shell exists on the tmux server afterwards.

  - **Developer:** intermediate-dev
  - **Files:** `e2e/web_terminal_test.go`
  - **Interfaces:** uses Story 1's `Core.SetWebTerminal`, Story 7's route and Story 6's frames; exposes none
  - **Verify:** `CGO_ENABLED=0 go test -tags e2e ./e2e/ -run TestE2E_Web_Terminal`
  - **Review:** light
  - **Form:** brief (a test of existing behaviour over the e2e package's helpers)
  - **Reading:** spec Testing (End to end), §16, §17; `e2e/CLAUDE.md`; `e2e/web_test.go`; `e2e/e2e_test.go`
  - **Drafts after:** Story 7

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
