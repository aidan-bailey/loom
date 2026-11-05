# Subagent job results: a job's message goes back to the subagent that submitted it

Status: design approved 2026-11-05.

Builds on [the work scheduler](2026-11-04-work-scheduler-design.md) (§5.8, the notice) and [the loom mod](2026-10-14-loom-claude-mod-design.md).

## 1. Problem

`loom job submit` sends a job's result to "the session this command runs in". The daemon finds that session from `$LOOM_LAUNCH`, which the command inherits (`jobOwner`, `core/queue_ops.go`). A Claude Code subagent runs inside its parent's process, in the same pane, with the same environment. So a job a subagent submits is filed under the session, and its `[loom] job N` message arrives as a prompt to the top-level Claude.

The subagent never hears its result. It either returns to its parent with "job 12 queued" and no outcome, or it goes on without knowing the build failed. The parent meanwhile gets a result for a job it didn't submit, and has to work out whose it is. Commit 2443acc1 told the parent to brief subagents to use the blocking `loom job run` instead. That works for short jobs, but a subagent can't wait out a long build: the Bash tool times out at 10 minutes.

Success:
- A subagent that runs `loom job submit` in a foreground Bash call gets the job's message itself, even after it has finished.
- The parent gets no extra message for that job.
- Whenever loom can't place a result, it reaches the parent as today, naming the subagent when known.
- No command an agent runs is changed, and no permission outcome changes.

## 2. Decisions

- **Attribute by watching calls, never by rewriting them.** Live probes (Claude Code 2.1.318, §4) showed that any rewrite of a Bash command breaks the allow rules that matched the original. Tested forms: `export X=…; cmd`, `export X=… && cmd` and `X=… cmd`. In interactive sessions that means new permission prompts; under `-p`, denials. The mod's `tool.call` runs before the permission check, so a rewrite there fails the same way. Claude Code gives a subagent's shell nothing to tell it apart from the parent's: the same variable names and values. Rejected: a `PreToolUse` command rewrite; a mod rewrite; answering the call from the mod, which skips Claude's permission check entirely.
- **Deliver through the mod with `$.session.send`, resuming a finished subagent** (the owner, 2026-11-05). The subagent has the context of why it submitted the job and can act on the result. Its reply then reaches the parent as a task notification. Rejected: sending to the subagent only while it runs, and to the parent once it has finished. That would save a resume, but the parent lacks the context and the mod would have to track which subagents are live.
- **No extra message to the parent when the subagent takes the result** (the owner, 2026-11-05). The parent hears through the subagent's own reply.
- **Show the submitting subagent in `loom job list` and the work view** (the owner, 2026-11-05).
- **Every failure falls back to today's behaviour.** No match, an ambiguous match, a mod that can't send, or a send the subagent can't take all deliver to the session's main conversation. The parent's copy names the subagent when there is one.

## 3. Scope

In:
- the mod's call reports and its sends to subagents;
- `POST /v1/calls`;
- attribution in the daemon;
- `sched.Record.Agent` and `core.JobView.Agent`;
- the routing of notices and their fallbacks;
- the owner text in `loom job list` and the work view;
- `submit`'s and `run`'s lines that say where a result goes;
- the context prompt;
- tests;
- the package rules that change.

Out (§13): submits made inside scripts or background shells, routing to agent-team teammates, the phone app, and redirecting heavy commands into the queue automatically.

## 4. What the probes established

Live, on Claude Code 2.1.318, 2026-11-05. Each probe ran a `claude -p` with loom's variables stripped, with only the probe's hooks or plugin loaded.

- A `PreToolUse` hook's input carries `agent_id` and `agent_type` for a subagent's tool call, and neither for the parent's. The mod's `tool.call` event carries the same `agentId`.
- That ID is the Agent tool result's `agentId`, and `SendMessage` and `$.session.send` both accept it.
- A plugin's `$.session.send({ to: { agentId }, text })` to a finished subagent returns `{ isDelivered: true }`. The subagent resumes with its context intact and its reply reaches the parent as a task notification, which starts a parent turn. This held whether the send ran mid-turn (inside a parent's `tool.call`) or as the parent's turn ended (`turn.complete`).
- A send to an ID with no transcript returns `{ isDelivered: false, reason: "Agent '…' could not be resumed: No transcript found for agent ID …" }` and doesn't throw.
- `requestMeta`, the one side channel `tool.call` offers, reaches MCP tools only.
- **A send to a subagent that is still running** returns `{ isDelivered: true }` at once; the probe sent 4 s into a 15 s Bash call. The message reaches the subagent when its current tool call returns, before its next model step. Claude Code frames it as from "the coordinator" and appends "Address this before completing your current task." The subagent handled it in the same turn: no resume, and no extra parent turn.
- **A send after Claude restarts with `--resume`** to a subagent of the earlier process returns `{ isDelivered: true }`. The subagent resumes from its transcript with its context intact, and its reply reaches the resumed parent as a task notification. A relaunch that starts a new conversation instead has no such transcript, so the send fails and the parent copy goes out (§5.3).

