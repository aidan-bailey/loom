# Claude Hook Events: Status, Resume and Last Message

**Date:** 2026-09-23
**Status:** Approved design (amended 2026-09-23 after the live probe: no
`hookGrace`, hook events trigger roster queries, `Stop` with a running
subagent is Running, session ID from `SessionStart` only; and while
planning: the sidecar read stays in `session/subagent`, resume goes through
a new `BuildResumeCommand`, reason precedence, and the dirty-promotion
exemption)
**Plan:** `docs/superpowers/plans/2026-09-23-claude-hook-events.md`
**Verified against:** Claude Code 2.1.280, by a live probe on 2026-09-23 (an
interactive haiku session on a private tmux server, hooks registered for
every event below, and two `claude agents --json` pollers sampling every
~50ms). Payload facts not observed live come from the hooks reference
(code.claude.com/docs/en/hooks) and loom's subagent-nesting findings
(`2026-09-16-subagent-nesting-design.md`).

## Problem

Loom works out a Claude session's status in two layers. The agent roster
(`claude agents --json`, polled every 3s) answers first; when it has no
answer, a pane-content ladder infers one from screen text (`app/app.go`,
`statusDetectedMsg`): changed content is Running, a matching prompt string is
Prompting, anything else is Ready, re-sampled via `maybeRedetect` because one
sample cannot tell "still working" from "just finished". The ladder has been
the subject of about ten fix commits (`1154c62`, `697c242`, `ce6e4a6`,
`166112a`, …) and depends on UI wording such as `"No, and tell Claude what to
do differently"`.

Three further gaps:

- Crash recovery relaunches with `--continue`, which picks the most recent
  conversation in the directory, not the one loom was showing.
- Cards show the last lines of pane output, with heuristics to skip Claude
  Code's footer (`ui/card.go`, `chromeFooterMax`). When a session stops, what
  Claude said matters more than what the screen shows.
- Snapshot-path cards have no emulator screen and show only a status label.

This is the first spec on the `claudetheist` branch (loom supporting Claude
Code only). It covers three sub-projects that share one event path: status
from hooks (A), exact resume (B) and cards from Claude's last message (C). An
argv-based launcher (D) and removing non-Claude support and the screen ladder
(E) are separate specs.

## Findings

### Loom already has the transport

Every Claude launch with tracking on gets `--settings <hooks>/settings.json`,
registering a shell hook that writes each payload to
`<hooks>/events/<unix-nanos>-<pid>.json` (`session/subagent/hooks.go`). A
gated scan (`maybeSubagentScan`, every 3s) converts new `.json` files to
compact `.ev` files and applies them through `Instance.ApplySubagentScan`,
which already drops results from another launch, adopts a restored
instance's `launch-id`, and rebuilds from the full `.ev` history on the
first scan after a loom restart.

### Hook events that carry status

From the hooks reference:

| Event | Fires | Fields used |
|---|---|---|
| `SessionStart` | a session begins or resumes | `session_id`, `transcript_path`, `source` (startup / resume / clear / compact) |
| `UserPromptSubmit` | a prompt is submitted, before Claude processes it | — |
| `PermissionRequest` | the moment a tool call needs a permission decision | `tool_name` |
| `Notification` | a notification is sent | `notification_type`, `message` |
| `Stop` | the main agent's turn ends | `last_assistant_message`, `background_tasks` |
| `SessionEnd` | the session ends | — |

Every payload carries `session_id`, `transcript_path` and `cwd`.

### Notification is too late to drive Prompting

`permission_prompt`, `elicitation_dialog` and `elicitation_url_dialog`
notifications fire only after the prompt has waited about six seconds, and
each keystroke defers them. `idle_prompt` fires about 60s after Claude
finishes. `PermissionRequest` fires immediately, but not for a sandboxed
command's network request; that prompt reports only through the
`permission_prompt` notification.

### Nothing fires when a prompt is answered

