# Supervised Workspaces

**Date:** 2026-10-02
**Status:** Approved design, brainstormed with the user one section at a time.
Supervision became a workspace mode at the end of the session.
**Origin:** a day on the kermit repository in which one loom session acted
as a hand-made supervisor over seven per-issue sessions, coordinating them
through Claude Code's cross-session messages: six landings and seven issues
closed.

## Problem

On 2026-10-02 one kermit session supervised the others. It wrote no code.
It triaged the issues, briefed each new session, kept sessions out of each
other's files, checked every branch before it landed, and kept GitHub
current. The user started sessions, made the decisions and pushed.

The hub paid off where no single session could see:

- the landing order;
- a single report-schema bump covering four sessions' changes;
- a single toolchain refresh;
- a flag-order bug in one session's plan, caught from another's report;
- a host-wide out-of-memory incident, traced to one session.

The coordination itself was costly:

- **Spawn, then brief.** The user started each session by hand, then
  @-mentioned it so the supervisor could brief it. Sessions started without
  context, and restarts changed their names (one session three times), so
  the supervisor lost track of them.
- **Decisions.** About a dozen decisions reached the user as prompts in the
  supervisor's pane, some of them routine (who writes a doc) and some spread
  over several prompts. Workers also asked in their own panes, so decisions
  lived in several places.
- **Chatter and visibility.** Status travelled as messages and idle notices.
  The state lived in the supervisor's transcript and task list, so the user
  saw it only when the supervisor summarised.

The push gate worked and stays: the user pushes, and agents' permission
classifiers refuse to.

## Goals

1. **Visibility:** the user can read the state of every piece of work in
   loom without opening a transcript.
2. **Less load:** the user handles approvals, real decisions and pushes, and
   nothing else.
3. **Repeatable supervision:** a fresh supervisor follows the same
   discipline from the protocol text alone.

Non-goals: enforcing resource limits, agents other than Claude Code,
automatic pushes or merges, and answering questions from inside loom.

## Decisions

| # | Decision |
|---|---|
| 1 | Supervision is a **workspace mode**. In a supervised workspace, the **main session** supervises and every worktree session is a worker. |
| 2 | The supervisor **proposes** worker sessions; the user approves each with one key, and the brief is the worker's first prompt. |
| 3 | The supervisor makes routine calls itself and logs them: landing order, who does what, test and doc details, issue housekeeping. The user decides scope, priorities, anything irreversible or outward-facing, and anything that changes measurements or data. |
| 4 | **One inbox** in loom holds everything that waits on the user, from every session. The user answers in the asking session's pane. |
| 5 | The state of each piece of work **lives in loom**. Workers declare it and the supervisor reacts to changes. Messages carry content (briefs, reports), not status. |
| 6 | Claude Code only. |
| 7 | Loom owns the mode, work state, the inbox and proposals, through a small agent CLI and an `AskUserQuestion` hook, delivered in three stages. |

Rejected:

- **The protocol as Claude Code skills, with loom only displaying it.** Loom
  would parse Claude Code's private files, and the inbox could not show the
  question being asked.
- **Loom as a deterministic orchestrator.** The hub's value came from
  judgement that no rule anticipates.
- **Per-session roles chosen at creation**, superseded by decision 1.

## Design

### 1. The mode

`workspaces.json` gains a `mode` for each workspace: `""` (normal, as today)
or `supervised`. It is set with `loom workspace mode [name]
<normal|supervised>`, or with a toggle in the settings overlay (`S`).

**Roles derive from the mode.** In a supervised workspace the main session
(`IsWorkspaceTerminal`) is the supervisor, and every worktree session is a
worker. No per-instance role is stored. A normal workspace behaves exactly
as today.

**Switching.** Context is a launch argument, so a switch reaches each
session at its next launch. Turning the mode on offers to restart the main
session as the supervisor, or starts one if the workspace has none. Turning
it off keeps each instance's work state but hides it.

**Sessions started by hand** (`n`, `N`, `I`) in a supervised workspace are
workers, and the user's prompt takes the place of a brief. They appear on
the board, and the supervisor sees them through `loom board`. Proposals
(§7) are the usual route.

