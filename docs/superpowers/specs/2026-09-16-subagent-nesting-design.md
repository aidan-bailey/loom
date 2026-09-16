# Subagent Nesting

**Date:** 2026-09-16
**Status:** Approved design
**Verified against:** Claude Code 2.1.270 (`claude-code-2.1.270/bin/.claude-unwrapped`),
by live probes on 2026-09-16 (a `claude -p --model haiku` run and two
interactive haiku sessions on a private tmux server)

## Problem

Claude Code's agent view lists the subagents and agent-team teammates a
session has spawned, with their working/idle state. Loom shows only the
parent session. A session that has fanned out into five agents looks the
same on a loom card as one working alone, so the user cannot tell from the
fleet view how much work is in flight or whether a team is waiting on
anything.

This is the second of three roster features (the first, wait reasons on
prompting cards, shipped in `4d1e6b9`; the third, launching background agents
from loom, is separate).

## Findings

### The roster cannot supply this

`claude agents --json` is process-level: every entry has a `pid` and `cwd`.
Subagents and teammates run inside their parent process and never appear
in it.

### Transcripts cannot say whether an agent is running

Subagent transcripts live at
`~/.claude/projects/<slug>/<sessionId>/subagents/agent-<agentId>.jsonl`, with
a small `agent-<agentId>.meta.json` beside each. They can list agents, but
their state cannot be read from them reliably:

- Claude writes each content block of a response as its own record, sharing
  one `message.id`. A text-only final record is normal mid-turn: 446
  occurrences across 60 finished transcripts.
- `stop_reason` is absent from ~60% of response groups (2,250 of 3,530), and
  many finished transcripts end on a record without one, including
  2.1.251 and 2.1.258.

### Metadata comes in several shapes

Across 175 real metadata files there were five key sets. The two that matter:

```json
{"agentType":"Explore","description":"probe sync","toolUseId":"toolu_…","spawnDepth":1}
{"agentType":"impl-t9","description":"Implement Task 9","name":"impl-t9","spawnDepth":0,
 "model":"opus","taskKind":"in_process_teammate","teamName":"session-a5da4fc1",
 "color":"red","planModeRequired":false,"permissionMode":"auto"}
```

157 of the 175 were agent-team teammates (`taskKind: "in_process_teammate"`),
which have no `toolUseId`. Some files add `requestShape: "background"` and
`requestNonInteractive`.

### Hooks report state authoritatively

Claude Code's documented hooks fire for every relevant transition. Verified
payloads (common fields `session_id`, `transcript_path`, `cwd`, `prompt_id`
omitted):

```json
{"hook_event_name":"SubagentStart","agent_id":"a91a01359700cebc7","agent_type":"Explore"}
{"hook_event_name":"SubagentStop","agent_id":"a91a01359700cebc7","agent_type":"Explore",
 "agent_transcript_path":"…","last_assistant_message":"pong","stop_hook_active":false,
 "background_tasks":[{"id":"a91a01359700cebc7","type":"subagent","status":"running",
                      "description":"probe sync","agent_type":"Explore"}]}
{"hook_event_name":"TeammateIdle","teammate_name":"probe-mate","team_name":"session-07e9a557"}
{"hook_event_name":"Stop","last_assistant_message":"DONE","stop_hook_active":false,
 "background_tasks":[{"id":"tkva0drgj","type":"teammate","status":"running",
                      "description":"Reply with the word pong. Use no tools."}]}
{"hook_event_name":"SessionEnd"}
```

Verified behaviour:

1. **Hooks passed with `--settings` merge** with project and user hooks; a
   project-local `SubagentStart` hook fired alongside the `--settings` one.
2. **`agent_id` names the metadata file**: `agent-<agent_id>.meta.json`.
3. **Teammates cycle.** First task: `SubagentStart` → `SubagentStop` →
   `TeammateIdle`. Re-tasked by the lead: `SubagentStart` fires again with the
   **same** `agent_id`, then `SubagentStop` → `TeammateIdle`. A teammate's
   `agent_type` is its name; its `agent_id` has the form `a<name>-<hash>`.
4. **Teammate shutdown** (lead sends `shutdown_request`): `SubagentStart` →
   `SubagentStop` with **no** `TeammateIdle` after it, then the lead's next
   `Stop` carries a `background_tasks` list with no teammate entries.
   Claude's own agent list drops the teammate at the same point.
5. **Teammate entries in `background_tasks` can't be matched to agents:** their
   `id` is a task ID (`tkva0drgj`) and their `description` is the spawn
   prompt, not the metadata description. Plain-subagent entries use the
   `agent_id` as `id`.
6. **Internal helper agents** produce `SubagentStop` events with no
   preceding `SubagentStart` and no metadata file (three in the shutdown
   probe).
