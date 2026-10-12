# Session Subcommands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans. The plan has four coarse packages (A–D). Each package gets one implementer, one review
> and one commit; the numbered steps inside it are checkpoints, not commits.

**Goal:** `loom session new|list|kill|pause|resume`, clients of the daemon that create and drive sessions in a
registered workspace from a shell.

**Spec:** `docs/superpowers/specs/2026-10-12-session-cli-design.md` (11f3ecf).

**Architecture:** the commands live in `internal/cli/session.go` and use the daemon's existing requests (`Create`,
`Kill`, `Pause`, `Resume`); nothing crosses the wire that doesn't today. A new session's checks and its launch
account move out of `app` into `core/new_session.go`, so the TUI's flows and `loom session new` share one source.

**Tech stack:** Go 1.25, Cobra, `core/rpc`, tmux, git.

---

## Decisions made while planning

Facts found in the code that refine the spec:

1. **The account helper takes options, not settings.** The spec's `NewSessionLaunch(c, settings, program)` becomes
   `core.ChooseLaunchAccount(c Core, opts launch.Options, program string) (launch.Options, string)`: the Launch Options
   modal serves `R` (restart with options) too, whose options come from the instance, not the settings. The CLI calls
   it with `launch.FromSettings(ws.Settings)`.
2. **More of the creation gates move to `core`.** The TUI also refuses a new session past 10 rows
   (`GlobalInstanceLimit`) and while the workspace's storage refuses writes (`latchedStorageErr`); the CLI must too. So
   `core/new_session.go` holds `InstanceLimit`, `MaxTitleWidth`, `CheckRoom`, `CheckWritable`, `CheckNewTitle`,
   `CheckPreservedTitle` and `ChooseLaunchAccount`; `app` calls them, with its texts unchanged.
3. **The TUI's Enter on a title now checks its width too.** Typing stops at 32 cells, but a paste can push past it;
   `CheckNewTitle` refuses that at Enter, as the CLI does.
