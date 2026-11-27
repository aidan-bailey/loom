# Loom web terminal, part 2: the app — Implementation Plan

> **For agentic workers:** this plan runs supervised: `/flow:draft` writes the stories from this skeleton, then `/flow:run` runs them story by story. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put a session's worktree shell in the new app as a `terminal` tab: typed into directly on the desktop, and on the phone scrolled with a finger, typed into from a command line with a row of keys, and copied from through a plain-text sheet.

**Architecture:** The daemon drops tmux's mouse reports from the web attach's output, so xterm.js scrolls its own scrollback and selects. The frame's document (`terminal.html`) gains a typed interface on its window, reconnecting with a backoff, a reset on each new connection, its own key handling and a touch handler that scrolls xterm.js. A `terminal` panel in the entry bundle draws the frame, drives it only through that interface, shows its states, and on the phone adds the key row, the command line and the copy sheet. webshot's canned loom serves the frame's page under its own CSP and answers the WebSocket route with a canned screen.

**Tech Stack:** Go (testify, `github.com/coder/websocket` v1.8.15 in webshot's canned loom); TypeScript, React 19, Vite 8, Vitest with happy-dom, Base UI's Drawer, `@xterm/xterm` 6.0.0 with its WebGL and fit addons (in the frame only).

**Spec:** `docs/superpowers/specs/2026-11-26-web-terminal-design.md`

**Part:** 2 of 2

**Budget:** draft $24; run $18 (estimate 2026-11-27)

## File structure

The daemon:

- `session/vt/altfilter.go`, `session/vt/altfilter_test.go` (changed: the filter that also drops the mouse modes); `session/vt/CLAUDE.md` (changed).
- `internal/web/terminal_conn.go`, `internal/web/terminal_conn_test.go` (changed: the pump uses it); `internal/web/README.md` (changed).

The frame (`internal/web/ui/src/terminal/`):

- `touch.ts` (new: a finger's drag and momentum as line scrolls), `text.ts` (new: buffer lines to plain text), each with its test.
- `frame.ts` (new: the interface's types and the accessor; no xterm.js), `reconnect.ts` (new: which endings reconnect, the backoff, the link), `keys.ts` (new: a named key's bytes, and what a key event in the frame does), each with its test.
- `socket.ts` (changed: the close codes as constants), `main.ts` (changed: it wires all of the above and sets `window.loomTerminal`), `terminal.css` (changed: no status line when framed).

The app (`internal/web/ui/src/`):

- `app/panels.ts`, `app/routes.tsx`, `app/shell.tsx`, `desktop/Inspector.tsx`, `desktop/Palette.tsx`, `store/fleet.ts` and their tests (changed: the panel's id, path and tab, `appliesTo`'s second argument, the host's facts).
- `panels/info/index.tsx`, `panels/mockup/index.tsx`, `panels/flow/index.tsx` (changed: their tab orders move down one); `panels/index.ts` (changed: the new import).
- `panels/terminal/index.tsx` (new: the panel's definition), `panels/terminal/Terminal.tsx` (new: the frame, its states, the desktop's focus), `panels/terminal/PhoneInput.tsx` (new: the key row, the command line, the sheet), each with its test.
- `internal/web/dist/` (rebuilt by every story that changes what the app builds, committed).

webshot (`tools/webshot/`):

- `canned.go` (changed: `terminalCSP` on `/terminal.html`, the WebSocket route), `main.go` (changed: the screens, the phone-only and click modes), `fixture/terminal.ansi` (new), `fixture/snapshot.json` (changed: `terminal` true), `webshot_test.go` (changed).

Elsewhere: `internal/web/ui_rules_test.go` (new: the panel never names the frame's document); `internal/web/ui/CLAUDE.md`, `USAGE.md`, `internal/web/README.md`, `internal/web/ui/public/sw.js` (the rule, the docs, the stale comment).

## Names contract

Every name one story defines and another uses. A story may add names of its own; a name another story needs and this section lacks goes back to the manager.

**The filter (Story 1)**, in `session/vt/altfilter.go`:

- `func NewAltScreenFilterWithoutMouse(dst io.Writer) io.Writer` (Story 1): as `NewAltScreenFilter`, and it also removes the private modes 1000, 1002, 1003, 1005, 1006 and 1015 by the same rule. `NewAltScreenFilter` keeps its signature and its behaviour. Only `internal/web/terminal_conn.go`'s pump calls the new one.

**Touch and text (Story 2)**, in `internal/web/ui/src/terminal/`:

- `interface Scroller { scrollLines(lines: number): void; cellHeight(): number }` (Story 2), in `touch.ts`: `scrollLines` takes a negative count to show older lines, as xterm.js's does; `cellHeight` is one row's height in CSS pixels.
- `function bindTouchScroll(el: HTMLElement, scroller: Scroller): () => void` (Story 2), in `touch.ts`: binds the touch listeners of spec §38 to `el` and returns the function that unbinds them and stops any momentum.
- `interface BufferLine { text: string; wrapped: boolean }` (Story 2), in `text.ts`: one buffer line with its trailing blanks already cut, and whether it continues the line before it.
- `function plainText(lines: Iterable<BufferLine>): string` (Story 2), in `text.ts`: spec §33's `text()` rule.

**The frame's interface (Story 3)**, in `internal/web/ui/src/terminal/`:

- `type KeyName = "up" | "down" | "left" | "right" | "tab" | "esc" | "ctrl-c" | "ctrl-d"` (Story 3), in `frame.ts`.
- `type Phase = "connecting" | "live" | "waiting" | "closed"` (Story 3), in `frame.ts`.
- `type Renderer = "webgl" | "dom"` (Story 3), in `frame.ts`.
- `interface FrameState { phase: Phase; closed?: Closed; retryAt?: number; renderer: Renderer }` (Story 3), in `frame.ts`: `Closed` is `socket.ts`'s existing `{ code: number; reason: string }`; `retryAt` is a time in milliseconds since the epoch, set only while `waiting`; `closed` is set only while `closed`.
- `interface LoomTerminal { send(bytes: Uint8Array<ArrayBuffer>): void; key(name: KeyName): void; text(): string; setTouch(on: boolean): void; setLink(up: boolean): void; reconnect(): void; focus(): void; close(): void; onState(cb: (state: FrameState) => void): void; onChord(cb: () => void): void }` (Story 3), in `frame.ts`. `onState` and `onChord` each hold one callback, the last one given; `onState` calls it at once with the current state.
- `function frameTerminal(frame: HTMLIFrameElement): LoomTerminal | null` (Story 3), in `frame.ts`: the frame's `window.loomTerminal`, or null while its script has not run. It is the only code outside the frame that reads the frame's window.
- `window.loomTerminal` (Story 3): set by `main.ts` as the script first runs, before the font has loaded; a call made before the terminal has started takes effect when it starts, and `text()` answers `""` until then.
- In `socket.ts` (Story 3): `CLOSE_STOPPING = 1001`, `CLOSE_OFF = 4001`, `CLOSE_REVOKED = 4002`, `CLOSE_SESSION = 4003`, `CLOSE_EXITED = 4004`, `CLOSE_SLOW = 4005`, `CLOSE_BAD_FRAME = 4006`, `CLOSE_NO_ATTACH = 4007`, exported constants. `describeClose(closed: Closed): string` stays as it is.

**The panel (Story 4)**, in `internal/web/ui/src/`:

- `PanelId` gains `"terminal"` (Story 4); its path is `/s/<id>/terminal` (Story 4), so its hash is `#s/<id>/terminal`.
- `interface HostFacts { terminal: boolean }` (Story 4), in `app/panels.ts`.
- `Panel.appliesTo` becomes `appliesTo?: (session: Session, host: HostFacts) => boolean` (Story 4), and `function sessionTabs(session: Session, host: HostFacts): Panel[]` (Story 4).
- `function useHostFacts(): HostFacts` (Story 4), in `store/fleet.ts`: the snapshot's `terminal`, false before the first snapshot; a component that calls it re-renders when the stream's `terminal` event changes it.
- `function Terminal(props: PanelProps): ReactElement` (Story 4), in `panels/terminal/Terminal.tsx`: the panel's body. Its root element carries `data-slot="terminal"` and `data-terminal-state` with the phase (absent until the frame gives a state). It holds the frame's `LoomTerminal | null` and its latest `FrameState | null` in state, which Story 5's component is given.
- On the frame's chord the panel dispatches `new KeyboardEvent("keydown", { key: "k", metaKey: true, bubbles: true })` on `document` (Story 4): the app's one key listener (`desktop/paletteState.ts`) answers it, and the entry bundle imports nothing of the desktop chunk.
- `TestTerminalPanel_ReachesTheFrameOnlyThroughItsInterface` (Story 4), in `internal/web/ui_rules_test.go`: no file under `internal/web/ui/src/panels/terminal/` holds `contentDocument` or `contentWindow`.

**The phone's input (Story 5)**, in `internal/web/ui/src/panels/terminal/PhoneInput.tsx`:

- `function PhoneInput(props: { terminal: LoomTerminal; renderer: Renderer }): ReactElement` (Story 5): the key row, the command line and the sheet. `Terminal` renders it under the frame when `useLayout()` is `"phone"` and the frame's interface is there.
- The Select key carries `data-slot="terminal-select"` and the sheet's text element `data-slot="terminal-text"` (Story 5).

**webshot (Story 6)**, in `tools/webshot/`:

- The screens `terminal` (hash `#s/1/terminal`) and `terminal-select` (the same hash) (Story 6).
- `screenMode` gains `phoneOnly bool` and `click string` (Story 6): a `phoneOnly` screen is left out on a desktop device as a `desktopOnly` one is on a phone; `click` is a selector clicked once it matches, after which `drawn` must match. A screen with `drawn` and neither `press` nor `click` waits for `drawn` before its shot.

## Stories

- **Story 1** — The web attach drops tmux's mouse reports.

  tmux's `mouse on` makes the attach ask the terminal for mouse reports, and while they are on xterm.js sends the wheel to tmux and selects nothing. A second constructor of the alt-screen filter also removes the six mouse modes, sharing the existing parameter loop and split-sequence handling, and the web's pump uses it; the TUI's pump keeps the old one, since its emulator reads those modes. Tests: each mode alone, several in one sequence with a mode that is kept, a sequence left empty and dropped, a sequence split across writes, `NewAltScreenFilter` still passing every mouse mode, and the web pump's output carrying none. `session/vt/CLAUDE.md` gains the rule that only the web's pump uses the new constructor, and `internal/web/README.md` describes it.

  - **Developer:** intermediate-dev
  - **Files:** `session/vt/altfilter.go`, `session/vt/altfilter_test.go`, `session/vt/CLAUDE.md`, `internal/web/terminal_conn.go`, `internal/web/terminal_conn_test.go`, `internal/web/README.md`
  - **Interfaces:** exposes `vt.NewAltScreenFilterWithoutMouse`; uses nothing of this plan
  - **Verify:** `CGO_ENABLED=0 go test ./session/vt/ ./internal/web/ ./tools/claudemd/`
  - **Review:** light
  - **Form:** brief (a second set of mode numbers through the filter's existing loop, and one call site)
  - **Reading:** spec §31, §32, §49, §52; `session/vt/altfilter.go`; `session/vt/altfilter_test.go`; `session/vt/CLAUDE.md`; `internal/web/terminal_conn.go:490-545`; `internal/web/terminal_conn_test.go`; `session/tmux/tmux.go:455-470`; `internal/web/README.md:85-120`

- **Story 2** — Touch scrolling and plain text, as modules of their own.

  xterm.js 6 binds no touch handler, so the frame scrolls itself. `touch.ts` turns one finger's vertical drag on an element into `scrollLines` calls (the distance accumulating, one line per cell height, the content following the finger), cancels `touchstart` so no click and no focus follow, carries a flick on by spec §38's numbers with `requestAnimationFrame`, and stops on a new touch, a second finger or the unbind. `text.ts` turns buffer lines into the plain text the copy sheet shows. Neither imports xterm.js, so both are tested with synthetic touches, a fake `Scroller` and fake timers; Story 3 wires them to the terminal.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/terminal/touch.ts`, `internal/web/ui/src/terminal/touch.test.ts`, `internal/web/ui/src/terminal/text.ts`, `internal/web/ui/src/terminal/text.test.ts`
  - **Interfaces:** exposes `Scroller`, `bindTouchScroll`, `BufferLine` and `plainText` to Story 3; uses nothing of this plan
  - **Verify:** `pnpm -C internal/web/ui run check`
  - **Review:** light
  - **Form:** brief (two pure modules whose tests pin the arithmetic)
  - **Reading:** spec §33 (`text()`), §38, §52; `internal/web/ui/src/terminal/main.ts`; `internal/web/ui/src/terminal/socket.test.ts:1-60`; `internal/web/ui/src/test-setup.ts`; `internal/web/ui/CLAUDE.md`

- **Story 3** — The frame's interface, reconnecting, the link and the keys.

  The frame's script becomes something a panel can drive. `frame.ts` holds the interface's types and the accessor. `reconnect.ts` decides which endings reconnect (1001, 4005, an ending with no close frame, a failure before opening), keeps the backoff (1 s doubling to 30 s, back to 1 s on `live`), reconnects at once when the page becomes visible, and holds every new connection while the link is down; a page that is not in a frame behaves as if the link were up and keeps part 1's status line. `keys.ts` gives each named key's bytes in both cursor modes and says what a key event in the frame does: ⌘K to the chord callback, Ctrl+Shift+C copying the selection, Ctrl+Shift+V left to the browser, Ctrl+K and everything else to the shell. `main.ts` sets `window.loomTerminal` as it first runs, resets xterm.js when a new connection opens, sends a new size 100 ms after the last change, reports the renderer and its change on a lost WebGL context, never focuses xterm.js in touch mode, and wires Story 2's modules to the terminal. `socket.ts` exports the close codes. Vitest covers the policy for every code, the backoff, visibility, the link, the keys and the copy keys.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/terminal/frame.ts`, `internal/web/ui/src/terminal/reconnect.ts`, `internal/web/ui/src/terminal/reconnect.test.ts`, `internal/web/ui/src/terminal/keys.ts`, `internal/web/ui/src/terminal/keys.test.ts`, `internal/web/ui/src/terminal/socket.ts`, `internal/web/ui/src/terminal/socket.test.ts`, `internal/web/ui/src/terminal/main.ts`, `internal/web/ui/src/terminal/terminal.css`, `internal/web/dist/**`, `internal/web/README.md`
  - **Interfaces:** exposes `KeyName`, `Phase`, `Renderer`, `FrameState`, `LoomTerminal`, `frameTerminal`, `window.loomTerminal` and the `CLOSE_` constants to Stories 4 and 5; uses Story 2's `bindTouchScroll`, `Scroller`, `BufferLine` and `plainText`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (a reconnecting state machine over timers, visibility and an outside connection, and a reset that must land before the history)
  - **Reading:** spec §16, §17, §23, §26, §33–§38, §52; `internal/web/ui/src/terminal/main.ts`; `internal/web/ui/src/terminal/socket.ts`; `internal/web/ui/src/terminal/socket.test.ts`; `internal/web/ui/src/terminal/terminal.css`; `internal/web/ui/terminal.html`; `internal/web/ui/src/api/stream.ts:1-60,160-200`; `internal/web/terminal_conn.go:36-90,450-480`; `internal/web/static_budget_test.go:300-335`; `internal/web/ui/CLAUDE.md`; `internal/web/README.md:85-120`
  - **Drafts after:** Story 2

- **Story 4** — The `terminal` panel: its tab, its routing, the frame and the desktop.

  The panel joins the registry and the hand-kept routing: `PanelId`, the path table and the session-tab pattern in `routes.tsx`, `TABBED` and `tabOf` in `shell.tsx`, with tab order 3 and info, mockup and flow moved to 4, 5 and 6. `appliesTo` and `sessionTabs` gain the host's facts, the phone's tabs, the inspector and the palette pass `useHostFacts()`, and the terminal applies when the host's terminal is on and the session is started, active and not paused, so the tab comes and goes on the stream's event. The body draws the frame, takes its interface once it has loaded, passes the layout's touch mode and the link (`!stale`), closes it on unmount, shows "Connecting…", "Reconnecting…", a closed reason and the "Start a new shell" button for 4004, focuses the terminal on the desktop when it goes live or is shown, and turns the frame's chord into the app's own key event. A direct link while the panel does not apply says why. It adds the rule to `internal/web/ui/CLAUDE.md` with its Go test, and the panel to `USAGE.md`.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/app/routes.tsx`, `internal/web/ui/src/app/routes.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/desktop/Inspector.tsx`, `internal/web/ui/src/desktop/Inspector.test.tsx`, `internal/web/ui/src/desktop/Palette.tsx`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/panels/index.ts`, `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/mockup/index.tsx`, `internal/web/ui/src/panels/flow/index.tsx`, `internal/web/ui/src/panels/terminal/index.tsx`, `internal/web/ui/src/panels/terminal/Terminal.tsx`, `internal/web/ui/src/panels/terminal/Terminal.test.tsx`, `internal/web/ui_rules_test.go`, `internal/web/ui/CLAUDE.md`, `internal/web/dist/**`, `USAGE.md`
  - **Interfaces:** exposes `HostFacts`, `useHostFacts`, the `terminal` panel and its path, `Terminal` and its `data-` attributes to Stories 5 and 6; uses Story 3's `LoomTerminal`, `FrameState`, `frameTerminal`, `CLOSE_EXITED` and `describeClose`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
  - **Review:** deep
  - **Form:** brief (a panel added by the registry's pattern, and a signature widened at its three callers)
  - **Reading:** spec §22, §25, §39–§43, §49, §50, §52, Errors and edge cases; `internal/web/ui/CLAUDE.md`; `internal/web/ui/src/app/panels.ts`; `internal/web/ui/src/app/routes.tsx:60-135`; `internal/web/ui/src/app/shell.tsx:180-235,285-420`; `internal/web/ui/src/app/viewport.ts`; `internal/web/ui/src/desktop/Inspector.tsx`; `internal/web/ui/src/desktop/Palette.tsx:20-50`; `internal/web/ui/src/desktop/paletteState.ts`; `internal/web/ui/src/store/fleet.ts`; `internal/web/ui/src/panels/changes/index.tsx`; `internal/web/ui/src/panels/mockup/index.tsx`; `internal/web/ui/src/commands/session.ts:1-51`; `internal/web/static_budget_test.go:186-335`; `USAGE.md:945-960`
  - **Drafts after:** Story 3

- **Story 5** — The phone's input: the key row, the command line and the copy sheet.

  On the phone the terminal takes no focus, so typing happens under it. The key row sends Ctrl-C, Ctrl-D, Tab, Esc and the four arrows through `key(name)` at once, and a press on it never takes the focus from the command line. The command line is a 16px one-line field with autocorrect, capitalisation, completion and spellcheck off and `enterKeyHint="send"`; Enter sends its text as UTF-8 and a carriage return (a bare one when empty), clears it and keeps the focus. Select opens the app's Drawer with `text()` read once, in a selectable `<pre>` scrolled to its end, a footer naming the renderer and a Done button; loom writes nothing to the clipboard. `Terminal` renders it under the frame on the phone. `USAGE.md` describes the phone's keys and the sheet.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/terminal/PhoneInput.tsx`, `internal/web/ui/src/panels/terminal/PhoneInput.test.tsx`, `internal/web/ui/src/panels/terminal/Terminal.tsx`, `internal/web/ui/src/panels/terminal/Terminal.test.tsx`, `internal/web/dist/**`, `USAGE.md`
  - **Interfaces:** exposes `PhoneInput` and the `terminal-select` and `terminal-text` slots to Story 6; uses Story 3's `LoomTerminal`, `KeyName` and `Renderer`, and Story 4's `Terminal`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light
  - **Form:** brief (a text field, a row of buttons and a sheet over an interface that exists)
  - **Reading:** spec §28, §44, §45, §50, §52; `internal/web/ui/CLAUDE.md`; `internal/web/ui/src/components/ui/drawer.tsx`; `internal/web/ui/src/app/ConfirmSheet.tsx`; `internal/web/ui/src/panels/pair/Pair.tsx:30-50`; `internal/web/ui/src/components/ui/textarea.tsx`; `internal/web/ui/src/app/shell.tsx:285-320`; `internal/web/ui/src/test-layout.ts`
  - **Drafts after:** Story 4

- **Story 6** — webshot: the canned page, the canned route and the screens.

  The canned loom would refuse the frame (it serves every file under the app's CSP) and fail the run on the WebSocket route (an API route it cannot answer). It serves `/terminal.html` under `terminalCSP`, a copy the fixture test holds to the layer's; answers `GET /api/v1/sessions/{id}/terminal` by upgrading with `github.com/coder/websocket`, waiting for the first resize frame, sending `fixture/terminal.ansi` in a binary frame and then `{"state":"live"}`, and holding the connection open, without counting a miss; and its snapshot's `terminal` becomes true. The screens are `terminal` on every device, waiting for `[data-terminal-state="live"]`, and `terminal-select` on the phone only, which clicks `[data-slot="terminal-select"]` and waits for `[data-slot="terminal-text"]`. The shot must show the canned text: if a WebGL canvas comes out blank, the canned run starts Chromium with WebGL off. It also corrects the stale comment in `public/sw.js` and updates whatever Vitest fixture expectations the snapshot's new value moves.

  - **Developer:** intermediate-dev
  - **Files:** `tools/webshot/canned.go`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `tools/webshot/fixture/terminal.ansi`, `tools/webshot/fixture/snapshot.json`, `internal/web/ui/public/sw.js`, `internal/web/dist/**`, `internal/web/README.md`
  - **Interfaces:** exposes nothing; uses Story 4's path `#s/1/terminal` and `data-terminal-state`, and Story 5's `terminal-select` and `terminal-text` slots
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -screens terminal -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -screens terminal -out "$d"'`
  - **Review:** light
  - **Form:** brief (a canned route and two screens in a test tool, by the tool's existing patterns)
  - **Reading:** spec §46–§48, §50; `tools/webshot/main.go:15-135,290-345,600-800`; `tools/webshot/canned.go`; `tools/webshot/frame.go:40-100`; `tools/webshot/webshot_test.go`; `tools/CLAUDE.md`; `internal/web/README.md:95-110`; `internal/web/static.go:260-295`; `internal/web/terminal_conn.go:36-90,450-480`; `internal/web/ui/src/terminal/socket.ts:60-130`; `internal/web/ui/public/sw.js:1-65`
  - **Drafts after:** Story 4, Story 5

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