7. **`SessionEnd`** fires when the session exits.
8. **`-p` mode never creates teammates.** A named agent there becomes a plain
   background subagent, so teammate behaviour can only be tested
   interactively. The trust dialog defaults to "No, exit" for folders under
   `/tmp`.

## Decisions

Made with the user during design:

- **State source:** hooks, with metadata files only for labels.
  Transcript-derived state was rejected because it can only guess.
- **Placement:** a count in the rail status line, and rows in overview cards.
  No workbench tab.
- **Card height:** overview cards stay at their fixed 7 lines; subagent rows
  take the tail's place.
- **Lifetime:** live agents only (working and idle). Finished plain
  subagents and shut-down teammates disappear.

## Design

### 1. Launch and transport

**Config.** `ClaudeSubagentTracking *bool` (`json:"claude_subagent_tracking,omitempty"`),
read via `Config.SubagentTrackingEnabled()`, where nil means enabled. It's
toggled from the Claude Preferences overlay (`ui/overlay/claudePreferences.go`),
following the `ClaudeRemoteControl` pattern. The app mirrors it into the session
package with `session.SetSubagentTrackingEnabled`, an `atomic.Bool` updated at
the same points as `SetLoomContextEnabled`. It is not added to the per-session
Session Launch Options overlay.

**Per-instance folder.** `{ConfigDir}/hooks/<tmux.ToLoomTmuxName(title)>/`,
mode 0700:

```
hooks/<name>/
  settings.json   hook definitions passed to --settings
  launch-id       fresh value written at every launch
  events/         one file per hook event
```

It deliberately lives outside `worktrees/`: `DiscoverOrphans` walks that tree
and descends into every directory lacking the `_<hex>` suffix. Workspace
terminals get a folder too, since the key is not tied to a worktree.

**Injection.** `Instance.applyLoomContext` becomes `Instance.launchProgram`,
used by all three tmux launch paths (`instance.go` fresh start, 
`startFreshWithRecovery`, `CrashRestart`). It applies the loom-context flag,
then `prepareSubagentHooks`, which:

1. returns the program unchanged when tracking is disabled, the program is
   not Claude, `runtime.GOOS == "windows"`, the program string already
   contains `--settings` (logged once at info), or the folder path contains a
   single quote;
2. empties and recreates the folder, writes `settings.json` and a new
   `launch-id`, and resets the instance's tracker (§2);
3. on any write error, logs a warning and returns the program unchanged, so
   the session launches untracked;
4. otherwise inserts `--settings '<dir>/settings.json'` via the Claude
   adapter (`ApplySettingsFlag`, same quoting and idempotency as
   `ApplyLoomContextFlag`).

The flag is applied at launch only and never written into `Program`, so
turning tracking on or off takes effect at the next launch or resume.

**Hooks registered:** `SubagentStart`, `SubagentStop`, `TeammateIdle`, `Stop`,
`SessionEnd`, each with the same command:

```sh
f='<dir>/events/'"$(date +%s%N)-$$"; { cat > "$f.tmp" && mv "$f.tmp" "$f.json"; } 2>/dev/null || cat >/dev/null
```

- The rename within one folder is atomic, so the scan never reads a
  partially written file.
- The command always exits 0 and always consumes its input, including when
  the folder has been deleted (an instance killed mid-run) or the disk is
  full.
- No loom binary is involved, so an old Nix store path being garbage
  collected cannot break a running session's hooks.

**Collection.** `subagentScanCmd` runs on the health tick with its own
`subagentInterval` (3s), `subagentInFlight` and `lastSubagentScan`, following
`maybeRosterQuery` exactly: nothing is set when nothing is dispatched, and
the result message clears the in-flight flag on every delivery, including
errors. Only Claude instances that have a hooks folder and a live status are
scanned. For each one, the command:

1. reads `launch-id`;
2. lists `events/`, deletes `.tmp` files older than one minute, and orders
   `*.json` files by modification time, then name (`%N` is unsupported on
   macOS, so the name guarantees uniqueness and mtime gives order);
3. reads and deletes at most 500 files, oldest first. Files over 1 MiB,
   unparseable files and unknown event names are deleted with a debug log;
4. reads metadata for every `SubagentStart` in the batch plus every agent ID
   the instance reports as still missing metadata. The path is
   `strings.TrimSuffix(transcript_path, ".jsonl") + "/subagents/agent-<id>.meta.json"`;
5. returns `subagentScanMsg{instance title, launchID, events, meta}`.

### 2. Tracking state

**Package `session/subagent`** holds event types and parsing, metadata parsing,
and `Tracker`. It has no dependency on tmux, the UI or the app.

