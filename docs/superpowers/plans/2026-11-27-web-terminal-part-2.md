# Loom web terminal, part 2: the app — Implementation Plan

> **For agentic workers:** this plan runs supervised: `/flow:draft` writes the stories from this skeleton, then `/flow:run` runs them story by story. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put a session's worktree shell in the new app as a `terminal` tab: typed into directly on the desktop, and on the phone scrolled with a finger, typed into from a command line with a row of keys, and copied from through a plain-text sheet.

**Architecture:** The daemon drops tmux's mouse reports from the web attach's output, so xterm.js scrolls its own scrollback and selects. The frame's document (`terminal.html`) gains a typed interface on its window, reconnecting with a backoff, a reset on each new connection, its own key handling and a touch handler that scrolls xterm.js. A `terminal` panel in the entry bundle draws the frame, drives it only through that interface, shows its states, and on the phone adds the key row, the command line and the copy sheet. webshot's canned loom serves the frame's page under its own CSP and answers the WebSocket route with a canned screen.

**Tech Stack:** Go (testify, `github.com/coder/websocket` v1.8.15 in webshot's canned loom); TypeScript, React 19, Vite 8, Vitest with happy-dom, Base UI's Drawer, `@xterm/xterm` 6.0.0 with its WebGL and fit addons (in the frame only).

**Spec:** `docs/superpowers/specs/2026-11-26-web-terminal-design.md`

**Part:** 2 of 2

**Budget:** draft $31; run $18 (estimate 2026-11-27)

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
- `interface BufferLine { text: string; wrapped: boolean }` (Story 2), in `text.ts`: one buffer row as the terminal holds it, its trailing blanks kept (xterm.js's `translateToString` with trimming off), and whether it continues the row before it.
- `function plainText(lines: Iterable<BufferLine>): string` (Story 2), in `text.ts`: spec §33's `text()` rule as §56 settles it: a row and the wrapped rows after it are one logical line, and trailing blanks are cut at the end of each logical line, never of a row the next one continues.

**The frame's interface (Story 3)**, in `internal/web/ui/src/terminal/`:

- `type KeyName = "up" | "down" | "left" | "right" | "tab" | "esc" | "ctrl-c" | "ctrl-d"` (Story 3), in `frame.ts`.
- `type Phase = "connecting" | "live" | "waiting" | "closed"` (Story 3), in `frame.ts`.
- `type Renderer = "webgl" | "dom"` (Story 3), in `frame.ts`.
- `interface FrameState { phase: Phase; closed?: Closed; retryAt?: number; renderer: Renderer }` (Story 3), in `frame.ts`: `Closed` is `socket.ts`'s existing `{ code: number; reason: string }`; `retryAt` is a time in milliseconds since the epoch, set only while `waiting`; `closed` is set only while `closed`. When the terminal cannot start, the state becomes `closed` with `closed` holding code 0 and the failure's text as its reason (spec §68).
- `interface LoomTerminal { send(bytes: Uint8Array<ArrayBuffer>): boolean; key(name: KeyName): boolean; text(): string; setTouch(on: boolean): void; setLink(up: boolean): void; reconnect(): void; focus(): void; close(): void; onState(cb: (state: FrameState) => void): void; onChord(cb: () => void): void }` (Story 3), in `frame.ts`. `onState` and `onChord` each hold one callback, the last one given; `onState` calls it at once with the current state. `send` and `key` answer true when the bytes went to an open connection and false otherwise; nothing is queued, so input given while the phase is not `live` is dropped (spec §55).
- `function frameTerminal(frame: HTMLIFrameElement): LoomTerminal | null` (Story 3), in `frame.ts`: the frame's `window.loomTerminal`, or null while its script has not run. It is the only code outside the frame that reads the frame's window.
- `window.loomTerminal` (Story 3): set by `main.ts` as the script first runs, before the font has loaded; a call that sets something (`setTouch`, `setLink`, `onState`, `onChord`, `reconnect`, `focus`, `close`) made before the terminal has started takes effect when it starts; until then `send` and `key` answer false and `text()` answers `""`.
- In `socket.ts` (Story 3): `CLOSE_STOPPING = 1001`, `CLOSE_OFF = 4001`, `CLOSE_REVOKED = 4002`, `CLOSE_SESSION = 4003`, `CLOSE_EXITED = 4004`, `CLOSE_SLOW = 4005`, `CLOSE_BAD_FRAME = 4006`, `CLOSE_NO_ATTACH = 4007`, exported constants. `describeClose(closed: Closed): string` stays as it is.

**The panel (Story 4)**, in `internal/web/ui/src/`:

- `PanelId` gains `"terminal"` (Story 4); its path is `/s/<id>/terminal` (Story 4), so its hash is `#s/<id>/terminal`.
- `interface HostFacts { terminal: boolean }` (Story 4), in `app/panels.ts`.
- `Panel.appliesTo` becomes `appliesTo?: (session: Session, host: HostFacts) => boolean` (Story 4), and `function sessionTabs(session: Session, host: HostFacts): Panel[]` (Story 4).
- `function useHostFacts(): HostFacts` (Story 4), in `store/fleet.ts`: the snapshot's `terminal`, false before the first snapshot; a component that calls it re-renders when the stream's `terminal` event changes it.
- `function Terminal(props: PanelProps): ReactElement` (Story 4), in `panels/terminal/Terminal.tsx`: the panel's body. Its root element carries `data-slot="terminal"` and `data-terminal-state` with the phase (absent until the frame gives a state). It holds the frame's `LoomTerminal | null` and its latest `FrameState | null` in state, which Story 5's component is given. It calls `focus()` by spec §57 as §67 narrows it (for a terminal the user brought up, at its first `live`, unless a text field of the app has the focus then; at every other `live`, only while the app's focused element is the frame; never before a `live`), and `close()` in a layout effect's cleanup inside a `try` (spec §61).
- Texts of Story 4 that Story 5 quotes, which every fix leaves as they are (Story 4): in `Terminal.tsx`, the line importing from `@/terminal/socket` and the closing lines of `Terminal`'s JSX (the frame and its notice in one `relative` element that is the root's only child); in `Terminal.test.tsx`, the two lines `expect(screen.queryByRole("button")).toBeNull();` and the helpers `mount`, `frameEl`, `rootEl`, `LIVE` and `WAITING`; in `USAGE.md`, the line `**The shell exited**, a **Start a new shell** button.`
- `function fakeTerminal()` (Story 4), in `panels/terminal/Terminal.test.tsx`: a test helper answering a `LoomTerminal` of `vi.fn()` members, with `@/terminal/frame` mocked so that `frameTerminal` answers it; Story 5's tests in that file use it.
- On the frame's chord the panel dispatches `new KeyboardEvent("keydown", { key: "k", metaKey: true, bubbles: true })` on `document` (Story 4): the app's one key listener (`desktop/paletteState.ts`) answers it, and the entry bundle imports nothing of the desktop chunk.
- `TestTerminalPanel_ReachesTheFrameOnlyThroughItsInterface` (Story 4), in `internal/web/ui_rules_test.go`: no file under `internal/web/ui/src/panels/terminal/` holds `contentDocument` or `contentWindow`.

**The phone's input (Story 5)**, in `internal/web/ui/src/panels/terminal/PhoneInput.tsx`:

- `function PhoneInput(props: { terminal: LoomTerminal; live: boolean; renderer: Renderer }): ReactElement` (Story 5): the key row, the command line and the sheet; `live` is whether the frame's phase is `live` (spec §55). Its root element is inside `Terminal`'s root. `Terminal` renders it under the frame when `useLayout()` is `"phone"` and the frame's interface is there.
- The Select key carries `data-slot="terminal-select"` and the sheet's text element `data-slot="terminal-text"` (Story 5).

**webshot (Story 6)**, in `tools/webshot/`:

- The screens `terminal` (hash `#s/1/terminal`) and `terminal-select` (the same hash) (Story 6).
- `screenMode` gains `phoneOnly bool`, `nextOnly bool`, `ready string` and `click string` (Story 6): a `phoneOnly` screen is left out on a desktop device as a `desktopOnly` one is on a phone; a `nextOnly` screen is left out of a run on the classic app; `click` is a selector clicked once it matches, and only after `ready` matches when the screen has one, after which `drawn` must match. A screen with `drawn` and neither `press` nor `click` waits for `drawn` before its shot.

**Design stage:** /flow:draft session a308cb99-e4ba-438f-8421-ecf80a0c5dff, 2026-11-27T07:28:45Z to 2026-11-27T09:01:09Z UTC: $32.76 logged, $32.82 with estimated thinking; 6724 plan lines, $4.87 per 1,000 lines; skeleton review $2.20; risk review $6.37; budget $31; forms: full 3 stories $17.74, brief 3 stories $6.45; loom flow roster 0e25f6995f89.

## Stories

- **Story 1** — The web attach drops tmux's mouse reports.

  tmux's `mouse on` makes the attach ask the terminal for mouse reports, and while they are on xterm.js sends the wheel to tmux and selects nothing. A second constructor of the alt-screen filter also removes the six mouse modes, sharing the existing parameter loop and split-sequence handling, and the web's pump uses it; the TUI's pump keeps the old one, since its emulator reads those modes. Tests: each mode alone, several in one sequence with a mode that is kept, a sequence left empty and dropped, a sequence split across writes, `NewAltScreenFilter` still passing every mouse mode, and the web pump's output carrying none. `session/vt/CLAUDE.md` gains the rule that only the web's pump uses the new constructor, and `internal/web/README.md` and `session/README.md`'s `vt` section describe it.

  - **Developer:** intermediate-dev
  - **Files:** `session/vt/altfilter.go`, `session/vt/altfilter_test.go`, `session/vt/CLAUDE.md`, `internal/web/terminal_conn.go`, `internal/web/terminal_conn_test.go`, `internal/web/README.md`, `session/README.md`
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
  - **Form:** brief (two pure modules whose tests pin the arithmetic, Task 2.2's momentum loop on animation frames included)
  - **Reading:** spec §33 (`text()`), §38, §52; `internal/web/ui/src/terminal/main.ts`; `internal/web/ui/src/terminal/socket.test.ts:1-60`; `internal/web/ui/src/test-setup.ts`; `internal/web/ui/CLAUDE.md`

- **Story 3** — The frame's interface, reconnecting, the link and the keys.

  The frame's script becomes something a panel can drive. `frame.ts` holds the interface's types and the accessor. `reconnect.ts` decides which endings reconnect (1001, 4005, an ending with no close frame, a failure before opening), keeps the backoff (1 s doubling to 30 s, back to 1 s only once a connection has stayed live for 30 s, spec §66), reconnects at once when the page becomes visible, and holds every new connection while the link is down; it connects again when the page is restored from the back-forward cache (spec §59), and replaces a `live` connection with a fresh one when the link comes back up or the page becomes visible after 30 s or longer hidden (spec §60); a page that is not in a frame behaves as if the link were up and keeps part 1's status line. `keys.ts` gives each named key's bytes in both cursor modes and says what a key event in the frame does: ⌘K to the chord callback, Ctrl+Shift+C copying the selection, Ctrl+Shift+V left to the browser, Ctrl+K and everything else to the shell. `main.ts` sets `window.loomTerminal` as it first runs, resets xterm.js when a new connection opens by writing RIS ahead of its bytes (spec §65), reports a start that throws as a `closed` state (spec §68), sends a new size 100 ms after the last change, reports the renderer and its change on a lost WebGL context, never focuses xterm.js in touch mode, answers `send` and `key` with whether the bytes went to an open connection and queues nothing (spec §55), and wires Story 2's modules to the terminal, giving `plainText` each row untrimmed. The size debounce and the link's controller live in `reconnect.ts`, where the tests reach them. `socket.ts` exports the close codes. Vitest covers the policy for every code, the backoff, visibility, the link, the keys and the copy keys.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/terminal/frame.ts`, `internal/web/ui/src/terminal/frame.test.ts`, `internal/web/ui/src/terminal/reconnect.ts`, `internal/web/ui/src/terminal/reconnect.test.ts`, `internal/web/ui/src/terminal/keys.ts`, `internal/web/ui/src/terminal/keys.test.ts`, `internal/web/ui/src/terminal/socket.ts`, `internal/web/ui/src/terminal/socket.test.ts`, `internal/web/ui/src/terminal/main.ts`, `internal/web/ui/src/terminal/terminal.css`, `internal/web/dist/**`, `internal/web/README.md`
  - **Interfaces:** exposes `KeyName`, `Phase`, `Renderer`, `FrameState`, `LoomTerminal`, `frameTerminal`, `window.loomTerminal` and the `CLOSE_` constants to Stories 4 and 5; uses Story 2's `bindTouchScroll`, `Scroller`, `BufferLine` and `plainText`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (a reconnecting state machine over timers, visibility and an outside connection, and a reset that must land before the history)
  - **Reading:** spec §16, §17, §23, §26, §33–§38, §52, §55, §56, §59, §60, §65, §66, §68; `internal/web/ui/src/terminal/main.ts`; `internal/web/ui/src/terminal/socket.ts`; `internal/web/ui/src/terminal/socket.test.ts`; `internal/web/ui/src/terminal/terminal.css`; `internal/web/ui/terminal.html`; `internal/web/ui/src/api/stream.ts:1-60,160-200`; `internal/web/terminal_conn.go:36-90,450-480`; `internal/web/static_budget_test.go:300-335`; `internal/web/ui/CLAUDE.md`; `internal/web/README.md:85-120`
  - **Drafts after:** Story 2

- **Story 4** — The `terminal` panel: its tab, its routing, the frame and the desktop.

  The panel joins the registry and the hand-kept routing: `PanelId`, the path table and the session-tab pattern in `routes.tsx`, `TABBED` and `tabOf` in `shell.tsx`, with tab order 3 and info, mockup and flow moved to 4, 5 and 6. `appliesTo` and `sessionTabs` gain the host's facts, the phone's tabs, the inspector and the palette pass `useHostFacts()`, and the terminal applies when the host's terminal is on and the session is started, active and not paused, so the tab comes and goes on the stream's event. The body draws the frame, takes its interface once it has loaded, passes the layout's touch mode and the link (`!stale`), closes it on unmount before the frame leaves the document (spec §61), shows "Connecting…", "Reconnecting…", a closed reason and the "Start a new shell" button for 4004, focuses the terminal on the desktop by spec §57 as §67 narrows it (a terminal the user brought up at its first `live`, never out of a text field; at any other `live` only while the frame has the focus), and turns the frame's chord into the app's own key event. The inspector never falls back to the terminal: `inspectorTab` draws it only as the stored choice (spec §58), pinned on both sides. A direct link while the panel does not apply says why. It adds the rule to `internal/web/ui/CLAUDE.md` with its Go test, and the panel to `USAGE.md`.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/app/routes.tsx`, `internal/web/ui/src/app/routes.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/desktop/Inspector.tsx`, `internal/web/ui/src/desktop/Inspector.test.tsx`, `internal/web/ui/src/desktop/inspector.ts`, `internal/web/ui/src/desktop/inspector.test.ts`, `internal/web/ui/src/desktop/Palette.tsx`, `internal/web/ui/src/desktop/Palette.test.tsx`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/panels/index.ts`, `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/mockup/index.tsx`, `internal/web/ui/src/panels/flow/index.tsx`, `internal/web/ui/src/panels/info/info.test.tsx`, `internal/web/ui/src/panels/mockup/mockup.test.tsx`, `internal/web/ui/src/panels/flow/flow.test.tsx`, `internal/web/ui/src/panels/changes/changes.test.tsx`, `internal/web/ui/src/panels/turns/turns.test.tsx`, `internal/web/ui/src/panels/terminal/index.tsx`, `internal/web/ui/src/panels/terminal/Terminal.tsx`, `internal/web/ui/src/panels/terminal/Terminal.test.tsx`, `internal/web/ui_rules_test.go`, `internal/web/ui/CLAUDE.md`, `internal/web/README.md`, `internal/web/dist/**`, `USAGE.md`
  - **Interfaces:** exposes `HostFacts`, `useHostFacts`, the `terminal` panel and its path, `Terminal` and its `data-` attributes to Stories 5 and 6; uses Story 3's `LoomTerminal`, `FrameState`, `frameTerminal`, `CLOSE_EXITED` and `describeClose`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
  - **Review:** deep
  - **Form:** full (the panel's handling of the frame's interface: taking it on load, the link, the close before unmount, the focus rule and the chord)
  - **Reading:** spec §22, §25, §39–§43, §49, §50, §52, §55, §57, §58, §61, §67, §68, Errors and edge cases; `internal/web/ui/src/desktop/inspector.ts`; `internal/web/ui/CLAUDE.md`; `internal/web/ui/src/app/panels.ts`; `internal/web/ui/src/app/routes.tsx:60-135`; `internal/web/ui/src/app/shell.tsx:180-235,285-420`; `internal/web/ui/src/app/viewport.ts`; `internal/web/ui/src/desktop/Inspector.tsx`; `internal/web/ui/src/desktop/Palette.tsx:20-50`; `internal/web/ui/src/desktop/paletteState.ts`; `internal/web/ui/src/store/fleet.ts`; `internal/web/ui/src/panels/changes/index.tsx`; `internal/web/ui/src/panels/mockup/index.tsx`; `internal/web/ui/src/commands/session.ts:1-51`; `internal/web/static_budget_test.go:186-335`; `USAGE.md:945-960`
  - **Drafts after:** Story 3

- **Story 5** — The phone's input: the key row, the command line and the copy sheet.

  On the phone the terminal takes no focus, so typing happens under it. The key row sends Ctrl-C, Ctrl-D, Tab, Esc and the four arrows through `key(name)` at once, and a press on it never takes the focus from the command line; the keys and Enter are disabled unless `live` (spec §55). The command line is a 16px one-line field with autocorrect, capitalisation, completion and spellcheck off and `enterKeyHint="send"`; Enter sends its text as UTF-8 and a carriage return (a bare one when empty), clears it when `send` answered true and keeps its text otherwise, and keeps the focus. Select opens the app's Drawer with `text()` read once, in a selectable `<pre>` scrolled to its end, a footer naming the renderer and a Done button; loom writes nothing to the clipboard. `Terminal` renders it under the frame on the phone. `USAGE.md` describes the phone's keys and the sheet.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/terminal/PhoneInput.tsx`, `internal/web/ui/src/panels/terminal/PhoneInput.test.tsx`, `internal/web/ui/src/panels/terminal/Terminal.tsx`, `internal/web/ui/src/panels/terminal/Terminal.test.tsx`, `internal/web/dist/**`, `USAGE.md`
  - **Interfaces:** exposes `PhoneInput` and the `terminal-select` and `terminal-text` slots to Story 6; uses Story 3's `LoomTerminal`, `KeyName` and `Renderer`, and Story 4's `Terminal`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light
  - **Form:** brief (a text field, a row of buttons and a sheet over an interface that exists)
  - **Reading:** spec §28, §44, §45, §50, §52, §55; `internal/web/ui/CLAUDE.md`; `internal/web/ui/src/components/ui/drawer.tsx`; `internal/web/ui/src/app/ConfirmSheet.tsx`; `internal/web/ui/src/panels/pair/Pair.tsx:30-50`; `internal/web/ui/src/components/ui/textarea.tsx`; `internal/web/ui/src/app/shell.tsx:285-320`; `internal/web/ui/src/test-layout.ts`
  - **Drafts after:** Story 4

- **Story 6** — webshot: the canned page, the canned route and the screens.

  The canned loom would refuse the frame (it serves every file under the app's CSP) and fail the run on the WebSocket route (an API route it cannot answer). It serves `/terminal.html` under `terminalCSP`, a copy `internal/web/webshot_fixture_test.go` holds to the layer's beside the other two (its count of fixture files moves with `terminal.ansi`, and the embed pattern widens to take it); answers `GET /api/v1/sessions/{id}/terminal` by upgrading with `github.com/coder/websocket`, waiting for the first resize frame, sending `fixture/terminal.ansi` in a binary frame and then `{"state":"live"}`, and holding the connection open, without counting a miss; and its snapshot's `terminal` becomes true. The screens are `terminal` on every device, waiting for `[data-terminal-state="live"]`, and `terminal-select` on the phone only, which waits for `[data-terminal-state="live"]` (`ready`), then clicks `[data-slot="terminal-select"]` and waits for `[data-slot="terminal-text"]`; both are `nextOnly`, since the classic app has no panel (spec §63). `canned.go` imports `github.com/coder/websocket`, so `tools/CLAUDE.md`'s stdlib-only rule for webshot gains that exception within its line's 1,200 bytes (spec §62), and `docs/claude/web-layer.md`'s list of webshot's screens gains the two. The shot must show the canned text: if a WebGL canvas comes out blank, the canned run starts Chromium with WebGL off. It also corrects the stale comment in `public/sw.js` and updates whatever Vitest fixture expectations the snapshot's new value moves.

  - **Developer:** intermediate-dev
  - **Files:** `tools/webshot/canned.go`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `tools/webshot/fixture/terminal.ansi`, `tools/webshot/fixture/snapshot.json`, `tools/CLAUDE.md`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/public/sw.js`, `internal/web/ui/vite.config.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/ui/src/desktop/Inspector.test.tsx`, `internal/web/ui/src/desktop/Palette.test.tsx`, `internal/web/ui/src/desktop/palette.test.ts`, `internal/web/dist/**`, `internal/web/README.md`, `docs/claude/web-layer.md`
  - **Interfaces:** exposes nothing; uses Story 4's path `#s/1/terminal` and `data-terminal-state`, and Story 5's `terminal-select` and `terminal-text` slots
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -screens terminal -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -screens terminal -out "$d"'`
  - **Review:** light
  - **Form:** full (a WebSocket handler that bounds its wait for the first frame and holds a hijacked connection open, and the screens' click and skip logic)
  - **Reading:** spec §46–§48, §50, §62, §63, §69; `internal/web/webshot_fixture_test.go:75-110`; `docs/claude/web-layer.md:70-100`; `tools/webshot/main.go:15-135,290-345,600-800`; `tools/webshot/canned.go`; `tools/webshot/frame.go:40-100`; `tools/webshot/webshot_test.go`; `tools/CLAUDE.md`; `internal/web/README.md:95-110`; `internal/web/static.go:260-295`; `internal/web/terminal_conn.go:36-90,450-480`; `internal/web/ui/src/terminal/socket.ts:60-130`; `internal/web/ui/public/sw.js:1-65`
  - **Drafts after:** Story 4, Story 5

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->

## Story 1: The web attach drops tmux's mouse reports

- **Developer:** intermediate-dev
- **Files:** `session/vt/altfilter.go`, `session/vt/altfilter_test.go`, `session/vt/CLAUDE.md`, `internal/web/terminal_conn.go`, `internal/web/terminal_conn_test.go`, `internal/web/README.md`, `session/README.md`
- **Interfaces:** exposes `vt.NewAltScreenFilterWithoutMouse`; uses nothing of this plan
- **Verify:** `CGO_ENABLED=0 go test ./session/vt/ ./internal/web/ ./tools/claudemd/`
- **Review:** light
- **Form:** brief (a second set of mode numbers through the filter's existing loop, and one call site)

tmux's `mouse on` (set on the worktree shell's session) makes the attach ask the terminal for mouse reports, and while they are on, xterm.js sends the wheel to tmux and selects nothing. This story adds `vt.NewAltScreenFilterWithoutMouse`, a second constructor of the alt-screen filter that also removes the private modes 1000, 1002, 1003, 1005, 1006 and 1015 through the filter's existing parameter loop and split-sequence handling, and makes the web terminal's output pump the only caller. The TUI's pump (`session/tmux/tmux.go`) keeps `vt.NewAltScreenFilter`, since its emulator reads those modes. tmux's `mouse` option is not changed. No code here holds a landmine marker. Task 1.1 is the filter, its tests and `session/README.md`; Task 1.2 is the pump's call, its test, the rule in `session/vt/CLAUDE.md` and the web README.

### Task 1.1: The filter's second constructor

**Files:**
- Modify: `session/vt/altfilter.go:8-34` (the mode set and the constructors), `session/vt/altfilter.go:62` (the call of `rewritePrivateModeSeq`), `session/vt/altfilter.go:118-149` (`rewritePrivateModeSeq`)
- Modify: `session/vt/altfilter_test.go`
- Modify: `session/README.md:174`

**Behaviour.** `NewAltScreenFilterWithoutMouse(dst io.Writer) io.Writer` (contract name, Story 1) behaves as `NewAltScreenFilter` and in addition removes the parameters `1000`, `1002`, `1003`, `1005`, `1006` and `1015` from every `CSI ? … h` and `CSI ? … l`, by the rule the filter applies to `47`, `1047` and `1049` today: the other parameters of the sequence are kept in order, a sequence left with no parameter is dropped whole, and a sequence split across writes is held back as today (`f.pend`, `maxPendingSeq`). Parameters are matched as whole strings, so `100`, `1004` (focus reports) and `2004` (bracketed paste) stay. A sequence that is not `CSI ? … h|l` (no `?`, another final byte) passes unchanged whatever its numbers. `NewAltScreenFilter` keeps its signature and behaviour: every mouse mode passes it. The implementation is the smallest change: `altScreenFilter` holds the set of modes it removes (a field), `rewritePrivateModeSeq` takes that set in place of reading the package variable `altScreenModes`, `Write` passes the field, `NewAltScreenFilter` gives the alt-screen set and the new constructor gives the alt-screen set plus the six mouse modes (a second package-level map built once, with a doc comment saying what the mouse modes are and that only the web's pump wants them). The one existing caller of `rewritePrivateModeSeq` is `Write` (grep shows no other use or test).

- [ ] **Step 1: Write the failing tests** in `session/vt/altfilter_test.go`. Add a helper `filterWithoutMouseAll(t *testing.T, chunks ...string) string`, a copy of `filterAll`'s loop (it builds the filter, checks every `Write` reports the full length) over `NewAltScreenFilterWithoutMouse`; `filterAll` itself stays. Add these tests, all with `require`:

  - `TestAltScreenFilterWithoutMouse_StripsEachMouseMode`: for each of `"1000"`, `"1002"`, `"1003"`, `"1005"`, `"1006"`, `"1015"` and each final byte `h` and `l`, `filterWithoutMouseAll(t, "a\x1b[?"+mode+final+"b")` is `"ab"` (use `t.Run` per mode so a failure names the mode).
  - `TestAltScreenFilterWithoutMouse_KeepsTheOtherModesOfASequence`: `"\x1b[?1000;25;1006h"` gives `"\x1b[?25h"`; `"\x1b[?1049;1000;1002;25;1006;1015l"` gives `"\x1b[?25l"`; `"\x1b[?25;1003;2004h"` gives `"\x1b[?25;2004h"`.
  - `TestAltScreenFilterWithoutMouse_DropsASequenceLeftEmpty`: `"a\x1b[?1000;1002;1003;1005;1006;1015hb"` gives `"ab"`, and `"a\x1b[?1049;1006hb"` gives `"ab"`.
  - `TestAltScreenFilterWithoutMouse_HandlesChunkSplitSequences`: `"a\x1b[?10", "06hb"` gives `"ab"`; `"a\x1b[?25;1", "000lb"` gives `"a\x1b[?25lb"`; a sequence split after the `?`, `"a\x1b[?", "1000;1006", "hb"`, gives `"ab"`.
  - `TestAltScreenFilterWithoutMouse_StillStripsTheAltScreenModesAndPassesTheRest`: `"a\x1b[?1049hb"` gives `"ab"`; and a stream that must pass untouched, `"x\x1b[?25l\x1b[?1004h\x1b[?2004h\x1b[?1h\x1b[1000h\x1b[1000;1006H\x1b[?100h\x1b[31mred\x1b[0m\x1b]0;title\x07"`, comes out equal to the input (the modes next to the mouse ones, a CSI with no `?`, a final byte that is not `h` or `l`, and a mode `100` that is only a prefix of `1000`).
  - `TestAltScreenFilter_PassesEveryMouseMode`: through the existing `filterAll`, each of the six modes with `h` and with `l` passes unchanged (`"a\x1b[?"+mode+final+"b"` comes out equal), and `"\x1b[?1000;1049;1006h"` gives `"\x1b[?1000;1006h"` (the alt-screen mode goes, the mouse modes stay).

  Fails without: `TestAltScreenFilterWithoutMouse_StripsEachMouseMode`: the mouse modes missing from the new constructor's set (or one of the six left out: the per-mode subtests name it), or the new constructor returning the old filter. `TestAltScreenFilterWithoutMouse_KeepsTheOtherModesOfASequence`: the rewrite dropping the whole sequence when it holds a removed mode, or joining the kept parameters in another order. `TestAltScreenFilterWithoutMouse_DropsASequenceLeftEmpty`: an empty sequence `\x1b[?h` forwarded when every parameter was removed (the `len(kept) == 0` branch removed). `TestAltScreenFilterWithoutMouse_HandlesChunkSplitSequences`: the new filter not sharing `Write`'s hold-back (a constructor that filters each chunk alone). `TestAltScreenFilterWithoutMouse_StillStripsTheAltScreenModesAndPassesTheRest`: the new set built without `47`, `1047` and `1049`, or a match by prefix or number instead of the whole parameter string, or the shape check on `?` and `h|l` dropped. `TestAltScreenFilter_PassesEveryMouseMode`: `NewAltScreenFilter` given the widened set, so the TUI's emulator would stop seeing the mouse modes.

- [ ] **Step 2: Run them to see them fail**

  Run: `CGO_ENABLED=0 go test ./session/vt/ -run 'TestAltScreenFilterWithoutMouse_|TestAltScreenFilter_PassesEveryMouseMode'`

  Expected: the package does not build: `undefined: NewAltScreenFilterWithoutMouse`, and `FAIL github.com/aidan-bailey/loom/session/vt [build failed]`.

- [ ] **Step 3: Implement** as the behaviour paragraph says, in `session/vt/altfilter.go`. The doc comment of `NewAltScreenFilterWithoutMouse` says: it wraps dst as `NewAltScreenFilter` does and also strips the mouse-reporting private modes (1000, 1002, 1003, 1005, 1006, 1015), for a consumer that must never ask its terminal for mouse reports (the web terminal's output pump), and that the TUI's pump must not use it because its emulator reads those modes. The comment of `rewritePrivateModeSeq` is updated to say it removes the modes of the set it is given. Then, in `session/README.md:174` (the `vt` section's one long line), make two edits and no other. Replace the exact text

  ```
  `vt/altfilter.go` holds `vt.NewAltScreenFilter`.
  ```

  with

  ```
  `vt/altfilter.go` holds `vt.NewAltScreenFilter` and `vt.NewAltScreenFilterWithoutMouse`.
  ```

  and replace the exact text

  ```
  which strips alt-screen mode switches (1047/1049/47) from the client stream.
  ```

  with

  ```
  which strips alt-screen mode switches (1047/1049/47) from the client stream. The web terminal's pump (`internal/web/terminal_conn.go`) writes through `vt.NewAltScreenFilterWithoutMouse` instead, which also strips the mouse modes (1000, 1002, 1003, 1005, 1006, 1015) so the browser's xterm.js never reports the mouse; the TUI's pump keeps the first, since its emulator reads those modes.
  ```

- [ ] **Step 4: Run them to see them pass**

  Run: `CGO_ENABLED=0 go test ./session/vt/ ./tools/claudemd/`

  Expected: `ok  	github.com/aidan-bailey/loom/session/vt` and `ok  	github.com/aidan-bailey/loom/tools/claudemd`.

- [ ] **Step 5: Commit**

  ```
  git commit -m "feat(vt): a second alt-screen filter that also drops the mouse modes" -- session/vt/altfilter.go session/vt/altfilter_test.go session/README.md
  ```

### Task 1.2: The web pump uses it

**Files:**
- Modify: `internal/web/terminal_conn.go:520-526` (`pumpLoop`)
- Modify: `internal/web/terminal_conn_test.go` (after `TestTermConn_TheOutputIsFilteredOfAlternateScreenSwitches`, line 858)
- Modify: `session/vt/CLAUDE.md:8`
- Modify: `internal/web/README.md:28` and `internal/web/README.md:119`

**Behaviour.** `pumpLoop` builds its filter with `vt.NewAltScreenFilterWithoutMouse(termSink{c: c, q: q})` where it builds `vt.NewAltScreenFilter(termSink{c: c, q: q})` now. Its doc comment ("pumpLoop reads the attach's output through the alternate-screen filter into the queue …") says the filter also drops the mouse modes, so the browser's terminal never asks tmux for mouse reports. Nothing else in the file changes; the TUI's `session/tmux/tmux.go:462` is not touched.

- [ ] **Step 1: Write the failing test** in `internal/web/terminal_conn_test.go`, shaped as `TestTermConn_TheOutputIsFilteredOfAlternateScreenSwitches` (same rig: `newTermOpenFake`, `startTermConn`, `sendResize`, `expectLive`, `readBinaryN`, then `rig.conn.end(termEndOff)` and `rig.finish(t)`):

  - `TestTermConn_TheOutputCarriesNoMouseReportRequests`: the fake attach feeds, as two reads so the last sequence is split as the pty can deliver it, `"\x1b[?1049h\x1b[?1000;25;1006hhi \x1b[?10"` and then `"06l\x1b[?1002;1003;1005;1015hthere"`. The browser reads `len("\x1b[?25hhi there")` bytes (14) with `readBinaryN` and the test asserts they equal `"\x1b[?25hhi there"`: the alt-screen switch and every mouse mode gone, the private mode 25 and the text kept in order, the split `1006l` removed, and the sequence of four mouse modes dropped whole.

  Fails without: `TestTermConn_TheOutputCarriesNoMouseReportRequests`: `pumpLoop` calling `vt.NewAltScreenFilter` (the old constructor), so the browser's first bytes are `\x1b[?1000;25;1006h…`, not `\x1b[?25hhi there`.

- [ ] **Step 2: Run it to see it fail**

  Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestTermConn_TheOutputCarriesNoMouseReportRequests'`

  Expected: FAIL, an assertion failure whose `expected` is `"\x1b[?25hhi there"` and whose `actual` begins `"\x1b[?1000;25;1006hhi \x1b[?1006l…"` (alt-screen modes gone, the mouse modes passed).

- [ ] **Step 3: Implement.** Make the one-line change to `pumpLoop` and its comment as the behaviour paragraph says. Then add the rule to `session/vt/CLAUDE.md` and the docs in `internal/web/README.md`.

  Add this rule to `session/vt/CLAUDE.md` as a new bullet directly after line 8, the bullet that begins "- **Keep `vt.NewAltScreenFilter` between the output pump and the emulator**" and ends "so that wiring is **Convention**." (insert after that line, leaving it as it is):

  ```
  - **Call `vt.NewAltScreenFilterWithoutMouse` only from the web terminal's output pump (`pumpLoop` in `internal/web/terminal_conn.go`); the TUI's pump (`session/tmux/tmux.go`) keeps `vt.NewAltScreenFilter`.** While the attach asks for mouse reports (tmux's `mouse on` makes it), xterm.js sends the wheel to tmux and selects nothing, so the web strips modes 1000, 1002, 1003, 1005, 1006 and 1015; the TUI's emulator reads those modes to forward the mouse, so stripping them there silently stops the mouse in every pane. **Enforced** for the filter by the `TestAltScreenFilterWithoutMouse_…` tests and `TestAltScreenFilter_PassesEveryMouseMode`, and for the web's pump by `TestTermConn_TheOutputCarriesNoMouseReportRequests` (`internal/web/terminal_conn_test.go`); the TUI's pump staying on the old constructor is **Convention**: the mouse stops working in a pane, with no error.
  ```

  Check the headroom with `go run ./tools/claudemd -v` (the `session/vt` line was 17/100 lines, 4078/20000 bytes before the rule: room). Then `internal/web/README.md`, two edits and no other:

  In the file table at line 28, replace the exact text

  ```
  the output pump through the alt-screen filter and a bounded buffer
  ```

  with

  ```
  the output pump through the alt-screen filter that also drops the mouse modes, and a bounded buffer
  ```

  In the paragraph at line 119 that begins "Frames: binary frames carry terminal bytes both ways.", replace the exact text

  ```
  once attached, after the history above the visible screen.
  ```

  with

  ```
  once attached, after the history above the visible screen. The attach's output passes through `vt.NewAltScreenFilterWithoutMouse`, which removes the alternate-screen modes and the mouse modes (1000, 1002, 1003, 1005, 1006, 1015) from every `CSI ? … h` and `CSI ? … l`, so the browser's xterm.js never reports the mouse: its wheel scrolls its own scrollback and a drag selects, and a full-screen program in the shell (vim, less, htop) gets no mouse and no wheel. tmux's `mouse` option is not changed, and the TUI's pump keeps `vt.NewAltScreenFilter`.
  ```

- [ ] **Step 4: Run it to see it pass**

  Run: `CGO_ENABLED=0 go test ./session/vt/ ./internal/web/ ./tools/claudemd/`

  Expected: all three `ok`, with `TestTermConn_TheOutputCarriesNoMouseReportRequests` and the existing `TestTermConn_TheOutputIsFilteredOfAlternateScreenSwitches` passing.

- [ ] **Step 5: Commit**

  ```
  git commit -m "feat(web): drop tmux's mouse reports from the terminal's output" -- internal/web/terminal_conn.go internal/web/terminal_conn_test.go session/vt/CLAUDE.md internal/web/README.md
  ```

## Story 2: Touch scrolling and plain text, as modules of their own

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/terminal/touch.ts`, `internal/web/ui/src/terminal/touch.test.ts`, `internal/web/ui/src/terminal/text.ts`, `internal/web/ui/src/terminal/text.test.ts`
- **Interfaces:** exposes `Scroller`, `bindTouchScroll`, `BufferLine` and `plainText` to Story 3; uses nothing of this plan
- **Verify:** `pnpm -C internal/web/ui run check`
- **Review:** light
- **Form:** brief (two pure modules whose tests pin the arithmetic, Task 2.2's momentum loop on animation frames included)

xterm.js 6 binds no touch handler, so the terminal's frame scrolls itself: `touch.ts` turns one finger's vertical drag on an element into `scrollLines` calls on a `Scroller` (spec §38), and `text.ts` turns buffer rows into the plain text the phone's copy sheet shows (spec §33's `text()` as §56 settles it). Neither imports xterm.js, so both are tested here with synthetic events, a fake `Scroller` and a fake clock; Story 3 wires them to the real terminal and `main.ts` (and so the bundle) does not change in this story, which is why no task rebuilds `internal/web/dist/`. The story is brief: each task gives the behaviour, the contract names, and each test with what it asserts and what turns it red; the developer writes the code. The package rules that bind it are `internal/web/ui/CLAUDE.md`'s: both files are plain modules with no xterm.js import (the one place xterm.js may be imported is `main.ts`), no `style` attribute and no HTML from a string, and comment each exported name above it as `socket.ts` does. Prettier's `printWidth` is 100, and `pnpm -C internal/web/ui run check` (typecheck, eslint with the type-checked recommended set, prettier, vitest) is each task's gate. No Go file changes, so the Go story gates (gofmt, `CGO_ENABLED=0 go vet ./...`, `CGO_ENABLED=0 go test -timeout 30m ./...`) pass as they stood.

### Task 2.1: `text.ts`, the buffer's rows as plain text

**Files:**
- Create: `internal/web/ui/src/terminal/text.ts`
- Create: `internal/web/ui/src/terminal/text.test.ts`

**Defines (contract, Story 2):** `interface BufferLine { text: string; wrapped: boolean }` and `function plainText(lines: Iterable<BufferLine>): string`, both exported from `text.ts`. **Uses:** nothing of this plan.

**Behaviour of `plainText`** (spec §33 and §56). A `BufferLine` is one row of the terminal's buffer as it is held, its trailing blanks kept (Story 3 gives it xterm.js's `translateToString` with trimming off), and `wrapped` is true when the row continues the row before it. `plainText` reads `lines` once, in order, and:

1. Groups the rows into logical lines: a row whose `wrapped` is false starts a new logical line, and a row whose `wrapped` is true is appended to the logical line before it (its text concatenated untrimmed). A first row with `wrapped` true starts the first logical line, since nothing precedes it.
2. Cuts the trailing blanks (U+0020 spaces, nothing else) of each logical line once, at its end, never of a row that the next row continues. Leading blanks stay.
3. Drops the empty logical lines at the end (those that are empty after the cut), keeps every empty one before the last non-empty line, and joins the logical lines with `\n`. No rows, or only empty lines, give `""`. There is no trailing `\n`.

It builds a string only: it never mutates its input, and it takes any `Iterable` (a generator, a `Set`, an array), so it must not use `.length` or an index.

- [ ] **Step 1: Write the failing test.** `text.test.ts` imports `plainText` and `type BufferLine` from `./text` and holds one `describe("plainText", …)` with a small helper `row(text, wrapped = false): BufferLine`. The tests, each by name:

  - `gives "" for no rows and for rows with nothing in them`: `plainText([])` is `""`, and so is `plainText([row("   "), row(""), row("  ")])`.
  - `cuts the trailing blanks of each line and keeps the leading ones`: `[row("  indent   "), row("b  ")]` gives `"  indent\nb"`.
  - `joins a wrapped row to the row before it`: `[row("echo hello w"), row("orld   ", true)]` gives `"echo hello world"`.
  - `joins a chain of wrapped rows and cuts only the end of the last`: `[row("aa "), row("bb ", true), row("cc  ", true), row("next  ")]` gives `"aa bb cc\nnext"`.
  - `keeps the blank a wrapped row ends in when the next row continues it`: `[row("ls -la "), row("/tmp   ", true)]` gives `"ls -la /tmp"` (the command wrapped exactly at its space).
  - `cuts a blank row that continues a line along with the line's own trailing blanks`: `[row("abc  "), row("     ", true), row("next")]` gives `"abc\nnext"`.
  - `drops the empty lines at the end and keeps the ones before text`: `[row(""), row("a"), row(""), row("b"), row("   "), row("")]` gives `"\na\n\nb"`.
  - `starts a line at a first row that says it is wrapped`: `[row("x", true), row("y")]` gives `"x\ny"`.
  - `reads any iterable once`: a generator function that yields `row("a")`, `row("b ", true)` and `row("c")` and counts how many times it was started; `plainText(gen())` gives `"ab\nc"`, and the generator was started once.
  - `leaves a tab, which is not a blank cell, where it is`: `[row("a\t")]` gives `"a\t"`.

  Fails without: `gives "" …`: the drop of the trailing empty lines, which would leave `"\n\n"` for the three empty rows, or a throw on an empty input; `cuts the trailing blanks …`: the cut of each logical line's end; `joins a wrapped row …`: the `wrapped` join (the rows would come out as two lines); `joins a chain …`: the join across more than one continuation, or a cut applied to each row (it gives `"aabbcc"`); `keeps the blank a wrapped row ends in …`: trimming each row before joining (it gives `"ls -la/tmp"`); `cuts a blank row that continues …`: cutting only the last row's own blanks instead of the joined line's (it gives `"abc       "` kept); `drops the empty lines at the end …`: the trailing-empty drop (it keeps `"\n\n"` after `b`) or dropping every empty line (it loses the one between `a` and `b`) or dropping the leading one; `starts a line at a first row …`: a join into a missing previous line, which throws or drops `"x"`; `reads any iterable once`: indexing or iterating twice; `leaves a tab …`: a cut by `trimEnd()` or `\s`, which would also take the tab.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/terminal/text.test.ts` fails at once with the import error `Failed to resolve import "./text" from "src/terminal/text.test.ts". Does the file exist?` and `Test Files  1 failed (1)`.

- [ ] **Step 3: Implement `text.ts`** by the behaviour above: export `BufferLine` and `plainText`, each with a comment above it saying what it holds and, for `plainText`, the §56 rule (a row that the next continues keeps its trailing blanks). A cut that removes only U+0020 from the end of the logical line, as a loop or a `/ +$/` replace, not `trimEnd()` (which also takes tabs and other whitespace).

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/terminal/text.test.ts` prints `Test Files  1 passed (1)` and `Tests  10 passed (10)`; then `pnpm -C internal/web/ui run check` passes (typecheck, eslint, prettier, the whole suite, `node --check public/sw.js`). Run `pnpm -C internal/web/ui exec prettier --write src/terminal/text.ts src/terminal/text.test.ts` first if prettier names either file.

- [ ] **Step 5: Commit.**

```bash
git add -N internal/web/ui/src/terminal/text.ts internal/web/ui/src/terminal/text.test.ts
git commit -m "feat(web): the terminal's buffer rows as plain text" -- internal/web/ui/src/terminal/text.ts internal/web/ui/src/terminal/text.test.ts
```

### Task 2.2: `touch.ts`, a finger's drag and momentum as line scrolls

**Files:**
- Create: `internal/web/ui/src/terminal/touch.ts`
- Create: `internal/web/ui/src/terminal/touch.test.ts`

**Defines (contract, Story 2):** `interface Scroller { scrollLines(lines: number): void; cellHeight(): number }` and `function bindTouchScroll(el: HTMLElement, scroller: Scroller): () => void`, both exported from `touch.ts`. `scrollLines` takes a negative count to show older lines, as xterm.js's does, and `cellHeight` is one row's height in CSS pixels. **Uses:** nothing of this plan. (`Scroller` is also the name of an unrelated type in `app/scrollkit`; the two modules never import each other.)

**Behaviour of `bindTouchScroll`** (spec §38). It adds `touchstart`, `touchmove`, `touchend` and `touchcancel` listeners to `el`, `touchstart` and `touchmove` registered with `{ passive: false }` so that `preventDefault` works, and returns the function that removes all four, cancels any momentum frame and ends any drag (calling it twice is harmless). Its state is the drag flag, the last finger y, the clock time of the last move, the carried remainder `acc` in pixels, the last move's velocity in px/ms, and the pending animation-frame handle or none. Time is `performance.now()` throughout (never an event's `timeStamp` or a frame callback's argument), and frames are `requestAnimationFrame` and `cancelAnimationFrame` read from the global at each call. Each listener reads only `event.touches.length` and `event.touches[0].clientY`.

- **`feed(dy)`**, one routine for a drag's moves and for momentum: `acc += dy`; read `h = scroller.cellHeight()` and do nothing more when `h` is not a finite number above 0; `lines = Math.trunc(acc / h)`; when `lines !== 0`, `acc -= lines * h` and call `scroller.scrollLines(-lines)`. So the content follows the finger: a finger moving down by one cell height is `scrollLines(-1)`, which shows an older line, and moving up is a positive count. The remainder under one cell height carries to the next move, and `cellHeight()` is read on every feed, since the terminal's rows can change mid-drag.
- **`touchstart`:** always `preventDefault()` (so no click and no focus follow, and the keyboard never rises) and stop any momentum (cancel its frame, clear the handle). With exactly one touch it starts a drag: drag flag on, `lastY` the touch's `clientY`, `lastT = performance.now()`, `acc = 0`, velocity 0. With more than one touch it ends the drag (flag off), and the drag does not resume when a finger lifts; only a new `touchstart` with exactly one touch starts one.
- **`touchmove`:** always `preventDefault()` (the page does not scroll under the finger). When the drag flag is off it does nothing more. When `touches.length` is not 1 it ends the drag and does nothing more. Otherwise `dy = y - lastY`, `dt = now - lastT`; when `dt > 0` the velocity becomes `dy / dt` (else it keeps the last); then `lastY = y`, `lastT = now`, `feed(dy)`.
- **`touchend`:** when a drag is on and no finger is left (`touches.length === 0`), the drag ends, and momentum starts if `now - lastT <= 100` (a finger held still before lifting is not a flick; 100 ms is this module's own constant) and `|velocity| > 0.3` px/ms (strictly faster). When fingers remain, nothing starts (a second finger already ended the drag).
- **`touchcancel`:** ends the drag and starts no momentum.
- **Momentum:** `v` is the velocity at the lift and `last` is `performance.now()`. It requests a frame; each frame does `dt = performance.now() - last` (at least 0), `last = now`, `feed(v * dt)`, then `v *= 0.95`; when `|v| < 0.02` it stops (handle cleared, no further frame), otherwise it requests the next frame. The first frame moves at the lift's speed, the decay is 0.95 each frame, and a speed under 0.02 px/ms ends it.

The constants are named at the top of the file: `FLICK_MIN = 0.3`, `FLICK_STALE_MS = 100`, `DECAY = 0.95`, `STOP_BELOW = 0.02`.

- [ ] **Step 1: Write the failing test.** `touch.test.ts` imports `bindTouchScroll` and `type Scroller` from `./touch` and builds this harness in the file (no fake timers, so a frame is exactly as long as the test says):

  - A **fake clock and frame queue**: `let now = 0`, `vi.spyOn(performance, "now").mockImplementation(() => now)`; `vi.stubGlobal("requestAnimationFrame", cb => { queue.push({ id: ++nextId, cb }); return nextId; })` and `vi.stubGlobal("cancelAnimationFrame", id => { remove the queue entry with that id })`; a `step(ms = 16)` that does `now += ms` and then runs and removes every callback queued at that moment, passing it `now`; `runFrames(max = 200)` that calls `step()` until the queue is empty or `max` steps were made and returns how many ran. A `beforeEach` resets `now`, the queue and the ids and creates the element (`document.createElement("div")`, appended to `document.body`) and a fake `Scroller` whose `scrollLines` pushes onto `calls: number[]` and whose `cellHeight` returns a variable `cell`, 10 at the start. An `afterEach` calls `vi.unstubAllGlobals()` and `vi.restoreAllMocks()` and unbinds what the test bound.
  - A **touch helper** `touch(type, ys: number[]): Event` that builds `new Event(type, { bubbles: true, cancelable: true })`, defines `touches` on it with `Object.defineProperty` as `ys.map((y) => ({ clientY: y }))` (no `Touch` or `TouchEvent` constructor is needed), dispatches it on the element and returns it, so a test reads `.defaultPrevented`. `touch("touchstart", [300])` is one finger down at y 300; `touch("touchend", [])` is the last finger lifting.
  - A **`drag(from, dys, dt)`** helper: a `touchstart` at `from`, then for each `dy` a `now += dt` and a `touchmove` at the running y.

  The tests, each by name:

  - `a drag down shows older lines, one line a cell height, keeping the remainder`: `touchstart` at 100, `touchmove` to 125 (`calls` is `[-2]`), then to 130 (`calls` is `[-2, -1]`: the 5 px left over joined the next 5). Fails without: the sign flipped (positive counts), or the remainder dropped each move (the second move would scroll nothing), or rounding instead of truncating (125 would give `-3`).
  - `a drag up shows newer lines`: from 100 to 70 gives `[3]`. Fails without: the `-lines` of `scrollLines(-lines)`, which would make dragging up scroll older.
  - `moves under a cell height accumulate until they reach one`: from 100, moves of 4 px each, three times: `calls` is `[]` after the second and `[-1]` after the third (12 px, 2 px left). Fails without: the carried `acc` (nothing would ever scroll) or `acc` kept after a scroll (it would scroll again at once).
  - `reads the cell height at each move`: `cell = 10`, a move of 10 gives `[-1]`; set `cell = 20`, a move of 10 adds nothing and a further move of 10 gives `[-1, -1]`. Fails without: the height cached when the drag starts.
  - `a zero or non-finite cell height scrolls nothing`: with `cell = 0` (and again with `NaN`) a move of 50 calls nothing and throws nothing. Fails without: the guard on `h` (a division by 0 gives `Infinity` lines).
  - `cancels touchstart and touchmove, and registers them as non-passive`: spy `el.addEventListener` before binding and expect the `touchstart` and `touchmove` calls to carry an options object whose `passive` is `false`; then `touch("touchstart", [100]).defaultPrevented` and `touch("touchmove", [110]).defaultPrevented` are both true (and `touchstart` with two fingers is too). Fails without: the `preventDefault` calls (a click and the keyboard would follow), or a passive registration.
  - `carries a flick on at the lift's speed, decaying by 0.95 a frame until under 0.02 px/ms`: cell 10, `touchstart` at 300, then three moves of -16 px at 16 ms each (`now` 16, 32, 48): `calls` is the drag's lines, which sum to 4 (`-16` gives 1 line and 6 px left, then 2, then 1: 4 lines in all, 8 px left, each call positive since the finger moves up). Then `touchend` with no touches at `now` 48 and `runFrames()` returns 77, the queue is then empty, every momentum call is positive, and their sum is 32 (the speed is 1 px/ms: 16 px a frame for 77 frames summing to 313.8 px, plus the 8 px carried, is 321.8 px, 32 lines). Fails without: the decay (it never ends, so `runFrames` hits its cap of 200), a decay factor of 0.9 or 0.99 (the frame count and the sum change), the threshold of 0.02 (the count changes), or `feed` not being shared with the drag (the 8 px carried would be lost and the sum would be 31).
  - `carries a flick on in the direction the finger moved`: the same drag mirrored (moves of +16 px from y 100): the drag's lines are negative, and the momentum's calls are negative and sum to -32. Fails without: the momentum using the speed's magnitude instead of its sign.
  - `a lift at 0.3 px/ms or less carries on no further, and one above it does`: three runs, each a `touchstart`, one `touchmove` and a `touchend` with no fingers: a move of 4 px in 20 ms (0.2) leaves the frame queue empty; 6 px in 20 ms (0.3, exactly the threshold) leaves it empty; 8 px in 20 ms (0.4) leaves one frame queued. Fails without: the threshold (0.2 would carry on), or `>=` for `>` (0.3 would carry on), or no flick at all (0.4 would not).
  - `a finger held still before it lifts carries on no further`: a fast move (16 px in 16 ms), then `now += 150`, then `touchend`: the queue is empty. The same with `now += 90` leaves one frame queued. Fails without: the `FLICK_STALE_MS` check (a held finger would fling the content).
  - `a new touch stops the momentum`: after a flick, `step()` twice (calls were made), then `touchstart`: the queue is empty, and `step()` five more times adds no call. Fails without: the stop in `touchstart` (the content would keep running under the new touch).
  - `a second finger ends the drag, and the finger left does not resume it`: `touchstart` with `[100]`, a `touchmove` to 130 gives `[-3]`; `touchstart` with `[130, 200]`; `touchmove` with `[150, 220]` adds nothing; `touchend` with `[150]` (one finger left) leaves the frame queue empty and adds nothing; `touchmove` with `[190]` adds nothing; `touchend` with `[]` leaves the queue empty. A `touchstart` with `[100]` afterwards starts a drag again: a move to 120 adds `-2`. Fails without: the drag flag cleared on a second finger (the first pair of moves would scroll), the drag resuming on the remaining finger, or momentum after a multi-finger gesture.
  - `a moving drag ended by touchmove with two fingers scrolls nothing further`: `touchstart` `[100]`, then `touchmove` with `[120, 300]` adds nothing and the next `touchmove` with `[140]` adds nothing. Fails without: the `touches.length` check in `touchmove`.
  - `touchcancel ends the drag with no momentum`: a fast drag then `touch("touchcancel", [])`: the queue is empty and a following `touchmove` adds nothing. Fails without: the `touchcancel` listener (the drag would go on, or momentum would start).
  - `the unbind stops everything`: bind (keep the returned function as `unbind`), `drag` fast and lift so a frame is queued, call `unbind()`: the queue is empty, `step()` adds no call, and later `touchstart`, `touchmove` and `touchend` events are not `defaultPrevented` and add no call. Calling `unbind()` a second time does not throw. Fails without: removing the listeners (the later events would be prevented and scroll), cancelling the frame (the momentum would run on after the unbind).

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/terminal/touch.test.ts` fails at once with `Failed to resolve import "./touch" from "src/terminal/touch.test.ts". Does the file exist?` and `Test Files  1 failed (1)`.

- [ ] **Step 3: Implement `touch.ts`** by the behaviour above and the four constants: export `Scroller` and `bindTouchScroll`, with a comment above each. Type the listeners as `(event: TouchEvent) => void`; the test's events are plain `Event`s given a `touches` property, so read only `touches.length` and `touches[0]?.clientY` (a `touchstart` or `touchmove` with no first touch does nothing more than `preventDefault`). The handle from `requestAnimationFrame` is a `number`. No import of xterm.js, and nothing is added to `main.ts` here.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/terminal/touch.test.ts` prints `Test Files  1 passed (1)` and `Tests  15 passed (15)`; then `pnpm -C internal/web/ui run check` passes. Run `pnpm -C internal/web/ui exec prettier --write src/terminal/touch.ts src/terminal/touch.test.ts` first if prettier names either file. `internal/web/dist/` is unchanged (nothing imports the module yet), so `pnpm -C internal/web/ui run dist:check` needs no rebuild.

- [ ] **Step 5: Commit.**

```bash
git add -N internal/web/ui/src/terminal/touch.ts internal/web/ui/src/terminal/touch.test.ts
git commit -m "feat(web): touch scrolling for the terminal frame" -- internal/web/ui/src/terminal/touch.ts internal/web/ui/src/terminal/touch.test.ts
```

## Story 3: The frame's interface, reconnecting, the link and the keys

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/terminal/frame.ts`, `internal/web/ui/src/terminal/frame.test.ts`, `internal/web/ui/src/terminal/reconnect.ts`, `internal/web/ui/src/terminal/reconnect.test.ts`, `internal/web/ui/src/terminal/keys.ts`, `internal/web/ui/src/terminal/keys.test.ts`, `internal/web/ui/src/terminal/socket.ts`, `internal/web/ui/src/terminal/socket.test.ts`, `internal/web/ui/src/terminal/main.ts`, `internal/web/ui/src/terminal/terminal.css`, `internal/web/dist/**`, `internal/web/README.md`
- **Interfaces:** exposes `KeyName`, `Phase`, `Renderer`, `FrameState`, `LoomTerminal`, `frameTerminal`, `window.loomTerminal` and the `CLOSE_` constants to Stories 4 and 5; uses Story 2's `bindTouchScroll`, `Scroller`, `BufferLine` and `plainText`
- **Verify:** `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** full (a reconnecting state machine over timers, visibility and an outside connection, and a reset that must land before the history)

The terminal's document becomes something a panel can drive (spec §33–§37, §55, §56, §59, §60). `frame.ts` holds the interface's types, the global `window.loomTerminal` and `frameTerminal`, the one reader of a frame's window; it imports nothing of xterm.js. `reconnect.ts` holds the connection policy and its controller, `createLink`: which endings reconnect (1001, 4005 and an ending with no close frame, which `socket.ts` reports as code 1006, whether it came before the open or after), the backoff (1 s doubling to 30 s, back to 1 s only once a connection has stayed live for 30 s, spec §66), an immediate reconnect when the page becomes visible, the link that holds every new connection while the app's snapshot is stale, a fresh connection in place of a `live` one when the link comes back up or the page becomes visible after 30 s or longer hidden, and a reconnect when the page is restored from the back-forward cache; it owns no WebSocket, so Vitest drives it with fake sockets and fake timers, and it also holds the 100 ms size debounce `lastCall` and `freshScreen`, which writes RIS (`ESC c`) to the terminal ahead of a new connection's first bytes (spec §65). `keys.ts` gives each named key's bytes in both cursor modes and decides what a key event in the frame does (⌘K to the chord callback, Ctrl+Shift+C copying the selection, Ctrl+Shift+V left to the browser, Ctrl+K and everything else to the shell). `socket.ts` exports the daemon's close codes. `main.ts` sets `window.loomTerminal` as the script first runs, wires the link, the keys and Story 2's `bindTouchScroll` and `plainText` to xterm.js, gives the link `freshScreen` for that reset, reports a start that throws as a `closed` state with code 0 (`startFailed` in `frame.ts`, spec §68), reports the renderer, and keeps its part 1 status line only on a page that is not in a frame. A page that is not in a frame behaves as if the link were up. `send` and `key` answer whether the bytes went to an open connection and queue nothing (spec §55). None of this code holds a landmine marker.

### Task 3.1: The close codes in `socket.ts`

**Files:**
- Modify: `internal/web/ui/src/terminal/socket.ts:1-8`
- Test: `internal/web/ui/src/terminal/socket.test.ts`

**Defines (contract, Story 3):** `CLOSE_STOPPING = 1001`, `CLOSE_OFF = 4001`, `CLOSE_REVOKED = 4002`, `CLOSE_SESSION = 4003`, `CLOSE_EXITED = 4004`, `CLOSE_SLOW = 4005`, `CLOSE_BAD_FRAME = 4006`, `CLOSE_NO_ATTACH = 4007`, exported constants of `socket.ts`; `describeClose` stays as it is. The values are the daemon's (`termEndOff` and its neighbours in `internal/web/terminal_conn.go`).

- [ ] **Step 1: Write the failing test.** In `socket.test.ts`, add the eight names to the import list, and a `describe` before `describe("input bytes", () => {`.

Quote the import's first lines:

```ts
import {
  FRAME_MAX,
  binaryBytes,
```

and make them:

```ts
import {
  CLOSE_BAD_FRAME,
  CLOSE_EXITED,
  CLOSE_NO_ATTACH,
  CLOSE_OFF,
  CLOSE_REVOKED,
  CLOSE_SESSION,
  CLOSE_SLOW,
  CLOSE_STOPPING,
  FRAME_MAX,
  binaryBytes,
```

Then add, immediately above the line `describe("input bytes", () => {`:

```ts
describe("close codes", () => {
  it("are the codes the daemon sends", () => {
    expect([
      CLOSE_STOPPING,
      CLOSE_OFF,
      CLOSE_REVOKED,
      CLOSE_SESSION,
      CLOSE_EXITED,
      CLOSE_SLOW,
      CLOSE_BAD_FRAME,
      CLOSE_NO_ATTACH,
    ]).toEqual([1001, 4001, 4002, 4003, 4004, 4005, 4006, 4007]);
  });
});

```

Fails without: `are the codes the daemon sends`: the eight `export const` lines in `socket.ts` (each import is `undefined`), or any one of them with another value.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/terminal/socket.test.ts` prints `AssertionError: expected [ undefined, undefined, undefined, …(5) ] to deeply equal [ 1001, 4001, 4002, 4003, 4004, 4005, 4006, 4007 ]` for `close codes > are the codes the daemon sends`, and `Test Files  1 failed (1)`.

- [ ] **Step 3: Implement.** In `socket.ts`, add the constants after `FRAME_MAX`. Quote:

```ts
/** The longest frame the daemon accepts from the page (`termFrameMax` in internal/web/terminal_conn.go). */
export const FRAME_MAX = 64 * 1024;
```

and add directly below it:

```ts

// The close codes the daemon sends (`termEndOff` and its neighbours in
// internal/web/terminal_conn.go). reconnect.ts decides from them which endings reconnect.
/** 1001: the daemon is stopping, or the listener is rebinding. */
export const CLOSE_STOPPING = 1001;
/** 4001: the terminal is off on this host. */
export const CLOSE_OFF = 4001;
/** 4002: this device is no longer paired. */
export const CLOSE_REVOKED = 4002;
/** 4003: the session was paused or killed. */
export const CLOSE_SESSION = 4003;
/** 4004: the shell exited. */
export const CLOSE_EXITED = 4004;
/** 4005: the browser fell behind. */
export const CLOSE_SLOW = 4005;
/** 4006: a frame the daemon does not understand. */
export const CLOSE_BAD_FRAME = 4006;
/** 4007: the daemon could not attach to the shell. */
export const CLOSE_NO_ATTACH = 4007;
```

Then correct the header comment, whose second line is stale. Replace

```ts
// The terminal page's socket (spec §17): one WebSocket to the daemon's terminal route, as a small
// state machine with no reconnecting (part 2's panel adds that). The first frame the page sends is
```

with

```ts
// The terminal page's socket (spec §17): one WebSocket to the daemon's terminal route, as a small
// state machine with no reconnecting of its own (reconnect.ts decides which endings reconnect and
// opens the next socket). The first frame the page sends is
```

and run `pnpm -C internal/web/ui exec prettier --write src/terminal/socket.ts src/terminal/socket.test.ts`.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/terminal/socket.test.ts` prints `Test Files  1 passed (1)`, and every test of the file passes.

- [ ] **Step 5: Commit.**

```bash
git commit -m "feat(web): the terminal socket's close codes as constants" -- internal/web/ui/src/terminal/socket.ts internal/web/ui/src/terminal/socket.test.ts
```

### Task 3.2: `frame.ts`, the frame's interface and its accessor

**Files:**
- Create: `internal/web/ui/src/terminal/frame.ts`
- Create: `internal/web/ui/src/terminal/frame.test.ts`

**Defines (contract, Story 3):** `KeyName`, `Phase`, `Renderer`, `FrameState`, `LoomTerminal`, `frameTerminal`, and the global `Window.loomTerminal`, all in `frame.ts`, exactly as the contract gives them. **Defines (private to this story):** `startFailed(err, renderer)` in `frame.ts`, the state of a terminal that could not start (spec §68). **Uses:** `Closed` from `socket.ts`, as a type only: `frame.ts` imports nothing of xterm.js, and the app's entry bundle will import it.

- [ ] **Step 1: Write the failing test.** Create `internal/web/ui/src/terminal/frame.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";

import { frameTerminal, startFailed, type LoomTerminal } from "./frame";

// A LoomTerminal of stubs.
function stub(): LoomTerminal {
  return {
    send: vi.fn(() => true),
    key: vi.fn(() => true),
    text: vi.fn(() => ""),
    setTouch: vi.fn(),
    setLink: vi.fn(),
    reconnect: vi.fn(),
    focus: vi.fn(),
    close: vi.fn(),
    onState: vi.fn(),
    onChord: vi.fn(),
  };
}

// A frame whose window is `win`: the accessor reads only `contentWindow`.
function frameWith(win: unknown): HTMLIFrameElement {
  return { contentWindow: win } as unknown as HTMLIFrameElement;
}

describe("frameTerminal", () => {
  it("reads the interface off the frame's window", () => {
    const term = stub();
    expect(frameTerminal(frameWith({ loomTerminal: term }))).toBe(term);
  });

  it("is null while the frame's script has not run", () => {
    expect(frameTerminal(frameWith({}))).toBeNull();
  });

  it("is null for a frame with no window", () => {
    expect(frameTerminal(frameWith(null))).toBeNull();
  });

  it("is null when the frame's window cannot be read", () => {
    const frame = {
      get contentWindow(): never {
        throw new DOMException("blocked", "SecurityError");
      },
    } as unknown as HTMLIFrameElement;
    expect(frameTerminal(frame)).toBeNull();
  });
});

describe("startFailed", () => {
  it("is a closed state with code 0 and the failure's text as its reason", () => {
    expect(startFailed(new Error("WebGL is gone"), "webgl")).toEqual({
      phase: "closed",
      closed: { code: 0, reason: "The terminal failed to start: WebGL is gone" },
      renderer: "webgl",
    });
  });

  it("says what it does not know when the failure has no text", () => {
    const reason = "The terminal failed to start: unknown error";
    expect(startFailed(new Error(""), "dom").closed).toEqual({ code: 0, reason });
    expect(startFailed("boom", "dom").closed).toEqual({ code: 0, reason });
  });

  it("is not waiting for a retry", () => {
    expect(startFailed(new Error("x"), "dom").retryAt).toBeUndefined();
  });
});
```

Fails without: `reads the interface off the frame's window`: the read of `contentWindow.loomTerminal` (a function returning null always fails it); `is null while the frame's script has not run`: the `?? null` (the accessor would answer `undefined`, which `toBeNull` refuses); `is null for a frame with no window`: the `?.` on `contentWindow` (a throw on null); `is null when the frame's window cannot be read`: the `try`/`catch` (the SecurityError would reach the caller); `is a closed state with code 0 and the failure's text as its reason`: `startFailed`'s phase, its code 0, the `The terminal failed to start: ` prefix, or the error's `message` (a fixed text would fail it); `says what it does not know when the failure has no text`: the `unknown error` fallback, for an `Error("")` and for a value that is not an `Error`; `is not waiting for a retry`: a `retryAt` set on the state (a closed state has none coming).

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/terminal/frame.test.ts` fails with `Failed to resolve import "./frame" from "src/terminal/frame.test.ts". Does the file exist?` and `Test Files  1 failed (1)`.

- [ ] **Step 3: Implement.** Create `frame.ts`:

```ts
// The interface the terminal's document gives the app (spec §33). The document's script
// (main.ts) sets `window.loomTerminal`; the panel in the app reads it off the loaded frame through
// `frameTerminal` and drives the terminal only through it, never through the frame's DOM. This
// module imports nothing of xterm.js, so the app's entry bundle may import it.
import type { Closed } from "./socket";

/** A key the phone's key row sends; the frame encodes it (the arrows depend on the cursor mode). */
export type KeyName = "up" | "down" | "left" | "right" | "tab" | "esc" | "ctrl-c" | "ctrl-d";

/** The connection's phase: `waiting` is a reconnect scheduled or held, `closed` has none coming. */
export type Phase = "connecting" | "live" | "waiting" | "closed";

/** The renderer xterm.js draws with. */
export type Renderer = "webgl" | "dom";

export interface FrameState {
  phase: Phase;
  /** The close, set only while `closed`; code 0 means the terminal could not start (spec §68). */
  closed?: Closed;
  /** The time of the next attempt, in milliseconds since the epoch, set only while `waiting`. */
  retryAt?: number;
  renderer: Renderer;
}

export interface LoomTerminal {
  /**
   * Types bytes into the shell and scrolls the terminal to the bottom. True when they went to an
   * open connection; false otherwise, and then they are dropped, never queued.
   */
  send(bytes: Uint8Array<ArrayBuffer>): boolean;
  /** `send` of a named key's bytes, in the cursor mode the program has set. */
  key(name: KeyName): boolean;
  /** The whole buffer, scrollback and screen, as plain text; "" until the terminal has started. */
  text(): string;
  /** The phone's mode: the terminal takes no focus and a finger scrolls it. */
  setTouch(on: boolean): void;
  /** Whether the app's snapshot is fresh; while it is not, no new connection starts. */
  setLink(up: boolean): void;
  /** Connects again, from any state, at once when the link is up. */
  reconnect(): void;
  focus(): void;
  /** Ends the connection for good. */
  close(): void;
  /** Holds one callback, the last one given, and calls it at once with the current state. */
  onState(cb: (state: FrameState) => void): void;
  /** Holds one callback, the last one given: ⌘K was pressed inside the frame. */
  onChord(cb: () => void): void;
}

declare global {
  interface Window {
    /** Set by the terminal document's script as it first runs, before the font has loaded. */
    loomTerminal?: LoomTerminal;
  }
}

/**
 * The state of a terminal whose start threw (spec §68): `closed`, with code 0 and the failure's
 * text as the reason, which the panel shows as it shows any closed reason.
 */
export function startFailed(err: unknown, renderer: Renderer): FrameState {
  const text = err instanceof Error && err.message !== "" ? err.message : "unknown error";
  return {
    phase: "closed",
    closed: { code: 0, reason: `The terminal failed to start: ${text}` },
    renderer,
  };
}

/**
 * The interface of a loaded frame, or null while its script has not run (or its window cannot be
 * read). It is the only code outside the frame that reads the frame's window.
 */
export function frameTerminal(frame: HTMLIFrameElement): LoomTerminal | null {
  try {
    return frame.contentWindow?.loomTerminal ?? null;
  } catch {
    return null;
  }
}
```

Run `pnpm -C internal/web/ui exec prettier --write src/terminal/frame.ts src/terminal/frame.test.ts`.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/terminal/frame.test.ts` prints `Test Files  1 passed (1)` and `Tests  7 passed (7)`; `pnpm -C internal/web/ui run typecheck` passes.

- [ ] **Step 5: Commit.**

```bash
git add -N internal/web/ui/src/terminal/frame.ts internal/web/ui/src/terminal/frame.test.ts
git commit -m "feat(web): the terminal frame's typed interface and its accessor" -- internal/web/ui/src/terminal/frame.ts internal/web/ui/src/terminal/frame.test.ts
```

### Task 3.3: `reconnect.ts`, the policy, the link and the size debounce

**Files:**
- Create: `internal/web/ui/src/terminal/reconnect.ts`
- Create: `internal/web/ui/src/terminal/reconnect.test.ts`

**Defines (private to this story):** `FIRST_DELAY = 1000`, `MAX_DELAY = 30_000`, `STABLE_LIVE = 30_000`, `LONG_HIDDEN = 30_000`, `SIZE_SETTLE = 100`, `RIS`, `reconnects(closed)`, `nextDelay(delay)`, `lastCall(fn, ms)`, `freshScreen(target)` with the type `ScreenSink`, and `createLink(opts)` with the types `LinkState`, `LinkOptions` and `Link`. **Uses:** `Phase` from `frame.ts`; `CLOSE_STOPPING`, `CLOSE_SLOW`, `Closed`, `Handlers` and `TerminalSocket` from `socket.ts`.

**The controller's behaviour** (spec §34, §35, §59, §60, §65, §66). `createLink(opts)` returns a `Link` and owns no WebSocket: `opts.open(on)` makes one connection (main passes `openSocket`; a test passes a fake) and `on` is `socket.ts`'s `Handlers`. It binds `visibilitychange` on `document` and `pagehide` and `pageshow` on `window`, and `dispose()` unbinds them and ends the current connection without a state change. Its state is a phase, `closed`, `retryAt`, the backoff delay, whether the link is up (`!opts.framed` at first, so a page not in a frame behaves as if the link were up), whether `start()` has run, whether `close()` has run (`final`), and whether `pagehide` ended the connection (`suspended`). Every connection has a generation number; a handler call from a connection that has been replaced or dropped changes nothing (a real `TerminalSocket.close()` reports a close with code 1000 at once, and a replaced connection's late events must not reach the phase).

- `reconnects(closed)` is true for exactly `CLOSE_STOPPING` (1001), `CLOSE_SLOW` (4005) and 1006, the code `socket.ts` reports for a close with no close frame and for a failure before the open. Every other code, 1000, 1005, 1011 and 4001–4004, 4006, 4007 included, is a refusal.
- `nextDelay(delay)` is `Math.min(delay * 2, MAX_DELAY)`.
- `connect()` drops the current connection (generation + 1, the timer cleared, the old socket's `close()` called), sets the phase `connecting`, and calls `opts.open` with handlers that act only while their generation is current: `live` calls `fresh` (once per connection, with the first bytes, below), sets the phase `live` and starts a timer of `STABLE_LIVE` (30 s) that, if it fires, sets the delay back to `FIRST_DELAY` (spec §66: a connection that ends sooner, whatever ended it, leaves the delay doubling); `closed` ends the connection (below); `data` calls `fresh` once and then `opts.data`. A throw from `opts.open` is a failure before the open: it ends as a close with code 1006.
- The `STABLE_LIVE` timer is cleared wherever the other timers are: `drop` (which `connect`, `reconnect`, `close`, `pagehide` and `dispose` all call) and `ended`.
- A connection ends with `ended(closed)`: when `reconnects(closed)` the phase is `waiting` with `retryAt = Date.now() + delay`, a timer for `delay` is set and the delay becomes `nextDelay(delay)`; otherwise the phase is `closed` with that close. When the timer fires, a connection starts if the link is up, else the reconnect is held (phase stays `waiting`) until the link comes up.
- `fresh` is called once per connection, before the first byte or the `live` state reaches the terminal, whichever comes first, so a connection that fails before sending anything leaves the old screen as it was (spec §36). `freshScreen(target)` answers the `fresh` that main gives: it writes `RIS` (`ESC c`, the bytes 0x1b 0x63) to what `target()` answers, if anything (spec §65). The reset is in-band: xterm.js's own `reset()` leaves its parser where the old connection's last bytes left it, so a connection cut inside an escape sequence would swallow the start of the next history, and RIS is ordered after the old bytes by the write queue itself.
- `start()` marks the link started and connects if the link is up and `close()` has not run. `setLink(up)` before `start()` only records it.
- `setLink(true)`, after being down, connects at once when the page is started, not closed by `close()`, not suspended, and the phase is `live` (the connection may be half-open: replace it, spec §60) or is not `closed` and no connection exists (a reconnect waiting, scheduled or held, or a first connection held for the link). A connection still being made is left. `setLink(false)` changes nothing but the flag: a live connection is left alone, and a scheduled reconnect waits.
- `reconnect()` (unless `close()` has run) sets the delay to `FIRST_DELAY`, clears `suspended`, drops the current connection, sets the phase `connecting`, and connects when started and the link is up (else `setLink(true)` or `start()` connects later).
- `close()` (once) drops the connection for good and sets the phase `closed` with `{ code: 1000, reason: "" }`; after it nothing connects.
- Visibility: when the page becomes hidden it records the time. When it becomes visible: if the page is started, not final, not suspended and the link is up, then a `waiting` phase connects at once (the delay back to `FIRST_DELAY`; the scheduled timer is dropped), and a `live` phase is replaced when the page was hidden for `LONG_HIDDEN` or longer (spec §60; the daemon's 20 s ping and 10 s wait).
- `pagehide` (when started, not final and not `closed`): `suspended` is set, the connection is dropped (closed normally, as part 1's `pagehide` did) and the phase is `waiting` with no `retryAt` and no timer. `pageshow` with `persisted` and `suspended`: `suspended` is cleared and a connection starts when the link is up and the page is started (else `setLink(true)` starts it). A page that ended in a refusal, or was closed by `close()`, is never reconnected by `pageshow`.
- `send(bytes)` is true only while the phase is `live` and the socket accepts the bytes; `resize` forwards to the current socket's `resize`.
- `state()` answers `{ phase }` plus `closed` and `retryAt` when they are set. `opts.changed(state)` is called whenever phase, `closed` or `retryAt` change, never twice for the same state.
- `lastCall(fn, ms)` answers a function that calls `fn` with the arguments of its latest call once `ms` have passed with no newer call, and has `cancel()`.

- [ ] **Step 1: Write the failing test.** Create `internal/web/ui/src/terminal/reconnect.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  FIRST_DELAY,
  LONG_HIDDEN,
  MAX_DELAY,
  RIS,
  SIZE_SETTLE,
  STABLE_LIVE,
  createLink,
  freshScreen,
  lastCall,
  nextDelay,
  reconnects,
  type Link,
  type LinkState,
  type ScreenSink,
} from "./reconnect";
import type { Handlers, TerminalSocket } from "./socket";

// One connection the link opened: it records what the link does to it and plays the daemon's
// side. close() reports a normal close at once, as the real socket does, so a test sees whether the
// link ignores a replaced connection's late events.
class Conn {
  closes = 0;
  sent: Uint8Array[] = [];
  accepts = true;
  readonly sock: TerminalSocket;

  constructor(private readonly on: Handlers) {
    this.sock = {
      send: (bytes) => {
        if (this.accepts) this.sent.push(bytes);
        return this.accepts;
      },
      resize: () => undefined,
      close: () => {
        this.closes++;
        this.on.state("closed", { code: 1000, reason: "" });
      },
    };
  }

  live(): void {
    this.on.state("live");
  }

  data(...bytes: number[]): void {
    this.on.data(new Uint8Array(bytes));
  }

  end(code: number, reason = ""): void {
    this.on.state("closed", { code, reason });
  }
}

interface Rig {
  link: Link;
  conns: Conn[];
  log: string[];
  states: LinkState[];
}

const links: Link[] = [];

function rig(framed = false): Rig {
  const conns: Conn[] = [];
  const log: string[] = [];
  const states: LinkState[] = [];
  const link = createLink({
    framed,
    open: (on) => {
      const conn = new Conn(on);
      conns.push(conn);
      return conn.sock;
    },
    fresh: () => {
      log.push("fresh");
    },
    data: (bytes) => {
      log.push(`data:${Array.from(bytes).join(",")}`);
    },
    changed: (state) => {
      states.push(state);
    },
  });
  links.push(link);
  return { link, conns, log, states };
}

function last(r: Rig): Conn {
  const conn = r.conns.at(-1);
  if (conn === undefined) throw new Error("the link opened no connection");
  return conn;
}

// The page's visibility, with the event the browser fires.
function visibility(state: "visible" | "hidden"): void {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  document.dispatchEvent(new Event("visibilitychange"));
}

function pageshow(persisted: boolean): void {
  const event = new Event("pageshow");
  Object.defineProperty(event, "persisted", { value: persisted });
  window.dispatchEvent(event);
}

function pagehide(): void {
  window.dispatchEvent(new Event("pagehide"));
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(1_000_000);
});

afterEach(() => {
  for (const link of links) link.dispose();
  links.length = 0;
  Reflect.deleteProperty(document, "visibilityState");
  vi.useRealTimers();
});

describe("reconnects", () => {
  it.each([1001, 4005, 1006])("reconnects after %i", (code) => {
    expect(reconnects({ code, reason: "" })).toBe(true);
  });

  it.each([1000, 1005, 1011, 4001, 4002, 4003, 4004, 4006, 4007])(
    "does not reconnect after %i",
    (code) => {
      expect(reconnects({ code, reason: "x" })).toBe(false);
    },
  );

  it("doubles the delay up to 30 s and no further", () => {
    expect(nextDelay(FIRST_DELAY)).toBe(2000);
    expect(nextDelay(16_000)).toBe(MAX_DELAY);
    expect(nextDelay(MAX_DELAY)).toBe(MAX_DELAY);
  });
});

describe("lastCall", () => {
  it("calls once, with the last arguments, after the quiet period", () => {
    const fn = vi.fn();
    const call = lastCall(fn, SIZE_SETTLE);
    call(1, 2);
    vi.advanceTimersByTime(60);
    call(3, 4);
    vi.advanceTimersByTime(SIZE_SETTLE - 1);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith(3, 4);
    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("calls nothing once cancelled", () => {
    const fn = vi.fn();
    const call = lastCall(fn, SIZE_SETTLE);
    call(1);
    call.cancel();
    vi.advanceTimersByTime(1000);
    expect(fn).not.toHaveBeenCalled();
  });
});

describe("the link", () => {
  it("connects at start when the page is not in a frame", () => {
    const r = rig();
    expect(r.conns).toHaveLength(0);
    r.link.start();
    expect(r.conns).toHaveLength(1);
    expect(r.link.state()).toEqual({ phase: "connecting" });
  });

  it("holds the first connection until the link is up when it is in a frame", () => {
    const r = rig(true);
    r.link.start();
    expect(r.conns).toHaveLength(0);
    expect(r.link.state()).toEqual({ phase: "connecting" });

    r.link.setLink(true);
    expect(r.conns).toHaveLength(1);
    r.link.setLink(true);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(1);
  });

  it("takes what was set before it started when it starts", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.reconnect();
    expect(r.conns).toHaveLength(0);
    r.link.start();
    expect(r.conns).toHaveLength(1);

    const closed = rig();
    closed.link.close();
    closed.link.start();
    expect(closed.conns).toHaveLength(0);
    expect(closed.link.state()).toEqual({ phase: "closed", closed: { code: 1000, reason: "" } });
  });

  it("is live when the daemon says so, and tells each change once", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    last(r).live();
    expect(r.link.state()).toEqual({ phase: "live" });
    last(r).end(1001, "loom is stopping");
    const retryAt = Date.now() + FIRST_DELAY;
    expect(r.link.state()).toEqual({ phase: "waiting", retryAt });
    vi.advanceTimersByTime(FIRST_DELAY);
    expect(r.states).toEqual([
      { phase: "live" },
      { phase: "waiting", retryAt },
      { phase: "connecting" },
    ]);
  });

  it("retries on a backoff of 1 s doubling to 30 s", () => {
    const r = rig();
    r.link.start();
    const waits: number[] = [];
    for (let i = 0; i < 8; i++) {
      last(r).end(1006);
      const state = r.link.state();
      expect(state.phase).toBe("waiting");
      const wait = (state.retryAt ?? 0) - Date.now();
      waits.push(wait);
      vi.advanceTimersByTime(wait - 1);
      expect(r.conns).toHaveLength(i + 1);
      vi.advanceTimersByTime(1);
      expect(r.conns).toHaveLength(i + 2);
    }
    expect(waits).toEqual([1000, 2000, 4000, 8000, 16_000, 30_000, 30_000, 30_000]);
  });

  // Two failures before anything is live: the next wait is 4 s.
  function failTwice(r: Rig): void {
    r.link.start();
    last(r).end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    last(r).end(1006);
    vi.advanceTimersByTime(2 * FIRST_DELAY);
  }

  function wait(r: Rig): number {
    return (r.link.state().retryAt ?? 0) - Date.now();
  }

  it.each([1001, 4005, 1006])(
    "keeps the delay doubling when a connection ends after %i before 30 s live",
    (code) => {
      const r = rig();
      failTwice(r);
      last(r).live();
      vi.advanceTimersByTime(STABLE_LIVE - 1);
      last(r).end(code);
      expect(wait(r)).toBe(4000);
      // The timer of the ended connection must not fire later and reset the delay.
      vi.advanceTimersByTime(4000);
      last(r).end(1006);
      expect(wait(r)).toBe(8000);
    },
  );

  it("goes back to 1 s once a connection has stayed live for 30 s", () => {
    const r = rig();
    failTwice(r);
    last(r).live();
    vi.advanceTimersByTime(STABLE_LIVE);
    last(r).end(1006);
    expect(wait(r)).toBe(FIRST_DELAY);
    vi.advanceTimersByTime(FIRST_DELAY);
    last(r).end(1006);
    expect(wait(r)).toBe(2 * FIRST_DELAY);
  });

  it("does not let a replaced connection's 30 s timer reset the delay", () => {
    const r = rig(true);
    r.link.setLink(true);
    failTwice(r);
    last(r).live();
    vi.advanceTimersByTime(STABLE_LIVE - 1000);
    r.link.setLink(false);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(4);
    vi.advanceTimersByTime(2000);
    last(r).end(1006);
    expect(wait(r)).toBe(4000);
  });

  it("retries a connection that failed before it opened", () => {
    const r = rig();
    r.link.start();
    expect(r.link.state()).toEqual({ phase: "connecting" });
    last(r).end(1006);
    expect(r.link.state().phase).toBe("waiting");
    vi.advanceTimersByTime(FIRST_DELAY);
    expect(r.conns).toHaveLength(2);
  });

  it("retries a socket that cannot be made", () => {
    let made = 0;
    const link = createLink({
      framed: false,
      open: () => {
        made++;
        throw new SyntaxError("bad url");
      },
      fresh: () => undefined,
      data: () => undefined,
      changed: () => undefined,
    });
    links.push(link);
    link.start();
    expect(link.state().phase).toBe("waiting");
    vi.advanceTimersByTime(FIRST_DELAY);
    expect(made).toBe(2);
  });

  it.each([
    [4001, "the terminal is off on this host"],
    [4002, "this device is no longer paired"],
    [4003, "the session was paused or killed"],
    [4004, "the shell exited"],
    [4006, "a frame loom does not understand"],
    [4007, "loom could not attach to the shell"],
  ])("ends closed after %i and never reconnects on its own", (code, reason) => {
    const r = rig();
    r.link.start();
    last(r).live();
    last(r).end(code, reason);
    expect(r.link.state()).toEqual({ phase: "closed", closed: { code, reason } });
    vi.advanceTimersByTime(10 * MAX_DELAY);
    visibility("hidden");
    visibility("visible");
    expect(r.conns).toHaveLength(1);
  });

  it("reconnects from closed at once when asked", () => {
    const r = rig();
    r.link.start();
    last(r).end(4004, "the shell exited");
    r.link.reconnect();
    expect(r.conns).toHaveLength(2);
    expect(r.link.state()).toEqual({ phase: "connecting" });
    last(r).live();
    expect(r.link.state()).toEqual({ phase: "live" });
  });

  it("holds a reconnect that was asked for while the link is down", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).end(4004, "the shell exited");
    r.link.setLink(false);
    r.link.reconnect();
    expect(r.conns).toHaveLength(1);
    expect(r.link.state()).toEqual({ phase: "connecting" });
    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
  });

  it("reconnects at once when the page becomes visible while waiting", () => {
    const r = rig();
    r.link.start();
    last(r).end(1006);
    visibility("hidden");
    visibility("visible");
    expect(r.conns).toHaveLength(2);
    expect(r.link.state()).toEqual({ phase: "connecting" });
    vi.advanceTimersByTime(MAX_DELAY);
    expect(r.conns).toHaveLength(2);
  });

  it("starts nothing while the link is down, and starts the held reconnect when it is up", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).end(1006);
    r.link.setLink(false);
    vi.advanceTimersByTime(MAX_DELAY);
    expect(r.conns).toHaveLength(1);
    expect(r.link.state().phase).toBe("waiting");
    visibility("hidden");
    visibility("visible");
    expect(r.conns).toHaveLength(1);

    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
    expect(r.link.state()).toEqual({ phase: "connecting" });
  });

  it("starts a scheduled reconnect at once when the link comes back up", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).end(1006);
    r.link.setLink(false);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
    vi.advanceTimersByTime(MAX_DELAY);
    expect(r.conns).toHaveLength(2);
  });

  it("replaces a live connection when the page was hidden for 30 s or longer", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    visibility("hidden");
    vi.advanceTimersByTime(LONG_HIDDEN);
    visibility("visible");
    expect(r.conns).toHaveLength(2);
    expect(r.conns[0].closes).toBe(1);
    expect(r.link.state()).toEqual({ phase: "connecting" });
  });

  it("leaves a live connection alone when the page was hidden for less than 30 s", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    visibility("hidden");
    vi.advanceTimersByTime(LONG_HIDDEN - 1);
    visibility("visible");
    expect(r.conns).toHaveLength(1);
    expect(r.conns[0].closes).toBe(0);
    expect(r.link.state()).toEqual({ phase: "live" });
  });

  it("ignores the late events of a connection it replaced", () => {
    const r = rig();
    r.link.start();
    const old = last(r);
    old.live();
    visibility("hidden");
    vi.advanceTimersByTime(LONG_HIDDEN);
    visibility("visible");
    expect(r.log).toEqual(["fresh"]);

    old.data(9);
    old.live();
    old.end(1006);
    expect(r.log).toEqual(["fresh"]);
    expect(r.link.state()).toEqual({ phase: "connecting" });
    vi.advanceTimersByTime(MAX_DELAY);
    expect(r.conns).toHaveLength(2);
  });

  it("replaces a live connection when the link comes back up after being down", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).live();
    r.link.setLink(false);
    expect(r.conns).toHaveLength(1);
    expect(r.conns[0].closes).toBe(0);
    expect(r.link.state()).toEqual({ phase: "live" });

    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
    expect(r.conns[0].closes).toBe(1);
  });

  it("leaves a live connection alone when the link is set up again without having been down", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).live();
    r.link.setLink(true);
    expect(r.conns).toHaveLength(1);
    expect(r.link.state()).toEqual({ phase: "live" });
  });

  it("leaves a connection that is still being made when the link comes back up", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    r.link.setLink(false);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(1);
    expect(r.conns[0].closes).toBe(0);
  });

  it("ends the connection for good on close", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    r.link.close();
    expect(r.conns[0].closes).toBe(1);
    expect(r.link.state()).toEqual({ phase: "closed", closed: { code: 1000, reason: "" } });

    vi.advanceTimersByTime(10 * MAX_DELAY);
    r.link.reconnect();
    r.link.setLink(false);
    r.link.setLink(true);
    visibility("hidden");
    visibility("visible");
    pageshow(true);
    expect(r.conns).toHaveLength(1);
    expect(r.link.state().phase).toBe("closed");
  });

  it("drops a scheduled reconnect on close", () => {
    const r = rig();
    r.link.start();
    last(r).end(1006);
    r.link.close();
    vi.advanceTimersByTime(10 * MAX_DELAY);
    expect(r.conns).toHaveLength(1);
  });

  it("ends the connection on pagehide and connects again when a cached page is restored", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    pagehide();
    expect(r.conns[0].closes).toBe(1);
    expect(r.link.state()).toEqual({ phase: "waiting" });
    vi.advanceTimersByTime(10 * MAX_DELAY);
    expect(r.conns).toHaveLength(1);

    pageshow(true);
    expect(r.conns).toHaveLength(2);
    expect(r.link.state()).toEqual({ phase: "connecting" });
  });

  it("does not reconnect on a pageshow that is not a restore", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    pagehide();
    pageshow(false);
    expect(r.conns).toHaveLength(1);
    pageshow(true);
    expect(r.conns).toHaveLength(2);
  });

  it("does not reconnect a page that ended in a refusal when it is restored", () => {
    const r = rig();
    r.link.start();
    last(r).end(4004, "the shell exited");
    pagehide();
    pageshow(true);
    expect(r.conns).toHaveLength(1);
    expect(r.link.state()).toEqual({
      phase: "closed",
      closed: { code: 4004, reason: "the shell exited" },
    });
  });

  it("waits for the link when a cached page is restored with it down", () => {
    const r = rig(true);
    r.link.setLink(true);
    r.link.start();
    last(r).live();
    r.link.setLink(false);
    pagehide();
    pageshow(true);
    expect(r.conns).toHaveLength(1);
    r.link.setLink(true);
    expect(r.conns).toHaveLength(2);
  });

  it("resets the terminal once a connection, before its first bytes", () => {
    const r = rig();
    r.link.start();
    expect(r.log).toEqual([]);
    last(r).data(1);
    last(r).data(2);
    last(r).live();
    last(r).data(3);
    expect(r.log).toEqual(["fresh", "data:1", "data:2", "data:3"]);

    last(r).end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    last(r).data(4);
    expect(r.log).toEqual(["fresh", "data:1", "data:2", "data:3", "fresh", "data:4"]);
  });

  it("resets the terminal when a connection goes live with no bytes", () => {
    const r = rig();
    r.link.start();
    last(r).live();
    expect(r.log).toEqual(["fresh"]);
    last(r).data(1);
    expect(r.log).toEqual(["fresh", "data:1"]);
  });

  it("leaves the old screen alone when a connection fails before anything arrives", () => {
    const r = rig();
    r.link.start();
    last(r).end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    last(r).end(1006);
    expect(r.log).toEqual([]);
  });

  it("sends only while live, and answers whether the bytes went", () => {
    const r = rig();
    r.link.start();
    const bytes = new Uint8Array([97]);
    expect(r.link.send(bytes)).toBe(false);
    expect(r.conns[0].sent).toEqual([]);

    last(r).live();
    expect(r.link.send(bytes)).toBe(true);
    expect(r.conns[0].sent).toEqual([bytes]);

    r.conns[0].accepts = false;
    expect(r.link.send(bytes)).toBe(false);
    r.conns[0].accepts = true;

    last(r).end(1006);
    expect(r.link.send(bytes)).toBe(false);
    expect(r.conns[0].sent).toEqual([bytes]);
  });
});

describe("freshScreen", () => {
  const ris = [0x1b, 0x63];

  // A terminal that records what is written to it, in order, as xterm.js's write queue takes it.
  function screen(): { out: number[][]; sink: ScreenSink } {
    const out: number[][] = [];
    return {
      out,
      sink: {
        write: (data) => {
          out.push(Array.from(data));
        },
      },
    };
  }

  // A link wired as main wires it: the reset is freshScreen's, the bytes go to the same screen.
  function wired(sink: ScreenSink | null): { link: Link; conns: Conn[] } {
    const conns: Conn[] = [];
    const link = createLink({
      framed: false,
      open: (on) => {
        const conn = new Conn(on);
        conns.push(conn);
        return conn.sock;
      },
      fresh: freshScreen(() => sink),
      data: (bytes) => {
        sink?.write(bytes);
      },
      changed: () => undefined,
    });
    links.push(link);
    return { link, conns };
  }

  it("is the bytes of RIS", () => {
    expect(Array.from(RIS)).toEqual(ris);
  });

  it("writes RIS ahead of each connection's first bytes, once per connection", () => {
    const { out, sink } = screen();
    const { link, conns } = wired(sink);
    link.start();
    expect(out).toEqual([]);
    conns[0].data(1);
    conns[0].data(2);
    conns[0].live();
    conns[0].data(3);
    expect(out).toEqual([ris, [1], [2], [3]]);

    // An old connection cut inside an escape sequence: the next one still begins with RIS.
    conns[0].data(0x1b, 0x5b);
    conns[0].end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    conns[1].data(4);
    expect(out).toEqual([ris, [1], [2], [3], [0x1b, 0x5b], ris, [4]]);
  });

  it("writes RIS when a connection goes live with no bytes", () => {
    const { out, sink } = screen();
    const { link, conns } = wired(sink);
    link.start();
    conns[0].live();
    expect(out).toEqual([ris]);
  });

  it("writes nothing for a connection that fails before anything arrives", () => {
    const { out, sink } = screen();
    const { link, conns } = wired(sink);
    link.start();
    conns[0].end(1006);
    vi.advanceTimersByTime(FIRST_DELAY);
    conns[1].end(1006);
    expect(out).toEqual([]);
  });

  it("writes nothing, and does not throw, while there is no terminal", () => {
    const { link, conns } = wired(null);
    link.start();
    expect(() => {
      conns[0].data(1);
      conns[0].live();
    }).not.toThrow();
  });
});
```

Fails without (one line a test):
- `reconnects after %i`: a code missing from `reconnects`' list (1001, 4005 or 1006).
- `does not reconnect after %i`: a code wrongly in the list, or a rule such as "every 4xxx but the refusals" or "everything but 4001–4007" (1000, 1005 and 1011 would reconnect).
- `doubles the delay up to 30 s and no further`: the doubling, or the `Math.min` against `MAX_DELAY`.
- `calls once, with the last arguments, after the quiet period`: the `clearTimeout` in `lastCall` (it would call twice, with `(1, 2)` first), or a timer that is not restarted by the second call.
- `calls nothing once cancelled`: `cancel` clearing the timer.
- `connects at start when the page is not in a frame`: `up` starting true when `framed` is false, or `start()` not connecting.
- `holds the first connection until the link is up when it is in a frame`: `up` starting true when framed (a connection before `setLink(true)`), or `setLink(true)` connecting when a connection exists or when the link was already up (two or three connections).
- `takes what was set before it started when it starts`: `setLink` or `reconnect` connecting before `start()`, or `start()` connecting after `close()`.
- `is live when the daemon says so, and tells each change once`: the dedupe in `set` (`live` twice would be told twice), the `retryAt` in `waiting`, or the `connecting` state after the timer.
- `retries on a backoff of 1 s doubling to 30 s`: the delay doubling, its cap, or the timer's length (the connection count after `wait - 1` ms).
- `keeps the delay doubling when a connection ends after %i before 30 s live`: the `STABLE_LIVE` timer being set shorter than 30 s, or the delay reset on `live` as before (the wait would be 1 s), or the `clearTimeout` of the stable timer in `ended` (the ended connection's timer would fire 1 s later and the next wait would be 1 s, not 8 s).
- `goes back to 1 s once a connection has stayed live for 30 s`: the stable timer not being set on `live`, or not setting `delay = FIRST_DELAY` (the wait would be 4 s).
- `does not let a replaced connection's 30 s timer reset the delay`: the `clearTimeout(stable)` in `drop` (the old connection's timer would fire while the replacement is connecting and the wait would be 1 s).
- `retries a connection that failed before it opened`: treating a close before `live` as a refusal.
- `retries a socket that cannot be made`: the `try`/`catch` around `opts.open` (the throw would reach `start`).
- `ends closed after %i and never reconnects on its own`: `ended` scheduling a retry for a refusal, or the visibility handler connecting from `closed`.
- `reconnects from closed at once when asked`: `reconnect()` connecting, or leaving `closed` set.
- `holds a reconnect that was asked for while the link is down`: `reconnect()` connecting without the link up, or `setLink(true)` not connecting a `connecting` phase with no connection.
- `reconnects at once when the page becomes visible while waiting`: the `waiting` case of the visibility handler, or the scheduled timer not being cleared by `connect` (a third connection after `MAX_DELAY`).
- `starts nothing while the link is down, and starts the held reconnect when it is up`: `fire` ignoring `up`, the visibility handler ignoring `up`, or `setLink(true)` not connecting a held reconnect.
- `starts a scheduled reconnect at once when the link comes back up`: `setLink(true)` ignoring a `waiting` phase with no connection, or the timer not cleared (a third connection).
- `replaces a live connection when the page was hidden for 30 s or longer`: the `live` case of the visibility handler, `>=` turned into `>`, or `drop` not closing the old socket.
- `leaves a live connection alone when the page was hidden for less than 30 s`: the `LONG_HIDDEN` check (a replacement on every visibility change).
- `ignores the late events of a connection it replaced`: the generation check in the handlers (the old close, reported at once by the fake's `close`, and its late `data`, `live` and `end` would reach the phase and the terminal).
- `replaces a live connection when the link comes back up after being down`: the `live` case of `setLink`, or `setLink(false)` closing the connection.
- `leaves a live connection alone when the link is set up again without having been down`: the `was` check in `setLink`.
- `leaves a connection that is still being made when the link comes back up`: the `sock === null` check in `setLink` (a second connection).
- `ends the connection for good on close`: `final` (any later `reconnect`, `setLink`, visibility or `pageshow` would connect), or `close` not dropping the connection.
- `drops a scheduled reconnect on close`: the `clearTimeout` in `drop` called by `close`.
- `ends the connection on pagehide and connects again when a cached page is restored`: the `pagehide` handler (no close, no `waiting`), or the `pageshow` handler's connect.
- `does not reconnect on a pageshow that is not a restore`: the `persisted` check.
- `does not reconnect a page that ended in a refusal when it is restored`: the `phase === "closed"` return in `pagehide` (it would set `suspended`).
- `waits for the link when a cached page is restored with it down`: the `up` check on `pageshow`, or `setLink(true)` not connecting a `waiting` phase with no connection.
- `is the bytes of RIS`: `RIS` being any other sequence (`ESC [ c`, `ESC ] c`, a lone `ESC`).
- `writes RIS ahead of each connection's first bytes, once per connection`: `freshScreen` writing nothing (no RIS in the log), writing after the data (the `fresh` call moved behind `opts.data` in `connect`'s handler), writing per frame, or not writing for the second connection (a `seen` flag shared across connections).
- `writes RIS when a connection goes live with no bytes`: the `first()` call in the `live` handler.
- `writes nothing for a connection that fails before anything arrives`: a `fresh` call made at `connect`.
- `writes nothing, and does not throw, while there is no terminal`: the `?.` on `target()` (a TypeError).
- `resets the terminal once a connection, before its first bytes`: `fresh` not called before the first `data`, or called per frame, or not called again for the next connection.
- `resets the terminal when a connection goes live with no bytes`: the `fresh` call in the `live` handler.
- `leaves the old screen alone when a connection fails before anything arrives`: a `fresh` call at `connect` instead of at the first bytes or `live`.
- `sends only while live, and answers whether the bytes went`: the `phase === "live"` check in `send` (the first send would be sent), the socket's answer being returned, or a `send` after the connection ended.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/terminal/reconnect.test.ts` fails with `Failed to resolve import "./reconnect" from "src/terminal/reconnect.test.ts". Does the file exist?` and `Test Files  1 failed (1)`.

- [ ] **Step 3: Implement.** Create `reconnect.ts`:

```ts
// The terminal page's connection policy (spec §34, §35, §59, §60): which endings reconnect, the
// backoff, and the link that holds every new connection while the app's snapshot is stale. It owns
// no WebSocket and no xterm.js: `open` makes each connection, so a test drives it with fake sockets
// and fake timers.
import type { Phase } from "./frame";
import {
  CLOSE_SLOW,
  CLOSE_STOPPING,
  type Closed,
  type Handlers,
  type TerminalSocket,
} from "./socket";

// The stream's numbers (api/stream.ts keeps its own, unexported).
export const FIRST_DELAY = 1000;
export const MAX_DELAY = 30_000;
/** A connection must stay live this long before the delay goes back to FIRST_DELAY (spec §66). */
export const STABLE_LIVE = 30_000;
/** A page hidden this long may hold a half-open connection: the daemon's 20 s ping and 10 s wait. */
export const LONG_HIDDEN = 30_000;
/** How long the window's size must hold still before the new size is sent. */
export const SIZE_SETTLE = 100;

// What a browser reports for a close with no close frame, and for a connection that failed before
// it opened.
const NO_CLOSE_FRAME = 1006;

/**
 * Whether an ending reconnects: the daemon stopping (1001), the browser falling behind (4005) and
 * an ending with no close frame. Every refusal (4001–4004, 4006, 4007) and any other code ends the
 * connection until `reconnect()`.
 */
export function reconnects(closed: Closed): boolean {
  return (
    closed.code === CLOSE_STOPPING || closed.code === CLOSE_SLOW || closed.code === NO_CLOSE_FRAME
  );
}

/** The wait after one of `delay`: doubled, never past MAX_DELAY. */
export function nextDelay(delay: number): number {
  return Math.min(delay * 2, MAX_DELAY);
}

/**
 * A function that calls `fn` with its latest arguments once `ms` have passed with no newer call.
 */
export function lastCall<A extends unknown[]>(
  fn: (...args: A) => void,
  ms: number,
): ((...args: A) => void) & { cancel(): void } {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const call = (...args: A): void => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      timer = undefined;
      fn(...args);
    }, ms);
  };
  return Object.assign(call, {
    cancel: () => {
      clearTimeout(timer);
      timer = undefined;
    },
  });
}

/** RIS, "reset to initial state" (`ESC c`): xterm.js leaves any escape sequence it was inside. */
export const RIS = new Uint8Array([0x1b, 0x63]);

/** The part of xterm.js's Terminal that `freshScreen` writes to. */
export interface ScreenSink {
  write(data: Uint8Array): void;
}

/**
 * The `fresh` of a link wired to a terminal (spec §65): it writes RIS to the terminal. The write
 * queue orders it after whatever the old connection left in it and before the new history, and RIS
 * leaves an escape sequence the old connection was cut inside, which xterm.js's `reset()` does not.
 */
export function freshScreen(target: () => ScreenSink | null): () => void {
  return () => {
    target()?.write(RIS);
  };
}

/** What the link tells the frame: FrameState without the renderer. */
export interface LinkState {
  phase: Phase;
  closed?: Closed;
  retryAt?: number;
}

export interface LinkOptions {
  /** Whether the page is in a frame; a page that is not behaves as if the link were up. */
  framed: boolean;
  /** Opens one connection, giving it the handlers. */
  open(on: Handlers): TerminalSocket;
  /** A new connection's first bytes (or its `live`) are about to reach the terminal: write the reset (RIS) to it. */
  fresh(): void;
  /** The shell's bytes. */
  data(bytes: Uint8Array): void;
  /** The state changed. */
  changed(state: LinkState): void;
}

export interface Link {
  /** The terminal is ready: connect now if the link is up. */
  start(): void;
  setLink(up: boolean): void;
  /** A fresh connection from any state, at once when the link is up. */
  reconnect(): void;
  /** Ends the connection for good. */
  close(): void;
  /** True when the bytes went to an open, live connection. */
  send(bytes: Uint8Array<ArrayBuffer>): boolean;
  resize(cols: number, rows: number): void;
  state(): LinkState;
  /** Unbinds the page's listeners and ends the connection with no state change. */
  dispose(): void;
}

/** The connection controller of the terminal page; see the file's comment. */
export function createLink(opts: LinkOptions): Link {
  let up = !opts.framed;
  let started = false;
  // close() has run: nothing connects any more.
  let final = false;
  // pagehide ended the connection, and a restore from the back-forward cache may revive it.
  let suspended = false;
  let sock: TerminalSocket | null = null;
  // Each connection has a generation; a handler of an older one changes nothing.
  let generation = 0;
  let phase: Phase = "connecting";
  let closed: Closed | undefined;
  let retryAt: number | undefined;
  let delay = FIRST_DELAY;
  let timer: ReturnType<typeof setTimeout> | undefined;
  // The live connection's STABLE_LIVE timer: when it fires the delay goes back to FIRST_DELAY.
  let stable: ReturnType<typeof setTimeout> | undefined;
  let hiddenAt: number | null = document.visibilityState === "hidden" ? Date.now() : null;

  const keyOf = (): string =>
    `${phase}|${closed?.code ?? ""}|${closed?.reason ?? ""}|${retryAt ?? ""}`;
  let last = keyOf();

  function state(): LinkState {
    const s: LinkState = { phase };
    if (closed !== undefined) s.closed = closed;
    if (retryAt !== undefined) s.retryAt = retryAt;
    return s;
  }

  function set(next: Phase, why?: Closed, at?: number): void {
    phase = next;
    closed = why;
    retryAt = at;
    const key = keyOf();
    if (key === last) return;
    last = key;
    opts.changed(state());
  }

  // drop ends the current connection, any scheduled reconnect and the stable timer. The generation
  // moves first, so the close the old socket reports at once changes nothing.
  function drop(): void {
    generation++;
    clearTimeout(timer);
    timer = undefined;
    clearTimeout(stable);
    stable = undefined;
    const old = sock;
    sock = null;
    old?.close();
  }

  function connect(): void {
    drop();
    const mine = generation;
    let seen = false;
    // The terminal is reset with a connection's first bytes, never before: the daemon sends the
    // history on every connect and the scrollback would otherwise hold it twice (spec §36), and a
    // connection that fails first leaves the old screen visible.
    const first = (): void => {
      if (seen) return;
      seen = true;
      opts.fresh();
    };
    set("connecting");
    try {
      sock = opts.open({
        state: (next, why) => {
          if (mine !== generation) return;
          if (next === "live") {
            first();
            // The delay goes back to FIRST_DELAY only when the connection stays live for
            // STABLE_LIVE (spec §66): one that ends sooner, whatever ended it, leaves it doubling, so
            // a flood that fills the daemon's buffer (4005 again and again) backs off.
            if (phase !== "live") {
              stable = setTimeout(() => {
                stable = undefined;
                delay = FIRST_DELAY;
              }, STABLE_LIVE);
            }
            set("live");
          } else if (next === "closed") {
            sock = null;
            ended(why ?? { code: NO_CLOSE_FRAME, reason: "" });
          }
        },
        data: (bytes) => {
          if (mine !== generation) return;
          first();
          opts.data(bytes);
        },
      });
    } catch {
      sock = null;
      ended({ code: NO_CLOSE_FRAME, reason: "" });
    }
  }

  function ended(why: Closed): void {
    clearTimeout(stable);
    stable = undefined;
    if (!reconnects(why)) {
      set("closed", why);
      return;
    }
    set("waiting", undefined, Date.now() + delay);
    timer = setTimeout(fire, delay);
    delay = nextDelay(delay);
  }

  // fire is the scheduled reconnect's time: it connects when the link is up, and otherwise stays
  // waiting until setLink(true).
  function fire(): void {
    timer = undefined;
    if (up) connect();
  }

  function start(): void {
    if (started) return;
    started = true;
    if (up && !final) connect();
  }

  function setLink(next: boolean): void {
    const was = up;
    up = next;
    if (!next || was || !started || final || suspended) return;
    if (phase === "live") {
      // Down and up again: the connection may be half-open, and nothing would tell the page.
      connect();
    } else if (phase !== "closed" && sock === null) {
      connect();
    }
  }

  function reconnect(): void {
    if (final) return;
    delay = FIRST_DELAY;
    suspended = false;
    drop();
    set("connecting");
    if (started && up) connect();
  }

  function close(): void {
    if (final) return;
    final = true;
    drop();
    set("closed", { code: 1000, reason: "" });
  }

  function onVisibility(): void {
    if (document.visibilityState === "hidden") {
      hiddenAt = Date.now();
      return;
    }
    const away = hiddenAt === null ? 0 : Date.now() - hiddenAt;
    hiddenAt = null;
    if (!started || final || suspended || !up) return;
    if (phase === "waiting") {
      delay = FIRST_DELAY;
      connect();
    } else if (phase === "live" && away >= LONG_HIDDEN) {
      connect();
    }
  }

  // pagehide closes the connection normally, as part 1's page did; a page restored from the
  // back-forward cache (pageshow with `persisted`) connects again as if it were waiting.
  function onPageHide(): void {
    if (!started || final || phase === "closed") return;
    suspended = true;
    drop();
    set("waiting");
  }

  function onPageShow(event: PageTransitionEvent): void {
    if (!event.persisted || !suspended) return;
    suspended = false;
    if (started && up) connect();
  }

  document.addEventListener("visibilitychange", onVisibility);
  window.addEventListener("pagehide", onPageHide);
  window.addEventListener("pageshow", onPageShow);

  return {
    start,
    setLink,
    reconnect,
    close,
    send: (bytes) => phase === "live" && sock !== null && sock.send(bytes),
    resize: (cols, rows) => {
      sock?.resize(cols, rows);
    },
    state,
    dispose: () => {
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("pagehide", onPageHide);
      window.removeEventListener("pageshow", onPageShow);
      final = true;
      drop();
    },
  };
}
```

Run `pnpm -C internal/web/ui exec prettier --write src/terminal/reconnect.ts src/terminal/reconnect.test.ts`.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/terminal/reconnect.test.ts` prints `Test Files  1 passed (1)` and `Tests  59 passed (59)`; then `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` pass.

- [ ] **Step 5: Commit.**

```bash
git add -N internal/web/ui/src/terminal/reconnect.ts internal/web/ui/src/terminal/reconnect.test.ts
git commit -m "feat(web): the terminal page's reconnect policy, link and size debounce" -- internal/web/ui/src/terminal/reconnect.ts internal/web/ui/src/terminal/reconnect.test.ts
```

### Task 3.4: `keys.ts`, a named key's bytes and what a key event does

**Files:**
- Create: `internal/web/ui/src/terminal/keys.ts`
- Create: `internal/web/ui/src/terminal/keys.test.ts`

**Defines (private to this story):** `keyBytes(name, applicationCursor)`, `keyAction(event)`, `handleKey(event, hooks)`, the types `KeyAction` and `KeyHooks`. **Uses:** `KeyName` from `frame.ts`; `textBytes` from `socket.ts`.

- [ ] **Step 1: Write the failing test.** Create `internal/web/ui/src/terminal/keys.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";

import type { KeyName } from "./frame";
import { handleKey, keyAction, keyBytes, type KeyAction } from "./keys";

const ESC = 0x1b;

function bytes(name: KeyName, applicationCursor: boolean): number[] {
  return Array.from(keyBytes(name, applicationCursor));
}

// A key event, cancelable so a test can read whether the handler cancelled it.
function ev(init: KeyboardEventInit, type = "keydown"): KeyboardEvent {
  return new KeyboardEvent(type, { cancelable: true, ...init });
}

describe("keyBytes", () => {
  it("sends the arrows as CSI sequences in the normal cursor mode", () => {
    expect(bytes("up", false)).toEqual([ESC, 0x5b, 0x41]);
    expect(bytes("down", false)).toEqual([ESC, 0x5b, 0x42]);
    expect(bytes("right", false)).toEqual([ESC, 0x5b, 0x43]);
    expect(bytes("left", false)).toEqual([ESC, 0x5b, 0x44]);
  });

  it("sends the arrows as SS3 sequences while the program has set application cursor keys", () => {
    expect(bytes("up", true)).toEqual([ESC, 0x4f, 0x41]);
    expect(bytes("down", true)).toEqual([ESC, 0x4f, 0x42]);
    expect(bytes("right", true)).toEqual([ESC, 0x4f, 0x43]);
    expect(bytes("left", true)).toEqual([ESC, 0x4f, 0x44]);
  });

  it("sends tab, escape, Ctrl-C and Ctrl-D as one byte in either mode", () => {
    for (const mode of [false, true]) {
      expect(bytes("tab", mode)).toEqual([0x09]);
      expect(bytes("esc", mode)).toEqual([ESC]);
      expect(bytes("ctrl-c", mode)).toEqual([0x03]);
      expect(bytes("ctrl-d", mode)).toEqual([0x04]);
    }
  });
});

describe("keyAction", () => {
  const cases: [string, KeyboardEventInit, KeyAction][] = [
    ["⌘K", { key: "k", metaKey: true }, "chord"],
    ["⌘K with caps lock", { key: "K", metaKey: true }, "chord"],
    ["Ctrl+K, readline's kill-line", { key: "k", ctrlKey: true }, "shell"],
    ["⌘⇧K", { key: "k", metaKey: true, shiftKey: true }, "shell"],
    ["⌘⌃K", { key: "k", metaKey: true, ctrlKey: true }, "shell"],
    ["Ctrl+Shift+C", { key: "C", ctrlKey: true, shiftKey: true }, "copy"],
    ["Ctrl+C, the interrupt", { key: "c", ctrlKey: true }, "shell"],
    ["⌘C, the browser's copy", { key: "c", metaKey: true }, "shell"],
    ["Ctrl+Shift+Alt+C", { key: "C", ctrlKey: true, shiftKey: true, altKey: true }, "shell"],
    ["Ctrl+Shift+V", { key: "V", ctrlKey: true, shiftKey: true }, "paste"],
    ["Ctrl+V", { key: "v", ctrlKey: true }, "shell"],
    ["a plain letter", { key: "a" }, "shell"],
  ];

  it.each(cases)("%s", (_name, init, action) => {
    expect(keyAction(ev(init))).toBe(action);
  });
});

describe("handleKey", () => {
  function hooks() {
    return { chord: vi.fn(), copy: vi.fn() };
  }

  it("sends ⌘K to the chord, not to the shell, and cancels the browser's own", () => {
    const h = hooks();
    const event = ev({ key: "k", metaKey: true });
    expect(handleKey(event, h)).toBe(false);
    expect(h.chord).toHaveBeenCalledTimes(1);
    expect(h.copy).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(true);
  });

  it("does not repeat a held chord or a held copy", () => {
    const h = hooks();
    expect(handleKey(ev({ key: "k", metaKey: true, repeat: true }), h)).toBe(false);
    expect(handleKey(ev({ key: "C", ctrlKey: true, shiftKey: true, repeat: true }), h)).toBe(false);
    expect(h.chord).not.toHaveBeenCalled();
    expect(h.copy).not.toHaveBeenCalled();
  });

  it("keeps the other events of a chord from the shell without acting twice", () => {
    const h = hooks();
    for (const type of ["keypress", "keyup"]) {
      expect(handleKey(ev({ key: "k", metaKey: true }, type), h)).toBe(false);
      expect(handleKey(ev({ key: "C", ctrlKey: true, shiftKey: true }, type), h)).toBe(false);
    }
    expect(h.chord).not.toHaveBeenCalled();
    expect(h.copy).not.toHaveBeenCalled();
  });

  it("copies on Ctrl+Shift+C and neither sends it to the shell nor leaves it to the browser", () => {
    const h = hooks();
    const event = ev({ key: "C", ctrlKey: true, shiftKey: true });
    expect(handleKey(event, h)).toBe(false);
    expect(h.copy).toHaveBeenCalledTimes(1);
    expect(h.chord).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(true);
  });

  it("leaves Ctrl+Shift+V to the browser and keeps it from the shell", () => {
    const h = hooks();
    const event = ev({ key: "V", ctrlKey: true, shiftKey: true });
    expect(handleKey(event, h)).toBe(false);
    expect(event.defaultPrevented).toBe(false);
    expect(h.chord).not.toHaveBeenCalled();
    expect(h.copy).not.toHaveBeenCalled();
  });

  it("gives Ctrl+K, Ctrl+C and every other key to the shell", () => {
    const h = hooks();
    for (const init of [
      { key: "k", ctrlKey: true },
      { key: "c", ctrlKey: true },
      { key: "c", metaKey: true },
      { key: "a" },
      { key: "Enter" },
    ]) {
      const event = ev(init);
      expect(handleKey(event, h)).toBe(true);
      expect(event.defaultPrevented).toBe(false);
    }
    expect(h.chord).not.toHaveBeenCalled();
    expect(h.copy).not.toHaveBeenCalled();
  });
});
```

Fails without:
- `sends the arrows as CSI sequences in the normal cursor mode`: the `[` of the CSI form (or a mapping of the letters).
- `sends the arrows as SS3 sequences while the program has set application cursor keys`: the `applicationCursor` branch (`O` for `[`).
- `sends tab, escape, Ctrl-C and Ctrl-D as one byte in either mode`: a byte value, or a cursor-mode branch that also changes these four.
- `keyAction` cases: `⌘K`, `⌘K with caps lock`: the `chord` rule or its `toLowerCase`; `Ctrl+K, readline's kill-line`: a rule that takes Ctrl+K as the chord; `⌘⇧K`, `⌘⌃K`: the `!shiftKey` and `!ctrlKey` terms of the chord rule; `Ctrl+Shift+C`: the copy rule; `Ctrl+C, the interrupt` and `⌘C, the browser's copy`: a copy rule that ignores `shiftKey` or `ctrlKey` (Ctrl+C must reach the shell); `Ctrl+Shift+Alt+C`: the `!altKey` term; `Ctrl+Shift+V`: the paste rule; `Ctrl+V`: a paste rule that ignores `shiftKey`; `a plain letter`: a default that is not `shell`.
- `sends ⌘K to the chord, not to the shell, and cancels the browser's own`: the `chord` hook call, the `false` return or the `preventDefault`.
- `does not repeat a held chord or a held copy`: the `repeat` check (the palette would toggle on every repeat).
- `keeps the other events of a chord from the shell without acting twice`: the `type !== "keydown"` branch (a `true` return would put the key's `keypress` into the shell, or the hooks would run three times).
- `copies on Ctrl+Shift+C and neither sends it to the shell nor leaves it to the browser`: the `copy` hook, the `false` return or the `preventDefault` (Firefox opens its inspector on it).
- `leaves Ctrl+Shift+V to the browser and keeps it from the shell`: the `paste` branch returning `true` (the shell would get a Ctrl+V byte), or calling `preventDefault` (the browser's paste event, which xterm.js turns into a paste, would never fire).
- `gives Ctrl+K, Ctrl+C and every other key to the shell`: any key but the three chords being cancelled or answered `false`.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/terminal/keys.test.ts` fails with `Failed to resolve import "./keys" from "src/terminal/keys.test.ts". Does the file exist?` and `Test Files  1 failed (1)`.

- [ ] **Step 3: Implement.** Create `keys.ts`:

```ts
// What the terminal page does with keys (spec §33, §37): the bytes of a named key, and the policy of
// the custom key handler xterm.js runs before it handles a key event itself.
import type { KeyName } from "./frame";
import { textBytes } from "./socket";

const ESC = "\x1b";
const ARROW_LETTER = { up: "A", down: "B", right: "C", left: "D" } as const;

function keyText(name: KeyName, applicationCursor: boolean): string {
  switch (name) {
    case "up":
    case "down":
    case "right":
    case "left":
      // ESC [ A–D normally, ESC O A–D while the program has set application cursor keys.
      return `${ESC}${applicationCursor ? "O" : "["}${ARROW_LETTER[name]}`;
    case "tab":
      return "\t";
    case "esc":
      return ESC;
    case "ctrl-c":
      return "\x03";
    case "ctrl-d":
      return "\x04";
  }
}

/** The bytes a named key sends, in the cursor mode the program has set. */
export function keyBytes(name: KeyName, applicationCursor: boolean): Uint8Array<ArrayBuffer> {
  return textBytes(keyText(name, applicationCursor));
}

/**
 * What a key event in the frame does: `chord` is ⌘K, which the app answers (key events in a frame
 * never reach the app's document); `copy` is Ctrl+Shift+C; `paste` is Ctrl+Shift+V; everything
 * else, Ctrl+K and Ctrl+C included, is `shell`.
 */
export type KeyAction = "chord" | "copy" | "paste" | "shell";

export function keyAction(ev: KeyboardEvent): KeyAction {
  const key = ev.key.toLowerCase();
  if (ev.metaKey && !ev.ctrlKey && !ev.altKey && !ev.shiftKey && key === "k") return "chord";
  if (ev.ctrlKey && ev.shiftKey && !ev.altKey && !ev.metaKey) {
    if (key === "c") return "copy";
    if (key === "v") return "paste";
  }
  return "shell";
}

export interface KeyHooks {
  /** ⌘K was pressed. */
  chord(): void;
  /** Ctrl+Shift+C was pressed: copy the selection. */
  copy(): void;
}

/**
 * The body of xterm.js's custom key event handler: false keeps the event from xterm.js (and so from
 * the shell), true lets it handle it. The chord and the copy key act on their keydown only, once
 * (not on a repeat), and cancel the browser's own action (Firefox opens its inspector on
 * Ctrl+Shift+C); Ctrl+Shift+V is kept from the shell and left to the browser, whose paste event
 * xterm.js turns into a paste, bracketed when the shell asked.
 */
export function handleKey(ev: KeyboardEvent, hooks: KeyHooks): boolean {
  const action = keyAction(ev);
  if (action === "shell") return true;
  if (ev.type === "keydown" && action !== "paste") {
    ev.preventDefault();
    if (!ev.repeat) {
      if (action === "chord") hooks.chord();
      else hooks.copy();
    }
  }
  return false;
}
```

Run `pnpm -C internal/web/ui exec prettier --write src/terminal/keys.ts src/terminal/keys.test.ts`.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/terminal/keys.test.ts` prints `Test Files  1 passed (1)` and `Tests  20 passed (20)`; `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` pass.

- [ ] **Step 5: Commit.**

```bash
git add -N internal/web/ui/src/terminal/keys.ts internal/web/ui/src/terminal/keys.test.ts
git commit -m "feat(web): the terminal page's key bytes and key handling" -- internal/web/ui/src/terminal/keys.ts internal/web/ui/src/terminal/keys.test.ts
```

### Task 3.5: `main.ts` and `terminal.css`, the wiring

**Files:**
- Modify: `internal/web/ui/src/terminal/main.ts` (rewritten; no earlier story edits it)
- Modify: `internal/web/ui/src/terminal/terminal.css`

**Defines (contract, Story 3):** `window.loomTerminal`, set by `main.ts` as the script first runs, before the font has loaded. **Uses:** this story's `frame.ts` (with `startFailed`), `reconnect.ts` (with `freshScreen`), `keys.ts` and `socket.ts`; Story 2's `bindTouchScroll(el, scroller)` (touch.ts, returns the function that unbinds) and `plainText(rows)` with `BufferLine` (text.ts, given each row untrimmed).

- [ ] **Step 1: No new unit test.** `main.ts` imports xterm.js and runs at import; every decision in it is made in the modules tested in Tasks 3.1–3.4 and in Story 2 (the RIS that resets the terminal in `freshScreen`, the `closed` state of a failed start in `startFailed`), and the build and the Go tests in Task 3.6 hold it to its bundle. This task's check is the typecheck, the lint and the suite, which a partial rewrite fails (an unused import, a name that no longer exists). Confirm the starting point first: `pnpm -C internal/web/ui run typecheck` passes.

- [ ] **Step 2: Implement `main.ts`.** Replace the whole file (the current file is the part 1 page: it makes the terminal and the socket in `start`, focuses xterm.js when the socket goes live, and has no reconnecting; no earlier story of this plan edits it) with:

```ts
// The terminal's document (spec §23): xterm.js on a session's worktree shell, over the daemon's
// WebSocket. It is the only script that imports xterm.js, and the web layer serves its page under
// terminalCSP, which lets xterm.js write the <style> elements the app's policy drops.
//
// In a frame it is driven by the app through `window.loomTerminal` (frame.ts), which exists from the
// moment the script runs: a call that sets something before the terminal has started takes effect
// when it starts, and `send` and `key` answer false and `text` answers "" until then. Opened on its
// own the page keeps part 1's status line and focuses the terminal when it goes live.
import "@xterm/xterm/css/xterm.css";
import "../styles/app.css";
import "./terminal.css";

import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import { Terminal } from "@xterm/xterm";

import { api } from "../api/request";
import {
  startFailed,
  type FrameState,
  type KeyName,
  type LoomTerminal,
  type Renderer,
} from "./frame";
import { handleKey, keyBytes } from "./keys";
import { SIZE_SETTLE, createLink, freshScreen, lastCall, type LinkState } from "./reconnect";
import {
  binaryBytes,
  describeClose,
  openSocket,
  sessionIdFromHash,
  socketURL,
  textBytes,
} from "./socket";
import { plainText, type BufferLine } from "./text";
import { bindTouchScroll } from "./touch";

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

// bufferRows gives plainText the buffer's rows as the terminal holds them: untrimmed, so a row that
// the next one continues keeps its trailing blanks (spec §56).
function* bufferRows(term: Terminal): Generator<BufferLine> {
  const buffer = term.buffer.active;
  for (let y = 0; y < buffer.length; y++) {
    const line = buffer.getLine(y);
    if (line === undefined) continue;
    yield { text: line.translateToString(false), wrapped: line.isWrapped };
  }
}

// statusText is the status line of a page that is not in a frame.
function statusText(state: LinkState): string {
  switch (state.phase) {
    case "connecting":
      return "Connecting…";
    case "waiting":
      return "Reconnecting…";
    case "closed":
      return describeClose(state.closed ?? { code: 1006, reason: "" });
    case "live":
      return "";
  }
}

function main(): void {
  const host = element("terminal");
  const status = element("status");
  const say = (text: string): void => {
    status.textContent = text;
    status.hidden = text === "";
  };

  // Framed, the panel shows the state and the page shows no status line (terminal.css).
  const framed = window.parent !== window;
  if (framed) document.documentElement.dataset.framed = "";

  const session = sessionIdFromHash(window.location.hash);
  if (session === null) {
    say("Open this page as /terminal.html#<session id>.");
    return;
  }
  const url = socketURL(api.terminalURL(session), window.location);

  // What the interface sets before the terminal exists, and what it holds after.
  let term: Terminal | null = null;
  let touch = false;
  let wantFocus = false;
  let renderer: Renderer = "dom";
  let unbindTouch: (() => void) | null = null;
  let onState: ((state: FrameState) => void) | null = null;
  let onChord: (() => void) | null = null;
  // Set when the terminal could not start (spec §68): the state the panel then gets, for good.
  let failed: FrameState | null = null;

  const frameState = (): FrameState => failed ?? { ...link.state(), renderer };
  const publish = (): void => {
    onState?.(frameState());
  };
  // In touch mode the keyboard must never rise, so xterm.js is never focused.
  const focusTerminal = (): void => {
    if (term !== null && !touch) term.focus();
  };

  const link = createLink({
    framed,
    open: (on) =>
      openSocket(
        url,
        () => (term === null ? { cols: 80, rows: 24 } : { cols: term.cols, rows: term.rows }),
        on,
      ),
    // RIS goes through the write queue, so it lands after whatever the old connection left in it
    // and before the history that follows, and it leaves an escape sequence the old connection was
    // cut inside, which term.reset() does not (spec §65).
    fresh: freshScreen(() => term),
    data: (bytes) => {
      term?.write(bytes);
    },
    changed: (state) => {
      publish();
      if (framed) return;
      say(statusText(state));
      if (state.phase === "live") focusTerminal();
    },
  });

  // Input to the shell: true when it went to an open connection, and then the terminal scrolls to
  // the bottom; otherwise it is dropped, never queued (spec §55).
  const sendInput = (bytes: Uint8Array<ArrayBuffer>): boolean => {
    const sent = link.send(bytes);
    if (sent) term?.scrollToBottom();
    return sent;
  };

  const applyTouch = (): void => {
    unbindTouch?.();
    unbindTouch = null;
    const t = term;
    if (t === null || !touch) return;
    t.blur();
    unbindTouch = bindTouchScroll(host, {
      scrollLines: (lines) => {
        t.scrollLines(lines);
      },
      // The terminal's height over its rows, in CSS pixels.
      cellHeight: () => (t.element?.clientHeight ?? 0) / t.rows,
    });
  };

  const copySelection = (): void => {
    const selected = term?.getSelection() ?? "";
    if (selected === "") return;
    try {
      void navigator.clipboard.writeText(selected).catch(() => undefined);
    } catch {
      // No clipboard in this context: the browser's own copy still works.
    }
  };

  const loomTerminal: LoomTerminal = {
    send: (bytes) => sendInput(bytes),
    key: (name: KeyName) =>
      sendInput(keyBytes(name, term?.modes.applicationCursorKeysMode ?? false)),
    text: () => (term === null ? "" : plainText(bufferRows(term))),
    setTouch: (on) => {
      touch = on;
      applyTouch();
    },
    setLink: (up) => {
      link.setLink(up);
    },
    reconnect: () => {
      link.reconnect();
    },
    focus: () => {
      if (term === null) wantFocus = true;
      else focusTerminal();
    },
    close: () => {
      link.close();
    },
    onState: (cb) => {
      onState = cb;
      cb(frameState());
    },
    onChord: (cb) => {
      onChord = cb;
    },
  };
  window.loomTerminal = loomTerminal;

  const start = (): void => {
    const t = new Terminal({
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
    term = t;
    const fit = new FitAddon();
    t.loadAddon(fit);
    // Loaded before open(): the addon waits for it (the spike's item 1 measured the fewest style
    // violations this way). The DOM renderer serves when WebGL is unavailable or its context is
    // lost, and terminalCSP allows its styles.
    if (webglAvailable()) {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => {
        webgl.dispose();
        renderer = "dom";
        publish();
      });
      t.loadAddon(webgl);
      renderer = "webgl";
    }
    t.attachCustomKeyEventHandler((ev) =>
      handleKey(ev, {
        chord: () => {
          onChord?.();
        },
        copy: copySelection,
      }),
    );
    t.open(host);
    fit.fit();

    t.onData((text) => {
      link.send(textBytes(text));
    });
    t.onBinary((text) => {
      link.send(binaryBytes(text));
    });
    // The new size goes to the daemon 100 ms after the last change of the window's size.
    const settle = lastCall((cols: number, rows: number) => {
      link.resize(cols, rows);
    }, SIZE_SETTLE);
    t.onResize(({ cols, rows }) => {
      settle(cols, rows);
    });
    new ResizeObserver(() => {
      fit.fit();
    }).observe(host);

    applyTouch();
    if (wantFocus) {
      wantFocus = false;
      focusTerminal();
    }
    publish();
    link.start();
    if (!framed) say(statusText(link.state()));
  };

  say("Loading…");
  // xterm.js measures a cell when it opens: wait for the font, or the grid is sized for the
  // fallback face.
  void document.fonts
    .load(`${FONT_SIZE}px "JetBrains Mono Variable"`)
    .catch(() => [])
    .then(() => {
      start();
    })
    .catch((err: unknown) => {
      // A start that threw (the WebGL addon, open) leaves the panel a closed state to show, and the
      // link, if it began, ends; a page that is not in a frame shows the same text in its status line.
      const state = startFailed(err, renderer);
      failed = state;
      link.close();
      publish();
      if (!framed) say(state.closed?.reason ?? "");
    });
}

main();
```

Run `pnpm -C internal/web/ui exec prettier --write src/terminal/main.ts`.

- [ ] **Step 3: Implement `terminal.css`.** Add a rule that hides the status line in a frame. Quote the end of the file:

```css
#status[hidden] {
  display: none;
}
```

and add after it:

```css

/* In a frame the panel shows the state (spec §35): main.ts sets data-framed on <html>. */
html[data-framed] #status {
  display: none;
}
```

Also replace the header comment's second sentence, which says the status line sits over the terminal "after it closed" for every page. Quote

```css
/* The terminal page (spec §23): the terminal fills the window, and a status line sits over its
   bottom edge while the socket connects or after it closed. Colours come from the app's tokens
```

and make it

```css
/* The terminal page (spec §23): the terminal fills the window, and, on a page that is not in a
   frame, a status line sits over its bottom edge while the socket connects or after it closed.
   Colours come from the app's tokens
```

(the next line of the comment, `(styles/app.css, which main.ts imports); the font is the stack of app.css's --font-mono. */`, is unchanged). Run `pnpm -C internal/web/ui exec prettier --check src/terminal/terminal.css`.

- [ ] **Step 4: Run the checks.** `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui run format:check && pnpm -C internal/web/ui run test` pass. `main.ts` is the one file here no Vitest test reaches; what it reads off the other modules is held by their tests.

- [ ] **Step 5: Commit.**

```bash
git commit -m "feat(web): the terminal page sets window.loomTerminal and reconnects" -- internal/web/ui/src/terminal/main.ts internal/web/ui/src/terminal/terminal.css
```

### Task 3.6: Rebuild the bundle and bring the README along

**Files:**
- Modify: `internal/web/dist/**` (rebuilt)
- Modify: `internal/web/README.md`

- [ ] **Step 1: Quote and edit the README.** In `internal/web/README.md`, in the paragraph that begins `The terminal's document, \`/terminal.html\`, is the second HTML entry of the build`, replace

```
and shows a close's reason in the page, with no reconnecting, no panel and no phone input.
```

with

```
and, on a page that is not in a frame, shows its state in a status line. In a frame it sets `window.loomTerminal` (`src/terminal/frame.ts`: `send`, `key`, `text`, `setTouch`, `setLink`, `reconnect`, `focus`, `close`, `onState` and `onChord`), the one interface a panel drives it through: input is sent only to a live connection and never queued, the terminal is reset by RIS written ahead of a new connection's first bytes, a terminal that cannot start reports a `closed` state, the new size is sent 100 ms after the last change, and a connection that ends with 1001, 4005 or no close frame reconnects on a backoff from 1 s to 30 s that returns to 1 s only once a connection has stayed live for 30 s (`src/terminal/reconnect.ts`), at once when the page becomes visible or is restored from the back-forward cache, never while the app's link is down, and in place of a live one when the link comes back up or the page was hidden for 30 s or longer; every other ending waits for `reconnect()`. Touch mode scrolls the terminal with a finger and never focuses it; ⌘K inside the frame is handed to the app, Ctrl+Shift+C copies the selection, and Ctrl+K and every other key go to the shell.
```

(The sentence that follows, `xterm.js appends \`<style>\` elements at run time…`, stays.) The README has no panel yet at this story: the panel is a later story's, so the new text names only the frame.

- [ ] **Step 2: Rebuild the bundle.** `pnpm -C internal/web/ui run build` rewrites `internal/web/dist/`: `git status --short internal/web/dist` lists a changed `assets/terminal-<hash>.js` (a new hash, the old file deleted) and, if the CSS changed, `assets/terminal-<hash>.css`; `index.html`, `sw.js` and the app's entry script do not change, since nothing the app imports changed.

- [ ] **Step 3: Run the gates for the story.** `pnpm -C internal/web/ui run check`, then `pnpm -C internal/web/ui run dist:check` (it fails with a stale-bundle message before Step 2's build and passes after), then `CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/` pass, `TestDist_XtermLoadsOnlyInTheTerminalPage` among them: xterm.js is still only in the terminal page's script, since `frame.ts`, `reconnect.ts`, `keys.ts` and `socket.ts` import nothing of it.

- [ ] **Step 4: Commit.**

```bash
git add internal/web/dist
git commit -m "build(web): rebuild the bundle for the terminal frame's interface" -- internal/web/dist internal/web/README.md
```

## Story 4: The `terminal` panel: its tab, its routing, the frame and the desktop

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/app/routes.tsx`, `internal/web/ui/src/app/routes.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/desktop/Inspector.tsx`, `internal/web/ui/src/desktop/Inspector.test.tsx`, `internal/web/ui/src/desktop/inspector.ts`, `internal/web/ui/src/desktop/inspector.test.ts`, `internal/web/ui/src/desktop/Palette.tsx`, `internal/web/ui/src/desktop/Palette.test.tsx`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/panels/index.ts`, `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/mockup/index.tsx`, `internal/web/ui/src/panels/flow/index.tsx`, `internal/web/ui/src/panels/info/info.test.tsx`, `internal/web/ui/src/panels/mockup/mockup.test.tsx`, `internal/web/ui/src/panels/flow/flow.test.tsx`, `internal/web/ui/src/panels/changes/changes.test.tsx`, `internal/web/ui/src/panels/turns/turns.test.tsx`, `internal/web/ui/src/panels/terminal/index.tsx`, `internal/web/ui/src/panels/terminal/Terminal.tsx`, `internal/web/ui/src/panels/terminal/Terminal.test.tsx`, `internal/web/ui_rules_test.go`, `internal/web/ui/CLAUDE.md`, `internal/web/README.md`, `internal/web/dist/**`, `USAGE.md`
- **Interfaces:** exposes `HostFacts`, `useHostFacts`, the `terminal` panel and its path, `Terminal` and its `data-` attributes to Stories 5 and 6; uses Story 3's `LoomTerminal`, `FrameState`, `frameTerminal`, `CLOSE_EXITED` and `describeClose`
- **Verify:** `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
- **Review:** deep
- **Form:** full (the panel's handling of the frame's interface: taking it on load, the link, the close before unmount, the focus rule and the chord)

The panel joins the registry and the app's hand-kept routing, and draws the frame Story 3 made drivable (spec §39–§43, §55, §57, §58, §61). `appliesTo` and `sessionTabs` gain the host's facts (`HostFacts`, read by `useHostFacts()` from the snapshot's `terminal`), the phone's `SessionTabs`, the desktop's `Inspector` and the palette pass them (and the existing panel tests that call `appliesTo` pass one too), and the terminal applies when the host's terminal is on and the session is started, active and not paused (`sessionActions(session).diff`, the rule the changes tab already follows), so the tab comes and goes on the stream's `terminal` event. `PanelId` gains `"terminal"`, with the path `/s/<id>/terminal` in `routes.tsx` (the session-route pattern, `hrefFor`'s table and the router's own `PATHS`) and a place in `TABBED`; its tab order is 3, and info, mockup and flow move to 4, 5 and 6. `Terminal` draws `<iframe src="/terminal.html#<id>">`, takes the interface with `frameTerminal` once the frame has loaded, hands it the layout's touch mode and the link (`!stale`), closes it in a layout effect's cleanup inside a `try` so the close runs before the frame leaves the document (spec §61), draws "Connecting…", "Reconnecting…" or the close's reason over the frame (a terminal that cannot start is a `closed` state with code 0 and the failure as its reason, spec §68, shown as any reason) with a "Start a new shell" button for 4004 only, turns the frame's chord into the app's own ⌘K `keydown` on `document`, and on the desktop calls `focus()` by spec §57 as §67 narrows it: never before a `live`; at the first `live` of a terminal the user brought up (it mounts as the middle column's panel, or its inspector tab is pressed), unless a text field has the focus then; at every other `live` (a later one, or the first of a terminal that was not brought up, which the inspector mounts from a stored choice) only while the app's focused element is the frame itself. A press of the inspector's terminal tab marks only the terminal that the same press mounts: the inspector clears the mark once the commit is done, so a press whose terminal does not mount leaves nothing behind. `TerminalBody` is the panel's body: it says why a direct link to a panel that does not apply shows nothing (the terminal is off on this host, or the session is not running) and keys `Terminal` by the session so another session gets a new frame. The inspector never falls back to the terminal: `inspectorTab` draws it only as the stored choice (spec §58), pinned on both sides. The panel is in the entry bundle (xterm.js stays in the frame): `TestDist_FirstLoadIsWithinBudget` is the gate that shows whether the first load still fits its budget, and no budget changes here. `internal/web/ui/CLAUDE.md` gains the rule that the panel reaches the frame only through its interface, with its Go test, and `USAGE.md` and `internal/web/README.md` describe the panel. None of this code holds a landmine marker. `Terminal` holds its states (`terminal`, `state`) itself, which Story 5's `PhoneInput` is given from it, and `TerminalBody` and `terminalApplies` sit beside it in `Terminal.tsx`.

### Task 4.1: The host's facts: `HostFacts`, `appliesTo`'s second argument, `useHostFacts`, and the three callers

**Files:**
- Modify: `internal/web/ui/src/app/panels.ts:37-39,50-52,72-79`
- Modify: `internal/web/ui/src/app/panels.test.ts:1-18,66-71`
- Modify: `internal/web/ui/src/store/fleet.ts:1-12,165-171`
- Modify: `internal/web/ui/src/store/fleet.test.ts:1-9,230-243`
- Modify: `internal/web/ui/src/app/shell.tsx:20,392-393`
- Modify: `internal/web/ui/src/app/shell.test.tsx:437-447`
- Modify: `internal/web/ui/src/desktop/Inspector.tsx:6-9,21`
- Modify: `internal/web/ui/src/desktop/Inspector.test.tsx:1-22`
- Modify: `internal/web/ui/src/desktop/Palette.tsx:9-12,25-43`
- Modify: `internal/web/ui/src/desktop/Palette.test.tsx:255-263`
- Modify: `internal/web/ui/src/panels/changes/changes.test.tsx:526-531`
- Modify: `internal/web/ui/src/panels/flow/flow.test.tsx:712-714`
- Modify: `internal/web/ui/src/panels/mockup/mockup.test.tsx:205-206`
- Modify: `internal/web/ui/src/panels/turns/turns.test.tsx:584-585`

**Defines (contract, Story 4):** `HostFacts` (in `app/panels.ts`), `Panel.appliesTo` and `sessionTabs` with the host argument, and `useHostFacts` (in `store/fleet.ts`), exactly as the contract gives them. **Uses:** nothing of another story. `PanelId` has no `"terminal"` yet at this task, so its tests use `mockup`, a tab panel whose `appliesTo` they define, as the stand-in for a tab that applies by the host's switch.

- [ ] **Step 1: Write the failing tests.**

In `internal/web/ui/src/app/panels.test.ts`, replace the import

```ts
import {
  definePanel,
  panelFor,
  sessionTabs,
  tabOrder,
  type PanelId,
  type PanelProps,
} from "./panels";

const Body = (): null => null;
const props: PanelProps = { sessionId: undefined, params: {}, search: {} };
```

with

```ts
import {
  definePanel,
  panelFor,
  sessionTabs,
  tabOrder,
  type HostFacts,
  type PanelId,
  type PanelProps,
} from "./panels";

const Body = (): null => null;
const props: PanelProps = { sessionId: undefined, params: {}, search: {} };
const host: HostFacts = { terminal: false };
```

Replace the two calls

```ts
    expect(sessionTabs(aSession({ claude: true })).map((p) => p.id)).toEqual([
```

with

```ts
    expect(sessionTabs(aSession({ claude: true }), host).map((p) => p.id)).toEqual([
```

and

```ts
    expect(sessionTabs(aSession({ claude: false })).map((p) => p.id)).toEqual(["info"]);
  });
```

with

```ts
    expect(sessionTabs(aSession({ claude: false }), host).map((p) => p.id)).toEqual(["info"]);
  });

  it("hands appliesTo the host's facts as its second argument", () => {
    definePanel({ id: "info", title: () => "Info", tab: { order: 3, label: "info" }, Body });
    definePanel({
      id: "mockup",
      title: () => "Mockup",
      tab: { order: 4, label: "mockup" },
      appliesTo: (session, facts) => facts.terminal && session.claude,
      Body,
    });
    const at = (facts: HostFacts, claude: boolean): string[] =>
      sessionTabs(aSession({ claude }), facts).map((p) => p.id);

    expect(at({ terminal: true }, true)).toContain("mockup");
    expect(at({ terminal: false }, true)).not.toContain("mockup");
    expect(at({ terminal: true }, false)).not.toContain("mockup");
  });
```

In `internal/web/ui/src/store/fleet.test.ts`, replace

```ts
import { MAX_NOTICES, useFleet, useFleetGroups, useLink, useSession } from "./fleet";
```

with

```ts
import {
  MAX_NOTICES,
  useFleet,
  useFleetGroups,
  useHostFacts,
  useLink,
  useSession,
} from "./fleet";
```

and replace the end of the `useLink` test and its `describe`

```ts
    expect(result.current).toEqual({ state: "live", stale: false });
  });
});
```

with

```ts
    expect(result.current).toEqual({ state: "live", stale: false });
  });

  it("useHostFacts is false before a snapshot and follows the stream's terminal event", () => {
    const { result } = renderHook(() => useHostFacts());
    expect(result.current).toEqual({ terminal: false });
    act(() => {
      store().apply("snapshot", { ...fixtureSnapshot(NOW), terminal: false });
    });
    expect(result.current).toEqual({ terminal: false });
    act(() => {
      store().apply("terminal", { enabled: true });
    });
    expect(result.current).toEqual({ terminal: true });
    act(() => {
      store().apply("snapshot", { ...fixtureSnapshot(NOW), terminal: true });
    });
    expect(result.current).toEqual({ terminal: true });
    act(() => {
      store().apply("terminal", { enabled: false });
    });
    expect(result.current).toEqual({ terminal: false });
  });
});
```

In `internal/web/ui/src/app/shell.test.tsx`, replace the end of the `keeps its tabs when a Mark throws` test and the table after it

```ts
      expect(links.map((a) => a.textContent)).toEqual(["conversation", "changes", "info"]);
    } finally {
      spy.mockRestore();
    }
  });
```

with

```ts
      expect(links.map((a) => a.textContent)).toEqual(["conversation", "changes", "info"]);
    } finally {
      spy.mockRestore();
    }
  });

  it("draws a tab that applies by the host's switch as the stream's terminal event turns it on and off", () => {
    tabPanels();
    definePanel({
      id: "mockup",
      title: () => "Mockup",
      tab: { order: 4, label: "host tab" },
      appliesTo: (_session, host) => host.terminal,
      Body: () => null,
    });
    window.history.replaceState(null, "", "#/s/7/info");
    live({ ...fixtureSnapshot(), terminal: false });

    render(<SessionTabs session={aSession(true)} />);

    const names = (): (string | null)[] =>
      within(screen.getByRole("navigation", { name: "Session views" }))
        .getAllByRole("link")
        .map((a) => a.textContent);
    expect(names()).toEqual(["conversation", "changes", "info"]);
    act(() => {
      useFleet.getState().apply("terminal", { enabled: true });
    });
    expect(names()).toEqual(["conversation", "changes", "info", "host tab"]);
    act(() => {
      useFleet.getState().apply("terminal", { enabled: false });
    });
    expect(names()).toEqual(["conversation", "changes", "info"]);
  });
```

In `internal/web/ui/src/desktop/Inspector.test.tsx`, replace the first imports and the `afterEach`

```tsx
import { fireEvent, render, screen, within } from "@testing-library/react";
```

with

```tsx
import { act, fireEvent, render, screen, within } from "@testing-library/react";
```

and

```tsx
import { allSessions } from "@/fleet/groups";

import { Inspector } from "./Inspector";
```

with

```tsx
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import { Inspector } from "./Inspector";
```

and

```tsx
afterEach(() => {
  localStorage.clear();
});
```

with

```tsx
afterEach(() => {
  localStorage.clear();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
});
```

and add, inside `describe("Inspector", () => {` directly after its first test (`lists the session's tabs but the middle's, and draws the first applicable one`, which ends with `expect(screen.queryByText("info body")).toBeNull();\n  });`), this test:

```tsx
  it("lists a tab that applies by the host's switch as the stream's terminal event turns it on", () => {
    tabs();
    definePanel({
      id: "mockup",
      title: () => "Mockup",
      tab: { order: 3, label: "mockup" },
      appliesTo: (_session, host) => host.terminal,
      Body: () => <p>mockup body</p>,
    });
    useFleet.setState({ snapshot: { ...fixtureSnapshot(), terminal: false } });
    render(<Inspector session={claude()} middle="turns" />);
    expect(names()).toEqual(["changes", "info"]);
    act(() => {
      useFleet.getState().apply("terminal", { enabled: true });
    });
    expect(names()).toEqual(["changes", "mockup", "info"]);
    act(() => {
      useFleet.getState().apply("terminal", { enabled: false });
    });
    expect(names()).toEqual(["changes", "info"]);
  });
```

In `internal/web/ui/src/desktop/Palette.test.tsx`, add, directly after the test `Enter on a tab place goes to its link` (it ends with `expect(window.location.hash).toBe("#/s/3/info");\n    expect(usePalette.getState().open).toBe(false);\n  });`), this test:

```tsx
  it("lists a tab that applies by the host's switch as the stream's terminal event turns it on", async () => {
    definePanel({
      id: "mockup",
      title: () => "Mockup",
      tab: { order: 4, label: "mockup" },
      appliesTo: (_session, host) => host.terminal,
      Body: () => null,
    });
    setup("#/s/3", { snapshot: { ...onlySession3(), terminal: false } });
    await userEvent.type(input(), "mockup");
    expect(titles()).not.toContain("mockup");
    act(() => {
      useFleet.getState().apply("terminal", { enabled: true });
    });
    expect(titles()).toContain("mockup");
    act(() => {
      useFleet.getState().apply("terminal", { enabled: false });
    });
    expect(titles()).not.toContain("mockup");
  });
```

The panels' own tests call `appliesTo` with one argument, which the typecheck refuses once the signature asks for the host. In each, pass a host whose switch is off (none of these four panels reads it). In `internal/web/ui/src/panels/changes/changes.test.tsx`, replace

```tsx
    expect(applies(show(1))).toBe(true);
    expect(applies(show(1, { status: "Ready", group: "your_turn" }))).toBe(true);
    expect(applies(show(1, { status: "Loading" }))).toBe(false);
    expect(applies(show(1, { status: "Deleting" }))).toBe(false);
    expect(applies(show(1, { status: "Paused", group: "paused" }))).toBe(false);
    expect(applies(show(1, { status: "Recoverable", group: "paused" }))).toBe(false);
```

with

```tsx
    const host = { terminal: false };
    expect(applies(show(1), host)).toBe(true);
    expect(applies(show(1, { status: "Ready", group: "your_turn" }), host)).toBe(true);
    expect(applies(show(1, { status: "Loading" }), host)).toBe(false);
    expect(applies(show(1, { status: "Deleting" }), host)).toBe(false);
    expect(applies(show(1, { status: "Paused", group: "paused" }), host)).toBe(false);
    expect(applies(show(1, { status: "Recoverable", group: "paused" }), host)).toBe(false);
```

In `internal/web/ui/src/panels/flow/flow.test.tsx`, replace

```tsx
    expect(flowPanel.appliesTo?.(session6())).toBe(true);
    expect(flowPanel.appliesTo?.(session6({ flows: [] }))).toBe(false);
    expect(flowPanel.appliesTo?.(session6({ flows: null }))).toBe(false);
```

with

```tsx
    expect(flowPanel.appliesTo?.(session6(), { terminal: false })).toBe(true);
    expect(flowPanel.appliesTo?.(session6({ flows: [] }), { terminal: false })).toBe(false);
    expect(flowPanel.appliesTo?.(session6({ flows: null }), { terminal: false })).toBe(false);
```

In `internal/web/ui/src/panels/mockup/mockup.test.tsx`, replace

```tsx
    expect(panel?.appliesTo?.(show())).toBe(true);
    expect(panel?.appliesTo?.(show({ mockup: null }))).toBe(false);
```

with

```tsx
    expect(panel?.appliesTo?.(show(), { terminal: false })).toBe(true);
    expect(panel?.appliesTo?.(show({ mockup: null }), { terminal: false })).toBe(false);
```

In `internal/web/ui/src/panels/turns/turns.test.tsx`, replace

```tsx
    expect(panel?.appliesTo?.(makeSession({ claude: true }))).toBe(true);
    expect(panel?.appliesTo?.(makeSession({ claude: false }))).toBe(false);
```

with

```tsx
    expect(panel?.appliesTo?.(makeSession({ claude: true }), { terminal: false })).toBe(true);
    expect(panel?.appliesTo?.(makeSession({ claude: false }), { terminal: false })).toBe(false);
```

Run `pnpm -C internal/web/ui exec prettier --write src/app/panels.test.ts src/store/fleet.test.ts src/app/shell.test.tsx src/desktop/Inspector.test.tsx src/desktop/Palette.test.tsx src/panels/changes/changes.test.tsx src/panels/flow/flow.test.tsx src/panels/mockup/mockup.test.tsx src/panels/turns/turns.test.tsx`.

Fails without: `hands appliesTo the host's facts as its second argument`: `sessionTabs` passing `host` on to `appliesTo` (a call `p.appliesTo(session)` throws reading `terminal` of undefined)
Fails without: `useHostFacts is false before a snapshot and follows the stream's terminal event`: `snapshot?.terminal === true` (a hook that is true before a snapshot, or ignores the event, fails an assertion)
Fails without: `draws a tab that applies by the host's switch as the stream's terminal event turns it on and off` (shell.test.tsx): `SessionTabs` reading `useHostFacts()` (a constant host never lists the fourth tab)
Fails without: `lists a tab that applies by the host's switch as the stream's terminal event turns it on` (Inspector.test.tsx): the `useHostFacts()` read in `Inspector`
Fails without: `lists a tab that applies by the host's switch as the stream's terminal event turns it on` (Palette.test.tsx): the `useHostFacts()` read in `Palette` and `host` in its tabs memo's dependencies

- [ ] **Step 2: Run them to see them fail.**

```bash
pnpm -C internal/web/ui exec vitest run src/app/panels.test.ts src/store/fleet.test.ts src/app/shell.test.tsx src/desktop/Inspector.test.tsx src/desktop/Palette.test.tsx
```

Expected: FAIL. `fleet.test.ts` fails with `useHostFacts is not a function`; the cases that define a host-dependent `appliesTo` fail with `TypeError: Cannot read properties of undefined (reading 'terminal')`, since the callers pass no host yet. The four panel tests edited above still pass at runtime (vitest does not typecheck, and the extra argument is ignored); `pnpm -C internal/web/ui run typecheck` flags them with `TS2554: Expected 1 arguments, but got 2` until Step 3 widens the signature.

- [ ] **Step 3: Implement.**

In `internal/web/ui/src/app/panels.ts`, add `HostFacts` before `PanelTab`. Replace

```ts
// PanelTab makes a panel one of a session's tabs: its place, its label and an
// optional Mark drawn after the label.
```

with

```ts
// HostFacts is what the app knows of the host beyond a session, read from the snapshot: whether
// the web terminal is on. A tab's appliesTo reads it with the session.
export interface HostFacts {
  terminal: boolean;
}

// PanelTab makes a panel one of a session's tabs: its place, its label and an
// optional Mark drawn after the label.
```

Replace

```ts
// sessions appliesTo accepts (every session without it). Strip is the line
```

with

```ts
// sessions and hosts appliesTo accepts (every session without it). Strip is the line
```

Replace

```ts
  appliesTo?: (session: Session) => boolean;
```

with

```ts
  appliesTo?: (session: Session, host: HostFacts) => boolean;
```

Replace

```ts
// sessionTabs are the tab panels that apply to the session, in tab order.
export function sessionTabs(session: Session): Panel[] {
  return tabOrder()
    .map((id) => registry.get(id))
    .filter(
      (p): p is Panel => p !== undefined && (p.appliesTo === undefined || p.appliesTo(session)),
    );
}
```

with

```ts
// sessionTabs are the tab panels that apply to the session on this host, in tab order.
export function sessionTabs(session: Session, host: HostFacts): Panel[] {
  return tabOrder()
    .map((id) => registry.get(id))
    .filter(
      (p): p is Panel =>
        p !== undefined && (p.appliesTo === undefined || p.appliesTo(session, host)),
    );
}
```

In `internal/web/ui/src/store/fleet.ts`, replace

```ts
import { checkPushSubscription, shellBuild, upgradeShell } from "@/app/worker";
```

with

```ts
import type { HostFacts } from "@/app/panels";
import { checkPushSubscription, shellBuild, upgradeShell } from "@/app/worker";
```

and, after `useLink`, replace

```ts
export function useLink(): { state: LinkState; stale: boolean } {
  return useFleet(useShallow((s) => ({ state: s.link, stale: s.stale })));
}
```

with

```ts
export function useLink(): { state: LinkState; stale: boolean } {
  return useFleet(useShallow((s) => ({ state: s.link, stale: s.stale })));
}

/**
 * The host's facts a tab's `appliesTo` reads: the snapshot's `terminal` (false before the first
 * snapshot). A component that calls it re-renders when the stream's `terminal` event changes it.
 */
export function useHostFacts(): HostFacts {
  const terminal = useFleet((s) => s.snapshot?.terminal === true);
  return useMemo(() => ({ terminal }), [terminal]);
}
```

In `internal/web/ui/src/app/shell.tsx`, replace

```ts
import { useFleet, useLink, useSession } from "@/store/fleet";
```

with

```ts
import { useFleet, useHostFacts, useLink, useSession } from "@/store/fleet";
```

and replace

```tsx
  const current = tabOf(route.panel, session);
  const tabs = sessionTabs(session);
```

with

```tsx
  const current = tabOf(route.panel, session);
  const host = useHostFacts();
  const tabs = sessionTabs(session, host);
```

In `internal/web/ui/src/desktop/Inspector.tsx`, replace

```tsx
import { Boundary, PanelHost, resetKey } from "@/app/shell";

import { inspectorTab } from "./inspector";
```

with

```tsx
import { Boundary, PanelHost, resetKey } from "@/app/shell";
import { useHostFacts } from "@/store/fleet";

import { inspectorTab } from "./inspector";
```

and replace

```tsx
  const panels = sessionTabs(session);
```

with

```tsx
  const host = useHostFacts();
  const panels = sessionTabs(session, host);
```

In `internal/web/ui/src/desktop/Palette.tsx`, replace

```tsx
import { useFleet } from "@/store/fleet";
```

with

```tsx
import { useFleet, useHostFacts } from "@/store/fleet";
```

and replace

```tsx
  const snapshot = useFleet((s) => s.snapshot);
  const [query, setQuery] = useState("");
```

with

```tsx
  const snapshot = useFleet((s) => s.snapshot);
  const host = useHostFacts();
  const [query, setQuery] = useState("");
```

and replace

```tsx
        : sessionTabs(ctx.session).map((panel) => ({
            id: panel.id,
            label: panel.tab?.label ?? panel.id,
          })),
    [ctx.session],
  );
```

with

```tsx
        : sessionTabs(ctx.session, host).map((panel) => ({
            id: panel.id,
            label: panel.tab?.label ?? panel.id,
          })),
    [ctx.session, host],
  );
```

Run `pnpm -C internal/web/ui exec prettier --write src/app/panels.ts src/store/fleet.ts src/app/shell.tsx src/desktop/Inspector.tsx src/desktop/Palette.tsx`.

- [ ] **Step 4: Run them to see them pass.**

```bash
pnpm -C internal/web/ui exec vitest run src/app/panels.test.ts src/store/fleet.test.ts src/app/shell.test.tsx src/desktop/Inspector.test.tsx src/desktop/Palette.test.tsx src/panels/changes/changes.test.tsx src/panels/flow/flow.test.tsx src/panels/mockup/mockup.test.tsx src/panels/turns/turns.test.tsx && pnpm -C internal/web/ui run typecheck
```

Expected: every test file passes and `tsc --noEmit` prints nothing (`tsc` covers every file under `src`, tests included, so it reports any caller of `sessionTabs` or of a panel's `appliesTo` that still passes no host; `grep -rn 'sessionTabs(' internal/web/ui/src` lists the three callers and the tests).

- [ ] **Step 5: Commit.**

```bash
git commit -m "feat(web): tabs apply by the host's facts as well as the session" -- internal/web/ui/src/app/panels.ts internal/web/ui/src/app/panels.test.ts internal/web/ui/src/store/fleet.ts internal/web/ui/src/store/fleet.test.ts internal/web/ui/src/app/shell.tsx internal/web/ui/src/app/shell.test.tsx internal/web/ui/src/desktop/Inspector.tsx internal/web/ui/src/desktop/Inspector.test.tsx internal/web/ui/src/desktop/Palette.tsx internal/web/ui/src/desktop/Palette.test.tsx internal/web/ui/src/panels/changes/changes.test.tsx internal/web/ui/src/panels/flow/flow.test.tsx internal/web/ui/src/panels/mockup/mockup.test.tsx internal/web/ui/src/panels/turns/turns.test.tsx
```

### Task 4.2: `Terminal.tsx`, the frame, its states, the desktop's focus and the chord

**Files:**
- Create: `internal/web/ui/src/panels/terminal/Terminal.tsx`
- Create: `internal/web/ui/src/panels/terminal/Terminal.test.tsx`
- Modify: `internal/web/ui/src/app/shell.tsx:285`

**Defines (contract, Story 4):** `Terminal(props: PanelProps)` with `data-slot="terminal"` and `data-terminal-state` on its root, holding the frame's `LoomTerminal | null` and `FrameState | null` in state; `fakeTerminal()` in `Terminal.test.tsx`. **Defines (private to this story):** `terminalApplies(session, host)`, `TerminalBody(props)`, `markInspectorPress()` and `takeInspectorPress()`, all in `Terminal.tsx`. **Uses:** Story 3's `frameTerminal`, `LoomTerminal`, `FrameState` (`@/terminal/frame`), `CLOSE_EXITED` and `describeClose` (`@/terminal/socket`); Task 4.1's `HostFacts` and `useHostFacts`; `sessionActions` (`commands/session.ts`), `useLayout`, `useLink`, `useSession`, `NoSession` and `Button`; `isTextField` from `app/shell.tsx`, which this task exports. `terminalApplies` is the panel's `appliesTo`: it follows `sessionActions(session).diff` (not busy, not in the paused group, the same set the changes tab is drawn for), which is what the daemon's terminal request admits: started, active and not paused; the daemon stays the authority, and a refusal closes the connection with its reason. `markInspectorPress` is what the inspector's tab button calls (Task 4.3), so that a terminal mounted by the press takes the focus (spec §57) and one mounted from a stored choice does not; the terminal takes the mark at its first commit, and the inspector takes (and drops) it at the end of every commit (Task 4.3), so a press whose terminal does not mount leaves nothing behind.

- [ ] **Step 1: Write the failing test.** Create `internal/web/ui/src/panels/terminal/Terminal.test.tsx`:

```tsx
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Session } from "@/api/gen";
import type { PanelProps } from "@/app/panels";
import { usePalette, usePaletteKey } from "@/desktop/paletteState";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";
import { frameTerminal, type FrameState, type LoomTerminal } from "@/terminal/frame";
import { CLOSE_EXITED, CLOSE_OFF, CLOSE_REVOKED } from "@/terminal/socket";
import { setViewportWidth } from "@/test-layout";

import { markInspectorPress, Terminal, TerminalBody, terminalApplies } from "./Terminal";

vi.mock("@/terminal/frame", () => ({ frameTerminal: vi.fn() }));

// A LoomTerminal of vi.fn() members that `frameTerminal` answers; `emit` gives the panel a state
// as the frame would, and `chord` is ⌘K pressed inside the frame. Story 5's tests use it too.
export type FakeTerminal = LoomTerminal & {
  emit(state: FrameState): void;
  chord(): void;
};

export function fakeTerminal(
  initial: FrameState = { phase: "connecting", renderer: "dom" },
): FakeTerminal {
  let state = initial;
  let stateCb: (next: FrameState) => void = () => {};
  let chordCb: () => void = () => {};
  const fake: FakeTerminal = {
    send: vi.fn(() => true),
    key: vi.fn(() => true),
    text: vi.fn(() => ""),
    setTouch: vi.fn(),
    setLink: vi.fn(),
    reconnect: vi.fn(),
    focus: vi.fn(),
    close: vi.fn(),
    onState: vi.fn((cb: (next: FrameState) => void) => {
      stateCb = cb;
      cb(state);
    }),
    onChord: vi.fn((cb: () => void) => {
      chordCb = cb;
    }),
    emit(next) {
      state = next;
      act(() => {
        stateCb(next);
      });
    },
    chord() {
      chordCb();
    },
  };
  vi.mocked(frameTerminal).mockReturnValue(fake);
  return fake;
}

const LIVE: FrameState = { phase: "live", renderer: "webgl" };
const WAITING: FrameState = { phase: "waiting", retryAt: 0, renderer: "dom" };
const PROPS: PanelProps = { sessionId: 7, params: { id: "7" }, search: {} };

beforeEach(() => {
  vi.mocked(frameTerminal).mockReset();
  useFleet.setState({ snapshot: null, stale: false, link: "live", updated: 0 });
});

afterEach(() => {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  usePalette.getState().setOpen(false);
});

function frameEl(): HTMLIFrameElement {
  const el = screen.getByTitle("Terminal");
  if (!(el instanceof HTMLIFrameElement)) throw new Error("the terminal's frame is not an iframe");
  return el;
}

function rootEl(): HTMLElement {
  const el = document.querySelector<HTMLElement>('[data-slot="terminal"]');
  if (el === null) throw new Error("no terminal root");
  return el;
}

// load is the frame's load event, after which the panel takes the interface.
function load(): void {
  fireEvent.load(frameEl());
}

// mount draws the panel and loads its frame, with a fake interface behind it.
function mount(fake = fakeTerminal()) {
  const view = render(<Terminal {...PROPS} />);
  load();
  return { fake, view };
}

describe("Terminal's frame", () => {
  it("draws the frame at /terminal.html with the session's id, titled Terminal", () => {
    render(<Terminal {...PROPS} />);
    expect(frameEl()).toHaveAttribute("src", "/terminal.html#7");
    expect(rootEl()).toContainElement(frameEl());
  });

  it("carries no state until the frame gives one, and says Connecting…", () => {
    const fake = fakeTerminal();
    render(<Terminal {...PROPS} />);
    expect(rootEl()).not.toHaveAttribute("data-terminal-state");
    expect(screen.getByText("Connecting…")).toBeInTheDocument();
    expect(fake.onState).not.toHaveBeenCalled();
  });

  it("takes the interface when the frame loads and carries the phase it gives", () => {
    const { fake } = mount();
    expect(fake.onState).toHaveBeenCalledTimes(1);
    expect(rootEl()).toHaveAttribute("data-terminal-state", "connecting");
    fake.emit(LIVE);
    expect(rootEl()).toHaveAttribute("data-terminal-state", "live");
    fake.emit(WAITING);
    expect(rootEl()).toHaveAttribute("data-terminal-state", "waiting");
  });

  it("says the terminal did not load when the frame has no interface", () => {
    vi.mocked(frameTerminal).mockReturnValue(null);
    render(<Terminal {...PROPS} />);
    load();
    expect(screen.getByText("The terminal did not load.")).toBeInTheDocument();
    expect(rootEl()).not.toHaveAttribute("data-terminal-state");
  });
});

describe("Terminal's states", () => {
  it("shows nothing over a live terminal", () => {
    const { fake } = mount();
    fake.emit(LIVE);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("says Connecting… while connecting and Reconnecting… while waiting", () => {
    const { fake } = mount();
    expect(screen.getByRole("status")).toHaveTextContent("Connecting…");
    fake.emit(WAITING);
    expect(screen.getByRole("status")).toHaveTextContent("Reconnecting…");
    expect(screen.queryByRole("button", { name: "Start a new shell" })).toBeNull();
  });

  it("shows a close's reason, and no button for any ending but the shell's exit", () => {
    const { fake } = mount();
    fake.emit({
      phase: "closed",
      closed: { code: CLOSE_OFF, reason: "the terminal is off on this host" },
      renderer: "dom",
    });
    expect(screen.getByRole("status")).toHaveTextContent("the terminal is off on this host");
    expect(screen.queryByRole("button")).toBeNull();
    fake.emit({
      phase: "closed",
      closed: { code: CLOSE_REVOKED, reason: "this device is no longer paired" },
      renderer: "dom",
    });
    expect(screen.getByRole("status")).toHaveTextContent("this device is no longer paired");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("shows the failure of a terminal that could not start as a reason, with no button (spec §68)", () => {
    const { fake } = mount();
    fake.emit({
      phase: "closed",
      closed: { code: 0, reason: "The terminal could not start: out of memory" },
      renderer: "dom",
    });
    expect(rootEl()).toHaveAttribute("data-terminal-state", "closed");
    expect(screen.getByRole("status")).toHaveTextContent(
      "The terminal could not start: out of memory",
    );
    expect(screen.queryByRole("button", { name: "Start a new shell" })).toBeNull();
  });

  it("offers Start a new shell after the shell exited, which connects again", async () => {
    const { fake } = mount();
    fake.emit({
      phase: "closed",
      closed: { code: CLOSE_EXITED, reason: "the shell exited" },
      renderer: "dom",
    });
    expect(screen.getByRole("status")).toHaveTextContent("the shell exited");
    await userEvent.click(screen.getByRole("button", { name: "Start a new shell" }));
    expect(fake.reconnect).toHaveBeenCalledTimes(1);
  });
});

describe("Terminal's touch mode and link", () => {
  it("gives the phone's layout touch mode and a desktop's none", () => {
    const { fake } = mount();
    expect(fake.setTouch).toHaveBeenLastCalledWith(true);

    setViewportWidth(1280);
    expect(fake.setTouch).toHaveBeenLastCalledWith(false);
    setViewportWidth(390);
    expect(fake.setTouch).toHaveBeenLastCalledWith(true);
  });

  it("gives the frame the link: up while the snapshot is fresh, down while it is stale", () => {
    const { fake } = mount();
    expect(fake.setLink).toHaveBeenLastCalledWith(true);
    act(() => {
      useFleet.setState({ stale: true, link: "offline" });
    });
    expect(fake.setLink).toHaveBeenLastCalledWith(false);
    act(() => {
      useFleet.setState({ stale: false, link: "live" });
    });
    expect(fake.setLink).toHaveBeenLastCalledWith(true);
  });
});

describe("Terminal's unmount", () => {
  it("closes the terminal once when the panel unmounts", () => {
    const { fake, view } = mount();
    view.unmount();
    expect(fake.close).toHaveBeenCalledTimes(1);
  });

  it("closes it before the frame leaves the document", () => {
    const fake = fakeTerminal();
    const view = render(<Terminal {...PROPS} />);
    const frame = frameEl();
    load();
    let attached: boolean | undefined;
    vi.mocked(fake.close).mockImplementation(() => {
      attached = frame.isConnected;
    });
    view.unmount();
    expect(attached).toBe(true);
    expect(frame.isConnected).toBe(false);
  });

  it("unmounts even when the close throws", () => {
    const { fake, view } = mount();
    vi.mocked(fake.close).mockImplementation(() => {
      throw new Error("the frame's document is gone");
    });
    expect(() => {
      view.unmount();
    }).not.toThrow();
    expect(fake.close).toHaveBeenCalledTimes(1);
  });

  it("closes nothing when the frame never gave an interface", () => {
    const fake = fakeTerminal();
    vi.mocked(frameTerminal).mockReturnValue(null);
    const view = render(<Terminal {...PROPS} />);
    load();
    view.unmount();
    expect(fake.close).not.toHaveBeenCalled();
  });
});

describe("Terminal's chord", () => {
  it("dispatches ⌘K on the app's document, where the palette's one listener answers", () => {
    function Listener(): null {
      usePaletteKey();
      return null;
    }
    const heard: KeyboardEvent[] = [];
    const record = (e: Event): void => {
      if (e instanceof KeyboardEvent) heard.push(e);
    };
    const { fake } = mount();
    render(<Listener />);
    document.addEventListener("keydown", record);
    try {
      expect(usePalette.getState().open).toBe(false);
      act(() => {
        fake.chord();
      });
    } finally {
      document.removeEventListener("keydown", record);
    }
    expect(heard).toHaveLength(1);
    expect(heard[0]).toMatchObject({ key: "k", metaKey: true, bubbles: true });
    expect(usePalette.getState().open).toBe(true);
  });
});

describe("Terminal's focus on the desktop (spec §57, §67)", () => {
  beforeEach(() => {
    setViewportWidth(1280);
  });

  const added: HTMLElement[] = [];
  afterEach(() => {
    for (const el of added.splice(0)) el.remove();
    Reflect.deleteProperty(document, "activeElement");
  });

  function focusedElement(tag: "input" | "button"): HTMLElement {
    const el = document.createElement(tag);
    document.body.appendChild(el);
    el.focus();
    expect(document.activeElement).toBe(el);
    added.push(el);
    return el;
  }

  // frameHasTheFocus makes the document name the frame as its focused element, as a browser does
  // while the focus is inside a frame; nothingHasTheFocus makes it name none.
  function frameHasTheFocus(): void {
    Object.defineProperty(document, "activeElement", { configurable: true, get: () => frameEl() });
  }
  function nothingHasTheFocus(): void {
    Object.defineProperty(document, "activeElement", { configurable: true, get: () => null });
  }

  // reconnected is a later live: the connection ends and a new one goes live.
  function reconnected(fake: FakeTerminal): void {
    fake.emit(WAITING);
    fake.emit(LIVE);
  }

  it("takes no focus before the terminal is live: not on taking the interface, not while waiting", () => {
    const { fake } = mount();
    expect(fake.focus).not.toHaveBeenCalled();
    fake.emit(WAITING);
    expect(fake.focus).not.toHaveBeenCalled();
  });

  it("focuses a terminal the user brought up at its first live", () => {
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);
  });

  it("takes the focus from no text field at its first live", () => {
    focusedElement("input");
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).not.toHaveBeenCalled();
  });

  it("takes the focus at its first live from a control that is not a text field", () => {
    focusedElement("button");
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);
  });

  it("refocuses at a later live while the frame has the focus", () => {
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);
    frameHasTheFocus();
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(2);
  });

  it("takes no focus at a later live while the body, a control, a text field or nothing has it", () => {
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);

    // The body has the focus (the document's default).
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);

    focusedElement("button");
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);

    focusedElement("input");
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);

    nothingHasTheFocus();
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);
  });

  it("takes none at its first live in the inspector, from a stored choice, and refocuses at a later live while the frame has the focus", () => {
    const fake = fakeTerminal();
    render(
      <aside data-slot="inspector">
        <Terminal {...PROPS} />
      </aside>,
    );
    load();
    fake.emit(LIVE);
    expect(fake.focus).not.toHaveBeenCalled();
    frameHasTheFocus();
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);
  });

  it("takes it in the inspector once its tab was pressed, and only for the terminal that press mounts", () => {
    const fake = fakeTerminal();
    markInspectorPress();
    const first = render(
      <aside data-slot="inspector">
        <Terminal {...PROPS} />
      </aside>,
    );
    load();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);
    first.unmount();

    const again = fakeTerminal();
    render(
      <aside data-slot="inspector">
        <Terminal {...PROPS} />
      </aside>,
    );
    load();
    again.emit(LIVE);
    expect(again.focus).not.toHaveBeenCalled();
  });

  it("takes no focus on the phone, where the keyboard must stay down", () => {
    setViewportWidth(390);
    const { fake } = mount();
    fake.emit(LIVE);
    frameHasTheFocus();
    reconnected(fake);
    expect(fake.focus).not.toHaveBeenCalled();
  });
});

function running(over: Partial<Session> = {}): Session {
  const base = allSessions(fixtureSnapshot())[0];
  if (base === undefined) throw new Error("the fixture holds no session");
  return { ...base, id: 7, status: "Running", group: "working", ...over };
}

// hostWith puts the sessions in the store's snapshot, with the host's terminal switch as given.
function hostWith(terminal: boolean, ...sessions: Session[]): void {
  useFleet.setState({
    snapshot: {
      ...fixtureSnapshot(),
      terminal,
      sessions: { "1": sessions },
    },
    stale: false,
    link: "live",
    updated: Date.now(),
  });
}

describe("terminalApplies", () => {
  it.each([
    ["a running session", true, { status: "Running", group: "working" }, true],
    ["a ready session in your turn", true, { status: "Ready", group: "your_turn" }, true],
    ["a prompting session", true, { status: "Prompting", group: "needs_you" }, true],
    ["the host's switch off", false, { status: "Running", group: "working" }, false],
    ["a paused session", true, { status: "Paused", group: "paused" }, false],
    ["a recoverable session", true, { status: "Recoverable", group: "paused" }, false],
    ["a ready session not yet started", true, { status: "Ready", group: "paused" }, false],
    ["a loading session", true, { status: "Loading", group: "working" }, false],
    ["a deleting session", true, { status: "Deleting", group: "working" }, false],
  ] as const)("%s", (_name, terminal, over, want) => {
    expect(terminalApplies(running(over), { terminal })).toBe(want);
  });
});

describe("TerminalBody", () => {
  it("draws the terminal for a session it applies to", () => {
    hostWith(true, running());
    render(<TerminalBody {...PROPS} />);
    expect(frameEl()).toHaveAttribute("src", "/terminal.html#7");
  });

  it("says the terminal is off on this host, with no frame", () => {
    hostWith(false, running());
    render(<TerminalBody {...PROPS} />);
    expect(screen.getByRole("status")).toHaveTextContent("The terminal is off on this host");
    expect(screen.queryByTitle("Terminal")).toBeNull();
  });

  it("says the session is not running when the host's terminal is on", () => {
    hostWith(true, running({ status: "Paused", group: "paused" }));
    render(<TerminalBody {...PROPS} />);
    expect(screen.getByRole("status")).toHaveTextContent("This session is not running");
    expect(screen.queryByTitle("Terminal")).toBeNull();
  });

  it("says the session is gone when the snapshot does not list it", () => {
    hostWith(true, running({ id: 8 }));
    render(<TerminalBody {...PROPS} />);
    expect(screen.getByRole("status")).toHaveTextContent("This session is gone.");
    expect(screen.queryByTitle("Terminal")).toBeNull();
  });

  it("swaps the frame for the message when the host turns the terminal off, closing it", () => {
    hostWith(true, running());
    const fake = fakeTerminal();
    render(<TerminalBody {...PROPS} />);
    load();
    act(() => {
      useFleet.getState().apply("terminal", { enabled: false });
    });
    expect(screen.queryByTitle("Terminal")).toBeNull();
    expect(screen.getByRole("status")).toHaveTextContent("The terminal is off on this host");
    expect(fake.close).toHaveBeenCalledTimes(1);
  });

  it("gives another session a frame of its own", () => {
    hostWith(true, running(), running({ id: 8 }));
    const fake = fakeTerminal();
    const view = render(<TerminalBody {...PROPS} />);
    const first = frameEl();
    load();
    view.rerender(<TerminalBody sessionId={8} params={{ id: "8" }} search={{}} />);
    const second = frameEl();
    expect(second).not.toBe(first);
    expect(second).toHaveAttribute("src", "/terminal.html#8");
    expect(fake.close).toHaveBeenCalledTimes(1);
  });
});
```

Run `pnpm -C internal/web/ui exec prettier --write src/panels/terminal/Terminal.test.tsx`.

Fails without: `draws the frame at /terminal.html with the session's id, titled Terminal`: the `src` and the `title` of the frame
Fails without: `carries no state until the frame gives one, and says Connecting…`: a `data-terminal-state` set before a state arrives, or the "Connecting…" line left out
Fails without: `takes the interface when the frame loads and carries the phase it gives`: the `onLoad` that reads `frameTerminal`, and `data-terminal-state={phase}`
Fails without: `says the terminal did not load when the frame has no interface`: the `failed` branch of `onLoad`
Fails without: `shows nothing over a live terminal`: the notice drawn for `live`
Fails without: `says Connecting… while connecting and Reconnecting… while waiting`: the two texts, and the button drawn only for 4004
Fails without: `shows a close's reason, and no button for any ending but the shell's exit`: `describeClose(state.closed)` and the `CLOSE_EXITED` test on the button (a button for every closed state fails the second half)
Fails without: `shows the failure of a terminal that could not start as a reason, with no button (spec §68)`: `describeClose(state.closed)` as the closed text (a generic "The terminal closed." fails the text) and the `CLOSE_EXITED` test on the button (a button for any `closed` state fails the last line)
Fails without: `offers Start a new shell after the shell exited, which connects again`: the button's `reconnect()`
Fails without: `gives the phone's layout touch mode and a desktop's none`: `setTouch(phone)` and its dependency on the layout
Fails without: `gives the frame the link: up while the snapshot is fresh, down while it is stale`: `setLink(!stale)` and its dependency on `stale`
Fails without: `closes the terminal once when the panel unmounts`: the cleanup's `close()`
Fails without: `closes it before the frame leaves the document`: a plain `useEffect` cleanup in place of the layout effect (it runs after the frame is removed, so `attached` is false)
Fails without: `unmounts even when the close throws`: the `try` around `close()`
Fails without: `closes nothing when the frame never gave an interface`: the `terminal === null` early return
Fails without: `dispatches ⌘K on the app's document, where the palette's one listener answers`: the `onChord` that dispatches `keydown` with `key: "k"`, `metaKey: true` and `bubbles: true` on `document` (without the meta key or `bubbles` the palette's listener never answers)
Fails without: `takes no focus before the terminal is live: not on taking the interface, not while waiting`: the `terminal.focus()` that a panel makes when it takes the interface (the removed bring-up effect), or on any phase but `live`
Fails without: `focuses a terminal the user brought up at its first live`: the `first && brought.current === true` branch that focuses (a panel that never focuses fails it)
Fails without: `takes the focus from no text field at its first live`: the `!isTextField(active)` term of the first-live branch
Fails without: `takes the focus at its first live from a control that is not a text field`: a first-live branch that applies the later-live rule (`active === frame.current`) to a terminal the user brought up
Fails without: `refocuses at a later live while the frame has the focus`: the `active === frame.current` term (a later live that never refocuses fails it)
Fails without: `takes no focus at a later live while the body, a control, a text field or nothing has it`: a later-live rule widened by one term, each failing its own case: `|| active === document.body` fails the body case, `|| active === null` the nothing case, `|| !isTextField(active)` the control case, and a bare `focus()` fails them all
Fails without: `takes none at its first live in the inspector, from a stored choice, and refocuses at a later live while the frame has the focus`: the `brought` rule (a panel that focuses at every first live fails the first half; one that gates every focus on `brought` fails the second)
Fails without: `takes it in the inspector once its tab was pressed, and only for the terminal that press mounts`: `takeInspectorPress` consuming the mark at the first commit (a mark that stays set focuses the second terminal), and `brought.current = !inInspector || pressed`
Fails without: `takes no focus on the phone, where the keyboard must stay down`: the `phone` early return in the live effect

Fails without: `terminalApplies` (each row): the `host.terminal &&` term (the switch-off row) and `sessionActions(session).diff` (the paused, recoverable, unstarted, loading and deleting rows)
Fails without: `TerminalBody draws the terminal for a session it applies to`: the branch that draws `Terminal`
Fails without: `TerminalBody says the terminal is off on this host, with no frame`: the switch-off message, and the frame not drawn
Fails without: `TerminalBody says the session is not running when the host's terminal is on`: the not-running message (a ternary swapped between the two causes fails this and the one above)
Fails without: `TerminalBody says the session is gone when the snapshot does not list it`: the `NoSession` branch
Fails without: `TerminalBody swaps the frame for the message when the host turns the terminal off, closing it`: `TerminalBody` drawing the message in place of `Terminal`, and the close on its unmount
Fails without: `TerminalBody gives another session a frame of its own`: `key={session.id}`

- [ ] **Step 2: Run it to see it fail.**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/terminal/Terminal.test.tsx
```

Expected: FAIL, `Failed to resolve import "./Terminal" from "src/panels/terminal/Terminal.test.tsx"`: the module does not exist yet.

- [ ] **Step 3: Implement.**

In `internal/web/ui/src/app/shell.tsx`, replace

```tsx
function isTextField(target: EventTarget | null): boolean {
```

with

```tsx
// isTextField is whether the target is a field that brings up the keyboard; the terminal panel's
// focus rule (panels/terminal/Terminal.tsx) reads it too.
export function isTextField(target: EventTarget | null): boolean {
```

Create `internal/web/ui/src/panels/terminal/Terminal.tsx`:

```tsx
// The terminal panel's body (spec §39–§43, §55, §57, §61): the frame of `/terminal.html`, driven
// only through the interface `frameTerminal` reads off it (terminal/frame.ts), the frame's states
// drawn over it, and on the desktop the focus. The frame owns xterm.js, the socket and the
// reconnecting; nothing here reaches into the frame's document (ui_rules_test.go pins it).
import { useEffect, useLayoutEffect, useRef, useState, type ReactElement } from "react";

import type { Session } from "@/api/gen";
import type { HostFacts, PanelProps } from "@/app/panels";
import { isTextField } from "@/app/shell";
import { useLayout } from "@/app/viewport";
import { sessionActions } from "@/commands/session";
import { Button } from "@/components/ui/button";
import { NoSession } from "@/session/NoSession";
import { useHostFacts, useLink, useSession } from "@/store/fleet";
import { frameTerminal, type FrameState, type LoomTerminal } from "@/terminal/frame";
import { CLOSE_EXITED, describeClose } from "@/terminal/socket";

// terminalApplies is whether the session has a terminal tab on this host: the host's switch is on
// and the session is started, active and not paused. `sessionActions(...).diff` is that set (not
// busy, not in the paused group, which holds the Recoverable and unstarted sessions), as the
// changes tab uses it; the daemon's request refuses the rest, and a refusal closes the connection
// with its reason.
export function terminalApplies(session: Session, host: HostFacts): boolean {
  return host.terminal && sessionActions(session).diff;
}

// The inspector's terminal tab was pressed (spec §57): the terminal that press mounts takes the
// focus, and one the inspector mounts from a stored choice does not. The tab's button marks it
// (desktop/Inspector.tsx), the terminal it mounts takes the mark at its first commit, and the
// inspector takes it again at the end of the same commit, so a press whose terminal does not mount
// (the boundary's fallback, a session that does not run) leaves no mark for a later terminal.
let inspectorPressed = false;

// markInspectorPress records that the user pressed the inspector's terminal tab.
export function markInspectorPress(): void {
  inspectorPressed = true;
}

// takeInspectorPress is whether the inspector's terminal tab was pressed since the last take.
export function takeInspectorPress(): boolean {
  const was = inspectorPressed;
  inspectorPressed = false;
  return was;
}

// openPalette is the frame's chord (⌘K pressed inside it) as the app's own key event: the app's
// one key listener answers it on `document`, and the entry bundle imports nothing of the desktop
// chunk that holds the palette.
function openPalette(): void {
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true, bubbles: true }));
}

// Notice is what the panel says over the frame: nothing while live, else the state, and for the
// shell's exit (4004) alone a button that connects again. A terminal that could not start is a
// `closed` state with code 0 (spec §68): its reason is shown as any other, with no button.
function Notice(props: {
  state: FrameState | null;
  failed: boolean;
  terminal: LoomTerminal | null;
}): ReactElement | null {
  const { state, failed, terminal } = props;
  let text: string;
  let again = false;
  if (failed) {
    text = "The terminal did not load.";
  } else if (state === null || state.phase === "connecting") {
    text = "Connecting…";
  } else if (state.phase === "waiting") {
    text = "Reconnecting…";
  } else if (state.phase === "closed") {
    text = state.closed === undefined ? "The terminal closed." : describeClose(state.closed);
    again = state.closed?.code === CLOSE_EXITED;
  } else {
    return null;
  }
  return (
    <div
      role="status"
      className="absolute inset-x-0 bottom-0 flex flex-wrap items-center gap-3 bg-card px-3 py-2 text-sm text-attention"
    >
      <span>{text}</span>
      {again && terminal !== null ? (
        <Button
          variant="secondary"
          size="lg"
          onClick={() => {
            terminal.reconnect();
          }}
        >
          Start a new shell
        </Button>
      ) : null}
    </div>
  );
}

// Terminal draws the session's shell: the frame, its interface once the frame has loaded, and the
// states over it. The session comes from its props only. It holds the interface and the latest
// state, which the phone's input (PhoneInput) is given from here.
export function Terminal(props: PanelProps): ReactElement {
  const phone = useLayout() === "phone";
  const { stale } = useLink();
  const root = useRef<HTMLDivElement>(null);
  const frame = useRef<HTMLIFrameElement>(null);
  const [terminal, setTerminal] = useState<LoomTerminal | null>(null);
  const [state, setState] = useState<FrameState | null>(null);
  const [failed, setFailed] = useState(false);
  // brought: whether the user brought the panel up (spec §57), decided once at the first commit.
  // In the middle column it is always so; in the inspector only the press of its tab does it.
  const brought = useRef<boolean | null>(null);
  const wasLive = useRef(false);
  const lived = useRef(false);

  useLayoutEffect(() => {
    if (brought.current !== null) return;
    const pressed = takeInspectorPress();
    const inInspector = (root.current?.closest('[data-slot="inspector"]') ?? null) !== null;
    brought.current = !inInspector || pressed;
  }, []);

  // The close runs in a layout effect's cleanup, which React runs before it removes the frame
  // from the document (spec §61), and in a `try`, since a call into a discarded document may throw.
  useLayoutEffect(() => {
    if (terminal === null) return;
    return () => {
      try {
        terminal.close();
      } catch {
        // The frame is gone already; its connection went with it.
      }
    };
  }, [terminal]);

  useEffect(() => {
    if (terminal === null) return;
    terminal.onState(setState);
    terminal.onChord(openPalette);
  }, [terminal]);

  useEffect(() => {
    terminal?.setTouch(phone);
  }, [terminal, phone]);

  useEffect(() => {
    terminal?.setLink(!stale);
  }, [terminal, stale]);

  // The focus (spec §57 as §67 narrows it). The panel never calls focus() before a live: until the
  // frame's terminal has started it would only be held. At the first live of a terminal the user
  // brought up it focuses unless a text field of the app has the focus then; at every other live
  // (a later one, or the first of a terminal that was not brought up) it focuses only while the
  // app's focused element is the frame itself, so a reconnect gives the focus back to a terminal
  // being typed in and never takes it from the body, the reply box or another control.
  const phase = state?.phase;
  useEffect(() => {
    const live = phase === "live";
    const entered = live && !wasLive.current;
    wasLive.current = live;
    if (!entered) return;
    const first = !lived.current;
    lived.current = true;
    if (terminal === null || phone) return;
    const active = document.activeElement;
    const focus =
      first && brought.current === true
        ? !isTextField(active)
        : frame.current !== null && active === frame.current;
    if (focus) terminal.focus();
  }, [phase, terminal, phone]);

  return (
    <div
      ref={root}
      data-slot="terminal"
      data-terminal-state={phase}
      className="flex min-h-[24rem] flex-1 flex-col"
    >
      <div className="relative flex min-h-0 flex-1 flex-col">
        <iframe
          ref={frame}
          src={`/terminal.html#${String(props.sessionId ?? "")}`}
          title="Terminal"
          onLoad={(e) => {
            const next = frameTerminal(e.currentTarget);
            setTerminal(next);
            setFailed(next === null);
          }}
          className="min-h-[50dvh] w-full flex-1 border-0 bg-background"
        />
        <Notice state={state} failed={failed} terminal={terminal} />
      </div>
    </div>
  );
}

// TerminalBody is the panel's Body: the terminal for a session it applies to, else why it does not
// (a direct link to the tab while the host's terminal is off, or the session is not running). The
// frame is keyed by the session, so another session gets a frame of its own.
export function TerminalBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  const host = useHostFacts();
  if (session === undefined) return <NoSession sessionId={props.sessionId} />;
  if (!terminalApplies(session, host)) {
    return (
      <p role="status" className="px-4 py-8 text-center text-muted-foreground">
        {host.terminal
          ? "This session is not running, so it has no terminal."
          : "The terminal is off on this host. loom web terminal on turns it on."}
      </p>
    );
  }
  return <Terminal key={session.id} {...props} />;
}
```

Run `pnpm -C internal/web/ui exec prettier --write src/panels/terminal/Terminal.tsx src/app/shell.tsx`.

- [ ] **Step 4: Run it to see it pass.**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/terminal/Terminal.test.tsx src/app/shell.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: both test files pass, and `tsc --noEmit` and `eslint .` print nothing.

- [ ] **Step 5: Commit.**

```bash
git add -N internal/web/ui/src/panels/terminal/Terminal.tsx internal/web/ui/src/panels/terminal/Terminal.test.tsx
git commit -m "feat(web): the terminal panel's body, its frame, states and desktop focus" -- internal/web/ui/src/panels/terminal/Terminal.tsx internal/web/ui/src/panels/terminal/Terminal.test.tsx internal/web/ui/src/app/shell.tsx
```

### Task 4.3: The panel's registration: `PanelId`, routing, the tab and its place, and the inspector's rule

**Files:**
- Create: `internal/web/ui/src/panels/terminal/index.tsx`
- Modify: `internal/web/ui/src/panels/index.ts:5-8`
- Modify: `internal/web/ui/src/app/panels.ts:7-10`
- Modify: `internal/web/ui/src/app/panels.test.ts:117-141`
- Modify: `internal/web/ui/src/app/routes.tsx:99-130,180-197`
- Modify: `internal/web/ui/src/app/routes.test.ts:60-72,95-112,115-130,150-165`
- Modify: `internal/web/ui/src/app/shell.tsx:324-334`
- Modify: `internal/web/ui/src/app/shell.test.tsx:291-320,869-880,889-896`
- Modify: `internal/web/ui/src/panels/info/index.tsx:21`
- Modify: `internal/web/ui/src/panels/info/info.test.tsx:403`
- Modify: `internal/web/ui/src/panels/mockup/index.tsx:1,31`
- Modify: `internal/web/ui/src/panels/mockup/mockup.test.tsx:202-203`
- Modify: `internal/web/ui/src/panels/flow/index.tsx:1,31`
- Modify: `internal/web/ui/src/panels/flow/flow.test.tsx:709-710`
- Modify: `internal/web/ui/src/desktop/inspector.ts:1-6,13-30`
- Modify: `internal/web/ui/src/desktop/inspector.test.ts`
- Modify: `internal/web/ui/src/desktop/Inspector.tsx:6-9,23-30,44-58`
- Modify: `internal/web/ui/src/desktop/Inspector.test.tsx`
- Modify: `internal/web/README.md:97`

**Defines (contract, Story 4):** `PanelId` gaining `"terminal"`, its path `/s/<id>/terminal` (hash `#s/<id>/terminal`), and the registered `terminal` panel (tab order 3, label "terminal"). **Uses:** Task 4.2's `TerminalBody`, `terminalApplies`, `markInspectorPress` and `takeInspectorPress`; Task 4.1's `appliesTo` signature. The router's own table `PATHS` (`routes.tsx`) is a `Record<PanelId, string>`, so the new id must have its entry there; the session-route pattern, `named` and `hrefFor`'s local `paths` are the three other places the id is spelled.

- [ ] **Step 1: Write the failing tests.**

In `internal/web/ui/src/app/routes.test.ts`, replace

```ts
    ["#s/5/diff", route("changes", { id: "5" })],
```

with

```ts
    ["#s/5/diff", route("changes", { id: "5" })],
    ["#s/5/terminal", route("terminal", { id: "5" })],
    ["#/s/5/terminal", route("terminal", { id: "5" })],
```

Replace

```ts
    "#s/5/turns/full",
```

with

```ts
    "#s/5/turns/full",
    "#s/5/terminal/full",
```

Replace

```ts
    ["changes", { id: "5" }, {}, "#/s/5/diff"],
```

with

```ts
    ["changes", { id: "5" }, {}, "#/s/5/diff"],
    ["terminal", { id: "5" }, {}, "#/s/5/terminal"],
```

Replace

```ts
      ["changes", { id: "5" }, {}],
```

with

```ts
      ["changes", { id: "5" }, {}],
      ["terminal", { id: "5" }, {}],
```

In `internal/web/ui/src/app/panels.test.ts`, replace

```ts
      changes: true,
      mockup: true,
```

with

```ts
      changes: true,
      terminal: true,
      mockup: true,
```

and replace

```ts
    for (const id of Object.keys(ids) as PanelId[]) {
      expect(registry.panelFor(id)?.id).toBe(id);
    }
  }, 30_000);
```

with

```ts
    for (const id of Object.keys(ids) as PanelId[]) {
      expect(registry.panelFor(id)?.id).toBe(id);
    }

    // The terminal is the third tab, after changes; info, mockup and flow follow it.
    expect(registry.tabOrder()).toEqual([
      "turns",
      "changes",
      "terminal",
      "info",
      "mockup",
      "flow",
    ]);
    // It applies by the host's switch and the session's state.
    const tabsOf = (session: Session, host: HostFacts): string[] =>
      registry.sessionTabs(session, host).map((p) => p.id);
    const running = aSession({ claude: true, status: "Running", group: "working" });
    expect(tabsOf(running, { terminal: true })).toContain("terminal");
    expect(tabsOf(running, { terminal: false })).not.toContain("terminal");
    const paused = aSession({ status: "Paused", group: "paused" });
    expect(tabsOf(paused, { terminal: true })).not.toContain("terminal");
  }, 30_000);
```

In `internal/web/ui/src/app/shell.test.tsx`, replace

```ts
    ["changes", "changes"],
    ["agent", "turns"],
```

with

```ts
    ["changes", "changes"],
    ["terminal", "terminal"],
    ["agent", "turns"],
```

and replace

```ts
    expect(TABBED.has("turns")).toBe(true);
    expect(TABBED.has("job")).toBe(true);
```

with

```ts
    expect(TABBED.has("turns")).toBe(true);
    expect(TABBED.has("terminal")).toBe(true);
    expect(TABBED.has("job")).toBe(true);
```

and add to `describe("router", () => {`, directly after its second test (`draws the fleet for a path no route matches (spec §27)`, which ends with `expect(await screen.findByText("the fleet panel")).toBeInTheDocument();\n  });`), this test:

```tsx
  it("has a route for the terminal's path in the router's own table", () => {
    expect(Object.keys(router.routesByPath)).toContain("/s/$id/terminal");
  });
```

In `internal/web/ui/src/desktop/inspector.test.ts`, replace the line

```ts
const TABS: PanelId[] = ["turns", "changes", "info"];
```

with

```ts
const TABS: PanelId[] = ["turns", "changes", "info"];

// The same with the terminal, which applies to the session under test (spec §58).
const ORDER_T: PanelId[] = ["turns", "changes", "terminal", "info", "mockup", "flow"];
const TABS_T: PanelId[] = ["turns", "changes", "terminal", "info"];
```

and replace the end of the file

```ts
  it("leaves its arguments as they were", () => {
```

with

```ts
  it("draws the terminal when it is the stored choice and applies", () => {
    expect(inspectorTab(ORDER_T, TABS_T, "turns", "terminal")).toBe("terminal");
    expect(inspectorTab(ORDER_T, TABS_T, "info", "terminal")).toBe("terminal");
  });

  it("never falls back to the terminal: no choice, a choice that does not apply, the middle's choice", () => {
    // No stored choice.
    expect(inspectorTab(ORDER_T, TABS_T, "turns", undefined)).toBe("changes");
    expect(inspectorTab(ORDER_T, TABS_T, "changes", undefined)).toBe("turns");
    // A stored tab no panel has.
    expect(inspectorTab(ORDER_T, TABS_T, "turns", "bogus")).toBe("changes");
    // The walk after the middle's own choice passes the terminal.
    expect(inspectorTab(ORDER_T, TABS_T, "changes", "changes")).toBe("info");
    // The walk after a choice that does not apply passes it too (changes is not among the tabs).
    expect(inspectorTab(ORDER_T, ["turns", "terminal", "info"], "turns", "changes")).toBe("info");
    // The terminal and the middle's tab alone: nothing else to fall to.
    expect(inspectorTab(ORDER_T, ["changes", "terminal"], "changes", undefined)).toBeUndefined();
  });

  it("walks past the terminal when it is the middle's tab and the stored choice", () => {
    expect(inspectorTab(ORDER_T, TABS_T, "terminal", "terminal")).toBe("info");
  });

  it("walks past a stored terminal that does not apply", () => {
    expect(inspectorTab(ORDER_T, TABS, "turns", "terminal")).toBe("info");
  });

  it("draws nothing rather than the terminal when only it and the middle's tab apply", () => {
    expect(inspectorTab(ORDER_T, ["turns", "terminal"], "turns", undefined)).toBeUndefined();
    expect(inspectorTab(ORDER_T, ["turns", "terminal"], "turns", "terminal")).toBe("terminal");
  });

  it("leaves its arguments as they were", () => {
```

In `internal/web/ui/src/desktop/Inspector.test.tsx` (which Task 4.1 gave the `useFleet` import), replace

```tsx
import { useEffect, type ReactElement } from "react";
```

with

```tsx
import { useEffect, useLayoutEffect, type ReactElement } from "react";
```

and add to the imports of the file, after `import { allSessions } from "@/fleet/groups";` and before the `@/store/fleet` import:

```tsx
import { takeInspectorPress } from "@/panels/terminal/Terminal";
```

Add inside `describe("Inspector", () => {`, after the test added in Task 4.1 (`lists a tab that applies by the host's switch as the stream's terminal event turns it on`), these tests:

```tsx
  // withTerminal defines the terminal tab as it applies by the host's switch, and turns the switch
  // on; its body is as given.
  function withTerminal(Body: () => ReactElement = () => <p>terminal body</p>): void {
    tabs();
    definePanel({
      id: "terminal",
      title: () => "Terminal",
      tab: { order: 5, label: "terminal" },
      appliesTo: (_session, host) => host.terminal,
      Body,
    });
    useFleet.setState({ snapshot: { ...fixtureSnapshot(), terminal: true } });
  }

  it("draws the terminal when it is the stored choice", () => {
    useDesktop.setState({ slots: [{ tab: "terminal" }] });
    withTerminal();
    render(<Inspector session={claude()} middle="turns" />);
    expect(names()).toEqual(["changes", "info", "terminal"]);
    expect(lit()).toBe("terminal");
    expect(screen.getByText("terminal body")).toBeInTheDocument();
  });

  it("never draws the terminal as a fallback: no choice, or a choice past it", () => {
    withTerminal();
    const view = render(<Inspector session={claude()} middle="turns" />);
    expect(lit()).toBe("changes");
    expect(screen.queryByText("terminal body")).toBeNull();
    view.unmount();

    useDesktop.setState({ slots: [{ tab: "info" }] });
    render(<Inspector session={claude()} middle="info" />);
    expect(lit()).toBe("conversation");
    expect(screen.queryByText("terminal body")).toBeNull();
  });

  // Mounted is a stand-in for the terminal: it takes the inspector's press mark in a layout effect
  // when it mounts, as Terminal does, and records what it took.
  function mountedInto(taken: boolean[]): () => ReactElement {
    return function Mounted(): ReactElement {
      useLayoutEffect(() => {
        taken.push(takeInspectorPress());
      }, []);
      return <p>terminal body</p>;
    };
  }

  it("marks the press of the terminal's tab for the terminal that press mounts, and no other press", async () => {
    const taken: boolean[] = [];
    withTerminal(mountedInto(taken));
    render(<Inspector session={claude()} middle="turns" />);
    expect(taken).toEqual([]);

    await userEvent.click(within(strip()).getByRole("button", { name: "info" }));
    expect(taken).toEqual([]);

    await userEvent.click(within(strip()).getByRole("button", { name: "terminal" }));
    expect(taken).toEqual([true]);

    // Pressing the tab that is already drawn mounts nothing and marks nothing.
    await userEvent.click(within(strip()).getByRole("button", { name: "terminal" }));
    expect(taken).toEqual([true]);
    expect(takeInspectorPress()).toBe(false);

    // Away and back mounts a new terminal, which the press marks again.
    await userEvent.click(within(strip()).getByRole("button", { name: "info" }));
    await userEvent.click(within(strip()).getByRole("button", { name: "terminal" }));
    expect(taken).toEqual([true, true]);
  });

  it("leaves no mark behind when the press's terminal does not mount", async () => {
    const taken: boolean[] = [];
    const Mounted = mountedInto(taken);
    let mounts = false;
    withTerminal(() => (mounts ? <Mounted /> : <p>no terminal</p>));
    const view = render(<Inspector session={claude()} middle="turns" />);

    // The press draws the panel's message in the terminal's place, and mounts no terminal.
    await userEvent.click(within(strip()).getByRole("button", { name: "terminal" }));
    expect(screen.getByText("no terminal")).toBeInTheDocument();
    expect(taken).toEqual([]);
    view.unmount();

    // A terminal the inspector now mounts from the stored choice is not the press's.
    mounts = true;
    render(<Inspector session={claude()} middle="turns" />);
    expect(screen.getByText("terminal body")).toBeInTheDocument();
    expect(taken).toEqual([false]);
  });
```

The three panels that move to 4, 5 and 6 have tests that pin their old places. In `internal/web/ui/src/panels/info/info.test.tsx`, replace

```tsx
    expect(panel?.tab).toEqual({ order: 3, label: "info" });
```

with

```tsx
    expect(panel?.tab).toEqual({ order: 4, label: "info" });
```

In `internal/web/ui/src/panels/mockup/mockup.test.tsx`, replace

```tsx
  it("are registered: the fourth tab for a session with a mockup, and the full view with no head", () => {
    const panel = panelFor("mockup");
    expect(panel?.tab).toEqual({ order: 4, label: "mockup", Mark: MockupMark });
```

with

```tsx
  it("are registered: the fifth tab for a session with a mockup, and the full view with no head", () => {
    const panel = panelFor("mockup");
    expect(panel?.tab).toEqual({ order: 5, label: "mockup", Mark: MockupMark });
```

In `internal/web/ui/src/panels/flow/flow.test.tsx`, replace

```tsx
  it("the flow panel: the fifth tab for a session with flows, under the session's head, its body a lazy chunk", () => {
    expect(panelFor("flow")).toBe(flowPanel);
    expect(flowPanel.tab).toEqual({ order: 5, label: "flow", Mark: FlowMark });
```

with

```tsx
  it("the flow panel: the sixth tab for a session with flows, under the session's head, its body a lazy chunk", () => {
    expect(panelFor("flow")).toBe(flowPanel);
    expect(flowPanel.tab).toEqual({ order: 6, label: "flow", Mark: FlowMark });
```

Run `pnpm -C internal/web/ui exec prettier --write src/app/routes.test.ts src/app/panels.test.ts src/app/shell.test.tsx src/desktop/inspector.test.ts src/desktop/Inspector.test.tsx src/panels/info/info.test.tsx src/panels/mockup/mockup.test.tsx src/panels/flow/flow.test.tsx`.

Fails without: the routes rows (`#s/5/terminal`, `#/s/5/terminal`, `hrefFor` and the round trip): the `terminal` alternative of the session-route pattern, the `terminal: "terminal"` entry of `named` and the `terminal` path in `paths`
Fails without: `#s/5/terminal/full is the fleet`: a pattern that lets `/full` follow a segment other than `mockup`
Fails without: `has a panel for every PanelId once @/panels is imported`: the `import "./terminal"` line in `panels/index.ts`, the panel's `tab: { order: 3 }` with info, mockup and flow at 4, 5 and 6, and its `appliesTo: terminalApplies`
Fails without: `tabOf lights, for the route terminal, the tab terminal`: a `tabOf` rule that maps the terminal route to another tab
Fails without: `TABBED holds the session's routes and not the fleet's`: `"terminal"` in `TABBED`
Fails without: `draws the terminal when it is the stored choice and applies` (inspector.test.ts): the kept-choice branch of `inspectorTab` excluding the terminal
Fails without: `never falls back to the terminal: no choice, a choice that does not apply, the middle's choice`: the `id !== "terminal"` term of `fallback`, in the walk and in the last `tabs.find`
Fails without: `walks past the terminal when it is the middle's tab and the stored choice` and `walks past a stored terminal that does not apply`: the walk using `fallback` instead of `usable`
Fails without: `draws nothing rather than the terminal when only it and the middle's tab apply`: the fallback test on the last line, and the kept branch for the second assertion
Fails without: `draws the terminal when it is the stored choice` (Inspector.test.tsx): the kept branch through the component
Fails without: `never draws the terminal as a fallback: no choice, or a choice past it`: the same rule through the component
Fails without: `marks the press of the terminal's tab for the terminal that press mounts, and no other press`: `markInspectorPress()` on a press of the terminal tab that is not already drawn (a press that marks nothing fails `[true]`, and one that marks the drawn tab too fails the `takeInspectorPress()` after the second press: pressing the drawn tab commits nothing, so its mark stays until taken)
Fails without: `leaves no mark behind when the press's terminal does not mount`: the `useLayoutEffect(() => { takeInspectorPress(); })` in `Inspector` (the mark stays set and the terminal mounted from the stored choice takes `true`)
Fails without: `has a route for the terminal's path in the router's own table` (shell.test.tsx): the `terminal: "/s/$id/terminal"` entry of `PATHS` in `routes.tsx` (the router builds its routes from `Object.values(PATHS)`; without the entry the typecheck fails too, since `PATHS` is a `Record<PanelId, string>`)
Fails without: the three edited tab-order assertions (info 4, mockup 5, flow 6): the `order` moves in the three `index.tsx` files

- [ ] **Step 2: Run them to see them fail.**

```bash
pnpm -C internal/web/ui exec vitest run src/app/routes.test.ts src/app/shell.test.tsx src/desktop/inspector.test.ts src/desktop/Inspector.test.tsx src/panels/info/info.test.tsx src/panels/mockup/mockup.test.tsx src/panels/flow/flow.test.tsx && pnpm -C internal/web/ui exec vitest run src/app/panels.test.ts
```

Expected: FAIL. The routes rows come back as `route("fleet")` (`expected { panel: 'fleet', … } to deeply equal { panel: 'terminal', … }`), `hrefFor("terminal", …)` returns `"#undefined"` (`paths["terminal"]` is undefined; vitest does not typecheck), `inspectorTab` returns `terminal` where the rule says `changes`, the router's table lacks the terminal's path (`expected [ '/', '/s/$id', … ] to include '/s/$id/terminal'`), the three tab-order assertions fail (`expected { order: 3, label: 'info' } to deeply equal { order: 4, … }`, and likewise mockup 4 against 5 and flow 5 against 6), the first press-mark test fails (nothing marks the press yet: the stand-in takes `false` where the test says `true`; the second passes until Step 3 marks the press, and goes red again if the inspector's clearing effect is left out), and `panels.test.ts` fails on the tab order (`expected [ 'turns', 'changes', 'info', 'mockup', 'flow' ] to deeply equal [ 'turns', 'changes', 'terminal', … ]`).

- [ ] **Step 3: Implement.**

In `internal/web/ui/src/app/panels.ts`, replace

```ts
  | "info"
  | "changes"
  | "mockup"
```

with

```ts
  | "info"
  | "changes"
  | "terminal"
  | "mockup"
```

In `internal/web/ui/src/app/routes.tsx`, replace

```ts
  const main = h.match(/^s\/(\d+)(?:\/(turns|diff|mockup|info))?(\/full)?$/);
```

with

```ts
  const main = h.match(/^s\/(\d+)(?:\/(turns|diff|terminal|mockup|info))?(\/full)?$/);
```

and replace

```ts
    const named: Partial<Record<string, PanelId>> = {
      diff: "changes",
      mockup: "mockup",
      info: "info",
    };
```

with

```ts
    const named: Partial<Record<string, PanelId>> = {
      diff: "changes",
      terminal: "terminal",
      mockup: "mockup",
      info: "info",
    };
```

and replace

```ts
    changes: `${session}/diff`,
```

with

```ts
    changes: `${session}/diff`,
    terminal: `${session}/terminal`,
```

and, in the router's own table, replace

```ts
  changes: "/s/$id/diff",
  mockup: "/s/$id/mockup",
```

with

```ts
  changes: "/s/$id/diff",
  terminal: "/s/$id/terminal",
  mockup: "/s/$id/mockup",
```

In `internal/web/ui/src/app/shell.tsx`, replace

```ts
  "info",
  "changes",
  "mockup",
```

with

```ts
  "info",
  "changes",
  "terminal",
  "mockup",
```

Create `internal/web/ui/src/panels/terminal/index.tsx`:

```tsx
// The terminal panel: the session's worktree shell, the third tab, shown while the host's terminal
// is on and the session runs. Its body is in the entry (a frame and its states; xterm.js stays in
// the frame's own document), so it adds no lazy chunk.
import { definePanel } from "@/app/panels";
import { SessionHead } from "@/session/SessionHead";

import { TerminalBody, terminalApplies } from "./Terminal";

export const terminalPanel = definePanel({
  id: "terminal",
  title: () => "Terminal",
  appliesTo: terminalApplies,
  tab: { order: 3, label: "terminal" },
  Head: SessionHead,
  Body: TerminalBody,
});
```

In `internal/web/ui/src/panels/index.ts`, replace

```ts
import "./changes";
```

with

```ts
import "./changes";
import "./terminal";
```

In `internal/web/ui/src/panels/info/index.tsx`, replace `  tab: { order: 3, label: "info" },` with `  tab: { order: 4, label: "info" },`.

In `internal/web/ui/src/panels/mockup/index.tsx`, replace `  tab: { order: 4, label: "mockup", Mark: MockupMark },` with `  tab: { order: 5, label: "mockup", Mark: MockupMark },`, and in its first comment line replace `as the fourth tab for a session that has` with `as the fifth tab for a session that has`.

In `internal/web/ui/src/panels/flow/index.tsx`, replace `  tab: { order: 5, label: "flow", Mark: FlowMark },` with `  tab: { order: 6, label: "flow", Mark: FlowMark },`, and in its first comment line replace `the last tab (order 5)` with `the last tab (order 6)`.

In `internal/web/ui/src/desktop/inspector.ts`, replace the header comment's last line

```ts
// first applicable one that is not the middle's.
```

with

```ts
// first applicable one that is not the middle's. The terminal is drawn only as the choice itself
// (spec §58): neither fallback lands on it, since drawing it starts a shell, and browsing sessions
// would start one for each.
```

and replace the body

```ts
  const usable = (id: PanelId): boolean => id !== middle && tabs.includes(id);
  const kept = tabs.find((id) => id === chosen);
  if (kept !== undefined && usable(kept)) return kept;
  const at = chosen === undefined ? -1 : order.findIndex((id) => id === chosen);
  if (at >= 0) {
    // Every other id of `order` once, starting after the choice and wrapping past the end.
    for (let step = 1; step < order.length; step++) {
      const id = order[(at + step) % order.length];
      if (usable(id)) return id;
    }
    return undefined;
  }
  return tabs.find((id) => id !== middle);
```

with

```ts
  const usable = (id: PanelId): boolean => id !== middle && tabs.includes(id);
  // A fallback is a usable tab that is not the terminal.
  const fallback = (id: PanelId): boolean => usable(id) && id !== "terminal";
  const kept = tabs.find((id) => id === chosen);
  if (kept !== undefined && usable(kept)) return kept;
  const at = chosen === undefined ? -1 : order.findIndex((id) => id === chosen);
  if (at >= 0) {
    // Every other id of `order` once, starting after the choice and wrapping past the end.
    for (let step = 1; step < order.length; step++) {
      const id = order[(at + step) % order.length];
      if (fallback(id)) return id;
    }
    return undefined;
  }
  return tabs.find(fallback);
```

In `internal/web/ui/src/desktop/Inspector.tsx`, replace

```tsx
import { Boundary, PanelHost, resetKey } from "@/app/shell";
import { useHostFacts } from "@/store/fleet";
```

with

```tsx
import { Boundary, PanelHost, resetKey } from "@/app/shell";
import { markInspectorPress, takeInspectorPress } from "@/panels/terminal/Terminal";
import { useHostFacts } from "@/store/fleet";
```

and replace

```tsx
import { useMemo, useState, type ReactElement } from "react";
```

with

```tsx
import { useLayoutEffect, useMemo, useState, type ReactElement } from "react";
```

and replace

```tsx
  if (tab === undefined) return null;

  return (
    <aside
      data-slot="inspector"
```

with

```tsx
  // A press of the terminal's tab marks the terminal that the same commit mounts (spec §57). Once
  // the commit is done the mark is spent: layout effects run children first, so a terminal
  // mounted by the press has taken it already, and a press whose terminal did not mount (the
  // boundary's fallback, a session that does not run) leaves nothing for a terminal the
  // inspector mounts later from a stored choice.
  useLayoutEffect(() => {
    takeInspectorPress();
  });

  if (tab === undefined) return null;

  return (
    <aside
      data-slot="inspector"
```

and replace

```tsx
              onClick={() => {
                setTab(p.id);
              }}
```

with

```tsx
              onClick={() => {
                // The terminal the press mounts takes the focus (spec §57); one mounted from a
                // stored choice takes none, and pressing the drawn tab mounts nothing.
                if (p.id === "terminal" && p.id !== tab) markInspectorPress();
                setTab(p.id);
              }}
```

In `internal/web/README.md`, in the paragraph that begins `Each classic session screen is a panel of the new app` (line 97; Story 3's edit of this file is in the paragraph about the terminal's document, and Story 6's in the screens paragraph, so this line is as the tree has it), make two edits and no other. Replace

```
`changes` (`#/s/<id>/diff`), `agent`
```

with

```
`changes` (`#/s/<id>/diff`), `terminal` (`#/s/<id>/terminal`: the session's worktree shell in a frame of `/terminal.html`, drawn while the host's terminal is on and the session runs), `agent`
```

and replace

```
with its line counts), info and mockup (while one is set
```

with

```
with its line counts), terminal (while the host's terminal is on and the session is started, active and not paused; the stream's `terminal` event adds and drops the tab), info and mockup (while one is set
```

Run `pnpm -C internal/web/ui exec prettier --write src/app/panels.ts src/app/routes.tsx src/app/shell.tsx src/panels/terminal/index.tsx src/panels/index.ts src/panels/info/index.tsx src/panels/mockup/index.tsx src/panels/flow/index.tsx src/desktop/inspector.ts src/desktop/Inspector.tsx`.

- [ ] **Step 4: Run them to see them pass.**

```bash
pnpm -C internal/web/ui exec vitest run src/app src/desktop src/panels src/store && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: every test file passes (the `has a panel for every PanelId` case takes a few seconds, since it imports every panel), and `tsc --noEmit` and `eslint .` print nothing. `grep -rn 'order: [3-6]' internal/web/ui/src/panels --include=index.tsx` lists the terminal at 3, info 4, mockup 5 and flow 6.

- [ ] **Step 5: Commit.**

```bash
git add -N internal/web/ui/src/panels/terminal/index.tsx
git commit -m "feat(web): the terminal tab: its panel id, route, place among the tabs and the inspector's rule" -- internal/web/ui/src/panels/terminal/index.tsx internal/web/ui/src/panels/index.ts internal/web/ui/src/app/panels.ts internal/web/ui/src/app/panels.test.ts internal/web/ui/src/app/routes.tsx internal/web/ui/src/app/routes.test.ts internal/web/ui/src/app/shell.tsx internal/web/ui/src/app/shell.test.tsx internal/web/ui/src/panels/info/index.tsx internal/web/ui/src/panels/mockup/index.tsx internal/web/ui/src/panels/flow/index.tsx internal/web/ui/src/desktop/inspector.ts internal/web/ui/src/desktop/inspector.test.ts internal/web/ui/src/desktop/Inspector.tsx internal/web/ui/src/desktop/Inspector.test.tsx internal/web/ui/src/panels/info/info.test.tsx internal/web/ui/src/panels/mockup/mockup.test.tsx internal/web/ui/src/panels/flow/flow.test.tsx internal/web/README.md
```

### Task 4.4: The rule and its Go test, `USAGE.md`, and the rebuilt bundle

**Files:**
- Create: `internal/web/ui_rules_test.go`
- Modify: `internal/web/ui/CLAUDE.md:25`
- Modify: `USAGE.md:723-727,751-753,957-959`
- Modify: `internal/web/dist/**` (rebuilt)

**Defines (contract, Story 4):** `TestTerminalPanel_ReachesTheFrameOnlyThroughItsInterface`, in `internal/web/ui_rules_test.go`. **Defines (private to this story):** `frameDocumentLeaks(fsys fs.FS)` and its table test `TestFrameDocumentLeaks_FindsTheFramesDocumentAndWindow`. **Uses:** the files Tasks 4.2 and 4.3 created under `internal/web/ui/src/panels/terminal/`.

- [ ] **Step 1: Write the failing test.** Create `internal/web/ui_rules_test.go`:

```go
package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// frameDocumentNames are the properties through which code reaches into a frame's document or
// window. The terminal panel reads the frame only through the interface terminal/frame.ts gives
// it, whose one accessor (frameTerminal) is the only code that reads the frame's window.
var frameDocumentNames = []string{"contentDocument", "contentWindow"}

// frameDocumentLeaks lists, for every .ts and .tsx file under fsys, each name of
// frameDocumentNames it holds, as "<file>: <name>", and counts the files it looked at.
func frameDocumentLeaks(fsys fs.FS) (leaks []string, files int, err error) {
	err = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !(strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")) {
			return nil
		}
		files++
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		for _, word := range frameDocumentNames {
			if strings.Contains(string(body), word) {
				leaks = append(leaks, name+": "+word)
			}
		}
		return nil
	})
	return leaks, files, err
}

func TestFrameDocumentLeaks_FindsTheFramesDocumentAndWindow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
		count int
	}{
		{"a clean panel", map[string]string{"index.tsx": "export const a = frameTerminal(el);"}, nil, 1},
		{"the document", map[string]string{"Terminal.tsx": "frame.contentDocument.body"}, []string{"Terminal.tsx: contentDocument"}, 1},
		{"the window", map[string]string{"Terminal.tsx": "frame.contentWindow?.loomTerminal"}, []string{"Terminal.tsx: contentWindow"}, 1},
		{"a test file", map[string]string{"Terminal.test.tsx": "el.contentWindow"}, []string{"Terminal.test.tsx: contentWindow"}, 1},
		{"a nested file", map[string]string{"sub/Deep.ts": "x.contentDocument"}, []string{"sub/Deep.ts: contentDocument"}, 1},
		{"both names in one file", map[string]string{"A.tsx": "a.contentDocument; b.contentWindow"}, []string{"A.tsx: contentDocument", "A.tsx: contentWindow"}, 1},
		{"a file that is not source is not read", map[string]string{"notes.md": "contentWindow", "style.css": "contentDocument"}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for name, body := range tc.files {
				fsys[name] = &fstest.MapFile{Data: []byte(body)}
			}
			leaks, files, err := frameDocumentLeaks(fsys)
			require.NoError(t, err)
			require.Equal(t, tc.want, leaks)
			require.Equal(t, tc.count, files)
		})
	}
}

func TestTerminalPanel_ReachesTheFrameOnlyThroughItsInterface(t *testing.T) {
	leaks, files, err := frameDocumentLeaks(os.DirFS(filepath.Join("ui", "src", "panels", "terminal")))
	require.NoError(t, err)
	// index.tsx, Terminal.tsx and Terminal.test.tsx at least: a walk that finds nothing would pass
	// for ever.
	require.GreaterOrEqual(t, files, 3, "the walk found too few files under ui/src/panels/terminal")
	require.Empty(t, leaks, "the terminal panel reaches into the frame's document or window; go through terminal/frame.ts's interface")
}
```

Run `gofmt -l internal/web/ui_rules_test.go` (it prints nothing once the file is formatted; `gofmt -w internal/web/ui_rules_test.go` fixes it).

Fails without: `TestFrameDocumentLeaks_FindsTheFramesDocumentAndWindow`: each name in `frameDocumentNames` (dropping one fails its row), the `.ts`/`.tsx` suffix test (a walk that reads every file fails `a file that is not source is not read`; one that skips `.tsx` fails the panel rows) and the recursion (`a nested file`)
Fails without: `TestTerminalPanel_ReachesTheFrameOnlyThroughItsInterface`: a `contentDocument` or `contentWindow` in any file under `panels/terminal/` (the real files: `index.tsx`, `Terminal.tsx`, `Terminal.test.tsx`); its `files >= 3` guard fails a path that no longer exists or an empty walk

- [ ] **Step 2: Run it to see it pass, then to see it fail.**

```bash
CGO_ENABLED=0 go test ./internal/web/ -run 'TestFrameDocumentLeaks_|TestTerminalPanel_'
```

Expected: PASS (`ok  	github.com/aidan-bailey/loom/internal/web`): Tasks 4.2 and 4.3 wrote no such name. To see the real-tree test red, add `// contentWindow` to the first line of `internal/web/ui/src/panels/terminal/index.tsx`, run the command again (FAIL: `the terminal panel reaches into the frame's document or window … index.tsx: contentWindow`), and remove the line.

- [ ] **Step 3: Write the rule.** In `internal/web/ui/CLAUDE.md`, after the last rule, the line

```
- **Import xterm.js and its addons (`@xterm/xterm`, `@xterm/addon-webgl`, `@xterm/addon-fit`) only from the script of `terminal.html` (`internal/web/ui/src/terminal/main.ts`); that document is the one place a runtime `<style>` is allowed.** xterm.js appends `<style>` elements at run time that the app's `default-src 'self'` policy drops, so it runs in a document of its own that the web layer serves under `terminalCSP`, and an import from the app's entry would put it in every phone launch, running under the policy that blanks it. **Enforced** by `TestDist_XtermLoadsOnlyInTheTerminalPage`, which fails on xterm.js in a file the app's first load fetches or in any script the terminal page does not load; a runtime style elsewhere is **Convention** — a view that is unstyled only under the app's policy.
```

add this line after it:

```
- **Reach the terminal's frame only through the interface of `internal/web/ui/src/terminal/frame.ts` (`LoomTerminal`, which `frameTerminal` reads off a loaded frame); no file under `internal/web/ui/src/panels/terminal/` names the frame's document or window (`contentDocument`, `contentWindow`).** The frame is a document of its own whose script owns xterm.js, the socket and the reconnecting, and the interface is its one contract with the app: a panel that reaches into the frame's DOM or globals skips its link, its close on unmount and its focus rules, and breaks when the frame changes, with no type error. The panel stays in the entry bundle, a frame and its states with no lazy chunk, because xterm.js stays in the frame. **Enforced** by `TestTerminalPanel_ReachesTheFrameOnlyThroughItsInterface` (`internal/web/ui_rules_test.go`), whose detection table is `TestFrameDocumentLeaks_FindsTheFramesDocumentAndWindow`.
```

Then run the docs gate: `CGO_ENABLED=0 go test ./tools/claudemd/` and `go run ./tools/claudemd -v` (the budget lines for `internal/web/ui/CLAUDE.md` stay under 100 lines and 20,000 bytes; it had about 4,500 bytes of room and the new line is about 1,000).

- [ ] **Step 4: Write the docs.** In `USAGE.md`, replace

```
  runs (with its line counts), **info**, and **mockup** while the
```

with

```
  runs (with its line counts), **terminal** while the host's terminal
  is on and the session runs, **info**, and **mockup** while the
```

Replace

```
  file: tap a file for its hunks. It refreshes while the session works and
  shows no tool output.
```

with

```
  file: tap a file for its hunks. It refreshes while the session works and
  shows no tool output.
- **The terminal.** The **terminal** tab is the session's worktree shell
  (the one the TUI's terminal pane shares, kept by tmux), in a frame of its
  own. It shows while the host's terminal is on (`loom web terminal on`) and
  the session runs, not paused, busy or recoverable, and it comes and goes
  as the host turns the terminal on or off, with no reload; a link to it
  while it does not apply says why. On the desktop (1024px and wider) it
  takes the keyboard when you open it: type into it, scroll with the wheel
  and select with the mouse, copy with ⌘C or Ctrl+Shift+C and paste with ⌘V
  or Ctrl+Shift+V (Ctrl+C is the shell's interrupt). ⌘K opens the command
  palette from inside it, while Ctrl+K goes to the shell (readline's
  kill-line): open the palette from the top bar's box there. A full-screen
  program (vim, less, htop) gets no mouse and no wheel: drive it with keys.
  The panel says **Connecting…** and **Reconnecting…** (after loom
  restarts or the network drops it reconnects on its own and keeps the
  scrollback once), the reason when the terminal closes, and, after
  **The shell exited**, a **Start a new shell** button.
```

Replace

```
`Terminal:`. The app's terminal panel is not in this release.
```

with

```
`Terminal:`. The app's session screen shows a **terminal** tab while it is on.
```

- [ ] **Step 5: Rebuild the bundle.** `pnpm -C internal/web/ui run build` rewrites `internal/web/dist/`: `git status --short internal/web/dist` lists a changed `assets/index-<hash>.js` (the entry now holds the panel, with `terminal/frame.ts` and `terminal/socket.ts`, which import nothing of xterm.js), `index.html` (its script and preload names), possibly a new shared chunk the entry and the terminal page both import, and `sw.js` if the precache list changed; `assets/terminal-<hash>.js` may change its hash for the shared chunk.

- [ ] **Step 6: Run the gates for the story.**

```bash
bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'
```

Expected: all pass. `TestDist_XtermLoadsOnlyInTheTerminalPage` still passes (no xterm.js outside the terminal page's script), and `TestDist_FirstLoadIsWithinBudget` is the gate that shows whether the entry plus the panel still fits `firstLoadBudget`: if it fails, report it with the sizes it prints and change no budget (the design session decides).

- [ ] **Step 7: Commit.**

```bash
git add -N internal/web/ui_rules_test.go
git add internal/web/dist
git commit -m "docs(web): the terminal panel's rule and its test, the usage and the rebuilt bundle" -- internal/web/ui_rules_test.go internal/web/ui/CLAUDE.md USAGE.md internal/web/dist
```

## Story 5: The phone's input: the key row, the command line and the copy sheet

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/terminal/PhoneInput.tsx`, `internal/web/ui/src/panels/terminal/PhoneInput.test.tsx`, `internal/web/ui/src/panels/terminal/Terminal.tsx`, `internal/web/ui/src/panels/terminal/Terminal.test.tsx`, `internal/web/dist/**`, `USAGE.md`
- **Interfaces:** exposes `PhoneInput` and the `terminal-select` and `terminal-text` slots to Story 6; uses Story 3's `LoomTerminal`, `KeyName` and `Renderer`, and Story 4's `Terminal`
- **Verify:** `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** light
- **Form:** brief (a text field, a row of buttons and a sheet over an interface that exists)

On the phone the terminal takes no focus (Story 4 never calls `focus()` there, and Story 3's frame never focuses xterm.js in touch mode), so typing happens under the frame, in `PhoneInput` (spec §44, §55). A row of keys sends Ctrl-C, Ctrl-D, Tab, Esc and the four arrows through `key(name)` at once, and a press on it never takes the focus from the command line, so the keyboard stays up. The command line is a 16px one-line field; Enter sends its text as UTF-8 plus a carriage return (a bare one when empty) through `send`, clears the line only when `send` answered true, and keeps the focus. The keys and Enter are off unless the frame's phase is `live`, and nothing is queued (the frame drops input it cannot send, spec §55). Select opens the app's Drawer over `text()` read once as it opens, in a selectable `<pre>` scrolled to its end, with a footer naming the renderer and a Done button; loom writes nothing to the clipboard. `Terminal` draws `PhoneInput` under the frame when the layout is the phone's and the frame has given an interface and a state. `USAGE.md` describes the phone's keys and the sheet. None of this code holds a landmine marker, and it invents no logic for stores, ids, transactions, concurrency, outside calls, bounds or auth.

Two choices of this story where the spec is silent: **Select stays enabled while the terminal is not live** (copying the scrollback is most useful while it is reconnecting or closed, and it sends nothing), and the key row scrolls sideways on a narrow phone while Select stays pinned at its right end. The command line stays typeable while not live, so a half-typed line is not lost; only Enter waits.

### Task 5.1: `PhoneInput`: the key row and the command line

**Files:**
- Create: `internal/web/ui/src/panels/terminal/PhoneInput.tsx`
- Create: `internal/web/ui/src/panels/terminal/PhoneInput.test.tsx`

**Defines (contract, Story 5):** `function PhoneInput(props: { terminal: LoomTerminal; live: boolean; renderer: Renderer }): ReactElement` in `PhoneInput.tsx`. This task draws the key row (without Select) and the command line and leaves `renderer` unused; Task 5.2 adds Select and the sheet. **Uses:** Story 3's `LoomTerminal`, `KeyName` and `Renderer` (`@/terminal/frame`; type imports only, so the entry bundle gains nothing of xterm.js); the shared `Button` (`@/components/ui/button`) and `Input` (`@/components/ui/input`: 16px text, a 44px floor, and the field the app's other one-line inputs use).

**Behaviour.**
- The root is a `div` with `data-slot="terminal-phone-input"`, a column of two children, the key row and then the command line, on the card colour with a top border (`border-t border-border bg-card`) and `px-3 py-2 gap-2`. It holds no `role="status"` element (Story 4's tests find the panel's notice by that role).
- **The key row** is a flex row holding a scrolling part (`flex min-w-0 flex-1 gap-1.5 overflow-x-auto`) of one `Button` (`variant="secondary"`) per key, in this order, with these visible labels and accessible names, and these `KeyName`s: `Ctrl-C` (`ctrl-c`), `Ctrl-D` (`ctrl-d`), `Tab` (`tab`), `Esc` (`esc`), `↑` named `Up` by `aria-label` (`up`), `↓` `Down` (`down`), `←` `Left` (`left`), `→` `Right` (`right`). Task 5.2 adds the Select button after that part, outside the scroll. Every button is `min-h-[44px] min-w-[44px] shrink-0` (px, not rem: `html` is 13px) and `type="button"`. A click calls `terminal.key(name)`; the boolean it answers is ignored (nothing is queued, spec §55). Every key button is `disabled` unless `live`.
- **A press on the row never takes the focus from the command line:** the row's container handles `onMouseDown` by calling `preventDefault()` on the event (a click still fires; a browser moves the focus on mouse-down, and on a touch screen on the mouse events it synthesises after the tap).
- **The command line** is `Input` with `type="text"`, `aria-label="Command line"`, `placeholder="Command"`, `autoCorrect="off"`, `autoCapitalize="none"`, `autoComplete="off"`, `spellCheck={false}` and `enterKeyHint="send"`, controlled by a `useState("")`. It is never disabled. On `keydown` of `Enter`, when `live` is false nothing happens (the text stays); otherwise it calls `terminal.send(new TextEncoder().encode(line + "\r"))` (an empty line sends the bare `"\r"`), and only when that answered `true` sets the line to `""`. It never blurs the field, and does not `preventDefault` anything else.

- [ ] **Step 1: Write the failing tests.** Create `internal/web/ui/src/panels/terminal/PhoneInput.test.tsx` (Vitest, `@testing-library/react`, `userEvent`, the `vi.fn()` style of `Terminal.test.tsx`). A local helper `fake()` builds a `LoomTerminal` whose `send` and `key` are `vi.fn(() => true)`, `text` is `vi.fn(() => "")`, and whose other members are `vi.fn()`. `bytes(call)` is `Array.from(call[0])`. Every test renders `<PhoneInput terminal={…} live renderer="dom" />` unless it says otherwise. The tests, by name:

  - `sends each key's name through key() at once`: renders `live`; clicks, in turn, the buttons named `Ctrl-C`, `Ctrl-D`, `Tab`, `Esc`, `Up`, `Down`, `Left`, `Right`; asserts `key` was called with `ctrl-c`, `ctrl-d`, `tab`, `esc`, `up`, `down`, `left`, `right`, in that order and once each, and `send` not at all.
    Fails without: `sends each key's name through key() at once`: a button's `KeyName` swapped or missing, or its `onClick` (the list differs).
  - `disables every key until the terminal is live, and enables them when it is`: renders `live={false}`; asserts all eight buttons are disabled; re-renders `live={true}` and asserts none is.
    Fails without: `disables every key until the terminal is live, and enables them when it is`: `disabled={!live}` on the buttons.
  - `a press on the row keeps the focus on the command line`: focuses the `Command line` textbox; clicks `Ctrl-C` with `userEvent`; asserts `key` was called with `ctrl-c` and `document.activeElement` is still the textbox.
    Fails without: `a press on the row keeps the focus on the command line`: the row's `onMouseDown` `preventDefault` (user-event moves the focus to the button).
  - `Enter sends the text as UTF-8 and a carriage return, clears the line and keeps the focus`: renders `live`; types `echo é` and `{Enter}` into the textbox; asserts `send` was called once with the bytes of `new TextEncoder().encode("echo é\r")` (the `é` is two bytes), the textbox's value is `""` and it is the active element.
    Fails without: `Enter sends the text as UTF-8 and a carriage return, clears the line and keeps the focus`: the `"\r"` appended, the `TextEncoder`, the clearing, or a handler that blurs the field.
  - `Enter on an empty line sends a bare carriage return`: focuses the textbox and presses `{Enter}`; asserts `send` got exactly `[13]`.
    Fails without: `Enter on an empty line sends a bare carriage return`: a guard that skips an empty line.
  - `keeps the text when send answers false, and clears it when a later one answers true`: `send` answers `false` once and then `true`; types `ls`, presses `{Enter}`: the value is still `ls`; presses `{Enter}` again: `send` was called twice with the same bytes and the value is `""`.
    Fails without: `keeps the text when send answers false, and clears it when a later one answers true`: the clearing gated on the answer (an unconditional clear empties the line on the first Enter).
  - `Enter does nothing while the terminal is not live, and keeps the text`: renders `live={false}`; types `ls` and `{Enter}`; asserts `send` was not called and the value is `ls`; re-renders `live={true}` and presses `{Enter}`: `send` was called once and the value is `""`.
    Fails without: `Enter does nothing while the terminal is not live, and keeps the text`: the `live` guard in the Enter handler (the buttons' `disabled` does not cover Enter).
  - `the command line is a 16px field with every correction off`: asserts the textbox has `type="text"`, `autocorrect="off"`, `autocapitalize="none"`, `autocomplete="off"`, `spellcheck="false"`, `enterkeyhint="send"` and the class `text-[16px]`.
    Fails without: `the command line is a 16px field with every correction off`: any one of those props, or `Input`'s 16px class.

- [ ] **Step 2: Run it to see it fail.**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/terminal/PhoneInput.test.tsx
```

Expected: FAIL, `Failed to resolve import "./PhoneInput" from "src/panels/terminal/PhoneInput.test.tsx"`: the module does not exist yet.

- [ ] **Step 3: Implement.** Create `internal/web/ui/src/panels/terminal/PhoneInput.tsx` as the behaviour above says: a header comment naming spec §44 and §55, the `KEYS` table (name, label, accessible name) beside the component, and `PhoneInput` with the exact contract signature. Then `pnpm -C internal/web/ui exec prettier --write src/panels/terminal/PhoneInput.tsx src/panels/terminal/PhoneInput.test.tsx`.

- [ ] **Step 4: Run it to see it pass.**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/terminal/PhoneInput.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: 8 tests pass; typecheck and lint clean. `PhoneInput` is imported by nothing yet, so the bundle is unchanged and `dist:check` still passes.

- [ ] **Step 5: Commit.**

```bash
git add -N internal/web/ui/src/panels/terminal/PhoneInput.tsx internal/web/ui/src/panels/terminal/PhoneInput.test.tsx
git commit -m "feat(web): the phone's key row and command line for the terminal" -- internal/web/ui/src/panels/terminal/PhoneInput.tsx internal/web/ui/src/panels/terminal/PhoneInput.test.tsx
```

### Task 5.2: The copy sheet: Select, the Drawer, the text and the renderer

**Files:**
- Modify: `internal/web/ui/src/panels/terminal/PhoneInput.tsx`
- Test: `internal/web/ui/src/panels/terminal/PhoneInput.test.tsx`

**Defines (contract, Story 5):** the Select button with `data-slot="terminal-select"` and the sheet's text element with `data-slot="terminal-text"`, both in `PhoneInput.tsx`. **Uses:** `Drawer`, `DrawerContent`, `DrawerHeader`, `DrawerTitle` and `DrawerFooter` from `@/components/ui/drawer` (the app's Base UI Drawer; `app/ConfirmSheet.tsx` is the precedent: an `open` and an `onOpenChange`, a title, a footer of buttons); `LoomTerminal.text()` and `Renderer`.

**Behaviour.**
- A `Button` (`variant="secondary"`, `min-h-[44px] min-w-[44px] shrink-0`, `type="button"`, text `Select`, `data-slot="terminal-select"`) follows the scrolling part of the key row, inside the same flex row and under the same `onMouseDown` rule, so it stays visible when the keys scroll. It is not disabled by `live`.
- Pressing it reads `terminal.text()` **once** into state, with a count of reads that keys the `<pre>` (so a reopened sheet scrolls to its end again), and sets the open state. The text is never read on a render.
- `<Drawer open onOpenChange>` over a `DrawerContent` holding: a `DrawerHeader` with a `DrawerTitle` `Terminal text`; the text in a `<pre data-slot="terminal-text">` with the classes `max-h-[50dvh] overflow-auto select-text whitespace-pre-wrap break-words rounded-lg border border-border bg-background p-3 font-mono text-[12px] text-foreground` and the attribute `data-base-ui-swipe-ignore` (so a finger dragging a text selection is not taken for the Drawer's swipe to dismiss; Base UI documents the attribute); and a `DrawerFooter` holding `Renderer: WebGL` or `Renderer: DOM` in a `<p>` (`renderer` `webgl` or `dom`) and a `Button` `Done` that closes the sheet.
- The `<pre>` is scrolled to its end when it mounts, by a **stable** callback ref (`useCallback` with no dependencies, so a re-render does not scroll the reader back) that sets `el.scrollTop = el.scrollHeight`.
- The sheet keeps the last text while its exit transition runs (the open state is separate from the text, as `ConfirmSheet` keeps its last ask). Nothing writes to the clipboard: iOS's own long press, handles and Copy do the rest.

- [ ] **Step 1: Write the failing tests.** In `PhoneInput.test.tsx`, a describe `PhoneInput's copy sheet`, with `fake()` as in Task 5.1 (here `text` returns what a test sets). `sheetText()` is `document.querySelector('[data-slot="terminal-text"]')`. The tests, by name:

  - `Select opens a sheet holding the buffer's text, read once, in a selectable pre`: `text` answers `line one\nline two`; clicks `Select` (found by `data-slot="terminal-select"` and by its name); awaits `findByRole("dialog")`; asserts the text element is a `PRE` whose `textContent` is `line one\nline two`, it has the class `select-text`, and `text` was called once; then changes what `text` answers, re-renders with the same props, and asserts the text and the call count are unchanged.
    Fails without: `Select opens a sheet holding the buffer's text, read once, in a selectable pre`: the read in the click handler (a read on render calls `text` again and shows the new text), the `<pre>`, or `select-text`.
  - `scrolls the text to its end when the sheet opens`: spies `HTMLElement.prototype`'s `scrollHeight` getter to answer 500 (`vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(500)`, restored after); opens the sheet; asserts the text element's `scrollTop` is 500.
    Fails without: `scrolls the text to its end when the sheet opens`: the callback ref that sets `scrollTop`.
  - `names the renderer in the footer`: a `test.each` over `webgl` and `dom`; opens the sheet and asserts the footer has `Renderer: WebGL` and `Renderer: DOM` respectively.
    Fails without: `names the renderer in the footer`: the renderer's mapping to its label (either row fails).
  - `Done closes the sheet`: opens it, clicks `Done`, and waits until `queryByRole("dialog")` is null.
    Fails without: `Done closes the sheet`: the Done button's `onClick`.
  - `reads the buffer again each time it opens, and scrolls to its end again`: opens, presses `Done` and waits for the dialog to go; changes what `text` answers; opens again; asserts the new text is shown, `text` was called twice, and `scrollTop` is again 500 (with the `scrollHeight` spy of the test above).
    Fails without: `reads the buffer again each time it opens, and scrolls to its end again`: the read being made once for the component's life, or the `<pre>` not keyed by the read count (the second open keeps the old node and its scroll).
  - `writes nothing to the clipboard`: defines `navigator.clipboard` as `{ writeText: vi.fn() }` (`Object.defineProperty`, configurable, removed after); opens the sheet, presses `Done`; asserts `writeText` was never called.
    Fails without: `writes nothing to the clipboard`: any `writeText` call added to the sheet.
  - `Select works while the terminal is not live`: renders `live={false}`; asserts the Select button is enabled; clicks it; asserts the dialog opens with the text.
    Fails without: `Select works while the terminal is not live`: a `disabled={!live}` on Select.

- [ ] **Step 2: Run it to see it fail.**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/terminal/PhoneInput.test.tsx
```

Expected: the eight new tests (the renderer's `test.each` is two) FAIL (`Unable to find an element` for the `Select` button); the eight of Task 5.1 still pass.

- [ ] **Step 3: Implement.** In `PhoneInput.tsx`, add the Select button, its state (`open`, `text`, a read counter), the Drawer and the stable scroll ref as the behaviour above says, and import the Drawer pieces. Then `pnpm -C internal/web/ui exec prettier --write src/panels/terminal/PhoneInput.tsx src/panels/terminal/PhoneInput.test.tsx`.

- [ ] **Step 4: Run it to see it pass.**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/terminal/PhoneInput.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: 16 tests pass; typecheck and lint clean.

- [ ] **Step 5: Commit.**

```bash
git commit -m "feat(web): the terminal's copy sheet on the phone, with the renderer named" -- internal/web/ui/src/panels/terminal/PhoneInput.tsx internal/web/ui/src/panels/terminal/PhoneInput.test.tsx
```

### Task 5.3: `Terminal` draws `PhoneInput` on the phone, and the rebuilt bundle

**Files:**
- Modify: `internal/web/ui/src/panels/terminal/Terminal.tsx` (Story 4's file: the `@/terminal/socket` import line, and the end of `Terminal`'s JSX)
- Test: `internal/web/ui/src/panels/terminal/Terminal.test.tsx` (Story 4's file: a new describe at the end, and two assertions)
- Modify: `internal/web/dist/**` (rebuilt)

**Uses:** Story 4's `Terminal` (its `phone`, `terminal`, `state` and `phase` values, and its root `data-slot="terminal"`), `fakeTerminal()`, `mount()`, `frameEl()`, `LIVE` and `WAITING` in `Terminal.test.tsx`; Task 5.1's and 5.2's `PhoneInput`.

**Behaviour.** In `Terminal.tsx`, after the line `import { CLOSE_EXITED, describeClose } from "@/terminal/socket";` add `import { PhoneInput } from "./PhoneInput";`. In `Terminal`'s returned JSX, the root `div` (`data-slot="terminal"`) holds one child today, `<div className="relative flex min-h-0 flex-1 flex-col">` with the `<iframe>` and `<Notice … />`. Add a second child after that div's closing tag, inside the root:

```tsx
      {phone && terminal !== null && state !== null ? (
        <PhoneInput terminal={terminal} live={phase === "live"} renderer={state.renderer} />
      ) : null}
```

`phone`, `terminal`, `state` and `phase` are the values `Terminal` already holds; the guard is the contract's "the layout is the phone's and the frame's interface is there", and `state` is also required so the renderer is known (the frame gives a state at once, so the input appears with the first state). The frame's `Notice` stays inside the frame's own div, so it never covers the input.

**Two assertions of Story 4's tests change**, because the phone's key row is now a set of buttons on every phone-layout test (the viewport defaults to 390px): in the test `shows a close's reason, and no button for any ending but the shell's exit`, both lines `expect(screen.queryByRole("button")).toBeNull();` become `expect(screen.queryByRole("button", { name: "Start a new shell" })).toBeNull();`. Every other query of Story 4's tests is by `status`, text or title and is unaffected (`PhoneInput` holds no `role="status"`).

- [ ] **Step 1: Write the failing tests.** In `Terminal.test.tsx`, make the two assertion changes above, then add a describe `Terminal's phone input` after the last describe, using `fakeTerminal()`, `mount()`, `LIVE`, `WAITING`, `frameEl()`, `rootEl()` and `setViewportWidth` as the file already does. The tests, by name:

  - `draws the phone's input under the frame once the frame has given a state`: on the phone (the default width), `mount()`; asserts the `Command line` textbox is inside `rootEl()` and follows `frameEl()` in document order (`frameEl().compareDocumentPosition(textbox) & Node.DOCUMENT_POSITION_FOLLOWING`).
    Fails without: `draws the phone's input under the frame once the frame has given a state`: the `PhoneInput` element in `Terminal`, or its place after the frame's div.
  - `draws none on the desktop`: `setViewportWidth(1280)`, then `mount()`; asserts there is no `Command line` textbox and no `Ctrl-C` button.
    Fails without: `draws none on the desktop`: the `phone &&` term of the guard.
  - `draws none before the frame has an interface`: `render(<Terminal …/>)` without the frame's load event; asserts no `Command line` textbox.
    Fails without: `draws none before the frame has an interface`: the `terminal !== null` term of the guard (without it the input would be handed a null interface).
  - `gives the input live from the phase`: `mount()` (phase `connecting`): the `Tab` button is disabled; `fake.emit(LIVE)`: it is enabled, and clicking it calls `fake.key` with `tab`; `fake.emit(WAITING)`: it is disabled again.
    Fails without: `gives the input live from the phase`: `live={phase === "live"}` (a constant true leaves it enabled while connecting; a constant false never enables it).
  - `gives the sheet the renderer the frame reports, and follows a change of it`: `mount()`, `fake.emit({ phase: "live", renderer: "webgl" })`; clicks `Select`; asserts the sheet's footer shows `Renderer: WebGL`; `fake.emit({ phase: "live", renderer: "dom" })`; asserts it now shows `Renderer: DOM`.
    Fails without: `gives the sheet the renderer the frame reports, and follows a change of it`: `renderer={state.renderer}` (a constant renderer fails one of the two assertions).
  - `types into the shell through the interface`: `mount()`, `fake.emit(LIVE)`; types `ls` and `{Enter}` into the `Command line` textbox; asserts `fake.send` was called once with the bytes of `ls\r`.
    Fails without: `types into the shell through the interface`: passing the panel's own `terminal` (the interface `frameTerminal` gave) to `PhoneInput`.

- [ ] **Step 2: Run it to see it fail.**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/terminal/Terminal.test.tsx
```

Expected: the four tests that need the input FAIL (`Unable to find an accessible element with the role "textbox" and name "Command line"`); `draws none on the desktop` and `draws none before the frame has an interface` pass already (they pin the guard from the side the first test does not, and go red only under their `Fails without` mutations); the two edited assertions pass.

- [ ] **Step 3: Implement.** Make the two edits to `Terminal.tsx` as the behaviour above says, then `pnpm -C internal/web/ui exec prettier --write src/panels/terminal/Terminal.tsx src/panels/terminal/Terminal.test.tsx`.

- [ ] **Step 4: Run it to see it pass.**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/terminal/
```

Expected: all of `Terminal.test.tsx` (Story 4's tests and the six new ones) and `PhoneInput.test.tsx` pass.

- [ ] **Step 5: Rebuild the bundle.** `pnpm -C internal/web/ui run build` rewrites `internal/web/dist/`: `git status --short internal/web/dist` lists a changed `assets/index-<hash>.js` (the entry now holds `PhoneInput` and the Drawer's use) and `index.html` (its script name), and nothing of xterm.js outside the terminal page's script.

- [ ] **Step 6: Run the gates for the task.**

```bash
bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'
```

Expected: all pass. `TestDist_FirstLoadIsWithinBudget` is the gate that shows whether the entry still fits `firstLoadBudget`: if it fails, report the sizes it prints and change no budget (the design session decides). `TestDist_XtermLoadsOnlyInTheTerminalPage` still passes.

- [ ] **Step 7: Commit.**

```bash
git add internal/web/dist
git commit -m "feat(web): the terminal panel draws the phone's input under the frame" -- internal/web/ui/src/panels/terminal/Terminal.tsx internal/web/ui/src/panels/terminal/Terminal.test.tsx internal/web/dist
```

### Task 5.4: `USAGE.md`: the phone's keys and the copy sheet

**Files:**
- Modify: `USAGE.md` (the paragraph Story 4 added under "The terminal.")

**Behaviour.** Story 4 wrote an item that begins `- **The terminal.** The **terminal** tab is the session's worktree shell` and ends with the line `  **The shell exited**, a **Start a new shell** button.`. This task appends the phone's paragraph to that item, in the same wrapped style (two-space indent, lines of about 72 columns). It says: on the phone the terminal takes no keyboard of its own; a row of keys sends **Ctrl-C**, **Ctrl-D**, **Tab**, **Esc** and the four arrows, and a command line under it types into the shell, **Enter** sending the line and a return (a bare Enter sends just the return) and keeping the keyboard up; the keys and Enter wait while the terminal is not connected, the line keeping its text and nothing being queued; a finger dragged on the screen scrolls its history and a flick carries on; **Select** opens a sheet with the whole screen and scrollback as plain text, where a long press selects and iOS's own Copy copies (loom copies nothing itself), and the sheet's footer says whether the terminal is drawn by WebGL or the DOM; and using the shell from the phone sizes the shared shell to the phone's width (about 48 columns) until it is used somewhere else.

- [ ] **Step 1: Write the docs.** In `USAGE.md`, replace the line

```
  **The shell exited**, a **Start a new shell** button.
```

with that line followed by the phone's paragraph as the behaviour above describes it (it begins `  On the phone `). The replaced line is unique in the file (`grep -c 'The shell exited\*\*, a \*\*Start a new shell\*\* button' USAGE.md` prints 1 at this point).

- [ ] **Step 2: Run the gates for the story.**

```bash
bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'
```

Expected: all pass (`USAGE.md` is read by no test here; the gates show Task 5.3's bundle is still fresh).

- [ ] **Step 3: Commit.**

```bash
git commit -m "docs(web): the terminal's phone keys, command line and copy sheet" -- USAGE.md
```

## Story 6: webshot: the canned page, the canned route and the screens

- **Developer:** intermediate-dev
- **Files:** `tools/webshot/canned.go`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `tools/webshot/fixture/terminal.ansi`, `tools/webshot/fixture/snapshot.json`, `tools/CLAUDE.md`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/public/sw.js`, `internal/web/ui/vite.config.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/ui/src/desktop/Inspector.test.tsx`, `internal/web/ui/src/desktop/Palette.test.tsx`, `internal/web/ui/src/desktop/palette.test.ts`, `internal/web/dist/**`, `internal/web/README.md`, `docs/claude/web-layer.md`
- **Interfaces:** exposes nothing; uses Story 4's path `#s/1/terminal` and `data-terminal-state`, and Story 5's `terminal-select` and `terminal-text` slots
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -screens terminal -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -screens terminal -out "$d"'`
- **Review:** light
- **Form:** full (a WebSocket handler that bounds its wait for the first frame and holds a hijacked connection open, and the screens' click and skip logic)

webshot's canned loom would refuse the terminal's frame (it serves every file under the app's policy, whose `frame-ancestors 'none'` forbids framing) and would fail the run on the terminal's WebSocket route (an API route it cannot answer). This story teaches it both, then draws the screens (spec §46–§48, §62, §63). Task 6.1 serves `/terminal.html` under `terminalCSP`, a copy that `internal/web/webshot_fixture_test.go` holds to the layer's beside the other two. Task 6.2 adds `fixture/terminal.ansi` and the route: it upgrades with `github.com/coder/websocket`, waits for the app's first frame (the size), sends the file in a binary frame and then `{"state":"live"}`, holds the connection open, and counts no miss; the fixture test's count of files moves to 12, and the dev server's proxy of `/api/v1` gains `ws: true` (`internal/web/ui/vite.config.ts`), since Vite forwards a WebSocket upgrade only then and `-serve` answers the route for it. Task 6.3 gives `screenMode` its `phoneOnly`, `nextOnly`, `ready` and `click` and adds the screens `terminal` (every device: waits for the framed terminal's own text to hold the canned run's last result, since `live` arrives before xterm.js has parsed and painted the bytes, and for `[data-terminal-state="live"]`) and `terminal-select` (the phone only: waits for `[data-terminal-state="live"]`, then for the framed terminal's own text to hold the canned run's last result, clicks `[data-slot="terminal-select"]` once, and waits for a `[data-slot="terminal-text"]` that holds text, since the sheet reads the terminal's text once as it opens); both are `nextOnly`, since the classic app has no panel. Task 6.4 turns the fixture snapshot's `terminal` on and fixes the one Vitest expectation it moves. Task 6.5 corrects the stale comment in `public/sw.js`, brings `tools/CLAUDE.md` (the stdlib-only exception for the one import, and the two lines that name the phone-only screen), `internal/web/README.md` and `docs/claude/web-layer.md` (its screens, and the terminal's device checks on its iPhone checklist) along, and rebuilds the bundle. Task 6.6 runs the screens for real and, only if the terminal comes out blank, starts the canned run's Chromium with WebGL off. None of this code holds a landmine marker. The WebSocket route's wait for the size (a timer that closes the connection under a read) and the screens' waits with deadlines are listed in the risk file.

### Task 6.1: serve `/terminal.html` under `terminalCSP`

**Files:**
- Modify: `tools/webshot/canned.go` (the `file` method, and a constant beside `contentSecurityPolicy`)
- Modify: `tools/webshot/webshot_test.go` (after `TestCanned_FilesCarryTheLayersCSP`)
- Modify: `internal/web/webshot_fixture_test.go:104-110`

**Defines (private to this story):** `terminalPage` and `terminalCSP` in `tools/webshot/canned.go`. **Uses:** the layer's `terminalCSP` (`internal/web/static.go`), which `webshot_fixture_test.go` holds the copy equal to.

- [ ] **Step 1: Write the failing tests.** In `tools/webshot/webshot_test.go`, after the whole of `TestCanned_FilesCarryTheLayersCSP` (it ends with the `assert.Equal(t, mockupCSP, ...)` line and its closing brace), add:

```go
func TestCanned_OnlyTheTerminalPageIsServedUnderTerminalCSP(t *testing.T) {
	static := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(static, "assets"), 0o755))
	for name, body := range map[string]string{
		"index.html":             `<meta name="loom-build" content="__LOOM_BUILD__">`,
		"terminal.html":          "<!doctype html><title>terminal</title>",
		"assets/terminal-abc.js": "export {};",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(static, filepath.FromSlash(name)), []byte(body), 0o644))
	}
	c, err := newCannedLoom(static)
	require.NoError(t, err)
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	for path, want := range map[string]string{
		"/terminal.html":          terminalCSP,
		"/":                       contentSecurityPolicy,
		"/index.html":             contentSecurityPolicy,
		"/assets/terminal-abc.js": contentSecurityPolicy,
	} {
		res, err := http.Get(srv.URL + path)
		require.NoError(t, err, path)
		res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode, path)
		assert.Equal(t, want, res.Header.Get("Content-Security-Policy"), path)
	}
	assert.Contains(t, terminalCSP, "frame-ancestors 'self'", "the app's panel may frame the page")
	assert.Contains(t, contentSecurityPolicy, "frame-ancestors 'none'", "and no other file may be framed")
}
```

In `internal/web/webshot_fixture_test.go`, in `TestWebshotFixture_IsTheJSONTheLayerServes`, replace

```go
	require.Contains(t, string(canned), `"`+contentSecurityPolicy+`"`, "webshot serves the app's files under the policy the layer serves them under")
```

with

```go
	require.Contains(t, string(canned), `"`+contentSecurityPolicy+`"`, "webshot serves the app's files under the policy the layer serves them under")
	require.Contains(t, string(canned), `"`+terminalCSP+`"`, "webshot serves the terminal's page under the policy the layer serves it under")
```

Fails without: `TestCanned_OnlyTheTerminalPageIsServedUnderTerminalCSP`: the `/terminal.html` branch in `file` (the page then carries `contentSecurityPolicy`, so the first row differs), and the branch applied to every file (`/index.html` and the script then carry `terminalCSP`)
Fails without: `TestWebshotFixture_IsTheJSONTheLayerServes`: the `terminalCSP` constant's text in `canned.go` equal to the layer's (a copy that drifts, or a missing one, fails the `Contains`)

- [ ] **Step 2: Run them to see them fail.**

```bash
CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ -run 'TestCanned_OnlyTheTerminalPage|TestWebshotFixture_IsTheJSONTheLayerServes'
```

Expected: FAIL. `tools/webshot` does not compile (`undefined: terminalCSP`), and `TestWebshotFixture_IsTheJSONTheLayerServes` fails its new `Contains` (`canned.go` has no such policy).

- [ ] **Step 3: Implement.** In `tools/webshot/canned.go`, after the `contentSecurityPolicy` constant (the line `const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"`), add:

```go

// terminalPage is the one file the canned loom serves under terminalCSP: the terminal's document
// (internal/web/ui/terminal.html), which the app frames.
const terminalPage = "/terminal.html"

// terminalCSP is the policy the web layer serves terminalPage under (internal/web/static.go is the
// source, and TestWebshotFixture_IsTheJSONTheLayerServes holds this copy to it): the app's own
// with inline styles allowed, since xterm.js appends <style> elements at run time, and
// frame-ancestors 'self', so the app's panel can frame it. The page's own script and styles stay
// under contentSecurityPolicy, as the layer serves them.
const terminalCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"
```

In the `file` method, replace

```go
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
```

with

```go
	policy := contentSecurityPolicy
	if clean == terminalPage {
		policy = terminalCSP
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", policy)
```

Then run `gofmt -l tools/webshot internal/web` (it prints nothing when the files are formatted).

- [ ] **Step 4: Run them to see them pass.**

```bash
CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ -run 'TestCanned_|TestWebshotFixture_'
```

Expected: PASS (`ok  	github.com/aidan-bailey/loom/tools/webshot` and `ok  	github.com/aidan-bailey/loom/internal/web`).

- [ ] **Step 5: Commit.**

```bash
git commit -m "feat(webshot): serve the terminal's page under terminalCSP" -- tools/webshot/canned.go tools/webshot/webshot_test.go internal/web/webshot_fixture_test.go
```

### Task 6.2: the canned terminal route and `fixture/terminal.ansi`

**Files:**
- Create: `tools/webshot/fixture/terminal.ansi`
- Modify: `tools/webshot/canned.go` (the imports, the `//go:embed` line, the `cannedLoom` struct, `apiHandler`, `ServeHTTP`, and a new route and method after `stream`)
- Modify: `tools/webshot/webshot_test.go` (the imports, and tests after `TestCanned_TheStreamStartsWithTheSnapshot`)
- Modify: `internal/web/webshot_fixture_test.go:104-110`
- Modify: `internal/web/ui/vite.config.ts:123`

**Defines (private to this story):** `terminalRoute`, `terminalSizeWait`, `cannedLoom.sizeWait` and `cannedLoom.terminal` in `canned.go`. **Uses:** `github.com/coder/websocket` v1.8.15 (`Accept`, `Dial`, `Conn.Read`, `Conn.Write`, `Conn.Close`, `Conn.CloseNow`, `CloseStatus`, `StatusPolicyViolation`; vendored, and already listed in `vendor/modules.txt` through `internal/web`, so no vendor change); the layer's frames (`internal/web/terminal_conn.go`, `internal/web/ui/src/terminal/socket.ts`): the app's first frame is the text `{"resize":{"cols":N,"rows":N}}`, and the layer answers with binary terminal bytes and then the text `{"state":"live"}`.

- [ ] **Step 1: Write the failing tests.** In `tools/webshot/webshot_test.go`, replace the import block's tail

```go
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
```

with

```go
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
```

After the whole of `TestCanned_TheStreamStartsWithTheSnapshot`, add:

```go
// terminalFrame is one message the canned terminal route sent, or the error that ended the read.
type terminalFrame struct {
	typ  websocket.MessageType
	data []byte
	err  error
}

func terminalURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/sessions/1/terminal"
}

// readFrames reads conn in the background, so a test can tell that nothing has arrived yet.
func readFrames(ctx context.Context, conn *websocket.Conn) <-chan terminalFrame {
	got := make(chan terminalFrame, 8)
	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			got <- terminalFrame{typ, data, err}
			if err != nil {
				return
			}
		}
	}()
	return got
}

func nextFrame(t *testing.T, got <-chan terminalFrame) terminalFrame {
	t.Helper()
	select {
	case f := <-got:
		require.NoError(t, f.err)
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no frame from the canned terminal route in 5s")
		return terminalFrame{}
	}
}

func TestCanned_TheTerminalRouteWaitsForTheSizeThenSendsTheScreenAndLive(t *testing.T) {
	c, srv := cannedForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, terminalURL(srv), nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	got := readFrames(ctx, conn)

	select {
	case f := <-got:
		t.Fatalf("the route sent a frame before the app's size: %v %q %v", f.typ, f.data, f.err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"resize":{"cols":48,"rows":20}}`)))

	screen := nextFrame(t, got)
	want, err := fixtureFiles.ReadFile("fixture/terminal.ansi")
	require.NoError(t, err)
	assert.Equal(t, websocket.MessageBinary, screen.typ)
	assert.Equal(t, want, screen.data)
	live := nextFrame(t, got)
	assert.Equal(t, websocket.MessageText, live.typ)
	assert.JSONEq(t, `{"state":"live"}`, string(live.data))

	select {
	case f := <-got:
		t.Fatalf("the route ended or sent more after live: %v %q %v", f.typ, f.data, f.err)
	case <-time.After(300 * time.Millisecond):
	}
	assert.Empty(t, c.takeMisses(), "the terminal route is not a miss")
}

func TestCanned_TheTerminalRouteLetsGoOfAConnectionThatSendsNoSize(t *testing.T) {
	c, srv := cannedForTest(t)
	c.sizeWait = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, terminalURL(srv), nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	_, _, err = conn.Read(ctx)
	require.Error(t, err)
	assert.Equal(t, websocket.StatusPolicyViolation, websocket.CloseStatus(err), "a close frame with a code, not a dropped connection")
}

func TestCanned_TheTerminalRouteRefusesAFirstFrameThatIsNotText(t *testing.T) {
	_, srv := cannedForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, terminalURL(srv), nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	require.NoError(t, conn.Write(ctx, websocket.MessageBinary, []byte("ls\r")))
	_, _, err = conn.Read(ctx)
	require.Error(t, err)
	assert.Equal(t, websocket.StatusPolicyViolation, websocket.CloseStatus(err))
}

func TestCanned_AnUnpairedLoomRefusesTheTerminalRouteAs401WithNoMiss(t *testing.T) {
	c, srv := cannedForTest(t)
	c.unpaired = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, terminalURL(srv), nil)
	require.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Empty(t, c.takeMisses())
}

func TestAPIHandler_AnswersTheTerminalRouteToo(t *testing.T) {
	srv := httptest.NewServer(newCannedAPI().apiHandler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, terminalURL(srv), nil)
	require.NoError(t, err, "-serve answers the terminal route for the dev server's frame")
	conn.CloseNow()
}

func TestFixture_TheTerminalScreenIsTheBytesOfASessionOnesShell(t *testing.T) {
	raw, err := fixtureFiles.ReadFile("fixture/terminal.ansi")
	require.NoError(t, err)
	text := string(raw)
	assert.Contains(t, text, "fix-pane-cursor", "session 1's title, the shell's directory")
	assert.Contains(t, text, "\x1b[", "colours, so the screen shows the renderer's palette")
	assert.Equal(t, strings.Count(text, "\n"), strings.Count(text, "\r\n"), "every line ends CR LF, or the next starts where the last ended")
	assert.False(t, strings.HasSuffix(text, "\n"), "it ends at a prompt, as a shell waits")
	escapes := regexp.MustCompile("\x1b\\[[0-9;]*m")
	for _, line := range strings.Split(text, "\r\n") {
		assert.LessOrEqual(t, utf8.RuneCountInString(escapes.ReplaceAllString(line, "")), 48, "%q wraps on the phone's 48 columns", line)
	}
}
```

In `internal/web/webshot_fixture_test.go`, replace

```go
	require.Len(t, entries, 11, "a new fixture is checked here too")
```

with

```go
	require.Len(t, entries, 12, "a new fixture is checked here too")
```

Fails without: `TestCanned_TheTerminalRouteWaitsForTheSizeThenSendsTheScreenAndLive`: the `conn.Read` for the size before the first write (the screen then arrives before the app's size, so the first `select` takes the frame and fails), the binary frame of `fixture/terminal.ansi`, the `{"state":"live"}` text frame after it, and the final read loop (a handler that returns after live ends the connection and the last `select` takes the read's error); the route counting a miss
Fails without: `TestCanned_TheTerminalRouteLetsGoOfAConnectionThatSendsNoSize`: the timer in `terminal` (the handler then waits on its read for ever, and the client's own 5 s context ends the read with no close status)
Fails without: `TestCanned_TheTerminalRouteRefusesAFirstFrameThatIsNotText`: the `typ != websocket.MessageText` check (the screen is then sent in answer to bytes, and the read returns a frame, not a close)
Fails without: `TestCanned_AnUnpairedLoomRefusesTheTerminalRouteAs401WithNoMiss`: the route's case placed before the unpaired case in `ServeHTTP` (the upgrade then succeeds, and `Dial` returns no error)
Fails without: `TestAPIHandler_AnswersTheTerminalRouteToo`: the route's case in `apiHandler` (it falls to `c.api`, which answers 404 and records a miss, and `Dial` fails)
Fails without: `TestFixture_TheTerminalScreenIsTheBytesOfASessionOnesShell`: a line over 48 columns, a bare `\n`, a trailing newline, no escape, or a screen that never names the session's title
Fails without: `TestWebshotFixture_IsTheJSONTheLayerServes`: `fixture/terminal.ansi` missing (11 entries, not 12)

- [ ] **Step 2: Run them to see them fail.**

```bash
CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ -run 'TestCanned_|TestAPIHandler_|TestFixture_TheTerminal|TestWebshotFixture_IsTheJSONTheLayerServes'
```

Expected: FAIL. `tools/webshot` fails to compile (`c.sizeWait undefined (type *cannedLoom has no field or method sizeWait)`), and `TestWebshotFixture_IsTheJSONTheLayerServes` fails `Len` (`expected: 12`, `actual: 11`).

- [ ] **Step 3: Implement.** First the fixture, the canned screen of session 1's shell (its title is `fix-pane-cursor`): a `git status`, then a `go test`, then a prompt, every line ending CR LF, none over 48 columns. From the repo root:

```bash
printf '\033[1;32mfresco\033[0m \033[34mfix-pane-cursor\033[0m $ git status -sb\r\n## aidanb/fix-pane-cursor [ahead 3]\r\n\033[31m M\033[0m ui/pane.go\r\n\033[31m M\033[0m ui/pane_test.go\r\n\033[1;32mfresco\033[0m \033[34mfix-pane-cursor\033[0m $ go test ./ui/\r\n\033[32mok\033[0m  github.com/aidan-bailey/loom/ui  0.412s\r\n\033[1;32mfresco\033[0m \033[34mfix-pane-cursor\033[0m $ ' > tools/webshot/fixture/terminal.ansi
```

In `tools/webshot/canned.go`, replace

```go
import (
	"bytes"
	"embed"
```

with

```go
import (
	"bytes"
	"context"
	"embed"
```

and replace

```go
	"sync"
	"time"
)
```

with

```go
	"sync"
	"time"

	"github.com/coder/websocket"
)
```

Replace the embed line and the comment's last line above it

```go
// run; a bare "now" is left as it is.
//
//go:embed fixture/*.json fixture/*.html
```

with

```go
// run; a bare "now" is left as it is. terminal.ansi is the terminal route's bytes, sent as they are.
//
//go:embed fixture/*.json fixture/*.html fixture/*.ansi
```

In the `cannedLoom` struct, replace

```go
	unpaired bool

	mu     sync.Mutex
```

with

```go
	unpaired bool

	// sizeWait, when set, is how long the terminal route waits for the app's size in place of
	// terminalSizeWait (a test shortens it).
	sizeWait time.Duration

	mu     sync.Mutex
```

In `apiHandler`, replace (the three-tab-indented `case`s inside the `HandlerFunc`)

```go
		case p == "/api/v1/stream" && r.Method == http.MethodGet:
			c.stream(w, r)
		case strings.HasPrefix(p, "/api/"):
			c.api(w, r)
		default:
			http.NotFound(w, r)
```

with

```go
		case p == "/api/v1/stream" && r.Method == http.MethodGet:
			c.stream(w, r)
		case terminalRoute.MatchString(p) && r.Method == http.MethodGet:
			c.terminal(w, r)
		case strings.HasPrefix(p, "/api/"):
			c.api(w, r)
		default:
			http.NotFound(w, r)
```

In `ServeHTTP`, replace (the two-tab-indented ones)

```go
	case p == "/api/v1/stream" && r.Method == http.MethodGet:
		c.stream(w, r)
	case strings.HasPrefix(p, "/api/"):
		c.api(w, r)
	default:
		c.file(w, p)
```

with

```go
	case p == "/api/v1/stream" && r.Method == http.MethodGet:
		c.stream(w, r)
	case terminalRoute.MatchString(p) && r.Method == http.MethodGet:
		c.terminal(w, r)
	case strings.HasPrefix(p, "/api/"):
		c.api(w, r)
	default:
		c.file(w, p)
```

Both keep the unpaired case first, so an unpaired loom refuses the upgrade as every `/api/` request. After the whole of the `stream` method (it ends with the `for { select { ... } }` loop and its closing brace, before the comment `// fixture reads a fixture, fills its relative times in`), add:

```go
// terminalRoute is the web layer's terminal route for one session
// (GET /api/v1/sessions/{id}/terminal, internal/web/terminal.go).
var terminalRoute = regexp.MustCompile(`^/api/v1/sessions/\d+/terminal$`)

// terminalSizeWait is how long the canned terminal route waits for the app's first frame, its
// size, before it lets the connection go.
const terminalSizeWait = 10 * time.Second

// terminal answers the terminal route as the web layer does once its checks have passed: it
// upgrades, waits for the app's first text frame (its size), sends fixture/terminal.ansi in one
// binary frame and then {"state":"live"}, and holds the connection open until the app closes it.
// The screen is the same whatever size the app asks for, and the request is not a miss.
func (c *cannedLoom) terminal(w http.ResponseWriter, r *http.Request) {
	screen, err := fixtureFiles.ReadFile("fixture/terminal.ansi")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has answered the request
	}
	defer conn.CloseNow()
	ctx := r.Context()

	// A context on the read would drop the connection with no close frame, so the wait is a
	// timer that closes it, with a code, under the read.
	wait := c.sizeWait
	if wait == 0 {
		wait = terminalSizeWait
	}
	refuse := func() { _ = conn.Close(websocket.StatusPolicyViolation, "the app's first frame is its size") }
	timer := time.AfterFunc(wait, refuse)
	typ, _, err := conn.Read(ctx)
	timer.Stop()
	if err != nil {
		return
	}
	if typ != websocket.MessageText {
		refuse()
		return
	}
	if err := conn.Write(ctx, websocket.MessageBinary, screen); err != nil {
		return
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"state":"live"}`)); err != nil {
		return
	}
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}
```

The route's allowed origin is the default: `websocket.Accept` refuses a request whose `Origin` host differs from its `Host`, and the page the browser frames is served from the same canned loom, so the two match.

The dev server forwards a WebSocket upgrade only for a proxy entry that sets `ws`, so `-serve` (which `TestAPIHandler_AnswersTheTerminalRouteToo` says answers the terminal route for the dev server's frame) is reached by the frame only once the entry has it. In `internal/web/ui/vite.config.ts`, replace

```ts
    proxy: { "/api/v1": "http://127.0.0.1:5670" },
```

with

```ts
    proxy: { "/api/v1": { target: "http://127.0.0.1:5670", ws: true } },
```

Then run `gofmt -l tools/webshot internal/web` (nothing printed).

- [ ] **Step 4: Run them to see them pass.**

```bash
CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/
```

Expected: PASS (`ok  	github.com/aidan-bailey/loom/tools/webshot`, `ok  	github.com/aidan-bailey/loom/internal/web`). Then run the route's tests under the race detector, since its timer closes the connection from its own goroutine under a read, which only a `-race` run overlaps:

```bash
CC=clang CGO_ENABLED=1 go test -race ./tools/webshot/ -run 'TestCanned_|TestAPIHandler_'
```

Expected: PASS (drop `CC=clang` where gcc exists).

- [ ] **Step 5: Commit.**

```bash
git add -N tools/webshot/fixture/terminal.ansi
git commit -m "feat(webshot): a canned answer for the terminal's WebSocket route" -- tools/webshot/canned.go tools/webshot/webshot_test.go tools/webshot/fixture/terminal.ansi internal/web/webshot_fixture_test.go internal/web/ui/vite.config.ts
```

### Task 6.3: `screenMode`'s `phoneOnly`, `nextOnly`, `ready` and `click`, and the screens `terminal` and `terminal-select`

**Files:**
- Modify: `tools/webshot/main.go` (the `screens` comment and list, `screenMode` and `screenModes`, `forDevice`, `forApp`, `skipReason`, `shoot`, `waitFor`, and new code after `pressScreen`)
- Modify: `tools/webshot/webshot_test.go` (the tests the new modes move, and new ones)

**Defines (contract, Story 6):** the screens `terminal` and `terminal-select`, both at the hash `#s/1/terminal`; `screenMode`'s `phoneOnly bool`, `nextOnly bool`, `ready string` and `click string`. **Defines (private to this story):** `screenMode.shows` (a string: text the framed terminal must hold before the click, or before the shot of a screen with none), `screenMode.waits`, `(*page).waitExpr`, `(*page).waitShows`, `(*page).click`, `(*page).drawScreen` and `drawnWait`, in `main.go`. **Uses:** Story 4's hash `#s/1/terminal` (the classic-shaped address `routes.tsx` rewrites to the panel's `#/s/1/terminal`), its root's `data-terminal-state` and its `<iframe src="/terminal.html#<id>">`; Story 3's `window.loomTerminal` and its `text()` (the frame's whole buffer as plain text, `""` until the terminal has started), read from the framing page through the frame's same-origin window; Story 5's `data-slot="terminal-select"` (the Select key, enabled whatever the phase) and `data-slot="terminal-text"` (the sheet's text, read once as the sheet opens); the existing `waitFor`, `query`, `pressScreen`, `recordingBrowser` and `evalValue`.

- [ ] **Step 1: Write the failing tests.** In `tools/webshot/webshot_test.go`, make these edits to the tests the new modes move.

In `TestScreens_PairIsShotUnpairedAndAddToHomeAsABrowserTab`, replace

```go
		if sc.name != "pair" && sc.name != "add-to-home" && sc.name != "palette" {
```

with

```go
		if sc.name != "pair" && sc.name != "add-to-home" && sc.name != "palette" && sc.name != "terminal" && sc.name != "terminal-select" {
```

In `TestForDevice_DropsAddToHomeOnADesktopAndThePaletteOnAPhone`, replace

```go
		if sc.name != "add-to-home" {
			want = append(want, sc)
```

with

```go
		if sc.name != "add-to-home" && sc.name != "terminal-select" {
			want = append(want, sc)
```

and replace

```go
		assert.Len(t, got, len(screens)-1, name)
		assert.Contains(t, got, screen{"pair", "#pair"}, name)
```

with

```go
		assert.Len(t, got, len(screens)-2, name)
		assert.Contains(t, got, screen{"pair", "#pair"}, name)
		assert.Contains(t, got, screen{"terminal", "#s/1/terminal"}, name)
		assert.NotContains(t, got, screen{"terminal-select", "#s/1/terminal"}, name)
```

In `TestForApp_DropsDesktopOnlyScreensUnlessTheAppIsNext`, replace

```go
		if sc.name != "palette" {
			without = append(without, sc)
```

with

```go
		if sc.name != "palette" && sc.name != "terminal" && sc.name != "terminal-select" {
			without = append(without, sc)
```

In `TestRun_SkipLinesNameTheirReason`, replace

```go
		{"a desktop-only screen under the classic app", []string{"-device", "desktop", "-screens", "palette,fleet"}, "palette\tskipped: next app only"},
```

with

```go
		{"a desktop-only screen under the classic app", []string{"-device", "desktop", "-screens", "palette,fleet"}, "palette\tskipped: next app only"},
		{"a phone-only screen on a desktop", []string{"-device", "desktop", "-app", "next", "-screens", "terminal-select,fleet"}, "terminal-select\tskipped: phone only"},
		{"a next-only screen under the classic app", []string{"-device", "iphone15", "-screens", "terminal,fleet"}, "terminal\tskipped: next app only"},
```

In `TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser`, replace

```go
			assert.True(t, os.IsNotExist(err), "add-to-home is phone only")
```

with

```go
			assert.True(t, os.IsNotExist(err), "add-to-home is phone only")
			_, err = os.Stat(filepath.Join(out, "terminal.png"))
			assert.NoError(t, err, "the terminal is shot on a desktop")
			_, err = os.Stat(filepath.Join(out, "terminal-select.png"))
			assert.True(t, os.IsNotExist(err), "the copy sheet is phone only")
```

Then add these tests after the whole of `TestScreens_PaletteIsDesktopOnlyAndPressesTheChord`:

```go
func TestScreens_TheTerminalOnesAreNextOnlyAndTheCopySheetIsPhoneOnly(t *testing.T) {
	term := screen{"terminal", "#s/1/terminal"}
	sheet := screen{"terminal-select", "#s/1/terminal"}
	assert.Contains(t, screens, term)
	assert.Contains(t, screens, sheet)
	assert.Equal(t, screenMode{nextOnly: true, shows: "0.412s", drawn: `[data-terminal-state="live"]`}, modeOf(term))
	assert.Equal(t, screenMode{
		phoneOnly: true, nextOnly: true,
		ready: `[data-terminal-state="live"]`, shows: "0.412s",
		click: `[data-slot="terminal-select"]`, drawn: `[data-slot="terminal-text"]:not(:empty)`,
	}, modeOf(sheet))
	two, err := pickScreens("terminal,terminal-select")
	require.NoError(t, err)
	assert.Equal(t, []screen{term, sheet}, two)
}

func TestScreens_TheTerminalOnesWaitForTextTheCannedScreenHolds(t *testing.T) {
	raw, err := fixtureFiles.ReadFile("fixture/terminal.ansi")
	require.NoError(t, err)
	for _, name := range []string{"terminal", "terminal-select"} {
		shows := modeOf(screen{name, "#s/1/terminal"}).shows
		require.NotEmpty(t, shows, name)
		assert.Contains(t, string(raw), shows, "%s: a wait for text the canned route never sends would fail every run", name)
	}
}

func TestScreenMode_WaitsOnlyForAScreenWithAClickOrADrawnSelectorAndNoChord(t *testing.T) {
	assert.True(t, modeOf(screen{"terminal", "#s/1/terminal"}).waits())
	assert.True(t, modeOf(screen{"terminal-select", "#s/1/terminal"}).waits())
	assert.False(t, modeOf(screen{"palette", "#s/1"}).waits(), "a chord's screen is pressScreen's")
	assert.False(t, screenMode{}.waits())
	assert.False(t, screenMode{unpaired: true}.waits())
}

func TestForDevice_DropsThePhoneOnlyCopySheetOnADesktop(t *testing.T) {
	sheet := screen{"terminal-select", "#s/1/terminal"}
	term := screen{"terminal", "#s/1/terminal"}
	for _, name := range []string{"desktop", "desktop-narrow"} {
		assert.Empty(t, forDevice(devices[name], []screen{sheet}), name)
		assert.Equal(t, []screen{term}, forDevice(devices[name], []screen{term, sheet}), name)
	}
	for _, name := range []string{"iphone15", "iphone16"} {
		assert.Equal(t, []screen{term, sheet}, forDevice(devices[name], []screen{term, sheet}), name)
	}
}

func TestForApp_DropsTheNextOnlyScreensUnderTheClassicApp(t *testing.T) {
	term := screen{"terminal", "#s/1/terminal"}
	sheet := screen{"terminal-select", "#s/1/terminal"}
	fleet := screen{"fleet", ""}
	for _, app := range []string{"classic", ""} {
		assert.Equal(t, []screen{fleet}, forApp(app, []screen{fleet, term, sheet}), app)
	}
	assert.Equal(t, []screen{fleet, term, sheet}, forApp("next", []screen{fleet, term, sheet}))
}

func TestRun_ATerminalScreenAloneWhereItIsNotDrawnRendersNothing(t *testing.T) {
	for _, args := range [][]string{
		{"-device", "desktop", "-app", "next", "-screens", "terminal-select"},
		{"-device", "iphone15", "-screens", "terminal"},
		{"-device", "iphone15", "-screens", "terminal-select"},
	} {
		err := run(append([]string{"-out", t.TempDir(), "-browser", "no-such-browser-binary"}, args...))
		require.Error(t, err, args)
		assert.Contains(t, err.Error(), "no screen to render on", args)
		assert.NotContains(t, err.Error(), "no-such-browser-binary", "the check runs before a browser is looked for")
	}
}

func TestDrawScreen_WaitsForReadyThenTheTerminalsTextClicksOnceAndWaitsForTheSheet(t *testing.T) {
	mode := modeOf(screen{"terminal-select", "#s/1/terminal"})
	var mu sync.Mutex
	var readyAsks, showsAsks, clicks int
	var readyTrue, shown, clickedBeforeReady, clickedBeforeShown bool
	var drawnExpr string
	p, _ := recordingBrowser(t, func(method string, params map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		if method != "Runtime.evaluate" {
			return struct{}{}
		}
		expr, _ := params["expression"].(string)
		switch {
		case strings.Contains(expr, ".click()"):
			if !readyTrue {
				clickedBeforeReady = true
			}
			if !shown {
				clickedBeforeShown = true
			}
			clicks++
			return evalValue(true)
		case strings.Contains(expr, "terminal-state"):
			readyAsks++
			readyTrue = readyAsks > 2
			return evalValue(readyTrue)
		case strings.Contains(expr, "loomTerminal"):
			showsAsks++
			shown = showsAsks > 2
			return evalValue(shown)
		case strings.Contains(expr, "terminal-select"):
			return evalValue(true)
		case strings.Contains(expr, "terminal-text"):
			drawnExpr = expr
			return evalValue(clicks > 0)
		}
		return evalValue(false)
	})
	require.NoError(t, p.drawScreen(context.Background(), mode, 2*time.Second))
	mu.Lock()
	defer mu.Unlock()
	assert.False(t, clickedBeforeReady, "the click waits for the terminal to be live")
	assert.False(t, clickedBeforeShown, "the click waits for the terminal to hold its text, which the sheet reads once")
	assert.Equal(t, 1, clicks, "the click is made once")
	assert.Greater(t, readyAsks, 2, "the ready selector is polled until it matches")
	assert.Greater(t, showsAsks, 2, "the terminal's text is polled until it holds the canned result")
	assert.Contains(t, drawnExpr, ":not(:empty)", "the sheet counts as drawn only once it holds text")
}

func TestDrawScreen_ATerminalThatNeverShowsItsTextFailsBeforeTheClick(t *testing.T) {
	var mu sync.Mutex
	var clicks, showsAsks int
	var query string
	p, _ := recordingBrowser(t, func(method string, params map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		expr, _ := params["expression"].(string)
		switch {
		case strings.Contains(expr, ".click()"):
			clicks++
		case strings.Contains(expr, "loomTerminal"):
			showsAsks++
			query = expr
			return evalValue(false)
		}
		return evalValue(true)
	})
	err := p.drawScreen(context.Background(), modeOf(screen{"terminal-select", "#s/1/terminal"}), 300*time.Millisecond)
	require.Error(t, err)
	assert.Equal(t, `the terminal never showed "0.412s"`, err.Error())
	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, clicks, "a click on a terminal that holds no text would open an empty sheet")
	assert.Positive(t, showsAsks)
	assert.Contains(t, query, "0.412s", "the page is asked for the text the screen waits for")
	assert.Contains(t, query, `iframe[src^="/terminal.html"]`, "through the terminal's own frame")
}

func TestDrawScreen_ASheetThatNeverDrawsFailsAfterOneClick(t *testing.T) {
	var mu sync.Mutex
	var clicks int
	p, _ := recordingBrowser(t, func(method string, params map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		if method != "Runtime.evaluate" {
			return struct{}{}
		}
		expr, _ := params["expression"].(string)
		switch {
		case strings.Contains(expr, ".click()"):
			clicks++
			return evalValue(true)
		case strings.Contains(expr, "terminal-text"):
			return evalValue(false)
		}
		return evalValue(true)
	})
	err := p.drawScreen(context.Background(), modeOf(screen{"terminal-select", "#s/1/terminal"}), 300*time.Millisecond)
	require.Error(t, err)
	assert.Equal(t, `clicking [data-slot="terminal-select"] drew no [data-slot="terminal-text"]:not(:empty)`, err.Error())
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, clicks)
}

func TestDrawScreen_ATerminalThatNeverGoesLiveFailsBeforeAnyClick(t *testing.T) {
	var mu sync.Mutex
	var clicks int
	p, _ := recordingBrowser(t, func(method string, params map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		expr, _ := params["expression"].(string)
		if strings.Contains(expr, ".click()") {
			clicks++
		}
		return evalValue(false)
	})
	err := p.drawScreen(context.Background(), modeOf(screen{"terminal-select", "#s/1/terminal"}), 300*time.Millisecond)
	require.Error(t, err)
	assert.Equal(t, `no [data-terminal-state="live"] to wait for`, err.Error())
	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, clicks)
}

func TestDrawScreen_ADrawnScreenWaitsForItsSelectorWithNoClick(t *testing.T) {
	drawnOnly := screenMode{drawn: `[data-terminal-state="live"]`}
	var mu sync.Mutex
	var asks int
	var clicked bool
	p, _ := recordingBrowser(t, func(method string, params map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		expr, _ := params["expression"].(string)
		if strings.Contains(expr, ".click()") {
			clicked = true
		}
		asks++
		return evalValue(asks > 3)
	})
	require.NoError(t, p.drawScreen(context.Background(), drawnOnly, 2*time.Second))
	mu.Lock()
	assert.Greater(t, asks, 3, "it polls until the selector matches")
	assert.False(t, clicked)
	mu.Unlock()

	never, _ := recordingBrowser(t, func(string, map[string]any) any { return evalValue(false) })
	err := never.drawScreen(context.Background(), drawnOnly, 300*time.Millisecond)
	require.Error(t, err)
	assert.Equal(t, `no [data-terminal-state="live"] drew`, err.Error())
}

func TestDrawScreen_TheTerminalScreenWaitsForItsTextBeforeItsShot(t *testing.T) {
	mode := modeOf(screen{"terminal", "#s/1/terminal"})
	var mu sync.Mutex
	var showsAsks, clicks int
	var liveBeforeShown bool
	p, _ := recordingBrowser(t, func(method string, params map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		expr, _ := params["expression"].(string)
		switch {
		case strings.Contains(expr, ".click()"):
			clicks++
		case strings.Contains(expr, "loomTerminal"):
			showsAsks++
			return evalValue(showsAsks > 2)
		case strings.Contains(expr, "terminal-state"):
			if showsAsks <= 2 {
				liveBeforeShown = true
			}
		}
		return evalValue(true)
	})
	require.NoError(t, p.drawScreen(context.Background(), mode, 2*time.Second))
	mu.Lock()
	assert.Greater(t, showsAsks, 2, "the shot waits for the terminal's own text, not only for live")
	assert.Zero(t, clicks, "the terminal screen presses nothing")
	assert.False(t, liveBeforeShown, "the live selector is drawn-after, so the text wait has passed before it is asked")
	mu.Unlock()

	never, _ := recordingBrowser(t, func(method string, params map[string]any) any {
		expr, _ := params["expression"].(string)
		return evalValue(!strings.Contains(expr, "loomTerminal"))
	})
	err := never.drawScreen(context.Background(), mode, 300*time.Millisecond)
	require.Error(t, err)
	assert.Equal(t, `the terminal never showed "0.412s"`, err.Error(), "a terminal that is live and blank fails the run rather than shooting a blank")
}

func TestDrawScreen_ADesktopChordScreenOrAPlainOneDoesNothing(t *testing.T) {
	p, calls := recordingBrowser(t, func(string, map[string]any) any { return evalValue(true) })
	require.NoError(t, p.drawScreen(context.Background(), modeOf(screen{"palette", "#s/1"}), 300*time.Millisecond))
	require.NoError(t, p.drawScreen(context.Background(), screenMode{}, 300*time.Millisecond))
	assert.Empty(t, calls())
}

func TestClick_ClicksTheElementTheSelectorNamesAndFailsWhereNoneMatches(t *testing.T) {
	var mu sync.Mutex
	var expr string
	found := true
	p, _ := recordingBrowser(t, func(method string, params map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		if e, _ := params["expression"].(string); e != "" {
			expr = e
		}
		return evalValue(found)
	})
	require.NoError(t, p.click(context.Background(), `[data-slot="terminal-select"]`))
	mu.Lock()
	assert.Contains(t, expr, `[data-slot=\"terminal-select\"]`, "the selector is quoted into the page's script")
	assert.Contains(t, expr, ".click()")
	found = false
	mu.Unlock()
	err := p.click(context.Background(), `[data-slot="terminal-select"]`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing matches")
}
```

Fails without: `TestScreens_TheTerminalOnesAreNextOnlyAndTheCopySheetIsPhoneOnly`: either screen missing from `screens`, or a mode field of the two lists left out of `screenModes`
Fails without: `TestScreenMode_WaitsOnlyForAScreenWithAClickOrADrawnSelectorAndNoChord`: `waits` ignoring the chord (the palette would answer true) or the click or the drawn selector
Fails without: `TestForDevice_DropsThePhoneOnlyCopySheetOnADesktop`: the `m.phoneOnly` term in `forDevice` (the sheet then stays on a desktop); the phone rows pin that it is kept where the layout is the phone's
Fails without: `TestForApp_DropsTheNextOnlyScreensUnderTheClassicApp`: the `m.nextOnly` term in `forApp` (the classic run then waits for a `data-terminal-state` the classic app never draws)
Fails without: `TestRun_SkipLinesNameTheirReason` (its two new rows): the `m.phoneOnly` term and the `m.nextOnly` term in `skipReason`
Fails without: `TestRun_ATerminalScreenAloneWhereItIsNotDrawnRendersNothing`: either term in `forDevice` or `forApp` (the screen is then kept, and the run goes on to look for a browser)
Fails without: `TestScreens_TheTerminalOnesWaitForTextTheCannedScreenHolds`: a `shows` that `fixture/terminal.ansi` lacks on either screen (every run would then fail waiting), or no `shows` on either mode
Fails without: `TestDrawScreen_WaitsForReadyThenTheTerminalsTextClicksOnceAndWaitsForTheSheet`: the `mode.ready` wait (`clickedBeforeReady` is true), the `mode.shows` wait (`clickedBeforeShown` is true and `showsAsks` stays 0), the click made once (`clicks` is 0 or more than 1), the polls of `ready` and of the text, and the `:not(:empty)` in the mode's `drawn` (the sheet's expression then lacks it)
Fails without: `TestDrawScreen_ATerminalThatNeverShowsItsTextFailsBeforeTheClick`: the `mode.shows` wait's error return (the click is made on a terminal that shows nothing, `clicks` is 1, and `drawScreen` returns another error or none), and the page query naming the frame and the text
Fails without: `TestDrawScreen_ASheetThatNeverDrawsFailsAfterOneClick`: the `drawn` wait after the click, its `:not(:empty)` in the error text, and a retried click
Fails without: `TestDrawScreen_ATerminalThatNeverGoesLiveFailsBeforeAnyClick`: the `ready` wait's error return (the click would be made on a terminal that is not live)
Fails without: `TestDrawScreen_ADrawnScreenWaitsForItsSelectorWithNoClick`: the `drawn` wait for a screen with no click, and its error
Fails without: `TestDrawScreen_TheTerminalScreenWaitsForItsTextBeforeItsShot`: the `shows` on the `terminal` mode, or the `mode.shows` wait ahead of the click branch (`showsAsks` is 0, and the never-showing run returns nil instead of the error); the `shows` wait placed after the `drawn` wait (`liveBeforeShown` is true)
Fails without: `TestDrawScreen_ADesktopChordScreenOrAPlainOneDoesNothing`: the `waits` guard at the top of `drawScreen` (a chord's screen would be asked for a `drawn` selector twice)
Fails without: `TestClick_ClicksTheElementTheSelectorNamesAndFailsWhereNoneMatches`: the `el.click()` call, the quoting of the selector, the `nothing matches` error

- [ ] **Step 2: Run them to see them fail.**

```bash
CGO_ENABLED=0 go test ./tools/webshot/
```

Expected: FAIL. The package does not compile (`unknown field nextOnly in struct literal of type screenMode`, `p.drawScreen undefined`, `p.click undefined`, `modeOf(...).waits undefined`).

- [ ] **Step 3: Implement.** In `tools/webshot/main.go`, make these edits.

Replace the end of the `screens` comment and its declaration

```go
// next only.
var screens = []screen{
```

with

```go
// next only. "terminal" is session 1's worktree shell, drawn from the canned
// loom's terminal route, and "terminal-select" the same with the phone's copy
// sheet open (Select clicked once the terminal is live); both are shot under
// -app next only, the second on a phone only.
var screens = []screen{
```

and replace the `info` row

```go
	{"info", "#s/1/info"},
```

with

```go
	{"info", "#s/1/info"},
	{"terminal", "#s/1/terminal"},
	{"terminal-select", "#s/1/terminal"},
```

Replace the `screenMode` comment, type and map

```go
// screenMode is how a screen's tab and canned loom differ from the Home
// Screen app on a paired loom. desktopOnly is a screen only the desktop layout
// draws; press is a chord pressed once the desktop frame has drawn (Control+k,
// with Alt+, Control+, Meta+ and Shift+ prefixes allowed); drawn is the
// selector that must match after the press.
type screenMode struct {
	unpaired, browserTab, desktopOnly bool
	press, drawn                      string
}

var screenModes = map[string]screenMode{
	"palette":     {desktopOnly: true, press: "Control+k", drawn: `[data-slot="palette"]`},
	"pair":        {unpaired: true},
	"add-to-home": {browserTab: true},
}
```

with

```go
// screenMode is how a screen's tab and canned loom differ from the Home
// Screen app on a paired loom. desktopOnly is a screen only the desktop layout
// draws, phoneOnly one only the phone's layout draws, and nextOnly one only
// the new app draws (the classic app has no such screen); press is a chord
// pressed once the desktop frame has drawn (Control+k, with Alt+, Control+,
// Meta+ and Shift+ prefixes allowed); ready is a selector that must match
// before the screen's click; shows is text the framed terminal's own text()
// must hold before the click (a copy sheet reads it once, as it opens) or, for a
// screen with no click, before its shot (the frame's "live" comes before xterm.js has
// parsed and painted the bytes); click
// is a selector clicked once, when it matches (and, when the screen has a
// ready, only after that matches); drawn is the selector that must match after
// the press or the click, or, for a screen with neither, before its shot. A screen
// with a shows and no click waits for that text before its shot too.
type screenMode struct {
	unpaired, browserTab, desktopOnly, phoneOnly, nextOnly bool
	press, ready, shows, click, drawn                      string
}

var screenModes = map[string]screenMode{
	"palette":         {desktopOnly: true, press: "Control+k", drawn: `[data-slot="palette"]`},
	"pair":            {unpaired: true},
	"add-to-home":     {browserTab: true},
	"terminal":        {nextOnly: true, shows: "0.412s", drawn: `[data-terminal-state="live"]`},
	"terminal-select": {phoneOnly: true, nextOnly: true, ready: `[data-terminal-state="live"]`, shows: "0.412s", click: `[data-slot="terminal-select"]`, drawn: `[data-slot="terminal-text"]:not(:empty)`},
}
```

In `forDevice`, replace the comment words

```go
// agent). It tests modeOf's modes, not the screens' names, so a later screen of
// either kind is skipped too. todo is not changed.
```

with

```go
// agent) or only the phone's layout draws (a phoneOnly screen). It tests
// modeOf's modes, not the screens' names, so a later screen of either kind is
// skipped too. todo is not changed.
```

and the `if`

```go
		if (d.desktop && m.browserTab) || (!d.desktop && m.desktopOnly) {
```

with

```go
		if (d.desktop && (m.browserTab || m.phoneOnly)) || (!d.desktop && m.desktopOnly) {
```

In `forApp`, replace

```go
// forApp is todo, or without the desktopOnly screens unless app is next (the
// classic app has no desktop layout and the -app flag defaults to it). todo is
// not changed.
```

with

```go
// forApp is todo, or without the desktopOnly and nextOnly screens unless app is
// next (the classic app has no desktop layout and no terminal, and the -app
// flag defaults to it). todo is not changed.
```

and

```go
		if !modeOf(sc).desktopOnly {
			out = append(out, sc)
		}
```

with

```go
		if m := modeOf(sc); !m.desktopOnly && !m.nextOnly {
			out = append(out, sc)
		}
```

In `skipReason`, replace

```go
	case d.desktop && m.browserTab:
		return "phone only"
	case !d.desktop && m.desktopOnly:
		return "desktop only"
	case m.desktopOnly && app != "next":
		return "next app only"
```

with

```go
	case d.desktop && (m.browserTab || m.phoneOnly):
		return "phone only"
	case !d.desktop && m.desktopOnly:
		return "desktop only"
	case (m.desktopOnly || m.nextOnly) && app != "next":
		return "next app only"
```

In `shoot`, replace

```go
	time.Sleep(settle)
	if mode := modeOf(sc); mode.press != "" {
		if err := p.pressScreen(ctx, mode, settle, pressWait); err != nil {
			return nil, err
		}
		time.Sleep(settle)
	}
```

with

```go
	time.Sleep(settle)
	mode := modeOf(sc)
	if mode.press != "" {
		if err := p.pressScreen(ctx, mode, settle, pressWait); err != nil {
			return nil, err
		}
		time.Sleep(settle)
	}
	if mode.waits() {
		if err := p.drawScreen(ctx, mode, drawnWait); err != nil {
			return nil, err
		}
		time.Sleep(settle)
	}
```

and in the comment above `shoot`, replace the line

```go
// again before the capture. It returns what went
```

with

```go
// again before the capture; one with a drawn selector and no chord waits for it,
// after its click when it has one, and settles again. It returns what went
```

In `waitFor`, replace

```go
func (p *page) waitFor(ctx context.Context, selector string, within time.Duration) bool {
	q, _ := json.Marshal(selector)
	expr := "!!document.querySelector(" + string(q) + ")"
	deadline := time.Now().Add(within)
	for {
```

with

```go
func (p *page) waitFor(ctx context.Context, selector string, within time.Duration) bool {
	q, _ := json.Marshal(selector)
	return p.waitExpr(ctx, "!!document.querySelector("+string(q)+")", within)
}

// waitExpr polls every 100 ms until expr, a page expression that answers a boolean, answers true
// or within has passed, and reports whether it did. A query that errors counts as no match.
func (p *page) waitExpr(ctx context.Context, expr string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
```

(the loop's body, which queries `expr` and sleeps 100 ms, is unchanged). After the whole of `pressScreen` (it ends with `return nil\n}` before the comment `// appDrawn is true once the page is the app`), add:

```go
// drawnWait is how long a screen that waits for its own state (a live terminal, then a sheet)
// gets for each selector to match: the frame has to load xterm.js and connect first.
const drawnWait = 10 * time.Second

// waits reports whether the screen waits for its own state once the app has drawn, with no chord:
// for a drawn selector, after a click when it has one.
func (m screenMode) waits() bool {
	return m.press == "" && (m.click != "" || m.drawn != "")
}

// drawScreen waits for mode's own state: its ready selector first when it has one, then the
// text the framed terminal must hold when it has one (a sheet reads the text once, so a click
// before the terminal has parsed its first bytes would open an empty one), then, for a screen
// with a click, the click's selector, which it clicks once, and then the drawn selector.
// It does nothing for a screen with a chord (pressScreen's) or with none of these. The click
// is never retried.
func (p *page) drawScreen(ctx context.Context, mode screenMode, wait time.Duration) error {
	if !mode.waits() {
		return nil
	}
	if mode.ready != "" && !p.waitFor(ctx, mode.ready, wait) {
		return fmt.Errorf("no %s to wait for", mode.ready)
	}
	if mode.shows != "" && !p.waitShows(ctx, mode.shows, wait) {
		return fmt.Errorf("the terminal never showed %q", mode.shows)
	}
	if mode.click != "" {
		if !p.waitFor(ctx, mode.click, wait) {
			return fmt.Errorf("no %s to click", mode.click)
		}
		if err := p.click(ctx, mode.click); err != nil {
			return err
		}
		if !p.waitFor(ctx, mode.drawn, wait) {
			return fmt.Errorf("clicking %s drew no %s", mode.click, mode.drawn)
		}
		return nil
	}
	if !p.waitFor(ctx, mode.drawn, wait) {
		return fmt.Errorf("no %s drew", mode.drawn)
	}
	return nil
}

// waitShows polls until the framed terminal's text() holds text. The framing page reads it through
// the frame's same-origin window, as the app's panel does (Story 3's window.loomTerminal, which
// answers "" until the terminal has started). A frame not yet there, or a script that throws,
// counts as no match, as waitFor treats a query that errors.
func (p *page) waitShows(ctx context.Context, text string, within time.Duration) bool {
	q, _ := json.Marshal(text)
	expr := `(() => { const f = document.querySelector('iframe[src^="/terminal.html"]'); ` +
		`const t = f && f.contentWindow && f.contentWindow.loomTerminal; ` +
		`return !!t && t.text().includes(` + string(q) + `); })()`
	return p.waitExpr(ctx, expr, within)
}

// click clicks the first element selector matches.
func (p *page) click(ctx context.Context, selector string) error {
	q, _ := json.Marshal(selector)
	expr := "(() => { const el = document.querySelector(" + string(q) + "); " +
		"if (el === null) return false; el.click(); return true; })()"
	var ok bool
	if err := p.query(ctx, expr, &ok); err != nil {
		return fmt.Errorf("click %s: %w", selector, err)
	}
	if !ok {
		return fmt.Errorf("click %s: nothing matches", selector)
	}
	return nil
}
```

Then run `gofmt -l tools/webshot` (nothing printed: the struct's second line is padded to the first's width, and the map's keys are padded to `"terminal-select":`; `gofmt -w tools/webshot/main.go` fixes any drift).

- [ ] **Step 4: Run them to see them pass.**

```bash
CGO_ENABLED=0 go test ./tools/webshot/
```

Expected: PASS (`ok  	github.com/aidan-bailey/loom/tools/webshot`). The real-browser tests skip without `LOOM_WEBSHOT_BROWSER`.

- [ ] **Step 5: Commit.**

```bash
git commit -m "feat(webshot): the terminal and terminal-select screens" -- tools/webshot/main.go tools/webshot/webshot_test.go
```

### Task 6.4: the snapshot turns the terminal on

**Files:**
- Modify: `tools/webshot/fixture/snapshot.json:551`
- Modify: `tools/webshot/webshot_test.go` (after `TestFixture_TheTerminalScreenIsTheBytesOfASessionOnesShell`)
- Modify: `internal/web/ui/src/store/fleet.test.ts:81-83`

**Defines:** nothing shared. **Uses:** Story 4's `terminalApplies` (the terminal tab applies when the host's terminal is on and the session is started, active and not paused, which session 1 of the fixture is: group `working`); the fixture's single `"terminal": false` line, which is also what Vitest's `fixtureSnapshot()` reads.

- [ ] **Step 1: Write the failing test, and keep the Vitest test green.** In `tools/webshot/webshot_test.go`, after the whole of `TestFixture_TheTerminalScreenIsTheBytesOfASessionOnesShell`, add:

```go
func TestFixture_TheSnapshotTurnsTheTerminalOnForARunningSessionOne(t *testing.T) {
	c, _ := cannedForTest(t)
	raw, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var snap struct {
		Terminal bool `json:"terminal"`
		Sessions map[string][]struct {
			ID    int    `json:"id"`
			Group string `json:"group"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(raw, &snap))
	assert.True(t, snap.Terminal, "the terminal tab and its screens need the host's switch on")
	require.NotEmpty(t, snap.Sessions["1"])
	assert.Equal(t, 1, snap.Sessions["1"][0].ID)
	assert.Equal(t, "working", snap.Sessions["1"][0].Group, "the terminal applies to a session that is started, active and not paused")
}
```

In `internal/web/ui/src/store/fleet.test.ts`, in the test `sets the snapshot's terminal flag from the terminal event`, replace

```ts
  it("sets the snapshot's terminal flag from the terminal event", () => {
    const fixture = fixtureSnapshot(NOW);
```

with

```ts
  it("sets the snapshot's terminal flag from the terminal event", () => {
    const fixture = { ...fixtureSnapshot(NOW), terminal: false };
```

That test asserts `terminal` is false right after the fixture loads, then true and false again on the events; the fixture's flag becomes true in Step 3, so the test starts from an explicit false and keeps pinning both directions of the event.

Fails without: `TestFixture_TheSnapshotTurnsTheTerminalOnForARunningSessionOne`: the `"terminal": true` line in `snapshot.json` (the assertion `the terminal tab and its screens need the host's switch on` fails with `false`), and session 1 staying in the `working` group
Fails without: `sets the snapshot's terminal flag from the terminal event` (the `fleet.test.ts` edit): with the old `fixtureSnapshot(NOW)` alone, its first `expect(store().snapshot?.terminal).toBe(false)` fails once the fixture's flag is true

- [ ] **Step 2: Run it to see it fail.**

```bash
CGO_ENABLED=0 go test ./tools/webshot/ -run TestFixture_TheSnapshotTurnsTheTerminalOn
```

Expected: FAIL: `the terminal tab and its screens need the host's switch on` (`expected: true`, `actual: false`).

- [ ] **Step 3: Implement.** In `tools/webshot/fixture/snapshot.json`, replace the last two lines

```
  "terminal": false
}
```

with

```
  "terminal": true
}
```

- [ ] **Step 4: Run the tests, and find any Vitest test the flag moves.**

```bash
CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ && pnpm -C internal/web/ui run test
```

Expected: the Go tests pass (`TestWebshotFixture_IsTheJSONTheLayerServes` decodes the snapshot with unknown fields refused: `terminal` is a field it knows). Vitest passes. The one expectation the flag moves, as far as a search of the tests that read `fixtureSnapshot()` finds, is the one edited in Step 1: the tab-list tests (`shell.test.tsx`, `DesktopShell.test.tsx`, `Inspector.test.tsx`, `Palette.test.tsx`, `palette.test.ts`) register their own panels and never import `@/panels`, so no terminal panel is registered for them, and Story 4's tests set `terminal` explicitly. If a Vitest file fails now with a tab list gaining `terminal` or a `terminal` flag read from the fixture, pin that test's snapshot with `{ ...fixtureSnapshot(), terminal: false }` (the form Step 1 uses), and name the file in the story's report: it is not in this story's file list.

- [ ] **Step 5: Commit.**

```bash
git commit -m "test(webshot): the canned snapshot has the terminal on" -- tools/webshot/fixture/snapshot.json tools/webshot/webshot_test.go internal/web/ui/src/store/fleet.test.ts
```

### Task 6.5: the stale comment in `sw.js`, the docs, and the rebuilt bundle

**Files:**
- Modify: `internal/web/ui/public/sw.js:54-55`
- Modify: `internal/web/README.md:107`
- Modify: `docs/claude/web-layer.md:77,97,133`
- Modify: `tools/CLAUDE.md:18,20`
- Modify: `internal/web/dist/**` (rebuilt)

**Defines:** nothing. **Uses:** `terminalOnly` in `internal/web/static.go` (the document and `/assets/terminal-*` are left out of the worker's precache list, so its fetch handler finds nothing in the cache for them and the browser's request goes to the network).

- [ ] **Step 1: Correct the comment in the worker.** In `internal/web/ui/public/sw.js`, replace

```js
  // Without respondWith the browser asks the network; the page's script and styles are other
  // paths and still come from the cache below.
```

with

```js
  // Without respondWith the browser asks the network. The page's own script and styles
  // (assets/terminal-*, with xterm.js in the script) are other paths, and the worker never
  // precaches them (terminalOnly in internal/web/static.go): the lookup below finds nothing in the
  // cache for them, so they come from the network too.
```

- [ ] **Step 2: Bring the docs along.** In `internal/web/README.md`, in the paragraph of "The new app's other screens" that ends `... under \`-app next\` on a desktop device only, \`palette\` after pressing Ctrl+K.`, replace

````
and, under `-app next` on a desktop device only, `palette` after pressing Ctrl+K.
````

with

````
and, under `-app next` only, `terminal` (session 1's worktree shell: the canned loom answers the terminal's WebSocket route with `tools/webshot/fixture/terminal.ansi`, which the frame draws) and, on a phone only, `terminal-select` (the copy sheet, after Select is clicked once the terminal is live), and, on a desktop device only, `palette` after pressing Ctrl+K.
````

In `docs/claude/web-layer.md`, in the paragraph that begins `` `go run ./tools/webshot -serve 127.0.0.1:5670` serves the canned loom's API ``, replace

````
and not the phone-only `add-to-home`, and they alone render `palette`
````

with

````
and not the phone-only `add-to-home` and `terminal-select`, and they alone render `palette`
````

and, in the bullet that begins `` - To see a screen without a phone, run ``, replace

````
`add-to-home` is skipped on them (`skipped: phone only`), and `palette`
````

with

````
`add-to-home` and `terminal-select` are skipped on them (`skipped: phone only`), and `palette`
````

and replace

````
(a phone skips it as `skipped: desktop only`, the classic app as `skipped: next app only`). It prints console errors
````

with

````
(a phone skips it as `skipped: desktop only`, the classic app as `skipped: next app only`); `terminal` (session 1's worktree shell: the canned loom's WebSocket route `GET /api/v1/sessions/{id}/terminal` answers the frame with `tools/webshot/fixture/terminal.ansi` and then `{"state":"live"}`, and the screen waits for the frame's own text to hold the canned run's last result and for the panel's `data-terminal-state="live"`) is shot on every device and `terminal-select` (the same, after Select is clicked, with the copy sheet open) on a phone only, and both only under `-app next` (the classic app skips them as `skipped: next app only`), since the classic app has no terminal panel. It prints console errors
````

In `tools/CLAUDE.md`, line 18 (the rule `- **Feed \`webshot\` only JSON the web layer serves ...`), replace

````
and keep it stdlib-only, with no daemon, session or tmux.**
````

with

````
and keep it to the standard library and `github.com/coder/websocket` (its canned terminal route), with no daemon, session or tmux.**
````

and replace

````
and is served under a copy of `mockupCSP` the same test holds equal.
````

with

````
and is served under a copy of `mockupCSP`, and the app's `/terminal.html` under a copy of `terminalCSP`, each held equal to the layer's by the same test.
````

Line 20, the rule on the desktop devices, names `add-to-home` as the phone-only screen it skips; `terminal-select` is skipped on them too, and the line is 1,192 bytes, so the addition is paid for in the same replacements. In `tools/CLAUDE.md`, replace

````
`add-to-home` skipped on them, and `palette` shot only on a desktop under `-app next`.**
````

with

````
`add-to-home` and `terminal-select` skipped on them, and `palette` shot only on a desktop under `-app next`.**
````

and replace

````
and the run then misses console errors without failing;
````

with

````
and the run then misses console errors silently;
````

and replace

````
the real-browser render is `TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser`, run only with `LOOM_WEBSHOT_BROWSER` set.
````

with

````
the real-browser render is `TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser`, with `LOOM_WEBSHOT_BROWSER` set.
````

The line comes to 1,198 bytes (`sed -n 20p tools/CLAUDE.md | wc -c` prints 1199 with its newline), under the 1,200-byte limit. The test it names, `TestForDevice_DropsAddToHomeOnADesktopAndThePaletteOnAPhone`, keeps its name in Task 6.3. Check the budgets hold: `go run ./tools/claudemd -v` lists the headroom of `tools/CLAUDE.md`; line 18 was 980 bytes and comes to about 1,140.

The iPhone checklist of `docs/claude/web-layer.md` ("The iPhone checklist before cutover", its numbered list ends at item 14) has no item for the terminal, whose device checks only a phone can show (spec Testing, §54). In `docs/claude/web-layer.md`, replace its last line

````
14. Switch back to classic, with the daemon down as well.
````

with (item 14 is kept as it is, and the terminal's items follow it, so no earlier line moves)

````
14. Switch back to classic, with the daemon down as well.
15. With `loom web terminal on`, open a started session's Terminal tab and run a command from the phone's command line, then use the key row (Esc, Tab, the arrows, Ctrl) and see each key's effect in the shell.
16. Scroll the terminal with a finger and flick it: the scrollback follows the finger, and the flick coasts and stops when touched.
17. Press Select, copy a line from the copy sheet, and read the sheet's renderer line (`Renderer: WebGL` or `Renderer: DOM`).
18. Lock and unlock the phone and find the shell as it was.
19. Lose the network on the phone and see the terminal reconnect with the scrollback held once, not doubled.
````

- [ ] **Step 3: Rebuild the bundle.** `pnpm -C internal/web/ui run build` rewrites `internal/web/dist/`; the only change it makes for this task is `internal/web/dist/sw.js` (the worker is copied from `public/` with its comment), so `git status --short internal/web/dist` lists that one file. If it lists more, an earlier story's source change was left unbuilt: commit it with this rebuild and say so in the report.

- [ ] **Step 4: Run the checks.**

```bash
bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ ./tools/claudemd/'
```

Expected: all pass (`node --check public/sw.js` is part of `check`; `dist:check` finds the bundle fresh).

- [ ] **Step 5: Commit.**

```bash
git commit -m "docs(webshot): the terminal screens, the worker's comment and the rebuilt bundle" -- internal/web/ui/public/sw.js internal/web/README.md docs/claude/web-layer.md tools/CLAUDE.md internal/web/dist
```

### Task 6.6: run the screens for real, and turn WebGL off only if the shot needs it

**Files:**
- Modify (only in Step 3, if the shot is blank): `tools/webshot/main.go` (`launchBrowser`, and a new `browserArgs`)
- Modify (only in Step 3): `tools/webshot/webshot_test.go` (after `TestBrowserArgs`'s place, at the end of the file's tests)

**Defines (private to this story, only if Step 3 runs):** `browserArgs(profile string) []string` in `main.go`. **Uses:** Story 3's renderer choice (the frame uses xterm.js's WebGL addon when a `webgl2` context exists and the DOM renderer otherwise) and Story 5's footer `Renderer: WebGL` or `Renderer: DOM`.

- [ ] **Step 1: Render the screens.** This needs a Chromium on `PATH` (or `-browser <path>`), and the bundle of Task 6.5. From the repo root:

```bash
bash -c 'd=$(mktemp -d) && echo "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -screens terminal -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -screens terminal -out "$d"'
```

Expected: the directory it prints, then one `<name>\t<file>` line per screen and no `!` line (no console error, no CSP violation, no route the fixture lacks), the exit status 0. The first run writes `terminal.png` and `terminal-select.png` on the phone; the other two write `terminal.png` (overwriting it) at the two desktop sizes. Run the same three commands once more with `-app classic` for the first: `go run ./tools/webshot -out "$d"` prints `terminal\tskipped: next app only` and `terminal-select\tskipped: next app only` and exits 0.

- [ ] **Step 2: Look at the shots.** Open each of `$d/terminal.png` and `$d/terminal-select.png` with the Read tool (it shows an image). `terminal.png` must show the canned text: `fix-pane-cursor $ git status -sb`, `## aidanb/fix-pane-cursor [ahead 3]`, ` M ui/pane.go`, ` M ui/pane_test.go`, `go test ./ui/`, `ok  github.com/aidan-bailey/loom/ui  0.412s` and a last prompt, with the key row under the frame on the phone. `terminal-select.png` must show the copy sheet over it, with the same text in the sheet and a footer reading `Renderer: WebGL` or `Renderer: DOM`. If both show the text, skip Step 3 and go to Step 4.

- [ ] **Step 3: Only if the terminal's area is blank where the text should be** (a headless WebGL canvas can come out empty in a screenshot), start the canned run's Chromium with WebGL off, so the frame's `webgl2` probe fails and xterm.js draws with its DOM renderer. In `tools/webshot/webshot_test.go`, add at the end of the file:

```go
func TestBrowserArgs_TurnWebGLOffAndKeepThePipeAndTheProfile(t *testing.T) {
	args := browserArgs("/tmp/p")
	assert.Contains(t, args, "--disable-3d-apis", "the terminal frame then draws with the DOM renderer, whose text a screenshot shows")
	assert.Contains(t, args, "--remote-debugging-pipe")
	assert.Contains(t, args, "--user-data-dir=/tmp/p")
	assert.Equal(t, "about:blank", args[len(args)-1])
}
```

Run `CGO_ENABLED=0 go test ./tools/webshot/ -run TestBrowserArgs` (FAIL: `undefined: browserArgs`). In `tools/webshot/main.go`, in `launchBrowser`, replace

```go
	cmd := exec.Command(bin, "--headless", "--remote-debugging-pipe", "--no-first-run", "--no-default-browser-check",
		"--disable-gpu", "--hide-scrollbars", "--mute-audio", "--user-data-dir="+profile, "about:blank")
```

with

```go
	cmd := exec.Command(bin, browserArgs(profile)...)
```

and before `func launchBrowser`, add:

```go
// browserArgs is Chromium's command line for a run: headless, over the debugging pipe, with a
// throwaway profile. WebGL is off (--disable-3d-apis), so the terminal's frame draws with xterm.js's
// DOM renderer: a headless WebGL canvas can come out blank in a screenshot.
func browserArgs(profile string) []string {
	return []string{"--headless", "--remote-debugging-pipe", "--no-first-run", "--no-default-browser-check",
		"--disable-gpu", "--disable-3d-apis", "--hide-scrollbars", "--mute-audio", "--user-data-dir=" + profile, "about:blank"}
}
```

Run `gofmt -l tools/webshot`, then the test again (PASS), then Step 1's commands and Step 2's look again: the text must show now, and the sheet's footer reads `Renderer: DOM`.

Fails without: `TestBrowserArgs_TurnWebGLOffAndKeepThePipeAndTheProfile`: the `--disable-3d-apis` flag, `--remote-debugging-pipe` (the driver reads the browser on fds 3 and 4), or the profile flag, and `about:blank` as the last argument

- [ ] **Step 4: Run the story's gates.**

```bash
bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi'
```

```bash
CGO_ENABLED=0 go vet ./...
```

```bash
CGO_ENABLED=0 go test -timeout 30m ./...
```

```bash
bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ ./tools/claudemd/ && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'
```

Expected: all pass. With a browser available, also run `LOOM_WEBSHOT_BROWSER=1 CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestRun_Renders|TestRun_ARealPhone'`: the classic run (`TestRun_RendersEveryScreenInARealBrowser`) skips both terminal screens, and the new app's two desktop sizes write `terminal.png` and no `terminal-select.png`.

- [ ] **Step 5: Commit (only if Step 3 ran).**

```bash
git commit -m "fix(webshot): start the canned run's Chromium with WebGL off" -- tools/webshot/main.go tools/webshot/webshot_test.go
```
