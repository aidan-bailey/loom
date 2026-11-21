# Loom web: the desktop

Date: 2026-11-21. Branch `aidanb/desktop-app`. Sub-project 3 of the web programme (`docs/superpowers/specs/2026-11-18-web-foundation-design.md`, The programme), taken before sub-project 2 (Phone) by the owner's choice. A desktop layout for the new app in `internal/web/ui/`, a keyboard and a ⌘K palette, a loopback origin beside the tunnel's, and `loom web open`; no Core request, no wire change, no `rpc.Protocol` bump, and no route under `/api/v1` added or changed.

## Problem

The owner wants loom web on a desktop: a multi-pane workspace driven from the keyboard, on the daemon's own machine and on others. Today the new app is a 40rem phone column centred on a wide screen (foundation §2), every action needs a tap, and the web layer accepts exactly one origin, the tunnel's, so a browser on the daemon's machine reaches it through Cloudflare and back. An older attempt, branch `aidanb/desktop-application` (June 2026, cut from v0.1.2), wrapped an in-process `session.Instance` in a Wails window; it predates the daemon, breaks "one owner of state", and is not this design's base. Its terminal latency work (`638ba1ea`) is a reference for Parity's terminal.

## Decisions by the owner (2026-11-21)

From the brainstorm, with mockups in `docs/superpowers/specs/2026-11-21-web-desktop/` (`layout.html`, `frame.html`):

