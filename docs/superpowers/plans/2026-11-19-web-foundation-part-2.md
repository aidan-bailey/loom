# Loom web foundation, part 2: the session views — Implementation Plan

> **For agentic workers:** this plan runs supervised: `/flow:draft` writes the stories from this skeleton, then `/flow:run` runs them story by story. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the classic app's session views to the new app, with their classic behaviour: the conversation (turns, thinking, agent links, fact strip, needs-you box, asks, reply box with quick replies and typed slash commands), info (tiles, agents, jobs, actions and their sheets), changes, the subagent view, the job view and the mockup views, every action a command.

**Architecture:** Every view is a panel on part 1's registry and every action a command on its command registry, which this part widens (a sheet with one input field, a per-command in-flight key, a confirmation a command may skip). Reads go through TanStack Query in one module (`src/api/reads.ts`) that holds the classic refresh rules and caches: turn pages merged by `before` and `after`, kept with their scroll position for the last 8 conversations, job logs kept for the last 5 jobs. Claude's text is drawn from its `MdNode` tree as elements. A session's frame (head, tabs, fact strip, seen times and the fleet's NEW mark) is shared by its panels; the job and mockup views load as chunks of their own. No Go route, wire or Core change: the Go side gains only fixture data and a test of the chunks.

**Tech Stack:** TypeScript, React 19, Vite, Tailwind CSS 4, shadcn/ui on Base UI, TanStack Router 1 and Query 5, Zustand 5, lucide-react, Vitest with happy-dom and Testing Library; Go (testify) for the bundle and fixture tests; `tools/webshot` (headless Chromium).

**Spec:** `docs/superpowers/specs/2026-11-18-web-foundation-design.md`

**Part:** 2 of 3

**Budget:** draft $67; run $41 (estimate 2026-11-19)

## File structure

The new app (`internal/web/ui/src/`):

- `app/commands.ts`, `app/ConfirmSheet.tsx`, `app/CommandButton.tsx` (changed): the sheet's field, `key`, a `confirm` that may return null, `run`'s input.
- `app/panels.ts`, `app/shell.tsx` (changed): a panel's `Strip` and its tab's `Mark`; the subagent and job views' tabs; a lazy `Body`'s `Suspense`.
- `app/routes.tsx` (changed): `AGENT_ID` exported.
- `components/ui/textarea.tsx` (new); `lib/utf8.ts` (new).
- `store/drafts.ts` (new); `store/fleet.ts` (changed: unpair clears the drafts).
- `commands/index.ts`, `commands/session.ts`, `commands/asks.ts`, `commands/jobs.ts` (new); `main.tsx` (changed: imports them).
- `api/reads.ts` (new): turn pages, the diff and job logs.
- `conversation/Markdown.tsx`, `conversation/TurnList.tsx`, `conversation/NeedsYou.tsx`, `conversation/Asks.tsx`, `conversation/ReplyBox.tsx` (new).
- `session/SessionHead.tsx`, `session/FactStrip.tsx`, `session/SubLine.tsx`, `session/NoSession.tsx`, `session/facts.ts`, `session/jobs.ts` (new).
- `fleet/seen.ts` (new); `panels/fleet/SessionRow.tsx`, `panels/fleet/index.tsx` (changed: the NEW mark).
- `panels/turns/`, `panels/info/`, `panels/changes/`, `panels/agent/`, `panels/job/`, `panels/mockup/` (new); `panels/index.ts` (changed).
- `fixtures.ts`, `fixtures.test.ts` (changed).
- `*.test.ts(x)` beside each module they test.

The built bundle: `internal/web/dist/` (rebuilt by every story that changes the app, committed).

