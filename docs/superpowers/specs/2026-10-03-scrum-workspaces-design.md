# Scrum Workspaces

**Date:** 2026-10-03
**Status:** Approved design. Supersedes
[2026-10-02-supervised-workspaces-design.md](2026-10-02-supervised-workspaces-design.md)
and its stage 1 plan. Brainstormed with the user from a workflow sketch
(below) against the supervised-workspaces spec; the probes, the work log,
the protocol text and the launch mechanics carry over, and the
coordination model changes. Depends on the loom daemon
([2026-10-03-loom-daemon-design.md](2026-10-03-loom-daemon-design.md)),
which must ship through its stage 3 first.
**Origin:** a day on the kermit repository in which one loom session acted
as a hand-made supervisor over seven per-issue sessions (see the superseded
spec's Problem section), and the user's sketch of the workflow wanted
instead:

1. The user prompts a **Product Owner** (PO) with goals and constraints.
   The PO triages the GitHub board, asks when scope is ambiguous, and
   proposes a shortlist. The user approves it; the PO hands the sprint to
   the **Scrum Master** (SM).
2. The SM orders the issues by dependency and file overlap, runs
   independent ones concurrently and dependent ones in sequence, and
   starts a **Developer** session per ready issue with a brief.
3. A Developer writes failing tests, implements, runs lint and tests,
   reports coarse lifecycle state, pushes its branch and opens a pull
   request, then yields.
4. The user reviews on GitHub. The Developer addresses comments. When the
   PR merges, the SM stands the Developer down and dispatches whatever
   was waiting on it.

## Problem

The superseded spec's problem stands: coordinating per-issue sessions by
hand costs the user spawn-then-brief relays, scattered decisions and
invisible state. What changed in the sketch:

- **Two coordinating roles, not one.** The PO reasons about scope with
  the user; the SM is mechanical. The user wants them in separate
  transcripts, and wants the SM restartable without losing the PO's
  conversation.
- **Approval per sprint, not per session.** The user approves a
  shortlist once; the SM starts sessions on its own within it.
- **Landing through pull requests.** Developers push their own branch
  and open a draft PR; the user merges on GitHub. The local push command
  goes.
- **The cascade runs unattended.** Review comments, merges and the next
  dispatch must happen with loom closed. That is why the daemon exists.

## Goals

1. **Visibility:** the user reads the state of every issue in the sprint
   from loom's board, without opening a transcript.
2. **Less load:** the user handles the sprint approval, real decisions,
   PR review and merges, and nothing else.
3. **Repeatable:** a fresh PO, SM or Developer follows the discipline
   from the protocol text alone.
4. **Unattended:** a sprint progresses while no TUI is open.

Non-goals: resource limits, agents other than Claude Code, automatic
merges, loom writing to GitHub, and answering questions from inside loom.

## Decisions

| # | Decision |
|---|---|
| 1 | Scrum is a **workspace mode**. A scrum workspace has two **staff** sessions, the PO and the SM, worktree-less in the repo root; every worktree session is a Developer. |
| 2 | Staff sessions carry a stored **role** (`po`, `sm`); Developers are role-less, derived from having a worktree. The role is the one new `InstanceData` field (schema v9). |
| 3 | Staff **start automatically** when the mode is turned on and are **relaunched** by the daemon when they die, as the workspace terminal is today. |
| 4 | The user approves a **sprint** once, in the PO's pane. Within it the SM **spawns** and **releases** Developer sessions through the agent CLI, with no per-session approval. |
| 5 | **Draft PR, SM flips it.** A Developer pushes its branch and opens a draft PR when its own checks pass. The SM runs the landing gate and marks the PR ready for review. The user reviews only PRs the SM passed, and merges on GitHub. |
| 6 | **Loom nudges idle panes.** The daemon's GitHub poller sees review activity and merges, and types one line into the right session's pane when that session is idle. No agent polls GitHub. |
| 7 | The **work log** (unchanged from the superseded spec) is the one store for coordination state, owned by the daemon. Messages carry content (briefs, reports); the log carries state. |
| 8 | Claude Code only; one account per scrum workspace; stable `--name`s. |
| 9 | The agent CLI (`loom work …`) is a **daemon client**. It works with no TUI open and spawns the daemon if none is running. |

Rejected, in addition to the superseded spec's list:

- **One supervisor with a triage phase.** The user wants scope reasoning
  and dispatch in separate transcripts (goal: the PO survives SM
  restarts and compactions).
- **PO and SM as a Claude agent team inside one loom session.** Almost
  no loom work, but the SM gets no card, no pane and no independent
  restart, and a teammate's ability to message other loom sessions is
  unverified.
- **No SM gate before review.** The kermit day's landing gate caught a
  flag-order bug and a scope creep; it stays, as the draft flip.
- **Agents polling GitHub with `/loop`.** Every idle agent would spend
  turns polling, and the sketch's cascade still needed loom to spawn.
- **Loom marking the PR ready or merging.** Loom never writes to GitHub;
  the SM runs `gh pr ready`, the user merges.

## Design

### 1. The mode and the staff

`workspaces.json` gains `mode` (`""` or `scrum`) and `account` (the
Claude account the workspace's sessions share; `""` is the default
account). Both are set with
`loom workspace mode [name] <normal|scrum> [--account <name>]`, a daemon
request, and later from the settings overlay. The daemon applies a mode
change at once: turning scrum on starts the staff; turning it off keeps
the work log, hides the board, stops the nudges, and the CLI refuses from
then on. The staff sessions are left running; the user kills them or
turns the mode back on.

**Roles.** `InstanceData` gains `role` (schema v9): `po`, `sm`, or
empty. A staff session is `IsWorkspaceTerminal` with a role, so it
inherits the terminal's behaviour: no worktree, runs in the repo root,
cannot pause, diff shows the root's uncommitted changes, relaunched by
the health tick when its agent exits. The plain workspace terminal (no
role) keeps existing beside them for the user's own use and gets no
protocol. A worktree session in a scrum workspace is a Developer; its
role at launch is `dev`, derived, never stored.

**Auto-start.** When the mode turns on, the daemon creates the two staff
instances with titles `<workspace>-po` and `<workspace>-sm` (through
`SlugTitle`, so they are valid branch leaves even though they never get
a branch) unless instances with those roles exist, and starts them. On
every daemon start, a scrum workspace with a missing staff instance gets
it recreated. A user who kills a staff session gets it back at the next
daemon start or mode toggle; to stop it for good, turn the mode off.

**Sessions started by hand** (`n`, `N`, `I`) in a scrum workspace are
Developers outside any sprint: they get the Developer protocol, appear on
the board, and the SM may `adopt` them into the open sprint. The user's
prompt stands in for a brief.

### 2. Launching a scrum session

A launch in a scrum workspace differs from a normal launch exactly as the
superseded spec's §2 described, with the role vocabulary updated:

1. **Context.** The role's protocol (§3) is appended to the loom-context
   file, written as `claude-loom-context-<role>.md` in the workspace's
   config dir and passed to `--append-system-prompt-file`, included even
   when the loom-context setting is off.
2. **The CLI path.** The protocol names the CLI by `os.Executable()`.
3. **Name and environment.** `--name` is `<workspace>/po`,
   `<workspace>/sm` or `<workspace>/<title>`. `LaunchEnv` exports
   `LOOM_INSTANCE` (title), `LOOM_ROLE` (`po`, `sm`, `dev`),
   `LOOM_WORKSPACE` (the repo path, which the CLI sends to the daemon)
   and `LOOM_WORK_DIR` (the work log folder, §4).
4. **Settings.** The per-launch settings file gets:
   - allow rules: `Bash(<loom path> work *)` for everyone;
     `Bash(git push -u origin <branch>)`, `Bash(git push origin <branch>)`
     and `Bash(gh pr create *)` for a Developer, with its own branch
     name substituted; `Bash(gh pr ready *)` for the SM;
   - deny rules for a Developer: `Bash(git push origin <base>*)` and
     `Bash(git push --force*)`, where `<base>` is the workspace's base
     branch. Branch protection on GitHub is the real guard; the deny
     rule keeps an honest mistake from prompting;
   - `LOOM_WORK_DIR` in `sandbox.filesystem.allowWrite`.

The superseded spec's rules about a program string with its own
`--name` or `--settings`, the status bar warning when loom can't add its
settings, the one-account rule, and the Launch Options warnings for
another account or permission mode all carry over unchanged. The
permission-mode warning compares with the **SM's** launch command, since
the SM is who messages Developers.

### 3. Protocol text

Three embedded files, each about a page. Commands are written
`loom work …`; the composed file uses the absolute path.

**`po.md`**

- **Role.** The PO owns *what* and *why*. It never implements and never
  dispatches. It talks to the user, reads the GitHub board, and hands
  the SM a sprint.
- **Sprint initiation.** When prompted with goals and constraints
  (domain, priority, quantity), read `loom work board` for the current
  sprint, list open issues with `gh`, score them against the goals, and
  ask through `AskUserQuestion` whenever scope is ambiguous. Propose a
  shortlist with a one-line rationale per issue and a recommended size,
  through one `AskUserQuestion`.
- **Commitment.** On approval run
  `loom work sprint start --goal "<text>" --issue <n>… [--max-workers <k>]`,
  then message the SM: the goal, the issue list, and anything the user
  said that constrains order or scope.
- **During the sprint.** The daemon nudges the PO when a Developer lands
  or when the sprint completes. Reply to the user only with what changes
  the plan. Routine calls go to `loom work note`. Escalations go to the
  user through `AskUserQuestion`, with a recommendation, related
  questions batched.
- **Close.** When every issue has landed or been dropped, run
  `loom work sprint close "<summary>"` and tell the user.
- **Never** push, merge, or start a session.

**`sm.md`**

- **Role.** The SM owns *how* and *when*. It never implements. It orders
  the sprint, starts and stands down Developers, runs the landing gate,
  and keeps the board true.
- **Planning.** On a sprint message, read `loom work board`. For each
  issue decide its dependencies (technical, logical, shared files, shared
  versions or locks) and record them with
  `loom work plan --issue <n> [--after <m>…]`. An issue with no
  unlanded dependency is ready.
- **Dispatch.** For each ready issue, up to the sprint's worker cap and
  the host's load: write a brief to `$LOOM_WORK_DIR/briefs/<title>.md`
  (template below), then run
  `loom work spawn --title <t> --issue <n> --brief <file>`. It returns
  when the session is running.
- **Brief template.** Why the work matters; code locations checked at a
  named commit; invariants to keep; how to verify, naming the tests that
  must actually run; coordination: files other Developers own, the
  landing order, who bumps shared versions; hypotheses labelled as
  hypotheses.
- **Landing gate.** When a Developer declares `ready`, confirm before
  `loom work verify <title> "<notes>"` that: the branch fast-forwards
  the current base; the changes stay in scope; no lock or toolchain bump
  unless assigned; you have read the invariant at risk in the diff; the
  report says which tests actually ran. Then run `gh pr ready <n>`. If
  the gate fails, message the Developer what to fix; it declares `ready`
  again.
- **After review starts,** the user and the Developer own the PR. Don't
  re-verify review rounds.
- **Landing and cascade.** The daemon nudges when a PR merges. Then run
  `loom work release <title>`, re-read the board, and dispatch whatever
  that landing unblocked. A Developer whose issue is dropped is released
  with `--abandon "<reason>"`.
- **Decisions.** Routine calls go to `loom work note`; escalations to
  the user through `AskUserQuestion`, with a recommendation.
- **Shared resources and noise.** As in the superseded spec: one owner
  per shared version or lock; investigate the host before blaming a
  session; no acknowledgement-only messages.

**`dev.md`**

- **Start.** The first prompt points at the brief: read it. Run
  `loom work state working "<plan>"`. Stay within the brief. After a
  restart, read the brief again.
- **Build.** Write the failing tests first, implement, run the linters
  and the test suite the brief names.
- **Blocked.** `loom work state blocked "<on what>"`. Questions for the
  user go through `AskUserQuestion`.
- **Ready.** Write the report (branch and HEAD; files touched; tests run
  and skipped; evidence; judgement calls; every question put to the user
  with its answer). Commit, push your branch, open a **draft** PR whose
  body starts with `Closes #<n>`, then run
  `loom work state ready "<summary>" --report <file>`. Then stop; the SM
  reviews.
- **Review.** When nudged about review activity, read the PR comments,
  address them, push, and declare `ready` again with an updated report.
- **Never** push to the base branch, force-push, mark the PR ready, or
  bump a lock or toolchain file unless assigned.
- **Resources and hypotheses.** As in the superseded spec.

Project specifics stay in the project's CLAUDE.md.

### 4. The work log

Unchanged in mechanism from the superseded spec's §4: `log.jsonl` under
`<repo>/.loom/work/`, append-only, one JSON object per line, written
under an exclusive `flock` as one complete line, with the torn-line
rule; briefs in `briefs/<title>.md`, reports in `reports/<title>.md`.
The **daemon** is the writer now, on behalf of the CLI, so the lock
guards only against a hand edit and a future second daemon. The common
fields stay (`v`, `at`, `by`, `kind`).

| Kind | Written for | Fields |
|---|---|---|
| `sprint` | the PO | `action` (`start` or `close`), `goal`, `issues`, `max_workers`, `summary` |
| `plan` | the SM | `issue`, `after` (issue numbers), `summary` |
| `spawn` | loom, on the SM's request | `title`, `issue`, `brief` |
| `adopt` | the SM | `title`, `issue` (a hand-started Developer joins the sprint) |
| `state` | a Developer | `state` (`working`, `blocked`, `ready`), `summary`; for `ready` also `head`, `report` |
| `verify` | the SM | `target`, `head`, `notes` |
| `note` | the PO or the SM | `text` |
| `release` | loom, on the SM's request | `title`, `reason` (empty, or the abandon reason) |
| `remove` | loom | `title`, when an instance is killed by any other route |
| `nudge` | loom | `target`, `text`, so the board shows what loom told whom |

**Authority.** The daemon refuses, writing nothing, when:

- a `sprint` doesn't come from the PO, or `start` arrives while a sprint
  is open, or `close` while Developers are unreleased;
- a `plan`, `spawn`, `adopt`, `verify` or `release` doesn't come from the
  SM, or a `note` comes from a Developer;
- a `state` doesn't come from a Developer about itself;
- a `spawn` names an issue outside the open sprint, a title already in
  use, or would exceed `max_workers`;
- a `verify` targets a Developer that is not `ready`, or whose branch
  tip has moved since;
- a `release` targets a Developer that has not landed, without
  `--abandon`;
- the workspace is not in scrum mode.

The fold applies the same rules; entries that break them are skipped and
counted.

**Derived, never stored.**

- **`pr`:** the poller's state for the Developer's branch: none, draft,
  open, merged, closed, with review decision and checks.
- **`in review`:** `verified` and the PR is open (not draft).
- **`landed`:** the PR is merged, or (with no PR) the recorded `ready`
  HEAD has commits past the base commit and is an ancestor of
  `origin/<base>`.
- **Stale:** `ready` or `verified` and the branch tip no longer matches
  the recorded HEAD.
- **Queued:** in the sprint's `plan` with an unlanded `after`.
- **Ready to dispatch:** in the sprint, no unlanded `after`, no
  Developer.

**Lifetime.** `release` and `remove` forget the title and delete its
brief and report. `sprint close` keeps everything. `loom reset` deletes
the log with the workspace's instances.

### 5. The agent CLI

All `loom work` commands are daemon requests. Each needs
`LOOM_INSTANCE`, `LOOM_ROLE` and `LOOM_WORKSPACE` (`LOOM_WORK_DIR` is for
the agents' own file writes, not the CLI), and each refuses once the
workspace is no longer in scrum mode. A refusal
goes to stderr with a non-zero exit. With no daemon running, the CLI
spawns one.

| Command | Caller | Effect |
|---|---|---|
| `sprint start --goal <g> --issue <n>… [--max-workers <k>]` | PO | Opens the sprint. Default cap: 3. |
| `sprint close "<summary>"` | PO | Closes it. |
| `plan --issue <n> [--after <m>…] ["<summary>"]` | SM | Records an issue's dependencies. |
| `spawn --title <t> --issue <n> --brief <file>` | SM | Starts a Developer (the TUI's new-session path, in the daemon) with the one-line prompt pointing at the brief. Prints the final title. Returns when running. |
| `adopt <title> --issue <n>` | SM | Joins a hand-started Developer to the sprint. |
| `state <working\|blocked\|ready> "<summary>" [--report <file>]` | Developer | Declares its own state; `ready` records HEAD and copies the report. |
| `verify <title> "<notes>"` | SM | Marks a `ready` Developer verified at its HEAD. |
| `note "<decision>"` | PO, SM | Logs a routine decision. |
| `release <title> [--abandon "<reason>"]` | SM | Kills the Developer (worktree, tmux session, branch). |
| `board [--json] [<title>]` | any | The sprint: goal, each issue's plan state, Developer, work state, PR, nudges; or one Developer with brief, report and PR. |

`spawn` and `release` are long requests; the CLI waits for the reply, so
the SM's tool output shows success or the reason.

### 6. Work state

| State | Set by | Meaning |
|---|---|---|
| `queued` | derived | In the sprint, waiting on another issue. |
| `ready to dispatch` | derived | Unblocked, no Developer yet. |
| `working` | Developer | Started, with its plan. |
| `blocked` | Developer | Waiting on a decision, a landing or the host. |
| `ready` | Developer | Its checks passed; HEAD and report recorded; draft PR open. |
| `verified` | SM | Landing gate passed; PR marked ready for review. |
| `in review` | derived | PR open, non-draft. Review rounds re-enter `ready`. |
| `landed` | derived | PR merged. |
| `released` | loom | Developer stood down. |

### 7. Nudges

The daemon types one line into a session's agent pane through
`SendPrompt` when something it watches changes. Rules:

- **Only when idle.** A nudge is held until the session's status is
  `Ready` (roster or hooks). A line typed into a permission prompt could
  answer it; a line typed mid-turn is fine but pointless. Held nudges
  for one session coalesce into one line.
- **Logged.** Every nudge is a `nudge` entry, so the board shows it.
- **Developer:** its PR's review count or comment count rose, its
  review decision became changes-requested, or checks on its head
  failed. Text: `PR #42: new review activity, address it` (or
  `checks failing`).
- **SM:** a Developer declared `blocked` or `ready`; a PR merged or was
  closed unmerged; a Developer's session died and could not be
  relaunched.
- **PO:** a Developer landed; every sprint issue landed or was
  abandoned.

To see review and comment counts the GitHub poller's PR query adds
`comments`, `reviews` and `updatedAt` to the fields it already reads
(`number,headRefName,state,isDraft,reviewDecision,statusCheckRollup`).
In a scrum workspace the poller runs every 60s as today; nothing changes
for other workspaces.

### 8. The board

In a scrum workspace the overview groups cards under a **sprint
header** (goal, landed/total, open PRs) with the PO and SM cards first,
then Developers sorted needs-attention first, then placeholder cards for
queued and ready-to-dispatch issues. Each Developer card carries the
work state, its summary, its age and its PR badge; the line comes out of
the live tail as an issue link's does, so `overviewCardHeight` holds.
Rail cards show the work state on their second line. Agent-supplied text
passes through `sanitizeCardText`.

A **sprint tab** in the SM's workbench lists the plan as a dependency
list with each issue's state, and the `note` entries. Read-only: to
change a decision, the user tells the PO or SM. Before the board ships,
`loom work board` prints the same as text.

### 9. The inbox

As the superseded spec's §9: `PreToolUse` and `PostToolUse` with the
`AskUserQuestion` matcher capture pending questions onto the instance;
the inbox overlay (`!`) lists pending questions, permission prompts and
PRs awaiting the user's review across every workspace; `]`/`[` include
them; the status bar shows "needs you: N". It ships last, after the
`AskUserQuestion` payload probe.

### 10. Failure handling

| Situation | Behaviour |
|---|---|
| The CLI runs outside a scrum loom session | Refuses, naming the missing variable or the mode. |
| A command from the wrong role, or outside the authority rules | Refuses, writes nothing (§4). |
| No daemon | The CLI spawns one, as any client. |
| A Developer ignores the protocol | The board shows its runtime status and PR state only. |
| A Developer's session dies | The daemon relaunches it with `--resume` (existing crash restart). After the retry limit, the SM is nudged. |
| A staff session dies | Relaunched by the health tick, as the workspace terminal is today. |
| The branch moves after `ready` or `verified` | Stale on the board; `verify` refuses. |
| A PR is closed without merging | The SM is nudged; it releases with `--abandon` or asks the user. |
| A spawn fails after the daemon started the session | The daemon's existing failed-start path kills the instance and the CLI reports the error; nothing is logged as `spawn`. |
| The sprint cap is reached | `spawn` refuses, naming the cap; the SM waits for a release. |
| The mode is switched off mid-sprint | Nudges stop, the CLI refuses, staff keep running until killed. Turning it on again resumes from the log. |
| Loom's launch settings can't be added | As the superseded spec: the session runs, the CLI may prompt, a sandboxed Developer may be refused, and a `notice` names the session. |
| A Developer on another account or permission mode | As the superseded spec: messaging may fail or hold; the log and nudges still work; Launch Options warn. |
| A nudge target is never idle | The nudge is held and coalesced; the board shows it pending. |

## Testing

Everything in the superseded spec's testing list that concerns the log,
the fold, `landed` and stale, the launch changes and the Launch Options
warnings carries over. In addition:

- **Authority and derivation:** table tests for every refusal in §4,
  `queued`/`ready to dispatch` from a `plan`, `in review` from PR state,
  a `release` that forgets a title.
- **Spawn and release** through the in-process `Core`: a Developer
  started with the one-line prompt, the cap, the sprint-membership check,
  a failed start leaving no `spawn` entry, a release killing worktree and
  branch.
- **Nudges:** held while not `Ready`, coalesced, logged, and each
  trigger in §7 from a fake poller snapshot.
- **Staff auto-start:** turning the mode on creates and starts `po` and
  `sm`; a daemon start recreates a missing one; turning the mode off
  leaves them.
- **Settings:** the Developer's allow and deny rules carry its own
  branch and the workspace's base.
- **`tools/fakeagent`:** personas for `po`, `sm` and `dev` that drive a
  whole sprint: start, plan, spawn, state, verify, release, close. An
  end-to-end test runs it with no TUI open.
- **The opt-in real-Claude contract test** adds: a Developer in sandbox
  mode pushes its own branch and opens a draft PR without a prompt; the
  deny rule blocks a push to base; a nudge typed into an idle session is
  acted on.

## Documentation

- **CLAUDE.md:** the scrum mode, roles and the `role` field (schema v9);
  the work log as the one coordination store; nudges only into idle
  panes; the `AskUserQuestion` matcher exception.
- **USAGE.md:** the mode, the staff, `loom work`, the sprint lifecycle,
  the board, the inbox.
- **The protocol files** are reviewed like documentation.

## Rollout

After the daemon's stage 3. Each stage ships on its own and is tried on
kermit, with one sprint of two issues, before the next.

1. **Mode and staff:** `loom workspace mode`, the `role` field, staff
   auto-start and relaunch, the launch changes, the three protocol
   files, the work log in the daemon, and `state`, `verify`, `note`,
   `board`. The SM still asks the user to start Developers by hand.
2. **The sprint:** `sprint`, `plan`, `spawn`, `adopt`, `release`, the
   poller's extra fields, nudges. The cascade runs unattended.
3. **The board:** sprint header, work-state cards, placeholders, the
   sprint tab, the settings toggle.
4. **The inbox**, after the `AskUserQuestion` probe.

Success: the user's interactions in a sprint are the shortlist approval,
decisions the PO or SM escalates, PR reviews and merges. The board shows
every issue's state without a transcript. A sprint started in the
evening has moved by morning with loom closed.

## Out of scope

- Loom writing to GitHub (marking ready, merging, commenting).
- Automatic merges.
- Resource limits.
- Agents other than Claude Code.
- More than one sprint open per workspace.
- Stacking a Developer on another Developer's branch.
- Answering questions inside loom.
- Windows.

## Assumptions

The superseded spec's probes 1–3 (stable `--name`, messages don't cross
accounts, allow rules and `sandbox.filesystem.allowWrite`) carry over.

### Still to verify

| # | Assumption | How |
|---|---|---|
| 4 | `PreToolUse`/`PostToolUse` on `AskUserQuestion` carry the questions and a `tool_use_id`. | Probe before stage 4. |
| 5 | A deny rule `Bash(git push origin main*)` beats the allow rule `Bash(git push origin <branch>)` and blocks without a prompt. | Probe in stage 1, in a throwaway session. |
| 6 | A line typed into an idle Claude session's pane with `send-keys` is taken as a user turn, including under `--permission-mode` auto, and `gh pr list --json comments,reviews` returns counts cheaply. | Probe in stage 2. |

### Verified against the code on 2026-10-03

- The GitHub poller already folds `isDraft` into `PRDraft` and
  `reviewDecision` into `ReviewChangesRequested`
  (`session/github/rollup.go`), so draft, ready-for-review and
  changes-requested are visible without new parsing; only comment and
  review counts are new fields.
- `SendPrompt` (`session/agent_pane.go`) types a line into the agent
  pane, which the one-line brief prompt and nudges reuse.
- `IsWorkspaceTerminal` is consulted in 45 non-test places; the staff
  sessions reuse all of that behaviour, so the role adds a field, not a
  new instance kind.
