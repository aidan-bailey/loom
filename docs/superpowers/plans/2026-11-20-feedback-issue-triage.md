# Feedback Triage Implementation Plan

> **Supervised:** a skeleton; `/flow:draft` writes the stories, then `/flow:run` runs them.

**Goal:** Let the owner publish feedback threads, scrubbed, as `loom-feedback` issues that the issue watcher fixes, with each thread linked to its issue and closed when the issue closes.

**Architecture:** The `feedback` package gains the thread-to-issue link (`Thread.Issue`, status `published`, `Thread.Closed` with a reason; `feedback.json` version 2) and a pure scanner for private text. `core` gains the `PublishThread` request, a reason on `CloseThread`, and a gated 5-minute poll that reads linked issues' states with `gh` and closes their threads. The CLI adds `loom feedback publish`, `scan` and `close --reason`; the `F` view shows the link; the loom repo gains the watcher's config and prompts, and a `/feedback-triage` skill drives the owner's review.

**Tech Stack:** Go (cobra CLI, Bubble Tea TUI), the `gh` CLI, a Claude Code skill (Markdown).

**Spec:** `docs/superpowers/specs/2026-11-20-feedback-issue-triage-design.md`

**Budget:** draft $44; run $25 (estimate 2026-11-20)

## File structure

- `feedback/types.go`: `Published`, `Issue`, `Closure`, the `Thread` fields, `MaxCloseReason`.
- `feedback/link.go` (new): `ParseIssueRef`, `IssueURL`, `PublishedFixError`, `StatusText`, `Thread.NewNotes`.
- `feedback/inbox.go`: `Inbox.Publish`, `Inbox.Close` with a reason, `Inbox.CloseIssue`, `Inbox.Published`.
- `feedback/store.go`: `Version` 2.
- `feedback/rank.go`: `Row`'s `Issue`, `Closed`, `New`.
- `feedback/scan.go` (new): the scanner, `Scan`, its types.
- `core/iface.go`, `core/loop_core.go`, `core/feedback_requests.go`: `PublishThread`, `CloseThread`'s reason, `FixThread`'s refusal.
- `core/rpc/methods_gen.go`, `docs/specs/protocol.md`: regenerated.
- `session/github/issue_state.go` (new): `IssueState`, `IssueStateOf`.
- `core/feedback_issues.go` (new): the issue poll; `core/gate.go`, `core/tick.go`, `core/feedback.go` wire it in.
- `internal/cli/feedback.go`: `publish`, `close --reason`, the list and show changes; `internal/cli/feedback_scan.go` (new): `scan`.
- `session/loom_context_shared.md`: the context paragraph names `publish`.
- `app/feedback.go`, `ui/feedback.go`: the `F` view.
- `loom-watch.json`, `.loom-watch/start.md`, `.loom-watch/resume.md` (new): the watcher config; `watch/repo_config_test.go` (new) checks them.
- `.claude/skills/feedback-triage/SKILL.md` (new): the skill.
- Docs: `feedback/README.md`, `feedback/CLAUDE.md`, `USAGE.md`, `docs/ARCHITECTURE.md`, `docs/claude/the-feedback-inbox.md`, `CLAUDE.md`.

## Names contract

**feedback** (Story 1)

