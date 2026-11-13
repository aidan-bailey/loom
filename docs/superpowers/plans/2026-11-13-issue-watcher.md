# Issue watcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A daemon-side watcher that starts one loom session per labelled GitHub issue, nudges it when its PR goes red, conflicts or the issue changes, escalates when it is stuck, and kills it once the PR merges, so polyoxide can drop its hourly systemd poller.

**Architecture:** A pure package `watch/` holds the config, the title template, the memory file, the hook runner and `Decide`, the rule table. `core` gets a watcher gate on the health tick: a poll job reads git, `gh` and the hook off the loop, `Decide` runs on the loop, and its actions go out as Jobs through the existing create, send and kill paths. New `Core` requests and `loom watch` switch it on and show it; escalations reach the PWA through the workspace view.

**Tech Stack:** Go, the `gh` CLI through `internalexec.GhCommand`, git through `internalexec.GitCommand`, cobra, testify.

**Spec:** `docs/superpowers/specs/2026-11-13-issue-watcher-design.md`

---

## File structure

- `watch/` (new, pure): `config.go` (`Config`, `ParseConfig`), `title.go` (`TitleTemplate`), `prompt.go` (`RenderPrompt`, `PromptFooter`), `memory.go` (`Memory`, `LoadMemory`, `SaveMemory`), `types.go` (`Session`, `Nudge`, `Action`, `Snapshot`), `nudge.go` (`NudgeFor`, `IssueSeen`), `decide.go` (`Decide`), `hook.go` (`RunStart`, `RunNudges`), their `_test.go` files, `testdata/`, `CLAUDE.md`, `README.md`.
- `session/github/watch.go` (new): the watcher's reads and writes; `session/github/watch_test.go`, `session/github/testdata/watch_*.json`.
- `session/git/util.go`: `sanitizeBranchName` exported as `SanitizeBranchName`. `session/git/worktree.go`, `session/git/worktree_ops.go`: `WorktreeSpec.BaseRef`. `session/git/watch_checkout.go` (new): `FetchBranch`, `ShowFile`, `SyncDetachedWorktree`.
- `session/instance.go`: `InstanceOptions.BaseRef`, threaded to the worktree spec, never persisted.
- `core/requests.go`: `NewInstance.BaseRef`. `core/gate.go`: `gateWatch`. `core/watch.go`, `core/watch_poll.go`, `core/watch_actions.go` (new) with tests. `core/mod.go`: `OriginWatch`. `core/iface.go`, `core/events.go`, `core/workspace_view.go`: the requests, `Reply.Watch` and `WorkspaceView.Watch`. `core/rpc/methods_gen.go` and `docs/specs/protocol.md` regenerated.
- `config/settings.go`: `Settings.Watch`.
- `internal/cli/watch.go` (new), `main.go`: `loom watch`.
- `internal/web/triggers.go`: the escalation push.
- `docs/specs/watch.md` (new), `USAGE.md`, `docs/ARCHITECTURE.md`, `core/CLAUDE.md`.
- `e2e/watch_test.go` (new).

## Names contract

**session/git** (Story 1)

- `func SanitizeBranchName(s string) string` (Story 1): today's `sanitizeBranchName`, exported; every caller renamed.

**watch config and templates** (Story 1)

