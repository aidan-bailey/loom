# Feedback from sessions: a loom inbox agents write to

Date: 2026-11-05. Status: design approved by the owner, not yet planned.

## 1. Purpose

Loom's session agents now use loom's own tooling every day: `loom session list/new/send`, `loom job submit/run/wait`, the appended loom context, the daemon's notices. They are the first to hit a misleading flag, a refusal with no reason, or a context sentence that no longer matches the code. That knowledge dies with their turn today.

This design gives agents one command to file a note about loom while they still have the context. The daemon groups notes into threads, the owner reads them ranked by how many sessions hit each, and one key turns a thread into a loom session that fixes it.

**Success measure, read after two weeks of use:** how many threads arrived, how many sessions reported each, how many fix sessions were started, and how many groupings the call log shows were wrong.

## 2. Owner decisions

- **Source:** the agent files a note in the moment. No end-of-session retro and no passive transcript mining in v1.
- **Destination:** a local inbox owned by the daemon. Nothing leaves the machine.
- **Repeats:** a headless model groups each note into an open thread, or opens a new one. Recurrence is the priority signal.
- **Closing the loop:** start a fix session from a thread. No fixed-in-build tracking, no telling reporters, no won't-fix state.
- **Surfaces:** CLI and TUI. The PWA comes later.
- **Approach A:** agents file through `loom feedback add` from Bash. A mod-provided tool (approach B) may come later on the same store; approach C (agents writing files a sweep reads) breaks the single owner of state.

## 3. Capture: `loom feedback add`

```
loom feedback add --kind bug|friction|missing|docs \
  --title "<one line>" --text "<what happened; what you expected; the command>"
loom feedback add --kind … --title "…" --file <path>
```

