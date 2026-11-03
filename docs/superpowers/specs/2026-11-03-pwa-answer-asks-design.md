# Answer Claude's questions and permission prompts from the phone

**Date:** 2026-11-03
**Status:** Approved design. Brainstormed with the user; the spike ran the
same day (Outcome, below) and settled the mechanism.

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
`--plugin-dir` folder it loads), checked live by the spike:

- **`tool.call`** fires when the engine is about to run a tool; `next(e)`
  runs the hooks beneath, then core: the permission prompt and the tool
  itself. A hook answers in place of `next` with `{ result }` (validated
  against the tool's output schema, mapped for the model, recorded as the
  tool's result) or `{ deny }` (the model reads the text as an error
  result), and **returning with `next` pending aborts what runs beneath**:
  the question dialog closes.
- **`AskUserQuestion`'s input** in this build has only choice questions:
  `{ question, header, options: [{ label, description, preview? }],
  multiSelect }`. Its result is `{ questions, answers, annotations }`:
  `answers` maps question text to the answer, several labels joined by
  `", "`, an Other answer as its text among them; `annotations` maps
  question text to `{ notes }`.
- **`classic.PermissionRequest`** is the settings hook as a function-hook
  event, chained [managed settings hooks, hooks modules, the other settings
  hooks as core], so loom's own status hook runs beneath the mod's `next`.
  Its input carries `tool_name`, `tool_input`, `permission_suggestions`
  and, for a subagent's, `agent_id` and `agent_type`; no `tool_use_id`. An
  answer's `decision` is `{ behavior: 'allow', updatedPermissions? }` or
  `{ behavior: 'deny', message?, interrupt? }`.
- **Budgets** (`HookBudget`): a hook has 10 s of its own time; the clock
  stops while its `next(e)` or any of its `$` calls is in flight, but not
  during a `$.clock` wait or while it awaits a promise another hook's `$`
  settles. The host aborts every `$.http.fetch` at 30 s, so the mod
  socket's holds stay 24 s (`modserver.DefaultHold`); `$.http.fetch` takes
  no abort signal.
- The phone already knows a wait through the hooks (`WaitReason`,
  `WaitDetail`), and pushes on it; nothing structured about the ask
  reaches the daemon. Claude's roster (`claude agents --json`, polled every
  3 s) reports `waiting` with `waitingFor: "permission prompt"` while a
  main-loop permission dialog is open.

## Decisions

