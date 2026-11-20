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
- `store/pending.ts` (new); `store/fleet.ts` (changed: every recent notice kept once).
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

- `components/ui/select.tsx` (Story 2): shadcn's Select on `@base-ui/react/select` (`SelectPrimitive`), exporting `Select` (`SelectPrimitive.Root` re-exported, so `value`, `onValueChange`, `items` and `disabled` pass through), `SelectTrigger` (the Trigger with its value and a chevron, `min-h-11`, `text-[16px]`), `SelectValue` (the Value, placeholder styled), `SelectContent` (Portal, Positioner, Popup and List in one, over the tokens), `SelectGroup` (Group), `SelectLabel` (GroupLabel) and `SelectItem` (Item with ItemText and an ItemIndicator check, `min-h-11`); each but `Select` takes `props: ComponentProps<typeof SelectPrimitive.<Part>>` for its part and returns `ReactElement`, merging its `className` through `cn` and passing every other prop (`value`, `id`, `aria-label`) through.
- `components/ui/switch.tsx` (Story 2): `function Switch(props: ComponentProps<typeof SwitchPrimitive.Root>): ReactElement`, shadcn's Switch on `@base-ui/react/switch` (`SwitchPrimitive`): the Root with its Thumb inside, a 44px hit area through an `after:` pseudo-element as `toggle-group.tsx` does, its `className` merged through `cn` and every other Root prop (`checked`, `onCheckedChange`, `disabled`, `id`, `aria-label`) passed through.
- `function Input(props: ComponentProps<"input">): ReactElement` (Story 2), in `components/ui/input.tsx`: shadcn's Input on a plain `<input>`.
- `components/ui/input-otp.tsx` (Story 2): the Input OTP on `@base-ui/react/otp-field` (`OTPField`), exporting `function InputOTP(props: OTPField.Root.Props): ReactElement` (a flex row over `OTPField.Root`, its `className` merged through `cn`, every other Root prop passed through: `length`, `validationType`, `normalizeValue`, `value`, `onValueChange`, `id`, `inputMode`, `autoComplete`, `aria-describedby`), `function InputOTPSlot(props: OTPField.Input.Props): ReactElement` (one `OTPField.Input`, 44px square, 16px text, no `index`: Base UI numbers slots by order; every prop, `aria-label` and `autoFocus` among them, passed through) and `function InputOTPSeparator(props: OTPField.Separator.Props): ReactElement`; each merges its `className` through `cn`; no `InputOTPGroup` (a caller groups slots in plain `div`s inside the root).
- `styles/app.css` (Story 2): `.base-ui-disable-scrollbar { scrollbar-width: none }` and `.base-ui-disable-scrollbar::-webkit-scrollbar { display: none }`.

**Reads (Story 3)**, in `api/reads.ts`:

- `const KEEP_FLOWS = 8` (Story 3).
- `interface FlowOptions { session: number, plan: string, key: string }` (Story 3): `key` is what `flowKey` answers for the session (below); once a board is held, a new `key` reads it again.
- `interface FlowView { flow: Flow | undefined, error: string, loading: boolean }` (Story 3).
- `function useFlow(options: FlowOptions): FlowView` (Story 3): reads on mount and on a new `key`, no poll; held per session and plan with `gcTime: Infinity`, the least recently opened board evicted past `KEEP_FLOWS`; a failed read keeps the held board and sets `error`; an empty `plan` reads nothing (`flow` undefined, `loading` false). Its query key is the session and plan only, never `key`: a new `key` refetches the held entry, so a re-priced run never adds a cache entry.
- `function flowKey(session: Session): string` (Story 3): `JSON.stringify(session.flows)`.
- `interface FlowDocOptions { session: number, plan: string, doc: string }` (Story 3).
- `interface FlowDocView { doc: FlowDoc | undefined, error: string, loading: boolean }` (Story 3).
- `function useFlowDoc(options: FlowDocOptions): FlowDocView` (Story 3): reads on mount and on a change of any option, no poll; an empty `plan` or `doc` reads nothing.
- `interface FlowsView { flows: Flows | undefined, error: string, loading: boolean, reload: () => void }` (Story 3).
- `function useFlows(): FlowsView` (Story 3): reads on mount and on `reload`, no poll; a failed read keeps the held answer and sets `error`. Every caller shares one query (`FlowsHead` and `FlowsBody` both call it): a second caller's mount sends no second read, and `reload` refetches it for every caller.
- `interface LaunchDefaultsView { defaults: LaunchDefaults | undefined, error: string, loading: boolean, retry: () => void }` (Story 3).
- `function useLaunchDefaults(workspace: number | undefined, live: boolean): LaunchDefaultsView` (Story 3): reads once per workspace once `live` is true and holds the answer for the page's lifetime (never read again on its own, not when the stream comes back); `retry` reads again after a failure; an undefined `workspace` reads nothing.

**The pending session, recent notices and the commands (Story 4):**

- `const PENDING_MS = 5000` (Story 4), in `store/pending.ts`.
- `interface PendingState { id: number | null, mark: (id: number) => void, clear: () => void }` (Story 4): `mark` replaces any earlier id and clears it after `PENDING_MS`; the id is also cleared once the snapshot lists that session, by a `useFleet` subscription that `mark` opens (never one at module top level) and `clear` closes.
- `const usePending: UseBoundStore<StoreApi<PendingState>>` (Story 4). `store/pending.ts` registers its own `onUnpaired` listener (`@/api/request`) when it loads, which calls `clear()`; it imports `useFleet` from `store/fleet.ts`, and `store/fleet.ts` never imports `store/pending.ts` (the cycle would leave `useFleet` in its temporal dead zone when the entry loads).
- `function useIsPending(id: number | undefined): boolean` (Story 4), in `store/pending.ts`.
- `const MAX_NOTICES = 20` (Story 4), in `store/fleet.ts`: the private `RECENT_NOTICES` renamed and exported. Every notice is kept exactly once: `applied`'s `notice` case no longer appends (`afterEvent` already hands each stream notice to the `onNotices` listeners), and `startFleet` registers one `onNotices` listener that appends every notice it delivers (a response's and each stream `notice`) to the held `snapshot.notices` through `FleetState.addNotices(notices: Notice[])`, keeping the last `MAX_NOTICES` (the spec's drafting settlements correct Part 3 settlement 11's premise).
- `interface LaunchPicks { account: string, model: string, permission_mode: string, effort: string }` (Story 4), in `commands/launch.ts`.
- `interface LaunchForm { workspace: number, title: string, prompt: string, picks: LaunchPicks }` (Story 4).
- `function launchBody(form: LaunchForm, defaults: LaunchDefaults): CreateBody` (Story 4): the title trimmed, the prompt's end trimmed, and each pick only where it differs from its default (spec Part 3 settlement 7).
- `session.create` (Story 4), in `commands/launch.ts`, title "Start session", args `{ body: CreateBody }`, `when` the panel is `new`, no sheet: a 201 marks the new id pending and opens `hrefFor("turns", { id: String(id) })` with `window.location.assign`; a 202 toasts "Still starting; the session will show in the fleet." and opens the fleet; any other outcome goes to `showOutcome`.
- `device.update` (Story 4), in `commands/device.ts`, title "Notification settings" (callers pass `label`), args `DeviceBody`, keyed by the names of the fields it sets (never their values), `when` the panel is `settings`, no sheet: a 200 applies the answered `Device` to the store as a `device` event only while `snapshot.device` is still the value held when the request was sent; any change since (a stream event, a new snapshot, or another write's answer) wins, and the stream `device` event that follows every write brings its value (spec drafting settlements).
- `quickReplies.save` (Story 4), in `commands/device.ts`, title "Save", args `{ replies: string[] | null, onSaved?: () => void }` (null resets to the defaults), no `key` (Reset and Save share one in-flight state), `when` the panel is `settings`, no sheet: a 200 applies the answer's `quick_replies` to the store as a `quick_replies` event only while `snapshot.quick_replies` is still the value held when the request was sent (any change since wins, and the stream event that follows brings the value), then calls `onSaved`; a refusal calls nothing.
- `device.unpair` (Story 4), in `commands/device.ts`, title "Unpair this phone", args `void`, `when` the panel is `settings`: a destructive sheet with the classic words (spec Part 3 settlement 13); a 204 calls `emitUnpaired()`.
- `device.pair` (Story 4), in `commands/device.ts`, title "Pair", args `{ code: string }`, `offline`, `when` the route is `pair`, no sheet: a 200 calls `startFleet()` and opens the fleet; a `failed` outcome with status 0 is "Can't reach loom. Check the connection and try again."; a `refused`, `stopping`, `timeout` or other `failed` outcome (a refusal, or the pair route's 500) is an error toast with the answer's text, and `pending` or `unpaired`, which carry none, toast the generic "That didn't work.".
- `commands/index.ts` (Story 4): imports `launch` and `device` for their registration; `internal/web/ui/CLAUDE.md`'s offline rule names `device.pair` beside `app.backToClassic` (Story 4).

**Push (Story 5):**

- `type PushStatus = "loading" | "unsupported" | "denied" | "on" | "again" | "off"` (Story 5), in `app/push.ts`: classic `pushView`'s states.
- `function usePushStatus(device: Device): PushStatus` (Story 5), in `app/push.ts`: reads the phone's own push state on mount, when `device.push` changes and when a `push.enable` run ends.
- `app/push.ts` (Story 5) also holds the push helpers moved from `app/worker.ts`, bodies unchanged, which `app/worker.ts` imports (`checkPushSubscription` stays there): `export function pushSupported(): boolean`, `export function activeRegistration(): Promise<ServiceWorkerRegistration | null>` (null once 5 s pass with no active worker; `READY_MS` moves with it, private) and `export function subscriptionBody(subscription: PushSubscription): SubscriptionBody | null`.
- `push.enable` (Story 5), in `commands/push.ts`, args `void`, no sheet, `when` the route is `settings`: spec Part 3 settlement 12, `Notification.requestPermission()` its first call; `commands/index.ts` imports it.

**The flow views (Stories 6 to 8):**

- `function planOf(session: Session, plan: string): string` (Story 6), in `panels/flow/parts.tsx`: `plan` when not empty, else the session's first (newest) flow's plan, else `""`.
- `function usd(value: number | null | undefined): string` (Story 6): classic `usd` (`$x.xx`, `$x.xxxx` above 0 and under 0.01, `""` for no number), delegating to `panels/fleet/SessionRow.tsx`'s `usd`, which Story 6 exports.
- `function median(values: number[]): number` (Story 6).
- `function Bars(props: { values: number[], current: number | null }): ReactElement` (Story 6): the per-story bars, oldest first, `current` an accent bar after them when not null, each height through the `style` prop (spec Part 3 settlement 5); it draws only the bars, and a caller draws its caption and draws nothing under 2 values.
- `function useClock(ms: number): number` (Story 6): the time now, re-rendering every `ms`.
- The `flow` panel (Story 6), in `panels/flow/index.tsx`: `{ id: "flow", tab: { order: 5, label: "flow", Mark: FlowMark }, Head: SessionHead }`, its `appliesTo` true while the session has flows, its `Body` `React.lazy` over `panels/flow/FlowBody.tsx`.
- `function FlowMark(props: { session: Session }): ReactElement | null` (Story 6): an amber dot while any of the session's flows asks; in `panels/flow/index.tsx`, which imports nothing from `parts.tsx` (that would pull the shared parts out of the lazy chunk into the entry).
- The `flowStory` and `flowDoc` panels (Story 7), registered in `panels/flow/index.tsx`: `{ id: "flowStory", Head: SessionHead }` and `{ id: "flowDoc", Head: SessionHead }`, their `Body`s `React.lazy` over `panels/flow/FlowStoryBody.tsx` and `panels/flow/FlowDocBody.tsx`.
- The `flows` panel (Story 8), in `panels/flows/index.tsx`: `{ id: "flows", Head: FlowsHead }`, its `Body` `React.lazy` over `panels/flows/FlowsBody.tsx`.
- `function FlowsHead(): ReactElement` (Story 8), taking no props as `FleetHead` does (an unused `props` fails the typed ESLint config): "Flows", back to the fleet, and ↻ calling `useFlows().reload`.
- `function flowTone(flow: FlowSummary): "docs" | "attention" | "error"` (Story 8): part 1's private helper in `panels/fleet/SessionRow.tsx`, exported, so the live box's chips take the fleet row's tones.

**New session, settings and pairing (Stories 9 to 11):**

- The `new` panel (Story 9), in `panels/new/index.tsx`: `{ id: "new", Head: NewHead }`, its `Body` `React.lazy` over `panels/new/NewSessionBody.tsx`.
- `function NewHead(): ReactElement` (Story 9), taking no props as `FleetHead` does: "New session", back to the fleet.
- `const FILTER_KEY = "loom.filter"` (Story 9): part 1's private constant in `panels/fleet/index.tsx`, exported.
- `SessionHead`, `AgentHead`, `JobHead` and `NoSession` (Story 9): "Loading…" while `useIsPending` holds the route's session (spec Part 3 settlement 8); `NoSession()` stays prop-less (it reads the route's session id itself), so Stories 6 and 7 draw `<NoSession />`.
- The `settings` panel (Story 10), in `panels/settings/index.tsx`: `{ id: "settings", Head: SettingsHead }`, its `Body` `React.lazy` over `panels/settings/SettingsBody.tsx`.
- `function SettingsHead(): ReactElement` (Story 10), taking no props as `FleetHead` does: "Settings", back to the fleet.
- The `pair` panel (Story 11), in `panels/pair/index.tsx`: `{ id: "pair" }` with no `Head`, its `Body` the entry's `Pair` from `panels/pair/Pair.tsx`.
- `const PAIRING_ALPHABET = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"`, `const CODE_LENGTH = 8` and `function normalizeCode(value: string): string` (Story 11), in `panels/pair/Pair.tsx`: upper-cased, every character outside the alphabet dropped, at most `CODE_LENGTH`; idempotent, as `OTPField`'s `normalizeValue` must be.
- `const BROWSER_KEY = "loom.browser"`, `function needsInstall(): boolean` and `function AddToHome(props: { onContinue: () => void }): ReactElement` (Story 11), in `app/install.tsx`; `main.tsx` registers the worker as today (spec §5, classic `app.mjs:110-118`), then reads `needsInstall()` once and, while it is true, renders only `AddToHome` and starts no stream and no route (`startFleet`, `watchHash`) until `onContinue` (Story 11).

**No placeholder (Story 12):**

- `app/shell.tsx` (Story 12): `Placeholder` removed; the splash drawn with no snapshot draws `<CommandButton id="app.backToClassic" />` on every route; `PanelHost` draws nothing for a panel with none registered; `app/panels.test.ts` pins that `panelFor` answers every `PanelId` once `@/panels` is imported.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): Story 6's `panels/flow/index.tsx` (Story 7); Story 4's `commands/index.ts` and `commands/index.test.ts` (Story 5); `panels/fleet/SessionRow.tsx` as Story 6 leaves it (Story 8); `panels/index.ts` as each panel story leaves it (the next one); `checkLazyViews`' list in `internal/web/static_budget_test.go` as each lazy story leaves it (the next one); Story 4's `internal/web/ui/CLAUDE.md` (Story 13). A brief story's draft quotes no current text. A full story that adds to `panels/index.ts` or `checkLazyViews`' list and table after brief stories (Story 10) anchors its addition on the list's end (the closing bracket, the last import line) and quotes nothing a brief story adds.

**Design stage:** /flow:draft session 006cd693-f5e9-4a09-8074-d10e643e548b, 2026-11-20T08:25:02Z to 2026-11-20T09:40:28Z UTC: $59.02 logged, $59.29 with estimated thinking; 7891 plan lines, $7.48 per 1,000 lines; skeleton review $2.39; plan review $15.05; budget $55; forms: full 4 stories $16.15, brief 9 stories $25.43; loom flow roster a71e5c0a4f9b.

**Plan review:** /flow:draft plan review; deep: 3, 4, 10; light: 1, 2, 8, 12, 13; full: 5, 6, 7, 9, 11

## Stories

- **Story 1** — webshot's fixtures carry the flow, device and notices part 3 draws, and two new screens show pairing and Add to Home.

  Add to the canned loom's fixtures what part 3's screens draw but no fixture holds (spec Part 3 settlement 17): a second flow on session 6, a final row, a running step with its wait, another run and an unpriced agent on the board, an expired push and a pause on the device, and two notices. Give webshot a `pair` screen shot unpaired (every API request answered 401 as `authed` answers it, none counted a miss) and an `add-to-home` screen shot as a browser tab, both in the one list both apps render, and add the typed fixture loaders Vitest needs. Every fixture still decodes into its Go type, the existing webshot tests keep passing, and the classic app renders every screen without a console error.

  - **Developer:** intermediate-dev
  - **Files:** `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/flow.json`, `tools/webshot/main.go`, `tools/webshot/canned.go`, `tools/webshot/webshot_test.go`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/src/fixtures.ts`, `internal/web/ui/src/fixtures.test.ts`
  - **Interfaces:** exposes the fixtures' second flow, final row, running step, other run, device and notices, the `pair` and `add-to-home` screens, `fixtureFlow`, `fixtureFlowDoc`, `fixtureFlows`, `fixtureLaunchDefaults`; uses part 1's `fillTimes` and `fixtureSnapshot`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app classic -screens flow,flow-story,flows,settings,pair,add-to-home -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -screens pair,add-to-home -out "$d"'`
  - **Review:** light (fixture data and a test tool's screen modes; the Go decode test and the typed fixture test check the shapes)
  - **Form:** brief (fixture data, their coverage rows and two webshot screen modes; Task 1.1's second canned loom on its own listener, chosen per screen, is a test tool's, kept brief by the owner, 2026-11-20)
  - **Reading:** spec Testing, §9, Part 2 settlements (webshot), Part 3 settlements (webshot), 17; `tools/webshot/main.go:40-100`, `tools/webshot/main.go:400-546`, `tools/webshot/canned.go:20-216`, `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/flow.json`, `internal/web/auth.go:1-75`, `internal/web/static/js/pair.mjs:1-56`, `internal/web/ui/src/api/stream.ts`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/src/fixtures.ts`, `internal/web/ui/src/fixtures.test.ts`, `tools/webshot/webshot_test.go:150-300`

- **Story 2** — Select, Switch, Input and Input OTP, and the scrollbar rules Base UI's Select needs.

  Add the shadcn components part 3's forms use (spec §32, Part 3 settlements (Components), 7, 14), each on Base UI 1.9 and owned in `components/ui/`: Select (the new-session pickers, with groups and labels), Switch (the notification toggles), Input (the title) and Input OTP on Base UI's `OTPField` (the pairing code, passing its normalisation through; no `input-otp` package). Every one keeps form text at 16px and touch targets at 44px and injects no `<style>` (the CSP provider stays), and `app.css` gains the two `.base-ui-disable-scrollbar` rules Base UI no longer injects. Vitest renders each, picks a Select item, toggles a Switch and fills the OTP.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/components/ui/select.tsx`, `internal/web/ui/src/components/ui/switch.tsx`, `internal/web/ui/src/components/ui/input.tsx`, `internal/web/ui/src/components/ui/input-otp.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/styles/app.css`, `internal/web/dist/`
  - **Interfaces:** exposes `Select`, `SelectTrigger`, `SelectValue`, `SelectContent`, `SelectGroup`, `SelectLabel`, `SelectItem`, `Switch`, `Input`, `InputOTP`, `InputOTPSlot`, `InputOTPSeparator`; uses part 1's `cn` and tokens
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** light (owned copies of shadcn components on Base UI; the CSP and the phone sizes are named tests)
  - **Form:** brief (shadcn components and two CSS rules)
  - **Reading:** spec §32, §33, §34, Part 2 settlements (Components), Part 3 settlements (Components); `internal/web/ui/components.json`, `internal/web/ui/src/components/ui/textarea.tsx`, `internal/web/ui/src/components/ui/toggle-group.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/styles/app.css`, and, once `pnpm install` has run, Base UI's `@base-ui/react/docs/react/components/otp-field.md` and `@base-ui/react/utils/styles.mjs` in the app's installed node modules

- **Story 3** — The reads for the flow views, the host's flows and launch defaults.

  Add to `src/api/reads.ts` the part 3 reads with their classic triggers (spec §26, Part 3 settlements 2 and 7): the board, read on open and whenever the session's flow summaries change, held per session and plan for the last 8; a flow document, read on open and on a change of session, plan or document; the host's flows, read on open and on reload; launch defaults, read once per workspace once the stream is live and held for the page's lifetime, with a retry. None polls. Reuse part 2's query helpers (the eviction, `unwrap`, the error text). Vitest covers each trigger, the eviction past 8, the held answer kept on a failed read, an empty plan or doc reading nothing, and launch defaults not read again when the stream returns.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/api/reads.ts`, `internal/web/ui/src/api/reads.test.ts`, `internal/web/dist/`
  - **Interfaces:** exposes `KEEP_FLOWS`, `FlowOptions`, `FlowView`, `useFlow`, `flowKey`, `FlowDocOptions`, `FlowDocView`, `useFlowDoc`, `FlowsView`, `useFlows`, `LaunchDefaultsView`, `useLaunchDefaults`; uses part 1's `api`, `queryClient`, `unwrap` and `OutcomeError`, part 2's `keepRecent`, and Story 1's fixture loaders
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (a cache bound and retention: boards held past their observers and evicted beyond `KEEP_FLOWS`, the held board kept on a failed read, launch defaults held for the page's lifetime; the owner, 2026-11-20)
  - **Reading:** spec §26, Part 3 settlements (Store), 2, 7; `internal/web/ui/src/api/reads.ts`, `internal/web/ui/src/api/reads.test.ts:1-120`, `internal/web/ui/src/api/queries.ts`, `internal/web/static/js/flow.mjs:80-110`, `internal/web/static/js/flowdoc.mjs:57-93`, `internal/web/static/js/flows.mjs`, `internal/web/static/js/newsession.mjs:40-100`, `internal/web/flow.go:320-393`, `internal/web/ui/src/api/gen.ts:145-280`, `internal/web/ui/src/api/gen.ts:331-344`
  - **Drafts after:** Story 1

- **Story 4** — A session just created is pending, the store keeps the recent notices, and the create, device, quick-replies, unpair and pair commands.

  Add the pending slice (an id marked by a 201 for 5 s, cleared once the snapshot lists it or on unpair; spec Part 3 settlement 8), keep the recent notices of responses and the stream in the held snapshot (the last 20, settlement 11), and register the remaining writes as commands with the classic words (§23 as settled): `session.create` with `launchBody` (only the picks that differ from the defaults; 201 opens the session, 202 the fleet), `device.update` and `quickReplies.save` (each answer applied to the store as its stream event, settlement 10), `device.unpair` (its sheet; a 204 runs the unpaired path), and `device.pair` (offline; a 200 starts the fleet again and opens it; settlement 14). Amend `internal/web/ui/CLAUDE.md`'s offline rule to name `device.pair`. Vitest covers the bodies each sends, each outcome's toast and navigation, the answers applied to the store, the pending timer and its clearing, the notices' bound, and `device.pair` running while stale where the others don't.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/store/pending.ts`, `internal/web/ui/src/store/pending.test.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/store/startFleet.test.ts`, `internal/web/ui/src/commands/launch.ts`, `internal/web/ui/src/commands/launch.test.ts`, `internal/web/ui/src/commands/device.ts`, `internal/web/ui/src/commands/device.test.ts`, `internal/web/ui/src/commands/index.ts`, `internal/web/ui/src/commands/index.test.ts`, `internal/web/ui/src/app/commands.ts`, `internal/web/ui/CLAUDE.md`, `internal/web/dist/`
  - **Interfaces:** exposes `PENDING_MS`, `PendingState`, `usePending`, `useIsPending`, `MAX_NOTICES`, `LaunchPicks`, `LaunchForm`, `launchBody`, and the commands `session.create`, `device.update`, `quickReplies.save`, `device.unpair`, `device.pair`; uses part 1's command path, `api`, `showOutcome`, `showMessage`, `hrefFor`, `onNotices`, `emitUnpaired`, `startFleet` and `useFleet`, part 2's `commands/testkit.ts`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
  - **Review:** deep
  - **Form:** full (the bodies sent to the daemon, the unpaired path a revoke runs, an offline command that bypasses the stale gate, store writes from answers and the bounded notices)
  - **Reading:** spec §20, §23, Part 3 settlements (Commands, Store), 7, 8, 10, 11, 13, 14; `internal/web/static/js/app.mjs:216-231`, `internal/web/static/js/app.mjs:315-358`, `internal/web/static/js/newsession.mjs:1-31`, `internal/web/static/js/newsession.mjs:105-123`, `internal/web/static/js/settings.mjs:100-135`, `internal/web/static/js/settings.mjs:186-195`, `internal/web/static/js/pair.mjs:58-80`, `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/commands/session.ts`, `internal/web/ui/src/commands/testkit.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/api/request.ts:1-80`, `internal/web/ui/src/app/toasts.ts`, `internal/web/api.go:213-263`, `internal/web/api.go:365-501`, `internal/web/auth.go:75-149`, `internal/web/ui/src/api/gen.ts:103-128`, `internal/web/ui/src/api/gen.ts:620-687`, `internal/web/ui/CLAUDE.md` (the offline rule)

- **Story 5** — Turning push on from the phone.

  Port `push.mjs`'s enable flow as the command `push.enable` (spec Part 3 settlement 12): the permission asked first, inside the tap; an active worker within 5 s; the key read; the old subscription dropped and a new one made with the key; the subscription posted, and the phone's dropped again if that fails; each failure an error toast with the classic words, success "Notifications are on.". Move `app/worker.ts`'s push helpers into `app/push.ts`, beside `usePushStatus`, the control's classic states read from the phone and the device, re-read when the device's push state changes and when a turn-on ends. Vitest with a stubbed `Notification`, `PushManager` and worker covers the order of the calls (the permission before any await), each failure's words and clean-up, and each state.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/push.ts`, `internal/web/ui/src/app/push.test.ts`, `internal/web/ui/src/app/worker.ts`, `internal/web/ui/src/commands/push.ts`, `internal/web/ui/src/commands/push.test.ts`, `internal/web/ui/src/commands/index.ts`, `internal/web/ui/src/commands/index.test.ts`, `internal/web/dist/`
  - **Interfaces:** exposes `PushStatus`, `usePushStatus`, the moved `pushSupported`, `activeRegistration` and `subscriptionBody`, and the command `push.enable`; uses part 1's command path (`defineCommand`), `api`, `showMessage`, `showOutcome` and `checkPushSubscription`, part 2's `commands/testkit.ts`, and Story 4's `commands/index.ts`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (outside calls in an order iOS enforces: the permission inside the gesture, a subscription made and dropped again on a failed post)
  - **Reading:** spec §28, Part 3 settlements (Push, Commands), 12; `internal/web/static/js/push.mjs`, `internal/web/ui/src/app/worker.ts`, `internal/web/ui/src/app/worker.test.ts`, `internal/web/ui/src/app/commands.ts:150-181`, `internal/web/push.go:360-384`, `internal/web/api.go:441-493`, `internal/web/ui/src/api/gen.ts:428-430`, `internal/web/ui/src/api/gen.ts:510-519`
  - **Drafts after:** Story 4

- **Story 6** — The flow board and the flow tab.

  Register the `flow` panel (order 5, its asks dot, shown while the session has flows; spec Part 3 settlement 4) with its lazy `FlowBody`, and port the classic board (`flow.mjs`'s `Flow`) over Story 3's `useFlow`: the gone, no-flow and loading states, the picker for 2 or more flows, the title and part, the error and unreadable lines, the stage strip with its document links, the lost and phase lines, the question box, the stories with their cells and right-hand text, the cost box (role bar, legend, per-story comparison, unpriced line, the cost error) and the other runs, each unknown value in neutral styling (settlements 1, 3 and 5). Add the shared parts the story view and the host's flows reuse, and the chunk to `checkLazyViews`. Vitest covers each state, the cells' words, the cost box's forms and the tab; webshot renders the flow screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/flow/index.tsx`, `internal/web/ui/src/panels/flow/parts.tsx`, `internal/web/ui/src/panels/flow/FlowBody.tsx`, `internal/web/ui/src/panels/flow/flow.test.tsx`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `flow` panel, `FlowMark`, `planOf`, `usd`, `median`, `Bars`, `useClock`, and `SessionRow.tsx`'s `usd` exported; uses Story 3's `useFlow` and `flowKey`, Story 1's `fixtureFlow`, part 2's `SessionHead`, `NoSession` and `elapsed`, part 1's `definePanel`, `useSession`, `hrefFor` and `Badge`
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
  - **Files:** `internal/web/ui/src/panels/flows/index.tsx`, `internal/web/ui/src/panels/flows/FlowsBody.tsx`, `internal/web/ui/src/panels/flows/flows.test.tsx`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `flows` panel, `FlowsHead` and the exported `flowTone`; uses Story 3's `useFlows`, Story 6's `usd`, `median` and `Bars`, Story 1's `fixtureFlows`, part 1's `flowChip`, `ago`, `ContextRow`, `definePanel` and `hrefFor`
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
  - **Form:** brief (a port of `newsession.mjs` over Story 4's command and Story 3's read; the 64 KiB bound reuses part 2's `utf8Length` and `MAX_MESSAGE_BYTES` on the end-trimmed prompt, and the pending heads read Story 4's `useIsPending`, each a named test; the owner kept it brief, 2026-11-20)
  - **Reading:** spec §34, Part 3 settlements 7, 8; `internal/web/static/js/newsession.mjs`, `internal/web/static/app.css:610-641`, `internal/web/static/js/session.mjs:340-360`, `internal/web/ui/src/panels/fleet/index.tsx:1-60`, `internal/web/ui/src/session/SessionHead.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/panels/agent/Agent.tsx:40-80`, `internal/web/ui/src/panels/job/index.tsx`, `internal/web/ui/src/conversation/ReplyBox.tsx`, `internal/web/ui/src/api/gen.ts:103-116`, `internal/web/ui/src/api/gen.ts:331-344`, `internal/web/ui/src/api/gen.ts:576-581`
  - **Drafts after:** Story 2, Story 3, Story 4

- **Story 10** — Settings.

  Register the `settings` panel with `SettingsHead` and a lazy `SettingsBody`, and port `settings.mjs` and `push.mjs`'s control (spec Part 3 settlements 9, 10 and 12): Notifications (the push control by `usePushStatus` with its words and `push.enable`'s busy lines, the expired or failing line, two Switches over `device.update` showing the asked value while in flight, the pause row redrawn when the pause ends), Quick replies (the textarea over the stored list until typed in, Reset to defaults and Save through `quickReplies.save`, the typed text kept on a refusal), This phone (Paired as, Host, Build, `device.unpair`), App (`app.backToClassic`), and Recent notices; "Loading…" until the snapshot holds a device. Add the chunk to `checkLazyViews`. Vitest covers each push state's words, the toggles' in-flight and refused values, the pause row's expiry, the quick replies' draft precedence and the notices list; webshot renders the settings screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/settings/index.tsx`, `internal/web/ui/src/panels/settings/SettingsBody.tsx`, `internal/web/ui/src/panels/settings/settings.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `settings` panel and `SettingsHead`; uses Story 2's `Switch`, part 2's `Textarea`, Story 4's `device.update`, `quickReplies.save` (its `onSaved`) and `device.unpair`, Story 5's `usePushStatus` and `push.enable`, part 1's `app.backToClassic`, `CommandButton`, `useCommandBusy`, `commandById`, `runCommand`, `commandEnabled`, `useCommandContext`, `ContextRow`, `useFleet`, `ago` and `definePanel`, Story 1's fixture device and notices
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens settings -out "$d"'`
  - **Review:** deep
  - **Form:** full (write concurrency and failure states: a Switch showing the asked value while its write is in flight and the stored one after, a refusal snapping it back, the quick-replies draft outliving a refusal, the pause row's expiry timer; the owner, 2026-11-20)
  - **Reading:** spec §23, §34, Part 3 settlements (Store, Push), 9, 10, 11, 12, 13; `internal/web/static/js/settings.mjs`, `internal/web/static/js/push.mjs:150-256`, `internal/web/static/app.css:610-641`, `internal/web/fleet.go:153-221`, `internal/web/ui/src/api/gen.ts:118-128`, `internal/web/ui/src/api/gen.ts:380-394`, `internal/web/ui/src/app/CommandButton.tsx`
  - **Drafts after:** Story 1, Story 2, Story 4, Story 5

- **Story 11** — Pairing and Add to Home.

  Register the `pair` panel (no head, in the entry) and port `pair.mjs` (spec Part 3 settlements 14 and 15): the wordmark, "Pair this phone with your loom." and its two steps, the code on Story 2's Input OTP (8 slots in two groups of four, `normalizeCode` upper-casing and dropping every character outside the pairing alphabet, typed or pasted), Pair through `device.pair` once 8 characters are in, no auto-submit, and the fleet opened when a live snapshot arrives. Add the Add to Home gate: `needsInstall` (iOS, not standalone, `loom.browser` not chosen) read once in `main.tsx`, which then renders only `AddToHome` with the classic words and starts nothing until "Continue in the browser anyway" stores the choice and starts the app as today. Vitest covers `normalizeCode` (an O, a 0, a paste with a dash), the enablement, the gate's cases and the continue; webshot renders the pair and add-to-home screens.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/pair/index.tsx`, `internal/web/ui/src/panels/pair/Pair.tsx`, `internal/web/ui/src/panels/pair/pair.test.tsx`, `internal/web/ui/src/app/install.tsx`, `internal/web/ui/src/app/install.test.tsx`, `internal/web/ui/src/main.tsx`, `internal/web/ui/src/main.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the `pair` panel, `PAIRING_ALPHABET`, `CODE_LENGTH`, `normalizeCode`, `BROWSER_KEY`, `needsInstall`, `AddToHome`; uses Story 2's `InputOTP` parts, Story 4's `device.pair`, part 1's `CommandButton`, `useLink`, `hrefFor`, `startFleet`, `registerWorker` and `definePanel`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens pair,add-to-home,fleet -out "$d"'`
  - **Review:** deep
  - **Form:** brief (a port of `pair.mjs` over Story 4's command; `normalizeCode`, which keeps a mistyped code from spending one of the daemon's 5 misses, its idempotence, and the gate starting nothing are named tests; Task 11.2's redirect on a live snapshot beside `device.pair`'s own navigation and Task 11.4's launch order in `main.tsx` are named tests too; the owner kept it brief, 2026-11-20)
  - **Reading:** spec §3, §34, Part 3 settlements (Push), 14, 15; `internal/web/static/js/pair.mjs`, `internal/web/static/js/app.mjs:160-175`, `internal/web/static/js/app.mjs:380-412`, `internal/web/static/app.css:60-140`, `internal/web/auth.go:75-149`, `internal/web/ui/src/main.tsx`, `internal/web/ui/src/app/shell.tsx:160-245`
  - **Drafts after:** Story 2, Story 4

- **Story 12** — No route draws the placeholder.

  With every panel registered (spec Part 3 settlement 16, §7), remove `Placeholder` from the shell, draw "Back to classic" (`app.backToClassic`) under the splash on every route so the switch still works with the daemon down and nothing saved, and pin that every `PanelId` has a panel. Move the shell tests that used `flows`, `flowDoc` and `settings` as unregistered samples onto a panel id they register themselves, keeping what they pin, and reword the comments that name the placeholder (`app/panels.ts`, `panels/index.ts`, `app/commands.ts`). webshot renders every screen of the new app.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/panels/index.ts`, `internal/web/ui/src/app/commands.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the splash's way back and the every-panel test; uses part 1's `CommandButton`, `panelFor` and the `PanelId` union, Stories 6 to 11's panels
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d"'`
  - **Review:** light (removing the placeholder and one test)
  - **Form:** brief (dead code removed and the splash's button)
  - **Reading:** spec §7, Part 3 settlements (Routes and the placeholder), 16; `internal/web/ui/src/app/shell.tsx:25-160`, `internal/web/ui/src/app/shell.tsx:160-245`, `internal/web/ui/src/app/shell.test.tsx:30-60`, `internal/web/ui/src/app/shell.test.tsx:255-280`, `internal/web/ui/src/app/panels.test.ts`
  - **Drafts after:** Story 6, Story 7, Story 8, Story 9, Story 10, Story 11

- **Story 13** — The rules and procedures for part 3.

  Update `internal/web/ui/CLAUDE.md` for what part 3 teaches (spec §35, Part 3 settlements 5, 6, 16): React's `style` prop may carry computed geometry, never a markup `style` attribute; the lazy-view rule names every chunk `checkLazyViews` pins; the registries no longer give a placeholder. Extend `docs/claude/web-layer.md` (a lazy view's stem in `checkLazyViews`, a command off a session route and an `offline` one, and the iPhone checklist as the procedure before cutover, settlement 18), naming them in `docs/claude/INDEX.md`'s `web-layer.md` row; reword its line on webshot's screens (each an iPhone 15 Home Screen app over the canned loom), which the `pair` and `add-to-home` screens make false; describe the new app's part 3 screens in `internal/web/README.md` and drop its placeholder text.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `internal/web/README.md`
  - **Interfaces:** none; uses every earlier story's names
  - **Verify:** `CGO_ENABLED=0 go test ./tools/claudemd/`
  - **Review:** light (docs, checked by the CLAUDE.md lint)
  - **Form:** brief (docs)
  - **Reading:** spec §35, §36, Testing (the iPhone checklist), Part 3 settlements (Docs), 5, 6, 14, 16, 18; `CLAUDE.md` (Conventions for CLAUDE.md files), `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `internal/web/README.md` (The new app and the switch, The new app's session views), `internal/web/CLAUDE.md`
  - **Drafts after:** Story 1, Story 2, Story 3, Story 4, Story 5, Story 6, Story 7, Story 8, Story 9, Story 10, Story 11, Story 12

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->

## Story 1: webshot's fixtures carry the flow, device and notices part 3 draws, and two new screens show pairing and Add to Home

- **Developer:** intermediate-dev
- **Files:** `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/flow.json`, `tools/webshot/main.go`, `tools/webshot/canned.go`, `tools/webshot/webshot_test.go`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/src/fixtures.ts`, `internal/web/ui/src/fixtures.test.ts`
- **Interfaces:** exposes the fixtures' second flow, final row, running step, other run, device and notices, the `pair` and `add-to-home` screens, `fixtureFlow`, `fixtureFlowDoc`, `fixtureFlows`, `fixtureLaunchDefaults`; uses part 1's `fillTimes` and `fixtureSnapshot`
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app classic -screens flow,flow-story,flows,settings,pair,add-to-home -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -screens pair,add-to-home -out "$d"'`
- **Review:** light (fixture data and a test tool's screen modes; the Go decode test and the typed fixture test check the shapes)
- **Form:** brief (fixture data, their coverage rows and two webshot screen modes; Task 1.1's second canned loom on its own listener, chosen per screen, is a test tool's, kept brief by the owner, 2026-11-20)

Part 3's screens draw things the canned loom cannot yet show (spec Part 3 settlement 17), and two of its screens, pairing and Add to Home, need a canned loom and a tab of a kind webshot has not had. This story adds the data (session 6's second flow, the board's final row, a running step with its wait, another run and an unpriced agent, the device's expired push and pause, two notices), gives webshot a `pair` screen (every `/api/` request answered 401 as `authed` answers an unpaired device, none counted a miss) and an `add-to-home` screen (an iPhone browser tab, `navigator.standalone` false), and adds the typed loaders Vitest needs (`fixtureFlow`, `fixtureFlowDoc`, `fixtureFlows`, `fixtureLaunchDefaults`). Every existing fixture value an existing test reads stays as it is, and the classic app renders every screen without a console error. The story is brief: every task names the behaviour, the names and the tests, and the developer writes the code. None of it holds a landmine marker.

Three traps the tasks below route around:

- `noticeJSON.At` in `internal/web/fleet.go` is a `time.Time`, so `internal/web/webshot_fixture_test.go`'s `readFixture` (which decodes the raw file, relative times and all) cannot decode a notice whose `at` is `"now-12m"`. Task 1.2 makes `readFixture` fill relative times before it decodes; the fixture keeps its relative `at` so the ages read the same on every run.
- `device.push` `"expired"` is the one bad state the launch-time check (`checkPushSubscription`, `internal/web/ui/src/app/worker.ts`) leaves alone ("`expired` or `failing` is left for the Settings button"), so no fixture state makes the new app write to the canned loom on load; `"ok"` or `""` would, and every write is a miss that fails the run.
- The status-bar page `run` opens first and each screen's own tab share `openPage`; only a screen's tab may be a browser tab.

### Task 1.1: The `pair` and `add-to-home` screens

**Files:**
- Modify: `tools/webshot/main.go:48-76` (the screens' comment and list), `tools/webshot/main.go:154-182` (`run`'s two `openPage` calls), `tools/webshot/main.go:408-433` (`openPage`), `tools/webshot/main.go:453-476` (`emulation`)
- Modify: `tools/webshot/canned.go:44-58` (`cannedLoom`), `tools/webshot/canned.go:70-95` (`apiHandler`, `ServeHTTP`)
- Test: `tools/webshot/webshot_test.go`

Behaviour and names (all private to `tools/webshot`; the contract gives only the two screens):

- `screens` gains two rows after `{"settings", "#settings"}`: `{"pair", "#pair"}` and `{"add-to-home", ""}`. The `screen` struct stays `{name, hash string}`, so every unkeyed `screen{...}` literal in `webshot_test.go` (`TestPickScreens`, `TestPickScreens_TheAgentScreenIsSessionOnesRunningSubagent`) still compiles. The comment above `screens` gains a sentence: `pair` is shot with every `/api/` request answered 401 (an unpaired device), `add-to-home` as an iPhone browser tab (not standalone).
- `type screenMode struct{ unpaired, browserTab bool }` and `func modeOf(sc screen) screenMode`, which looks the screen's name up in a package-level `map[string]screenMode`: `pair` is `{unpaired: true}`, `add-to-home` is `{browserTab: true}`, every other name is the zero value.
- `emulation(d device) []cdpStep` becomes `emulation(d device, standalone bool) []cdpStep`, and `openPage(ctx, d)` becomes `openPage(ctx, d, standalone bool)`. The last step of `emulation` (`Page.addScriptToEvaluateOnNewDocument`, whose source is `Object.defineProperty(Navigator.prototype, "standalone", {get: () => true, configurable: true});`) writes `standalone`'s value (`true` or `false`) in place of that `true`; every other step is unchanged, the iPhone user agent included, since `needsInstall` reads it. `run` passes `true` for the status-bar page it opens first (`pg`) and `!modeOf(sc).browserTab` for each screen's own tab. The `openPage` doc comment says a tab is the Home Screen app unless told otherwise.
- `cannedLoom` gains `unpaired bool`, set once after `newCannedLoom` returns and before the loom serves, never changed while it serves (so it needs no lock). When it is true, both `ServeHTTP` and `apiHandler` answer every path under `/api/`, the stream (`/api/v1/stream`) included, with status 401, `Content-Type: application/json` and the body `{"error":"this phone is not paired"}` (what `authed` writes, `internal/web/auth.go`), and record no miss and call no `onMiss`; every other path (the app's files) is served as before. The text lives in one constant, `unpairedText`.
- `run` serves a second canned loom (the same `dir`, `unpaired` true) on a listener of its own, closed when `run` returns, and shoots a screen whose `modeOf` is `unpaired` against that loom's origin and its misses; every other screen uses the first loom as today. `shoot` already takes the loom and the origin, so it is unchanged.

- [ ] **Step 1: Write the failing tests** in `tools/webshot/webshot_test.go`, beside the tests of the same kind (`TestPickScreens`, `cannedForTest`, `get`):

  - `TestScreens_PairIsShotUnpairedAndAddToHomeAsABrowserTab`: `pickScreens("pair,add-to-home")` returns `[]screen{{"pair", "#pair"}, {"add-to-home", ""}}`; `modeOf` of the first is `screenMode{unpaired: true}` and of the second `screenMode{browserTab: true}`; `modeOf` of every other screen in `screens` is the zero `screenMode`; the names in `screens` are all different. 
    Fails without: `TestScreens_PairIsShotUnpairedAndAddToHomeAsABrowserTab`: the `pair` row or its `unpaired` entry in the mode map removed (the pick errors or the mode is zero), a mode set on a third screen (the zero check names it), or a repeated name.
  - `TestCanned_AnUnpairedLoomAnswersEveryAPIRequest401AsAuthedDoes`: on `cannedForTest`, first `GET /api/v1/sessions/1/turns` is 200 (the paired loom is as it was); then with `c.unpaired = true`, each of `GET /api/v1/stream`, `GET /api/v1/sessions/1/turns`, `GET /api/v1/push-key` (a route with no fixture) and `POST /api/v1/pair` (`http.Post` with an empty JSON body) answers 401, with `Content-Type` `application/json`, and a body that decodes to `map[string]string{"error": "this phone is not paired"}`; `c.takeMisses()` is empty after all four; `GET /` is still 200 and holds the build.
    Fails without: `TestCanned_AnUnpairedLoomAnswersEveryAPIRequest401AsAuthedDoes`: the unpaired branch removed from `ServeHTTP` (turns answers 200), from only the stream's case (the stream answers `text/event-stream`), or a refusal that records a miss (`takeMisses` holds the push-key and pair routes).
  - `TestEmulation_ABrowserTabIsNotStandalone`: `emulation(devices[defaultDevice], true)` and `emulation(devices[defaultDevice], false)` have the same length and the same methods in the same order; the `Page.addScriptToEvaluateOnNewDocument` step's `source` param contains `get: () => true` for `true` and `get: () => false` for `false`, and neither contains the other; the `Emulation.setUserAgentOverride` step's `userAgent` is the device's in both.
    Fails without: `TestEmulation_ABrowserTabIsNotStandalone`: `emulation` ignoring its argument (the `false` source still says `() => true`), or dropping the user-agent step for a browser tab (the tab would not read as iOS, so `needsInstall` is false).

- [ ] **Step 2: Run them to see them fail**

Run the three tests:

```bash
CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestScreens_PairIsShot|TestCanned_AnUnpairedLoom|TestEmulation_ABrowserTab'
```

Expected: `FAIL github.com/aidan-bailey/loom/tools/webshot [build failed]`, naming `undefined: modeOf`, `c.unpaired undefined` and `too many arguments in call to emulation`.

- [ ] **Step 3: Implement** the behaviour above in `tools/webshot/main.go` and `tools/webshot/canned.go`. Run `gofmt -w tools/webshot/main.go tools/webshot/canned.go tools/webshot/webshot_test.go` (these files only; never `gofmt -w .`).

- [ ] **Step 4: Run them to see them pass**, with the whole package, since `TestPickScreens` and the canned tests share the list:

```bash
CGO_ENABLED=0 go test ./tools/webshot/
```

Expected: `ok  github.com/aidan-bailey/loom/tools/webshot`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(webshot): a pair screen shot unpaired and an add-to-home screen shot as a browser tab" -- tools/webshot/main.go tools/webshot/canned.go tools/webshot/webshot_test.go
```

### Task 1.2: Session 6's second flow, the device's expired push and pause, two notices

**Files:**
- Modify: `tools/webshot/fixture/snapshot.json:457-478` (session 6's `flows`), `tools/webshot/fixture/snapshot.json:480-490` (`device` and `notices`)
- Modify: `internal/web/webshot_fixture_test.go:14-21` (`readFixture`)
- Test: `tools/webshot/webshot_test.go`, `internal/web/webshot_fixture_test.go`

Data (contract: `tools/webshot/fixture/snapshot.json` (Story 1)). Every other session and field stays.

- Session 6's `flows` array keeps its first summary exactly as it is and gains a second one after it. The second is another plan of the same session, older and not yet drafted: `plan` `docs/superpowers/plans/2026-11-16-flow-plan-treatment-part-2-estimate.md`, `spec` `docs/superpowers/specs/2026-11-16-flow-plan-treatment-design.md`, `title` `Plan treatment, part 2: the planner's estimate`, `stage` `spec`, `status` `designed`, `run` `""`, `done` 0, `total` 0, `drafted` 0, `asks` `""`, `phase` `""`, `updated` `now-72h`, `unreadable` `""`, `spent` `null`, `part` `""`.
- `device.push` changes from `""` to `"expired"`, and `device.notify.paused_until` from `""` to `"now+40m"` (`"waiting": true` and `"finished": true` stay). `"expired"` is the state the launch-time push check leaves alone, so no new write is asked of the canned loom on load.
- `notices` changes from `[]` to two entries, oldest first as the stream appends them: `{"text": "Merged flow-run into main.", "error": false, "at": "now-12m"}` and `{"text": "Could not push fix-pane-cursor: the remote rejected it.", "error": true, "at": "now-3m"}`.

`readFixture` (the one helper every fixture decode in `internal/web/webshot_fixture_test.go` goes through) fills every JSON string that is exactly `now` or `now` followed by `+` or `-` and a Go duration (`"now-12m"`, `"now+1h40m"`, matched whole, so `"nowhere"` stays) with one fixed RFC 3339 time, `2026-11-03T10:00:00Z`, before it decodes, so a `time.Time` field (a notice's `at`) decodes from a fixture that keeps its relative form. A string field decodes as before.

- [ ] **Step 1: Write the failing tests**

  In `tools/webshot/webshot_test.go`, after `TestFixture_SessionOneHoldsAWorktreeATmuxSessionAndAQueuedJob`, in its style (a `cannedLoom{now: ...}` with a fixed `now`, `c.fixture("snapshot.json")`, a local struct holding only the fields read):

  - `TestFixture_SessionSixHoldsASecondOlderFlowSoThePickerShows`: session 6 has two flows; the first's `plan` is `docs/superpowers/plans/2026-11-09-flow-visualisation.md` with `status` `parked`; the second has `stage` `spec`, `status` `designed`, a `plan` different from the first's, and an `updated` (parsed as RFC 3339) before the first's; every other session has no flow.
    Fails without: `TestFixture_SessionSixHoldsASecondOlderFlowSoThePickerShows`: the second summary removed, put before the first (`[0].plan` differs), given the first's plan or a status other than `designed`, or made newer than the first.
  - `TestFixture_TheDeviceIsExpiredAndPausedAndTwoNoticesOneAnError`: with `now` fixed, `device.push` is `expired`; `device.notify.paused_until` parses and is exactly 40 minutes after `now`; `notices` has two entries, in order, with `error` false then true, non-empty `text`, and an `at` that parses and is before `now`.
    Fails without: `TestFixture_TheDeviceIsExpiredAndPausedAndTwoNoticesOneAnError`: push left `""` or `ok`, the pause left `""` or in the past, a missing or second error notice, or a notice whose `at` is not relative (it would parse but not be before `now` after a fill).

  In `internal/web/webshot_fixture_test.go`:

  - `TestWebshotFixture_TheSnapshotsNoticesDecodeWithTheirTimes`: `readFixture(t, "snapshot.json", &snap)` into `snapshotJSON` leaves two `Notices`, each with a non-zero `At`, exactly one with `Error` true; `snap.Device.Push` is `expired` and `snap.Device.Notify.PausedUntil` is not empty.
    Fails without: `TestWebshotFixture_TheSnapshotsNoticesDecodeWithTheirTimes`: the time fill removed from `readFixture` (the decode errors on `parsing time "now-12m"`), or either notice's `error` flag changed.

- [ ] **Step 2: Run them to see them fail**

Run the four tests:

```bash
CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ -run 'TestFixture_SessionSix|TestFixture_TheDevice|TestWebshotFixture_'
```

Expected: `FAIL` for the three new tests (the second flow, push and notices are absent) and `ok` for the existing `TestWebshotFixture_*` ones.

- [ ] **Step 3: Implement**: edit the JSON as described (quote and keep the surrounding lines: `"flows": [` of session 6 and its closing `],`, then `"push": ""`, `"paused_until": ""` and `"notices": [],` are the lines changed), and add the fill to `readFixture` (a package-level compiled `regexp`, `ReplaceAll` over the raw bytes before `json.NewDecoder`). Run `gofmt -w internal/web/webshot_fixture_test.go tools/webshot/webshot_test.go`.

- [ ] **Step 4: Run them to see them pass**, with both packages, which also decode every other fixture through the new `readFixture`:

```bash
CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/
```

Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git commit -m "test(webshot): session 6's second flow, an expired push with a pause and two notices in the canned snapshot" -- tools/webshot/fixture/snapshot.json tools/webshot/webshot_test.go internal/web/webshot_fixture_test.go
```

### Task 1.3: The board's final row, running step, other run and unpriced agent

**Files:**
- Modify: `tools/webshot/fixture/flow.json` (story 4's block, `final`, `others`, `costs.unpriced`)
- Modify: `internal/web/ui/src/fixtures.test.ts:210-243` (`unionFields`)
- Test: `tools/webshot/webshot_test.go`, `internal/web/ui/src/fixtures.test.ts`

Data (contract: `tools/webshot/fixture/flow.json` (Story 1)). The rows, cells and ids the existing tests read stay: `premise`, stories 1 to 3, story 5, `question`, `kept` (its one line stays, which is the final row's kept line), `stages`, every other `costs` field. The values below are exact so the later stories' tests can read them.

- `final` changes from `null` to a row: `id` `F`, `developer` `""`, `tier` `""`, `review` `""`, `line` 0, `end` 0, `reading` `""`, `status` `""`, `commits` 0, `deviations` 0, `findings` `[0, 0, 0]`, `cells` `{"gates": "passed", "review": "running", "fix": "todo"}` (the final row's kinds are gates, review and fix), and two steps, shaped as the premise row's and story 1's steps are (`name`, `kind`, `k`, `role`, `status`, `jobs`, `report`, `gates`, `started`, `ended`, `unreadable`, `waiting`):
  - `2026-11-09-flow-visualisation-final-gates1`, kind `gates`, `k` 1, `role` `""`, `status` `passed`, `jobs` `[50, 51, 52]`, `report` false, `gates` the three lines `gofmt check`, `go vet ./...`, `go test ./...` each with verdict `PASS` and its job (50, 51, 52), `started` `now-20m`, `ended` `now-16m`.
  - `2026-11-09-flow-visualisation-final-review1`, kind `review`, `k` 1, `role` `code-quality-reviewer`, `status` `running`, `jobs` `[53]`, `report` false, `gates` `[]`, `started` `now-3m`, `ended` `""`, `unreadable` `""`, `waiting` `""`.
- Story 4 starts. Its block (`"id": "4"`, `"developer": "junior-dev"`, `"tier": "light"`, `"review": "vouched"`, `"status": ""`, its `cells` all `todo`, `"steps": []`) changes: `status` `developing`, `cells.dev` `running` (the other four stay `todo`), and `steps` holds one step: `2026-11-09-flow-visualisation-s4-dev1`, kind `dev`, `k` 1, `role` `junior-dev`, `status` `running`, `jobs` `[46]`, `report` false, `gates` `[]`, `started` `now-2m`, `ended` `""`, `unreadable` `""`, `waiting` `waiting for an agent slot`.
- `others` changes from `[]` to one run: `{"id": "2026-11-08-flow-visualisation", "status": "stopped", "start": "now-26h", "unreadable": ""}`.
- `costs.unpriced` changes from `[]` to `["2026-11-09-flow-visualisation-s4-dev1"]` (story 4's agent, which has not been priced yet).

- [ ] **Step 1: Write the failing tests**

  In `tools/webshot/webshot_test.go`, in the style of Task 1.2's (`c.fixture("flow.json")`, a local struct):

  - `TestFixture_TheBoardHoldsAFinalRowARunningStepAnOtherRunAndAnUnpricedAgent`: `final.id` is `F`, its `cells` are `gates` `passed`, `review` `running`, `fix` `todo`, its two steps have kinds `gates` then `review` and statuses `passed` then `running`; story 4 (`id` `4`) has `status` `developing`, `cells.dev` `running`, one step of kind `dev` with `status` `running`, an empty `ended` and a non-empty `waiting`; `others` has one run with a non-empty `id` and `start`; `costs.unpriced` has one entry and it is story 4's step name; `kept` has at least one line; stories 1, 2, 3 and 5 keep their ids in order and story 3's `cells.dev` is still `asks`.
    Fails without: `TestFixture_TheBoardHoldsAFinalRowARunningStepAnOtherRunAndAnUnpricedAgent`: `final` left `null`, either final step's status or kind changed, story 4's step left out or finished (`ended` filled), `waiting` emptied, `others` left empty, `unpriced` naming another agent, or story 3's cells disturbed.

  In `internal/web/ui/src/fixtures.test.ts`, in `unionFields` (an array literal; add after the `flow.json` `stories/*/steps/*/kind` row), two rows: `{ fixture: "flow.json", path: "final/cells/*", union: flowCell }` and `{ fixture: "flow.json", path: "final/steps/*/kind", union: flowStepKind }`. The existing test `it.each(unionFields)` runs them: each needs at least one value at its path and none outside its union.
    Fails without: `fixture "flow.json" holds only values of its union at final/cells/*`: `final` left `null` (no value at the path, so `values.length` is 0) or a cell outside `FlowCell`.

- [ ] **Step 2: Run them to see them fail**

```bash
bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ -run TestFixture_TheBoardHolds && pnpm -C internal/web/ui exec vitest run src/fixtures.test.ts'
```

Expected: the Go test fails (`final` is `null`, story 4 has no step), and the Vitest rows for `final/cells/*` and `final/steps/*/kind` fail with `expected 0 to be greater than 0`.

- [ ] **Step 3: Implement**: edit `flow.json` as described, keeping its two-space indentation and its order of keys.

- [ ] **Step 4: Run them to see them pass**, with the Go decode of `flow.json` into `flowJSON` (unknown fields refused) and the typed Vitest check:

```bash
bash -c 'CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ && pnpm -C internal/web/ui exec vitest run src/fixtures.test.ts && pnpm -C internal/web/ui run typecheck'
```

Expected: `ok` for both packages, the Vitest file passes, `tsc` prints nothing.

- [ ] **Step 5: Commit**

```bash
git commit -m "test(webshot): a final row, a running step, another run and an unpriced agent in the canned board" -- tools/webshot/fixture/flow.json tools/webshot/webshot_test.go internal/web/ui/src/fixtures.test.ts
```

### Task 1.4: The typed loaders

**Files:**
- Modify: `internal/web/ui/src/fixtures.ts:1-12` (the header comment and imports), and the end of the file (four functions)
- Test: `internal/web/ui/src/fixtures.test.ts`

Names this task defines (contract, Story 1), in `fixtures.ts`, each beside `fixtureJobLog` in its style (a doc comment, then `return fillTimes(<json>, Date.now()) as <Type>`, so every call is a fresh copy with its relative times filled as `fixtureSnapshot` fills them):

- `function fixtureFlow(): Flow` over `tools/webshot/fixture/flow.json`
- `function fixtureFlowDoc(): FlowDoc` over `flow-doc.json`
- `function fixtureFlows(): Flows` over `flows.json`
- `function fixtureLaunchDefaults(): LaunchDefaults` over `launch-defaults.json`

The type import line gains `Flow`, `FlowDoc`, `Flows` and `LaunchDefaults` (all four exist in `@/api/gen`), the four JSON files are imported as the existing five are (a relative `../../../../tools/webshot/fixture/<name>.json` path, imports kept in the file's alphabetical order), and the header comment's list of fixtures (`the snapshot, the turns, the diff and the job log`) names them too. Uses part 1's `fillTimes`.

- [ ] **Step 1: Write the failing test** in `internal/web/ui/src/fixtures.test.ts`, as a new `describe("the part 3 loaders", ...)` after the `"the session views' inputs"` block, with the fake `Date` clock that block uses (`vi.useFakeTimers({ toFake: ["Date"] })` and `vi.setSystemTime(NOW)` in `beforeEach`, `vi.useRealTimers()` in `afterEach`), importing the four loaders from `./fixtures`:

  - `the loaders fill every relative time and return a fresh copy`: `JSON.stringify(fixtureFlow())` and `JSON.stringify(fixtureFlows())` contain no `"now` (the board's `start` is `now-1h12m`, so the fill is not vacuous); `fixtureFlow().start` is `2026-11-18T10:48:00Z` for `NOW` of 12:00; after setting `fixtureFlow().summary.title` to `changed` on one call, the next call's is not `changed`; the same fresh-copy check for `fixtureFlows().live`'s first title.
    Fails without: `the loaders fill every relative time and return a fresh copy`: a loader returning the imported JSON without `fillTimes` (`start` stays `now-1h12m`, and the mutation shows in the next call).
  - `the loaders hold the documents the part 3 screens draw`: `fixtureFlow().summary.plan` is session 6's first flow's `plan` in `fixtureSnapshot(NOW)` and `fixtureFlow().final?.id` is `F`; `fixtureFlowDoc().path` is `docs/superpowers/specs/2026-11-09-flow-visualisation-design.md` and its `blocks` has more than one node; `fixtureFlows().live` has an entry whose `session` is 6 and `fixtureFlows().past` is not empty; `fixtureLaunchDefaults().accounts` is `["default", "work"]` and `.models` starts with `opus`.
    Fails without: `the loaders hold the documents the part 3 screens draw`: a loader reading another fixture's file (its path or its plan differs), or `flow.json`'s final row reverted to `null`.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/fixtures.test.ts
```

Expected: the new tests fail with `fixtureFlow is not a function` (and typecheck would say the four names are not exported).

- [ ] **Step 3: Implement** the four loaders and the imports. Format with `pnpm -C internal/web/ui exec prettier --write src/fixtures.ts src/fixtures.test.ts`.

- [ ] **Step 4: Run it to see it pass**, then the story's gates:

```bash
bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'
```

Expected: every check passes; `dist:check` passes unchanged (a fixture module that no screen imports changes no bundle). Then run the story's webshot renders, which need Chromium on `PATH` (`LOOM_WEBSHOT_BROWSER` as the real-browser test names it):

```bash
bash -c 'd=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app classic -screens flow,flow-story,flows,settings,pair,add-to-home -out "$d" && CGO_ENABLED=0 go run ./tools/webshot -app next -screens pair,add-to-home -out "$d"'
```

Expected: a line `<name>	<file>` per screen and no `!` line. A `! console.error` or `! the canned loom has no answer for GET /api/v1/...` line is a failure: for `pair` it means a request escaped the unpaired loom, for `add-to-home` that the tab still reads as standalone (the new app has no Add to Home page yet, so under `-app next` that shot is the fleet, and that is not a failure).

- [ ] **Step 5: Commit**

```bash
git commit -m "test(web): typed loaders for the flow board, a document, the host's flows and the launch defaults" -- internal/web/ui/src/fixtures.ts internal/web/ui/src/fixtures.test.ts
```

## Story 2: Select, Switch, Input and Input OTP, and the scrollbar rules Base UI's Select needs

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/components/ui/select.tsx`, `internal/web/ui/src/components/ui/switch.tsx`, `internal/web/ui/src/components/ui/input.tsx`, `internal/web/ui/src/components/ui/input-otp.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/styles/app.css`, `internal/web/dist/`
- **Interfaces:** exposes `Select`, `SelectTrigger`, `SelectValue`, `SelectContent`, `SelectGroup`, `SelectLabel`, `SelectItem`, `Switch`, `Input`, `InputOTP`, `InputOTPSlot`, `InputOTPSeparator`; uses part 1's `cn` and tokens
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** light (owned copies of shadcn components on Base UI; the CSP and the phone sizes are named tests)
- **Form:** brief (shadcn components and two CSS rules)

Part 3's forms need four components `internal/web/ui/src/components/ui/` lacks (spec §32, Part 3 settlements, Components): Select for the new-session pickers, Switch for the notification toggles, Input for the title, and an Input OTP for the pairing code on Base UI's `OTPField`, so no `input-otp` package. Each is an owned copy in the style of `textarea.tsx` and `toggle-group.tsx` (shadcn's component rewritten on Base UI 1.9, `data-slot` on every part, `className` merged through `cn`, every other prop passed through), keeps form text at 16px and touch targets at 44px, and writes no `<style>`; `main.tsx`'s `CSPProvider disableStyleElements` stays, which is why `app.css` gains the two `.base-ui-disable-scrollbar` rules Base UI would otherwise have injected. The story is brief: every task names the behaviour, the contract names and the tests, and the developer writes the code. None of it holds a landmine marker. Nothing imports these components yet, so the bundle changes only by the two CSS rules; Task 2.5 rebuilds and commits `internal/web/dist/`, and the story's `dist:check` is its last check, not a task's.

Three Base UI facts the tests are written around (from `internal/web/ui/node_modules/@base-ui/react`):

- `Select.Trigger` has role `combobox`; its popup is a portal with role `listbox` and its items role `option`; with the root's `items` prop (a record of value to label, or an array of `{ value, label }`) `Select.Value` shows the chosen item's label even while closed.
- `Switch.Root` renders a `<span role="switch">` with `aria-checked`, and passes `aria-label` and `id` to it.
- `OTPField.Input` is one slot (an `<input>`), numbered by its order inside the root; the root's `id` is the first slot's id and later slots are `{id}-2`, `{id}-3`, and so on; `aria-label` on the first slot is ignored with a warning (it is labelled by a native `<label htmlFor={id}>`), and slots 2 to n take an `aria-label`.

### Task 2.1: The scrollbar rules

**Files:**
- Modify: `internal/web/ui/src/styles/app.css` (after the `@layer base { ... }` block, which ends the file)
- Test: `internal/web/ui/src/components/ui/components.test.tsx`

Contract (`styles/app.css`, Story 2): two plain rules, outside any `@layer`, that Base UI's Select and ScrollArea set the class `base-ui-disable-scrollbar` for and no longer inject under `disableStyleElements`: `.base-ui-disable-scrollbar { scrollbar-width: none }` and `.base-ui-disable-scrollbar::-webkit-scrollbar { display: none }`. A comment above them says why (Base UI's `<style>` is suppressed by the CSP provider, so the rules live here).

- [ ] **Step 1: Write the failing test** in `components.test.tsx`: import the stylesheet's text with `import appCss from "../../styles/app.css?raw";` (the precedent is `import source from "../../public/sw.js?raw"` in `app/sw.test.ts`), and add a `describe("the scrollbar rules", ...)` with one test:

  - `app.css carries the two .base-ui-disable-scrollbar rules Base UI no longer injects`: the text matches `/\.base-ui-disable-scrollbar\s*\{[^}]*scrollbar-width:\s*none/` and `/\.base-ui-disable-scrollbar::-webkit-scrollbar\s*\{[^}]*display:\s*none/`.
    Fails without: `app.css carries the two .base-ui-disable-scrollbar rules Base UI no longer injects`: either rule deleted, or its property changed (`scrollbar-width: auto`, `display: block`).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx -t "scrollbar rules"
```

Expected: the test fails on its first `toMatch` (the stylesheet has no such rule).

- [ ] **Step 3: Implement** the two rules and their comment.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx
```

Expected: the file passes, every earlier test included.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the scrollbar rules Base UI's Select needs under the CSP provider" -- internal/web/ui/src/styles/app.css internal/web/ui/src/components/ui/components.test.tsx
```

### Task 2.2: Input and Switch

**Files:**
- Create: `internal/web/ui/src/components/ui/input.tsx`, `internal/web/ui/src/components/ui/switch.tsx`
- Test: `internal/web/ui/src/components/ui/components.test.tsx`

Names this task defines (contract, Story 2), each exported by name, a function returning `ReactElement`, imports `cn` from `@/lib/utils`:

- `function Input(props: ComponentProps<"input">): ReactElement` in `input.tsx`: a plain `<input>` with `data-slot="input"`, the classes of `Textarea` that apply to a one-line field (`min-h-11 w-full rounded-lg border border-input bg-background px-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50`), `className` merged through `cn` after them, and every other prop (`type`, `value`, `onChange`, `onKeyDown`, `placeholder`, `id`, `aria-label`) passed through.
- `function Switch(props: ComponentProps<typeof SwitchPrimitive.Root>): ReactElement` in `switch.tsx` (`SwitchPrimitive` being `@base-ui/react/switch`): the Root with its Thumb inside, `data-slot="switch"` on the Root and `data-slot="switch-thumb"` on the Thumb; a track about 44 by 26 px coloured from the tokens by `data-checked` and `data-unchecked` (the checked track `bg-primary`, the unchecked `bg-input`, a thumb that moves across), `focus-visible:ring-2 focus-visible:ring-ring`, `data-disabled:opacity-50` (the Root is a `<span>`, which `:disabled` never matches); the Root carries `relative` and `after:absolute after:top-1/2 after:left-1/2 after:h-full after:min-h-11 after:w-full after:min-w-11 after:-translate-x-1/2 after:-translate-y-1/2 after:content-['']` for a 44px hit area as `toggle-group.tsx` does; `className` merged through `cn` and every other Root prop (`checked`, `onCheckedChange`, `disabled`, `id`, `aria-label`) passed through. Uses part 1's tokens only (no literal colours).

- [ ] **Step 1: Write the failing tests** in `components.test.tsx` (imports of `./input` and `./switch` added to its import block in alphabetical place), a `describe("Input", ...)` and a `describe("Switch", ...)`:

  - `Input: a plain input that takes its props, at 16px with a 44px floor, and a caller's class wins`: renders `<Input placeholder="title" onChange={fn} />`; the element is an `INPUT` with `data-slot="input"` and classes `text-[16px]` and `min-h-11`; typing `hi` leaves value `hi` and calls `fn` twice; `<Input className="min-h-14" />` has `min-h-14` and not `min-h-11`; `type="password"` and `id` pass through.
    Fails without: `Input: a plain input that takes its props, at 16px with a 44px floor, and a caller's class wins`: the `text-[16px]` or `min-h-11` class dropped, `{...props}` not spread (no typing, no `fn`), or `className` not merged through `cn` (both min-heights present).
  - `Switch: a click turns it on, reports the new value, and a controlled one shows the value it is given`: uncontrolled `<Switch aria-label="Needs you" defaultChecked={false} onCheckedChange={fn} />`: `getByRole("switch", { name: "Needs you" })` has `aria-checked="false"`, a click makes it `"true"` and `fn`'s first argument `true`; a second click `false`. A controlled `<Switch aria-label="Finished" checked={false} onCheckedChange={fn2} />` is clicked: `fn2` is called with `true` and the switch still reads `aria-checked="false"` (the Settings toggle shows the value asked for, then the stored one, and a refusal snaps it back).
    Fails without: `Switch: a click turns it on, reports the new value, and a controlled one shows the value it is given`: `onCheckedChange` or `checked` not passed through (no call, or the controlled switch flips by itself), or `aria-label` not passed (`getByRole` finds no named switch).
  - `Switch: a disabled one takes no click, and its hit area reaches 44px`: `<Switch disabled onCheckedChange={fn} aria-label="Off" />` clicked calls nothing and stays `aria-checked="false"`; an enabled switch has classes `after:min-h-11` and `after:min-w-11` and `data-slot="switch"`.
    Fails without: `Switch: a disabled one takes no click, and its hit area reaches 44px`: `disabled` not passed through (the click reaches `fn`), or the `after:` classes dropped.

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx -t "Input|Switch"
```

Expected: the file fails to load: `Failed to resolve import "./input"` (and `"./switch"`).

- [ ] **Step 3: Implement** both files. Format with `pnpm -C internal/web/ui exec prettier --write src/components/ui/input.tsx src/components/ui/switch.tsx src/components/ui/components.test.tsx`.

- [ ] **Step 4: Run them to see them pass**

```bash
pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx
```

Expected: the file passes.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/components/ui/input.tsx internal/web/ui/src/components/ui/switch.tsx
git commit -m "feat(web): shadcn's Input and Switch on Base UI" -- internal/web/ui/src/components/ui/input.tsx internal/web/ui/src/components/ui/switch.tsx internal/web/ui/src/components/ui/components.test.tsx
```

### Task 2.3: Select

**Files:**
- Create: `internal/web/ui/src/components/ui/select.tsx`
- Test: `internal/web/ui/src/components/ui/components.test.tsx`

Names this task defines (contract, Story 2), all in `select.tsx`, `SelectPrimitive` being `@base-ui/react/select`; each but `Select` takes `props: ComponentProps<typeof SelectPrimitive.<Part>>`, returns `ReactElement`, merges its `className` through `cn` and passes every other prop through; every part has a `data-slot` of its own name:

- `Select`: `SelectPrimitive.Root` re-exported (`const Select = SelectPrimitive.Root`), so `value`, `onValueChange`, `items` and `disabled` pass through.
- `SelectTrigger` (`Trigger`, `data-slot="select-trigger"`): its children, then a lucide `ChevronDown` icon (`lucide-react` is a dependency); `flex min-h-11 w-full items-center justify-between gap-2 rounded-lg border border-input bg-background px-3 text-[16px] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50`; `id` and `aria-label` pass through.
- `SelectValue` (`Value`, `data-slot="select-value"`): `data-placeholder:text-faint` so a placeholder reads dim.
- `SelectContent` (`data-slot="select-content"`): `Portal` > `Positioner` > `Popup` > `List` in one, its props going to the `Popup`; the popup is as wide as the trigger (`min-w-(--anchor-width)`), a `max-h-72` that scrolls, `rounded-lg border border-border bg-popover text-popover-foreground shadow-lg`, above the sticky header and the drawers (a `z-50` class on the Positioner); the scrolling element carries `base-ui-disable-scrollbar` only if Base UI sets it itself (the class is Base UI's, the rules are Task 2.1's; add none by hand).
- `SelectGroup` (`Group`, `data-slot="select-group"`) and `SelectLabel` (`GroupLabel`, `data-slot="select-label"`, `px-3 py-2 text-xs text-faint`).
- `SelectItem` (`Item`, `data-slot="select-item"`): its children inside `ItemText`, and an `ItemIndicator` holding a lucide `Check`; `flex min-h-11 items-center gap-2 px-3 text-[16px] outline-none data-highlighted:bg-accent data-highlighted:text-accent-foreground data-disabled:opacity-50`.

- [ ] **Step 1: Write the failing tests** in `components.test.tsx` (importing the seven names from `./select`, and `CSPProvider` from `@base-ui/react/csp-provider`; every Select is rendered inside `<CSPProvider disableStyleElements>`, as `main.tsx` does, since without it an opened Select writes a `<style>`). A small `SampleSelect` helper in the file renders a `Select` with `items={{ a: "Alpha", b: "Bravo", c: "Charlie" }}` and a `value` and `onValueChange` it is given, a `SelectTrigger` with `aria-label="Pick"` holding `<SelectValue placeholder="(default)" />`, and a `SelectContent` with a `SelectGroup` labelled `Active` holding items `a` and `b` and a `SelectGroup` labelled `Other` holding item `c` (the new-session workspace Select's shape). Tests, in `describe("Select", ...)` (opening needs `userEvent.setup()` and `await screen.findByRole("listbox")`, since the popup mounts after the click):

  - `Select: the trigger shows the chosen label, opening lists the groups, and picking an item reports its value`: with `value="b"` the combobox named `Pick` reads `Bravo`; with `value=""` it reads `(default)`; after a click on the trigger the listbox holds the texts `Active` and `Other` and three options `Alpha`, `Bravo`, `Charlie`; a click on `Charlie` calls `onValueChange` with `"c"` as its first argument.
    Fails without: `Select: the trigger shows the chosen label, opening lists the groups, and picking an item reports its value`: `Select` not the Root (no `items`, so the trigger reads `b`), `SelectLabel` not rendering its group's label, `SelectItem` not passing `value`, or `onValueChange` not reaching the Root.
  - `Select: a disabled one does not open`: `disabled` on the root, a click on the trigger, and no listbox in the document.
    Fails without: `Select: a disabled one does not open`: `disabled` not passed through (the listbox opens).
  - `Select: its trigger, items and label keep the phone sizes, the slots and the caller's props`: the trigger has `data-slot="select-trigger"`, classes `min-h-11` and `text-[16px]`, the `aria-label` and an `id` it was given; `<SelectTrigger className="w-40">` has `w-40` and not `w-full`; after opening, each option has `data-slot="select-item"` and `min-h-11`, and the group label `data-slot="select-label"`.
    Fails without: `Select: its trigger, items and label keep the phone sizes, the slots and the caller's props`: the `min-h-11` or `text-[16px]` classes dropped from the trigger or an item (iOS zooms and the target shrinks), or `className` not merged through `cn` (`w-40` and `w-full` both present).

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx -t "Select"
```

Expected: the file fails to load: `Failed to resolve import "./select"`.

- [ ] **Step 3: Implement** `select.tsx`. If an assertion on the opened popup needs a happy-dom stub (a missing `ResizeObserver`, say), put it in the test file, not in `src/test-setup.ts`, and say so in the commit message body. Format with prettier as in Task 2.2.

- [ ] **Step 4: Run them to see them pass**

```bash
pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx
```

Expected: the file passes.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/components/ui/select.tsx
git commit -m "feat(web): shadcn's Select on Base UI" -- internal/web/ui/src/components/ui/select.tsx internal/web/ui/src/components/ui/components.test.tsx
```

### Task 2.4: Input OTP

**Files:**
- Create: `internal/web/ui/src/components/ui/input-otp.tsx`
- Test: `internal/web/ui/src/components/ui/components.test.tsx`

Names this task defines (contract, Story 2), all in `input-otp.tsx`, `OTPField` being `@base-ui/react/otp-field`; each returns `ReactElement`, merges `className` through `cn`, passes every other prop through, and has a `data-slot`:

- `function InputOTP(props: OTPField.Root.Props): ReactElement`: `data-slot="input-otp"`, a flex row (`flex items-center gap-2`) over `OTPField.Root`; `length`, `validationType`, `normalizeValue`, `value`, `onValueChange`, `id`, `inputMode` and `autoComplete` and `aria-describedby` pass through untouched (the pairing screen passes its idempotent normaliser, so the root must not wrap or replace it).
- `function InputOTPSlot(props: OTPField.Input.Props): ReactElement`: `data-slot="input-otp-slot"`, one `OTPField.Input`: `size-11 rounded-lg border border-input bg-background text-center text-[16px] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50`; no `index` prop (Base UI numbers slots by order); `aria-label` and `autoFocus` pass through.
- `function InputOTPSeparator(props: OTPField.Separator.Props): ReactElement`: `data-slot="input-otp-separator"`, a short bar (`h-0.5 w-2 bg-border`) between two groups of slots.

There is no `InputOTPGroup`: a caller groups slots in plain `div`s inside the root.

- [ ] **Step 1: Write the failing tests** in `components.test.tsx` (importing the three names from `./input-otp`), in `describe("InputOTP", ...)`. A `SampleOTP` helper renders, around a `useState` string shown in `<output data-testid="value">`, a `<label htmlFor="code">Code</label>` and an `InputOTP` with `id="code"`, `length={8}`, `validationType="alphanumeric"`, `value`, `onValueChange` setting the state, and `normalizeValue` a local idempotent function that upper-cases and drops every character outside `23456789ABCDEFGHJKMNPQRSTUVWXYZ` (a copy for the test; Story 11 owns the real one), holding two `div`s of four `InputOTPSlot`s (slots 2 to 8 with `aria-label="Character <n> of 8"`) with an `InputOTPSeparator` between the `div`s:

  - `InputOTP: typing fills the slots in order through the caller's normaliser`: clicking the first slot (found by its label `Code`) and typing `k7qm` leaves the value output at `K7QM`, with slots 1 to 4 holding `K`, `7`, `Q`, `M` and focus on the fifth; typing `o0` after it adds nothing (the `O` and `0` are outside the alphabet), and typing `2xpa` completes it as `K7QM2XPA`.
    Fails without: `InputOTP: typing fills the slots in order through the caller's normaliser`: `normalizeValue` not passed to the Root (the value is `k7qm`, lower case), `value` or `onValueChange` not passed (the output stays empty), or `length` not passed.
  - `InputOTP: a paste is normalised and clamped to the length`: pasting `k7qm-2xpa9` into the first slot (`user.paste`) leaves the value output at `K7QM2XPA` (the dash and the ninth character dropped).
    Fails without: `InputOTP: a paste is normalised and clamped to the length`: `normalizeValue` not passed to the Root (the pasted text keeps its lower case), or `length` not passed (a ninth character stays).
  - `InputOTP: eight 44px slots, a separator, the root's id on the first slot and the caller's props`: the root has `data-slot="input-otp"` and a `flex` class; there are eight inputs with `data-slot="input-otp-slot"`, each with classes `size-11` and `text-[16px]`; the first has `id="code"` and is found by the label `Code`; the second has the accessible name `Character 2 of 8`; one element has `data-slot="input-otp-separator"`; `<InputOTPSlot className="size-12" />` has `size-12` and not `size-11`.
    Fails without: `InputOTP: eight 44px slots, a separator, the root's id on the first slot and the caller's props`: `size-11` or `text-[16px]` dropped from a slot, `id` not passed to the Root (the label finds nothing), `aria-label` not passed to a slot, or `className` not merged through `cn`.

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx -t "InputOTP"
```

Expected: the file fails to load: `Failed to resolve import "./input-otp"`.

- [ ] **Step 3: Implement** `input-otp.tsx`. If a paste in happy-dom does not reach Base UI's handler through `user.paste`, dispatch the same event the component listens for in the test, not a different component behaviour. Format with prettier as in Task 2.2.

- [ ] **Step 4: Run them to see them pass**

```bash
pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx
```

Expected: the file passes.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/components/ui/input-otp.tsx
git commit -m "feat(web): shadcn's Input OTP on Base UI's OTPField" -- internal/web/ui/src/components/ui/input-otp.tsx internal/web/ui/src/components/ui/components.test.tsx
```

### Task 2.5: No `<style>`, and the rebuilt bundle

**Files:**
- Modify: `internal/web/ui/src/components/ui/components.test.tsx:243-270` (the `no <style> element` describe: a second test beside the first, whose lines stay)
- Modify: `internal/web/dist/` (rebuilt)

- [ ] **Step 1: Write the failing test**: in the `describe("no <style> element", ...)` block, after its existing test (leave that test as it is), add:

  - `No Select, Switch, Input or InputOTP injects a <style> element, even with the Select open under the CSP provider`: starting from zero `<style>` elements, render inside `<CSPProvider disableStyleElements>` an `Input`, a `Switch` (`defaultChecked`), an `InputOTP` of two slots and the Task 2.3 `SampleSelect` shape; open the Select with a click on its trigger and wait for the listbox; the document holds no `<style>` element.
    Fails without: `No Select, Switch, Input or InputOTP injects a <style> element, even with the Select open under the CSP provider`: any of the four rendering a `<style>` of its own (a `SelectContent` that wraps its popup in one, say), which the CSP drops on the phone and webshot reports as a console error.

- [ ] **Step 2: Run it to see it fail or pass**: this test guards what Tasks 2.2 to 2.4 already built, so it passes once written; confirm it is not vacuous by running it with the provider's `disableStyleElements` removed from the test for one run, which must fail with a `<style>` element in the document (Base UI's Select writes its scrollbar `<style>` without it), then restore it. If Base UI's `<style>` does not reach the document in happy-dom without the provider, say so in the commit message body and keep the test as a guard of the four components' own code.

```bash
pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx -t "no <style> element"
```

- [ ] **Step 3: Rebuild the bundle** (the two CSS rules change the built stylesheet, so its hash and `index.html` change):

```bash
bash -c 'pnpm -C internal/web/ui run build && grep -l "base-ui-disable-scrollbar" internal/web/dist/assets/*.css'
```

Expected: the build succeeds and the `grep` prints one `internal/web/dist/assets/<name>-<hash>.css` path.

- [ ] **Step 4: Run the story's checks**

```bash
bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'
```

Expected: every check passes, `dist:check` included (the bundle is fresh) and `ok github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "build(web): rebuild the bundle for the scrollbar rules and the new components" -- internal/web/dist internal/web/ui/src/components/ui/components.test.tsx
```

## Story 3: The reads for the flow views, the host's flows and launch defaults

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/api/reads.ts`, `internal/web/ui/src/api/reads.test.ts`, `internal/web/dist/`
- **Interfaces:** exposes `KEEP_FLOWS`, `FlowOptions`, `FlowView`, `useFlow`, `flowKey`, `FlowDocOptions`, `FlowDocView`, `useFlowDoc`, `FlowsView`, `useFlows`, `LaunchDefaultsView`, `useLaunchDefaults`; uses part 1's `api`, `queryClient`, `unwrap` and `OutcomeError`, part 2's `keepRecent`, and Story 1's fixture loaders
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** full (a cache bound and retention: boards held past their observers and evicted beyond `KEEP_FLOWS`, the held board kept on a failed read, launch defaults held for the page's lifetime; the owner, 2026-11-20)

Part 3's screens read four more things (spec §26, Part 3 settlements 2 and 7), and this story adds each to `internal/web/ui/src/api/reads.ts` as a hook with its classic trigger and none of them polling. The board (`useFlow`) is held per session and plan with `gcTime: Infinity` for the last `KEEP_FLOWS` (8) opened, through the module's own `keepRecent`; its query key is the session and plan only, so a new `key` (the JSON of the session's flow summaries, `flowKey`) refetches the held entry and a re-priced run never adds a cache entry; one read is in flight per board and a key change asked meanwhile (the first read's included, so a board opened mid-run is never left one step stale) runs once when it lands, as the diff's does; the disabled entry of an empty plan, like that of an undefined workspace below, leaves the cache with its observer. A document (`useFlowDoc`) is a query keyed by session, plan and document that leaves the cache with its last reader, so a change of any option draws its own document or "loading", never the previous one. The host's flows (`useFlows`) are one shared query: the first caller to mount reads, a later caller (`FlowsHead` beside `FlowsBody`) sends nothing, and `reload` refetches for every caller. Launch defaults (`useLaunchDefaults`) are read once per workspace once the stream is `live`, held for the page's lifetime with `staleTime: Infinity` and `gcTime: Infinity`, never read again on their own once held (a stream that comes back, or a remount, reads nothing), and `retry` reads again after a failure. Every read is made with `retry: false` (the next attempt is the user's or the next key change), and a failed read keeps what is held and sets `error`. No task here holds a landmine marker.

### Task 3.1: The board: `KEEP_FLOWS`, `flowKey` and `useFlow`

**Files:**
- Modify: `internal/web/ui/src/api/reads.ts:9` (the `./gen` import), and the end of the file (a new section after `useJobLog`)
- Test: `internal/web/ui/src/api/reads.test.ts`

Names this task defines (contract, Story 3), all in `reads.ts`: `const KEEP_FLOWS = 8`, `interface FlowOptions`, `interface FlowView`, `function flowKey(session: Session): string` and `function useFlow(options: FlowOptions): FlowView`. It reuses the module's `startRead`, `inFlight`, `again`, `askedAgain`, `errorText`, `hashOf` and `keepRecent`, `api`, `unwrap` and `queryClient`.

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/api/reads.test.ts`:

1. Add `Flow` and `Session` to the `import type { AgentPage, JobLog, Outcome, Patch, Turn, Turns } from "./gen";` statement (alphabetical position).
2. Change `import { fixtureJobLog, fixturePatch, fixtureTurns } from "@/fixtures";` to also import `fixtureFlow` and `fixtureSnapshot`.
3. Add `flowKey`, `KEEP_FLOWS`, `useFlow` and `type FlowOptions` to the `import { … } from "./reads";` list (before its closing `} from "./reads";`).
4. In `internal/web/ui/src/api/reads.test.ts`, after the line `let logRead: ReturnType<typeof spyOnLog>;` add:

```ts
const spyOnFlow = () => vi.spyOn(api, "flow").mockRejectedValue(new Error("unexpected flow read"));
let flowRead: ReturnType<typeof spyOnFlow>;
```

5. In `beforeEach`, after the line `  logRead = spyOnLog();` add `  flowRead = spyOnFlow();`.
6. After the `refused` helper (the `const refused = (status: number, message: string): Outcome<never> => ({ … });` statement) add the outcome the client builds for a 504 and the tasks below use for a read that loom may be asked again (the default `retryRead` reads a `timeout` or `failed` outcome again, never a `refused` one):

```ts
const timedOut = (message: string): Outcome<never> => ({ kind: "timeout", message });
```

7. Append at the end of `internal/web/ui/src/api/reads.test.ts`:

```ts
const PLAN = "docs/superpowers/plans/2026-11-09-flow-visualisation.md";

const flowOptions = (extra: Partial<FlowOptions> = {}): FlowOptions => ({
  session: 6,
  plan: PLAN,
  key: "k1",
  ...extra,
});

function mountFlow(initial: FlowOptions) {
  return renderHook((props: FlowOptions) => useFlow(props), {
    initialProps: initial,
    wrapper,
  });
}

function boardTitled(title: string): Flow {
  const board = fixtureFlow();
  return { ...board, summary: { ...board.summary, title } };
}

function sessionSix(): Session {
  const found = Object.values(fixtureSnapshot().sessions ?? {})
    .flatMap((list) => list ?? [])
    .find((s) => s.id === 6);
  if (found === undefined) throw new Error("the fixture has no session 6");
  return found;
}

describe("flowKey", () => {
  it("is the JSON of every flow summary the session lists, and of none", () => {
    const session = sessionSix();
    const flows = session.flows ?? [];
    expect(flows.length).toBeGreaterThan(1);
    expect(flowKey(session)).toBe(JSON.stringify(flows));
    expect(flowKey({ ...session, flows: null })).toBe("null");
    const lastRepriced = flows.map((f, i) => (i === flows.length - 1 ? { ...f, done: f.done + 1 } : f));
    expect(flowKey({ ...session, flows: lastRepriced })).not.toBe(flowKey(session));
  });
});

describe("useFlow", () => {
  it("reads the board on mount, with no poll", async () => {
    const board = fixtureFlow();
    flowRead.mockResolvedValueOnce(ok(board));
    const { result } = mountFlow(flowOptions());
    expect(result.current).toMatchObject({ flow: undefined, loading: true, error: "" });
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(1);
    expect(flowRead).toHaveBeenCalledWith(6, { plan: PLAN });
    expect(result.current).toMatchObject({ flow: board, loading: false, error: "" });
    await settle(60_000);
    expect(flowRead).toHaveBeenCalledTimes(1);
  });

  it("reads the held board again when the key changes, in the one cache entry", async () => {
    flowRead.mockResolvedValueOnce(ok(boardTitled("first")));
    const view = mountFlow(flowOptions());
    await settle();
    flowRead.mockResolvedValueOnce(ok(boardTitled("second")));
    view.rerender(flowOptions({ key: "k2" }));
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(2);
    expect(view.result.current.flow?.summary.title).toBe("second");
    view.rerender(flowOptions({ key: "k2" }));
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(2);
    expect(queryClient.getQueryCache().findAll({ queryKey: ["flow"] })).toHaveLength(1);
  });

  it("queues one read for key changes asked while the first read is still out, run when it lands", async () => {
    const first = deferred<Outcome<Flow>>();
    flowRead.mockReturnValueOnce(first.promise);
    const view = mountFlow(flowOptions());
    await settle();
    view.rerender(flowOptions({ key: "k2" }));
    view.rerender(flowOptions({ key: "k3" }));
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(1);
    flowRead.mockResolvedValueOnce(ok(boardTitled("second")));
    first.resolve(ok(boardTitled("first")));
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(2);
    expect(view.result.current.flow?.summary.title).toBe("second");
    await settle(60_000);
    expect(flowRead).toHaveBeenCalledTimes(2);
  });

  it("keeps one read in flight and runs a key change asked meanwhile once, when it lands", async () => {
    flowRead.mockResolvedValueOnce(ok(boardTitled("first")));
    const view = mountFlow(flowOptions());
    await settle();
    const second = deferred<Outcome<Flow>>();
    flowRead.mockReturnValueOnce(second.promise);
    view.rerender(flowOptions({ key: "k2" }));
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(2);
    view.rerender(flowOptions({ key: "k3" }));
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(2);
    flowRead.mockResolvedValueOnce(ok(boardTitled("third")));
    second.resolve(ok(boardTitled("second")));
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(3);
    expect(view.result.current.flow?.summary.title).toBe("third");
    await settle(60_000);
    expect(flowRead).toHaveBeenCalledTimes(3);
  });

  it("keeps the held board on a failed read, says why, and clears the error on the next read", async () => {
    const board = fixtureFlow();
    flowRead.mockResolvedValueOnce(ok(board));
    const view = mountFlow(flowOptions());
    await settle();
    flowRead.mockResolvedValueOnce(refused(409, "the worktree is busy"));
    view.rerender(flowOptions({ key: "k2" }));
    await settle();
    expect(view.result.current).toMatchObject({
      flow: board,
      loading: false,
      error: "the worktree is busy",
    });
    flowRead.mockResolvedValueOnce(ok(board));
    view.rerender(flowOptions({ key: "k3" }));
    await settle();
    expect(view.result.current.error).toBe("");
  });

  it("does not read a failed read again on its own, even when loom could be asked again", async () => {
    flowRead.mockResolvedValueOnce(timedOut("loom did not answer in time"));
    const view = mountFlow(flowOptions());
    await settle(60_000);
    expect(flowRead).toHaveBeenCalledTimes(1);
    expect(view.result.current).toMatchObject({
      flow: undefined,
      loading: false,
      error: "loom did not answer in time",
    });
  });

  it("reads again on a key change after a first read that failed", async () => {
    flowRead.mockResolvedValueOnce(refused(409, "the worktree is busy"));
    const view = mountFlow(flowOptions());
    await settle();
    expect(view.result.current).toMatchObject({
      flow: undefined,
      loading: false,
      error: "the worktree is busy",
    });
    const board = fixtureFlow();
    flowRead.mockResolvedValueOnce(ok(board));
    view.rerender(flowOptions({ key: "k2" }));
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(2);
    expect(view.result.current).toMatchObject({ flow: board, error: "" });
  });

  it("reads nothing for an empty plan, and reads once a plan is given", async () => {
    const view = mountFlow(flowOptions({ plan: "" }));
    await settle();
    expect(flowRead).not.toHaveBeenCalled();
    expect(view.result.current).toEqual({ flow: undefined, error: "", loading: false });
    view.rerender(flowOptions({ plan: "", key: "k2" }));
    await settle();
    expect(flowRead).not.toHaveBeenCalled();
    flowRead.mockResolvedValueOnce(ok(fixtureFlow()));
    view.rerender(flowOptions());
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(1);
    expect(view.result.current.flow).toBeDefined();
  });

  it("holds no cache entry for an empty plan once its reader is gone", async () => {
    const view = mountFlow(flowOptions({ plan: "" }));
    await settle();
    view.unmount();
    await settle();
    expect(queryClient.getQueryCache().findAll({ queryKey: ["flow"] })).toHaveLength(0);
  });

  it("draws a picked plan's own board or loading, never the previous plan's", async () => {
    const other = "docs/superpowers/plans/2026-11-16-flow-plan-treatment-part-2-estimate.md";
    flowRead.mockResolvedValueOnce(ok(boardTitled("plan a")));
    const view = mountFlow(flowOptions());
    await settle();
    const pending = deferred<Outcome<Flow>>();
    flowRead.mockReturnValueOnce(pending.promise);
    view.rerender(flowOptions({ plan: other }));
    await settle();
    expect(flowRead).toHaveBeenLastCalledWith(6, { plan: other });
    expect(view.result.current).toMatchObject({ flow: undefined, loading: true });
    pending.resolve(ok(boardTitled("plan b")));
    await settle();
    expect(view.result.current.flow?.summary.title).toBe("plan b");
    flowRead.mockResolvedValueOnce(ok(boardTitled("plan a")));
    view.rerender(flowOptions());
    expect(view.result.current.flow?.summary.title).toBe("plan a");
    await settle();
  });

  it("draws the held board at once when a session is opened again, and reads it again", async () => {
    flowRead.mockResolvedValueOnce(ok(boardTitled("held")));
    const first = mountFlow(flowOptions());
    await settle();
    first.unmount();
    await settle(10 * 60_000);
    flowRead.mockResolvedValueOnce(ok(boardTitled("fresh")));
    const again = mountFlow(flowOptions());
    expect(again.result.current.flow?.summary.title).toBe("held");
    await settle();
    expect(flowRead).toHaveBeenCalledTimes(2);
    expect(again.result.current.flow?.summary.title).toBe("fresh");
  });

  it("keeps the boards of the last KEEP_FLOWS sessions and evicts the least recently opened", async () => {
    const open = async (session: number): Promise<void> => {
      flowRead.mockResolvedValueOnce(ok(boardTitled(`session ${session}`)));
      const view = mountFlow(flowOptions({ session }));
      await settle();
      view.unmount();
    };
    for (let session = 1; session <= KEEP_FLOWS + 1; session++) await open(session);
    await settle(10 * 60_000);
    flowRead.mockResolvedValue(ok(fixtureFlow()));
    const two = mountFlow(flowOptions({ session: 2 }));
    expect(two.result.current.flow?.summary.title).toBe("session 2");
    const one = mountFlow(flowOptions({ session: 1 }));
    expect(one.result.current).toMatchObject({ flow: undefined, loading: true });
    await settle();
  });

  it("drops the boards with the cache when the pairing is revoked", async () => {
    flowRead.mockResolvedValueOnce(ok(fixtureFlow()));
    const view = mountFlow(flowOptions());
    await settle();
    view.unmount();
    queryClient.clear();
    flowRead.mockResolvedValueOnce(ok(fixtureFlow()));
    const again = mountFlow(flowOptions());
    expect(again.result.current.flow).toBeUndefined();
    await settle();
  });
});
```

Fails without: `flowKey`: a key that stringifies only the first summary (`session.flows?.[0]`) or the plan (the last summary's `done` changes and the keys are equal).
Fails without: `useFlow reads the board on mount, with no poll`: the observer's `enabled` always false (no mount read), or a `refetchInterval` (the count passes 1 after 60 s).
Fails without: `useFlow reads the held board again when the key changes, in the one cache entry`: the key-change effect removed (the count stays 1), or `key` in the query key (a second entry, and the board falls back to loading).
Fails without: `useFlow does not read a failed read again on its own, even when loom could be asked again`: `retry: false` removed from `useFlow`'s `useQuery` (the default `retryRead` reads a timed-out read again after 1 s and 2 s: the count passes 1).
Fails without: `useFlow queues one read for key changes asked while the first read is still out, run when it lands`: the key-change effect skipping a board whose first read is out (no second read: the count stays 1 and the title stays "first"), or `reloadFlow`'s `again.add` removed (the same).
Fails without: `useFlow keeps one read in flight and runs a key change asked meanwhile once, when it lands`: `reloadFlow`'s `again.add` removed (the count stays 2 and the title stays "second"), or `askedAgain` dropped from `readFlow`'s `finally`.
Fails without: `useFlow keeps the held board on a failed read, says why, and clears the error on the next read`: the hook returning `undefined` for `flow` once `error` is set, or `errorText` replaced by a fixed text.
Fails without: `useFlow reads again on a key change after a first read that failed`: the effect's `state !== undefined` test narrowed to `state?.data !== undefined` (no second read after a failed first one).
Fails without: `useFlow reads nothing for an empty plan, and reads once a plan is given`: `enabled: plan !== ""` removed (a read with an empty plan), or `loading` not guarded by the plan (loading stays true).
Fails without: `useFlow holds no cache entry for an empty plan once its reader is gone`: `gcTime: Infinity` for every plan (the `["flow", 6, ""]` entry stays in the cache).
Fails without: `useFlow draws a picked plan's own board or loading, never the previous plan's`: `plan` left out of the query key (the other plan draws plan a's board at once).
Fails without: `useFlow draws the held board at once when a session is opened again, and reads it again`: `gcTime: Infinity` removed (the board is gone after 10 minutes), or `enabled` false on a held query (no second read).
Fails without: `useFlow keeps the boards of the last KEEP_FLOWS sessions and evicts the least recently opened`: `openFlow`'s `keepRecent` call removed (session 1's board is still held and `loading` is false).
Fails without: `useFlow drops the boards with the cache when the pairing is revoked`: a board kept outside the query cache (a module map), which `queryClient.clear()` does not empty.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts
```

Expected: the file's earlier tests pass; the `flowKey` and `useFlow` tests fail with `TypeError: (0 , flowKey) is not a function` and `TypeError: (0 , useFlow) is not a function`.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/api/reads.ts`, add `Flow` and `Session` to the `import type { AgentPage, JobLog, Patch, Turn, Turns, TurnsParams } from "./gen";` statement. Then append at the end of the file, after `useJobLog`'s closing brace:

```ts
// The reads of part 3's views (spec §26, Part 3 settlements 2 and 7): the flow board and
// document, the host's flows and launch defaults, each read by the classic app's triggers
// (internal/web/static/js/flow.mjs, flowdoc.mjs, flows.mjs and newsession.mjs) and none polled.

/** How many boards keep their place: a ninth evicts the least recently opened. */
export const KEEP_FLOWS = 8;

const flowQueryKey = (session: number, plan: string): QueryKey => ["flow", session, plan];

// The boards kept, least recently opened first.
let keptFlows: QueryKey[] = [];

function openFlow(session: number, plan: string): void {
  keptFlows = keepRecent(keptFlows, flowQueryKey(session, plan), KEEP_FLOWS);
}

/** What a board is read again on: the JSON of the session's flow summaries, as classic keys it. */
export function flowKey(session: Session): string {
  return JSON.stringify(session.flows);
}

async function readFlow(session: number, plan: string): Promise<Flow> {
  try {
    return unwrap(await api.flow(session, { plan }));
  } finally {
    askedAgain(flowQueryKey(session, plan), () => reloadFlow(session, plan));
  }
}

// reloadFlow reads the board: one read is in flight at a time, and a read asked for meanwhile
// runs once when it lands.
function reloadFlow(session: number, plan: string): void {
  const key = flowQueryKey(session, plan);
  if (inFlight(key)) {
    again.add(hashOf(key));
    return;
  }
  startRead(key, () => readFlow(session, plan), Infinity);
}

export interface FlowOptions {
  session: number;
  /** The flow's plan path; empty reads nothing. */
  plan: string;
  /** What `flowKey` answers for the session: a new one reads a held board again. */
  key: string;
}

export interface FlowView {
  flow: Flow | undefined;
  /** Why the last read failed, "" for none; the board held stays. */
  error: string;
  /** No board is held and the read has neither landed nor failed. */
  loading: boolean;
}

/**
 * Reads a session's flow board on mount (a held board drawn first), and again whenever `key`
 * changes (a change asked while a read is out runs once when it lands). Boards are held per session and plan, the
 * last KEEP_FLOWS opened, and the query key is the session and plan only, so a re-priced run
 * (a new `key`) never adds an entry. A plan that is not the one drawn never shows its board.
 */
export function useFlow({ session, plan, key }: FlowOptions): FlowView {
  const query = useQuery<Flow>({
    queryKey: flowQueryKey(session, plan),
    queryFn: () => readFlow(session, plan),
    enabled: plan !== "",
    // A board is held past its observers; the disabled entry of an empty plan holds nothing and
    // leaves with its observer.
    gcTime: plan !== "" ? Infinity : 0,
    retry: false,
  });
  useEffect(() => {
    if (plan !== "") openFlow(session, plan);
  }, [session, plan]);
  // seen follows the options: a change of session or plan is a new query whose own observer
  // reads it, and a mount is no change, so only a new key asks for a read of its own. A key
  // that changes while the first read is out is asked all the same: reloadFlow queues it, and it
  // runs once when that read lands, so a board opened mid-run is never left one step stale.
  const seen = useRef({ session, plan, key });
  useEffect(() => {
    const was = seen.current;
    seen.current = { session, plan, key };
    if (plan === "" || was.session !== session || was.plan !== plan || was.key === key) return;
    if (queryClient.getQueryState(flowQueryKey(session, plan)) !== undefined) {
      reloadFlow(session, plan);
    }
  }, [session, plan, key]);
  return {
    flow: query.data,
    error: errorText(query.error, "Could not read the flow."),
    loading: plan !== "" && query.data === undefined && query.error === null,
  };
}
```

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec prettier --write src/api/reads.ts src/api/reads.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts
```

Expected: typecheck and lint print nothing, and vitest ends with `Test Files  1 passed (1)`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): read a session's flow board, held for the last 8 and read again on a new flow key" -- internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts
```

### Task 3.2: A flow document: `FlowDocOptions`, `FlowDocView` and `useFlowDoc`

**Files:**
- Modify: `internal/web/ui/src/api/reads.ts` (the `./gen` import, and the end of the file after `useFlow`)
- Test: `internal/web/ui/src/api/reads.test.ts`

Names this task defines (contract, Story 3), in `reads.ts`: `interface FlowDocOptions`, `interface FlowDocView` and `function useFlowDoc(options: FlowDocOptions): FlowDocView`.

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/api/reads.test.ts`:

1. Add `FlowDoc` to the `import type { … } from "./gen";` statement, and `fixtureFlowDoc` to the `@/fixtures` import, and `useFlowDoc` and `type FlowDocOptions` to the `./reads` import list.
2. In `internal/web/ui/src/api/reads.test.ts`, after the line `let flowRead: ReturnType<typeof spyOnFlow>;` add:

```ts
const spyOnFlowDoc = () =>
  vi.spyOn(api, "flowDoc").mockRejectedValue(new Error("unexpected flow document read"));
let flowDocRead: ReturnType<typeof spyOnFlowDoc>;
```

3. In `beforeEach`, after `  flowRead = spyOnFlow();` add `  flowDocRead = spyOnFlowDoc();`.
4. Append at the end of `internal/web/ui/src/api/reads.test.ts`:

```ts
const docOptions = (extra: Partial<FlowDocOptions> = {}): FlowDocOptions => ({
  session: 6,
  plan: PLAN,
  doc: "spec",
  ...extra,
});

interface DocCase {
  name: string;
  change: Partial<FlowDocOptions>;
  session: number;
  plan: string;
  doc: string;
}

function mountDoc(initial: FlowDocOptions) {
  return renderHook((props: FlowDocOptions) => useFlowDoc(props), {
    initialProps: initial,
    wrapper,
  });
}

describe("useFlowDoc", () => {
  it("reads the document on mount, with no poll", async () => {
    const doc = fixtureFlowDoc();
    flowDocRead.mockResolvedValueOnce(ok(doc));
    const { result } = mountDoc(docOptions());
    expect(result.current).toMatchObject({ doc: undefined, loading: true, error: "" });
    await settle();
    expect(flowDocRead).toHaveBeenCalledTimes(1);
    expect(flowDocRead).toHaveBeenCalledWith(6, { plan: PLAN, doc: "spec" });
    expect(result.current).toMatchObject({ doc, loading: false, error: "" });
    await settle(60_000);
    expect(flowDocRead).toHaveBeenCalledTimes(1);
  });

  it.each<DocCase>([
    { name: "session", change: { session: 7 }, session: 7, plan: PLAN, doc: "spec" },
    {
      name: "plan",
      change: { plan: "docs/superpowers/plans/other.md" },
      session: 6,
      plan: "docs/superpowers/plans/other.md",
      doc: "spec",
    },
    { name: "document", change: { doc: "plan" }, session: 6, plan: PLAN, doc: "plan" },
  ])("reads again when the $name changes, and not when nothing does", async (c) => {
    flowDocRead.mockResolvedValue(ok(fixtureFlowDoc()));
    const view = mountDoc(docOptions());
    await settle();
    view.rerender(docOptions());
    await settle();
    expect(flowDocRead).toHaveBeenCalledTimes(1);
    view.rerender(docOptions(c.change));
    await settle();
    expect(flowDocRead).toHaveBeenCalledTimes(2);
    expect(flowDocRead).toHaveBeenLastCalledWith(c.session, { plan: c.plan, doc: c.doc });
  });

  it.each<{ name: string; empty: Partial<FlowDocOptions> }>([
    { name: "plan", empty: { plan: "" } },
    { name: "document", empty: { doc: "" } },
  ])("reads nothing for an empty $name", async (c) => {
    const view = mountDoc(docOptions(c.empty));
    await settle();
    expect(flowDocRead).not.toHaveBeenCalled();
    expect(view.result.current).toEqual({ doc: undefined, error: "", loading: false });
  });

  it("draws the document asked for or loading, never the previous one", async () => {
    flowDocRead.mockResolvedValueOnce(ok(fixtureFlowDoc()));
    const view = mountDoc(docOptions());
    await settle();
    const pending = deferred<Outcome<FlowDoc>>();
    flowDocRead.mockReturnValueOnce(pending.promise);
    view.rerender(docOptions({ doc: "plan" }));
    await settle();
    expect(view.result.current).toMatchObject({ doc: undefined, loading: true });
    const next = { ...fixtureFlowDoc(), path: "docs/superpowers/plans/x.md" };
    pending.resolve(ok(next));
    await settle();
    expect(view.result.current.doc).toEqual(next);
  });

  it("says why a read failed", async () => {
    flowDocRead.mockResolvedValueOnce(refused(404, "no such document"));
    const { result } = mountDoc(docOptions());
    await settle();
    expect(result.current).toEqual({ doc: undefined, error: "no such document", loading: false });
  });

  it("does not keep a document once its reader is gone", async () => {
    flowDocRead.mockResolvedValueOnce(ok(fixtureFlowDoc()));
    const first = mountDoc(docOptions());
    await settle();
    first.unmount();
    await settle();
    flowDocRead.mockResolvedValueOnce(ok(fixtureFlowDoc()));
    const again = mountDoc(docOptions());
    expect(again.result.current).toMatchObject({ doc: undefined, loading: true });
    await settle();
    expect(flowDocRead).toHaveBeenCalledTimes(2);
  });
});
```

Fails without: `useFlowDoc reads the document on mount, with no poll`: `enabled` always false (no read), or a `refetchInterval` (the count passes 1 after 60 s).
Fails without: `useFlowDoc reads again when the $name changes, and not when nothing does`: the session, plan or document left out of the query key (the changed option reads nothing).
Fails without: `useFlowDoc reads nothing for an empty $name`: `enabled` ignoring either term (a read with an empty plan or document), or `loading` not guarded (it stays true).
Fails without: `useFlowDoc draws the document asked for or loading, never the previous one`: `placeholderData: keepPreviousData` added (the previous document draws while the next loads).
Fails without: `useFlowDoc says why a read failed`: `errorText` replaced by a fixed text, or the failure swallowed.
Fails without: `useFlowDoc does not keep a document once its reader is gone`: `gcTime: 0` removed (the default 5 minutes holds it and the remount draws it at once).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts
```

Expected: the `useFlowDoc` tests fail with `TypeError: (0 , useFlowDoc) is not a function`; the earlier tests pass.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/api/reads.ts`, add `FlowDoc` to the `import type { … } from "./gen";` statement (prettier will wrap it over lines), then append after `useFlow`'s closing brace:

```ts
export interface FlowDocOptions {
  session: number;
  /** The flow's plan path; empty reads nothing. */
  plan: string;
  /** The document's name (`spec`, `plan`, `report:<id>`); empty reads nothing. */
  doc: string;
}

export interface FlowDocView {
  doc: FlowDoc | undefined;
  /** Why the read failed, "" for none. */
  error: string;
  /** The read has neither landed nor failed. */
  loading: boolean;
}

/**
 * Reads a flow document on mount and whenever the session, plan or document changes, never
 * polled. The query is keyed by all three and leaves the cache with its last reader, so a view
 * draws the document it asked for or loading, never the previous one.
 */
export function useFlowDoc({ session, plan, doc }: FlowDocOptions): FlowDocView {
  const enabled = plan !== "" && doc !== "";
  const query = useQuery<FlowDoc>({
    queryKey: ["flow-doc", session, plan, doc],
    queryFn: async () => unwrap(await api.flowDoc(session, { plan, doc })),
    enabled,
    gcTime: 0,
    retry: false,
  });
  return {
    doc: query.data,
    error: errorText(query.error, "Could not read the document."),
    loading: enabled && query.data === undefined && query.error === null,
  };
}
```

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec prettier --write src/api/reads.ts src/api/reads.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts
```

Expected: typecheck and lint print nothing, and vitest ends with `Test Files  1 passed (1)`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): read a flow document on open and on a change of session, plan or document" -- internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts
```

### Task 3.3: The host's flows: `FlowsView` and `useFlows`

**Files:**
- Modify: `internal/web/ui/src/api/reads.ts` (the `./gen` import, and the end of the file after `useFlowDoc`)
- Test: `internal/web/ui/src/api/reads.test.ts`

Names this task defines (contract, Story 3), in `reads.ts`: `interface FlowsView` and `function useFlows(): FlowsView`.

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/api/reads.test.ts`:

1. Add `Flows` to the `import type { … } from "./gen";` statement, `fixtureFlows` to the `@/fixtures` import, `useFlows` to the `./reads` import list, and `StrictMode` to the `react` import (`import { createElement, StrictMode, type ReactElement, type ReactNode } from "react";`).
2. After `let flowDocRead: ReturnType<typeof spyOnFlowDoc>;` add:

```ts
const spyOnFlows = () => vi.spyOn(api, "flows").mockRejectedValue(new Error("unexpected flows read"));
let flowsRead: ReturnType<typeof spyOnFlows>;
```

3. In `beforeEach`, after `  flowDocRead = spyOnFlowDoc();` add `  flowsRead = spyOnFlows();`.
4. Append at the end of `internal/web/ui/src/api/reads.test.ts`:

```ts
function mountFlows() {
  return renderHook(() => useFlows(), { wrapper });
}

// The app mounts under StrictMode (main.tsx), whose second mount effect runs while the first
// caller's read is out.
function strictWrapper({ children }: { children: ReactNode }): ReactElement {
  return createElement(StrictMode, null, wrapper({ children }));
}

// Head and body are the two callers the host's flows screen has: both call useFlows.
function mountBoth() {
  return renderHook(() => ({ head: useFlows(), body: useFlows() }), { wrapper });
}

describe("useFlows", () => {
  it("reads the host's flows on mount, with no poll", async () => {
    const flows = fixtureFlows();
    flowsRead.mockResolvedValueOnce(ok(flows));
    const { result } = mountFlows();
    expect(result.current).toMatchObject({ flows: undefined, loading: true, error: "" });
    await settle();
    expect(flowsRead).toHaveBeenCalledTimes(1);
    expect(result.current).toMatchObject({ flows, loading: false, error: "" });
    await settle(60_000);
    expect(flowsRead).toHaveBeenCalledTimes(1);
  });

  it("sends one read for callers that mount together and for one that mounts later", async () => {
    flowsRead.mockResolvedValueOnce(ok(fixtureFlows()));
    const both = mountBoth();
    await settle();
    expect(flowsRead).toHaveBeenCalledTimes(1);
    expect(both.result.current.head.flows).toBeDefined();
    expect(both.result.current.body.flows).toBe(both.result.current.head.flows);
    const later = mountFlows();
    expect(later.result.current.flows).toBe(both.result.current.head.flows);
    await settle(60_000);
    expect(flowsRead).toHaveBeenCalledTimes(1);
  });

  it("sends one read when the app mounts under StrictMode", async () => {
    flowsRead.mockResolvedValueOnce(ok(fixtureFlows()));
    const { result } = renderHook(() => useFlows(), { wrapper: strictWrapper });
    await settle();
    expect(flowsRead).toHaveBeenCalledTimes(1);
    expect(result.current.flows).toBeDefined();
    await settle(60_000);
    expect(flowsRead).toHaveBeenCalledTimes(1);
  });

  it("refetches for every caller on reload", async () => {
    flowsRead.mockResolvedValueOnce(ok(fixtureFlows()));
    const both = mountBoth();
    await settle();
    const updated: Flows = { ...fixtureFlows(), live: [] };
    flowsRead.mockResolvedValueOnce(ok(updated));
    act(() => {
      both.result.current.head.reload();
    });
    await settle();
    expect(flowsRead).toHaveBeenCalledTimes(2);
    expect(both.result.current.body.flows).toEqual(updated);
    expect(both.result.current.head.flows).toEqual(updated);
  });

  it("draws the held answer at once when opened again, and reads again", async () => {
    const first = fixtureFlows();
    flowsRead.mockResolvedValueOnce(ok(first));
    const view = mountBoth();
    await settle();
    view.unmount();
    await settle();
    flowsRead.mockResolvedValueOnce(ok({ ...first, live: [] }));
    const again = mountBoth();
    expect(again.result.current.head.flows).toEqual(first);
    await settle();
    expect(flowsRead).toHaveBeenCalledTimes(2);
    expect(again.result.current.head.flows?.live).toEqual([]);
  });

  it("keeps the held answer on a failed read, says why, and clears the error on the next", async () => {
    const flows = fixtureFlows();
    flowsRead.mockResolvedValueOnce(ok(flows));
    const { result } = mountFlows();
    await settle();
    flowsRead.mockResolvedValueOnce(timedOut("loom did not answer in time"));
    act(() => {
      result.current.reload();
    });
    await settle(60_000);
    expect(flowsRead).toHaveBeenCalledTimes(2);
    expect(result.current).toMatchObject({
      flows,
      loading: false,
      error: "loom did not answer in time",
    });
    flowsRead.mockResolvedValueOnce(ok(flows));
    act(() => {
      result.current.reload();
    });
    await settle();
    expect(result.current.error).toBe("");
  });

  it("says why a first read failed, and loads again on reload", async () => {
    flowsRead.mockResolvedValueOnce(timedOut("loom did not answer in time"));
    const { result } = mountFlows();
    await settle(60_000);
    expect(flowsRead).toHaveBeenCalledTimes(1);
    expect(result.current).toMatchObject({
      flows: undefined,
      loading: false,
      error: "loom did not answer in time",
    });
    const pending = deferred<Outcome<Flows>>();
    flowsRead.mockReturnValueOnce(pending.promise);
    act(() => {
      result.current.reload();
    });
    await settle();
    expect(result.current).toMatchObject({ loading: true, error: "" });
    pending.resolve(ok(fixtureFlows()));
    await settle();
    expect(result.current).toMatchObject({ loading: false, error: "" });
    expect(result.current.flows).toBeDefined();
  });

  it("keeps one read in flight and runs a reload asked meanwhile once, when it lands", async () => {
    const first = deferred<Outcome<Flows>>();
    flowsRead.mockReturnValueOnce(first.promise);
    const { result } = mountFlows();
    await settle();
    act(() => {
      result.current.reload();
    });
    expect(flowsRead).toHaveBeenCalledTimes(1);
    flowsRead.mockResolvedValueOnce(ok(fixtureFlows()));
    first.resolve(ok(fixtureFlows()));
    await settle();
    expect(flowsRead).toHaveBeenCalledTimes(2);
    await settle(60_000);
    expect(flowsRead).toHaveBeenCalledTimes(2);
  });
});
```

Fails without: `useFlows reads the host's flows on mount, with no poll`: no mount read (the first caller's effect removed), or a `refetchInterval`.
Fails without: `useFlows sends one read for callers that mount together and for one that mounts later`: the first-caller guard removed from the mount effect (each caller reads, so two or three reads), or the observer fetching on mount (a stale query refetched by the later caller).
Fails without: `useFlows sends one read when the app mounts under StrictMode`: the `!inFlight(flowsKey)` term removed from the mount effect (the second mount effect asks `reloadFlows` while the first read is out, and it runs when that read lands: two reads).
Fails without: `useFlows refetches for every caller on reload`: `reload` bound to one caller's own state instead of the shared query (the body keeps the old answer).
Fails without: `useFlows draws the held answer at once when opened again, and reads again`: the reader count not decremented in the effect's cleanup (no read on the second open).
Fails without: `useFlows keeps the held answer on a failed read, says why, and clears the error on the next`: the hook returning `undefined` for `flows` once `error` is set, or a fixed error text, or `retry: false` removed from `startRead` (a timed-out read is read again: the count passes 2).
Fails without: `useFlows says why a first read failed, and loads again on reload`: `reload` not starting a read while an error is held (loading never turns true).
Fails without: `useFlows keeps one read in flight and runs a reload asked meanwhile once, when it lands`: `reloadFlows`'s `again.add` removed (the count stays 1), or `askedAgain` dropped from `readFlows`'s `finally`.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts
```

Expected: the `useFlows` tests fail with `TypeError: (0 , useFlows) is not a function`; the earlier tests pass.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/api/reads.ts`, add `Flows` to the `import type { … } from "./gen";` statement (after `FlowDoc`), then append after `useFlowDoc`'s closing brace:

```ts
const flowsKey: QueryKey = ["flows"];

// How many components read the host's flows now. The query's observers only watch (they are
// disabled): the first caller to mount asks for the read, and a caller that mounts while another
// is mounted (FlowsHead beside FlowsBody) sends none, so a screen with two callers reads once.
let flowsReaders = 0;

async function readFlows(): Promise<Flows> {
  try {
    return unwrap(await api.flows());
  } finally {
    askedAgain(flowsKey, reloadFlows);
  }
}

// reloadFlows reads the host's flows: one read is in flight at a time, and a read asked for
// meanwhile runs once when it lands.
function reloadFlows(): void {
  if (inFlight(flowsKey)) {
    again.add(hashOf(flowsKey));
    return;
  }
  startRead(flowsKey, readFlows, DEFAULT_GC);
}

export interface FlowsView {
  flows: Flows | undefined;
  /** Why the last read failed, "" for none; the answer held stays. */
  error: string;
  /** No answer is held and the read has neither landed nor failed. */
  loading: boolean;
  /** Reads the host's flows again, for every caller. */
  reload: () => void;
}

/**
 * Reads the host's flows when the first caller mounts and on `reload`, never polled. Every
 * caller shares one query, so FlowsHead's reload redraws FlowsBody, and the answer of the last
 * visit, when it is within the 5 minutes the cache keeps it, is drawn at once while the next
 * read runs.
 */
export function useFlows(): FlowsView {
  const query = useQuery<Flows>({
    queryKey: flowsKey,
    queryFn: readFlows,
    // The observer only watches: every read goes through startRead, which is never retried.
    enabled: false,
  });
  useEffect(() => {
    const first = flowsReaders === 0;
    flowsReaders++;
    if (first && !inFlight(flowsKey)) reloadFlows();
    return () => {
      flowsReaders--;
    };
  }, []);
  return {
    flows: query.data,
    error: errorText(query.error, "Could not read the host's flows."),
    loading: query.data === undefined && query.error === null,
    reload: reloadFlows,
  };
}
```

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec prettier --write src/api/reads.ts src/api/reads.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts
```

Expected: typecheck and lint print nothing, and vitest ends with `Test Files  1 passed (1)`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): read the host's flows once for every caller, and again on reload" -- internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts
```

### Task 3.4: Launch defaults: `LaunchDefaultsView` and `useLaunchDefaults`, then the bundle

**Files:**
- Modify: `internal/web/ui/src/api/reads.ts` (the `./gen` import, and the end of the file after `useFlows`)
- Modify: `internal/web/dist/` (only if the build changes it)
- Test: `internal/web/ui/src/api/reads.test.ts`

Names this task defines (contract, Story 3), in `reads.ts`: `interface LaunchDefaultsView` and `function useLaunchDefaults(workspace: number | undefined, live: boolean): LaunchDefaultsView`.

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/api/reads.test.ts`:

1. Add `LaunchDefaults` to the `import type { … } from "./gen";` statement, `fixtureLaunchDefaults` to the `@/fixtures` import, and `useLaunchDefaults` to the `./reads` import list.
2. After `let flowsRead: ReturnType<typeof spyOnFlows>;` add:

```ts
const spyOnDefaults = () =>
  vi.spyOn(api, "launchDefaults").mockRejectedValue(new Error("unexpected launch defaults read"));
let defaultsRead: ReturnType<typeof spyOnDefaults>;
```

3. In `beforeEach`, after `  flowsRead = spyOnFlows();` add `  defaultsRead = spyOnDefaults();`.
4. Append at the end of `internal/web/ui/src/api/reads.test.ts`:

```ts
interface DefaultsProps {
  workspace: number | undefined;
  live: boolean;
}

function mountDefaults(initial: DefaultsProps) {
  return renderHook((props: DefaultsProps) => useLaunchDefaults(props.workspace, props.live), {
    initialProps: initial,
    wrapper,
  });
}

const defaultsFor = (model: string): LaunchDefaults => ({ ...fixtureLaunchDefaults(), model });

describe("useLaunchDefaults", () => {
  it("waits for the stream, then reads the workspace's defaults once", async () => {
    const defaults = fixtureLaunchDefaults();
    const view = mountDefaults({ workspace: 3, live: false });
    await settle();
    expect(defaultsRead).not.toHaveBeenCalled();
    expect(view.result.current).toMatchObject({ defaults: undefined, loading: true, error: "" });
    defaultsRead.mockResolvedValueOnce(ok(defaults));
    view.rerender({ workspace: 3, live: true });
    await settle();
    expect(defaultsRead).toHaveBeenCalledTimes(1);
    expect(defaultsRead).toHaveBeenCalledWith({ workspace: "3" });
    expect(view.result.current).toMatchObject({ defaults, loading: false, error: "" });
    await settle(60_000);
    expect(defaultsRead).toHaveBeenCalledTimes(1);
  });

  it("reads nothing without a workspace", async () => {
    const view = mountDefaults({ workspace: undefined, live: true });
    await settle();
    expect(defaultsRead).not.toHaveBeenCalled();
    expect(view.result.current).toMatchObject({ defaults: undefined, loading: false, error: "" });
  });

  it("does not read again when the stream comes back", async () => {
    const defaults = fixtureLaunchDefaults();
    defaultsRead.mockResolvedValueOnce(ok(defaults));
    const view = mountDefaults({ workspace: 3, live: true });
    await settle();
    view.rerender({ workspace: 3, live: false });
    await settle();
    expect(view.result.current.defaults).toEqual(defaults);
    view.rerender({ workspace: 3, live: true });
    await settle(60_000);
    expect(defaultsRead).toHaveBeenCalledTimes(1);
    expect(view.result.current.defaults).toEqual(defaults);
  });

  it("holds the answer for the page's lifetime, across a remount", async () => {
    defaultsRead.mockResolvedValueOnce(ok(defaultsFor("held")));
    const first = mountDefaults({ workspace: 3, live: true });
    await settle();
    first.unmount();
    await settle(10 * 60_000);
    const again = mountDefaults({ workspace: 3, live: true });
    expect(again.result.current.defaults?.model).toBe("held");
    await settle(60_000);
    expect(defaultsRead).toHaveBeenCalledTimes(1);
  });

  it("reads once for each workspace, and a workspace seen before reads nothing", async () => {
    defaultsRead.mockResolvedValueOnce(ok(defaultsFor("three")));
    const view = mountDefaults({ workspace: 3, live: true });
    await settle();
    defaultsRead.mockResolvedValueOnce(ok(defaultsFor("four")));
    view.rerender({ workspace: 4, live: true });
    await settle();
    expect(defaultsRead).toHaveBeenCalledTimes(2);
    expect(defaultsRead).toHaveBeenLastCalledWith({ workspace: "4" });
    expect(view.result.current.defaults?.model).toBe("four");
    view.rerender({ workspace: 3, live: true });
    await settle(60_000);
    expect(defaultsRead).toHaveBeenCalledTimes(2);
    expect(view.result.current.defaults?.model).toBe("three");
  });

  it("does not read a failed read again on its own, even when loom could be asked again", async () => {
    defaultsRead.mockResolvedValueOnce({ kind: "failed", status: 0, message: "loom is unreachable" });
    const view = mountDefaults({ workspace: 3, live: true });
    await settle(60_000);
    expect(defaultsRead).toHaveBeenCalledTimes(1);
    expect(view.result.current).toMatchObject({
      defaults: undefined,
      loading: false,
      error: "loom is unreachable",
    });
  });

  it("holds no cache entry for an undefined workspace once its reader is gone", async () => {
    const view = mountDefaults({ workspace: undefined, live: true });
    await settle();
    view.unmount();
    await settle();
    expect(queryClient.getQueryCache().findAll({ queryKey: ["launch-defaults"] })).toHaveLength(0);
  });

  it("says why a read failed and reads again only on retry", async () => {
    defaultsRead.mockResolvedValueOnce(refused(400, "workspace: no such workspace"));
    const view = mountDefaults({ workspace: 3, live: true });
    await settle();
    expect(view.result.current).toMatchObject({
      defaults: undefined,
      loading: false,
      error: "workspace: no such workspace",
    });
    await settle(60_000);
    expect(defaultsRead).toHaveBeenCalledTimes(1);
    const pending = deferred<Outcome<LaunchDefaults>>();
    defaultsRead.mockReturnValueOnce(pending.promise);
    act(() => {
      view.result.current.retry();
      view.result.current.retry();
    });
    await settle();
    expect(defaultsRead).toHaveBeenCalledTimes(2);
    expect(view.result.current).toMatchObject({ loading: true, error: "" });
    pending.resolve(ok(fixtureLaunchDefaults()));
    await settle();
    expect(view.result.current).toMatchObject({ loading: false, error: "" });
    expect(view.result.current.defaults).toEqual(fixtureLaunchDefaults());
  });

  it("drops the defaults with the cache when the pairing is revoked", async () => {
    defaultsRead.mockResolvedValueOnce(ok(fixtureLaunchDefaults()));
    const view = mountDefaults({ workspace: 3, live: true });
    await settle();
    view.unmount();
    queryClient.clear();
    defaultsRead.mockResolvedValueOnce(ok(fixtureLaunchDefaults()));
    const again = mountDefaults({ workspace: 3, live: true });
    expect(again.result.current.defaults).toBeUndefined();
    await settle();
    expect(defaultsRead).toHaveBeenCalledTimes(2);
  });
});
```

Fails without: `useLaunchDefaults waits for the stream, then reads the workspace's defaults once`: `enabled` not reading `live` (a read before the stream is live), or the workspace sent as a number (the argument is `{ workspace: 3 }`, not `"3"`).
Fails without: `useLaunchDefaults reads nothing without a workspace`: `enabled` not reading `workspace` (a read for an undefined workspace), or `loading` not guarded by it (it stays true).
Fails without: `useLaunchDefaults does not read again when the stream comes back`: `staleTime: Infinity` removed (the observer reads the held answer again when `live` turns true).
Fails without: `useLaunchDefaults holds the answer for the page's lifetime, across a remount`: `gcTime: Infinity` removed (the answer is gone after 10 minutes and read again), or `staleTime: Infinity` removed (a remount reads again).
Fails without: `useLaunchDefaults reads once for each workspace, and a workspace seen before reads nothing`: the workspace left out of the query key (workspace 4 draws 3's defaults and reads nothing), or a key that drops the held answer on a change.
Fails without: `useLaunchDefaults does not read a failed read again on its own, even when loom could be asked again`: `retry: false` removed from `useLaunchDefaults`' `useQuery` (the default `retryRead` reads a `failed` outcome again after 1 s and 2 s: the count passes 1).
Fails without: `useLaunchDefaults holds no cache entry for an undefined workspace once its reader is gone`: `gcTime: Infinity` for every workspace (the `["launch-defaults", null]` entry stays in the cache).
Fails without: `useLaunchDefaults says why a read failed and reads again only on retry`: `retry` doing nothing (the count stays 1 and `loading` stays false).
Fails without: `useLaunchDefaults drops the defaults with the cache when the pairing is revoked`: the answer kept outside the query cache.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts
```

Expected: the `useLaunchDefaults` tests fail with `TypeError: (0 , useLaunchDefaults) is not a function`; the earlier tests pass.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/api/reads.ts`, add `LaunchDefaults` to the `import type { … } from "./gen";` statement (after `JobLog`), then append after `useFlows`'s closing brace:

```ts
const defaultsKey = (workspace: number | undefined): QueryKey => [
  "launch-defaults",
  workspace ?? null,
];

async function readDefaults(workspace: number): Promise<LaunchDefaults> {
  return unwrap(await api.launchDefaults({ workspace: String(workspace) }));
}

export interface LaunchDefaultsView {
  defaults: LaunchDefaults | undefined;
  /** Why the read failed, "" for none. */
  error: string;
  /** A workspace is given and its defaults have neither landed nor failed (the stream may be down). */
  loading: boolean;
  /** Reads the defaults again after a failure; startRead joins a read already out rather than repeating it. */
  retry: () => void;
}

/**
 * Reads a workspace's launch defaults once the stream is `live`, and holds the answer for the
 * page's lifetime: it is never read again on its own, so a stream that comes back or a remount
 * keeps the picks made on it (`retry` reads again after a failure). A failed read is read again
 * by the observer only when the stream comes back or the screen is opened again. No workspace
 * reads nothing.
 */
export function useLaunchDefaults(
  workspace: number | undefined,
  live: boolean,
): LaunchDefaultsView {
  const query = useQuery<LaunchDefaults>({
    queryKey: defaultsKey(workspace),
    queryFn: async () => {
      if (workspace === undefined) throw new Error("no workspace to read the defaults of");
      return readDefaults(workspace);
    },
    enabled: workspace !== undefined && live,
    staleTime: Infinity,
    // An answer is held for the page's lifetime; the disabled entry of no workspace holds nothing
    // and leaves with its observer.
    gcTime: workspace !== undefined ? Infinity : 0,
    retry: false,
  });
  const retry = useCallback(() => {
    if (workspace === undefined) return;
    startRead(defaultsKey(workspace), () => readDefaults(workspace), Infinity);
  }, [workspace]);
  return {
    defaults: query.data,
    error: errorText(query.error, "Could not load the launch defaults."),
    loading: workspace !== undefined && query.data === undefined && query.error === null,
    retry,
  };
}
```

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec prettier --write src/api/reads.ts src/api/reads.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts
```

Expected: typecheck and lint print nothing, and vitest ends with `Test Files  1 passed (1)`.

Then the story's gates. No module imports the four hooks yet, so the build tree-shakes them out and `internal/web/dist/` may be unchanged:

```bash
pnpm -C internal/web/ui run build && git status --short internal/web/dist && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/
```

Expected: `git status` prints nothing when the bundle is unchanged (or lists the files the build rewrote), `check` and `dist:check` pass, and `go test` prints `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): read a workspace's launch defaults once the stream is live, held for the page" -- internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts
```

If `git status --short internal/web/dist` listed files, commit them with the change instead: add `internal/web/dist` after the two paths in the pathspec (`git add <the new files>` first, then `git commit -m "<the same message>" -- internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts internal/web/dist`).

## Story 4: A session just created is pending, the store keeps the recent notices, and the create, device, quick-replies, unpair and pair commands

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/store/pending.ts`, `internal/web/ui/src/store/pending.test.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/fleet.test.ts`, `internal/web/ui/src/store/startFleet.test.ts`, `internal/web/ui/src/commands/launch.ts`, `internal/web/ui/src/commands/launch.test.ts`, `internal/web/ui/src/commands/device.ts`, `internal/web/ui/src/commands/device.test.ts`, `internal/web/ui/src/commands/index.ts`, `internal/web/ui/src/commands/index.test.ts`, `internal/web/ui/src/app/commands.ts`, `internal/web/ui/CLAUDE.md`, `internal/web/dist/`
- **Interfaces:** exposes `PENDING_MS`, `PendingState`, `usePending`, `useIsPending`, `MAX_NOTICES`, `LaunchPicks`, `LaunchForm`, `launchBody`, and the commands `session.create`, `device.update`, `quickReplies.save`, `device.unpair`, `device.pair`; uses part 1's command path, `api`, `showOutcome`, `showMessage`, `hrefFor`, `onNotices`, `emitUnpaired`, `startFleet` and `useFleet`, part 2's `commands/testkit.ts`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
- **Review:** deep
- **Form:** full (the bodies sent to the daemon, the unpaired path a revoke runs, an offline command that bypasses the stale gate, store writes from answers and the bounded notices)

The new app can read the daemon but, apart from the session's own actions, cannot yet write to it. This story adds the pending slice (`store/pending.ts`: a session id a 201 marked, held for 5 s, let go sooner when the snapshot lists the session or the device is unpaired), makes the store keep every notice a response or the stream delivers (the last 20, each once), and registers the remaining writes as commands with the classic words: `session.create` with `launchBody`, `device.update`, `quickReplies.save`, `device.unpair` and the offline `device.pair`. The commands apply each answer to the store as the stream's event would, unless the held value changed while the write was in flight (the stream's own event, a new snapshot, or another write's answer). The story holds no landmine marker, and its developer runs at the default effort.

Five tasks: the pending slice, the notices, `launchBody` and `session.create`, the device commands, and their registration with the `CLAUDE.md` amendment and the rebuilt bundle. Each task leaves the Vitest suite, the typecheck and the lint green. The bundle (`internal/web/dist/`) is rebuilt in the last task only: Task 4.2 changes `store/fleet.ts`, which the entry imports, so `pnpm run dist:check` reports the bundle stale from Task 4.2 to Task 4.4, and Task 4.5 restores its freshness.

### Task 4.1: The pending slice

**Files:**
- Create: `internal/web/ui/src/store/pending.ts`
- Create: `internal/web/ui/src/store/pending.test.ts`

`store/pending.ts` imports `useFleet`; `store/fleet.ts` never imports it back (that cycle would leave `useFleet` in its temporal dead zone when the entry loads), so the unpaired clear is a listener this module registers when it loads, and the watch of the snapshot is opened by `mark` and closed by `clear`, never at module top level.

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/store/pending.test.ts`:

```ts
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { emitUnpaired } from "@/api/request";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import { PENDING_MS, useIsPending, usePending } from "./pending";

const NOW = Date.parse("2026-11-18T12:00:00Z");
const NEW_ID = 4242;
const pending = () => usePending.getState();

// list is the stream's sessions event for workspace 1 with a session of this id added to the ones
// the snapshot holds.
function list(id: number): void {
  const base = allSessions(fixtureSnapshot(NOW))[0];
  if (base === undefined) throw new Error("the canned snapshot holds no session");
  const held = useFleet.getState().snapshot?.sessions?.["1"] ?? [];
  useFleet
    .getState()
    .apply("sessions", { workspace: 1, sessions: [...held, { ...base, id, workspace: 1 }], badge: 0 });
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  vi.setSystemTime(NOW);
  useFleet.setState({ snapshot: fixtureSnapshot(NOW), stale: false, link: "live", updated: NOW });
});

afterEach(() => {
  pending().clear();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  vi.useRealTimers();
});

describe("the pending session", () => {
  it("starts with no session pending", () => {
    expect(pending().id).toBeNull();
  });

  it("holds a marked id for PENDING_MS and then lets it go", () => {
    pending().mark(NEW_ID);
    expect(pending().id).toBe(NEW_ID);
    vi.advanceTimersByTime(PENDING_MS - 1);
    expect(pending().id).toBe(NEW_ID);
    vi.advanceTimersByTime(1);
    expect(pending().id).toBeNull();
  });

  it("replaces an earlier id and restarts the wait, so the earlier wait never ends the later id", () => {
    pending().mark(NEW_ID);
    vi.advanceTimersByTime(3000);
    pending().mark(NEW_ID + 1);
    vi.advanceTimersByTime(3000);
    expect(pending().id).toBe(NEW_ID + 1);
    vi.advanceTimersByTime(2000);
    expect(pending().id).toBeNull();
  });

  it("lets go once the snapshot lists the session, and not for another session's listing", () => {
    pending().mark(NEW_ID);
    list(NEW_ID + 1);
    expect(pending().id).toBe(NEW_ID);
    list(NEW_ID);
    expect(pending().id).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("lets go at once for a session the snapshot already lists", () => {
    list(NEW_ID);
    pending().mark(NEW_ID);
    expect(pending().id).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("clear cancels the wait and closes the watch of the snapshot", () => {
    pending().mark(NEW_ID);
    pending().clear();
    expect(pending().id).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
    // A watch that outlived clear would let go of this id when NEW_ID is listed.
    usePending.setState({ id: 7 });
    list(NEW_ID);
    expect(pending().id).toBe(7);
  });

  it("lets go when the device is unpaired", () => {
    pending().mark(NEW_ID);
    emitUnpaired();
    expect(pending().id).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe("useIsPending", () => {
  it("is true only for the marked id, and false for an undefined one", () => {
    const marked = renderHook(() => useIsPending(NEW_ID));
    const other = renderHook(() => useIsPending(NEW_ID + 1));
    const none = renderHook(() => useIsPending(undefined));
    expect(marked.result.current).toBe(false);

    act(() => {
      pending().mark(NEW_ID);
    });
    expect(marked.result.current).toBe(true);
    expect(other.result.current).toBe(false);
    expect(none.result.current).toBe(false);

    act(() => {
      pending().clear();
    });
    expect(marked.result.current).toBe(false);
  });
});
```

Fails without: "holds a marked id for PENDING_MS and then lets it go": the `setTimeout` in `mark` removed (the id stays), or `PENDING_MS` changed.
Fails without: "replaces an earlier id and restarts the wait, so the earlier wait never ends the later id": `release()` dropped from the top of `mark` (the first timer ends the second id at 5 s).
Fails without: "lets go once the snapshot lists the session, and not for another session's listing": the `useFleet.subscribe` watch removed (the id stays), or its `listed` check made to match any listing (the first `list` lets go).
Fails without: "lets go at once for a session the snapshot already lists": the check of the current snapshot in `mark` removed (the id is held for 5 s).
Fails without: "clear cancels the wait and closes the watch of the snapshot": `release()` dropped from `clear` (a timer or the watch survives).
Fails without: "lets go when the device is unpaired": the `onUnpaired` registration removed.
Fails without: "useIsPending is true only for the marked id, and false for an undefined one": the `s.id === id` comparison dropped or inverted (the unmarked id reads pending, or the marked one does not).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/store/pending.test.ts`
Expected: the file fails to load, `Failed to resolve import "./pending" from "src/store/pending.test.ts"`.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/store/pending.ts`:

```ts
// The session just created (spec Part 3 settlement 8). A 201 from POST /api/v1/sessions names the
// session before the snapshot lists it, so for PENDING_MS the views of that id draw "Loading…"
// rather than "This session is gone.". The wait ends sooner when the snapshot lists the session,
// and when the device is unpaired.
import { create } from "zustand";

import type { Snapshot } from "@/api/gen";
import { onUnpaired } from "@/api/request";
import { allSessions } from "@/fleet/groups";

import { useFleet } from "./fleet";

export const PENDING_MS = 5000;

export interface PendingState {
  id: number | null;
  mark: (id: number) => void;
  clear: () => void;
}

// timer ends the wait and unwatch closes the watch of the snapshot; both belong to the one id
// held, and live beside the store as the fleet's save timer does.
let timer: ReturnType<typeof setTimeout> | undefined;
let unwatch: (() => void) | undefined;

function listed(snapshot: Snapshot | null, id: number): boolean {
  return snapshot !== null && allSessions(snapshot).some((s) => s.id === id);
}

// release ends the wait for the id held: its timer and its watch of the snapshot.
function release(): void {
  clearTimeout(timer);
  timer = undefined;
  unwatch?.();
  unwatch = undefined;
}

export const usePending = create<PendingState>()((set, get) => ({
  id: null,

  // mark holds id for PENDING_MS, replacing any earlier id. It lets go at once for a session the
  // snapshot already lists, and later as soon as a snapshot does. The watch opens here, never at
  // module load: this module imports the store, and a subscription at the top level would run
  // while the store's own module may still be loading.
  mark(id) {
    release();
    if (listed(useFleet.getState().snapshot, id)) {
      set({ id: null });
      return;
    }
    set({ id });
    timer = setTimeout(() => {
      timer = undefined;
      get().clear();
    }, PENDING_MS);
    unwatch = useFleet.subscribe((state) => {
      if (listed(state.snapshot, id)) get().clear();
    });
  },

  clear() {
    release();
    set({ id: null });
  },
}));

// An unpairing ends the wait: the new session was the revoked device's.
onUnpaired(() => {
  usePending.getState().clear();
});

/** Whether the session with this id was created a moment ago and is not in the snapshot yet. */
export function useIsPending(id: number | undefined): boolean {
  return usePending((s) => id !== undefined && s.id === id);
}
```

Prettier formats the long `apply("sessions", …)` call of the test; run it in Step 4.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec prettier --write src/store/pending.ts src/store/pending.test.ts && pnpm -C internal/web/ui exec vitest run src/store/pending.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
Expected: `Tests  8 passed (8)`, then the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/store/pending.ts internal/web/ui/src/store/pending.test.ts
git commit -m "feat(web): the pending session slice" -- internal/web/ui/src/store/pending.ts internal/web/ui/src/store/pending.test.ts
```

### Task 4.2: The store keeps the recent notices

**Files:**
- Modify: `internal/web/ui/src/store/fleet.ts:6-8,22,31-32,95-101,117-119,274-292`
- Test: `internal/web/ui/src/store/fleet.test.ts`
- Test: `internal/web/ui/src/store/startFleet.test.ts`

Today `applied`'s `notice` case appends a stream notice to `snapshot.notices`, and `afterEvent` also hands it to the `onNotices` listeners. A response's notices reach those listeners too (`request.ts`) but never the snapshot. The fix keeps each notice once: `applied`'s case no longer appends (it stamps the time, as a ping does), and `startFleet` registers one `onNotices` listener that appends everything delivered, through a new `FleetState.addNotices`.

- [ ] **Step 1: Write the failing tests**

In `internal/web/ui/src/store/fleet.test.ts`, change the import of the store. Replace:

```ts
import { useFleet, useFleetGroups, useLink, useSession } from "./fleet";
```

with:

```ts
import { MAX_NOTICES, useFleet, useFleetGroups, useLink, useSession } from "./fleet";
```

Replace the test:

```ts
  it("keeps the last 20 notices", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    for (let i = 0; i < 25; i++) store().apply("notice", notice(`n${i}`));
    const texts = (store().snapshot?.notices ?? []).map((n) => n.text);
    expect(texts).toHaveLength(20);
    expect(texts[0]).toBe("n5");
    expect(texts[19]).toBe("n24");
  });
```

with:

```ts
  it("keeps the last MAX_NOTICES notices it is given, the oldest dropped", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    for (let i = 0; i < MAX_NOTICES + 5; i++) store().addNotices([notice(`n${i}`)]);
    const texts = (store().snapshot?.notices ?? []).map((n) => n.text);
    expect(MAX_NOTICES).toBe(20);
    expect(texts).toHaveLength(MAX_NOTICES);
    expect(texts[0]).toBe("n5");
    expect(texts.at(-1)).toBe(`n${MAX_NOTICES + 4}`);
  });

  it("appends a batch in order after the notices already held", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    const held = store().snapshot?.notices ?? [];
    store().addNotices([notice("a"), notice("b")]);
    expect(store().snapshot?.notices).toEqual([...held, notice("a"), notice("b")]);
  });

  it("keeps no notice while there is no snapshot", () => {
    store().addNotices([notice("a")]);
    expect(store().snapshot).toBeNull();
  });

  it("does not stamp the time for notices it is given: they are not news from the daemon", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    vi.advanceTimersByTime(7000);
    store().addNotices([notice("a")]);
    expect(store().updated).toBe(NOW);
  });

  it("keeps nothing for a stream notice event and stamps the time: the onNotices listener keeps it", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    const held = store().snapshot?.notices;
    vi.advanceTimersByTime(7000);
    store().apply("notice", notice("x"));
    expect(store().snapshot?.notices).toEqual(held);
    expect(store().updated).toBe(NOW + 7000);
  });
```

In `internal/web/ui/src/store/startFleet.test.ts`, replace the import line:

```ts
import { emitStopping, emitUnpaired, onNotices } from "@/api/request";
```

with:

```ts
import { emitNotices, emitStopping, emitUnpaired, onNotices } from "@/api/request";
```

Add these tests after the test `hands each stream notice to the onNotices listeners and keeps it` (which stays as it is: it now passes through the listener):

```ts
  it("keeps a response's notices, which only the onNotices listeners hear", () => {
    start();
    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    const held = store().snapshot?.notices?.length ?? 0;
    const n = notice("Pushed to origin");
    emitNotices([n]);
    expect(store().snapshot?.notices).toHaveLength(held + 1);
    expect(store().snapshot?.notices?.at(-1)).toEqual(n);
  });

  it("keeps a stream notice exactly once", () => {
    start();
    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    handlers.onEvent("notice", notice("stash kept: def456"));
    const kept = (store().snapshot?.notices ?? []).filter((n) => n.text === "stash kept: def456");
    expect(kept).toHaveLength(1);
  });

  it("keeps no notice before the first snapshot, and none once the start is stopped", () => {
    const stop = start();
    emitNotices([notice("early")]);
    expect(store().snapshot).toBeNull();

    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    stop();
    const held = store().snapshot?.notices?.length ?? 0;
    emitNotices([notice("late")]);
    expect(store().snapshot?.notices).toHaveLength(held);
  });
```

In the same file, the test `checks the push subscription once per launch, with the first snapshot's device` sends a second snapshot whose `device.push` differs from the first's, so that a second check would show. The fixture's own `device.push` is `"expired"` (Story 1), so that value no longer differs. Replace:

```ts
      device: { ...first.device, push: "expired" },
```

with:

```ts
      device: { ...first.device, push: "failing: 410 Gone" },
```

Fails without: "keeps the last MAX_NOTICES notices it is given, the oldest dropped": the `.slice(-MAX_NOTICES)` in `addNotices` removed (25 notices are kept), or `MAX_NOTICES` not exported.
Fails without: "appends a batch in order after the notices already held": the spread of `current.notices` in `addNotices` removed (the held notices are lost).
Fails without: "keeps no notice while there is no snapshot": the `current === null` guard in `addNotices` removed (it throws on `...current`).
Fails without: "does not stamp the time for notices it is given": an `updated: Date.now()` added to `addNotices`.
Fails without: "keeps nothing for a stream notice event and stamps the time": the old append restored in `applied`'s `notice` case (the notice is kept there as well as by the listener), or the `{ updated: now }` removed (the time is not stamped).
Fails without: "keeps a response's notices, which only the onNotices listeners hear": the `onNotices` registration in `startFleet` removed.
Fails without: "keeps a stream notice exactly once": the old append restored in `applied`'s `notice` case (the stream notice is kept twice).
Fails without: "keeps no notice before the first snapshot, and none once the start is stopped": `offNotices()` removed from `stop` (the late notice is kept), or the `snapshot === null` guard in `addNotices` removed.
Fails without: "checks the push subscription once per launch, with the first snapshot's device": `checkPushSubscription` run for every snapshot rather than once (the second snapshot's `push` value, which the fixture's device lacks, makes the second call differ and the count 2).

- [ ] **Step 2: Run them to see them fail**

Run: `pnpm -C internal/web/ui exec vitest run src/store/fleet.test.ts src/store/startFleet.test.ts`
Expected: the fleet tests fail with `store(...).addNotices is not a function`, and `keeps a response's notices` fails with an assertion on the held length (the listener does not exist).

- [ ] **Step 3: Implement**

In `internal/web/ui/src/store/fleet.ts`, change the imports. Replace:

```ts
import type { Session, Snapshot, StreamEvents } from "@/api/gen";
import { queryClient } from "@/api/queries";
import { emitNotices, onStopping, onUnpaired } from "@/api/request";
```

with:

```ts
import type { Notice, Session, Snapshot, StreamEvents } from "@/api/gen";
import { queryClient } from "@/api/queries";
import { emitNotices, onNotices, onStopping, onUnpaired } from "@/api/request";
```

In `FleetState`, replace:

```ts
  setLink: (state: LinkState) => void;
```

with:

```ts
  setLink: (state: LinkState) => void;
  addNotices: (notices: Notice[]) => void;
```

Replace the constant:

```ts
// The snapshot keeps the last 20 notices.
const RECENT_NOTICES = 20;
```

with:

```ts
// The snapshot keeps the last 20 notices, those of responses and of the stream alike.
export const MAX_NOTICES = 20;
```

Replace the `notice` case of `applied`:

```ts
    case "notice": {
      const notice = event.data;
      return change((s) => ({
        ...s,
        notices: [...(s.notices ?? []), notice].slice(-RECENT_NOTICES),
      }));
    }
```

with:

```ts
    case "notice":
      // Kept by the onNotices listener startFleet registers (addNotices), which hears this
      // notice from afterEvent and a response's notices too: appending here would keep it twice.
      return { updated: now };
```

In the store, after `setLink`, replace:

```ts
  setLink(state) {
    set({ link: state, stale: state !== "live" });
  },
```

with:

```ts
  setLink(state) {
    set({ link: state, stale: state !== "live" });
  },

  // Keeps notices among the recent ones the snapshot carries (the daemon keeps a response's for
  // the next snapshot, which replaces this list). It does not stamp `updated`: a response's
  // notices are not news from the daemon.
  addNotices(notices) {
    const current = get().snapshot;
    if (current === null || notices.length === 0) return;
    set({
      snapshot: {
        ...current,
        notices: [...(current.notices ?? []), ...notices].slice(-MAX_NOTICES),
      },
    });
  },
```

In `startFleet`, after the `offStopping` registration (the block that ends with `}, RESTART_GRACE);\n  });`) and before `const offUnpaired = onUnpaired(() => {`, insert:

```ts
  // Every notice delivered, a response's and each stream notice (afterEvent hands those over),
  // is kept in the snapshot, and saved with it.
  const offNotices = onNotices((notices) => {
    useFleet.getState().addNotices(notices);
    scheduleSave();
  });

```

In `stop`, replace:

```ts
    offUnpaired();
    offStopping();
```

with:

```ts
    offUnpaired();
    offStopping();
    offNotices();
```

- [ ] **Step 4: Run them to see them pass**

Run: `pnpm -C internal/web/ui exec prettier --write src/store/fleet.ts src/store/fleet.test.ts src/store/startFleet.test.ts && pnpm -C internal/web/ui exec vitest run src/store && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
Expected: every store test file passes (`Test Files  … passed`), the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the store keeps the notices of responses and of the stream, once each" -- internal/web/ui/src/store/fleet.ts internal/web/ui/src/store/fleet.test.ts internal/web/ui/src/store/startFleet.test.ts
```

### Task 4.3: `launchBody` and `session.create`

**Files:**
- Create: `internal/web/ui/src/commands/launch.ts`
- Create: `internal/web/ui/src/commands/launch.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/commands/launch.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import type { Created, LaunchDefaults, Outcome } from "@/api/gen";
import { api } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { usePending } from "@/store/pending";

import { launchBody, type LaunchForm } from "./launch";
import { command, context, makeSession, resetFleet, seed, spyOnAdd } from "./testkit";

let add: ReturnType<typeof spyOnAdd>;
let assign: MockInstance<(url: string | URL) => void>;

beforeEach(() => {
  add = spyOnAdd();
  assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
  usePending.getState().clear();
  resetFleet();
});

const DEFAULTS: LaunchDefaults = {
  account: "",
  accounts: ["work"],
  model: "opus",
  models: ["opus", "sonnet"],
  permission_mode: "auto",
  permission_modes: ["auto", "plan"],
  effort: "high",
  efforts: ["high", "low"],
};

function form(over: Partial<LaunchForm> = {}): LaunchForm {
  return {
    workspace: 1,
    title: "fix the build",
    prompt: "go",
    picks: {
      account: DEFAULTS.account,
      model: DEFAULTS.model,
      permission_mode: DEFAULTS.permission_mode,
      effort: DEFAULTS.effort,
    },
    ...over,
  };
}

const NEW = context(undefined, { panel: "new" });

function created(id: number): Outcome<Created> {
  return { kind: "ok", status: 201, value: { id, notices: null }, notices: [] };
}

describe("launchBody", () => {
  it("trims the title, and the end of the prompt only", () => {
    const body = launchBody(form({ title: "  fix the build \n", prompt: "  go on \n\n" }), DEFAULTS);
    expect(body.title).toBe("fix the build");
    expect(body.prompt).toBe("  go on");
    expect(body.workspace).toBe(1);
  });

  it("sends no pick that equals its default", () => {
    expect(launchBody(form(), DEFAULTS)).toStrictEqual({
      workspace: 1,
      title: "fix the build",
      prompt: "go",
    });
  });

  it("sends each pick that differs from its default, and only those", () => {
    const picks = { account: "work", model: "sonnet", permission_mode: "auto", effort: "low" };
    expect(launchBody(form({ picks }), DEFAULTS)).toStrictEqual({
      workspace: 1,
      title: "fix the build",
      prompt: "go",
      account: "work",
      model: "sonnet",
      effort: "low",
    });
  });

  it("sends an empty pick when the default is not empty: the empty one is a choice too", () => {
    const picks = { account: "", model: "", permission_mode: "auto", effort: "high" };
    expect(launchBody(form({ picks }), DEFAULTS)).toStrictEqual({
      workspace: 1,
      title: "fix the build",
      prompt: "go",
      model: "",
    });
  });
});

describe("session.create", () => {
  it("is on in the new-session view, and off elsewhere and while the snapshot is stale", () => {
    const create = command("session.create");
    expect(commandEnabled(create, NEW)).toBe(true);
    expect(commandEnabled(create, context(undefined, { panel: "fleet" }))).toBe(false);
    expect(
      commandEnabled(create, context(undefined, { panel: "new", stale: true, link: "offline" })),
    ).toBe(false);
  });

  it("sends the body, marks the new session pending and opens it on a 201", async () => {
    const body = launchBody(form(), DEFAULTS);
    const create = vi.spyOn(api, "create").mockResolvedValue(created(42));

    await runCommand(command("session.create"), NEW, { body });

    expect(create).toHaveBeenCalledOnce();
    expect(create).toHaveBeenCalledWith(body);
    expect(usePending.getState().id).toBe(42);
    expect(assign).toHaveBeenCalledOnce();
    expect(assign).toHaveBeenCalledWith("#/s/42");
  });

  it("does not hold a session pending when the snapshot already lists it", async () => {
    seed([makeSession({ id: 42 })]);
    vi.spyOn(api, "create").mockResolvedValue(created(42));

    await runCommand(command("session.create"), NEW, { body: launchBody(form(), DEFAULTS) });

    expect(usePending.getState().id).toBeNull();
    expect(assign).toHaveBeenCalledWith("#/s/42");
  });

  it("toasts that the session is still starting and opens the fleet on a 202", async () => {
    vi.spyOn(api, "create").mockResolvedValue({ kind: "pending" });

    await runCommand(command("session.create"), NEW, { body: launchBody(form(), DEFAULTS) });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Still starting; the session will show in the fleet.",
        type: "info",
      }),
    );
    expect(assign).toHaveBeenCalledOnce();
    expect(assign).toHaveBeenCalledWith("#/");
    expect(usePending.getState().id).toBeNull();
  });

  it.each([
    [400, "title: a session needs a title"],
    [409, "repo already has a session titled \"fix the build\""],
  ])("toasts the daemon's words and goes nowhere when it refuses with %i", async (status, message) => {
    vi.spyOn(api, "create").mockResolvedValue({ kind: "refused", status, message });

    await runCommand(command("session.create"), NEW, { body: launchBody(form(), DEFAULTS) });

    expect(add).toHaveBeenCalledWith(expect.objectContaining({ description: message, type: "error" }));
    expect(assign).not.toHaveBeenCalled();
    expect(usePending.getState().id).toBeNull();
  });

  it("toasts the failure's words and goes nowhere when the request fails", async () => {
    vi.spyOn(api, "create").mockResolvedValue({
      kind: "failed",
      status: 0,
      message: "Can't reach loom.",
    });

    await runCommand(command("session.create"), NEW, { body: launchBody(form(), DEFAULTS) });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "Can't reach loom.", type: "error" }),
    );
    expect(assign).not.toHaveBeenCalled();
  });

  it("runs nothing while the snapshot is stale", async () => {
    const create = vi.spyOn(api, "create").mockResolvedValue(created(42));

    await runCommand(command("session.create"), context(undefined, { panel: "new", stale: true }), {
      body: launchBody(form(), DEFAULTS),
    });

    expect(create).not.toHaveBeenCalled();
  });
});
```

Fails without: "trims the title, and the end of the prompt only": `.trim()` dropped from the title, or `.trimEnd()` replaced by `.trim()` (the prompt's start is lost).
Fails without: "sends no pick that equals its default": the `!==` test in `launchBody` inverted or removed (every pick is sent).
Fails without: "sends each pick that differs from its default, and only those": a key missing from `PICKS` (that pick is never sent).
Fails without: "sends an empty pick when the default is not empty": the loop skipping an empty pick (`model: ""` is dropped).
Fails without: "is on in the new-session view, and off elsewhere and while the snapshot is stale": `when` returning true for every panel, or the command marked `offline`.
Fails without: "sends the body, marks the new session pending and opens it on a 201": the `mark` call or the `assign` call removed.
Fails without: "does not hold a session pending when the snapshot already lists it": `mark`'s check of the current snapshot removed (Task 4.1): the id is held.
Fails without: "toasts that the session is still starting and opens the fleet on a 202": the `pending` branch of `session.create` removed (`showOutcome` says "Start is still running…").
Fails without: "toasts the daemon's words and goes nowhere when it refuses with %i": the `showOutcome` fall-through removed, or a refusal treated as a success (`assign` is called).
Fails without: "toasts the failure's words and goes nowhere when the request fails": the same fall-through.
Fails without: "runs nothing while the snapshot is stale": the command marked `offline`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/commands/launch.test.ts`
Expected: the file fails to load, `Failed to resolve import "./launch" from "src/commands/launch.test.ts"`.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/commands/launch.ts`:

```ts
// The new-session command (spec §23, Part 3 settlement 7): the body of POST /api/v1/sessions as
// the classic app builds it (newsession.mjs), and what the daemon's answer does. The daemon's
// checks stay the authority: its 400 and 409 words are the toast, and a refused create leaves
// the form as typed.
import type { CreateBody, LaunchDefaults } from "@/api/gen";
import { api } from "@/api/request";
import { defineCommand } from "@/app/commands";
import { hrefFor } from "@/app/routes";
import { showMessage, showOutcome } from "@/app/toasts";
import { usePending } from "@/store/pending";

// LaunchPicks are the four launch options the form picks from; "" is the "(default)" of a list.
export interface LaunchPicks {
  account: string;
  model: string;
  permission_mode: string;
  effort: string;
}

// LaunchForm is what the new-session form holds when Start session is pressed.
export interface LaunchForm {
  workspace: number;
  title: string;
  prompt: string;
  picks: LaunchPicks;
}

const PICKS = ["account", "model", "permission_mode", "effort"] as const;

// launchBody builds the POST /api/v1/sessions body: the workspace, the trimmed title, the prompt
// trimmed at its end only (so does the daemon), and each launch option that differs from its
// default.
export function launchBody(form: LaunchForm, defaults: LaunchDefaults): CreateBody {
  const body: CreateBody = {
    workspace: form.workspace,
    title: form.title.trim(),
    prompt: form.prompt.trimEnd(),
  };
  for (const key of PICKS) {
    if (form.picks[key] !== defaults[key]) body[key] = form.picks[key];
  }
  return body;
}

defineCommand<{ body: CreateBody }>({
  id: "session.create",
  title: "Start session",
  when: (ctx) => ctx.panel === "new",
  run: async (_ctx, args) => {
    const outcome = await api.create(args.body);
    // A 201 names the session before the snapshot lists it: its views say "Loading…" meanwhile.
    if (outcome.kind === "ok" && outcome.status === 201) {
      const id = outcome.value.id;
      usePending.getState().mark(id);
      window.location.assign(hrefFor("turns", { id: String(id) }));
      return;
    }
    // A 202: the start outlived the wait, and its outcome comes on the stream.
    if (outcome.kind === "pending") {
      showMessage("Still starting; the session will show in the fleet.");
      window.location.assign(hrefFor("fleet"));
      return;
    }
    showOutcome(outcome, "That");
  },
});
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec prettier --write src/commands/launch.ts src/commands/launch.test.ts && pnpm -C internal/web/ui exec vitest run src/commands/launch.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
Expected: `Tests  12 passed (12)` (the two `it.each` rows count as two), the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/commands/launch.ts internal/web/ui/src/commands/launch.test.ts
git commit -m "feat(web): launchBody and the session.create command" -- internal/web/ui/src/commands/launch.ts internal/web/ui/src/commands/launch.test.ts
```

### Task 4.4: The device commands

**Files:**
- Create: `internal/web/ui/src/commands/device.ts`
- Create: `internal/web/ui/src/commands/device.test.ts`

`device.update`, `quickReplies.save` and `device.unpair` are for the settings view (`when` the panel is `settings`) and, like every command but one, are off while the snapshot is stale. `device.pair` is `offline` and runs on the `pair` route.

A write's answer is applied to the store as the stream's event would be, unless that event has landed while the write was in flight: the answer was then read before the stream's value, so the stream's holds. The test of "landed" is the identity of the snapshot's `device` (or `quick_replies`) value: any change to the held value since the request was sent wins, whether an applied stream event, a new snapshot, or the answer of another write that was in flight with this one (a write of a different field runs together with it). The stream's `device` event that follows every write brings the daemon's own value.

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/commands/device.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import type { Device, Outcome, Pair, QuickReplies } from "@/api/gen";
import { api, onUnpaired } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { askConfirm } from "@/app/ConfirmSheet";
import { startFleet, useFleet } from "@/store/fleet";

import "./device";
import "./launch";
import {
  askedSheet,
  command,
  context,
  deferred,
  makeSession,
  resetFleet,
  seed,
  spyOnAdd,
} from "./testkit";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

vi.mock("@/store/fleet", async (original) => ({
  ...(await original<typeof import("@/store/fleet")>()),
  startFleet: vi.fn(() => () => undefined),
}));

let add: ReturnType<typeof spyOnAdd>;
let assign: MockInstance<(url: string | URL) => void>;

beforeEach(() => {
  add = spyOnAdd();
  assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);
  seed([makeSession()]);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  vi.mocked(startFleet).mockClear();
  resetFleet();
});

const SETTINGS = context(undefined, { panel: "settings" });
const PAIR_OFFLINE = context(undefined, { panel: "pair", stale: true, link: "offline" });

function ok<T>(value: T, status = 200): Outcome<T> {
  return { kind: "ok", status, value, notices: [] };
}

const ANSWERED: Device = {
  name: "iPhone",
  notify: { waiting: true, finished: false, paused_until: "2026-11-20T13:00:00Z" },
  push: "",
};
const STREAMED: Device = { ...ANSWERED, notify: { ...ANSWERED.notify, waiting: false } };
const device = () => useFleet.getState().snapshot?.device;
const replies = () => useFleet.getState().snapshot?.quick_replies;

describe("device.update", () => {
  it("is on in the settings view, and off elsewhere and while the snapshot is stale", () => {
    const update = command("device.update");
    expect(commandEnabled(update, SETTINGS)).toBe(true);
    expect(commandEnabled(update, context(undefined, { panel: "turns" }))).toBe(false);
    expect(
      commandEnabled(update, context(undefined, { panel: "settings", stale: true, link: "offline" })),
    ).toBe(false);
  });

  it("sends the body as given and applies the answered device to the store", async () => {
    const put = vi.spyOn(api, "putDevice").mockResolvedValue(ok(ANSWERED));

    await runCommand(command("device.update"), SETTINGS, { waiting: true, pause: "1h" });

    expect(put).toHaveBeenCalledOnce();
    expect(put).toHaveBeenCalledWith({ waiting: true, pause: "1h" });
    expect(device()).toEqual(ANSWERED);
  });

  it("keeps the stream's device when its event was applied while the write was in flight", async () => {
    vi.spyOn(api, "putDevice").mockImplementation(() => {
      useFleet.getState().apply("device", STREAMED);
      return Promise.resolve(ok(ANSWERED));
    });

    await runCommand(command("device.update"), SETTINGS, { waiting: true });

    expect(device()).toBe(STREAMED);
  });

  it("does not apply the second answer of two writes in flight together, and the stream's event then brings its value", async () => {
    const FIRST: Device = { ...ANSWERED, notify: { ...ANSWERED.notify, waiting: false } };
    const SECOND: Device = { ...ANSWERED, notify: { ...ANSWERED.notify, finished: true } };
    const a = deferred<Outcome<Device>>();
    const b = deferred<Outcome<Device>>();
    vi.spyOn(api, "putDevice").mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
    const update = command("device.update");

    const runA = runCommand(update, SETTINGS, { waiting: false });
    const runB = runCommand(update, SETTINGS, { finished: true });
    a.resolve(ok(FIRST));
    await runA;
    expect(device()).toBe(FIRST);
    b.resolve(ok(SECOND));
    await runB;

    expect(device()).toBe(FIRST);
    useFleet.getState().apply("device", STREAMED);
    expect(device()).toBe(STREAMED);
  });

  it("toasts the daemon's words and leaves the device when the write is refused", async () => {
    const before = device();
    vi.spyOn(api, "putDevice").mockResolvedValue({
      kind: "refused",
      status: 400,
      message: 'pause: want "1h" or "none"',
    });

    await runCommand(command("device.update"), SETTINGS, { pause: "later" });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: 'pause: want "1h" or "none"', type: "error" }),
    );
    expect(device()).toBe(before);
  });

  it("runs writes of different fields together, and holds a second write of the same field whatever its value", async () => {
    const first = deferred<Outcome<Device>>();
    const put = vi.spyOn(api, "putDevice").mockReturnValue(first.promise);
    const update = command("device.update");

    const a = runCommand(update, SETTINGS, { waiting: true });
    const b = runCommand(update, SETTINGS, { finished: true });
    const c = runCommand(update, SETTINGS, { waiting: false });
    expect(put).toHaveBeenCalledTimes(2);
    expect(put).toHaveBeenNthCalledWith(1, { waiting: true });
    expect(put).toHaveBeenNthCalledWith(2, { finished: true });

    first.resolve(ok(ANSWERED));
    await Promise.all([a, b, c]);
  });
});

describe("quickReplies.save", () => {
  const ANSWER: QuickReplies = { quick_replies: ["ok", "later"] };

  it("is on in the settings view, and off elsewhere and while the snapshot is stale", () => {
    const save = command("quickReplies.save");
    expect(commandEnabled(save, SETTINGS)).toBe(true);
    expect(commandEnabled(save, context(undefined, { panel: "info" }))).toBe(false);
    expect(
      commandEnabled(save, context(undefined, { panel: "settings", stale: true, link: "offline" })),
    ).toBe(false);
  });

  it("sends the list, applies the answer to the store and calls onSaved", async () => {
    const put = vi.spyOn(api, "putQuickReplies").mockResolvedValue(ok(ANSWER));
    const onSaved = vi.fn();

    await runCommand(command("quickReplies.save"), SETTINGS, { replies: ["ok", "later"], onSaved });

    expect(put).toHaveBeenCalledWith({ replies: ["ok", "later"] });
    expect(replies()).toEqual(["ok", "later"]);
    expect(onSaved).toHaveBeenCalledOnce();
  });

  it("sends no list to reset to the defaults, and applies the defaults the daemon answers", async () => {
    const put = vi
      .spyOn(api, "putQuickReplies")
      .mockResolvedValue(ok({ quick_replies: ["yes", "continue"] }));

    await runCommand(command("quickReplies.save"), SETTINGS, { replies: null });

    expect(put.mock.calls[0]?.[0]).toStrictEqual({});
    expect(replies()).toEqual(["yes", "continue"]);
  });

  it("keeps the stream's list when its event was applied while the write was in flight, and still calls onSaved", async () => {
    vi.spyOn(api, "putQuickReplies").mockImplementation(() => {
      useFleet.getState().apply("quick_replies", { quick_replies: ["from the stream"] });
      return Promise.resolve(ok(ANSWER));
    });
    const onSaved = vi.fn();

    await runCommand(command("quickReplies.save"), SETTINGS, { replies: ["ok"], onSaved });

    expect(replies()).toEqual(["from the stream"]);
    expect(onSaved).toHaveBeenCalledOnce();
  });

  it("toasts the daemon's words, leaves the list and does not call onSaved when it is refused", async () => {
    const before = replies();
    vi.spyOn(api, "putQuickReplies").mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "at most 12 quick replies",
    });
    const onSaved = vi.fn();

    await runCommand(command("quickReplies.save"), SETTINGS, { replies: ["a"], onSaved });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "at most 12 quick replies", type: "error" }),
    );
    expect(replies()).toBe(before);
    expect(onSaved).not.toHaveBeenCalled();
  });

  it("holds a second save, or a reset, while one is in flight", async () => {
    const first = deferred<Outcome<QuickReplies>>();
    const put = vi.spyOn(api, "putQuickReplies").mockReturnValue(first.promise);
    const save = command("quickReplies.save");

    const a = runCommand(save, SETTINGS, { replies: ["a"] });
    const b = runCommand(save, SETTINGS, { replies: null });
    expect(put).toHaveBeenCalledOnce();

    first.resolve(ok(ANSWER));
    await Promise.all([a, b]);
  });
});

describe("device.unpair", () => {
  it("is on in the settings view, and off elsewhere and while the snapshot is stale", () => {
    const unpair = command("device.unpair");
    expect(commandEnabled(unpair, SETTINGS)).toBe(true);
    expect(commandEnabled(unpair, context(undefined, { panel: "fleet" }))).toBe(false);
    expect(
      commandEnabled(unpair, context(undefined, { panel: "settings", stale: true, link: "offline" })),
    ).toBe(false);
  });

  it("asks with the classic words, and on a yes and a 204 runs the unpaired path", async () => {
    vi.mocked(askConfirm).mockResolvedValue("");
    const del = vi.spyOn(api, "deleteDevice").mockResolvedValue(ok<void>(undefined, 204));
    const unpaired = vi.fn();
    const off = onUnpaired(unpaired);

    await runCommand(command("device.unpair"), SETTINGS, undefined);
    off();

    expect(askedSheet()).toEqual({
      title: "Unpair this phone?",
      body: "This phone stops getting notifications and goes back to pairing. Pair it again with loom web pair on the host.",
      action: "Unpair",
      destructive: true,
    });
    expect(del).toHaveBeenCalledOnce();
    expect(unpaired).toHaveBeenCalledOnce();
  });

  it("sends nothing when the sheet is cancelled", async () => {
    vi.mocked(askConfirm).mockResolvedValue(null);
    const del = vi.spyOn(api, "deleteDevice").mockResolvedValue(ok<void>(undefined, 204));
    const unpaired = vi.fn();
    const off = onUnpaired(unpaired);

    await runCommand(command("device.unpair"), SETTINGS, undefined);
    off();

    expect(del).not.toHaveBeenCalled();
    expect(unpaired).not.toHaveBeenCalled();
  });

  it("toasts the daemon's words and does not unpair when the request is refused", async () => {
    vi.mocked(askConfirm).mockResolvedValue("");
    vi.spyOn(api, "deleteDevice").mockResolvedValue({
      kind: "failed",
      status: 500,
      message: "That didn't work.",
    });
    const unpaired = vi.fn();
    const off = onUnpaired(unpaired);

    await runCommand(command("device.unpair"), SETTINGS, undefined);
    off();

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "That didn't work.", type: "error" }),
    );
    expect(unpaired).not.toHaveBeenCalled();
  });
});

describe("device.pair", () => {
  it("is on the pairing route, offline, and nowhere else", () => {
    const pair = command("device.pair");
    expect(commandEnabled(pair, PAIR_OFFLINE)).toBe(true);
    expect(commandEnabled(pair, context(undefined, { panel: "settings" }))).toBe(false);
    expect(commandEnabled(pair, context(undefined, { panel: "fleet" }))).toBe(false);
  });

  it.each([
    ["session.create", "new"],
    ["device.update", "settings"],
    ["quickReplies.save", "settings"],
    ["device.unpair", "settings"],
  ] as const)("%s, unlike device.pair, is off on its own route while the snapshot is stale", (id, panel) => {
    expect(commandEnabled(command(id), context(undefined, { panel }))).toBe(true);
    expect(
      commandEnabled(command(id), context(undefined, { panel, stale: true, link: "offline" })),
    ).toBe(false);
  });

  it("pairs with the code while the snapshot is stale, starts the fleet and opens it", async () => {
    const calls: string[] = [];
    vi.mocked(startFleet).mockImplementation(() => {
      calls.push("startFleet");
      return () => undefined;
    });
    assign.mockImplementation((url: string | URL) => {
      calls.push(`assign ${String(url)}`);
    });
    const pair = vi.spyOn(api, "pair").mockResolvedValue(ok<Pair>({ device: "iPhone" }));

    await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

    expect(pair).toHaveBeenCalledOnce();
    expect(pair).toHaveBeenCalledWith({ code: "K7QM2XPA" });
    expect(calls).toEqual(["startFleet", "assign #/"]);
  });

  it.each([
    [400, "that code is not right"],
    [429, "too many wrong codes; run loom web pair again"],
    [409, "a device named iPhone is already paired"],
  ])("toasts the daemon's words and starts nothing when it refuses with %i", async (status, message) => {
    vi.spyOn(api, "pair").mockResolvedValue({ kind: "refused", status, message });

    await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

    expect(add).toHaveBeenCalledWith(expect.objectContaining({ description: message, type: "error" }));
    expect(startFleet).not.toHaveBeenCalled();
    expect(assign).not.toHaveBeenCalled();
  });

  // request.ts answers a 503 as `stopping` and a 504 as `timeout`, never as `refused`.
  it.each([
    ["stopping", "the web is off"],
    ["timeout", "loom took too long to answer"],
  ] as const)("toasts the daemon's words and starts nothing for a %s answer", async (kind, message) => {
    vi.spyOn(api, "pair").mockResolvedValue({ kind, message });

    await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

    expect(add).toHaveBeenCalledWith(expect.objectContaining({ description: message, type: "error" }));
    expect(startFleet).not.toHaveBeenCalled();
    expect(assign).not.toHaveBeenCalled();
  });

  it("toasts the daemon's text for a pairing that failed on its side", async () => {
    vi.spyOn(api, "pair").mockResolvedValue({
      kind: "failed",
      status: 500,
      message: "pairing failed; the daemon's log says why",
    });

    await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "pairing failed; the daemon's log says why",
        type: "error",
      }),
    );
    expect(startFleet).not.toHaveBeenCalled();
  });

  it("says it cannot reach loom, in the classic words, when the request never got an answer", async () => {
    vi.spyOn(api, "pair").mockResolvedValue({
      kind: "failed",
      status: 0,
      message: "Can't reach loom.",
    });

    await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Can't reach loom. Check the connection and try again.",
        type: "error",
      }),
    );
    expect(startFleet).not.toHaveBeenCalled();
    expect(assign).not.toHaveBeenCalled();
  });
});
```

Fails without: "device.update is on in the settings view, and off elsewhere and while the snapshot is stale": `when` returning true for every panel, or the command marked `offline`.
Fails without: "sends the body as given and applies the answered device to the store": the `apply("device", …)` call removed (the store keeps the old device).
Fails without: "keeps the stream's device when its event was applied while the write was in flight": the identity check (`=== before`) removed (the answer overwrites the stream's device).
Fails without: "does not apply the second answer of two writes in flight together, and the stream's event then brings its value": the identity check (`=== before`) removed from `device.update` (the second answer overwrites the first's device).
Fails without: "toasts the daemon's words and leaves the device when the write is refused": the `showOutcome` call removed, or the answer applied whatever the outcome.
Fails without: "runs writes of different fields together, and holds a second write of the same field whatever its value": the `key` removed (the second field's write is held: two calls become one) or made to include the values (the third write is sent: three calls).
Fails without: "quickReplies.save is on in the settings view, …": `when` returning true for every panel.
Fails without: "sends the list, applies the answer to the store and calls onSaved": the `apply("quick_replies", …)` call or the `onSaved` call removed.
Fails without: "sends no list to reset to the defaults, …": `replies: null` sent as `{ replies: null }` (the strict `{}` fails) or the answer not applied.
Fails without: "keeps the stream's list when its event was applied while the write was in flight, and still calls onSaved": the identity check removed (the answer overwrites the stream's list), or `onSaved` moved into the applying branch.
Fails without: "toasts the daemon's words, leaves the list and does not call onSaved when it is refused": the early `return` after `showOutcome` removed.
Fails without: "holds a second save, or a reset, while one is in flight": a `key` added to the command (the reset's key differs, so two requests are sent).
Fails without: "device.unpair is on in the settings view, …": `when` returning true for every panel.
Fails without: "asks with the classic words, and on a yes and a 204 runs the unpaired path": the `confirm` words changed, or `emitUnpaired()` removed from the 204 branch.
Fails without: "sends nothing when the sheet is cancelled": the `confirm` removed (the request is sent without a sheet).
Fails without: "toasts the daemon's words and does not unpair when the request is refused": `emitUnpaired()` called for every outcome rather than only a success.
Fails without: "device.pair is on the pairing route, offline, and nowhere else": the command not marked `offline`, or its `when` not reading the panel.
Fails without: "%s, unlike device.pair, is off on its own route while the snapshot is stale": any of the four commands marked `offline`, or its `when` not true on its own route.
Fails without: "pairs with the code while the snapshot is stale, starts the fleet and opens it": `startFleet()` or the `assign` removed, or the two swapped (the order check fails).
Fails without: "toasts the daemon's words and starts nothing when it refuses with %i": the `refused` case removed from `device.pair`'s switch (no toast is shown).
Fails without: "toasts the daemon's words and starts nothing for a %s answer": the `stopping` case, or the `timeout` case, removed from `device.pair`'s switch (that row shows no toast).
Fails without: "toasts the daemon's text for a pairing that failed on its side": the `failed` branch showing the classic unreachable words for every status.
Fails without: "says it cannot reach loom, in the classic words, when the request never got an answer": the status-0 branch removed (the toast reads "Can't reach loom." without the second sentence).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/commands/device.test.ts`
Expected: the file fails to load, `Failed to resolve import "./device" from "src/commands/device.test.ts"`.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/commands/device.ts`:

```ts
// The phone's own commands (spec §23, Part 3 settlements 9, 10, 13 and 14): its notification
// settings, the quick replies every paired phone offers, unpairing, and pairing. The words are the
// classic app's (settings.mjs, pair.mjs). A write's answer is applied to the store as the stream's
// event would be, so it shows at once; when the held value changed while the write was in flight
// (the stream's event, a new snapshot, another write's answer) that value holds, since this answer
// was read before it, and the stream's event that follows every write brings the daemon's.
import type { DeviceBody } from "@/api/gen";
import { api, emitUnpaired } from "@/api/request";
import { defineCommand, type CommandContext } from "@/app/commands";
import { hrefFor } from "@/app/routes";
import { showMessage, showOutcome } from "@/app/toasts";
import { startFleet, useFleet } from "@/store/fleet";

// The fallback of showOutcome for a refusal with no text: the classic app's "That didn't work.".
const WHAT = "That";

const CANT_REACH = "Can't reach loom. Check the connection and try again.";

// onSettings is the `when` of the settings view's commands.
function onSettings(ctx: CommandContext): boolean {
  return ctx.panel === "settings";
}

// setFields are the names of the fields a device body sets, in order: what keys the in-flight
// state, so a write of one field does not hold a write of another, whatever the values.
function setFields(body: DeviceBody): string {
  return Object.entries(body)
    .filter(([, value]) => value !== undefined)
    .map(([name]) => name)
    .sort()
    .join(",");
}

defineCommand<DeviceBody>({
  id: "device.update",
  title: "Notification settings",
  key: setFields,
  when: onSettings,
  run: async (_ctx, args) => {
    const before = useFleet.getState().snapshot?.device;
    const outcome = await api.putDevice(args);
    if (outcome.kind !== "ok") {
      showOutcome(outcome, WHAT);
      return;
    }
    // Any device value other than the one held when the write went out (a stream event, a new
    // snapshot, or the answer of another write in flight with this one) wins: this answer is not
    // the newest. The stream's event that follows every write brings the daemon's value.
    if (useFleet.getState().snapshot?.device === before) {
      useFleet.getState().apply("device", outcome.value);
    }
  },
});

// null replies reset the list to the defaults: the daemon takes no field for that.
defineCommand<{ replies: string[] | null; onSaved?: () => void }>({
  id: "quickReplies.save",
  title: "Save",
  when: onSettings,
  run: async (_ctx, args) => {
    const before = useFleet.getState().snapshot?.quick_replies;
    const outcome = await api.putQuickReplies(args.replies === null ? {} : { replies: args.replies });
    if (outcome.kind !== "ok") {
      showOutcome(outcome, WHAT);
      return;
    }
    if (useFleet.getState().snapshot?.quick_replies === before) {
      useFleet.getState().apply("quick_replies", outcome.value);
    }
    args.onSaved?.();
  },
});

defineCommand({
  id: "device.unpair",
  title: "Unpair this phone",
  when: onSettings,
  confirm: () => ({
    title: "Unpair this phone?",
    body: "This phone stops getting notifications and goes back to pairing. Pair it again with loom web pair on the host.",
    action: "Unpair",
    destructive: true,
  }),
  run: async () => {
    const outcome = await api.deleteDevice();
    // The unpaired path clears what a 401 clears and opens pairing (main.tsx).
    if (outcome.kind === "ok") {
      emitUnpaired();
      return;
    }
    showOutcome(outcome, "Unpair");
  },
});

// device.pair is offline: it runs while unpaired, with no fresh snapshot (a pairing screen has
// none). A refusal is the daemon's own text, as a toast: classic's inline line.
defineCommand<{ code: string }>({
  id: "device.pair",
  title: "Pair",
  offline: true,
  when: (ctx) => ctx.panel === "pair",
  run: async (_ctx, args) => {
    const outcome = await api.pair({ code: args.code });
    switch (outcome.kind) {
      case "ok":
        startFleet();
        window.location.assign(hrefFor("fleet"));
        return;
      case "failed":
        showMessage(outcome.status === 0 ? CANT_REACH : outcome.message, true);
        return;
      case "refused":
      case "stopping":
      case "timeout":
        showMessage(outcome.message, true);
        return;
      case "pending":
      case "unpaired":
        showMessage("That didn't work.", true);
        return;
    }
  },
});
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec prettier --write src/commands/device.ts src/commands/device.test.ts && pnpm -C internal/web/ui exec vitest run src/commands/device.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
Expected: `Tests  29 passed (29)` (the `it.each` rows count each), the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/commands/device.ts internal/web/ui/src/commands/device.test.ts
git commit -m "feat(web): the device, quick-replies, unpair and pair commands" -- internal/web/ui/src/commands/device.ts internal/web/ui/src/commands/device.test.ts
```

### Task 4.5: Register the commands, name `device.pair` in the offline rule, rebuild the bundle

**Files:**
- Modify: `internal/web/ui/src/commands/index.ts`
- Modify: `internal/web/ui/src/commands/index.test.ts`
- Modify: `internal/web/ui/src/app/commands.ts:28-31`
- Modify: `internal/web/ui/CLAUDE.md:9`
- Modify: `internal/web/dist/`

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/commands/index.test.ts`, replace:

```ts
    "job.resume",
  ])("registers %s on import", (id) => {
```

with:

```ts
    "job.resume",
    "session.create",
    "device.update",
    "quickReplies.save",
    "device.unpair",
    "device.pair",
  ])("registers %s on import", (id) => {
```

Fails without: "registers %s on import": the `import "./launch"` or the `import "./device"` removed from `commands/index.ts` (the five ids are not registered).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/commands/index.test.ts`
Expected: the five new rows fail with `expected undefined to be defined`.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/commands/index.ts`, replace:

```ts
import "./jobs";
```

with:

```ts
import "./jobs";
import "./launch";
import "./device";
```

In `internal/web/ui/CLAUDE.md`, line 9, replace the sentence:

```
Only `app.backToClassic` is `offline`, because the switch must work with the daemon down.
```

with:

```
Only `app.backToClassic` and `device.pair` are `offline`, because the switch must work with the daemon down and pairing runs before any snapshot exists.
```

In `internal/web/ui/src/app/commands.ts`, in the doc comment of `Command`, replace:

```ts
// Command is one action. `offline` marks a command that runs while the
// snapshot is stale (a cold start, the daemon down, unpaired): only
// app.backToClassic, since the switch works offline (spec §4).
```

with:

```ts
// Command is one action. `offline` marks a command that runs while the
// snapshot is stale (a cold start, the daemon down, unpaired): app.backToClassic,
// since the switch works offline (spec §4), and device.pair, since pairing runs
// before any snapshot exists.
```

Rebuild the bundle: `pnpm -C internal/web/ui run build`. The commands are imported by `main.tsx`, so the entry script changes. This also restores what Task 4.2's edit of `store/fleet.ts` made stale: `dist:check` is not run before this task, and passes after it.

- [ ] **Step 4: Run the story's gates**

Run: `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
Expected: the Vitest suite passes (every file), `dist:check` prints that the bundle is fresh, and the Go packages print `ok`. If `check` fails on `format:check`, run `pnpm -C internal/web/ui exec prettier --write <the files it names>` and run it again.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "feat(web): register the launch and device commands, and rebuild the bundle" -- internal/web/ui/src/commands/index.ts internal/web/ui/src/commands/index.test.ts internal/web/ui/src/app/commands.ts internal/web/ui/CLAUDE.md internal/web/dist
```

## Story 5: Turning push on from the phone

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/push.ts`, `internal/web/ui/src/app/push.test.ts`, `internal/web/ui/src/app/worker.ts`, `internal/web/ui/src/commands/push.ts`, `internal/web/ui/src/commands/push.test.ts`, `internal/web/ui/src/commands/index.ts`, `internal/web/ui/src/commands/index.test.ts`, `internal/web/dist/`
- **Interfaces:** exposes `PushStatus`, `usePushStatus`, the moved `pushSupported`, `activeRegistration` and `subscriptionBody`, and the command `push.enable`; uses part 1's command path (`defineCommand`), `api`, `showMessage`, `showOutcome` and `checkPushSubscription`, part 2's `commands/testkit.ts`, and Story 4's `commands/index.ts`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** full (outside calls in an order iOS enforces: the permission inside the gesture, a subscription made and dropped again on a failed post)

This story ports `push.mjs`'s enable flow as the command `push.enable` (spec Part 3 settlement 12) and gives Settings the phone's push state. The command asks for the notification permission first, inside the tap (iOS asks only inside a gesture, so `Notification.requestPermission()` is the first call `run` makes and nothing is awaited before it), then waits up to 5 s for an active worker, reads the key, drops the phone's old subscription, subscribes with the key, and posts the subscription, dropping the new one again when the post fails. Each failure is an error toast in the classic words, and success toasts "Notifications are on.". The push helpers `pushSupported`, `activeRegistration` and `subscriptionBody` move from `app/worker.ts` into the new `app/push.ts` with their bodies unchanged, and `usePushStatus` reads the phone beside them: classic `pushView`'s states, re-read when `device.push` changes and when a `push.enable` run ends. Three tasks: the move, the hook, the command. Each rebuilds the bundle, which comes out unchanged where nothing imports the new code (Task 5.2's hook until Task 5.3 and Story 6 use it) and changes in Task 5.3, since `main.tsx` imports the commands module. None of the code holds a landmine marker.

### Task 5.1: Move the push helpers into `app/push.ts`

**Files:**
- Create: `internal/web/ui/src/app/push.ts`
- Create: `internal/web/ui/src/app/push.test.ts`
- Modify: `internal/web/ui/src/app/worker.ts:4-6`
- Modify: `internal/web/ui/src/app/worker.ts:15-16`
- Modify: `internal/web/ui/src/app/worker.ts:82-119`
- Modify: `internal/web/dist/`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/app/push.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

import { activeRegistration, pushSupported, subscriptionBody } from "./push";

// installWorkers gives the page a service worker whose `ready` is the promise given.
function installWorkers(ready: Promise<unknown>): void {
  Object.defineProperty(navigator, "serviceWorker", { configurable: true, value: { ready } });
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, "serviceWorker");
});

// browser sets up a page that has push, except for the one thing named in `missing`.
function browser(missing: "serviceWorker" | "Notification" | "PushManager" | ""): void {
  if (missing !== "serviceWorker") installWorkers(new Promise(() => undefined));
  vi.stubGlobal("Notification", missing === "Notification" ? undefined : { permission: "granted" });
  vi.stubGlobal("PushManager", missing === "PushManager" ? undefined : class {});
}

describe("pushSupported", () => {
  it("is true where the page has a service worker, Notification and PushManager", () => {
    browser("");
    expect(pushSupported()).toBe(true);
  });

  it.each(["serviceWorker", "Notification", "PushManager"] as const)(
    "is false without %s",
    (missing) => {
      browser(missing);
      expect(pushSupported()).toBe(false);
    },
  );
});

describe("activeRegistration", () => {
  it("is the registration once the worker is ready", async () => {
    const registration = { pushManager: {} };
    installWorkers(Promise.resolve(registration));
    await expect(activeRegistration()).resolves.toBe(registration);
  });

  it("is null when no worker is active within 5 s, and not before", async () => {
    vi.useFakeTimers();
    installWorkers(new Promise(() => undefined));
    let answer: unknown = "waiting";
    void activeRegistration().then((registration) => {
      answer = registration;
    });
    await vi.advanceTimersByTimeAsync(4999);
    expect(answer).toBe("waiting");
    await vi.advanceTimersByTimeAsync(1);
    expect(answer).toBeNull();
  });
});

const subscriptionOf = (json: object): PushSubscription =>
  ({ toJSON: () => json }) as unknown as PushSubscription;

describe("subscriptionBody", () => {
  it("is the subscription in the shape the daemon reads", () => {
    const body = subscriptionBody(
      subscriptionOf({
        endpoint: "https://web.push.apple.com/abc",
        expirationTime: 99,
        keys: { p256dh: "pk", auth: "au" },
      }),
    );
    expect(body).toEqual({
      endpoint: "https://web.push.apple.com/abc",
      expirationTime: 99,
      keys: { p256dh: "pk", auth: "au" },
    });
  });

  it("gives a missing expirationTime as null", () => {
    const body = subscriptionBody(
      subscriptionOf({ endpoint: "https://e", keys: { p256dh: "pk", auth: "au" } }),
    );
    expect(body?.expirationTime).toBeNull();
  });

  it.each([
    ["an endpoint", { keys: { p256dh: "pk", auth: "au" } }],
    ["a p256dh key", { endpoint: "https://e", keys: { auth: "au" } }],
    ["an auth key", { endpoint: "https://e", keys: { p256dh: "pk" } }],
    ["keys", { endpoint: "https://e" }],
  ])("is null for a subscription without %s", (_name, json) => {
    expect(subscriptionBody(subscriptionOf(json))).toBeNull();
  });
});
```

Fails without: pushSupported `is false without %s` (each row): the matching term of `"serviceWorker" in navigator && typeof Notification !== "undefined" && typeof PushManager !== "undefined"` removed, which makes a page without that part count as supported.
Fails without: activeRegistration `is null when no worker is active within 5 s, and not before`: the `late` race removed (the promise never settles) or `READY_MS` changed from 5000.
Fails without: subscriptionBody `is null for a subscription without %s` (each row): the matching term of the `!json.endpoint || !p256dh || !auth` check removed.
Fails without: subscriptionBody `gives a missing expirationTime as null`: `json.expirationTime ?? null` replaced by `json.expirationTime`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/push.test.ts`
Expected: FAIL, `Failed to resolve import "./push" from "src/app/push.test.ts". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/app/push.ts` (the code is `worker.ts`'s, moved with its bodies unchanged and exported):

```ts
// The phone's side of push (spec §28 and Part 3 settlement 12): whether this page can take it, the
// worker registration it needs, and the subscription in the shape the daemon reads. The launch-time
// check that uses them is in worker.ts, and the command that turns push on is commands/push.ts.
import type { SubscriptionBody } from "@/api/gen";

// READY_MS is how long to wait for an active worker before giving up.
const READY_MS = 5000;

export function pushSupported(): boolean {
  return (
    "serviceWorker" in navigator &&
    typeof Notification !== "undefined" &&
    typeof PushManager !== "undefined"
  );
}

// activeRegistration is the page's worker registration once its worker is active, or null when
// none becomes active within READY_MS.
export async function activeRegistration(): Promise<ServiceWorkerRegistration | null> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const late = new Promise<null>((resolve) => {
    timer = setTimeout(() => {
      resolve(null);
    }, READY_MS);
  });
  try {
    return await Promise.race([navigator.serviceWorker.ready, late]);
  } finally {
    clearTimeout(timer);
  }
}

// subscriptionBody is the phone's subscription in the shape POST /api/v1/device/push reads, or
// null when it lacks what the daemon would refuse it for.
export function subscriptionBody(subscription: PushSubscription): SubscriptionBody | null {
  const json = subscription.toJSON();
  const p256dh = json.keys?.p256dh;
  const auth = json.keys?.auth;
  if (!json.endpoint || !p256dh || !auth) return null;
  return {
    endpoint: json.endpoint,
    expirationTime: json.expirationTime ?? null,
    keys: { p256dh, auth },
  };
}
```

In `internal/web/ui/src/app/worker.ts`, replace the imports:

```ts
import type { Device, SubscriptionBody } from "@/api/gen";
import { api } from "@/api/request";
import { cookieApp, page } from "./switchApp";
```

with:

```ts
import type { Device } from "@/api/gen";
import { api } from "@/api/request";
import { activeRegistration, pushSupported, subscriptionBody } from "./push";
import { cookieApp, page } from "./switchApp";
```

In the same file, delete these lines and the blank line after them (`READY_MS` moved with `activeRegistration`):

```ts
// READY_MS is how long the push check waits for an active worker before it gives up.
const READY_MS = 5000;

```

In the same file, delete the three private functions and the blank line after them (lines 82-119; the blank line at 81 stays as the one blank line between `registerWorker` and `checkPushSubscription`):

```ts
function pushSupported(): boolean {
  return (
    "serviceWorker" in navigator &&
    typeof Notification !== "undefined" &&
    typeof PushManager !== "undefined"
  );
}

// activeRegistration is the page's worker registration once its worker is active, or null when
// none becomes active within READY_MS.
async function activeRegistration(): Promise<ServiceWorkerRegistration | null> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const late = new Promise<null>((resolve) => {
    timer = setTimeout(() => {
      resolve(null);
    }, READY_MS);
  });
  try {
    return await Promise.race([navigator.serviceWorker.ready, late]);
  } finally {
    clearTimeout(timer);
  }
}

// subscriptionBody is the phone's subscription in the shape POST /api/v1/device/push reads, or
// null when it lacks what the daemon would refuse it for.
function subscriptionBody(subscription: PushSubscription): SubscriptionBody | null {
  const json = subscription.toJSON();
  const p256dh = json.keys?.p256dh;
  const auth = json.keys?.auth;
  if (!json.endpoint || !p256dh || !auth) return null;
  return {
    endpoint: json.endpoint,
    expirationTime: json.expirationTime ?? null,
    keys: { p256dh, auth },
  };
}

```

`checkPushSubscription`'s `/**` comment and everything above the deleted lines stay as they are (line numbers are the file's before this task's edits). Rebuild the bundle: `pnpm -C internal/web/ui run build`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/app/push.test.ts src/app/worker.test.ts`
Expected: both files pass. `worker.test.ts` is unchanged: its `checkPushSubscription` tests prove the move changed no behaviour.

Run: `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
Expected: the typecheck, lint, format check and every Vitest file pass, and `dist:check` says the bundle is fresh. If `format:check` fails, run `pnpm -C internal/web/ui exec prettier --write <the files it names>` and run it again.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/app/push.ts internal/web/ui/src/app/push.test.ts internal/web/dist
git commit -m "refactor(web): move the push helpers from the worker module into app/push.ts" -- internal/web/ui/src/app/push.ts internal/web/ui/src/app/push.test.ts internal/web/ui/src/app/worker.ts internal/web/dist
```

### Task 5.2: `usePushStatus`

**Files:**
- Modify: `internal/web/ui/src/app/push.ts`
- Test: `internal/web/ui/src/app/push.test.ts`
- Modify: `internal/web/dist/`

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/app/push.test.ts`, replace the imports:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

import { activeRegistration, pushSupported, subscriptionBody } from "./push";
```

with:

```ts
import { act, renderHook, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Device } from "@/api/gen";

import {
  activeRegistration,
  pushRunEnded,
  pushSupported,
  subscriptionBody,
  usePushStatus,
  type PushStatus,
} from "./push";
```

and append at the end of the file:

```ts
const deviceOf = (push: string): Device => ({
  name: "iphone",
  notify: { waiting: true, finished: true, paused_until: "" },
  push,
});

// gate is a promise the test settles by hand.
function gate<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

// phone gives the page a ready worker whose push manager holds a subscription (or none), a
// notification permission and push support; it returns the `getSubscription` spy and the worker's
// registration.
function phone(held: boolean, permission: NotificationPermission = "granted") {
  const getSubscription = vi.fn().mockResolvedValue(held ? { endpoint: "https://e" } : null);
  const registration = { pushManager: { getSubscription } } as unknown as ServiceWorkerRegistration;
  installWorkers(Promise.resolve(registration));
  vi.stubGlobal("Notification", { permission });
  vi.stubGlobal("PushManager", class {});
  return { getSubscription, registration };
}

// useRecorded is usePushStatus for a device loom holds nothing for, noting each status that is
// committed (a render React throws away is not noted).
function useRecorded(statuses: PushStatus[]): PushStatus {
  const status = usePushStatus(deviceOf(""));
  useEffect(() => {
    statuses.push(status);
  });
  return status;
}

describe("usePushStatus", () => {
  it("is loading until the phone has answered, and off when no worker is active within 5 s", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("Notification", { permission: "granted" });
    vi.stubGlobal("PushManager", class {});
    installWorkers(new Promise(() => undefined));
    const { result } = renderHook(() => usePushStatus(deviceOf("")));
    expect(result.current).toBe("loading");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(result.current).toBe("off");
  });

  it("reads a push manager that cannot answer as holding no subscription", async () => {
    phone(true).getSubscription.mockRejectedValue(new Error("no push manager"));
    const { result } = renderHook(() => usePushStatus(deviceOf("ok")));
    await waitFor(() => {
      expect(result.current).toBe("off");
    });
  });

  it.each<[string, boolean, NotificationPermission, PushStatus]>([
    ["", false, "granted", "off"],
    ["", true, "granted", "off"],
    ["ok", true, "granted", "on"],
    ["ok", false, "granted", "off"],
    ["expired", true, "granted", "again"],
    ["failing: 410", false, "granted", "again"],
    ["ok", true, "denied", "denied"],
    ["expired", true, "denied", "denied"],
  ])(
    "device.push %j with a subscription held by the phone: %s, permission %s, is %s",
    async (state, held, permission, want) => {
      phone(held, permission);
      const { result } = renderHook(() => usePushStatus(deviceOf(state)));
      await waitFor(() => {
        expect(result.current).toBe(want);
      });
    },
  );

  it("is unsupported where the page has no push, whatever loom holds", async () => {
    phone(true);
    vi.stubGlobal("PushManager", undefined);
    const { result } = renderHook(() => usePushStatus(deviceOf("ok")));
    await waitFor(() => {
      expect(result.current).toBe("unsupported");
    });
  });

  it("reads the phone again when device.push changes, and not when only the object does", async () => {
    const { getSubscription } = phone(false);
    getSubscription.mockResolvedValueOnce(null).mockResolvedValue({ endpoint: "https://e" });
    const { result, rerender } = renderHook(({ device }) => usePushStatus(device), {
      initialProps: { device: deviceOf("expired") },
    });
    await waitFor(() => {
      expect(result.current).toBe("again");
    });
    expect(getSubscription).toHaveBeenCalledTimes(1);

    rerender({ device: deviceOf("expired") });
    expect(getSubscription).toHaveBeenCalledTimes(1);

    rerender({ device: deviceOf("ok") });
    await waitFor(() => {
      expect(result.current).toBe("on");
    });
    expect(getSubscription).toHaveBeenCalledTimes(2);
  });

  it("follows the newest read when two overlap, whichever settles last", async () => {
    const { getSubscription, registration } = phone(false);
    const first = gate<unknown>();
    getSubscription.mockReset().mockReturnValueOnce(first.promise).mockResolvedValue(null);
    const { result } = renderHook(() => usePushStatus(deviceOf("ok")));
    await waitFor(() => {
      expect(getSubscription).toHaveBeenCalledTimes(1);
    });

    // A turn-on that failed ends while the first read is still waiting; the phone holds none.
    await act(async () => {
      await pushRunEnded(false, registration);
    });
    await waitFor(() => {
      expect(result.current).toBe("off");
    });

    // The first read settles last, with a subscription: it is stale and changes nothing.
    await act(async () => {
      first.resolve({ endpoint: "https://e" });
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(result.current).toBe("off");
  });

  it("holds the phone's new state when a turn-on ends: on at once, with no render of off between", async () => {
    const { getSubscription, registration } = phone(false);
    const statuses: PushStatus[] = [];
    const { result } = renderHook(() => useRecorded(statuses));
    await waitFor(() => {
      expect(result.current).toBe("off");
    });

    // The turn-on made a subscription; loom's own state has not caught up.
    getSubscription.mockResolvedValue({ endpoint: "https://e" });
    const before = statuses.length;
    await act(async () => {
      await pushRunEnded(true, registration);
    });
    await waitFor(() => {
      expect(result.current).toBe("on");
    });
    expect(statuses.slice(before)).not.toContain("off");
  });

  it("reads the phone again when a turn-on ends, and says on at once while loom still says expired", async () => {
    const { getSubscription, registration } = phone(false);
    getSubscription.mockResolvedValueOnce(null).mockResolvedValue({ endpoint: "https://e" });
    const { result } = renderHook(() => usePushStatus(deviceOf("expired")));
    await waitFor(() => {
      expect(result.current).toBe("again");
    });

    await act(async () => {
      await pushRunEnded(true, registration);
    });
    await waitFor(() => {
      expect(result.current).toBe("on");
    });
    expect(getSubscription.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it("reads again after a turn-on that failed, and does not say on", async () => {
    const { getSubscription, registration } = phone(true);
    const { result } = renderHook(() => usePushStatus(deviceOf("expired")));
    await waitFor(() => {
      expect(result.current).toBe("again");
    });
    const before = getSubscription.mock.calls.length;

    await act(async () => {
      await pushRunEnded(false, registration);
    });
    expect(getSubscription.mock.calls.length).toBeGreaterThan(before);
    expect(result.current).toBe("again");
  });

  it("reads the phone for a run that got no worker without waiting for one", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("Notification", { permission: "granted" });
    vi.stubGlobal("PushManager", class {});
    installWorkers(new Promise(() => undefined));
    let ended = false;
    void pushRunEnded(false, null).then(() => {
      ended = true;
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(ended).toBe(true);
  });

  it("forgets the turn-on's on once loom's state changes", async () => {
    const { registration } = phone(true);
    const { result, rerender } = renderHook(({ device }) => usePushStatus(device), {
      initialProps: { device: deviceOf("expired") },
    });
    await waitFor(() => {
      expect(result.current).toBe("again");
    });
    await act(async () => {
      await pushRunEnded(true, registration);
    });
    await waitFor(() => {
      expect(result.current).toBe("on");
    });

    rerender({ device: deviceOf("failing: 410") });
    await waitFor(() => {
      expect(result.current).toBe("again");
    });
  });

  it("does not count a turn-on that ended before it mounted", async () => {
    const { registration } = phone(true);
    await act(async () => {
      await pushRunEnded(true, registration);
    });
    const { result } = renderHook(() => usePushStatus(deviceOf("expired")));
    await waitFor(() => {
      expect(result.current).not.toBe("loading");
    });
    expect(result.current).toBe("again");
  });
});
```

Fails without: `device.push %j with a subscription held by the phone` (each row): a state of `statusOf` removed or inverted (`"ok"` no longer needing `phone.subscribed`, `"expired"` or `startsWith("failing")` dropped, `denied` checked after `on`).
Fails without: `is unsupported where the page has no push, whatever loom holds`: the `phone.supported` check removed from `statusOf`.
Fails without: `is loading until the phone has answered, and off when no worker is active within 5 s`: the initial `null` phone replaced by a guess (the `loading` check), or `activeRegistration`'s answer never reaching `setPhone` (it stays `loading`).
Fails without: `reads a push manager that cannot answer as holding no subscription`: the `try`/`catch` in `readPhone` removed (the rejection leaves the status `loading`).
Fails without: `reads the phone again when device.push changes, and not when only the object does`: `state` removed from the read effect's dependencies (the second call count and the `on` fail), or `device` put there in its place (the first `rerender` re-reads and the count of 1 fails).
Fails without: `follows the newest read when two overlap, whichever settles last`: the `live` guard in the read effect removed (the first read settles last and sets `on`).
Fails without: `holds the phone's new state when a turn-on ends: on at once, with no render of off between`: `setPhone(run.phone)` removed from the render-time branch on a changed `run` (the render after the run holds the old phone and says `off` until the re-read), or `pushRunEnded` reading the phone after it notifies instead of before.
Fails without: `reads the phone again when a turn-on ends, and says on at once while loom still says expired`: `setDone(run.ok)` removed (it stays `again`), or `pushRunEnded` not reading the phone (the count stays 1).
Fails without: `reads again after a turn-on that failed, and does not say on`: `setDone(run.ok)` replaced by `setDone(true)`, or `pushRunEnded` not reading the phone.
Fails without: `reads the phone for a run that got no worker without waiting for one`: `readPhone` ignoring the registration it is given and waiting on `activeRegistration()` (the run does not end within 0 ms).
Fails without: `forgets the turn-on's on once loom's state changes`: the `setDone(false)` on a changed `state` removed.
Fails without: `does not count a turn-on that ended before it mounted`: `seenRun`'s initial value replaced by a fixed first run, which makes the earlier run count.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/push.test.ts`
Expected: FAIL on the new tests, `usePushStatus is not a function` (and `pushRunEnded is not a function`); the helpers' tests from Task 5.1 still pass.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/app/push.ts`, replace the import:

```ts
import type { SubscriptionBody } from "@/api/gen";
```

with:

```ts
import { useEffect, useState, useSyncExternalStore } from "react";

import type { Device, SubscriptionBody } from "@/api/gen";
```

and append at the end of the file:

```ts

// PushStatus is what Settings' push control shows (classic `pushView`'s states): "loading" until
// the phone has said what it holds, "unsupported" where the page has no push, "denied" where the
// phone refuses notifications, "on", "again" where loom holds a subscription that is expired or
// failing, and "off".
export type PushStatus = "loading" | "unsupported" | "denied" | "on" | "again" | "off";

// Phone is what this phone knows: whether it can take push, the notification permission and
// whether it holds a subscription.
interface Phone {
  supported: boolean;
  permission: NotificationPermission;
  subscribed: boolean;
}

// readPhone asks the phone; a registration or subscription it cannot read is no subscription.
// `known` is the worker registration to ask, null for none; left out, it waits for the page's own.
async function readPhone(known?: ServiceWorkerRegistration | null): Promise<Phone> {
  if (!pushSupported()) return { supported: false, permission: "default", subscribed: false };
  let subscribed = false;
  try {
    const registration = known === undefined ? await activeRegistration() : known;
    if (registration !== null) {
      subscribed = (await registration.pushManager.getSubscription()) !== null;
    }
  } catch {
    subscribed = false;
  }
  return { supported: true, permission: Notification.permission, subscribed };
}

// PushRun is the last end of a turn-on: whether it worked, and what the phone held when it ended
// (null before any has ended).
interface PushRun {
  ok: boolean;
  phone: Phone | null;
}

let lastRun: PushRun = { ok: false, phone: null };
const runListeners = new Set<() => void>();

/**
 * pushRunEnded is awaited by push.enable as its run ends, whether it worked or not, with the worker
 * registration the run used (null when it got none). It reads the phone first and then tells every
 * usePushStatus, so the first render after the run already holds what the phone has now, and
 * after a run that worked says "on" until loom's own state changes, since the stream's `device`
 * event follows the run by a moment.
 */
export async function pushRunEnded(
  ok: boolean,
  registration: ServiceWorkerRegistration | null,
): Promise<void> {
  const phone = await readPhone(registration);
  lastRun = { ok, phone };
  for (const listener of [...runListeners]) listener();
}

function subscribeRuns(listener: () => void): () => void {
  runListeners.add(listener);
  return () => {
    runListeners.delete(listener);
  };
}

function currentRun(): PushRun {
  return lastRun;
}

// statusOf is classic `pushView`: loom's state for the device (`device.push` is "", "ok", "expired"
// or "failing: <status>") against what the phone knows (null while it loads) and whether the
// person just turned it on here.
function statusOf(state: string, phone: Phone | null, done: boolean): PushStatus {
  if (phone === null) return "loading";
  if (!phone.supported) return "unsupported";
  if (phone.permission === "denied") return "denied";
  if (done && phone.subscribed) return "on";
  if (state === "ok" && phone.subscribed) return "on";
  if (state === "expired" || state.startsWith("failing")) return "again";
  return "off";
}

/**
 * The state of Settings' push control: the phone's own push state (read on mount, whenever
 * `device.push` changes and whenever a push.enable run ends, the run's own read being the one the
 * render after it uses) against what loom holds for the device. A turn-on that worked reads "on"
 * until `device.push` next changes.
 */
export function usePushStatus(device: Device): PushStatus {
  const [phone, setPhone] = useState<Phone | null>(null);
  const run = useSyncExternalStore(subscribeRuns, currentRun);
  const state = device.push;

  // The "on" a turn-on gives is dropped when loom's state changes, and set with the phone's new
  // state when a run ends after this hook mounted: both adjust state during the render that sees the
  // change, so no render shows the phone's old state beside the run's end.
  const [seenState, setSeenState] = useState(state);
  const [seenRun, setSeenRun] = useState(run);
  const [done, setDone] = useState(false);
  if (seenState !== state) {
    setSeenState(state);
    setDone(false);
  }
  if (seenRun !== run) {
    setSeenRun(run);
    setDone(run.ok);
    if (run.phone !== null) setPhone(run.phone);
  }

  // A read still waiting when `state` or `run` changes is dropped, and the phone is read again.
  useEffect(() => {
    let live = true;
    void readPhone().then((read) => {
      if (live) setPhone(read);
    });
    return () => {
      live = false;
    };
  }, [state, run]);

  return statusOf(state, phone, done);
}
```

Rebuild the bundle: `pnpm -C internal/web/ui run build` (nothing imports the hook yet, so the bundle may come out unchanged).

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/app/push.test.ts`
Expected: the file passes: the helpers' tests and every `usePushStatus` test.

Run: `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
Expected: every check passes and `dist:check` says the bundle is fresh. If `format:check` fails, run `pnpm -C internal/web/ui exec prettier --write <the files it names>` and run it again.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "feat(web): usePushStatus, the push control's states read from the phone and the device" -- internal/web/ui/src/app/push.ts internal/web/ui/src/app/push.test.ts internal/web/dist
```

### Task 5.3: The `push.enable` command

**Files:**
- Create: `internal/web/ui/src/commands/push.ts`
- Create: `internal/web/ui/src/commands/push.test.ts`
- Modify: `internal/web/ui/src/commands/index.ts`
- Modify: `internal/web/ui/src/commands/index.test.ts`
- Modify: `internal/web/dist/`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/commands/push.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/request";
import { commandById, commandEnabled, runCommand } from "@/app/commands";
import { activeRegistration, pushRunEnded, pushSupported } from "@/app/push";

import "./index";
import { command, context, deferred, spyOnAdd } from "./testkit";

// The helpers that read the page's browser are the test's to answer; `subscriptionBody` and the rest
// of the module stay real.
vi.mock("@/app/push", async (original) => ({
  ...(await original<typeof import("@/app/push")>()),
  pushSupported: vi.fn(),
  activeRegistration: vi.fn(),
  pushRunEnded: vi.fn(),
}));

// KEY is the daemon's key as unpadded base64url: the bytes 0xfb 0xff 0xbe.
const KEY = "-_--";
const KEY_BYTES = new Uint8Array([0xfb, 0xff, 0xbe]);

const SUBSCRIPTION_JSON = {
  endpoint: "https://web.push.apple.com/abc",
  expirationTime: null,
  keys: { p256dh: "pk", auth: "au" },
};

const BLOCKED =
  "Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.";

let add: ReturnType<typeof spyOnAdd>;
let requestPermission: ReturnType<typeof vi.fn<() => Promise<NotificationPermission>>>;
let oldSubscription: { unsubscribe: ReturnType<typeof vi.fn> };
let newSubscription: { toJSON: () => object; unsubscribe: ReturnType<typeof vi.fn> };
let getSubscription: ReturnType<typeof vi.fn>;
let subscribe: ReturnType<typeof vi.fn>;
let registration: ServiceWorkerRegistration;
let pushKey: ReturnType<typeof vi.spyOn<typeof api, "pushKey">>;
let postPush: ReturnType<typeof vi.spyOn<typeof api, "postPush">>;

const settings = () => context(undefined, { panel: "settings" });
const enable = () => runCommand(command("push.enable"), settings(), undefined);

beforeEach(() => {
  add = spyOnAdd();
  requestPermission = vi.fn<() => Promise<NotificationPermission>>().mockResolvedValue("granted");
  vi.stubGlobal("Notification", { requestPermission });
  oldSubscription = { unsubscribe: vi.fn().mockResolvedValue(true) };
  newSubscription = { toJSON: () => SUBSCRIPTION_JSON, unsubscribe: vi.fn().mockResolvedValue(true) };
  getSubscription = vi.fn().mockResolvedValue(oldSubscription);
  subscribe = vi.fn().mockResolvedValue(newSubscription);
  vi.mocked(pushSupported).mockReset().mockReturnValue(true);
  registration = { pushManager: { getSubscription, subscribe } } as unknown as ServiceWorkerRegistration;
  vi.mocked(pushRunEnded).mockReset().mockResolvedValue(undefined);
  vi.mocked(activeRegistration).mockReset().mockResolvedValue(registration);
  pushKey = vi
    .spyOn(api, "pushKey")
    .mockResolvedValue({ kind: "ok", status: 200, value: { key: KEY }, notices: [] });
  postPush = vi.spyOn(api, "postPush").mockResolvedValue({
    kind: "ok",
    status: 200,
    value: { name: "iphone", notify: { waiting: true, finished: true, paused_until: "" }, push: "ok" },
    notices: [],
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const toasted = (description: string, type: "error" | "info") =>
  expect(add).toHaveBeenCalledWith(expect.objectContaining({ description, type }));

describe("the push.enable command's availability", () => {
  it("is registered by the commands module, on the settings route only, and off while stale", () => {
    expect(commandById("push.enable")).toBeDefined();
    expect(commandEnabled(command("push.enable"), settings())).toBe(true);
    expect(commandEnabled(command("push.enable"), context(undefined, { panel: "fleet" }))).toBe(
      false,
    );
    expect(commandEnabled(command("push.enable"), context(undefined, { panel: "turns" }))).toBe(
      false,
    );
    expect(
      commandEnabled(
        command("push.enable"),
        context(undefined, { panel: "settings", stale: true, link: "offline" }),
      ),
    ).toBe(false);
  });
});

describe("turning push on", () => {
  it("asks for the permission inside the tap, before anything is awaited", async () => {
    const gate = deferred<NotificationPermission>();
    requestPermission.mockReturnValue(gate.promise);

    const run = enable();

    // Nothing has been awaited yet: the permission is asked, and nothing after it has been.
    expect(requestPermission).toHaveBeenCalledOnce();
    expect(activeRegistration).not.toHaveBeenCalled();
    expect(pushKey).not.toHaveBeenCalled();

    gate.resolve("granted");
    await run;
    expect(pushKey).toHaveBeenCalledOnce();
  });

  it("makes its calls in order and ends with the new subscription posted", async () => {
    await enable();

    const order = [
      requestPermission,
      vi.mocked(activeRegistration),
      pushKey,
      getSubscription,
      oldSubscription.unsubscribe,
      subscribe,
      postPush,
    ].flatMap((fn) => fn.mock.invocationCallOrder.slice(0, 1));
    expect(order).toHaveLength(7);
    expect(order).toEqual([...order].sort((a, b) => a - b));
    expect(subscribe).toHaveBeenCalledWith({
      userVisibleOnly: true,
      applicationServerKey: KEY_BYTES,
    });
    expect(postPush).toHaveBeenCalledWith(SUBSCRIPTION_JSON);
    expect(newSubscription.unsubscribe).not.toHaveBeenCalled();
    toasted("Notifications are on.", "info");
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(true, registration);
  });

  it("subscribes with the bytes of an unpadded key whose length is not a multiple of 4", async () => {
    pushKey.mockResolvedValue({ kind: "ok", status: 200, value: { key: "-_8" }, notices: [] });
    await enable();
    expect(subscribe).toHaveBeenCalledWith({
      userVisibleOnly: true,
      applicationServerKey: new Uint8Array([0xfb, 0xff]),
    });
  });

  it("makes a subscription when the phone holds none, without dropping one", async () => {
    getSubscription.mockResolvedValue(null);
    await enable();
    expect(oldSubscription.unsubscribe).not.toHaveBeenCalled();
    expect(subscribe).toHaveBeenCalledOnce();
    expect(postPush).toHaveBeenCalledOnce();
  });
});

describe("a turn-on that fails", () => {
  it("says where push is not supported, asks for nothing and ends the run", async () => {
    vi.mocked(pushSupported).mockReturnValue(false);
    await enable();
    expect(requestPermission).not.toHaveBeenCalled();
    toasted(
      "Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.",
      "error",
    );
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, null);
  });

  it.each<NotificationPermission>(["denied", "default"])(
    "says the notifications are blocked when the permission comes back %s, and goes no further",
    async (permission) => {
      requestPermission.mockResolvedValue(permission);
      await enable();
      toasted(BLOCKED, "error");
      expect(activeRegistration).not.toHaveBeenCalled();
      expect(pushKey).not.toHaveBeenCalled();
      expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, null);
    },
  );

  it("says loom is not ready when no worker is active in time, and reads no key", async () => {
    vi.mocked(activeRegistration).mockResolvedValue(null);
    await enable();
    toasted("loom isn't ready for notifications yet. Reload the app and try again.", "error");
    expect(pushKey).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, null);
  });

  it("toasts the daemon's words when the key read fails, and touches no subscription", async () => {
    pushKey.mockResolvedValue({ kind: "stopping", message: "push is not available" });
    await enable();
    toasted("push is not available", "error");
    expect(getSubscription).not.toHaveBeenCalled();
    expect(subscribe).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("says loom sent no push key for an empty key, and touches no subscription", async () => {
    pushKey.mockResolvedValue({ kind: "ok", status: 200, value: { key: "" }, notices: [] });
    await enable();
    toasted("loom sent no push key.", "error");
    expect(getSubscription).not.toHaveBeenCalled();
    expect(subscribe).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("says loom sent no push key for a key that does not decode, and drops no subscription", async () => {
    pushKey.mockResolvedValue({ kind: "ok", status: 200, value: { key: "not a key!" }, notices: [] });
    await enable();
    toasted("loom sent no push key.", "error");
    expect(getSubscription).not.toHaveBeenCalled();
    expect(oldSubscription.unsubscribe).not.toHaveBeenCalled();
    expect(subscribe).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("says the phone refused the subscription, and posts nothing", async () => {
    subscribe.mockRejectedValue(new Error("the push service is unreachable"));
    await enable();
    toasted("The phone refused the subscription: the push service is unreachable", "error");
    expect(postPush).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("says the phone refused it when dropping the old subscription throws, and makes no new one", async () => {
    oldSubscription.unsubscribe.mockRejectedValue(new Error("gone"));
    await enable();
    toasted("The phone refused the subscription: gone", "error");
    expect(subscribe).not.toHaveBeenCalled();
    expect(postPush).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("drops the phone's new subscription again when the daemon refuses it, with the daemon's words", async () => {
    postPush.mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "that push service is not allowed",
    });
    await enable();
    toasted("that push service is not allowed", "error");
    expect(newSubscription.unsubscribe).toHaveBeenCalledOnce();
    expect(add).not.toHaveBeenCalledWith(expect.objectContaining({ description: "Notifications are on." }));
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("drops it again when the post cannot reach loom, and one failing to drop adds nothing", async () => {
    postPush.mockResolvedValue({ kind: "failed", status: 0, message: "Can't reach loom." });
    newSubscription.unsubscribe.mockRejectedValue(new Error("already gone"));
    await enable();
    expect(newSubscription.unsubscribe).toHaveBeenCalledOnce();
    toasted("Can't reach loom.", "error");
    expect(add).toHaveBeenCalledTimes(1);
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("posts nothing the daemon would not refuse for a subscription without its keys: it sends an empty body and drops the subscription", async () => {
    newSubscription.toJSON = () => ({ endpoint: "https://web.push.apple.com/abc" });
    postPush.mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "the subscription has no keys",
    });
    await enable();
    expect(postPush).toHaveBeenCalledWith({});
    toasted("the subscription has no keys", "error");
    expect(newSubscription.unsubscribe).toHaveBeenCalledOnce();
  });

  it("ends the run, and toasts the error, when a call throws", async () => {
    requestPermission.mockRejectedValue(new Error("not allowed here"));
    await enable();
    toasted("not allowed here", "error");
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, null);
  });

  it("does not end the run until the phone's new state has been read", async () => {
    const read = deferred<void>();
    vi.mocked(pushRunEnded).mockReturnValue(read.promise);
    let ended = false;
    const run = enable().then(() => {
      ended = true;
    });
    await vi.waitFor(() => {
      expect(pushRunEnded).toHaveBeenCalledOnce();
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(ended).toBe(false);

    read.resolve();
    await run;
    expect(ended).toBe(true);
  });
});
```

Fails without: `is registered by the commands module, on the settings route only, and off while stale`: the `import "./push"` removed from `commands/index.ts` (`command("push.enable")` throws), `when` not reading the panel (the fleet and turns routes get it), or `offline: true` set (the stale case is enabled).
Fails without: `asks for the permission inside the tap, before anything is awaited`: any `await` (`activeRegistration()`, the key read) placed before `Notification.requestPermission()` in `enable`, which leaves `requestPermission` uncalled when the test checks.
Fails without: `makes its calls in order and ends with the new subscription posted`: the old subscription dropped after `subscribe`, `userVisibleOnly` or the key's bytes wrong, the body posted other than `subscriptionBody`'s, or the success toast or `pushRunEnded(true, registration)` missing.
Fails without: `subscribes with the bytes of an unpadded key whose length is not a multiple of 4`: `keyBytes` not mapping `-` and `_` to `+` and `/` (the key reads as no key and `subscribe` is never called).
Fails without: `makes a subscription when the phone holds none, without dropping one`: the `old !== null` guard removed (`null.unsubscribe()` throws, and the run ends with the phone-refused toast and no `subscribe`).
Fails without: `says where push is not supported, asks for nothing and ends the run`: the `pushSupported()` check removed (the permission is asked).
Fails without: `says the notifications are blocked when the permission comes back %s, and goes no further`: the `!== "granted"` test removed, or written as `=== "denied"` (the `default` row goes on).
Fails without: `says loom is not ready when no worker is active in time, and reads no key`: the `registration === null` check removed.
Fails without: `toasts the daemon's words when the key read fails, and touches no subscription`: the key outcome's `kind !== "ok"` check removed, or `showOutcome` not called.
Fails without: `says loom sent no push key for an empty key, and touches no subscription`: the `text !== ""` term removed (`keyBytes("")` is an empty array, which reaches `subscribe`).
Fails without: `says loom sent no push key for a key that does not decode, and drops no subscription`: the key decoded after the old subscription is dropped instead of before it (`getSubscription` and `unsubscribe` are called), or `keyBytes`' `catch` removed (the decode error reaches `runCommand` as its own toast).
Fails without: `says the phone refused the subscription, and posts nothing`: the `try`'s catch removed or its text changed.
Fails without: `says the phone refused it when dropping the old subscription throws, and makes no new one`: the old subscription's `unsubscribe` taken out of the `try`.
Fails without: `drops the phone's new subscription again when the daemon refuses it, with the daemon's words`: the clean-up `unsubscribe` removed, or the success toast not guarded by the post's outcome.
Fails without: `drops it again when the post cannot reach loom, and one failing to drop adds nothing`: the clean-up's own `try`/`catch` removed (the rejection becomes a second toast).
Fails without: `posts nothing the daemon would not refuse for a subscription without its keys`: `?? {}` replaced by a skip of the post (the post is never made).
Fails without: `ends the run, and toasts the error, when a call throws`: the `try`/`finally` around `enable(used)` removed.
Fails without: `does not end the run until the phone's new state has been read`: the `await` in front of `pushRunEnded` in the `finally` removed (the run ends before the read does).

In `internal/web/ui/src/commands/index.test.ts`, as Story 4 leaves it, replace:

```ts
    "device.pair",
  ])("registers %s on import", (id) => {
```

with:

```ts
    "device.pair",
    "push.enable",
  ])("registers %s on import", (id) => {
```

Fails without: `registers %s on import` (the `push.enable` row): the `import "./push"` removed from `commands/index.ts` (the command is not registered).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/commands/push.test.ts src/commands/index.test.ts`
Expected: FAIL. In `push.test.ts` every test but the first fails with `Error: no command push.enable is registered` from `command("push.enable")` (the file's `import "./index"` registers the others, not this one); the first, the availability test, fails on its first line with `expected undefined to be defined`, as does the `push.enable` row of `index.test.ts`.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/commands/push.ts`:

```ts
// push.enable: turning notifications on from Settings, a port of the classic push.mjs's enablePush
// (spec Part 3 settlement 12). The steps run in an order iOS enforces: the permission is asked
// first, inside the tap that ran the command (runCommand reaches `run` with nothing awaited when a
// command has no sheet, and iOS asks for the permission only inside a gesture), then an active
// worker, the key, the phone's subscription made afresh, and the daemon told.
import type { PushKey } from "@/api/gen";
import { api } from "@/api/request";
import { defineCommand } from "@/app/commands";
import { activeRegistration, pushRunEnded, pushSupported, subscriptionBody } from "@/app/push";
import { showMessage, showOutcome } from "@/app/toasts";

const UNSUPPORTED =
  "Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.";
const BLOCKED =
  "Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.";
const NOT_READY = "loom isn't ready for notifications yet. Reload the app and try again.";
const WHAT = "Turning on notifications";

// keyBytes turns the unpadded base64url the daemon sends (the VAPID public key) into the bytes
// pushManager.subscribe takes (atob takes the unpadded form), or null when it is not base64url.
function keyBytes(text: string): Uint8Array<ArrayBuffer> | null {
  const b64 = text.replace(/-/g, "+").replace(/_/g, "/");
  try {
    return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
  } catch {
    return null;
  }
}

// Run is what a turn-on leaves for pushRunEnded: whether notifications are on, and the worker
// registration it used (null while it has none).
interface Run {
  ok: boolean;
  registration: ServiceWorkerRegistration | null;
}

// enable runs the steps, noting in `used` how it went. It shows every failure itself; a call that
// throws is runCommand's error toast.
async function enable(used: Run): Promise<void> {
  if (!pushSupported()) {
    showMessage(UNSUPPORTED, true);
    return;
  }
  // The first call that waits, and so the first await: the tap's gesture is still current.
  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    showMessage(BLOCKED, true);
    return;
  }
  const registration = await activeRegistration();
  if (registration === null) {
    showMessage(NOT_READY, true);
    return;
  }
  used.registration = registration;
  const key = await api.pushKey();
  if (key.kind !== "ok") {
    showOutcome(key, WHAT);
    return;
  }
  // The key is decoded before anything is dropped: a key the phone cannot use leaves the old
  // subscription as it is.
  const text: unknown = (key.value as Partial<PushKey> | undefined)?.key;
  const applicationServerKey = typeof text === "string" && text !== "" ? keyBytes(text) : null;
  if (applicationServerKey === null) {
    showMessage("loom sent no push key.", true);
    return;
  }

  let subscription: PushSubscription;
  try {
    // A fresh subscription each time: after "expired" the old one is dead.
    const old = await registration.pushManager.getSubscription();
    if (old !== null) await old.unsubscribe();
    subscription = await registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey,
    });
  } catch (err) {
    const reason = err instanceof Error && err.message !== "" ? err.message : String(err);
    showMessage(`The phone refused the subscription: ${reason}`, true);
    return;
  }

  // A subscription the daemon would refuse for what it lacks goes as an empty body, so the daemon
  // says why, as it did for classic.
  const stored = await api.postPush(subscriptionBody(subscription) ?? {});
  if (stored.kind !== "ok") {
    showOutcome(stored, WHAT);
    // The daemon never learned of it, so keep none the daemon cannot use.
    try {
      await subscription.unsubscribe();
    } catch {
      // Nothing more to undo.
    }
    return;
  }
  showMessage("Notifications are on.");
  used.ok = true;
}

defineCommand({
  id: "push.enable",
  title: "Turn on notifications",
  when: (ctx) => ctx.panel === "settings",
  run: async () => {
    const used: Run = { ok: false, registration: null };
    try {
      await enable(used);
    } finally {
      // Awaited: the phone's new state is read before the run ends, so the control's first render
      // after it (when runCommand clears the in-flight key) already shows it.
      await pushRunEnded(used.ok, used.registration);
    }
  },
});
```

In `internal/web/ui/src/commands/index.ts`, as Story 4 leaves it, replace:

```ts
import "./jobs";
import "./launch";
import "./device";
```

with:

```ts
import "./jobs";
import "./launch";
import "./device";
import "./push";
```

Rebuild the bundle: `pnpm -C internal/web/ui run build`. `main.tsx` imports the commands module, so the entry script changes in this task.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/commands/push.test.ts src/commands/index.test.ts`
Expected: both files pass.

Run: `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
Expected: the typecheck, lint, format check and every Vitest file pass, `dist:check` says the bundle is fresh, and the Go package prints `ok`. If `format:check` fails, run `pnpm -C internal/web/ui exec prettier --write <the files it names>` and run it again.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/commands/push.ts internal/web/ui/src/commands/push.test.ts internal/web/dist
git commit -m "feat(web): the push.enable command, turning notifications on from Settings" -- internal/web/ui/src/commands/push.ts internal/web/ui/src/commands/push.test.ts internal/web/ui/src/commands/index.ts internal/web/ui/src/commands/index.test.ts internal/web/dist
```

## Story 6: The flow board and the flow tab

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/flow/index.tsx`, `internal/web/ui/src/panels/flow/parts.tsx`, `internal/web/ui/src/panels/flow/FlowBody.tsx`, `internal/web/ui/src/panels/flow/flow.test.tsx`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
- **Interfaces:** exposes the `flow` panel, `FlowMark`, `planOf`, `usd`, `median`, `Bars`, `useClock`, and `SessionRow.tsx`'s `usd` exported; uses Story 3's `useFlow` and `flowKey`, Story 1's `fixtureFlow`, part 2's `SessionHead`, `NoSession` and `elapsed`, part 1's `definePanel`, `useSession`, `hrefFor` and `Badge`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens flow -out "$d"'`
- **Review:** deep
- **Form:** brief (a port of `flow.mjs`'s board over Story 3's read)

The new app's `flow` panel: the fifth tab of a session that has flows (an amber dot while any of them asks), whose lazy `FlowBody` is a port of the classic board (`flow.mjs`'s `Flow`) over Story 3's `useFlow`. It draws the gone, no-flow, loading and error states, the picker for two flows or more, the title and part, the unreadable, lost and phase lines, the stage strip with its document links, the question box, the stories with their cells and right-hand text, the cost box (role bar, legend, per-story comparison, unpriced line, the cost error) and the other runs. Every unknown value draws its raw text in neutral styling (spec §29, Part 3 settlement 1), and every bar takes its width or height through React's `style` prop (settlement 5). The story also adds the parts the story view and the host's flows reuse (`planOf`, `usd`, `median`, `Bars`, `useClock`, in `panels/flow/parts.tsx`) and the board's chunk to `checkLazyViews`. The story is brief: every task names the behaviour, the contract names and the tests, and the developer writes the code. None of it holds a landmine marker.

Facts the tasks are written around:

- `useFlow` (Story 3) answers `{ flow, error, loading }`: `flow` is the held board or `undefined`, `error` is `""` or the failed read's text (the held board stays drawn under it), and an empty `plan` reads nothing. The board never needs `loading`: a plan that is not empty with no `flow` and no `error` is loading.
- Part 1's `SessionTabs` (`app/shell.tsx`) already lists a panel with a `tab` that `appliesTo` the session, and keeps the flow tab under the user on the story and document routes (`tabOf`), so this story only registers the panel. Its tab link is `hrefFor("flow", { id })`: no plan, so it opens the newest flow, as classic.
- `PanelHost` draws a lazy `Body` under a `Suspense`; the panel modules in `panels/` register on import from `panels/index.ts`. The chunk is `FlowBody-<hash>.js`, and `index.html` must not name it.
- Tailwind's scan reads class names written in full: every tone class below is a whole literal in a map, never built from a string.
- A board's boxes are Info's (`rounded-xl border border-border bg-card p-3`, legend `text-[11px] tracking-wide text-muted-foreground uppercase`); colours come from the tokens (`text-attention`, `text-working`, `text-error`, `text-turn`, `text-docs`, `text-workspace`, `text-faint`, `bg-secondary`), form text is 16px and a touch target is `min-h-11`.
- The tests mock `@/api/reads`' `useFlow` as `job.test.tsx` mocks `useJobLog` (`vi.mock("@/api/reads", async (original) => ({ ...(await original<typeof import("@/api/reads")>()), useFlow: vi.fn() }))`, so `flowKey` stays real), seed session 6 of `fixtureSnapshot()` (two flows after Story 1's fixtures) through `seed` from `@/commands/testkit`, and draw the board from `fixtureFlow()` (Story 1: story 4 running with a wait, a final row `F` with a running review, one other run, one unpriced agent, a question parked on story 3). The route's hash is set with `window.history.replaceState(null, "", "#/s/6/flow")`, as the other panel tests do.

### Task 6.1: The shared parts: `planOf`, `usd`, `median`, `Bars`, `useClock`

**Files:**
- Create: `internal/web/ui/src/panels/flow/parts.tsx`
- Modify: `internal/web/ui/src/panels/fleet/SessionRow.tsx:32` (the `const usd` line, exported)
- Create: `internal/web/ui/src/panels/flow/flow.test.tsx`
- Test: `internal/web/ui/src/panels/flow/flow.test.tsx`

Names this task defines (contract, Story 6), all in `panels/flow/parts.tsx`:

- `function planOf(session: Session, plan: string): string`: `plan` when it is not empty, else the plan of the session's first flow (`session.flows` is newest first), else `""` (also for `flows` null).
- `function usd(value: number | null | undefined): string`: `""` for a value that is not a number, else `SessionRow.tsx`'s `usd` of it. That function already words a figure as `$4.71` and `$0.0312` above 0 and under a cent, so this task exports it (put `export` before `const usd` on the one line that declares it) and `parts.tsx` calls it; nothing is copied. `SessionRow.tsx` is quoted by Story 8 as this story leaves it, so change nothing else in it.
- `function median(values: number[]): number`: the middle of the sorted values, the mean of the two middle ones for an even count, `0` for none (a caller draws nothing under 2 values); it sorts a copy.
- `function Bars(props: { values: number[], current: number | null }): ReactElement`: an `aria-hidden` row `flex h-11 items-end gap-1` of `<span data-slot="bar">` elements, one per value oldest first, then one more for `current` when it is not null (it also has `data-current="true"` and the accent colour `bg-turn`; the others `bg-muted-foreground/40`), each `w-4` and with `style={{ height: "<n>%" }}`, where `n` is `max(5, value / top * 100)` and `top` is the largest of the values and `current` and `0.0001`. It draws only the bars: the caller draws a caption and draws nothing under 2 values.
- `function useClock(ms: number): number`: the time now (`Date.now()` read through a small function outside the component, as `JobBody.tsx`'s `clock` is, because the purity lint holds a component to render the same for the same props), re-read and re-rendered every `ms` by a `setInterval` that the effect clears.

The file's header comment says the parts are shared by the board, the story view and the host's flows, and that `panels/flow/index.tsx` must not import it (it would pull these out of the lazy chunks into the entry script).

- [ ] **Step 1: Write the failing tests** in the new `flow.test.tsx`: the imports (`@testing-library/react`'s `act`, `render`, `renderHook`, `screen`; `vitest`'s `afterEach`, `describe`, `expect`, `it`, `vi`; `Session` from `@/api/gen`; `fixtureSnapshot` from `@/fixtures`; `allSessions` from `@/fleet/groups`; `usd as rowUsd` from `../fleet/SessionRow`; the five names from `./parts`) and a `describe("flow parts", …)` holding:

  - `planOf: the given plan, else the newest flow's, else none`: with session 6 of `fixtureSnapshot()` (two flows), `planOf(session, "x.md")` is `x.md`, `planOf(session, "")` is `session.flows[0].plan`; with `flows: []` and with `flows: null`, `planOf(session, "")` is `""`.
    Fails without: `planOf: the given plan, else the newest flow's, else none`: the given plan ignored, the last flow taken (`[1]`), or a null `flows` not guarded (it throws).
  - `usd: words a figure as the fleet row does, and nothing for no number`: `usd(4.71)` is `$4.71`, `usd(0.0312)` is `$0.0312`, `usd(0)` is `$0.00`, `usd(0.01)` is `$0.01`, `usd(undefined)` and `usd(null)` are `""`, and `usd(0.0312)` equals `rowUsd(0.0312)`.
    Fails without: `usd: words a figure as the fleet row does, and nothing for no number`: the empty-string guard removed (`usd(null)` is `$NaN` or throws), or the four-decimals rule lost (`$0.03`).
  - `median: the middle value, the mean of the middle two, and none for none`: `[3, 1, 2]` is `2`, `[4, 1, 3, 2]` is `2.5`, `[]` is `0`, and the array passed in is left in its order.
    Fails without: `median: the middle value, the mean of the middle two, and none for none`: the sort removed (the unsorted `[3, 1, 2]` answers `1`), a sort of the argument itself (the order check fails), or the even case taking one middle value.
  - `Bars: one bar a value, the current one after them in the accent colour, each height an exact share of the tallest`: render `<Bars values={[1, 2, 4]} current={2} />`: four `[data-slot="bar"]` elements, the fourth with `data-current="true"` and the `bg-turn` class and no other; heights `25%`, `50%`, `100%`, `50%` read from each element's `style.height`; the row is `aria-hidden`. With `current={null}` there are three bars. With `values={[0.001, 4]}` the first is `5%`. With `values={[2]}` and `current={8}` the heights are `25%` and `100%`. With `values={[1, 3]}` the first height, parsed as a number, is `33.333…` to 2 places (an exact share, not a step of 5).
    Fails without: `Bars: one bar a value, the current one after them in the accent colour, each height an exact share of the tallest`: `current` left out of the bar count (three bars) or out of `top` (the `[2]` and `8` heights), the 5% floor removed (the `0.001` bar is `0.025%`), the heights rounded to steps of 5 (the `[1, 3]` case reads `35`), or the height set through a class instead of `style` (`style.height` is empty).
  - `useClock: the time now, moving every ms and no sooner, and stopped on unmount`: with `vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] })` and the system time set, `renderHook(() => useClock(30_000))` answers that time; `act(() => vi.advanceTimersByTime(29_000))` leaves it, a further `act(() => vi.advanceTimersByTime(1_000))` moves it to 30 s later; `renderHook(() => useClock(1_000))` moves after 1 s; after `unmount`, `vi.getTimerCount()` is `0`.
    Fails without: `useClock: the time now, moving every ms and no sooner, and stopped on unmount`: the interval hard-coded (the `1_000` case never moves, or the `30_000` one moves at 29 s), the state never set (the value stays), or the cleanup removed (the timer count stays 1).

  Reset real timers in an `afterEach` (`vi.useRealTimers()`).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx
```

Expected: the file fails to load with `Failed to resolve import "./parts" from "src/panels/flow/flow.test.tsx"`.

- [ ] **Step 3: Implement** `parts.tsx` as described, and export `usd` in `SessionRow.tsx`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx src/panels/fleet/fleet.test.tsx
```

Expected: both files pass (the fleet's tests read `flowChip`, which still uses the now exported `usd`).

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/flow/parts.tsx internal/web/ui/src/panels/flow/flow.test.tsx
git commit -m "feat(web): the flow views' shared parts: planOf, usd, median, Bars and useClock" -- internal/web/ui/src/panels/flow/parts.tsx internal/web/ui/src/panels/flow/flow.test.tsx internal/web/ui/src/panels/fleet/SessionRow.tsx
```

### Task 6.2: `FlowBody`: the states, the picker and the header lines

**Files:**
- Create: `internal/web/ui/src/panels/flow/FlowBody.tsx`
- Modify: `internal/web/ui/src/panels/flow/flow.test.tsx`
- Test: `internal/web/ui/src/panels/flow/flow.test.tsx`

Names this task defines (private to the story): the module's `default export function FlowBody(props: PanelProps): ReactElement` (`React.lazy` needs a default export), and its private parts `Picker` and the header lines. It uses Story 3's `useFlow` and `flowKey` (`@/api/reads`), this story's `planOf` and `useClock`, `useSession` (`@/store/fleet`), `NoSession` (`@/session/NoSession`), `elapsed` (`@/session/jobs`: `elapsed(from: number, to: number)` in milliseconds, `""` for a start that did not parse) and `hrefFor` (`@/app/routes`).

Behaviour. `FlowBody` calls its hooks first and unconditionally: `useSession(props.sessionId)`, `const now = useClock(30_000)`, and `useFlow({ session: props.sessionId ?? 0, plan, key })` with `plan = planOf(session, props.search.plan ?? "")` and `key = flowKey(session)` when the session is listed, else `plan` `""` and `key` `""` (an empty plan reads nothing). Then, in this order:

- no session in the snapshot: `<NoSession />` (it says "This session is gone."; Story 9 makes it prop-less and route-reading, so pass nothing);
- `plan` empty: a `<p role="status">` "No flow in this session's worktree.", muted, centred;
- no `flow` and no `error`: a `<p role="status">` "Loading…", muted, centred;
- no `flow` and an `error`: a `<p role="alert">` with the error text in `text-error`;
- else the board, a `section` `flex flex-col gap-4 px-4 pb-6`: the picker, the title line, the error line, the unreadable line, (the strip, from Task 6.3), the lost line, the phase line, (the question, the stories, the cost box and the other runs, from Tasks 6.3 and 6.4).

The pieces of this task:

- Picker: only when `session.flows` holds 2 or more. A `<label>` with the muted word "flow" and a native `<select>` (the tokens' classes as the Merge sheet's select has: `h-11 w-full rounded-lg border border-input bg-background px-3 text-[16px] text-foreground`) whose options are each flow's `title`, else its `plan`, valued by `plan`; its `value` is the board's plan. A change calls `window.location.assign(hrefFor("flow", { id: String(session.id) }, { plan: <picked> }))` (the lint refuses assigning `window.location.hash`), so the picked plan draws its own held board or "Loading…", never the previous plan's.
- Title line: a `<p>` `font-semibold break-words` with `summary.title`, else `summary.plan`, and, when `summary.part` is not empty, a muted ` <span>` after it with the part.
- Error line: when `error` is not empty and a board is held, a small `text-error` `<p role="alert">` with the text, above the board.
- Unreadable line: when `summary.unreadable` is not empty, a small `text-error` line `unreadable: <text>`.
- Lost line: when `summary.status` is `lost`, a small `text-error` line `The run's engine has ended without it: loom flow run resume <summary.run>`.
- Phase line: when `summary.phase` is not empty and the board has no `question`, a small muted line `<summary.status> · <summary.phase>`, followed by ` · <elapsed>` when `flow.start` parses, the elapsed time from `flow.start` to `flow.end` (when it parses) else to `now`.

- [ ] **Step 1: Write the failing tests** in `flow.test.tsx`: the `useFlow` mock and its imports (`FlowBody` default from `./FlowBody`, `useFlow` and `flowKey` from `@/api/reads`, `PanelProps` from `@/app/panels`, `hrefFor` from `@/app/routes`, `fixtureFlow` from `@/fixtures`, `makeSession`, `resetFleet`, `seed` from `@/commands/testkit`, `userEvent` from `@testing-library/user-event`), `NOW` as `Date.parse("2026-11-19T12:00:00Z")` with `vi.useFakeTimers({ toFake: ["Date"] })` and `vi.setSystemTime(NOW)` in a `beforeEach` (`vi.mocked(useFlow).mockReturnValue({ flow: undefined, error: "", loading: false })`), an `afterEach` (`vi.useRealTimers()`, `vi.restoreAllMocks()`, `resetFleet()`, the hash reset to `window.location.pathname`), and a helper `show(opts: { session?: Session | null; flow?: Flow; error?: string; search?: Record<string, string> })` that seeds the session (default: session 6 of `fixtureSnapshot()`; `null` seeds none), sets the `useFlow` mock's answer from `flow` and `error`, sets the hash `#/s/6/flow`, and renders `<FlowBody sessionId={6} params={{ id: "6" }} search={opts.search ?? {}} />`. Then a `describe("FlowBody", …)` holding:

  - `a session the snapshot does not list says it is gone and reads nothing`: `show({ session: null })` draws "This session is gone."; `useFlow` was called with `plan: ""`.
    Fails without: `a session the snapshot does not list says it is gone and reads nothing`: the session check removed (the board draws or the page throws on the missing session), or a plan taken from the route's search for a session that is not listed (the call's plan is not `""`).
  - `a session with no flow and no plan says so and reads nothing`: session 6 with `flows: []` and no `plan` search: "No flow in this session's worktree." and a `useFlow` call with `plan: ""`; with `flows: null` the same.
    Fails without: `a session with no flow and no plan says so and reads nothing`: the empty-plan branch removed (it draws "Loading…" forever, as classic), or `planOf` bypassed (a `undefined` plan).
  - `reads the route's plan, else the newest flow's, keyed by the session's flows`: with no `plan` search the call is `{ session: 6, plan: <session.flows[0].plan>, key: flowKey(session) }`; with `search: { plan: <flows[1].plan> }` its plan is that one and its key is unchanged.
    Fails without: `reads the route's plan, else the newest flow's, keyed by the session's flows`: the route's plan ignored, `key` left out or a constant (the `flowKey` of the seeded session differs), or the session id not passed.
  - `draws Loading… until the board arrives, and the read's error when none is held`: no `flow` and no `error` is "Loading…"; no `flow` and `error: "loom answered 500"` draws that text (in `role="alert"`) and no "Loading…".
    Fails without: `draws Loading… until the board arrives, and the read's error when none is held`: the error branch removed (it says "Loading…" over a failure), or "Loading…" drawn beside the error.
  - `draws the title with its part, the plan path without a title, and the error over a held board`: the fixture's title is a heading-weight `<p>`; with `summary.part` `"part 2 of 3"` that text is beside it; with `title: ""` the plan path draws; with `error: "loom answered 500"` and the board held, the error and the title are both on screen.
    Fails without: `draws the title with its part, the plan path without a title, and the error over a held board`: the `|| plan` fallback removed, the part dropped, or the error replacing the board.
  - `draws the unreadable line and the lost line`: `summary.unreadable: "bad plan"` draws `unreadable: bad plan`; `summary.status: "lost"` with `run: "r1"` draws `The run's engine has ended without it: loom flow run resume r1`; neither with the fixture's values.
    Fails without: `draws the unreadable line and the lost line`: either line removed, the lost line keyed on a different status, or drawn always (the last assertion).
  - `draws the phase line with the time since the run started, until a question is open, and the time moves with the clock`: with `question: null`, `phase: "story 3"`, `status: "running"`, `start` 100 s before `NOW` (an ISO string) and `end: ""`, the line `running · story 3 · 1m 40s` is drawn; with the fixture's question present it is not; with `end` 40 s after `start` it reads `· 40s`; with `vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] })` an `act(() => vi.advanceTimersByTime(30_000))` moves the first one to `2m 10s`.
    Fails without: `draws the phase line with the time since the run started, until a question is open, and the time moves with the clock`: the `question` guard removed, `end` ignored (the 40 s case reads the time since `NOW`), or the clock not used (the line stays `1m 40s`).
  - `the picker shows for two flows or more, and picking one opens its board`: session 6 (two flows) draws a `combobox` named "flow" with two options (the first flow's title, the second's title) and the board's plan selected; `userEvent.selectOptions` of the second plan sets `window.location.hash` to `hrefFor("flow", { id: "6" }, { plan: <second plan> })`; a session with one flow draws no `combobox`.
    Fails without: `the picker shows for two flows or more, and picking one opens its board`: the `length < 2` test inverted or removed, the plan left out of the address (the hash has no `plan`), or the option valued by title.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx
```

Expected: the file fails to load with `Failed to resolve import "./FlowBody" from "src/panels/flow/flow.test.tsx"`.

- [ ] **Step 3: Implement** `FlowBody.tsx` as described (the board's remaining boxes arrive in the next two tasks; the section holds what this task draws).

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx
```

Expected: every test in the file passes.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/flow/FlowBody.tsx
git commit -m "feat(web): the flow board's states, picker and header lines" -- internal/web/ui/src/panels/flow/FlowBody.tsx internal/web/ui/src/panels/flow/flow.test.tsx
```

### Task 6.3: The stage strip, the question box and the stories

**Files:**
- Modify: `internal/web/ui/src/panels/flow/FlowBody.tsx`
- Modify: `internal/web/ui/src/panels/flow/flow.test.tsx`
- Test: `internal/web/ui/src/panels/flow/flow.test.tsx`

Names (private to the story), all in `FlowBody.tsx`: the components `Strip`, `Question`, `Cells`, `StoryRow`, and the maps `STAGES` (`design`, `spec`, `draft`, `run`, `record`), `KINDS` (`story`: `dev`, `gates`, `review`, `fix`, `docs`; `P`: `premise`; `F`: `gates`, `review`, `fix`), `LABEL` (`dev` `dev`, `gates` `gate`, `review` `rev`, `fix` `fix`, `docs` `doc`, `premise` `prem`) and the tone maps. It uses `hrefFor` (`@/app/routes`) and `usd` from `./parts`.

Behaviour, placed in the board's `section` in the classic order: the strip after the unreadable line, the question after the phase line, the stories box after it.

- Strip: a `<nav aria-label="Stages" className="flex gap-0.5">` of the five `STAGES` in order, each the board's stage of that name or `{ name, state: "todo", detail: "", doc: "" }` when `stages` lacks it (null too). Its word is the stage's name; for `run`, when `summary.run` is not empty, `run <summary.done>/<summary.total>`; and ` ✓` after it when the state is `done`. A stage with a `doc` is an `<a>` to `hrefFor("flowDoc", { id, doc: stage.doc }, { plan })` (`plan` the board's plan), else a `<span>`; both carry `title={stage.detail}`, `flex-1`, `min-h-11`, centred, `text-[11px]`, truncated. Tones (a map of whole class strings): `done` `text-working`, `now` `bg-secondary text-foreground`, `todo` `text-faint`, `unreadable` `text-error`; any other state `text-muted-foreground` and its raw text after the word, as `design (paused)`.
- Question: only with `flow.question`: a box with an attention border (`border-attention/40`), the legend `parked · <kind>` plus ` · story <story>` when `story` is not empty (the kind's raw text, whatever it is), the question's text in `whitespace-pre-wrap break-words`, and a link `open manager ›` to `hrefFor("turns", { id })` styled as a secondary button in the attention colour, `min-h-11`.
- Stories box: the legend `stories`, then the rows in this order: `flow.premise` when not null, each of `flow.stories` (null as none), `flow.final` when not null. A row is an `<a>` (`flex min-h-11 items-center gap-2 border-b border-border last:border-b-0`) to `hrefFor("flowStory", { id, story: row.id }, { plan })` holding the id (`w-6 flex-none font-bold`, `text-faint` for `P` and `F`), the cells, and the right-hand text (`ml-auto text-right text-xs truncate`, muted once the row has started, faint before).
- Right-hand text: `premises` for `P`, `final` for `F`; else, for a row that has started (its `steps` is not empty), the commits as `<n>c` when `commits` is not 0, then the row's spend (`flow.costs.by_story[row.id]`, a number) as `usd(spend)`, joined with ` · ` when both are there, `""` when neither; else (not started) `<tier> · <review>` with an empty one left out.
- Cells: each kind of the row's kinds (`P` → `premise`, `F` → its three, any other id → the story five) is a `<span>` `min-w-[34px] rounded-sm px-1 text-center text-[10px] leading-[18px]` with `aria-label="<LABEL>: <state>"`, the state being `row.cells[kind]` or `todo` when missing. Its text is the label, with `?` after it for `asks`, and `—` alone for `skipped`. Tones: `running` `bg-secondary text-foreground`, `passed` `bg-diff-add text-working`, `failed` `bg-diff-del text-error`, `asks` `bg-attention/15 text-attention`, `skipped` `text-faint`, `todo` `bg-secondary text-faint`; any other state `bg-secondary text-muted-foreground` and its raw text as the cell's text (the cell grows: it has `min-w`, not a fixed width).

- [ ] **Step 1: Write the failing tests** in `flow.test.tsx`, inside the `describe("FlowBody", …)` (or a sibling `describe` using the same `show`), with `fixtureFlow()`'s board (called after the system time is set, so its times are relative to `NOW`) and `plan` the first flow's plan of session 6:

  - `the stage strip: five stages in order, each with its document where it has one`: the `Stages` navigation holds, in order, `design ✓`, `spec ✓`, `draft ✓`, `run 2/5`, `record`; the first three are links whose `href`s are `hrefFor("flowDoc", { id: "6", doc: "spec" }, { plan })`, `…doc: "skeleton"…` and `…doc: "plan"…`, each `title` its stage's `detail`; `run 2/5` and `record` are not links (the board's `run` and `record` have no `doc`).
    Fails without: `the stage strip: five stages in order, each with its document where it has one`: the order taken from `stages` (a shuffled `stages` array changes it), the `✓` or the run counts dropped, the plan left out of a link, or every stage a link.
  - `the stage strip: a stage the board lacks is to come, and an unknown state draws its raw state in neutral styling`: with `stages: null` the five names draw, none a link, none with `✓`, each `text-faint`; with the first stage's `state` set to `"paused"` (cast) the text is `design (paused)` with `text-muted-foreground` and not `text-working`.
    Fails without: `the stage strip: a stage the board lacks is to come, and an unknown state draws its raw state in neutral styling`: `stages` null not guarded (it throws), the fallback stage missing (fewer than five items), or an unknown state given a known tone.
  - `the question box: the kind and story, the text, and the way to the manager's conversation`: the fixture's question draws the legend `parked · blocked-decision · story 3`, its text, and a link `open manager ›` to `#/s/6`; with `story: ""` the legend is `parked · blocked-decision`; with `question: null` none of it draws.
    Fails without: `the question box: the kind and story, the text, and the way to the manager's conversation`: the story suffix unconditional (`· story ` with nothing after it), the link pointing at the board, or the box drawn for a null question.
  - `the stories box: the premise row, the stories and the final row, each a link to its row`: the box's legend is `stories` and its links are, in order, the rows `P`, `1`, `2`, `3`, `4`, `5`, `F` with `href`s `hrefFor("flowStory", { id: "6", story: <id> }, { plan })`; a board with `premise: null` and `final: null` draws only the five stories.
    Fails without: `the stories box: the premise row, the stories and the final row, each a link to its row`: the premise or final row skipped, the order changed (final first), or the plan left out of the link.
  - `the stories' right-hand text: commits and spend once started, tier and review before, premises and final`: story 1 `7c · $1.41`, story 2 `4c · $1.10`, story 3 `$0.62` (no commits, spend 0.62), story 4 (started, no commits, no spend) none, story 5 `deep · full`, `P` `premises`, `F` `final`; with story 1's `commits` 0 and no `by_story` entry the text is empty, and with story 5's `review` `""` it is `deep`.
    Fails without: `the stories' right-hand text: commits and spend once started, tier and review before, premises and final`: the `started` test inverted, the ` · ` joiner drawn when only one part is there (` · $0.62`), `P` and `F` given their commits, or the spend read from `by_agent`.
  - `the cells: each kind's word, ? for asks, a dash for skipped, todo for a missing cell, by row kind`: story 3's dev cell is `dev?` with `aria-label` `dev: asks`; story 2's fix is `—` with `aria-label` `fix: skipped`; story 1 has five cells labelled `dev`, `gate`, `rev`, `fix`, `doc`; `P` has one, `prem` (`premise: passed`); `F` has `gate`, `rev`, `fix` (`gates: passed`, `review: running`, `fix: todo`); a story whose `cells` is `null` draws five `todo` cells.
    Fails without: `the cells: each kind's word, ? for asks, a dash for skipped, todo for a missing cell, by row kind`: the kinds not chosen by row id (`P` or `F` showing the five), `LABEL` words changed, the `?` or the dash missing, or a null `cells` not guarded.
  - `a cell with an unknown state draws its raw state in neutral styling`: story 1's `dev` cell set to `"weird"` (cast) draws the text `weird`, `aria-label` `dev: weird` and `text-muted-foreground`, and not the `passed` tone class.
    Fails without: `a cell with an unknown state draws its raw state in neutral styling`: a lookup that falls through to the `passed` or `todo` tone, or the label drawn in place of the raw state.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx
```

Expected: the new tests fail (no `Stages` navigation, no `stories` legend), the Task 6.2 tests still pass.

- [ ] **Step 3: Implement** the three boxes in `FlowBody.tsx` as described.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx
```

Expected: every test in the file passes.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the flow board's stage strip, question box and stories" -- internal/web/ui/src/panels/flow/FlowBody.tsx internal/web/ui/src/panels/flow/flow.test.tsx
```

### Task 6.4: The cost box and the other runs

**Files:**
- Modify: `internal/web/ui/src/panels/flow/FlowBody.tsx`
- Modify: `internal/web/ui/src/panels/flow/flow.test.tsx`
- Test: `internal/web/ui/src/panels/flow/flow.test.tsx`

Names (private to the story), in `FlowBody.tsx`: `CostBox`, `OtherRuns`, the `ROLE_CLASS` map (role to one of `dev`, `review`, `docs`, `manager`, `other`: `junior-dev`, `intermediate-dev` and `senior-dev` `dev`; `code-quality-reviewer`, `story-reviewer` and `spec-reviewer` `review`; `doc-writer` `docs`; `premise-check` `other`; `manager` `manager`; any other role `other`) and the maps of whole class strings for each class's bar colour (`dev` `bg-turn`, `review` `bg-working`, `docs` `bg-docs`, `manager` `bg-workspace`, `other` `bg-muted-foreground`) and legend text (`text-turn`, `text-working`, `text-docs`, `text-workspace`, `text-muted-foreground`). It uses `Bars` and `usd` from `./parts`. They are drawn after the stories box, the cost box and then the other runs.

Behaviour of `CostBox` (props: `costs: FlowCosts`, `hasRun: boolean`, with `hasRun` true when `summary.run` is not empty):

- `costs.error` not empty: only a box with the legend `cost` and the error text in `text-error`.
- Otherwise the legend `cost · list prices`, then a line:
  - `run <usd(spent)>` (the figure bold) when `hasRun`, else a muted `no run yet`;
  - a muted ` · flow <usd(flow)>` when `flow_parts > 0`, with ` (<flow_parts> of 3 parts)` after the figure when `flow_parts < 3`;
  - a muted ` · ~<usd(projection)> projected` when `projection` is a number, else, when `hasRun` and `why` is not empty, a faint ` · <why>`.
- The role bar and its legend row, only when the sum of the roles with a figure above 0 is above 0: the `by_role` entries above 0 (null as none) summed by their `ROLE_CLASS` class; one `<span data-slot="role-segment" data-role="<class>">` for each class that has a sum, in the order `dev`, `review`, `docs`, `manager`, `other`, in an `aria-hidden` flex bar (`h-2 overflow-hidden rounded-sm bg-secondary`), each `style={{ width: "<share * 100>%" }}` (the exact percentage: the class's sum over the total); and under it a wrapping row of `■ <class> <usd(sum)>` keys, each in its class's legend text colour, in the same order.
- `unpriced` (null as none) with entries: a small faint line `not priced yet: <n> running`.
- The comparison: when `past` (null as none) plus one for a `this` that is a number come to 2 or more values, `<Bars values={past} current={costs.this} />` and a small faint `<p>` whose text is `per story: last <past.length> runs`, then `, median <usd(median)>` when `costs.median` is a number, then ` · this run <usd(this)>` (the this-run figure in `text-turn`) when `this` is a number. (`median` is the server's figure, not the `median` helper's.) Under 2 values, nothing.

`OtherRuns`: nothing for `others` null or empty; else a box with the legend `other runs` and a row per run, the run's `id` on the left and, muted and right-aligned, `unreadable` when its `unreadable` is not empty, else its `status` (raw).

- [ ] **Step 1: Write the failing tests** in `flow.test.tsx`, with `fixtureFlow()`'s board (costs: spent 4.71; `by_role` dev 2.1, review 1.5, premise-check 0.12, manager 0.99; flow 19.9 over 3 parts; projection 9.8; past five runs, median 2.52, this 2.36; one unpriced agent after Story 1; `others` one stopped run):

  - `the cost box: the run's spend, the whole flow, the projection and the list-price legend`: the legend `cost · list prices`; the text `run $4.71`, `flow $19.90` and `~$9.80 projected` are on screen; there is no `of 3 parts`.
    Fails without: `the cost box: the run's spend, the whole flow, the projection and the list-price legend`: any figure not passed through `usd`, or the legend reworded.
  - `the cost box: a run still to come, a flow priced from fewer parts, a projection that is missing and why`: with `summary.run: ""` the box says `no run yet` and not `run $`; with `flow_parts: 2` the flow figure is followed by ` (2 of 3 parts)`; with `flow_parts: 0` no `flow` figure draws; with `projection: null` and `why: "no history"` and a run, ` · no history` draws, and with no run it does not.
    Fails without: `the cost box: a run still to come, a flow priced from fewer parts, a projection that is missing and why`: the `hasRun` test missing (a `run $0.00` for a board with no run), the parts note unconditional or off by one (`< 3` as `<= 3`), or `why` drawn without a run.
  - `the cost box: a role bar of exact shares, a segment for each role class and a legend in order`: four `[data-slot="role-segment"]` elements with `data-role` `dev`, `review`, `manager`, `other` in that order (no `docs`), whose `style.width` values are `2.1 / 4.71`, `1.5 / 4.71`, `0.99 / 4.71` and `0.12 / 4.71` as percentages (`toBeCloseTo` to 3 places); the keys `■ dev $2.10`, `■ review $1.50`, `■ manager $0.99`, `■ other $0.12` in that order; the `premise-check` figure is in `other`; a role `mystery-agent` (cast in) of 1 adds to `other` (`$1.12`).
    Fails without: `the cost box: a role bar of exact shares, a segment for each role class and a legend in order`: the shares rounded to 5% steps (the `toBeCloseTo` check), the classes drawn in the map's order rather than the fixed one, `premise-check` given its own class, or an unknown role dropped.
  - `the cost box: no bar for no spend, and zero roles left out`: with `by_role: null` and with `by_role: { "manager": 0 }` no segment and no legend key draws, and the rest of the box still does.
    Fails without: `the cost box: no bar for no spend, and zero roles left out`: the sum guard removed (a bar of `NaN%`), or a zero role given a segment.
  - `the cost box: the unpriced line counts the agents still running`: after Story 1's fixture (one unpriced agent) `not priced yet: 1 running` draws; with `unpriced: []` and with `null` it does not; with three, `not priced yet: 3 running`.
    Fails without: `the cost box: the unpriced line counts the agents still running`: the line drawn for an empty list, or a null list not guarded.
  - `the cost box: the per-story comparison draws its bars and its caption`: five bars plus the current one (`[data-slot="bar"]`, the last `data-current`), and a `<p>` whose text content is exactly `per story: last 5 runs, median $2.52 · this run $2.36`; with `median: null` it is `per story: last 5 runs · this run $2.36`; with `this: null` it is `per story: last 5 runs, median $2.52` and five bars.
    Fails without: `the cost box: the per-story comparison draws its bars and its caption`: `current` not passed (five bars), the median or the this-run part not conditional (`median $` followed by nothing), or the past runs' count taken from the bars (it would say 6).
  - `the cost box: under two values no comparison draws`: `past: []` with `this: null` and `past: [1.5]` with `this: null` draw no bars and no `per story` text; `past: [1.5]` with `this: 2.36` draws two bars; `past: null` with `this: 2.36` draws none.
    Fails without: `the cost box: under two values no comparison draws`: the guard testing `past.length > 0` alone (classic's rule: the lone-bar cases draw), or a null `past` not guarded.
  - `the cost box: a cost error is all it says`: with `costs.error: "no cost records"` the box draws `cost` and `no cost records` and none of `run`, `flow`, a bar or a key.
    Fails without: `the cost box: a cost error is all it says`: the error branch drawn beside the figures, or missing.
  - `the other runs: a box of each other run's id and status, unreadable when it is, none without`: the fixture's run draws its id `2026-11-08-flow-visualisation` and `stopped` under the legend `other runs`; a run with `unreadable: "bad"` shows `unreadable` and not its status; `others: []` and `others: null` draw no `other runs` legend; a status the app does not know (`"weird"`) draws as it is.
    Fails without: `the other runs: a box of each other run's id and status, unreadable when it is, none without`: the status shown for an unreadable run, the box drawn for an empty list, or the status mapped through a table (the unknown one vanishes).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx
```

Expected: the new tests fail (no `cost · list prices` legend, no `other runs` legend), the earlier ones pass.

- [ ] **Step 3: Implement** `CostBox` and `OtherRuns` in `FlowBody.tsx` as described.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx
```

Expected: every test in the file passes.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the flow board's cost box and other runs" -- internal/web/ui/src/panels/flow/FlowBody.tsx internal/web/ui/src/panels/flow/flow.test.tsx
```

### Task 6.5: The `flow` panel, its tab and its mark

**Files:**
- Create: `internal/web/ui/src/panels/flow/index.tsx`
- Modify: `internal/web/ui/src/panels/index.ts` (one import after the last one)
- Modify: `internal/web/ui/src/panels/flow/flow.test.tsx`
- Test: `internal/web/ui/src/panels/flow/flow.test.tsx`

Names this task defines (contract, Story 6), in `panels/flow/index.tsx`: `function FlowMark(props: { session: Session }): ReactElement | null` and `export const flowPanel`. The module's other names are private: `const FlowBody = lazy(() => import("./FlowBody"))`. Story 7 adds its two panels to this file beside `flowPanel`, as `jobPanel`'s file and `mockupPanel`'s have their lazy constants.

- `FlowMark`: nothing when no flow of `session.flows` (null as none) has `asks` other than `""`; else an amber dot as `MockupMark` draws its dot: `<span role="img" aria-label="asks" className="ml-1.5 inline-block size-2 rounded-full bg-attention" />`.
- `flowPanel = definePanel({ id: "flow", title: () => "Flow", appliesTo, tab: { order: 5, label: "flow", Mark: FlowMark }, Head: SessionHead, Body: FlowBody })`, where `appliesTo` is true while `session.flows` holds at least one flow (null as none). `SessionHead` is `@/session/SessionHead`.
- The module imports nothing from `./parts` (that file would be pulled into the entry script out of the lazy chunk); `FlowBody` is the only module it loads, lazily.
- `panels/index.ts` gains `import "./flow";` after its last import line, so the panel registers.

- [ ] **Step 1: Write the failing tests** in `flow.test.tsx` (imports: `flowPanel`, `FlowMark` from `./index`; `panelFor` from `@/app/panels`; `PanelHost`, `SessionTabs` from `@/app/shell`; `SessionHead` from `@/session/SessionHead`; `within` from `@testing-library/react`; and `import "@/panels"` so the other tabs are registered), a `describe("the flow panel", …)` holding:

  - `FlowMark: an amber dot while any flow asks, nothing otherwise`: the fixture's session 6 (its first flow asks) draws a `role="img"` named `asks` with `bg-attention`; the same session with every flow's `asks` set to `""` draws an empty container; so does `flows: null`; a session whose second flow only asks draws the dot.
    Fails without: `FlowMark: an amber dot while any flow asks, nothing otherwise`: the test on `status` rather than `asks`, only the first flow read (the second-flow case), or a null `flows` not guarded.
  - `the flow panel: the fifth tab for a session with flows, under the session's head, its body a lazy chunk`: `panelFor("flow")` is `flowPanel`; its `tab` equals `{ order: 5, label: "flow", Mark: FlowMark }`; its `Head` is `SessionHead`; `appliesTo` is true for session 6, false for `flows: []` and for `flows: null`.
    Fails without: `the flow panel: the fifth tab for a session with flows, under the session's head, its body a lazy chunk`: the panel left unregistered (`panelFor` is undefined, the `panels/index.ts` import missing), the order or label changed, `appliesTo` always true, or the head not `SessionHead`.
  - `the session's tabs: the flow tab is last, links to the board with no plan and lights on the board`: with the hash `#/s/6/flow`, `<SessionTabs session={session6} />`'s last link is `flow` with `href` `#/s/6/flow`, `aria-current` `page`, and the `asks` dot inside it; on `#/s/6/info` it has no `aria-current`; a session with `flows: []` has no `flow` link.
    Fails without: `the session's tabs: the flow tab is last, links to the board with no plan and lights on the board`: the tab order below the info tab's, a plan in the link, or `appliesTo` ignoring `flows`.
  - `the flow panel draws the board through PanelHost once its chunk loads`: with the `useFlow` mock answering the fixture board and session 6 seeded, `render(<PanelHost panel="flow" props={…} />)` shows, after `await screen.findByText(<the board's title>)`, the legend `stories`.
    Fails without: `the flow panel draws the board through PanelHost once its chunk loads`: the body not the default export of `FlowBody` (the lazy import fails into the error boundary), or the panel's `Body` not lazy-wrapped from `./FlowBody`.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx
```

Expected: the file fails to load with `Failed to resolve import "./index" from "src/panels/flow/flow.test.tsx"`.

- [ ] **Step 3: Implement** `index.tsx` as described and add the import to `panels/index.ts`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/flow.test.tsx src/app/shell.test.tsx src/app/panels.test.ts
```

Expected: all three files pass (the shell's and the registry's tests define their own panels and read no `flow` panel).

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/flow/index.tsx
git commit -m "feat(web): the flow panel, its tab and its asks mark" -- internal/web/ui/src/panels/flow/index.tsx internal/web/ui/src/panels/index.ts internal/web/ui/src/panels/flow/flow.test.tsx
```

### Task 6.6: `FlowBody` is a chunk of its own: `checkLazyViews`, the bundle and the screenshot

**Files:**
- Modify: `internal/web/static_budget_test.go` (`checkLazyViews` and its table test)
- Modify: `internal/web/dist/` (rebuilt)
- Test: `internal/web/static_budget_test.go`

Behaviour. `checkLazyViews`' name list (the `[]string{"JobBody", "MockupBody"}` literal in its `for` loop) gains `"FlowBody"` at its end, and the function's doc comment names the flow board beside the job and mockup screens. Later lazy stories append their own stems after it (Stories 7 to 10 each add one), so add only this one. In `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`:

- the `all` slice gains `"/assets/FlowBody-d.js"` at its end;
- the table gains two cases after the existing "no job chunk" one: `no flow chunk`, built with `build(entry, "/assets/index-a.js", "/assets/JobBody-b.js", "/assets/MockupBody-c.js")` and the wanted text `FlowBody`; and `a preloaded flow chunk`, built with `build(entry+`<link rel="modulepreload" crossorigin href="/assets/FlowBody-d.js">`, all...)` and the wanted text `/assets/FlowBody-d.js`;
- the existing cases stay as they are.

The test reads the committed bundle, so the bundle is rebuilt in this task: `pnpm run build` writes `internal/web/dist/` (the Vite build puts `FlowBody` in `assets/FlowBody-<hash>.js`, imported lazily, so `index.html` does not name it).

- [ ] **Step 1: Write the failing tests**: edit the table test as above first. The two new table cases pass at once (they test the function's list with fabricated assets), so the failing one is the bundle's: with `"FlowBody"` in the list, `TestDist_LazyViewsAreChunksOfTheirOwn` reads the committed bundle, which has no `FlowBody` chunk yet.
  - `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/no_flow_chunk`: the assets lack `FlowBody` and the error names it.
    Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/no_flow_chunk`: `"FlowBody"` removed from the list (no error, `want` not found).
  - `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/a_preloaded_flow_chunk`: `index.html` preloads `FlowBody-d.js` and the error names it.
    Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/a_preloaded_flow_chunk`: `"FlowBody"` removed from the list, or the page check dropped.
  - `TestDist_LazyViewsAreChunksOfTheirOwn`: the committed bundle holds exactly one `FlowBody-<hash>.js` that `index.html` does not name.
    Fails without: `TestDist_LazyViewsAreChunksOfTheirOwn`: the panel importing `FlowBody` statically (it lands in the entry script and no chunk of that name exists), or the bundle not rebuilt after this story's source changes.

- [ ] **Step 2: Run it to see it fail**

```bash
CGO_ENABLED=0 go test ./internal/web/ -run 'TestDist_LazyViewsAreChunksOfTheirOwn|TestCheckLazyViews'
```

Expected: `TestDist_LazyViewsAreChunksOfTheirOwn` fails with `the bundle has no lazy chunk for FlowBody: its panel is imported statically, or the build renamed it`; the table's cases pass.

- [ ] **Step 3: Implement** the list, comment and table edits, then rebuild the bundle and check the chunk, the entry page and the first-load budget:

```bash
pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build
ls internal/web/dist/assets/FlowBody-*.js
bash -c '! grep -q FlowBody internal/web/dist/index.html'
```

Expected: the build succeeds, `ls` prints one `internal/web/dist/assets/FlowBody-<hash>.js` path and the `grep` check exits 0 (the page does not name the chunk).

- [ ] **Step 4: Run it to see it pass**, with the story's full gates and the screenshot of the flow screen under the real Content-Security-Policy (it exits 1 on a console error or an unanswered route):

```bash
bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens flow -out "$d" && echo "$d/flow.png"'
```

Expected: every check passes (`dist:check` included), `ok github.com/aidan-bailey/loom/internal/web` (`TestDist_FirstLoadIsWithinBudget` among them) and the last line prints the path of `flow.png`. Read that PNG: it shows the picker (two flows), the stage strip, the parked question box, the stories with story 4 running and the final row, the cost box with its role bar and comparison, and the other run.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "build(web): the flow board is a chunk of its own, pinned in checkLazyViews, and the bundle rebuilt" -- internal/web/static_budget_test.go internal/web/dist
```

## Story 7: The flow story and document views

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/flow/index.tsx`, `internal/web/ui/src/panels/flow/FlowStoryBody.tsx`, `internal/web/ui/src/panels/flow/FlowDocBody.tsx`, `internal/web/ui/src/panels/flow/story.test.tsx`, `internal/web/ui/src/panels/flow/doc.test.tsx`, `internal/web/static_budget_test.go`, `internal/web/dist/`
- **Interfaces:** exposes the `flowStory` and `flowDoc` panels; uses Story 3's `useFlow`, `flowKey` and `useFlowDoc`, Story 6's `planOf`, `usd` and `useClock`, Story 1's `fixtureFlow` and `fixtureFlowDoc`, part 2's `Markdown`, `SessionHead`, `NoSession` and `elapsed`, part 1's `definePanel`, `useSession` and `hrefFor`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens flow-story,flow-doc -out "$d"'`
- **Review:** deep
- **Form:** brief (ports of `flow.mjs`'s story view and `flowdoc.mjs` over Story 3's reads)

The new app's `flowStory` and `flowDoc` panels, each a head in the entry (`SessionHead`) and a lazy Body, ported from `flow.mjs`'s `FlowStory` and `flowdoc.mjs`'s `FlowDoc`. The story view draws one row of the board Story 3's `useFlow` holds: the way back to the board with its plan, its name, its facts, its steps (outcome dot, report link, elapsed time and cost, the unreadable and waiting lines, the gate lines linking to their jobs), its findings, the final row's kept lines and its reads (the plan's lines, each spec section once, the changes). The document view draws one document over `useFlowDoc`: the way back, its name and path, the sections index, `Markdown`, the cut note "The document was cut short." and the jump to `at` (a `§N` section or a source line), which lands the heading just below the pinned header. Both draw "This session is gone." and "No flow in this session's worktree." where classic hangs on "Loading…" (spec Part 3 settlement 3), and each draws an unknown step status in neutral styling (spec §29). The story adds both chunks to `checkLazyViews`. The story is brief: every task names the behaviour, the contract names and the tests, and the developer writes the code. None of it holds a landmine marker.

Facts the tasks are written around:

- Story 3's `useFlow({ session, plan, key })` answers `{ flow, error, loading }` (`flow` the held board or `undefined`, `error` `""` or the failed read's text with the held board kept, an empty `plan` reading nothing) and `useFlowDoc({ session, plan, doc })` answers `{ doc, error, loading }` (an empty `plan` or `doc` reads nothing). A plan that is not empty with no board (or document) and no `error` is loading. `flowKey(session)` is the key `useFlow` wants.
- Story 6's `panels/flow/parts.tsx` holds `planOf(session: Session, plan: string): string` (the plan when not empty, else the session's first flow's plan, else `""`), `usd(value: number | null | undefined): string` and `useClock(ms: number): number` (the time now, re-rendered every `ms`). Its `panels/flow/index.tsx` registers `flowPanel` with a `const FlowBody = lazy(() => import("./FlowBody"))` and must not import `./parts`; this story adds its two panels to that file beside it, the way `jobPanel`'s file holds its lazy constant. Part 1's `tabOf` already lights the flow tab for both routes, and part 1's `routeOf` already hands a panel the story id and the document name decoded (`params.story`, `params.doc`) and `search.plan`, `search.at` as strings (`""` when absent).
- `elapsed(from: number, to: number)` (`@/session/jobs`) words milliseconds as `45s`, `2m 10s`, `1h 4m` and answers `""` for a `from` that did not parse. `hrefFor` (`@/app/routes`) writes every link: `hrefFor("flow", { id }, { plan })`, `hrefFor("flowStory", { id, story }, { plan })`, `hrefFor("flowDoc", { id, doc }, { plan, at })` (an empty search value is left out), `hrefFor("job", { id, job })` and `hrefFor("changes", { id })`.
- Every colour is a token class written as a whole literal in a map (`text-working`, `text-error`, `text-turn`, `text-faint`, `text-muted-foreground`, `bg-working`, `bg-turn`, `bg-error`, `bg-faint`, `bg-muted-foreground`), never built from a string. Form text is 16px and a link or button is `min-h-11`. The boxes are the board's (`rounded-xl border border-border bg-card p-3`, legend `text-[11px] tracking-wide text-muted-foreground uppercase`). Claude's and the flow's text is drawn as text nodes and `Markdown`'s elements, never as markup.
- Both tests files mock `@/api/reads` as `panels/job/job.test.tsx` mocks `useJobLog` (`vi.mock("@/api/reads", async (original) => ({ ...(await original<typeof import("@/api/reads")>()), useFlow: vi.fn() }))`, so `flowKey` stays real; the document file mocks `useFlowDoc` the same way), seed session 6 of `fixtureSnapshot()` (two flows after Story 1's fixtures) with `seed([session])` from `@/commands/testkit` (the session is `allSessions(fixtureSnapshot()).find((s) => s.id === 6)`, `allSessions` from `@/fleet/groups`), and set the route's hash with `window.history.replaceState(null, "", "#/s/6/flow/1")`, as the other panel tests do.

### Task 7.1: `FlowStoryBody`: the states, the head line and the facts

**Files:**
- Create: `internal/web/ui/src/panels/flow/FlowStoryBody.tsx`
- Create: `internal/web/ui/src/panels/flow/story.test.tsx`
- Test: `internal/web/ui/src/panels/flow/story.test.tsx`

Names this task defines (private to the story): the module's `default export function FlowStoryBody(props: PanelProps): ReactElement` (`React.lazy` needs a default export). It uses Story 3's `useFlow` and `flowKey` (`@/api/reads`), Story 6's `planOf`, `usd` and `useClock` (`./parts`), `useSession` (`@/store/fleet`), `NoSession` (`@/session/NoSession`, prop-less: draw `<NoSession />`), `elapsed` (`@/session/jobs`) and `hrefFor` (`@/app/routes`).

Behaviour. `FlowStoryBody` calls its hooks first and unconditionally: `useSession(props.sessionId)`, `const now = useClock(30_000)` and `useFlow({ session: props.sessionId ?? 0, plan, key })`, with `plan = planOf(session, props.search.plan ?? "")` and `key = flowKey(session)` when the session is listed, else `plan` `""` and `key` `""` (an empty plan reads nothing). The row's id is `props.params.story`. Then, in this order:

- no session in the snapshot: `<NoSession />`;
- `plan` empty: a muted, centred `<p role="status">` "No flow in this session's worktree.";
- no `flow` and no `error`: a muted, centred `<p role="status">` "Loading…";
- no `flow` and an `error`: a `<p role="alert">` with the error text in `text-error`;
- the row is `flow.premise` for `P`, `flow.final` for `F`, else the entry of `flow.stories` (null as none) with that `id`; with no such row (a null premise or final too), a muted, centred `<p role="status">` `The board has no row <id>.`;
- else the row's view, a `section` `flex flex-col gap-4 px-4 pb-6`: the head line, the error line, the facts, then (Task 7.2) the steps, the findings and the reads boxes.

The pieces of this task:

- Head line: a `<p>` holding a link `‹ board` (`min-h-11`, muted) to `hrefFor("flow", { id: String(session.id) }, { plan })` and, after it, the row's name in `font-semibold`: `Premises` for `P`, `Final round` for `F`, else `Story <id>`.
- Error line: when `error` is not empty and the board is held, a small `text-error` `<p role="alert">` with the text, under the head line (the held board stays drawn, as on the board).
- Facts: a muted `<p data-slot="facts">` of these parts joined with ` · `, each left out when empty: the row's `developer`; for a row that is neither `P` nor `F`, its `tier` and `review` joined with ` · ` (an empty one left out); `<commits> commits` when `commits` is not 0; the elapsed time from the first step that has `started` to the last step's `ended` that has one, or to `now` when a step is running (`running` or `starting`) or no step has ended, as `elapsed(Date.parse(from), …)`; and `usd(flow.costs.by_story[row.id])` when that is a number (a null `by_story` is none). With no part the line is not drawn.

- [ ] **Step 1: Write the failing tests** in the new `story.test.tsx`: the imports (`act`, `render`, `screen` from `@testing-library/react`; `afterEach`, `beforeEach`, `describe`, `expect`, `it`, `vi` from `vitest`; `Flow`, `Session` from `@/api/gen`; `flowKey`, `useFlow` from `@/api/reads`; `hrefFor` from `@/app/routes`; `fixtureFlow`, `fixtureSnapshot` from `@/fixtures`; `allSessions` from `@/fleet/groups`; `resetFleet`, `seed` from `@/commands/testkit`; `FlowStoryBody` default from `./FlowStoryBody`), the `useFlow` mock above, `NOW` as `Date.parse("2026-11-19T12:00:00Z")` with `vi.useFakeTimers({ toFake: ["Date"] })` and `vi.setSystemTime(NOW)` in a `beforeEach` (and `vi.mocked(useFlow).mockReturnValue({ flow: undefined, error: "", loading: false })`), an `afterEach` (`vi.useRealTimers()`, `vi.restoreAllMocks()`, `resetFleet()`, the hash reset to `window.location.pathname`) and a helper `show(opts: { session?: Session | null; flow?: Flow; error?: string; story?: string; search?: Record<string, string> })` that seeds the session (default: session 6 of `fixtureSnapshot()`; `null` seeds none), sets the `useFlow` mock's answer from `flow` (default `fixtureFlow()`, called after the system time is set so its times are relative to `NOW`) and `error`, sets the hash `#/s/6/flow/<story>` and renders `<FlowStoryBody sessionId={6} params={{ id: "6", story }} search={opts.search ?? {}} />` (`story` default `"1"`). `plan` below is session 6's first flow's `plan`. A `describe("FlowStoryBody", …)` holding:

  - `a session the snapshot does not list says it is gone and reads nothing`: `show({ session: null })` draws "This session is gone."; `useFlow` was called with `plan: ""`.
    Fails without: `a session the snapshot does not list says it is gone and reads nothing`: the session check removed (the page throws on the missing session or draws a row), or a plan taken from the route's search for a session that is not listed (the call's plan is not `""`).
  - `a session with no flow and no plan says so and reads nothing`: session 6 with `flows: []` and no `plan` search draws "No flow in this session's worktree." and `useFlow` was called with `plan: ""`; with `flows: null` the same.
    Fails without: `a session with no flow and no plan says so and reads nothing`: the empty-plan branch removed (it draws "Loading…" forever, as classic does), or `planOf` bypassed (an `undefined` plan).
  - `reads the route's plan, else the newest flow's, keyed by the session's flows`: with no `plan` search the call is `{ session: 6, plan: <session.flows[0].plan>, key: flowKey(session) }`; with `search: { plan: <flows[1].plan> }` its plan is that one and its key is unchanged.
    Fails without: `reads the route's plan, else the newest flow's, keyed by the session's flows`: the route's plan ignored, `key` left out or constant, or the session id not passed.
  - `draws Loading… until the board arrives, and the read's error when none is held`: `flow: undefined` with `error: ""` draws "Loading…" (`role="status"`); `flow: undefined` with `error: "loom answered 500"` draws that text in `role="alert"` and no "Loading…".
    Fails without: `draws Loading… until the board arrives, and the read's error when none is held`: the error branch removed (it says "Loading…" over a failure), or "Loading…" drawn beside the error.
  - `says the board has no row for an id it lacks`: `show({ story: "9" })` draws `The board has no row 9.` and no `steps` legend; `story: "F"` with `final: null` draws `The board has no row F.`; `story: "P"` with `premise: null` the same.
    Fails without: `says the board has no row for an id it lacks`: the missing-row branch removed (the page throws reading a missing row's steps), or `P` and `F` looked up among the stories.
  - `the head line: a way back to the board with its plan, and the row's name`: story `1` draws a link `‹ board` whose `href` is `hrefFor("flow", { id: "6" }, { plan })` and the text `Story 1`; story `P` says `Premises`; story `F` says `Final round`; with `search: { plan: <flows[1].plan> }` the link's plan is that one.
    Fails without: `the head line: a way back to the board with its plan, and the row's name`: the plan left out of the link, the premise or final row named `Story P`, or the route's plan not used.
  - `keeps the held board drawn under a failed read's error`: `error: "loom answered 500"` with the board held draws that text (`role="alert"`) and the facts line as well.
    Fails without: `keeps the held board drawn under a failed read's error`: the error replacing the row, or never drawn once a board is held.
  - `the facts: developer, tier and review, commits, how long it took and what it cost`: story 1 draws `intermediate-dev · deep · full · 7 commits · 37m 0s · $1.41` as one line (`[data-slot="facts"]`); story 2 `intermediate-dev · deep · full · 4 commits · 21m 0s · $1.10`; story 3 (no commits) `intermediate-dev · deep · full · 8m 0s · $0.62`; story 5 (no step, no spend) `intermediate-dev · deep · full`; `P` `2m 0s · $0.12` (no developer, tier or review); with story 5's `review` set to `""` the line is `intermediate-dev · deep`.
    Fails without: `the facts: developer, tier and review, commits, how long it took and what it cost`: the tier and review shown for `P` or `F`, an empty part drawn as a stray ` · `, the elapsed time taken to `now` for a row that has ended, or the spend read from `by_agent`.
  - `a running row's time runs to now and moves with the clock`: story 4 (its dev step running since 2 minutes before `NOW`) draws `junior-dev · light · vouched · 2m 0s`; story `F` (a finished gate run, then a review running since 3 minutes before) draws `20m 0s`; with `vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] })` and `vi.setSystemTime(NOW)` before `show`, `act(() => vi.advanceTimersByTime(30_000))` moves story 4's line to `2m 30s`.
    Fails without: `a running row's time runs to now and moves with the clock`: the running test removed (`F` stops at its last ended step and reads `4m 0s`), or `useClock` not used (story 4's line stays `2m 0s`).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/story.test.tsx
```

Expected: the file fails to load with `Failed to resolve import "./FlowStoryBody" from "src/panels/flow/story.test.tsx"`.

- [ ] **Step 3: Implement** `FlowStoryBody.tsx` as described (the steps, findings and reads boxes arrive in the next task; the section holds what this task draws).

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/story.test.tsx
```

Expected: every test in the file passes.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/flow/FlowStoryBody.tsx internal/web/ui/src/panels/flow/story.test.tsx
git commit -m "feat(web): the flow story view's states, head line and facts" -- internal/web/ui/src/panels/flow/FlowStoryBody.tsx internal/web/ui/src/panels/flow/story.test.tsx
```

### Task 7.2: The story view's steps, findings and reads

**Files:**
- Modify: `internal/web/ui/src/panels/flow/FlowStoryBody.tsx`
- Modify: `internal/web/ui/src/panels/flow/story.test.tsx`
- Test: `internal/web/ui/src/panels/flow/story.test.tsx`

Names (private to the story), in `FlowStoryBody.tsx`: the components `Steps`, `Findings` and `Reads`, the function `stepOutcome(step: FlowStep): "running" | "passed" | "failed" | "todo" | "unknown"`, the function `specAts(reading: string): string[]` and the maps of whole class strings for the outcome dots and the verdicts. It uses `hrefFor` (`@/app/routes`), `usd` from `./parts` and `elapsed`.

Behaviour, placed in the row's `section` after the facts, in this order.

- Steps box: the legend `steps`; with no steps a faint `no step yet`; else an `<ol>` with one `<li data-slot="step" data-outcome="<outcome>">` per step in order, each holding:
  - a dot (`aria-hidden`, `size-2 flex-none rounded-full`) by outcome: `passed` `bg-working`, `running` `bg-turn`, `failed` `bg-error`, `todo` `bg-faint`, `unknown` `bg-muted-foreground`;
  - the step's head, one line: the step word (`<kind><k>`, so `dev1`, `gates2`), then muted ` <role or "gates"> · <status or "unreadable">` (the status text raw, whatever it is), and on the right, muted, `<elapsed> <cost>` with either left out: the elapsed time from `started` to `ended`, or to `now` when `ended` is empty (nothing when `started` is empty), and `usd(flow.costs.by_agent[step.name])` when that is a number (a null `by_agent` is none). The head is a link to `hrefFor("flowDoc", { id, doc: "report:" + step.name }, { plan })` when `step.report` is true, else a plain element (`min-h-11` either way);
  - when `step.unreadable` is not empty, a small `text-error` line with it; when `step.waiting` is not empty, a small muted line with it;
  - one gate line per `step.gates` entry (null as none), in order: the verdict (`PASS` in `text-working`, any other non-empty verdict in `text-error`, an empty one drawn `…` in `text-muted-foreground`) and the gate's `text`; the line is a link to `hrefFor("job", { id, job: String(gate.job) })` when `gate.job` is not 0, else a plain element, `data-slot="gate"` on both.
- `stepOutcome`: `running` and `starting` are `running`; `DONE`, `DONE_WITH_CONCERNS` and `passed` are `passed`; an empty status is `todo`; `failed`, `BLOCKED`, `NEEDS_CONTEXT`, `cancelled` and `infra` are `failed`; any other status is `unknown` (its raw text is on the line and its dot is neutral, spec §29).
- Findings box: not for `P`. The legend `findings`; the line `Critical <n> · Important <n> · Minor <n>` from the row's three `findings` numbers (muted when all are 0, foreground otherwise), or a muted `none yet` when `findings` has fewer than 3 entries. For `F`, each line of `flow.kept` (null as none) follows, one small faint `<p>` each.
- Reads box: not for `P` or `F`. The legend `reads`, then, as links (`min-h-11`, `text-turn`): `plan · Story <id>` followed by ` (L<line>–<end>)` when `line` is not 0, to `hrefFor("flowDoc", { id, doc: "plan" }, { plan, at: line === 0 ? "" : String(line) })`; one `spec §<n>` link for each distinct section `specAts(row.reading)` finds, to `hrefFor("flowDoc", { id, doc: "spec" }, { plan, at: "§<n>" })`; and, when `commits` is not 0, `<commits> commits → changes` to `hrefFor("changes", { id })`.
- `specAts`: the reading's matches of `§\s*[\d.]+`, each with its whitespace removed (`§ 4` is `§4`), in order of first appearance, each at most once. A reading that names no section gives no link.

- [ ] **Step 1: Write the failing tests** in `story.test.tsx`, inside `describe("FlowStoryBody", …)` or a sibling `describe` using the same `show`, with story 1's row unless the test says another (`ids` are the board's `id`s):

  - `the steps: each in order with its word, role, status and elapsed time, and the cost where the agent is priced`: with `costs.by_agent` set to `{ "2026-11-09-flow-visualisation-s1-dev1": 0.4 }`, story 1's five `[data-slot="step"]` items read in order `dev1`, `gates1`, `fix1`, `gates2`, `review1`; the first's line holds `intermediate-dev · DONE` and `14m 0s $0.40`; the second's holds `gates · failed` (the role of a gate run is empty, so it says `gates`) and `3m 0s` and no `$`; a step with an empty `started` shows no time.
    Fails without: `the steps: each in order with its word, role, status and elapsed time, and the cost where the agent is priced`: the `gates` fallback for an empty role removed (an empty word), the cost read from `by_story`, or the elapsed time left out.
  - `the steps: the outcome of each decides its dot, and an unknown status is neutral and raw`: story 1's items have `data-outcome` `passed`, `failed`, `passed`, `passed`, `passed`; story 3's `BLOCKED` step is `failed`; story 4's running step is `running` and its time is `2m 0s` (to `NOW`); a step set to `starting` is `running`; set to `""` it is `todo` and its line says `unreadable`; set to `"weird"` (cast) it is `unknown`, its line says `weird` and its dot has `bg-muted-foreground` and neither `bg-error` nor `bg-working`.
    Fails without: `the steps: the outcome of each decides its dot, and an unknown status is neutral and raw`: any status outside the list falling to `failed` (the `weird` case turns red), `starting` missing, or the status text replaced by its outcome.
  - `a step with a report links to it, and one without is not a link`: story 1's `dev1` head is a link to `hrefFor("flowDoc", { id: "6", doc: "report:2026-11-09-flow-visualisation-s1-dev1" }, { plan })`; `gates1` (`report: false`) is not a link.
    Fails without: `a step with a report links to it, and one without is not a link`: every step a link, the report name left out of `doc`, or the plan left out.
  - `the unreadable and waiting lines`: story 4's step draws its `waiting` text (`waiting for an agent slot`) in a muted line; with that step's `unreadable` set to `bad report` that text draws in `text-error`; story 1's steps draw neither.
    Fails without: `the unreadable and waiting lines`: either line removed, or both drawn for steps that have none.
  - `the gate lines: the verdict, the text and a link to the job`: story 1's second step has three `[data-slot="gate"]` items: `PASS gofmt check` and `PASS go vet ./...` (verdict `text-working`) and `FAIL go test ./...` (verdict `text-error`), the last a link whose `href` is `hrefFor("job", { id: "6", job: "34" })`; a gate with `verdict: ""` and `job: 0` draws `… <text>` in `text-muted-foreground` and is not a link.
    Fails without: `the gate lines: the verdict, the text and a link to the job`: the verdict coloured alike for every value, a link drawn for job 0 (`#/s/6/job/0`), or the empty verdict drawn as nothing.
  - `a row with no step says so`: story 5 draws `no step yet` under the `steps` legend and no `[data-slot="step"]`.
    Fails without: `a row with no step says so`: the empty branch removed (an empty box).
  - `the findings box: the counts, the kept lines of the final row, none for the premise row`: story 2 draws `Critical 0 · Important 0 · Minor 1` (not muted); story 1 draws `Critical 0 · Important 0 · Minor 0` (muted); with `findings: []` the line says `none yet`; story `F` draws its counts and the board's `kept` line `Minor internal/flowview/detail.go:88 name the cache key` below them; story 1 does not draw that line; story `P` has no `findings` legend.
    Fails without: `the findings box: the counts, the kept lines of the final row, none for the premise row`: the kept lines drawn for every row, the `P` guard removed, or a short `findings` array not guarded (`undefined` in the text).
  - `the reads box: the plan's lines, each spec section once and the changes`: story 1 draws the links `plan · Story 1 (L212–604)` to `hrefFor("flowDoc", { id: "6", doc: "plan" }, { plan, at: "212" })`, `spec §1` and `spec §2` to `hrefFor("flowDoc", { id: "6", doc: "spec" }, { plan, at: "§1" })` and `…"§2"`, and `7 commits → changes` to `hrefFor("changes", { id: "6" })`; story 4 (no commits) has no changes link; a row with `line: 0` draws `plan · Story 4` with no range and no `at` in its `href`.
    Fails without: `the reads box: the plan's lines, each spec section once and the changes`: the range left out, the `at` left out of the plan link, the changes link drawn for no commits, or the spec links built from the whole reading text.
  - `the reads box: a section the reading names twice is one link, and spaces in it do not matter`: with story 1's `reading` set to `spec §1, §2, §1 and § 1` the spec links are exactly `spec §1` and `spec §2`; with `reading: "none"` there is no spec link; with `reading: "spec §3.2"` the link is `spec §3.2` with `at` `§3.2`.
    Fails without: `the reads box: a section the reading names twice is one link, and spaces in it do not matter`: the de-duplication removed (duplicate keys and three links), the whitespace not stripped (`§ 1` is a third link), or a reading with no section drawn as text.
  - `the premise and final rows draw no reads box`: stories `P` and `F` have no `reads` legend; `F` still draws its steps (`gates1`, `review1`) with the review's `data-outcome` `running`.
    Fails without: `the premise and final rows draw no reads box`: the guard removed (a `plan · Story F` link).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/story.test.tsx
```

Expected: the new tests fail (no `steps` legend), the Task 7.1 tests still pass.

- [ ] **Step 3: Implement** the three boxes in `FlowStoryBody.tsx` as described.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/story.test.tsx
```

Expected: every test in the file passes.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the flow story view's steps, findings and reads" -- internal/web/ui/src/panels/flow/FlowStoryBody.tsx internal/web/ui/src/panels/flow/story.test.tsx
```

### Task 7.3: `FlowDocBody`: the states, the head line, the sections index, the body and the cut note

**Files:**
- Create: `internal/web/ui/src/panels/flow/FlowDocBody.tsx`
- Create: `internal/web/ui/src/panels/flow/doc.test.tsx`
- Test: `internal/web/ui/src/panels/flow/doc.test.tsx`

Names this task defines (private to the story): the module's `default export function FlowDocBody(props: PanelProps): ReactElement`, and its private `docName(doc: string): string`, `headings(blocks: (MdNode | null)[]): Heading[]` (with `interface Heading { text: string; level: number; line: number }`) and the component `Sections`. It uses Story 3's `useFlowDoc` (`@/api/reads`), Story 6's `planOf` (`./parts`), `Markdown` (`@/conversation/Markdown`, props `{ blocks: (MdNode | null)[] }`), `useSession`, `NoSession` and `hrefFor`; `MdNode` is `@/api/gen`'s.

Behaviour. `FlowDocBody` calls its hooks first and unconditionally: `useSession(props.sessionId)` and `useFlowDoc({ session: props.sessionId ?? 0, plan, doc })`, with `plan = planOf(session, props.search.plan ?? "")` when the session is listed, else `""`, and `doc = props.params.doc` (the route has already decoded it). Then, in this order:

- no session in the snapshot: `<NoSession />`;
- `plan` empty: a muted, centred `<p role="status">` "No flow in this session's worktree.";
- the view, a `section` `flex flex-col gap-3 px-4 pb-6`: the head line; then, with no document and no `error`, a muted, centred `<p role="status">` "Loading…"; with an `error`, a `<p role="alert">` with it in `text-error` (above the document when one is held); then, when a document is held, the path, the sections index, the body and the cut note.

The pieces:

- Head line: as the story view's: a link `‹ board` to `hrefFor("flow", { id: String(session.id) }, { plan })` and the document's name in `font-semibold`, `docName(doc)`: `spec`, `skeleton`, `plan` and `record` as themselves, `drafts-state` as `drafts' state`, `report:<name>` as `report · <name>`, any other text as it is.
- Path: a small faint `font-mono break-all` `<p>` with `doc.path`.
- `headings(blocks)`: every `h` node of the tree in document order, each `{ text, level, line }` (`text` the concatenated `v` of its descendants' `text` and `code_inline` nodes, `level` its `level`, `line` its `line` or 0); it descends through every node with children except a `code` node, and treats a `null` entry as none.
- Sections index: only when more than 2 headings have `level <= 3`: a `<details>` box with the muted `<summary>` `sections` (`min-h-11`) and, for each heading of level 3 or less, a button (`min-h-11`, `text-left`, indented by level: `pl-0`, `pl-3`, `pl-6` for levels 1, 2, 3) with its text; a button calls the jump (Task 7.4) for that heading's index among all headings.
- Body: `<Markdown blocks={doc.blocks ?? []} />` in a `div` the jump reads (a `ref`).
- Cut note: when `doc.cut` is true, a small faint `<p>` "The document was cut short."

- [ ] **Step 1: Write the failing tests** in the new `doc.test.tsx`: the imports as `story.test.tsx`'s with `FlowDoc` for the type, `useFlowDoc` for the mock (`vi.mocked(useFlowDoc).mockReturnValue({ doc: undefined, error: "", loading: false })` in `beforeEach`), `fixtureFlowDoc` for the document (called after the system time is set), `userEvent` from `@testing-library/user-event` and `FlowDocBody` default from `./FlowDocBody`, the same `NOW`, fake `Date`, `afterEach` and seeding, and a helper `show(opts: { session?: Session | null; doc?: FlowDoc; error?: string; name?: string; search?: Record<string, string> })` that seeds session 6 (`null` seeds none), sets the mock's answer (`doc` default `fixtureFlowDoc()`, `loading` false), sets the hash `#/s/6/flow/doc/<name>` (`name` default `"spec"`) and renders `<FlowDocBody sessionId={6} params={{ id: "6", doc: name }} search={opts.search ?? {}} />`. `plan` below is session 6's first flow's `plan`. The fixture document (`fixtureFlowDoc()`) holds, among its blocks, six headings: `Loom PWA: a flow's design, spec, progress, story states and costs` (level 1, line 1), `Problem` (2, line 8), `Design` (2, line 60), `1. Flows and their stages` (3, line 62), `2. The reader (internal/flowview)` (3, line 120) and `3. Costs` (3, line 190), and its `path` is `docs/superpowers/specs/2026-11-09-flow-visualisation-design.md`. A `describe("FlowDocBody", …)` holding:

  - `a session the snapshot does not list says it is gone and reads nothing`: `show({ session: null })` draws "This session is gone." and `useFlowDoc` was called with `plan: ""`.
    Fails without: `a session the snapshot does not list says it is gone and reads nothing`: the session check removed (the page throws), or a plan taken from the route's search for an unlisted session.
  - `a session with no flow and no plan says so and reads nothing`: session 6 with `flows: []` and no `plan` search draws "No flow in this session's worktree." and `useFlowDoc` was called with `plan: ""`; with `flows: null` the same.
    Fails without: `a session with no flow and no plan says so and reads nothing`: the empty-plan branch removed (it draws "Loading…" forever, as classic does), or `planOf` bypassed.
  - `reads the route's plan, else the newest flow's, and the document the route names`: with no `plan` search the call is `{ session: 6, plan: <session.flows[0].plan>, doc: "spec" }`; with `search: { plan: <flows[1].plan> }` and `name: "report:abc-dev1"` it is that plan and `doc: "report:abc-dev1"`.
    Fails without: `reads the route's plan, else the newest flow's, and the document the route names`: the document name left out or the route's plan ignored.
  - `draws Loading… until the document arrives, and the read's error when none is held`: `doc: undefined` with `error: ""` draws "Loading…" under the head line; with `error: "loom answered 500"` that text draws in `role="alert"` and no "Loading…"; the head line draws in both.
    Fails without: `draws Loading… until the document arrives, and the read's error when none is held`: the error branch removed, "Loading…" drawn beside the error, or the head line missing while loading.
  - `the head line: a way back to the board with its plan, and the document's name`: the link `‹ board` has `href` `hrefFor("flow", { id: "6" }, { plan })`; the names read `spec`, `skeleton`, `plan`, `record`, `drafts' state` (for `drafts-state`), `report · 2026-11-09-flow-visualisation-s1-dev1` (for `report:2026-11-09-flow-visualisation-s1-dev1`) and an unknown `whatever` as `whatever`.
    Fails without: `the head line: a way back to the board with its plan, and the document's name`: the plan left out of the link, the `report:` prefix not turned into `report · `, or `drafts-state` drawn raw.
  - `draws the path, and the document's text through Markdown`: the fixture's `path` is on screen in the mono line, and the text of the first heading is on screen as a heading (`role="heading"`).
    Fails without: `draws the path, and the document's text through Markdown`: the path not drawn, or `Markdown` not given the document's blocks (no heading).
  - `the sections index: a details box of the headings of level 3 or less, only when there are more than 2`: the fixture draws a `sections` summary and, after opening it (`userEvent.click` on the summary), buttons for the headings `Loom PWA: …`, `Problem`, `Design`, `1. Flows and their stages`, `2. The reader (internal/flowview)` and `3. Costs`; a document whose blocks hold two headings draws no `sections`; one holding three headings, one of them level 4, draws no `sections` (only 2 of level 3 or less); a heading nested inside a `quote` node counts and one inside a `code` node does not.
    Fails without: `the sections index: a details box of the headings of level 3 or less, only when there are more than 2`: the `> 2` test as `>= 2` or counting level 4, the level filter removed, nested headings not walked, or headings read from code.
  - `the cut note: drawn only for a cut document`: with `cut: true` "The document was cut short." draws; with the fixture's `cut: false` it does not, and the old words "cut at 1 MiB" never draw.
    Fails without: `the cut note: drawn only for a cut document`: the note unconditional, or classic's wording.
  - `a held document draws under a failed read's error`: `error: "loom answered 500"` with the document held draws the error (`role="alert"`) and the path.
    Fails without: `a held document draws under a failed read's error`: the error replacing the document.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/doc.test.tsx
```

Expected: the file fails to load with `Failed to resolve import "./FlowDocBody" from "src/panels/flow/doc.test.tsx"`.

- [ ] **Step 3: Implement** `FlowDocBody.tsx` as described (the jump arrives in the next task: the buttons call a `jump` that this task leaves as a no-op, named in a comment that Task 7.4 replaces).

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/doc.test.tsx
```

Expected: every test in the file passes.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/flow/FlowDocBody.tsx internal/web/ui/src/panels/flow/doc.test.tsx
git commit -m "feat(web): the flow document view's states, head line, sections index and body" -- internal/web/ui/src/panels/flow/FlowDocBody.tsx internal/web/ui/src/panels/flow/doc.test.tsx
```

### Task 7.4: The document's jump to `at`, below the pinned header

**Files:**
- Modify: `internal/web/ui/src/panels/flow/FlowDocBody.tsx`
- Modify: `internal/web/ui/src/panels/flow/doc.test.tsx`
- Test: `internal/web/ui/src/panels/flow/doc.test.tsx`

Names (private to the story), in `FlowDocBody.tsx`: `function targetOf(headings: Heading[], at: string): number` (the index of the heading `at` names among all the headings, `-1` for none) and the `jump(index: number)` the view uses for both `at` and the sections index.

Behaviour.

- `targetOf`: for an `at` that is empty, `-1`. For `§<n>` (`^§\s*([\d.]+)`, a trailing `.` of the number dropped): the first heading whose trimmed text equals `<n>`, or starts with `<n>.`, `<n> ` or `§<n>`; `-1` when none. For anything else, a source line: `Number(at)` finite and above 0, the index of the last heading whose `line` is not 0 and is at or below it, `-1` when there is none (and for a number that is not a line).
- `jump(index)`: the `index`th element of the body `div`'s `querySelectorAll("h3, h4")` (`Markdown` draws every heading as an `h3` or an `h4`, in document order, so the index of a heading among all headings is the element's); with none, nothing. Otherwise it calls `window.scrollTo({ top })` with `top = el.getBoundingClientRect().top + window.scrollY - headerHeight`, `headerHeight` being the `getBoundingClientRect().height` of `document.querySelector('[data-slot="header"]')`, or 0 when there is none: the heading lands just below the pinned header, not under it. A `top` under 0 is 0.
- When the document is held, an effect runs `jump(targetOf(headings, at))` for a target of 0 or more, keyed on the document and `at` (so a re-render with the same document and `at` does not scroll again, and a new `at` does). An `at` that matches no heading leaves the page where it is: no call to `scrollTo`.
- The sections index's buttons call `jump` with their own heading's index, ignoring `at`.

- [ ] **Step 1: Write the failing tests** in `doc.test.tsx`, in a `describe("the jump", …)` with `vi.spyOn(window, "scrollTo").mockImplementation(() => {})`, `Object.defineProperty(window, "scrollY", { value: 100, configurable: true })` (restored in the `afterEach`), and `vi.spyOn(HTMLElement.prototype, "getBoundingClientRect")` answering by element: for `[data-slot="header"]` a rect of height 120 (the test appends `<header data-slot="header">` to `document.body` and removes it after), for a heading element a `top` of `1000 + 100 * <its index among the body's headings>`, for anything else a zero rect. Using `fixtureFlowDoc()`:

  - `at §2 lands the second numbered section just below the header`: `show({ search: { at: "§2" } })` calls `window.scrollTo` once with `{ top: 1000 + 100 * 4 + 100 - 120 }` (the heading `2. The reader (internal/flowview)` is the fifth heading, index 4).
    Fails without: `at §2 lands the second numbered section just below the header`: the header height not subtracted (`top` is 120 higher), `scrollY` not added, or the wrong heading found (`§2` matching `Problem`, or the first heading that contains a 2).
  - `a section number is not a prefix of another`: `at: "§1"` lands on `1. Flows and their stages` (index 3), not on a heading whose text merely begins with a `1`; with a document whose headings are `10. Ten` and `1. One`, `§1` lands on `1. One`.
    Fails without: `a section number is not a prefix of another`: the match as `startsWith(n)` alone (`10. Ten` found first).
  - `at a source line lands the last heading at or above it`: `at: "130"` lands on `2. The reader (internal/flowview)` (line 120); `at: "62"` on `1. Flows and their stages` (line 62); `at: "5"` on the first heading (line 1); `at: "190"` on `3. Costs`.
    Fails without: `at a source line lands the last heading at or above it`: the first heading at or above taken (the `130` case lands on `Problem`), or the comparison strict (`62` lands on `Design`).
  - `an at that matches no heading leaves the page where it is`: `at: "§9"`, `at: "abc"`, `at: "0"` and `at: ""` each call `scrollTo` zero times; so does a document with no headings and `at: "§1"`.
    Fails without: `an at that matches no heading leaves the page where it is`: `-1` used as an index (the last heading found, or a throw), or an empty `at` jumping to the first heading.
  - `the jump runs once per document and at, and again for a new at`: after the first render `scrollTo` has one call; a re-render with the same props adds none; rendering with `search: { at: "§3" }` adds one more, to `3. Costs`.
    Fails without: `the jump runs once per document and at, and again for a new at`: the effect with no dependency list (it scrolls on every render), or keyed on the document only (no second call).
  - `a heading never lands above the top of the page`: with the heading's rect `top` stubbed to 10 and `scrollY` 0, `at: "§2"` calls `scrollTo` with `{ top: 0 }`.
    Fails without: `a heading never lands above the top of the page`: the clamp removed (`top` is -110).
  - `the sections index's buttons jump to their own heading`: with no `at`, opening the index and clicking `3. Costs` calls `scrollTo` once with the top of the sixth heading less the header; clicking `Problem` lands on the second heading; a click while `at` names another section still goes to the clicked one.
    Fails without: `the sections index's buttons jump to their own heading`: the index passed as the position among the buttons (a level-4 heading would shift it) rather than among all the headings, or the button calling the `at` target.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/doc.test.tsx
```

Expected: the jump tests fail (`scrollTo` is never called), the Task 7.3 tests still pass.

- [ ] **Step 3: Implement** `targetOf`, the real `jump` and the effect in `FlowDocBody.tsx`, replacing Task 7.3's no-op.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/doc.test.tsx
```

Expected: every test in the file passes.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the flow document's jump to a section or a line, below the pinned header" -- internal/web/ui/src/panels/flow/FlowDocBody.tsx internal/web/ui/src/panels/flow/doc.test.tsx
```

### Task 7.5: The `flowStory` and `flowDoc` panels

**Files:**
- Modify: `internal/web/ui/src/panels/flow/index.tsx`
- Modify: `internal/web/ui/src/panels/flow/story.test.tsx`
- Modify: `internal/web/ui/src/panels/flow/doc.test.tsx`
- Test: `internal/web/ui/src/panels/flow/story.test.tsx`, `internal/web/ui/src/panels/flow/doc.test.tsx`

Names this task defines (contract, Story 7), in `panels/flow/index.tsx`: `export const flowStoryPanel` and `export const flowDocPanel`. Its other names are private: `const FlowStoryBody = lazy(() => import("./FlowStoryBody"))` and `const FlowDocBody = lazy(() => import("./FlowDocBody"))`, each beside Story 6's `FlowBody` constant.

- `flowStoryPanel = definePanel({ id: "flowStory", title: () => "Flow row", Head: SessionHead, Body: FlowStoryBody })` and `flowDocPanel = definePanel({ id: "flowDoc", title: () => "Flow document", Head: SessionHead, Body: FlowDocBody })`: no `tab` (the flow tab lights for both through `tabOf`, and each is no tab of its own) and no `appliesTo`. `SessionHead` is `@/session/SessionHead`, already imported by Story 6's `flowPanel`.
- The module imports nothing from `./parts`, and no `FlowStoryBody` or `FlowDocBody` statically: both bodies load lazily (a static import would pull them out of their chunks into the entry script). It adds to the file only these two constants and two panel definitions, after `flowPanel`'s; `flowPanel`, `FlowMark` and `FlowBody` stay as Story 6 left them. `panels/index.ts` already imports `./flow`, so nothing is added there.

- [ ] **Step 1: Write the failing tests**, in `story.test.tsx` (imports: `flowStoryPanel` from `./index`; `panelFor` from `@/app/panels`; `PanelHost` from `@/app/shell`; `SessionHead` from `@/session/SessionHead`) a `describe("the flowStory panel", …)`, and in `doc.test.tsx` the same for `flowDocPanel`, each holding:

  - `the panel is registered under its id, with the session's head and no tab`: `panelFor("flowStory")` is `flowStoryPanel` (`"flowDoc"` and `flowDocPanel` in the other file); its `Head` is `SessionHead`; its `tab` and `appliesTo` are `undefined`.
    Fails without: `the panel is registered under its id, with the session's head and no tab`: `definePanel` missing (`panelFor` answers `undefined`), a `tab` added (a second flow tab beside the board's), or the head not `SessionHead`.
  - `the panel draws its view through PanelHost once its chunk loads`: with the `useFlow` mock answering the fixture board (`useFlowDoc` the fixture document) and session 6 seeded, `render(<PanelHost panel="flowStory" props={{ sessionId: 6, params: { id: "6", story: "1" }, search: {} }} />)` shows, after `await screen.findByText("Story 1")`, the legend `steps` (`doc`: `panel="flowDoc"` with `params: { id: "6", doc: "spec" }` shows, after `await screen.findByText("spec")`, the document's path).
    Fails without: `the panel draws its view through PanelHost once its chunk loads`: the body not the default export of its module (the lazy import fails into the error boundary), or the panel's `Body` not lazy-wrapped from `./FlowStoryBody` (`./FlowDocBody`).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow/story.test.tsx src/panels/flow/doc.test.tsx
```

Expected: the new tests fail with `flowStoryPanel` and `flowDocPanel` not exported from `./index` (`panelFor("flowStory")` is `undefined`).

- [ ] **Step 3: Implement** the two panels in `index.tsx` as described.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flow src/app/shell.test.tsx src/app/panels.test.ts
```

Expected: every file passes (the shell's and the registry's tests define their own panels and read no `flowStory` or `flowDoc` panel).

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the flow story and document panels" -- internal/web/ui/src/panels/flow/index.tsx internal/web/ui/src/panels/flow/story.test.tsx internal/web/ui/src/panels/flow/doc.test.tsx
```

### Task 7.6: Both views are chunks of their own: `checkLazyViews`, the bundle and the screenshots

**Files:**
- Modify: `internal/web/static_budget_test.go` (`checkLazyViews` and its table test)
- Modify: `internal/web/dist/` (rebuilt)
- Test: `internal/web/static_budget_test.go`

Behaviour. The story adds two stems to what the tree holds, and quotes none of its text: Story 6 added `"FlowBody"` and its table cases, and Stories 8 to 10 add theirs, so add each of these at the end of the list as it is when this task runs.

- `checkLazyViews`' name list (the `[]string{…}` literal in its `for` loop) gains `"FlowStoryBody"` and then `"FlowDocBody"` at its end, and the function's doc comment names the flow story and document views beside the screens it already names.
- In `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`: the `all` slice gains `"/assets/FlowStoryBody-e.js"` and `"/assets/FlowDocBody-f.js"` at its end (letters no other entry of the slice uses; take the next free ones if `e` or `f` is in use); the table gains four cases after the last case Story 6 added: `no flow story chunk` (`build(entry, <all without the FlowStoryBody file>...)`, the wanted text `FlowStoryBody`), `no flow document chunk` (the same without the `FlowDocBody` file, the wanted text `FlowDocBody`), `a preloaded flow story chunk` (`build(entry+`<link rel="modulepreload" crossorigin href="/assets/FlowStoryBody-e.js">`, all...)`, the wanted text `/assets/FlowStoryBody-e.js`) and `a preloaded flow document chunk` (the same for `FlowDocBody-f.js`); the existing cases stay as they are.

The test reads the committed bundle, so the bundle is rebuilt in this task: `pnpm run build` writes `internal/web/dist/`, the Vite build puts each body in `assets/FlowStoryBody-<hash>.js` and `assets/FlowDocBody-<hash>.js` (imported lazily, so `index.html` does not name them).

- [ ] **Step 1: Write the failing tests**: edit the table test as above first. The four new table cases pass at once (they test the function's list with fabricated assets), so the failing one is the bundle's: with the two stems in the list, `TestDist_LazyViewsAreChunksOfTheirOwn` reads the committed bundle, which has no such chunks yet.
  - `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/no_flow_story_chunk` and `…/no_flow_document_chunk`: the assets lack the chunk and the error names it.
    Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/no_flow_story_chunk`: `"FlowStoryBody"` removed from the list (no error, `want` not found); the document case the same for `"FlowDocBody"`.
  - `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/a_preloaded_flow_story_chunk` and `…/a_preloaded_flow_document_chunk`: `index.html` preloads the chunk and the error names its file.
    Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/a_preloaded_flow_story_chunk`: the stem removed from the list, or the page check dropped; the document case the same.
  - `TestDist_LazyViewsAreChunksOfTheirOwn`: the committed bundle holds exactly one `FlowStoryBody-<hash>.js` and one `FlowDocBody-<hash>.js` that `index.html` does not name.
    Fails without: `TestDist_LazyViewsAreChunksOfTheirOwn`: a panel importing its body statically (it lands in the entry script and no chunk of that name exists), or the bundle not rebuilt after this story's source changes.

- [ ] **Step 2: Run it to see it fail**

```bash
CGO_ENABLED=0 go test ./internal/web/ -run 'TestDist_LazyViewsAreChunksOfTheirOwn|TestCheckLazyViews'
```

Expected: `TestDist_LazyViewsAreChunksOfTheirOwn` fails with `the bundle has no lazy chunk for FlowStoryBody: its panel is imported statically, or the build renamed it`; the table's cases pass.

- [ ] **Step 3: Implement** the list, comment and table edits, then rebuild the bundle and check the chunks, the entry page and the first-load budget:

```bash
pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build
ls internal/web/dist/assets/FlowStoryBody-*.js internal/web/dist/assets/FlowDocBody-*.js
bash -c '! grep -q -e FlowStoryBody -e FlowDocBody internal/web/dist/index.html'
```

Expected: the build succeeds, `ls` prints one path for each chunk and the `grep` check exits 0 (the page names neither).

- [ ] **Step 4: Run it to see it pass**, with the story's full gates and the screenshots of the story and document screens under the real Content-Security-Policy (each exits 1 on a console error or an unanswered route):

```bash
bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens flow-story,flow-doc -out "$d" && echo "$d/flow-story.png $d/flow-doc.png"'
```

Expected: every check passes (`dist:check` included), `ok github.com/aidan-bailey/loom/internal/web` (`TestDist_FirstLoadIsWithinBudget` among them) and the last line prints the paths of both PNGs. Read them: `flow-story.png` shows story 1's head line `‹ board` and `Story 1`, its facts, its five steps (the failed gate run with its three gate lines, the red `FAIL go test ./...`), the findings box and the reads box with its `plan`, `spec §1`, `spec §2` and changes links; `flow-doc.png` shows the `‹ board` head line and `spec`, the path, the sections index closed, and the document opened at its `2. The reader` heading just under the pinned header.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "build(web): the flow story and document views are chunks of their own, pinned in checkLazyViews, and the bundle rebuilt" -- internal/web/static_budget_test.go internal/web/dist
```

## Story 8: The host's flows

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/flows/index.tsx`, `internal/web/ui/src/panels/flows/FlowsBody.tsx`, `internal/web/ui/src/panels/flows/flows.test.tsx`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
- **Interfaces:** exposes the `flows` panel, `FlowsHead` and the exported `flowTone`; uses Story 3's `useFlows`, Story 6's `usd`, `median` and `Bars`, Story 1's `fixtureFlows`, part 1's `flowChip`, `ago`, `ContextRow`, `definePanel` and `hrefFor`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens flows -out "$d"'`
- **Review:** light (a port of `flows.mjs` over Story 3's read)
- **Form:** brief (a port of `flows.mjs`)

The new app's `flows` panel, a port of `flows.mjs` over Story 3's `useFlows`: a head in the entry (`FlowsHead`: "Flows", the way back to the fleet and a ↻ that reads the flows again) and a lazy `FlowsBody`. The body draws the error and loading states; the live box, each flow a link to its board with its title, its part and its chip (part 1's `flowChip`, toned by `flowTone`, which this story exports from `panels/fleet/SessionRow.tsx`), "No session's worktree holds a flow." when none; one past box for each repo in first-seen order, named as classic's `repoName` names it, with 8 runs shown and "and N more" past them, each run's stem, stories, cost, how it ended and its age (or "unreadable"), and the per-story cost bars of the repo's last 5 priced runs (none under 2); and "No run records yet." when the host has none (spec Part 3 settlements 1, 2 and 5). The bars take their heights through `Bars`' `style` prop. The story adds the chunk to `checkLazyViews`. The story is brief: every task names the behaviour, the contract names and the tests, and the developer writes the code. None of it holds a landmine marker.

Facts the tasks are written around:

- Story 3's `useFlows()` answers `{ flows, error, loading, reload }` (`flows` the held answer or `undefined`, `error` `""` or the failed read's text with the held answer kept, `loading` while nothing is held and no read has failed). Every caller shares one query: `FlowsHead` and `FlowsBody` both call it, the second mount sends no second read, and `reload` refetches for every caller. `Flows.live` and `Flows.past` are each `null` or a list (`LiveFlow`: `session`, `title`, `flow` a `FlowSummary`; `PastRun`: `key`, `file`, `stem`, `plan`, `stories`, `ended`, `end`, `usd` a number or null, `unreadable`), newest run first within each repo.
- Story 6's `panels/flow/parts.tsx` holds `usd(value: number | null | undefined): string`, `median(values: number[]): number` and `Bars(props: { values: number[], current: number | null })` (an `aria-hidden` row of `<span data-slot="bar">` elements, oldest first, each height an exact share of the tallest through `style`; it draws only the bars, a caller draws its caption and draws nothing under 2 values). Story 6's `panels/flow/index.tsx` registers `flowPanel`; this story's `panels/flows/index.tsx` must not import `./parts` (its `Head` is in the entry and its `Body` lazy), and `FlowsBody.tsx` imports it from `../flow/parts`.
- Part 1's `panels/fleet/SessionRow.tsx` holds `ago(iso, now?)` (`now`, `3m`, `5h`, `3d`, `""` for a time that does not parse), `flowChip(flow: FlowSummary): string` and a private `flowTone(flow: FlowSummary): "docs" | "attention" | "error"` (`attention` while the flow asks, `error` when it is lost or unreadable, else `docs`); the fleet row draws a chip as `<Badge variant={flowTone(flow)} data-tone={flowTone(flow)}>{flowChip(flow)}</Badge>` (`Badge` from `@/components/ui/badge`). Story 6 already exports `usd` in that file, one line apart; change nothing else there.
- `ContextRow` (`@/app/shell`) draws the header's first row: `ContextRow({ title, back, actions })`, `back` an href (the fleet's `hrefFor("fleet")`, drawn as `‹ fleet`). `JobHead` in `panels/job/index.tsx` is the pattern for a head and a lazy Body in one `panels/<name>/index.tsx`. `hrefFor("flow", { id }, { plan })` is the board's link.
- Every colour is a token class written as a whole literal (`text-error`, `text-muted-foreground`, `text-faint`), form text is 16px and a link or button is `min-h-11`. The boxes are the board's (`rounded-xl border border-border bg-card p-3`, legend `text-[11px] tracking-wide text-muted-foreground uppercase`). Text is drawn as text nodes, never as markup.
- The tests mock `@/api/reads` as `panels/job/job.test.tsx` mocks `useJobLog` (`vi.mock("@/api/reads", async (original) => ({ ...(await original<typeof import("@/api/reads")>()), useFlows: vi.fn() }))`) and answer `useFlows` with `{ flows: fixtureFlows(), error: "", loading: false, reload: vi.fn() }`. `fixtureFlows()` (Story 1, called after the system time is set so its times are relative to it) holds two live flows (session 6 `flow-run`, parked on a question, whose chip reads `flow 2/5 · asks · $4.71`; session 4 `flow-docs`, drafting, `flow · draft 3/4`) and four past runs: in the repo key `-tb-Source-Personal-loom`, newest first, `2026-11-06-pwa-conversation-first` (4 stories, `$10.08`, `stopped`, ended 72 hours ago), `2026-11-04-work-scheduler` (6, `$14.20`, `done`, 120 hours ago) and `2026-10-22-loom-pwa` (9, `$31.77`, `supervised`, 430 hours ago), and in `-tb-Source-Academia-kermit` one, `2026-10-22-per-query-index-build` (23, `$58.40`, `supervised`, 432 hours ago).

### Task 8.1: `FlowsBody`: the states and the live box, and `flowTone` exported

**Files:**
- Create: `internal/web/ui/src/panels/flows/FlowsBody.tsx`
- Modify: `internal/web/ui/src/panels/fleet/SessionRow.tsx` (the line that declares `function flowTone`, exported)
- Create: `internal/web/ui/src/panels/flows/flows.test.tsx`
- Test: `internal/web/ui/src/panels/flows/flows.test.tsx`

Names this task defines (private to the story): the module's `default export function FlowsBody(): ReactElement` (`React.lazy` needs a default export) and its private `Live` component. `flowTone` keeps its name and signature and gains `export` (put `export` before `function flowTone` on the one line that declares it). It uses Story 3's `useFlows` (`@/api/reads`), `flowChip` and `flowTone` (`../fleet/SessionRow`), `Badge` (`@/components/ui/badge`) and `hrefFor` (`@/app/routes`).

Behaviour. `FlowsBody` calls `useFlows()` and then, in this order:

- no `flows` and an `error`: a `<p role="alert">` with the error text in `text-error`, centred;
- no `flows` (no error): a muted, centred `<p role="status">` "Loading…";
- else a `section` `flex flex-col gap-4 px-4 pb-6` holding, in order: when `error` is not empty, a small `text-error` `<p role="alert">` with it (the held answer stays drawn under it); the live box; the past boxes and the empty line (Task 8.2).

The live box: a box with the legend `live`; with no live flow (`flows.live` null or empty) a faint "No session's worktree holds a flow."; else one link (`flex min-h-11 items-center gap-2`) per live flow, in order, to `hrefFor("flow", { id: String(live.session) }, { plan: live.flow.plan })`, holding the session's `title` in `font-semibold truncate` followed, when `flow.part` is not empty, by a muted ` <part>`, and on the right the chip: `<Badge variant={flowTone(live.flow)} data-tone={flowTone(live.flow)}>{flowChip(live.flow)}</Badge>`.

- [ ] **Step 1: Write the failing tests** in the new `flows.test.tsx`: the imports (`render`, `screen`, `within` from `@testing-library/react`; `afterEach`, `beforeEach`, `describe`, `expect`, `it`, `vi` from `vitest`; `Flows`, `FlowSummary` from `@/api/gen`; `useFlows` from `@/api/reads`; `hrefFor` from `@/app/routes`; `fixtureFlows` from `@/fixtures`; `FlowsBody` default from `./FlowsBody`; `flowTone` from `../fleet/SessionRow`), the `useFlows` mock, `NOW` as `Date.parse("2026-11-19T12:00:00Z")` with `vi.useFakeTimers({ toFake: ["Date"] })` and `vi.setSystemTime(NOW)` in a `beforeEach`, an `afterEach` (`vi.useRealTimers()`, `vi.restoreAllMocks()`), and a helper `show(opts: { flows?: Flows; error?: string; loading?: boolean })` that sets the mock's answer (`flows` default `undefined`, `error` default `""`, `reload` a `vi.fn()`) and renders `<FlowsBody />`. A `describe("FlowsBody", …)` holding:

  - `draws Loading… until the flows arrive, and the read's error when none is held`: `show({ loading: true })` draws "Loading…" (`role="status"`); `show({ error: "loom answered 500" })` draws that text in `role="alert"` and no "Loading…".
    Fails without: `draws Loading… until the flows arrive, and the read's error when none is held`: the error branch removed (it says "Loading…" over a failure), or "Loading…" drawn beside the error.
  - `keeps the held flows drawn under a failed read's error`: `show({ flows: fixtureFlows(), error: "loom answered 500" })` draws that text (`role="alert"`) and the live flow `flow-run`.
    Fails without: `keeps the held flows drawn under a failed read's error`: the error replacing the boxes.
  - `the live box: each flow a link to its board with its title, part and chip`: the legend `live`; two links, in order, `flow-run` with `href` `hrefFor("flow", { id: "6" }, { plan: "docs/superpowers/plans/2026-11-09-flow-visualisation.md" })` and the chip text `flow 2/5 · asks · $4.71`, and `flow-docs` with `href` `hrefFor("flow", { id: "4" }, { plan: "docs/superpowers/plans/2026-11-08-job-exploration.md" })` and `flow · draft 3/4`; with the first flow's `part` set to `part 2 of 3` that text is in the first link and the second's has none.
    Fails without: `the live box: each flow a link to its board with its title, part and chip`: the plan left out of the link, the title taken from the flow instead of the session, the part dropped, or the chip not `flowChip`.
  - `the live chips take the fleet row's tones`: the first chip has `data-tone` `attention` (the flow asks), the second `docs`; a live flow whose `status` is `lost` has `error`, one with `unreadable: "bad plan"` has `error`.
    Fails without: `the live chips take the fleet row's tones`: `data-tone` left off or a fixed tone (every chip `docs`), or `flowTone` not the fleet row's.
  - `flowTone: attention while it asks, error when lost or unreadable, else docs`: a summary (the fixture's first live flow's) with `asks` `blocked-decision` is `attention`; with `asks` `""` and `status` `lost` is `error`; with `asks` `""` and `unreadable` `bad` is `error`; with `asks` `""` and `status` `running` is `docs`; and `asks` wins over `lost`.
    Fails without: `flowTone: attention while it asks, error when lost or unreadable, else docs`: `flowTone` not exported (the import is `undefined` and the call throws), or a term of its test inverted.
  - `with no live flow the box says so`: `flows.live: []` and `flows.live: null` each draw "No session's worktree holds a flow." under the legend `live` and no link into a board.
    Fails without: `with no live flow the box says so`: the empty line missing, or a null `live` not guarded (it throws).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flows/flows.test.tsx
```

Expected: the file fails to load with `Failed to resolve import "./FlowsBody" from "src/panels/flows/flows.test.tsx"`.

- [ ] **Step 3: Implement** `FlowsBody.tsx` as described (the past boxes arrive in the next task; the section holds what this task draws), and export `flowTone` in `SessionRow.tsx`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flows/flows.test.tsx src/panels/fleet
```

Expected: every file passes (the fleet's own tests still read `flowTone` through the row).

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/flows/FlowsBody.tsx internal/web/ui/src/panels/flows/flows.test.tsx
git commit -m "feat(web): the host's flows: its states and the live box" -- internal/web/ui/src/panels/flows/FlowsBody.tsx internal/web/ui/src/panels/flows/flows.test.tsx internal/web/ui/src/panels/fleet/SessionRow.tsx
```

### Task 8.2: The past boxes, their bars and the empty line

**Files:**
- Modify: `internal/web/ui/src/panels/flows/FlowsBody.tsx`
- Modify: `internal/web/ui/src/panels/flows/flows.test.tsx`
- Test: `internal/web/ui/src/panels/flows/flows.test.tsx`

Names (private to the story), in `FlowsBody.tsx`: the constants `SHOWN = 8` and `BARS = 5`, `function repoName(key: string): string`, `function stemName(stem: string): string` and the components `PastBox` and `RepoBars`. It uses `Bars`, `median` and `usd` (`../flow/parts`), `ago` (`../fleet/SessionRow`) and the run's `PastRun` type.

Behaviour, placed after the live box:

- `repoName(key)`: a records key is the main checkout's path with every other character made `-`, so the part after the last `-Source-` when the key has one (`-tb-Source-Personal-loom` is `Personal-loom`), else the key with its leading dashes dropped (`-tb-elsewhere` is `tb-elsewhere`).
- Grouping: `flows.past` (null as none) grouped by `key`, the boxes in the order the keys first appear, each box's runs in the order the answer lists them (newest first).
- Past box: the legend `past · <repoName>`; its first `SHOWN` runs, one row (`flex min-h-11 items-center gap-2`) each, holding the run's stem with a leading `YYYY-MM-DD-` dropped (`stemName`) and, on the right (`ml-auto text-right text-xs`), `unreadable` in `text-error` when the run's `unreadable` is not empty, else, muted, these parts joined with ` · ` and each left out when empty: `<stories> st`, `usd(usd)` for a run with a number, `ended`, and `ago(end)` for a run with an `end`; then, when the repo has more than `SHOWN` runs, a small faint `and <n> more` with `n` the rest; then the bars.
- Bars (`RepoBars`): the repo's runs (all of them, not only the shown) that have a number `usd` and `stories` above 0, the first `BARS` of them (the newest), each as `usd / stories`, reversed so the oldest comes first. With fewer than 2 such values nothing is drawn. Else `<Bars values={per} current={null} />` and a small faint `<p>` `run cost per story, last <n> runs · median <usd(median(per))>` with `n` the number of values.
- `flows.past` null or empty: a small faint `<p>` "No run records yet." after the live box (no past box).

- [ ] **Step 1: Write the failing tests** in `flows.test.tsx`, in a `describe("the past runs", …)` using the same `show` and `fixtureFlows()` (a helper `run(over)` builds a `PastRun` for the tests that need their own: `{ key: "-tb-Source-Personal-loom", file: "r.md", stem: "2026-01-01-r", plan: "p.md", stories: 2, ended: "done", end: <ISO>, usd: 4, unreadable: "" }`):

  - `one past box for each repo, in first-seen order, named by the part after -Source-`: the fixture draws the legends `past · Personal-loom` and `past · Academia-kermit` in that order; with the fixture's kermit run listed first the order is `Academia-kermit` then `Personal-loom`; a run keyed `-tb-elsewhere` is `past · tb-elsewhere`.
    Fails without: `one past box for each repo, in first-seen order, named by the part after -Source-`: the boxes sorted by name, the key used raw (`past · -tb-Source-Personal-loom`), or the leading dashes kept.
  - `a run's row: its stem without the date, then stories, cost, how it ended and its age`: the loom box's three rows read `pwa-conversation-first` with `4 st · $10.08 · stopped · 3d`, `work-scheduler` with `6 st · $14.20 · done · 5d` and `loom-pwa` with `9 st · $31.77 · supervised · 17d`, in that order; the kermit box's row reads `per-query-index-build` with `23 st · $58.40 · supervised · 18d`.
    Fails without: `a run's row: its stem without the date, then stories, cost, how it ended and its age`: the date prefix kept, a part left out or reordered, the age taken from `ended`, or the cost not passed through `usd`.
  - `a run with no cost or no end leaves that part out, and an unreadable run says only that`: a run with `usd: null` and `end: ""` reads `2 st · done`; a run with `unreadable: "bad record"` reads `unreadable` in `text-error` with no stories, cost or age.
    Fails without: `a run with no cost or no end leaves that part out, and an unreadable run says only that`: `usd(null)` drawn as `$NaN` or an empty ` · ` part, or the unreadable run's figures shown.
  - `8 runs are shown and the rest are counted`: a repo of 10 runs shows 8 rows and `and 2 more`; a repo of 8 shows 8 rows and no `and`; a repo of 9 says `and 1 more`.
    Fails without: `8 runs are shown and the rest are counted`: the cap at another number, the count `n - 8` off by one, or the line drawn for exactly 8.
  - `the bars: the last 5 priced runs, oldest first, with their median`: the loom box draws 3 `[data-slot="bar"]` elements (and the kermit box none: one priced run is under 2) whose heights, read from `style.height` and parsed, are close to `100`, `67.04` and `71.39` (the per-story costs `31.77 / 9`, `14.2 / 6` and `10.08 / 4`, the oldest run first, each an exact share of the tallest), and the caption `run cost per story, last 3 runs · median $2.52`.
    Fails without: `the bars: the last 5 priced runs, oldest first, with their median`: the order not reversed (the first bar is `71.39`), the cost per run not divided by its stories (the heights follow `31.77`, `14.2`, `10.08`), or the median not the middle value.
  - `the bars take the newest 5 priced runs, and skip a run with no price or no stories`: a repo of 7 priced runs draws 5 bars and `last 5 runs` (the 6th and 7th, older, are left out); a repo whose newest run has `usd: null` and whose next has `stories: 0` draws bars from the others; a repo with one priced run and others unpriced draws no bar and no caption.
    Fails without: `the bars take the newest 5 priced runs, and skip a run with no price or no stories`: the cap taken from the shown rows (`SHOWN`) or removed (7 bars), a run with no price counted (`NaN` or a 0-height bar), or a run with 0 stories dividing by zero.
  - `the bars are drawn from every run, not only the 8 shown`: a repo of 10 runs whose first 8 (the newest, the rows shown) have `usd: null` and whose 9th and 10th (older, not shown) are priced draws 2 bars and the caption `run cost per story, last 2 runs · median <usd of the middle of the two>`.
    Fails without: `the bars are drawn from every run, not only the 8 shown`: the bars computed from the 8 shown rows (no bar).
  - `no run records says so`: `flows.past: []` and `flows.past: null` each draw "No run records yet." and no `past ·` legend; with a past run it is not drawn.
    Fails without: `no run records says so`: the line missing, drawn beside run records, or a null `past` not guarded (it throws).

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flows/flows.test.tsx
```

Expected: the new tests fail (no `past ·` legend), the Task 8.1 tests still pass.

- [ ] **Step 3: Implement** the past boxes, `repoName`, `stemName` and the bars in `FlowsBody.tsx` as described.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flows/flows.test.tsx
```

Expected: every test in the file passes.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the host's flows: the past runs by repo with their cost bars" -- internal/web/ui/src/panels/flows/FlowsBody.tsx internal/web/ui/src/panels/flows/flows.test.tsx
```

### Task 8.3: `FlowsHead` and the `flows` panel

**Files:**
- Create: `internal/web/ui/src/panels/flows/index.tsx`
- Modify: `internal/web/ui/src/panels/index.ts` (one import after the last one)
- Modify: `internal/web/ui/src/panels/flows/flows.test.tsx`
- Test: `internal/web/ui/src/panels/flows/flows.test.tsx`

Names this task defines (contract, Story 8), in `panels/flows/index.tsx`: `function FlowsHead(): ReactElement` (no props, as `FleetHead` takes none) and `export const flowsPanel`. The module's other name is private: `const FlowsBody = lazy(() => import("./FlowsBody"))`.

- `FlowsHead`: `<ContextRow back={hrefFor("fleet")} title="Flows" actions={…} />`, the action a `<button type="button" aria-label="Read the flows again">` showing `↻`, `min-h-11 min-w-11`, calling `reload` from `useFlows()` (`@/api/reads`) on a click. It takes no props.
- `flowsPanel = definePanel({ id: "flows", title: () => "Flows", Head: FlowsHead, Body: FlowsBody })`: no `tab` and no `appliesTo`.
- The module imports nothing from `../flow/parts` and no `FlowsBody` statically (a static import pulls the body out of its chunk into the entry script); the head's `useFlows` is the entry's own `@/api/reads`.
- `panels/index.ts` gains `import "./flows";` after its last import line, so the panel registers.

- [ ] **Step 1: Write the failing tests** in `flows.test.tsx` (imports: `FlowsHead`, `flowsPanel` from `./index`; `panelFor` from `@/app/panels`; `PanelHost` from `@/app/shell`; `userEvent` from `@testing-library/user-event`; and `import "@/panels"` so the registration is read as the app has it), a `describe("the flows panel", …)` holding:

  - `FlowsHead: a way back to the fleet, the title, and a button that reads the flows again`: with the mock's `reload` a `vi.fn()`, `<FlowsHead />` draws a link `‹ fleet` whose `href` is `hrefFor("fleet")`, a heading `Flows`, and a button named `Read the flows again` that is a 44px target (`min-h-11`); one click calls `reload` once (the head sends no read of its own).
    Fails without: `FlowsHead: a way back to the fleet, the title, and a button that reads the flows again`: the button not wired to `reload` (no call), the back link pointing elsewhere, or the title missing.
  - `FlowsHead and FlowsBody both read through useFlows`: rendering both leaves the mock called by each (two callers, one shared query in the real hook); the head's `reload` is the one the mock answered.
    Fails without: `FlowsHead and FlowsBody both read through useFlows`: the head holding its own state or a `fetch` of its own (`reload` is never the mock's).
  - `the panel is registered under flows with its head, a lazy body and no tab`: `panelFor("flows")` is `flowsPanel`; its `Head` is `FlowsHead`; its `tab` and `appliesTo` are `undefined`.
    Fails without: `the panel is registered under flows with its head, a lazy body and no tab`: `panels/index.ts`'s import missing (`panelFor` answers `undefined`), a `tab` added (a tab on every session), or the head not `FlowsHead`.
  - `the flows panel draws its body through PanelHost once its chunk loads`: with the `useFlows` mock answering `fixtureFlows()`, `render(<PanelHost panel="flows" props={{ sessionId: undefined, params: {}, search: {} }} />)` shows, after `await screen.findByText("flow-run")`, the legend `live`.
    Fails without: `the flows panel draws its body through PanelHost once its chunk loads`: the body not the default export of `FlowsBody` (the lazy import fails into the error boundary), or the panel's `Body` not lazy-wrapped from `./FlowsBody`.

- [ ] **Step 2: Run it to see it fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flows/flows.test.tsx
```

Expected: the file fails to load with `Failed to resolve import "./index" from "src/panels/flows/flows.test.tsx"`.

- [ ] **Step 3: Implement** `index.tsx` as described and add the import to `panels/index.ts`.

- [ ] **Step 4: Run it to see it pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/flows src/app/shell.test.tsx src/app/panels.test.ts
```

Expected: all files pass (the shell's and the registry's tests define their own panels or draw `Placeholder` directly, and import no panel module).

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/flows/index.tsx
git commit -m "feat(web): the flows panel and its head" -- internal/web/ui/src/panels/flows/index.tsx internal/web/ui/src/panels/index.ts internal/web/ui/src/panels/flows/flows.test.tsx
```

### Task 8.4: `FlowsBody` is a chunk of its own: `checkLazyViews`, the bundle and the screenshot

**Files:**
- Modify: `internal/web/static_budget_test.go` (`checkLazyViews` and its table test)
- Modify: `internal/web/dist/` (rebuilt)
- Test: `internal/web/static_budget_test.go`

Behaviour. The story adds one stem to what the tree holds and quotes none of its text: Stories 6 and 7 added theirs, and Stories 9 and 10 add theirs, so add this at the end of the list as it is when this task runs.

- `checkLazyViews`' name list (the `[]string{…}` literal in its `for` loop) gains `"FlowsBody"` at its end, and the function's doc comment names the host's flows beside the screens it already names.
- In `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`: the `all` slice gains `"/assets/FlowsBody-g.js"` at its end (a letter no other entry of the slice uses; take the next free one if `g` is in use); the table gains two cases after the last case Story 7 added: `no flows chunk` (`build(entry, <all without the FlowsBody file>...)`, the wanted text `FlowsBody`) and `a preloaded flows chunk` (`build(entry+`<link rel="modulepreload" crossorigin href="/assets/FlowsBody-g.js">`, all...)`, the wanted text `/assets/FlowsBody-g.js`); the existing cases stay as they are.

The test reads the committed bundle, so the bundle is rebuilt in this task: `pnpm run build` writes `internal/web/dist/`, the Vite build puts the body in `assets/FlowsBody-<hash>.js` (imported lazily, so `index.html` does not name it).

- [ ] **Step 1: Write the failing tests**: edit the table test as above first. The two new table cases pass at once (they test the function's list with fabricated assets), so the failing one is the bundle's: with `"FlowsBody"` in the list, `TestDist_LazyViewsAreChunksOfTheirOwn` reads the committed bundle, which has no `FlowsBody` chunk yet.
  - `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/no_flows_chunk`: the assets lack `FlowsBody` and the error names it.
    Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/no_flows_chunk`: `"FlowsBody"` removed from the list (no error, `want` not found).
  - `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/a_preloaded_flows_chunk`: `index.html` preloads `FlowsBody-g.js` and the error names it.
    Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/a_preloaded_flows_chunk`: `"FlowsBody"` removed from the list, or the page check dropped.
  - `TestDist_LazyViewsAreChunksOfTheirOwn`: the committed bundle holds exactly one `FlowsBody-<hash>.js` that `index.html` does not name.
    Fails without: `TestDist_LazyViewsAreChunksOfTheirOwn`: the panel importing `FlowsBody` statically (it lands in the entry script and no chunk of that name exists), or the bundle not rebuilt after this story's source changes.

- [ ] **Step 2: Run it to see it fail**

```bash
CGO_ENABLED=0 go test ./internal/web/ -run 'TestDist_LazyViewsAreChunksOfTheirOwn|TestCheckLazyViews'
```

Expected: `TestDist_LazyViewsAreChunksOfTheirOwn` fails with `the bundle has no lazy chunk for FlowsBody: its panel is imported statically, or the build renamed it`; the table's cases pass.

- [ ] **Step 3: Implement** the list, comment and table edits, then rebuild the bundle and check the chunk, the entry page and the first-load budget:

```bash
pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build
ls internal/web/dist/assets/FlowsBody-*.js
bash -c '! grep -q FlowsBody internal/web/dist/index.html'
```

Expected: the build succeeds, `ls` prints one `internal/web/dist/assets/FlowsBody-<hash>.js` path and the `grep` check exits 0 (the page does not name the chunk).

- [ ] **Step 4: Run it to see it pass**, with the story's full gates and the screenshot of the flows screen under the real Content-Security-Policy (it exits 1 on a console error or an unanswered route):

```bash
bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens flows -out "$d" && echo "$d/flows.png"'
```

Expected: every check passes (`dist:check` included), `ok github.com/aidan-bailey/loom/internal/web` (`TestDist_FirstLoadIsWithinBudget` among them) and the last line prints the path of `flows.png`. Read that PNG: it shows the head `‹ fleet`, `Flows` and ↻, the live box with the two flows and their chips (amber for the one that asks), the `past · Personal-loom` box with its three runs and its three cost bars with the caption, and the `past · Academia-kermit` box with one run and no bars.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "build(web): the host's flows is a chunk of its own, pinned in checkLazyViews, and the bundle rebuilt" -- internal/web/static_budget_test.go internal/web/dist
```

## Story 9: New session, and "Loading…" for the session it creates

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/new/index.tsx`, `internal/web/ui/src/panels/new/NewSessionBody.tsx`, `internal/web/ui/src/panels/new/new.test.tsx`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/session/SessionHead.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/session/session.test.tsx`, `internal/web/ui/src/panels/agent/Agent.tsx`, `internal/web/ui/src/panels/agent/agent.test.tsx`, `internal/web/ui/src/panels/job/index.tsx`, `internal/web/ui/src/panels/job/job.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
- **Interfaces:** exposes the `new` panel, `NewHead`, the exported `FILTER_KEY`, and the pending heads; uses Story 2's `Select` parts, `Input` and part 2's `Textarea`, Story 3's `useLaunchDefaults`, Story 4's `session.create`, `launchBody`, `LaunchForm` and `useIsPending`, part 2's `utf8Length`, `MAX_MESSAGE_BYTES`, `Collapsible`, `CommandButton`'s `disabled`, part 1's `ContextRow`, `useLink`, `useFleet` and `definePanel`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens new,conversation,agent,job -out "$d"'`
- **Review:** deep
- **Form:** brief (a port of `newsession.mjs` over Story 4's command and Story 3's read; the 64 KiB bound reuses part 2's `utf8Length` and `MAX_MESSAGE_BYTES` on the end-trimmed prompt, and the pending heads read Story 4's `useIsPending`, each a named test; the owner kept it brief, 2026-11-20)

This story ports the classic new-session screen (`internal/web/static/js/newsession.mjs`) to the new app and makes the screens of a session just created say "Loading…" until the snapshot lists it (spec Part 3 settlements 7 and 8). The `new` panel has a `NewHead` and a `React.lazy` Body over `panels/new/NewSessionBody.tsx`; the form is built from Story 2's `Select` parts and `Input`, part 2's `Textarea` and `Collapsible`, Story 3's `useLaunchDefaults` and Story 4's `session.create` command and `launchBody`, and "Start session" is that command drawn by `CommandButton`, so the stale gate, the in-flight key and the toasts are the registry's. The pending heads read Story 4's `useIsPending`. The story is brief: each task gives the behaviour, the contract names it uses and defines, and each test by name with what it asserts and what makes it red; the developer writes the code. None of it holds a landmine marker, and the developer runs at the default effort. Three tasks: the pending heads, the form, and the panel with its chunk and the rebuilt bundle. Each leaves the tree green.

### Task 9.1: "Loading…" while the route's session is pending

**Files:**
- Modify: `internal/web/ui/src/session/SessionHead.tsx`
- Modify: `internal/web/ui/src/session/NoSession.tsx`
- Modify: `internal/web/ui/src/panels/agent/Agent.tsx` (`AgentHead`)
- Modify: `internal/web/ui/src/panels/job/index.tsx` (`JobHead`)
- Test: `internal/web/ui/src/session/session.test.tsx`
- Test: `internal/web/ui/src/panels/agent/agent.test.tsx`
- Test: `internal/web/ui/src/panels/job/job.test.tsx`

Behaviour. A session a 201 just created is pending (`usePending`, Story 4, `store/pending.ts`) until the snapshot lists it, so for a moment the route names a session the store does not hold. Today its heads draw an empty title and `NoSession` says "This session is gone." (classic draws "gone" under a "Loading…" head; spec settlement 8 fixes that). Change four components, each calling Story 4's `useIsPending(id: number | undefined): boolean`:

- `SessionHead`, `AgentHead` and `JobHead`: for `props.sessionId` that `useSession` does not list and that is pending, the context row keeps its way back to the fleet (`hrefFor("fleet")`) and its title is `Loading…` (not the empty string). A listed session, or an unlisted one that is not pending, draws what it draws today. Call `useIsPending(props.sessionId)` before the `if (session === undefined)` early return of each (a hook after an early return changes the hook order when the snapshot lists the session).
- `NoSession` keeps its signature `NoSession(): ReactElement` (Stories 6 and 7 draw `<NoSession />` and every body passes it nothing). It reads the route's session itself: `panelProps(useRoute()).sessionId` (both from `@/app/routes`), and when `useIsPending` holds that id it draws the same `<p role="status">` with the text `Loading…`, else "This session is gone." as today.

Names used: `useIsPending` (Story 4), `useRoute` and `panelProps` (part 1, `app/routes.tsx`), `usePending` (Story 4, in the tests only, `usePending.setState({ id })` and `usePending.getState().clear()`).

- [ ] **Step 1: Write the failing tests**

Each test file gets its own `describe("pending session", ...)` block (so no existing block changes) with an `afterEach(() => { usePending.getState().clear(); })` of its own and the imports it needs (`usePending` from `@/store/pending`). The route is set before rendering with `window.history.replaceState(null, "", "#/s/9")` (the files' existing `afterEach` resets the hash). Session 9 is listed by none of the stores these files seed.

In `session/session.test.tsx` (the file's `load()` helper stores a snapshot holding session 7 only):

  - `SessionHead titles a pending session Loading…`: `load()`, `usePending.setState({ id: 9 })`, render `<SessionHead sessionId={9} params={{ id: "9" }} search={{}} />`; the heading's text is `Loading…` and the link's `href` is `#/`.
    Fails without: `SessionHead titles a pending session Loading…`: the `useIsPending` branch in `SessionHead` removed (the heading is empty).
  - `SessionHead keeps an empty title for an unlisted session that is not pending, and for another session's pending id`: `load()`, render the same head twice, once with `usePending.getState().id` null and once after `usePending.setState({ id: 8 })`; both headings are empty.
    Fails without: `SessionHead keeps an empty title for an unlisted session that is not pending, and for another session's pending id`: the pending test ignoring the id (any pending id titles every unlisted session) or `Loading…` drawn unconditionally.
  - `SessionHead keeps a listed session's title while its id is pending`: `load({ title: "listed" })` (session 7), `usePending.setState({ id: 7 })`, render with `sessionId={7}`; the heading is `listed`.
    Fails without: `SessionHead keeps a listed session's title while its id is pending`: the `Loading…` branch run before the `session === undefined` check (a listed session is titled `Loading…`).
  - `NoSession says Loading… for the route's pending session`: `load()`, `window.history.replaceState(null, "", "#/s/9")`, `usePending.setState({ id: 9 })`, render `<NoSession />`; the `status` role holds `Loading…` and the page does not contain "This session is gone.".
    Fails without: `NoSession says Loading… for the route's pending session`: `NoSession` not reading the pending slice or not reading the route's id.
  - `NoSession still says gone for an unpending session, another session's pending id, and a route with no session`: three renders in turn, each "This session is gone.": pending null at `#/s/9`; pending 8 at `#/s/9`; pending 9 at the fleet hash `#/`.
    Fails without: `NoSession still says gone for an unpending session, another session's pending id, and a route with no session`: the id comparison dropped or the `undefined` route id treated as pending (`Loading…` shown for every route while anything is pending).

In `panels/agent/agent.test.tsx` (the file's `props(id)` helper and `wrapper` exist; `useFleet` already holds the fixture snapshot, which lists sessions 1 to 7 and not 99):

  - `AgentHead titles a pending unlisted session Loading… and keeps its way back to the fleet`: `usePending.setState({ id: 99 })`, render `<AgentHead {...props(99)} />` in `wrapper`; the heading is `Loading…` and the one link's `href` is `#/`.
    Fails without: `AgentHead titles a pending unlisted session Loading… and keeps its way back to the fleet`: the `useIsPending` branch in `AgentHead` removed.
  - `AgentHead keeps its empty title for an unlisted session that is not pending`: the same render with pending null: the heading is empty (the file's existing test for 99 stays as it is and also guards this).
    Fails without: `AgentHead keeps its empty title for an unlisted session that is not pending`: `Loading…` drawn for every unlisted session.

In `panels/job/job.test.tsx` (the file's `seed`, `makeSession` and `resetFleet` from `@/commands/testkit`):

  - `JobHead titles a pending unlisted session Loading…`: `seed([makeSession({ id: 1 })])`, `usePending.setState({ id: 9 })`, render `<JobHead sessionId={9} params={{ id: "9", job: "214" }} search={{}} />`; the heading is `Loading…` and the link's `href` is `#/`.
    Fails without: `JobHead titles a pending unlisted session Loading…`: the `useIsPending` branch in `JobHead` removed.
  - `JobHead keeps its empty title for an unlisted session that is not pending, and for another session's pending id`: the same render with pending null, then with `{ id: 8 }`; both headings are empty.
    Fails without: `JobHead keeps its empty title for an unlisted session that is not pending, and for another session's pending id`: the pending test ignoring the id.

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/session/session.test.tsx src/panels/agent/agent.test.tsx src/panels/job/job.test.tsx -t "pending"
```

Expected: the `Loading…` tests fail with an `Expected: "Loading…"` / `Received: ""` heading (for `NoSession`, the text "This session is gone." found where `Loading…` was expected); the "keeps" tests pass.

- [ ] **Step 3: Implement** the four changes above. Format with `pnpm -C internal/web/ui exec prettier --write` on the seven files.

- [ ] **Step 4: Run them to see them pass**

```bash
pnpm -C internal/web/ui exec vitest run src/session src/panels/agent src/panels/job && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: every test passes (the files' existing tests too: `NoSession says the session is gone` renders at the fleet route, which has no session id), and the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): session views say Loading… while a created session is pending" -- internal/web/ui/src/session/SessionHead.tsx internal/web/ui/src/session/NoSession.tsx internal/web/ui/src/session/session.test.tsx internal/web/ui/src/panels/agent/Agent.tsx internal/web/ui/src/panels/agent/agent.test.tsx internal/web/ui/src/panels/job/index.tsx internal/web/ui/src/panels/job/job.test.tsx
```

### Task 9.2: The new-session form

**Files:**
- Modify: `internal/web/ui/src/panels/fleet/index.tsx` (the `FILTER_KEY` constant)
- Create: `internal/web/ui/src/panels/new/NewSessionBody.tsx`
- Create: `internal/web/ui/src/panels/new/new.test.tsx`

Names this task defines (contract, Story 9): `FILTER_KEY` exported, `const FILTER_KEY = "loom.filter"` in `panels/fleet/index.tsx` (change `const` to `export const`, its comment and value as they are); and the default export `NewSessionBody(): ReactElement` (it reads none of the panel's props, so it takes no parameter, as `FleetHead` takes none: an unused `props` fails the typed ESLint config; the lazy `Body` still fits `ComponentType<PanelProps>`) of `panels/new/NewSessionBody.tsx` (a default export, since the panel's `React.lazy` takes a module's default, as `JobBody.tsx` does). Names used: Story 2's `Select`, `SelectTrigger`, `SelectValue`, `SelectContent`, `SelectGroup`, `SelectLabel`, `SelectItem` (`Select` is Base UI's Root, so `value`, `onValueChange`, `items` and `disabled` pass through; `SelectTrigger` passes `id` and `aria-label`; `SelectItem` takes `value`) and `Input`; part 2's `Textarea`, `Collapsible`, `CollapsibleTrigger`, `CollapsibleContent`, `Button`, `utf8Length` and `MAX_MESSAGE_BYTES` (`@/lib/utf8`); Story 3's `useLaunchDefaults(workspace: number | undefined, live: boolean): LaunchDefaultsView` (`{ defaults, error, loading, retry }`); Story 4's `launchBody(form: LaunchForm, defaults: LaunchDefaults): CreateBody` and `LaunchForm` (`@/commands/launch`) and the command `session.create` (args `{ body: CreateBody }`, title "Start session", `when` the panel `new`); part 2's `CommandButton` (`disabled`) with `commandById`, `runCommand` and `useCommandContext` (`@/app/commands`); `useFleet` and `useLink` (`@/store/fleet`).

Behaviour of `NewSessionBody`:

- **No snapshot:** while `useFleet` holds none, the body is `<p>` "Loading…" (as the fleet's), and nothing else; the form below mounts when the first snapshot lands, so its initial state is computed from that snapshot.
- **Workspace.** The initial choice is, in order: the workspace whose id (as a string) the fleet's filter holds, read from localStorage under `FILTER_KEY` as the fleet reads it (a JSON string holding an id or `""`; anything else, a throw included, is `""`; the fleet's `readFilter` stays private, so the body parses the value itself in a small private helper); else the first workspace with `shown !== false`; else the first workspace; else none. It is state of the form (a string, `""` for none), changed by the Select. The Select is Story 2's, labelled `Workspace` (its accessible name), `items` the workspaces as `{ value: String(id), label: label || name }`; when any workspace has `shown === false` the options are two `SelectGroup`s labelled `Active` (the shown ones) and `Other workspaces`, else one flat list. With no workspace the Select is replaced by the text "No workspace is open." and Start session stays off. A chosen id that the snapshot later stops listing leaves Start session off.
- **Title and prompt.** The title is Story 2's `Input` (`autoCapitalize="none"`, `autoComplete="off"`), accessible name `Title`; the prompt is part 2's `Textarea` (`rows={6}`), accessible name `Prompt`; both controlled by the form's own state, not the drafts store. When `utf8Length(prompt.trimEnd()) > MAX_MESSAGE_BYTES` an element with `role="alert"` says "Too long: a prompt is at most 64 KiB." (class `text-[12px] text-error`, as `ReplyBox`'s).
- **Defaults.** `useLaunchDefaults(chosenWorkspaceId, live)` with `live` true when `useLink().state === "live"`. While `error !== ""` the body shows, in a `role="alert"` paragraph, "Could not load the launch defaults." and a secondary `Button` `Retry` calling the view's `retry`.
- **Launch options.** Once `defaults` is defined, a part 2 `Collapsible` (closed) with a trigger `Launch options` holds four Selects labelled `Account`, `Model`, `Permission mode` and `Effort` (accessible names), the Account one only when `defaults.accounts` is not null and not empty. Each Select's options are its list (`accounts`, `models`, `permission_modes`, `efforts`, null read as empty) with the default (`defaults.account`, `.model`, `.permission_mode`, `.effort`) put first when the list lacks it, and a value of `""` is labelled `(default)` (as `items` of `{ value, label }`). The picks are the user's overrides on top of the defaults: hold them as an object of overrides keyed to the form (a pick shows `overrides[key] ?? defaults[key]`), and clear it in the same handler that changes the workspace, so a workspace change resets every pick and the defaults of the other workspace show; the defaults are held by Story 3's hook for the page's lifetime, so a stream that drops and returns leaves the picks as they are (no effect keyed on `live` or on `defaults` clears them). A Select's `onValueChange` may hand `null`; read it as `""`.
- **Start session** is `<CommandButton id="session.create" args={{ body }} disabled={!ready} />` with `body = launchBody({ workspace, title, prompt, picks }, defaults)` (`workspace` the chosen id as a number, `picks` the four values shown) built only once `ready`, else `args` is undefined. `ready` is true when `defaults` is defined, a listed workspace is chosen, `title.trim() !== ""` and the prompt fits; the stale gate and the in-flight key are the command's and the `CommandButton`'s. Enter in the title field (a `keyDown` for `Enter` that is not an IME composition) calls `runCommand(commandById("session.create"), ctx, { body })` with `ctx` from `useCommandContext()`, only when `ready` (and nothing when the command is not registered); there is no `<form>` element, since a form's submit would run the button's click as well. Nothing clears the form after a run: a refused create (409, 400, the toast is `session.create`'s) keeps every field as typed, and a 201 leaves the page.
- Layout: a single column, `flex flex-col gap-4 p-4`, each field a visible label above its control, Start session last and full width (`className` on the wrapper, the button as `CommandButton` draws it). Take colours from the tokens.

- [ ] **Step 1: Write the failing tests** in `new.test.tsx`

Set-up the file's helpers (the developer writes them):

- `vi.mock("@/api/reads", async (original) => ({ ...(await original<typeof import("@/api/reads")>()), useLaunchDefaults: vi.fn() }))`, and `import "@/commands"` so `session.create` is registered. The mock is given a per-workspace answer: `DEFAULTS[1]` `{ account: "", accounts: ["default", "work"], model: "opus", models: ["opus", "sonnet"], permission_mode: "", permission_modes: ["default", "acceptEdits"], effort: "high", efforts: ["low", "high"] }` and `DEFAULTS[2]` `{ account: "", accounts: null, model: "", models: ["opus", "sonnet"], permission_mode: "plan", permission_modes: ["plan", "default"], effort: "", efforts: ["low"] }`; `useLaunchDefaults` returns `{ defaults: DEFAULTS[workspace], error: "", loading: false, retry }` for a defined workspace when `live` is true, else `{ defaults: undefined, error: "", loading: true, retry }`; `retry` is a `vi.fn()` the test can read.
- Workspaces `W1 { id: 1, name: "alpha", label: "Alpha", shown: true }`, `W2 { id: 2, name: "beta", label: "", shown: true }` (drawn `beta`) and `W3 { id: 3, name: "gamma", label: "Gamma", shown: false }`. `show(workspaces, link = "live")` stores `{ ...fixtureSnapshot(), workspaces }` with `useFleet.setState({ snapshot, stale: link !== "live", link, updated: Date.now() })`, sets the hash `#/new`, and renders `<NewSessionBody />`; it returns `userEvent.setup()`.
- `api.create` spied (`vi.spyOn(api, "create")`) resolving a 201 `{ kind: "ok", status: 201, value: { id: 42, notices: null }, notices: [] }` unless a test says otherwise, `window.location.assign` spied with an empty implementation, the toast spy `spyOnAdd()` from `@/commands/testkit`; `afterEach` restores the mocks, clears `usePending`, calls `resetFleet()`, empties localStorage and resets the hash.

Tests, in `describe("NewSessionBody", ...)`:

  - `says Loading… until the first snapshot`: `useFleet` holds none; the text `Loading…` is shown and no `Workspace` combobox.
    Fails without: `says Loading… until the first snapshot`: the no-snapshot guard removed (the form's first render reads `snapshot.workspaces` of null and throws).
  - `starts on the fleet filter's workspace`: localStorage `loom.filter` holds `JSON.stringify("2")`, workspaces W1, W2; the `Workspace` combobox reads `beta`, and `useLaunchDefaults` was last called with `(2, true)`.
    Fails without: `starts on the fleet filter's workspace`: the filter not read, or read from another key than `FILTER_KEY` (the first shown, `Alpha`, is chosen).
  - `falls back to the first shown workspace, then the first one`: three renders in turn (each unmounted and the mocks reset): filter `""` with `[W3, W1]` reads `Alpha`; filter `JSON.stringify("9")` (an id no workspace has) with `[W1, W2]` reads `Alpha`; filter `""` with only `[W3]` reads `Gamma`.
    Fails without: `falls back to the first shown workspace, then the first one`: the `shown !== false` preference dropped (the first listed, `Gamma`, is chosen in the first case), or the final fallback dropped (nothing chosen in the third).
  - `a stored filter that is not a JSON string is read as none`: localStorage `loom.filter` holds `not json`; the first shown workspace is chosen and nothing throws.
    Fails without: `a stored filter that is not a JSON string is read as none`: the parse not guarded (the render throws).
  - `groups the workspaces Active and Other workspaces when one is hidden, and lists them flat when none is`: with `[W1, W3]` opening the `Workspace` combobox lists `Active` then `Alpha`, `Other workspaces` then `Gamma`; with `[W1, W2]` the opened list holds the options `Alpha` and `beta` and neither group label.
    Fails without: `groups the workspaces Active and Other workspaces when one is hidden, and lists them flat when none is`: the `shown === false` test inverted or removed (the groups are always or never drawn).
  - `says No workspace is open. and keeps Start session off with none`: workspaces `[]`; the text "No workspace is open." is shown, there is no `Workspace` combobox, `useLaunchDefaults` was called with `(undefined, true)`, and after typing a title `Start session` is still disabled.
    Fails without: `says No workspace is open. and keeps Start session off with none`: the empty-list branch removed (an empty Select is drawn), or `ready` not requiring a chosen workspace.
  - `reads the defaults only while the stream is live`: `show([W1], "offline")` calls `useLaunchDefaults` with `(1, false)` and no `Model` is offered; with the link `live` it is called with `(1, true)`.
    Fails without: `reads the defaults only while the stream is live`: `live` hard-coded true or not taken from `useLink().state`.
  - `Start session is off until the defaults, a title and a fitting prompt are there`: one workspace, link live. With the mock returning no defaults and a title typed, the button is disabled; with defaults and a blank title (`"   "`) it is disabled; a title `fix` enables it. Then, with `fireEvent.change` on the `Prompt` box: `"a".repeat(65536) + "   "` leaves it enabled and shows no alert (the end is trimmed first); `"a".repeat(65537)` disables it and shows the alert text "Too long: a prompt is at most 64 KiB."; `"é".repeat(32769)` (32,769 characters, 65,538 bytes) does the same.
    Fails without: `Start session is off until the defaults, a title and a fitting prompt are there`: any one of the four terms of `ready` dropped (each case above flips), the prompt not trimmed before the check (the 65,536-byte case is refused), or the length counted in characters rather than bytes (the `é` case is accepted).
  - `Start session is off while the snapshot is stale`: `show([W1], "offline")` with a title typed; the button is disabled even with the defaults mocked as loaded (the mock returns defaults when `live`; this test makes it return them regardless).
    Fails without: `Start session is off while the snapshot is stale`: the button not being the `session.create` command's `CommandButton` (a plain button ignores the stale gate).
  - `Launch options is closed at first and offers Account only when the defaults list accounts`: the trigger `Launch options` is there and no `Model` combobox until it is clicked; then `Account`, `Model`, `Permission mode` and `Effort` are all there for workspace 1, and with the filter naming workspace 2 (`accounts: null`) there is no `Account`.
    Fails without: `Launch options is closed at first and offers Account only when the defaults list accounts`: the `Collapsible` open by default, or the account Select drawn for an empty or null list.
  - `a pick's list puts a missing default first and labels the empty one (default)`: workspace 2, options open on `Model` (default `""`, list `opus`, `sonnet`) read `(default)`, `opus`, `sonnet` in that order and the trigger reads `(default)`; workspace 1's `Model` (default `opus`, already listed) reads `opus`, `sonnet` with no `(default)` and no second `opus`; workspace 1's `Permission mode` (default `""`, list `default`, `acceptEdits`) reads `(default)`, `default`, `acceptEdits`.
    Fails without: `a pick's list puts a missing default first and labels the empty one (default)`: the default not added when missing (no `(default)` option), added when present (a duplicate), or the `""` label dropped (an empty option).
  - `sends the trimmed title, the prompt's end trimmed and only the picks that differ from their defaults`: workspace 1; type `  fix the build ` in `Title` and `go on\n\n` in `Prompt` (`fireEvent.change`), open Launch options, pick `sonnet` in `Model`, click `Start session`; `api.create` was called once with, by `toStrictEqual`, `{ workspace: 1, title: "fix the build", prompt: "go on", model: "sonnet" }`, `window.location.assign` with `#/s/42`.
    Fails without: `sends the trimmed title, the prompt's end trimmed and only the picks that differ from their defaults`: the body not built through `launchBody` (an untrimmed field, or `account`/`effort` sent at their defaults), or the picks not reaching it (no `model`).
  - `changing the workspace resets the picks and keeps the title and prompt`: in workspace 1 pick `sonnet` for `Model` and type a title and a prompt; choose `beta` in the `Workspace` combobox; `useLaunchDefaults` was last called with `(2, true)`, `Model` reads `(default)` (workspace 2's default), `Title` and `Prompt` still hold what was typed; choosing `Alpha` again shows `Model` as `opus` (the default), not `sonnet`; Start session then sends a body without `model`.
    Fails without: `changing the workspace resets the picks and keeps the title and prompt`: the overrides not cleared in the workspace handler (`sonnet` survives), or the title and prompt cleared with them.
  - `a stream that drops and returns leaves the picks as they are`: pick `sonnet`; `act(() => useFleet.getState().setLink("offline"))` then `setLink("live")`; `Model` still reads `sonnet`.
    Fails without: `a stream that drops and returns leaves the picks as they are`: the picks reset by an effect on `live` or on `defaults` (the pick returns to `opus`).
  - `Enter in the title starts the session only when it is ready`: with defaults and a title `fix`, `user.type(title, "{Enter}")` runs `api.create` once; with a blank title, `{Enter}` runs nothing; with the stream offline, `{Enter}` runs nothing.
    Fails without: `Enter in the title starts the session only when it is ready`: the Enter handler absent (no call), not gated on `ready` (a call with a blank title or no defaults), or not through `runCommand` (a call while stale).
  - `a refused create keeps the form as typed`: `api.create` resolves `{ kind: "refused", status: 409, message: "Too many sessions." }`; fill the title and prompt, pick `sonnet`, click Start session; `await` the click's settling; the toast spy was called with an error whose description is `Too many sessions.`, `Title` and `Prompt` hold what was typed, `Model` still reads `sonnet`, Start session is enabled again, and `window.location.assign` was not called.
    Fails without: `a refused create keeps the form as typed`: the form cleared or unmounted after the run (a field is empty), or the command's `showOutcome` not reached.
  - `a failed read says so and Retry asks again`: the mock returns `{ defaults: undefined, error: "network", loading: false, retry }`; the alert text "Could not load the launch defaults." is shown, no `Launch options`, and Start session is disabled; clicking `Retry` calls `retry` once.
    Fails without: `a failed read says so and Retry asks again`: the error branch removed, or `Retry` not wired to the view's `retry`.

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/new/new.test.tsx
```

Expected: the file fails to load: `Failed to resolve import "./NewSessionBody"`.

- [ ] **Step 3: Implement** `NewSessionBody.tsx` and the `FILTER_KEY` export. Format with `pnpm -C internal/web/ui exec prettier --write src/panels/new/NewSessionBody.tsx src/panels/new/new.test.tsx src/panels/fleet/index.tsx`. If an assertion on an opened Select popup needs a happy-dom stub Story 2's tests also needed (a missing `ResizeObserver`, say), put it in `new.test.tsx`, not in `src/test-setup.ts`, and say so in the commit message body.

- [ ] **Step 4: Run them to see them pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: every test passes (the fleet's tests too: `FILTER_KEY` keeps its value), and the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/new/NewSessionBody.tsx internal/web/ui/src/panels/new/new.test.tsx
git commit -m "feat(web): the new-session form on Select, Input and session.create" -- internal/web/ui/src/panels/new/NewSessionBody.tsx internal/web/ui/src/panels/new/new.test.tsx internal/web/ui/src/panels/fleet/index.tsx
```

### Task 9.3: The `new` panel, its chunk and the rebuilt bundle

**Files:**
- Create: `internal/web/ui/src/panels/new/index.tsx`
- Modify: `internal/web/ui/src/panels/index.ts` (one import line)
- Modify: `internal/web/static_budget_test.go` (`checkLazyViews`' name list and `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`)
- Modify: `internal/web/dist/` (the rebuilt bundle)
- Test: `internal/web/ui/src/panels/new/new.test.tsx`

Names this task defines (contract, Story 9), in `panels/new/index.tsx`: `function NewHead(): ReactElement` (no parameter, as `FleetHead`: an unused `props` fails the typed ESLint config) and the panel `newPanel = definePanel({ id: "new", title: () => "New session", Head: NewHead, Body: NewSessionBody })` where `NewSessionBody = lazy(() => import("./NewSessionBody"))` (as `jobPanel` does over `JobBody`); the panel has no tab and no `appliesTo`. `NewHead` is `<ContextRow back={hrefFor("fleet")} title="New session" />` (part 1's `ContextRow`, `@/app/shell`; `hrefFor`, `@/app/routes`); a plain link, no command. Names used: Task 9.2's `NewSessionBody` default export and `definePanel` (`@/app/panels`).

- `panels/index.ts` imports `./new` beside the other panels (after the last `import` line there as it stands once Stories 6 to 8 have added theirs; add one line, change nothing else).
- `internal/web/static_budget_test.go` (spec settlement 19): add the stem `"NewSessionBody"` to the name list `checkLazyViews` loops over (append it after the last stem the list holds), and in `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne` add a chunk file `/assets/NewSessionBody-n.js` to the `all` slice (after its last chunk), a table case `"no new-session chunk"` (the page and every chunk of `all` but that one, wanting `NewSessionBody`) and a table case `"a preloaded new-session chunk"` (the page with a `<link rel="modulepreload" crossorigin href="/assets/NewSessionBody-n.js">` after `entry`, wanting that URL), each beside the cases the other stems already have, in their form.

- [ ] **Step 1: Write the failing tests**

In `new.test.tsx`, a second `describe("the new panel", ...)` (reusing Task 9.2's helpers):

  - `registers the new panel with its head and a lazy body`: `import { NewHead, newPanel } from "./index"`; `panelFor("new")` is `newPanel`, its `Head` is `NewHead`, `title({ sessionId: undefined, params: {}, search: {} })` is `New session`, and it has no `tab`.
    Fails without: `registers the new panel with its head and a lazy body`: the `definePanel` call absent or its id other than `new`.
  - `NewHead is "New session" with a way back to the fleet`: render `<NewHead />`; the heading is `New session` and the one link's `href` is `#/`.
    Fails without: `NewHead is "New session" with a way back to the fleet`: the title or the `back` href changed.
  - `draws the form through PanelHost once its chunk loads`: a snapshot with W1, hash `#/new`, render `<PanelHost panel="new" props={{ sessionId: undefined, params: {}, search: {} }} />` (from `@/app/shell`); `await screen.findByRole("combobox", { name: "Workspace" })`.
    Fails without: `draws the form through PanelHost once its chunk loads`: the `Body` not the lazy `NewSessionBody` (the screen stays on `BodyLoading` or draws nothing).

In `internal/web/static_budget_test.go`: the two table cases and the `all` entry above. Before the bundle is rebuilt, `go test ./internal/web/ -run 'TestDist_LazyViewsAreChunksOfTheirOwn'` must fail on the new stem.

    Fails without: `TestDist_LazyViewsAreChunksOfTheirOwn`: the panel's Body imported statically (the build puts `NewSessionBody` in the entry and no `NewSessionBody-<hash>.js` chunk exists: "the bundle has no lazy chunk for NewSessionBody"), or `index.html` naming the chunk.
    Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne` (the two new cases): the `"NewSessionBody"` stem absent from the list (neither case errors).

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/new/new.test.tsx -t "the new panel" ; CGO_ENABLED=0 go test ./internal/web/ -run 'TestDist_LazyViewsAreChunksOfTheirOwn|TestCheckLazyViews'
```

Expected: the Vitest run fails with `Failed to resolve import "./index"` for the new test's import; the Go run fails with `the bundle has no lazy chunk for NewSessionBody: its panel is imported statically, or the build renamed it` for the first test (the second passes once its cases are written, and fails on its two new cases while the stem is missing from the list).

- [ ] **Step 3: Implement** `panels/new/index.tsx`, the `./new` import and the Go changes, then rebuild the bundle:

```bash
pnpm -C internal/web/ui exec prettier --write src/panels/new/index.tsx src/panels/index.ts src/panels/new/new.test.tsx && pnpm -C internal/web/ui run build
```

Format the Go file with `gofmt -w internal/web/static_budget_test.go` (never `gofmt -w .`).

- [ ] **Step 4: Run them to see them pass**

```bash
bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens new,conversation,agent,job -out "$d"'
```

Expected: the checks pass; `go test` prints `ok  	github.com/aidan-bailey/loom/internal/web` (the first load stays within `firstLoadBudget`, the new chunk is named by no entry); webshot writes `new.png`, `conversation.png`, `agent.png` and `job.png` into `$d` and exits 0 with no missing request (the `#new` screen shows the form's Workspace, Title and Prompt, and Start session off). Open `new.png` once with the Read tool to confirm the form is drawn, not the splash.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/new/index.tsx
git commit -m "feat(web): the new-session panel as a lazy chunk" -- internal/web/ui/src/panels/new/index.tsx internal/web/ui/src/panels/index.ts internal/web/ui/src/panels/new/new.test.tsx internal/web/static_budget_test.go internal/web/dist
```

## Story 10: Settings

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/settings/index.tsx`, `internal/web/ui/src/panels/settings/SettingsBody.tsx`, `internal/web/ui/src/panels/settings/settings.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
- **Interfaces:** exposes the `settings` panel and `SettingsHead`; uses Story 2's `Switch`, part 2's `Textarea`, Story 4's `device.update`, `quickReplies.save` (its `onSaved`) and `device.unpair`, Story 5's `usePushStatus` and `push.enable`, part 1's `app.backToClassic`, `CommandButton`, `useCommandBusy`, `commandById`, `runCommand`, `commandEnabled`, `useCommandContext`, `ContextRow`, `useFleet`, `ago` and `definePanel`, Story 1's fixture device and notices
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens settings -out "$d"'`
- **Review:** deep
- **Form:** full (write concurrency and failure states: a Switch showing the asked value while its write is in flight and the stored one after, a refusal snapping it back, the quick-replies draft outliving a refusal, the pause row's expiry timer; the owner, 2026-11-20)

This story registers the `settings` panel and ports `settings.mjs` and `push.mjs`'s control (spec Part 3 settlements 9, 10 and 12). `panels/settings/index.tsx` holds the head (`SettingsHead`: "Settings", back to the fleet) and the panel, whose `Body` is `React.lazy` over `SettingsBody.tsx`, a chunk of its own that `checkLazyViews` pins. The body draws "Loading…" until the snapshot holds a device, then five sections. This phone: Paired as, Host, Build and `device.unpair`. App: `app.backToClassic`. Recent notices: newest first, each with its age, red when an error, "None." when empty. Notifications: the push control (`usePushStatus`'s words, `push.enable`'s "Turning on…" lines read from the command's in-flight state, which outlives a visit elsewhere), the red expired or failing line, two Switches over `device.update` that show the value asked for while the write is in flight and the stored value after it (a refusal snaps back), and the pause row, redrawn by a timer when the pause ends. Quick replies: the textarea over the stored list until typed in, Reset to defaults and Save through `quickReplies.save`, whose `onSaved` clears the draft, so a refusal leaves the typed text. The Switches run `device.update` with `runCommand`, since a Switch is not a `CommandButton`; every button is a `CommandButton`. Three tasks: the panel with the sections that need no commands of their own (10.1), Notifications (10.2), Quick replies and the webshot render (10.3). Each rebuilds the bundle. `SettingsHead` takes no parameter (as `FleetHead` does), since it reads nothing from its props. None of the code holds a landmine marker.

### Task 10.1: The panel, This phone, App and Recent notices

**Files:**
- Create: `internal/web/ui/src/panels/settings/index.tsx`
- Create: `internal/web/ui/src/panels/settings/SettingsBody.tsx`
- Create: `internal/web/ui/src/panels/settings/settings.test.tsx`
- Modify: `internal/web/ui/src/panels/index.ts` (its last import line)
- Modify: `internal/web/static_budget_test.go` (`checkLazyViews`' list, the `all` slice of its table test, a new test at the end of the file)
- Modify: `internal/web/dist/`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/ui/src/panels/settings/settings.test.tsx`:

```tsx
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/commands";
import type { Device, Notice, Outcome, Snapshot } from "@/api/gen";
import { api } from "@/api/request";
import { askConfirm } from "@/app/ConfirmSheet";
import { panelFor } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { askedSheet, resetFleet } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { SettingsHead, settingsPanel } from "./index";
import SettingsBody from "./SettingsBody";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

const NOW = Date.parse("2026-11-20T12:00:00Z");

const PHONE: Device = {
  name: "iphone",
  notify: { waiting: true, finished: false, paused_until: "" },
  push: "ok",
};

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  window.history.replaceState(null, "", "#/settings");
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  window.history.replaceState(null, "", window.location.pathname);
});

// live makes the store hold the canned snapshot with these fields changed, live.
function live(over: Partial<Snapshot> = {}): void {
  useFleet.setState({
    snapshot: {
      ...fixtureSnapshot(NOW),
      device: PHONE,
      notices: [],
      quick_replies: ["ok", "later"],
      host: "fresco",
      build: "v1.2.3",
      ...over,
    },
    stale: false,
    link: "live",
    updated: NOW,
  });
}

function ok<T>(value: T, status = 200): Outcome<T> {
  return { kind: "ok", status, value, notices: [] };
}

// section is the section of the screen under this heading.
function section(name: string): HTMLElement {
  const found = screen.getByRole("heading", { name }).closest("section");
  if (found === null) throw new Error(`the screen has no ${name} section`);
  return found;
}

const ago = (ms: number): string => new Date(NOW - ms).toISOString();

describe("the settings panel", () => {
  it("is registered with its head and its title", () => {
    expect(panelFor("settings")).toBe(settingsPanel);
    expect(settingsPanel.Head).toBe(SettingsHead);
    expect(settingsPanel.title({ sessionId: undefined, params: {}, search: {} })).toBe("Settings");
  });

  it("heads the screen with Settings and a way back to the fleet", () => {
    render(<SettingsHead />);
    expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
    expect(screen.getByRole("link")).toHaveAttribute("href", hrefFor("fleet"));
  });
});

describe("SettingsBody: Loading…", () => {
  it("says Loading… until the snapshot holds a device", () => {
    render(<SettingsBody />);
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "This phone" })).toBeNull();

    act(() => live());
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.getByRole("heading", { name: "This phone" })).toBeInTheDocument();
  });

  it("says Loading… for a saved snapshot that holds no device", () => {
    live({ device: undefined as unknown as Device });
    render(<SettingsBody />);
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });
});

describe("SettingsBody: This phone and App", () => {
  it("names the device, the host and the build", () => {
    live();
    render(<SettingsBody />);
    const phone = within(section("This phone"));
    expect(phone.getByText("Paired as")).toBeInTheDocument();
    expect(phone.getByText("iphone")).toBeInTheDocument();
    expect(phone.getByText("Host")).toBeInTheDocument();
    expect(phone.getByText("fresco")).toBeInTheDocument();
    expect(phone.getByText("Build")).toBeInTheDocument();
    expect(phone.getByText("v1.2.3")).toBeInTheDocument();
  });

  it("asks before it unpairs, and unpairs only on a yes", async () => {
    const unpair = vi.spyOn(api, "deleteDevice").mockResolvedValue(ok(undefined, 204));
    live();
    render(<SettingsBody />);
    const user = userEvent.setup();

    vi.mocked(askConfirm).mockResolvedValueOnce(null);
    await user.click(screen.getByRole("button", { name: "Unpair this phone" }));
    expect(askedSheet().title).toBe("Unpair this phone?");
    expect(unpair).not.toHaveBeenCalled();

    vi.mocked(askConfirm).mockResolvedValueOnce("");
    await user.click(screen.getByRole("button", { name: "Unpair this phone" }));
    await vi.waitFor(() => expect(unpair).toHaveBeenCalledTimes(1));
  });

  it("is off while the snapshot is stale, except the way back to the classic app", () => {
    live();
    useFleet.setState({ stale: true, link: "offline" });
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Unpair this phone" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Back to classic" })).toBeEnabled();
  });
});

describe("SettingsBody: Recent notices", () => {
  const notice = (text: string, at: string, error = false): Notice => ({ text, error, at });

  it("lists the newest first, each with its age, an error in red", () => {
    live({
      notices: [
        notice("Merged flow-run into main.", ago(12 * 60_000)),
        notice("Could not push fix-pane-cursor.", ago(3 * 60_000), true),
      ],
    });
    render(<SettingsBody />);
    const items = within(section("Recent notices")).getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("3m");
    expect(items[0]).toHaveTextContent("Could not push fix-pane-cursor.");
    expect(items[0]).toHaveClass("text-error");
    expect(items[1]).toHaveTextContent("12m");
    expect(items[1]).toHaveTextContent("Merged flow-run into main.");
    expect(items[1]).not.toHaveClass("text-error");
  });

  it("says None. when there are no notices, and for notices that are null", () => {
    live({ notices: [] });
    const view = render(<SettingsBody />);
    expect(within(section("Recent notices")).getByText("None.")).toBeInTheDocument();
    view.unmount();

    live({ notices: null });
    render(<SettingsBody />);
    expect(within(section("Recent notices")).getByText("None.")).toBeInTheDocument();
  });
});
```

Fails without: "the settings panel is registered with its head and its title": the `definePanel` call removed, or its `Head` left out.

Fails without: "the settings panel heads the screen with Settings and a way back to the fleet": the title text or the `back` link of `SettingsHead` changed.

Fails without: "SettingsBody: Loading… says Loading… until the snapshot holds a device": the `device === undefined` early return removed (the body reads a field of undefined and throws).

Fails without: "SettingsBody: Loading… says Loading… for a saved snapshot that holds no device": the early return guarding only a null snapshot (the body throws on `device.name`).

Fails without: "SettingsBody: This phone and App names the device, the host and the build": a row dropped, or `host` or `build` read from the wrong field.

Fails without: "SettingsBody: This phone and App asks before it unpairs, and unpairs only on a yes": `device.unpair` drawn without its sheet (the first click deletes), or the Unpair button not a `CommandButton` of `device.unpair`.

Fails without: "SettingsBody: This phone and App is off while the snapshot is stale, except the way back to the classic app": the Unpair button drawn outside the command path (not disabled), or the way back not the offline `app.backToClassic`.

Fails without: "SettingsBody: Recent notices lists the newest first, each with its age, an error in red": the list not reversed, the age dropped, or the `text-error` class not tied to `notice.error`.

Fails without: "SettingsBody: Recent notices says None. when there are no notices, and for notices that are null": the empty branch removed, or `notices` not defaulted when null.

At the end of `internal/web/static_budget_test.go`, append:

```go
func TestCheckLazyViews_TheSettingsChunkIsRequiredAndKeptOutOfTheEntry(t *testing.T) {
	assets := nextStaticAssets(t)
	var chunk string
	for url := range assets {
		if strings.HasPrefix(url, "/assets/SettingsBody-") && strings.HasSuffix(url, ".js") {
			chunk = url
		}
	}
	require.NotEmpty(t, chunk, "the bundle has no SettingsBody chunk: the settings body is imported statically, or the bundle was not rebuilt")
	require.NoError(t, checkLazyViews(assets))

	without := map[string]staticAsset{}
	named := map[string]staticAsset{}
	for url, asset := range assets {
		named[url] = asset
		if url != chunk {
			without[url] = asset
		}
	}
	require.ErrorContains(t, checkLazyViews(without), "SettingsBody")

	index := assets["/index.html"]
	named["/index.html"] = staticAsset{
		body:  append(append([]byte{}, index.body...), []byte(`<link rel="modulepreload" href="`+chunk+`">`)...),
		ctype: index.ctype,
	}
	require.ErrorContains(t, checkLazyViews(named), chunk)
}
```

Fails without: `TestCheckLazyViews_TheSettingsChunkIsRequiredAndKeptOutOfTheEntry`: the settings body imported statically (no `SettingsBody-<hash>.js` chunk, so the `NotEmpty` fails), `"SettingsBody"` missing from `checkLazyViews`' list (the chunk-less bundle passes), or the check that index.html names no lazy chunk removed (the preload is accepted).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/settings/settings.test.tsx`
Expected: the file fails to load: `Failed to resolve import "./index" from "src/panels/settings/settings.test.tsx"`.

Run: `CGO_ENABLED=0 go test ./internal/web/ -run TestCheckLazyViews_TheSettingsChunk`
Expected: FAIL with `the bundle has no SettingsBody chunk: the settings body is imported statically, or the bundle was not rebuilt`.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/panels/settings/index.tsx`:

```tsx
// The settings panel: the head names the screen and goes back to the fleet; the body is a lazy
// chunk, so a first launch never loads it (spec Part 3 settlement 6).
import { lazy, type ReactElement } from "react";

import { definePanel } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { ContextRow } from "@/app/shell";

// SettingsHead is the settings screen's context row: its title and the way back to the fleet.
export function SettingsHead(): ReactElement {
  return <ContextRow back={hrefFor("fleet")} title="Settings" />;
}

const SettingsBody = lazy(() => import("./SettingsBody"));

export const settingsPanel = definePanel({
  id: "settings",
  title: () => "Settings",
  Head: SettingsHead,
  Body: SettingsBody,
});
```

Create `internal/web/ui/src/panels/settings/SettingsBody.tsx`:

```tsx
// The settings body (a chunk of its own): this phone, the way back to the classic app and the
// recent notices. A port of the classic settings.mjs (spec Part 3 settlement 9).
import type { ReactElement, ReactNode } from "react";

import { CommandButton } from "@/app/CommandButton";
import { cn } from "@/lib/utils";
import { ago } from "@/panels/fleet/SessionRow";
import { useFleet } from "@/store/fleet";

const NOTE = "text-sm text-muted-foreground";

// Section is one titled group of the screen.
function Section({ title, children }: { title: string; children: ReactNode }): ReactElement {
  return (
    <section className="flex flex-col gap-3 border-b border-border px-4 py-4">
      <h2 className="text-sm font-semibold text-muted-foreground">{title}</h2>
      {children}
    </section>
  );
}

// Row is a label with its value or control on the right, at the 44px touch height.
function Row({ label, children }: { label: string; children: ReactNode }): ReactElement {
  return (
    <div className="flex min-h-11 items-center justify-between gap-3">
      <span>{label}</span>
      {children}
    </div>
  );
}

export default function SettingsBody(): ReactElement {
  const device = useFleet((s) => s.snapshot?.device);
  const host = useFleet((s) => s.snapshot?.host ?? "");
  const build = useFleet((s) => s.snapshot?.build ?? "");
  const notices = useFleet((s) => s.snapshot?.notices ?? null);

  if (device === undefined) {
    return <p className="px-4 py-6 text-muted-foreground">Loading…</p>;
  }
  // Newest first: the snapshot keeps them oldest first and drops the oldest at its cap, so an
  // index shifts; a notice's key is its time and text, and a repeat of both counts up.
  const seen = new Map<string, number>();
  const recent = (notices ?? [])
    .map((notice) => {
      const base = `${notice.at}|${notice.text}`;
      const repeat = seen.get(base) ?? 0;
      seen.set(base, repeat + 1);
      return { notice, key: `${base}|${repeat}` };
    })
    .reverse();

  return (
    <div className="flex flex-col pb-8">
      <Section title="This phone">
        <Row label="Paired as">
          <span className="min-w-0 truncate">{device.name}</span>
        </Row>
        <Row label="Host">
          <span className="min-w-0 truncate">{host}</span>
        </Row>
        <Row label="Build">
          <span className="min-w-0 truncate font-mono text-sm">{build}</span>
        </Row>
        <div className="flex">
          <CommandButton id="device.unpair" variant="destructive" />
        </div>
      </Section>

      <Section title="App">
        <div className="flex">
          <CommandButton id="app.backToClassic" variant="secondary" />
        </div>
      </Section>

      <Section title="Recent notices">
        {recent.length === 0 ? (
          <p className={NOTE}>None.</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {recent.map(({ notice, key }) => (
              <li key={key} className={cn("text-sm", notice.error && "text-error")}>
                <span className="mr-2 text-faint">{ago(notice.at)}</span>
                {notice.text}
              </li>
            ))}
          </ul>
        )}
      </Section>
    </div>
  );
}
```

In `internal/web/ui/src/panels/index.ts`, add one line after the file's last import line (whatever panel imports the earlier stories left there; add nothing else):

```ts
import "./settings";
```

In `internal/web/static_budget_test.go`:

- `checkLazyViews`' `for _, name := range []string{…} {` line: add `"SettingsBody"` as the last element of the slice literal, directly before its closing `}`, keeping whatever shape (one line or one name a line) the earlier stories left it in.
- `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`: add `"/assets/SettingsBody-s.js"` as the last element of its `all := []string{…}` literal, directly before the closing `}`.

- `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`'s table: append two cases as the last elements of the `[]struct{…}{…}` literal, directly before the `} {` line that opens the loop body, after whatever cases the earlier stories left (`strings` and `slices` are imported by the file already). The other cases stay as they are, and each lazy stem keeps one missing and one preloaded case:

```go
		{"no settings chunk", build(entry, slices.DeleteFunc(slices.Clone(all), func(f string) bool { return strings.HasPrefix(f, "/assets/SettingsBody-") })...), "SettingsBody"},
		{"a preloaded settings chunk", build(entry+`<link rel="modulepreload" crossorigin href="/assets/SettingsBody-s.js">`, all...), "/assets/SettingsBody-s.js"},
```

Fails without: `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne/no_settings_chunk` and `…/a_preloaded_settings_chunk`: `"SettingsBody"` removed from `checkLazyViews`' list (the first case finds no error, the second's `want` is not found), or the page check dropped (the second).

Format and rebuild the bundle:

```bash
pnpm -C internal/web/ui exec prettier --write src/panels/settings/index.tsx src/panels/settings/SettingsBody.tsx src/panels/settings/settings.test.tsx
gofmt -w internal/web/static_budget_test.go
pnpm -C internal/web/ui run build
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/settings/settings.test.tsx`
Expected: the file passes.

Run: `CGO_ENABLED=0 go test ./internal/web/`
Expected: `ok`, with `TestDist_LazyViewsAreChunksOfTheirOwn`, `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne` and the new test passing, and `TestDist_FirstLoadIsWithinBudget` too (the body is a chunk).

Run: `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check'`
Expected: every check passes and `dist:check` says the bundle is fresh.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/settings/index.tsx internal/web/ui/src/panels/settings/SettingsBody.tsx internal/web/ui/src/panels/settings/settings.test.tsx internal/web/dist
git commit -m "feat(web): the settings panel with This phone, App and Recent notices" -- internal/web/ui/src/panels/settings/index.tsx internal/web/ui/src/panels/settings/SettingsBody.tsx internal/web/ui/src/panels/settings/settings.test.tsx internal/web/ui/src/panels/index.ts internal/web/static_budget_test.go internal/web/dist
```

### Task 10.2: Notifications

**Files:**
- Modify: `internal/web/ui/src/panels/settings/SettingsBody.tsx`
- Modify: `internal/web/ui/src/panels/settings/settings.test.tsx`
- Modify: `internal/web/dist/`

The push control reads `usePushStatus(device)` (Story 5) and draws `CommandButton`s of `push.enable`: while the command is in flight (`useCommandBusy`, which is the module-level in-flight set, so a visit elsewhere and back finds it still waiting) the button reads "Turning on…" with the note "The phone can take a minute or two.". The two Switches run `device.update` through `runCommand` with the one field they set; a Switch shows the value asked for from the tap until `runCommand` settles, then the stored value, and is off while its write is in flight or the command is off (stale). The pause row is "Paused until <h:mm>" with Resume now, or "Not paused" with Pause for an hour, both `CommandButton`s of `device.update`; a timer redraws it when the pause ends.

- [ ] **Step 1: Write the failing tests**

In `internal/web/ui/src/panels/settings/settings.test.tsx`, replace the import block and the `vi.mock` call:

```tsx
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/commands";
import type { Device, Notice, Outcome, Snapshot } from "@/api/gen";
import { api } from "@/api/request";
import { askConfirm } from "@/app/ConfirmSheet";
import { panelFor } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { askedSheet, resetFleet } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { SettingsHead, settingsPanel } from "./index";
import SettingsBody from "./SettingsBody";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));
```

with:

```tsx
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/commands";
import type { Device, Notice, Outcome, Snapshot } from "@/api/gen";
import { api } from "@/api/request";
import { askConfirm } from "@/app/ConfirmSheet";
import { panelFor } from "@/app/panels";
import { activeRegistration, pushSupported, usePushStatus } from "@/app/push";
import { hrefFor } from "@/app/routes";
import { askedSheet, deferred, resetFleet, spyOnAdd } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { SettingsHead, settingsPanel } from "./index";
import SettingsBody from "./SettingsBody";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));
// The phone's own push state and the browser calls the command makes are the test's to answer;
// the command itself stays real.
vi.mock("@/app/push", async (original) => ({
  ...(await original<typeof import("@/app/push")>()),
  usePushStatus: vi.fn(),
  pushSupported: vi.fn(),
  activeRegistration: vi.fn(),
}));
```

Replace the file's `beforeEach` and `afterEach`:

```tsx
beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  window.history.replaceState(null, "", "#/settings");
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  window.history.replaceState(null, "", window.location.pathname);
});
```

with:

```tsx
let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  window.history.replaceState(null, "", "#/settings");
  add = spyOnAdd();
  vi.mocked(usePushStatus).mockReturnValue("off");
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  window.history.replaceState(null, "", window.location.pathname);
});
```

Append at the end of the file:

```tsx
// pausedUntil is a device whose pause ends this long from now.
const pausedFor = (ms: number): Device => ({
  ...PHONE,
  notify: { ...PHONE.notify, paused_until: new Date(NOW + ms).toISOString() },
});

const aSwitch = (name: string): HTMLElement => screen.getByRole("switch", { name });

describe("SettingsBody: the push control", () => {
  it.each([
    [
      "unsupported",
      "Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.",
    ],
    [
      "denied",
      "Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.",
    ],
    ["on", "Notifications are on for this phone."],
  ] as const)("says %s in the classic words and offers no button to turn it on", (status, words) => {
    vi.mocked(usePushStatus).mockReturnValue(status);
    live();
    render(<SettingsBody />);
    expect(within(section("Notifications")).getByText(words)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /notifications/i })).toBeNull();
  });

  it("draws nothing for the push control while the phone's state loads", () => {
    vi.mocked(usePushStatus).mockReturnValue("loading");
    live();
    render(<SettingsBody />);
    expect(screen.queryByRole("button", { name: /notifications/i })).toBeNull();
    expect(screen.queryByText(/Home Screen|blocked for loom|are on for this phone/)).toBeNull();
  });

  it("offers Turn on notifications when it is off and Turn notifications on again when it lapsed", () => {
    vi.mocked(usePushStatus).mockReturnValue("off");
    live();
    const view = render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Turn on notifications" })).toBeEnabled();
    expect(usePushStatus).toHaveBeenCalledWith(PHONE);
    view.unmount();

    vi.mocked(usePushStatus).mockReturnValue("again");
    live({ device: { ...PHONE, push: "expired" } });
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Turn notifications on again" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Turn on notifications" })).toBeNull();
  });

  it("is off while the snapshot is stale", () => {
    live();
    useFleet.setState({ stale: true, link: "offline" });
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Turn on notifications" })).toBeDisabled();
  });

  it("reads Turning on… with its note while push.enable runs, and still does after a visit elsewhere", async () => {
    const registration = deferred<ServiceWorkerRegistration | null>();
    vi.mocked(pushSupported).mockReturnValue(true);
    vi.mocked(activeRegistration).mockReturnValue(registration.promise);
    vi.stubGlobal("Notification", { requestPermission: vi.fn().mockResolvedValue("granted") });
    live();
    const first = render(<SettingsBody />);

    await userEvent.setup().click(screen.getByRole("button", { name: "Turn on notifications" }));
    expect(await screen.findByRole("button", { name: "Turning on…" })).toBeDisabled();
    expect(screen.getByText("The phone can take a minute or two.")).toBeInTheDocument();

    first.unmount();
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Turning on…" })).toBeDisabled();

    await act(async () => {
      registration.resolve(null);
    });
    expect(await screen.findByRole("button", { name: "Turn on notifications" })).toBeEnabled();
    expect(screen.queryByText("The phone can take a minute or two.")).toBeNull();
    expect(add).toHaveBeenCalledWith(expect.objectContaining({ type: "error" }));
  });
});

describe("SettingsBody: the expired or failing line", () => {
  it.each([
    ["expired", "Notifications stopped; turn them on again."],
    ["failing: 410 Gone", "Notifications are failing (410 Gone); turn them on again."],
  ])("says %s in red", (push, words) => {
    live({ device: { ...PHONE, push } });
    render(<SettingsBody />);
    expect(screen.getByText(words)).toHaveClass("text-error");
  });

  it.each(["", "ok"])("says nothing for a push state of %j", (push) => {
    live({ device: { ...PHONE, push } });
    render(<SettingsBody />);
    expect(screen.queryByText(/Notifications stopped|Notifications are failing/)).toBeNull();
  });
});

describe("SettingsBody: the notification Switches", () => {
  it("shows the stored values", () => {
    live({ device: { ...PHONE, notify: { ...PHONE.notify, waiting: true, finished: false } } });
    render(<SettingsBody />);
    expect(aSwitch("When a session needs you")).toHaveAttribute("aria-checked", "true");
    expect(aSwitch("When a session finishes")).toHaveAttribute("aria-checked", "false");
  });

  it("shows the value asked for while the write is in flight, holds a second tap, then shows the stored one", async () => {
    const write = deferred<Outcome<Device>>();
    const put = vi.spyOn(api, "putDevice").mockReturnValue(write.promise);
    live();
    render(<SettingsBody />);
    const user = userEvent.setup();
    const waiting = aSwitch("When a session needs you");

    await user.click(waiting);
    expect(put).toHaveBeenCalledTimes(1);
    expect(put).toHaveBeenCalledWith({ waiting: false });
    // Asked for, while the store still holds true.
    expect(waiting).toHaveAttribute("aria-checked", "false");
    expect(useFleet.getState().snapshot?.device.notify.waiting).toBe(true);

    // Off while its write runs, so the second tap reaches nothing and the asked value stays.
    expect(waiting).toHaveAttribute("data-disabled");
    await user.click(waiting);
    expect(put).toHaveBeenCalledTimes(1);
    expect(waiting).toHaveAttribute("aria-checked", "false");

    await act(async () => {
      write.resolve(ok({ ...PHONE, notify: { ...PHONE.notify, waiting: false } }));
    });
    expect(useFleet.getState().snapshot?.device.notify.waiting).toBe(false);
    expect(waiting).toHaveAttribute("aria-checked", "false");
  });

  it("writes only the field it is for", async () => {
    const put = vi
      .spyOn(api, "putDevice")
      .mockResolvedValue(ok({ ...PHONE, notify: { ...PHONE.notify, finished: true } }));
    live();
    render(<SettingsBody />);

    await userEvent.setup().click(aSwitch("When a session finishes"));
    await waitFor(() => expect(put).toHaveBeenCalledWith({ finished: true }));
    expect(put).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(aSwitch("When a session finishes")).toHaveAttribute("aria-checked", "true"),
    );
  });

  it("snaps back to the stored value, and toasts the daemon's words, when the write is refused", async () => {
    vi.spyOn(api, "putDevice").mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "the device was unpaired",
    });
    live();
    render(<SettingsBody />);

    await userEvent.setup().click(aSwitch("When a session finishes"));
    await waitFor(() =>
      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ description: "the device was unpaired", type: "error" }),
      ),
    );
    await waitFor(() =>
      expect(aSwitch("When a session finishes")).toHaveAttribute("aria-checked", "false"),
    );
    expect(useFleet.getState().snapshot?.device.notify.finished).toBe(false);
  });

  it("shows what the daemon stored when it answers otherwise than asked", async () => {
    vi.spyOn(api, "putDevice").mockResolvedValue(
      ok({ ...PHONE, notify: { ...PHONE.notify, waiting: true } }),
    );
    live();
    render(<SettingsBody />);

    await userEvent.setup().click(aSwitch("When a session needs you"));
    await waitFor(() =>
      expect(aSwitch("When a session needs you")).toHaveAttribute("aria-checked", "true"),
    );
  });

  it("is off while the snapshot is stale", async () => {
    const put = vi.spyOn(api, "putDevice");
    live();
    useFleet.setState({ stale: true, link: "offline" });
    render(<SettingsBody />);

    // Story 2's Switch is a span, so a disabled one shows `data-disabled`, not the `disabled` property.
    expect(aSwitch("When a session needs you")).toHaveAttribute("data-disabled");
    expect(aSwitch("When a session finishes")).toHaveAttribute("data-disabled");

    await userEvent.setup().click(aSwitch("When a session needs you"));
    expect(put).not.toHaveBeenCalled();
    expect(aSwitch("When a session needs you")).toHaveAttribute("aria-checked", "true");
  });
});

describe("SettingsBody: the pause row", () => {
  it("offers Pause for an hour when not paused", async () => {
    const put = vi.spyOn(api, "putDevice").mockResolvedValue(ok(pausedFor(3_600_000)));
    live();
    render(<SettingsBody />);
    expect(screen.getByText("Not paused")).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("button", { name: "Pause for an hour" }));
    expect(put).toHaveBeenCalledWith({ pause: "1h" });
    expect(await screen.findByRole("button", { name: "Resume now" })).toBeInTheDocument();
    expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();
  });

  it("says when the pause ends and offers Resume now", async () => {
    const put = vi.spyOn(api, "putDevice").mockResolvedValue(ok(PHONE));
    live({ device: pausedFor(40 * 60_000) });
    render(<SettingsBody />);
    expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();
    expect(screen.queryByText("Not paused")).toBeNull();

    await userEvent.setup().click(screen.getByRole("button", { name: "Resume now" }));
    expect(put).toHaveBeenCalledWith({ pause: "none" });
    expect(await screen.findByText("Not paused")).toBeInTheDocument();
  });

  it("draws the row again when the pause ends", () => {
    vi.useRealTimers();
    vi.useFakeTimers({ now: NOW });
    live({ device: pausedFor(60_000) });
    render(<SettingsBody />);
    expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(59_000);
    });
    expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(screen.getByText("Not paused")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pause for an hour" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Resume now" })).toBeNull();
  });

  it("says Not paused for a pause that ended before the screen opened", () => {
    live({ device: pausedFor(-60_000) });
    render(<SettingsBody />);
    expect(screen.getByText("Not paused")).toBeInTheDocument();
  });
});
```

Fails without: "SettingsBody: the push control says %s in the classic words and offers no button to turn it on": the words of that state changed or its branch missing (the "off" button shows), or `usePushStatus` not read.

Fails without: "SettingsBody: the push control draws nothing for the push control while the phone's state loads": the `loading` case drawing a button or words.

Fails without: "SettingsBody: the push control offers Turn on notifications when it is off and Turn notifications on again when it lapsed": the `again` label not passed to the `CommandButton` (both read "Turn on notifications"), or `usePushStatus` called with something other than the snapshot's device.

Fails without: "SettingsBody: the push control is off while the snapshot is stale": the control drawn as a plain button rather than a `CommandButton` of `push.enable`.

Fails without: "SettingsBody: the push control reads Turning on… with its note while push.enable runs, and still does after a visit elsewhere": the busy state read from component state instead of `useCommandBusy` (the remounted screen shows the idle button), or the note dropped.

Fails without: "SettingsBody: the expired or failing line says %s in red": `pushLine`'s words changed, the status text not taken from after `failing:`, or the `text-error` class dropped.

Fails without: "SettingsBody: the expired or failing line says nothing for a push state of %j": the line drawn for every state.

Fails without: "SettingsBody: the notification Switches shows the stored values": a Switch's `checked` read from the wrong field.

Fails without: "SettingsBody: the notification Switches shows the value asked for while the write is in flight, holds a second tap, then shows the stored one": the asked value not set (the Switch reads true while in flight), or `busy` dropped from the Switch's `disabled` (the Switch stays enabled while its write runs, so the second tap asks for `true`, `runCommand` refuses the held key, and its `.finally` restores the stored `true` mid-write: the asked value is lost and `aria-checked` reads "true" after the second tap).

Fails without: "SettingsBody: the notification Switches writes only the field it is for": the body carrying both fields, or the wrong field name.

Fails without: "SettingsBody: the notification Switches snaps back to the stored value, and toasts the daemon's words, when the write is refused": the asked value left in place after `runCommand` settles (cleared only on success).

Fails without: "SettingsBody: the notification Switches shows what the daemon stored when it answers otherwise than asked": the asked value kept after the write ends, instead of dropping to the stored one.

Fails without: "SettingsBody: the notification Switches is off while the snapshot is stale": `!commandEnabled(update, ctx)` dropped from the Switch's `disabled` (no `data-disabled` while stale).

Fails without: "SettingsBody: the pause row offers Pause for an hour when not paused": the button's args other than `{ pause: "1h" }`, or the row not drawn from the stored value.

Fails without: "SettingsBody: the pause row says when the pause ends and offers Resume now": the args other than `{ pause: "none" }`, or the paused branch missing.

Fails without: "SettingsBody: the pause row draws the row again when the pause ends": the expiry timer removed (the row keeps "Paused until" past the end, as classic does).

Fails without: "SettingsBody: the pause row says Not paused for a pause that ended before the screen opened": the paused test reading only that `paused_until` parses, not that it lies in the future.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/settings/settings.test.tsx`
Expected: the file loads and the new tests FAIL (`Unable to find role="switch"`, `Unable to find an element with the text: Notifications stopped; turn them on again.`, and the like); Task 10.1's tests still pass.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/panels/settings/SettingsBody.tsx`, replace the import block:

```tsx
import type { ReactElement, ReactNode } from "react";

import { CommandButton } from "@/app/CommandButton";
import { cn } from "@/lib/utils";
import { ago } from "@/panels/fleet/SessionRow";
import { useFleet } from "@/store/fleet";
```

with:

```tsx
import { useEffect, useReducer, useState, type ReactElement, type ReactNode } from "react";

import type { Device } from "@/api/gen";
import { CommandButton } from "@/app/CommandButton";
import {
  commandById,
  commandEnabled,
  runCommand,
  useCommandBusy,
  useCommandContext,
  type Command,
} from "@/app/commands";
import { usePushStatus } from "@/app/push";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import { ago } from "@/panels/fleet/SessionRow";
import { useFleet } from "@/store/fleet";
```

Insert the following above the line `export default function SettingsBody(): ReactElement {`:

```tsx
// The classic app's words for the push control's states that draw no button.
const PUSH_WORDS = {
  unsupported:
    "Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.",
  denied:
    "Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.",
  on: "Notifications are on for this phone.",
} as const;

// MAX_DELAY is the longest delay setTimeout takes (2^31 - 1 ms); a longer wait is re-armed.
const MAX_DELAY = 2_147_483_647;

// commandOf is the registered command with this id; the commands module registers it, so a miss
// is a bug and fails loudly, as CommandButton does.
function commandOf(id: string): Command<unknown> {
  const command = commandById(id);
  if (command === undefined) throw new Error(`loom: unknown command ${id}`);
  return command;
}

// now reads this phone's clock; kept apart from the components, which the purity lint holds to
// render the same for the same props.
function now(): number {
  return Date.now();
}

// pushLine is what the card says when the push subscription stopped working, and "" while it
// works or was never made (device.push is "", "ok", "expired" or "failing: <status>").
function pushLine(push: string): string {
  if (push === "expired") return "Notifications stopped; turn them on again.";
  if (push.startsWith("failing")) {
    return `Notifications are failing (${push.replace(/^failing:\s*/, "")}); turn them on again.`;
  }
  return "";
}

// clock words a time as the phone's own hours and minutes.
function clock(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

// PushControl is the push control: its state from the phone and the device, and the turn-on's
// buttons. "Turning on…" is the command's in-flight state, which is module-level, so a visit
// elsewhere and back finds it still waiting and takes no second tap.
function PushControl({ device }: { device: Device }): ReactElement | null {
  const ctx = useCommandContext();
  const status = usePushStatus(device);
  const turning = useCommandBusy(commandOf("push.enable"), ctx, undefined);

  if (turning) {
    // subscribe() can take minutes: after an unsubscribe iOS took over two.
    return (
      <div className="flex flex-col items-start gap-1">
        <CommandButton id="push.enable" label="Turning on…" />
        <p className={NOTE}>The phone can take a minute or two.</p>
      </div>
    );
  }
  switch (status) {
    case "loading":
      return null;
    case "unsupported":
    case "denied":
    case "on":
      return <p className={NOTE}>{PUSH_WORDS[status]}</p>;
    case "again":
      return (
        <div className="flex">
          <CommandButton id="push.enable" label="Turn notifications on again" />
        </div>
      );
    case "off":
      return (
        <div className="flex">
          <CommandButton id="push.enable" />
        </div>
      );
  }
}

// NotifySwitch is one of the device's notification toggles. A Switch is not a button, so it runs
// device.update through runCommand with the one field it sets. It shows the value asked for from
// the tap until the write settles, then the stored one, so a refusal snaps it back; it is off
// while its write is in flight and while the command is off (a stale snapshot).
function NotifySwitch({
  field,
  label,
  checked,
}: {
  field: "waiting" | "finished";
  label: string;
  checked: boolean;
}): ReactElement {
  const ctx = useCommandContext();
  const update = commandOf("device.update");
  const [asked, setAsked] = useState<boolean | null>(null);
  const busy = useCommandBusy(update, ctx, { [field]: checked });

  const change = (next: boolean): void => {
    setAsked(next);
    void runCommand(update, ctx, { [field]: next }).finally(() => setAsked(null));
  };

  return (
    <label className="flex min-h-11 items-center justify-between gap-3">
      <span>{label}</span>
      <Switch
        aria-label={label}
        checked={asked ?? checked}
        disabled={busy || !commandEnabled(update, ctx)}
        onCheckedChange={change}
      />
    </label>
  );
}

// usePaused reports whether the pause that ends at `iso` is still running, and draws again when it
// ends: classic keeps "Paused until" past the end.
function usePaused(iso: string): boolean {
  const until = Date.parse(iso);
  const [ticks, redraw] = useReducer((n: number) => n + 1, 0);
  const paused = !Number.isNaN(until) && until > now();

  useEffect(() => {
    if (!paused) return undefined;
    // A wait past MAX_DELAY ends early and is armed again by the redraw it causes.
    const timer = setTimeout(redraw, Math.min(Math.max(until - now(), 0), MAX_DELAY));
    return () => clearTimeout(timer);
  }, [paused, until, ticks]);

  return paused;
}

// PauseRow is "Paused until <time>" with Resume now, or "Not paused" with Pause for an hour.
function PauseRow({ pausedUntil }: { pausedUntil: string }): ReactElement {
  const paused = usePaused(pausedUntil);
  return (
    <Row label={paused ? `Paused until ${clock(pausedUntil)}` : "Not paused"}>
      {paused ? (
        <CommandButton
          id="device.update"
          args={{ pause: "none" }}
          label="Resume now"
          variant="secondary"
        />
      ) : (
        <CommandButton
          id="device.update"
          args={{ pause: "1h" }}
          label="Pause for an hour"
          variant="secondary"
        />
      )}
    </Row>
  );
}

// Notifications is the push control, its failing line, the two toggles and the pause row.
function Notifications({ device }: { device: Device }): ReactElement {
  const line = pushLine(device.push);
  return (
    <Section title="Notifications">
      <PushControl device={device} />
      {line === "" ? null : <p className="text-sm text-error">{line}</p>}
      <NotifySwitch
        field="waiting"
        label="When a session needs you"
        checked={device.notify.waiting}
      />
      <NotifySwitch
        field="finished"
        label="When a session finishes"
        checked={device.notify.finished}
      />
      <PauseRow pausedUntil={device.notify.paused_until} />
    </Section>
  );
}

```

In `SettingsBody`'s JSX, replace:

```tsx
    <div className="flex flex-col pb-8">
      <Section title="This phone">
```

with:

```tsx
    <div className="flex flex-col pb-8">
      <Notifications device={device} />
      <Section title="This phone">
```

Format and rebuild the bundle:

```bash
pnpm -C internal/web/ui exec prettier --write src/panels/settings/SettingsBody.tsx src/panels/settings/settings.test.tsx
pnpm -C internal/web/ui run build
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/settings/settings.test.tsx`
Expected: the file passes.

Run: `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
Expected: every check passes, `dist:check` says the bundle is fresh and `go test` says `ok`. If `format:check` fails, run `pnpm -C internal/web/ui exec prettier --write <the files it names>` and run it again.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "feat(web): settings' notifications, the push control, the two toggles and the pause row" -- internal/web/ui/src/panels/settings/SettingsBody.tsx internal/web/ui/src/panels/settings/settings.test.tsx internal/web/dist
```

### Task 10.3: Quick replies, and the settings screen in webshot

**Files:**
- Modify: `internal/web/ui/src/panels/settings/SettingsBody.tsx`
- Modify: `internal/web/ui/src/panels/settings/settings.test.tsx`
- Modify: `internal/web/dist/`

The textarea shows the stored list until the person types, then the draft; Save sends the draft's lines (each trimmed, blanks dropped, no client check of the count or length: the daemon's 400 is the toast), Reset to defaults sends `null`. Both are `CommandButton`s of `quickReplies.save` with an `onSaved` that clears the draft, which the command calls only after a 200, so a refusal leaves the typed text and Save enabled. Save is off until the person has typed. The textarea and both buttons are off while a save is in flight (the command has no `key`, so Reset and Save share one in-flight state) and while the snapshot is stale.

- [ ] **Step 1: Write the failing tests**

In `internal/web/ui/src/panels/settings/settings.test.tsx`, replace the import line:

```tsx
import { act, render, screen, waitFor, within } from "@testing-library/react";
```

with:

```tsx
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
```

and the import line:

```tsx
import type { Device, Notice, Outcome, Snapshot } from "@/api/gen";
```

with:

```tsx
import type { Device, Notice, Outcome, QuickReplies, Snapshot } from "@/api/gen";
```

Append at the end of the file:

```tsx
const repliesBox = (): HTMLTextAreaElement =>
  screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Quick replies" });

describe("SettingsBody: Quick replies", () => {
  it("shows the stored list, one a line, with the classic note", () => {
    live({ quick_replies: ["ok", "later"] });
    render(<SettingsBody />);
    expect(repliesBox()).toHaveValue("ok\nlater");
    expect(
      screen.getByText(
        "One a line, at most 12. Every paired phone offers them above its reply box.",
      ),
    ).toBeInTheDocument();
  });

  it("shows an empty box for no stored list", () => {
    live({ quick_replies: null });
    render(<SettingsBody />);
    expect(repliesBox()).toHaveValue("");
  });

  it("follows the stored list until typed in, then keeps the draft over a new stored list", () => {
    live({ quick_replies: ["ok", "later"] });
    render(<SettingsBody />);

    act(() => useFleet.getState().apply("quick_replies", { quick_replies: ["x", "y"] }));
    expect(repliesBox()).toHaveValue("x\ny");

    fireEvent.change(repliesBox(), { target: { value: "mine" } });
    expect(repliesBox()).toHaveValue("mine");

    act(() => useFleet.getState().apply("quick_replies", { quick_replies: ["z"] }));
    expect(repliesBox()).toHaveValue("mine");
  });

  it("leaves Save off until the person has typed, and on after", () => {
    live();
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reset to defaults" })).toBeEnabled();

    fireEvent.change(repliesBox(), { target: { value: "ok" } });
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("saves the lines trimmed with the blanks dropped, then shows the stored list again", async () => {
    const put = vi
      .spyOn(api, "putQuickReplies")
      .mockResolvedValue(ok<QuickReplies>({ quick_replies: ["ok", "later"] }));
    live({ quick_replies: ["old"] });
    render(<SettingsBody />);

    fireEvent.change(repliesBox(), { target: { value: "  ok  \n\n later\n   \n" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Save" }));

    expect(put).toHaveBeenCalledTimes(1);
    expect(put).toHaveBeenCalledWith({ replies: ["ok", "later"] });
    await waitFor(() => expect(repliesBox()).toHaveValue("ok\nlater"));
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("resets to the defaults the daemon answers, sending no list", async () => {
    const put = vi
      .spyOn(api, "putQuickReplies")
      .mockResolvedValue(ok<QuickReplies>({ quick_replies: ["yes", "continue"] }));
    live({ quick_replies: ["old"] });
    render(<SettingsBody />);

    fireEvent.change(repliesBox(), { target: { value: "typed" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Reset to defaults" }));

    expect(put).toHaveBeenCalledTimes(1);
    expect(put.mock.calls[0]?.[0]).toStrictEqual({});
    await waitFor(() => expect(repliesBox()).toHaveValue("yes\ncontinue"));
  });

  it("keeps the typed text, and Save on, when the daemon refuses the list", async () => {
    vi.spyOn(api, "putQuickReplies").mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "at most 12 quick replies",
    });
    live({ quick_replies: ["old"] });
    render(<SettingsBody />);

    fireEvent.change(repliesBox(), { target: { value: "one\ntwo" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ description: "at most 12 quick replies", type: "error" }),
      ),
    );
    await waitFor(() => expect(repliesBox()).toBeEnabled());
    expect(repliesBox()).toHaveValue("one\ntwo");
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("holds the box and both buttons while a save is in flight", async () => {
    const write = deferred<Outcome<QuickReplies>>();
    const put = vi.spyOn(api, "putQuickReplies").mockReturnValue(write.promise);
    live();
    render(<SettingsBody />);
    const user = userEvent.setup();

    fireEvent.change(repliesBox(), { target: { value: "one" } });
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(repliesBox()).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reset to defaults" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Reset to defaults" }));
    expect(put).toHaveBeenCalledTimes(1);

    await act(async () => {
      write.resolve(ok<QuickReplies>({ quick_replies: ["one"] }));
    });
    expect(repliesBox()).toBeEnabled();
  });

  it("is off while the snapshot is stale", () => {
    live();
    useFleet.setState({ stale: true, link: "offline" });
    render(<SettingsBody />);
    expect(repliesBox()).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reset to defaults" })).toBeDisabled();
  });
});
```

Fails without: "SettingsBody: Quick replies shows the stored list, one a line, with the classic note": the list not joined with newlines, or the note's words changed.

Fails without: "SettingsBody: Quick replies shows an empty box for no stored list": `quick_replies` null not defaulted (the box reads `null`, or the body throws).

Fails without: "SettingsBody: Quick replies follows the stored list until typed in, then keeps the draft over a new stored list": the box showing a copy of the stored list taken once (the first change is missed), or the draft not winning over the stored list.

Fails without: "SettingsBody: Quick replies leaves Save off until the person has typed, and on after": Save's `disabled` not tied to the draft being null.

Fails without: "SettingsBody: Quick replies saves the lines trimmed with the blanks dropped, then shows the stored list again": `replyLines` not trimming or not dropping blank lines, or `onSaved` not clearing the draft (the box keeps the untrimmed text).

Fails without: "SettingsBody: Quick replies resets to the defaults the daemon answers, sending no list": Reset sending the draft's lines rather than `replies: null`, or its `onSaved` not clearing the draft (the box keeps "typed").

Fails without: "SettingsBody: Quick replies keeps the typed text, and Save on, when the daemon refuses the list": the draft cleared before the write ends, or in a path a refusal takes.

Fails without: "SettingsBody: Quick replies holds the box and both buttons while a save is in flight": the textarea not disabled by `useCommandBusy`, or Reset and Save not sharing the one in-flight state (the Reset tap sends a second write).

Fails without: "SettingsBody: Quick replies is off while the snapshot is stale": the textarea disabled by `busy` alone, not by `commandEnabled`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/settings/settings.test.tsx`
Expected: the new tests FAIL with `Unable to find an accessible element with the role "textbox" and name "Quick replies"`; the earlier tests still pass.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/panels/settings/SettingsBody.tsx`, replace the import line:

```tsx
import { Switch } from "@/components/ui/switch";
```

with:

```tsx
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
```

Insert the following above the line `export default function SettingsBody(): ReactElement {`:

```tsx
// replyLines is the editor's replies: one a line, each trimmed, blank lines dropped. The daemon
// trims again, drops a duplicate after its first and refuses more than 12.
function replyLines(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

// QuickReplies edits the list every paired phone offers above its reply box. The box shows the
// stored list until the person types, then the draft; the command's onSaved, which it calls only
// after a 200, clears the draft, so a refused list stays in the box for another try.
function QuickReplies({ stored }: { stored: string[] | null }): ReactElement {
  const ctx = useCommandContext();
  const save = commandOf("quickReplies.save");
  const busy = useCommandBusy(save, ctx, { replies: null });
  const [draft, setDraft] = useState<string | null>(null);
  const shown = draft ?? (stored ?? []).join("\n");
  const saved = (): void => setDraft(null);

  return (
    <Section title="Quick replies">
      <p className={NOTE}>
        One a line, at most 12. Every paired phone offers them above its reply box.
      </p>
      <Textarea
        aria-label="Quick replies"
        value={shown}
        disabled={busy || !commandEnabled(save, ctx)}
        onChange={(e) => setDraft(e.target.value)}
      />
      <div className="flex gap-2">
        <CommandButton
          id="quickReplies.save"
          args={{ replies: null, onSaved: saved }}
          label="Reset to defaults"
          variant="secondary"
        />
        <CommandButton
          id="quickReplies.save"
          args={{ replies: replyLines(shown), onSaved: saved }}
          disabled={draft === null}
        />
      </div>
    </Section>
  );
}

```

In `SettingsBody`, after the line `const notices = useFleet((s) => s.snapshot?.notices ?? null);` add:

```tsx
  const replies = useFleet((s) => s.snapshot?.quick_replies ?? null);
```

and replace the JSX lines:

```tsx
      <Notifications device={device} />
      <Section title="This phone">
```

with:

```tsx
      <Notifications device={device} />
      <QuickReplies stored={replies} />
      <Section title="This phone">
```

Format and rebuild the bundle:

```bash
pnpm -C internal/web/ui exec prettier --write src/panels/settings/SettingsBody.tsx src/panels/settings/settings.test.tsx
pnpm -C internal/web/ui run build
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/settings/settings.test.tsx`
Expected: the file passes.

Run: `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
Expected: every check passes, `dist:check` says the bundle is fresh and `go test` says `ok`.

Render the screen in the canned loom (the new app's `settings` screen shows the fixture's expired push, the 40-minute pause and the two notices, and webshot fails on a console error, a CSP violation or a request the canned loom does not answer):

```bash
bash -c 'd=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens settings -out "$d"'
```

Expected: exits 0.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "feat(web): settings' quick replies, with the draft kept across a refusal" -- internal/web/ui/src/panels/settings/SettingsBody.tsx internal/web/ui/src/panels/settings/settings.test.tsx internal/web/dist
```

## Story 11: Pairing and Add to Home

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/pair/index.tsx`, `internal/web/ui/src/panels/pair/Pair.tsx`, `internal/web/ui/src/panels/pair/pair.test.tsx`, `internal/web/ui/src/app/install.tsx`, `internal/web/ui/src/app/install.test.tsx`, `internal/web/ui/src/main.tsx`, `internal/web/ui/src/main.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
- **Interfaces:** exposes the `pair` panel, `PAIRING_ALPHABET`, `CODE_LENGTH`, `normalizeCode`, `BROWSER_KEY`, `needsInstall`, `AddToHome`; uses Story 2's `InputOTP` parts, Story 4's `device.pair`, part 1's `CommandButton`, `useLink`, `hrefFor`, `startFleet`, `registerWorker` and `definePanel`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens pair,add-to-home,fleet -out "$d"'`
- **Review:** deep
- **Form:** brief (a port of `pair.mjs` over Story 4's command; `normalizeCode`, which keeps a mistyped code from spending one of the daemon's 5 misses, its idempotence, and the gate starting nothing are named tests; Task 11.2's redirect on a live snapshot beside `device.pair`'s own navigation and Task 11.4's launch order in `main.tsx` are named tests too; the owner kept it brief, 2026-11-20)

The new app can draw no pairing screen and has no Add to Home gate, so an unpaired launch, and an iPhone's browser tab, reach only the placeholder or a stream that answers 401. This story registers the `pair` panel (no head; its body stays in the entry because an unpaired launch opens it) and ports classic's `pair.mjs` (spec Part 3 settlements 14 and 15): the wordmark, "Pair this phone with your loom." and its two steps, the code on Story 2's Input OTP (8 slots in two groups of four, `normalizeCode` upper-casing it and dropping every character the pairing alphabet lacks, typed or pasted, so a mistyped O never spends one of the daemon's 5 misses), Pair through Story 4's `device.pair` once 8 characters are in, no auto-submit, and the fleet opened when a live snapshot arrives. It adds the Add to Home gate (`app/install.tsx`) and wires it into `main.tsx`: the worker is registered as today at every launch, `needsInstall()` is read once, and while it is true only `AddToHome` is drawn and no stream, hash watch, notice watch or router starts, until "Continue in the browser anyway" stores `loom.browser` and the app starts as it does today. The story is brief: each task names the behaviour, the contract names it defines and uses, and each test with what makes it red; the developer writes the code. No code here holds a landmine marker. Neither screen writes a `style` attribute or a `<style>` element (the CSP drops both), takes colours from the tokens, keeps form text at 16px and touch targets at 44px, and keeps the safe-area insets. Task 11.5 rebuilds and commits `internal/web/dist/`; the story's `dist:check` is its last check, not a task's.

Facts the tests are written around, from the code as it stands (`internal/web/ui/src`):

- A panel is `definePanel({ id, title, Head?, Body })` (`app/panels.ts`; `title` and `Body` are required, so `title` is the pair panel's own: "Pairing"); `PanelHost` draws `Body` under the route's error boundary, and `AppShell` draws a `pair` route with no header, no footer and no splash (`chromed` and `waiting` both exclude `pair`), so `Pair` fills the page itself.
- `CommandButton` (`app/CommandButton.tsx`) takes `id`, `args`, `disabled` (the caller's own form state, joined to the command's enablement) and `label` (drawn instead of the command's title); `useCommandBusy(command, ctx, args)`, `useCommandContext()`, `commandById(id)` and `runCommand(command, ctx, args)` are exported by `app/commands.ts`. `device.pair` is `offline` and its `when` is `ctx.panel === "pair"`, so it is enabled on the pair route while the snapshot is stale (Story 4); `ctx.panel` comes from the hash, so a test draws the screen with `window.location.hash = "#/pair"`.
- The pairing alphabet and the code's length are the daemon's (`internal/web/auth.go`'s pairing, 8 characters, `23456789ABCDEFGHJKMNPQRSTUVWXYZ`, 31 characters: no `0`, `1`, `I`, `L` or `O`), and classic's own copy is `NOT_IN_ALPHABET` and `CODE_LEN` in `internal/web/static/js/pair.mjs`.
- `InputOTP` (Story 2, `components/ui/input-otp.tsx`) is a flex row over `OTPField.Root` and passes every Root prop through (`length`, `validationType`, `normalizeValue`, `value`, `onValueChange`, `id`, `inputMode`, `autoComplete`, `aria-describedby`); `InputOTPSlot` is one `OTPField.Input` with no `index` (Base UI numbers slots by order) that passes `aria-label`, `autoFocus` and every other prop through; `InputOTPSeparator` is a short bar; there is no group part, so a caller groups slots in plain `div`s inside the root. Base UI labels the first slot from the root's `id` through a native `<label htmlFor={id}>` (an `aria-label` on the first slot is ignored with a warning), and slots 2 to 8 take an `aria-label`. `normalizeValue` runs after `validationType` filtering and must be idempotent; with `validationType="none"` the normaliser is the whole rule.
- Classic's gate (`pair.mjs`'s `needsInstall`, `app.mjs:380-412`): iOS (`/iPhone|iPad|iPod/` in the user agent, or `navigator.platform === "MacIntel"` with `navigator.maxTouchPoints > 1`, which is how iPadOS says Macintosh), not standalone (`navigator.standalone === true`, or `window.matchMedia("(display-mode: standalone)").matches`), and `pref("browser") !== true`, where `pref` reads localStorage `loom.browser` as JSON and gives null for a key that is unset, unparsable or unreadable. The choice is written as `localStorage.setItem("loom.browser", "true")`, so the classic and new apps share it. webshot's `add-to-home` screen (Story 1) shows the gate by shooting an iPhone tab whose `navigator.standalone` is false.

### Task 11.1: `normalizeCode`

**Files:**
- Create: `internal/web/ui/src/panels/pair/Pair.tsx` (the two constants and the function; Task 11.2 adds the screen to this file)
- Create: `internal/web/ui/src/panels/pair/pair.test.tsx`

Names this task defines (contract, Story 11), in `Pair.tsx`:

- `const PAIRING_ALPHABET = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"` and `const CODE_LENGTH = 8`.
- `function normalizeCode(value: string): string`: upper-cases `value`, keeps only the characters `PAIRING_ALPHABET` holds, then keeps at most `CODE_LENGTH` of them (the cut comes after the filter, so a dropped character costs no slot). It is idempotent, because `OTPField` normalises the same value more than once.

- [ ] **Step 1: Write the failing tests** in `pair.test.tsx`, a `describe("normalizeCode", ...)` (the file's imports are `normalizeCode`, `PAIRING_ALPHABET` and `CODE_LENGTH` from `./Pair`):

  - `normalizeCode: upper-cases what the alphabet holds`: `k7qm2xpa` gives `K7QM2XPA`; `PAIRING_ALPHABET` is 31 characters, holds none of `0`, `1`, `I`, `L`, `O`, and `normalizeCode` leaves its first 8 characters as they are.
    Fails without: `normalizeCode: upper-cases what the alphabet holds`: the upper-casing removed (the result stays lower case), or an alphabet that holds a look-alike.
  - `normalizeCode: drops every character the alphabet lacks, as typed or pasted`: `K7QM-2XPA` gives `K7QM2XPA` (a pasted dash); `O0I1L` gives the empty string; `k7 qm` gives `K7QM` (a space); `k7qmo02xpa` gives `K7QM2XPA` (a mistyped O and 0 never reach the daemon).
    Fails without: `normalizeCode: drops every character the alphabet lacks, as typed or pasted`: the filter removed, or filtering against a wider set (letters and digits), which would let the O and 0 spend a miss.
  - `normalizeCode: clamps to 8 after the filter`: `K7QM2XPA9` gives `K7QM2XPA`; `K7QMO2XPA` gives `K7QM2XPA` (the dropped O costs no slot); `23456789ABC` gives `23456789`.
    Fails without: `normalizeCode: clamps to 8 after the filter`: no clamp (a ninth character stays), or the clamp before the filter (`K7QMO2XPA` gives `K7QM2XP`).
  - `normalizeCode: is idempotent`: for each of `k7qm-2xpa9`, `ßß`, `oO0`, the empty string, `PAIRING_ALPHABET` and `k7qmo02xpa`, `normalizeCode(normalizeCode(x))` equals `normalizeCode(x)`.
    Fails without: `normalizeCode: is idempotent`: a normaliser that rewrites `0` to `O` after the filter (its first pass gives `O`, its second `""`).

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/pair/pair.test.tsx
```

Expected: the file fails to load: `Failed to resolve import "./Pair"`.

- [ ] **Step 3: Implement** the two constants and `normalizeCode` in `Pair.tsx`, with a comment on each saying what it is and why the cut follows the filter. Format with `pnpm -C internal/web/ui exec prettier --write src/panels/pair/Pair.tsx src/panels/pair/pair.test.tsx`.

- [ ] **Step 4: Run them to see them pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/pair/pair.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: the file passes; the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/pair/Pair.tsx internal/web/ui/src/panels/pair/pair.test.tsx
git commit -m "feat(web): normalize the pairing code to the daemon's alphabet" -- internal/web/ui/src/panels/pair/Pair.tsx internal/web/ui/src/panels/pair/pair.test.tsx
```

### Task 11.2: The pairing screen and the `pair` panel

**Files:**
- Modify: `internal/web/ui/src/panels/pair/Pair.tsx` (add the screen below `normalizeCode`)
- Create: `internal/web/ui/src/panels/pair/index.tsx`
- Modify: `internal/web/ui/src/panels/index.ts` (one new line)
- Modify: `internal/web/ui/src/panels/pair/pair.test.tsx`

Names this task defines (contract, Story 11):

- `function Pair(): ReactElement`, exported from `panels/pair/Pair.tsx`: the `pair` panel's body.
- The `pair` panel in `panels/pair/index.tsx`: `definePanel({ id: "pair", title: () => "Pairing", Body: Pair })`, no `Head`, no `tab`, so the header stays empty (the shell draws none for `pair` anyway). `panels/index.ts` gains `import "./pair";` as a new last line, after the last `import "./…";` line it holds when this task runs (the other panel stories each add one); leave its comment and every other line as they are.

Names it uses: Story 2's `InputOTP`, `InputOTPSlot` and `InputOTPSeparator`; Story 4's `device.pair` (args `{ code: string }`); part 1's `CommandButton`, `commandById`, `runCommand`, `useCommandBusy`, `useCommandContext`, `hrefFor`, `useFleet` and `definePanel`; this story's `normalizeCode`, `CODE_LENGTH`.

Behaviour of `Pair`:

- A column filling the page (`flex-1`, padding of `px-4 py-8`, a gap, tokens only): an `<h1>` "Loom" (the wordmark); the paragraph "Pair this phone with your loom."; an ordered list of two steps, "On the computer running loom, run `loom web pair`." (the command in a `<code>`) and "Enter the code it prints. It works once, for 10 minutes."; the code field; the Pair button.
- The code field: a `useId()` for the root's `id`; a native `<label htmlFor={id}>` reading "Pairing code", visually hidden (`sr-only`); `InputOTP` with `id`, `length={CODE_LENGTH}`, `validationType="none"`, `normalizeValue={normalizeCode}`, `inputMode="text"`, `autoComplete="off"`, `value` and `onValueChange` over a `useState("")` string; inside it two plain `div`s (flex, a gap) of four `InputOTPSlot`s each with one `InputOTPSeparator` between the `div`s; the first slot `autoFocus` (as classic's input was), slots 2 to 8 `aria-label="Character <n> of 8"`, and every slot `autoCapitalize="characters"`, `autoCorrect="off"` and `spellCheck={false}`. No `autoSubmit` and no `onValueComplete`: classic never submitted on the eighth character.
- The button is `<CommandButton id="device.pair" args={{ code }} disabled={code.length !== CODE_LENGTH} label={busy ? "Pairing…" : undefined} />` (44px tall), where `busy` is `useCommandBusy` of the `commandById("device.pair")` command (an unknown command throws `loom: unknown command device.pair`, as `CommandButton` does) with `useCommandContext()` and the same args. Enter in any slot (a `keydown` handler on the column) pairs through `runCommand` with the same command, context and `{ code }`, only at `CODE_LENGTH` characters; `runCommand` itself ignores a second call while one is in flight. A refusal, a failed request and every toast are `device.pair`'s (Story 4): `Pair` shows no message of its own and keeps the typed code, as classic's inline line did.
- A live snapshot opens the fleet: an effect over `useFleet((s) => s.snapshot !== null && !s.stale)` sets `window.location.hash = hrefFor("fleet")` when it is true, as classic's first snapshot on `#pair` did. A snapshot loaded from storage is stale, so it does not.

- [ ] **Step 1: Write the failing tests** in `pair.test.tsx`, in a `describe("Pair", ...)` after `normalizeCode`'s. The file mocks `@/store/fleet` with `startFleet: vi.fn(() => () => undefined)` over the original (as Story 4's `commands/device.test.ts` does), imports `@/commands/device` for the registration, and in `beforeEach` sets `window.location.hash = "#/pair"`, spies `api.pair` from `@/api/request` (`vi.spyOn`, resolving `{ kind: "ok", status: 200, value: { device: "iPhone" }, notices: [] }` unless a test says otherwise), spies `window.location.assign`, and calls `spyOnAdd()` from `@/commands/testkit`; `afterEach` restores the mocks, resets the fleet (`resetFleet`) and the hash. A helper types into the first slot with `userEvent` (the first slot is found by its label `Pairing code`) and reads the eight slots' values:

  - `Pair: draws the wordmark, the lede, the two steps and eight slots in two groups of four`: an `h1` named `Loom`; the text `Pair this phone with your loom.`; the step texts, the first holding a `code` element `loom web pair`; eight inputs with `data-slot="input-otp-slot"`; the first found by the label `Pairing code`, the second named `Character 2 of 8`, the eighth `Character 8 of 8`; one `data-slot="input-otp-separator"` element; the first four slots share one parent `div` and the last four another, and the two differ.
    Fails without: `Pair: draws the wordmark, the lede, the two steps and eight slots in two groups of four`: the slots in one `div`, a label or an `aria-label` missing, the separator dropped, or a step's words changed.
  - `Pair: Pair is off with seven characters and on with eight`: typing `K7QM2XP` leaves the button named `Pair` disabled; typing `A` after it enables it.
    Fails without: `Pair: Pair is off with seven characters and on with eight`: the `disabled` prop removed (enabled at seven), or compared with `>` rather than the length (off at eight).
  - `Pair: typing drops what the alphabet lacks, so a mistyped O never reaches the daemon`: typing `k7qmo02xpa` and pressing Pair calls `api.pair` once, with `{ code: "K7QM2XPA" }`, and the eight slots read `K`, `7`, `Q`, `M`, `2`, `X`, `P`, `A`.
    Fails without: `Pair: typing drops what the alphabet lacks, so a mistyped O never reaches the daemon`: `normalizeValue` not passed to `InputOTP` (the slots hold `k7qmo02x` and the call sends it), or `length` not passed.
  - `Pair: a paste is upper-cased, loses its dash and is clamped`: `user.paste` of `k7qm-2xpa9` into the first slot leaves the slots reading `K7QM2XPA` and Pair enabled.
    Fails without: `Pair: a paste is upper-cased, loses its dash and is clamped`: `normalizeValue` or `length` not passed to `InputOTP`.
  - `Pair: filling the eight slots sends nothing until Pair is pressed`: after typing `K7QM2XPA` and letting the event loop turn (`await waitFor` over a settled promise), `api.pair` was not called; pressing Pair calls it once.
    Fails without: `Pair: filling the eight slots sends nothing until Pair is pressed`: `autoSubmit` set on the root, or an `onValueComplete` that runs `device.pair`.
  - `Pair: Enter in a slot pairs once at eight characters and does nothing before`: with `K7QM2XP` typed, `{Enter}` calls nothing; after the eighth character, `{Enter}` calls `api.pair` once with `{ code: "K7QM2XPA" }`.
    Fails without: `Pair: Enter in a slot pairs once at eight characters and does nothing before`: the `keydown` handler removed (no call at eight), or its length check removed (a call at seven).
  - `Pair: the button reads "Pairing…" and is off while the request is in flight, and a refusal keeps the code`: with `api.pair` held by the testkit's `deferred()`, pressing Pair leaves a disabled button named `Pairing…` (and Enter in a slot meanwhile adds no second call); resolving it with `{ kind: "refused", status: 400, message: "that code is not valid" }` brings back an enabled button named `Pair`, with the eight slots still holding the code.
    Fails without: `Pair: the button reads "Pairing…" and is off while the request is in flight, and a refusal keeps the code`: the `label` swap removed (the name stays `Pair`), or the code reset after the command ends.
  - `Pair: a live snapshot opens the fleet, a stale or absent one does not`: with `useFleet.setState({ snapshot: fixtureSnapshot(), stale: true, link: "offline" })` the hash stays `#/pair`; with `{ snapshot: null, stale: false }` it stays; with `{ snapshot: fixtureSnapshot(), stale: false, link: "live" }` (inside `act`) it becomes `#/`.
    Fails without: `Pair: a live snapshot opens the fleet, a stale or absent one does not`: the effect removed (never `#/`), the `!s.stale` term dropped (the stale snapshot navigates at once), or the `snapshot !== null` term dropped (the empty live state navigates).
  - `the pair panel: is registered with no head and Pair as its body, and @/panels registers it`: after importing `./index`, `panelFor("pair")` has `Body` equal to `Pair`, no `Head` and no `tab`; in a second test `await import("@/panels")` leaves `panelFor("pair")` defined.
    Fails without: `the pair panel: is registered with no head and Pair as its body, and @/panels registers it`: the `definePanel` call removed, a `Head` added, or the `import "./pair";` line missing from `panels/index.ts` (the second test).

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/pair/pair.test.tsx
```

Expected: the `normalizeCode` tests pass; the `Pair` tests fail rendering `Pair`, which `Pair.tsx` does not export yet (`Element type is invalid`), and the panel tests fail on the missing `./index`.

- [ ] **Step 3: Implement** the screen in `Pair.tsx`, the panel in `index.tsx` (a short comment above it: the body stays in the entry because an unpaired launch opens it) and the import line, to the behaviour above. Take every colour from the tokens (`text-muted-foreground`, `bg-card` and the like), write no `style` attribute, and format with prettier on the files this task touches.

- [ ] **Step 4: Run them to see them pass**

```bash
pnpm -C internal/web/ui exec vitest run src/panels/pair/pair.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: the file passes; the typecheck and lint print nothing. If a paste in happy-dom does not reach Base UI's handler through `user.paste`, dispatch the same `paste` event the component listens for in the test, never a different component behaviour (Story 2's note).

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/pair/index.tsx
git commit -m "feat(web): the pairing screen on Base UI's OTP field" -- internal/web/ui/src/panels/pair/Pair.tsx internal/web/ui/src/panels/pair/index.tsx internal/web/ui/src/panels/pair/pair.test.tsx internal/web/ui/src/panels/index.ts
```

### Task 11.3: `needsInstall` and `AddToHome`

**Files:**
- Create: `internal/web/ui/src/app/install.tsx`
- Create: `internal/web/ui/src/app/install.test.tsx`

Names this task defines (contract, Story 11), in `app/install.tsx`:

- `const BROWSER_KEY = "loom.browser"`.
- `function needsInstall(): boolean`: true when the user agent says iPhone, iPad or iPod, or `navigator.platform` is `MacIntel` with `navigator.maxTouchPoints > 1`; and the page is not standalone (`navigator.standalone` is not `true`, and `window.matchMedia("(display-mode: standalone)").matches` is not true, a missing `matchMedia` counting as not matching); and the stored choice is not `true`: `localStorage.getItem(BROWSER_KEY)` parsed as JSON equals `true`, an absent, unparsable or unreadable value (a `getItem` that throws, a `JSON.parse` that throws) counting as no choice. It never throws.
- `function AddToHome(props: { onContinue: () => void }): ReactElement`: the page, with the classic words. A column filling the page inside the safe-area insets (`min-h-dvh`, the `env(safe-area-inset-*)` paddings and the shell's `max-w-[40rem]` centring, as `AppShell` does for an unchromed route; it is drawn outside the shell): an `<h1>` "Loom"; "Add Loom to your Home Screen to use it. The installed app keeps its own pairing, separate from this tab."; an ordered list of three steps, "Tap the Share button in Safari's toolbar.", "Scroll down and tap **Add to Home Screen**." and "Tap **Add**, then open Loom from your Home Screen." (the bold words in `<strong>`); "The app then asks for a pairing code."; and a plain `<button type="button">` "Continue in the browser anyway" (44px tall, link-styled with `underline` and `text-muted-foreground`), which stores `localStorage.setItem(BROWSER_KEY, "true")` and then calls `onContinue()`. A storage that throws (private mode, a full quota) is swallowed: the browser is still continued, the choice just does not outlive the page.

Names it uses: part 1's tokens and `cn`.

- [ ] **Step 1: Write the failing tests** in `install.test.tsx`. A helper sets the page's iOS-ness for one test: `vi.spyOn(navigator, "userAgent", "get")`, `vi.spyOn(navigator, "platform", "get")` and `vi.spyOn(navigator, "maxTouchPoints", "get")`, `Object.defineProperty(navigator, "standalone", { value, configurable: true })` (deleted in `afterEach`), `vi.spyOn(window, "matchMedia")` returning `{ matches }`, and `localStorage.clear()` in `beforeEach`; the iPhone user agent is `Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1` and the desktop one `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36`. In `describe("needsInstall", ...)`:

  - `needsInstall: an iPhone browser tab that has not chosen the browser needs the install`: the iPhone user agent, not standalone, nothing stored: `true`.
    Fails without: `needsInstall: an iPhone browser tab that has not chosen the browser needs the install`: the user-agent test removed or inverted.
  - `needsInstall: an iPad that reports itself as a Mac needs it too`: the desktop user agent with `navigator.platform` `MacIntel` and `maxTouchPoints` 5: `true`; the same with `maxTouchPoints` 0 (a Mac) and with `platform` `Linux x86_64` and 5 touch points: `false`.
    Fails without: `needsInstall: an iPad that reports itself as a Mac needs it too`: the `MacIntel` term removed (an iPad is missed), or the touch-point term removed (every Mac needs it).
  - `needsInstall: a desktop browser does not`: the desktop user agent, platform `Linux x86_64`, no touch points: `false`.
    Fails without: `needsInstall: a desktop browser does not`: the iOS test replaced by `true`.
  - `needsInstall: the installed app does not, by navigator.standalone or by display-mode`: the iPhone user agent with `navigator.standalone` `true`: `false`; with `standalone` undefined and `matchMedia("(display-mode: standalone)")` matching: `false`; the query asked is exactly `(display-mode: standalone)`.
    Fails without: `needsInstall: the installed app does not, by navigator.standalone or by display-mode`: either standalone term removed, or the media query changed.
  - `needsInstall: a stored browser choice of true ends it, and nothing else does`: the iPhone user agent with `localStorage` `loom.browser` set to `true` (the text): `false`; set to `1`, `"yes"` (with its quotes), `false`, `yes` (unparsable) or absent: `true`; with `Storage.prototype.getItem` throwing: `true`.
    Fails without: `needsInstall: a stored browser choice of true ends it, and nothing else does`: the comparison loosened to a truthy check (the stored `1` ends it), or the parse not guarded (the throw escapes or the unparsable text counts as a choice).
  - `BROWSER_KEY: is the classic app's key`: `BROWSER_KEY` is `loom.browser`, which `internal/web/static/js/store.mjs`'s `pref("browser")` reads, so a choice made in one app holds in the other.
    Fails without: `BROWSER_KEY: is the classic app's key`: the key renamed.

  In `describe("AddToHome", ...)`:

  - `AddToHome: draws the classic words`: the `h1` `Loom`, the lede, the three steps (the second with `Add to Home Screen` in a `strong`), "The app then asks for a pairing code." and a button named `Continue in the browser anyway`.
    Fails without: `AddToHome: draws the classic words`: any of the words changed or a step dropped.
  - `AddToHome: Continue stores the choice before it calls onContinue, once`: pressing the button calls an `onContinue` that records `localStorage.getItem("loom.browser")` at the time it runs; the record is the text `true`, `onContinue` ran once, and `needsInstall()` is now `false` for an iPhone tab.
    Fails without: `AddToHome: Continue stores the choice before it calls onContinue, once`: the store removed (the record is null), the store after `onContinue` (the record is null), or the call removed.
  - `AddToHome: a storage that refuses still continues`: with `Storage.prototype.setItem` throwing, pressing the button still calls `onContinue` once and throws nothing.
    Fails without: `AddToHome: a storage that refuses still continues`: the `try`/`catch` around the store removed (the click handler throws before `onContinue`).

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/app/install.test.tsx
```

Expected: the file fails to load: `Failed to resolve import "./install"`.

- [ ] **Step 3: Implement** `install.tsx` to the behaviour above, with a comment on `needsInstall` saying why the installed app keeps its own cookies (pairing in Safari would pair the wrong place), as classic's does. Format with prettier on the two files.

- [ ] **Step 4: Run them to see them pass**

```bash
pnpm -C internal/web/ui exec vitest run src/app/install.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: the file passes; the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/app/install.tsx internal/web/ui/src/app/install.test.tsx
git commit -m "feat(web): the Add to Home gate's check and page" -- internal/web/ui/src/app/install.tsx internal/web/ui/src/app/install.test.tsx
```

### Task 11.4: `main.tsx` reads the gate once and starts nothing behind it

**Files:**
- Modify: `internal/web/ui/src/main.tsx:18-46` (the body after its imports)
- Create: `internal/web/ui/src/main.test.tsx` (the entry's `Files` line lacks it; `main.tsx` has no test today)

Names it uses: this story's `needsInstall` and `AddToHome`; part 1's `watchHash`, `watchNotices`, `startFleet`, `onUnpaired`, `goToPairing`, `registerWorker`, `router`, `queryClient`.

Today `main.tsx` starts everything at module load, right after the `mount` lookup (lines 18 to 19, which stay):

```
const stopHash = watchHash();
const stopNotices = watchNotices();
const stopFleet = startFleet();
const stopUnpaired = onUnpaired(goToPairing);
registerWorker();
```

then renders `<StrictMode><CSPProvider disableStyleElements><QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider></CSPProvider></StrictMode>` with `createRoot(mount).render(`, and ends with the `import.meta.hot.dispose` block calling `stopHash()`, `stopNotices()`, `stopFleet()` and `stopUnpaired()`.

New behaviour, in this order and nothing else changed (the imports gain `AddToHome` and `needsInstall` from `@/app/install`; `import "./styles/app.css"`, `import "@/panels"` and `import "@/commands"` stay):

- `registerWorker()` runs at every launch, first, as it does today (spec §5: the worker updates and the push check runs whether or not the gate shows).
- One `createRoot(mount)` is made. The first draw is decided by one read of `needsInstall()`: when it is true the root draws `<StrictMode><CSPProvider disableStyleElements><AddToHome onContinue={start} /></CSPProvider></StrictMode>` and nothing else runs; when it is false `start()` runs at once.
- `start()` is the old body moved into a function, once: `watchHash()`, `watchNotices()`, `startFleet()`, `onUnpaired(goToPairing)` (their stops kept for the hot-reload dispose block, which stays, and stops whatever has started), then the root draws the old tree (the `StrictMode`, `CSPProvider`, `QueryClientProvider` and `RouterProvider` as above). `needsInstall()` is not read again: after "Continue" the app starts whatever it would now say.
- While the gate shows, nothing the old body started has started: no stream (`startFleet`), no hash watch, no notice watch, no unpaired listener and no `RouterProvider`. (`routes.tsx`'s import-time `settleHash()` still runs: it only rewrites a slashless hash in place and starts nothing.)

- [ ] **Step 1: Write the failing tests** in `main.test.tsx`. The file mocks, over their originals, `@/store/fleet` (`startFleet: vi.fn(() => () => undefined)`), `@/app/worker` (`registerWorker: vi.fn()`), `@/app/toasts` (`watchNotices: vi.fn(() => () => undefined)`), `@/app/routes` (`watchHash: vi.fn(() => () => undefined)` and `goToPairing: vi.fn()`) and `@tanstack/react-router` (`RouterProvider` drawing `<p>router</p>`). Each test makes a `<div id="app">` in `document.body`, sets the page's iOS-ness as Task 11.3's helper does (copy its few lines: the helper is private to `install.test.tsx`), clears the mocks and `localStorage`, calls `vi.resetModules()`, and draws `main` with `await act(async () => { await import("@/main"); })`; `afterEach` removes the div and restores the spies. In `describe("main", ...)`:

  - `main: a launch that needs no install registers the worker and starts the app`: the desktop user agent: `registerWorker`, `startFleet`, `watchHash` and `watchNotices` each called once, the text `router` drawn, no `Continue in the browser anyway` button.
    Fails without: `main: a launch that needs no install registers the worker and starts the app`: `start()` never run on the false branch, or a start step dropped.
  - `main: an iOS browser tab draws only Add to Home and starts nothing`: the iPhone user agent, not standalone, nothing stored: `registerWorker` called once; `startFleet`, `watchHash` and `watchNotices` never called; the button `Continue in the browser anyway` drawn; the text `router` absent.
    Fails without: `main: an iOS browser tab draws only Add to Home and starts nothing`: the gate removed (the app starts), the check made after `start()`, or one of the starts moved above the check.
  - `main: Continue in the browser anyway stores the choice and starts the app once`: after the iPhone case, pressing the button leaves `localStorage` `loom.browser` at `true`, `startFleet` and `watchHash` each called once, the text `router` drawn and the button gone; `registerWorker` still called once in all.
    Fails without: `main: Continue in the browser anyway stores the choice and starts the app once`: `onContinue` not wired to `start` (nothing starts), `start` run twice (two calls), or `registerWorker` moved into `start()` (before the press it has not run, so the check that it was already called once fails).
  - `main: an iOS browser tab that already chose the browser starts at once`: the iPhone user agent with `loom.browser` stored as `true`: `startFleet` called once, no button, `router` drawn.
    Fails without: `main: an iOS browser tab that already chose the browser starts at once`: `needsInstall()` replaced by a user-agent check alone, which would show the gate again on every launch.

- [ ] **Step 2: Run them to see them fail**

```bash
pnpm -C internal/web/ui exec vitest run src/main.test.tsx
```

Expected: `main: an iOS browser tab draws only Add to Home and starts nothing` fails (`startFleet` was called, no button drawn); the second and third cases fail on the same missing gate; the first and fourth pass.

- [ ] **Step 3: Implement** the change to `main.tsx` as described: keep the `mount` check, the hot-reload dispose block's stops (as `let` stops that are undefined until `start()` runs, called only when set) and its comments, and add a comment above the gate quoting spec §5's reason for registering the worker first and Part 3 settlement 15's rule that the gate starts no stream and no route. Format with prettier.

- [ ] **Step 4: Run them to see them pass**

```bash
pnpm -C internal/web/ui exec vitest run src/main.test.tsx src/app/install.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint
```

Expected: both files pass; the typecheck and lint print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/main.test.tsx
git commit -m "feat(web): show Add to Home before the app starts on an iOS browser tab" -- internal/web/ui/src/main.tsx internal/web/ui/src/main.test.tsx
```

### Task 11.5: The rebuilt bundle and the two screens

**Files:**
- Modify: `internal/web/dist/` (rebuilt)

- [ ] **Step 1: Run the suite against the app as the story leaves it**

```bash
pnpm -C internal/web/ui run check
```

Expected: the typecheck, lint, format check, tests and `node --check public/sw.js` all pass.

- [ ] **Step 2: Rebuild the bundle** (the pairing screen, the gate and `InputOTP` join the entry, so its hashed files and `index.html` change):

```bash
bash -c 'pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run dist:check'
```

Expected: the build succeeds and `dist:check` reports the bundle fresh.

- [ ] **Step 3: Run the Go checks over the bundle**

```bash
CGO_ENABLED=0 go test ./internal/web/
```

Expected: `ok github.com/aidan-bailey/loom/internal/web`, `TestDist_FirstLoadIsWithinBudget` and `TestStatic_NextIndexHasNoInlineScriptOrStyle` among them. If the first load is over its budget, do not raise it: report the size, since the pairing screen is meant to cost the entry only the OTP field's few kilobytes.

- [ ] **Step 4: Shoot the screens under the real policy**

```bash
bash -c 'd=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens pair,add-to-home,fleet -out "$d"'
```

Expected: the run exits 0 with no console error and no counted miss: `pair` draws the pairing screen (the canned loom answers every `/api/v1/` request 401 with the unpaired body, and the app stays on `#/pair`), `add-to-home` draws only the Add to Home page, and `fleet` still draws the fleet (a standalone tab starts the app).

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "build(web): rebuild the bundle for pairing and the Add to Home gate" -- internal/web/dist
```

## Story 12: No route draws the placeholder

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/panels/index.ts`, `internal/web/ui/src/app/commands.ts`, `internal/web/dist/`
- **Interfaces:** exposes the splash's way back and the every-panel test; uses part 1's `CommandButton`, `panelFor` and the `PanelId` union, Stories 6 to 11's panels
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d"'`
- **Review:** light (removing the placeholder and one test)
- **Form:** brief (dead code removed and the splash's button)

With Stories 6 to 11 every `PanelId` has a panel, so the shell's `Placeholder` (a heading, "<name> is not in the new app yet. It is in the classic one." and the switch button) draws for nothing. This story removes it, keeps the one thing it did that must survive (the splash drawn with no snapshot offers "Back to classic" on every route, so the way back works with the daemon down and nothing saved), and pins that `@/panels` registers a panel under every `PanelId`. The code is removal plus one button, so every task is described, not copied: the developer edits `shell.tsx`, `panels.ts`, `panels/index.ts` and `commands.ts` against what the tree holds once Stories 6 to 11 have landed (`panels/index.ts` in particular has gained their imports; no edit here quotes or changes one). No code of this story holds a landmine marker.

Facts the tasks rely on, from `internal/web/ui/src/app/shell.tsx` as it stands:

- `Placeholder` (exported) is drawn from two places: `PanelHost` when `panelFor(panel)` is undefined, and `AppShell`'s `<main>` under the splash line when the route has no panel. Its heading comes from `SCREEN_NAMES: Record<PanelId, string>`, which `AppShell` also uses as the fallback of the header's and the strip's boundary name (`entry?.title(props) ?? SCREEN_NAMES[route.panel]`); that fallback is never reached, because `Head` and `Strip` are read from `entry`, so neither exists without it.
- `AppShell` draws the splash when `waiting` (`!hasSnapshot && route.panel !== "pair"`); a `pair` route is drawn through the outlet with no splash and no chrome. `hrefFor` stays imported: `SessionTabs` and the tab-cycling code still use it. `Button` (the `Boundary`'s Reload), `CommandButton` and `PanelId` (`resetKey`, `TABBED`, `tabOf`) stay used.
- `app.backToClassic` is part 1's `offline` command (`app/commands.ts`), so a `CommandButton` for it runs while the snapshot is stale or missing; no store state or route context is needed.
- Vitest gives each test file its own module registry, and `app/shell.test.tsx` and `app/panels.test.ts` never import `@/panels` (only `main.tsx` and the panel test files do), so in them a panel is registered only by the file's own `definePanel` calls, and `panelFor` of any other id is `undefined`.

### Task 12.1: Remove `Placeholder`; the splash offers the way back on every route

**Files:**
- Modify: `internal/web/ui/src/app/shell.tsx`
- Test: `internal/web/ui/src/app/shell.test.tsx`

Names this task defines: none new. It changes `PanelHost` (still exported, same props) to draw nothing for a panel with none registered, removes the export `Placeholder`, and gives `AppShell`'s splash the button.

- [ ] **Step 1: Write the failing tests** in `shell.test.tsx` (its existing imports, helpers `shellAt`, `header` and `footer`, the `switchApp` mock and the `afterEach` that resets the store and the hash stay; `Placeholder` leaves the `./shell` import, and `definePanel` is already imported). Replace the `describe("Placeholder", …)` block (its one test, "names the screen, links back to the fleet and offers the classic app") with nothing, and change these:

  - In `describe("PanelHost", …)`, replace `draws the placeholder for a panel nothing registered` with `draws nothing for a panel nothing registered`: `const { container } = render(<PanelHost panel="flowDoc" props={props} />)` (nothing in this file registers `flowDoc`, and the file never imports `@/panels`), then `expect(container).toBeEmptyDOMElement()`, `screen.queryByRole("heading")` is null and `screen.queryByRole("button", { name: "Back to classic" })` is null.
    Fails without: `draws nothing for a panel nothing registered`: `PanelHost` still returning a placeholder (a heading, a button) for an unregistered panel, or throwing on it.
  - In `describe("AppShell", …)`, replace `offers the way back to the classic app on a route no panel is registered for, with no snapshot` with an `it.each` named `offers the way back to the classic app under the splash on %s, with no snapshot` over the hashes `#/` (the fleet), `#/s/5/flow` (a session's board), `#/settings` and `#/new` (the file's other tests register some of these panels and not others, and the button must not depend on which): `useFleet.setState({ link: "offline" })`, `shellAt(hash)`, `await screen.findByText("Can't reach loom. Retrying…")`, then click the button named "Back to classic" and expect `switchApp` toHaveBeenCalledWith `"classic"`; also expect exactly one such button (`getAllByRole("button", { name: "Back to classic" })` has length 1) and no heading (`queryByRole("heading")` null: the placeholder's is gone).
    Fails without: `offers the way back to the classic app under the splash on %s, with no snapshot`: the splash not drawing the button for any of the four routes, or drawing it twice (the length check); the heading check fails while any placeholder is still drawn.
  - Add, in the same `describe`, `offers the way back on a route whose panel is registered, with no snapshot`: `definePanel({ id: "settings", title: () => "Settings", Body: () => <p>the settings panel</p> })`, `useFleet.setState({ link: "offline" })`, `shellAt("#/settings")`, the splash line `Can't reach loom. Retrying…` found, and the button named "Back to classic" present (the registered panel's own body is not asked for: with no snapshot the shell draws the splash instead of the outlet).
    Fails without: `offers the way back on a route whose panel is registered, with no snapshot`: the button drawn only for an unregistered route (so not once a panel is registered, which is every route now).
  - Add `draws no way back of its own once the snapshot is held`: `useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" })`, `shellAt("#/")`, `await screen.findByText("outlet")`, and `queryByRole("button", { name: "Back to classic" })` is null (the button belongs to the splash; Settings draws its own, Story 10).
    Fails without: `draws no way back of its own once the snapshot is held`: the button moved out of the splash so that it draws whenever the shell does.
  - `draws pairing without a snapshot, and without chrome` stays as it is, and gains `queryByRole("button", { name: "Back to classic" })` null (pairing has no splash, and Pair draws its own screen).
    Fails without: `draws pairing without a snapshot, and without chrome`: the button drawn for the `pair` route, where the pairing screen has no way back to draw.
  - In `describe("router", …)`, replace `draws the placeholder of a route no panel is registered for` with `draws the panel registered for a route`: `definePanel({ id: "settings", title: () => "Settings", Body: () => <p>the settings panel</p> })`, the same `useFleet.setState`, `render(<RouterProvider router={router} />)` and `router.navigate({ to: "/settings" })` inside `act`, then `await screen.findByText("the settings panel")` and `window.location.hash` is `#/settings`. It keeps what the old test pinned (a hash route reaches its screen through the router).
    Fails without: `draws the panel registered for a route`: `RoutedPanel` or `PanelHost` not drawing a registered `settings` panel's body, or the route not matching `#/settings`.

- [ ] **Step 2: Run it to see it fail**: `pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx`. Expected: `draws nothing for a panel nothing registered` fails (the placeholder's "Flow document" heading is drawn), the splash test fails for every hash where the placeholder still draws (its heading is found, `queryByRole("heading")` is not null), and `offers the way back on a route whose panel is registered, with no snapshot` fails (the old shell draws the button only for an unregistered route); `draws no way back of its own once the snapshot is held`, the pairing test and the router test already pass.

- [ ] **Step 3: Implement** in `shell.tsx`:
  - Delete `SCREEN_NAMES` (with its comment) and the exported `Placeholder` (with its comment).
  - `PanelHost`: when `entry` is undefined return `null` (the signature's return type becomes `ReactElement | null`); reword its comment so it no longer says "or the placeholder when none is" (a panel with none registered draws nothing; every `PanelId` has one, which `app/panels.test.ts` pins).
  - `AppShell`: the header's and the strip's boundary names become `` `The ${title} header` `` and `` `The ${title} strip` `` with one local `const title = entry === undefined ? route.panel : entry.title(props)` computed beside `entry` (the replacement for `entry?.title(props) ?? SCREEN_NAMES[route.panel]`, whose fallback is unreachable). In `<main>`, replace the line that draws `<Placeholder panel={route.panel} />` when `panelFor(route.panel) === undefined` with the button under the splash line, drawn whenever `waiting` is true: `<CommandButton id="app.backToClassic" variant="secondary" />` in a centred row (`flex justify-center px-4`), after the `role="status"` splash `div`. The `waiting` branch's wrapper fragment stays.
  - Reword `AppShell`'s own doc comment (it says a route no panel is registered for "still draws its placeholder under the splash"): the splash draws the way back to the classic app on every route, so it works with the daemon down and nothing saved. Remove any import this leaves unused (`hrefFor`, `Button`, `CommandButton` and `PanelId` stay used; `panelFor` stays used by `PanelHost` and `AppShell`'s `entry`).

- [ ] **Step 4: Run it to see it pass**: `pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx`, then `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`. Expected: every shell test passes, typecheck and lint report nothing (an unused `Placeholder` import in `shell.test.tsx` or an unused `hrefFor` in `shell.tsx` would show here).

- [ ] **Step 5: Commit**: `git commit -m "refactor(web): remove the placeholder; the splash offers the way back on every route" -- internal/web/ui/src/app/shell.tsx internal/web/ui/src/app/shell.test.tsx`

### Task 12.2: Pin that every `PanelId` has a panel

**Files:**
- Modify: `internal/web/ui/src/app/panels.test.ts`

Names this task defines: none. It uses part 1's `panelFor` and `PanelId`, and Stories 6 to 11's panels (`flow`, `flowStory`, `flowDoc`, `flows`, `new`, `settings`, `pair`), which register when `@/panels` (`panels/index.ts`) is imported.

- [ ] **Step 1: Write the failing test**, added last in `describe("the panel registry", …)` of `panels.test.ts`: `it("has a panel for every PanelId once @/panels is imported", …)`, with a timeout of 30 000 ms as the `it`'s third argument (it imports every panel module). The file's other tests register `pair`, `new`, `flows`, `info`, `changes`, `turns` and `mockup` in the file's own registry and its first one pins `panelFor("pair")` undefined, so this test must not touch that registry nor import `@/panels` at the top: it calls `vi.resetModules()` (add `vi` to the file's `vitest` import), then `const registry = await import("./panels")` and `await import("@/panels")` (a fresh registry that only the real panel modules fill: they import `@/app/panels`, which resolves to the same fresh module as `./panels`). The ids are listed once as `const ids: Record<PanelId, true> = { fleet: true, turns: true, info: true, changes: true, mockup: true, mockupFull: true, agent: true, job: true, flow: true, flowStory: true, flowDoc: true, flows: true, new: true, settings: true, pair: true }` (add `type PanelId` to the file's `./panels` import), typed so a `PanelId` added later without a line here fails the typecheck. Before the `@/panels` import, assert `registry.panelFor("fleet")` is undefined (the fresh registry is empty, so the import is what registers); after it, for each key of `ids` assert `registry.panelFor(id)?.id` equals `id` (so `toBe(id)`, which fails with the id named when none is registered).
  Fails without: `has a panel for every PanelId once @/panels is imported`: any one of `panels/index.ts`'s imports of Stories 6 to 11 (`./flow`, `./flows`, `./new`, `./settings`, `./pair`; `./flow` also registers `flowStory` and `flowDoc` through Story 7) removed, or a panel module defining an id other than its own; the empty-registry assertion (`panelFor("fleet")` undefined before the import) fails if the import is made before it, so the test cannot pass on a registry some other import already filled.

- [ ] **Step 2: Run it to see it fail**: with the `import "./pair";` line of `internal/web/ui/src/panels/index.ts` deleted for this run only, `pnpm -C internal/web/ui exec vitest run src/app/panels.test.ts` fails on `pair` (`expected undefined to be 'pair'`); put the line back afterwards (the test passes at once on the tree as Stories 6 to 11 leave it, which is the point of a pin).

- [ ] **Step 3: Implement**: the test is the whole change; no source change in this task (restore `panels/index.ts` if Step 2 left it edited: `git diff --stat -- internal/web/ui/src/panels/index.ts` prints nothing).

- [ ] **Step 4: Run it to see it pass**: `pnpm -C internal/web/ui exec vitest run src/app/panels.test.ts`, then `pnpm -C internal/web/ui run typecheck`. Expected: the file's four tests pass.

- [ ] **Step 5: Commit**: `git commit -m "test(web): pin that every PanelId has a panel" -- internal/web/ui/src/app/panels.test.ts`

### Task 12.3: Reword the comments that name the placeholder; rebuild the bundle; render every screen

**Files:**
- Modify: `internal/web/ui/src/app/panels.ts`
- Modify: `internal/web/ui/src/panels/index.ts`
- Modify: `internal/web/ui/src/app/commands.ts`
- Modify: `internal/web/dist/` (rebuilt, never edited by hand)

Names this task defines: none. The edits are comments only.

- [ ] **Step 1: List the comments to reword**: `grep -rn 'placeholder' internal/web/ui/src --include=*.ts --include=*.tsx | grep -v 'placeholder=\|placeholder:\|Placeholder(\|api/gen.ts'` and read the hits that are not an input's `placeholder` text or the worker's unfilled build tag (`app/worker.ts`, `app/sw.test.ts`): expect three, `app/panels.ts` (the `PanelId` comment, "a route no panel is registered for draws the placeholder"), `panels/index.ts` (its header comment, "A route with no panel registered draws the placeholder.") and `app/commands.ts` (the `app.backToClassic` comment, "drawn by every placeholder"), and none in `app/shell.tsx`, which Task 12.1 reworded (a hit there is a comment 12.1 missed: reword it too).

- [ ] **Step 2: Reword each**, comments only, leaving every import line and every other line of those files as it is:
  - `app/panels.ts`: the `PanelId` comment says a panel is registered under one of these ids and that every id has one (`app/panels.test.ts` pins it); nothing about a placeholder.
  - `panels/index.ts`: the header comment keeps its first sentences (a panel registers when its module is imported, so a part that adds a panel adds a line) and drops the placeholder sentence, saying instead that every `PanelId` has an import here.
  - `app/commands.ts`: the `app.backToClassic` comment says it is the switch back to the classic app, drawn under the splash on every route (so it works with the daemon down and nothing saved) and in Settings, and is `offline` for that reason; the rest of that comment (the cookie is set, the worker's update may fail and the switch then comes with the next launch) stays.

- [ ] **Step 3: Rebuild the bundle and check it**: `pnpm -C internal/web/ui run build`, then `pnpm -C internal/web/ui run dist:check` (expect `internal/web/dist matches the build.`), then `grep -rl 'is not in the new app yet' internal/web/dist` (expect no output: the placeholder's sentence is gone from every chunk, and this story's own text does not contain it), then `CGO_ENABLED=0 go test ./internal/web/` (the bundle, budget and lazy-chunk tests pass; this story changes no chunk's name and adds none to the entry's budget but removes the placeholder's bytes).

- [ ] **Step 4: Render every screen of the new app**: `d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d"`. Expected: webshot writes one image per screen of its list (the fleet, conversation, the flow views, flows, new, settings and the rest, `pair` and `add-to-home` included) and exits 0; it fails the run on a console error or a request its canned loom does not answer, so a screen that drew a stray placeholder fetch or threw would stop it. Open the `settings` and `flows` images only if the run fails.

- [ ] **Step 5: Commit**: `git commit -m "refactor(web): reword the placeholder comments, rebuild the bundle" -- internal/web/ui/src/app/panels.ts internal/web/ui/src/panels/index.ts internal/web/ui/src/app/commands.ts internal/web/dist`

Then run the story's gates (`bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d"'`, then the profile's three gates).

## Story 13: The rules and procedures for part 3

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `internal/web/README.md`
- **Interfaces:** none; uses every earlier story's names
- **Verify:** `CGO_ENABLED=0 go test ./tools/claudemd/`
- **Review:** light (docs, checked by the CLAUDE.md lint)
- **Form:** brief (docs)

Part 3 lands the last screens of the new app and teaches three rules the app's docs must carry: React's `style` prop may carry computed geometry (the cost box's and the host's flows' bars) though a `style` attribute in markup never may; eight screens are lazy chunks that `checkLazyViews` pins, and pairing stays in the entry; and no route draws a placeholder any more. This story records them in `internal/web/ui/CLAUDE.md`, adds to `docs/claude/web-layer.md` the procedures part 3 teaches (a lazy view's stem, a command off a session route and an `offline` one, the iPhone checklist as the procedure before cutover) and rewords its line on webshot's screens, which the `pair` and `add-to-home` screens make false, names them in the `web-layer.md` row of `docs/claude/INDEX.md`, and describes the part 3 screens in `internal/web/README.md`, dropping its placeholder text. The story is brief and all of it is prose: each task quotes the exact text it replaces and says what the new text must hold, and the developer writes the wording. `internal/web/CLAUDE.md` is not touched (no line of it names a placeholder). Nothing here holds a landmine marker. The only check is the CLAUDE.md lint (`TestRepo_ClaudeMDStructure`, in `./tools/claudemd/`), which fails on a path, a test name or a file-and-symbol reference that does not resolve in the finished tree, and on a line over 1,200 bytes or a file over its budget. Every name below is one the earlier stories leave in the tree.

### Task 13.1: `internal/web/ui/CLAUDE.md`

**Files:**
- Modify: `internal/web/ui/CLAUDE.md:8`, `internal/web/ui/CLAUDE.md:10`, `internal/web/ui/CLAUDE.md:12-13`

Behaviour: four edits, each the smallest span. Do not touch line 9: Story 4 has already amended its `offline` sentence to name `device.pair` beside `app.backToClassic`, and a later edit there would clash. Every line stays under 1,200 bytes (line 13 is 858 bytes now and gains about 140; check with `awk '{ if (length($0) > 1200) print NR": "length($0) }' internal/web/ui/CLAUDE.md`, which must print nothing).

- [ ] **Step 1: Edit line 8 (no placeholder).** In the sentence

```
The registries give every view its tab, error boundary and placeholder and every action its confirmation, toast and stale gate;
```

drop `and placeholder`, so it reads `...every view its tab and error boundary and every action its confirmation, toast and stale gate;` (Story 12 removes the `Placeholder` the registries drew for an unregistered panel, and `PanelHost` now draws nothing for one; a test pins that every `PanelId` has a panel). Change nothing else in the line.

- [ ] **Step 2: Edit line 10 (the `style` prop).** Between the sentence ending `a string put into the page renders whatever markup Claude's text holds.` and the sentence starting `Import a component's CSS statically`, add one sentence saying: React's `style` prop is not a `style` attribute, since it sets the property through the CSSOM, which the policy allows; so a bar's computed width or height (`Bars` in `internal/web/ui/src/panels/flow/parts.tsx` and the cost box's and the flows list's bars, which take exact percentages) goes through the prop, never through a `style=` string in markup and never through one class per step. Keep the bold head of the rule as it is (`Write no runtime <style>, no style attribute as markup ...`) and its **Enforced** sentence as it is: the real-policy webshot render catches a violation at run time.

- [ ] **Step 3: Edit line 12 (the budget rule's examples).** Replace `(flow, job, mockup)` with `(the flow views, job, mockup, new session, settings)`. Change nothing else in the line.

- [ ] **Step 4: Edit line 13 (the lazy-view rule).** Two spans.

Replace

```
the job view (`internal/web/ui/src/panels/job/JobBody.tsx`) and the mockup views (`internal/web/ui/src/panels/mockup/MockupBody.tsx`) are the pattern, and the conversation, info, changes and subagent views stay in the entry because a push opens the conversation.
```

with the same sentence made to say that the job view (`internal/web/ui/src/panels/job/JobBody.tsx`) is the pattern, and that the conversation, info, changes and subagent views and pairing stay in the entry because a push opens the conversation and an unpaired launch opens pairing.

Replace

```
**Enforced** for the job and mockup chunks by `TestDist_LazyViewsAreChunksOfTheirOwn` (`internal/web/static_budget_test.go`)
```

with `**Enforced**` for the chunks `checkLazyViews` lists (`JobBody`, `MockupBody`, `FlowBody`, `FlowStoryBody`, `FlowDocBody`, `FlowsBody`, `NewSessionBody` and `SettingsBody`), to which a new lazy view adds its stem, by `TestDist_LazyViewsAreChunksOfTheirOwn` (`internal/web/static_budget_test.go`). The rest of the line (`; another view imported statically is held only by ...`) stays.

- [ ] **Step 5: Run the lint.**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/`
Expected: `ok  github.com/aidan-bailey/loom/tools/claudemd`. A failure naming `internal/web/ui/CLAUDE.md` and a path or symbol means a name above is misspelt: fix it against the tree. Then run `go run ./tools/claudemd -v` and check the file's line is still well under 100 lines and 20,000 bytes.

Fails without: the lint's line budget: a line 13 grown past 1,200 bytes fails `TestRepo_ClaudeMDStructure` naming the line; a stem or path that does not exist (for instance `FlowStoryBody.tsx` mistyped in a path) fails it as an unresolved reference.

- [ ] **Step 6: Commit**

```bash
git commit -m "docs(web): the app's rules for the style prop, every lazy chunk and no placeholder" -- internal/web/ui/CLAUDE.md
```

### Task 13.2: `docs/claude/web-layer.md`

**Files:**
- Modify: `docs/claude/web-layer.md:33` (Adding a panel), `docs/claude/web-layer.md:37` (Adding a command), `docs/claude/web-layer.md:41` (Adding a read), `docs/claude/web-layer.md:65` (the webshot line), `docs/claude/web-layer.md:80-82` (Trying a change, then a new section after it)

Behaviour: guide lines have no length budget, so the edits may be as long as they need; each stays a clause or two inside its paragraph, except the new section.

- [ ] **Step 1: Adding a panel (line 33).** After the clause `and a Go test like `TestDist_LazyViewsAreChunksOfTheirOwn` (`internal/web/static_budget_test.go`) pins that the chunk exists and that `internal/web/dist/index.html` names it nowhere`, add: the chunk is pinned only once its module's stem (`FlowBody`, `NewSessionBody`, ...) is in the list `checkLazyViews` ranges over, so add the stem there and a case for it to the table of `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`, in the same commit as the panel; and a view the first load needs stays in the entry (the conversation, info, changes and subagent views, which a push opens, and pairing, which an unpaired launch opens). Then, at the end of the paragraph, add: `PanelHost` draws nothing for a panel id nothing registers, so `internal/web/ui/src/app/panels.test.ts` pins that every `PanelId` has a panel once `@/panels` is imported, and a new id without its `definePanel` fails it. Keep every other word of the paragraph.

- [ ] **Step 2: Adding a command (line 37).** Make two additions and keep the rest. First, after `offline` only for an action that must work without the daemon (the switch)`, widen the parenthesis to name pairing: `(the switch, and pairing, which runs before any snapshot exists)`. Second, add a sentence saying: a command off a session route (new session, turning push on, a device setting, quick replies, unpair, pair) has `ctx.session` undefined, so its `run` reads its args and the store and never the session, and its `when` reads `ctx.panel` (`device.pair` is `when` the panel is `pair`, `push.enable` `when` it is `settings`) rather than a session's status; and that `runCommand` reaches an unsheeted command's `run` in the tap's own task, nothing awaited before it, which is what lets `push.enable` call `Notification.requestPermission()` first, since iOS asks for a permission only inside a gesture. An `offline` command also runs while the snapshot is stale or absent, which every other command does not.

- [ ] **Step 3: Adding a read (line 41).** In the sentence listing the bounds, replace

```
`gcTime`, `KEEP_SESSIONS` and `KEEP_JOBS` (the least recently opened is evicted past them)
```

with the same text naming `KEEP_FLOWS` too: ``` `gcTime`, `KEEP_SESSIONS`, `KEEP_JOBS` and `KEEP_FLOWS` (the least recently opened is evicted past them)```. Add one sentence at the end of the paragraph saying that a read keyed on what the stream changes (the board, read again when the JSON of the session's `flows` changes, `flowKey`) or held for the page's lifetime (launch defaults, never read again when the stream comes back) states its trigger in its test, as the polled reads state their intervals.

- [ ] **Step 4: The webshot line (line 65).** Reword the one clause, since two screens are no longer an iPhone 15 Home Screen app over a paired canned loom. Replace

```
renders each screen in headless Chromium as an iPhone 15 Home Screen app, the owner's phone (393×852 points at @3x, safe-area insets 59 and 34, `navigator.standalone`, service workers bypassed), and writes
```

with the same clause followed, before `and writes`, by the two exceptions: `add-to-home` is shot as a browser tab (`navigator.standalone` false, so the app's Add to Home gate draws), and `pair` is shot against a canned loom that answers every `/api/` request 401, as `authed` answers an unpaired device, neither of those 401s counting as a miss. Leave the rest of the line (`-screens`, `-guides`, `-bare`, the console errors and misses it fails on, `apiRoutes`) as it is. The tail of the line says a route a new screen reads needs a fixture; add that a screen sends no write and no `GET /api/v1/push-key`, which the canned loom does not answer.

- [ ] **Step 5: The iPhone checklist.** Add a new section `## The iPhone checklist before cutover`, between `## Trying a change` and `## Traps`, and leave `## Trying a change`'s own list of steps as it is. The section says: the checklist is the owner's, on the owner's phone, once part 3 is merged and the daemon rebuilt, and passing it is what the owner decides cutover on (cutover itself is the owner's call, never an agent's, and the classic tree stays until then); a dev build for it runs through the `loom-dev` skill, never `./loom` or `loom serve stop` in a loom pane. Then a numbered list, each step one line: switch the installed classic app to the new one in place (Settings, "Try the new app"); pair a fresh install, seeing the Add to Home gate in a Safari tab first and its "Continue in the browser anyway", then typing a code that holds an O or a 0 and seeing it dropped; turn notifications on from Settings (the permission asked on the tap, then "Notifications are on."); receive a waiting and a finished push with the app open and with it closed; Open in Claude; reply, and run a slash command; answer a question and a permission; start a session from New session and see "Loading…" give way to its conversation; open a session's flow tab, a story and a document, and the host's flows; kill with its confirmation and see the stash notice; save quick replies and flip a notification toggle in Settings; unpair from Settings and be offered pairing; revoke a device from the TUI or `loom web devices` and see the phone fall back to pairing; switch back to classic, with the daemon down as well.

- [ ] **Step 6: Run the lint.**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/`
Expected: `ok  github.com/aidan-bailey/loom/tools/claudemd`. A failure naming `docs/claude/web-layer.md` is a path, test name or symbol that does not resolve (`KEEP_FLOWS`, `flowKey`, `panels.test.ts`, `checkLazyViews`): check each against the tree.

Fails without: the lint's reference check: a mistyped `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`, or `panels.test.ts` named before Story 12 has added it, fails `TestRepo_ClaudeMDStructure`.

- [ ] **Step 7: Commit**

```bash
git commit -m "docs(web): the part 3 procedures, webshot's two new screen modes and the iPhone checklist" -- docs/claude/web-layer.md
```

### Task 13.3: `docs/claude/INDEX.md`

**Files:**
- Modify: `docs/claude/INDEX.md:28`

Behaviour: the `web-layer.md` row names what the guide now teaches, by three small insertions in the row's description and nothing else. The row is 1,116 bytes, and `INDEX.md` has no line budget, but keep each insertion to a few words.

- [ ] **Step 1: Edit the row.** In the row beginning `| [`web-layer.md`](web-layer.md) |`:

- after ``a panel (a session view's tab `Mark`, `Strip` and `Head`, a lazy `Body`)`` insert `, whose stem joins `checkLazyViews`` inside the parenthesis, so it reads `..., a lazy `Body` whose stem joins `checkLazyViews`)`;
- after ``a command (a sheet's field, a `key`, a `confirm` that returns null)`` insert `, one off a session route or `offline`` inside the parenthesis;
- replace `trying it on a phone;` with `trying it on a phone, and the iPhone checklist before cutover;`.

- [ ] **Step 2: Run the lint.**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/`
Expected: `ok  github.com/aidan-bailey/loom/tools/claudemd`.

Fails without: the lint's index check: a row edited so that `web-layer.md` is no longer linked from the index fails `TestRepo_ClaudeMDStructure` as an orphan guide.

- [ ] **Step 3: Commit**

```bash
git commit -m "docs(claude): name part 3's procedures in the web-layer row" -- docs/claude/INDEX.md
```

### Task 13.4: `internal/web/README.md`

**Files:**
- Modify: `internal/web/README.md:89` (The new app and the switch), and insert a new section before `## Push` (after The new app's session views)

Behaviour: the README holds facts, in the file's own present-tense style, with names that resolve (the lint checks paths and test names).

- [ ] **Step 1: Drop the placeholder text in line 89.** Two spans in the paragraph `The new app lives in ...`.

Replace

```
the new app's "Back to classic" (on every placeholder)
```

with the new app's "Back to classic" (in its Settings, and under the splash it draws before it has a snapshot, on every route, so the switch works with the daemon down and nothing saved).

Replace

```
The new app draws the fleet and the session views below; the flow screens, the flows list, new session, settings and pairing draw a placeholder that offers the switch back.
```

with a sentence saying the new app draws every screen the classic one does: the fleet and the session views, the part 3 screens in the section after them, and pairing, and that no route draws a placeholder. Leave the rest of the paragraph (the stack, the build, `firstLoadBudget`) as it is.

- [ ] **Step 2: Add the section `## The new app's other screens`** before `## Push`, in the file's prose style (one or two paragraphs, no table). It states these facts, each from the code part 3 leaves, and no others:

- The panels and their hash addresses: `flow` (`#/s/<id>/flow?plan=`), `flowStory` (`#/s/<id>/flow/<story>?plan=`), `flowDoc` (`#/s/<id>/flow/doc/<doc>?plan=&at=`), `flows` (`#/flows`), `new` (`#/new`), `settings` (`#/settings`) and `pair` (`#/pair`); the classic `#s/<id>/flow...`, `#flows`, `#new`, `#settings` and `#pair` addresses are rewritten to them. The flow tab is the fifth of a session that has flows, with an amber dot while any of its flows asks, and links to the board of the newest flow when no plan is given.
- The flow reads (`internal/web/ui/src/api/reads.ts`): the board is read on open and again whenever the JSON of the session's `flows` changes, and is held for the last 8 per session and plan (`KEEP_FLOWS`); a document is read on open and when the session, plan or document changes; the host's flows are read on open and on the head's reload; none polls. Launch defaults are read once per workspace once the stream is live and held for the page's lifetime. The cost box's and the flows list's bars take exact widths and heights through React's `style` prop.
- New session: the form's workspace, title, prompt (at most 64 KiB once trimmed) and "Launch options"; the create body carries an account, model, permission mode or effort only where the pick differs from the default; a 201 opens the session, which draws "Loading…" while it is pending (`PENDING_MS`, 5 s, cleared sooner once the snapshot lists it: `internal/web/ui/src/store/pending.ts`), a 202 toasts that the session is still starting and opens the fleet, and a refusal keeps the form.
- Settings: the push control (`internal/web/ui/src/app/push.ts`, whose states are loading, unsupported, denied, on, again and off, and `push.enable` in `internal/web/ui/src/commands/push.ts`, which asks for the permission first, in the tap), the two notification switches and the pause row, quick replies, This phone (with Unpair) and Back to classic, and the recent notices, which the store keeps to the last 20 (`MAX_NOTICES` in `internal/web/ui/src/store/fleet.ts`). A device or quick-replies write applies its answer to the store as the stream event would.
- Pairing and Add to Home: the pairing code is 8 characters of the pairing alphabet in two groups of four, anything else dropped as it is typed or pasted, and Pair is enabled at 8 with no auto-submit; `device.pair` (`internal/web/ui/src/commands/device.ts`) is the one `offline` command besides the switch and opens the fleet once the daemon accepts the code. An iPhone browser tab that is not the Home Screen app and has not chosen the browser (`localStorage`'s `loom.browser`, the classic key) draws only the Add to Home page (`internal/web/ui/src/app/install.tsx`) and starts nothing until "Continue in the browser anyway".
- The chunks: the flow board, story and document views, the host's flows, new session and settings are chunks loaded on demand beside the job and mockup views (`TestDist_LazyViewsAreChunksOfTheirOwn`), and pairing stays in the entry; webshot's canned loom (`tools/webshot/fixture/`) holds the second flow, final row, device and notices these screens draw, and shoots `pair` unpaired and `add-to-home` as a browser tab.

Do not edit the sentence in `## The new app's session views` that says the job and mockup views' bodies are chunks (it stays true), and do not add a file to the Files table.

- [ ] **Step 3: Run the lint.**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/`
Expected: `ok  github.com/aidan-bailey/loom/tools/claudemd`. A failure naming `internal/web/README.md` is a path or test name that does not resolve: check it against the tree.

Fails without: the lint's README check: a path named in the new section that Story 5, 4 or 11 did not create (`internal/web/ui/src/app/install.tsx` mistyped) fails `TestRepo_ClaudeMDStructure` as an unresolved path.

- [ ] **Step 4: Commit**

```bash
git commit -m "docs(web): describe the new app's part 3 screens and drop the placeholder text" -- internal/web/README.md
```