After the user approves a permission prompt, the next event is
`PostToolUse`, when the tool finishes. A long tool run therefore shows no
hook evidence of work for its whole duration. The roster reports `busy`
at once (probe: the first sample after the approval keystroke).

### Subagents run inside the parent process

From the subagent-nesting findings: subagents and teammates run in their
parent's process and report through `SubagentStart` / `SubagentStop` /
`TeammateIdle`. A subagent's permission prompt appears in the parent's UI,
and its `PermissionRequest` carries the subagent's `agent_id` (probe).

### `Stop` ends a turn, not the work

Probe: the parent's `Stop` fired mid-work twice.

- **Background subagent.** Claude launched the subagent in the background
  and ended the parent's turn (`last_assistant_message: "Agent launched.
  Waiting for completion..."`). That `Stop`'s `background_tasks` listed
  `{"type":"subagent","status":"running"}`. When the subagent finished,
  Claude resumed the parent with a `UserPromptSubmit` whose `prompt` starts
  with `<task-notification>`.
- **Teammate.** The lead stopped with "Waiting for probe-mate's reply...",
  then picked up the reply and stopped again 1.27s later with **no event in
  between**. Both `Stop`s list the teammate as `running`, including the
  final one after the teammate had gone idle, so teammate entries cannot
  tell the two apart.

The roster stayed `busy` through both intermediate `Stop`s and reported
`idle` only at the real end.

### The roster leads the hooks

At every change the probe measured, the roster had already moved when the
hook's timestamp was taken: `busy` 60–63ms before `UserPromptSubmit`,
`waiting` ("permission prompt") 86ms before `PermissionRequest`, `idle` 0–70ms
before `Stop`. Claude updates its published status first and then runs the
hook, whose `date` runs in a newly started shell. A roster answer stamped
after a hook event therefore always reflects that event. One
`claude agents --json` call took 100–111ms (five timed runs), not the
~380ms the comments in `session/claude_roster.go` cite.

### Session lifecycle events

- `/clear` sends `SessionEnd` (`reason: "clear"`) for the old session, then,
  31ms later, `SessionStart` (`source: "clear"`) with a new `session_id`. The
  new transcript exists at once.
- `claude --resume <id>` sends `SessionStart` (`source: "resume"`) with the
  **same** `session_id`.
- `claude --resume <unknown id>` prints "No conversation found with session
  ID: …", exits 1, and sends only a `SessionEnd` carrying the unknown ID.
- `/exit` sends `SessionEnd` (`reason: "prompt_input_exit"`).

## Decisions

1. **Scope A+B+C as one spec**, because they share the event path.
2. **Status latency target: under about 1s.**
3. **Newest observation wins.** Hooks and the roster are peer sources, each
   stamped with the time it was observed. Rejected: hooks only trigger a
   roster query (status would still depend entirely on the roster), and
   hooks only with no roster (the prompt-answered gap would need
   screen-based patching). Amended after the probe: there is no grace
   window, and a status event also triggers a roster query, which corrects
   intermediate `Stop`s and answered prompts within about 100–200ms whenever
   the roster works.
4. **Cards replace the tail with Claude's last message when stopped**, in
   both the rail and the overview, within the validity window in §6.
5. **Hooks are installed on every Claude launch that can take them.**
   `claude_subagent_tracking` now only controls whether subagent rows are
   shown.

## Design

### 1. Packages

- **`session/hooks` (new).** Moved out of `session/subagent`: folder
  layout, `SettingsJSON`/`HookCommand`/`Prepare`, `Scan`, `Request`/`Result`,
  and `Event` with `ParseEvent`/`Compact`. No tmux, UI or app dependency.
  `HookEvents` gains `SessionStart`, `UserPromptSubmit`, `PermissionRequest`
  and `Notification`.
