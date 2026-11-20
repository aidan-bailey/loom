# Loom web foundation, part 3: the rest, to parity — Implementation Plan

> **For agentic workers:** this plan runs supervised: `/flow:draft` writes the stories from this skeleton, then `/flow:run` runs them story by story. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the classic app's remaining screens to the new app with their classic behaviour (the flow board, story and document views and the flow tab, the host's flows, new session, settings with push enabling, pairing and Add to Home), every action a command, so that no route draws the placeholder.

**Architecture:** Every screen is a panel on part 1's registry and every action a command on its command registry; the flow views, the host's flows, new session and settings load as chunks of their own, pairing stays in the entry. Reads go through `src/api/reads.ts` with the classic triggers (a board read again when the session's flow summaries change, launch defaults read once per workspace). The device, quick-replies, create, unpair, pair and push-enable commands write through the generated client, apply each answer to the store, and keep classic's words; a session just created is pending for 5 s so its views draw "Loading…". No Go route, wire or Core change: the Go side gains only webshot's fixtures, two webshot screens and the chunk test's list.

**Tech Stack:** TypeScript, React 19, Vite, Tailwind CSS 4, shadcn/ui on Base UI 1.9 (Select, Switch, OTPField), TanStack Router 1 and Query 5, Zustand 5, lucide-react, Vitest with happy-dom and Testing Library; Go (testify) for the bundle and fixture tests; `tools/webshot` (headless Chromium).

**Spec:** `docs/superpowers/specs/2026-11-18-web-foundation-design.md`

**Part:** 3 of 3

**Budget:** draft $55; run $40 (estimate 2026-11-20)

## File structure

The new app (`internal/web/ui/src/`):

- `components/ui/select.tsx`, `components/ui/switch.tsx`, `components/ui/input.tsx`, `components/ui/input-otp.tsx` (new); `styles/app.css` (changed: the scrollbar rules).
- `api/reads.ts` (changed): the board, a document, the host's flows and launch defaults.
- `store/pending.ts` (new); `store/fleet.ts` (changed: recent notices kept, the pending id cleared on unpair).
- `commands/launch.ts`, `commands/device.ts`, `commands/push.ts` (new); `commands/index.ts` (changed).
- `app/push.ts` (new): the phone's push state; `app/worker.ts` (changed: its push helpers moved to `app/push.ts`).
- `app/install.tsx` (new): the Add to Home gate; `main.tsx` (changed).
- `panels/flow/` (new): `index.tsx`, `parts.tsx`, `FlowBody.tsx`, `FlowStoryBody.tsx`, `FlowDocBody.tsx`.
- `panels/flows/`, `panels/new/`, `panels/settings/` (new): each an `index.tsx` and its lazy Body; `panels/pair/` (new): `index.tsx`, `Pair.tsx`; `panels/index.ts` (changed).
- `session/SessionHead.tsx`, `session/NoSession.tsx`, `panels/agent/Agent.tsx`, `panels/job/index.tsx` (changed: "Loading…" while pending); `panels/fleet/index.tsx` (changed: `FILTER_KEY` exported).
- `app/shell.tsx` (changed: no placeholder, the splash's way back).
- `fixtures.ts`, `fixtures.test.ts` (changed).
- `*.test.ts(x)` beside each module they test.

The built bundle: `internal/web/dist/` (rebuilt by every story that changes the app, committed).

Elsewhere: `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/flow.json`, `tools/webshot/main.go`, `tools/webshot/canned.go`, `tools/webshot/webshot_test.go`, `internal/web/webshot_fixture_test.go`; `internal/web/static_budget_test.go`; `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `internal/web/README.md`.

## Names contract

Every name one story defines and another uses. A story may add names of its own; a name another story needs and this section lacks goes back to the manager. Paths are under `internal/web/ui/src/` unless they start at the repo root.

**webshot (Story 1):**

- `tools/webshot/fixture/snapshot.json` (Story 1): session 6's `flows` gains a second, older summary after its first (another plan path and title, stage `spec`, status `designed`), so the board's picker shows; `device.push` is `"expired"` and `device.notify.paused_until` `"now+40m"`; `notices` holds two entries, one with `error` true. Every other session and field the existing tests read stays as it is.
- `tools/webshot/fixture/flow.json` (Story 1): `final` becomes a row `F` with steps (gates passed, review running); `kept` keeps a line; story 4 starts, its dev step `running` with a `waiting` text; `others` holds one run; `costs.unpriced` names one agent. The rows, cells and ids the existing tests read stay as they are.
- The webshot screens `pair` (Story 1), hash `#pair`, shot with the canned loom answering every `/api/v1/` request 401 with `authed`'s body (`internal/web/auth.go`), none counted a miss; and `add-to-home` (Story 1), hash `""`, shot as an iPhone browser tab that is not standalone. Both are in the one screen list both apps render.
- `function fixtureFlow(): Flow`, `function fixtureFlowDoc(): FlowDoc`, `function fixtureFlows(): Flows` and `function fixtureLaunchDefaults(): LaunchDefaults` (Story 1), in `fixtures.ts`: `flow.json`, `flow-doc.json`, `flows.json` and `launch-defaults.json`, their relative times filled as `fixtureSnapshot` fills them.

**Components (Story 2)**, each owned source in `components/ui/`, text at 16px and touch targets at 44px:

- `components/ui/select.tsx` (Story 2): shadcn's Select on `@base-ui/react/select`, exporting `Select`, `SelectTrigger`, `SelectValue`, `SelectContent`, `SelectGroup`, `SelectLabel` and `SelectItem` (Story 2 proposes).
- `components/ui/switch.tsx` (Story 2): shadcn's Switch on `@base-ui/react/switch`, exporting `Switch` (Story 2 proposes).
- `function Input(props: ComponentProps<"input">): ReactElement` (Story 2), in `components/ui/input.tsx`: shadcn's Input on a plain `<input>`.
- `components/ui/input-otp.tsx` (Story 2): the Input OTP on `@base-ui/react/otp-field`, exporting `InputOTP` (the root, passing `length`, `validationType`, `normalizeValue`, `value` and `onValueChange` through), `InputOTPSlot` and `InputOTPSeparator` (Story 2 proposes).
- `styles/app.css` (Story 2): `.base-ui-disable-scrollbar { scrollbar-width: none }` and `.base-ui-disable-scrollbar::-webkit-scrollbar { display: none }`.

**Reads (Story 3)**, in `api/reads.ts`:

- `const KEEP_FLOWS = 8` (Story 3).
- `interface FlowOptions { session: number, plan: string, key: string }` (Story 3): `key` is what `flowKey` answers for the session (below); once a board is held, a new `key` reads it again.
- `interface FlowView { flow: Flow | undefined, error: string, loading: boolean }` (Story 3).
- `function useFlow(options: FlowOptions): FlowView` (Story 3): reads on mount and on a new `key`, no poll; held per session and plan with `gcTime: Infinity`, the least recently opened board evicted past `KEEP_FLOWS`; a failed read keeps the held board and sets `error`; an empty `plan` reads nothing (`flow` undefined, `loading` false).
- `function flowKey(session: Session): string` (Story 3): `JSON.stringify(session.flows)`.
- `interface FlowDocOptions { session: number, plan: string, doc: string }` (Story 3).
- `interface FlowDocView { doc: FlowDoc | undefined, error: string, loading: boolean }` (Story 3).
- `function useFlowDoc(options: FlowDocOptions): FlowDocView` (Story 3): reads on mount and on a change of any option, no poll; an empty `plan` or `doc` reads nothing.
- `interface FlowsView { flows: Flows | undefined, error: string, loading: boolean, reload: () => void }` (Story 3).
- `function useFlows(): FlowsView` (Story 3): reads on mount and on `reload`, no poll; a failed read keeps the held answer and sets `error`.
- `interface LaunchDefaultsView { defaults: LaunchDefaults | undefined, error: string, loading: boolean, retry: () => void }` (Story 3).
- `function useLaunchDefaults(workspace: number | undefined, live: boolean): LaunchDefaultsView` (Story 3): reads once per workspace once `live` is true and holds the answer for the page's lifetime (never read again on its own, not when the stream comes back); `retry` reads again after a failure; an undefined `workspace` reads nothing.

**The pending session, recent notices and the commands (Story 4):**

- `const PENDING_MS = 5000` (Story 4), in `store/pending.ts`.
- `interface PendingState { id: number | null, mark: (id: number) => void, clear: () => void }` (Story 4): `mark` replaces any earlier id and clears it after `PENDING_MS`; the id is also cleared once the snapshot lists that session.
- `const usePending: UseBoundStore<StoreApi<PendingState>>` (Story 4); `startFleet`'s unpaired handler in `store/fleet.ts` calls its `clear()`.
- `function useIsPending(id: number | undefined): boolean` (Story 4), in `store/pending.ts`.
- `const MAX_NOTICES = 20` (Story 4), in `store/fleet.ts`: `startFleet` appends every notice that `onNotices` delivers (a response's and each stream `notice`) to the held `snapshot.notices`, keeping the last `MAX_NOTICES`.
- `interface LaunchPicks { account: string, model: string, permission_mode: string, effort: string }` (Story 4), in `commands/launch.ts`.
- `interface LaunchForm { workspace: number, title: string, prompt: string, picks: LaunchPicks }` (Story 4).
- `function launchBody(form: LaunchForm, defaults: LaunchDefaults): CreateBody` (Story 4): the title trimmed, the prompt's end trimmed, and each pick only where it differs from its default (spec Part 3 settlement 7).
- `session.create` (Story 4), in `commands/launch.ts`, args `{ body: CreateBody }`, no sheet: a 201 marks the new id pending and opens `hrefFor("turns", { id })` with `window.location.assign`; a 202 toasts "Still starting; the session will show in the fleet." and opens the fleet; any other outcome goes to `showOutcome`.
- `device.update` (Story 4), in `commands/device.ts`, args `DeviceBody`, keyed by the names of the fields it sets, no sheet: a 200 applies the answered `Device` to the store as a `device` event.
- `quickReplies.save` (Story 4), in `commands/device.ts`, args `{ replies: string[] | null }` (null resets to the defaults), no sheet: a 200 applies the answer's `quick_replies` to the store as a `quick_replies` event.
- `device.unpair` (Story 4), in `commands/device.ts`, args `void`: a destructive sheet with the classic words (spec Part 3 settlement 13); a 204 calls `emitUnpaired()`.
- `device.pair` (Story 4), in `commands/device.ts`, args `{ code: string }`, `offline`, `when` the route is `pair`, no sheet: a 200 calls `startFleet()` and opens the fleet; a refusal is an error toast with the daemon's text; a failed request is "Can't reach loom. Check the connection and try again.".
- `commands/index.ts` (Story 4): imports `launch` and `device` for their registration; `internal/web/ui/CLAUDE.md`'s offline rule names `device.pair` beside `app.backToClassic` (Story 4).

**Push (Story 5):**

- `type PushStatus = "loading" | "unsupported" | "denied" | "on" | "again" | "off"` (Story 5), in `app/push.ts`: classic `pushView`'s states.
- `function usePushStatus(device: Device): PushStatus` (Story 5), in `app/push.ts`: reads the phone's own push state on mount, when `device.push` changes and when a `push.enable` run ends.
- `app/push.ts` (Story 5) also holds the push helpers moved from `app/worker.ts` (`pushSupported`, `activeRegistration`, `subscriptionBody`), which `app/worker.ts` imports (Story 5 proposes).
- `push.enable` (Story 5), in `commands/push.ts`, args `void`, no sheet, `when` the route is `settings`: spec Part 3 settlement 12, `Notification.requestPermission()` its first call; `commands/index.ts` imports it.

**The flow views (Stories 6 to 8):**

- `function planOf(session: Session, plan: string): string` (Story 6), in `panels/flow/parts.tsx`: `plan` when not empty, else the session's first (newest) flow's plan, else `""`.
- `function usd(value: number | null | undefined): string` (Story 6): classic `usd` (`$x.xx`, `$x.xxxx` above 0 and under 0.01, `""` for no number).
- `function median(values: number[]): number` (Story 6).
- `function Bars(props: { values: number[], current: number | null }): ReactElement` (Story 6): the per-story bars, oldest first, `current` an accent bar after them when not null, each height through the `style` prop (spec Part 3 settlement 5).
- `function useClock(ms: number): number` (Story 6): the time now, re-rendering every `ms`.
- The `flow` panel (Story 6), in `panels/flow/index.tsx`: `{ id: "flow", tab: { order: 5, label: "flow", Mark: FlowMark }, Head: SessionHead }`, its `appliesTo` true while the session has flows, its `Body` `React.lazy` over `panels/flow/FlowBody.tsx`.
- `function FlowMark(props: { session: Session }): ReactElement | null` (Story 6): an amber dot while any of the session's flows asks.
- The `flowStory` and `flowDoc` panels (Story 7), registered in `panels/flow/index.tsx`: `{ id: "flowStory", Head: SessionHead }` and `{ id: "flowDoc", Head: SessionHead }`, their `Body`s `React.lazy` over `panels/flow/FlowStoryBody.tsx` and `panels/flow/FlowDocBody.tsx`.
- The `flows` panel (Story 8), in `panels/flows/index.tsx`: `{ id: "flows", Head: FlowsHead }`, its `Body` `React.lazy` over `panels/flows/FlowsBody.tsx`.
- `function FlowsHead(props: PanelProps): ReactElement` (Story 8): "Flows", back to the fleet, and ↻ calling `useFlows().reload`.

**New session, settings and pairing (Stories 9 to 11):**

- The `new` panel (Story 9), in `panels/new/index.tsx`: `{ id: "new", Head: NewHead }`, its `Body` `React.lazy` over `panels/new/NewSessionBody.tsx`.
- `function NewHead(props: PanelProps): ReactElement` (Story 9): "New session", back to the fleet.
- `const FILTER_KEY = "loom.filter"` (Story 9): part 1's private constant in `panels/fleet/index.tsx`, exported.
- `SessionHead`, `AgentHead`, `JobHead` and `NoSession` (Story 9): "Loading…" while `useIsPending` holds the route's session (spec Part 3 settlement 8).
- The `settings` panel (Story 10), in `panels/settings/index.tsx`: `{ id: "settings", Head: SettingsHead }`, its `Body` `React.lazy` over `panels/settings/SettingsBody.tsx`.
- `function SettingsHead(props: PanelProps): ReactElement` (Story 10): "Settings", back to the fleet.
- The `pair` panel (Story 11), in `panels/pair/index.tsx`: `{ id: "pair" }` with no `Head`, its `Body` the entry's `Pair` from `panels/pair/Pair.tsx`.
- `const PAIRING_ALPHABET = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"`, `const CODE_LENGTH = 8` and `function normalizeCode(value: string): string` (Story 11), in `panels/pair/Pair.tsx`: upper-cased, every character outside the alphabet dropped, at most `CODE_LENGTH`; idempotent, as `OTPField`'s `normalizeValue` must be.
- `const BROWSER_KEY = "loom.browser"`, `function needsInstall(): boolean` and `function AddToHome(props: { onContinue: () => void }): ReactElement` (Story 11), in `app/install.tsx`; `main.tsx` reads `needsInstall()` once and, while it is true, renders only `AddToHome` and starts nothing (Story 11).

**No placeholder (Story 12):**

- `app/shell.tsx` (Story 12): `Placeholder` removed; the splash drawn with no snapshot draws `<CommandButton id="app.backToClassic" />` on every route; `PanelHost` draws nothing for a panel with none registered; `app/panels.test.ts` pins that `panelFor` answers every `PanelId` once `@/panels` is imported.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): Story 6's `panels/flow/index.tsx` (Story 7); `panels/index.ts` as each panel story leaves it (the next one); `checkLazyViews`' list in `internal/web/static_budget_test.go` as each lazy story leaves it (the next one); Story 4's `internal/web/ui/CLAUDE.md` (Story 13).

## Stories

- **Story 1** — webshot's fixtures carry the flow, device and notices part 3 draws, and two new screens show pairing and Add to Home.

  Add to the canned loom's fixtures what part 3's screens draw but no fixture holds (spec Part 3 settlement 17): a second flow on session 6, a final row, a running step with its wait, another run and an unpriced agent on the board, an expired push and a pause on the device, and two notices. Give webshot a `pair` screen shot unpaired (every API request answered 401 as `authed` answers it, none counted a miss) and an `add-to-home` screen shot as a browser tab, both in the one list both apps render, and add the typed fixture loaders Vitest needs. Every fixture still decodes into its Go type, the existing webshot tests keep passing, and the classic app renders every screen without a console error.

  - **Developer:** intermediate-dev
  - **Files:** `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/flow.json`, `tools/webshot/main.go`, `tools/webshot/canned.go`, `tools/webshot/webshot_test.go`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/src/fixtures.ts`, `internal/web/ui/src/fixtures.test.ts`
  - **Interfaces:** exposes the fixtures' second flow, final row, running step, other run, device and notices, the `pair` and `add-to-home` screens, `fixtureFlow`, `fixtureFlowDoc`, `fixtureFlows`, `fixtureLaunchDefaults`; uses part 1's `fillTimes` and `fixtureSnapshot`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app classic -screens flow,flow-story,flows,settings,pair,add-to-home -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -screens pair,add-to-home -out "$d"'`
  - **Review:** light (fixture data and a test tool's screen modes; the Go decode test and the typed fixture test check the shapes)
  - **Form:** brief (fixture data, their coverage rows and two webshot screen modes)
  - **Reading:** spec Testing, §9, Part 2 settlements (webshot), Part 3 settlements (webshot), 17; `tools/webshot/main.go:40-100`, `tools/webshot/main.go:400-546`, `tools/webshot/canned.go:20-216`, `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/flow.json`, `internal/web/auth.go:1-75`, `internal/web/static/js/pair.mjs:1-56`, `internal/web/ui/src/api/stream.ts`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/src/fixtures.ts`, `internal/web/ui/src/fixtures.test.ts`, `tools/webshot/webshot_test.go:150-300`

- **Story 2** — Select, Switch, Input and Input OTP, and the scrollbar rules Base UI's Select needs.

  Add the shadcn components part 3's forms use (spec §32, Part 3 settlements (Components), 7, 14), each on Base UI 1.9 and owned in `components/ui/`: Select (the new-session pickers, with groups and labels), Switch (the notification toggles), Input (the title) and Input OTP on Base UI's `OTPField` (the pairing code, passing its normalisation through; no `input-otp` package). Every one keeps form text at 16px and touch targets at 44px and injects no `<style>` (the CSP provider stays), and `app.css` gains the two `.base-ui-disable-scrollbar` rules Base UI no longer injects. Vitest renders each, picks a Select item, toggles a Switch and fills the OTP.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/components/ui/select.tsx`, `internal/web/ui/src/components/ui/switch.tsx`, `internal/web/ui/src/components/ui/input.tsx`, `internal/web/ui/src/components/ui/input-otp.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/styles/app.css`, `internal/web/dist/`
  - **Interfaces:** exposes `Select`, `SelectTrigger`, `SelectValue`, `SelectContent`, `SelectGroup`, `SelectLabel`, `SelectItem`, `Switch`, `Input`, `InputOTP`, `InputOTPSlot`, `InputOTPSeparator`; uses part 1's `cn` and tokens
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (owned copies of shadcn components on Base UI; the CSP and the phone sizes are named tests)
  - **Form:** brief (shadcn components and two CSS rules)
  - **Reading:** spec §32, §33, §34, Part 2 settlements (Components), Part 3 settlements (Components); `internal/web/ui/components.json`, `internal/web/ui/src/components/ui/textarea.tsx`, `internal/web/ui/src/components/ui/toggle-group.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/styles/app.css`, `internal/web/ui/node_modules/@base-ui/react/docs/react/components/otp-field.md`, `internal/web/ui/node_modules/@base-ui/react/utils/styles.mjs`

- **Story 3** — The reads for the flow views, the host's flows and launch defaults.

  Add to `src/api/reads.ts` the part 3 reads with their classic triggers (spec §26, Part 3 settlements 2 and 7): the board, read on open and whenever the session's flow summaries change, held per session and plan for the last 8; a flow document, read on open and on a change of session, plan or document; the host's flows, read on open and on reload; launch defaults, read once per workspace once the stream is live and held for the page's lifetime, with a retry. None polls. Reuse part 2's query helpers (the eviction, `unwrap`, the error text). Vitest covers each trigger, the eviction past 8, the held answer kept on a failed read, an empty plan or doc reading nothing, and launch defaults not read again when the stream returns.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/api/reads.ts`, `internal/web/ui/src/api/reads.test.ts`, `internal/web/dist/`
  - **Interfaces:** exposes `KEEP_FLOWS`, `FlowOptions`, `FlowView`, `useFlow`, `flowKey`, `FlowDocOptions`, `FlowDocView`, `useFlowDoc`, `FlowsView`, `useFlows`, `LaunchDefaultsView`, `useLaunchDefaults`; uses part 1's `api`, `queryClient`, `unwrap` and `OutcomeError`, part 2's `keepRecent`, and Story 1's fixture loaders
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (four reads on part 2's helpers, the eviction reused; each trigger and the bound is a named test)
  - **Reading:** spec §26, Part 3 settlements (Store), 2, 7; `internal/web/ui/src/api/reads.ts`, `internal/web/ui/src/api/reads.test.ts:1-120`, `internal/web/ui/src/api/queries.ts`, `internal/web/static/js/flow.mjs:80-110`, `internal/web/static/js/flowdoc.mjs:57-93`, `internal/web/static/js/flows.mjs:134-213`, `internal/web/static/js/newsession.mjs:40-100`, `internal/web/flow.go:320-393`, `internal/web/ui/src/api/gen.ts:145-280`, `internal/web/ui/src/api/gen.ts:331-344`
  - **Drafts after:** Story 1

- **Story 4** — A session just created is pending, the store keeps the recent notices, and the create, device, quick-replies, unpair and pair commands.

  Add the pending slice (an id marked by a 201 for 5 s, cleared once the snapshot lists it or on unpair; spec Part 3 settlement 8), keep the recent notices of responses and the stream in the held snapshot (the last 20, settlement 11), and register the remaining writes as commands with the classic words (§23 as settled): `session.create` with `launchBody` (only the picks that differ from the defaults; 201 opens the session, 202 the fleet), `device.update` and `quickReplies.save` (each answer applied to the store as its stream event, settlement 10), `device.unpair` (its sheet; a 204 runs the unpaired path), and `device.pair` (offline; a 200 starts the fleet again and opens it; settlement 14). Amend `internal/web/ui/CLAUDE.md`'s offline rule to name `device.pair`. Vitest covers the bodies each sends, each outcome's toast and navigation, the answers applied to the store, the pending timer and its clearing, the notices' bound, and `device.pair` running while stale where the others don't.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/store/pending.ts`, `internal/web/ui/src/store/pending.test.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/store/startFleet.test.ts`, `internal/web/ui/src/commands/launch.ts`, `internal/web/ui/src/commands/launch.test.ts`, `internal/web/ui/src/commands/device.ts`, `internal/web/ui/src/commands/device.test.ts`, `internal/web/ui/src/commands/index.ts`, `internal/web/ui/src/commands/index.test.ts`, `internal/web/ui/CLAUDE.md`, `internal/web/dist/`
  - **Interfaces:** exposes `PENDING_MS`, `PendingState`, `usePending`, `useIsPending`, `MAX_NOTICES`, `LaunchPicks`, `LaunchForm`, `launchBody`, and the commands `session.create`, `device.update`, `quickReplies.save`, `device.unpair`, `device.pair`; uses part 1's command path, `api`, `showOutcome`, `showMessage`, `hrefFor`, `onNotices`, `emitUnpaired`, `startFleet` and `useFleet`, part 2's `commands/testkit.ts`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
  - **Review:** deep
  - **Form:** full (the bodies sent to the daemon, the unpaired path a revoke runs, an offline command that bypasses the stale gate, store writes from answers and the bounded notices)
  - **Reading:** spec §20, §23, Part 3 settlements (Commands, Store), 7, 8, 10, 11, 13, 14; `internal/web/static/js/app.mjs:216-231`, `internal/web/static/js/app.mjs:315-358`, `internal/web/static/js/newsession.mjs:1-31`, `internal/web/static/js/newsession.mjs:105-123`, `internal/web/static/js/settings.mjs:100-135`, `internal/web/static/js/settings.mjs:186-195`, `internal/web/static/js/pair.mjs:58-80`, `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/commands/session.ts`, `internal/web/ui/src/commands/testkit.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/api/request.ts:1-80`, `internal/web/ui/src/app/toasts.ts`, `internal/web/api.go:213-263`, `internal/web/api.go:365-503`, `internal/web/auth.go:75-150`, `internal/web/ui/src/api/gen.ts:103-128`, `internal/web/ui/src/api/gen.ts:620-687`, `internal/web/ui/CLAUDE.md` (the offline rule)

- **Story 5** — Turning push on from the phone.

  Port `push.mjs`'s enable flow as the command `push.enable` (spec Part 3 settlement 12): the permission asked first, inside the tap; an active worker within 5 s; the key read; the old subscription dropped and a new one made with the key; the subscription posted, and the phone's dropped again if that fails; each failure an error toast with the classic words, success "Notifications are on.". Move `app/worker.ts`'s push helpers into `app/push.ts`, beside `usePushStatus`, the control's classic states read from the phone and the device, re-read when the device's push state changes and when a turn-on ends. Vitest with a stubbed `Notification`, `PushManager` and worker covers the order of the calls (the permission before any await), each failure's words and clean-up, and each state.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/push.ts`, `internal/web/ui/src/app/push.test.ts`, `internal/web/ui/src/app/worker.ts`, `internal/web/ui/src/app/worker.test.ts`, `internal/web/ui/src/commands/push.ts`, `internal/web/ui/src/commands/push.test.ts`, `internal/web/ui/src/commands/index.ts`, `internal/web/dist/`
  - **Interfaces:** exposes `PushStatus`, `usePushStatus`, the moved `pushSupported`, `activeRegistration` and `subscriptionBody`, and the command `push.enable`; uses part 1's command path, `api`, `showMessage` and `checkPushSubscription`, part 2's `commands/testkit.ts`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (outside calls in an order iOS enforces: the permission inside the gesture, a subscription made and dropped again on a failed post)
  - **Reading:** spec §28, Part 3 settlements (Push, Commands), 12; `internal/web/static/js/push.mjs`, `internal/web/ui/src/app/worker.ts`, `internal/web/ui/src/app/worker.test.ts`, `internal/web/ui/src/app/commands.ts:150-181`, `internal/web/push.go:360-384`, `internal/web/api.go:441-493`, `internal/web/ui/src/api/gen.ts:428-430`, `internal/web/ui/src/api/gen.ts:510-519`

- **Story 6** — The flow board and the flow tab.

  Register the `flow` panel (order 5, its asks dot, shown while the session has flows; spec Part 3 settlement 4) with its lazy `FlowBody`, and port the classic board (`flow.mjs`'s `Flow`) over Story 3's `useFlow`: the gone, no-flow and loading states, the picker for 2 or more flows, the title and part, the error and unreadable lines, the stage strip with its document links, the lost and phase lines, the question box, the stories with their cells and right-hand text, the cost box (role bar, legend, per-story comparison, unpriced line, the cost error) and the other runs, each unknown value in neutral styling (settlements 1, 3 and 5). Add the shared parts the story view and the host's flows reuse, and the chunk to `checkLazyViews`. Vitest covers each state, the cells' words, the cost box's forms and the tab; webshot renders the flow screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/flow/index.tsx`, `internal/web/ui/src/panels/flow/parts.tsx`, `internal/web/ui/src/panels/flow/FlowBody.tsx`, `internal/web/ui/src/panels/flow/flow.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `flow` panel, `FlowMark`, `planOf`, `usd`, `median`, `Bars`, `useClock`; uses Story 3's `useFlow` and `flowKey`, Story 1's `fixtureFlow`, part 2's `SessionHead`, `NoSession` and `elapsed`, part 1's `definePanel`, `useSession`, `hrefFor` and `Badge`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens flow -out "$d"'`
  - **Review:** deep
  - **Form:** brief (a port of `flow.mjs`'s board over Story 3's read)
  - **Reading:** spec §15, §29, Part 3 settlements (Routes and the placeholder, The budget), 1, 2, 4, 5, 6; `internal/web/static/js/flow.mjs:1-293`, `internal/web/static/js/chrome.mjs:63-97`, `internal/web/static/app.css:788-891`, `internal/web/ui/src/api/gen.ts:8-24`, `internal/web/ui/src/api/gen.ts:145-280`, `internal/web/ui/src/panels/job/index.tsx`, `internal/web/ui/src/panels/mockup/index.tsx`, `internal/web/ui/src/app/shell.tsx:299-400`, `internal/web/ui/src/session/jobs.ts:42-52`, `internal/web/static_budget_test.go:180-262`
  - **Drafts after:** Story 1, Story 3

- **Story 7** — The flow story and document views.

  Register the `flowStory` and `flowDoc` panels with their lazy Bodies and port `flow.mjs`'s `FlowStory` and `flowdoc.mjs`'s `FlowDoc` (spec Part 3 settlements 1 to 3). The story view, over the board Story 3 holds: back to the board with its plan, its title, facts, steps (outcome dot, report link, elapsed and cost, unreadable and waiting lines, gate lines linking to jobs), findings, the final row's kept lines, and its reads (the plan's lines, each spec section once, the changes), "The board has no row <id>." for a row it lacks. The document view, over `useFlowDoc`: back to the board, its name and path, the sections index, `Markdown`, the cut note "The document was cut short.", and the jump to `at` (a `§N` section or a source line) below the pinned header. Both draw "This session is gone." and "No flow in this session's worktree." where classic hangs on "Loading…". Add both chunks to `checkLazyViews`. Vitest covers each state, the jump's targets and offset, and the de-duplicated links; webshot renders both screens.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/flow/index.tsx`, `internal/web/ui/src/panels/flow/FlowStoryBody.tsx`, `internal/web/ui/src/panels/flow/FlowDocBody.tsx`, `internal/web/ui/src/panels/flow/story.test.tsx`, `internal/web/ui/src/panels/flow/doc.test.tsx`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `flowStory` and `flowDoc` panels; uses Story 3's `useFlow`, `flowKey` and `useFlowDoc`, Story 6's `planOf`, `usd` and `useClock`, Story 1's `fixtureFlow` and `fixtureFlowDoc`, part 2's `Markdown`, `SessionHead`, `NoSession` and `elapsed`, part 1's `definePanel`, `useSession` and `hrefFor`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens flow-story,flow-doc -out "$d"'`
  - **Review:** deep
  - **Form:** brief (ports of `flow.mjs`'s story view and `flowdoc.mjs` over Story 3's reads)
  - **Reading:** spec §33, Part 3 settlements 1, 2, 3, 6; `internal/web/static/js/flow.mjs:296-391`, `internal/web/static/js/flowdoc.mjs`, `internal/web/flow.go:375-393`, `internal/web/markdown.go:25-83`, `internal/web/ui/src/conversation/Markdown.tsx`, `internal/web/ui/src/api/gen.ts:184-255`, `internal/web/static/app.css:788-850`
  - **Drafts after:** Story 1, Story 3, Story 6

- **Story 8** — The host's flows.

  Register the `flows` panel with `FlowsHead` (back to the fleet, ↻ reading again) and a lazy `FlowsBody`, and port `flows.mjs` over Story 3's `useFlows` (spec Part 3 settlements 1 and 2): the error and loading states; the live box, each flow a link to its board with its title, part and chip (part 1's `flowChip`), "No session's worktree holds a flow." when empty; one past box per repo, in first-seen order, named as classic `repoName` names it, 8 runs shown then "and N more", each run's stem, stories, cost, ended and age or "unreadable", and the per-story cost bars of its last 5 priced runs (none under 2); "No run records yet.". Add the chunk to `checkLazyViews`. Vitest covers each box's forms; webshot renders the flows screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/flows/index.tsx`, `internal/web/ui/src/panels/flows/FlowsBody.tsx`, `internal/web/ui/src/panels/flows/flows.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `flows` panel and `FlowsHead`; uses Story 3's `useFlows`, Story 6's `usd`, `median` and `Bars`, Story 1's `fixtureFlows`, part 1's `flowChip`, `ago`, `ContextRow`, `definePanel` and `hrefFor`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens flows -out "$d"'`
  - **Review:** light (a port of `flows.mjs` over Story 3's read)
  - **Form:** brief (a port of `flows.mjs`)
  - **Reading:** Part 3 settlements 1, 2, 5, 6; `internal/web/static/js/flows.mjs`, `internal/web/flow.go:270-294`, `internal/web/ui/src/api/gen.ts:346-350`, `internal/web/ui/src/api/gen.ts:404-414`, `internal/web/ui/src/panels/fleet/SessionRow.tsx:17-60`, `internal/web/ui/src/panels/fleet/FleetHead.tsx`
  - **Drafts after:** Story 1, Story 3, Story 6

- **Story 9** — New session, and "Loading…" for the session it creates.

  Register the `new` panel with `NewHead` and a lazy `NewSessionBody`, and port `newsession.mjs` onto Story 2's components, Story 3's `useLaunchDefaults` and Story 4's `session.create` and `launchBody` (spec Part 3 settlement 7): the workspace Select (the fleet's filter choice first, grouped when some are hidden, "No workspace is open." with none), the title and the prompt with its 64 KiB line, the retry line, "Launch options" with its four Selects (account only when listed, defaults first, "(default)"), and "Start session" with the classic enablement and Enter; a refused create keeps the form. Export the fleet's `FILTER_KEY`. Draw "Loading…" while the route's session is pending in `SessionHead`, `AgentHead`, `JobHead` and `NoSession` (settlement 8). Add the chunk to `checkLazyViews`. Vitest covers the initial workspace, the picks' reset on a workspace change, the enablement, the body sent and the pending heads; webshot renders the new screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/new/index.tsx`, `internal/web/ui/src/panels/new/NewSessionBody.tsx`, `internal/web/ui/src/panels/new/new.test.tsx`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/session/SessionHead.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/session/session.test.tsx`, `internal/web/ui/src/panels/agent/Agent.tsx`, `internal/web/ui/src/panels/agent/agent.test.tsx`, `internal/web/ui/src/panels/job/index.tsx`, `internal/web/ui/src/panels/job/job.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `new` panel, `NewHead`, the exported `FILTER_KEY`, and the pending heads; uses Story 2's `Select` parts, `Input` and part 2's `Textarea`, Story 3's `useLaunchDefaults`, Story 4's `session.create`, `launchBody`, `LaunchForm` and `useIsPending`, part 2's `utf8Length`, `MAX_MESSAGE_BYTES`, `Collapsible`, `CommandButton`'s `disabled`, part 1's `ContextRow`, `useLink`, `useFleet` and `definePanel`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens new,conversation,agent,job -out "$d"'`
  - **Review:** deep
  - **Form:** brief (a port of `newsession.mjs` over Story 4's command and Story 3's read; the pending heads read Story 4's slice)
  - **Reading:** spec §34, Part 3 settlements 7, 8; `internal/web/static/js/newsession.mjs`, `internal/web/static/app.css:610-641`, `internal/web/static/js/session.mjs:340-360`, `internal/web/ui/src/panels/fleet/index.tsx:1-60`, `internal/web/ui/src/session/SessionHead.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/panels/agent/Agent.tsx:40-80`, `internal/web/ui/src/panels/job/index.tsx`, `internal/web/ui/src/conversation/ReplyBox.tsx`, `internal/web/ui/src/api/gen.ts:103-116`, `internal/web/ui/src/api/gen.ts:331-344`, `internal/web/ui/src/api/gen.ts:576-581`
  - **Drafts after:** Story 2, Story 3, Story 4

- **Story 10** — Settings.

  Register the `settings` panel with `SettingsHead` and a lazy `SettingsBody`, and port `settings.mjs` and `push.mjs`'s control (spec Part 3 settlements 9, 10 and 12): Notifications (the push control by `usePushStatus` with its words and `push.enable`'s busy lines, the expired or failing line, two Switches over `device.update` showing the asked value while in flight, the pause row redrawn when the pause ends), Quick replies (the textarea over the stored list until typed in, Reset to defaults and Save through `quickReplies.save`, the typed text kept on a refusal), This phone (Paired as, Host, Build, `device.unpair`), App (`app.backToClassic`), and Recent notices; "Loading…" until the snapshot holds a device. Add the chunk to `checkLazyViews`. Vitest covers each push state's words, the toggles' in-flight and refused values, the pause row's expiry, the quick replies' draft precedence and the notices list; webshot renders the settings screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/settings/index.tsx`, `internal/web/ui/src/panels/settings/SettingsBody.tsx`, `internal/web/ui/src/panels/settings/settings.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `settings` panel and `SettingsHead`; uses Story 2's `Switch`, part 2's `Textarea`, Story 4's `device.update`, `quickReplies.save` and `device.unpair`, Story 5's `usePushStatus` and `push.enable`, part 1's `app.backToClassic`, `CommandButton`, `useCommandBusy`, `ContextRow`, `useFleet`, `ago` and `definePanel`, Story 1's fixture device and notices
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens settings -out "$d"'`
  - **Review:** deep
  - **Form:** brief (a port of `settings.mjs` over Stories 4 and 5's commands)
  - **Reading:** spec §23, §34, Part 3 settlements (Store, Push), 9, 10, 11, 12, 13; `internal/web/static/js/settings.mjs`, `internal/web/static/js/push.mjs:150-256`, `internal/web/static/app.css:610-641`, `internal/web/fleet.go:153-221`, `internal/web/ui/src/api/gen.ts:118-128`, `internal/web/ui/src/api/gen.ts:380-394`, `internal/web/ui/src/app/CommandButton.tsx`
  - **Drafts after:** Story 1, Story 2, Story 4, Story 5

- **Story 11** — Pairing and Add to Home.

  Register the `pair` panel (no head, in the entry) and port `pair.mjs` (spec Part 3 settlements 14 and 15): the wordmark, "Pair this phone with your loom." and its two steps, the code on Story 2's Input OTP (8 slots in two groups of four, `normalizeCode` upper-casing and dropping every character outside the pairing alphabet, typed or pasted), Pair through `device.pair` once 8 characters are in, no auto-submit, and the fleet opened when a live snapshot arrives. Add the Add to Home gate: `needsInstall` (iOS, not standalone, `loom.browser` not chosen) read once in `main.tsx`, which then renders only `AddToHome` with the classic words and starts nothing until "Continue in the browser anyway" stores the choice and starts the app as today. Vitest covers `normalizeCode` (an O, a 0, a paste with a dash), the enablement, the gate's cases and the continue; webshot renders the pair and add-to-home screens.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/pair/index.tsx`, `internal/web/ui/src/panels/pair/Pair.tsx`, `internal/web/ui/src/panels/pair/pair.test.tsx`, `internal/web/ui/src/app/install.tsx`, `internal/web/ui/src/app/install.test.tsx`, `internal/web/ui/src/main.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the `pair` panel, `PAIRING_ALPHABET`, `CODE_LENGTH`, `normalizeCode`, `BROWSER_KEY`, `needsInstall`, `AddToHome`; uses Story 2's `InputOTP` parts, Story 4's `device.pair`, part 1's `CommandButton`, `useLink`, `hrefFor`, `startFleet`, `registerWorker` and `definePanel`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens pair,add-to-home,fleet -out "$d"'`
  - **Review:** deep
  - **Form:** brief (a port of `pair.mjs` over Story 4's command; `normalizeCode`, which keeps a mistyped code from spending one of the daemon's 5 misses, and the gate starting nothing are named tests)
  - **Reading:** spec §3, §34, Part 3 settlements (Push), 14, 15; `internal/web/static/js/pair.mjs`, `internal/web/static/js/app.mjs:160-175`, `internal/web/static/js/app.mjs:380-412`, `internal/web/static/app.css:60-140`, `internal/web/auth.go:75-150`, `internal/web/ui/src/main.tsx`, `internal/web/ui/src/app/shell.tsx:160-245`
  - **Drafts after:** Story 2, Story 4

- **Story 12** — No route draws the placeholder.

  With every panel registered (spec Part 3 settlement 16, §7), remove `Placeholder` from the shell, draw "Back to classic" (`app.backToClassic`) under the splash on every route so the switch still works with the daemon down and nothing saved, and pin that every `PanelId` has a panel. Move the shell tests that used `flows`, `flowDoc` and `settings` as unregistered samples onto a panel id they register themselves, keeping what they pin. webshot renders every screen of the new app.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the splash's way back and the every-panel test; uses part 1's `CommandButton`, `panelFor` and the `PanelId` union, Stories 6 to 11's panels
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d"'`
  - **Review:** light (removing the placeholder and one test)
  - **Form:** brief (dead code removed and the splash's button)
  - **Reading:** spec §7, Part 3 settlements (Routes and the placeholder), 16; `internal/web/ui/src/app/shell.tsx:25-160`, `internal/web/ui/src/app/shell.tsx:160-245`, `internal/web/ui/src/app/shell.test.tsx:30-60`, `internal/web/ui/src/app/shell.test.tsx:255-280`, `internal/web/ui/src/app/panels.test.ts`
  - **Drafts after:** Story 6, Story 7, Story 8, Story 9, Story 10, Story 11

- **Story 13** — The rules and procedures for part 3.

  Update `internal/web/ui/CLAUDE.md` for what part 3 teaches (spec §35, Part 3 settlements 5, 6, 16): React's `style` prop may carry computed geometry, never a markup `style` attribute; the lazy-view rule names every chunk `checkLazyViews` pins; the registries no longer give a placeholder. Extend `docs/claude/web-layer.md` (a lazy view's stem in `checkLazyViews`, a command off a session route and an `offline` one, and the iPhone checklist as the procedure before cutover, settlement 18), naming them in `docs/claude/INDEX.md`'s `web-layer.md` row; describe the new app's part 3 screens in `internal/web/README.md` and drop its placeholder text, keeping `internal/web/CLAUDE.md` within its budget if any line there names the placeholder.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `internal/web/README.md`, `internal/web/CLAUDE.md`
  - **Interfaces:** none; uses every earlier story's names
  - **Verify:** `CGO_ENABLED=0 go test ./tools/claudemd/`
  - **Review:** light (docs, checked by the CLAUDE.md lint)
  - **Form:** brief (docs)
  - **Reading:** spec §35, §36, Testing (the iPhone checklist), Part 3 settlements (Docs), 5, 6, 14, 16, 18; `CLAUDE.md` (Conventions for CLAUDE.md files), `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `internal/web/README.md` (The new app and the switch, The new app's session views), `internal/web/CLAUDE.md`
  - **Drafts after:** Story 1, Story 2, Story 3, Story 4, Story 5, Story 6, Story 7, Story 8, Story 9, Story 10, Story 11, Story 12

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
