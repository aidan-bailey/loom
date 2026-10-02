# Supervised Workspaces

**Date:** 2026-10-02
**Status:** Approved design, brainstormed with the user one section at a time.
Supervision became a workspace mode at the end of the session. Revised the
same day after a review against the code. Coordination state moved from
hook events and `state.json` into a per-workspace work log. Proposals now
ship before the inbox, and per-repo protocol files were dropped.
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
| 2 | The supervisor **proposes** worker sessions, and the user approves each with one key. The brief is a file, and the worker's first prompt points at it. |
| 3 | The supervisor makes routine calls itself and logs them: landing order, who does what, test and doc details, issue housekeeping. The user decides scope, priorities, anything irreversible or outward-facing, and anything that changes measurements or data. |
| 4 | **One inbox** in loom holds everything that waits on the user, from every session. The user answers in the asking session's pane. |
| 5 | The state of each piece of work **lives in loom**, in a per-workspace **work log** that agents append to through loom's CLI. Workers declare their state, the supervisor verifies, and loom derives `landed` from git. Messages carry content (briefs, reports), not status. |
| 6 | Claude Code only. |
| 7 | Every session in a supervised workspace runs under a **stable name**, so cross-session messages keep reaching it across restarts. |
| 8 | Loom owns the mode, the work log, proposals and the inbox, through a small agent CLI (`loom work`) and an `AskUserQuestion` hook. They are delivered in three stages: first the CLI and the log, then the board and proposals, then the inbox. |

Rejected:

- **The protocol as Claude Code skills, with loom only displaying it.** Loom
  would parse Claude Code's private files, and the inbox could not show the
  question being asked.
- **Loom as a deterministic orchestrator.** The hub's value came from
  judgement that no rule anticipates.
- **Per-session roles chosen at creation**, superseded by decision 1.
- **Events through the hooks folder, folded into `state.json`.** This spec's
  first version used them, and they lost or delayed state. Every launch
  empties the hooks folder, and at startup loom relaunches dead sessions
  before it scans. Events written while loom was closed were therefore lost.
  Loom writes `state.json` only at lifecycle events (start, pause, kill, a
  workspace switch, quit), so `loom board` could lag by hours, and a crash
  lost whatever loom had scanned. An event file is also fire-and-forget, so
  the CLI could not tell an agent that loom had refused it.
- **Proposals as unstarted instances.** A saved instance that was never
  started is a new case for reconcile, orphan discovery and title claims. A
  log entry needs none of that.
- **Per-repo protocol files under `.loom/protocol/`.** `.loom/` is
  gitignored, so such a file could be committed only with `git add -f`. The
  protocol is loom's own text, changed by committing to loom. Project
  specifics belong in the project's CLAUDE.md, which every session already
  reads.

## Design

### 1. The mode

`workspaces.json` gains a `mode` for each workspace: `""` (normal, as today)
or `supervised`. It is set with `loom workspace mode [name]
<normal|supervised>`, and from stage 2 also with a toggle in the settings
overlay (`S`). Registry writes already reload the file before saving, so
the CLI and a running TUI don't overwrite each other. The TUI notices a
change made elsewhere by checking the file's size and modification time on
the health tick, as it does for `accounts.json`.

**Roles derive from the mode.** In a supervised workspace the main session
(`IsWorkspaceTerminal`) is the supervisor, and every worktree session is a
worker. No per-instance role is stored. A normal workspace behaves exactly
as today.

**Resolved per instance, at launch.** Each time loom launches an instance,
it reads the mode of that instance's own workspace. The mode is never a
process-wide setting. `applySessionConfig` sets the loom-context toggle that
way, and whichever workspace loads last wins. Done the same way, the mode
could give a worker another workspace's role.

**Switching.** The mode is a launch argument, so a switch reaches each
session at its next launch. Turning the mode on in the TUI offers to restart
the main session as the supervisor, or starts one if the workspace has none.
`loom workspace mode` prints how instead: exit Claude in the main session,
and loom's health tick relaunches it. Turning the mode off keeps the work
log but hides the board, and the CLI refuses from then on.

**Sessions started by hand** (`n`, `N`, `I`) in a supervised workspace are
workers, and the user's prompt takes the place of a brief. They appear on
the board. Proposals (§8) are the usual route.

### 2. Launching a supervised session

A launch in a supervised workspace differs from a normal launch in four
ways. A normal workspace launches exactly as today.