- **`session/subagent` (smaller).** `Tracker` and `Meta`, consuming
  `hooks.Event`, plus `ReadMeta`: the sidecar read `Scan` used to do stays
  here, because sidecars are a subagent concept and `hooks` cannot import
  `subagent` without a cycle. No behaviour change. `realclaude_test.go`
  stays here too; it exercises the tracker.
- **`session`.** `HookScanRequest`/`HookScanResult` and `ScanHooks`
  (`hooks.Scan`, then `subagent.ReadMeta`) in `hook_scan.go`. A
  `claudeState` on `Instance` (§2), updated by `ApplySubagentScan`, renamed
  `ApplyHookScan`. `SubagentScanRequest` becomes `NextHookScan`.
- **`app`.** `adoptRosterStatus` becomes `adoptClaudeStatus` (§3). The scan
  gate `gateSubagent` becomes `gateHookScan` (§4).

`hooks.Event` gains:

| Field | Source |
|---|---|
| `At time.Time` | the `<unix-nanos>` filename prefix; the file's modification time when that does not parse (macOS `date` has no `%N`) |
| `SessionID` | `session_id` |
| `Source` | `source` (SessionStart) |
| `ToolName` | `tool_name` (PermissionRequest) |
| `NotificationType`, `Message` | `notification_type`, `message` (Notification) |
| `LastAssistantMessage` | `last_assistant_message` (Stop), capped at 4 KB keeping the end, which is what a card shows |

`Compact` writes the new fields using the payload's own names, so
`ParseEvent` reads both forms as today. `At` is not serialized: a replay
recomputes it from the retained `.ev` file, since `writeCompact` keeps the
original stem and copies the original modification time onto it
(`os.Chtimes`), so both sources of `At` survive.

### 2. Instance state

```go
type observation struct {
    status Status
    reason string    // wait reason; empty unless Prompting
    at     time.Time // when observed, not when delivered
    source obsSource // obsHook | obsRoster
    valid  bool      // false: no opinion
}

type claudeState struct {
    obs            observation
    sessionID      string // persisted (§5)
    transcriptPath string // persisted (§5)
    lastMessage    string // transient (§6)
    lastMsgValid   bool
}
```

The fields live under `i.mu`, with locking accessors, following the existing
launch-field pattern. `ApplyHookScan` folds events in order: a replayed
result resets `lastMessage` first, then applies the full history; an
incremental result applies on top. `obs` needs no reset on a replay, since
replayed events are never newer than it (newest wins). `sessionID` and
`transcriptPath` are never reset by a replay, since a replay can only
replace them with the same or newer values. A new launch
(`resetHookLaunch`) sets `obs` to no-opinion stamped *now*, so a roster
answer from before the relaunch cannot revive the old process's status; a
vanished hooks folder does the same to a hook-sourced `obs` only.

### 3. Events to observations, and the merge rule

Only **parent** events, those without an `agent_id`, set status or the
session ID. The one exception is `PermissionRequest`, whose prompt appears in
the parent's UI whichever agent raised it.

| Event | Observation | Also |
|---|---|---|
| `SessionStart`, source startup / resume / clear | Ready | set `sessionID`, `transcriptPath` |
| `SessionStart`, source compact or unknown | none (compaction can happen mid-turn) | set `sessionID`, `transcriptPath` |
| `UserPromptSubmit` | Running | invalidate `lastMessage` |
| `PermissionRequest` | Prompting, reason `permission: <tool_name>` | invalidate `lastMessage` |
| `Notification` permission_prompt / elicitation_dialog / elicitation_url_dialog | Prompting, reason = `message` | |
| `Notification`, any other type | none | |
| `Stop` whose `background_tasks` has an entry with `type: "subagent"` and `status: "running"` | Running | set `lastMessage`, mark valid |
| `Stop`, otherwise (including a missing or malformed `background_tasks`) | Ready | set `lastMessage`, mark valid |
| `SessionEnd` | no opinion, at the event's `at` | |