| # | Decision |
|---|---|
| 1 | **Scope:** `AskUserQuestion` dialogs and tool permission prompts, the main loop's and subagents' (a background agent's prompt sits in Claude's agents panel, out of the main view). MCP elicitation dialogs and every other dialog are out. |
| 2 | **The race:** Claude's own dialog stays up in the terminal and in Remote Control; the mod holds the ask open, and the first answer wins, wherever it comes from. |
| 3 | **Mechanism (approach A):** a `tool.call` hook on `AskUserQuestion` answers with `{ result }` or `{ deny }`; a `classic.PermissionRequest` hook answers with `{ decision }`. Approach B (both through `PermissionRequest`) and keystrokes into the pane were rejected. |
| 4 | **Fidelity, as this build's dialogs offer it:** options, single and multi-select, Other, previews, notes, and "Chat about this"; for permissions, allow, each offered "allow, and …" rule, and deny, which stops the turn as the terminal's "No" does, with an optional message that follows as the person's own reply. Text and number questions, `response` and `followUp` are not produced by this build's dialog and are left out. |
| 5 | **Placement:** the session screen only. The fleet row's detail line names the first ask; no buttons on the fleet, no change to push. |
| 6 | **Policy in Go:** the daemon validates every answer and composes the exact `result`, `deny` or `decision`; the mod returns what it is handed. The module stays a carrier (`session/claudemod`'s rule). |
| 7 | **Fail closed:** whenever the daemon can't hold an ask (no mod, an older mod, the daemon down, a malformed ask), the phone shows today's box and the terminal asks as it always has. The mod never answers on its own. |
| 8 | **No protocol bumps:** an older daemon answers 404 to the new mod routes, and an older mod never reports an ask; `InstanceView.Asks` is a new wire field (protocol reference regenerated, no `rpc.Protocol` bump). |
| 9 | **A confirmation sheet** for an "allow, and …" rule saved to the user's, project's or local settings, since it outlives the session; allow, a session rule and deny are one tap. |
| 10 | **How an ask closes when answered elsewhere** (the spike's finding): a question's `next` resolves; a permission's call settles (the mod pairs each permission ask with its `tool.call`), or, for a main-loop ask, the daemon sees the session leave Prompting. `next.signal` never aborts on a person's answer. |

## Design

### The mod

Three hooks beside the turn hooks. Each first makes sure of the pump
(`ensurePump`); without the launch's socket and ID each is a plain
`return next(e)`.

- **Pairing, `tool.call` on every tool:** record the call's `tool_use_id`
  under its tool name and canonical input (the event less `tool`,
  `tool_use_id`, `agentId` and `consent`, keys sorted), run `next(e)`, and
  on settling drop the record and settle the permission ask paired with
  it, if any. It changes nothing it passes on.
- **Questions, `tool.call` on `{ tool: 'AskUserQuestion' }`:** start
  `next(e)` and keep its promise; this draws the dialog. Post the ask (its
  ID minted as `POLLER` is) and wait for its answer. Whichever comes first
  wins:
  - the answer: return `{ result }` or `{ deny }` as handed; `next` is
    pending, so the dialog closes;
  - `next`: the terminal, Remote Control or an AFK auto-resolve answered;
    post the close (`how: 'elsewhere'`) and return what `next` returned.
- **Permissions, `classic.PermissionRequest`:** await `next(e)` first, so
  loom's status hook records the prompt; return its result at once when it
  carries a `decision`, or when `tool_name` is `AskUserQuestion` (that
  event fires for every question, and the question hook holds it).
  Otherwise pair the ask with the oldest unpaired in-flight call of the
  same tool and input, post the ask, and wait for whichever comes first:
  - the answer: return `{ ...result, decision }`;
  - the paired call settling: post the close (`how: 'settled'`) and
    return the result;
  - the daemon closing the ask (410): return the result.
- **Waiting for an answer:** `POST /v<n>/asks` once (a 400 or 404 there
  stops waiting: an ask the daemon refused, a launch it doesn't know, or an
  older daemon without the route), then `GET /v<n>/answer?launch=L&ask=A`
  in a loop, each held by the daemon up to `DefaultHold`. 200 carries the
  answer; 204 polls again; 404 (the daemon doesn't know the ask: it
  restarted) posts the ask again and polls; 410 (the daemon closed it) and
  409 (a newer instance of the module holds the launch) stop waiting.
  Any other failure retries after the pump's backoff while something else
  holds the hook's clock (the question's `next`), and otherwise stops
  waiting, so the hook never fails. A poll still in flight when the hook
  returns is left to end on its own.

The ask the mod posts: `{ launch, poller, ask, kind: 'question' |
'permission', agentId?, agentType?, toolUseId?, tool, input, suggestions? }`,
where `input` is the tool's input as Claude handed it and `suggestions` the
request's `permission_suggestions`, both passed through unread.

### The protocol

Three routes on the mod socket, built from `claudemod.Version` like the
others, each naming its launch (unknown: 404) and the poller (superseded:
409):

| Route | Body / query | Answers |
|---|---|---|
| `POST /v<n>/asks` | the ask | 204, kept; 400 malformed, nothing kept |
| `GET /v<n>/answer?launch=L&poller=P&ask=A` | — | 200 `{ result }`, `{ deny }` or `{ decision }` once answered; 204 after the hold; 404 unknown ask; 410 closed; 503 on a stop |
| `POST /v<n>/asks/close` | `{ launch, poller, ask, how }` | 204; an unknown ask is 204 too |

The answer hold ends on a wake, a stop or the client leaving, as the
inbox's does (`internal/modserver`'s rule).

### The daemon

- **Where asks live:** on the launch's `modLink`, keyed by ask ID, in
  memory only. `endLink` and `modGone` drop them with the link; a
  restarted daemon learns them again from the mods still holding. A closed
  ask is kept on the link, marked closed, until its poll hears 410 or 30 s
  pass, then dropped.