1. **Context.** Loom composes the appended system-prompt file from today's
   base context and the role's protocol (§3). The base context is
   `claude-loom-context.md`, or the workspace variant for the main session.
   Loom writes the result to `claude-loom-context-<role>.md` in the
   workspace's config dir and passes it to `--append-system-prompt-file`.
   The protocol is included even when the loom-context setting is off; the
   file then holds the protocol alone.
2. **The CLI path.** The protocol names the CLI by the absolute path of the
   running binary (`os.Executable()`). An agent then calls the loom that
   launched it, whatever its `PATH` says. Under `loomdev`, or with a Nix
   store path, the `loom` on `PATH` is a different build.
3. **Name and environment.** Loom passes `--name`. The main session is
   named after the workspace, and a worker `<workspace>/<title>`.
   Cross-session messages address a session by this name, and it survives
   `--resume` and relaunches (assumption 1). Through `LaunchEnv`, loom also
   exports three variables into the session's environment:
   - `LOOM_INSTANCE`, the instance title;
   - `LOOM_ROLE`, `supervisor` or `worker`;
   - `LOOM_WORK_DIR`, the workspace's work log folder (§4).
4. **Settings.** Loom's per-launch settings file, which already carries its
   hooks, gets two additions:
   - an allow rule for the CLI, `Bash(<loom path> work:*)`, so it never
     prompts;
   - `LOOM_WORK_DIR` in `permissions.additionalDirectories`, so a sandboxed
     worker can write the log (assumption 3). The main session's working
     directory already contains it.

A program string with its own `--name` keeps it. Sometimes loom can't add
its settings: the program has its own `--settings`, or the config dir path
contains a `'`. The session still runs, but the CLI may prompt and a
sandboxed worker may be refused, and loom warns at launch.

### 3. Protocol text

There are two files, embedded with `go:embed`, each about a page of rules
and checklists. The commands below are written `loom work …`. The composed
file uses the absolute path.

**`supervisor.md`**

- **Role.** The supervisor doesn't implement. It owns priority,
  coordination, verification and issue housekeeping, and routes decisions.
- **Board.** Read `loom work board` before proposing work and before each
  landing.
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

  Propose a worker with `loom work propose --title <t> [--issue <n>] --brief
  <file>`. Before stage 2, write the brief to `$LOOM_WORK_DIR/briefs/<title>.md`.
  Then ask the user to start the session with that title and a one-line
  prompt pointing at the file.
- **Decisions.** Routine calls go to `loom work note`. Escalations go to the
  user through `AskUserQuestion`, always with a recommendation, with related
  questions batched into one prompt.
- **Landing gate.** Before running `loom work verify <title>`, the
  supervisor confirms that:
  - the branch fast-forwards the current base;
  - the changes stay in scope;
  - no lock or toolchain bump is included unless assigned;
  - it has read the invariant at risk in the diff;
  - the worker's report says which tests actually ran.

  Branches land one at a time, in order, and CI is checked after each one.
  Then the next worker is told to merge. Merging moves its HEAD, so it
  re-runs its checks and declares `ready` again.
- **Shared resources.** Each shared version or lock has one owner. When the
  host misbehaves, investigate (journal, process groups) before blaming
  another session.
- **Noise.** No acknowledgement-only messages. Relay only what changes
  another session's work.

**`worker.md`**

- **Start.** The first prompt points at the brief: read it. Run `loom work
  state working "<plan>"` and stay within the brief. After a restart, read
  the brief again.
- **Blocked.** Run `loom work state blocked "<on what>"`. Put questions for
  the user through `AskUserQuestion`.
- **Ready.** Write a report covering:
  - the branch and HEAD;
  - the files touched;
  - which tests ran and which were skipped;
  - the evidence;
  - any judgement calls;
  - every question you put to the user, with its answer.

  Run `loom work state ready "<summary>" --report <file>`, then send the
  supervisor the report as one message.
- **Never** push to the base branch, or bump a lock or toolchain file unless
  assigned.
- **Resources.** Run builds in the foreground with limited jobs, detach long
  jobs fully, run heavy analysis under a memory cap, and kill only processes
  you started.
- **Hypotheses in the brief are hypotheses.** Check them before relying on
  them, and report any corrections.

**Project specifics** stay in the project's CLAUDE.md, which every session
reads, supervised or not. For kermit those are:

- `nix develop`;
- `CARGO_BUILD_JOBS=2`;
- a private miri sysroot;
- the earlyoom warning;
- measurement only through `--verify`;
- the requirement that the LUBM test actually run;
- keeping timing figures out of commits.

### 4. The work log

The work log is the one store for coordination state. It lives in the
workspace's config dir, which is gitignored:

- `<repo>/.loom/work/log.jsonl` is append-only, with one JSON object per
  line.