Only `SessionStart` sets `sessionID` and `transcriptPath`; no other event
does. A failed `--resume <unknown id>` sends a `SessionEnd` carrying the
unknown ID, so taking the ID from any other event would record it. Teammate
and `shell` entries in `background_tasks` never make a `Stop` Running:
teammates stay listed as `running` while idle, and a background shell (a dev
server, a `tail -f`) runs while Claude waits for input. A missing task list
reads as Ready here, unlike the subagent tracker, which must do nothing with
one: a wrong Ready is corrected by the roster query the event triggers (§4),
while a wrong Running would stay until the next event. `PreToolUse` and
`PostToolUse` are not registered: they fire on every tool call, and
`PostToolUse` includes the tool's whole output, which the hook would write
to disk.

**Roster observations** are stamped with `at` taken inside
`rosterQueryCmd`'s Cmd immediately before the subprocess starts, carried on
`rosterReadyMsg`. `adoptClaudeStatus` offers the instance's roster entry
(when there is one) as an observation, then applies whatever `claudeState`
holds.

**Merge rule.** A new observation replaces the current one when its `at` is
newer, whichever source either came from. There is no grace window: the
roster leads the hooks (see Findings), so a roster answer stamped after a
hook event already reflects it, and one stamped before is older and
dropped. The comparison uses `at` whether or not the current observation
is valid: `SessionEnd` stores a no-opinion observation at its own `at`, so
a roster answer stamped before it and delivered after is still dropped.

A roster answer with no opinion for an instance (no entry, ambiguous cwd,
unknown status, or a failed query) leaves a hook observation alone and
turns a roster observation into no opinion at the query's `at`. The second
half keeps today's rule that a failed query clears the roster: a
roster-sourced status must not outlive the roster that produced it.

**Reasons.** A roster observation with the same status as a current hook
observation keeps the hook's reason: the hook names the tool
(`permission: Bash`), the roster only the kind of wait (`permission
prompt`). A `Notification` that arrives while the session is already
Prompting keeps the current reason too; it is the delayed, generic twin of
the `PermissionRequest`.

When `claudeState` holds no valid observation, the screen ladder decides, as
today. When it does, `maybeRedetect` is suppressed, as the roster already
does, and `paneDirtyMsg`'s Ready→Running promotion is skipped: the report
owns the status, and output alone (a repaint on a focus change) says
nothing new. `adoptClaudeStatus` sets or clears `Instance.WaitReason` with the same
lifetime rule as `adoptRosterStatus` today, and both status paths
(`statusDetectedMsg`, `metadataReadyMsg`) keep calling the one choke point.

### 4. Delivery

**Trailing requests on `pollGate`.** `expedite()` only resets the interval:
a trigger that lands while a job is in flight waits for the next health
tick, up to 3s on the emulator path. Here that in-flight job started
*before* the event, so the merge rule discards its answer. `pollGate` gains
`request()`: expedite plus a `pending` mark. After `deliverGated` has routed
the inner message, a gate still marked pending is cleared and the job is
dispatched again at once through `m.redispatch(kind)`, which maps a kind to
its `maybe…` dispatcher on the Update goroutine. The in-flight guard is
unchanged, so jobs never overlap, and repeated requests during one flight
collapse into a single follow-up.

**Hook scans.** One gate, `gateHookScan` (renamed from `gateSubagent`), with
its interval lowered from 3s to 250ms. A single gate keeps the in-flight
guard: `Scan` converts `.json` to `.ev` by rename, so two overlapping scans
of one folder must never run. A hooked instance is one whose launch ID is
not the `noHooksLaunchID` sentinel; a restored instance that has not yet
adopted its folder's ID (empty launch ID) counts as hooked.