- **Parsing an ask (`ModAsk`):** a question ask keeps its questions as
  given (raw, to hand back) and parsed (text, header, multi-select,
  options with label, description and preview); a permission ask keeps
  the tool, its input (raw, capped at 64 KiB), its suggestions (each raw,
  to hand back, and parsed), a one-line summary (`hooks` summary rules, as
  `WaitDetail`), the tool-use ID, and the agent ID and type. Over 4
  questions, 4 options per question (Claude's own schema caps), 16
  suggestions or 64 KiB of input, or an input that doesn't parse as its
  kind: 400, kept nowhere.
- **Publishing:** `InstanceView.Asks []AskView`, oldest first, open ones
  only, through the views diff (`syncEvents`).
- **Closing on a status change:** when an instance's reported status
  leaves Prompting, every open main-loop permission ask (no agent ID) it
  held from before that change closes. A subagent's waits only for its
  call: the roster reads `busy` while a background agent's prompt waits.
- **`Core.Answer(id InstanceID, ask string, answer AskAnswer, req
  ReqID)`**, a request. `AskAnswer`: for a question ask, `Answers` per
  question keyed by its text (`Labels`, `Other`, `Note`) or `Chat` (the
  person chose "Chat about this", with optional words); for a permission
  ask, `Allow`, `Suggestion` (0 none, else the 1-based index of an offered
  one) and `Message` (deny only). The model refuses, with the reason:
  - an instance or ask it doesn't know, a closed one, one already answered
    (first answer wins);
  - a question it has no answer for and no Chat, a label not among the
    question's options, several labels or labels plus Other on a
    single-select question, an empty answer, a suggestion not offered, a
    message on an allow.

  It composes, stores on the ask and wakes the ask's poll:
  - a question's `result`: `{ questions: <as given>, answers, annotations }`,
    each answer the chosen labels in option order then Other, joined by
    `", "`, each note as `annotations[text].notes`;
  - Chat's `deny`: Claude's own clarify text, as the terminal writes it
    (Outcome, below), listing every question with "(No answer provided)";
  - a permission's `decision`: `{ behavior: 'allow' }`, with
    `updatedPermissions: [<the chosen suggestion as given>]`, or
    `{ behavior: 'deny', interrupt: true }`.

  A Chat's words and a deny's message then go to the session as the
  person's own reply (`Send`, `OriginWeb`), queued until the turn ends,
  only once the mod has taken the answer.
- **The Reply:** sent when the mod takes the answer (its `GET /answer`
  returns it). When the ask closes first, the Reply is an error, "answered
  in Claude first". When no poll takes it within 30 s (`modTick`), the
  answer is withdrawn (a late poll must not deliver an answer the phone
  was told failed) and the Reply is an error naming that.

### The phone

- **Projection (`internal/web`):** `sessionJSON.asks`, each `{ id, kind,
  agent, opened }` plus, for a question, its questions; for a permission,
  `tool`, `summary`, `input` and `suggestions: [{ label, persists }]`.
  `input` is a view chosen per tool: Bash's command and description; Edit's
  path with its old and new text; MultiEdit's path and edits; Write's path
  and content; WebFetch's URL; any other tool's input as indented JSON;
  each text capped. Suggestion labels name what the rule does and for how
  long ("Yes, and don't ask again for Bash(npm test:*) in this project",
  "Yes, and switch to accept edits for this session", "Yes, and allow
  access to <dir> for this session"); `persists` is set for a destination
  of user, project or local settings.
- **Route:** `POST /api/v1/sessions/{id}/asks/{ask}/answer`, behind
  `authed`, calls `Core.Answer` and waits for its Reply: 200; 409 for an
  ask answered or closed; 422 for a refused answer, with the reason.
- **Fleet:** a waiting row's detail line is its first ask's first
  question, or the permission's tool and summary, in place of
  `WaitDetail`; with several asks it adds "+n".
- **Session screen:** the needs-you box lists each open ask, oldest first,
  with "Open in Claude ›" below them; a subagent's names its agent type.
  - A question ask: per question, its header chip and text; its options as
    a radio or checkbox list, each with its description, a chosen option's
    preview shown below the list in monospace; an "Other" field; a "Note"
    field. **Answer** sends every question at once; **Chat about this**
    opens a sheet with an optional message and sends the chat.
  - A permission ask: the tool's input view; **Allow**; one **Allow, and
    …** per suggestion, the persisting ones behind a confirmation sheet;
    **Deny…**, a sheet with an optional message to Claude.
  - Drafts survive switching tabs, as the reply's does. A sent answer
    shows "Sending…" until its reply; then the ask leaves with the view. A
    409 shows "Answered in Claude first"; a 422 shows its reason inline.

## Limits

- An ask reaches the phone only from a session launched with a mod that
  has the new hooks: a session started before the upgrade must be paused
  and resumed.
- A permission allowed in the terminal for a subagent stays on the phone
  until its tool finishes (only the call's settling tells the mod), and so
  does a main-loop one for up to one roster poll. An answer sent in that
  window is taken and ignored by Claude: the dialog had gone.
- If Claude's AFK auto-resolve closes a question first, the phone's answer
  is refused as answered elsewhere.
- In `auto` permission mode the classifier settles most asks before a
  person is asked; only what reaches the person reaches the phone.
- Two identical calls in flight (same tool, same input) pair with their
  asks oldest first, which may swap which ask closes when.

## Testing

- **Model (`core`):** an ask's life (posted, published, answered, taken,
  closed); each refusal in `Core.Answer`; the composed `result`, `deny` and
  `decision` against goldens taken from the spike; first answer wins; the
  terminal-first close; the status-change close (main loop only); the 30 s
  withdrawal; asks dropped by `endLink` and `modGone`; an ask posted again
  after a restart; a Chat's words and a deny's message sent as a reply
  only after the take.
- **`internal/modserver`:** the three routes' 204/400/404/409/410/503; the
  answer hold ending on a wake, a stop and the client leaving; URLs built
  from `claudemod.Version`.
- **`internal/web`:** the projection (input views, suggestion labels,
  `persists`, the fleet's detail line), the answer route's 200/409/422.
- **Opt-in real Claude (`LOOM_TEST_REAL_CLAUDE=1`):** a question answered
  through the daemon, a permission allowed, one denied, the terminal
  answering first for each, and `TestRealClaude_ValidatesTheMod`'s pinned
  call list updated for the new hooks.
- **App:** screens rendered with headless Chromium against a canned server
  (`docs/claude/web-layer.md`); the plan's iPhone checklist.

## Spike, as a gate

A throwaway mod on a private tmux server, against Claude 2.1.316 (haiku),
answering from a local HTTP server; the questions it asked:

1. Does a question hook holding `next(e)` close the dialog by returning
   `{ result }`, and what does the terminal's own answer look like?
2. Is the permission dialog drawn while a `PermissionRequest` hook holds,
   and do allow, a suggested rule and deny work from the held hook?
3. How does a held hook learn that the person answered in the terminal?
4. Do a subagent's asks reach the hooks; does `AskUserQuestion` raise
   `PermissionRequest`?
5. Does a hook held for minutes on 24 s fetches keep its budget?

### Outcome, 2026-11-03

- **1, yes.** Returning `{ result: { questions, answers, annotations } }`
  with `next` pending closed the dialog; the model read `"…"="Green" notes:
  greener the better, "…"="Dog, Axolotl"`. The terminal's own answer
  resolved `next` to `{ result: { questions, answers: { "Which color do
  you prefer?": "Blue", "Which pets do you have?": "Cat, Fish, Hamster" },
  annotations: {} }, text, isReadOnly }` (Hamster typed as Other in a
  multi-select). "Chat about this" resolved `next` to an errored result
  whose text is, verbatim (`<q>` each question):

  ```
  The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). To tell you how to proceed, the user said:
  The user wants to clarify these questions.
      This means they may have additional information, context or questions for you.
      Take their response into account and then reformulate the questions if appropriate.
      Start by asking them what they would like to clarify.

      Questions asked:
  - "<q>"
    (No answer provided)
  ```

  Claude then asked what to clarify and ended its turn.
- **2, yes.** The Bash dialog stayed drawn and answerable while the hook
  held. `{ behavior: 'allow' }` closed it ("Allowed by PermissionRequest
  hook") and ran the command; `updatedPermissions: [{ type: 'setMode',
  mode: 'acceptEdits', destination: 'session' }]` switched the mode; `{
  behavior: 'deny', message }` refused it, and the model took the message
  as tool output, not the person's words ("That text came from the tool
  output, not from you"); `{ behavior: 'deny', interrupt: true }` ended
  the turn exactly as the terminal's "No" does ("Interrupted · What should
  Claude do instead?"). Hence decision 4's deny. A decision returned after
  the dialog had closed was ignored. The dialog's options differ from the
  suggestions (it offered "switch to auto mode" for a `setMode` to
  `acceptEdits`), so the phone labels each suggestion by what it does.
- **3, the design changed here.** A question's `next` resolves on any
  answer. A permission hook's `next.signal` never aborted when the person
  answered (the hook kept polling 120 s after), `$.ui.notice` on the call
  was never refused (and drew nothing), and `tool.check` settles `ask`
  before the dialog opens. Only the paired `tool.call` settling tells the
  mod, which for an allow is after the tool ran (24 s for a `sleep 20`).
  The roster flipped from `waiting`/`permission prompt` to `busy` within
  1.5 s of a terminal answer, but read `busy` throughout a background
  agent's pending prompt. Hence decision 10.
- **4, yes.** A background `general-purpose` agent's Bash reached
  `tool.call` with `agentId` and `PermissionRequest` with `agent_id` and
  `agent_type`, and an allow from the hook ran it. `AskUserQuestion`
  raises `PermissionRequest` with the questions as `tool_input`.
- **5, yes.** A permission hook held 122 s and a question hook 50 s on
  24 s fetches, with no budget failure.
- AFK auto-resolve was not observed; the output schema's `afkTimeoutMs` is
  all the API says.

## Out of scope

- MCP elicitation dialogs, plan-mode exit prompts and any other dialog.
- Answering from the fleet list, from a notification, or from the CLI
  (`Core.Answer` leaves the CLI possible later).
- Editing a tool's input before allowing it (`updatedInput`), which the
  terminal's dialog offers only through its own amend.
- Persisting asks across daemon restarts: the holding mods post them again.
