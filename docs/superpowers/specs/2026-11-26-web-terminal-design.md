# Loom web: the terminal

Date: 2026-11-26. Branch `aidanb/terminal-in-app`. The first sub-project of the web programme's TUI parity series (`docs/superpowers/specs/2026-11-18-web-foundation-design.md`, The programme), taken before sub-project 2 (Phone) by the owner's choice. A session's worktree shell in the new app, on the desktop and the phone: two Core methods (compatible, no `rpc.Protocol` bump), one WebSocket route under `/api/v1`, one new Go dependency, a new field in `web.json`, and a panel.

## Problem

The owner wants a terminal in loom web: a shell in a session's worktree where they type commands, from a desktop browser or the phone. Today the web shows what Claude said and did (the conversation, the changes, the jobs) and can prompt Claude, but running `git status`, a test or an editor in a session's worktree needs the TUI on the host.

## Decisions by the owner (2026-11-26)

From the brainstorm:

- **"Terminal" means a shell, not the agent's pane.** A pty where the owner types commands. Streaming the agent's TUI to the web has no use: the conversation view covers the agent.
- **The shell is the one the TUI already has:** the session's terminal pane, the tmux session `tmux.TerminalSessionName(title)` (`term_<title>`), shared with the TUI, kept by tmux across a reload, a sleeping phone and a daemon restart.
- **Desktop and phone in this sub-project.** The desktop types straight into the terminal. The phone reads the terminal and types into a command line below it, with a row of keys the on-screen keyboard lacks.
- **A host-side switch, off by default.** The web faces the internet through the tunnel, and a shell is command execution as the owner with no permission mode between. The switch is set only on the host, never from the web.
- **A pty attach client per browser connection, over a WebSocket,** as the foundation spec foresaw (its Decisions that shape this spec).
- **The TUI's keys go last** in the parity series, once the features they bind to exist.

## Findings

From the repo at `b6b67817` and the dev machine, 2026-11-26:

- **The daemon has no pane byte stream.** The TUI reaches each pane by running `tmux attach` in a pty of its own (`tmux.NewAttachClient`, `session/tmux/attach.go`) and pumping the output into an emulator; `Core.PaneOutput` and `Core.PaneQuiet` are casts from the TUI to the model. Nothing in the daemon reads a pane continuously.
- **The terminal pane's shell** is created by the TUI, lazily, in `TerminalPane.ensureSessionLocked` (`ui/terminal.go`): `$SHELL` (else `/bin/sh`) in the session's worktree, as the tmux session `tmux.TerminalSessionName(title)`, only for a started, active, unpaused instance with a worktree. Its comment records a race: a shell started while a kill or pause is ending the old one outlives the row. The instance closes the shell by name on pause and kill (`session/reconcile.go`, `CloseRelatedSession`).
- **Scrollback.** tmux clients enter the alternate screen at attach and never leave it, so an emulator fed an attach client's bytes keeps no history. The TUI writes the pump through `vt.NewAltScreenFilter` (`session/vt/altfilter.go`) and seeds the emulator with the pane's history (`TmuxSession.SeedHistory`).
- **Size.** Loom sets no `window-size` option, so tmux's default applies: `latest` since tmux 3.1 (the host runs 3.7c), the window taking the size of the client used last.
- **The web layer** (`internal/web`) runs in the daemon's process, which has pinned the daemon's tmux server. `guard` (`internal/web/server.go`) checks the `Origin` header only on requests that are neither GET nor HEAD; a WebSocket upgrade is a GET. `authed` (`internal/web/auth.go`) resolves the device from its cookie. The event stream (`internal/web/stream.go`) is hand-registered, limits streams per device and in total (`maxStreamsPerDevice`, `maxStreams`), and ends a revoked device's streams (`hub.closeDevice`).
- **The CSP** is `default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`, with no `connect-src`, so `default-src` governs a WebSocket. The mockup route answers under a CSP of its own (`mockupCSP`, `internal/web/mockup.go`).
- **xterm.js's default renderer breaks that CSP.** Its DOM renderer appends `<style>` elements at runtime (`_themeStyleElement`, `_dimensionsStyleElement` in `DomRenderer.ts`) and sets `style` attributes with `setAttribute` (`DomRendererRowFactory.ts`), with no nonce support. The WebGL addon draws to a canvas, but a terminal is created on the DOM renderer before an addon replaces it. Not yet tried under the CSP (the spike).
- **Dependencies.** `go.mod` has `github.com/creack/pty` and no WebSocket library. Modules are vendored for the Nix build.
- **Web config.** `web.json` (`core/web_store.go`, `webFile`) holds `listen`, `origin`, the VAPID key, the devices and the quick replies; `Core.ConfigureWeb`, `PairDevice` and `RevokeDevice` change it, and `WebView` publishes it. `loom web` has `enable`, `disable`, `status`, `pair`, `devices`, `revoke` and `open`.
- **The app.** A view is a panel (`definePanel`); a session's tabs and the desktop inspector's tabs both come from the panels that declare `tab`. Lazy chunks stay out of the entry budget, and a guard lists them. Workspace terminals are left out of the web's fleet.

