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

**The palette's items (Story 2)**, in `desktop/palette.ts`, which imports no React:

- `function fuzzyScore(query: string, text: string): number | null` (Story 2): the spec's Part 2 settlements, decision 7, its five examples each a test.
- `function compareCodePoints(a: string, b: string): number` (Story 2): negative when `a` sorts first by code point (`codePointAt`, not UTF-16 units), 0 when equal, positive otherwise; never `localeCompare`. `compareCodePoints("｡", "\u{1F600}")` is negative, where `<` on the strings says the opposite.
- `function hasShownFlows(snapshot: Snapshot): boolean` (Story 2), in `fleet/groups.ts`: true when a session of a shown workspace (`shownWorkspaces`) has a flow; `FleetLinks` (`panels/fleet/FleetHead.tsx`) calls it instead of its own selector.
- `interface PaletteTab { id: PanelId, label: string }` (Story 2): a tab panel of the current session.
- `interface PaletteSession { kind: "session", key: string, title: string, hint: string, href: string }` (Story 2): `key` is `session:<id>`, `hint` the workspace's name (`label || name`) and the branch joined by ` · ` with an empty one left out, `href` the session's `sessionHref` link.
- `interface PaletteCommand { kind: "command", key: string, title: string, command: Command<unknown>, enabled: boolean }` (Story 2): `key` is `command:<id>`, `title` the command's title (a function title called with the context), `enabled` what `commandEnabled` answers for the command and the context.
- `interface PalettePlace { kind: "place", key: string, title: string, hint: string, href: string }` (Story 2): `key` is `place:<panel id>`; a tab's `title` is its label, its `hint` the session's title, its `href` the tab panel's `hrefFor` link with the session's id; Fleet, New session, Flows and Settings have an empty `hint` and `hrefFor("fleet")`, `hrefFor("new")`, `hrefFor("flows")` and `hrefFor("settings")`.
- `type PaletteItem = PaletteSession | PaletteCommand | PalettePlace` (Story 2).
- `interface PaletteGroup { label: "Sessions" | "Commands" | "Places", items: PaletteItem[] }` (Story 2).
- `interface PaletteInput { query: string, snapshot: Snapshot | null, commands: Command<unknown>[], ctx: CommandContext, tabs: PaletteTab[] }` (Story 2): `commands` every registered command (`allCommands()`), narrowed here to those with `palette`; `tabs` the current session's tab panels in tab order (`sessionTabs(ctx.session)` as `{ id, label }`, empty with no session).
- `function paletteGroups(input: PaletteInput): PaletteGroup[]` (Story 2): the items and their order as the Part 2 settlements' decisions 8 and 9 say, a group with no items left out; a null snapshot gives no sessions and no Flows place.

**The palette (Story 3)**, modules of the desktop chunk:

- `interface PaletteState { open: boolean, setOpen: (open: boolean) => void, toggle: () => void }` and `const usePalette: UseBoundStore<StoreApi<PaletteState>>` (Story 3), in `desktop/paletteState.ts`: in memory, closed at load.
- `function isPaletteChord(e: KeyboardEvent): boolean` (Story 3), in `desktop/paletteState.ts`: `metaKey` or `ctrlKey`, not `altKey`, not `shiftKey`, not `isComposing`, and `key` `k` or `K`.
- `function usePaletteKey(): void` (Story 3), in `desktop/paletteState.ts`: one document `keydown` listener, added in an effect and removed on unmount; on the chord it calls `preventDefault()`, then does nothing while `confirmOpen()` (Story 1), else calls `usePalette.getState().toggle()`, whatever has focus. `DesktopShell` calls it.
- `function paletteKeyLabel(platform: string): string` (Story 3), in `desktop/paletteState.ts`: `⌘K` when `platform` matches `/mac|iphone|ipad/i`, else `Ctrl K`.
- `function PaletteBox(): ReactElement` (Story 3), in `desktop/TopBar.tsx`: a `button` with `data-slot="palette-box"`, the text `Search sessions, commands, places` and `paletteKeyLabel(navigator.platform)`; a click opens the palette through `usePalette`'s `setOpen`. `TopBar` draws it after `HostLabel` and before `WorkspaceFilter`.
- `function Palette(): ReactElement` (Story 3), the named export of `desktop/Palette.tsx`: a `Dialog` (`components/ui/dialog.tsx`) open while `usePalette().open`, its popup carrying `data-slot="palette"`, around a Base UI `Autocomplete.Root` that is `inline` and `open` with `filter={null}` and `autoHighlight`, its `items` the `paletteGroups` (Story 2) for the query; the input carries `data-slot="palette-input"` and each item `data-slot="palette-item"` and `data-kind` (its kind); a command that is not `enabled` is a `disabled` item; an item's `onClick` (a click, or Enter while highlighted) acts as the Part 2 settlements' decision 10 says; each opening starts with an empty query. `DesktopShell` draws it.

