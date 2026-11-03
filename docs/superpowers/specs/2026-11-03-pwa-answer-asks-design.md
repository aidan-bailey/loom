# Answer Claude's questions and permission prompts from the phone

**Date:** 2026-11-03
**Status:** Approved design. Brainstormed with the user. The spike below
gates the plan.

## Problem

When a session waits on an `AskUserQuestion` dialog or a tool permission
prompt, the phone shows "Needs you", the wait's one-line detail, and "Open
in Claude" (Remote Control). The reply box can't help: the mod submits a
reply with `$.prompt.submit`, which Claude queues until the session is
idle, so a reply never answers an open dialog (by design: the mod spec
rejected typing into dialogs). Answering means leaving the app for
Claude's own, or going back to the desk.

## Findings

From Claude Code 2.1.316's mod API (`.claude-plugin/types/claude-code/index.d.ts`
and `claude-code-tools/index.d.ts`, which Claude writes into every
`--plugin-dir` folder it loads):

- **`tool.call`** fires when the engine is about to run a tool; `next(e)`
  runs the hooks beneath, then core: the permission prompt and the tool
  itself. A hook answers in place of `next` with `{ result }` (validated
  against the tool's output schema, mapped for the model, recorded as the
  tool's result) or `{ deny }`, and **returning with `next` pending aborts
  what runs beneath**. `$.ui.ask` documents that `AskUserQuestion`'s dialog
  is drawn by core under `tool.call`.
- **`AskUserQuestion`'s output** is `{ questions, answers, response?,
  annotations?, afkTimeoutMs?, followUp? }`: `answers` maps question text to
  the answer (multi-select comma-joined), `response` is free text typed
  instead of choosing, `followUp` asks for another round, and `afkTimeoutMs`
  marks a dialog that auto-resolved after the person was idle. Its
  questions may carry `kind` (`choice`, `text`, `number`), `description`,
  `placeholder`, `min`/`max`/`step`/`defaultValue`/`unit`; options carry
  `label`, `description?`, `preview?`. The input's `annotations` are keyed by
  question text (this session's own tool schema: `{ notes?, preview? }`).
- **`classic.PermissionRequest`** is the settings hook as a function-hook
  event. Its chain is [managed settings hooks, hooks modules, the other
  settings hooks as core], so loom's own status hook (a settings hook in
  the launch's `settings.json`) runs beneath the mod's `next`. An answer's
  `decision` is `{ behavior: 'allow', updatedInput?, updatedPermissions? }`
  or `{ behavior: 'deny', message?, interrupt? }`; the input carries
  `tool_name`, `tool_input` and `permission_suggestions` (each `addRules`,
  `replaceRules`, `removeRules`, `setMode`, `addDirectories` or
  `removeDirectories`, with a `destination`: `userSettings`,
  `projectSettings`, `localSettings`, `session` or `cliArg`). It carries no
  `tool_use_id`. The API calls such an ask "a person's: the dialog, the
  host's callback or a PermissionRequest hook answers".
- **Budgets** (`HookBudget`): a hook has 10 s of its own time; the clock
  stops while its `next(e)` or any of its `$` calls is in flight, but not
  during a `$.clock` wait or while it awaits a promise another hook's `$`
  settles. After `next.signal` aborts (the person interrupted, a hook above
  settled first) a hook may linger 5 s. The host aborts every `$.http.fetch`
  at 30 s, so the mod socket's holds stay 24 s (`modserver.DefaultHold`).
- The phone already knows a wait through the hooks (`WaitReason`,
  `WaitDetail`), and pushes on it; nothing structured about the ask
  reaches the daemon.

## Decisions

| # | Decision |
|---|---|
| 1 | **Scope:** `AskUserQuestion` dialogs and tool permission prompts, the main loop's and subagents'. MCP elicitation dialogs and every other dialog are out. |
| 2 | **The race:** Claude's own dialog stays up in the terminal and in Remote Control; the mod holds the ask open, and the first answer wins, wherever it comes from. |
| 3 | **Mechanism (approach A):** a `tool.call` hook on `AskUserQuestion` answers with `{ result }`; a `classic.PermissionRequest` hook answers with `{ decision }`. Approach B (both through `PermissionRequest`, a question answered by `updatedInput.answers`) was rejected: it can't carry `response` or `followUp`. Keystrokes into the pane were rejected outright. |
| 4 | **Fidelity: everything.** Options, single and multi-select, Other, previews, notes (annotations), text and number questions, "chat about this" (`response`/`followUp`); for permissions, allow, each offered "allow, and …" rule, and deny with a message and an optional stop of the turn (`interrupt`). |
| 5 | **Placement:** the session screen only. The fleet row's detail line names the first ask; no buttons on the fleet, no change to push. |
| 6 | **Policy in Go:** the daemon validates every answer and composes the exact `result` or `decision`; the mod returns what it is handed. The module stays a carrier (`session/claudemod`'s rule). |
| 7 | **Fail closed:** whenever the daemon can't hold an ask (no mod, an older mod, the daemon down, a malformed ask), the phone shows today's box and the terminal asks as it always has. The mod never answers on its own. |
| 8 | **No protocol bumps:** an older daemon answers 404 to the new mod routes, and an older mod never reports an ask; `InstanceView.Asks` is a new wire field (protocol reference regenerated, no `rpc.Protocol` bump). |
| 9 | **A confirmation sheet** for an "allow, and …" rule saved to the user's, project's or local settings, since it outlives the session; allow, a session rule and deny are one tap. |

## Design

### The mod

Two hooks, registered beside the turn hooks. Each first makes sure of the
pump (`ensurePump`); without the launch's socket and ID it is a plain
`return next(e)`.

- **`tool.call` on `{ tool: 'AskUserQuestion' }`**, every loop (`agentId`
  set for a subagent's): start `next(e)` and keep its promise; this draws
  the dialog. Mint an ask ID (as `POLLER` is minted), post the ask, and
  wait for its answer (below). Whichever settles first wins:
  - the answer: return `{ result: answer.result }`; `next` is pending, so
    the dialog is aborted;
  - `next`: the terminal or Remote Control answered; post the close and
    return what `next` returned.
- **`classic.PermissionRequest`**: await `next(e)` first, so loom's status
  hook records the prompt; return its result at once when it carries a
  `decision` (a settings hook decided), or when `tool_name` is
  `AskUserQuestion` and the spike finds that it raises this event too (the
  question hook holds it). Otherwise mint an ask ID, post the ask, and wait
  for its answer or `next.signal`:
  - the answer: return `{ ...result, decision: answer.decision }`;
  - the signal: the person answered in the terminal; post the close and
    return within the 5 s linger.
- **Waiting for an answer:** `POST /v<n>/asks` once (a 400 or 404 there
  stops waiting: an ask the daemon refused, a launch it doesn't know, or an
  older daemon without the route), then
  `GET /v<n>/answer?launch=L&ask=A` in a loop, each held by the daemon up
  to `DefaultHold`. 200 carries the answer; 204 polls again; 404 (the
  daemon doesn't know the ask: it restarted) posts the ask again and polls;
  409 (a newer instance of the module holds the launch) stops waiting for
  good, leaving the dialog to the person. Any other failure retries with
  the pump's backoff, within the hook's budget; past it the hook stops
  waiting and returns as above, never failing. Every wait ends when the
  hook settles.
- **The close:** `POST /v<n>/asks/close` with the ask ID and how it ended
  (`elsewhere`: the terminal, Remote Control or an AFK auto-resolve), not
  awaited.

The ask the mod posts: `{ launch, poller, ask, kind: 'question' |
'permission', agentId?, tool, input, suggestions? }`, where `input` is the
tool's input as Claude handed it and `suggestions` the request's
`permission_suggestions`, both passed through unread.

### The protocol

Three routes on the mod socket, built from `claudemod.Version` like the
others, each naming its launch (unknown: 404) and the poller (superseded:
409):

| Route | Body / query | Answers |
|---|---|---|
| `POST /v<n>/asks` | the ask | 204, kept; 400 malformed, nothing kept |
| `GET /v<n>/answer?launch=L&ask=A` | — | 200 `{ result }` or `{ decision }` once answered; 204 after the hold; 404 unknown ask; 503 on a stop |
| `POST /v<n>/asks/close` | `{ launch, ask, how }` | 204; an unknown ask is 204 too |

The answer hold ends on a wake, a stop or the client leaving, as the
inbox's does (`internal/modserver`'s rule).

### The daemon

- **Where asks live:** on the launch's `modLink`, keyed by ask ID, in
  memory only. `endLink` and `modGone` drop them with the link, so a dead
  launch's asks vanish with it; a restarted daemon learns them again from
  the mods still holding.
- **Parsing an ask (`ModAsk`):** a question ask keeps its questions (text,
  header, kind, description, options with label, description and preview,
  multi-select, placeholder, min, max, step, default, unit); a permission
  ask keeps the tool, its input (capped at 64 KiB), the suggestions, a
  one-line summary (`hooks.CapSummary` over the tool's summary key, as
  `WaitDetail` reads it) and the agent ID. An ask whose input doesn't parse
  as its kind, or exceeds the caps, is refused with 400 and kept nowhere.
- **Publishing:** `InstanceView.Asks []AskView`, oldest first, through the
  views diff (`syncEvents`). A subagent's ask names the subagent by its
  type when the tracker knows the agent ID.
- **`Core.Answer(id InstanceID, ask string, answer AskAnswer, req ReqID)`**,
  a request. `AskAnswer` holds, for a question ask, an answer per question
  keyed by the question's text (chosen labels, Other text, a text answer or
  a number, and a note) plus an optional chat text and follow-up flag; for
  a permission ask, allow or deny, the index of an offered suggestion, a
  message and the interrupt flag. The model refuses, with the reason:
  - an instance or ask it doesn't know, or one already answered (first
    answer wins: a second phone, or the phone after the terminal);
  - a label not among the question's options, several labels on a
    single-select question, a number outside `min..max`, a text answer to a
    choice question, a suggestion index not offered, a message on an allow.

  It composes the `result` (`answers` keyed by question text, multi-select
  labels comma-joined, Other as the answer's text, notes as `annotations`,
  `response` and `followUp` as the spike records the terminal writing them)
  or the `decision` (`updatedPermissions` holding the one chosen
  suggestion), stores it on the ask and wakes the ask's poll.
- **The Reply:** sent when the mod takes the answer (its `GET /answer`
  returns it). When the mod closes the ask first, the Reply is an error,
  "answered in Claude first". When no poll takes it within 30 s, the
  answer is withdrawn (a late poll must not deliver an answer the phone
  was told failed) and the Reply is an error naming that.

### The phone

- **Projection (`internal/web`):** `sessionJSON.asks`, each `{ id, kind,
  agent, opened }` plus, for a question, its questions; for a permission,
  `tool`, `summary`, `input` and `suggestions: [{ label, persists }]`.
  `input` is a view chosen per tool: Bash's command and description; Edit's
  and MultiEdit's path and old→new hunks; Write's path and content;
  WebFetch's URL; any other tool's input as indented JSON, capped.
  Suggestion labels are composed in Go in the terminal's words ("Yes, and
  don't ask again for `npm test` commands in this project", "Yes, and
  switch to accept edits", "Yes, and always allow access to `<dir>`");
  `persists` is set for a destination of user, project or local settings.
- **Route:** `POST /api/v1/sessions/{id}/asks/{ask}/answer`, behind
  `authed`, calls `Core.Answer` and waits for its Reply: 200; 409 for an
  ask answered or closed; 422 for a refused answer, with the reason.
- **Fleet:** a waiting row's detail line is its first ask's first question,
  or the permission's tool and summary, in place of `WaitDetail`; with
  several asks it adds "+n".
- **Session screen:** the needs-you box lists each open ask, oldest first,
  with "Open in Claude ›" below them.
  - A question ask: per question, its header chip, text and description;
    its options as a radio or checkbox list, each with its description, a
    tapped option's preview shown below it in monospace; an "Other" field;
    a "Note" field; a text box for a text question and a stepper with its
    unit for a number one. **Answer** sends every question at once;
    **Chat about this instead** opens a text box and sends the chat.
  - A permission ask: the tool's input view; **Allow**; one **Allow, and
    …** per suggestion, the persisting ones behind a confirmation sheet;
    **Deny…**, a sheet with an optional message to Claude and a "Stop the
    turn" toggle.
  - Drafts survive switching tabs, as the reply's does. A sent answer
    shows "Sending…" until its reply; then the ask leaves with the view. A
    409 shows "Answered in Claude first"; a 422 shows its reason inline.

## Limits

- An ask reaches the phone only from a session launched with a mod that
  has the new hooks: a session started before the upgrade must be paused
  and resumed.
- The phone answers only while Claude holds the dialog. If Claude's AFK
  auto-resolve (`afkTimeoutMs`) closes a question first, the phone's answer
  is refused as answered elsewhere.
- In `auto` permission mode the classifier settles most asks before a
  person is asked; only what reaches the person reaches the phone.
- An ask the daemon refused as malformed exists only in the terminal.

## Testing

- **Model (`core`):** an ask's life (posted, published, answered, taken,
  closed); each refusal in `Core.Answer`; the composed `result` and
  `decision` against goldens taken from the spike's recorded shapes; first
  answer wins; the terminal-first close; the 30 s withdrawal; asks dropped
  by `endLink` and `modGone`; an ask posted again after a restart.
- **`internal/modserver`:** the three routes' 204/400/404/409/503; the
  answer hold ending on a wake, a stop and the client leaving; URLs built
  from `claudemod.Version`.
- **`internal/web`:** the projection (input views, suggestion labels,
  `persists`, the fleet's detail line), the answer route's 200/409/422.
- **Opt-in real Claude (`LOOM_TEST_REAL_CLAUDE=1`):** a question answered
  through the daemon, a permission allowed, one denied with a message, the
  terminal answering first, and `TestRealClaude_ValidatesTheMod`'s pinned
  call list updated for the new hooks.
- **App:** screens rendered with headless Chromium against a canned server
  (`docs/claude/web-layer.md`); the plan's iPhone checklist.

## Spike, as a gate

Before the plan's first story, a throwaway mod (outside the worktree) on a
private tmux server, against Claude 2.1.316, records:

1. A `tool.call` hook on `AskUserQuestion` holding `next(e)` returns `{
   result }`: the dialog closes in the terminal (and in Remote Control), and
   the model reads the answers, annotations, `response` and `followUp`.
   Record the result `next` resolves to for each way of answering in the
   terminal: an option, several, Other, a note, "chat about this", and any
   follow-up path; the composer's goldens come from these.
2. While a `classic.PermissionRequest` hook holds after its `next`, the
   terminal dialog is drawn and answerable; allow, allow with a suggestion
   (the rule is saved where its destination says) and deny with a message
   each take effect from the held hook. **If the dialog is not drawn while
   the hook holds, stop and take the design back to the user.**
3. Answering in the terminal first resolves `next` (the question hook) or
   aborts `next.signal` (the permission hook), and the hook can return
   within the linger.
4. A subagent's question and permission ask reach both hooks, with their
   agent ID; whether `AskUserQuestion` also raises `PermissionRequest`.
5. A hook held for minutes, with only `$.http.fetch` calls of 24 s in
   flight, keeps its budget; when Claude's AFK auto-resolve closes a
   question, and how it reaches the hook.

## Out of scope

- MCP elicitation dialogs, plan-mode exit prompts and any other dialog.
- Answering from the fleet list, from a notification, or from the CLI
  (`Core.Answer` leaves the CLI possible later).
- Editing a tool's input before allowing it (`updatedInput`), which the
  terminal doesn't offer either.
- Persisting asks across daemon restarts: the holding mods post them again.