### 2. Context and environment

At launch loom composes the appended system-prompt file from three parts:

1. today's base context (`claude-loom-context.md`, or the workspace variant
   for the main session);
2. the role's protocol (§3);
3. the repo's extension `.loom/protocol/<role>.md`, when present.

It writes the result to `claude-loom-context-<role>.md` and passes that file
to `--append-system-prompt-file`. A normal workspace gets today's file,
unchanged.

Every launch in a supervised workspace also exports two variables into the
agent's environment: `LOOM_INSTANCE` (the instance title) and
`LOOM_HOOKS_DIR` (its hooks folder). The agent CLI (§4) uses them to know
who is calling and where to write.

### 3. Protocol text

There are two files, embedded with `go:embed`, each about a page of rules
and checklists.

**`supervisor.md`**

- **Role.** The supervisor doesn't implement. It owns priority,
  coordination, verification and issue housekeeping, and routes decisions.
- **Before proposing work.** Ask whether the work is *necessary* for the
  goal, not merely complete. Check its file overlap with active workers, any
  shared versions, schemas or locks, and the host's load.
- **Brief template.** A brief states:
  - why the work matters;
  - the code locations, checked at a named commit;
  - the invariants to keep;
  - how to verify it, naming the tests that must actually run, not skip;
  - coordination: which files other workers own, the landing order, and
    who bumps shared versions;
  - hypotheses, labelled as hypotheses.
- **Decisions.** Routine calls go to `loom log`. Escalations go to the user
  through `AskUserQuestion`, always with a recommendation, with related
  questions batched into one prompt.
- **Landing gate.** Before marking a worker `verified`, the supervisor
  confirms that:
  - the branch fast-forwards the current base;
  - the changes stay in scope;
  - no lock or toolchain bump is included unless assigned;
  - it has read the invariant at risk in the diff;
  - the worker's report says which tests actually ran.

  Branches land one at a time, in order, and CI is checked after each one.
  Then the next worker is told to merge.
- **Shared resources.** Each shared version or lock has one owner. When the
  host misbehaves, investigate (journal, process groups) before blaming
  another session.
- **Noise.** No acknowledgement-only messages. Relay only what changes
  another session's work.

**`worker.md`**

- **Start.** The brief is the first prompt. Set `loom state working
  "<plan>"` and stay within the brief.
- **Blocked.** Set `loom state blocked "<on what>"`.
- **Ready.** Set `loom state ready "<summary>"` and send the supervisor one
  report: branch and HEAD, files touched, which tests ran or were skipped,
  the evidence, any judgement calls, and every question you put to the user
  with its answer.
- **Never** push to the base branch, or bump a lock or toolchain file unless
  assigned.
- **Resources.** Run builds in the foreground with limited jobs, detach long
  jobs fully, run heavy analysis under a memory cap, and kill only processes
  you started.
- **Hypotheses in the brief are hypotheses.** Check them before relying on
  them, and report any corrections.

**Repo extensions** add project specifics. For kermit those are `nix
develop`, `CARGO_BUILD_JOBS=2`, a private miri sysroot, the earlyoom warning,
measurement only through `--verify`, the requirement that the LUBM test
actually run, and keeping timing figures out of commits.

### 4. The agent CLI

These are new `loom` subcommands for agents. Each refuses to run without
`LOOM_INSTANCE`, or outside a supervised workspace.

| Command | Caller | Effect |
|---|---|---|
| `loom state <working\|blocked\|ready> "<summary>"` | worker | Declares the caller's own state. `ready` also records the worktree's HEAD. |
| `loom state --for <title> verified "<notes>"` | supervisor | Marks a worker as verified. |
| `loom log "<decision>"` | supervisor | Appends a routine decision to the workspace log. |
| `loom propose --title <t> [--issue <n>] --brief <file> [--base <ref>]` | supervisor (stage 3) | Proposes a worker. |
| `loom board [--json]` | any | Prints the workspace's board. |

