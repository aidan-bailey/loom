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

- `core/web.go` (changed): `LoopbackOrigins`.
- `internal/web/server.go` (changed): the origin set; `internal/web/web_test.go` (changed); `internal/web/README.md`, `internal/web/CLAUDE.md` (changed: the guard rule's host and origin wording).
- `internal/cli/web.go` (changed: `loom web open`, the status's Local row); `internal/cli/web_open.go` (new: the command and the opener's wait); `internal/cli/open_unix.go`, `internal/cli/open_windows.go` (new: the opener's command per platform); `internal/cli/web_test.go` (changed).
- `e2e/web_test.go` (changed): pairing and streaming over the loopback origin.
- `USAGE.md` (changed).

The new app (`internal/web/ui/src/`):

- `app/pairLink.ts` (new); `main.tsx` (changed).
- `app/scroll.tsx` (new); `panels/turns/Conversation.tsx`, `panels/job/JobBody.tsx`, `panels/flow/FlowDocBody.tsx` (changed: scroll through the pane); `session/NoSession.tsx` and its callers (changed: the session id from props).
- `components/ui/dialog.tsx` (new); `app/ConfirmSheet.tsx`, `components/ui/toast.tsx` (changed: a variant and a placement).
- `app/viewport.ts`, `app/desktopState.ts` (new); `app/shell.tsx` (changed: `PhoneShell`, the switch, shared parts exported).
- `app/status.tsx` (new: the status row's parts); `fleet/filter.ts`, `fleet/WorkspaceFilter.tsx` (new); `panels/fleet/index.tsx`, `panels/fleet/FleetHead.tsx`, `panels/new/NewSessionBody.tsx` (changed).
- `desktop/DesktopShell.tsx`, `desktop/TopBar.tsx`, `desktop/Sidebar.tsx`, `desktop/inspector.ts`, `desktop/Inspector.tsx` (new), each with a test beside it.
- `*.test.ts(x)` beside each module they test.

The built bundle: `internal/web/dist/` (rebuilt by every story that changes the app, committed).

Elsewhere: `internal/web/static_budget_test.go` (the lazy chunk list); `tools/webshot/frame.go`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `.github/workflows/web.yml`; `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`.

## Names contract

Every name one story defines and another uses. A story may add names of its own; a name another story needs and this section lacks goes back to the manager. Paths are under `internal/web/ui/src/` unless they start at the repo root.

**The loopback origins (Story 1):**

- `func LoopbackOrigins(addr string) ([]string, error)` (Story 1), in `core/web.go`: the loopback origins of a bound `ip:port`, `http://localhost:<port>` first when the IP is `127.0.0.1` or `::1`, then `http://<ip>:<port>` (an IPv6 literal bracketed); an error when `addr` is not a loopback IP literal and a port from 1 to 65535.
- `internal/web/server.go` (Story 1): `guard` accepts a `Host`, and on a write exactly one `Origin`, from the set of the configured origin and `LoopbackOrigins` of the bound address; a configured origin that is one of them counts once; `bind` builds the set from the address it bound.
- The guard test case `"localhost is not 127.0.0.1"` in `internal/web/web_test.go` (Story 1): accepted when the listener is bound to `127.0.0.1:7420`, refused when it is bound to another port.

**`loom web open` (Story 2):**

- `loom web open [--name <device>]` (Story 2), default name `desktop` (`const defaultDesktopName = "desktop"`, Story 2), in `internal/cli/web_open.go`, registered by `newWebCmd` in `internal/cli/web.go`.
- `var openBrowser func(url string) error` (Story 2), in `internal/cli/web_open.go`: the seam tests replace; it starts the command `openerCommand` builds for the URL and waits through `waitOpener`.
- `func openerCommand(url string) *exec.Cmd` (Story 2): `xdg-open <url>` in `internal/cli/open_unix.go` (macOS: `open <url>`, by `runtime.GOOS`), `rundll32 url.dll,FileProtocolHandler <url>` in `internal/cli/open_windows.go`.
- `const openerWait = 5 * time.Second` (Story 2) and `func waitOpener(cmd *exec.Cmd, limit time.Duration) error` (Story 2): nil when the opener exits 0 within `limit` or is still running at `limit` (left running, never killed); an error naming the exit status when it exits otherwise within `limit`.
- The messages tests match (Story 2): `The web app is off; turn it on with loom web enable.`; `Pairing code for <name> expires at <HH:MM:SS>.`; `<name> is already paired. If this browser is not, pass --name or run loom web revoke <name>.`; `Opening <url>` before the opener runs; `Could not open a browser (<reason>); open the URL above yourself.`
- `writeWebStatus` (Story 2): while serving, a row `Local:` listing `LoopbackOrigins(w.Addr)` joined by `", "`.
- The pairing link (Story 2 writes it, Story 3 reads it): `<local>/#/pair?code=<8 characters>`, `<local>` being `LoopbackOrigins(w.Addr)[0]`; `<local>/` for a paired name.

**The pairing link in the app (Story 3)**, in `app/pairLink.ts`:

- `function holdPairLink(): void` (Story 3): when `window.location.hash` is `#/pair` or `#pair` with a `code` query value, replaces it with `#/pair` (`history.replaceState`) and holds the code as `normalizeCode` (from `panels/pair/Pair.tsx`) returns it when its length is `CODE_LENGTH`; `main.tsx` calls it before `registerWorker()` and before the router or the stream starts.
- `function watchPairLink(): () => void` (Story 3): on the first `onUnpaired` signal while a code is held, runs `device.pair` with `{ code }` through `runCommand` with a context whose `panel` is `"pair"`, then drops the code; a live (not stale) snapshot drops it unused; returns its stop. `main.tsx`'s `start()` calls it before `startFleet()`.

**The scroll pane and the body props (Story 4)**, in `app/scroll.tsx`:

- `interface ScrollPane { top: () => number, viewport: () => number, height: () => number, scrollTo: (top: number) => void, topFor: (el: Element) => number, subscribe: (listener: () => void) => () => void }` (Story 4): `top` the scroll position, `viewport` the visible height, `height` the content height, `topFor` the scroll position that puts `el`'s top just below the pane's pinned head (at least 0), `subscribe` called on each scroll, returning its stop.
- `const windowPane: ScrollPane` (Story 4): the window (`window.scrollY`, `window.innerHeight`, `document.documentElement.scrollHeight`), its pinned head `[data-slot="header"]`.
- `function elementPane(scroller: HTMLElement, head: HTMLElement | null): ScrollPane` (Story 4): an element that scrolls, its pinned head `head`.
- `function ScrollPaneProvider(props: { pane: ScrollPane, children: ReactNode }): ReactElement` (Story 4) and `function useScrollPane(): ScrollPane` (Story 4): the pane in context, `windowPane` without a provider.
- `function NoSession(props: { sessionId: number | undefined }): ReactElement` (Story 4), in `session/NoSession.tsx`: reads no route.

**The dialog, the sheet and the toasts (Story 5):**

- `components/ui/dialog.tsx` (Story 5): shadcn's Dialog on `@base-ui/react/dialog`, exporting `Dialog`, `DialogContent`, `DialogTitle`, `DialogDescription` and `DialogFooter` (Story 5 proposes).
- `type ConfirmVariant = "drawer" | "dialog"` and `function ConfirmSheet(props: { variant: ConfirmVariant }): ReactElement` (Story 5), in `app/ConfirmSheet.tsx`: the same sheet content in a Drawer or a centred Dialog; `askConfirm`'s API unchanged.
- `type ToastPlacement = "top" | "bottom-right"` and `function Toaster(props: { placement: ToastPlacement }): ReactElement` (Story 5), in `components/ui/toast.tsx`.

**The layout and the shell (Story 6):**

- `type Layout = "phone" | "desk2" | "desk3"`, `const DESK2_MIN = 1024`, `const DESK3_MIN = 1280`, `function layoutOf(width: number): Layout` and `function useLayout(): Layout` (Story 6), in `app/viewport.ts`: `useLayout` reads `matchMedia` through `useSyncExternalStore`.
- `const DESKTOP_KEY = "loom.desktop"`, `interface DesktopSlot { tab: string }`, `interface DesktopPrefs { sidebar: boolean, inspector: boolean, slots: DesktopSlot[] }` and `function readDesktopPrefs(raw: string | null): DesktopPrefs` (Story 6), in `app/desktopState.ts`: anything that does not parse into that shape is `{ sidebar: true, inspector: true, slots: [] }`.
- `interface DesktopState { sidebar: boolean, inspector: boolean, slots: DesktopSlot[], setSidebar: (shown: boolean) => void, setInspector: (shown: boolean) => void, setTab: (tab: PanelId) => void }` and `const useDesktop: UseBoundStore<StoreApi<DesktopState>>` (Story 6): read from `DESKTOP_KEY` once, written back on each set; `setTab` sets the first slot's tab, adding the slot when there is none.
- `app/shell.tsx` (Story 6): `function PhoneShell(): ReactElement` (today's frame without the sheet and the toaster); `AppShell` draws `PhoneShell` for the phone layout, for pairing and for the splash with no snapshot, else the lazy `DesktopShell`, and mounts `<ConfirmSheet variant>` and `<Toaster placement>` once above both (`"drawer"`/`"top"` on the phone, `"dialog"`/`"bottom-right"` on desktop); exports `Boundary`, `TABBED` and `tabOf`, which `DesktopShell` uses.
- `function DesktopShell(): ReactElement` (Story 6), the default export of `desktop/DesktopShell.tsx`: the grid; Story 6 draws its middle (the route's Head, `SessionTabs`, Strip and Body in an `elementPane` with its own pinned head) and the full mockup full-window; Stories 8 to 10 add the top bar, the sidebar and the inspector to it.
- `checkLazyViews`' list in `internal/web/static_budget_test.go` gains `DesktopShell` (Story 6).

**The status parts and the filter (Story 7):**

- `function HostLabel(): ReactElement`, `function LinkPill(): ReactElement` and `function GroupCounts(): ReactElement` (Story 7), in `app/status.tsx`: `StatusRow`'s parts as they draw today (`loom@host` with the last update's time while offline; the link's pill; the three counts that open the group's next session), which `StatusRow` composes.
- `const FILTER_KEY = "loom.filter"` (Story 7), moved from `panels/fleet/index.tsx` to `fleet/filter.ts`; `panels/new/NewSessionBody.tsx` imports it from there.
- `interface FilterState { stored: string, pick: (id: string) => void }` and `const useFilter: UseBoundStore<StoreApi<FilterState>>` (Story 7), in `fleet/filter.ts`: the stored choice (a workspace id as a string, or `""` for all), read from `FILTER_KEY` once and written on `pick`.
- `function activeFilter(stored: string, workspaces: Workspace[]): string` (Story 7): `stored` when it names one of `workspaces`, else `""`.
- `function WorkspaceFilter(): ReactElement | null` (Story 7), in `fleet/WorkspaceFilter.tsx`: the fleet's toggle group over the shown workspaces and "All", null with fewer than two; the fleet panel draws it only in the phone layout.

**The desktop chrome (Stories 8 to 10):**

- `function FleetLinks(): ReactElement` (Story 8), in `panels/fleet/FleetHead.tsx`: the flows, settings and new-session links `FleetHead` draws today, which `FleetHead` and the top bar both draw.
- `function TopBar(): ReactElement` (Story 8), in `desktop/TopBar.tsx`: `LinkPill`, `HostLabel`, `WorkspaceFilter`, `GroupCounts` and `FleetLinks`; `DesktopShell` draws it, and draws no Head on the fleet route.
- `interface SidebarLine { text: string, tone: "attention" | "docs" | "error" | "muted" }` and `function sidebarLine(session: Session): SidebarLine` (Story 9), in `desktop/Sidebar.tsx`: why it waits (`wait.reason`, then `wait.detail`) while its group is `needs_you`, else its newest flow's `flowChip` in `flowTone`, else `last_message`.
- `function Sidebar(props: { current: number | undefined }): ReactElement` (Story 9): the groups over the shown workspaces under `activeFilter`, Paused folded; `DesktopShell` draws it on every route but the fleet while `useDesktop().sidebar`.
- `function inspectorTab(tabs: PanelId[], middle: PanelId, chosen: string | undefined): PanelId | undefined` (Story 10), in `desktop/inspector.ts`: `chosen` when it is in `tabs` and is not `middle`; else, when `chosen` is in `tabs`, the next tab after it in `tabs`' order, wrapping, that is not `middle`; else the first tab in `tabs` that is not `middle`; undefined when none.
- `function Inspector(props: { session: Session, middle: PanelId }): ReactElement | null` (Story 10), in `desktop/Inspector.tsx`; `DesktopShell` draws it on session routes in the `desk3` layout while `useDesktop().inspector`.

**webshot (Story 11):**

- The devices `desktop` (1440×900) and `desktop-narrow` (1100×800) (Story 11), in `tools/webshot/frame.go`: bare viewports, scale 1, no touch, a desktop Chrome user agent; `-device` accepts them; the phone-only `add-to-home` screen is skipped on them.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): `desktop/DesktopShell.tsx` as Story 6 leaves it (Stories 8, 9, 10, each adding its part); `app/shell.tsx` as Story 6 leaves it (Story 7); `panels/fleet/index.tsx` as Story 7 leaves it (Story 8); `internal/web/README.md` as Story 1 leaves it (Story 12); `USAGE.md` as Story 2 leaves it (Story 12).

## Stories

- **Story 1** — The web layer accepts the listener's own loopback origins beside the configured one.

  Add `LoopbackOrigins` to core and make `guard`'s rule a set: the configured origin plus the bound address's loopback origins, `localhost` among them only for a `127.0.0.1` or `::1` listener (spec §21–§23). The Host check (421) and the write's Origin check (403) accept any member and nothing else; a configured loopback origin counts once; push keeps the configured origin as its subject. Pin both sides of every guard: each loopback host and origin accepted on the right listener, another port's refused, the configured origin unchanged, the flipped `"localhost is not 127.0.0.1"` case on both listeners. Extend the e2e web test to pair and stream over the loopback origin. Amend `internal/web/CLAUDE.md`'s guard rule in place (198 bytes free) and describe the set in `internal/web/README.md`'s "Every request".

  - **Developer:** intermediate-dev
  - **Files:** `core/web.go`, `core/web_test.go`, `internal/web/server.go`, `internal/web/web_test.go`, `internal/web/README.md`, `internal/web/CLAUDE.md`, `e2e/web_test.go`
  - **Interfaces:** exposes `core.LoopbackOrigins` and the origin set's behaviour; uses `parseOrigin`, `hostKey`, `core.ValidateWebListen`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./core/ ./internal/web/ && CGO_ENABLED=0 go test -tags e2e -timeout 30m ./e2e/ -run TestE2E_Web'`
  - **Review:** deep
  - **Form:** full (auth: the Host and Origin checks front every request, and a wrong member opens the API to another origin)
  - **Reading:** spec Findings (the origin rule), §21–§23, Testing (Go, e2e); `internal/web/server.go:1-200`, `internal/web/web.go:255-330`, `internal/web/web_test.go:80-230`, `internal/web/push.go:195-220`, `core/web.go:1-60`, `core/web.go:200-280`, `internal/web/README.md:36-60`, `internal/web/CLAUDE.md:11`, `e2e/web_test.go:130-308`

- **Story 2** — `loom web open` pairs or opens the browser on localhost, and `loom web status` shows the local origin.

  Add `loom web open [--name <device>]` (spec §24–§25): refused while the web is off; for a name not yet paired it mints a code with `PairDevice` and opens the pairing link; for a paired name it opens the plain local URL and says so; the URL is always printed. The opener runs through the `openBrowser` seam with the platform's command and a 5-second wait that never kills it; a missing or failing opener is reported and the command still exits 0. `loom web status` gains the Local row. `internal/cli/CLAUDE.md` is at its byte budget, so no rule is added there. Document the command in `USAGE.md`'s web section.

  - **Developer:** intermediate-dev
  - **Files:** `internal/cli/web.go`, `internal/cli/web_open.go`, `internal/cli/open_unix.go`, `internal/cli/open_windows.go`, `internal/cli/web_test.go`, `USAGE.md`
  - **Interfaces:** exposes `loom web open`, `openBrowser`, `openerCommand`, `waitOpener`, `openerWait`, the Local status row and the pairing link's form; uses `core.LoopbackOrigins` (Story 1), `PairDevice`, `ValidateDeviceName`, `ErrDeviceExists`, `formatPairingCode`, `withDaemon`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test -timeout 30m ./internal/cli/ -run TestWeb && GOOS=windows CGO_ENABLED=0 go build ./...'`
  - **Review:** deep
  - **Form:** full (an outside call: the opener's start, its 5-second wait and the rule that it is never killed)
  - **Reading:** spec §24–§25, Errors, Testing (Go); `internal/cli/web.go:1-270`, `internal/cli/web_test.go:1-100`, `internal/cli/web_test.go:379-540`, `core/web.go:255-280`, `core/web.go:365-400`, `USAGE.md:840-885`

- **Story 3** — The app consumes a pairing link before its router starts.

  Add `app/pairLink.ts` and wire it into `main.tsx` (spec §26): `holdPairLink` takes `#/pair?code=…` (or `#pair?code=…`) out of the address in place before the worker, the router or the stream starts, holding the normalised code only at 8 characters; `watchPairLink` runs `device.pair` with it once, on the first unpaired signal, and a live snapshot drops it unused. A refused code leaves the usual pairing screen with the daemon's words as the command's toast. Pin the order: no request and no route sees the code, the code is used at most once, and a paired browser never posts it.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/pairLink.ts`, `internal/web/ui/src/app/pairLink.test.ts`, `internal/web/ui/src/main.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `holdPairLink`, `watchPairLink`; uses `normalizeCode`, `CODE_LENGTH`, `onUnpaired`, `runCommand`, `commandById`, `useFleet`, and the pairing link's form (Story 2)
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (auth: a pairing credential in the address, removed before any request and used once)
  - **Reading:** spec §26, Errors, Testing (Vitest); `internal/web/ui/src/main.tsx`, `internal/web/ui/src/panels/pair/Pair.tsx`, `internal/web/ui/src/commands/device.ts:30-70`, `internal/web/ui/src/api/request.ts:1-70`, `internal/web/ui/src/app/commands.ts:1-200`, `internal/web/ui/src/app/routes.tsx:40-60`, `internal/web/ui/src/app/routes.tsx:140-175`, `internal/web/ui/src/store/fleet.ts:100-170`

- **Story 4** — Panels scroll through a `ScrollPane`, and bodies take their session from props.

  Add `app/scroll.tsx` (spec §12) and move the three panels that scroll the window onto it: the conversation's bottom-following, anchoring and kept scroll, the job log's follow, and the flow document's heading jump below the pane's pinned head. With no provider the pane is the window, so the phone behaves exactly as today and the existing tests keep passing unchanged; new tests run each behaviour inside an `elementPane`. `NoSession` takes the session id as a prop (spec §13) and every caller passes its own.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/scroll.tsx`, `internal/web/ui/src/app/scroll.test.tsx`, `internal/web/ui/src/panels/turns/Conversation.tsx`, `internal/web/ui/src/panels/turns/turns.test.tsx`, `internal/web/ui/src/panels/job/JobBody.tsx`, `internal/web/ui/src/panels/job/job.test.tsx`, `internal/web/ui/src/panels/flow/FlowDocBody.tsx`, `internal/web/ui/src/panels/flow/doc.test.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/session/session.test.tsx`, `internal/web/ui/src/panels/agent/Agent.tsx`, `internal/web/ui/src/panels/changes/index.tsx`, `internal/web/ui/src/panels/flow/FlowBody.tsx`, `internal/web/ui/src/panels/flow/FlowStoryBody.tsx`, `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/mockup/MockupBody.tsx`, `internal/web/ui/src/panels/turns/index.tsx`, `internal/web/dist/`
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

  Add `app/viewport.ts` and `app/desktopState.ts` (spec §4–§6), split today's frame into `PhoneShell`, and make `AppShell` choose it or the lazy `DesktopShell` live as the window resizes, mounting the one confirmation sheet and toaster above both in the layout's form (spec §11). `DesktopShell` draws the route's Head, the session's tabs, its Strip and its Body in a middle pane that scrolls on its own under a pinned head (an `elementPane` provided to the panel), keeps the conversation's readable measure, draws the full mockup full-window, and leaves pairing and the splash to `PhoneShell`. Add the `DesktopShell` stem to `checkLazyViews` so the phone's first load is unchanged. Pin the breakpoints, a resize swapping the shell with the route and an open confirmation kept, and every malformed `loom.desktop` value read as the default.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/viewport.ts`, `internal/web/ui/src/app/viewport.test.ts`, `internal/web/ui/src/app/desktopState.ts`, `internal/web/ui/src/app/desktopState.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes `Layout`, `DESK2_MIN`, `DESK3_MIN`, `layoutOf`, `useLayout`, `DESKTOP_KEY`, `DesktopSlot`, `DesktopPrefs`, `readDesktopPrefs`, `DesktopState`, `useDesktop`, `PhoneShell`, `DesktopShell`, the exported `Boundary`, `TABBED` and `tabOf`; uses `elementPane`, `ScrollPaneProvider` (Story 4), `ConfirmSheet`'s `variant` and `Toaster`'s `placement` (Story 5), `PanelHost`, `SessionTabs`, `panelFor`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (bounds: a stored value read back from localStorage, and the lazy chunk the budget test pins)
  - **Reading:** spec §4–§6, §9, §11, Errors, Testing (Vitest, Go); `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/routes.tsx:1-140`, `internal/web/ui/src/app/panels.ts`, `internal/web/static_budget_test.go:180-304`, `internal/web/ui/CLAUDE.md` (the lazy-view rules)

- **Story 7** — The status row's parts and the workspace filter become shared pieces.

  Pull `HostLabel`, `LinkPill` and `GroupCounts` out of `StatusRow` into `app/status.tsx`, the phone's footer drawing exactly what it draws today (spec §7). Move the workspace filter out of the fleet panel into `fleet/filter.ts` (`FILTER_KEY`, `useFilter`, `activeFilter`) and `fleet/WorkspaceFilter.tsx`, so the fleet panel, the new-session form and the desktop share one choice, `loom.filter` keeping its classic encoding; the fleet panel draws the filter only in the phone layout (spec §8). The existing fleet, footer and new-session tests keep passing; new tests pin `activeFilter` and that a pick in one place shows in the other.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/status.tsx`, `internal/web/ui/src/app/status.test.tsx`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/fleet/filter.ts`, `internal/web/ui/src/fleet/filter.test.ts`, `internal/web/ui/src/fleet/WorkspaceFilter.tsx`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/panels/fleet/fleet.test.tsx`, `internal/web/ui/src/panels/new/NewSessionBody.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `HostLabel`, `LinkPill`, `GroupCounts`, `FILTER_KEY` (moved), `FilterState`, `useFilter`, `activeFilter`, `WorkspaceFilter`; uses `useLayout` (Story 6), `nextInGroup`, `fleetGroups`, `shownWorkspaces`, `groupSessions`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (moves of existing parts and one small shared store; the existing footer and fleet tests pin their drawing)
  - **Form:** brief (existing parts moved into shared modules, and a store over an existing localStorage key)
  - **Reading:** spec §7–§8; `internal/web/ui/src/app/shell.tsx:330-476`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/panels/new/NewSessionBody.tsx:20-60`, `internal/web/ui/src/fleet/groups.ts`

- **Story 8** — The desktop's top bar.

  Add `desktop/TopBar.tsx` (spec §7) from the shared parts: the link's pill and `loom@host`, the workspace filter, the group counts and the fleet's links, which `FleetHead` and the top bar now share as `FleetLinks`. `DesktopShell` draws the top bar across the window and, on the fleet route, no Head (spec §8). The palette's box comes with the palette in part 2. Tests pin the bar's parts, a count opening the group's next session, and the fleet route drawing no Head on desktop.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/TopBar.tsx`, `internal/web/ui/src/desktop/TopBar.test.tsx`, `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `FleetLinks`, `TopBar`; uses `HostLabel`, `LinkPill`, `GroupCounts`, `WorkspaceFilter` (Story 7), `DesktopShell` (Story 6)
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (chrome assembled from shared parts)
  - **Form:** brief (a bar of existing parts)
  - **Reading:** spec §5, §7–§8; `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `docs/superpowers/specs/2026-11-21-web-desktop/frame.html`
  - **Drafts after:** Story 6, Story 7

- **Story 9** — The desktop's fleet sidebar.

  Add `desktop/Sidebar.tsx` (spec §8): the fleet's groups over the shown workspaces under the shared filter, in compact two-line rows (the group's dot, the title and its age; then `sidebarLine`'s why-it-waits, flow chip or last message in its tone), the session on screen highlighted, each row opening `sessionHref`, Paused folded closed. `DesktopShell` draws it beside the middle on every route but the fleet, while `useDesktop().sidebar`. Tests pin `sidebarLine`'s three cases and its order, the highlight, the fold, the filter, and the sidebar's absence on the fleet route.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/Sidebar.tsx`, `internal/web/ui/src/desktop/Sidebar.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `SidebarLine`, `sidebarLine`, `Sidebar`; uses `useFilter`, `activeFilter` (Story 7), `useDesktop`, `DesktopShell` (Story 6), `GROUPS`, `groupSessions`, `shownWorkspaces`, `sessionHref`, `ago`, `flowChip`, `flowTone`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (a list drawn from existing fleet helpers; its one decision, `sidebarLine`, is pinned by named tests)
  - **Form:** brief (rows from existing helpers)
  - **Reading:** spec §8; `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/fleet/groups.ts`, `internal/web/ui/src/fleet/links.ts`, `docs/superpowers/specs/2026-11-21-web-desktop/frame.html`
  - **Drafts after:** Story 8

- **Story 10** — The desktop's inspector.

  Add `desktop/inspector.ts` and `desktop/Inspector.tsx` (spec §10): a tab strip over the session's tab panels minus the middle's lit tab, its choice the first slot's (`useDesktop().setTab`), falling as `inspectorTab` says, drawing the chosen panel's Body through `PanelHost` with the session's props, in its own error boundary and its own `elementPane`. `DesktopShell` draws it on session routes, in the `desk3` layout, while `useDesktop().inspector`. Tests pin every branch of `inspectorTab`, the choice surviving a reload through `loom.desktop`, a throwing inspector panel leaving the middle drawn, and the inspector's absence at `desk2` and off session routes.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/inspector.ts`, `internal/web/ui/src/desktop/inspector.test.ts`, `internal/web/ui/src/desktop/Inspector.tsx`, `internal/web/ui/src/desktop/Inspector.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `inspectorTab`, `Inspector`; uses `useDesktop`, `useLayout`, `DesktopShell`, `Boundary`, `tabOf` (Story 6), `elementPane`, `ScrollPaneProvider` (Story 4), `sessionTabs`, `PanelHost`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (a panel host in a second pane; `inspectorTab`'s branches are pinned by named tests)
  - **Reading:** spec §6, §10, Errors; `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/shell.tsx` (`PanelHost`, `SessionTabs`, `tabOf`)
  - **Drafts after:** Story 9

- **Story 11** — webshot renders every screen at two desktop sizes, and CI runs them.

  Add the `desktop` (1440×900) and `desktop-narrow` (1100×800) devices to webshot (spec Testing): bare viewports at scale 1 with no touch emulation, no safe-area insets and a desktop Chrome user agent, rendering every screen of the new app but the phone-only `add-to-home`, with the same failure rules (a console error, a CSP violation, an unanswered route). `.github/workflows/web.yml` renders the new app at the phone and both desktop devices. Tests pin the device table and the skipped screen.

  - **Developer:** intermediate-dev
  - **Files:** `tools/webshot/frame.go`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `.github/workflows/web.yml`
  - **Interfaces:** exposes the `desktop` and `desktop-narrow` devices; uses the frame Stories 6 to 10 draw
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -out "$d"'`
  - **Review:** light (a test tool's device modes and a CI step)
  - **Form:** brief (two device entries, a skipped screen and a workflow step)
  - **Reading:** spec Testing (webshot); `tools/webshot/frame.go:1-60`, `tools/webshot/main.go:40-130`, `tools/webshot/main.go:180-230`, `tools/webshot/main.go:440-530`, `tools/webshot/webshot_test.go`, `.github/workflows/web.yml`
  - **Drafts after:** Story 10

- **Story 12** — The rules and the procedures for the desktop.

  Add to `internal/web/ui/CLAUDE.md` the rules part 1 introduces (spec §27): a panel scrolls through `ScrollPane`, never the window; a body reads its session from its props, never the route; desktop chrome (top bar, sidebar, inspector) is not a panel. Add to `docs/claude/web-layer.md` how the desktop composes the panels, the dev loop at desktop widths (webshot's devices) and the desktop checklist (spec Testing), and to `USAGE.md` a paragraph on the desktop layout. Each rule names the test that enforces it or says Convention, and the CLAUDE.md lint passes.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md`
  - **Interfaces:** none
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd'`
  - **Review:** light (docs)
  - **Form:** brief (docs)
  - **Reading:** spec §27, Testing (the desktop checklist); `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md:840-885`
  - **Drafts after:** Story 1, Story 2, Story 3, Story 4, Story 5, Story 6, Story 7, Story 8, Story 9, Story 10, Story 11

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
