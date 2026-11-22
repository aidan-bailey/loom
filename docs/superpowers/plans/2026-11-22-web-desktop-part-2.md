# Loom web desktop, part 2: the palette — Implementation Plan

> **For agentic workers:** this plan runs supervised: `/flow:draft` writes the stories from this skeleton, then `/flow:run` runs them story by story. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the desktop layout a ⌘K palette: one box that finds any session, runs a session's or the view's commands, and opens a place (settings, the host's flows, new session, the fleet, the current session's tabs), opened by ⌘K or Ctrl+K from anywhere and by the top bar's box.

**Architecture:** The command registry gains a `palette` mark and a list accessor, and two offline view commands that toggle the sidebar and the inspector register from the desktop chunk. A pure module (`desktop/palette.ts`) builds the palette's grouped, scored and ordered items from the snapshot, the registry and the route's command context; a Base UI Dialog around an inline Base UI Autocomplete draws them, and Enter navigates or runs the command through `runCommand`. One document listener in `DesktopShell` opens it on the chord. webshot gains a desktop-only screen that presses the chord after load. Everything but two small helpers lives in the lazy desktop chunk, so the phone's first load is unchanged. No route under `/api/v1`, wire, Core request or `rpc.Protocol` change. The keys of spec §14–§17 are out of this epic (spec, Part 2 settlements).

**Tech Stack:** TypeScript, React 19, Vite, Tailwind CSS 4, shadcn/ui on Base UI 1.9 (Dialog, Autocomplete), Zustand 5, Vitest with happy-dom and Testing Library; Go (testify) for `tools/webshot` (headless Chromium over CDP).

**Spec:** `docs/superpowers/specs/2026-11-21-web-desktop-design.md`

**Part:** 2 of 2

**Budget:** draft $18; run $14 (estimate 2026-11-22)

## File structure

The new app (`internal/web/ui/src/`):

- `app/commands.ts` (changed: the `palette` mark, `allCommands`); `commands/session.ts`, `commands/push.ts`, `commands/device.ts` (changed: the mark on their no-args commands); `commands/view.ts` (new: the two view toggles); `commands/palette.test.ts` (new: the palette set).
- `app/ConfirmSheet.tsx` (changed: `confirmOpen`).
- `fleet/groups.ts` (changed: `hasShownFlows`); `panels/fleet/FleetHead.tsx` (changed: `FleetLinks` calls it).
- `desktop/palette.ts` (new: the items, the score and the order, pure).
- `desktop/paletteState.ts` (new: the open store, the chord and its listener, the chord's label); `desktop/Palette.tsx` (new: the dialog); `desktop/TopBar.tsx` (changed: `PaletteBox`); `desktop/DesktopShell.tsx` (changed: registers the view toggles, draws the palette, binds the chord).
- `*.test.ts(x)` beside each module they test.

The built bundle: `internal/web/dist/` (rebuilt by every story that changes the app, committed).

Elsewhere: `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `tools/CLAUDE.md` (the palette screen); `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md`, `internal/web/README.md` (the rules and docs).

## Names contract

Every name one story defines and another uses. A story may add names of its own; a name another story needs and this section lacks goes back to the manager. Paths are under `internal/web/ui/src/` unless they start at the repo root.

**The registry and the view toggles (Story 1):**

- The `Command` interface in `app/commands.ts` gains the optional field `palette?: boolean` (Story 1): true only on a command that takes no args; the palette lists exactly the registered commands that set it.
- `function allCommands(): Command<unknown>[]` (Story 1), in `app/commands.ts`: every registered command, in definition order (a redefined id keeps its first place, as the registry's `Map` does).
- The palette set (Story 1): `session.pause`, `session.resume`, `session.push`, `session.merge` and `session.kill` (`commands/session.ts`), `push.enable` (`commands/push.ts`), `device.unpair` (`commands/device.ts`), `app.backToClassic` (`app/commands.ts`), `view.toggleSidebar` and `view.toggleInspector` (`commands/view.ts`). `commands/palette.test.ts` (Story 1) imports `@/commands` and `@/commands/view` and pins the set exactly.
- `commands/view.ts` (Story 1): `view.toggleSidebar` titled `Show or hide the sidebar` and `view.toggleInspector` titled `Show or hide the inspector`, each `offline: true`, `palette: true` and `when: () => true`; `run` sets `useDesktop`'s `sidebar` (or `inspector`) to the opposite of its current value through `setSidebar` (or `setInspector`). `desktop/DesktopShell.tsx` imports it for its registration (Story 1 adds the import); `commands/index.ts` never does, so the phone's first load does not carry it.
- `function confirmOpen(): boolean` (Story 1), in `app/ConfirmSheet.tsx`: true while an `askConfirm` sheet waits for its answer, false once it is answered.
- The destructive ask's focus (Story 1), in `app/ConfirmSheet.tsx` (spec, decision 15): an ask whose spec is `destructive` opens its Dialog with focus on its Cancel button; any other ask keeps the Dialog's default focus. Story 3's palette relies on it when Enter opens a confirmation.

**The palette's items (Story 2)**, in `desktop/palette.ts`, which imports no React:

- `function fuzzyScore(query: string, text: string): number | null` (Story 2): the spec's Part 2 settlements, decision 7, its five examples each a test.
- `function compareCodePoints(a: string, b: string): number` (Story 2): negative when `a` sorts first by code point (`codePointAt`, not UTF-16 units), 0 when equal, positive otherwise; never `localeCompare`. `compareCodePoints("｡", "\u{1F600}")` is negative, where `<` on the strings says the opposite.
- `function hasShownFlows(snapshot: Snapshot): boolean` (Story 2), in `fleet/groups.ts`: true when a session of a shown workspace (`shownWorkspaces`) has a flow; `FleetLinks` (`panels/fleet/FleetHead.tsx`) calls it instead of its own selector.
- `interface PaletteTab { id: PanelId, label: string }` (Story 2): a tab panel of the current session.
- `interface PaletteSession { kind: "session", key: string, title: string, hint: string, href: string }` (Story 2): `key` is `session:<id>`, `hint` the workspace's name (`label || name`) and the branch joined by ` · ` with an empty one left out, `href` the session's `sessionHref` link.
- `interface PaletteCommand { kind: "command", key: string, title: string, command: Command<unknown>, enabled: boolean }` (Story 2): `key` is `command:<id>`, `title` the command's title (a function title called with the context), `enabled` what `commandEnabled` answers for the command and the context.
- `interface PalettePlace { kind: "place", key: string, title: string, hint: string, href: string }` (Story 2): `key` is `place:<panel id>`; a tab's `title` is its label, its `hint` the session's title, its `href` the tab panel's `hrefFor` link with the session's id; Fleet, New session, Flows and Settings have the titles `Fleet`, `New session`, `Flows` and `Settings` exactly (Story 3's tests read them), an empty `hint` and `hrefFor("fleet")`, `hrefFor("new")`, `hrefFor("flows")` and `hrefFor("settings")`.
- `type PaletteItem = PaletteSession | PaletteCommand | PalettePlace` (Story 2).
- `interface PaletteGroup { label: "Sessions" | "Commands" | "Places", items: PaletteItem[] }` (Story 2).
- `interface PaletteInput { query: string, snapshot: Snapshot | null, commands: Command<unknown>[], ctx: CommandContext, tabs: PaletteTab[] }` (Story 2): `commands` every registered command (`allCommands()`), narrowed here to those with `palette`; `tabs` the current session's tab panels in tab order (`sessionTabs(ctx.session)` as `{ id, label }`, empty with no session).
- `function paletteGroups(input: PaletteInput): PaletteGroup[]` (Story 2): the items and their order as the Part 2 settlements' decisions 8 and 9 say, a group with no items left out; a null snapshot gives no sessions and no Flows place.

**The palette (Story 3)**, modules of the desktop chunk:

- `interface PaletteState { open: boolean, setOpen: (open: boolean) => void, toggle: () => void }` and `const usePalette: UseBoundStore<StoreApi<PaletteState>>` (Story 3), in `desktop/paletteState.ts`: in memory, closed at load.
- `function isPaletteChord(e: KeyboardEvent): boolean` (Story 3), in `desktop/paletteState.ts`: `metaKey` or `ctrlKey`, not `altKey`, not `shiftKey`, not `isComposing`, and `key` `k` or `K`.
- `function usePaletteKey(): void` (Story 3), in `desktop/paletteState.ts`: one document `keydown` listener, added in an effect; on the chord it calls `preventDefault()`, then does nothing while `confirmOpen()` (Story 1), else calls `usePalette.getState().toggle()`, whatever has focus. The effect's cleanup removes the listener and closes the palette through `setOpen` with false, so a palette open when `DesktopShell` unmounts (the window narrowing below the desktop breakpoint) is closed when the desktop layout comes back. `DesktopShell` calls it before its `mockupFull` early return, so the chord works on every route.
- The case title `toggles the palette on the chord from a text field` (Story 3), in `desktop/paletteState.test.ts`: Story 5's rule on the palette's chord names it as the test that enforces the rule.
- `function paletteKeyLabel(platform: string): string` (Story 3), in `desktop/paletteState.ts`: `⌘K` when `platform` matches `/mac|iphone|ipad/i`, else `Ctrl K`.
- `function PaletteBox(): ReactElement` (Story 3), in `desktop/TopBar.tsx`: a `button` with `data-slot="palette-box"`, the text `Search sessions, commands, places` and `paletteKeyLabel(navigator.platform)`; a click opens the palette through `usePalette`'s `setOpen`. `TopBar` draws it after `HostLabel` and before `WorkspaceFilter`.
- `function Palette(): ReactElement` (Story 3), the named export of `desktop/Palette.tsx`: a `Dialog` (`components/ui/dialog.tsx`) open while `usePalette().open`, its popup carrying `data-slot="palette"`, around a Base UI `Autocomplete.Root` (imported from `@base-ui/react/autocomplete`; no wrapper under `components/ui/`) that is `inline` and `open` with `filter={null}` and `autoHighlight`, its `items` the `paletteGroups` (Story 2) for the query; the input carries `data-slot="palette-input"` and each item `data-slot="palette-item"` and `data-kind` (its kind); a command that is not `enabled` is a `disabled` item; an item's `onClick` (a click, or Enter while highlighted) acts as the Part 2 settlements' decision 10 says; each opening starts with an empty query; Esc closes it, also from the input with a non-empty query. `DesktopShell` draws it on both branches: inside the frame, and beside the `Outlet` on its `mockupFull` return, so the store and the dialog never disagree.

**webshot (Story 4)**, in `tools/webshot/main.go`:

- `type screenMode struct{ unpaired, browserTab, desktopOnly bool; press, drawn string }` (Story 4): `press` a chord pressed once the app is drawn, in the form `Control+k` (prefixes `Alt+`, `Control+`, `Meta+`, `Shift+`), `drawn` a selector that must match after the press. `screens` gains `{"palette", "#s/1"}` and `screenModes` gains `"palette": {desktopOnly: true, press: "Control+k", drawn: "[data-slot=\"palette\"]"}` (Story 4).
- `forDevice` (Story 4) drops `desktopOnly` screens on a phone and `browserTab` screens on a desktop; `TestForDevice_DropsOnlyAddToHomeOnADesktop` becomes `TestForDevice_DropsAddToHomeOnADesktopAndThePaletteOnAPhone`, and `tools/CLAUDE.md`'s webshot device rule names the new test.
- The classic app (Story 4): `run` also skips a `desktopOnly` screen unless `-app` is `next` (the classic app has no desktop layout, and `-app` defaults to `classic`), so `-device desktop` with every screen still passes on the classic app. Each skip line names its reason: `skipped: phone only` (a `browserTab` screen on a desktop), `skipped: desktop only` (a `desktopOnly` screen on a phone), `skipped: next app only` (a `desktopOnly` screen under `-app classic`); a test pins each.
- `func (p *page) press(ctx context.Context, chord string) error` (Story 4): `Input.dispatchKeyEvent` `keyDown` then `keyUp` with the chord's modifiers (Alt 1, Control 2, Meta 4, Shift 8). For a screen with `press`, `shoot`, after its usual wait for `appDrawn` and settle, waits up to 5 s for the desktop frame (`[data-slot="desktop"]`, drawn by the lazy `DesktopShell`), failing the screen with `no desktop frame to press <chord> on`; settles again, so `DesktopShell`'s effects (the chord's listener) have run; presses `press`; waits up to 5 s for `drawn` to match, failing the screen with `pressing <chord> drew no <drawn>`; then settles before the capture. It presses once: a second press of a toggling chord closes what the first opened.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): `desktop/DesktopShell.tsx` as Story 1 leaves it (Story 3); `app/commands.ts` as Story 1 leaves it (Stories 2 and 3).

**For every story:** each test's `Fails without:` line names a change to the code that the test, as written, really catches when run: part 1's three plan problems were claims no test could catch (a probe that never read the value it claimed to, a `grep -c` that counted lines rather than matches). A test that reads the DOM reads the attribute or text it claims to.

**Design stage:** /flow:draft session f9fa5538-e16d-462c-ad5d-ff35c526a198, 2026-11-22T08:32:12Z to 2026-11-22T09:24:16Z UTC: $12.45 logged, $12.55 with estimated thinking; 917 plan lines, $13.57 per 1,000 lines; skeleton review $1.68; risk review $0.00; budget $18; forms: brief 5 stories $10.77; loom flow roster 0e25f6995f89.

## Stories

- **Story 1** — The registry's palette mark, the two view toggles, and the confirmation's open state.

  Add `palette?: boolean` to `Command` and `allCommands()` to the registry (spec, Part 2 settlements, decision 4), and set the mark on the eight no-args commands registered today. Add `commands/view.ts` with `view.toggleSidebar` and `view.toggleInspector`, `offline`, flipping `useDesktop`'s visibility, registered by an import in `desktop/DesktopShell.tsx` and never from `commands/index.ts` (decision 5). Export `confirmOpen()` from `app/ConfirmSheet.tsx` for Story 3's listener, and open a `destructive` ask's Dialog with focus on its Cancel button (spec, decision 15). Tests pin `allCommands`' order, the palette set exactly (`commands/palette.test.ts`), each toggle flipping its value and running while the snapshot is stale, the shell's module registering the toggles, `confirmOpen` across an ask, its answer and a replacing ask, and a destructive ask's Dialog focusing Cancel while a non-destructive one does not.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/app/commands.test.ts`, `internal/web/ui/src/commands/session.ts`, `internal/web/ui/src/commands/push.ts`, `internal/web/ui/src/commands/device.ts`, `internal/web/ui/src/commands/view.ts`, `internal/web/ui/src/commands/view.test.ts`, `internal/web/ui/src/commands/palette.test.ts`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `Command.palette`, `allCommands`, the palette set, `view.toggleSidebar`, `view.toggleInspector`, `confirmOpen`; uses `defineCommand`, `commandById`, `runCommand`, `useDesktop`, `askConfirm`, `testSeam`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (a field, a list accessor and two toggles over an existing store, each pinned by a named test)
  - **Form:** brief (plumbing a mark and an accessor, and two small commands)
  - **Reading:** spec §16, §18, Part 2 settlements (decisions 3–5, 15); `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/commands/session.ts:55-183`, `internal/web/ui/src/commands/push.ts:95-116`, `internal/web/ui/src/commands/device.ts:70-110`, `internal/web/ui/src/app/ConfirmSheet.tsx:55-130`, `internal/web/ui/src/app/desktopState.ts`, `internal/web/ui/src/commands/testkit.ts`, `internal/web/ui/src/commands/index.test.ts`, `internal/web/ui/src/desktop/DesktopShell.tsx`

- **Story 2** — The palette's items, score and order, as a pure module.

  Add `desktop/palette.ts` (spec §18–§19, Part 2 settlements, decisions 7–9): `fuzzyScore`, `compareCodePoints`, and `paletteGroups`, which builds the Sessions, Commands and Places groups from the snapshot, the registry and the command context. A non-empty query keeps what matches, ordered by score, then by title, then by key, by code point; an empty query lists the sessions that need you (the longest wait first), the palette commands whose `when` holds, and every place in list order. Move `FleetLinks`' flows rule into `hasShownFlows` in `fleet/groups.ts`, so the Flows place and the top bar's link share it. Tests pin each of decision 7's examples, the best alignment over every start, a null for no match, the code-point tie order, the three groups' order and their absence when empty, sessions matched on branch and workspace name, a command not `enabled` while stale, the Flows place following `hasShownFlows`, and the current session's tabs as places.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/palette.ts`, `internal/web/ui/src/desktop/palette.test.ts`, `internal/web/ui/src/fleet/groups.ts`, `internal/web/ui/src/fleet/groups.test.ts`, `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `fuzzyScore`, `compareCodePoints`, `hasShownFlows`, `PaletteTab`, `PaletteSession`, `PaletteCommand`, `PalettePlace`, `PaletteItem`, `PaletteGroup`, `PaletteInput`, `paletteGroups`; uses `Command.palette`, `allCommands` (Story 1), `commandEnabled`, `CommandContext`, `allSessions`, `groupSessions`, `shownWorkspaces`, `sessionHref`, `hrefFor`, `sessionTabs`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (a pure function whose every rule is a named test; Task 2.2, the uncapped quadratic score, stays brief by spec decisions 7 and 17)
  - **Reading:** spec §18–§19, Part 2 settlements (decisions 7–9); `internal/web/ui/src/fleet/groups.ts`, `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `internal/web/ui/src/fleet/links.ts`, `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/routes.tsx:100-200`, `internal/web/ui/src/app/commands.ts:20-90`, `internal/web/ui/src/commands/testkit.ts`, `internal/web/ui/src/fixtures.ts` (`fixtureSnapshot`)
  - **Drafts after:** Story 1

- **Story 3** — The palette dialog, its chord, and the top bar's box.

  Add `desktop/paletteState.ts` and `desktop/Palette.tsx` (spec §18, §20, §7; Part 2 settlements, decisions 2, 6, 10, 11): a Dialog around an inline Base UI Autocomplete over `paletteGroups`, a command not enabled drawn dimmed and inert, Enter or a click opening a session or a place, or closing the palette and running the command through `runCommand` with the route's context. `DesktopShell` draws it on both its branches (the frame and the full mockup) and binds the chord before its early return; the chord toggles the palette from anywhere, a text field included, and does nothing while a confirmation is open; unmounting the shell closes the palette. The top bar gains `PaletteBox`, with the chord's label for the platform. Tests pin the chord from a text field, its refusals (Alt, Shift, composing, a confirmation open), a second chord closing, Esc from the input with a query closing, the chord and the palette on the full mockup, the palette closed after the shell unmounts, the box opening it, an empty query on reopening, Enter on a session and a place navigating, Enter on a command running it through `runCommand` after the palette closes, a stale snapshot's session commands dimmed and inert while the view toggles still run, and both labels.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/paletteState.ts`, `internal/web/ui/src/desktop/paletteState.test.ts`, `internal/web/ui/src/desktop/Palette.tsx`, `internal/web/ui/src/desktop/Palette.test.tsx`, `internal/web/ui/src/desktop/TopBar.tsx`, `internal/web/ui/src/desktop/TopBar.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `PaletteState`, `usePalette`, `isPaletteChord`, `usePaletteKey`, `paletteKeyLabel`, `PaletteBox`, `Palette`, the slots `palette`, `palette-input`, `palette-item` and `palette-box`; uses `paletteGroups`, `PaletteItem`, `PaletteGroup` (Story 2), `allCommands`, `confirmOpen` (Story 1), `useCommandContext`, `runCommand`, `useFleet`, `sessionTabs`, `Dialog`, `DialogContent`, `DialogTitle`, `Autocomplete`, `TopBar`, `HostLabel`, `WorkspaceFilter`, `setViewportWidth`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (a dialog over a pure module and a third-party list; each behaviour a named test; Task 3.1, the chord listener refusing during a confirmation and its cleanup closing the palette, and Task 3.2, the palette closing before `runCommand` opens the confirmation, stay brief by spec decisions 16 and 17)
  - **Reading:** spec §7, §18, §20, Errors (a stale snapshot), Part 2 settlements (decisions 2, 6, 10, 11); `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/TopBar.tsx`, `internal/web/ui/src/components/ui/dialog.tsx`, `internal/web/ui/src/app/commands.ts:60-200`, `internal/web/ui/src/desktop/DesktopShell.test.tsx:1-80`, `internal/web/ui/src/desktop/TopBar.test.tsx`, Base UI's Autocomplete types (the `autocomplete` directory of the installed `@base-ui/react` package, after the install), `docs/superpowers/specs/2026-11-21-web-desktop/frame.html` (the box)
  - **Drafts after:** Story 1, Story 2

- **Story 4** — webshot renders the palette at both desktop sizes.

  Add the desktop-only `palette` screen to webshot (spec Testing; Part 2 settlements, decision 12): `screenMode` gains `desktopOnly`, `press` and `drawn`; `shoot` waits for the desktop frame and settles, presses the chord once through CDP, and fails the screen when the frame or the palette's slot never appears; a phone skips the screen as a desktop skips `add-to-home`, the classic app skips it too, and each skip line names its reason. `.github/workflows/web.yml` already renders every screen at the phone and both desktop sizes and does not change. Tests pin the chord's parsing into a key and modifiers, the screen's mode, `forDevice` on both kinds of device, the classic app's skip and each skip line's reason, and, in the real-browser run (only with `LOOM_WEBSHOT_BROWSER`), `palette.png` written at both desktop sizes and absent on the phone. Update `tools/CLAUDE.md`'s device rule for the renamed test and the desktop-only screen.

  - **Developer:** intermediate-dev
  - **Files:** `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `tools/CLAUDE.md`
  - **Interfaces:** exposes the `palette` screen, `screenMode`'s `desktopOnly`, `press` and `drawn`, `page.press`; uses the `palette` slot and the chord (Story 3), `forDevice`, `shoot`, `page.query`, `cdpClient.call`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ ./tools/claudemd/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -screens palette -out "$d/w" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -screens palette -out "$d/n" && test -s "$d/w/palette.png" && test -s "$d/n/palette.png"'`
  - **Review:** light (a test tool's screen and a key press)
  - **Form:** brief (a screen entry, a mode and a CDP call; Task 4.2, the frame wait, the single press and the slot wait against the browser, stays brief by spec decisions 16 and 17)
  - **Reading:** spec Testing (webshot), Part 2 settlements (decision 12); `tools/webshot/main.go:56-100`, `tools/webshot/main.go:286-302`, `tools/webshot/main.go:485-643`, `tools/webshot/cdp.go`, `tools/webshot/webshot_test.go:740-970`, `tools/CLAUDE.md` (the webshot device rule)
  - **Drafts after:** Story 3

- **Story 5** — The rules and the procedures for the palette.

  Amend `internal/web/ui/CLAUDE.md`'s offline rule to name the two view toggles, and add the rules that a command sets `palette` only when it takes no args and that the palette's chord is the app's only document key listener, bound in `DesktopShell` (spec §27 as the Part 2 settlements re-cut it, decision 13). Add to `docs/claude/web-layer.md` adding a palette command and a palette place, amend its "Adding a command" sentence on `offline`, correct its lines that say the desktop devices skip only `add-to-home` (a phone now skips the desktop-only `palette` screen, and the classic app skips it), and make its desktop checklist's item 8 the palette's. Add the palette to `USAGE.md`'s desktop paragraph and `internal/web/README.md`'s desktop layout, and the palette command and place to `docs/claude/INDEX.md`'s row for `web-layer.md`. Each rule names the test that enforces it or says Convention, and the CLAUDE.md lint passes. `internal/web/CLAUDE.md` and root `CLAUDE.md` do not change.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `USAGE.md`, `internal/web/README.md`
  - **Interfaces:** none
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd'`
  - **Review:** light (docs)
  - **Form:** brief (docs)
  - **Reading:** spec §27, Part 2 settlements (decision 13); `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md:35-80`, `docs/claude/web-layer.md:113-125`, `USAGE.md:820-845`, `internal/web/README.md:105-110`
  - **Drafts after:** Story 1, Story 2, Story 3, Story 4

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->

## Story 1: The registry's palette mark, the two view toggles, and the confirmation's open state

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/app/commands.test.ts`, `internal/web/ui/src/commands/session.ts`, `internal/web/ui/src/commands/push.ts`, `internal/web/ui/src/commands/device.ts`, `internal/web/ui/src/commands/view.ts`, `internal/web/ui/src/commands/view.test.ts`, `internal/web/ui/src/commands/palette.test.ts`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `Command.palette`, `allCommands`, the palette set, `view.toggleSidebar`, `view.toggleInspector`, `confirmOpen`; uses `defineCommand`, `commandById`, `runCommand`, `useDesktop`, `askConfirm`, `testSeam`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** light (a field, a list accessor and two toggles over an existing store, each pinned by a named test)
- **Form:** brief (plumbing a mark and an accessor, and two small commands)

This story gives Stories 2 and 3 what they read and does nothing visible by itself. The `Command` interface gains the optional `palette` mark and the registry gains `allCommands()`, so the palette lists exactly the registered commands that set the mark: the eight no-args commands registered today get it (spec, Part 2 settlements, decision 4). `commands/view.ts` adds the two offline view toggles, `view.toggleSidebar` and `view.toggleInspector`, which flip `useDesktop`'s visibility and are registered by an import in `desktop/DesktopShell.tsx`, never from `commands/index.ts`, so the phone's first load is unchanged (decisions 3 and 5). `app/ConfirmSheet.tsx` exports `confirmOpen()`, which Story 3's chord listener reads to do nothing while a confirmation waits, and opens a `destructive` ask's Dialog with focus on its Cancel button, because the palette's Enter opens a command's confirmation from the keyboard and the desktop Dialog lists the action before Cancel (decision 15). The tasks run in this order so the tree is green after each: the accessor, the toggles, the marks and the pinned set, `confirmOpen`, the focus, then the rebuilt bundle. No code here holds a landmine marker, so the developer runs at the default effort. Every command below is run from the repo root.

### Task 1.1: `Command.palette` and `allCommands`

**Files:**
- Modify: `internal/web/ui/src/app/commands.ts` (the `Command` interface, near `defineCommand`)
- Test: `internal/web/ui/src/app/commands.test.ts` (a new `describe("allCommands", …)` after `describe("the registry", …)`)

- [ ] **Step 1: Write the failing test.** In `app/commands.test.ts` add `allCommands` to the import from `./commands`, and add one `describe("allCommands")` with these tests. The file's `ctx()` helper and its `vi.mock` of `./ConfirmSheet` and `./switchApp` stay as they are.
  - `lists every registered command in definition order, and a redefined id keeps its first place`: define `test.order.a`, `test.order.b` and `test.order.c` (each `when: () => true`, `run: () => Promise.resolve()`), then define `test.order.a` again as a new object. Take `allCommands().filter((c) => c.id.startsWith("test.order."))`. Assert its ids are `["test.order.a", "test.order.b", "test.order.c"]` and that its first element `toBe` the second definition of `a` (the replacement, in the first's place).
  - `holds the commands the module registers itself`: `allCommands().map((c) => c.id)` contains `app.backToClassic`, and `commandById("app.backToClassic")` is `toBe` the element of the list with that id.
  - `is a snapshot: pushing to the list changes nothing`: call `allCommands()`, `push` an object onto the result, and assert a second `allCommands()` has the same length as the first.

  Fails without: `lists every registered command in definition order…`: `allCommands` sorting its result, or `defineCommand` deleting an id before it sets it again (the redefined `a` would come last or the ids would be sorted differently), turns it red. `holds the commands the module registers itself`: an `allCommands` that reads a copy taken before `app.backToClassic` is defined turns it red. `is a snapshot…`: an `allCommands` that returns the registry's own storage turns it red.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/app/commands.test.ts` fails at the import: `allCommands` is not exported from `./commands` (a TypeScript error from the typecheck, `is not a function` at run).

- [ ] **Step 3: Implement.** In `app/commands.ts`:
  - Add the optional field `palette?: boolean` to `Command`, after `offline`, with a comment: the palette lists exactly the registered commands that set it, and a command sets it only when it takes no args (a command whose args a view picks, such as an ask's answer, a job's controls or a send, never sets it).
  - Export `function allCommands(): Command<unknown>[]` beside `commandById`: the registry's values as a new array, in definition order (the `Map`'s insertion order, a redefined id keeping its first place), with a comment that says so.
  - Change nothing else in the file; `defineCommand` keeps `registry.set`.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/app/commands.test.ts && pnpm -C internal/web/ui run typecheck` print the file's tests green and no type error.

- [ ] **Step 5: Commit.** `git commit -m "feat(web): list the registered commands and mark the ones the palette shows" -- internal/web/ui/src/app/commands.ts internal/web/ui/src/app/commands.test.ts`

### Task 1.2: The two view toggles, registered by the desktop shell

**Files:**
- Create: `internal/web/ui/src/commands/view.ts`
- Create: `internal/web/ui/src/commands/view.test.ts`
- Modify: `internal/web/ui/src/desktop/DesktopShell.tsx` (the import block)
- Test: `internal/web/ui/src/desktop/DesktopShell.test.tsx` (a new `describe("DesktopShell's commands", …)` at the end of the file)

- [ ] **Step 1: Write the failing tests.**
  - `commands/view.test.ts` imports `./view`, `commandById`, `runCommand` and `CommandContext` from `@/app/commands`, and `useDesktop` and `DESKTOP_KEY` from `@/app/desktopState`. A `ctx(over)` helper builds `{ session: undefined, link: "live", stale: false, panel: "fleet", ...over }`. `beforeEach` sets `useDesktop.setState({ sidebar: true, inspector: true })` and clears `window.localStorage`. Tests:
    - `view.toggleSidebar is titled, offline, in the palette and always on`: the command from `commandById("view.toggleSidebar")` has `title` exactly `Show or hide the sidebar`, `offline === true`, `palette === true`, and `when(ctx())` true both with no session and with `ctx({ panel: "settings" })`.
    - `view.toggleInspector is titled, offline, in the palette and always on`: the same for `view.toggleInspector` and `Show or hide the inspector`.
    - `view.toggleSidebar flips the sidebar and leaves the inspector`: `runCommand` it with `ctx()` and `undefined` args; `useDesktop.getState().sidebar` is false and `inspector` still true; run it again and `sidebar` is true.
    - `view.toggleInspector flips the inspector and leaves the sidebar`: the same the other way round.
    - `a toggle runs while the snapshot is stale`: `runCommand` each toggle with `ctx({ stale: true, link: "connecting" })`; each value flips (an offline command runs stale).
    - `a toggle writes its choice to loom.desktop`: after `view.toggleSidebar` runs, `JSON.parse(window.localStorage.getItem(DESKTOP_KEY) ?? "null")` has `sidebar: false` and `inspector: true`, so the flip went through `setSidebar` and its write, not a bare `setState`.
  - `desktop/DesktopShell.test.tsx`, in `describe("DesktopShell's commands")`, one test: `registers the view toggles by being imported`: `commandById("view.toggleSidebar")` and `commandById("view.toggleInspector")` are both defined with no import of `@/commands/view` anywhere in the file (add `commandById` to the file's imports from `@/app/commands`).

  Fails without: `view.toggleSidebar is titled…` and its inspector twin: a wrong title, a missing `offline` or `palette`, or a `when` that reads the panel turns them red. The two flip tests: a `run` that sets the other preference (`setInspector` in the sidebar's command), or one that sets a constant, turns them red. `a toggle runs while the snapshot is stale`: dropping `offline: true` turns it red. `a toggle writes its choice to loom.desktop`: a `run` that calls `useDesktop.setState` instead of the setter leaves `localStorage` empty and turns it red. `registers the view toggles by being imported`: removing the new import from `DesktopShell.tsx` leaves both ids undefined, because no other module in that file's import graph imports `commands/view`.

- [ ] **Step 2: Run them to see them fail.** `pnpm -C internal/web/ui exec vitest run src/commands/view.test.ts src/desktop/DesktopShell.test.tsx` fails: `./view` cannot be resolved for the first file, and `registers the view toggles by being imported` fails with `expected undefined not to be undefined`.

- [ ] **Step 3: Implement.**
  - `commands/view.ts` registers, with `defineCommand`, the two commands of the names contract: `view.toggleSidebar` titled `Show or hide the sidebar` and `view.toggleInspector` titled `Show or hide the inspector`, each `offline: true`, the palette mark set to `true`, and `when: () => true`. Each `run` is async, reads `useDesktop.getState()` and calls `setSidebar(!sidebar)` (or `setInspector(!inspector)`). Give them no icon. A header comment says why they are `offline` (they act on nothing in loom, so they run while the snapshot is stale, spec §16) and that only `desktop/DesktopShell.tsx` imports the module, never `commands/index.ts`, so the phone's first load does not carry it. Write the mark exactly as the field is named, but keep the comment free of the text `palette: true`, which Task 1.3's count must find only in code.
  - `desktop/DesktopShell.tsx`: add `import "@/commands/view";` to the import block, as the first line of the `@/` group, with a one-line comment above it (the side-effect import registers the two view toggles with the desktop chunk). No other change.

- [ ] **Step 4: Run them to see them pass.** `pnpm -C internal/web/ui exec vitest run src/commands/view.test.ts src/desktop/DesktopShell.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print the files' tests green and no type or lint error.