- Briefs live in `<repo>/.loom/work/briefs/<title>.md`, and reports in
  `<repo>/.loom/work/reports/<title>.md`.

**Entries.** Every entry carries four common fields:

- `v`, the format version (1);
- `at`, a UTC timestamp;
- `by`, the caller's title, or `loom`;
- `kind`, from the table below.

| Kind | Written by | Fields |
|---|---|---|
| `state` | a worker | `state` (`working`, `blocked` or `ready`) and `summary`; for `ready`, also `head` and `report` |
| `verify` | the supervisor | `target`, `head` (the `ready` HEAD it verified) and `notes` |
| `note` | the supervisor | `text`, a routine decision |
| `propose` | the supervisor | `title`, `issue`, `brief` and `summary` (stage 2) |
| `approve`, `reject` | loom | `title`, plus `reason` for a rejection (stage 2) |
| `remove` | loom | `title`, when an instance is killed |

**Writing.** Each append takes an exclusive `flock` on `log.jsonl` and
writes one complete line in a single write. The CLI reads the log under the
same lock before it appends, so its checks see every earlier entry. Loom
appends its own entries the same way, from a `tea.Cmd`.

**Authority.** The CLI refuses, and writes nothing, when:

- the supervisor sends a `state` (a `state` is always about its caller);
- a worker sends a `verify`, `note` or `propose`;
- a `verify` targets a worker that is not `ready`, or whose branch tip has
  moved since it declared `ready`.

When the board is built from the log, the same rules apply. Entries that
break them (a hand edit, say) are skipped, and the board counts them. These
checks catch protocol mistakes, not malice: any agent can write any file as
the user.

**Reading.** `loom work board` and the TUI build the board by folding the
log from the start. For each title the fold keeps:

- the latest `state`;
- a `verify` that matches it;
- where its proposal stands.

A `remove` forgets the title, so a reused title starts clean. The TUI keeps
its read offset. When the file's size or modification time changes (checked
on the health tick), it reads the new complete lines in a `tea.Cmd` and
applies them on the Update goroutine. A trailing partial line waits for the
next read.

**Derived states, never stored.**

- **`landed`:** the latest state is `ready` or `verified`, and its recorded
  HEAD has commits past the worker's base commit. In addition, either that
  HEAD is an ancestor of `origin/<base>`, or (in the TUI only) the GitHub
  poller reports the branch's PR as `PRMerged`. The PR signal covers merges
  made on GitHub, squash merges included.

  `<base>` is the branch loom cuts worktrees from: `Config.GetBaseBranch`,
  or else the branch `git.ResolveBaseCommit` picks. It is always compared
  through its `origin/` ref, which a push from this machine updates.

  The base-commit condition matters. A fresh branch's tip *is* its base
  commit, and `git merge-base --is-ancestor` counts a commit as its own
  ancestor.
- **Stale:** the latest state is `ready` or `verified`, and the branch tip
  no longer matches the recorded HEAD.

**Lifetime.** Killing an instance appends `remove` and deletes its brief and
report. `loom reset` deletes the log along with the workspace's instances.
The log stays small, so v1 does no compaction.

### 5. The agent CLI

These are new `loom work` subcommands for agents.

| Command | Caller | Effect |
|---|---|---|
| `loom work state <working\|blocked\|ready> "<summary>" [--report <file>]` | worker | Declares the caller's own state. `ready` records the branch tip and requires a report, which it copies into `reports/`. |
| `loom work verify <title> "<notes>"` | supervisor | Marks a `ready` worker verified at its recorded HEAD. |
| `loom work note "<decision>"` | supervisor | Logs a routine decision. |
| `loom work propose --title <t> [--issue <n>] --brief <file>` | supervisor (stage 2) | Proposes a worker and prints its final title. |
| `loom work board [--json] [<title>]` | any | Prints the board, or one worker's entry with its brief, report and push command. |

Each command needs `LOOM_INSTANCE`, `LOOM_ROLE` and `LOOM_WORK_DIR`, and
each refuses once the workspace is no longer supervised. A refusal goes to
stderr with a non-zero exit, so the agent sees it in its tool output.

`loom work board` needs no running TUI. It folds the log, reads
`state.json` for each worker's branch and base commit, and asks git. It
shows no runtime status.

### 6. Work state

Work state is separate from the runtime status (Ready, Running, Prompting
and so on).