**webshot (Story 4)**, in `tools/webshot/main.go`:

- `type screenMode struct{ unpaired, browserTab, desktopOnly bool; press, drawn string }` (Story 4): `press` a chord pressed once the app is drawn, in the form `Control+k` (prefixes `Alt+`, `Control+`, `Meta+`, `Shift+`), `drawn` a selector that must match after the press. `screens` gains `{"palette", "#s/1"}` and `screenModes` gains `"palette": {desktopOnly: true, press: "Control+k", drawn: "[data-slot=\"palette\"]"}` (Story 4).
- `forDevice` (Story 4) drops `desktopOnly` screens on a phone and `browserTab` screens on a desktop; `TestForDevice_DropsOnlyAddToHomeOnADesktop` becomes `TestForDevice_DropsAddToHomeOnADesktopAndThePaletteOnAPhone`, and `tools/CLAUDE.md`'s webshot device rule names the new test.
- `func (p *page) press(ctx context.Context, chord string) error` (Story 4): `Input.dispatchKeyEvent` `keyDown` then `keyUp` with the chord's modifiers (Alt 1, Control 2, Meta 4, Shift 8); `shoot` presses `press`, then waits up to 5 s for `drawn` to match, failing the screen with `pressing <chord> drew no <drawn>`, then settles before the capture.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): `desktop/DesktopShell.tsx` as Story 1 leaves it (Story 3); `app/commands.ts` as Story 1 leaves it (Stories 2 and 3).

**For every story:** each test's `Fails without:` line names a change to the code that the test, as written, really catches when run: part 1's three plan problems were claims no test could catch (a probe that never read the value it claimed to, a `grep -c` that counted lines rather than matches). A test that reads the DOM reads the attribute or text it claims to.

## Stories

