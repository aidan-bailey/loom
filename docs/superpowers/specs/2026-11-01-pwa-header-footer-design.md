# Loom PWA: one header for every screen

**Date:** 2026-11-01
**Status:** Approved design. Brainstormed with the user. The mockups were
shown in a local browser session and are not kept; the sketches below
are the record.

## Problem

Each screen of the phone app draws its own top: the fleet a `loom@host`
heading with the gear and `+ new`, the other screens a `‹ back` link and an
h1. None of them is pinned, so the way back and the session's name scroll
away in a long conversation or diff. Only the fleet has a status line (the
link pill, the host, the session count), pinned at the bottom, so the link's
state and the fleet's attention vanish the moment a session opens; a stale
bar above every screen says only when the stream is down. A session's three
views (session, conversation, changes) reach each other only by going back,
and the changes screen can't reply.

This spec gives the app one pinned header, drawn by the app rather than
each screen: a status row on every screen, a context row per screen, and a
row of tabs for a session's three views. The only chrome at the bottom is
the reply box, on all three of a session's views.

It changes only `internal/web/static` and the docs: no Go, no route, no
field. Everything the header shows is already in the snapshot.

## Decisions

| # | Decision |
|---|---|
| 1 | **Layout B, status on top.** The pinned header is the status row, then the context row, then (on a session) the tabs. The bottom holds only the reply box, so typing gets the most room. Chosen over a status line at the bottom (A) and bottom tabs (C); see Alternatives. |
| 2 | **The status row is on every screen that has a snapshot**: the link pill, `loom@host`, and the counts ●, ◆ and ◐ of Needs you, Your turn and Working. It replaces the stale bar and the fleet's bottom status line. |
| 3 | **A count opens the next session in its group**, on its session tab, cycling on each tap: the phone's `]`. |
| 4 | **The whole header stays pinned**: no row hides on scroll. |
| 5 | **A session's views are tabs**: session, conversation, changes. Back goes to the fleet from all three, since they are siblings. |
| 6 | **The reply box is on all three tabs**, the changes tab included, and a draft survives a tab switch. |
| 7 | **The counts cover every shown workspace**, whatever the fleet's chip filter; the chip scopes only the fleet's body. |

## Layout

Every screen with a snapshot, top to bottom:

```
┌──────────────────────────────────────┐
│ [LIVE] loom@fresco        ●2 ◆1 ◐4   │  status row   (every screen)
│ ‹ fleet  fix-pane-cursor   claude ↗  │  context row  (per screen)
│  session │ conversation │ changes +12│  tabs         (a session's views)
├──────────────────────────────────────┤
│                                      │
│  the screen's body, scrolling        │
│                                      │
├──────────────────────────────────────┤
│ [yes] [continue] [/compact]          │  reply box    (a session's views,
│ [reply, or /command…        ] [send] │               while it can reply)
└──────────────────────────────────────┘
```

| Screen | Context row | Tabs | Bottom |
|---|---|---|---|
| Fleet | `Fleet`, the gear (`#settings`), `+ new` (off while stale) | none | none |
| Session | `‹ fleet`, the title, `claude ↗` | yes | reply box |
| Conversation | the same | yes | reply box |
| Changes | the same | yes | reply box |
| Settings | `‹ fleet`, `Settings` | none | none |
| New session | `‹ fleet`, `New session` | none | none |

Pairing, Add to Home Screen and the splash before the first snapshot have no
header: they have no host and no counts to show.

## The status row

`[PILL] loom@<host>`, then the three counts at the right edge. With no host,
`loom`. A long host truncates with an ellipsis; the counts never do.

**The pill** is the link's state, replacing the stale bar's words:

| State | When | Pill | Row |
|---|---|---|---|
| live | the stream is up | `LIVE` on `--ok` | counts in their colours |
| connecting | not live, no connect has failed yet, no bye (a cold start shows the cached snapshot here) | `CONNECTING` on `--dim` | counts faint |
| restarting | a bye within `restartGrace` (30 s) | `RESTARTING` on `--dim` | counts faint |
| offline | a connect failed | `OFFLINE` on `--error` | ` · 14:02` (the last update, as the stale bar gave it) after the host when there was one; the row's bottom border `--error-border`; counts faint |

`app.mjs`'s `link` gains `connecting` (today `offline` covers it). The pill
carries `aria-live="polite"`, as the stale bar's `role="status"` did.

**The counts** are the lengths of `fleetGroups(snap)`'s groups: the sessions of `shownWorkspaces`,
grouped as the fleet groups them (`groupSessions(…, "")`). Each is a button
with its glyph and number, coloured as the fleet's groups are (● amber, ◆
accent, ◐ plain), with an `aria-label` such as "2 need you: open the next".
A zero count is faint and not a button. While not live the counts are the
cached snapshot's, drawn faint and still buttons: they navigate and act on
nothing. The ● count is the icon badge's count: the daemon's badge counts
needs-you sessions, and a workspace with a session that isn't paused is
always shown.

