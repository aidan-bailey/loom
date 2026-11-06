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

- `func ValidAgentID(id string) bool`: false for `""`, `"."`, `".."` and any id holding `/` or `\`; `MetaPath` returns `""` when it is false.
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

## Stories

- **Story 1** — The transcript reader reads subagents and thinking.

  Add `Query.Agent`, the brief turn, the step→agent links from meta files and `toolUseResult`, `Step.Finished`, thinking items and `Page.Agent` to `session/transcript`, with the shared id check and meta fields in `session/subagent`. Fixture transcripts under `testdata/` modelled on real files (a parent with a foreground Agent step and a background one, `agent-*.jsonl` and `agent-*.meta.json` with and without `toolUseId` and `finished`, thinking blocks blank, redacted and with text). Extend the opt-in `TestRealClaude_TranscriptReader` with one subagent.

  - **Developer:** intermediate-dev
  - **Files:** `session/subagent/meta.go`, `session/subagent/meta_test.go`, `session/transcript/transcript.go`, `session/transcript/records.go`, `session/transcript/build.go`, `session/transcript/agents.go`, `session/transcript/agents_test.go`, `session/transcript/transcript_test.go`, `session/transcript/realclaude_test.go`, `session/transcript/testdata/`
  - **Interfaces:** exposes `subagent.ValidAgentID`, `subagent.SubagentsDir`, `Meta.ToolUseID`, `Meta.Finished`, `transcript.Query.Agent`, `Step.Agent`, `Step.Finished`, `KindThinking`, `FromBrief`, `AgentInfo`, `AgentRunning`/`AgentFinished`/`AgentUnknown`, `Page.Agent`; uses nothing from other stories
  - **Verify:** `CGO_ENABLED=0 go test ./session/transcript/ ./session/subagent/`
  - **Review:** deep
  - **Reading:** spec Findings, Decisions §10–§14, Errors and edge cases, Testing; `session/transcript/transcript.go`, `session/transcript/records.go`, `session/transcript/build.go`, `session/subagent/meta.go`

- **Story 2** — The wire and the web layer carry them.

  `subagent.View.ID` from the tracker; regenerate `docs/specs/protocol.md`; `core/reads_test.go` proves `ReadTurns` passes `Query.Agent` to the reader; `internal/web/reads.go` reads `?agent=` (400 on a bad id) and maps the new fields; `internal/web/fleet.go` carries the agent id; `internal/web/README.md` documents the route's parameter and fields.

  - **Developer:** intermediate-dev
  - **Files:** `session/subagent/tracker.go`, `session/subagent/tracker_test.go`, `core/reads_test.go`, `docs/specs/protocol.md`, `internal/web/reads.go`, `internal/web/reads_test.go`, `internal/web/fleet.go`, `internal/web/fleet_test.go`, `internal/web/README.md`
  - **Interfaces:** uses Story 1's transcript and subagent names; exposes the web JSON fields and the `agent` parameter in the names contract
  - **Verify:** `CGO_ENABLED=0 go test ./session/subagent/ ./core/ ./core/rpc/ ./internal/web/`
  - **Review:** deep
  - **Reading:** spec Decisions §15–§17; `internal/web/reads.go`, `internal/web/fleet.go:55-120,240-275`, `core/reads.go`, `core/view.go:60-90`, `session/subagent/tracker.go`

- **Story 3** — The app opens on the conversation.

  Routes, tabs, the fact strip, the info tab, the needs-you box moved to the conversation's foot, thinking items and agent links in the turn renderer, and the `useTurnPages`/`TurnList` split that Story 4 reuses. A non-Claude session's `#s/<id>` draws `Info`. Webshot fixtures and screens for conversation, waiting and info; the `session` and `turns` screens retire.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/static/js/app.mjs`, `internal/web/static/js/screens.mjs`, `internal/web/static/js/chrome.mjs`, `internal/web/static/js/strip.mjs`, `internal/web/static/js/needs.mjs`, `internal/web/static/js/session.mjs`, `internal/web/static/js/turns.mjs`, `internal/web/static/js/fleet.mjs`, `internal/web/static/app.css`, `tools/webshot/main.go`, `tools/webshot/fixture/turns.json`, `tools/webshot/fixture/snapshot.json`
  - **Interfaces:** uses Story 2's JSON fields; exposes `parseRoute`'s routes, `FactStrip`, `NeedsYou`, `Info`, `useTurnPages`, `TurnList`, the tab keys and the webshot screen names
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && go run ./tools/webshot -out /tmp/loom-shots-s3 -screens conversation,waiting,info,fleet'`
  - **Review:** deep
  - **Reading:** spec Decisions §1–§6, §8; `internal/web/static/js/turns.mjs`, `internal/web/static/js/session.mjs`, `internal/web/static/js/chrome.mjs`, `internal/web/static/js/app.mjs:20-40`, `docs/claude/web-layer.md`

- **Story 4** — The subagent screen.

  `#s/<id>/agent/<agentId>`: `AgentHead`, `AgentStrip`, the brief, live refresh by `agent.state`, the Starting… and gone states, no reply box; info's Agents rows link to it; the conversation keeps its held turns and scroll per session so back returns to the same place. Webshot's `agent` screen and fixture, and the screens' docs in `internal/web/README.md` and `docs/claude/web-layer.md`.

  - **Developer:** intermediate-dev
  - **Files:** `internal/web/static/js/agent.mjs`, `internal/web/static/js/strip.mjs`, `internal/web/static/js/turns.mjs`, `internal/web/static/js/session.mjs`, `internal/web/static/js/screens.mjs`, `internal/web/static/app.css`, `tools/webshot/main.go`, `tools/webshot/canned.go`, `tools/webshot/fixture/agent-turns.json`, `internal/web/README.md`, `docs/claude/web-layer.md`
  - **Interfaces:** uses Story 3's `useTurnPages`, `TurnList`, `parseRoute`'s `agent` route and Story 2's `agent` page object; exposes `Agent`, `AgentHead`, `AgentStrip`
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ && go run ./tools/webshot -out /tmp/loom-shots-s4 -screens conversation,agent,info'`
  - **Review:** deep
  - **Reading:** spec Decisions §7, §9, Errors and edge cases; `internal/web/static/js/turns.mjs`, `internal/web/static/js/reply.mjs`, `tools/webshot/canned.go`, `tools/webshot/main.go:39-60`

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