- `Published Status = "published"`: a thread linked to an issue. (Story 1)
- `Version = 2`: `feedback.json`'s version. (Story 1)
- `Issue { Repo string; Number int; URL string; At time.Time; Posted NoteID }`, JSON `repo`, `number`, `url`, `at`, `posted`: a thread's link; `Posted` is the newest note the issue or its comments cover. (Story 1)
- `Closure { At time.Time; Reason string }`, JSON `at`, `reason`: when and why a thread closed. (Story 1)
- `Thread.Issue Issue` (JSON `issue,omitzero`) and `Thread.Closed Closure` (JSON `closed,omitzero`). (Story 1)
- `MaxCloseReason = 300`: a close reason's bound in bytes, one line. (Story 1)
- `func ParseIssueRef(ref string) (repo string, number int, err error)`: accepts `https://github.com/<owner>/<name>/issues/<N>` and `<owner>/<name>#<N>`. (Story 1)
- `func IssueURL(repo string, number int) string`: `https://github.com/<repo>/issues/<N>`. (Story 1)
- `func (in *Inbox) Publish(t ThreadID, repo string, number int, through NoteID, now time.Time) error`: links and marks `published`; the same issue again only advances `Posted`, never backwards. (Story 1)
- `func (in *Inbox) Close(t ThreadID, reason string, now time.Time) error`: replaces `Close(t ThreadID) error`; stores `Closure`. (Story 1)
- `func (in *Inbox) CloseIssue(repo string, number int, reason string, now time.Time) []ThreadID`: closes every published thread linked to `repo#number`, returning their IDs. (Story 1)
- `func (in Inbox) Published() []Thread`: the published threads, by ID. (Story 1)
- `func (t Thread) NewNotes() int`: a published thread's notes with an ID past `Issue.Posted`, else 0. (Story 1)
- `func PublishedFixError(t Thread) error`: `thread <T> is published as <repo>#<N>; the issue watcher fixes it`. (Story 1)
- `func StatusText(r Row) string`: `open`; `fixing <fix title>`; `published #<N>`, plus ` +<k> new` when `k > 0`; `closed`. (Story 1)
- `Row.Issue Issue` (JSON `issue,omitzero`), `Row.Closed Closure` (JSON `closed,omitzero`), `Row.New int` (JSON `new,omitempty`). (Story 1)
- Error texts tests match (Story 1): `thread <T> doesn't exist`; `thread <T> is already closed`; `thread <T> is linked to <repo>#<N>`; `note <nN> is not in thread <T>`; `the close reason must be one line`; `the close reason is <n> bytes: at most 300`; `issue reference <ref>: want https://github.com/<owner>/<name>/issues/<N> or <owner>/<name>#<N>`.

**feedback scanner** (Story 5)