- `--kind`, `--title` and one of `--text`/`--file` are required. Kinds mean: `bug` (loom did something wrong), `friction` (it worked, but awkwardly or surprisingly), `missing` (a capability the agent needed), `docs` (the loom context or a loom message says something untrue or unclear).
- The CLI sends `$LOOM_LAUNCH` (`claudemod.EnvLaunch`), as `loom job submit` does. The daemon resolves it to the owning session and stamps the note itself: instance ID, title, workspace, branch, the subagent when one filed it, the daemon's version and commit, the Claude session ID, and the time. The agent supplies none of these.
- It prints `[loom] feedback nN filed` and returns at once. Grouping happens later, so filing never blocks the agent.
- **Fail closed:** with no daemon, no `$LOOM_LAUNCH` or a launch the daemon can't resolve, the command errors with the reason and nothing is stored. A note is never attributed to a guessed session.
- **The subagent** comes from the mod's call reports, as a job's does: `claudemod.JobCallPattern` widens to `loom feedback add`, and the daemon matches the one in-flight call on the launch whose command holds `feedback` and `add`. None, or more than one, records no subagent (amended while planning).
- **The model and effort** the session ran at (the owner's addition, 2026-11-05): the model from Claude's session info, which the mod reads with `$.session.model()` (the main loop's model, as `/model` shows it) and sends with its call report, so the daemon stamps it from the matched call; the effort from `$CLAUDE_EFFORT`, which Claude Code sets in every Bash call's environment (the turn's effort after any downgrade), sent by the CLI. No matched call records no model; an effort that isn't one of Claude's levels records none.
- **The build** is the daemon's: `core.Options.Build`, which `serve.go` sets from main's `version` and `rpc.Build()` (amended while planning; `core` can't import `rpc`).
- **The limits:** a kind from the four, a title of 1–120 characters on one line, a text of 1–20,000 bytes, checked by the model (`feedback.ValidateNote`).

### The context paragraph

`session/loom_context_shared.md` gains a paragraph, so the main session, worktree sessions and subagents all see it:

> **Tell loom what got in your way.** When loom's own tooling (`loom session`, `loom job`, the daemon, this context's text) fails, surprises you, contradicts what this context says, or lacks something you needed, file a note: `loom feedback add --kind bug|friction|missing|docs --title "…" --text "…"`, saying what happened, what you expected and the command. Report on the loom you run, not on loom code you are editing, and not your task's own problems. `loom feedback list` and `show` are yours to read before filing; `fix` and `close` are the user's. Then carry on: one line to the user is enough.

`TestLoomContext_NamesRealSessionFlags` (`internal/cli`) extends to the `loom feedback` subcommands and flags this paragraph names.

## 4. The store

`<global config dir>/feedback/feedback.json`: one per host, because feedback about loom is host-wide. Each note still records its workspace.

- The daemon's model owns it and saves it as it saves `jobs.json`: on every change, through the model's save path, never by a client.
- The file is versioned as `jobs.json` is (amended while planning): a file with a newer version than this loom reads is left untouched and the inbox refuses writes, so a downgrade never overwrites what a newer loom wrote.
- If the file exists but can't be decoded, the daemon leaves it alone, serves an empty inbox that refuses writes, and reports the error by Notice. It never overwrites a file it couldn't read.

**A note** has:
- `N`, counted per host (like job numbers);
- kind, title, text;
- the session: instance ID, title, workspace name, branch, subagent ID or empty, the model and effort;
- the loom version and commit, and the Claude session ID;
- `at`;
- `thread` (0 while ungrouped), and a grouping state: `pending`, `grouped`, or `ungrouped` (failed, will retry).

**A thread** has:
- `T`, counted per host, and a title (from the grouper);
- a kind (the first note's);
- a status: `open`, `fixing` or `closed`;
- its note IDs;
- the fix session's instance ID and title once one is started;
- `first` and `last` times.

IDs are counted, not derived from disk. The `stableID` rule covers instances and workspaces because clients rejoin and keep naming them; feedback IDs, like job numbers, are only ever assigned by the one daemon.

## 5. Grouping

A gated background job in the model (`gateFeedback`, dispatched through `dispatchGated`), with one call in flight at a time. Its builder returns nil when no note is `pending` or due for a retry.

- **The request** is JSON: the note (kind, title, text), plus every thread whose status is not `closed`, each with its ID, title, kind and its notes' titles plus the first ~300 characters of each text.
- **The answer** comes back under a JSON schema: `{"thread": T or 0, "title": "<title for a new thread>", "why": "<one sentence>"}`. A 0, or a `T` that is unknown or closed, opens a new thread under `title`; an empty `title` takes the note's own title.
- **The runner** is the advisor's (`sched/advisor`'s `Run`): `claude -p` with a model, an effort, a schema and a system prompt. `advisor.Call` gains a `SystemPrompt` field (today `run.go` hard-codes the advisor's), so the grouper reuses the runner instead of copying it. The grouper's system prompt lives in the `feedback` package as an embedded `prompt.md`. The call keeps the advisor's lockdown and runs in an empty `feedback/grouper/` directory, so its read-only tools reach nothing (amended while planning: notes are untrusted text).
- **Its settings' bounds** are the advisor's: `advisor.Resolve` becomes `advisor.ResolveBlock(key, block)`, so a bad `feedback.grouper` value names its own key (amended while planning).
- **The settings** go in the global `config.json`'s `feedback` block:
  - `grouper`, with the same shape as `scheduler.agent` (`config.AgentSettings`: `enabled`, `model`, `effort`, `account`);
  - defaults: enabled, Haiku 5.5, low effort;
  - it runs in the feedback directory, on the account environment the advisor resolves.
- **Fail closed:**
  - a failed call marks the note `ungrouped`, which the inbox shows as its own row, and retries it no sooner than 10 minutes later (the advisor's `advisorRetryAfter`);
  - 3 failed calls in a row (the advisor's `advisorPauseAfter`) pause the grouper with a Notice for every client, until the daemon restarts or the `feedback.grouper` setting changes;
  - a disabled grouper leaves every note `ungrouped`;
  - no note is ever dropped or merged on a guess.
- **The call log** is `feedback/grouper.jsonl`, one line per call: the note, the request's thread IDs, the model, effort, usage, the raw answer and `why`. It is what §1's measure and any later model replay read.

Because only threads that are not closed are offered, a closed problem that recurs opens a new thread, which is the regression signal without a fixed-in-build field. The request also stays small as history grows.

## 6. The wire

New `Core` methods in `core/iface.go`, all additions, so `rpc.Protocol` doesn't change:

- `FileFeedback(req, FeedbackNote)`: a request. It resolves the launch, stamps the note, saves, and arms the gate. Its Reply carries `N`. It refuses an unresolvable launch or a store that refused its load.
- `CloseThread(req, T)`: a request. Unknown or already closed → refused (`ErrRefused`).
- `FixThread(req, T, workspace)`: a request; see §7.
- `Feedback()`: a local query returning the inbox and the grouper's state (`FeedbackView`), published as a new state event `FeedbackChanged` the way `QueueChanged` publishes the queue: on a revision change, coalesced by the server, kept by the replica, applied by the TUI (amended while planning). The guide is `docs/claude/adding-a-core-event.md`.
- `Reply` gains `Note`, the filed note's ID.

The `TestMethods_CoverCore`, `TestStateViews_AnswerAsTheModel` and `TestReplica_AnswersAsTheModel` cases, the generated `core/rpc/methods_gen.go`, and the regenerated `docs/specs/protocol.md` come with them. The guide is `docs/claude/adding-a-core-method.md`.

## 7. The fix session

`FixThread` resolves the workspace: the request's own, else `feedback.workspace` in the global `config.json`. With neither, or a name no served workspace has, it refuses with a reason naming the setting.

It then makes a `Create` in that workspace, as `loom session new --prompt` does, on the workspace's default launch options:
- **Title:** `fb-T-<slug of the thread title>`, cut to `core.MaxTitleWidth` (32 cells).
- **Prompt** (composed by the `feedback` package):
  - the thread's title and kind;
  - every note, with its kind, text, session title, workspace, and loom version and commit;
  - for each note, where its reporter's transcript lives: the Claude session ID, with both places to look (`~/.claude/projects/…`, and loom's `archive/chats/` once the session has stopped);
  - the opening instruction: "Reporters may run an older loom than this branch. First check whether the base branch already fixes this. If it does, say so and stop. If not, reproduce it, then propose a fix to the user before building it."

The model composes the `Create` itself, as `loom session new` does: the workspace's program, `launch.FromSettings` and `ChooseLaunchAccount`, and a refusal when remote control is blocked for the chosen account. It marks the thread `fixing` with the new instance's ID, title and workspace as the `Create` is made (amended while planning: a failed start removes its instance, which leaves a `fixing` thread whose session is gone, fixable again as below). The Reply and `Started` reach the requester as a `Create`'s do.

`FixThread` refuses a closed thread, and a `fixing` thread whose fix session still exists (the refusal names it). A `fixing` thread whose session is gone (killed without a fix) can be fixed again, and the new session replaces the old one on the thread.

Notes keep attaching to a `fixing` thread, because the grouper offers every thread that isn't closed. It stays `fixing` until the owner closes it: no auto-close on merge, push or kill.

## 8. The CLI

`loom feedback` in `internal/cli`, as a client of the daemon:

| Command | What it does |
|---|---|
| `add …` | §3 |
| `list [--all] [--json]` | Threads that aren't closed, plus ungrouped notes as `nN` rows. Ranked by distinct reporting sessions, then by the latest note. Columns: `T`, kind, sessions, notes, last, status, title. `--all` adds closed threads. |
| `show T` / `show nN` | One thread with every note in full, or one note. |
| `fix T [-w <workspace>]` | §7. Prints the session's title and branch. |
| `close T` | Closes the thread. |

Ranking counts distinct sessions, not notes, so one session filing five notes about the same thing doesn't outrank a problem three sessions hit once each.

## 9. The TUI

- **`F`** (unbound in `script/defaults.lua` today) toggles the feedback view, a Lua action `toggle_feedback_view`, built as the work view is.
- **Inside the view:**
  - the ranked thread list, as `loom feedback list` shows it;
  - `enter` expands a thread to its notes;
  - `x` asks to start a fix session, with a confirm line naming the target workspace (or the missing setting);
  - `c` closes the thread;
  - `esc` leaves the view.
  - A `fixing` thread shows its session's title and status beside it.
- **The chrome:** while threads are open, the status line of focus and overview starts `F feedback N ·` (amended while planning: the status line is the chrome's one place for it). A note arriving raises no toast.
- `x` leaves the view for focus before it sends `FixThread`, so the start's inline attach lands in focus, not under the view (amended while planning).
- **Keymap:** `USAGE.md` and the help list get the key; its truth is `script/defaults.lua`.

## 10. Package layout

- **New `feedback/` package:** the store types, load and save (versioned, unknown records kept), the grouper's request and answer, its prompt and schema, the fix prompt's composition, and ranking. Pure logic, no model state. Its `CLAUDE.md` holds the rules below; its `README.md` holds the facts.
- **`core`:** the gated job, `FileFeedback`/`CloseThread`/`FixThread`/`Feedback`, the save, Notices. `core/CLAUDE.md` has 29 bytes free, so core-side rules go in `feedback/CLAUDE.md` or a `docs/claude/the-feedback-inbox.md` guide indexed in `docs/claude/INDEX.md`. Check the budgets' headroom when planning.
- **`sched/advisor`:** `Call.SystemPrompt`.
- **`config`:** the `feedback` block (`workspace`, `grouper`), cloned like `scheduler`'s.
- **`internal/cli`:** `loom feedback`.
- **`app`, `ui`, `script`:** the view, the badge, the action and its key.

### Rules to write down

- The daemon alone writes `feedback.json`; an unreadable one is left alone and refuses writes.
- No note is dropped: a failed or disabled grouper leaves it `ungrouped`, shown and retried.
- The grouper offers only threads that are not closed, and a closed or unknown answer opens a new thread.
- A note's session, build and transcript fields come from the daemon's resolution of `$LOOM_LAUNCH`, never from the agent's text.

## 11. Testing

- **`feedback`:**
  - store round trip; unknown records and fields kept; an undecodable file reported, not replaced;
  - ranking by distinct sessions;
  - the grouper's request offers no closed thread, and its answer parsing (unknown `T`, closed `T`, empty title);
  - the fix prompt holds every note's text, build and transcript pointer.
- **`core`:**
  - a note is filed, attributed from its launch and saved; an unknown launch is refused;
  - grouping through the advisor's fake-`claude` seam: attach to an open thread, a new thread on 0 or closed or unknown `T`, `ungrouped` and a retry on failure, a pause with a Notice after repeated failures, `ungrouped` when disabled;
  - `gateFeedback` cases in `TestProductionGatedJobsYieldOneResult` and `TestGatedDeliveryDisarmsFirst`;
  - `FixThread`: the Create lands in the resolved workspace with the composed prompt, the thread moves to `fixing` on `Started` and stays `open` on a failed start, and an unknown thread or missing workspace is refused;
  - `CloseThread`'s refusals.
- **Wire:** the three parity tests' cases, `TestGenerated_IsFresh`, `TestProtocolReference`.
- **`internal/cli`:** each subcommand against a test daemon; `add` without `$LOOM_LAUNCH`; the extended context-flags test.
- **TUI:** the view renders a ranked list and an expanded thread, its keys map to the right requests, and the badge shows and hides.
- **Opt-in live contract:** `go test -tags feedback_live ./feedback/...` runs real Haiku on three notes (two about one problem, one about another), and expects two threads.
- **Gates:** full suite and race through `loom job`, and `go test -tags e2e ./e2e/...` since the daemon gains requests.

## 12. Out of scope (v1)

- The PWA view and a push kind for new threads.
- A mod-provided feedback tool (approach B); it would write to the same store.
- End-of-session retros, and passive mining of transcripts and job records.
- Telling reporters when a thread is fixed; forwarding new notes to a running fix session.
- Fixed-in-build tracking and a won't-fix status.
- Editing a thread's title, or moving notes between threads by hand.