These last two are §12's gate, run 2026-11-05 after the spec was first written. Both passed, and nothing in §5 changed.

## 5. Behaviour

### 5.1 The mod reports `loom job` calls

The mod's `tool.call` hook reports every Bash call whose command matches the job pattern `\bloom\s+job\s+(submit|run)\b`, from a subagent or from the main loop. A report is a `POST /v1/calls` (§6) naming:
- the call's `tool_use_id`;
- its `agentId`, or none on the main loop;
- its command text.

The mod awaits the report before `next(e)` runs the command, bounded at 2 s. When `next(e)` settles, it reports the call ended, without awaiting. A report that fails or times out is logged to the debug log, and the call goes on unreported.

Other Bash calls are neither reported nor delayed. The main loop's calls are reported only so the daemon can tell two identical commands apart (§5.2).

The pattern is a filter, not a decision: the daemon still decides attribution. It lives in two places, `register.ts` and Go (`jobCallPattern`), and a Go test checks that `register.ts` carries Go's pattern.

### 5.2 Attribution

The daemon keeps, on each instance's mod link, the calls its mod reported in flight. They are kept oldest first and capped at 64, the oldest dropped with a debug log line. Each call holds its `tool_use_id`, agent, command and `uses`, the number of non-overlapping matches of `jobCallPattern` in its command. An ended report removes its call. The link's end drops them all.

When a submit arrives from a launch whose instance has a link (`submitJob`, after `jobOwner`), the daemon looks for **the one reported call whose command contains every element of the submitted argv as a substring and has `uses` left**:
- **Exactly one such call:** it spends one use. The job's `Agent` is that call's agent, or empty for the main loop.
- **None, or more than one:** `Agent` is empty, and the job reports to the session as today.

The match is exact or nothing, never a guess by position or timing. What follows from the rule:
- A long `loom job run -- cargo test` in flight doesn't take a script's unrelated submit: the argv differs.
- Two identical commands in flight, from two subagents or from a subagent and the parent, are ambiguous, so neither job gets an agent.
- A call that submits twice (`loom job submit -- a && loom job submit -- b`) has two uses, so both submits match it.
- Uses keep a finished `run` from taking a later submit of the same command.
- A submit in a background Bash call arrives after its call has ended, so it matches nothing. So do a submit inside a script and the third pass of a shell loop.

The ordering holds without timestamps. The mod's report lands before the command starts. The CLI waits for the daemon's reply to its submit before it exits. The ended report is sent only after the command exits.

### 5.3 Delivery

`sendJobNotice` and `deliverJobNotice` carry the job's `Agent` onto the queued `modMessage` as `agent`. Which path the message takes is decided when an inbox poll hands it to the mod, not when it is queued: the hello that says whether this mod can send to subagents may come after the message was queued.

- **`agent` set, and the link's hello said `agents: true`:** the mod gets `ModMessage.Agent` and the subagent text (§5.4). It sends that with `$.session.send({ to: { agentId }, text })`, keeping the same de-duplication of IDs already handed to Claude that its submits use:
  - `isDelivered: true`: it reports `queued`, and the daemon records the notice sent (`noteNotice`, as today). No `started` or `complete` follows: the message starts no main-loop turn.
  - `isDelivered: false`, or the send throws: it reports `failed` with the reason. The daemon queues the parent text (§5.4) for the same instance, with no agent. Its fate is recorded when that one is queued, as any job notice's is.
- **`agent` set, and the link's hello did not say `agents: true`** (a mod from before the upgrade): the message goes to the main loop as the parent text, with the old-mod reason.
- **`agent` empty:** today's path, unchanged.

A link that ends while holding an agent message settles it as today: `undelivered`, with the reason.

