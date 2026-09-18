# GitHub Issue Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Start sessions from GitHub issues (picker on `I`, `#123` shorthand in the `N` prompt), remember the link, and show issue / PR / CI / base-branch-parity state on rail and overview cards.

**Architecture:** A new standalone `session/github` package wraps the `gh` CLI (injected executor, fail-closed). The app runs one batched 60s poller per open workspace repo (throttle pair mirroring `maybeRosterQuery`), which also fetches the base branch; results are joined onto each `Instance` as transient fields that `ui.BuildCardData` reads. Ahead/behind rides the 3s metadata tick as one local `git rev-list`. Session creation from an issue reuses the existing pre-started-instance + launch-options path.

**Tech Stack:** Go 1.23, Bubble Tea v2 (`charm.land/bubbletea/v2`), lipgloss v2, gopher-lua, testify, `gh` CLI, git.

**Spec:** `docs/superpowers/specs/2026-09-18-github-issues-design.md`

**Deviation from spec (deliberate):** the spec has `ghStatusFor` run inside `BuildCardData`. `BuildCardData` is called from `ui/list.go` and `ui/overview.go` with only the `*session.Instance`, so the join result is stored on the instance (`Instance.SetGitHubState`) when `ghReadyMsg` arrives, and `BuildCardData` just copies it. Same for ahead/behind.

**Conventions every task follows:**
- Run tests with `CGO_ENABLED=0 go test ./<pkg>/...` (the repo builds CGO-off; see CLAUDE.md).
- Format only your own files: `gofmt -w <files>` — never `gofmt -w .` (rewrites vendored code).
- `go vet ./...` before each commit (local golangci-lint is a different major version; don't use it).
- Commit messages: conventional commits, end with the attribution trailer lines the session provides.
- Never `Send` or mutate model state from inside a `tea.Cmd` body; Cmds return messages, handlers in `Update` mutate.

---

## File map

| File | Responsibility |
|---|---|
| `session/github/types.go` (new) | `PRState`/`Review`/`Checks` enums, `PR`, `Issue`, `Snapshot`, `State`, `StateFor` (pure join) |
| `session/github/rollup.go` (new) | Fold `statusCheckRollup` JSON into `Checks`; parse `state`/`isDraft`/`reviewDecision` |
| `session/github/cli.go` (new) | `CheckCLI`, `Query`, `View` — the only file that runs `gh` |
| `session/github/issue.go` (new) | `SeedPrompt`, `SlugTitle`, `ParseShorthand` |
| `session/git/util.go` | `checkGHCLI` delegates to `github.CheckCLI` |
| `session/git/parity.go` (new) | `AheadBehind`, `FetchRef` |
| `session/instance.go` | `issue` field + accessors; transient `github.State`, ahead/behind; `UpdateParity` |
| `session/storage.go`, `session/storage_migrate.go` | `Issue` in `InstanceData`, schema v6 |
| `cmd/workspace_migrate.go` + shape test | mirror struct + fixture gain `issue` |
| `app/github.go` (new) | `ghReadyMsg`, `ghPollCmd`, `maybeGHQuery`, `handleGHReady`, `applyGitHubState`, `openRepoPaths` |
| `app/app.go` | home fields, tick wiring, message cases, state enum, overlay gating, parity in `gatherMetadataCmd` |
| `app/state_issue_picker.go` (new) | `runNewFromIssue`, `handleStateIssuePickerKey`, `issuePickedMsg` handling, `openLaunchOptionsForNew` |
| `app/state_prompt.go` | `#123` shorthand |
| `app/state_new.go` | use `openLaunchOptionsForNew` |
| `app/intents.go` | push success returns `ghRefreshMsg` |
| `app/app_scripts.go` | `NewFromIssueIntent` dispatch |
| `app/state_default.go` | overview intercept for `I` |
| `script/intent.go`, `script/api_actions.go`, `script/defaults.lua`, `script/loader_defaults_test.go` | `cs.actions.new_from_issue`, `I` binding |
| `keys/keys.go` | `KeyNewFromIssue` help entry |
| `ui/overlay/issuePicker.go` (new) | picker overlay |
| `app/overlay_host.go` | `overlayIssuePicker` kind + accessor |
| `ui/card.go`, `ui/card_github.go` (new), `ui/overview.go` | `CardData.GitHub`, parity fields, badge rendering |
| `CLAUDE.md` | keybinding row + gotcha |

---

### Task 1: `session/github` types and pure join

**Files:**
- Create: `session/github/types.go`
- Test: `session/github/types_test.go`

- [ ] **Step 1: Write the failing test**

```go
package github

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStateFor_UnknownWhenNoSnapshot(t *testing.T) {
	s := StateFor(Snapshot{}, false, "u/b", 12)
	assert.False(t, s.Known)
	assert.False(t, s.HasPR)
	assert.Equal(t, 0, s.IssueNumber, "nothing is reported when the repo was not queried")
}

func TestStateFor_JoinsPRByBranchAndIssueByNumber(t *testing.T) {
	snap := Snapshot{
		PRs:    map[string]PR{"u/b": {Number: 45, State: PROpen, Review: ReviewApproved, Checks: ChecksPassing}},
		Issues: map[int]Issue{12: {Number: 12, Title: "Fix it", Closed: true}},
	}
	s := StateFor(snap, true, "u/b", 12)
	assert.True(t, s.Known)
	assert.True(t, s.HasPR)
	assert.Equal(t, 45, s.PRNumber)
	assert.Equal(t, PROpen, s.PRState)
	assert.Equal(t, ReviewApproved, s.Review)
	assert.Equal(t, ChecksPassing, s.Checks)
	assert.Equal(t, 12, s.IssueNumber)
	assert.Equal(t, "Fix it", s.IssueTitle)
	assert.True(t, s.IssueClosed)
}

func TestStateFor_KnownWithoutPR(t *testing.T) {
	s := StateFor(Snapshot{PRs: map[string]PR{}}, true, "u/other", 0)
	assert.True(t, s.Known)
	assert.False(t, s.HasPR)
	assert.Equal(t, 0, s.IssueNumber)
}

func TestStateFor_LinkedIssueMissingFromSnapshotKeepsNumber(t *testing.T) {
	// The number is the user's link; a snapshot that lacks the issue
	// (backfill failed) must still show "#12" rather than dropping it.
	s := StateFor(Snapshot{}, true, "u/b", 12)
	assert.Equal(t, 12, s.IssueNumber)
	assert.Equal(t, "", s.IssueTitle)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/github/ -run TestStateFor -v`
Expected: FAIL — `undefined: StateFor` (package does not compile).

- [ ] **Step 3: Write the types**

```go
// Package github reads GitHub state through the gh CLI for loom's
// session cards and issue picker. It is standalone: no app, ui, or
// session imports. Every entry point fails closed — when gh is
// missing, unauthenticated, or offline the caller gets an error and
// renders nothing, never a guess.
package github

import "time"

// PRState is the folded pull-request lifecycle state.
type PRState int

const (
	PRNone PRState = iota
	PRDraft
	PROpen
	PRMerged
	PRClosed
)

// Review is the folded review decision on a pull request.
type Review int

const (
	ReviewNone Review = iota
	ReviewApproved
	ReviewChangesRequested
)

// Checks is the folded CI status of a pull request's head commit.
type Checks int

const (
	ChecksNone Checks = iota
	ChecksPending
	ChecksPassing
	ChecksFailing
)

// PR is one pull request keyed by its head branch in Snapshot.PRs.
type PR struct {
	Number int
	State  PRState
	Review Review
	Checks Checks
}

// Issue is one GitHub issue. Body is empty in list results and
// populated by View.
type Issue struct {
	Number int
	Title  string
	Body   string
	URL    string
	Labels []string
	Closed bool
}

// Snapshot is one repo's GitHub state at FetchedAt.
type Snapshot struct {
	PRs       map[string]PR // keyed by head branch name
	Issues    map[int]Issue // keyed by issue number
	FetchedAt time.Time
}

// State is the per-session join of a Snapshot, ready for rendering.
// Known=false means the repo was not (successfully) queried and the UI
// must render nothing; Known=true with HasPR=false means "no PR" —
// the two must never be conflated.
type State struct {
	Known       bool
	IssueNumber int
	IssueTitle  string
	IssueClosed bool
	HasPR       bool
	PRNumber    int
	PRState     PRState
	Review      Review
	Checks      Checks
}

// StateFor joins snap onto one session: its PR by branch, its issue by
// the linked number (0 = none). known reports whether snap is real.
func StateFor(snap Snapshot, known bool, branch string, issue int) State {
	if !known {
		return State{}
	}
	s := State{Known: true, IssueNumber: issue}
	if pr, ok := snap.PRs[branch]; ok {
		s.HasPR = true
		s.PRNumber = pr.Number
		s.PRState = pr.State
		s.Review = pr.Review
		s.Checks = pr.Checks
	}
	if issue != 0 {
		if is, ok := snap.Issues[issue]; ok {
			s.IssueTitle = is.Title
			s.IssueClosed = is.Closed
		}
	}
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/github/ -run TestStateFor -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w session/github/*.go && go vet ./session/github/
git add session/github/
git commit -m "feat(github): add session/github types and pure state join"
```

---

### Task 2: Rollup folding and PR field parsing

**Files:**
- Create: `session/github/rollup.go`
- Test: `session/github/rollup_test.go`

- [ ] **Step 1: Write the failing test**

```go
package github

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rollup(t *testing.T, js string) []rollupItem {
	t.Helper()
	var items []rollupItem
	require.NoError(t, json.Unmarshal([]byte(js), &items))
	return items
}

func TestFoldChecks(t *testing.T) {
	cases := []struct {
		name string
		js   string
		want Checks
	}{
		{"empty", `[]`, ChecksNone},
		{"all success", `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"StatusContext","state":"SUCCESS"}]`, ChecksPassing},
		{"one failing wins", `[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]`, ChecksFailing},
		{"error is failing", `[{"state":"ERROR"}]`, ChecksFailing},
		{"timed out is failing", `[{"status":"COMPLETED","conclusion":"TIMED_OUT"}]`, ChecksFailing},
		{"cancelled is failing", `[{"status":"COMPLETED","conclusion":"CANCELLED"}]`, ChecksFailing},
		{"in progress is pending", `[{"status":"IN_PROGRESS"},{"status":"COMPLETED","conclusion":"SUCCESS"}]`, ChecksPending},
		{"queued is pending", `[{"status":"QUEUED"}]`, ChecksPending},
		{"pending context", `[{"state":"PENDING"}]`, ChecksPending},
		{"skipped and neutral count as passing", `[{"status":"COMPLETED","conclusion":"SKIPPED"},{"status":"COMPLETED","conclusion":"NEUTRAL"}]`, ChecksPassing},
		{"unknown conclusion is pending", `[{"status":"COMPLETED","conclusion":"SOMETHING_NEW"}]`, ChecksPending},
		{"failing beats pending", `[{"status":"IN_PROGRESS"},{"state":"FAILURE"}]`, ChecksFailing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, foldChecks(rollup(t, c.js)))
		})
	}
}

func TestFoldPRState(t *testing.T) {
	assert.Equal(t, PRDraft, foldPRState("OPEN", true))
	assert.Equal(t, PROpen, foldPRState("OPEN", false))
	assert.Equal(t, PRMerged, foldPRState("MERGED", false))
	assert.Equal(t, PRClosed, foldPRState("CLOSED", false))
	assert.Equal(t, PROpen, foldPRState("WEIRD", false), "unknown strings fold to Open, never crash")
}

func TestFoldReview(t *testing.T) {
	assert.Equal(t, ReviewApproved, foldReview("APPROVED"))
	assert.Equal(t, ReviewChangesRequested, foldReview("CHANGES_REQUESTED"))
	assert.Equal(t, ReviewNone, foldReview("REVIEW_REQUIRED"))
	assert.Equal(t, ReviewNone, foldReview(""))
}

func TestPRPriority_OpenBeatsMergedBeatsClosed(t *testing.T) {
	assert.True(t, prOutranks(PR{State: PROpen}, PR{State: PRMerged}))
	assert.True(t, prOutranks(PR{State: PRDraft}, PR{State: PRMerged}))
	assert.True(t, prOutranks(PR{State: PRMerged}, PR{State: PRClosed}))
	assert.False(t, prOutranks(PR{State: PRClosed}, PR{State: PROpen}))
	assert.True(t, prOutranks(PR{Number: 9, State: PROpen}, PR{Number: 3, State: PROpen}), "same state: newer number wins")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/github/ -run 'TestFold|TestPRPriority' -v`
Expected: FAIL — `undefined: rollupItem`, `foldChecks`, etc.

- [ ] **Step 3: Write the folding code**