## Decisions

### Scope

1. **In scope:** the worktree shell of a session, on the desktop and the phone, in the new app only (the classic app is frozen).
2. **Out of scope:** the agent's pane; workspace terminals; a shell for a paused session (pause closes it, as in the TUI); the TUI's keys; more than one shell per session; file transfer; the TUI's own creation path, which stays as it is.
3. **Done when** the spike has passed (The spike), a paired desktop browser and the iPhone each run a command in a session's shell and see the same shell the TUI shows, every refusal below is tested, webshot renders the terminal screens with no console error or CSP violation, and the owner's checklist passes.

### The switch

4. **`web.json` gains `terminal`** (a boolean, `omitempty`, absent meaning off). An older build that rewrites the file drops it, which turns the terminal off: the failure is closed.
5. **`Core.SetWebTerminal(on bool) error`** sets it and writes `web.json`, refused as `ConfigureWeb` is while the file is left alone. `WebView` gains `Terminal bool`. Adding a method and a field is compatible: no `rpc.Protocol` bump.
6. **Set only on the host:** `loom web terminal on|off`, a line in `loom web status`, and a row in the TUI's settings. No route under `/api/v1` changes it, so a stolen device cookie cannot turn it on.
7. **The app learns it from the snapshot** (`terminal`, a boolean) and from a stream event `terminal` (`{ "enabled": bool }`) sent to every stream when it changes. Both are new lines in `api.fields`.
8. **Turning it off closes every open terminal connection** with the refusal of §16.

### Creating the shell

9. **`Core.EnsureTerminal(id InstanceID, req ReqID)`** answers with the Reply's `Terminal`, the exact tmux session name. The model runs the tmux work as a job: when the session `tmux.TerminalSessionName(title)` does not exist it starts `$SHELL` (else `/bin/sh`) in the instance's worktree, as the TUI does.
10. **The model refuses** an instance that is not started, is paused, has no worktree, is a workspace terminal, or is being killed or paused, and one whose kill or pause begins while the job runs gets no shell left behind: the model checks again on delivery and closes a shell it started for a row that is no longer active. This closes the race the TUI's comment records, for this path.
11. **The TUI and the web share the shell** because both name it `tmux.TerminalSessionName(title)`. Pause and kill close it as today.

### Attaching

12. **A unit of its own, `session/tmux`'s `ShellAttach`** (a new file there): given an exact session name and a size it starts `tmux attach` on `tmux.SessionTarget(name)` in a pty, built with `tmux.Command` and the package's `PtyFactory`, and returns a value with `Read`, `Write`, `Resize(cols, rows)` and `Close`. It holds no emulator, no monitor and no status state: it is not a `TmuxSession`. `Close` ends the client and never the session.
13. **Scrollback:** output passes through `vt.NewAltScreenFilter`, and on connect the web layer first sends the pane's history (a `capture-pane` with escapes over the pane's history, at most `paneScrollbackLines` lines), so xterm.js keeps real scrollback that touch scrolls.
14. **Size:** the browser's size is the pty's size, and tmux's `latest` rule applies. Using the shell in the browser resizes it to the browser; the TUI shows it cropped or padded until it is used there again. Loom sets no tmux option for this.