- [ ] **Step 5: Commit.** `git add -N internal/web/ui/src/commands/view.ts internal/web/ui/src/commands/view.test.ts`, then `git commit -m "feat(web): add the sidebar and inspector toggles as offline commands" -- internal/web/ui/src/commands/view.ts internal/web/ui/src/commands/view.test.ts internal/web/ui/src/desktop/DesktopShell.tsx internal/web/ui/src/desktop/DesktopShell.test.tsx`

### Task 1.3: Mark the eight no-args commands and pin the palette set

**Files:**
- Modify: `internal/web/ui/src/app/commands.ts` (the `app.backToClassic` definition at the end)
- Modify: `internal/web/ui/src/commands/session.ts` (the five definitions `session.pause`, `session.resume`, `session.push`, `session.merge`, `session.kill`)
- Modify: `internal/web/ui/src/commands/push.ts` (`push.enable`)
- Modify: `internal/web/ui/src/commands/device.ts` (`device.unpair`)
- Create: `internal/web/ui/src/commands/palette.test.ts`

- [ ] **Step 1: Write the failing test.** `commands/palette.test.ts` imports `allCommands` and `commandById` from `@/app/commands`, then `@/commands` and `@/commands/view` for their registrations. Tests:
  - `the palette lists exactly these ten commands`: the ids of `allCommands().filter((c) => c.palette === true)`, sorted, equal this sorted list: `app.backToClassic`, `device.unpair`, `push.enable`, `session.kill`, `session.merge`, `session.pause`, `session.push`, `session.resume`, `view.toggleInspector`, `view.toggleSidebar`.
  - `no command that takes args sets the mark`: for each of `session.send`, `ask.answer`, `ask.chat`, `ask.allow`, `ask.deny`, `job.start`, `job.park`, `job.unpark`, `job.pause`, `job.resume`, `session.create`, `device.update`, `quickReplies.save`, `device.pair`, the command is registered (`commandById` is defined) and its `palette` is not `true`.
  - `the palette commands are the ones that take no args`: each of the ten, run through `command.when` with a context that has no session on the `fleet` panel, does not throw (a `when` of a no-args command reads only the context), and `command.key` is `undefined` for each (a no-args command has no `key`).

  Fails without: `the palette lists exactly these ten commands`: removing the mark from any one of the eight (`session.kill`, say) or from a toggle turns it red, as does marking an eleventh. `no command that takes args sets the mark`: setting the mark on `session.send` or `ask.answer` turns it red, and a renamed id turns it red because `commandById` is undefined. `the palette commands are the ones that take no args`: giving a marked command a `key` turns it red.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/commands/palette.test.ts` fails on the first test: the list is only the two view toggles, so the eight others are missing from the received array.

- [ ] **Step 3: Implement.** Set the mark `palette: true` on exactly these definitions, one added line each, after the line holding the command's `title` (after `offline: true` for `app.backToClassic`):

  | Command | File | Added after |
  |---|---|---|
  | `app.backToClassic` | `app/commands.ts` | `offline: true,` |
  | `session.pause` | `commands/session.ts` | `title: "Pause",` |
  | `session.resume` | `commands/session.ts` | `title: "Resume",` |
  | `session.push` | `commands/session.ts` | `title: "Push",` |
  | `session.merge` | `commands/session.ts` | `title: "Merge",` |
  | `session.kill` | `commands/session.ts` | `title: "Kill",` |
  | `push.enable` | `commands/push.ts` | `title: "Turn on notifications",` |
  | `device.unpair` | `commands/device.ts` | `title: "Unpair this phone",` |

  Mark no other command. Extend the `app.backToClassic` comment above its definition with one sentence: the palette lists it. The check for this task: `grep -rn 'palette: true' internal/web/ui/src --include='*.ts' --include='*.tsx' --exclude='*.test.ts' --exclude='*.test.tsx'` prints ten lines (the eight above and the two in `commands/view.ts`); the comments Task 1.1 and 1.2 wrote do not contain the pattern.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/commands src/app/commands.test.ts && pnpm -C internal/web/ui run typecheck` print every commands test green, including the three in `palette.test.ts`.

- [ ] **Step 5: Commit.** `git add -N internal/web/ui/src/commands/palette.test.ts`, then `git commit -m "feat(web): mark the commands the palette lists" -- internal/web/ui/src/app/commands.ts internal/web/ui/src/commands/session.ts internal/web/ui/src/commands/push.ts internal/web/ui/src/commands/device.ts internal/web/ui/src/commands/palette.test.ts`

### Task 1.4: `confirmOpen`

**Files:**
- Modify: `internal/web/ui/src/app/ConfirmSheet.tsx` (beside `askConfirm` and `answer`)
- Test: `internal/web/ui/src/app/ConfirmSheet.test.tsx` (a new `describe("confirmOpen", …)` before `describe("CommandButton", …)`)

- [ ] **Step 1: Write the failing test.** Add `confirmOpen` to the import from `./ConfirmSheet`. The file's `kill` spec, its `ask()` helper (which wraps `askConfirm` in `act`) and `testSeam` are used as they are. `confirmOpen` reads module state, so these tests render no sheet except where they say. Tests:
  - `is false while nobody asks`: `confirmOpen()` is false.
  - `is true while an ask waits and false once it is answered`: `ask(kill)`; `confirmOpen()` is true; `act(() => { testSeam.answer(testSeam.openId() as number, null) })` (read the id before calling), then `await` the ask's promise; `confirmOpen()` is false.
  - `stays true when a newer ask replaces the open one, and false only after the newer one is answered`: `ask(kill)` then a second `ask({ ...kill, title: "Push" })`; the first promise resolves `null`, `confirmOpen()` is true; answer the second through the seam; `confirmOpen()` is false.
  - `is false after a cancel from the drawn sheet`: render `<ConfirmSheet variant="dialog" />`, `ask(kill)`, click `Cancel`; `confirmOpen()` is false.
  - An `afterEach` in the new `describe` answers any ask still open (`testSeam.openId()` not null → `testSeam.answer(id, null)`), so a failing test leaves nothing open for the next.

  Fails without: `is true while an ask waits…`: a `confirmOpen` that returns false (or reads a flag set only by the sheet being drawn, since this test draws none) turns it red. `stays true when a newer ask replaces…`: a flag cleared by the replaced ask's resolve turns it red. `is false while nobody asks` and `is false after a cancel…`: a `confirmOpen` that returns true always, or one that never clears on an answer, turns them red.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/app/ConfirmSheet.test.tsx` fails: `confirmOpen` is not exported (`confirmOpen is not a function`).

- [ ] **Step 3: Implement.** In `app/ConfirmSheet.tsx` export `function confirmOpen(): boolean`: true when the module's `current` ask is not `null`. Put a comment on it: Story 3's chord listener reads it (a confirmation open blocks the chord), it reads the module's state and not a subscription, and it is true from `askConfirm` until the ask is answered, a replaced ask leaving it true for the newer one. Add no state: `current` is already that.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/app/ConfirmSheet.test.tsx && pnpm -C internal/web/ui run typecheck` print the file green.

