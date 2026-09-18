# GitHub issue integration — design

Date: 2026-09-18
Status: approved design, awaiting implementation plan

## Goal

Start sessions from GitHub issues, remember which issue a session is
working on, and show live GitHub state (issue open/closed, PR state,
CI checks) on rail mini-cards and overview cards. PR/CI state applies
to every session whose branch has a PR, linked or not. Every session
also shows how far its branch is ahead of and behind the base branch.

Everything fails closed: without `gh`, without auth, or offline, loom
renders exactly as it does today.

## Non-goals

- Creating PRs or commenting on issues from loom.
- Showing GitHub state anywhere other than rail and overview cards.
- Any GitHub API access other than the `gh` CLI.

## 1. Data layer: `session/github`

A standalone package with no `app` or `ui` imports.

**Availability.** `Available(repoPath) bool` runs `gh auth status` once
per process and caches the result. The existing `checkGHCLI` logic in
`session/git/util.go` moves here; `session/git.PushChanges` calls the
shared function.

**Snapshot query.** `Query(ctx, repoPath) (Snapshot, error)` runs:

```
gh pr list    --json number,headRefName,state,isDraft,reviewDecision,statusCheckRollup,mergedAt --limit 200 --state all
gh issue list --json number,title,state --limit 200
```

and returns

```go
type Snapshot struct {
    PRs       map[string]PR   // keyed by head branch name
    Issues    map[int]Issue   // keyed by issue number
    FetchedAt time.Time
}
type PR struct {
    Number         int
    State          PRState   // Draft | Open | Merged | Closed
    ReviewDecision Review    // None | Approved | ChangesRequested
    Checks         Checks    // None | Pending | Passing | Failing
}
type Issue struct {
    Number int
    Title  string
    Body   string   // empty in list results, filled by View
    URL    string
    Labels []string
    Closed bool
}
```

`statusCheckRollup` folds to `Checks`: any FAILURE/ERROR/TIMED_OUT/
CANCELLED → Failing; else any PENDING/QUEUED/IN_PROGRESS → Pending;
else any SUCCESS → Passing; empty → None. Unknown strings count as
Pending. Linked issue numbers absent from the open list (closed issues)
are backfilled by `gh issue view <n> --json number,title,state,url`,
one call per missing number; callers pass the set of linked numbers.

**Issue expansion.** `View(ctx, repoPath, n) (Issue, error)` fetches
number, title, body, labels, URL, state. `SeedPrompt(Issue) string`
composes:

```
You are working on GitHub issue #123 (<url>).

# <title>

<body verbatim>
```

`SlugTitle(Issue) string` yields `gh-123-<slug>`: title lowercased,
non-alphanumerics collapsed to single dashes, slug capped at 40 runes,
trailing dash trimmed.

The subprocess runner is injected via `cmd.Executor` so tests feed
canned JSON. Any error returns a zero `Snapshot` and the error; callers
drop the cached entry ("stale is worse than none").

## 2. Poller and app wiring

`home` gains:

```go
ghState     map[string]github.Snapshot // keyed by repo path
lastGHQuery time.Time
ghInFlight  bool
```

**Cadence.** `maybeGHQuery` is called from the 3s health tick and
dispatches at most once per `ghInterval = 60 * time.Second`, and never
while `ghInFlight`. One `tea.Cmd` queries every open workspace's repo
path and returns a single `ghReadyMsg{snapshots map[string]Snapshot,
errs map[string]error}`. `handleGHReady` clears `ghInFlight` on every
delivery and replaces `ghState` wholesale, omitting repos whose query
errored. `maybeGHQuery` arms neither field when it dispatches nothing
(no open workspaces, `gh` unavailable).

**Immediate refresh.** `lastGHQuery` is zeroed when a push completes,
when a session is created from the picker or `#123` shorthand, and
when a workspace becomes open. The next health tick then queries.

**Linkage.** `Instance` gains `issue int` with `SetIssue(int)` and
`IssueNumber() int` (0 = none). `InstanceData` gains
`Issue int `json:"issue,omitempty"``. `CurrentSchemaVersion` is bumped,
`storage_migrate.go` gains the case (no-op upgrade, field defaults to
0), and the fixture in `cmd/workspace_migrate_shape_test.go` gains the
field.

