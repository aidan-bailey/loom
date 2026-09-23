# Claude Hook Events: Status, Resume and Last Message

**Date:** 2026-09-23
**Status:** Approved design
**Sourced from:** the Claude Code hooks reference (code.claude.com/docs/en/hooks,
read 2026-09-23) and loom's own subagent-nesting findings
(`2026-09-16-subagent-nesting-design.md`). Installed CLI: Claude Code
2.1.280. The payload facts below have **not** been probed live yet; see
[Assumptions to probe](#assumptions-to-probe), which gates planning.

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
hook evidence of work for its whole duration.

### Subagents run inside the parent process

From the subagent-nesting findings: subagents and teammates run in their
parent's process and report through `SubagentStart` / `SubagentStop` /
`TeammateIdle`. A subagent's permission prompt appears in the parent's UI.

## Decisions

1. **Scope A+B+C as one spec**, because they share the event path.
2. **Status latency target: under about 1s.**
3. **Newest observation wins.** Hooks and the roster are peer sources, each
   stamped with the time it was observed. Rejected: hooks only trigger a
   roster query (status would still depend entirely on the roster), and
   hooks only with no roster (the prompt-answered gap would need
   screen-based patching).
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
- **`session/subagent` (smaller).** `Tracker` and `Meta` only, consuming
  `hooks.Event`. No behaviour change. `realclaude_test.go` moves with the
  code it exercises.
- **`session`.** A `claudeState` on `Instance` (§2), updated by
  `ApplySubagentScan`, renamed `ApplyHookScan`. `SubagentScanRequest` becomes
  `HookScanRequest`.
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
| `LastAssistantMessage` | `last_assistant_message` (Stop), capped at 4 KB |

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
result resets `obs` and `lastMessage` first, then applies the full history;
an incremental result applies on top. `sessionID` and `transcriptPath` are
never reset by a replay, since a replay can only replace them with the same
or newer values.

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
| `Stop` | Ready | set `lastMessage`, mark valid |
| `SessionEnd` | clear `obs` (no opinion) | |

If a launch has no parent `SessionStart`, the latest parent event's
`session_id` is used. `PreToolUse` and `PostToolUse` are not registered: they
fire on every tool call, and `PostToolUse` includes the tool's whole output,
which the hook would write to disk.

**Roster observations** are stamped with `at` taken inside
`rosterQueryCmd`'s Cmd immediately before the subprocess starts, carried on
`rosterReadyMsg`. `adoptClaudeStatus` offers the instance's roster entry
(when there is one) as an observation, then applies whatever `claudeState`
holds.

**Merge rule.** A new observation replaces the current one when its `at` is
newer, except that a roster observation replaces a *hook* observation only
when it is at least `hookGrace` (1s) newer. That keeps a roster query started
just after `Stop`, before Claude updates the status it publishes, from
flipping the card back to Running for a full roster interval. A roster
answer with no opinion (no entry, ambiguous cwd, unknown status, failed
query) proposes nothing and leaves the current observation alone.

When `claudeState` holds no valid observation, the screen ladder decides, as
today. When it does, `maybeRedetect` is suppressed, as the roster already
does. `adoptClaudeStatus` sets or clears `Instance.WaitReason` with the same
lifetime rule as `adoptRosterStatus` today, and both status paths
(`statusDetectedMsg`, `metadataReadyMsg`) keep calling the one choke point.

### 4. Delivery

One gate, `gateHookScan`, with its interval lowered from 3s to 250ms. A
single gate keeps the in-flight guard: `Scan` converts `.json` to `.ev` by
rename, so two overlapping scans of one folder must never run. Triggers:

1. `paneDirtyMsg` for a hooked Claude instance: one whose launch ID is not
   the `noHooksLaunchID` sentinel. A restored instance that has not yet
   adopted its folder's ID (empty launch ID) counts as hooked. The spinner keeps output
   flowing while Claude works, so `UserPromptSubmit` is read within about
   250ms.
2. `paneQuietMsg`. `Stop` and `PermissionRequest` arrive as output settles,
   so they are read about 0.5–0.6s after the event.
3. The health tick, as the backstop.

A trigger the gate refuses sets `m.hookScanPending`. When a scan result
lands, the handler checks it and, if set, returns a `tea.Tick` for the rest
of the interval. That tick delivers a message which re-dispatches through
the gate, so the last event of a burst is never left for the backstop. The
tick is a single-message Cmd, as `dispatchGated` requires. A warm scan is one
`readdir` per instance and skips retained `.ev` files by name.

**Known gap.** Answering a prompt produces no event. With a working roster,
Prompting becomes Running within about one roster interval (3s). Without
one, the card stays Prompting until the next hook event.

### 5. Exact resume

**Persistence.** `CurrentSchemaVersion` 6 → 7. `InstanceData` gains
`claude_session_id` and `claude_transcript_path` (both `omitempty`). The
v6 → v7 step in `session/storage_migrate.go` needs no payload change. The
`cmd/workspace_migrate_shape_test.go` fixture and `loom workspace migrate`'s
mirror struct both gain the fields; without that the migrate command, which
writes through its own mirror, would drop them. Values are written at the
existing save points, with no new write path. A crash before the next save
falls back to `--continue`.

**Relaunch.** `BuildRecoveryCommand(program, sessionID, transcriptPath)`:

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

- `CardData` gains `LastMessage []string`. `BuildCardData` uses it in place
  of `TailLines` when it is valid and the status is not Running.
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
shown. `Config.SubagentTrackingEnabled()` moves from the launch path to the
card builder.

## Testing

- **`session/hooks`:** `ParseEvent` for each new event from captured
  payloads; `At` from the filename and from modification time; `Compact`
  round trip including the 4 KB cap; the moved tests pass unchanged.
- **`claudeState` (table tests):** newest wins; `hookGrace` both ways; a
  roster answer with no opinion changes nothing; `SessionEnd` clears; the
  `agent_id` filter and the `PermissionRequest` exception; compact
  `SessionStart` records the ID but no status; `lastMessage` validity
  window; `sessionID` follows `/clear`; replay rebuilds the same state as
  incremental application.
- **`BuildRecoveryCommand`:** resume with the transcript present; continue
  when it is missing, the ID is empty or malformed; user-supplied
  `--continue` / `--resume` left alone.
- **`app`:** dirty, quiet and backstop triggers through `gateHookScan`; the
  pending trailing tick; both status paths call `adoptClaudeStatus`
  (extending `app/roster_status_test.go`); regression: a roster query started
  before `Stop` and delivered after it does not flip the card to Running;
  `maybeRedetect` is suppressed while a hook observation is valid.
- **`ui`:** `BuildCardData` uses `LastMessage` on Ready, the live tail on
  Running and on `PermissionRequest` Prompting; fence and heading handling;
  sanitization.
- **Storage:** v6 → v7 migration; shape fixture; the migrate mirror keeps
  the new fields.
- **`tools/fakeagent`:** the claude persona reads the events directory from
  `--settings` and writes events in the real payload shape (`SessionStart`,
  `UserPromptSubmit`, `PermissionRequest` on its pending prompt, `Stop` with
  `last_assistant_message`). An e2e test drives status transitions through
  hooks with no roster.
- **Opt-in real-Claude contract test (`LOOM_TEST_REAL_CLAUDE=1`):** extended
  to assert each item under [Assumptions to probe](#assumptions-to-probe),
  so a CLI change that breaks one fails loudly.
- `CC=clang CGO_ENABLED=1 go test -race ./...`.

## Documentation

- CLAUDE.md: the "Claude's roster outranks the pane scraper" gotcha becomes
  "Claude status: hooks and roster, newest observation wins", covering
  `hookGrace`, the parent-only rule, the delivery triggers and the known
  prompt-answered gap. The `session/subagent/` bullet points at
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
- **`hookGrace` sizing.** 1s is a guess at how far Claude's published status
  lags its hooks. Too short brings back the post-`Stop` flicker; too long
  delays the roster's correction of a stale hook observation.
- **Stale Prompting without a roster.** Covered in §4; accepted for this spec.
- **Scan rate.** Dirty-triggered scans run up to four times a second while
  any session works. A warm scan is one `readdir` per hooked instance, but a
  fleet of many sessions should be measured before merge.

## Assumptions to probe

Verify on Claude Code 2.1.280 in a live interactive session on a private
tmux server (the subagent-nesting probe method) **before writing the plan**.
If any fails, amend this spec first.

1. `PermissionRequest` fires in interactive mode as the dialog appears, with
   `tool_name`, and without `agent_id` for the parent's own tool calls.
2. A subagent's permission prompt raises a `PermissionRequest` in the
   parent's folder (with or without `agent_id`).
3. `Stop` carries `last_assistant_message`.
4. `SessionStart` fires at startup with `source: "startup"`, and `/clear`
   produces a new `SessionStart` (`source: "clear"`) with a new
   `session_id`.
5. `claude --resume <id>` fires `SessionStart` with `source: "resume"`;
   record whether the `session_id` stays the same.
6. `UserPromptSubmit` fires for every interactive prompt, without
   `agent_id`.
7. Teammates and subagents never write parent-level `Stop`,
   `UserPromptSubmit` or `SessionStart` events without an `agent_id`.
8. The lag between a `Stop` hook and the roster reporting `idle` (sizes
   `hookGrace`).