**Transport.** The commands that write drop a JSON event into
`$LOOM_HOOKS_DIR/events/`. That is the folder loom's hooks already write
to, using the same tmp-then-rename write as the hook command. The events
have new names: `LoomState`, `LoomLog` and `LoomPropose`. The existing scan
(`session/hooks`) parses them, and `ApplyHookScan` folds them into the
instance, which persists its work state at the next save. The hooks folder
is emptied at every launch, which doesn't matter because that state is
already persisted. `loom board` only reads loom's state.

**Authority.** Loom applies an event only from the right caller:

- a worker's state events change only that worker;
- `verified`, `log` and `propose` take effect only from the supervisor.

Any other event is dropped and logged.

### 5. Work state and the board

`InstanceData` gains `work_state`, a record of `{state, summary, head, at,
by}`. This bumps the schema from v8 to v9, and the field is `omitempty`.
Work state is separate from the runtime status (Ready, Running, Prompting
and so on).

| State | Set by | Meaning |
|---|---|---|
| `proposed` | loom | A proposal not yet approved (stage 3). |
| `working` | worker | Set at start, with its plan. |
| `blocked` | worker | Waiting on a decision, another landing or the host. The summary says which. |
| `ready` | worker | Its checks passed. HEAD is recorded. |
| `verified` | supervisor | The supervisor's review passed. |
| `landed` | loom | The branch HEAD is an ancestor of the base branch's remote-tracking ref. |

Killing a landed instance removes it from the board, as killing does today.

Loom detects `landed` from git on the health tick, with `git merge-base
--is-ancestor`. The base is the branch loom cuts new worktrees from
(`Config.GetBaseBranch`, or else the branch `git.ResolveBaseCommit`
chooses), compared through its `origin/` ref. No network access is needed,
because a push from a worktree updates that local ref. A `ready` or
`verified` state whose HEAD no longer matches the branch tip shows as stale.

**The board.** In a supervised workspace the overview cards carry the work
state:

- the state, with its summary and age;
- the runtime status loom already shows.

The main session's card heads the group. Cards that need the user sort
first. A `verified` card shows the exact push command for its branch.

A **decisions tab** lists the workspace log, `<repo>/.loom/decisions.jsonl`.
It is read-only: to overturn a decision, the user tells the supervisor.

### 6. The inbox (stage 2)

**Capture.** Loom registers `PreToolUse` and `PostToolUse` with the matcher
`AskUserQuestion`, and for nothing else. This narrows the hook-events
spec's exclusion of those two events. That exclusion was about firing on
every tool call and writing whole tool outputs to disk, and neither happens
with this matcher.

- `PreToolUse` records the questions on the instance: each one's header,
  text, options and multi-select flag, plus the `tool_use_id`.
- `PostToolUse` with the same ID clears them.
- If a question is interrupted, it is cleared at the instance's next
  `UserPromptSubmit`, `Stop` or `SessionEnd`.

**Items.** The inbox lists everything that waits on the user, across every
open workspace. Each item shows the session, its age and one line of
context.

- Pending questions, with the recommended option marked.
- Permission prompts: the existing Prompting status, with its reason.
- Proposals (stage 3).
- `verified` branches waiting for a push.

**Keys and status.**

- `!` opens the inbox overlay. The key is free in CLAUDE.md's keybinding
  table; confirm against `script/defaults.lua`. `enter` focuses the session,
  whose pane already shows the question.
- `]` and `[`, which jump to the next waiting agent, include the new kinds.
- The status bar shows "needs you: N".

### 7. Proposals (stage 3)

A proposal is a worker instance in the supervised workspace that has not
been started. Its status is `Ready` and its work state is `proposed`. It
carries a title, an optional issue, a base ref and the brief as its prompt.
Titles go through `github.SlugTitle`, so branch names stay valid and survive
restarts.

- **Approving** takes one key, `y`, from the inbox or the card. It runs the
  existing start path with the brief as the first prompt, exactly as `N`
  does.
- **Rejecting** takes one key, `x`, and an optional reason. Loom kills the
  instance and appends the rejection to the decision log.