- `const ConfigFile = "loom-watch.json"` (Story 1)
- `type Config struct { Version int; Label string; EscalateLabel string; Title string; Cap int; Stuck time.Duration; NudgeLimit int; NudgeGap time.Duration; IssueChanged bool; LogTail int; Session SessionConfig; Hook *HookConfig }` (Story 1); JSON keys as spec §3.1, durations as Go duration strings.
- `type SessionConfig struct { Model string; Effort string; PermissionMode string; Account string; Prompt string; ResumePrompt string }` (Story 1)
- `type HookConfig struct { Command []string; Timeout time.Duration }` (Story 1)
- `func ParseConfig(data []byte) (Config, error)` (Story 1): applies the defaults and the bounds of spec §3.1 and §7.1; any fault is an error naming the key.
- `type TitleTemplate struct` (Story 1) (opaque)
- `func ParseTitleTemplate(tmpl string) (TitleTemplate, error)` (Story 1)
- `func (t TitleTemplate) Render(issue int, vars map[string]string) (string, error)` (Story 1): refuses a missing or ill-formed variable and a title wider than 32 cells.
- `func (t TitleTemplate) Parse(title string) (issue int, vars map[string]string, ok bool)` (Story 1)
- `func (t TitleTemplate) BranchPattern(prefix string, issue int) *regexp.Regexp` (Story 1): built with `git.SanitizeBranchName`.
- `func (t TitleTemplate) Vars() []string` (Story 1): the variable names other than `issue`, in template order.
- `func RenderPrompt(tmpl string, vars map[string]string) (string, error)` (Story 1): `{name}` substitution; an unknown name is an error.
- `func PromptFooter(issue int) string` (Story 1): the fixed footer of spec §3.3.

**watch memory** (Story 2)

- `const MemoryFile = "watch.json"` (Story 2): under the workspace's config dir.
- `type Memory struct { Version int; PRs map[int]PRMemory; Issues map[int]IssueMemory }` (Story 2), plus unexported unknown top-level keys written back.
- `type PRMemory struct { Nudges map[string]NudgeRecord; Settled bool }` (Story 2)
- `type NudgeRecord struct { N int; Key string; At time.Time }` (Story 2)
- `type IssueMemory struct { Escalated *Escalation; Seen string }` (Story 2)
- `type Escalation struct { At time.Time; Reason string }` (Story 2)
- `func LoadMemory(path string) (Memory, error)` (Story 2): a missing file is empty memory; an unreadable, undecodable or newer file is an error and the file is left alone.
- `func SaveMemory(path string, m Memory) error` (Story 2): atomic, through `config`'s atomic write.
- `func (m *Memory) RecordNudge(pr int, kind string, key string, at time.Time) int` (Story 2): returns the new count.
- `func (m *Memory) RecordEscalation(issue int, reason string, at time.Time)` (Story 2)
- `func (m *Memory) Settle(prs []int)` (Story 2)
- `func (m *Memory) SetSeen(issue int, seen string)` (Story 2)

**session/github watcher calls** (Story 3)

- `type WatchPR struct { Number int; State string; HeadRefName string; HeadSHA string; BaseSHA string; Mergeable string; Author string; CrossRepo bool; Closes []int; Failing []FailedCheck; Body string; URL string }` (Story 3); `State` is `OPEN`, `CLOSED` or `MERGED`.
- `type FailedCheck struct { Name string; URL string; JobID int64 }` (Story 3): a check concluded `FAILURE`, `TIMED_OUT` or `ERROR`; `JobID` 0 when its URL names no Actions job.
- `type WatchIssue struct { Number int; Title string; Body string; URL string; Labels []string; Closed bool; Comments []IssueComment }` (Story 3)
- `type IssueComment struct { ID string; Author string; Association string; Body string; CreatedAt time.Time }` (Story 3)
- `func WatchPRs(ctx context.Context, repoDir string, r internalexec.Executor) ([]WatchPR, error)` (Story 3): `gh pr list --author @me --state all --limit 100`.
- `func WatchIssues(ctx context.Context, repoDir string, label string, r internalexec.Executor) ([]WatchIssue, error)` (Story 3): `gh issue list --label <label> --state open --limit 50`.
- `func ViewWatchIssue(ctx context.Context, repoDir string, number int, r internalexec.Executor) (WatchIssue, error)` (Story 3): one issue, any state, with comments.
- `func Viewer(ctx context.Context, repoDir string, r internalexec.Executor) (string, error)` (Story 3): `gh api user`, the login.
- `func FailedJobLogs(ctx context.Context, repoDir string, jobID int64, lines int, r internalexec.Executor) (string, error)` (Story 3)
- `func ClosePR(ctx context.Context, repoDir string, number int, comment string, r internalexec.Executor) error` (Story 3)
- `func CommentIssue(ctx context.Context, repoDir string, number int, body string, r internalexec.Executor) error` (Story 3)
- `func AddLabel(ctx context.Context, repoDir string, number int, label string, r internalexec.Executor) error` (Story 3): creates the label when GitHub reports it missing, then adds it.

