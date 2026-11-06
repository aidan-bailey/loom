# PWA: conversation first, with subagents and thinking

Date: 2026-11-06. Branch `aidanb/improve-pwm-conversation`. Front end, transcript reader, and two optional wire fields; no new Core request, no `rpc.Protocol` bump.

## Problem

The owner named two pains on the phone app's conversation screen (`internal/web/static/js/turns.mjs`):

- **Missing content.** A subagent's work shows as one `Agent` step row: what it read, did and reported is invisible. Claude's thinking is never shown.
- **Navigation.** A session opens on its session screen, whose "Last message" repeats the conversation's newest turn, so there are two views of one story; a push or a fleet row lands on the session screen when the owner wanted to see what Claude has been doing.

Out of scope (owner's choice): a tool step's input and result, images, streaming, in-chat jump navigation, the reply box's behaviour.

## Findings

From the owner's transcripts under `~/.claude/projects` on 2026-11-06 (Claude 2.1.x, Opus/Sonnet/Haiku 5.5):

- **Subagents have transcripts of their own** at `<projects>/<key>/<conversation>/subagents/agent-<agentId>.jsonl`, every record carrying `agentId` and `isSidechain: true`; the first record is the brief (a user prompt). Beside each is `agent-<agentId>.meta.json`, e.g. `{"agentType":"premise-check","description":"…","toolUseId":"toolu_…","parentAgentId":"a06…","spawnDepth":2,"requestShape":"background","finished":true}`. `toolUseId` is the launching `tool_use` block's id in the *parent's* file (the main transcript, or the parent subagent's file when nested). `session/subagent.Meta` notes several key sets across CLI versions: every field is optional.
- The parent's `tool_result` record carries `toolUseResult.agentId` (for a background agent at launch, with `status: "async_launched"`; for a foreground one when it finishes).
- **Thinking is mostly stored empty.** Of 2820 `thinking` blocks in the newest 40 transcripts, 2588 had blank `thinking` text (a signature only); the 232 with text are short progress summaries ("Still in design: items 1 and 2 are worked out…").
- `session/subagent.MetaPath(transcriptPath, agentID)` already composes the meta path and refuses an id holding `/`, `\` or equal to `..`.
- Pushes open `/#s/<id>` (`internal/web/triggers.go`); a fleet row opens `#s/<id>`, or `#s/<id>/mockup` while a mockup is fresh (`fleet.mjs` `opensMockup`).

## Decisions

### Screens and routes (front end)

1. **`#s/<id>` is the conversation.** A fleet row, a push, the status row's `●◆◐` jumps and every other link to a session land there. `#s/<id>/turns` is replaced by `#s/<id>` in place (`history.replaceState`), so old links and history entries still work.
2. **`#s/<id>/info` is the info tab:** today's session screen (`session.mjs`) without the Last message section and without the needs-you box. It keeps the tiles, the Agents section and the actions (pause, resume, push, merge, kill, with their sheets).
3. **Tabs:** conversation · changes · info · mockup. Changes and mockup show under today's conditions (`SessionTabs`). For a session that does not run Claude (`!s.claude`) there is no conversation: `#s/<id>` draws the info screen and the tab row is changes · info (as today, a lone tab draws no row).
4. **The conversation screen**, top to bottom:
   1. **Fact strip**, pinned in the header under the tabs: status glyph, label and age; `+a −r` when the diff has lines; `PR #n` with its checks glyph when there is a PR; the account's 5h share (7d when no 5h) as the fleet row shows it (`rowShare`), amber at ≥80%. One line, ellipsised; a missing fact is left out. The whole strip is a link to `#s/<id>/info`.
   2. **Turns**, as today, plus the agent links and thinking items below.
   3. **Needs-you box** while `s.group === "needs_you"`: the reason, the detail when there are no asks, `Asks`, and Open in Claude — moved unchanged from the session screen into its own component used here.
   4. **Reply box**, as today.
5. **Agent links.** A step whose `agent` is set draws its tool and summary as a link to `#s/<id>/agent/<agent>`, ending in `›`, with `◐` while not `finished` (accent) and nothing once finished. A step without `agent` is a plain row as today.
6. **Thinking.** A `thinking` item draws as a folded `<details>`: summary `▸ thinking: <first line>` dim and italic, the body its full text as plain pre-wrapped text (not markdown). In the newest turn while the session is Running, the newest thinking item starts open.
7. **The subagent screen** (`#s/<id>/agent/<agentId>`, route name `agent`):
   - Context row: `‹ conversation` (a link that calls `history.back()` when the previous entry is this session's, else `#s/<id>`) and the agent's description as the title (its type when no description). The session's tabs show with conversation current.
   - Strip: by the page's `agent.state`, `◐ subagent running <age>` (accent), `✓ finished` (dim; `idle` for a teammate) or `subagent` (dim, state unknown), the age counted from `agent.started`, then the agent type. While the session needs you, the strip ends in `● session needs you ›` (amber), a link to `#s/<id>`.
   - Body: the same turn renderer, the first turn's prompt box labelled `BRIEF`. The brief folds after 12 lines as prompts do.
   - No reply box and no needs-you box.
   - It refreshes the newest turn every 5 s while `agent.state` is `running`, or is `unknown` and the session is Running; and once more when the state turns `finished`. Nested agent steps link to their own screens the same way.
8. **Info's Agents rows** link to `#s/<id>/agent/<id>` when the row carries an `id`.
9. **Scroll on back.** Returning from a subagent screen restores the conversation's scroll position: the conversation keeps its held turns and `scrollY` per session in a module-level map (like the reply drafts) and restores them on mount instead of reloading at the bottom, then refreshes.

### Transcript reader (`session/transcript`)

10. **`Query.Agent string`.** Empty reads the conversation as today. Set, `Read` reads `<dir of path>/<conversation>/subagents/agent-<Agent>.jsonl`, refusing an id that is empty after trimming, holds `/` or `\`, or is `.` or `..` (the same test as `subagent.MetaPath`, shared rather than copied). The file must be a regular file. Paging, refresh, `Reset`, budgets and skipped-line counting are unchanged.
11. **The brief.** In a subagent file, the first user prompt opens a turn with `From: "brief"`. Later prompts in the file (a `SendMessage` continuing the agent) parse as they do in the main file.
12. **Step → agent links.** When a page holds a step whose tool is `Agent` or `Task`, the reader reads the session's `subagents/*.meta.json` once (its directory listing and each file's `toolUseId`, `finished`), and maps `toolUseId → agentId`. It also records, while scanning, each `tool_result` record's `toolUseResult.agentId` against its `tool_use_id`. `Step` gains `Agent string` (meta first, then the result) and `Finished bool`: meta's `finished` when the key is present, else whether the step has a result and its `toolUseResult.status` is not `async_launched`. A step with no agent found keeps `Agent` empty. The listing reads only meta files, each at most 64 KiB, and an unreadable directory or file leaves the affected steps unlinked: the page still answers.
13. **Thinking items.** A `thinking` block with non-blank text becomes `Item{Kind: KindThinking, Text}`, capped at `maxItem` like a text item (`Cut` set when cut), counted towards `maxText`. A blank `thinking` block and any `redacted_thinking` block add nothing.
14. **`Page.Agent *AgentInfo`**, set only when `Query.Agent` is: `ID`, `Type` (meta `agentType`), `Description` (meta `description`, else the brief's first line), `Teammate` (`Meta.IsTeammate`), `State` (`"finished"` or `"running"` from meta's `finished`; `"unknown"` when meta or its key is missing), `Started` (the first record's timestamp). A missing meta file leaves `Type` and `Description` empty. The file's last record can't stand in for the key: in 342 of 350 finished agents sampled, the last assistant record stops on `tool_use` (the handback), not `end_turn`.

### Core, wire and web

15. `core.ReadTurns` passes the query through unchanged; its admit and refusals stay. The new fields (`Query.Agent`, `Step.Agent`, `Step.Finished`, `Item` kind `thinking`, `Page.Agent` with its `State`) are optional wire fields: regenerate `docs/specs/protocol.md` (`go test ./core/rpc -run TestProtocolReference -update`); no `rpc.Protocol` bump, as with the earlier PWA wire additions.
16. **`subagent.View` gains `ID`** (the tracker's agent id), carried on `InstanceView`'s agents and the web's session JSON as `id`.
17. **`GET /api/v1/sessions/{id}/turns?agent=<agentId>`** (`internal/web/reads.go`): the parameter joins the existing query parsing; a malformed id is a 400 naming it. The web JSON gains `agent` and `finished` on steps, the `thinking` item kind, and the page's `agent` object.

## Errors and edge cases

- **Agent file not yet written** (meta lands first): the reader's not-found error reaches the screen, which shows "Starting…" and keeps polling while it has never had a page; after 30 s it shows the error with a retry. The web layer tells this case apart by a distinct error (`transcript.ErrNoAgent`, wrapped), answered as a 404.
- **Record gone** (Claude's cleanup, or a session whose project dir was archived without its `subagents/`): "This subagent's record is gone." with a link back. Whether `claudetmp.Snapshot` carries `subagents/` is checked during planning; if it does not, that is a follow-up, not this spec.
- **Background agents:** the parent's step settles at launch; `Finished` comes from meta, so the link keeps `◐` while the agent runs.
- **Fail closed:** no guessed links. A meta file without `toolUseId` and no `toolUseResult.agentId` means no link.

## Testing

- `session/transcript` table tests on fixture files: `Query.Agent` reads the right file and refuses each bad id; the brief turn; links by meta `toolUseId` and by `toolUseResult.agentId` without meta; `Finished` for a foreground agent, a background one (`async_launched`), and meta's key; an unreadable meta folder leaves steps unlinked and the page answers; thinking kept with text and dropped blank or redacted, and its bytes counted against `maxText`; paging and refresh on a subagent file; `Page.Agent` from meta and without it.
- The opt-in `TestRealClaude_TranscriptReader` gains a case that has Claude launch one subagent and asserts the parent step's `Agent` and the subagent page's brief and `Agent` info (`State` `finished` once it ends).
- `core`: `ReadTurns` passes `Agent` through (`core/reads_test.go`); `View.ID` reaches `InstanceView`.
- `internal/web`: `?agent=` parsing and its 400 (`reads_test.go`); the JSON shape of the new fields; `TestProtocolReference` regenerated.
- Front end: there are no JS unit tests; screens are checked with `go run ./tools/webshot` at the iPhone 15 target against extended fixtures (`tools/webshot/fixture/turns.json` with an agent step, a thinking item and a waiting session; a new subagent page fixture): the conversation with the strip and the needs-you box, a subagent screen running and finished, the info tab, and a non-Claude session's info.
- Gates: `CGO_ENABLED=0 go test ./...`, race on `session/transcript`, `core`, `internal/web`, gofmt, vet, the CLAUDE.md lint; golangci-lint per the root CLAUDE.md.