**Join.** A pure function on the Update goroutine:

```go
func ghStatusFor(inst *session.Instance, state map[string]github.Snapshot) ui.GitHubState
```

returns

```go
type GitHubState struct {
    Known       bool   // false when the repo has no snapshot
    IssueNumber int
    IssueTitle  string
    IssueClosed bool
    HasPR       bool
    PRNumber    int
    PRState     github.PRState
    Review      github.Review
    Checks      github.Checks
}
```

The PR is looked up by `inst.Branch`, the issue by `inst.IssueNumber()`.
`Known=false` renders nothing; `Known=true, HasPR=false` renders no PR
badge. The two must not be conflated.

**Picker source.** The issue picker lists `ghState[repo].Issues`
(open ones), and triggers an immediate query when no snapshot exists.

## 3. Issue picker and `#123` shorthand

**Key.** `I` in `script/defaults.lua` binds `cs.actions.new_from_issue()`.
In overview it drops to focus first, like `n`. `I` is currently unbound.

**Overlay.** `overlay.IssuePicker` in `ui/overlay/issue_picker.go`,
modeled on the branch picker: filter line, scrolling rows
`#123  title  [labels]`, `j`/`k`/arrows move, `enter` picks, `esc`
cancels. Shows `loading…` while a query is in flight and
`gh unavailable: <reason>` when `Available` is false. New
`stateIssuePicker` and `handleStateIssuePickerKey` in
`app/state_issue_picker.go`, following `state_mergepicker.go`. The
state joins the overlay-active list in `app.go` (the `m.state ==` chain
near line 2144 and the View switch).

**Pick flow.** On enter, a Cmd calls `View` and returns
`issuePickedMsg{issue, err}`. The handler appends a pre-started
instance with `Title = SlugTitle`, `Prompt = SeedPrompt`, calls
`SetIssue`, and enters the launch-options modal via the same
`pendingLaunchOptions` closure `handleStateNewKey` uses. Title and
prompt are not shown for editing; the session can be renamed later
like any other. `View` failure surfaces as a footer error and returns
to the default state with no instance appended.

**Shorthand.** In `handleStatePromptKey`, on submit of an unstarted
instance, if the prompt's first whitespace-delimited token matches
`^#\d+$`, a Cmd calls `View`. On success the token is replaced by
`SeedPrompt` output with the user's remaining text appended after a
blank line, `SetIssue` is called, and the launch-options modal opens.
On failure the footer shows the error and the launch proceeds with the
literal prompt unchanged and no issue linked.

**Persistence.** Both paths call `SetIssue` before `Start`, so the
first save carries the link. Pause/resume/recovery preserve it via
`InstanceData`.

## 4. Rendering

`ui.CardData` gains `GitHub GitHubState`, set by `BuildCardData` from
`ghStatusFor`. Both renderers read only this field.

**Overview card** (`renderOverviewCard`). When `IssueNumber != 0` an
issue line is inserted above the branch line: `#123 <title>`, in Text;
when `IssueClosed`, prefixed `✓ ` and rendered in Dim. On the branch
line, when `HasPR`, a badge is appended to the right column before the
diff stats: `PR#45 draft` / `PR#45 open` / `PR#45 ✓approved` /
`PR#45 ✗changes` / `PR#45 merged`, followed by one checks glyph:
`●` Pending (Dim), `✓` Passing (OK), `✗` Failing (ErrorColor), nothing
for None. When `PRState == Merged` the whole branch line renders in
Dim. Right column truncates first, then the left column against the
remainder, so the card height invariant (`overviewCardHeight`) holds;
the issue line counts toward the fixed height, so unlinked cards gain
one extra tail line to stay the same height.

**Rail mini-card** (`RenderCard`). No new lines. When `Known`, the
status line's right side shows a compact token: `#123` if linked, then
` · PR <glyph>` where glyph is the PR state as one character
(`○` draft, `●` open, `✓` approved, `✗` changes requested, `⇄` merged)
followed by the checks glyph. `IssueClosed` or `Merged` dims the title.