**watch types and nudges** (Story 4)

- `type Session struct { ID uint64; Title string; Status string; Since time.Time; Branch string; Issue int }` (Story 4); `Status` is loom's status name (`Running`, `Ready`, `Loading`, `Paused`, `Prompting`, `Deleting`, `Recoverable`).
- `type Nudge struct { Kind string; Key string; Text string }` (Story 4)
- `type ActionKind string` (Story 4) with constants `ActStart`, `ActKill`, `ActClosePR`, `ActSend`, `ActEscalate`, `ActWait` (Story 4)
- `type Action struct { Kind ActionKind; Issue int; Session uint64; PR int; Branch string; Resume bool; Nudge Nudge; N int; Reason string; Settle []int }` (Story 4)
- `type Snapshot struct { Now time.Time; Viewer string; Prefix string; Issues []github.WatchIssue; PRs []github.WatchPR; Sessions []Session; HookNudges map[int][]Nudge; HookFailed map[int]string; Considered int; Unconsidered int }` (Story 4); hook maps keyed by issue number.
- `func IssueSeen(is github.WatchIssue) string` (Story 4): body hash and the newest trusted comment's ID, `"<hash>:<id>"`.
- `func NudgeFor(is github.WatchIssue, pr github.WatchPR, sess Session, hook []Nudge, mem Memory, cfg Config, now time.Time) (Action, bool)` (Story 4): rule 8 with the repeat guard (spec §4.2, §4.3); an `ActSend` or, at `NudgeLimit`, an `ActEscalate`.

**watch decide** (Story 5)

- `func Decide(s Snapshot, mem Memory, cfg Config, tmpl TitleTemplate) []Action` (Story 5): rules 1 to 9 of spec §4.1.
- `func OwnPR(pr github.WatchPR, issue int, viewer string, pattern *regexp.Regexp) bool` (Story 5): the four terms of spec §2.
- `func OwnSession(s Session, tmpl TitleTemplate) bool` (Story 5)

**watch hook** (Story 6)

- `type HookInput struct { Point string; Workspace string; Repo string; Base string; BaseSHA string; Issue github.WatchIssue; PR *HookPR; Sessions []HookSession; PRs []HookPR; Session *HookSession; HeadSHA string }` (Story 6); JSON as spec §5.2, `PR`, `Session` and `HeadSHA` omitted when unset.
- `type HookPR struct { Number int; Issue int; Branch string; Body string }` (Story 6)
- `type HookSession struct { Title string; Issue int; Branch string; Status string; Vars map[string]string }` (Story 6)
- `type StartAnswer struct { Start bool; Vars map[string]string; Reason string }` (Story 6)
- `type HookError struct { Point string; Err error; Stderr string }` (Story 6), with `Error() string` and `Unwrap() error`.
- `func RunStart(ctx context.Context, cfg HookConfig, dir string, env []string, in HookInput) (StartAnswer, error)` (Story 6)
- `func RunNudges(ctx context.Context, cfg HookConfig, dir string, env []string, in HookInput) ([]Nudge, error)` (Story 6)

**base ref and the watcher checkout** (Story 7)

- `WorktreeSpec.BaseRef string` (Story 7): a commit SHA to cut a new branch from; empty keeps `ResolveBaseCommit`.
- `InstanceOptions.BaseRef string` (Story 7): passed to the worktree spec at first setup; never persisted.
- `NewInstance.BaseRef string` (Story 7): passed to `InstanceOptions.BaseRef` by `createWS`.
- `func FetchBranch(repoPath string, branch string, runner CommandRunner) (string, error)` (Story 7): `git fetch origin <branch>`, returns `origin/<branch>`'s SHA.
- `func ShowFile(repoPath string, rev string, path string, runner CommandRunner) ([]byte, error)` (Story 7): `git show <rev>:<path>`; a missing path is `ErrNoSuchPath`.
- `var ErrNoSuchPath error` (Story 7)
- `func SyncDetachedWorktree(repoPath string, path string, sha string, runner CommandRunner) error` (Story 7): adds the detached worktree when absent, else checks out `sha` detached in it.
- `const watch.CheckoutDir = "watch-checkout"` (Story 7): under the workspace's config dir.

