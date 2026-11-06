# PWA conversation first, with subagents and thinking: Implementation Plan

> **For agentic workers:** this plan runs supervised: `/flow:draft` writes the stories from this skeleton, then `/flow:supervise` runs them story by story. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a session open on its conversation in the phone app, with a pinned fact strip and an info tab, and let the reader see a subagent's own turns and Claude's non-empty thinking.

**Architecture:** `session/transcript` learns to read a subagent's sibling transcript (`Query.Agent`), to link Agent steps to their subagents through Claude's `agent-*.meta.json` files, and to keep thinking text; `core.ReadTurns` carries the query unchanged, and the web layer exposes it as `?agent=` and new JSON fields. The app's session routes turn conversation-first: `#s/<id>` is the conversation, `#s/<id>/info` today's session screen, `#s/<id>/agent/<agentId>` a subagent's turns drawn by the same renderer.

**Tech Stack:** Go (stdlib `encoding/json`, testify), Preact + htm ES modules with no build step, `tools/webshot` (headless Chromium) for screens.

**Spec:** `docs/superpowers/specs/2026-11-06-pwa-conversation-first-design.md`

## File structure

Go:

- `session/subagent/meta.go`: `ValidAgentID`, `SubagentsDir`; `Meta` gains `ToolUseID`, `Finished`; `MetaPath` uses `ValidAgentID`.
- `session/subagent/tracker.go`: `View` gains `ID`.
- `session/transcript/transcript.go`: `Query.Agent`, `Step.Agent`, `Step.Finished`, `KindThinking`, `FromBrief`, `AgentInfo`, the agent states, `Page.Agent`; `Read` opens the subagent file when asked.
- `session/transcript/records.go`: thinking blocks, the brief, each `tool_result`'s `toolUseResult` (`agentId`, `status`).
- `session/transcript/build.go`: thinking items join a turn's items.
- `session/transcript/agents.go` (new): the meta listing and the step→agent linking (`linkAgents`), and `agentInfo` for a subagent page.
- `session/transcript/*_test.go`, `session/transcript/testdata/` (fixture transcripts and meta files), `session/transcript/realclaude_test.go`.
- `core/reads_test.go`: `ReadTurns` passes `Agent` through.
- `internal/web/reads.go`: `turnsQuery` reads `agent`; `stepJSON`, `itemJSON`, `turnsJSON` gain the new fields.
- `internal/web/fleet.go`: `agentJSON` gains `ID`.
- `internal/web/reads_test.go`, `internal/web/fleet_test.go`.
- `docs/specs/protocol.md`: regenerated, never edited.
- `internal/web/README.md`, `docs/claude/web-layer.md`: the new routes, fields and screens.

App (`internal/web/static/`):

- `js/app.mjs`: `parseRoute` for the new routes; `#s/<id>/turns` replaced by `#s/<id>`.
- `js/screens.mjs`: the `turns`, `info` and `agent` entries.
- `js/chrome.mjs`: `SessionTabs` with conversation · changes · info · mockup.
- `js/strip.mjs` (new): `FactStrip`, `AgentStrip`.
- `js/needs.mjs` (new): `NeedsYou`, moved out of `js/session.mjs`.
- `js/session.mjs`: the session screen becomes `Info` (no last message, no needs-you box); `SessionHead` draws the strip on the conversation route.
- `js/turns.mjs`: `Turns` (the conversation, or `Info` for a non-Claude session), the shared `useTurnPages` and `TurnList`, thinking items, agent links, the held-turns cache for scroll restore.
- `js/agent.mjs` (new): `Agent`, `AgentHead`.
- `js/fleet.mjs`: rows link to `#s/<id>` as today (no change unless the drafter finds a `/turns` link).
- `app.css`: the strip, the thinking fold, agent links.
- `tools/webshot/main.go`, `tools/webshot/canned.go`, `tools/webshot/fixture/turns.json`, `tools/webshot/fixture/agent-turns.json` (new), `tools/webshot/fixture/snapshot.json`.

## Names contract

Go, `session/subagent` (Story 1):

- `func ValidAgentID(id string) bool`: false for an id that is empty after `strings.TrimSpace`, `"."`, `".."` and any id holding `/` or `\`; `MetaPath` returns `""` when it is false.
- `func SubagentsDir(transcriptPath string) string`: `strings.TrimSuffix(transcriptPath, ".jsonl")` joined with `subagents`; `""` for an empty path.
- `Meta` gains `ToolUseID string` (json `toolUseId`) and `Finished *bool` (json `finished`; nil when the key is absent).

Go, `session/subagent` (Story 2):

- `View` gains `ID string` as its first field: the tracker's agent id (`agent.id`).

Go, `session/transcript` (Story 1):

- `Query.Agent string`: empty reads the conversation; set, the subagent's file `SubagentsDir(path)/agent-<Agent>.jsonl`.
- `const FromBrief = "brief"`: a subagent file's first prompt.
- `const KindThinking = "thinking"`: an `Item` with `Text` and `Cut`.
- `Step.Agent string` and `Step.Finished bool`, after `State`.
- `const ( AgentRunning = "running"; AgentFinished = "finished"; AgentUnknown = "unknown" )`
- `type AgentInfo struct { ID, Type, Description string; Teammate bool; State string; Started time.Time; Missing bool }`
- `Page.Agent *AgentInfo`: nil unless `Query.Agent` is set.
- A bad agent id is an error from `Read` whose text is `transcript: agent <id>: not an agent id` (the `%q` of the id).

Web JSON (Story 2), `internal/web/reads.go`:

- Query parameter `agent`; a bad one is refused 400 with `agent "<id>": not an agent id`.
- `stepJSON` gains `Agent string \`json:"agent,omitempty"\`` and `Finished bool \`json:"finished"\``.
- An item of kind `thinking` carries `text` and `truncated` (from `Cut`).
- `turnsJSON` gains `Agent *agentPageJSON \`json:"agent,omitempty"\``; `agentPageJSON` is `{id, type, description, teammate, state, started, missing}` (`started` through `stamp`).
- `internal/web/fleet.go`: `agentJSON` gains `ID string \`json:"id"\``.

App (Stories 3 and 4):

- `parseRoute(hash)` returns `{name: "turns", id}` for `#s/<id>` and `#s/<id>/turns` (the latter also `legacy: true`, which `app.mjs` answers with `history.replaceState` to `#s/<id>`), `{name: "info", id}` for `#s/<id>/info`, `{name: "agent", id, agent}` for `#s/<id>/agent/<agentId>` (`agentId` matching `[A-Za-z0-9_-]+`), and `changes`, `mockup` and `full` as today.
- Tab keys in `SessionTabs`: `turns` (label `conversation`), `changes`, `info`, `mockup`.
- `js/strip.mjs`: `FactStrip({ s, snap, now })` and `AgentStrip({ s, agent, now })`.
- `js/needs.mjs`: `NeedsYou({ s, off, run })`: today's needs-you box from `js/session.mjs`, unchanged in markup and classes.
- `js/session.mjs`: `Info` (was `Session`, same props) and `SessionHead`.
- `js/turns.mjs`: `Turns` (screen), `useTurnPages({ id, agent, live })` returning `{ held, err, load }`, `TurnList({ turns, live, now, remote, sessionId })`, `mergeTurns` as today; Agent step links are `#s/<sessionId>/agent/<step.agent>`.
- `js/agent.mjs`: `Agent` (screen) and `AgentHead`.
- Screen texts the webshot run and reviewers match: `Starting…`, `This subagent's record is gone.`, `● session needs you ›`, `◐ subagent running `, `✓ finished`, `▸ thinking: `.
- Webshot screens (`tools/webshot/main.go`): `conversation` (`#s/1`), `waiting` (`#s/2`), `info` (`#s/1/info`), `agent` (`#s/1/agent/a1f00dcafe0001`), and today's `diff`, `mockup`, `mockup-full`, `new`, `settings`, `fleet`; the `session` and `turns` screens go.
- Webshot fixtures: `turns.json` gains an Agent step with `agent: "a1f00dcafe0001"` and a thinking item; `agent-turns.json` is a subagent page (`agent` object, a brief turn); `apiRoutes` serves `agent-turns.json` for a turns request whose query has `agent`.

Ownership settled after the seams recon:

- Agent ids: the app links only ids matching `[A-Za-z0-9_-]+` (`parseRoute`, `LINKABLE`, `AGENT_ID`), narrower than `ValidAgentID` (which accepts `.` inside an id). Claude's ids are hex, so a step or row whose id has another character is drawn unlinked. Deliberate; no story widens either.
- `GET /api/v1/sessions/{id}/message` stays, undrawn (spec §18). Story 3 deletes the session screen's `Message` and its now-dead `.sc-rule-link` CSS. Story 4 owns every doc and webshot edit about it: `tools/webshot/fixture/message.json` deleted, its `apiRoutes` entry in `canned.go`, `TestFixture_TheLastMessageIsTheTreesText` in `tools/webshot/webshot_test.go`, the `message.json` entry of `internal/web/webshot_fixture_test.go`'s decode map (so with `agent-turns.json` added its `Len(entries, …)` stays 6), the `last_message`/`message.json` clause of `tools/CLAUDE.md`'s webshot rule, `internal/web/README.md`'s `/message` row and its app paragraph, `docs/claude/web-layer.md`, and `js/markdown.mjs`'s header comment.
- `internal/web/CLAUDE.md`'s draw-from-the-tree rule (Story 4): it names the turns route's thinking items as text crossing and drawn as text, and the last message route as one no screen draws; keep the file in its budget (`go run ./tools/claudemd -v`).
- `USAGE.md`'s phone-app bullets (Story 4): the conversation-first screens, tabs conversation · changes · info · mockup, the fact strip, the needs-you box at the conversation's foot, thinking folds, agent links and the subagent screen without a reply box.
- `session/README.md` (Story 1): its `### transcript` section gains the thinking items, `Query.Agent`, the brief, `Page.Agent` and the step→agent links; its `### subagent` paragraph names `ValidAgentID` and `SubagentsDir`.
- Docs naming the retired `turns` screen or `#s/<id>/turns` (`docs/claude/web-layer.md`, `internal/web/README.md`) stay stale between Story 3 and Story 4; Story 4 fixes them.
- Texts Story 4 quotes from Story 3 as "current", which every fixer leaves unchanged: in `js/turns.mjs` the `const LINKABLE = …` line, `useTurnPages`'s `useState(null)` line, the `useLayoutEffect(() => {` / `const a = anchor.current;` opening, the opening-load effect and the `sig` effect; in `js/screens.mjs` the `Mockup` import and the `mockup:` entry with the closing `};`; in `js/session.mjs` the `Agents` use line; in `app.css` the `.sc-strip .sc-hot` rule; in `tools/webshot/main.go` the screens comment's tail and the screen list.

**Design stage:** /flow:draft session 48fe787d-394a-4b0d-9b2d-fbbd6ed14ca9, 2026-11-06T12:33:06Z to 2026-11-06T13:05:46Z UTC: $12.05 logged, $12.16 with estimated thinking; 5225 plan lines, $2.31 per 1,000 lines.

## Stories

- **Story 1** — The transcript reader reads subagents and thinking.

  Add `Query.Agent`, the brief turn, the step→agent links from meta files and `toolUseResult`, `Step.Finished`, thinking items and `Page.Agent` to `session/transcript`, with the shared id check and meta fields in `session/subagent`. Fixture transcripts under `testdata/` modelled on real files (a parent with a foreground Agent step and a background one, `agent-*.jsonl` and `agent-*.meta.json` with and without `toolUseId` and `finished`, thinking blocks blank, redacted and with text). Extend the opt-in `TestRealClaude_TranscriptReader` with one subagent.

  - **Developer:** intermediate-dev
  - **Files:** `session/subagent/meta.go`, `session/subagent/meta_test.go`, `session/transcript/transcript.go`, `session/transcript/records.go`, `session/transcript/build.go`, `session/transcript/agents.go`, `session/transcript/agents_test.go`, `session/transcript/transcript_test.go`, `session/transcript/realclaude_test.go`, `session/transcript/testdata/`, `session/README.md`
  - **Interfaces:** exposes `subagent.ValidAgentID`, `subagent.SubagentsDir`, `Meta.ToolUseID`, `Meta.Finished`, `transcript.Query.Agent`, `Step.Agent`, `Step.Finished`, `KindThinking`, `FromBrief`, `AgentInfo`, `AgentRunning`/`AgentFinished`/`AgentUnknown`, `Page.Agent`; uses nothing from other stories
  - **Verify:** `CGO_ENABLED=0 go test ./session/transcript/ ./session/subagent/`
  - **Review:** deep
  - **Reading:** spec Findings, Decisions §10–§14, Errors and edge cases, Testing; `session/transcript/transcript.go`, `session/transcript/records.go`, `session/transcript/build.go`, `session/subagent/meta.go`

- **Story 2** — The wire and the web layer carry them.

  `subagent.View.ID` from the tracker; regenerate `docs/specs/protocol.md`; `core/reads_test.go` proves `ReadTurns` passes `Query.Agent` to the reader; `internal/web/reads.go` reads `?agent=` (400 on a bad id) and maps the new fields; `internal/web/fleet.go` carries the agent id; `internal/web/README.md` documents the route's parameter and fields.

  - **Developer:** intermediate-dev
  - **Files:** `session/subagent/tracker.go`, `session/subagent/tracker_test.go`, `session/subagent/realclaude_test.go`, `session/subagent_hooks_test.go`, `core/hook_scan_test.go`, `core/reads_test.go`, `docs/specs/protocol.md`, `internal/web/reads.go`, `internal/web/reads_test.go`, `internal/web/fleet.go`, `internal/web/fleet_test.go`, `internal/web/README.md`
  - **Interfaces:** uses Story 1's transcript and subagent names; exposes the web JSON fields and the `agent` parameter in the names contract
  - **Verify:** `CGO_ENABLED=0 go test ./session/subagent/ ./core/ ./core/rpc/ ./internal/web/`
  - **Review:** deep
  - **Reading:** spec Decisions §15–§17; `internal/web/reads.go`, `internal/web/fleet.go:55-120,240-275`, `core/reads.go`, `core/view.go:60-90`, `session/subagent/tracker.go`

- **Story 3** — The app opens on the conversation.

  Routes, tabs, the fact strip, the info tab, the needs-you box moved to the conversation's foot, thinking items and agent links in the turn renderer, and the `useTurnPages`/`TurnList` split that Story 4 reuses. A non-Claude session's `#s/<id>` draws `Info`. Webshot fixtures and screens for conversation, waiting and info; the `session` and `turns` screens retire.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/static/js/app.mjs`, `internal/web/static/js/screens.mjs`, `internal/web/static/js/chrome.mjs`, `internal/web/static/js/strip.mjs`, `internal/web/static/js/needs.mjs`, `internal/web/static/js/session.mjs`, `internal/web/static/js/turns.mjs`, `internal/web/static/js/fleet.mjs`, `internal/web/static/js/usage.mjs`, `internal/web/static/app.css`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `tools/webshot/fixture/turns.json`, `tools/webshot/fixture/snapshot.json`
  - **Interfaces:** uses Story 2's JSON fields; exposes `parseRoute`'s routes, `FactStrip`, `NeedsYou`, `Info`, `useTurnPages`, `TurnList`, the tab keys and the webshot screen names
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && go run ./tools/webshot -out /tmp/loom-shots-s3 -screens conversation,waiting,info,fleet'`
  - **Review:** deep
  - **Reading:** spec Decisions §1–§6, §8; `internal/web/static/js/turns.mjs`, `internal/web/static/js/session.mjs`, `internal/web/static/js/chrome.mjs`, `internal/web/static/js/app.mjs:20-40`, `docs/claude/web-layer.md`

- **Story 4** — The subagent screen.

  `#s/<id>/agent/<agentId>`: `AgentHead`, `AgentStrip`, the brief, live refresh by `agent.state`, the Starting… and gone states, no reply box; info's Agents rows link to it; the conversation keeps its held turns and scroll per session so back returns to the same place. Webshot's `agent` screen and fixture, and the screens' docs in `internal/web/README.md` and `docs/claude/web-layer.md`.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/static/js/agent.mjs`, `internal/web/static/js/strip.mjs`, `internal/web/static/js/turns.mjs`, `internal/web/static/js/session.mjs`, `internal/web/static/js/screens.mjs`, `internal/web/static/app.css`, `tools/webshot/main.go`, `tools/webshot/canned.go`, `tools/webshot/webshot_test.go`, `tools/webshot/fixture/agent-turns.json`, `tools/webshot/fixture/message.json` (deleted), `tools/CLAUDE.md`, `internal/web/webshot_fixture_test.go`, `internal/web/static/js/markdown.mjs`, `internal/web/README.md`, `internal/web/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md`
  - **Interfaces:** uses Story 3's `useTurnPages`, `TurnList`, `parseRoute`'s `agent` route and Story 2's `agent` page object; exposes `Agent`, `AgentHead`, `AgentStrip`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && go run ./tools/webshot -out /tmp/loom-shots-s4 -screens conversation,agent,info'`
  - **Review:** deep
  - **Reading:** spec Decisions §7, §9, Errors and edge cases; `internal/web/static/js/turns.mjs`, `internal/web/static/js/reply.mjs`, `tools/webshot/canned.go`, `tools/webshot/main.go:39-60`

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->

## Story 1: The transcript reader reads subagents and thinking

- **Developer:** intermediate-dev
- **Files:** `session/subagent/meta.go`, `session/subagent/meta_test.go`, `session/transcript/transcript.go`, `session/transcript/records.go`, `session/transcript/build.go`, `session/transcript/agents.go`, `session/transcript/agents_test.go`, `session/transcript/transcript_test.go`, `session/transcript/realclaude_test.go`, `session/transcript/testdata/`, `session/README.md`
- **Interfaces:** exposes `subagent.ValidAgentID`, `subagent.SubagentsDir`, `Meta.ToolUseID`, `Meta.Finished`, `transcript.Query.Agent`, `Step.Agent`, `Step.Finished`, `KindThinking`, `FromBrief`, `AgentInfo`, `AgentRunning`/`AgentFinished`/`AgentUnknown`, `Page.Agent`; uses nothing from other stories
- **Verify:** `CGO_ENABLED=0 go test ./session/transcript/ ./session/subagent/`
- **Review:** deep

The reader learns three things Claude's transcripts hold and loom's did not show. Task 1.1 gives `session/subagent` the shared agent-id check (`ValidAgentID`), the folder helper (`SubagentsDir`) and the two sidecar keys the links need (`toolUseId`, `finished`). Task 1.2 keeps a `thinking` block's text as a `KindThinking` item. Task 1.3 adds `Query.Agent`: `Read` opens `agent-<id>.jsonl` in the conversation's `subagents/` folder, reads its sidechain records, labels the file's first record `FromBrief`, and answers `Page.Agent` from the sidecar and the file (a missing file is a page with `Missing` set, not an error). Task 1.4 links each `Agent`/`Task` step to its subagent through the sidecars' `toolUseId` and the result's `toolUseResult.agentId`, and sets `Step.Finished`. No code here holds a landmine marker. The fixture transcripts under `session/transcript/testdata/subagents/` are modelled on real files from Claude 2.1.x (one record per content block, `isSidechain: true` and `agentId` on every subagent record, sidecars with and without `toolUseId` and `finished`); the edge cases that need a variation of them (bad ids, paging, oversize or clashing sidecars) are built in the tests with the existing `tfile` helper. One narrowing of the spec's "first user prompt": the brief is the record at offset 0 of the subagent file (the first record, as in every file sampled), so a refresh page and a `Before` page label it the same way without reading ahead. Task 1.5 extends the opt-in real-Claude test with one subagent, and Task 1.6 brings `session/README.md` up to date with all of it.

### Task 1.1: The shared agent-id check and the sidecar's link keys

**Files:**
- Modify: `session/subagent/meta.go:13-20` (`Meta`), `session/subagent/meta.go:50-60` (`MetaPath`)
- Test: `session/subagent/meta_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `session/subagent/meta_test.go` (its imports already hold `assert` and `require`):

```go
func TestValidAgentID(t *testing.T) {
	for _, id := range []string{"a1", "a1f00dcafe0001", "agent-x", "a.b"} {
		assert.True(t, ValidAgentID(id), "%q", id)
	}
	for _, id := range []string{"", " ", "\t", ".", "..", "../x", "a/b", `a\b`, "/", `\`} {
		assert.False(t, ValidAgentID(id), "%q", id)
	}
}

func TestSubagentsDir(t *testing.T) {
	assert.Equal(t, "/p/-work/sess/subagents", SubagentsDir("/p/-work/sess.jsonl"))
	assert.Equal(t, "/p/-work/sess/subagents", SubagentsDir("/p/-work/sess"), "no suffix to trim")
	assert.Equal(t, "", SubagentsDir(""))
}

func TestMetaPath_UsesValidAgentID(t *testing.T) {
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", " "))
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", "."))
	assert.Equal(t, "/p/sess/subagents/agent-a.b.meta.json", MetaPath("/p/sess.jsonl", "a.b"))
}

func TestParseMeta_LinkFields(t *testing.T) {
	m, err := ParseMeta([]byte(`{"agentType":"Explore","toolUseId":"toolu_01","finished":false}`))
	require.NoError(t, err)
	assert.Equal(t, "toolu_01", m.ToolUseID)
	require.NotNil(t, m.Finished)
	assert.False(t, *m.Finished)

	m, err = ParseMeta([]byte(`{"finished":true}`))
	require.NoError(t, err)
	require.NotNil(t, m.Finished)
	assert.True(t, *m.Finished)

	m, err = ParseMeta([]byte(`{"agentType":"Explore"}`))
	require.NoError(t, err)
	assert.Empty(t, m.ToolUseID)
	assert.Nil(t, m.Finished, "an absent key is nil, not false")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/subagent/ -run 'TestValidAgentID|TestSubagentsDir|TestMetaPath|TestParseMeta_LinkFields'`
Expected: `FAIL github.com/aidan-bailey/loom/session/subagent [build failed]`, with `undefined: ValidAgentID`, `undefined: SubagentsDir`, `m.ToolUseID undefined` and `m.Finished undefined`.

- [ ] **Step 3: Implement**

In `session/subagent/meta.go` replace