**Colors.** New styles are built inside a `ui.RegisterThemeHook`
callback using existing roles only (Text, Dim, OK, ErrorColor).

**Attention.** GitHub state never sets `NeedsAttention` and never
affects `]`/`[`.

## 5. Parity with the base branch

**Computation.** `GitWorktree.AheadBehind(base string) (ahead, behind
int, err error)` in `session/git` runs
`git rev-list --left-right --count <branch>...<base>` in the worktree.
`base` is the ref `ResolveBaseCommit` already selects for new
worktrees: configured `BaseBranch`, else `origin/HEAD`, `main`,
`master`. It runs on the 3s health tick next to diff stats (one local
subprocess, no network). Workspace terminals, paused and recoverable
instances are skipped. The result lives on `Instance` as transient
fields (`ahead`, `behind`, `hasParity`), never serialized.

**Fetch.** The 60s poller Cmd (section 2) runs
`git fetch origin <base-branch> --quiet` once per open repo before the
`gh` queries, under `gitNetworkTimeout`. Fetch failure is logged at
debug and ignored; parity then reflects the last successful fetch. The
fetch runs even when `gh` is unavailable, so parity works on repos with
no GitHub remote access. When the resolved base is a local-only ref
(no `origin/`), no fetch is issued.

**Rendering.** `CardData` gains `Ahead`, `Behind`, `HasParity`.
Overview cards show `↑N ↓M` on the branch line before the PR badge:
`↑` in OK, `↓` in Dim; `↑0 ↓0` renders `✓ even` in Dim. Rail cards
append `↓M` to the status-line token only when `M > 0`. Neither
surface sets `NeedsAttention`.

**Testing.** `session/git`: mocked runner for count parsing and the
base fallback chain, and no fetch for local-only bases. `app`: a fetch
failure does not drop the repo's `gh` snapshot; the fetch still runs
when `Available=false`. `ui`: width tests extended with the parity
token; card heights unchanged.

## Error handling summary

| Condition | Behaviour |
|---|---|
| `gh` missing or unauthenticated | `Available=false`; no poll, no badges, picker shows reason |
| Query error for one repo | That repo dropped from `ghState`; its cards render `Known=false` |
| `View` fails in picker | Footer error, no instance created |
| `View` fails for `#123` | Footer error, launch with literal prompt, no link |
| Unknown check/PR strings | Folded to Pending / Open, never a crash |
| `git fetch` fails | Debug log; parity keeps last fetched state; `gh` snapshot unaffected |
| `rev-list` fails (base missing) | `HasParity=false`; no token rendered |

## Testing

- `session/github`: canned JSON via mock `cmd.Executor` for rollup
  folding, closed-issue backfill, `Available=false`, `SlugTitle`,
  `SeedPrompt`.
- `app`: throttle pair (mirror `roster_throttle_test.go`), `ghInFlight`
  cleared on error, wholesale replace drops errored repo, `Known=false`
  for missing repo, `#123` expansion success and non-fatal failure,
  picker pick creates instance with issue set and enters launch options.
- `session`: schema migration round-trip; `cmd` mirror fixture.
- `ui`: both renderers at narrow widths, overview card height unchanged
  for linked and unlinked cards, rail card line count unchanged.
- e2e (`loomdev`) with `gh` absent from PATH: screen identical to a
  build without this feature.

## Files touched

- new `session/github/{github.go,query.go,issue.go,*_test.go}`
- `session/git/util.go` (delegate `checkGHCLI`), `session/git/worktree_git.go` (`AheadBehind`, fetch helper)
- `session/instance.go`, `session/storage.go`, `session/storage_migrate.go`
- `cmd/workspace_migrate.go` + shape test fixture
- `app/app.go`, `app/events.go`, new `app/state_issue_picker.go`,
  `app/state_prompt.go`, `app/state_new.go`, `app/intents.go`,
  `app/app_scripts.go`
- `script/defaults.lua`
- new `ui/overlay/issue_picker.go`
- `ui/card.go`, `ui/overview.go`, `ui/theme.go` (hook only)
- `CLAUDE.md` keybinding table and a gotcha entry for the poller and fetch