**core watcher** (Story 8)

- `gateWatch gateKind` (Story 8), interval 60 s in `gateIntervals`.
- `type watchState struct` (Story 8) (opaque): per workspace the cached viewer, the memory, the last snapshot, actions and errors, and the actions in flight.
- `func (m *Model) maybeWatch() bool` (Story 8): called from `tickInst` after `maybeGHQuery`.
- `type watchResult struct` (Story 8) (opaque), delivered by `deliverWatch`.

**core watcher actions** (Story 9)

- `OriginWatch Origin = "watch"` (Story 9): a message the watcher sends; it needs a live mod.
- `func (m *Model) dispatchWatchActions(ws *Workspace, st *watchState, actions []watch.Action)` (Story 9)
- `type watchActionResult struct` (Story 9) (opaque), delivered by `deliverWatchAction`.

**Core requests and views** (Story 10)

- `Settings.Watch *bool` (Story 10), JSON `watch,omitempty`, and `func (s Settings) WatchEnabled() bool` (Story 10): nil is off.
- `SetWatch(id WorkspaceID, on bool, req ReqID)` (Story 10): a request on `Core`.
- `ReadWatch(id WorkspaceID, dryRun bool, req ReqID)` (Story 10): a request; its Reply's `Watch`.
- `Reply.Watch WatchView` (Story 10)
- `type WatchView struct { On bool; Err string; Viewer string; Considered int; Unconsidered int; Issues []WatchIssueView; Actions []watch.Action; Escalations []WatchEscalation }` (Story 10); `Actions` are the last tick's, or with `dryRun` the next tick's, none taken.
- `type WatchIssueView struct { Number int; State string; Session string; PR int; Waiting string; Nudges map[string]int; Escalated bool }` (Story 10)
- `type WatchEscalation struct { Issue int; Reason string; At time.Time }` (Story 10)
- `WorkspaceView.Watch WatchSummary` (Story 10)
- `type WatchSummary struct { On bool; Err string; Escalations []WatchEscalation }` (Story 10): at most the 20 newest.

**CLI** (Story 11)

- `var WatchCmd *cobra.Command` (Story 11): `loom watch on|off|status|dry-run [-w workspace] [--json]`.

## Stories

- **Story 1** — The watcher's config and title template.

  The new pure package `watch/` starts with what every other story reads: `loom-watch.json` parsed and validated with every default and bound of spec §3.1 and §7.1, the title template that renders a session's title and parses it back to its issue and variables, the branch pattern derived from it through the real branch sanitizer (exported from `session/git` for this), and prompt rendering with the fixed footer. It also adds `watch/CLAUDE.md` and `watch/README.md`, which the CLAUDE.md lint requires of a new package.

  - **Developer:** intermediate-dev
  - **Files:** `watch/config.go`, `watch/config_test.go`, `watch/title.go`, `watch/title_test.go`, `watch/prompt.go`, `watch/prompt_test.go`, `watch/CLAUDE.md`, `watch/README.md`, `session/git/util.go`, `session/git/util_test.go`, `session/git/worktree.go`, `session/git/worktree_ops.go`
  - **Interfaces:** exposes `ParseConfig`, `Config`, `SessionConfig`, `HookConfig`, `TitleTemplate`, `RenderPrompt`, `PromptFooter`, `git.SanitizeBranchName`; uses none
  - **Verify:** `CGO_ENABLED=0 go test ./watch/ ./session/git/ ./tools/claudemd/`
  - **Review:** deep
  - **Reading:** spec §3, §7.1 (the bounds table); `session/git/util.go:14-45`; `session/git/worktree.go:220-240`; `tools/claudemd/lint.go` (package coverage)