- `ScanKind string`, constants `ScanPath = "path"`, `ScanWorkspace = "workspace"`, `ScanSession = "session"`, `ScanIdentity = "identity"`, `ScanThread = "thread"`, `ScanEmail = "email"`, `ScanUUID = "uuid"`, `ScanToken = "token"`. (Story 5)
- `ScanTerms { Private []string; Workspaces []string; Sessions []string; Identity []string; Thread []string; Repo string }`: `Private` the path prefixes that are hits in any spelling given (home, every workspace's repository path, raw and canonical); `Workspaces` the names matched as whole words; `Sessions` the titles matched as whole word sequences; `Identity` the user, host, emails and account names; `Thread` the thread's session IDs, transcript paths and branches; `Repo` the own repository's top level. (Story 5)
- `ScanHit { Line int; Col int; Kind ScanKind; Match string }`: 1-based line and column. (Story 5)
- `func Scan(text string, terms ScanTerms, allow []string) (hits []ScanHit, allowed []ScanHit)`: `allowed` holds the hits `allow` dropped. (Story 5)

**session/github** (Story 3)

- `IssueState { Closed bool; Reason string }`: `Reason` is `completed`, `not planned` or `""`, from `stateReason`. (Story 3)
- `func IssueStateOf(ctx context.Context, dir string, repo string, number int, r internalexec.Executor) (IssueState, error)`: `gh issue view <N> -R <repo> --json state,stateReason` in `dir`, under `ghTimeout`. (Story 3)

**core** (Story 2)

- `Core.PublishThread(thread feedback.ThreadID, issue string, through feedback.NoteID, req ReqID)`: a request (no `rpc:` tag). (Story 2)
- `Core.CloseThread(thread feedback.ThreadID, reason string, req ReqID)`: `reason` added. (Story 2)
- `func (l *Loop) PublishThread(thread feedback.ThreadID, issue string, through feedback.NoteID, req ReqID)` and `func (l *Loop) CloseThread(thread feedback.ThreadID, reason string, req ReqID)`. (Story 2)

**core** (Story 3)

- `gateFeedbackIssues gateKind`, its `String()` `feedback_issues`, and `feedbackIssuesInterval = 5 * time.Minute` in `gateIntervals`. (Story 3)
- `feedbackIssueChecks = 10` and `feedbackIssueBudget = 60 * time.Second`. (Story 3)
- `func (m *Model) maybeCheckFeedbackIssues() bool`: called from the health tick in `core/tick.go`. (Story 3)
- `FeedbackView.IssueErrs map[feedback.ThreadID]string`: the poll's last error per published thread, deep-copied by `Clone`. (Story 3)

**CLI** (Story 4, Story 5)

- `loom feedback publish <thread> --issue <ref> --through <nN>`, both flags required; prints `[loom] thread <T> published as <repo>#<N>`. (Story 4)
- `loom feedback close <thread> [--reason "…"]`; prints `Closed thread <T>`. (Story 4)
- `loom feedback scan <file|-> [--thread <T>] [--allow <word>]…`: hits print as `<line>:<col> <kind>: <match>`, dropped ones as `allowed <line>:<col> <kind>: <match>`; exit 0 clean, 1 with a hit, 2 on an error, `no loom daemon` among them. (Story 5)

**Watcher config** (Story 7)

- `loom-watch.json`, `.loom-watch/start.md`, `.loom-watch/resume.md` at the repository root, with the contents spec §6 gives. (Story 7)

**Skill** (Story 8)

- `.claude/skills/feedback-triage/SKILL.md`, invoked as `/feedback-triage`. (Story 8)

## Stories

- **Story 1** — The thread-to-issue link in the feedback package.

  Adds the `published` status, `Thread.Issue` and `Thread.Closed`, the inbox methods that set them, the issue reference parser, the status text and the fix refusal both clients show, and `Row`'s new fields. `feedback.json` goes to version 2 so an older loom serves the inbox read-only instead of dropping the link. Pure logic, no model state.

  - **Developer:** intermediate-dev
  - **Files:** `feedback/types.go`, `feedback/link.go`, `feedback/link_test.go`, `feedback/inbox.go`, `feedback/inbox_test.go`, `feedback/store.go`, `feedback/store_test.go`, `feedback/rank.go`, `feedback/rank_test.go`, `feedback/README.md`
  - **Interfaces:** exposes every name under **feedback** (Story 1) in the names contract; uses none
  - **Verify:** `CGO_ENABLED=0 go test ./feedback/`
  - **Review:** deep
  - **Form:** full (store writes: the link, the close and the version bump)
  - **Reading:** spec §4.1, §4.2 (the refusals), §4.3 (the status text), §7; `feedback/types.go`, `feedback/inbox.go`, `feedback/store.go`, `feedback/rank.go`, `feedback/CLAUDE.md`

- **Story 2** — `PublishThread`, `CloseThread`'s reason and `FixThread`'s refusal on the wire.

  Adds the `PublishThread` request and the `reason` parameter to `CloseThread` in `core.Core`, the model's handlers (refusals, save, revision bump, Reply), the loop's forwards, and `FixThread`'s refusal of a published thread. Regenerates the wire and the protocol reference, adds the parity-test cases, and passes `""` as the reason at today's two `CloseThread` call sites (`app/feedback.go`, `internal/cli/feedback.go`) so the tree builds; the CLI flag is Story 4's.

  - **Developer:** intermediate-dev
  - **Files:** `core/iface.go`, `core/loop_core.go`, `core/feedback_requests.go`, `core/feedback_test.go`, `core/rpc/methods_gen.go`, `docs/specs/protocol.md`, `core/rpc/generate_test.go`, `core/state_views_test.go`, `core/rpc/client_test.go`, `app/feedback.go`, `internal/cli/feedback.go`
  - **Interfaces:** exposes `Core.PublishThread`, `Core.CloseThread(thread, reason, req)` and the loop forwards; uses `Inbox.Publish`, `Inbox.Close`, `ParseIssueRef`, `PublishedFixError` (Story 1)
  - **Verify:** `CGO_ENABLED=0 go test ./feedback/ ./core/ ./core/rpc/ ./app/`
  - **Review:** deep
  - **Form:** full (a wire change and store writes through the model)
  - **Reading:** spec §4.2, §7; `core/feedback_requests.go`, `core/iface.go:190-205`, `core/loop_core.go:275-290`, `docs/claude/adding-a-core-method.md`, `core/CLAUDE.md`

- **Story 3** — The issue poll that closes a thread when its issue closes.

  Adds `github.IssueStateOf` and a gated job on the health tick: every 5 minutes, at most 10 published threads' issues, least recently checked first (check times in memory), each `gh issue view` under `ghTimeout` in a 60 s budget, run in the feedback directory through the model's gh executor (`watchExec`). On delivery a `CLOSED` answer closes every thread linked to that issue with `issue #<N> closed (<reason>)` and saves; a failure changes no status and records the error in `FeedbackView.IssueErrs`, bumping the inbox revision so clients see it. Documents the poll's `gh` calls and `feedback.json` version 2 in `docs/ARCHITECTURE.md`.

  - **Developer:** intermediate-dev
  - **Files:** `session/github/issue_state.go`, `session/github/issue_state_test.go`, `core/feedback_issues.go`, `core/feedback_issues_test.go`, `core/gate.go`, `core/gate_test.go`, `core/tick.go`, `core/feedback.go`, `docs/ARCHITECTURE.md`
  - **Interfaces:** exposes `IssueState`, `IssueStateOf`, `FeedbackView.IssueErrs`; uses `Inbox.Published`, `Inbox.CloseIssue` (Story 1)
  - **Verify:** `bash -c 'CGO_ENABLED=0 go test ./session/github/ ./core/ && CGO_ENABLED=0 go test -tags e2e ./e2e/...'`
  - **Review:** deep
  - **Form:** full (outside calls with timeouts, a per-poll bound, a gated job)
  - **Reading:** spec §5, §7; `docs/claude/adding-a-gated-job.md`, `core/gate.go`, `core/tick.go:100-150`, `core/feedback.go`, `core/feedback_group.go`, `core/watch_poll.go:110-130`, `session/github/cli.go:40-60,145-160`, `core/CLAUDE.md` (GitHub poller, gated jobs)

- **Story 4** — `loom feedback publish`, `close --reason`, and the link in `list` and `show`.

  Adds the `publish` subcommand and the `--reason` flag on `close`, shows each row's status through `feedback.StatusText`, prints the issue URL, `posted`, the close reason and the poll's error in `show`, and carries `issue`, `closed` and `new` in `list --json`. The context paragraph in `session/loom_context_shared.md` names `publish` among the user's commands, and `TestLoomContext_NamesRealSessionFlags` covers it. `USAGE.md` documents both commands.

  - **Developer:** intermediate-dev
  - **Files:** `internal/cli/feedback.go`, `internal/cli/feedback_test.go`, `session/loom_context_shared.md`, `internal/cli/loom_context_test.go`, `USAGE.md`
  - **Interfaces:** exposes the `publish` and `close --reason` commands; uses `Core.PublishThread`, `Core.CloseThread` (Story 2), `StatusText`, `Row`'s fields (Story 1), `FeedbackView.IssueErrs` (Story 3)
  - **Verify:** `CGO_ENABLED=0 go test ./internal/cli/`
  - **Review:** light
  - **Form:** brief (plumbing flags and output through the requests Stories 2 and 3 add)
  - **Reading:** spec §4.3; `internal/cli/feedback.go`, `internal/cli/loom_context_test.go`, `session/loom_context_shared.md`, `internal/cli/CLAUDE.md`

- **Story 5** — `loom feedback scan`, the backstop against private text.

  Adds the pure scanner (`feedback.Scan`) and the `scan` subcommand that gathers its terms from the daemon (served workspaces and their sessions, the thread's notes with `--thread`), the global config (accounts), the environment (`$HOME`, `$USER`, the host name) and `git config user.email`, and the own workspace from the current directory's canonical path. Each kind of spec §3.2's table is its own case. `USAGE.md` and `feedback/README.md` document it.

  - **Developer:** intermediate-dev
  - **Files:** `feedback/scan.go`, `feedback/scan_test.go`, `internal/cli/feedback_scan.go`, `internal/cli/feedback_scan_test.go`, `internal/cli/feedback.go`, `USAGE.md`, `feedback/README.md`
  - **Interfaces:** exposes every name under **feedback scanner** (Story 5) and the `scan` command; uses `Core.Feedback`, `Core.Workspaces` and the served sessions' views through the CLI's daemon client
  - **Verify:** `CGO_ENABLED=0 go test ./feedback/ ./internal/cli/ -run Scan`
  - **Review:** deep
  - **Form:** full (bounds and matching rules a leak depends on)
  - **Reading:** spec §3.1, §3.2; `internal/cli/feedback.go`, `internal/cli/CLAUDE.md`, `account/registry.go`
  - **Drafts after:** Story 4

- **Story 6** — The `F` view shows the link and refuses `x` on a published thread.

  Renders each row's status through `feedback.StatusText` (`published #57 +2 new`), shows a published thread's issue URL and the poll's error when it is expanded, and makes `x` on a published thread show `feedback.PublishedFixError` on the view's message line without sending `FixThread`. `c` keeps closing with no reason.

  - **Developer:** intermediate-dev
  - **Files:** `app/feedback.go`, `app/feedback_test.go`, `ui/feedback.go`, `ui/feedback_test.go`
  - **Interfaces:** uses `StatusText`, `PublishedFixError`, `Row`'s fields (Story 1), `FeedbackView.IssueErrs` (Story 3)
  - **Verify:** `CGO_ENABLED=0 go test ./app/ ./ui/`
  - **Review:** light
  - **Form:** brief (rendering and one key's gate)
  - **Reading:** spec §4.3; `app/feedback.go`, `ui/feedback.go`, `app/CLAUDE.md`, `ui/CLAUDE.md`

- **Story 7** — The issue watcher's config and prompts for the loom repository.

  Adds `loom-watch.json`, `.loom-watch/start.md` and `.loom-watch/resume.md` with spec §6's contents, and a test that parses the repository's own config with `watch.ParseConfig`, reads both prompt files, checks each is at most 64 KiB, and renders each with `watch.RenderPrompt` and the variables the watcher supplies (`issue`, `issue_title`, `issue_url`, `pr`, `pr_url`, `branch`, `base`), so a bad edit fails `go test` before it reaches `origin/main`.

  - **Developer:** junior-dev
  - **Files:** `loom-watch.json`, `.loom-watch/start.md`, `.loom-watch/resume.md`, `watch/repo_config_test.go`
  - **Interfaces:** none
  - **Verify:** `CGO_ENABLED=0 go test ./watch/ -run TestRepoWatchConfig`
  - **Review:** light
  - **Form:** brief (config and prompt text the spec gives in full, and one test)
  - **Reading:** spec §6; `watch/config.go:90-120`, `watch/prompt.go`, `watch/CLAUDE.md`

- **Story 8** — The `/feedback-triage` skill and the rules it rests on.

  Writes the skill spec §2 describes: preflight, escalations, published threads with new notes, open threads one question each, ungrouped notes, the summary; §2.1's lost-link recovery and publish order; §3.1's draft shape and §3.2's scan before every `gh` write, with `--allow` only for a word the owner names. Adds the three rules of spec §10 to `feedback/CLAUDE.md`, the model's side (the link, the poll) to `docs/claude/the-feedback-inbox.md`, and the skill to the root `CLAUDE.md`'s Claude Code automation line.

  - **Developer:** intermediate-dev
  - **Files:** `.claude/skills/feedback-triage/SKILL.md`, `feedback/CLAUDE.md`, `docs/claude/the-feedback-inbox.md`, `CLAUDE.md`
  - **Interfaces:** uses the `publish`, `close --reason`, `list`, `show` (Story 4) and `scan` (Story 5) commands and the watcher config (Story 7)
  - **Verify:** `CGO_ENABLED=0 go test ./tools/claudemd/`
  - **Review:** light
  - **Form:** brief (a skill and docs)
  - **Reading:** spec §2, §2.1, §3, §8, §10; `.claude/skills/loom-dev/SKILL.md`, `docs/claude/the-feedback-inbox.md`, `feedback/CLAUDE.md`
  - **Drafts after:** Story 4, Story 5, Story 7

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