**A tap** goes to `#s/<id>` of `nextInGroup(fleetGroups(snap), group, currentId)`:
the session after the current one in the group's order as it stands at the
tap (Needs you longest wait first, the others newest first), wrapping to the
first; the first when the current screen is no session or its session is
not in the group. The order is read at each tap, so a session whose `since`
changed between taps may come round early. It always opens the session tab.

**Size.** The row is 32 px tall and each count's hit area at least 40 px
wide. At 320 px the worst case, `RESTARTING loom@fresco ●12 ◆3 ◐14`, fits
with the host truncated.

## The context row

`ContextRow({back, title, children})`: an optional `‹ <label>` link, the
title as the page's `h1` on one line with an ellipsis, then the screen's
actions. It is 44 px tall (the back link's touch target, `.sc-back` today).
The screens' own `h1`s go: the fleet's `loom@host` heading (the host is in
the status row), the session's wrapped title, and Conversation, Changes,
Settings and New session.

The session's context row is the same on all three tabs: `‹ fleet`, the
title, and `claude ↗` under today's rule (`remote_url` set and the session
not in Needs you, whose box has its own Open in Claude button). While the
session is not in the snapshot it shows `‹ fleet` and `Loading…` (pending)
or no title, and no tabs.

## The tabs

`SessionTabs({s, route})`, under the context row on the three routes that
already exist: `#s/<id>` session, `#s/<id>/turns` conversation, `#s/<id>/diff`
changes. A tab is shown when it applies: conversation for `s.claude`,
changes for `actionsFor(s).diff`. The changes tab's label ends in
`+added −removed` from the session's `diff` when either is non-zero. **The current tab is always
shown**, so a session that pauses or turns busy on the changes tab keeps its
tab under the user. With only the session tab left, the row is not drawn.
The current tab is underlined with `--accent` and carries
`aria-current="page"`.

The ways in from the body stay: `conversation ›` beside the last message and
the Changes tile.

## The bottom

The fleet's status line (`sc-status`) goes, and with it the fleet's bottom
padding. The reply box (`ReplyBox`, `sc-reply`, sticky at the bottom) is on
all three tabs while `actionsFor(s).send`; a paused session's tabs say
"Resume the session to reply." as the session screen does today.

**Drafts.** A route change remounts the screen, so `reply.mjs` keeps each
session's unsent text in a module-level map by session id, read when the box
mounts and cleared when a send answers 2xx other than 202 (today's rule for
emptying the box). It lives as long as the page; a reload loses it.

**One runner.** The session and conversation screens each wrap `act` with a
busy flag and the 202 toast (`run`); the changes screen would make three.
`reply.mjs` exports `useRun(act, toast)`, returning `{busy, run}`, and all
three use it.

## Toasts

Toasts drop from the top today, past the safe area. Under a pinned header
they would cover the status row, so the toast stack moves inside the header
element, positioned at `top: 100%` with an 8 px gap: it hangs from the
header's bottom edge whatever its height (two rows on the fleet, three on a
session). Setting a measured height would need a `style` attribute, which
the CSP drops. Their behaviour is unchanged.

## Architecture

| File | Change |
|---|---|
| `js/chrome.mjs` (new) | `StatusRow`, `ContextRow`, `SessionTabs`, `FLEET_BACK`; it imports only `skin.mjs` |
| `js/app.mjs` | Renders `<header class="sc-chrome">` (the status row, the route's head, the toasts) above `<main>` (the route's screen). Drops the stale bar; `staleText`'s offline time moves to the status row; `link` gains `connecting`; `lastUpdated` reaches the row as a prop |
| `js/screens.mjs` | `name → {Screen, Head}`. Heads: `FleetHead` (`fleet.mjs`), `SessionHead` (`session.mjs`, for `session`, `turns` and `changes`), `SettingsHead`, `NewHead` |
| `js/fleet.mjs` | Drops `sc-header`, the `h1`, `sc-status` and `LINK`; exports `FleetHead`; adds `fleetGroups(snap)`, `nextInGroup(groups, group, currentId)` and `sessionOf(snap, id)`, pure (here rather than in `chrome.mjs`, which `fleet.mjs` imports) |
| `js/session.mjs`, `js/turns.mjs` | Drop `sc-topbar` and the `h1`; use `useRun` |
| `js/changes.mjs` | Drops `sc-topbar` and the `h1`; adds the reply box (or the paused line) through `useRun` |
| `js/settings.mjs`, `js/newsession.mjs` | Drop `sc-topbar` and the `h1`; export their heads |
| `js/reply.mjs` | Drafts by session id; `useRun` |
| `app.css` | `.sc-chrome`: `position: sticky; top: 0`, padding `env(safe-area-inset-top)` itself so it fills behind the iOS status bar, `--bg` with the `--rule` bottom border; `.shell` drops its top inset (only while the header is drawn: `.sc-chromed`); `main` becomes a flex column and a session's views fill it with `flex: 1` instead of a `100dvh` min-height, which the header would add to; the status row, pill states, counts, context row and tabs; `.toasts` inside the header; `.sc-group-title`'s `scroll-margin-top` clears the fleet's header (status and context rows plus the top inset) so a tile's jump lands below it. Removes `.stale-bar`, `.sc-status`, `.sc-header`, `.sc-topbar` and `.sc-fleet`'s bottom padding |