### The route

15. **`GET /api/v1/sessions/{id}/terminal`** upgrades to a WebSocket. It is hand-registered beside the stream, behind `authed`, with a path-only entry in the route table so the generated client builds its URL (as the mockup route has). The library is `github.com/coder/websocket`, vendored.
16. **It refuses before upgrading:** an `Origin` header that is not one the layer's origin rule accepts (403; the route checks it itself, since `guard` does not for a GET); an unpaired device (401); the switch off (403, "the terminal is off on this host"); an unknown session (404); and whatever `EnsureTerminal` refuses (409 with the model's text). After the upgrade, an ending is a close frame with a reason the app shows: the switch turned off, the device revoked, the session paused or killed, the shell exited, or the daemon stopping.
17. **Frames:** binary frames carry terminal bytes both ways. Text frames carry JSON control messages: from the app `{ "resize": { "cols": n, "rows": n } }`, the first of which opens the attach at that size; from the daemon `{ "state": "live" }` once attached. Sizes outside 2–500 columns or 1–200 rows are refused. A frame from the app is capped at 64 KiB.
18. **Limits:** at most 4 terminals per device and 16 in total, refused with 429 before upgrading. A connection silent for 45 s is pinged, and dropped when the ping goes unanswered.
19. **A slow browser never blocks tmux.** Each connection has a bounded output buffer; when it fills, the connection closes with a reason and the app reconnects, repainted by tmux.
20. **Logging:** each open and close is logged through `log.For("web")` with the device's name, the session's ID and the close's reason. Terminal bytes are never logged.
21. **A device revoked, or the daemon stopping, closes its terminals** as it ends its streams.

### The panel

22. **A `terminal` panel** (`src/panels/terminal/`), a session tab after Changes, labelled "terminal". It applies to a session when the snapshot's `terminal` is true and the session is started, active and not paused, so no tab shows otherwise. The desktop inspector lists it from the same registry.
23. **xterm.js is a lazy chunk of the panel's**, with an entry in the lazy-chunk guard, so the entry budget does not count it. Its stylesheet is imported statically. The renderer is the one the spike proves under the CSP.
24. **Theme and type** come from the design tokens: JetBrains Mono, the dark palette, the `--attention` cursor.
25. **One connection per shown panel** (`src/panels/terminal/socket.ts`): it opens when the panel mounts, sends the size, writes binary frames into xterm.js, and closes when the panel unmounts. It does not connect while the snapshot is stale or the link is offline.
26. **Reconnecting:** a connection that drops without a refusal reconnects on the stream's backoff (1 s to 30 s) and whenever the page becomes visible. A refusal (§16) shows its reason and does not reconnect. "The shell exited" shows a "Start a new shell" button, which connects again.
27. **Desktop input:** keys go to the terminal while it has focus; ⌘K still opens the palette. Copy takes xterm.js's selection, paste goes in as bracketed paste when the shell asked for it. A resize of the panel sends the new size, debounced by 100 ms.
28. **Phone input:** below 1024px the terminal view takes no keyboard focus: touch scrolls and selects. Below it sits a command line, a 16px text field with autocorrect, capitalisation and spellcheck off, whose Enter sends its text and a carriage return (a bare one when empty) and clears it. Above the field, a row of keys, each sending its bytes at once: Ctrl-C, Ctrl-D, Tab, Esc and the four arrows. The footer hides while the field has focus, as for the reply box.

### Docs and rules