- [ ] **Step 5: Commit.** `git commit -m "feat(web): report whether a confirmation is open" -- internal/web/ui/src/app/ConfirmSheet.tsx internal/web/ui/src/app/ConfirmSheet.test.tsx`

### Task 1.5: A destructive ask's Dialog opens on Cancel

**Files:**
- Modify: `internal/web/ui/src/app/ConfirmSheet.tsx` (`Sheet`'s dialog branch, `AskBody`'s Cancel button)
- Test: `internal/web/ui/src/app/ConfirmSheet.test.tsx` (new tests at the end of `describe("ConfirmSheet as a dialog", …)`)

- [ ] **Step 1: Write the failing tests.** In `describe("ConfirmSheet as a dialog")` add, using the file's `kill` spec (`destructive: true`) and `ask()` helper, and `waitFor` already imported:
  - `a destructive ask opens with focus on Cancel`: render `<ConfirmSheet variant="dialog" />`, `ask(kill)`, find the dialog `Kill session`, then `await waitFor(() => { expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus(); })`. Then press Enter with `userEvent.keyboard("{Enter}")`: the ask's promise resolves `null` (Cancel, not the action), proving a held Enter cancels.
  - `a non-destructive ask keeps the Dialog's default focus`: `ask({ ...kill, destructive: false })`; wait for focus to be inside the dialog (`await waitFor(() => { expect(dialog.contains(document.activeElement)).toBe(true); })`), then `expect(within(dialog).getByRole("button", { name: "Cancel" })).not.toHaveFocus()`.
  - `a destructive ask with a text field still opens on Cancel`: `ask({ ...textSpec(5), destructive: true })` (the file's `textSpec` helper), then wait for Cancel to have focus (the field does not take it).
  - `the focus follows the next ask when the dialog is reopened`: ask a destructive spec, click Cancel, wait for the dialog to unmount (`queryByText("Kill session")` null), then ask a non-destructive spec and assert, as in the second test, that Cancel is not focused: a destructive ask's focus does not carry into the next.

  Fails without: `a destructive ask opens with focus on Cancel`: removing the `initialFocus` the story adds to the Dialog's popup leaves focus on the action button (`Kill`, the first tabbable), so Cancel is not focused and the Enter resolves `""` instead of `null`. `a non-destructive ask keeps the Dialog's default focus`: an `initialFocus` that always points at Cancel, whatever `spec.destructive` says, turns it red. `a destructive ask with a text field…`: an implementation that only reorders the buttons, or keys off the absence of a field, turns it red. `the focus follows the next ask…`: a Cancel ref kept past the ask (or a destructive flag latched in state) turns it red.

- [ ] **Step 2: Run them to see them fail.** `pnpm -C internal/web/ui exec vitest run src/app/ConfirmSheet.test.tsx` fails the first and third new tests with `expected element to have focus` (the focused element is the `Kill` button or the dialog).

- [ ] **Step 3: Implement.** In `app/ConfirmSheet.tsx`, dialog variant only (the drawer is the phone's and is unchanged):
  - Before writing, run `pnpm -C internal/web/ui install --frozen-lockfile` and read the `DialogPopup` type declarations under the app's installed `@base-ui/react/dialog/popup/` for the exact type of the popup's `initialFocus` prop (Base UI 1.9; it takes a boolean, a ref, or a function that returns an element, a boolean or nothing). `DialogContent` in `components/ui/dialog.tsx` spreads its props onto that popup, so it needs no change. If the prop is named otherwise, use the d.ts's name and report it.
  - In `Sheet`, hold a `useRef<HTMLButtonElement>(null)` for the Cancel button and, in the dialog branch, give `DialogContent` an `initialFocus` that returns the ref's element when `ask.spec.destructive` is true (falling back to the default behaviour when the ref is empty) and keeps the default behaviour otherwise. Pass the ref to `AskBody` as a prop and set it as the `ref` of the Cancel `Button` (`Button` spreads its props, `ref` included, onto Base UI's button, as React 19 allows).
  - Put a comment on it that says why: the palette's Enter opens a confirmation from the keyboard, and the desktop dialog lists the action before Cancel, so first-tabbable focus would let an Enter held past key-repeat press the action. State that a newer ask replacing an open one does not move focus (the dialog stays open), and that the drawer is unchanged.

- [ ] **Step 4: Run them to see them pass.** `pnpm -C internal/web/ui exec vitest run src/app/ConfirmSheet.test.tsx src/app/shell.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print both files green (the existing dialog and drawer tests among them) and no type or lint error.

- [ ] **Step 5: Commit.** `git commit -m "feat(web): open a destructive confirmation on Cancel" -- internal/web/ui/src/app/ConfirmSheet.tsx internal/web/ui/src/app/ConfirmSheet.test.tsx`

### Task 1.6: Rebuild the bundle and run the story's gates

**Files:**
- Modify: `internal/web/dist/` (rebuilt output; no hand edit)

- [ ] **Step 1: Build.** `pnpm -C internal/web/ui run build` writes the bundle into `internal/web/dist/`. `DesktopShell`'s chunk and the entry's hashed names change; the entry gains only `confirmOpen` and `Command.palette`'s unused field (types vanish), so the entry's gzipped size moves by a few bytes at most.

- [ ] **Step 2: Run the story's checks.** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'` passes: `check` runs the typecheck, lint, format check and the whole vitest suite, `dist:check` finds the bundle fresh, and the Go package's `checkLazyViews` and first-load budget tests pass unchanged (`view.ts` is in the desktop chunk, not the entry). If `format:check` flags a file of this story, run `pnpm -C internal/web/ui exec prettier --write <that file>` and rebuild.

- [ ] **Step 3: Run the repo's story gates.** `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi'`, then `CGO_ENABLED=0 go vet ./...`, then `CGO_ENABLED=0 go test -timeout 30m ./...` each exit 0.

- [ ] **Step 4: Commit.** `git add -N internal/web/dist/`, then `git commit -m "build(web): rebuild the bundle for the palette's registry and the confirmation's focus" -- internal/web/dist/`

## Story 2: The palette's items, score and order, as a pure module

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/desktop/palette.ts`, `internal/web/ui/src/desktop/palette.test.ts`, `internal/web/ui/src/fleet/groups.ts`, `internal/web/ui/src/fleet/groups.test.ts`, `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `fuzzyScore`, `compareCodePoints`, `hasShownFlows`, `PaletteTab`, `PaletteSession`, `PaletteCommand`, `PalettePlace`, `PaletteItem`, `PaletteGroup`, `PaletteInput`, `paletteGroups`; uses `Command.palette`, `allCommands` (Story 1), `commandEnabled`, `CommandContext`, `allSessions`, `groupSessions`, `shownWorkspaces`, `sessionHref`, `hrefFor`, `sessionTabs`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** brief (a pure function whose every rule is a named test; Task 2.2, the uncapped quadratic score, stays brief by spec decisions 7 and 17)

This story builds what the palette lists and draws nothing. `desktop/palette.ts` (which imports no React) holds `fuzzyScore`, `compareCodePoints` and `paletteGroups`: from the snapshot, the registered commands and the route's command context it builds the Sessions, Commands and Places groups, matched, scored and ordered as the Part 2 settlements' decisions 7 to 9 say. `hasShownFlows` moves into `fleet/groups.ts` out of `FleetLinks`' own selector, so the top bar's Flows link and the palette's Flows place share one rule (the entry gains this one small helper; the module stays out of the entry otherwise, since nothing imports `palette.ts` until Story 3). The tasks run so that the tree is green after each: the shared rule, the score and the code-point compare, the item types with the Sessions group, the Commands and Places groups, then the rebuilt bundle. Story 1's `Command.palette` and `allCommands` exist when this story starts. No code here holds a landmine marker, so the developer runs at the default effort. Every command below is run from the repo root.

### Task 2.1: `hasShownFlows`

**Files:**
- Modify: `internal/web/ui/src/fleet/groups.ts` (a new function after `shownWorkspaces`)
- Modify: `internal/web/ui/src/panels/fleet/FleetHead.tsx:9` and `:18-24`
- Test: `internal/web/ui/src/fleet/groups.test.ts` (a new `describe("hasShownFlows", …)` after `describe("allSessions and shownWorkspaces", …)`)

- [ ] **Step 1: Write the failing test.** In `fleet/groups.test.ts` add `hasShownFlows` to the import from `./groups`, and add `describe("hasShownFlows")` over `fixtureSnapshot(NOW)` (its session 6, of workspace 2, is the only one with flows). Tests:
  - `is true when a session of a shown workspace has a flow`: `hasShownFlows(fixtureSnapshot(NOW))` is `true`.
  - `is false when no session has a flow, whether its list is empty or null`: set every session's `flows` to `[]` and then to `null` (a loop over `allSessions`), `hasShownFlows` is `false` each time.
  - `ignores the flows of a hidden workspace`: replace workspace 2 with `{ ...second, shown: false }` (as `shownWorkspaces`' test above does), `hasShownFlows` is `false`.
  - `counts a workspace without the shown flag, as an older daemon sends it`: copy workspace 2 without `shown` (delete the key from a `Partial`, as the test above does), `hasShownFlows` is `true`.
  - `is false for a snapshot with no sessions`: `{ ...snapshot, sessions: null }` gives `false`.

  Fails without: `is true when a session of a shown workspace has a flow`: a `hasShownFlows` that returns a constant `false` turns it red. `is false when no session has a flow…`: one that checks only that `flows` is not `undefined` (so an empty list counts) turns it red. `ignores the flows of a hidden workspace`: one that reads `allSessions` without `shownWorkspaces` turns it red. `counts a workspace without the shown flag…`: one that tests `w.shown === true` turns it red. `is false for a snapshot with no sessions`: one that reads `snapshot.sessions` without `allSessions`' null guard throws and turns it red.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/fleet/groups.test.ts` fails: `hasShownFlows` is not exported from `./groups` (the typecheck error, and `hasShownFlows is not a function` at run).

- [ ] **Step 3: Implement.**
  - In `fleet/groups.ts` add `export function hasShownFlows(snapshot: Snapshot): boolean` after `shownWorkspaces`, with a doc comment: true when a session of a shown workspace has at least one flow, the rule of the top bar's and the fleet header's Flows link and of the palette's Flows place. It is today's selector body: the ids of `shownWorkspaces(snapshot)` in a `Set`, then `allSessions(snapshot).some((x) => shown.has(x.workspace) && (x.flows ?? []).length > 0)`.
  - In `panels/fleet/FleetHead.tsx` change the import on line 9, which is now

```ts
import { allSessions, shownWorkspaces } from "@/fleet/groups";
```

    to import `hasShownFlows` alone, and replace the selector on lines 18 to 24, which is now

```ts
  const hasFlows = useFleet((s) => {
    if (s.snapshot === null) return false;
    const shown = new Set(shownWorkspaces(s.snapshot).map((w) => w.id));
    return allSessions(s.snapshot).some(
      (x) => shown.has(x.workspace) && (x.flows ?? []).length > 0,
    );
  });
```

    with a one-line selector that returns `false` for a null snapshot and `hasShownFlows(s.snapshot)` otherwise. `allSessions` and `shownWorkspaces` are no longer used in that file, so drop them from its import (lint refuses an unused import). Nothing else in `FleetHead.tsx` changes.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/fleet/groups.test.ts src/panels/fleet/fleet.test.tsx src/desktop/TopBar.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print the five new tests green and the existing flows cases (`FleetHead shows the flows link only when a listed session has flows`, `FleetHead ignores the flows of a session in a hidden workspace` in `fleet.test.tsx`, and the top bar's two Flows cases in `TopBar.test.tsx`) still green, with no type or lint error. Those four cases are what pin that `FleetLinks` still follows the rule through its new call.

- [ ] **Step 5: Commit.** `git commit -m "refactor(web): share the flows link's rule as hasShownFlows" -- internal/web/ui/src/fleet/groups.ts internal/web/ui/src/fleet/groups.test.ts internal/web/ui/src/panels/fleet/FleetHead.tsx`

### Task 2.2: `fuzzyScore` and `compareCodePoints`

**Files:**
- Create: `internal/web/ui/src/desktop/palette.ts`
- Create: `internal/web/ui/src/desktop/palette.test.ts`

- [ ] **Step 1: Write the failing test.** `desktop/palette.test.ts` imports `describe`, `expect` and `it` from `vitest` and `compareCodePoints` and `fuzzyScore` from `./palette`. Two `describe` blocks. `describe("fuzzyScore")`:
  - `scores "pa" against "Pause" as 7`: `fuzzyScore("pa", "Pause")` is `7` (the first code point starts the text, 1 + 3, and `a` directly follows it, 1 + 2).
  - `scores "pa" against "update panel" as 7, from the alignment at "panel"`: `fuzzyScore("pa", "update panel")` is `7`, though the alignment that starts at the `p` of "update" scores 2.
  - `scores "ps" against "Pause" as 5`: `fuzzyScore("ps", "Pause")` is `5` (`p` 4, then `s` at its next occurrence, not directly after: 1).
  - `scores "sp" against "session.push" as 8`: `fuzzyScore("sp", "session.push")` is `8` (`s` 4, then the `p` of "push", which starts a word after the `.`: 4).
  - `gives null when the query's code points do not all match in order`: `fuzzyScore("xyz", "Pause")` is `null`, and so is `fuzzyScore("ba", "ab")` (all present, wrong order).
  - `takes the best start, not the first or the last`: `fuzzyScore("ab", "a ab")` is `7` (the alignment from the second `a`, after the space: 4 + 3), and `fuzzyScore("ab", "axbab")` is `5` (the first start, 4 + 1, beats the later one, 1 + 3).
  - `scores an empty or blank query as 0, and trims the query`: `fuzzyScore("", "Pause")` and `fuzzyScore("   ", "Pause")` are `0`, `fuzzyScore("  pa ", "Pause")` is `7`, and `fuzzyScore("", "")` is `0`.
  - `lowercases both sides`: `fuzzyScore("PA", "pause")` is `7` and `fuzzyScore("pa", "PAUSE")` is `7`.
  - `starts a word at the text's start or after a code point that is neither a letter nor a digit`: `fuzzyScore("b", "b")`, `fuzzyScore("b", " b")` and `fuzzyScore("b", "-b")` are `4`; `fuzzyScore("b", "ab")` and `fuzzyScore("b", "1b")` are `1` (a letter or a digit before it is not a word break).
  - `walks code points, not UTF-16 units`: `fuzzyScore("\u{1F600}", "a\u{1F600}")` is `1` (one matched code point after a letter), where a walk over UTF-16 units would match two units and add the consecutive bonus; and `fuzzyScore("\u{1F600}b", "\u{1F600}b")` is `7` (4 for the first code point at the text's start, then 1 + 2).

  `describe("compareCodePoints")`:
  - `orders by code point`: `compareCodePoints("a", "b")` is below 0, `("b", "a")` above 0, `("a", "a")` is `0`, `("ab", "abc")` is below 0 (a prefix first), and `("Z", "a")` is below 0 (an uppercase letter first, where `localeCompare` says the opposite).
  - `puts an astral code point after a BMP one, where < on the strings does the opposite`: `compareCodePoints("\u{FF61}", "\u{1F600}")` is below 0 and `compareCodePoints("\u{1F600}", "\u{FF61}")` above 0; and, to show what the test is for, `"\u{FF61}" < "\u{1F600}"` is `false`.

  Fails without: the `"pa"`, `"ps"` and `"sp"` tests: a score that gives no word-start bonus, or none for a consecutive code point, changes at least one of 7, 5 and 8 and turns them red. `scores "pa" against "update panel" as 7…`: a walk that takes only the first start (or the first occurrence of the first code point) gives 2 and turns it red. `takes the best start…`: a walk of the last start only, or the first only, turns one of its two assertions red. `gives null…`: a match that ignores order (a bag of code points) turns `("ba", "ab")` red. `scores an empty or blank query…`: no trim, or `null` for an empty query, turns it red. `lowercases both sides`: lowercasing only the text turns the `"PA"` assertion red. `starts a word…`: treating a digit as a word break turns `("b", "1b")` red, and treating only a space as one turns the `-b` assertion red. `walks code points…`: indexing the strings by UTF-16 unit turns it red. `orders by code point`: `localeCompare` turns the `"Z"` assertion red, and comparing lengths first turns the prefix one red. `puts an astral code point after…`: a compare by `<` on the strings or by `charCodeAt` turns it red.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/desktop/palette.test.ts` fails: `Failed to resolve import "./palette" from "src/desktop/palette.test.ts"`, because the module does not exist.

- [ ] **Step 3: Implement.** Create `desktop/palette.ts` with a header comment saying it is the palette's pure part (no React, no store: the score, the code-point order, and the items and their order from a snapshot, the registered commands and a command context), and export two functions:
  - `export function compareCodePoints(a: string, b: string): number`: walks both strings by code point (`codePointAt` and the code point's UTF-16 length, never `localeCompare` or `<`), returns the first difference as a negative or positive number, and when one string is a prefix of the other, the shorter first; 0 for equal strings. Its comment names the astral case: `<` compares UTF-16 units, where a surrogate sorts below U+E000 to U+FFFF.
  - `export function fuzzyScore(query: string, text: string): number | null`, as the Part 2 settlements' decision 7 defines it: trim the query, lowercase it and the text (`toLowerCase()`), and spread each into an array of code points. An empty query is 0. An alignment starts at a text position holding the query's first code point and takes each later query code point at its next occurrence after the previous match. Each matched code point scores 1, plus 2 when it directly follows the previous matched one in the text, plus 3 when it starts a word (text index 0, or the code point before it matches neither `/\p{L}/u` nor `/\p{N}/u`). Return the best score over every start, `null` when no start matches every code point. Write the word-start test as a small private helper over the code point array. The comment says the work is quadratic only in the text's length, which is a session's title, branch or workspace name, a command's title or a place's title, so no length bound is needed.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/desktop/palette.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print all twelve tests green (ten for the score, two for the compare) and no type or lint error.

- [ ] **Step 5: Commit.** `git add -N internal/web/ui/src/desktop/palette.ts internal/web/ui/src/desktop/palette.test.ts`, then `git commit -m "feat(web): score a palette query against a title, and compare by code point" -- internal/web/ui/src/desktop/palette.ts internal/web/ui/src/desktop/palette.test.ts`

### Task 2.3: The item types and the Sessions group

**Files:**
- Modify: `internal/web/ui/src/desktop/palette.ts` (the types and `paletteGroups`, after `fuzzyScore`)
- Test: `internal/web/ui/src/desktop/palette.test.ts` (a new `describe("paletteGroups: sessions", …)` after the two from Task 2.2)

- [ ] **Step 1: Write the failing test.** In `desktop/palette.test.ts` add `paletteGroups` and the types `PaletteGroup`, `PaletteInput` and `PaletteItem` to the import from `./palette`; import `Session` and `Snapshot` as types from `@/api/gen`, `fixtureSnapshot` from `@/fixtures`, and `context` and `makeSession` from `@/commands/testkit`. The canned fleet (`fixtureSnapshot(NOW)`, `NOW = Date.parse("2026-11-18T12:00:00Z")`) is: workspace 1 `loom` holds session 1 `fix-pane-cursor` (working), 2 `sandbox-ask` (needs you, 18m), 3 `plain-shell` (working) and 7 `a-very-long-session-title-that-will-not-fit-on-one-line` (your turn, mockup fresh); workspace 2 `flow` holds 4 `flow-docs` (needs you, 52m), 5 `flow-old` (paused) and 6 `flow-run` (working, two flows); each branch is `aidanb/<title>`. Write four helpers at the top of the new `describe`:
  - `input(over: Partial<PaletteInput> = {}): PaletteInput` is `{ query: "", snapshot: fixtureSnapshot(NOW), commands: [], ctx: context(undefined), tabs: [], ...over }`.
  - `sessionItems(groups: PaletteGroup[]): PaletteItem[]` is the `Sessions` group's items, `[]` when the group is absent.
  - `keys(items: PaletteItem[]): string[]` maps to `key`.
  - `snapshotOf(sessions: Session[]): Snapshot` is `{ ...fixtureSnapshot(NOW), sessions: { "1": sessions } }`; every session in it is built as `makeSession({ id, title, branch: "" })` (an empty branch, so only the title and the workspace's name `loom` can match).

  Tests:
  - `lists no sessions for a null snapshot`: `paletteGroups(input({ snapshot: null, query: "flow" }))` has no group labelled `Sessions`.
  - `with an empty query lists only the sessions that need you, the longest wait first`: `keys(sessionItems(paletteGroups(input())))` is `["session:4", "session:2"]` (workspace 2's 52m wait before workspace 1's 18m; the working, your-turn and paused sessions are left out).
  - `with an empty query leaves the Sessions group out when nothing needs you`: over `snapshotOf` of one your-turn session, no group is labelled `Sessions`.
  - `draws a session as its title, a hint of workspace and branch, and its link`: query `plain-shell`; the one session item `toMatchObject` `{ kind: "session", key: "session:3", title: "plain-shell", hint: "loom · aidanb/plain-shell", href: "#/s/3" }`.
  - `opens a session whose mockup is fresh at its mockup`: query `a-very-long`; the item for session 7 has `href` `#/s/7/mockup`.
  - `leaves an empty branch or an unnamed workspace out of the hint`: over `snapshotOf` of a session titled `solo` with branch `""`, query `solo`, the hint is `loom`; with the same session's `workspace` set to 99 (no such workspace) and branch `aidanb/solo`, the hint is `aidanb/solo`.
  - `matches a session on its branch`: query `aidanb/plain` lists exactly `["session:3"]` (no title holds it).
  - `matches a session on its workspace's label, or its name when the label is empty`: with workspace 2's `label` set to `Scratch` (its `name` stays `flow`) and query `scratch`, the keys are `["session:4", "session:5", "session:6"]` (order as the next tests fix it); with workspace 1's `label` set to `""` and query `loom`, they are `["session:1", "session:2", "session:3", "session:7"]`. Each assertion sorts the keys by code point before comparing, since these two are about membership.
  - `lists paused sessions, and those of a hidden workspace`: query `flow-old` lists `["session:5"]`; with workspace 2 `shown: false`, the same query still lists it.
  - `drops what does not match`: query `zzzz` leaves no Sessions group, and `paletteGroups(input({ query: "zzzz" }))` is `[]`.
  - `orders by score, higher first`: over `snapshotOf` of sessions titled `xpa` (id 10), `update panel` (id 11) and `Pause` (id 12), query `pa` lists `["session:12", "session:11", "session:10"]` (7, 7, then 4; the tie between 12 and 11 by title, `Pause` first).
  - `orders equal scores by title by code point, not locale`: sessions titled `xb` (id 10) and `Xc` (id 11), query `x`: `["session:11", "session:10"]` (both score 4; `Xc` sorts before `xb` by code point, where `localeCompare` puts `xb` first).
  - `orders equal scores by title across BMP and astral code points`: titles `x\u{1F600}` (id 10) and `x\u{FF61}` (id 11), query `x`: `["session:11", "session:10"]`.
  - `orders equal scores and titles by key by code point`: two sessions both titled `same`, ids 3 and 12, query `same`: `["session:12", "session:3"]` (the key `session:12` sorts before `session:3`, where a numeric order puts 3 first).
  - `scores a session by the best of its title, branch and workspace name`: over `snapshotOf`-style sessions `A` (title `zzz`, branch `pa`, id 10) and `B` (title `xpa`, branch `""`, id 11), query `pa` lists `["session:10", "session:11"]` (A's branch scores 4 + 3 = 7, B's title 4).

  Fails without: `lists no sessions for a null snapshot`: a function that reads `snapshot.sessions` unguarded throws and turns it red. `with an empty query lists only the sessions that need you…`: a listing of every session turns it red, and ordering the newest wait first gives `["session:2", "session:4"]` and turns it red. `…leaves the Sessions group out when nothing needs you`: an empty group left in the result turns it red. `draws a session as…`: a hint joined by something other than ` · `, or a `href` built by hand rather than through `sessionHref`, turns it red (session 7's test too). `leaves an empty branch…`: a hint that always joins both parts (`"loom · "`) turns the first assertion red, and a workspace lookup that falls back to another workspace's name turns the second red. `matches a session on its branch`: matching the title alone turns it red. `matches a session on its workspace's label…`: matching `name` only turns the `scratch` assertion red, and matching `label` only turns the `loom` one red. `lists paused sessions, and those of a hidden workspace`: reading `groupSessions`' groups without `paused`, or `fleetGroups`' shown-only listing, turns the two assertions red. `drops what does not match`: a score that treats `null` as 0 turns it red. `orders by score…`: an order by title alone turns it red. `orders equal scores by title by code point, not locale`: `localeCompare` turns it red. `orders equal scores by title across BMP and astral…`: a `<` compare turns it red. `orders equal scores and titles by key…`: no key tie-break (the registry's input order, 3 then 12) or a numeric one turns it red. `scores a session by the best…`: scoring the title alone leaves A unlisted and turns it red.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/desktop/palette.test.ts` fails on the new block: `paletteGroups` is not exported from `./palette` (the typecheck error, `paletteGroups is not a function` at run).

- [ ] **Step 3: Implement.** In `desktop/palette.ts` export the names of the names contract, each with the contract's exact fields and no more: `PaletteTab`, `PaletteSession`, `PaletteCommand`, `PalettePlace`, `PaletteItem`, `PaletteGroup` and `PaletteInput`. Import `Session` and `Snapshot` as types from `@/api/gen`, `Command`, `CommandContext` and `commandEnabled` from `@/app/commands`, `PanelId` as a type from `@/app/panels`, `hrefFor` from `@/app/routes`, `allSessions`, `groupSessions` and `hasShownFlows` from `@/fleet/groups`, and `sessionHref` from `@/fleet/links`. Add `export function paletteGroups(input: PaletteInput): PaletteGroup[]`, returning for now only the Sessions group (Task 2.4 adds the other two); build it from private helpers so Task 2.4 can reuse them:
  - A private `rank<T>(entries: { item: T, score: number, title: string, key: string }[]): T[]`: orders by score (higher first), then `compareCodePoints` of the title, then `compareCodePoints` of the key, and returns the items. One helper orders all three groups.
  - The Sessions group: the query is trimmed. Over `allSessions(snapshot)` (every workspace's sessions, paused included, hidden workspaces included), a non-empty query keeps each session whose best `fuzzyScore` over its title, its branch and its workspace's name (`label || name` of the workspace in `snapshot.workspaces` with the session's `workspace` id, an empty string when none) is not `null`, scored by that best, and ranks them. An empty query lists the `needs_you` list of `groupSessions(allSessions(snapshot), null)` in the order that function gives it (the longest wait first), unranked. A null snapshot gives no sessions. Each session becomes a `PaletteSession`: `key` `session:<id>`, `title` the session's title, `hint` the workspace name and the branch joined by ` · ` with an empty one left out, `href` `sessionHref(session)`.
  - The result is the group `{ label: "Sessions", items }`, left out when `items` is empty.

  A comment on `paletteGroups` states the three groups' order and the empty-query rules, as decisions 8 and 9 say.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/desktop/palette.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print every test green and no type or lint error.

- [ ] **Step 5: Commit.** `git commit -m "feat(web): build the palette's Sessions group, matched and ordered" -- internal/web/ui/src/desktop/palette.ts internal/web/ui/src/desktop/palette.test.ts`

### Task 2.4: The Commands and Places groups

**Files:**
- Modify: `internal/web/ui/src/desktop/palette.ts` (`paletteGroups` and two private builders)
- Test: `internal/web/ui/src/desktop/palette.test.ts` (a new `describe("paletteGroups: commands and places", …)` after Task 2.3's)

- [ ] **Step 1: Write the failing test.** In the new `describe`, reuse Task 2.3's `input` and `keys` helpers (move them to the top of the file, above both `describe` blocks, when this task needs them there), add `Command` as a type from `@/app/commands`, and add two helpers: `cmd(id: string, title: Command<unknown>["title"], over: Partial<Command<unknown>> = {}): Command<unknown>` is `{ id, title, palette: true, when: () => true, run: () => Promise.resolve(), ...over }`; `items(groups: PaletteGroup[], label: PaletteGroup["label"]): PaletteItem[]` is that group's items, `[]` when absent. Session 3 of the canned fleet (`plain-shell`, from `fixtureSnapshot(NOW)`) is the current session in the tests that need one: `const current = allSessions(fixtureSnapshot(NOW)).find((s) => s.id === 3)` (import `allSessions` from `@/fleet/groups`; fail the test file with a thrown error when it is missing, as `testkit` does). Tests for commands:
  - `lists only the commands that carry the palette mark`: `commands` is `[cmd("a.one", "One"), cmd("a.two", "Two", { palette: false }), { ...cmd("a.three", "Three"), palette: undefined }]`; the Commands group's keys are `["command:a.one"]`.
  - `draws a command as its key, its title and itself`: for `cmd("a.one", "One")` the item `toMatchObject` `{ kind: "command", key: "command:a.one", title: "One", enabled: true }` and its `command` is `toBe` the input command.
  - `calls a function title with the context`: `cmd("a.title", (ctx) => \`Pause ${ctx.session?.title ?? "nothing"}\`)` over `context(current)` is titled `Pause plain-shell`, and over `context(undefined)` `Pause nothing`.
  - `says enabled as commandEnabled does`: over `context(current, { stale: true })`, a command with `when: () => true` and no `offline` is `enabled: false`; the same with `offline: true` is `enabled: true`; over `context(current)` a command with `when: () => false` is `enabled: false`; one with `when` true is `enabled: true`.
  - `with an empty query lists the commands whose when holds, by title by code point`: commands titled `beta`, `Zed`, `alpha` and one titled `hidden` whose `when` is `() => false`; the Commands keys are the ids of `Zed`, `alpha` and `beta` in that order (code point: `Z` before `a`).
  - `with an empty query lists a command whose when holds even while stale, not enabled`: over `context(current, { stale: true })`, `cmd("a.one", "One")` is listed with `enabled: false` (the empty query reads `when`, not `commandEnabled`).
  - `with a query matches on the title and lists a command whose when fails, not enabled`: query `hid` over the same four commands lists `hidden` only, `enabled: false`; query `a` over `alpha`, `beta` and `Zed` orders them by score, then title (`alpha` 4 first, then `beta`: `a` at the end, 1, and `Zed`: `a` is not in it, dropped).
  - `matches a command on its title, never its id`: a command with id `zzz.id` and title `One`; query `zzz` lists no Commands group.

  Tests for places:
  - `with an empty query lists the session's tabs, then Fleet, New session, Flows and Settings`: `ctx` is `context(current)`, `tabs` is `[{ id: "turns", label: "conversation" }, { id: "info", label: "info" }]`; the Places keys are `["place:turns", "place:info", "place:fleet", "place:new", "place:flows", "place:settings"]`.
  - `draws a tab as its label, the session's title and its tab's link`: the `place:turns` item `toMatchObject` `{ kind: "place", title: "conversation", hint: "plain-shell", href: "#/s/3" }` and `place:info` has `href` `#/s/3/info`.
  - `draws Fleet, New session, Flows and Settings with their titles, no hint and their links`: the four items `toMatchObject` `{ title: "Fleet", hint: "", href: "#/" }`, `{ title: "New session", hint: "", href: "#/new" }`, `{ title: "Flows", hint: "", href: "#/flows" }` and `{ title: "Settings", hint: "", href: "#/settings" }`.
  - `leaves the Flows place out when no shown workspace's session has a flow`: with every session's `flows` set to `null`, and again with workspace 2 hidden, the Places keys hold no `place:flows`; with a `null` snapshot they hold `["place:fleet", "place:new", "place:settings"]`.
  - `has no tab places without a session`: `tabs` of `[]` with `context(undefined)` lists the four plain places; and with `tabs` given but `context(undefined)`, no tab place is drawn (a tab's link needs the session's id).
  - `with a query matches a place on its title, not its hint, ordered by score, title, key`: query `s` with the tabs above and `context(current)` lists `["place:new", "place:settings", "place:flows", "place:turns"]` (`New session` 4, `Settings` 4, tie by title `N` before `S`; then `Flows` 1 and `conversation` 1, tie by title `F` before `c`; `info` and `Fleet` hold no `s`). Query `plain` lists no Places group although the session's title is in every tab's hint.

  Tests for the whole:
  - `orders the groups Sessions, Commands, Places, and leaves out an empty one`: with `commands: [cmd("a.one", "One")]` and an empty query, the labels are `["Sessions", "Commands", "Places"]`; with `snapshot` over a fleet where nothing needs you (`snapshotOf`-style, one your-turn session) the labels are `["Commands", "Places"]`; with `commands: []` too, `["Places"]`; and query `zzzz` gives `[]`.
  - `matches across the three kinds in one query`: query `pa` over `commands: [cmd("p.pause", "Pause")]`, the canned fleet, `context(current)` and `tabs: [{ id: "turns", label: "conversation" }]`: the Sessions keys are exactly `["session:1", "session:3"]` (`fix-pane-cursor` scores 7, `plain-shell` 5; no other session holds `p` then `a`), the Commands keys exactly `["command:p.pause"]`, and no Places group is drawn (no place title holds `p` then `a`).

  Fails without: `lists only the commands that carry the palette mark`: a list of every command, or one that tests `palette !== undefined`, turns it red. `draws a command as its key…`: a wrapped copy of the command, a different key or an `enabled` that is not read from `commandEnabled` turns it red. `calls a function title with the context`: a title taken as a string without calling it renders `undefined` or throws and turns it red. `says enabled as commandEnabled does`: an `enabled` that is `when(ctx)` alone turns the stale assertions red, and one that is `!ctx.stale` alone turns the `offline` and `when: () => false` ones red. `with an empty query lists the commands whose when holds…`: a list that skips the `when` filter turns it red, and a sort by `localeCompare` turns the `Zed`-first assertion red. `…even while stale, not enabled`: a filter on `commandEnabled` instead of `when` turns it red. `with a query matches on the title and lists a command whose when fails…`: a query filter that also applies `when` turns the `hid` assertion red. `matches a command on its title, never its id`: scoring the id turns it red. The places tests: `with an empty query lists the session's tabs…`: a Places order that puts the tabs last turns it red, as does any place in the wrong place. `draws a tab…`: a tab link built from the route rather than `hrefFor(tab.id, { id })`, or a hint other than the session's title, turns it red. `draws Fleet, New session…`: a wrong title (`New`, `Flow`) or a hint that is not empty turns it red. `leaves the Flows place out…`: a Flows place always drawn, or one that reads the snapshot's sessions without `hasShownFlows` (the hidden workspace's flows), turns it red. `has no tab places without a session`: reading `ctx.session.id` unguarded throws and turns it red. `with a query matches a place on its title…`: matching the hint turns the `plain` assertion red, and an order by the list position instead of score turns the order assertion red. `orders the groups…`: a group order other than the three, or an empty group kept, turns it red. `matches across the three kinds…`: a query matched only against sessions leaves the Commands keys empty and turns it red.

- [ ] **Step 2: Run it to see it fail.** `pnpm -C internal/web/ui exec vitest run src/desktop/palette.test.ts` fails on the new tests: the result of `paletteGroups` holds only a `Sessions` group, so the Commands and Places expectations receive `[]` or `undefined`.

- [ ] **Step 3: Implement.** In `desktop/palette.ts`, add two private builders beside Task 2.3's session builder, reusing `rank`, and make `paletteGroups` return `[Sessions, Commands, Places]` with each left out when it holds no items:
  - Commands: narrow `input.commands` to those whose `palette` is `true`. A non-empty query keeps the commands whose `fuzzyScore` over the resolved title (a function title called with `input.ctx`) is not `null`, scored by it, and ranks them; it keeps a command whose `when` fails (it is drawn disabled). An empty query keeps the commands whose `command.when(input.ctx)` holds (not `commandEnabled`: a stale snapshot still lists them, dimmed), ordered by title by code point and then by key by code point. Each becomes a `PaletteCommand`: `key` `command:<id>`, the resolved `title`, `command`, and `enabled` `commandEnabled(command, input.ctx)`.
  - Places: the candidates, in list order: for each `input.tabs` entry, when `input.ctx.session` is defined, a place keyed `place:<tab id>` titled by the tab's label, hinted by the session's title, linked by `hrefFor(tab.id, { id: String(session.id) })`; then `place:fleet` titled `Fleet` (`hrefFor("fleet")`), `place:new` titled `New session` (`hrefFor("new")`), `place:flows` titled `Flows` (`hrefFor("flows")`; only when the snapshot is not null and `hasShownFlows(snapshot)`), and `place:settings` titled `Settings` (`hrefFor("settings")`), each with an empty hint. A non-empty query keeps the places whose `fuzzyScore` over the title (never the hint) is not `null` and ranks them; an empty query keeps every candidate in this order.

  Update the header comment of `paletteGroups` if Task 2.3 left a "for now" note: none remains.

- [ ] **Step 4: Run it to see it pass.** `pnpm -C internal/web/ui exec vitest run src/desktop/palette.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print every test of the file green and no type or lint error.

- [ ] **Step 5: Commit.** `git commit -m "feat(web): build the palette's Commands and Places groups and assemble the palette" -- internal/web/ui/src/desktop/palette.ts internal/web/ui/src/desktop/palette.test.ts`

### Task 2.5: Rebuild the bundle and run the story's gates

**Files:**
- Modify: `internal/web/dist/` (rebuilt output; no hand edit)

- [ ] **Step 1: Build.** `pnpm -C internal/web/ui run build` writes the bundle into `internal/web/dist/`. `fleet/groups.ts` and `FleetHead.tsx` are in the entry, so its hashed name changes and `hasShownFlows` is the entry's only new code; `desktop/palette.ts` is imported by nothing yet, so it is in no chunk, and the entry's gzipped size moves by a few bytes at most.

- [ ] **Step 2: Run the story's checks.** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'` passes: `check` runs the typecheck, lint, format check and the whole vitest suite, `dist:check` finds the bundle fresh, and the Go package's `checkLazyViews` and first-load budget tests pass unchanged. If `format:check` flags a file of this story, run `pnpm -C internal/web/ui exec prettier --write <that file>` and rebuild.

- [ ] **Step 3: Run the repo's story gates.** `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi'`, then `CGO_ENABLED=0 go vet ./...`, then `CGO_ENABLED=0 go test -timeout 30m ./...` each exit 0.

- [ ] **Step 4: Commit.** `git add -N internal/web/dist/`, then `git commit -m "build(web): rebuild the bundle for the shared flows rule" -- internal/web/dist/`

## Story 3: The palette dialog, its chord, and the top bar's box

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/desktop/paletteState.ts`, `internal/web/ui/src/desktop/paletteState.test.ts`, `internal/web/ui/src/desktop/Palette.tsx`, `internal/web/ui/src/desktop/Palette.test.tsx`, `internal/web/ui/src/desktop/TopBar.tsx`, `internal/web/ui/src/desktop/TopBar.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `PaletteState`, `usePalette`, `isPaletteChord`, `usePaletteKey`, `paletteKeyLabel`, `PaletteBox`, `Palette`, the slots `palette`, `palette-input`, `palette-item` and `palette-box`; uses `paletteGroups`, `PaletteItem`, `PaletteGroup` (Story 2), `allCommands`, `confirmOpen` (Story 1), `useCommandContext`, `runCommand`, `useFleet`, `sessionTabs`, `Dialog`, `DialogContent`, `DialogTitle`, `Autocomplete`, `TopBar`, `HostLabel`, `WorkspaceFilter`, `setViewportWidth`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** brief (a dialog over a pure module and a third-party list; each behaviour a named test; Task 3.1, the chord listener refusing during a confirmation and its cleanup closing the palette, and Task 3.2, the palette closing before `runCommand` opens the confirmation, stay brief by spec decisions 16 and 17)

This story draws the palette and opens it. `desktop/paletteState.ts` holds the open store (`usePalette`), the chord (`isPaletteChord`), its one document listener (`usePaletteKey`) and the box's label (`paletteKeyLabel`); `desktop/Palette.tsx` is a Dialog around an inline Base UI Autocomplete over Story 2's `paletteGroups`, where Enter or a click opens a session or a place, or closes the palette and then runs the command through `runCommand` with the route's context, and a command `commandEnabled` refuses is a disabled item (spec §18, §20, Part 2 settlements, decisions 2, 6, 10 and 11). `desktop/TopBar.tsx` gains `PaletteBox`, which opens it. `desktop/DesktopShell.tsx` (as Story 1 leaves it, with its `import "@/commands/view";` line) calls `usePaletteKey()` before its `mockupFull` early return, so the chord works on every route, and draws `<Palette />` on both branches, so the store and the dialog never disagree; the listener's cleanup closes the palette, so one open when the window narrows below the desktop breakpoint (the shell unmounts) is closed when the desktop comes back. The chord toggles from anywhere, a text field included, and does nothing, after `preventDefault()`, while a confirmation is open (`confirmOpen()`, Story 1). Two behaviours rest on the sequence of two modals and carry their own tests: the palette closes before the command runs, so its confirmation shows alone, and the palette's closing returns no focus to the page behind it, so Story 1's Cancel focus on a destructive ask is not taken back. The tasks run so that the tree is green after each: the store and the chord, the dialog, the box, the shell's wiring, then the rebuilt bundle; nothing imports `Palette.tsx` until Task 3.4 and only `TopBar.tsx` (Task 3.3) imports `paletteState.ts` before it, all in the desktop chunk, so Tasks 3.1 and 3.2 change no screen. No code here holds a landmine marker, so the developer runs at the default effort. Every command below is run from the repo root. The app's `node_modules` tree appears with the install in Task 3.2's first step; Base UI's types are read there.

### Task 3.1: The open store, the chord, its listener and the box's label

**Files:**
- Create: `internal/web/ui/src/desktop/paletteState.ts`
- Create: `internal/web/ui/src/desktop/paletteState.test.ts`

- [ ] **Step 1: Write the failing tests.** `desktop/paletteState.test.ts` imports `act`, `renderHook` from `@testing-library/react`, `vi` and the vitest helpers, `askConfirm` and `testSeam` from `@/app/ConfirmSheet`, and `isPaletteChord`, `paletteKeyLabel`, `usePalette`, `usePaletteKey` from `./paletteState`. Helpers at the top: `chord(over: KeyboardEventInit = {}): KeyboardEvent` builds `new KeyboardEvent("keydown", { key: "k", ctrlKey: true, bubbles: true, cancelable: true, ...over })`; `withComposing(event)` returns the event after `Object.defineProperty(event, "isComposing", { value: true })` (happy-dom may ignore the init's `isComposing`, so the test sets the property itself); `press(target: EventTarget, event: KeyboardEvent): KeyboardEvent` dispatches inside `act` and returns the event, so a test reads its `defaultPrevented`. `beforeEach` calls `act(() => usePalette.getState().setOpen(false))`. `afterEach` answers any ask still open (`testSeam.openId()` not null: `testSeam.answer(id, null)`). A `confirm` spec `{ title: "Kill", body: "b", action: "Kill", destructive: true }` serves the confirmation test. Tests:
  - `starts closed at load`: `vi.resetModules()`, then `(await import("./paletteState")).usePalette.getState().open` is `false`.
  - `setOpen and toggle`: `setOpen(true)` gives `open` true, `toggle()` gives false, `toggle()` again gives true, `setOpen(false)` gives false.
  - `isPaletteChord accepts` (`it.each`): `{ key: "k", ctrlKey }`, `{ key: "k", metaKey }`, `{ key: "K", ctrlKey }` (caps lock) and `{ key: "K", metaKey }` are each true.
  - `isPaletteChord refuses` (`it.each`, each a case with its own name): `k` with no modifier; `Ctrl+Alt+K`; `Meta+Alt+K`; `Ctrl+Shift+K`; `Meta+Shift+K`; `Ctrl+J`; `Ctrl+Enter`; Ctrl+K while composing (`withComposing`), each false.
  - `paletteKeyLabel` (`it.each`): `MacIntel`, `Macintosh`, `iPhone` and `iPad` give `⌘K`; `Linux x86_64`, `Win32` and the empty string give `Ctrl K`.
  - `toggles the palette on the chord from a text field`: `renderHook(() => usePaletteKey())`; append an `<input>` created with `document.createElement("input")` to `document.body` and `focus()` it; `press(input, chord())` leaves `usePalette.getState().open` true and the event `defaultPrevented` true; a second `press(input, chord())` leaves it false (a second chord closes); a `press(document.body, chord({ ctrlKey: false, metaKey: true }))` opens it again. Remove the input at the end.
  - `refuses what is not the chord and leaves the key alone` (`it.each` over Alt, Shift, composing and a bare `k`): with the hook mounted, pressing the event on `document.body` leaves `open` false and `defaultPrevented` false (the key reaches the page).
  - `does nothing while a confirmation is open, and still prevents the browser's default`: mount the hook; `act(() => { void askConfirm(confirm); })`; `press(document.body, chord())` leaves `open` false and `defaultPrevented` true; answer the ask through `testSeam.answer(testSeam.openId() as number, null)` (read the id first, inside `act`), and `press(document.body, chord())` opens it.
  - `adds one keydown listener on the document and removes the same one on unmount`: spy `document.addEventListener` and `document.removeEventListener`; mount, rerender twice and unmount; exactly one `keydown` registration was added across the three renders, and the removal names the same function.
  - `stops listening after unmount`: unmount the hook; `press(document.body, chord())` leaves `open` false.
  - `closes the palette when it unmounts`: mount, `act(() => usePalette.getState().setOpen(true))`, unmount: `open` is false.

  Fails without: `starts closed at load`: an initial `open: true` turns it red. `setOpen and toggle`: a `toggle` that only opens, or a `setOpen` that ignores its argument, turns it red. `isPaletteChord accepts`: a test of `key === "k"` alone turns the `K` cases red, and `ctrlKey` alone turns the Meta ones red. `isPaletteChord refuses`: dropping the `altKey`, `shiftKey` or `isComposing` term turns its own case red (each term of the compound guard has a case), and a missing `key` test turns `Ctrl+J` and `Ctrl+Enter` red. `paletteKeyLabel`: a `/mac|iphone|ipad/` without the `i` flag turns `MacIntel`, `Macintosh` and `iPhone` red, and a regex missing `iphone` or `ipad` turns that case red. `toggles the palette on the chord from a text field`: a listener that returns early when `event.target` is an input, textarea or contenteditable turns it red, a missing `preventDefault()` turns its `defaultPrevented` assertion red, and a `setOpen(true)` in place of `toggle()` turns the second press red. `refuses what is not the chord…`: a `preventDefault()` placed before the chord test turns every case red on `defaultPrevented`. `does nothing while a confirmation is open…`: a missing `confirmOpen()` check opens the palette and turns it red, and a check placed before `preventDefault()` turns the `defaultPrevented` assertion red. `adds one keydown listener…`: an effect that re-runs on every render (a dependency that changes) registers a second listener and turns it red, and a missing removal turns it red. `stops listening after unmount`: a cleanup that does not remove the listener turns it red. `closes the palette when it unmounts`: a cleanup that only removes the listener turns it red.

- [ ] **Step 2: Run them to see them fail.** `pnpm -C internal/web/ui exec vitest run src/desktop/paletteState.test.ts` fails at the import: `./paletteState` cannot be resolved.

- [ ] **Step 3: Implement.** `desktop/paletteState.ts`, a module of the desktop chunk (nothing in the entry imports it), with a header comment saying so and why the chord is the app's only document key listener (spec decision 2, kept alone from the keys that left the epic):
  - `export interface PaletteState { open: boolean, setOpen: (open: boolean) => void, toggle: () => void }` and `export const usePalette` a Zustand store (`create<PaletteState>()`, as `useDesktop` does) holding `open` (false at load, in memory only: no localStorage), `setOpen` and `toggle` (`open` to its opposite).
  - `export function isPaletteChord(e: KeyboardEvent): boolean`: `(e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && !e.isComposing && (e.key === "k" || e.key === "K")`. Comment: `key`, not `code`, because spec decision 6 rejected a non-Latin layout's `code` fallback.
  - `export function usePaletteKey(): void`: a `useEffect` with an empty dependency list that adds one `keydown` listener to `document` and, as its cleanup, removes that listener and calls `usePalette.getState().setOpen(false)`. The listener: when `isPaletteChord(event)` is false, return; else `event.preventDefault()`; then return when `confirmOpen()` (import it from `@/app/ConfirmSheet`); else `usePalette.getState().toggle()`. It reads the store through `getState()`, so the effect has no dependency. Comment the cleanup: a palette open when `DesktopShell` unmounts would otherwise come back open.
  - `export function paletteKeyLabel(platform: string): string`: `/mac|iphone|ipad/i.test(platform) ? "⌘K" : "Ctrl K"`.

- [ ] **Step 4: Run them to see them pass.** `pnpm -C internal/web/ui exec vitest run src/desktop/paletteState.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print the file green and no type or lint error.

- [ ] **Step 5: Commit.** `git add -N internal/web/ui/src/desktop/paletteState.ts internal/web/ui/src/desktop/paletteState.test.ts`, then `git commit -m "feat(web): add the palette's open store and its chord" -- internal/web/ui/src/desktop/paletteState.ts internal/web/ui/src/desktop/paletteState.test.ts`

### Task 3.2: The palette dialog

**Files:**
- Create: `internal/web/ui/src/desktop/Palette.tsx`
- Create: `internal/web/ui/src/desktop/Palette.test.tsx`

- [ ] **Step 1: Install and read Base UI's types.** `pnpm -C internal/web/ui install --frozen-lockfile`. Read the `.d.ts` files of the installed `@base-ui/react` package: its `autocomplete` directory (the root and its input, list, item, group and group-label parts) and its `dialog` popup part, for: the grouped `items` form (a group is an object with an `items` array), whether `Autocomplete.Root`'s `value`/`onValueChange` hold the input's text, the `inline`, `open`, `filter` and `autoHighlight` props, how an item is given its value and `onClick`, the `disabled` item prop and the attribute a disabled item renders (`aria-disabled` or `data-disabled`), and the Dialog popup's `finalFocus` and `initialFocus` props. The names this story uses are the ones the files declare; where one differs from the names below, use the file's and say so in the report.

- [ ] **Step 2: Write the failing tests.** `desktop/Palette.test.tsx` imports `act`, `render`, `screen`, `waitFor`, `within` from `@testing-library/react`, `userEvent`, `defineCommand` from `@/app/commands`, `ConfirmSheet`, `confirmOpen` and `testSeam` from `@/app/ConfirmSheet`, `useDesktop` from `@/app/desktopState`, `definePanel` from `@/app/panels`, `fixtureSnapshot` from `@/fixtures`, `useFleet` from `@/store/fleet`, `@/commands/view` (side-effect import, for the two toggles), `Palette` from `./Palette` and `usePalette` from `./paletteState`. The canned fleet is `fixtureSnapshot()`: workspace 1 `loom` holds session 3 `plain-shell`, 2 `sandbox-ask` and 7; workspace 2 `flow` holds 4 `flow-docs` (needs you 52m, then 2: 18m); `Settings` is a place. The file defines, at module level, three commands over a `vi.fn()` each: `test.palette.run` titled `Zebra run` (`palette: true`, `when: () => true`, `run` records `usePalette.getState().open` and its ctx into a `seen` array, then calls the spy), `test.palette.never` titled `Aardvark never` (`palette: true`, `when: () => false`) and `test.palette.kill` titled `Zebra kill` (`palette: true`, `when: () => true`, `confirm` returning `{ title: "Kill zebra", body: "b", action: "Kill", destructive: true }`, `run` calling a spy). It defines two tab panels as `DesktopShell.test.tsx`'s `tabbed()` does (`turns` labelled `conversation` and `info` labelled `info`, bodies `() => null`). `setup(hash = "#/s/3", over = {})` replaces the history entry with `hash`, sets the store (`snapshot: fixtureSnapshot()`, `stale: false`, `link: "live"`, `updated: Date.now()`, with `over` merged), sets `useDesktop` to `{ sidebar: true, inspector: true }`, renders `<Palette />` and `<ConfirmSheet variant="dialog" />`, and `act`s `usePalette.getState().setOpen(true)`. `afterEach` closes the palette, answers any open ask, resets the fleet store and clears the hash. Helpers: `items()` is the `[data-slot="palette-item"]` elements; `titles()` their `textContent`'s title part (read from each item's first child, so a hint is not part of it); `input()` is the `[data-slot="palette-input"]` element. Tests:
  - `draws nothing while closed`: render with the store closed; `document.querySelector('[data-slot="palette"]')` is null.
  - `draws a dialog named for the palette with the input focused`: `screen.getByRole("dialog", { name: "Command palette" })` exists, its popup is `[data-slot="palette"]`, and `input()` `toHaveFocus()` (`waitFor`).
  - `with an empty query lists the sessions that need you, then the commands, then the places`: the group labels (`Sessions`, `Commands`, `Places`, the text of each group's heading) are in that order in the document; `data-kind` of every item reads as session items, then command items, then place items, with no interleaving; the session titles are `["flow-docs", "sandbox-ask"]`; the places include `Fleet`, `New session` and `Settings` and, for `#/s/3`, the tabs `conversation` and `info`.
  - `draws each item with its kind`: every item has `data-kind` one of `session`, `command`, `place`, and a session item's text holds both its title and its hint (`loom · aidanb/plain-shell` for `plain-shell`, found by a query `plain-shell`).
  - `a query narrows the items as paletteGroups does`: type `plain-shell`; the items are one session titled `plain-shell`, no command and no `Commands` label, and `Sessions` is the only group label.
  - `starts each opening with an empty query`: type `plain-shell`; `act` the store closed and wait for the dialog to unmount; open it again: `input()` has value `""` and the items are the empty query's list.
  - `Esc in the input with a query closes the palette`: type `plain-shell`; `userEvent.keyboard("{Escape}")`; `usePalette.getState().open` is false and the popup is gone (`waitFor`).
  - `Esc with an empty query closes it too`: the same with no text.
  - `Enter on a session goes to its link and closes the palette`: type `plain-shell`; `userEvent.keyboard("{Enter}")`; `window.location.hash` is `#/s/3`, `open` is false.
  - `a click on a session does the same`: type `sandbox-ask`; click the one item; the hash is `#/s/2`, `open` is false.
  - `Enter on a place goes to its link and closes the palette`: type `Settings`; Enter; the hash is `#/settings`, `open` false. A second test, `Enter on a tab place goes to its link`, runs over a fleet that holds only session 3, so that no session, command or other place can match the query ahead of the tab: `setup("#/s/3", { snapshot: onlySession3() })`, where `onlySession3()` is a module-level helper returning `{ ...fixtureSnapshot(), sessions: Object.fromEntries(Object.entries(fixtureSnapshot().sessions).map(([workspace, list]) => [workspace, list.filter((s) => s.id === 3)])) }` (the fixture's other sessions are gone; `plain-shell`, branch `aidanb/plain-shell` and workspace `loom` hold no `f`, so `info` matches none of them, and no command title (`Show or hide the sidebar`, `Show or hide the inspector`, `Zebra run`, `Aardvark never`, `Zebra kill`) or other place (`conversation`, `Fleet`, `New session`, `Settings`) holds i, n, f, o in order). It types `info`, asserts the one item is the place titled `info` (`data-kind` `place`), then Enter: the hash is `#/s/3/info` and `open` is false. The fixture's session 7, whose title holds i, n, f, o in order, would take the highlight in the full fleet, which is why this test narrows the fleet.
  - `Enter on a command closes the palette, then runs it through runCommand with the route's context`: type `Zebra run`, Enter; `waitFor` the spy called once; the `seen` entry holds `open` false (the palette was closed when `run` started) and a ctx whose `session?.id` is 3 and `panel` is `turns`; `window.location.hash` is still `#/s/3`.
  - `a click on a command runs it the same way`: type `Zebra run`, click the item; the spy is called once and `open` is false.
  - `a command with a confirmation asks first, with the palette already closed`: type `Zebra kill`, Enter; `waitFor(confirmOpen)` is true; the kill spy has not run; `open` is false; answering through the seam with `""` (`testSeam.answer(testSeam.openId() as number, "")`) runs the spy once.
  - `leaves focus in the confirmation, not on the page behind the palette`: render an `<input aria-label="behind">` before `setup`'s render (inside the same container) and `focus()` it, then open the palette; type `Zebra kill`, Enter; `waitFor` the dialog `Kill zebra`'s `Cancel` button to have focus (Story 1: a destructive ask opens on Cancel); wait 100 ms (`await act(() => new Promise((r) => setTimeout(r, 100)))`); `Cancel` still has focus and `behind` does not.
  - `a command whose when fails is a disabled item that does nothing`: type `Aardvark never`; the one item reads as disabled (read the attribute the real render sets, `aria-disabled="true"` or `data-disabled`, from Step 1's finding, and assert that one by name); Enter and a click leave its spy uncalled, and `open` stays true.
  - `while the snapshot is stale, a session command is disabled and a view toggle still runs`: `setup("#/s/3", { stale: true, link: "offline" })`; type `Zebra run`: the item is disabled, Enter does not call its spy, `open` stays true. Clear the input and type `sidebar`: the `Show or hide the sidebar` item is not disabled; Enter flips `useDesktop.getState().sidebar` to false and closes the palette.
  - `the popup carries palette, not dialog-content`: the popup's `data-slot` is `palette`; `document.querySelector('[data-slot="dialog-content"]')` is null.

  Fails without: `draws nothing while closed`: a `Dialog` with `open` always true turns it red. `draws a dialog named…`: a missing `DialogTitle`, a title other than `Command palette`, or an input without `autoFocus`/the Autocomplete's own focus turns it red. `with an empty query lists…`: groups in another order, a listing of every session instead of the needs-you ones, or places before commands turns it red. `draws each item with its kind`: a missing `data-kind`, or an item without the hint, turns it red. `a query narrows…`: an Autocomplete left on its own filter (no `filter={null}`) filters by the item's text and the grouped items come back unfiltered or doubly filtered, turning it red, and a query that is not passed to `paletteGroups` turns it red. `starts each opening with an empty query`: a query held in state above the dialog, or kept across a close, turns it red. `Esc in the input with a query closes…`: an input that swallows Escape (clearing the text) without closing turns it red, and so does a dialog whose `onOpenChange` does not call `setOpen`. `Esc with an empty query…`: the same. `Enter on a session…` and `a click on a session…`: an `onClick` that does nothing for the kind, or a link built by hand, turns them red; leaving the palette open turns the `open` assertion red. `Enter on a place…` and `Enter on a tab place…`: a navigation that skips places, or the wrong link for a tab (`#/s/3/info`), turns them red. `Enter on a command closes…`: running before `setOpen(false)` turns the `seen` assertion red, and a ctx taken from nowhere (not the route's) turns the ctx assertion red. `a click on a command…`: an `onClick` only for Enter turns it red. `a command with a confirmation asks first…`: calling `command.run` directly skips the sheet, so the spy is called before the seam answers and the test is red. `leaves focus in the confirmation…`: a popup without `finalFocus` disabled returns focus to the `behind` input when it closes, so Cancel loses focus and the test is red. `a command whose when fails…`: an item that is not `disabled` runs, or opens, and turns it red. `while the snapshot is stale…`: an `enabled` read from `when` alone, or a disabled check that also refuses an `offline` command, turns it red. `the popup carries palette…`: a `data-slot` placed on an inner element, or left as `dialog-content`, turns it red.

- [ ] **Step 3: Run them to see them fail.** `pnpm -C internal/web/ui exec vitest run src/desktop/Palette.test.tsx` fails at the import: `./Palette` cannot be resolved.

- [ ] **Step 4: Implement.** `desktop/Palette.tsx`, a module of the desktop chunk with a header comment (spec §18, §20, Part 2 settlements 10), named export `Palette(): ReactElement`:
  - Reads `usePalette` (`open`, `setOpen`), `useCommandContext()`, `useFleet((s) => s.snapshot)` and the tabs: `useMemo` over `ctx.session` of `sessionTabs(ctx.session)` mapped to `{ id: panel.id, label: panel.tab?.label ?? panel.id }`, empty when `ctx.session` is undefined, typed `PaletteTab[]` (Story 2).
  - Draws `Dialog` (`@/components/ui/dialog`) with `open` and `onOpenChange={setOpen}`, and a `DialogContent` whose `data-slot="palette"` replaces the default (the popup spreads its props after its own slot), with `className` overriding `max-w-md` and `p-4` (a wider popup with no padding, `max-w-xl p-0`, its colours the tokens `bg-card`, `border-border`, `text-card-foreground`, `text-muted-foreground`; no literal colour, no `style` attribute) and `finalFocus` set to `false` (Step 1's finding on the prop's type): the palette's closing then returns focus to nothing, so a confirmation that opens right after it keeps its own focus. Comment the prop with that reason. A `DialogTitle` named `Command palette`, visually hidden with `sr-only`.
  - The body is a separate component drawn inside `DialogContent`, so its query state is fresh each opening (the popup unmounts when closed); it also resets the query when `open` becomes true, so a reopen during the closing transition starts empty too. It holds the query in state, calls `paletteGroups({ query, snapshot, commands: allCommands(), ctx, tabs })` and draws, inside `Autocomplete.Root` imported from `@base-ui/react/autocomplete` (`inline`, `open`, `filter={null}`, `autoHighlight`, the groups as its grouped `items`, the input's text as the controlled query): the input (`Autocomplete.Input`, `data-slot="palette-input"`, placeholder `Search sessions, commands, places`, form text left at the desktop's `text-sm`: the dialog is desktop-only), and the list with an `Autocomplete.Group` and `Autocomplete.GroupLabel` per group (the label the group's `label`), and one `Autocomplete.Item` per item, `data-slot="palette-item"`, `data-kind` its `kind`, drawing the item's `title` as its first child and, for a session or place with a non-empty `hint`, the hint after it in muted text. A command item whose `enabled` is false is `disabled` and drawn with `opacity-50`; every other item is enabled. An empty result draws a short muted line (`Nothing matches`) in place of the list.
  - Each item's `onClick` calls one `choose(item)` of this module: for a `session` or `place` item, `setOpen(false)` then `window.location.assign(item.href)`; for a `command` item, `setOpen(false)` first, then `void runCommand(item.command, ctx, undefined)` (`@/app/commands`) with the `ctx` of the render, so the confirmation, the in-flight key and the toasts behave as on a button and the confirmation shows alone. `choose` does nothing for a `disabled` command, which Base UI's `disabled` item already refuses; the guard is a second line, not the only one.
  - Escape: write nothing for it first; run Step 5, and only if `Esc in the input with a query closes the palette` fails with the Dialog's own handling alone, add an `onKeyDown` on the input that calls `setOpen(false)` on `Escape` (and `preventDefault()`), with a comment saying the inline, forced-open Autocomplete's input handles Escape before the Dialog sees it.
  - Do not import `Palette.tsx` anywhere yet; Task 3.4 does.

- [ ] **Step 5: Run them to see them pass.** `pnpm -C internal/web/ui exec vitest run src/desktop/Palette.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print the file green and no type or lint error. If a test fails because happy-dom's `window.location.assign` does not move the hash, report it and read the hash through `window.location.href`; do not replace the call with `location.hash =`, which spec decision 10 does not name.

- [ ] **Step 6: Commit.** `git add -N internal/web/ui/src/desktop/Palette.tsx internal/web/ui/src/desktop/Palette.test.tsx`, then `git commit -m "feat(web): add the palette dialog over the pure items" -- internal/web/ui/src/desktop/Palette.tsx internal/web/ui/src/desktop/Palette.test.tsx`

### Task 3.3: The top bar's box

**Files:**
- Modify: `internal/web/ui/src/desktop/TopBar.tsx` (the header comment, the import block, a new `PaletteBox`, and the row)
- Test: `internal/web/ui/src/desktop/TopBar.test.tsx` (a new `describe("PaletteBox", …)` at the end; one line in the file's `afterEach`)

- [ ] **Step 1: Write the failing tests.** In `TopBar.test.tsx` import `usePalette` from `./paletteState` and `PaletteBox` with `TopBar` from `./TopBar`; add `usePalette.setState({ open: false })` to the file's `afterEach`. Add `describe("PaletteBox")` with a helper `withPlatform(platform: string)` that `vi.spyOn(window.navigator, "platform", "get")` returns it (restore in the describe's `afterEach` with `vi.restoreAllMocks()`). Tests:
  - `draws a button with the prompt and the Ctrl chord on a non-Mac`: `withPlatform("Linux x86_64")`, render `<PaletteBox />`: the element `[data-slot="palette-box"]` is a `button`, its text holds `Search sessions, commands, places` and `Ctrl K`, and does not hold `⌘K`.
  - `shows ⌘K on a Mac, an iPhone and an iPad` (`it.each`): with each platform the text holds `⌘K` and not `Ctrl K`.
  - `a click opens the palette`: render, click the button: `usePalette.getState().open` is true.
  - `a click on an open palette's box leaves it open`: set `open` true first, click: still true (`setOpen(true)`, not `toggle`).
  - `is dense and about 16rem wide`: the box's class holds `w-64` and `flex-none`, and not `min-h-[44px]`.
  - `the top bar draws it after loom@host and before the workspace filter` (in `describe("TopBar")`'s style, using `load()`): the box's position in the bar is after the host label's (`getByText("fresco")` ancestor) and before the `Workspaces` group (`compareDocumentPosition` both ways).
  - The existing `draws every part dense` test (no `min-h-[44px]` anywhere in the bar) now covers the box too.

  Fails without: `draws a button with the prompt…`: a `div`, or a missing `data-slot`, turns it red; a label that always says `⌘K` turns the Ctrl assertion red. `shows ⌘K on a Mac…`: a label read from `navigator.userAgent` or hard-coded turns it red. `a click opens the palette`: a box that calls `toggle` turns the second test red, and one with no handler turns the first red. `is dense and about 16rem wide`: a box without `flex-none` (which `HostLabel`'s `flex-1` would squeeze) or with the 44px minimum turns it red. `the top bar draws it after…`: a box placed after the filter, or before the host, turns it red. `draws every part dense`: a `min-h-[44px]` class on the box turns it red.

- [ ] **Step 2: Run them to see them fail.** `pnpm -C internal/web/ui exec vitest run src/desktop/TopBar.test.tsx` fails: `PaletteBox` is not exported from `./TopBar` (`is not a function` at run, a type error under the typecheck).

- [ ] **Step 3: Implement.** In `desktop/TopBar.tsx`:
  - Import `paletteKeyLabel` and `usePalette` from `./paletteState`.
  - Export `function PaletteBox(): ReactElement`: a `button` (`type="button"`, `data-slot="palette-box"`, `onClick` calling `usePalette.getState().setOpen(true)`), classes `flex w-64 flex-none items-center justify-between gap-2 rounded-md border border-border px-2 py-0.5 text-left text-muted-foreground hover:text-foreground` (token colours only; no 44px minimum), holding the text `Search sessions, commands, places` (in a `truncate` span: `block` or a flex child, as the package's `truncate` rule needs) and, in a second span with the frame's `kbd` look (`rounded border border-border px-1 font-mono text-[10px]`), `paletteKeyLabel(navigator.platform)`.
  - In `TopBar`, draw `<PaletteBox />` after `<HostLabel dense />` and before `<WorkspaceFilter dense />`.
  - Update the file's header comment: the palette's box now sits between the host and the filter, and replace the sentence saying it comes with the palette.

- [ ] **Step 4: Run them to see them pass.** `pnpm -C internal/web/ui exec vitest run src/desktop/TopBar.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print the file green and no type or lint error.

- [ ] **Step 5: Commit.** `git commit -m "feat(web): add the palette's box to the desktop top bar" -- internal/web/ui/src/desktop/TopBar.tsx internal/web/ui/src/desktop/TopBar.test.tsx`

### Task 3.4: Wire the chord and the dialog into the desktop shell

**Files:**
- Modify: `internal/web/ui/src/desktop/DesktopShell.tsx` (the import block and `DesktopShell`'s body, from `DesktopShell`'s `const session = useSession(…)` line to its closing `);`)
- Test: `internal/web/ui/src/desktop/DesktopShell.test.tsx` (a new `describe("DesktopShell's palette", …)` at the end of the file; two lines in the file's `afterEach`)

- [ ] **Step 1: Write the failing tests.** In `DesktopShell.test.tsx`, as Story 1 leaves it, add `usePalette` from `./paletteState` to the imports and `usePalette.setState({ open: false })` to the file's `afterEach`. The file's `desktopAt`, `live`, `tabbed` and `slot` helpers serve. In the new `describe`:
  - `the chord opens the palette on a session route, and a second chord closes it`: `tabbed(); live(); desktopAt("#/s/100")`; `await screen.findByText("outlet")`; `fireEvent.keyDown(document, { key: "k", ctrlKey: true })`; `await waitFor` `slot("palette")` is in the document; a second keyDown closes it (`waitFor` it is gone).
  - `the chord opens it from a text field the screen holds`: `desktopAt("#/s/100", <input aria-label="reply" />)`; focus the input; `fireEvent.keyDown(input, { key: "k", metaKey: true })` opens the palette.
  - `the chord and the palette work on the full mockup, which draws no chrome`: `desktopAt("#/s/100/mockup/full")`; after the screen text, `slot("desktop")` is null; `fireEvent.keyDown(document, { key: "k", ctrlKey: true })` makes `slot("palette")` appear, and `usePalette.getState().open` is true.
  - `the top bar's box opens the palette`: on `#/s/100`, click `slot("palette-box")`; `slot("palette")` appears.
  - `a palette open when the shell unmounts is closed, and not drawn when it returns`: open it through the chord, `unmount()` the render, then `usePalette.getState().open` is false; render `desktopAt("#/s/100")` again: `slot("palette")` is null.
  - `a chord pressed while a confirmation is open does not open it`: `act(() => { void askConfirm(spec) })` (import `askConfirm`, `testSeam`), keyDown the chord: `slot("palette")` is null and `usePalette.getState().open` false; answer the ask in `afterEach`'s cleanup (answer through the seam at the end of the test).
  - `the palette lists the sessions of the shell's fleet`: `tabbed(); live(); desktopAt("#/s/100")`; read `title` from `allSessions(aSnapshot())[0]` (throw when it is missing); open the palette through the chord (`fireEvent.keyDown(document, { key: "k", ctrlKey: true })`, then `waitFor` `slot("palette")`); the empty query lists only the sessions that need you, and session 100 does not, so type its title: `fireEvent.change(slot("palette-input"), { target: { value: title } })`; then `waitFor` an item `[data-slot="palette-item"][data-kind="session"]` is drawn whose text holds `title`, proving the shell's palette reads the store.

  Fails without: `the chord opens the palette on a session route…`: a shell that does not call `usePaletteKey` or draw `Palette` turns it red; a listener that only opens (never closes) turns the second half red. `the chord opens it from a text field…`: an effect in the shell restricted by target turns it red. `the chord and the palette work on the full mockup…`: a `usePaletteKey()` or a `Palette` placed after the `mockupFull` return (the early return skips both) turns it red; the hook placed after the return would also break the rules of hooks when the route changes. `the top bar's box opens the palette`: a `TopBar` without the box (no click target), or a box whose click does not reach `usePalette`, turns it red. `a palette open when the shell unmounts…`: a cleanup that does not close the palette leaves `open` true and turns it red. `a chord pressed while a confirmation is open…`: a missing `confirmOpen()` check turns it red. `the palette lists the sessions…`: a `Palette` drawn without the store's snapshot (a null snapshot gives no sessions, so the typed title matches no item) turns it red.

- [ ] **Step 2: Run them to see them fail.** `pnpm -C internal/web/ui exec vitest run src/desktop/DesktopShell.test.tsx` fails the new tests: `slot("palette")` is null after the chord (`expected null not to be null`).

- [ ] **Step 3: Implement.** In `desktop/DesktopShell.tsx` (as Story 1 leaves it):
  - Add `import { Palette } from "./Palette";` and `import { usePaletteKey } from "./paletteState";` to the relative-import group, keeping its alphabetical order (`Inspector`, `Palette`, `paletteState`, `Sidebar`, `TopBar`).
  - In `DesktopShell`, call `usePaletteKey();` after `const session = useSession(panelProps(route).sessionId);` and before the line `if (route.panel === "mockupFull") return <Outlet />;`, with a comment that the hook runs before the early return so the chord works on every route and that Palette is drawn on both branches so the store and the dialog never disagree.
  - Replace the early return's `<Outlet />` with a fragment holding `<Outlet />` and `<Palette />`, and draw `<Palette />` as the last child of the `data-slot="desktop"` frame `div`, after the row holding the sidebar, the middle and the inspector. Change nothing else in the file.

- [ ] **Step 4: Run them to see them pass.** `pnpm -C internal/web/ui exec vitest run src/desktop src/app/shell.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint` print every desktop test green (the existing `draws the full mockup alone, with no chrome` among them: the closed palette draws nothing) and no type or lint error.

- [ ] **Step 5: Commit.** `git commit -m "feat(web): bind the palette's chord and draw it in the desktop shell" -- internal/web/ui/src/desktop/DesktopShell.tsx internal/web/ui/src/desktop/DesktopShell.test.tsx`

### Task 3.5: Rebuild the bundle and run the story's gates

**Files:**
- Modify: `internal/web/dist/` (rebuilt output; no hand edit)

- [ ] **Step 1: Build.** `pnpm -C internal/web/ui run build` writes the bundle into `internal/web/dist/`. The palette, its store and the Autocomplete land in `DesktopShell`'s chunk; the entry script changes only in its hashed name, since `paletteState.ts` and `Palette.tsx` are imported by the shell alone.

- [ ] **Step 2: Run the story's checks.** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'` passes: `check` runs the typecheck, lint, format check and the whole vitest suite, `dist:check` finds the bundle fresh, and the Go package's `checkLazyViews`, first-load budget and CSP tests pass with `checkLazyViews` unchanged (the palette is in the desktop chunk, not the entry). If `format:check` flags a file of this story, run `pnpm -C internal/web/ui exec prettier --write <that file>` and rebuild.

- [ ] **Step 3: Run the repo's story gates.** `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi'`, then `CGO_ENABLED=0 go vet ./...`, then `CGO_ENABLED=0 go test -timeout 30m ./...` each exit 0.

- [ ] **Step 4: Commit.** `git add -N internal/web/dist/`, then `git commit -m "build(web): rebuild the bundle for the palette" -- internal/web/dist/`

## Story 4: webshot renders the palette at both desktop sizes

- **Developer:** intermediate-dev
- **Files:** `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `tools/CLAUDE.md`
- **Interfaces:** exposes the `palette` screen, `screenMode`'s `desktopOnly`, `press` and `drawn`, `page.press`; uses the `palette` slot and the chord (Story 3), `forDevice`, `shoot`, `page.query`, `cdpClient.call`
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ ./tools/claudemd/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -screens palette -out "$d/w" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -screens palette -out "$d/n" && test -s "$d/w/palette.png" && test -s "$d/n/palette.png"'`
- **Review:** light (a test tool's screen and a key press)
- **Form:** brief (a screen entry, a mode and a CDP call; Task 4.2, the frame wait, the single press and the slot wait against the browser, stays brief by spec decisions 16 and 17)

This story gives `tools/webshot` a `palette` screen: the app at `#s/1`, after ⌘K's twin `Control+k` has opened Story 3's palette, so the owner (and CI, through `.github/workflows/web.yml`, which already renders every screen at the phone and both desktop sizes and does not change) sees it at 1440×900 and 1100×800. `screenMode` gains `desktopOnly`, `press` and `drawn`; `shoot` waits for the desktop frame, settles, presses the chord once through CDP and waits for the palette's slot, failing the screen when either never appears (spec, Part 2 settlements, decision 12; the skeleton review's F1, because `appDrawn` is true on the phone shell's splash and on the lazy shell's fallback, where no listener exists yet). A phone skips the screen as a desktop skips `add-to-home`, and so does the classic app (F5: it has no desktop layout, and `-app` defaults to `classic`, so `-device desktop` with every screen must still pass); each skip line names its reason. The tasks run in this order so the tree is green after each: the screen, its mode, the skips and the rule's renamed test together with `tools/CLAUDE.md` (the rule names the test, and the lint fails on a name that does not resolve); then the chord, `page.press` and the press-and-wait in `shoot`; then the real-browser checks and the story's gates. The code is in one Go package and holds none of the profile's landmine markers, so the developer runs at the default effort. Every command below runs from the repo root, and none runs loom or a browser except Task 4.3's, which needs a Chromium on `PATH` and the `internal/web/dist` bundle Story 3 rebuilt.

### Task 4.1: The `palette` screen, its mode, and the skips

**Files:**
- Modify: `tools/webshot/main.go` (the `screens` list and its comment, `screenMode` and `screenModes`, `forDevice`, and the skip lines and empty check in `run`)
- Modify: `tools/CLAUDE.md` (the webshot device rule, line 20)
- Test: `tools/webshot/webshot_test.go`

Contract names this task defines: `screenMode`'s `desktopOnly`, `press` and `drawn`, the `palette` screen, and `forDevice`'s new rule. Names of its own: `forApp(app string, todo []screen) []screen`, `skipReason(d device, app string, sc screen) string`, and the test helper `captureStdout`. It uses `screen`, `screens`, `modeOf`, `devices`, `defaultDevice`, `pickScreens` and `run`.

- [ ] **Step 1: Write the failing tests.** In `tools/webshot/webshot_test.go`:
  - In `TestScreens_PairIsShotUnpairedAndAddToHomeAsABrowserTab`, change the guard line `if sc.name != "pair" && sc.name != "add-to-home" {` to also spare `palette` (`&& sc.name != "palette"`), so the loop that pins every other screen's zero mode does not fail on the new one. Change nothing else in that test.
  - New `TestScreens_PaletteIsDesktopOnlyAndPressesTheChord`: `screens` contains `screen{"palette", "#s/1"}`, and `modeOf(screen{"palette", "#s/1"})` equals `screenMode{desktopOnly: true, press: "Control+k", drawn: "[data-slot=\"palette\"]"}` (the Go string `[data-slot="palette"]`). Also `pickScreens("palette")` returns exactly that one screen.
  - Rename `TestForDevice_DropsOnlyAddToHomeOnADesktop` to `TestForDevice_DropsAddToHomeOnADesktopAndThePaletteOnAPhone` and extend its body (the lines between its braces; keep its existing assertions for the two desktops as they are, since a desktop still drops exactly `add-to-home` and keeps `palette`, but add `assert.Contains(t, got, screen{"palette", "#s/1"}, name)` in that loop). Add a loop over `[]string{"iphone15", "iphone16"}`: `forDevice(devices[name], screens)` equals `screens` without the `palette` screen (so `len(screens)-1` and it still holds `screen{"add-to-home", ""}`). Replace the line `assert.Equal(t, screens, forDevice(devices[defaultDevice], screens))`, which no longer holds, with `assert.Equal(t, forDevice(devices["iphone15"], screens), forDevice(devices[defaultDevice], screens))`. Add `assert.Empty(t, forDevice(devices["iphone15"], []screen{{"palette", "#s/1"}}))` beside the existing `assert.Empty(t, forDevice(devices["desktop"], []screen{{"add-to-home", ""}}))`. The existing `assert.Contains(t, screens, screen{"add-to-home", ""}, "screens is not changed")` stays and gains a sibling for `palette`.
  - New `TestForApp_DropsDesktopOnlyScreensUnlessTheAppIsNext`: `forApp("next", screens)` equals `screens`; `forApp("classic", screens)` and `forApp("", screens)` each equal `screens` without `palette`; `forApp("classic", []screen{{"palette", "#s/1"}})` is empty; `screens` itself still holds `palette` afterwards.
  - A helper `captureStdout(t *testing.T, fn func()) string` that swaps `os.Stdout` for the write end of an `os.Pipe`, runs `fn`, restores `os.Stdout` (also in `t.Cleanup`), closes the writer and returns what the pipe held (read it in a goroutine so a full pipe cannot block `fn`).
  - New `TestRun_SkipLinesNameTheirReason`, three subtests, each calling `run` through `captureStdout` with `-browser no-such-browser-binary` and `-out t.TempDir()`, so `run` prints its skip lines, then fails on the missing browser (assert `run` returned an error, and that the output holds the expected line and no skip line for the screen kept):
    - `a browser-tab screen on a desktop`: `-device desktop -app next -screens add-to-home,fleet`: the output contains `add-to-home\tskipped: phone only` and not `fleet\tskipped`.
    - `a desktop-only screen on a phone`: `-device iphone15 -app next -screens palette,fleet`: contains `palette\tskipped: desktop only` and not `fleet\tskipped`.
    - `a desktop-only screen under the classic app`: `-device desktop -screens palette,fleet` (no `-app`, so `classic`): contains `palette\tskipped: next app only` and not `fleet\tskipped`.
  - New `TestRun_PaletteAloneOnAPhoneOrTheClassicAppRendersNothing`: `run` with `-device iphone15 -app next -screens palette` and with `-device desktop -screens palette` each fails with an error containing `no screen to render on` and not `no-such-browser-binary` (pass `-browser no-such-browser-binary`, so the check is shown to run before a browser is looked for, as `TestRun_AddToHomeAloneOnADesktopRendersNothing` does for `add-to-home`).

  Fails without: `TestScreens_PaletteIsDesktopOnlyAndPressesTheChord`: dropping the `palette` entry from `screens`, or from `screenModes`, or changing the chord or the selector turns it red. `TestForDevice_DropsAddToHomeOnADesktopAndThePaletteOnAPhone`: a `forDevice` that tests only `browserTab` (today's) keeps `palette` on the phone and turns the phone loop red; one that drops every non-`desktopOnly` screen on a desktop turns the desktop loop red. `TestForApp_DropsDesktopOnlyScreensUnlessTheAppIsNext`: a `forApp` that is the identity, or that tests `app == "classic"` instead of `app != "next"` (so `""` keeps `palette`), turns it red. `TestRun_SkipLinesNameTheirReason`: a `run` that prints the old fixed `skipped: phone only` for every dropped screen turns the second and third subtests red, one that prints a reason for `fleet` turns the `NotContains` red. `TestRun_PaletteAloneOnAPhoneOrTheClassicAppRendersNothing`: a `run` that applies `forApp` after the browser lookup, or not at all, reaches the browser error and turns it red.

- [ ] **Step 2: Run them to see them fail.** `CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestScreens_|TestForDevice_|TestForApp_|TestRun_SkipLines|TestRun_PaletteAlone|TestPickScreens'` does not compile: `unknown field desktopOnly in struct literal of type screenMode`, and `undefined: forApp`.

- [ ] **Step 3: Implement.** In `tools/webshot/main.go`:
  - `screenMode` becomes `struct{ unpaired, browserTab, desktopOnly bool; press, drawn string }`, its comment extended: `desktopOnly` is a screen only the desktop layout draws, `press` a chord pressed once the desktop frame has drawn (`Control+k`, with `Alt+`, `Control+`, `Meta+` and `Shift+` prefixes allowed), `drawn` the selector that must match after the press. Add to `screenModes` the entry `"palette": {desktopOnly: true, press: "Control+k", drawn: "[data-slot=\"palette\"]"}`.
  - Add `{"palette", "#s/1"}` to `screens` between `{"settings", "#settings"}` and `{"pair", "#pair"}`, and a sentence to the comment above `screens`: "palette" is session 1's route with the command palette opened by its chord, shot on a desktop under `-app next` only.
  - `forDevice` keeps its signature and its "todo is not changed" promise. For a desktop it drops the screens whose mode has `browserTab`; for a phone it drops the screens whose mode has `desktopOnly`. Update its comment to say both, and that it tests the modes, not the names. Add `forApp(app string, todo []screen) []screen`, with a comment: it is todo, or without the `desktopOnly` screens unless `app` is `next` (the classic app has no desktop layout and the `-app` flag defaults to it); todo is not changed.
  - Add `skipReason(d device, app string, sc screen) string`: `phone only` for a `browserTab` screen on a desktop, else `desktop only` for a `desktopOnly` screen on a phone, else `next app only` for a `desktopOnly` screen when `app` is not `next`, else the empty string. Its comment says the device's reason comes first and that it is the words of the skip line.
  - In `run`, replace the block that sets `kept := forDevice(d, todo)` and prints `%s\tskipped: phone only\n` with: `kept := forApp(*app, forDevice(d, todo))`; for each screen of `todo` not in `kept`, print `%s\tskipped: %s\n` with its name and `skipReason(d, *app, sc)`. Keep the `slices.Contains` test already there. Change the empty-check error to `fmt.Errorf("no screen to render on %s with -app %s", d.name, *app)`, which keeps the text `no screen to render on desktop` that `TestRun_AddToHomeAloneOnADesktopRendersNothing` reads.
  - In `tools/CLAUDE.md` line 20, make exactly two replacements, which leave the line at 1,192 of its 1,200 bytes: replace `nothing drawn over the screenshot, and `add-to-home` skipped on them.**` with `nothing drawn over the screenshot, `add-to-home` skipped on them, and `palette` shot only on a desktop under `-app next`.**`, and replace `TestForDevice_DropsOnlyAddToHomeOnADesktop` with `TestForDevice_DropsAddToHomeOnADesktopAndThePaletteOnAPhone`. Nothing else in that file changes.

- [ ] **Step 4: Run them to see them pass.** `CGO_ENABLED=0 go test ./tools/webshot/ ./tools/claudemd/` prints `ok` for both: the new tests, the existing `TestRun_AddToHomeAloneOnADesktopRendersNothing` and the rest of the webshot suite pass, and the CLAUDE.md lint finds the renamed test and the line under its byte budget. Then `awk '{ if (length($0) > 1200) print NR }' tools/CLAUDE.md` prints nothing (run it with `LC_ALL=C` so `length` counts bytes).

- [ ] **Step 5: Commit.** `git commit -m "feat(webshot): a desktop-only palette screen, skipped on a phone and under the classic app" -- tools/webshot/main.go tools/webshot/webshot_test.go tools/CLAUDE.md`

### Task 4.2: The chord, `page.press`, and the press-and-wait in `shoot`

**Files:**
- Modify: `tools/webshot/main.go` (a new block after `page.query`; `shoot`)
- Test: `tools/webshot/webshot_test.go`

Contract names this task defines: `func (p *page) press(ctx context.Context, chord string) error`, and `shoot`'s press behaviour. Names of its own: `parseChord(chord string) (key string, modifiers int, err error)`, `(p *page) waitFor(ctx context.Context, selector string, within time.Duration) bool`, `(p *page) pressScreen(ctx context.Context, mode screenMode, settle, wait time.Duration) error`, and the constants `pressWait` (5 s) and `desktopFrame` (the selector `[data-slot="desktop"]`). It uses `page.query`, `cdpClient.call`, `modeOf`, `fakeBrowser` (the test file's helper over a pair of pipes) and `cdpMessage`.

The behaviour, in `shoot`'s order for a screen whose mode has `press`: the usual wait for `appDrawn` and `settle`; `pressScreen`, which waits up to `wait` (in `shoot`, `pressWait`) for the desktop frame and fails with `no desktop frame to press <chord> on`, sleeps `settle` so `DesktopShell`'s effects (the chord's listener) have run, presses the chord once, then waits up to `wait` for `drawn` and fails with `pressing <chord> drew no <drawn>`; then `shoot` sleeps `settle` again before the capture. A screen with no `press` takes none of these steps and settles once, as today. The chord is pressed once and never retried: it toggles, and a second press closes what the first opened.

- [ ] **Step 1: Write the failing tests.** In `tools/webshot/webshot_test.go`, using `fakeBrowser`. Helper `recordingBrowser(t, answer func(method string, params map[string]any) any) (*page, func() []string)`: it builds a `fakeBrowser` whose answer function decodes each call's `Params` into a map, appends `method` and, for `Input.dispatchKeyEvent`, the call's `type` (as `Input.dispatchKeyEvent:keyDown`) to a mutex-guarded list with a timestamp, replies `{"id": m.ID, "result": <answer's return, as JSON>}`, and returns `&page{b: &browser{cdp: c}, session: "s1"}` and a getter of the list; the test's `t.Cleanup` calls the stop function. The `Runtime.evaluate` answer wraps its value as `{"result": {"value": <value>}}`.
  - `TestParseChord`: a table. `Control+k` gives key `k`, modifiers 2; `Meta+k` 4; `Alt+k` 1; `Shift+k` 8; `Control+Shift+k` 10; `Alt+Control+Meta+Shift+x` 15; `k` gives `k`, 0; `Control+5` gives `5`, 2. Each of `` (empty), `Control+`, `Hyper+k`, `Control+Control+k`, `Control+kk` and `Control+é` is an error whose text contains the chord it was given.
  - `TestPress_SendsKeyDownThenKeyUpWithTheChordsModifiers`: `press(ctx, "Control+k")` makes exactly two calls, both `Input.dispatchKeyEvent` on session `s1`, the first of `type` `keyDown` and the second `keyUp`, each with `modifiers` 2, `key` `k`, `code` `KeyK` and `windowsVirtualKeyCode` 75, and neither holding a `text` field (a chord types nothing). A second case, `Control+Shift+k`, sends `modifiers` 10.
  - `TestPress_ABadChordSendsNothing`: `press(ctx, "Hyper+k")` returns an error and the recorded list is empty.
  - `TestPressScreen_WaitsForTheFrameSettlesPressesOnceAndWaitsForTheSlot`: `mode` is the palette's (`modeOf(screen{"palette", "#s/1"})`), `settle` 150 ms, `wait` 2 s. The fake answers `Runtime.evaluate` by its expression: one containing `desktop` answers `false` for the first three asks and `true` after, one containing `palette` answers `true` once the `keyDown` has been seen and `false` before. `pressScreen` returns nil. The recorded list holds a `keyDown` and a `keyUp` once each, no `Input` call before the first desktop ask that answered `true`, and the gap between that ask and the `keyDown` is at least 140 ms (the settle, which a timestamp bounds below only).
  - `TestPressScreen_NoDesktopFrameFailsBeforeAnyKey`: the fake answers `false` to every `Runtime.evaluate`; `wait` is 300 ms. The error text is exactly `no desktop frame to press Control+k on`, and the list holds no `Input.dispatchKeyEvent`.
  - `TestPressScreen_ASlotThatNeverDrawsFailsAfterOnePress`: the fake answers `true` for the desktop frame and `false` for the palette; `wait` 300 ms. The error text is exactly `pressing Control+k drew no [data-slot="palette"]`, and the list holds exactly one `keyDown` and one `keyUp` (pressed once, never retried).
  - `TestPressScreen_AScreenWithNoChordDoesNothing`: `pressScreen` with `screenMode{}` returns nil and the list is empty.

  Fails without: `TestParseChord`: a modifier table with one wrong bit (Meta 8, Shift 4) turns a row red; accepting a multi-character key turns `Control+kk` red; accepting a repeated modifier turns `Control+Control+k` red; a parser that splits on the last `+` only turns `Hyper+k` red. `TestPress_SendsKeyDownThenKeyUpWithTheChordsModifiers`: no `keyUp`, the two events in the other order, a `modifiers` of 0, an uppercase `key`, a missing `code` or `windowsVirtualKeyCode`, or a `text` of `k` (which would type a letter) turns it red. `TestPress_ABadChordSendsNothing`: a `press` that sends the key events before it parses the chord turns it red. `TestPressScreen_WaitsForTheFrameSettlesPressesOnceAndWaitsForTheSlot`: pressing at once (no frame wait) puts an `Input` call before the first `true` ask, no settle after the frame shrinks the gap under 140 ms, and a second press adds a second `keyDown`. `TestPressScreen_NoDesktopFrameFailsBeforeAnyKey`: a `pressScreen` that presses without the frame turns the `Input` assertion red, and a different message turns the text red. `TestPressScreen_ASlotThatNeverDrawsFailsAfterOnePress`: a retry loop around the press turns the count red, and a wait that returns nil on a timeout turns the error red. `TestPressScreen_AScreenWithNoChordDoesNothing`: a `pressScreen` that waits for the frame whatever the mode turns the empty list red (the fake would be asked for `desktop`).

- [ ] **Step 2: Run them to see them fail.** `CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestParseChord|TestPress_|TestPressScreen_'` does not compile: `undefined: parseChord`, `p.press undefined`, `p.pressScreen undefined`.

- [ ] **Step 3: Implement.** In `tools/webshot/main.go`, after `page.query`:
  - `parseChord(chord string) (string, int, error)`: split `chord` on `+`; every part but the last must be one of `Alt` (1), `Control` (2), `Meta` (4) or `Shift` (8) and none repeats; the last must be one ASCII letter or digit. The modifiers are the sum of the bits. Any other chord (empty, a missing key, an unknown or repeated modifier, a longer or non-ASCII key) is an error wrapping `chord` in its text, as `fmt.Errorf("chord %q: …", chord)`.
  - `(p *page) press(ctx, chord)`: parse first (an error sends nothing), then call `Input.dispatchKeyEvent` on `p.session` with `type` `keyDown` and again with `keyUp`, each with `modifiers`, `key` (as written in the chord), `code` (`Key` plus the upper-cased letter, or `Digit` plus the digit) and `windowsVirtualKeyCode` (the ASCII code of the upper-cased letter or the digit), and no `text`. Return the first error from either call, wrapped with the chord. A comment says the event is a key down and up only, since a chord with Control or Meta types no character, and that the page's own `keydown` listener is what the screen waits on.
  - `(p *page) waitFor(ctx, selector, within) bool`: poll every 100 ms until `within` has passed, asking the page `!!document.querySelector(<selector as a JSON string>)` through `p.query` (a query that errors counts as no match, as `shoot`'s `appDrawn` loop treats it), and report whether it matched. Quote the selector with `json.Marshal`, so the `"` in `[data-slot="palette"]` cannot end the expression.
  - `(p *page) pressScreen(ctx, mode, settle, wait) error`: nil at once when `mode.press` is empty; otherwise the sequence above, with `waitFor(ctx, desktopFrame, wait)`, `time.Sleep(settle)`, `p.press(ctx, mode.press)`, `waitFor(ctx, mode.drawn, wait)` and the two error texts exactly as the tests pin them.
  - Constants `pressWait = 5 * time.Second` and `desktopFrame = `[data-slot="desktop"]`` with a comment: the lazy `DesktopShell` draws it, and `appDrawn` is already true on the phone shell's splash and on the lazy shell's fallback, where the chord has no listener.
  - In `shoot`, after the existing `time.Sleep(settle)` and before `p.capture`, add: take `mode := modeOf(sc)`; when `mode.press != ""`, call `p.pressScreen(ctx, mode, settle, pressWait)`, return `nil, err` on an error, and then `time.Sleep(settle)`. Update `shoot`'s comment to say that a screen with a chord is pressed after the frame has drawn and settled, and settles again before the capture. Change nothing else in `shoot`.

- [ ] **Step 4: Run them to see them pass.** `CGO_ENABLED=0 go test ./tools/webshot/` prints `ok`: the new tests, and every existing one (the real-browser ones skip without `LOOM_WEBSHOT_BROWSER`). `CGO_ENABLED=0 go vet ./tools/webshot/` prints nothing.

- [ ] **Step 5: Commit.** `git commit -m "feat(webshot): press a chord once the desktop frame has drawn, and wait for the screen's slot" -- tools/webshot/main.go tools/webshot/webshot_test.go`

### Task 4.3: The real-browser checks and the story's gates

**Files:**
- Test: `tools/webshot/webshot_test.go` (`TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser`, and a new test beside it)

Names of its own: the test helper `realBrowserArgs(t *testing.T) []string`. It uses `run`, `devices`, `png`, `filepath`.

- [ ] **Step 1: Write the tests.** In `tools/webshot/webshot_test.go`:
  - Add the helper `realBrowserArgs(t)`: skips the test with `set LOOM_WEBSHOT_BROWSER to run it` when the variable is empty; otherwise returns `[]string{}` for `1` (the browser on `PATH`) and, for a binary name, checks `exec.LookPath` with `require.NoError` and returns `[]string{"-browser", which}`. The two existing real-browser tests keep their own inline copy (leave them as they are); only the new test calls it.
  - In `TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser`, after the existing `add-to-home.png` assertion inside the subtest, add: open `palette.png` in `out`, decode it, and assert its size equals `[2]int{d.width, d.height}` (the same check the test makes of `fleet.png`, so the palette's screenshot is a bare viewport at scale 1); close the file. The existing `require.NoError(t, run(args), …)` already fails the run when the press finds no frame or no palette (`shoot` returns the error), or when the open palette logs a console error or CSP violation, so this assertion adds only that the file exists and is the right size.
  - New `TestRun_ARealPhoneRunWritesNoPaletteScreen`: `-app next -static <the dist tree, as the desktop test passes it>` `-device iphone15 -screens fleet,palette -out <t.TempDir()> -settle 800ms` plus `realBrowserArgs(t)`. `run` returns no error; `fleet.png` exists; `palette.png` does not (`os.IsNotExist` on its `os.Stat`).

  Fails without: `TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser`: a `palette` screen not in `screens`, a mode with no `press`, or a chord the page never hears (a listener not bound, or an event Chromium does not deliver as a `keydown`) leaves no `palette.png` or fails `run`. `TestRun_ARealPhoneRunWritesNoPaletteScreen`: a `forDevice` that keeps `desktopOnly` screens on a phone writes `palette.png` (or fails the run, since a phone shell has no desktop frame) and turns it red.

- [ ] **Step 2: Run the unit suite.** `CGO_ENABLED=0 go test ./tools/webshot/ ./tools/claudemd/` prints `ok` for both (the real-browser tests skip without the variable).

- [ ] **Step 3: Run the real browser.** `bash -c 'LOOM_WEBSHOT_BROWSER=1 CGO_ENABLED=0 go test ./tools/webshot/ -run "TestRun_RendersTheNewAppAtBothDesktopSizesInARealBrowser|TestRun_ARealPhoneRunWritesNoPaletteScreen" -v'` prints `--- PASS` for the desktop test's two subtests and the phone test. This needs a Chromium or Chrome on `PATH` and `internal/web/dist` as Story 3 rebuilt it. If the press draws no palette (`pressing Control+k drew no [data-slot="palette"]`), the dispatch is the suspect: the page's `keydown` listener (Story 3's `usePaletteKey`) hears `key` `k` with `ctrlKey`, which `modifiers` 2 and `key` `k` set, so read the event's fields in the browser before changing the listener.

- [ ] **Step 4: Run the story's Verify and the repo's gates.** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ ./tools/claudemd/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -screens palette -out "$d/w" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -screens palette -out "$d/n" && test -s "$d/w/palette.png" && test -s "$d/n/palette.png"'` prints `palette` and a path under `$d` for each render and exits 0; it needs the same browser and bundle as Step 3, and its `$d` is outside the worktree. Then the gates: `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi'`, `CGO_ENABLED=0 go vet ./...`, and `CGO_ENABLED=0 go test -timeout 30m ./...` each exit 0.

- [ ] **Step 5: Commit.** `git commit -m "test(webshot): check the palette screen in a real browser at both desktop sizes and on a phone" -- tools/webshot/webshot_test.go`

## Story 5: The rules and the procedures for the palette

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `USAGE.md`, `internal/web/README.md`
- **Interfaces:** none
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd'`
- **Review:** light (docs)
- **Form:** brief (docs)

This story writes down what Stories 1 to 4 built, in the files a later session reads before it changes the palette: `internal/web/ui/CLAUDE.md` (the rules), `docs/claude/web-layer.md` (the procedures and the checklist) with its row in `docs/claude/INDEX.md`, `USAGE.md` (the owner's view) and `internal/web/README.md` (the desktop layout). It changes no code, so every task leaves the tree green as long as the CLAUDE.md lint passes, which each task runs. Four things are written: the offline rule names the two view toggles (spec, Part 2 settlements, decisions 3 and 13); a rule that a command sets `palette` only when it takes no args; a rule that the palette's chord is the app's only document key listener, bound in `DesktopShell`; and the procedures for adding a palette command and a palette place. `docs/claude/web-layer.md` also corrects the two lines that say the desktop devices skip only `add-to-home` (a phone now skips the desktop-only `palette` screen, and so does the classic app, which is Story 4's behaviour), and makes the desktop checklist's item 8 the palette's, since the keys left the epic. `internal/web/CLAUDE.md`, root `CLAUDE.md` and `tools/CLAUDE.md` (Story 4's) do not change. No code here holds a landmine marker, so the developer runs at the default effort. This story is docs, so it has no test of its own: the named tests it cites belong to Stories 1 to 3, and its gate is the CLAUDE.md lint (paths, Go test names, line and byte budgets). Every command below runs from the repo root.

### Task 5.1: `internal/web/ui/CLAUDE.md`: the offline rule, the palette mark and the chord

**Files:**
- Modify: `internal/web/ui/CLAUDE.md:9` (the offline rule), and two new rules (after line 9 and after the file's last line)

Behaviour: the file's rules say, after this task, that the offline set is the switch, pairing and the two view toggles, that `palette` marks only no-args commands, and that the chord's listener is the app's only one. Each new rule has the repo's shape (what to do; why, in one clause; what breaks and how silently; then **Enforced** by a named test or **Convention** with the silent wrong result), in the present tense, with paths from the repo root.

- [ ] **Step 1: Check what is there.** `grep -c 'view.toggleSidebar' internal/web/ui/CLAUDE.md` prints `0`, and `LC_ALL=C awk '{ if (length($0) > 1200) print NR }' internal/web/ui/CLAUDE.md` prints nothing. `go run ./tools/claudemd -v` shows the headroom of `internal/web/ui/CLAUDE.md` (about 7,200 bytes free; the three edits add under 1,900).

- [ ] **Step 2: Amend the offline rule.** In the rule on line 9 (it begins `- **A command's \`when\` reads only the session's status and group`), make two replacements, each inside that one line:
  - Replace `Only \`app.backToClassic\` and \`device.pair\` are \`offline\`, because the switch must work with the daemon down and pairing runs before any snapshot exists.` with:

    ```
    Only `app.backToClassic`, `device.pair` and the palette's two view toggles, `view.toggleSidebar` and `view.toggleInspector`, are `offline`, because the switch must work with the daemon down, pairing runs before any snapshot exists and a toggle changes only this browser's layout.
    ```
  - Replace `**Enforced** for the stale gate by \`internal/web/ui/src/app/commands.test.ts\`; what` with:

    ```
    **Enforced** for the stale gate by `internal/web/ui/src/app/commands.test.ts` and for the toggles by the case `a toggle runs while the snapshot is stale` in `internal/web/ui/src/commands/view.test.ts`; what
    ```
  The rest of the line stays.

- [ ] **Step 3: Add the palette-mark rule.** Insert this rule as a new line directly after the amended offline rule (line 9), before the `Write no runtime \`<style>\`` rule:

    ```
    - **Set `palette` on a command only when it takes no args, and never on one whose args a view picks (an ask's answer, a job's controls, a send).** The palette lists exactly the registered commands that set the mark and runs one through `runCommand` with the route's context and no args, so a command that reads its args would run with `undefined` and fail or act on nothing. **Enforced** for the set by the cases `the palette lists exactly these ten commands` and `no command that takes args sets the mark` in `internal/web/ui/src/commands/palette.test.ts`; marking a new no-args command is **Convention** — a command the palette never lists.
    ```

- [ ] **Step 4: Add the chord rule.** Append this rule as the file's last line, after the rule on `DesktopShell` being a `React.lazy` module:

    ```
    - **Bind a document `keydown` listener only through `usePaletteKey` (`internal/web/ui/src/desktop/paletteState.ts`), called by `DesktopShell`: ⌘K and Ctrl+K are the app's only key listener, and they toggle the palette from anywhere, a text field included, and do nothing while `confirmOpen()` (`internal/web/ui/src/app/ConfirmSheet.tsx`) is true.** A listener bound elsewhere loads in the phone's entry and runs where no palette is drawn, a second one steals keys from a text field, and a chord pressed over a confirmation would stack a palette on it. **Enforced** for the chord by the cases `toggles the palette on the chord from a text field` and `does nothing while a confirmation is open, and still prevents the browser's default`, and for the one listener by `adds one keydown listener on the document and removes the same one on unmount`, all in `internal/web/ui/src/desktop/paletteState.test.ts`; another listener elsewhere is **Convention** — a key that acts on the phone or inside a field.
    ```

- [ ] **Step 5: Run the lint.** `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd` print `ok` and no finding. Then `grep -cF 'Only `app.backToClassic` and `device.pair` are' internal/web/ui/CLAUDE.md` prints `0` (the old sentence is gone), `grep -c 'view.toggleInspector' internal/web/ui/CLAUDE.md` prints `1` (the sentence names it once; the other two rules do not), and `LC_ALL=C awk '{ if (length($0) > 1200) print NR }' internal/web/ui/CLAUDE.md` prints nothing (no line over 1,200 bytes).

  Fails without: there is no test of this task's own text, so what would catch each part is the lint and the greps: a rule that cites a path that does not exist (`internal/web/ui/src/commands/palette.test.ts` before Story 1, say) fails `TestRepo_ClaudeMDStructure` through `go test ./tools/claudemd/`; a line over 1,200 bytes fails the same test and the `awk` check; a missed replacement leaves the old sentence and makes the first `grep -cF` print `1`.

- [ ] **Step 6: Commit.** `git commit -m "docs(web): name the view toggles as offline, and add the palette mark and chord rules" -- internal/web/ui/CLAUDE.md`

### Task 5.2: `docs/claude/web-layer.md` and `docs/claude/INDEX.md`: adding a palette command and a palette place

**Files:**
- Modify: `docs/claude/web-layer.md:45-47` ("Adding a command": its `offline` sentence), and two new sections between "Adding a command" and "Adding a read" (before the line `## Adding a read`)
- Modify: `docs/claude/INDEX.md:28` (the `web-layer.md` row's list of topics)

Behaviour: "Adding a command" names the view toggles beside the switch and pairing as the `offline` commands, and two new sections tell a later session how a command or a place reaches the palette. The new sections follow the file's style (a `## ` heading, one dense paragraph each, paths from the repo root, no count of what the code holds). The guide index's row for `web-layer.md` lists the guide's topics, so it names the two new procedures too.

- [ ] **Step 1: Check what is there.** `grep -n '^## Adding a' docs/claude/web-layer.md` lists `Adding an API route`, `Adding a stream event`, `Adding a push kind`, `Adding a panel`, `Adding a command` and `Adding a read`, `grep -c 'palette' docs/claude/web-layer.md` prints `1` (the checklist's item 8, which Task 5.3 rewrites), and `grep -c 'palette' docs/claude/INDEX.md` prints `0`.

- [ ] **Step 2: Amend the `offline` sentence.** In the "Adding a command" paragraph, replace `\`offline\` only for an action that must work without the daemon (the switch, and pairing, which runs before any snapshot exists);` with:

    ```
    `offline` only for an action that must work without the daemon (the switch; pairing, which runs before any snapshot exists; and the two palette view toggles, which change only this browser's layout);
    ```
  Nothing else in that paragraph changes; its last sentence (`An \`offline\` command also runs while the snapshot is stale or absent, which every other command does not.`) stays.

- [ ] **Step 3: Add the two sections.** Insert, directly above the line `## Adding a read`, a section `## Adding a palette command` and a section `## Adding a palette place`, with this content (each a single paragraph, written out in full):
  - `## Adding a palette command`: A command appears in the desktop's palette when its `defineCommand` sets `palette: true`, and only when it takes no args, since the palette runs it through `runCommand` with the route's context (`useCommandContext()`) and no args after it closes (`internal/web/ui/CLAUDE.md` holds the rule). A command whose args a view picks (an ask's answer, a job's controls, a send) never sets it. The palette draws the command's `title` (a function title is called with the context), lists it on an empty query only when its `when` holds for the route's command context, and draws it dimmed and inert when `commandEnabled` refuses it (its `when` fails, or the snapshot is stale and it is not `offline`), so `when` and `offline` are written for the button and the palette reads them as they are; no key is shown beside it, since the keys are not in the app. Add the command's id to the set `internal/web/ui/src/commands/palette.test.ts` pins (the case `the palette lists exactly these ten commands`, which fails on an id added or dropped without it; rename the case with the new count). A command that changes only this browser's layout lives in `internal/web/ui/src/commands/view.ts` and is `offline`, and a command the phone never needs is imported by the module of the desktop chunk that needs it (`view.ts` by `internal/web/ui/src/desktop/DesktopShell.tsx`), never by `internal/web/ui/src/commands/index.ts`, since the entry's imports are the phone's first load and `TestDist_FirstLoadIsWithinBudget` reads them. Test it as the palette's other commands are tested: the command's own cases beside its module, and the set above.
  - `## Adding a palette place`: A place is a row of the palette's Places group, built in `paletteGroups` (`internal/web/ui/src/desktop/palette.ts`, pure, no React) in this order: the current session's tab panels, then Fleet, New session, Flows (only while `hasShownFlows` in `internal/web/ui/src/fleet/groups.ts` holds, the rule of the top bar's Flows link) and Settings. A panel registered with a `tab` is a tab place with no edit: `Palette` hands `paletteGroups` the session's `sessionTabs`, and the place's title is the tab's label, its hint the session's title, its link `hrefFor(<panel id>, { id })`. A place that is not a tab (a new top-level panel with a URL) is one more entry in the plain places' list of `paletteGroups`: a key `place:<panel id>`, a title, an empty hint and `hrefFor("<panel id>")`, written with a case in `internal/web/ui/src/desktop/palette.test.ts` beside `with an empty query lists the session's tabs, then Fleet, New session, Flows and Settings`, which names the order and fails on an entry added without its case. A place that only some fleets have (Flows is the example) states its condition in `paletteGroups` and in a test that leaves it out.

- [ ] **Step 4: Amend the index row.** `docs/claude/INDEX.md:28` is the `web-layer.md` row. In its list of topics, replace `` `offline`) or a read (through `reads.ts`) in the new app `` (the end of the command item and the read item; it occurs once) with:

    ```
    `offline`), a palette command (`palette: true`, only when it takes no args, and its id in the set `commands/palette.test.ts` pins), a palette place (a row of `paletteGroups`; a panel with a `tab` is one with no edit) or a read (through `reads.ts`) in the new app
    ```
  The list then reads `a panel (...), a command (...), a palette command (...), a palette place (...) or a read (...)`. Nothing else in the row changes.

- [ ] **Step 5: Run the lint.** `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd` print `ok` and no finding (`docs/claude/` guides and the index are linted for their links and paths, and every guide must still be listed in the index). Then `grep -c '^## Adding a palette' docs/claude/web-layer.md` prints `2`, `grep -cF 'the switch, and pairing, which runs before any snapshot exists' docs/claude/web-layer.md` prints `0`, `grep -cF 'a palette command (' docs/claude/INDEX.md` prints `1`, `grep -cF 'a palette place (' docs/claude/INDEX.md` prints `1`, and `grep -cF '\`offline\`) or a read' docs/claude/INDEX.md` prints `0` (the old join is gone).

  Fails without: no test reads this prose; what catches a slip is the lint (a path that does not exist, a link that does not resolve, a guide missing from the index) and the greps (the two headings present, the old `offline` wording gone, the index row naming both procedures and no longer joining the command item to the read item with `or`).

- [ ] **Step 6: Commit.** `git commit -m "docs(web): add the palette command and palette place procedures, and name them in the guide index" -- docs/claude/web-layer.md docs/claude/INDEX.md`

### Task 5.3: `docs/claude/web-layer.md`: the webshot device lines and the checklist's item 8

**Files:**
- Modify: `docs/claude/web-layer.md:55` ("The dev loop": the desktop devices' sentence), `docs/claude/web-layer.md:75` ("Changing the classic app": the `-device desktop` sentence), `docs/claude/web-layer.md:124` (checklist item 8)

Behaviour: the two lines that say the desktop devices skip only `add-to-home` say what Story 4 built (a desktop renders `palette`, a phone skips it, and the classic app skips it), and the checklist's item 8 is the palette's alone, since the keys left the epic (spec, Part 2 settlements, decisions 1 and 13). Line numbers are today's (before Task 5.2 adds its two sections); find each by its quoted text, since Task 5.2 moves the lines after it down.

- [ ] **Step 1: Check what is there.** `grep -c 'phone-only \`add-to-home\`' docs/claude/web-layer.md` prints `1`, `grep -c 'and \`add-to-home\` is skipped on them' docs/claude/web-layer.md` prints `1`, and `grep -c 'the keys and the help' docs/claude/web-layer.md` prints `1`.

- [ ] **Step 2: Correct "The dev loop".** In the paragraph that begins `` `go run ./tools/webshot -serve 127.0.0.1:5670` serves the canned loom's API ``, replace `and not the phone-only \`add-to-home\`; \`.github/workflows/web.yml\` renders the phone and both.` with:

    ```
    and not the phone-only `add-to-home`, and they alone render `palette`, the desktop-only screen that presses Ctrl+K once the app is drawn (a phone skips it, and so does `-app classic`, the default); `.github/workflows/web.yml` renders the phone and both.
    ```

- [ ] **Step 3: Correct "Changing the classic app".** In the bullet that begins `- To see a screen without a phone, run`, replace `home indicator or status bar, and \`add-to-home\` is skipped on them.` with:

    ```
    home indicator or status bar; `add-to-home` is skipped on them (`skipped: phone only`), and `palette`, which presses Ctrl+K once the app is drawn, is shot only on them and only under `-app next` (a phone skips it as `skipped: desktop only`, the classic app as `skipped: next app only`).
    ```

- [ ] **Step 4: Make item 8 the palette's.** In "The desktop checklist after each part", replace the line `8. From part 2: the keys and the help, and the palette's three kinds.` with:

    ```
    8. From part 2: the palette's three kinds, and ⌘K and Ctrl+K from a text field.
    ```

- [ ] **Step 5: Run the checks.** `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd` print `ok` and no finding. Then, each by `grep -cF`, `the keys and the help` prints `0`, `skipped: next app only` prints `1`, `and they alone render` prints `1`, and `8. From part 2: the palette's three kinds, and ⌘K and Ctrl+K from a text field.` prints `1`.

  Fails without: no test reads this prose; the four greps are the check. The first fails (prints `1`) while the checklist keeps the keys, the other three fail (print `0`) until the corrected webshot lines are in.

- [ ] **Step 6: Commit.** `git commit -m "docs(web): correct the webshot device lines for the palette screen and make the checklist's item 8 the palette's" -- docs/claude/web-layer.md`

### Task 5.4: `USAGE.md` and `internal/web/README.md`: the palette

**Files:**
- Modify: `USAGE.md:820-845` (the desktop layout paragraph)
- Modify: `internal/web/README.md:103` (webshot's screens) and `internal/web/README.md:107` (the desktop layout)

Behaviour: the owner's reference tells how to open the palette and what it finds and does, and the package README states what it is made of. The palette is for the desktop layout only, so the phone's text is untouched.

- [ ] **Step 1: Check what is there.** `grep -c 'palette' USAGE.md` prints `1` (the unrelated colour-palette line) and `grep -c 'palette' internal/web/README.md` prints `0`. Read `USAGE.md:820-845`: the desktop paragraph ends `confirmation is a centred dialog and toasts sit at the bottom` / `right. The layout follows the window as it resizes, and under 1024px it is` / `the phone's. To open it on the machine that runs the daemon, use` / `` `loom web open` (below). ``

- [ ] **Step 2: `USAGE.md`.** In the desktop-layout paragraph, insert after `right.` (the end of `A confirmation is a centred dialog and toasts sit at the bottom right.`) and before `The layout follows the window as it resizes`, text of this meaning, wrapped at the paragraph's width (about 75 columns, each line indented two spaces as its neighbours are): **A search box** in the top bar, after `loom@host`, **opens the palette**, as do ⌘K on a Mac and Ctrl+K elsewhere, from anywhere, a text field included. One box finds any session (by its title, its branch or its workspace's name, paused ones too), runs a command on the session you are on (pause, resume, push, merge, kill) or on the app (turn notifications on, unpair, back to classic, show or hide the sidebar, show or hide the inspector), and opens a place (the session's tabs, the fleet, new session, the host's flows, settings). With nothing typed it lists the sessions that need you first. Enter opens the highlighted one, Esc closes the box, and a command that cannot run now (no session, or the daemon unreachable) is dimmed and does nothing. Kill and the other commands that ask first still ask. Nothing about it is on the phone, which keeps its tabs and footer.

- [ ] **Step 3: `internal/web/README.md`, the desktop layout.** In the paragraph under `## The desktop layout`, insert after `an inspector that draws another of the session's tabs.` and before `Pairing, the splash`: 

    ```
    The palette opens over the frame from ⌘K or Ctrl+K (one document `keydown` listener, `usePaletteKey` in `internal/web/ui/src/desktop/paletteState.ts`, bound by `DesktopShell`, from any focus but a confirmation) and from the top bar's box (`PaletteBox`, `internal/web/ui/src/desktop/TopBar.tsx`): a Base UI Dialog around an inline Autocomplete (`internal/web/ui/src/desktop/Palette.tsx`) over the groups `paletteGroups` builds (`internal/web/ui/src/desktop/palette.ts`, pure): every session, the registered commands that set `palette`, and places, with Enter opening a session or a place or running the command through `runCommand`. Its code is in the desktop chunk, so the phone's first load is unchanged.
    ```

- [ ] **Step 4: `internal/web/README.md`, webshot's screens.** In the paragraph on line 103, replace `shoots \`pair\` unpaired and \`add-to-home\` as a browser tab.` with:

    ```
    shoots `pair` unpaired and `add-to-home` as a browser tab, and, under `-app next` on a desktop device only, `palette` after pressing Ctrl+K.
    ```

- [ ] **Step 5: Run the checks.** `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd` print `ok` and no finding (the README's paths resolve once Stories 1 to 3 are in). Then `grep -c 'opens the palette' USAGE.md` prints `1`, `grep -c 'The palette opens over the frame' internal/web/README.md` prints `1`, `grep -c 'palette. after pressing Ctrl+K' internal/web/README.md` prints `1` (the `.` matches the closing backtick), and `git diff --stat HEAD -- internal/web/CLAUDE.md CLAUDE.md` prints nothing (neither file changed).

  Fails without: no test reads the prose; the greps and the lint (every path in the README must resolve) are the check, and the empty `git diff --stat` pins that the two files this story leaves alone stayed so.

- [ ] **Step 6: Commit.** `git commit -m "docs(web): add the palette to the usage guide and the web README" -- USAGE.md internal/web/README.md`

### Task 5.5: Run the story's gates

**Files:**
- none (the gates read the tree)

- [ ] **Step 1: Run the lint and the structure test.** `CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd -v` print `ok`, no finding, and `internal/web/ui/CLAUDE.md` under its 100 lines and 20,000 bytes.

- [ ] **Step 2: Run the story gates.** `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi'`, then `CGO_ENABLED=0 go vet ./...`, then `CGO_ENABLED=0 go test -timeout 30m ./...` pass (this story changes no Go file; the full run confirms the docs tests, `TestRepo_ClaudeMDStructure` among them, pass with the rest).