```go
type Meta struct {
	AgentType   string `json:"agentType"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TaskKind    string `json:"taskKind"`
}
```

with

```go
type Meta struct {
	AgentType   string `json:"agentType"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TaskKind    string `json:"taskKind"`
	// ToolUseID is the id of the tool_use block that launched the agent, in
	// its parent's transcript (the conversation's, or the parent agent's
	// own file when nested); "" where the CLI version writes none.
	ToolUseID string `json:"toolUseId"`
	// Finished is the sidecar's finished key: nil when the key is absent,
	// which says nothing about whether the agent ended.
	Finished *bool `json:"finished"`
}
```

and replace

```go
// MetaPath returns where Claude writes agentID's sidecar for the session
// whose transcript is at transcriptPath. It returns "" when either is
// empty, or when agentID could escape the subagents folder.
func MetaPath(transcriptPath, agentID string) string {
	if transcriptPath == "" || agentID == "" || agentID == ".." ||
		strings.ContainsAny(agentID, `/\`) {
		return ""
	}
	sessionDir := strings.TrimSuffix(transcriptPath, ".jsonl")
	return filepath.Join(sessionDir, "subagents", "agent-"+agentID+".meta.json")
}
```

with

```go
// ValidAgentID reports whether id can name an agent's files: it is not
// blank, is not "." or "..", and holds no path separator, so
// "agent-"+id+".jsonl" stays inside the subagents folder. Agent IDs come
// from hook payloads, transcripts and HTTP queries; check one with this
// before it joins a path.
func ValidAgentID(id string) bool {
	return strings.TrimSpace(id) != "" && id != "." && id != ".." &&
		!strings.ContainsAny(id, `/\`)
}

// SubagentsDir returns the folder Claude keeps a session's subagent files
// in (agent-<id>.jsonl and agent-<id>.meta.json): the transcript's path
// without ".jsonl", then "subagents". It returns "" for an empty path.
func SubagentsDir(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	return filepath.Join(strings.TrimSuffix(transcriptPath, ".jsonl"), "subagents")
}

// MetaPath returns where Claude writes agentID's sidecar for the session
// whose transcript is at transcriptPath. It returns "" when either is
// empty, or when agentID could escape the subagents folder.
func MetaPath(transcriptPath, agentID string) string {
	dir := SubagentsDir(transcriptPath)
	if dir == "" || !ValidAgentID(agentID) {
		return ""
	}
	return filepath.Join(dir, "agent-"+agentID+".meta.json")
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/subagent/`
Expected: `ok  	github.com/aidan-bailey/loom/session/subagent`

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(subagent): share the agent-id check, and read a sidecar's toolUseId and finished" -- session/subagent/meta.go session/subagent/meta_test.go
```

### Task 1.2: Thinking items

**Files:**
- Modify: `session/transcript/transcript.go:97-120` (`Item`, the kinds)
- Modify: `session/transcript/records.go:76-86` (`block`), `session/transcript/records.go:143-157` (`assistant`)
- Test: `session/transcript/transcript_test.go`

`build.go` needs no change here: it appends every part that is not a step as an `Item` with the part's kind, text and cut flag, so a thinking part joins its turn like a text part.

- [ ] **Step 1: Write the failing tests**

Append to `session/transcript/transcript_test.go`:

```go
// thinks is one thinking block, as Claude writes it: a signature, and the
// text it keeps, which is mostly blank.
func thinks(text string) map[string]any {
	return assistant(map[string]any{"type": "thinking", "thinking": text, "signature": "EqQB"})
}

func TestRead_ThinkingKeepsItsTextAndDropsBlankAndRedacted(t *testing.T) {
	f := newTFile(t)
	f.add(typed("design it"))
	f.add(thinks(""))
	f.add(thinks("  \n "))
	f.add(assistant(map[string]any{"type": "redacted_thinking", "data": "EmQK"}))
	f.add(thinks("Still in design: items 1 and 2 are worked out."))
	f.add(said("Here is the plan."))
	f.add(bash("t1", "make"))
	f.add(result("t1", false, "ok"))
	f.add(thinks("Checking the result."))

	p := f.read(Query{})
	require.Len(t, p.Turns, 1)
	turn := p.Turns[0]
	require.Equal(t, []string{KindThinking, KindText, KindSteps, KindThinking}, kinds(turn.Items),
		"a blank and a redacted block add nothing")
	assert.Equal(t, "Still in design: items 1 and 2 are worked out.", turn.Items[0].Text)
	assert.False(t, turn.Items[0].Cut)
	assert.False(t, turn.Items[0].At.IsZero())
	assert.Equal(t, "Checking the result.", turn.Items[3].Text)
}

func TestRead_ThinkingIsCutLikeATextItem(t *testing.T) {
	f := newTFile(t)
	f.add(typed("p"))
	f.add(thinks(strings.Repeat("t", maxItem+10)))

	item := f.read(Query{}).Turns[0].Items[0]
	assert.Equal(t, KindThinking, item.Kind)
	assert.True(t, item.Cut)
	assert.Len(t, item.Text, maxItem)
}

func TestRead_ThinkingCountsTowardsAPagesText(t *testing.T) {
	big := strings.Repeat("w", 60<<10)
	f := newTFile(t)
	var ids []int64
	for i := 1; i <= 5; i++ {
		ids = append(ids, f.add(typed(fmt.Sprintf("p%d", i))))
		f.add(thinks(big))
		f.add(thinks(big))
	}
	p := f.read(Query{})
	assert.Equal(t, []string{"p4", "p5"}, texts(p.Turns), "a third turn would take the page past 256 KiB")
	assert.Equal(t, ids[3], p.Earlier)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/transcript/ -run 'TestRead_Thinking'`
Expected: `FAIL ... [build failed]` with `undefined: KindThinking`.

- [ ] **Step 3: Implement**

In `session/transcript/transcript.go` replace

```go
type Item struct {
	Kind string // KindText, KindSteps, KindCompacted or KindNote
	// At is its record's timestamp; a steps item's is its first step's.
	At time.Time
	// Text is a text item's markdown (its first maxItem bytes; Cut says it
	// was cut), or a note's line.
	Text  string
```

with

```go
type Item struct {
	Kind string // KindText, KindThinking, KindSteps, KindCompacted or KindNote
	// At is its record's timestamp; a steps item's is its first step's.
	At time.Time
	// Text is a text or thinking item's markdown (its first maxItem bytes;
	// Cut says it was cut), or a note's line.
	Text  string
```

and replace

```go
const (
	KindText      = "text"
	KindSteps     = "steps"
```

with

```go
const (
	KindText = "text"
	// KindThinking is a thinking block that holds text: Claude stores most
	// as a signature alone, and those add nothing.
	KindThinking  = "thinking"
	KindSteps     = "steps"
```

(gofmt aligns the block; run `gofmt -w session/transcript/transcript.go` and keep its output.)

In `session/transcript/records.go` replace

```go
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
```

with

```go
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
```

and replace

```go
			e.parts = append(e.parts, part{kind: KindText, text: t, cut: cut})
			e.size += len(t)
		case "tool_use":
```

with

```go
			e.parts = append(e.parts, part{kind: KindText, text: t, cut: cut})
			e.size += len(t)
		case "thinking":
			// Most are a signature alone; a redacted_thinking block has no
			// text to show and falls through to nothing.
			if strings.TrimSpace(b.Thinking) == "" {
				continue
			}
			t, cut := capBytes(b.Thinking, maxItem)
			e.parts = append(e.parts, part{kind: KindThinking, text: t, cut: cut})
			e.size += len(t)
		case "tool_use":
```

Also change the comment above `assistant` from "text items and steps" to "text and thinking items and steps":

```go
// assistant reads Claude's blocks: text and thinking items and steps, or an
// API error's note.
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/transcript/`
Expected: `ok  	github.com/aidan-bailey/loom/session/transcript` (the earlier tests still pass: `TestRead_ATurnIsItsPromptRepliesAndSteps` holds a blank thinking block and still expects `[steps text steps]`).

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(transcript): keep a thinking block's text as a thinking item" -- session/transcript/transcript.go session/transcript/records.go session/transcript/transcript_test.go
```

### Task 1.3: Query.Agent, the brief turn and Page.Agent

**Files:**
- Create: `session/transcript/agents.go`
- Create: `session/transcript/agents_test.go`
- Create: `session/transcript/testdata/subagents/2fba5511-2348-4614-a16b-fd369153f8ba.jsonl`
- Create: `session/transcript/testdata/subagents/2fba5511-2348-4614-a16b-fd369153f8ba/subagents/agent-a1f00dcafe0001.jsonl`, `agent-a1f00dcafe0001.meta.json`, `agent-a2f00dcafe0002.jsonl`, `agent-a2f00dcafe0002.meta.json`, `agent-a4f00dcafe0004.jsonl`, `agent-a4f00dcafe0004.meta.json`, `agent-a5f00dcafe0005.jsonl`
- Modify: `session/transcript/transcript.go:9-16` (imports), `:37-66` (`Query`, `Page`), `:68-95` (`Turn`, the `From` constants), `:139-209` (`Read`, `open`, `read`), `:213`, `:219`, `:259`, `:262` (`pageBefore`, `since`)
- Modify: `session/transcript/records.go:88-119` (`decode`)

- [ ] **Step 1: Write the failing tests and the fixtures**

Create the fixtures first. Each line is one JSON record, as Claude writes them (one content block per record). `testdata/subagents/` is read in place by the tests; nothing writes to it. The parent transcript, `session/transcript/testdata/subagents/2fba5511-2348-4614-a16b-fd369153f8ba.jsonl`:

```jsonl
{"type":"user","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:00.000Z","promptSource":"typed","origin":{"kind":"human"},"message":{"role":"user","content":"Survey the web layer, then check the docs"}}
{"type":"assistant","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:01.000Z","message":{"id":"msg_1","role":"assistant","content":[{"type":"thinking","thinking":"","signature":"EqQB"}]}}
{"type":"assistant","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:01.500Z","message":{"id":"msg_1","role":"assistant","content":[{"type":"redacted_thinking","data":"EmQK"}]}}
{"type":"assistant","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:02.000Z","message":{"id":"msg_1","role":"assistant","content":[{"type":"thinking","thinking":"Still in design: the web layer first, then the docs.","signature":"EqQB"}]}}
{"type":"assistant","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:02.500Z","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"toolu_fg","name":"Agent","input":{"description":"Survey the web layer","subagent_type":"Explore","prompt":"List the web layer's routes. Report in two lines."}}]}}
{"type":"user","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:06.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_fg","content":[{"type":"text","text":"Seven routes, all under /api/v1."}]}]},"toolUseResult":{"status":"completed","prompt":"List the web layer's routes.","agentId":"a1f00dcafe0001","agentType":"Explore","totalDurationMs":3800}}
{"type":"assistant","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:07.000Z","message":{"id":"msg_2","role":"assistant","content":[{"type":"tool_use","id":"toolu_bg","name":"Agent","input":{"description":"Run the slow checks","prompt":"Run the slow checks and report any failure.","run_in_background":true}}]}}
{"type":"user","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:07.500Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bg","content":[{"type":"text","text":"Async agent launched successfully."}]}]},"toolUseResult":{"status":"async_launched","description":"Run the slow checks","agentId":"a2f00dcafe0002"}}
{"type":"assistant","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:08.000Z","message":{"id":"msg_3","role":"assistant","content":[{"type":"tool_use","id":"toolu_old","name":"Task","input":{"description":"Check the docs","prompt":"Check the docs."}}]}}
{"type":"user","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:10.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_old","content":[{"type":"text","text":"Docs are current."}]}]},"toolUseResult":{"status":"completed","prompt":"Check the docs.","agentId":"a3f00dcafe0003"}}
{"type":"assistant","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:11.000Z","message":{"id":"msg_4","role":"assistant","content":[{"type":"tool_use","id":"toolu_wait","name":"Agent","input":{"description":"Still starting","prompt":"Wait."}}]}}
{"type":"assistant","isSidechain":false,"cwd":"/w","timestamp":"2026-11-06T09:00:12.000Z","message":{"id":"msg_5","role":"assistant","content":[{"type":"text","text":"The survey is back; the slow checks run in the background."}]}}
```

`.../2fba5511-2348-4614-a16b-fd369153f8ba/subagents/agent-a1f00dcafe0001.jsonl` (a finished foreground agent: a brief, blank and text thinking, a Read, a reply, and a later prompt continuing it):

```jsonl
{"parentUuid":null,"isSidechain":true,"agentId":"a1f00dcafe0001","type":"user","cwd":"/w","timestamp":"2026-11-06T09:00:02.000Z","message":{"role":"user","content":"List the web layer's routes. Report in two lines."}}
{"isSidechain":true,"agentId":"a1f00dcafe0001","type":"assistant","cwd":"/w","timestamp":"2026-11-06T09:00:03.000Z","message":{"id":"msg_s1","role":"assistant","content":[{"type":"thinking","thinking":"","signature":"EqQB"}]}}
{"isSidechain":true,"agentId":"a1f00dcafe0001","type":"assistant","cwd":"/w","timestamp":"2026-11-06T09:00:03.500Z","message":{"id":"msg_s1","role":"assistant","content":[{"type":"thinking","thinking":"Routes live in internal/web/reads.go.","signature":"EqQB"}]}}
{"isSidechain":true,"agentId":"a1f00dcafe0001","type":"assistant","cwd":"/w","timestamp":"2026-11-06T09:00:04.000Z","message":{"id":"msg_s1","role":"assistant","content":[{"type":"tool_use","id":"toolu_r1","name":"Read","input":{"file_path":"/w/internal/web/reads.go"}}]}}
{"isSidechain":true,"agentId":"a1f00dcafe0001","type":"user","cwd":"/w","timestamp":"2026-11-06T09:00:04.500Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_r1","content":"package web"}]},"toolUseResult":{"type":"text"}}
{"isSidechain":true,"agentId":"a1f00dcafe0001","type":"assistant","cwd":"/w","timestamp":"2026-11-06T09:00:05.000Z","message":{"id":"msg_s2","role":"assistant","content":[{"type":"text","text":"Seven routes, all under /api/v1."}]}}
{"isSidechain":true,"agentId":"a1f00dcafe0001","type":"user","cwd":"/w","timestamp":"2026-11-06T09:00:05.500Z","message":{"role":"user","content":"And the websocket?"}}
{"isSidechain":true,"agentId":"a1f00dcafe0001","type":"assistant","cwd":"/w","timestamp":"2026-11-06T09:00:05.800Z","message":{"id":"msg_s3","role":"assistant","content":[{"type":"text","text":"None; it polls."}]}}
```

`.../subagents/agent-a1f00dcafe0001.meta.json`:

```json
{"agentType":"Explore","description":"Survey the web layer","toolUseId":"toolu_fg","spawnDepth":1,"requestShape":"foreground","finished":true}
```

`.../subagents/agent-a2f00dcafe0002.jsonl` (a background agent still running its first command):

```jsonl
{"parentUuid":null,"isSidechain":true,"agentId":"a2f00dcafe0002","type":"user","cwd":"/w","timestamp":"2026-11-06T09:00:07.800Z","message":{"role":"user","content":"Run the slow checks and report any failure."}}
{"isSidechain":true,"agentId":"a2f00dcafe0002","type":"assistant","cwd":"/w","timestamp":"2026-11-06T09:00:08.200Z","message":{"id":"msg_s4","role":"assistant","content":[{"type":"tool_use","id":"toolu_b1","name":"Bash","input":{"command":"make slow"}}]}}
```

`.../subagents/agent-a2f00dcafe0002.meta.json`:

```json
{"agentType":"general-purpose","description":"Run the slow checks","toolUseId":"toolu_bg","spawnDepth":1,"requestShape":"background","finished":false}
```

`.../subagents/agent-a4f00dcafe0004.jsonl` (an older CLI's agent: its sidecar has neither `toolUseId` nor `finished`):

```jsonl
{"parentUuid":null,"isSidechain":true,"agentId":"a4f00dcafe0004","type":"user","cwd":"/w","timestamp":"2026-11-06T09:00:09.000Z","message":{"role":"user","content":"Plan the migration of the old tables."}}
{"isSidechain":true,"agentId":"a4f00dcafe0004","type":"assistant","cwd":"/w","timestamp":"2026-11-06T09:00:09.500Z","message":{"id":"msg_s5","role":"assistant","content":[{"type":"text","text":"Three steps."}]}}
```

`.../subagents/agent-a4f00dcafe0004.meta.json`:

```json
{"agentType":"Plan","description":"Plan the migration"}
```

`.../subagents/agent-a5f00dcafe0005.jsonl` (no sidecar at all; a brief of two lines):

```jsonl
{"parentUuid":null,"isSidechain":true,"agentId":"a5f00dcafe0005","type":"user","cwd":"/w","timestamp":"2026-11-06T09:00:11.000Z","message":{"role":"user","content":"Check the sidecar docs\nSecond line of the brief."}}
```

Agent `a3f00dcafe0003` (named by the parent's result in `toolu_old`) has no files; the tests use it as the record that is gone.

Now create `session/transcript/agents_test.go`:

```go
package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/session/subagent"
)

// fixtureTranscript is the parent transcript under testdata/subagents; its
// subagents folder holds the agent files modelled on Claude's. Tests only
// read it.
func fixtureTranscript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "subagents", conv+".jsonl"))
	require.NoError(t, err)
	return p
}

// withAgents writes f as the conversation's transcript, and files (name to
// content) into its subagents folder, and returns the transcript's path.
func withAgents(t *testing.T, f *tfile, files map[string]string) string {
	t.Helper()
	path := f.path()
	dir := subagent.SubagentsDir(path)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	for name, data := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600))
	}
	return path
}

func TestRead_AgentReadsTheSubagentsOwnTurns(t *testing.T) {
	p, err := Read(fixtureTranscript(t), conv, Query{Agent: "a1f00dcafe0001"})
	require.NoError(t, err)
	assert.Equal(t, conv, p.Conversation)
	assert.Zero(t, p.Earlier)
	assert.False(t, p.Reset)
	assert.Zero(t, p.Skipped)
	require.Len(t, p.Turns, 2, "the brief, and the prompt that continued the agent")

	brief := p.Turns[0]
	assert.Equal(t, FromBrief, brief.From)
	assert.Equal(t, "List the web layer's routes. Report in two lines.", brief.Text)
	assert.Zero(t, brief.ID, "the brief is the file's first record")
	assert.False(t, brief.Command)
	require.Equal(t, []string{KindThinking, KindSteps, KindText}, kinds(brief.Items),
		"the sidechain records are read, the blank thinking block is not")
	assert.Equal(t, "Routes live in internal/web/reads.go.", brief.Items[0].Text)
	assert.Equal(t, []string{"internal/web/reads.go"}, summaries(brief.Items[1].Steps))
	assert.Equal(t, []string{StateDone}, states(brief.Items[1].Steps))
	assert.Equal(t, "Seven routes, all under /api/v1.", brief.Items[2].Text)

	follow := p.Turns[1]
	assert.Equal(t, FromYou, follow.From, "a later prompt parses as it does in the conversation")
	assert.Equal(t, "And the websocket?", follow.Text)
	require.Len(t, follow.Items, 1)
	assert.Equal(t, "None; it polls.", follow.Items[0].Text)
}

func TestRead_AConversationReadHasNoAgentInfo(t *testing.T) {
	p, err := Read(fixtureTranscript(t), conv, Query{})
	require.NoError(t, err)
	assert.Nil(t, p.Agent)
	for _, turn := range p.Turns {
		assert.NotEqual(t, FromBrief, turn.From)
	}
}

func TestRead_AgentInfoComesFromTheSidecarAndTheFile(t *testing.T) {
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339Nano, s)
		require.NoError(t, err)
		return ts
	}
	for name, tc := range map[string]struct {
		id      string
		want    AgentInfo
		started time.Time
		turns   bool
	}{
		"finished": {"a1f00dcafe0001",
			AgentInfo{ID: "a1f00dcafe0001", Type: "Explore", Description: "Survey the web layer", State: AgentFinished},
			at("2026-11-06T09:00:02.000Z"), true},
		"running": {"a2f00dcafe0002",
			AgentInfo{ID: "a2f00dcafe0002", Type: "general-purpose", Description: "Run the slow checks", State: AgentRunning},
			at("2026-11-06T09:00:07.800Z"), true},
		"a sidecar without the finished key": {"a4f00dcafe0004",
			AgentInfo{ID: "a4f00dcafe0004", Type: "Plan", Description: "Plan the migration", State: AgentUnknown},
			at("2026-11-06T09:00:09.000Z"), true},
		"no sidecar: the brief's first line describes it": {"a5f00dcafe0005",
			AgentInfo{ID: "a5f00dcafe0005", Description: "Check the sidecar docs", State: AgentUnknown},
			at("2026-11-06T09:00:11.000Z"), true},
		"no files: the record is gone": {"a3f00dcafe0003",
			AgentInfo{ID: "a3f00dcafe0003", State: AgentUnknown, Missing: true},
			time.Time{}, false},
	} {
		p, err := Read(fixtureTranscript(t), conv, Query{Agent: tc.id})
		require.NoError(t, err, name)
		require.NotNil(t, p.Agent, name)
		got := *p.Agent
		assert.True(t, tc.started.Equal(got.Started), "%s: started %v", name, got.Started)
		got.Started = time.Time{}
		assert.Equal(t, tc.want, got, name)
		assert.Equal(t, tc.turns, len(p.Turns) > 0, name)
		assert.Equal(t, conv, p.Conversation, name)
	}
}

func TestRead_AgentTeammateComesFromTheSidecar(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, map[string]string{
		"agent-mate.jsonl":     `{"isSidechain":true,"type":"user","timestamp":"2026-11-06T09:00:00.000Z","message":{"role":"user","content":"hi"}}` + "\n",
		"agent-mate.meta.json": `{"name":"probe-mate","taskKind":"in_process_teammate","finished":false}`,
	})
	p, err := Read(path, conv, Query{Agent: "mate"})
	require.NoError(t, err)
	assert.True(t, p.Agent.Teammate)
	assert.Equal(t, AgentRunning, p.Agent.State)
}

func TestRead_AgentWithASidecarButNoFileIsAPageNotAnError(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, map[string]string{
		"agent-early.meta.json": `{"agentType":"Explore","description":"just launched","finished":false}`,
	})
	p, err := Read(path, conv, Query{Agent: "early"})
	require.NoError(t, err)
	assert.Empty(t, p.Turns)
	assert.Zero(t, p.Earlier)
	require.NotNil(t, p.Agent)
	assert.Equal(t, AgentInfo{ID: "early", Type: "Explore", Description: "just launched", State: AgentRunning, Missing: true}, *p.Agent)
}

func TestRead_AgentRefusesABadID(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, nil)
	for _, id := range []string{" ", "\t", ".", "..", "../x", "a/b", `a\b`, "../" + conv} {
		_, err := Read(path, conv, Query{Agent: id})
		assert.EqualError(t, err, fmt.Sprintf("transcript: agent %q: not an agent id", id), "%q", id)
	}
}

func TestRead_AgentFileThatIsNotAFileIsAnError(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, nil)
	require.NoError(t, os.Mkdir(filepath.Join(subagent.SubagentsDir(path), "agent-dir.jsonl"), 0o700))
	_, err := Read(path, conv, Query{Agent: "dir"})
	assert.ErrorContains(t, err, "not a regular file")
}

func TestRead_AgentStillChecksTheConversationsPath(t *testing.T) {
	_, err := Read(conv+".jsonl", conv, Query{Agent: "a1"})
	assert.ErrorContains(t, err, "not an absolute path")
	_, err = Read(fixtureTranscript(t), "other", Query{Agent: "a1"})
	assert.ErrorContains(t, err, "not the transcript of conversation")
	_, err = Read(fixtureTranscript(t), conv, Query{Agent: "a1", Before: 5, After: 5})
	assert.ErrorContains(t, err, "before or after")
}

func TestRead_AgentPagesAndRefreshesLikeTheConversation(t *testing.T) {
	sub := newTFile(t)
	brief := sub.add(typed("Count the routes"))
	require.Zero(t, brief)
	sub.add(said("counting"))
	var ids []int64
	for i := 1; i <= 12; i++ {
		ids = append(ids, sub.add(typed(fmt.Sprintf("p%d", i))))
		sub.add(said(fmt.Sprintf("a%d", i)))
	}
	parent := newTFile(t)
	parent.add(typed("go"))
	path := withAgents(t, parent, map[string]string{"agent-ap.jsonl": sub.b.String()})

	newest, err := Read(path, conv, Query{Agent: "ap"})
	require.NoError(t, err)
	require.Len(t, newest.Turns, maxTurns)
	assert.Equal(t, "p3", newest.Turns[0].Text)
	assert.Equal(t, ids[2], newest.Earlier)
	require.NotNil(t, newest.Agent)
	assert.Equal(t, "ap", newest.Agent.ID)

	before, err := Read(path, conv, Query{Agent: "ap", Before: newest.Earlier})
	require.NoError(t, err)
	assert.Equal(t, []string{"Count the routes", "p1", "p2"}, texts(before.Turns))
	assert.Equal(t, FromBrief, before.Turns[0].From)
	assert.Equal(t, FromYou, before.Turns[1].From, "only the first record is the brief")
	assert.Zero(t, before.Earlier)
	require.NotNil(t, before.Agent)
	assert.Equal(t, "Count the routes", before.Agent.Description, "no sidecar: the brief's first line")

	after, err := Read(path, conv, Query{Agent: "ap", Conversation: conv, After: ids[10]})
	require.NoError(t, err)
	assert.Equal(t, []string{"p11", "p12"}, texts(after.Turns))
	assert.Equal(t, FromYou, after.Turns[0].From)
	assert.False(t, after.Reset)
	assert.Zero(t, after.Earlier)
	require.NotNil(t, after.Agent)

	other, err := Read(path, conv, Query{Agent: "ap", Conversation: "an-earlier-conversation"})
	require.NoError(t, err)
	assert.True(t, other.Reset, "another conversation's cursor is a reset of the agent's newest page")
	assert.Len(t, other.Turns, maxTurns)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/transcript/ -run 'TestRead_Agent|TestRead_AConversationReadHasNoAgentInfo'`
Expected: `FAIL github.com/aidan-bailey/loom/session/transcript [build failed]` with `unknown field Agent in struct literal of type Query`, `undefined: FromBrief`, `undefined: AgentInfo`, `undefined: AgentFinished`, `p.Agent undefined`.

- [ ] **Step 3: Implement**

In `session/transcript/transcript.go`:

Replace the imports

```go
import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)
```

with

```go
import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aidan-bailey/loom/session/subagent"
)
```

Replace

```go
	// After > 0 reads every turn from the one at this offset (a turn's ID)
	// to the end of the file.
	After int64
}
```

with

```go
	// After > 0 reads every turn from the one at this offset (a turn's ID)
	// to the end of the file.
	After int64
	// Agent, when set, reads that subagent's transcript, <session>/subagents/
	// agent-<Agent>.jsonl beside the conversation's, with the same paging,
	// instead of the conversation. It must pass subagent.ValidAgentID.
	Agent string
}
```

Replace

```go
	// Skipped counts the page's lines that could not be read: not JSON,
	// or longer than lineLimit.
	Skipped int
}
```

with

```go
	// Skipped counts the page's lines that could not be read: not JSON,
	// or longer than lineLimit.
	Skipped int
	// Agent says whose page it is when Query.Agent was set, nil otherwise.
	Agent *AgentInfo
}

// AgentInfo describes the subagent a page belongs to, from its sidecar
// (agent-<id>.meta.json) and its file.
type AgentInfo struct {
	ID string
	// Type is the sidecar's agentType, Description its description or, with
	// none, the brief's first line.
	Type        string
	Description string
	Teammate    bool
	// State is AgentFinished or AgentRunning from the sidecar's finished
	// key, AgentUnknown when the sidecar or the key is missing. The file's
	// last record can't say: it is usually the handback, not an end.
	State string
	// Started is the file's first record's timestamp.
	Started time.Time
	// Missing says the agent's .jsonl does not exist: not written yet, or
	// deleted by Claude's cleanup.
	Missing bool
}

// The states of an AgentInfo.
const (
	AgentRunning  = "running"
	AgentFinished = "finished"
	AgentUnknown  = "unknown"
)
```

Replace

```go
	// From says whose the prompt is: FromYou, FromPlugin, FromPeer or
	// FromTask; Name is the plugin's or the peer's name.
```

with

```go
	// From says whose the prompt is: FromYou, FromPlugin, FromPeer,
	// FromTask or, for a subagent file's first prompt, FromBrief; Name is
	// the plugin's or the peer's name.
```

and replace

```go
	FromPeer   = "peer"
	FromTask   = "task"
)
```

with

```go
	FromPeer   = "peer"
	FromTask   = "task"
	// FromBrief is a subagent file's first prompt: what the parent told it.
	FromBrief = "brief"
)
```

(run `gofmt -w session/transcript/transcript.go` and keep its alignment.)

Replace the whole of `Read` and `open` (from the `// Read reads the page q asks for` comment through the end of `open`) with

```go
// Read reads the page q asks for from the transcript at path, which belongs
// to conversation (its session ID). It refuses a path that is not
// absolute, not <conversation>.jsonl or not a regular file, so a bad hook
// payload can't point it at another file. It opens the file read-only and
// reads nothing past the last newline it finds: a line after that is one
// Claude is still writing. With q.Agent set it reads that subagent's file
// instead (agents.go); a bad agent id is an error.
func Read(path, conversation string, q Query) (Page, error) {
	if q.Before > 0 && q.After > 0 {
		return Page{}, errors.New("transcript: a query takes before or after, not both")
	}
	if q.Agent != "" && !subagent.ValidAgentID(q.Agent) {
		return Page{}, fmt.Errorf("transcript: agent %q: not an agent id", q.Agent)
	}
	if err := checkPath(path, conversation); err != nil {
		return Page{}, err
	}
	if q.Agent != "" {
		return readAgent(path, conversation, q)
	}
	f, size, err := openFile(path)
	if err != nil {
		return Page{}, err
	}
	defer f.Close()
	page, err := read(f, size, conversation, q)
	if err != nil {
		return Page{}, fmt.Errorf("transcript %s: %w", path, err)
	}
	page.Conversation = conversation
	return page, nil
}

// checkPath refuses a path that is not the absolute path of conversation's
// transcript.
func checkPath(path, conversation string) error {
	switch {
	case !filepath.IsAbs(path):
		return fmt.Errorf("transcript %s: not an absolute path", path)
	case conversation == "" || filepath.Base(path) != conversation+".jsonl":
		return fmt.Errorf("transcript %s: not the transcript of conversation %q", path, conversation)
	}
	return nil
}

// openFile opens the regular file at path read-only, and returns its size.
func openFile(path string) (*os.File, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("transcript: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("transcript: %w", err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("transcript %s: not a regular file", path)
	}
	return f, info.Size(), nil
}
```

Replace the whole of `read` (from `// read reads q's page from r` through its closing brace) with

```go
// read reads q's page from r, whose first size bytes are the transcript (a
// subagent's when q.Agent is set).
func read(r io.ReaderAt, size int64, conversation string, q Query) (Page, error) {
	end, err := lastLineEnd(r, size)
	if err != nil {
		return Page{}, err
	}
	agent := q.Agent != ""
	other := q.Conversation != "" && q.Conversation != conversation
	if other || q.Before > end || (q.After > 0 && q.After >= end) {
		return pageBefore(r, end, true, agent)
	}
	if q.After > 0 {
		page, ok, err := since(r, end, q.After, agent)
		if err != nil || ok {
			return page, err
		}
		return pageBefore(r, end, true, agent)
	}
	to := end
	if q.Before > 0 {
		to = q.Before
	}
	return pageBefore(r, to, false, agent)
}
```

Replace `func pageBefore(r io.ReaderAt, to int64, reset bool) (Page, error) {` with

```go
func pageBefore(r io.ReaderAt, to int64, reset, agent bool) (Page, error) {
```

and, in it, `e := decode(off, data)` with

```go
		e := decode(off, data, agent)
```

Replace `func since(r io.ReaderAt, end, after int64) (Page, bool, error) {` with

```go
func since(r io.ReaderAt, end, after int64, agent bool) (Page, bool, error) {
```

and, in it, `newestFirst = append(newestFirst, decode(off, data))` with

```go
		newestFirst = append(newestFirst, decode(off, data, agent))
```

In `session/transcript/records.go` replace

```go
// decode reads the line at off.
func decode(off int64, data []byte) entry {
```

with

```go
// decode reads the line at off. In a subagent's file (agent) every record is
// a sidechain record and is read, and the prompt at offset 0, the first
// record, is the agent's brief.
func decode(off int64, data []byte, agent bool) entry {
```

replace

```go
	if r.IsSidechain {
		return e
	}
```

with

```go
	if r.IsSidechain && !agent {
		return e
	}
```

and replace

```go
	case "user":
		e.user(r)
	}
	return e
}
```

with

```go
	case "user":
		e.user(r)
	}
	if agent && off == 0 && e.opens {
		e.turn.From, e.turn.Name, e.turn.Command = FromBrief, "", false
	}
	return e
}
```

Create `session/transcript/agents.go`:

```go
package transcript

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
)

// metaLimit is the most a sidecar may hold; a larger one is not read.
const metaLimit = 64 << 10

// readAgent reads q's page of the subagent's file beside the conversation's
// transcript at path. The sidecar lands before the file, and Claude's
// cleanup can delete the file, so a file that does not exist is a page with
// no turns and AgentInfo.Missing set: only core's own errors survive the
// wire, and a caller could not tell "not yet written" from a failure.
func readAgent(path, conversation string, q Query) (Page, error) {
	file := filepath.Join(subagent.SubagentsDir(path), "agent-"+q.Agent+".jsonl")
	f, size, err := openFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		info := agentInfo(path, q.Agent, nil, 0)
		return Page{Conversation: conversation, Agent: &info}, nil
	}
	if err != nil {
		return Page{}, err
	}
	defer f.Close()
	page, err := read(f, size, conversation, q)
	if err != nil {
		return Page{}, fmt.Errorf("transcript %s: %w", file, err)
	}
	page.Conversation = conversation
	info := agentInfo(path, q.Agent, f, size)
	page.Agent = &info
	return page, nil
}

// agentInfo describes agent id of the conversation whose transcript is at
// path. file is the agent's open transcript of size bytes, nil when it does
// not exist. A missing or unreadable sidecar leaves Type and Description
// empty (the brief's first line stands in for Description) and State
// unknown.
func agentInfo(path, id string, file io.ReaderAt, size int64) AgentInfo {
	info := AgentInfo{ID: id, State: AgentUnknown, Missing: file == nil}
	if m, ok := readMeta(subagent.MetaPath(path, id)); ok {
		info.Type, info.Description, info.Teammate = m.AgentType, m.Description, m.IsTeammate()
		switch {
		case m.Finished == nil:
		case *m.Finished:
			info.State = AgentFinished
		default:
			info.State = AgentRunning
		}
	}
	if file != nil {
		started, brief := firstRecord(file, size)
		info.Started = started
		if info.Description == "" {
			info.Description = brief
		}
	}
	return info
}

// readMeta reads the sidecar at path: a regular file of at most metaLimit
// bytes that parses. It reports false for anything else, so a bad sidecar
// leaves its agent unlinked and the page still answers.
func readMeta(path string) (subagent.Meta, bool) {
	if path == "" {
		return subagent.Meta{}, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > metaLimit {
		return subagent.Meta{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return subagent.Meta{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, metaLimit+1))
	if err != nil || len(data) > metaLimit {
		return subagent.Meta{}, false
	}
	m, err := subagent.ParseMeta(data)
	return m, err == nil
}

// firstRecord is the timestamp of the file's first record and the first
// line of the brief it holds, both zero when the first line is longer than
// lineLimit, unfinished or not a prompt.
func firstRecord(r io.ReaderAt, size int64) (time.Time, string) {
	line, err := bufio.NewReader(io.LimitReader(io.NewSectionReader(r, 0, size), lineLimit+1)).ReadBytes('\n')
	if err != nil {
		return time.Time{}, ""
	}
	e := decode(0, bytes.TrimSuffix(line, []byte("\n")), true)
	brief := ""
	if e.opens {
		brief = hooks.CapSummary(e.turn.Text)
	}
	return e.at, brief
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/transcript/`
Expected: `ok  	github.com/aidan-bailey/loom/session/transcript`. Then `gofmt -l session/transcript session/subagent` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add -N session/transcript/agents.go session/transcript/agents_test.go session/transcript/testdata
git commit -m "feat(transcript): read a subagent's own turns, its brief and its info" -- session/transcript/transcript.go session/transcript/records.go session/transcript/agents.go session/transcript/agents_test.go session/transcript/testdata
```

### Task 1.4: Link Agent steps to their subagents

**Files:**
- Modify: `session/transcript/transcript.go` (`Step`, `Read`), `session/transcript/agents.go` (imports, `readAgent`, new functions)
- Modify: `session/transcript/records.go:52-66` (`record`), `:31-39` (`part`), `:179-182` (`user`), after `resultState` (`agentResult`), imports
- Modify: `session/transcript/build.go:61-67` (`addPart`)
- Test: `session/transcript/agents_test.go`

- [ ] **Step 1: Write the failing tests**

In `session/transcript/agents_test.go` change the imports to

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/session/subagent"
)
```

and append:

```go
// agentBack is the user record that carries an agent tool's result, with
// the toolUseResult Claude writes beside it (nil writes none).
func agentBack(id string, res map[string]any) map[string]any {
	return user([]any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": false, "content": "done"}},
		map[string]any{"toolUseResult": res})
}

// stepByID is the step with id in any turn of p.
func stepByID(t *testing.T, p Page, id string) Step {
	t.Helper()
	for _, turn := range p.Turns {
		for _, it := range turn.Items {
			for _, s := range it.Steps {
				if s.ID == id {
					return s
				}
			}
		}
	}
	require.Fail(t, "no step "+id)
	return Step{}
}

func TestRead_AgentStepsLinkToTheirSubagents(t *testing.T) {
	p, err := Read(fixtureTranscript(t), conv, Query{})
	require.NoError(t, err)
	require.Len(t, p.Turns, 1)
	turn := p.Turns[0]
	require.Equal(t, []string{KindThinking, KindSteps, KindText}, kinds(turn.Items))
	assert.Equal(t, "Still in design: the web layer first, then the docs.", turn.Items[0].Text,
		"the blank and the redacted thinking blocks before it add nothing")
	steps := turn.Items[1].Steps
	require.Len(t, steps, 4)

	type link struct {
		id, tool, agent string
		finished        bool
		state           string
	}
	var got []link
	for _, s := range steps {
		got = append(got, link{s.ID, s.Tool, s.Agent, s.Finished, s.State})
	}
	assert.Equal(t, []link{
		{"toolu_fg", "Agent", "a1f00dcafe0001", true, StateDone},      // sidecar and result agree
		{"toolu_bg", "Agent", "a2f00dcafe0002", false, StateDone},     // launched: the result settles, the sidecar says running
		{"toolu_old", "Task", "a3f00dcafe0003", true, StateDone},      // no sidecar: the result names it
		{"toolu_wait", "Agent", "", false, StatePending},              // no result, no sidecar
	}, got)
	assert.Equal(t, "Survey the web layer", steps[0].Summary)
}

func TestRead_AgentStepFinishedAndLinkRules(t *testing.T) {
	meta := func(m string) map[string]string { return map[string]string{"agent-a9.meta.json": m} }
	for _, tc := range []struct {
		name         string
		tool         string // "Agent" when empty
		pending      bool   // the step has no result yet
		result       map[string]any
		files        map[string]string
		wantAgent    string
		wantFinished bool
	}{
		{name: "a sidecar links a step with no result, and says running", pending: true,
			files: meta(`{"toolUseId":"tu","finished":false}`), wantAgent: "a9"},
		{name: "a sidecar that says finished settles a step with no result", pending: true,
			files: meta(`{"toolUseId":"tu","finished":true}`), wantAgent: "a9", wantFinished: true},
		{name: "a completed result names the agent", result: map[string]any{"status": "completed", "agentId": "a8"},
			wantAgent: "a8", wantFinished: true},
		{name: "a launch result names the agent but is not the end", result: map[string]any{"status": "async_launched", "agentId": "a8"},
			wantAgent: "a8"},
		{name: "the sidecar's finished beats a launch result", result: map[string]any{"status": "async_launched", "agentId": "a9"},
			files: meta(`{"toolUseId":"tu","finished":true}`), wantAgent: "a9", wantFinished: true},
		{name: "the sidecar's finished false beats a completed result", result: map[string]any{"status": "completed", "agentId": "a9"},
			files: meta(`{"toolUseId":"tu","finished":false}`), wantAgent: "a9"},
		{name: "a sidecar without the key falls back to a launch result", result: map[string]any{"status": "async_launched", "agentId": "a9"},
			files: meta(`{"toolUseId":"tu"}`), wantAgent: "a9"},
		{name: "a sidecar without the key falls back to a completed result", result: map[string]any{"status": "completed", "agentId": "a9"},
			files: meta(`{"toolUseId":"tu"}`), wantAgent: "a9", wantFinished: true},
		{name: "the sidecar names the agent before the result does", result: map[string]any{"status": "completed", "agentId": "a8"},
			files: meta(`{"toolUseId":"tu"}`), wantAgent: "a9", wantFinished: true},
		{name: "no agent anywhere: unlinked, though the result ended the step", result: map[string]any{"status": "completed"},
			files: meta(`{"agentType":"Explore"}`), wantFinished: true},
		{name: "a result with no toolUseResult at all", files: meta(`{"agentType":"Explore"}`), wantFinished: true},
		{name: "two sidecars claim one tool use: neither links", pending: true,
			files: map[string]string{"agent-a1.meta.json": `{"toolUseId":"tu"}`, "agent-a2.meta.json": `{"toolUseId":"tu"}`}},
		{name: "two sidecars claim one tool use: the result still names it", result: map[string]any{"status": "completed", "agentId": "a8"},
			files:     map[string]string{"agent-a1.meta.json": `{"toolUseId":"tu"}`, "agent-a2.meta.json": `{"toolUseId":"tu"}`},
			wantAgent: "a8", wantFinished: true},
		{name: "an agent id that would escape the folder is dropped", result: map[string]any{"status": "completed", "agentId": "../x"},
			wantFinished: true},
		{name: "a sidecar over 64 KiB is not read", pending: true,
			files: meta(`{"toolUseId":"tu","description":"` + strings.Repeat("x", 70<<10) + `"}`)},
		{name: "a broken sidecar is skipped and the others still link", pending: true,
			files: map[string]string{"agent-a7.meta.json": "{", "agent-a9.meta.json": `{"toolUseId":"tu"}`}, wantAgent: "a9"},
		{name: "a sidecar of another tool use does not link", pending: true,
			files: meta(`{"toolUseId":"another"}`)},
		{name: "Task is an agent tool too", tool: "Task", result: map[string]any{"status": "completed", "agentId": "a8"},
			wantAgent: "a8", wantFinished: true},
		{name: "another tool is never linked or finished", tool: "Read", result: map[string]any{"agentId": "a8"},
			files: meta(`{"toolUseId":"tu","finished":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.tool
			if name == "" {
				name = "Agent"
			}
			f := newTFile(t)
			f.add(typed("go"))
			f.add(tool("tu", name, map[string]any{"description": "work", "prompt": "p"}))
			if !tc.pending {
				f.add(agentBack("tu", tc.result))
			}
			p, err := Read(withAgents(t, f, tc.files), conv, Query{})
			require.NoError(t, err)
			s := stepByID(t, p, "tu")
			assert.Equal(t, tc.wantAgent, s.Agent)
			assert.Equal(t, tc.wantFinished, s.Finished)
		})
	}
}

func TestRead_AResultWithSeveralToolResultsNamesNoAgent(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	f.add(tool("u1", "Agent", map[string]any{"description": "one"}))
	f.add(tool("u2", "Agent", map[string]any{"description": "two"}))
	f.add(user([]any{
		map[string]any{"type": "tool_result", "tool_use_id": "u1", "content": "a"},
		map[string]any{"type": "tool_result", "tool_use_id": "u2", "content": "b"},
	}, map[string]any{"toolUseResult": map[string]any{"status": "completed", "agentId": "a8"}}))
	p := f.read(Query{})
	for _, id := range []string{"u1", "u2"} {
		s := stepByID(t, p, id)
		assert.Empty(t, s.Agent, id)
		assert.Equal(t, StateDone, s.State, id)
	}
}

func TestRead_AnUnreadableSubagentsFolderLeavesStepsUnlinkedAndTheResultsLinks(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	f.add(tool("t1", "Agent", map[string]any{"description": "no result id"}))
	f.add(agentBack("t1", map[string]any{"status": "completed"}))
	f.add(tool("t2", "Agent", map[string]any{"description": "named by its result"}))
	f.add(agentBack("t2", map[string]any{"status": "completed", "agentId": "a8"}))
	path := f.path()
	// A file where the folder should be: listing it fails, even for root.
	require.NoError(t, os.MkdirAll(filepath.Dir(subagent.SubagentsDir(path)), 0o700))
	require.NoError(t, os.WriteFile(subagent.SubagentsDir(path), []byte("x"), 0o600))

	p, err := Read(path, conv, Query{})
	require.NoError(t, err, "the page still answers")
	assert.Empty(t, stepByID(t, p, "t1").Agent)
	assert.Equal(t, "a8", stepByID(t, p, "t2").Agent)
}

func TestRead_AgentPageLinksItsOwnAgentSteps(t *testing.T) {
	sub := newTFile(t)
	sub.add(typed("Split the work"))
	sub.add(tool("tn", "Agent", map[string]any{"description": "nested"}))
	parent := newTFile(t)
	parent.add(typed("go"))
	path := withAgents(t, parent, map[string]string{
		"agent-ap.jsonl":        sub.b.String(),
		"agent-nest.meta.json": `{"toolUseId":"tn","finished":false}`,
	})

	p, err := Read(path, conv, Query{Agent: "ap"})
	require.NoError(t, err)
	s := stepByID(t, p, "tn")
	assert.Equal(t, "nest", s.Agent, "a nested agent's sidecar sits in the same folder")
	assert.False(t, s.Finished)
}

func TestRead_AgentInfoIgnoresASidecarOver64KiB(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, map[string]string{
		"agent-big.jsonl":     `{"isSidechain":true,"type":"user","timestamp":"2026-11-06T09:00:00.000Z","message":{"role":"user","content":"the brief"}}` + "\n",
		"agent-big.meta.json": `{"agentType":"Explore","finished":true,"description":"` + strings.Repeat("x", 70<<10) + `"}`,
	})
	p, err := Read(path, conv, Query{Agent: "big"})
	require.NoError(t, err)
	require.NotNil(t, p.Agent)
	assert.Equal(t, AgentInfo{ID: "big", Description: "the brief", State: AgentUnknown, Started: p.Agent.Started}, *p.Agent)
	assert.False(t, p.Agent.Started.IsZero())
}
```

(Run `gofmt -w session/transcript/agents_test.go` after pasting; the trailing comments and map keys will realign.)

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/transcript/ -run 'TestRead_Agent|TestRead_AResultWithSeveral|TestRead_AnUnreadable'`
Expected: `FAIL ... [build failed]` with `s.Agent undefined (type Step has no field or method Agent)` and `s.Finished undefined`.

- [ ] **Step 3: Implement**

In `session/transcript/transcript.go` replace

```go
	At      time.Time
	State   string // StatePending, StateDone, StateFailed or StateInterrupted
}
```

with

```go
	At      time.Time
	State   string // StatePending, StateDone, StateFailed or StateInterrupted
	// Agent is the subagent an Agent or Task step launched: the id of the
	// sidecar naming the step's tool_use id (toolUseId), else the one the
	// step's result names (toolUseResult.agentId). "" for another tool, and
	// for a step no agent is found for: no link is guessed.
	Agent string
	// Finished says an Agent or Task step's agent has ended: the sidecar's
	// finished key when it has one, else whether the step has a result that
	// is not a background launch's (status async_launched). False for
	// another tool.
	Finished bool
}
```

In `Read` (conversation branch) replace

```go
	page.Conversation = conversation
	return page, nil
}

// checkPath
```

with

```go
	page.Conversation = conversation
	linkAgents(path, page.Turns)
	return page, nil
}

// checkPath
```

In `session/transcript/agents.go`, in `readAgent` replace

```go
	page.Conversation = conversation
	info := agentInfo(path, q.Agent, f, size)
```

with

```go
	page.Conversation = conversation
	linkAgents(path, page.Turns)
	info := agentInfo(path, q.Agent, f, size)
```

Change its imports to add `"strings"` (after `"path/filepath"`), and append:

```go
// isAgentTool reports whether tool launches a subagent: Claude calls the
// tool Agent, older versions Task.
func isAgentTool(tool string) bool { return tool == "Agent" || tool == "Task" }

func hasAgentStep(turns []Turn) bool {
	for _, t := range turns {
		for _, it := range t.Items {
			for _, s := range it.Steps {
				if isAgentTool(s.Tool) {
					return true
				}
			}
		}
	}
	return false
}

// metaIndex is what a subagents folder's sidecars say.
type metaIndex struct {
	byAgent map[string]subagent.Meta
	// byTool maps a launching tool_use id to its agent; "" marks an id two
	// sidecars claim, which links neither.
	byTool map[string]string
}

// listMeta reads the sidecars in dir. An unreadable folder or sidecar is
// left out: its steps stay unlinked.
func listMeta(dir string) metaIndex {
	idx := metaIndex{byAgent: map[string]subagent.Meta{}, byTool: map[string]string{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return idx
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
		if !subagent.ValidAgentID(id) {
			continue
		}
		m, ok := readMeta(filepath.Join(dir, name))
		if !ok {
			continue
		}
		idx.byAgent[id] = m
		if m.ToolUseID == "" {
			continue
		}
		if prev, claimed := idx.byTool[m.ToolUseID]; claimed && prev != id {
			idx.byTool[m.ToolUseID] = ""
		} else {
			idx.byTool[m.ToolUseID] = id
		}
	}
	return idx
}

// linkAgents settles the Agent and Finished of the Agent and Task steps of
// turns, in place, from the sidecars of the conversation whose transcript is
// at path. The builder has set each step's Agent and Finished from its
// result; a sidecar naming the step's tool_use id overrides the agent, and
// the finished key of the step's agent's sidecar overrides Finished. The
// folder is listed once, and only when a step needs it. A nested agent's
// sidecar sits in the same folder as the conversation's.
func linkAgents(path string, turns []Turn) {
	if !hasAgentStep(turns) {
		return
	}
	idx := listMeta(subagent.SubagentsDir(path))
	for ti := range turns {
		for ii := range turns[ti].Items {
			steps := turns[ti].Items[ii].Steps
			for si := range steps {
				s := &steps[si]
				if !isAgentTool(s.Tool) {
					continue
				}
				if id := idx.byTool[s.ID]; id != "" {
					s.Agent = id
				}
				if m, ok := idx.byAgent[s.Agent]; ok && m.Finished != nil {
					s.Finished = *m.Finished
				}
			}
		}
	}
}
```

In `session/transcript/records.go` add the import `"github.com/aidan-bailey/loom/session/subagent"` beside `hooks`:

```go
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
)
```

Replace

```go
	Origin                    json.RawMessage `json:"origin"`
	Message                   json.RawMessage `json:"message"`
}
```

with

```go
	Origin                    json.RawMessage `json:"origin"`
	Message                   json.RawMessage `json:"message"`
	ToolUseResult             json.RawMessage `json:"toolUseResult"`
}
```

Replace

```go
type part struct {
	kind  string
	text  string
	cut   bool
	error bool
	step  Step
}
```

with

```go
type part struct {
	kind  string
	text  string
	cut   bool
	error bool
	step  Step
	// status is a partResult's toolUseResult.status; its step's Agent is the
	// agent that result names.
	status string
}
```

Replace

```go
	if len(results) > 0 {
		e.parts = results
		return
	}
```

with

```go
	if len(results) > 0 {
		// toolUseResult describes one result: with several in a record it
		// names none of them.
		if len(results) == 1 {
			results[0].step.Agent, results[0].status = agentResult(r.ToolUseResult)
		}
		e.parts = results
		return
	}
```

and add after `resultState`:

```go
// statusAsyncLaunched is the toolUseResult status of an agent launched in
// the background: its tool_result settles at launch, not at the agent's end.
const statusAsyncLaunched = "async_launched"

// agentResult reads a tool result's toolUseResult for the agent it names and
// its status. Only a result that mentions an agent is decoded: other tools'
// results can be large. An agent id that could escape the subagents folder
// is dropped.
func agentResult(raw json.RawMessage) (agent, status string) {
	if !bytes.Contains(raw, []byte(`"agentId"`)) && !bytes.Contains(raw, []byte(statusAsyncLaunched)) {
		return "", ""
	}
	var r struct {
		AgentID string `json:"agentId"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return "", ""
	}
	if !subagent.ValidAgentID(r.AgentID) {
		r.AgentID = ""
	}
	return r.AgentID, r.Status
}
```

In `session/transcript/build.go` replace

```go
	if p.kind == partResult {
		if at, ok := b.steps[p.step.ID]; ok {
			b.turns[at.turn].Items[at.item].Steps[at.step].State = p.step.State
		}
		return
	}
```

with

```go
	if p.kind == partResult {
		if at, ok := b.steps[p.step.ID]; ok {
			s := &b.turns[at.turn].Items[at.item].Steps[at.step]
			s.State = p.step.State
			if isAgentTool(s.Tool) {
				// linkAgents lets a sidecar override both.
				s.Agent = p.step.Agent
				s.Finished = p.status != statusAsyncLaunched
			}
		}
		return
	}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./session/transcript/ ./session/subagent/`
Expected: `ok  	github.com/aidan-bailey/loom/session/transcript` and `ok  	github.com/aidan-bailey/loom/session/subagent`. Then `gofmt -l session/transcript session/subagent` prints nothing and `CGO_ENABLED=0 go vet ./session/transcript/ ./session/subagent/` prints nothing.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(transcript): link Agent steps to their subagents and say when they finished" -- session/transcript/transcript.go session/transcript/records.go session/transcript/build.go session/transcript/agents.go session/transcript/agents_test.go
```

### Task 1.5: The opt-in real-Claude case with one subagent

**Files:**
- Modify: `session/transcript/realclaude_test.go` (replace the whole file, 73 lines)

The run is skipped unless `LOOM_TEST_REAL_CLAUDE=1`, so `go test` stays green; the existing test's body moves into `runClaude` and its assertions stay.

- [ ] **Step 1: Write the test**

Replace `session/transcript/realclaude_test.go` with:

```go
package transcript

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runClaude runs one print-mode prompt on haiku in a fresh directory, with
// args before it, and returns the path and session ID of the transcript it
// wrote under the developer's Claude config dir.
func runClaude(t *testing.T, prompt string, args ...string) (path, session string) {
	t.Helper()
	argv := append([]string{"-p", "--model", "haiku", "--output-format", "json"}, args...)
	run := exec.Command("claude", append(argv, prompt)...)
	run.Dir = t.TempDir()
	out, err := run.Output()
	require.NoError(t, err, "claude -p: %s", out)
	var res struct {
		SessionID string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(out, &res), "claude -p's JSON: %s", out)
	require.NotEmpty(t, res.SessionID)

	config := os.Getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		config = filepath.Join(home, ".claude")
	}
	paths, err := filepath.Glob(filepath.Join(config, "projects", "*", res.SessionID+".jsonl"))
	require.NoError(t, err)
	require.Len(t, paths, 1, "the transcript of session %s under %s", res.SessionID, config)
	return paths[0], res.SessionID
}

// TestRealClaude_TranscriptReader runs one print-mode prompt that makes one
// Bash call and reads its transcript back: one turn of the prompt, one done
// Bash step whose summary is the command, and Claude's text. A second case
// has Claude launch one subagent and reads the agent step's link and the
// subagent's own page. Claude's transcript format is its own and changes
// without notice, so this is the reader's guard. It spends a few cents of
// haiku per case and leaves the sessions in the developer's Claude config
// dir (projects/), so it runs only with LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_TranscriptReader(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	path, session := runClaude(t,
		"Run the shell command `echo loom-transcript-probe` with the Bash tool, then reply with exactly the word DONE.",
		"--allowedTools", "Bash(echo:*)")

	p, err := Read(path, session, Query{})
	require.NoError(t, err)
	assert.Zero(t, p.Skipped)
	require.Len(t, p.Turns, 1)
	turn := p.Turns[0]
	assert.Equal(t, FromYou, turn.From)
	assert.Contains(t, turn.Text, "loom-transcript-probe")
	var steps []Step
	var said []string
	for _, it := range turn.Items {
		switch it.Kind {
		case KindSteps:
			steps = append(steps, it.Steps...)
		case KindText:
			said = append(said, it.Text)
		}
	}
	require.Len(t, steps, 1)
	assert.Equal(t, "Bash", steps[0].Tool)
	assert.True(t, strings.HasPrefix(steps[0].Summary, "echo"), "the summary is the command: %q", steps[0].Summary)
	assert.Contains(t, steps[0].Summary, "loom-transcript-probe")
	assert.Equal(t, StateDone, steps[0].State)
	assert.Empty(t, steps[0].Agent, "a Bash step launches no agent")
	assert.Contains(t, strings.Join(said, " "), "DONE")

	t.Run("subagent", func(t *testing.T) {
		path, session := runClaude(t,
			"Launch exactly one subagent with the Agent tool. Its task: reply with exactly the word SUBPROBE and nothing else. "+
				"Wait for it to finish, then reply with exactly the word DONE.")
		p, err := Read(path, session, Query{})
		require.NoError(t, err)
		var launch *Step
		for _, turn := range p.Turns {
			for _, it := range turn.Items {
				for i := range it.Steps {
					if isAgentTool(it.Steps[i].Tool) {
						launch = &it.Steps[i]
					}
				}
			}
		}
		require.NotNil(t, launch, "the parent holds an Agent step")
		require.NotEmpty(t, launch.Agent, "the step is linked to its subagent")
		assert.Equal(t, StateDone, launch.State)
		assert.True(t, launch.Finished)

		sub, err := Read(path, session, Query{Agent: launch.Agent})
		require.NoError(t, err)
		assert.Equal(t, session, sub.Conversation)
		require.NotNil(t, sub.Agent)
		assert.Equal(t, launch.Agent, sub.Agent.ID)
		assert.False(t, sub.Agent.Missing)
		assert.Equal(t, AgentFinished, sub.Agent.State, "the sidecar's finished key once the agent ends")
		assert.False(t, sub.Agent.Started.IsZero())
		require.NotEmpty(t, sub.Turns)
		assert.Equal(t, FromBrief, sub.Turns[0].From)
		assert.Contains(t, sub.Turns[0].Text, "SUBPROBE")
	})
}
```

- [ ] **Step 2: Run it to see it skip**

Run: `CGO_ENABLED=0 go test ./session/transcript/ -run TestRealClaude_TranscriptReader -v`
Expected: `--- SKIP: TestRealClaude_TranscriptReader` with `set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)`. The opt-in run (`LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/transcript/ -run TestRealClaude_TranscriptReader`, which spends money and writes sessions into the developer's Claude config dir) is the owner's, not the supervisor's.

- [ ] **Step 3: Implement**

Nothing beyond Step 1: the test is the change.

- [ ] **Step 4: Run the story's tests**

Run: `CGO_ENABLED=0 go test ./session/transcript/ ./session/subagent/`
Expected: `ok  	github.com/aidan-bailey/loom/session/transcript` and `ok  	github.com/aidan-bailey/loom/session/subagent`. Then `CGO_ENABLED=0 go vet ./session/...` prints nothing.

- [ ] **Step 5: Commit**

```bash
git commit -m "test(transcript): have the real-Claude reader test launch one subagent" -- session/transcript/realclaude_test.go
```

### Task 1.6: Document the reader's additions in session/README.md

**Files:**
- Modify: `session/README.md:154` (`### subagent`), `session/README.md:158` (`### transcript`)

The README holds facts, so this task changes no code and has no test; the gates are the repo's docs checks, which read the README's links, paths and test names.

- [ ] **Step 1: Name the shared helpers in the `### subagent` section**

In `session/README.md`, in the `### subagent` section, replace

```
`subagent/meta.go`/`subagent/readmeta.go` parse and read the `agent-<id>.meta.json` sidecars. No tmux, UI or app dependency.
```

with

```
`subagent/meta.go`/`subagent/readmeta.go` parse and read the `agent-<id>.meta.json` sidecars, whose `toolUseId` and `finished` keys the transcript reader links agent steps by. `ValidAgentID` is the one check an agent id passes before it joins a path (not blank, not `.` or `..`, no path separator), and `SubagentsDir` gives the `<transcript without .jsonl>/subagents` folder that holds the agent files. No tmux, UI or app dependency.
```

- [ ] **Step 2: Describe thinking items, `Query.Agent`, the brief, `Page.Agent` and the links in the `### transcript` section**

In the `### transcript` section, replace

```
Claude's text blocks become text items (64 KiB each), each run of `tool_use` blocks
```

with

```
Claude's text blocks become text items (64 KiB each) and its `thinking` blocks with text become thinking items (`KindThinking`, capped like text; a blank or redacted block is dropped), each run of `tool_use` blocks
```

Then replace

```
Lines that are not JSON or too long are counted in `Skipped`. No tmux, UI or app dependency;
```

with

```
An `Agent` or `Task` step is linked to the subagent it launched: `Step.Agent` is the id of the `agent-<id>.meta.json` sidecar whose `toolUseId` names the step's `tool_use` id, else the id the step's result names in `toolUseResult.agentId`, and empty when neither is found (no link is guessed); `Step.Finished` is the sidecar's `finished` key, else whether the step has a result that is not a background launch's (`async_launched`). `Query.Agent` reads that subagent's page instead of the conversation's: `Read` opens `<transcript without .jsonl>/subagents/agent-<id>.jsonl` (an id that fails `subagent.ValidAgentID` is an error), reads its sidechain records with the same paging, and labels the file's first record, the brief the parent gave it, `From: "brief"` (`FromBrief`). `Page.Agent` (`AgentInfo`) says whose page it is: the id, the sidecar's type and description (the brief's first line when the sidecar has none), `Teammate`, `Started` (the first record's timestamp) and `State` (`AgentFinished` or `AgentRunning` from the sidecar's `finished` key, `AgentUnknown` when the sidecar or the key is missing); an agent file that does not exist yet, or that Claude's cleanup deleted, is a page with no turns and `Missing` set, not an error. Lines that are not JSON or too long are counted in `Skipped`. No tmux, UI or app dependency;
```

- [ ] **Step 3: Run the docs checks**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ ./session/...`
Expected: `ok` for each package; the README's paths and test names still resolve.

- [ ] **Step 4: Commit**

```bash
git commit -m "docs(session): document the transcript reader's subagent and thinking support" -- session/README.md
```

## Story 2: The wire and the web layer carry them

- **Developer:** intermediate-dev
- **Files:** `session/subagent/tracker.go`, `session/subagent/tracker_test.go`, `session/subagent/realclaude_test.go`, `session/subagent_hooks_test.go`, `core/hook_scan_test.go`, `core/reads_test.go`, `docs/specs/protocol.md`, `internal/web/reads.go`, `internal/web/reads_test.go`, `internal/web/fleet.go`, `internal/web/fleet_test.go`, `internal/web/README.md`
- **Interfaces:** uses Story 1's transcript and subagent names; exposes the web JSON fields and the `agent` parameter in the names contract
- **Verify:** `CGO_ENABLED=0 go test ./session/subagent/ ./core/ ./core/rpc/ ./internal/web/`
- **Review:** deep

This story puts Story 1's reader on the wire and in the phone's JSON. `subagent.View` gains `ID` (the tracker's agent id), which `InstanceView` copies untouched (`slices.Clone` in `core/views.go`) and the web's session JSON carries as `agents[].id`. `core.ReadTurns` already hands its `Query` to the reader unchanged, so a test pins that `Query.Agent` and the new `Page.Agent` cross it, and `docs/specs/protocol.md` is regenerated (the new fields are optional, so `rpc.Protocol` stays). `internal/web/reads.go` reads `?agent=` (400 `agent "<id>": not an agent id` when `subagent.ValidAgentID` refuses it) and maps `Step.Agent`/`Step.Finished`, thinking items (`text`, `truncated`) and the page's `agent` object; `internal/web/fleet.go` adds the agent id; `internal/web/README.md` documents them. The JSON only gains fields, as the package's rule on `/api/v1` requires. Task 2.1 also edits three test files (`session/subagent_hooks_test.go`, `core/hook_scan_test.go`, `session/subagent/realclaude_test.go`): they compare whole `subagent.View` values, which now hold an `ID`, and go red without it. No code here holds a landmine marker.

### Task 2.1: `subagent.View` carries the agent id

**Files:**
- Modify: `session/subagent/tracker.go:44-49` (`View`), `session/subagent/tracker.go:253-256` (`Visible`)
- Modify: `session/subagent/tracker_test.go`
- Modify: `session/subagent_hooks_test.go:214`
- Modify: `core/hook_scan_test.go:95,138`
- Modify: `session/subagent/realclaude_test.go:101`

- [ ] **Step 1: Write the failing test.** Every whole-`View` comparison names the id the fixture's agent has. In `session/subagent/tracker_test.go` make these replacements (each old line is the current text):

  `TestTracker_PlainLifecycle`:
  ```go
  	assert.Equal(t, []View{{Name: "Explore", Description: "map code"}}, tr.Visible())
  ```
  becomes
  ```go
  	assert.Equal(t, []View{{ID: "a1", Name: "Explore", Description: "map code"}}, tr.Visible())
  ```

  `TestTracker_TeammateCycle`, two lines:
  ```go
  	assert.Equal(t, []View{{Name: "mate", Description: "implement task"}}, tr.Visible())
  ```
  becomes
  ```go
  	assert.Equal(t, []View{{ID: "amate-1", Name: "mate", Description: "implement task"}}, tr.Visible())
  ```
  and
  ```go
  	assert.Equal(t, []View{{Name: "mate", Description: "implement task", Idle: true}}, tr.Visible(),
  ```
  becomes
  ```go
  	assert.Equal(t, []View{{ID: "amate-1", Name: "mate", Description: "implement task", Idle: true}}, tr.Visible(),
  ```

  `TestTracker_PartialShutdownRemovesStoppingFirst`:
  ```go
  	assert.Equal(t, []View{{Name: "b", Description: "y", Idle: true}}, tr.Visible())
  ```
  becomes
  ```go
  	assert.Equal(t, []View{{ID: "ab-1", Name: "b", Description: "y", Idle: true}}, tr.Visible())
  ```

  `TestTracker_PartialShutdownFallsBackToMostRecentlyStopped`:
  ```go
  	assert.Equal(t, []View{{Name: "a", Description: "x", Idle: true}}, tr.Visible(),
  ```
  becomes
  ```go
  	assert.Equal(t, []View{{ID: "aa-1", Name: "a", Description: "x", Idle: true}}, tr.Visible(),
  ```

  `TestTracker_TeammateStopBeforeMetaStaysIdle` and `TestTracker_TeammateIdleBeforeMetaMarksTeammate` hold the same line; replace both (Edit with `replace_all`):
  ```go
  	assert.Equal(t, []View{{Name: "mate", Description: "d", Idle: true}}, tr.Visible())
  ```
  becomes
  ```go
  	assert.Equal(t, []View{{ID: "amate-1", Name: "mate", Description: "d", Idle: true}}, tr.Visible())
  ```

  `TestTracker_ParentStopKeepsListedSubagents`:
  ```go
  	assert.Equal(t, []View{{Name: "Explore", Description: "d"}}, tr.Visible(),
  ```
  becomes
  ```go
  	assert.Equal(t, []View{{ID: "a1", Name: "Explore", Description: "d"}}, tr.Visible(),
  ```

  `TestTracker_SubagentStopListIsNotUsedForReconciliation`:
  ```go
  	assert.Equal(t, []View{{Name: "Plan", Description: "e"}}, tr.Visible())
  ```
  becomes
  ```go
  	assert.Equal(t, []View{{ID: "a2", Name: "Plan", Description: "e"}}, tr.Visible())
  ```

  `TestTracker_VisibleOrder`:
  ```go
  	assert.Equal(t, []View{
  		{Name: "Explore", Description: "first"},
  		{Name: "Plan", Description: "third"},
  		{Name: "mate", Description: "second", Idle: true},
  	}, tr.Visible(), "working first, then idle, each in spawn order")
  ```
  becomes
  ```go
  	assert.Equal(t, []View{
  		{ID: "a1", Name: "Explore", Description: "first"},
  		{ID: "a3", Name: "Plan", Description: "third"},
  		{ID: "amate-1", Name: "mate", Description: "second", Idle: true},
  	}, tr.Visible(), "working first, then idle, each in spawn order; each keeps its own id")
  ```

  In `session/subagent_hooks_test.go` (`TestSubagents_EndToEndAndRestart`):
  ```go
  	want := []subagent.View{{Name: "mate", Description: "implement", Idle: true}}
  ```
  becomes
  ```go
  	want := []subagent.View{{ID: "amate-1", Name: "mate", Description: "implement", Idle: true}}
  ```

  In `core/hook_scan_test.go` the line occurs twice (`TestSubagentScanResultAppliesAndGatesByLaunch` and the end-to-end scan test); replace both (`replace_all`):
  ```go
  	require.Equal(t, []subagent.View{{Name: "Explore", Description: "map code"}}, inst.Subagents())
  ```
  becomes
  ```go
  	require.Equal(t, []subagent.View{{ID: "a1", Name: "Explore", Description: "map code"}}, inst.Subagents())
  ```

  In `session/subagent/realclaude_test.go` (opt-in, `LOOM_TEST_REAL_CLAUDE=1`; the real agent's id is not known beforehand):
  ```go
  	require.Equal(t, []View{{Name: "probe-mate", Description: "probe teammate", Idle: true}}, tracker.Visible())
  ```
  becomes
  ```go
  	got := tracker.Visible()
  	require.Len(t, got, 1)
  	require.NotEmpty(t, got[0].ID, "the tracker names the agent")
  	require.Equal(t, View{ID: got[0].ID, Name: "probe-mate", Description: "probe teammate", Idle: true}, got[0])
  ```

- [ ] **Step 2: Run it to see it fail.**
  `CGO_ENABLED=0 go test ./session/subagent/`
  It fails to build: `unknown field ID in struct literal of type View`.

- [ ] **Step 3: Implement.** In `session/subagent/tracker.go` replace the current

  ```go
  // View is one agent as the UI renders it.
  type View struct {
  	Name        string
  	Description string
  	Idle        bool
  }
  ```
  with
  ```go
  // View is one agent as the UI renders it. ID is the tracker's agent id,
  // the one Claude names the agent's transcript with
  // (subagents/agent-<ID>.jsonl); it stays the same while the agent is
  // re-tasked.
  type View struct {
  	ID          string
  	Name        string
  	Description string
  	Idle        bool
  }
  ```
  and in `Visible` replace
  ```go
  		out[i] = View{Name: a.name, Description: a.description, Idle: a.state != stateWorking}
  ```
  with
  ```go
  		out[i] = View{ID: a.id, Name: a.name, Description: a.description, Idle: a.state != stateWorking}
  ```

- [ ] **Step 4: Run it to see it pass.**
  `CGO_ENABLED=0 go test ./session/subagent/ ./session/ ./core/`
  Expected: `ok` for the three packages. Mutation check: drop `ID: a.id` from `Visible`; `TestTracker_VisibleOrder` and the core and session tests go red; restore it.

- [ ] **Step 5: Commit.**
  ```
  git commit -m "feat(subagent,session,core): give a subagent View its agent id" -- session/subagent/tracker.go session/subagent/tracker_test.go session/subagent/realclaude_test.go session/subagent_hooks_test.go core/hook_scan_test.go
  ```

### Task 2.2: `ReadTurns` passes the agent query through; the protocol reference follows

**Files:**
- Modify: `core/reads_test.go` (imports, and a new test after `TestReadTurns_RepliesWithAPageOfTheTranscript`)
- Modify (generated): `docs/specs/protocol.md`

- [ ] **Step 1: Write the failing test.** In `core/reads_test.go` add the subagent package to the imports. Replace

  ```go
  	"github.com/aidan-bailey/loom/session"
  	"github.com/aidan-bailey/loom/session/transcript"
  ```
  with
  ```go
  	"github.com/aidan-bailey/loom/session"
  	"github.com/aidan-bailey/loom/session/subagent"
  	"github.com/aidan-bailey/loom/session/transcript"
  ```
  Then add, after `TestReadTurns_RepliesWithAPageOfTheTranscript`:

  ```go
  func TestReadTurns_PassesTheAgentQueryToTheReader(t *testing.T) {
  	path := filepath.Join(t.TempDir(), readsConv+".jsonl")
  	require.NoError(t, os.WriteFile(path, []byte(
  		`{"type":"user","timestamp":"2026-11-06T09:00:00.000Z","promptSource":"typed","message":{"role":"user","content":"survey the tree"}}`+"\n"), 0o600))
  	dir := subagent.SubagentsDir(path)
  	require.NoError(t, os.MkdirAll(dir, 0o700))
  	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-a1f00dcafe0001.jsonl"), []byte(
  		`{"type":"user","isSidechain":true,"agentId":"a1f00dcafe0001","timestamp":"2026-11-06T09:00:01.000Z","message":{"role":"user","content":"Map the tree and report."}}`+"\n"+
  			`{"type":"assistant","isSidechain":true,"agentId":"a1f00dcafe0001","timestamp":"2026-11-06T09:00:04.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Mapped."}]}}`+"\n"), 0o600))
  	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-a1f00dcafe0001.meta.json"),
  		[]byte(`{"agentType":"Explore","description":"map the tree","finished":true}`), 0o600))
  	m := NewForTest(Options{})
  	ws := storedWorkspace(t, "a")
  	inst := transcriptInst(t, "paused", path)
  	ws.add(inst)
  	m.SetWorkspacesForTest(ws)
  	id := m.idOf(inst)

  	read := func(req ReqID, q transcript.Query) Reply {
  		t.Helper()
  		m.ReadTurns(id, q, req)
  		rs := replies(run(m, m.Drain()))
  		require.Len(t, rs, 1)
  		assert.Equal(t, req, rs[0].Req)
  		return rs[0]
  	}

  	conversation := read(10, transcript.Query{})
  	require.NoError(t, conversation.Err)
  	assert.Nil(t, conversation.Turns.Agent, "a read of the conversation has no agent page")
  	require.Len(t, conversation.Turns.Turns, 1)
  	assert.Equal(t, "survey the tree", conversation.Turns.Turns[0].Text)

  	sub := read(11, transcript.Query{Agent: "a1f00dcafe0001"})
  	require.NoError(t, sub.Err)
  	require.NotNil(t, sub.Turns.Agent, "Query.Agent reaches the reader and Page.Agent comes back")
  	assert.Equal(t, "a1f00dcafe0001", sub.Turns.Agent.ID)
  	assert.Equal(t, "Explore", sub.Turns.Agent.Type)
  	assert.Equal(t, transcript.AgentFinished, sub.Turns.Agent.State)
  	assert.False(t, sub.Turns.Agent.Missing)
  	require.Len(t, sub.Turns.Turns, 1)
  	assert.Equal(t, transcript.FromBrief, sub.Turns.Turns[0].From)
  	assert.Equal(t, "Map the tree and report.", sub.Turns.Turns[0].Text)
  	require.NotEmpty(t, sub.Turns.Turns[0].Items)
  	assert.Equal(t, "Mapped.", sub.Turns.Turns[0].Items[0].Text)

  	gone := read(12, transcript.Query{Agent: "a2e2e2e2e2e2"})
  	require.NoError(t, gone.Err, "an agent whose file is not there is a page, not a failure")
  	require.NotNil(t, gone.Turns.Agent)
  	assert.True(t, gone.Turns.Agent.Missing)
  	assert.Empty(t, gone.Turns.Turns)

  	bad := read(13, transcript.Query{Agent: "../x"})
  	require.Error(t, bad.Err)
  	assert.ErrorContains(t, bad.Err, "not an agent id")
  	assert.Nil(t, bad.Turns.Agent)
  }
  ```

- [ ] **Step 2: Run it to see it fail, then see the protocol reference stale.**
  `CGO_ENABLED=0 go test ./core/ -run TestReadTurns_PassesTheAgentQueryToTheReader`
  With Story 1 in the tree it already passes (`ReadTurns` hands the query on unchanged); without it, it fails to build (`unknown field Agent in struct literal of type transcript.Query`). The red here is the reference:
  `CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference`
  fails with a diff: `docs/specs/protocol.md` still shows `"query":{"Conversation":"","Before":0,"After":0}` and a `Turns` object without the new fields (`Agent` among them).

- [ ] **Step 3: Implement.** No Go code changes: `ReadTurns` and `readTurnsJob` pass the query on as it is. Regenerate the reference (never edit it by hand):
  `CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update`
  Check `git diff docs/specs/protocol.md` changes only the `query` params line and the `Turns` objects, and that `rpc.Protocol` is untouched.

- [ ] **Step 4: Run it to see it pass.**
  `CGO_ENABLED=0 go test ./core/ ./core/rpc/`
  Expected: `ok` for both. Mutation check: in `readTurnsJob` (`core/reads.go`) pass `transcript.Query{Conversation: query.Conversation, Before: query.Before, After: query.After}` to `transcript.Read`; the test goes red at `require.NotNil(t, sub.Turns.Agent, …)`; restore it.

- [ ] **Step 5: Commit.**
  ```
  git commit -m "feat(core,rpc): ReadTurns carries the subagent query on the wire" -- core/reads_test.go docs/specs/protocol.md
  ```

### Task 2.3: the turns route reads `?agent=` and maps the new fields

**Files:**
- Modify: `internal/web/reads.go:1-12` (imports), `:20-62` (`turnsJSON`, `itemJSON`, `stepJSON`), `:89-125` (`turnsOf`), `:172-196` (`turnsQuery`)
- Modify: `internal/web/reads_test.go`
- Modify: `internal/web/README.md:71`

- [ ] **Step 1: Write the failing tests.** In `internal/web/reads_test.go`:

  Add the two imports. Replace
  ```go
  	"github.com/aidan-bailey/loom/core"
  	"github.com/aidan-bailey/loom/session"
  	"github.com/stretchr/testify/assert"
  ```
  with
  ```go
  	"github.com/aidan-bailey/loom/core"
  	"github.com/aidan-bailey/loom/session"
  	"github.com/aidan-bailey/loom/session/subagent"
  	"github.com/aidan-bailey/loom/session/transcript"
  	"github.com/stretchr/testify/assert"
  ```

  In `TestAPI_TurnsIsAPageOfTheConversation` a step now always carries `finished`. Replace
  ```go
  				 "steps": [{"tool": "Bash", "summary": "gh pr create", "at": "2026-10-23T12:20:05Z", "state": "pending"}]},
  ```
  with
  ```go
  				 "steps": [{"tool": "Bash", "summary": "gh pr create", "at": "2026-10-23T12:20:05Z", "state": "pending", "finished": false}]},
  ```

  Add after `TestAPI_TurnsTakesItsCursorsAndRefusesBadOnes`:

  ```go
  func TestTurnsOf_MapsThinkingStepsAndTheAgentPage(t *testing.T) {
  	at := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
  	page := transcript.Page{
  		Conversation: webConv,
  		Turns: []transcript.Turn{{
  			At: at, From: transcript.FromBrief, Text: "Map the tree.",
  			Items: []transcript.Item{
  				{Kind: transcript.KindThinking, At: at, Text: "Still in design.", Cut: true},
  				{Kind: transcript.KindThinking, At: at, Text: "Next."},
  				{Kind: transcript.KindSteps, At: at, Total: 2, Steps: []transcript.Step{
  					{ID: "t1", Tool: "Agent", Summary: "map", At: at, State: transcript.StateDone, Agent: "a1f00dcafe0001", Finished: true},
  					{ID: "t2", Tool: "Bash", At: at, State: transcript.StatePending},
  				}},
  			},
  		}},
  		Agent: &transcript.AgentInfo{
  			ID: "a1f00dcafe0001", Type: "Explore", Description: "map", Teammate: true,
  			State: transcript.AgentRunning, Started: at, Missing: true,
  		},
  	}

  	got, err := json.Marshal(turnsOf(page))
  	require.NoError(t, err)

  	assert.JSONEq(t, `{
  		"conversation": "`+webConv+`",
  		"turns": [{
  			"id": 0, "at": "2026-11-06T09:00:00Z", "from": "brief", "name": "", "text": "Map the tree.",
  			"cut": false, "command": false, "partial": false,
  			"items": [
  				{"kind": "thinking", "at": "2026-11-06T09:00:00Z", "text": "Still in design.", "truncated": true},
  				{"kind": "thinking", "at": "2026-11-06T09:00:00Z", "text": "Next."},
  				{"kind": "steps", "at": "2026-11-06T09:00:00Z", "total": 2, "steps": [
  					{"tool": "Agent", "summary": "map", "at": "2026-11-06T09:00:00Z", "state": "done", "agent": "a1f00dcafe0001", "finished": true},
  					{"tool": "Bash", "summary": "", "at": "2026-11-06T09:00:00Z", "state": "pending", "finished": false}
  				]}
  			]
  		}],
  		"earlier": null, "reset": false, "skipped": 0,
  		"agent": {"id": "a1f00dcafe0001", "type": "Explore", "description": "map", "teammate": true,
  			"state": "running", "started": "2026-11-06T09:00:00Z", "missing": true}
  	}`, string(got), "a thinking item is text and truncated; a step names its agent only when it has one")

  	page.Agent = nil
  	got, err = json.Marshal(turnsOf(page))
  	require.NoError(t, err)
  	assert.NotContains(t, string(got), `"agent":{`, "a conversation page has no agent object")
  }

  // agentFixture writes a subagent's own transcript and meta file beside the
  // session's transcript, as Claude lays them out.
  func agentFixture(t *testing.T, inst *session.Instance, agentID, meta string, lines ...string) {
  	t.Helper()
  	_, path := inst.ClaudeSession()
  	dir := subagent.SubagentsDir(path)
  	require.NoError(t, os.MkdirAll(dir, 0o700))
  	if meta != "" {
  		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-"+agentID+".meta.json"), []byte(meta), 0o600))
  	}
  	if len(lines) > 0 {
  		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-"+agentID+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600))
  	}
  }

  func TestAPI_TurnsLinkAnAgentStepToItsSubagentAndKeepThinkingText(t *testing.T) {
  	inst := apiWithTranscript(t, "talk",
  		`{"type":"user","timestamp":"2026-11-06T09:00:00.000Z","promptSource":"typed","message":{"role":"user","content":"survey the tree"}}`,
  		`{"type":"assistant","timestamp":"2026-11-06T09:00:02.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"thinking","thinking":"Still in design: items 1 and 2 are worked out.","signature":"sig"},{"type":"redacted_thinking","data":"x"}]}}`,
  		`{"type":"assistant","timestamp":"2026-11-06T09:00:05.000Z","cwd":"/w","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_agent1","name":"Agent","input":{"description":"map the tree","subagent_type":"Explore","prompt":"Map the tree."}}]}}`,
  		`{"type":"assistant","timestamp":"2026-11-06T09:00:06.000Z","cwd":"/w","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_agent2","name":"Task","input":{"description":"later survey","prompt":"Survey."}}]}}`,
  		`{"type":"user","timestamp":"2026-11-06T09:00:40.000Z","toolUseResult":{"agentId":"a1f00dcafe0001","status":"completed"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_agent1","content":"done"}]}}`,
  		`{"type":"assistant","timestamp":"2026-11-06T09:00:45.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Mapped."}]}}`,
  	)
  	agentFixture(t, inst, "a1f00dcafe0001",
  		`{"agentType":"Explore","description":"map the tree","toolUseId":"toolu_agent1","finished":true}`)
  	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})

  	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"]))
  	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)

  	var page turnsJSON
  	require.NoError(t, json.Unmarshal(got.body, &page))
  	require.Len(t, page.Turns, 1)
  	assert.Nil(t, page.Agent, "a conversation page has no agent object")
  	var thinking []itemJSON
  	steps := map[string]stepJSON{}
  	for _, it := range page.Turns[0].Items {
  		switch it.Kind {
  		case transcript.KindThinking:
  			thinking = append(thinking, it)
  		case transcript.KindSteps:
  			for _, s := range it.Steps {
  				steps[s.Summary] = s
  			}
  		}
  	}
  	require.Len(t, thinking, 1, "a blank and a redacted thinking block add nothing")
  	assert.Equal(t, "Still in design: items 1 and 2 are worked out.", thinking[0].Text)
  	assert.False(t, thinking[0].Truncated)
  	assert.Empty(t, thinking[0].Blocks, "thinking crosses as text, not as a tree")

  	linked := steps["map the tree"]
  	assert.Equal(t, "Agent", linked.Tool)
  	assert.Equal(t, "a1f00dcafe0001", linked.Agent)
  	assert.True(t, linked.Finished, "meta says finished")
  	unlinked := steps["later survey"]
  	assert.Equal(t, "Task", unlinked.Tool)
  	assert.Empty(t, unlinked.Agent, "no meta and no result: no link is guessed")
  	assert.False(t, unlinked.Finished)

  	assert.Contains(t, string(got.body), `"agent":"a1f00dcafe0001"`)
  	assert.Contains(t, string(got.body), `"finished":true`)
  	assert.NotContains(t, string(got.body), `"agent":""`, "an unlinked step leaves the key out")
  }

  func TestAPI_TurnsOfASubagentAreItsOwnWithItsAgentPage(t *testing.T) {
  	inst := apiWithTranscript(t, "talk", talkLines...)
  	agentFixture(t, inst, "a1f00dcafe0001",
  		`{"agentType":"Explore","description":"map the tree","toolUseId":"toolu_agent1","finished":true}`,
  		`{"type":"user","isSidechain":true,"agentId":"a1f00dcafe0001","timestamp":"2026-11-06T09:00:01.000Z","message":{"role":"user","content":"Map the tree and report."}}`,
  		`{"type":"assistant","isSidechain":true,"agentId":"a1f00dcafe0001","timestamp":"2026-11-06T09:00:04.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Mapped."}]}}`,
  	)
  	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})
  	base := fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"])

  	got := env.readAnswer(base + "?agent=a1f00dcafe0001")
  	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
  	var page turnsJSON
  	require.NoError(t, json.Unmarshal(got.body, &page))
  	require.NotNil(t, page.Agent)
  	assert.Equal(t, agentPageJSON{
  		ID: "a1f00dcafe0001", Type: "Explore", Description: "map the tree",
  		State: transcript.AgentFinished, Started: "2026-11-06T09:00:01Z",
  	}, *page.Agent)
  	require.Len(t, page.Turns, 1)
  	assert.Equal(t, transcript.FromBrief, page.Turns[0].From)
  	assert.Equal(t, "Map the tree and report.", page.Turns[0].Text)
  	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "turns", session: env.ids["talk"], result: "200 ok"})

  	gone := env.readAnswer(base + "?agent=a2e2e2e2e2e2")
  	require.Equal(t, http.StatusOK, gone.status, "an agent with no file is a page, not an error; body: %s", gone.body)
  	var empty turnsJSON
  	require.NoError(t, json.Unmarshal(gone.body, &empty))
  	require.NotNil(t, empty.Agent)
  	assert.True(t, empty.Agent.Missing)
  	assert.Equal(t, "a2e2e2e2e2e2", empty.Agent.ID)
  	assert.Equal(t, transcript.AgentUnknown, empty.Agent.State, "no meta file, no state")
  	assert.Empty(t, empty.Agent.Started)
  	assert.Empty(t, empty.Turns)

  	main := env.readAnswer(base + "?agent=")
  	require.Equal(t, http.StatusOK, main.status, "body: %s", main.body)
  	var conv turnsJSON
  	require.NoError(t, json.Unmarshal(main.body, &conv))
  	assert.Nil(t, conv.Agent, "an empty agent reads the conversation")
  	require.Len(t, conv.Turns, 1)
  	assert.Equal(t, "open the PR", conv.Turns[0].Text)
  }

  func TestAPI_TurnsRefusesABadAgentID(t *testing.T) {
  	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiWithTranscript(t, "talk", talkLines...)}})
  	base := fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"])

  	for _, c := range []struct{ query, id string }{
  		{"?agent=..", ".."},
  		{"?agent=.", "."},
  		{"?agent=a%2Fb", "a/b"},
  		{"?agent=a%5Cb", `a\b`},
  		{"?agent=..%2F..%2Fx", "../../x"},
  	} {
  		status, out := env.do(http.MethodGet, base+c.query, nil)
  		assert.Equal(t, http.StatusBadRequest, status, c.query)
  		assert.Equal(t, fmt.Sprintf("agent %q: not an agent id", c.id), apiErrorOf(t, out), c.query)
  	}
  	assert.Empty(t, env.loop.JobsForTest(), "a refused query reaches no model")
  }
  ```

- [ ] **Step 2: Run it to see it fail.**
  `CGO_ENABLED=0 go test ./internal/web/ -run 'TestTurnsOf|TestAPI_Turns'`
  It fails to build: `undefined: agentPageJSON`, `page.Agent undefined (type turnsJSON has no field or method Agent)`, `unknown field Agent in struct literal of type stepJSON`.

- [ ] **Step 3: Implement.** In `internal/web/reads.go`:

  Imports: replace
  ```go
  	"github.com/aidan-bailey/loom/session/git"
  	"github.com/aidan-bailey/loom/session/transcript"
  ```
  with
  ```go
  	"github.com/aidan-bailey/loom/session/git"
  	"github.com/aidan-bailey/loom/session/subagent"
  	"github.com/aidan-bailey/loom/session/transcript"
  ```

  Replace the `turnsJSON` type:
  ```go
  // turnsJSON is a page of a session's conversation (transcript.Page) as the
  // app reads it. A text item crosses as its tree, never as its text.
  type turnsJSON struct {
  	Conversation string     `json:"conversation"`
  	Turns        []turnJSON `json:"turns"`
  	// Earlier is the before of the page before this one: null at the start
  	// of the file and on a refresh's page.
  	Earlier *int64 `json:"earlier"`
  	Reset   bool   `json:"reset"`
  	Skipped int    `json:"skipped"`
  }
  ```
  with
  ```go
  // turnsJSON is a page of a session's conversation (transcript.Page) as the
  // app reads it. A text item crosses as its tree, never as its text.
  type turnsJSON struct {
  	Conversation string     `json:"conversation"`
  	Turns        []turnJSON `json:"turns"`
  	// Earlier is the before of the page before this one: null at the start
  	// of the file and on a refresh's page.
  	Earlier *int64 `json:"earlier"`
  	Reset   bool   `json:"reset"`
  	Skipped int    `json:"skipped"`
  	// Agent describes the subagent whose turns the page holds; left out
  	// unless the query named one (?agent=).
  	Agent *agentPageJSON `json:"agent,omitempty"`
  }

  // agentPageJSON is a subagent page's agent (transcript.AgentInfo). State
  // is one of running, finished or unknown; Missing says the agent's
  // transcript file is not there, and Started is "" without a first record.
  type agentPageJSON struct {
  	ID          string `json:"id"`
  	Type        string `json:"type"`
  	Description string `json:"description"`
  	Teammate    bool   `json:"teammate"`
  	State       string `json:"state"`
  	Started     string `json:"started"`
  	Missing     bool   `json:"missing"`
  }
  ```

  Replace the `itemJSON` comment and `stepJSON`:
  ```go
  // itemJSON is one item of a turn. Its fields follow its kind: a text item
  // has blocks and truncated, a steps item total, failed and steps, a note
  // text and error; each has at.
  ```
  with
  ```go
  // itemJSON is one item of a turn. Its fields follow its kind: a text item
  // has blocks and truncated, a thinking item text and truncated (plain text,
  // never a tree), a steps item total, failed and steps, a note text and
  // error; each has at.
  ```
  and
  ```go
  type stepJSON struct {
  	Tool    string `json:"tool"`
  	Summary string `json:"summary"`
  	At      string `json:"at"`
  	State   string `json:"state"`
  }
  ```
  with
  ```go
  // stepJSON is one tool call. Agent is the subagent an Agent or Task step
  // launched, left out when none was found; Finished says that subagent has
  // ended (false for every other step).
  type stepJSON struct {
  	Tool     string `json:"tool"`
  	Summary  string `json:"summary"`
  	At       string `json:"at"`
  	State    string `json:"state"`
  	Agent    string `json:"agent,omitempty"`
  	Finished bool   `json:"finished"`
  }
  ```

  In `turnsOf`, replace
  ```go
  	if p.Earlier > 0 {
  		earlier := p.Earlier
  		out.Earlier = &earlier
  	}
  ```
  with
  ```go
  	if p.Earlier > 0 {
  		earlier := p.Earlier
  		out.Earlier = &earlier
  	}
  	if a := p.Agent; a != nil {
  		out.Agent = &agentPageJSON{
  			ID: a.ID, Type: a.Type, Description: a.Description, Teammate: a.Teammate,
  			State: a.State, Started: stamp(a.Started), Missing: a.Missing,
  		}
  	}
  ```
  and replace
  ```go
  				for _, s := range it.Steps {
  					ij.Steps = append(ij.Steps, stepJSON{Tool: s.Tool, Summary: s.Summary, At: stamp(s.At), State: s.State})
  				}
  			case transcript.KindNote:
  				ij.Text, ij.Error = it.Text, it.Error
  ```
  with
  ```go
  				for _, s := range it.Steps {
  					ij.Steps = append(ij.Steps, stepJSON{
  						Tool: s.Tool, Summary: s.Summary, At: stamp(s.At), State: s.State,
  						Agent: s.Agent, Finished: s.Finished,
  					})
  				}
  			case transcript.KindThinking:
  				ij.Text, ij.Truncated = it.Text, it.Cut
  			case transcript.KindNote:
  				ij.Text, ij.Error = it.Text, it.Error
  ```

  Replace `turnsQuery`'s comment and start:
  ```go
  // turnsQuery reads the turns route's query: before and after are byte
  // offsets, and at most one of them is set.
  func turnsQuery(r *http.Request) (transcript.Query, error) {
  	v := r.URL.Query()
  	q := transcript.Query{Conversation: v.Get("conversation")}
  ```
  with
  ```go
  // turnsQuery reads the turns route's query: before and after are byte
  // offsets, and at most one of them is set; agent, when not empty, is a
  // subagent's id (subagent.ValidAgentID), whose own turns are read in
  // place of the conversation's.
  func turnsQuery(r *http.Request) (transcript.Query, error) {
  	v := r.URL.Query()
  	q := transcript.Query{Conversation: v.Get("conversation"), Agent: v.Get("agent")}
  	if q.Agent != "" && !subagent.ValidAgentID(q.Agent) {
  		return transcript.Query{}, fmt.Errorf("agent %q: not an agent id", q.Agent)
  	}
  ```

  In `internal/web/README.md` line 71 (the turns row; use Edit, each old text unique in the file):
  - `` `GET /api/v1/sessions/{id}/turns?conversation=&before=&after=` `` becomes `` `GET /api/v1/sessions/{id}/turns?conversation=&before=&after=&agent=` ``
  - `` `from` one of `you`, `plugin`, `peer`, `task`; `` becomes `` `from` one of `you`, `plugin`, `peer`, `task` or `brief` (the first prompt of a subagent's page); ``
  - `` (`{tool, summary, at, state}`, `state` one of `pending`, `done`, `failed`, `interrupted`), `` becomes `` (`{tool, summary, at, state, agent, finished}`, `state` one of `pending`, `done`, `failed`, `interrupted`; `agent` is the subagent an `Agent` or `Task` step launched, left out when none was found, and `finished` says that subagent has ended), `thinking` has `text` (Claude's reasoning in plain text, drawn as text and never as markup; blank and redacted thinking is not sent) and `truncated`, ``
  - `` `reset` says the page replaces every turn held |`` becomes `` `reset` says the page replaces every turn held. `agent` (an `agentId`, as the session's `agents` and the steps give it) reads that subagent's own transcript in place of the conversation, its first prompt the `brief`; an id Claude could not have written (empty after trimming, holding `/` or `\`, `.` or `..`) is 400 `agent "<id>": not an agent id`, and an empty `agent` reads the conversation. Such a page also holds `agent`, `{id, type, description, teammate, state, started, missing}`: `state` is `running` or `finished` from the subagent's meta file, `unknown` without it; `missing` says its transcript file is not there (yet, or any more), and then `turns` is empty. A conversation page has no `agent` |``

- [ ] **Step 4: Run it to see it pass.**
  `CGO_ENABLED=0 go test ./internal/web/`
  Expected: `ok`. Mutation checks, each reverted: drop the `Agent:`/`Finished:` fields of the step mapping (the link test and the unit test go red); drop the `KindThinking` case (the thinking assertions go red); drop the `ValidAgentID` check (the refusal test goes red on the first row, which then reaches a job).

- [ ] **Step 5: Commit.**
  ```
  git commit -m "feat(web): the turns route reads ?agent= and carries agent links, thinking and the agent page" -- internal/web/reads.go internal/web/reads_test.go internal/web/README.md
  ```

### Task 2.4: a session's agents carry their id

**Files:**
- Modify: `internal/web/fleet.go:61-66` (`agentJSON`), `internal/web/fleet.go:~104` (`sessionOf`)
- Modify: `internal/web/fleet_test.go:77,113`
- Modify: `internal/web/README.md:83`

- [ ] **Step 1: Write the failing test.** In `internal/web/fleet_test.go` (the session-JSON test), replace
  ```go
  		Subagents:      []subagent.View{{Name: "explorer", Description: "reads the tree", Idle: true}},
  ```
  with
  ```go
  		Subagents:      []subagent.View{{ID: "a1f00dcafe0001", Name: "explorer", Description: "reads the tree", Idle: true}},
  ```
  and replace
  ```go
  		"agents": [{"name": "explorer", "description": "reads the tree", "idle": true}],
  ```
  with
  ```go
  		"agents": [{"id": "a1f00dcafe0001", "name": "explorer", "description": "reads the tree", "idle": true}],
  ```

- [ ] **Step 2: Run it to see it fail.**
  `CGO_ENABLED=0 go test ./internal/web/ -run TestSessionOf`
  The JSON comparison fails: the actual `agents` entry lacks `"id": "a1f00dcafe0001"`.

- [ ] **Step 3: Implement.** In `internal/web/fleet.go` replace
  ```go
  // agentJSON is one running subagent, as subagent.View has it.
  type agentJSON struct {
  	Name        string `json:"name"`
  ```
  with
  ```go
  // agentJSON is one running subagent, as subagent.View has it. ID is the
  // agent id the app opens its page with (?agent=).
  type agentJSON struct {
  	ID          string `json:"id"`
  	Name        string `json:"name"`
  ```
  and in `sessionOf` replace
  ```go
  		s.Agents = append(s.Agents, agentJSON{Name: a.Name, Description: a.Description, Idle: a.Idle})
  ```
  with
  ```go
  		s.Agents = append(s.Agents, agentJSON{ID: a.ID, Name: a.Name, Description: a.Description, Idle: a.Idle})
  ```
  In `internal/web/README.md` line 83 replace `` `account`, `agents`, `remote_url` `` with `` `account`, `agents` (each `id`, `name`, `description`, `idle`; `id` is the agent id `?agent=` takes), `remote_url` ``.

- [ ] **Step 4: Run it to see it pass.**
  `CGO_ENABLED=0 go test ./session/subagent/ ./core/ ./core/rpc/ ./internal/web/`
  Expected: `ok` for all four. Mutation check: drop `ID: a.ID` from `sessionOf`; the session-JSON test goes red; restore it.

- [ ] **Step 5: Commit.**
  ```
  git commit -m "feat(web): carry the agent id on a session's agents" -- internal/web/fleet.go internal/web/fleet_test.go internal/web/README.md
  ```

## Story 3: The app opens on the conversation

- **Developer:** intermediate-dev
- **Files:** `internal/web/static/js/app.mjs`, `internal/web/static/js/screens.mjs`, `internal/web/static/js/chrome.mjs`, `internal/web/static/js/strip.mjs`, `internal/web/static/js/needs.mjs`, `internal/web/static/js/session.mjs`, `internal/web/static/js/turns.mjs`, `internal/web/static/js/fleet.mjs`, `internal/web/static/js/usage.mjs`, `internal/web/static/app.css`, `tools/webshot/main.go`, `tools/webshot/webshot_test.go`, `tools/webshot/fixture/turns.json`, `tools/webshot/fixture/snapshot.json`
- **Interfaces:** uses Story 2's JSON fields; exposes `parseRoute`'s routes, `FactStrip`, `NeedsYou`, `Info`, `useTurnPages`, `TurnList`, the tab keys and the webshot screen names
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && go run ./tools/webshot -out /tmp/loom-shots-s3 -screens conversation,waiting,info,fleet'`
- **Review:** deep

A session now opens on its conversation. `parseRoute` maps `#s/<id>` to the `turns` route (and `#s/<id>/turns`, answered in place with `history.replaceState`), `#s/<id>/info` to the info tab and `#s/<id>/agent/<agentId>` to a route name `agent` that Story 4 gives a screen (until it lands, that route draws "Nothing here yet."). `SessionTabs` becomes conversation · changes · info · mockup; `SessionHead` pins a `FactStrip` under the tabs on the conversation. The old session screen becomes `Info` (no last message, no needs-you box), and the needs-you box moves, unchanged, into `js/needs.mjs` and the conversation's foot. `js/turns.mjs` is split into `useTurnPages` (paging, refresh, scroll anchoring) and `TurnList` (the turn renderer), which Story 4's subagent screen reuses, and draws thinking items as folds and Agent steps as links to `#s/<id>/agent/<agent>`. A session that does not run Claude draws `Info` at `#s/<id>`. The app has no JS test harness (`docs/claude/web-layer.md`): the checks are `node --check`, the static Go tests over the modules, and the webshot run, so a task's "failing" step is whichever of those can fail first. The webshot fixtures gain an Agent step, a thinking item and agent ids; the `session` and `turns` screens retire for `conversation` and `info`. No code here holds a landmine marker.

### Task 3.1: The needs-you box and the fact strip as modules

**Files:**
- Create: `internal/web/static/js/needs.mjs`
- Create: `internal/web/static/js/strip.mjs`
- Modify: `internal/web/static/js/fleet.mjs:1,133-135`
- Modify: `internal/web/static/js/usage.mjs:3`
- Modify: `internal/web/static/app.css:457`

- [ ] **Step 1: Write the two modules**

Create `internal/web/static/js/needs.mjs`. The markup and classes are those of the box in `session.mjs` (lines 239-247), unchanged:

```js
// The needs-you box: why a session waits, the dialogs its Claude holds open
// (asks.mjs) and the way into Claude. The conversation draws it at its foot
// while the session's group is needs_you.
import { html } from "../lib/preact-htm.mjs";
import { GLYPHS } from "./skin.mjs";
import { Asks } from "./asks.mjs";

export function NeedsYou({ s, off, run }) {
  return html`<div class="sc-box sc-attn-box">
    <span class="sc-legend">${GLYPHS.needs_you} Needs you</span>
    <div class="sc-attn">${(s.wait && s.wait.reason) || "Needs you"}</div>
    ${s.wait && s.wait.detail && !(s.asks && s.asks.length) ? html`<pre class="sc-detail-box">${s.wait.detail}</pre>` : null}
    <${Asks} s=${s} off=${off} run=${run} />
    ${s.remote_url
      ? html`<a class=${"sc-btn sc-block" + (s.asks && s.asks.length ? "" : " sc-primary")} href=${s.remote_url} target="_blank" rel="noopener">Open in Claude</a>`
      : html`<p class="sc-dim sc-small">Remote Control is off for this session.</p>`}
  </div>`;
}
```

Create `internal/web/static/js/strip.mjs`:

```js
// The fact strip pinned under a session's tabs on the conversation: its
// status and age, its changes, its PR and its account's share, on one
// ellipsised line that opens the info tab. A fact the session lacks is left
// out.
import { html } from "../lib/preact-htm.mjs";
import { ago, PrMark } from "./fleet.mjs";
import { glyphOf } from "./skin.mjs";
import { rowShare } from "./usage.mjs";

export function FactStrip({ s, snap, now }) {
  const d = s.diff || {};
  const share = rowShare(snap, s, now);
  const facts = [
    {
      key: "status",
      node: html`<span class=${"sc-glyph sc-glyph-" + s.group} aria-hidden="true">${glyphOf(s.group)}</span> ${(s.label || "").toLowerCase()}${s.since ? " " + ago(s.since, now) : ""}`,
    },
  ];
  if (d.added || d.removed) {
    facts.push({ key: "diff", node: html`<span class="sc-add">+${d.added || 0}</span> <span class="sc-del">−${d.removed || 0}</span>` });
  }
  if (s.pr) facts.push({ key: "pr", node: html`PR #${s.pr.number} <${PrMark} pr=${s.pr} />` });
  if (share) facts.push({ key: "share", node: html`<span class=${share.hot ? "sc-hot" : ""}>${share.text}</span>` });
  return html`<a class="sc-strip" href=${"#s/" + s.id + "/info"}>
    ${facts.map((f, i) => html`<span key=${f.key}>${i > 0 ? " · " : ""}${f.node}</span>`)}
  </a>`;
}
```

- [ ] **Step 2: Run the static tests to see them fail**

`strip.mjs` imports `PrMark`, which `fleet.mjs` does not export yet.

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestStatic_NamedImportsAreExported'`
Expected: FAIL, `/js/strip.mjs imports PrMark, which /js/fleet.mjs does not export`.

- [ ] **Step 3: Export `PrMark`, reword two stale comments and add the strip's style**

In `internal/web/static/js/fleet.mjs`, replace the file's first line

```js
// The fleet screen, and the helpers the session screen shares with it.
```

with

```js
// The fleet screen, and the helpers the conversation and the info tab share with it.
```

In `internal/web/static/js/usage.mjs`, replace the comment's third line

```js
// The fleet's rows and the session screen's Account tile read them.
```

with

```js
// The fleet's rows and the info tab's Account tile read them.
```

In `internal/web/static/js/fleet.mjs`, replace

```js
function PrMark({ pr }) {
```

with

```js
export function PrMark({ pr }) {
```

In `internal/web/static/app.css`, after the line

```css
.sc-tab-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--accent); }
```

add:

```css
/* The conversation's fact strip, pinned under the tabs: one ellipsised line,
   the whole of it a link to the info tab. Inline children only, so the
   ellipsis works and a text's leading space survives. */
.sc-strip {
  display: block;
  padding: 7px 16px 8px;
  border-top: 1px solid var(--rule);
  font-size: 12px;
  color: var(--dim);
  text-decoration: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sc-strip .sc-hot { color: var(--attention); }
```

- [ ] **Step 4: Run the checks to see them pass**

Run: `bash -c 'for f in internal/web/static/js/*.mjs; do node --check "$f" || exit 1; done; CGO_ENABLED=0 go test ./internal/web/ -run TestStatic'`
Expected: no output from `node --check`, then `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 5: Commit**

```bash
git add -N internal/web/static/js/needs.mjs internal/web/static/js/strip.mjs
git commit -m "feat(web): the needs-you box and a fact strip as modules" -- internal/web/static/js/needs.mjs internal/web/static/js/strip.mjs internal/web/static/js/fleet.mjs internal/web/static/js/usage.mjs internal/web/static/app.css
```

### Task 3.2: Routes, tabs and the info tab

**Files:**
- Modify: `internal/web/static/js/app.mjs:22-38,128,131-133,163,323,379`
- Modify: `internal/web/static/js/chrome.mjs:48-86`
- Modify: `internal/web/static/js/screens.mjs:1-22`
- Modify: `internal/web/static/js/session.mjs:1-324`
- Modify: `internal/web/static/app.css` (the `.sc-rule-link` rule)

- [ ] **Step 1: Route the new screens**

In `internal/web/static/js/app.mjs`, replace the comment and `parseRoute`:

```js
// parseRoute reads the location's hash: "" is the fleet, "#s/<id>" a session,
// "#s/<id>/turns" its conversation, "#s/<id>/diff" its changes,
// "#s/<id>/mockup" its brainstorm mockup and "#s/<id>/mockup/full" the mockup
// alone (full: no header, no reply box), "#new", "#settings", "#pair";
// anything else is the fleet. The session's id is a number, as in the JSON.
export function parseRoute(hash) {
  const h = (hash || "").replace(/^#\/?/, "");
  if (h === "new") return { name: "new" };
  if (h === "settings") return { name: "settings" };
  if (h === "pair") return { name: "pair" };
  const m = h.match(/^s\/(\d+)(?:\/(turns|diff|mockup))?(\/full)?$/);
  if (m && (!m[3] || m[2] === "mockup")) {
    const name = { turns: "turns", diff: "changes", mockup: "mockup" }[m[2]] || "session";
    return m[3] ? { name, id: Number(m[1]), full: true } : { name, id: Number(m[1]) };
  }
  return { name: "fleet" };
}
```

with:

```js
// parseRoute reads the location's hash: "" is the fleet, "#s/<id>" a session's
// conversation (route name "turns"), "#s/<id>/info" its info tab,
// "#s/<id>/diff" its changes, "#s/<id>/mockup" its brainstorm mockup and
// "#s/<id>/mockup/full" the mockup alone (full: no header, no reply box),
// "#s/<id>/agent/<agentId>" one of its subagents' turns, "#new", "#settings",
// "#pair"; anything else is the fleet. The session's id is a number, as in the
// JSON. "#s/<id>/turns" is the conversation's old address: it parses as
// "turns" with legacy set, and settle answers it in place.
export function parseRoute(hash) {
  const h = (hash || "").replace(/^#\/?/, "");
  if (h === "new") return { name: "new" };
  if (h === "settings") return { name: "settings" };
  if (h === "pair") return { name: "pair" };
  const a = h.match(/^s\/(\d+)\/agent\/([A-Za-z0-9_-]+)$/);
  if (a) return { name: "agent", id: Number(a[1]), agent: a[2] };
  const m = h.match(/^s\/(\d+)(?:\/(turns|diff|mockup|info))?(\/full)?$/);
  if (m && (!m[3] || m[2] === "mockup")) {
    const id = Number(m[1]);
    if (m[2] === "turns") return { name: "turns", id, legacy: true };
    const name = { diff: "changes", mockup: "mockup", info: "info" }[m[2]] || "turns";
    return m[3] ? { name, id, full: true } : { name, id };
  }
  return { name: "fleet" };
}

// settle answers a legacy route (#s/<id>/turns) by replacing its history
// entry with #s/<id>, so an old link or entry still works and back does not
// land on the address twice. replaceState fires no hashchange, so the route
// it returns is the one to draw.
function settle(route) {
  if (!route.legacy) return route;
  history.replaceState(null, "", "#s/" + route.id);
  return { name: "turns", id: route.id };
}
```

Replace

```js
  const [route, setRoute] = useState(() => parseRoute(location.hash));
```

with

```js
  const [route, setRoute] = useState(() => settle(parseRoute(location.hash)));
```

Replace (in `go`)

```js
      setRoute(parseRoute(h));
```

with

```js
      setRoute(settle(parseRoute(h)));
```

Replace

```js
    const onHash = () => setRoute(parseRoute(location.hash));
```

with

```js
    const onHash = () => setRoute(settle(parseRoute(location.hash)));
```

Replace the comment on `pending`, which names the old session screen:

```js
  // pending is the id of a session whose create answered 201 but which the
  // fleet does not list yet; the session screen says "Loading…" for it. It
  // clears once the fleet lists it, or after pendingFor.
```

with

```js
  // pending is the id of a session whose create answered 201 but which the
  // fleet does not list yet; the session's head says "Loading…" for it. It
  // clears once the fleet lists it, or after pendingFor.
```

Replace the screen's key, so that moving between two subagents of one session remounts the screen:

```js
      ? html`<${entry.Screen} key=${route.name + (route.id === undefined ? "" : route.id)} ...${props} />`
```

with

```js
      ? html`<${entry.Screen} key=${route.name + (route.id === undefined ? "" : route.id) + (route.agent ? "/" + route.agent : "")} ...${props} />`
```

- [ ] **Step 2: Rewrite the tabs and let the way back carry a handler**

In `internal/web/static/js/chrome.mjs`, replace `ContextRow`:

```js
// ContextRow is the header's first row: an optional way back, the screen's
// title as its h1 on one line, and the screen's actions (children).
export function ContextRow({ back, title, children }) {
  return html`<div class="sc-context">
    ${back ? html`<a class="sc-back" href=${back.href}>‹ ${back.label}</a>` : null}
    <h1 class="sc-h1">${title}</h1>
    ${children}
  </div>`;
}
```

with:

```js
// ContextRow is the header's first row: an optional way back (an href and a
// label, and an onClick that may take the click over), the screen's title as
// its h1 on one line, and the screen's actions (children).
export function ContextRow({ back, title, children }) {
  return html`<div class="sc-context">
    ${back ? html`<a class="sc-back" href=${back.href} onClick=${back.onClick}>‹ ${back.label}</a>` : null}
    <h1 class="sc-h1">${title}</h1>
    ${children}
  </div>`;
}
```

Replace `SessionTabs` (its comment and function, from `// SessionTabs are a session's views` to the closing `}` of the function):

```js
// SessionTabs are a session's views under its context row: the session, its
// conversation (conversation: a Claude session) and its changes (changes:
// while its diff can be read, with its line counts). A tab shows when it
// applies or is the current one, so a session that pauses on its changes
// keeps the tab under the user; a lone session tab draws no row.
export function SessionTabs({ s, current, conversation, changes }) {
  const base = "#s/" + s.id;
  const d = s.diff || {};
  const tabs = [{ key: "session", label: "session", href: base }];
  if (conversation || current === "turns") tabs.push({ key: "turns", label: "conversation", href: base + "/turns" });
  if (changes || current === "changes") {
    tabs.push({ key: "changes", label: "changes", href: base + "/diff", diff: d.added || d.removed ? d : null });
  }
  // The mockup tab shows while the session's brainstorm companion runs; its
  // dot while the screen is new this turn.
  if (s.mockup || current === "mockup") {
    tabs.push({ key: "mockup", label: "mockup", href: base + "/mockup", fresh: Boolean(s.mockup && s.mockup.fresh) });
  }
  if (tabs.length === 1) return null;
```

with:

```js
// SessionTabs are a session's views under its context row: its conversation
// (conversation: a Claude session; the tab key is "turns" and #s/<id> its
// address), its changes (changes: while its diff can be read, with its line
// counts), its info and its mockup. A tab shows when it applies or is the
// current one, so a session that pauses on its changes keeps the tab under
// the user; a lone info tab draws no row.
export function SessionTabs({ s, current, conversation, changes }) {
  const base = "#s/" + s.id;
  const d = s.diff || {};
  const tabs = [];
  if (conversation) tabs.push({ key: "turns", label: "conversation", href: base });
  if (changes || current === "changes") {
    tabs.push({ key: "changes", label: "changes", href: base + "/diff", diff: d.added || d.removed ? d : null });
  }
  tabs.push({ key: "info", label: "info", href: base + "/info" });
  // The mockup tab shows while the session's brainstorm companion runs; its
  // dot while the screen is new this turn.
  if (s.mockup || current === "mockup") {
    tabs.push({ key: "mockup", label: "mockup", href: base + "/mockup", fresh: Boolean(s.mockup && s.mockup.fresh) });
  }
  if (tabs.length === 1) return null;
```

The `return html`<nav …>` below it is unchanged.

- [ ] **Step 3: Name the screens**

Replace the whole of `internal/web/static/js/screens.mjs` (the comment's text is unchanged):

```js
// The screens app.mjs renders by route name: each a Screen (the body, in
// <main>) and a Head (its context row and, for a session, its tabs, in the
// pinned header; the status row is the footer's), both with the props
// {snap, stale, link, route, go, act, toast, pending}. Pairing is app.mjs's
// own screen.
import { Fleet, FleetHead } from "./fleet.mjs";
import { Info, SessionHead } from "./session.mjs";
import { NewSession, NewHead } from "./newsession.mjs";
import { Settings, SettingsHead } from "./settings.mjs";
import { Turns } from "./turns.mjs";
import { Changes } from "./changes.mjs";
import { Mockup } from "./mockup.mjs";

export const screens = {
  fleet: { Screen: Fleet, Head: FleetHead },
  info: { Screen: Info, Head: SessionHead },
  new: { Screen: NewSession, Head: NewHead },
  settings: { Screen: Settings, Head: SettingsHead },
  turns: { Screen: Turns, Head: SessionHead },
  changes: { Screen: Changes, Head: SessionHead },
  mockup: { Screen: Mockup, Head: SessionHead },
};
```

- [ ] **Step 4: Run the static tests to see them fail**

`screens.mjs` imports `Info`, which `session.mjs` does not export yet.

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestStatic_NamedImportsAreExported'`
Expected: FAIL, `/js/screens.mjs imports Info, which /js/session.mjs does not export`.

- [ ] **Step 5: Turn the session screen into the info tab and the head into the conversation's**

In `internal/web/static/js/session.mjs`, replace the header and imports:

```js
// The session screen: why it waits and the asks to answer, its tiles, the last message, its agents,
// the actions, and the reply box with the quick replies. The changes tile
// opens the changes screen, and the last message's rule the conversation.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { ConfirmSheet } from "./sheets.mjs";
import { allSessions, ago, markSeen, sessionOf } from "./fleet.mjs";
import { ContextRow, FLEET_BACK, SessionTabs } from "./chrome.mjs";
import { GLYPHS, glyphOf, OutIcon, ChevronIcon } from "./skin.mjs";
import { usageFor, shareText, isStale, resetPassed, resetText, shownWindow } from "./usage.mjs";
import { Markdown } from "./markdown.mjs";
import { ReplyBox, useRun } from "./reply.mjs";
import { Asks } from "./asks.mjs";
```

with:

```js
// The info tab (#s/<id>/info, and #s/<id> for a session that does not run
// Claude): its tiles, its agents, the actions, and the reply box with the
// quick replies. The changes tile opens the changes screen. The conversation
// is turns.mjs's and the needs-you box needs.mjs's.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { ConfirmSheet } from "./sheets.mjs";
import { allSessions, ago, markSeen, sessionOf } from "./fleet.mjs";
import { ContextRow, FLEET_BACK, SessionTabs } from "./chrome.mjs";
import { FactStrip } from "./strip.mjs";
import { glyphOf, OutIcon, ChevronIcon } from "./skin.mjs";
import { usageFor, shareText, isStale, resetPassed, resetText, shownWindow } from "./usage.mjs";
import { ReplyBox, useRun } from "./reply.mjs";
```

Delete the `Message` component and its comment (the block that starts `// Message is the last message: its tree once fetched` and ends with the `}` before `// Agents are the session's subagents by name`):

```js
// Message is the last message: its tree once fetched for this very text, the
// plain text before that and whenever the fetch fails. For a Claude session
// its rule ends in the way into the conversation, shown even before the
// first message, which is when a first turn is worth watching.
function Message({ s, tree }) {
  const ok = s.last_message && tree && tree.text === s.last_message;
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Last message${s.claude ? html`<a class="sc-rule-link" href=${"#s/" + s.id + "/turns"}>conversation ›</a>` : null}</h2>
      ${!s.last_message
        ? html`<p class="sc-pre sc-dim">No message yet.</p>`
        : ok
          ? html`<${Markdown} blocks=${tree.blocks} />`
          : html`<p class="sc-pre">${s.last_message}</p>`}
      ${ok && tree.truncated
        ? html`<p class="sc-dim sc-small">… continues in ${s.remote_url ? html`<a href=${s.remote_url} target="_blank" rel="noopener">Claude</a>` : "Claude"}</p>`
        : null}
    </section>
  `;
}

```

Replace the start of the screen, through the message-tree effect:

```js
export function Session({ snap, stale, route, go, act, toast, pending }) {
  const id = route.id;
  const [sheet, setSheet] = useState("");
  const [source, setSource] = useState("");
  const { busy, run } = useRun(act, toast, id);
  const [tree, setTree] = useState(null);

  // The time this phone last opened the session feeds the fleet's NEW mark;
  // it is written on arrival and again on leaving, so a turn that ended while
  // the session was open does not light it.
  useEffect(() => {
    setSheet("");
    setTree(null);
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  const s = sessionOf(snap, id);
  const msg = s ? s.last_message : "";

  // The tree is fetched for the message the stream holds, and again when the
  // stream brings another. The plain text shows until it arrives, and stays
  // when the fetch fails or the app is offline.
  useEffect(() => {
    if (id === undefined || !msg || stale || (tree && tree.text === msg)) return undefined;
    let live = true;
    call("GET", "/api/v1/sessions/" + id + "/message").then(({ status, data }) => {
      if (live && status === 200 && data && data.text === msg) setTree(data);
    });
    return () => {
      live = false;
    };
  }, [id, msg, stale]);
```

with:

```js
export function Info({ snap, stale, route, go, act, toast, pending }) {
  const id = route.id;
  const [sheet, setSheet] = useState("");
  const [source, setSource] = useState("");
  const { busy, run } = useRun(act, toast, id);

  // The time this phone last opened the session feeds the fleet's NEW mark;
  // it is written on arrival and again on leaving, so a turn that ended while
  // the session was open does not light it.
  useEffect(() => {
    setSheet("");
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  const s = sessionOf(snap, id);
```

Replace

```js
  const candidates = mergeCandidates(snap, s);
  const waiting = s.group === "needs_you";
  const now = Date.now();
```

with

```js
  const candidates = mergeCandidates(snap, s);
  const now = Date.now();
```

Replace the body's start, from the `SubLine` through the Agents line:

```js
      <${SubLine} s=${s} snap=${snap} now=${now} />
      ${waiting
        ? html`<div class="sc-box sc-attn-box">
            <span class="sc-legend">${GLYPHS.needs_you} Needs you</span>
            <div class="sc-attn">${(s.wait && s.wait.reason) || "Needs you"}</div>
            ${s.wait && s.wait.detail && !(s.asks && s.asks.length) ? html`<pre class="sc-detail-box">${s.wait.detail}</pre>` : null}
            <${Asks} s=${s} off=${off} run=${run} />
            ${s.remote_url
              ? html`<a class=${"sc-btn sc-block" + (s.asks && s.asks.length ? "" : " sc-primary")} href=${s.remote_url} target="_blank" rel="noopener">Open in Claude</a>`
              : html`<p class="sc-dim sc-small">Remote Control is off for this session.</p>`}
          </div>`
        : null}
      <${Tiles} s=${s} snap=${snap} now=${now} diff=${can.diff} />
      ${s.last_message || s.claude ? html`<${Message} s=${s} tree=${tree} />` : null}
      ${s.agents && s.agents.length > 0 ? html`<${Agents} agents=${s.agents} />` : null}
```

with:

```js
      <${SubLine} s=${s} snap=${snap} now=${now} />
      <${Tiles} s=${s} snap=${snap} now=${now} diff=${can.diff} />
      ${s.agents && s.agents.length > 0 ? html`<${Agents} agents=${s.agents} />` : null}
```

Replace `SessionHead` and its comment (the last function of the file):

```js
// SessionHead is the context row and the tabs of a session's views (session,
// conversation, changes, mockup): back to the fleet, its title, and the way to
// Claude while the session doesn't wait on you (the needs-you box has its
// own button).
export function SessionHead({ snap, route, pending }) {
  const s = sessionOf(snap, route.id);
  if (!s) return html`<${ContextRow} back=${FLEET_BACK} title=${pending === route.id ? "Loading…" : ""} />`;
  return html`
    <${ContextRow} back=${FLEET_BACK} title=${s.title}>
      ${s.remote_url && s.group !== "needs_you"
        ? html`<a class="sc-claude" href=${s.remote_url} target="_blank" rel="noopener">claude <${OutIcon} /></a>`
        : null}
    <//>
    <${SessionTabs} s=${s} current=${route.name} conversation=${Boolean(s.claude)} changes=${actionsFor(s).diff} />
  `;
}
```

with:

```js
// SessionHead is the context row, the tabs and, on the conversation, the fact
// strip of a session's views (conversation, changes, info, mockup): back to
// the fleet, its title, and the way to Claude unless the needs-you box (which
// has its own button) is on screen, as it is at a waiting Claude session's
// conversation. A session that does not run Claude has no conversation:
// its #s/<id> is the info tab, and the tab row says so.
export function SessionHead({ snap, route, pending }) {
  const s = sessionOf(snap, route.id);
  if (!s) return html`<${ContextRow} back=${FLEET_BACK} title=${pending === route.id ? "Loading…" : ""} />`;
  const talks = route.name === "turns" && s.claude;
  const current = route.name === "turns" && !s.claude ? "info" : route.name;
  const boxed = talks && s.group === "needs_you";
  return html`
    <${ContextRow} back=${FLEET_BACK} title=${s.title}>
      ${s.remote_url && !boxed
        ? html`<a class="sc-claude" href=${s.remote_url} target="_blank" rel="noopener">claude <${OutIcon} /></a>`
        : null}
    <//>
    <${SessionTabs} s=${s} current=${current} conversation=${Boolean(s.claude)} changes=${actionsFor(s).diff} />
    ${talks ? html`<${FactStrip} s=${s} snap=${snap} now=${Date.now()} />` : null}
  `;
}
```

- [ ] **Step 6: Delete the link's now-dead rule**

The `Message` link was the only user of `.sc-rule-link`. In `internal/web/static/app.css`, delete this rule and leave the lines around it:

```css
.sc-rule-link {
  order: 2;
  display: inline-flex;
  align-items: center;
  min-height: 44px;
  margin: -14px 0;
  font-size: 12px;
  font-weight: 400;
  letter-spacing: 0;
  text-transform: none;
  text-decoration: none;
  color: var(--accent);
}
```

The line before it, `.sc-rule::after { order: 1; }`, stays, and so does the `/* ---- the conversation ---- */` comment after it.

- [ ] **Step 7: Run the checks to see them pass**

Run: `bash -c 'for f in internal/web/static/js/*.mjs; do node --check "$f" || exit 1; done; CGO_ENABLED=0 go test ./internal/web/ -run TestStatic'`
Expected: no output from `node --check`, then `ok  	github.com/aidan-bailey/loom/internal/web`. (`turns.mjs` still imports `SubLine` and `actionsFor` from `session.mjs`, both still exported; Task 3.3 rewrites it.)

- [ ] **Step 8: Commit**

```bash
git commit -m "feat(web): #s/<id> is the conversation; the session screen becomes the info tab" -- internal/web/static/js/app.mjs internal/web/static/js/chrome.mjs internal/web/static/js/screens.mjs internal/web/static/js/session.mjs internal/web/static/app.css
```

### Task 3.3: The conversation: pages hook, turn list, thinking, agent links, needs-you at the foot

**Files:**
- Modify: `internal/web/static/js/turns.mjs:1-278` (replaced whole)
- Modify: `internal/web/static/app.css:643`

- [ ] **Step 1: Replace `turns.mjs`**

Replace the whole of `internal/web/static/js/turns.mjs`. `mergeTurns`, `turnLabel`, `clock` and `Prompt` are today's text, unchanged. What is new: `LINKABLE` and the link in `Step`; `Thinking` and its `Item` case; `sessionId` threaded through `Steps`, `Item` and `Turn`; `useTurnPages` and `TurnList`; and `Turns`, now a wrapper that draws `Info` for a session that does not run Claude and otherwise `Conversation`.

```js
// The conversation screen: a session's turns from Claude's transcript
// (GET /api/v1/sessions/{id}/turns), oldest first and opening at the newest;
// earlier pages on request, and the newest turn refreshed while the session
// works. Claude's text is drawn from its tree, everything else as text. The
// subagent screen (agent.mjs) reads and draws its turns with useTurnPages
// and TurnList too.
import { html, useState, useEffect, useRef, useLayoutEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { sessionOf, ago, markSeen } from "./fleet.mjs";
import { Markdown } from "./markdown.mjs";
import { Info, actionsFor } from "./session.mjs";
import { NeedsYou } from "./needs.mjs";
import { ReplyBox, useRun } from "./reply.mjs";

// POLL is how often the screen refreshes the newest turn while the session
// is Running; PROMPT_LINES how many lines of a prompt show before "more".
const POLL = 5000;
const PROMPT_LINES = 12;
// LINKABLE is what an agent id may hold for the app to link to its screen
// (parseRoute's agent route); a step with another id is a plain row.
const LINKABLE = /^[A-Za-z0-9_-]+$/;

// mergeTurns folds a page into the turns held: the first page and a reset
// replace them, an earlier page goes before them, and a refresh replaces
// every held turn from its first turn on. skipped stays set once a page
// reported a record it could not read.
export function mergeTurns(held, page, kind) {
  const skipped = Boolean((held && held.skipped) || page.skipped > 0);
  const turns = page.turns || [];
  if (!held || page.reset || kind === "newest") {
    return { conversation: page.conversation, turns, earlier: page.earlier || 0, skipped };
  }
  if (kind === "earlier") {
    const first = held.turns.length > 0 ? held.turns[0].id : Infinity;
    return { ...held, turns: turns.filter((t) => t.id < first).concat(held.turns), earlier: page.earlier || 0, skipped };
  }
  if (turns.length === 0) return { ...held, skipped };
  const from = turns[0].id;
  return { ...held, turns: held.turns.filter((t) => t.id < from).concat(turns), skipped };
}

// turnLabel is the label in a turn's box: whose the prompt is.
export function turnLabel(t) {
  switch (t.from) {
    case "you":
      return "YOU";
    case "plugin":
      return (t.name || "plugin").toUpperCase();
    case "peer":
      return t.name ? "FROM " + t.name.toUpperCase() : "FROM ANOTHER SESSION";
    case "task":
      return "BACKGROUND TASK";
    default:
      return String(t.from || "").toUpperCase();
  }
}

// clock is a time as the phone's 24-hour hh:mm, "" for none.
export function clock(iso) {
  const t = Date.parse(iso || "");
  if (Number.isNaN(t)) return "";
  return new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
}

function Prompt({ t }) {
  const [open, setOpen] = useState(false);
  const lines = (t.text || "").split("\n");
  const folded = lines.length > PROMPT_LINES && !open;
  const at = clock(t.at);
  return html`<div class="sc-box sc-prompt">
    <span class="sc-legend">${turnLabel(t)}${at ? " · " + at : ""}</span>
    <div class=${"sc-pre" + (t.command ? " sc-cmd" : "")}>${folded ? lines.slice(0, PROMPT_LINES).join("\n") : t.text}${t.cut && !folded ? "…" : ""}</div>
    ${folded ? html`<button class="sc-more" type="button" onClick=${() => setOpen(true)}>▸ ${lines.length - PROMPT_LINES} more lines</button>` : null}
  </div>`;
}

// Step is one row of a fold: its state's glyph, the tool, the summary, and
// for a pending step "running" (only in the newest turn while the session
// is Running) or "no result". A step that launched a subagent is a link to
// the subagent's screen, ending in "›", with ◐ while the agent has not
// finished.
function Step({ st, live, now, sessionId }) {
  const linked = Boolean(st.agent) && LINKABLE.test(st.agent);
  const running = live && st.state === "pending";
  let glyph = "";
  let cls = "sc-dim";
  if (running || (linked && !st.finished)) {
    glyph = "◐";
    cls = "sc-accent";
  } else if (st.state === "failed") {
    glyph = "×";
    cls = "sc-err";
  } else if (st.state === "interrupted") {
    glyph = "×";
  }
  let end = running ? "running " + ago(st.at, now) : st.state === "pending" ? "no result" : "";
  if (linked) end = end ? end + " ›" : "›";
  const cells = html`
    <span class=${"sc-step-glyph " + cls} aria-hidden="true">${glyph}</span>
    <span class="sc-step-tool">${st.tool}</span>
    <span class="sc-step-sum">${st.summary}</span>
    <span class=${"sc-step-end " + (running ? "sc-accent" : "sc-dim")}>${end}</span>
  `;
  return linked ? html`<a class="sc-step-link" href=${"#s/" + sessionId + "/agent/" + st.agent}>${cells}</a>` : cells;
}

function Steps({ it, live, open, now, sessionId }) {
  const total = it.total || 0;
  const failed = it.failed || 0;
  const steps = it.steps || [];
  const hidden = total - steps.length;
  return html`<details class="sc-steps" open=${open}>
    <summary>${total} ${total === 1 ? "step" : "steps"}${failed ? html`<span class="sc-err">${" · " + failed + " failed"}</span>` : null}</summary>
    ${hidden > 0 ? html`<p class="sc-dim sc-small">${hidden} earlier steps not shown</p>` : null}
    <div class="sc-step-grid">${steps.map((st, i) => html`<${Step} key=${i} st=${st} live=${live} now=${now} sessionId=${sessionId} />`)}</div>
  </details>`;
}

// Thinking is a run of Claude's thinking, folded: its first line in the
// summary, the whole text (plain, never markdown) in the body.
function Thinking({ it, open }) {
  const first = (it.text || "").split("\n").map((l) => l.trim()).find((l) => l !== "") || "";
  return html`<details class="sc-think" open=${open}>
    <summary>▸ thinking: ${first}</summary>
    <p class="sc-think-body">${it.text}${it.truncated ? "…" : ""}</p>
  </details>`;
}

function Item({ it, live, open, now, remote, sessionId }) {
  switch (it.kind) {
    case "text":
      return html`<div class="sc-said-text">
        <${Markdown} blocks=${it.blocks} />
        ${it.truncated
          ? html`<p class="sc-dim sc-small">… continues in ${remote ? html`<a href=${remote} target="_blank" rel="noopener">Claude</a>` : "Claude"}</p>`
          : null}
      </div>`;
    case "steps":
      return html`<${Steps} it=${it} live=${live} open=${open} now=${now} sessionId=${sessionId} />`;
    case "thinking":
      return html`<${Thinking} it=${it} open=${open} />`;
    case "compacted":
      return html`<div class="sc-compacted"><span>COMPACTED${it.at ? " " + clock(it.at) : ""}</span></div>`;
    case "note":
      return html`<p class=${"sc-note " + (it.error ? "sc-err" : "sc-dim")}>${it.error ? it.text : "× " + it.text}</p>`;
    default:
      return null;
  }
}

// Turn is one turn: its prompt's box, then Claude's side. live is set on the
// newest turn while the session is Running: its label says so, its last
// fold and its last thinking item start open, and its pending steps are
// running.
function Turn({ t, live, now, remote, sessionId }) {
  const items = t.items || [];
  let lastFold = -1;
  let lastThink = -1;
  items.forEach((it, i) => {
    if (it.kind === "steps") lastFold = i;
    if (it.kind === "thinking") lastThink = i;
  });
  const last = items.length > 0 ? items[items.length - 1] : null;
  // A turn whose only items are compaction rules has nothing of Claude's to
  // label, as after a /compact.
  const said = items.some((it) => it.kind !== "compacted");
  return html`<article class="sc-turn">
    ${t.partial ? html`<p class="sc-dim sc-small">Its start is earlier.</p>` : html`<${Prompt} t=${t} />`}
    ${last
      ? html`<div class="sc-said">
          ${said
            ? html`<div class="sc-said-label">CLAUDE · ${live ? html`<span aria-hidden="true">◐</span> WORKING` : clock(last.at)}</div>`
            : null}
          ${items.map((it, i) => html`<${Item} key=${i} it=${it} live=${live} open=${live && (i === lastFold || i === lastThink)} now=${now} remote=${remote} sessionId=${sessionId} />`)}
        </div>`
      : null}
  </article>`;
}

// TurnList draws turns oldest first. live is set while the newest turn's
// side is still being written (the session is Running); remote is the
// "Open in Claude" address a cut text item points to; sessionId is the
// session an Agent step's link belongs to.
export function TurnList({ turns, live, now, remote, sessionId }) {
  return html`<div class="sc-turn-list">
    ${turns.map((t, i) => html`<${Turn} key=${t.id} t=${t} live=${live && i === turns.length - 1} now=${now} remote=${remote} sessionId=${sessionId} />`)}
  </div>`;
}

// useTurnPages reads a session's turns, or a subagent's when agent is set:
// the newest page on open, an earlier page or a refresh of the newest turn on
// load("earlier") and load("refresh"), and a refresh every POLL ms while live
// is set. It returns the turns held ({conversation, turns, earlier, skipped,
// agent}; null until the first page lands, agent the page's subagent object
// or null) and the last error ("" for none). One request is in flight at a
// time; a refresh asked for meanwhile runs once it lands. The scroll is kept
// where it was, or moves to the bottom for a first page and for a refresh
// that lands while the reader is at the bottom.
export function useTurnPages({ id, agent, live }) {
  const [held, setHeld] = useState(null);
  const [err, setErr] = useState("");
  const heldRef = useRef(null);
  const loading = useRef(false);
  const again = useRef(false);
  const alive = useRef(true);
  // anchor says where the scroll goes after the next page lands: "bottom",
  // or {fromBottom} to keep the position an earlier page pushed down.
  const anchor = useRef(null);
  heldRef.current = held;

  // load fetches a page: "newest" (on open), "earlier" (the page before the
  // held turns) or "refresh" (from the newest turn held).
  async function load(kind) {
    if (id === undefined) return;
    if (loading.current) {
      if (kind === "refresh") again.current = true;
      return;
    }
    const cur = heldRef.current;
    let ask = kind;
    if (ask === "refresh" && (!cur || cur.turns.length === 0)) ask = "newest";
    if (ask === "earlier" && (!cur || !cur.earlier)) return;
    const q = new URLSearchParams();
    if (agent) q.set("agent", agent);
    if (cur && cur.conversation) q.set("conversation", cur.conversation);
    if (ask === "earlier") q.set("before", String(cur.earlier));
    if (ask === "refresh") q.set("after", String(cur.turns[cur.turns.length - 1].id));
    const doc = document.documentElement;
    const atBottom = window.innerHeight + window.scrollY >= doc.scrollHeight - 48;
    const fromBottom = doc.scrollHeight - window.scrollY;
    loading.current = true;
    let r;
    try {
      r = await call("GET", "/api/v1/sessions/" + id + "/turns" + (q.toString() ? "?" + q.toString() : ""));
    } finally {
      loading.current = false;
    }
    if (!alive.current) return;
    if (r.status !== 200 || !r.data) {
      setErr((r.data && r.data.error) || "Could not read the conversation.");
    } else {
      setErr("");
      const first = !cur || r.data.reset || ask === "newest";
      anchor.current = first ? "bottom" : ask === "earlier" ? { fromBottom } : atBottom ? "bottom" : null;
      setHeld({ ...mergeTurns(cur, r.data, ask), agent: r.data.agent || null });
    }
    if (again.current) {
      again.current = false;
      load("refresh");
    }
  }

  useLayoutEffect(() => {
    const a = anchor.current;
    anchor.current = null;
    if (!a) return;
    const doc = document.documentElement;
    window.scrollTo(0, a === "bottom" ? doc.scrollHeight : doc.scrollHeight - a.fromBottom);
  }, [held]);

  useEffect(() => {
    alive.current = true;
    load("newest");
    return () => {
      alive.current = false;
    };
  }, [id, agent]);

  useEffect(() => {
    if (!live) return undefined;
    const timer = setInterval(() => load("refresh"), POLL);
    return () => clearInterval(timer);
  }, [live, id, agent]);

  return { held, err, load };
}

// Conversation is a Claude session's turns, the needs-you box while it waits,
// and the reply box.
function Conversation({ snap, stale, route, act, toast }) {
  const id = route.id;
  const s = sessionOf(snap, id);
  const { busy, run } = useRun(act, toast, id);
  const working = Boolean(s && s.status === "Running");
  const { held, err, load } = useTurnPages({ id, live: working });

  // The time this phone last opened the session feeds the fleet's NEW mark;
  // it is written on arrival and again on leaving, so a turn that ended while
  // the session was open does not light it. The conversation is where a fleet
  // row and a push land.
  useEffect(() => {
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  // The stream changing the session's status or last message refreshes the
  // newest turn; so does the poll while the session is Running.
  const sig = s ? s.status + "\n" + (s.last_message || "") : "";
  useEffect(() => {
    if (held) load("refresh");
  }, [sig]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  const now = Date.now();
  const turns = held ? held.turns : [];

  return html`
    <section class="sc-screen sc-turns">
      ${held && held.skipped ? html`<p class="sc-dim sc-small">Some records could not be read.</p>` : null}
      ${held && held.earlier ? html`<button class="sc-earlier" type="button" onClick=${() => load("earlier")}>↑ earlier turns</button>` : null}
      ${!held
        ? html`<div class="sc-empty">
            <p class=${err ? "sc-err" : "sc-dim"}>${err || "Loading…"}</p>
            ${err ? html`<button class="sc-btn" type="button" onClick=${() => load("newest")}>retry</button>` : null}
          </div>`
        : null}
      ${held && turns.length === 0 ? html`<p class="sc-dim sc-empty">No turns yet.</p>` : null}
      <${TurnList} turns=${turns} live=${working} now=${now} remote=${s.remote_url} sessionId=${s.id} />
      ${held && err
        ? html`<p class="sc-dim sc-small">Could not refresh: ${err} · <button class="sc-inline-btn" type="button" onClick=${() => load("refresh")}>retry</button></p>`
        : null}
      ${s.group === "needs_you" ? html`<${NeedsYou} s=${s} off=${off} run=${run} />` : null}
      ${can.send
        ? html`<${ReplyBox} snap=${snap} off=${off} run=${run} id=${s.id} />`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}
    </section>
  `;
}

// Turns is the screen at #s/<id>: the conversation of a Claude session, and
// for any other program (which has no transcript to read) the info tab.
export function Turns(props) {
  const s = sessionOf(props.snap, props.route.id);
  if (s && !s.claude) return html`<${Info} ...${props} />`;
  return html`<${Conversation} ...${props} />`;
}
```

In `internal/web/static/app.css`, after the line

```css
.sc-note { margin: 0; font-size: 12px; }
```

add the thinking fold and the agent link rows:

```css
/* A thinking item: folded to its first line, dim and italic; the body is
   the text as written. */
.sc-think { font-size: 12px; }
.sc-think > summary {
  display: block;
  min-height: 44px;
  margin: -8px 0;
  overflow: hidden;
  line-height: 44px;
  white-space: nowrap;
  text-overflow: ellipsis;
  color: var(--dim);
  font-style: italic;
  cursor: pointer;
  list-style: none;
}
.sc-think > summary::-webkit-details-marker { display: none; }
.sc-think-body { margin: 0 0 4px; padding-left: 12px; border-left: 1px solid var(--rule); color: var(--dim); white-space: pre-wrap; overflow-wrap: anywhere; }
/* A step that launched a subagent is a link row across the fold's grid
   (subgrid keeps its cells in the grid's columns; without it the anchor
   dissolves into them). */
.sc-step-link { display: grid; grid-column: 1 / -1; grid-template-columns: subgrid; padding: 6px 0; color: inherit; text-decoration: none; }
@supports not (grid-template-columns: subgrid) {
  .sc-step-link { display: contents; }
}
.sc-step-link .sc-step-tool,
.sc-step-link .sc-step-sum,
.sc-step-link .sc-step-end { color: var(--accent); }
```

- [ ] **Step 2: Run the checks**

Run: `bash -c 'for f in internal/web/static/js/*.mjs; do node --check "$f" || exit 1; done; CGO_ENABLED=0 go test ./internal/web/ -run TestStatic'`
Expected: no output from `node --check`, then `ok  	github.com/aidan-bailey/loom/internal/web`. (`TestStatic_NamedImportsAreExported` holds `turns.mjs`'s imports of `Info`, `actionsFor`, `NeedsYou`, `markSeen` and `ago` to real exports; `TestStatic_TheStylesheetHasNoFenceAndItsBracesBalance` holds the `@supports` block's braces.)

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(web): the conversation shows thinking, links its subagents and ends in the needs-you box" -- internal/web/static/js/turns.mjs internal/web/static/app.css
```

### Task 3.4: Webshot: the conversation, waiting and info screens

**Files:**
- Modify: `tools/webshot/main.go:8-9,39-53`
- Modify: `tools/webshot/webshot_test.go` (`TestPickScreens`; two new tests)
- Modify: `tools/webshot/fixture/turns.json`
- Modify: `tools/webshot/fixture/snapshot.json:50-58`

- [ ] **Step 1: Write the failing tests**

In `tools/webshot/webshot_test.go`, replace `TestPickScreens`:

```go
func TestPickScreens(t *testing.T) {
	all, err := pickScreens("")
	require.NoError(t, err)
	assert.Equal(t, screens, all)
	two, err := pickScreens("turns, fleet")
	require.NoError(t, err)
	assert.Equal(t, []screen{{"turns", "#s/1/turns"}, {"fleet", ""}}, two)
	_, err = pickScreens("fleet,nope")
	assert.Error(t, err)
}
```

with:

```go
func TestPickScreens(t *testing.T) {
	all, err := pickScreens("")
	require.NoError(t, err)
	assert.Equal(t, screens, all)
	two, err := pickScreens("conversation, fleet")
	require.NoError(t, err)
	assert.Equal(t, []screen{{"conversation", "#s/1"}, {"fleet", ""}}, two)
	for _, retired := range []string{"session", "turns"} {
		_, err = pickScreens(retired)
		assert.Error(t, err, "%s: a session opens on its conversation, and its old screens are gone", retired)
	}
	_, err = pickScreens("fleet,nope")
	assert.Error(t, err)
}
```

After `TestFixture_TheLastMessageIsTheTreesText` add:

```go
func TestFixture_TheTurnsHoldAnAgentStepAndAThinkingItem(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("turns.json")
	require.NoError(t, err)
	var page struct {
		Turns []struct {
			Items []struct {
				Kind  string `json:"kind"`
				Text  string `json:"text"`
				Steps []struct {
					Agent    string `json:"agent"`
					Finished bool   `json:"finished"`
				} `json:"steps"`
			} `json:"items"`
		} `json:"turns"`
	}
	require.NoError(t, json.Unmarshal(raw, &page))
	finished := map[string]bool{}
	thinking := ""
	for _, turn := range page.Turns {
		for _, it := range turn.Items {
			if it.Kind == "thinking" {
				thinking = it.Text
			}
			for _, st := range it.Steps {
				if st.Agent != "" {
					finished[st.Agent] = st.Finished
				}
			}
		}
	}
	require.Contains(t, finished, "a1f00dcafe0001", "the subagent the agent screen shows")
	assert.False(t, finished["a1f00dcafe0001"], "it is still running: its link keeps the ◐")
	require.Contains(t, finished, "a1f00dcafe0002")
	assert.True(t, finished["a1f00dcafe0002"], "a finished one draws no ◐")
	assert.NotEmpty(t, thinking, "a thinking item with text")
}

func TestFixture_SessionOnesAgentsCarryTheIdsTheTurnsLinkTo(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var snap struct {
		Sessions map[string][]struct {
			ID     int `json:"id"`
			Agents []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"agents"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(raw, &snap))
	for _, s := range snap.Sessions["1"] {
		if s.ID != 1 {
			continue
		}
		require.Len(t, s.Agents, 2)
		assert.Equal(t, "a1f00dcafe0001", s.Agents[0].ID, "recon, the running one")
		assert.Equal(t, "a1f00dcafe0002", s.Agents[1].ID, "verifier, the idle one")
		return
	}
	t.Fatal("no session 1")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./tools/webshot/ -run 'TestPickScreens|TestFixture_'`
Expected: FAIL. `TestPickScreens` reports `no screen "conversation" (have fleet,session,waiting,turns,diff,mockup,mockup-full,new,settings)`; `TestFixture_TheTurnsHoldAnAgentStepAndAThinkingItem` fails on `Should contain "a1f00dcafe0001"`; `TestFixture_SessionOnesAgentsCarryTheIdsTheTurnsLinkTo` fails on `"" != "a1f00dcafe0001"`.

- [ ] **Step 3: Name the screens**

In `tools/webshot/main.go`, replace the usage line

```go
//	go run ./tools/webshot -out /tmp/shots -screens fleet,turns -guides
```

with

```go
//	go run ./tools/webshot -out /tmp/shots -screens fleet,conversation -guides
```

Replace the comment and the `screens` list:

```go
// screens are the routes webshot knows, in the order it renders them. The
// session screens are of session 1 (working, with a PR, agents and a diff),
// session 2 (waiting on a permission) and session 7 (your turn, with a
// fresh brainstorm mockup).
var screens = []screen{
	{"fleet", ""},
	{"session", "#s/1"},
	{"waiting", "#s/2"},
	{"turns", "#s/1/turns"},
	{"diff", "#s/1/diff"},
```

with:

```go
// screens are the routes webshot knows, in the order it renders them. The
// session screens are of session 1 (working, with a PR, agents and a diff),
// session 2 (waiting on a permission: its conversation ends in the needs-you
// box) and session 7 (your turn, with a fresh brainstorm mockup). A session
// opens on its conversation; "info" is the tab that holds its tiles, agents
// and actions.
var screens = []screen{
	{"fleet", ""},
	{"conversation", "#s/1"},
	{"waiting", "#s/2"},
	{"info", "#s/1/info"},
	{"diff", "#s/1/diff"},
```

(`{"mockup", …}` and the rest are unchanged.)

- [ ] **Step 4: Give the fixtures an Agent step, a thinking item and agent ids**

In `tools/webshot/fixture/snapshot.json`, replace

```json
            "name": "recon",
```

with

```json
            "id": "a1f00dcafe0001",
            "name": "recon",
```

and replace

```json
            "name": "verifier",
```

with

```json
            "id": "a1f00dcafe0002",
            "name": "verifier",
```

(keep each line's indentation; the new `"id"` line sits at the `"name"` line's indentation.)

In `tools/webshot/fixture/turns.json`, turn 1: replace

```json
          "total": 6,
```

with

```json
          "total": 7,
```

and replace the end of its steps

```json
              "at": "now-24m",
              "state": "done"
            }
          ]
```

with

```json
              "at": "now-24m",
              "state": "done"
            },
            {
              "tool": "Agent",
              "summary": "Review the draw order",
              "at": "now-23m",
              "state": "done",
              "agent": "a1f00dcafe0002",
              "finished": true
            }
          ]
```

Turn 3: replace the opening of its steps item

```json
        {
          "kind": "steps",
          "at": "now-4m",
          "total": 2,
```

with a thinking item before it, and the new total:

```json
        {
          "kind": "thinking",
          "at": "now-4m",
          "text": "The ui tests may be slow, so start with TestPane\nand run the whole package after.\nIf the header test fails, check the redraw order first.",
          "truncated": false
        },
        {
          "kind": "steps",
          "at": "now-4m",
          "total": 3,
```

and replace

```json
              "summary": "go test ./ui/... -run TestPane",
              "at": "now-4m",
              "state": "done"
            },
```

with the step plus a background Agent step (launched, so its step settled while the agent runs):

```json
              "summary": "go test ./ui/... -run TestPane",
              "at": "now-4m",
              "state": "done"
            },
            {
              "tool": "Agent",
              "summary": "Map every caller of the cursor path",
              "at": "now-3m",
              "state": "done",
              "agent": "a1f00dcafe0001",
              "finished": false
            },
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/`
Expected: `ok  	github.com/aidan-bailey/loom/tools/webshot` and `ok  	github.com/aidan-bailey/loom/internal/web`. (`TestWebshotFixture_IsTheJSONTheLayerServes` decodes both fixtures with unknown fields refused, so it fails if Story 2's `agent`, `finished` and agent `id` fields are not there.)

- [ ] **Step 6: Render the screens and look at them**

Run: `go run ./tools/webshot -out /tmp/loom-shots-s3 -screens conversation,waiting,info,fleet`
Expected: four lines `conversation	/tmp/loom-shots-s3/conversation.png` … and no `!` line (no console error, no unanswered route). Read the PNGs: `conversation` shows the tabs conversation · changes · info, the strip (`◐ working 2m · +212 −40 · PR #482 … · 62%`) under them, the newest turn with an open `▸ thinking: The ui tests may be slow, so start with TestPane`, the open fold with the `Agent` row in accent with `◐` and `›`, and the reply box; `waiting` ends in the needs-you box above the reply box and its header has no `claude ›` link; `info` shows the tiles, the Agents section and the actions with no last message and no needs-you box.

- [ ] **Step 7: Commit**

```bash
git commit -m "test(web): webshot screens for the conversation, waiting and info tabs" -- tools/webshot/main.go tools/webshot/webshot_test.go tools/webshot/fixture/turns.json tools/webshot/fixture/snapshot.json
```

## Story 4: The subagent screen

- **Developer:** intermediate-dev
- **Files:** `internal/web/static/js/agent.mjs`, `internal/web/static/js/strip.mjs`, `internal/web/static/js/turns.mjs`, `internal/web/static/js/session.mjs`, `internal/web/static/js/screens.mjs`, `internal/web/static/app.css`, `tools/webshot/main.go`, `tools/webshot/canned.go`, `tools/webshot/webshot_test.go`, `tools/webshot/fixture/agent-turns.json`, `tools/webshot/fixture/message.json` (deleted), `tools/CLAUDE.md`, `internal/web/webshot_fixture_test.go`, `internal/web/static/js/markdown.mjs`, `internal/web/README.md`, `internal/web/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md`
- **Interfaces:** uses Story 3's `useTurnPages`, `TurnList`, `parseRoute`'s `agent` route and Story 2's `agent` page object; exposes `Agent`, `AgentHead`, `AgentStrip`
- **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && go run ./tools/webshot -out /tmp/loom-shots-s4 -screens conversation,agent,info'`
- **Review:** deep

`#s/<id>/agent/<agentId>` gets its screen. `Agent` reads the subagent's page with Story 3's `useTurnPages` (its `agent` argument sets `?agent=`) and draws it with `TurnList`: the brief is the first turn (its box reads BRIEF through `turnLabel`'s default branch), there is no reply box and no needs-you box. The header cannot see the screen's page, so the screen publishes the page's `agent` object to a small module-level store in `js/agent.mjs` that `AgentHead` watches, and `AgentHead` draws the `‹ conversation` way back, the agent's description as the title, the session's tabs with conversation current and an `AgentStrip` (running with its age, finished or idle, or unknown, then the type, and `● session needs you ›` while the session waits). The screen refreshes every 5 s while the agent runs (or its state is unknown and the session is Running, or its file is not written yet), once more when the state turns finished, shows "Starting…" while the file is missing and "This subagent's record is gone." when it is missing and finished or has been missing for 30 s. Info's Agents rows link to the screen when a row carries an `id`. The conversation keeps its held turns and scroll position per session in a module-level map, so back from a subagent (or a tab switch) returns to the same place and then refreshes. Webshot gains the `agent` screen, an `agent-turns.json` fixture and a canned route that answers a turns request naming an agent from it; The last message route stays but no screen draws it now (spec decision 18), so webshot's side of it retires: `fixture/message.json`, its canned route and `TestFixture_TheLastMessageIsTheTreesText` go, and the webshot rule in `tools/CLAUDE.md` loses its `last_message` clause. `internal/web/README.md`, `internal/web/CLAUDE.md`, `docs/claude/web-layer.md`, `js/markdown.mjs`'s header and `USAGE.md` document the conversation-first screens and the undrawn route. As in Story 3 the checks are `node --check`, the static Go tests and the webshot run. No code here holds a landmine marker.

### Task 4.1: The subagent strip

**Files:**
- Modify: `internal/web/static/js/strip.mjs`
- Modify: `internal/web/static/app.css`

- [ ] **Step 1: Add `AgentStrip` to `strip.mjs`**

Append to `internal/web/static/js/strip.mjs`, after `FactStrip` (the file's imports, `html`, `ago`, `PrMark`, `glyphOf` and `rowShare`, already cover it):

```js

// AgentStrip is the strip under the tabs of a subagent's screen, by the
// page's agent.state: running (with its age since agent.started), finished
// (idle for a teammate, which stays up for more work) or unknown, then the
// agent's type. While the session waits on you it ends in a link back to the
// conversation, where the needs-you box is. It is not itself a link, so the
// link to the session nests in no other.
export function AgentStrip({ s, agent, now }) {
  let lead;
  if (agent.state === "running") {
    lead = html`<span class="sc-accent">${["◐ subagent running", ago(agent.started, now)].filter(Boolean).join(" ")}</span>`;
  } else if (agent.state === "finished") {
    lead = html`<span class="sc-dim">${agent.teammate ? "○ idle" : "✓ finished"}</span>`;
  } else {
    lead = html`<span class="sc-dim">subagent</span>`;
  }
  return html`<div class="sc-strip sc-strip-agent">
    <span class="sc-strip-main">${lead}${agent.type ? " · " + agent.type : ""}</span>
    ${s.group === "needs_you" ? html`<a class="sc-strip-needs" href=${"#s/" + s.id}>● session needs you ›</a>` : null}
  </div>`;
}
```

- [ ] **Step 2: Style it**

In `internal/web/static/app.css`, after the rule

```css
.sc-strip .sc-hot { color: var(--attention); }
```

add:

```css
/* The subagent's strip: its state on the left, ellipsised, and the way back
   to a waiting session pinned on the right. */
.sc-strip-agent { display: flex; align-items: baseline; gap: 12px; }
.sc-strip-main { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sc-strip-needs { flex: none; color: var(--attention); text-decoration: none; }
```

- [ ] **Step 3: Run the checks**

Run: `bash -c 'for f in internal/web/static/js/*.mjs; do node --check "$f" || exit 1; done; CGO_ENABLED=0 go test ./internal/web/ -run TestStatic'`
Expected: no output from `node --check`, then `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(web): a strip for the subagent screen" -- internal/web/static/js/strip.mjs internal/web/static/app.css
```

### Task 4.2: The conversation keeps its turns and scroll

**Files:**
- Modify: `internal/web/static/js/turns.mjs`

- [ ] **Step 1: Keep the turns and the scroll per session**

In `internal/web/static/js/turns.mjs` (as Story 3 leaves it), after

```js
const LINKABLE = /^[A-Za-z0-9_-]+$/;
```

add:

```js

// kept holds each session's conversation as it was left, by session id: the
// turns held and the scroll position. Leaving the screen (a subagent's, the
// info tab) and coming back restores both before the first paint and then
// refreshes, instead of loading at the bottom. Like the reply drafts it
// lasts as long as the page.
const kept = new Map();
```

In `useTurnPages`, replace

```js
  const [held, setHeld] = useState(null);
```

with

```js
  const [held, setHeld] = useState(() => (agent ? null : (kept.get(id) || {}).held || null));
```

Replace the start of the anchoring effect

```js
  useLayoutEffect(() => {
    const a = anchor.current;
```

with the restore, then the same start:

```js
  // A conversation that was kept is scrolled back to where it was left,
  // before the first paint; a subagent's page is not kept.
  useLayoutEffect(() => {
    const k = agent ? undefined : kept.get(id);
    if (k && k.scrollY !== undefined && heldRef.current) window.scrollTo(0, k.scrollY);
  }, [id, agent]);

  useEffect(() => {
    if (agent || !held) return;
    kept.set(id, { ...(kept.get(id) || {}), held });
  }, [held]);

  useEffect(() => {
    if (agent || id === undefined) return undefined;
    const onScroll = () => kept.set(id, { ...(kept.get(id) || {}), scrollY: window.scrollY });
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, [id, agent]);

  useLayoutEffect(() => {
    const a = anchor.current;
```

Replace the opening effect

```js
  useEffect(() => {
    alive.current = true;
    load("newest");
    return () => {
      alive.current = false;
    };
  }, [id, agent]);
```

with

```js
  useEffect(() => {
    alive.current = true;
    // Turns kept from an earlier visit are refreshed from their newest turn;
    // anything else opens at the newest page.
    load(heldRef.current ? "refresh" : "newest");
    return () => {
      alive.current = false;
    };
  }, [id, agent]);
```

In `Conversation`, a kept conversation already refreshes on open, so the stream effect must not refresh a second time on mount. Replace

```js
  const sig = s ? s.status + "\n" + (s.last_message || "") : "";
  useEffect(() => {
    if (held) load("refresh");
  }, [sig]);
```

with

```js
  const sig = s ? s.status + "\n" + (s.last_message || "") : "";
  const sigSeen = useRef(sig);
  useEffect(() => {
    if (sigSeen.current === sig) return;
    sigSeen.current = sig;
    if (held) load("refresh");
  }, [sig]);
```

- [ ] **Step 2: Run the checks**

Run: `bash -c 'for f in internal/web/static/js/*.mjs; do node --check "$f" || exit 1; done; CGO_ENABLED=0 go test ./internal/web/ -run TestStatic'`
Expected: no output from `node --check`, then `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(web): the conversation keeps its turns and scroll for the way back" -- internal/web/static/js/turns.mjs
```

### Task 4.3: The subagent screen, and info's links to it

**Files:**
- Create: `internal/web/static/js/agent.mjs`
- Modify: `internal/web/static/js/screens.mjs`
- Modify: `internal/web/static/js/session.mjs`
- Modify: `internal/web/static/app.css`

- [ ] **Step 1: Register the screen**

In `internal/web/static/js/screens.mjs` (as Story 3 leaves it), replace

```js
import { Mockup } from "./mockup.mjs";
```

with

```js
import { Mockup } from "./mockup.mjs";
import { Agent, AgentHead } from "./agent.mjs";
```

and replace

```js
  mockup: { Screen: Mockup, Head: SessionHead },
};
```

with

```js
  mockup: { Screen: Mockup, Head: SessionHead },
  agent: { Screen: Agent, Head: AgentHead },
};
```

- [ ] **Step 2: Run the static tests to see them fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TestStatic_EveryReferenceResolves'`
Expected: FAIL, `/js/screens.mjs imports ./agent.mjs, which is not embedded`.

- [ ] **Step 3: Write the screen**

Create `internal/web/static/js/agent.mjs`:

```js
// The subagent screen (#s/<id>/agent/<agentId>): one subagent's turns from
// its own transcript (GET …/turns?agent=), drawn by the conversation's
// renderer. Its brief is the first turn; there is no reply box and no
// needs-you box. The header (AgentHead) draws the agent's description and a
// strip by its state, which the screen only learns with its page, so the
// screen publishes the page's agent object to a store the header watches.
import { html, useState, useEffect, useRef } from "../lib/preact-htm.mjs";
import { sessionOf } from "./fleet.mjs";
import { ContextRow, FLEET_BACK, SessionTabs } from "./chrome.mjs";
import { AgentStrip } from "./strip.mjs";
import { actionsFor } from "./session.mjs";
import { TurnList, useTurnPages } from "./turns.mjs";

// GONE_AFTER is how long an agent's file may stay unwritten before the screen
// says its record is gone.
const GONE_AFTER = 30000;

// previousHash is the hash the app was at before the current one, from the
// hashchange events: when it is this session's conversation, the way back
// is history.back(), which returns to the conversation's scroll position.
let previousHash = "";
window.addEventListener("hashchange", (e) => {
  previousHash = new URL(e.oldURL).hash;
});

// infos are the agent objects the screens have published, by session and
// agent, and watchers the headers to redraw when one changes.
const infos = new Map();
const watchers = new Set();

function publish(key, agent) {
  if (agent) infos.set(key, agent);
  else infos.delete(key);
  watchers.forEach((f) => f());
}

function useInfo(key) {
  const [, tick] = useState(0);
  useEffect(() => {
    const f = () => tick((n) => n + 1);
    watchers.add(f);
    return () => watchers.delete(f);
  }, []);
  return infos.get(key) || null;
}

function keyOf(route) {
  return route.id + "/" + route.agent;
}

// AgentHead is the subagent screen's context row, tabs (conversation
// current) and strip. Its way back says "conversation": a click goes back in
// history when the previous entry is this session's conversation, and
// otherwise follows the link to it.
export function AgentHead({ snap, route, pending }) {
  const s = sessionOf(snap, route.id);
  const info = useInfo(keyOf(route));
  if (!s) return html`<${ContextRow} back=${FLEET_BACK} title=${pending === route.id ? "Loading…" : ""} />`;
  const back = {
    href: "#s/" + s.id,
    label: "conversation",
    onClick: (e) => {
      if (previousHash === "#s/" + s.id) {
        e.preventDefault();
        history.back();
      }
    },
  };
  const title = (info && (info.description || info.type)) || "subagent";
  return html`
    <${ContextRow} back=${back} title=${title} />
    <${SessionTabs} s=${s} current="turns" conversation=${Boolean(s.claude)} changes=${actionsFor(s).diff} />
    ${info ? html`<${AgentStrip} s=${s} agent=${info} now=${Date.now()} />` : null}
  `;
}

export function Agent({ snap, route }) {
  const id = route.id;
  const key = keyOf(route);
  const s = sessionOf(snap, id);
  const working = Boolean(s && s.status === "Running");
  const [live, setLive] = useState(false);
  const { held, err, load } = useTurnPages({ id, agent: route.agent, live });
  const info = held ? held.agent : null;
  const state = info ? info.state : "";
  const missingSince = useRef(0);
  const lastState = useRef("");

  // The header draws its strip from the page's agent object.
  useEffect(() => {
    publish(key, info);
  }, [held]);
  useEffect(() => () => publish(key, null), [key]);

  // missingSince is when a page first said the agent's file is not there.
  useEffect(() => {
    if (info && info.missing) {
      if (!missingSince.current) missingSince.current = Date.now();
    } else {
      missingSince.current = 0;
    }
  }, [held]);

  // The agent's file is missing: not yet written (meta lands first) until
  // the state says finished, or until it has been missing for GONE_AFTER.
  const missing = Boolean(info && info.missing);
  const waited = missing && missingSince.current ? Date.now() - missingSince.current : 0;
  const gone = missing && (state === "finished" || waited > GONE_AFTER);
  const starting = missing && !gone;
  // The agent is working: running, or its state unknown while the session is.
  const running = state === "running" || (state === "unknown" && working);
  const want = running || starting;
  useEffect(() => {
    setLive(want);
  }, [want]);

  // Once more when the state turns finished: the page that said so may have
  // been read before the agent's last records.
  useEffect(() => {
    if (lastState.current && lastState.current !== "finished" && state === "finished") load("refresh");
    lastState.current = state;
  }, [state]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  function retry() {
    missingSince.current = 0;
    load("newest");
  }

  let body;
  if (!held) {
    body = html`<div class="sc-empty">
      <p class=${err ? "sc-err" : "sc-dim"}>${err || "Loading…"}</p>
      ${err ? html`<button class="sc-btn" type="button" onClick=${() => load("newest")}>retry</button>` : null}
    </div>`;
  } else if (gone) {
    body = html`<div class="sc-empty">
      <p class="sc-dim">This subagent's record is gone.</p>
      <button class="sc-btn" type="button" onClick=${retry}>retry</button>
      <p class="sc-small"><a href=${"#s/" + s.id}>‹ conversation</a></p>
    </div>`;
  } else if (starting) {
    body = html`<p class="sc-dim sc-empty">Starting…</p>`;
  } else {
    body = html`
      ${held.skipped ? html`<p class="sc-dim sc-small">Some records could not be read.</p>` : null}
      ${held.earlier ? html`<button class="sc-earlier" type="button" onClick=${() => load("earlier")}>↑ earlier turns</button>` : null}
      ${held.turns.length === 0 ? html`<p class="sc-dim sc-empty">No turns yet.</p>` : null}
      <${TurnList} turns=${held.turns} live=${running} now=${Date.now()} remote="" sessionId=${s.id} />
      ${err
        ? html`<p class="sc-dim sc-small">Could not refresh: ${err} · <button class="sc-inline-btn" type="button" onClick=${() => load("refresh")}>retry</button></p>`
        : null}
    `;
  }

  return html`<section class="sc-screen sc-subagent">${body}</section>`;
}
```

- [ ] **Step 4: Link info's Agents rows**

In `internal/web/static/js/session.mjs` (as Story 3 leaves it), replace the `Agents` component and its comment

```js
// Agents are the session's subagents by name, each running or idle, with its
// description under it.
function Agents({ agents }) {
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Agents ${agents.length}</h2>
      ${agents.map(
        (a, i) => html`<div key=${i} class="sc-agent">
          <div class="sc-row-top">
            <span class="sc-glyph" aria-hidden="true">${a.idle ? "○" : "◐"}</span>
            <span class="sc-row-title">${a.name}</span>
            <span class="sc-age">${a.idle ? "idle" : "running"}</span>
          </div>
          ${a.description ? html`<div class="sc-agent-desc">${a.description}</div>` : null}
        </div>`,
      )}
    </section>
  `;
}
```

with:

```js
// AGENT_ID is what an agent id may hold for the app to link to its screen
// (parseRoute's agent route).
const AGENT_ID = /^[A-Za-z0-9_-]+$/;

// Agents are the session's subagents by name, each running or idle, with its
// description under it. A row that carries an id is a link to the agent's
// screen.
function Agents({ agents, sessionId }) {
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Agents ${agents.length}</h2>
      ${agents.map((a, i) => {
        const linked = Boolean(a.id) && AGENT_ID.test(a.id);
        const inner = html`
          <div class="sc-row-top">
            <span class="sc-glyph" aria-hidden="true">${a.idle ? "○" : "◐"}</span>
            <span class="sc-row-title">${a.name}</span>
            <span class="sc-age">${a.idle ? "idle" : "running"}</span>
            ${linked ? html`<${ChevronIcon} />` : null}
          </div>
          ${a.description ? html`<div class="sc-agent-desc">${a.description}</div>` : null}
        `;
        return linked
          ? html`<a key=${i} class="sc-agent sc-agent-link" href=${"#s/" + sessionId + "/agent/" + a.id}>${inner}</a>`
          : html`<div key=${i} class="sc-agent">${inner}</div>`;
      })}
    </section>
  `;
}
```

and replace the component's use in `Info`

```js
      ${s.agents && s.agents.length > 0 ? html`<${Agents} agents=${s.agents} />` : null}
```

with

```js
      ${s.agents && s.agents.length > 0 ? html`<${Agents} agents=${s.agents} sessionId=${s.id} />` : null}
```

- [ ] **Step 5: Style the linked rows**

In `internal/web/static/app.css`, after the rule

```css
.sc-agent-desc { padding-left: 16px; font-size: 12px; color: var(--dim); }
```

add:

```css
.sc-agent-link { display: block; min-height: 44px; color: inherit; text-decoration: none; }
.sc-agent-link svg { align-self: center; color: var(--accent); }
```

- [ ] **Step 6: Run the checks to see them pass**

Run: `bash -c 'for f in internal/web/static/js/*.mjs; do node --check "$f" || exit 1; done; CGO_ENABLED=0 go test ./internal/web/ -run TestStatic'`
Expected: no output from `node --check`, then `ok  	github.com/aidan-bailey/loom/internal/web`.

- [ ] **Step 7: Commit**

```bash
git add -N internal/web/static/js/agent.mjs
git commit -m "feat(web): a screen for a subagent's turns, linked from the conversation and info" -- internal/web/static/js/agent.mjs internal/web/static/js/screens.mjs internal/web/static/js/session.mjs internal/web/static/app.css
```

### Task 4.4: Webshot: the agent screen

**Files:**
- Modify: `tools/webshot/main.go`
- Modify: `tools/webshot/canned.go:76-114`
- Create: `tools/webshot/fixture/agent-turns.json`
- Delete: `tools/webshot/fixture/message.json`
- Modify: `tools/CLAUDE.md:18`
- Test: `tools/webshot/webshot_test.go`
- Test: `internal/web/webshot_fixture_test.go`

- [ ] **Step 1: Write the failing tests**

In `tools/webshot/webshot_test.go`, after `TestPickScreens` add:

```go
func TestPickScreens_TheAgentScreenIsSessionOnesRunningSubagent(t *testing.T) {
	got, err := pickScreens("agent")
	require.NoError(t, err)
	assert.Equal(t, []screen{{"agent", "#s/1/agent/a1f00dcafe0001"}}, got)
}
```

After `TestCanned_AnswersTheRoutesTheScreensReadAndNamesTheRest` add:

```go
func TestCanned_ASubagentsTurnsAreTheAgentFixture(t *testing.T) {
	c, srv := cannedForTest(t)
	_, plain := get(t, srv.URL+"/api/v1/sessions/1/turns")
	var conversation map[string]any
	require.NoError(t, json.Unmarshal([]byte(plain), &conversation))
	assert.NotContains(t, conversation, "agent", "the conversation's page names no subagent")

	code, body := get(t, srv.URL+"/api/v1/sessions/1/turns?agent=a1f00dcafe0001")
	assert.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, `"now`, "every relative time is filled")
	var page struct {
		Agent struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Missing bool   `json:"missing"`
		} `json:"agent"`
		Turns []struct {
			From string `json:"from"`
			Text string `json:"text"`
		} `json:"turns"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &page))
	assert.Equal(t, "a1f00dcafe0001", page.Agent.ID)
	assert.Equal(t, "running", page.Agent.State)
	assert.False(t, page.Agent.Missing)
	require.NotEmpty(t, page.Turns)
	assert.Equal(t, "brief", page.Turns[0].From, "a subagent's page opens with its brief")
	assert.Greater(t, strings.Count(page.Turns[0].Text, "\n"), 12, "long enough to fold after 12 lines")
	assert.Empty(t, c.takeMisses())
}
```

In `internal/web/webshot_fixture_test.go`, replace

```go
		"message.json":         &messageJSON{},
		"turns.json":           &turnsJSON{},
```

with

```go
		"turns.json":           &turnsJSON{},
		"agent-turns.json":     &turnsJSON{},
```

(`message.json` leaves the map because Step 3 deletes the fixture: no screen reads the last message route now. `agent-turns.json` replaces it in the directory's count, so `require.Len(t, entries, 6, "a new fixture is checked here too")` stays as it is.)

In `tools/webshot/webshot_test.go`, in `TestCanned_AnswersTheRoutesTheScreensReadAndNamesTheRest`, replace

```go
	for _, p := range []string{"/api/v1/sessions/1/message", "/api/v1/sessions/1/turns", "/api/v1/sessions/2/diff", "/api/v1/launch-defaults?workspace=loom"} {
```

with

```go
	for _, p := range []string{"/api/v1/sessions/1/turns", "/api/v1/sessions/2/diff", "/api/v1/launch-defaults?workspace=loom"} {
```

and replace the end of that test

```go
	code, _ = get(t, srv.URL+"/api/v1/push-key")
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, []string{"GET /api/v1/push-key"}, c.takeMisses())
}
```

with

```go
	code, _ = get(t, srv.URL+"/api/v1/push-key")
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, []string{"GET /api/v1/push-key"}, c.takeMisses())
	code, _ = get(t, srv.URL+"/api/v1/sessions/1/message")
	assert.Equal(t, http.StatusNotFound, code, "no screen draws the last message, so the canned loom has no answer for it")
	assert.Equal(t, []string{"GET /api/v1/sessions/1/message"}, c.takeMisses())
}
```

Delete `TestFixture_TheLastMessageIsTheTreesText` from `tools/webshot/webshot_test.go`: the whole function, from its `func` line through its closing brace, which is

```go
func TestFixture_TheLastMessageIsTheTreesText(t *testing.T) {
	c := &cannedLoom{now: time.Now}
	raw, err := c.fixture("snapshot.json")
	require.NoError(t, err)
	var snap struct {
		Sessions map[string][]struct {
			ID          int    `json:"id"`
			LastMessage string `json:"last_message"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(raw, &snap))
	raw, err = c.fixture("message.json")
	require.NoError(t, err)
	var msg struct{ Text string }
	require.NoError(t, json.Unmarshal(raw, &msg))
	for _, s := range snap.Sessions["1"] {
		if s.ID == 1 {
			assert.Equal(t, msg.Text, s.LastMessage, "the app draws the tree only when its text is the session's last message")
			return
		}
	}
	t.Fatal("no session 1")
}
```

(it pins `snapshot.json`'s `last_message` to `message.json`, which no screen reads now; the test Story 3 added after it, `TestFixture_TheTurnsHoldAnAgentStepAndAThinkingItem`, stays, and the file's imports are all still used.)

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ -run 'TestPickScreens|TestCanned_|TestWebshotFixture'`
Expected: FAIL. `TestPickScreens_TheAgentScreenIsSessionOnesRunningSubagent`: `no screen "agent"`; `TestCanned_AnswersTheRoutesTheScreensReadAndNamesTheRest`: `Should be 404` for `/api/v1/sessions/1/message` (the route still answers `message.json`); `TestCanned_ASubagentsTurnsAreTheAgentFixture`: `Should not contain "agent"` is the first assertion that holds, then `Equal "a1f00dcafe0001" != ""` fails (the route still answers `turns.json`); `TestWebshotFixture_IsTheJSONTheLayerServes/agent-turns.json`: `open ../../tools/webshot/fixture/agent-turns.json: no such file or directory`.

- [ ] **Step 3: Add the screen and the route**

In `tools/webshot/main.go`, replace

```go
// opens on its conversation; "info" is the tab that holds its tiles, agents
// and actions.
var screens = []screen{
	{"fleet", ""},
	{"conversation", "#s/1"},
	{"waiting", "#s/2"},
	{"info", "#s/1/info"},
	{"diff", "#s/1/diff"},
```

with

```go
// opens on its conversation; "info" is the tab that holds its tiles, agents
// and actions; "agent" is the screen of session 1's running subagent (recon,
// the one its conversation links to).
var screens = []screen{
	{"fleet", ""},
	{"conversation", "#s/1"},
	{"waiting", "#s/2"},
	{"info", "#s/1/info"},
	{"agent", "#s/1/agent/a1f00dcafe0001"},
	{"diff", "#s/1/diff"},
```

In `tools/webshot/canned.go`, replace the route table (which also drops the last message route: no screen draws it now, and the fixture goes with it)

```go
// apiRoutes maps each GET route the screens read on load to its fixture: a
// JSON one with its relative times filled, or the mockup's page as it is.
var apiRoutes = []struct {
	pattern *regexp.Regexp
	fixture string
}{
	{regexp.MustCompile(`^/api/v1/sessions/\d+/message$`), "message.json"},
	{regexp.MustCompile(`^/api/v1/sessions/\d+/turns$`), "turns.json"},
	{regexp.MustCompile(`^/api/v1/sessions/\d+/diff$`), "diff.json"},
	{regexp.MustCompile(`^/api/v1/sessions/\d+/mockup$`), "mockup.html"},
	{regexp.MustCompile(`^/api/v1/launch-defaults$`), "launch-defaults.json"},
}
```

with

```go
// apiRoutes maps each GET route the screens read on load to its fixture: a
// JSON one with its relative times filled, or the mockup's page as it is.
var apiRoutes = []struct {
	pattern *regexp.Regexp
	fixture string

	// agent is the fixture for a request whose query names a subagent
	// (?agent=), "" for a route that has none.
	agent string
}{
	{regexp.MustCompile(`^/api/v1/sessions/\d+/turns$`), "turns.json", "agent-turns.json"},
	{regexp.MustCompile(`^/api/v1/sessions/\d+/diff$`), "diff.json", ""},
	{regexp.MustCompile(`^/api/v1/sessions/\d+/mockup$`), "mockup.html", ""},
	{regexp.MustCompile(`^/api/v1/launch-defaults$`), "launch-defaults.json", ""},
}
```

and in `api`, replace

```go
				body, err := c.fixture(rt.fixture)
```

with

```go
				name := rt.fixture
				if rt.agent != "" && r.URL.Query().Get("agent") != "" {
					name = rt.agent
				}
				body, err := c.fixture(name)
```

Delete the last message's canned page, which no route answers now (nothing else reads it: `grep -rn 'message.json' tools internal/web` finds only the entries removed above and in `tools/CLAUDE.md` below):

```bash
git rm -q tools/webshot/fixture/message.json
```

In `tools/CLAUDE.md`, in the webshot rule (line 18), replace

```
is served under a copy of `mockupCSP` the same test holds equal; the session's `last_message` must equal `message.json`'s `text`, or the app shows the plain text instead of the tree. Chromium is driven
```

with

```
is served under a copy of `mockupCSP` the same test holds equal. Chromium is driven
```

and replace

```
**Enforced** by `TestWebshotFixture_IsTheJSONTheLayerServes` (`internal/web/webshot_fixture_test.go`) and `TestFixture_TheLastMessageIsTheTreesText`; the real-browser run
```

with

```
**Enforced** by `TestWebshotFixture_IsTheJSONTheLayerServes` (`internal/web/webshot_fixture_test.go`); the real-browser run
```

- [ ] **Step 4: Write the subagent page fixture**

Create `tools/webshot/fixture/agent-turns.json`, the page `GET …/turns?agent=a1f00dcafe0001` serves for the running subagent `recon`: its `agent` object, a brief of 15 lines (it folds after 12) and one live turn:

```json
{
  "conversation": "c0ffee",
  "earlier": null,
  "reset": true,
  "skipped": 0,
  "agent": {
    "id": "a1f00dcafe0001",
    "type": "recon",
    "description": "Map every caller of the cursor path",
    "teammate": false,
    "state": "running",
    "started": "now-3m",
    "missing": false
  },
  "turns": [
    {
      "id": 1,
      "at": "now-3m",
      "from": "brief",
      "name": "",
      "text": "You are mapping callers.\n\nTask: find every caller of the cursor path in ui/ and session/.\n\nStart from ui/pane.go: cursorVisible, drawCursor and the header redraw.\nFor each call site report:\n- the file and line\n- whether it runs before or after the frame is drawn\n- whether it holds the pane's lock\n\nDo not edit anything.\nKeep the report under 200 words.\nName any caller you could not trace.\nReturn a list, most important first.\nIf the search is wider than ui/ and session/, say so.",
      "cut": false,
      "command": false,
      "partial": false,
      "items": [
        {
          "kind": "thinking",
          "at": "now-3m",
          "text": "Start with a grep for drawCursor,\nthen read each hit's surrounding frame code.",
          "truncated": false
        },
        {
          "kind": "steps",
          "at": "now-2m",
          "total": 4,
          "steps": [
            {
              "tool": "Grep",
              "summary": "drawCursor",
              "at": "now-2m",
              "state": "done"
            },
            {
              "tool": "Read",
              "summary": "ui/pane.go",
              "at": "now-2m",
              "state": "done"
            },
            {
              "tool": "Read",
              "summary": "ui/header.go",
              "at": "now-1m",
              "state": "done"
            },
            {
              "tool": "Grep",
              "summary": "cursorVisible",
              "at": "now-30s",
              "state": "pending"
            }
          ]
        },
        {
          "kind": "text",
          "at": "now-1m",
          "blocks": [
            {
              "t": "p",
              "c": [
                {
                  "t": "text",
                  "v": "Two callers so far: "
                },
                {
                  "t": "code_inline",
                  "v": "ui/pane.go"
                },
                {
                  "t": "text",
                  "v": " draws the cursor after the frame, under the pane's lock, and "
                },
                {
                  "t": "code_inline",
                  "v": "ui/header.go"
                },
                {
                  "t": "text",
                  "v": " redraws the header without it."
                }
              ]
            }
          ]
        }
      ]
    }
  ]
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/ ./tools/claudemd/`
Expected: `ok  	github.com/aidan-bailey/loom/tools/webshot`, `ok  	github.com/aidan-bailey/loom/internal/web` and `ok  	github.com/aidan-bailey/loom/tools/claudemd` (its structure test finds no `TestFixture_TheLastMessageIsTheTreesText` left in `tools/CLAUDE.md`).

- [ ] **Step 6: Render the screens and look at them**

Run: `go run ./tools/webshot -out /tmp/loom-shots-s4 -screens conversation,agent,info`
Expected: three lines `conversation	/tmp/loom-shots-s4/conversation.png`, `agent	…`, `info	…`, and no `!` line. Read the PNGs: `agent` shows `‹ conversation` and the title `Map every caller of the cursor path`, the tabs with conversation underlined, the strip `◐ subagent running 3m · recon`, a `BRIEF` box folded to 12 lines with `▸ 3 more lines`, a live turn (`CLAUDE · ◐ WORKING`) with an open `▸ thinking:` item, the open fold with its last step running, and no reply box and no needs-you box; `info` shows its two Agents rows with a `›` each.

- [ ] **Step 7: Commit**

```bash
git add -N tools/webshot/fixture/agent-turns.json
git commit -m "test(web): webshot renders a running subagent's screen and drops the last message's fixture" -- tools/webshot/main.go tools/webshot/canned.go tools/webshot/fixture/agent-turns.json tools/webshot/fixture/message.json tools/webshot/webshot_test.go tools/CLAUDE.md internal/web/webshot_fixture_test.go
```

### Task 4.5: Document the screens

**Files:**
- Modify: `internal/web/README.md:14,70,93,95`
- Modify: `internal/web/CLAUDE.md:30`
- Modify: `internal/web/static/js/markdown.mjs:1-4`
- Modify: `docs/claude/web-layer.md:38,39`
- Modify: `USAGE.md:708-728`

- [ ] **Step 1: Update the README**

In `internal/web/README.md`, in the `/message` row of the routes table, replace

```
the app draws it as elements, and only while `text` is the message it shows |
```

with

```
no screen draws it now (the conversation draws each text item of the turns route's trees), and the route stays |
```

In `internal/web/README.md`, in the `markdown.go` row of the files table, replace

```
`buildTree` parses a last message with goldmark into the plain nodes the app draws (`mdNode`)
```

with

```
`buildTree` parses a text item of the conversation (and the last message, whose route no screen draws now) with goldmark into the plain nodes the app draws (`mdNode`)
```

In the paragraph that starts "`index.html` loads `js/app.mjs`", replace

```
`#s/<id>` (a session), `#s/<id>/turns` (its conversation, `js/turns.mjs`), `#s/<id>/diff`
```

with

```
`#s/<id>` (a Claude session's conversation, `js/turns.mjs`; a session that does not run Claude draws its info tab; `#s/<id>/turns` is its old address, replaced by `#s/<id>` in place), `#s/<id>/info` (its info tab, `js/session.mjs`), `#s/<id>/agent/<agentId>` (a subagent's turns, `js/agent.mjs`), `#s/<id>/diff`
```

In the paragraph that starts "The skin is loom's TUI on the phone", replace

```
a session's screen fetches its message's tree on demand and draws it with `js/markdown.mjs`, showing the plain text until it arrives, and fills its reply box from the quick-reply chips, edited in Settings.
```

with

```
Claude's text in a turn is drawn from its tree with `js/markdown.mjs`, and a reply box fills from the quick-reply chips, edited in Settings (`GET …/message` stays, but no screen draws the last message now).
```

replace

```
The conversation screen reads its turns on open and refreshes the newest turn (`after`) when the stream changes the session's status or last message and every 5 s while it is Running;
```

with

```
The conversation screen reads its turns on open (a conversation left and returned to is restored from the turns and scroll position `js/turns.mjs` keeps per session, then refreshed) and refreshes the newest turn (`after`) when the stream changes the session's status or last message and every 5 s while it is Running;
```

and replace

```
A session's views (session, conversation, changes, and mockup while its brainstorm companion runs) are tabs under its context row (`SessionTabs`), and each ends in `js/reply.mjs`'s reply box, which keeps a draft per session across them; `useRun` is their shared runner. A waiting session's needs-you box lists the dialogs its Claude holds open, each answered there
```

with

```
A session's views (conversation, changes, info, and mockup while its brainstorm companion runs) are tabs under its context row (`SessionTabs`), and each but the subagent screen ends in `js/reply.mjs`'s reply box, which keeps a draft per session across them; `useRun` is their shared runner. A Claude session's conversation pins a fact strip under the tabs (`js/strip.mjs`: status and age, changes, PR and the account's share, the whole strip a link to the info tab), draws Claude's thinking as folds, and draws an Agent step as a link to the subagent's screen (`#s/<id>/agent/<agentId>`: the same turn renderer over `GET …/turns?agent=`, the brief first, a strip by the page's `agent.state`, no reply box, refreshed while the agent runs; the info tab's Agents rows link there too). A session that does not run Claude has no conversation: its `#s/<id>` draws the info tab. A waiting session's conversation ends in the needs-you box (`js/needs.mjs`), which lists the dialogs its Claude holds open, each answered there
```

- [ ] **Step 2: Update the web-layer guide**

In `docs/claude/web-layer.md`, replace the bullet

```
- A last message is drawn from its tree (`GET /api/v1/sessions/{id}/message`), never from an HTML string. A node the app needs is added in `internal/web/markdown.go`, with a case in `TestBuildTree_EachNodeType`.
```

with

```
- Claude's text is drawn from its tree (each text item of `GET /api/v1/sessions/{id}/turns`), never from an HTML string; a thinking item's `text` is drawn as a text node. `GET /api/v1/sessions/{id}/message` stays but no screen draws the last message now. A node the app needs is added in `internal/web/markdown.go`, with a case in `TestBuildTree_EachNodeType`.
```

In the same file, in the bullet that starts "To see a screen without a phone", replace

```
`-screens fleet,turns` picks screens
```

with

```
`-screens fleet,conversation` picks screens
```

and replace

```
a route a new screen reads needs a fixture and an entry in `apiRoutes` (`tools/webshot/canned.go`).
```

with

```
a route a new screen reads needs a fixture and an entry in `apiRoutes` (`tools/webshot/canned.go`), which also names the fixture a request with `?agent=` gets (`agent-turns.json` for the turns route).
```

After the bullet that ends "which then always scrolls." (the header and footer bullet), add a bullet:

```
- **A screen's `Head` and `Screen` share nothing but the snapshot.** The subagent screen's header draws its strip from the page the screen read, so `internal/web/static/js/agent.mjs` publishes the page's `agent` object to a module-level store the head watches (cleared when the screen unmounts), and `internal/web/static/js/turns.mjs` keeps each session's held turns and scroll in a module-level map so a trip to a subagent or another tab returns to the same place, as the reply drafts in `internal/web/static/js/reply.mjs` survive a tab switch. State of that kind lasts as long as the page and belongs in such a map, not in a component, which a tab switch unmounts.
```

- [ ] **Step 3: Reword the draw-from-the-tree rule**

In `internal/web/CLAUDE.md`, replace line 30

```
- **Draw Claude's text only from its tree (`buildTree` in `internal/web/markdown.go`): a last message (`GET /api/v1/sessions/{id}/message`) and each text item of a conversation (`GET /api/v1/sessions/{id}/turns`, which sends no item's text), as elements; draw prompts, step summaries and diff lines as text; and keep the link check (`safeHref`) in Go.** Claude's text can hold markup; an HTML string put into the page renders it, links and forms included, and the CSP stops only its scripts. **Enforced** for the tree by `TestBuildTree_KeepsNoMarkupAndFollowsNoUnsafeLink`, and for the turns route sending trees by `TestAPI_TurnsIsAPageOfTheConversation`; that no module puts an HTML string into the page is **Convention** — a message that rewrites the phone's screen.
```

with

```
- **Draw Claude's text only from its tree (`buildTree` in `internal/web/markdown.go`): each text item of a conversation (`GET /api/v1/sessions/{id}/turns` sends its `blocks`, not its text), as elements; draw prompts, step summaries, diff lines and a thinking item (the one item whose `text` that route sends) as text nodes, never as HTML; and keep the link check (`safeHref`) in Go.** `GET /api/v1/sessions/{id}/message` stays but no screen draws the last message, so a screen that does must draw its tree too. Claude's text can hold markup; an HTML string put into the page renders it, links and forms included, and the CSP stops only its scripts. **Enforced** for the tree by `TestBuildTree_KeepsNoMarkupAndFollowsNoUnsafeLink`, for the turns route sending trees by `TestAPI_TurnsIsAPageOfTheConversation` and thinking as plain `text` by `TestAPI_TurnsLinkAnAgentStepToItsSubagentAndKeepThinkingText`; that no module puts an HTML string into the page is **Convention** — a message that rewrites the phone's screen.
```

In `internal/web/static/js/markdown.mjs`, replace the header

```js
// The last message as the daemon parsed it (GET /api/v1/sessions/{id}/message):
// a tree of plain nodes, each drawn here as its element. The daemon kept no
// markup and no unsafe link, and this file never inserts an HTML string, so
// nothing a message says runs in or restyles the page.
```

with

```js
// Claude's text as the daemon parsed it (the blocks of a text item of
// GET /api/v1/sessions/{id}/turns; GET …/message sends the same tree for the
// last message, which no screen draws now): a tree of plain nodes, each drawn
// here as its element. The daemon kept no markup and no unsafe link, and this
// file never inserts an HTML string, so nothing Claude says runs in or
// restyles the page.
```

- [ ] **Step 4: Rewrite the phone-app screens in `USAGE.md`**

In `USAGE.md`, in the fleet bullet, replace

```
  open); the phone remembers the chip. A session leads with why it waits,
  then tiles for its changes, its pull request (which opens on GitHub) and
  its account's usage (both windows, and when each resets), its last
  message as Claude formatted it, its subagents by name, the actions, and
  a reply box.
```

with

```
  open); the phone remembers the chip. A Claude session opens on its
  conversation (below); a session that doesn't run Claude opens on its
  info tab.
```

and replace the bullet

```
- **The conversation and the changes.** A session's screen has tabs under
  its title: **session**, **conversation** on a Claude session (also
  **conversation ›** beside its last message) and **changes** while the
  session runs (also the **Changes** tile), with its line counts, and
  **mockup** while the brainstorming skill's visual companion runs in the
  session's worktree (marked when its screen changed in the latest turn):
  the page the companion shows, sandboxed, with a full-screen view. The
  conversation is its recent turns: each prompt (yours, a background
  task's, or a subagent's report), Claude's replies as it formatted them,
  and its tool calls as one-line steps folded under a count (tap to open;
  the newest turn's run is open while Claude works, its running step
  marked). **↑ earlier turns** loads more. The changes are the worktree's
  diff against its base commit, file by file: tap a file for its hunks.
  Both refresh while the session works; neither shows tool output. Every
  tab ends in the reply box, and a reply you started stays as you switch
  tabs.
```

with

```
- **The conversation.** A session's screen has tabs under its title:
  **conversation** on a Claude session, **changes** while the session
  runs (with its line counts), **info**, and **mockup** while the
  brainstorming skill's visual companion runs in the session's worktree
  (marked when its screen changed in the latest turn): the page the
  companion shows, sandboxed, with a full-screen view. A strip under the
  tabs shows the session's state and age, its line counts, its pull
  request and its account's usage; tap it for the info tab. The
  conversation is its recent turns: each prompt (yours, a background
  task's, or a subagent's report), Claude's replies as it formatted them,
  its thinking as a folded line (tap to read it), and its tool calls as
  one-line steps folded under a count (tap to open; the newest turn's run
  is open while Claude works, its running step marked). A subagent's step
  is a link to that subagent's screen. **↑ earlier turns** loads more. A
  session that waits on you ends its conversation in a box that says why
  and lists what it asks, each answered there, above the reply box. Every
  tab but a subagent's ends in the reply box, and a reply you started
  stays as you switch tabs. Going back from a subagent or another tab
  returns you to where you were, then refreshes.
- **The info tab.** Tiles for the session's changes, its pull request
  (which opens on GitHub) and its account's usage (both windows, and when
  each resets), its subagents by name (a row opens that subagent's
  screen), the actions, and the reply box.
- **A subagent's screen.** Its description is the title, with the session's
  tabs under it and a strip by its state: running with its age, finished
  (idle for a teammate) or unknown, then its type, and **session needs
  you ›** while the session waits. It shows the subagent's own turns, its
  brief first, and refreshes while it runs. It has no reply box. It reads
  **Starting…** until the subagent's record is written, and **This
  subagent's record is gone.** when it never is.
- **The changes.** The worktree's diff against its base commit, file by
  file: tap a file for its hunks. It refreshes while the session works and
  shows no tool output.
```

- [ ] **Step 5: Run the checks**

Run: `bash -c 'for f in internal/web/static/js/*.mjs; do node --check "$f" || exit 1; done; CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ ./tools/claudemd/ -run "TestStatic|TestRepo_ClaudeMDStructure|TestPickScreens"; go run ./tools/claudemd -v | grep "internal/web/CLAUDE.md"'`
Expected: no output from `node --check`, `ok` for each of the three packages, then `internal/web/CLAUDE.md:` with its lines under 100 and its bytes under 20000.

- [ ] **Step 6: Commit**

```bash
git commit -m "docs(web): the conversation-first screens, the subagent screen, and the last message route no screen draws" -- internal/web/README.md internal/web/CLAUDE.md internal/web/static/js/markdown.mjs docs/claude/web-layer.md USAGE.md
```