| Trigger | Call | Why |
|---|---|---|
| `paneDirtyMsg` for a hooked instance | `maybeHookScan` (interval honoured) | the spinner keeps output flowing while Claude works, so `UserPromptSubmit` is read within about 250ms |
| `paneQuietMsg` for a hooked instance | `request(gateHookScan)` | `Stop` and `PermissionRequest` arrive as output settles; the request guarantees a scan after the quiet even when one is in flight or the interval has not elapsed, so they are read about 0.5–0.6s after the event |
| health tick | `maybeHookScan` | backstop |

A warm scan is one `readdir` per instance and skips retained `.ev` files by
name.

**Roster queries.** The roster keeps its 3s cadence on the health tick and
gains two triggers:

| Trigger | Call | Why |
|---|---|---|
| a hook scan result that changed any instance's observation | `request(gateRoster)` | the answer, stamped after the event, confirms or corrects it: an intermediate `Stop` (a teammate reply the lead is about to pick up) becomes Running again about 100–200ms later |
| `paneDirtyMsg` for an instance whose status is Prompting | `maybeRosterQuery` under `promptingRosterSpacing` (500ms) instead of the 3s interval | on approval the dialog disappears, which is output; the roster reports `busy` and the card becomes Running without waiting for the next hook |

The Prompting trigger uses a spacing, not `request()`: a Prompting instance
whose roster answer never changes it (no entry, failed query) would
otherwise run back-to-back queries for as long as it produces output. At
~100ms per call this adds a few queries per turn.

**Remaining gaps, with no working roster.** An answered prompt stays
Prompting until the next hook event, and a lead that resumes after a
teammate reply with no event shows Ready until its next `Stop`.

### 5. Exact resume

**Persistence.** `CurrentSchemaVersion` 6 → 7. `InstanceData` gains
`claude_session_id` and `claude_transcript_path` (both `omitempty`). The
v6 → v7 step in `session/storage_migrate.go` needs no payload change. The
`cmd/workspace_migrate_shape_test.go` fixture and `loom workspace migrate`'s
mirror struct both gain the fields; without that the migrate command, which
writes through its own mirror, would drop them. Values are written at the
existing save points, with no new write path. A crash before the next save
falls back to `--continue`.

**Relaunch.** A new `BuildResumeCommand(program, sessionID, transcriptPath)`
beside `BuildRecoveryCommand`, which keeps producing `--continue`; the
adapter gains `ApplyResumeFlag` (a no-op for non-Claude agents):

1. If `program` already carries `--continue` or `--resume`, return it
   unchanged (as today).
2. If `sessionID` matches a strict UUID pattern (it is inserted into a shell
   string) and `os.Stat(transcriptPath)` succeeds, insert
   `--resume <sessionID>`.
3. Otherwise insert `--continue` (as today).

The transcript check exists because Claude deletes transcripts after
`cleanupPeriodDays` (default 30). Without it, a session paused for longer
would relaunch with `--resume` on a missing transcript, Claude would exit at
once, and the health tick would pause the instance again.

`recoveryLaunch()` passes the instance's values, so every relaunch path gets
this: `CrashRestart`, `startFreshWithRecovery`, and the workspace-terminal
`Restart`. `resetSubagentLaunch` (renamed `resetHookLaunch`) clears `obs`
and `lastMessage` at each new launch but **not** `sessionID` or
`transcriptPath`. The recovery launch reads them first, and the new launch's
`SessionStart` replaces them.

### 6. Cards

`lastMessage` is valid from its `Stop` until the next `UserPromptSubmit` or
`PermissionRequest`. The window matters for Prompting: a `PermissionRequest`
comes mid-turn, when the stored message belongs to the previous turn, and
the live tail shows the dialog the user must answer.

- `BuildCardData` fills `TailLines` from the message (`MessageTailLines`)
  instead of the screen when the message is valid and the status is not
  Running or Loading, so the renderers need no change.
