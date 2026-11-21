# Loom web desktop, part 1: reach and frame — Implementation Plan

> **For agentic workers:** this plan runs supervised: `/flow:draft` writes the stories from this skeleton, then `/flow:run` runs them story by story. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the new web app usable on a desktop by mouse: reachable at `http://localhost:<port>` beside the tunnel, paired by `loom web open`, and drawn at 1024px and wider as a top bar, a fleet sidebar, the route's panel in the middle and an inspector holding another view of the same session.

**Architecture:** The web layer's single origin rule becomes a set (the configured origin plus the listener's own loopback origins), and a new CLI command mints a pairing code and opens the browser on a link the app consumes before its router starts. In the app, `AppShell` switches between today's frame (`PhoneShell`) and a lazily loaded `DesktopShell` by a width media query; the desktop chrome reuses the fleet helpers and the panel registry, panels scroll through a `ScrollPane` seam instead of the window, and `askConfirm` draws a Dialog on desktop. No route under `/api/v1`, wire, Core request or `rpc.Protocol` change.

**Tech Stack:** Go (testify) for the web layer, the CLI and webshot; TypeScript, React 19, Vite, Tailwind CSS 4, shadcn/ui on Base UI 1.9 (Dialog), TanStack Router 1, Zustand 5, Vitest with happy-dom and Testing Library; `tools/webshot` (headless Chromium).

**Spec:** `docs/superpowers/specs/2026-11-21-web-desktop-design.md`

**Part:** 1 of 2

**Budget:** draft $47; run $35 (estimate 2026-11-21)

## File structure

The web layer and the CLI:

- `core/web.go` (changed): `LocalOrigin`, `IsDeviceExists`; `core/README.md` (changed).
- `internal/web/server.go`, `internal/web/web.go` (changed): the origin set; `internal/web/web_test.go` (changed); `internal/web/README.md`, `internal/web/CLAUDE.md` (changed: the guard rule's host and origin wording).
- `internal/cli/web.go` (changed: `loom web open`, the status's Local row); `internal/cli/web_open.go` (new: the command and the opener's wait); `internal/cli/open_unix.go`, `internal/cli/open_windows.go` (new: the opener's command per platform); `internal/cli/web_test.go` (changed); `internal/cli/README.md`, `docs/ARCHITECTURE.md` (changed: the web subcommands).
- `e2e/web_test.go`, `e2e/CLAUDE.md` (changed): pairing and streaming over the local origin.
- `USAGE.md` (changed).

The new app (`internal/web/ui/src/`):

- `app/pairHold.ts`, `app/pairLink.ts`, `panels/pair/code.ts` (new); `main.tsx`, `app/switchApp.ts`, `panels/pair/Pair.tsx` (changed).
- `app/scroll.tsx` (new); `panels/turns/Conversation.tsx`, `panels/job/JobBody.tsx`, `panels/flow/FlowDocBody.tsx` (changed: scroll through the pane); `session/NoSession.tsx` and its callers (changed: the session id from props).
- `components/ui/dialog.tsx` (new); `app/ConfirmSheet.tsx`, `components/ui/toast.tsx` (changed: a variant and a placement).
- `app/viewport.ts`, `app/desktopState.ts`, `test-layout.ts` (new); `app/shell.tsx` (changed: `PhoneShell`, the switch, shared parts exported); `test-setup.ts` (changed: the phone layout by default).
- `app/status.tsx` (new: the status row's parts); `fleet/filter.ts`, `fleet/WorkspaceFilter.tsx` (new); `panels/fleet/index.tsx`, `panels/fleet/FleetHead.tsx`, `panels/fleet/SessionRow.tsx`, `panels/new/NewSessionBody.tsx` (changed).
- `desktop/DesktopShell.tsx`, `desktop/TopBar.tsx`, `desktop/Sidebar.tsx`, `desktop/inspector.ts`, `desktop/Inspector.tsx` (new), each with a test beside it.
- `*.test.ts(x)` beside each module they test.

The built bundle: `internal/web/dist/` (rebuilt by every story that changes the app, committed).

Elsewhere: `internal/web/static_budget_test.go` (the lazy chunk list); `tools/webshot/frame.go`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `.github/workflows/web.yml`, `tools/CLAUDE.md`; `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`.

## Names contract

Every name one story defines and another uses. A story may add names of its own; a name another story needs and this section lacks goes back to the manager. Paths are under `internal/web/ui/src/` unless they start at the repo root.

**The local origin (Story 1):**

- `func LocalOrigin(addr string) (string, error)` (Story 1), in `core/web.go`: `http://loom.localhost:<port>` for a bound `127.0.0.1:<port>` or `[::1]:<port>`, the port from 1 to 65535; an error for any other address, which for another loopback IP literal says it has no local origin and to listen on 127.0.0.1 or ::1. Bare `localhost` and the IP literal are never a local origin (spec §21: a cookie on them reaches every local port).
- `internal/web/server.go` (Story 1): `guard` accepts a `Host`, and on a write exactly one `Origin`, from the set of the configured origin and `LocalOrigin` of the bound address when it has one; a configured origin equal to the local one counts once; `bind` builds the set from the address it bound; `Layer.rule` (`internal/web/web.go`) holds the set; push keeps the configured origin as its subject.
- The guard test case `"localhost is not 127.0.0.1"` in `internal/web/web_test.go` (Story 1) stays refused on every listener; a new case accepts `loom.localhost:7420` (as `Host`, and as the write's `Origin` `http://loom.localhost:7420`) on a `127.0.0.1:7420` listener and refuses it on a listener on another port.

**`loom web open` (Story 2):**

- `loom web open [--name <device>]` (Story 2), default name `desktop` (`const defaultDesktopName = "desktop"`, Story 2), in `internal/cli/web_open.go`, registered by `newWebCmd` in `internal/cli/web.go`.
- `func IsDeviceExists(err error, name string) bool` (Story 2), in `core/web.go` beside `IsNoDevice`: true when `err` is `ErrDeviceExists` or carries the text `fmt.Errorf("%w: %q", ErrDeviceExists, name)` gives (model errors cross the wire as text). `loom web open` calls `PairDevice` first and reads such a refusal for the trimmed name as "paired"; it never reads `Devices` to decide.
- The order in `loom web open` (Story 2): refused unless the web is serving (`WebView.Serving`); then `<local>` is built with `LocalOrigin(w.Addr)`, refused when there is none; only then is a code minted.
- `var openBrowser func(url string) error` (Story 2), in `internal/cli/web_open.go`: the seam tests replace; it starts the command `openerCommand` builds for the URL and waits through `waitOpener`.
- `func openerCommand(url string) *exec.Cmd` (Story 2): `xdg-open <url>` in `internal/cli/open_unix.go` (macOS: `open <url>`, by `runtime.GOOS`), `rundll32 url.dll,FileProtocolHandler <url>` in `internal/cli/open_windows.go`; its `Stdin`, `Stdout` and `Stderr` left nil (`/dev/null`), so a console browser never takes the terminal; on unix it starts in its own session (`Setsid`), so a Ctrl-C during the wait doesn't reach it.
- `const openerWait = 5 * time.Second` (Story 2) and `func waitOpener(cmd *exec.Cmd, limit time.Duration) error` (Story 2): nil when the opener exits 0 within `limit` or is still running at `limit` (left running, never killed); an error naming the exit status when it exits otherwise within `limit`. The `<reason>` printed is the start error or that exit status, never the opener's output.
- The messages tests match (Story 2): `The web app is off; turn it on with loom web enable.`; `The web app is off, and its settings file is left alone (<WebView.Error>); see loom web status.` when `Listen` is empty and `Error` set; `The web app is not serving (<WebView.Error>); see loom web status.`, or `The web app is not serving yet; see loom web status.` with no error; `The web app has no local origin: <LocalOrigin's error>.`; `Pairing code for <name> expires at <HH:MM:SS>.`; `<name> is already paired. If this browser is not, pass --name or run loom web revoke <name>.`; `Opening <url>` before the opener runs; `Could not open a browser (<reason>); open the URL above yourself.`
- `writeWebStatus` (Story 2): while serving, a row `Local:` with `LocalOrigin(w.Addr)`, or `none (<its error>)` when it has none.
- The pairing link (Story 2 writes it, Story 3 reads it): `<local>/?ui=next#/pair?code=<8 characters>` (the code as `PairDevice` returns it, without `formatPairingCode`'s separator), `<local>` being `LocalOrigin(w.Addr)`; `<local>/?ui=next` for a paired name. `?ui=next` makes `treeOf` serve the new app to a browser with no `__Host-loom-ui` cookie; the code stays in the fragment, so no request or log sees it.

**The pairing link in the app (Story 3)**, in `app/pairLink.ts` (`watchPairLink`) and the leaf `app/pairHold.ts` (`adoptNextApp`, `holdPairLink`):

- The code leaves the address before any module that builds the router evaluates (`app/routes.tsx` builds its hash history at import): `app/pairHold.ts` imports nothing that reaches `app/routes.tsx`, `main.tsx` imports it first (ahead of `./styles/app.css`, `@/panels` and `@/commands`), and its evaluation runs `adoptNextApp` and `holdPairLink`. `normalizeCode` and `CODE_LENGTH` move from `panels/pair/Pair.tsx` to the leaf `panels/pair/code.ts` (Story 3), and `Pair.tsx` re-exports them. The main test asserts the router's own location (`router.history.location`) holds no code.
- `function adoptNextApp(): void` (Story 3), in `app/pairHold.ts`: when the address's query has `ui=next` and `document.cookie` holds no `__Host-loom-ui`, sets that cookie to `next` with the attributes `switchApp` gives it (through one writer shared with `app/switchApp.ts`), so later launches and the worker (`registerWorker` registers `/sw.js?ui=${cookieApp()}`) keep the new app; an explicit `classic` cookie is left as it is. `app/pairHold.ts`'s evaluation runs it first, before `holdPairLink`.
- `function holdPairLink(): void` (Story 3), in `app/pairHold.ts`: when `window.location.hash` is `#/pair` or `#pair` with a `code` query value, replaces it with `#/pair` (`history.replaceState`, keeping `history.state`) and holds the code as `normalizeCode` (from `panels/pair/code.ts`) returns it when its length is `CODE_LENGTH`; it runs before `registerWorker()` and before the router or the stream starts. A worker takeover that reloads the page drops the held code (spec §26): the pairing screen then shows as usual.
- `function watchPairLink(): () => void` (Story 3): on the first `onUnpaired` signal while a code is held, runs `device.pair` with `{ code }` through `runCommand` with a context whose `panel` is `"pair"`, then drops the code; a live (not stale) snapshot drops it unused; returns its stop. `main.tsx`'s `start()` calls it before `startFleet()`.

**The scroll pane and the body props (Story 4)**, in `app/scroll.tsx`:

- `interface ScrollPane { top: () => number, viewport: () => number, height: () => number, scrollTo: (top: number) => void, topFor: (el: Element) => number, subscribe: (listener: () => void) => () => void }` (Story 4): `top` the scroll position, `viewport` the visible height, `height` the content height, `topFor` the scroll position that puts `el`'s top just below the pane's pinned head (at least 0), `subscribe` called on each scroll, returning its stop.
- `const windowPane: ScrollPane` (Story 4): the window (`window.scrollY`, `window.innerHeight`, `document.documentElement.scrollHeight`, its `scrollTo` calling `window.scrollTo` with 0 and the top), its pinned head `[data-slot="header"]`.
- `function elementPane(scroller: HTMLElement, head: HTMLElement | null): ScrollPane` (Story 4): an element that scrolls; `head` a pinned element inside the scroller that overlaps its top (sticky), whose height `topFor` subtracts, or `null` when the pane's head sits outside the scroller (nothing subtracted). Stories 6 and 10 put their pinned heads outside their scrollers and pass `null`.
- A desktop pane (Stories 6 and 10) renders its panel's Body only once its scroller element has mounted (a callback ref into state), so no desktop panel ever renders against `windowPane`; each story's test pins that its pane's panel reads no `window.scrollY`.
- `function ScrollPaneProvider(props: { pane: ScrollPane, children: ReactNode }): ReactElement` (Story 4) and `function useScrollPane(): ScrollPane` (Story 4): the pane in context, `windowPane` without a provider.
- `function NoSession(props: { sessionId: number | undefined }): ReactElement` (Story 4), in `session/NoSession.tsx`: reads no route.

**The dialog, the sheet and the toasts (Story 5):**

- `components/ui/dialog.tsx` (Story 5): shadcn's Dialog on `@base-ui/react/dialog`, shaped as `drawer.tsx`, exporting `Dialog` (Base UI `Dialog.Root`), `DialogContent` (Portal, Backdrop, Viewport and Popup, centred, its children in the popup, no close button), `DialogTitle`, `DialogDescription` (Base UI's Title and Description) and `DialogFooter` (a `div`), each with its `data-slot`; no `DialogHeader`, `DialogClose` or `DialogTrigger`.
- `type ConfirmVariant = "drawer" | "dialog"` and `function ConfirmSheet(props: { variant: ConfirmVariant }): ReactElement` (Story 5), in `app/ConfirmSheet.tsx`: the same sheet content in a Drawer or a centred Dialog; `askConfirm`'s API unchanged.
- `type ToastPlacement = "top" | "bottom-right"` and `function Toaster(props: { placement: ToastPlacement }): ReactElement` (Story 5), in `components/ui/toast.tsx`: its viewport carries `data-slot="toast-viewport"` and `data-placement`, which Story 6's tests read.

**The layout and the shell (Story 6):**

- `type Layout = "phone" | "desk2" | "desk3"`, `const DESK2_MIN = 1024`, `const DESK3_MIN = 1280`, `function layoutOf(width: number): Layout` and `function useLayout(): Layout` (Story 6), in `app/viewport.ts`: `useLayout` reads `matchMedia` through `useSyncExternalStore`.
- `const DESKTOP_KEY = "loom.desktop"`, `interface DesktopSlot { tab: string }`, `interface DesktopPrefs { sidebar: boolean, inspector: boolean, slots: DesktopSlot[] }` and `function readDesktopPrefs(raw: string | null): DesktopPrefs` (Story 6), in `app/desktopState.ts`: anything that does not parse into that shape is `{ sidebar: true, inspector: true, slots: [] }`.
- `interface DesktopState { sidebar: boolean, inspector: boolean, slots: DesktopSlot[], setSidebar: (shown: boolean) => void, setInspector: (shown: boolean) => void, setTab: (tab: PanelId) => void }` and `const useDesktop: UseBoundStore<StoreApi<DesktopState>>` (Story 6): read from `DESKTOP_KEY` once, written back on each set (fields this layout doesn't own kept), and following another window's write (the `storage` event); `setTab` sets the first slot's tab, adding the slot when there is none.
- `app/shell.tsx` (Story 6): `function PhoneShell(): ReactElement` (today's frame without the sheet and the toaster); `AppShell` draws `PhoneShell` for the phone layout, for pairing and for the splash with no snapshot, else the lazy `DesktopShell`, and mounts `<ConfirmSheet variant>` and `<Toaster placement>` once above both (`"drawer"`/`"top"` on the phone, `"dialog"`/`"bottom-right"` on desktop); exports `Boundary`, `TABBED`, `tabOf` and `function resetKey(panel: PanelId, props: PanelProps): string` (today private), which `DesktopShell` and Story 10's inspector use.
- `function DesktopShell(): ReactElement` (Story 6), the default export of `desktop/DesktopShell.tsx`: the grid; Story 6 draws its middle (the route's Head, `SessionTabs` and Strip pinned above the scroller, outside it, and the Body in an `elementPane` over the scroller with a `null` head, through `ScrollPaneProvider`, drawn once the scroller has mounted) and the full mockup full-window; Stories 8 to 10 add the top bar, the sidebar and the inspector to it.
- `function PanelHead(props: { route: RouteState, session: Session | undefined }): ReactElement` (Story 6), exported from `app/shell.tsx`: the route's head rows (its Head, `SessionTabs` for the routes in `TABBED`, its Strip), which `PhoneShell` and `DesktopShell`'s middle both draw.
- The desktop frame's slots (Story 6), which Stories 8 to 10 extend and their tests query: `data-slot="desktop"` (the grid), `"middle"`, `"middle-head"` (the pinned head) and `"middle-scroller"`; Story 8 adds `"top-bar"`, Story 9 `"sidebar"`.
- `checkLazyViews`' list in `internal/web/static_budget_test.go` gains `DesktopShell` (Story 6).
- `function setViewportWidth(width: number): void` (Story 6), in `test-layout.ts` (test-only): sets the window width that `matchMedia` and `useLayout` read and notifies `useLayout`'s subscribers; `test-setup.ts` sets 390 (the phone layout) before each test, so every test runs on the phone unless it calls it (happy-dom's default 1024px would read `desk2`). Stories 6 to 10's desktop tests call it.

**The status parts and the filter (Story 7):**

- `function HostLabel(): ReactElement`, `function LinkPill(): ReactElement` and `function GroupCounts(): ReactElement` (Story 7), in `app/status.tsx`: `StatusRow`'s parts as they draw today (`loom@host` with the last update's time while offline; the link's pill; the three counts that open the group's next session), which `StatusRow` composes. Each takes `props: { dense?: boolean }`: `dense` drops the 44px minimum (the top bar's density, spec §11), and without it each draws as today.
- `const FILTER_KEY = "loom.filter"` (Story 7), moved from `panels/fleet/index.tsx` to `fleet/filter.ts`; `panels/new/NewSessionBody.tsx` imports it from there.
- `function readFilter(): string` (Story 7), in `fleet/filter.ts`: the value stored under `FILTER_KEY`, `""` when absent or unreadable; a test that presets `loom.filter` resets the store with `useFilter.setState({ stored: readFilter() })`.
- `interface FilterState { stored: string, pick: (id: string) => void }` and `const useFilter: UseBoundStore<StoreApi<FilterState>>` (Story 7), in `fleet/filter.ts`: the stored choice (a workspace id as a string, or `""` for all), read from `FILTER_KEY` once and written on `pick`.
- `function activeFilter(stored: string, workspaces: Workspace[]): string` (Story 7): `stored` when it names one of `workspaces`, else `""`.
- `function WorkspaceFilter(props: { dense?: boolean }): ReactElement | null` (Story 7), in `fleet/WorkspaceFilter.tsx`: the fleet's toggle group over the shown workspaces and "All", null with fewer than two, `dense` as for the status parts; the fleet panel draws it only in the phone layout.

**The desktop chrome (Stories 8 to 10):**

- `function FleetLinks(props: { dense?: boolean }): ReactElement` (Story 8), in `panels/fleet/FleetHead.tsx`: the flows, settings and new-session links `FleetHead` draws today, which `FleetHead` (without `dense`) and the top bar both draw.
- `function TopBar(): ReactElement` (Story 8), in `desktop/TopBar.tsx`: `LinkPill`, `HostLabel`, `WorkspaceFilter`, `GroupCounts` and `FleetLinks`, each `dense`; `DesktopShell` draws it, and draws no Head on the fleet route.
- `interface SidebarLine { text: string, tone: "attention" | "docs" | "error" | "muted" }` and `function sidebarLine(session: Session): SidebarLine` (Story 9), in `desktop/Sidebar.tsx`: why it waits (`wait.reason`, then `wait.detail`) while its group is `needs_you`, else its newest flow's `flowChip` in `flowTone`, else `last_message`.
- `DOT` (Story 9), exported from `panels/fleet/SessionRow.tsx` (today a private constant): the group dot's colour classes, which the sidebar's rows share.
- `function Sidebar(props: { current: number | undefined }): ReactElement` (Story 9): the groups over the shown workspaces under `activeFilter`, Paused folded; `DesktopShell` draws it on every route but the fleet while `useDesktop().sidebar`.
- `function inspectorTab(order: PanelId[], tabs: PanelId[], middle: PanelId, chosen: string | undefined): PanelId | undefined` (Story 10), in `desktop/inspector.ts`, spec §6 and §10: `order` every tab panel in tab order (from the panel registry), `tabs` those that apply to the session (`sessionTabs`), in the same order. `chosen` when it is in `tabs` and is not `middle`; else, when `chosen` is in `order`, the first tab after it in `order`, wrapping, that is in `tabs` and is not `middle`; else the first tab in `tabs` that is not `middle`; undefined when none.
- `function Inspector(props: { session: Session, middle: PanelId }): ReactElement | null` (Story 10), in `desktop/Inspector.tsx`: its tab strip pinned above its scroller, outside it, and the Body in an `elementPane` over its scroller with a `null` head, drawn once the scroller has mounted, in a `Boundary` keyed by `resetKey`; `DesktopShell` draws it on session routes in the `desk3` layout while `useDesktop().inspector`.

**webshot (Story 11):**

- The devices `desktop` (1440×900) and `desktop-narrow` (1100×800) (Story 11), in `tools/webshot/frame.go`: bare viewports, scale 1, no touch, a desktop Chrome user agent; `-device` accepts them; the phone-only `add-to-home` screen is skipped on them.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): `desktop/DesktopShell.tsx` as Story 6 leaves it (Stories 8, 9, 10, each adding its part); `app/shell.tsx` as Story 6 leaves it (Story 7); `internal/web/README.md` as Story 1 leaves it (Story 12); `USAGE.md` as Story 2 leaves it (Story 12).

**Design stage:** /flow:draft session 0265c609-b08c-4edd-b71c-68cf40763e2a, 2026-11-21T10:11:14Z to 2026-11-21T11:26:35Z UTC: $44.61 logged, $44.80 with estimated thinking; 6133 plan lines, $7.27 per 1,000 lines; skeleton review $2.30; risk review $5.79; budget $47; forms: full 4 stories $13.17, brief 8 stories $23.34; loom flow roster 0e25f6995f89.

## Stories

- **Story 1** — The web layer accepts the listener's own loopback origins beside the configured one.

  Add `LocalOrigin` to core and make `guard`'s rule a set: the configured origin plus the bound address's local origin, `http://loom.localhost:<port>` for a `127.0.0.1` or `::1` listener, never bare `localhost` or the IP literal (spec §21–§23). The Host check (421) and the write's Origin check (403) accept any member and nothing else; a configured local origin counts once; push keeps the configured origin as its subject. Pin both sides of every guard: `loom.localhost` accepted as host and origin on a `127.0.0.1` and a `::1` listener, another port's refused, bare `localhost` and the IP literal refused, nothing local on a `127.0.0.2` listener, the configured origin unchanged, the `"localhost is not 127.0.0.1"` case refused on every listener. Extend the e2e web test to pair and stream over the local origin. Amend `internal/web/CLAUDE.md`'s guard rule in place (198 bytes free) and describe the set in `internal/web/README.md`'s "Every request".

  - **Developer:** intermediate-dev
  - **Files:** `core/web.go`, `core/web_test.go`, `core/README.md`, `internal/web/server.go`, `internal/web/web.go`, `internal/web/web_test.go`, `internal/web/README.md`, `internal/web/CLAUDE.md`, `e2e/web_test.go`, `e2e/CLAUDE.md`
  - **Interfaces:** exposes `core.LocalOrigin` and the origin set's behaviour; uses `parseOrigin`, `hostKey`, `core.ValidateWebListen`, `Layer.rule`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./core/ ./internal/web/ && CGO_ENABLED=0 go test -tags e2e -timeout 30m ./e2e/ -run TestE2E_Web'`
  - **Review:** deep
  - **Form:** full (auth: the Host and Origin checks front every request, and a wrong member opens the API to another origin)
  - **Reading:** spec Findings (the origin rule), §21–§23, Testing (Go, e2e); `internal/web/server.go:1-198`, `internal/web/web.go:255-322`, `internal/web/web_test.go:80-230`, `internal/web/push.go:195-220`, `core/web.go:1-60`, `core/web.go:200-280`, `internal/web/README.md:36-60`, `internal/web/CLAUDE.md:11`, `e2e/web_test.go:130-308`

- **Story 2** — `loom web open` pairs or opens the browser on localhost, and `loom web status` shows the local origin.

  Add `loom web open [--name <device>]` (spec §24–§25): refused while the web is off or not serving, and when the listener has no local origin, minting nothing; otherwise it calls `PairDevice` and opens the pairing link, or, when the model refuses because the name is paired (`core.IsDeviceExists`, added here), opens the plain local URL and says so; the URL is always printed. The opener runs through the `openBrowser` seam with the platform's command, its stdio on `/dev/null`, and a 5-second wait that never kills it; a missing or failing opener is reported and the command still exits 0. `loom web status` gains the Local row. `internal/cli/CLAUDE.md` is at its byte budget, so no rule is added there. Document the command in `USAGE.md`'s web section, and list it in `internal/cli/README.md` and `docs/ARCHITECTURE.md`. Name every test of the story `TestWebOpen_…` (`TestIsDeviceExists` in core).

  - **Developer:** intermediate-dev
  - **Files:** `core/web.go`, `core/web_test.go`, `internal/cli/web.go`, `internal/cli/web_open.go`, `internal/cli/open_unix.go`, `internal/cli/open_unix_test.go`, `internal/cli/open_windows.go`, `internal/cli/web_test.go`, `internal/cli/README.md`, `docs/ARCHITECTURE.md`, `USAGE.md`
  - **Interfaces:** exposes `loom web open`, `core.IsDeviceExists`, `openBrowser`, `openerCommand`, `waitOpener`, `openerWait`, the Local status row and the pairing link's form; uses `core.LocalOrigin` (Story 1), `PairDevice`, `ValidateDeviceName`, `ErrDeviceExists`, `IsNoDevice`, `formatPairingCode`, `withDaemon`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./core/ -run IsDeviceExists && CGO_ENABLED=0 go test -timeout 30m ./internal/cli/ -run TestWeb && GOOS=windows CGO_ENABLED=0 go build ./...'`
  - **Review:** deep
  - **Form:** full (an outside call: the opener's start, its 5-second wait and the rule that it is never killed)
  - **Reading:** spec §24–§25, Errors, Testing (Go); `internal/cli/web.go:1-270`, `internal/cli/web_test.go:1-100`, `internal/cli/web_test.go:379-540`, `core/web.go:255-280`, `core/web.go:365-400`, `USAGE.md:840-885`

- **Story 3** — The app consumes a pairing link before its router starts.

  Add the leaf `app/pairHold.ts` (with `normalizeCode` and `CODE_LENGTH` moved to the leaf `panels/pair/code.ts`) and `app/pairLink.ts`, and wire them into `main.tsx`, which imports `app/pairHold.ts` first (spec §26): `adoptNextApp` keeps a browser that arrived by `?ui=next` with no app cookie on the new app (the cookie set before the worker registers); `holdPairLink` takes `#/pair?code=…` (or `#pair?code=…`) out of the address in place before the worker, the router or the stream starts, holding the normalised code only at 8 characters; `watchPairLink` runs `device.pair` with it once, on the first unpaired signal, and a live snapshot drops it unused. A refused code leaves the usual pairing screen with the daemon's words as the command's toast. Pin the order: no request and no route sees the code, the code is used at most once, and a paired browser never posts it.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/pairHold.ts`, `internal/web/ui/src/app/pairLink.ts`, `internal/web/ui/src/app/pairLink.test.ts`, `internal/web/ui/src/panels/pair/code.ts`, `internal/web/ui/src/panels/pair/code.test.ts`, `internal/web/ui/src/panels/pair/Pair.tsx`, `internal/web/ui/src/main.tsx`, `internal/web/ui/src/main.pairing.test.tsx`, `internal/web/ui/src/app/switchApp.ts`, `internal/web/dist/`
  - **Interfaces:** exposes `adoptNextApp`, `holdPairLink`, `watchPairLink`; uses `cookieApp`, `registerWorker`, `normalizeCode`, `CODE_LENGTH`, `onUnpaired`, `runCommand`, `commandById`, `useFleet`, and the pairing link's form (Story 2)
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (auth: a pairing credential in the address, removed before any request and used once)
  - **Reading:** spec §26, Errors, Testing (Vitest); `internal/web/ui/src/main.tsx`, `internal/web/ui/src/panels/pair/Pair.tsx`, `internal/web/ui/src/commands/device.ts:30-70`, `internal/web/ui/src/api/request.ts:1-70`, `internal/web/ui/src/app/commands.ts:1-200`, `internal/web/ui/src/app/routes.tsx:40-60`, `internal/web/ui/src/app/routes.tsx:140-175`, `internal/web/ui/src/store/fleet.ts:100-170`

- **Story 4** — Panels scroll through a `ScrollPane`, and bodies take their session from props.

  Add `app/scroll.tsx` (spec §12) and move the three panels that scroll the window onto it: the conversation's bottom-following, anchoring and kept scroll, the job log's follow, and the flow document's heading jump below the pane's pinned head. With no provider the pane is the window, so the phone behaves exactly as today and the existing tests keep passing, `doc.test.tsx`'s `scrollTo` expectations moving to the two-argument form `windowPane` calls; new tests run each behaviour inside an `elementPane`. `NoSession` takes the session id as a prop (spec §13) and every caller passes its own; `session.test.tsx`'s cases pass it.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/scroll.tsx`, `internal/web/ui/src/app/scroll.test.tsx`, `internal/web/ui/src/app/scrollkit.ts`, `internal/web/ui/src/panels/turns/Conversation.tsx`, `internal/web/ui/src/panels/turns/turns.test.tsx`, `internal/web/ui/src/panels/job/JobBody.tsx`, `internal/web/ui/src/panels/job/job.test.tsx`, `internal/web/ui/src/panels/flow/FlowDocBody.tsx`, `internal/web/ui/src/panels/flow/doc.test.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/session/session.test.tsx`, `internal/web/ui/src/panels/agent/Agent.tsx`, `internal/web/ui/src/panels/changes/index.tsx`, `internal/web/ui/src/panels/flow/FlowBody.tsx`, `internal/web/ui/src/panels/flow/FlowStoryBody.tsx`, `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/mockup/MockupBody.tsx`, `internal/web/ui/src/panels/turns/index.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `ScrollPane`, `windowPane`, `elementPane`, `ScrollPaneProvider`, `useScrollPane`, `NoSession`'s `sessionId` prop; uses `keepScroll`, `keptScroll`, `useIsPending`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (a seam over the window; the phone's scroll behaviour is pinned by the existing tests, and each pane behaviour by a named test)
  - **Reading:** spec Findings (window scrolling, route reads in bodies), §12–§13; `internal/web/ui/src/panels/turns/Conversation.tsx`, `internal/web/ui/src/panels/job/JobBody.tsx:95-130`, `internal/web/ui/src/panels/flow/FlowDocBody.tsx:110-145`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/api/reads.ts` (`keepScroll`, `keptScroll`)

- **Story 5** — A Dialog component, a desktop form of the confirmation sheet, and a placement for the toasts.

  Add shadcn's Dialog on Base UI 1.9, owned in `components/ui/dialog.tsx`, injecting no `<style>` (spec §11, §32 of the foundation spec). `ConfirmSheet` takes a `variant` and draws the same sheet content (title, paragraphs, the optional field, the actions) in the Drawer or a centred Dialog, with `askConfirm`'s API and its replaced-ask rule unchanged; `Toaster` takes a `placement`, top-centre as today or bottom-right. Today's call site in `app/shell.tsx` passes `"drawer"` and `"top"` until Story 6 lifts it.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/components/ui/dialog.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/components/ui/toast.tsx`, `internal/web/ui/src/app/toasts.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `Dialog`, `DialogContent`, `DialogTitle`, `DialogDescription`, `DialogFooter`, `ConfirmVariant`, `ConfirmSheet`'s `variant`, `ToastPlacement`, `Toaster`'s `placement`; uses `askConfirm`, the Drawer
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (an owned shadcn component and two presentational props; the sheet's answer rules keep their tests)
  - **Form:** brief (a shadcn component, a variant and a placement)
  - **Reading:** spec §11; `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/components/ui/drawer.tsx`, `internal/web/ui/src/components/ui/toast.tsx`, `internal/web/ui/src/app/shell.tsx:125-212`

- **Story 6** — The layout hook, the desktop preferences, and the shell switch with the desktop's middle pane.

  Add `app/viewport.ts` and `app/desktopState.ts` (spec §4–§6), split today's frame into `PhoneShell`, and make `AppShell` choose it or the lazy `DesktopShell` live as the window resizes, mounting the one confirmation sheet and toaster above both in the layout's form (spec §11). `DesktopShell` draws the route's Head, the session's tabs, its Strip and its Body in a middle pane that scrolls on its own under a pinned head (an `elementPane` provided to the panel), keeps the conversation's readable measure, draws the full mockup full-window, and leaves pairing and the splash to `PhoneShell`. The middle's Body renders only once its scroller has mounted, so it never reads the window. Every test runs on the phone layout unless it calls `setViewportWidth`. Add the `DesktopShell` stem to `checkLazyViews` so the phone's first load is unchanged. Pin the breakpoints, a resize swapping the shell with the route and an open confirmation kept, and every malformed `loom.desktop` value read as the default.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/viewport.ts`, `internal/web/ui/src/app/viewport.test.ts`, `internal/web/ui/src/app/desktopState.ts`, `internal/web/ui/src/app/desktopState.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/ui/src/test-layout.ts`, `internal/web/ui/src/test-setup.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes `Layout`, `DESK2_MIN`, `DESK3_MIN`, `layoutOf`, `useLayout`, `DESKTOP_KEY`, `DesktopSlot`, `DesktopPrefs`, `readDesktopPrefs`, `DesktopState`, `useDesktop`, `PhoneShell`, `DesktopShell`, `setViewportWidth`, `PanelHead`, the desktop slots, the exported `Boundary`, `TABBED`, `tabOf` and `resetKey`; uses `elementPane`, `ScrollPaneProvider` (Story 4), `ConfirmSheet`'s `variant` and `Toaster`'s `placement` (Story 5), `PanelHost`, `SessionTabs`, `panelFor`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (bounds: a stored value read back from localStorage, and the lazy chunk the budget test pins)
  - **Reading:** spec §4–§6, §9, §11, Errors, Testing (Vitest, Go); `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/routes.tsx:1-140`, `internal/web/ui/src/app/panels.ts`, `internal/web/static_budget_test.go:180-304`, `internal/web/ui/CLAUDE.md` (the lazy-view rules)

- **Story 7** — The status row's parts and the workspace filter become shared pieces.

  Pull `HostLabel`, `LinkPill` and `GroupCounts` out of `StatusRow` into `app/status.tsx`, the phone's footer drawing exactly what it draws today (spec §7). Move the workspace filter out of the fleet panel into `fleet/filter.ts` (`FILTER_KEY`, `useFilter`, `activeFilter`) and `fleet/WorkspaceFilter.tsx`, so the fleet panel, the new-session form and the desktop share one choice, `loom.filter` keeping its classic encoding; the fleet panel draws the filter only in the phone layout (spec §8). The existing fleet, footer and new-session tests keep passing; new tests pin `activeFilter` and that a pick in one place shows in the other.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/status.tsx`, `internal/web/ui/src/app/status.test.tsx`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/fleet/filter.ts`, `internal/web/ui/src/fleet/filter.test.ts`, `internal/web/ui/src/fleet/WorkspaceFilter.tsx`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/panels/fleet/fleet.test.tsx`, `internal/web/ui/src/panels/new/NewSessionBody.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `HostLabel`, `LinkPill`, `GroupCounts` (each with `dense`), `FILTER_KEY` (moved), `readFilter`, `FilterState`, `useFilter`, `activeFilter`, `WorkspaceFilter`; uses `useLayout`, `setViewportWidth` (Story 6), `nextInGroup`, `fleetGroups`, `shownWorkspaces`, `groupSessions`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (moves of existing parts and one small shared store; the existing footer and fleet tests pin their drawing)
  - **Form:** brief (existing parts moved into shared modules, and a store over an existing localStorage key)
  - **Reading:** spec §7–§8; `internal/web/ui/src/app/shell.tsx:330-476`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/panels/new/NewSessionBody.tsx:20-60`, `internal/web/ui/src/fleet/groups.ts`

- **Story 8** — The desktop's top bar.

  Add `desktop/TopBar.tsx` (spec §7) from the shared parts: the link's pill and `loom@host`, the workspace filter, the group counts and the fleet's links, which `FleetHead` and the top bar now share as `FleetLinks`. `DesktopShell` draws the top bar across the window and, on the fleet route, no Head (spec §8). The palette's box comes with the palette in part 2. Tests pin the bar's parts, a count opening the group's next session, and the fleet route drawing no Head on desktop.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/TopBar.tsx`, `internal/web/ui/src/desktop/TopBar.test.tsx`, `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `FleetLinks` (with `dense`), `TopBar`; uses `HostLabel`, `LinkPill`, `GroupCounts`, `WorkspaceFilter`, `readFilter` (Story 7), `DesktopShell`, `PanelHead`, the desktop slots, `setViewportWidth` (Story 6)
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (chrome assembled from shared parts)
  - **Form:** brief (a bar of existing parts)
  - **Reading:** spec §5, §7–§8; `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `docs/superpowers/specs/2026-11-21-web-desktop/frame.html`
  - **Drafts after:** Story 6, Story 7

- **Story 9** — The desktop's fleet sidebar.

  Add `desktop/Sidebar.tsx` (spec §8): the fleet's groups over the shown workspaces under the shared filter, in compact two-line rows (the group's dot, the title and its age; then `sidebarLine`'s why-it-waits, flow chip or last message in its tone), the session on screen highlighted, each row opening `sessionHref`, Paused folded closed. `DesktopShell` draws it beside the middle on every route but the fleet, while `useDesktop().sidebar`. Tests pin `sidebarLine`'s three cases and its order, the highlight, the fold, the filter, and the sidebar's absence on the fleet route.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/Sidebar.tsx`, `internal/web/ui/src/desktop/Sidebar.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `SidebarLine`, `sidebarLine`, `Sidebar`, `DOT` (exported); uses `useFilter`, `activeFilter`, `readFilter` (Story 7), `useDesktop`, `DesktopShell`, `setViewportWidth` (Story 6), `useFleet`, `Collapsible`, `CollapsibleTrigger`, `CollapsibleContent`, `GROUPS`, `groupSessions`, `shownWorkspaces`, `sessionHref`, `ago`, `flowChip`, `flowTone`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (a list drawn from existing fleet helpers; its one decision, `sidebarLine`, is pinned by named tests)
  - **Form:** brief (rows from existing helpers)
  - **Reading:** spec §8; `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/fleet/groups.ts`, `internal/web/ui/src/fleet/links.ts`, `docs/superpowers/specs/2026-11-21-web-desktop/frame.html`
  - **Drafts after:** Story 8

- **Story 10** — The desktop's inspector.

  Add `desktop/inspector.ts` and `desktop/Inspector.tsx` (spec §10): a tab strip over the session's tab panels minus the middle's lit tab, its choice the first slot's (`useDesktop().setTab`), falling as `inspectorTab` says, drawing the chosen panel's Body through `PanelHost` with the session's props, in its own error boundary and its own `elementPane`. `DesktopShell` draws it on session routes, in the `desk3` layout, while `useDesktop().inspector`. Tests pin every branch of `inspectorTab`, the choice surviving a reload through `loom.desktop`, a throwing inspector panel leaving the middle drawn, and the inspector's absence at `desk2` and off session routes.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/inspector.ts`, `internal/web/ui/src/desktop/inspector.test.ts`, `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/desktop/Inspector.tsx`, `internal/web/ui/src/desktop/Inspector.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `inspectorTab`, `Inspector`; uses `useDesktop`, `useLayout`, `DesktopShell`, `Boundary`, `tabOf`, `resetKey`, `setViewportWidth` (Story 6), `elementPane`, `ScrollPaneProvider` (Story 4), `sessionTabs`, `PanelHost`, the panel registry (`app/panels.ts`) for the tab order
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (a panel host in a second pane; `inspectorTab`'s branches are pinned by named tests)
  - **Reading:** spec §6, §10, Errors; `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/shell.tsx` (`PanelHost`, `SessionTabs`, `tabOf`)
  - **Drafts after:** Story 9

- **Story 11** — webshot renders every screen at two desktop sizes, and CI runs them.

  Add the `desktop` (1440×900) and `desktop-narrow` (1100×800) devices to webshot (spec Testing): bare viewports at scale 1 with no touch emulation, no safe-area insets and a desktop Chrome user agent, rendering every screen of the new app but the phone-only `add-to-home`, with the same failure rules (a console error, a CSP violation, an unanswered route). `.github/workflows/web.yml` renders the new app at the phone and both desktop devices. Tests pin the device table and the skipped screen.

  - **Developer:** intermediate-dev
  - **Files:** `tools/webshot/frame.go`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `.github/workflows/web.yml`, `tools/CLAUDE.md`
  - **Interfaces:** exposes the `desktop` and `desktop-narrow` devices; uses the frame Stories 6 to 10 draw
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -out "$d"'`
  - **Review:** light (a test tool's device modes and a CI step)
  - **Form:** brief (two device entries, a skipped screen and a workflow step)
  - **Reading:** spec Testing (webshot); `tools/webshot/frame.go:1-60`, `tools/webshot/main.go:40-130`, `tools/webshot/main.go:180-230`, `tools/webshot/main.go:440-530`, `tools/webshot/webshot_test.go`, `.github/workflows/web.yml`
  - **Drafts after:** Story 10

- **Story 12** — The rules and the procedures for the desktop.

  Add to `internal/web/ui/CLAUDE.md` the rules part 1 introduces (spec §27): a panel scrolls through `ScrollPane`, never the window; a body reads its session from its props, never the route; desktop chrome (top bar, sidebar, inspector) is not a panel. Add to `docs/claude/web-layer.md` how the desktop composes the panels, the dev loop at desktop widths (webshot's devices) and the desktop checklist (spec Testing), and to `USAGE.md` a paragraph on the desktop layout. Each rule names the test that enforces it or says Convention, and the CLAUDE.md lint passes.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md`, `internal/web/README.md`
  - **Interfaces:** none
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd'`
  - **Review:** light (docs)
  - **Form:** brief (docs)
  - **Reading:** spec §27, Testing (the desktop checklist); `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md:840-885`
  - **Drafts after:** Story 1, Story 2, Story 3, Story 4, Story 5, Story 6, Story 7, Story 8, Story 9, Story 10, Story 11

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->

## Story 1: The web layer accepts the listener's own loopback origins beside the configured one.

- **Developer:** intermediate-dev
- **Files:** `core/web.go`, `core/web_test.go`, `core/README.md`, `internal/web/server.go`, `internal/web/web.go`, `internal/web/web_test.go`, `internal/web/README.md`, `internal/web/CLAUDE.md`, `e2e/web_test.go`, `e2e/CLAUDE.md`
- **Interfaces:** exposes `core.LocalOrigin` and the origin set's behaviour; uses `parseOrigin`, `hostKey`, `core.ValidateWebListen`, `Layer.rule`
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./core/ ./internal/web/ && CGO_ENABLED=0 go test -tags e2e -timeout 30m ./e2e/ -run TestE2E_Web'`
- **Review:** deep
- **Form:** full (auth: the Host and Origin checks front every request, and a wrong member opens the API to another origin)

The web layer's single origin rule becomes a set: the configured origin plus the bound address's local origin, `http://loom.localhost:<port>`, which only a `127.0.0.1` or `[::1]` listener has. `core.LocalOrigin` builds it; `guard`'s Host check (421) and a write's Origin check (403) accept a member of the set and nothing else, so bare `localhost` and the IP literal stay refused on every listener (a cookie set there would reach every server the browser reaches on that name, on any port). The `loom.localhost` cookie is not private to loom either: cookies ignore the port, so a hostile listener on another loopback port that steers the browser to `loom.localhost:<its port>` receives it; that is accepted on a single-user machine, and the README says so. Push is untouched: it keeps the configured origin as its subject. No route, wire or Core request changes. The code lands in four tasks: `LocalOrigin` in core, the set in `internal/web`, an e2e test that pairs and streams over the local origin, and the docs (`internal/web/CLAUDE.md`'s guard rule in the 198 bytes it has free, with the residual risk in `internal/web/README.md`, both READMEs and `e2e/CLAUDE.md`).

### Task 1.1: `core.LocalOrigin`

**Files:**
- Modify: `core/web.go` (insert before the comment `// ValidateWebOrigin checks the origin the phone reaches loom at:`)
- Test: `core/web_test.go` (insert after `TestValidateWebListen`)

- [ ] **Step 1: Write the failing test**

Insert after `TestValidateWebListen`, that is, after its last two lines

```go
	assert.ErrorContains(t, ValidateWebListen("localhost:7420"), "loopback IP literal", "a host name is refused for what it is")
}
```

this test:

```go
func TestLocalOrigin(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:7420":  "http://loom.localhost:7420",
		"[::1]:7420":      "http://loom.localhost:7420",
		"127.0.0.1:1":     "http://loom.localhost:1",
		"[::1]:65535":     "http://loom.localhost:65535",
		"127.0.0.1:80":    "http://loom.localhost:80",
		"127.0.0.1:12345": "http://loom.localhost:12345",
	} {
		got, err := LocalOrigin(addr)
		require.NoError(t, err, addr)
		assert.Equal(t, want, got, addr)
	}
	for _, addr := range []string{
		"", "localhost:7420", "loom.localhost:7420", "0.0.0.0:7420", ":7420", "192.168.1.5:7420", "[::]:7420",
		"127.0.0.1", "127.0.0.1:", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:-1", "127.0.0.1:07420",
		"[0:0:0:0:0:0:0:1]:7420", " 127.0.0.1:7420", "127.0.0.1:7420 ", "unix:/tmp/loom.sock",
	} {
		got, err := LocalOrigin(addr)
		require.Error(t, err, "%q", addr)
		assert.Empty(t, got, "%q", addr)
		assert.ErrorContains(t, err, "loopback IP literal", "%q", addr)
	}
	// Another loopback address is a listener with no local origin, and the
	// error says what to do.
	for _, addr := range []string{"127.0.0.2:7420", "127.1.2.3:7420", "[::ffff:127.0.0.1]:7420"} {
		got, err := LocalOrigin(addr)
		require.Error(t, err, addr)
		assert.Empty(t, got, addr)
		assert.ErrorContains(t, err, "has no local origin", addr)
		assert.ErrorContains(t, err, "listen on 127.0.0.1 or ::1", addr)
	}
}
```

Fails without: `TestLocalOrigin`: `LocalOrigin` returning the bare IP literal or `localhost` for `127.0.0.1:7420` fails the first loop; accepting any `IsLoopback` address (dropping the `127.0.0.1`/`::1` check) fails the `127.0.0.2:7420` row (a nil error); dropping the `validPort` term fails the `127.0.0.1:0`, `:65536` and `:07420` rows; dropping the `ip.String() != host` term fails the `[0:0:0:0:0:0:0:1]:7420` row.

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./core/ -run TestLocalOrigin`
Expected: FAIL to build: `undefined: LocalOrigin`.

- [ ] **Step 3: Implement**

Insert in `core/web.go`, just before the comment that begins `// ValidateWebOrigin checks the origin the phone reaches loom at:` (after `ValidateWebListen`'s closing brace):

```go
// LocalOrigin is the origin a browser on the daemon's machine reaches a bound
// listener at: http://loom.localhost:<port> for addr 127.0.0.1:<port> or
// [::1]:<port>, written as net.JoinHostPort writes them (what a listener's
// Addr gives), with a port from 1 to 65535. Browsers resolve every
// *.localhost name to loopback and keep a cookie for one apart from every
// other name (though not from another port on that name). Bare localhost and the IP
// literal are never the local origin: a device cookie set there would reach
// every server the browser reaches on that name, on any port, and the model
// ties a token to no origin. Another loopback IP literal has no local origin
// and the error says to listen on 127.0.0.1 or ::1; any other address is an
// error too.
func LocalOrigin(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("address %q: want a loopback IP literal and a port from 1 to 65535, like 127.0.0.1:7420", addr)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.String() != host || !ip.IsLoopback() || !validPort(port) {
		return "", fmt.Errorf("address %q: want a loopback IP literal and a port from 1 to 65535, like 127.0.0.1:7420", addr)
	}
	if ip != netip.AddrFrom4([4]byte{127, 0, 0, 1}) && ip != netip.IPv6Loopback() {
		return "", fmt.Errorf("address %q has no local origin: listen on 127.0.0.1 or ::1", addr)
	}
	return "http://loom.localhost:" + port, nil
}

```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestLocalOrigin|TestValidateWebListen'`
Expected: `ok  	github.com/aidan-bailey/loom/core`

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core): LocalOrigin, a loopback listener's loom.localhost origin" -- core/web.go core/web_test.go
```

### Task 1.2: The origin set in `guard` and `bind`

**Files:**
- Modify: `internal/web/server.go` (a new type and function before `guard`, `guard`'s comment, `bind`)
- Modify: `internal/web/web.go:38-40` (the `rule` field)
- Test: `internal/web/web_test.go` (`guardedBy`, one `l.rule.Store`, three new tests)

`guard`'s body does not change: `l.rule.Load()` now returns a pointer to the set, and `allowsHost` and `allowsOrigin` are the set's methods, so the 421 and 403 decisions are the same two calls.

- [ ] **Step 1: Write the failing tests**

In `internal/web/web_test.go`, replace the head of `guardedBy`, from its comment through the `l.rule.Store` line. Today:

```go
// guardedBy is guard around a handler that answers 200 with the number of
// body bytes it read, or 413 with the reader's error. The layer enforces
// origin, as a bound listener's does.
func guardedBy(t *testing.T, origin string) http.Handler {
	t.Helper()
	rule, err := parseOrigin(origin)
	require.NoError(t, err)
	l := &Layer{}
	l.rule.Store(&rule)
	return l.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
```

becomes (the `return l.guard(...` line and the rest of the function stay):

```go
// guardedBy is guard around a handler that answers 200 with the number of
// body bytes it read, or 413 with the reader's error. The layer enforces
// origin alone, as a listener whose address has no local origin does.
func guardedBy(t *testing.T, origin string) http.Handler {
	t.Helper()
	return guardedOn(t, origin, "")
}

// guardedOn is guardedBy for a listener bound to addr: the layer enforces
// origin and addr's local origin, the set bind makes.
func guardedOn(t *testing.T, origin, addr string) http.Handler {
	t.Helper()
	rule, err := parseOrigin(origin)
	require.NoError(t, err)
	origins := originsFor(rule, addr)
	l := &Layer{}
	l.rule.Store(&origins)
	return l.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
```

In the stream test further down, the three lines

```go
	rule, err := parseOrigin(srv.URL)
	require.NoError(t, err)
	l.rule.Store(&rule)
```

become

```go
	rule, err := parseOrigin(srv.URL)
	require.NoError(t, err)
	l.rule.Store(&originSet{rule})
```

Insert these tests between `TestGuard_TheHostMustBeTheOrigins` and `TestGuard_AnUnsafeMethodMustCarryTheOrigin` (before the line `func TestGuard_AnUnsafeMethodMustCarryTheOrigin(t *testing.T) {`):

```go
// A listener bound to 127.0.0.1 or ::1 answers to its own loom.localhost name
// beside the configured origin, as Host and as a write's Origin; no other
// name for loopback, no other port and no other listener is its.
func TestGuard_TheLocalOriginIsTheListenersOwn(t *testing.T) {
	const local = "http://loom.localhost:7420"
	for _, tc := range []struct {
		name    string
		origin  string // the configured origin
		listen  string // the address the listener is bound to
		method  string
		host    string
		origins []string // the Origin headers, none when nil
		status  int
	}{
		{"the local host on a 127.0.0.1 listener", guardOrigin, "127.0.0.1:7420", http.MethodGet, "loom.localhost:7420", nil, http.StatusOK},
		{"the local host in any case", guardOrigin, "127.0.0.1:7420", http.MethodGet, "LOOM.Localhost:7420", nil, http.StatusOK},
		{"the local host on a ::1 listener", guardOrigin, "[::1]:7420", http.MethodGet, "loom.localhost:7420", nil, http.StatusOK},
		{"the local host on another port", guardOrigin, "127.0.0.1:7420", http.MethodGet, "loom.localhost:7421", nil, http.StatusMisdirectedRequest},
		{"the local host with no port", guardOrigin, "127.0.0.1:7420", http.MethodGet, "loom.localhost", nil, http.StatusMisdirectedRequest},
		{"a name under the local host", guardOrigin, "127.0.0.1:7420", http.MethodGet, "app.loom.localhost:7420", nil, http.StatusMisdirectedRequest},
		{"bare localhost", guardOrigin, "127.0.0.1:7420", http.MethodGet, "localhost:7420", nil, http.StatusMisdirectedRequest},
		{"the 127.0.0.1 literal", guardOrigin, "127.0.0.1:7420", http.MethodGet, "127.0.0.1:7420", nil, http.StatusMisdirectedRequest},
		{"the ::1 literal on a ::1 listener", guardOrigin, "[::1]:7420", http.MethodGet, "[::1]:7420", nil, http.StatusMisdirectedRequest},
		{"localhost is not 127.0.0.1 on a 127.0.0.1 listener", "http://127.0.0.1:7420", "127.0.0.1:7420", http.MethodGet, "localhost:7420", nil, http.StatusMisdirectedRequest},
		{"localhost is not 127.0.0.1 on a ::1 listener", "http://127.0.0.1:7420", "[::1]:7420", http.MethodGet, "localhost:7420", nil, http.StatusMisdirectedRequest},
		{"a 127.0.0.2 listener has no local host", guardOrigin, "127.0.0.2:7420", http.MethodGet, "loom.localhost:7420", nil, http.StatusMisdirectedRequest},
		{"the configured host beside the local one", guardOrigin, "127.0.0.1:7420", http.MethodGet, "loom.example.com", nil, http.StatusOK},
		{"the configured host on a 127.0.0.2 listener", guardOrigin, "127.0.0.2:7420", http.MethodGet, "loom.example.com", nil, http.StatusOK},
		{"a configured local origin", local, "127.0.0.1:7420", http.MethodGet, "loom.localhost:7420", nil, http.StatusOK},

		{"a post from the local origin", guardOrigin, "127.0.0.1:7420", http.MethodPost, "loom.localhost:7420", []string{local}, http.StatusOK},
		{"a post from the local origin on a ::1 listener", guardOrigin, "[::1]:7420", http.MethodPost, "loom.localhost:7420", []string{local}, http.StatusOK},
		{"a post from the local origin in another case", guardOrigin, "127.0.0.1:7420", http.MethodPost, "loom.localhost:7420", []string{"HTTP://LOOM.LOCALHOST:7420"}, http.StatusOK},
		{"a post from the configured origin beside it", guardOrigin, "127.0.0.1:7420", http.MethodPost, "loom.example.com", []string{guardOrigin}, http.StatusOK},
		{"a post from the local origin's other port", guardOrigin, "127.0.0.1:7420", http.MethodPost, "loom.localhost:7420", []string{"http://loom.localhost:7421"}, http.StatusForbidden},
		{"a post from the local origin's https form", guardOrigin, "127.0.0.1:7420", http.MethodPost, "loom.localhost:7420", []string{"https://loom.localhost:7420"}, http.StatusForbidden},
		{"a post from bare localhost", guardOrigin, "127.0.0.1:7420", http.MethodPost, "loom.localhost:7420", []string{"http://localhost:7420"}, http.StatusForbidden},
		{"a post from the 127.0.0.1 literal", guardOrigin, "127.0.0.1:7420", http.MethodPost, "loom.localhost:7420", []string{"http://127.0.0.1:7420"}, http.StatusForbidden},
		{"a post from the ::1 literal", guardOrigin, "[::1]:7420", http.MethodPost, "loom.localhost:7420", []string{"http://[::1]:7420"}, http.StatusForbidden},
		{"a post from the local origin with a path", guardOrigin, "127.0.0.1:7420", http.MethodPost, "loom.localhost:7420", []string{local + "/x"}, http.StatusForbidden},
		{"two local Origin headers", guardOrigin, "127.0.0.1:7420", http.MethodPost, "loom.localhost:7420", []string{local, local}, http.StatusForbidden},
		{"a post from the local origin on a 127.0.0.2 listener", guardOrigin, "127.0.0.2:7420", http.MethodPost, "loom.example.com", []string{local}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendGuarded(guardedOn(t, tc.origin, tc.listen), tc.method, tc.host, tc.origins, nil)
			assert.Equal(t, tc.status, rec.Code)
		})
	}
}

func TestOriginsFor_AConfiguredLocalOriginCountsOnce(t *testing.T) {
	configured := func(origin string) originRule {
		t.Helper()
		rule, err := parseOrigin(origin)
		require.NoError(t, err)
		return rule
	}
	assert.Len(t, originsFor(configured(guardOrigin), "127.0.0.1:7420"), 2, "the configured origin and the local one")
	assert.Len(t, originsFor(configured("http://loom.localhost:7420"), "127.0.0.1:7420"), 1, "a configured origin that is the local one")
	assert.Len(t, originsFor(configured("http://loom.localhost:7420"), "[::1]:7420"), 1, "the same on a ::1 listener")
	assert.Len(t, originsFor(configured("http://loom.localhost:7421"), "127.0.0.1:7420"), 2, "another port's origin is another origin")
	assert.Len(t, originsFor(configured(guardOrigin), "127.0.0.2:7420"), 1, "no local origin on another loopback address")
	assert.Len(t, originsFor(configured(guardOrigin), ""), 1, "no local origin without an address")
}
```

Insert this test before `func TestBind_ListensOnLoopbackOnly(t *testing.T) {`:

```go
// bind makes the set from the address it bound: a real listener answers to its
// own loom.localhost:<port> and the configured origin, not to bare localhost
// or the IP literal, and a rebind to another port moves the local origin.
func TestBind_ServesItsLocalOriginBesideTheConfiguredOne(t *testing.T) {
	l := &Layer{}
	t.Cleanup(l.shutListener)
	// status sends one request to the listener at listen with host as its
	// Host and origin as its Origin (none when ""). No cookie goes along, so
	// a request guard lets through is answered 401 by the route.
	status := func(listen, method, host, origin string) int {
		t.Helper()
		path := "/api/v1/launch-defaults?workspace=1"
		if method == http.MethodPost {
			path = "/api/v1/sessions/1/kill"
		}
		req, err := http.NewRequest(method, "http://"+listen+path, nil)
		require.NoError(t, err)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	listen := freeLoopbackAddr(t)
	_, port, err := net.SplitHostPort(listen)
	require.NoError(t, err)
	_, err = l.bind(webKey{listen: listen, origin: guardOrigin})
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, status(listen, http.MethodGet, "loom.localhost:"+port, ""), "the local host passes guard")
	assert.Equal(t, http.StatusUnauthorized, status(listen, http.MethodGet, "loom.example.com", ""), "so does the configured one")
	assert.Equal(t, http.StatusMisdirectedRequest, status(listen, http.MethodGet, "localhost:"+port, ""), "bare localhost does not")
	assert.Equal(t, http.StatusMisdirectedRequest, status(listen, http.MethodGet, listen, ""), "nor does the IP literal")
	assert.Equal(t, http.StatusUnauthorized, status(listen, http.MethodPost, "loom.localhost:"+port, "http://loom.localhost:"+port), "a write from the local origin passes guard")
	assert.Equal(t, http.StatusForbidden, status(listen, http.MethodPost, "loom.localhost:"+port, "http://localhost:"+port), "a write from bare localhost does not")
	assert.Equal(t, http.StatusForbidden, status(listen, http.MethodPost, "loom.localhost:"+port, "http://"+listen), "nor one from the IP literal")

	// A rebind to another port builds the set again from the new address.
	again := freeLoopbackAddr(t)
	_, port2, err := net.SplitHostPort(again)
	require.NoError(t, err)
	_, err = l.bind(webKey{listen: again, origin: guardOrigin})
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, status(again, http.MethodGet, "loom.localhost:"+port2, ""), "the new port's local host passes")
	assert.Equal(t, http.StatusMisdirectedRequest, status(again, http.MethodGet, "loom.localhost:"+port, ""), "the old port's does not")
}
```

Fails without: `TestGuard_TheLocalOriginIsTheListenersOwn`: `originsFor` returning only the configured origin fails every `loom.localhost` row that expects 200; adding bare `localhost` or the IP literal as members fails the `localhost`, `127.0.0.1`, `[::1]` and "localhost is not 127.0.0.1" rows; building the member without its port fails the "another port" and "no port" rows; ignoring `core.LocalOrigin`'s error (a member for `127.0.0.2`) fails the "a 127.0.0.2 listener" rows.
Fails without: `TestOriginsFor_AConfiguredLocalOriginCountsOnce`: the `rule != configured` check in `originsFor` removed, so a configured local origin makes a set of 2.
Fails without: `TestBind_ServesItsLocalOriginBesideTheConfiguredOne`: `bind` storing the configured origin alone (the `loom.localhost` rows answer 421 and 403), or building the set from `key.origin` rather than the address it bound (the rebind row for the old port stays 401).

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestGuard_TheLocalOriginIsTheListenersOwn|TestOriginsFor|TestBind_ServesItsLocalOrigin'`
Expected: FAIL to build: `undefined: originsFor` and `undefined: originSet`.

- [ ] **Step 3: Implement**

In `internal/web/web.go`, replace

```go
	// rule is the origin rule of the listener now running, which guard
	// enforces; nil while none runs, and then guard refuses every request.
	rule atomic.Pointer[originRule]
```

with

```go
	// rule is the origin set of the listener now running (its configured
	// origin and its local one), which guard enforces; nil while none runs,
	// and then guard refuses every request.
	rule atomic.Pointer[originSet]
```

In `internal/web/server.go`, insert before the comment `// guard is the door every route stands behind. It sets the security headers` (after `allowsOrigin`'s closing brace):

```go
// originSet is the origins a listener serves: the configured one and, when the
// listener's address has one, its local origin (core.LocalOrigin). A Host or
// an Origin header is accepted when any member accepts it and refused
// otherwise, so a member added here is a name every route answers to.
type originSet []originRule

// originsFor is the set for a listener bound to addr: configured, and the
// local origin http://loom.localhost:<port> when addr is 127.0.0.1:<port> or
// [::1]:<port>, which is not added twice when configured is it. Another
// address has no local origin, and its set is the configured origin alone;
// bind has checked the address by then.
func originsFor(configured originRule, addr string) originSet {
	set := originSet{configured}
	local, err := core.LocalOrigin(addr)
	if err != nil {
		return set
	}
	if rule, err := parseOrigin(local); err == nil && rule != configured {
		set = append(set, rule)
	}
	return set
}

// allowsHost reports whether a request's Host header names a member's host.
func (s originSet) allowsHost(host string) bool {
	for _, rule := range s {
		if rule.allowsHost(host) {
			return true
		}
	}
	return false
}

// allowsOrigin reports whether an Origin header is a member itself.
func (s originSet) allowsOrigin(header string) bool {
	for _, rule := range s {
		if rule.allowsOrigin(header) {
			return true
		}
	}
	return false
}

```

Replace the head of `guard`'s comment. Today:

```go
// guard is the door every route stands behind. It sets the security headers
// on every answer, refuses a Host that is not the origin's (421: DNS
// rebinding), refuses any request but a GET or HEAD without the origin's own
// Origin header (403: a cross-site post), and caps the body at maxBody. With
// no origin rule (no listener runs) it refuses everything. It hands next the
```

becomes

```go
// guard is the door every route stands behind. It sets the security headers
// on every answer, refuses a Host that is not a member of the origin set's
// (421: DNS rebinding), refuses any request but a GET or HEAD without exactly
// one Origin header that is a member (403: a cross-site post), and caps the
// body at maxBody. With no origin set (no listener runs) it refuses
// everything. It hands next the
```

In `bind`, after the listen, build the set from the address bound. Today:

```go
	ln, err := net.Listen("tcp", key.listen)
	if err != nil {
		return "", err
	}
	l.srv = &http.Server{
```

becomes

```go
	ln, err := net.Listen("tcp", key.listen)
	if err != nil {
		return "", err
	}
	origins := originsFor(rule, ln.Addr().String())
	l.srv = &http.Server{
```

and further down, today:

```go
	l.rule.Store(&rule)
	go l.serve(l.srv, ln)
```

becomes

```go
	l.rule.Store(&origins)
	go l.serve(l.srv, ln)
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./internal/web/`
Expected: `ok  	github.com/aidan-bailey/loom/internal/web`

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): guard accepts the listener's local origin beside the configured one" -- internal/web/server.go internal/web/web.go internal/web/web_test.go
```

### Task 1.3: e2e over the local origin

**Files:**
- Modify: `e2e/web_test.go` (four helpers extracted from `TestE2E_Web_SessionLifecycleOverHTTP`, which calls them; one new test)

The lifecycle test's command closure, its wait for the listener, its pairing and its stream open are the steps the new test repeats, so they become helpers both call.

- [ ] **Step 1: Write the new test and the helpers**

Insert after `freeLoopbackAddr` (before the comment `// \`loom web enable\` starts the layer in the sandbox's daemon, \`loom web`):

```go
// webCommand runs `loom web args…` in the sandbox, failing the test on an
// error, and returns what it printed.
func webCommand(t *testing.T, sb *devsandbox.Sandbox) func(args ...string) string {
	return func(args ...string) string {
		t.Helper()
		out, err := runLoom(t, sb, append([]string{sb.LoomBin(), "web"}, args...)...)
		require.NoError(t, err, out)
		return out
	}
}

// awaitListening waits until something accepts connections at addr.
func awaitListening(t *testing.T, addr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, uiTimeout, 100*time.Millisecond, "the daemon never listened on %s", addr)
}

// pairWith posts code to /api/v1/pair and keeps the device cookie it sets as
// the client's token.
func (c *webClient) pairWith(code string) {
	c.t.Helper()
	status, body, header := c.round(c.request(http.MethodPost, "/api/v1/pair", map[string]string{"code": code}))
	require.Equal(c.t, http.StatusOK, status, string(body))
	for _, ck := range (&http.Response{Header: header}).Cookies() {
		if ck.Name == webCookie {
			c.token = ck.Value
		}
	}
	require.NotEmpty(c.t, c.token, "pairing sets the device cookie")
}

// stream opens the paired device's event stream and returns its events; the
// stream closes with the test. It has no timeout of its own: it stays open.
func (c *webClient) stream() <-chan sseEvent {
	c.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c.t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/api/v1/stream", nil)
	require.NoError(c.t, err)
	req.AddCookie(&http.Cookie{Name: webCookie, Value: c.token})
	resp, err := (&http.Client{Transport: c.client.Transport}).Do(req)
	require.NoError(c.t, err)
	c.t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(c.t, http.StatusOK, resp.StatusCode)
	return readStream(ctx, resp.Body)
}
```

In `TestE2E_Web_SessionLifecycleOverHTTP`, replace the closure

```go
	loomWeb := func(args ...string) string {
		t.Helper()
		out, err := runLoom(t, sb, append([]string{sb.LoomBin(), "web"}, args...)...)
		require.NoError(t, err, out)
		return out
	}
```

with

```go
	loomWeb := webCommand(t, sb)
```

replace the wait

```go
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, uiTimeout, 100*time.Millisecond, "the daemon never listened on %s", addr)
```

with

```go
	awaitListening(t, addr)
```

replace the pairing lines

```go
	status, body, header := web.round(web.request(http.MethodPost, "/api/v1/pair", map[string]string{"code": code}))
	require.Equal(t, http.StatusOK, status, string(body))
	for _, c := range (&http.Response{Header: header}).Cookies() {
		if c.Name == webCookie {
			web.token = c.Value
		}
	}
	require.NotEmpty(t, web.token, "pairing sets the device cookie")
```

with

```go
	web.pairWith(code)
```

replace the stream lines

```go
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	streamReq, err := http.NewRequestWithContext(ctx, http.MethodGet, web.origin+"/api/v1/stream", nil)
	require.NoError(t, err)
	streamReq.AddCookie(&http.Cookie{Name: webCookie, Value: web.token})
	resp, err := (&http.Client{}).Do(streamReq)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	events := readStream(ctx, resp.Body)
	snapshot := awaitEvent(t, events, "snapshot", nil)
```

with

```go
	events := web.stream()
	snapshot := awaitEvent(t, events, "snapshot", nil)
```

and, because `body` was declared by the pairing line that is gone, change the create call from `=` to `:=` (`status` already exists, `body` is new). Today:

```go
	status, body = web.call(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": ws, "title": "webkeeper"})
```

becomes

```go
	status, body := web.call(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": ws, "title": "webkeeper"})
```

Append at the end of `e2e/web_test.go`:

```go
// The listener answers to its own local origin, http://loom.localhost:<port>,
// beside the configured one: a device pairs over it with a `loom web pair`
// code, its stream opens and an action passes the guard, while bare localhost
// and the IP literal are refused as Host and as Origin. The client dials the
// listener's address whatever the name, so the test does not depend on how
// the machine's resolver treats *.localhost.
func TestE2E_Web_PairsAndStreamsOverTheLocalOrigin(t *testing.T) {
	sb := newSandbox(t, "")
	addr := freeLoopbackAddr(t)
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	web := &webClient{t: t, origin: "http://loom.localhost:" + port, client: &http.Client{Timeout: 45 * time.Second, Transport: transport}}
	loomWeb := webCommand(t, sb)

	// The configured origin is the tunnel's; the local one comes with the listener.
	loomWeb("enable", "--listen", addr, "--origin", "https://loom.example.com")
	awaitListening(t, addr)

	// Before pairing: the guard lets the local host through to the route's
	// own 401, and still refuses the names that are not the listener's.
	status, _, _ := web.round(web.request(http.MethodGet, "/api/v1/launch-defaults?workspace=1", nil))
	assert.Equal(t, http.StatusUnauthorized, status, "the local origin reaches the route, which needs the cookie")
	for _, host := range []string{"localhost:" + port, addr} {
		req := web.request(http.MethodGet, "/api/v1/launch-defaults?workspace=1", nil)
		req.Host = host
		status, _, _ = web.round(req)
		assert.Equal(t, http.StatusMisdirectedRequest, status, "the Host %s is not the listener's", host)
	}

	// Pairing over the local origin, then the stream.
	code := pairingCode.FindString(loomWeb("pair", "--name", "e2e-local"))
	require.NotEmpty(t, code, "loom web pair printed no code")
	web.pairWith(code)
	events := web.stream()
	snapshot := awaitEvent(t, events, "snapshot", nil)
	assert.Contains(t, snapshot.Data, devsandbox.WorkspaceName, "the snapshot lists the sandbox's workspace")

	// A write from the local origin passes the guard and reaches the model,
	// which has no such session; one from bare localhost or the IP literal
	// does not pass the guard, cookie or not.
	status, body := web.call(http.MethodPost, "/api/v1/sessions/1/kill", nil)
	assert.Equal(t, http.StatusNotFound, status, "the model says there is no session 1: %s", body)
	for _, origin := range []string{"http://localhost:" + port, "http://" + addr} {
		req := web.request(http.MethodPost, "/api/v1/sessions/1/kill", nil)
		req.Header.Set("Origin", origin)
		req.AddCookie(&http.Cookie{Name: webCookie, Value: web.token})
		status, _, _ = web.round(req)
		assert.Equal(t, http.StatusForbidden, status, "the Origin %s is not the listener's", origin)
	}
}
```

Fails without: `TestE2E_Web_PairsAndStreamsOverTheLocalOrigin`: `bind` storing the configured origin alone (the first request answers 421 instead of 401, and the pairing post is refused with 403), or adding bare `localhost` or the IP literal to the set (the two refusal loops see 401 and 404 instead of 421 and 403).

- [ ] **Step 2: Run it to see it fail**

Run this on a tree where Task 1.2's `bind` change is reverted, or read the failure it would print if the guard held the configured origin alone:
`CGO_ENABLED=0 go test -tags e2e -timeout 30m ./e2e/ -run TestE2E_Web_PairsAndStreamsOverTheLocalOrigin`
Expected without Task 1.2: FAIL at the first assertion, `Not equal: expected: 401 actual: 421`.

- [ ] **Step 3: Implement**

Nothing more to write: Task 1.2 is the implementation, and the helpers above are in place.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test -tags e2e -timeout 30m ./e2e/ -run TestE2E_Web`
Expected: `ok  	github.com/aidan-bailey/loom/e2e` with both `TestE2E_Web_SessionLifecycleOverHTTP` and `TestE2E_Web_PairsAndStreamsOverTheLocalOrigin` run (add `-v` to see them).

- [ ] **Step 5: Commit**

```bash
git commit -m "test(e2e): pair and stream over the listener's local origin" -- e2e/web_test.go
```

### Task 1.4: The docs

**Files:**
- Modify: `internal/web/CLAUDE.md:11` (the guard rule, 185 of the 198 free bytes)
- Modify: `internal/web/README.md` ("Every request", items 2 and 3)
- Modify: `core/README.md:43`
- Modify: `e2e/CLAUDE.md:7`

- [ ] **Step 1: Write the docs**

`internal/web/CLAUDE.md`, line 11, three spans. First, replace

```
`guard` answers 421 unless the Host header is the origin's host (DNS rebinding), 403 for any method but GET and HEAD without the origin's own `Origin` header (a cross-site post),
```

with

```
`guard` answers 421 unless the Host header is the configured origin's host or the listener's own `loom.localhost:<port>` (DNS rebinding; never bare `localhost` or an IP, whose cookie reaches every local port), 403 for any method but GET and HEAD without one of those origins as its `Origin` header (a cross-site post),
```

Second, replace

```
**Enforced** by `TestGuard_TheHostMustBeTheOrigins`, `TestGuard_AnUnsafeMethodMustCarryTheOrigin`,
```

with

```
**Enforced** by `TestGuard_TheHostMustBeTheOrigins`, `TestGuard_TheLocalOriginIsTheListenersOwn`, `TestGuard_AnUnsafeMethodMustCarryTheOrigin`,
```

The rest of the line is unchanged. The file grows from 19,802 to 19,987 bytes, under its 20,000, and the line stays under 1,200 bytes (1,115 with its newline).

`internal/web/README.md`, "Every request": replace items 2 and 3

```
2. The `Host` must be the origin's host (default port dropped, any case), else 421.
3. A method other than GET or HEAD must carry exactly one `Origin` header equal to the origin, else 403.
```

with

```
2. The `Host` must be the host of a member of the origin set (default port dropped, any case), else 421. The set is the configured origin and, when the listener is bound to `127.0.0.1` or `[::1]`, its local origin `http://loom.localhost:<port>` (`core.LocalOrigin`; browsers resolve `*.localhost` to loopback). Bare `localhost` and the IP literal are never members: a device cookie set on them would reach every server the browser reaches there, on any port, and a token is tied to no origin. A listener on another loopback address (`127.0.0.2`) has no local origin. The `loom.localhost` cookie is host-only, so no service at bare `localhost` or a loopback literal receives it, but cookies ignore the port: a hostile listener on another loopback port (another OS user's server, a forward to a compromised machine, a container's published port) that steers the browser to `loom.localhost:<its port>` receives it. That is accepted on a single-user machine. The local origin grants nothing the tunnel's does not: every route but pairing still needs the device cookie, and each origin holds its own.
3. A method other than GET or HEAD must carry exactly one `Origin` header equal to a member of the set, else 403.
```

`core/README.md`, line 43: replace

```
`NormalizeQuickReplies`), the requests `ConfigureWeb`
```

with

```
`NormalizeQuickReplies`), `LocalOrigin` (a `127.0.0.1` or `::1` listener's `http://loom.localhost:<port>`, the origin its guard adds to the configured one), the requests `ConfigureWeb`
```

`e2e/CLAUDE.md`, line 7: replace the line's ending

```
and `loom web revoke` ending the stream and the cookie.
```

with

```
and `loom web revoke` ending the stream and the cookie; `TestE2E_Web_PairsAndStreamsOverTheLocalOrigin` pairs and streams over the listener's `http://loom.localhost:<port>` and sees bare `localhost` and the IP literal refused.
```

- [ ] **Step 2: Run the checks to see them fail**

Not applicable: the lint passes before the docs change. Confirm the starting headroom with `go run ./tools/claudemd -v` (`internal/web/CLAUDE.md` 19802 of 20000).

- [ ] **Step 3: Implement**

The edits in Step 1 are the implementation.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd -v`
Expected: the test passes; the verbose listing shows `internal/web/CLAUDE.md` at most 20000 bytes and no line over 1200 bytes, and `TestGuard_TheLocalOriginIsTheListenersOwn` resolves.

- [ ] **Step 5: Commit**

```bash
git commit -m "docs(web): the origin set in the guard rule, the READMEs and the e2e notes" -- internal/web/CLAUDE.md internal/web/README.md core/README.md e2e/CLAUDE.md
```

## Story 2: `loom web open` pairs or opens the browser on localhost, and `loom web status` shows the local origin.

- **Developer:** intermediate-dev
- **Files:** `core/web.go`, `core/web_test.go`, `internal/cli/web.go`, `internal/cli/web_open.go`, `internal/cli/open_unix.go`, `internal/cli/open_unix_test.go`, `internal/cli/open_windows.go`, `internal/cli/web_test.go`, `internal/cli/README.md`, `docs/ARCHITECTURE.md`, `USAGE.md`
- **Interfaces:** exposes `loom web open`, `core.IsDeviceExists`, `openBrowser`, `openerCommand`, `waitOpener`, `openerWait`, the Local status row and the pairing link's form; uses `core.LocalOrigin` (Story 1), `PairDevice`, `ValidateDeviceName`, `ErrDeviceExists`, `IsNoDevice`, `formatPairingCode`, `withDaemon`
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./core/ -run IsDeviceExists && CGO_ENABLED=0 go test -timeout 30m ./internal/cli/ -run TestWeb && GOOS=windows CGO_ENABLED=0 go build ./...'`
- **Review:** deep
- **Form:** full (an outside call: the opener's start, its 5-second wait and the rule that it is never killed)

`loom web open [--name <device>]` (name `desktop` by default) opens this machine's browser on the listener's local origin, `http://loom.localhost:<port>/?ui=next`. It is refused, minting nothing, while the web is off (quoting the error when `web.json` is left alone, since `enable` refuses then), not serving, or serving an address with no local origin (`core.LocalOrigin`, Story 1); only then does it call `PairDevice`. A code minted goes into the link's fragment (`#/pair?code=…`, the form Story 3's app reads); a refusal that `core.IsDeviceExists` reads as "the name is paired" opens the plain URL and says so; any other `PairDevice` error is the model's own and passes through. The URL is always printed, before the opener runs. The opener (`openBrowser`, a seam tests replace) starts the platform's command (`xdg-open`, `open` or `rundll32 url.dll,FileProtocolHandler`, stdio on `/dev/null`, in its own session on Unix so a Ctrl-C reaches only `loom`) and waits at most 5 seconds: an exit status other than 0 within them is a failure, an opener still running after them counts as opened and is left running, never killed. A missing or failing opener is reported and the command still exits 0. `loom web status` gains a `Local:` row. Five tasks: `core.IsDeviceExists`, the opener, the command, the status row, the docs. `internal/cli/CLAUDE.md` is at its byte budget, so it gains no rule. Every test of the command is named `TestWebOpen_…` so the story's `-run TestWeb` runs it; `TestIsDeviceExists` is core's.

### Task 2.1: `core.IsDeviceExists`

**Files:**
- Modify: `core/web.go` (insert after `IsNoDevice`)
- Test: `core/web_test.go` (insert after `TestIsNoDevice`)

A model error crosses the rpc wire as text under `CodeError`, which `WireError.Is` does not map back to `ErrDeviceExists`, so `errors.Is` cannot see the sentinel in a client. `IsNoDevice` solves the same for `ErrNoDevice`; this is its twin.

- [ ] **Step 1: Write the failing test**

Insert after `TestIsNoDevice`, that is, after its last line

```go
	assert.False(t, IsNoDevice(nil, "ghost"))
}
```

this test:

```go
func TestIsDeviceExists(t *testing.T) {
	m, _ := webHostModel(t)
	webHostPair(t, m, "iPhone")
	_, local := m.PairDevice("iPhone")
	require.ErrorIs(t, local, ErrDeviceExists)
	// What a client holds once the error has crossed the rpc wire: its text
	// under CodeError, which WireError.Is does not map back to ErrDeviceExists.
	wire := &WireError{Code: CodeError, Message: local.Error()}
	require.NotErrorIs(t, wire, ErrDeviceExists, "control: the sentinel is lost on the wire")

	assert.True(t, IsDeviceExists(ErrDeviceExists, "iPhone"), "the sentinel")
	assert.True(t, IsDeviceExists(local, "iPhone"), "the model's own error")
	assert.True(t, IsDeviceExists(wire, "iPhone"), "the text the model gives for that name")
	assert.False(t, IsDeviceExists(wire, "iPad"), "another name's text")
	assert.False(t, IsDeviceExists(&WireError{Code: CodeError, Message: ErrDeviceExists.Error()}, "iPhone"), "the sentinel's text alone names no device")
	assert.False(t, IsDeviceExists(&WireError{Code: CodeError, Message: "write web.json: " + local.Error()}, "iPhone"), "text that merely contains it")
	assert.False(t, IsDeviceExists(ErrWebOff, "iPhone"), "another refusal of PairDevice")
	assert.False(t, IsDeviceExists(errors.New("web.json is left alone"), "iPhone"), "an unrelated error")
	assert.False(t, IsDeviceExists(nil, "iPhone"))
}
```

Fails without: `TestIsDeviceExists`: `IsDeviceExists` matching only `errors.Is` fails the `wire` row; matching any text that contains the sentinel's fails the "merely contains it" and "sentinel's text alone" rows; ignoring `name` fails the `iPad` row.

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./core/ -run TestIsDeviceExists`
Expected: FAIL to build: `undefined: IsDeviceExists`.

- [ ] **Step 3: Implement**

Insert in `core/web.go`, after `IsNoDevice`, that is, after its last lines

```go
	return errors.Is(err, ErrNoDevice) || err.Error() == noDeviceError(name).Error()
}
```

this function:

```go
// IsDeviceExists reports whether err says a device is called name already:
// ErrDeviceExists itself, or the text the model gives for that name
// (fmt.Errorf("%w: %q", ErrDeviceExists, name), what PairDevice and the host's
// redemption answer with). A client sees the second, as IsNoDevice's does:
// the error crossed the rpc wire as text under CodeError, which WireError.Is
// does not map back to ErrDeviceExists, so errors.Is cannot see the sentinel.
// It matches any error wrapping ErrDeviceExists, whatever name that error
// carries; only the text comparison is exact to name, so over the wire
// another name's error is not this name's.
func IsDeviceExists(err error, name string) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrDeviceExists) || err.Error() == fmt.Errorf("%w: %q", ErrDeviceExists, name).Error()
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestIsDeviceExists|TestIsNoDevice'`
Expected: `ok  	github.com/aidan-bailey/loom/core`

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core): IsDeviceExists, the refusal PairDevice gives a paired name, as a client reads it" -- core/web.go core/web_test.go
```

### Task 2.2: The opener

**Files:**
- Create: `internal/cli/web_open.go`
- Create: `internal/cli/open_unix.go`
- Create: `internal/cli/open_windows.go`
- Test: `internal/cli/web_test.go` (two imports and tests appended at the end)
- Create: `internal/cli/open_unix_test.go`

`openBrowser` is used by Task 2.3's command; until then only its tests call it, which is why its tests name it.

The opener is started in its own session (`Setsid`) on Unix, so a Ctrl-C at the terminal during `waitOpener`'s wait reaches `loom` alone and not the opener or a browser it started (spec §25: an opener is never killed).

- [ ] **Step 1: Write the failing tests**

In `internal/cli/web_test.go`, add `"os/exec"` and `"runtime"` to the import block, keeping it sorted. Today:

```go
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
```

becomes

```go
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
```

Append at the end of the file:

```go
// startedOpener starts sh running script (its arguments after the script, as
// $1 and on) as a stand-in for an opener, and kills it when the test ends.
func startedOpener(t *testing.T, script string, args ...string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("these tests stand an opener in with sh")
	}
	cmd := exec.Command("sh", append([]string{"-c", script, "sh"}, args...)...)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

func TestWebOpen_WaitOpener_AnOpenerThatExitsCleanlyIsOpened(t *testing.T) {
	cmd := startedOpener(t, "exit 0")

	assert.NoError(t, waitOpener(cmd, 5*time.Second))
}

func TestWebOpen_WaitOpener_AQuickFailureNamesItsExitStatus(t *testing.T) {
	cmd := startedOpener(t, "exit 3")

	err := waitOpener(cmd, 5*time.Second)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 3")
}

func TestWebOpen_WaitOpener_AnOpenerStillRunningIsOpenedAndLeftRunning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran-on")
	cmd := startedOpener(t, `sleep 1; touch "$1"`, marker)

	err := waitOpener(cmd, 50*time.Millisecond)

	require.NoError(t, err, "still running at the limit counts as opened")
	assert.NoFileExists(t, marker, "waitOpener returned at its limit, with the opener still running")
	assert.Eventually(t, func() bool {
		_, statErr := os.Stat(marker)
		return statErr == nil
	}, 10*time.Second, 20*time.Millisecond, "the opener was left running: it went on to finish")
}

func TestWebOpen_OpenerCommandIsThePlatformsOwn(t *testing.T) {
	const link = "http://loom.localhost:7420/?ui=next#/pair?code=K7QM2XPA"
	want := []string{"xdg-open", link}
	switch runtime.GOOS {
	case "darwin":
		want = []string{"open", link}
	case "windows":
		want = []string{"rundll32", "url.dll,FileProtocolHandler", link}
	}

	cmd := openerCommand(link)

	assert.Equal(t, want, cmd.Args)
	assert.Nil(t, cmd.Stdin, "a console browser must not take the terminal")
	assert.Nil(t, cmd.Stdout)
	assert.Nil(t, cmd.Stderr)
}

func TestWebOpen_AMissingOpenerIsAnError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := startBrowser("http://loom.localhost:7420/?ui=next")

	require.Error(t, err, "no opener on the PATH is a start error, never a silent success")
}

func TestWebOpen_TheOpenerWaitIsFiveSeconds(t *testing.T) {
	assert.Equal(t, 5*time.Second, openerWait)
}
```

Fails without: `TestWebOpen_WaitOpener_AQuickFailureNamesItsExitStatus`: `waitOpener` returning nil for any exit fails it; `TestWebOpen_WaitOpener_AnOpenerStillRunningIsOpenedAndLeftRunning`: killing the opener at the limit leaves the marker unwritten (the `Eventually` times out), and waiting for it past the limit makes the marker exist at the `NoFileExists` check; `TestWebOpen_WaitOpener_AnOpenerThatExitsCleanlyIsOpened`: reading a zero exit as a failure fails it; `TestWebOpen_OpenerCommandIsThePlatformsOwn`: an opener with `Stdout` or `Stderr` set (to the terminal) fails the `Nil` rows; `TestWebOpen_AMissingOpenerIsAnError`: `startBrowser` discarding `Start`'s error fails it; `TestWebOpen_TheOpenerWaitIsFiveSeconds`: another limit.

Create `internal/cli/open_unix_test.go`:

```go
//go:build !windows

package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebOpen_OpenerCommandStartsInItsOwnSession(t *testing.T) {
	cmd := openerCommand("http://loom.localhost:7420/?ui=next")

	require.NotNil(t, cmd.SysProcAttr, "the opener must not share the CLI's process group")
	assert.True(t, cmd.SysProcAttr.Setsid, "a Ctrl-C during the wait must not reach the opener or its browser")
}
```

Fails without: `TestWebOpen_OpenerCommandStartsInItsOwnSession`: an `openerCommand` that leaves `SysProcAttr` nil or `Setsid` false fails it.

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestWebOpen_(WaitOpener|OpenerCommand|AMissingOpener|TheOpenerWait)'`
Expected: FAIL to build: `undefined: waitOpener`, `undefined: openerCommand`, `undefined: startBrowser`, `undefined: openerWait`.

- [ ] **Step 3: Implement**

Create `internal/cli/web_open.go`:

```go
package cli

import (
	"fmt"
	"os/exec"
	"time"
)

// openerWait is how long openBrowser waits for the opener. Some openers stay
// up while the browser starts, so one still running then counts as opened.
const openerWait = 5 * time.Second

// openBrowser opens link in the machine's browser. It is a variable so tests
// replace it: the real one starts the user's browser.
var openBrowser = startBrowser

// startBrowser starts the platform's opener (openerCommand) on link and waits
// for it as waitOpener says. The opener's stdio is /dev/null.
func startBrowser(link string) error {
	cmd := openerCommand(link)
	if err := cmd.Start(); err != nil {
		return err
	}
	return waitOpener(cmd, openerWait)
}

// waitOpener waits up to limit for the started opener cmd: nil when it exits 0
// within limit, an error naming its exit status when it exits otherwise, and
// nil when it is still running at limit, when it is left running and never
// killed (killing an opener that hands the URL to a running browser can take
// the browser with it).
func waitOpener(cmd *exec.Cmd, limit time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("the opener failed: %w", err)
		}
		return nil
	case <-timer.C:
		return nil
	}
}
```

Create `internal/cli/open_unix.go`:

```go
//go:build !windows

package cli

import (
	"os/exec"
	"runtime"
	"syscall"
)

// openerCommand is the command that opens link in the user's browser: open on
// macOS, xdg-open on Linux and the BSDs. Its Stdin, Stdout and Stderr stay
// nil (/dev/null), so a console browser never takes the terminal. It runs in
// its own session (Setsid), so a Ctrl-C in the CLI's foreground process group
// during the wait does not kill it or a browser it started.
func openerCommand(link string) *exec.Cmd {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	cmd := exec.Command(name, link)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
```

Create `internal/cli/open_windows.go`:

```go
//go:build windows

package cli

import "os/exec"

// openerCommand is the command that opens link in the user's browser. Its
// Stdin, Stdout and Stderr stay nil.
func openerCommand(link string) *exec.Cmd {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestWebOpen_(WaitOpener|OpenerCommand|AMissingOpener|TheOpenerWait)' && GOOS=windows CGO_ENABLED=0 go build ./...`
Expected: `ok  	github.com/aidan-bailey/loom/internal/cli` and a silent cross-build.

- [ ] **Step 5: Commit**

```bash
git add -N internal/cli/web_open.go internal/cli/open_unix.go internal/cli/open_windows.go internal/cli/open_unix_test.go
git commit -m "feat(cli): the browser opener behind loom web open, with its 5-second wait" -- internal/cli/web_open.go internal/cli/open_unix.go internal/cli/open_windows.go internal/cli/open_unix_test.go internal/cli/web_test.go
```

### Task 2.3: `loom web open`

**Files:**
- Modify: `internal/cli/web_open.go` (imports, the command)
- Modify: `internal/cli/web.go:35-42` (registration), `internal/cli/web.go:33` (the command's `Short`)
- Test: `internal/cli/web_test.go` (appended)

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/web_test.go`:

```go
// fakeBrowser replaces openBrowser for the test with one that records the
// URLs it is asked to open and answers err. A test of `loom web open` must
// call it: the real opener starts the developer's browser.
func fakeBrowser(t *testing.T, err error) *[]string {
	t.Helper()
	var opened []string
	prev := openBrowser
	openBrowser = func(link string) error {
		opened = append(opened, link)
		return err
	}
	t.Cleanup(func() { openBrowser = prev })
	return &opened
}

// servingWeb has the daemon enable the web on listen for the tunnel's origin
// and its layer report that it serves addr.
func servingWeb(t *testing.T, d *testDaemon, listen, addr string) {
	t.Helper()
	require.NoError(t, d.loop.ConfigureWeb(listen, "https://loom.example.com"))
	d.loop.WebServing(addr, nil)
}

// openLink matches the link `loom web open` opens for a new name, and holds
// its code.
var openLink = regexp.MustCompile(`^http://loom\.localhost:7420/\?ui=next#/pair\?code=([` + codeAlphabet + `]{8})$`)

func TestWebOpen_RefusedWhileTheWebIsOff(t *testing.T) {
	webDaemon(t)
	opened := fakeBrowser(t, nil)

	out, _, err := runWeb(t, "open")

	require.EqualError(t, err, "The web app is off; turn it on with loom web enable.")
	assert.Empty(t, *opened)
	assert.NotContains(t, out, "Pairing code")
}

func TestWebOpen_WebJSONLeftAloneAnswersItsErrorNotTheOffMessage(t *testing.T) {
	isolateWorkspaces(t)
	dir, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, core.WebFile), []byte("not json"), 0o600))
	d := serveDaemon(t, "true")
	opened := fakeBrowser(t, nil)
	w := webView(t, d)
	require.Empty(t, w.Listen, "web.json left alone keeps the web off")
	require.NotEmpty(t, w.Error)

	out, _, err := runWeb(t, "open")

	require.EqualError(t, err, "The web app is off, and its settings file is left alone ("+w.Error+"); see loom web status.")
	assert.NotContains(t, err.Error(), "turn it on with loom web enable", "enable refuses while web.json is left alone")
	assert.NotContains(t, out, "Pairing code", "nothing is minted")
	assert.Empty(t, *opened)
}

func TestWebOpen_RefusedUntilTheLayerServes(t *testing.T) {
	d := webDaemon(t)
	opened := fakeBrowser(t, nil)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))

	out, _, err := runWeb(t, "open")
	require.EqualError(t, err, "The web app is not serving yet; see loom web status.")
	assert.NotContains(t, out, "Pairing code")

	d.loop.WebServing("", errors.New("listen tcp 127.0.0.1:7420: bind: address already in use"))
	out, _, err = runWeb(t, "open")
	require.EqualError(t, err, "The web app is not serving (listen tcp 127.0.0.1:7420: bind: address already in use); see loom web status.")
	assert.NotContains(t, out, "Pairing code")
	assert.Empty(t, *opened)
}

func TestWebOpen_RefusedWhenTheListenerHasNoLocalOrigin(t *testing.T) {
	d := webDaemon(t)
	opened := fakeBrowser(t, nil)
	servingWeb(t, d, "127.0.0.1:7420", "127.0.0.2:7420")
	_, noLocal := core.LocalOrigin("127.0.0.2:7420")
	require.Error(t, noLocal, "control: another loopback address has no local origin")

	out, _, err := runWeb(t, "open")

	require.EqualError(t, err, "The web app has no local origin: "+noLocal.Error()+".")
	assert.NotContains(t, out, "Pairing code", "no code is minted for a link nothing can open")
	assert.Empty(t, *opened)
}

func TestWebOpen_PairsANewNameAndOpensItsLink(t *testing.T) {
	d := webDaemon(t)
	opened := fakeBrowser(t, nil)
	servingWeb(t, d, "127.0.0.1:7420", "127.0.0.1:7420")
	webDevice(t, d, "iPhone") // another device paired does not make desktop's name taken

	out, _, err := runWeb(t, "open")

	require.NoError(t, err)
	assert.Regexp(t, `Pairing code for desktop expires at \d\d:\d\d:\d\d\.`, out)
	require.Len(t, *opened, 1)
	m := openLink.FindStringSubmatch((*opened)[0])
	require.NotNil(t, m, (*opened)[0])
	assert.Contains(t, out, "Opening "+(*opened)[0], "the URL is printed")
	name, token, err := d.loop.RedeemPairing(m[1])
	require.NoError(t, err, "the link's code is the daemon's")
	assert.Equal(t, "desktop", name, "the default name")
	assert.NotEmpty(t, token)
	assert.Len(t, webView(t, d).Devices, 2)
}

func TestWebOpen_NamesTheDeviceAndTrimsIt(t *testing.T) {
	d := webDaemon(t)
	opened := fakeBrowser(t, nil)
	servingWeb(t, d, "127.0.0.1:7420", "127.0.0.1:7420")

	out, _, err := runWeb(t, "open", "--name", "  Work PC ")

	require.NoError(t, err)
	assert.Contains(t, out, "Pairing code for Work PC expires at")
	require.Len(t, *opened, 1)
	m := openLink.FindStringSubmatch((*opened)[0])
	require.NotNil(t, m, (*opened)[0])
	name, _, err := d.loop.RedeemPairing(m[1])
	require.NoError(t, err)
	assert.Equal(t, "Work PC", name)
}

func TestWebOpen_AnIPv6ListenerHasTheSameLocalOrigin(t *testing.T) {
	d := webDaemon(t)
	opened := fakeBrowser(t, nil)
	servingWeb(t, d, "[::1]:7420", "[::1]:7420")

	_, _, err := runWeb(t, "open")

	require.NoError(t, err)
	require.Len(t, *opened, 1)
	assert.Regexp(t, openLink, (*opened)[0])
}

func TestWebOpen_APairedNameOpensThePlainURL(t *testing.T) {
	d := webDaemon(t)
	opened := fakeBrowser(t, nil)
	servingWeb(t, d, "127.0.0.1:7420", "127.0.0.1:7420")
	webDevice(t, d, "desktop")

	for _, args := range [][]string{{"open"}, {"open", "--name", "  desktop "}} {
		*opened = nil
		out, _, err := runWeb(t, args...)

		require.NoError(t, err, args)
		assert.Contains(t, out, "desktop is already paired. If this browser is not, pass --name or run loom web revoke desktop.", args)
		assert.NotContains(t, out, "Pairing code", args)
		assert.Contains(t, out, "Opening http://loom.localhost:7420/?ui=next\n", "the URL is printed: %v", args)
		assert.Equal(t, []string{"http://loom.localhost:7420/?ui=next"}, *opened, args)
	}
	assert.Len(t, webView(t, d).Devices, 1, "nothing was paired")
}

func TestWebOpen_ABadNameIsTheModelsRefusal(t *testing.T) {
	d := webDaemon(t)
	opened := fakeBrowser(t, nil)
	servingWeb(t, d, "127.0.0.1:7420", "127.0.0.1:7420")
	_, badErr := core.ValidateDeviceName("   ")
	require.Error(t, badErr, "the model refuses a name of spaces")

	_, _, err := runWeb(t, "open", "--name", "   ")

	assert.EqualError(t, err, badErr.Error(), "the model's refusal, not a paired name")
	assert.Empty(t, *opened)
}

func TestWebOpen_SaysWhatItOpensBeforeTheOpenerRuns(t *testing.T) {
	d := webDaemon(t)
	servingWeb(t, d, "127.0.0.1:7420", "127.0.0.1:7420")
	cmd := newWebCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"open"})
	var seenByOpener string
	prev := openBrowser
	openBrowser = func(string) error {
		seenByOpener = out.String()
		return nil
	}
	t.Cleanup(func() { openBrowser = prev })

	require.NoError(t, cmd.Execute())

	assert.Contains(t, seenByOpener, "Pairing code for desktop expires at")
	assert.Contains(t, seenByOpener, "Opening http://loom.localhost:7420/?ui=next#/pair?code=", "the opener may block for its 5 seconds: the URL is printed first")
}

func TestWebOpen_AnOpenerFailureIsReportedAndTheCommandSucceeds(t *testing.T) {
	d := webDaemon(t)
	opened := fakeBrowser(t, errors.New(`exec: "xdg-open": executable file not found in $PATH`))
	servingWeb(t, d, "127.0.0.1:7420", "127.0.0.1:7420")

	out, _, err := runWeb(t, "open")

	require.NoError(t, err, "a browser that does not open is not a failed command")
	assert.Contains(t, out, `Could not open a browser (exec: "xdg-open": executable file not found in $PATH); open the URL above yourself.`)
	require.Len(t, *opened, 1)
	assert.Contains(t, out, "Opening "+(*opened)[0], "the URL is printed for the user to open")
	m := openLink.FindStringSubmatch((*opened)[0])
	require.NotNil(t, m, (*opened)[0])
	_, _, err = d.loop.RedeemPairing(m[1])
	assert.NoError(t, err, "the code that was minted stays good")
}
```

Fails without: `TestWebOpen_RefusedWhileTheWebIsOff`, `TestWebOpen_RefusedUntilTheLayerServes`, `TestWebOpen_RefusedWhenTheListenerHasNoLocalOrigin`: any of the three checks in `webLocalOrigin` removed or reordered answers another message (`EqualError`), or mints a code first (the `Pairing code` line appears); `TestWebOpen_WebJSONLeftAloneAnswersItsErrorNotTheOffMessage`: dropping the `w.Listen == "" && w.Error != ""` case answers the "off" message (the `EqualError` and the `NotContains` fail); `TestWebOpen_PairsANewNameAndOpensItsLink`: a link without `?ui=next`, without `#/pair?code=`, or built from the tunnel's origin fails `openLink`; treating any paired device as the name's makes the `iPhone` fixture open the plain URL; `TestWebOpen_NamesTheDeviceAndTrimsIt`: passing the untrimmed name fails the `Work PC` line; `TestWebOpen_AnIPv6ListenerHasTheSameLocalOrigin`: building `<local>` from the IP literal fails `openLink`; `TestWebOpen_APairedNameOpensThePlainURL`: not calling `core.IsDeviceExists` (the refusal is returned as an error), or calling it with the untrimmed name (the second invocation returns the error), or adding the code to a paired name's link; `TestWebOpen_ABadNameIsTheModelsRefusal`: an `IsDeviceExists` that is true for any error opens a link; `TestWebOpen_SaysWhatItOpensBeforeTheOpenerRuns`: printing after `openBrowser` returns leaves `seenByOpener` empty; `TestWebOpen_AnOpenerFailureIsReportedAndTheCommandSucceeds`: returning the opener's error from the command fails `NoError`, and dropping the message fails the `Contains`.

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run TestWebOpen_`
Expected: FAIL: the `open` argument is `unknown command "open" for "web"` in every command test (an error where `NoError` was required, or a different `EqualError` text).

- [ ] **Step 3: Implement**

Append to `internal/cli/web_open.go`, and extend its import block. Today:

```go
import (
	"fmt"
	"os/exec"
	"time"
)
```

becomes

```go
import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/spf13/cobra"
)
```

and at the end of the file:

```go
// defaultDesktopName is what `open` calls this browser when --name is not
// given.
const defaultDesktopName = "desktop"

func newWebOpenCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "open [--name <device>]",
		Short: "Open the web app in this machine's browser, pairing it first when the name is new",
		Long: "Open the web app in this machine's browser at the listener's local origin, " +
			"http://loom.localhost:<port>, with no tunnel. For a name that is not paired yet " +
			"(desktop, or --name) it makes a pairing code and opens the link that carries it; " +
			"for a paired name it opens the app itself and says so. The URL is always printed. " +
			"The web app must be on and serving (loom web enable), on 127.0.0.1 or ::1. If no " +
			"browser opens (no display, over SSH) the URL is left for you to open, and a code " +
			"it made works for ten minutes.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			out := c.OutOrStdout()
			return withDaemon(c, func(d *client) error {
				local, err := webLocalOrigin(d.Web())
				if err != nil {
					return err
				}
				device := strings.TrimSpace(name)
				link := local + "/?ui=next"
				p, err := d.PairDevice(device)
				switch {
				case err == nil:
					fmt.Fprintf(out, "Pairing code for %s expires at %s.\n", device, p.Expires.Local().Format("15:04:05"))
					link += "#/pair?code=" + p.Code
				case core.IsDeviceExists(err, device):
					fmt.Fprintf(out, "%s is already paired. If this browser is not, pass --name or run loom web revoke %s.\n", device, device)
				default:
					return err
				}
				fmt.Fprintf(out, "Opening %s\n", link)
				if err := openBrowser(link); err != nil {
					fmt.Fprintf(out, "Could not open a browser (%v); open the URL above yourself.\n", err)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", defaultDesktopName, "Name for this browser's device")
	return cmd
}

// webLocalOrigin is the origin `open` builds its link on, from the web state
// the command joined to: the listener's local origin (core.LocalOrigin of the
// address it serves). Nothing is minted before it is known, so the refusals
// say why in the order the user fixes them: the web is off (and, when web.json
// is left alone, why, since `loom web enable` refuses then), it is not serving
// (its error, when the layer has one), or its address has no local origin.
func webLocalOrigin(w core.WebView) (string, error) {
	switch {
	case w.Listen == "" && w.Error != "":
		return "", fmt.Errorf("The web app is off, and its settings file is left alone (%s); see loom web status.", w.Error)
	case w.Listen == "":
		return "", errors.New("The web app is off; turn it on with loom web enable.")
	case !w.Serving && w.Error != "":
		return "", fmt.Errorf("The web app is not serving (%s); see loom web status.", w.Error)
	case !w.Serving:
		return "", errors.New("The web app is not serving yet; see loom web status.")
	}
	local, err := core.LocalOrigin(w.Addr)
	if err != nil {
		return "", fmt.Errorf("The web app has no local origin: %w.", err)
	}
	return local, nil
}
```

In `internal/cli/web.go`, register the command. Today:

```go
		newWebPairCmd(),
		newWebDevicesCmd(),
```

becomes

```go
		newWebPairCmd(),
		newWebOpenCmd(),
		newWebDevicesCmd(),
```

and the command's `Short`, today:

```go
		Short: "Serve loom's fleet to a paired phone: enable, disable, pair, devices, revoke, status",
```

becomes

```go
		Short: "Serve loom's fleet to a paired phone: enable, disable, pair, open, devices, revoke, status",
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run TestWeb && GOOS=windows CGO_ENABLED=0 go build ./...`
Expected: `ok  	github.com/aidan-bailey/loom/internal/cli` and a silent cross-build.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(cli): loom web open pairs and opens the browser on the local origin" -- internal/cli/web_open.go internal/cli/web.go internal/cli/web_test.go
```

### Task 2.4: The `Local:` status row

**Files:**
- Modify: `internal/cli/web.go` (`writeWebStatus`, the `case w.Serving:` arm)
- Test: `internal/cli/web_test.go` (appended)

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/web_test.go`:

```go
func TestWebStatus_ShowsTheLocalOrigin(t *testing.T) {
	d := webDaemon(t)
	servingWeb(t, d, "127.0.0.1:7420", "127.0.0.1:7420")

	out, _, err := runWeb(t, "status")

	require.NoError(t, err)
	assert.Regexp(t, `Local:\s+http://loom\.localhost:7420\n`, out)
	assert.Regexp(t, `Origin:\s+https://loom\.example\.com`, out, "beside the configured origin, which is unchanged")
}

func TestWebStatus_SaysWhenTheListenerHasNoLocalOrigin(t *testing.T) {
	d := webDaemon(t)
	servingWeb(t, d, "127.0.0.1:7420", "127.0.0.2:7420")
	_, noLocal := core.LocalOrigin("127.0.0.2:7420")
	require.Error(t, noLocal, "control: another loopback address has no local origin")

	out, _, err := runWeb(t, "status")

	require.NoError(t, err)
	assert.Equal(t, "Local:    none ("+noLocal.Error()+")", lineWith(t, out, "Local:"))
}

func TestWebStatus_HasNoLocalRowUntilItServes(t *testing.T) {
	d := webDaemon(t)
	out, _, err := runWeb(t, "status")
	require.NoError(t, err)
	assert.NotContains(t, out, "Local", "off")

	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	out, _, err = runWeb(t, "status")
	require.NoError(t, err)
	assert.NotContains(t, out, "Local", "on, and nothing has answered")

	d.loop.WebServing("", errors.New("listen tcp 127.0.0.1:7420: bind: address already in use"))
	out, _, err = runWeb(t, "status")
	require.NoError(t, err)
	assert.NotContains(t, out, "Local", "on, and the bind failed")
}
```

Fails without: `TestWebStatus_ShowsTheLocalOrigin`: no row, or the IP literal in it; `TestWebStatus_SaysWhenTheListenerHasNoLocalOrigin`: a row that hides the error or prints an empty origin; `TestWebStatus_HasNoLocalRowUntilItServes`: a row printed outside the `case w.Serving:` arm.

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run TestWebStatus_`
Expected: FAIL: `TestWebStatus_ShowsTheLocalOrigin` and `TestWebStatus_SaysWhenTheListenerHasNoLocalOrigin` find no `Local:` line (`no line with "Local:" in: …`).

- [ ] **Step 3: Implement**

In `writeWebStatus` (`internal/cli/web.go`), today:

```go
	case w.Serving:
		row("Serving", "yes, on "+w.Addr)
```

becomes

```go
	case w.Serving:
		row("Serving", "yes, on "+w.Addr)
		if local, err := core.LocalOrigin(w.Addr); err != nil {
			row("Local", "none ("+err.Error()+")")
		} else {
			row("Local", local)
		}
```

and its doc comment line `// which origin, whether it serves or why not, and the device count.` becomes `// which origin, whether it serves or why not (and, while it serves, its local origin), and the device count.`

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run TestWeb`
Expected: `ok  	github.com/aidan-bailey/loom/internal/cli`

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(cli): loom web status shows the local origin" -- internal/cli/web.go internal/cli/web_test.go
```

### Task 2.5: The docs

**Files:**
- Modify: `USAGE.md:840-885` (the web section), `USAGE.md:948-960` (the troubleshooting table), `USAGE.md:1345-1354` (the Web Subcommands table)
- Modify: `internal/cli/README.md:13`, `internal/cli/README.md:49`, `internal/cli/README.md:52`
- Modify: `docs/ARCHITECTURE.md:49`

- [ ] **Step 1: Write the docs**

`USAGE.md`, web section. After the numbered list's last item, the line

```
5. **Notifications.** In the app's settings, tap to turn them on.
```

insert a blank line and then this paragraph:

```
**On the daemon's machine.** `loom web open [--name <device>]` opens the app
in this machine's browser with no tunnel, at the listener's local origin,
`http://loom.localhost:<port>` (browsers resolve `*.localhost` to loopback; a
listener on `127.0.0.1` or `[::1]` has one, `loom web status` shows it as
`Local:`, and a listener on any other loopback address has none). Bare
`localhost` and `127.0.0.1` are not served: a cookie set there would reach every
server this machine's browser reaches on any port. The name is `desktop` unless
`--name` gives another. For a name not paired yet it makes a code and opens the
link that carries it (`<local>/?ui=next#/pair?code=…`: the app takes the code out
of the address before it does anything else, and it works once, for ten
minutes); for a paired name it opens `<local>/?ui=next` and says so, and
`loom web revoke <name>` unpairs it for another go. The URL is always printed.
The command refuses, minting nothing, while the web is off (when `web.json` is
left alone it quotes why, as `loom web status` does in its `Error:` row) or not
serving, or when the listener has no local origin. If no browser opens (no
display, over SSH) it says so, leaves the URL for you and exits 0. Each origin
holds its own device cookie, so this browser and a phone on the tunnel are
separate devices. Safari may not resolve `*.localhost`: pair a Safari desktop
through the tunnel with `loom web pair`.

Two things are accepted on a single-user machine, and not on a shared one. The
pairing link, code included, is the opener's argument, so another local user can
read the code from the process list (`ps`, `/proc/<pid>/cmdline`) while the
opener or a freshly started browser runs; the code works once and for ten
minutes. And the `loom.localhost` device cookie goes to whatever listens on
loopback at the port the browser is steered to, so a hostile local listener
there can read it.
```

In the paragraph after it, replace

```
the settings, whether it serves and why not, and the device count.
```

with

```
the settings, whether it serves and why not, its local origin and the device
count.
```

In the troubleshooting table, after the row beginning `| Pairing says the code is wrong, used or expired |`, add:

```
| `loom web open` says it could not open a browser | The URL is printed above the message: open it yourself (a code it made works for ten minutes); over SSH there is no display to open it on |
| `loom web open` says the web app is off and its settings file is left alone | `loom web enable` refuses too: fix or move `web.json` by hand (the message quotes why it failed to load; `loom web status` shows it as `Error:`), then run `loom web enable` |
| `loom web open` says there is no local origin | The listener is bound to another loopback address than `127.0.0.1` or `[::1]`: run `loom web enable` with one of those |
```

`USAGE.md`, the Web Subcommands table (under `### Web Subcommands`). After the row beginning `` | `web pair [--name <device>]` ``, add this row:

```
| `web open [--name <device>]` | Open the web app in this machine's browser with no tunnel, at the listener's local origin (`http://loom.localhost:<port>`). A name not paired yet gets a one-time code in the opened link (default name `desktop`); a paired name opens the plain link. Prints the URL always; refused, minting nothing, while the web is off or not serving or when the listener has no local origin; with no browser to open it says so and exits 0 |
```

and replace the row

```
| `web status` | The settings, whether the web app serves or why not, and the number of devices |
```

with

```
| `web status` | The settings, whether the web app serves or why not, its local origin (the `Local:` row) and the number of devices |
```

`internal/cli/README.md`, the file table (line 13). In the row that begins `` | `web.go` | `WebCmd` ``, replace

```
`enable`, `disable`, `pair`, `devices`, `revoke`, `status`;
```

with

```
`enable`, `disable`, `pair`, `open`, `devices`, `revoke`, `status`;
```

(the `open` subcommand's own file is the row added next). After it add this row:

```
| `web_open.go`, `open_unix.go`, `open_windows.go` | `open` (built by `newWebOpenCmd`), `webLocalOrigin`, `defaultDesktopName`; the opener: `openBrowser` (the seam tests replace), `startBrowser`, `openerCommand` (the platform's command, in the `_unix.go` and `_windows.go` files), `waitOpener`, `openerWait` |
```

and in the command table, after the row

```
| `web pair` | `PairDevice`; the code is printed as `XXXX-XXXX` with its expiry and the origin |
```

add:

```
| `web open` | refused unless `Web()` is serving and `core.LocalOrigin` gives its address a local origin (`webLocalOrigin`), then `PairDevice`: a refusal `core.IsDeviceExists` reads as a paired name opens the plain URL with no code, any other refusal is the model's; the URL is printed, then handed to `openBrowser` (5 s wait, never killed, a failure reported and exit 0) |
```

and replace the row

```
| `web status` | `Web()` (read only) |
```

with

```
| `web status` | `Web()` (read only); the `Local:` row is `webLocalOrigin`'s answer for the listener |
```

`docs/ARCHITECTURE.md`, line 49: replace

```
`loom web` (`enable`, `disable`, `pair`, `devices`, `revoke`, `status`) sends `ConfigureWeb`, `PairDevice` and `RevokeDevice` and reads `Web()` from the replica;
```

with

```
`loom web` (`enable`, `disable`, `pair`, `open`, `devices`, `revoke`, `status`) sends `ConfigureWeb`, `PairDevice` and `RevokeDevice` and reads `Web()` from the replica (`open` pairs a new name and starts the machine's browser on the listener's local origin, `core.LocalOrigin`);
```

- [ ] **Step 2: Run the checks to see them fail**

Not applicable: the docs lint passes before the change.

- [ ] **Step 3: Implement**

The edits in Step 1 are the implementation.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go test ./internal/cli/ -run TestWeb`
Expected: both `ok`.

- [ ] **Step 5: Commit**

```bash
git commit -m "docs(cli): loom web open in USAGE, the cli README and the architecture note" -- USAGE.md internal/cli/README.md docs/ARCHITECTURE.md
```

## Story 3: The app consumes a pairing link before its router starts

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/pairHold.ts`, `internal/web/ui/src/app/pairLink.ts`, `internal/web/ui/src/app/pairLink.test.ts`, `internal/web/ui/src/panels/pair/code.ts`, `internal/web/ui/src/panels/pair/code.test.ts`, `internal/web/ui/src/panels/pair/Pair.tsx`, `internal/web/ui/src/main.tsx`, `internal/web/ui/src/main.pairing.test.tsx`, `internal/web/ui/src/app/switchApp.ts`, `internal/web/dist/`
- **Interfaces:** exposes `adoptNextApp`, `holdPairLink`, `watchPairLink`; uses `cookieApp`, `registerWorker`, `normalizeCode`, `CODE_LENGTH`, `onUnpaired`, `runCommand`, `commandById`, `useFleet`, and the pairing link's form (Story 2)
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** full (auth: a pairing credential in the address, removed before any request and used once)

`loom web open` (Story 2) opens `<local>/?ui=next#/pair?code=<8 characters>` for a new device name and `<local>/?ui=next` for a paired one. This story makes the app act on that link before anything that builds the router runs. `app/routes.tsx` builds the router's hash history when it is imported, and the history reads the address then, so the code must be out of the address before any module that reaches `routes.tsx` is evaluated: `adoptNextApp` and `holdPairLink` live in a new leaf module `app/pairHold.ts` that imports only leaves (`app/switchApp.ts` and the new `panels/pair/code.ts`), `main.tsx` imports it first, and its evaluation runs them in order. `adoptNextApp` keeps a browser that arrived by `?ui=next` with no `__Host-loom-ui` cookie on the new app (the cookie is set before the worker registers, through one writer, `setAppCookie`, that `switchApp` now shares); `holdPairLink` takes `#/pair?code=…` (or `#pair?code=…`) out of the address in place with `history.replaceState` (keeping `history.state`) and holds the normalised code in memory only when it has `CODE_LENGTH` characters. `watchPairLink`, in `app/pairLink.ts` (it needs the commands, so it reaches the router), runs `device.pair` with the held code once, on the first unpaired signal (the daemon's 401), and a live snapshot first means this browser is paired, so the code is dropped unused. `normalizeCode`, `CODE_LENGTH` and the alphabet behind them move from `panels/pair/Pair.tsx` to the leaf `panels/pair/code.ts`, which `Pair.tsx` re-exports, so `pairHold.ts` need not import the pairing panel (which imports the router). A refused code leaves the usual pairing screen with the daemon's words as the command's toast. A worker takeover that reloads the page drops the held code (it lives only in memory), and the pairing screen shows as usual. Three tasks: the leaf `code.ts`, the pairing link's modules, and `main.tsx` with the rebuilt bundle. No landmine marker appears in this story's code.

### Task 3.1: `panels/pair/code.ts`, the leaf `Pair.tsx` re-exports

**Files:**
- Create: `internal/web/ui/src/panels/pair/code.ts`, `internal/web/ui/src/panels/pair/code.test.ts`
- Modify: `internal/web/ui/src/panels/pair/Pair.tsx:1-26`
- Test: `internal/web/ui/src/panels/pair/pair.test.tsx` (unchanged: it imports `CODE_LENGTH`, `normalizeCode` and `PAIRING_ALPHABET` from `./Pair`, which the re-export keeps working)

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/panels/pair/code.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import * as pairPanel from "./Pair";
import { CODE_LENGTH, normalizeCode, PAIRING_ALPHABET } from "./code";

describe("panels/pair/code", () => {
  it("code: Pair.tsx re-exports the same values, so its importers are unchanged", () => {
    expect(pairPanel.CODE_LENGTH).toBe(CODE_LENGTH);
    expect(pairPanel.normalizeCode).toBe(normalizeCode);
    expect(pairPanel.PAIRING_ALPHABET).toBe(PAIRING_ALPHABET);
  });

  it("code: a code is 8 characters of the pairing alphabet, normalised as typed or pasted", () => {
    expect(CODE_LENGTH).toBe(8);
    expect(normalizeCode("k7qm-2xpa9")).toBe("K7QM2XPA");
    expect(normalizeCode("O0I1L")).toBe("");
  });
});
```

Fails without:
- `code: Pair.tsx re-exports the same values`: the re-export line removed from `Pair.tsx` (its exports are then undefined), or a second definition of `normalizeCode` left in it (a different function by identity).
- `code: a code is 8 characters…`: `CODE_LENGTH` or the alphabet changed in the move.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/pair/code.test.ts`

Expected: the file fails to load, with `Failed to resolve import "./code" from "src/panels/pair/code.test.ts". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/panels/pair/code.ts`. It imports nothing: `app/pairHold.ts` imports it before the router exists.

```ts
// The pairing code's shape, in a module of its own that imports nothing: app/pairHold.ts reads it
// before the router is built, and Pair.tsx (which imports the router) re-exports it.

// PAIRING_ALPHABET is the daemon's own pairing alphabet (internal/web/auth.go): 31 characters, no
// 0, 1, I, L or O, so a code read off a screen has no look-alikes.
export const PAIRING_ALPHABET = "23456789ABCDEFGHJKMNPQRSTUVWXYZ";

// CODE_LENGTH is the length of a pairing code.
export const CODE_LENGTH = 8;

// normalizeCode upper-cases value, keeps only the characters the pairing alphabet holds, and keeps
// at most CODE_LENGTH of them. The cut follows the filter, so a dropped character costs no slot,
// and a mistyped O or 0 never reaches the daemon, which counts 5 wrong codes before it locks.
// It is idempotent, because OTPField normalises the same value more than once.
export function normalizeCode(value: string): string {
  let out = "";
  for (const ch of value.toUpperCase()) {
    if (PAIRING_ALPHABET.includes(ch)) out += ch;
  }
  return out.slice(0, CODE_LENGTH);
}
```

In `internal/web/ui/src/panels/pair/Pair.tsx`, add the import after `import { useFleet } from "@/store/fleet";`:

```tsx
import { useFleet } from "@/store/fleet";

import { CODE_LENGTH, normalizeCode } from "./code";

export { CODE_LENGTH, normalizeCode, PAIRING_ALPHABET } from "./code";
```

and delete the definitions it replaces, the block that follows the imports (from the `// PAIRING_ALPHABET is the daemon's own pairing alphabet` comment to the closing brace of `normalizeCode`), which is exactly the text now in `code.ts`:

```tsx
// PAIRING_ALPHABET is the daemon's own pairing alphabet (internal/web/auth.go): 31 characters, no
// 0, 1, I, L or O, so a code read off a screen has no look-alikes.
export const PAIRING_ALPHABET = "23456789ABCDEFGHJKMNPQRSTUVWXYZ";

// CODE_LENGTH is the length of a pairing code.
export const CODE_LENGTH = 8;

// normalizeCode upper-cases value, keeps only the characters the pairing alphabet holds, and keeps
// at most CODE_LENGTH of them. The cut follows the filter, so a dropped character costs no slot,
// and a mistyped O or 0 never reaches the daemon, which counts 5 wrong codes before it locks.
// It is idempotent, because OTPField normalises the same value more than once.
export function normalizeCode(value: string): string {
  let out = "";
  for (const ch of value.toUpperCase()) {
    if (PAIRING_ALPHABET.includes(ch)) out += ch;
  }
  return out.slice(0, CODE_LENGTH);
}
```

Leave the comment and the body of `Pair` that follow it as they are (`Pair` still uses `CODE_LENGTH` and `normalizeCode`, now imported).

Format with `pnpm -C internal/web/ui exec prettier --write src/panels/pair/code.ts src/panels/pair/code.test.ts src/panels/pair/Pair.tsx`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/pair && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`

Expected: `Test Files  2 passed (2)` (`code.test.ts` and the unchanged `pair.test.tsx`), then the typecheck and lint exit 0.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/pair/code.ts internal/web/ui/src/panels/pair/code.test.ts
git commit -m "refactor(web): the pairing code's shape in a leaf module Pair re-exports" -- internal/web/ui/src/panels/pair/code.ts internal/web/ui/src/panels/pair/code.test.ts internal/web/ui/src/panels/pair/Pair.tsx
```

### Task 3.2: `app/pairHold.ts` and `app/pairLink.ts`, and `switchApp`'s shared cookie writer

**Files:**
- Create: `internal/web/ui/src/app/pairHold.ts`, `internal/web/ui/src/app/pairLink.ts`, `internal/web/ui/src/app/pairLink.test.ts`
- Modify: `internal/web/ui/src/app/switchApp.ts:12-17`, `internal/web/ui/src/app/switchApp.ts:26-27`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/app/pairLink.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, emitUnpaired } from "@/api/request";
import { resetFleet, seed, spyOnAdd } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import "@/commands/device";
import pairHoldSource from "./pairHold.ts?raw";
import { adoptNextApp, heldCode, holdPairLink } from "./pairHold";
import { watchPairLink } from "./pairLink";

const NEXT_COOKIE = "__Host-loom-ui=next; Secure; Path=/; SameSite=Strict; Max-Age=31536000";
const REFUSED = "that code is not right";

const spyOnPair = () =>
  vi.spyOn(api, "pair").mockResolvedValue({ kind: "refused", status: 400, message: REFUSED });

let jar: string;
let writes: string[];
let stop: () => void;
let add: ReturnType<typeof spyOnAdd>;
let pair: ReturnType<typeof spyOnPair>;

beforeEach(() => {
  jar = "";
  writes = [];
  Object.defineProperty(document, "cookie", {
    configurable: true,
    get: () => jar,
    set: (value: string) => {
      writes.push(value);
    },
  });
  add = spyOnAdd();
  pair = spyOnPair();
  stop = () => undefined;
  resetFleet();
  window.history.replaceState(null, "", "/");
  holdPairLink();
});

afterEach(() => {
  stop();
  Reflect.deleteProperty(document, "cookie");
  vi.restoreAllMocks();
  resetFleet();
  window.history.replaceState(null, "", "/");
  holdPairLink();
});

// open puts the address as a browser opened on it, then reads it as pairHold's evaluation does.
function open(address: string): void {
  window.history.replaceState(null, "", address);
  holdPairLink();
}

describe("adoptNextApp", () => {
  it("sets the new app's cookie, with switchApp's attributes, for ?ui=next and no app cookie", () => {
    window.history.replaceState(null, "", "/?ui=next#/pair");
    adoptNextApp();
    expect(writes).toEqual([NEXT_COOKIE]);
  });

  it("sets it when other cookies are set but not the app's", () => {
    jar = "a=1; b=2";
    window.history.replaceState(null, "", "/?ui=next");
    adoptNextApp();
    expect(writes).toEqual([NEXT_COOKIE]);
  });

  it("leaves an explicit classic choice alone", () => {
    jar = "__Host-loom-ui=classic";
    window.history.replaceState(null, "", "/?ui=next");
    adoptNextApp();
    expect(writes).toEqual([]);
  });

  it("leaves a next cookie as it is", () => {
    jar = "a=1; __Host-loom-ui=next";
    window.history.replaceState(null, "", "/?ui=next");
    adoptNextApp();
    expect(writes).toEqual([]);
  });

  it.each(["/", "/?ui=classic", "/?x=ui%3Dnext", "/#/?ui=next", "/?ui=nextish"])(
    "does nothing at %s",
    (address) => {
      window.history.replaceState(null, "", address);
      adoptNextApp();
      expect(writes).toEqual([]);
    },
  );
});

describe("holdPairLink", () => {
  it.each(["#/pair?code=K7QM-2XPA", "#pair?code=k7qm2xpa&x=1"])(
    "takes the code out of %s in place, with replaceState, no history entry and the state kept",
    (hash) => {
      const state = { key: "router-state" };
      window.history.replaceState(state, "", `/${hash}`);
      const replace = vi.spyOn(window.history, "replaceState");
      const entries = window.history.length;
      holdPairLink();
      expect(replace).toHaveBeenCalledWith(state, "", "#/pair");
      expect(window.history.state).toEqual(state);
      expect(window.location.hash).toBe("#/pair");
      expect(window.history.length).toBe(entries);
    },
  );

  it.each(["#/", "#/pair", "#/pair?x=1", "#/s/5?code=K7QM2XPA", "#/pairs?code=K7QM2XPA"])(
    "leaves the address %s alone",
    (hash) => {
      window.history.replaceState(null, "", `/${hash}`);
      const replace = vi.spyOn(window.history, "replaceState");
      holdPairLink();
      expect(replace).not.toHaveBeenCalled();
      expect(window.location.hash).toBe(hash);
    },
  );

  it.each([
    ["K7QM-2XPA", true],
    ["k7qm2xpa", true],
    ["K7QM-2XP", false],
    ["", false],
    ["O0I1L", false],
  ])("holds the code %j only when it has 8 characters (%s)", (raw, held) => {
    open(`/#/pair?code=${raw}`);
    expect(window.location.hash).toBe("#/pair");
    expect(heldCode()).toBe(held ? "K7QM2XPA" : undefined);
    stop = watchPairLink();
    emitUnpaired();
    if (held) expect(pair).toHaveBeenCalledWith({ code: "K7QM2XPA" });
    else expect(pair).not.toHaveBeenCalled();
  });

  it("holds nothing from an earlier address once it is called again", () => {
    open("/#/pair?code=K7QM2XPA");
    open("/#/");
    expect(heldCode()).toBeUndefined();
    stop = watchPairLink();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });
});

describe("pairHold", () => {
  it("imports only leaves, nothing that reaches app/routes.tsx", () => {
    const specifiers = [...pairHoldSource.matchAll(/^import .* from "(.*)";$/gm)].map((m) => m[1]);
    expect(specifiers.sort()).toEqual(["./switchApp", "@/panels/pair/code"]);
  });

  it("runs adoptNextApp, then holdPairLink, when it is first evaluated", async () => {
    window.history.replaceState(null, "", "/?ui=next#/pair?code=K7QM-2XPA");
    vi.resetModules();
    const fresh = await import("./pairHold");
    expect(writes).toEqual([NEXT_COOKIE]);
    expect(window.location.hash).toBe("#/pair");
    expect(fresh.heldCode()).toBe("K7QM2XPA");
  });
});

describe("watchPairLink", () => {
  it("pairs with the held code on the first unpaired signal, never before", () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    expect(pair).not.toHaveBeenCalled();
    emitUnpaired();
    expect(pair).toHaveBeenCalledOnce();
    expect(pair).toHaveBeenCalledWith({ code: "K7QM2XPA" });
  });

  it("uses the code once: a second unpaired signal posts nothing, though the first was refused", async () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    emitUnpaired();
    await vi.waitFor(() => {
      expect(add).toHaveBeenCalled();
    });
    expect(heldCode()).toBeUndefined();
    emitUnpaired();
    emitUnpaired();
    expect(pair).toHaveBeenCalledOnce();
  });

  it("shows the daemon's words as the command's toast when the code is refused", async () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    emitUnpaired();
    await vi.waitFor(() => {
      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ description: REFUSED, type: "error" }),
      );
    });
  });

  it("posts nothing when no code was held", () => {
    open("/#/");
    stop = watchPairLink();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });

  it("drops the code unused when a live snapshot arrives first", () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    seed([]);
    expect(heldCode()).toBeUndefined();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });

  it("drops the code unused when a live snapshot is already held as watching starts", () => {
    open("/#/pair?code=K7QM2XPA");
    seed([]);
    stop = watchPairLink();
    expect(heldCode()).toBeUndefined();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });

  it("keeps the code through a stale snapshot, the saved one a cold start loads", () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: true, link: "offline" });
    emitUnpaired();
    expect(pair).toHaveBeenCalledOnce();
  });

  it("stops listening when its stop is called", () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    stop();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });
});
```

Fails without:
- `adoptNextApp: sets the new app's cookie, with switchApp's attributes, for ?ui=next and no app cookie`: the `adoptNextApp` body removed, or `setAppCookie` writing other attributes than `switchApp`'s.
- `adoptNextApp: leaves an explicit classic choice alone`: the `hasAppCookie` guard removed or replaced by `cookieApp() === "next"` (which reads no cookie and classic alike).
- `adoptNextApp: sets it when other cookies are set but not the app's`: `hasAppCookie` answering true for any cookie.
- `adoptNextApp: does nothing at %s`: the `ui` query test loosened (a prefix match, or the fragment's text read).
- `holdPairLink: takes the code out of %s in place`: `replaceState` replaced by assigning `location.hash` (no `replaceState` call), the replacement hash other than `#/pair`, or `null` passed for `window.history.state` (the router's own state is dropped).
- `holdPairLink: leaves the address %s alone`: the pattern loosened to any hash holding `code=` or to a route other than `pair`.
- `holdPairLink: holds the code %j only when it has 8 characters`: the length test removed (a 7-character code held), or `normalizeCode` not applied (`k7qm2xpa` or the hyphenated form held as typed).
- `holdPairLink: holds nothing from an earlier address once it is called again`: the held code not reset at the start of `holdPairLink`.
- `pairHold: imports only leaves, nothing that reaches app/routes.tsx`: any other import added to `pairHold.ts` (`@/panels/pair/Pair`, `@/app/commands`, `@/app/routes`), the leak that made the router read the code.
- `pairHold: runs adoptNextApp, then holdPairLink, when it is first evaluated`: the two trailing calls removed from `pairHold.ts`.
- `watchPairLink: pairs with the held code on the first unpaired signal, never before`: the code sent at once, or `panel` in the command's context other than `"pair"` (the command's `when` then refuses and nothing is sent).
- `watchPairLink: uses the code once`: the held code kept after use (`dropHeldCode()` not called in the unpaired listener).
- `watchPairLink: shows the daemon's words as the command's toast`: the command not run through `runCommand`.
- `watchPairLink: posts nothing when no code was held`: the missing-code guard removed.
- `watchPairLink: drops the code unused when a live snapshot arrives first`: the fleet subscription removed.
- `watchPairLink: drops the code unused when a live snapshot is already held as watching starts`: the check at start removed.
- `watchPairLink: keeps the code through a stale snapshot`: the drop made on any snapshot, without the `!stale` term.
- `watchPairLink: stops listening when its stop is called`: the unpaired listener left registered.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/pairLink.test.ts`

Expected: the file fails to load, with `Failed to resolve import "./pairHold.ts?raw" from "src/app/pairLink.test.ts". Does the file exist?`

- [ ] **Step 3: Implement**

In `internal/web/ui/src/app/switchApp.ts`, after `cookieApp`, add the presence test and the shared writer. The anchor is the end of `cookieApp`:

```ts
  return pair === `${prefix}next` ? "next" : "classic";
}
```

Insert directly after its closing brace:

```ts

// hasAppCookie is whether the device has chosen an app at all: cookieApp says "classic" for no
// cookie as it does for an explicit choice, and the two must be told apart (spec §26).
export function hasAppCookie(): boolean {
  return document.cookie.split("; ").some((c) => c.startsWith(`${COOKIE}=`));
}

// setAppCookie records app as the device's choice, the one writer of the cookie: switchApp sets it
// when the owner switches, adoptNextApp when a launch by ?ui=next has no choice yet.
export function setAppCookie(app: AppName): void {
  document.cookie = `${COOKIE}=${app}; Secure; Path=/; SameSite=Strict; Max-Age=31536000`;
}
```

In `switchApp`, replace its first body line

```ts
  document.cookie = `${COOKIE}=${app}; Secure; Path=/; SameSite=Strict; Max-Age=31536000`;
```

with

```ts
  setAppCookie(app);
```

(Do this replacement in `switchApp`'s body, the line with two leading spaces, not in the new `setAppCookie`, which keeps its copy.)

Create `internal/web/ui/src/app/pairHold.ts`. It imports only `switchApp` and `panels/pair/code`, both leaves; its last two lines run on import:

```ts
// The leaf that takes the pairing link (spec §26) out of the address: `loom web open` opens
// <local>/?ui=next#/pair?code=<code> on the daemon's machine. main.tsx imports this module before
// any other of the app's, and its evaluation (the last two lines) runs adoptNextApp and then
// holdPairLink, because app/routes.tsx builds the router's hash history when it is imported and
// that history reads the address then: a module evaluated before this one that reaches the router
// would hand it #/pair?code=…. So this module imports leaves only (switchApp and panels/pair/code),
// never the commands, the pairing panel or the routes; watchPairLink, which needs the commands,
// is in app/pairLink.ts and reads the held code from here. The code stays in memory: a reload
// drops it and the pairing screen shows as usual.
import { CODE_LENGTH, normalizeCode } from "@/panels/pair/code";

import { hasAppCookie, setAppCookie } from "./switchApp";

// held is the normalised code taken from the address, until it is used or dropped.
let held: string | undefined;

// A hash of #/pair or #pair with a query: the pairing link's fragment.
const PAIR_HASH = /^#\/?pair\?(.*)$/;

// heldCode is the code holdPairLink took from the address, if it is still held.
export function heldCode(): string | undefined {
  return held;
}

// dropHeldCode forgets the code, used or not.
export function dropHeldCode(): void {
  held = undefined;
}

// adoptNextApp keeps a browser that arrived by ?ui=next with no app cookie on the new app: the
// daemon served it the new shell for that query alone, and the worker registers by the cookie
// (registerWorker), so without the cookie the next launch would be the classic app's. An explicit
// choice, "classic" included, is left as it is.
export function adoptNextApp(): void {
  if (new URLSearchParams(window.location.search).get("ui") !== "next") return;
  if (hasAppCookie()) return;
  setAppCookie("next");
}

// holdPairLink takes the code out of a pairing address in place (no history entry, the history's
// own state kept for the router) and holds it when it has CODE_LENGTH characters once normalised;
// any other code is dropped, and the pairing screen shows as usual. A hash with no code query, or
// another route's, is left alone.
export function holdPairLink(): void {
  held = undefined;
  const found = window.location.hash.match(PAIR_HASH);
  if (found === null) return;
  const raw = new URLSearchParams(found[1] ?? "").get("code");
  if (raw === null) return;
  window.history.replaceState(window.history.state, "", "#/pair");
  const code = normalizeCode(raw);
  if (code.length === CODE_LENGTH) held = code;
}

// The module's evaluation is the launch step: the cookie of a launch by ?ui=next is set before the
// worker registers by it, and the code is out of the address before the router reads it.
adoptNextApp();
holdPairLink();
```

Create `internal/web/ui/src/app/pairLink.ts`:

```ts
// watchPairLink (spec §26): once the app starts, pairs with the code app/pairHold.ts took out of
// the address, on the first unpaired signal. It is apart from pairHold.ts because it runs a command
// and so reaches the router, which must not be evaluated before pairHold.ts has run.
import { onUnpaired } from "@/api/request";
import { commandById, runCommand } from "@/app/commands";
import { useFleet } from "@/store/fleet";

import { dropHeldCode, heldCode } from "./pairHold";

// watchPairLink pairs with the held code on the first unpaired signal (the daemon's 401), once. A
// live snapshot first means this browser is paired already, so the code is dropped unused; a stale
// one (a cold start's saved snapshot) is not that. It returns its stop.
export function watchPairLink(): () => void {
  if (heldCode() === undefined) return () => undefined;
  const live = (): boolean => {
    const { snapshot, stale } = useFleet.getState();
    return snapshot !== null && !stale;
  };
  if (live()) {
    dropHeldCode();
    return () => undefined;
  }
  let offFleet = (): void => undefined;
  let offUnpaired = (): void => undefined;
  const stop = (): void => {
    offFleet();
    offUnpaired();
  };
  offFleet = useFleet.subscribe(() => {
    if (!live()) return;
    dropHeldCode();
    stop();
  });
  offUnpaired = onUnpaired(() => {
    const code = heldCode();
    dropHeldCode();
    stop();
    if (code === undefined) return;
    const command = commandById("device.pair");
    if (command === undefined) throw new Error("loom: unknown command device.pair");
    void runCommand(
      command,
      { session: undefined, link: useFleet.getState().link, stale: true, panel: "pair" },
      { code },
    );
  });
  return stop;
}
```

Format with `pnpm -C internal/web/ui exec prettier --write src/app/pairHold.ts src/app/pairLink.ts src/app/pairLink.test.ts src/app/switchApp.ts`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/app/pairLink.test.ts src/app/switchApp.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`

Expected: `Test Files  2 passed (2)`, then the typecheck and lint exit 0. `switchApp.test.ts` is unchanged: its cookie-string assertions pin the shared writer.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/app/pairHold.ts internal/web/ui/src/app/pairLink.ts internal/web/ui/src/app/pairLink.test.ts
git commit -m "feat(web): hold a pairing link's code before the router and adopt the new app from ?ui=next" -- internal/web/ui/src/app/pairHold.ts internal/web/ui/src/app/pairLink.ts internal/web/ui/src/app/pairLink.test.ts internal/web/ui/src/app/switchApp.ts
```

### Task 3.3: `main.tsx` imports `pairHold` first and watches the link, and the bundle is rebuilt

**Files:**
- Create: `internal/web/ui/src/main.pairing.test.tsx`
- Modify: `internal/web/ui/src/main.tsx:5-7`, `internal/web/ui/src/main.tsx:13`, `internal/web/ui/src/main.tsx:33`, `internal/web/ui/src/main.tsx:42`, `internal/web/ui/src/main.tsx:70-76`
- Modify: `internal/web/dist/` (rebuilt)
- Test: `internal/web/ui/src/main.pairing.test.tsx` (`main.test.tsx` is unchanged: its mocks still serve its tests, with `pairHold` evaluating as a no-op at `/`)

The test is a file of its own because `main.test.tsx` mocks `@/app/routes` through a factory that vitest keeps across `vi.resetModules()`, so its router is built once, before any test sets an address. Here `routes.tsx` is the real module, evaluated afresh at each launch, so the router the test reads (`router.history.location`, handed to the `RouterProvider` stub) is the one the launch built.

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/main.pairing.test.tsx`:

```tsx
import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// seen records, for each launch, the order main takes its steps in, the address and the count of
// cookie writes when the worker registers, and the router's own location when it is handed to
// the RouterProvider: the location the router read when routes.tsx was evaluated.
const seen = vi.hoisted(() => ({
  order: [] as string[],
  hashAtWorker: undefined as string | undefined,
  writes: [] as string[],
  writesAtWorker: undefined as number | undefined,
  routerAt: undefined as { pathname: string; search: string } | undefined,
}));

const NEXT_COOKIE = "__Host-loom-ui=next; Secure; Path=/; SameSite=Strict; Max-Age=31536000";

vi.mock("@/store/fleet", async (original) => ({
  ...(await original<typeof import("@/store/fleet")>()),
  startFleet: vi.fn(() => {
    seen.order.push("startFleet");
    return () => undefined;
  }),
}));
vi.mock("@/app/worker", async (original) => ({
  ...(await original<typeof import("@/app/worker")>()),
  registerWorker: vi.fn(() => {
    seen.order.push("registerWorker");
    seen.hashAtWorker = window.location.hash;
    seen.writesAtWorker = seen.writes.length;
  }),
}));
vi.mock("@/app/toasts", async (original) => ({
  ...(await original<typeof import("@/app/toasts")>()),
  watchNotices: vi.fn(() => () => undefined),
}));
// The pairing is pairLink.test.ts's; here only when main arms it matters.
vi.mock("@/app/pairLink", () => ({
  watchPairLink: vi.fn(() => {
    seen.order.push("watchPairLink");
    return () => undefined;
  }),
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  RouterProvider: ({
    router,
  }: {
    router: { history: { location: { pathname: string; search: string } } };
  }) => {
    const { pathname, search } = router.history.location;
    seen.routerAt = { pathname, search };
    return <p>router</p>;
  },
}));

const IPHONE =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1";
const DESKTOP =
  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36";

let mount: HTMLDivElement;

// ios sets the page up as an iPhone browser tab, or as a desktop browser.
function ios(on: boolean): void {
  vi.spyOn(navigator, "userAgent", "get").mockReturnValue(on ? IPHONE : DESKTOP);
  vi.spyOn(navigator, "platform", "get").mockReturnValue(on ? "iPhone" : "Linux x86_64");
  vi.spyOn(navigator, "maxTouchPoints", "get").mockReturnValue(on ? 5 : 0);
  Object.defineProperty(navigator, "standalone", { value: false, configurable: true });
  vi.spyOn(window, "matchMedia").mockImplementation(
    (query: string) => ({ matches: false, media: query }) as MediaQueryList,
  );
}

// launch draws main into a fresh #app, the modules evaluated afresh.
async function launch(): Promise<void> {
  vi.resetModules();
  await act(async () => {
    await import("@/main");
  });
}

beforeEach(() => {
  mount = document.createElement("div");
  mount.id = "app";
  document.body.append(mount);
  localStorage.clear();
  vi.clearAllMocks();
  seen.order.length = 0;
  seen.hashAtWorker = undefined;
  seen.writes.length = 0;
  seen.writesAtWorker = undefined;
  seen.routerAt = undefined;
  Object.defineProperty(document, "cookie", {
    configurable: true,
    get: () => "",
    set: (value: string) => {
      seen.writes.push(value);
    },
  });
});

afterEach(() => {
  mount.remove();
  vi.restoreAllMocks();
  Reflect.deleteProperty(navigator, "standalone");
  Reflect.deleteProperty(document, "cookie");
  localStorage.clear();
  window.history.replaceState(null, "", "/");
});

describe("main and the pairing link", () => {
  it("main: a pairing link is out of the address before the router is built, and before the worker and the stream start", async () => {
    ios(false);
    window.history.replaceState(null, "", "/?ui=next#/pair?code=K7QM-2XPA");
    await launch();
    expect(seen.routerAt).toEqual({ pathname: "/pair", search: "" });
    expect(seen.order).toEqual(["registerWorker", "watchPairLink", "startFleet"]);
    expect(seen.hashAtWorker).toBe("#/pair");
    expect(seen.writes).toEqual([NEXT_COOKIE]);
    expect(seen.writesAtWorker).toBe(1);
    const { heldCode } = await import("@/app/pairHold");
    expect(heldCode()).toBe("K7QM2XPA");
  });

  it("main: an iOS browser tab takes the link at launch and watches it only once the app starts", async () => {
    ios(true);
    window.history.replaceState(null, "", "/#/pair?code=K7QM2XPA");
    await launch();
    expect(seen.order).toEqual(["registerWorker"]);
    expect(window.location.hash).toBe("#/pair");
    act(() => {
      fireEvent.click(screen.getByRole("button", { name: "Continue in the browser anyway" }));
    });
    expect(seen.order).toEqual(["registerWorker", "watchPairLink", "startFleet"]);
    expect(seen.routerAt).toEqual({ pathname: "/pair", search: "" });
  });

  it("main: a plain launch changes neither the address nor the app's cookie", async () => {
    ios(false);
    window.history.replaceState(null, "", "/#/s/5");
    await launch();
    expect(window.location.hash).toBe("#/s/5");
    expect(seen.writes).toEqual([]);
    expect(seen.hashAtWorker).toBe("#/s/5");
    expect(seen.routerAt).toEqual({ pathname: "/s/5", search: "" });
  });
});
```

Fails without:
- `main: a pairing link is out of the address before the router is built…`: the `import "@/app/pairHold"` in `main.tsx` moved below `@/panels`, `@/commands` or `@/app/routes` (the router is built from `#/pair?code=…`, so `routerAt` is `{ pathname: "/pair", search: "?code=K7QM-2XPA" }`), or removed (the address and the router keep the code and no cookie is written), or `watchPairLink()` called after `startFleet()` (the order differs).
- `main: an iOS browser tab takes the link at launch and watches it only once the app starts`: `watchPairLink()` called at launch rather than in `start()`.
- `main: a plain launch changes neither the address nor the app's cookie`: `holdPairLink` replacing an address that holds no pairing code, or `adoptNextApp` writing without `?ui=next`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/main.pairing.test.tsx`

Expected: the two link tests fail, the first with `expected { pathname: '/pair', search: '?code=K7QM-2XPA' } to deeply equal { pathname: '/pair', search: '' }` (the router reads the code, since `main.tsx` does not yet import `pairHold`) and the iOS one with `expected [ 'registerWorker' ] to deeply equal [ 'registerWorker', 'watchPairLink', 'startFleet' ]` after Continue is pressed (`routerAt` also still holds the code); the plain-launch test passes.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/main.tsx`, the first import of the app's own goes ahead of the stylesheet and the panels. Replace

```tsx
import { createRoot } from "react-dom/client";

import "./styles/app.css";
```

with

```tsx
import { createRoot } from "react-dom/client";

// First of the app's own imports (spec §26): its evaluation sets the cookie of a launch by ?ui=next
// and takes a pairing link's code out of the address, and app/routes.tsx builds the router from
// the address when a later import reaches it. Keep every import that reaches the router below it.
import "@/app/pairHold";

import "./styles/app.css";
```

Add the import after `import { AddToHome, needsInstall } from "@/app/install";`:

```tsx
import { watchPairLink } from "@/app/pairLink";
```

The `registerWorker();` line and its comment stay as they are: the cookie and the address are settled by the import above, before the worker registers.

After `let stopFleet: (() => void) | undefined;` add

```tsx
let stopPairLink: (() => void) | undefined;
```

In `start()`, replace

```tsx
  stopFleet = startFleet();
```

with

```tsx
  stopPairLink = watchPairLink();
  stopFleet = startFleet();
```

In the `import.meta.hot.dispose` callback, after `stopNotices?.();` add

```tsx
    stopPairLink?.();
```

Format with `pnpm -C internal/web/ui exec prettier --write src/main.tsx src/main.pairing.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/main.pairing.test.tsx src/main.test.tsx` — `Test Files  2 passed (2)`.

Then rebuild the bundle and run the story's gates:

`pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/`

Expected: every command exits 0; `git status --short internal/web/dist` lists the rebuilt entry script and its hashed siblings as changed.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/main.pairing.test.tsx internal/web/dist
git commit -m "feat(web): main takes the pairing link before the router, the worker and the stream" -- internal/web/ui/src/main.tsx internal/web/ui/src/main.pairing.test.tsx internal/web/dist
```

## Story 4: Panels scroll through a `ScrollPane`, and bodies take their session from props

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/scroll.tsx`, `internal/web/ui/src/app/scroll.test.tsx`, `internal/web/ui/src/app/scrollkit.ts`, `internal/web/ui/src/panels/turns/Conversation.tsx`, `internal/web/ui/src/panels/turns/turns.test.tsx`, `internal/web/ui/src/panels/job/JobBody.tsx`, `internal/web/ui/src/panels/job/job.test.tsx`, `internal/web/ui/src/panels/flow/FlowDocBody.tsx`, `internal/web/ui/src/panels/flow/doc.test.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/session/session.test.tsx`, `internal/web/ui/src/panels/agent/Agent.tsx`, `internal/web/ui/src/panels/changes/index.tsx`, `internal/web/ui/src/panels/flow/FlowBody.tsx`, `internal/web/ui/src/panels/flow/FlowStoryBody.tsx`, `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/mockup/MockupBody.tsx`, `internal/web/ui/src/panels/turns/index.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `ScrollPane`, `windowPane`, `elementPane`, `ScrollPaneProvider`, `useScrollPane`, `NoSession`'s `sessionId` prop; uses `keepScroll`, `keptScroll`, `useIsPending`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** brief (a seam over the window; the phone's scroll behaviour is pinned by the existing tests, and each pane behaviour by a named test)

Three panels scroll the window today: the conversation follows the bottom and keeps each session's scroll, the job log follows its end, and the flow document jumps to a heading below the page's pinned header. This story adds `app/scroll.tsx`, a seam over "the thing that scrolls" (spec §12), and moves those three panels onto it. With no provider the pane is the window, so the phone behaves exactly as today and the existing tests keep passing, except that `doc.test.tsx`'s `scrollTo` expectations move to the two-argument form `windowPane` calls. New tests run each behaviour inside an `elementPane` over a stub scroller. `NoSession` takes the session id as a prop instead of reading the route (spec §13), and every caller passes its own. This story is brief-form: the code is not written here, only the behaviour, the contract names and each test with the code whose removal turns it red. Stories 6 and 10 draw their panes with `elementPane(scroller, null)` (the pinned head sits outside the scroller) and render a panel's Body only once the scroller has mounted; this story's `elementPane` still supports a head inside the scroller. No landmine marker appears in this story's code.

### Task 4.1: `app/scroll.tsx` and its test kit

**Files:**
- Create: `internal/web/ui/src/app/scroll.tsx`
- Create: `internal/web/ui/src/app/scrollkit.ts`, `internal/web/ui/src/app/scroll.test.tsx`

Behaviour. `app/scroll.tsx` defines, with exactly the contract's names and signatures:

- `interface ScrollPane { top: () => number; viewport: () => number; height: () => number; scrollTo: (top: number) => void; topFor: (el: Element) => number; subscribe: (listener: () => void) => () => void }`.
- `const windowPane: ScrollPane`: `top` is `window.scrollY`, `viewport` `window.innerHeight`, `height` `document.documentElement.scrollHeight`; `scrollTo(top)` calls `window.scrollTo(0, top)` (the two-argument form the conversation's and the job log's tests already pin); `topFor(el)` is `el.getBoundingClientRect().top + window.scrollY` minus the height of the element matching `[data-slot="header"]` (0 when there is none, found at each call), and at least 0; `subscribe(listener)` adds a passive `scroll` listener to `window` and returns the function that removes it.
- `function elementPane(scroller: HTMLElement, head: HTMLElement | null): ScrollPane`: `top` is `scroller.scrollTop`, `viewport` `scroller.clientHeight`, `height` `scroller.scrollHeight`; `scrollTo(top)` assigns `scroller.scrollTop`; `topFor(el)` is `el.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop` minus `head`'s `getBoundingClientRect().height` (nothing when `head` is `null`), and at least 0; `subscribe` adds a passive `scroll` listener to `scroller` (never to `window`) and returns its remover.
- `function ScrollPaneProvider(props: { pane: ScrollPane; children: ReactNode }): ReactElement` and `function useScrollPane(): ScrollPane`: a React context whose default value is `windowPane`.

`app/scrollkit.ts` is the test helper the panels' tests share (the precedent is `commands/testkit.ts`; it is imported only by tests). It exports `interface Scroller { el: HTMLDivElement; scrolls: number[]; set: (over: { height?: number; viewport?: number; top?: number }) => void; place: (top: number) => void }` and `function stubScroller(init: { height: number; viewport: number; top?: number }): Scroller`. `el` is a detached `div` whose `scrollHeight`, `clientHeight` and `scrollTop` are accessors over the helper's own numbers (`top` starts at `init.top ?? 0`); assigning `scrollTop` appends the value to `scrolls` and sets the number; `set` changes the numbers without an event or a record; `place(top)` sets `top` without recording it and dispatches a `scroll` event on `el`, as the browser does when the reader moves.

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/app/scroll.test.tsx` with these tests, each rendering or calling only what its name says (window values set with `Object.defineProperty` on `window` and `document.documentElement`, removed in `afterEach` with `Reflect.deleteProperty`; element rectangles given by `vi.spyOn(el, "getBoundingClientRect")` on the element itself):

- `windowPane reads the window`: with `scrollY` 120, `innerHeight` 800 and `scrollHeight` 2000, `windowPane.top()`, `.viewport()` and `.height()` return 120, 800 and 2000; `windowPane.scrollTo(300)` calls a spied `window.scrollTo` once with `(0, 300)`.
- `windowPane.topFor puts an element just below the page's header, never above 0`: a `header` element with `data-slot="header"` of height 120 in the body, `scrollY` 100, a child whose rect `top` is 500 gives 480; a child whose rect `top` is 10 with `scrollY` 0 gives 0; with the header removed the first child gives 600.
- `windowPane.subscribe calls the listener on each window scroll until stopped`: two `fireEvent.scroll(window)` calls make a `vi.fn()` listener run twice; after the returned stop is called a third makes no call.
- `elementPane reads the scroller`: `stubScroller({ height: 2000, viewport: 600, top: 40 })` gives `top()` 40, `viewport()` 600, `height()` 2000; `scrollTo(700)` leaves `scrolls` equal to `[700]` and `top()` 700, and the spied `window.scrollTo` is not called.
- `elementPane.topFor measures from the scroller, below its head when it has one`: the scroller's rect `top` 200, `scrollTop` 300, a child's rect `top` 1400: `elementPane(el, null).topFor(child)` is 1500; with a head element whose rect `height` is 70 it is 1430; a child whose rect `top` is 10 with the scroller at `scrollTop` 0 gives 0 for both heads.
- `elementPane.subscribe follows the scroller, not the window`: `place(10)` makes the listener run once; `fireEvent.scroll(window)` makes no further call; after stop, `place(20)` makes none.
- `useScrollPane is the window pane without a provider and the given pane inside one`: a probe component rendering `String(useScrollPane() === windowPane)` shows `true` bare, and `false` inside `<ScrollPaneProvider pane={elementPane(stubScroller(...).el, null)}>`; the provided pane is the very object passed.

Fails without:
- `windowPane reads the window`: `windowPane.scrollTo` calling `window.scrollTo({ top })`, or any of the three readers reading another metric.
- `windowPane.topFor puts an element just below the page's header, never above 0`: the header height not subtracted, or the result not clamped at 0, or `scrollY` left out.
- `windowPane.subscribe calls the listener on each window scroll until stopped`: the remover not removing the listener.
- `elementPane reads the scroller`: `elementPane` reading the window's metrics, or `scrollTo` calling `window.scrollTo`.
- `elementPane.topFor measures from the scroller, below its head when it has one`: the scroller's own rect `top` or `scrollTop` left out, or the head's height not subtracted, or the clamp removed.
- `elementPane.subscribe follows the scroller, not the window`: the listener added to `window`.
- `useScrollPane is the window pane without a provider and the given pane inside one`: the context's default other than `windowPane`, or the provider not passing its `pane`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/scroll.test.tsx`

Expected: the file fails to load: `Failed to resolve import "./scroll" from "src/app/scroll.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement**

Create `app/scroll.tsx` and `app/scrollkit.ts` as described above, with the comment lines the repo's modules carry (what each export is, one line). `ScrollPaneProvider` and `useScrollPane` use `createContext(windowPane)`. Run `pnpm -C internal/web/ui exec prettier --write src/app/scroll.tsx src/app/scrollkit.ts src/app/scroll.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/app/scroll.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`

Expected: `Test Files  1 passed (1)`; typecheck and lint exit 0.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/app/scroll.tsx internal/web/ui/src/app/scrollkit.ts internal/web/ui/src/app/scroll.test.tsx
git commit -m "feat(web): a ScrollPane seam over the window and over a scrolling element" -- internal/web/ui/src/app/scroll.tsx internal/web/ui/src/app/scrollkit.ts internal/web/ui/src/app/scroll.test.tsx
```

### Task 4.2: The conversation scrolls through the pane

**Files:**
- Modify: `internal/web/ui/src/panels/turns/Conversation.tsx:14-24`, `internal/web/ui/src/panels/turns/Conversation.tsx:54-86`
- Test: `internal/web/ui/src/panels/turns/turns.test.tsx`

Behaviour. `Conversation` reads `const pane = useScrollPane()` and uses it wherever it used the window, with no change of rule:

- `measure` takes the pane: `height` is `pane.height()`, `atBottom` is `pane.viewport() + pane.top() >= height - BOTTOM_MARGIN`, `fromBottom` is `height - pane.top()`.
- The kept-scroll restore calls `pane.scrollTo(kept)`; an anchoring page calls `pane.scrollTo(pane.height())` for a `newest` landing or a `refresh` landing with the reader at the bottom, and `pane.scrollTo(pane.height() - metrics.current.fromBottom)` for an `earlier` landing.
- The scroll recorder calls `keepScroll(session.id, pane.top())` and `measure(pane)` from `pane.subscribe(...)`, whose stop is the effect's cleanup; the effects that use the pane list it in their dependencies (the layout effects that run once per session id add `pane`).

- [ ] **Step 1: Write the failing tests**

In `turns.test.tsx`, add `import { elementPane, ScrollPaneProvider, type ScrollPane } from "@/app/scroll";` and `import { stubScroller, type Scroller } from "@/app/scrollkit";`. Give `show` an optional second parameter `pane?: ScrollPane`: when given, `<Screen />` is wrapped in `<ScrollPaneProvider pane={pane}>` inside the `QueryClientProvider`. Add a describe `Conversation's scroll inside a pane`, whose `beforeEach` runs `stubPage()`, then sets `pageHeight = 9000`, then makes `s = stubScroller({ height: 2000, viewport: 600 })` and `pane = elementPane(s.el, null)` (one pane for the whole test, so the effects' dependency is stable); the window's numbers differ from the scroller's on purpose (page height 9000 and `innerHeight` 800 against 2000 and 600), so a reading of the window gives other numbers. Every test also asserts the page's `scrollTo` mock was never called.

- `the first page lands at the bottom of the pane`: one page of turns; after `settle()`, `s.scrolls` contains 2000.
- `an earlier page keeps the reader's place in the pane`: a first page `[5, 6]` with `earlier: 5`; `s.place(300)`; the held page is replaced by `[3, 4]` after `s.set({ height: 3200 })` and a click on "↑ earlier turns"; the last entry of `s.scrolls` is 1500 (`3200 - (2000 - 300)`).
- `a refresh follows the pane's bottom for a reader within 48 px of it`: after the first page, `s.place(1352)` (`1352 + 600 = 2000 - 48`), `s.scrolls.length = 0`, `s.set({ height: 2400 })`, a `last_message` change with a page `[3, 4]`: `s.scrolls` equals `[2400]`.
- `a refresh leaves a reader 49 px from the pane's bottom where they are`: the same with `s.place(1351)`: `s.scrolls` stays empty.
- `a reset page lands at the bottom of the pane wherever the reader is`: after `s.place(0)` and `s.set({ height: 2600 })`, a reset page `[9]` with `conversation: "c2"`: `s.scrolls` contains 2600.
- `a conversation kept in the cache comes back where it was left in the pane`: after the first page, `s.place(640)`; unmount; `s.scrolls.length = 0`; showing it again with the same pane puts 640 in `s.scrolls` before any `settle()`, and `settle()` adds no 2000.
- `leaving the conversation stops recording the pane's scroll`: `s.place(640)`; unmount; `s.place(5)`; `keptScroll(1)` is 640.
- `the window's scroll is not the pane's`: after `s.place(640)`, `pageY = 77` and `fireEvent.scroll(window)`: `keptScroll(1)` is still 640.

Fails without:
- `the first page lands at the bottom of the pane`: the anchoring still calling `window.scrollTo` (the pane's `scrolls` stays empty).
- `an earlier page keeps the reader's place in the pane`: `fromBottom` measured from the window's height and position (the result is then 9000-based, not 1500).
- `a refresh follows the pane's bottom for a reader within 48 px of it`: `atBottom` using `window.innerHeight` (800) instead of `pane.viewport()` (600): the 49 px case then follows too.
- `a refresh leaves a reader 49 px from the pane's bottom where they are`: the same, from the other side.
- `a reset page lands at the bottom of the pane wherever the reader is`: the `newest` landing scrolling the window.
- `a conversation kept in the cache comes back where it was left in the pane`: the restore calling `window.scrollTo`, or the kept value read from `window.scrollY`.
- `leaving the conversation stops recording the pane's scroll`: the subscription's stop not returned as the cleanup.
- `the window's scroll is not the pane's`: the recorder subscribed on `window` instead of `pane.subscribe`.

The existing `Conversation's scroll` tests, which run with no provider, stay as they are.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/turns/turns.test.tsx`

Expected: the new describe fails (for the first test `expected [] to include 2000`, since the conversation still scrolls the window); every earlier test passes.

- [ ] **Step 3: Implement**

Change `Conversation.tsx` as described, importing `useScrollPane` and the type `ScrollPane` from `@/app/scroll`; `measure(pane: ScrollPane)` replaces `measure()`. Run `pnpm -C internal/web/ui exec prettier --write src/panels/turns/Conversation.tsx src/panels/turns/turns.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/turns/turns.test.tsx && pnpm -C internal/web/ui run typecheck`

Expected: `Test Files  1 passed (1)`, the old scroll tests unchanged and green, typecheck exits 0.

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor(web): the conversation scrolls through the ScrollPane" -- internal/web/ui/src/panels/turns/Conversation.tsx internal/web/ui/src/panels/turns/turns.test.tsx
```

### Task 4.3: The job log scrolls through the pane

**Files:**
- Modify: `internal/web/ui/src/panels/job/JobBody.tsx:105-122`
- Test: `internal/web/ui/src/panels/job/job.test.tsx`

Behaviour. `JobScreen` reads `const pane = useScrollPane()`. `follow` is `pane.viewport() + pane.top() >= pane.height() - 40`, recomputed from `pane.subscribe(...)`'s listener (the stop is the effect's cleanup, the effect lists `pane`); the effect that follows new output calls `pane.scrollTo(pane.height())` while `follow.current` is true.

- [ ] **Step 1: Write the failing tests**

In `job.test.tsx`, import `elementPane`, `ScrollPaneProvider` from `@/app/scroll` and `stubScroller` from `@/app/scrollkit`. Add a describe `JobBody following the log inside a pane` with the same `useJobLog` mock helpers the existing `JobBody following the log` describe uses (`more`, a fresh element per rerender), a spy on `window.scrollTo`, the window's `innerHeight` 800 and `scrollHeight` 9000, and `s = stubScroller({ height: 5000, viewport: 600 })`, `pane = elementPane(s.el, null)`; the job renders through `<ScrollPaneProvider pane={pane}>` around `<JobBody sessionId={1} params={{ id: "1", job: "214" }} search={{}} />` with a running job 214 seeded as `show` does. Each test asserts the window's `scrollTo` mock was never called.

- `opens at the bottom of the pane when the screen opens with output`: `s.scrolls` equals `[5000]`.
- `follows new output while the reader is within 40 px of the pane's bottom`: `s.place(5000 - 600 - 40)`, `s.scrolls.length = 0`, a new log text and a rerender: `s.scrolls` equals `[5000]`.
- `leaves a reader more than 40 px above the pane's bottom, and follows again once they are back`: `s.place(5000 - 600 - 41)`, a new log and a rerender leave `s.scrolls` empty; `s.place(5000 - 600)` and another new log give `[5000]`.

Fails without:
- `opens at the bottom of the pane when the screen opens with output`: the follow effect still calling `window.scrollTo`.
- `follows new output while the reader is within 40 px of the pane's bottom`: the follow effect still calling `window.scrollTo` or reading the window's height.
- `leaves a reader more than 40 px above the pane's bottom, and follows again once they are back`: the scroll listener on `window` instead of `pane.subscribe` (the pane's `place` never reaches it, so `follow` stays true and the 41 px reader is followed anyway), or `follow` computed from `window.innerHeight` and `window.scrollY`.

The existing `JobBody following the log` tests stay as they are.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/job/job.test.tsx`

Expected: the three new tests fail (`expected [] to deeply equal [ 5000 ]`); the older tests pass.

- [ ] **Step 3: Implement**

Change `JobScreen` as described, importing `useScrollPane` from `@/app/scroll`. Run `pnpm -C internal/web/ui exec prettier --write src/panels/job/JobBody.tsx src/panels/job/job.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/job/job.test.tsx && pnpm -C internal/web/ui run typecheck`

Expected: `Test Files  1 passed (1)`; typecheck exits 0.

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor(web): the job log follows its end through the ScrollPane" -- internal/web/ui/src/panels/job/JobBody.tsx internal/web/ui/src/panels/job/job.test.tsx
```

### Task 4.4: The flow document's jump goes through the pane

**Files:**
- Modify: `internal/web/ui/src/panels/flow/FlowDocBody.tsx:120-132`
- Test: `internal/web/ui/src/panels/flow/doc.test.tsx`

Behaviour. `FlowDocBody` reads `const pane = useScrollPane()`; `jump(index)` finds the heading element as before and calls `pane.scrollTo(pane.topFor(el))`; `jump`'s `useCallback` dependency list is `[pane]` (it was `[]`), and nothing reads `document.querySelector('[data-slot="header"]')` or `window` any more (the window pane's `topFor` does).

- [ ] **Step 1: Write the failing tests and move the existing expectations**

First, the two-argument form. List the expectations to rewrite: `grep -n 'toHaveBeen.*CalledWith({ top' internal/web/ui/src/panels/flow/doc.test.tsx` prints 10 lines (at HEAD: 257, 262, 271, 285, 313, 320, 330, 332, 342, 352). Rewrite each `({ top: E })` as `(0, E)` (for example line 257 becomes `expect(scrollTo).toHaveBeenCalledWith(0, 1000 + 100 * 4 + 100 - 120);` and line 320 becomes `expect(scrollTo).toHaveBeenCalledWith(0, 0);`); the grep then prints no line.

Then, in the `the jump` describe, add `let scroller: Scroller;` and `let paneHead: HTMLElement;` (made in a `beforeEach` after the existing one: `scroller = stubScroller({ height: 4000, viewport: 600, top: 300 })`, `paneHead = document.createElement("div")`), and extend the file's `getBoundingClientRect` mock so that `this === scroller.el` has `top` 200 and `this === paneHead` has `height` 70 (the existing branches for the header and the headings stay). Give `show`'s options an optional `pane?: ScrollPane`: when given, the body is wrapped in `<ScrollPaneProvider pane={pane}>`. Add, in that describe:

- `inside a pane the jump scrolls the pane just below its head, not the window`: `show({ search: { at: "§2" }, pane: elementPane(scroller.el, paneHead) })`: `scroller.scrolls` equals `[1000 + 100 * 4 - 200 + 300 - 70]` (1430), and the window's `scrollTo` mock is not called (the window's `scrollY` is 100 in this describe, so a window reading would give another number).
- `a pane with no head subtracts nothing`: the same with `elementPane(scroller.el, null)`: `scroller.scrolls` equals `[1500]`.
- `a heading never lands above the top of the pane`: `headingTop = () => 10`, `scroller.set({ top: 0 })`, rendered with `elementPane(scroller.el, paneHead)`: `scroller.scrolls` equals `[0]` (`10 - 200 + 0 - 70` is negative).
- `the sections index's buttons jump inside the pane`: with `elementPane(scroller.el, null)` and no `at`, clicking "sections" then the button "3. Costs" leaves `scroller.scrolls` equal to `[1000 + 100 * 5 - 200 + 300]` (1600) and the window's `scrollTo` mock not called.

Fails without:
- `inside a pane the jump scrolls the pane just below its head, not the window`: `jump` still reading the page's header and calling `window.scrollTo` (the pane's `scrolls` stays empty).
- `a pane with no head subtracts nothing`: the head's height always subtracted, or the page header's height used for a pane.
- `a heading never lands above the top of the pane`: the clamp at 0 missing in `elementPane.topFor`.
- `the sections index's buttons jump inside the pane`: the button path calling a separate window jump.
- The ten rewritten expectations: `windowPane.scrollTo` not calling `window.scrollTo(0, top)` (they keep pinning the phone's jump, 120 px header included).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/flow/doc.test.tsx`

Expected: the ten rewritten expectations fail (`expected "spy" to be called with arguments: [ 0, 1380 ]`, the component still passing `{ top: 1380 }`) and the four pane tests fail.

- [ ] **Step 3: Implement**

Change `FlowDocBody.tsx` as described, importing `useScrollPane` from `@/app/scroll`. Run `pnpm -C internal/web/ui exec prettier --write src/panels/flow/FlowDocBody.tsx src/panels/flow/doc.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/flow/doc.test.tsx && pnpm -C internal/web/ui run typecheck`

Expected: `Test Files  1 passed (1)`; typecheck exits 0.

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor(web): the flow document's heading jump goes through the ScrollPane" -- internal/web/ui/src/panels/flow/FlowDocBody.tsx internal/web/ui/src/panels/flow/doc.test.tsx
```

### Task 4.5: `NoSession` takes the session id from props

**Files:**
- Modify: `internal/web/ui/src/session/NoSession.tsx:1-15`
- Modify: `internal/web/ui/src/panels/agent/Agent.tsx`, `internal/web/ui/src/panels/changes/index.tsx`, `internal/web/ui/src/panels/flow/FlowBody.tsx`, `internal/web/ui/src/panels/flow/FlowStoryBody.tsx`, `internal/web/ui/src/panels/flow/FlowDocBody.tsx`, `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/job/JobBody.tsx`, `internal/web/ui/src/panels/mockup/MockupBody.tsx`, `internal/web/ui/src/panels/turns/index.tsx` (one-line edits)
- Test: `internal/web/ui/src/session/session.test.tsx`

Behaviour. `NoSession(props: { sessionId: number | undefined })` says "Loading…" when `useIsPending(props.sessionId)` and otherwise "This session is gone." (the same `role="status"` paragraph); it imports neither `panelProps` nor `useRoute`. The callers, which all hold `props.sessionId` (`PanelProps`'s field), pass it:

| File | Sites now | Rewrite |
|---|---|---|
| `panels/agent/Agent.tsx` | 233 | `<NoSession />` to `<NoSession sessionId={props.sessionId} />` |
| `panels/changes/index.tsx` | 14 | same |
| `panels/flow/FlowBody.tsx` | 432 | same |
| `panels/flow/FlowStoryBody.tsx` | 285 | same |
| `panels/flow/FlowDocBody.tsx` | 140 | same |
| `panels/info/index.tsx` | 14 | same |
| `panels/job/JobBody.tsx` | 167 | same |
| `panels/mockup/MockupBody.tsx` | 45, 87 | same (both bodies take `props`) |
| `panels/turns/index.tsx` | 16, 20 | same |

The list of sites to rewrite is `grep -rn '<NoSession />' internal/web/ui/src --include='*.tsx' --exclude='*.test.tsx'`, which prints 11 lines at the start of this task (line numbers after Tasks 4.2 to 4.4 may differ by a few in `JobBody.tsx` and `FlowDocBody.tsx`) and none at its end.

- [ ] **Step 1: Write the failing tests**

In `session.test.tsx`, rewrite the three `render(<NoSession />)` calls and their cases:

- `NoSession says the session is gone` (line 126): `render(<NoSession sessionId={undefined} />)`; same assertion.
- `NoSession says Loading… for the pending session` (rename from "for the route's pending session"): `usePending.setState({ id: 9 })`, `render(<NoSession sessionId={9} />)`; asserts "Loading…" and no "This session is gone."; the `window.history.replaceState(null, "", "#/s/9")` line goes (the route is no input).
- `NoSession still says gone for an unpending session, another session's pending id, and no session id`: the cases become `[pending id, sessionId prop]` pairs `[null, 9]`, `[8, 9]` and `[9, undefined]`, each `render(<NoSession sessionId={prop} />)` with `usePending.setState({ id })` and the "This session is gone." assertion; the hash loop variable goes.
- New `NoSession reads no route`: with the hash at `#/s/9` and `usePending.setState({ id: 9 })`, `<NoSession sessionId={undefined} />` says "This session is gone."; with the hash at `#/` and the same pending id, `<NoSession sessionId={9} />` says "Loading…".

Fails without:
- `NoSession reads no route`: `NoSession` still deriving the id from `useRoute()` (the first case then says Loading…, the second gone).
- `NoSession says Loading… for the pending session`: the prop ignored, with the route at `/` (the default hash in this file) the component says gone.
- `NoSession still says gone for … no session id`: `useIsPending` given the route's id (the `[9, undefined]` case, with the hash left from the previous test, would say Loading…) or `undefined` treated as pending.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/session/session.test.tsx`

Expected: type errors are not reported by vitest, so the run shows the failures: `NoSession reads no route` and `NoSession says Loading… for the pending session` fail (the component reads the route, which is `#/` or `#/s/9` not matching the prop).

- [ ] **Step 3: Implement**

Change `NoSession.tsx` and rewrite the 11 sites by the table (one `sed -i 's/<NoSession \/>/<NoSession sessionId={props.sessionId} \/>/'` over the nine non-test files is the mechanical way; the check is the grep above). Run `pnpm -C internal/web/ui exec prettier --write` on the ten source files and `src/session/session.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run`

Expected: typecheck exits 0 (a caller left on `<NoSession />` is a type error: `Property 'sessionId' is missing`), and `Test Files  N passed` for the whole app suite, with no failure.

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor(web): NoSession takes the session id from its props, not the route" -- internal/web/ui/src/session/NoSession.tsx internal/web/ui/src/session/session.test.tsx internal/web/ui/src/panels/agent/Agent.tsx internal/web/ui/src/panels/changes/index.tsx internal/web/ui/src/panels/flow/FlowBody.tsx internal/web/ui/src/panels/flow/FlowStoryBody.tsx internal/web/ui/src/panels/flow/FlowDocBody.tsx internal/web/ui/src/panels/info/index.tsx internal/web/ui/src/panels/job/JobBody.tsx internal/web/ui/src/panels/mockup/MockupBody.tsx internal/web/ui/src/panels/turns/index.tsx
```

### Task 4.6: Rebuild the bundle

**Files:**
- Modify: `internal/web/dist/` (rebuilt)

- [ ] **Step 1: Rebuild**

Run: `pnpm -C internal/web/ui run build`

Expected: the build finishes with no error; `git status --short internal/web/dist` lists the entry script (and any chunk the three panels sit in) as changed, with new hashed names where their content changed.

- [ ] **Step 2: Run the story's gates**

Run: `pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/`

Expected: every command exits 0 (`dist:check` finds the committed bundle equal to a fresh build; the Go budget tests read the new bundle and stay within their budgets: the seam adds a few hundred bytes to the first load).

- [ ] **Step 3: Commit**

```bash
git add -N internal/web/dist
git commit -m "build(web): rebuild the bundle for the ScrollPane and NoSession's prop" -- internal/web/dist
```

## Story 5: A Dialog component, a desktop form of the confirmation sheet, and a placement for the toasts

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/components/ui/dialog.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/components/ui/toast.tsx`, `internal/web/ui/src/app/toasts.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `Dialog`, `DialogContent`, `DialogTitle`, `DialogDescription`, `DialogFooter`, `ConfirmVariant`, `ConfirmSheet`'s `variant`, `ToastPlacement`, `Toaster`'s `placement`; uses `askConfirm`, the Drawer
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** light (an owned shadcn component and two presentational props; the sheet's answer rules keep their tests)
- **Form:** brief (a shadcn component, a variant and a placement)

The desktop draws a confirmation as a centred Dialog and its toasts at the bottom right (spec §11), the phone keeps the Drawer and the top-centre toasts. This story adds the pieces and nothing that chooses between them: `components/ui/dialog.tsx` (Base UI's own Dialog, as `drawer.tsx` is Base UI's own Drawer, so no `<style>` is written under the CSP), `ConfirmSheet`'s `variant`, and `Toaster`'s `placement`. `askConfirm`'s API, its one-open-ask rule and its replaced-ask rule do not change. The only call site, `app/shell.tsx`, passes `"drawer"` and `"top"` until Story 6 lifts the sheet and toaster above both shells. Every existing `<ConfirmSheet />` and `<Toaster />` in the tests gains the required prop. Nothing here is written in the landmine list, so the developer runs at the default effort.

### Task 5.1: The Dialog component

**Files:**
- Create: `internal/web/ui/src/components/ui/dialog.tsx`
- Test: `internal/web/ui/src/components/ui/components.test.tsx`

Defines (contract names, `components/ui/dialog.tsx`): `Dialog`, `DialogContent`, `DialogTitle`, `DialogDescription`, `DialogFooter`. Uses `@base-ui/react/dialog` (`Dialog.Root`, `Portal`, `Backdrop`, `Viewport`, `Popup`, `Title`, `Description`) and `cn` from `@/lib/utils`. No `DialogHeader`, `DialogClose` or `DialogTrigger`: nothing in part 1 draws them.

Behaviour, shaped as `components/ui/drawer.tsx` (read it and keep its comment about Base UI's own component and the CSP):

- `Dialog` is `DialogPrimitive.Root` with `data-slot="dialog"` and every prop passed through (`open`, `onOpenChange`).
- `DialogContent` is Portal, then Backdrop (`data-slot="dialog-backdrop"`, `fixed inset-0 bg-black/60`, the drawer's opacity transition and `data-starting-style` / `data-ending-style` classes), then Viewport (`data-slot="dialog-viewport"`, `fixed inset-0 flex items-center justify-center p-4`), then Popup (`data-slot="dialog-content"`) holding `children` directly. The popup is centred, `w-full max-w-md`, `max-h-[90dvh] overflow-y-auto`, `rounded-xl border border-border bg-card p-4 text-card-foreground outline-none`, and fades and scales in from `data-starting-style` to `data-ending-style` (opacity and a scale of about 0.96, 150 ms). Its `className` is a string or Base UI's state function, merged through `cn` the way `DrawerContent` merges it. It draws no close button and no handle: the sheet's own buttons close it.
- `DialogTitle` and `DialogDescription` are Base UI's `Title` and `Description` with `data-slot="dialog-title"` and `data-slot="dialog-description"` and the classes `DrawerTitle` and `DrawerDescription` have (`text-base font-semibold`; `text-sm text-muted-foreground`), each accepting a string or function `className` as the drawer's do.
- `DialogFooter` is a `div` with `data-slot="dialog-footer"`: a column with `gap-2` and `mt-4` that is, from the `sm` breakpoint, a reversed row that starts at the right (`sm:flex-row-reverse sm:justify-start`), so a footer whose children are written action then Cancel (the sheet's order, which is also its tab order) reads Cancel, action from the left.

**Tests** (in `components.test.tsx`, a `SampleDialog({ open, onOpenChange })` beside `SampleDrawer` drawing a `Dialog` whose `DialogContent` holds a `DialogTitle` "Kill fix-pane-cursor?", a `DialogDescription` "The worktree is stashed first." and a `DialogFooter` with one `Button` "Cancel" that calls `onOpenChange(false)`; import `Dialog`, `DialogContent`, `DialogDescription`, `DialogFooter`, `DialogTitle` from `./dialog`):

- `Dialog: it shows its title and description while open, none while closed, and Escape reports false` (in a `describe("Dialog")`): open, `findByRole("dialog")` has the accessible name "Kill fix-pane-cursor?" and the description text is shown; pressing Escape (`userEvent.keyboard("{Escape}")`) calls `onOpenChange` with `false` as its first argument; a second render with `open={false}` has no `dialog` role.
  Fails without: `Dialog: it shows its title and description while open, none while closed, and Escape reports false`: `DialogTitle` rendering a plain element instead of Base UI's Title (no accessible name), or `Dialog` dropping `open` or `onOpenChange`.
- `Dialog: its parts carry their slots, the popup holds no button of its own, and the footer is a row from sm`: the open dialog's popup is the element with `data-slot="dialog-content"` and contains `data-slot="dialog-title"`, `"dialog-description"` and `"dialog-footer"` elements; the only `button` inside the popup is the sample's Cancel (`getAllByRole("button")` within the popup has length 1); the footer's `className` contains `sm:flex-row-reverse`; the sibling Backdrop (`data-slot="dialog-backdrop"`) exists in the document.
  Fails without: `Dialog: its parts carry their slots, the popup holds no button of its own, and the footer is a row from sm`: a `data-slot` removed from any part, a close button added to `DialogContent`, or the footer's `sm:flex-row-reverse` removed.
- `No Dialog injects a <style> element, even open under the CSP provider` (in the `describe("no <style> element")`): with `document.querySelectorAll("style").length` 0 first, render `<CSPProvider disableStyleElements><SampleDialog open onOpenChange={vi.fn()} /></CSPProvider>` on its own (a Dialog and a Drawer open together make the first inert to role queries), `findByRole("dialog", { name: "Kill fix-pane-cursor?" })`, and the count is still 0.
  Fails without: `No Dialog injects a <style> element, even open under the CSP provider`: a Dialog written as shadcn's vaul-style component that appends a `<style>`, or a Base UI part that writes one the provider no longer disables.

- [ ] **Step 1: Write the failing tests**

Add the three tests above to `internal/web/ui/src/components/ui/components.test.tsx`: the import of `./dialog` beside the `./drawer` import, `SampleDialog` after `SampleDrawer`, the `describe("Dialog")` after `describe("Drawer")`, and the style test inside `describe("no <style> element")`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx`
Expected: the file fails to load: `Failed to resolve import "./dialog" from "src/components/ui/components.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/components/ui/dialog.tsx` as described above, exporting `Dialog`, `DialogContent`, `DialogTitle`, `DialogDescription` and `DialogFooter`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
Expected: the file's tests pass, including the three new ones; typecheck and lint print no errors.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/components/ui/dialog.tsx
git commit -m "feat(web): an owned Dialog on Base UI, for the desktop's confirmations" -- internal/web/ui/src/components/ui/dialog.tsx internal/web/ui/src/components/ui/components.test.tsx
```

### Task 5.2: `ConfirmSheet`'s variant

**Files:**
- Modify: `internal/web/ui/src/app/ConfirmSheet.tsx`
- Modify: `internal/web/ui/src/app/shell.tsx:208`
- Test: `internal/web/ui/src/app/ConfirmSheet.test.tsx`

Defines (contract names, `app/ConfirmSheet.tsx`): `type ConfirmVariant = "drawer" | "dialog"` and `ConfirmSheet(props: { variant: ConfirmVariant })`. Uses `Dialog`, `DialogContent`, `DialogDescription`, `DialogFooter`, `DialogTitle` (Task 5.1), and the Drawer parts the file imports today. `askConfirm`, `testSeam`, `ConfirmSpec` and its field types keep their exports and behaviour.

Behaviour:

- `ConfirmSheet({ variant })` keeps today's store reading, its `last` state (the last ask stays drawn after an answer until the close has finished) and its `onOpenChange` rule (`!open && ask !== null` answers that ask `null`), and draws the one ask in the form `variant` names: `"drawer"` is today's `Drawer` over `DrawerContent`; `"dialog"` is `Dialog` over `DialogContent`. The open/close wiring is written once, not per variant.
- The sheet's content is the same in both: the title and body (the body still `break-words whitespace-pre-line`) over `AskBody`, which keeps its `key={ask.id}`, its field, its over-limit and empty-choice rules and its two buttons. In the drawer the title and body sit in `DrawerHeader` with `DrawerTitle` and `DrawerDescription`; in the dialog in a `div` with the classes `flex flex-col gap-1.5` with `DialogTitle` and `DialogDescription`. `AskBody` takes the variant and draws its buttons in `DrawerFooter` or `DialogFooter` accordingly; the buttons themselves (`size="lg"`, the destructive variant, `answer(ask.id, …)`) are unchanged and shared.
- A `variant` that changes while an ask is open (the window crosses a breakpoint, spec §11) redraws the same ask in the other form: the ask stays open and its buttons still answer it. The text typed into a field is lost with the old form's content, which is accepted.
- `shell.tsx` line 208 `<ConfirmSheet />` becomes `<ConfirmSheet variant="drawer" />` (Story 6 lifts it).

The existing `render(<ConfirmSheet />)` and `<ConfirmSheet />` in the test file (18 of them) become `<ConfirmSheet variant="drawer" />`: `grep -c '<ConfirmSheet />' internal/web/ui/src/app/ConfirmSheet.test.tsx` prints 18 before and 0 after, and `sed -i 's|<ConfirmSheet />|<ConfirmSheet variant="drawer" />|g' internal/web/ui/src/app/ConfirmSheet.test.tsx` does it.

**Tests** (in `ConfirmSheet.test.tsx`, a new `describe("ConfirmSheet as a dialog")` after the existing `describe("ConfirmSheet")`, reusing its `ask`, `kill`, `textSpec`, `merge` and `testSeam`; each renders `<ConfirmSheet variant="dialog" />`):

- `draws a centred dialog and no drawer, with the title, the paragraphs and both actions`: after `ask(kill)`, `findByRole("dialog", { name: "Kill session" })` exists, an element with `data-slot="dialog-content"` holds it and none with `data-slot="drawer-content"` is in the document; the body text `/Removes the worktree \/w\/x/` and the buttons "Kill" and "Cancel" are inside it.
  Fails without: `draws a centred dialog and no drawer, with the title, the paragraphs and both actions`: the `"dialog"` branch of `ConfirmSheet` (a sheet that ignores `variant` draws `drawer-content`).
- `the drawer variant still draws the drawer and no dialog` (in the existing `describe("ConfirmSheet")`): `<ConfirmSheet variant="drawer" />` after `ask(kill)` has `data-slot="drawer-content"` and no `data-slot="dialog-content"`.
  Fails without: `the drawer variant still draws the drawer and no dialog`: the `"drawer"` branch (a sheet that always draws the dialog).
- `the dialog resolves the empty value on its action`, `the dialog resolves null on Cancel` and `the dialog resolves null on Escape`: as the drawer's three tests (`ask(kill)`, click "Kill" → `""`; click "Cancel" → `null`; `{Escape}` → `null`), and after the action the dialog's title is gone (`waitFor`).
  Fails without: `the dialog resolves the empty value on its action`, `the dialog resolves null on Cancel` and `the dialog resolves null on Escape`: the dialog's `onOpenChange` not answering the ask `null`, or the Dialog's buttons not calling `answer`.
- `a text field in the dialog resolves what was typed, and past its limit the action is off with the line showing`: `textSpec(5)`: typing "not now" into "Tell Claude why" shows "That is over the limit." and disables "Deny"; clearing and typing "no" enables it and clicking resolves `"no"`.
  Fails without: `a text field in the dialog resolves what was typed, and past its limit the action is off with the line showing`: `AskBody`'s field and its `over` rule drawn in the dialog branch.
- `a choice field in the dialog resolves the picked option`: `merge`: the select "Merge from" starts on "fix-a (aidanb/fix-a)"; `selectOptions` "fix-b (aidanb/fix-b)" then "Merge" resolves `"9"`.
  Fails without: `a choice field in the dialog resolves the picked option`: the choice branch of `AskBody` in the dialog.
- `a newer ask replaces the open one in the dialog, and the replaced ask's late answer changes nothing`: as the drawer's replaced-ask test: `first = ask(kill)`, `second = ask({ title: "Push", body: "Pushes the branch.", action: "Push", destructive: false })`, `first` resolves `null`, "Pushes the branch." shows and "Kill session" is gone, `testSeam.answer(firstId, "late")` leaves the dialog open, and "Push" resolves `""`.
  Fails without: `a newer ask replaces the open one in the dialog, and the replaced ask's late answer changes nothing`: `ask.id !== id` in `answer`, or `AskBody` keyed by something other than `ask.id`.
- `a variant that changes while an ask is open redraws it in the other form and the ask still resolves`: `render(<ConfirmSheet variant="drawer" />)`, `ask(kill)`, wait for `drawer-content`; `rerender(<ConfirmSheet variant="dialog" />)`; `waitFor` `dialog-content` present and `drawer-content` gone, "Kill session" still shown; click "Kill" → `""`.
  Fails without: `a variant that changes while an ask is open redraws it in the other form and the ask still resolves`: reading `variant` live in `ConfirmSheet` (a variant fixed at first render leaves the drawer).

- [ ] **Step 1: Write the failing tests**

Run the `sed` above, then add the `describe("ConfirmSheet as a dialog")` with its tests, and the drawer-variant test, to `internal/web/ui/src/app/ConfirmSheet.test.tsx`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/ConfirmSheet.test.tsx`
Expected: the existing tests pass (an unknown `variant` prop is ignored at run time) and the dialog tests fail, for example `expected null not to be null` at the `data-slot="dialog-content"` assertion, since the unchanged sheet draws a drawer.

- [ ] **Step 3: Implement**

Rewrite `ConfirmSheet.tsx` as the behaviour above says: add `import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle } from "@/components/ui/dialog";`, export `ConfirmVariant`, split today's `Sheet` into a drawer form and a dialog form that share `AskBody`, and give `ConfirmSheet` the `variant` prop; edit `shell.tsx` line 208 as above.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/app/ConfirmSheet.test.tsx src/app/shell.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
Expected: both files pass, including "mounts the confirmation sheet and the toaster once"; typecheck and lint print no errors.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): ConfirmSheet draws a drawer or a centred dialog by variant" -- internal/web/ui/src/app/ConfirmSheet.tsx internal/web/ui/src/app/ConfirmSheet.test.tsx internal/web/ui/src/app/shell.tsx
```

### Task 5.3: `Toaster`'s placement, and the rebuilt bundle

**Files:**
- Modify: `internal/web/ui/src/components/ui/toast.tsx`
- Modify: `internal/web/ui/src/app/shell.tsx:209`
- Test: `internal/web/ui/src/components/ui/components.test.tsx`
- Modify: `internal/web/dist/` (rebuilt)

Defines (contract names, `components/ui/toast.tsx`): `type ToastPlacement = "top" | "bottom-right"` and `Toaster(props: { placement: ToastPlacement })`. `toastManager` and the toasts' look keep their exports and behaviour.

Behaviour:

- `Toaster({ placement })` draws its `Toast.Viewport` with `data-slot="toast-viewport"` and `data-placement={placement}`. `"top"` keeps today's classes exactly (`fixed top-[max(0.5rem,env(safe-area-inset-top))] left-1/2 z-50 flex w-[min(100vw-1rem,24rem)] -translate-x-1/2 flex-col gap-2 outline-none`). `"bottom-right"` is `fixed right-4 bottom-4 z-50 flex w-[min(100vw-2rem,24rem)] flex-col gap-2 outline-none`: anchored to the window's right and bottom, no centring translate, no safe-area inset (desktop has none).
- The viewport's class for each placement is a constant written once (a `Record<ToastPlacement, string>`), and `ToastList` takes the placement too: a toast enters from above at the top (`data-starting-style:-translate-y-2` as today) and from below at the bottom right (`data-starting-style:translate-y-2`).
- `shell.tsx` line 209 `<Toaster />` becomes `<Toaster placement="top" />` (Story 6 lifts it).

The two `<Toaster />` renders in `components.test.tsx` (lines 238 and 277) become `<Toaster placement="top" />`: `grep -c '<Toaster />' internal/web/ui/src/components/ui/components.test.tsx` prints 2 before and 0 after.

**Tests** (in `components.test.tsx`, inside `describe("Toaster")`):

- `Toaster: the top placement is centred at the top, and the bottom-right one is anchored to the right and bottom`: for each placement in turn (`it.each`), render `<Toaster placement={placement} />`, add a toast through `toastManager` (`timeout: 0`), wait for its title, and read the viewport (`document.querySelector('[data-slot="toast-viewport"]')`): its `data-placement` equals the placement; for `"top"` its class contains `left-1/2` and `top-[max(0.5rem,env(safe-area-inset-top))]` and not `right-4`; for `"bottom-right"` it contains `right-4` and `bottom-4` and neither `left-1/2` nor `-translate-x-1/2`.
  Fails without: `Toaster: the top placement is centred at the top, and the bottom-right one is anchored to the right and bottom`: `placement` ignored (one class for both), or the bottom-right class keeping the centring.
- `Toaster: an error toast is styled and dismisses in the bottom-right placement too`: render `placement="bottom-right"`, add an error toast "Could not push" (`type: "error"`, `timeout: 0`), its title has class `text-error`, and its "Dismiss" button removes it.
  Fails without: `Toaster: an error toast is styled and dismisses in the bottom-right placement too`: `ToastList` not drawn under the `"bottom-right"` branch (a list that exists only for `"top"`), or the dismiss control missing there.

- [ ] **Step 1: Write the failing tests**

Run `sed -i 's|<Toaster />|<Toaster placement="top" />|g' internal/web/ui/src/components/ui/components.test.tsx`, then add the two tests to `internal/web/ui/src/components/ui/components.test.tsx`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx`
Expected: the bottom-right case of the first test fails (`expected 'fixed top-[max(…' to contain 'right-4'`, since the unchanged viewport has one class for every placement); the other existing tests pass.

- [ ] **Step 3: Implement**

Edit `toast.tsx` as the behaviour above says (a `ToastPlacement` type, a `Record<ToastPlacement, string>` of viewport classes, `placement` passed to `ToastList`, the two data attributes), and `shell.tsx` line 209.

- [ ] **Step 4: Run it to see it pass, and rebuild the bundle**

Run: `pnpm -C internal/web/ui run build && pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/`
Expected: the build writes `internal/web/dist/` (the old `index-*.js` and `index-*.css` replaced by new hashes); `check` passes (typecheck, lint, format, every Vitest file, `node --check public/sw.js`); `dist:check` prints no difference; the Go tests pass, including `TestDist_FirstLoadIsWithinBudget`.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "feat(web): Toaster takes a placement, top or bottom right; rebuild the bundle" -- internal/web/ui/src/components/ui/toast.tsx internal/web/ui/src/components/ui/components.test.tsx internal/web/ui/src/app/shell.tsx internal/web/dist
```

## Story 6: The layout hook, the desktop preferences, and the shell switch with the desktop's middle pane

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/viewport.ts`, `internal/web/ui/src/app/viewport.test.ts`, `internal/web/ui/src/app/desktopState.ts`, `internal/web/ui/src/app/desktopState.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/ui/src/test-layout.ts`, `internal/web/ui/src/test-setup.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
- **Interfaces:** exposes `Layout`, `DESK2_MIN`, `DESK3_MIN`, `layoutOf`, `useLayout`, `DESKTOP_KEY`, `DesktopSlot`, `DesktopPrefs`, `readDesktopPrefs`, `DesktopState`, `useDesktop`, `PhoneShell`, `DesktopShell`, `setViewportWidth`, `PanelHead`, the desktop slots, the exported `Boundary`, `TABBED`, `tabOf` and `resetKey`; uses `elementPane`, `ScrollPaneProvider` (Story 4), `ConfirmSheet`'s `variant` and `Toaster`'s `placement` (Story 5), `PanelHost`, `SessionTabs`, `panelFor`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** full (bounds: a stored value read back from localStorage, and the lazy chunk the budget test pins)

The app becomes one app with two frames (spec §4 to §6, §9, §11). `app/viewport.ts` reads the window's width through `matchMedia` (`phone` under 1024px, `desk2` to 1279px, `desk3` from 1280px) and re-reads it as the window resizes; `app/desktopState.ts` holds the desktop's preferences in `loom.desktop`, reads anything malformed as the default, follows another window's write through the `storage` event, and writes back unchanged the keys and slot fields it does not own. `app/shell.tsx` splits today's frame into `PhoneShell` and a new `AppShell` that draws `PhoneShell` on the phone layout, for pairing and for the splash with no snapshot, and otherwise the lazy `DesktopShell` (`desktop/DesktopShell.tsx`, a chunk of its own, so the phone's first load does not grow); the one `ConfirmSheet` and `Toaster` are mounted by `AppShell` above both, in the layout's form (`"drawer"`/`"top"` on the phone, `"dialog"`/`"bottom-right"` on desktop), so a resize keeps an open confirmation and the toasts. `DesktopShell` draws the route's middle: the panel's Head, the session's tabs and its Strip pinned above a scroller, the panel's Body inside the scroller under an `elementPane` (its pinned head sits outside the scroller, so `elementPane`'s `head` is `null`), drawn only once the scroller has mounted so no desktop panel ever reads the window; conversation text keeps a 48rem measure; the full mockup draws full-window with no chrome. The head rows the phone's header draws move into an exported `PanelHead` that both frames use, so the Head, the tabs and the Strip are written once. Test-only, `src/test-layout.ts` exports `setViewportWidth(width)`, which `src/test-setup.ts` calls with 390 before every test, so every test runs on the phone unless it calls it (happy-dom's own 1024px window would read `desk2`). No code here holds a landmine marker, so the developer runs at the default effort.

### Task 6.1: The layout hook and the test viewport

**Files:**
- Create: `internal/web/ui/src/app/viewport.ts`
- Create: `internal/web/ui/src/test-layout.ts`
- Modify: `internal/web/ui/src/test-setup.ts`
- Create: `internal/web/ui/src/app/viewport.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/app/viewport.test.ts`:

```ts
import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { setViewportWidth, viewportWatchers } from "@/test-layout";

import { DESK2_MIN, DESK3_MIN, layoutOf, useLayout, type Layout } from "./viewport";

describe("layoutOf", () => {
  it("the breakpoints are 1024px and 1280px", () => {
    expect(DESK2_MIN).toBe(1024);
    expect(DESK3_MIN).toBe(1280);
  });

  it.each<[number, Layout]>([
    [0, "phone"],
    [390, "phone"],
    [1023, "phone"],
    [1024, "desk2"],
    [1279, "desk2"],
    [1280, "desk3"],
    [2560, "desk3"],
  ])("layoutOf(%i) is %s", (width, want) => {
    expect(layoutOf(width)).toBe(want);
  });
});

describe("useLayout", () => {
  it.each<[number, Layout]>([
    [390, "phone"],
    [1023, "phone"],
    [1024, "desk2"],
    [1279, "desk2"],
    [1280, "desk3"],
    [1920, "desk3"],
  ])("reads %ipx as %s", (width, want) => {
    setViewportWidth(width);
    const { result } = renderHook(() => useLayout());
    expect(result.current).toBe(want);
  });

  it("follows the window as it resizes, in both directions", () => {
    const { result } = renderHook(() => useLayout());
    expect(result.current).toBe("phone");
    setViewportWidth(1100);
    expect(result.current).toBe("desk2");
    setViewportWidth(1300);
    expect(result.current).toBe("desk3");
    setViewportWidth(1000);
    expect(result.current).toBe("phone");
  });

  it("stops listening once it unmounts", () => {
    const before = viewportWatchers();
    const { unmount } = renderHook(() => useLayout());
    expect(viewportWatchers()).toBeGreaterThan(before);
    unmount();
    expect(viewportWatchers()).toBe(before);
  });

  it("reads a matchMedia that cannot listen, and throws nothing", () => {
    for (const [matches, want] of [
      [false, "phone"],
      [true, "desk3"],
    ] as const) {
      const spy = vi
        .spyOn(window, "matchMedia")
        .mockImplementation((query: string) => ({ matches, media: query }) as MediaQueryList);
      try {
        const { result, unmount } = renderHook(() => useLayout());
        expect(result.current).toBe(want);
        unmount();
      } finally {
        spy.mockRestore();
      }
    }
  });

  it("is the phone where there is no matchMedia", () => {
    const original = Object.getOwnPropertyDescriptor(window, "matchMedia");
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: undefined,
    });
    try {
      const { result } = renderHook(() => useLayout());
      expect(result.current).toBe("phone");
    } finally {
      if (original !== undefined) Object.defineProperty(window, "matchMedia", original);
    }
  });
});
```

`setViewportWidth` and `viewportWatchers` come from the test-only module the next step creates, so the file does not load yet. `setViewportWidth` is the contract's; `viewportWatchers` is this story's own, a count of the change listeners the fake `matchMedia` holds.

Fails without: `layoutOf(%i) is %s` cases: `>=` written `>` at either breakpoint (1024 reads `phone`, 1280 reads `desk2`).
Fails without: `reads %ipx as %s`: `read` skipping the `DESK3_QUERY` check (1280 reads `desk2`) or the `DESK2_QUERY` one.
Fails without: `follows the window as it resizes, in both directions`: `subscribe` registering no `change` listener (the hook keeps its first value).
Fails without: `stops listening once it unmounts`: the cleanup's `removeEventListener` calls (the count stays up).
Fails without: `reads a matchMedia that cannot listen, and throws nothing`: the `typeof list.addEventListener === "function"` guard in `subscribe` and in its cleanup (`addEventListener is not a function`, which `main.test.tsx`'s own `matchMedia` mock would hit when it launches the app).
Fails without: `is the phone where there is no matchMedia`: the `typeof window.matchMedia !== "function"` guards in `read` and `subscribe`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/viewport.test.ts`
Expected: the file fails to load: `Failed to resolve import "@/test-layout" from "src/app/viewport.test.ts". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/app/viewport.ts`:

```ts
// The layout the window's width gives (spec §4): the phone's one column under 1024px, `desk2` from
// 1024px to 1279px (a sidebar and the middle) and `desk3` from 1280px (the inspector too). It is
// read from matchMedia, so a resize swaps the layout live.
import { useSyncExternalStore } from "react";

export type Layout = "phone" | "desk2" | "desk3";

export const DESK2_MIN = 1024;
export const DESK3_MIN = 1280;

// layoutOf is the layout a window `width` pixels wide has.
export function layoutOf(width: number): Layout {
  if (width >= DESK3_MIN) return "desk3";
  if (width >= DESK2_MIN) return "desk2";
  return "phone";
}

const DESK2_QUERY = `(min-width: ${DESK2_MIN}px)`;
const DESK3_QUERY = `(min-width: ${DESK3_MIN}px)`;

// read is the layout now. A page with no matchMedia is the phone.
function read(): Layout {
  if (typeof window.matchMedia !== "function") return "phone";
  if (window.matchMedia(DESK3_QUERY).matches) return layoutOf(DESK3_MIN);
  if (window.matchMedia(DESK2_QUERY).matches) return layoutOf(DESK2_MIN);
  return layoutOf(0);
}

// subscribe calls onChange when either breakpoint is crossed. A media query list that cannot
// listen is skipped: its value is read once.
function subscribe(onChange: () => void): () => void {
  if (typeof window.matchMedia !== "function") return () => {};
  const lists = [window.matchMedia(DESK2_QUERY), window.matchMedia(DESK3_QUERY)];
  for (const list of lists) {
    if (typeof list.addEventListener === "function") list.addEventListener("change", onChange);
  }
  return () => {
    for (const list of lists) {
      if (typeof list.removeEventListener === "function") {
        list.removeEventListener("change", onChange);
      }
    }
  };
}

// useLayout is the window's layout, re-read whenever it crosses 1024px or 1280px.
export function useLayout(): Layout {
  return useSyncExternalStore(subscribe, read, () => "phone");
}
```

Create `internal/web/ui/src/test-layout.ts`:

```ts
// Test-only: the window width the layout tests drive. happy-dom's own window is 1024px wide, which
// useLayout reads as `desk2`, so test-setup.ts sets 390 (the phone) before every test; a test
// that wants a desktop frame calls setViewportWidth itself.
import { act } from "@testing-library/react";

type Listener = (event: Event) => void;

interface Watch {
  query: string;
  listener: Listener;
}

let width = 390;
const watches: Watch[] = [];
let installed = false;

// matchesQuery answers a `(min-width: <n>px)` query from the current width; any other query
// (display-mode, prefers-reduced-motion) is false, as a headless browser answers it.
function matchesQuery(query: string): boolean {
  const min = query.trim().match(/^\(min-width:\s*(\d+)px\)$/);
  return min !== null && width >= Number(min[1]);
}

function fakeMatchMedia(query: string): MediaQueryList {
  const list = {
    media: query,
    get matches(): boolean {
      return matchesQuery(query);
    },
    onchange: null,
    addEventListener(type: string, listener: Listener): void {
      if (type === "change") watches.push({ query, listener });
    },
    removeEventListener(type: string, listener: Listener): void {
      if (type !== "change") return;
      const at = watches.findIndex((w) => w.query === query && w.listener === listener);
      if (at >= 0) watches.splice(at, 1);
    },
    addListener(): void {},
    removeListener(): void {},
    dispatchEvent(): boolean {
      return false;
    },
  };
  return list as unknown as MediaQueryList;
}

// setViewportWidth makes the window `next` pixels wide for matchMedia and useLayout, and tells
// every change listener inside act, so a test needs no act of its own around it.
export function setViewportWidth(next: number): void {
  if (!installed) {
    installed = true;
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: fakeMatchMedia,
    });
  }
  width = next;
  act(() => {
    for (const watch of [...watches]) watch.listener(new Event("change"));
  });
}

// viewportWatchers is how many change listeners are registered now.
export function viewportWatchers(): number {
  return watches.length;
}
```

Replace `internal/web/ui/src/test-setup.ts`, whose text is today:

```ts
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
});
```

with:

```ts
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";

import { setViewportWidth } from "./test-layout";

// Every test starts on the phone layout: happy-dom's 1024px window would read `desk2`.
beforeEach(() => {
  setViewportWidth(390);
});

afterEach(() => {
  cleanup();
});
```

Then format what was written: `pnpm -C internal/web/ui exec prettier --write src/app/viewport.ts src/app/viewport.test.ts src/test-layout.ts src/test-setup.ts`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/app/viewport.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui run test`
Expected: the viewport file passes, typecheck and lint print no errors, and the whole Vitest suite still passes with the phone viewport set before each test.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/app/viewport.ts internal/web/ui/src/app/viewport.test.ts internal/web/ui/src/test-layout.ts
git commit -m "feat(web): useLayout reads the window's breakpoints; tests run on the phone unless they say otherwise" -- internal/web/ui/src/app/viewport.ts internal/web/ui/src/app/viewport.test.ts internal/web/ui/src/test-layout.ts internal/web/ui/src/test-setup.ts
```

### Task 6.2: The desktop preferences

**Files:**
- Create: `internal/web/ui/src/app/desktopState.ts`
- Create: `internal/web/ui/src/app/desktopState.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/app/desktopState.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DESKTOP_KEY, readDesktopPrefs, useDesktop } from "./desktopState";

const DEFAULT = { sidebar: true, inspector: true, slots: [] };

// fromOtherWindow plays the event a browser fires in this window when another window of the same
// origin writes DESKTOP_KEY (it never fires in the window that wrote).
function fromOtherWindow(newValue: string | null): void {
  window.dispatchEvent(new StorageEvent("storage", { key: DESKTOP_KEY, newValue }));
}

beforeEach(() => {
  window.localStorage.clear();
  // The default, through the store's own listener, so what it kept from an earlier test goes too.
  fromOtherWindow(null);
});

afterEach(() => {
  vi.restoreAllMocks();
});

function stored(): unknown {
  return JSON.parse(window.localStorage.getItem(DESKTOP_KEY) ?? "null");
}

// blockStorage makes every read and write of the storage throw, as a browser that blocks storage
// does; the afterEach puts the real methods back.
function blockStorage(): void {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("blocked");
  });
}

describe("readDesktopPrefs", () => {
  it.each<[string, string | null]>([
    ["nothing stored", null],
    ["an empty string", ""],
    ["text that is not JSON", "{not json"],
    ["JSON null", "null"],
    ["a number", "7"],
    ["a string", '"hello"'],
    ["an array", "[]"],
    ["no sidebar", '{"inspector":true,"slots":[]}'],
    ["a sidebar that is not a boolean", '{"sidebar":"yes","inspector":true,"slots":[]}'],
    ["no inspector", '{"sidebar":false,"slots":[]}'],
    ["an inspector that is not a boolean", '{"sidebar":false,"inspector":1,"slots":[]}'],
    ["no slots", '{"sidebar":false,"inspector":false}'],
    ["slots that is not a list", '{"sidebar":false,"inspector":false,"slots":{"tab":"info"}}'],
    ["a slot that is a number", '{"sidebar":false,"inspector":false,"slots":[3]}'],
    ["a slot that is null", '{"sidebar":false,"inspector":false,"slots":[null]}'],
    ["a slot with no tab", '{"sidebar":false,"inspector":false,"slots":[{}]}'],
    ["a slot whose tab is not a string", '{"sidebar":false,"inspector":false,"slots":[{"tab":3}]}'],
  ])("%s reads as the default", (_name, raw) => {
    expect(readDesktopPrefs(raw)).toEqual(DEFAULT);
  });

  it("reads a stored shape back as it is", () => {
    expect(
      readDesktopPrefs(
        '{"sidebar":false,"inspector":true,"slots":[{"tab":"changes"},{"tab":"info"}]}',
      ),
    ).toEqual({
      sidebar: false,
      inspector: true,
      slots: [{ tab: "changes" }, { tab: "info" }],
    });
    expect(readDesktopPrefs('{"sidebar":true,"inspector":false,"slots":[]}')).toEqual({
      sidebar: true,
      inspector: false,
      slots: [],
    });
  });

  it("keeps a tab name no panel has: only the shape is read here, and the inspector skips it", () => {
    expect(
      readDesktopPrefs('{"sidebar":true,"inspector":true,"slots":[{"tab":"nothing"}]}'),
    ).toEqual({
      sidebar: true,
      inspector: true,
      slots: [{ tab: "nothing" }],
    });
  });

  it("hands out a default of its own each time", () => {
    const first = readDesktopPrefs(null);
    first.slots.push({ tab: "info" });
    expect(readDesktopPrefs(null).slots).toEqual([]);
  });
});

describe("useDesktop", () => {
  it("sets the sidebar and the inspector, and writes the whole shape back each time", () => {
    useDesktop.getState().setSidebar(false);
    expect(useDesktop.getState().sidebar).toBe(false);
    expect(stored()).toEqual({ sidebar: false, inspector: true, slots: [] });

    useDesktop.getState().setInspector(false);
    expect(useDesktop.getState().inspector).toBe(false);
    expect(stored()).toEqual({ sidebar: false, inspector: false, slots: [] });
  });

  it("setTab adds the first slot when there is none", () => {
    useDesktop.getState().setTab("changes");
    expect(useDesktop.getState().slots).toEqual([{ tab: "changes" }]);
    expect(stored()).toEqual({ sidebar: true, inspector: true, slots: [{ tab: "changes" }] });
  });

  it("setTab sets the first slot's tab and leaves the others", () => {
    useDesktop.setState({ slots: [{ tab: "info" }, { tab: "flow" }] });
    useDesktop.getState().setTab("changes");
    expect(useDesktop.getState().slots).toEqual([{ tab: "changes" }, { tab: "flow" }]);
    expect(stored()).toEqual({
      sidebar: true,
      inspector: true,
      slots: [{ tab: "changes" }, { tab: "flow" }],
    });
  });

  it("starts from what is stored, once, when the module loads", async () => {
    window.localStorage.setItem(
      DESKTOP_KEY,
      '{"sidebar":false,"inspector":true,"slots":[{"tab":"info"}]}',
    );
    vi.resetModules();
    const fresh = await import("./desktopState");
    expect(fresh.useDesktop.getState()).toMatchObject({
      sidebar: false,
      inspector: true,
      slots: [{ tab: "info" }],
    });
  });

  it("starts at the default from a malformed stored value, not from its good parts", async () => {
    window.localStorage.setItem(DESKTOP_KEY, '{"sidebar":false,"inspector":false,"slots":"x"}');
    vi.resetModules();
    const fresh = await import("./desktopState");
    expect(fresh.useDesktop.getState()).toMatchObject(DEFAULT);
  });

  it("starts at the default and still sets when the storage throws", async () => {
    blockStorage();
    vi.resetModules();
    const fresh = await import("./desktopState");
    expect(fresh.useDesktop.getState()).toMatchObject(DEFAULT);
    expect(() => {
      fresh.useDesktop.getState().setSidebar(false);
    }).not.toThrow();
    expect(fresh.useDesktop.getState().sidebar).toBe(false);
  });

  it("follows another window's write, and a later set writes that choice back unchanged", () => {
    fromOtherWindow('{"sidebar":false,"inspector":true,"slots":[{"tab":"info"}]}');
    expect(useDesktop.getState()).toMatchObject({
      sidebar: false,
      inspector: true,
      slots: [{ tab: "info" }],
    });

    useDesktop.getState().setInspector(false);
    expect(stored()).toEqual({ sidebar: false, inspector: false, slots: [{ tab: "info" }] });
  });

  it("reads another window's malformed write as the default, and ignores another key's", () => {
    useDesktop.setState({ sidebar: false });
    window.dispatchEvent(
      new StorageEvent("storage", { key: "loom.other", newValue: '{"sidebar":true}' }),
    );
    expect(useDesktop.getState().sidebar).toBe(false);

    fromOtherWindow('{"sidebar":false,"inspector":false,"slots":"x"}');
    expect(useDesktop.getState()).toMatchObject(DEFAULT);
  });

  it("writes back what this layout does not own: other top-level keys and a slot's other fields", async () => {
    window.localStorage.setItem(
      DESKTOP_KEY,
      JSON.stringify({
        sidebar: true,
        inspector: true,
        slots: [{ tab: "info", pinned: true }, { tab: "flow", width: 300 }],
        theme: { name: "dusk" },
      }),
    );
    vi.resetModules();
    const fresh = await import("./desktopState");

    fresh.useDesktop.getState().setSidebar(false);
    fresh.useDesktop.getState().setTab("changes");
    expect(stored()).toEqual({
      sidebar: false,
      inspector: true,
      slots: [{ tab: "changes", pinned: true }, { tab: "flow", width: 300 }],
      theme: { name: "dusk" },
    });
    expect(fresh.useDesktop.getState()).toMatchObject({
      sidebar: false,
      slots: [{ tab: "changes" }, { tab: "flow" }],
    });
  });

  it("keeps what another window's write holds beyond this layout's fields", () => {
    fromOtherWindow(
      '{"sidebar":true,"inspector":true,"slots":[{"tab":"info","pinned":true}],"theme":"dusk"}',
    );
    useDesktop.getState().setInspector(false);
    expect(stored()).toEqual({
      sidebar: true,
      inspector: false,
      slots: [{ tab: "info", pinned: true }],
      theme: "dusk",
    });
  });
});
```

Fails without: `%s reads as the default` cases: the matching check in `readDesktopPrefs` (a missing or mistyped `sidebar`, `inspector` or `slots`, or a bad slot, passes through; the cases with `"sidebar":false` also catch a parse that keeps the good parts).
Fails without: `reads a stored shape back as it is`: `readDesktopPrefs` returning the default for a valid shape, or dropping a slot.
Fails without: `keeps a tab name no panel has: only the shape is read here, and the inspector skips it`: a check of the tab against a list of panels, which this module does not hold.
Fails without: `hands out a default of its own each time`: a shared default object (the pushed slot would show in the next read).
Fails without: `sets the sidebar and the inspector, and writes the whole shape back each time`: `persist` not called by a setter, or writing only the changed field.
Fails without: `setTab adds the first slot when there is none`: `setTab` assuming a first slot (it throws or writes nothing).
Fails without: `setTab sets the first slot's tab and leaves the others`: `setTab` replacing the whole list.
Fails without: `starts from what is stored, once, when the module loads`: the store's initial state not read from `DESKTOP_KEY`.
Fails without: `starts at the default from a malformed stored value, not from its good parts`: the initial state taken from `JSON.parse` instead of `readDesktopPrefs`.
Fails without: `starts at the default and still sets when the storage throws`: the `try`/`catch` in `load` or in `write`.
Fails without: `follows another window's write, and a later set writes that choice back unchanged`: the `storage` listener removed (the store stays at the default, and the later set writes `sidebar: true` over the other window's choice).
Fails without: `reads another window's malformed write as the default, and ignores another key's`: the listener applying `JSON.parse` instead of `readDesktopPrefs`, or not testing `e.key` (the other key's event would set `sidebar` to `true`).
Fails without: `writes back what this layout does not own: other top-level keys and a slot's other fields`: `write` writing only `sidebar`, `inspector` and `slots` rebuilt as `{ tab }` (`theme`, `pinned` and `width` are lost).
Fails without: `keeps what another window's write holds beyond this layout's fields`: the listener updating the state but not what is kept beside it (`theme` and `pinned` are lost on the next set).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/desktopState.test.ts`
Expected: the file fails to load: `Failed to resolve import "./desktopState" from "src/app/desktopState.test.ts". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/app/desktopState.ts`:

```ts
// The desktop's preferences (spec §6): whether the sidebar and the inspector show, and the
// inspector's tab, kept in localStorage `loom.desktop`. `slots` is a list so a later layout can add
// panes without a new key; this layout reads and writes only its first entry.
import { create } from "zustand";

import type { PanelId } from "./panels";

export const DESKTOP_KEY = "loom.desktop";

// DesktopSlot is one pane's choice: the tab it shows.
export interface DesktopSlot {
  tab: string;
}

export interface DesktopPrefs {
  sidebar: boolean;
  inspector: boolean;
  slots: DesktopSlot[];
}

function defaults(): DesktopPrefs {
  return { sidebar: true, inspector: true, slots: [] };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// Kept is what the stored value holds that this layout does not own: the top-level keys other than
// the three it reads, and each slot object whole. A newer build's fields ride along in it and are
// written back unchanged, so a toggle here never drops them.
interface Kept {
  top: Record<string, unknown>;
  slots: Record<string, unknown>[];
}

interface Parsed {
  prefs: DesktopPrefs;
  kept: Kept;
}

function fallback(): Parsed {
  return { prefs: defaults(), kept: { top: {}, slots: [] } };
}

// parse reads the stored text: anything that is not exactly the shape (not JSON, not an object, a
// flag that is not a boolean, a slot with no string tab) is the default, so one bad part never
// keeps the good ones. A tab no panel has is kept: only the shape is read here, and the inspector
// skips a tab it does not show.
function parse(raw: string | null): Parsed {
  if (raw === null) return fallback();
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return fallback();
  }
  if (!isRecord(value)) return fallback();
  const { sidebar, inspector, slots, ...top } = value;
  if (typeof sidebar !== "boolean" || typeof inspector !== "boolean" || !Array.isArray(slots)) {
    return fallback();
  }
  const read: DesktopSlot[] = [];
  const keptSlots: Record<string, unknown>[] = [];
  for (const slot of slots as unknown[]) {
    if (!isRecord(slot) || typeof slot.tab !== "string") return fallback();
    read.push({ tab: slot.tab });
    keptSlots.push(slot);
  }
  return { prefs: { sidebar, inspector, slots: read }, kept: { top, slots: keptSlots } };
}

// readDesktopPrefs is parse's preferences alone.
export function readDesktopPrefs(raw: string | null): DesktopPrefs {
  return parse(raw).prefs;
}

function load(): Parsed {
  try {
    return parse(window.localStorage.getItem(DESKTOP_KEY));
  } catch {
    return fallback();
  }
}

function write(prefs: DesktopPrefs, kept: Kept): void {
  try {
    const slots = prefs.slots.map((slot, i) => ({ ...kept.slots[i], ...slot }));
    window.localStorage.setItem(DESKTOP_KEY, JSON.stringify({ ...kept.top, ...prefs, slots }));
  } catch {
    // Full or blocked storage: the choice holds for this page only.
  }
}

export interface DesktopState {
  sidebar: boolean;
  inspector: boolean;
  slots: DesktopSlot[];
  setSidebar: (shown: boolean) => void;
  setInspector: (shown: boolean) => void;
  // setTab sets the first slot's tab, adding the slot when there is none.
  setTab: (tab: PanelId) => void;
}

const initial = load();
let kept = initial.kept;

// useDesktop is read from DESKTOP_KEY when the module loads, and written back whole on each set
// (what the layout does not own, kept beside the state, goes back unchanged). Another window's
// write to the key replaces both, so a set here never undoes that window's choice.
export const useDesktop = create<DesktopState>()((set, get) => {
  const persist = (): void => {
    const { sidebar, inspector, slots } = get();
    write({ sidebar, inspector, slots }, kept);
  };
  return {
    ...initial.prefs,

    setSidebar(shown) {
      set({ sidebar: shown });
      persist();
    },

    setInspector(shown) {
      set({ inspector: shown });
      persist();
    },

    setTab(tab) {
      set({ slots: [{ tab }, ...get().slots.slice(1)] });
      persist();
    },
  };
});

// The browser fires `storage` here when another window of the origin writes the key.
window.addEventListener("storage", (event) => {
  if (event.key !== DESKTOP_KEY) return;
  const next = parse(event.newValue);
  kept = next.kept;
  useDesktop.setState(next.prefs);
});
```

Then format: `pnpm -C internal/web/ui exec prettier --write src/app/desktopState.ts src/app/desktopState.test.ts`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/app/desktopState.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
Expected: the file passes; typecheck and lint print no errors.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/app/desktopState.ts internal/web/ui/src/app/desktopState.test.ts
git commit -m "feat(web): the desktop's preferences in loom.desktop, malformed values read as the default" -- internal/web/ui/src/app/desktopState.ts internal/web/ui/src/app/desktopState.test.ts
```

### Task 6.3: The head rows shared, and the desktop's middle pane

**Files:**
- Modify: `internal/web/ui/src/app/shell.tsx`
- Create: `internal/web/ui/src/desktop/DesktopShell.tsx`
- Test: `internal/web/ui/src/app/shell.test.tsx`
- Create: `internal/web/ui/src/desktop/DesktopShell.test.tsx`

`DesktopShell` uses `elementPane` and `ScrollPaneProvider`, and its test `useScrollPane` and `windowPane`, from `app/scroll.tsx` (Story 4), and the head rows below.

- [ ] **Step 1: Write the failing tests**

In `internal/web/ui/src/app/shell.test.tsx`, make these edits.

Replace the import lines:

```tsx
import { definePanel, type PanelProps } from "./panels";
import { router, useRoute } from "./routes";
import { AppShell, ContextRow, PanelHost, SessionTabs, StatusRow } from "./shell";
```

with:

```tsx
import { definePanel, type PanelId, type PanelProps } from "./panels";
import { routeOf, router, useRoute } from "./routes";
import {
  AppShell,
  ContextRow,
  PanelHead,
  PanelHost,
  resetKey,
  SessionTabs,
  StatusRow,
  TABBED,
  tabOf,
} from "./shell";
```

and append at the end of the file:

```tsx
// claudeSession is the fixture's first session as a session numbered 7.
function claudeSession(claude = true): Session {
  const base = allSessions(fixtureSnapshot())[0];
  if (base === undefined) throw new Error("the fixture holds no session");
  return { ...base, id: 7, claude };
}

describe("PanelHead", () => {
  function tabbed(): void {
    definePanel({
      id: "turns",
      title: () => "Conversation",
      tab: { order: 1, label: "conversation" },
      Head: () => <p>turns head</p>,
      Strip: () => <p>the strip</p>,
      Body: () => null,
    });
    definePanel({
      id: "info",
      title: () => "Info",
      tab: { order: 3, label: "info" },
      Body: () => null,
    });
  }

  it("draws the panel's head, then the session's tabs, then its strip", () => {
    tabbed();
    window.history.replaceState(null, "", "#/s/7");
    render(<PanelHead route={routeOf("#/s/7")} session={claudeSession()} />);

    const head = screen.getByText("turns head");
    const nav = screen.getByRole("navigation", { name: "Session views" });
    const strip = screen.getByText("the strip");
    expect(head.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(nav.compareDocumentPosition(strip) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("keeps the tabs and the strip when the head throws", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      tabbed();
      definePanel({
        id: "turns",
        title: () => "Conversation",
        tab: { order: 1, label: "conversation" },
        Head: () => {
          throw new Error("head boom");
        },
        Strip: () => <p>the strip</p>,
        Body: () => null,
      });
      window.history.replaceState(null, "", "#/s/7");
      render(<PanelHead route={routeOf("#/s/7")} session={claudeSession()} />);

      expect(screen.getByRole("navigation", { name: "Session views" })).toBeInTheDocument();
      expect(screen.getByText("the strip")).toBeInTheDocument();
      expect(screen.queryByText(/head boom/)).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  it("draws no tabs and no strip for a route that is not tabbed, or for no session", () => {
    tabbed();
    window.history.replaceState(null, "", "#/");
    const { unmount } = render(<PanelHead route={routeOf("#/")} session={claudeSession()} />);
    expect(screen.queryByRole("navigation", { name: "Session views" })).toBeNull();
    expect(screen.queryByText("the strip")).toBeNull();
    unmount();

    window.history.replaceState(null, "", "#/s/7");
    render(<PanelHead route={routeOf("#/s/7")} session={undefined} />);
    expect(screen.getByText("turns head")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Session views" })).toBeNull();
    expect(screen.queryByText("the strip")).toBeNull();
  });
});

describe("tabOf, TABBED and resetKey", () => {
  it.each<[PanelId, PanelId]>([
    ["turns", "turns"],
    ["changes", "changes"],
    ["agent", "turns"],
    ["job", "info"],
    ["flowStory", "flow"],
    ["flowDoc", "flow"],
    ["mockupFull", "mockup"],
  ])("tabOf lights, for the route %s, the tab %s", (panel, lit) => {
    expect(tabOf(panel, claudeSession())).toBe(lit);
  });

  it("tabOf lights info for the conversation of a session that is not Claude's", () => {
    expect(tabOf("turns", claudeSession(false))).toBe("info");
  });

  it("TABBED holds the session's routes and not the fleet's", () => {
    expect(TABBED.has("turns")).toBe(true);
    expect(TABBED.has("job")).toBe(true);
    expect(TABBED.has("fleet")).toBe(false);
    expect(TABBED.has("settings")).toBe(false);
  });

  it("resetKey changes with the panel, the route's params and its search", () => {
    const base = resetKey("turns", { sessionId: 5, params: { id: "5" }, search: {} });
    expect(resetKey("turns", { sessionId: 5, params: { id: "5" }, search: {} })).toBe(base);
    expect(resetKey("info", { sessionId: 5, params: { id: "5" }, search: {} })).not.toBe(base);
    expect(resetKey("turns", { sessionId: 6, params: { id: "6" }, search: {} })).not.toBe(base);
    expect(resetKey("turns", { sessionId: 5, params: { id: "5" }, search: { at: "2" } })).not.toBe(
      base,
    );
  });
});
```

Create `internal/web/ui/src/desktop/DesktopShell.test.tsx`:

```tsx
import { fireEvent, render, screen, within } from "@testing-library/react";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { useEffect, type ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Snapshot } from "@/api/gen";
import { definePanel } from "@/app/panels";
import { useScrollPane, windowPane } from "@/app/scroll";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions, shownWorkspaces } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import DesktopShell from "./DesktopShell";

afterEach(() => {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

// desktopAt draws DesktopShell at the hash over a one-route router of its own (memory history),
// whose only screen is `outlet`.
function desktopAt(hash: string, outlet: ReactElement = <p>outlet</p>) {
  window.history.replaceState(null, "", hash);
  const root = createRootRoute({ component: DesktopShell });
  const index = createRoute({ getParentRoute: () => root, path: "/", component: () => outlet });
  const testRouter = createRouter({
    routeTree: root.addChildren([index]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return render(<RouterProvider router={testRouter} />);
}

// aSnapshot is the fixture with one Claude session, numbered 100, in its first shown workspace.
function aSnapshot(): Snapshot {
  const snapshot = fixtureSnapshot();
  const workspace = shownWorkspaces(snapshot)[0];
  const base = allSessions(snapshot)[0];
  if (workspace === undefined || base === undefined) throw new Error("the fixture holds none");
  return {
    ...snapshot,
    sessions: {
      [String(workspace.id)]: [{ ...base, id: 100, workspace: workspace.id, claude: true }],
    },
  };
}

function live(): void {
  useFleet.setState({ snapshot: aSnapshot(), stale: false, link: "live", updated: Date.now() });
}

function tabbed(): void {
  definePanel({
    id: "turns",
    title: () => "Conversation",
    tab: { order: 1, label: "conversation" },
    Head: () => <p>turns head</p>,
    Strip: () => <p>the strip</p>,
    Body: () => null,
  });
  definePanel({
    id: "info",
    title: () => "Info",
    tab: { order: 3, label: "info" },
    Head: () => <p>info head</p>,
    Body: () => null,
  });
}

const slot = (name: string): HTMLElement | null =>
  document.querySelector<HTMLElement>(`[data-slot="${name}"]`);

describe("DesktopShell's middle", () => {
  it("draws the head, the session's tabs and the strip above the scroller, and the screen inside it", async () => {
    tabbed();
    live();
    desktopAt("#/s/100");

    const outlet = await screen.findByText("outlet");
    const head = slot("middle-head");
    const scroller = slot("middle-scroller");
    if (head === null || scroller === null) throw new Error("the middle drew no head or scroller");
    expect(head).toHaveTextContent("turns head");
    const nav = within(head).getByRole("navigation", { name: "Session views" });
    const strip = within(head).getByText("the strip");
    expect(nav.compareDocumentPosition(strip) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(scroller).toContainElement(outlet);
    expect(scroller).not.toContainElement(head);
    expect(head.compareDocumentPosition(scroller) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("draws neither the phone's header nor its footer", async () => {
    tabbed();
    live();
    desktopAt("#/s/100");

    await screen.findByText("outlet");
    expect(slot("header")).toBeNull();
    expect(slot("footer")).toBeNull();
  });

  it("keeps the screen and the tabs when the head throws", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      tabbed();
      definePanel({
        id: "turns",
        title: () => "Conversation",
        tab: { order: 1, label: "conversation" },
        Head: () => {
          throw new Error("head boom");
        },
        Body: () => null,
      });
      live();
      desktopAt("#/s/100");

      expect(await screen.findByText("outlet")).toBeInTheDocument();
      expect(screen.getByRole("navigation", { name: "Session views" })).toBeInTheDocument();
      expect(screen.queryByText(/head boom/)).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  it("draws no tabs on a route with no session, and no head row at all when the panel has no head", async () => {
    tabbed();
    definePanel({ id: "settings", title: () => "Settings", Body: () => null });
    live();
    desktopAt("#/settings");

    await screen.findByText("outlet");
    expect(screen.queryByRole("navigation", { name: "Session views" })).toBeNull();
    expect(slot("middle-head")).toHaveClass("empty:hidden");
    expect(slot("middle-head")).toBeEmptyDOMElement();
  });

  it.each([
    ["#/s/100", true],
    ["#/s/100/info", false],
  ])("the screen at %s has the readable measure: %s", async (hash, readable) => {
    tabbed();
    live();
    desktopAt(hash);

    await screen.findByText("outlet");
    const main = document.querySelector('[data-slot="middle"] main');
    if (main === null) throw new Error("the middle drew no main");
    if (readable) expect(main).toHaveClass("max-w-[48rem]");
    else expect(main).not.toHaveClass("max-w-[48rem]");
  });

  it("draws the full mockup alone, with no chrome", async () => {
    tabbed();
    live();
    desktopAt("#/s/100/mockup/full");

    expect(await screen.findByText("outlet")).toBeInTheDocument();
    expect(slot("desktop")).toBeNull();
    expect(slot("middle")).toBeNull();
    expect(slot("middle-head")).toBeNull();
  });
});

// PaneProbe draws what the pane it is given says, and tells onScroll when that pane scrolls.
function PaneProbe({ onScroll }: { onScroll: () => void }): ReactElement {
  const pane = useScrollPane();
  const top = pane.top();
  useEffect(() => pane.subscribe(onScroll), [pane, onScroll]);
  return (
    <p>
      probe {pane === windowPane ? "window" : "pane"} {top}
    </p>
  );
}

describe("DesktopShell's scroll pane", () => {
  it("gives the screen the scroller's pane and never reads the window's scroll", async () => {
    const before = Object.getOwnPropertyDescriptor(window, "scrollY");
    const reads = vi.fn(() => 0);
    Object.defineProperty(window, "scrollY", { configurable: true, get: reads });
    try {
      tabbed();
      live();
      const onScroll = vi.fn();
      desktopAt("#/s/100", <PaneProbe onScroll={onScroll} />);

      expect(await screen.findByText("probe pane 0")).toBeInTheDocument();
      expect(reads).not.toHaveBeenCalled();

      fireEvent.scroll(window);
      expect(onScroll).not.toHaveBeenCalled();
      const scroller = slot("middle-scroller");
      if (scroller === null) throw new Error("the middle drew no scroller");
      fireEvent.scroll(scroller);
      expect(onScroll).toHaveBeenCalledOnce();
    } finally {
      if (before !== undefined) Object.defineProperty(window, "scrollY", before);
      else Reflect.deleteProperty(window, "scrollY");
    }
  });
});
```

Fails without: `PanelHead: draws the panel's head, then the session's tabs, then its strip`: `PanelHead` not exported from `app/shell.tsx` (`PanelHead is not a function`), or its rows in another order.
Fails without: `PanelHead: keeps the tabs and the strip when the head throws`: the `Boundary` around `Head` in `PanelHead`.
Fails without: `PanelHead: draws no tabs and no strip for a route that is not tabbed, or for no session`: `TABBED.has(route.panel)` or `session !== undefined` in `PanelHead`.
Fails without: `tabOf lights, for the route %s, the tab %s`, `tabOf lights info for the conversation of a session that is not Claude's`, `TABBED holds the session's routes and not the fleet's` and `resetKey changes with the panel, the route's params and its search`: `tabOf`, `TABBED` or `resetKey` not exported.
Fails without: `DesktopShell's middle: draws the head, the session's tabs and the strip above the scroller, and the screen inside it`: the head rows drawn inside the scroller (`not.toContainElement(head)`), or the screen drawn outside it.
Fails without: `draws neither the phone's header nor its footer`: `DesktopShell` reusing the phone's `<header data-slot="header">`.
Fails without: `keeps the screen and the tabs when the head throws`: the head's `Boundary` in `PanelHead`.
Fails without: `draws no tabs on a route with no session, and no head row at all when the panel has no head`: `empty:hidden` on the head element (an empty bordered strip stays drawn), or tabs drawn with no session.
Fails without: `the screen at %s has the readable measure: %s`: `READABLE` leaving `turns` out (the first case), or holding `info` (the second).
Fails without: `draws the full mockup alone, with no chrome`: the early `return <Outlet />` for `mockupFull` in `DesktopShell`.
Fails without: `gives the screen the scroller's pane and never reads the window's scroll`: `ScrollPaneProvider` missing (the probe reads `window`), the `pane === null ? null` guard (the probe's first render gets `windowPane` and reads `window.scrollY`), or `elementPane` over the window instead of the scroller (`onScroll` fires on the window's scroll).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx src/desktop/DesktopShell.test.tsx`
Expected: `src/desktop/DesktopShell.test.tsx` fails to load (`Failed to resolve import "./DesktopShell"`); in `shell.test.tsx` the new tests fail with `PanelHead is not a function` (and `tabOf is not a function`), the existing ones pass.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/app/shell.tsx`, export what `DesktopShell` and the inspector use. These four lines each gain `export `:

```tsx
function resetKey(panel: PanelId, props: PanelProps): string {
```

```tsx
class Boundary extends Component<BoundaryProps, BoundaryState> {
```

```tsx
const TABBED: ReadonlySet<PanelId> = new Set<PanelId>([
```

```tsx
function tabOf(panel: PanelId, session: Session): PanelId {
```

and read `export function resetKey(panel: PanelId, props: PanelProps): string {`, `export class Boundary extends Component<BoundaryProps, BoundaryState> {`, `export const TABBED: ReadonlySet<PanelId> = new Set<PanelId>([` and `export function tabOf(panel: PanelId, session: Session): PanelId {`.

Insert `PanelHead` above AppShell's comment, that is between the end of `SPLASH` (`};`) and the line `// AppShell is the app's frame: the pinned header (the panel's Head), main`:

```tsx
// PanelHead is a route's head rows: the panel's Head, then, for a session on a tabbed route, the
// session's tabs and the panel's Strip, each in a boundary of its own so a head that throws
// leaves the tabs, the strip and the screen. The phone pins it in its header and the desktop's
// middle pins it above its scroller.
export function PanelHead({
  route,
  session,
}: {
  route: RouteState;
  session: Session | undefined;
}): ReactElement {
  const props = panelProps(route);
  const entry = panelFor(route.panel);
  const title = entry === undefined ? route.panel : entry.title(props);
  const Head = entry?.Head;
  const Strip = entry?.Strip;
  return (
    <>
      {Head === undefined ? null : (
        <Boundary key={resetKey(route.panel, props)} what={`The ${title} header`} fallback={null}>
          <Head {...props} />
        </Boundary>
      )}
      {session !== undefined && TABBED.has(route.panel) ? (
        <>
          <SessionTabs session={session} />
          {Strip === undefined ? null : (
            <Boundary
              key={resetKey(route.panel, props)}
              what={`The ${title} strip`}
              fallback={null}
            >
              <Strip {...props} />
            </Boundary>
          )}
        </>
      ) : null}
    </>
  );
}

```

In `AppShell`, delete these four lines (`PanelHead` computes them now):

```tsx
  const entry = panelFor(route.panel);
  const title = entry === undefined ? route.panel : entry.title(props);
  const Head = entry?.Head;
  const Strip = entry?.Strip;
```

and, inside its `<header data-slot="header" …>`, replace this span:

```tsx
          {Head === undefined ? null : (
            <Boundary
              key={resetKey(route.panel, props)}
              what={`The ${title} header`}
              fallback={null}
            >
              <Head {...props} />
            </Boundary>
          )}
          {session !== undefined && TABBED.has(route.panel) ? (
            <>
              <SessionTabs session={session} />
              {Strip === undefined ? null : (
                <Boundary
                  key={resetKey(route.panel, props)}
                  what={`The ${title} strip`}
                  fallback={null}
                >
                  <Strip {...props} />
                </Boundary>
              )}
            </>
          ) : null}
```

with:

```tsx
          <PanelHead route={route} session={session} />
```

Create `internal/web/ui/src/desktop/DesktopShell.tsx`:

```tsx
// The desktop's frame (spec §4, §5, §9). It is a module of its own, loaded only at 1024px and
// wider, so the phone's first load does not grow. The row below holds the middle, the route's
// pane: the panel's head rows pinned above a scroller that scrolls on its own.
import { Outlet } from "@tanstack/react-router";
import { useMemo, useState, type ReactElement } from "react";

import type { PanelId } from "@/app/panels";
import { panelProps, useRoute } from "@/app/routes";
import { elementPane, ScrollPaneProvider } from "@/app/scroll";
import { PanelHead } from "@/app/shell";
import { cn } from "@/lib/utils";
import { useSession } from "@/store/fleet";

// READABLE are the panels whose text keeps a readable measure in a wide pane: a conversation's
// turns and its reply box are at most 48rem wide, centred.
const READABLE: ReadonlySet<PanelId> = new Set<PanelId>(["turns", "agent"]);

// Middle is the route's pane: exactly the phone's frame for the route, with its head rows pinned
// above the scroller (outside it, so the pane's own head is `null`) and the panel's screen inside
// the scroller under an elementPane. The screen is drawn only once the scroller has mounted, so no
// panel of the middle ever gets the window's pane and reads the window's scroll.
function Middle(): ReactElement {
  const route = useRoute();
  const session = useSession(panelProps(route).sessionId);
  const [scroller, setScroller] = useState<HTMLElement | null>(null);
  const pane = useMemo(() => (scroller === null ? null : elementPane(scroller, null)), [scroller]);

  return (
    <section data-slot="middle" className="flex min-w-0 flex-1 flex-col">
      <header
        data-slot="middle-head"
        className="flex-none border-b border-border bg-background pt-2 empty:hidden"
      >
        <PanelHead route={route} session={session} />
      </header>
      <div
        ref={setScroller}
        data-slot="middle-scroller"
        className="min-h-0 flex-1 overflow-y-auto"
      >
        {pane === null ? null : (
          <ScrollPaneProvider pane={pane}>
            <main
              className={cn(
                "mx-auto flex min-h-full w-full flex-col",
                READABLE.has(route.panel) && "max-w-[48rem]",
              )}
            >
              <Outlet />
            </main>
          </ScrollPaneProvider>
        )}
      </div>
    </section>
  );
}

// DesktopShell is the frame at `desk2` and `desk3`: the whole window, one row holding the middle.
// The full mockup is the one screen that draws the whole window itself, with no chrome.
export default function DesktopShell(): ReactElement {
  const route = useRoute();
  if (route.panel === "mockupFull") return <Outlet />;
  return (
    <div data-slot="desktop" className="flex h-dvh min-h-0 flex-col bg-background text-foreground">
      <div className="flex min-h-0 flex-1">
        <Middle />
      </div>
    </div>
  );
}
```

Then format: `pnpm -C internal/web/ui exec prettier --write src/app/shell.tsx src/app/shell.test.tsx src/desktop/DesktopShell.tsx src/desktop/DesktopShell.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx src/desktop/DesktopShell.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
Expected: both files pass, `shell.test.tsx`'s existing `AppShell` and `AppShell's chrome` tests unchanged (the phone's header now draws `PanelHead`); typecheck and lint print no errors.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/desktop/DesktopShell.tsx internal/web/ui/src/desktop/DesktopShell.test.tsx
git commit -m "feat(web): the desktop's middle pane, and PanelHead shared with the phone's header" -- internal/web/ui/src/app/shell.tsx internal/web/ui/src/app/shell.test.tsx internal/web/ui/src/desktop/DesktopShell.tsx internal/web/ui/src/desktop/DesktopShell.test.tsx
```

### Task 6.4: The shell switch, with the sheet and the toaster above both frames

**Files:**
- Modify: `internal/web/ui/src/app/shell.tsx`
- Test: `internal/web/ui/src/app/shell.test.tsx`

`AppShell` uses `ConfirmSheet`'s `variant` and `Toaster`'s `placement` (Story 5), whose call in `shell.tsx` today reads `<ConfirmSheet variant="drawer" />` and `<Toaster placement="top" />`, and the lazy `DesktopShell` of Task 6.3.

- [ ] **Step 1: Write the failing tests**

In `internal/web/ui/src/app/shell.test.tsx`, replace the first line:

```tsx
import { act, fireEvent, render, screen, within } from "@testing-library/react";
```

with:

```tsx
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
```

and, after the line `import { useFleet } from "@/store/fleet";`, add:

```tsx
import { setViewportWidth } from "@/test-layout";
```

Append at the end of the file:

```tsx
describe("AppShell's layouts", () => {
  const desktopFrame = (): Element | null => document.querySelector('[data-slot="desktop"]');

  it.each([
    [390, false],
    [1023, false],
    [1024, true],
    [1279, true],
    [1280, true],
  ])("at %ipx the desktop frame is drawn: %s", async (width, desktop) => {
    live(withGroups(["needs_you"]));
    setViewportWidth(width);
    shellAt("#/");

    expect(await screen.findByText("outlet")).toBeInTheDocument();
    if (desktop) {
      await waitFor(() => {
        expect(desktopFrame()).not.toBeNull();
      });
      expect(header()).toBeNull();
      expect(footer()).toBeNull();
    } else {
      expect(desktopFrame()).toBeNull();
      expect(header()).not.toBeNull();
      expect(footer()).not.toBeNull();
    }
  });

  it("a resize swaps the frame and keeps the route and the screen", async () => {
    definePanel({
      id: "info",
      title: () => "Info",
      Head: () => <p>info head</p>,
      Body: () => null,
    });
    live(withGroups(["needs_you"]));
    shellAt("#/s/100/info");
    await screen.findByText("outlet");
    expect(header()).toHaveTextContent("info head");

    setViewportWidth(1280);
    await waitFor(() => {
      expect(desktopFrame()).not.toBeNull();
    });
    expect(document.querySelector('[data-slot="middle-head"]')).toHaveTextContent("info head");
    expect(screen.getByText("outlet")).toBeInTheDocument();
    expect(window.location.hash).toBe("#/s/100/info");

    setViewportWidth(500);
    await waitFor(() => {
      expect(desktopFrame()).toBeNull();
    });
    expect(header()).toHaveTextContent("info head");
    expect(screen.getByText("outlet")).toBeInTheDocument();
    expect(window.location.hash).toBe("#/s/100/info");
  });

  it("draws pairing in the phone's column at a desktop width, even with a snapshot", async () => {
    live(withGroups(["needs_you"]));
    setViewportWidth(1400);
    shellAt("#/pair");

    expect(await screen.findByText("outlet")).toBeInTheDocument();
    expect(desktopFrame()).toBeNull();
  });

  it("draws the splash in the phone's column at a desktop width while there is no snapshot", async () => {
    useFleet.setState({ link: "offline" });
    setViewportWidth(1400);
    shellAt("#/");

    expect(await screen.findByText("Can't reach loom. Retrying…")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Back to classic" })).toBeInTheDocument();
    expect(desktopFrame()).toBeNull();
  });

  it("keeps an open confirmation across a swap, redrawn as a dialog, and it still answers", async () => {
    live(withGroups(["needs_you"]));
    shellAt("#/");
    await screen.findByText("outlet");

    let answer = Promise.resolve<string | null>(null);
    act(() => {
      answer = askConfirm({
        title: "Kill session",
        body: "Ends it.",
        action: "Kill",
        destructive: true,
      });
    });
    expect(await screen.findByText("Kill session")).toBeInTheDocument();
    expect(document.querySelector('[data-slot="drawer-content"]')).not.toBeNull();
    expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull();

    setViewportWidth(1100);
    await waitFor(() => {
      expect(document.querySelector('[data-slot="dialog-content"]')).not.toBeNull();
    });
    expect(document.querySelector('[data-slot="drawer-content"]')).toBeNull();
    expect(screen.getByText("Kill session")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Kill" }));
    expect(await answer).toBe("");
  });

  it("keeps a toast showing across a swap, at the top on the phone and the bottom right on desktop", async () => {
    live(withGroups(["needs_you"]));
    shellAt("#/");
    await screen.findByText("outlet");

    act(() => {
      showMessage("Could not push", true);
    });
    expect(await screen.findByText("Could not push")).toBeInTheDocument();
    const viewport = (): Element | null => document.querySelector('[data-slot="toast-viewport"]');
    expect(viewport()).toHaveAttribute("data-placement", "top");

    setViewportWidth(1100);
    await waitFor(() => {
      expect(desktopFrame()).not.toBeNull();
    });
    expect(screen.getByText("Could not push")).toBeInTheDocument();
    expect(viewport()).toHaveAttribute("data-placement", "bottom-right");
  });
});
```

Fails without: `at %ipx the desktop frame is drawn: %s`: `layout !== "phone"` in `AppShell` (the frame never switches), or the breakpoints in `useLayout`.
Fails without: `a resize swaps the frame and keeps the route and the screen`: `AppShell` reading the layout once (no `useLayout`), or the frame resetting the route.
Fails without: `draws pairing in the phone's column at a desktop width, even with a snapshot`: `route.panel !== "pair"` in `desktop`.
Fails without: `draws the splash in the phone's column at a desktop width while there is no snapshot`: `hasSnapshot` in `desktop` (the desktop frame would be drawn over no fleet).
Fails without: `keeps an open confirmation across a swap, redrawn as a dialog, and it still answers`: the sheet mounted inside `PhoneShell` (the swap unmounts it and the ask hangs), or `variant` fixed at `"drawer"`.
Fails without: `keeps a toast showing across a swap, at the top on the phone and the bottom right on desktop`: the toaster mounted inside `PhoneShell` (the toast is lost at the swap), or `placement` fixed at `"top"`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx`
Expected: the new tests fail, for example `at 1024px the desktop frame is drawn: true` with `expected null not to be null` (`AppShell` draws only the phone's frame); the existing tests pass.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/app/shell.tsx`:

Replace the first import lines:

```tsx
import {
  Component,
  Suspense,
```

with:

```tsx
import {
  Component,
  lazy,
  Suspense,
```

and after the line `import { hrefFor, panelProps, routeOf, useRoute, type RouteState } from "./routes";` add:

```tsx
import { useLayout } from "./viewport";
```

Replace the comment line and the function line of today's frame:

```tsx
// AppShell is the app's frame: the pinned header (the panel's Head), main
```

with:

```tsx
// PhoneShell is the phone's frame, one centred column: the pinned header (the panel's Head), main
```

and

```tsx
export function AppShell(): ReactElement {
```

with:

```tsx
export function PhoneShell(): ReactElement {
```

Delete these two lines from the end of `PhoneShell`'s returned `<div>` (`AppShell` mounts them now):

```tsx
      <ConfirmSheet variant="drawer" />
      <Toaster placement="top" />
```

and after `PhoneShell`'s closing `}` (the line before `// NON_TEXT_INPUTS are the input types that bring up no keyboard.`) add:

```tsx
// DesktopShell is the desktop's frame, a module of its own (spec §4): the phone's first load does not
// carry it.
const DesktopShell = lazy(() => import("@/desktop/DesktopShell"));

// AppShell chooses the frame by the window's width, live as it resizes: the lazy DesktopShell at
// 1024px and wider, once there is a snapshot and off the pairing screen, else PhoneShell, which is
// also the pairing screen's and the splash's frame at every width (a centred column). The one
// confirmation sheet and the one toaster are mounted here, above both frames, told the layout, so a
// swap neither drops an open confirmation nor a toast (spec §11). A chunk of the desktop that fails
// to load reaches the boundary, which offers a reload.
export function AppShell(): ReactElement {
  const layout = useLayout();
  const route = useRoute();
  const hasSnapshot = useFleet((s) => s.snapshot !== null);
  const desktop = layout !== "phone" && hasSnapshot && route.panel !== "pair";
  return (
    <>
      {desktop ? (
        <Boundary what="The desktop layout">
          <Suspense fallback={<BodyLoading />}>
            <DesktopShell />
          </Suspense>
        </Boundary>
      ) : (
        <PhoneShell />
      )}
      <ConfirmSheet variant={layout === "phone" ? "drawer" : "dialog"} />
      <Toaster placement={layout === "phone" ? "top" : "bottom-right"} />
    </>
  );
}

```

Then format: `pnpm -C internal/web/ui exec prettier --write src/app/shell.tsx src/app/shell.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx src/desktop/DesktopShell.test.tsx src/main.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui run test`
Expected: the three files pass (`main.test.tsx` launches the app with a `matchMedia` mock that cannot listen), then the whole Vitest suite passes on the phone viewport; typecheck and lint print no errors.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): AppShell switches between the phone's frame and the lazy desktop frame, the sheet and toaster above both" -- internal/web/ui/src/app/shell.tsx internal/web/ui/src/app/shell.test.tsx
```

### Task 6.5: The lazy chunk's budget check, and the rebuilt bundle

**Files:**
- Modify: `internal/web/static_budget_test.go`
- Modify: `internal/web/dist/` (rebuilt)

- [ ] **Step 1: Write the failing test**

In `internal/web/static_budget_test.go`, replace the comment line:

```go
// document views, the host's flows, the new session screen and settings are chunks of their
```

with:

```go
// document views, the host's flows, the new session screen, settings and the desktop shell are chunks of their
```

replace the end of the `for _, name := range []string{…}` line in `checkLazyViews`:

```go
"NewSessionBody", "SettingsBody"} {
```

with:

```go
"NewSessionBody", "SettingsBody", "DesktopShell"} {
```

replace the end of the `all := []string{…}` line in `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`:

```go
"/assets/NewSessionBody-h.js", "/assets/SettingsBody-s.js"}
```

with:

```go
"/assets/NewSessionBody-h.js", "/assets/SettingsBody-s.js", "/assets/DesktopShell-i.js"}
```

and after the case `{"a preloaded settings chunk", …},` add:

```go
		{"no desktop shell chunk", build(entry, slices.DeleteFunc(slices.Clone(all), func(f string) bool { return strings.HasPrefix(f, "/assets/DesktopShell-") })...), "DesktopShell"},
		{"a preloaded desktop shell chunk", build(entry+`<link rel="modulepreload" crossorigin href="/assets/DesktopShell-i.js">`, all...), "/assets/DesktopShell-i.js"},
```

Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/no desktop shell chunk`: `"DesktopShell"` in `checkLazyViews`' list (no error is returned for a bundle without the chunk).
Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/a preloaded desktop shell chunk`: the same entry in the list, or the `strings.Contains(page, …)` check.
Fails without: `TestDist_LazyViewsAreChunksOfTheirOwn`: the bundle holding no `DesktopShell-<hash>.js` of its own, or `index.html` naming it (`AppShell` importing `DesktopShell` statically).

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestDist_LazyViewsAreChunksOfTheirOwn|TestCheckLazyViews'`
Expected: `FAIL` in `TestDist_LazyViewsAreChunksOfTheirOwn`: `the bundle has no lazy chunk for DesktopShell: its panel is imported statically, or the build renamed it`, because the committed bundle predates the lazy frame; the `TestCheckLazyViews_*` cases pass.

- [ ] **Step 3: Implement**

Rebuild the bundle, which writes `internal/web/dist/` with the `DesktopShell-<hash>.js` chunk.

Run: `pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build`
Expected: Vite lists `dist/assets/DesktopShell-<hash>.js` among the built files, and `index.html` names no `DesktopShell` file.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/`
Expected: `check` passes (typecheck, lint, format, every Vitest file, `node --check public/sw.js`); `dist:check` prints no difference; the Go package passes, `TestDist_LazyViewsAreChunksOfTheirOwn` and `TestDist_FirstLoadIsWithinBudget` among them. If the first load is over its budget, report it: raising `firstLoadBudget` is the owner's call.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "test(web): the desktop shell is a lazy chunk of its own; rebuild the bundle" -- internal/web/static_budget_test.go internal/web/dist
```

## Story 7: The status row's parts and the workspace filter become shared pieces

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/status.tsx`, `internal/web/ui/src/app/status.test.tsx`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/fleet/filter.ts`, `internal/web/ui/src/fleet/filter.test.ts`, `internal/web/ui/src/fleet/WorkspaceFilter.tsx`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/panels/fleet/fleet.test.tsx`, `internal/web/ui/src/panels/new/NewSessionBody.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `HostLabel`, `LinkPill`, `GroupCounts` (each with `dense`), `FILTER_KEY` (moved), `readFilter`, `FilterState`, `useFilter`, `activeFilter`, `WorkspaceFilter`; uses `useLayout`, `setViewportWidth` (Story 6), `nextInGroup`, `fleetGroups`, `shownWorkspaces`, `groupSessions`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** light (moves of existing parts and one small shared store; the existing footer and fleet tests pin their drawing)
- **Form:** brief (existing parts moved into shared modules, and a store over an existing localStorage key)

The footer's pill, host label and group counts move out of `StatusRow` into `app/status.tsx` as `LinkPill`, `HostLabel` and `GroupCounts`, and the fleet's workspace filter moves out of the fleet panel into `fleet/filter.ts` (the key, a store over it, `activeFilter`) and `fleet/WorkspaceFilter.tsx`, so the phone's footer and fleet, the new-session form and the desktop's top bar and sidebar (Stories 8 and 9) share one parts list and one choice. The phone draws exactly what it draws today: the existing `StatusRow`, fleet and new-session tests keep passing, and `loom.filter` keeps its classic encoding (a JSON string holding a workspace id, or `""`). The one new behaviour is that the fleet panel draws the filter only in the phone layout (`useLayout() === "phone"`, Story 6), since the desktop's top bar carries it. No code in this story holds a landmine marker; its developer runs at the default effort. Every task rebuilds `internal/web/dist/` and commits it with the source.

### Task 7.1: `LinkPill`, `HostLabel` and `GroupCounts` in `app/status.tsx`

**Files:**
- Create: `internal/web/ui/src/app/status.tsx`
- Create: `internal/web/ui/src/app/status.test.tsx`
- Modify: `internal/web/ui/src/app/shell.tsx` (`PILL`, `PILL_TONE`, `COUNTED`, `clock` and `StatusRow`'s body, the block that starts at `const PILL: Record<LinkState, string> = {` and ends with `StatusRow`'s closing brace)
- Modify: `internal/web/dist/` (rebuilt)

**Behaviour.** Move the three parts of `StatusRow` as they draw today, byte for byte in what they render, into `app/status.tsx`, together with the constants and helper only they use (`PILL`, `PILL_TONE`, `COUNTED`, `clock`).

- `LinkPill(props: { dense?: boolean })` draws the `<span aria-live="polite">` with the link state's label (`LIVE`, `CONNECTING`, `RESTARTING`, `OFFLINE`) and tone, reading `useLink()`.
- `HostLabel(props: { dense?: boolean })` draws the `min-w-0 flex-1 truncate font-mono` span: `loom` with no host, else `loom@` muted then the host, then ` · <clock(updated)>` while the link is `offline` and `updated > 0`. It reads `useLink()` and `useFleet` for `snapshot?.host` and `updated`.
- `GroupCounts(props: { dense?: boolean })` returns the three counts as a fragment of siblings (so it sits in the row's flex as today): a button per non-zero group with `aria-label` `"<n> <words>: open the next"`, a faint span for a zero, all faint (`opacity-60`) while the link is not `live`; a click opens `hrefFor("turns", { id })` of `nextInGroup(fleetGroups(snapshot), group, current)` with `current` from `panelProps(route).sessionId ?? null`, read from `useFleet.getState().snapshot` at the click. It reads `useFleetGroups()`, `useLink()` and `useRoute()`.
- `dense` is the top bar's density (spec §11): in `GroupCounts` it drops the `min-h-[44px]` from each count (without it each count keeps it, as today). `LinkPill` and `HostLabel` draw the same either way today, so they declare the prop and do not read it (name the parameter `_props` if the lint rule for unused parameters asks).
- `StatusRow` stays in `app/shell.tsx`, exported, and composes them in the same container `<div className="flex min-h-[44px] items-center gap-1 px-4 text-xs">`: `LinkPill`, `HostLabel`, `GroupCounts`, in that order, with no `dense`. Remove from `shell.tsx` the imports the move leaves unused (`LinkState`, `SessionGroup` if nothing else uses it, `fleetGroups`, `nextInGroup`, `useFleetGroups`, and any other the lint flags); `tsc` and eslint name them.

Names used: `useLink`, `useFleet`, `useFleetGroups` (`store/fleet`), `fleetGroups`, `nextInGroup` (`fleet/groups`), `hrefFor`, `panelProps`, `useRoute` (`app/routes`), `cn`. Names defined: `LinkPill`, `HostLabel`, `GroupCounts`, each with `dense`.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/app/status.test.tsx` (Vitest, Testing Library; load a snapshot with `useFleet.getState().apply("snapshot", fixtureSnapshot(now))` or `useFleet.setState`, as `shell.test.tsx`'s `StatusRow` block does, and reset the store and `window.location.hash` after each test):
  - `LinkPill says each link state`: `it.each` over `live`, `connecting`, `restarting`, `offline` with their labels; renders `<LinkPill />` and finds the label. Fails without: `LinkPill`, removed (nothing renders the label).
  - `HostLabel names the host, and the time of the last update only while offline`: renders `<HostLabel />` live and sees `devbox` and no ` · `; then offline with `updated` set and sees `devbox · <clock>` (normalise whitespace in the expected clock as `shell.test.tsx` does). Fails without: the `when` suffix in `HostLabel`, removed; or its `state === "offline"` term, inverted (the live case then shows a time).
  - `HostLabel says loom with no snapshot`: no snapshot; the text is `loom`. Fails without: the `host === ""` branch, removed.
  - `GroupCounts counts each group and draws a zero as no button`: a snapshot with two needs-you, one your-turn, no working; buttons named `2 need you: open the next` and `1 your turn: open the next`, none named `/working/`, and the working zero drawn as text `0`. Fails without: the `n === 0` branch in `GroupCounts`, removed (a button for the zero).
  - `GroupCounts opens the next session of a group after the one on screen`: two needs-you sessions (ids 100 and 101), hash `#/s/100`, click, hash `#/s/101`; hash `#/s/101`, click, hash `#/s/100` (wraps). Fails without: `current` read from the route, replaced with `null` (the second click lands on 100 from 100, and the first never advances).
  - `GroupCounts keeps its 44px minimum, and drops it when dense`: render `<GroupCounts />` and `<GroupCounts dense />` (separately); every count's class list (the button and the zero span) contains `min-h-[44px]` without `dense` and does not with it. Fails without: the `dense` branch in `GroupCounts`, removed (the dense counts keep the minimum); or inverted (the plain counts lose it).
  - `GroupCounts draws faint counts that stay buttons while the stream is down`: offline link with a cached needs-you snapshot; the button has `opacity-60` in its class and is enabled. Fails without: the `state !== "live"` term, removed.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/app/status.test.tsx
```

Expected: the suite fails to load, `Failed to resolve import "./status"`.

- [ ] **Step 3: Implement** `app/status.tsx` and rewire `StatusRow` as the behaviour above says. Then run the existing footer tests to see that nothing drawn has changed:

```bash
pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx src/app/status.test.tsx
pnpm -C internal/web/ui run build
```

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check
```

Expected: typecheck, lint, format and every Vitest suite pass (`shell.test.tsx`'s `StatusRow` and `AppShell's chrome` blocks unchanged), and `dist:check` prints no difference.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/app/status.tsx internal/web/ui/src/app/status.test.tsx internal/web/dist
git commit -m "refactor(web): the status row's pill, host label and counts are shared parts" -- internal/web/ui/src/app/status.tsx internal/web/ui/src/app/status.test.tsx internal/web/ui/src/app/shell.tsx internal/web/dist
```

### Task 7.2: `fleet/filter.ts`, the shared filter choice

**Files:**
- Create: `internal/web/ui/src/fleet/filter.ts`
- Create: `internal/web/ui/src/fleet/filter.test.ts`
- Modify: `internal/web/dist/` (rebuilt; nothing imports the module yet, so the bundle may be unchanged: run `dist:check` and commit only what it reports)

**Behaviour.** One module owns the workspace filter's key and its in-memory copy.

- `export const FILTER_KEY = "loom.filter"`, with the comment that moves from `panels/fleet/index.tsx` (the classic app keeps the choice there as a JSON string holding a workspace id or `""`, so it survives a switch between the apps). Task 7.3 deletes the old constant.
- `export function readFilter(): string`: `JSON.parse(window.localStorage.getItem(FILTER_KEY) ?? '""')` when it is a string, else `""`; anything that throws (no value is not a throw; unparseable JSON, a refused `getItem`) is `""`.
- `export interface FilterState { stored: string; pick: (id: string) => void }` and `export const useFilter = create<FilterState>()(...)` (zustand, as `store/drafts.ts` does): `stored` starts as `readFilter()` when the module loads (once; later changes come only through `pick`, or a test's `useFilter.setState({ stored: readFilter() })`), `pick(id)` sets `stored` to `id` and writes `JSON.stringify(id)` under `FILTER_KEY`, the write in a `try` so a full or refused store keeps the choice for this launch only.
- `export function activeFilter(stored: string, workspaces: Workspace[]): string`: `stored` when `workspaces.some((w) => String(w.id) === stored)`, else `""` (an unlisted stored id is read as all and left stored, as the fleet does today).

Names defined: `FILTER_KEY`, `readFilter`, `FilterState`, `useFilter`, `activeFilter`. Names used: `Workspace` (`@/api/gen`).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/fleet/filter.test.ts` (clear `localStorage` and restore spies after each test):
  - `readFilter reads the classic encoding`: `it.each` of stored raw values and expected results: absent gives `""`; `'""'` gives `""`; `'"2"'` gives `"2"`; `'7'` gives `""` (not a string); `'["2"]'` gives `""`; `'not json'` gives `""`. Fails without: the `typeof value === "string"` check in `readFilter` (the `7` and array cases return non-strings); the `catch` (`not json` throws).
  - `readFilter reads an unreadable store as all`: `vi.spyOn(Storage.prototype, "getItem")` throws; the result is `""`. Fails without: the `try`/`catch` in `readFilter`, removed.
  - `useFilter starts from the stored choice`: preset `localStorage` to `'"2"'`, `vi.resetModules()`, `await import("./filter")`; `useFilter.getState().stored` is `"2"`. Fails without: `stored`'s initial value read from `readFilter()` (it would start as `""`).
  - `pick keeps the choice and writes it as a JSON string, and "" writes all`: `useFilter.getState().pick("2")` makes `stored` `"2"` and `localStorage.getItem("loom.filter")` `'"2"'`; `pick("")` makes them `""` and `'""'`. Fails without: the write in `pick` (nothing stored), or `JSON.stringify` (stored `2`).
  - `pick keeps the choice for this launch when the store refuses`: `Storage.prototype.setItem` spied to throw; `pick("2")` does not throw and `stored` is `"2"`. Fails without: the `try`/`catch` around the write (the call throws, or `set` never runs).
  - `activeFilter names a listed workspace and reads any other as all`: workspaces with ids 1 and 2 (take the type from `Workspace`, filling only what the helper needs through a cast-free fixture such as `fixtureSnapshot`'s workspaces): `("2", ws)` gives `"2"`; `("9", ws)` gives `""`; `("", ws)` gives `""`; `("2", [])` gives `""`. Fails without: the `some` comparison in `activeFilter`, replaced with `return stored` (the `9` and empty-list cases return their input), or comparing `w.id === stored` (the `2` case gives `""`, since an id is a number).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/fleet/filter.test.ts
```

Expected: the suite fails to load, `Failed to resolve import "./filter"`.

- [ ] **Step 3: Implement** `fleet/filter.ts` as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/fleet/filter.test.ts && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check
```

Expected: `filter.test.ts` passes with its cases, `check` passes and `dist:check` prints no difference.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/fleet/filter.ts internal/web/ui/src/fleet/filter.test.ts
git commit -m "feat(web): the workspace filter's choice is one store over loom.filter" -- internal/web/ui/src/fleet/filter.ts internal/web/ui/src/fleet/filter.test.ts internal/web/dist
```

### Task 7.3: `WorkspaceFilter`, and the fleet panel and the new-session form on the shared choice

**Files:**
- Create: `internal/web/ui/src/fleet/WorkspaceFilter.tsx`
- Modify: `internal/web/ui/src/panels/fleet/index.tsx` (the `FILTER_KEY`, `ALL`, `readFilter`, `writeFilter` block after the `SessionRow` import; `Fleet`'s `stored` state, `active`, `pick` and the `ToggleGroup` block)
- Modify: `internal/web/ui/src/panels/new/NewSessionBody.tsx` (the `FILTER_KEY` import and `storedFilter`)
- Test: `internal/web/ui/src/panels/fleet/fleet.test.tsx`
- Modify: `internal/web/dist/` (rebuilt)

**Behaviour.**

- `fleet/WorkspaceFilter.tsx` exports `WorkspaceFilter(props: { dense?: boolean }): ReactElement | null`: the `ToggleGroup` the fleet draws today (`aria-label="Workspaces"`, an "All" item with value `"all"`, then one item per shown workspace with value `String(w.id)` and the text `w.label || w.name`, `value={[active === "" ? "all" : active]}`), where `workspaces` is `shownWorkspaces(snapshot)` from `useFleet`'s snapshot, `active` is `activeFilter(useFilter((s) => s.stored), workspaces)`, and `onValueChange` calls `useFilter.getState().pick(id)` with `""` for an empty value or `"all"`, else the value (so choosing the chosen item again, which Base UI reports as an empty value, picks all). It returns `null` with no snapshot or fewer than two shown workspaces. With `dense`, each item overrides the toggle item's 44px touch minimum (`after:min-h-[44px] after:min-w-[44px]` in `components/ui/toggle-group.tsx`) with `after:min-h-0 after:min-w-0` through its `className`; without it the group draws exactly as today.
- `panels/fleet/index.tsx`: delete `FILTER_KEY`, `ALL`, `readFilter`, `writeFilter` and the `useState(readFilter)` state; read `stored` from `useFilter((s) => s.stored)`; compute `active` with `activeFilter(stored, workspaces)` where the memo computes it today (`workspaces.some((w) => String(w.id) === stored) ? stored : ""`); delete `pick` and the inline `ToggleGroup` block, and draw `{layout === "phone" ? <WorkspaceFilter /> : null}` in its place with `const layout = useLayout()` (from `@/app/viewport`, Story 6; hooks stay above the early `Loading…` return). Remove the `ToggleGroup` and `ToggleGroupItem` imports (`useState` stays: the 30 s tick uses it).
- `panels/new/NewSessionBody.tsx`: delete `storedFilter` and the `import { FILTER_KEY } from "@/panels/fleet"` line, import `readFilter` from `@/fleet/filter`, and have `firstWorkspace` call `readFilter()` where it called `storedFilter()`. The form keeps reading the stored value fresh each time it mounts, so `panels/new/new.test.tsx`, which presets `loom.filter` before rendering, is unchanged and still pins the shared encoding.

Names used: `useLayout` (`app/viewport`), `setViewportWidth` (`test-layout`, test only; Story 6 makes every test run at the phone layout unless it calls it), `activeFilter`, `useFilter`, `readFilter`, `shownWorkspaces`, `groupSessions`. Names defined: `WorkspaceFilter`.

- [ ] **Step 1: Write the failing tests and fix the existing ones** in `internal/web/ui/src/panels/fleet/fleet.test.tsx`:
  - Import `useFilter` and `readFilter` from `@/fleet/filter`, `WorkspaceFilter` from `@/fleet/WorkspaceFilter` and `setViewportWidth` from `@/test-layout`.
  - In the file's `afterEach`, after `window.localStorage.clear()`, add `useFilter.setState({ stored: readFilter() })`, so one test's pick does not leak into the next (the store outlives a render).
  - In the three existing tests that preset `loom.filter` (`honours a filter the classic app stored`, `reads a stored filter for a workspace that is not listed as all, and leaves it stored`, and `says there are no sessions`'s second half), add `useFilter.setState({ stored: readFilter() })` straight after the `setItem`, since the store reads storage only when the module loads.
  - New `describe("WorkspaceFilter", ...)` tests, each loading `fixtureSnapshot(NOW)` (workspaces `loom` id 1 and `flow` id 2):
    - `draws no filter of its own in the fleet panel on desktop, and one on the phone`: `setViewportWidth(1100)` then `render(<Body />)`: `screen.queryByLabelText("Workspaces")` is null; unmount, `setViewportWidth(1400)`, render: still null; unmount, `setViewportWidth(390)`, render: the group is present. Fails without: the `layout === "phone"` gate in `Fleet` (the desktop renders draw the group).
    - `a pick in the filter shows in the fleet and in what the new-session form reads`: `setViewportWidth(1100)`, render `<><WorkspaceFilter /><Body /></>`; click `flow` in the `Workspaces` group; `flow-docs` shows, `fix-pane-cursor` does not, `window.localStorage.getItem("loom.filter")` is `'"2"'` and `readFilter()` is `"2"`; click `All`: both show and storage holds `'""'`. Fails without: `Fleet` reading `useFilter` (the fleet keeps its own copy and does not follow the pick), or `pick`'s write (`readFilter()` stays `""`).
    - `draws nothing with one shown workspace`: edit the snapshot so only one workspace is shown (`shown: false` on the other, the field `shownWorkspaces` reads); `render(<WorkspaceFilter />)` gives an empty container. Fails without: the fewer-than-two check, removed.
    - `reads a stored workspace that is not listed as all, and leaves it stored`: preset `loom.filter` to `'"9"'`, `useFilter.setState({ stored: readFilter() })`, render `<WorkspaceFilter />`: `All` has `aria-pressed="true"` and storage still holds `'"9"'`. Fails without: `activeFilter` in `WorkspaceFilter` (no item is pressed).
    - `drops the 44px touch minimum when dense`: render `<WorkspaceFilter />` and `<WorkspaceFilter dense />` separately; the `All` item's class list contains `after:min-h-[44px]` without `dense` and does not with it. Fails without: the `dense` override in `WorkspaceFilter`, removed.
  - The other existing fleet tests (`narrows to a workspace, stores the choice and restores it on the next launch`, `selects all again when the chosen workspace is chosen twice`, `names the workspace on each row only while the fleet shows all of them`) stay as they are and now run through the shared store.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/fleet/fleet.test.tsx src/panels/new/new.test.tsx
```

Expected: `fleet.test.tsx` fails to load, `Failed to resolve import "@/fleet/WorkspaceFilter"`; `new.test.tsx` passes.

- [ ] **Step 3: Implement** `fleet/WorkspaceFilter.tsx`, and rewire the fleet panel and the new-session form as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/fleet/fleet.test.tsx src/panels/new/new.test.tsx
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/
```

Expected: both suites pass with the new `WorkspaceFilter` tests, `check` passes, `dist:check` prints no difference, and `ok  github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/fleet/WorkspaceFilter.tsx internal/web/dist
git commit -m "refactor(web): the fleet, the new-session form and the desktop share one workspace filter" -- internal/web/ui/src/fleet/WorkspaceFilter.tsx internal/web/ui/src/panels/fleet/index.tsx internal/web/ui/src/panels/fleet/fleet.test.tsx internal/web/ui/src/panels/new/NewSessionBody.tsx internal/web/dist
```

## Story 8: The desktop's top bar

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/desktop/TopBar.tsx`, `internal/web/ui/src/desktop/TopBar.test.tsx`, `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `FleetLinks` (with `dense`), `TopBar`; uses `HostLabel`, `LinkPill`, `GroupCounts`, `WorkspaceFilter`, `readFilter` (Story 7), `DesktopShell`, `PanelHead`, the desktop slots, `setViewportWidth` (Story 6)
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** light (chrome assembled from shared parts)
- **Form:** brief (a bar of existing parts)

The desktop frame gets its top bar (spec §5, §7, §8): one dense row across the window holding the link's pill and `loom@host`, the workspace filter, the three group counts and the fleet's links. The pieces are the shared ones Story 7 made (`LinkPill`, `HostLabel`, `GroupCounts`, `WorkspaceFilter`), each drawn `dense`, and the fleet's links, which this story lifts out of `FleetHead` into `FleetLinks` so `FleetHead` (the phone's fleet head, without `dense`) and the top bar draw one copy. `DesktopShell` (Story 6) draws the bar above the middle on every route but the full mockup, and on the fleet route its middle draws no Head, since the bar carries the fleet's links and the filter (spec §8). The palette's box is not here: it comes with the palette in part 2. The phone draws exactly what it draws today. No code in this story holds a landmine marker; its developer runs at the default effort. Every task rebuilds `internal/web/dist/` and commits what changed with the source (a task that nothing in the entry imports yet may leave the bundle unchanged; `dist:check` says which).

### Task 8.1: `FleetLinks`, shared by `FleetHead` and the top bar

**Files:**
- Modify: `internal/web/ui/src/panels/fleet/FleetHead.tsx`
- Create: `internal/web/ui/src/desktop/TopBar.test.tsx` (its first test, `FleetLinks`'s density; Task 8.2 adds the rest)
- Modify: `internal/web/dist/` (rebuilt)

**Behaviour.** Move the flows, settings and new-session links out of `FleetHead` into an exported `FleetLinks(props: { dense?: boolean }): ReactElement` in the same file, drawing exactly what `FleetHead` puts in its `actions` fragment today (the `flows` link only while a shown session has flows, the settings link and the `+ New` link with its stale gate), reading `useFleet` and `useLink()` as `FleetHead` does now. `FleetHead` becomes `return <ContextRow title="Fleet" actions={<FleetLinks />} />;`, and loses the imports and the `hasFlows`/`stale` reads the move leaves unused (`allSessions`, `shownWorkspaces`, `useFleet`, `useLink`, `MouseEvent`, `Settings`, `cn`, `hrefFor`, whatever `tsc` and eslint name); the file's top comment keeps saying it is the fleet's context row and gains that `FleetLinks` is the links alone, for the desktop's top bar too.

`dense` is the top bar's density (spec §11): the module's `LINK` constant is `"flex min-h-[44px] min-w-[44px] flex-none items-center justify-center text-sm"` (the phone's touch size); with `dense` each of the three links takes, in its place, `"flex h-7 min-w-7 flex-none items-center justify-center px-1.5 text-sm"`, and the `+ New` link keeps its `rounded-md bg-secondary px-3 font-medium text-foreground` look (its `px-3` after the base class wins over `px-1.5` through `cn`). Without `dense` every class is as today.

Names defined: `FleetLinks` (with `dense`). Names used: `ContextRow` (`app/shell`), `hrefFor` (`app/routes`), `allSessions`, `shownWorkspaces` (`fleet/groups`), `useFleet`, `useLink` (`store/fleet`), `cn`.

- [ ] **Step 1: Write the failing test** in `internal/web/ui/src/desktop/TopBar.test.tsx` (new file; its imports are Vitest, Testing Library, `fixtureSnapshot` from `@/fixtures`, `useFleet` from `@/store/fleet`, `FleetLinks` from `@/panels/fleet/FleetHead`; `afterEach` resets the store with `useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 })` and the address with `window.history.replaceState(null, "", window.location.pathname)`). One test now:
  - `FleetLinks keeps its 44px touch minimum, and drops it when dense`: load `fixtureSnapshot()` as the live snapshot (the fleet's tests do it with `useFleet.setState({ snapshot, stale: false, link: "live", updated: Date.now() })`; the fixture's sessions hold flows, so the Flows link is drawn); render `<FleetLinks />` and read the class of the `Flows`, `Settings` and `+ New` links: each contains `min-h-[44px]`; unmount, render `<FleetLinks dense />`: none contains `min-h-[44px]` or `min-w-[44px]`. Fails without: the `dense` branch in `FleetLinks`, removed (the dense links keep the minimum), or inverted (the plain links lose it).

  `FleetHead`'s own behaviour is pinned by the existing tests in `panels/fleet/fleet.test.tsx` (`FleetHead shows the flows link only when a listed session has flows`, `FleetHead ignores the flows of a session in a hidden workspace`, `FleetHead links settings and new to their panels`, `+ New is dimmed and does nothing while the view is stale`), which stay as they are and must keep passing.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/TopBar.test.tsx
```

Expected: the test fails, `FleetLinks` is not exported from `@/panels/fleet/FleetHead` (`undefined` is not a valid element type).

- [ ] **Step 3: Implement** `FleetLinks` and rewire `FleetHead` as the behaviour above says, then format the two files with `pnpm -C internal/web/ui exec prettier --write src/panels/fleet/FleetHead.tsx src/desktop/TopBar.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/TopBar.test.tsx src/panels/fleet/fleet.test.tsx
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check
```

Expected: both suites pass (the four `FleetHead` tests unchanged), `check` passes, and `dist:check` prints no difference after the build.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/desktop/TopBar.test.tsx internal/web/dist
git commit -m "refactor(web): the fleet's links are FleetLinks, with a dense form for the desktop" -- internal/web/ui/src/panels/fleet/FleetHead.tsx internal/web/ui/src/desktop/TopBar.test.tsx internal/web/dist
```

### Task 8.2: `TopBar`

**Files:**
- Create: `internal/web/ui/src/desktop/TopBar.tsx`
- Test: `internal/web/ui/src/desktop/TopBar.test.tsx`
- Modify: `internal/web/dist/` (rebuilt; nothing imports the bar yet, so the bundle may be unchanged)

**Behaviour.** `TopBar(): ReactElement` in `desktop/TopBar.tsx` draws one row, `<div data-slot="top-bar" className="flex h-9 flex-none items-center gap-2 border-b border-border bg-background px-3 text-xs">` (not `data-slot="header"` or `"footer"`: those are the phone's frame, and Story 6's tests expect the desktop to draw neither), holding in this order `<LinkPill dense />`, `<HostLabel dense />` (it has `flex-1`, so it takes the row's slack and pushes what follows to the right), `<WorkspaceFilter dense />`, `<GroupCounts dense />` (a fragment of siblings, as in the footer) and `<FleetLinks dense />` (a fragment of links). The palette's box is not drawn: it is part 2's, and the row has room for it between `HostLabel` and the filter. `TopBar` reads nothing itself: every part reads the store, the link and the route as it does in the footer and the fleet.

Names used: `LinkPill`, `HostLabel`, `GroupCounts` (`app/status`), `WorkspaceFilter` (`fleet/WorkspaceFilter`), `useFilter`, `readFilter` (`fleet/filter`) in the test, `FleetLinks` (Task 8.1). Names defined: `TopBar`.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/desktop/TopBar.test.tsx`: add to its imports `TopBar` from `./TopBar`, `userEvent` from `@testing-library/user-event`, `hrefFor` from `@/app/routes`, `fleetGroups` from `@/fleet/groups`, `useFilter` and `readFilter` from `@/fleet/filter`, and `act`, `within` from Testing Library; extend `afterEach` with `window.localStorage.clear()` and `useFilter.setState({ stored: readFilter() })` (a pick outlives a render in the store). A helper `load(link = "live")` sets the store to `fixtureSnapshot()` with `stale: link !== "live"`, `link` and `updated: Date.now()`. The fixture's host is `fresco`; it has two shown workspaces (`loom` id 1, `flow` id 2), two needs-you, one your-turn and three working sessions, and sessions with flows.
  - `TopBar draws the link, the host, the filter, the counts and the fleet's links`: live; `LIVE`, `fresco` (inside the `top-bar` slot), the `Workspaces` group with `All`, `loom` and `flow`, buttons `2 need you: open the next`, `1 your turn: open the next` and `3 working: open the next`, and the links `Flows` (href `#/flows`), `Settings` (`#/settings`) and `+ New` (`#/new`). Fails without: any of `LinkPill`, `HostLabel`, `WorkspaceFilter`, `GroupCounts` or `FleetLinks` in `TopBar` (its text, group, buttons or links are missing).
  - `TopBar draws every part dense`: the `top-bar` slot holds no element whose class contains `min-h-[44px]` (a `[class*="min-h-[44px]"]` query is empty, which also catches the filter items' `after:min-h-[44px]`). Fails without: `dense` on any one part in `TopBar` (that part's 44px minimum is found).
  - `a count opens the group's next session, as the footer's do`: live; with the address `#/`, click `2 need you: open the next`: the hash is `hrefFor("turns", { id })` with the id of `fleetGroups(snapshot).needs_you[0]`; set the address to that session's route and click again: the hash is the second needs-you session's; click once more from the second: back to the first (wraps). Read the ids from `fleetGroups(useFleet.getState().snapshot!)`, never a literal. Fails without: `GroupCounts` in `TopBar` (no button), or its `current` read from the route (the second click lands on the first session again).
  - `a pick in the bar's filter is the shared choice`: live; click `flow` in the `Workspaces` group (`within(screen.getByRole("group", { name: "Workspaces" }))`): `useFilter.getState().stored` is `"2"` and `localStorage.getItem("loom.filter")` is `'"2"'`; click `All`: `""` and `'""'`. Fails without: `WorkspaceFilter` in `TopBar`, or the bar drawing a filter of its own that does not call `useFilter`'s `pick`.
  - `TopBar says when the view is stale: the pill, the host's time and a dimmed New`: `load("offline")` with `updated` set to a fixed time: `OFFLINE` is drawn, the host label reads `fresco · <clock>` (the expected clock normalised as `app/shell.test.tsx`'s `StatusRow` test does: `toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }).replace(/\s+/g, " ")`), `+ New` has `aria-disabled="true"` and a click on it leaves the hash unchanged. Fails without: `LinkPill` or `HostLabel` in `TopBar` (no pill or time), or `FleetLinks`' stale gate.
  - `TopBar draws the fleet's links without a Flows link when no shown session has flows`: set every session's `flows` to `null` in the loaded snapshot; no `Flows` link, `Settings` and `+ New` still drawn. Fails without: `FleetLinks`' `hasFlows` gate, removed.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/TopBar.test.tsx
```

Expected: the suite fails to load, `Failed to resolve import "./TopBar"`.

- [ ] **Step 3: Implement** `desktop/TopBar.tsx` as the behaviour above says (a file comment: the desktop's top bar, chrome and not a panel, spec §7), then format it and the test with prettier.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/TopBar.test.tsx
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check
```

Expected: `TopBar.test.tsx` passes with its tests, `check` passes and `dist:check` prints no difference.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/desktop/TopBar.tsx internal/web/dist
git commit -m "feat(web): the desktop's top bar from the shared status parts and the fleet's links" -- internal/web/ui/src/desktop/TopBar.tsx internal/web/ui/src/desktop/TopBar.test.tsx internal/web/dist
```

### Task 8.3: `DesktopShell` draws the top bar, and no Head on the fleet route

**Files:**
- Modify: `internal/web/ui/src/desktop/DesktopShell.tsx`
- Modify: `internal/web/ui/src/desktop/DesktopShell.test.tsx`
- Modify: `internal/web/dist/` (rebuilt; the `DesktopShell` chunk changes)

**Behaviour.** In `desktop/DesktopShell.tsx` as Story 6 left it, three edits, each anchored on the exact lines below.

1. Import the bar, with the other local import's place (after the `@/…` imports, as the lint's order wants): `import { TopBar } from "./TopBar";`.
2. In `Middle`, replace the line

```tsx
        <PanelHead route={route} session={session} />
```

with the same line guarded so the fleet route's head holds nothing (the `<header>` is `empty:hidden`, so an empty one is not drawn):

```tsx
        {route.panel === "fleet" ? null : <PanelHead route={route} session={session} />}
```

   On desktop the fleet's context row (its title and links) is not drawn: the top bar carries the links (spec §8), and the fleet panel's Body is drawn in full in the scroller.
3. In `DesktopShell`'s return, draw `<TopBar />` as the first child of the `data-slot="desktop"` column, above the row that holds the middle, so that the lines

```tsx
    <div data-slot="desktop" className="flex h-dvh min-h-0 flex-col bg-background text-foreground">
      <div className="flex min-h-0 flex-1">
        <Middle />
      </div>
    </div>
```

   read, with `<TopBar />` added between the first and second line and nothing else changed. The early `return <Outlet />` for `mockupFull` stays above it, so the full mockup draws no bar. Stories 9 and 10 add the sidebar and the inspector inside that row, beside `<Middle />`.

Names used: `TopBar` (Task 8.2), `PanelHead`, the slots `data-slot="desktop"`, `"middle"`, `"middle-head"` and `"middle-scroller"` (Story 6), `definePanel`. The tests render `DesktopShell` through Story 6's `desktopAt` helper in `DesktopShell.test.tsx` (the frame is drawn directly, so they need no `setViewportWidth`).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/desktop/DesktopShell.test.tsx`: add a `describe("DesktopShell's top bar", ...)` after the existing blocks, using the file's own `desktopAt`, `live`, `tabbed` and `slot` helpers (`slot("top-bar")` reads `[data-slot="top-bar"]`):
  - `draws the top bar above the middle, inside the frame`: `tabbed()`, `live()`, `desktopAt("#/s/100")`; the `top-bar` slot exists, is inside `slot("desktop")`, and precedes `slot("middle")` (`compareDocumentPosition` has `DOCUMENT_POSITION_FOLLOWING`); it holds a `Settings` link. Fails without: `<TopBar />` in `DesktopShell` (no slot), or drawn after the row (the order check fails).
  - `draws the top bar on a route with no session`: `desktopAt("#/settings")` after `definePanel({ id: "settings", title: () => "Settings", Body: () => null })`: the slot exists. Fails without: the bar drawn only for a session route.
  - `draws no head on the fleet route, and its Body in full`: `definePanel({ id: "fleet", title: () => "Fleet", Head: () => <p>fleet head</p>, Body: () => null })`, `live()`, `desktopAt("#/")`: `await screen.findByText("outlet")`, `queryByText("fleet head")` is null, `slot("middle-head")` is empty (`toBeEmptyDOMElement()`), and the bar's `+ New` link is drawn. Fails without: the `route.panel === "fleet"` guard in `Middle` (the head `fleet head` is drawn).
  - `still draws the head on a session route`: `tabbed()`, `live()`, `desktopAt("#/s/100")`: `turns head` is inside `slot("middle-head")`. Fails without: the guard inverted or widened to every route (the head is gone on a session route).
  - `draws no top bar on the full mockup`: `desktopAt("#/s/100/mockup/full")`: `await screen.findByText("outlet")`; `slot("top-bar")` is null and so is `slot("desktop")`. Fails without: `<TopBar />` drawn above the early `return <Outlet />` for `mockupFull` (the slot exists).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/DesktopShell.test.tsx
```

Expected: the first three new tests fail (`top-bar` slot not found; `fleet head` found); the last two and every earlier test pass.

- [ ] **Step 3: Implement** the three edits above, then format with `pnpm -C internal/web/ui exec prettier --write src/desktop/DesktopShell.tsx src/desktop/DesktopShell.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/DesktopShell.test.tsx src/desktop/TopBar.test.tsx src/app/shell.test.tsx
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/
```

Expected: the three suites pass, `check` passes, `dist:check` prints no difference after the build, and `ok  github.com/aidan-bailey/loom/internal/web` (`TestDist_LazyViewsAreChunksOfTheirOwn` still sees `DesktopShell` as a chunk of its own and `index.html` naming none).

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the desktop frame draws the top bar, and the fleet route no head" -- internal/web/ui/src/desktop/DesktopShell.tsx internal/web/ui/src/desktop/DesktopShell.test.tsx internal/web/dist
```

## Story 9: The desktop's fleet sidebar

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/desktop/Sidebar.tsx`, `internal/web/ui/src/desktop/Sidebar.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `SidebarLine`, `sidebarLine`, `Sidebar`, `DOT` (exported); uses `useFilter`, `activeFilter`, `readFilter` (Story 7), `useDesktop`, `DesktopShell`, `setViewportWidth` (Story 6), `useFleet`, `Collapsible`, `CollapsibleTrigger`, `CollapsibleContent`, `GROUPS`, `groupSessions`, `shownWorkspaces`, `sessionHref`, `ago`, `flowChip`, `flowTone`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** light (a list drawn from existing fleet helpers; its one decision, `sidebarLine`, is pinned by named tests)
- **Form:** brief (rows from existing helpers)

The desktop frame gets its fleet sidebar (spec §8): the fleet's groups over the shown workspaces under the shared workspace filter, in compact two-line rows. A row holds the group's dot, the title and its age; under them one line, `sidebarLine`'s (why the session waits, else its newest flow's chip, else what it said last), in that line's tone. The session on screen is highlighted, each row opens `sessionHref`, and Paused is folded closed as on the phone. The sidebar is chrome, not a panel: it is a plain list of links, so it needs no command, boundary or scroll pane, and it is denser than the phone's fleet (no 44px minimum, spec §11). `DesktopShell` (Story 6, with Story 8's top bar) draws it beside the middle on every route but the fleet, while `useDesktop().sidebar`. The dot colours are the fleet's own: Task 9.1 exports `DOT` from `SessionRow.tsx` rather than copying it. No code in this story holds a landmine marker; its developer runs at the default effort. Every task rebuilds `internal/web/dist/` and commits what changed with the source (a task that nothing imports yet may leave the bundle unchanged; `dist:check` says which).

### Task 9.1: `DOT` exported, and `sidebarLine`

**Files:**
- Modify: `internal/web/ui/src/panels/fleet/SessionRow.tsx:60`
- Create: `internal/web/ui/src/desktop/Sidebar.tsx` (the line only; Task 9.2 adds the component)
- Create: `internal/web/ui/src/desktop/Sidebar.test.tsx` (the `sidebarLine` tests; Task 9.2 adds the rest)
- Modify: `internal/web/dist/` (rebuilt; nothing imports the sidebar yet, so the bundle may be unchanged)

**Behaviour.** In `panels/fleet/SessionRow.tsx`, one word changes: the line

```tsx
const DOT: Record<string, string> = {
```

becomes `export const DOT: Record<string, string> = {`; nothing else in the file changes (the fleet's rows keep using it).

In `desktop/Sidebar.tsx` (a file comment: the desktop's fleet sidebar, chrome and not a panel, spec §8), export `interface SidebarLine { text: string; tone: "attention" | "docs" | "error" | "muted" }` and `function sidebarLine(session: Session): SidebarLine`, the sidebar row's second line, decided in this order:

1. While `session.group === "needs_you"`, why it waits: `wait.reason` and `wait.detail` joined by `": "`, an empty one left out (`"Allow Bash"` and `"go test ./ui/..."` read `Allow Bash: go test ./ui/...`; a reason alone or a detail alone is itself). Tone `"attention"`. When both are empty this step gives nothing and the next ones decide, so a waiting row is not blank.
2. Else, when the session has flows (`session.flows` is `null` for none), its newest flow's chip, `flowChip(flow)` (`panels/fleet/SessionRow`), in the tone `flowTone(flow)` gives (`"docs"`, `"attention"` or `"error"`). The newest flow is the one with the greatest `Date.parse(flow.updated)`; a time that does not parse counts as 0, and of equal times the first in the list wins.
3. Else `session.last_message` in the tone `"muted"` (an empty message is an empty text; the row then draws no line).

Names defined: `SidebarLine`, `sidebarLine`. Names used: `Session`, `FlowSummary` (`@/api/gen`), `flowChip`, `flowTone` (`@/panels/fleet/SessionRow`).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/desktop/Sidebar.test.tsx` (new file; imports Vitest, `Session` and `FlowSummary` from `@/api/gen`, `fixtureSnapshot` from `@/fixtures`, `allSessions` from `@/fleet/groups`, `flowChip`, `flowTone` from `@/panels/fleet/SessionRow`, and `sidebarLine` from `./Sidebar`). A module constant `NOW = Date.parse("2026-11-18T12:00:00Z")`, `const snapshot = fixtureSnapshot(NOW)` and `byId(id)` reading `allSessions(snapshot)` (as `fleet.test.tsx` does) give the fixture's sessions: 2 (`sandbox-ask`, needs you, wait `Allow Bash` with detail `go test ./ui/... -run TestPane`), 4 (`flow-docs`, needs you, reason `Plan approval`, no detail), 1 (working, a last message), 3 (working, no last message) and 6 (`flow-run`, working, two flows: first `parked` updated 4 minutes ago with `asks` set, second `designed` updated 72 hours ago). A helper `flowOf(session, i)` reads `session.flows![i]`. Read every expected text from the session or from `flowChip`, never as a literal copied from the chip's rules, except the wait texts above. Tests, in a `describe("sidebarLine", ...)`:
  - `words a waiting session's why: the reason, then the detail`: session 2 gives `{ text: "Allow Bash: go test ./ui/... -run TestPane", tone: "attention" }`; session 4 (reason only) gives `{ text: "Plan approval", tone: "attention" }`; session 4 with `wait` set to `{ reason: "", detail: "go test ./..." }` gives the text `go test ./...`. Fails without: the `needs_you` branch in `sidebarLine` (session 2 gives its last message, muted), or the join (the detail is dropped or the reason is lost), or the empty-part rule (a lone detail gets a leading `": "`).
  - `a waiting session with no reason and no detail falls through to what it said`: session 2 with `wait` set to `{ reason: "", detail: "" }` and `last_message` `"thinking"` gives `{ text: "thinking", tone: "muted" }`. Fails without: the empty check on step 1 (the text is `""` with the attention tone).
  - `a waiting session shows why it waits ahead of its flow`: session 2 with `flows` set to session 6's flows gives the wait text, tone `"attention"`. Fails without: the order of the steps (the flow's chip is shown for a waiting session).
  - `a session with flows shows its newest flow's chip in the flow's tone`: session 6 gives `{ text: flowChip(flowOf(6, 0)), tone: flowTone(flowOf(6, 0)) }`, whose tone is `"attention"` (it asks); the same session with its flows in the opposite order (`[...flows].reverse()`) gives the same; with only the `designed` flow, the text is `flowChip` of it and the tone `"docs"`; with that flow's `status` set to `"lost"` the tone is `"error"`. Assert the first case's text differs from `flowChip(flowOf(6, 1))`, so the test cannot pass on either flow. Fails without: the newest choice (the reversed list shows the old flow's chip, so `flows[0]` or `flows[last]` taken instead fails one of the two orders), or `flowTone` (the tone is fixed).
  - `equal or unreadable flow times keep the first flow`: session 6 with both flows' `updated` set to `""`, and then with both set to one time, gives the chip of the first in each case. Fails without: the tie rule (a `>=` comparison picks the last).
  - `a session with no flow and no wait shows its last message, muted`: session 1 gives `{ text: session.last_message, tone: "muted" }`, the text not empty; session 3 gives text `""` with tone `"muted"`; session 1 with `flows` set to `[]` gives the same as with `null`. Fails without: the last-message step (the text is empty), or a `flows!` read of a `null` (the `null` case throws).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/Sidebar.test.tsx
```

Expected: the suite fails to load, `Failed to resolve import "./Sidebar"`.

- [ ] **Step 3: Implement** the `DOT` export and `desktop/Sidebar.tsx` with `SidebarLine` and `sidebarLine` as the behaviour above says, then format with `pnpm -C internal/web/ui exec prettier --write src/panels/fleet/SessionRow.tsx src/desktop/Sidebar.tsx src/desktop/Sidebar.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/Sidebar.test.tsx src/panels/fleet/fleet.test.tsx
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check
```

Expected: both suites pass (the fleet's tests unchanged), `check` passes and `dist:check` prints no difference after the build.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/desktop/Sidebar.tsx internal/web/ui/src/desktop/Sidebar.test.tsx internal/web/dist
git commit -m "feat(web): the sidebar's row line, and the fleet's dot colours exported" -- internal/web/ui/src/panels/fleet/SessionRow.tsx internal/web/ui/src/desktop/Sidebar.tsx internal/web/ui/src/desktop/Sidebar.test.tsx internal/web/dist
```

### Task 9.2: `Sidebar`

**Files:**
- Modify: `internal/web/ui/src/desktop/Sidebar.tsx`
- Test: `internal/web/ui/src/desktop/Sidebar.test.tsx`
- Modify: `internal/web/dist/` (rebuilt; nothing imports the component yet, so the bundle may be unchanged)

**Behaviour.** `Sidebar(props: { current: number | undefined }): ReactElement` in `desktop/Sidebar.tsx`, a `<nav data-slot="sidebar" aria-label="Sessions">` that is `w-[250px] flex-none overflow-y-auto border-r border-border` (it scrolls on its own when the fleet is long). It reads `useFleet`'s snapshot and `useFilter((s) => s.stored)`, and draws, in `GROUPS`' order, one group for each of the four that holds a session:

- The sessions are those of the shown workspaces (`shownWorkspaces(snapshot)`, `allSessions(snapshot)` kept to those workspaces), grouped by `groupSessions(listed, active === "" ? null : Number(active))` with `active = activeFilter(stored, workspaces)`, exactly as the fleet panel does (the groups' sort order is `groupSessions`'s). A stored id that names no shown workspace is read as all and left stored.
- A group is `<section data-slot="sidebar-group" data-group={key}>` holding a heading (the group's title and its count, in the group's colour: `text-attention` for needs you, `text-turn` for your turn, `text-working` for working, `text-muted-foreground` for paused; `text-xs font-semibold uppercase`) and its rows. Paused is a `Collapsible` (`@/components/ui/collapsible`), closed by default, whose `CollapsibleTrigger` is the heading and whose `CollapsibleContent` holds the rows, so the closed group draws no row; the other groups are always open.
- A row is `<a data-slot="sidebar-row" href={sessionHref(session)}>`, `block border-b border-border px-3 py-1.5` with no minimum height, holding two lines. The first is a flex row: the group's dot (`<span data-slot="sidebar-dot" aria-hidden="true">`, `size-2 flex-none rounded-full` with `DOT[session.group] ?? "bg-muted-foreground"`, from `@/panels/fleet/SessionRow`), the title (`min-w-0 flex-1 truncate text-[13px] font-medium`) and `ago(session.since)` (`flex-none font-mono text-[11px] text-muted-foreground`). The second is `sidebarLine(session)`'s text, drawn only when the text is not empty, as `<div data-slot="sidebar-line" data-tone={tone}>` that is `block truncate pl-4 text-xs` in the tone's colour (`text-attention`, `text-docs`, `text-error`, `text-muted-foreground`), the `block` and `truncate` on the one element.
- The row whose `session.id === current` is highlighted: `aria-current="page"` and `bg-secondary`; no other row has the attribute, and an `undefined` current highlights none.
- With no session listed the nav draws `No sessions.` (the filter on all) or `No sessions in this workspace.` (a workspace chosen); with no snapshot it draws the nav empty.
- Ages move with the clock, not only the stream: a `setInterval` of 30,000 ms, cleared on unmount, re-renders the sidebar, as the fleet panel's does.

The sidebar draws no workspace filter, no usage share and no meta line: the top bar carries the filter (Story 8) and the compact row has two lines.

Names used: `useFleet` (`@/store/fleet`), `useFilter`, `activeFilter` (`@/fleet/filter`, Story 7), `GROUPS`, `groupSessions`, `allSessions`, `shownWorkspaces` (`@/fleet/groups`), `sessionHref` (`@/fleet/links`), `ago`, `DOT` (`@/panels/fleet/SessionRow`), `Collapsible`, `CollapsibleTrigger`, `CollapsibleContent`, `cn`, `sidebarLine` (Task 9.1). Names defined: `Sidebar`.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/desktop/Sidebar.test.tsx`: extend its imports with Testing Library's `act`, `render`, `screen`, `within`, `userEvent`, `hrefFor` from `@/app/routes`, `readFilter` and `useFilter` from `@/fleet/filter`, `sessionHref` from `@/fleet/links`, `useFleet` from `@/store/fleet`, `Sidebar` from `./Sidebar`. Add `beforeEach` that fakes only `Date` (`vi.useFakeTimers({ toFake: ["Date"] })`, `vi.setSystemTime(NOW)`) and loads the store live: `useFleet.setState({ snapshot: fixtureSnapshot(NOW), stale: false, link: "live", updated: NOW })`; and `afterEach` that calls `vi.useRealTimers()`, resets the store (`snapshot: null, stale: true, link: "connecting", updated: 0`), clears `localStorage` and `useFilter.setState({ stored: readFilter() })`. Helpers: `group(key)` is `document.querySelector('[data-slot="sidebar-group"][data-group="<key>"]')`, `titles(key)` the rows' title texts inside it, `row(title)` the `sidebar-row` link whose text starts with the title. The fixture's workspaces are `loom` (1) and `flow` (2), both shown; its sessions are those listed in Task 9.1. Tests, in a `describe("Sidebar", ...)`:
  - `draws the groups in the fleet's order, each with its count and its sessions`: `render(<Sidebar current={undefined} />)`; the `sidebar-group` elements' `data-group` values are `needs_you`, `your_turn`, `working`, `paused`; `titles("needs_you")` is `["flow-docs", "sandbox-ask"]`, `titles("your_turn")` the one long title, `titles("working")` `["fix-pane-cursor", "flow-run", "plain-shell"]`; each group's heading holds its count (`2`, `1`, `3`, `1`). Fails without: `GROUPS`' order or `groupSessions`' sort in the sidebar (the order or the titles differ), or a missing group.
  - `draws a row as the dot, the title, the age and the line`: session 2's row (`sandbox-ask`) holds the age `18m`, a `sidebar-dot` whose class contains `bg-attention` and a `sidebar-line` with `data-tone="attention"` and the text `Allow Bash: go test ./ui/... -run TestPane`; `flow-run`'s dot class contains `bg-working` and its line is `flowChip` of its newest flow with `data-tone="attention"`; `fix-pane-cursor`'s line is its last message with `data-tone="muted"`; `plain-shell`'s row has no `sidebar-line`. Fails without: `DOT` in the dot (no colour class), `ago` (no age), or `sidebarLine` in the row (a missing, empty or wrong-toned line).
  - `a row is dense: no 44px minimum, and its line is clipped on a block element`: no row nor any element inside the sidebar has a class containing `min-h-[44px]`; the `sidebar-line` of `sandbox-ask` has the classes `block` and `truncate`. Fails without: the dense row (a 44px class is found), or `block` on the line (the clip does not apply to an inline element).
  - `a row opens the session's conversation, or its fresh mockup`: `row("fix-pane-cursor")` has `href` `sessionHref` of session 1 and `hrefFor("turns", { id: "1" })`; the long-titled session 7 (its mockup is fresh) has `hrefFor("mockup", { id: "7" })`, which is not its turns link. Fails without: `sessionHref` in the row (a hand-built turns link opens session 7's conversation).
  - `highlights the session on screen`: `current={2}`: only `row("sandbox-ask")` has `aria-current="page"` (exactly one row in the sidebar has the attribute); `rerender` with `current={1}` moves it to `fix-pane-cursor`; `current={undefined}` leaves none; `current={999}` (a session no row has) leaves none. Fails without: the `aria-current` from `current` (none or all are highlighted), or a comparison on position instead of the id.
  - `folds Paused until it is opened`: `flow-old` is not in the document and the Paused heading is a button named `/^Paused/` that holds `1`; a click on it draws `flow-old` (awaited with `findByText`) inside `group("paused")`, whose dot class contains `bg-muted-foreground`; a second click folds it again (`waitFor` it gone). Fails without: the `Collapsible` on Paused (the row is drawn at once).
  - `shows only the chosen workspace's sessions`: `act(() => useFilter.getState().pick("2"))` then render: `titles("needs_you")` is `["flow-docs"]`, `titles("working")` `["flow-run"]`, no `your_turn` group, and `sandbox-ask` is not in the document; after `pick("")` (an act) all return. Fails without: the workspace argument of `groupSessions` (the other workspace's rows stay), or `useFilter` read at all (the pick changes nothing).
  - `reads a stored workspace that no shown workspace has as all, and leaves it stored`: `localStorage.setItem("loom.filter", JSON.stringify("9"))`, `useFilter.setState({ stored: readFilter() })`, render: `sandbox-ask` and `flow-docs` are both drawn, and the stored value is still `'"9"'`. Fails without: `activeFilter` (the unlisted id filters every session out).
  - `lists no session of a hidden workspace`: set the second workspace's `shown` to `false` in the loaded snapshot: `flow-docs`, `flow-run` and `flow-old` (after opening Paused) are not drawn, `sandbox-ask` is. Fails without: `shownWorkspaces` in the sidebar (the hidden workspace's sessions are listed).
  - `says so when nothing is listed`: a snapshot whose `sessions` is `{}`: the text `No sessions.`; with the filter picked to `"2"` and only workspace 1's sessions left in the snapshot: `No sessions in this workspace.`. Fails without: the empty lines (nothing is drawn, or the same text for both).
  - `draws nothing but the nav with no snapshot`: the store reset to `snapshot: null`: the `sidebar` slot exists and holds no row or group. Fails without: the null guard (the render throws on a `null` snapshot).
  - `ages move with the clock`: `vi.useRealTimers()` then `vi.useFakeTimers({ now: NOW })` (fakes the timers as well, in this test only), render, `sandbox-ask`'s age is `18m`; `act(() => vi.advanceTimersByTime(5 * 60_000 + 30_000))` and the age reads `23m`; after `unmount`, `vi.getTimerCount()` is `0`. Fails without: the interval (the age stays `18m`), or its cleanup (a timer is left).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/Sidebar.test.tsx
```

Expected: the `sidebarLine` tests pass and every `Sidebar` test fails, `Sidebar` is not exported from `./Sidebar` (`undefined` is not a valid element type).

- [ ] **Step 3: Implement** `Sidebar` in `desktop/Sidebar.tsx` as the behaviour above says (extending the file's imports; its comment gains the component's), then format it and the test with `pnpm -C internal/web/ui exec prettier --write src/desktop/Sidebar.tsx src/desktop/Sidebar.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/Sidebar.test.tsx
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check
```

Expected: `Sidebar.test.tsx` passes with every test, `check` passes and `dist:check` prints no difference after the build.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the desktop's fleet sidebar" -- internal/web/ui/src/desktop/Sidebar.tsx internal/web/ui/src/desktop/Sidebar.test.tsx internal/web/dist
```

### Task 9.3: `DesktopShell` draws the sidebar, on every route but the fleet

**Files:**
- Modify: `internal/web/ui/src/desktop/DesktopShell.tsx`
- Modify: `internal/web/ui/src/desktop/DesktopShell.test.tsx`
- Modify: `internal/web/dist/` (rebuilt; the `DesktopShell` chunk changes)

**Behaviour.** In `desktop/DesktopShell.tsx` as Stories 6 and 8 left it, three edits, each anchored on the exact lines below.

1. Add two imports: `import { useDesktop } from "@/app/desktopState";` with the other `@/app/…` imports (before `import type { PanelId } from "@/app/panels";`), and `import { Sidebar } from "./Sidebar";` immediately before `import { TopBar } from "./TopBar";`.
2. In `DesktopShell` (not `Middle`, which also holds `const route = useRoute();`), read the preference above the early return, so the hook runs on every render. The lines

```tsx
  const route = useRoute();
  if (route.panel === "mockupFull") return <Outlet />;
```

read, with one line added between them:

```tsx
  const route = useRoute();
  const sidebar = useDesktop((s) => s.sidebar);
  if (route.panel === "mockupFull") return <Outlet />;
```

3. In the row that holds the middle, draw the sidebar before it. The line

```tsx
        <Middle />
```

(inside `<div className="flex min-h-0 flex-1">`, the only line of the file that reads exactly so) becomes:

```tsx
        {sidebar && route.panel !== "fleet" ? (
          <Sidebar current={panelProps(route).sessionId} />
        ) : null}
        <Middle />
```

`panelProps` is already imported by the file (`Middle` uses it). On the fleet route the middle draws the fleet panel's Body in full, which the sidebar would repeat, and the early return keeps the full mockup chrome-less. Story 10 adds the inspector after `<Middle />` in the same row.

Names used: `Sidebar` (Task 9.2), `useDesktop` (Story 6, `app/desktopState`), `panelProps`, the slots `data-slot="desktop"` and `"middle"`, and the test helpers `desktopAt`, `live`, `tabbed` and `slot` (Story 6's `DesktopShell.test.tsx`; `live()` loads one Claude session numbered 100, titled `fix-pane-cursor`, the fixture's first).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/desktop/DesktopShell.test.tsx`: add `import { useDesktop } from "@/app/desktopState";` to its imports, and a `describe("DesktopShell's sidebar", ...)` after the existing blocks, with `beforeEach(() => useDesktop.setState({ sidebar: true, inspector: true, slots: [] }))` and the same call in an `afterEach`, using the file's helpers (`slot("sidebar")` reads `[data-slot="sidebar"]`). Tests:
  - `draws the sidebar before the middle, inside the frame, with the route's session highlighted`: `tabbed()`, `live()`, `desktopAt("#/s/100")`; the `sidebar` slot is inside `slot("desktop")` and precedes `slot("middle")` (`compareDocumentPosition` has `DOCUMENT_POSITION_FOLLOWING`); within it the link named `/fix-pane-cursor/` has `aria-current="page"`. Fails without: `<Sidebar />` in `DesktopShell` (no slot), drawn after the middle (the order check fails), or `current` not read from the route (no row is highlighted).
  - `draws the sidebar on a route with no session, with nothing highlighted`: `definePanel({ id: "settings", title: () => "Settings", Body: () => null })`, `live()`, `desktopAt("#/settings")`: the slot exists, holds the link `fix-pane-cursor` and no element with `aria-current`. Fails without: the guard narrowed to session routes (no sidebar), or `current` taken from a fixed id (a row is highlighted).
  - `draws no sidebar on the fleet route, and the middle in full`: `definePanel({ id: "fleet", title: () => "Fleet", Body: () => null })`, `live()`, `desktopAt("#/")`: `await screen.findByText("outlet")`, `slot("sidebar")` is null and `slot("middle")` is drawn. Fails without: the `route.panel !== "fleet"` term of the guard (the sidebar is drawn on the fleet).
  - `draws no sidebar while the preference hides it, and keeps the middle`: `useDesktop.setState({ sidebar: false })`, `tabbed()`, `live()`, `desktopAt("#/s/100")`: `slot("sidebar")` is null, `slot("middle")` is drawn and the top bar is too; then `act(() => useDesktop.getState().setSidebar(true))` draws the sidebar without a reload. Fails without: the `sidebar` term of the guard or the `useDesktop` read (the sidebar is drawn while hidden, or does not return).
  - `draws no sidebar on the full mockup`: `live()`, `desktopAt("#/s/100/mockup/full")`, `await screen.findByText("outlet")`: `slot("sidebar")` is null. Fails without: the early `return <Outlet />` for `mockupFull` (a sidebar drawn above it, outside the early return, exists on the full mockup).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/DesktopShell.test.tsx
```

Expected: the first, second and fourth new tests fail (`sidebar` slot not found); the fleet and full-mockup tests and every earlier test pass.

- [ ] **Step 3: Implement** the three edits above, then format with `pnpm -C internal/web/ui exec prettier --write src/desktop/DesktopShell.tsx src/desktop/DesktopShell.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/DesktopShell.test.tsx src/desktop/Sidebar.test.tsx src/app/shell.test.tsx
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/
```

Expected: the three suites pass, `check` passes, `dist:check` prints no difference after the build, and `ok  github.com/aidan-bailey/loom/internal/web` (`DesktopShell` is still a chunk of its own and `index.html` names none of its modules).

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the desktop frame draws the fleet sidebar, but not on the fleet" -- internal/web/ui/src/desktop/DesktopShell.tsx internal/web/ui/src/desktop/DesktopShell.test.tsx internal/web/dist
```

## Story 10: The desktop's inspector

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/desktop/inspector.ts`, `internal/web/ui/src/desktop/inspector.test.ts`, `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/desktop/Inspector.tsx`, `internal/web/ui/src/desktop/Inspector.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `inspectorTab`, `Inspector`; uses `useDesktop`, `useLayout`, `DesktopShell`, `Boundary`, `tabOf`, `resetKey`, `setViewportWidth` (Story 6), `elementPane`, `ScrollPaneProvider` (Story 4), `sessionTabs`, `PanelHost`, the panel registry (`app/panels.ts`) for the tab order
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** brief (a panel host in a second pane; `inspectorTab`'s branches are pinned by named tests)

The desktop frame at `desk3` gets its inspector (spec §6, §10): a column at the right holding another view of the session on screen. A tab strip lists the session's tab panels (`sessionTabs`) except the one the middle lights (`tabOf`); the chosen tab is the first slot of `loom.desktop` (`useDesktop().setTab` writes it) and falls, as `inspectorTab` says, to the next applicable tab when it is the middle's, does not apply to this session, or names no panel. The chosen panel's Body is drawn through `PanelHost` with the session's props (`{ sessionId, params: { id }, search: {} }`), in an `elementPane` over a scroller of its own with a `null` head (the strip sits above the scroller, outside it) and inside a `Boundary` keyed by `resetKey`, so a panel that throws replaces only the inspector's body: the middle, the sidebar and the top bar keep working. `inspectorTab` needs the order of every tab panel, which the registry holds but does not list, so Task 10.1 adds `tabOrder()` to `app/panels.ts` for it (a name of this story's own). `DesktopShell` draws the inspector after the middle, on a session route, at `desk3`, while `useDesktop().inspector`. The inspector's tabs are desktop chrome (spec §11): no 44px minimum. A link inside a panel navigates the middle, as the hash links already do. No code in this story holds a landmine marker; its developer runs at the default effort. Every task rebuilds `internal/web/dist/` and commits what changed with the source (a task that nothing imports yet may leave the bundle unchanged; `dist:check` says which).

### Task 10.1: `tabOrder`, and `inspectorTab`

**Files:**
- Modify: `internal/web/ui/src/app/panels.ts`
- Test: `internal/web/ui/src/app/panels.test.ts`
- Create: `internal/web/ui/src/desktop/inspector.ts`
- Create: `internal/web/ui/src/desktop/inspector.test.ts`
- Modify: `internal/web/dist/` (rebuilt; nothing imports either yet, so the bundle may be unchanged)

**Behaviour.**

`tabOrder(): PanelId[]` in `app/panels.ts`, beside `sessionTabs`: the ids of every registered panel that has a `tab`, in ascending `tab.order`, whether or not it applies to any session (`appliesTo` is not read). A panel without a `tab` is not in it. A comment says it is what `sessionTabs` filters, for the inspector's tab walk.

`inspectorTab(order: PanelId[], tabs: PanelId[], middle: PanelId, chosen: string | undefined): PanelId | undefined` in `desktop/inspector.ts` (the contract's signature and rules), a pure function: `order` is every tab panel in tab order, `tabs` those that apply to the session in the same order, `middle` the tab the middle lights, `chosen` the stored choice.
1. `chosen` itself when it is in `tabs` and is not `middle`.
2. Else, when `chosen` is in `order`: walk `order` from the entry after `chosen`, wrapping past the end, and return the first id that is in `tabs` and is not `middle`.
3. Else (no `chosen`, or one that is in no panel's order): the first id in `tabs` that is not `middle`.
4. `undefined` when no id qualifies.
The walk of rule 2 visits every other id of `order` once, so it never returns `chosen` (which is `middle`, or not in `tabs`, when it gets there). A file comment says what the function decides and that the stored choice is the first slot's tab (spec §6, §10).

Names defined: `tabOrder` (this story's own), `inspectorTab` (contract). Names used: `definePanel`, `sessionTabs`, `PanelId` (`app/panels`).

- [ ] **Step 1: Write the failing tests**

In `internal/web/ui/src/app/panels.test.ts`, add `tabOrder` to the import from `./panels`, and add this test after `lists the tab panels that apply to a session, in tab order`, inside the same `describe`:
  - `lists every tab panel in tab order, whether or not it applies`: define out of order `flow` (tab order 5, `appliesTo: () => false`), `info` (3), `turns` (1, `appliesTo: (s) => s.claude`), `mockup` (4, `appliesTo: () => false`), `changes` (2), each with the file's `Body`, and `settings` with no tab; `tabOrder()` is `["turns", "changes", "info", "mockup", "flow"]`, `settings` is not in it. Fails without: `tabOrder` (not a function), its sort (the definition order is returned), reading `appliesTo` (`mockup` and `flow` drop out), or reading panels without a tab (`settings` is in it).

Create `internal/web/ui/src/desktop/inspector.test.ts` (imports: Vitest's `describe`, `expect`, `it`; `PanelId` from `@/app/panels`; `inspectorTab` from `./inspector`). A constant `ORDER: PanelId[] = ["turns", "changes", "mockup", "info", "flow"]` is every tab panel in order, and `TABS: PanelId[] = ["turns", "changes", "info"]` those that apply to the session in the tests (the session has no mockup or flow). A `describe("inspectorTab", ...)` with one `it` per branch, each asserting the returned id:
  - `keeps the chosen tab when it applies and is not the middle's`: chosen `"changes"`, middle `"turns"` gives `"changes"`; chosen `"info"`, middle `"turns"` gives `"info"` (the last applicable one). Fails without: rule 1 (the chosen tab replaced by the first applicable one, `"changes"` for the second case).
  - `takes the next applicable tab after the chosen one when it is the middle's`: chosen `"turns"`, middle `"turns"` gives `"changes"`; chosen `"changes"`, middle `"changes"` gives `"info"` (`mockup`, between them in `order`, does not apply). Fails without: rule 2 (rule 3 gives `"changes"` for the second case, the middle's own tab), or a walk that starts at `chosen` rather than after it.
  - `wraps past the end of the order`: chosen `"info"`, middle `"info"` gives `"turns"` (`flow`, after `info`, does not apply, so the walk wraps). Fails without: the wrap in rule 2 (`undefined`).
  - `skips the middle's tab on the walk`: chosen `"mockup"` (does not apply), middle `"info"` gives `"turns"` (the walk meets `info`, the middle, then `flow`, which does not apply, then wraps to `turns`); chosen `"flow"`, middle `"turns"` gives `"changes"` (wraps, skips `turns`). Fails without: the `is not middle` term of the walk (`"info"` for the first case, `"turns"` for the second).
  - `takes the next applicable tab after a chosen one that does not apply`: chosen `"mockup"`, middle `"turns"` gives `"info"`. Fails without: the `in tabs` term of the walk (it returns `"mockup"`, which the session lacks), or rule 2 (it returns `"changes"`, the first applicable).
  - `falls to the first applicable tab that is not the middle's with no choice`: chosen `undefined`, middle `"turns"` gives `"changes"`; chosen `undefined`, middle `"changes"` gives `"turns"`. Fails without: rule 3.
  - `falls to the first applicable tab when the choice names no panel`: chosen `"bogus"`, middle `"turns"` gives `"changes"`; chosen `""`, middle `"turns"` gives `"changes"`. Fails without: rule 3 (a stored name that is in no order must not start a walk: it returns undefined).
  - `is undefined when nothing but the middle's tab applies`: `tabs` `["info"]`, middle `"info"`, chosen `"info"` gives `undefined`; `tabs` `[]`, middle `"turns"`, chosen `undefined` gives `undefined`. Fails without: rule 4 (the middle's own tab is returned, or a throw on an empty list).
  - `leaves its arguments as they were`: after a call, `ORDER` and `TABS` equal copies taken before it (`toEqual`). Fails without: a function that sorts or splices `order` or `tabs` in place.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/app/panels.test.ts src/desktop/inspector.test.ts
```

Expected: `inspector.test.ts` fails to load (`Failed to resolve import "./inspector"`); in `panels.test.ts` the new test fails (`tabOrder is not a function`) and the others pass.

- [ ] **Step 3: Implement** `tabOrder` and `inspectorTab` as the behaviour above says, then format with `pnpm -C internal/web/ui exec prettier --write src/app/panels.ts src/app/panels.test.ts src/desktop/inspector.ts src/desktop/inspector.test.ts`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/app/panels.test.ts src/desktop/inspector.test.ts
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check
```

Expected: both suites pass (`inspector.test.ts` with its eight tests), `check` passes and `dist:check` prints no difference after the build.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/desktop/inspector.ts internal/web/ui/src/desktop/inspector.test.ts internal/web/dist
git commit -m "feat(web): inspectorTab picks the inspector's tab, and the registry lists the tab order" -- internal/web/ui/src/app/panels.ts internal/web/ui/src/app/panels.test.ts internal/web/ui/src/desktop/inspector.ts internal/web/ui/src/desktop/inspector.test.ts internal/web/dist
```

### Task 10.2: `Inspector`

**Files:**
- Create: `internal/web/ui/src/desktop/Inspector.tsx`
- Create: `internal/web/ui/src/desktop/Inspector.test.tsx`
- Modify: `internal/web/dist/` (rebuilt; nothing imports the inspector yet, so the bundle may be unchanged)

**Behaviour.** `Inspector(props: { session: Session, middle: PanelId }): ReactElement | null` in `desktop/Inspector.tsx` (the contract's), a file comment saying it is the desktop's inspector, chrome and not a panel, spec §10.

- It reads the choice from `useDesktop((s) => s.slots[0]?.tab)`, the applicable tabs from `sessionTabs(session)` (their ids), the order from `tabOrder()`, and calls `inspectorTab(order, tabs, middle, chosen)`. Undefined: it returns `null` (nothing drawn, not an empty column).
- Otherwise it draws `<aside data-slot="inspector" aria-label="Inspector">`, a flex column that does not shrink, `330px` wide and at most a third of the window (`w-[330px] max-w-[33vw] flex-none`), with a left border in the border token and the background token, holding, in order:
  1. `<nav data-slot="inspector-tabs" aria-label="Inspector views">`: one `<button type="button">` per id of `tabs` that is not `middle` (in `tabs`' order), its text the panel's `tab.label`, `aria-current="true"` on the one `inspectorTab` returned and none on the others; a click calls `useDesktop`'s `setTab(id)`. Dense chrome: `h-8` buttons in `text-xs`, no `min-h-[44px]` or `min-w-[44px]`; the row scrolls sideways when it overflows (`overflow-x-auto`) and has a bottom border. It is above the scroller, outside it.
  2. A `Boundary` (`app/shell`) with `what` `"The inspector"` and `key={resetKey(tab, props)}`, `props` being the panel props below, around the scroller: `<div data-slot="inspector-scroller" ref={setScroller} className="min-h-0 flex-1 overflow-y-auto">` where `scroller` is state set by the callback ref, and `pane` is `useMemo(() => scroller === null ? null : elementPane(scroller, null), [scroller])`. While `pane` is `null` the scroller draws nothing, so the panel never gets `windowPane`; once it is set the scroller draws `<ScrollPaneProvider pane={pane}><PanelHost panel={tab} props={props} /></ScrollPaneProvider>`.
- `props` is `{ sessionId: session.id, params: { id: String(session.id) }, search: {} }`, the same value each render for the same session (built with `useMemo` over `session.id`) so `PanelHost`'s boundary and effects see a stable one.
- The strip is drawn by the Inspector outside the `Boundary`, so a panel whose `title` throws (which `PanelHost` calls above its own boundary) or whose Body throws leaves the strip, and a click on another tab starts over.

Names used: `useDesktop` (`app/desktopState`), `Boundary`, `PanelHost`, `resetKey` (`app/shell`), `elementPane`, `ScrollPaneProvider` (`app/scroll`), `sessionTabs`, `tabOrder`, `PanelId`, `PanelProps` (`app/panels`), `inspectorTab` (Task 10.1), `Session` (`@/api/gen`), `cn`. Names defined: `Inspector`.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/desktop/Inspector.test.tsx` (new file). Imports: Vitest's `afterEach`, `beforeEach`, `describe`, `expect`, `it`, `vi`; Testing Library's `fireEvent`, `render`, `screen`, `within`; `userEvent` from `@testing-library/user-event`; `DESKTOP_KEY`, `readDesktopPrefs`, `useDesktop` from `@/app/desktopState`; `definePanel`, `type PanelId`, `type PanelProps` from `@/app/panels`; `useScrollPane`, `windowPane` from `@/app/scroll`; `fixtureSnapshot` from `@/fixtures`; `allSessions` from `@/fleet/groups`; `Session` from `@/api/gen`; `Inspector` from `./Inspector`. `beforeEach` clears `localStorage` and sets `useDesktop.setState({ sidebar: true, inspector: true, slots: [] })`.

  Helpers: `claude(id = 7, isClaude = true): Session` is the fixture's first session (`allSessions(fixtureSnapshot())[0]`) with `id` and `claude: isClaude`. `tabs(changes: Partial<Panel> = {})` (`Panel` from `@/app/panels`) defines `turns` (tab order 1, label `conversation`, `appliesTo: (s) => s.claude`), `changes` (2, `changes`, `appliesTo: (s) => s.claude`), `mockup` (3, `mockup`, `appliesTo: () => false`) and `info` (4, `info`), so the order is turns, changes, mockup, info, each with `title` its capitalised name and a Body drawing `<p>{id} body</p>`, except that `changes`' Body draws `<p>changes body {JSON.stringify(props)}</p>` (so the props are visible); `changes` replaces fields of the `changes` definition (a throwing `Body` or `title`). A helper `strip()` is `screen.getByRole("navigation", { name: "Inspector views" })`, `lit()` the name of the button in it with `aria-current="true"`.

  Tests, in a `describe("Inspector", ...)`:
  - `lists the session's tabs but the middle's, and draws the first applicable one`: `tabs()`; render `<Inspector session={claude()} middle="turns" />`: the strip's buttons are `changes` and `info` (no `conversation`, since it is the middle's, and no `mockup`, which does not apply); `changes` is lit; `changes body` is drawn and `info body` is not. Fails without: dropping the middle from the strip (`conversation` is listed), reading `sessionTabs` (`mockup` is listed), or `inspectorTab`'s rule 3 (nothing is lit or drawn).
  - `draws the chosen panel's Body with the session's props`: `tabs()`; render for session 7; the Body's text holds `{"sessionId":7,"params":{"id":"7"},"search":{}}`. Fails without: the props built from the session (a different id, or `params` empty).
  - `a click on a tab draws it, lights it and writes loom.desktop`: `tabs()`; render for `turns`; `await userEvent.click(within(strip()).getByRole("button", { name: "info" }))`: `info body` is drawn, `changes body` is not, `lit()` is `info`, and `JSON.parse(localStorage.getItem(DESKTOP_KEY) ?? "null")` is `{ sidebar: true, inspector: true, slots: [{ tab: "info" }] }`. Fails without: the click calling `setTab` (nothing changes, nothing is stored).
  - `the choice survives a reload`: as the click test, then unmount, reset the store to the default (`useDesktop.setState({ sidebar: true, inspector: true, slots: [] })`) and then set it from what the page stored, as a reload reads it (`useDesktop.setState(readDesktopPrefs(localStorage.getItem(DESKTOP_KEY)))`); render again: `info` is lit and `info body` drawn. Fails without: the click's `setTab` writing the slot (a reload reads the default and lights `changes`).
  - `the choice falls to the next applicable tab when it is the middle's`: `useDesktop.setState({ slots: [{ tab: "info" }] })`, `tabs()`, middle `info`: the strip's buttons are `conversation` and `changes`, `conversation` is lit (`info` is last in the order, so the walk wraps to the first tab), and `conversation body` is drawn. Fails without: passing `middle` to `inspectorTab` (the middle's own tab is drawn: `info body`).
  - `the choice falls past a tab that does not apply`: slot `mockup` (which does not apply to the session), middle `turns`: `lit()` is `info`, the next applicable tab after `mockup` in the registry's order (`mockup` sits between `changes` and `info`). Fails without: `tabOrder` as `inspectorTab`'s `order` (with `tabs` for both, `mockup` is in no order and rule 3 lights `changes`), or rule 2's walk starting after `chosen` rather than at the first applicable tab (`changes`).
  - `a stored tab no panel has falls to the first applicable tab`: slot `bogus`, middle `turns`: `changes` lit. Fails without: rule 3 of `inspectorTab` (nothing is lit).
  - `follows the middle when it changes`: slot `changes`, middle `turns`: `changes` lit; `rerender` with middle `changes`: the strip's buttons are `conversation` and `info`, `info` is lit (after `changes`), `info body` drawn. Fails without: `middle` read once at mount (the strip keeps `changes`).
  - `draws nothing when the middle's tab is the session's only one`: a session with `claude: false` (only `info` applies): `tabs()`, render with middle `info` in a container: `container` is empty (`toBeEmptyDOMElement()`), and no `aside` or `navigation`. Fails without: the `undefined` branch returning `null` (an empty column is drawn).
  - `draws the strip above the scroller and outside it, with no 44px minimum`: the strip precedes `[data-slot="inspector-scroller"]` (`compareDocumentPosition` has `DOCUMENT_POSITION_FOLLOWING`), the scroller does not contain the strip, and the `inspector` slot holds no element whose class contains `min-h-[44px]` or `min-w-[44px]`. Fails without: the strip drawn inside the scroller (it scrolls away), or the phone's touch size on the buttons.
  - `a panel that throws is replaced in the inspector alone, and the strip stays`: `vi.spyOn(console, "error").mockImplementation(() => {})` (restored in `finally`); `tabs({ Body: () => { throw new Error("body boom"); } })`; render `<><p>middle</p><Inspector session={claude()} middle="turns" /></>`: `middle` is drawn, `body boom` is found inside the `inspector` slot, the strip's buttons are still `changes` and `info`; click `info`: `info body` is drawn and `body boom` is gone. Fails without: the boundary in `PanelHost` (the error reaches the render and the test throws), or the strip drawn inside what the error replaces (the buttons are gone).
  - `a panel whose title throws is caught by the inspector's own boundary, and another tab recovers`: the same with `tabs({ title: () => { throw new Error("title boom"); } })` (the title is read by `PanelHost` above its own boundary): `middle` is drawn, an alert reading `The inspector failed to draw.` holds `title boom`, the strip stays; click `info`: `info body` is drawn and the alert is gone. Fails without: the `Boundary` in `Inspector` (the throw reaches the test), or its `key` (`resetKey`) not changing with the panel (the alert stays after the click).
  - `starts again for another session`: `tabs({ title: (p: PanelProps) => { if (p.sessionId === 7) throw new Error("title boom"); return "Changes"; } })`; render for session 7: the alert is drawn; `rerender` with `claude(8)`: the alert is gone and `changes body` is drawn. Fails without: `resetKey` over the props in the boundary's `key` (the alert stays for another session).
  - `gives the panel the inspector's pane and never reads the window's scroll`: `scrollY` replaced on `window` by a getter counting its reads (restored in `finally`, as `DesktopShell.test.tsx`'s "gives the screen the scroller's pane" does, with `Object.getOwnPropertyDescriptor` and `Reflect.deleteProperty`); `tabs({ Body: Probe })` where `Probe` reads `useScrollPane()`, draws `probe pane` or `probe window` by `pane === windowPane`, and subscribes `onScroll` (a `vi.fn()`) with `useEffect`; render: `probe pane` is drawn, the getter was never read; `fireEvent.scroll(window)` does not call `onScroll`; `fireEvent.scroll` on `[data-slot="inspector-scroller"]` calls it once. Fails without: `ScrollPaneProvider` (the probe reads `window`), the `pane === null` gate (the probe's first render is `windowPane` and reads `scrollY`), or `elementPane` over another element (the scroll on the scroller does not reach `onScroll`).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/Inspector.test.tsx
```

Expected: the suite fails to load, `Failed to resolve import "./Inspector"`.

- [ ] **Step 3: Implement** `desktop/Inspector.tsx` as the behaviour above says, then format with `pnpm -C internal/web/ui exec prettier --write src/desktop/Inspector.tsx src/desktop/Inspector.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/Inspector.test.tsx
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check
```

Expected: `Inspector.test.tsx` passes with its fourteen tests, `check` passes and `dist:check` prints no difference after the build.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/desktop/Inspector.tsx internal/web/ui/src/desktop/Inspector.test.tsx internal/web/dist
git commit -m "feat(web): the desktop's inspector, a tab strip over a panel host in a pane of its own" -- internal/web/ui/src/desktop/Inspector.tsx internal/web/ui/src/desktop/Inspector.test.tsx internal/web/dist
```

### Task 10.3: `DesktopShell` draws the inspector, at `desk3` on a session route

**Files:**
- Modify: `internal/web/ui/src/desktop/DesktopShell.tsx`
- Modify: `internal/web/ui/src/desktop/DesktopShell.test.tsx`
- Modify: `internal/web/dist/` (rebuilt; the `DesktopShell` chunk changes)

**Behaviour.** In `desktop/DesktopShell.tsx` as Stories 6, 8 and 9 left it, three edits, each anchored on the exact lines below.

1. The import `import { PanelHead } from "@/app/shell";` becomes `import { PanelHead, tabOf } from "@/app/shell";`, and `import { useLayout } from "@/app/viewport";` is added on the line after it (the `@/app/…` imports stay in alphabetical order). `import { Inspector } from "./Inspector";` is added immediately before `import { Sidebar } from "./Sidebar";`. (`useSession` is already imported from `@/store/fleet`, and `panelProps` from `@/app/routes`.)
2. In `DesktopShell` (not `Middle`), read what the gate needs above the early return, so the hooks run on every render. The lines

```tsx
  const route = useRoute();
  const sidebar = useDesktop((s) => s.sidebar);
  if (route.panel === "mockupFull") return <Outlet />;
```

read, with three lines added between the second and the third:

```tsx
  const route = useRoute();
  const sidebar = useDesktop((s) => s.sidebar);
  const inspector = useDesktop((s) => s.inspector);
  const layout = useLayout();
  const session = useSession(panelProps(route).sessionId);
  if (route.panel === "mockupFull") return <Outlet />;
```

3. In the row that holds the middle, draw the inspector after it. The line

```tsx
        <Middle />
```

(inside `<div className="flex min-h-0 flex-1">`, the only line of the file that reads exactly so) becomes:

```tsx
        <Middle />
        {layout === "desk3" && inspector && session !== undefined ? (
          <Inspector session={session} middle={tabOf(route.panel, session)} />
        ) : null}
```

`session` is defined only on a session route (a route without an id, the fleet, new, settings and flows, has no `sessionId`), so the inspector is drawn on session routes only; the early return keeps the full mockup chrome-less. `middle` is the tab the route lights (`tabOf`), so a flow's story lights `flow` and a job lights `info` and the inspector never repeats it. The middle's own `PanelHead`, tabs and Strip are unchanged: at `desk2` the tabs reach every view.

Names used: `Inspector` (Task 10.2), `tabOf` (Story 6, `app/shell`), `useLayout` (Story 6, `app/viewport`), `useDesktop` (Story 6), `Sidebar` and `TopBar` (Stories 9 and 8, drawn beside it), the slots `data-slot="desktop"`, `"middle"`, `"sidebar"`, `"top-bar"` and `"inspector"`, and the test helpers `desktopAt`, `live`, `tabbed` and `slot` (Story 6's `DesktopShell.test.tsx`; `live()` loads one Claude session numbered 100, titled `fix-pane-cursor`, the fixture's first) and `setViewportWidth` from `@/test-layout` (Story 6; every test runs at 390 unless it calls it, and `DesktopShell` reads `useLayout` itself, so the inspector's tests call it with `1400`, a `desk3` width, or `1100`, a `desk2` one).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/desktop/DesktopShell.test.tsx`: add `import { act } from "@testing-library/react";` to the Testing Library import if it is not there (and `fireEvent`, already imported by the file's scroll test, stays), `import { setViewportWidth } from "@/test-layout";`, and `readDesktopPrefs` beside `useDesktop` in the `@/app/desktopState` import Story 9 added; add a `describe("DesktopShell's inspector", ...)` after the existing blocks, with `beforeEach(() => useDesktop.setState({ sidebar: true, inspector: true, slots: [] }))`, the same call in an `afterEach`, and a helper `inspectable(over = {})` that defines `turns` (tab order 1, label `conversation`, Head `turns head`, Body none drawn: `() => null`), `changes` (2, `changes`, Body `<p>changes body</p>`), `info` (3, `info`, Body `<p>info body</p>`) and `job` (no tab, Body `() => null`), each `title` its name and `appliesTo` unset, `over` replacing fields of `changes` (for a throwing body). `slot("inspector")` reads `[data-slot="inspector"]`; `strip()` is `within(slot("inspector")!).getByRole("navigation", { name: "Inspector views" })`; `lit()` the name of the button in it with `aria-current="true"`. Tests:
  - `draws the inspector after the middle at desk3, with the next tab drawn`: `setViewportWidth(1400)`, `inspectable()`, `live()`, `desktopAt("#/s/100")`: the `inspector` slot is inside `slot("desktop")`, follows `slot("middle")` (`compareDocumentPosition` has `DOCUMENT_POSITION_FOLLOWING`), holds `changes body`, the strip's buttons are `changes` and `info`, and `slot("sidebar")` and `slot("top-bar")` are drawn too. Fails without: `<Inspector />` in `DesktopShell` (no slot), or drawn before the middle (the order check fails).
  - `draws no inspector at desk2, and draws it when the window grows to desk3`: `setViewportWidth(1100)`, `inspectable()`, `live()`, `desktopAt("#/s/100")`; `await screen.findByText("outlet")`; `slot("inspector")` is null and `slot("middle")` is drawn; `setViewportWidth(1400)` (which wraps its own `act`) draws it; `setViewportWidth(1279)` removes it again. Fails without: the `layout === "desk3"` term of the gate (drawn at 1100 and 1279), or `useLayout` read once (it does not follow the resize).
  - `draws no inspector off session routes`: `it.each` over `["#/settings", "settings"]`, `["#/", "fleet"]`, `["#/s/999", "turns"]` (a session the fleet does not hold): `setViewportWidth(1400)`, `inspectable()`, `definePanel` for the route's panel when it is `settings` or `fleet` (`Body: () => null`), `live()`, `desktopAt(hash)`: `slot("middle")` is drawn and `slot("inspector")` is null. Fails without: the `session !== undefined` term of the gate (an inspector drawn for no session, or a throw on the unknown one).
  - `draws no inspector while the preference hides it, and draws it again when shown`: `useDesktop.setState({ inspector: false })`, `setViewportWidth(1400)`, `inspectable()`, `live()`, `desktopAt("#/s/100")`: `slot("inspector")` is null and the middle is drawn; `act(() => useDesktop.getState().setInspector(true))` draws it without a reload. Fails without: the `inspector` term of the gate or the `useDesktop` read.
  - `draws no inspector on the full mockup`: `setViewportWidth(1400)`, `inspectable()`, `live()`, `desktopAt("#/s/100/mockup/full")`, `await screen.findByText("outlet")`: `slot("inspector")` is null. Fails without: the early `return <Outlet />` for `mockupFull` (an inspector drawn above it would exist).
  - `never repeats the tab the route lights`: `setViewportWidth(1400)`, `inspectable()`, `live()`; `desktopAt("#/s/100/info")`: the strip's buttons are `conversation` and `changes`, `conversation` lit; unmount and `desktopAt("#/s/100/job/5")` (the job panel lights `info` through `tabOf`): the strip's buttons are again `conversation` and `changes`. Fails without: `middle` passed to the inspector (`info` is listed), or `route.panel` passed instead of `tabOf(...)` (the second case lists `info`, since `job` is not a tab).
  - `draws the tab the first slot of loom.desktop names`: `setViewportWidth(1400)`, `inspectable()`, `live()`, `useDesktop.setState(readDesktopPrefs('{"sidebar":true,"inspector":true,"slots":[{"tab":"info"}]}'))` (as a reload reads it), `desktopAt("#/s/100")`: `info` is lit and `info body` drawn. Fails without: the inspector reading the first slot (`changes` is lit).
  - `a throwing inspector panel leaves the middle, the sidebar and the top bar drawn`: `vi.spyOn(console, "error").mockImplementation(() => {})` (restored in `finally`), `setViewportWidth(1400)`, `inspectable({ Body: () => { throw new Error("body boom"); } })`, `live()`, `desktopAt("#/s/100")`: `await screen.findByText("outlet")` (the middle), `body boom` is found inside the `inspector` slot and not outside it, and `slot("sidebar")`, `slot("top-bar")` and the middle's `Session views` navigation are drawn. Fails without: the inspector's boundaries (the error unmounts the shell: the middle's `outlet` is not found).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/DesktopShell.test.tsx
```

Expected: the tests that expect an inspector fail (`inspector` slot not found: the first, the resize, the reload and the route-lit ones, and the throwing one, whose `body boom` is not found); the tests that expect none, and every earlier test, pass.

- [ ] **Step 3: Implement** the edits above, then format with `pnpm -C internal/web/ui exec prettier --write src/desktop/DesktopShell.tsx src/desktop/DesktopShell.test.tsx`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/desktop/DesktopShell.test.tsx src/desktop/Inspector.test.tsx src/desktop/inspector.test.ts src/app/shell.test.tsx
pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/
```

Expected: the four suites pass, `check` passes, `dist:check` prints no difference after the build, and `ok  github.com/aidan-bailey/loom/internal/web` (`DesktopShell` is still a chunk of its own, the inspector inside it, and `index.html` names none of its modules).

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the desktop frame draws the inspector at desk3, on session routes" -- internal/web/ui/src/desktop/DesktopShell.tsx internal/web/ui/src/desktop/DesktopShell.test.tsx internal/web/dist
```

## Story 11: webshot at desktop sizes

- **Developer:** intermediate-dev
- **Files:** `tools/webshot/frame.go`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `.github/workflows/web.yml`, `tools/CLAUDE.md`
- **Interfaces:** exposes the `desktop` and `desktop-narrow` devices; uses the frame Stories 6 to 10 draw
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -out "$d"'`
- **Review:** light (a test tool's device modes and a CI step)
- **Form:** brief (two device entries, a skipped screen and a workflow step)

webshot learns two desktop devices, `desktop` (1440×900) and `desktop-narrow` (1100×800), so the desktop frame Stories 6 to 10 draw can be seen and checked as the phone's is (spec Testing, webshot). A desktop device is a bare viewport: scale 1, not `mobile`, no touch emulation, no safe-area insets, a desktop Chrome user agent, and no bezel, Dynamic Island, home indicator or status bar drawn over the screenshot. `-device` accepts both; every screen of the new app is rendered on them but the phone-only `add-to-home`, with the failure rules webshot already has (a console error, a CSP violation, an uncaught exception, an API route the canned loom cannot answer), so a desktop panel that writes a `<style>` element or reads a route the fixture lacks fails the run here. `.github/workflows/web.yml` renders the new app at the phone and at both desktop devices, and `tools/CLAUDE.md` names the devices. The widths are chosen against the layouts of Story 6: 1100 is `desk2` (no inspector) and 1440 is `desk3` (the inspector drawn), so the two runs show both frames. No code here holds a landmine marker, so the developer runs at the default effort.

### Task 11.1: the desktop devices and the frame's desktop case

**Files:**
- Modify: `tools/webshot/frame.go`
- Test: `tools/webshot/webshot_test.go`

**Behaviour.**

In `tools/webshot/frame.go`, `device` gains a bool field `desktop` (a comment: a desktop browser window, not a phone: a bare viewport that `frame` returns as it is, and that `emulation` does not make a touch screen). `devices` gains two entries beside the iPhones, built by one helper the developer names (private to this story, as `sixOneInchIsland` is for the phones):

| key and `name` | `width`×`height` | `scale` | `desktop` | insets, `corner`, `island`, `homeBar` | `userAgent` |
|---|---|---|---|---|---|
| `desktop` | 1440×900 | 1 | true | all zero | a desktop Chrome on Linux: `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36` |
| `desktop-narrow` | 1100×800 | 1 | true | all zero | the same |

The doc comment on `devices` says what the desktop entries are and why their widths are what they are (1100 is under the inspector's 1280 and over the sidebar's 1024; 1440 is over both; the layouts' thresholds are `DESK2_MIN` and `DESK3_MIN` in `internal/web/ui/src/app/viewport.ts`, which Go cannot import, so `TestDevices_TheDesktopsAreBareViewportsAndThePhonesAreNot` holds the pairing). `defaultDevice` stays `iphone15`.

`frame(shot, bar, d, o)` for a `desktop` device returns a new image holding the screenshot's pixels and nothing else: no bezel (so its bounds are the screenshot's, not padded), no guides, no status bar, no island, no home indicator, whatever `o` and `bar` say. The doc comment of `frame` gains that sentence. The phone path is unchanged.

Names defined: the `desktop` field and the two `devices` keys (the contract's). Names used: `device`, `devices`, `frame`, `frameOpts` (`tools/webshot/frame.go`).

- [ ] **Step 1: Write the failing tests**

In `tools/webshot/webshot_test.go`, after `TestFrame_LaysTheStatusBarOverTheScreen`, add:

- `TestDevices_TheDesktopsAreBareViewportsAndThePhonesAreNot`: for `desktop` it asserts width 1440, height 900, scale 1, `desktop` true, `insetTop`, `insetBottom` and `corner` zero, `island` and `homeBar` the zero `rectPt`, a user agent that contains `Chrome/` and `X11; Linux x86_64` and contains neither `Mobile` nor `iPhone`; for `desktop-narrow` the same with 1100×800. It asserts the layout each width lands in, with a comment that the thresholds are `DESK2_MIN` 1024 and `DESK3_MIN` 1280 of `app/viewport.ts`: `desktop-narrow`'s width is at least 1024 and under 1280 (the sidebar without the inspector), `desktop`'s at least 1280 (with it). For `iphone15` and `iphone16` it asserts `desktop` false and scale 3 (the phones stay phones). Fails without: the two `devices` entries (the lookups are not found and the test stops at `require.True(t, ok)`), `desktop: true` on an entry, a width moved into another layout (`desktop-narrow` at 1280 would draw the inspector the run is meant to omit), or a touch or `Mobile` user agent left on a desktop one.
- `TestFrame_ADesktopScreenshotComesBackUnchanged`: a 1440×900 opaque mid-grey `image.NRGBA` as the screenshot, with one pixel at (7, 9) made distinct; a `bar` of the same size, transparent but for a red pixel at (100, 100); `frame(shot, bar, devices["desktop"], frameOpts{bezel: true, guides: true})` returns an image whose bounds equal the screenshot's and whose `Pix` equals the screenshot's byte for byte (so the red pixel, the island's black at the top centre and the bezel's transparent corner are all absent), and the input is not the returned image (a pixel set on the result does not show in the screenshot). Fails without: the desktop case in `frame` (the bezel pads the bounds by `bezelPt` points, the island's black lands on the top centre, the bar's pixel is laid over).

- [ ] **Step 2: Run it to see it fail**

Run `CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestDevices_TheDesktopsAreBareViewportsAndThePhonesAreNot|TestFrame_ADesktopScreenshotComesBackUnchanged'`. It fails to build with `d.desktop undefined (type device has no field or method desktop)`, so no test runs.

- [ ] **Step 3: Implement**

Add the `desktop` field, the helper and the two entries, and the desktop case at the top of `frame`, as above. The `desktop` case copies the screenshot into a fresh `*image.NRGBA` (the function's result type) the way the phone path converts each pixel, rather than returning `shot` itself.

- [ ] **Step 4: Run it to see it pass**

Run `CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestDevices_|TestFrame_'`. It prints `ok  	github.com/aidan-bailey/loom/tools/webshot`, the phone's `TestFrame_DrawsTheDeviceOverTheViewport` and `TestFrame_LaysTheStatusBarOverTheScreen` still passing.

- [ ] **Step 5: Commit**

`git commit -m "feat(webshot): the desktop and desktop-narrow devices, bare viewports the frame leaves alone" -- tools/webshot/frame.go tools/webshot/webshot_test.go`

### Task 11.2: a desktop emulation, the skipped screen, and `run`

**Files:**
- Modify: `tools/webshot/main.go`
- Test: `tools/webshot/webshot_test.go`

**Behaviour.**

In `tools/webshot/main.go`:

- `emulation(d, standalone)` for a `desktop` device sends the steps the phone's does but: `Emulation.setDeviceMetricsOverride` with `mobile` false, the device's `width`, `height` and `deviceScaleFactor` (1), `screenWidth` and `screenHeight` the same, and no `screenOrientation`; no `Emulation.setSafeAreaInsetsOverride` step; no `Emulation.setTouchEmulationEnabled` step; `Emulation.setUserAgentOverride` with the device's `userAgent` and `platform` `"Linux x86_64"`. Every other step (`Page.enable`, `Runtime.enable`, `Log.enable`, `Network.enable`, the service-worker bypass, `Emulation.setScrollbarsHidden`, and the `navigator.standalone` script, which the app's install gate reads only on an iOS user agent) is as for a phone. The doc comment of `emulation` says a desktop device is no touch screen and has no insets. The step list for a phone is unchanged, in order and in content.
- `forDevice(d device, todo []screen) []screen`: `todo` as it is for a phone; for a `desktop` device, `todo` without the screens whose `modeOf` is `browserTab` (today `add-to-home`, the iOS browser tab's Add to Home gate, which draws only for an iOS user agent), the order kept and `todo` not changed. A comment says why `modeOf`'s `browserTab` and not the screen's name is what is tested.
- `deviceNames() string`: the keys of `devices`, sorted and joined with `", "`, which the `-device` flag's help and the unknown-device error both use.
- `run`: the `-device` help reads `the device to render as: ` followed by `deviceNames()`; an unknown device is `no device %q (have <deviceNames()>)`; right after `pickScreens` it applies `forDevice(d, todo)`, prints one line `<name>\tskipped: phone only` on stdout for each screen it dropped, and returns the error `no screen to render on <device name>` when nothing is left (before it looks for a browser, so the check needs none); it draws no status bar for a desktop device (`pg.statusBar` is called only when `!*bare && !d.desktop`, so `bar` stays nil); `-bare` and `-guides` are accepted and change nothing on a desktop device, as `frame` ignores them. The package's doc comment gains a paragraph with `go run ./tools/webshot -app next -device desktop -out /tmp/shots` and `-device desktop-narrow`, saying they are bare browser windows at 1440×900 and 1100×800 and skip `add-to-home`.

Names defined: `forDevice` and `deviceNames` (this story's own, private). Names used: `device`, `devices`, `screen`, `modeOf`, `screenMode`, `emulation`, `pickScreens`, `run` (`tools/webshot/main.go`).

- [ ] **Step 1: Write the failing tests**

In `tools/webshot/webshot_test.go`, after `TestEmulation_ABrowserTabIsNotStandalone`, add (each by name, with what it asserts):

- `TestEmulation_ADesktopIsNotMobileAndHasNoTouchNoInsets`: for `desktop` and `desktop-narrow`, each with `standalone` true and false, it collects the `method` of every step of `emulation(d, standalone)` and asserts that `Emulation.setSafeAreaInsetsOverride` and `Emulation.setTouchEmulationEnabled` are not among them, that `Emulation.setDeviceMetricsOverride`'s params (a `map[string]any`) carry `"mobile": false`, `"width"` and `"height"` the device's, `"deviceScaleFactor"` 1.0 and no `"screenOrientation"` key, and that `Emulation.setUserAgentOverride`'s carry the device's `userAgent` and a `"platform"` that is not `"iPhone"`. For `iphone15` it asserts the other side: both of those steps are present and `"mobile"` is true. Fails without: the desktop branch of `emulation` (the phone's steps put a touch screen and 59-point insets on a desktop window, and the frame would draw the phone's layout under a `mobile` viewport), or a desktop branch that drops the phone's steps too.
- `TestForDevice_DropsOnlyAddToHomeOnADesktop`: over the full `screens`, `forDevice(devices["desktop"], screens)` has every screen but `add-to-home`, in `screens`' order (so `len(screens)-1` entries, `pair` among them), and `screens` itself still holds `add-to-home` afterwards; the same for `desktop-narrow`; `forDevice(devices[defaultDevice], screens)` equals `screens`; `forDevice(devices["desktop"], []screen{{"add-to-home", ""}})` is empty. Fails without: the filter (the desktop list keeps `add-to-home`), the `desktop` test (it also drops the phone's), or a filter that drops more than the browser-tab screen (`pair`, which a desktop renders unpaired, would go too).
- `TestRun_AnUnknownDeviceNamesTheOnesItHas`: `run([]string{"-out", t.TempDir(), "-device", "ipad"})` errors with a message that contains `ipad`, `desktop`, `desktop-narrow`, `iphone15` and `iphone16`. Fails without: `deviceNames()` in the error (it names only the device asked for).
- `TestRun_AddToHomeAloneOnADesktopRendersNothing`: `run([]string{"-out", t.TempDir(), "-device", "desktop", "-screens", "add-to-home", "-browser", "no-such-browser-binary"})` errors with a message that contains `no screen to render on desktop`, and not the browser lookup's (the error does not contain `no-such-browser-binary`), which proves the check runs before a browser is looked for. Fails without: the empty-list refusal (the run goes on to the browser lookup and fails there, or renders nothing and exits 0).
- `TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser`, beside `TestRun_RendersEveryScreenInARealBrowser`: skipped unless `LOOM_WEBSHOT_BROWSER` is set, as it is, and built the same way for the browser argument; for each of `desktop` and `desktop-narrow` it runs `run` with `-app next`, `-device <name>`, `-out` a `t.TempDir()`, `-settle 800ms` and the browser arguments, requires no error (every screen of the new app draws at that size with no console error, CSP violation or route the fixture lacks), then opens `fleet.png` in the output directory with `image/png` and asserts its size is exactly the device's (1440×900, 1100×800: a bare viewport at scale 1, with no bezel padding) and that `add-to-home.png` does not exist. Fails without: the bare desktop frame (the PNG is bezel-padded), the skipped screen (`add-to-home.png` exists), or a desktop panel that breaks the run (a console error, a `<style>` element, an unanswered route).

- [ ] **Step 2: Run it to see it fail**

Run `CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestEmulation_|TestForDevice_|TestRun_AnUnknownDevice|TestRun_AddToHomeAlone'`. It fails to build with `undefined: forDevice`, so no test runs.

- [ ] **Step 3: Implement**

Add the desktop branch of `emulation`, `forDevice` and `deviceNames`, and wire `run` as above: the flag's help, the unknown-device error, the `forDevice` call with its skipped lines and its refusal placed right after `pickScreens` and before `findBrowser`, and `pg.statusBar` only for a non-desktop device. Extend the package's doc comment.

- [ ] **Step 4: Run it to see it pass**

Run `CGO_ENABLED=0 go test ./tools/webshot/`. It prints `ok  	github.com/aidan-bailey/loom/tools/webshot`; the real-browser tests report `SKIP` without `LOOM_WEBSHOT_BROWSER`. With a browser, run `LOOM_WEBSHOT_BROWSER=1 CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestRun_Renders'` through `loom job submit`; both real-browser tests pass.

- [ ] **Step 5: Commit**

`git commit -m "feat(webshot): render the new app at the desktop devices, skipping add-to-home" -- tools/webshot/main.go tools/webshot/webshot_test.go`

### Task 11.3: the workflow's desktop renders and the tools rule

**Files:**
- Modify: `.github/workflows/web.yml`
- Modify: `tools/CLAUDE.md`

**Behaviour.**

`.github/workflows/web.yml`: the last step, `Render every route`, whose run line is `go run ./tools/webshot -app next -out "$RUNNER_TEMP/shots"`, renders the new app at three devices instead of one: the phone (the default `iphone15`), `desktop` and `desktop-narrow`, each into a directory of its own under `$RUNNER_TEMP`, and the step fails when any of them does. The step keeps its `env` (`CGO_ENABLED: 0`), becomes `Render every route at the phone and both desktop sizes`, and its comment above it (which says webshot exits 1 on a console error, a CSP violation, an uncaught exception or an unanswered route) gains that the run covers the phone and both desktop devices. The run is one shell loop over the three device names with `-device "$device"` and `-out "$RUNNER_TEMP/shots-$device"`; GitHub's bash runs with `-e`, so a failing render fails the step. The header comment's "every route rendered under the layer's CSP" stays true and is left as it is. No new trigger: the `paths` lists already hold `tools/webshot/**` and `internal/web/ui/**`.

`tools/CLAUDE.md`:

- In the first paragraph, after `` `-device iphone16` is the same screen ``, add that `-device desktop` (1440×900) and `-device desktop-narrow` (1100×800) are bare desktop browser windows instead. Keep the line under the 1,200-byte limit.
- One new rule after the rule beginning `**Render the new app only through`, in the file's shape (what; why in one clause; what breaks and how silently; Enforced or Convention): keep the desktop devices bare viewports, scale 1, not `mobile`, no touch, no insets, a desktop Chrome user agent, nothing drawn over the screenshot, and `add-to-home` skipped on them, because a desktop render under a phone's emulation draws the phone's frame and passes every check (the app's frame follows the window's width, and a `mobile` viewport or a touch screen changes what it draws); and that the widths keep `desktop-narrow` in the layout with no inspector (under 1280) and `desktop` with it (1280 and over). It names as **Enforced** `TestDevices_TheDesktopsAreBareViewportsAndThePhonesAreNot`, `TestFrame_ADesktopScreenshotComesBackUnchanged`, `TestEmulation_ADesktopIsNotMobileAndHasNoTouchNoInsets` and `TestForDevice_DropsOnlyAddToHomeOnADesktop` (`tools/webshot/webshot_test.go`), and says the real-browser render is `TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser`, run only with `LOOM_WEBSHOT_BROWSER` set. The rule's line stays under 1,200 bytes, and the file under the package budget (100 lines, 20,000 bytes; it is 26 lines and 9,329 bytes before).

Names used: the test names of Tasks 11.1 and 11.2, which the lint checks exist in `tools/webshot/webshot_test.go`.

- [ ] **Step 1: Write the rule and the workflow edit**

This task has no test of its own: its check is the CLAUDE.md lint, which fails on a test name it cannot find in the file a rule names and on a line over its byte limit. Write the rule and the workflow edit as in Step 3 first.

- [ ] **Step 2: Run the lint**

Run `CGO_ENABLED=0 go run ./tools/claudemd`; it prints no problem and exits 0. A misspelt test name or a line over 1,200 bytes is listed as a problem and exits 1, which is the failure to correct.

- [ ] **Step 3: Implement**

Edit `.github/workflows/web.yml` and `tools/CLAUDE.md` as above. In the workflow, only the last step and its comment change; no other step, no `paths` entry.

- [ ] **Step 4: Run it to see it pass**

Run `CGO_ENABLED=0 go test ./tools/claudemd/ ./tools/webshot/` (it prints `ok` for both) and `CGO_ENABLED=0 go run ./tools/claudemd -v` to see the budget's headroom. Then, with a browser on PATH, `bash -c 'd=$(mktemp -d) && for device in iphone15 desktop desktop-narrow; do CGO_ENABLED=0 go run ./tools/webshot -app next -device "$device" -out "$d/$device" || exit 1; done'` from the repo root, through `loom job submit`, each device printing one line per screen and no `!` line.

- [ ] **Step 5: Commit**

`git commit -m "ci(web): render the new app at the phone and both desktop sizes, and name the desktop devices in tools" -- .github/workflows/web.yml tools/CLAUDE.md`

## Story 12: The rules and the procedures for the desktop

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md`, `internal/web/README.md`
- **Interfaces:** none
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd'`
- **Review:** light (docs)
- **Form:** brief (docs)

Part 1's code is in; this story writes down what a later change must keep (spec §27) and how to work on it. `internal/web/ui/CLAUDE.md` gains four rules: a panel scrolls through the `ScrollPane` and never the window; a Body reads its session from its props, never the route; the desktop chrome is not a panel and is drawn dense; `DesktopShell` stays a lazy chunk and the one confirmation sheet and toaster sit above both frames. `docs/claude/web-layer.md` gains how the desktop composes the panels, the dev loop at desktop widths (webshot's two devices and the Vitest viewport), the desktop checklist, and a corrected origin trap (the set, `loom.localhost`, since Story 1). `USAGE.md` gains a paragraph on the desktop layout, and `internal/web/README.md` learns the new app's cookie writer, the lazy `DesktopShell` chunk and the desktop layout. This story is brief-form: it writes no code, and the rule texts below are what the developer copies, since the CLAUDE.md lint reads their shape. Each rule names its guard as **Enforced** by a test that Stories 4 to 10 write, or says **Convention** with the silent wrong result. No code here holds a landmine marker, so the developer runs at the default effort.

The part's other docs are not here: Story 1 changed `internal/web/CLAUDE.md`, `internal/web/README.md`'s "Every request" section (which Task 12.4 leaves alone) and `core/README.md`, Story 2 `USAGE.md`'s web section and the cli README, Story 11 `tools/CLAUDE.md` and the workflow. Root `CLAUDE.md`, `docs/specs/web-api.md` and `api.fields` do not change (no route changes). The keys, the palette and their rules and checklist items are part 2's.

### Task 12.1: The rules in `internal/web/ui/CLAUDE.md`

**Files:**
- Modify: `internal/web/ui/CLAUDE.md:3` (the opening paragraph), `:12` and `:13` (the lazy lists), and append four rules after line 18

Behaviour. The file is 18 lines and 8,525 bytes before (budget 100 lines, 20,000 bytes), and no line may pass 1,200 bytes. Every rule sits under `## Rules when modifying this app` and carries **Enforced** or **Convention**, which the lint checks.

Names used (all in the contract or in the stories' own tests): `ScrollPane`, `windowPane`, `elementPane`, `useScrollPane`, `NoSession`, `DesktopShell`, `AppShell`, `PhoneShell`, `ConfirmSheet`, `Toaster`, `LinkPill`, `HostLabel`, `GroupCounts`, `FleetLinks`, `WorkspaceFilter`. Test files and cases they cite, which the earlier stories write: `internal/web/ui/src/app/scroll.test.tsx` (Story 4), `internal/web/ui/src/desktop/DesktopShell.test.tsx` (`gives the screen the scroller's pane and never reads the window's scroll`, Story 6), `internal/web/ui/src/desktop/Inspector.test.tsx` (`gives the panel the inspector's pane and never reads the window's scroll`, `draws the chosen panel's Body with the session's props`, Story 10), `internal/web/ui/src/session/session.test.tsx` (`NoSession reads no route`, Story 4), `internal/web/ui/src/app/shell.test.tsx` (`keeps an open confirmation across a swap, redrawn as a dialog, and it still answers`, `keeps a toast showing across a swap, at the top on the phone and the bottom right on desktop`, Story 6), `internal/web/ui/src/desktop/TopBar.test.tsx` (Story 8) and `internal/web/ui/src/desktop/Sidebar.test.tsx` (Story 9) for the density, and `TestDist_LazyViewsAreChunksOfTheirOwn` and `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne` (Story 6, `internal/web/static_budget_test.go`).

- [ ] **Step 1: Write the rules**

In line 3, replace

```
The web layer's new phone app: its source is here
```

with

```
The web layer's new app, for the phone and, from 1024px wide, the desktop: its source is here
```

(the rest of the line is unchanged). In line 12 (the rule beginning `Keep the first load within`), replace

```
(the flow views, job, mockup, new session, settings)
```

with

```
(the flow views, job, mockup, new session, settings, the desktop shell)
```

In line 13 (the rule beginning `Make a view the first load does not need a panel`), replace

```
`NewSessionBody` and `SettingsBody`)
```

with

```
`NewSessionBody`, `SettingsBody` and `DesktopShell`, the desktop shell: a lazy frame, not a panel's Body, so the phone's first load is unchanged)
```

(line 13 stays under 1,200 bytes: it is 1,003 now and grows by about 115). Then, after the last rule (the one beginning `Give a truncate element block`), append these four lines, each a single line in the file (the lint reads every line of `## Rules when modifying this app` that starts with `- `):

```
- **Scroll a panel only through `useScrollPane()` (`internal/web/ui/src/app/scroll.tsx`), never through `window`; a desktop pane keeps its pinned head outside the scroller (`elementPane(scroller, null)`) and draws its Body only once the scroller has mounted.** The pane is the window on the phone (`windowPane`) and the middle's or the inspector's own element on desktop, where the page does not scroll: a panel that reads `window.scrollY` stops following the conversation's newest turn, never reaches a job log's end and lands a flow document's jump nowhere, and a Body drawn before its scroller exists keeps the window's pane. **Enforced** for the pane by `internal/web/ui/src/app/scroll.test.tsx`, for the three panels that scroll by `turns.test.tsx`, `job.test.tsx` and `doc.test.tsx` under `internal/web/ui/src/panels/`, and for each desktop pane by the cases `gives the screen the scroller's pane and never reads the window's scroll` (`DesktopShell.test.tsx`) and `gives the panel the inspector's pane and never reads the window's scroll` (`Inspector.test.tsx`, both in `internal/web/ui/src/desktop/`); another panel reading the window is **Convention** — a view stuck on desktop.
- **Read a session in a panel's Body only from its props (`PanelProps.sessionId`, `params`), never from the route (`useRoute`, `panelProps(route)`); a Head or a Strip may read the route.** The inspector draws a tab's Body through `PanelHost` with `{ sessionId, params: { id }, search: {} }` beside a middle that shows another route, so a Body that reads the route draws the middle's session in the inspector; only the middle draws a Head or a Strip, and `NoSession` takes `sessionId` as a prop for this. **Enforced** for `NoSession` by the case `NoSession reads no route` in `internal/web/ui/src/session/session.test.tsx` and for the props the inspector hands a Body by the case `draws the chosen panel's Body with the session's props` in `internal/web/ui/src/desktop/Inspector.test.tsx`; another Body reading the route is **Convention** — an inspector tab that shows the wrong session with no error.
- **Draw the desktop's top bar, sidebar and inspector from `internal/web/ui/src/desktop/` as chrome, never as a panel (`definePanel`), and denser than a panel: no 44px minimum.** A panel gets a URL, a tab and a Head, so chrome registered as one would answer a route and sit among a session's tabs; the 44px targets and 16px form text stay in the panels, which the phone also draws. Chrome reuses the shared pieces (`LinkPill`, `HostLabel` and `GroupCounts` in `internal/web/ui/src/app/status.tsx`, `FleetLinks` in `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `WorkspaceFilter`) instead of a copy, so the phone's footer and the top bar cannot drift. **Enforced** for the density by `internal/web/ui/src/desktop/TopBar.test.tsx`, `internal/web/ui/src/desktop/Sidebar.test.tsx` and `internal/web/ui/src/desktop/Inspector.test.tsx`; that chrome is not a panel is **Convention** — a sidebar that is reachable at a URL and listed among a session's tabs.
- **Keep `DesktopShell` (`internal/web/ui/src/desktop/DesktopShell.tsx`) a `React.lazy` module of its own that `AppShell` draws only at `desk2` and wider, and mount the one `ConfirmSheet` and the one `Toaster` in `AppShell` above both frames, never inside `PhoneShell` or `DesktopShell`.** A static import puts the desktop code in the entry script and slows every phone launch; a sheet or toaster inside a frame unmounts when the window crosses 1024px, so an open confirmation's command waits for an answer nobody can give and a toast is lost. **Enforced** for the chunk by `TestDist_LazyViewsAreChunksOfTheirOwn` and `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne` (`internal/web/static_budget_test.go`, `DesktopShell` in `checkLazyViews`' list), and for the sheet and toaster by the cases `keeps an open confirmation across a swap, redrawn as a dialog, and it still answers` and `keeps a toast showing across a swap, at the top on the phone and the bottom right on desktop` in `internal/web/ui/src/app/shell.test.tsx`.
```

- [ ] **Step 2: Run the lint**

Run `CGO_ENABLED=0 go run ./tools/claudemd`: it prints no problem and exits 0. It fails for a rule without **Enforced** or **Convention**, a path or test name that does not resolve (a misspelt test file or case's file), or a line over 1,200 bytes; that is the failure to correct. `CGO_ENABLED=0 go run ./tools/claudemd -v` lists `internal/web/ui/CLAUDE.md` at about 12,700 of 20,000 bytes and 22 of 100 lines.

- [ ] **Step 3: Commit**

`git commit -m "docs(web): the desktop's rules in the app's CLAUDE.md" -- internal/web/ui/CLAUDE.md`

### Task 12.2: The procedures in `docs/claude/web-layer.md`

**Files:**
- Modify: `docs/claude/web-layer.md` (four places: a new section before `## Adding a command`, the dev loop's first paragraph, the classic webshot bullet, the origin trap; a new section before `## Traps`)

Behaviour. Every edit anchors on text that is in the file today.

Names used: `useLayout`, `Layout` (`phone`, `desk2`, `desk3`), `AppShell`, `PhoneShell`, `DesktopShell`, `PanelHead`, `TopBar`, `Sidebar`, `Inspector`, `inspectorTab`, `useDesktop`, `DESKTOP_KEY`, `elementPane`, `setViewportWidth` (`internal/web/ui/src/test-layout.ts`, Story 6), `core.LocalOrigin` (Story 1), webshot's `desktop` and `desktop-narrow` (Story 11). `PanelHead` is exported from `internal/web/ui/src/app/shell.tsx` by Story 6; it is a name of Story 6's own, read from its draft.

- [ ] **Step 1: Write the edits**

1. **The dev loop.** In `## The dev loop`, after the sentence `` `go run ./tools/webshot -app next -out <dir outside the worktree>` renders the built app under the real policy. `` insert (before `Node 22 and pnpm come ...`):

```
`-device desktop` (1440×900, the `desk3` layout, inspector drawn) and `-device desktop-narrow` (1100×800, `desk2`, no inspector) render it at the two desktop sizes: bare viewports with a desktop Chrome user agent, scale 1, no touch, no bezel, no status bar, and not the phone-only `add-to-home`; `.github/workflows/web.yml` renders the phone and both. `pnpm dev` in a desktop browser shows the desktop layout at 1024px wide and up, resizing swaps it live. Vitest runs every test at the phone layout (`internal/web/ui/src/test-setup.ts` sets 390px before each test); a test of the desktop frame calls `setViewportWidth(1100)` or `setViewportWidth(1400)` from `@/test-layout` first, since happy-dom's own 1024px window would read `desk2`.
```

2. **The classic webshot bullet.** In the bullet beginning `- To see a screen without a phone`, after `` `-bare` writes the viewport only. `` insert:

```
The two desktop devices (`-device desktop`, 1440×900, and `-device desktop-narrow`, 1100×800, for `-app next`) are the exception to the iPhone: bare browser windows with no touch, no safe-area insets and no bezel, Dynamic Island, home indicator or status bar, and `add-to-home` is skipped on them.
```

3. **How the desktop composes the panels.** Insert before `## Adding a command` a section `## How the desktop composes the panels` holding these paragraphs (the developer writes them out in the file's voice, one paragraph or bullet each, keeping each fact):
   - From 1024px wide `AppShell` (`internal/web/ui/src/app/shell.tsx`) draws the lazy `DesktopShell` (`internal/web/ui/src/desktop/DesktopShell.tsx`) instead of `PhoneShell`, by `useLayout()` (`internal/web/ui/src/app/viewport.ts`): `phone` under 1024, `desk2` to 1279, `desk3` from 1280, read from `matchMedia`. Pairing and the splash with no snapshot stay in the phone's column at any width; the full mockup draws alone. Routes do not change: the hash names the middle's panel on both frames, and a push link opens the same view.
   - The frame: `TopBar` across the window (the link pill, `loom@host`, the workspace filter, the three group counts and the fleet's links), then `Sidebar` (the fleet's groups, hidden on the fleet route `#/`, where the middle draws the fleet body in full), the middle, and at `desk3` on a session route the `Inspector`.
   - The middle is the phone's frame for the route: `PanelHead` (the panel's Head, the session's tabs, its Strip) pinned above a scroller, and the panel's Body inside it under `ScrollPaneProvider` with `elementPane(scroller, null)`. The inspector draws the session's tab strip minus the tab the middle lights, and the chosen tab's Body through `PanelHost` with `{ sessionId, params: { id }, search: {} }`, in its own error boundary and scroll pane; `inspectorTab` picks the tab, and the choice, with the sidebar's and inspector's visibility, is kept in localStorage `loom.desktop` (`useDesktop`, `internal/web/ui/src/app/desktopState.ts`), anything unreadable being the default.
   - A panel is written once: registering it with a `tab` gives it a middle on the phone and on desktop and a place among the inspector's tabs, which is why its Body takes its session from its props and scrolls through the pane (the rules in `internal/web/ui/CLAUDE.md`). The top bar, the sidebar and the inspector's strip are chrome, not panels. `askConfirm` draws a Drawer on the phone and a centred Dialog on desktop and the `Toaster` sits top-centre or bottom-right, both mounted once by `AppShell` above the two frames.

4. **The origin trap.** Replace the whole bullet

```
- **Every request is checked against the configured origin.** The `Host` must equal the origin's host and port (421 otherwise), and a request other than GET or HEAD must carry an `Origin` equal to it (403). A test that builds its own request sets both.
```

with

```
- **Every request is checked against the origin set.** The set is the configured origin plus, for a listener bound to `127.0.0.1` or `[::1]`, its local origin `http://loom.localhost:<port>` (`core.LocalOrigin`; `loom web open` opens it). The `Host` must be a member's host and port (421 otherwise), and a request other than GET or HEAD must carry exactly one `Origin` equal to a member (403). Bare `localhost` and the IP literal are never members, since a device cookie set there would reach every server the browser reaches there on any port, and a listener on another loopback address has no local origin. Each origin holds its own device cookie, so a browser on the local origin and one on the tunnel are two devices. The `loom.localhost` cookie is host-only, but cookies ignore the port: a hostile listener on another loopback port (another OS user's server, a forward to a compromised machine, a container's published port) that steers the browser to `loom.localhost:<its port>` receives it, and the pairing code `loom web open` mints is its opener's argument, readable from the process list while the opener or a freshly started browser runs. Both are accepted on a single-user machine. A test that builds its own request sets both headers.
```

5. **The desktop checklist.** Insert before `## Traps` a section `## The desktop checklist after each part` with this lead and list (the developer writes the lead in the file's voice):
   - Lead: the checklist is the owner's, on the daemon's machine, after each part of the desktop merges and the daemon is rebuilt; a dev build for it runs through the `loom-dev` skill, never `./loom` or `loom serve stop` in a loom pane.
   - 1. `loom web open` in Chrome pairs and opens the fleet at `http://loom.localhost:<port>`, the address bar showing no code. 2. A second `loom web open` says the name is already paired and opens the plain URL. 3. The frame at 1100px and at 1440px: top bar, sidebar, middle, and the inspector at 1280 and up only; resizing across 1024 and 1280 keeps the route, the reply draft and each session's held scroll; the fleet route has no sidebar; the full mockup draws alone. 4. A confirmation (Kill) draws as a centred dialog on desktop and a drawer on the phone, and toasts sit bottom-right. 5. Install App from Chrome. 6. A waiting push received on the desktop (it needs an `https` configured origin). 7. The tunnel URL from another machine, paired with `loom web pair`; a Safari desktop pairs this way too, since it may not resolve `*.localhost`. 8. From part 2: the keys and the help, and the palette's three kinds.

- [ ] **Step 2: Run the lint**

Run `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd`: `ok`, and no problem printed; a link that does not resolve is the failure to correct.

- [ ] **Step 3: Commit**

`git commit -m "docs(web): how the desktop composes the panels, its dev loop and checklist, and the origin set" -- docs/claude/web-layer.md`

### Task 12.3: The desktop layout in `USAGE.md`

**Files:**
- Modify: `USAGE.md` (the web section's feature list, after the bullet `**The new app.**`)

Behaviour. After the bullet that ends

```
  switch happens the next time the app opens.
```

(the line directly above the blank line and `#### Set it up`), insert a blank line and one more bullet in the same list, wrapped at about 75 columns like its neighbours:

```
- **The desktop layout.** The new app draws a desktop layout whenever its
  window is 1024px wide or more, with nothing to turn on: a top bar across
  the window (the link's pill, `loom@host`, the workspace filter, the counts
  of sessions that need you, wait for your turn and are working, and links
  to new session, flows and settings), a sidebar of the fleet's sessions
  (two lines each: the title and age, then why it waits, its flow's state or
  what it said last), the screen you are on in a pane of its own, and, from
  1280px, an inspector at the right showing another view of the same
  session (its tab strip never repeats the tab on the left, and its choice
  is kept in this browser). The fleet screen has no sidebar, since it is the
  same list. Under 1280px the session's tabs reach every view. A
  confirmation is a centred dialog and notifications sit at the bottom
  right. The layout follows the window as it resizes, and under 1024px it is
  the phone's. To open it on the machine that runs the daemon, use
  `loom web open` (below).
```

Names used: none (prose). The facts it states are Stories 6 to 10's behaviour.

- [ ] **Step 1: Write the bullet** as above.

- [ ] **Step 2: Run the docs gate**

Run `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd`: `ok`, no problem. `USAGE.md` is checked for its links only, so the lint cannot fail on this text; read the bullet against Stories 6 to 10 (the top bar's contents are Story 8's, the sidebar's lines Story 9's, the inspector's rule Story 10's).

- [ ] **Step 3: Commit**

`git commit -m "docs(usage): the desktop layout of the new web app" -- USAGE.md`

### Task 12.4: The desktop in `internal/web/README.md`

**Files:**
- Modify: `internal/web/README.md:89` (the cookie's writers), `:95` and `:103` (the lazy chunks), and a new section before `## Push`

Behaviour. Four edits, each anchored on text in the file today. Do not touch `## Every request`, which Story 1 rewrote for the origin set. The README is not linted for prose (the lint checks its links, paths and test names), so the developer greps each path and test name below before committing.

Names used: `adoptNextApp` (`internal/web/ui/src/app/pairHold.ts`, Story 3), `DesktopShell` (`internal/web/ui/src/desktop/DesktopShell.tsx`, Story 6), `useLayout` (`internal/web/ui/src/app/viewport.ts`, Story 6), `TestDist_LazyViewsAreChunksOfTheirOwn` (`internal/web/static_budget_test.go`).

- [ ] **Step 1: Write the edits**

1. **The cookie's writers** (line 89, "The new app and the switch"). Replace

```
set it and register the worker again at `/sw.js?ui=<app>`; each app registers its worker by the cookie at every launch
```

with

```
set it and register the worker again at `/sw.js?ui=<app>`; a browser that arrives at the new app by `?ui=next` with no cookie, as `loom web open`'s link does, has `adoptNextApp` (`internal/web/ui/src/app/pairHold.ts`) set it before its worker registers; each app registers its worker by the cookie at every launch
```

2. **The lazy chunks** (line 95, "The new app's session views"). Replace

```
The job and mockup views' bodies are chunks loaded on demand, apart from the entry (`TestDist_LazyViewsAreChunksOfTheirOwn`)
```

with

```
The job and mockup views' bodies and the desktop shell (`DesktopShell`) are chunks loaded on demand, apart from the entry (`TestDist_LazyViewsAreChunksOfTheirOwn`)
```

and, in line 103 ("The new app's other screens"), replace

```
are chunks loaded on demand beside the job and mockup views (`TestDist_LazyViewsAreChunksOfTheirOwn`), and pairing stays in the entry.
```

with

```
are chunks loaded on demand beside the job and mockup views and the desktop shell (`TestDist_LazyViewsAreChunksOfTheirOwn`), and pairing stays in the entry.
```

3. **The desktop layout.** Insert before the heading `## Push` (after the paragraph ending `` `add-to-home` as a browser tab. ``) a section:

```
## The desktop layout

One app draws two frames. `AppShell` (`internal/web/ui/src/app/shell.tsx`) reads the window's width through `useLayout` (`internal/web/ui/src/app/viewport.ts`, from `matchMedia`) and draws the phone's frame under 1024px and, from 1024px, the lazy `DesktopShell` (`internal/web/ui/src/desktop/DesktopShell.tsx`), so the phone's first load does not grow. The routes are the same on both: the hash names the middle's panel. The desktop frame is a top bar across the window (the link's pill, `loom@host`, the workspace filter, the three group counts and the fleet's links), a sidebar of the fleet's sessions (hidden on the fleet route, whose middle draws the fleet in full), the middle, which is the phone's frame for the route (the panel's head, the session's tabs and strip pinned above a scroller that holds its body), and, from 1280px on a session route, an inspector that draws another of the session's tabs. Pairing, the splash drawn before a snapshot and the full mockup keep the phone's single column or the whole window at any width. The one confirmation sheet and the one toaster are mounted by `AppShell` above both frames. The rules for changing it are in `internal/web/ui/CLAUDE.md`.
```

- [ ] **Step 2: Check the paths**

Run `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd`: `ok`, no problem printed. Run `grep -c "DesktopShell" internal/web/README.md`: it prints `3`.

- [ ] **Step 3: Commit**

`git commit -m "docs(web): the new app's cookie writer, the desktop shell chunk and the desktop layout in the README" -- internal/web/README.md`

Fails without: the lint in Task 12.1 is this story's only check. It is red for a rule under `## Rules when modifying this app` that names neither **Enforced** nor **Convention**, and for a line over 1,200 bytes. It cannot see a misspelt test file or case name, so the developer greps each name the rules cite in the file it is cited with (`grep -n "<case>" <file>` prints one line) before committing. There is no red test for the prose in `docs/claude/web-layer.md`, `USAGE.md` and `internal/web/README.md`; the review reads them against the drafts.