- **Story 2** — The watcher's memory file.

  `{repo}/.loom/watch.json`: load, save atomically, and the record methods the actions call. A missing file is empty memory; an unreadable, undecodable or newer-version file is an error and stays untouched, since acting without the counts would re-nudge and re-escalate. Unknown top-level keys survive a save.

  - **Developer:** junior-dev
  - **Files:** `watch/memory.go`, `watch/memory_test.go`
  - **Interfaces:** exposes `Memory`, `PRMemory`, `NudgeRecord`, `IssueMemory`, `Escalation`, `LoadMemory`, `SaveMemory`, the record methods, `MemoryFile`; uses `config`'s atomic write
  - **Verify:** `CGO_ENABLED=0 go test ./watch/`
  - **Review:** light
  - **Reading:** spec §6; `config/atomic.go`

- **Story 3** — The watcher's `gh` reads and writes.

  `session/github` gains the watcher's own calls, leaving the existing poll's query unchanged: its PRs with the fields ownership and nudges need, the labelled issues with bodies and comments, one issue by number, the viewer's login, a failed job's log tail, and the three writes (close a PR with a comment, comment on an issue, add a label, creating it when missing). Fixtures are captured from real `gh` output.

  - **Developer:** intermediate-dev
  - **Files:** `session/github/watch.go`, `session/github/watch_test.go`, `session/github/testdata/watch_prs.json`, `session/github/testdata/watch_issues.json`, `session/github/testdata/watch_issue_view.json`
  - **Interfaces:** exposes `WatchPR`, `FailedCheck`, `WatchIssue`, `IssueComment`, `WatchPRs`, `WatchIssues`, `ViewWatchIssue`, `Viewer`, `FailedJobLogs`, `ClosePR`, `CommentIssue`, `AddLabel`; uses the package's `gh` helper
  - **Verify:** `CGO_ENABLED=0 go test ./session/github/`
  - **Review:** deep
  - **Reading:** spec §1 (the `gh` additions row), §7.1; `session/github/cli.go`; `session/github/rollup.go`