The brief is also kept at `<repo>/.loom/briefs/<title>.md`, so a restarted
worker can re-read it. Loom removes it when the instance is killed.

### 8. Failure handling

| Situation | Behaviour |
|---|---|
| The CLI is run outside loom, or in a normal workspace | It fails with a clear message. |
| An event comes from the wrong role | Dropped and logged. |
| A worker ignores the protocol | The board shows its runtime status only, with no work state. |
| The hooks folder is emptied at relaunch | Work state is already persisted. |
| A question is cleared by neither hook nor a later event | Cleared at the next health tick once the instance is no longer Prompting. |
| A proposal reuses an existing title | `loom propose` fails, and the supervisor picks another title. |
| The branch is rewritten after `ready` | The card shows the state as stale (HEAD mismatch). |
| The mode is switched while sessions run | Each session picks up the change at its next launch. Loom offers to restart the main session. |

## Testing

- **`session/hooks`:** parsing fixtures for `LoomState`, `LoomLog` and
  `LoomPropose`, and for the `AskUserQuestion` `PreToolUse` and
  `PostToolUse` payloads captured by the probe (assumption 1).
- **Work state (table tests):** the authority rules; a stale HEAD; `landed`
  detection on a temporary repo; a replay rebuilds the same state as
  incremental application.
- **The mode:** roles derived from the mode and `IsWorkspaceTerminal`;
  context composition with and without a repo extension; a normal workspace
  launches exactly as before.
- **CLI:** uses a temporary `LOOM_HOOKS_DIR`; events are written atomically;
  calls outside loom are refused.
- **Storage:** the v8 → v9 migration, the shape fixture, and the mirror
  struct in `loom workspace migrate`.
- **`tools/fakeagent`:** its claude persona calls `loom state` and raises an
  `AskUserQuestion` that runs the registered hooks. An end-to-end test
  approves a proposal from the inbox and checks that the worker's first
  prompt is the brief.
- **Opt-in real-Claude contract test (`LOOM_TEST_REAL_CLAUDE=1`):** the
  `AskUserQuestion` matcher fires, `PreToolUse` carries the questions and
  the `tool_use_id`, and `PostToolUse` carries the same ID.
- `CC=clang CGO_ENABLED=1 go test -race ./...`.

## Documentation

- **CLAUDE.md** gains three gotchas: roles derive from the workspace mode;
  agent CLI events travel through the hooks folder; the `AskUserQuestion`
  matcher is an exception to the hook-events spec.
- **USAGE.md** covers the mode, the board, the inbox, proposals and their
  keys.
- **The protocol files** are user-facing text and are reviewed like
  documentation.

## Rollout

Each stage gets its own implementation plan and ships on its own. Each is
tried on kermit, with one supervisor and two workers, before the next stage
starts.

1. The mode, context composition, the protocol text, `loom state`, `loom
   log`, `loom board`, and the board itself.
2. The inbox.
3. Proposals.

Success means three things. The user reads every session's state from the
board. The user's interactions are approvals, decisions and pushes, with no
relays. A fresh supervisor follows the brief template and the landing gate
from the text alone.

## Out of scope

- Answering questions inside loom.
- Resource limits.
- Agents other than Claude Code.
- Automatic pushes or merges.
- A socket transport.
- More than one supervisor per workspace.

## Assumptions to verify first

| # | Assumption | How |
|---|---|---|
| 1 | Claude Code honours a `PreToolUse`/`PostToolUse` matcher on `AskUserQuestion`, and the payloads carry the questions and a `tool_use_id`. | A probe, as for the hook-events spec. |
| 2 | Loom can export environment variables to the agent at launch. | Read the launcher. Accounts already set `CLAUDE_CONFIG_DIR` there. |
| 3 | The main session (workspace terminal) gets hooks like worktree sessions do. | Read `session/hooks` and the workspace-terminal launch path. |
| 4 | Overview cards can carry the work-state line without a layout change. | Read `ui/overview.go` and `ui/card.go`. |