A job submitted with `loom job run` that its follower's interrupt turns into a notice (`NotifyJob`) takes the same path, since `Agent` was recorded at submit.

### 5.4 Texts

- **Subagent text:** today's notice, unchanged (`composeNotice`: head line, then the log tail). The subagent submitted the job and reads it as its own.
- **Parent text:** today's notice with one line after the head:
  - unreachable subagent: `(Submitted by your subagent <id>; loom could not reach it: <reason>.)`
  - old mod: `(Submitted by your subagent <id>; this session's loom mod is too old to message it, so pass this on with SendMessage if it still needs it.)`

  The full ID is written there, since `SendMessage` takes it.

### 5.5 Where a result goes, said at submit

- `loom job submit`, after its placement line:
  - `its result comes back to this subagent (<short id>)` when the job has an agent;
  - `its result comes to this session` when it has an owner and no agent;
  - the existing `no session to notify: …` line when it has neither;
  - nothing with `--no-notify`.

  The short ID is the first 7 characters. The line lets a subagent see that it may end its turn.
- `loom job run`'s interrupt line says the subagent is sent the message, rather than the session, when the job has an agent.

## 6. The mod protocol

Every addition is optional, so `MOD_VERSION`, the manifest's major version and the `/v1` path stay. A mod from before the upgrade sends no `agents` and no call reports, so it gets parent texts. A newer mod facing an older daemon gets 404 from `/calls` and ignores it, and the older daemon ignores the hello's `agents` field (`decode` allows unknown fields).

- **`ModHello.Agents bool` (`agents`):** this mod reports `loom job` calls and can send a message to a subagent. Kept on the link as `modLink.agents`.
- **`POST /v1/calls`:** body `{ launch, poller, call: { id, agent, command }, ended }`.
  - Checked as `/asks` checks (`askLink`): an unknown launch is refused, and a poller other than the latest hello's gets 409.
  - Answers 204.
  - The model's side is a new `ModHost` method, `ModCall(launch, poller string, c ModCall, ended bool) error`.
- **`ModMessage.Agent string` (`agent`):** set only on a message for a subagent. The mod sends `Text` to it instead of submitting `Text`.
- **Events:** unchanged. A send to a subagent reports `queued` or `failed`.

The mod's reach stays within `$.env`, `$.http`, `$.prompt`, `$.command`, `$.session`, `$.clock` and `$.ui.log`, since `$.session.send` is a `$.session` call. `TestRealClaude_ValidatesTheMod`'s pinned call list gains `$.session.send`.

## 7. Records and the wire

- **`sched.Record.Agent string` (`agent`, omitempty):** set at submit, never changed. No `StoreVersion` bump. An older build ignores the field and drops it at its next save, so the job reports to the session, which is today's behaviour.
- **`core.JobView.Agent string`:** the record's agent, full ID. The wire carries it as a new field, and `docs/specs/protocol.md` is regenerated. No `rpc.Protocol` bump, since nothing is removed or renamed.
- **`sched.Submit`:** unchanged. The daemon attributes, the CLI sends nothing new.

## 8. Display

- `loom job list` OWNER (`ownerText`): `lto build › ab91bd4` when the job has an agent; otherwise as today.
- The work view's owner cell (`ui/work.go`): `lto build › ab91bd4 · loom`, the workspace label last as today.

## 9. Loom's Claude context

The sentence commit 2443acc1 added to `session/loom_context_shared.md` is replaced by one along these lines:

> A job's message goes back to whoever submitted it, so a subagent you brief to run `loom job submit` in a foreground Bash call gets its own result, even after it has finished, and can end its turn; a result loom can't place comes to you, naming the subagent.

`TestJobContext_NamesOnlyFlagsTheCommandsHave` keeps asserting that the paragraph names subagents. It drops the `loom job wait <N>` assertion if the new sentence no longer names `wait`.

## 10. Components