```go
type Kind int  // Plain, Teammate
type State int // Working, Idle, Stopping

type Agent struct {
    ID          string
    Name        string // meta "name", else meta "agentType", else event agent_type
    Description string // meta "description"
    Kind        Kind   // Teammate iff meta taskKind == "in_process_teammate"
    State       State
    spawnSeq    int
    stoppedSeq  int  // event sequence of the latest SubagentStop
    hasMeta     bool
}
```

**Transitions**:

| Event | Plain | Teammate |
|---|---|---|
| `SubagentStart` | create or update → Working | create or update → Working |
| `SubagentStop` (known ID) | remove | → Stopping |
| `SubagentStop` (unknown ID) | ignore | ignore |
| `TeammateIdle` (match `Name`) | — | Stopping/Working → Idle |
| `Stop` with valid `background_tasks` | remove plain agents whose ID is absent from its `subagent` entries | reconcile by count (below) |
| `Stop` with `background_tasks` missing or malformed | nothing | nothing |
| `SessionEnd` | clear | clear |

- **Rows are only created by `SubagentStart`.** Internal helpers (finding 6)
  never create one.
- **`TeammateIdle` matches on `Name`.** Until metadata arrives, `Name` is the
  event's `agent_type`, which for a teammate is its name (finding 3), so an
  idle event is matched even before the agent is visible.
- **Teammate reconciliation at `Stop`:** let `live` be the number of
  `type == "teammate"` entries with `status == "running"`. If more teammates
  are tracked than `live` (counting hidden ones, since the list counts
  every teammate), remove the excess: Stopping teammates first, then
  those with the highest `stoppedSeq`. Remaining Stopping teammates become
  Idle. When `live == 0`, all teammates are removed, which is exact.
- **Only the parent's `Stop` reconciles, never `SubagentStop`.** A foreground
  subagent blocks the parent's turn, so none can be running when the parent
  stops. A `SubagentStop` list can arrive while a parallel foreground agent is
  still running. The parent-`Stop` reconciliation also repairs Start/Stop
  pairs processed in the wrong order.
- **Hidden until metadata exists.** An agent without metadata stays tracked
  but is left out of `Visible()`. `MissingMeta()` lists such IDs with their
  metadata paths for the next scan.

**API.**

```go
func (t *Tracker) Apply(events []Event, meta map[string]Meta)
func (t *Tracker) Visible() []View        // working first, then idle; spawnSeq order within each
func (t *Tracker) MissingMeta() []MetaRef
func (t *Tracker) Reset()
```

`View` is `{Name, Description string; Idle bool}`; Stopping is shown as idle.

**Ownership.** `Instance` holds the tracker and its current `hookLaunchID`
behind `mu`:

- `ApplySubagentScan(launchID, events, meta)` does nothing if `launchID`
  differs from the instance's current value, which drops results from
  before a relaunch.
- `Subagents() []subagent.View` returns a copy for rendering.
- `SubagentsMissingMeta()` is read in `Update` when dispatching a scan.

The tracker is mutated only from `Update` (the scan handler) and from
`prepareSubagentHooks` during launch, both under `mu`. `Subagents()` returns
nil unless the instance is Running, Ready, Prompting or Loading, so a Paused,
Recoverable or Deleting instance shows no rows without any status-change
hook. The next launch resets the tracker. None of this is persisted, so no
`SchemaVersion` bump is needed.

### 3. Rendering

`CardData.Subagents []SubagentRow` (`Name`, `Description string; Idle bool`),
filled by `BuildCardData` from `inst.Subagents()`. When the list is empty,
every density renders byte-identically to today.

**Rail (`DensityRail`).** The status line gets a suffix; the overview
status line does not:

| Live agents | Suffix |
|---|---|
| 1, working | ` · 1 agent` |
| 1, idle | ` · 1 agent (idle)` |
| n > 1, all working | ` · n agents` |
| n > 1, k idle | ` · n agents (k idle)` |

The existing end-truncation drops the suffix before the status on narrow
rails. The word "agents" is used rather than `⑂` (U+2442), which is in a
Unicode block many terminal fonts omit.

**Overview (`DensityCard`).** When there are live agents, the two tail slots
in `renderOverviewCard` are filled as follows:

| Live agents | Slot 1 | Slot 2 |
|---|---|---|
| 1 | `└ ` row | last tail line, if any |
| 2 | `├ ` row | `└ ` row |
| n > 2 | `├ ` row for the first agent | `└ +<n-1> more · <w> working · <i> idle` (counts cover the hidden agents; zero-count clauses omitted) |

A row is `<tree> <glyph> <name padded> <text>`:

- glyph: `✻` when working, `◦` when idle. `◦` is not East-Asian-ambiguous
  width; Claude's own `◯` is.