- **Story 4** — The watcher's types and nudges.

  The plain types `Decide` works on (sessions, nudges, actions, the snapshot) and rule 8: for a Ready session with an open PR, the first nudge that applies (the hook's, then conflict, ci, issue-changed), the repeat guard keyed per kind, the count, and the escalation at the limit.

  - **Developer:** intermediate-dev
  - **Files:** `watch/types.go`, `watch/nudge.go`, `watch/nudge_test.go`
  - **Interfaces:** exposes `Session`, `Nudge`, `ActionKind` and its constants, `Action`, `Snapshot`, `IssueSeen`, `NudgeFor`; uses `Config` (Story 1), `Memory` (Story 2), `WatchPR` and `WatchIssue` (Story 3)
  - **Verify:** `CGO_ENABLED=0 go test ./watch/`
  - **Review:** deep
  - **Reading:** spec §4.2, §4.3, §2

- **Story 5** — The rule table.

  `Decide`: rules 1 to 9 in order for each issue, with PR and session ownership, the cap, one start per tick, the skipped statuses, and the waits with their reasons. Its tests carry one case per rule plus every rule-order case of spec §9, and each of `OwnPR`'s four terms is mutation-checked on its own.

  - **Developer:** intermediate-dev
  - **Files:** `watch/decide.go`, `watch/decide_test.go`, `watch/testdata/sessions.json`
  - **Interfaces:** exposes `Decide`, `OwnPR`, `OwnSession`; uses everything Stories 1 to 4 expose
  - **Verify:** `CGO_ENABLED=0 go test ./watch/`
  - **Review:** deep
  - **Reading:** spec §2, §4.1, §9

- **Story 6** — The hook runner.

  Runs the configured hook for `start` and `nudges`: the command plus the point's name, in a given directory, JSON in and out, under its timeout, with stdout capped and stderr's tail kept, and every answer checked against the bounds of spec §7.1. Any failure is a `HookError` and changes nothing.

  - **Developer:** intermediate-dev
  - **Files:** `watch/hook.go`, `watch/hook_test.go`, `watch/testdata/hook_ok.sh`, `watch/testdata/hook_slow.sh`, `watch/testdata/hook_bad.sh`
  - **Interfaces:** exposes `HookInput`, `HookPR`, `HookSession`, `StartAnswer`, `HookError`, `RunStart`, `RunNudges`; uses `HookConfig` (Story 1), `Nudge` (Story 4)
  - **Verify:** `CGO_ENABLED=0 go test ./watch/`
  - **Review:** deep (builds a child's environment; `os.Environ()`)
  - **Reading:** spec §5, §7.1

- **Story 7** — Cutting a session from `origin/<base>`, and the watcher's checkout.

  A new session's branch can be cut from an explicit commit (`BaseRef`) threaded from `core.NewInstance` through `session.InstanceOptions` to the worktree's first setup, so unpushed local commits never reach a watcher PR; loom still creates the branch, so a kill still deletes it. The same story adds the git helpers the poll job needs: fetch a branch, show a file at a revision, and keep a detached worktree at a commit, outside `worktrees/`.

  - **Developer:** intermediate-dev
  - **Files:** `session/git/worktree.go`, `session/git/worktree_ops.go`, `session/git/watch_checkout.go`, `session/git/watch_checkout_test.go`, `session/git/base_test.go`, `session/instance.go`, `session/instance_test.go`, `core/requests.go`, `core/requests_test.go`
  - **Interfaces:** exposes `WorktreeSpec.BaseRef`, `InstanceOptions.BaseRef`, `NewInstance.BaseRef`, `FetchBranch`, `ShowFile`, `ErrNoSuchPath`, `SyncDetachedWorktree`, `watch.CheckoutDir`; uses none
  - **Verify:** `CGO_ENABLED=0 go test ./session/git/ ./session/ ./core/`
  - **Review:** deep (worktree setup and branch ownership)
  - **Reading:** spec §1 (base override, watcher worktree); `session/git/worktree_ops.go:440-480`; `session/instance.go:508-540`, `session/instance.go:910-940`; `core/requests.go:456-520`; `session/orphan.go:140-160`

- **Story 8** — The watcher's poll on the health tick.

  A new gate dispatches, for every served workspace with the watcher on, a poll job that fetches the base, refreshes the watcher checkout, reads the config and prompts from `origin/<base>`, makes the `gh` reads within the budget, and runs the hook's `nudges`; on delivery `Decide` runs on the loop and the result is kept for status. Any failed read means no decisions for that workspace that tick.

  - **Developer:** intermediate-dev
  - **Files:** `core/gate.go`, `core/gate_test.go`, `core/tick.go`, `core/model.go`, `core/watch.go`, `core/watch_poll.go`, `core/watch_poll_test.go`
  - **Interfaces:** exposes `gateWatch`, `watchState`, `maybeWatch`, `watchResult`; uses Stories 1 to 7
  - **Verify:** `CGO_ENABLED=0 go test ./core/`
  - **Review:** deep
  - **Reading:** spec §1, §7, §7.1; `core/gate.go`; `core/github.go:100-330`; `core/tick.go:84-155`; `core/model.go:480-560`; `docs/claude/adding-a-gated-job.md`

- **Story 9** — Carrying out the watcher's actions.

  Starts (with the hook's `start`, the rendered title and prompt, `BaseRef`, the session config), nudges through the mod with a new origin, kills through `Model.Kill`, closes PRs, and escalates in the order comment, label, alert, record; memory is saved before a nudge is sent; an action already in flight is not dispatched again.

  - **Developer:** intermediate-dev
  - **Files:** `core/watch_actions.go`, `core/watch_actions_test.go`, `core/mod.go`, `core/model.go`
  - **Interfaces:** exposes `OriginWatch`, `dispatchWatchActions`, `watchActionResult`; uses Stories 1 to 8
  - **Verify:** `CGO_ENABLED=0 go test ./core/`
  - **Review:** deep (kills sessions)
  - **Reading:** spec §4.1 (kill authority, in flight), §4.4, §5.2, §7; `core/mod.go:60-160`; `core/requests.go:300-340`; `core/queue_notice.go:40-63`

- **Story 10** — The watcher's requests and views.

  The workspace switch in its local config, `SetWatch` and `ReadWatch` (status, or a dry run that takes no action) on `Core`, `Reply.Watch`, and the summary on `WorkspaceView` from which the PWA pushes; the wire and the protocol reference are regenerated.

  - **Developer:** intermediate-dev
  - **Files:** `config/settings.go`, `config/settings_test.go`, `core/iface.go`, `core/events.go`, `core/workspace_view.go`, `core/watch.go`, `core/watch_requests_test.go`, `core/state_views.go`, `core/state_views_test.go`, `core/rpc/methods_gen.go`, `docs/specs/protocol.md`
  - **Interfaces:** exposes `Settings.Watch`, `WatchEnabled`, `SetWatch`, `ReadWatch`, `Reply.Watch`, `WatchView`, `WatchIssueView`, `WatchEscalation`, `WorkspaceView.Watch`, `WatchSummary`; uses `watchState` (Story 8)
  - **Verify:** `CGO_ENABLED=0 go test ./config/ ./core/ ./core/rpc/`
  - **Review:** deep
  - **Reading:** spec §1 (CLI row); `docs/claude/adding-a-core-method.md`; `core/iface.go`; `core/events.go:225-258`; `core/workspace_view.go`

- **Story 11** — `loom watch`.

  The command group `on`, `off`, `status` and `dry-run`, each through the daemon for one workspace (`-w`, default the current repository's), with `--json` on `status` and `dry-run`.

  - **Developer:** junior-dev
  - **Files:** `internal/cli/watch.go`, `internal/cli/watch_test.go`, `main.go`
  - **Interfaces:** exposes `WatchCmd`; uses `SetWatch`, `ReadWatch`, `WatchView` (Story 10)
  - **Verify:** `CGO_ENABLED=0 go test ./internal/cli/ .`
  - **Review:** light
  - **Reading:** spec §1 (CLI row); `internal/cli/feedback.go:1-120`; `internal/cli/session.go`

- **Story 12** — The escalation push.

  The PWA's push watch sends one push for each escalation it has not seen in a workspace's `Watch` summary, with the issue and the reason.

  - **Developer:** junior-dev
  - **Files:** `internal/web/triggers.go`, `internal/web/triggers_test.go`
  - **Interfaces:** exposes none; uses `WorkspaceView.Watch`, `WatchSummary`, `WatchEscalation` (Story 10)
  - **Verify:** `CGO_ENABLED=0 go test ./internal/web/`
  - **Review:** light
  - **Reading:** spec §4.4; `internal/web/triggers.go`

- **Story 13** — Documentation.

  `docs/specs/watch.md` (the config and the hook contract, by hand), `loom watch` in `USAGE.md`, the memory file, the watcher checkout and the extra `gh` calls in `docs/ARCHITECTURE.md`, and the rules `core/CLAUDE.md` needs for the watcher's kill authority and fail-closed paths.

  - **Developer:** intermediate-dev
  - **Files:** `docs/specs/watch.md`, `USAGE.md`, `docs/ARCHITECTURE.md`, `core/CLAUDE.md`, `watch/README.md`
  - **Interfaces:** exposes none; uses every story's names
  - **Verify:** `CGO_ENABLED=0 go test ./tools/claudemd/`
  - **Review:** light
  - **Reading:** spec §10; CLAUDE.md's conventions for CLAUDE.md files

- **Story 14** — End to end.

  One e2e test on a private tmux server with a stubbed `gh` on `PATH`: a labelled issue starts a watcher session cut from `origin/<base>`, and its PR reported merged kills it.

  - **Developer:** intermediate-dev
  - **Files:** `e2e/watch_test.go`, `e2e/testdata/gh-watch-stub.sh`
  - **Interfaces:** exposes none; uses `loom watch` (Story 11) and the daemon
  - **Verify:** `CGO_ENABLED=0 go test -tags e2e ./e2e/ -run TestWatch`
  - **Review:** deep
  - **Reading:** spec §9 (e2e); `e2e/` (an existing daemon test, for the harness)

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
