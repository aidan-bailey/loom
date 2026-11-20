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

- `tools/webshot/fixture/snapshot.json` (Story 1): session 2 (needs_you, Prompting, Claude, with a `remote_url`) holds two asks, a permission ask with `id` `"fixture-permission"` (`agent` `""`, `tool` `"Bash"`, `summary` `"go test ./ui/... -run TestPane"`, `input` a mono `command` block `"go test ./ui/... -run TestPane"` and a `description` block `"Run the pane tests"`, `suggestions` `"Yes, and don't ask again for Bash(go test:*) in this project, for you"` with `persists` true and `"Yes, and don't ask again for Bash(go test:*) for this session"` without) and a question ask with `id` `"fixture-question"` whose `agent` is `"recon"` (the type of session 1's subagent `a1f00dcafe0001`: `Ask` carries no agent id), holding `"Which tests should run?"` (header `"Tests"`, single-select, options `"Pane only"` with a `preview` and `"Whole package"`) and `"Which checks to add?"` (header `"Checks"`, `multi` true, options `"race"`, `"vet"`, `"lint"`); session 2's `agents` stays empty; session 1 gains `worktree` `"/work/loom/fix-pane-cursor"`, `tmux` `"loom_fix-pane-cursor"` and job 216 (`state` `"queued"`, `description` `"e2e tests"`, `command` `"go test -tags e2e ./e2e/..."`, `waiting` `"waiting for 6 free cores"`, `agent` `""`, its other fields as job 215's); every other session keeps `asks` empty.
- `tools/webshot/fixture/turns.json` (Story 1): adds a `note` item (one plain, one with `error`), a step whose `state` is `interrupted`, and a text item whose `blocks` hold `h`, `ul`, `ol`, `li`, `quote`, `code`, `em`, `strong`, `a`, `hr` and `table` (`tr`, `th`, `td`) nodes; the agent step, the thinking item and every id the existing webshot tests read stay as they are.
- `tools/webshot/fixture/diff.json` (Story 1): adds a file with `status` `D` and a file with `omitted` `lines`.
- `function fixtureTurns(agent?: boolean): Turns` (Story 1), in `fixtures.ts`: `turns.json`, or `agent-turns.json` when `agent`, with its relative times filled as `fixtureSnapshot` fills them.
- `function fixturePatch(): Patch` (Story 1), in `fixtures.ts`: `diff.json`.
- `function fixtureJobLog(after?: boolean): JobLog` (Story 1), in `fixtures.ts`: `job-log.json`, or `job-log-after.json` when `after`.

**Commands and the sheet, widened (Story 2):**

- `interface Command<A = void> { id: string, title: string | ((ctx: CommandContext) => string), icon?: LucideIcon, offline?: boolean, key?: (args: A) => string, when: (ctx: CommandContext) => boolean, confirm?: (ctx: CommandContext, args: A) => ConfirmSpec | null, run: (ctx: CommandContext, args: A, input: string) => Promise<void> }` (Story 2), in `app/commands.ts`, replacing part 1's. The in-flight key is the command's id, the session's id and what `key` returns for the args (`""` without `key`); a `confirm` that returns null runs `run` with `input` `""` and no sheet; `input` is the sheet's field value (`""` with no field).
- `function useCommandBusy<A>(command: Command<A>, ctx: CommandContext, args: A): boolean` (Story 2), replacing part 1's two-argument form; `CommandButton` passes its `args`.
- `CommandButton`'s props gain `disabled?: boolean` (Story 2), ORed into the button's own enablement (a caller's form state: an empty or too-long reply, an unanswered question, an ask that is `answering`).
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
- The session commands (Story 3), in `commands/session.ts`, each with its classic sheet and words (spec Part 2 settlement 8): `session.send` with args `{ text: string }` (no sheet; the trimmed text to `api.send`; a 200 clears the session's reply draft only when the draft, trimmed, still equals the text sent, so what was typed while the send was in flight is kept), `session.pause`, `session.resume`, `session.push` (sheet), `session.merge` (a sheet whose `ChoiceField` lists the other sessions of the session's workspace by `"<title> (<branch>)"`, valued by id; null and an error toast when there are none), `session.kill` (a destructive sheet; a 200 opens the fleet with `window.location.assign(hrefFor("fleet"))`), each of the last five with args `void`.
- `interface AllowArgs { ask: string, suggestion: number, label: string, persists: boolean }` (Story 3), in `commands/asks.ts`.
- `function allowArgs(ask: Ask, suggestion: number): AllowArgs` (Story 3), in `commands/asks.ts`: `suggestion` 0 is the plain Allow (`label` `"Allow"`, `persists` false); n ≥ 1 is `ask.suggestions[n - 1]`, its `label`, and `persists` true only when that suggestion's `persists` is true. The permission card builds every Allow's args through it.
- `function answerBody(ask: Ask, draft: AskDraft | undefined): AnswerBody | null` (Story 3), in `commands/asks.ts`: null until every question of `ask.questions` has a picked label or an Other text, or while the answer's text passes `MAX_MESSAGE_BYTES` (the classic `asks.mjs` rules); else `{ answers }` keyed by each question's text, `labels` in pick order, `other` and `note` trimmed and left out when empty, and a single-select question carrying one answer, a label or Other, never both. The question form builds Answer's body through it and enables Answer by it.
- The ask commands (Story 3), in `commands/asks.ts`, each keyed by its ask's id: `ask.answer` with args `{ ask: string, body: AnswerBody }` (a 200 drops the ask's draft), `ask.chat` with args `{ ask: string }` (a sheet with a `TextField`; body `{ chat: true, chat_text: <trimmed input> }`; a 200 drops the ask's draft), `ask.allow` with args `AllowArgs` (`suggestion` 0 for the plain Allow, else 1-based; a "Save this rule?" sheet only when `persists`; body `{ allow: true }` plus `suggestion` when above 0), `ask.deny` with args `{ ask: string }` (a destructive sheet with a `TextField`; body `{ allow: false, message: <trimmed input> }`).
- The job commands (Story 3), in `commands/jobs.ts`, each with args `{ job: number }`, keyed by the job's id, calling `api.jobControl(session, job, <action>)`: `job.start` (the "Start job N now?" sheet, its body naming the job's `waiting` when set), `job.park`, `job.unpark`, `job.pause`, `job.resume`.
- `commands/testkit.ts` (Story 3): the command tests' shared helpers (`makeSession`, `seed`, `resetFleet`, `noticesOk` and the rest Story 3 defines there, with the `vi.mock("@/app/ConfirmSheet")` pattern its tests use), which the tests of Stories 7 and 8 import too.
- `commands/index.ts` (Story 3): imports the three modules for their registration; `main.tsx` imports `@/commands`, and a test that draws a `CommandButton` imports it too.

**Reads (Story 4)**, in `api/reads.ts`:

- `type TurnsLoad = "newest" | "refresh" | "earlier"` (Story 4).
- `interface HeldTurns { conversation: string, turns: Turn[], earlier: number | null, skipped: number, agent: AgentPage | undefined }` (Story 4).
- `function mergeTurns(held: HeldTurns | undefined, page: Turns, load: TurnsLoad): HeldTurns` (Story 4): the classic `mergeTurns` (`internal/web/static/js/turns.mjs`).
- `interface TurnPagesOptions { session: number, agent: string | undefined, live: boolean }` (Story 4).
- `interface TurnPages { held: HeldTurns | undefined, landed: TurnsLoad | undefined, error: string, loading: boolean, load: (kind: TurnsLoad) => void }` (Story 4): `landed` is the kind of the load whose page produced `held` (a page with `reset` counts as `"newest"`; `undefined` before the first page), written in the same cache write as `held`, so the conversation tells a first page, a reset or a refresh from an earlier page when it anchors its scroll.
- `function useTurnPages(options: TurnPagesOptions): TurnPages` (Story 4): on mount a `refresh` when pages are held, else `newest`; a `refresh` every 5 s while `live`; one request in flight, a load asked meanwhile run once when it lands; held in the query cache, so two components with the same `session` and `agent` share one held state and one poll. Retention: a conversation's pages (no `agent`) are held with `gcTime: Infinity` and leave the cache only by the `KEEP_SESSIONS` eviction (which drops their kept scroll too) or `queryClient.clear()`; a subagent's pages are held with `gcTime: 0`, so they leave once no component reads them and a reopened subagent view loads `newest`.
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
- `function useJobLog(options: JobLogOptions): JobLogView` (Story 4): on mount the kept log drawn first and a read `after` its `end`, or the tail when no log is kept (a tail answer never sets `reset`, so a tail appended to a kept log would repeat it; classic `job.mjs` reads `after` too); then `after` every 2 s while `live` and not `gone`, and once more when `live` turns false; a 409 sets `gone` and stops. A job's log is held with `gcTime: Infinity` and leaves the cache only by the `KEEP_JOBS` eviction or `queryClient.clear()`.

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
- `ContextRow`'s props gain `onBack?: (e: MouseEvent<HTMLAnchorElement>) => void` (Story 6), in `app/shell.tsx`: set as the back link's `onClick`; a `ContextRow` without it is unchanged. The subagent head goes back through history with it.
- Part 1's `ago(iso: string, now?: number): string` stays exported from `panels/fleet/SessionRow.tsx` (Story 6 edits that file; Stories 5, 9 and 12 import it), and Story 6 exports `PrMark` from it for `FactStrip`.
- `const SEEN_KEY = "loom.seen"` (Story 6), in `fleet/seen.ts`, stored in the classic double encoding (spec Part 2 settlement 12): `readSeen` returns `JSON.parse(JSON.parse(raw) || "{}")` of `localStorage.getItem(SEEN_KEY)` (null read as `"{}"`), and `{}` on any failure or a value that is not an object; `markSeen` and `pruneSeen` write `JSON.stringify(JSON.stringify(seen))`, as classic `pref("seen", JSON.stringify(seen))` does; a test reads a value the classic `pref` wrote and checks the bytes it writes back.
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
- `function jobTone(job: Job): string` (Story 9), in `session/jobs.ts`: the text-colour class of a job's glyph by its state (neutral for an unknown one), which the info view's job rows and the job view both draw, so neither keeps a private copy.
- The `changes` panel (Story 10): `{ id: "changes", appliesTo: (s: Session) => sessionActions(s: Session).diff, tab: { order: 2, label: "changes", Mark: <the session's line counts while either is above 0> }, Head: SessionHead }`.
- The `agent` panel (Story 11): `{ id: "agent", Head: AgentHead, Strip: AgentStrip }`, no tab.
- The `job` panel (Story 12): `{ id: "job", Head: JobHead }`, its `Body` `React.lazy` over `panels/job/JobBody.tsx`.
- The `mockup` and `mockupFull` panels (Story 12): `mockup` is `{ id: "mockup", appliesTo: (s: Session) => s.mockup !== null, tab: { order: 4, label: "mockup", Mark: <a dot while the mockup is fresh> }, Head: SessionHead }`, `mockupFull` has no `Head`; both `Body`s are `React.lazy` over `panels/mockup/MockupBody.tsx`.
- `PanelHost` (Story 12), in `app/shell.tsx`: wraps `Body` in `Suspense` with a `Skeleton` fallback, inside its error boundary.
- `TestDist_LazyViewsAreChunksOfTheirOwn` (Story 12), in `internal/web/static_budget_test.go`: `internal/web/dist/assets/` holds a `JobBody-*.js` and a `MockupBody-*.js`, and `internal/web/dist/index.html` names neither.

**Texts later stories quote** (a fixer leaves each unchanged, or reports the clash): Story 2's `runCommand` and `askConfirm` (Story 3); Story 3's `commands/testkit.ts` helpers (Stories 7, 8); Story 6's `AppShell`, `tabOf` and `PanelHost` in `app/shell.tsx` (Story 12); Story 6's `panels/fleet/index.tsx` (none later); `panels/index.ts` as each panel story leaves it (the next one).

**Design stage:** /flow:draft session 5d2fbae5-e7ea-4983-ace3-17ccc2e7ae0a, 1c94346d-a21f-4506-9691-d9b41148a506, 2026-11-19T19:09:20Z to 2026-11-19T20:37:34Z UTC: $54.44 logged, $54.65 with estimated thinking; 7927 plan lines, $6.87 per 1,000 lines; skeleton review $2.84; plan review $11.40; budget $67; forms: full 3 stories $12.34, brief 10 stories $27.86; loom flow roster a71e5c0a4f9b.

**Plan review:** /flow:draft plan review; deep: 2, 3, 4; light: 1, 9, 10, 11, 13; full: 5, 6, 7, 8, 12

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

  Widen part 1's command registry for part 2's actions (spec Part 2 settlements 6 and 7): the confirmation sheet draws an optional text field (a `Textarea`, its UTF-8 limit disabling the action past it) or a native-select choice, `askConfirm` resolves the field's value or null, a command's `confirm` may return null to run without a sheet, `run` receives the value, and a command's `key` (`(args: A) => string`) joins its in-flight key so commands on two jobs or two asks don't block each other. The freshness check after a yes, the stale gate and the error toast stay as they are. Give `CommandButton` a `disabled` prop for a caller's form state. Add shadcn's `Textarea` and the shared UTF-8 length. Update part 1's command and sheet tests (and `shell.test.tsx`'s sheet test, which reads `askConfirm`'s result) to the new signatures, keeping what they pin.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/app/commands.test.ts`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/app/CommandButton.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/components/ui/textarea.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/lib/utf8.ts`, `internal/web/ui/src/lib/utf8.test.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the widened `Command`, `useCommandBusy`, `CommandButton`'s `disabled`, `ConfirmSpec`, `ConfirmField`, `TextField`, `ChoiceField`, `ChoiceOption`, `askConfirm`, `Textarea`, `utf8Length`, `MAX_MESSAGE_BYTES`; uses part 1's registry, `freshContext`, `showMessage` and `Drawer`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** full (the confirmation and in-flight path every destructive command runs through: a wrong key runs Kill twice, a wrong null runs it unasked, a value read before the freshness check acts on a stale session)
  - **Reading:** spec §23, Part 2 settlements (Commands, 6, 7, 18); `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/CommandButton.tsx`, `internal/web/ui/src/app/commands.test.ts`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/components/ui/drawer.tsx`, `internal/web/static/js/sheets.mjs`

- **Story 3** — The drafts slice, and the session, ask and job commands.

  Add the in-memory drafts slice (a reply per session, an answer per ask, cleared on unpair; spec Part 2 settlement 3) and register every part 2 action as a command (§23 as settled): send, pause, resume, push, merge and kill on a session, with the classic sheets and words (settlement 8) and the classic availability as `sessionActions` (settlement 2); answer, chat, allow and deny on an ask; start, park, unpark, pause and resume on a job, with the pure helpers that build an Allow's args and an answer's body from its draft (spec Part 2 settlement 19). Each `run` calls its `api` function and hands the outcome to `showOutcome`; send, answer and chat clear their draft only on a 200 (send only while the draft still holds the text sent); Kill opens the fleet only on a 200. Amend the rules the `group` reading breaks: `internal/web/ui/CLAUDE.md`'s command rule (a `when` may read the session's `group`, settlement 2) and `internal/web/CLAUDE.md`'s clause that the app's buttons follow only the session's `status` (to its `status` and `group`, within that file's 210 bytes of headroom). Vitest covers each command's `when` across statuses, groups and link states, each sheet naming what it does (Kill's tmux session, worktree and branch, and its paused paragraph only for a Paused session), the bodies each sends, `allowArgs` and `answerBody` on every form, and the drafts.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/store/drafts.ts`, `internal/web/ui/src/store/drafts.test.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/startFleet.test.ts`, `internal/web/ui/src/commands/index.ts`, `internal/web/ui/src/commands/index.test.ts`, `internal/web/ui/src/commands/testkit.ts`, `internal/web/ui/src/commands/session.ts`, `internal/web/ui/src/commands/session.test.ts`, `internal/web/ui/src/commands/asks.ts`, `internal/web/ui/src/commands/asks.test.ts`, `internal/web/ui/src/commands/jobs.ts`, `internal/web/ui/src/commands/jobs.test.ts`, `internal/web/ui/src/main.tsx`, `internal/web/ui/CLAUDE.md`, `internal/web/CLAUDE.md`, `internal/web/dist/`
  - **Interfaces:** exposes `QuestionDraft`, `AskDraft`, `DraftsState`, `useDrafts`, `SessionActions`, `sessionActions`, `AllowArgs`, `allowArgs`, `answerBody`, and the commands `session.send`, `session.pause`, `session.resume`, `session.push`, `session.merge`, `session.kill`, `ask.answer`, `ask.chat`, `ask.allow`, `ask.deny`, `job.start`, `job.park`, `job.unpark`, `job.pause`, `job.resume`; uses Story 2's command path and fields, part 1's `api`, `showOutcome`, `showMessage`, `hrefFor`, `allSessions` and `startFleet`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
  - **Review:** deep
  - **Form:** full (the owner, 2026-11-19, spec Part 2 settlement 19: the bodies sent to the daemon, Allow's 1-based suggestion and its persist sheet, the drafts' 200-only clearing, the per-ask and per-job keys and Kill's navigation)
  - **Reading:** spec §20, §23, Part 2 settlements 2, 3, 6, 7, 8; `internal/web/static/js/session.mjs` (`actionsFor`, `mergeCandidates`, the sheets in `Info`), `internal/web/static/js/asks.mjs`, `internal/web/static/js/reply.mjs`, `internal/web/static/js/job.mjs:86-112` and its start sheet, `internal/web/ui/src/app/toasts.ts`, `internal/web/ui/src/store/fleet.ts:200-290`, `internal/web/ui/src/api/gen.ts:61-101`, `internal/web/ui/src/api/gen.ts:620-646`, `internal/web/ui/CLAUDE.md`, `internal/web/CLAUDE.md` (the buttons' clause)
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

  Give every session view the classic frame (spec §22, Part 2 settlements 12 and 16): a panel's `Strip` drawn under the tabs and a tab's `Mark` after its label; the subagent and job views drawing the session's tabs and lighting the conversation and info tabs; a non-Claude session's `#s/<id>` lighting info; `SessionHead` (back to the fleet, the title, `claude ↗`), `FactStrip`, `SubLine`, `NoSession` and the facts' helpers. Give `ContextRow` an `onBack` for the subagent head's way back through history, and add to `internal/web/ui/CLAUDE.md` the rule that Open in Claude and other links that act on nothing in loom stay links, not commands (settlement 5). Add the seen times in the classic `loom.seen` double encoding, the hook that marks a session seen on opening and closing, and the fleet row's NEW mark, the fleet pruning the times of sessions the daemon no longer lists. Vitest covers the tab rules, the strip's facts and links, and the seen encoding against a value the classic `pref` wrote.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/session/SessionHead.tsx`, `internal/web/ui/src/session/FactStrip.tsx`, `internal/web/ui/src/session/SubLine.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/session/facts.ts`, `internal/web/ui/src/session/session.test.tsx`, `internal/web/ui/src/fleet/seen.ts`, `internal/web/ui/src/fleet/seen.test.ts`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/panels/fleet/fleet.test.tsx`, `internal/web/ui/CLAUDE.md`, `internal/web/dist/`
  - **Interfaces:** exposes `PanelTab`, the widened `Panel`, the tab rules, `SessionHead`, `FactStrip`, `SubLine`, `NoSession`, `parityText`, `checksText`, `groupGlyph`, `SEEN_KEY`, `readSeen`, `markSeen`, `pruneSeen`, `hasNewTurn`, `useMarkSeen`, `ContextRow`'s `onBack`, the exported `PrMark`; uses part 1's `ContextRow`, `SessionTabs`, `sessionTabs`, `rowShare`, `ago` and `hrefFor`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens fleet -out "$d"'`
  - **Review:** deep
  - **Form:** brief (the shell's slots and a port of `session.mjs`'s head, `strip.mjs` and `fleet.mjs`'s seen times)
  - **Reading:** spec §22, §29, Part 2 settlements (Panels, The shell), 5, 12, 16; `internal/web/static/js/session.mjs:40-67`, `internal/web/static/js/session.mjs:340-360`, `internal/web/static/js/strip.mjs`, `internal/web/static/js/chrome.mjs:60-97`, `internal/web/static/js/fleet.mjs:70-142`, `internal/web/static/js/store.mjs:70-93`, `internal/web/static/js/skin.mjs`, `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/panels/fleet/index.tsx:1-40`, `internal/web/ui/src/fleet/usage.ts`

- **Story 7** — The needs-you box, the asks and the reply box.

  Port the classic `needs.mjs`, `asks.mjs` and `reply.mjs` onto Story 3's commands, drafts and helpers (every Allow's args through `allowArgs`, Answer's body and enablement through `answerBody`): the needs-you box (its reason, the wait's detail when there are no asks, the asks, Open in Claude as a link or "Remote Control is off for this session.", spec Part 2 settlement 5); the question form (options as radios or checkboxes, the preview of the last picked option that has one, Other, a note, Answer enabled once every question is answered within 64 KiB, Chat about this); the permission card (the input blocks, Allow, each suggestion, Deny); a card disabled while its ask is answering, an unknown kind's neutral card (settlement 9); and the reply box (a `Textarea` with the placeholder "reply, or /command…", quick-reply chips while it is empty, the 64 KiB limit, the draft kept across tab switches, settlement 1). Vitest covers the ask forms as spec Testing lists them and the reply draft across a remount.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/conversation/NeedsYou.tsx`, `internal/web/ui/src/conversation/NeedsYou.test.tsx`, `internal/web/ui/src/conversation/Asks.tsx`, `internal/web/ui/src/conversation/Asks.test.tsx`, `internal/web/ui/src/conversation/ReplyBox.tsx`, `internal/web/ui/src/conversation/ReplyBox.test.tsx`, `internal/web/dist/`
  - **Interfaces:** exposes `NeedsYou`, `Asks`, `ReplyBox`; uses Story 2's `Textarea`, `utf8Length`, `MAX_MESSAGE_BYTES` and `CommandButton`'s `disabled`, Story 3's `useDrafts`, `sessionActions`, `allowArgs`, `answerBody` and the `session.send` and `ask.*` commands (keyed by their ask's id), Story 6's `groupGlyph`, part 1's `CommandButton`, `useFleet`, `useLink`, `buttonVariants` and `Badge`, Story 1's `fixtureSnapshot` asks
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  - **Review:** deep
  - **Form:** brief (forms over Story 3's commands, whose bodies the daemon validates and bounds; each form's rule is a named test)
  - **Reading:** spec §34, Testing (the ask forms, reply drafts), Part 2 settlements 1, 3, 5, 9, 13; `internal/web/static/js/needs.mjs`, `internal/web/static/js/asks.mjs`, `internal/web/static/js/reply.mjs`, `internal/web/static/app.css:400-420`, `internal/web/static/app.css:550-575`, `internal/web/asks.go:30-151`, `internal/web/README.md` (Answering an ask, if present), `docs/claude/web-layer.md` (Answering an ask)
  - **Drafts after:** Story 2, Story 3

- **Story 8** — The conversation panel.

  Register the `turns` panel (spec Part 2 settlement 16) and port the classic `Conversation`: the unreadable-records notice, `↑ earlier turns`, loading and error with retry, "No turns yet.", the turn list (live while the session is Running), the refresh error, the needs-you box while the group is needs_you, and the reply box. It refreshes when the stream changes the session's status or last message (not on mount) and every 5 s while Running (§26), anchors the page to the bottom on a first page, a reset or a refresh that lands near it (48 px), keeps the reader's place after an earlier page, restores a kept conversation's scroll before the first paint, and marks the session seen. A non-Claude session's `#s/<id>` draws info through `panelFor("info")`, and `NoSession` while no `info` panel is registered (Story 9 registers it). Vitest covers the triggers and the drawn parts; webshot renders the conversation and waiting screens.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/turns/index.tsx`, `internal/web/ui/src/panels/turns/Conversation.tsx`, `internal/web/ui/src/panels/turns/turns.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the `turns` panel and `Conversation`; uses Story 4's `useTurnPages` (with `landed`), `keptScroll` and `keepScroll`, Story 1's `fixtureTurns`, part 1's `fixtureSnapshot`, `Panel` and `PanelProps`, Story 5's `TurnList`, Story 6's `SessionHead`, `FactStrip`, `NoSession` and `useMarkSeen`, Story 7's `NeedsYou` and `ReplyBox`, part 1's `definePanel`, `panelFor` and `useSession`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens conversation,waiting -out "$d"'`
  - **Review:** deep
  - **Form:** brief (a port of `turns.mjs`'s `Conversation` over Story 4's reads)
  - **Reading:** spec §26, §27, Part 2 settlements 12, 13, 15, 16; `internal/web/static/js/turns.mjs:205-392`, `internal/web/README.md` (The app), `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/store/fleet.ts:137-144`, `internal/web/ui/src/app/shell.tsx:121-131`
  - **Drafts after:** Story 4, Story 5, Story 6, Story 7

- **Story 9** — The info panel.

  Register the `info` panel and port the classic `Info` (`session.mjs`): the sub-line; the Changes, PR and Account tiles (the account's shares, reset texts and staleness as the classic tile draws them); the agents, each linking to the subagent view when its id is a valid agent id; the jobs, each linking to the job view with its glyph and line, re-drawn every 5 s while one runs; the actions the session allows (`sessionActions`) as command buttons; the reply box; and "This session is gone." for a session the snapshot no longer lists. It marks the session seen. Add the job helpers the job view shares, with an unknown state's raw text (spec Part 2 settlement 9). Vitest covers each tile's forms, the rows' links and the actions shown per status and group; webshot renders the info screen.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/info/Info.tsx`, `internal/web/ui/src/panels/info/info.test.tsx`, `internal/web/ui/src/session/jobs.ts`, `internal/web/ui/src/session/jobs.test.ts`, `internal/web/ui/src/fleet/usage.ts`, `internal/web/ui/src/fleet/usage.test.ts`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
  - **Interfaces:** exposes the `info` panel, `jobGlyph`, `jobTone`, `elapsed`, `jobEnd`, `jobLine`; uses Story 3's `sessionActions` and session commands, Story 5's `AGENT_ID`, Story 6's `SessionHead`, `SubLine`, `NoSession`, `parityText`, `checksText` and `useMarkSeen`, Story 7's `ReplyBox`, part 1's `CommandButton`, `ago` and `fleet/usage.ts`'s helpers, widened (its private helpers exported, `shownWindow` reshaped with `rowShare`'s answers unchanged, `shareText` and `resetText` added)
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens info -out "$d"'`
  - **Review:** light (a port of `session.mjs`'s `Info`; its actions are Story 3's commands)
  - **Form:** brief (a port of `session.mjs`'s `Info`)
  - **Reading:** spec §29, §31, Part 2 settlements 2, 9, 12; `internal/web/static/js/session.mjs:70-337`, `internal/web/static/js/usage.mjs`, `internal/web/ui/src/fleet/usage.ts`, `internal/web/ui/src/fleet/usage.test.ts`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`
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
  - **Interfaces:** exposes the `agent` panel, `AgentHead`, `AgentStrip`; uses Story 4's `useTurnPages` and `useHeldTurns`, Story 5's `TurnList`, Story 6's tab rules, `NoSession` and `ContextRow`'s `onBack`, part 1's `ContextRow` and `hrefFor`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens agent -out "$d"'`
  - **Review:** light (a port of `agent.mjs` over Story 4's read)
  - **Form:** brief (a port of `agent.mjs`; Task 11.2's risk, the 30 s gone timer and the once-more refresh's order against the poll's stop, is UI timing over Story 4's single-flight read, and the run's full review checks it: the owner, 2026-11-19)
  - **Reading:** spec §26, Part 2 settlements 10, 16; `internal/web/static/js/agent.mjs`, `internal/web/static/js/strip.mjs:36-55`, `internal/web/README.md` (The app: the subagent's screen)
  - **Drafts after:** Story 4, Story 5, Story 6

- **Story 12** — The job and mockup panels, each a chunk of its own.

  Give `PanelHost` a `Suspense` around `Body` and register the job and mockup views with lazy bodies (spec §15, Part 2 settlement 14). The job view ports `job.mjs`: `JobHead` (back to info, the job's title), its facts by state, its command and submitter, its controls by state as Story 3's job commands (Start now with its sheet, Park or Unpark, Pause or Resume, or "Pausing…"/"Resuming…" while a control is in flight, and its control error), and its log over Story 4's `useJobLog`, following the bottom unless the reader scrolled up more than 40 px. The mockup views port `mockup.mjs`: the frame (`sandbox="allow-scripts"` and never `allow-same-origin`, `referrerpolicy="no-referrer"`, its URL from `api.mockupURL` with `v` the change time and `r` a reload count), the strip with its age, reload and full-screen link, the reply box, "No mockup is showing; the companion may have stopped.", and the full view with only its frame and "done"; both mark the session seen. A Go test pins the two chunks; webshot renders every screen of the new app.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/panels/job/index.tsx`, `internal/web/ui/src/panels/job/JobBody.tsx`, `internal/web/ui/src/panels/job/job.test.tsx`, `internal/web/ui/src/panels/mockup/index.tsx`, `internal/web/ui/src/panels/mockup/MockupBody.tsx`, `internal/web/ui/src/panels/mockup/mockup.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
  - **Interfaces:** exposes the `job`, `mockup` and `mockupFull` panels, `JobHead`, `PanelHost`'s `Suspense`, `TestDist_LazyViewsAreChunksOfTheirOwn`; uses Story 3's job commands, Story 4's `useJobLog`, Story 6's `SessionHead`, `NoSession` and `useMarkSeen`, Story 7's `ReplyBox`, Story 9's job helpers, part 1's `api.mockupURL`, `Skeleton`, `CommandButton`, `ContextRow` and `ago`
  - **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d"'`
  - **Review:** deep
  - **Form:** brief (two ports and the lazy wiring; the frame's sandbox, scripts only and never same-origin, and the two chunks are pinned by named tests)
  - **Reading:** spec §15, §26, §33, Part 2 settlements (The budget), 8, 11, 12, 14; `internal/web/static/js/job.mjs`, `internal/web/static/js/mockup.mjs`, `internal/web/static/js/app.mjs:405-456`, `internal/web/mockup.go:15-62`, `internal/web/static/app.css:724-780`, `internal/web/static_budget_test.go`, `internal/web/ui/src/app/shell.tsx:118-135`, `internal/web/ui/vite.config.ts`
  - **Drafts after:** Story 3, Story 4, Story 6, Story 7, Story 9

- **Story 13** — The rules and procedures for the session views.

  Add to `internal/web/ui/CLAUDE.md` the rules part 2 teaches that no earlier story wrote (Story 3 amended the command rule for `group` and Story 6 added the links rule): a read goes through `src/api/reads.ts`, never a fetch of its own; a view the first load doesn't need is a lazy `Body`. Extend `docs/claude/web-layer.md`'s panel and command procedures (its command procedure's `when` line reading the session's `status` and `group`, as Story 3 amended the rule; `Strip`, a tab's `Mark`, a lazy `Body`; `key`, a `confirm` that returns null, a sheet's field and `run`'s input) and add one for a read, naming them in `docs/claude/INDEX.md`'s `web-layer.md` row; describe the new app's session views in `internal/web/README.md`.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `internal/web/README.md`
  - **Interfaces:** none; uses every earlier story's names
  - **Verify:** `CGO_ENABLED=0 go test ./tools/claudemd/`
  - **Review:** light (docs, checked by the CLAUDE.md lint)
  - **Form:** brief (docs)
  - **Reading:** spec §35, §36, Part 2 settlements 2, 5, 14, 15, 18; `CLAUDE.md` (Conventions for CLAUDE.md files), `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `internal/web/README.md` (The app)
  - **Drafts after:** Story 2, Story 3, Story 4, Story 5, Story 6, Story 7, Story 8, Story 9, Story 10, Story 11, Story 12

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->

## Story 1: webshot's fixtures carry the asks, items, steps, nodes, jobs and files the session views draw

- **Developer:** intermediate-dev
- **Files:** `tools/webshot/fixture/snapshot.json`, `tools/webshot/fixture/turns.json`, `tools/webshot/fixture/diff.json`, `tools/webshot/webshot_test.go`, `internal/web/webshot_fixture_test.go`, `internal/web/ui/src/fixtures.ts`, `internal/web/ui/src/fixtures.test.ts`
- **Interfaces:** exposes the fixtures' asks, items, jobs and files, `fixtureTurns`, `fixturePatch`, `fixtureJobLog`; uses part 1's `fillTimes` and `fixtureSnapshot`
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app classic -screens conversation,waiting,info,diff -out "$d"'`
- **Review:** light (fixture data; the Go decode test and the typed fixture test check its shapes)
- **Form:** brief (fixture data and their coverage rows)

The canned loom's fixtures hold no ask, no `note` item, no `interrupted` step, few markdown nodes, no queued job and no deleted or omitted file, so neither app's screenshots show what part 2's views draw. This story adds that data to `tools/webshot/fixture/snapshot.json`, `turns.json` and `diff.json` (no new screen, no new fixture file), pins it with Go tests (`tools/webshot/webshot_test.go` and `internal/web/webshot_fixture_test.go`, which also hold it to the types and the tree builder the layer ships) and gives Vitest typed loaders: `fixtureTurns`, `fixturePatch` and `fixtureJobLog` beside `fixtureSnapshot`, with the new union rows in `fixtures.test.ts`. The story is fixture data and tests: it changes no app source, so `internal/web/dist/` is untouched. None of its code holds a landmine marker. Every existing webshot fixture test, and every id and value they read (session 1's agents `a1f00dcafe0001` and `a1f00dcafe0002`, the agent step, the thinking item, turn 3 as the newest and partial turn), stays as it is.

Form: brief. A task gives the data to add by its fields, each test by name with what it asserts and what turns it red, and the developer writes the JSON and the test code. JSON is written with the file's existing two-space indent and key order; a time is a relative string (`"now-4m"`), never an absolute one.

### Task 1.1: session 2's asks, session 1's paths and queued job

**Files:**
- Modify: `tools/webshot/fixture/snapshot.json` (session 1: the `worktree`, `tmux` and `jobs` fields, around lines 21-152; session 2: its `asks`, line 153 onward, which is `"asks": []` today)
- Test: `tools/webshot/webshot_test.go`
- Test: `internal/web/webshot_fixture_test.go`

The data, all under `sessions."1"`:

- Session 2 (id 2, `sandbox-ask`): `asks` becomes two entries, permission first. Its `agents` stays `[]`; its `wait`, `group`, `status` and `remote_url` stay.
  - Permission ask: `id` `"fixture-permission"`, `kind` `"permission"`, `agent` `""`, `opened` `"now-4m"`, `answering` false, `tool` `"Bash"`, `summary` `"go test ./ui/... -run TestPane"`, `input` two blocks: `{ "label": "command", "text": "go test ./ui/... -run TestPane", "mono": true }` and `{ "label": "description", "text": "Run the pane tests" }`, and `suggestions` two: `{ "label": "Yes, and don't ask again for Bash(go test:*) in this project, for you", "persists": true }` and `{ "label": "Yes, and don't ask again for Bash(go test:*) for this session" }` (no `persists` key). It has no `questions` key.
  - Question ask: `id` `"fixture-question"`, `kind` `"question"`, `agent` `"recon"`, `opened` `"now-3m"`, `answering` false, no `tool`, `summary`, `input` or `suggestions`, and `questions` two:
    - `question` `"Which tests should run?"`, `header` `"Tests"`, `multi` false, `options` `"Pane only"` (`description` `"Just TestPane"`, `preview` `"go test ./ui/... -run TestPane\n--- PASS: TestPane (0.01s)"`) and `"Whole package"` (`description` `"Every test in ui"`, no `preview` key).
    - `question` `"Which checks to add?"`, `header` `"Checks"`, `multi` true, `options` `"race"` (`description` `"Run with the race detector"`), `"vet"` (`"go vet ./..."`) and `"lint"` (`"golangci-lint on the diff"`), none with a `preview`.
- Session 1 (id 1, `fix-pane-cursor`): add `worktree` `"/work/loom/fix-pane-cursor"` and `tmux` `"loom_fix-pane-cursor"` (after `branch`, as `sessionJSON` orders them); its `jobs` gain job 216 after job 215: `state` `"queued"`, `description` `"e2e tests"`, `command` `"go test -tags e2e ./e2e/..."`, `agent` `""`, `submitted` `"now-1m"`, `started` `""`, `ended` `""`, `exit` null, `failure` `""`, `cancel_reason` `""`, `waiting` `"waiting for 6 free cores"`, `parked`, `paused`, `pause_reclaimed` and `forced` false, `control` `""`, `control_error` `""`. Its `jobs_label` `"⚙ job 214"` and `jobs_focus` 214 stay.
- Every other session keeps `"asks": []`, and no other session gains `worktree`, `tmux` or a job.

- [ ] **Step 1: Write the failing tests**

In `tools/webshot/webshot_test.go`, beside `TestFixture_SessionOnesAgentsCarryTheIdsTheTurnsLinkTo` (the same `c := &cannedLoom{now: time.Now}` and `c.fixture("snapshot.json")` start, decoding into a local struct that holds each session's `id`, `worktree`, `tmux`, `agents` and `asks` and each job's `id`, `state`, `waiting`):

- `TestFixture_SessionTwoHoldsAPermissionAndAQuestionAsk` finds session 2 and asserts: two asks, `fixture-permission` then `fixture-question`, with kinds `permission` and `question`; the permission ask's tool is `Bash`, its input holds the labels `command` then `description` with only the command block `mono`, and its two suggestions' `persists` are true then false; the question ask's `agent` is `recon` and its two questions' texts are `Which tests should run?` and `Which checks to add?` with `multi` false then true, a `preview` on `Pane only` and on no other option; session 2's `agents` is empty; and every other session's `asks` is empty.
- `TestFixture_SessionOneHoldsAWorktreeATmuxSessionAndAQueuedJob` finds session 1 and asserts: `worktree` `/work/loom/fix-pane-cursor`, `tmux` `loom_fix-pane-cursor`; a job 216 whose state is `queued` and whose `waiting` is `waiting for 6 free cores`; the jobs before it are still 214, 215, 212 and 209 as a set (214 `running`, 215 `held`), and no other session has a `worktree`, `tmux` or job.

In `internal/web/webshot_fixture_test.go`:

- `TestWebshotFixture_TheAsksAreOfKindsTheLayerSends` reads `snapshot.json` into `snapshotJSON` (unknown fields refused, as `TestWebshotFixture_IsTheJSONTheLayerServes` does), walks every session's `Asks`, requires at least one, and asserts each `Kind` is `askKind(core.AskKindQuestion)` or `askKind(core.AskKindPermission)` and that a question ask has `Questions` and no `Tool`, a permission ask has `Tool` and `Input` and no `Questions`.

Fails without: TestFixture_SessionTwoHoldsAPermissionAndAQuestionAsk: session 2's `asks` left `[]`, the `persists` of the first suggestion removed, or the `preview` of `Pane only` removed. TestFixture_SessionOneHoldsAWorktreeATmuxSessionAndAQueuedJob: job 216 left out, or its `state` set to `held`. TestWebshotFixture_TheAsksAreOfKindsTheLayerSends: the question ask's `kind` changed to a word the layer never sends (`"input"`), or a `tool` left on the question ask.

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestFixture_SessionTwoHoldsAPermissionAndAQuestionAsk|TestFixture_SessionOneHoldsAWorktreeATmuxSessionAndAQueuedJob' && CGO_ENABLED=0 go test ./internal/web/ -run TestWebshotFixture_TheAsksAreOfKindsTheLayerSends`

Expected: the first two fail (`Len` of session 2's asks: expected 2, actual 0; session 1's `worktree` empty), and the third fails on its "at least one ask" requirement.

- [ ] **Step 3: Implement**

Edit `tools/webshot/fixture/snapshot.json` as the data above lists, and nothing else in it.

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ -run 'TestFixture_|TestCanned_|TestWebshotFixture_'`

Expected: `ok` for both packages, including the three new tests and every existing `TestFixture_` and `TestCanned_` test.

- [ ] **Step 5: Commit**

```
git commit -m "test(webshot): session 2 holds two asks and session 1 a queued job in the canned snapshot" -- tools/webshot/fixture/snapshot.json tools/webshot/webshot_test.go internal/web/webshot_fixture_test.go
```

### Task 1.2: the turns' items, steps and nodes, and the diff's two files

**Files:**
- Modify: `tools/webshot/fixture/turns.json` (turn 1's `items`, lines 18-87, and nothing in turns 2 and 3)
- Modify: `tools/webshot/fixture/diff.json` (`files`, appended after `assets/cursor.png`)
- Test: `tools/webshot/webshot_test.go`
- Test: `internal/web/webshot_fixture_test.go`

The data:

- `turns.json`, turn 1 (id 1, `at` `"now-30m"`), whose `items` today are a `steps` item then a `text` item:
  - The `steps` item gains one step between the failed `go test ./ui/...` Bash step and the `Edit` step: `{ "tool": "Bash", "summary": "go vet ./...", "at": "now-25m", "state": "interrupted", "finished": false }`. Its `total` (7) and `failed` (1) stay.
  - After the existing `text` item, in this order: a `text` item (`at` `"now-21m"`) whose `blocks` are the tree `buildTree` makes of the markdown source below, written out as JSON nodes (`t`, `v`, `level`, `start`, `lang`, `href`, `align`, `c`); a `note` item (`at` `"now-20m"`, `text` `"Interrupted by you"`, no `error` key); and a `note` item (`at` `"now-20m"`, `text` `"API error: overloaded, retrying"`, `error` true).
  - The markdown source (the text a test holds as a Go raw string):

    ````
    # Findings

    The cursor *jumps* when the header **redraws**; see [the notes](https://example.com/pane).

    - first cause
    - second cause

    3. fix the draw order
    4. draw the cursor last

    > Keep the cursor off the header row.

    ```go
    p.drawCursor(frame)
    ```

    ---

    | file | lines |
    | :--- | ---: |
    | ui/pane.go | 12 |
    ````

    It holds an `h` (level 1), a `p` with `em`, `strong` and `a` nodes, a `ul` and an `ol` (`start` 3) of `li` nodes, a `quote`, a `code` (`lang` `go`), an `hr` and a `table` (`align` `["left","right"]`, one `tr` of `th` cells and one of `td` cells). Run `buildTree` over the source (the test below prints the tree it makes when the fixture differs) and write the nodes it returns, no others.
- `diff.json`: two files after `assets/cursor.png`:
  - `ui/legacy.go`: `old_path` `""`, `status` `"D"`, `binary` false, `added` 0, `removed` 31, `omitted` `""`, one hunk (`header` `"@@ -1,31 +0,0 @@"`, `cut` `[]`, `lines` three removed lines `"-package ui"`, `"-"`, `"-// legacy is gone."`).
  - `ui/tables_gen.go`: `old_path` `""`, `status` `"M"`, `binary` false, `added` 5200, `removed` 0, `omitted` `"lines"`, `hunks` `[]`.

- [ ] **Step 1: Write the failing tests**

In `tools/webshot/webshot_test.go` (decode with local structs, as `TestFixture_TheTurnsHoldAnAgentStepAndAThinkingItem` does):

- `TestFixture_TheTurnsHoldEveryItemKindAnInterruptedStepAndTheTreesNodes` reads `turns.json` and asserts: the item kinds across all turns include `text`, `thinking`, `steps`, `compacted` and `note`; exactly two `note` items, one with `error` true and one without; some step's `state` is `interrupted`; and the node kinds (`t`, collected through every `c`) across the text items include `h`, `p`, `ul`, `ol`, `li`, `quote`, `code`, `em`, `strong`, `a`, `hr`, `table`, `tr`, `th`, `td`, `text` and `code_inline`. The existing `TestFixture_TheTurnsHoldAnAgentStepAndAThinkingItem` is not edited.
- `TestFixture_TheDiffHoldsADeletedAndAnOmittedFile` reads `diff.json` and asserts: a file with `status` `D`, a file with `omitted` `lines` whose `hunks` is empty, the three files that were there (`ui/pane.go`, `ui/header.go`, `assets/cursor.png`) still first and in order.

In `internal/web/webshot_fixture_test.go`:

- `TestWebshotFixture_TheMarkdownTreeIsWhatBuildTreeMakes` holds the source above as a raw string, reads `turns.json` into `turnsJSON`, finds the text item whose first block is an `h` node, and requires `require.JSONEq` between that item's `Blocks` and what `buildTree(source)` returns (and that `buildTree` reports it uncut). It also requires that no `a` node in the fixture's turns has an empty `Href`.
- `TestWebshotFixture_TheDiffsFilesAreOfStatusesAndOmissionsTheLayerSends` reads `diff.json` into `patchJSON` and asserts every file's `Status` is one of `A`, `M`, `D`, `R`, `C` and every `Omitted` one of `""`, `lines`, `budget`, with a `D` and a `lines` among them.

Fails without: TestFixture_TheTurnsHoldEveryItemKindAnInterruptedStepAndTheTreesNodes: the `interrupted` step, the error note or the table node removed. TestFixture_TheDiffHoldsADeletedAndAnOmittedFile: the `D` file's `status` changed to `M`, or the `lines` file's `omitted` left `""`. TestWebshotFixture_TheMarkdownTreeIsWhatBuildTreeMakes: the `ol`'s `start` changed from 3 to 1, or one node written by hand that `buildTree` does not make. TestWebshotFixture_TheDiffsFilesAreOfStatusesAndOmissionsTheLayerSends: the omitted file's `omitted` changed to `"big"`.

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestFixture_TheTurnsHoldEveryItemKind|TestFixture_TheDiffHolds' && CGO_ENABLED=0 go test ./internal/web/ -run 'TestWebshotFixture_TheMarkdownTree|TestWebshotFixture_TheDiffsFiles'`

Expected: the turns and diff tests fail on the missing `note` items and the missing `D` file, the markdown test fails to find an item whose first block is `h`, and the diff-files test fails on the missing `D`.

- [ ] **Step 3: Implement**

Edit `turns.json` and `diff.json` as the data above lists. For the tree, run the markdown test, read the tree it expects, and write that tree's nodes into the text item.

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ -run 'TestFixture_|TestCanned_|TestWebshotFixture_'`

Expected: `ok` for both packages; `TestFixture_TheTurnsHoldAnAgentStepAndAThinkingItem` and `TestCanned_ASubagentsTurnsAreTheAgentFixture` pass unchanged.

- [ ] **Step 5: Commit**

```
git commit -m "test(webshot): the canned turns hold every item kind, an interrupted step and the tree's nodes, and the diff a deleted and an omitted file" -- tools/webshot/fixture/turns.json tools/webshot/fixture/diff.json tools/webshot/webshot_test.go internal/web/webshot_fixture_test.go
```

### Task 1.3: the typed loaders and the new union rows

**Files:**
- Modify: `internal/web/ui/src/fixtures.ts` (after `fixtureSnapshot`, line 52)
- Modify: `internal/web/ui/src/fixtures.test.ts` (the `import type` list at lines 2-24, the unions after `fileOmitted` at line 122, `unionFields` at line 178, and a new `describe` at the end)
- Test: `internal/web/ui/src/fixtures.test.ts`

Names this task defines (Story 5, 4, 8, 10 and 11 use them), in `fixtures.ts`, each returning a fresh copy with its relative times filled as `fixtureSnapshot` fills them, from `Date.now()`:

- `function fixtureTurns(agent?: boolean): Turns` returns `turns.json`, or `agent-turns.json` when `agent` is true.
- `function fixturePatch(): Patch` returns `diff.json`.
- `function fixtureJobLog(after?: boolean): JobLog` returns `job-log.json`, or `job-log-after.json` when `after` is true.

It uses part 1's `fillTimes`, and imports the five JSON files and the types `Turns`, `Patch` and `JobLog` from `@/api/gen`; the filled value is cast through `fillTimes`'s `unknown`, as `fixtureSnapshot` does. Its file comment line 1-3 names the other fixtures too.

- [ ] **Step 1: Write the failing tests**

In `fixtures.test.ts`: import the `AskKind` type; add `const askKind: Record<AskKind, true> = { question: true, permission: true };`; add the row `{ fixture: "snapshot.json", path: "sessions/*/*/asks/*/kind", union: askKind }` to `unionFields`; import `fixtureTurns`, `fixturePatch` and `fixtureJobLog`; and add `describe("the session views' inputs", …)` with `NOW = Date.parse("2026-11-18T12:00:00Z")` and `vi.useFakeTimers({ toFake: ["Date"] })` plus `vi.setSystemTime(NOW)` in a `beforeEach` (undone in an `afterEach`), holding:

- `the snapshot holds session two's permission and question asks`: through `fixtureSnapshot(NOW)`, session 2's asks have the ids `fixture-permission` and `fixture-question`, the permission's `suggestions` have `persists` true then not true, the question's `agent` is `recon`, its first question is single-select with a `preview` on exactly one option and its second is `multi`; session 1 has `worktree` `/work/loom/fix-pane-cursor`, `tmux` `loom_fix-pane-cursor` and a job 216 in state `queued` whose `waiting` is `waiting for 6 free cores`.
- `fixtureTurns holds every item kind, both notes and an interrupted step`: the item kinds across `fixtureTurns().turns` include all five of `ItemKind` (compare with `Object.keys(itemKind)`); one `note` has `error` true and one has not; a step is `interrupted`.
- `fixtureTurns holds every node kind a text item's tree can draw`: a recursive walk of the text items' `blocks` finds `h`, `p`, `ul`, `ol`, `li`, `quote`, `code`, `code_inline`, `em`, `strong`, `a`, `hr`, `table`, `tr`, `th`, `td` and `text`, and every `a` has an `href` starting `https://`.
- `fixtureTurns(true) is the subagent's page`: `agent?.id` is `a1f00dcafe0001`, `agent?.state` is `running`, the first turn's `from` is `brief`; `fixtureTurns()` has no `agent`.
- `the loaders fill every relative time and return a fresh copy`: `JSON.stringify` of `fixtureTurns()`, `fixtureTurns(true)`, `fixturePatch()` and both job logs holds no `"now`; the first turn's `at` is `2026-11-18T11:30:00Z`; changing the first turn's `text` in one `fixtureTurns()` leaves the next call's unchanged.
- `fixturePatch holds a deleted file and a file omitted for its length`: a file with `status` `D` and `removed` above 0, a file with `omitted` `lines` and no hunks, and the first three files' paths `ui/pane.go`, `ui/header.go`, `assets/cursor.png`.
- `fixtureJobLog is the tail and its after is empty`: `fixtureJobLog()` has non-empty `text`, `cut` true and `reset` false; `fixtureJobLog(true)` has `text` `""` and `start` equal to `end`, and that `end` equals the tail's `end`.

Fails without: `the snapshot holds session two's permission and question asks`: session 2's asks left empty, or session 1's job 216 left out. `fixtureTurns holds every item kind, both notes and an interrupted step`: the error note removed from `turns.json`. `fixtureTurns holds every node kind a text item's tree can draw`: the `table` node removed from the text item. `fixtureTurns(true) is the subagent's page`: `fixtureTurns` ignoring its argument. `the loaders fill every relative time and return a fresh copy`: a loader returning the imported JSON without `fillTimes`, or the same object on every call. `fixturePatch holds a deleted file and a file omitted for its length`: `fixturePatch` returning `fixtureSnapshot`'s data or the `D` file removed. `fixtureJobLog is the tail and its after is empty`: `fixtureJobLog(true)` returning the tail. The `askKind` row: an ask `kind` in `snapshot.json` changed to `"input"` turns the row's `outside` list non-empty.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/fixtures.test.ts`

Expected: the file fails to compile its imports (`fixtureTurns`, `fixturePatch` and `fixtureJobLog` are not exported by `./fixtures`), so every test in it is reported failed.

- [ ] **Step 3: Implement**

Add the three loaders to `fixtures.ts` as the names above describe, with a one-line doc comment each, and update the file's header comment.

- [ ] **Step 4: Run it to see it pass, then the app's gates**

Run: `pnpm -C internal/web/ui exec vitest run src/fixtures.test.ts && pnpm -C internal/web/ui exec prettier --write src/fixtures.ts src/fixtures.test.ts && pnpm -C internal/web/ui run check`

Expected: every fixtures test passes and `check` (typecheck, lint, format, the whole Vitest suite) is green, the existing fleet, store and shell tests among them against the widened snapshot.

Then the story's end-to-end check, which needs the browser `tools/webshot` finds (see `go run ./tools/webshot -h`):

Run: `d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app classic -screens conversation,waiting,info,diff -out "$d"`

Expected: it writes four screenshots and exits 0, the classic app having logged no console error for the asks, the queued job, the notes, the nodes or the two new files. `internal/web/dist/` is unchanged (`git status --short internal/web/dist` prints nothing).

- [ ] **Step 5: Commit**

```
git commit -m "test(web): typed loaders for the turns, the diff and the job log, and the ask kind's union row" -- internal/web/ui/src/fixtures.ts internal/web/ui/src/fixtures.test.ts
```

## Story 2: The command path takes an input field, a key of its own and a confirmation it may skip

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/app/commands.test.ts`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/app/CommandButton.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/components/ui/textarea.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/lib/utf8.ts`, `internal/web/ui/src/lib/utf8.test.ts`, `internal/web/dist/`
- **Interfaces:** exposes the widened `Command`, `useCommandBusy`, `CommandButton`'s `disabled`, `ConfirmSpec`, `ConfirmField`, `TextField`, `ChoiceField`, `ChoiceOption`, `askConfirm`, `Textarea`, `utf8Length`, `MAX_MESSAGE_BYTES`; uses part 1's registry, `freshContext`, `showMessage` and `Drawer`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** full (the confirmation and in-flight path every destructive command runs through: a wrong key runs Kill twice, a wrong null runs it unasked, a value read before the freshness check acts on a stale session)

Widen part 1's command registry for part 2's actions. The confirmation sheet draws an optional field, a `Textarea` whose UTF-8 limit disables the action once passed or a native `<select>`, and `askConfirm` resolves the field's value (`""` with no field) or null when the sheet is cancelled, dismissed or replaced. A command's `confirm` may return null to run with no sheet, `run` receives the value as a third argument, and a command's `key(args)` joins its in-flight key so Pause on one job does not block Pause on another. `CommandButton` gains a `disabled` prop for a caller's form state. The freshness check after a yes, the stale gate and the error toast stay as they are. No task here holds a landmine marker, so the developer runs at the default effort. Every command in the app, Kill included, runs through the path this story changes, so each task pins both sides of what it touches: the sheet's yes with `""` as well as with a value, a null from `confirm` as well as a spec, a key that differs and one that matches.

### Task 2.1: The shared UTF-8 length

**Files:**
- Create: `internal/web/ui/src/lib/utf8.ts`
- Create: `internal/web/ui/src/lib/utf8.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/lib/utf8.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { MAX_MESSAGE_BYTES, utf8Length } from "./utf8";

describe("utf8Length", () => {
  it("counts bytes, not UTF-16 units", () => {
    expect(utf8Length("")).toBe(0);
    expect(utf8Length("abc")).toBe(3);
    expect(utf8Length("é")).toBe(2);
    expect(utf8Length("€")).toBe(3);
    expect(utf8Length("😀")).toBe(4);
  });

  it("counts a lone surrogate as the three bytes of U+FFFD, as the daemon receives it", () => {
    expect(utf8Length("\ud83d")).toBe(3);
  });
});

describe("MAX_MESSAGE_BYTES", () => {
  it("is the 64 KiB the daemon holds a reply, an answer's text and a message to", () => {
    expect(MAX_MESSAGE_BYTES).toBe(65536);
  });
});
```

Fails without: utf8Length: counts bytes, not UTF-16 units: `text.length` returned in place of the encoded length (`"é"` gives 1, not 2).
Fails without: utf8Length: counts a lone surrogate: a hand-rolled count that reads a lone surrogate as 1 or 2 bytes (the daemon reads the encoded U+FFFD, 3).
Fails without: MAX_MESSAGE_BYTES: the constant changed from 65536.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/lib/utf8.test.ts`
Expected: FAIL, `Error: Failed to resolve import "./utf8" from "src/lib/utf8.test.ts". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/lib/utf8.ts`:

```ts
// MAX_MESSAGE_BYTES is the limit, in UTF-8 bytes, the daemon holds a reply, an
// answer's text and a message to (64 KiB, as the classic screens' limit).
export const MAX_MESSAGE_BYTES = 65536;

const encoder = new TextEncoder();

// utf8Length is the number of bytes text takes encoded as UTF-8, which is what
// the daemon counts: not text.length, which counts UTF-16 units.
export function utf8Length(text: string): number {
  return encoder.encode(text).length;
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/lib/utf8.test.ts`
Expected: PASS, 3 tests.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/lib/utf8.ts internal/web/ui/src/lib/utf8.test.ts
git commit -m "feat(web): utf8Length and the message limit the daemon holds" -- internal/web/ui/src/lib/utf8.ts internal/web/ui/src/lib/utf8.test.ts
```

### Task 2.2: The Textarea

**Files:**
- Create: `internal/web/ui/src/components/ui/textarea.tsx`
- Test: `internal/web/ui/src/components/ui/components.test.tsx`

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/components/ui/components.test.tsx`, after the import line

```tsx
import { Skeleton } from "./skeleton";
```

add:

```tsx
import { Textarea } from "./textarea";
```

Before the line `function SampleDrawer({` add:

```tsx
describe("Textarea", () => {
  it("Textarea: a plain textarea that takes its props, at 16px, growing with its content to 120px", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Textarea placeholder="reply" onChange={onChange} />);
    const el = screen.getByPlaceholderText("reply");
    expect(el.tagName).toBe("TEXTAREA");
    expect(el).toHaveAttribute("data-slot", "textarea");
    expect(el).toHaveClass("field-sizing-content", "max-h-[120px]", "text-[16px]");
    await user.type(el, "hi");
    expect(el).toHaveValue("hi");
    expect(onChange).toHaveBeenCalledTimes(2);
  });

  it("Textarea: a caller's class wins over the one it conflicts with", () => {
    render(<Textarea placeholder="tall" className="max-h-40" />);
    const el = screen.getByPlaceholderText("tall");
    expect(el).toHaveClass("max-h-40");
    expect(el).not.toHaveClass("max-h-[120px]");
  });
});

```

In the `describe("no <style> element"` test, replace

```tsx
        <Skeleton />
        <Toaster />
```

with

```tsx
        <Skeleton />
        <Textarea />
        <Toaster />
```

Fails without: Textarea: a plain textarea: the `field-sizing-content`, `max-h-[120px]` or `text-[16px]` class removed from the component, or `...props` not spread (the placeholder lookup finds nothing).
Fails without: Textarea: a caller's class wins: `cn` replaced by plain string joining, which leaves both `max-h` classes.
Fails without: the no-style test: a Textarea that injected a `<style>` element.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx`
Expected: FAIL, `Error: Failed to resolve import "./textarea" from "src/components/ui/components.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/components/ui/textarea.tsx`:

```tsx
import type { ComponentProps, ReactElement } from "react";

import { cn } from "@/lib/utils";

// Textarea is shadcn's Textarea on a plain <textarea>: text at 16px (iOS zooms
// into a smaller field), a 44px floor, and `field-sizing: content` so it grows
// with its text to 120px, past which it scrolls.
function Textarea({ className, ...props }: ComponentProps<"textarea">): ReactElement {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "field-sizing-content max-h-[120px] min-h-11 w-full resize-none overflow-y-auto rounded-lg border border-input bg-background px-3 py-2.5 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}

export { Textarea };
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/components/ui/components.test.tsx`
Expected: PASS, every test in the file including the two Textarea tests.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/components/ui/textarea.tsx
git commit -m "feat(web): shadcn's Textarea" -- internal/web/ui/src/components/ui/textarea.tsx internal/web/ui/src/components/ui/components.test.tsx
```

### Task 2.3: The sheet's field, and askConfirm's new result

`askConfirm` now resolves the field's value or null; this task changes the one place `runCommand` reads it, so the tree stays green. Task 2.4 widens the rest of the command path.

**Files:**
- Modify: `internal/web/ui/src/app/ConfirmSheet.tsx`
- Modify: `internal/web/ui/src/app/commands.ts`
- Test: `internal/web/ui/src/app/ConfirmSheet.test.tsx`
- Test: `internal/web/ui/src/app/commands.test.ts`
- Test: `internal/web/ui/src/app/shell.test.tsx`

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/app/ConfirmSheet.test.tsx`, replace

```tsx
function ask(spec: ConfirmSpec): Promise<boolean> {
  let answer = Promise.resolve(false);
```

with

```tsx
function ask(spec: ConfirmSpec): Promise<string | null> {
  let answer = Promise.resolve<string | null>(null);
```

Replace the test title and its expectation

```tsx
  it("names what the command does, and resolves true on its action", async () => {
```

with

```tsx
  it("names what the command does, and resolves the empty value on its action when it has no field", async () => {
```

and replace `    expect(await answer).toBe(true);` (the only line of that text) with:

```tsx
    expect(await answer).toBe("");
```

Replace

```tsx
  it("resolves false on Cancel", async () => {
```

with

```tsx
  it("resolves null on Cancel", async () => {
```

and replace

```tsx
    await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(await answer).toBe(false);
```

with

```tsx
    await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(await answer).toBeNull();
```

Replace

```tsx
  it("resolves false on Escape", async () => {
```

with

```tsx
  it("resolves null on Escape", async () => {
```

and replace

```tsx
    await userEvent.keyboard("{Escape}");
    expect(await answer).toBe(false);
```

with

```tsx
    await userEvent.keyboard("{Escape}");
    expect(await answer).toBeNull();
```

Replace

```tsx
  it("lets a newer ask replace an open one, which resolves false", async () => {
```

with

```tsx
  it("lets a newer ask replace an open one, which resolves null", async () => {
```

Replace `    expect(await first).toBe(false);` with:

```tsx
    expect(await first).toBeNull();
```

and `    expect(await second).toBe(true);` with:

```tsx
    expect(await second).toBe("");
```

Insert before the lines

```tsx
afterEach(() => {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
```

this block:

```tsx
// textSpec is a sheet with a text field whose limit is max bytes.
function textSpec(max: number): ConfirmSpec {
  return {
    title: "Deny this?",
    body: "Claude is told no.",
    action: "Deny",
    destructive: true,
    field: {
      kind: "text",
      label: "Tell Claude why",
      placeholder: "optional",
      max,
      tooLong: "That is over the limit.",
    },
  };
}

const merge: ConfirmSpec = {
  title: "Merge",
  body: "Merges one session into this one.",
  action: "Merge",
  destructive: false,
  field: {
    kind: "choice",
    label: "Merge from",
    options: [
      { value: "4", label: "fix-a (aidanb/fix-a)" },
      { value: "9", label: "fix-b (aidanb/fix-b)" },
    ],
  },
};

describe("ConfirmSheet's field", () => {
  it("a text field shows its label and placeholder and resolves what was typed, as typed", async () => {
    render(<ConfirmSheet />);
    const answer = ask(textSpec(100));

    const field = await screen.findByLabelText("Tell Claude why");
    expect(field).toHaveAttribute("placeholder", "optional");
    await userEvent.type(field, "a b ");
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));

    expect(await answer).toBe("a b ");
  });

  it("a text field left empty resolves the empty string, which is an answer and not a cancel", async () => {
    render(<ConfirmSheet />);
    const answer = ask(textSpec(100));

    await userEvent.click(await screen.findByRole("button", { name: "Deny" }));

    expect(await answer).toBe("");
  });

  it("a text field counts UTF-8 bytes: at the limit the action is on, one byte past it the action is off and the too-long line shows", async () => {
    render(<ConfirmSheet />);
    const answer = ask(textSpec(4));
    const field = await screen.findByLabelText("Tell Claude why");

    await userEvent.type(field, "éé");
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
    expect(screen.queryByText("That is over the limit.")).toBeNull();

    await userEvent.type(field, "a");
    expect(screen.getByRole("button", { name: "Deny" })).toBeDisabled();
    expect(screen.getByText("That is over the limit.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));

    await userEvent.type(field, "{Backspace}");
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
    expect(screen.queryByText("That is over the limit.")).toBeNull();

    // The disabled click resolved nothing: the sheet is still open to cancel.
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await answer).toBeNull();
  });

  it("cancelling with text typed resolves null, not the text", async () => {
    render(<ConfirmSheet />);
    const answer = ask(textSpec(100));

    await userEvent.type(await screen.findByLabelText("Tell Claude why"), "abc");
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(await answer).toBeNull();
  });

  it("a choice field is a native select with its first option chosen, and resolves that option's value", async () => {
    render(<ConfirmSheet />);
    const answer = ask(merge);

    const select = await screen.findByLabelText("Merge from");
    expect(select.tagName).toBe("SELECT");
    expect(select).toHaveValue("4");
    expect(screen.getByRole("option", { name: "fix-b (aidanb/fix-b)" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    expect(await answer).toBe("4");
  });

  it("a choice field resolves the option that was picked", async () => {
    render(<ConfirmSheet />);
    const answer = ask(merge);

    await userEvent.selectOptions(await screen.findByLabelText("Merge from"), "9");
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    expect(await answer).toBe("9");
  });

  it("a newer ask starts with a field of its own, not the text typed into the one it replaced", async () => {
    render(<ConfirmSheet />);
    const first = ask(textSpec(100));
    await userEvent.type(await screen.findByLabelText("Tell Claude why"), "old");

    const second = ask({ ...textSpec(100), title: "Chat about this" });

    expect(await screen.findByText("Chat about this")).toBeInTheDocument();
    expect(screen.getByLabelText("Tell Claude why")).toHaveValue("");
    expect(await first).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));
    expect(await second).toBe("");
  });

  it("a choice field with no options leaves the action off, with Cancel still open", async () => {
    render(<ConfirmSheet />);
    const answer = ask({ ...merge, field: { kind: "choice", label: "Merge from", options: [] } });

    expect(await screen.findByLabelText("Merge from")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Merge" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await answer).toBeNull();
  });

  it("a sheet with no field draws no input", async () => {
    render(<ConfirmSheet />);
    const answer = ask(kill);
    await screen.findByText("Kill session");

    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("combobox")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await answer).toBeNull();
  });
});

```

Fails without: names what the command does, and resolves the empty value: `askConfirm` still resolving `true` on the action.
Fails without: resolves null on Cancel, resolves null on Escape, lets a newer ask replace an open one: `answer(false)` in the cancel, dismiss and replace paths, left as `false` instead of `null`.
Fails without: a text field shows its label and placeholder and resolves what was typed: the field not drawn, or its value trimmed or replaced by `""` when resolving.
Fails without: a text field left empty resolves the empty string: `answer(value === "" ? null : value)`, which reads an empty field as a cancel.
Fails without: a text field counts UTF-8 bytes: the limit compared with `value.length` (`"éé"` is 2, not 4, so the byte count never goes over at `"ééa"` = 5 bytes), `>=` in place of `>` at the boundary (the action goes off at exactly 4 bytes), or the action not disabled past the limit.
Fails without: cancelling with text typed: `answer(value)` on the cancel path.
Fails without: a choice field is a native select: the select drawn as another element, or no option chosen by default (`firstValue` returning `""`).
Fails without: a choice field resolves the option that was picked: the select's `onChange` not writing the value.
Fails without: a newer ask starts with a field of its own: the `key={ask.id}` removed from `AskBody`, so React keeps the typed text across asks.
Fails without: a choice field with no options: the `empty` term removed from the action's `disabled` (the action is on and would resolve `""`).
Fails without: a sheet with no field draws no input: a field drawn for every spec.

In `internal/web/ui/src/app/commands.test.ts`, replace each mocked sheet answer to the new result (a yes is the field's value, `""` with no field; a decline is null):

Replace `    vi.mocked(askConfirm).mockResolvedValue(true);` with:

```ts
    vi.mocked(askConfirm).mockResolvedValue("");
```

Replace `    vi.mocked(askConfirm).mockResolvedValue(false);` with:

```ts
    vi.mocked(askConfirm).mockResolvedValue(null);
```

Replace

```ts
      useFleet.getState().setLink("offline");
      return Promise.resolve(true);
```

with

```ts
      useFleet.getState().setLink("offline");
      return Promise.resolve("");
```

Replace `    vi.mocked(askConfirm).mockResolvedValueOnce(true);` with:

```ts
    vi.mocked(askConfirm).mockResolvedValueOnce("");
```

Replace

```ts
      useFleet.setState({ snapshot: { ...snapshot, sessions } });
      return Promise.resolve(true);
```

with

```ts
      useFleet.setState({ snapshot: { ...snapshot, sessions } });
      return Promise.resolve("");
```

Replace

```ts
      useFleet.setState({ snapshot: { ...snapshot, sessions: {} } });
      return Promise.resolve(true);
```

with

```ts
      useFleet.setState({ snapshot: { ...snapshot, sessions: {} } });
      return Promise.resolve("");
```

Fails without: asks the sheet first, with the spec built from the context and arguments, and runs on yes: `runCommand` reading the sheet's `""` as a decline (`!value`) in place of `=== null`, so the empty value of a sheet with no field runs nothing.

In `internal/web/ui/src/app/shell.test.tsx`, replace

```tsx
    let answer = Promise.resolve(true);
```

with

```tsx
    let answer = Promise.resolve<string | null>("");
```

and replace

```tsx
    expect(await answer).toBe(false);
```

with

```tsx
    expect(await answer).toBeNull();
```

Fails without: mounts the confirmation sheet and the toaster once: a Cancel that resolves anything but null.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/ConfirmSheet.test.tsx src/app/commands.test.ts src/app/shell.test.tsx`
Expected: FAIL. ConfirmSheet's tests print `AssertionError: expected true to be ''` and `expected false to be null` (`askConfirm` still resolves a boolean), and the field tests print `Unable to find a label with the text of: Tell Claude why`; commands.test's "runs on yes" fails with `expected "spy" to be called 1 times, but got 0 times`.

- [ ] **Step 3: Implement**

Replace the whole contents of `internal/web/ui/src/app/ConfirmSheet.tsx` with the following (the drawer, its exit behaviour and the buttons are part 1's; the field, the ask's id and the value are new):

```tsx
import { useId, useState, useSyncExternalStore, type ReactElement } from "react";

import { Button } from "@/components/ui/button";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerFooter,
  DrawerHeader,
  DrawerTitle,
} from "@/components/ui/drawer";
import { Textarea } from "@/components/ui/textarea";
import { utf8Length } from "@/lib/utf8";

// TextField is a sheet's free text (Chat about this, Deny): `max` is its limit
// in UTF-8 bytes, past which the action is off and `tooLong` shows.
export interface TextField {
  kind: "text";
  label: string;
  placeholder: string;
  max: number;
  tooLong: string;
}

// ChoiceOption is one option of a ChoiceField: its `value` is what the sheet
// resolves, its `label` what the person reads.
export interface ChoiceOption {
  value: string;
  label: string;
}

// ChoiceField is a pick of one option (Merge's source session), drawn as a
// native <select> with the first option chosen.
export interface ChoiceField {
  kind: "choice";
  label: string;
  options: ChoiceOption[];
}

export type ConfirmField = TextField | ChoiceField;

// ConfirmSpec is what a sheet says: what the command does (`body` may run
// to several lines: the worktree's path, the tmux session), the action's
// label, whether the action is destructive (drawn as such), and at most one
// field whose value the action resolves.
export interface ConfirmSpec {
  title: string;
  body: string;
  action: string;
  destructive: boolean;
  field?: ConfirmField;
}

interface Ask {
  id: number;
  spec: ConfirmSpec;
  resolve: (value: string | null) => void;
}

// current is the open ask: at most one sheet is open at a time.
let current: Ask | null = null;
let seq = 0;
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// askConfirm opens the confirmation sheet and resolves the field's value when
// the action is chosen (a text field's as typed, "" for a sheet with no
// field), and null when the sheet is cancelled, dismissed or replaced by a
// newer ask.
export function askConfirm(spec: ConfirmSpec): Promise<string | null> {
  current?.resolve(null);
  return new Promise<string | null>((resolve) => {
    seq += 1;
    current = { id: seq, spec, resolve };
    emit();
  });
}

// answer resolves the ask with that id, and does nothing when it is no longer
// the open one: a sheet's action, Cancel and dismissal answer their own ask,
// never a newer one that has since replaced it.
function answer(id: number, value: string | null): void {
  const ask = current;
  if (ask === null || ask.id !== id) return;
  current = null;
  emit();
  ask.resolve(value);
}

// firstValue is the value a field starts with: a text field's empty text, a
// choice's first option.
function firstValue(field: ConfirmField | undefined): string {
  return field?.kind === "choice" ? (field.options[0]?.value ?? "") : "";
}

// Sheet is one ask's drawer content. A newer ask replaces the text in place,
// inside the one DrawerContent, so the drawer's transition and focus trap do
// not restart.
function Sheet({ ask }: { ask: Ask }): ReactElement {
  return (
    <DrawerContent>
      <DrawerHeader>
        <DrawerTitle>{ask.spec.title}</DrawerTitle>
        <DrawerDescription className="break-words whitespace-pre-line">
          {ask.spec.body}
        </DrawerDescription>
      </DrawerHeader>
      <AskBody key={ask.id} ask={ask} />
    </DrawerContent>
  );
}

// AskBody is the ask's field and buttons. Sheet keys it by the ask, so each
// ask starts with a field of its own; its buttons answer that ask by its id.
function AskBody({ ask }: { ask: Ask }): ReactElement {
  const { spec } = ask;
  const [value, setValue] = useState(() => firstValue(spec.field));
  const fieldId = useId();
  const field = spec.field;
  const over = field !== undefined && field.kind === "text" && utf8Length(value) > field.max;
  // A choice with no options has nothing to resolve: its action stays off.
  const empty = field !== undefined && field.kind === "choice" && field.options.length === 0;
  return (
    <>
      {field === undefined ? null : (
        <div className="mt-3 flex flex-col gap-1.5">
          <label htmlFor={fieldId} className="text-[12.5px] font-semibold text-muted-foreground">
            {field.label}
          </label>
          {field.kind === "text" ? (
            <>
              <Textarea
                id={fieldId}
                value={value}
                placeholder={field.placeholder}
                onChange={(event) => {
                  setValue(event.target.value);
                }}
              />
              {over ? (
                <p role="alert" className="text-[12.5px] text-error">
                  {field.tooLong}
                </p>
              ) : null}
            </>
          ) : (
            <select
              id={fieldId}
              value={value}
              className="h-11 w-full rounded-lg border border-input bg-background px-3 text-[16px] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
              onChange={(event) => {
                setValue(event.target.value);
              }}
            >
              {field.options.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          )}
        </div>
      )}
      <DrawerFooter>
        <Button
          size="lg"
          variant={spec.destructive ? "destructive" : "default"}
          disabled={over || empty}
          onClick={() => {
            answer(ask.id, value);
          }}
        >
          {spec.action}
        </Button>
        <Button
          size="lg"
          variant="outline"
          onClick={() => {
            answer(ask.id, null);
          }}
        >
          Cancel
        </Button>
      </DrawerFooter>
    </>
  );
}

// ConfirmSheet is the one confirmation drawer: AppShell mounts it once, and
// every command with a `confirm` opens it through runCommand. The last ask
// stays drawn after an answer, until the drawer's close has finished and it
// unmounts its content, so the exit transition plays over the sheet and not
// over an empty one.
export function ConfirmSheet(): ReactElement {
  const ask = useSyncExternalStore(subscribe, () => current);
  const [last, setLast] = useState<Ask | null>(null);
  if (ask !== null && ask !== last) setLast(ask);
  const shown = ask ?? last;
  return (
    <Drawer
      open={ask !== null}
      onOpenChange={(open) => {
        if (!open && ask !== null) answer(ask.id, null);
      }}
    >
      {shown === null ? null : <Sheet ask={shown} />}
    </Drawer>
  );
}
```

In `internal/web/ui/src/app/commands.ts`, replace the one line

```ts
      if (!(await askConfirm(command.confirm(ctx, args)))) return;
```

with:

```ts
      if ((await askConfirm(command.confirm(ctx, args))) === null) return;
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/app src/components`
Expected: PASS, typecheck clean and every test of `src/app` and `src/components`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the confirmation sheet takes a text or a choice field" -- internal/web/ui/src/app/ConfirmSheet.tsx internal/web/ui/src/app/ConfirmSheet.test.tsx internal/web/ui/src/app/commands.ts internal/web/ui/src/app/commands.test.ts internal/web/ui/src/app/shell.test.tsx
```

### Task 2.4: The command's key, a confirm that may skip, the sheet's input, and CommandButton's disabled

**Files:**
- Modify: `internal/web/ui/src/app/commands.ts`
- Modify: `internal/web/ui/src/app/CommandButton.tsx`
- Test: `internal/web/ui/src/app/commands.test.ts`
- Test: `internal/web/ui/src/app/ConfirmSheet.test.tsx`

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/app/commands.test.ts`, replace

```ts
    const run = vi.fn<(ctx: CommandContext, args: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    const command = defineCommand<string>({
      id: "test.run",
```

with

```ts
    const run = vi.fn<(ctx: CommandContext, args: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    const command = defineCommand<string>({
      id: "test.run",
```

Replace `    expect(run).toHaveBeenCalledWith(context, "args");` with:

```ts
    expect(run).toHaveBeenCalledWith(context, "args", "");
```

Replace

```ts
    const run = vi.fn<(ctx: CommandContext, note: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
```

with

```ts
    const run = vi.fn<(ctx: CommandContext, note: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
```

Replace `    expect(run).toHaveBeenCalledWith(context, "hello");` with:

```ts
    expect(run).toHaveBeenCalledWith(context, "hello", "");
```

Replace `    const busy = renderHook(() => useCommandBusy(command, ctx()));` with:

```ts
    const busy = renderHook(() => useCommandBusy(command, ctx(), undefined));
```

Insert before the line `  it("clears the in-flight mark when the run throws", async () => {`:

```ts
  it("gives run the sheet's value as its input", async () => {
    vi.mocked(askConfirm).mockResolvedValue("because");
    useFleet.setState({ stale: false, link: "live" });
    const run = vi.fn<(ctx: CommandContext, id: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    const command = defineCommand<string>({
      id: "test.input",
      title: "Deny",
      when: () => true,
      confirm: () => ({
        title: "Deny this?",
        body: "",
        action: "Deny",
        destructive: true,
        field: { kind: "text", label: "Why", placeholder: "", max: 100, tooLong: "too long" },
      }),
      run,
    });
    const context = ctx();

    await runCommand(command, context, "ask-1");

    expect(run).toHaveBeenCalledOnce();
    expect(run).toHaveBeenCalledWith(context, "ask-1", "because");
  });

  it("runs with no sheet and an empty input when confirm returns null, and asks when it returns a spec", async () => {
    useFleet.setState({ stale: false, link: "live" });
    vi.mocked(askConfirm).mockResolvedValue("yes");
    const run = vi.fn<(ctx: CommandContext, persists: boolean, input: string) => Promise<void>>(
      () => Promise.resolve(),
    );
    const command = defineCommand<boolean>({
      id: "test.nullConfirm",
      title: "Allow",
      when: () => true,
      confirm: (_ctx, persists) =>
        persists
          ? { title: "Save this rule?", body: "", action: "Save", destructive: false }
          : null,
      run,
    });
    const context = ctx();

    await runCommand(command, context, false);
    expect(askConfirm).not.toHaveBeenCalled();
    expect(run).toHaveBeenCalledOnce();
    expect(run).toHaveBeenLastCalledWith(context, false, "");

    await runCommand(command, context, true);
    expect(askConfirm).toHaveBeenCalledOnce();
    expect(run).toHaveBeenCalledTimes(2);
    expect(run).toHaveBeenLastCalledWith(context, true, "yes");
  });

  it("keeps the in-flight mark per key: the same key is held, another key runs, and each key's busy state is its own", async () => {
    const finishers: (() => void)[] = [];
    const run = vi.fn<(ctx: CommandContext, args: { job: number }, input: string) => Promise<void>>(
      () =>
        new Promise<void>((resolve) => {
          finishers.push(resolve);
        }),
    );
    const command = defineCommand<{ job: number }>({
      id: "test.keyed",
      title: "Park",
      when: () => true,
      key: (args) => `job${args.job}`,
      run,
    });
    const one = renderHook(() => useCommandBusy(command, ctx(), { job: 1 }));
    const two = renderHook(() => useCommandBusy(command, ctx(), { job: 2 }));
    const three = renderHook(() => useCommandBusy(command, ctx(), { job: 3 }));

    let first: Promise<void> = Promise.resolve();
    let second: Promise<void> = Promise.resolve();
    act(() => {
      first = runCommand(command, ctx(), { job: 1 });
    });
    await runCommand(command, ctx(), { job: 1 });
    expect(run).toHaveBeenCalledOnce();

    act(() => {
      second = runCommand(command, ctx(), { job: 2 });
    });
    expect(run).toHaveBeenCalledTimes(2);
    expect(one.result.current).toBe(true);
    expect(two.result.current).toBe(true);
    expect(three.result.current).toBe(false);

    await act(async () => {
      for (const finish of finishers) finish();
      await Promise.all([first, second]);
    });
    expect(one.result.current).toBe(false);
    expect(two.result.current).toBe(false);
  });

  it("holds the same key for another session apart: the key joins the session's, it does not replace it", async () => {
    const snapshot = fixtureSnapshot();
    const sessions = allSessions(snapshot);
    const a = sessions[0];
    const b = sessions[1];
    if (a === undefined || b === undefined)
      throw new Error("the fixture holds fewer than two sessions");
    const finishers: (() => void)[] = [];
    const run = vi.fn<(ctx: CommandContext, args: { job: number }, input: string) => Promise<void>>(
      () =>
        new Promise<void>((resolve) => {
          finishers.push(resolve);
        }),
    );
    const command = defineCommand<{ job: number }>({
      id: "test.keyedSessions",
      title: "Park",
      when: () => true,
      key: (args) => `job${args.job}`,
      run,
    });

    let first: Promise<void> = Promise.resolve();
    let second: Promise<void> = Promise.resolve();
    act(() => {
      first = runCommand(command, ctx({ session: a }), { job: 1 });
      second = runCommand(command, ctx({ session: b }), { job: 1 });
    });
    expect(run).toHaveBeenCalledTimes(2);

    await act(async () => {
      for (const finish of finishers) finish();
      await Promise.all([first, second]);
    });
  });

  it("treats a key that throws as no key: running the command and reading its busy state neither reject nor throw", async () => {
    const run = vi.fn<(ctx: CommandContext, args: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    const command = defineCommand<string>({
      id: "test.keyThrows",
      title: "Park",
      when: () => true,
      key: () => {
        throw new Error("no job to key on");
      },
      run,
    });

    const busy = renderHook(() => useCommandBusy(command, ctx(), "args"));
    expect(busy.result.current).toBe(false);
    await expect(runCommand(command, ctx(), "args")).resolves.toBeUndefined();
    await expect(runCommand(command, ctx(), "args")).resolves.toBeUndefined();

    expect(run).toHaveBeenCalledTimes(2);
  });

```

Fails without: gives run the sheet's value as its input: `run` called with two arguments, or with `""` in place of the sheet's value.
Fails without: runs with no sheet and an empty input when confirm returns null: the null passed to `askConfirm` (the not-called assertion fails), or the `spec !== null` test removed so a spec never asks; both sides are pinned by the two calls.
Fails without: keeps the in-flight mark per key: `busyKey` ignoring `command.key` (the job 2 run is dropped and the run count stays 1), or `useCommandBusy` computing its key without the `args` (`key(undefined)` throws on `args.job`, which counts as no key, so job 1's hook reads not busy and its `toBe(true)` fails).
Fails without: treats a key that throws as no key: `ownKey` calling `command.key(args)` outside its `try` (the busy hook throws while rendering and `runCommand` rejects before its own `try`).
Fails without: holds the same key for another session apart: `busyKey` dropping the session's id once a key exists (the second session's run is dropped).
Fails without: asks the sheet first, with the spec built from the context and arguments, and runs on yes: `command.run(at, args)` without the input, so the three-argument assertion fails.

In `internal/web/ui/src/app/ConfirmSheet.test.tsx`, insert before the line `  it("fails loudly for a command nothing defined", () => {`:

```tsx
  it("is off while the caller's disabled is set, and a caller's false does not turn on what the command refuses", async () => {
    live();
    const run = vi.fn(() => Promise.resolve());
    defineCommand({ id: "test.button.callerOff", title: "Send", when: () => true, run });
    defineCommand({ id: "test.button.callerNo", title: "Never", when: () => false, run });

    const { rerender } = render(<CommandButton id="test.button.callerOff" disabled />);
    const button = screen.getByRole("button", { name: "Send" });
    expect(button).toBeDisabled();
    await userEvent.click(button);
    expect(run).not.toHaveBeenCalled();

    rerender(<CommandButton id="test.button.callerOff" disabled={false} />);
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
    rerender(<CommandButton id="test.button.callerOff" />);
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();

    rerender(<CommandButton id="test.button.callerNo" disabled={false} />);
    expect(screen.getByRole("button", { name: "Never" })).toBeDisabled();
  });

  it("goes off only for its own key while a keyed command runs", async () => {
    live();
    const finishers: (() => void)[] = [];
    const run = vi.fn<(ctx: CommandContext, job: number) => Promise<void>>(
      () =>
        new Promise<void>((resolve) => {
          finishers.push(resolve);
        }),
    );
    defineCommand<number>({
      id: "test.button.keyed",
      title: "Park",
      when: () => true,
      key: (job) => `job${job}`,
      run,
    });

    render(
      <>
        <CommandButton id="test.button.keyed" args={1} label="Park 1" />
        <CommandButton id="test.button.keyed" args={2} label="Park 2" />
      </>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Park 1" }));

    expect(screen.getByRole("button", { name: "Park 1" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Park 2" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "Park 2" }));
    expect(run).toHaveBeenCalledTimes(2);

    act(() => {
      for (const finish of finishers) finish();
    });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Park 1" })).toBeEnabled();
    });
  });

  it("draws and runs a keyed command given no args: the throwing key is no key, not a crash or an unhandled rejection", async () => {
    live();
    const run = vi.fn<(ctx: CommandContext, job: number) => Promise<void>>(() =>
      Promise.reject(new Error("no job")),
    );
    defineCommand<number>({
      id: "test.button.keyNoArgs",
      title: "Park",
      when: () => true,
      key: (job) => `job${job.toFixed()}`,
      run,
    });

    render(<CommandButton id="test.button.keyNoArgs" />);
    const button = screen.getByRole("button", { name: "Park" });
    expect(button).toBeEnabled();
    await userEvent.click(button);

    await waitFor(() => {
      expect(run).toHaveBeenCalledOnce();
    });
  });

  it("gives run the value typed into the sheet's field, and runs with an empty input when the confirm returns null", async () => {
    live();
    const run = vi.fn<(ctx: CommandContext, id: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    defineCommand<string>({
      id: "test.button.field",
      title: "Deny",
      when: () => true,
      confirm: (_ctx, id) =>
        id === "skip"
          ? null
          : {
              title: "Deny this?",
              body: "Claude is told no.",
              action: "Deny it",
              destructive: true,
              field: {
                kind: "text",
                label: "Tell Claude why",
                placeholder: "",
                max: 100,
                tooLong: "too long",
              },
            },
      run,
    });

    const { rerender } = render(
      <>
        <ConfirmSheet />
        <CommandButton id="test.button.field" args="ask-1" />
      </>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));
    await userEvent.type(await screen.findByLabelText("Tell Claude why"), "not now");
    await userEvent.click(screen.getByRole("button", { name: "Deny it" }));
    await waitFor(() => {
      expect(run).toHaveBeenCalledOnce();
    });
    expect(run.mock.calls[0]?.[1]).toBe("ask-1");
    expect(run.mock.calls[0]?.[2]).toBe("not now");
    await waitFor(() => {
      expect(screen.queryByText("Deny this?")).toBeNull();
    });

    rerender(
      <>
        <ConfirmSheet />
        <CommandButton id="test.button.field" args="skip" />
      </>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));
    await waitFor(() => {
      expect(run).toHaveBeenCalledTimes(2);
    });
    expect(run.mock.calls[1]?.[2]).toBe("");
    expect(screen.queryByText("Deny this?")).toBeNull();
  });

```

Fails without: is off while the caller's disabled is set: `disabled` left out of the button's `disabled` expression (the first assertion fails), or replacing the command's own enablement instead of joining it (the `Never` button turns on).
Fails without: goes off only for its own key: `useCommandBusy` called without the button's `args` (the hook reads the key `jobundefined`, which no run holds, so Park 1's `toBeDisabled` fails).
Fails without: draws and runs a keyed command given no args: the key's call outside a guard (`job.toFixed()` on the missing args throws while the button renders, or `runCommand` rejects unhandled instead of toasting `run`'s error).
Fails without: gives run the value typed into the sheet's field: `run`'s input not passed, or passed before the sheet resolves; and the `args="skip"` half: `confirm`'s null asked of the sheet (the "Deny this?" sheet opens) or `run` given the previous value.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/app/commands.test.ts src/app/ConfirmSheet.test.tsx`
Expected: FAIL. commands.test prints `expected "spy" to be called with arguments: [ ..., "args", "" ]` with `Received` showing two arguments, the null-confirm test fails on `expected "spy" to not be called at all`, the keyed test on `expected "spy" to be called 2 times, but got 1 times`; the button tests fail on `expected element to be enabled` and `Unable to find an accessible element with the role "button" and name "Park 1"`-style assertions (the key and the `disabled` prop do not exist yet). The two throwing-key tests pass here, since a key nothing reads cannot throw; they guard the implementation below, which is the first code to call it.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/app/commands.ts`, replace the lines

```ts
  offline?: boolean;
  when: (ctx: CommandContext) => boolean;
  confirm?: (ctx: CommandContext, args: A) => ConfirmSpec;
  run: (ctx: CommandContext, args: A) => Promise<void>;
}
```

with

```ts
  offline?: boolean;
  // key names what the args pick (a job's id, an ask's id): it joins the
  // command's id and the session's id in the in-flight key, so Pause on one
  // job does not hold Pause on another. Without it the key is the command's
  // id and the session's, and so it is when `key` throws (a keyed command
  // drawn or run without its args): the throw never escapes, and `run` meets
  // the missing args itself.
  key?: (args: A) => string;
  when: (ctx: CommandContext) => boolean;
  // confirm is the sheet's spec; null runs `run` with no sheet and an empty input.
  confirm?: (ctx: CommandContext, args: A) => ConfirmSpec | null;
  // run gets the sheet's field value as `input` ("" with no field or no sheet).
  run: (ctx: CommandContext, args: A, input: string) => Promise<void>;
}
```

Replace the lines

```ts
// inflight is the set of commands being run, each keyed by its id and the
// session it acts on: a second call while one is in flight does nothing, so a
```

with

```ts
// inflight is the set of commands being run, each keyed by its id, the
// session it acts on and what its `key` returns for the args: a second call
// while one is in flight does nothing, so a
```

Replace

```ts
function busyKey(command: { id: string }, ctx: CommandContext): string {
  return `${command.id}\u0000${ctx.session === undefined ? "" : ctx.session.id}`;
}

// useCommandBusy reports whether the command is being run for ctx's session.
export function useCommandBusy<A>(command: Command<A>, ctx: CommandContext): boolean {
  const key = busyKey(command, ctx);
```

with

```ts
// ownKey is what the command's `key` gives for args: "" without a `key`, and
// "" when it throws, so neither runCommand (before its try) nor a button's
// render can be taken down by a `key` that was handed no args.
function ownKey<A>(command: Command<A>, args: A): string {
  if (command.key === undefined) return "";
  try {
    return command.key(args);
  } catch {
    return "";
  }
}

function busyKey<A>(command: Command<A>, ctx: CommandContext, args: A): string {
  const session = ctx.session === undefined ? "" : ctx.session.id;
  return `${command.id}\u0000${session}\u0000${ownKey(command, args)}`;
}

// useCommandBusy reports whether the command is being run for ctx's session
// and the args' key.
export function useCommandBusy<A>(command: Command<A>, ctx: CommandContext, args: A): boolean {
  const key = busyKey(command, ctx, args);
```

Replace the comment and the start of `runCommand`'s body

```ts
// runCommand runs a command in ctx with args: nothing when it is not enabled
// or when the same command is already running for the same session; else,
// when it has a `confirm`, the confirmation sheet first (a declined sheet runs
// nothing), then the enablement again against the store's current state (a
// yes given after the view went stale, or the session changed, toasts that
// nothing was done), then its `run` with that fresh context. A `run` that
// throws is an error toast, never an unhandled rejection.
```

with

```ts
// runCommand runs a command in ctx with args: nothing when it is not enabled
// or when the same command is already running for the same session and key;
// else, when its `confirm` gives a spec, the confirmation sheet first (a
// declined sheet runs nothing; a `confirm` that gives null runs with no sheet
// and an empty input), then the enablement again against the store's current
// state (a yes given after the view went stale, or the session changed, toasts
// that nothing was done), then its `run` with that fresh context and the
// sheet's value. A `run` that throws is an error toast, never an unhandled
// rejection.
```

Replace

```ts
  const key = busyKey(command, ctx);
  if (inflight.has(key)) return;
```

with

```ts
  const key = busyKey(command, ctx, args);
  if (inflight.has(key)) return;
```

Replace

```ts
    let at = ctx;
    if (command.confirm !== undefined) {
      if ((await askConfirm(command.confirm(ctx, args))) === null) return;
      at = freshContext(ctx);
      if (!commandEnabled(command, at)) {
        showMessage("loom changed while you were deciding, so nothing was done.", true);
        return;
      }
    }
    await command.run(at, args);
```

with

```ts
    let at = ctx;
    let input = "";
    const spec = command.confirm === undefined ? null : command.confirm(ctx, args);
    if (spec !== null) {
      const value = await askConfirm(spec);
      if (value === null) return;
      input = value;
      at = freshContext(ctx);
      if (!commandEnabled(command, at)) {
        showMessage("loom changed while you were deciding, so nothing was done.", true);
        return;
      }
    }
    await command.run(at, args, input);
```

In `internal/web/ui/src/app/CommandButton.tsx`, replace the comment line and the props

```tsx
// every button of the command goes off together and a double tap runs it
// once). Confirmation and toasts are runCommand's, so every command behaves
// alike.
export function CommandButton({
  id,
  args,
  variant,
  size,
  label,
}: {
  id: string;
  args?: unknown;
```

with

```tsx
// every button of the command and key goes off together and a double tap runs
// it once). `disabled` is the caller's own form state (an empty reply, an
// unanswered question), joined to the command's enablement and never a
// replacement for it. Confirmation and toasts are runCommand's, so every
// command behaves alike.
export function CommandButton({
  id,
  args,
  disabled,
  variant,
  size,
  label,
}: {
  id: string;
  args?: unknown;
  disabled?: boolean;
```

Replace `  const busy = useCommandBusy(command, ctx);` with:

```tsx
  const busy = useCommandBusy(command, ctx, args);
```

Replace `      disabled={busy || !commandEnabled(command, ctx)}` with:

```tsx
      disabled={busy || disabled === true || !commandEnabled(command, ctx)}
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/app src/components src/lib`
Expected: PASS, typecheck clean and every test of the three directories.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): a command's key, a confirm that may skip the sheet, the sheet's input and a button's disabled" -- internal/web/ui/src/app/commands.ts internal/web/ui/src/app/commands.test.ts internal/web/ui/src/app/CommandButton.tsx internal/web/ui/src/app/ConfirmSheet.test.tsx
```

### Task 2.5: Rebuild the bundle

**Files:**
- Modify: `internal/web/dist/`
- Modify, only if `prettier --write` reformats them: the story's source and test files, `internal/web/ui/src/lib/utf8.ts`, `internal/web/ui/src/lib/utf8.test.ts`, `internal/web/ui/src/components/ui/textarea.tsx`, `internal/web/ui/src/components/ui/components.test.tsx`, `internal/web/ui/src/app/ConfirmSheet.tsx`, `internal/web/ui/src/app/ConfirmSheet.test.tsx`, `internal/web/ui/src/app/commands.ts`, `internal/web/ui/src/app/commands.test.ts`, `internal/web/ui/src/app/CommandButton.tsx`, `internal/web/ui/src/app/shell.test.tsx`

- [ ] **Step 1: Rebuild**

Run: `pnpm -C internal/web/ui run build`
Expected: vite prints the built assets and exits 0; `git status --short internal/web/dist` lists the changed entry script, stylesheet and, if its hash moved, `index.html`.

- [ ] **Step 2: Run the story's gates**

Run: `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
If `format:check` flags the new code, run `pnpm -C internal/web/ui exec prettier --write` on the story's files, rerun the gates and amend nothing already committed: `git status --short internal/web/ui/src` lists the files it reformatted, and Step 3 commits them with the bundle. Expected: `check` passes (typecheck, lint, format check, every Vitest test, the worker's syntax check), `dist:check` prints that the committed bundle is fresh, and `go test ./internal/web/` prints `ok` (it reads the committed bundle, so `TestDist_FirstLoadIsWithinBudget` and `TestDist_TheSkinDefinesEveryToken` run over the rebuilt one).

- [ ] **Step 3: Commit**

```bash
git add -N internal/web/dist
git commit -m "build(web): rebuild the app's bundle with the widened command path" -- internal/web/dist internal/web/ui/src/lib/utf8.ts internal/web/ui/src/lib/utf8.test.ts internal/web/ui/src/components/ui/textarea.tsx internal/web/ui/src/components/ui/components.test.tsx internal/web/ui/src/app/ConfirmSheet.tsx internal/web/ui/src/app/ConfirmSheet.test.tsx internal/web/ui/src/app/commands.ts internal/web/ui/src/app/commands.test.ts internal/web/ui/src/app/CommandButton.tsx internal/web/ui/src/app/shell.test.tsx
```

The source and test paths are in the pathspec so that a `prettier --write` of Step 2 lands in this commit; when Step 2 reformatted nothing they carry no change and the commit holds the bundle alone.

## Story 3: The drafts slice, and the session, ask and job commands

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/store/drafts.ts`, `internal/web/ui/src/store/drafts.test.ts`, `internal/web/ui/src/store/fleet.ts`, `internal/web/ui/src/store/startFleet.test.ts`, `internal/web/ui/src/commands/index.ts`, `internal/web/ui/src/commands/index.test.ts`, `internal/web/ui/src/commands/testkit.ts`, `internal/web/ui/src/commands/session.ts`, `internal/web/ui/src/commands/session.test.ts`, `internal/web/ui/src/commands/asks.ts`, `internal/web/ui/src/commands/asks.test.ts`, `internal/web/ui/src/commands/jobs.ts`, `internal/web/ui/src/commands/jobs.test.ts`, `internal/web/ui/src/main.tsx`, `internal/web/ui/CLAUDE.md`, `internal/web/CLAUDE.md`, `internal/web/dist/`
- **Interfaces:** exposes `QuestionDraft`, `AskDraft`, `DraftsState`, `useDrafts`, `SessionActions`, `sessionActions`, `AllowArgs`, `allowArgs`, `answerBody`, and the commands `session.send`, `session.pause`, `session.resume`, `session.push`, `session.merge`, `session.kill`, `ask.answer`, `ask.chat`, `ask.allow`, `ask.deny`, `job.start`, `job.park`, `job.unpark`, `job.pause`, `job.resume`; uses Story 2's command path and fields, part 1's `api`, `showOutcome`, `showMessage`, `hrefFor`, `allSessions` and `startFleet`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
- **Review:** deep
- **Form:** full (the owner, 2026-11-19, spec Part 2 settlement 19: the bodies sent to the daemon, Allow's 1-based suggestion and its persist sheet, the drafts' 200-only clearing, the per-ask and per-job keys and Kill's navigation)

Add the in-memory drafts slice (a reply per session, an answer per ask, cleared on unpair) and register every part 2 action as a command on Story 2's widened path: send, pause, resume, push, merge and kill on a session, with the classic sheets and words and the classic availability as `sessionActions`; answer, chat, allow and deny on an ask; start, park, unpark, pause and resume on a job. The ask commands come with the two pure helpers the permission card and the question form call, `allowArgs` and `answerBody`. Each `run` calls its `api` function and hands the outcome to `showOutcome`. Send, answer and chat clear their draft only on a 200 (`outcome.kind === "ok"`; a 202 is `pending`, a refusal is `refused`), and send only while the draft, trimmed, still equals the text sent; Kill opens the fleet only on a 200. The command's `when` reads the session's `group` as well as its status (spec Part 2 decision 2), so the last task amends the two rules that said it reads only the status. No task here holds a landmine marker, so the developer runs at the default effort. Every command here is the only thing between a tap and a daemon action, so each task pins both sides of what it does: the 200 and the 202 and the refusal, the sheet's yes and its cancel, a draft that is cleared and one that is kept, a key that differs and one that matches.

This story adds one file no entry names: `internal/web/ui/src/commands/testkit.ts`, the sessions, jobs, store seeding, command lookup, toast spy and asked-sheet reader the three command test files share, and `internal/web/ui/src/commands/index.test.ts`.

### Task 3.1: The drafts slice, cleared on unpair

**Files:**
- Create: `internal/web/ui/src/store/drafts.ts`
- Modify: `internal/web/ui/src/store/fleet.ts`
- Create: `internal/web/ui/src/store/drafts.test.ts`
- Test: `internal/web/ui/src/store/startFleet.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/store/drafts.test.ts`:

```ts
import { afterEach, describe, expect, it } from "vitest";

import { useDrafts, type AskDraft } from "./drafts";

afterEach(() => {
  useDrafts.getState().clear();
});

const state = () => useDrafts.getState();

const draft = (labels: string[]): AskDraft => ({
  answers: { "Which?": { labels, other: "", note: "" } },
});

describe("the reply drafts", () => {
  it("keeps a reply per session", () => {
    state().setReply(1, "first");
    state().setReply(2, "second");
    expect(state().replies).toEqual({ 1: "first", 2: "second" });
  });

  it("replaces a session's reply and leaves the others", () => {
    state().setReply(1, "first");
    state().setReply(2, "second");
    state().setReply(1, "first, longer");
    expect(state().replies).toEqual({ 1: "first, longer", 2: "second" });
  });

  it("deletes a session's entry when it is set to the empty string, and no other", () => {
    state().setReply(1, "first");
    state().setReply(2, "second");
    state().setReply(1, "");
    expect(state().replies).toStrictEqual({ 2: "second" });
    expect(1 in state().replies).toBe(false);
  });

  it("keeps whitespace as typed, since only the empty string deletes", () => {
    state().setReply(1, "  ");
    expect(state().replies).toEqual({ 1: "  " });
  });

  it("changes nothing when an empty reply is set for a session that has none", () => {
    state().setReply(2, "second");
    const before = state().replies;
    state().setReply(1, "");
    expect(state().replies).toBe(before);
  });
});

describe("the ask drafts", () => {
  it("keeps a draft per ask and replaces a whole draft by setting it again", () => {
    state().setAsk("a", draft(["x"]));
    state().setAsk("b", draft(["y"]));
    state().setAsk("a", draft(["z"]));
    expect(state().asks).toEqual({ a: draft(["z"]), b: draft(["y"]) });
  });

  it("drops one ask's draft and keeps the others", () => {
    state().setAsk("a", draft(["x"]));
    state().setAsk("b", draft(["y"]));
    state().dropAsk("a");
    expect(state().asks).toStrictEqual({ b: draft(["y"]) });
  });

  it("drops an ask it holds no draft for without a word", () => {
    state().setAsk("a", draft(["x"]));
    state().dropAsk("nothing");
    expect(state().asks).toEqual({ a: draft(["x"]) });
  });
});

describe("clear", () => {
  it("forgets every reply and every answer", () => {
    state().setReply(1, "first");
    state().setAsk("a", draft(["x"]));
    state().clear();
    expect(state().replies).toEqual({});
    expect(state().asks).toEqual({});
  });
});
```

Fails without: "deletes a session's entry when it is set to the empty string, and no other": `setReply` storing `""` instead of deleting leaves `1` in `replies`. "keeps whitespace as typed, since only the empty string deletes": `setReply` deleting on a trimmed-empty text. "changes nothing when an empty reply is set for a session that has none": `setReply` returning a fresh `replies` object for a delete of a missing key. "drops one ask's draft and keeps the others": `dropAsk` resetting `asks` to `{}`. "forgets every reply and every answer": `clear` resetting only one of the two maps.

In `internal/web/ui/src/store/startFleet.test.ts`, replace

```ts
import { setBadge } from "./badge";
```

with

```ts
import { setBadge } from "./badge";
import { useDrafts } from "./drafts";
```

Replace

```ts
  for (const stop of stops.splice(0)) stop();
  queryClient.clear();
```

with

```ts
  for (const stop of stops.splice(0)) stop();
  queryClient.clear();
  useDrafts.getState().clear();
```

Insert before the line `  it("marks the link restarting when a request is answered with the stopping text, until the stream is live", () => {`:

```ts
  it("drops every unsent reply and answer when unpaired, and keeps them until then", () => {
    start();
    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    useDrafts.getState().setReply(7, "half a reply");
    useDrafts
      .getState()
      .setAsk("ask-1", { answers: { "Which?": { labels: ["a"], other: "", note: "" } } });
    expect(useDrafts.getState().replies).toEqual({ 7: "half a reply" });
    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);

    emitUnpaired();

    expect(useDrafts.getState().replies).toEqual({});
    expect(useDrafts.getState().asks).toEqual({});
  });

```

Fails without: "drops every unsent reply and answer when unpaired, and keeps them until then": the `useDrafts.getState().clear()` call this story adds to the unpaired handler, removed, leaves the reply and the ask.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/store/drafts.test.ts src/store/startFleet.test.ts`
Expected: both files FAIL, with `Failed to resolve import "./drafts" from "src/store/drafts.test.ts"` (and the same for `startFleet.test.ts`): the module does not exist yet.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/store/drafts.ts`:

```ts
// The unsent text the phone holds (spec Part 2 decision 3): a reply per session and an answer per
// ask, kept across tab switches for the page's lifetime as the classic app keeps them, dropped
// by the command that sends them on a 200, and dropped on unpair. Nothing survives a reload.
import { create } from "zustand";

// QuestionDraft is one question's unsent answer: the labels picked, in the order they were
// picked, the Other text and the note, all as typed.
export interface QuestionDraft {
  labels: string[];
  other: string;
  note: string;
}

// AskDraft is a question ask's unsent answers, keyed by the question's text.
export interface AskDraft {
  answers: Record<string, QuestionDraft>;
}

export interface DraftsState {
  replies: Record<number, string>;
  asks: Record<string, AskDraft>;
  // setReply keeps a session's reply as typed; the empty string deletes the session's entry.
  setReply: (session: number, text: string) => void;
  // setAsk replaces an ask's whole draft.
  setAsk: (ask: string, draft: AskDraft) => void;
  dropAsk: (ask: string) => void;
  // clear forgets every draft: the device was unpaired.
  clear: () => void;
}

export const useDrafts = create<DraftsState>()((set) => ({
  replies: {},
  asks: {},

  setReply(session, text) {
    set((state) => {
      if (text !== "") return { replies: { ...state.replies, [session]: text } };
      if (!(session in state.replies)) return state;
      const replies = { ...state.replies };
      delete replies[session];
      return { replies };
    });
  },

  setAsk(ask, draft) {
    set((state) => ({ asks: { ...state.asks, [ask]: draft } }));
  },

  dropAsk(ask) {
    set((state) => {
      if (!(ask in state.asks)) return state;
      const asks = { ...state.asks };
      delete asks[ask];
      return { asks };
    });
  },

  clear() {
    set({ replies: {}, asks: {} });
  },
}));
```

In `internal/web/ui/src/store/fleet.ts`, replace

```ts
import { setBadge } from "./badge";
```

with

```ts
import { setBadge } from "./badge";
import { useDrafts } from "./drafts";
```

Replace

```ts
    queryClient.clear();
    setBadge(0);
```

with

```ts
    queryClient.clear();
    // The unsent replies and answers are the revoked device's too.
    useDrafts.getState().clear();
    setBadge(0);
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/store`
Expected: PASS, typecheck clean and every test of `src/store`.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/store/drafts.ts internal/web/ui/src/store/drafts.test.ts
git commit -m "feat(web): a drafts slice for unsent replies and answers, cleared on unpair" -- internal/web/ui/src/store/drafts.ts internal/web/ui/src/store/drafts.test.ts internal/web/ui/src/store/fleet.ts internal/web/ui/src/store/startFleet.test.ts
```

### Task 3.2: sessionActions and the session commands

**Files:**
- Create: `internal/web/ui/src/commands/testkit.ts`
- Create: `internal/web/ui/src/commands/session.ts`
- Create: `internal/web/ui/src/commands/session.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/commands/testkit.ts` (the three command test files share it; no app module imports it):

```ts
// What the command tests share: sessions and jobs built from the canned snapshot, the store
// seeded with them (a command with a sheet checks its session again against the store after a
// yes), the lookup of a registered command, the toast spy, the sheet a command asked for, and a
// promise the test settles itself. Each test file still mocks `@/app/ConfirmSheet` itself: a
// `vi.mock` is hoisted to the top of the file that holds it.
import { vi } from "vitest";

import type { Job, Notices, Outcome, Session } from "@/api/gen";
import { commandById, type Command, type CommandContext } from "@/app/commands";
import { askConfirm, type ConfirmSpec } from "@/app/ConfirmSheet";
import { toastManager } from "@/components/ui/toast";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

// makeSession is a Ready session of workspace 1 with no asks and no jobs, over the canned
// snapshot's first session.
export function makeSession(over: Partial<Session> = {}): Session {
  const base = allSessions(fixtureSnapshot())[0];
  if (base === undefined) throw new Error("the canned snapshot holds no session");
  return {
    ...base,
    id: 1,
    workspace: 1,
    status: "Ready",
    group: "your_turn",
    asks: [],
    jobs: [],
    ...over,
  };
}

export function makeJob(over: Partial<Job> = {}): Job {
  return {
    id: 214,
    state: "queued",
    description: "cargo test",
    command: "cargo test",
    agent: "",
    submitted: "2026-11-19T10:00:00Z",
    started: "",
    ended: "",
    exit: null,
    failure: "",
    cancel_reason: "",
    waiting: "",
    parked: false,
    paused: false,
    pause_reclaimed: false,
    forced: false,
    control: "",
    control_error: "",
    ...over,
  };
}

// seed makes the store's live snapshot hold exactly these sessions, grouped by workspace in the
// order given.
export function seed(sessions: Session[]): void {
  const byWorkspace: Record<string, Session[]> = {};
  for (const session of sessions) (byWorkspace[String(session.workspace)] ??= []).push(session);
  useFleet.setState({
    snapshot: { ...fixtureSnapshot(), sessions: byWorkspace },
    stale: false,
    link: "live",
    updated: Date.now(),
  });
}

export function resetFleet(): void {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
}

export function context(
  session: Session | undefined,
  over: Partial<CommandContext> = {},
): CommandContext {
  return { session, link: "live", stale: false, panel: "turns", ...over };
}

// command is the command registered under id; the test fails when there is none.
export function command(id: string): Command<unknown> {
  const found = commandById(id);
  if (found === undefined) throw new Error(`no command ${id} is registered`);
  return found;
}

export function noticesOk(): Outcome<Notices> {
  return { kind: "ok", status: 200, value: { notices: [] }, notices: [] };
}

// spyOnAdd keeps the toast manager from showing anything and records what it was asked to show;
// the test file restores it with `vi.restoreAllMocks` after each test.
export function spyOnAdd() {
  return vi.spyOn(toastManager, "add").mockImplementation(() => "toast");
}

// askedSheet is the spec the command asked the sheet to show, read from the file's mocked
// `askConfirm`; it fails the test when no sheet was asked for.
export function askedSheet(): ConfirmSpec {
  const call = vi.mocked(askConfirm).mock.calls[0];
  if (call === undefined) throw new Error("no sheet was asked for");
  return call[0];
}

export function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
```

Create `internal/web/ui/src/commands/session.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Session } from "@/api/gen";
import { api } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { askConfirm } from "@/app/ConfirmSheet";
import { useDrafts } from "@/store/drafts";

import { sessionActions, type SessionActions } from "./session";
import {
  askedSheet,
  command,
  context,
  makeSession,
  noticesOk,
  resetFleet,
  seed,
  spyOnAdd,
} from "./testkit";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  add = spyOnAdd();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  useDrafts.getState().clear();
});

const OFFERED: (keyof SessionActions)[] = [
  "pause",
  "resume",
  "push",
  "merge",
  "send",
  "diff",
  "kill",
];

const STATES: [Session["status"], Session["group"], (keyof SessionActions)[]][] = [
  ["Ready", "your_turn", ["pause", "push", "merge", "send", "diff", "kill"]],
  ["Running", "working", ["pause", "push", "merge", "send", "diff", "kill"]],
  ["Prompting", "needs_you", ["pause", "push", "merge", "send", "diff", "kill"]],
  ["Loading", "working", []],
  ["Deleting", "working", []],
  ["Loading", "paused", []],
  ["Paused", "paused", ["resume", "push", "merge", "kill"]],
  ["Recoverable", "paused", ["kill"]],
  ["Ready", "paused", ["kill"]],
];

describe("sessionActions", () => {
  it.each(STATES)("a %s session in group %s offers %j", (status, group, offered) => {
    const actions = sessionActions(makeSession({ status, group }));
    expect(OFFERED.filter((key) => actions[key])).toEqual(offered);
  });
});

const COMMANDS: [string, keyof SessionActions][] = [
  ["session.send", "send"],
  ["session.pause", "pause"],
  ["session.resume", "resume"],
  ["session.push", "push"],
  ["session.merge", "merge"],
  ["session.kill", "kill"],
];

describe("the session commands' availability", () => {
  it.each(COMMANDS)("%s is enabled exactly where sessionActions offers %s", (id, key) => {
    for (const [status, group] of STATES) {
      const session = makeSession({ status, group });
      expect(commandEnabled(command(id), context(session))).toBe(sessionActions(session)[key]);
    }
  });

  it.each(COMMANDS)("%s is off with no session and while the snapshot is stale", (id) => {
    const open =
      id === "session.resume"
        ? makeSession({ status: "Paused", group: "paused" })
        : makeSession();
    expect(commandEnabled(command(id), context(open))).toBe(true);
    expect(commandEnabled(command(id), context(undefined))).toBe(false);
    expect(commandEnabled(command(id), context(open, { stale: true, link: "offline" }))).toBe(
      false,
    );
  });
});

describe("session.send", () => {
  it("sends the trimmed text and clears the session's draft, and no other session's", async () => {
    const send = vi.spyOn(api, "send").mockResolvedValue(noticesOk());
    useDrafts.getState().setReply(1, "  hello  ");
    useDrafts.getState().setReply(2, "keep me");

    await runCommand(command("session.send"), context(makeSession()), { text: "  hello  " });

    expect(send).toHaveBeenCalledOnce();
    expect(send).toHaveBeenCalledWith(1, { text: "hello" });
    expect(useDrafts.getState().replies).toStrictEqual({ 2: "keep me" });
  });

  it("keeps what was typed while the send was in flight", async () => {
    vi.spyOn(api, "send").mockImplementation(() => {
      useDrafts.getState().setReply(1, "hello, and one more thing");
      return Promise.resolve(noticesOk());
    });
    useDrafts.getState().setReply(1, "hello");

    await runCommand(command("session.send"), context(makeSession()), { text: "hello" });

    expect(useDrafts.getState().replies).toEqual({ 1: "hello, and one more thing" });
  });

  it("keeps the draft on a 202 and says the outcome will follow", async () => {
    vi.spyOn(api, "send").mockResolvedValue({ kind: "pending" });
    useDrafts.getState().setReply(1, "hello");

    await runCommand(command("session.send"), context(makeSession()), { text: "hello" });

    expect(useDrafts.getState().replies).toEqual({ 1: "hello" });
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Send is still running. Its outcome will follow.",
        type: "info",
      }),
    );
  });

  it("keeps the draft and toasts the model's text when the send is refused", async () => {
    vi.spyOn(api, "send").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "session is busy",
    });
    useDrafts.getState().setReply(1, "hello");

    await runCommand(command("session.send"), context(makeSession()), { text: "hello" });

    expect(useDrafts.getState().replies).toEqual({ 1: "hello" });
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "session is busy", type: "error" }),
    );
  });

  it("sends nothing from a session that cannot take a reply", async () => {
    const send = vi.spyOn(api, "send").mockResolvedValue(noticesOk());
    const paused = makeSession({ status: "Paused", group: "paused" });

    await runCommand(command("session.send"), context(paused), { text: "hello" });

    expect(send).not.toHaveBeenCalled();
  });
});

describe("session.pause and session.resume", () => {
  it("pauses the session with no sheet", async () => {
    const pause = vi.spyOn(api, "pause").mockResolvedValue(noticesOk());

    await runCommand(command("session.pause"), context(makeSession()), undefined);

    expect(askConfirm).not.toHaveBeenCalled();
    expect(pause).toHaveBeenCalledOnce();
    expect(pause).toHaveBeenCalledWith(1);
  });

  it("resumes a Paused session with no sheet", async () => {
    const resume = vi.spyOn(api, "resume").mockResolvedValue(noticesOk());
    const paused = makeSession({ status: "Paused", group: "paused" });

    await runCommand(command("session.resume"), context(paused), undefined);

    expect(askConfirm).not.toHaveBeenCalled();
    expect(resume).toHaveBeenCalledOnce();
    expect(resume).toHaveBeenCalledWith(1);
  });

  it("toasts a refusal in the model's words", async () => {
    vi.spyOn(api, "pause").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "session is not running",
    });

    await runCommand(command("session.pause"), context(makeSession()), undefined);

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "session is not running", type: "error" }),
    );
  });

  it("does not resume a session that is not Paused", async () => {
    const resume = vi.spyOn(api, "resume").mockResolvedValue(noticesOk());
    const recoverable = makeSession({ status: "Recoverable", group: "paused" });

    await runCommand(command("session.resume"), context(recoverable), undefined);

    expect(resume).not.toHaveBeenCalled();
  });
});

describe("session.push", () => {
  it("asks first, in the classic words, and pushes on a yes", async () => {
    const session = makeSession({ branch: "feat/login" });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const push = vi.spyOn(api, "push").mockResolvedValue(noticesOk());

    await runCommand(command("session.push"), context(session), undefined);

    expect(askedSheet()).toEqual({
      title: "Push this branch?",
      body: "This commits the session's uncommitted changes first, then pushes feat/login.",
      action: "Commit and push",
      destructive: false,
    });
    expect(push).toHaveBeenCalledOnce();
    expect(push).toHaveBeenCalledWith(1);
  });

  it("pushes nothing when the sheet is cancelled", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const push = vi.spyOn(api, "push").mockResolvedValue(noticesOk());

    await runCommand(command("session.push"), context(session), undefined);

    expect(push).not.toHaveBeenCalled();
  });
});

describe("session.kill", () => {
  const tree = {
    tmux: "loom_login",
    worktree: "/work/login",
    branch: "feat/login",
  };

  it("asks first, naming the tmux session, the worktree and the branch", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(askedSheet()).toEqual({
      title: "Kill this session?",
      body: [
        "It ends tmux session loom_login.",
        "It force-removes worktree /work/login, discarding its uncommitted changes. Push first to keep them.",
        "It deletes branch feat/login unless the session checked out an existing branch.",
      ].join("\n\n"),
      action: "Kill",
      destructive: true,
    });
  });

  it("speaks of the session's own tmux session and worktree when it has none recorded", async () => {
    const session = makeSession({ branch: "feat/login", tmux: undefined, worktree: undefined });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(askedSheet().body).toBe(
      [
        "It ends the session's tmux session.",
        "It force-removes the session's worktree, discarding its uncommitted changes. Push first to keep them.",
        "It deletes branch feat/login unless the session checked out an existing branch.",
      ].join("\n\n"),
    );
  });

  it("adds the stash paragraph for a Paused session only", async () => {
    const paused = makeSession({ ...tree, status: "Paused", group: "paused" });
    seed([paused]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    await runCommand(command("session.kill"), context(paused), undefined);
    const stash =
      "If an older loom paused it, its worktree is gone and its changes may be in a stash, which Kill drops.";
    expect(askedSheet().body).toContain(stash);

    vi.mocked(askConfirm).mockClear();
    const ready = makeSession(tree);
    seed([ready]);
    await runCommand(command("session.kill"), context(ready), undefined);
    expect(askedSheet().body).not.toContain("stash");
  });

  it("kills on a yes and opens the fleet when the daemon answers 200", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const kill = vi.spyOn(api, "kill").mockResolvedValue(noticesOk());
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(kill).toHaveBeenCalledOnce();
    expect(kill).toHaveBeenCalledWith(1);
    expect(assign).toHaveBeenCalledOnce();
    expect(assign).toHaveBeenCalledWith("#/");
  });

  it("kills nothing, and stays on the session, when the sheet is cancelled", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const kill = vi.spyOn(api, "kill").mockResolvedValue(noticesOk());
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(kill).not.toHaveBeenCalled();
    expect(assign).not.toHaveBeenCalled();
  });

  it("stays on the session when the daemon answers 202, and says the outcome will follow", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    vi.spyOn(api, "kill").mockResolvedValue({ kind: "pending" });
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(assign).not.toHaveBeenCalled();
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Kill is still running. Its outcome will follow.",
        type: "info",
      }),
    );
  });

  it("stays on the session and toasts the model's text when the kill is refused", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    vi.spyOn(api, "kill").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "session is deleting",
    });
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(assign).not.toHaveBeenCalled();
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "session is deleting", type: "error" }),
    );
  });
});

describe("session.merge", () => {
  const self = makeSession({ id: 1, workspace: 1, branch: "feat/login" });
  const fixLogin = makeSession({ id: 2, workspace: 1, title: "Fix login", branch: "fix-login" });
  const elsewhere = makeSession({ id: 3, workspace: 2, title: "Elsewhere", branch: "elsewhere" });
  const docs = makeSession({ id: 4, workspace: 1, title: "Docs", branch: "docs" });

  it("offers the other sessions of its workspace, by title and branch, valued by id", async () => {
    seed([self, fixLogin, elsewhere, docs]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("session.merge"), context(self), undefined);

    expect(askedSheet()).toEqual({
      title: "Merge a branch in",
      body: "Merge which session's branch into feat/login?",
      action: "Merge",
      destructive: false,
      field: {
        kind: "choice",
        label: "Session to merge in",
        options: [
          { value: "2", label: "Fix login (fix-login)" },
          { value: "4", label: "Docs (docs)" },
        ],
      },
    });
  });

  it("merges the session the sheet chose into this one", async () => {
    seed([self, fixLogin, elsewhere, docs]);
    vi.mocked(askConfirm).mockResolvedValue("4");
    const merge = vi.spyOn(api, "merge").mockResolvedValue(noticesOk());

    await runCommand(command("session.merge"), context(self), undefined);

    expect(merge).toHaveBeenCalledOnce();
    expect(merge).toHaveBeenCalledWith(1, { source: 4 });
  });

  it("merges nothing when the sheet is cancelled", async () => {
    seed([self, fixLogin, elsewhere, docs]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const merge = vi.spyOn(api, "merge").mockResolvedValue(noticesOk());

    await runCommand(command("session.merge"), context(self), undefined);

    expect(merge).not.toHaveBeenCalled();
  });

  it("shows no sheet and an error toast when no other session shares the workspace", async () => {
    seed([self, elsewhere]);
    const merge = vi.spyOn(api, "merge").mockResolvedValue(noticesOk());

    await runCommand(command("session.merge"), context(self), undefined);

    expect(askConfirm).not.toHaveBeenCalled();
    expect(merge).not.toHaveBeenCalled();
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "No other session in this workspace to merge in.",
        type: "error",
      }),
    );
  });
});
```

Fails without: "a %s session in group %s offers %j" (each row): a term of `sessionActions` removed or inverted: `!paused` dropped from `pause` offers pause to the Paused row; `status === "Paused"` dropped from `resume` offers it to the Recoverable row; `|| session.status === "Paused"` dropped from the push and merge tree term hides them from the Paused row, and the term made `true` offers them to the Recoverable and Ready/paused rows; `!busy` dropped from `kill` offers it to the Loading and Deleting rows. "%s is enabled exactly where sessionActions offers %s": a `when` that is only `ctx.session !== undefined` enables the busy and paused rows. "%s is off with no session and while the snapshot is stale": a `when` that does not check `ctx.session !== undefined` is enabled with no session, and the first expect fails if `when` is inverted. "sends the trimmed text…": `api.send` given `args.text` untrimmed; the draft cleared with `clear()` instead of for the session, which removes session 2's draft too. "keeps what was typed while the send was in flight": the equality check removed, so the draft is cleared whatever it holds. "keeps the draft on a 202…": the draft cleared on any outcome but a refusal. "keeps the draft and toasts…": the draft cleared on any outcome but a 202. "sends nothing from a session that cannot take a reply": `session.send`'s `when` not reading `sessionActions`. "pauses the session with no sheet": a `confirm` added to `session.pause`. "resumes a Paused session…" and "does not resume a session that is not Paused": `session.resume`'s `when` reading something other than `status === "Paused"`. "asks first, in the classic words…" and "pushes nothing…": the push sheet's words changed, or `api.push` called before the sheet. "asks first, naming the tmux session…": a line of Kill's sheet dropped, reworded or reordered. "speaks of the session's own tmux session…": the fallback words dropped, so `undefined` prints in the body. "adds the stash paragraph for a Paused session only": the paragraph printed for every session, or never. "kills on a yes and opens the fleet…": the `window.location.assign(hrefFor("fleet"))` removed. "kills nothing, and stays…": `api.kill` called before the sheet's answer is read. "stays on the session when the daemon answers 202…": the fleet opened on any outcome but a refusal. "stays on the session and toasts…": the fleet opened on any outcome but a 202. "offers the other sessions…": the candidates not filtered by workspace (offers session 3), by `id !== session.id` (offers session 1), or labelled otherwise. "merges the session the sheet chose…": `source` sent as a string or read from the wrong value. "shows no sheet and an error toast…": the empty-candidates check removed, so the sheet opens with no options.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/commands/session.test.ts`
Expected: FAIL with `Failed to resolve import "./session" from "src/commands/session.test.ts"`: the module does not exist yet.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/commands/session.ts`:

```ts
// The session's commands: send, pause, resume, push, merge and kill, with the classic app's sheets
// and words (session.mjs) and its availability, `sessionActions`. A `when` here reads the
// session's status and group and nothing else (spec Part 2 decision 2); the daemon's admit stays
// the authority and a refusal comes back as a toast.
import type { Session } from "@/api/gen";
import { api } from "@/api/request";
import { defineCommand, type CommandContext } from "@/app/commands";
import type { ChoiceOption } from "@/app/ConfirmSheet";
import { hrefFor } from "@/app/routes";
import { showMessage, showOutcome } from "@/app/toasts";
import { allSessions } from "@/fleet/groups";
import { useDrafts } from "@/store/drafts";
import { useFleet } from "@/store/fleet";

// SessionActions says which of a session's actions are on offer: the classic `actionsFor`. `diff`
// is the changes tab: ReadDiff refuses a busy session and one in the paused group.
export interface SessionActions {
  pause: boolean;
  resume: boolean;
  push: boolean;
  merge: boolean;
  send: boolean;
  diff: boolean;
  kill: boolean;
}

// sessionActions follows the session's status and group. Busy is Loading or Deleting; paused is
// the group, which holds the Recoverable and unstarted Ready sessions that status alone cannot
// tell from a started Ready. Pause keeps the worktree, so push and merge work on a Paused
// session, but a Recoverable or unstarted one may have no worktree to work on.
export function sessionActions(session: Session): SessionActions {
  const busy = session.status === "Loading" || session.status === "Deleting";
  const paused = session.group === "paused";
  const tree = !paused || session.status === "Paused";
  return {
    pause: !busy && !paused,
    resume: session.status === "Paused",
    push: !busy && tree,
    merge: !busy && tree,
    send: !busy && !paused,
    diff: !busy && !paused,
    kill: !busy,
  };
}

// requireSession is the session a command acts on: every command's `when` needs one, so none is
// a bug, which runCommand shows as an error toast.
export function requireSession(ctx: CommandContext): Session {
  if (ctx.session === undefined) throw new Error("loom: this action needs a session on screen");
  return ctx.session;
}

// offers is a session command's `when`: a session is on screen and `sessionActions` offers the action.
function offers(action: keyof SessionActions): (ctx: CommandContext) => boolean {
  return (ctx) => ctx.session !== undefined && sessionActions(ctx.session)[action];
}

defineCommand<{ text: string }>({
  id: "session.send",
  title: "Send",
  when: offers("send"),
  run: async (ctx, args) => {
    const session = requireSession(ctx);
    const text = args.text.trim();
    const outcome = await api.send(session.id, { text });
    showOutcome(outcome, "Send");
    if (outcome.kind !== "ok") return;
    // What was typed while the send was in flight is a new draft: only the text sent is dropped.
    const drafts = useDrafts.getState();
    if ((drafts.replies[session.id] ?? "").trim() === text) drafts.setReply(session.id, "");
  },
});

defineCommand({
  id: "session.pause",
  title: "Pause",
  when: offers("pause"),
  run: async (ctx) => {
    showOutcome(await api.pause(requireSession(ctx).id), "Pause");
  },
});

defineCommand({
  id: "session.resume",
  title: "Resume",
  when: offers("resume"),
  run: async (ctx) => {
    showOutcome(await api.resume(requireSession(ctx).id), "Resume");
  },
});

defineCommand({
  id: "session.push",
  title: "Push",
  when: offers("push"),
  confirm: (ctx) => ({
    title: "Push this branch?",
    body: `This commits the session's uncommitted changes first, then pushes ${requireSession(ctx).branch}.`,
    action: "Commit and push",
    destructive: false,
  }),
  run: async (ctx) => {
    showOutcome(await api.push(requireSession(ctx).id), "Push");
  },
});

// mergeSources are the other sessions of the session's workspace, whose branch can be merged into
// this one, in the snapshot's order.
function mergeSources(session: Session): ChoiceOption[] {
  const snapshot = useFleet.getState().snapshot;
  if (snapshot === null) return [];
  return allSessions(snapshot)
    .filter((other) => other.workspace === session.workspace && other.id !== session.id)
    .map((other) => ({ value: String(other.id), label: `${other.title} (${other.branch})` }));
}

defineCommand({
  id: "session.merge",
  title: "Merge",
  when: offers("merge"),
  // No sheet when there is nothing to pick: run then says so.
  confirm: (ctx) => {
    const session = requireSession(ctx);
    const options = mergeSources(session);
    if (options.length === 0) return null;
    return {
      title: "Merge a branch in",
      body: `Merge which session's branch into ${session.branch}?`,
      action: "Merge",
      destructive: false,
      field: { kind: "choice", label: "Session to merge in", options },
    };
  },
  run: async (ctx, args, input) => {
    const session = requireSession(ctx);
    if (input === "") {
      showMessage("No other session in this workspace to merge in.", true);
      return;
    }
    showOutcome(await api.merge(session.id, { source: Number(input) }), "Merge");
  },
});

// killBody is Kill's sheet: what it ends, removes and deletes, as the classic app words it.
function killBody(session: Session): string {
  const tmux = session.tmux
    ? `It ends tmux session ${session.tmux}.`
    : "It ends the session's tmux session.";
  const tree = session.worktree
    ? `It force-removes worktree ${session.worktree}`
    : "It force-removes the session's worktree";
  const paragraphs = [
    tmux,
    `${tree}, discarding its uncommitted changes. Push first to keep them.`,
  ];
  if (session.status === "Paused") {
    paragraphs.push(
      "If an older loom paused it, its worktree is gone and its changes may be in a stash, which Kill drops.",
    );
  }
  paragraphs.push(
    `It deletes branch ${session.branch} unless the session checked out an existing branch.`,
  );
  return paragraphs.join("\n\n");
}

defineCommand({
  id: "session.kill",
  title: "Kill",
  when: offers("kill"),
  confirm: (ctx) => ({
    title: "Kill this session?",
    body: killBody(requireSession(ctx)),
    action: "Kill",
    destructive: true,
  }),
  run: async (ctx) => {
    const outcome = await api.kill(requireSession(ctx).id);
    showOutcome(outcome, "Kill");
    // The session is gone only when the daemon answered 200; a 202 or a refusal leaves it here.
    if (outcome.kind === "ok") window.location.assign(hrefFor("fleet"));
  },
});
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/commands/session.test.ts`
Expected: PASS, typecheck clean and every test of the file.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/commands/testkit.ts internal/web/ui/src/commands/session.ts internal/web/ui/src/commands/session.test.ts
git commit -m "feat(web): the session's commands and the classic availability of its actions" -- internal/web/ui/src/commands/testkit.ts internal/web/ui/src/commands/session.ts internal/web/ui/src/commands/session.test.ts
```

### Task 3.3: The ask commands, allowArgs and answerBody

**Files:**
- Create: `internal/web/ui/src/commands/asks.ts`
- Create: `internal/web/ui/src/commands/asks.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/commands/asks.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AnswerBody, Ask, AskQuestion } from "@/api/gen";
import { api } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { askConfirm } from "@/app/ConfirmSheet";
import { MAX_MESSAGE_BYTES } from "@/lib/utf8";
import { useDrafts, type AskDraft, type QuestionDraft } from "@/store/drafts";

import { allowArgs, answerBody } from "./asks";
import {
  askedSheet,
  command,
  context,
  deferred,
  makeSession,
  noticesOk,
  resetFleet,
  seed,
  spyOnAdd,
} from "./testkit";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  add = spyOnAdd();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  useDrafts.getState().clear();
});

function question(text: string, multi: boolean): AskQuestion {
  return {
    question: text,
    header: "Pick",
    multi,
    options: [
      { label: "a", description: "" },
      { label: "b", description: "" },
    ],
  };
}

const questionAsk: Ask = {
  id: "ask-1",
  kind: "question",
  agent: "",
  opened: "2026-11-19T10:00:00Z",
  answering: false,
  questions: [question("Which?", false), question("Which all?", true)],
};

const permissionAsk: Ask = {
  id: "perm-1",
  kind: "permission",
  agent: "",
  opened: "2026-11-19T10:00:00Z",
  answering: false,
  tool: "Bash",
  suggestions: [
    { label: "Always allow ls", persists: true },
    { label: "Allow ls this session", persists: false },
    { label: "Allow ls once more" },
  ],
};

const picked = (labels: string[], other = "", note = ""): QuestionDraft => ({
  labels,
  other,
  note,
});

const drafted = (one: QuestionDraft, two: QuestionDraft): AskDraft => ({
  answers: { "Which?": one, "Which all?": two },
});

describe("allowArgs", () => {
  it("is the plain Allow at suggestion 0: no rule to save", () => {
    expect(allowArgs(permissionAsk, 0)).toEqual({
      ask: "perm-1",
      suggestion: 0,
      label: "Allow",
      persists: false,
    });
  });

  it("is the 1-based suggestion with its label, persisting when that suggestion does", () => {
    expect(allowArgs(permissionAsk, 1)).toEqual({
      ask: "perm-1",
      suggestion: 1,
      label: "Always allow ls",
      persists: true,
    });
    expect(allowArgs(permissionAsk, 2)).toEqual({
      ask: "perm-1",
      suggestion: 2,
      label: "Allow ls this session",
      persists: false,
    });
  });

  it("does not persist a suggestion that does not say so", () => {
    expect(allowArgs(permissionAsk, 3).persists).toBe(false);
  });

  it("refuses a suggestion the ask does not hold, rather than allowing something else", () => {
    expect(() => allowArgs(permissionAsk, 4)).toThrow("no suggestion 4");
    expect(() => allowArgs({ ...permissionAsk, suggestions: undefined }, 1)).toThrow(
      "no suggestion 1",
    );
  });
});

describe("answerBody", () => {
  it("is null until every question has a picked label or an Other text", () => {
    expect(answerBody(questionAsk, undefined)).toBeNull();
    expect(answerBody(questionAsk, drafted(picked([]), picked(["a"])))).toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked([])))).toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked(["a"])))).not.toBeNull();
    expect(answerBody(questionAsk, drafted(picked([], "mine"), picked([], "ours")))).not.toBeNull();
  });

  it("does not count an Other text of whitespace, or a note alone, as an answer", () => {
    expect(answerBody(questionAsk, drafted(picked([], "   "), picked(["a"])))).toBeNull();
    expect(answerBody(questionAsk, drafted(picked([], "", "a note"), picked(["a"])))).toBeNull();
  });

  it("is null when the draft lacks one of the questions", () => {
    const draft: AskDraft = { answers: { "Which?": picked(["a"]) } };
    expect(answerBody(questionAsk, draft)).toBeNull();
  });

  it("is null for an ask with no questions", () => {
    expect(answerBody({ ...questionAsk, questions: [] }, { answers: {} })).toBeNull();
    expect(answerBody({ ...questionAsk, questions: undefined }, { answers: {} })).toBeNull();
  });

  it("keys each answer by its question's text, labels in pick order, other and note trimmed", () => {
    const body = answerBody(
      questionAsk,
      drafted(picked(["a"], "", "  because  "), picked(["b", "a"], "  extra  ")),
    );
    expect(body).toStrictEqual({
      answers: {
        "Which?": { labels: ["a"], note: "because" },
        "Which all?": { labels: ["b", "a"], other: "extra" },
      },
    });
  });

  it("leaves other and note out when they are empty after trimming", () => {
    const body = answerBody(questionAsk, drafted(picked(["a"], "  ", "\n"), picked(["b"])));
    expect(body).toStrictEqual({
      answers: { "Which?": { labels: ["a"] }, "Which all?": { labels: ["b"] } },
    });
  });

  it("answers a single-select question with its Other text alone", () => {
    const body = answerBody(questionAsk, drafted(picked([], " mine "), picked(["a"])));
    expect(body).toStrictEqual({
      answers: { "Which?": { labels: [], other: "mine" }, "Which all?": { labels: ["a"] } },
    });
  });

  it("gives a single-select question one answer, a label or Other and never both", () => {
    const both = answerBody(questionAsk, drafted(picked(["b"], "typed"), picked(["a"])));
    expect(both).toStrictEqual({
      answers: { "Which?": { labels: ["b"] }, "Which all?": { labels: ["a"] } },
    });
    const two = answerBody(questionAsk, drafted(picked(["a", "b"]), picked(["a"])));
    expect(two?.answers?.["Which?"]).toStrictEqual({ labels: ["b"] });
  });

  it("lets a multi-select question carry labels and Other together", () => {
    const body = answerBody(questionAsk, drafted(picked(["a"]), picked(["a", "b"], "and more")));
    expect(body?.answers?.["Which all?"]).toStrictEqual({
      labels: ["a", "b"],
      other: "and more",
    });
  });

  it("is null while an Other text or a note passes the limit in bytes, and not at it", () => {
    const at = "é".repeat(MAX_MESSAGE_BYTES / 2);
    const over = at + "a";
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked(["a"], at)))).not.toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked(["a"], over)))).toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"], "", at), picked(["a"])))).not.toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"], "", over), picked(["a"])))).toBeNull();
  });

  it("measures the limit on the trimmed text it would send, not the whitespace around it", () => {
    const at = "é".repeat(MAX_MESSAGE_BYTES / 2);
    const padded = drafted(picked(["a"]), picked(["a"], ` ${at}\n`));
    expect(answerBody(questionAsk, padded)).not.toBeNull();
    const notePadded = drafted(picked(["a"], "", `${at}  `), picked(["a"]));
    expect(answerBody(questionAsk, notePadded)).not.toBeNull();
  });

  it("does not measure an Other text that a single-select question's label drops", () => {
    const over = "é".repeat(MAX_MESSAGE_BYTES / 2 + 1);
    const body = answerBody(questionAsk, drafted(picked(["a"], over), picked(["a"])));
    expect(body?.answers?.["Which?"]).toStrictEqual({ labels: ["a"] });
    const alone = answerBody(questionAsk, drafted(picked([], over), picked(["a"])));
    expect(alone).toBeNull();
  });

  it("counts bytes, not characters: 32769 two-byte characters is over the limit", () => {
    const text = "é".repeat(MAX_MESSAGE_BYTES / 2 + 1);
    expect(text.length).toBeLessThan(MAX_MESSAGE_BYTES);
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked(["a"], text)))).toBeNull();
  });
});

describe("the ask commands' availability", () => {
  it.each(["ask.answer", "ask.chat", "ask.allow", "ask.deny"])(
    "%s is on with a session on screen, and off with none or while the snapshot is stale",
    (id) => {
      const session = makeSession();
      expect(commandEnabled(command(id), context(session))).toBe(true);
      expect(commandEnabled(command(id), context(undefined))).toBe(false);
      expect(commandEnabled(command(id), context(session, { stale: true, link: "offline" }))).toBe(
        false,
      );
    },
  );
});

describe("ask.answer", () => {
  const body: AnswerBody = { answers: { "Which?": { labels: ["a"] } } };

  it("sends the body to the ask and drops that ask's draft, and no other", async () => {
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));
    useDrafts.getState().setAsk("ask-2", drafted(picked(["b"]), picked(["b"])));

    await runCommand(command("ask.answer"), context(makeSession()), { ask: "ask-1", body });

    expect(answer).toHaveBeenCalledOnce();
    expect(answer).toHaveBeenCalledWith(1, "ask-1", body);
    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-2"]);
  });

  it("keeps the draft on a 202 and says the outcome will follow", async () => {
    vi.spyOn(api, "answer").mockResolvedValue({ kind: "pending" });
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));

    await runCommand(command("ask.answer"), context(makeSession()), { ask: "ask-1", body });

    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Answer is still running. Its outcome will follow.",
        type: "info",
      }),
    );
  });

  it("keeps the draft and toasts the model's text when the answer is refused", async () => {
    vi.spyOn(api, "answer").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "that question is already answered",
    });
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));

    await runCommand(command("ask.answer"), context(makeSession()), { ask: "ask-1", body });

    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "that question is already answered",
        type: "error",
      }),
    );
  });

  it("keys its in-flight mark by the ask: another ask runs beside it, the same ask runs once", async () => {
    const gate = deferred<ReturnType<typeof noticesOk>>();
    const answer = vi.spyOn(api, "answer").mockReturnValue(gate.promise);
    const ctx = context(makeSession());

    const first = runCommand(command("ask.answer"), ctx, { ask: "ask-1", body });
    const again = runCommand(command("ask.answer"), ctx, { ask: "ask-1", body });
    const other = runCommand(command("ask.answer"), ctx, { ask: "ask-2", body });
    gate.resolve(noticesOk());
    await Promise.all([first, again, other]);

    expect(answer.mock.calls.map((call) => call[1])).toEqual(["ask-1", "ask-2"]);
  });
});

describe("ask.chat", () => {
  it("asks for what to clarify, in the classic words, with the message's limit", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });

    expect(askedSheet()).toEqual({
      title: "Chat about this",
      body: "Claude puts the questions aside and asks what you want to clarify.",
      action: "Send",
      destructive: false,
      field: {
        kind: "text",
        label: "What to clarify",
        placeholder: "What to clarify (optional)",
        max: MAX_MESSAGE_BYTES,
        tooLong: "Too long: a message is at most 64 KiB.",
      },
    });
  });

  it("sends the trimmed text as a chat and drops the ask's draft", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("  what does a do?  ");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));

    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });

    expect(answer).toHaveBeenCalledWith(1, "ask-1", { chat: true, chat_text: "what does a do?" });
    expect(useDrafts.getState().asks).toEqual({});
  });

  it("sends an empty chat text when nothing was typed", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });

    expect(answer).toHaveBeenCalledWith(1, "ask-1", { chat: true, chat_text: "" });
  });

  it("sends nothing, and keeps the draft, when the sheet is cancelled", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));

    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });

    expect(answer).not.toHaveBeenCalled();
    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);
  });

  it("keeps the draft when the daemon answers 202 or refuses", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("hm");
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));
    const answer = vi.spyOn(api, "answer").mockResolvedValue({ kind: "pending" });
    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });
    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);

    answer.mockResolvedValue({ kind: "refused", status: 409, message: "already answered" });
    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });
    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);
  });
});

describe("ask.allow", () => {
  it("allows with no sheet and no suggestion for the plain Allow", async () => {
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.allow"), context(makeSession()), allowArgs(permissionAsk, 0));

    expect(askConfirm).not.toHaveBeenCalled();
    expect(answer).toHaveBeenCalledOnce();
    expect(answer.mock.calls[0]?.[2]).toStrictEqual({ allow: true });
    expect(answer.mock.calls[0]?.[1]).toBe("perm-1");
  });

  it("allows a suggestion that does not persist with no sheet, naming it 1-based", async () => {
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.allow"), context(makeSession()), allowArgs(permissionAsk, 2));

    expect(askConfirm).not.toHaveBeenCalled();
    expect(answer).toHaveBeenCalledWith(1, "perm-1", { allow: true, suggestion: 2 });
  });

  it("asks 'Save this rule?' first for a suggestion that persists, and allows on a yes", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.allow"), context(session), allowArgs(permissionAsk, 1));

    expect(askedSheet()).toEqual({
      title: "Save this rule?",
      body: "Always allow ls.\n\nClaude writes it to a settings file, so it lasts past this session.",
      action: "Allow and save",
      destructive: false,
    });
    expect(answer).toHaveBeenCalledWith(1, "perm-1", { allow: true, suggestion: 1 });
  });

  it("allows nothing when the rule's sheet is cancelled", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.allow"), context(session), allowArgs(permissionAsk, 1));

    expect(answer).not.toHaveBeenCalled();
  });

  it("keys its in-flight mark by the ask", async () => {
    const gate = deferred<ReturnType<typeof noticesOk>>();
    const answer = vi.spyOn(api, "answer").mockReturnValue(gate.promise);
    const ctx = context(makeSession());
    const other: Ask = { ...permissionAsk, id: "perm-2" };

    const first = runCommand(command("ask.allow"), ctx, allowArgs(permissionAsk, 0));
    const again = runCommand(command("ask.allow"), ctx, allowArgs(permissionAsk, 0));
    const second = runCommand(command("ask.allow"), ctx, allowArgs(other, 0));
    gate.resolve(noticesOk());
    await Promise.all([first, again, second]);

    expect(answer.mock.calls.map((call) => call[1])).toEqual(["perm-1", "perm-2"]);
  });
});

describe("ask.deny", () => {
  it("asks what Claude should do instead, destructively, with the message's limit", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("ask.deny"), context(session), { ask: "perm-1" });

    expect(askedSheet()).toEqual({
      title: "Deny this?",
      body: 'Claude stops this turn, as "No" does in the terminal.',
      action: "Deny",
      destructive: true,
      field: {
        kind: "text",
        label: "What Claude should do instead",
        placeholder: "What Claude should do instead (optional)",
        max: MAX_MESSAGE_BYTES,
        tooLong: "Too long: a message is at most 64 KiB.",
      },
    });
  });

  it("denies with the trimmed message", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("  use ls instead  ");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.deny"), context(session), { ask: "perm-1" });

    expect(answer).toHaveBeenCalledWith(1, "perm-1", { allow: false, message: "use ls instead" });
  });

  it("denies with an empty message when none was typed", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.deny"), context(session), { ask: "perm-1" });

    expect(answer).toHaveBeenCalledWith(1, "perm-1", { allow: false, message: "" });
  });

  it("denies nothing when the sheet is cancelled", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.deny"), context(session), { ask: "perm-1" });

    expect(answer).not.toHaveBeenCalled();
  });
});
```

Fails without: "is the plain Allow at suggestion 0": `allowArgs` treating 0 as suggestion 1 (`suggestions[-1]`), or giving `persists` true. "is the 1-based suggestion…": `allowArgs` indexing `suggestions[suggestion]` (0-based) returns the wrong label and `persists`. "does not persist a suggestion that does not say so": `persists` taken as `s.persists !== false`. "refuses a suggestion the ask does not hold…": the out-of-range guard removed, so `allowArgs` reads a property of undefined or invents a label. "is null until every question…": the `every`-question loop returning early after the first question, or the empty-answer check removed. "does not count an Other text of whitespace…": `other` tested untrimmed, or a note counted as an answer. "is null when the draft lacks one of the questions": the `typed === undefined` return removed, so the missing entry's `typed.other` reads a property of undefined and throws. "is null for an ask with no questions": the vacuous `every` returning `{ answers: {} }`. "keys each answer by its question's text…": `labels` sorted, `other` or `note` sent untrimmed. "leaves other and note out…": `other: ""` or `note: ""` always set (the `toStrictEqual` catches a present key). "answers a single-select question with its Other text alone": labels sent for a question answered by Other. "gives a single-select question one answer…": a single-select question sending both its label and its Other, or every label. "lets a multi-select question carry labels and Other together": the single-select rule applied to every question. "is null while an Other text or a note passes the limit in bytes, and not at it": the limit compared with `>=`, or applied to Other only, or to the note only. "measures the limit on the trimmed text it would send…": `utf8Length(typed.other)` or `utf8Length(typed.note)` taken before the trim, so a limit-sized text with trailing whitespace is over. "does not measure an Other text that a single-select question's label drops…": the Other limit checked before the single-select rule empties `other`, so the dropped over-limit text turns the answer off; and its second expect (a single-select question answered by Other alone, over the limit, is still null) fails if the limit is not checked on `other` at all. "counts bytes, not characters…": `.length` instead of `utf8Length`. "%s is on with a session…": an ask command's `when` that does not check `ctx.session !== undefined`. "sends the body to the ask and drops that ask's draft, and no other": `api.answer` given another id, or `clear()` instead of `dropAsk`. "keeps the draft on a 202…" and "keeps the draft and toasts…": the draft dropped on any outcome but a refusal, or but a 202. "keys its in-flight mark by the ask…" (both): `key` removed, so the second ask is held by the first; `key` constant, likewise. "asks for what to clarify…": the sheet's words or its field's limit changed. "sends the trimmed text as a chat…": `chat_text` untrimmed, or the draft kept. "sends an empty chat text…": the input skipped when it is empty. "sends nothing, and keeps the draft, when the sheet is cancelled": the answer sent before the sheet's value is read. "keeps the draft when the daemon answers 202 or refuses": `ask.chat` dropping the draft on any outcome. "allows with no sheet and no suggestion for the plain Allow": `suggestion: 0` sent, or a sheet shown for the plain Allow. "allows a suggestion that does not persist with no sheet…": a sheet shown for a non-persisting suggestion, or `suggestion` sent 0-based. "asks 'Save this rule?' first…": the `persists` test removed (no sheet), or the sheet's words changed. "allows nothing when the rule's sheet is cancelled": the answer sent before the sheet's value is read. "keys its in-flight mark by the ask": `key` removed from `ask.allow`. "asks what Claude should do instead…": the sheet not destructive, or its field's limit changed. "denies with the trimmed message": `message` untrimmed. "denies with an empty message…": `message` left out when empty. "denies nothing when the sheet is cancelled": the answer sent before the sheet's value is read.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/commands/asks.test.ts`
Expected: FAIL with `Failed to resolve import "./asks" from "src/commands/asks.test.ts"`: the module does not exist yet.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/commands/asks.ts`:

```ts
// The ask commands: answer a question, chat about it, allow or deny a permission, each keyed by
// its ask's id so two asks never hold each other (spec Part 2 decision 7), with the classic
// app's sheets and words (asks.mjs). `allowArgs` and `answerBody` are the pure halves the
// permission card and the question form call: what an Allow sends, and what Answer sends and when
// it is on.
import type { AnswerBody, Ask, QuestionAnswerBody } from "@/api/gen";
import { api } from "@/api/request";
import { defineCommand, type CommandContext } from "@/app/commands";
import { showOutcome } from "@/app/toasts";
import { MAX_MESSAGE_BYTES, utf8Length } from "@/lib/utf8";
import { useDrafts, type AskDraft } from "@/store/drafts";

import { requireSession } from "./session";

// AllowArgs is what `ask.allow` takes: the ask, which suggestion (0 for the plain Allow, else
// 1-based), its label for the sheet, and whether Claude would save it as a rule.
export interface AllowArgs {
  ask: string;
  suggestion: number;
  label: string;
  persists: boolean;
}

// allowArgs is the args of the permission card's Allow button for `suggestion`: 0 is the plain
// Allow, which saves nothing; n >= 1 is the ask's n-th suggestion, which persists only when that
// suggestion says so. A suggestion the ask does not hold is an error, never another's rule.
export function allowArgs(ask: Ask, suggestion: number): AllowArgs {
  if (suggestion === 0) return { ask: ask.id, suggestion: 0, label: "Allow", persists: false };
  const chosen = ask.suggestions?.[suggestion - 1];
  if (chosen === undefined) throw new Error(`loom: ask ${ask.id} has no suggestion ${suggestion}`);
  return { ask: ask.id, suggestion, label: chosen.label, persists: chosen.persists === true };
}

// answerBody is the body the question form's Answer sends, or null while Answer is off: until
// every question has a picked label or an Other text, and while an Other text or a note that
// would be sent passes the daemon's limit in UTF-8 bytes. Other and note are trimmed and left out
// when empty, and a single-select question carries one answer, its last picked label or else its
// Other text, never both. The limit is checked on that sent text, as the daemon checks it: not on
// whitespace trimmed away, and not on an Other text a single-select question's label drops.
export function answerBody(ask: Ask, draft: AskDraft | undefined): AnswerBody | null {
  const questions = ask.questions ?? [];
  if (questions.length === 0 || draft === undefined) return null;
  const answers: Record<string, QuestionAnswerBody> = {};
  for (const question of questions) {
    const typed = draft.answers[question.question];
    if (typed === undefined) return null;
    let other = typed.other.trim();
    if (typed.labels.length === 0 && other === "") return null;
    let labels = typed.labels;
    if (!question.multi && labels.length > 0) {
      labels = labels.slice(-1);
      other = "";
    }
    const note = typed.note.trim();
    if (utf8Length(other) > MAX_MESSAGE_BYTES || utf8Length(note) > MAX_MESSAGE_BYTES) return null;
    const answer: QuestionAnswerBody = { labels };
    if (other !== "") answer.other = other;
    if (note !== "") answer.note = note;
    answers[question.question] = answer;
  }
  return { answers };
}

// answer sends body as the answer to the ask and reports whether the daemon answered 200: a 202
// leaves the ask on screen until the stream says it was answered, so nothing is dropped for it.
async function answer(ctx: CommandContext, ask: string, body: AnswerBody, what: string) {
  const outcome = await api.answer(requireSession(ctx).id, ask, body);
  showOutcome(outcome, what);
  return outcome.kind === "ok";
}

defineCommand<{ ask: string; body: AnswerBody }>({
  id: "ask.answer",
  title: "Answer",
  key: (args) => args.ask,
  when: (ctx) => ctx.session !== undefined,
  run: async (ctx, args) => {
    if (await answer(ctx, args.ask, args.body, "Answer")) useDrafts.getState().dropAsk(args.ask);
  },
});

const TOO_LONG = "Too long: a message is at most 64 KiB.";

defineCommand<{ ask: string }>({
  id: "ask.chat",
  title: "Chat about this",
  key: (args) => args.ask,
  when: (ctx) => ctx.session !== undefined,
  confirm: () => ({
    title: "Chat about this",
    body: "Claude puts the questions aside and asks what you want to clarify.",
    action: "Send",
    destructive: false,
    field: {
      kind: "text",
      label: "What to clarify",
      placeholder: "What to clarify (optional)",
      max: MAX_MESSAGE_BYTES,
      tooLong: TOO_LONG,
    },
  }),
  run: async (ctx, args, input) => {
    const body: AnswerBody = { chat: true, chat_text: input.trim() };
    if (await answer(ctx, args.ask, body, "Chat")) useDrafts.getState().dropAsk(args.ask);
  },
});

defineCommand<AllowArgs>({
  id: "ask.allow",
  title: "Allow",
  key: (args) => args.ask,
  when: (ctx) => ctx.session !== undefined,
  // Only a rule Claude would save asks first: the plain Allow and a one-time suggestion go at once.
  confirm: (ctx, args) =>
    args.persists
      ? {
          title: "Save this rule?",
          body: `${args.label}.\n\nClaude writes it to a settings file, so it lasts past this session.`,
          action: "Allow and save",
          destructive: false,
        }
      : null,
  run: async (ctx, args) => {
    const body: AnswerBody =
      args.suggestion > 0 ? { allow: true, suggestion: args.suggestion } : { allow: true };
    await answer(ctx, args.ask, body, "Allow");
  },
});

defineCommand<{ ask: string }>({
  id: "ask.deny",
  title: "Deny…",
  key: (args) => args.ask,
  when: (ctx) => ctx.session !== undefined,
  confirm: () => ({
    title: "Deny this?",
    body: 'Claude stops this turn, as "No" does in the terminal.',
    action: "Deny",
    destructive: true,
    field: {
      kind: "text",
      label: "What Claude should do instead",
      placeholder: "What Claude should do instead (optional)",
      max: MAX_MESSAGE_BYTES,
      tooLong: TOO_LONG,
    },
  }),
  run: async (ctx, args, input) => {
    await answer(ctx, args.ask, { allow: false, message: input.trim() }, "Deny");
  },
});
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/commands`
Expected: PASS, typecheck clean and every test of `src/commands` (the session file's and the ask file's).

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/commands/asks.ts internal/web/ui/src/commands/asks.test.ts
git commit -m "feat(web): the ask commands, with allowArgs and answerBody for the forms" -- internal/web/ui/src/commands/asks.ts internal/web/ui/src/commands/asks.test.ts
```

### Task 3.4: The job commands, and the registration

**Files:**
- Create: `internal/web/ui/src/commands/jobs.ts`
- Create: `internal/web/ui/src/commands/index.ts`
- Modify: `internal/web/ui/src/main.tsx`
- Create: `internal/web/ui/src/commands/jobs.test.ts`
- Create: `internal/web/ui/src/commands/index.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/commands/jobs.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { askConfirm } from "@/app/ConfirmSheet";

import "./jobs";
import {
  askedSheet,
  command,
  context,
  deferred,
  makeJob,
  makeSession,
  noticesOk,
  resetFleet,
  seed,
  spyOnAdd,
} from "./testkit";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  add = spyOnAdd();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
});

const CONTROLS: [string, string][] = [
  ["job.park", "park"],
  ["job.unpark", "unpark"],
  ["job.pause", "pause"],
  ["job.resume", "resume"],
];

describe("the job commands' availability", () => {
  it.each(["job.start", ...CONTROLS.map(([id]) => id)])(
    "%s is on with a session on screen, and off with none or while the snapshot is stale",
    (id) => {
      const session = makeSession();
      expect(commandEnabled(command(id), context(session))).toBe(true);
      expect(commandEnabled(command(id), context(undefined))).toBe(false);
      expect(commandEnabled(command(id), context(session, { stale: true, link: "offline" }))).toBe(
        false,
      );
    },
  );
});

describe("park, unpark, pause and resume", () => {
  it.each(CONTROLS)("%s controls the job with no sheet", async (id, action) => {
    const control = vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk());

    await runCommand(command(id), context(makeSession()), { job: 214 });

    expect(askConfirm).not.toHaveBeenCalled();
    expect(control).toHaveBeenCalledOnce();
    expect(control).toHaveBeenCalledWith(1, 214, action);
  });

  it("toasts a refusal in the model's words", async () => {
    vi.spyOn(api, "jobControl").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "job 214 is not running",
    });

    await runCommand(command("job.pause"), context(makeSession()), { job: 214 });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "job 214 is not running", type: "error" }),
    );
  });

  it("says a 202's outcome will follow", async () => {
    vi.spyOn(api, "jobControl").mockResolvedValue({ kind: "pending" });

    await runCommand(command("job.park"), context(makeSession()), { job: 214 });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Park is still running. Its outcome will follow.",
        type: "info",
      }),
    );
  });

  it("keys each control by its job: another job's runs beside it, the same job's runs once", async () => {
    const gate = deferred<ReturnType<typeof noticesOk>>();
    const control = vi.spyOn(api, "jobControl").mockReturnValue(gate.promise);
    const ctx = context(makeSession());

    const first = runCommand(command("job.pause"), ctx, { job: 1 });
    const again = runCommand(command("job.pause"), ctx, { job: 1 });
    const other = runCommand(command("job.pause"), ctx, { job: 2 });
    gate.resolve(noticesOk());
    await Promise.all([first, again, other]);

    expect(control.mock.calls).toEqual([
      [1, 1, "pause"],
      [1, 2, "pause"],
    ]);
  });
});

describe("job.start", () => {
  it("asks 'Start job N now?', naming what the job waits for", async () => {
    const session = makeSession({
      jobs: [makeJob({ id: 216, waiting: "memory: 6 GiB free of 8 GiB" })],
    });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("job.start"), context(session), { job: 216 });

    expect(askedSheet()).toEqual({
      title: "Start job 216 now?",
      body: "It waits: memory: 6 GiB free of 8 GiB. It starts past the queue's fit; the ladder takes it last if memory runs short.",
      action: "Start now",
      destructive: false,
    });
  });

  it("leaves out the wait when the job names none, and for a job the session does not list", async () => {
    const session = makeSession({ jobs: [makeJob({ id: 216, waiting: "" })] });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const plain =
      "It starts past the queue's fit; the ladder takes it last if memory runs short.";

    await runCommand(command("job.start"), context(session), { job: 216 });
    expect(askedSheet().body).toBe(plain);

    vi.mocked(askConfirm).mockClear();
    await runCommand(command("job.start"), context(session), { job: 999 });
    expect(askedSheet().title).toBe("Start job 999 now?");
    expect(askedSheet().body).toBe(plain);
  });

  it("starts the job on a yes", async () => {
    const session = makeSession({ jobs: [makeJob({ id: 216 })] });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const control = vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk());

    await runCommand(command("job.start"), context(session), { job: 216 });

    expect(control).toHaveBeenCalledOnce();
    expect(control).toHaveBeenCalledWith(1, 216, "start");
  });

  it("starts nothing when the sheet is cancelled", async () => {
    const session = makeSession({ jobs: [makeJob({ id: 216 })] });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const control = vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk());

    await runCommand(command("job.start"), context(session), { job: 216 });

    expect(control).not.toHaveBeenCalled();
  });
});
```

Create `internal/web/ui/src/commands/index.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { commandById } from "@/app/commands";

import "./index";

describe("the commands module", () => {
  it.each([
    "session.send",
    "session.pause",
    "session.resume",
    "session.push",
    "session.merge",
    "session.kill",
    "ask.answer",
    "ask.chat",
    "ask.allow",
    "ask.deny",
    "job.start",
    "job.park",
    "job.unpark",
    "job.pause",
    "job.resume",
  ])("registers %s on import", (id) => {
    expect(commandById(id)).toBeDefined();
  });
});
```

Fails without: "%s controls the job with no sheet": `api.jobControl` given another action name, a `confirm` on a control, or the session's id for the job's. "toasts a refusal…" and "says a 202's outcome will follow": the outcome not handed to `showOutcome`. "keys each control by its job…": `key` removed (job 2 held by job 1) or the key constant. "asks 'Start job N now?'…": the title or body changed, or `waiting` not read from the session's job. "leaves out the wait…": the wait printed as `It waits: .` for an empty `waiting`, or a throw for a job the session does not list. "starts the job on a yes" and "starts nothing when the sheet is cancelled": the `start` call made without the sheet, or with another action. "%s is on with a session…": a job command's `when` that does not check `ctx.session !== undefined`. "registers %s on import": a module's `import` removed from `commands/index.ts`, so its commands are never registered.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/commands/jobs.test.ts src/commands/index.test.ts`
Expected: both files FAIL with `Failed to resolve import "./jobs"` and `Failed to resolve import "./index"`: neither module exists yet.

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/commands/jobs.ts`:

```ts
// The job commands: start, park, unpark, pause and resume a work-queue job of the session on
// screen, each keyed by the job's id so a control on one job never holds one on another (spec
// Part 2 decision 7). The daemon decides what each state allows; the job view draws only the
// buttons its state offers (job.mjs `controls`).
import { api } from "@/api/request";
import { defineCommand, type CommandContext } from "@/app/commands";
import { showOutcome } from "@/app/toasts";

import { requireSession } from "./session";

interface JobArgs {
  job: number;
}

// control asks the daemon for `action` on the job; `what` names it in a toast.
async function control(ctx: CommandContext, job: number, action: string, what: string) {
  showOutcome(await api.jobControl(requireSession(ctx).id, job, action), what);
}

const key = (args: JobArgs): string => String(args.job);
const when = (ctx: CommandContext): boolean => ctx.session !== undefined;

const QUEUE_NOTE =
  "It starts past the queue's fit; the ladder takes it last if memory runs short.";

defineCommand<JobArgs>({
  id: "job.start",
  title: "Start now",
  key,
  when,
  confirm: (ctx, args) => {
    const waiting = ctx.session?.jobs?.find((job) => job.id === args.job)?.waiting ?? "";
    return {
      title: `Start job ${args.job} now?`,
      body: waiting === "" ? QUEUE_NOTE : `It waits: ${waiting}. ${QUEUE_NOTE}`,
      action: "Start now",
      destructive: false,
    };
  },
  run: (ctx, args) => control(ctx, args.job, "start", "Start"),
});

defineCommand<JobArgs>({
  id: "job.park",
  title: "Park",
  key,
  when,
  run: (ctx, args) => control(ctx, args.job, "park", "Park"),
});

defineCommand<JobArgs>({
  id: "job.unpark",
  title: "Unpark",
  key,
  when,
  run: (ctx, args) => control(ctx, args.job, "unpark", "Unpark"),
});

defineCommand<JobArgs>({
  id: "job.pause",
  title: "Pause",
  key,
  when,
  run: (ctx, args) => control(ctx, args.job, "pause", "Pause"),
});

defineCommand<JobArgs>({
  id: "job.resume",
  title: "Resume",
  key,
  when,
  run: (ctx, args) => control(ctx, args.job, "resume", "Resume"),
});
```

Create `internal/web/ui/src/commands/index.ts`:

```ts
// The commands of the session's views register by being imported: main.tsx imports this module,
// and a test that draws a CommandButton for one of them imports it too.
import "./session";
import "./asks";
import "./jobs";
```

In `internal/web/ui/src/main.tsx`, replace

```tsx
import "@/panels";
```

with

```tsx
import "@/panels";
import "@/commands";
```

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/commands`
Expected: PASS, typecheck clean and every test of `src/commands`.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/commands/jobs.ts internal/web/ui/src/commands/jobs.test.ts internal/web/ui/src/commands/index.ts internal/web/ui/src/commands/index.test.ts
git commit -m "feat(web): the job commands, and the commands module the app imports" -- internal/web/ui/src/commands/jobs.ts internal/web/ui/src/commands/jobs.test.ts internal/web/ui/src/commands/index.ts internal/web/ui/src/commands/index.test.ts internal/web/ui/src/main.tsx
```

### Task 3.5: The rules the group reading amends, and the rebuilt bundle

**Files:**
- Modify: `internal/web/ui/CLAUDE.md:9`
- Modify: `internal/web/CLAUDE.md:22`
- Modify: `internal/web/dist/`

- [ ] **Step 1: Amend the command rule**

In `internal/web/ui/CLAUDE.md`, replace

```
- **A command's `when` reads only the session's status, its `claude` flag, the route and the link state, and only an `offline` command runs while the snapshot is stale.** The model decides what is allowed and a wrong guess is its 409, shown as a toast; a copied precondition drifts
```

with

```
- **A command's `when` reads only the session's status and group, its `claude` flag, the route and the link state, and only an `offline` command runs while the snapshot is stale.** The model decides what is allowed and a wrong guess is its 409, shown as a toast; `group` is read because only it tells a Recoverable or unstarted Ready session from a started Ready, as the classic actions do; a copied precondition drifts
```

(the rest of the line, from `, then offers what the daemon refuses` on, is unchanged).

- [ ] **Step 2: Amend the web layer's clause**

In `internal/web/CLAUDE.md`, replace

```
the app's buttons only follow the session's `status`.
```

with

```
the app's buttons only follow the session's `status` and `group`.
```

(it is on line 22, in the rule that begins `**Answer a refused action 409`).

- [ ] **Step 3: Rebuild the bundle**

Run: `pnpm -C internal/web/ui run build`
Expected: vite prints the built assets and exits 0; `git status --short internal/web/dist` lists the changed entry script and, if its hash moved, `index.html`.

- [ ] **Step 4: Run the story's gates**

Run: `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/'`
If `format:check` flags the new code, run `pnpm -C internal/web/ui exec prettier --write` on the story's files, rerun the gates and commit the formatting with the bundle. Expected: `check` passes (typecheck, lint, format check, every Vitest test, the worker's syntax check), `dist:check` prints that the committed bundle is fresh, and `go test` prints `ok` for `internal/web` (`TestDist_FirstLoadIsWithinBudget` over the rebuilt bundle) and for `tools/claudemd` (the two CLAUDE.md files keep their shape and budgets: `internal/web/CLAUDE.md` gains 12 bytes of its 210 bytes of headroom).

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/dist
git commit -m "docs(web): a command's when may read the session's group; rebuild the bundle with the session commands" -- internal/web/ui/CLAUDE.md internal/web/CLAUDE.md internal/web/dist
```

## Story 4: The reads: turn pages, the diff and job logs, with their refresh rules and kept caches

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/api/reads.ts`, `internal/web/ui/src/api/reads.test.ts`, `internal/web/dist/`
- **Interfaces:** exposes `TurnsLoad`, `HeldTurns`, `mergeTurns`, `TurnPagesOptions`, `TurnPages`, `useTurnPages`, `useHeldTurns`, `keptScroll`, `keepScroll`, `KEEP_SESSIONS`, `KEEP_JOBS`, `MAX_LOG_CHARS`, `PatchOptions`, `PatchView`, `usePatch`, `HeldLog`, `appendLog`, `JobLogOptions`, `JobLogView`, `useJobLog`; uses part 1's `api`, `queryClient`, `unwrap` and `OutcomeError`, and Story 1's fixture loaders
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** full (the bounded caches, the log cap and the single-flight merge every conversation and log depends on; a wrong merge loses or repeats turns silently)

Adds `internal/web/ui/src/api/reads.ts`, the one module every part 2 read goes through (spec §26, Part 2 settlements 10, 11 and 15). Each read is a TanStack Query entry in `queryClient` with the classic app's rules: a conversation's (or a subagent's) turn pages are paged back by `before`, refreshed by `after` and merged as the classic `mergeTurns` does (`internal/web/static/js/turns.mjs`), with one request in flight and a refresh asked meanwhile run once when it lands; the pages of the last 8 conversations stay in the cache with the page's scroll, a subagent's leave with their last reader; the diff is read on mount, again when the caller's signature changes once a patch is held and every 10 s while live; a job's log is appended by `after` up to 512 Ki characters, polled every 2 s while live and once more when it ends, gone on a 409, and kept for the last 5 jobs. Every retry is switched off (`retry: false`): a poll or the view's own retry is the next attempt, as classic. The story changes no other file. Nothing imports `reads.ts` yet (Stories 8, 10, 11 and 12 do), so the bundle in `internal/web/dist/` does not change; the last task proves it. No landmine marker appears in this story's code.

### Task 4.1: Types, constants, `mergeTurns` and `appendLog`

**Files:**
- Create: `internal/web/ui/src/api/reads.ts`
- Create: `internal/web/ui/src/api/reads.test.ts`

- [ ] **Step 1: Write the failing test**

Create `internal/web/ui/src/api/reads.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { fixtureTurns } from "@/fixtures";
import type { AgentPage, JobLog, Turn, Turns } from "./gen";
import {
  appendLog,
  mergeTurns,
  MAX_LOG_CHARS,
  type HeldLog,
  type HeldTurns,
  type TurnsLoad,
} from "./reads";

function turn(id: number): Turn {
  return {
    id,
    at: "2026-11-19T10:00:00Z",
    from: "you",
    name: "",
    text: `turn ${id}`,
    cut: false,
    command: false,
    partial: false,
    items: null,
  };
}

function page(ids: number[], extra: Partial<Turns> = {}): Turns {
  return {
    conversation: "c1",
    turns: ids.map(turn),
    earlier: null,
    reset: false,
    skipped: 0,
    ...extra,
  };
}

function heldOf(ids: number[], extra: Partial<HeldTurns> = {}): HeldTurns {
  return {
    conversation: "c1",
    turns: ids.map(turn),
    earlier: null,
    skipped: 0,
    agent: undefined,
    ...extra,
  };
}

const idsOf = (held: HeldTurns): number[] => held.turns.map((t) => t.id);

function logPage(text: string, end: number, extra: Partial<JobLog> = {}): JobLog {
  return { text, start: 0, end, cut: false, reset: false, ...extra };
}

describe("mergeTurns", () => {
  it.each<{ when: string; held: HeldTurns | undefined; next: Turns; load: TurnsLoad }>([
    { when: "no pages are held", held: undefined, next: page([5, 6], { earlier: 4 }), load: "refresh" },
    { when: "the load is newest", held: heldOf([1, 2]), next: page([5, 6], { earlier: 4 }), load: "newest" },
    {
      when: "the page is a reset",
      held: heldOf([1, 2]),
      next: page([5, 6], { earlier: 4, reset: true }),
      load: "refresh",
    },
  ])("replaces what is held when $when", ({ held, next, load }) => {
    const merged = mergeTurns(held, next, load);
    expect(idsOf(merged)).toEqual([5, 6]);
    expect(merged.conversation).toBe("c1");
    expect(merged.earlier).toBe(4);
  });

  it("puts an earlier page before the held turns, dropping a turn the held ones start with", () => {
    const held = heldOf([4, 5, 6], { earlier: 4 });
    const merged = mergeTurns(held, page([2, 3, 4], { earlier: 2 }), "earlier");
    expect(idsOf(merged)).toEqual([2, 3, 4, 5, 6]);
    expect(merged.earlier).toBe(2);
    const start = mergeTurns(held, page([1, 2, 3], { earlier: null }), "earlier");
    expect(start.earlier).toBeNull();
  });

  it("replaces every held turn from the refresh page's first on, and keeps `earlier`", () => {
    const held = heldOf([1, 2, 3], { earlier: 1 });
    const grown: Turn = { ...turn(3), text: "grown", partial: false };
    const merged = mergeTurns(held, { ...page([4]), turns: [grown, turn(4)], earlier: null }, "refresh");
    expect(idsOf(merged)).toEqual([1, 2, 3, 4]);
    expect(merged.turns[2]?.text).toBe("grown");
    expect(merged.earlier).toBe(1);
  });

  it("keeps the held turns when a refresh page holds none", () => {
    const held = heldOf([1, 2], { earlier: 1 });
    const merged = mergeTurns(held, { ...page([]), turns: null }, "refresh");
    expect(idsOf(merged)).toEqual([1, 2]);
    expect(merged.earlier).toBe(1);
  });

  it("keeps `skipped` once a page reported a record it could not read", () => {
    expect(mergeTurns(heldOf([1], { skipped: 2 }), page([2]), "refresh").skipped).toBe(2);
    expect(mergeTurns(heldOf([1]), page([2], { skipped: 1 }), "refresh").skipped).toBe(1);
    expect(mergeTurns(heldOf([1], { skipped: 2 }), page([2], { skipped: 3 }), "refresh").skipped).toBe(3);
    expect(mergeTurns(undefined, page([2], { skipped: 1 }), "newest").skipped).toBe(1);
  });

  it("takes the subagent from the latest page", () => {
    const agent: AgentPage = {
      id: "a1f00dcafe0001",
      type: "Explore",
      description: "look around",
      teammate: false,
      state: "running",
      started: "2026-11-19T10:00:00Z",
      missing: false,
    };
    expect(mergeTurns(heldOf([1]), page([2], { agent }), "refresh").agent).toEqual(agent);
    expect(mergeTurns(heldOf([4], { earlier: 4 }), page([3], { agent }), "earlier").agent).toEqual(agent);
    expect(mergeTurns(undefined, page([1]), "newest").agent).toBeUndefined();
  });

  it("holds the fixture's page as it came", () => {
    const fixture = fixtureTurns();
    const merged = mergeTurns(undefined, fixture, "newest");
    expect(merged.turns).toEqual(fixture.turns ?? []);
    expect(merged.conversation).toBe(fixture.conversation);
  });
});

describe("appendLog", () => {
  it("holds a first page as it came", () => {
    expect(appendLog(undefined, logPage("one\n", 4, { cut: true }))).toEqual({
      text: "one\n",
      end: 4,
      cut: true,
    });
  });

  it("replaces the held log with a reset page", () => {
    const held: HeldLog = { text: "old\n", end: 4, cut: false };
    expect(appendLog(held, logPage("new\n", 9, { reset: true, cut: true }))).toEqual({
      text: "new\n",
      end: 9,
      cut: true,
    });
  });

  it("appends a page, moves `end` and keeps the held `cut`", () => {
    const held: HeldLog = { text: "one\n", end: 4, cut: true };
    expect(appendLog(held, logPage("two\n", 8, { cut: false }))).toEqual({
      text: "one\ntwo\n",
      end: 8,
      cut: true,
    });
  });

  it("trims past the cap at the next newline and marks the log cut", () => {
    const held: HeldLog = { text: "AAAA\n" + "b".repeat(MAX_LOG_CHARS - 5), end: 1, cut: false };
    const next = appendLog(held, logPage("\ncc", 2));
    expect(next.cut).toBe(true);
    expect(next.text.startsWith("b")).toBe(true);
    expect(next.text.endsWith("\ncc")).toBe(true);
    expect(next.text.length).toBe(MAX_LOG_CHARS - 2);
  });

  it("trims past the cap in mid-line when there is no newline to trim at", () => {
    const held: HeldLog = { text: "x".repeat(MAX_LOG_CHARS), end: 1, cut: false };
    const next = appendLog(held, logPage("yyy", 2));
    expect(next.cut).toBe(true);
    expect(next.text.length).toBe(MAX_LOG_CHARS);
    expect(next.text.endsWith("xxxyyy")).toBe(true);
  });

  it("leaves a log of exactly the cap whole", () => {
    const held: HeldLog = { text: "x".repeat(MAX_LOG_CHARS - 3), end: 1, cut: false };
    const next = appendLog(held, logPage("yyy", 2));
    expect(next.cut).toBe(false);
    expect(next.text.length).toBe(MAX_LOG_CHARS);
  });
});
```

Fails without: `replaces what is held when no pages are held`: `mergeTurns`'s `held === undefined` term removed (the refresh path reads `held.turns` of undefined and throws).
Fails without: `replaces what is held when the load is newest`: the `load === "newest"` term removed (a newest load with pages held merges as a refresh and keeps ids 1 and 2).
Fails without: `replaces what is held when the page is a reset`: the `page.reset` term removed (the reset page merges as a refresh and keeps ids 1 and 2).
Fails without: `puts an earlier page before the held turns, dropping a turn the held ones start with`: the `t.id < first` filter removed (id 4 appears twice) or `earlier: page.earlier || null` removed (`earlier` stays 4).
Fails without: `replaces every held turn from the refresh page's first on, and keeps earlier`: the `t.id < from` filter changed to keep id 3 (turn 3 twice, the old text first).
Fails without: `keeps the held turns when a refresh page holds none`: the `turns.length === 0` guard removed (`turns[0].id` throws on an empty page).
Fails without: `keeps skipped once a page reported a record it could not read`: `Math.max(held?.skipped ?? 0, page.skipped)` replaced by `page.skipped` (the first expectation reads 0).
Fails without: `takes the subagent from the latest page`: `agent` dropped from the refresh or the earlier return (it stays undefined).
Fails without: `appends a page, moves end and keeps the held cut`: `cut: held.cut` replaced by `cut: page.cut` (the result reads false).
Fails without: `trims past the cap at the next newline and marks the log cut`: the trim removed (length stays `MAX_LOG_CHARS + 3`, `cut` false) or `indexOf("\n", from)` replaced by slicing at `from` (the text then starts with `"\n"` or `"b"` mid-run and its length is `MAX_LOG_CHARS`).
Fails without: `trims past the cap in mid-line when there is no newline to trim at`: the `nl >= 0 ? nl + 1 : from` fallback removed (`slice(0)` keeps all `MAX_LOG_CHARS + 3` characters).
Fails without: `leaves a log of exactly the cap whole`: the trim's `>` made `>=` (`cut` turns true).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts`

Expected: the file fails to load: `Failed to resolve import "./reads" from "src/api/reads.test.ts". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/api/reads.ts`:

```ts
// The reads of the session views (spec §26, Part 2 settlements 11 and 15): a conversation's or a
// subagent's turn pages, a session's diff and a job's log, each held in the query cache and read
// again by the classic app's rules (internal/web/static/js/turns.mjs, changes.mjs and job.mjs). A
// view reads through these hooks and never fetches on its own: one request is in flight per key,
// and a read asked for meanwhile runs once when it lands.
import type { AgentPage, JobLog, Turn, Turns } from "./gen";

/** How many conversations keep their pages and scroll: a ninth evicts the least recently opened. */
export const KEEP_SESSIONS = 8;
/** How many jobs keep their log: a sixth evicts the least recently opened. */
export const KEEP_JOBS = 5;
/** The most characters (UTF-16 units, as classic counts them) of one job's log that are held. */
export const MAX_LOG_CHARS = 524288;

/** Which page a turn read asks for: the newest, the newest turn refreshed, or the one before. */
export type TurnsLoad = "newest" | "refresh" | "earlier";

/** The turns held for a conversation or a subagent, oldest first. */
export interface HeldTurns {
  conversation: string;
  turns: Turn[];
  /** The `before` of the page ahead of the first held turn; null when there is none. */
  earlier: number | null;
  /** The most records any page reported unreadable: once set it stays set. */
  skipped: number;
  /** The subagent the latest page described; undefined on a conversation. */
  agent: AgentPage | undefined;
}

/**
 * Folds a page into the turns held, as the classic `mergeTurns` does: the first page, a newest
 * load and a reset replace them, an earlier page goes before them, and a refresh replaces every
 * held turn from its own first turn on.
 */
export function mergeTurns(held: HeldTurns | undefined, page: Turns, load: TurnsLoad): HeldTurns {
  const skipped = Math.max(held?.skipped ?? 0, page.skipped);
  const turns = page.turns ?? [];
  const agent = page.agent;
  if (held === undefined || page.reset || load === "newest") {
    return { conversation: page.conversation, turns, earlier: page.earlier || null, skipped, agent };
  }
  if (load === "earlier") {
    const first = held.turns.length > 0 ? held.turns[0].id : Infinity;
    return {
      ...held,
      turns: turns.filter((t) => t.id < first).concat(held.turns),
      earlier: page.earlier || null,
      skipped,
      agent,
    };
  }
  if (turns.length === 0) return { ...held, skipped, agent };
  const from = turns[0].id;
  return { ...held, turns: held.turns.filter((t) => t.id < from).concat(turns), skipped, agent };
}

/** A job's log as held: its text, the offset the next read continues from, and whether it is cut. */
export interface HeldLog {
  text: string;
  end: number;
  cut: boolean;
}

/**
 * Adds a log page to the log held, as the classic `appended` does: a reset page, or no log yet,
 * replaces it; the text is then trimmed to `MAX_LOG_CHARS` at the next newline, and marked cut.
 */
export function appendLog(held: HeldLog | undefined, page: JobLog): HeldLog {
  let next: HeldLog =
    held === undefined || page.reset
      ? { text: page.text, end: page.end, cut: page.cut }
      : { text: held.text + page.text, end: page.end, cut: held.cut };
  if (next.text.length > MAX_LOG_CHARS) {
    const from = next.text.length - MAX_LOG_CHARS;
    const newline = next.text.indexOf("\n", from);
    next = { ...next, text: next.text.slice(newline >= 0 ? newline + 1 : from), cut: true };
  }
  return next;
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/api/reads.ts src/api/reads.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts'`

Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts
git commit -m "feat(web): merge turn pages and append job logs as the classic app does" -- internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts
```

### Task 4.2: Turn pages, held in the query cache and kept for eight conversations

**Files:**
- Modify: `internal/web/ui/src/api/reads.ts` (the import line, then append at the end)
- Test: `internal/web/ui/src/api/reads.test.ts`

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/api/reads.test.ts` replace the import lines

```ts
import { describe, expect, it } from "vitest";
import { fixtureTurns } from "@/fixtures";
import type { AgentPage, JobLog, Turn, Turns } from "./gen";
import {
  appendLog,
  mergeTurns,
  MAX_LOG_CHARS,
  type HeldLog,
  type HeldTurns,
  type TurnsLoad,
} from "./reads";
```

with these imports and the helpers every hook test shares (the existing `function turn(` and the code after it stay as they are):

```ts
import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { createElement, type ReactElement, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fixtureTurns } from "@/fixtures";
import type { AgentPage, JobLog, Outcome, Turn, Turns } from "./gen";
import { queryClient } from "./queries";
import {
  appendLog,
  keepScroll,
  keptScroll,
  KEEP_SESSIONS,
  mergeTurns,
  MAX_LOG_CHARS,
  useHeldTurns,
  useTurnPages,
  type HeldLog,
  type HeldTurns,
  type TurnPagesOptions,
  type TurnsLoad,
} from "./reads";
import { api } from "./request";

// Timers are faked so a poll's cadence is exact: every wait is a settle, which also lets the
// query client's own zero-delay notifications run.
const spyOnTurns = () =>
  vi.spyOn(api, "turns").mockRejectedValue(new Error("unexpected turns read"));
let turnsRead: ReturnType<typeof spyOnTurns>;

beforeEach(() => {
  vi.useFakeTimers();
  queryClient.clear();
  turnsRead = spyOnTurns();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function settle(ms = 1): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

function wrapper({ children }: { children: ReactNode }): ReactElement {
  return createElement(QueryClientProvider, { client: queryClient }, children);
}

const ok = <T>(value: T): Outcome<T> => ({ kind: "ok", status: 200, value, notices: [] });
const refused = (status: number, message: string): Outcome<never> => ({
  kind: "refused",
  status,
  message,
});

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
```

Then append at the end of the file:

```ts
const options = (extra: Partial<TurnPagesOptions> = {}): TurnPagesOptions => ({
  session: 7,
  agent: undefined,
  live: false,
  ...extra,
});

function mountTurns(initial: TurnPagesOptions) {
  return renderHook((props: TurnPagesOptions) => useTurnPages(props), {
    initialProps: initial,
    wrapper,
  });
}

// open mounts a conversation, lets its page land and closes it again: it stays in the cache.
async function open(session: number): Promise<void> {
  turnsRead.mockResolvedValueOnce(ok(page([1])));
  const view = mountTurns(options({ session }));
  await settle();
  view.unmount();
}

describe("useTurnPages", () => {
  it("opens at the newest page and says so", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3], { earlier: 40 })));
    const { result } = mountTurns(options());
    expect(result.current.held).toBeUndefined();
    expect(result.current.landed).toBeUndefined();
    expect(result.current.loading).toBe(true);
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    expect(turnsRead).toHaveBeenCalledWith(7, {});
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3]);
    expect(result.current.held?.earlier).toBe(40);
    expect(result.current.landed).toBe("newest");
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBe("");
  });

  it("holds the fixture's page as mergeTurns would", async () => {
    const fixture = fixtureTurns();
    turnsRead.mockResolvedValueOnce(ok(fixture));
    const { result } = mountTurns(options());
    await settle();
    expect(result.current.held).toEqual(mergeTurns(undefined, fixture, "newest"));
  });

  it("refreshes from the newest turn every 5 s while live, and not otherwise", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const view = mountTurns(options({ live: true }));
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    await settle(5000);
    expect(turnsRead).toHaveBeenCalledTimes(2);
    expect(turnsRead).toHaveBeenLastCalledWith(7, { conversation: "c1", after: "3" });
    expect(view.result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3, 4]);
    expect(view.result.current.landed).toBe("refresh");
    view.rerender(options({ live: false }));
    await settle(20_000);
    expect(turnsRead).toHaveBeenCalledTimes(2);
  });

  it("pages back by `before` and asks nothing once the start is reached", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([5, 6], { earlier: 5 })));
    const { result } = mountTurns(options());
    await settle();
    turnsRead.mockResolvedValueOnce(ok(page([3, 4], { earlier: null })));
    act(() => {
      result.current.load("earlier");
    });
    await settle();
    expect(turnsRead).toHaveBeenLastCalledWith(7, { conversation: "c1", before: "5" });
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([3, 4, 5, 6]);
    expect(result.current.held?.earlier).toBeNull();
    expect(result.current.landed).toBe("earlier");
    act(() => {
      result.current.load("earlier");
    });
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(2);
  });

  it("calls a reset page newest, and replaces the turns", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const { result } = mountTurns(options());
    await settle();
    turnsRead.mockResolvedValueOnce(
      ok(page([8, 9], { conversation: "c2", reset: true })),
    );
    act(() => {
      result.current.load("refresh");
    });
    await settle();
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([8, 9]);
    expect(result.current.held?.conversation).toBe("c2");
    expect(result.current.landed).toBe("newest");
  });

  it("keeps one request in flight and runs a refresh asked meanwhile once, when it lands", async () => {
    const first = deferred<Outcome<Turns>>();
    turnsRead.mockReturnValueOnce(first.promise);
    const { result } = mountTurns(options());
    await settle();
    act(() => {
      result.current.load("refresh");
      result.current.load("refresh");
      result.current.load("earlier");
    });
    expect(turnsRead).toHaveBeenCalledTimes(1);
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    first.resolve(ok(page([1, 2, 3], { earlier: 9 })));
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(2);
    expect(turnsRead).toHaveBeenLastCalledWith(7, { conversation: "c1", after: "3" });
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3, 4]);
    await settle(10_000);
    expect(turnsRead).toHaveBeenCalledTimes(2);
  });

  it("says why a read failed, keeps the turns held, and clears the error on the next page", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    const { result } = mountTurns(options());
    await settle();
    turnsRead.mockResolvedValueOnce(refused(409, "session is busy"));
    act(() => {
      result.current.load("refresh");
    });
    await settle();
    expect(result.current.error).toBe("session is busy");
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([1, 2]);
    expect(result.current.loading).toBe(false);
    turnsRead.mockResolvedValueOnce(ok(page([2, 3])));
    act(() => {
      result.current.load("refresh");
    });
    await settle();
    expect(result.current.error).toBe("");
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3]);
  });

  it("gives two components with the same session and agent one request and one held state", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    const { result } = renderHook(() => ({ a: useTurnPages(options()), b: useTurnPages(options()) }), {
      wrapper,
    });
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    expect(result.current.a.held).toBeDefined();
    expect(result.current.a.held).toBe(result.current.b.held);
  });

  it("draws a conversation's pages first when it is opened again, and refreshes them", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const view = mountTurns(options());
    await settle();
    view.unmount();
    await settle(10 * 60_000);
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    const again = mountTurns(options());
    expect(again.result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3]);
    await settle();
    expect(turnsRead).toHaveBeenLastCalledWith(7, { conversation: "c1", after: "3" });
    expect(again.result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3, 4]);
  });

  it("does not keep a subagent's pages: a reopened view loads the newest", async () => {
    const agent = "a1f00dcafe0001";
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    const view = renderHook(
      () => ({
        pages: useTurnPages(options({ agent })),
        head: useHeldTurns(7, agent),
      }),
      { wrapper },
    );
    await settle();
    expect(turnsRead).toHaveBeenCalledWith(7, { agent });
    expect(view.result.current.head).toBeDefined();
    expect(view.result.current.head).toBe(view.result.current.pages.held);
    view.unmount();
    await settle();
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const again = mountTurns(options({ agent }));
    expect(again.result.current.held).toBeUndefined();
    await settle();
    expect(turnsRead).toHaveBeenLastCalledWith(7, { agent });
    expect(again.result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3]);
  });
});

describe("the kept scroll and the eight conversations kept", () => {
  it("keeps a page scroll with a conversation's pages", async () => {
    await open(3);
    expect(keptScroll(3)).toBeUndefined();
    keepScroll(3, 340);
    expect(keptScroll(3)).toBe(340);
    keepScroll(3, 120);
    expect(keptScroll(3)).toBe(120);
  });

  it("keeps no scroll for a conversation whose pages are not held", () => {
    keepScroll(99, 5);
    expect(keptScroll(99)).toBeUndefined();
  });

  it("keeps no scroll while a mounted conversation's first read is still out", async () => {
    const first = deferred<Outcome<Turns>>();
    turnsRead.mockReturnValueOnce(first.promise);
    const view = mountTurns(options({ session: 4 }));
    await settle();
    keepScroll(4, 77);
    expect(keptScroll(4)).toBeUndefined();
    first.resolve(ok(page([1])));
    await settle();
    expect(keptScroll(4)).toBeUndefined();
    keepScroll(4, 77);
    expect(keptScroll(4)).toBe(77);
    view.unmount();
  });

  it("evicts the least recently opened conversation's pages and scroll for a ninth", async () => {
    for (let session = 1; session <= KEEP_SESSIONS; session++) {
      await open(session);
      keepScroll(session, session * 100);
    }
    expect(keptScroll(1)).toBe(100);
    await open(KEEP_SESSIONS + 1);
    expect(keptScroll(1)).toBeUndefined();
    expect(renderHook(() => useHeldTurns(1, undefined), { wrapper }).result.current).toBeUndefined();
    expect(keptScroll(2)).toBe(200);
    expect(renderHook(() => useHeldTurns(2, undefined), { wrapper }).result.current).toBeDefined();
  });

  it("never evicts a conversation a view still shows", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1])));
    const shown = mountTurns(options({ session: 1 }));
    await settle();
    keepScroll(1, 100);
    for (let session = 2; session <= KEEP_SESSIONS + 1; session++) {
      await open(session);
      keepScroll(session, session * 100);
    }
    expect(keptScroll(1)).toBe(100);
    expect(shown.result.current.held).toBeDefined();
    expect(keptScroll(2)).toBeUndefined();
    expect(keptScroll(3)).toBe(300);
    shown.unmount();
  });

  it("neither reads nor brings back a conversation evicted while a refresh was queued", async () => {
    const first = deferred<Outcome<Turns>>();
    turnsRead.mockReturnValueOnce(first.promise);
    const view = mountTurns(options({ session: 1 }));
    await settle();
    act(() => {
      view.result.current.load("refresh");
    });
    view.unmount();
    for (let session = 2; session <= KEEP_SESSIONS + 1; session++) await open(session);
    first.resolve(ok(page([1])));
    await settle(100);
    expect(turnsRead).toHaveBeenCalledTimes(KEEP_SESSIONS + 1);
    expect(renderHook(() => useHeldTurns(1, undefined), { wrapper }).result.current).toBeUndefined();
  });

  it("counts opening a conversation again as using it", async () => {
    for (let session = 1; session <= KEEP_SESSIONS; session++) {
      await open(session);
      keepScroll(session, session * 100);
    }
    await open(1);
    await open(KEEP_SESSIONS + 1);
    expect(keptScroll(2)).toBeUndefined();
    expect(keptScroll(1)).toBe(100);
  });

  it("drops the scroll with the cache when the pairing is revoked", async () => {
    await open(3);
    keepScroll(3, 50);
    queryClient.clear();
    expect(keptScroll(3)).toBeUndefined();
    await open(3);
    expect(keptScroll(3)).toBeUndefined();
  });
});
```

Fails without: `opens at the newest page and says so`: the `landed` field not written with `held`, or the `{}` query changed (the request would carry a conversation or an offset).
Fails without: `refreshes from the newest turn every 5 s while live, and not otherwise`: `refetchInterval: live ? TURNS_POLL : false` removed or inverted (no poll while live, or a poll after live turns false).
Fails without: `pages back by before and asks nothing once the start is reached`: the `held === undefined || held.earlier === null` test for an `earlier` load in `requestTurns` removed (the last `load("earlier")` sends a third request, with no `before`).
Fails without: `calls a reset page newest, and replaces the turns`: `page.reset ? "newest" : kind` replaced by `kind` (`landed` reads `refresh`).
Fails without: `keeps one request in flight and runs a refresh asked meanwhile once, when it lands`: `again.add` for a refresh asked in flight removed (no second read), or `askedAgain` removed from `fetchTurns` (no second read), or the in-flight check removed (the asks join the read out and nothing follows it: one read).
Fails without: `says why a read failed, keeps the turns held, and clears the error on the next page`: `errorText(query.error, …)` replaced by `""` (the error never shows).
Fails without: `gives two components with the same session and agent one request and one held state`: the query key made per hook instead of per session and agent (two requests, two held objects).
Fails without: `draws a conversation's pages first when it is opened again, and refreshes them`: `gcTime: turnsRetention(agent)` returning the default for a conversation (the pages are gone after the 10 minutes and the second mount asks the newest page).
Fails without: `does not keep a subagent's pages: a reopened view loads the newest`: `turnsRetention` returning `Infinity` for an agent (the reopened view draws the old pages and asks `after`), or `useHeldTurns` not sharing the key (`head` stays undefined).
Fails without: `keeps a page scroll with a conversation's pages`: `keepScroll` storing nothing, or `keptScroll` reading another query.
Fails without: `keeps no scroll for a conversation whose pages are not held`: the `query !== undefined` term of `keepScroll`'s guard removed (reading `query.state` throws a `TypeError`).
Fails without: `keeps no scroll while a mounted conversation's first read is still out`: the `query.state.data !== undefined` term of `keepScroll`'s guard removed (the scroll is kept while no pages are held, and `keptScroll(4)` reads 77 before the read lands).
Fails without: `evicts the least recently opened conversation's pages and scroll for a ninth`: `keepRecent`'s `removeQueries` removed (session 1 keeps its scroll and pages) or the limit raised.
Fails without: `never evicts a conversation a view still shows`: the `getObserversCount() === 0` test in `keepRecent`'s `idle` removed (session 1, the oldest, is evicted: its scroll and pages are gone while its view is mounted).
Fails without: `neither reads nor brings back a conversation evicted while a refresh was queued`: the query-and-observer check in `askedAgain` removed (the queued refresh runs after the eviction: a tenth read, and the evicted conversation's pages are held again).
Fails without: `counts opening a conversation again as using it`: `openConversation` not called on mount (session 1, opened first, is evicted instead of 2).
Fails without: `drops the scroll with the cache when the pairing is revoked`: the scroll kept in a plain `Map` by session id (the reopened conversation reads 50).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts`

Expected: the `mergeTurns` and `appendLog` tests still pass; every test of `useTurnPages` and of the kept scroll fails with `TypeError: useTurnPages is not a function` (or `keepScroll is not a function`, `useHeldTurns is not a function`).

- [ ] **Step 3: Implement**

In `internal/web/ui/src/api/reads.ts` replace the line

```ts
import type { AgentPage, JobLog, Turn, Turns } from "./gen";
```

with

```ts
import { useQuery, type QueryKey } from "@tanstack/react-query";
import { useCallback, useEffect, useSyncExternalStore } from "react";
import type { AgentPage, JobLog, Turn, Turns, TurnsParams } from "./gen";
import { queryClient, unwrap } from "./queries";
import { api } from "./request";
```

and append at the end of the file:

```ts
// How often a live conversation is refreshed, as the classic app does.
const TURNS_POLL = 5000;

// again holds the reads asked for while one of the same key was in flight: each runs once when
// that one lands. A key is its query key's JSON.
const again = new Set<string>();

function hashOf(key: QueryKey): string {
  return JSON.stringify(key);
}

function inFlight(key: QueryKey): boolean {
  return queryClient.getQueryState(key)?.fetchStatus === "fetching";
}

// startRead runs read as the query of key. A read is never retried here: the next poll, or the
// view's own retry, is the next attempt, as classic. The promise's failure is the query's error.
function startRead<T>(key: QueryKey, read: () => Promise<T>, gcTime: number): void {
  void queryClient.fetchQuery<T>({ queryKey: key, queryFn: read, gcTime, retry: false }).catch(() => undefined);
}

// askedAgain runs rerun once, after the read that was in flight has landed, when a read of key
// was asked for meanwhile. The zero delay lets that read's result reach the cache first. The
// rerun does nothing when the query is gone (an unpair cleared it, keepRecent evicted it) or no
// component reads it any more: it would read for nobody, or create a query outside the kept
// lists.
function askedAgain(key: QueryKey, rerun: () => void): void {
  if (!again.delete(hashOf(key))) return;
  setTimeout(() => {
    const query = queryClient.getQueryCache().find({ queryKey: key, exact: true });
    if (query !== undefined && query.getObserversCount() > 0) rerun();
  }, 0);
}

function errorText(error: Error | null, fallback: string): string {
  if (error === null) return "";
  return error.message !== "" ? error.message : fallback;
}

// keepRecent notes key as the most recently opened of order, dropping the keys whose query has
// left the cache (an unpair clears it), and removes the queries of the oldest past limit. A query
// a view still observes is not removed: it stays in the order and goes once it is closed and
// another is opened. It returns the new order.
function keepRecent(order: QueryKey[], key: QueryKey, limit: number): QueryKey[] {
  const hash = hashOf(key);
  const cache = queryClient.getQueryCache();
  const next = order.filter((k) => hashOf(k) !== hash && cache.find({ queryKey: k }) !== undefined);
  next.push(key);
  const idle = (k: QueryKey): boolean =>
    (cache.find({ queryKey: k, exact: true })?.getObserversCount() ?? 0) === 0;
  while (next.length > limit) {
    const at = next.findIndex((k) => hashOf(k) !== hash && idle(k));
    if (at < 0) break;
    for (const oldest of next.splice(at, 1)) {
      queryClient.removeQueries({ queryKey: oldest, exact: true });
    }
  }
  return next;
}

// What the cache holds for a conversation or a subagent: the pages, and the kind of load whose
// page produced them, written together.
interface TurnsEntry {
  held: HeldTurns;
  landed: TurnsLoad;
}

const turnsKey = (session: number, agent: string | undefined): QueryKey => [
  "turns",
  session,
  agent ?? "",
];

// A conversation's pages stay in the cache until KEEP_SESSIONS evicts them or an unpair clears
// it; a subagent's leave with their last reader, so a reopened subagent view loads the newest.
const turnsRetention = (agent: string | undefined): number => (agent === undefined ? Infinity : 0);

// asked holds the kind of the load a `load` call asked for, for the read that runs next; a read
// nobody asked for (a mount, a poll) is a refresh of what is held, or the newest.
const asked = new Map<string, TurnsLoad>();

async function fetchTurns(session: number, agent: string | undefined): Promise<TurnsEntry> {
  const key = turnsKey(session, agent);
  const entry = queryClient.getQueryData<TurnsEntry>(key);
  const held = entry?.held;
  let kind = asked.get(hashOf(key)) ?? "refresh";
  asked.delete(hashOf(key));
  if (held === undefined || (kind === "refresh" && held.turns.length === 0)) kind = "newest";
  const query: Partial<TurnsParams> = {};
  if (agent !== undefined) query.agent = agent;
  if (held !== undefined) {
    query.conversation = held.conversation;
    if (kind === "earlier" && held.earlier !== null) query.before = String(held.earlier);
    if (kind === "refresh") query.after = String(held.turns[held.turns.length - 1].id);
  }
  try {
    const page = unwrap(await api.turns(session, query));
    return { held: mergeTurns(held, page, kind), landed: page.reset ? "newest" : kind };
  } finally {
    askedAgain(key, () => requestTurns(session, agent, "refresh"));
  }
}

// requestTurns asks for a load of the pages. One read is in flight at a time: a refresh asked
// meanwhile runs once when it lands, and any other kind asked meanwhile is dropped, as classic.
// An earlier page is not asked for when none is left.
function requestTurns(session: number, agent: string | undefined, kind: TurnsLoad): void {
  const key = turnsKey(session, agent);
  if (inFlight(key)) {
    if (kind === "refresh") again.add(hashOf(key));
    return;
  }
  const held = queryClient.getQueryData<TurnsEntry>(key)?.held;
  if (kind === "earlier" && (held === undefined || held.earlier === null)) return;
  asked.set(hashOf(key), kind);
  startRead(key, () => fetchTurns(session, agent), turnsRetention(agent));
}

// The conversations kept, least recently opened first, and the page scroll kept with each one's
// query: a scroll lives and dies with its query object, so evicting or clearing the pages drops
// it, and a conversation read again after a clear starts with none.
let keptSessions: QueryKey[] = [];
const scrolls = new WeakMap<object, number>();

function openConversation(session: number): void {
  keptSessions = keepRecent(keptSessions, turnsKey(session, undefined), KEEP_SESSIONS);
}

/** The page scroll kept with a conversation's pages, undefined when none is kept. */
export function keptScroll(session: number): number | undefined {
  const query = queryClient.getQueryCache().find({ queryKey: turnsKey(session, undefined) });
  return query === undefined ? undefined : scrolls.get(query);
}

/** Keeps the page scroll with a conversation's pages; nothing is kept while no pages are held. */
export function keepScroll(session: number, y: number): void {
  const query = queryClient.getQueryCache().find({ queryKey: turnsKey(session, undefined) });
  if (query !== undefined && query.state.data !== undefined) scrolls.set(query, y);
}

export interface TurnPagesOptions {
  session: number;
  /** A subagent's id: its turns are read in place of the conversation's. */
  agent: string | undefined;
  /** The session works: its newest turn is refreshed every 5 s. */
  live: boolean;
}

export interface TurnPages {
  held: HeldTurns | undefined;
  /** The kind of the load whose page produced `held`; a reset page counts as "newest". */
  landed: TurnsLoad | undefined;
  /** Why the last read failed, "" for none; the pages held stay. */
  error: string;
  /** A read is in flight. */
  loading: boolean;
  load: (kind: TurnsLoad) => void;
}

/**
 * Reads a conversation's pages, or a subagent's when `agent` is set: on mount a refresh when
 * pages are held, else the newest page; a refresh every 5 s while `live`; `load` asks for an
 * earlier page or a refresh. Components with the same session and agent share one held state.
 */
export function useTurnPages({ session, agent, live }: TurnPagesOptions): TurnPages {
  const query = useQuery<TurnsEntry>({
    queryKey: turnsKey(session, agent),
    queryFn: () => fetchTurns(session, agent),
    gcTime: turnsRetention(agent),
    retry: false,
    refetchInterval: live ? TURNS_POLL : false,
  });
  useEffect(() => {
    if (agent === undefined) openConversation(session);
  }, [session, agent]);
  const load = useCallback(
    (kind: TurnsLoad) => {
      requestTurns(session, agent, kind);
    },
    [session, agent],
  );
  return {
    held: query.data?.held,
    landed: query.data?.landed,
    error: errorText(query.error, "Could not read the conversation."),
    loading: query.isFetching,
    load,
  };
}

/**
 * The pages held for a conversation or a subagent, read without fetching them. It reads the
 * cache directly rather than through a query observer: an observer writes its own options
 * (`queryFn`, `retry`) onto the shared query on every render, and a later refetch with no options
 * of its own would then fail with "Missing queryFn". No turns key is refetched without options.
 */
export function useHeldTurns(session: number, agent: string | undefined): HeldTurns | undefined {
  return useSyncExternalStore(
    (onChange) => queryClient.getQueryCache().subscribe(onChange),
    () => queryClient.getQueryData<TurnsEntry>(turnsKey(session, agent))?.held,
  );
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/api/reads.ts src/api/reads.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts'`

Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): read turn pages through the query cache, keeping eight conversations" -- internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts
```

### Task 4.3: The diff

**Files:**
- Modify: `internal/web/ui/src/api/reads.ts` (the gen import line, then append at the end)
- Test: `internal/web/ui/src/api/reads.test.ts`

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/api/reads.test.ts` replace the line `import { fixtureTurns } from "@/fixtures";` with

```ts
import { fixturePatch, fixtureTurns } from "@/fixtures";
```

and replace the line `  useHeldTurns,` in the `./reads` import with

```ts
  useHeldTurns,
  usePatch,
```

and the line `  type HeldTurns,` with

```ts
  type HeldTurns,
  type PatchOptions,
```

Replace the line `let turnsRead: ReturnType<typeof spyOnTurns>;` with

```ts
const spyOnDiff = () => vi.spyOn(api, "diff").mockRejectedValue(new Error("unexpected diff read"));
let turnsRead: ReturnType<typeof spyOnTurns>;
let diffRead: ReturnType<typeof spyOnDiff>;
```

and the line `  turnsRead = spyOnTurns();` with

```ts
  turnsRead = spyOnTurns();
  diffRead = spyOnDiff();
```

Then append at the end of the file:

```ts
const patchOptions = (extra: Partial<PatchOptions> = {}): PatchOptions => ({
  session: 7,
  signature: "Running:1:0",
  live: false,
  ...extra,
});

function mountPatch(initial: PatchOptions) {
  return renderHook((props: PatchOptions) => usePatch(props), { initialProps: initial, wrapper });
}

describe("usePatch", () => {
  it("reads the diff on mount", async () => {
    const patch = fixturePatch();
    diffRead.mockResolvedValueOnce(ok(patch));
    const { result } = mountPatch(patchOptions());
    expect(result.current.patch).toBeUndefined();
    expect(result.current.loading).toBe(true);
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(1);
    expect(diffRead).toHaveBeenCalledWith(7);
    expect(result.current.patch).toEqual(patch);
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBe("");
  });

  it("reads again when the signature changes once a patch is held, and only then", async () => {
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    const view = mountPatch(patchOptions());
    await settle();
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    view.rerender(patchOptions({ signature: "Running:3:1" }));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(2);
    view.rerender(patchOptions({ signature: "Running:3:1" }));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(2);
  });

  it("reads once on a reopened diff with the same signature", async () => {
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    const first = mountPatch(patchOptions());
    await settle();
    first.unmount();
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    const again = mountPatch(patchOptions());
    expect(again.result.current.patch).toBeDefined();
    await settle(100);
    expect(diffRead).toHaveBeenCalledTimes(2);
  });

  it("treats a change of session as a new key, with no read beyond the observer's own", async () => {
    diffRead.mockResolvedValue(ok(fixturePatch()));
    const other = mountPatch(patchOptions({ session: 8, signature: "Idle:0:0" }));
    await settle();
    other.unmount();
    const view = mountPatch(patchOptions());
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(2);
    view.rerender(patchOptions({ session: 8, signature: "Running:3:1" }));
    await settle(100);
    expect(diffRead).toHaveBeenCalledTimes(3);
    expect(diffRead).toHaveBeenLastCalledWith(8);
  });

  it("does not read again on a signature change while the first read is still out", async () => {
    const first = deferred<Outcome<Patch>>();
    diffRead.mockReturnValueOnce(first.promise);
    const view = mountPatch(patchOptions());
    await settle();
    view.rerender(patchOptions({ signature: "Running:3:1" }));
    first.resolve(ok(fixturePatch()));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(1);
  });

  it("keeps one read in flight and runs a read asked meanwhile once, when it lands", async () => {
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    const view = mountPatch(patchOptions());
    await settle();
    const second = deferred<Outcome<Patch>>();
    diffRead.mockReturnValueOnce(second.promise);
    view.rerender(patchOptions({ signature: "Running:3:1" }));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(2);
    view.rerender(patchOptions({ signature: "Running:4:1" }));
    act(() => {
      view.result.current.reload();
    });
    expect(diffRead).toHaveBeenCalledTimes(2);
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    second.resolve(ok(fixturePatch()));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(3);
    await settle(5000);
    expect(diffRead).toHaveBeenCalledTimes(3);
  });

  it("reads every 10 s while live, and not otherwise", async () => {
    diffRead.mockResolvedValue(ok(fixturePatch()));
    const view = mountPatch(patchOptions({ live: true }));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(1);
    await settle(10_000);
    expect(diffRead).toHaveBeenCalledTimes(2);
    view.rerender(patchOptions({ live: false }));
    await settle(30_000);
    expect(diffRead).toHaveBeenCalledTimes(2);
  });

  it("says why a read failed, keeps the patch held, and reads again on reload", async () => {
    const patch = fixturePatch();
    diffRead.mockResolvedValueOnce(ok(patch));
    const { result } = mountPatch(patchOptions());
    await settle();
    diffRead.mockResolvedValueOnce(refused(409, "session is busy"));
    act(() => {
      result.current.reload();
    });
    await settle();
    expect(result.current.error).toBe("session is busy");
    expect(result.current.patch).toEqual(patch);
    diffRead.mockResolvedValueOnce(ok(patch));
    act(() => {
      result.current.reload();
    });
    await settle();
    expect(result.current.error).toBe("");
  });
});
```

Also add `Patch` to the `./gen` type import: replace `import type { AgentPage, JobLog, Outcome, Turn, Turns } from "./gen";` with

```ts
import type { AgentPage, JobLog, Outcome, Patch, Turn, Turns } from "./gen";
```

Fails without: `reads the diff on mount`: `queryFn: () => readDiff(session)` not wired (no read on mount), or `loading` not `isFetching`.
Fails without: `reads again when the signature changes once a patch is held, and only then`: the signature effect removed (a second read never happens).
Fails without: `reads once on a reopened diff with the same signature`: the `was.signature === signature` early return in the signature effect removed (the mount's effect asks a read while the mount's own is out, which runs again when it lands: a third read).
Fails without: `treats a change of session as a new key, with no read beyond the observer's own`: `seen` not following the session (the key change with a new signature asks a read while the observer's own is out, which runs again: a fourth read).
Fails without: `does not read again on a signature change while the first read is still out`: the `getQueryData(...) !== undefined` check removed (the change queues a read that runs after the first lands: two reads).
Fails without: `keeps one read in flight and runs a read asked meanwhile once, when it lands`: `again.add` in `reloadDiff` removed (no third read) or `askedAgain` removed from `readDiff`, or the in-flight check removed (the asks join the read out and nothing follows it: two reads).
Fails without: `reads every 10 s while live, and not otherwise`: `refetchInterval: live ? DIFF_POLL : false` removed or inverted.
Fails without: `says why a read failed, keeps the patch held, and reads again on reload`: `errorText(query.error, …)` replaced by `""`, or `reload` not calling `reloadDiff`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts`

Expected: the earlier tests pass; every test of `usePatch` fails with `TypeError: usePatch is not a function`.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/api/reads.ts` replace the line

```ts
import type { AgentPage, JobLog, Turn, Turns, TurnsParams } from "./gen";
```

with

```ts
import type { AgentPage, JobLog, Patch, Turn, Turns, TurnsParams } from "./gen";
```

and append at the end of the file:

```ts
// How often a live session's diff is read again, as the classic app does.
const DIFF_POLL = 10000;

const diffKey = (session: number): QueryKey => ["diff", session];

async function readDiff(session: number): Promise<Patch> {
  try {
    return unwrap(await api.diff(session));
  } finally {
    askedAgain(diffKey(session), () => reloadDiff(session));
  }
}

// reloadDiff reads the diff: one read is in flight at a time, and a read asked for meanwhile
// runs once when it lands.
function reloadDiff(session: number): void {
  const key = diffKey(session);
  if (inFlight(key)) {
    again.add(hashOf(key));
    return;
  }
  startRead(key, () => readDiff(session), DEFAULT_GC);
}

export interface PatchOptions {
  session: number;
  /** What the caller reads the diff's change from (the session's status and line counts). */
  signature: string;
  /** The session works: the diff is read every 10 s. */
  live: boolean;
}

export interface PatchView {
  patch: Patch | undefined;
  /** Why the last read failed, "" for none; the patch held stays. */
  error: string;
  /** A read is in flight. */
  loading: boolean;
  /** Reads the diff now. */
  reload: () => void;
}

/**
 * Reads a session's diff on mount, again when `signature` changes once a patch is held, and every
 * 10 s while `live`.
 */
export function usePatch({ session, signature, live }: PatchOptions): PatchView {
  const query = useQuery<Patch>({
    queryKey: diffKey(session),
    queryFn: () => readDiff(session),
    retry: false,
    refetchInterval: live ? DIFF_POLL : false,
  });
  // seen follows the key: a change of session starts from the new session's own signature, and a
  // mount is no change, so the observer's own read is the only one either asks for.
  const seen = useRef({ session, signature });
  useEffect(() => {
    const was = seen.current;
    seen.current = { session, signature };
    if (was.session !== session || was.signature === signature) return;
    if (queryClient.getQueryData(diffKey(session)) !== undefined) reloadDiff(session);
  }, [session, signature]);
  const reload = useCallback(() => {
    reloadDiff(session);
  }, [session]);
  return {
    patch: query.data,
    error: errorText(query.error, "Could not read the diff."),
    loading: query.isFetching,
    reload,
  };
}
```

and, near the top of the file's helpers, make the shared default explicit and import `useRef`: replace the line `import { useCallback, useEffect, useSyncExternalStore } from "react";` with

```ts
import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";
```

and insert, directly above the line `// How often a live conversation is refreshed, as the classic app does.`:

```ts
// The query cache's own default for a query nobody reads for a while (5 minutes), which a diff
// keeps: only turn pages and job logs are kept longer.
const DEFAULT_GC = 5 * 60 * 1000;

```

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/api/reads.ts src/api/reads.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts'`

Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): read a session's diff through the query cache" -- internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts
```

### Task 4.4: Job logs, kept for five jobs

**Files:**
- Modify: `internal/web/ui/src/api/reads.ts` (the imports, then append at the end)
- Test: `internal/web/ui/src/api/reads.test.ts`

- [ ] **Step 1: Write the failing test**

In `internal/web/ui/src/api/reads.test.ts` replace the line `import { fixturePatch, fixtureTurns } from "@/fixtures";` with

```ts
import { fixtureJobLog, fixturePatch, fixtureTurns } from "@/fixtures";
```

replace the line `  usePatch,` in the `./reads` import with

```ts
  useJobLog,
  usePatch,
```

the line `  KEEP_SESSIONS,` with

```ts
  KEEP_JOBS,
  KEEP_SESSIONS,
```

and the line `  type HeldTurns,` with

```ts
  type HeldTurns,
  type JobLogOptions,
```

Replace the line `let diffRead: ReturnType<typeof spyOnDiff>;` with

```ts
const spyOnLog = () =>
  vi.spyOn(api, "jobLog").mockRejectedValue(new Error("unexpected job log read"));
let diffRead: ReturnType<typeof spyOnDiff>;
let logRead: ReturnType<typeof spyOnLog>;
```

and the line `  diffRead = spyOnDiff();` with

```ts
  diffRead = spyOnDiff();
  logRead = spyOnLog();
```

Then append at the end of the file:

```ts
const logOptions = (extra: Partial<JobLogOptions> = {}): JobLogOptions => ({
  session: 7,
  job: 214,
  live: false,
  ...extra,
});

function mountLog(initial: JobLogOptions) {
  return renderHook((props: JobLogOptions) => useJobLog(props), {
    initialProps: initial,
    wrapper,
  });
}

describe("useJobLog", () => {
  it("reads the tail on mount and holds the fixture's page", async () => {
    const tail = fixtureJobLog();
    logRead.mockResolvedValueOnce(ok(tail));
    const { result } = mountLog(logOptions());
    expect(result.current.log).toBeUndefined();
    expect(result.current.loading).toBe(true);
    await settle();
    expect(logRead).toHaveBeenCalledTimes(1);
    expect(logRead).toHaveBeenCalledWith(7, 214, undefined);
    expect(result.current.log).toEqual({ text: tail.text, end: tail.end, cut: tail.cut });
    expect(result.current.gone).toBe("");
    expect(result.current.error).toBe("");
    expect(result.current.loading).toBe(false);
  });

  it("draws the kept log first when a job is opened again, and reads after its end", async () => {
    const tail = fixtureJobLog();
    const more = logPage("and more\n", tail.end + 9, { start: tail.end });
    logRead.mockResolvedValueOnce(ok(tail));
    const view = mountLog(logOptions());
    await settle();
    view.unmount();
    await settle(10 * 60_000);
    logRead.mockResolvedValueOnce(ok(more));
    const again = mountLog(logOptions());
    expect(again.result.current.log?.text).toBe(tail.text);
    await settle();
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: String(tail.end) });
    expect(again.result.current.log?.text).toBe(tail.text + "and more\n");
    expect(again.result.current.log?.end).toBe(more.end);
  });

  it("reads after the end every 2 s while live and appends what it gets", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const { result } = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce(ok(logPage("b\n", 4)));
    await settle(2000);
    expect(logRead).toHaveBeenCalledTimes(2);
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: "2" });
    expect(result.current.log).toEqual({ text: "a\nb\n", end: 4, cut: false });
  });

  it("stops polling when the job ends, after one more read", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const view = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce(ok(logPage("last\n", 7)));
    view.rerender(logOptions({ live: false }));
    await settle();
    expect(logRead).toHaveBeenCalledTimes(2);
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: "2" });
    expect(view.result.current.log?.text).toBe("a\nlast\n");
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(2);
  });

  it("does not read once more when it was never live", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const view = mountLog(logOptions({ live: false }));
    await settle();
    view.rerender(logOptions({ live: false }));
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(1);
  });

  it("says a 409 means the job left the queue, keeps the log, and stops reading", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const { result } = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce(refused(409, "job 214 has left the queue"));
    await settle(2000);
    expect(result.current.gone).toBe("job 214 has left the queue");
    expect(result.current.error).toBe("");
    expect(result.current.log?.text).toBe("a\n");
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(2);
  });

  it("waits for the next tick after a read that timed out", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const { result } = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce({ kind: "timeout", message: "timed out" });
    await settle(2000);
    expect(result.current.error).toBe("timed out");
    expect(result.current.gone).toBe("");
    logRead.mockResolvedValueOnce(ok(logPage("b\n", 4)));
    await settle(2000);
    expect(logRead).toHaveBeenCalledTimes(3);
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: "2" });
    expect(result.current.error).toBe("");
    expect(result.current.log?.text).toBe("a\nb\n");
  });

  it("reads once more at the job's end, once, when a read is still out then", async () => {
    const first = deferred<Outcome<JobLog>>();
    logRead.mockReturnValueOnce(first.promise);
    const view = mountLog(logOptions({ live: true }));
    await settle();
    view.rerender(logOptions({ live: false }));
    await settle();
    expect(logRead).toHaveBeenCalledTimes(1);
    logRead.mockResolvedValueOnce(ok(logPage("last\n", 7)));
    first.resolve(ok(logPage("a\n", 2)));
    await settle();
    expect(logRead).toHaveBeenCalledTimes(2);
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: "2" });
    expect(view.result.current.log?.text).toBe("a\nlast\n");
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(2);
  });

  it("treats a change of job as a new key, with no read beyond the observer's own", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const view = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce(ok(logPage("b\n", 3)));
    view.rerender(logOptions({ job: 215, live: false }));
    await settle(100);
    expect(logRead).toHaveBeenCalledTimes(2);
    expect(logRead).toHaveBeenLastCalledWith(7, 215, undefined);
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(2);
  });

  it("evicts the least recently opened job's log for a sixth", async () => {
    const openJob = async (job: number): Promise<void> => {
      logRead.mockResolvedValueOnce(ok(logPage("j", 1)));
      const view = mountLog(logOptions({ session: 5, job }));
      await settle();
      view.unmount();
    };
    for (let job = 1; job <= KEEP_JOBS + 1; job++) await openJob(job);
    await openJob(2);
    expect(logRead).toHaveBeenLastCalledWith(5, 2, { after: "1" });
    await openJob(1);
    expect(logRead).toHaveBeenLastCalledWith(5, 1, undefined);
  });

  it("drops the logs with the cache when the pairing is revoked", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const view = mountLog(logOptions());
    await settle();
    view.unmount();
    queryClient.clear();
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const again = mountLog(logOptions());
    expect(again.result.current.log).toBeUndefined();
    await settle();
    expect(logRead).toHaveBeenLastCalledWith(7, 214, undefined);
  });
});
```

Fails without: `reads the tail on mount and holds the fixture's page`: `readLog` passing `{ after: "0" }` for no held log (the call has an argument), or `appendLog` not applied.
Fails without: `draws the kept log first when a job is opened again, and reads after its end`: `gcTime: Infinity` removed from `useJobLog` (the log is gone after the 10 minutes and the call has no `after`), `after` read from anywhere but the held `end`, or the `after` page ignored by `appendLog` (the text stays the tail's and `end` stays the tail's).
Fails without: `reads after the end every 2 s while live and appends what it gets`: `refetchInterval` removed (one read only) or the read ignoring the held log (the text is only `"b\n"`).
Fails without: `stops polling when the job ends, after one more read`: the `ended` effect removed (one read only), or the poll left running when `live` is false (more than two reads).
Fails without: `does not read once more when it was never live`: the `before.live && !live` terms reduced to `!live` (a second read happens).
Fails without: `says a 409 means the job left the queue, keeps the log, and stops reading`: `goneText` removed from `refetchInterval` (the polling goes on and reads more than twice), or `gone` read from nowhere.
Fails without: `waits for the next tick after a read that timed out`: `refetchInterval` also stopping on any error (the third read never happens), or a non-409 error read as `gone`.
Fails without: `reads once more at the job's end, once, when a read is still out then`: `again.add` removed from `requestLog` (the read out is the only one), or `askedAgain` removed from `readLog` (the same), or the in-flight check removed (the end's read joins the one out, nothing follows it, and one read is made).
Fails without: `treats a change of job as a new key, with no read beyond the observer's own`: `was` not following the key (the change from a live job to an ended one asks a read while the observer's own is out, which runs again: a third read).
Fails without: `evicts the least recently opened job's log for a sixth`: `keepRecent`'s `removeQueries` removed or `openJob` not called on mount (job 1 keeps its log and is read `after`).
Fails without: `drops the logs with the cache when the pairing is revoked`: the log kept outside the query cache (the first render after the clear holds the old log).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts`

Expected: the earlier tests pass; every test of `useJobLog` fails with `TypeError: useJobLog is not a function`.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/api/reads.ts` replace the line

```ts
import { queryClient, unwrap } from "./queries";
```

with

```ts
import { OutcomeError, queryClient, unwrap } from "./queries";
```

and append at the end of the file:

```ts
// How often a live job's log is read again, as the classic app does.
const LOG_POLL = 2000;

const logKey = (session: number, job: number): QueryKey => ["job-log", session, job];

// The jobs kept, least recently opened first.
let keptJobs: QueryKey[] = [];

function openJob(session: number, job: number): void {
  keptJobs = keepRecent(keptJobs, logKey(session, job), KEEP_JOBS);
}

// goneText is the text of a 409 (the job has left the queue), "" for any other error.
function goneText(error: Error | null | undefined): string {
  if (!(error instanceof OutcomeError)) return "";
  if (error.outcome.kind !== "refused" || error.outcome.status !== 409) return "";
  return error.message !== "" ? error.message : "This job is no longer in the queue.";
}

// readLog reads what the log holds past the end held, or its tail when none is held, and appends
// it. A held log is always read `after`: a tail answer never says it is a reset, so a tail
// appended to a held log would repeat it.
async function readLog(session: number, job: number): Promise<HeldLog> {
  const key = logKey(session, job);
  try {
    const held = queryClient.getQueryData<HeldLog>(key);
    const page = unwrap(
      await api.jobLog(session, job, held === undefined ? undefined : { after: String(held.end) }),
    );
    return appendLog(held, page);
  } finally {
    askedAgain(key, () => requestLog(session, job));
  }
}

// requestLog reads the log now: one read is in flight at a time, and a read asked for meanwhile
// runs once when it lands.
function requestLog(session: number, job: number): void {
  const key = logKey(session, job);
  if (inFlight(key)) {
    again.add(hashOf(key));
    return;
  }
  startRead(key, () => readLog(session, job), Infinity);
}

export interface JobLogOptions {
  session: number;
  job: number;
  /** The job has yet to end: its log is read every 2 s, and once more when this turns false. */
  live: boolean;
}

export interface JobLogView {
  log: HeldLog | undefined;
  /** The text of a 409: the job has left the queue and nothing more is read; "" for none. */
  gone: string;
  /** Why the last read failed, other than a 409, "" for none; the log held stays. */
  error: string;
  /** A read is in flight. */
  loading: boolean;
}

/**
 * Reads a job's log: on mount the kept log drawn first and a read after its end (or the tail when
 * none is kept), then after the end every 2 s while `live` and not gone, and once more when
 * `live` turns false. A 409 sets `gone` and stops; a timeout or a network failure waits for the
 * next tick. A job's log stays in the cache until KEEP_JOBS evicts it or an unpair clears it.
 */
export function useJobLog({ session, job, live }: JobLogOptions): JobLogView {
  const query = useQuery<HeldLog>({
    queryKey: logKey(session, job),
    queryFn: () => readLog(session, job),
    gcTime: Infinity,
    retry: false,
    refetchInterval: (q) => (live && goneText(q.state.error) === "" ? LOG_POLL : false),
  });
  useEffect(() => {
    openJob(session, job);
  }, [session, job]);
  // was follows the key: a change of job is no end of the old one, so only the observer's own
  // read is made for the new key.
  const was = useRef({ session, job, live });
  useEffect(() => {
    const before = was.current;
    was.current = { session, job, live };
    const ended = before.session === session && before.job === job && before.live && !live;
    if (ended && goneText(queryClient.getQueryState(logKey(session, job))?.error) === "") {
      requestLog(session, job);
    }
  }, [live, session, job]);
  const gone = goneText(query.error);
  return {
    log: query.data,
    gone,
    error: gone === "" ? errorText(query.error, "Could not read the job's log.") : "",
    loading: query.isFetching,
  };
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/api/reads.ts src/api/reads.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/api/reads.test.ts'`

Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): read job logs through the query cache, keeping five jobs" -- internal/web/ui/src/api/reads.ts internal/web/ui/src/api/reads.test.ts
```

### Task 4.5: The story's gates and the bundle

**Files:**
- Test: `internal/web/dist/` (checked, expected unchanged)

- [ ] **Step 1: Build the bundle and see it has not changed**

Run: `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build && git status --short internal/web/dist'`

Expected: the build succeeds and `git status --short internal/web/dist` prints nothing: no module imports `reads.ts` yet, so the build leaves the committed bundle as it is. If it prints a changed file, commit it with the next step's command and the paths it lists.

- [ ] **Step 2: Run the story's checks**

Run: `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`

Expected: `check` (typecheck, lint, prettier, the whole Vitest suite and `node --check public/sw.js`) passes, `dist:check` reports a fresh bundle, and `go test` prints `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 3: Commit only if the bundle changed**

When Step 1 listed a changed file under `internal/web/dist/`:

```bash
git commit -m "build(web): rebuild the bundle" -- internal/web/dist
```

Otherwise there is nothing to commit and the story ends with Task 4.4's commit.

## Story 5: Claude's text from its tree, and the turn renderer

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/conversation/Markdown.tsx`, `internal/web/ui/src/conversation/Markdown.test.tsx`, `internal/web/ui/src/conversation/TurnList.tsx`, `internal/web/ui/src/conversation/TurnList.test.tsx`, `internal/web/ui/src/app/routes.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `Markdown`, `TurnList`, `turnLabel`, `clock`, `AGENT_ID`; uses part 1's `Collapsible`, `hrefFor` and `ago`, and Story 1's `fixtureTurns`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** brief (rendering a tree the daemon builds and bounds; the no-markup rule is pinned by a named test)

This story draws Claude's text from the `MdNode` tree the daemon builds, as elements and text nodes and never as markup from a string (spec §33), and builds on it the turn list of the classic conversation (`internal/web/static/js/turns.mjs`): each turn's label by `from`, its prompt folded past 12 lines, its partial and cut forms, and the Claude side's items by kind (text with its "continues in Claude" link, thinking and step folds as `Collapsible`s open on the live newest turn, steps with their links to the subagent view, compacted, note, and an unknown kind in neutral styling, spec Part 2 settlement 9). It also exports `AGENT_ID` from `app/routes.tsx` for the turn list and, later, the info panel. Nothing in the story is drawn by a route yet: `Conversation` (Story 8) and the subagent view (Story 11) mount `TurnList`. None of its code holds a landmine marker.

Form: brief. A task gives the behaviour, the contract names it defines and uses, and each test by name with what it asserts and what turns it red; the developer writes the components. House rules for the code: components return `ReactElement` (React 19's types have no global `JSX`); take every colour and font from the tokens' Tailwind names (`text-muted-foreground`, `text-faint`, `text-error`, `text-turn`, `text-attention`, `font-mono`, `border-border`, `bg-card`), never a literal; write no `style` attribute and no runtime `<style>`; a touch target (the "more lines" button, a step link, a fold's trigger) is at least 44px high (`min-h-11`); a lookup from a value to a Tailwind class is a literal object, so the class names appear in the source for Tailwind to find; the repo's ESLint rejects `_`-prefixed unused parameters; a mock is typed `vi.fn<(a: A, b: B) => R>(…)`.

A `Collapsible`'s content is not mounted while it is closed (Base UI's default), so a test that reads a fold's content opens it first (`userEvent` click on its trigger, then `findBy…`), and a test that asserts a fold is closed uses `queryBy…` with `waitFor`, as `components.test.tsx`'s Collapsible test does. Tests that read relative times fake only `Date` (`vi.useFakeTimers({ toFake: ["Date"] })` and `vi.setSystemTime(NOW)` with `NOW = Date.parse("2026-11-18T12:00:00Z")`, undone in `afterEach`) and call `fixtureTurns()` after setting it.

### Task 5.1: export `AGENT_ID`

**Files:**
- Modify: `internal/web/ui/src/app/routes.tsx:18-30`
- Create: `internal/web/ui/src/conversation/TurnList.test.tsx`

The constant is today private. Quoted as it stands:

```ts
// AGENT_ID is what an agent id may hold for the app to link to its screen
// (the classic app's session.mjs says the same).
const AGENT_ID = /^[A-Za-z0-9_-]+$/;
```

It becomes `export const AGENT_ID` with the regular expression unchanged; `routeOf` keeps using it. This task creates `TurnList.test.tsx` with this one test, which Task 5.3 and Task 5.4 extend.

- [ ] **Step 1: Write the failing test**

`AGENT_ID: admits the ids the daemon writes and refuses the rest` imports `AGENT_ID` from `@/app/routes` and asserts it matches `a1f00dcafe0001`, `agent_1` and `a-b`, and does not match `""`, `a b`, `a/b`, `../x` or `a?b=1`.

Fails without: AGENT_ID: admits the ids the daemon writes and refuses the rest: the `export` removed (the import is then `undefined` and `.test` throws), or the pattern widened to `/^.+$/`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/conversation/TurnList.test.tsx`

Expected: the test fails with `AGENT_ID` not exported by `@/app/routes` (a typecheck error under `pnpm run typecheck`, `Cannot read properties of undefined (reading 'test')` under Vitest).

- [ ] **Step 3: Implement**

Add `export` to the constant's declaration in `routes.tsx`, changing nothing else.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui exec vitest run src/conversation/TurnList.test.tsx src/app/routes.test.ts`

Expected: both files pass.

- [ ] **Step 5: Commit**

```
git add -N internal/web/ui/src/conversation/TurnList.test.tsx
git commit -m "refactor(web): export AGENT_ID for the turn list and the info panel" -- internal/web/ui/src/app/routes.tsx internal/web/ui/src/conversation/TurnList.test.tsx
```

### Task 5.2: `Markdown`

**Files:**
- Create: `internal/web/ui/src/conversation/Markdown.tsx`
- Create: `internal/web/ui/src/conversation/Markdown.test.tsx`

Defines (Story 5's `TurnList`, and Stories 7 and later read it): `function Markdown(props: { blocks: (MdNode | null)[] }): ReactElement`, in `conversation/Markdown.tsx`, over `MdNode` from `@/api/gen` (`t`, `v`, `level`, `start`, `lang`, `href`, `align`, `c`). Reference: the classic `internal/web/static/js/markdown.mjs`, whose switch it ports, and the kinds `internal/web/markdown.go:25-45` lists.

Behaviour: the component returns one wrapper `div` holding each block drawn recursively; a node's children are its `c`, each drawn the same way; keys are the child's index. Per `t`:

- `text` draws its `v` as a text node, `br` a `<br>`, `p` a `<p>`, `ul` a `<ul>`, `li` an `<li>`, `quote` a `<blockquote>`, `hr` an `<hr>`, `em`, `strong` and `del` their elements, `code_inline` a `<code>` holding `v`.
- `h` draws an `<h3>` for `level` 1 or 2 and an `<h4>` for any other (the page already holds the `h1` and `h2` of its chrome), so a missing level draws `<h4>`.
- `ol` draws an `<ol>` with its `start` attribute only when the node has one (`start` is a number or absent).
- `code` draws `<pre><code>` holding `v`, in the mono font, scrolling sideways rather than wrapping.
- `a` draws `<a href target="_blank" rel="noopener noreferrer">` over its children when `href` is a non-empty string, and a `<span>` of its children otherwise. The link check (an absolute http or https URL) is `safeHref` in Go; this component adds none.
- `table` draws a wrapper `div` that scrolls sideways around a `<table><tbody>`, its `c` children (`tr`) drawn as `<tr>`, whose `th` and `td` children are `<th>` and `<td>`; each cell takes the text alignment of its column, `align[k]` of the table (`"left"`, `"right"`, `"center"`; anything else, or no entry, none), as a class from a literal lookup (`text-left`, `text-right`, `text-center`), never a `style` attribute.
- any other `t` draws a `<span>` of its children, and a `null` in `blocks` or in any `c` draws nothing.

The component never sets an element's inner HTML and never builds a string of markup.

- [ ] **Step 1: Write the failing tests**

`Markdown.test.tsx` renders with Testing Library. It builds small trees inline (a typed `MdNode` literal), and one test reads the fixture's: `fixtureTurns().turns` flattened to items, the `text` item whose first block's `t` is `h` (Story 1 puts it in turn 1: a level-1 heading `Findings`, a paragraph with `em`, `strong` and a link, a `ul` of two `li`, an `ol` with `start` 3 of two `li`, a `quote`, a `go` `code` of `p.drawCursor(frame)`, an `hr`, and a table whose header row holds two `th` cells and whose body row two `td` cells, aligned left then right).

- `Markdown: the fixture's tree draws each node as its element` renders that item's blocks and asserts: an `h3` named `Findings`; an `em`, a `strong` and a link; a `ul` with 2 `li`; an `ol` whose `start` attribute is `3` with 2 `li`; a `blockquote`; a `pre` holding a `code` with the text `p.drawCursor(frame)`; an `hr`; a `table` with 2 `tr`, 2 `th` and 2 `td`.
- `Markdown: a heading of level 3 or more is an h4, and levels 1 and 2 are h3` renders `h` nodes of levels 1, 2, 3 and 6 and asserts two `h3` and two `h4`.
- `Markdown: an ordered list without a start has no start attribute` renders an `ol` with no `start` and asserts the `ol` has no `start` attribute.
- `Markdown: the inline kinds draw their elements` renders a `p` of `text`, `br`, `del`, `code_inline` and asserts a `br`, a `del` holding its text and a `code` holding the `v`.
- `Markdown: a table cell takes its column's alignment as a class and no style attribute` renders a table with `align` `["left","right","center"]` and asserts the first, second and third cells carry `text-left`, `text-right` and `text-center`, that a cell past the `align` list carries none of the three, and that no element in the container has a `style` attribute.
- `Markdown: a link opens in a new tab and carries noopener noreferrer` renders an `a` with an `href` and asserts its `href`, `target` `_blank` and `rel` `noopener noreferrer`; and that an `a` with no `href` is a `span` with no `href` attribute, still holding its text.
- `Markdown: an unknown node is a span of its children` renders `{ t: "mystery", c: [text "inside"] }` and asserts a `span` holding `inside`, and no element named `mystery`.
- `Markdown: a null node draws nothing and its siblings still draw` renders `[null, p("kept"), null]` and a `p` whose `c` is `[null, text "x"]`, asserting `kept` and `x` are drawn and the wrapper holds one `p` for the first tree.
- `Markdown: text holding markup renders as text` renders a `p` whose text node is `<script>alert(1)</script><img src=x onerror=alert(1)>`, a `code` node holding `<b>bold</b>`, and an `a` whose child text is `<i>x</i>`, and asserts the literal strings are in the container's text, and that `container.querySelector("script, img, b, i")` is `null`.
- `Markdown: nothing carries a style attribute` renders the fixture's tree and asserts `container.querySelectorAll("[style]")` is empty.

Fails without: Markdown: the fixture's tree draws each node as its element: the `table`, `quote` or `ol` case removed from the switch, or `start` not passed. Markdown: a heading of level 3 or more is an h4, and levels 1 and 2 are h3: every `h` drawn as `h3`. Markdown: an ordered list without a start has no start attribute: `start` always passed (a `start={undefined}` is fine, a default of 1 is not). Markdown: the inline kinds draw their elements: the `del` or `br` case removed. Markdown: a table cell takes its column's alignment as a class and no style attribute: the alignment written as `style={{ textAlign }}`, or applied to the wrong column. Markdown: a link opens in a new tab and carries noopener noreferrer: `rel` left as `noopener` alone, or the empty-`href` link drawn as an `<a>`. Markdown: an unknown node is a span of its children: the default case drawing nothing, or an element named by `t`. Markdown: a null node draws nothing and its siblings still draw: the null check removed (a `Cannot read properties of null`). Markdown: text holding markup renders as text: the text drawn through an inner-HTML prop. Markdown: nothing carries a style attribute: any `style` prop in the component.

- [ ] **Step 2: Run them to see them fail**

Run: `pnpm -C internal/web/ui exec vitest run src/conversation/Markdown.test.tsx`

Expected: the file fails to resolve `./Markdown`.

- [ ] **Step 3: Implement**

Write `conversation/Markdown.tsx` as the behaviour above describes, with a doc comment on `Markdown` that says Claude's text comes only from its tree and why (a string put into the page renders whatever markup Claude's text holds).

- [ ] **Step 4: Run them to see them pass, then the checks and the bundle**

Run: `pnpm -C internal/web/ui exec prettier --write src/conversation/Markdown.tsx src/conversation/Markdown.test.tsx && pnpm -C internal/web/ui exec vitest run src/conversation/Markdown.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`

Expected: every test passes; typecheck and lint print no error.

Then run `pnpm -C internal/web/ui run build` and `git status --short internal/web/dist`. Nothing imports `Markdown` yet, so the build is expected to leave `internal/web/dist/` unchanged and the status to print nothing; if it lists files, they are part of this commit.

- [ ] **Step 5: Commit**

```
git add -N internal/web/ui/src/conversation/Markdown.tsx internal/web/ui/src/conversation/Markdown.test.tsx
git commit -m "feat(web): draw Claude's text from its tree as elements" -- internal/web/ui/src/conversation/Markdown.tsx internal/web/ui/src/conversation/Markdown.test.tsx internal/web/dist
```

### Task 5.3: `turnLabel`, `clock` and the turn list's frame

**Files:**
- Create: `internal/web/ui/src/conversation/TurnList.tsx`
- Modify: `internal/web/ui/src/conversation/TurnList.test.tsx`

Defines, in `conversation/TurnList.tsx`: `function TurnList(props: { turns: Turn[], sessionId: number, live: boolean, remote: string }): ReactElement`, `function turnLabel(turn: Turn): string` and `function clock(iso: string): string`. Uses `Turn` and `Item` from `@/api/gen`, `ago` from `@/panels/fleet/SessionRow` (part 1's, kept exported by Story 6), `hrefFor` and `AGENT_ID` from `@/app/routes`, `Collapsible`, `CollapsibleTrigger` and `CollapsibleContent` from `@/components/ui/collapsible`, and `Markdown` (Task 5.2). Reference: `turns.mjs:55-201` (`turnLabel`, `clock`, `Prompt`, `Turn`, `TurnList`).

Behaviour:

- `turnLabel(turn)`: `you` is `YOU`; `plugin` is the turn's `name` uppercased, `PLUGIN` without a name; `peer` is `FROM <NAME>` with a name and `FROM ANOTHER SESSION` without; `task` is `BACKGROUND TASK`; any other `from` (`brief` included) is the word uppercased (an empty one gives `""`).
- `clock(iso)` is the time as 24-hour `hh:mm` in the device's zone (the local hours and minutes, each two digits), and `""` for an empty or unparseable `iso`.
- `TurnList` draws a `<div>` of one `<article>` per turn, keyed by `turn.id`, oldest first. `live` makes only the last turn live. A turn with `partial` draws the small muted line `Its start is earlier.` in place of its prompt box; any other draws the prompt box. A turn whose `items` is null or empty draws the prompt box alone.
- The prompt box has a legend of `turnLabel` plus ` · <clock(at)>` when the clock is not empty, and the text in a block that keeps its newlines and breaks long words (`whitespace-pre-wrap`, `break-words`); a `command` prompt is in the mono font (`font-mono`). A text of more than 12 lines (split on `"\n"`) shows its first 12 and a button `▸ <n> more lines` (n the lines hidden, the button 44px high) that shows all of it when pressed and then disappears; a `cut` text that is not folded ends with `…`.
- The Claude side (when the turn has items) is a block whose label row reads `CLAUDE · <clock of the last item's at>`, or `CLAUDE · ◐ WORKING` when the turn is live; the row is left out when every item is `compacted`. Below it each item is drawn by `Item`, keyed by its index, which this task gives one temporary form: every item draws as the neutral unknown-kind line (Task 5.4 replaces it with the kinds). A turn's `live` and the session id are passed to `Item`.

- [ ] **Step 1: Write the failing tests**

In `TurnList.test.tsx` (the AGENT_ID test stays), with a helper that builds a `Turn` (`id`, `at`, `from`, `name`, `text`, `cut`, `command`, `partial`, `items`) from overrides:

- `turnLabel: each from has its own label` asserts the eight forms above (`you`, `plugin` with and without a name, `peer` with and without, `task`, `brief`, and an unknown `mystery` through a cast, uppercased).
- `clock: the device's 24-hour hh:mm, empty for none` asserts `clock("2026-11-18T09:05:00Z")` equals the local `getHours()` and `getMinutes()` of that date, each padded to two digits and joined by `:` (computed in the test from a `Date`, not from `toLocaleTimeString`; the Vitest run pins `test.env.TZ` to `Africa/Johannesburg` in `internal/web/ui/vite.config.ts`, so the expected value differs from a UTC one under CI's UTC too), and that `clock("")` and `clock("not a time")` are `""`.
- `TurnList: a prompt's box has its label, its time and its text` renders a `you` turn and asserts the legend `YOU · <clock>` and the text.
- `TurnList: a command prompt is in the mono font` renders a turn with `command` true beside one without and asserts the first's text block has `font-mono` and the second's does not.
- `TurnList: a prompt of more than 12 lines folds and opens` renders a 20-line prompt and asserts only the first 12 lines are in the text, the button `▸ 8 more lines`, and after a click all 20 lines and no button; a 12-line prompt has no button.
- `TurnList: a cut prompt ends with an ellipsis once unfolded` renders a short `cut` prompt (ends with `…`) and a 20-line `cut` prompt (no `…` while folded, `…` after the click).
- `TurnList: a partial turn says its start is earlier and has no prompt box` renders a `partial` turn with text `hidden prompt` and asserts the line `Its start is earlier.` and that `hidden prompt` and `YOU` are not drawn.
- `TurnList: the newest turn is labelled working only while live` renders two turns with an item each, `live` true, and asserts one `CLAUDE · ◐ WORKING` (the second turn's) and one `CLAUDE · <clock>` (the first's); with `live` false none says `WORKING`.
- `TurnList: a turn of compaction rules alone has no Claude label` renders a turn whose only item is `compacted` and asserts no `CLAUDE` text.
- `TurnList: a turn with no items draws its prompt alone` renders a turn with `items: null` and asserts the prompt and no `CLAUDE` text.
- `TurnList: prompt text holding markup renders as text` renders a prompt `<b>hi</b><script>x</script>` and asserts the literal text is drawn and `container.querySelector("b, script")` is `null`.

Fails without: turnLabel: each from has its own label: the `peer` no-name branch or the default uppercase removed. clock: the device's 24-hour hh:mm, empty for none: the result built with `toISOString` (UTC) or the empty check removed. TurnList: a prompt's box has its label, its time and its text: the clock left out of the legend. TurnList: a command prompt is in the mono font: `font-mono` applied to every prompt or none. TurnList: a prompt of more than 12 lines folds and opens: the fold threshold changed to 13, or the button not removed once open. TurnList: a cut prompt ends with an ellipsis once unfolded: the `…` drawn while folded, or never. TurnList: a partial turn says its start is earlier and has no prompt box: `partial` ignored. TurnList: the newest turn is labelled working only while live: `live` applied to every turn, or never. TurnList: a turn of compaction rules alone has no Claude label: the `said` check removed. TurnList: a turn with no items draws its prompt alone: a read of `items.length` on null. TurnList: prompt text holding markup renders as text: the text drawn through an inner-HTML prop.

- [ ] **Step 2: Run them to see them fail**

Run: `pnpm -C internal/web/ui exec vitest run src/conversation/TurnList.test.tsx`

Expected: the file fails to resolve `./TurnList`.

- [ ] **Step 3: Implement**

Write `conversation/TurnList.tsx` as the behaviour above describes: the exports, the prompt box and the turn frame, `Item` as the temporary neutral form (a doc comment says Task 5.4 replaces it).

- [ ] **Step 4: Run them to see them pass, then the checks**

Run: `pnpm -C internal/web/ui exec prettier --write src/conversation/TurnList.tsx src/conversation/TurnList.test.tsx && pnpm -C internal/web/ui exec vitest run src/conversation/TurnList.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`

Expected: every test passes; typecheck and lint print no error. Then `pnpm -C internal/web/ui run build` and `git status --short internal/web/dist` (unchanged while nothing imports `TurnList`; any file it lists is part of this commit).

- [ ] **Step 5: Commit**

```
git add -N internal/web/ui/src/conversation/TurnList.tsx
git commit -m "feat(web): the turn list's frame, prompts and labels" -- internal/web/ui/src/conversation/TurnList.tsx internal/web/ui/src/conversation/TurnList.test.tsx internal/web/dist
```

### Task 5.4: the items by kind

**Files:**
- Modify: `internal/web/ui/src/conversation/TurnList.tsx`
- Modify: `internal/web/ui/src/conversation/TurnList.test.tsx`

Replaces Task 5.3's temporary `Item` with the kinds. Reference: `turns.mjs:92-165` (`Step`, `Steps`, `Thinking`, `Item`). Behaviour, per `item.kind`:

- `text` draws `Markdown` over `blocks ?? []`; with `truncated` it adds the muted small line `… continues in ` followed by `Claude` as `<a href={remote} target="_blank" rel="noopener noreferrer">` when `remote` is not empty and as plain `Claude` when it is.
- `thinking` draws a `Collapsible` whose trigger reads `▸ thinking: <first>`, `first` the first line of `text` that is not blank after trimming (`""` for none), and whose content is the whole `text` as plain text, ending `…` when `truncated`.
- `steps` draws a `Collapsible` whose trigger reads `<total> step` or `<total> steps` (`1 step` only for 1; `total` is 0 when absent) and, when `failed` is above 0, ` · <failed> failed` in `text-error`. Its content, in order: `<hidden> earlier steps not shown` (muted, small) when `total` exceeds the steps held, `hidden` being the difference; then one row per step (a four-column grid: glyph, tool, summary, end). A step row: it is *linked* when its `agent` is non-empty and matches `AGENT_ID`; it is *running* when the turn is live and its `state` is `pending`. The glyph is `◐` (in `text-turn`) when running or when linked and not `finished`; otherwise `×` in `text-error` for `failed`, `×` muted for `interrupted`, and empty for the rest. The end text is `running <ago(at)>` when running, `no result` for a `pending` step that is not running, and empty otherwise; a linked step appends ` ›` to it (or is `›` alone). A linked row is an `<a href={hrefFor("agent", { id: String(sessionId), agent })}>` at least 44px high; any other row is a plain row. Tool and summary are text nodes.
- `compacted` draws a centred muted rule line `COMPACTED`, followed by ` <clock(at)>` when the clock is not empty.
- `note` draws the item's `text` alone in `text-error` when `error` is true, and `× <text>` muted otherwise.
- any other kind draws a neutral line (muted): the kind's name in the mono font and, when `text` is non-empty, the text after it as plain text. It draws no `Markdown` and no `Collapsible`.

Fold state: a fold (`steps` and `thinking`) is open when the reader opened it, closed when the reader closed it, and otherwise *automatically* open exactly when the turn is live and the fold is the last `steps` item or the last `thinking` item of its turn. This is a controlled `Collapsible` (`open` and `onOpenChange`) over a state that is `undefined` until the reader toggles it, so a fold that was automatically open closes by itself when the turn stops being live, and a fold the reader opened stays open.

- [ ] **Step 1: Write the failing tests**

Added to `TurnList.test.tsx`, over `fixtureTurns().turns` (Story 1's `turns.json`: turn 1 holds a `steps` item of 7 total with 6 held, one `failed` and one `interrupted`, and the agent step to `a1f00dcafe0002`, a `text` item, the markdown `text` item and two `note` items, one with `error`; turn 2 is `/compact` with a `compacted` item; turn 3 is `partial` with a `thinking` item, a `steps` item with the running agent step `a1f00dcafe0001` and a pending step, and a `text` item with `code_inline`) and inline items:

- `TurnList: the fixture's conversation draws each kind of item` renders all three turns with `sessionId` 1, `live` false and `remote` `https://claude.ai/code/webshot`, and asserts: the prompt `YOU` of turn 1, `COMPACTED`, `Its start is earlier.` (turn 3), `× Interrupted by you`, `API error: overloaded, retrying` (and that this one has no `×` before it), the `Findings` heading, `7 steps` with ` · 1 failed`, and `▸ thinking: The ui tests may be slow, so start with TestPane`.
- `TurnList: a text item draws its tree and continues in Claude when cut` renders a `text` item with `truncated` and asserts the text, the line `… continues in Claude` with `Claude` a link to `remote` with `target` `_blank` and `rel` `noopener noreferrer`; with `remote` `""` the same line has no link; an item not `truncated` has no such line.
- `TurnList: a thinking item folds to its first non-blank line` renders `\n  \nsecond\nthird` with `truncated` true, asserts the trigger `▸ thinking: second`, that the body is not drawn, and after a click the whole text and a trailing `…`.
- `TurnList: a steps fold counts its steps and its failures` renders items with `total` 1 and `total` 3 with `failed` 2, asserting `1 step` and `3 steps · 2 failed`, and that a `total` of 5 with 3 steps held says `2 earlier steps not shown` once opened.
- `TurnList: a step's glyph and end by its state` opens a fold of steps: pending in a live turn (`◐` and `running 3m` for an `at` three minutes before `NOW`), pending in a turn that is not live (`no result`, no glyph), `failed` (`×` in `text-error`), `interrupted` (`×`, not in `text-error`), `done` (no glyph, no end text).
- `TurnList: a step that launched a subagent links to its screen` opens a fold with an `Agent` step carrying `agent` `a1f00dcafe0001`, `finished` false, and asserts a link whose `href` is `#/s/9/agent/a1f00dcafe0001` for `sessionId` 9, `◐` and an end of `›`; with `finished` true no `◐`, the end still `›`; a step whose `agent` is `a b/c` is no link and has no `›`.
- `TurnList: the live newest turn opens its last steps fold and last thinking item` renders one turn with two `steps` items and two `thinking` items (distinct texts) and `live` true and asserts the later of each is open (its content found) and the earlier of each closed; with `live` false all four are closed; and in a two-turn list only the newest turn's last folds open.
- `TurnList: a fold closes by itself when the turn stops being live and a fold the reader opened stays open` renders live, rerenders with `live` false and asserts the automatically open fold closes (`waitFor`); then renders a non-live turn, clicks a fold open, rerenders with the same props, then with `live` true and again with `live` false (so `auto` changes twice), and asserts the fold is still open after each.
- `TurnList: a compacted rule carries its time` asserts `COMPACTED <clock(at)>` for an item with `at` and `COMPACTED` alone for one with `at` `""`.
- `TurnList: a note is dim with a cross, or red when it is an error` asserts `× Interrupted by you` without the `text-error` class and `API error: overloaded, retrying` with it.
- `TurnList: an item of an unknown kind draws its kind and text in neutral styling` renders `{ kind: "mystery", at, text: "hello" }` (through a cast) and one with no `text`, asserting `mystery` and `hello` are drawn, that `mystery` alone is drawn for the second, and that neither has the `text-error` class or a fold.
- `TurnList: steps, thinking and notes holding markup render as text` renders a step summary `<img src=x onerror=alert(1)>`, a thinking text `<b>x</b>` and a note `<script>y</script>` (fold opened) and asserts the literals are drawn and `container.querySelector("img, b, script")` is `null`.

Fails without: TurnList: the fixture's conversation draws each kind of item: any one kind's case removed from `Item`. TurnList: a text item draws its tree and continues in Claude when cut: `truncated` ignored, or `rel` left as `noopener`, or a link drawn when `remote` is empty. TurnList: a thinking item folds to its first non-blank line: the first line taken without skipping blanks, or the `…` dropped. TurnList: a steps fold counts its steps and its failures: the singular `1 step` always plural, or the failed count dropped, or `hidden` not computed. TurnList: a step's glyph and end by its state: `running` taken from the state alone (without `live`), or `interrupted` drawn in `text-error`. TurnList: a step that launched a subagent links to its screen: `AGENT_ID` not tested, or the `href` built from the agent alone. TurnList: the live newest turn opens its last steps fold and last thinking item: the open rule taking the first fold, or ignoring `live`. TurnList: a fold closes by itself when the turn stops being live and a fold the reader opened stays open: `defaultOpen` in place of the controlled `open` (the automatic fold stays open), or the reader's choice reset whenever `auto` changes. TurnList: a compacted rule carries its time: the clock dropped. TurnList: a note is dim with a cross, or red when it is an error: `error` ignored. TurnList: an item of an unknown kind draws its kind and text in neutral styling: the default case drawing nothing, or drawing a `Collapsible`. TurnList: steps, thinking and notes holding markup render as text: any of the three drawn through an inner-HTML prop.

- [ ] **Step 2: Run them to see them fail**

Run: `pnpm -C internal/web/ui exec vitest run src/conversation/TurnList.test.tsx`

Expected: the new tests fail (every item is still the neutral line: `COMPACTED`, `7 steps` and the markdown heading are not found); Task 5.3's and Task 5.1's tests still pass.

- [ ] **Step 3: Implement**

Replace the temporary `Item` in `TurnList.tsx` with the kinds above, adding the step row, the two folds and their fold-state hook as private pieces of the file; drop the temporary-form comment.

- [ ] **Step 4: Run them to see them pass, then the story's gates**

Run: `pnpm -C internal/web/ui exec prettier --write src/conversation/TurnList.tsx src/conversation/TurnList.test.tsx && pnpm -C internal/web/ui exec vitest run src/conversation/ && pnpm -C internal/web/ui run check`

Expected: every test in `src/conversation/` passes and `check` (typecheck, lint, format, the whole Vitest suite) is green.

Then `pnpm -C internal/web/ui run build`, `pnpm -C internal/web/ui run dist:check` and `CGO_ENABLED=0 go test ./internal/web/`: nothing imports `TurnList` yet, so the bundle is expected to be unchanged; `git status --short internal/web/dist` lists any file the build changed, and those files are part of this commit.

- [ ] **Step 5: Commit**

```
git commit -m "feat(web): the turn list draws text, thinking, steps, compaction and notes" -- internal/web/ui/src/conversation/TurnList.tsx internal/web/ui/src/conversation/TurnList.test.tsx internal/web/dist
```

## Story 6: A session's frame: its head, tabs, fact strip, seen times and the fleet's NEW mark

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/panels.ts`, `internal/web/ui/src/app/panels.test.ts`, `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/session/SessionHead.tsx`, `internal/web/ui/src/session/FactStrip.tsx`, `internal/web/ui/src/session/SubLine.tsx`, `internal/web/ui/src/session/NoSession.tsx`, `internal/web/ui/src/session/facts.ts`, `internal/web/ui/src/session/session.test.tsx`, `internal/web/ui/src/fleet/seen.ts`, `internal/web/ui/src/fleet/seen.test.ts`, `internal/web/ui/src/panels/fleet/SessionRow.tsx`, `internal/web/ui/src/panels/fleet/index.tsx`, `internal/web/ui/src/panels/fleet/fleet.test.tsx`, `internal/web/ui/CLAUDE.md`, `internal/web/dist/`
- **Interfaces:** exposes `PanelTab`, the widened `Panel`, the tab rules, `SessionHead`, `FactStrip`, `SubLine`, `NoSession`, `parityText`, `checksText`, `groupGlyph`, `SEEN_KEY`, `readSeen`, `markSeen`, `pruneSeen`, `hasNewTurn`, `useMarkSeen`, `ContextRow`'s `onBack`, the exported `PrMark`; uses part 1's `ContextRow`, `SessionTabs`, `sessionTabs`, `rowShare`, `ago` and `hrefFor`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens fleet -out "$d"'`
- **Review:** deep
- **Form:** brief (the shell's slots and a port of `session.mjs`'s head, `strip.mjs` and `fleet.mjs`'s seen times)

This story gives every session view the classic frame. The shell draws a panel's `Strip` under the session's tabs and a tab's `Mark` after its label; the subagent and job routes draw the session's tabs (lighting conversation and info); a non-Claude session's `#/s/<id>` lights info. The story adds `SessionHead`, `FactStrip`, `SubLine`, `NoSession` and the facts' helpers, the `onBack` of `ContextRow`, the seen times in the classic `loom.seen` double encoding with the hook that marks a session seen, and the fleet row's NEW mark with the fleet's pruning. It also adds the links rule to `internal/web/ui/CLAUDE.md`. No code here holds a landmine marker. Every task changes only `internal/web/ui/src/`, plus one rule in `internal/web/ui/CLAUDE.md`; `internal/web/dist/` is rebuilt once, in the last task, so `pnpm run dist:check` is stale between tasks 6.1 to 6.5 (no story gate reads it; the Go tests read the committed bundle, which still passes its budget tests).

Every command below runs from the repo root. A task's test command is `pnpm -C internal/web/ui exec vitest run <file>`; the type and lint check after each implementation is `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`. Repo ESLint rejects `_`-prefixed unused parameters (type a mock as `vi.fn<(a: A) => R>(…)`) and assigning `window.location.hash`; components return `ReactElement`; write no `style` attribute, take colours from the tokens (`text-attention`, `text-turn`, `text-working`, `text-muted-foreground`, `text-error`) and keep touch targets at `min-h-11`.

### Task 6.1: The panel's Strip, a tab's Mark, the tab rules and ContextRow's onBack

**Files:**
- Modify: `internal/web/ui/src/app/panels.ts:33-43`
- Modify: `internal/web/ui/src/app/shell.tsx` (the `react` import at lines 2-9; `AppShell`'s header at lines 168-185; `TABBED`, `ContextRow`, `tabOf` and `SessionTabs` from line 268)
- Test: `internal/web/ui/src/app/shell.test.tsx`

Behaviour and names (contract: `PanelTab`, `Panel`, the tabs a route lights, `ContextRow`'s `onBack`):

- `app/panels.ts`: add `export interface PanelTab { order: number; label: string; Mark?: ComponentType<{ session: Session }> }`. In `Panel`, replace the line `  tab?: { order: number; label: string };` with `  tab?: PanelTab;` and add `  Strip?: ComponentType<PanelProps>;` directly after the line `  Head?: ComponentType<PanelProps>;`. Extend the comment above `Panel` (it begins `// Panel is one view.`) with: Strip is the line under the session's tabs; a tab's Mark is drawn after its label and is handed the session. `sessionTabs` is unchanged.
- `app/shell.tsx`, `ContextRow`: add the prop `onBack?: (e: MouseEvent<HTMLAnchorElement>) => void` beside `back` (import `type MouseEvent` from `react` in the existing import block, after `type ErrorInfo,`) and set it as the `onClick` of the back link, the `<a href={back} className="flex min-h-11 flex-none items-center text-sm text-muted-foreground">`. A `ContextRow` without `onBack` is unchanged.
- `app/shell.tsx`, `TABBED`: add `"agent"` and `"job"` to the set (its comment line, `// TABBED are the routes that show a session's tabs under their head.`, stays true).
- `app/shell.tsx`, `tabOf`: its signature becomes `tabOf(panel: PanelId, session: Session): PanelId`. After the two existing lines that map `flowStory`/`flowDoc` to `flow` and `mockupFull` to `mockup`, an `agent` route lights `turns`, a `job` route lights `info`, and a result of `turns` for a session whose `claude` is false is `info` (so an agent route of a non-Claude session lights `info` too). Update its comment (it begins `// tabOf is the tab a route lights`) to say so. `SessionTabs` calls `tabOf(route.panel, session)`. Because `turns` is then never the current tab of a non-Claude session, the existing keep-the-tab-under-the-user rule never adds its `turns` tab.
- `app/shell.tsx`, `SessionTabs`: inside the `tabs.map` callback, after `{tab.tab?.label ?? tab.id}` draw the tab's Mark when it has one: `<Boundary what={`The ${tab.id} tab's mark`} fallback={null}><Mark session={session} /></Boundary>`, where `Mark` is `tab.tab?.Mark` (turn the callback's arrow body into a block to read it). A Mark that throws leaves its tab and the other tabs drawn, and is logged by `Boundary`.
- `app/shell.tsx`, `AppShell`: take `const Strip = entry?.Strip;` beside `const Head = entry?.Head;` and, directly after the existing

  ```
          {session !== undefined && TABBED.has(route.panel) ? (
            <SessionTabs session={session} />
          ) : null}
  ```

  draw the strip under the tabs on the same condition, in its own boundary keyed like the head's: `Strip === undefined` draws nothing, else `<Boundary key={resetKey(route.panel, props)} what={`The ${entry?.title(props) ?? SCREEN_NAMES[route.panel]} strip`} fallback={null}><Strip {...props} /></Boundary>`. Wrap the tabs and the strip in one fragment under the one condition, so the strip never draws without the session's tabs.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/app/shell.test.tsx`, using its existing helpers (`shellAt`, `withGroups`, `live`, `header`, the `SessionTabs` describe's `tabPanels` and `aSession`). Add to `describe("ContextRow")`, `describe("SessionTabs")` and `describe("AppShell's chrome")`:

  - `ContextRow` — `calls onBack with the click and lets it cancel the navigation`: draw `<ContextRow title="Subagent" back="#/s/5" onBack={onBack} />` with `onBack = vi.fn<(e: MouseEvent<HTMLAnchorElement>) => void>((e) => { e.preventDefault(); })`; `fireEvent.click(screen.getByRole("link", { name: /conversation|back/ }))` returns false (the default was prevented), `onBack` was called once, and the link still has `href` `#/s/5`. Fails without: `ContextRow` not setting `onClick={onBack}` (onBack never called and the click returns true).
  - `SessionTabs` — `draws a tab's Mark after its label, handed the session`: define the three tab panels with `tabPanels()` then redefine `changes` with `tab: { order: 2, label: "changes", Mark: ({ session }) => <b>+{session.id}</b> }`; render `<SessionTabs session={aSession(true)} />` at `#/s/7/info`; the `changes` link's text is `changes+7` and the other links' texts are bare labels. Fails without: `SessionTabs` not drawing `Mark` (the link's text stays `changes`).
  - `SessionTabs` — `keeps its tabs when a Mark throws`: a `Mark` that throws, `console.error` spied (restore in `finally`, as the existing head test does); the three links are all drawn and the throwing tab's link has its label. Fails without: the `Boundary` around `Mark` (the render throws and the nav is gone).
  - `SessionTabs` — a parameterised test `lights the tab its route names` with `it.each` over: `#/s/7/agent/a1f00d` for a Claude session lights `conversation`; `#/s/7/job/216` for a Claude session lights `info`; `#/s/7` for a non-Claude session lights `info`, draws no `conversation` link (define `changes` with `appliesTo: () => true` in this test so two tabs draw), and `#/s/7/agent/a1f00d` for a non-Claude session lights `info` and draws no `conversation` link. Each asserts the `aria-current="page"` link. Fails without: `tabOf` mapping `agent` to `turns` (the first case lights nothing), `job` to `info` (the second lights nothing), or the `claude` term (the third and fourth light `conversation` or add a `conversation` tab).
  - `AppShell's chrome` — `draws the panel's Strip under the session's tabs, in the header`: define `turns` with `tab`, `Head` and `Strip: () => <p>the strip</p>`, and `info` with a tab; `live(withGroups(["working"]))`; `shellAt("#/s/100")`; the header holds the nav and the strip text, and the strip follows the nav in document order (`nav.compareDocumentPosition(strip) & Node.DOCUMENT_POSITION_FOLLOWING`). Fails without: `AppShell` drawing `Strip` (the text is absent), or drawing it above the tabs (the position check fails).
  - `AppShell's chrome` — `keeps the header and the tabs when the Strip throws`: a `Strip` that throws, `console.error` spied; the header still holds the head text and the nav, and the screen draws. Fails without: the strip's own `Boundary` (the head's boundary does not cover it, so the shell loses the header).
  - `AppShell's chrome` — `draws a session's tabs on a subagent and a job route`: define `agent` and `job` panels (`Head: () => <p>head</p>`, plain `Body`) with `turns` and `info` tabs; `shellAt("#/s/100/agent/a1f00d")` has a nav whose `conversation` link is current; the same at `#/s/100/job/216` has `info` current. Fails without: `agent` and `job` in `TABBED` (no nav).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx`
  Expected: the seven new tests fail (the Mark, Strip, tab-rule and `onBack` assertions above); the existing tests pass.

- [ ] **Step 3: Implement** as the behaviour above says, in `internal/web/ui/src/app/panels.ts` and `internal/web/ui/src/app/shell.tsx`.

- [ ] **Step 4: Run it to see it pass**

  Run: `pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx src/app/panels.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
  Expected: all tests pass; typecheck and lint print no error.

- [ ] **Step 5: Commit** (`git add -N` has nothing to add: every file exists)

  ```
  git commit -m "feat(web): a panel's Strip, a tab's Mark, the agent and job tabs and ContextRow's onBack" -- internal/web/ui/src/app/panels.ts internal/web/ui/src/app/shell.tsx internal/web/ui/src/app/shell.test.tsx
  ```

### Task 6.2: The facts' helpers, SubLine and NoSession

**Files:**
- Create: `internal/web/ui/src/session/facts.ts`
- Create: `internal/web/ui/src/session/SubLine.tsx`
- Create: `internal/web/ui/src/session/NoSession.tsx`
- Create: `internal/web/ui/src/session/session.test.tsx` (task 6.4 adds to it)

Behaviour and names (contract: `parityText`, `checksText`, `groupGlyph`, `SubLine`, `NoSession`; the classic `parityText`, `checksText` and `SubLine` in `internal/web/static/js/session.mjs:44-67` and `glyphOf` in `skin.mjs`):

- `session/facts.ts`:
  - `function parityText(session: Session): string`: `""` while `parity_known` is false; `"✓ even"` when `ahead` and `behind` are both 0; else `"↑<ahead> ↓<behind>"`.
  - `function checksText(pr: Pr): string`: for a PR whose `state` is not `open`, its `state`; for an open one, by `checks`: `passing` → `"✓ checks pass"`, `pending` → `"checks pending"`, `failing` → `"✗ checks fail"`, `none` → `"no checks"`, anything else `""`.
  - `function groupGlyph(group: string): string`: `needs_you` `●`, `your_turn` `◆`, `working` `◐`, `paused` `○`; a group this build does not know gets Paused's `○`.
  - `function groupTone(group: string): string` (private to this story, used by `SubLine` and `FactStrip`): the token class of the group's glyph: `text-attention` for `needs_you`, `text-turn` for `your_turn`, `text-working` for `working`, else `text-muted-foreground`.
- `session/SubLine.tsx`: `SubLine({ session }: { session: Session }): ReactElement` draws, in one wrapping `text-xs text-muted-foreground` row with `px-4 py-2`: the glyph (`aria-hidden`, its `groupTone`) then ` <label lowercased>` and, when `since` is not empty and `ago(since)` is not empty, ` · <ago>`; then the session's workspace name (`label` else `name`, read from `useFleet`'s snapshot `workspaces` by `session.workspace`) when the snapshot lists it; then the session's `branch` when it is not empty. `ago` is imported from `@/panels/fleet/SessionRow`.
- `session/NoSession.tsx`: `NoSession(): ReactElement` draws `<p role="status" className="px-4 py-8 text-center text-muted-foreground">This session is gone.</p>`.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/session/session.test.tsx`. The file's header imports `render`, `screen`, `act` from `@testing-library/react`, `afterEach`, `beforeEach`, `describe`, `expect`, `it`, `vi` from `vitest`, `type Session`, `type Snapshot` from `@/api/gen`, `fixtureSnapshot` from `@/fixtures`, `allSessions`, `shownWorkspaces` from `@/fleet/groups` and `useFleet` from `@/store/fleet`. It fixes the clock with `vi.useFakeTimers({ toFake: ["Date"] })` and `vi.setSystemTime(NOW)` (`NOW = Date.parse("2026-11-18T12:00:00Z")`) in `beforeEach`, restores real timers and resets the store (`useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 })`) and the hash (`window.history.replaceState(null, "", window.location.pathname)`) in `afterEach`. It defines a helper `load(over: Partial<Session>, usage = fixture's usage): Session` that takes the fixture's first session, sets `workspace` to the first shown workspace's id, applies `over` (default `id: 7`, `claude: true`, `since` 5 minutes before `NOW`), stores a snapshot holding only that session in that workspace (`useFleet.setState({ snapshot, stale: false, link: "live" })`), and returns it. Tests, in `describe("facts")` and `describe("SubLine and NoSession")`:

  - `parityText words the TUI's parity token`: unknown parity (`parity_known` false, ahead 3) is `""`; known 0/0 is `"✓ even"`; ahead 2 behind 0 is `"↑2 ↓0"`; ahead 0 behind 4 is `"↑0 ↓4"`. Fails without: the `parity_known` check (the first case returns `"↑3 ↓0"`), or the even case (the second returns `"↑0 ↓0"`).
  - `checksText words an open PR's checks and any other PR by its state`: `open` with each of `passing`, `pending`, `failing`, `none` gives the four texts above; `open` with an unrecognised checks value gives `""`; `merged`, `closed` and `draft` give their own words, whatever their `checks`. Fails without: the non-open branch (a merged PR with `checks: "passing"` would read `"✓ checks pass"`).
  - `groupGlyph is each group's glyph, and Paused's for one it does not know`: the four groups and `"somethingnew"` → `○`. Fails without: the fallback (it returns `undefined`).
  - `SubLine words the status, the age, the workspace and the branch`: a `working`-group session with `label` `"Running"`, `since` five minutes before `NOW`, workspace named `"loom"` (set the workspace's `label` to `""` and `name` to `"loom"` in the loaded snapshot) and branch `"feat/x"` draws text `"◐ running · 5m"`, `loom` and `feat/x`. Fails without: lowercasing the label (reads `Running`), or the workspace lookup (no `loom`).
  - `SubLine prefers the workspace's label, and leaves out what the session lacks`: workspace `label` `"Main"` wins over its name; a session with `branch` `""` and `since` `""` draws only the glyph and label (text exactly `"◐ running"` plus the workspace, no ` · `). Fails without: `label || name` (reads the name), the branch check (an empty branch draws an empty span, which the test's child-count assertion on the row catches), or the `since` check (draws ` · `).
  - `NoSession says the session is gone`: draws a `status` with `This session is gone.`. Fails without: the text changed or the component drawing nothing.

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/session/session.test.tsx`
  Expected: the file fails to load: `Failed to resolve import "./facts"` (and the other two modules), until they exist.

- [ ] **Step 3: Implement** the three modules as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `pnpm -C internal/web/ui exec vitest run src/session/session.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
  Expected: the tests of `facts` and `SubLine and NoSession` pass; typecheck and lint print no error.

- [ ] **Step 5: Commit**

  ```
  git add -N internal/web/ui/src/session/facts.ts internal/web/ui/src/session/SubLine.tsx internal/web/ui/src/session/NoSession.tsx internal/web/ui/src/session/session.test.tsx
  git commit -m "feat(web): the session frame's facts helpers, sub-line and gone notice" -- internal/web/ui/src/session/facts.ts internal/web/ui/src/session/SubLine.tsx internal/web/ui/src/session/NoSession.tsx internal/web/ui/src/session/session.test.tsx
  ```

### Task 6.3: The seen times

**Files:**
- Create: `internal/web/ui/src/fleet/seen.ts`
- Create: `internal/web/ui/src/fleet/seen.test.ts`

Behaviour and names (contract: `SEEN_KEY`, `readSeen`, `markSeen`, `pruneSeen`, `hasNewTurn`, `useMarkSeen`; the classic ones in `internal/web/static/js/fleet.mjs:70-112` over `pref` in `store.mjs`):

- `export const SEEN_KEY = "loom.seen"`. The stored value is the classic double encoding: `localStorage.getItem(SEEN_KEY)` is the JSON text of a string, and that string is the JSON text of `{ "<session id>": <ms> }`.
- `readSeen(): Record<string, number>`: `JSON.parse(JSON.parse(raw) || "{}")` of `localStorage.getItem(SEEN_KEY)`, a null (nothing stored) read as `"{}"`; `{}` on any failure (a throwing `getItem`, text that does not parse, an outer value that is not a string) and for a result that is not an object (`null`, an array, a number); keep only the entries whose value is a finite number.
- `markSeen(id: number): void`: sets `seen[String(id)] = Date.now()` on what `readSeen` returns and writes `JSON.stringify(JSON.stringify(seen))`; a refused write (full or blocked storage) is swallowed.
- `pruneSeen(ids: number[]): void`: an empty list prunes nothing (it is more likely not loaded than a fleet of none); otherwise it deletes the times of every id not in `ids`, writing (in the same encoding) only when it deleted something.
- `hasNewTurn(session: Session, seen: Record<string, number>): boolean`: false unless `session.group === "your_turn"`; true when `seen[String(session.id)]` is undefined, or `Date.parse(session.since) || 0` is greater than it.
- `useMarkSeen(id: number | undefined): void`: a `useEffect` over `id` that, for a defined `id`, calls `markSeen(id)` and returns a cleanup calling `markSeen(id)` again (on mount, on unmount, and for the old id when `id` changes).
- `useSeen(): Record<string, number>` (this story's own name, used by the fleet panel in task 6.5): the stored times, re-read whenever `markSeen` or `pruneSeen` writes (they notify a module-level set of listeners after a write) and on the window's `storage` event, through `useSyncExternalStore` over the raw stored string (its snapshot is that string, so it is stable; the object is parsed from it with `useMemo`). Why: the conversation's cleanup marks the session seen after the fleet's first render, and the fleet must redraw without its NEW mark then.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/fleet/seen.test.ts` (imports `renderHook`, `act` from `@testing-library/react`; `vi.useFakeTimers({ toFake: ["Date"] })` with a fixed `NOW` in `beforeEach`; `window.localStorage.clear()` and `vi.restoreAllMocks()` in `afterEach`; a helper `yourTurn(id, since)` building a session from `fixtureSnapshot()`'s first session with `group: "your_turn"`). The classic value is the literal `CLASSIC = '"{\\"5\\":1700000000000}"'`, which is what classic `pref("seen", JSON.stringify({ "5": 1700000000000 }))` stores. Tests:

  - `SEEN_KEY is the classic key`: `SEEN_KEY` is `"loom.seen"`. Fails without: a renamed key.
  - `readSeen reads what the classic pref wrote`: store `CLASSIC`; `readSeen()` equals `{ "5": 1700000000000 }`. Fails without: the double decode (a single parse yields a string, so the result is not the object).
  - `readSeen is empty for nothing stored and for every value it cannot use`: nothing stored; `not json`; the outer string holding an array (`JSON.stringify("[1,2]")`); the outer string holding `null`; a single-encoded object (`JSON.stringify({ "5": 1 })`); a `getItem` that throws (`vi.spyOn(Storage.prototype, "getItem")`) — each `{}`. Fails without: the null-as-`"{}"` read, the object check or the try/catch (each of those cases throws or returns a non-record).
  - `readSeen keeps only the entries that are numbers`: store `JSON.stringify(JSON.stringify({ "5": 10, "6": "x", "7": null }))`; result `{ "5": 10 }`. Fails without: the number filter.
  - `markSeen writes the classic double encoding and keeps the other times`: store `CLASSIC`; with `NOW` fixed, `markSeen(6)`; `localStorage.getItem(SEEN_KEY)` is exactly `JSON.stringify(JSON.stringify({ "5": 1700000000000, "6": NOW }))`. Fails without: the outer `JSON.stringify` (the stored text is single-encoded), or reading the old value first (the `5` entry is lost).
  - `markSeen survives a refused write`: `setItem` spied to throw; `markSeen(5)` does not throw. Fails without: the try/catch.
  - `pruneSeen drops the times of sessions no longer listed, in the classic encoding`: store times for 5, 6, 7; `pruneSeen([5, 7])`; stored text is exactly the double encoding of `{ "5": …, "7": … }`. Fails without: the filter (6 stays), or the outer encoding.
  - `pruneSeen of an empty list prunes nothing`: store `CLASSIC`; `pruneSeen([])`; the stored text is still `CLASSIC`. Fails without: the empty-list return (the store becomes `"{}"` double-encoded).
  - `pruneSeen writes nothing when nothing is dropped`: `setItem` spied; `pruneSeen([5])` over `CLASSIC`; `setItem` was not called. Fails without: the changed check.
  - `hasNewTurn is true for a Your turn session never opened, or whose turn ended after it was opened`: `since` `T`; never opened → true; opened at `T - 1000` → true; opened at `T + 1000` → false; opened at exactly `T` → false. Fails without: the `undefined` term (never opened gives false) or the `>` comparison (an opened-after session gives true).
  - `hasNewTurn is false for every other group, opened or not`: `needs_you`, `working`, `paused`, never opened → false. Fails without: the group term (never opened gives true).
  - `useMarkSeen marks on mount and again on unmount`: `renderHook(() => useMarkSeen(5))` with the clock at `T1` stores `{ "5": T1 }`; advance the clock to `T2` and unmount; stored `{ "5": T2 }`. Fails without: the cleanup (stays `T1`), or the mount call (empty before unmount).
  - `useMarkSeen marks the old session closed and the new one opened when the id changes, and nothing without one`: rerender from 5 to 6 at a later time stores both at the later time; `useMarkSeen(undefined)` stores nothing. Fails without: the dependency on `id` (6 is never marked) or the undefined guard (an entry named `"undefined"` appears).
  - `useSeen follows the writes of markSeen and pruneSeen`: `renderHook(() => useSeen())` is `{}`; `act(() => markSeen(5))` makes `result.current["5"]` the clock's time; `act(() => pruneSeen([9]))` makes it `{}` again. Fails without: the listeners notified after a write (the result never changes).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/fleet/seen.test.ts`
  Expected: `Failed to resolve import "./seen"`.

- [ ] **Step 3: Implement** `internal/web/ui/src/fleet/seen.ts` as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `pnpm -C internal/web/ui exec vitest run src/fleet/seen.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
  Expected: all tests of the file pass; typecheck and lint print no error.

- [ ] **Step 5: Commit**

  ```
  git add -N internal/web/ui/src/fleet/seen.ts internal/web/ui/src/fleet/seen.test.ts
  git commit -m "feat(web): the seen times in the classic double encoding, and the hook that marks a session seen" -- internal/web/ui/src/fleet/seen.ts internal/web/ui/src/fleet/seen.test.ts
  ```

### Task 6.4: SessionHead, FactStrip and the exported PrMark

**Files:**
- Create: `internal/web/ui/src/session/SessionHead.tsx`
- Create: `internal/web/ui/src/session/FactStrip.tsx`
- Modify: `internal/web/ui/src/panels/fleet/SessionRow.tsx:71-72`
- Test: `internal/web/ui/src/session/session.test.tsx`

Behaviour and names (contract: `SessionHead`, `FactStrip`; part 1's `ContextRow`, `useRoute`, `hrefFor`, `rowShare`, `ago`; the classic ones in `session.mjs:340-360` and `strip.mjs:10-35`):

- `panels/fleet/SessionRow.tsx`: export the PR mark. Replace the line `function PrMark({ pr }: { pr: Pr }): ReactElement | null {` with `export function PrMark({ pr }: { pr: Pr }): ReactElement | null {` (its comment, `// A PR's state: ✓, ✗ or … for an open PR's checks, else the state's own text.`, stays).
- `session/SessionHead.tsx`: `SessionHead(props: PanelProps): ReactElement`, reading the session by `useSession(props.sessionId)` and the route by `useRoute()`. Without the session: `<ContextRow back={hrefFor("fleet")} title="" />` (the "Loading…" head is part 3's). With it: `<ContextRow back={hrefFor("fleet")} title={session.title} actions={…} />`, where the action is, unless `boxed` or `remote_url` is empty, the link `<a href={session.remote_url} target="_blank" rel="noopener noreferrer" className="flex min-h-11 flex-none items-center text-sm text-muted-foreground">claude <span aria-hidden="true">↗</span></a>`, and `boxed` is `route.panel === "turns" && session.claude && session.group === "needs_you"` (the conversation's needs-you box then has its own Open in Claude button). It is a plain link, never a command (spec Part 2 settlement 5).
- `session/FactStrip.tsx`: `FactStrip(props: PanelProps): ReactElement`: an empty fragment unless the route is `turns`, the session exists and `session.claude`. Otherwise the facts, in this order, each left out when the session lacks it, joined by ` · ` (a `<span key>` per fact, the separator inside it as the classic strip does):
  1. status: the group's glyph (`groupGlyph`, `aria-hidden`, `groupTone`) then ` <label lowercased>` and, when `since` parses, ` <ago(since)>`;
  2. changes: `+<added>` (class `text-working`) and `−<removed>` (class `text-error`), when either is above 0;
  3. PR: `PR #<number>` then `<PrMark pr={…} />`, when `pr` is not null;
  4. share: `rowShare(session, usage, staleAfterS)`'s `text`, in `text-attention` when its `amber` is true, with `usage` read from the snapshot (`snapshot?.usage ?? []`) and `staleAfterS` from `snapshot?.usage_stale_after_s`; nothing when it is null.
  
  The strip is `flex min-h-11 items-center px-4 text-xs text-muted-foreground`, its facts on one line that truncates (`min-w-0 flex-1 truncate`). With `jobs_label` empty the whole line is one link to the info tab, `hrefFor("info", { id })`. With a `jobs_label`, the line is a link to info and, on the right, a second link showing `jobs_label` (`flex-none pl-3 font-mono`): to the job named by `jobs_focus` (`hrefFor("job", { id, job: String(jobs_focus) })`) when `jobs_focus` is above 0, else to info (a `⚙ queued` badge).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/session/session.test.tsx`, with the `load` helper of task 6.2 and, for the share, a `usage` entry for the default account (`account: "default"`, `plan: ""`, `five_hour: { pct: 42, resets_at: <an hour after NOW, as RFC 3339> }`, `seven_day: null`, `at` `NOW` as RFC 3339) put into the loaded snapshot's `usage`. Draw each component with `render(<SessionHead sessionId={7} params={{ id: "7" }} search={{}} />)` after `window.history.replaceState(null, "", "#/s/7")` (or `…/info`). Tests, in `describe("SessionHead")` and `describe("FactStrip")`:

  - `SessionHead` — `draws the title as the h1 and a way back to the fleet`: `getByRole("heading", { level: 1, name: <title> })`; the back link's `href` is `#/`. Fails without: the title or the `back` prop.
  - `SessionHead` — `links to claude on the session's remote_url, in a new window, with no referrer or opener`: a working Claude session with `remote_url` `https://claude.ai/code/abc` at `#/s/7`: the link named `claude ↗` has that `href`, `target` `_blank` and `rel` holding `noopener` and `noreferrer`. Fails without: any of the three attributes removed.
  - `SessionHead` — `leaves the claude link to the needs-you box on the conversation of a waiting Claude session`: `needs_you`, `claude: true`, at `#/s/7`: no `claude` link. Fails without: the `boxed` rule (the link shows).
  - `SessionHead` — `keeps the claude link wherever the needs-you box is not on screen`: `needs_you` and Claude at `#/s/7/info` shows it (route term); a `working` Claude session at `#/s/7` shows it (group term); a `needs_you` non-Claude session at `#/s/7` shows it (claude term). Fails without: dropping the route, group or claude term of `boxed` (the matching case loses its link).
  - `SessionHead` — `draws no claude link while Remote Control is off`: `remote_url` `""`. Fails without: the `remote_url` check (an `<a>` with an empty href shows).
  - `SessionHead` — `draws only the way back for a session the snapshot does not list`: no session 7 in the store: the heading's text is empty, the back link is drawn, no `claude` link. Fails without: the no-session branch (the component throws on `session.title`).
  - `FactStrip` — `words the status, the changes, the PR and the share on one line`: working Claude session, `since` five minutes before `NOW`, `label` `"Running"`, `diff` `{ added: 12, removed: 3 }`, `pr` `{ number: 9, state: "open", checks: "passing" }`, usage 42% at `#/s/7`: the strip's text is `◐ running 5m · +12 −3 · PR #9 ✓ · 42%`. Fails without: any fact or the order (the text differs).
  - `FactStrip` — `leaves out the facts the session lacks`: `diff` 0/0, `pr` null, no usage entry: the text is `◐ running 5m`. Fails without: the diff, PR or share guards (` · +0 −0`, a `PR #` or a `0%` shows).
  - `FactStrip` — `tints the share amber from 80 percent`: usage `pct: 85`: the share's element has class `text-attention`; at 42 it has not. Fails without: the `amber` branch.
  - `FactStrip` — `is one link to the info tab with no jobs`: `jobs_label` `""`: one link, `href` `#/s/7/info`, holding the facts. Fails without: the link target.
  - `FactStrip` — `splits off the jobs badge, linking to the job it names or to info`: `jobs_label` `"⚙ 1 running"`, `jobs_focus` 216: two links, the facts' one to `#/s/7/info` and the badge's to `#/s/7/job/216` with the label as its text; with `jobs_label` `"⚙ queued"` and `jobs_focus` 0 the badge links to `#/s/7/info`. Fails without: the `jobs_focus` branch (the first badge links to info) or the split (a single link holds both).
  - `FactStrip` — `draws nothing off the conversation, for a non-Claude session, or for a session the snapshot does not list`: the same session at `#/s/7/info` and `#/s/7/diff`; a non-Claude session at `#/s/7`; no session 7: the container is empty each time. Fails without: the route, `claude` or session guard (the matching case draws the strip).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/session/session.test.tsx`
  Expected: the new tests fail on `Failed to resolve import "./SessionHead"` / `"./FactStrip"` (the file fails to load until both exist).

- [ ] **Step 3: Implement** `SessionHead.tsx`, `FactStrip.tsx` and the `PrMark` export as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `pnpm -C internal/web/ui exec vitest run src/session/session.test.tsx src/panels/fleet/fleet.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
  Expected: all tests pass; typecheck and lint print no error.

- [ ] **Step 5: Commit**

  ```
  git add -N internal/web/ui/src/session/SessionHead.tsx internal/web/ui/src/session/FactStrip.tsx
  git commit -m "feat(web): the session head and the conversation's fact strip" -- internal/web/ui/src/session/SessionHead.tsx internal/web/ui/src/session/FactStrip.tsx internal/web/ui/src/session/session.test.tsx internal/web/ui/src/panels/fleet/SessionRow.tsx
  ```

### Task 6.5: The fleet's NEW mark and its pruning of seen times

**Files:**
- Modify: `internal/web/ui/src/panels/fleet/SessionRow.tsx:1-3` (the header comment) and `SessionRow`'s props and title row
- Modify: `internal/web/ui/src/panels/fleet/index.tsx:3-14`, `Card` (from line 38), `Fleet`'s hooks (after `const [, setTick] = useState(0);`) and the two `<Card sessions={list} usage={usage} chipOf={chipOf} />` calls (lines 127 and 137)
- Test: `internal/web/ui/src/panels/fleet/fleet.test.tsx`

Behaviour and names (contract: `readSeen`, `pruneSeen`, `hasNewTurn`; the classic `Row`'s `fresh` and `Fleet`'s prune in `fleet.mjs:168` and `:196-198`):

- `SessionRow.tsx`: the header comment says `A port of the classic fleet's Row, less the NEW mark, which comes with the conversation that marks a session seen.`; replace the words from `, less the NEW mark` to the end of that sentence so it reads `A port of the classic fleet's Row.` `SessionRow` takes a new optional prop `fresh?: boolean` (false when absent). When true it draws `NEW` right after the title span (`<span className="min-w-0 flex-1 truncate text-sm font-medium">{session.title}</span>`), before the share: `<span className="flex-none rounded-sm bg-turn px-1 text-[10px] font-bold text-background">NEW</span>`, a plain span (not a `Badge`, so the fleet's badge-counting tests are undisturbed).
- `panels/fleet/index.tsx`: import `hasNewTurn`, `pruneSeen`, `useSeen` from `@/fleet/seen`. `Card` takes a new prop `seen: Record<string, number>` and passes `fresh={hasNewTurn(s, seen)}` to each `SessionRow`; both `<Card … />` calls pass `seen={seen}`. `Fleet` reads `const seen = useSeen();` and, above its early `return` for a null snapshot (hooks stay above it), runs `useEffect(() => { if (snapshot !== null) pruneSeen(allSessions(snapshot).map((s) => s.id)); }, [snapshot]);` — every session of every workspace, shown or not, as classic does (an empty list prunes nothing, `pruneSeen`'s own rule).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/fleet/fleet.test.tsx`, using its `withSnapshot`, `load`, `Body`, `byId` and fake clock (`NOW`); import `SEEN_KEY`, `markSeen`, `readSeen` from `@/fleet/seen`. The file's `afterEach` already clears `localStorage`. Pick the fixture's your-turn session with `allSessions(snapshot).find((s) => s.group === "your_turn")` and assert it is defined. Tests:

  - in `describe("SessionRow")` — `draws NEW after the title only when fresh`: `render(<SessionRow session={…} usage={usage} fresh />)` has a `NEW` text and the same row without `fresh` has none. Fails without: the `fresh` prop (no `NEW`), or drawing it unconditionally (the second has one).
  - in `describe("the fleet panel")` — `marks a Your turn session NEW until it is opened`: with `localStorage` empty the your-turn row shows `NEW`; then `act(() => { markSeen(id); })` (clock at `NOW`, after the session's `since`) removes it without a re-render of the panel from outside. Fails without: `hasNewTurn` (no mark), or `useSeen` (the mark stays until the 30 s tick).
  - `marks a session NEW only for a turn that ended after the last time it was opened`: store `{ [id]: sinceMs - 1000 }` in the classic encoding (`window.localStorage.setItem(SEEN_KEY, JSON.stringify(JSON.stringify({ [id]: sinceMs - 1000 })))`) → `NEW`; `sinceMs + 1000` → none. Fails without: the `>` comparison.
  - `marks no needs-you, working or paused row NEW`: with nothing stored, `NEW` appears once per your-turn session of the fixture and nowhere else (count the `NEW` texts against the number of your-turn sessions). Fails without: the group term of `hasNewTurn`.
  - `prunes the seen times of sessions the daemon no longer lists`: store times for a listed session's id and for `999`; after the panel draws, `readSeen()` holds the listed id and not `999`; the stored text is the double encoding. Fails without: the `pruneSeen` effect.
  - `prunes by every session of every workspace, shown or not`: a session of a workspace with `shown: false` keeps its time. Fails without: pruning only the shown workspaces' ids.
  - `never prunes on an empty fleet`: `withSnapshot((s) => { s.sessions = {}; })` with stored times: `readSeen()` still holds them. Fails without: `pruneSeen`'s empty-list rule or the guard on the snapshot.

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/fleet/fleet.test.tsx`
  Expected: the new tests fail (no `NEW` text; the times are never pruned); the existing ones pass.

- [ ] **Step 3: Implement** as the behaviour above says, in `SessionRow.tsx` and `index.tsx`.

- [ ] **Step 4: Run it to see it pass**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/fleet/fleet.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`
  Expected: all tests pass; typecheck and lint print no error.

- [ ] **Step 5: Commit**

  ```
  git commit -m "feat(web): the fleet row's NEW mark, and the fleet drops the seen times of sessions the daemon no longer lists" -- internal/web/ui/src/panels/fleet/SessionRow.tsx internal/web/ui/src/panels/fleet/index.tsx internal/web/ui/src/panels/fleet/fleet.test.tsx
  ```

### Task 6.6: The links rule in internal/web/ui/CLAUDE.md, and the rebuilt bundle

**Files:**
- Modify: `internal/web/ui/CLAUDE.md` (insert before the last rule, the one that begins `- **Leave the classic app (`internal/web/static/`) as it is until cutover`)
- Modify: `internal/web/dist/` (rebuilt)

- [ ] **Step 1: Add the rule.** Insert this bullet immediately before the line beginning `- **Leave the classic app (`internal/web/static/`) as it is until cutover`; do not edit any other rule (Story 3 amends the command rule and Story 13 adds the rest):

  ```
  - **Draw Open in Claude, and every other link that acts on nothing in loom, as a plain `<a>`, never as a command.** Such a link (`claude ↗` in `internal/web/ui/src/session/SessionHead.tsx`, the needs-you box's Open in Claude) changes no state, so it needs no confirmation, in-flight key or stale gate, and a window opened after a command's awaits (`runCommand` in `internal/web/ui/src/app/commands.ts`) is a popup the browser blocks. Give it `target="_blank"` and `rel="noopener noreferrer"`. **Convention** — a button that does nothing when tapped, with no error.
  ```

  The rule names only files that exist after task 6.4 (the needs-you box is named without a path, since its file arrives in a later story).

- [ ] **Step 2: Run the lint on it**

  Run: `CGO_ENABLED=0 go test ./tools/claudemd/`
  Expected: `ok` (the file stays far inside its budgets: 100 lines, 20,000 bytes, 1,200 bytes a line).

- [ ] **Step 3: Rebuild the bundle and run the whole story's Verify**

  Run: `pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ ./tools/claudemd/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens fleet -out "$d"`
  Expected: the build writes `internal/web/dist/`; `check` passes every test, typecheck, lint and format; `dist:check` prints no stale-bundle error; both Go packages print `ok`; webshot renders the fleet with no console error.

- [ ] **Step 4: Run the story gates**

  Run: `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi' && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test -timeout 30m ./...`
  Expected: no output from gofmt, vet silent, every package `ok`.

- [ ] **Step 5: Commit** (`git add -N` the bundle's new files; a rebuilt bundle renames its hashed assets)

  ```
  git add -N internal/web/dist
  git commit -m "feat(web): the links rule, and the bundle with the session frame" -- internal/web/ui/CLAUDE.md internal/web/dist
  ```

## Story 7: The needs-you box, the asks and the reply box

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/conversation/NeedsYou.tsx`, `internal/web/ui/src/conversation/NeedsYou.test.tsx`, `internal/web/ui/src/conversation/Asks.tsx`, `internal/web/ui/src/conversation/Asks.test.tsx`, `internal/web/ui/src/conversation/ReplyBox.tsx`, `internal/web/ui/src/conversation/ReplyBox.test.tsx`, `internal/web/dist/`
- **Interfaces:** exposes `NeedsYou`, `Asks`, `ReplyBox`; uses Story 2's `Textarea`, `utf8Length`, `MAX_MESSAGE_BYTES` and `CommandButton`'s `disabled`, Story 3's `useDrafts`, `sessionActions`, `allowArgs`, `answerBody` and the `session.send` and `ask.*` commands (keyed by their ask's id), Story 6's `groupGlyph`, part 1's `CommandButton`, `useFleet`, `useLink`, `buttonVariants` and `Badge`, Story 1's `fixtureSnapshot` asks
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
- **Review:** deep
- **Form:** brief (forms over Story 3's commands, whose bodies the daemon validates and bounds; each form's rule is a named test)

Port the classic `needs.mjs`, `asks.mjs` and `reply.mjs` onto Story 3's drafts, helpers and commands, so a session that waits can be answered, and any session replied to, from the conversation. The reply box (`ReplyBox`) is a `Textarea` over the session's reply draft in `useDrafts`, with quick-reply chips while it is empty and a Send that is the `session.send` command. The asks (`Asks`) draw a card per ask in `session.asks`: a question form over the ask's draft, a permission card, and a neutral card for a kind this build does not know. The needs-you box (`NeedsYou`) wraps the reason, the asks and Open in Claude. No code of this story invents a body, an argument or an enablement rule: every Allow's args come from Story 3's `allowArgs`, Answer's body and its enablement from `answerBody`, and every send goes through a command, so the confirmation, the in-flight key, the stale gate and the toasts are the command path's. Nothing here holds a landmine marker, so the developer runs at the default effort.

Rules every task keeps (the app's `internal/web/ui/CLAUDE.md`): components return `ReactElement`; Claude's and the daemon's strings are drawn as text nodes, never as markup, and no `style` attribute is written (the CSP drops it); every colour is a token class (`text-error`, `text-muted-foreground`, `text-attention`, `border-border`, …), never a literal; form text is `text-[16px]`; touch targets come from `Button` and `buttonVariants` (their 44px hit area) or a `min-h-11` row; an unused parameter is never `_`-prefixed; a test mock is typed as `vi.fn<(a: A) => R>(…)`. Open in Claude is an anchor, never a command (spec Part 2 settlement 5).

What every test file of this story sets up, once, the way Story 3's `commands/asks.test.ts` does (`internal/web/ui/src/commands/testkit.ts` holds `makeSession`, `seed`, `resetFleet` and `noticesOk`; reuse them, copy nothing):

- `import "@/commands";` (registers the session, ask and job commands the buttons name), and the `vi.mock("@/app/ConfirmSheet", …)` of Story 3's tests wherever a test clicks a button whose command has a sheet (`ask.chat`, `ask.deny`, `ask.allow` with `persists`): `askConfirm` becomes a `vi.fn` that the test resolves with the sheet's value (`""`, a typed text) or `null` for a cancel.
- `beforeEach`: `vi.spyOn(toastManager, "add").mockImplementation(() => "toast")` (`@/components/ui/toast`), `window.history.replaceState(null, "", "#/s/2")` (a `CommandButton` reads its session from the route), and `seed([...])` with the session under test given id 2 and the status and group a test needs; `afterEach`: `vi.restoreAllMocks()`, `vi.mocked(askConfirm).mockReset()` where mocked, `resetFleet()`, `useDrafts.getState().clear()`, `window.history.replaceState(null, "", window.location.pathname)`.
- The asks under test are the canned snapshot's: `allSessions(fixtureSnapshot()).find((s) => s.id === 2)?.asks`, the ask whose `id` is `"fixture-permission"` (tool `Bash`, a mono `command` block and a `description` block, one suggestion that persists and one that does not) and the one whose `id` is `"fixture-question"` (asked by the agent type `recon`, which `Ask.agent` carries, not an id; its first question single-select with a `preview` on one option, its second multi-select). A test reads every label, question text and index it needs from those asks (`options.find((o) => o.preview)`, `suggestions.findIndex((g) => g.persists === true)`), never hard-codes one, so a change to the fixture's wording leaves the tests standing.
- A request is observed by `vi.spyOn(api, "send")` or `vi.spyOn(api, "answer")` (`@/api/request`) resolving `noticesOk()`; "answers" below means that spy was called with the session id 2, the ask's id where there is one, and the body named.

### Task 7.1: The reply box

**Files:**
- Create: `internal/web/ui/src/conversation/ReplyBox.tsx`
- Create: `internal/web/ui/src/conversation/ReplyBox.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/ui/src/conversation/ReplyBox.test.tsx`, drawing `<ReplyBox session={session} />` with the session `seed` holds (id 2; `makeSession({ id: 2, status: "Running", group: "working" })` unless a test says otherwise; the snapshot's `quick_replies` are the canned `yes`, `continue`, `/compact`, `/clear`). Each test below is one `it`:

- `draws a textarea labelled Reply with the placeholder "reply, or /command…", a Send button and the quick replies as a group named Quick replies while it is empty`: asserts `getByRole("textbox", { name: "Reply" })` with `placeholder` `reply, or /command…`, a `button` named `send`, and, inside `getByRole("group", { name: "Quick replies" })`, a button for each of the four canned replies. Fails without: `ReplyBox` drawing no chips (the group is missing), or reading `quick_replies` from anywhere but the snapshot.
- `a quick reply fills the box and the chips go`: click the `/compact` chip; the textbox's value is `/compact`, the draft `useDrafts.getState().replies[2]` is `/compact`, and the group `Quick replies` is gone. Fails without: the chip not calling `setReply`, or the chips drawn without the `text === ""` condition (the group stays).
- `keeps the draft across a remount`: type `half a thought` (`userEvent.type`), `unmount()`, render again; the textbox's value is `half a thought`. Fails without: the text held in the component's own `useState` instead of `useDrafts` (the remounted box is empty).
- `an emptied box deletes the draft, and a different session's draft is its own`: type `x`, clear the box (`userEvent.clear`), `useDrafts.getState().replies` has no key `2`; set `useDrafts.getState().setReply(3, "other")` first and it is still there after. Fails without: `ReplyBox` writing under a fixed key or not writing `""` through `setReply`.
- `send is off for an empty or blank reply and on for text`: the `send` button is disabled at first and with `"   "` typed (`fireEvent.change`), enabled with `yes`. Fails without: `disabled` not holding `reply === ""` on the trimmed text (the blank reply enables it) or not holding it at all.
- `send sends the typed text through session.send and a 200 empties the box`: `vi.spyOn(api, "send").mockResolvedValue(noticesOk())`; set the box to `go on` (`fireEvent.change`), click `send`; `api.send` was called with `(2, { text: "go on" })` and the textbox's value becomes `""` (`await waitFor`). The trim is `session.send`'s own and Story 3 tests it. Fails without: the button not carrying `{ text: reply }` as the command's args (`api.send` gets an empty text), or the box holding its own text so the 200's `setReply(2, "")` does not empty it.
- `a reply of exactly 64 KiB can be sent and one byte more cannot`: `fireEvent.change` to `"a".repeat(MAX_MESSAGE_BYTES)`: `send` is enabled and no `Too long` text is drawn; to `"a".repeat(MAX_MESSAGE_BYTES + 1)`: `send` is disabled and `Too long: a reply is at most 64 KiB.` is drawn. Fails without: `>` written `>=` (the exact limit turns Send off), or the line not drawn.
- `the limit counts UTF-8 bytes, not characters`: `"é".repeat(32769)` is 32,769 characters and 65,538 bytes: `send` is disabled and the too-long line is drawn. Fails without: `utf8Length` replaced by `.length`.
- `says to resume while the session's group is paused`: `seed` a session with `status: "Paused"`, `group: "paused"`; the text `Resume the session to reply.` is drawn and there is no textbox. Fails without: the paused branch (nothing is drawn, or the box is).
- `draws nothing for a session that cannot take a reply`: a session with `status: "Loading"`, `group: "working"` draws an empty container (`container` has no children with text, no textbox and no button). Fails without: the `sessionActions(session).send` check (the box draws for a busy session).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/conversation/ReplyBox.test.tsx`
Expected: FAIL: `Failed to resolve import "./ReplyBox" from "src/conversation/ReplyBox.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/conversation/ReplyBox.tsx` exporting `function ReplyBox(props: { session: Session }): ReactElement` (contract: Story 7), after the classic `reply.mjs`'s `ReplyBox`:

- Reads `sessionActions(session)` (`@/commands/session`). While `send` is false it draws a `<p>` of muted text (`text-[12.5px] text-muted-foreground`) `Resume the session to reply.` when `session.group === "paused"`, else an empty fragment (`<></>`, which is a `ReactElement`). Every hook is called before that branch.
- Its text is the session's draft, with no state of its own: `useDrafts((s) => s.replies[session.id] ?? "")`, written through `useDrafts.getState().setReply(session.id, value)` on each change (the empty string deletes the entry, `setReply`'s own rule). `reply` is the text trimmed; `tooLong` is `utf8Length(reply) > MAX_MESSAGE_BYTES` (`@/lib/utf8`, Story 2).
- Quick replies: `useFleet((s) => s.snapshot?.quick_replies) ?? []`, drawn only while the draft is `""` and the list is not empty, as `<div role="group" aria-label="Quick replies">` of `Button`s (`variant="secondary"`, `size="sm"`, text the reply) whose click calls `setReply(session.id, q)`; a chip only edits the draft, so it is not a command.
- The row: Story 2's `Textarea` (`rows={1}`, `aria-label="Reply"`, `placeholder="reply, or /command…"`, `value`, `onChange`; the Textarea already sets 16px text and grows to 120px) and `<CommandButton id="session.send" args={{ text: reply }} label="send" disabled={reply === "" || tooLong} />` (`@/app/CommandButton`; its `disabled` joins the command's own enablement, Story 2). Enter in the textarea is a newline, as in the classic box.
- When `tooLong`, a `<p role="alert">` in `text-[12px] text-error`: `Too long: a reply is at most 64 KiB.`
- The send's 200 clearing the draft, the trim, and the 202 keeping it are `session.send`'s (Story 3); this component only draws the draft.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/conversation/ReplyBox.test.tsx`
Expected: PASS, typecheck clean and the ten tests of `ReplyBox.test.tsx` pass.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/conversation/ReplyBox.tsx internal/web/ui/src/conversation/ReplyBox.test.tsx
git commit -m "feat(web): the reply box with quick replies and a draft kept across tabs" -- internal/web/ui/src/conversation/ReplyBox.tsx internal/web/ui/src/conversation/ReplyBox.test.tsx
```

### Task 7.2: The asks and the permission card

**Files:**
- Create: `internal/web/ui/src/conversation/Asks.tsx`
- Create: `internal/web/ui/src/conversation/Asks.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/ui/src/conversation/Asks.test.tsx`, drawing `<Asks session={session} />` over `seed([session])` with `session = makeSession({ id: 2, status: "Prompting", group: "needs_you", asks: <the asks a test names> })` (the canned asks, read as the setup above says). This task's tests cover the cards' frame and the permission card; Task 7.3 adds the question form's to the same file. Each test is one `it`, inside `describe("Asks")` and its nested `describe("a permission ask")`:

- `draws a card per ask in order, and nothing for a session with no asks`: with both canned asks in the order `[question, permission]`, `container.querySelectorAll("[data-ask]")` holds two elements whose `data-ask` values are `"fixture-question"` and `"fixture-permission"` in that order (every card carries `data-ask={ask.id}`); with `asks: []` and with `asks: null` the container is empty. Fails without: `Asks` mapping nothing or reordering, or drawing a wrapper for no asks.
- `names the subagent that asked`: the canned question ask, whose `agent` is the agent type, is drawn with the text `asked by recon`; the permission ask, whose `agent` is `""`, has no `asked by` text. Fails without: the `asked by` line dropped, or drawn for an empty `agent`.
- `an ask of a kind this build does not know draws a neutral card that names it and has no answer buttons`: the canned permission ask with `kind` set to `"plan"` (`as Ask["kind"]`) draws the text `plan` and a sentence that loom cannot answer it here (`This kind of ask is answered in Claude.`), and the container has no `button`. Fails without: the unknown branch falling into the permission card (it draws `Allow` and `Deny…`, the answer buttons whose Allow would answer an ask loom does not understand).
- `a permission card draws the tool, each input block, a mono block as preformatted text and a cut block marked`: the tool `Bash` is drawn; each block's `label` is drawn, the `mono: true` block's text inside a `pre` (`closest("pre")` is not null), and a non-mono block's text is not in a `pre`; a block `{ label: "command", text: "ls", mono: true, cut: true }` added to the ask's `input` draws `command (cut)`. Fails without: the mono branch (every block in `p`, or every one in `pre`), or the `(cut)` mark.
- `Allow answers allow: true with no suggestion`: click the `Allow` button: `api.answer` was called with `(2, "fixture-permission", { allow: true })`, with no `suggestion` key (`not.toHaveProperty("suggestion")` on the body). Fails without: the Allow button built from `allowArgs(ask, 1)` instead of `allowArgs(ask, 0)` (the body carries `suggestion: 1`).
- `a suggestion that does not persist answers at once with its 1-based number`: click the button named by the non-persisting suggestion's label: `askConfirm` was not called and `api.answer` was called with `(2, "fixture-permission", { allow: true, suggestion: n })` with `n` that suggestion's index plus one. Fails without: the card building the suggestion's args with a 0-based index (the number is off by one), or with `persists` true (the sheet opens).
- `a suggestion that persists asks first: cancelled sends nothing, confirmed sends its number`: with `askConfirm` resolving `null`, click the persisting suggestion's button: `api.answer` was not called; with `askConfirm` resolving `""`, click it again: `api.answer` was called with `(2, "fixture-permission", { allow: true, suggestion: m })`, `m` its index plus one. Fails without: the card building the suggestion's args with `persists` false (no sheet, the answer is sent at once, so the cancelled case sends).
- `Deny sends the typed message trimmed, and nothing when it is cancelled`: with `askConfirm` resolving `"  use ls instead  "`, click `Deny…`: `api.answer` was called with `(2, "fixture-permission", { allow: false, message: "use ls instead" })`; with it resolving `null` (reset the spy first) `api.answer` is not called. Fails without: the card not drawing a Deny button that runs `ask.deny` with `{ ask: ask.id }`, or an Allow in its place.
- `an answering card is off and says Sending…`: the permission ask with `answering: true` draws `Sending…` and every button (`Allow`, each suggestion, `Deny…`) is disabled. Fails without: `disabled={ask.answering}` left off any one of the buttons, or the `Sending…` line.
- `a card's buttons are off while the snapshot is stale`: `useFleet.setState({ stale: true })`: the `Allow` button is disabled. Fails without: the buttons not being `CommandButton`s (a hand-wired button acts on a stale fleet).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/conversation/Asks.test.tsx`
Expected: FAIL: `Failed to resolve import "./Asks" from "src/conversation/Asks.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/conversation/Asks.tsx` exporting `function Asks(props: { session: Session }): ReactElement` (contract: Story 7), after the classic `asks.mjs`:

- `Asks` draws `<div className="flex flex-col gap-3">` of one `AskCard` per `session.asks ?? []`, keyed by `ask.id`, in order; an empty or null list draws `<></>`.
- `AskCard({ ask })` (private): a bordered card with `data-ask={ask.id}` (`rounded-lg border border-border bg-card p-3`) holding, when `ask.agent !== ""`, `<p className="text-[12px] text-muted-foreground">asked by {ask.agent}</p>`, then by kind: `"question"` draws Task 7.3's `QuestionAsk` and `"permission"` draws `PermissionAsk`; any other kind draws `UnknownAsk` (compare `ask.kind` with the two literals and let the rest fall through; `kind` is typed as a closed union, so draw it with `String(ask.kind)`). Under the body, `Sending…` (`text-[12px] text-muted-foreground`) while `ask.answering`.
- `off` for an ask's inputs is `ask.answering || useLink().stale` (`@/store/fleet`); every button is a `CommandButton` with `disabled={ask.answering}`, so the command path's stale gate and Story 2's `disabled` carry the rest.
- `PermissionAsk({ ask })`: the tool (`ask.tool`, `font-mono text-[13px]`); each of `ask.input ?? []` as a block: the `label` in muted 12px text, with ` (cut)` appended when `block.cut`, then the `text` in a `<pre className="overflow-x-auto whitespace-pre-wrap rounded-md bg-background p-2 font-mono text-[12.5px]">` when `block.mono`, else a `<p className="text-[14px]">`. Then a `flex flex-col gap-2` column of `CommandButton`s, each full width (`[&>button]:w-full` on the column), in this order: `<CommandButton id="ask.allow" args={allowArgs(ask, 0)} label="Allow" disabled={ask.answering} />`; one per `ask.suggestions ?? []` at index `i`: `<CommandButton id="ask.allow" args={allowArgs(ask, i + 1)} label={g.label} variant="secondary" disabled={ask.answering} />` (a button's `key` is `i`); and `<CommandButton id="ask.deny" args={{ ask: ask.id }} label="Deny…" variant="destructive" disabled={ask.answering} />`. `allowArgs` decides `persists`, so the "Save this rule?" sheet is the command's, never drawn here.
- `UnknownAsk({ kind })`: a neutral paragraph (`text-[13px] text-muted-foreground`): the kind in `font-mono`, then `This kind of ask is answered in Claude.` No button: the way in is `NeedsYou`'s Open in Claude (Task 7.4).
- Until Task 7.3, the `"question"` kind draws `UnknownAsk` too: a temporary stub that Task 7.3 replaces with `QuestionAsk`. No test of this task depends on what the question card draws beyond its `data-ask` element and its `asked by` line, which both cards carry.

Import `allowArgs` from `@/commands/asks`.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/conversation`
Expected: PASS, typecheck clean and every test of `Asks.test.tsx` and `ReplyBox.test.tsx`.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/conversation/Asks.tsx internal/web/ui/src/conversation/Asks.test.tsx
git commit -m "feat(web): the asks, the permission card and a neutral card for an unknown kind" -- internal/web/ui/src/conversation/Asks.tsx internal/web/ui/src/conversation/Asks.test.tsx
```

### Task 7.3: The question form

**Files:**
- Modify: `internal/web/ui/src/conversation/Asks.tsx`
- Modify: `internal/web/ui/src/conversation/Asks.test.tsx`

- [ ] **Step 1: Write the failing tests**

Add to `Asks.test.tsx` a `describe("a question ask")` over the canned question ask (`Q0` its first question, single-select with a previewed option; `Q1` its second, multi-select; read the texts and labels from the ask). Two helpers of the file: `radio(label)` and `checkbox(label)` are `getByRole("radio" | "checkbox", { name: label })` (each input carries `aria-label={option.label}`), and `draftOf()` is `useDrafts.getState().asks["fixture-question"]`. Each test is one `it`:

- `draws a radio per option of a single-select question and a checkbox per option of a multi-select one`: every option of `Q0` is a `radio` and none a `checkbox`; every option of `Q1` is a `checkbox`; each question's `header` and text are drawn, and an option's `description` when it has one. Fails without: the `q.multi` switch of the input type (all radios, or all checkboxes).
- `picking another option of a single-select question replaces the pick`: pick `Q0`'s first option, then its second: the second radio is checked, the first is not, and `draftOf().answers[Q0].labels` is the second label alone. Fails without: the single-select pick appending to `labels` instead of replacing them.
- `a multi-select keeps its picks in the order picked, and unpicking removes one`: check `Q1`'s second option, then its first: `draftOf().answers[Q1].labels` is `[second, first]`; uncheck the second: it is `[first]`. Fails without: the pick sorted into option order, or the uncheck not removing the label.
- `typing Other on a single-select question clears its pick, picking clears Other, and a multi-select keeps both`: pick `Q0`'s first option, type `mine` in the input labelled `Other answer to <Q0>`: the radio is unchecked and `labels` is `[]`; pick the option again: the Other input is `""`. For `Q1`, check an option and type in its Other input: the checkbox stays checked and `other` is `mine`. Fails without: `other()` clearing `labels` for a multi-select, or `pick()` not clearing `other` for a single-select.
- `shows the preview of the last picked option that has one`: pick `Q0`'s option that has a `preview`: its text is drawn in a `pre`; pick another option of `Q0` (none of the others has a preview): no `pre` of a preview. Then for `Q1`, if one of its options has a preview, check it, then check one without: the preview stays. If `Q1` has no preview in the fixture, this second half builds its own ask (`{ ...ask, questions: [...] }` with a previewed first option and a plain second) and draws it. Fails without: the preview derived from the first label instead of the last previewed one, or kept after a single-select pick of an option without one.
- `Answer is off until every question has a pick or an Other text`: with nothing picked `Answer` is disabled; picking `Q0` only: still disabled; Other text in `Q1` (not a pick): enabled; clearing it: disabled again. A whitespace-only Other does not count. Fails without: `disabled={body === null}` (Answer always on), or `answerBody` bypassed for a check of the form's own (a blank Other enables it).
- `Answer sends the body answerBody builds and a 200 drops the draft`: pick `Q0`'s first option, type `x` in `Q1`'s Other and `  n  ` in `Q1`'s note (`Note on <Q1>`), click `Answer`: `api.answer` was called with `(2, "fixture-question", { answers: { [Q0]: { labels: [o0] }, [Q1]: { labels: [], other: "x", note: "n" } } })`, and once it resolves the radio is unchecked again (`await waitFor`) and `draftOf()` is `undefined`. Fails without: the form sending anything but `answerBody(ask, draft)`'s result (an untrimmed note, a `chat` field), or the `ask.answer` button's args not being `{ ask: ask.id, body }` (the draft is not dropped).
- `an Other text or a note past 64 KiB says so and turns Answer off`: with `Q0` picked and `Q1`'s Other set to `"a".repeat(MAX_MESSAGE_BYTES)`, `Answer` is enabled and no `Too long` text; at `MAX_MESSAGE_BYTES + 1` the line `Too long: an answer is at most 64 KiB.` is drawn and `Answer` is disabled; the same for a note. Fails without: the line not drawn, or the limit not checked on the note (the note's case stays enabled).
- `an Other of exactly 64 KiB plus a trailing space says Too long and turns Answer off`: with `Q0` picked and `Q1`'s Other set to `"a".repeat(MAX_MESSAGE_BYTES) + " "` (65,537 bytes untrimmed, 65,536 trimmed), `Too long: an answer is at most 64 KiB.` is drawn and `Answer` is disabled. Fails without: `tooLong` dropped from `Answer`'s `disabled` condition (`answerBody` trims, so it is not null and Answer stays enabled beside the line).
- `Chat about this sends the sheet's text and nothing when the sheet is cancelled`: with `askConfirm` resolving `"what does x mean"`, click `Chat about this`: `api.answer` was called with `(2, "fixture-question", { chat: true, chat_text: "what does x mean" })`; with `null` it is not called. Fails without: the button not running `ask.chat` with `{ ask: ask.id }`.
- `keeps the picks across a remount`: pick `Q0`'s first option and type `kept` in `Q1`'s note, `unmount()`, render again: the radio is checked and the note's value is `kept`. Fails without: the picks held in component state instead of `useDrafts` (the remounted form is empty).
- `the inputs and both buttons are off while the ask is answering, and the inputs while the snapshot is stale`: with `answering: true`, every radio, checkbox, text input, `Answer` and `Chat about this` is disabled; with `useFleet.setState({ stale: true })` and `answering: false` the radios and text inputs are disabled. Fails without: `off` left out of the fieldset's `disabled`, or `stale` left out of `off`.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/conversation/Asks.test.tsx`
Expected: FAIL: the question tests fail on `Unable to find an accessible element with the role "radio"` (the stub draws the neutral card), the Task 7.2 tests still pass.

- [ ] **Step 3: Implement**

In `internal/web/ui/src/conversation/Asks.tsx` replace Task 7.2's stub (the `"question"` kind drawing `UnknownAsk`) with `QuestionAsk({ ask, off })` after the classic `asks.mjs`'s `QuestionAsk`, and import `answerBody` beside `allowArgs`:

- The draft is `useDrafts((s) => s.asks[ask.id])`; a missing one is the empty draft, `{ answers }` with `{ labels: [], other: "", note: "" }` for each of `ask.questions ?? []` by `question.question`. Every change reads the current draft with `useDrafts.getState().asks[ask.id] ?? <empty>`, replaces one question's entry and writes the whole draft with `setAsk(ask.id, next)`; the form holds no other state.
- Picks: a single-select pick sets `labels: [label], other: ""`; a multi-select pick appends the label (or removes it when already in `labels`), so `labels` stays in pick order. Typing Other sets `other` and, for a single-select question with a non-empty text, `labels: []`; for a multi-select one `labels` stays. Typing a note sets `note` only. `checked` is `labels.includes(label)`.
- Each question is a `<fieldset disabled={off}>` (`flex flex-col gap-2`; `border-0 p-0`) with a `<legend>` of the `header` chip (`Badge`, `variant="neutral"`) and the question text; each of `question.options ?? []` is a `<label className="flex min-h-11 items-start gap-2">` of `<input type={question.multi ? "checkbox" : "radio"} name={`${ask.id}\u0000${question.question}`} aria-label={option.label} checked onChange className="mt-1 accent-primary" />` and a text block of the label and, when it has one, the `description` (muted, 12.5px). No `style` attribute.
- The preview: of the question's picked `labels`, the last one whose option has a non-empty `preview` (search `labels` from the end); drawn in a `<pre>` as Task 7.2's mono block is.
- Two text `<input type="text">` (a plain element with the Textarea's classes: `min-h-11 w-full rounded-lg border border-input bg-background px-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50`), `placeholder="Other…"` with `aria-label={`Other answer to ${question.question}`}` and `placeholder="Note (optional)"` with `aria-label={`Note on ${question.question}`}`.
- `tooLong` is true when any draft answer's `other` or `note` has `utf8Length(...) > MAX_MESSAGE_BYTES` (untrimmed, as the classic form counts them, so a trailing space can show it while `answerBody` is not null); then `<p role="alert" className="text-[12px] text-error">Too long: an answer is at most 64 KiB.</p>`.
- `const body = answerBody(ask, stored draft)`. Buttons, side by side (`flex gap-2`): `<CommandButton id="ask.answer" args={{ ask: ask.id, body: body ?? {} }} label="Answer" disabled={ask.answering || body === null || tooLong} />` (`AnswerBody` has only optional fields, so `{}` types and is never sent, the button being off for null; off for `tooLong` too, as the classic Answer is, so the line and the button never disagree) and `<CommandButton id="ask.chat" args={{ ask: ask.id }} label="Chat about this" variant="secondary" disabled={ask.answering} />`. Dropping the draft after a 200 and the Chat sheet are the commands'.
- `AskCard` passes `off`: the `ask.answering || stale` of Task 7.2.

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/conversation`
Expected: PASS, typecheck clean, every test of `Asks.test.tsx` (both describes) and `ReplyBox.test.tsx`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the question form over the ask's draft and answerBody" -- internal/web/ui/src/conversation/Asks.tsx internal/web/ui/src/conversation/Asks.test.tsx
```

### Task 7.4: The needs-you box

**Files:**
- Create: `internal/web/ui/src/conversation/NeedsYou.tsx`
- Create: `internal/web/ui/src/conversation/NeedsYou.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/ui/src/conversation/NeedsYou.test.tsx`, drawing `<NeedsYou session={session} />` over `seed([session])`, `session = makeSession({ id: 2, status: "Prompting", group: "needs_you", remote_url: "https://claude.ai/code/webshot2", wait: { reason: "Allow Bash", detail: "go test ./ui/..." }, asks: [] })` unless a test says otherwise. Each test is one `it`:

- `says why the session waits: its reason, else asks: and its gist, else Needs you`: with the reason `Allow Bash` that text is drawn; with `wait.reason` `""` and `gist: "pick a plan"` the text `asks: pick a plan` is drawn; with both empty `getAllByText("Needs you")` has two entries (the legend and the line) where the first two cases have one. Fails without: the `reason || gist || "Needs you"` order (any one of the three cases misdraws).
- `draws the wait's detail only while there are no asks`: with no asks the detail text is drawn in a `pre`; with the canned permission ask in `asks` it is not. Fails without: the `asks.length === 0` condition (the detail draws under the asks).
- `draws the asks between the reason and the link`: with the canned question ask, its first question's text is drawn, and its position in the box's text follows the reason's and precedes `Open in Claude` (`compareDocumentPosition`). Fails without: `NeedsYou` not drawing `Asks`.
- `Open in Claude is a link to remote_url that opens a new tab without an opener or a referrer`: `getByRole("link", { name: "Open in Claude" })` has `href` the session's `remote_url`, `target` `_blank` and a `rel` holding `noopener` and `noreferrer`; there is no `button` of that name. Fails without: a `button` or a command in place of the anchor, `target` or either `rel` word missing.
- `Open in Claude is the primary action when the session has no asks and secondary when it has`: with `asks: []` the link has the class `bg-primary`; with the canned permission ask it has `bg-secondary` and not `bg-primary`. Fails without: the `buttonVariants` variant not following `asks.length`.
- `says Remote Control is off when there is no remote_url, and draws no link`: `remote_url: ""` draws `Remote Control is off for this session.` and no `link`. Fails without: the empty `remote_url` still drawing an anchor, or the sentence.
- `an unknown ask kind still leaves the way into Claude`: the canned permission ask with `kind` `"plan"` (`as Ask["kind"]`) draws the neutral card (`This kind of ask is answered in Claude.`), no `Allow` button, and the `Open in Claude` link. Fails without: `NeedsYou` hiding the link whenever there are asks.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/conversation/NeedsYou.test.tsx`
Expected: FAIL: `Failed to resolve import "./NeedsYou" from "src/conversation/NeedsYou.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/conversation/NeedsYou.tsx` exporting `function NeedsYou(props: { session: Session }): ReactElement` (contract: Story 7), after the classic `needs.mjs`:

- `<section aria-label="Needs you">` in a bordered box (`flex flex-col gap-3 rounded-xl border border-attention/30 bg-attention/10 p-3`): a legend `<div className="text-[12px] font-semibold text-attention">` of `<span aria-hidden="true">{groupGlyph("needs_you")}</span> Needs you` (`groupGlyph` is Story 6's, `@/session/facts`); the reason line, `session.wait.reason`, else `asks: ${session.gist}` when `gist` is set, else `Needs you`; the wait's `detail` in a `pre` (the mono block's classes of Task 7.2) only when it is non-empty and `session.asks` is empty or null; `<Asks session={session} />`; and Open in Claude.
- Open in Claude: with `session.remote_url !== ""`, `<a href={session.remote_url} target="_blank" rel="noopener noreferrer" className={cn(buttonVariants({ variant: asks ? "secondary" : "default", size: "lg" }), "w-full")}>Open in Claude</a>` (`buttonVariants` from `@/components/ui/button`, `cn` from `@/lib/utils`; `asks` is "the session holds at least one ask"); else `<p className="text-[12.5px] text-muted-foreground">Remote Control is off for this session.</p>`. It is an anchor, never a command or a `window.open` (spec Part 2 settlement 5).

- [ ] **Step 4: Run it to see it pass**

Run: `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui exec vitest run src/conversation`
Expected: PASS, typecheck clean and every test of the three files of this story.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/conversation/NeedsYou.tsx internal/web/ui/src/conversation/NeedsYou.test.tsx
git commit -m "feat(web): the needs-you box with its asks and the way into Claude" -- internal/web/ui/src/conversation/NeedsYou.tsx internal/web/ui/src/conversation/NeedsYou.test.tsx
```

### Task 7.5: Rebuild the bundle

**Files:**
- Modify: `internal/web/dist/`

- [ ] **Step 1: Rebuild the bundle**

Run: `pnpm -C internal/web/ui run build`
Expected: vite prints the built assets and exits 0. `NeedsYou`, `Asks` and `ReplyBox` are imported by no panel yet (Story 8 draws them), so the bundle may not change; when `git status --short internal/web/dist` lists nothing, there is nothing to commit and the task ends here.

- [ ] **Step 2: Run the story's gates**

Run: `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
If `format:check` flags this story's files, run `pnpm -C internal/web/ui exec prettier --write src/conversation/NeedsYou.tsx src/conversation/NeedsYou.test.tsx src/conversation/Asks.tsx src/conversation/Asks.test.tsx src/conversation/ReplyBox.tsx src/conversation/ReplyBox.test.tsx`, rerun the gates and commit the formatting with the files.
Expected: `check` passes (typecheck, lint, format check, every Vitest test, the worker's syntax check), `dist:check` prints that the committed bundle is fresh and `go test` prints `ok` for `internal/web`.

- [ ] **Step 3: Commit**

When Step 1 or the formatting changed files:

```bash
git add -N internal/web/dist
git commit -m "feat(web): rebuild the bundle with the needs-you box, the asks and the reply box" -- internal/web/dist internal/web/ui/src/conversation
```

## Story 8: The conversation panel

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/turns/index.tsx`, `internal/web/ui/src/panels/turns/Conversation.tsx`, `internal/web/ui/src/panels/turns/turns.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
- **Interfaces:** exposes the `turns` panel and `Conversation`; uses Story 4's `useTurnPages` (with `landed`), `keptScroll` and `keepScroll`, Story 1's `fixtureTurns`, part 1's `fixtureSnapshot`, `Panel` and `PanelProps`, Story 5's `TurnList`, Story 6's `SessionHead`, `FactStrip`, `NoSession` and `useMarkSeen`, Story 7's `NeedsYou` and `ReplyBox`, part 1's `definePanel`, `panelFor` and `useSession`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens conversation,waiting -out "$d"'`
- **Review:** deep
- **Form:** brief (a port of `turns.mjs`'s `Conversation` over Story 4's reads)

Register the `turns` panel and port the classic `Conversation` (`internal/web/static/js/turns.mjs:310-392`, its page handling `:205-300`) over Story 4's `useTurnPages`: the unreadable-records notice, `↑ earlier turns`, loading and error with retry, "No turns yet.", Story 5's `TurnList` (live while the session is Running), the refresh error with its retry, Story 7's `NeedsYou` while the group is needs_you, and Story 7's `ReplyBox`. The component refreshes when the stream changes the session's status or last message (never on mount) and every 5 s while the session is Running (the `live` option of `useTurnPages`), anchors the page to the bottom when a first page lands, a reset page lands, or a refresh lands while the reader is within 48 px of the bottom, keeps the reader's place after an earlier page, restores a kept conversation's scroll before the first paint, and marks the session seen through Story 6's `useMarkSeen`. The panel's `Body` draws `NoSession` for a session the snapshot no longer lists, `Conversation` for a Claude session, and for any other program the `info` panel's `Body` through `panelFor("info")` (`NoSession` while no `info` panel is registered: Story 9 registers it). No code of this story invents a read, a body or an enablement rule: every read is `useTurnPages`'s and every action a command of Story 3 reached through Story 7's components. None of its code holds a landmine marker, so the developer runs at the default effort.

Rules every task keeps (the app's `internal/web/ui/CLAUDE.md`): components return `ReactElement`; Claude's and the daemon's strings are drawn as text nodes, never as markup, and no `style` attribute is written (the CSP drops it); every colour is a token class (`text-error`, `text-muted-foreground`, …), never a literal; touch targets come from `Button` (its 44px hit area) or a `min-h-11` row; an unused parameter is never `_`-prefixed (ESLint rejects it: type a mock as `vi.fn<(a: A, b: B) => R>(…)`); nothing assigns `window.location.hash` (the repo's ESLint rejects it: navigate with `window.location.assign`). Every command below runs from the repo root. A task's test command is `pnpm -C internal/web/ui exec vitest run src/panels/turns/turns.test.tsx`; the type and lint check after each implementation is `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`. `internal/web/dist/` is rebuilt once, in the last task, so `pnpm run dist:check` is stale between tasks 8.1 and 8.2 (no story gate reads it; the Go tests read the committed bundle, which still passes its budget tests).

### Task 8.1: `Conversation`: its drawn parts

**Files:**
- Create: `internal/web/ui/src/panels/turns/Conversation.tsx`
- Create: `internal/web/ui/src/panels/turns/turns.test.tsx` (tasks 8.2 and 8.3 add to it)

Behaviour and names (contract: `Conversation`; Story 4's `useTurnPages`, Story 5's `TurnList`, Story 7's `NeedsYou` and `ReplyBox`, part 1's `Button`):

- [ ] **Step 1: Write the failing tests**

Create `internal/web/ui/src/panels/turns/turns.test.tsx`. Its header and helpers, shared by all three tasks (it fakes the timers so a poll's cadence is exact: every wait is a `settle`, which also lets the query client's own zero-delay notifications run; `@/commands` registers `session.send`, which `ReplyBox`'s button names; the pages go through the real `useTurnPages` and `queryClient`, with only `api.turns` stubbed):

```tsx
import { QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Outcome, Session, Turn, Turns } from "@/api/gen";
import { queryClient } from "@/api/queries";
import { api } from "@/api/request";
import "@/commands";
import { deferred, makeSession, resetFleet, seed } from "@/commands/testkit";
import { fixtureTurns } from "@/fixtures";
import { useSession } from "@/store/fleet";

import { Conversation } from "./Conversation";

const NOW = Date.parse("2026-11-19T12:00:00Z");

const spyOnTurns = () =>
  vi.spyOn(api, "turns").mockRejectedValue(new Error("unexpected turns read"));
let turnsRead: ReturnType<typeof spyOnTurns>;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  queryClient.clear();
  turnsRead = spyOnTurns();
  window.history.replaceState(null, "", "#/s/1");
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  resetFleet();
  window.localStorage.clear();
  window.history.replaceState(null, "", window.location.pathname);
});

async function settle(ms = 1): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

const ok = <T,>(value: T): Outcome<T> => ({ kind: "ok", status: 200, value, notices: [] });
const refused = (status: number, message: string): Outcome<never> => ({
  kind: "refused",
  status,
  message,
});

function turn(id: number): Turn {
  return {
    id,
    at: "2026-11-19T10:00:00Z",
    from: "you",
    name: "",
    text: `prompt ${id}`,
    cut: false,
    command: false,
    partial: false,
    items: null,
  };
}

function page(ids: number[], extra: Partial<Turns> = {}): Turns {
  return {
    conversation: "c1",
    turns: ids.map(turn),
    earlier: null,
    reset: false,
    skipped: 0,
    ...extra,
  };
}

// The session on screen is the store's, so a test changes it as the stream would.
let current: Partial<Session> = {};

function sessionNow(): Session {
  return makeSession({
    id: 1,
    claude: true,
    remote_url: "https://claude.ai/code/webshot",
    ...current,
  });
}

function change(over: Partial<Session>): void {
  current = { ...current, ...over };
  act(() => {
    seed([sessionNow()]);
  });
}

function Screen(): ReactElement | null {
  const session = useSession(1);
  return session === undefined ? null : <Conversation session={session} />;
}

function show(over: Partial<Session> = {}) {
  current = over;
  seed([sessionNow()]);
  return render(
    <QueryClientProvider client={queryClient}>
      <Screen />
    </QueryClientProvider>,
  );
}
```

Then append the drawn-parts tests, in `describe("Conversation's drawn parts")`. Each is one `it`; a page is held by `turnsRead.mockResolvedValueOnce(ok(<page>))` before `show()` and a `await settle()` after it:

- `draws the turns, and marks only the newest working while the session runs`: the page is `fixtureTurns()`; with `show({ status: "Running", group: "working" })` the text `Its start is earlier.` (the fixture's partial newest turn) is drawn and `screen.getAllByText(/WORKING/)` has one entry; with `show({ status: "Ready", group: "your_turn" })` `screen.queryByText(/WORKING/)` is null. (The fixture's newest turn is turn 3, partial, and carries Claude's items, Story 1.)
- `says the records it could not read`: a page `page([1], { skipped: 2 })` draws `Some records could not be read.`; the same page with `skipped: 0` draws no such text.
- `offers earlier turns only while there are some, and asks for them by before`: `page([5, 6], { earlier: 5 })` draws a button named `↑ earlier turns`; clicking it (`fireEvent.click` inside `act`) with `turnsRead.mockResolvedValueOnce(ok(page([3, 4])))` then `settle()` makes the last read `(1, { conversation: "c1", before: "5" })`, draws `prompt 3`, and removes the button; a page with `earlier: null` draws no such button.
- `says Loading… until the first page lands`: `turnsRead.mockReturnValueOnce(deferred.promise)` (a `deferred<Outcome<Turns>>()` of the testkit) draws `Loading…`, no retry button and no `No turns yet.`; resolving it with `ok(page([1]))` and `settle()` removes `Loading…` and draws `prompt 1`.
- `says why the first read failed and retries it`: `turnsRead.mockResolvedValueOnce(refused(409, "session is busy"))` draws `session is busy` and a button named `retry` and no `Loading…`; with `turnsRead.mockResolvedValueOnce(ok(page([1])))` clicking `retry` and `settle()` draws `prompt 1`, and `turnsRead` was called twice, the second with `(1, {})`.
- `says there are no turns yet for an empty page`: `ok(page([]))` and `ok({ ...page([]), turns: null })` each draw `No turns yet.`; a page holding a turn draws none.
- `keeps the turns and says why a refresh failed, with its retry`: after the first page `[1, 2]`, `turnsRead.mockResolvedValueOnce(refused(504, "timed out"))` and `change({ last_message: "x" })` (the stream's refresh) with `settle()` draw `Could not refresh: timed out`, still draw `prompt 2`, and a button named `retry`; with `turnsRead.mockResolvedValueOnce(ok(page([2, 3])))` clicking it draws `prompt 3` and removes the line.
- `draws the needs-you box only while the group is needs_you`: with `show({ status: "Prompting", group: "needs_you" })` the link named `Open in Claude` is drawn (Story 7's box, the session has a `remote_url`); with `status: "Ready", group: "your_turn"` it is not.
- `ends in the reply box, or says to resume while the session is paused`: `show()` on a Ready session draws `getByRole("textbox", { name: "Reply" })`; `show({ status: "Paused", group: "paused" })` draws `Resume the session to reply.` and no textbox; `show({ status: "Loading", group: "working" })` draws neither (a busy session).

Fails without: `draws the turns, and marks only the newest working while the session runs`: `live` passed to `TurnList` as `true` always (the Ready case draws WORKING) or as `false` always (the Running case draws none). `says the records it could not read`: the `skipped` notice removed, or drawn for any `held`. `offers earlier turns only while there are some, and asks for them by before`: the button drawn without its `held.earlier` condition, or its click not calling `load("earlier")`. `says Loading… until the first page lands`: the loading text drawn with the pages held, or never drawn. `says why the first read failed and retries it`: the retry calling `load("earlier")` or `load("refresh")` in place of `load("newest")`, or the error text dropped for `Loading…`. `says there are no turns yet for an empty page`: the `turns.length === 0` test removed, or written on `held` alone (an empty page then draws nothing), or a null `turns` read as non-empty. `keeps the turns and says why a refresh failed, with its retry`: the refresh error drawn only while no page is held. `draws the needs-you box only while the group is needs_you`: `NeedsYou` drawn for every group. `ends in the reply box, or says to resume while the session is paused`: `ReplyBox` left out, or drawn only under `sessionActions(session).send` (the paused text, which is `ReplyBox`'s, is lost).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/turns/turns.test.tsx`

Expected: FAIL: `Failed to resolve import "./Conversation" from "src/panels/turns/turns.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/panels/turns/Conversation.tsx` exporting `function Conversation(props: { session: Session }): ReactElement` (contract: Story 8), after the classic `Conversation` in `turns.mjs:310-392`. It draws one `<section className="flex flex-col gap-3 px-4 py-3">` holding, in this order, each only when its condition holds:

1. `Some records could not be read.` (`text-xs text-muted-foreground`) when `held !== undefined && held.skipped > 0`.
2. A `Button` (`variant="ghost"`, `size="sm"`) `↑ earlier turns` whose click calls `load("earlier")`, when `held !== undefined && held.earlier` is truthy (a page id, never 0).
3. While `held === undefined`: a `<div>` holding a `<p>` of the error text in `text-sm text-error` when `error !== ""`, else `Loading…` in `text-sm text-muted-foreground`; and, when `error !== ""`, a `Button` (`variant="outline"`) `retry` calling `load("newest")`.
4. `No turns yet.` (`text-sm text-muted-foreground`) when `held !== undefined` and its `turns` is empty.
5. `<TurnList turns={held?.turns ?? []} sessionId={session.id} live={working} remote={session.remote_url} />`.
6. When `held !== undefined && error !== ""`: a `<p className="text-xs text-muted-foreground">` `Could not refresh: <error> · ` followed by a `Button` (`variant="ghost"`, `size="sm"`) `retry` calling `load("refresh")`.
7. `<NeedsYou session={session} />` when `session.group === "needs_you"`.
8. `<ReplyBox session={session} />`, always: the box while `sessionActions` allows a send, "Resume the session to reply." while the group is paused, nothing otherwise are `ReplyBox`'s own rules (Story 7), where the classic `Conversation` branched here.

The pages: `const { held, error, load } = useTurnPages({ session: session.id, agent: undefined, live: working })`, `working` being `session.status === "Running"`. Import `useTurnPages` from `@/api/reads`, `TurnList` from `@/conversation/TurnList`, `NeedsYou` from `@/conversation/NeedsYou`, `ReplyBox` from `@/conversation/ReplyBox`, `Button` from `@/components/ui/button` and `Session` from `@/api/gen`. The classic `Conversation` also computed `now` for `TurnList` and drew "This session is gone." and "Loading…" for a missing session or snapshot: here `TurnList` reads its own clock, and the panel's `Body` (task 8.3) answers for a missing session, the shell draws its splash while the store holds no snapshot.

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/turns/Conversation.tsx src/panels/turns/turns.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/turns/turns.test.tsx'`

Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/ui/src/panels/turns/Conversation.tsx internal/web/ui/src/panels/turns/turns.test.tsx
git commit -m "feat(web): the conversation, drawn over the turn pages" -- internal/web/ui/src/panels/turns/Conversation.tsx internal/web/ui/src/panels/turns/turns.test.tsx
```

### Task 8.2: `Conversation`: refreshes, scroll and seen times

**Files:**
- Modify: `internal/web/ui/src/panels/turns/Conversation.tsx`
- Test: `internal/web/ui/src/panels/turns/turns.test.tsx`

Behaviour and names (contract: Story 4's `useTurnPages` with `landed`, `keptScroll` and `keepScroll`, Story 6's `useMarkSeen` and `readSeen`; the classic `useTurnPages` in `turns.mjs:205-300` and `Conversation`'s two effects in `:310-340`):

- [ ] **Step 1: Write the failing tests**

In `internal/web/ui/src/panels/turns/turns.test.tsx` add `type Mock` to the `vitest` import and `import { keptScroll } from "@/api/reads";` and `import { readSeen } from "@/fleet/seen";`. The page is a stub, since happy-dom lays nothing out: append this helper, which the scroll tests share (the page is `pageHeight` tall, the window `800` high and `pageY` down; `scrollTo` moves `pageY` as a browser does, without the scroll event, which a test sends with `fireEvent.scroll(window)` when the reader moves):

```tsx
let pageHeight = 2000;
let pageY = 0;
let scrollTo: Mock<(x: number, y: number) => void>;

function stubPage(): void {
  pageHeight = 2000;
  pageY = 0;
  Object.defineProperty(document.documentElement, "scrollHeight", {
    configurable: true,
    get: () => pageHeight,
  });
  Object.defineProperty(window, "scrollY", { configurable: true, get: () => pageY });
  Object.defineProperty(window, "innerHeight", { configurable: true, value: 800 });
  scrollTo = vi.fn<(x: number, y: number) => void>((...args: [number, number]) => {
    pageY = args[1];
  });
  window.scrollTo = scrollTo as unknown as typeof window.scrollTo;
}

// readerAt puts the reader at y and tells the page, as the browser's scroll event does.
function readerAt(y: number): void {
  pageY = y;
  fireEvent.scroll(window);
}
```

Then append `describe("Conversation's refreshes")` and `describe("Conversation's scroll")` (the second with `beforeEach(stubPage)`), and `describe("Conversation's seen time")`. Each test is one `it`; `show()`, `change()`, `page()` and `settle()` are the helpers above, and a page is held as in task 8.1:

- `a change of the last message or of the status refreshes the newest turn, an unrelated change does not`: the first page `[1, 2, 3]` held; `turnsRead` was called once (nothing refreshes on mount); with `turnsRead.mockResolvedValueOnce(ok(page([3, 4])))` `change({ last_message: "next" })` and `settle()` make a second read `(1, { conversation: "c1", after: "3" })` and draw `prompt 4`; with `turnsRead.mockResolvedValueOnce(ok(page([4, 5])))` `change({ status: "Prompting", group: "needs_you" })` and `settle()` make a third read; `change({ title: "renamed" })` and `settle()` leave it at three.
- `a change while the first page is in flight asks for nothing`: the first read is a `deferred`; `change({ last_message: "next" })`; resolving it with `ok(page([1, 2]))` and `settle(10)` leave `turnsRead` called once.
- `a running session is refreshed every 5 s and a ready one is not`: with `show({ status: "Running", group: "working" })` and the first page `[1]`, `turnsRead.mockResolvedValueOnce(ok(page([1, 2])))` and `settle(5000)` make a second read `(1, { conversation: "c1", after: "1" })`; with `show({ status: "Ready", group: "your_turn" })` and the first page, `settle(20_000)` leaves one read.
- `the first page lands at the bottom`: `show()` with the first page `[1, 2]` and `settle()` call `scrollTo` with `(0, 2000)`.
- `an earlier page keeps the reader's place`: the first page `page([5, 6], { earlier: 5 })` held, `readerAt(300)`; with `turnsRead.mockResolvedValueOnce(ok(page([3, 4])))` clicking `↑ earlier turns` after `pageHeight = 3200`, then `settle()`, the last `scrollTo` call is `(0, 1500)` (the reader stays 1700 px above the bottom) and no call went to `(0, 3200)`.
- `a refresh lands at the bottom only when the reader is within 48 px of it`: with the first page held, `pageHeight` 2000 and the window 800 high, `readerAt(1152)` (exactly 48 above the bottom), `scrollTo.mockClear()`, `pageHeight = 2400`, a refresh of `page([3, 4])` through `change({ last_message: "a" })` and `settle()` call `scrollTo` with `(0, 2400)`; the same with `readerAt(1151)` calls `scrollTo` not at all.
- `a reset page lands at the bottom wherever the reader is`: the first page `[1, 2]`, `readerAt(0)`, `scrollTo.mockClear()`, `pageHeight = 2600`; a refresh answered `page([9], { conversation: "c2", reset: true })` through `change({ last_message: "a" })` and `settle()` call `scrollTo` with `(0, 2600)`.
- `a conversation kept in the cache comes back where it was left`: the first page `[1, 2, 3]` held, `readerAt(640)`; `unmount()` (the render result's); `scrollTo.mockClear()`; with `turnsRead.mockResolvedValueOnce(ok(page([3, 4])))` `show()` again: `scrollTo` was called with `(0, 640)` straight after the render, and after `settle()` (the refresh landed) it was never called with `(0, 2000)`.
- `leaving the conversation stops recording its scroll`: as above to `unmount()`, then `readerAt(5)`: `keptScroll(1)` is still `640`.
- `marks the session seen on arrival and again on leaving`: `show()` with the first page and `settle()`: `readSeen()["1"]` is `NOW`; `vi.setSystemTime(NOW + 60_000)` and `unmount()`: `readSeen()["1"]` is `NOW + 60_000`.

Fails without: `a change of the last message or of the status refreshes the newest turn, an unrelated change does not`: the signature built from the status alone, or from the last message alone (one of the two changes refreshes nothing), or from the whole session (the title change refreshes), or a refresh asked on mount (the first count is two). `a change while the first page is in flight asks for nothing`: the `held !== undefined` condition removed. `a running session is refreshed every 5 s and a ready one is not`: `live` passed as `false` always (no poll) or as `true` always (the Ready case reads again). `the first page lands at the bottom`: the anchor effect removed, or its `landed === "newest"` case left out. `an earlier page keeps the reader's place`: the earlier case scrolling to the bottom, or using the height after the page landed for `fromBottom` (`(0, 3200 - 1700)` becomes `(0, 3200 - 2900)`). `a refresh lands at the bottom only when the reader is within 48 px of it`: the margin written `>` for `>=` (1152 stays), or 49 or 47 for 48 (one of the two readers is misjudged), or the refresh case always anchoring (1151 scrolls) or never anchoring. `a reset page lands at the bottom wherever the reader is`: the reset page read as a refresh (`landed` not `"newest"`, or the effect keying on `reset` nowhere). `a conversation kept in the cache comes back where it was left`: the restore effect removed, or run after the first page only, or the anchor effect scrolling to the bottom for the page already held at mount. `leaving the conversation stops recording its scroll`: the listener's cleanup removed. `marks the session seen on arrival and again on leaving`: `useMarkSeen` not called, or called with a session id other than the session's.

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/turns/turns.test.tsx`

Expected: the tests of task 8.1 pass; the new ones fail on their assertions (`expected "spy" to be called with arguments: [ 0, 2000 ]` for the scroll tests, `expected 1 to be 2` for the stream refresh, `expected undefined to be 1795089600000` for the seen time).

- [ ] **Step 3: Implement**

Edit `internal/web/ui/src/panels/turns/Conversation.tsx`. Import `keepScroll`, `keptScroll` from `@/api/reads` and `useMarkSeen` from `@/fleet/seen`, and take `landed` from `useTurnPages` beside `held`, `error` and `load`. Add, after that call and before the returned section (all hooks before any early return), in this order:

- `useMarkSeen(session.id)`: the seen time is written on arrival and again on leaving, so the fleet's NEW mark skips a turn that ended while the session was open.
- The stream's refresh. A `useRef` `seenSignature` holds `` `${session.status}\n${session.last_message}` `` as of the last render that acted; an effect over that string, `held` and `load` returns when it equals the ref, otherwise stores it and calls `load("refresh")` only when `held !== undefined`. The ref starts as the first render's string, so mounting asks for nothing. The 5 s poll is not here: it is `live`.
- The page's metrics. A private `measure()` returns `{ atBottom, fromBottom }` of the page now: `atBottom` is `window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 48` and `fromBottom` is `document.documentElement.scrollHeight - window.scrollY`. A `useRef` `metrics` holds the last measure; a reader's scroll and every commit that scrolls refresh it, so it says how the page stood before the content that is about to land.
- A layout effect that restores a kept conversation, declared once per session id: it reads whether pages were held at mount through a ref set by an earlier layout effect of this component (`heldNow.current = held` on every commit, so the restore runs on the mount commit and its dependencies stay honest), and when `heldNow.current !== undefined` and `keptScroll(session.id)` is a number, calls `window.scrollTo(0, <that number>)`; then, in every case, sets `metrics.current = measure()`. It runs before the first paint.
- A layout effect over `held` and `landed` that anchors the page when a page lands: a `useRef` `anchored` starts as the first render's `held`; the effect returns when `held === anchored.current`, otherwise stores `held` in it and then acts on `landed`: `"newest"` (a first page, or a reset page, which Story 4 calls `"newest"`) calls `window.scrollTo(0, document.documentElement.scrollHeight)`; `"earlier"` calls `window.scrollTo(0, document.documentElement.scrollHeight - metrics.current.fromBottom)`; `"refresh"` scrolls to the bottom the same way only when `metrics.current.atBottom`, else does nothing; and every case ends with `metrics.current = measure()`.
- An effect over `session.id` that adds a passive `scroll` listener on `window` calling `keepScroll(session.id, window.scrollY)` and `metrics.current = measure()`, and removes it in its cleanup.

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/turns/Conversation.tsx src/panels/turns/turns.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/turns/turns.test.tsx'`

Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(web): the conversation refreshes on the stream and keeps its scroll" -- internal/web/ui/src/panels/turns/Conversation.tsx internal/web/ui/src/panels/turns/turns.test.tsx
```

### Task 8.3: The `turns` panel, its registration and the rebuilt bundle

**Files:**
- Create: `internal/web/ui/src/panels/turns/index.tsx`
- Modify: `internal/web/ui/src/panels/index.ts` (after the line `import "./fleet";`)
- Modify: `internal/web/dist/` (rebuilt)
- Test: `internal/web/ui/src/panels/turns/turns.test.tsx`

Behaviour and names (contract: the `turns` panel, `{ id: "turns", appliesTo: (s: Session) => s.claude, tab: { order: 1, label: "conversation" }, Head: SessionHead, Strip: FactStrip }`; part 1's `definePanel`, `panelFor`, `useSession`, `Panel` and `PanelProps`; Story 6's `NoSession`; the classic `Turns` in `turns.mjs:380-392`):

- [ ] **Step 1: Write the failing tests**

In `internal/web/ui/src/panels/turns/turns.test.tsx` add the imports `import { definePanel, panelFor, type PanelProps } from "@/app/panels";`, `import { FactStrip } from "@/session/FactStrip";` and `import { SessionHead } from "@/session/SessionHead";` and the side-effect import `import "./index";` (the panel registers when its module is imported). Append `describe("the turns panel")`, whose helper draws the registered `Body` for a route (`props` being `{ sessionId: 1, params: { id: "1" }, search: {} }` as `panelProps` builds them), in the same `QueryClientProvider`. Tests, each one `it`; this file imports `./index` and not `@/panels`, so no `info` panel is registered by the time the first of them runs, and the test that registers a stub is the last:

- `is registered as the contract says`: `panelFor("turns")` has `id` `"turns"`, `tab` equal to `{ order: 1, label: "conversation" }`, `Head` `SessionHead`, `Strip` `FactStrip`; its `title` gives `Conversation`; its `appliesTo` is true for a session with `claude: true` and false for one with `claude: false`.
- `draws This session is gone. for a session the snapshot does not list`: with `seed([makeSession({ id: 2 })])` (the snapshot holds only another session) and `props.sessionId` 1, `This session is gone.` is drawn and `turnsRead` was never called.
- `draws the conversation of a Claude session`: with the first page `[1, 2]` held, `prompt 2` is drawn, and `turnsRead` was called once with `(1, {})`.
- `draws no conversation and reads no turns for a session that does not run Claude, and says so while info is not registered`: `makeSession({ id: 1, claude: false })`: `This session is gone.` is drawn (`NoSession`, the answer while no `info` panel is registered), `turnsRead` was never called, and there is no textbox named `Reply`.
- `draws the info panel's body for a session that does not run Claude`: `definePanel({ id: "info", title: () => "Info", Body: () => <p>stub info</p> })`, then `makeSession({ id: 1, claude: false })`: `stub info` is drawn, `turnsRead` was never called, and `This session is gone.` is not drawn.

Fails without: `is registered as the contract says`: any one field of the registration changed, or `appliesTo` left out (a non-Claude session then gets a conversation tab). `draws This session is gone. for a session the snapshot does not list`: the `Body` reading `session.claude` of an undefined session (it throws) or drawing `Conversation` for no session. `draws the conversation of a Claude session`: `Body` drawing nothing or the info panel for a Claude session. `draws no conversation and reads no turns for a session that does not run Claude, and says so while info is not registered`: the `claude` check removed (a read is made and the conversation drawn), or `panelFor("info")` dereferenced without the undefined case (it throws). `draws the info panel's body for a session that does not run Claude`: `NoSession` drawn in place of the `info` panel's `Body`, or the lookup made at module load (the stub, registered later, is not found).

- [ ] **Step 2: Run it to see it fail**

Run: `pnpm -C internal/web/ui exec vitest run src/panels/turns/turns.test.tsx`

Expected: FAIL: `Failed to resolve import "./index" from "src/panels/turns/turns.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement**

Create `internal/web/ui/src/panels/turns/index.tsx` (a header comment: the conversation panel, a port of the classic `Turns`): a private `Body(props: PanelProps): ReactElement` that reads `const session = useSession(props.sessionId)` and returns, in this order, `<NoSession />` when `session === undefined`; for `!session.claude` the `info` panel's body looked up at render time, `const info = panelFor("info")`, as `<info.Body {...props} />`, or `<NoSession />` when `info` is undefined (Story 9 registers it, and the lookup is at render so import order does not matter); else `<Conversation session={session} />`. Export `const turnsPanel = definePanel({ id: "turns", title: () => "Conversation", appliesTo: (s: Session) => s.claude, tab: { order: 1, label: "conversation" }, Head: SessionHead, Strip: FactStrip, Body })`. Imports: `definePanel`, `panelFor`, `type PanelProps` from `@/app/panels`; `useSession` from `@/store/fleet`; `NoSession` from `@/session/NoSession`; `SessionHead` from `@/session/SessionHead`; `FactStrip` from `@/session/FactStrip`; `type Session` from `@/api/gen`; `Conversation` from `./Conversation`.

In `internal/web/ui/src/panels/index.ts`, after the line

```
import "./fleet";
```

add the line `import "./turns";` (later panel stories add theirs beneath it).

- [ ] **Step 4: Run it to see it pass**

Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/turns/index.tsx src/panels/turns/turns.test.tsx src/panels/index.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/turns/turns.test.tsx'`

Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Rebuild the bundle and run the story's Verify**

Run: `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens conversation,waiting -out "$d"'`

Expected: the build writes `internal/web/dist/`; `check` passes every test, typecheck, lint and format; `dist:check` prints no stale-bundle error; `go test` prints `ok  	github.com/aidan-bailey/loom/internal/web`; webshot renders the conversation and waiting screens of the new app (session 1's turns and session 2's needs-you box) with no console error and no failed request.

- [ ] **Step 6: Run the story gates**

Run: `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi' && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test -timeout 30m ./...`

Expected: no output from gofmt, vet silent, every package `ok`.

- [ ] **Step 7: Commit** (`git add -N` the new files, the bundle's included: a rebuilt bundle renames its hashed assets)

```bash
git add -N internal/web/ui/src/panels/turns/index.tsx internal/web/dist
git commit -m "feat(web): the conversation panel and the bundle that draws it" -- internal/web/ui/src/panels/turns/index.tsx internal/web/ui/src/panels/turns/turns.test.tsx internal/web/ui/src/panels/index.ts internal/web/dist
```

## Story 9: The info panel

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/info/index.tsx`, `internal/web/ui/src/panels/info/Info.tsx`, `internal/web/ui/src/panels/info/info.test.tsx`, `internal/web/ui/src/session/jobs.ts`, `internal/web/ui/src/session/jobs.test.ts`, `internal/web/ui/src/fleet/usage.ts`, `internal/web/ui/src/fleet/usage.test.ts`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
- **Interfaces:** exposes the `info` panel, `jobGlyph`, `jobTone`, `elapsed`, `jobEnd`, `jobLine`; uses Story 3's `sessionActions` and session commands, Story 5's `AGENT_ID`, Story 6's `SessionHead`, `SubLine`, `NoSession`, `parityText`, `checksText` and `useMarkSeen`, Story 7's `ReplyBox`, part 1's `CommandButton`, `ago` and `fleet/usage.ts`'s helpers, widened (its private helpers exported, `shownWindow` reshaped with `rowShare`'s answers unchanged, `shareText` and `resetText` added)
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens info -out "$d"'`
- **Review:** light (a port of `session.mjs`'s `Info`; its actions are Story 3's commands)
- **Form:** brief (a port of `session.mjs`'s `Info`)

This story registers the `info` panel and ports the classic `Info` (`internal/web/static/js/session.mjs:70-337`): the sub-line; the Changes, PR and Account tiles; the session's subagents, each linking to the subagent view when its id is a valid agent id; its jobs, each linking to the job view with its glyph and line and drawn again every 5 s while one runs; the actions `sessionActions` offers, as command buttons; the reply box; and "This session is gone." for a session the snapshot no longer lists. It marks the session seen on opening and closing. It adds the job helpers the job view shares (`session/jobs.ts`), with an unknown state drawn as its raw text under a neutral glyph (spec Part 2 settlement 9), and widens part 1's `fleet/usage.ts`, which keeps its helpers private, so the Account tile can draw a window's share and reset as the classic tile does; `rowShare` keeps answering `{ text, amber }` exactly as before. Every action is Story 3's command (`CommandButton`), so the sheets, the in-flight key, the stale gate and the toasts are the command path's; this story invents no body and no enablement rule. One deliberate difference from the classic tile: the Account tile is drawn only for a session that runs Claude, since another program records no account and the classic lookup of `""` would show the default account's usage under a plain shell (the rule `rowShare` already keeps, and the repo's "no fallback account"). No code of this story holds a landmine marker, so the developer runs at the default effort. The bundle in `internal/web/dist/` is rebuilt once, in the last task, so `pnpm run dist:check` is stale between tasks 9.1 and 9.4 (no story gate reads it between tasks; the Go tests read the committed bundle).

Rules every task keeps (the app's `internal/web/ui/CLAUDE.md`): components return `ReactElement`; the daemon's strings (titles, commands, descriptions, branches, waiting texts, raw states) are drawn as text nodes, never as markup, and no `style` attribute is written; every colour is a token class (`text-working`, `text-turn`, `text-error`, `text-muted-foreground`, `border-border`, `bg-card`, …), never a literal; form text is 16px and touch targets at least 44px (`min-h-11` on a row that is a link); an unused parameter is never `_`-prefixed; a test mock is typed as `vi.fn<(a: A) => R>(…)`; any sort whose order reaches `dist/` compares by code point. Every command below runs from the repo root; a task's test command is `pnpm -C internal/web/ui exec vitest run <file>` and its typecheck is `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`.

### Task 9.1: The job helpers

**Files:**
- Create: `internal/web/ui/src/session/jobs.ts`
- Create: `internal/web/ui/src/session/jobs.test.ts`

Behaviour and names (contract: `jobGlyph`, `jobTone`, `elapsed`, `jobEnd`, `jobLine`; the classic ones are in `internal/web/static/js/session.mjs:153-205`; `Job` and `JobState` are `@/api/gen`'s):

- `function jobGlyph(job: Job): string`: `running` is `◐`; `queued` and `held` are `○`; `cancelled` is `⊘`; `done` with `exit` not null and `exit.code` 0 is `✓`; `done` otherwise (a non-zero code, no exit), `failed` and `lost` are `✗`; a state this build does not know is `?` (a neutral mark, never a success or a failure).
- `function jobTone(job: Job): string`, exported: the text-colour class of the job's glyph, so the info view's job rows and the job view draw one tone and neither keeps a private copy. It is chosen by the job's state through `jobGlyph(job)`, so a done job's tone follows its exit code: `◐` (running) `text-turn`, `✓` `text-working`, `✗` `text-error`, any other glyph (queued, held, cancelled, an unknown state) `text-muted-foreground`.
- `function elapsed(from: number, to: number): string` (milliseconds): `""` when `from` is NaN; else the whole seconds between them, rounded and never below 0: under 60 `"<s>s"`, under an hour `"<m>m <s>s"` (`"2m 10s"`), else `"<h>h <m>m"` (`"1h 4m"`).
- `function jobEnd(job: Job): string`: how a finished job ended: its `failure` when not empty; else `"cancelled: <cancel_reason>"` when that is not empty; else, when `exit` is not null, its `signal` when it has one, else `"exit <code>"`; else the raw `state` (so an unknown state shows its own word).
- `function jobLine(job: Job, now: number): string`: the row's sub-line, its parts joined by `" · "` with empty parts left out and always starting `"job <id>"`: a `running` job adds `elapsed(Date.parse(job.started), now)`; a `queued` or `held` job adds its `state` then its `waiting`; any other state adds `jobEnd(job)` then, when `job.ended` is not empty and `ago(job.ended, now)` is not empty, that age as `"<age> ago"`, except that `"now"` stays `"now"`. `ago` is part 1's, from `@/panels/fleet/SessionRow`.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/session/jobs.test.ts`. Build jobs with `makeJob` from `@/commands/testkit` (Story 3's; reuse it, copy nothing) and fix `NOW = Date.parse("2026-11-19T12:00:00Z")`, with a helper `ago(ms)` giving the ISO time `ms` before `NOW`. An unknown state is `"paused" as Job["state"]`. Tests, in `describe("jobGlyph")`, `describe("jobTone")`, `describe("elapsed")`, `describe("jobEnd")` and `describe("jobLine")`:

  - `jobGlyph marks each state`: running `◐`, queued `○`, held `○`, cancelled `⊘`, done with `exit: { code: 0 }` `✓`. Fails without: any state's branch removed or its glyph changed.
  - `jobGlyph marks a done job that did not exit 0, a failed job and a lost job as failures`: `done` with `{ code: 2 }` is `✗`; `done` with `exit: null` is `✗`; `failed` `✗`; `lost` `✗`. Fails without: the `exit.code === 0` test dropped (the first gives `✓`) or the null check (the second throws).
  - `jobGlyph gives a state it does not know a neutral mark`: `"paused"` is `?`. Fails without: the final fallback returning `✗` or `✓`.
  - `jobTone colours a running job, a success and a failure`: running is `text-turn`; `done` with `exit: { code: 0 }` is `text-working`; `done` with `{ code: 2 }`, `failed` and `lost` are `text-error`. Fails without: any of the three branches removed or its class changed, or `jobTone` reading `job.state` alone (the `done` with code 2 then gives `text-working`).
  - `jobTone is neutral for a state that is not running, finished or failed, and for one it does not know`: queued, held, cancelled and `"paused"` (cast) are each `text-muted-foreground`. Fails without: the default returning `text-error` or `text-working` (the unknown state then reads as a failure or a success).
  - `elapsed words seconds, minutes and hours`: `elapsed(0, 45_000)` `45s`; `elapsed(0, 130_000)` `2m 10s`; `elapsed(0, 3_840_000)` `1h 4m`; `elapsed(0, 59_500)` `1m 0s` (59.5 s rounds to 60 s); `elapsed(5_000, 1_000)` `0s` (never negative). Fails without: the rounding, the `Math.max(0, …)`, or either threshold.
  - `elapsed is empty for a time that does not parse`: `elapsed(Date.parse(""), NOW)` is `""`. Fails without: the NaN check (it returns `NaNs`).
  - `jobEnd names the failure, then the cancel reason, then the signal, then the exit code`: a job with `failure: "boom"`, `cancel_reason: "x"` and `exit: { code: 1 }` is `boom`; with the failure empty it is `cancelled: x`; with both empty and `exit: { code: 9, signal: "SIGKILL" }` it is `SIGKILL`; with `exit: { code: 3 }` it is `exit 3`. Fails without: the order of the four checks changed or the signal preferred over nothing (each assertion names one).
  - `jobEnd of a job with no exit says its state, an unknown one included`: `makeJob({ state: "lost" })` is `lost`; `"paused"` is `paused`. Fails without: the final `return job.state`.
  - `jobLine of a running job says how long it has run`: `started` 130 s before `NOW` gives `job 214 · 2m 10s`; a running job whose `started` is `""` gives `job 214`. Fails without: the `elapsed` part, or the empty-part filter (the second ends in ` · `).
  - `jobLine of a queued or held job says its state and why it waits`: queued with `waiting: "slot busy"` gives `job 216 · queued · slot busy`; held with no `waiting` gives `job 215 · held`. Fails without: the `waiting` part or the empty-part filter.
  - `jobLine of a finished job says how it ended and when`: done, `exit: { code: 0 }`, `ended` 4 minutes before `NOW` gives `job 212 · exit 0 · 4m ago`; failed with `failure: "out of memory"` ended 3 hours before gives `job 209 · out of memory · 3h ago`; ended 10 s before gives `job 212 · exit 0 · now` (no `ago`); with `ended: ""` it ends at `exit 0`. Fails without: the `ago` suffix, the `now` exception (the third reads `now ago`), or the `ended` check.
  - `jobLine of a job whose state it does not know says the raw state`: `"paused"` with no `exit` and `ended: ""` gives `job 214 · paused`. Fails without: a state-by-state table in `jobLine` with no final branch (the line is `job 214`), or `jobEnd` not falling back to the state.

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/session/jobs.test.ts`
  Expected: `Failed to resolve import "./jobs" from "src/session/jobs.test.ts". Does the file exist?`

- [ ] **Step 3: Implement** `internal/web/ui/src/session/jobs.ts` as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/session/jobs.ts src/session/jobs.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/session/jobs.test.ts'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```
  git add -N internal/web/ui/src/session/jobs.ts internal/web/ui/src/session/jobs.test.ts
  git commit -m "feat(web): the job helpers the info and job views share" -- internal/web/ui/src/session/jobs.ts internal/web/ui/src/session/jobs.test.ts
  ```

### Task 9.2: The usage helpers, widened

**Files:**
- Modify: `internal/web/ui/src/fleet/usage.ts`
- Test: `internal/web/ui/src/fleet/usage.test.ts`

Behaviour and names (the classic `usage.mjs`: `resetPassed`, `shareText`, `resetText`, `isStale`, `shownWindow`; this story's own names, used by Task 9.3 only):

- Part 1's `usage.ts` keeps `usageFor`, `shownWindow`, `resetPassed` and `isStale` private. Export `usageFor`, `resetPassed` and `isStale` with their signatures unchanged: the line `function usageFor(usage: Usage[], account: string): Usage | undefined {` becomes `export function usageFor(…` (same for the other two).
- Reshape `shownWindow`: replace its signature line `function shownWindow(entry: Usage): Window | null {` and its body `return entry.five_hour ?? entry.seven_day ?? null;` with an exported `shownWindow(entry: Usage): ShownWindow | null` over a new exported `interface ShownWindow { w: Window; label: string; other: Window | null; otherLabel: string }`: an entry with a 5-hour window gives `{ w: five_hour, label: "5h", other: seven_day, otherLabel: "7d" }`; one with only a 7-day window gives `{ w: seven_day, label: "7d", other: null, otherLabel: "" }`; one with neither gives null. Update the comment above it (it begins `// The window a row shows`) to say it also names the window and the other one. `rowShare` reads `shown.w` where it read the window, and nothing else of its answer changes (the `{ text, amber }` it returns, and null in the same cases).
- Add `export function shareText(w: Window, now: number): string`: `"reset"` when `resetPassed(w, now)`, else `` `${Math.round(w.pct)}%` ``; `rowShare`'s `text` is `shareText(w, now)` (it computes `reset` once for `amber` as it does today; only the text moves).
- Add `export function resetText(iso: string, now: number): string`: `""` when `iso` does not parse; else, with `left = max(0, t - now)`: under 24 hours, `mins = ceil(left / 60000)` and `hours = floor(mins / 60)`, the text is `` `${hours}h${String(mins % 60).padStart(2, "0")}m` `` when `hours > 0` (`"1h40m"`, `"2h05m"`), else `` `${mins}m` `` (`"35m"`, `"0m"`); at 24 hours or more, the weekday as `new Date(t).toLocaleDateString([], { weekday: "short" })`.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/fleet/usage.test.ts`. Replace the line `import { rowShare } from "./usage";` with

  ```ts
  import { isStale, resetPassed, resetText, rowShare, shareText, shownWindow, usageFor } from "./usage";
  ```

  and append, after the existing `describe("rowShare", …)`, these (`NOW`, `HOUR`, `iso`, `win` and `entry` are the file's own):

  - `describe("usageFor")` — `finds an account's entry and reads "" as the default account`: over `[entry("default"), entry("work")]`, `usageFor(list, "work")?.account` is `work`, `usageFor(list, "")?.account` is `default`, `usageFor(list, "other")` is undefined. Fails without: the `"" → "default"` mapping or the export.
  - `describe("shownWindow")` — `names the 5-hour window and the 7-day one beside it`: an entry with both gives `{ w: its five_hour, label: "5h", other: its seven_day, otherLabel: "7d" }`. Fails without: `label` or `other` missing.
  - `shownWindow falls back to the 7-day window alone`: only `seven_day` gives `label` `7d`, `other` null, `otherLabel` `""`. Fails without: the fallback branch.
  - `shownWindow is null for an entry with neither`: both null gives null. Fails without: the final `return null`.
  - `describe("resetPassed and isStale")` — `resetPassed is true at the reset time and after, false before and for a time it cannot read`: `resets_at` equal to `iso(NOW)` and `iso(NOW - 1)` are true, `iso(NOW + 1000)` and `""` are false. Fails without: `<=` written `<` (the first case) or an unparsable time read as passed.
  - `isStale needs a bound and a sample at least that old`: `isStale(iso(NOW - 900_000), 900, NOW)` true; with `iso(NOW - 899_000)` false; a bound of `undefined` or 0 is false. Fails without: `>=` written `>` (the first case), or the bound check.
  - `describe("shareText")` — `shareText is the rounded percentage, or reset once the window has reset`: `win(62.4)` gives `62%`, `win(62.5)` gives `63%`, `win(90, iso(NOW - HOUR))` gives `reset`. Fails without: the rounding (`62.4%`) or the reset branch.
  - `describe("resetText")` — `resetText words under a day as hours and minutes, or minutes alone`: `iso(NOW + 100 * 60_000)` gives `1h40m`; `iso(NOW + 125 * 60_000)` gives `2h05m`; `iso(NOW + 35 * 60_000)` gives `35m`; `iso(NOW + 30_000)` gives `1m` (30 s rounds up); `iso(NOW)` gives `0m`; a time before `NOW` gives `0m`. Fails without: the `ceil` (the fourth reads `0m`), the zero-padding (the second reads `2h5m`), or the `max(0, …)` (the last reads a negative).
  - `resetText names the weekday from a day out, and is empty for no time`: for `iso(NOW + 24 * HOUR)` and `iso(NOW + 70 * HOUR)` the text is `new Date(t).toLocaleDateString([], { weekday: "short" })` for the same `t` and holds no digit; `resetText("", NOW)` and `resetText("later", NOW)` are `""`. Fails without: the 24-hour boundary (`24 * HOUR` reads `24h00m`, which holds digits) or the unparsable check.
  - `rowShare still answers the same text and amber after the widening`: the existing `rowShare` tests are left as they are and must stay green; add one, `rowShare takes its text from shareText`: a window at 62.5% gives `63%`. Fails without: `rowShare` rounding differently from `shareText`.

  The existing `rowShare` tests are the pin that this story's reshaping of `shownWindow` changes no answer of the fleet row.

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/fleet/usage.test.ts`
  Expected: the new tests fail with `usageFor is not a function` and its siblings (the names are not exported yet); the existing `rowShare` tests pass.

- [ ] **Step 3: Implement** the changes to `internal/web/ui/src/fleet/usage.ts` as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/fleet/usage.ts src/fleet/usage.test.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/fleet/usage.test.ts src/panels/fleet/fleet.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  2 passed (2)` and no failed test (the fleet panel's tests, which read `rowShare`, stay green).

- [ ] **Step 5: Commit**

  ```
  git commit -m "refactor(web): export the usage helpers and add the window's share and reset texts" -- internal/web/ui/src/fleet/usage.ts internal/web/ui/src/fleet/usage.test.ts
  ```

### Task 9.3: The info view's sub-line, tiles, subagents and jobs

**Files:**
- Create: `internal/web/ui/src/panels/info/Info.tsx`
- Create: `internal/web/ui/src/panels/info/info.test.tsx`

Behaviour and names (contract: Story 6's `SubLine`, `parityText`, `checksText`; Story 5's `AGENT_ID` from `@/app/routes`; Task 9.1's `jobGlyph`, `jobTone`, `jobLine`; Task 9.2's helpers; Story 3's `sessionActions`; part 1's `hrefFor`, `useFleet`; the classic `Tiles`, `Agents` and `Jobs` in `session.mjs:76-232`):

`Info.tsx` exports `function Info(props: { session: Session }): ReactElement`, this story's own name, drawn by the panel's `Body` (Task 9.5). In this task it draws, in a column with `gap-4 px-4 pb-6`, in this order, `<SubLine session={session} />`, the tiles, the subagents and the jobs; Task 9.4 adds the rest. Its `now` is `Date.now()` read at each render.

- Tiles: a grid (`grid grid-cols-[repeat(auto-fill,minmax(96px,1fr))] gap-2`) of boxes (`rounded-xl border border-border bg-card p-3`), each with a small uppercase legend line (`text-[11px] tracking-wide text-muted-foreground uppercase`), a figure (`text-[17px] font-bold` tabular figures) and zero or more sub-lines (`text-[11px] text-muted-foreground`, truncated on one line). No tiles at all draws no grid. A tile whose data is missing is left out:
  - **Changes**, drawn when `session.diff.added` or `session.diff.removed` is above 0, or `session.parity_known`: legend `Changes`; figure `+<added>` in `text-working` and `−<removed>` (U+2212) in `text-error`; a sub-line `parityText(session)` when it is not `""`. It is a link to `hrefFor("changes", { id: String(session.id) })`, with a trailing `ChevronRight` icon (lucide-react), when `sessionActions(session).diff` is true, else a plain box with no link and no chevron.
  - **PR**, drawn when `session.pr` is not null: legend `PR`; figure `#<number>`, with an `ExternalLink` icon (lucide-react) when `pr.url` is not empty; a sub-line `checksText(pr)`. It is an `<a href={pr.url} target="_blank" rel="noopener noreferrer">` when `pr.url` is not empty, else a plain box.
  - **Account**, drawn only when `session.claude` is true and the snapshot's `usage` (read through `useFleet((s) => s.snapshot?.usage)`, `?? []`) holds an entry for `usageFor(usage, session.account)` with a `shownWindow`: legend the entry's `account` (the daemon's name for the default account is `default`); figure `shareText(shown.w, now)`; first sub-line `[shown.label, reset(shown.w)]` and, when `shown.other` is not null, a second `[shown.otherLabel + " " + shareText(shown.other, now), reset(shown.other)]`, each part left out when empty and joined by `" · "`, where `reset(w)` is `""` when `resetPassed(w, now)` else `resetText(w.resets_at, now)`. When `isStale(entry.at, snapshot.usage_stale_after_s, now)` the figure is `text-muted-foreground` instead of `text-foreground`.
- Subagents: drawn when `session.agents` is not null and not empty: a heading `<h2>Agents <count></h2>` (`text-xs font-semibold tracking-wide text-muted-foreground uppercase`) over one row per agent, in the daemon's order: a glyph (`○` when `idle`, else `◐`, `aria-hidden`), the `name`, `idle` or `running` at the right, the `description` under it when not empty. A row is a link to `hrefFor("agent", { id: String(session.id), agent: a.id })`, with a `ChevronRight`, when `a.id` is not empty and `AGENT_ID.test(a.id)`, else a plain row.
- Jobs: drawn when `session.jobs` is not null and not empty: a heading `<h2>Jobs <count></h2>` over one link row per job (every job has an id) to `hrefFor("job", { id: String(session.id), job: String(job.id) })` with a `ChevronRight`: the glyph `jobGlyph(job)` in the class `jobTone(job)` (Task 9.1's; the row keeps no mapping of its own), the title `job.description` or, when that is empty, `job.command` (in `font-mono` when it is the command), and under it `jobLine(job, now)`.
- Rows that are links are at least 44px high (`min-h-11`); rows and tiles are never wider than the screen (`min-w-0`, truncation on one line for the title and sub-lines).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/info/info.test.tsx`. Set up once: `import "@/commands";` (the command registry the later task's buttons need), `vi.useFakeTimers({ toFake: ["Date"] })` with `vi.setSystemTime(NOW)` (`NOW = Date.parse("2026-11-18T12:00:00Z")`) in `beforeEach`, and in `afterEach` `vi.useRealTimers()`, `cleanup`-style resets: `useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 })`, `window.localStorage.clear()` and `window.history.replaceState(null, "", window.location.pathname)`. A helper `show(id: number, over: Partial<Session> = {}, tweak?: (snapshot: Snapshot) => void): Session` takes `fixtureSnapshot(NOW)`, lets `tweak` edit it, finds the session with that id, applies `over` to it in the snapshot, stores the snapshot with `useFleet.getState().apply("snapshot", snapshot)` then `useFleet.setState({ stale: false, link: "live" })`, sets the hash to `#/s/<id>/info` with `window.history.replaceState(null, "", "#/s/<id>/info")`, and returns the edited session. Tests use the canned sessions: session 1 (Running, working, Claude, account `""`, `+212 −40`, `parity_known` with ahead 3 and behind 0, an open PR `#482` with url and `pending` checks, agents `recon` (`a1f00dcafe0001`, running) and `verifier` (`a1f00dcafe0002`, idle), jobs 214 running, 215 held, 212 done, 209 failed and 216 queued), session 6 (Claude, account `work`, a merged PR with no url and `none` checks, no diff, no jobs) and session 3 (not Claude, no diff). Tests, in `describe("Info: the sub-line and the tiles")`, `describe("Info: the subagents")` and `describe("Info: the jobs")`:

  - `draws the sub-line first`: `render(<Info session={show(1)} />)` draws text `working` (the sub-line's lowercased label) and the branch `aidanb/fix-pane-cursor`. Fails without: `Info` not drawing `SubLine`.
  - `the Changes tile is a link to the changes screen with its counts and parity`: the link named `/Changes/` has `href` `#/s/1/diff`, text holding `+212` and `−40` (U+2212) and `↑3 ↓0`. Fails without: the link wrapper (a plain box has no role `link`), the counts, or `parityText`.
  - `the Changes tile is a plain box while the diff cannot be read`: session 1 with `status: "Loading"` draws the text `Changes` but no link named `/Changes/`. Fails without: the `sessionActions(session).diff` check (a link is drawn).
  - `the Changes tile is left out with no counts and unknown parity`: session 3 (`parity_known` false, diff 0/0) draws no `Changes` text. Fails without: the drawing condition (it draws `+0 −0`).
  - `the Changes tile is drawn for known parity alone`: session 3 with `parity_known: true`, `ahead: 0`, `behind: 0` draws `Changes`, `+0` and `✓ even`. Fails without: the `parity_known` term of the condition.
  - `the PR tile links to the pull request in a new tab and words its checks`: the link named `/PR/` has `href` `https://github.com/aidan-bailey/loom/pull/482`, `target` `_blank`, `rel` containing `noopener` and `noreferrer`, and text `#482` and `checks pending`; with `pr.checks` `passing` the text is `✓ checks pass`. Fails without: the `rel`, `checksText`, or the link.
  - `the PR tile is a plain box when the PR has no url, and merged words its state`: session 6's PR draws `#31` and `merged` and no link named `/PR/`. Fails without: the `url` check (a link with an empty `href` is drawn) or `checksText`'s non-open branch.
  - `no PR, no PR tile`: session 3 draws no `PR` text. Fails without: the `session.pr` check.
  - `the Account tile draws the shown window's share and both resets`: session 1's account entry is `default` (62%, resets in 1h40m; 7-day 31%, resets in 70h): the tile's legend `default`, figure `62%`, a sub-line `5h · 1h40m` and a sub-line `7d 31% · ` followed by `new Date(NOW + 70 * 3_600_000).toLocaleDateString([], { weekday: "short" })`. Fails without: the second sub-line, `resetText`, or the `"" → default` lookup.
  - `the Account tile of a named account with one window has one sub-line`: session 6 (account `work`, 18%, 5-hour only) draws `work`, `18%` and `5h · 3h05m` and no `7d` text. Fails without: the `other` check (a `7d` line is drawn for a null window) or the account lookup.
  - `the Account tile says reset once the window has reset and drops its reset text`: `tweak` sets the `default` entry's `five_hour.resets_at` to `NOW - 60_000` as an ISO string: the figure is `reset` and the first sub-line is `5h` alone. Fails without: `shareText`'s reset branch, or `reset()` returning the reset text for a passed window (the line reads `5h · 0m`).
  - `the Account tile's figure is muted when the sample is stale`: `tweak` sets the entry's `at` to `NOW - 901_000` (the snapshot's `usage_stale_after_s` is 900): the figure `62%` has class `text-muted-foreground`; with `at` at `NOW - 60_000` it has `text-foreground`. Fails without: the `isStale` check or its bound.
  - `no Account tile for a session that does not run Claude, nor without a usage entry`: session 3 (`claude` false) draws no `default` legend even though the default account has an entry; session 1 with `tweak` emptying `snapshot.usage` draws no `62%`. Fails without: the `session.claude` check (the first draws the default account's tile), or the entry check.
  - `the subagents are rows with a name, a state and a description, linked by their ids`: session 1 draws the heading `Agents 2`, `recon` with `running` and its description `Map every caller of the cursor path` inside a link of `href` `#/s/1/agent/a1f00dcafe0001`, and `verifier` with `idle` in a link `#/s/1/agent/a1f00dcafe0002`. Fails without: the link, the idle/running word, or the description.
  - `a subagent without a valid id is a row, not a link`: agents `[{ id: "", name: "no-id", description: "", idle: false }, { id: "a b/c", name: "odd-id", description: "", idle: true }, { id: "ok_id-1", name: "fine", description: "", idle: false }]`: `no-id` and `odd-id` have no link; `fine` is a link to `#/s/1/agent/ok_id-1`. Fails without: the `AGENT_ID` test (`odd-id` links) or the empty-id check (`no-id` links).
  - `no subagents, no Agents heading`: session 6 draws no heading named `/Agents/`. Fails without: the empty check (it draws `Agents 0`).
  - `the jobs are links to the job view with their glyph, title and line`: session 1 draws the heading `Jobs 5`; the link `#/s/1/job/214` holds `◐`, `ui tests` and `job 214 · 2m 10s`; the link `#/s/1/job/215` holds `○`, `race tests` and `job 215 · held · after job 214`; `#/s/1/job/212` holds `✓`, its command `go vet ./...` (its description is empty) and `job 212 · exit 0 · 4m ago`; `#/s/1/job/209` holds `✗` and `job 209 · exit 1 · 20m ago`; `#/s/1/job/216` holds `○` and `queued`. Fails without: the link, `jobGlyph`, the `description || command` title, or `jobLine`.
  - `the jobs' glyphs are drawn in the tone of their state`: in session 1 the `◐` of job 214 has class `text-turn`, the `✓` of job 212 `text-working` and the `✗` of job 209 `text-error`. Fails without: the row's glyph drawn through `jobTone` (a literal class, or none, turns it red).
  - `a job whose state this build does not know shows its raw state`: `tweak` sets job 214's `state` to `"paused"` (cast) and clears its `started`: its row holds `?` and `job 214 · paused`, and no `◐`, and the `?` has class `text-muted-foreground`. Fails without: `jobGlyph`'s neutral fallback, `jobTone`'s neutral default, or `jobEnd`'s `state` fallback.
  - `no jobs, no Jobs heading`: session 6 draws no heading named `/Jobs/`. Fails without: the empty check.

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/info/info.test.tsx`
  Expected: `Failed to resolve import "./Info" from "src/panels/info/info.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement** `internal/web/ui/src/panels/info/Info.tsx` as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/info/Info.tsx src/panels/info/info.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/info/info.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```
  git add -N internal/web/ui/src/panels/info/Info.tsx internal/web/ui/src/panels/info/info.test.tsx
  git commit -m "feat(web): the info view's sub-line, tiles, subagents and jobs" -- internal/web/ui/src/panels/info/Info.tsx internal/web/ui/src/panels/info/info.test.tsx
  ```

### Task 9.4: The actions, the reply box, the seen mark and the jobs' clock

**Files:**
- Modify: `internal/web/ui/src/panels/info/Info.tsx`
- Test: `internal/web/ui/src/panels/info/info.test.tsx`

Behaviour and names (contract: Story 3's `sessionActions` and the commands `session.pause`, `session.resume`, `session.push`, `session.merge`, `session.kill`; part 1's `CommandButton`; Story 7's `ReplyBox`; Story 6's `useMarkSeen`; the classic ones are the actions and the tick in `Info`, `session.mjs:233-337`):

In `Info`, after the jobs, in this order:

- The actions: a row (`flex gap-2`, each button `flex-1`) of `CommandButton`s, one per action `sessionActions(session)` gives, in this order: `<CommandButton id="session.pause" variant="secondary" />`, `<CommandButton id="session.resume" />` (the primary variant), `<CommandButton id="session.push" variant="secondary" />`, `<CommandButton id="session.merge" variant="secondary" />`, `<CommandButton id="session.kill" variant="destructive" />`. An action `sessionActions` withholds is not drawn; no row at all when none is on offer. The sheets (Kill's words, Push's, Merge's picker and its "No other session in this workspace to merge in." refusal) and a Kill answered 200 opening the fleet are the commands'; `Info` draws no sheet and keeps no sheet state.
- `<ReplyBox session={session} />` (Story 7): it draws the box, "Resume the session to reply." or nothing by itself.
- A mark: `useMarkSeen(session.id)` (Story 6), called before any early return, so the session is marked seen on opening and again on closing.
- A clock: while any of the session's jobs has `state === "running"`, a `setInterval` every 5000 ms (set in a `useEffect` whose dependency is that boolean, cleared on cleanup) bumps a counter state so the running job's elapsed time moves with the clock and not only with the stream; with no running job there is no interval.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/info/info.test.tsx`, in a new `describe("Info: the actions, the reply box and the clock")` (add the imports it needs: `userEvent`, `act`, `readSeen` from `@/fleet/seen`, `api` from `@/api/request`, `noticesOk` from `@/commands/testkit`). Its `beforeEach` adds `vi.spyOn(toastManager, "add").mockImplementation(() => "toast")` (`@/components/ui/toast`). Tests:

  - `a Running session offers pause, push, merge and kill, in that order, and no resume`: session 1's buttons named `Pause`, `Push`, `Merge`, `Kill` appear in that document order; there is no `Resume`. Fails without: the order of the five lines, or a missing `sessionActions` check (Resume drawn).
  - `a Paused session offers resume, push, merge and kill, and no pause`: `show(5, { status: "Paused", group: "paused" })`: `Resume`, `Push`, `Merge`, `Kill`, no `Pause`. Fails without: the `pause`/`resume` gating.
  - `a Recoverable session offers kill alone`: `show(5, { status: "Recoverable", group: "paused" })`: only `Kill` among the five. Fails without: the `push`/`merge` gating (they are drawn).
  - `a Loading or Deleting session offers none of the five, and no row`: `show(1, { status: "Loading" })` and `"Deleting"` draw none of the five names. Fails without: the `kill`/`pause` busy check.
  - `Resume is the primary button and Kill the destructive one`: `Resume`'s class holds `bg-primary` and `Kill`'s `text-destructive` (the classes `buttonVariants` gives them). Fails without: the `variant` of either.
  - `a button runs its command`: with `vi.spyOn(api, "pause").mockResolvedValue(noticesOk())`, clicking `Pause` calls `api.pause` with `1` (`await waitFor`). Fails without: the button naming a different command id or no command at all.
  - `the buttons are off while the snapshot is stale`: after `useFleet.setState({ stale: true, link: "offline" })`, `Pause` is disabled. Fails without: drawing a plain `Button` in place of `CommandButton` (it stays enabled).
  - `draws the reply box for a session that can take a reply, and says to resume for a paused one`: session 1 draws a textbox named `Reply`; `show(5, { status: "Paused", group: "paused" })` draws `Resume the session to reply.` and no textbox. Fails without: `Info` not drawing `ReplyBox`.
  - `marks the session seen when it opens and again when it closes`: with the clock at `NOW`, `const view = render(<Info session={show(1)} />)`: `readSeen()["1"]` is `NOW`; after `vi.setSystemTime(NOW + 60_000)` and `view.unmount()` it is `NOW + 60_000`. Fails without: the `useMarkSeen` call or its cleanup.
  - `a running job's time moves every 5 s`: use `vi.useFakeTimers()` (every timer faked, replacing the file's `toFake: ["Date"]`) with `vi.setSystemTime(NOW)` at the top of this test; session 1 draws `job 214 · 2m 10s`; `act(() => { vi.advanceTimersByTime(5000); })` makes it `job 214 · 2m 15s`. Fails without: the interval (the text stays `2m 10s`).
  - `no timer runs for a session with no running job`: session 1 with job 214 made `done` (ended 295 s before `NOW`, `exit: { code: 0 }`) so no job runs: its line reads `job 214 · exit 0 · 4m ago`, and after the same 5 s advance it still reads `4m ago` (a re-render would read `5m ago`). Fails without: the `timing` guard (an interval always runs, the line changes to `5m ago`).
  - `the interval stops when the last running job ends`: draw session 1 with job 214 running, then `rerender` the same component with job 214 `done` (a new session object); after 5 s the finished job's `ago` text has not moved. Fails without: the cleanup of the effect (the old interval keeps ticking).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/info/info.test.tsx`
  Expected: the new tests fail (`Unable to find role "button" and name "Pause"`, no textbox, `readSeen()["1"]` undefined, the text staying `2m 10s`); Task 9.3's tests pass.

- [ ] **Step 3: Implement** the additions to `internal/web/ui/src/panels/info/Info.tsx` as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/info/Info.tsx src/panels/info/info.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/info/info.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```
  git commit -m "feat(web): the info view's actions, reply box, seen mark and jobs' clock" -- internal/web/ui/src/panels/info/Info.tsx internal/web/ui/src/panels/info/info.test.tsx
  ```

### Task 9.5: Register the panel, and rebuild the bundle

**Files:**
- Create: `internal/web/ui/src/panels/info/index.tsx`
- Modify: `internal/web/ui/src/panels/index.ts`
- Modify: `internal/web/dist/` (rebuilt)
- Test: `internal/web/ui/src/panels/info/info.test.tsx`

Behaviour and names (contract: the `info` panel; Story 6's `SessionHead`, `NoSession`; part 1's `definePanel`, `useSession`, `PanelProps`; `panels/index.ts` is quoted by the next panel's story, so only a line is added to it):

- `panels/info/index.tsx`: defines a private `function InfoBody(props: PanelProps): ReactElement` that reads `useSession(props.sessionId)` and draws `<NoSession />` when it is undefined (the snapshot lists no such session), else `<Info session={session} />`; and calls `definePanel({ id: "info", title: () => "Info", tab: { order: 3, label: "info" }, Head: SessionHead, Body: InfoBody })`. It sets no `appliesTo` (every session has the tab) and no `Strip`. Story 8's `turns` panel draws this `Body` through `panelFor("info")` for a session that does not run Claude, so `InfoBody` takes the route's `PanelProps` and nothing else.
- `panels/index.ts`: after the lines the earlier panel stories left (`import "./fleet";` and the `turns` panel's), add the line `import "./info";`. Do not change any other line of the file.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/info/info.test.tsx`: add `import { panelFor } from "@/app/panels";` and `import "@/panels";` (the registration `main.tsx` triggers) and, in a new `describe("the info panel")`, with the file's `show`:

  - `is registered as the info tab, with a session head and no strip`: `panelFor("info")` is defined; its `tab` is `{ order: 3, label: "info" }`; its `Head` is `SessionHead` and its `Strip` and `appliesTo` are undefined; `title` of `{ sessionId: 1, params: { id: "1" }, search: {} }` is `Info`. Fails without: the `import "./info"` line in `panels/index.ts` (`panelFor` is undefined), or a field of the registration (a `Strip`, an `appliesTo` or another `order` fails the matching assertion).
  - `draws the session's info for a session the snapshot lists`: `show(1)`, then render `<Body {...props(1)} />` with `Body` from `panelFor("info")`: the heading `Agents 2` is drawn. Fails without: `InfoBody` drawing `Info`.
  - `says the session is gone for one the snapshot does not list`: after `show(1)`, render the Body with `sessionId: 99`: the status `This session is gone.` is drawn and no `Agents` heading. Fails without: the `NoSession` branch (the body throws on an undefined session or draws nothing).
  - `the Body works for a session that does not run Claude`: `show(3)` and render the Body with `sessionId: 3`: the sub-line (`working`, session 3's lowercased label) is drawn and no `Account` tile. Fails without: the Body assuming a Claude session (it throws or draws a conversation).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/info/info.test.tsx`
  Expected: the four new tests fail, the first with `expected undefined not to be undefined` on `panelFor("info")`; the earlier tests pass.

- [ ] **Step 3: Implement** `panels/info/index.tsx` and the `panels/index.ts` line as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/info/index.tsx src/panels/info/info.test.tsx src/panels/index.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/info/info.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Rebuild the bundle and run the story's Verify**

  Run: `pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens info -out "$d"`
  Expected: the build writes `internal/web/dist/`; `check` passes every test, typecheck, lint and format; `dist:check` prints no stale-bundle error; `internal/web` prints `ok` (its first-load budget test holds: the info view stays in the entry, by design); webshot prints `info` and a file under `$d` with no line starting `  !` (no console error under the real CSP), the screen showing the sub-line, the Changes, PR and Account tiles, the two agents, the jobs and the actions. A `  !` line, or a miss on a request, means a view sent a request on load or wrote a `style` attribute: fix the view.

- [ ] **Step 6: Run the story gates**

  Run: `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi' && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test -timeout 30m ./...`
  Expected: no output from gofmt, vet silent, every package `ok`.

- [ ] **Step 7: Commit** (`git add -N` the new files; a rebuilt bundle renames its hashed assets)

  ```
  git add -N internal/web/ui/src/panels/info/index.tsx internal/web/dist
  git commit -m "feat(web): the info panel, registered, and the bundle with it" -- internal/web/ui/src/panels/info/index.tsx internal/web/ui/src/panels/info/info.test.tsx internal/web/ui/src/panels/index.ts internal/web/dist
  ```

## Story 10: The changes panel

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/changes/index.tsx`, `internal/web/ui/src/panels/changes/Changes.tsx`, `internal/web/ui/src/panels/changes/changes.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
- **Interfaces:** exposes the `changes` panel; uses Story 3's `sessionActions`, Story 4's `usePatch`, Story 6's `SessionHead`, `SubLine` and `NoSession`, Story 7's `ReplyBox`, Story 1's `fixturePatch`, part 1's `Collapsible`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens diff -out "$d"'`
- **Review:** light (a port of `changes.mjs` over Story 4's read)
- **Form:** brief (a port of `changes.mjs`)

This story registers the `changes` panel and ports the classic `Changes` (`internal/web/static/js/changes.mjs`) over Story 4's `usePatch`: the sub-line, then the file count, the counts and the base once the patch is held; loading, and an error with a retry; "No changes since <sha>."; each file as a fold with its status letter, name, directory or source path, and counts or "binary"; an open file's hunks with their cut marks, or the reason it has none; a lone file open on the first load only, the open set kept across refreshes; the refresh error; and the reply box. The tab carries the session's line counts while either is above 0 (spec Part 2 settlement 16); that the tab stays while the panel is on screen, though the session no longer applies, is the shell's rule (Story 6's `SessionTabs`), not this story's. Every line of a diff is drawn as a text node, never as markup, and the daemon's own bounds (5,000 lines a file, 2,000 bytes a line, 1 MiB in all, `internal/web/README.md` Limits) are drawn as the reasons they come with; the story invents no limit of its own. The story reads only: it sends no request but Story 4's diff read, and its only action is the reply box's `session.send` (Story 7). No code here holds a landmine marker, so the developer runs at the default effort. The bundle in `internal/web/dist/` is rebuilt once, in the last task, so `pnpm run dist:check` is stale between tasks 10.1 and 10.2 (no story gate reads it between tasks; the Go tests read the committed bundle).

Rules every task keeps (the app's `internal/web/ui/CLAUDE.md`): components return `ReactElement`; the daemon's strings (paths, hunk headers, diff lines) are drawn as text nodes, never as markup, and no `style` attribute is written; every colour is a token class (`text-working`, `text-turn`, `text-error`, `text-muted-foreground`, `bg-diff-add`, `bg-diff-del`, `border-border`, `bg-card`, …), never a literal, and code and paths take `font-mono`; form text is 16px and touch targets at least 44px (`min-h-11` on a file's row); an unused parameter is never `_`-prefixed; a test mock is typed as `vi.fn<(a: A) => R>(…)`; the repo's react-hooks lint rejects a `setState` called inside an effect and an assignment to a ref during render, so state derived from a prop or a read is adjusted during render (`if (!seeded && …) { setSeeded(true); … }`). Every command below runs from the repo root; a task's test command is `pnpm -C internal/web/ui exec vitest run <file>` and its typecheck is `pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint`.

### Task 10.1: The view and its file rows

**Files:**
- Create: `internal/web/ui/src/panels/changes/Changes.tsx`
- Create: `internal/web/ui/src/panels/changes/changes.test.tsx`

Behaviour and names (contract: Story 4's `usePatch`, `PatchView`; Story 6's `SubLine`; Story 7's `ReplyBox`; part 1's `Collapsible`, `CollapsibleTrigger`, `CollapsibleContent`, `Button`; the classic `Changes`, `File` and `splitPath` in `changes.mjs`):

`Changes.tsx` exports `function Changes(props: { session: Session }): ReactElement`, this story's own name, drawn by the panel's `Body` (Task 10.3). It reads `usePatch({ session: session.id, signature: <status>:<added>:<removed>, live: session.status === "Running" })`, the signature being `` `${session.status}:${session.diff.added}:${session.diff.removed}` ``, so a change of status or counts reads the diff again once a patch is held and a Running session reads it every 10 s (both are `usePatch`'s). It draws, in a column with `gap-3 px-4 pb-6`, in this order:

- The sub-line. While no patch is held: `<SubLine session={session} />`. Once one is held, a row (`flex flex-wrap gap-x-3 text-xs text-muted-foreground`) of: `<n> file` or `<n> files` (`1 file`, `0 files`, `3 files`; `patch.files` may be null, which is none); the counts, `+<added>` in `text-working` and `−<removed>` (U+2212) in `text-error`, each the sum of the files' `added` and `removed`; and `since <base>` where `<base>` is the first 7 characters of `patch.base`, left out when `patch.base` is empty.
- No patch held: a block with `<p>` `Loading…` in `text-muted-foreground` while `error` is `""`, or the error's text in `text-error` with a `Button` (`variant="secondary"`) `retry` that calls `reload()` when it is not.
- A patch held with no files: `<p>` `No changes since <base>.` (`No changes.` when the base is empty), muted and centred.
- The files, one fold each, in the patch's order, keyed by their index and path (a path is not unique across a rename and a copy): a `Collapsible` whose `open` is whether the path is in the open set and whose `onOpenChange` adds or removes it. Its `CollapsibleTrigger` is the row (`flex min-h-11 w-full items-center gap-2 text-left`): the status letter (`file.status`, drawn as the daemon sent it, in `font-mono font-bold`: `A` `text-working`, `D` `text-error`, `R` and `C` `text-turn`, any other `text-muted-foreground`), the name (the path after its last `/`, `font-mono`) with its sub-text (`text-xs text-muted-foreground`, truncated, on the same block) and, at the right, the counts. The sub-text is `from <old_path>` for a status `R` or `C` whose `old_path` is not empty, else the path's directory up to and including its last `/`, or `./` for a path with no `/`. The counts are `binary` (muted) for a binary file, else `+<added>` (`text-working`) when `added` is above 0 and `−<removed>` (`text-error`) when `removed` is above 0, a space between when both. Its content, `CollapsibleContent`, is `<FileBody file={file} />`; **in this task `FileBody` is a stub** that draws nothing, which Task 10.2 replaces.
- The open set is state of `Changes` (a `Set` of paths), so it survives every refresh. When the first patch ever held has exactly one file, that file's path starts in it; a patch held later never adds one (a refresh does not reopen a file the reader closed). It is adjusted during render with a `seeded` state (see the rules above), not in an effect.
- When a patch is held and `error` is not `""` (a refresh failed): `<p>` `Could not refresh: <error> · ` followed by an inline `<button type="button">retry</button>` that calls `reload()`, `text-xs text-muted-foreground`.
- Last, `<ReplyBox session={session} />` (Story 7), which draws the box, "Resume the session to reply." or nothing by itself.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/changes/changes.test.tsx`. Setup (once, at the top of the file): `vi.useFakeTimers()` and `queryClient.clear()` (`@/api/queries`) in `beforeEach`, with `vi.useRealTimers()`, `vi.restoreAllMocks()`, the store reset (`useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 })`), `useDrafts.getState().clear()` and `window.history.replaceState(null, "", window.location.pathname)` in `afterEach`; `import "@/commands"` (the registry `ReplyBox`'s send button needs). `diff = vi.spyOn(api, "diff")` (`@/api/request`) re-created in `beforeEach` with `.mockRejectedValue(new Error("unexpected diff read"))`. Helpers: `ok = <T,>(value: T): Outcome<T> => ({ kind: "ok", status: 200, value, notices: [] })` and `refused = (status: number, message: string): Outcome<never> => ({ kind: "refused", status, message })`; `wrapper` is a `QueryClientProvider` over `queryClient`; `settle(ms = 1)` is `await act(async () => { await vi.advanceTimersByTimeAsync(ms); })`; `deferred<T>()` is a promise the test resolves. `NOW = Date.parse("2026-11-18T12:00:00Z")`. `show(id = 1, over: Partial<Session> = {}): Session` stores `fixtureSnapshot(NOW)` with session `id` edited by `over` through `useFleet.getState().apply("snapshot", snapshot)` then `useFleet.setState({ stale: false, link: "live" })`, sets the hash to `#/s/<id>/diff`, and returns the edited session. `draw(session)` is `render(<Changes session={session} />, { wrapper })`. `patchOf(...files: Partial<File>[]): Patch` builds `{ base: "a1b2c3d4e5f6", files }` over a file with `path: "a.go"`, `old_path: ""`, `status: "M"`, `binary: false`, `added: 1`, `removed: 0`, `hunks: []`, `omitted: ""`. Canned session 1 is Running with `+212 −40`; the canned patch (`fixturePatch()`, Story 1's) holds five files: `ui/pane.go` (M, +12 −3), `ui/header.go` (R from `ui/title.go`, +40 −2), `assets/cursor.png` (A, binary), `ui/legacy.go` (D, −31) and `ui/tables_gen.go` (M, +5200, omitted `lines`). Tests, in `describe("Changes: the sub-line, loading and errors")` and `describe("Changes: the files")`:

  - `says loading with the session's sub-line before the patch lands`: `diff` returns a `deferred` promise; `draw(show())`; the text `Loading…` and the branch `aidanb/fix-pane-cursor` are drawn, and no `files` count. Fails without: the Loading branch or the `SubLine` fallback.
  - `draws the file count, the summed counts and the short base once the patch is held`: the canned patch lands: the text `5 files`, `+5252`, `−36` and `since a1b2c3d` are drawn, and the session's branch is not. Fails without: the sums (the counts would read the session's `+212 −40`), `base.slice(0, 7)`, or replacing the sub-line.
  - `says 1 file for one`: a patch of one file draws `1 file`, not `1 files`. Fails without: the singular.
  - `leaves the base out of the sub-line when the patch has none`: `{ base: "", files: [one file] }` draws no `since`. Fails without: the empty-base check.
  - `says why a read failed, with a retry that reads again`: `diff` resolves `refused(409, "session is busy")` once, then the canned patch: `session is busy` is drawn in the error block with a button `retry`; clicking it (`fireEvent.click`, then `settle()`) draws `5 files` and the error is gone. Fails without: the error text from `usePatch`, the `retry` button, or its `reload` call.
  - `says no changes since the base for an empty patch`: `{ base: "a1b2c3d4e5f6", files: [] }` draws `No changes since a1b2c3d.`; `files: null` the same; `{ base: "", files: [] }` draws `No changes.`. Fails without: the empty branch, its base text, or `files ?? []` (the `null` case throws).
  - `keeps the patch and says the refresh failed`: draw the canned patch; `rerender` with the session's `diff.added` raised by 1 (a new signature) while `diff` resolves `refused(409, "session is busy")`; after `settle()` the five files are still drawn and `Could not refresh: session is busy` with an inline `retry` button is drawn; the next `diff` resolving the canned patch and a click on that `retry` clears the line. Fails without: the signature making `usePatch` read again (the line never appears), the held patch being kept, or the retry button.
  - `draws each file's status, name, directory or source and counts`: in the canned patch's rows (found by `getAllByRole("button", { name: /<name>/ })`), `pane.go` holds `M`, `ui/` and `+12`, `−3`; `header.go` holds `R`, `from ui/title.go`, `+40` and `−2`; `cursor.png` holds `A`, `assets/` and `binary` and no `+`; `legacy.go` holds `D`, `−31` and no `+`; `tables_gen.go` holds `+5200` and no `−`. Fails without: the `R`/`C` source text, the binary form, or either count's `> 0` test (`legacy.go` would read `+0`).
  - `a path with no directory has ./ and a copy names its source`: `patchOf({ path: "README.md" }, { path: "b.go", old_path: "a.go", status: "C" })` draws `./` in the first row and `from a.go` in the second. Fails without: the `./` default, or `C` in the source rule (the second would read its directory).
  - `a status this build does not know is drawn as sent`: `patchOf({ status: "X" as File["status"] })` draws `X`. Fails without: a lookup table of letters with no fallback.
  - `every file is closed at first when there are several`: with the canned patch every file's trigger has `aria-expanded` `false`. Fails without: the lone-file seed opening every file.
  - `a lone file starts open`: a patch of one file: its trigger has `aria-expanded` `true`. Fails without: the seed.
  - `a lone file opens on the first load only`: the first read lands two files (all closed); the `rerender` with a new signature reads a patch of one file: that file's trigger is `aria-expanded` `false`. Fails without: the `seeded` flag (the second patch's lone file opens).
  - `the open set survives a refresh`: the canned patch lands, `pane.go`'s trigger is clicked (`aria-expanded` `true`); `rerender` with a new signature and the same patch again (`settle()`): it is still `true`; clicking it again makes it `false` and a third refresh leaves it `false`. Fails without: the open set held in a place a refresh resets (a key that changes with the patch, or state inside a file component keyed by the patch).
  - `draws the reply box under the files`: a Running session draws a textbox named `Reply`; `show(1, { status: "Paused", group: "paused" })` draws the files and `Resume the session to reply.`. Fails without: `Changes` not drawing `ReplyBox`.
  - `reads the diff every 10 s while the session runs, and not otherwise`: with the canned patch always answering, a Running session's `diff` is called once on mount and twice after `settle(10_000)`; `show(1, { status: "Ready", group: "your_turn" })` is called once and still once after `settle(30_000)`. Fails without: `live` set from the status (the first stays at one call, or the second keeps reading).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/changes/changes.test.tsx`
  Expected: `Failed to resolve import "./Changes" from "src/panels/changes/changes.test.tsx". Does the file exist?`

- [ ] **Step 3: Implement** `internal/web/ui/src/panels/changes/Changes.tsx` as the behaviour above says, with the `FileBody` stub.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/changes/Changes.tsx src/panels/changes/changes.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/changes/changes.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```
  git add -N internal/web/ui/src/panels/changes/Changes.tsx internal/web/ui/src/panels/changes/changes.test.tsx
  git commit -m "feat(web): the changes view's sub-line, files and refresh rules" -- internal/web/ui/src/panels/changes/Changes.tsx internal/web/ui/src/panels/changes/changes.test.tsx
  ```

### Task 10.2: An open file's hunks and the reasons it has none

**Files:**
- Modify: `internal/web/ui/src/panels/changes/Changes.tsx`
- Test: `internal/web/ui/src/panels/changes/changes.test.tsx`

Behaviour and names (the classic `Body`, `HunkView` and `lineClass` in `changes.mjs`; `File`, `Hunk` and `FileOmitted` are `@/api/gen`'s):

Replace Task 10.1's `FileBody` stub (it draws nothing) with the real `FileBody({ file }: { file: File }): ReactElement`, which draws the file's content when it is open:

- **The reason, when the file has no hunks to draw**, one `<p className="px-1 py-2 text-xs text-muted-foreground">`, chosen in this order: `omitted` `lines` is `Too long to show here (<added + removed> changed lines).`; `omitted` `budget` is `Not shown: the diff is over 1 MiB.`; any other non-empty `omitted` (a value from a newer daemon) is `Not shown (<omitted>).`; `binary` is `Binary file.`; no hunks (`hunks` null or empty) is `No text changes.`.
- **Otherwise the hunks**, in a horizontally scrolling block (`overflow-x-auto rounded-lg border border-border bg-card font-mono text-[12px] leading-5`) whose inner column is `w-max min-w-full` and holds, for each hunk in order (keyed by index), a header row then one row per line (keyed by index), every row `whitespace-pre px-2` and at least one line high (`min-h-5`, so an empty line keeps its height):
  - the header row: the hunk's `header` split by the pattern `^(@@[^@]*@@)(.*)$` into the ranges (`text-turn`) and the section after them (`text-muted-foreground`); a header that does not match is drawn whole;
  - a line (`hunk.lines`, null read as none) is drawn as the text the daemon sent, its row's class by its first character: `+` `bg-diff-add`, `-` `bg-diff-del`, `\` `text-muted-foreground`, any other (a context line, an empty line) no background; when the line's index is in `hunk.cut` (null read as none) a `<span className="text-muted-foreground">…</span>` follows the text. A line is never put into the page as markup.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/changes/changes.test.tsx`, in a new `describe("Changes: an open file")` with the file's setup and helpers. A helper `open(name: RegExp)` clicks the file row's trigger found by `screen.getByRole("button", { name })` and returns it. Text of a diff line is read with `screen.getByText(text, { normalizer: (s) => s })` (the default normalizer collapses the tab and space runs the lines hold). Tests:

  - `opening a file draws its hunk header and lines`: the canned patch; `open(/pane\.go/)`: the header's ranges `@@ -88,9 +88,12 @@` and its section ` func (p *Pane) View() string {` are drawn as two elements (the ranges element has class `text-turn`), and the lines `+\tif p.cursorVisible() {` and `-\tp.drawCursor(frame)` are drawn. Fails without: the `FileBody` (nothing is drawn), or the header split (one element holds both).
  - `an added line, a removed line and a context line have their own rows`: in `pane.go`'s hunk the row of `+\tif p.cursorVisible() {` has class `bg-diff-add`, the row of `-\tp.drawCursor(frame)` has `bg-diff-del`, and the row of `  \tframe := p.render()` has neither. Fails without: the class by first character, or either class swapped.
  - `a "no newline" marker is muted`: `patchOf` a file with a hunk line `\ No newline at end of file`: its row has class `text-muted-foreground` and neither diff class. Fails without: the `\` case.
  - `a header that is not a hunk range is drawn whole`: a hunk whose `header` is `not a range` draws that text in one element. Fails without: the non-match branch (the header is dropped or throws).
  - `a cut line ends in a mark`: a hunk with `lines: ["+a", "+b"]` and `cut: [1]`: the row of `+b` holds a span `…` and the row of `+a` holds none; `cut: null` draws no span and does not throw. Fails without: the `cut` lookup by index (both or neither carry the mark), or the null check.
  - `an empty line keeps its row`: a hunk line `""` draws a row with class `min-h-5`. Fails without: the row's minimum height.
  - `a deleted file draws its removed lines`: `open(/legacy\.go/)` draws `-package ui` and `-// legacy is gone.`. Fails without: treating a `D` file as one with nothing to show.
  - `a file too long to show says how many lines`: `open(/tables_gen\.go/)` draws `Too long to show here (5200 changed lines).` and no hunk; `patchOf({ omitted: "lines", added: 5200, removed: 7 })` open draws `(5207 changed lines)`. Fails without: the `lines` reason, or the sum (`added` alone reads 5200 in the second case).
  - `a file the diff budget cut says so`: `patchOf({ omitted: "budget" })` open draws `Not shown: the diff is over 1 MiB.`. Fails without: the `budget` reason.
  - `an omitted reason from a newer daemon is drawn as sent`: `patchOf({ omitted: "huge" as File["omitted"] })` open draws `Not shown (huge).`. Fails without: the fallback reason (the file draws as one with no hunks, `No text changes.`).
  - `a binary file says so`: `open(/cursor\.png/)` draws `Binary file.`. Fails without: the `binary` reason.
  - `an omitted reason beats a binary flag`: `patchOf({ binary: true, omitted: "lines", added: 9 })` open draws `Too long to show here (9 changed lines).` and not `Binary file.`. Fails without: the order of the reasons.
  - `a text file with no hunks says there are no text changes, for an empty and a null list`: `patchOf({ hunks: [] })` and `patchOf({ hunks: null })` open draw `No text changes.`. Fails without: the final reason, or the null check.
  - `a lone file's hunks are drawn on the first load`: a patch of one file with the canned `pane.go` hunk draws `@@ -88,9 +88,12 @@` with no click. Fails without: the seed from Task 10.1 reaching the content.
  - `diff lines are drawn as text, never as markup`: a hunk line `+<b>bold</b><img src=x>` opens to a row whose text is exactly that string and holds no `b` and no `img` element (`container.querySelector("b, img")` is null). Fails without: any path that puts the line into the page as HTML.

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/changes/changes.test.tsx`
  Expected: the new tests fail (`Unable to find an element with the text: Binary file.`, `Unable to find … @@ -88,9 +88,12 @@` and the like, as the stub draws nothing); Task 10.1's tests pass.

- [ ] **Step 3: Implement** the real `FileBody` in `internal/web/ui/src/panels/changes/Changes.tsx` as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/changes/Changes.tsx src/panels/changes/changes.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/changes/changes.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```
  git commit -m "feat(web): an open file's hunks, with their cut marks, or why it has none" -- internal/web/ui/src/panels/changes/Changes.tsx internal/web/ui/src/panels/changes/changes.test.tsx
  ```

### Task 10.3: The tab's mark, the panel, and the rebuilt bundle

**Files:**
- Modify: `internal/web/ui/src/panels/changes/Changes.tsx`
- Create: `internal/web/ui/src/panels/changes/index.tsx`
- Modify: `internal/web/ui/src/panels/index.ts`
- Modify: `internal/web/dist/` (rebuilt)
- Test: `internal/web/ui/src/panels/changes/changes.test.tsx`

Behaviour and names (contract: the `changes` panel; `PanelTab`'s `Mark: ComponentType<{ session: Session }>` (Story 6); Story 3's `sessionActions`; Story 6's `SessionHead`, `NoSession`; part 1's `definePanel`, `useSession`, `PanelProps`; `panels/index.ts` is quoted by the next panel's story, so only a line is added to it):

- `Changes.tsx` also exports `function ChangesMark(props: { session: Session }): ReactElement`, the tab's mark: when `session.diff.added` or `session.diff.removed` is above 0 a `<span className="ml-1.5 font-mono text-xs">` of `+<added>` in `text-working` and `−<removed>` (U+2212) in `text-error`, a space between; else an empty fragment (`<></>`). It reads the session it is handed, which is the live one, so the counts move with the stream.
- `panels/changes/index.tsx`: defines a private `function ChangesBody(props: PanelProps): ReactElement` that reads `useSession(props.sessionId)`, draws `<NoSession />` when it is undefined, else `<Changes key={session.id} session={session} />` (the key gives each session its own open set and held patch state), and calls `definePanel({ id: "changes", title: () => "Changes", appliesTo: (s) => sessionActions(s).diff, tab: { order: 2, label: "changes", Mark: ChangesMark }, Head: SessionHead, Body: ChangesBody })`. It sets no `Strip`. `sessionActions` is from `@/commands/session`.
- `panels/index.ts`: after the lines the earlier panel stories left (`import "./fleet";`, the `turns` panel's and, if Story 9's has landed, `import "./info";`), add the line `import "./changes";`. Do not change any other line of the file.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/changes/changes.test.tsx`: add `import { panelFor } from "@/app/panels";`, `import "@/panels";` (the registration `main.tsx` triggers) and `ChangesMark` to the `./Changes` import, and add `describe("ChangesMark")` and `describe("the changes panel")` with the file's setup:

  - `ChangesMark draws the added and removed counts`: `render(<ChangesMark session={show(1)} />)` draws `+212` and `−40` (U+2212), the first in a `text-working` element and the second in a `text-error` one. Fails without: the counts, their order or their tones.
  - `ChangesMark draws one count when the other is 0`: a session with `diff: { added: 5, removed: 0 }` draws `+5` and `−0`; `{ added: 0, removed: 3 }` draws `+0` and `−3`. Fails without: the `either above 0` rule written as `both`.
  - `ChangesMark draws nothing for no changes`: `diff: { added: 0, removed: 0 }` draws an empty container. Fails without: the zero check (the span `+0 −0` is drawn).
  - `is registered as the changes tab, second, with a session head and no strip`: `panelFor("changes")` is defined; its `tab` is `{ order: 2, label: "changes", Mark: ChangesMark }`; its `Head` is `SessionHead`; its `Strip` is undefined; `title` of `{ sessionId: 1, params: { id: "1" }, search: {} }` is `Changes`. Fails without: the `import "./changes"` line in `panels/index.ts` (`panelFor` is undefined), or a field of the registration.
  - `applies to a session whose diff can be read, and to no other`: `appliesTo` of `show(1)` (Running) is true; of a session with `status: "Ready"`, `group: "your_turn"` true; of `status: "Loading"` false; of `status: "Deleting"` false; of `status: "Paused"`, `group: "paused"` false; of `status: "Recoverable"`, `group: "paused"` false. Fails without: `appliesTo` (every session gets the tab) or its use of `sessionActions` (a hand-copied rule drifts: the Paused session is the case that separates `status` from `group`).
  - `the Body draws the changes of a session the snapshot lists`: `diff` returns the canned patch; render the panel's `Body` with `{ sessionId: 1, params: { id: "1" }, search: {} }`: after `settle()` the text `5 files` is drawn. Fails without: `ChangesBody` drawing `Changes`.
  - `the Body says the session is gone for one the snapshot does not list`: after `show(1)`, the Body with `sessionId: 99` draws the status `This session is gone.` and `diff` was never called. Fails without: the `NoSession` branch.
  - `a different session does not inherit the open files`: `show(1)` (the canned snapshot lists session 7 too) and `diff.mockImplementation(async (id) => ok(id === 1 ? patchOf({ path: "ui/pane.go" }) : fixturePatch()))`; render the panel's Body with `sessionId: 1` (the lone `ui/pane.go` starts open) and `settle()`; `rerender` it with `sessionId: 7` and `settle()`: the `pane.go` trigger is `aria-expanded` `false`. Fails without: the `key={session.id}` (the first session's open set, which holds `ui/pane.go`, carries over and opens it).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/changes/changes.test.tsx`
  Expected: the new tests fail (`ChangesMark` is not exported, and the first panel test with `expected undefined not to be undefined` on `panelFor("changes")`); the earlier tests pass.

- [ ] **Step 3: Implement** `ChangesMark`, `panels/changes/index.tsx` and the `panels/index.ts` line as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/changes/Changes.tsx src/panels/changes/index.tsx src/panels/changes/changes.test.tsx src/panels/index.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/changes/changes.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Rebuild the bundle and run the story's Verify**

  Run: `pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens diff -out "$d"`
  Expected: the build writes `internal/web/dist/`; `check` passes every test, typecheck, lint and format; `dist:check` prints no stale-bundle error; `internal/web` prints `ok` (its first-load budget test holds: the changes view stays in the entry, by design); webshot prints `diff` and a file under `$d` with no line starting `  !` (no console error under the real CSP), the screen showing the file count and counts, the five files with the deleted and the too-long one among them, and the reply box. A `  !` line, or a miss on a request, means a view sent a request on load or wrote a `style` attribute: fix the view.

- [ ] **Step 6: Run the story gates**

  Run: `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi' && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test -timeout 30m ./...`
  Expected: no output from gofmt, vet silent, every package `ok`.

- [ ] **Step 7: Commit** (`git add -N` the new files; a rebuilt bundle renames its hashed assets)

  ```
  git add -N internal/web/ui/src/panels/changes/index.tsx internal/web/dist
  git commit -m "feat(web): the changes panel, its tab's line counts, and the bundle with it" -- internal/web/ui/src/panels/changes/Changes.tsx internal/web/ui/src/panels/changes/index.tsx internal/web/ui/src/panels/changes/changes.test.tsx internal/web/ui/src/panels/index.ts internal/web/dist
  ```

## Story 11: The subagent panel

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/panels/agent/index.tsx`, `internal/web/ui/src/panels/agent/Agent.tsx`, `internal/web/ui/src/panels/agent/agent.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/dist/`
- **Interfaces:** exposes the `agent` panel, `AgentHead`, `AgentStrip`; uses Story 4's `useTurnPages` and `useHeldTurns`, Story 5's `TurnList`, Story 6's tab rules, `NoSession` and `ContextRow`'s `onBack`, part 1's `ContextRow` and `hrefFor`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens agent -out "$d"'`
- **Review:** light (a port of `agent.mjs` over Story 4's read)
- **Form:** brief (a port of `agent.mjs`; Task 11.2's risk, the 30 s gone timer and the once-more refresh's order against the poll's stop, is UI timing over Story 4's single-flight read, and the run's full review checks it: the owner, 2026-11-19)

This story registers the `agent` panel, the new app's port of the classic subagent screen (`internal/web/static/js/agent.mjs`, `strip.mjs:42-55`), over Story 4's turn pages with the agent's id (spec Part 2 settlement 10). `AgentHead` is the context row: the way back to the conversation, through history when the conversation was the previous page, and the title from the page's agent. `AgentStrip` is the line under the session's tabs, by the page's `agent.state`. The body draws the loading, error, gone, starting and turn-list states with the classic poll rules (every 5 s while the agent runs or is starting, once more when it finishes), through the one `useTurnPages` hook, whose pages for a subagent leave the cache with their last reader (not kept, as the settlement says). The shell (Story 6) draws the head, the session's tabs lit on the conversation, and the strip; the body has no reply box and no needs-you box. The story edits no shell file: `ContextRow`'s `onBack` is Story 6's. None of its code holds a landmine marker. The bundle in `internal/web/dist/` is rebuilt once, in the last task, so `pnpm run dist:check` is stale between tasks 11.1 and 11.2 (no story gate reads it between tasks; the Go tests read the committed bundle).

### Task 11.1: AgentStrip and AgentHead

**Files:**
- Create: `internal/web/ui/src/panels/agent/Agent.tsx`
- Create: `internal/web/ui/src/panels/agent/agent.test.tsx`

Behaviour and names (contract: `AgentHead`, `AgentStrip`; Story 4's `useHeldTurns`; Story 6's `ContextRow` `onBack`; part 1's `ContextRow`, `useSession`, `hrefFor`, `routeOf`, `ago`; the classic ones are `AgentHead` in `agent.mjs:55-75` and `AgentStrip` in `strip.mjs:42-55`):

- `panels/agent/Agent.tsx` imports `ContextRow` from `@/app/shell`, `hrefFor` and `routeOf` from `@/app/routes`, `type PanelProps` from `@/app/panels`, `useHeldTurns` from `@/api/reads`, `ago` from `@/panels/fleet/SessionRow` and `useSession` from `@/store/fleet`.
- The previous page, private to the file. At module level, `let previousHash = ""` and `window.addEventListener("hashchange", (e: HashChangeEvent) => { previousHash = hashOf(e.oldURL); })`, where `hashOf(url: string): string` is the text from the first `#` on (`""` when there is none; no `URL` parsing, so a malformed `oldURL` can't throw). The module is imported when the app starts (through `panels/index.ts`), so it sees every in-app navigation: the app's own links are plain anchors, which fire `hashchange`. `function cameFromConversation(session: number): boolean` is `routeOf(previousHash)` being the `turns` panel with `params.id` equal to `String(session)`; `routeOf` normalises the `#s/<id>` and `#/s/<id>/turns` forms.
- `AgentHead(props: PanelProps): ReactElement`: `const id = props.sessionId ?? 0` (an agent route always has one; `0` matches no session), `useSession(props.sessionId)` and `useHeldTurns(id, props.params.agent)`. Without the session: `<ContextRow back={hrefFor("fleet")} title="" />`. With it: `<ContextRow back={hrefFor("turns", { id: String(id) })} onBack={…} title={…} />`. The title is the first non-empty of the page agent's `description` and `type`, else `"subagent"` (empty strings, not only `undefined`, fall through: do not write it with `??`). `onBack` is a handler that, when `cameFromConversation(id)`, calls `e.preventDefault()` and `window.history.back()` (the conversation's scroll comes back with the entry), and otherwise does nothing, so the link navigates to the conversation. `ContextRow` words the back link from the `turns` panel's tab label (`conversation`).
- `AgentStrip(props: PanelProps): ReactElement`: the session by `useSession(props.sessionId)` and the page's agent by `useHeldTurns(id, props.params.agent)`; an empty fragment when either is missing. Otherwise `<div data-slot="agent-strip" className="flex min-h-11 items-center gap-3 px-4 text-xs text-muted-foreground">` holding `<span className="min-w-0 flex-1 truncate">` and, only while `session.group === "needs_you"`, the link `<a href={hrefFor("turns", { id: String(session.id) })} className="flex min-h-11 flex-none items-center text-attention">● session needs you ›</a>` (a sibling of the span, never nested in a link). The span holds the lead, then ` · <type>` when `agent.type` is non-empty. The lead by `agent.state`: `running` is `<span className="text-working">` with `◐ subagent running` and, when `ago(agent.started, Date.now())` is non-empty, a space and that age; `finished` is a muted `○ idle` for a `teammate` and `✓ finished` otherwise; any other state is `subagent`.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/agent/agent.test.tsx`. Imports: `act`, `fireEvent`, `render`, `screen` from `@testing-library/react`; `QueryClientProvider` from `@tanstack/react-query`; `afterEach`, `beforeEach`, `describe`, `expect`, `it`, `vi` from `vitest`; `type AgentPage`, `type Outcome`, `type Turns` from `@/api/gen`; `queryClient` from `@/api/queries`; `useTurnPages` from `@/api/reads`; `api` from `@/api/request`; `definePanel` from `@/app/panels`; `fixtureSnapshot`, `fixtureTurns` from `@/fixtures`; `useFleet` from `@/store/fleet`; `AgentHead`, `AgentStrip` from `./Agent`. Helpers, which Task 11.2 extends: fake timers `vi.useFakeTimers()` and `vi.setSystemTime(NOW)` (`NOW = Date.parse("2026-11-18T12:00:00Z")`) in `beforeEach`, with `queryClient.clear()`, `turnsRead = vi.spyOn(api, "turns")` (`mockResolvedValue(ok(page()))` by default) and the store loaded with `useFleet.getState().apply("snapshot", fixtureSnapshot(NOW))`; `afterEach` restores real timers and mocks and resets the store (`useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 })`) and the hash (`window.history.replaceState(null, "", window.location.pathname)`). `ok = <T,>(value: T): Outcome<T> => ({ kind: "ok", status: 200, value, notices: [] })`; `AGENT = "a1f00dcafe0001"`; `props = (id: number) => ({ sessionId: id, params: { id: String(id), agent: AGENT }, search: {} })`; `page(agent: Partial<AgentPage> = {}, over: Partial<Turns> = {}): Turns` is `fixtureTurns(true)` with `over` applied and its `agent` merged with `agent` (the fixture's page is the running `recon` agent "Map every caller of the cursor path", started 3 minutes ago); `settle(ms = 1)` is `await act(async () => { await vi.advanceTimersByTimeAsync(ms); })`; `wrapper` provides `queryClient`. Because the shell mounts the head and the strip beside the body, which reads the page, the tests here use `function Reader({ session }: { session: number }): null` that calls `useTurnPages({ session, agent: AGENT, live: false })` in its place. `definePanel({ id: "turns", title: () => "Conversation", tab: { order: 1, label: "conversation" }, Body: () => null })` registers the panel the back link is worded from. `navigated(oldURL: string)` dispatches `Object.assign(new Event("hashchange"), { oldURL })` on `window`. Tests, in `describe("AgentHead")` and `describe("AgentStrip")`:

  - `AgentHead` — `titles the row with the agent's description, then its type, else "subagent"`: `it.each` over `{ description: "Map every caller of the cursor path", type: "recon" }` → `Map every caller of the cursor path`, `{ description: "", type: "recon" }` → `recon`, `{ description: "", type: "" }` → `subagent`; each draws `<AgentHead {...props(1)} />` and `<Reader session={1} />`, and its `heading` reads `subagent` before `settle()` and the expected title after. Fails without: the title chain (the order or the `subagent` fallback) or reading the page through `useHeldTurns`.
  - `AgentHead` — `has a way back named conversation, to the session's conversation`: the link's name matches `conversation` and its `href` is `#/s/1`. Fails without: the `back` href.
  - `AgentHead` — `goes back through history when the conversation was the previous page`: `it.each` over the previous pages `http://localhost/#/s/1`, `http://localhost/#s/1` and `http://localhost/#/s/1/turns`; after `navigated(url)`, with `window.history.back` spied (`vi.spyOn(window.history, "back").mockImplementation(() => undefined)`), `fireEvent.click` on the link returns `false` (the navigation was cancelled) and `back` was called once. Fails without: the `onBack` handler (`back` never called, the click returns true) or `routeOf`'s normalisation (the `#s/1` and `/turns` forms don't match).
  - `AgentHead` — `follows the link when the previous page was not this conversation`: `it.each` over `http://localhost/#/s/1/info`, `http://localhost/#/s/2` and `http://localhost/#/`; after `navigated(url)` the click returns `true` and `back` was not called. Fails without: the panel term or the id term of `cameFromConversation` (the info tab, or another session's conversation, would go back).
  - `AgentHead` — `draws the fleet as the way back, with no title, for a session the snapshot does not list`: `<AgentHead {...props(99)} />`: the link's `href` is `#/`, and the `heading` has no text. Fails without: the missing-session branch (the link goes to `#/s/99` and the title reads `subagent`).
  - `AgentStrip` — `says what the agent is doing by its state, then its type`: `it.each` over the page's agent, `{ state: "running" }` → `◐ subagent running 3m · recon`, `{ state: "finished" }` → `✓ finished · recon`, `{ state: "finished", teammate: true }` → `○ idle · recon`, `{ state: "unknown" }` → `subagent · recon`, `{ state: "unknown", type: "" }` → `subagent`; each draws `<AgentStrip {...props(1)} />` with `<Reader session={1} />` (`session={2}` for session 2's) and reads the `[data-slot="agent-strip"]` element's text after `settle()` (`turnsRead` returns `ok(page(agent))` for the case). Fails without: any branch of the lead, the age, or the ` · type` join.
  - `AgentStrip` — `links to the conversation only while the session needs you`: session 2 (group `needs_you`, `<AgentStrip {...props(2)} />`) has the link `● session needs you ›` with `href` `#/s/2`, and the link is not inside another link; session 1 (working) has none. Fails without: the `needs_you` term (the working session shows it) or the link (the needs-you session lacks it).
  - `AgentStrip` — `draws nothing before the page lands or for a session the snapshot does not list`: session 1 before `settle()` and session 99 after it: no `[data-slot="agent-strip"]` element. Fails without: the missing-page or missing-session guard (the strip draws `subagent`).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/agent/agent.test.tsx`
  Expected: the file fails to load with `Failed to resolve import "./Agent"`.

- [ ] **Step 3: Implement** `Agent.tsx` as the behaviour above says (`AgentHead`, `AgentStrip`, the previous-page tracker), exporting both components.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/agent/Agent.tsx src/panels/agent/agent.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/agent/agent.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```
  git add -N internal/web/ui/src/panels/agent/Agent.tsx internal/web/ui/src/panels/agent/agent.test.tsx
  git commit -m "feat(web): the subagent's head, with its way back through history, and its strip" -- internal/web/ui/src/panels/agent/Agent.tsx internal/web/ui/src/panels/agent/agent.test.tsx
  ```

### Task 11.2: The subagent's body

**Files:**
- Modify: `internal/web/ui/src/panels/agent/Agent.tsx` (the import block, then append at the end)
- Modify: `internal/web/ui/src/panels/agent/agent.test.tsx`

Behaviour and names (contract: `useTurnPages` and `useHeldTurns` (Story 4), `TurnList` (Story 5), `NoSession` (Story 6); the classic `Agent` in `agent.mjs:77-164` and its `GONE_AFTER` of 30 s; spec Part 2 settlement 10):

- Add to `Agent.tsx`'s imports `useCallback`, `useEffect`, `useRef`, `useState` and `type ReactElement` as needed from `react`, `useTurnPages` from `@/api/reads`, `Button` from `@/components/ui/button`, `TurnList` from `@/conversation/TurnList` and `NoSession` from `@/session/NoSession`, and `type Session` from `@/api/gen`. The previous-page code and the head and strip stay as Task 11.1 left them.
- `const GONE_AFTER = 30_000` (milliseconds an agent's file may stay unwritten before the screen says its record is gone).
- `function Agent(props: PanelProps): ReactElement`, the panel's `Body`: reads `useSession(props.sessionId)`; without the session it draws `<NoSession />` and reads nothing (a snapshot, which the shell guarantees by now, that no longer lists the session); with it, it draws `<AgentTurns session={session} agent={props.params.agent} />`. `AgentTurns` is private and holds every hook, so the missing-session case issues no request.
- `AgentTurns` derives the screen from the page's agent, read first through `useHeldTurns(session.id, agent)` (the entry `useTurnPages` below writes) so the poll switch can depend on it without a state of its own: `state` is `held?.agent?.state` (or `""`), `missing` is `held?.agent?.missing === true`, `running` is `state === "running"` or (`state === "unknown"` and `session.status === "Running"`), `gone` is `missing` and (`state === "finished"` or the 30 s wait is over), `starting` is `missing` and not `gone`, `live` is `running` or `starting`. It then calls `useTurnPages({ session: session.id, agent, live })` and draws from its `held`, `error` and `load`.
- The 30 s wait, private: `useGoneTimer(missing: boolean, attempt: number): boolean`, true once `missing` has stayed true for `GONE_AFTER` since the page first said so (or since `attempt` last changed): an effect that, while `missing`, sets a `setTimeout` of `GONE_AFTER` that sets a state flag, and whose cleanup clears the timeout and resets the flag; the hook returns the flag and `missing`. `missing` turning false (a page that has the file) and `attempt` changing both restart the wait. State is set only in the timeout and the cleanup, never in the effect body (`react-hooks/set-state-in-effect` refuses that). `attempt` is a `useState` counter that the retry on the gone screen increments.
- Once more when the agent finishes: an effect on `state` with a ref holding the last state; when the last state is non-empty and not `finished` and the new one is `finished`, it calls `load("refresh")` (the page that said so may have been read before the agent's last records), and it then stores the state in the ref. A first page that already says `finished` does not refresh (the ref starts empty).
- The drawing, in the classic order, inside `<section className="flex flex-col gap-3 px-4 py-3">`:
  - no page held (`held` undefined): `<p>` `Loading…` in `text-muted-foreground`, or, with `error` set, that text in `text-error` and a `Button` (`variant="secondary"`) `retry` that calls `load("newest")`;
  - `gone`: `This subagent's record is gone.`, a `retry` `Button` that increments `attempt` and calls `load("newest")`, and the link `‹ conversation` to `hrefFor("turns", { id: String(session.id) })`;
  - `starting`: `Starting…`;
  - otherwise: `Some records could not be read.` when `held.skipped` is above 0; a `↑ earlier turns` `Button` (`variant="secondary"`) calling `load("earlier")` when `held.earlier` is not null; `No turns yet.` when `held.turns` is empty; `<TurnList turns={held.turns} sessionId={session.id} live={running} remote="" />`; and, with `error` set, `Could not refresh: <error> · ` and an inline `retry` button (`variant="ghost"`) calling `load("refresh")`.
  There is no reply box, no needs-you box and no scroll anchoring (the classic subagent screen has none).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/agent/agent.test.tsx`: import `Agent` from `./Agent` and `userEvent` from `@testing-library/user-event` beside the existing imports, and add helpers `refused = (status: number, message: string): Outcome<never> => ({ kind: "refused", status, message })` and `draw(session = 1)` which renders `<Agent {...props(session)} />` in `wrapper` (the body alone: it reads the page itself). Fake timers stay on, so every wait is a `settle(ms)`; `turnsRead` returns `ok(page(...))` as each test sets it. Tests, in `describe("the subagent's screen")`:

  - `says Loading… until the first page lands, then draws the brief`: `turnsRead` pending (a deferred `mockImplementationOnce`): `Loading…` is drawn; after it resolves with `page()` the text `Loading…` is gone and the brief's text `You are mapping callers.` (its first line, from the fixture) and a text matching `/BRIEF/` are drawn. Fails without: the `!held` branch (the first assertion) or the `TurnList` (the second).
  - `shows a failed first read with a retry that loads the newest page`: the first read `refused(500, "boom")`: the text `boom` and a `retry` button; with the next read `ok(page())` a click on it draws the brief and removes `boom`; `turnsRead` was called twice. Fails without: the retry's `load("newest")` (the error stays) or the error text (`Loading…` shows).
  - `draws no reply box and no needs-you box, even when the session needs you`: `draw(2)` (group `needs_you`) with `page()`: the brief is drawn, `screen.queryByRole("textbox")` is null and `screen.queryByText(/Open in Claude/)` is null. Fails without: the body keeping to the turn list (a `ReplyBox` or `NeedsYou` added draws a textbox or the text).
  - `says No turns yet. for a page without turns`: `page({ state: "finished" }, { turns: [] })`: `No turns yet.` is drawn. Fails without: the empty line.
  - `says some records could not be read and pages back to the earlier turns`: `page({}, { skipped: 2, earlier: 5 })`: `Some records could not be read.` and a `↑ earlier turns` button; a click on it calls `turnsRead` with the second argument matching `{ agent: AGENT, before: "5" }`. A page with `skipped: 0, earlier: null` draws neither. Fails without: the notice or the button (absent), the `earlier` guard (the plain page draws the button) or `load("earlier")` (no `before` read).
  - `says Starting… while the agent's record is missing, and gone after 30 seconds`: `page({ state: "running", missing: true })`: `Starting…` after `settle()` and still after `settle(29_000)`; after a further `settle(2_000)` the text is `This subagent's record is gone.`. Fails without: the 30 s timer (never gone) or its length (gone early, or at once).
  - `is gone at once when the agent finished and its record is missing`: `page({ state: "finished", missing: true })`: `This subagent's record is gone.` after `settle()`, a `retry` button and the link `‹ conversation` with `href` `#/s/1`. A finished agent whose record is there (`missing: false`) draws its turns instead. Fails without: the `state === "finished"` term of `gone` (it says `Starting…`) or the `missing` term (the finished agent with a record says gone).
  - `retries a gone record, which restarts the 30 second wait`: gone by the timer as above; with the same missing page returned again, a click on `retry` draws `Starting…` at once (after `settle()`), which holds after `settle(29_000)` and gives way to gone after a further `settle(2_000)`; `turnsRead` was called again with no `after`. Fails without: `attempt` in the timer's reset (it stays gone, or goes gone at once) or `load("newest")` (no new read).
  - `polls every 5 seconds while the agent runs, and once more when it finishes, then stops`: `page({ state: "running" })`: one read after `settle()`, two after `settle(5_000)`; then `turnsRead` returns `ok(page({ state: "finished" }))`: after `settle(5_000)` and `settle()` three reads in all (the poll that learns of the finish, and the one more), and still three after `settle(30_000)`. Fails without: the poll (the second assertion), `live` ending with the run (the count grows past three) or the once-more refresh (only two reads).
  - `does not poll a finished agent, nor refresh for a first page that says finished`: `page({ state: "finished" })`: one read after `settle()` and still one after `settle(30_000)`. Fails without: `live` reading `finished` as running (it polls) or the empty initial ref (a refresh follows the first page: two reads).
  - `polls an unknown state while the session is running`: `page({ state: "unknown" })` with `draw()` (session 1, status `Running`): one read after `settle()` and two after `settle(5_000)`. Fails without: the `unknown` and `Running` term of `running` (the screen never polls).
  - `does not poll an unknown state while the session is not running`: the same page, with the snapshot's session 1 set to status `Ready` before the draw (`const snapshot = fixtureSnapshot(NOW)`, change the session found by `allSessions(snapshot)` from `@/fleet/groups`, then `useFleet.getState().apply("snapshot", snapshot)`): one read after `settle()` and still one after `settle(30_000)`. Fails without: the session half of that term (the ready session polls).
  - `keeps the turns when a refresh fails, with a retry that refreshes`: `page()` then, at the 5 s poll, `refused(502, "boom")`: the brief stays and `Could not refresh: boom` is drawn with an inline `retry`; with the next read `ok(page())` a click on it removes the line. Fails without: the error line (absent), keeping `held` (the screen turns to the error alone) or `load("refresh")` (the line stays).
  - `draws This session is gone. for a session the snapshot does not list, and reads nothing`: `draw(99)`: the text is drawn and `turnsRead` was not called. Fails without: the `NoSession` branch (it reads and draws `Loading…`).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/agent/agent.test.tsx`
  Expected: the new tests fail with `Element type is invalid: expected a string (for built-in components) or a class/function (for composite components) but got: undefined` (`Agent` is not exported yet); the Task 11.1 tests pass.

- [ ] **Step 3: Implement** `Agent` and its private `AgentTurns` and `useGoneTimer` as the behaviour above says, exporting `Agent`.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/agent/Agent.tsx src/panels/agent/agent.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/agent/agent.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```
  git commit -m "feat(web): the subagent's body, with its gone, starting and poll rules" -- internal/web/ui/src/panels/agent/Agent.tsx internal/web/ui/src/panels/agent/agent.test.tsx
  ```

### Task 11.3: Register the panel, and rebuild the bundle

**Files:**
- Create: `internal/web/ui/src/panels/agent/index.tsx`
- Modify: `internal/web/ui/src/panels/index.ts`
- Modify: `internal/web/dist/` (rebuilt)
- Modify: `internal/web/ui/src/panels/agent/agent.test.tsx`

Behaviour and names (contract: the `agent` panel; part 1's `definePanel`; the plan's `panels/index.ts` is quoted by the next panel's story, so only a line is added to it):

- `panels/agent/index.tsx`: imports `definePanel` from `@/app/panels` and `Agent`, `AgentHead`, `AgentStrip` from `./Agent`, and calls `definePanel({ id: "agent", title: () => "Subagent", Head: AgentHead, Strip: AgentStrip, Body: Agent })`. It sets no `tab` and no `appliesTo`: the subagent view is no tab of its own, the shell lights the conversation tab for it (Story 6).
- `panels/index.ts`: after the lines the earlier panel stories left, add the line `import "./agent";`. Do not change any other line of the file.

- [ ] **Step 1: Write the failing test** in `internal/web/ui/src/panels/agent/agent.test.tsx`: add `import { panelFor } from "@/app/panels";` (merge it with the existing `@/app/panels` import) and `import "@/panels";` (the registration `main.tsx` triggers), and, in `describe("the agent panel")`:

  - `is registered with a head, a strip and no tab`: `panelFor("agent")` is defined; its `Head` is `AgentHead`, its `Strip` is `AgentStrip` and its `Body` is `Agent`; its `tab` and `appliesTo` are undefined; `title` of `props(1)` is `Subagent`. Fails without: the `agent` import in `panels/index.ts` (`panelFor` is undefined) or the registration's fields (a `tab` would give the subagent view a tab of its own).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/agent/agent.test.tsx`
  Expected: the new test fails with `expected undefined not to be undefined` on `panelFor("agent")`; the others pass.

- [ ] **Step 3: Implement** `index.tsx` and the `panels/index.ts` line as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/agent/index.tsx src/panels/agent/agent.test.tsx src/panels/index.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/agent/agent.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Rebuild the bundle and run the story's Verify**

  Run: `pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -screens agent -out "$d"`
  Expected: the build writes `internal/web/dist/`; `check` passes every test, typecheck, lint and format; `dist:check` prints no stale-bundle error; `internal/web` prints `ok` (its first-load budget test holds: the subagent view stays in the entry by design); webshot renders the agent screen (the running `recon` agent's brief, its head and its strip) with no console error under the real CSP.

- [ ] **Step 6: Run the story gates**

  Run: `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi' && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test -timeout 30m ./...`
  Expected: no output from gofmt, vet silent, every package `ok`.

- [ ] **Step 7: Commit** (`git add -N` the new files; a rebuilt bundle renames its hashed assets)

  ```
  git add -N internal/web/ui/src/panels/agent/index.tsx internal/web/dist
  git commit -m "feat(web): the subagent panel, registered, and the bundle with it" -- internal/web/ui/src/panels/agent/index.tsx internal/web/ui/src/panels/agent/agent.test.tsx internal/web/ui/src/panels/index.ts internal/web/dist
  ```

## Story 12: The job and mockup panels, each a chunk of its own

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/src/app/shell.tsx`, `internal/web/ui/src/app/shell.test.tsx`, `internal/web/ui/src/panels/job/index.tsx`, `internal/web/ui/src/panels/job/JobBody.tsx`, `internal/web/ui/src/panels/job/job.test.tsx`, `internal/web/ui/src/panels/mockup/index.tsx`, `internal/web/ui/src/panels/mockup/MockupBody.tsx`, `internal/web/ui/src/panels/mockup/mockup.test.tsx`, `internal/web/ui/src/panels/index.ts`, `internal/web/static_budget_test.go`, `internal/web/dist/`
- **Interfaces:** exposes the `job`, `mockup` and `mockupFull` panels, `JobHead`, `PanelHost`'s `Suspense`, `TestDist_LazyViewsAreChunksOfTheirOwn`; uses Story 3's job commands, Story 4's `useJobLog`, Story 6's `SessionHead`, `NoSession` and `useMarkSeen`, Story 7's `ReplyBox`, Story 9's job helpers, part 1's `api.mockupURL`, `Skeleton`, `CommandButton`, `ContextRow` and `ago`
- **Verify:** `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/ && d=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d"'`
- **Review:** deep
- **Form:** brief (two ports and the lazy wiring; the frame's sandbox, scripts only and never same-origin, and the two chunks are pinned by named tests)

This story gives `PanelHost` a `Suspense` and registers the job and mockup views with lazy bodies, so neither loads on a first launch (spec §15, Part 2 settlement 14). The job view ports `internal/web/static/js/job.mjs`: `JobHead` (back to info, the job's title), the facts by state, the command and submitter, the controls by state as Story 3's job commands, and the log over Story 4's `useJobLog`, following the bottom unless the reader has scrolled up more than 40 px. The mockup views port `internal/web/static/js/mockup.mjs`: the sandboxed frame, the strip with its age, a reload button and the full-screen link, the reply box, the empty notice, and the full view with only its frame and "done"; both mark the session seen. A Go test pins that the two chunks exist and that `index.html` names neither. No code here holds a landmine marker. Every task changes only `internal/web/ui/src/` until Task 12.4, which adds the Go test and rebuilds `internal/web/dist/`: `pnpm run dist:check` is stale between Tasks 12.1 and 12.3 (no story gate reads it; the Go tests read the committed bundle, which still passes its budget tests).

### Task 12.1: PanelHost's Suspense

**Files:**
- Modify: `internal/web/ui/src/app/shell.tsx` (the `react` import at the top; `PanelHost` and its comment, after `Placeholder`)
- Test: `internal/web/ui/src/app/shell.test.tsx`

Behaviour and names (contract: `PanelHost`'s `Suspense`; part 1's `Skeleton`; Story 6 leaves `PanelHost` as it is, so the lines quoted below are today's):

- `app/shell.tsx`, the `react` import block: add `  Suspense,` as a line directly after `  Component,`.
- `app/shell.tsx`, imports: add `import { Skeleton } from "@/components/ui/skeleton";` directly after the line `import { Button } from "@/components/ui/button";`.
- `app/shell.tsx`, a private `function BodyLoading(): ReactElement` placed directly above `PanelHost`'s comment: the fallback of a lazy body, `<div role="status" aria-label="Loading" className="flex flex-col gap-3 px-4 py-6">` holding two `Skeleton`s (`className="h-6 w-1/2"` and `className="h-32 w-full"`).
- `app/shell.tsx`, `PanelHost`: replace the lines

  ```
      <Boundary key={resetKey(panel, props)} what={entry.title(props)}>
        <entry.Body {...props} />
      </Boundary>
  ```

  with

  ```
      <Boundary key={resetKey(panel, props)} what={entry.title(props)}>
        <Suspense fallback={<BodyLoading />}>
          <entry.Body {...props} />
        </Suspense>
      </Boundary>
  ```

  and extend its comment (`// PanelHost draws the panel registered under `panel` inside an error` / `// boundary, or the placeholder when none is.`) with a sentence: a body that is `React.lazy` suspends under a skeleton, and its chunk failing to load reaches the boundary, which offers a reload. The `Suspense` stays inside the `Boundary`, so a failed load is the boundary's and a pending one never is.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/app/shell.test.tsx`, in the existing `describe("PanelHost")`. Add the import lines `import { lazy, type ComponentType } from "react";` (merge with the file's existing `react` type import) and use `await act(async () => { … })` (already imported) and `findBy`. Panels register into a global registry, so each test registers its own `new` panel (an id no other test in the file registers: `definePanel({ id: "new", title: () => "Lazy", Body })`). Tests:

  - `draws a skeleton while a lazy body loads, then the body`: build `const gate = new Promise<{ default: ComponentType<PanelProps> }>((resolve) => { release = resolve; })`, register `Body: lazy(() => gate)`, `render(<PanelHost panel="new" props={props} />)`. Assert `screen.getByRole("status", { name: "Loading" })` is drawn and the text `loaded` is not; then `await act(async () => { release({ default: () => <p>loaded</p> }); })` and assert `loaded` is drawn and the status is gone. Fails without: the `Suspense` removed from `PanelHost` (React suspends the root and nothing is drawn, so `getByRole("status", { name: "Loading" })` throws), or the fallback not carrying the `Loading` name.
  - `shows what failed and a reload when a lazy body's chunk does not load`: `console.error` spied and restored in `finally` (as the existing throwing-body test does); register `Body: lazy(() => Promise.reject(new Error("chunk gone")))` with title `() => "Lazy"`; `render`, then `await screen.findByRole("alert")` has text `Lazy failed to draw` and `chunk gone`, and a `Reload` button is drawn. Fails without: the `Boundary` removed from around the `Suspense` (the rejection reaches the test as an uncaught error, no alert is drawn).
  - `a body that is not lazy is drawn at once, with no skeleton`: register `Body: () => <p>plain</p>`; after `render`, `plain` is drawn and `queryByRole("status", { name: "Loading" })` is null. Fails without: the fallback drawn always (a `Suspense` replaced by a wrapper that always shows its fallback).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx`
  Expected: the first two new tests fail (no status named `Loading`; no alert, the rejection thrown); the third and every existing test pass.

- [ ] **Step 3: Implement** the import lines, `BodyLoading` and `PanelHost`'s `Suspense` as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/app/shell.tsx src/app/shell.test.tsx && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/app/shell.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```bash
  git commit -m "feat(web): PanelHost suspends a lazy body under a skeleton" -- internal/web/ui/src/app/shell.tsx internal/web/ui/src/app/shell.test.tsx
  ```

### Task 12.2: The job panel

**Files:**
- Create: `internal/web/ui/src/panels/job/index.tsx`
- Create: `internal/web/ui/src/panels/job/JobBody.tsx`
- Modify: `internal/web/ui/src/panels/index.ts`
- Create: `internal/web/ui/src/panels/job/job.test.tsx`

Behaviour and names (contract: the `job` panel, `JobHead`; Story 3's `job.start`, `job.park`, `job.unpark`, `job.pause`, `job.resume` (each with args `{ job: number }`, keyed by the job's id) and `@/commands/testkit`'s `seed`, `makeSession`, `makeJob`, `resetFleet`, `noticesOk`; Story 4's `useJobLog` and `HeldLog` from `@/api/reads`; Story 6's `NoSession` (`@/session/NoSession`) and `ContextRow` with the tab rules (the job route lights `info`); Story 9's `jobGlyph`, `jobTone`, `elapsed` and `jobEnd` from `@/session/jobs`; part 1's `definePanel`, `useSession`, `hrefFor`, `ContextRow` (`@/app/shell`), `CommandButton`, `ago` (`@/panels/fleet/SessionRow`); the classic `job.mjs`):

`panels/job/index.tsx`:

- `export function JobHead(props: PanelProps): ReactElement`: reads `useSession(props.sessionId)`. Without the session: `<ContextRow back={hrefFor("fleet")} title="" />`. With it: `<ContextRow back={hrefFor("info", { id: String(session.id) })} title={…} />`, the title the job's `description`, else its `command`, else `job <n>` (`n` is `props.params.job`), the job being `session.jobs` (null is empty) entry whose `id` is `Number(props.params.job)`. The session's tabs under it, with info lit, are the shell's (Story 6).
- `definePanel({ id: "job", title: (props) => "Job " + (props.params.job ?? ""), Head: JobHead, Body: JobBody })`, `JobBody` being `lazy(() => import("./JobBody"))` (`lazy` from `react`). It has no `tab` and no `appliesTo`.
- `panels/index.ts`: after the lines the earlier panel stories left, add the line `import "./job";`. Do not change any other line of the file.

`panels/job/JobBody.tsx` (default export `function JobBody(props: PanelProps): ReactElement`, which `lazy` needs):

- The session is `useSession(props.sessionId)`; without it the body is `<NoSession />`. With it the body is a private `JobScreen({ session, job }: { session: Session; job: number })`, `job` being `Number(props.params.job)`, so every hook of the screen is called under one session. The job is the entry of `session.jobs ?? []` whose `id` is `job`, possibly absent.
- A private `isLive(job: Job): boolean` is true for `running`, `queued` and `held` (classic `live`). `live` is `job !== undefined && isLive(job)`. The log is `useJobLog({ session: session.id, job, live })`, read whether or not the session lists the job (a 409 then says it has left the queue).
- The facts, drawn only when the session lists the job, in a column (`flex flex-col gap-1 text-[13px]`): the first line is the glyph in a `<span aria-hidden="true">` then `job <id> · <state text>`; the glyph is `jobGlyph(job)` in the text-colour class `jobTone(job)` (Story 9's, by the job's state; the body keeps no mapping of its own). The state text (private `stateText(job, now)`, `now` read with `Date.now()` at each render, as classic does) is, by state:
  - `running`: `running`, `elapsed(Date.parse(job.started), now)`, `paused` when `job.paused` and `job.pause_reclaimed`, `paused, CPU only` when `job.paused` and not reclaimed, and `forced` when `job.forced`, with empty parts left out and the rest joined by ` · `;
  - `queued` or `held`: the state then `job.waiting`, empty parts left out, joined by ` · `;
  - any other state (an unknown one included): `jobEnd(job)`, then `ran <elapsed(Date.parse(job.started), Date.parse(job.ended))>` only when `started` and `ended` are both not empty and that elapsed is not `""`, then the age `ago(job.ended, now)` when `job.ended` is not empty and the age is not `""` (as `<age> ago`, except that `now` stays `now`), empty parts left out, joined by ` · `.
  Under the first line: `job.command` in `font-mono` when `job.description` is not empty; `submitted by subagent <agent>` (`text-xs text-muted-foreground`) when `job.agent` is not empty; then the controls.
- The controls (private `Controls({ job }: { job: Job })`): while `job.control` is not `""`, only `<p className="text-xs text-muted-foreground">` `Pausing…` when it is `pause`, else `Resuming…` (no buttons and no error). Otherwise the buttons the state allows, each `<CommandButton id=… args={{ job: job.id }} label=… />` in a row (`flex gap-2 pt-2`): `queued` offers `job.start` (`label` `Start now`, `variant="default"`); `queued` and `held` offer `job.unpark` (`label` `Unpark`) when `job.parked`, else `job.park` (`label` `Park`); `running` offers `job.resume` (`label` `Resume`) when `job.paused`, else `job.pause` (`label` `Pause`); the secondary ones are `variant="secondary"`. No button, the row is not drawn. `job.control_error`, when not empty, is a `<p className="text-xs text-muted-foreground">` under the row, drawn only when the row is. The buttons are commands, so the stale gate, the sheet and the toast are `runCommand`'s.
- The log, under the facts: `log.gone` not empty draws `<p>` with that text; else when `log.log` is undefined it draws `log.error` when that is not empty, else `Loading…`; else when `log.log.text` is `""`: `No output yet.` while `live`, else `No output.`; else, `Earlier output not shown.` first when `log.log.cut`, then `<pre className="mt-2 overflow-x-hidden rounded-lg border border-border bg-card p-2 font-mono text-[11px] leading-[1.45] break-words whitespace-pre-wrap">{text}</pre>`. When `log.log` is defined and `log.error` is not empty, `log.error` is drawn as a muted line above the `<pre>` too (the held text stays). The text is a text node, never HTML. The notices are muted text (`text-[13px] text-muted-foreground`, `px-0 py-2`).
- Following the bottom: a ref `follow`, true on mount; a `scroll` listener on `window` (added in a `useEffect`, passive, removed on cleanup) that sets `follow.current = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 40`; and a `useEffect` over the log (`log.log`) that, when `follow.current`, calls `window.scrollTo(0, document.documentElement.scrollHeight)`. A reader more than 40 px above the bottom is left where they are when output arrives; within 40 px, or back at the bottom, the page follows again. No ref is read or written during render (react-hooks/refs).
- Layout: `<section className="flex flex-col gap-2 px-4 pb-6">`.

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/job/job.test.tsx`. Set up once: `import "@/commands";`; `vi.mock("@/api/reads", async (original) => ({ ...(await original<typeof import("@/api/reads")>()), useJobLog: vi.fn() }))` so each test sets `vi.mocked(useJobLog).mockReturnValue({ log: undefined, gone: "", error: "", loading: false })` (type `JobLogView`) and the tests read what the screen passes it; `vi.mock("@/app/ConfirmSheet", async (original) => ({ ...(await original<typeof import("@/app/ConfirmSheet")>()), askConfirm: vi.fn() }))`; `NOW = Date.parse("2026-11-19T12:00:00Z")` with `vi.useFakeTimers({ toFake: ["Date"] })` and `vi.setSystemTime(NOW)` in `beforeEach` and `vi.useRealTimers()`, `vi.restoreAllMocks()`, `resetFleet()` and `window.history.replaceState(null, "", window.location.pathname)` in `afterEach` (and `askConfirm`'s `mockReset`); a helper `at(ms: number): string` giving the ISO time `ms` before `NOW`; a helper `show(jobs: Job[], job: number, over: Partial<Session> = {}): RenderResult` that `seed`s `[makeSession({ id: 1, status: "Running", group: "working", jobs, ...over })]`, sets the hash with `window.history.replaceState(null, "", "#/s/1/job/<job>")` and renders `<JobBody sessionId={1} … />` as `<JobBody sessionId={1} params={{ id: "1", job: String(job) }} search={{}} />`, where `JobBody` is the default import of `./JobBody` (`PanelProps` is `{ sessionId, params, search }`); jobs are `makeJob({ … })`. Group the tests in `describe("JobBody facts")`, `describe("JobBody controls")`, `describe("JobBody log")`, `describe("JobHead")` and `describe("the job panel")`. Tests:

  - facts: `a running job says how long it has run, paused and forced`: `makeJob({ id: 214, state: "running", started: at(130_000), paused: true, pause_reclaimed: false, forced: true })` draws `job 214 · running · 2m 10s · paused, CPU only · forced`; with `pause_reclaimed: true` the text has `paused` and no `CPU only`; with `paused` and `forced` false it is `job 214 · running · 2m 10s`. Fails without: the `paused`, `CPU only` or `forced` parts, or the empty-part filter.
  - facts: `a queued or held job says its state and why it waits`: queued with `waiting: "waiting for 6 free cores"` draws `job 216 · queued · waiting for 6 free cores`; held with no `waiting` draws `job 215 · held`. Fails without: the `waiting` part or the empty-part filter.
  - facts: `a finished job says how it ended, how long it ran and when`: `done`, `exit: { code: 0 }`, `started` `at(540_000)`, `ended` `at(240_000)` draws `job 212 · exit 0 · ran 5m 0s · 4m ago`; the same with `ended` `at(10_000)` and `started` `at(70_000)` ends `· ran 1m 0s · now` and has no `now ago`; with `started` and `ended` empty it is `job 212 · exit 0` and has no `ran`. Fails without: the `ran` part and its both-times check (the third reads `ran ` or `ran NaNs`), the `ago` part, or the `now` exception.
  - facts: `a job whose state this build does not know shows its raw state, a neutral glyph and no buttons`: `state: "paused"` (cast to `Job["state"]`) with no `exit` draws `job 214 · paused`, the glyph `?` and no `button`. Fails without: `jobEnd`'s state fallback, `jobGlyph`'s neutral mark, or the controls offering buttons for a state they do not know.
  - facts: `draws the command under a described job and the submitter of a subagent's job`: `description: "race tests"`, `command: "go test -race ./ui/..."`, `agent: "a1f00dcafe0001"` draws both `go test -race ./ui/...` and `submitted by subagent a1f00dcafe0001`; with `description: ""` and `agent: ""` neither line is drawn (the command is the head's title already). Fails without: the `description` check (the command repeats) or the `agent` line.
  - facts: `draws no facts for a job the session does not list, and still reads its log`: `show([], 999)` draws no `job 999 ·` text and `useJobLog` was called with `{ session: 1, job: 999, live: false }`. Fails without: the facts drawn from an absent job (a throw), or the log read gated on the job being listed.
  - controls: a parameterised test `offers the buttons its state allows` over a table: queued, not parked → `Start now`, `Park`; queued, parked → `Start now`, `Unpark`; held → `Park`; held, parked → `Unpark`; running → `Pause`; running, paused → `Resume`; `done` → none; `failed` → none; `cancelled` → none. Each asserts the exact set of button names. Fails without: any state's branch, or a parked or paused flag ignored.
  - controls: `shows Pausing… or Resuming… in place of the buttons while a control is in flight`: a running job with `control: "pause"` draws `Pausing…` and no `button`, even with `control_error: "x"`; `control: "resume"` draws `Resuming…`. Fails without: the in-flight branch (the buttons stay) or the two words swapped.
  - controls: `draws the last control's error under the buttons`: a running job with `control_error: "refused"` draws `refused` and a `Pause` button; a finished job with `control_error: "refused"` draws neither. Fails without: the `control_error` line, or its being drawn with no buttons.
  - controls: `Start now asks first, naming what the job waits for, and then posts start`: `vi.mocked(askConfirm).mockResolvedValue("")` and `vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk())`; a queued job 216 with `waiting: "waiting for 6 free cores"`; click `Start now`; `askConfirm` was called with a spec whose `title` is `Start job 216 now?` and whose `body` holds `waiting for 6 free cores`, and `api.jobControl` was called with `(1, 216, "start")`. With `askConfirm` resolving `null` nothing is posted. Fails without: the button not being the `job.start` command with `{ job: job.id }` (`api.jobControl` is called with another job or the sheet is skipped).
  - controls: `Pause posts pause for this job`: a running job 214, click `Pause`; `api.jobControl` was called with `(1, 214, "pause")` and `askConfirm` was not called. Fails without: the button's `args` (the job's id) or its id.
  - controls: `the buttons are off while the snapshot is stale`: after `show`, `useFleet.setState({ stale: true })` (`@/store/fleet`): the `Pause` button is disabled. Fails without: the buttons being plain `<button>`s that ignore the stale gate (`CommandButton` is what disables them).
  - log: a parameterised test `reads the log live while the job has yet to end` over states `running`, `queued` and `held` (live true) and `done`, `failed`, `cancelled`, `lost` (live false): `useJobLog` was called with `{ session: 1, job: 214, live }`. Fails without: any of `running`, `queued` or `held` missing from `isLive`, or a finished state counted live.
  - log: `draws the log as text, never as HTML`: a log `{ text: "<b>bold</b> & more", end: 18, cut: false }` draws a `pre` whose `textContent` is exactly `<b>bold</b> & more` and no `b` element in the container. Fails without: the text set as HTML.
  - log: `says earlier output was cut, from the fixture's log`: a log built from `fixtureJobLog()` (`{ text: page.text, end: page.end, cut: page.cut }`) draws `Earlier output not shown.` and the text `TestPane_CursorDrawnAfterFrame`; with `cut: false` the notice is not drawn. Fails without: the `cut` notice or its condition.
  - log: `says there is no output yet while the job runs and no output once it ended`: a log with `text: ""` for a running job draws `No output yet.`; for a done job `No output.`. Fails without: the `live` choice of words.
  - log: `says Loading… before the first read, the error when it failed, and why a job is gone`: `log: undefined` draws `Loading…`; with `error: "Could not read the job's log."` that text and not `Loading…`; with `gone: "This job is no longer in the queue."` that text, even while a held log exists (no `pre`). Fails without: any of the three branches, or `gone` not taking the body over.
  - log: `keeps the held log and says the read failed when a later read fails`: `log: { text: "kept", end: 4, cut: false }` with `error: "Could not read the job's log."` draws both `kept` and the error. Fails without: the error replacing the held text.
  - log: `draws This session is gone. for a session the snapshot does not list`: `resetFleet()` then `seed([])` and render the body for `sessionId` 1: the text is drawn and `useJobLog` was not called. Fails without: the `NoSession` branch.
  - follow: define a helper `placeReader(fromBottom: number)` that defines `window.innerHeight` as 800, `document.documentElement.scrollHeight` as 5000 and `window.scrollY` as `5000 - 800 - fromBottom` with `Object.defineProperty(…, { value, configurable: true })`, and dispatches `window.dispatchEvent(new Event("scroll"))` inside `act`; `vi.spyOn(window, "scrollTo").mockImplementation(() => undefined)` in `beforeEach` of the describe. A helper `more(text)` gives the next `useJobLog` return (a new `log` object) and `rerender` draws the same element again. Tests:
    - `scrolls to the bottom when the screen opens with output`: render with a log; `scrollTo` was called with `(0, 5000)` (the scrollHeight defined before the render). Fails without: `follow` starting false, or the effect missing.
    - `follows new output while the reader is at the bottom or within 40 px of it`: `placeReader(0)`, new log, `scrollTo` called with `(0, 5000)`; `placeReader(40)` after clearing the mock, new log, called again. Fails without: the threshold written `<` for `<=` at exactly 40 px, or the effect not scrolling.
    - `leaves a reader who scrolled up more than 40 px where they are, and follows again once they are back`: `placeReader(41)`, new log: `scrollTo` not called; `placeReader(0)`, new log: called. Fails without: the threshold written 41 or more, or the listener missing (a reader scrolled up is dragged down), or `follow` never turning true again.
  - `JobHead` — `titles the head by the description, else the command, else job N`: `show` is not used (no body); seed the same way, set the hash, and draw `<JobHead sessionId={1} params={{ id: "1", job: "214" }} search={{}} />`: a job with `description: "ui tests"` has the heading `ui tests`; with `description: ""` and `command: "go vet ./..."` it is `go vet ./...`; for a job the session does not list it is `job 999`. Fails without: the `description || command || job N` order.
  - `JobHead` — `goes back to info, named by the info tab, and to the fleet without the session`: after `definePanel({ id: "info", title: () => "Info", tab: { order: 3, label: "info" }, Body: () => null })`, the link has `href` `#/s/1/info` and the name `‹ info`; with no session in the store the link has `href` `#/` and the heading is empty. Fails without: the back link's target.
  - the job panel — `is registered with a head, a lazy body and no tab`: `panelFor("job")` is defined; its `Head` is `JobHead`; its `tab` and `appliesTo` are undefined; its `title` for `params.job` `214` is `Job 214`. Fails without: the registration or its head.
  - the job panel — `draws the screen through PanelHost once its chunk loads`: `show` is not used; seed a session 1 with a running job 214, set the hash, `render(<PanelHost panel="job" props={{ sessionId: 1, params: { id: "1", job: "214" }, search: {} }} />)`; `await screen.findByText(/job 214 · running/)`. Fails without: `panels/job/index.tsx` not registering the panel (the placeholder is drawn) or its `Body` not resolving to `JobBody`'s default export.

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/job/job.test.tsx`
  Expected: the file fails to load with `Failed to resolve import "./JobBody"` (neither module exists yet).

- [ ] **Step 3: Implement** `JobBody.tsx`, `index.tsx` and the `panels/index.ts` line as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/job/index.tsx src/panels/job/JobBody.tsx src/panels/job/job.test.tsx src/panels/index.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/job/job.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```bash
  git add -N internal/web/ui/src/panels/job/index.tsx internal/web/ui/src/panels/job/JobBody.tsx internal/web/ui/src/panels/job/job.test.tsx
  git commit -m "feat(web): the job panel, its body a lazy chunk" -- internal/web/ui/src/panels/job/index.tsx internal/web/ui/src/panels/job/JobBody.tsx internal/web/ui/src/panels/job/job.test.tsx internal/web/ui/src/panels/index.ts
  ```

### Task 12.3: The mockup panels

**Files:**
- Create: `internal/web/ui/src/panels/mockup/index.tsx`
- Create: `internal/web/ui/src/panels/mockup/MockupBody.tsx`
- Modify: `internal/web/ui/src/panels/index.ts`
- Create: `internal/web/ui/src/panels/mockup/mockup.test.tsx`

Behaviour and names (contract: the `mockup` and `mockupFull` panels; Story 6's `SessionHead`, `NoSession`, `useMarkSeen` (`@/fleet/seen`, with `readSeen`) and `PanelTab`'s `Mark`; Story 7's `ReplyBox`; Story 3's `@/commands/testkit` (`seed`, `makeSession`, `resetFleet`); part 1's `definePanel`, `useSession`, `hrefFor`, `api.mockupURL`, `ago` (`@/panels/fleet/SessionRow`), `Button`; the classic `mockup.mjs` and `internal/web/static/app.css:724-780`; the daemon's `internal/web/mockup.go` serves the route under `mockupCSP`, which is why the frame's `sandbox` is never widened):

`panels/mockup/index.tsx`:

- `export function MockupMark({ session }: { session: Session }): ReactElement | null`: the tab's mark, `<span role="img" aria-label="fresh" className="ml-1.5 inline-block size-2 rounded-full bg-attention" />` while `session.mockup !== null && session.mockup.fresh`, else `null`.
- `definePanel({ id: "mockup", title: () => "Mockup", appliesTo: (session) => session.mockup !== null, tab: { order: 4, label: "mockup", Mark: MockupMark }, Head: SessionHead, Body: MockupBody })` and `definePanel({ id: "mockupFull", title: () => "Mockup", Body: MockupFullBody })`, with no `Head` on the second. `MockupBody` is `lazy(() => import("./MockupBody"))` and `MockupFullBody` is `lazy(() => import("./MockupBody").then((m) => ({ default: m.MockupFullBody })))`: two dynamic imports of one module, so Vite builds one chunk, `MockupBody-<hash>.js`.
- `panels/index.ts`: after the lines the earlier panel stories left (including the `import "./job";` of Task 12.2), add the line `import "./mockup";`. Do not change any other line of the file.

`panels/mockup/MockupBody.tsx` (default export `MockupBody` for the lazy import; named export `MockupFullBody`; both `(props: PanelProps) => ReactElement`):

- Both read `useSession(props.sessionId)`, call `useMarkSeen(props.sessionId)` before any branch (it marks on open and on close, so the fleet's NEW mark clears for a fresh mockup reached by a push), and draw `<NoSession />` when the session is undefined.
- A private `Frame({ session, again }: { session: Session; again: number })` draws the mockup: when `session.mockup` is null, `<p className="px-4 py-8 text-center text-muted-foreground">No mockup is showing; the companion may have stopped.</p>`; else `<iframe key={src} src={src} title={"Mockup: " + mk.screen} sandbox="allow-scripts" referrerPolicy="no-referrer" className="min-h-60 w-full flex-1 border-0 border-t border-border bg-background" />`, where `src` is `` `${api.mockupURL(session.id)}?v=${encodeURIComponent(mk.changed)}&r=${again}` ``: `v` is the mockup's change time, so a new screen found at a turn's end reloads the frame, and `r` is the reload count. The `sandbox` is exactly `allow-scripts`, never `allow-same-origin`: the page is HTML a session wrote, and with the origin it could call `/api/v1` with the device cookie. The `key={src}` makes a changed URL a new frame.
- `MockupBody`: keeps `const [again, setAgain] = useState(0)`. `<section className="flex flex-1 flex-col">`: the strip (`flex min-h-11 items-center gap-1 px-4 text-xs text-muted-foreground`): the name (`min-w-0 flex-1 truncate`), `<mk.screen> · <ago(mk.changed)>` or `no mockup` without one; with one, a `Button` (`variant="ghost"`, `size="icon"`, `aria-label="Read the mockup again"`, text `↻`) whose click is `setAgain((n) => n + 1)` and an `<a href={hrefFor("mockupFull", { id: String(session.id) })} aria-label="Full screen">` with text `⤢`, each at least 44 px square (`min-h-11 min-w-11`). The reload acts on nothing in loom (it reads the page again), so it is a plain button as the fleet's header links are plain links, not a command. Then `<Frame />`, then `<ReplyBox session={session} />` (it draws the box while the session can take a reply, `Resume the session to reply.` while paused, and nothing otherwise).
- `MockupFullBody`: `<section className="fixed inset-0 z-40 flex flex-col pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] bg-background">` with `<Frame />` and the link `<a href={hrefFor("mockup", { id: String(session.id) })} className="absolute top-[calc(env(safe-area-inset-top)+6px)] right-2 flex min-h-11 items-center rounded-lg border border-border bg-card px-3 text-[13px]">✕ done</a>`. Only the frame and "done": no strip and no reply box (the shell draws no header or footer on this route).
- No `style` attribute anywhere (the CSP drops it; sizes are classes).

- [ ] **Step 1: Write the failing tests** in `internal/web/ui/src/panels/mockup/mockup.test.tsx`. Set up once: `import "@/commands";` (the reply box's `session.send` button needs it), `NOW = Date.parse("2026-11-19T12:00:00Z")` with `vi.useFakeTimers({ toFake: ["Date"] })` and `vi.setSystemTime(NOW)` in `beforeEach`, and in `afterEach` `vi.useRealTimers()`, `vi.restoreAllMocks()`, `resetFleet()`, `window.localStorage.clear()` and `window.history.replaceState(null, "", window.location.pathname)`; a helper `mockup(over: Partial<Mockup> = {}): Mockup` giving `{ screen: "layout.html", changed: <ISO of NOW minus 2 minutes>, fresh: true, ...over }`; a helper `show(over: Partial<Session> = {}): Session` that `seed`s `[makeSession({ id: 7, status: "Ready", group: "your_turn", mockup: mockup(), ...over })]`, sets the hash `#/s/7/mockup` and returns the session; props `{ sessionId: 7, params: { id: "7" }, search: {} }`. `MockupBody` is the default import and `MockupFullBody` the named import of `./MockupBody`. Tests:

  - `frames the companion's page sandboxed, with the change time and no reload yet`: the `iframe` named `Mockup: layout.html` has `sandbox` exactly `allow-scripts`, `referrerpolicy` `no-referrer` and `src` `/api/v1/sessions/7/mockup?v=` + `encodeURIComponent(<changed>)` + `&r=0`. Fails without: `sandbox` widened (`allow-same-origin` added or the attribute dropped), `referrerPolicy` dropped, or `v` or `r` left out of the URL.
  - `words the strip with the screen and its age, and links the full-screen view`: the strip holds `layout.html · 2m`, and the link named `Full screen` has `href` `#/s/7/mockup/full`. Fails without: the age, or the link's target.
  - `reads the page again when the reload is tapped`: click `Read the mockup again`: the frame's `src` ends `&r=1` and it is a new element (not the node drawn before); a second tap ends `&r=2`. Fails without: the counter being left out of `src`, or `key={src}` (the same node is kept).
  - `loads a new screen when the change time moves`: after drawing, `seed` again with `changed` a minute later and `rerender`: the `src` carries the new `v` and `&r=0`. Fails without: `v` (the URL stays the same and the frame never reloads).
  - `says so when no mockup is showing`: `show({ mockup: null })`: the text `No mockup is showing; the companion may have stopped.` and the strip `no mockup` are drawn, no `iframe`, no `Read the mockup again` button and no `Full screen` link. Fails without: the `mk` check (a throw) or the strip's words.
  - `draws the reply box, or says to resume, or nothing`: the running session `show({ status: "Running", group: "working" })` draws a textbox named `Reply`; `show({ status: "Paused", group: "paused" })` draws `Resume the session to reply.` and no textbox; `show({ status: "Loading", group: "working" })` draws neither. Fails without: `ReplyBox` not drawn, or the paused words missing (each assertion names one).
  - `marks the session seen when it opens and again when it closes`, parameterised over `MockupBody` and `MockupFullBody`: after `render`, `readSeen()["7"]` is `NOW`; advance the clock one minute (`vi.setSystemTime(NOW + 60_000)`), `unmount()`, and it is `NOW + 60_000`. Fails without: `useMarkSeen` not called, or called only on one of the two views.
  - `says the session is gone`, parameterised over both: with no session in the store `This session is gone.` is drawn and no `iframe`. Fails without: the `NoSession` branch (a throw reading `mockup`).
  - `the full view draws only the frame and the way back`: `MockupFullBody`: the `iframe` is drawn with the same `sandbox` and `src`; the link named `✕ done` has `href` `#/s/7/mockup`; there is no `Read the mockup again` button, no `layout.html · ` strip text and no textbox. With `mockup: null` the empty notice is drawn beside the `done` link. Fails without: the strip or the reply box drawn on the full view, or the done link's target.
  - `the tab's mark is a dot while the mockup is fresh`: `render(<MockupMark session={show()} />)` draws an element with role `img` named `fresh`; with `mockup({ fresh: false })` and with `mockup: null` it draws nothing (`container` is empty). Fails without: the `fresh` check (a dot for a stale mockup), or the null check (a throw).
  - `is registered as the fourth tab for a session with a mockup, and the full view has no head`: `panelFor("mockup")` has `tab` `{ order: 4, label: "mockup", Mark: MockupMark }`, `Head` `SessionHead`, and `appliesTo` is true for `show()` and false for `show({ mockup: null })`; `panelFor("mockupFull")` is defined with `Head` undefined and `tab` undefined. Fails without: the registration, the `appliesTo`, or a head on the full view (the header would draw on a screen that has none).
  - `draws both screens through PanelHost once their chunk loads`: `render(<PanelHost panel="mockup" props={…} />)`; `await screen.findByTitle("Mockup: layout.html")` and the strip text; then the same for `panel="mockupFull"` with the `✕ done` link. Fails without: either `Body` not resolving to `MockupBody.tsx`'s exports (the second `lazy` taking the default export would draw the strip on the full view).

- [ ] **Step 2: Run it to see it fail**

  Run: `pnpm -C internal/web/ui exec vitest run src/panels/mockup/mockup.test.tsx`
  Expected: the file fails to load with `Failed to resolve import "./MockupBody"` (neither module exists yet).

- [ ] **Step 3: Implement** `MockupBody.tsx`, `index.tsx` and the `panels/index.ts` line as the behaviour above says.

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui exec prettier --write src/panels/mockup/index.tsx src/panels/mockup/MockupBody.tsx src/panels/mockup/mockup.test.tsx src/panels/index.ts && pnpm -C internal/web/ui run typecheck && pnpm -C internal/web/ui run lint && pnpm -C internal/web/ui exec vitest run src/panels/mockup/mockup.test.tsx'`
  Expected: typecheck and lint print no error, and vitest ends with `Test Files  1 passed (1)` and no failed test.

- [ ] **Step 5: Commit**

  ```bash
  git add -N internal/web/ui/src/panels/mockup/index.tsx internal/web/ui/src/panels/mockup/MockupBody.tsx internal/web/ui/src/panels/mockup/mockup.test.tsx
  git commit -m "feat(web): the mockup panels, their body a lazy chunk" -- internal/web/ui/src/panels/mockup/index.tsx internal/web/ui/src/panels/mockup/MockupBody.tsx internal/web/ui/src/panels/mockup/mockup.test.tsx internal/web/ui/src/panels/index.ts
  ```

### Task 12.4: The two chunks, pinned, and the bundle

**Files:**
- Modify: `internal/web/static_budget_test.go` (the end of the file)
- Modify: `internal/web/dist/` (rebuilt, never edited by hand)

Behaviour and names (contract: `TestDist_LazyViewsAreChunksOfTheirOwn`; the file's `staticAsset`, `nextStaticAssets`, `firstLoadFiles` and the imports it already holds; `regexp`, `strings`, `fmt` and `testing` are already imported there):

- A private `func checkLazyViews(assets map[string]staticAsset) error`, appended at the end of the file: for each of the two names `JobBody` and `MockupBody` it finds the keys of `assets` that match `^/assets/<name>-[^/]+\.js$` (a `regexp.MustCompile` per name, built with `regexp.QuoteMeta`); it is an error when there is none (`the bundle has no lazy chunk for <name>: its panel is imported statically, or the build renamed it`) or more than one. It then reads `/index.html` (an error when absent, as `checkFirstLoad` does) and is an error when the page names either chunk, by its URL or by its file name (`strings.Contains` on the page text for both), or when `firstLoadFiles(page)` lists it (`index.html names the lazy chunk <url>: it would load on every launch`). The names are checked in the fixed order `JobBody`, `MockupBody`, so the first error is stable.
- `TestDist_LazyViewsAreChunksOfTheirOwn`: `require.NoError(t, checkLazyViews(nextStaticAssets(t)))` over the committed bundle.
- `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne` (the detection table: an enforcement test that can silently never fail has none): over synthetic assets, a page `<!doctype html><script type="module" crossorigin src="/assets/index-a.js"></script>` with `/assets/index-a.js`, `/assets/JobBody-b.js` and `/assets/MockupBody-c.js` is clean; without `/assets/MockupBody-c.js` the error names `MockupBody`; without `/assets/JobBody-b.js` it names `JobBody`; with a second `/assets/JobBody-z.js` the error says there is more than one; with the page also holding `<link rel="modulepreload" crossorigin href="/assets/JobBody-b.js">` the error names `/assets/JobBody-b.js` and `launch`; with the page holding `<script>load("MockupBody-c.js")</script>` the error names `MockupBody`; with no `/index.html` the error names `/index.html`.

- [ ] **Step 1: Write the failing tests** `TestDist_LazyViewsAreChunksOfTheirOwn` and `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne` at the end of `internal/web/static_budget_test.go`, the first as above and the second as a table of cases (`name`, `assets`, `want` error substring, `""` for clean) with the assets built by a small closure, as `TestCheckFirstLoad_RefusesAMissingFile` builds its map. Fails without: `TestDist_LazyViewsAreChunksOfTheirOwn`: `MockupBody` or `JobBody` imported statically (Rollup folds it into the entry, so no `MockupBody-*.js` or `JobBody-*.js` is emitted), or a chunk preloaded by `index.html`; `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`: the missing-chunk check, the duplicate check, the `modulepreload` check, or the page-text check removed (each case names one).

- [ ] **Step 2: Run it to see it fail**

  Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestDist_LazyViewsAreChunksOfTheirOwn|TestCheckLazyViews'`
  Expected: FAIL to compile with `undefined: checkLazyViews`.

- [ ] **Step 3: Implement** `checkLazyViews` as the behaviour above says, run the same command and see `TestDist_LazyViewsAreChunksOfTheirOwn` fail with `the bundle has no lazy chunk for JobBody` (the committed bundle predates the two panels) and `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne` pass, then rebuild the bundle:

  Run: `bash -c 'pnpm -C internal/web/ui install --frozen-lockfile && pnpm -C internal/web/ui run build && ls internal/web/dist/assets | grep -E "^(JobBody|MockupBody)-" && git status --short internal/web/dist'`
  Expected: the build succeeds; `ls` prints one `JobBody-<hash>.js` and one `MockupBody-<hash>.js`; `git status` lists the changed or new files under `internal/web/dist/` (the entry script, `index.html` and the two chunks).

- [ ] **Step 4: Run it to see it pass**

  Run: `bash -c 'pnpm -C internal/web/ui run check && pnpm -C internal/web/ui run dist:check && CGO_ENABLED=0 go test ./internal/web/'`
  Expected: `check` (typecheck, lint, prettier, the whole Vitest suite and `node --check public/sw.js`) passes; `dist:check` reports a fresh bundle; `go test` prints `ok  	github.com/aidan-bailey/loom/internal/web`, with `TestDist_FirstLoadIsWithinBudget` still passing (the chunks are outside it: the entry grew only by `PanelHost`'s `Suspense` and the two panels' small heads) and `TestDist_LazyViewsAreChunksOfTheirOwn` passing.

  Then render every screen of the new app under the real policy:

  Run: `bash -c 'd=$(mktemp -d) && CGO_ENABLED=0 go run ./tools/webshot -app next -out "$d" && ls "$d"'`
  Expected: it exits 0 with no console error or CSP violation logged and writes a screenshot per screen, `job` and `mockup` and `mockup-full` among them. Open the `job`, `mockup` and `mockup-full` images: the job shows its facts and log, the mockup its strip and frame, the full view only the frame and `✕ done`; none is a skeleton or the placeholder.

- [ ] **Step 5: Commit**

  ```bash
  git commit -m "test(web): pin the job and mockup views as chunks of their own, and rebuild the bundle" -- internal/web/static_budget_test.go internal/web/dist
  ```

## Story 13: The rules and procedures for the session views

- **Developer:** intermediate-dev
- **Files:** `internal/web/ui/CLAUDE.md`, `docs/claude/web-layer.md`, `docs/claude/INDEX.md`, `internal/web/README.md`
- **Interfaces:** none; uses every earlier story's names
- **Verify:** `CGO_ENABLED=0 go test ./tools/claudemd/`
- **Review:** light (docs, checked by the CLAUDE.md lint)
- **Form:** brief (docs)

This story writes down what part 2 taught, in the three places that load it. It adds two rules to `internal/web/ui/CLAUDE.md` that no earlier story wrote (Story 3 amended the command rule for `group` and Story 6 added the links rule): a read goes through `internal/web/ui/src/api/reads.ts`, and a view the first load does not need is a lazy `Body`. It extends `docs/claude/web-layer.md`'s panel and command procedures, corrects the command procedure's `when` line to read the session's status and group, adds a procedure for a read, and names all of them in `docs/claude/INDEX.md`'s `web-layer.md` row. It describes the new app's session views in `internal/web/README.md`. The story changes no code and no test: the gate is the CLAUDE.md lint (`TestRepo_ClaudeMDStructure`), which checks structure, budgets and that every path and test name the text names resolves, so each task names only paths and tests that Stories 3 to 12 create. Nothing here changes the app, so `internal/web/dist/` is not rebuilt. No landmine marker appears in this story's text.

### Task 13.1: The read rule and the lazy-body rule in internal/web/ui/CLAUDE.md

**Files:**
- Modify: `internal/web/ui/CLAUDE.md` (two bullets inserted into "Rules when modifying this app")

Behaviour: two rules in the file's shape (what to do; why, in one clause; what breaks and how silently; then **Enforced** by a named test or **Convention** with the silent wrong result). Each is one line under 1,200 bytes. Both anchors are lines that no earlier story edits (Story 3 edits the command rule and Story 6 inserts before the classic-app rule), so the insertions do not collide.

- [ ] **Step 1: Add the lazy-body rule.** Insert this bullet immediately after the line beginning `- **Keep the first load within `firstLoadBudget`:` (the budget rule) and before the line beginning `- **Take the type of every JSON shape the daemon sends from `gen.ts``:

  ```
  - **Make a view the first load does not need a panel whose `Body` is `React.lazy` over a module of its own, keep its `Head` in the entry, and let `PanelHost` draw it under its `Suspense`.** A view imported statically lands in the entry script and slows every launch on a phone, and a lazy `Head` leaves the header empty until its chunk loads; the job view (`internal/web/ui/src/panels/job/JobBody.tsx`) and the mockup views (`internal/web/ui/src/panels/mockup/MockupBody.tsx`) are the pattern, and the conversation, info, changes and subagent views stay in the entry because a push opens the conversation. **Enforced** for the job and mockup chunks by `TestDist_LazyViewsAreChunksOfTheirOwn` (`internal/web/static_budget_test.go`); another view imported statically is held only by `TestDist_FirstLoadIsWithinBudget` — a heavier launch until the budget fails.
  ```

- [ ] **Step 2: Add the read rule.** Insert this bullet immediately after the line beginning `- **Take the type of every JSON shape the daemon sends from `gen.ts``:

  ```
  - **Read the daemon from a view only through `internal/web/ui/src/api/reads.ts`, never with a `fetch` or an `api.<client>` call that sends a request from a component of its own; `api.mockupURL`, which only builds a URL, is not one.** The module holds the classic app's refresh rules and its caches in the query cache: one request in flight with a load asked meanwhile run once it lands, no retry (the next poll is the next attempt), the pages and scroll of the last 8 conversations and the logs of the last 5 jobs kept, and an unpair clears them through `queryClient.clear()`; a read a component writes itself refetches on every mount, polls without those rules and keeps a stranger's pages after an unpair. **Enforced** for the rules by `internal/web/ui/src/api/reads.test.ts`; that a component reads only through the module is **Convention** — a screen that polls twice or shows another account's turns.
  ```

- [ ] **Step 3: Run the lint**

  Run: `CGO_ENABLED=0 go test ./tools/claudemd/`
  Expected: `ok`. The file stays far inside its budgets (100 lines, 20,000 bytes, 1,200 bytes a line); `go run ./tools/claudemd -v` shows the headroom. If it reports a path or a test name that does not resolve, a story before this one named it differently: fix the text to the name in the tree, never the tree.

  There is no new test to turn red: the lint guards the file's shape and its names, not that the rules are true.

- [ ] **Step 4: Commit**

  ```
  git commit -m "docs(web): reads go through reads.ts; a view the first load does not need is a lazy Body" -- internal/web/ui/CLAUDE.md
  ```

### Task 13.2: The panel, command and read procedures, and the INDEX row

**Files:**
- Modify: `docs/claude/web-layer.md` ("Adding a panel", "Adding a command", and a new "Adding a read" section after it)
- Modify: `docs/claude/INDEX.md` (the `web-layer.md` row, line 28)

Behaviour: the procedures say what part 2 added, in the file's own prose style (one paragraph a procedure, `;`-joined clauses, paths in full). Each edit below quotes the exact current text it replaces; the replacements are small spans of the paragraphs, never the whole paragraph.

- [ ] **Step 1: Adding a panel, the call.** In `docs/claude/web-layer.md`, replace

  ```
  `definePanel({ id, title, appliesTo, tab, Head, Body })`
  ```

  with

  ```
  `definePanel({ id, title, appliesTo, tab, Head, Strip, Body })`
  ```

- [ ] **Step 2: Adding a panel, the session's frame.** In the same paragraph, replace

  ```
  `internal/web/ui/src/panels/index.ts` imports the module for its registration; reads go through TanStack Query over `api`, with `unwrap`; a screen the first view does not need loads through a dynamic `import()` so the budget test keeps passing;
  ```

  with

  ```
  `internal/web/ui/src/panels/index.ts` imports the module for its registration; a session's view takes its frame from the panel: `tab: { order, label, Mark }` puts a tab in the session's tabs, where `Mark` (a component handed the session) is drawn after the label, `Head` is the context row (`SessionHead`), and `Strip` is a line under the tabs (the fact strip, the subagent's), each of the three in an error boundary of its own, so one that throws leaves the header and the other tabs; a `Body` draws `NoSession` when the snapshot lists no such session, and the conversation, info and mockup views call `useMarkSeen` (`internal/web/ui/src/fleet/seen.ts`) so the fleet's NEW mark clears; a read follows "Adding a read" below; a screen the first view does not need is a lazy `Body` (`Body: lazy(() => import("./JobBody"))`, a default export in a module of its own, as `internal/web/ui/src/panels/job/index.tsx` does) with its `Head` left in the entry: `PanelHost` draws it under a `Suspense` with a `Skeleton`, a chunk that fails to load reaches the panel's error boundary, which offers a reload, and a Go test like `TestDist_LazyViewsAreChunksOfTheirOwn` (`internal/web/static_budget_test.go`) pins that the chunk exists and that `internal/web/dist/index.html` names it nowhere;
  ```

- [ ] **Step 3: Adding a command, the call and `when`.** In the "Adding a command" paragraph, replace

  ```
  `defineCommand({ id, title, icon, offline, when, confirm, run })` in the panel's module; `when` reads only the session's status, its `claude` flag, the route and the link state;
  ```

  with

  ```
  `defineCommand({ id, title, icon, offline, key, when, confirm, run })` in the panel's module, or for a session's actions in `internal/web/ui/src/commands/` (`session.ts`, `asks.ts`, `jobs.ts`), which `internal/web/ui/src/commands/index.ts` imports and `main.tsx` imports in turn (a test that draws a `CommandButton` imports it too); `when` reads only the session's status and group, its `claude` flag, the route and the link state (the group is how a Recoverable or unstarted Ready session is told from a started Ready, as `sessionActions` in `internal/web/ui/src/commands/session.ts` does for every session command);
  ```

- [ ] **Step 4: Adding a command, `key`, `confirm` and `run`.** In the same paragraph, replace

  ```
  `confirm` for anything that can lose work, naming exactly what happens; `run` calls `api.<client>(…)` and hands the outcome to `showOutcome(outcome, what)`;
  ```

  with

  ```
  `confirm` for anything that can lose work, naming exactly what happens: it returns a `ConfirmSpec` (title, body, action, destructive, and an optional `field`, a `TextField` whose `max` is in UTF-8 bytes and past which the sheet disables the action and shows `tooLong`, or a `ChoiceField`, a native select with its first option chosen), or null to run with no sheet (an Allow whose suggestion does not persist, a Merge with no other session); `run(ctx, args, input)` calls `api.<client>(…)` and hands the outcome to `showOutcome(outcome, what)`, `input` being the field's value (`""` with no field); `key(args)` for a command whose args pick the thing it acts on (a job, an ask): the in-flight key is the command's id, the session's id and what `key` returns, so Pause on one job does not block Pause on another, and `useCommandBusy(command, ctx, args)` and `CommandButton`'s `args` read the same key;
  ```

- [ ] **Step 5: Adding a command, the test.** In the same paragraph, replace

  ```
  a Vitest case for `when` across statuses and link states.
  ```

  with

  ```
  a Vitest case for `when` across statuses, groups and link states, and for a command that skips its sheet or holds a field, both sides of it (a sheet's yes with `""` and with a value, a `confirm` that returns null and one that returns a spec).
  ```

- [ ] **Step 6: Adding a read.** Insert this section between the end of the "Adding a command" paragraph and the `## The dev loop` heading:

  ```
  ## Adding a read

  A read a view draws goes in `internal/web/ui/src/api/reads.ts`, never in the component: a hook over `queryClient` that calls `api.<client>(…)` through `unwrap` and holds what it read in the query cache under a key of the session (and the agent or job), so two components with the same key share one held state and one poll, and an unpair's `queryClient.clear()` drops it. Port the classic app's refresh rule from its module under `internal/web/static/js/` (turns, changes, job), and keep it: one request in flight, with a load asked meanwhile run once when it lands; `retry: false`, because a poll or the view's own retry button is the next attempt; a poll through `refetchInterval` only while the screen says `live` (TanStack pauses it while the page is hidden); a merge by the classic rule (`mergeTurns`, `appendLog`), tested as a pure function beside the hook. Bound what outlives its reader: `gcTime`, `KEEP_SESSIONS` and `KEEP_JOBS` (the least recently opened is evicted past them) and `MAX_LOG_CHARS` (the log's length in UTF-16 units, trimmed at the next newline); a new read decides its own bound and writes it as a constant beside them. A 409 or a 504 on a read has a meaning the classic screen words ("This job is no longer in the queue."): keep its text and its effect on the polling. Test it in `internal/web/ui/src/api/reads.test.ts` over a stubbed `api`, with a fake clock for the polls, and give webshot's canned loom the route's fixture (`tools/webshot/canned.go`; a screen sends no request the canned loom cannot answer).
  ```

- [ ] **Step 7: The INDEX row.** In `docs/claude/INDEX.md` line 28, in the `web-layer.md` row, replace

  ```
  a panel or a command in the new app, the dev loop
  ```

  with

  ```
  a panel (a session view's tab `Mark`, `Strip` and `Head`, a lazy `Body`), a command (a sheet's field, a `key`, a `confirm` that returns null) or a read (through `reads.ts`) in the new app, the dev loop
  ```

- [ ] **Step 8: Run the lint**

  Run: `CGO_ENABLED=0 go test ./tools/claudemd/`
  Expected: `ok` (it checks the guides' links and the paths and test names they name; every name above is a path or test that Stories 2 to 12 create, `tools/webshot/canned.go` exists today). If it names a path that does not resolve, correct the text to the path the earlier story created.

  There is no new test to turn red: the lint guards structure, not truth.

- [ ] **Step 9: Commit**

  ```
  git commit -m "docs(web): the procedures for a session view's frame, a command's field and key, and a read" -- docs/claude/web-layer.md docs/claude/INDEX.md
  ```

### Task 13.3: The session views in internal/web/README.md

**Files:**
- Modify: `internal/web/README.md` ("The new app and the switch", and a new section after it)

Behaviour: facts, not rules (README holds facts). Both edits are in the one section the new app already has.

- [ ] **Step 1: Correct what the new app draws.** In `internal/web/README.md`, in "The new app and the switch", replace

  ```
  In this part the new app draws the fleet; every other route draws a placeholder that offers the switch back.
  ```

  with

  ```
  The new app draws the fleet and the session views below; the flow screens, the flows list, new session, settings and pairing draw a placeholder that offers the switch back.
  ```

- [ ] **Step 2: Add the section.** Insert this section immediately before the `## Push` heading:

  ```
  ## The new app's session views

  Each classic session screen is a panel of the new app (`internal/web/ui/src/panels/`), at the hash address `#/s/<id>…` (the classic `#s/<id>…` addresses a push carries are rewritten to it): `turns` (`#/s/<id>`, the conversation: turns, thinking folds, agent links, the needs-you box and its asks, the reply box with its quick replies and typed slash commands, and under the session's tabs the fact strip), `info` (`#/s/<id>/info`: tiles, agents, jobs and the actions with their sheets), `changes` (`#/s/<id>/diff`), `agent` (`#/s/<id>/agent/<agentId>`, a subagent's turns under its own strip), `job` (`#/s/<id>/job/<n>`), and `mockup` and `mockupFull` (`#/s/<id>/mockup` and `…/mockup/full`). The session's tabs are conversation (a Claude session), changes (while the session's diff can be read, with its line counts), info and mockup (while one is set or on screen, with a dot while fresh); the subagent view lights the conversation tab and the job view the info tab, and a session that does not run Claude draws its info at `#/s/<id>`. Every action is a command (`internal/web/ui/src/commands/session.ts`, `asks.ts`, `jobs.ts`) with the classic sheet and words, enabled by `sessionActions` from the session's status and group; Open in Claude is a plain link to the session's `remote_url`, in the session head, the needs-you box and under a truncated text item.

  Reads go through `internal/web/ui/src/api/reads.ts` with the classic app's rules: the conversation's pages are paged back by `before`, refreshed by `after` every 5 s while the session runs and merged as `js/turns.mjs` does, and the pages and scroll of the last 8 conversations are kept; the diff is read on open, again when the session's counts or status change and every 10 s while it runs; a job's log is appended by `after` every 2 s while the job has yet to end and once more when it ends, at most 512 Ki characters, and kept for the last 5 jobs. Drafts of a reply and of an ask's answer live in memory for the page's lifetime (`internal/web/ui/src/store/drafts.ts`) and an unpair clears them with the query cache. The times a session was last opened (the fleet's NEW mark) are `localStorage`'s `loom.seen`, in the classic encoding, so the marks carry across the switch both ways. The job and mockup views' bodies are chunks loaded on demand, apart from the entry (`TestDist_LazyViewsAreChunksOfTheirOwn`), and webshot's canned loom (`tools/webshot/fixture/`) holds the asks, items, jobs and files these screens draw.
  ```

- [ ] **Step 3: Run the lint and the story's Verify**

  Run: `CGO_ENABLED=0 go test ./tools/claudemd/`
  Expected: `ok`; the README's paths and test names resolve (`internal/web/ui/src/store/drafts.ts` is Story 3's, `internal/web/ui/src/api/reads.ts` Story 4's, `internal/web/ui/src/commands/` Story 3's, `TestDist_LazyViewsAreChunksOfTheirOwn` Story 12's).

  There is no new test to turn red: the lint guards the README's links and names, not its prose.

- [ ] **Step 4: Run the story gates**

  Run: `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi' && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test -timeout 30m ./...`
  Expected: no output from gofmt, vet silent, every package `ok`.

- [ ] **Step 5: Commit**

  ```
  git commit -m "docs(web): the new app's session views" -- internal/web/README.md
  ```