- **Form:** the same app, responsive, opened in any desktop browser or installed as a browser app (Chrome or Edge's Install App). No native wrapper (Wails, Tauri, Electron) and no third tree; a wrapper can come later around the same app.
- **Reach:** on the daemon's machine, directly at `http://localhost:<port>`; elsewhere, through the tunnel's URL, as the phone does.
- **Layout A**, three columns: the fleet sidebar, the route's panel in the middle, an inspector holding another view of the same session. Layout B (split panes, any panel of any session) is a later addition, so the layout's state is shaped to admit it (§6); layout C (a grid of session tiles) is not taken.
- **Keys:** the TUI's single keys wherever no text field has focus, and ⌘K everywhere.
- **Palette:** commands, sessions and places. Searching conversation text is out of scope (it needs a route that does not exist).
- **Pairing on localhost:** `loom web open`, which mints a code and opens the browser on it. Loopback is never trusted: every agent on the machine could then drive the fleet.
- **Approach:** a responsive shell in the same app with the desktop shell loaded lazily (§4), not a general pane tree from day one and not a separate desktop entry point.

## Findings

From the repo at `a3fce074`, 2026-11-21:

- **The shell** (`internal/web/ui/src/app/shell.tsx`): `AppShell` draws one column (`max-w-[40rem]`): a sticky header (the panel's `Head`, then `SessionTabs` for the routes in `TABBED`, then the panel's `Strip`), `<main>` (the router's `Outlet`, or the splash with no snapshot), and a sticky footer holding `StatusRow` (the link's pill, `loom@host`, and the needs-you, your-turn and working counts that open the group's next session with `nextInGroup`), hidden while a text field has focus. `PanelHost` draws a panel's `Body` in an error boundary keyed by the route, with a `Suspense` inside it. `ConfirmSheet` (`app/ConfirmSheet.tsx`) is a Base UI Drawer; the `Toaster` (`components/ui/toast.tsx`) is fixed top-centre.
- **Panels** (`app/panels.ts`): `{ id, title, appliesTo?, tab?: { order, label, Mark? }, Head?, Strip?, Body }`, fifteen `PanelId`s, every one registered. `sessionTabs(session)` lists the tab panels that apply, in order: conversation 1, changes 2, info 3, mockup 4, flow 5. `PanelProps` is `{ sessionId, params, search }`.
- **Window scrolling:** three panels scroll the window itself. `panels/turns/Conversation.tsx` follows the bottom and keeps each session's scroll (`window.scrollY`, `window.scrollTo`, `keepScroll`); `panels/job/JobBody.tsx` follows a log's end; `panels/flow/FlowDocBody.tsx` jumps to a heading below the height of `[data-slot="header"]`.
- **Route reads in bodies:** `session/NoSession.tsx` reads `useRoute()` for the session id (`useIsPending(panelProps(useRoute()).sessionId)`); `session/SessionHead.tsx` and `session/FactStrip.tsx` read it too, but only draw as the route's own Head and Strip.
- **Commands** (`app/commands.ts`): `defineCommand({ id, title, icon?, offline?, key?, when, confirm?, run })`, run through `runCommand` (sheet, enablement checked again, in-flight key, toasts). Only an `offline` command runs while the snapshot is stale (`app.backToClassic`, `device.pair`), a rule `internal/web/ui/CLAUDE.md` states.
- **Fleet helpers:** `GROUPS` (needs you, your turn, working, paused), `fleetGroups`, `groupSessions`, `shownWorkspaces`, `nextInGroup` (`fleet/groups.ts`); `sessionHref` (`fleet/links.ts`); `ago`, `usd`, `flowChip`, `flowTone` (`panels/fleet/SessionRow.tsx`); the workspace filter's localStorage key `loom.filter` (`FILTER_KEY`, `panels/fleet/index.tsx`).
- **Base UI** 1.9 ships Dialog, Autocomplete, Combobox, Collapsible and Tabs; `main.tsx`'s `CSPProvider disableStyleElements` keeps any of them from writing a `<style>`.
- **The TUI's keymap** (`script/defaults.lua`): `j`/`k` and the arrows move, `g`/`G` and `J`/`K` jump and page, `]`/`[` next and previous waiting, `{`/`}` and `l`/`;` previous and next workspace, `d` diff, `n`/`N` new, `D` kill, `s` pause, `r` resume, `p` push, `m` merge, `S` settings, `\` the rail, `?` help; plus keys for features the web lacks (`T`, `i`, `a`, `t`, `f`, `w`, `F`, `c`, `I`, `R`, `W`, the split keys).
- **The origin rule** (`internal/web/server.go`): `bind` refuses a listen address that is not a loopback IP literal and port (`core.ValidateWebListen`), parses the one configured origin (`parseOrigin`), and stores one `originRule`; `guard` answers 421 to a `Host` that is not the origin's and 403 to a write without exactly one `Origin` equal to it. `loom web enable --origin` already accepts `http://localhost:<port>` and `http://127.0.0.1:<port>` for development, and `noteHTTPOrigin` says an http origin sends no notification: the VAPID token's subject is the origin, which must be https.
- **Pairing** (`internal/cli/web.go`, `core/web.go`): `loom web pair [--name]` (default name `iPhone`) calls `PairDevice`, which refuses a name already paired (`ErrDeviceExists`) and replaces a pending code for the same name; a code works once, for ten minutes (`PairingTTL`). The device cookie `__Host-loom` is Secure, HttpOnly, SameSite=Strict and host-only, so each origin holds its own. No loom code opens a browser today.
- **The budget:** the first load (the entry script and every `modulepreload` `dist/index.html` names) is about 194,000 bytes gzipped against 250,000. `checkLazyViews` (`internal/web/static_budget_test.go`) pins eight lazy chunk stems.
- **webshot** (`tools/webshot`) renders as an iPhone 15 or 16 only (`-device`, `frame.go`); `.github/workflows/web.yml` runs `go run ./tools/webshot -app next`.
- **Budgets:** root `CLAUDE.md` 15,981 of 16,000 bytes; `internal/web/CLAUDE.md` 19,802 of 20,000; `internal/web/ui/CLAUDE.md` 8,525 of 20,000.

## Decisions

### Scope

1. **In scope:** the desktop layout and its panes (§4–§13), the keymap and its view commands (§14–§17), the palette (§18–§20), the loopback origin (§21–§23), `loom web open` and the pairing link (§24–§26), webshot's desktop devices (Testing), and their rules and docs (§27).
2. **Out of scope:** layout B's split panes; searching conversation text; a native wrapper; a light theme; every Parity feature (the terminal and the TUI keys that only it gives meaning: `T`, `a`, `t`, `f`, `w`, `F`, `c`, `I`, `R`, `W` and the split keys); any change to the phone layout below 1024px; user-defined keys (they come with Lua in the daemon, Parity).
3. **Done when** every screen renders in webshot at the phone and both desktop sizes with no console error, CSP violation or unanswered route; the key, palette, origin and `loom web open` tests pass; the phone's first load is unchanged by the desktop code; and the desktop checklist (Testing) passes.

### The layout

4. **One app, two shells.** `useLayout()` (`app/viewport.ts`) is `phone` under 1024px, `desk2` from 1024 to 1279px and `desk3` from 1280px, read from `matchMedia` through `useSyncExternalStore`, so resizing the window swaps the layout live. `AppShell` becomes a switch: `PhoneShell` is today's frame, unchanged; `DesktopShell` is a `React.lazy` module of its own (`desktop/DesktopShell.tsx`), its stem added to `checkLazyViews`' list and its table test, so the phone's first load does not grow. Routes do not change: the hash names the middle's panel on both shells, and a push link opens the same view. A breakpoint crossed keeps the route, the drafts and each session's held scroll.
5. **The frame at `desk3`:** a top bar across the window, then the sidebar (fixed width, about 250px), the middle (the rest), and the inspector (about 330px, at most a third of the window). At `desk2` there is no inspector: the middle's session tabs reach every view. There is no footer on desktop; the top bar carries what the phone's footer does.
6. **The layout's state** is the sidebar's and the inspector's visibility and the inspector's tab, kept in localStorage `loom.desktop` as `{ "sidebar": true, "inspector": true, "slots": [{ "tab": "changes" }] }`: `slots` is a list so layout B can add panes without a new key, and this sub-project reads and writes only its first entry. A value that does not parse, or names a tab no panel has, is the default (both shown, the inspector's tab the first that applies).
7. **The top bar:** the link's pill and `loom@host` (with the last update's time while offline), the workspace filter, the palette's box ("Search sessions, commands, places" with its key), the needs-you, your-turn and working counts (each opens the group's next session, as the footer's do), and links to new session, the host's flows (while a shown session has flows) and settings. The pill and the counts are pulled out of `StatusRow` into `LinkPill` and `GroupCounts`, which the phone's footer and the top bar both draw. The workspace filter moves out of the fleet panel into `fleet/filter.ts` (`FILTER_KEY` and its hook, `loom.filter` unchanged), which the fleet panel and the top bar both use.
8. **The sidebar** is the fleet's groups over the shown workspaces in compact two-line rows: the group's dot, the title and its age on the first line; on the second, why it waits (an ask or a permission), else the flow's chip in its tone, else what it said last. The session on screen is highlighted; a row opens `sessionHref`; Paused folds as on the phone (a Collapsible, closed by default). It is chrome, like the footer, not a panel. On the fleet route (`#/`) the sidebar is hidden and the middle draws the fleet panel's Body in full, since the sidebar would repeat it; on desktop the fleet's Head is not drawn, since the top bar carries what it holds.
9. **The middle** is exactly the phone's frame for the route: the panel's Head, the session's tabs, its Strip and its Body, in a pane that scrolls on its own under a pinned head. Conversation text keeps a readable measure (the reply box and turns at most about 48rem). Pairing and the full mockup draw full-window with no chrome, as on the phone.
10. **The inspector** draws on session routes only (not on the fleet, the host's flows, new session, settings or pairing), at `desk3`, while shown. Its tab strip lists `sessionTabs(session)` minus the tab the middle lights (`tabOf`); its chosen tab is the slot's, and when that tab is the middle's or does not apply it shows the next one in tab order. It draws the tab panel's `Body` through `PanelHost` with `{ sessionId, params: { id }, search: {} }`, in its own error boundary and scroll pane. A link inside it (an agent, a job, a story) navigates the middle.
11. **Shared parts by layout:** `askConfirm` keeps its API and draws a Drawer on the phone and a centred Base UI Dialog on desktop; the `Toaster` stays top-centre on the phone and sits bottom-right on desktop. Phone-only rules (44px targets, 16px form text, safe-area insets) stay in the panels; the desktop chrome (top bar, sidebar, inspector tabs) is denser.

### Panels in panes

12. **Panels scroll through `ScrollPane`, never the window.** `app/scroll.tsx` gives a panel its scroller through context: the window on the phone, the pane's element on desktop, with its scroll position, its viewport height, its content height, `scrollTo`, a scroll subscription and the height of the pane's pinned head. Conversation, JobBody and FlowDocBody move onto it, with their behaviour unchanged on the phone (their tests pin it). FlowDocBody's jump reads the pane's head height instead of `[data-slot="header"]`'s.
13. **A body reads its session from its props, never from the route.** `NoSession` takes the session id as a prop instead of reading `useRoute()`. A Head or a Strip may read the route, since only the middle draws them.

### The keys

14. **One table, `app/keymap.ts`,** maps a key to a command id, as `script/defaults.lua` does for the TUI. Every key runs a registered command through `runCommand`; the palette and the help read the same table. The keys are bound only in `DesktopShell`, by one `keydown` listener on the document (`desktop/useKeys.ts`), which does nothing while a text field has focus, while a dialog or the palette is open, during IME composition (`isComposing`), or with ⌘, Ctrl or Alt held (Shift is part of the key), except ⌘K and Ctrl+K, which open the palette from anywhere, a text field included. `Esc` in the reply box blurs it.
15. **The keys:**

    | Key | Command | Does |
    |---|---|---|
    | `j`, `↓` / `k`, `↑` | `nav.nextSession` / `nav.prevSession` | the next or previous session the sidebar shows, in its order (a folded Paused group's skipped), stopping at the ends; it opens in the middle's tab when that tab applies to it, else at its default (conversation, or info for a session that is not Claude's) |
    | `g` / `G` | `nav.firstSession` / `nav.lastSession` | the first or last |
    | `J` / `K` | `nav.pageDown` / `nav.pageUp` | ten sessions on or back, stopping at the ends |
    | `]` / `[` | `nav.nextWaiting` / `nav.prevWaiting` | the next or previous session that needs you, wrapping (`nextInGroup` over `needs_you`) |
    | `}`, `;` / `{`, `l` | `nav.nextWorkspace` / `nav.prevWorkspace` | the workspace filter's next or previous choice, wrapping through "all" |
    | `i`, `Enter` | `nav.focusReply` | focus the reply box (`data-slot="reply"`), on a session whose conversation is in the middle |
    | `d` | `nav.toggleChanges` | the changes tab in the middle while it applies to the session, or back to the conversation |
    | `n`, `N` | `go.new` | new session |
    | `s`, `r`, `p`, `m`, `D` | `session.pause`, `.resume`, `.push`, `.merge`, `.kill` | the session's commands, with their dialogs |
    | `S` | `go.settings` | settings |
    | `\` | `view.toggleSidebar` | show or hide the sidebar |
    | `\|` | `view.toggleInspector` | show or hide the inspector |
    | `?` | `app.help` | the key help |
    | ⌘K, Ctrl+K | `app.palette` | the palette |

    The session commands' ids are the ones part 2 registered; the view commands (`nav.*`, `go.*`, `view.*`, `app.help`, `app.palette`) are new, in `commands/nav.ts`.
16. **View commands are `offline`:** they act on nothing in loom (they move the view, toggle chrome or open a dialog), so they run while the snapshot is stale. `internal/web/ui/CLAUDE.md`'s offline rule is amended to name them beside the switch and pairing. A session command keeps its `when` and stays off while stale.
17. **The help** (`desktop/KeyHelp.tsx`) is a Dialog listing the table: each key with its command's title, grouped as navigation, session, view and app, generated from `keymap.ts` so it never drifts.

### The palette

18. **A Base UI Dialog around a Base UI Autocomplete** (`desktop/Palette.tsx`), opened by ⌘K, Ctrl+K or the top bar's box. Its items come from `desktop/palette.ts` (pure): sessions (every session of every workspace, paused included, matched on title, branch and workspace name), commands (each registered command that takes no args, matched on its title, with its key from the table), and places (Settings, the host's flows, New session, the fleet, and the current session's tab panels and flow board, matched on their titles). Commands that need args picked in a view (a job's controls, an ask's answers) are not listed.
19. **Matching and order:** a hand-written fuzzy score (a subsequence match, consecutive and word-start characters scoring higher), no dependency. Results are grouped sessions, commands, places, each ordered by score, then by title compared by code point (never `localeCompare`). An empty query lists the sessions that need you, then the commands whose `when` holds for the current session, then the places.
20. **Enter** opens a session or a place, or runs a command through `runCommand`, so a confirmation, the in-flight key and the toasts behave as on a button. A command whose `when` fails, or that is not `offline` while the snapshot is stale, is drawn dimmed and does nothing.

### The loopback origin

21. **The origin rule becomes a set:** the configured origin plus the listener's own loopback origins, `http://localhost:<port>` and `http://127.0.0.1:<port>`, or `http://[::1]:<port>` when it listens on IPv6, `<port>` being the port it bound. `guard` answers 421 to a `Host` outside the set and 403 to a write without exactly one `Origin` in it. A configured origin that is itself a loopback one is in the set once. No flag and no setting: the listener is loopback-only, so its loopback origins are known.
22. **The loopback origin grants nothing the tunnel's does not:** every route but `POST /api/v1/pair` still needs a paired device's cookie. DNS rebinding cannot present `localhost` or a loopback literal as its `Host`, and a page on another local port is another origin, refused. Browsers treat `http://localhost` as a secure context, so the `__Host-` cookies, the worker and Install App work there; each origin holds its own cookie, so the localhost browser and a tunnel browser are separate devices.
23. **Push is unchanged:** the VAPID subject stays the configured origin, so a desktop subscribed on localhost gets notifications while the configured origin is https; an http configured origin still sends none (`noteHTTPOrigin`). `loom web status` prints the local origin beside the configured one.

### `loom web open` and the pairing link

24. **`loom web open [--name <device>]`**, the name `desktop` by default: refused while the web is off ("The web app is off; turn it on with loom web enable."); when no device has the name it mints a code with `PairDevice` and opens `http://localhost:<port>/#/pair?code=<code>`; when the name is paired it opens `http://localhost:<port>/` with no code and says so ("desktop is already paired. If this browser is not, pass --name or run loom web revoke desktop."). The URL is always printed. The port is the bound address's (`WebView.Addr`).
25. **The opener** is `xdg-open` (Linux and the BSDs), `open` (macOS) or `rundll32 url.dll,FileProtocolHandler` (Windows), in `_unix.go` and `_windows.go` files, behind a seam the tests replace. An opener missing or failing (no display, over SSH) prints that the browser could not be opened, leaves the URL printed, and exits 0; a code minted stays good for its ten minutes.
26. **The app reads `#/pair?code=`:** `routeOf` gives the pair route a `code` search value. The pair panel takes the code and removes it from the address at once (`history.replaceState`, before any request), then runs `device.pair` with it only once the daemon has answered 401 (the unpaired signal); a live snapshot first means this browser is paired, so the code is dropped (it expires unused) and the fleet opens, as a pairing screen receiving a snapshot does today. A refused code leaves the pairing screen with the daemon's words as a toast. Remote desktops pair through the tunnel with `loom web pair`, as the phone does.

### Errors and edge cases

- **The desktop chunk fails to load:** the shell's error boundary shows what failed and a reload.
- **An inspector panel throws:** its boundary replaces the inspector only; the middle, the sidebar and the top bar keep working.
- **A key with no binding, or a binding while a text field has focus:** nothing happens, and the key reaches the field.
- **A stale snapshot:** view commands and the palette still work; session commands show dimmed and refuse, as buttons do.
- **`loom.desktop` unreadable** (private window, a bad value): the default layout.
- **The window crosses a breakpoint mid-dialog:** the open confirmation is dismissed (resolving null, as a replaced sheet does) and the new shell draws; the command does not run.
- **`loom web open` with the port taken, the web off, or no browser:** each said plainly; nothing guessed.
- **A pairing link opened in a browser that is already paired:** the code is dropped unused (§26).

### Testing

- **Vitest:** `useLayout` at each breakpoint and across a resize; the key filter (a text field, an open dialog, the palette, each modifier, `isComposing`, Shift letters, ⌘K from a field); each view command's order and ends (`j`/`k` stopping, `]`/`[` and `{`/`}` wrapping, a folded Paused skipped, the middle's tab kept when it applies); the inspector never showing the middle's tab, falling to the next, and its choice read back from `loom.desktop` (an unreadable value the default); the sidebar hidden on `#/`; `askConfirm` drawing a Dialog on desktop and a Drawer on the phone; the palette's score, grouping, code-point tie order, empty query, dimmed commands and Enter through `runCommand`; the pair code removed before any request and used only after a 401; Conversation, JobBody and FlowDocBody following the bottom and jumping inside a pane as on the window; the help listing every keymap entry.
- **Go:** the origin set (localhost, 127.0.0.1 and an IPv6 listener's [::1] accepted for `Host` and `Origin`; another port's loopback origin refused with 421 and 403; the configured origin still accepted; a loopback configured origin counted once); `loom web open` refusing while off, opening a URL with a code for a new name and without one for a paired name, always printing the URL, and reporting an opener failure with exit 0, through the opener seam; `loom web status` printing the local origin; `checkLazyViews` with `DesktopShell` (lazy, not preloaded); `GOOS=windows CGO_ENABLED=0 go build ./...` for the opener.
- **webshot:** `-device desktop` (1440×900) and `-device desktop-narrow` (1100×800), bare viewports with a desktop Chrome user agent, render every screen; a screen may name keys to `press` after load, for the palette (⌘K) and the help (`?`). `.github/workflows/web.yml` renders the phone and both desktop devices.
- **e2e:** `e2e/web_test.go` also pairs and streams over the loopback origin.
- **The desktop checklist (the owner's, after each part merges):** `loom web open` in Chrome on the daemon's machine pairs and opens the fleet; a second run says the name is paired; the frame at both widths; the keys and the help; the palette's three kinds; Install App; a waiting push received on the desktop; the tunnel URL from another machine, paired with `loom web pair`.

### Docs and rules

27. **`internal/web/ui/CLAUDE.md`** gains: a panel scrolls through `ScrollPane`, never the window; a body reads its session from its props, never the route; a key is bound only in `app/keymap.ts` to a registered command; view commands are `offline` (the amended rule, §16); desktop chrome is not a panel. **`internal/web/CLAUDE.md`** (198 bytes free) gains at most one line, that the origin rule is the configured origin plus the listener's loopback ones and grants nothing more, with its detail in `internal/web/README.md`. **`docs/claude/web-layer.md`** gains adding a key, adding a palette place, and the desktop checklist. **`USAGE.md`** gains `loom web open` and the desktop keys. Root `CLAUDE.md` does not change (19 bytes free). `docs/specs/web-api.md` and `api.fields` do not change: no route changes.

## Delivery

An epic of 2 parts, each a plan merged to main before the next part is designed.

1. **Part 1: reach and frame.** The loopback origin set and `loom web status` (§21–§23); `loom web open`, the opener and the pairing link (§24–§26); `useLayout`, the shell switch and the lazy `DesktopShell` (§4–§6); `ScrollPane` and the body-props rule (§12–§13); `LinkPill`, `GroupCounts` and `fleet/filter.ts`; the top bar, sidebar and inspector (§7–§10); the desktop Dialog and toasts (§11); webshot's desktop devices; e2e over loopback; the rules and docs for these. It ends with the desktop usable by mouse at both widths, reached on localhost.
2. **Part 2: keys and palette.** `app/keymap.ts`, `useKeys` and the view commands (§14–§16); the help (§17); the palette (§18–§20); webshot's `press` screens; the rules and docs for these. It ends with the TUI's keys and ⌘K working.