| State | Set by | Meaning |
|---|---|---|
| `proposed` | supervisor | A proposal not yet approved (stage 2). |
| `working` | worker | Set at start, with its plan. |
| `blocked` | worker | Waiting on a decision, another landing or the host. The summary says which. |
| `ready` | worker | Its checks passed. HEAD and the report are recorded. |
| `verified` | supervisor | The supervisor's review passed at the recorded HEAD. |
| `landed` | derived | See §4. |

A `ready` or `verified` state can also show as stale (§4).

### 7. The board (stage 2)

In a supervised workspace the overview cards carry the work state: the
state, its summary and its age. The line comes out of the live tail, as an
issue link's does, so `overviewCardHeight` holds. A rail card shows the work
state on its second line when the card doesn't need attention. Text that
agents supply passes through `sanitizeCardText`.

The main session's card heads the group, and cards that need the user sort
first.

A `verified` card shows the push command, pinned to the verified commit:
`git push origin <sha>:refs/heads/<base>`. If the base has moved, git
refuses the push as a non-fast-forward. That enforces the landing gate's
first check.

A **decisions tab** in the main session's workbench lists the `note`
entries and the rejections. It is read-only: to overturn a decision, the
user tells the supervisor. Before stage 2, `loom work board` prints the
same information as text.

### 8. Proposals (stage 2)

A proposal is a `propose` entry plus a brief at `briefs/<title>.md`. Nothing
enters `state.json` until the proposal is approved. `loom work propose`
passes the title through `github.SlugTitle` and prints the result. It
refuses a title that an instance or a live proposal already uses. A
proposal shows as a placeholder card in its workspace's group.

- **Approving** takes one key, `y`, on the card (and, from stage 3, in the
  inbox). Loom runs the existing new-session path with the title and issue.
  The first prompt is one line: "You are worker `<title>` in a supervised
  workspace. Your brief is at `<path>`: read it, then start." One line
  matters because `SendPrompt` types the prompt with `send-keys`, which
  works for a line but not for a page of markdown. The brief stays on disk
  for restarts and compactions.

  Loom then appends `approve`. If loom's own title checks refuse the title
  at that point (because a preserved record holds it, say), loom appends
  `reject` with the reason instead.
- **Rejecting** takes one key, `x`, and an optional reason. Loom appends
  `reject` and deletes the brief.

Workers start from the workspace's base, like any session. Stacking a
worker on another worker's branch is out of scope.

### 9. The inbox (stage 3)

**Capture.** Loom registers `PreToolUse` and `PostToolUse` with the matcher
`AskUserQuestion`, and for nothing else. This narrows the hook-events
spec's exclusion of those two events. That exclusion was about firing on
every tool call and writing whole tool outputs to disk, and neither happens
with this matcher. Questions are transient runtime state, so they travel
through the hooks folder like the other hook events, not through the work
log.

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
- Proposals.
- `verified` branches waiting for a push.

**Keys and status.**

- `!` opens the inbox overlay, and `enter` there focuses the session, whose
  pane already shows the question. `!`, `y` and `x` are unbound in
  `script/defaults.lua`, and all three join `overviewKeyAllowed`.
- `]` and `[`, which jump to the next waiting agent, include the new kinds.
- The status bar shows "needs you: N".

### 10. Failure handling

| Situation | Behaviour |
|---|---|
| The CLI runs outside a supervised loom session | It refuses, naming the missing variable or the mode. |
| A command comes from the wrong role, or `verify` targets a moved branch | The CLI refuses and writes nothing. |
| A worker ignores the protocol | The board shows its runtime status only, with no work state. |
| Loom is closed or crashes, or the host reboots | Nothing is lost: an entry is on disk once the CLI returns. |
| The branch moves after `ready` or `verified` | The card shows the state as stale, and `verify` refuses. The push command stays pinned to the verified commit. |
| A proposal reuses a title | `propose` refuses. Loom's own checks still run at approval, and a refusal there is logged as a rejection. |
| A branch lands through GitHub | The TUI marks it `landed` from the poller's PR state. `loom work board` uses git only, so it shows the landing after a fetch. |
| Loom can't add its launch settings | The session runs, but the CLI may prompt and a sandboxed worker may be refused. Loom warns at launch. |
| A question is cleared by neither hook nor a later event | Cleared at the next health tick once the instance is no longer Prompting. |
| The mode is switched while sessions run | Each session picks up the change at its next launch, and the CLI refuses once the mode is off. Loom offers to restart the main session. |

## Testing

- **The work log** gets a new package with table tests for:
  - the fold and the authority rules;
  - a `verify` refused on a moved branch;
  - a `remove` followed by a reused title;
  - skipped entries;
  - `landed` and stale on a temporary repo, including a fresh branch, which
    must not count as landed;
  - concurrent appends from several processes, each landing as a complete
    line;
  - reading across a partial trailing line.