- **Story 1** — The registry's palette mark, the two view toggles, and the confirmation's open state.

  Add `palette?: boolean` to `Command` and `allCommands()` to the registry (spec, Part 2 settlements, decision 4), and set the mark on the eight no-args commands registered today. Add `commands/view.ts` with `view.toggleSidebar` and `view.toggleInspector`, `offline`, flipping `useDesktop`'s visibility, registered by an import in `desktop/DesktopShell.tsx` and never from `commands/index.ts` (decision 5). Export `confirmOpen()` from `app/ConfirmSheet.tsx` for Story 3's listener. Tests pin `allCommands`' order, the palette set exactly (`commands/palette.test.ts`), each toggle flipping its value and running while the snapshot is stale, the shell's module registering the toggles, and `confirmOpen` across an ask, its answer and a replacing ask.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/app/commands.test.ts`, `internal/web/ui/src/commands/session.ts`, `internal/web/ui/src/commands/push.ts`, `internal/web/ui/src/commands/device.ts`, `internal/web/ui/src/commands/view.ts`, `internal/web/ui/src/commands/view.test.ts`, `internal/web/ui/src/commands/palette.test.ts`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `Command.palette`, `allCommands`, the palette set, `view.toggleSidebar`, `view.toggleInspector`, `confirmOpen`; uses `defineCommand`, `commandById`, `runCommand`, `useDesktop`, `askConfirm`, `testSeam`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (a field, a list accessor and two toggles over an existing store, each pinned by a named test)
  - **Form:** brief (plumbing a mark and an accessor, and two small commands)
  - **Reading:** spec §16, §18, Part 2 settlements (decisions 3–5); `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/commands/session.ts:55-183`, `internal/web/ui/src/commands/push.ts:95-116`, `internal/web/ui/src/commands/device.ts:70-110`, `internal/web/ui/src/app/ConfirmSheet.tsx:55-130`, `internal/web/ui/src/app/desktopState.ts`, `internal/web/ui/src/commands/testkit.ts`, `internal/web/ui/src/commands/index.test.ts`, `internal/web/ui/src/desktop/DesktopShell.tsx`

- **Story 2** — The palette's items, score and order, as a pure module.

  Add `desktop/palette.ts` (spec §18–§19, Part 2 settlements, decisions 7–9): `fuzzyScore`, `compareCodePoints`, and `paletteGroups`, which builds the Sessions, Commands and Places groups from the snapshot, the registry and the command context. A non-empty query keeps what matches, ordered by score, then by title, then by key, by code point; an empty query lists the sessions that need you (the longest wait first), the palette commands whose `when` holds, and every place in list order. Move `FleetLinks`' flows rule into `hasShownFlows` in `fleet/groups.ts`, so the Flows place and the top bar's link share it. Tests pin each of decision 7's examples, the best alignment over every start, a null for no match, the code-point tie order, the three groups' order and their absence when empty, sessions matched on branch and workspace name, a command not `enabled` while stale, the Flows place following `hasShownFlows`, and the current session's tabs as places.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/palette.ts`, `internal/web/ui/src/desktop/palette.test.ts`, `internal/web/ui/src/fleet/groups.ts`, `internal/web/ui/src/fleet/groups.test.ts`, `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `fuzzyScore`, `compareCodePoints`, `hasShownFlows`, `PaletteTab`, `PaletteSession`, `PaletteCommand`, `PalettePlace`, `PaletteItem`, `PaletteGroup`, `PaletteInput`, `paletteGroups`; uses `Command.palette`, `allCommands` (Story 1), `commandEnabled`, `CommandContext`, `allSessions`, `groupSessions`, `shownWorkspaces`, `sessionHref`, `hrefFor`, `sessionTabs`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (a pure function whose every rule is a named test)
  - **Reading:** spec §18–§19, Part 2 settlements (decisions 7–9); `internal/web/ui/src/fleet/groups.ts`, `internal/web/ui/src/panels/fleet/FleetHead.tsx`, `internal/web/ui/src/fleet/links.ts`, `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/routes.tsx:100-200`, `internal/web/ui/src/app/commands.ts:20-90`, `internal/web/ui/src/commands/testkit.ts`, `internal/web/ui/src/fixtures.ts` (`fixtureSnapshot`)
  - **Drafts after:** Story 1

- **Story 3** — The palette dialog, its chord, and the top bar's box.

  Add `desktop/paletteState.ts` and `desktop/Palette.tsx` (spec §18, §20, §7; Part 2 settlements, decisions 2, 6, 10, 11): a Dialog around an inline Base UI Autocomplete over `paletteGroups`, a command not enabled drawn dimmed and inert, Enter or a click opening a session or a place, or closing the palette and running the command through `runCommand` with the route's context. `DesktopShell` draws it and binds the chord, which toggles it from anywhere, a text field included, and does nothing while a confirmation is open. The top bar gains `PaletteBox`, with the chord's label for the platform. Tests pin the chord from a text field, its refusals (Alt, Shift, composing, a confirmation open), a second chord closing, the box opening it, an empty query on reopening, Enter on a session and a place navigating, Enter on a command running it through `runCommand` after the palette closes, a stale snapshot's session commands dimmed and inert while the view toggles still run, and both labels.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/desktop/paletteState.ts`, `internal/web/ui/src/desktop/paletteState.test.ts`, `internal/web/ui/src/desktop/Palette.tsx`, `internal/web/ui/src/desktop/Palette.test.tsx`, `internal/web/ui/src/desktop/TopBar.tsx`, `internal/web/ui/src/desktop/TopBar.test.tsx`, `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/DesktopShell.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `PaletteState`, `usePalette`, `isPaletteChord`, `usePaletteKey`, `paletteKeyLabel`, `PaletteBox`, `Palette`, the slots `palette`, `palette-input`, `palette-item` and `palette-box`; uses `paletteGroups`, `PaletteItem`, `PaletteGroup` (Story 2), `allCommands`, `confirmOpen` (Story 1), `useCommandContext`, `runCommand`, `useFleet`, `sessionTabs`, `Dialog`, `DialogContent`, `DialogTitle`, `Autocomplete`, `TopBar`, `HostLabel`, `WorkspaceFilter`, `setViewportWidth`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (a dialog over a pure module and a third-party list; each behaviour a named test)
  - **Reading:** spec §7, §18, §20, Errors (a stale snapshot), Part 2 settlements (decisions 2, 6, 10, 11); `internal/web/ui/src/desktop/DesktopShell.tsx`, `internal/web/ui/src/desktop/TopBar.tsx`, `internal/web/ui/src/components/ui/dialog.tsx`, `internal/web/ui/src/app/commands.ts:60-200`, `internal/web/ui/src/desktop/DesktopShell.test.tsx:1-80`, `internal/web/ui/src/desktop/TopBar.test.tsx`, Base UI's Autocomplete types (`internal/web/ui/node_modules/@base-ui/react/autocomplete/`, after the install), `docs/superpowers/specs/2026-11-21-web-desktop/frame.html` (the box)
  - **Drafts after:** Story 2

- **Story 4** — webshot renders the palette at both desktop sizes.

  Add the desktop-only `palette` screen to webshot (spec Testing; Part 2 settlements, decision 12): `screenMode` gains `desktopOnly`, `press` and `drawn`; `shoot` presses the chord through CDP once the app is drawn and fails the screen when the palette's slot never appears; a phone skips the screen as a desktop skips `add-to-home`. `.github/workflows/web.yml` already renders every screen at the phone and both desktop sizes and does not change. Tests pin the chord's parsing into a key and modifiers, the screen's mode, `forDevice` on both kinds of device, and, in the real-browser run (only with `LOOM_WEBSHOT_BROWSER`), `palette.png` written at both desktop sizes and absent on the phone. Update `tools/CLAUDE.md`'s device rule for the renamed test and the desktop-only screen.

  - **Developer:** intermediate-dev
  - **Files:** `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `tools/CLAUDE.md`
  - **Interfaces:** exposes the `palette` screen, `screenMode`'s `desktopOnly`, `press` and `drawn`, `page.press`; uses the `palette` slot and the chord (Story 3), `forDevice`, `shoot`, `page.query`, `cdpClient.call`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ ./tools/claudemd/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop -screens palette -out "$d/w" && CGO_ENABLED=0 go run ./tools/webshot -app next -device desktop-narrow -screens palette -out "$d/n" && test -s "$d/w/palette.png" && test -s "$d/n/palette.png"'`
  - **Review:** light (a test tool's screen and a key press)
  - **Form:** brief (a screen entry, a mode and a CDP call)
  - **Reading:** spec Testing (webshot), Part 2 settlements (decision 12); `tools/webshot/main.go:56-100`, `tools/webshot/main.go:286-302`, `tools/webshot/main.go:485-643`, `tools/webshot/cdp.go`, `tools/webshot/webshot_test.go:740-970`, `tools/CLAUDE.md` (the webshot device rule)
  - **Drafts after:** Story 3

- **Story 5** — The rules and the procedures for the palette.

  Amend `internal/web/ui/CLAUDE.md`'s offline rule to name the two view toggles, and add the rules that a command sets `palette` only when it takes no args and that the palette's chord is the app's only document key listener, bound in `DesktopShell` (spec §27 as the Part 2 settlements re-cut it, decision 13). Add to `docs/claude/web-layer.md` adding a palette command and a palette place, amend its "Adding a command" sentence on `offline`, and make its desktop checklist's item 8 the palette's. Add the palette to `USAGE.md`'s desktop paragraph and `internal/web/README.md`'s desktop layout. Each rule names the test that enforces it or says Convention, and the CLAUDE.md lint passes. `internal/web/CLAUDE.md` and root `CLAUDE.md` do not change.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md`, `internal/web/README.md`
  - **Interfaces:** none
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./tools/claudemd/ && CGO_ENABLED=0 go run ./tools/claudemd'`
  - **Review:** light (docs)
  - **Form:** brief (docs)
  - **Reading:** spec §27, Part 2 settlements (decision 13); `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md:35-50`, `docs/claude/web-layer.md:113-125`, `USAGE.md:820-845`, `internal/web/README.md:105-110`
  - **Drafts after:** Story 1, Story 2, Story 3, Story 4

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