A head takes the screen's props (`{snap, stale, link, route, go, act, toast,
pending}`) and finds its session by `route.id` as the screen does
(`allSessions(snap).find`). The conversation's scroll anchoring measures the
document, so the pinned header leaves it as it is.

## Failure modes

- **A count's session is gone by the time it opens** (a stale snapshot, or
  a kill between render and tap): the session screen says "This session is
  gone.", as it does today for any missing session.
- **A group empties between render and tap**: `nextInGroup` returns nothing
  and the tap does nothing; the next render drops the button.
- **A route to a tab that doesn't apply** (an old link to `/turns` on a
  session that isn't Claude): the screen behaves as today, and the tab row
  shows that tab because it is the current one.
- **The keyboard is open on iOS**: the header and the reply box are both
  sticky; if iOS scrolls the header away while the textarea has focus, that
  is accepted for this spec and noted on the phone checklist, not worked
  around.

## Testing

There is no JS test runner and CI has no Node, as today.

- `for f in internal/web/static/js/*.mjs; do node --check "$f"; done`.
- `CGO_ENABLED=0 go test ./internal/web/...`: `TestStatic_ModulesSetNoStyleAttribute`,
  `TestStatic_EveryReferenceResolves`, `TestStatic_NamedImportsAreExported`
  and `TestStatic_TheStylesheetHasNoFenceAndItsBracesBalance` hold the new
  module and CSS to the CSP and the import rules.
- `nextInGroup`, `fleetGroups` and `sessionOf` get a throwaway Node script
  that imports `fleet.mjs` and asserts: next after the current, the wrap, the first from
  outside the group, an empty group, unshown workspaces left out, a group
  the build doesn't know counted as Paused (not in any count). Kept outside
  the worktree; nothing in CI would run it.
- Headless Chromium at 390×844 with a canned snapshot, as
  `docs/claude/web-layer.md` describes: the fleet, each of a session's three
  tabs, a session with no tabs, Settings, New session, the four pill states,
  a toast under a two-row and a three-row header, and the status row at
  320 px with a long host.
- On the iPhone: the header fills behind the status bar in standalone mode;
  the header and the reply box with the keyboard open; a tile's jump on the
  fleet lands below the header; the count cycle; a draft surviving a tab
  switch; toasts below the header.

## Work packages

1. **The header.** `chrome.mjs`'s status row and context row, `fleet.mjs`'s
   `fleetGroups` and `nextInGroup`; `app.mjs` drawing the header with the toasts; heads in
   `screens.mjs`; every screen's own top, the stale bar and the fleet's
   status line removed; the CSS. USAGE.md's phone section and
   `internal/web/README.md`'s skin paragraph.
2. **The tabs and the reply box everywhere.** `SessionTabs`; `useRun`; the
   reply box on the changes tab; drafts by session. The docs' lines on the
   conversation and the changes.

Each ends with the testing above for what it touched.

## Alternatives considered

- **A: the status line at the bottom**, under the reply box, as in the
  TUI. Most like the TUI and one fixed place for the status; it costs the
  most height at the bottom of a session's views, where the keyboard and
  the reply box need it.
- **C: tabs at the bottom**, under the reply box, in thumb reach, with the
  needs-you count as a badge on the back link. Two bars at the bottom, and
  the status split between the top and the bottom.
- **Hiding rows on scroll** (the status row, or the whole header, coming
  back on a scroll up): more room to read, at the cost of scroll listeners
  and jumpiness on iOS's rubber-banding. The pinned header was chosen.
- **A count opening the fleet at its group**, or **counts for display
  only**: the cycle does what the TUI's `]` does from any screen.

## Out of scope

- Swiping between tabs; keeping each tab's scroll position.
- A tap on the pill to reconnect now.
- Drafts that survive a reload.
- The TUI's chrome.