```go
package github

import "strings"

// rollupItem is one element of gh's statusCheckRollup. GitHub mixes two
// shapes in the array: CheckRun (status + conclusion) and StatusContext
// (state). Both are decoded loosely so a new field never breaks us.
type rollupItem struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// foldChecks reduces a rollup to one Checks value. Priority: any
// failure → Failing; else any pending → Pending; else any item →
// Passing; empty → None. Unknown strings count as Pending so a state
// GitHub adds later degrades to "still waiting" rather than "green".
func foldChecks(items []rollupItem) Checks {
	if len(items) == 0 {
		return ChecksNone
	}
	failing, pending := false, false
	for _, it := range items {
		switch classifyRollup(it) {
		case ChecksFailing:
			failing = true
		case ChecksPending:
			pending = true
		}
	}
	switch {
	case failing:
		return ChecksFailing
	case pending:
		return ChecksPending
	default:
		return ChecksPassing
	}
}

func classifyRollup(it rollupItem) Checks {
	// CheckRun shape: status tells whether it finished.
	if it.Status != "" && !strings.EqualFold(it.Status, "COMPLETED") {
		return ChecksPending
	}
	verdict := it.Conclusion
	if verdict == "" {
		verdict = it.State
	}
	switch strings.ToUpper(verdict) {
	case "SUCCESS", "NEUTRAL", "SKIPPED", "EXPECTED":
		return ChecksPassing
	case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return ChecksFailing
	case "PENDING", "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED":
		return ChecksPending
	default:
		return ChecksPending
	}
}

// foldPRState maps gh's state string plus isDraft to PRState.
func foldPRState(state string, draft bool) PRState {
	switch strings.ToUpper(state) {
	case "MERGED":
		return PRMerged
	case "CLOSED":
		return PRClosed
	default:
		if draft {
			return PRDraft
		}
		return PROpen
	}
}

// foldReview maps gh's reviewDecision to Review. REVIEW_REQUIRED and ""
// both mean "no decision yet".
func foldReview(decision string) Review {
	switch strings.ToUpper(decision) {
	case "APPROVED":
		return ReviewApproved
	case "CHANGES_REQUESTED":
		return ReviewChangesRequested
	default:
		return ReviewNone
	}
}

// prRank orders states for the "one PR per branch" reduction: a live
// PR is what the user cares about, then a merged one, then closed.
func prRank(s PRState) int {
	switch s {
	case PROpen, PRDraft:
		return 3
	case PRMerged:
		return 2
	case PRClosed:
		return 1
	default:
		return 0
	}
}

// prOutranks reports whether a should replace b as the branch's PR.
func prOutranks(a, b PR) bool {
	if ra, rb := prRank(a.State), prRank(b.State); ra != rb {
		return ra > rb
	}
	return a.Number > b.Number
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/github/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w session/github/*.go && go vet ./session/github/
git add session/github/
git commit -m "feat(github): fold check rollups, PR state and review decisions"
```

---

### Task 3: `gh` CLI wrapper: `CheckCLI`, `Query`, `View`

**Files:**
- Create: `session/github/cli.go`
- Test: `session/github/cli_test.go`

- [ ] **Step 1: Write the failing test**

```go
package github

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedExec answers each gh invocation by matching a substring of
// the joined argv. Unmatched commands fail, which keeps a test honest
// about exactly which commands it expects.
type scriptedExec struct {
	answers map[string]string // argv substring -> stdout
	errs    map[string]error
	calls   []string
}

func (s *scriptedExec) find(c *exec.Cmd) (string, error, bool) {
	argv := strings.Join(c.Args, " ")
	s.calls = append(s.calls, argv)
	for k, v := range s.answers {
		if strings.Contains(argv, k) {
			return v, s.errs[k], true
		}
	}
	for k, e := range s.errs {
		if strings.Contains(argv, k) {
			return "", e, true
		}
	}
	return "", errors.New("unexpected command: " + argv), false
}
func (s *scriptedExec) Run(c *exec.Cmd) error { _, err, _ := s.find(c); return err }
func (s *scriptedExec) Output(c *exec.Cmd) ([]byte, error) {
	out, err, _ := s.find(c)
	return []byte(out), err
}
func (s *scriptedExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return s.Output(c) }

const prListJSON = `[
 {"number":45,"headRefName":"u/b","state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"}]},
 {"number":40,"headRefName":"u/b","state":"CLOSED","isDraft":false,"reviewDecision":"","statusCheckRollup":[]},
 {"number":46,"headRefName":"u/c","state":"OPEN","isDraft":true,"reviewDecision":"","statusCheckRollup":[{"status":"IN_PROGRESS"}]}
]`

const issueListJSON = `[
 {"number":12,"title":"Fix it","state":"OPEN","url":"https://x/12","labels":[{"name":"bug"}]},
 {"number":13,"title":"Other","state":"OPEN","url":"https://x/13","labels":[]}
]`

func TestQuery_ParsesPRsAndIssues(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{
		"pr list":    prListJSON,
		"issue list": issueListJSON,
	}}
	snap, err := Query(context.Background(), "/repo", nil, ex)
	require.NoError(t, err)

	pr := snap.PRs["u/b"]
	assert.Equal(t, 45, pr.Number, "the open PR outranks the closed one on the same branch")
	assert.Equal(t, PROpen, pr.State)
	assert.Equal(t, ReviewApproved, pr.Review)
	assert.Equal(t, ChecksPassing, pr.Checks)
	assert.Equal(t, PRDraft, snap.PRs["u/c"].State)
	assert.Equal(t, ChecksPending, snap.PRs["u/c"].Checks)

	assert.Equal(t, "Fix it", snap.Issues[12].Title)
	assert.Equal(t, []string{"bug"}, snap.Issues[12].Labels)
	assert.False(t, snap.Issues[12].Closed)
	assert.False(t, snap.FetchedAt.IsZero())
	for _, c := range ex.calls {
		assert.Contains(t, c, "-R", "")
	}
}

func TestQuery_RunsInRepoDir(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{"pr list": `[]`, "issue list": `[]`}}
	_, err := Query(context.Background(), "/repo", nil, ex)
	require.NoError(t, err)
	// Every gh call is executed with Dir set (checked through argv
	// having no -R flag: gh infers the repo from cwd).
	for _, c := range ex.calls {
		assert.NotContains(t, c, " -R ")
	}
}

func TestQuery_BackfillsClosedLinkedIssues(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{
		"pr list":       `[]`,
		"issue list":    issueListJSON,
		"issue view 99": `{"number":99,"title":"Done","state":"CLOSED","url":"https://x/99","labels":[]}`,
	}}
	snap, err := Query(context.Background(), "/repo", []int{12, 99}, ex)
	require.NoError(t, err)
	assert.True(t, snap.Issues[99].Closed)
	assert.Equal(t, "Done", snap.Issues[99].Title)
	// #12 was in the open list, so no view call for it.
	for _, c := range ex.calls {
		assert.NotContains(t, c, "issue view 12")
	}
}

func TestQuery_BackfillFailureIsNotFatal(t *testing.T) {
	ex := &scriptedExec{
		answers: map[string]string{"pr list": `[]`, "issue list": `[]`},
		errs:    map[string]error{"issue view 7": errors.New("not found")},
	}
	snap, err := Query(context.Background(), "/repo", []int{7}, ex)
	require.NoError(t, err)
	_, ok := snap.Issues[7]
	assert.False(t, ok)
}

func TestQuery_PRListErrorIsFatal(t *testing.T) {
	ex := &scriptedExec{
		answers: map[string]string{"issue list": `[]`},
		errs:    map[string]error{"pr list": errors.New("boom")},
	}
	_, err := Query(context.Background(), "/repo", nil, ex)
	assert.Error(t, err)
}

func TestQuery_BadJSONIsFatal(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{"pr list": `nope`, "issue list": `[]`}}
	_, err := Query(context.Background(), "/repo", nil, ex)
	assert.Error(t, err)
}

func TestView_ParsesBody(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{
		"issue view 12": `{"number":12,"title":"Fix it","body":"Steps:\n1. x","state":"OPEN","url":"https://x/12","labels":[{"name":"bug"},{"name":"p1"}]}`,
	}}
	is, err := View(context.Background(), "/repo", 12, ex)
	require.NoError(t, err)
	assert.Equal(t, "Steps:\n1. x", is.Body)
	assert.Equal(t, []string{"bug", "p1"}, is.Labels)
	assert.Equal(t, "https://x/12", is.URL)
}

func TestCheckCLI_AuthFailure(t *testing.T) {
	ex := &scriptedExec{errs: map[string]error{"auth status": errors.New("not logged in")}}
	err := CheckCLI(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gh auth login")
}

func TestCheckCLI_OK(t *testing.T) {
	ex := &scriptedExec{answers: map[string]string{"auth status": ""}}
	assert.NoError(t, CheckCLI(ex))
}
```

Note: `TestCheckCLI_OK` only passes on a machine with `gh` on PATH (LookPath runs first). Guard it:

```go
func TestCheckCLI_OK(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not installed")
	}
	ex := &scriptedExec{answers: map[string]string{"auth status": ""}}
	assert.NoError(t, CheckCLI(ex))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/github/ -run 'TestQuery|TestView|TestCheckCLI' -v`
Expected: FAIL — `undefined: Query`, `View`, `CheckCLI`.

- [ ] **Step 3: Write the CLI wrapper**

```go
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
)

// ghTimeout bounds every gh subprocess. gh talks to the network, so
// this is a network budget, not a tick budget.
const ghTimeout = 20 * time.Second

// listLimit caps pr/issue list sizes. Large enough for any repo loom
// is plausibly pointed at; small enough that the JSON stays cheap.
const listLimit = "200"

func runner(r internalexec.Executor) internalexec.Executor {
	if r == nil {
		return internalexec.Default{}
	}
	return r
}

// CheckCLI reports whether gh is installed and authenticated. The
// messages are user-facing (they surface in the picker and the push
// error path).
func CheckCLI(r internalexec.Executor) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GitHub CLI (gh) is not installed. Please install it first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	if err := runner(r).Run(exec.CommandContext(ctx, "gh", "auth", "status")); err != nil {
		return fmt.Errorf("GitHub CLI is not configured. Please run 'gh auth login' first")
	}
	return nil
}

func gh(ctx context.Context, r internalexec.Executor, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "gh", args...)
	c.Dir = dir
	out, err := runner(r).Output(c)
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w", args[0]+" "+args[1], err)
	}
	return out, nil
}

type rawPR struct {
	Number         int          `json:"number"`
	HeadRefName    string       `json:"headRefName"`
	State          string       `json:"state"`
	IsDraft        bool         `json:"isDraft"`
	ReviewDecision string       `json:"reviewDecision"`
	Rollup         []rollupItem `json:"statusCheckRollup"`
}

type rawIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	URL    string `json:"url"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (ri rawIssue) issue() Issue {
	is := Issue{Number: ri.Number, Title: ri.Title, Body: ri.Body, URL: ri.URL, Closed: ri.State == "CLOSED"}
	for _, l := range ri.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	return is
}

// Query fetches repoDir's PRs (all states, reduced to one per head
// branch) and open issues. linked lists issue numbers loom sessions
// reference; any of them absent from the open list (i.e. closed) is
// backfilled with one `gh issue view` each — a backfill failure only
// omits that issue. A pr/issue list failure is fatal: the caller must
// drop its cached snapshot rather than keep a stale one.
func Query(ctx context.Context, repoDir string, linked []int, r internalexec.Executor) (Snapshot, error) {
	out, err := gh(ctx, r, repoDir, "pr", "list", "--state", "all", "--limit", listLimit,
		"--json", "number,headRefName,state,isDraft,reviewDecision,statusCheckRollup")
	if err != nil {
		return Snapshot{}, err
	}
	var prs []rawPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return Snapshot{}, fmt.Errorf("parsing gh pr list: %w", err)
	}

	out, err = gh(ctx, r, repoDir, "issue", "list", "--state", "open", "--limit", listLimit,
		"--json", "number,title,state,url,labels")
	if err != nil {
		return Snapshot{}, err
	}
	var issues []rawIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return Snapshot{}, fmt.Errorf("parsing gh issue list: %w", err)
	}

	snap := Snapshot{PRs: map[string]PR{}, Issues: map[int]Issue{}, FetchedAt: time.Now()}
	for _, p := range prs {
		pr := PR{Number: p.Number, State: foldPRState(p.State, p.IsDraft), Review: foldReview(p.ReviewDecision), Checks: foldChecks(p.Rollup)}
		if cur, ok := snap.PRs[p.HeadRefName]; !ok || prOutranks(pr, cur) {
			snap.PRs[p.HeadRefName] = pr
		}
	}
	for _, i := range issues {
		snap.Issues[i.Number] = i.issue()
	}
	for _, n := range linked {
		if n == 0 {
			continue
		}
		if _, ok := snap.Issues[n]; ok {
			continue
		}
		is, err := View(ctx, repoDir, n, r)
		if err != nil {
			log.For("github").Debug("issue.backfill_failed", "issue", n, "err", err.Error())
			continue
		}
		snap.Issues[n] = is
	}
	return snap, nil
}