4. **The model refuses to pause a Paused or Recoverable session.** `admit`'s `opPause` precondition lets both through
   today (so does the TUI's `s` gate, `selectedNotBusyNotWorkspace`): the job moves the instance to Loading, and
   `Instance.Pause` then runs its steps on a session with no worktree. `loom session pause` would expose that to every
   shell. The refusal reads `pause <t>: the session is not running (Paused)`; the TUI shows it through `opReq`.
5. **`loom session new` waits in one loop for its `Reply`, `Started` and notices** (`client.awaitStart`): a refused
   Create is the Reply's `Err`; a failed start is one or two `Notice{Req, Err}` (`deliverStart` notifies a save error,
   then the start's); a success is `Started{Req}` after the initial prompt (`deliverPromptSent`). Waiting for the Reply
   first could drain a later batch's notice into `awaitReply` and lose it.
6. **The live tests run the daemon in process on a private real tmux server** with a fake `claude` that answers
   `auth status`, exits 1 on any other subcommand (the roster's `agents`, the usage probe's `-p`) and sleeps when
   launched with flags or none.
7. **The session command tree is built by `newSessionCmd()`**, and `SessionCmd = newSessionCmd()` is what `main`
   attaches: tests build a fresh tree per run, so no flag value leaks between tests (`runWorkspace` resets
   `workspaceName` by hand today).

## Files

| File | Package | Change |
|---|---|---|
| `core/new_session.go` | A | New: the checks and `ChooseLaunchAccount`. |
| `core/new_session_test.go` | A | New. |
| `core/requests.go` | A | `opPause` refuses Paused and Recoverable. |
| `core/requests_test.go` | A | Two refusal cases. |
| `app/accounts.go` | A | `newLaunchOptionsOverlay` calls `ChooseLaunchAccount`; `removedAccountNotice` moves to core. |
| `app/state_new.go` | A | Enter calls `CheckNewTitle`; the width cap uses `MaxTitleWidth`; `preservedTitleErr`, `latchedStorageErr` delegate. |
| `app/intents.go`, `app/state_issue_picker.go` | A | The limit checks call `CheckRoom`. |
| `app/app.go` | A | `GlobalInstanceLimit = core.InstanceLimit`. |
| `internal/cli/join.go` | B | `client.ctx`; `events` returns this command's notices; `await` ends on the context. |
| `internal/cli/workspace.go` | B | `resolveWorkspace`, used by `workspace status`. |
| `internal/cli/session.go` | B, C | New: `SessionCmd`, `list` (B); `new`, `kill`, `pause`, `resume` (C). |
| `internal/cli/session_test.go` | B, C | New. |
| `internal/cli/daemon_test.go` | C | `serveDaemonOn` takes any executor; `serveDaemonLive`, `fakeAgent`, `liveWorkspace`. |
| `main.go` | B | `rootCmd.AddCommand(cli.SessionCmd)`. |
| `e2e/clients_test.go` | D | `TestE2E_Clients_SessionLifecycle`. |
| `USAGE.md`, `internal/cli/README.md`, `internal/cli/CLAUDE.md`, `core/README.md`, `e2e/CLAUDE.md`, `docs/ARCHITECTURE.md` (if it lists the subcommands) | D | Docs. |

Rules every implementer keeps (restated from the CLAUDE.md files):

- Scratch files go in the session scratchpad, never the worktree. Commit with explicit `-- <paths>`, never `-a`,
  `git add -A` or `.`.
- Never run loom itself (`./loom`, `go run .`) or `loom serve stop` in a loom pane.
- No test touches the real `~/.loom`, `~/.claude` or the user's tmux server: `internal/cli`'s `TestMain` isolates the
  dirs, `serveDaemon` refuses an unisolated `CLAUDE_CONFIG_DIR`, and live tests run on a private tmux server.
- Invoke tmux only through `tmux.Command`/`tmux.CommandOnSocket` with exact targets (`tmux.SessionTarget`,
  `tmux.PaneTarget`); git through `internalexec.GitCommand`, except in `internal/cli`'s tests, which already use
  `exec.Command("git", …)` for fixtures (`gitRepo` in `workspace_test.go`); keep to that file's pattern.
- Tests: `CGO_ENABLED=0 go test ./…`; format with `gofmt -l` on changed files; `CGO_ENABLED=0 go vet ./...`.

---

## Package A: one source for a new session's checks and launch account

**Commit:** `refactor(core,app): one source for a new session's checks and launch account`

### A1. Write the failing core tests

Create `core/new_session_test.go`:

```go
package core

import (
	"slices"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckRoom_RefusesAtTheLimit(t *testing.T) {
	assert.NoError(t, CheckRoom(InstanceLimit-1))
	err := CheckRoom(InstanceLimit)
	require.Error(t, err)
	assert.Equal(t, "you can't create more than 10 instances", err.Error())
}

func TestCheckWritable_RefusesALatchedWorkspace(t *testing.T) {
	assert.NoError(t, CheckWritable(WorkspaceView{}))
	err := CheckWritable(WorkspaceView{WritesRefused: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this workspace's saved sessions could not be read")
}

func TestCheckNewTitle(t *testing.T) {
	ws := WorkspaceView{PreservedTitles: []string{"future"}}
	for _, tc := range []struct {
		name, title, want string
	}{
		{"a free title", "alpha", ""},
		{"exactly the widest", strings.Repeat("a", MaxTitleWidth), ""},
		{"empty", "", "title cannot be empty"},
		{"one cell too wide", strings.Repeat("a", MaxTitleWidth+1), "title cannot be longer than 32 characters"},
		{"wide runes count as cells", strings.Repeat("界", 17), "title cannot be longer than 32 characters"},
		{"preserved", "future", `title "future" belongs to a saved session this version of loom could not load; choose another`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckNewTitle(tc.title, ws)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

// accountsCore answers the account queries ChooseLaunchAccount reads; any
// other Core method panics (the embedded interface is nil).
type accountsCore struct {
	Core
	names  AccountNames
	loaded bool
	known  []string
}

func (c accountsCore) AccountNames() AccountNames { return c.names }
func (c accountsCore) AccountsLoaded() bool        { return c.loaded }
func (c accountsCore) Account(name string) (account.Account, bool) {
	return account.Account{Name: name}, slices.Contains(c.known, name)
}

func TestChooseLaunchAccount(t *testing.T) {
	present := AccountNames{Present: true, Default: "work", Names: []string{"work", account.DefaultName}}
	for _, tc := range []struct {
		name        string
		c           accountsCore
		program     string
		account     string
		want        string
		wantNotice  string
	}{
		{"a non-Claude program records no account", accountsCore{names: present, loaded: true, known: []string{"work"}}, "aider", "work", "", ""},
		{"no account registry records none", accountsCore{}, "claude", "work", "", ""},
		{"none chosen takes the registry's default", accountsCore{names: present, loaded: true}, "claude", "", "work", ""},
		{"the default name is kept", accountsCore{names: present, loaded: true}, "claude", account.DefaultName, account.DefaultName, ""},
		{"a registry that failed to load keeps the choice", accountsCore{names: present}, "claude", "gone", "gone", ""},
		{"a registered account is kept", accountsCore{names: present, loaded: true, known: []string{"work"}}, "claude", "work", "work", ""},
		{"a removed account moves to the default, saying so", accountsCore{names: present, loaded: true}, "claude", "gone", "work", "gone was removed — this session will run on work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, notice := ChooseLaunchAccount(tc.c, launch.Options{Account: tc.account, Model: "opus"}, tc.program)
			assert.Equal(t, tc.want, opts.Account)
			assert.Equal(t, "opus", opts.Model, "the other options pass through")
			assert.Equal(t, tc.wantNotice, notice)
		})
	}
}
```

Add two cases to the table in `TestRequests_RefuseWhatTheTUIRefuses` (`core/requests_test.go`), after
`"pause a loading session"`:

```go
		{"pause a paused session", func(f fixture) { f.m.Pause(f.id["paused"], 1) }, "pause paused: the session is not running (Paused)"},
		{"pause a recoverable session", func(f fixture) { f.m.Pause(f.id["orphan"], 1) }, "pause orphan: the session is not running (Recoverable)"},
```

Run: `CGO_ENABLED=0 go test ./core -run 'TestCheckRoom|TestCheckWritable|TestCheckNewTitle|TestChooseLaunchAccount|TestRequests_RefuseWhatTheTUIRefuses'`
Expected: build failure (`undefined: CheckRoom` …).

### A2. Write `core/new_session.go`

```go
package core

import (
	"errors"
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
	runewidth "github.com/mattn/go-runewidth"
)

// A new session's rules, for every client that creates one: the TUI's
// creation flows and `loom session new` check a workspace with these
// before they send a Create, and launch on the account
// ChooseLaunchAccount picks. One source keeps the two from drifting.

// InstanceLimit caps the rows a workspace holds (its terminal and
// recoverable rows count) before a client creates another. It is a soft
// guardrail: tmux server memory and git worktree overhead are the real
// bound, so raise it deliberately.
const InstanceLimit = 10

// MaxTitleWidth is the widest title, in terminal cells, a new session
// takes.
const MaxTitleWidth = 32

// CheckRoom refuses a new session in a workspace that already holds rows
// rows (InstanceLimit).
func CheckRoom(rows int) error {
	if rows >= InstanceLimit {
		return fmt.Errorf("you can't create more than %d instances", InstanceLimit)
	}
	return nil
}

// CheckWritable refuses a new session in ws while its storage refuses
// writes (WorkspaceView.WritesRefused): the session could never be saved.
func CheckWritable(ws WorkspaceView) error {
	if ws.WritesRefused {
		return errors.New("new sessions can't be created here: this workspace's saved sessions could not be read, so nothing can be saved (see serve.log)")
	}
	return nil
}

// CheckNewTitle refuses a title a new session in ws can't take: empty,
// wider than MaxTitleWidth cells, or preserved (CheckPreservedTitle).
func CheckNewTitle(title string, ws WorkspaceView) error {
	switch {
	case title == "":
		return errors.New("title cannot be empty")
	case runewidth.StringWidth(title) > MaxTitleWidth:
		return fmt.Errorf("title cannot be longer than %d characters", MaxTitleWidth)
	}
	return CheckPreservedTitle(title, ws)
}

// CheckPreservedTitle refuses a title that belongs to a record ws's
// storage preserves on disk but could not load (a reconcile failure, or a
// newer loom's record after a downgrade). Storage never dedupes against
// such records, so creating the session would persist a second record
// under the title, and the two would share a tmux session name.
func CheckPreservedTitle(title string, ws WorkspaceView) error {
	if slices.Contains(ws.PreservedTitles, title) {
		return fmt.Errorf("title %q belongs to a saved session this version of loom could not load; choose another", title)
	}
	return nil
}

// ChooseLaunchAccount is opts with the account a launch of program runs
// on, as the Session Launch Options modal pre-fills it, and the notice to
// show when it moved the launch off a removed account ("" otherwise). The
// caller reloads the accounts first (Core.ReloadAccounts).
//
// A Claude launch with no account chosen, or with one no longer
// registered, takes the registry's default, so R on a session whose
// account was removed can't relaunch as it again; the notice names the
// switch (removedAccountNotice). A registry that failed to load keeps a
// named account: it lists no accounts, and rewriting the account would
// move the session to a subscription the user never saw chosen; kept, its
// launch fails closed (session.RegistryLoadError). Any other program, or
// a model with no account registry, records no account.
func ChooseLaunchAccount(c Core, opts launch.Options, program string) (launch.Options, string) {
	names := c.AccountNames()
	switch {
	case !session.IsClaudeProgram(program) || !names.Present:
		opts.Account = ""
	case opts.Account == "":
		opts.Account = names.Default
	case opts.Account == account.DefaultName || !c.AccountsLoaded():
		// Kept: the default, or a registry that can't tell whether the
		// account still exists.
	default:
		if _, ok := c.Account(opts.Account); !ok {
			removed := opts.Account
			opts.Account = names.Default
			return opts, removedAccountNotice(removed, opts.Account)
		}
	}
	return opts, ""
}

// removedAccountNotice says a launch whose account was removed runs on
// another one, which the user sees before confirming.
func removedAccountNotice(removed, now string) string {
	return fmt.Sprintf("%s was removed — this session will run on %s", removed, now)
}
```

### A3. Refuse a pause of a session that isn't running

In `core/requests.go`, the `opKill, opPause, opPush, opMergeInto, opMergeFrom` case of `precondition`, add a case
after the busy one:

```go
		case op == opPause && (st == session.Paused || st == session.Recoverable):
			why = fmt.Sprintf("the session is not running (%s)", st)
```

Run: `CGO_ENABLED=0 go test ./core -run 'TestCheckRoom|TestCheckWritable|TestCheckNewTitle|TestChooseLaunchAccount|TestRequests_RefuseWhatTheTUIRefuses|TestRequests_RefusalsMatchErrRefused'`
Expected: PASS. If the `orphan` fixture of `TestRequests_RefuseWhatTheTUIRefuses` is not Recoverable, read the
fixture's `insts` map and use the key it gives a Recoverable instance.

### A4. Point the TUI at the core helpers

`app/app.go`, replacing the constant and its doc:

```go
// GlobalInstanceLimit is the rows a workspace slot holds before the
// creation flows refuse another (core.InstanceLimit, checked through
// core.CheckRoom).
const GlobalInstanceLimit = core.InstanceLimit
```

`app/intents.go`: in `runPromptNewInstance`, `runNewInstance` and `runNewFromIssue`, replace each

```go
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(
			fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}
```

(and the one-line form in `runNewFromIssue`) with

```go
	if err := core.CheckRoom(m.list.NumInstances()); err != nil {
		return m, m.handleError(err)
	}
```

Do the same at both limit checks in `app/state_issue_picker.go` (lines 68 and 137). Add the `core` import where a
file lacks it; drop `fmt` only if the compiler says it is unused.

`app/state_new.go`:

- In the `tea.KeyEnter` case, replace the empty check and the `preservedTitleErr` call with

  ```go
  		if err := core.CheckNewTitle(d.title, m.info); err != nil {
  			return m, m.handleError(err)
  		}
  ```

- In the `default` case, use the core width:

  ```go
  		if runewidth.StringWidth(d.title) >= core.MaxTitleWidth {
  			return m, m.handleError(fmt.Errorf("title cannot be longer than %d characters", core.MaxTitleWidth))
  		}
  ```

- Keep `preservedTitleErr` and `latchedStorageErr` (the issue flow and the key gates call them) and their doc
  comments' first lines, with bodies

  ```go
  	return core.CheckPreservedTitle(title, m.info)
  ```

  and

  ```go
  	return core.CheckWritable(m.info)
  ```

  Drop the `slices` import.

`app/accounts.go`: delete `removedAccountNotice`, and in `newLaunchOptionsOverlay` replace the `notice := ""` line and
the whole `switch` with

```go
	opts, notice := core.ChooseLaunchAccount(m.core, opts, program)
```

keeping `claude := session.IsClaudeProgram(program)` (the Account row still needs it). Trim the function's doc comment
to what it still does (builds the modal, reloads first, shows the Account row for a Claude launch when an extra account
exists or a notice is due) and point at `core.ChooseLaunchAccount` for the account rules.

### A5. Verify and commit

Run:

```bash
CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./core/... ./app/... ./script/...
gofmt -l core/new_session.go core/new_session_test.go core/requests.go core/requests_test.go app/app.go app/intents.go app/state_issue_picker.go app/state_new.go app/accounts.go
CGO_ENABLED=0 go vet ./core/... ./app/...
```

Expected: PASS, no gofmt output. App tests that pin the old texts (`title cannot be empty`, the limit, the latch,
`was removed — this session will run on`) pass unchanged, since the texts are identical.

```bash
git add -- core/new_session.go core/new_session_test.go core/requests.go core/requests_test.go app/app.go app/intents.go app/state_issue_picker.go app/state_new.go app/accounts.go
git commit -m "refactor(core,app): one source for a new session's checks and launch account" -- core/new_session.go core/new_session_test.go core/requests.go core/requests_test.go app/app.go app/intents.go app/state_issue_picker.go app/state_new.go app/accounts.go
```

(End the message with the session's attribution trailers.)

---

## Package B: `loom session list`, and the plumbing the other commands need

**Commit:** `feat(cli): loom session list`

### B1. Write the failing tests

Create `internal/cli/session_test.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSession runs `loom session args…` with stdin, on a fresh command tree
// (newSessionCmd), so no flag carries over from another test.
func runSession(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newSessionCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// pausedRecords are two Paused sessions, alpha and beta, as state.json
// holds them.
const pausedRecords = `[{"title":"alpha","status":3,"program":"true","branch":"me/alpha","worktree":{"worktree_path":"/tmp/wt-alpha"}},` +
	`{"title":"beta","status":3,"program":"true","worktree":{"worktree_path":"/tmp/wt-beta"}}]`

func TestSessionList_ShowsTheWorkspacesSessions(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	registered(t, "ws-b", `[{"title":"other","status":3,"program":"true"}]`)
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a")

	require.NoError(t, err)
	assert.Regexp(t, `TITLE\s+STATUS\s+BRANCH\s+ACCOUNT\s+SINCE`, out)
	assert.Regexp(t, `alpha\s+Paused\s+me/alpha`, out)
	assert.Regexp(t, `beta\s+Paused\s+-`, out)
	assert.NotContains(t, out, "other", "another workspace's session")
}

func TestSessionList_TheWorkingDirectoryPicksTheWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	ws := registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")
	t.Chdir(ws.Path)

	out, _, err := runSession(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "alpha")
}

func TestSessionList_RefusesADirectoryNoWorkspaceHolds(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")
	t.Chdir(t.TempDir())

	_, _, err := runSession(t, "", "list")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workspace matches current directory")
}

func TestSessionList_RefusesAnUnknownWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	serveDaemon(t, "true")

	_, _, err := runSession(t, "", "list", "-w", "nope")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `workspace "nope" not found`)
}

func TestSessionList_SaysWhenThereAreNone(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, "No sessions in ws-a")
}

// The JSON's field names are a contract (scrum's board reads them).
func TestSessionList_JSONFieldNames(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a", "--json")

	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &rows))
	require.Len(t, rows, 2)
	var keys []string
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{"account", "branch", "id", "issue", "pr", "program", "repo", "status", "status_since",
		"title", "wait_reason", "worktree"}, keys)
	assert.Equal(t, "alpha", rows[0]["title"])
	assert.Equal(t, "Paused", rows[0]["status"])
	assert.Equal(t, "me/alpha", rows[0]["branch"])
	assert.NotZero(t, rows[0]["id"])
}

func TestSessionList_JSONOfNoSessionsIsAnEmptyArray(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a", "--json")

	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestFindSession(t *testing.T) {
	views := []core.InstanceView{
		{ID: 1, Title: "ws-a", IsWorkspaceTerminal: true},
		{ID: 2, Title: "alpha"},
		{ID: 3, Title: "twin"},
		{ID: 4, Title: "twin"},
	}
	for _, tc := range []struct {
		name   string
		title  string
		id     core.InstanceID
		want   core.InstanceID
		errHas string
	}{
		{"an exact title", "alpha", 0, 2, ""},
		{"never a prefix", "alp", 0, 0, `no session "alp"`},
		{"never the workspace terminal", "ws-a", 0, 0, `no session "ws-a"`},
		{"a shared title is refused", "twin", 0, 0, "2 sessions are titled \"twin\": name one with --id"},
		{"--id picks among them", "twin", 4, 4, ""},
		{"--id must match the title", "alpha", 4, 0, `no session "alpha" with id 4`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findSession(views, tc.title, tc.id)
			if tc.errHas != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errHas)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.ID)
		})
	}
}

func TestSince(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "-", since(time.Time{}, now))
	assert.Equal(t, "<1m", since(now.Add(-30*time.Second), now))
	assert.Equal(t, "5m", since(now.Add(-5*time.Minute), now))
	assert.Equal(t, "3h", since(now.Add(-3*time.Hour), now))
	assert.Equal(t, "2d", since(now.Add(-50*time.Hour), now))
}
```

Run: `CGO_ENABLED=0 go test ./internal/cli -run 'TestSessionList|TestFindSession|TestSince'`
Expected: build failure (`undefined: newSessionCmd` …).

### B2. Let a wait end on the command's context, and hand the command its own notices

In `internal/cli/join.go`:

- Import `context`. Give `client` a field after `errOut`:

  ```go
  	// ctx ends a wait (await) early: the command's context, which
  	// `session new` cancels on an interrupt.
  	ctx context.Context
  ```

- In `withDaemon`, build the client with it:

  ```go
  	ctx := cmd.Context()
  	if ctx == nil {
  		ctx = context.Background()
  	}
  	c := &client{Client: rc, errOut: cmd.ErrOrStderr(), ctx: ctx}
  ```

- In `events`, return a notice that names a request instead of dropping it, and say so in its doc comment ("A Notice
  naming a request (Req) is one of this command's own, which the daemon routes to it alone: it is returned, not
  written, so the command reports it with its request's outcome."):

  ```go
  		case n.Req != 0:
  			out = append(out, ev)
  ```

- In `await`, check the context before waiting and wait on it too; add "or the command's context ends (its error)" to
  the doc comment:

  ```go
  		if err := c.Err(); err != nil {
  			return false, fmt.Errorf("lost the loom daemon: %w", err)
  		}
  		if err := c.ctx.Err(); err != nil {
  			return false, err
  		}
  		select {
  		case <-c.Wakes():
  		case <-expired:
  			return false, nil
  		case <-c.ctx.Done():
  			return false, c.ctx.Err()
  		}
  ```

`awaitReply` ignores notices, so `Reset` and the account commands are unchanged (`TestReset_AFailureIsReportedOnce`
still passes: the notice is still never written).

### B3. Extract `resolveWorkspace`

In `internal/cli/workspace.go`, add beside `served`:

```go
// resolveWorkspace is the registered workspace a command acts on, name's
// or, with name "", the one whose repository holds the working directory,
// and the view the daemon serves it as. It rereads the registry first
// (ReloadRegistry): another process may have changed it.
func (c *client) resolveWorkspace(name string) (config.Workspace, core.WorkspaceView, error) {
	if err := c.ReloadRegistry(); err != nil {
		return config.Workspace{}, core.WorkspaceView{}, err
	}
	reg := &config.WorkspaceRegistry{Workspaces: c.Registry().Workspaces}
	var ws *config.Workspace
	if name != "" {
		if ws = reg.Get(name); ws == nil {
			return config.Workspace{}, core.WorkspaceView{}, fmt.Errorf("workspace %q not found", name)
		}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return config.Workspace{}, core.WorkspaceView{}, fmt.Errorf("failed to get working directory: %w", err)
		}
		if ws = reg.FindByPath(cwd); ws == nil {
			return config.Workspace{}, core.WorkspaceView{}, errors.New("no workspace matches current directory; specify a name")
		}
	}
	v, ok := c.served(ws.Name)
	if !ok {
		return config.Workspace{}, core.WorkspaceView{}, fmt.Errorf("workspace %q is not served by the loom daemon", ws.Name)
	}
	return *ws, v, nil
}
```

and make `workspaceStatusCmd`'s body use it:

```go
		return withDaemon(c, func(d *client) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			ws, v, err := d.resolveWorkspace(name)
			if err != nil {
				return err
			}
			// The daemon's sessions, as state.json keeps them: a
			// Recoverable entry is a worktree found on disk, never stored.
			var titles []string
			for _, iv := range d.Views(v.ID) {
```

(the rest of the body is unchanged). Add the `errors` import.

### B4. Write `internal/cli/session.go` with `list`

```go
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/spf13/cobra"
)

// SessionCmd is `loom session`: create, list and drive the sessions of a
// registered workspace, as clients of the daemon. main attaches it.
var SessionCmd = newSessionCmd()

// newSessionCmd builds the `loom session` command tree, with flags of its
// own: each call is independent (tests build one per run).
func newSessionCmd() *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Create, list and drive a workspace's sessions",
	}
	cmd.PersistentFlags().StringVarP(&workspace, "workspace", "w", "",
		"Registered workspace (default: the one whose repository holds the current directory)")
	cmd.AddCommand(newSessionListCmd(&workspace))
	return cmd
}

func newSessionListCmd(workspace *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a workspace's sessions",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return withDaemon(c, func(d *client) error {
				_, v, err := d.resolveWorkspace(*workspace)
				if err != nil {
					return err
				}
				rows := sessions(d.Views(v.ID))
				if asJSON {
					return writeSessionsJSON(c.OutOrStdout(), rows)
				}
				return writeSessionsTable(c.OutOrStdout(), v.Label, rows, time.Now())
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the sessions as a JSON array")
	return cmd
}

// sessions are the rows a session command shows and names: every row but
// the workspace terminal, Recoverable ones included.
func sessions(views []core.InstanceView) []core.InstanceView {
	out := make([]core.InstanceView, 0, len(views))
	for _, v := range views {
		if !v.IsWorkspaceTerminal {
			out = append(out, v)
		}
	}
	return out
}

// findSession is the session titled title among views, by exact title,
// never a prefix, and never the workspace terminal. Several sessions under
// one title are refused unless id names one; an id must still match the
// title, so a stale one can't land on another session.
func findSession(views []core.InstanceView, title string, id core.InstanceID) (core.InstanceView, error) {
	var found []core.InstanceView
	for _, v := range sessions(views) {
		if v.Title == title && (id == 0 || v.ID == id) {
			found = append(found, v)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1:
		return core.InstanceView{}, fmt.Errorf("%d sessions are titled %q: name one with --id (see loom session list --json)", len(found), title)
	case id != 0:
		return core.InstanceView{}, fmt.Errorf("no session %q with id %d", title, id)
	default:
		return core.InstanceView{}, fmt.Errorf("no session %q", title)
	}
}

// sessionJSON is a session as `loom session list --json` prints it. The
// field names are a contract that scripts and scrum's board read:
// TestSessionList_JSONFieldNames pins them.
type sessionJSON struct {
	ID          core.InstanceID `json:"id"`
	Title       string          `json:"title"`
	Status      string          `json:"status"`
	WaitReason  string          `json:"wait_reason"`
	StatusSince time.Time       `json:"status_since"`
	Branch      string          `json:"branch"`
	Worktree    string          `json:"worktree"`
	Repo        string          `json:"repo"`
	Program     string          `json:"program"`
	Account     string          `json:"account"`
	Issue       int             `json:"issue"`
	PR          int             `json:"pr"`
}

func writeSessionsJSON(w io.Writer, rows []core.InstanceView) error {
	out := make([]sessionJSON, 0, len(rows))
	for _, v := range rows {
		pr := 0
		if v.GitHub.HasPR {
			pr = v.GitHub.PRNumber
		}
		out = append(out, sessionJSON{
			ID: v.ID, Title: v.Title, Status: v.Status.String(), WaitReason: v.WaitReason,
			StatusSince: v.StatusSince, Branch: v.Branch, Worktree: v.WorktreePath, Repo: v.RepoPath,
			Program: v.Program, Account: v.Account, Issue: v.Issue, PR: pr,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeSessionsTable(w io.Writer, label string, rows []core.InstanceView, now time.Time) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "No sessions in %s. Use 'loom session new' to create one.\n", label)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TITLE\tSTATUS\tBRANCH\tACCOUNT\tSINCE")
	for _, v := range rows {
		status := v.Status.String()
		if v.WaitReason != "" {
			status += " (" + v.WaitReason + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", v.Title, status, orDash(v.Branch), orDash(v.Account), since(v.StatusSince, now))
	}
	return tw.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// since is how long before now t was, coarsely ("-" for a zero t).
func since(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
```

In `main.go`, after `rootCmd.AddCommand(cli.AccountCmd)`:

```go
	rootCmd.AddCommand(cli.SessionCmd)
```

### B5. Verify and commit

```bash
CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./internal/cli/... .
gofmt -l internal/cli/join.go internal/cli/workspace.go internal/cli/session.go internal/cli/session_test.go main.go
CGO_ENABLED=0 go vet ./internal/cli/...
```

Expected: PASS (the workspace and account tests included), no gofmt output.

```bash
git commit -m "feat(cli): loom session list" -- internal/cli/join.go internal/cli/workspace.go internal/cli/session.go internal/cli/session_test.go main.go
```

(`git add --` the new files first.)

---

## Package C: `loom session new`, `kill`, `pause` and `resume`

**Commit:** `feat(cli): loom session new, kill, pause and resume`

### C1. Give the tests a daemon that starts sessions

In `internal/cli/daemon_test.go`:

- `serveDaemonOn` takes any executor: change its parameter to `ex cmd.Executor` (import
  `github.com/aidan-bailey/loom/cmd`), pass `CmdExec: ex`, and update its doc ("with ex as the daemon's executor").
  `serveDaemon` and `reset_test.go` keep passing `deadTmux{…}` values.
- Add:

```go
// serveDaemonLive is serveDaemon on a private real tmux server, killed
// once the daemon has stopped, so sessions really start: their agent is
// whatever program the workspace's config names (liveWorkspace).
func serveDaemonLive(t *testing.T, program string) *testDaemon {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	var sock string
	// Registered first so it runs last: after the loop has stopped.
	t.Cleanup(func() {
		if sock != "" {
			_ = tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run()
		}
	})
	d := serveDaemonOn(t, program, internalexec.Default{})
	sock = os.Getenv(tmux.EnvTmuxSocket)
	return d
}

// fakeAgent is a stand-in Claude, named claude so loom launches it as
// one: `auth status` reports a login (by authMethod, "claude.ai" for one
// remote control accepts), any other subcommand fails at once (the
// roster's `agents`, a usage probe's -p), and a launch, with flags or
// none, stays up until its tmux session ends.
func fakeAgent(t *testing.T, authMethod string) string {
	t.Helper()
	bin := t.TempDir()
	auth := `{"loggedIn":true,"authMethod":"` + authMethod + `","email":"you@example.com","subscriptionType":"max"}`
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  auth) printf '%s\\n' '" + auth + "'; exit 0;;\n" +
		"  -p) exit 1;;\n" +
		"  ''|-*) exec sleep 3600;;\n" +
		"esac\n" +
		"exit 1\n"
	path := filepath.Join(bin, "claude")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// liveWorkspace registers, before the daemon boots, a repository with one
// commit as name, whose config.json launches program. edit, when set,
// changes the config before it is written.
func liveWorkspace(t *testing.T, name, program string, edit func(*config.Config)) config.Workspace {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "loom test"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".loom/\n"), 0o644))
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "--no-verify", "-m", "init"}} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	ws := config.Workspace{Name: name, Path: dir}
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.Add(ws.Name, ws.Path))
	cfg := config.DefaultConfig()
	cfg.DefaultProgram = program
	if edit != nil {
		edit(cfg)
	}
	require.NoError(t, config.SaveConfigTo(cfg, config.WorkspaceConfigDir(&ws)))
	return ws
}

// view is the daemon's view of the session titled title in the workspace
// served as ws, as its client has it now.
func (d *testDaemon) view(t *testing.T, ws, title string) (core.InstanceView, bool) {
	t.Helper()
	for _, v := range d.client.Views(d.workspace(t, ws).ID) {
		if v.Title == title {
			return v, true
		}
	}
	return core.InstanceView{}, false
}

// tmuxHas reports whether the agent session titled title runs on the
// test's private tmux server.
func tmuxHas(title string) bool {
	return tmux.Command(context.Background(), "has-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(title))).Run() == nil
}
```

(Imports to add: `context`, `github.com/aidan-bailey/loom/cmd`, `github.com/aidan-bailey/loom/core` if absent.
`tmux.Command(ctx, args...)` and `tmux.CommandOnSocket(ctx, socket, args...)` return an `*exec.Cmd`.)

### C2. Write the failing tests

Append to `internal/cli/session_test.go` (add imports `context`, `os`, `path/filepath`,
`github.com/aidan-bailey/loom/account`, `github.com/aidan-bailey/loom/config`, `github.com/aidan-bailey/loom/session`):

```go
// A started session: `new` returns once it runs, its prompt sent.
func TestSessionNew_ReturnsOnceTheSessionRuns(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)

	out, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a", "--prompt", "hello there")

	require.NoError(t, err)
	assert.Contains(t, out, `Started "alpha" in ws-a`)
	v, ok := d.view(t, "ws-a", "alpha")
	require.True(t, ok)
	assert.NotEqual(t, session.Loading, v.Status, "it returned before the start finished")
	assert.NotEmpty(t, v.WorktreePath)
	assert.True(t, tmuxHas("alpha"))
	assert.Eventually(t, func() bool {
		out, err := tmux.Command(context.Background(), "capture-pane", "-p", "-t", tmux.PaneTarget(tmux.ToLoomTmuxName("alpha"))).Output()
		return err == nil && strings.Contains(string(out), "hello there")
	}, 5*time.Second, 50*time.Millisecond, "the prompt reached the agent's pane")
}

// A start that fails is the command's failure, and leaves no session.
func TestSessionNew_AFailedStartFailsAndLeavesNoSession(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)

	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a", "--branch", "no-such-branch")

	require.Error(t, err)
	assert.Eventually(t, func() bool {
		require.NoError(t, d.client.FlushForTest())
		d.client.Sync()
		_, ok := d.view(t, "ws-a", "alpha")
		return !ok
	}, 5*time.Second, 50*time.Millisecond, "the daemon removed the failed start")
}

// An interrupt ends the wait, not the start: the daemon carries on.
func TestSessionNew_AnInterruptLeavesTheDaemonStarting(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := newSessionCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"new", "alpha", "-w", "ws-a"})
	err := cmd.ExecuteContext(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `interrupted: the loom daemon is still starting "alpha"`)
	assert.Eventually(t, func() bool {
		require.NoError(t, d.client.FlushForTest())
		d.client.Sync()
		v, ok := d.view(t, "ws-a", "alpha")
		return ok && v.Status != session.Loading && tmuxHas("alpha")
	}, 10*time.Second, 50*time.Millisecond)
}

// What `new` refuses, it refuses before asking the daemon for anything.
func TestSessionNew_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"a title the workspace holds", []string{"alpha"}, `ws-a already has a session titled "alpha"`},
		{"a preserved title", []string{"future"}, `title "future" belongs to a saved session`},
		{"an empty title", []string{""}, "title cannot be empty"},
		{"a title too wide", []string{strings.Repeat("a", 33)}, "title cannot be longer than 32 characters"},
		{"an unknown model", []string{"gamma", "--model", "gpt"}, `--model "gpt": want one of default, sonnet`},
		{"an unknown permission mode", []string{"gamma", "--permission-mode", "yolo"}, `--permission-mode "yolo"`},
		{"an unknown effort", []string{"gamma", "--effort", "huge"}, `--effort "huge"`},
		{"both prompt flags", []string{"gamma", "--prompt", "x", "--prompt-file", "y"}, "prompt"},
		{"an unregistered account", []string{"gamma", "--account", "nope"}, `account "nope" is not registered`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateWorkspaces(t)
			registered(t, "ws-a", `[{"title":"alpha","status":3,"program":"true"},{"schema_version":9999,"title":"future"}]`)
			d := serveDaemon(t, fakeClaude(t, ""))

			_, _, err := runSession(t, "", append([]string{"new", "-w", "ws-a"}, tc.args...)...)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			views := d.client.Views(d.workspace(t, "ws-a").ID)
			assert.Len(t, views, 1, "nothing was created")
		})
	}
}

// Remote control on, for an account whose login can't use it: `new`
// refuses with the reason, and --no-remote-control starts it without.
func TestSessionNew_RemoteControlBlocked(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "console")
	on := true
	liveWorkspace(t, "ws-a", agent, func(c *config.Config) { c.ClaudeRemoteControl = &on })
	d := serveDaemonLive(t, agent)
	require.Eventually(t, func() bool {
		require.NoError(t, d.client.FlushForTest())
		d.client.Sync()
		return d.client.RCAuthFor(account.DefaultName).Blocked()
	}, 10*time.Second, 50*time.Millisecond, "fixture: the daemon learned the login can't use remote control")

	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote control is unavailable: you're authenticated with a non-claude.ai account")
	assert.Contains(t, err.Error(), "--no-remote-control")
	_, ok := d.view(t, "ws-a", "alpha")
	assert.False(t, ok, "nothing was created")

	_, _, err = runSession(t, "", "new", "alpha", "-w", "ws-a", "--no-remote-control")
	require.NoError(t, err)
	assert.True(t, tmuxHas("alpha"))
}

// The workspace's settings are the defaults; the flags override them; a
// non-Claude program takes no account.
func TestLaunchOptions_SettingsThenFlags(t *testing.T) {
	isolateWorkspaces(t)
	plan := "plan"
	ws := registered(t, "ws-a", "")
	cfg := config.DefaultConfig()
	cfg.ClaudePermissionMode = &plan
	require.NoError(t, config.SaveConfigTo(cfg, config.WorkspaceConfigDir(&ws)))
	d := serveDaemon(t, fakeClaude(t, ""))
	v := d.workspace(t, "ws-a")
	var warn bytes.Buffer
	c := &client{Client: d.client, errOut: &warn, ctx: context.Background()}

	opts, err := c.launchOptions(v, "claude", newFlags{})
	require.NoError(t, err)
	assert.Equal(t, "plan", opts.PermissionMode, "the workspace's setting")

	opts, err = c.launchOptions(v, "claude", newFlags{permissionMode: "acceptEdits", model: "opus", effort: "high"})
	require.NoError(t, err)
	assert.Equal(t, "acceptEdits", opts.PermissionMode)
	assert.Equal(t, "opus", opts.Model)
	assert.Equal(t, "high", opts.Effort)

	opts, err = c.launchOptions(v, "aider", newFlags{account: "work"})
	require.NoError(t, err)
	assert.Empty(t, opts.Account)
	assert.Contains(t, warn.String(), "--account ignored")
}

func TestSessionKill_AsksFirst(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)

	for _, answer := range []string{"n\n", ""} {
		out, _, err := runSession(t, answer, "kill", "alpha", "-w", "ws-a")
		require.Error(t, err, "answer %q", answer)
		assert.Contains(t, err.Error(), "aborted: nothing was killed")
		assert.Contains(t, out, "Kill session 'alpha' in ws-a? [y/N]")
		assert.True(t, tmuxHas("alpha"), "answer %q killed it", answer)
	}

	out, _, err := runSession(t, "y\n", "kill", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	assert.Contains(t, out, `Killed "alpha" in ws-a`)
	assert.False(t, tmuxHas("alpha"))
	_, ok := d.view(t, "ws-a", "alpha")
	assert.False(t, ok)
}

func TestSessionKill_YesSkipsTheQuestion(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)

	out, _, err := runSession(t, "", "kill", "alpha", "-w", "ws-a", "-y")

	require.NoError(t, err)
	assert.NotContains(t, out, "[y/N]")
	assert.False(t, tmuxHas("alpha"))
}

func TestSessionPauseResume_RoundTrip(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)

	out, _, err := runSession(t, "", "pause", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	assert.Contains(t, out, `Paused "alpha" in ws-a`)
	v, _ := d.view(t, "ws-a", "alpha")
	assert.Equal(t, session.Paused, v.Status)
	assert.False(t, tmuxHas("alpha"))

	_, _, err = runSession(t, "", "pause", "alpha", "-w", "ws-a")
	require.Error(t, err, "a paused session can't be paused")
	assert.Contains(t, err.Error(), "the session is not running (Paused)")

	out, _, err = runSession(t, "", "resume", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	assert.Contains(t, out, `Resumed "alpha" in ws-a`)
	v, _ = d.view(t, "ws-a", "alpha")
	assert.NotEqual(t, session.Paused, v.Status)
	assert.True(t, tmuxHas("alpha"))

	_, _, err = runSession(t, "", "resume", "alpha", "-w", "ws-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the session is not paused")
}

func TestSessionOps_NameASessionExactly(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")

	_, _, err := runSession(t, "", "resume", "alp", "-w", "ws-a")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `no session "alp"`)
}
```

Run: `CGO_ENABLED=0 go test ./internal/cli -run 'TestSessionNew|TestLaunchOptions|TestSessionKill|TestSessionPauseResume|TestSessionOps'`
Expected: build failure (`undefined: newFlags` …).

If `view` in `TestSessionNew_ReturnsOnceTheSessionRuns` reads a stale replica, call
`require.NoError(t, d.client.FlushForTest()); d.client.Sync()` before it, as `workspace` does; do the same in
`d.view` itself if several tests need it.

### C3. Wait for a start

In `internal/cli/join.go`, after `awaitReply`:

```go
// awaitStart waits for the start the Create req asked for. A refused
// Create is its Reply's Err; then either the start's Started (the daemon
// sends it once the initial prompt is sent) or the errors it reports for
// the request end the wait: a failed start notices its error, and its
// save's first (deliverStart), by which time the daemon has removed the
// instance. One loop waits for all three, since a batch can hold more
// than one. Notices naming req that carry no error are written to errOut.
func (c *client) awaitStart(req core.ReqID) (core.Started, error) {
	var (
		started core.Started
		ok      bool
		errs    []error
	)
	_, err := c.await(0, func(events []core.Event) bool {
		for _, ev := range events {
			switch e := ev.(type) {
			case core.Reply:
				if e.Req == req && e.Err != nil {
					errs = append(errs, e.Err)
				}
			case core.Started:
				if e.Req == req {
					started, ok = e, true
				}
			case core.Notice:
				switch {
				case e.Req != req:
				case e.Err != nil:
					errs = append(errs, e.Err)
				case e.Info != "":
					fmt.Fprintf(c.errOut, "loom: %s\n", e.Info)
				}
			}
		}
		return ok || len(errs) > 0
	})
	switch {
	case err != nil:
		return core.Started{}, err
	case len(errs) > 0:
		return core.Started{}, errors.Join(errs...)
	}
	return started, nil
}

// request makes a command's one request (send, given sessionReq) and
// waits for its Reply: its Err fails the command, and a notice riding a
// success (a kill's undroppable stash, a resume's recovery commands) goes
// to errOut.
func (c *client) request(send func(core.ReqID)) error {
	send(sessionReq)
	reply, err := c.awaitReply(sessionReq)
	switch {
	case err != nil:
		return err
	case reply.Err != nil:
		return reply.Err
	}
	if reply.Notice != nil {
		fmt.Fprintf(c.errOut, "loom: %v\n", reply.Notice)
	}
	return nil
}
```

### C4. Add the commands to `internal/cli/session.go`

Add to `newSessionCmd`:

```go
	cmd.AddCommand(
		newSessionListCmd(&workspace),
		newSessionNewCmd(&workspace),
		newSessionOpCmd(&workspace, "kill", "Kill a session: its agent, tmux session and worktree", "Killed", true,
			func(d *client, id core.InstanceID, req core.ReqID) { d.Kill(id, req) }),
		newSessionOpCmd(&workspace, "pause", "Pause a session: its agent stops and its worktree is removed, its branch kept", "Paused", false,
			func(d *client, id core.InstanceID, req core.ReqID) { d.Pause(id, req) }),
		newSessionOpCmd(&workspace, "resume", "Resume a paused session", "Resumed", false,
			func(d *client, id core.InstanceID, req core.ReqID) { d.Resume(id, req) }),
	)
```

(replacing the single `AddCommand(newSessionListCmd(&workspace))`). Then:

```go
// sessionReq is the ID of the one request a session command makes, which
// its Reply (and a Create's Started and notices) names.
const sessionReq core.ReqID = 1

// newFlags are `loom session new`'s flags.
type newFlags struct {
	prompt, promptFile, branch             string
	issue                                  int
	account, model, permissionMode, effort string
	noRemoteControl                        bool
}

// validate refuses flag values the Session Launch Options modal doesn't
// offer.
func (f newFlags) validate() error {
	for _, o := range []struct {
		name, value string
		allowed     []string
	}{
		{"model", f.model, config.ClaudeModelAliases()},
		{"permission-mode", f.permissionMode, config.ClaudePermissionModes},
		{"effort", f.effort, config.ClaudeEfforts},
	} {
		if o.value != "" && !slices.Contains(o.allowed, o.value) {
			return fmt.Errorf("--%s %q: want one of %s", o.name, o.value, strings.Join(o.allowed, ", "))
		}
	}
	if f.issue < 0 {
		return fmt.Errorf("--issue %d: want an issue number", f.issue)
	}
	return nil
}

// readPrompt is the creation prompt: --prompt's, or --prompt-file's text
// without its trailing newlines (the paste's Enter submits it).
func (f newFlags) readPrompt() (string, error) {
	if f.promptFile == "" {
		return f.prompt, nil
	}
	b, err := os.ReadFile(f.promptFile)
	if err != nil {
		return "", fmt.Errorf("--prompt-file: %w", err)
	}
	return strings.TrimRight(string(b), "\n"), nil
}

func newSessionNewCmd(workspace *string) *cobra.Command {
	var f newFlags
	cmd := &cobra.Command{
		Use:   "new <title>",
		Short: "Create and start a session, and wait until it runs",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := f.validate(); err != nil {
				return err
			}
			prompt, err := f.readPrompt()
			if err != nil {
				return err
			}
			// An interrupt ends the wait (await), not the start.
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				return d.newSession(c.OutOrStdout(), *workspace, args[0], prompt, f)
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.prompt, "prompt", "", "Send this prompt once the agent is up")
	fl.StringVar(&f.promptFile, "prompt-file", "", "Send this file's text as the prompt")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
	fl.StringVar(&f.branch, "branch", "", "Check out this existing branch (default: a new branch)")
	fl.IntVar(&f.issue, "issue", 0, "Link the session to this GitHub issue")
	fl.StringVar(&f.account, "account", "", "Claude account to run on (default: the default account)")
	fl.StringVar(&f.model, "model", "", "Model: "+strings.Join(config.ClaudeModelAliases(), ", "))
	fl.StringVar(&f.permissionMode, "permission-mode", "", "Permission mode: "+strings.Join(config.ClaudePermissionModes, ", "))
	fl.StringVar(&f.effort, "effort", "", "Effort: "+strings.Join(config.ClaudeEfforts, ", "))
	fl.BoolVar(&f.noRemoteControl, "no-remote-control", false, "Start without remote control")
	return cmd
}

// newSession creates title in the workspace and waits until it runs: the
// TUI's checks first (core's), then a title no row of the workspace
// holds, since the CLI names sessions by title, then the launch options
// (launchOptions), the Create and its start (awaitStart).
func (d *client) newSession(out io.Writer, workspace, title, prompt string, f newFlags) error {
	_, v, err := d.resolveWorkspace(workspace)
	if err != nil {
		return err
	}
	views := d.Views(v.ID)
	for _, check := range []error{core.CheckRoom(len(views)), core.CheckWritable(v), core.CheckNewTitle(title, v)} {
		if check != nil {
			return check
		}
	}
	for _, iv := range views {
		if iv.Title == title {
			return fmt.Errorf("%s already has a session titled %q", v.Label, title)
		}
	}
	program := v.Settings.GetProgram()
	d.ReloadAccounts()
	opts, err := d.launchOptions(v, program, f)
	if err != nil {
		return err
	}
	d.Create(v.ID, core.NewInstance{
		Title: title, Path: v.RepoPath, Program: program, Prompt: prompt,
		Branch: f.branch, Issue: f.issue, Launch: opts, Start: true,
	}, sessionReq)
	st, err := d.awaitStart(sessionReq)
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("interrupted: the loom daemon is still starting %q", title)
	case err != nil:
		return err
	}
	fmt.Fprintf(out, "Started %q in %s\n", title, v.Label)
	if iv, ok := d.View(st.ID); ok {
		if iv.Branch != "" {
			fmt.Fprintf(out, "  branch:   %s\n", iv.Branch)
		}
		if iv.WorktreePath != "" {
			fmt.Fprintf(out, "  worktree: %s\n", iv.WorktreePath)
		}
	}
	return nil
}

// launchOptions are the options a new session in v launches program with:
// what the Session Launch Options modal pre-fills (the workspace's
// settings, then core.ChooseLaunchAccount), then f's flags. The caller
// reloads the accounts first. The modal's notice, and an --account a
// non-Claude program can't use, go to errOut. Remote control the
// account's login can't use is refused, naming --no-remote-control: the
// modal asks here, and a command can't.
func (d *client) launchOptions(v core.WorkspaceView, program string, f newFlags) (launch.Options, error) {
	opts, notice := core.ChooseLaunchAccount(d, launch.FromSettings(v.Settings), program)
	if notice != "" {
		fmt.Fprintf(d.errOut, "loom: %s\n", notice)
	}
	if f.account != "" {
		switch {
		case !session.IsClaudeProgram(program):
			fmt.Fprintf(d.errOut, "loom: --account ignored: %s is not Claude\n", program)
		case f.account != account.DefaultName:
			if _, ok := d.Account(f.account); !ok {
				return launch.Options{}, fmt.Errorf("account %q is not registered", f.account)
			}
			opts.Account = f.account
		default:
			opts.Account = f.account
		}
	}
	if f.model != "" {
		opts.Model = f.model
	}
	if f.permissionMode != "" {
		opts.PermissionMode = f.permissionMode
	}
	if f.effort != "" {
		opts.Effort = f.effort
	}
	if f.noRemoteControl {
		opts.RemoteControl = false
	}
	if auth := d.RCAuthFor(opts.Account); launch.RemoteControlBlocked(auth, launch.EffectiveRemoteControl(opts), program) {
		return launch.Options{}, fmt.Errorf("remote control is unavailable: %s (--no-remote-control starts without it)", auth.Reason)
	}
	return opts, nil
}

// newSessionOpCmd is `kill`, `pause` or `resume`: op's request on the
// session named by title (and --id), waiting for its Reply, then done.
// With confirm it asks first, as `account remove` does; -y skips that.
func newSessionOpCmd(workspace *string, verb, short, done string, confirm bool, op func(d *client, id core.InstanceID, req core.ReqID)) *cobra.Command {
	var (
		id  uint64
		yes bool
	)
	cmd := &cobra.Command{
		Use:   verb + " <title>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return withDaemon(c, func(d *client) error {
				_, v, err := d.resolveWorkspace(*workspace)
				if err != nil {
					return err
				}
				s, err := findSession(d.Views(v.ID), args[0], core.InstanceID(id))
				if err != nil {
					return fmt.Errorf("%s: %w", v.Label, err)
				}
				if confirm && !yes {
					fmt.Fprintf(c.OutOrStdout(), "Kill session '%s' in %s? [y/N] ", s.Title, v.Label)
					line, _ := bufio.NewReader(c.InOrStdin()).ReadString('\n')
					if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
						return errors.New("aborted: nothing was killed")
					}
				}
				if err := d.request(func(req core.ReqID) { op(d, s.ID, req) }); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "%s %q in %s\n", done, s.Title, v.Label)
				return nil
			})
		},
	}
	cmd.Flags().Uint64Var(&id, "id", 0, "The session's ID (loom session list --json), when several share the title")
	if confirm {
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	}
	return cmd
}
```

The confirmation names "Kill" because `kill` is the one command with `confirm`; if another verb ever confirms, pass the
question in. Imports to add to `session.go`: `bufio`, `context`, `errors`, `os`, `os/signal`, `slices`, `strings`,
`github.com/aidan-bailey/loom/account`, `github.com/aidan-bailey/loom/config`, `github.com/aidan-bailey/loom/session`,
`github.com/aidan-bailey/loom/session/launch`. `join.go` needs `errors` for `awaitStart`.

### C5. Verify and commit

```bash
CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./internal/cli/... ./core/...
CC=clang CGO_ENABLED=1 go test -race ./internal/cli/...
gofmt -l internal/cli/*.go
CGO_ENABLED=0 go vet ./internal/cli/...
GOOS=windows CGO_ENABLED=0 go build ./...
```

Expected: PASS everywhere, no gofmt output. The Windows build matters because `session.go` uses `os/signal` and
`os.Interrupt` (both portable).

Mutation-check the two waits by hand, then restore:

- In `awaitStart`, ignore `Notice` errors: `TestSessionNew_AFailedStartFailsAndLeavesNoSession` must fail (it hangs
  until the test timeout, so run it with `-timeout 60s`).
- In `await`, drop the `c.ctx.Err()` check and the `ctx.Done()` case: `TestSessionNew_AnInterruptLeavesTheDaemonStarting`
  must fail.
- In `newSessionOpCmd`, drop `confirm &&`'s question: `TestSessionKill_AsksFirst` must fail.

```bash
git commit -m "feat(cli): loom session new, kill, pause and resume" -- internal/cli/join.go internal/cli/session.go internal/cli/session_test.go internal/cli/daemon_test.go
```

---

## Package D: end to end, and the docs

**Commit:** `docs,test(e2e): loom session subcommands`

### D1. The end-to-end scenario

Append to `e2e/clients_test.go`:

```go
// `loom session` drives a session from a shell with no TUI open: new
// waits until it runs, list --json shows it, pause and resume round-trip
// it, and kill -y ends its agent and removes it.
func TestE2E_Clients_SessionLifecycle(t *testing.T) {
	sb := newSandbox(t, "")
	loom := func(args ...string) string {
		t.Helper()
		out, err := runLoom(t, sb, append([]string{sb.LoomBin(), "session"}, args...)...)
		require.NoError(t, err, out)
		return out
	}

	out := loom("new", "keeper", "-w", devsandbox.WorkspaceName)
	assert.Contains(t, out, `Started "keeper" in `+devsandbox.WorkspaceName)
	assert.True(t, hasSession(sb, "keeper"))
	require.Eventually(t, func() bool {
		return strings.Contains(paneText(t, sb, tmux.ToLoomTmuxName("keeper")), "commands: work N")
	}, uiTimeout, 100*time.Millisecond, "the fake agent came up")

	var rows []struct {
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(loom("list", "-w", devsandbox.WorkspaceName, "--json")), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "keeper", rows[0].Title)

	assert.Contains(t, loom("pause", "keeper", "-w", devsandbox.WorkspaceName), `Paused "keeper"`)
	assert.False(t, hasSession(sb, "keeper"))
	assert.Contains(t, loom("resume", "keeper", "-w", devsandbox.WorkspaceName), `Resumed "keeper"`)
	assert.True(t, hasSession(sb, "keeper"))

	assert.Contains(t, loom("kill", "keeper", "-w", devsandbox.WorkspaceName, "-y"), `Killed "keeper"`)
	assert.False(t, hasSession(sb, "keeper"))
	assert.Contains(t, loom("list", "-w", devsandbox.WorkspaceName), "No sessions in")
	assert.False(t, sb.DriverRunning(), "no TUI was open")
}
```

Add `encoding/json` to the imports. Run: `go test -tags e2e ./e2e/... -run TestE2E_Clients_SessionLifecycle`.
Expected: PASS. If the sandbox's default profile's banner differs from `commands: work N`, read `createSession` in
`e2e/e2e_test.go` and wait on what it waits on. If the trust prompt holds the start, use the profile the other client
scenarios use.

Then the whole suite: `go test -tags e2e ./e2e/...`.

### D2. Docs

- **`USAGE.md`**, CLI Reference:
  - add a `session` row to the Commands table: "Create, list and drive a registered workspace's sessions (see below); a
    client of the daemon";
  - add a **Session Subcommands** table after Account Subcommands:

    | Command | Description |
    |---|---|
    | `session new <title> [flags]` | Create and start a session in the workspace (`-w`, else the one holding the current directory) with the workspace's default program and launch options; returns once it runs. `--prompt`/`--prompt-file` (sent once the agent is up), `--branch <existing>`, `--issue <n>` (links it), `--account`, `--model`, `--permission-mode`, `--effort`, `--no-remote-control` (needed when remote control is on and the account's login can't use it). Refuses a title the workspace already has. Ctrl-C stops waiting; the daemon still starts it |
    | `session list [--json]` | The workspace's sessions: title, status, branch, account, time in that status. `--json` prints `id`, `title`, `status`, `wait_reason`, `status_since`, `branch`, `worktree`, `repo`, `program`, `account`, `issue`, `pr` |
    | `session kill <title> [-y] [--id <n>]` | Kill a session after asking; `-y` skips the question |
    | `session pause <title> [--id <n>]` | Pause a running session |
    | `session resume <title> [--id <n>]` | Resume a paused session |

    and one sentence under it: sessions are named by exact title; when two share a title, `--id` (from `list --json`)
    picks one.
  - add two examples to Examples (`loom session new fix-login --prompt "Fix the login redirect"`,
    `loom session list --json`).
- **`internal/cli/README.md`**: first paragraph names `loom session` too; add `session.go` to the Files table
  (`SessionCmd`, `list`, `new`, `kill`, `pause`, `resume`; `findSession`, `launchOptions`, the JSON row) and
  `awaitStart`, `request` to `join.go`'s row; add rows to the command table: `session list` → `ReloadRegistry`, then
  `Views`; `session new` → `ReloadAccounts`, `Create`, then a wait for its `Started` or its notices (`awaitStart`);
  `session kill|pause|resume` → `Kill`/`Pause`/`Resume`, then a wait for its `Reply`. Under Tests, name
  `serveDaemonLive`.
- **`internal/cli/CLAUDE.md`**: update the first paragraph to include `SessionCmd`; add three rules (check the budget
  with `go run ./tools/claudemd -v` first; package files allow 100 lines and 20,000 bytes):
  - **Wait for `loom session new` in one loop for its Reply, its `Started` and its own notices (`awaitStart`).** A
    refused Create is the Reply's `Err`, a failed start one or two `Notice{Req}` (the daemon has removed the instance
    by then), a success `Started{Req}` after the initial prompt; `client.events` hands the command the notices naming
    its requests rather than writing them. A wait for the Reply alone could drain the start's notice and then wait for
    a `Started` that never comes. **Enforced** by `TestSessionNew_AFailedStartFailsAndLeavesNoSession` and
    `TestSessionNew_ReturnsOnceTheSessionRuns`.
  - **Name a session by exact title among the workspace's rows, never a prefix, never the workspace terminal, and
    refuse a shared title unless `--id` picks one whose title still matches (`findSession`).** A prefix or a stale ID
    acts on another session. **Enforced** by `TestFindSession`.
  - **Take a new session's checks and launch account from `core` (`CheckRoom`, `CheckWritable`, `CheckNewTitle`,
    `ChooseLaunchAccount`), the TUI's own source.** A check written here drifts from the TUI's. **Convention** — a
    session the TUI would have refused, or one on another account than the modal would pick.
- **`core/README.md`**: one line where it lists the model's files: `new_session.go` holds the checks a client runs
  before a `Create` and the launch account the Session Launch Options modal pre-fills, shared by the TUI and
  `loom session new`.
- **`e2e/CLAUDE.md`**: the `e2e/clients_test.go` sentence lists "`loom session` creating, pausing, resuming and
  killing a session with no TUI open" too.
- **`docs/ARCHITECTURE.md`**: if it lists the subcommands that are daemon clients, add `loom session`.

Run: `CGO_ENABLED=0 go test ./... && go run ./tools/claudemd -v`
Expected: PASS, within budgets.

### D3. Full gate and commit

```bash
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
CGO_ENABLED=0 go vet ./...
GOOS=windows CGO_ENABLED=0 go build ./...
go test -tags e2e ./e2e/...
```

golangci-lint: run the pinned v1 binary with `--fast --new-from-rev=origin/main` if available; otherwise say plainly
that it was skipped.

```bash
git commit -m "docs,test(e2e): loom session subcommands" -- e2e/clients_test.go USAGE.md internal/cli/README.md internal/cli/CLAUDE.md core/README.md e2e/CLAUDE.md
```

(add `docs/ARCHITECTURE.md` if changed).

---

## Spec coverage

| Spec section | Package |
|---|---|
| Commands; naming a session; `--id` | B (`findSession`, `list`), C (ops) |
| `new`: title rules, taken title, defaults, flags, remote control, `--branch`, `--issue`, prompt, return | A (core checks, account), C |
| `list` and its JSON | B |
| `kill` confirmation | C |
| Exit codes, notices | B (`events`), C (`awaitStart`, `request`) |
| How a command runs (resolve, wait, interrupt, daemon lost) | B, C |
| Code table | A, B, C |
| Testing | A, B, C, D1 |
| Documentation | D2 |