29. **`internal/web/CLAUDE.md`** gains: the terminal route checks `Origin` itself and refuses while the switch is off; terminal bytes are never logged. **`internal/web/ui/CLAUDE.md`** gains: xterm.js loads only in the terminal panel's chunk. **`session/tmux/CLAUDE.md`** gains: `ShellAttach.Close` never closes the session. Each within its budget (`go run ./tools/claudemd -v` first).
30. **`USAGE.md`** documents `loom web terminal`, the settings row and the panel; **`internal/web/README.md`** the route, its frames and limits; **`docs/claude/web-layer.md`** how to add a WebSocket route; **`docs/ARCHITECTURE.md`** the daemon's attach clients.

## Errors and edge cases

- **The switch is off:** no tab; a direct link to the route's panel says the terminal is off on this host.
- **The session is paused or killed while a terminal is open:** the shell ends, the connection closes with that reason, and the tab goes when the snapshot says so.
- **The shell exits** (`exit`, Ctrl-D): "The shell exited", with "Start a new shell" (§26).
- **Two browsers and the TUI on one shell:** all see the same screen at the size of the client used last (§14).
- **The daemon restarts:** the connection closes, the app shows RESTARTING as for the stream, and reconnects to the same shell, which tmux kept.
- **A daemon older than the app** has no route: the upgrade fails with 404 and the panel says the host's loom has no terminal.
- **A flood of output** (`yes`): the buffer fills and the connection closes and reconnects (§19); tmux is never blocked.
- **`web.json` left alone** (unreadable): `SetWebTerminal` is refused and the terminal stays off.

## Testing

- **Go, the model:** `SetWebTerminal` writing and publishing, and refused while the file is left alone; `EnsureTerminal` for each status, both sides of each guard (a started active instance gets a shell; unstarted, paused, no worktree, workspace terminal and mid-kill do not), and the shell closed when a kill begins while the job runs.
- **Go, the attach unit:** against a fake pty (start, read, write, resize, close ending the client only); one real-tmux test on a private server with a short socket path: attach, type, read the echo, resize, and the session still alive after `Close`.
- **Go, the route:** each refusal of §16 before the upgrade; the limits; the frame cap and the size bounds; switch-off, revocation and the layer's close ending live connections with their reasons; the full buffer closing the connection; nothing of the bytes in the log.
- **Vitest:** the socket's state machine with a fake WebSocket (connecting, live, each refusal, reconnect and its backoff, visible again); the command line and each key's bytes; `appliesTo` across statuses and the switch; the resize debounce.
- **webshot:** a `terminal` screen on the phone and both desktop sizes, the fixture server answering the route with canned bytes, under the real CSP.
- **The owner's checklist:** `loom web terminal on`; a command in a session's shell from the desktop browser and from the iPhone through the tunnel; the same shell in the TUI; lock and unlock the phone and find the shell as it was; `exit` and start a new one; `loom web terminal off` closing both; revoke a device with a terminal open.

## The spike

Before the plan is written, in a throwaway page outside the worktree:

1. **A renderer under the CSP** with no violation in the console: xterm.js with the WebGL addon first; if the DOM renderer's first moments violate it, the terminal in a frame served under a CSP of its own, as the mockup is. The result fixes §23.
2. **Safari on the iPhone opening a same-origin `wss:` connection** under `default-src 'self'`. If it refuses, the CSP gains `connect-src 'self' wss://<origin's host>`.
3. **Touch scrolling and selection** in xterm.js on iOS with the keyboard unfocused (§28).
4. **A WebSocket through the tunnel**, held open for 10 minutes with only pings.

Items 2 to 4 need the owner's iPhone. A failed item changes the spec before any plan.

## Delivery

An epic of 2 parts, each a plan merged to main before the next part is designed.

1. **Part 1: the daemon side.** The switch (§4–§8) with its CLI and settings row; `EnsureTerminal` (§9–§11); the attach unit (§12–§14); the dependency and the route (§15–§21); the snapshot field and the stream event; the Go tests; the rules and docs for these (§29–§30). It ends with a WebSocket client reaching a session's shell through a paired device's cookie.
2. **Part 2: the app.** The panel, the socket and both input modes (§22–§28); the fixture's canned route and the webshot screens; Vitest; the app's rules and docs; the owner's checklist.