| Where | What changes |
|---|---|
| `session/claudemod/mod/hooks/register.ts` | the Bash call reports in `tool.call`; the hello's `agents`; `sendToAgent` beside `submit` and `run` in the pump |
| `internal/modserver/server.go` | the `/calls` route |
| `core/mod.go` | `ModHello.Agents`, `ModMessage.Agent`, `ModCall`, `ModHost.ModCall`; `modLink.agents` and `calls`; the hand-out choice in the inbox; `modFailed`'s fallback for an agent message |
| `core/mod_calls.go` (new) | the in-flight call list, `jobCallPattern`, `attribute` |
| `core/queue_ops.go` | `submitJob` sets `Record.Agent` |
| `core/queue_notice.go` | the agent on the notice; the parent text |
| `core/queue_view.go` | `JobView.Agent` |
| `core/loop_mod.go` | `Loop.ModCall` forwarding |
| `sched/job.go` | `Record.Agent` |
| `internal/cli/job.go`, `job_follow.go`, `job_list.go` | the routing line, the interrupt line, `ownerText` |
| `ui/work.go` | the owner cell |
| `session/loom_context_shared.md`, `internal/cli/job_test.go` | §9 |
| `session/claudemod/CLAUDE.md`, `core/CLAUDE.md`, `internal/modserver/CLAUDE.md` | rules for the call reports, exact-or-nothing attribution and the hand-out choice, within each file's budget (`go run ./tools/claudemd -v` first) |

## 11. Failure handling

| What goes wrong | What happens |
|---|---|
| The daemon is slow or gone when the mod reports a call | The report times out at 2 s, and the command runs unreported, so the job reports to the session |
| The ended report is lost | The call stays in the list until the cap drops it or the link ends; its `uses` keep it from matching more submits than its command makes |
| No reported call matches, or several do | No agent: today's path |
| A submit in a script, a background shell or a loop | No match: today's path |
| The subagent's ID can't be resumed (a workflow agent, an engine fork, a lost transcript) | `isDelivered: false`, so the parent text, with the reason |
| A mod from before the upgrade | The parent text, with the old-mod reason |
| The link ends holding an agent message | `undelivered`, as today |
| The daemon restarts | Reported calls are lost, so jobs submitted meanwhile have no agent. Agents already recorded stay in `jobs.json` |
| A downgrade to an older loom | `agent` is dropped at its next save, so the job reports to the session |

## 12. Testing

The gate before building was to probe two open premises live, as §4's probes ran: a send to a subagent that is still running, and a send after Claude restarts with `--resume`. Both passed on 2026-11-05 (§4).

Go:
- **`core`, attribution:**
  - one match sets the agent;
  - two matches, and no match, set none;
  - a main-loop match sets none;
  - an ended call matches nothing;
  - `uses` are spent, and a call with two pattern matches takes two submits;
  - a missing argv element defeats a match;
  - the cap drops the oldest;
  - a link's end drops its calls.
  Each guard is pinned on both sides, and each term of the match is mutation-checked separately.
- **`core`, delivery:**
  - a link with `agents` hands out `Agent` and the subagent text;
  - one without hands out the parent text, decided at hand-out (an agent message queued before the hello);
  - `queued` records the notice sent;
  - `failed` queues the parent text with the reason, then records it sent;
  - a link's end settles an agent message `undelivered`.
- **`core`, the pattern:** `register.ts` carries `jobCallPattern`'s pattern.
- **`internal/modserver`:** `/calls` decodes and forwards; a superseded poller gets 409; an unknown launch is refused as `/asks` refuses it.
- **`sched`:** `Record.Agent` round-trips, and a record without it loads.
- **`internal/cli`:** `ownerText` with an agent; `submit`'s routing line in each case; `run`'s interrupt line with an agent; the context paragraph (§9).
- **`ui`:** the work view's owner cell.

Opt-in, real Claude (`LOOM_TEST_REAL_CLAUDE=1`):
- `TestRealClaude_ASubagentGetsItsJobsResult`: a test daemon and a real Claude with the mod. A subagent runs `loom job submit -- sh -c 'echo MARKER'` and ends its turn. The test asserts that the subagent's resumed reply quotes the marker, and that the parent received no `[loom] job` prompt.
- `TestRealClaude_ValidatesTheMod`, with `$.session.send` added to its call list.

## 13. Known limits and later work

- A submit loom can't see go to the parent as today: one inside a script, a background shell or the passes of a loop beyond its pattern matches.
- Agent-team teammates carry an `agentId` the mod sees, and `$.session.send` reaches a live one, but this design is untested with them.
- Resuming a finished subagent costs a subagent turn and then a parent turn. A subagent still running takes the message in its current turn, at no extra cost (§4). The owner chose this over the parent taking results once the subagent has finished.
- The phone app's job views, if it gains any, should show `Agent` as §8 does.
- Sending heavy commands to the queue automatically, by denying a direct run with a pointer to `loom job run`, is a separate feature.