- name column: the width of the longest name shown, capped at 14.
- text: the description when working, `idle` when idle.
- colours: tree in `Rule`, working glyph in `OK`, idle glyph and text in
  `Dim`, name in `Text`. Styles are built at render time from the theme
  role variables, as `renderOverviewCard` already does.

Every line is truncated to the inner width, so the `overviewCardHeight`
invariant holds.

**Unchanged:** attention accent and overview sort order, the agent pane title,
the workbench, focus layout, and mouse handling. A subagent's permission
prompt appears in the parent session, so the existing roster wait reason
already covers it.

### 4. Failure handling

When unsure, show nothing. A missing row is acceptable; a wrong one is not.

| Where | Failure | Result |
|---|---|---|
| Launch | disabled / non-Claude / Windows / existing `--settings` / `'` in path | launch without hooks |
| Launch | writing the folder fails | launch without hooks, warning logged |
| Hook | folder gone, disk full | exit 0, input consumed |
| Scan | file unparseable, over 1 MiB, or unknown event | delete, debug log |
| Scan | more than 500 files | oldest 500 now, the rest next tick |
| Scan | folder or `launch-id` unreadable | no result for that instance |
| Scan | leftover `.tmp` older than 1 minute | delete |
| Tracker | unknown stop ID, missing metadata, unknown `taskKind` | ignore, hide, treat as Plain |
| Tracker | `background_tasks` missing or malformed | no reconciliation |
| App | `launch-id` mismatch | drop the result |
| Cleanup | instance killed (`Instance.Kill`) | remove its folder |
| Cleanup | workspace load | remove a folder only if no instance uses it **and** its tmux session is not alive |

## Testing

- **`session/subagent`** (table-driven; fixtures are the probe payloads
  above): plain lifecycle; teammate working → idle → re-tasked → idle; full
  shutdown via `Stop` with no teammates; partial shutdown removes Stopping
  teammates first; unknown-ID stops ignored; missing metadata hidden until
  supplied; Start/Stop processed out of order corrected by the next `Stop`;
  malformed or missing `background_tasks` leaves state untouched;
  `SessionEnd` clears everything; `Visible` ordering.
- **Hook settings:** golden JSON for `settings.json`; the command with a
  folder path containing spaces; each "launch without hooks" condition.
- **Scan** (temporary folder): mtime-then-name order; 500 cap; oversized,
  unparseable and unknown files deleted; fresh `.tmp` left alone and stale
  `.tmp` removed; missing folder; metadata paths derived from
  `transcript_path`.
- **Launch:** `--settings` present for the fresh-start, recovery and
  crash-restart paths; absent when disabled; `Program` never modified.
- **App:** a scan result reaches `inst.Subagents()`; a stale `launch-id` is
  dropped; `subagentInFlight` is cleared on every delivery including errors;
  nothing is armed when there is nothing to scan (matching the roster tests).
- **UI:** rail suffix variants; overview rows for 0, 1, 2 and 4 agents;
  `TestOverview_UniformCardHeight` with subagents; line widths within bounds;
  byte-identical output with no subagents.
- **Real Claude** (`LOOM_TEST_REAL_CLAUDE=1`, skipped otherwise, never in CI):
  replays the haiku probe on a private tmux socket (`tmux -L`) and asserts the
  event sequences in findings 3 and 4. This guards the observed-but-undocumented
  parts (`background_tasks`, teammate event order) against CLI changes.
- **Race detector** (`CGO_ENABLED=1 CC=clang go test -race`) on `app`,
  `session` and `session/subagent`.

## Documentation

- CLAUDE.md: a Gotchas entry covering the hooks folder layout and its
  location outside `worktrees/`, the parent-`Stop`-only reconciliation rule,
  launch-id gating, and fail-closed behaviour; `session/subagent` in Key
  Packages; `claude_subagent_tracking` under Persistent State.
- USAGE.md: the setting under Claude Preferences, and what the rail suffix
  and overview rows mean.

## Out of scope

- A workbench agents tab or per-agent detail view.
- Listing finished agents or keeping history.
- Nesting deeper than one level (`spawnDepth` > 1 renders flat).
- Matching sessions by tmux pane via `~/.claude/sessions/<pid>.json` (a
  better key than `cwd`; a separate change).
- Tracking for sessions launched before this ships, until they are resumed.
- Windows.

## Risks

- **Undocumented event details.** Hooks are documented; `background_tasks`
  and the teammate event order were observed, not specified. If they change,
  the fail-closed rules hide rows rather than showing wrong ones, and the
  opt-in real-Claude test detects the change.
- **Partial teammate shutdown is inferred.** With several teammates alive,
  choosing which ones were removed relies on the Stopping heuristic. It is
  exact for the common case of shutting down all teammates.
- **Up to 3s delay**, the same as roster status.