- **The CLI** runs against a temporary `LOOM_WORK_DIR`. Tests cover the
  refusals (outside loom, from the wrong role, with the mode off), the exit
  codes and the `--json` shape.
- **Launch** tests cover:
  - roles derived from the mode and `IsWorkspaceTerminal`;
  - the composed context, which includes the protocol even with
    loom-context off;
  - `--name`, the environment and the settings additions, present only in
    a supervised workspace;
  - a normal workspace launching exactly as before;
  - two open workspaces in different modes, each launching with its own
    role.
- **The TUI (stage 2):**
  - work-state cards keep `overviewCardHeight`;
  - placeholder cards, `y` and `x` work;
  - entries read from the log are applied only on the Update goroutine.
- **`tools/fakeagent`:** its claude persona calls `loom work state`. An
  end-to-end test approves a proposal and checks that the worker's first
  prompt points at the brief.
- **The opt-in real-Claude contract test** (`LOOM_TEST_REAL_CLAUDE=1`)
  checks that:
  - `--name` survives `--resume`;
  - with loom's settings, a sandboxed worker runs `loom work state` without
    a prompt;
  - in stage 3, the `AskUserQuestion` matcher fires, `PreToolUse` carries
    the questions and the `tool_use_id`, and `PostToolUse` carries the same
    ID.
- `CC=clang CGO_ENABLED=1 go test -race ./...`.

`InstanceData` doesn't change, so there is no schema bump.

## Documentation

- **CLAUDE.md** gains three gotchas:
  - roles derive from the workspace mode and are resolved per instance at
    launch;
  - the work log carries coordination state, not hooks or `state.json`, and
    every append is one locked write of one line;
  - the `AskUserQuestion` matcher is an exception to the hook-events spec.
- **USAGE.md** covers the mode, `loom work`, the board, proposals, the inbox
  and their keys.
- **The protocol files** are user-facing text and are reviewed like
  documentation.

## Rollout

Each stage gets its own implementation plan and ships on its own. Each is
tried on kermit, with one supervisor and two workers, before the next stage
starts.

1. **Supervision without new UI:** the mode (`loom workspace mode`), the
   launch changes, the protocol text, the work log, and `loom work state`,
   `verify`, `note` and `board`. The supervisor and the user read the board
   through `loom work board`.
2. **The board and proposals:**
   - work state on the cards, the decisions tab and the push command;
   - the settings toggle and the offer to restart the main session;
   - `loom work propose`, placeholder cards, `y` and `x`.
3. **The inbox**, after the `AskUserQuestion` probe.

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
- Per-repo protocol files.
- Stacking a worker on another worker's branch.
- Windows, which has neither `flock` nor loom's hooks.

## Assumptions to verify first

| # | Assumption | How |
|---|---|---|
| 1 | `claude --name` sets the address that cross-session messages use, keeps it across `--resume` and relaunches, and accepts `/`. | The cross-session messaging docs say so. Confirm in the contract test before stage 1. |
| 2 | A cross-session message reaches a session running on another Claude account. | Unknown: each account has its own `sessions` and `daemon` dirs, and `claude agents` answers per config dir. If not, reports on the board are the fallback, and loom either keeps a supervised workspace on one account or warns when its sessions span accounts. Probe before stage 1. |
| 3 | An allow rule and `permissions.additionalDirectories` in loom's `--settings` let a sandboxed worker run the CLI without a prompt and write the log. | The permissions and sandboxing docs say so. Probe before stage 1. |
| 4 | Claude Code honours a `PreToolUse`/`PostToolUse` matcher on `AskUserQuestion`, and the payloads carry the questions and a `tool_use_id`. | The docs confirm the matcher, not the payload. Probe before stage 3, as for the hook-events spec. |

Verified against the code on 2026-10-02:

- Loom can export environment variables at launch: `LaunchEnv` becomes the
  tmux session's environment through `InstanceEnv`
  (`session/agent_restart.go`).
- The main session gets hooks. The workspace terminal starts with
  `Start(true)`, which runs `launchProgram(…, true)`, and its hooks folder
  is keyed by tmux session name (`session/subagent_hooks.go`).
- Overview cards can carry the work-state line without a layout change,
  by taking a tail line as an issue link does (`ui/overview.go`).
- `!`, `y` and `x` are unbound in `script/defaults.lua`.
- The health tick relaunches a workspace terminal whose agent exits
  (`Restart`), and the relaunch reapplies the context.
- The GitHub poller already reports merged PRs (`github.PRMerged`).
