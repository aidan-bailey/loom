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
- **xterm.js's default renderer breaks that CSP.** Its DOM renderer appends `<style>` elements at runtime (`_themeStyleElement`, `_dimensionsStyleElement` in `DomRenderer.ts`) and sets `style` attributes with `setAttribute` (`DomRendererRowFactory.ts`), with no nonce support. Measured on 2026-11-26 (the spike, item 1) with `@xterm/xterm` 6.0.0 and `@xterm/addon-webgl` 0.19.0 in headless Chromium under the layer's exact CSP: the DOM renderer raised 8 `style-src-elem` violations, the WebGL addon loaded after `open()` 8, and the WebGL addon loaded before `open()` still 2 (one `<style>` element of the core's remains). No renderer runs clean. The same page in a same-origin frame served with `style-src 'self' 'unsafe-inline'` raised none, in the frame or its parent.
- **Dependencies.** `go.mod` has `github.com/creack/pty` and no WebSocket library. Modules are vendored for the Nix build.
- **Web config.** `web.json` (`core/web_store.go`, `webFile`) holds `listen`, `origin`, the VAPID key, the devices and the quick replies; `Core.ConfigureWeb`, `PairDevice` and `RevokeDevice` change it, and `WebView` publishes it. `loom web` has `enable`, `disable`, `status`, `pair`, `devices`, `revoke` and `open`.
- **The service worker** (`internal/web/ui/public/sw.js`) answers a navigation, a frame's included, from the cache under the key `/`, so a load of `/terminal.html` on a controlled page would get the app's shell. `buildStaticAssets` fills `__LOOM_BUILD__` only into `/index.html` and `/sw.js`, and `guard` sets the app's CSP before any handler, which a handler may overwrite (the mockup does).
- **webshot's canned loom** serves every file under the app's CSP and answers no WebSocket; an API route it cannot answer fails the run.
- **The app.** A view is a panel (`definePanel`); a session's tabs and the desktop inspector's tabs both come from the panels that declare `tab`. Lazy chunks stay out of the entry budget, and a guard lists them. Workspace terminals are left out of the web's fleet.

## Decisions

### Scope

1. **In scope:** the worktree shell of a session, on the desktop and the phone, in the new app only (the classic app is frozen).
2. **Out of scope:** the agent's pane; workspace terminals; a shell for a paused session (pause closes it, as in the TUI); the TUI's keys; more than one shell per session; file transfer; the TUI's own creation path, which stays as it is.
3. **Done when** the spike's checks have passed (The spike), a paired desktop browser and the iPhone each run a command in a session's shell and see the same shell the TUI shows, every refusal below is tested, webshot renders the terminal screens with no console error or CSP violation, and the owner's checklist passes.

### The switch

4. **`web.json` gains `terminal`** (a boolean, `omitempty`, absent meaning off). An older build that rewrites the file drops it, which turns the terminal off: the failure is closed.
5. **`Core.SetWebTerminal(on bool) error`** sets it and writes `web.json`, refused as `ConfigureWeb` is while the file is left alone. `WebView` gains `Terminal bool`. It does not bump `WebView.Rev`: the layer rebinds its listener and ends every stream when `Rev` changes, and only `ConfigureWeb` bumps it. Setting the value it already has writes nothing. Adding a method and a field is compatible: no `rpc.Protocol` bump.
6. **Set only on the host:** `loom web terminal on|off` (it prints `The web terminal is on.` or `The web terminal is off.`, and `The web terminal is already on.` or `… already off.` when nothing changes), a `Terminal:` row (`on` or `off`) in `loom web status`, and a `Web Terminal` row in the TUI's settings after Job Borrowing, a global setting like it, showing `on (all workspaces)` or `off (all workspaces)` and refreshed when another client changes it. No route under `/api/v1` changes it, so a stolen device cookie cannot turn it on.
7. **The app learns it from the snapshot** (`terminal`, a boolean) and from a stream event `terminal` (`{ "enabled": bool }`) sent to every stream when it changes. Both are new lines in `api.fields`.
8. **Turning it off closes every open terminal connection** with the refusal of §16.

### Creating the shell

9. **`Core.EnsureTerminal(id InstanceID, req ReqID)`** answers with the Reply's `Terminal`, the session's name as tmux knows it (`tmux.ToLoomTmuxName(tmux.TerminalSessionName(title))`), ready for `tmux.SessionTarget`. The model runs the tmux work as a job: when the session `tmux.TerminalSessionName(title)` does not exist it starts `$SHELL` (else `/bin/sh`, read in the daemon's environment) in the instance's worktree, through the lifecycle's `tmux.Session` (`NewSession` and `Start`), which launches without attaching a client. A session of that name that already exists is the instance's own only when it started in the instance's worktree, the rule pause and kill judge the shell by (`session/reconcile.go`): one held elsewhere (another workspace's same-titled session, or an agent session whose title begins `term_`), or a tmux that cannot answer, is a refusal, and nothing is attached. `tmux.ErrSessionExists` from the start means another client made the shell meanwhile: the job answers as for one that existed, having started nothing. Any other error after the launch leaves the shell possibly running, and the job says so.
10. **The model refuses** an instance that is not started, is paused, has no worktree, is a workspace terminal, or is being killed or paused, and one whose kill or pause begins while the job runs gets no shell left behind: the model checks again on delivery, by the instance the result carries, and closes a shell the job started for a row that is no longer started and active, with a `kill-session` on the exact name (`tmux.SessionTarget`), in a job that first confirms the session still starts in that instance's worktree. A shell the job only possibly started (§9) is treated as started when it is alive and starts there. A shell that existed before the request is never closed by this path: pause and kill own that. This closes the race the TUI's comment records, for this path.
11. **The TUI and the web share the shell** because both name it `tmux.TerminalSessionName(title)`. Pause and kill close it as today.

### Attaching

12. **A unit of its own, `session/tmux`'s `ShellAttach`** (a new file there): given an exact session name and a size it starts `tmux attach` on `tmux.SessionTarget(name)` in a pty, built with `tmux.Command` and the package's `PtyFactory`, and returns a value with `Read`, `Write`, `Resize(cols, rows)` and `Close`. It holds no emulator, no monitor and no status state: it is not a `TmuxSession`. `Close` ends the client and never the session.
13. **Scrollback:** output passes through `vt.NewAltScreenFilter`, and on connect the web layer first sends the pane's history (a `capture-pane -p -e` over the rows above the visible screen, from `-S -<paneScrollbackLines>` to `-E -1`, its rows joined by `\r\n`, followed by as many line feeds as the screen has rows, so the history is in xterm.js's scrollback before tmux draws the screen; it is sent before the pump starts, outside §19's buffer, in frames of at most 64 KiB), so xterm.js keeps real scrollback that touch scrolls.
14. **Size:** the browser's size is the pty's size, and tmux's `latest` rule applies. Using the shell in the browser resizes it to the browser; the TUI shows it cropped or padded until it is used there again. Loom sets no tmux option for this.

### The route

15. **`GET /api/v1/sessions/{id}/terminal`** upgrades to a WebSocket. It is one `page` entry in the route table, as the mockup route is (log `terminal`, client `terminal`, ok 101), so registration wraps it in `authed` and the generated client builds its URL (`api.terminalURL(id)`). The library is `github.com/coder/websocket` v1.8.15, added with `go get` and `go mod vendor` (`vendor/` is committed, and Nix builds from it).
16. **It refuses before upgrading:** anything but exactly one `Origin` header that the layer's origin rule accepts (403, `the request does not come from loom's origin`; the route checks it itself, since `guard` does not for a GET, and the library's own origin check is switched off in its favour); an unpaired device (401); the switch off (403, "the terminal is off on this host"); an unknown session (404); and whatever `EnsureTerminal` refuses (409 with the model's text). After the upgrade, an ending is a close frame with a code and a reason the app shows: 4001 `the terminal is off on this host`; 4002 `this device is no longer paired`; 4003 `the session was paused or killed`; 4004 `the shell exited`; 4005 `the browser fell behind` (§19); 4006 `a frame loom does not understand` (a bad size, an oversized frame, JSON it cannot read, or bytes before the first resize); 4007 `loom could not attach to the shell`; and 1001 `loom is stopping` (the daemon stopping, or the listener rebinding). When the attach ends, the route asks the replica: a session still started and active means the shell exited (4004), anything else 4003. The app (part 2) reconnects after 1001, 4005 and an ending with no close frame, and after no other.
17. **Frames:** binary frames carry terminal bytes both ways. Text frames carry JSON control messages: from the app `{ "resize": { "cols": n, "rows": n } }`, the first of which opens the attach at that size; from the daemon `{ "state": "live" }` once attached. Sizes outside 2–500 columns or 1–200 rows are refused. A frame from the app is capped at 64 KiB: the daemon lifts the library's own read limit above the cap and ends a longer frame itself (4006), and the page sends its input, a paste included, in binary frames of at most 64 KiB.
18. **Limits** count open connections, never shells, and a connection counts from before the model is asked for the shell: the route reserves a slot under the terminals' part's lock, which refuses when the part is closed, the switch is off (the part's own copy, kept by its sink), the device is unknown, or the counts are full; a refusal after that releases the slot. Whatever ends connections (the switch, a device revoked or paired again, a rebind, the layer's stop) ends reserved slots too, and a connection whose slot was ended while it waited closes with that ending as soon as it has upgraded. The counts: at most 4 connections per device and 16 in total, refused with 429 before upgrading. A connection exists only while a terminal panel is on screen (§25), so the owner closes nothing by hand, and a session's shell stays in tmux whether or not any connection shows it. The daemon pings each connection every 20 s, as the stream does, and drops one whose ping goes unanswered for 10 s.
19. **A slow browser never blocks tmux.** Each connection has a bounded buffer for the attach's output (1 MiB; the history of §13 is outside it); when it fills, the connection closes with a reason and the app reconnects, repainted by tmux.
20. **Logging:** each open and close is logged through `log.For("web")` with the device's name, the session's ID and the close's reason. Terminal bytes are never logged.
21. **A device revoked, the daemon stopping, or the listener rebinding (`ConfigureWeb`) closes the terminals it touches** as it ends the streams. `http.Server.Close` does not track a hijacked connection, so the terminals' own part ends them, and it is stopped with the layer's other parts, before the listener closes. Its goroutines recover panics as the layer's others do.

### The panel

22. **A `terminal` panel** (`src/panels/terminal/`), a session tab after Changes, labelled "terminal". It applies to a session when the snapshot's `terminal` is true and the session is started, active and not paused, so no tab shows otherwise. The desktop inspector lists it from the same registry.
23. **xterm.js runs in a document of its own, in a frame** (the spike, item 1: no renderer runs under the app's CSP). The app's build gains a second HTML entry, `terminal.html`, whose script holds xterm.js (`@xterm/xterm` 6.0.0), the WebGL addon (`@xterm/addon-webgl` 0.19.0) and the socket (§25); the main bundle never imports them, so the entry budget does not count them, and a test pins that. The web layer answers `terminal.html` and nothing else with `terminalCSP`: `default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'`, set as `mockupCSP` is. Scripts stay `'self'` there: only styles are relaxed. The panel draws the frame (`<iframe src="/terminal.html#<session id>">`) and, being same-origin, drives it through one typed interface the frame's window exposes (`send(bytes)`, `reconnect()`, `focus()`, and a state callback), never through its DOM. The WebGL addon is loaded before `open()`; when WebGL is unavailable or its context is lost, the DOM renderer serves, which the frame's CSP allows. The app's own CSP does not change. The service worker answers every navigation with the cached shell (`/`), a frame's load included, so its `fetch` handler passes a request for `/terminal.html` through to the network, which answers it with `terminalCSP`; the page's script and styles still come from the cache by path. `terminal.html` carries no `loom-build` meta: `buildStaticAssets` fills the build only into `index.html` and `sw.js`.
24. **Theme and type** come from the design tokens: JetBrains Mono, the dark palette, the `--attention` cursor.
25. **One connection per shown panel**, held by the frame's script (`src/terminal/socket.ts`): it opens when the panel mounts its frame, sends the size, writes binary frames into xterm.js, and closes when the panel unmounts. It does not connect while the snapshot is stale or the link is offline.
26. **Reconnecting:** a connection that drops without a refusal reconnects on the stream's backoff (1 s to 30 s) and whenever the page becomes visible. A refusal (§16) shows its reason and does not reconnect. "The shell exited" shows a "Start a new shell" button, which connects again.
27. **Desktop input:** keys go to the terminal while it has focus; ⌘K still opens the palette. Copy takes xterm.js's selection, paste goes in as bracketed paste when the shell asked for it. A resize of the panel sends the new size, debounced by 100 ms.
28. **Phone input:** below 1024px the terminal view takes no keyboard focus: touch scrolls and selects. Below it sits a command line, a 16px text field with autocorrect, capitalisation and spellcheck off, whose Enter sends its text and a carriage return (a bare one when empty) and clears it. Above the field, a row of keys, each sending its bytes at once: Ctrl-C, Ctrl-D, Tab, Esc and the four arrows. The footer hides while the field has focus, as for the reply box.

### Docs and rules

29. **`internal/web/CLAUDE.md`** gains: the terminal route checks `Origin` itself and refuses while the switch is off; terminal bytes are never logged. **`internal/web/ui/CLAUDE.md`** gains: xterm.js loads only in `terminal.html`'s script, and the panel reaches the frame only through its interface. **`internal/web/CLAUDE.md`** also gains: only `terminal.html` gets `terminalCSP`. **`session/tmux/CLAUDE.md`** gains: `ShellAttach.Close` never closes the session. Each within its budget (`go run ./tools/claudemd -v` first): `internal/web/CLAUDE.md` is 13 bytes under its 20,000, so the story that adds its rules first moves supporting context of existing rules into `docs/claude/web-layer.md`, changing no rule's meaning.
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
- **Go, the frame's document:** `terminal.html` answered with `terminalCSP` and every other path with the app's; the entry bundle importing nothing of xterm.js.
- **Go, the route:** each refusal of §16 before the upgrade; the limits; the frame cap and the size bounds; switch-off, revocation and the layer's close ending live connections with their reasons; the full buffer closing the connection; nothing of the bytes in the log.
- **Vitest:** the socket's state machine with a fake WebSocket (connecting, live, each refusal, reconnect and its backoff, visible again); the command line and each key's bytes; `appliesTo` across statuses and the switch; the resize debounce.
- **End to end** (`e2e/`, the `e2e` tag): a real daemon with the switch on and a paired device's cookie; a WebSocket client opens a started session's terminal, types `echo` of a marker and reads it back, and the same route answers 403 with the switch off.
- **webshot:** a `terminal` screen on the phone and both desktop sizes, the fixture server answering the route with canned bytes, under the real CSP.
- **The owner's checklist:** `loom web terminal on`; a command in a session's shell from the desktop browser and from the iPhone through the tunnel; the same shell in the TUI; lock and unlock the phone and find the shell as it was; `exit` and start a new one; `loom web terminal off` closing both; revoke a device with a terminal open.

## The spike

1. **A renderer under the CSP** with no violation in the console. **Done, 2026-11-26** (Findings): no renderer runs clean under the app's CSP; the framed document does. §23 is written from it.

The other three need the owner's iPhone and the tunnel, and are checked on the real route instead of a throwaway page (owner, 2026-11-26), through the bare `terminal.html` part 1 ships (Delivery):

2. **Safari on the iPhone opening a same-origin `wss:` connection** under `default-src 'self'`: checked when part 1 lands. If it refuses, the CSP gains `connect-src 'self' wss://<origin's host>`.
3. **A WebSocket through the tunnel**, held open for 10 minutes with only pings: checked when part 1 lands. Part 1 is built before this is proven; the tunnel is Cloudflare's, which carries WebSockets.
4. **Touch scrolling and selection** in xterm.js on iOS with the keyboard unfocused (§28), and whether iOS Safari gives xterm.js a WebGL context or falls to the DOM renderer (§23): checked when part 1 lands, before part 2 is designed. A failure changes §28 in part 2's design.

## Delivery

An epic of 2 parts, each a plan merged to main before the next part is designed.

1. **Part 1: the daemon side.** The switch (§4–§8) with its CLI and settings row; `EnsureTerminal` (§9–§11); the attach unit (§12–§14); the dependency and the route (§15–§21); the snapshot field and the stream event; the Go tests and the end-to-end test; the rules and docs for these (§29–§30). It ends with a WebSocket client reaching a session's shell through a paired device's cookie. It also ships the frame's document bare (§23: `terminal.html`, its script with xterm.js and the socket, and `terminalCSP`), with no panel and no phone input, so that opening `/terminal.html#<session id>` on a paired device shows a working shell. The owner checks the spike's items 2 to 4 with it on the iPhone.
2. **Part 2: the app.** The panel around the frame, the frame's interface, reconnecting and both input modes (§22, §24–§28); the fixture's canned route and the webshot screens; Vitest; the app's rules and docs; the owner's checklist.