// View fetches one issue with its body, for the picker and the #123
// prompt shorthand.
func View(ctx context.Context, repoDir string, number int, r internalexec.Executor) (Issue, error) {
	out, err := gh(ctx, r, repoDir, "issue", "view", strconv.Itoa(number),
		"--json", "number,title,body,state,url,labels")
	if err != nil {
		return Issue{}, err
	}
	var ri rawIssue
	if err := json.Unmarshal(out, &ri); err != nil {
		return Issue{}, fmt.Errorf("parsing gh issue view: %w", err)
	}
	return ri.issue(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/github/ -v`
Expected: PASS (`TestCheckCLI_OK` may SKIP).

- [ ] **Step 5: Commit**

```bash
gofmt -w session/github/*.go && go vet ./session/github/
git add session/github/
git commit -m "feat(github): query PRs and issues through the gh CLI"
```

---

### Task 4: Issue expansion helpers (`SeedPrompt`, `SlugTitle`, `ParseShorthand`)

**Files:**
- Create: `session/github/issue.go`
- Test: `session/github/issue_test.go`

- [ ] **Step 1: Write the failing test**

```go
package github

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlugTitle(t *testing.T) {
	assert.Equal(t, "gh-12-fix-the-flaky-reconcile-test", SlugTitle(Issue{Number: 12, Title: "Fix the flaky reconcile test!"}))
	assert.Equal(t, "gh-7-a-b", SlugTitle(Issue{Number: 7, Title: "  A -- b  "}))
	assert.Equal(t, "gh-3", SlugTitle(Issue{Number: 3, Title: "###"}), "no usable words: number only")
	long := SlugTitle(Issue{Number: 1, Title: strings.Repeat("word ", 30)})
	assert.LessOrEqual(t, len(long), len("gh-1-")+slugMax)
	assert.False(t, strings.HasSuffix(long, "-"))
}

func TestSeedPrompt(t *testing.T) {
	got := SeedPrompt(Issue{Number: 12, Title: "Fix it", Body: "Steps:\n1. x", URL: "https://x/12"})
	assert.Equal(t, "You are working on GitHub issue #12 (https://x/12).\n\n# Fix it\n\nSteps:\n1. x\n", got)
}

func TestSeedPrompt_EmptyBody(t *testing.T) {
	got := SeedPrompt(Issue{Number: 12, Title: "Fix it", URL: "https://x/12"})
	assert.Equal(t, "You are working on GitHub issue #12 (https://x/12).\n\n# Fix it\n", got)
}

func TestParseShorthand(t *testing.T) {
	n, rest, ok := ParseShorthand("#123")
	assert.True(t, ok)
	assert.Equal(t, 123, n)
	assert.Equal(t, "", rest)

	n, rest, ok = ParseShorthand("  #45 and also refactor the tests")
	assert.True(t, ok)
	assert.Equal(t, 45, n)
	assert.Equal(t, "and also refactor the tests", rest)

	_, _, ok = ParseShorthand("fix #45")
	assert.False(t, ok, "only a leading token counts")
	_, _, ok = ParseShorthand("#abc")
	assert.False(t, ok)
	_, _, ok = ParseShorthand("#0")
	assert.False(t, ok)
	_, _, ok = ParseShorthand("")
	assert.False(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/github/ -run 'TestSlug|TestSeed|TestParseShorthand' -v`
Expected: FAIL — undefined functions.

- [ ] **Step 3: Write the helpers**

```go
package github

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// slugMax caps the slug portion of SlugTitle (after "gh-<n>-"), in
// bytes; slugs are ASCII so bytes equal runes.
const slugMax = 40

// SlugTitle yields the session title for an issue: "gh-<n>-<slug>",
// where slug is the lowercased title with every non-alphanumeric run
// collapsed to one dash, capped at slugMax and trimmed of a trailing
// dash. A title with no usable characters yields "gh-<n>".
func SlugTitle(is Issue) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(is.Title) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := b.String()
	if len(slug) > slugMax {
		slug = slug[:slugMax]
	}
	slug = strings.TrimRight(slug, "-")
	if slug == "" {
		return fmt.Sprintf("gh-%d", is.Number)
	}
	return fmt.Sprintf("gh-%d-%s", is.Number, slug)
}

// SeedPrompt composes the agent's initial prompt from an issue: a
// preamble naming the issue and URL, the title as a heading, then the
// body verbatim.
func SeedPrompt(is Issue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are working on GitHub issue #%d (%s).\n\n# %s\n", is.Number, is.URL, is.Title)
	if body := strings.TrimSpace(is.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	return b.String()
}

// ParseShorthand recognizes a prompt whose first token is "#<n>" and
// returns the number and the remaining text (trimmed). ok is false for
// anything else, including "#0".
func ParseShorthand(prompt string) (n int, rest string, ok bool) {
	fields := strings.Fields(prompt)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "#") {
		return 0, "", false
	}
	n, err := strconv.Atoi(fields[0][1:])
	if err != nil || n <= 0 {
		return 0, "", false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(prompt), fields[0]))
	return n, rest, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/github/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w session/github/*.go && go vet ./session/github/
git add session/github/
git commit -m "feat(github): issue slug, prompt seeding and #n shorthand parsing"
```

---

### Task 5: `session/git` delegates `checkGHCLI` and gains `AheadBehind` + `FetchRef`

**Files:**
- Modify: `session/git/util.go:44-60`
- Create: `session/git/parity.go`
- Test: `session/git/parity_test.go`

- [ ] **Step 1: Write the failing test**

```go
package git

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAheadBehind_CountsBothSides(t *testing.T) {
	repo := newRepo(t, "main")
	runGit(t, repo, "checkout", "-b", "feature")
	commitFile(t, repo, "a.txt", "a")
	commitFile(t, repo, "b.txt", "b")
	runGit(t, repo, "checkout", "main")
	commitFile(t, repo, "m.txt", "m")

	ahead, behind, err := AheadBehind(repo, "feature", "main", nil)
	require.NoError(t, err)
	assert.Equal(t, 2, ahead)
	assert.Equal(t, 1, behind)
}

func TestAheadBehind_Even(t *testing.T) {
	repo := newRepo(t, "main")
	runGit(t, repo, "checkout", "-b", "feature")
	ahead, behind, err := AheadBehind(repo, "feature", "main", nil)
	require.NoError(t, err)
	assert.Equal(t, 0, ahead)
	assert.Equal(t, 0, behind)
}

func TestAheadBehind_MissingBaseErrors(t *testing.T) {
	repo := newRepo(t, "main")
	_, _, err := AheadBehind(repo, "main", "nope", nil)
	assert.Error(t, err)
}

func TestFetchRef_UpdatesRemoteTracking(t *testing.T) {
	clone := newClone(t, "main")
	// Advance origin out of band.
	origin := filepath.Join(filepath.Dir(clone), "origin.git")
	src := filepath.Join(filepath.Dir(clone), "src")
	commitFile(t, src, "new.txt", "n")
	runGit(t, src, "push", origin, "main")

	require.NoError(t, FetchRef(clone, "origin/main", nil))
	out, err := exec.Command("git", "-C", clone, "rev-list", "--count", "main..origin/main").Output()
	require.NoError(t, err)
	assert.Equal(t, "1\n", string(out))
}

func TestFetchRef_LocalRefIsNoop(t *testing.T) {
	repo := newRepo(t, "main")
	assert.NoError(t, FetchRef(repo, "main", nil), "no remote prefix: nothing to fetch, no error")
}
```

Check `newClone` returns the clone path and where `src`/`origin.git` sit (`session/git/base_test.go:44-60`); adjust the two `filepath.Join` lines if the helper lays them out differently.

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/git/ -run 'TestAheadBehind|TestFetchRef' -v`
Expected: FAIL — `undefined: AheadBehind`, `FetchRef`.

- [ ] **Step 3: Write parity.go**

```go
package git

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// AheadBehind returns how many commits branch has that base lacks
// (ahead) and vice versa (behind), reading local refs only — one
// subprocess, no network, so it is safe on the metadata tick.
func AheadBehind(repoPath, branch, base string, runner CommandRunner) (ahead, behind int, err error) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-list", "--left-right", "--count", branch+"..."+base)
	out, err := r.Output(c)
	if err != nil {
		return 0, 0, fmt.Errorf("rev-list %s...%s: %w", branch, base, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("rev-list %s...%s: unexpected output %q", branch, base, strings.TrimSpace(string(out)))
	}
	if ahead, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, fmt.Errorf("rev-list ahead count: %w", err)
	}
	if behind, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("rev-list behind count: %w", err)
	}
	return ahead, behind, nil
}

// FetchRef updates the remote-tracking ref named like "origin/main".
// A ref with no "<remote>/" prefix is local and returns nil without
// running anything. Network-bound: gitNetworkTimeout applies.
func FetchRef(repoPath, ref string, runner CommandRunner) error {
	remote, branch, ok := strings.Cut(ref, "/")
	if !ok || remote == "" || branch == "" {
		return nil
	}
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--quiet", remote, branch)
	if out, err := r.CombinedOutput(c); err != nil {
		return fmt.Errorf("fetch %s %s: %s (%w)", remote, branch, strings.TrimSpace(string(out)), err)
	}
	return nil
}
```

Check that `defaultRunner` exists in `session/git` (it is used by `FetchBranches`). If its name differs, use that name.

- [ ] **Step 4: Replace `checkGHCLI` body in `session/git/util.go`**

```go
// checkGHCLI checks if GitHub CLI is installed and configured.
func (g *GitWorktree) checkGHCLI() error {
	return github.CheckCLI(g.runner)
}
```

Add the import `"github.com/aidan-bailey/loom/session/github"` and drop now-unused imports (`context`/`exec` may still be used elsewhere in the file; let the compiler tell you).

- [ ] **Step 5: Run tests**

Run: `CGO_ENABLED=0 go test ./session/git/ ./session/github/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w session/git/parity.go session/git/parity_test.go session/git/util.go && go vet ./session/git/
git add session/git/
git commit -m "feat(git): ahead/behind counting, targeted ref fetch, shared gh check"
```

---

### Task 6: `Instance` link field, schema v6, transient GitHub/parity state

**Files:**
- Modify: `session/storage.go:25,32-50`
- Modify: `session/storage_migrate.go` (switch)
- Modify: `session/instance.go` (struct, `Snapshot`, `FromInstanceData`, accessors)
- Modify: `cmd/workspace_migrate.go:28-44`, `cmd/workspace_migrate_shape_test.go:20-48`
- Test: `session/storage_migrate_test.go`, `session/instance_github_test.go` (new)

- [ ] **Step 1: Write the failing tests**

Append to `session/storage_migrate_test.go`:

```go
// v5 records lack the issue field; it must default to 0 and the
// record stamp to CurrentSchemaVersion.
func TestMigrate_V5UpgradesAddsIssue(t *testing.T) {
	raw := []byte(`{"schema_version":5,"title":"t","path":"/p","branch":"b","status":0,"height":1,"width":1,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","program":"claude","worktree":{},"diff_stats":{},"is_workspace_terminal":false}`)
	data, err := Migrate(raw)
	require.NoError(t, err)
	assert.Equal(t, CurrentSchemaVersion, data.SchemaVersion)
	assert.Equal(t, 0, data.Issue)
}

func TestMigrate_V6RoundTripsIssue(t *testing.T) {
	raw := []byte(`{"schema_version":6,"title":"t","issue":42,"worktree":{},"diff_stats":{}}`)
	data, err := Migrate(raw)
	require.NoError(t, err)
	assert.Equal(t, 42, data.Issue)
}
```

Create `session/instance_github_test.go`:

```go
package session

import (
	"testing"

	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstance_IssueRoundTripsThroughInstanceData(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "t", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetIssue(42)
	assert.Equal(t, 42, inst.IssueNumber())

	data := inst.ToInstanceData()
	assert.Equal(t, 42, data.Issue)

	back, err := FromInstanceData(data, "")
	require.NoError(t, err)
	assert.Equal(t, 42, back.IssueNumber())
}

func TestInstance_GitHubStateIsTransient(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "t", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	assert.False(t, inst.GitHubState().Known)
	inst.SetGitHubState(github.State{Known: true, HasPR: true, PRNumber: 5})
	assert.Equal(t, 5, inst.GitHubState().PRNumber)
	// Not serialized: a fresh decode has no state.
	back, err := FromInstanceData(inst.ToInstanceData(), "")
	require.NoError(t, err)
	assert.False(t, back.GitHubState().Known)
}

func TestInstance_ParityAccessors(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "t", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	_, _, ok := inst.Parity()
	assert.False(t, ok)
	inst.setParity(3, 1, true)
	a, b, ok := inst.Parity()
	assert.True(t, ok)
	assert.Equal(t, 3, a)
	assert.Equal(t, 1, b)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./session/ -run 'TestMigrate_V5|TestMigrate_V6|TestInstance_Issue|TestInstance_GitHub|TestInstance_Parity' -v`
Expected: FAIL — `data.Issue undefined`, `SetIssue undefined`, etc.

- [ ] **Step 3: Storage changes**

In `session/storage.go`:

```go
const CurrentSchemaVersion = 6
```

and in `InstanceData`, after `IsWorkspaceTerminal`:

```go
	// Issue is the GitHub issue number this session was started from
	// (0 = not linked). Set once at creation by the issue picker or the
	// #n prompt shorthand; read by the GitHub poller join.
	Issue int `json:"issue,omitempty"`
```

In `session/storage_migrate.go`, add before `default:`:

```go
		case 5:
			// v5 → v6: Issue added. Zero value (0 = unlinked) is the
			// correct default for pre-existing records — version stamp only.
			data.SchemaVersion = 6
```

Update the doc comment above `Migrate` to mention `v5→v6 adds Issue`.

- [ ] **Step 4: Instance changes**

In `session/instance.go`, add to the `Instance` struct next to `waitReason` (the transient block):

```go
	// issue is the linked GitHub issue number (0 = none). Persisted as
	// InstanceData.Issue.
	issue int
	// githubState is the poller's join for this session (see
	// app/github.go). Transient: never serialized; Known=false until the
	// first successful poll after startup.
	githubState github.State
	// ahead/behind count commits relative to the base branch
	// (git.AheadBehind); hasParity is false until the first successful
	// count and after any failure. Transient.
	ahead, behind int
	hasParity     bool
```

Add the import `"github.com/aidan-bailey/loom/session/github"`.

In `Snapshot()` add `Issue: i.issue,` to the `InstanceData` literal. In `FromInstanceData` add `issue: data.Issue,` to the `&Instance{...}` literal.

Add accessors next to `WaitReason`/`SetWaitReason`:

```go
// IssueNumber returns the linked GitHub issue, or 0.
func (i *Instance) IssueNumber() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.issue
}

// SetIssue links this session to a GitHub issue (0 unlinks).
func (i *Instance) SetIssue(n int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.issue = n
}

// GitHubState returns the last joined GitHub state for this session.
func (i *Instance) GitHubState() github.State {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.githubState
}

// SetGitHubState records the poller's join result.
func (i *Instance) SetGitHubState(s github.State) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.githubState = s
}

// Parity returns commits ahead/behind the base branch; ok is false
// when no count has succeeded yet.
func (i *Instance) Parity() (ahead, behind int, ok bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.ahead, i.behind, i.hasParity
}

func (i *Instance) setParity(ahead, behind int, ok bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.ahead, i.behind, i.hasParity = ahead, behind, ok
}

// UpdateParity recounts ahead/behind against base (a ref name such as
// "origin/main"). Skipped — leaving parity unknown — for workspace
// terminals, unstarted, paused or recoverable instances, and when base
// is empty. Runs one local git subprocess; call it from the metadata
// fan-out, never from Update.
func (i *Instance) UpdateParity(base string) {
	if base == "" || i.IsWorkspaceTerminal || !i.isStarted() {
		i.setParity(0, 0, false)
		return
	}
	if s := i.GetStatus(); s == Paused || s == Recoverable {
		i.setParity(0, 0, false)
		return
	}
	gw := i.getGitWorktree()
	if gw == nil {
		i.setParity(0, 0, false)
		return
	}
	ahead, behind, err := git.AheadBehind(gw.GetRepoPath(), gw.GetBranchName(), base, nil)
	if err != nil {
		i.logger.Debug("parity.failed", "base", base, "err", err.Error())
		i.setParity(0, 0, false)
		return
	}
	i.setParity(ahead, behind, true)
}
```

Check `getGitWorktree` exists as an unexported locked getter (`FromInstanceData` uses `setGitWorktree`; `worktreeBackend.RepoName` uses `getGitWorktree`). If the logger field is named differently than `logger`, use that name.

- [ ] **Step 5: CLI mirror struct and fixture**

In `cmd/workspace_migrate.go`, add to `migrationInstance` after `IsWorkspaceTerminal`:

```go
	Issue               int                   `json:"issue,omitempty"`
```

In `cmd/workspace_migrate_shape_test.go` fixture, add after `"is_workspace_terminal": false`:

```json
		"is_workspace_terminal": false,
		"issue": 42
```

- [ ] **Step 6: Run tests**

Run: `CGO_ENABLED=0 go test ./session/ ./cmd/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w session/instance.go session/storage.go session/storage_migrate.go session/instance_github_test.go session/storage_migrate_test.go cmd/workspace_migrate.go cmd/workspace_migrate_shape_test.go && go vet ./session/ ./cmd/
git add session/ cmd/
git commit -m "feat(session): link instances to GitHub issues (schema v6) and hold transient GitHub/parity state"
```

---

### Task 7: App poller — `app/github.go`, home fields, tick wiring

**Files:**
- Create: `app/github.go`
- Modify: `app/app.go` (home struct near line 402; health tick near line 1372; message switch near line 1194; `activateWorkspace`)
- Test: `app/github_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package app

import (
	"errors"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGHQueryDispatchesOnFirstCall(t *testing.T) {
	m := homeWithAppState(t)
	require.NotNil(t, m.maybeGHQuery())
	assert.True(t, m.ghInFlight)
}

func TestGHQueryThrottledWithinInterval(t *testing.T) {
	m := homeWithAppState(t)
	require.NotNil(t, m.maybeGHQuery())
	m.ghInFlight = false
	assert.Nil(t, m.maybeGHQuery())
}

func TestGHQueryResumesAfterInterval(t *testing.T) {
	m := homeWithAppState(t)
	require.NotNil(t, m.maybeGHQuery())
	m.ghInFlight = false
	m.lastGHQuery = time.Now().Add(-ghInterval - time.Second)
	assert.NotNil(t, m.maybeGHQuery())
}

func TestGHQueryNotStackedWhileInFlight(t *testing.T) {
	m := homeWithAppState(t)
	require.NotNil(t, m.maybeGHQuery())
	m.lastGHQuery = time.Now().Add(-ghInterval - time.Second)
	assert.Nil(t, m.maybeGHQuery())
}

func TestGHQueryDisabledWhenCLIUnavailable(t *testing.T) {
	m := homeWithAppState(t)
	m.ghAvailable = ghAvailability{checked: true, ok: false, reason: "no gh"}
	assert.Nil(t, m.maybeGHQuery(), "a known-unavailable gh must not spawn subprocesses")
	assert.False(t, m.ghInFlight)
}

func TestGHReadyClearsInFlightAndReplacesWholesale(t *testing.T) {
	m := homeWithAppState(t)
	m.ghInFlight = true
	m.ghState = map[string]github.Snapshot{"/old": {}}

	m.Update(ghReadyMsg{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{"/repo": {PRs: map[string]github.PR{}}},
		errs:      map[string]error{"/old": errors.New("boom")},
	})
	assert.False(t, m.ghInFlight)
	_, hasOld := m.ghState["/old"]
	assert.False(t, hasOld, "an errored repo is dropped, not retained")
	_, hasNew := m.ghState["/repo"]
	assert.True(t, hasNew)
}

func TestGHReadyJoinsOntoInstances(t *testing.T) {
	m := homeWithAppState(t)
	inst := addReadyInstance(t, m)
	inst.Branch = "u/b"
	inst.SetIssue(12)
	repo := inst.Path

	m.Update(ghReadyMsg{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{repo: {
			PRs:    map[string]github.PR{"u/b": {Number: 45, State: github.PROpen}},
			Issues: map[int]github.Issue{12: {Number: 12, Title: "Fix"}},
		}},
	})
	s := inst.GitHubState()
	assert.True(t, s.Known)
	assert.Equal(t, 45, s.PRNumber)
	assert.Equal(t, "Fix", s.IssueTitle)
}

func TestGHReadyUnknownRepoLeavesStateUnknown(t *testing.T) {
	m := homeWithAppState(t)
	inst := addReadyInstance(t, m)
	m.Update(ghReadyMsg{available: ghAvailability{checked: true, ok: true}, snapshots: map[string]github.Snapshot{}})
	assert.False(t, inst.GitHubState().Known)
}

func TestGHRefreshMsgZeroesWindow(t *testing.T) {
	m := homeWithAppState(t)
	m.lastGHQuery = time.Now()
	m.Update(ghRefreshMsg{})
	assert.True(t, m.lastGHQuery.IsZero())
}

func TestLinkedIssuesCollectsNonZero(t *testing.T) {
	m := homeWithAppState(t)
	a := addReadyInstance(t, m)
	a.SetIssue(3)
	b, err := session.NewInstance(session.InstanceOptions{Title: "b", Path: a.Path, Program: "claude"})
	require.NoError(t, err)
	m.list.AddInstance(b)
	assert.Equal(t, []int{3}, m.linkedIssues(a.Path))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestGH|TestLinkedIssues' -v`
Expected: FAIL — undefined `maybeGHQuery`, `ghInterval`, `ghReadyMsg`, etc.

- [ ] **Step 3: Add home fields** in `app/app.go` next to the roster fields (around line 402):

```go
	// lastGHQuery / ghInFlight throttle the GitHub poller (see
	// maybeGHQuery in github.go), exactly like the roster pair. Zeroing
	// lastGHQuery forces the next health tick to poll.
	lastGHQuery time.Time
	ghInFlight  bool
	// ghAvailable caches gh's install/auth check, resolved by the first
	// poll. Until checked, polls proceed (the poll itself checks).
	ghAvailable ghAvailability
	// ghState is the latest GitHub snapshot per open repo path. Replaced
	// wholesale on every ghReadyMsg; a repo whose query failed is absent.
	ghState map[string]github.Snapshot
	// ghBases is the resolved base ref name per repo ("origin/main"),
	// refreshed by the poll and read by gatherMetadataCmd for parity.
	ghBases map[string]string
```

Add import `"github.com/aidan-bailey/loom/session/github"`.

- [ ] **Step 4: Create `app/github.go`**

```go
package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
)

// ghInterval is the GitHub poller's own cadence. It rides the health
// tick but dispatches at most this often: each poll is two gh
// subprocesses plus a git fetch per open repo, all network-bound.
const ghInterval = 60 * time.Second

// ghAvailability is the cached result of github.CheckCLI.
type ghAvailability struct {
	checked bool
	ok      bool
	reason  string
}

// ghRefreshMsg asks for an immediate poll on the next tick. Sent after
// a push, an issue-born session, and a workspace activation.
type ghRefreshMsg struct{}

// ghReadyMsg carries one poll's result for every open repo. errs holds
// repos whose query failed; they are dropped from ghState so a stale
// snapshot never keeps rendering. bases is the resolved base ref per
// repo, for parity.
type ghReadyMsg struct {
	available ghAvailability
	snapshots map[string]github.Snapshot
	errs      map[string]error
	bases     map[string]string
}

// ghPollRequest is the main-goroutine snapshot the poll Cmd works
// from. Everything it needs is copied here so the Cmd body touches no
// model state.
type ghPollRequest struct {
	repos      []string
	linked     map[string][]int
	configured string // config.BaseBranch
	check      bool   // run CheckCLI first
}

// openRepoPaths lists the repo path of every open workspace (or the
// classic single repo), deduplicated, in slot order.
func (m *home) openRepoPaths() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(m.slots) == 0 {
		add(m.repoPath())
		return out
	}
	for _, s := range m.slots {
		if s.wsCtx != nil {
			add(s.wsCtx.RepoPath)
		}
	}
	return out
}

// allInstances returns every instance across open slots (or the
// classic list).
func (m *home) allInstances() []*session.Instance {
	if len(m.slots) == 0 {
		return m.list.GetInstances()
	}
	var out []*session.Instance
	for _, s := range m.slots {
		if s.list != nil {
			out = append(out, s.list.GetInstances()...)
		}
	}
	return out
}

// linkedIssues lists the non-zero issue numbers of instances in repo.
func (m *home) linkedIssues(repo string) []int {
	var out []int
	for _, inst := range m.allInstances() {
		if inst.Path == repo && inst.IssueNumber() != 0 {
			out = append(out, inst.IssueNumber())
		}
	}
	return out
}

// maybeGHQuery returns a poll Cmd when one is due: gh not known
// unavailable, none in flight, and ghInterval since the last dispatch.
// Arms neither field when it dispatches nothing — no Cmd means no
// ghReadyMsg to disarm them. Update goroutine only.
func (m *home) maybeGHQuery() tea.Cmd {
	if m.ghAvailable.checked && !m.ghAvailable.ok {
		return nil
	}
	if m.ghInFlight || time.Since(m.lastGHQuery) < ghInterval {
		return nil
	}
	repos := m.openRepoPaths()
	if len(repos) == 0 {
		return nil
	}
	req := ghPollRequest{repos: repos, linked: map[string][]int{}, check: !m.ghAvailable.checked}
	if m.appConfig != nil {
		req.configured = m.appConfig.GetBaseBranch()
	}
	for _, r := range repos {
		req.linked[r] = m.linkedIssues(r)
	}
	m.ghInFlight = true
	m.lastGHQuery = time.Now()
	return ghPollCmd(req, internalexec.Default{})
}

// ghPollCmd runs one poll: an optional CLI check, then per repo a base
// resolve + fetch (always, even without gh) and the gh query (only
// when gh is available). Pure I/O; returns a single message.
func ghPollCmd(req ghPollRequest, r internalexec.Executor) tea.Cmd {
	return func() tea.Msg {
		msg := ghReadyMsg{snapshots: map[string]github.Snapshot{}, errs: map[string]error{}, bases: map[string]string{}}
		msg.available = ghAvailability{checked: true, ok: true}
		if req.check {
			if err := github.CheckCLI(r); err != nil {
				msg.available = ghAvailability{checked: true, ok: false, reason: err.Error()}
			}
		}
		for _, repo := range req.repos {
			if _, name, err := git.ResolveBaseCommit(repo, req.configured, nil); err == nil {
				msg.bases[repo] = name
				if ferr := git.FetchRef(repo, name, nil); ferr != nil {
					log.For("github").Debug("base.fetch_failed", "repo", repo, "err", ferr.Error())
				}
			}
			if !msg.available.ok {
				continue
			}
			snap, err := github.Query(context.Background(), repo, req.linked[repo], r)
			if err != nil {
				msg.errs[repo] = err
				continue
			}
			msg.snapshots[repo] = snap
		}
		return msg
	}
}

// handleGHReady applies a poll result: disarms the in-flight guard
// first (a miss latches the poller off), replaces ghState wholesale,
// and re-joins every instance.
func (m *home) handleGHReady(msg ghReadyMsg) {
	m.ghInFlight = false
	m.ghAvailable = msg.available
	for repo, err := range msg.errs {
		log.For("github").Debug("query_failed", "repo", repo, "err", err.Error())
	}
	m.ghState = msg.snapshots
	if msg.bases != nil {
		m.ghBases = msg.bases
	}
	m.applyGitHubState()
}

// applyGitHubState joins ghState onto every instance. Cheap and pure,
// so it also runs when a link is set outside a poll (issue pick).
func (m *home) applyGitHubState() {
	for _, inst := range m.allInstances() {
		snap, known := m.ghState[inst.Path]
		inst.SetGitHubState(github.StateFor(snap, known, inst.GetBranch(), inst.IssueNumber()))
	}
}
```

- [ ] **Step 5: Wire messages and tick in `app/app.go`**

In the health-tick handler, right after the `maybeSubagentScan` block (around line 1374):

```go
		// GitHub PR/issue state + base-branch fetch, on the poller's own
		// 60s cadence (see maybeGHQuery). nil when not due, in flight, or
		// gh is known unavailable.
		if poll := m.maybeGHQuery(); poll != nil {
			cmds = append(cmds, poll)
		}
```

In the message switch, next to `case rosterReadyMsg:`:

```go
	case ghReadyMsg:
		m.handleGHReady(msg)
		return m, nil
	case ghRefreshMsg:
		m.lastGHQuery = time.Time{}
		return m, nil
```

In `activateWorkspace` (line ~2736), at the end of the successful path add `m.lastGHQuery = time.Time{}` so a newly opened workspace polls on the next tick.

- [ ] **Step 6: Run tests**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestGH|TestLinkedIssues|TestRoster' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w app/github.go app/github_test.go app/app.go && go vet ./app/
git add app/
git commit -m "feat(app): batched GitHub poller with roster-style throttle and wholesale replace"
```

---

### Task 8: Parity on the metadata tick, push triggers refresh

**Files:**
- Modify: `app/app.go` `gatherMetadataCmd` (around line 2510-2550)
- Modify: `app/intents.go:282-294` (`pushActionFor`)
- Test: `app/github_test.go` (append), `app/intents_push_test.go` (new)

- [ ] **Step 1: Write the failing tests**

Append to `app/github_test.go`:

```go
func TestBaseFor_ReadsGHBases(t *testing.T) {
	m := homeWithAppState(t)
	m.ghBases = map[string]string{"/r": "origin/main"}
	assert.Equal(t, "origin/main", m.baseFor("/r"))
	assert.Equal(t, "", m.baseFor("/other"))
}
```

Create `app/intents_push_test.go`:

```go
package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// pushActionFor used to return nil on success; the GitHub poller needs
// a signal to refresh right after a push, so success now yields
// ghRefreshMsg. An unstarted instance has no worktree, so the Cmd
// returns the error path — this pins that the two outcomes are
// distinguishable.
func TestPushActionFor_ErrorPathReturnsError(t *testing.T) {
	m := newTestHome(t)
	inst := addReadyInstance(t, m)
	msg := pushActionFor(inst)()
	_, isErr := msg.(error)
	assert.True(t, isErr, "no worktree on a test instance: expected error, got %T", msg)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestBaseFor|TestPushActionFor' -v`
Expected: FAIL — `baseFor` undefined; push test may pass already (that is fine; it pins behavior).

- [ ] **Step 3: Add `baseFor` to `app/github.go`**

```go
// baseFor returns the resolved base ref for repo, or "" before the
// first poll resolved it (parity then stays unknown).
func (m *home) baseFor(repo string) string {
	return m.ghBases[repo]
}
```

- [ ] **Step 4: Thread parity through `gatherMetadataCmd`**

`gatherMetadataCmd` is a free function (`gatherMetadataCmd(active, selected, dirty)`). Add a parameter `bases map[string]string` and pass `m.ghBases` at the call site (line ~1359: `cmds = append(cmds, gatherMetadataCmd(active, selected, m.takeDirty(), m.ghBases))`). Maps are read-only inside the Cmd; `handleGHReady` replaces the map rather than mutating it, so the Cmd's copy of the reference stays consistent.

Inside the per-instance goroutine, after the diff refresh block (after the `if wantFull {...} else {...}` at ~2541-2545) add:

```go
				instance.UpdateParity(bases[instance.Path])
```

Update `gatherMetadataCmd`'s signature and every test caller (`grep -rn 'gatherMetadataCmd(' app/`), passing `nil` where tests don't care.

- [ ] **Step 5: Push success returns `ghRefreshMsg`**

In `app/intents.go` `pushActionFor`, change the final `return nil` to `return ghRefreshMsg{}`. Check `grep -rn 'pushActionFor' app/*_test.go` for a test asserting nil on success and update it to expect `ghRefreshMsg{}`.

- [ ] **Step 6: Run tests**

Run: `CGO_ENABLED=0 go test ./app/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w app/app.go app/intents.go app/github.go app/github_test.go app/intents_push_test.go && go vet ./app/
git add app/
git commit -m "feat(app): count base-branch parity on the metadata tick; refresh GitHub state after push"
```

---

### Task 9: Card rendering — `CardData.GitHub`, parity, overview and rail badges

**Files:**
- Modify: `ui/card.go:53-76` (CardData), `ui/card.go:92-104` (BuildCardData), `ui/card.go:482-500` (rail second line)
- Create: `ui/card_github.go`
- Modify: `ui/overview.go:255-300` (renderOverviewCard)
- Test: `ui/card_github_test.go` (new), `ui/overview_test.go` (extend `TestOverview_UniformCardHeight`)

- [ ] **Step 1: Write the failing tests**

Create `ui/card_github_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
)

func TestGitHubBadge(t *testing.T) {
	cases := []struct {
		name string
		gh   github.State
		want string
	}{
		{"unknown renders nothing", github.State{}, ""},
		{"known no PR", github.State{Known: true}, ""},
		{"draft", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PRDraft}, "PR#45 draft"},
		{"open", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PROpen}, "PR#45 open"},
		{"approved", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PROpen, Review: github.ReviewApproved}, "PR#45 ✓approved"},
		{"changes", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PROpen, Review: github.ReviewChangesRequested}, "PR#45 ✗changes"},
		{"merged", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PRMerged}, "PR#45 merged"},
		{"checks pending", github.State{Known: true, HasPR: true, PRNumber: 1, PRState: github.PROpen, Checks: github.ChecksPending}, "PR#1 open ●"},
		{"checks passing", github.State{Known: true, HasPR: true, PRNumber: 1, PRState: github.PROpen, Checks: github.ChecksPassing}, "PR#1 open ✓"},
		{"checks failing", github.State{Known: true, HasPR: true, PRNumber: 1, PRState: github.PROpen, Checks: github.ChecksFailing}, "PR#1 open ✗"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, plain(githubBadge(c.gh)))
		})
	}
}

func TestParityToken(t *testing.T) {
	assert.Equal(t, "", plain(parityToken(CardData{})))
	assert.Equal(t, "✓ even", plain(parityToken(CardData{HasParity: true})))
	assert.Equal(t, "↑3 ↓12", plain(parityToken(CardData{HasParity: true, Ahead: 3, Behind: 12})))
}

func TestRailToken(t *testing.T) {
	assert.Equal(t, "", plain(railGitHubToken(CardData{})))
	d := CardData{GitHub: github.State{Known: true, IssueNumber: 12}}
	assert.Equal(t, "#12", plain(railGitHubToken(d)))
	d.GitHub.HasPR, d.GitHub.PRState, d.GitHub.Checks = true, github.PRMerged, github.ChecksPassing
	assert.Equal(t, "#12 · PR ⇄✓", plain(railGitHubToken(d)))
	d.HasParity, d.Behind = true, 4
	assert.Equal(t, "#12 · PR ⇄✓ ↓4", plain(railGitHubToken(d)))
	only := CardData{HasParity: true, Behind: 2, GitHub: github.State{Known: true}}
	assert.Equal(t, "↓2", plain(railGitHubToken(only)))
	even := CardData{HasParity: true, GitHub: github.State{Known: true}}
	assert.Equal(t, "", plain(railGitHubToken(even)), "behind=0 is not worth rail space")
}

func TestRenderCard_RailShowsGitHubTokenOnStatusLine(t *testing.T) {
	d := CardData{Title: "t", Index: 1, Status: session.Ready,
		GitHub: github.State{Known: true, IssueNumber: 12, HasPR: true, PRState: github.PROpen}}
	out := plain(RenderCard(d, DensityRail, 40))
	lines := strings.Split(out, "\n")
	assert.Len(t, lines, 2, "rail cards stay two lines")
	assert.Contains(t, lines[1], "#12 · PR ●")
	assert.Contains(t, lines[1], "idle")
}

func TestRenderOverviewCard_ShowsIssueLineAndBadge(t *testing.T) {
	d := CardData{Title: "t", Status: session.Ready, Branch: "u/b",
		HasParity: true, Ahead: 1, Behind: 2,
		GitHub: github.State{Known: true, IssueNumber: 12, IssueTitle: "Fix flaky test", HasPR: true, PRNumber: 45, PRState: github.PROpen, Checks: github.ChecksFailing}}
	out := plain(renderOverviewCard(d, 60))
	assert.Contains(t, out, "#12 Fix flaky test")
	assert.Contains(t, out, "↑1 ↓2")
	assert.Contains(t, out, "PR#45 open ✗")
	assert.Equal(t, overviewCardHeight, len(strings.Split(out, "\n")))
}

func TestRenderOverviewCard_ClosedIssueMarked(t *testing.T) {
	d := CardData{Title: "t", Status: session.Ready, GitHub: github.State{Known: true, IssueNumber: 12, IssueTitle: "Done", IssueClosed: true}}
	out := plain(renderOverviewCard(d, 60))
	assert.Contains(t, out, "✓ #12 Done")
}

func TestRenderOverviewCard_UnlinkedHasNoIssueLine(t *testing.T) {
	d := CardData{Title: "t", Status: session.Ready, TailLines: []string{"a", "b", "c"}}
	out := plain(renderOverviewCard(d, 60))
	assert.NotContains(t, out, "#")
	// Unlinked cards get one extra tail line (3 shown) so height matches.
	assert.Contains(t, out, "a")
	assert.Equal(t, overviewCardHeight, len(strings.Split(out, "\n")))
}

func TestBuildCardData_CopiesGitHubAndParity(t *testing.T) {
	inst, err := session.NewInstance(session.InstanceOptions{Title: "t", Path: t.TempDir(), Program: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	inst.SetGitHubState(github.State{Known: true, IssueNumber: 9})
	d := BuildCardData(inst, false, "", 0)
	assert.Equal(t, 9, d.GitHub.IssueNumber)
	assert.False(t, d.HasParity)

	// Linked but not yet polled: the number still shows.
	fresh, err := session.NewInstance(session.InstanceOptions{Title: "f", Path: t.TempDir(), Program: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	fresh.SetIssue(7)
	d = BuildCardData(fresh, false, "", 0)
	assert.False(t, d.GitHub.Known)
	assert.Equal(t, 7, d.GitHub.IssueNumber)
}
```

Extend the `variants` slice in `TestOverview_UniformCardHeight` (`ui/overview_test.go:90`) with:

```go
		{Title: "linked-card", Status: session.Ready, Branch: "u/b", TailLines: []string{"a", "b", "c"},
			HasParity: true, Ahead: 100, Behind: 2000,
			GitHub: github.State{Known: true, IssueNumber: 1234, IssueTitle: strings.Repeat("long issue title ", 6),
				HasPR: true, PRNumber: 9999, PRState: github.PROpen, Review: github.ReviewChangesRequested, Checks: github.ChecksFailing}},
		{Title: "unlinked-three-tails", Status: session.Running, TailLines: []string{"a", "b", "c", "d"}},
```

and add the import `"github.com/aidan-bailey/loom/session/github"` to that test file.

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'TestGitHubBadge|TestParityToken|TestRailToken|TestRenderCard_RailShowsGitHub|TestRenderOverviewCard_|TestBuildCardData_Copies|TestOverview_UniformCardHeight' -v`
Expected: FAIL — `CardData` has no `GitHub`/`HasParity` fields.

- [ ] **Step 3: Extend `CardData` and `BuildCardData`** in `ui/card.go`

Add fields after `Subagents`:

```go
	// GitHub is the poller's join for this session (issue, PR, checks).
	// Known=false renders nothing. See app/github.go.
	GitHub github.State
	// Ahead/Behind count commits relative to the base branch; HasParity
	// is false until a count succeeded.
	Ahead, Behind int
	HasParity     bool
```

Import `"github.com/aidan-bailey/loom/session/github"`. In `BuildCardData` after the diff block:

```go
	d.GitHub = inst.GitHubState()
	d.GitHub.IssueTitle = sanitizeCardText(d.GitHub.IssueTitle)
	// Before the first poll the join is empty, but the link itself is
	// known from the instance — show "#12" immediately.
	if !d.GitHub.Known && inst.IssueNumber() != 0 {
		d.GitHub.IssueNumber = inst.IssueNumber()
	}
	d.Ahead, d.Behind, d.HasParity = inst.Parity()
```

- [ ] **Step 4: Create `ui/card_github.go`**

```go
package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/aidan-bailey/loom/session/github"
)

// Theme-derived styles must be hook-built (see ui/theme.go): init-time
// styles would capture pre-ApplyTheme colors.
var (
	ghDimStyle  lipgloss.Style
	ghOKStyle   lipgloss.Style
	ghErrStyle  lipgloss.Style
	ghTextStyle lipgloss.Style
)

func init() { RegisterThemeHook(rebuildGitHubStyles) }

func rebuildGitHubStyles() {
	ghDimStyle = lipgloss.NewStyle().Foreground(Dim)
	ghOKStyle = lipgloss.NewStyle().Foreground(OK)
	ghErrStyle = lipgloss.NewStyle().Foreground(ErrorColor)
	ghTextStyle = lipgloss.NewStyle().Foreground(Text)
}

// checksGlyph is the one-character CI summary; "" for none.
func checksGlyph(c github.Checks) string {
	switch c {
	case github.ChecksPending:
		return ghDimStyle.Render("●")
	case github.ChecksPassing:
		return ghOKStyle.Render("✓")
	case github.ChecksFailing:
		return ghErrStyle.Render("✗")
	default:
		return ""
	}
}

// githubBadge renders the overview PR badge: "PR#45 open ✓". Empty
// when unknown or no PR. Merged PRs dim the whole badge.
func githubBadge(s github.State) string {
	if !s.Known || !s.HasPR {
		return ""
	}
	label := fmt.Sprintf("PR#%d ", s.PRNumber)
	var word string
	style := ghTextStyle
	switch {
	case s.PRState == github.PRMerged:
		word, style = "merged", ghDimStyle
	case s.PRState == github.PRClosed:
		word, style = "closed", ghDimStyle
	case s.PRState == github.PRDraft:
		word, style = "draft", ghDimStyle
	case s.Review == github.ReviewApproved:
		word, style = "✓approved", ghOKStyle
	case s.Review == github.ReviewChangesRequested:
		word, style = "✗changes", ghErrStyle
	default:
		word = "open"
	}
	out := style.Render(label + word)
	if g := checksGlyph(s.Checks); g != "" {
		out += " " + g
	}
	return out
}

// parityToken renders "↑N ↓M" (or "✓ even"); "" when unknown.
func parityToken(d CardData) string {
	if !d.HasParity {
		return ""
	}
	if d.Ahead == 0 && d.Behind == 0 {
		return ghDimStyle.Render("✓ even")
	}
	return ghOKStyle.Render(fmt.Sprintf("↑%d", d.Ahead)) + " " + ghDimStyle.Render(fmt.Sprintf("↓%d", d.Behind))
}

// prGlyph is the rail's one-character PR state.
func prGlyph(s github.State) string {
	switch {
	case s.PRState == github.PRMerged:
		return "⇄"
	case s.PRState == github.PRDraft:
		return "○"
	case s.Review == github.ReviewApproved:
		return "✓"
	case s.Review == github.ReviewChangesRequested:
		return "✗"
	default:
		return "●"
	}
}

// railGitHubToken is the compact right-side token for rail cards:
// "#12 · PR ⇄✓ ↓4". Only actionable parts appear: the issue number,
// the PR state+checks, and "behind" when nonzero.
func railGitHubToken(d CardData) string {
	var parts []string
	if d.GitHub.IssueNumber != 0 {
		parts = append(parts, ghDimStyle.Render(fmt.Sprintf("#%d", d.GitHub.IssueNumber)))
	}
	if d.GitHub.Known && d.GitHub.HasPR {
		parts = append(parts, ghDimStyle.Render("PR "+prGlyph(d.GitHub))+checksGlyph(d.GitHub.Checks))
	}
	tok := strings.Join(parts, ghDimStyle.Render(" · "))
	if d.HasParity && d.Behind > 0 {
		if tok != "" {
			tok += " "
		}
		tok += ghDimStyle.Render(fmt.Sprintf("↓%d", d.Behind))
	}
	return tok
}

// issueLine renders the overview's issue line; "" when unlinked.
func issueLine(d CardData, inner int) string {
	if d.GitHub.IssueNumber == 0 {
		return ""
	}
	text := fmt.Sprintf("#%d %s", d.GitHub.IssueNumber, d.GitHub.IssueTitle)
	if d.GitHub.IssueClosed {
		return ghDimStyle.Render(truncate("✓ "+text, inner))
	}
	return ghTextStyle.Render(truncate(text, inner))
}

// finished reports whether the card's work is done from GitHub's
// point of view (merged PR or closed issue), which dims meta lines.
func (d CardData) finished() bool {
	return d.GitHub.Known && ((d.GitHub.HasPR && d.GitHub.PRState == github.PRMerged) || d.GitHub.IssueClosed)
}
```

- [ ] **Step 5: Overview card** — in `renderOverviewCard` (`ui/overview.go`), replace the `meta`/`mid` block and the content assembly:

```go
	dim := lipgloss.NewStyle().Foreground(Dim)
	var right []string
	if p := parityToken(d); p != "" {
		right = append(right, p)
	}
	if b := githubBadge(d.GitHub); b != "" {
		right = append(right, b)
	}
	if d.HasDiff {
		right = append(right, lipgloss.NewStyle().Foreground(OK).Render(fmt.Sprintf("+%d", d.DiffAdded))+" "+
			lipgloss.NewStyle().Foreground(ErrorColor).Render(fmt.Sprintf("−%d", d.DiffRemoved)))
	}
	meta := strings.Join(right, " ")
	if lipgloss.Width(meta) > inner {
		// Styled composition, so ANSI-aware truncation.
		meta = ansi.Truncate(meta, inner, "…")
	}
	branchStyle := dim
	if d.finished() {
		meta = dim.Render(ansi.Strip(meta))
	}
	mid := spreadLine(branchStyle.Render(truncate(d.Branch, inner-lipgloss.Width(meta)-1)), meta, inner)

	rule := lipgloss.NewStyle().Foreground(Rule).Render(strings.Repeat("─", inner))

	// A linked card spends one line on the issue; an unlinked card gives
	// that line to the tail so every card is overviewCardHeight tall.
	lines := []string{top}
	tailN := overviewCardTailLines + 1
	if il := issueLine(d, inner); il != "" {
		lines = append(lines, il)
		tailN = overviewCardTailLines
	}
	lines = append(lines, mid, rule)
	lines = append(lines, overviewTailN(d, inner, tailN)...)
	content := strings.Join(lines, "\n")
```

`overviewCardHeight` becomes `overviewCardTailLines + 6` (title, issue-or-extra-tail, branch, rule, 2 tails, 2 borders = tails+6). Update the constant and its comment in `ui/overview.go:16-23`.

In `ui/card_agents.go`, rename `overviewTail(d, inner)` to `overviewTailN(d CardData, inner, n int)` and replace the two `overviewCardTailLines` uses at its end with `n`; the `TailLines` it shows for the `n == 0` case already use whatever `d.TailLines` holds, so `BuildCardData`'s `tailN` argument in `ui/overview.go:156` must become `overviewCardTailLines+1` so three lines are available. `grep -rn 'overviewTail(' ui/` and fix callers.

- [ ] **Step 6: Rail card** — in `RenderCard` (`ui/card.go`), replace the `secondLine` assembly:

```go
	secondStyle := lipgloss.NewStyle().Foreground(secondFg)
	if solidBg {
		secondStyle = secondStyle.Background(Panel)
	}
	tok := railGitHubToken(d)
	body := second
	if tok != "" {
		body = truncate(second, inner-lipgloss.Width(tok)-1)
		secondLine := bar + sep + spreadLine(secondStyle.Render(body), tok, inner)
		if d.finished() {
			titleLine = bar + sep + titleStyleC.Foreground(Dim).Render(title)
		}
		if solidBg {
			pad := lipgloss.NewStyle().Background(Panel).Width(width)
			return pad.Render(titleLine) + "\n" + pad.Render(secondLine)
		}
		return titleLine + "\n" + secondLine
	}
	secondLine := bar + sep + secondStyle.Render(truncate(body, inner))
```

(keep the existing tail of the function after this for the no-token path). `spreadLine` lives in `ui/overview.go`; it is package-level so `card.go` can use it.

- [ ] **Step 7: Run tests**

Run: `CGO_ENABLED=0 go test ./ui/...`
Expected: PASS, including `TestOverview_UniformCardHeight` at all widths. If an existing test pins `overviewCardHeight` numerically or the rail's second-line content, update it to the new invariant.

- [ ] **Step 8: Commit**

```bash
gofmt -w ui/card.go ui/card_github.go ui/card_github_test.go ui/card_agents.go ui/overview.go ui/overview_test.go && go vet ./ui/...
git add ui/
git commit -m "feat(ui): issue, PR, checks and parity badges on rail and overview cards"
```

---

### Task 10: Script intent, key binding, help entry

**Files:**
- Modify: `script/intent.go:77-80,116-131`, `script/api_actions.go:271-276`, `script/defaults.lua:15-16`, `script/loader_defaults_test.go:24-31`
- Modify: `keys/keys.go:21-30,120-132`
- Test: `script/api_actions_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `script/api_actions_test.go` (mirror the `merge_selected` test at line ~287):

```go
func TestActionsNewFromIssueEnqueuesIntent(t *testing.T) {
	e, h := newEngineWithRecordingHost(t)
	require.NoError(t, e.L.DoString(`cs.bind("I", function() cs.actions.new_from_issue() end)`))
	_, err := e.Dispatch(context.Background(), "I", h)
	require.NoError(t, err)
	require.Len(t, h.enqueued, 1)
	_, ok := h.enqueued[0].(NewFromIssueIntent)
	assert.True(t, ok)
}
```

Copy the exact engine/host construction the `merge_selected` test uses (`sed -n 280,295p script/api_actions_test.go`) if `newEngineWithRecordingHost` is not its helper's name.

Add `"I"` to the stock-key list in `script/loader_defaults_test.go` (after `"m"`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./script/ -run 'TestActionsNewFromIssue|TestEngineLoadsEmbeddedDefaults' -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

`script/intent.go`, after `MergeSessionsIntent`:

```go
// NewFromIssueIntent asks the app to open the GitHub issue picker; the
// chosen issue seeds a new session's title and prompt.
type NewFromIssueIntent struct{}
```

and `func (NewFromIssueIntent) intent() {}` in the method block. Add `var _ Intent = NewFromIssueIntent{}` to `script/intent_test.go:29` block.

`script/api_actions.go`, after `merge_selected`:

```go
	actions.RawSetString("new_from_issue", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, NewFromIssueIntent{})
	}))
```

`script/defaults.lua`, after the `N` line:

```lua
cs.bind("I", function() cs.actions.new_from_issue() end,            { help = "new from issue" })
```

`keys/keys.go`: add `KeyNewFromIssue` to the `KeyName` enum after `KeyMerge`, and in the binding map:

```go
	KeyNewFromIssue: key.NewBinding(
		key.WithKeys("I"),
		key.WithHelp("I", "new from issue"),
	),
```

Check whether `keys.go` has a string→KeyName reverse table used by `KeyForString` that must also list `"I"`; add it if so.

- [ ] **Step 4: Run tests**

Run: `CGO_ENABLED=0 go test ./script/ ./keys/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w script/*.go keys/keys.go && go vet ./script/ ./keys/
git add script/ keys/
git commit -m "feat(script): bind I to cs.actions.new_from_issue"
```

---

### Task 11: Issue picker overlay

**Files:**
- Create: `ui/overlay/issuePicker.go`
- Test: `ui/overlay/issuePicker_test.go`

- [ ] **Step 1: Write the failing test**

```go
package overlay

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	default:
		r := []rune(s)
		return tea.KeyPressMsg{Code: r[0], Text: s}
	}
}

func rows() []IssueRow {
	return []IssueRow{
		{Number: 12, Title: "Fix flaky test", Labels: []string{"bug"}},
		{Number: 13, Title: "Add dark theme"},
		{Number: 20, Title: "Flaky CI on main"},
	}
}

func TestIssuePicker_NavigateAndPick(t *testing.T) {
	p := NewIssuePicker(rows())
	committed, canceled := p.HandleKeyPress(key("down"))
	assert.False(t, committed)
	committed, canceled = p.HandleKeyPress(key("enter"))
	assert.True(t, committed)
	assert.False(t, canceled)
	require.NotNil(t, p.Selected())
	assert.Equal(t, 13, p.Selected().Number)
}

func TestIssuePicker_EscCancels(t *testing.T) {
	p := NewIssuePicker(rows())
	committed, canceled := p.HandleKeyPress(key("esc"))
	assert.True(t, committed)
	assert.True(t, canceled)
}

func TestIssuePicker_FilterMatchesNumberAndTitle(t *testing.T) {
	p := NewIssuePicker(rows())
	for _, r := range "flaky" {
		p.HandleKeyPress(key(string(r)))
	}
	assert.Equal(t, []int{12, 20}, p.VisibleNumbers())
	p.HandleKeyPress(key("enter"))
	assert.Equal(t, 12, p.Selected().Number)

	p = NewIssuePicker(rows())
	p.HandleKeyPress(key("2"))
	p.HandleKeyPress(key("0"))
	assert.Equal(t, []int{20}, p.VisibleNumbers())
}

func TestIssuePicker_BackspaceEditsFilter(t *testing.T) {
	p := NewIssuePicker(rows())
	p.HandleKeyPress(key("z"))
	assert.Empty(t, p.VisibleNumbers())
	p.HandleKeyPress(key("backspace"))
	assert.Len(t, p.VisibleNumbers(), 3)
}

func TestIssuePicker_SelectedNilWhenFilteredOut(t *testing.T) {
	p := NewIssuePicker(rows())
	p.HandleKeyPress(key("z"))
	committed, canceled := p.HandleKeyPress(key("enter"))
	assert.True(t, committed)
	assert.False(t, canceled)
	assert.Nil(t, p.Selected())
}

func TestIssuePicker_RenderStatusAndRows(t *testing.T) {
	p := NewIssuePicker(nil)
	p.SetStatus("loading…")
	out := ansi.Strip(p.Render())
	assert.Contains(t, out, "loading…")

	p.SetRows(rows())
	p.SetStatus("")
	out = ansi.Strip(p.Render())
	assert.Contains(t, out, "#12")
	assert.Contains(t, out, "Fix flaky test")
	assert.Contains(t, out, "[bug]")
	assert.True(t, strings.Contains(out, "esc cancel"))
}

func TestIssuePicker_SetRowsClampsCursor(t *testing.T) {
	p := NewIssuePicker(rows())
	p.HandleKeyPress(key("down"))
	p.HandleKeyPress(key("down"))
	p.SetRows(rows()[:1])
	p.HandleKeyPress(key("enter"))
	assert.Equal(t, 12, p.Selected().Number)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/overlay/ -run TestIssuePicker -v`
Expected: FAIL — undefined `NewIssuePicker`.

- [ ] **Step 3: Write the overlay**

```go
package overlay

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/aidan-bailey/loom/ui"
)

// IssueRow is one selectable GitHub issue. Plain fields only so this
// package does not import session/github.
type IssueRow struct {
	Number int
	Title  string
	Labels []string
}

// IssuePicker lists open issues with a type-to-filter line. Filtering
// matches the number (prefix) or the title (case-insensitive
// substring). Enter picks the highlighted visible row; Esc cancels.
type IssuePicker struct {
	rows    []IssueRow
	visible []int // indices into rows after filtering
	filter  string
	cursor  int // index into visible
	status  string
	width   int
}

// NewIssuePicker creates a picker over rows (may be nil while loading).
func NewIssuePicker(rows []IssueRow) *IssuePicker {
	p := &IssuePicker{width: 72}
	p.SetRows(rows)
	return p
}

// SetRows replaces the row set (e.g. when a poll lands while the picker
// is open), re-applies the filter, and clamps the cursor.
func (p *IssuePicker) SetRows(rows []IssueRow) {
	p.rows = rows
	p.applyFilter()
}

// SetStatus shows a line under the header ("loading…", "gh
// unavailable: …"); "" hides it.
func (p *IssuePicker) SetStatus(s string) { p.status = s }

func (p *IssuePicker) applyFilter() {
	p.visible = p.visible[:0]
	f := strings.ToLower(strings.TrimSpace(p.filter))
	for i, r := range p.rows {
		if f == "" || strings.HasPrefix(strconv.Itoa(r.Number), f) || strings.Contains(strings.ToLower(r.Title), f) {
			p.visible = append(p.visible, i)
		}
	}
	if p.cursor >= len(p.visible) {
		p.cursor = len(p.visible) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// VisibleNumbers lists the numbers of the rows the filter shows (tests).
func (p *IssuePicker) VisibleNumbers() []int {
	out := make([]int, 0, len(p.visible))
	for _, i := range p.visible {
		out = append(out, p.rows[i].Number)
	}
	return out
}

// HandleKeyPress returns (committed, canceled) like MergePicker.
func (p *IssuePicker) HandleKeyPress(msg tea.KeyPressMsg) (bool, bool) {
	switch msg.String() {
	case "up", "ctrl+p":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "ctrl+n":
		if p.cursor < len(p.visible)-1 {
			p.cursor++
		}
	case "enter":
		return true, false
	case "esc", "ctrl+c":
		return true, true
	case "backspace":
		if p.filter != "" {
			r := []rune(p.filter)
			p.filter = string(r[:len(r)-1])
			p.applyFilter()
		}
	default:
		if msg.Text != "" && !strings.ContainsAny(msg.Text, "\n\r\t") {
			p.filter += msg.Text
			p.cursor = 0
			p.applyFilter()
		}
	}
	return false, false
}

// Selected returns the highlighted visible row, or nil when the filter
// hides everything.
func (p *IssuePicker) Selected() *IssueRow {
	if p.cursor < 0 || p.cursor >= len(p.visible) {
		return nil
	}
	return &p.rows[p.visible[p.cursor]]
}

// HandleKey satisfies the Overlay interface.
func (p *IssuePicker) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := p.HandleKeyPress(msg)
	return closed, nil
}

// View satisfies the Overlay interface.
func (p *IssuePicker) View() string { return p.Render() }

// SetSize satisfies the Overlay interface; only width is used.
func (p *IssuePicker) SetSize(width, _ int) { p.width = width }

// maxIssueRows caps the visible list so a large repo does not push the
// hint line off screen.
const maxIssueRows = 15

// Render draws the picker.
func (p *IssuePicker) Render() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	selectedStyle := lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	normalStyle := lipgloss.NewStyle().Foreground(ui.Text)
	dimStyle := lipgloss.NewStyle().Foreground(ui.Dim)
	hintStyle := lipgloss.NewStyle().Foreground(ui.Faint)

	var b strings.Builder
	b.WriteString(titleStyle.Render("New session from GitHub issue") + "\n")
	b.WriteString(dimStyle.Render("filter: ") + normalStyle.Render(p.filter+"▏") + "\n\n")
	if p.status != "" {
		b.WriteString(dimStyle.Render(p.status) + "\n")
	}
	if len(p.visible) == 0 && p.status == "" {
		b.WriteString(dimStyle.Render("no matching open issues") + "\n")
	}
	// Window the list around the cursor.
	start := 0
	if p.cursor >= maxIssueRows {
		start = p.cursor - maxIssueRows + 1
	}
	end := min(start+maxIssueRows, len(p.visible))
	for vi := start; vi < end; vi++ {
		r := p.rows[p.visible[vi]]
		labels := ""
		if len(r.Labels) > 0 {
			labels = "  [" + strings.Join(r.Labels, ", ") + "]"
		}
		line := fmt.Sprintf("  #%-5d %s%s", r.Number, r.Title, labels)
		if vi == p.cursor {
			line = "> " + line[2:]
			b.WriteString(selectedStyle.Render(line) + "\n")
		} else {
			b.WriteString(normalStyle.Render(line) + "\n")
		}
	}
	b.WriteString("\n" + hintStyle.Render("type to filter • ↑↓ move • enter start session • esc cancel"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(p.width).
		Render(b.String())
}
```

- [ ] **Step 4: Run tests**

Run: `CGO_ENABLED=0 go test ./ui/overlay/ -run TestIssuePicker -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w ui/overlay/issuePicker.go ui/overlay/issuePicker_test.go && go vet ./ui/overlay/
git add ui/overlay/
git commit -m "feat(overlay): GitHub issue picker with type-to-filter"
```

---

### Task 12: App state for the picker, pick flow, shared launch-options helper

**Files:**
- Modify: `app/app.go` (state enum ~line 158, overlay gating ~2144, key switch ~2194, message switch)
- Modify: `app/overlay_host.go` (kind + accessor)
- Modify: `app/app_scripts.go:603-604` (intent dispatch)
- Modify: `app/state_default.go:82` (overview intercept)
- Modify: `app/state_new.go:49-80` (use helper)
- Create: `app/state_issue_picker.go`
- Test: `app/state_issue_picker_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package app

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunNewFromIssue_OpensPickerFromSnapshot(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	m.ghState = map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{
		12: {Number: 12, Title: "Fix"}, 13: {Number: 13, Title: "Closed one", Closed: true},
	}}}
	_, _ = runNewFromIssue(m)
	require.Equal(t, stateIssuePicker, m.state)
	p := m.issuePicker()
	require.NotNil(t, p)
	assert.Equal(t, []int{12}, p.VisibleNumbers(), "closed issues are not offered")
}

func TestRunNewFromIssue_NoSnapshotShowsLoadingAndForcesPoll(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	m.lastGHQuery = time.Now()
	_, _ = runNewFromIssue(m)
	require.Equal(t, stateIssuePicker, m.state)
	assert.True(t, m.lastGHQuery.IsZero())
	assert.Contains(t, m.issuePicker().Render(), "loading")
}

func TestRunNewFromIssue_UnavailableGHErrors(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: false, reason: "no gh"}
	_, cmd := runNewFromIssue(m)
	assert.Equal(t, stateDefault, m.state)
	assert.NotNil(t, cmd, "error surfaces via handleError")
}

func TestGHReadyRefreshesOpenPicker(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	_, _ = runNewFromIssue(m)
	m.Update(ghReadyMsg{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{7: {Number: 7, Title: "New"}}}},
	})
	assert.Equal(t, []int{7}, m.issuePicker().VisibleNumbers())
}

func TestIssuePickerEnter_DispatchesView(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	m.ghState = map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{12: {Number: 12, Title: "Fix"}}}}
	_, _ = runNewFromIssue(m)
	_, cmd := handleStateIssuePickerKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.activeOverlay)
	assert.NotNil(t, cmd)
}

func TestIssuePickedMsg_CreatesLinkedInstanceAndOpensLaunchOptions(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	before := m.list.NumInstances()
	m.Update(issuePickedMsg{issue: github.Issue{Number: 12, Title: "Fix flaky test", URL: "https://x/12", Body: "do it"}})
	require.Equal(t, before+1, m.list.NumInstances())
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	assert.Equal(t, "gh-12-fix-flaky-test", inst.Title)
	assert.Equal(t, 12, inst.IssueNumber())
	assert.Contains(t, inst.Prompt, "# Fix flaky test")
	assert.Equal(t, stateLaunchOptions, m.state)
	_, ok := m.activeOverlay.(*overlay.SessionLaunchOptions)
	assert.True(t, ok)
	assert.True(t, m.lastGHQuery.IsZero(), "an issue-born session forces the next poll")
}

func TestIssuePickedMsg_ErrorCreatesNothing(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	before := m.list.NumInstances()
	_, cmd := m.Update(issuePickedMsg{err: errors.New("boom")})
	assert.Equal(t, before, m.list.NumInstances())
	assert.Equal(t, stateDefault, m.state)
	assert.NotNil(t, cmd)
}

func TestIssuePickedMsg_RespectsInstanceLimit(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	for i := 0; i < GlobalInstanceLimit; i++ {
		inst, err := session.NewInstance(session.InstanceOptions{Title: "x", Path: t.TempDir(), Program: "claude"})
		require.NoError(t, err)
		m.list.AddInstance(inst)
	}
	m.Update(issuePickedMsg{issue: github.Issue{Number: 1, Title: "t"}})
	assert.Equal(t, GlobalInstanceLimit, m.list.NumInstances())
}
```

Add `"time"` to the test file's imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestRunNewFromIssue|TestGHReadyRefreshesOpenPicker|TestIssuePicker|TestIssuePickedMsg' -v`
Expected: FAIL — undefined `runNewFromIssue`, `stateIssuePicker`, etc.

- [ ] **Step 3: State enum, overlay kind, gating, dispatch**

`app/app.go` state consts: add after `stateLaunchOptions`:

```go
	// stateIssuePicker is the state when the GitHub issue picker overlay
	// is displayed (opened by the 'I' key).
	stateIssuePicker
```

Line ~2144 overlay gating chain: append `|| m.state == stateIssuePicker`.
Line ~2194 key switch: add `case stateIssuePicker: return handleStateIssuePickerKey(m, msg)`.
Message switch: add

```go
	case issuePickedMsg:
		return m.handleIssuePicked(msg)
```

`app/overlay_host.go`: add `overlayIssuePicker` to the kind enum and

```go
// issuePicker returns the active IssuePicker, or nil when a different
// overlay is active.
func (m *home) issuePicker() *overlay.IssuePicker {
	if o, ok := m.activeOverlay.(*overlay.IssuePicker); ok {
		return o
	}
	return nil
}
```

`app/app_scripts.go` after the `MergeSessionsIntent` case:

```go
	case script.NewFromIssueIntent:
		_, cmd = runNewFromIssue(m)
```

`app/state_default.go:82`: change `case "n", "N":` to `case "n", "N", "I":` (the comment already explains why creation drops to focus first).

In `handleGHReady` (`app/github.go`) append, after `m.applyGitHubState()`:

```go
	if p := m.issuePicker(); p != nil {
		p.SetRows(m.issueRows())
		p.SetStatus(m.issuePickerStatus())
	}
```

- [ ] **Step 4: Create `app/state_issue_picker.go`**

```go
package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
)

// issuePickedMsg carries the full issue (with body) after the user
// chose a row, or the View error.
type issuePickedMsg struct {
	issue github.Issue
	err   error
}

// issueRows lists the focused repo's open issues, newest first.
func (m *home) issueRows() []overlay.IssueRow {
	snap, ok := m.ghState[m.repoPath()]
	if !ok {
		return nil
	}
	rows := make([]overlay.IssueRow, 0, len(snap.Issues))
	for _, is := range snap.Issues {
		if is.Closed {
			continue
		}
		rows = append(rows, overlay.IssueRow{Number: is.Number, Title: is.Title, Labels: is.Labels})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Number > rows[j].Number })
	return rows
}

// issuePickerStatus is the picker's status line for the current poll
// state: "" once a snapshot exists, "loading…" before.
func (m *home) issuePickerStatus() string {
	if _, ok := m.ghState[m.repoPath()]; ok {
		return ""
	}
	return "loading…"
}

// runNewFromIssue opens the issue picker. When no snapshot exists yet
// it opens in the loading state and forces the next tick to poll.
func runNewFromIssue(m *home) (tea.Model, tea.Cmd) {
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}
	if m.ghAvailable.checked && !m.ghAvailable.ok {
		return m, m.handleError(fmt.Errorf("gh unavailable: %s", m.ghAvailable.reason))
	}
	p := overlay.NewIssuePicker(m.issueRows())
	p.SetStatus(m.issuePickerStatus())
	if _, ok := m.ghState[m.repoPath()]; !ok {
		m.lastGHQuery = time.Time{}
	}
	m.setOverlay(p, overlayIssuePicker)
	m.state = stateIssuePicker
	return m, nil
}

// handleStateIssuePickerKey drives the picker. Enter fetches the full
// issue in a Cmd; the instance is created in handleIssuePicked.
func handleStateIssuePickerKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.issuePicker()
	if p == nil {
		return m, nil
	}
	committed, canceled := p.HandleKeyPress(msg)
	if !committed {
		return m, nil
	}
	row := p.Selected()
	m.dismissOverlay()
	m.state = stateDefault
	if canceled || row == nil {
		return m, nil
	}
	repo := m.repoPath()
	n := row.Number
	return m, func() tea.Msg {
		is, err := github.View(context.Background(), repo, n, internalexec.Default{})
		return issuePickedMsg{issue: is, err: err}
	}
}

// handleIssuePicked creates the pre-started instance titled by the
// issue slug, seeds its prompt, links the issue, and opens the launch
// options modal — the same path n/N take after title entry.
func (m *home) handleIssuePicked(msg issuePickedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, m.handleError(fmt.Errorf("fetch issue: %w", msg.err))
	}
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:     github.SlugTitle(msg.issue),
		Path:      m.repoPath(),
		Program:   m.program,
		ConfigDir: m.configDir(),
	})
	if err != nil {
		return m, m.handleError(err)
	}
	instance.Prompt = github.SeedPrompt(msg.issue)
	instance.SetIssue(msg.issue.Number)
	m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.lastGHQuery = time.Time{}
	m.applyGitHubState()
	return m.openLaunchOptionsForNew(instance, "")
}

// openLaunchOptionsForNew shows the Session Launch Options modal for an
// unstarted instance already in the list. Confirming composes the
// program from the chosen options and starts the instance; cancelling
// pops it (killPendingLaunchOptionsCancel). selectedBranch is threaded
// to instanceStartedMsg for the N flow's branch picker.
func (m *home) openLaunchOptionsForNew(instance *session.Instance, selectedBranch string) (tea.Model, tea.Cmd) {
	m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		startTask := overlay.ConfirmationTask{
			Sync: func() {
				instance.Program = applyLaunchOptions(opts, m.rcAuth, instance.Program, instance.Title)
				instance.HeadroomProxy = opts.HeadroomProxy
				instance.CacheTTL1h = opts.CacheTTL1h
				// Always recorded, edited or not, so branch composition has a
				// single source of truth instead of falling back to a re-read
				// of config.json inside the git package.
				instance.SetBranchPrefix(opts.BranchPrefix)
				_ = instance.TransitionTo(session.Loading)
				m.promptAfterName = false
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
			},
			Async: tea.Batch(tea.RequestWindowSize, func() tea.Msg {
				err := instance.Start(true)
				return instanceStartedMsg{
					instance:        instance,
					err:             err,
					promptAfterName: false,
					selectedBranch:  selectedBranch,
				}
			}),
		}
		if m.remoteControlBlocked(effectiveRemoteControl(opts), instance.Program) {
			return m, m.promptRemoteControlBlocked(startTask)
		}
		return m, tea.Batch(startTask.Run(), m.instanceChanged())
	}
	m.pendingLaunchOptionsCancel = m.killPendingLaunchOptionsCancel
	m.state = stateLaunchOptions
	m.setOverlay(overlay.NewSessionLaunchOptions(launchOptionsFromConfig(m.appConfig), m.rcAuth.Blocked(), m.rcAuth.Reason), overlayLaunchOptions)
	m.menu.SetState(ui.StateNewInstance)
	return m, tea.RequestWindowSize
}
```

Check `instanceStartedMsg` has a `selectedBranch` field (it does in `state_prompt.go:75`). If `handleStateNewKey`'s literal omits it, zero is fine.

- [ ] **Step 5: Make `handleStateNewKey` use the helper**

In `app/state_new.go`, replace everything in the `case tea.KeyEnter:` branch from `m.pendingLaunchOptions = func(...)` through the `return m, tea.RequestWindowSize` that closes the launch-options setup with:

```go
		return m.openLaunchOptionsForNew(instance, "")
```

Read `sed -n 60,110p app/state_new.go` first to confirm the replaced block ends exactly at that `return` and contains nothing the helper lacks. Then run the existing new-instance tests: `CGO_ENABLED=0 go test ./app/ -run 'StateNew|LaunchOptions|BranchPrefix' -v`.

- [ ] **Step 6: Run tests**

Run: `CGO_ENABLED=0 go test ./app/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w app/*.go && go vet ./app/
git add app/
git commit -m "feat(app): issue picker state and issue-seeded session creation"
```

---

### Task 13: `#123` shorthand in the N prompt flow

**Files:**
- Modify: `app/state_prompt.go:40-60`
- Modify: `app/state_issue_picker.go` (add `issueExpandedMsg` + handler)
- Modify: `app/app.go` (message switch)
- Test: `app/state_prompt_issue_test.go` (new)

- [ ] **Step 1: Write the failing tests**

```go
package app

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Drives n → title → N-style prompt overlay for an unstarted instance.
func promptOverlayForNewInstance(t *testing.T, m *home) {
	t.Helper()
	_, _ = runPromptNewInstance(m)
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	inst.Title = "shorthand"
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, statePrompt, m.state)
}

func TestPromptShorthand_DispatchesExpansion(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	promptOverlayForNewInstance(t, m)
	ti := m.textInput()
	require.NotNil(t, ti)
	ti.SetValue("#12 and tidy tests")
	ti.MarkSubmitted() // see note below
	_, cmd := handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.NotNil(t, cmd)
	assert.Equal(t, stateDefault, m.state, "waits for the expansion before launch options")
}

func TestIssueExpandedMsg_SeedsPromptAndLinks(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	promptOverlayForNewInstance(t, m)
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	m.dismissOverlay()
	m.state = stateDefault
	m.Update(issueExpandedMsg{instance: inst, rest: "and tidy tests",
		issue: github.Issue{Number: 12, Title: "Fix", URL: "https://x/12", Body: "b"}})
	assert.Equal(t, 12, inst.IssueNumber())
	assert.Contains(t, inst.Prompt, "# Fix")
	assert.Contains(t, inst.Prompt, "\n\nand tidy tests")
	assert.Equal(t, stateLaunchOptions, m.state)
}

func TestIssueExpandedMsg_FailureLaunchesLiteral(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	promptOverlayForNewInstance(t, m)
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	m.dismissOverlay()
	m.state = stateDefault
	_, cmd := m.Update(issueExpandedMsg{instance: inst, number: 12, literal: "#12 and tidy tests", err: errors.New("nope")})
	assert.Equal(t, 0, inst.IssueNumber())
	assert.Equal(t, "#12 and tidy tests", inst.Prompt)
	assert.Equal(t, stateLaunchOptions, m.state, "a bad number never blocks the session")
	assert.NotNil(t, cmd, "the footer error still surfaces")
}
```

`TextInputOverlay` has `GetValue`/`IsSubmitted` but check whether it has `SetValue` and a way to mark submitted (`grep -n 'func (t \*TextInputOverlay)' ui/overlay/textInput.go`). If not, add minimal test-only helpers in `ui/overlay/textInput.go`:

```go
// SetValue replaces the input text (tests and programmatic prefill).
func (t *TextInputOverlay) SetValue(s string) { t.textarea.SetValue(s) }

// MarkSubmitted flags the overlay as submitted without a key press.
func (t *TextInputOverlay) MarkSubmitted() { t.submitted = true }
```

adjusting field names to the real ones in that file.

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestPromptShorthand|TestIssueExpandedMsg' -v`
Expected: FAIL — undefined `issueExpandedMsg`.

- [ ] **Step 3: Add the message and handler to `app/state_issue_picker.go`**

```go
// issueExpandedMsg is the #n shorthand's result: on success the seeded
// prompt replaces the token; on error the literal prompt launches
// unchanged and unlinked.
type issueExpandedMsg struct {
	instance       *session.Instance
	number         int
	issue          github.Issue
	rest           string // user text after the #n token
	literal        string // original prompt, used when err != nil
	selectedBranch string
	err            error
}

// issueExpandCmd fetches issue n for the shorthand.
func issueExpandCmd(repo string, n int, inst *session.Instance, rest, literal, selectedBranch string) tea.Cmd {
	return func() tea.Msg {
		is, err := github.View(context.Background(), repo, n, internalexec.Default{})
		return issueExpandedMsg{instance: inst, number: n, issue: is, rest: rest, literal: literal, selectedBranch: selectedBranch, err: err}
	}
}

// handleIssueExpanded finishes the N flow after a #n expansion.
func (m *home) handleIssueExpanded(msg issueExpandedMsg) (tea.Model, tea.Cmd) {
	inst := msg.instance
	if inst == nil || inst.Started() {
		return m, nil
	}
	var errCmd tea.Cmd
	if msg.err != nil {
		inst.Prompt = msg.literal
		errCmd = m.handleError(fmt.Errorf("issue #%d not expanded: %w", msg.number, msg.err))
	} else {
		inst.Prompt = github.SeedPrompt(msg.issue)
		if msg.rest != "" {
			inst.Prompt += "\n" + msg.rest + "\n"
		}
		inst.SetIssue(msg.issue.Number)
		m.lastGHQuery = time.Time{}
		m.applyGitHubState()
	}
	_, cmd := m.openLaunchOptionsForNew(inst, msg.selectedBranch)
	return m, tea.Batch(cmd, errCmd)
}
```

`app/app.go` message switch: `case issueExpandedMsg: return m.handleIssueExpanded(msg)`.

- [ ] **Step 4: Hook the shorthand into `handleStatePromptKey`**

In `app/state_prompt.go`, inside `if !selected.Started() {`, after `selected.Prompt = prompt` and before `m.pendingLaunchOptions = ...`, insert:

```go
				if n, rest, ok := github.ParseShorthand(prompt); ok && !(m.ghAvailable.checked && !m.ghAvailable.ok) {
					m.dismissOverlay()
					m.state = stateDefault
					return m, issueExpandCmd(m.repoPath(), n, selected, rest, prompt, selectedBranch)
				}
```

Import `"github.com/aidan-bailey/loom/session/github"`.

- [ ] **Step 5: Run tests**

Run: `CGO_ENABLED=0 go test ./app/ ./ui/overlay/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w app/*.go ui/overlay/textInput.go && go vet ./app/ ./ui/overlay/
git add app/ ui/overlay/
git commit -m "feat(app): expand a leading #n in the new-session prompt into the issue"
```

---

### Task 14: Documentation

**Files:**
- Modify: `CLAUDE.md` (keybinding table, Key Packages, Gotchas, Persistent State)
- Modify: `USAGE.md` (keybinding table if it has one; `grep -n '| `m` |' USAGE.md`)

- [ ] **Step 1: Keybinding row** — after the `N` row in the CLAUDE.md table:

```
| `I` | New instance from a GitHub issue (picker; needs `gh` auth). In the `N` prompt, a leading `#123` expands to that issue |
```

- [ ] **Step 2: Key Packages** — add after `session/files/`:

```
- **`session/github/`** — Reads GitHub state through the `gh` CLI: `Query` (PRs by head branch + open issues, one snapshot per repo), `View` (one issue with body), `CheckCLI`, `StateFor` (pure per-session join), `SeedPrompt`/`SlugTitle`/`ParseShorthand`. No app, ui or session imports; injected executor. `app/github.go` drives it.
```

- [ ] **Step 3: Gotcha** — add to the Gotchas list:

```
- **The GitHub poller is roster-shaped and fails closed.** `maybeGHQuery` (`app/github.go`) rides the health tick on its own 60s cadence with the same `lastGHQuery`/`ghInFlight` pair rules as the roster: `handleGHReady` must clear `ghInFlight` on every delivery, and `maybeGHQuery` arms nothing when it dispatches nothing. Each poll resolves + fetches the base ref per open repo (`git.FetchRef`, even without `gh`) and, when `gh` is available, runs `github.Query`. `ghState` is replaced wholesale — an errored repo is dropped, so its cards render `Known=false` (nothing) rather than stale badges; `Known=true, HasPR=false` is a real "no PR". Results are joined onto `Instance.SetGitHubState` (transient; `ui.BuildCardData` copies it); ahead/behind (`Instance.UpdateParity`, one local `rev-list`) rides the 3s metadata fan-out using `home.ghBases`. Zero `lastGHQuery` (or return `ghRefreshMsg`) to force the next tick to poll — push, issue-born sessions and workspace activation do. GitHub state never sets `NeedsAttention`.
```

- [ ] **Step 4: Persistent State** — in the `instances.json` bullet mention `issue` (schema v6). Update the schema gotcha's example version if it names one.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md USAGE.md
git commit -m "docs: GitHub issue integration keybinding, package and poller gotcha"
```

---

### Task 15: Full verification

- [ ] **Step 1: Whole-tree tests**

Run: `CGO_ENABLED=0 go test ./...`
Expected: PASS.

- [ ] **Step 2: Race detector on the touched packages**

Run: `CC=clang CGO_ENABLED=1 go test -race ./app/ ./session/... ./ui/...`
Expected: PASS, no `DATA RACE`.

- [ ] **Step 3: vet + format check**

Run: `go vet ./... && gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')`
Expected: vet clean; gofmt prints nothing.

- [ ] **Step 4: Manual smoke in the dev sandbox** (see `.claude/skills/loom-dev`)

```bash
go run ./tools/loomdev up && go run ./tools/loomdev start
go run ./tools/loomdev keys I      # picker opens (loading… or "gh unavailable" in the toy repo)
go run ./tools/loomdev shot
go run ./tools/loomdev keys esc
go run ./tools/loomdev down
```

Expected: picker overlay renders and dismisses; with `gh` absent from PATH (`PATH=/usr/bin:/bin` when starting) the rail and overview look identical to a build from `main`.

- [ ] **Step 5: Final commit if anything changed**

```bash
git status --short
```

If files changed in this task, commit them as `chore: verification fixes for GitHub issue integration`.