Elsewhere: `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/turns.json`, `tools/webshot/fixture/diff.json`, `tools/webshot/webshot_test.go`, `internal/web/webshot_fixture_test.go`; `internal/web/static_budget_test.go`; `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `internal/web/README.md`.

## Names contract

Every name one story defines and another uses. A story may add names of its own; a name another story needs and this section lacks goes back to the manager. Paths are under `internal/web/ui/src/` unless they start at the repo root.

**webshot's fixtures (Story 1):**

- `tools/webshot/fixture/snapshot.json` (Story 1): session 2 (needs_you, Prompting, Claude, with a `remote_url`) holds two asks, a permission ask with `id` `"fixture-permission"` (tool `Bash`, `input` a mono `command` block and a `description` block, `suggestions` one with `persists` true and one without) and a question ask with `id` `"fixture-question"` asked by subagent `a1f00dcafe0001` (a single-select question with a `preview` on one option, and a multi-select question); session 1 gains `worktree`, `tmux` and a queued job with `id` 216 and a `waiting` text; every other session keeps `asks` empty.
- `tools/webshot/fixture/turns.json` (Story 1): adds a `note` item (one plain, one with `error`), a step whose `state` is `interrupted`, and a text item whose `blocks` hold `h`, `ul`, `ol`, `li`, `quote`, `code`, `em`, `strong`, `a`, `hr` and `table` (`tr`, `th`, `td`) nodes; the agent step, the thinking item and every id the existing webshot tests read stay as they are.
- `tools/webshot/fixture/diff.json` (Story 1): adds a file with `status` `D` and a file with `omitted` `lines`.
- `function fixtureTurns(agent?: boolean): Turns` (Story 1), in `fixtures.ts`: `turns.json`, or `agent-turns.json` when `agent`, with its relative times filled as `fixtureSnapshot` fills them.
- `function fixturePatch(): Patch` (Story 1), in `fixtures.ts`: `diff.json`.
- `function fixtureJobLog(after?: boolean): JobLog` (Story 1), in `fixtures.ts`: `job-log.json`, or `job-log-after.json` when `after`.

**Commands and the sheet, widened (Story 2):**

- `interface Command<A = void> { id: string, title: string | ((ctx: CommandContext) => string), icon?: LucideIcon, offline?: boolean, key?: (args: A) => string, when: (ctx: CommandContext) => boolean, confirm?: (ctx: CommandContext, args: A) => ConfirmSpec | null, run: (ctx: CommandContext, args: A, input: string) => Promise<void> }` (Story 2), in `app/commands.ts`, replacing part 1's. The in-flight key is the command's id, the session's id and what `key` returns for the args (`""` without `key`); a `confirm` that returns null runs `run` with `input` `""` and no sheet; `input` is the sheet's field value (`""` with no field).
- `function useCommandBusy<A>(command: Command<A>, ctx: CommandContext, args: A): boolean` (Story 2), replacing part 1's two-argument form; `CommandButton` passes its `args`.
- `interface ConfirmSpec { title: string, body: string, action: string, destructive: boolean, field?: ConfirmField }` (Story 2), in `app/ConfirmSheet.tsx`.
- `type ConfirmField = TextField | ChoiceField` (Story 2).
- `interface TextField { kind: "text", label: string, placeholder: string, max: number, tooLong: string }` (Story 2); `max` in UTF-8 bytes; past it the action is disabled and `tooLong` shows.
- `interface ChoiceField { kind: "choice", label: string, options: ChoiceOption[] }` (Story 2), drawn as a native `<select>` at 16px, its first option chosen.
- `interface ChoiceOption { value: string, label: string }` (Story 2).
- `function askConfirm(spec: ConfirmSpec): Promise<string | null>` (Story 2), replacing part 1's boolean form: the field's value (a text field's as typed, `""` with no field) when the action is chosen, null when the sheet is cancelled, dismissed or replaced by a newer ask.
- `function Textarea(props: ComponentProps<"textarea">): ReactElement` (Story 2), in `components/ui/textarea.tsx`: shadcn's Textarea on a plain `<textarea>`, text at 16px, growing with its content (`field-sizing: content`) to at most 120px.
- `function utf8Length(text: string): number` and `const MAX_MESSAGE_BYTES = 65536` (Story 2), in `lib/utf8.ts`: the limit the daemon holds a reply, an answer's text and a message to.

**Drafts and the session, ask and job commands (Story 3):**

- `interface QuestionDraft { labels: string[], other: string, note: string }` (Story 3), in `store/drafts.ts`, `labels` in the order they were picked.
- `interface AskDraft { answers: Record<string, QuestionDraft> }` (Story 3), keyed by the question's text.
- `interface DraftsState { replies: Record<number, string>, asks: Record<string, AskDraft>, setReply: (session: number, text: string) => void, setAsk: (ask: string, draft: AskDraft) => void, dropAsk: (ask: string) => void, clear: () => void }` (Story 3); `setReply` with `""` deletes the session's entry.
- `const useDrafts: UseBoundStore<StoreApi<DraftsState>>` (Story 3); `startFleet`'s unpaired handler in `store/fleet.ts` calls its `clear()` beside `queryClient.clear()`.
- `interface SessionActions { pause: boolean, resume: boolean, push: boolean, merge: boolean, send: boolean, diff: boolean, kill: boolean }` (Story 3), in `commands/session.ts`.
- `function sessionActions(session: Session): SessionActions` (Story 3): spec Part 2 settlement 2's rules; every session command's `when` is `ctx.session !== undefined` and its entry here.
- The session commands (Story 3), in `commands/session.ts`, each with its classic sheet and words (spec Part 2 settlement 8): `session.send` with args `{ text: string }` (no sheet; the trimmed text to `api.send`; a 200 clears the session's reply draft), `session.pause`, `session.resume`, `session.push` (sheet), `session.merge` (a sheet whose `ChoiceField` lists the other sessions of the session's workspace by `"<title> (<branch>)"`, valued by id; null and an error toast when there are none), `session.kill` (a destructive sheet; a 200 opens the fleet with `window.location.assign(hrefFor("fleet"))`), each of the last five with args `void`.
- The ask commands (Story 3), in `commands/asks.ts`, each keyed by its ask's id: `ask.answer` with args `{ ask: string, body: AnswerBody }` (a 200 drops the ask's draft), `ask.chat` with args `{ ask: string }` (a sheet with a `TextField`; body `{ chat: true, chat_text: <trimmed input> }`), `ask.allow` with args `{ ask: string, suggestion: number, label: string, persists: boolean }` (`suggestion` 0 for the plain Allow, else 1-based; a "Save this rule?" sheet only when `persists`; body `{ allow: true }` plus `suggestion` when above 0), `ask.deny` with args `{ ask: string }` (a destructive sheet with a `TextField`; body `{ allow: false, message: <trimmed input> }`).
- The job commands (Story 3), in `commands/jobs.ts`, each with args `{ job: number }`, keyed by the job's id, calling `api.jobControl(session, job, <action>)`: `job.start` (the "Start job N now?" sheet, its body naming the job's `waiting` when set), `job.park`, `job.unpark`, `job.pause`, `job.resume`.
- `commands/index.ts` (Story 3): imports the three modules for their registration; `main.tsx` imports `@/commands`, and a test that draws a `CommandButton` imports it too.

**Reads (Story 4)**, in `api/reads.ts`:

- `type TurnsLoad = "newest" | "refresh" | "earlier"` (Story 4).
- `interface HeldTurns { conversation: string, turns: Turn[], earlier: number | null, skipped: number, agent: AgentPage | undefined }` (Story 4).
- `function mergeTurns(held: HeldTurns | undefined, page: Turns, load: TurnsLoad): HeldTurns` (Story 4): the classic `mergeTurns` (`internal/web/static/js/turns.mjs`).
- `interface TurnPagesOptions { session: number, agent: string | undefined, live: boolean }` (Story 4).
- `interface TurnPages { held: HeldTurns | undefined, error: string, loading: boolean, load: (kind: TurnsLoad) => void }` (Story 4).
- `function useTurnPages(options: TurnPagesOptions): TurnPages` (Story 4): on mount a `refresh` when pages are held, else `newest`; a `refresh` every 5 s while `live`; one request in flight, a load asked meanwhile run once when it lands; held in the query cache, so two components with the same `session` and `agent` share one held state and one poll.
- `function useHeldTurns(session: number, agent: string | undefined): HeldTurns | undefined` (Story 4): the held pages, read without fetching (the subagent head reads its page's `agent` through it).
- `function keptScroll(session: number): number | undefined` and `function keepScroll(session: number, y: number): void` (Story 4): the page scroll kept with a conversation's pages.
- `const KEEP_SESSIONS = 8`, `const KEEP_JOBS = 5`, `const MAX_LOG_CHARS = 524288` (Story 4): a ninth conversation's pages evict the least recently opened one's (its pages and scroll); a sixth job's log evicts likewise.
- `interface PatchOptions { session: number, signature: string, live: boolean }` (Story 4).
- `interface PatchView { patch: Patch | undefined, error: string, loading: boolean, reload: () => void }` (Story 4).
- `function usePatch(options: PatchOptions): PatchView` (Story 4): reads on mount, again when `signature` changes once a patch is held, and every 10 s while `live`.
- `interface HeldLog { text: string, end: number, cut: boolean }` (Story 4).
- `function appendLog(held: HeldLog | undefined, page: JobLog): HeldLog` (Story 4): the classic `appended` (`internal/web/static/js/job.mjs`), capped at `MAX_LOG_CHARS`.
- `interface JobLogOptions { session: number, job: number, live: boolean }` (Story 4).
- `interface JobLogView { log: HeldLog | undefined, gone: string, error: string, loading: boolean }` (Story 4).
- `function useJobLog(options: JobLogOptions): JobLogView` (Story 4): the tail on mount (the kept log drawn first), then `after` every 2 s while `live` and not `gone`, and once more when `live` turns false; a 409 sets `gone` and stops.

**Claude's text and the turns (Story 5):**

- `function Markdown(props: { blocks: (MdNode | null)[] }): ReactElement` (Story 5), in `conversation/Markdown.tsx`.
- `function TurnList(props: { turns: Turn[], sessionId: number, live: boolean, remote: string }): ReactElement` (Story 5), in `conversation/TurnList.tsx`: `live` marks the newest turn working; `remote` is the session's `remote_url` (`""` on the subagent view).
- `function turnLabel(turn: Turn): string` and `function clock(iso: string): string` (Story 5), in `conversation/TurnList.tsx`.
- `const AGENT_ID: RegExp` (Story 5), in `app/routes.tsx`: today's private constant, exported.

**The session's frame and seen times (Story 6):**

- `interface PanelTab { order: number, label: string, Mark?: ComponentType<{ session: Session }> }` (Story 6), in `app/panels.ts`; `SessionTabs` draws `Mark` after the label.
- `interface Panel { id: PanelId, title: (props: PanelProps) => string, appliesTo?: (session: Session) => boolean, tab?: PanelTab, Head?: ComponentType<PanelProps>, Strip?: ComponentType<PanelProps>, Body: ComponentType<PanelProps> }` (Story 6), replacing part 1's; `AppShell` draws `Strip` under the session's tabs, inside the header and its own error boundary.
- The tabs a route lights (Story 6), in `app/shell.tsx`: `agent` lights `turns` and `job` lights `info`, both now drawing the session's tabs; `turns` lights `info` for a session whose `claude` is false, whose `turns` tab is then never added.
- `function SessionHead(props: PanelProps): ReactElement` (Story 6), in `session/SessionHead.tsx`: the context row back to the fleet with the session's title, and `claude ↗` to its `remote_url` unless the needs-you box is on screen (the `turns` route of a Claude session in group needs_you).
- `function FactStrip(props: PanelProps): ReactElement` (Story 6), in `session/FactStrip.tsx`: the classic `FactStrip` on the `turns` route of a Claude session, nothing otherwise.
- `function SubLine(props: { session: Session }): ReactElement` (Story 6), in `session/SubLine.tsx`.
- `function NoSession(): ReactElement` (Story 6), in `session/NoSession.tsx`: "This session is gone.", which a session panel's `Body` draws when the snapshot lists no such session.
- `function parityText(session: Session): string`, `function checksText(pr: Pr): string` and `function groupGlyph(group: string): string` (Story 6), in `session/facts.ts`.
- `const SEEN_KEY = "loom.seen"` (Story 6), in `fleet/seen.ts`, stored in the classic encoding (spec Part 2 settlement 12).
- `function readSeen(): Record<string, number>`, `function markSeen(id: number): void`, `function pruneSeen(ids: number[]): void`, `function hasNewTurn(session: Session, seen: Record<string, number>): boolean` (Story 6), in `fleet/seen.ts`.
- `function useMarkSeen(id: number | undefined): void` (Story 6), in `fleet/seen.ts`: marks the session seen on mount and on unmount.

**What the session asks, and the reply (Story 7):**

- `function NeedsYou(props: { session: Session }): ReactElement` (Story 7), in `conversation/NeedsYou.tsx`.
- `function Asks(props: { session: Session }): ReactElement` (Story 7), in `conversation/Asks.tsx`: a card per ask in `session.asks`.
- `function ReplyBox(props: { session: Session }): ReactElement` (Story 7), in `conversation/ReplyBox.tsx`: the box while `sessionActions` gives `send`, else "Resume the session to reply." while the session's group is paused, else nothing.

**The panels (Stories 8 to 12)**, each registered from `panels/<id>/index.tsx` and imported by `panels/index.ts`:

- The `turns` panel (Story 8): `{ id: "turns", appliesTo: (s: Session) => s.claude, tab: { order: 1, label: "conversation" }, Head: SessionHead, Strip: FactStrip }`; its `Body` draws the `info` panel's `Body` (through `panelFor("info")`) for a session whose `claude` is false.
- `function Conversation(props: { session: Session }): ReactElement` (Story 8), in `panels/turns/Conversation.tsx`.
- The `info` panel (Story 9): `{ id: "info", tab: { order: 3, label: "info" }, Head: SessionHead }`.
- `function jobGlyph(job: Job): string`, `function elapsed(from: number, to: number): string` (milliseconds), `function jobEnd(job: Job): string` and `function jobLine(job: Job, now: number): string` (Story 9), in `session/jobs.ts`.
- The `changes` panel (Story 10): `{ id: "changes", appliesTo: (s: Session) => sessionActions(s: Session).diff, tab: { order: 2, label: "changes", Mark: <the session's line counts while either is above 0> }, Head: SessionHead }`.
- The `agent` panel (Story 11): `{ id: "agent", Head: AgentHead, Strip: AgentStrip }`, no tab.
- The `job` panel (Story 12): `{ id: "job", Head: JobHead }`, its `Body` `React.lazy` over `panels/job/JobBody.tsx`.
- The `mockup` and `mockupFull` panels (Story 12): `mockup` is `{ id: "mockup", appliesTo: (s: Session) => s.mockup !== null, tab: { order: 4, label: "mockup", Mark: <a dot while the mockup is fresh> }, Head: SessionHead }`, `mockupFull` has no `Head`; both `Body`s are `React.lazy` over `panels/mockup/MockupBody.tsx`.
- `PanelHost` (Story 12), in `app/shell.tsx`: wraps `Body` in `Suspense` with a `Skeleton` fallback, inside its error boundary.
- `TestDist_LazyViewsAreChunksOfTheirOwn` (Story 12), in `internal/web/static_budget_test.go`: `internal/web/dist/assets/` holds a `JobBody-*.js` and a `MockupBody-*.js`, and `internal/web/dist/index.html` names neither.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): Story 2's `runCommand` and `askConfirm` (Story 3); Story 6's `AppShell`, `tabOf` and `PanelHost` in `app/shell.tsx` (Story 12); Story 6's `panels/fleet/index.tsx` (none later); `panels/index.ts` as each panel story leaves it (the next one).

## Stories

- **Story 1** — webshot's fixtures carry the asks, items, steps, nodes, jobs and files the session views draw.

  Add to the canned loom's fixtures what the session screens draw but no fixture holds (spec Part 2 settlement 17), so both apps' screenshots show the asks, every item kind, the markdown nodes, a queued job and a deleted and an omitted file, and Vitest can load each fixture typed. Every fixture still decodes into its Go type (`internal/web/webshot_fixture_test.go`), the new union-typed fields gain their rows in `fixtures.test.ts`, the existing webshot fixture tests keep passing unchanged, and the classic app still renders every screen without a console error.

  - **Developer:** intermediate-dev
  - **Files:** `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/turns.json`, `tools/webshot/fixture/diff.json`, `tools/webshot/webshot_test.go`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/src/fixtures.ts`, `internal/web/ui/src/fixtures.test.ts`
  - **Interfaces:** exposes the fixtures' asks, items, jobs and files, `fixtureTurns`, `fixturePatch`, `fixtureJobLog`; uses part 1's `fillTimes` and `fixtureSnapshot`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app classic -screens conversation,waiting,info,diff -out "$d"'`
  - **Review:** light (fixture data; the Go decode test and the typed fixture test check its shapes)
  - **Form:** brief (fixture data and their coverage rows)
  - **Reading:** spec Testing (typed fixtures), Part 2 settlements (webshot, 17); `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/turns.json`, `tools/webshot/fixture/diff.json`, `internal/web/asks.go:30-151`, `internal/web/markdown.go:140-225`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/src/fixtures.ts`, `internal/web/ui/src/fixtures.test.ts`, `tools/webshot/webshot_test.go` (its `TestFixture_` and `TestCanned_` tests)

- **Story 2** — The command path takes an input field, a key of its own and a confirmation it may skip.

  Widen part 1's command registry for part 2's actions (spec Part 2 settlements 6 and 7): the confirmation sheet draws an optional text field (a `Textarea`, its UTF-8 limit disabling the action past it) or a native-select choice, `askConfirm` resolves the field's value or null, a command's `confirm` may return null to run without a sheet, `run` receives the value, and a command's `key` (`(args: A) => string`) joins its in-flight key so commands on two jobs or two asks don't block each other. The freshness check after a yes, the stale gate and the error toast stay as they are. Add shadcn's `Textarea` and the shared UTF-8 length. Update part 1's command and sheet tests to the new signatures, keeping what they pin.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/app/commands.test.ts`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/app/CommandButton.tsx`, `internal/web/ui/src/components/ui/textarea.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/lib/utf8.ts`, `internal/web/ui/src/lib/utf8.test.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the widened `Command`, `useCommandBusy`, `ConfirmSpec`, `ConfirmField`, `TextField`, `ChoiceField`, `ChoiceOption`, `askConfirm`, `Textarea`, `utf8Length`, `MAX_MESSAGE_BYTES`; uses part 1's registry, `freshContext`, `showMessage` and `Drawer`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (the confirmation and in-flight path every destructive command runs through: a wrong key runs Kill twice, a wrong null runs it unasked, a value read before the freshness check acts on a stale session)
  - **Reading:** spec §23, Part 2 settlements (Commands, 6, 7, 18); `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/CommandButton.tsx`, `internal/web/ui/src/app/commands.test.ts`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/components/ui/drawer.tsx`, `internal/web/static/js/sheets.mjs`

- **Story 3** — The drafts slice, and the session, ask and job commands.

  Add the in-memory drafts slice (a reply per session, an answer per ask, cleared on unpair; spec Part 2 settlement 3) and register every part 2 action as a command (§23 as settled): send, pause, resume, push, merge and kill on a session, with the classic sheets and words (settlement 8) and the classic availability as `sessionActions` (settlement 2); answer, chat, allow and deny on an ask; start, park, unpark, pause and resume on a job. Each `run` calls its `api` function and hands the outcome to `showOutcome`; send and answer clear their draft only on a 200; Kill opens the fleet only on a 200. Vitest covers each command's `when` across statuses, groups and link states, each sheet naming what it does (Kill's tmux session, worktree and branch, and its paused paragraph only for a Paused session), the bodies each sends, and the drafts.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/store/drafts.ts`, `internal/web/ui/src/store/drafts.test.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/startFleet.test.ts`, `internal/web/ui/src/commands/index.ts`, `internal/web/ui/src/commands/session.ts`, `internal/web/ui/src/commands/session.test.ts`, `internal/web/ui/src/commands/asks.ts`, `internal/web/ui/src/commands/asks.test.ts`, `internal/web/ui/src/commands/jobs.ts`, `internal/web/ui/src/commands/jobs.test.ts`, `internal/web/ui/src/main.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `QuestionDraft`, `AskDraft`, `DraftsState`, `useDrafts`, `SessionActions`, `sessionActions`, and the commands `session.send`, `session.pause`, `session.resume`, `session.push`, `session.merge`, `session.kill`, `ask.answer`, `ask.chat`, `ask.allow`, `ask.deny`, `job.start`, `job.park`, `job.unpark`, `job.pause`, `job.resume`; uses Story 2's command path and fields, part 1's `api`, `showOutcome`, `showMessage`, `hrefFor`, `allSessions` and `startFleet`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (command definitions over Story 2's path and the generated client; the sheets' words and the bodies are listed in the spec's settlements and pinned by named tests)
  - **Reading:** spec §20, §23, Part 2 settlements 2, 3, 6, 7, 8; `internal/web/static/js/session.mjs` (`actionsFor`, `mergeCandidates`, the sheets in `Info`), `internal/web/static/js/asks.mjs`, `internal/web/static/js/reply.mjs`, `internal/web/static/js/job.mjs:86-112` and its start sheet, `internal/web/ui/src/app/toasts.ts`, `internal/web/ui/src/store/fleet.ts:200-290`, `internal/web/ui/src/api/gen.ts:61-101`, `internal/web/ui/src/api/gen.ts:620-646`
  - **Drafts after:** Story 2

- **Story 4** — The reads: turn pages, the diff and job logs, with their refresh rules and kept caches.

  Add `src/api/reads.ts`, where every part 2 read goes through TanStack Query with the classic rules (spec §26, Part 2 settlements 11 and 15): turn pages for a conversation or a subagent, paged back by `before` and refreshed by `after` and merged as the classic `mergeTurns` does, one request in flight, kept with the page's scroll for the last 8 conversations; the diff, read again on a signature change and every 10 s while live; job logs appended by `after` up to 512 Ki characters, polled every 2 s while live and once more when they end, gone on a 409, kept for the last 5 jobs. Vitest with fake timers covers the merge cases, the cadences, the single flight, the evictions, the cap and the 409.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/api/reads.ts`, `internal/web/ui/src/api/reads.test.ts`, `internal/web/dist/`
  - **Interfaces:** exposes `TurnsLoad`, `HeldTurns`, `mergeTurns`, `TurnPagesOptions`, `TurnPages`, `useTurnPages`, `useHeldTurns`, `keptScroll`, `keepScroll`, `KEEP_SESSIONS`, `KEEP_JOBS`, `MAX_LOG_CHARS`, `PatchOptions`, `PatchView`, `usePatch`, `HeldLog`, `appendLog`, `JobLogOptions`, `JobLogView`, `useJobLog`; uses part 1's `api`, `queryClient`, `unwrap` and `OutcomeError`, and Story 1's fixture loaders
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (the bounded caches, the log cap and the single-flight merge every conversation and log depends on; a wrong merge loses or repeats turns silently)
  - **Reading:** spec §26, Part 2 settlements (Store and reads, 10, 11, 15); `internal/web/static/js/turns.mjs:17-53`, `internal/web/static/js/turns.mjs:205-310`, `internal/web/static/js/changes.mjs:72-150`, `internal/web/static/js/job.mjs:17-60`, `internal/web/static/js/job.mjs:114-195`, `internal/web/README.md` (The app, Limits), `internal/web/reads.go:20-130`, `internal/web/reads.go:253-310`, `internal/web/jobs.go:180-232`, `internal/web/ui/src/api/queries.ts`, `internal/web/ui/src/api/request.ts`

- **Story 5** — Claude's text from its tree, and the turn renderer.

  Draw an `MdNode` tree as elements and text nodes, never markup from a string (spec §33): every node kind the Go tree emits, a link only to its `href` in a new tab with `noopener noreferrer`, an unknown node as a span of its children, a null node as nothing. On it build the classic turn list (`turns.mjs`): each turn's label by `from`, its prompt folded past 12 lines, its partial and cut forms, and the Claude side's items by kind (text with its "continues in Claude" link, thinking and step folds as `Collapsible`s, open on the live newest turn, steps with their agent links to the subagent view, compacted, note, and an unknown kind in neutral styling, spec Part 2 settlement 9). Vitest covers every node and item kind over the fixture's turns, and that text holding markup renders as text.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/conversation/Markdown.tsx`, `internal/web/ui/src/conversation/Markdown.test.tsx`, `internal/web/ui/src/conversation/TurnList.tsx`, `internal/web/ui/src/conversation/TurnList.test.tsx`, `internal/web/ui/src/app/routes.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `Markdown`, `TurnList`, `turnLabel`, `clock`, `AGENT_ID`; uses part 1's `Collapsible`, `hrefFor` and `ago`, and Story 1's `fixtureTurns`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (rendering a tree the daemon builds and bounds; the no-markup rule is pinned by a named test)
  - **Reading:** spec §31, §33, Part 2 settlements 5, 9; `internal/web/static/js/markdown.mjs`, `internal/web/static/js/turns.mjs:55-201`, `internal/web/markdown.go:1-70`, `internal/web/markdown.go:140-270`, `internal/web/ui/src/components/ui/collapsible.tsx`, `internal/web/ui/src/app/routes.tsx:18-30`, `internal/web/ui/src/panels/fleet/SessionRow.tsx:1-40`

- **Story 6** — A session's frame: its head, tabs, fact strip, seen times and the fleet's NEW mark.

  Give every session view the classic frame (spec §22, Part 2 settlements 12 and 16): a panel's `Strip` drawn under the tabs and a tab's `Mark` after its label; the subagent and job views drawing the session's tabs and lighting the conversation and info tabs; a non-Claude session's `#s/<id>` lighting info; `SessionHead` (back to the fleet, the title, `claude ↗`), `FactStrip`, `SubLine`, `NoSession` and the facts' helpers. Add the seen times in the classic `loom.seen` encoding, the hook that marks a session seen on opening and closing, and the fleet row's NEW mark, the fleet pruning the times of sessions the daemon no longer lists. Vitest covers the tab rules, the strip's facts and links, and the seen encoding against a value the classic `pref` wrote.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/session/SessionHead.tsx`, `internal/web/ui/src/session/FactStrip.tsx`, `internal/web/ui/src/session/SubLine.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/session/facts.ts`, `internal/web/ui/src/session/session.test.tsx`, `internal/web/ui/src/fleet/seen.ts`, `internal/web/ui/src/fleet/seen.test.ts`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/panels/fleet/fleet.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `PanelTab`, the widened `Panel`, the tab rules, `SessionHead`, `FactStrip`, `SubLine`, `NoSession`, `parityText`, `checksText`, `groupGlyph`, `SEEN_KEY`, `readSeen`, `markSeen`, `pruneSeen`, `hasNewTurn`, `useMarkSeen`; uses part 1's `ContextRow`, `SessionTabs`, `sessionTabs`, `rowShare`, `ago` and `hrefFor`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens fleet -out "$d"'`
  - **Review:** deep
  - **Form:** brief (the shell's slots and a port of `session.mjs`'s head, `strip.mjs` and `fleet.mjs`'s seen times)
  - **Reading:** spec §22, §29, Part 2 settlements (Panels, The shell), 5, 12, 16; `internal/web/static/js/session.mjs:40-67`, `internal/web/static/js/session.mjs:340-360`, `internal/web/static/js/strip.mjs`, `internal/web/static/js/chrome.mjs:60-97`, `internal/web/static/js/fleet.mjs:70-142`, `internal/web/static/js/store.mjs:70-93`, `internal/web/static/js/skin.mjs`, `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/panels/fleet/index.tsx:1-40`, `internal/web/ui/src/fleet/usage.ts`

- **Story 7** — The needs-you box, the asks and the reply box.

  Port the classic `needs.mjs`, `asks.mjs` and `reply.mjs` onto Story 3's commands and drafts: the needs-you box (its reason, the wait's detail when there are no asks, the asks, Open in Claude as a link or "Remote Control is off for this session.", spec Part 2 settlement 5); the question form (options as radios or checkboxes, the preview of the last picked option that has one, Other, a note, Answer enabled once every question is answered within 64 KiB, Chat about this); the permission card (the input blocks, Allow, each suggestion, Deny); a card disabled while its ask is answering, an unknown kind's neutral card (settlement 9); and the reply box (a `Textarea` with the placeholder "reply, or /command…", quick-reply chips while it is empty, the 64 KiB limit, the draft kept across tab switches, settlement 1). Vitest covers the ask forms as spec Testing lists them and the reply draft across a remount.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/conversation/NeedsYou.tsx`, `internal/web/ui/src/conversation/Asks.tsx`, `internal/web/ui/src/conversation/Asks.test.tsx`, `internal/web/ui/src/conversation/ReplyBox.tsx`, `internal/web/ui/src/conversation/ReplyBox.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `NeedsYou`, `Asks`, `ReplyBox`; uses Story 2's `Textarea`, `utf8Length` and `MAX_MESSAGE_BYTES`, Story 3's `useDrafts`, `sessionActions` and the `session.send` and `ask.*` commands, part 1's `CommandButton`, `useFleet` and `Badge`, Story 1's `fixtureSnapshot` asks
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (forms over Story 3's commands, whose bodies the daemon validates and bounds; each form's rule is a named test)
  - **Reading:** spec §34, Testing (the ask forms, reply drafts), Part 2 settlements 1, 3, 5, 9, 13; `internal/web/static/js/needs.mjs`, `internal/web/static/js/asks.mjs`, `internal/web/static/js/reply.mjs`, `internal/web/static/app.css:400-420`, `internal/web/static/app.css:550-575`, `internal/web/asks.go:30-151`, `internal/web/README.md` (Answering an ask, if present), `docs/claude/web-layer.md` (Answering an ask)
  - **Drafts after:** Story 2, Story 3

- **Story 8** — The conversation panel.

  Register the `turns` panel (spec Part 2 settlement 16) and port the classic `Conversation`: the unreadable-records notice, `↑ earlier turns`, loading and error with retry, "No turns yet.", the turn list (live while the session is Running), the refresh error, the needs-you box while the group is needs_you, and the reply box. It refreshes when the stream changes the session's status or last message (not on mount) and every 5 s while Running (§26), anchors the page to the bottom on a first page, a reset or a refresh that lands near it (48 px), keeps the reader's place after an earlier page, restores a kept conversation's scroll before the first paint, and marks the session seen. A non-Claude session's `#s/<id>` draws info. Vitest covers the triggers and the drawn parts; webshot renders the conversation and waiting screens.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/turns/index.tsx`, `internal/web/ui/src/panels/turns/Conversation.tsx`, `internal/web/ui/src/panels/turns/turns.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the `turns` panel and `Conversation`; uses Story 4's `useTurnPages`, `keptScroll` and `keepScroll`, Story 5's `TurnList`, Story 6's `SessionHead`, `FactStrip`, `NoSession` and `useMarkSeen`, Story 7's `NeedsYou` and `ReplyBox`, part 1's `definePanel`, `panelFor` and `useSession`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens conversation,waiting -out "$d"'`
  - **Review:** deep
  - **Form:** brief (a port of `turns.mjs`'s `Conversation` over Story 4's reads)
  - **Reading:** spec §26, §27, Part 2 settlements 12, 13, 15, 16; `internal/web/static/js/turns.mjs:310-392`, `internal/web/README.md` (The app), `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/app/panels.ts`
  - **Drafts after:** Story 4, Story 5, Story 6, Story 7

- **Story 9** — The info panel.

  Register the `info` panel and port the classic `Info` (`session.mjs`): the sub-line; the Changes, PR and Account tiles (the account's shares, reset texts and staleness as the classic tile draws them); the agents, each linking to the subagent view when its id is a valid agent id; the jobs, each linking to the job view with its glyph and line, re-drawn every 5 s while one runs; the actions the session allows (`sessionActions`) as command buttons; the reply box; and "This session is gone." for a session the snapshot no longer lists. It marks the session seen. Add the job helpers the job view shares, with an unknown state's raw text (spec Part 2 settlement 9). Vitest covers each tile's forms, the rows' links and the actions shown per status and group; webshot renders the info screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/info/Info.tsx`, `internal/web/ui/src/panels/info/info.test.tsx`, `internal/web/ui/src/session/jobs.ts`, `internal/web/ui/src/session/jobs.test.ts`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the `info` panel, `jobGlyph`, `elapsed`, `jobEnd`, `jobLine`; uses Story 3's `sessionActions` and session commands, Story 5's `AGENT_ID`, Story 6's `SessionHead`, `SubLine`, `NoSession`, `parityText`, `checksText` and `useMarkSeen`, Story 7's `ReplyBox`, part 1's `CommandButton`, `rowShare` and `ago`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens info -out "$d"'`
  - **Review:** light (a port of `session.mjs`'s `Info`; its actions are Story 3's commands)
  - **Form:** brief (a port of `session.mjs`'s `Info`)
  - **Reading:** spec §29, §31, Part 2 settlements 2, 9, 12; `internal/web/static/js/session.mjs:70-337`, `internal/web/static/js/usage.mjs`, `internal/web/ui/src/fleet/usage.ts`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`
  - **Drafts after:** Story 3, Story 6, Story 7

- **Story 10** — The changes panel.

  Register the `changes` panel, its tab carrying the session's line counts, and port the classic `Changes` over Story 4's `usePatch` (signature the status and counts, live while Running): the sub-line, then the file count, counts and base once the patch is held; loading and error with retry; "No changes since <sha>."; each file as a fold with its status letter, name, directory or source path, counts or "binary", its hunks with their cut marks, or the reason it has none (too long, over the 1 MiB budget, binary, no text changes); a lone file open on the first load only, the open set kept across refreshes; the refresh error; and the reply box. Vitest covers the file forms over the fixture patch; webshot renders the diff screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/changes/index.tsx`, `internal/web/ui/src/panels/changes/Changes.tsx`, `internal/web/ui/src/panels/changes/changes.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the `changes` panel; uses Story 3's `sessionActions`, Story 4's `usePatch`, Story 6's `SessionHead`, `SubLine` and `NoSession`, Story 7's `ReplyBox`, Story 1's `fixturePatch`, part 1's `Collapsible`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens diff -out "$d"'`
  - **Review:** light (a port of `changes.mjs` over Story 4's read)
  - **Form:** brief (a port of `changes.mjs`)
  - **Reading:** spec §26, §30, Part 2 settlements 2, 16; `internal/web/static/js/changes.mjs`, `internal/web/reads.go:300-310`, `internal/web/README.md` (Limits)
  - **Drafts after:** Story 4, Story 6, Story 7

- **Story 11** — The subagent panel.

  Register the `agent` panel and port the classic `agent.mjs` over Story 4's turn pages with the agent's id (spec Part 2 settlement 10): `AgentHead` (back to the conversation, through history when the conversation was the previous page; the title from the page's agent), `AgentStrip` (running, finished or idle for a teammate, the agent's type, and "session needs you" linking to the conversation while the group is needs_you), and the body's loading, gone, starting and turn-list states with their poll rules. No reply box, no needs-you box, and its pages are not kept. Vitest covers each state and the poll's stop; webshot renders the agent screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/agent/index.tsx`, `internal/web/ui/src/panels/agent/Agent.tsx`, `internal/web/ui/src/panels/agent/agent.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the `agent` panel, `AgentHead`, `AgentStrip`; uses Story 4's `useTurnPages` and `useHeldTurns`, Story 5's `TurnList`, Story 6's tab rules and `NoSession`, part 1's `ContextRow` and `hrefFor`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens agent -out "$d"'`
  - **Review:** light (a port of `agent.mjs` over Story 4's read)
  - **Form:** brief (a port of `agent.mjs`)
  - **Reading:** spec §26, Part 2 settlements 10, 16; `internal/web/static/js/agent.mjs`, `internal/web/static/js/strip.mjs:36-55`, `internal/web/README.md` (The app: the subagent's screen)
  - **Drafts after:** Story 4, Story 5, Story 6

- **Story 12** — The job and mockup panels, each a chunk of its own.

  Give `PanelHost` a `Suspense` around `Body` and register the job and mockup views with lazy bodies (spec §15, Part 2 settlement 14). The job view ports `job.mjs`: `JobHead` (back to info, the job's title), its facts by state, its command and submitter, its controls by state as Story 3's job commands (Start now with its sheet, Park or Unpark, Pause or Resume, or "Pausing…"/"Resuming…" while a control is in flight, and its control error), and its log over Story 4's `useJobLog`, following the bottom unless the reader scrolled up more than 40 px. The mockup views port `mockup.mjs`: the frame (`sandbox="allow-scripts"` and never `allow-same-origin`, `referrerpolicy="no-referrer"`, its URL from `api.mockupURL` with `v` the change time and `r` a reload count), the strip with its age, reload and full-screen link, the reply box, "No mockup is showing; the companion may have stopped.", and the full view with only its frame and "done"; both mark the session seen. A Go test pins the two chunks; webshot renders every screen of the new app.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/panels/job/index.tsx`, `internal/web/ui/src/panels/job/JobBody.tsx`, `internal/web/ui/src/panels/job/job.test.tsx`, `internal/web/ui/src/panels/mockup/index.tsx`, `internal/web/ui/src/panels/mockup/MockupBody.tsx`, `internal/web/ui/src/panels/mockup/mockup.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `job`, `mockup` and `mockupFull` panels, `JobHead`, `PanelHost`'s `Suspense`, `TestDist_LazyViewsAreChunksOfTheirOwn`; uses Story 3's job commands, Story 4's `useJobLog`, Story 6's `SessionHead`, `NoSession` and `useMarkSeen`, Story 7's `ReplyBox`, Story 9's job helpers, part 1's `api.mockupURL`, `Skeleton` and `CommandButton`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d"'`
  - **Review:** deep
  - **Form:** brief (two ports and the lazy wiring; the frame's sandbox, scripts only and never same-origin, and the two chunks are pinned by named tests)
  - **Reading:** spec §15, §26, §33, Part 2 settlements (The budget), 8, 11, 12, 14; `internal/web/static/js/job.mjs`, `internal/web/static/js/mockup.mjs`, `internal/web/static/js/app.mjs:405-456`, `internal/web/mockup.go:15-62`, `internal/web/static/app.css:724-780`, `internal/web/static_budget_test.go`, `internal/web/ui/src/app/shell.tsx:118-135`, `internal/web/ui/vite.config.ts`
  - **Drafts after:** Story 3, Story 4, Story 6, Story 7, Story 9

- **Story 13** — The rules and procedures for the session views.

  Amend `internal/web/ui/CLAUDE.md`'s command rule (a `when` may read the session's group, spec Part 2 settlement 2) and add the rules part 2 teaches: Open in Claude and other links that act on nothing in loom stay links (settlement 5); a read goes through `src/api/reads.ts`, never a fetch of its own; a view the first load doesn't need is a lazy `Body`. Extend `docs/claude/web-layer.md`'s panel and command procedures (`Strip`, a tab's `Mark`, a lazy `Body`; `key`, a `confirm` that returns null, a sheet's field and `run`'s input) and add one for a read; describe the new app's session views in `internal/web/README.md`. `internal/web/CLAUDE.md` does not change (210 bytes of headroom).

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `internal/web/README.md`
  - **Interfaces:** none; uses every earlier story's names
  - **Verify:** `CGO_ENABLED=0 go test ./tools/claudemd/`
  - **Review:** light (docs, checked by the CLAUDE.md lint)
  - **Form:** brief (docs)
  - **Reading:** spec §35, §36, Part 2 settlements 2, 5, 14, 15, 18; `CLAUDE.md` (Conventions for CLAUDE.md files), `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `internal/web/README.md` (The app)
  - **Drafts after:** Story 2, Story 3, Story 4, Story 5, Story 6, Story 7, Story 8, Story 9, Story 10, Story 11, Story 12

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