- Lines are the message's last non-empty lines: as many as the overview
  card's existing tail (`overviewCardTailLines`), and the last one in the
  rail. Code fence lines (```` ``` ````) are
  dropped and leading heading `#`s trimmed, with no other markdown handling.
- Every line goes through `sanitizeCardText`; this is model-written text.
- This works on the snapshot path, which has no emulator screen today.

`CardData.WaitReason` is now also fed by hook observations, not only the
roster.

### 7. Failure handling

| Situation | Behaviour |
|---|---|
| Launched without hooks (Windows, the user's own `--settings`, `'` in the config path) | no hook observations; roster, then screen ladder, as today |
| Unknown event name | dropped by `ParseEvent` (`ErrUnknownEvent`), as today |
| Unknown `Notification` type or `SessionStart` source | no status; `SessionStart` still records the ID |
| Filename prefix does not parse | `At` from modification time |
| Result from an earlier launch | dropped by the launch-ID check, as today |
| Folder vanishes mid-run (`ErrNoHooks` for a real launch ID) | as today for subagent rows; also clear `obs` and `lastMessage` |
| Paused / Loading / Recoverable / not started | never scanned or driven (`subagentLive`, `statusEligible`) |
| `--resume` target unusable | `--continue` (§5) |

### 8. Config

`claude_subagent_tracking` keeps its JSON key and its "Track Subagents" row
in the Claude Preferences overlay, but no longer decides whether hooks are
installed. With it off, hooks are still installed and scanned for status,
session ID and last message; the tracker still runs, and its rows are not
shown: `Instance.Subagents` returns nil while it is off, so the setting
applies at once.

## Testing

- **`session/hooks`:** `ParseEvent` for each new event from the probe's
  captured payloads, kept as fixtures under
  `session/hooks/testdata/probe-2.1.280/` with home and scratch paths
  scrubbed; `At` from the filename and from modification time; `Compact`
  round trip including the 4 KB cap; the moved tests pass unchanged.
- **`claudeState` (table tests):** newest wins across sources in both
  directions; a roster answer with no opinion leaves a hook observation
  alone and voids a roster one; `SessionEnd` gives no opinion and still
  drops an older roster answer delivered after it; the `agent_id` filter and the `PermissionRequest` exception;
  `Stop` with a running `subagent` task is Running, with only teammate or
  `shell` tasks is Ready, with a missing list is Ready; compact
  `SessionStart` records the ID but no status; only `SessionStart` sets the
  ID (a `SessionEnd` carrying another ID changes nothing); `lastMessage`
  validity window; `sessionID` follows `/clear`; the probe's event
  sequences (plain turn, permission prompt, background subagent, teammate,
  `/clear`, resume) replayed in order give the expected status after each
  event; replay rebuilds the same state as incremental application.
- **`BuildRecoveryCommand`:** resume with the transcript present; continue
  when it is missing, the ID is empty or malformed; user-supplied
  `--continue` / `--resume` left alone.
- **`app/pollgate`:** `request()` during a flight re-dispatches exactly once
  after delivery; several requests during one flight collapse into one; a
  request while idle dispatches at once even inside the interval; a
  delivery with nothing pending re-dispatches nothing.
- **`app`:** dirty (interval honoured), quiet (`request`) and backstop
  triggers through `gateHookScan`; a scan that changed an observation
  requests a roster query; dirty on a Prompting instance queries the roster
  no more often than `promptingRosterSpacing`; both status paths call
  `adoptClaudeStatus` (extending `app/roster_status_test.go`); regression:
  a roster query started before `Stop` and delivered after it does not flip
  the card to Running; regression: an intermediate `Stop` followed by a
  newer `busy` roster answer shows Running; `maybeRedetect` is suppressed
  while a hook observation is valid.
- **`ui`:** `BuildCardData` uses `LastMessage` on Ready, the live tail on
  Running and on `PermissionRequest` Prompting; fence and heading handling;
  sanitization.
- **Storage:** v6 → v7 migration; shape fixture; the migrate mirror keeps
  the new fields.
- **`tools/fakeagent`:** the claude persona reads `--settings` and runs the
  hook commands it registers with payloads in the real shape
  (`SessionStart`, `UserPromptSubmit`, `PermissionRequest` on its pending
  prompt, `Stop` with `last_assistant_message`, `SessionEnd`), honours
  `--resume`, and answers `claude agents --json` with `[]`. An e2e test
  drives status transitions through hooks with no roster opinion.
- **Opt-in real-Claude contract test (`LOOM_TEST_REAL_CLAUDE=1`):** extended
  to assert each [probe result](#probe-results), including that the roster
  has already moved when a `Stop` and a `PermissionRequest` are stamped, so
  a CLI change that breaks one fails loudly.
- `CC=clang CGO_ENABLED=1 go test -race ./...`.

## Documentation

- CLAUDE.md: the "Claude's roster outranks the pane scraper" gotcha becomes
  "Claude status: hooks and roster, newest observation wins", covering why
  there is no grace window (the roster leads the hooks), `Stop` meaning end
  of turn, the parent-only rule, the delivery and roster triggers, and the
  gaps that remain without a roster. The roster comments' ~380ms figure
  becomes the measured ~100ms. The `session/subagent/` bullet points at
  `session/hooks`. The `claude_subagent_tracking` description changes per §8.
  Schema v7 fields are noted under Persistent State.
- USAGE.md: the Track Subagents setting's new meaning.

## Out of scope

- The argv-based launcher (D) and removing non-Claude support and the screen
  ladder (E).
- Registering `PreToolUse` / `PostToolUse` / `PostToolBatch`.
- Replacing the file-drop transport with a socket or file watcher.
- Token or cost display from transcripts.
- Fixing the user-supplied `--settings` collision (such sessions still launch
  without hooks).

## Risks

- **Undocumented behaviour drift.** The design relies on payload fields and
  firing order from the docs. The contract test pins them, but it is opt-in
  and costs money, so a break may surface first in use. Every rule fails to
  "no opinion", so the worst case is today's behaviour.
- **The roster stops leading the hooks.** Newest-wins with no grace window
  relies on Claude publishing its status before it runs the hook. If a
  future CLI reversed that, a roster answer stamped just after `Stop` could
  report `busy` and hold a finished session at Running until the next
  roster query (at most 3s later). The contract test asserts the ordering.
- **Gaps without a roster.** Covered in §4; accepted for this spec.
- **Scan rate.** Dirty-triggered scans run up to four times a second while
  any session works. A warm scan is one `readdir` per hooked instance, but a
  fleet of many sessions should be measured before merge.

## Probe results

Probed on Claude Code 2.1.280, 2026-09-23. The 34 captured payloads are the
fixtures named under Testing.

| # | Assumption | Result |
|---|---|---|
| 1 | `PermissionRequest` fires as the dialog appears, with `tool_name`, without `agent_id` for the parent's own tools | Holds. The matching `Notification` came 6.0s later with the generic message "Claude needs your permission" |
| 2 | A subagent's permission prompt raises a `PermissionRequest` in the parent's folder | Holds, carrying the subagent's `agent_id` |
| 3 | `Stop` carries `last_assistant_message` | Holds |
| 4 | `SessionStart` at startup; `/clear` gives a new `session_id` | Holds; `/clear` also sends `SessionEnd` (`reason: "clear"`) first |
| 5 | `--resume <id>` sends `SessionStart` (`source: "resume"`) | Holds, with the same `session_id`; an unknown ID sends only `SessionEnd` |
| 6 | `UserPromptSubmit` fires for every prompt, without `agent_id` | Holds; it also fires when a background subagent's result resumes the parent |
| 7 | Subagents and teammates write no parent-level events without `agent_id` | Holds |
| 8 | The roster lags `Stop` (to size a grace window) | Does not hold: the roster leads every hook measured by 0–90ms, so the grace window was removed |

The probe also found that `Stop` fires mid-work (see Findings), which
changed the `Stop` mapping and added the roster triggers.
