# Supervised Workspaces — Stage 1 Implementation Plan

> **Superseded on 2026-10-03.** The supervised-workspaces spec was replaced by
> [the scrum workspaces spec](../specs/2026-10-03-scrum-workspaces-design.md),
> which depends on [the loom daemon](../specs/2026-10-03-loom-daemon-design.md).
> Do not execute this plan; the daemon's stage 1 plan comes first.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A workspace can be switched to supervised mode. Its main session
then launches as the supervisor and its worktree sessions as workers. Each
session gets four things:

- the role's protocol;
- a stable `--name`;
- the `LOOM_*` variables;
- settings that let `loom work` run without prompts.

Sessions declare and read coordination state through `loom work state`,
`verify`, `note` and `board`, backed by an append-only log guarded by
`flock`.

**Architecture:** A new top-level package `work` owns:

- the log: entries, locked appends, reading;
- the fold into each session's work state, and the authority rules;
- the board, with `landed` and stale derived from git;
- the embedded protocol text;
- session names and launch facts.

It imports no `app`, `ui`, `session` or `cmd` code, so both the launch path
(`session`) and the CLI (`cmd`) use it. The other packages each take a
small part:

- `config` stores each workspace's `mode` and `account` in
  `workspaces.json`.
- `session` resolves a `*work.Launch` at every real launch and applies it.
- `cmd/work.go` is the agent CLI.
- The app preselects the workspace's account and warns in Launch Options,
  and appends `remove` to the log when a session is killed.

**Tech Stack:** Go 1.25, Cobra, Bubble Tea v2, testify, `syscall.Flock`
(Unix only), and git through `internal/exec.GitCommand`.

**Spec:** `docs/superpowers/specs/2026-10-02-supervised-workspaces-design.md`,
Rollout stage 1. Out of scope:

- stage 2: the TUI board, the decisions tab, the settings toggle, the offer
  to restart the main session, and proposals;
- stage 3: the inbox.

---

## Before you start

Run every command from the worktree root.

- **Build:** `CGO_ENABLED=0 go build ./...`.
- **Test one package:** `go test ./work/ -run TestX -v`.
- **Format:** `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')`. Never
  run plain `gofmt -w .`, which rewrites `vendor/`. A file this plan
  creates is not tracked yet, so also `gofmt -w` it by name before its
  commit.
- **Lint:** the local golangci-lint is v2, while `.golangci.yml` targets
  v1.60.1. Run `go vet ./...` instead.
- **No raw git in production code:** never `exec.Command("git", …)`. Go
  through `internalexec.GitCommand` or the `session/git` helpers;
  `TestNoRawGitGhExec` fails the build otherwise. `_test.go` files are
  exempt.
- **New packages need a TestMain:** a new package whose tests can reach
  `config` must call `testenv.MustIsolateLoomDirs()` in its `TestMain`
  (`TestEveryConfigReachingPackageIsolatesLoomDirs`).
- **Commits:** commit after each task, ending the message with
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## File map

| File | Responsibility |
|---|---|
| `session/git/ancestry.go` (new) | `ResolveRef`, `IsAncestor` |
| `config/workspace.go` | `Workspace.Mode`/`Account`, `ModeNormal`/`ModeSupervised`, `SetMode`, `FindByConfigDir` |
| `work/entry.go` (new) | Package doc, `Role`, `Kind`, `State`, `Entry`, constants |
| `work/log.go` (new) | `Dir`, `LogPath`, `BriefPath`, `ReportPath`, `Append`, `Read`, `Forget` |
| `work/lock_unix.go`, `work/lock_windows.go` (new) | `flock` (Unix) / refusal (Windows) |
| `work/fold.go` (new) | `Fold`, `Validate`, `Item`, `Note`, `Folded` |
| `work/launch.go` (new) | `Launch`, `ResolveLaunch`, `SessionName`, `Env`, `AllowRule`, `Executable` |
| `work/protocol.go`, `work/protocol/*.md` (new) | Embedded role protocols, `Protocol` |
| `work/board.go` (new) | `Sessions`, `BaseBranch`, `Tip`, `OriginBase`, `Landed`, `Board`, `Row`, `BuildBoard`, text rendering |
| `cmd/work.go` (new) | `loom work state/verify/note/board` |
| `cmd/workspace.go` | `loom workspace mode` |
| `main.go` | Register `WorkCmd`; `reset` removes the work folder |
| `session/hooks/hooks.go` | `Extra`, `SettingsJSONWith`, `PrepareWith` |
| `session/agent/*.go` | `ApplyNameFlag` on every adapter |
| `session/supervision.go` (new) | `BuildNameCommand`, `supervision`, `supervisedProgram`, `supervisedContextProgram`, `SetLaunchWarning`/`TakeLaunchWarning` |
| `session/agent_restart.go`, `session/instance.go`, `session/subagent_hooks.go`, `session/loom_context.go` | Launch wiring |
| `ui/overlay/sessionLaunchOptions.go` | `SetSupervisor` and its warnings |
| `app/supervision.go` (new), `app/accounts.go`, `app/intents.go`, `app/app.go` | Preselect and warnings; launch warnings on the health tick; `work.Forget` on kill |
| `tools/fakeagent/agent.go`, `e2e/e2e_test.go` | `run` command; supervised e2e |
| `CLAUDE.md`, `USAGE.md` | Documentation |

---

### Task 1: Git helpers `ResolveRef` and `IsAncestor`

**Files:**
- Create: `session/git/ancestry.go`
- Test: `session/git/ancestry_test.go`

- [ ] **Step 1: Write the failing test**

`session/git/ancestry_test.go` (reuses this package's test helpers `newRepo`, `commitFile` and `revParse` from `base_test.go`):

```go
package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRef(t *testing.T) {
	dir := newRepo(t, "main")
	sha, ok := ResolveRef(dir, "refs/heads/main", nil)
	require.True(t, ok)
	assert.Equal(t, revParse(t, dir, "main"), sha)

	_, ok = ResolveRef(dir, "refs/heads/nope", nil)
	assert.False(t, ok)
}

func TestIsAncestor(t *testing.T) {
	dir := newRepo(t, "main")
	first := revParse(t, dir, "HEAD")
	commitFile(t, dir, "two.txt", "two")
	second := revParse(t, dir, "HEAD")

	got, err := IsAncestor(dir, first, second, nil)
	require.NoError(t, err)
	assert.True(t, got, "an older commit is an ancestor")

	got, err = IsAncestor(dir, second, first, nil)
	require.NoError(t, err)
	assert.False(t, got, "a newer commit is not")

	got, err = IsAncestor(dir, first, first, nil)
	require.NoError(t, err)
	assert.True(t, got, "git counts a commit as its own ancestor")

	_, err = IsAncestor(dir, "0000000000000000000000000000000000000000", first, nil)
	assert.Error(t, err, "an unknown commit is an error, not a no")
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./session/git/ -run 'TestResolveRef|TestIsAncestor' -v`
Expected: FAIL (`undefined: ResolveRef`, `undefined: IsAncestor`).

- [ ] **Step 3: Implement**

`session/git/ancestry.go`:

```go
package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// ResolveRef returns the commit SHA ref points at; ok is false when the
// ref does not exist. Read-only, local refs only.
func ResolveRef(repoPath, ref string, runner CommandRunner) (sha string, ok bool) {
	return resolveRef(repoPath, ref, runner)
}

// IsAncestor reports whether ancestor is an ancestor of descendant, as git
// merge-base --is-ancestor answers it: a commit counts as its own
// ancestor. Exit status 1 is a plain "no"; any other failure (an unknown
// commit, a timeout) is an error, so a caller never mistakes it for one.
func IsAncestor(repoPath, ancestor, descendant string, runner CommandRunner) (bool, error) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	err := r.Run(internalexec.GitCommand(ctx, repoPath, "merge-base", "--is-ancestor", ancestor, descendant))
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("merge-base --is-ancestor %s %s: %w", ancestor, descendant, err)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./session/git/ -run 'TestResolveRef|TestIsAncestor' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w session/git/ancestry.go session/git/ancestry_test.go
git add session/git/ancestry.go session/git/ancestry_test.go
git commit -m "feat(git): add ResolveRef and IsAncestor helpers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Workspace mode and account in the registry

**Files:**
- Modify: `config/workspace.go` (the `Workspace` struct at the top; add constants and two methods)
- Test: `config/workspace_mode_test.go`

- [ ] **Step 1: Write the failing test**

`config/workspace_mode_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modeRegistry registers one workspace, kermit, in a throwaway global dir.
func modeRegistry(t *testing.T) (*WorkspaceRegistry, string) {
	t.Helper()
	t.Setenv(EnvGlobalDir, t.TempDir())
	repo := t.TempDir()
	reg := &WorkspaceRegistry{Workspaces: []Workspace{{Name: "kermit", Path: repo}}}
	require.NoError(t, SaveWorkspaceRegistry(reg))
	return reg, repo
}

func TestSetMode_SupervisedWithAccount(t *testing.T) {
	reg, _ := modeRegistry(t)
	acct := "personal"
	require.NoError(t, reg.SetMode("kermit", ModeSupervised, &acct))

	assert.Equal(t, ModeSupervised, reg.Get("kermit").Mode, "the receiver is refreshed")
	fresh, err := LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, ModeSupervised, fresh.Get("kermit").Mode)
	assert.Equal(t, "personal", fresh.Get("kermit").Account)
}

func TestSetMode_NilAccountKeepsTheStoredOne(t *testing.T) {
	reg, _ := modeRegistry(t)
	acct := "personal"
	require.NoError(t, reg.SetMode("kermit", ModeSupervised, &acct))
	require.NoError(t, reg.SetMode("kermit", ModeNormal, nil))
	require.NoError(t, reg.SetMode("kermit", ModeSupervised, nil))

	fresh, err := LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, "personal", fresh.Get("kermit").Account)
}

func TestSetMode_RejectsUnknownModeAndWorkspace(t *testing.T) {
	reg, _ := modeRegistry(t)
	assert.Error(t, reg.SetMode("kermit", "babysit", nil))
	assert.Error(t, reg.SetMode("nope", ModeSupervised, nil))
}

func TestSetMode_KeepsAnotherWritersChanges(t *testing.T) {
	reg, _ := modeRegistry(t)
	other, err := LoadWorkspaceRegistry()
	require.NoError(t, err)
	other.Workspaces = append(other.Workspaces, Workspace{Name: "loom", Path: t.TempDir()})
	require.NoError(t, SaveWorkspaceRegistry(other))

	require.NoError(t, reg.SetMode("kermit", ModeSupervised, nil))
	fresh, err := LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.NotNil(t, fresh.Get("loom"), "SetMode reloads before saving")
}

func TestFindByConfigDir(t *testing.T) {
	reg, repo := modeRegistry(t)
	ws := reg.FindByConfigDir(filepath.Join(repo, ".loom"))
	require.NotNil(t, ws)
	assert.Equal(t, "kermit", ws.Name)
	assert.Nil(t, reg.FindByConfigDir(repo), "the repo itself is not its config dir")
	assert.Nil(t, reg.FindByConfigDir(""))
}

func TestNormalWorkspaceWritesNoMode(t *testing.T) {
	_, _ = modeRegistry(t)
	dir, err := GetGlobalConfigDir()
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(dir, workspacesFileName))
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"mode"`, "a normal workspace is written as before")
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./config/ -run 'TestSetMode|TestFindByConfigDir|TestNormalWorkspaceWritesNoMode' -v`
Expected: FAIL (`undefined: ModeSupervised`, `reg.SetMode undefined`).

- [ ] **Step 3: Implement**

In `config/workspace.go`, replace the `Workspace` struct with:

```go
// Workspace modes. A normal workspace behaves as loom always has; in a
// supervised one the main session supervises and every worktree session is
// a worker (docs/superpowers/specs/2026-10-02-supervised-workspaces-design.md).
const (
	ModeNormal     = ""
	ModeSupervised = "supervised"
)

// Workspace represents a registered workspace tied to a git repository.
type Workspace struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	AddedAt time.Time `json:"added_at"`
	// Mode is ModeNormal or ModeSupervised. Sessions read it at each launch.
	Mode string `json:"mode,omitempty"`
	// Account is the Claude account a supervised workspace's sessions
	// share ("" = the default account): cross-session messages don't cross
	// accounts. Kept, but unused, while the workspace is normal.
	Account string `json:"account,omitempty"`
}
```

Add, after `UpdateLastUsed`:

```go
// SetMode sets a workspace's mode. A non-nil account also sets the account
// its sessions share in supervised mode; nil keeps the stored one. Like
// UpdateLastUsed, it reloads the registry before saving, so another
// process's changes survive.
func (r *WorkspaceRegistry) SetMode(name, mode string, account *string) error {
	if mode != ModeNormal && mode != ModeSupervised {
		return fmt.Errorf("unknown workspace mode %q (want normal or %s)", mode, ModeSupervised)
	}
	fresh, err := LoadWorkspaceRegistry()
	if err != nil {
		return err
	}
	ws := fresh.Get(name)
	if ws == nil {
		return fmt.Errorf("workspace %q not found", name)
	}
	ws.Mode = mode
	if account != nil {
		ws.Account = *account
	}
	if err := SaveWorkspaceRegistry(fresh); err != nil {
		return err
	}
	r.syncFrom(fresh)
	return nil
}

// FindByConfigDir returns the workspace whose loom data lives in dir (its
// <repo>/.loom), or nil.
func (r *WorkspaceRegistry) FindByConfigDir(dir string) *Workspace {
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	for i := range r.Workspaces {
		if filepath.Clean(WorkspaceConfigDir(&r.Workspaces[i])) == dir {
			return &r.Workspaces[i]
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./config/ -v -run 'TestSetMode|TestFindByConfigDir|TestNormalWorkspaceWritesNoMode' && go test ./config/ ./internal/devsandbox/ ./cmd/`
Expected: PASS (devsandbox and cmd still compile with the wider struct).

- [ ] **Step 5: Commit**

```bash
gofmt -w config/workspace_mode_test.go
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add config/workspace.go config/workspace_mode_test.go
git commit -m "feat(config): store a supervised mode and account per workspace

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The work log: entries, locked appends and reading

**Files:**
- Create: `work/entry.go`, `work/log.go`, `work/lock_unix.go`, `work/lock_windows.go`, `work/testmain_test.go`
- Test: `work/log_test.go`

- [ ] **Step 1: Write the failing test**

`work/testmain_test.go`:

```go
package work

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
)

// TestMain points LOOM_HOME and LOOM_GLOBAL_DIR at throwaway directories,
// so no test here can resolve the developer's real ~/.loom. Tests that
// need directories of their own still t.Setenv over them.
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
```

`work/log_test.go`:

```go
//go:build !windows

package work

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func logEntry(by string) Entry {
	return Entry{V: FormatVersion, At: time.Unix(1700000000, 0).UTC(), By: by, Role: RoleWorker,
		Kind: KindState, State: StateWorking, Summary: "plan"}
}

func appendEntry(t *testing.T, dir string, e Entry) {
	t.Helper()
	require.NoError(t, Append(dir, func([]Entry) (Entry, error) { return e, nil }))
}

func TestAppendThenRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "work")
	appendEntry(t, dir, logEntry("a"))
	appendEntry(t, dir, logEntry("b"))

	got, bad, err := Read(dir)
	require.NoError(t, err)
	assert.Zero(t, bad)
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].By)
	assert.Equal(t, "b", got[1].By)
}

func TestAppendRefusalWritesNothing(t *testing.T) {
	dir := t.TempDir()
	appendEntry(t, dir, logEntry("a"))
	err := Append(dir, func(existing []Entry) (Entry, error) {
		assert.Len(t, existing, 1, "build sees the entries already there")
		return Entry{}, errors.New("refused")
	})
	assert.EqualError(t, err, "refused")
	got, _, err := Read(dir)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestReadMissingLogIsEmpty(t *testing.T) {
	got, bad, err := Read(filepath.Join(t.TempDir(), "absent"))
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, bad)
}

func TestReadSkipsBadLinesAndAPartialTail(t *testing.T) {
	dir := t.TempDir()
	appendEntry(t, dir, logEntry("a"))
	f, err := os.OpenFile(LogPath(dir), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("not json\n{\"v\":1,\"by\":\"half")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	got, bad, err := Read(dir)
	require.NoError(t, err)
	assert.Len(t, got, 1, "the partial last line is not read yet")
	assert.Equal(t, 1, bad)
}

// A crash or a hand edit can leave the last line without its newline. The
// next append starts a fresh line rather than merging its entry into that
// one, which would lose the entry while the CLI reported success.
func TestAppendAfterATornLineKeepsTheNewEntry(t *testing.T) {
	dir := t.TempDir()
	appendEntry(t, dir, logEntry("a"))
	f, err := os.OpenFile(LogPath(dir), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"v":1,"by":"to`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	appendEntry(t, dir, logEntry("b"))

	got, bad, err := Read(dir)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "b", got[1].By, "the new entry survives")
	assert.Equal(t, 1, bad, "the torn line stands alone and is skipped")
}

// Every append holds the lock while it reads and writes, so concurrent
// appends each land as one whole line and each build sees every earlier one.
func TestConcurrentAppendsAreSerialized(t *testing.T) {
	dir := t.TempDir()
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			assert.NoError(t, Append(dir, func(existing []Entry) (Entry, error) {
				e := logEntry(fmt.Sprint(i))
				e.Summary = fmt.Sprint(len(existing)) // how many this append saw
				return e, nil
			}))
		}(i)
	}
	wg.Wait()

	got, bad, err := Read(dir)
	require.NoError(t, err)
	assert.Zero(t, bad)
	require.Len(t, got, n)
	for i, e := range got {
		assert.Equal(t, fmt.Sprint(i), e.Summary, "append %d saw every earlier entry", i)
	}
}

func TestForget(t *testing.T) {
	cfg := t.TempDir()
	dir := Dir(cfg)
	appendEntry(t, dir, logEntry("fix/ci"))
	for _, p := range []string{BriefPath(dir, "fix/ci"), ReportPath(dir, "fix/ci")} {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}

	require.NoError(t, Forget(cfg, "fix/ci", time.Now()))

	got, _, err := Read(dir)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, KindRemove, got[1].Kind)
	assert.Equal(t, "fix/ci", got[1].Title)
	assert.Equal(t, ByLoom, got[1].By)
	assert.NoFileExists(t, BriefPath(dir, "fix/ci"))
	assert.NoFileExists(t, ReportPath(dir, "fix/ci"))
}

func TestForgetWithoutALogDoesNothing(t *testing.T) {
	cfg := t.TempDir()
	require.NoError(t, Forget(cfg, "x", time.Now()))
	assert.NoDirExists(t, Dir(cfg))
}

func TestBriefAndReportPathsStayInTheirFolders(t *testing.T) {
	assert.Equal(t, filepath.Join("/w", "reports", "a%2Fb.md"), ReportPath("/w", "a/b"))
	assert.Equal(t, filepath.Join("/w", "briefs", "..%2Fx.md"), BriefPath("/w", "../x"))
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./work/ -v`
Expected: FAIL to build (`undefined: Entry`, `undefined: Append`, …).

- [ ] **Step 3: Implement**

`work/entry.go`:

```go
// Package work is the coordination store of a supervised workspace: an
// append-only log of what each session declared, what the supervisor
// verified and decided, and what loom recorded, plus the board folded from
// it. Sessions write it through `loom work`; loom reads it. It imports no
// app, ui, session or cmd code, so the launch path and the CLI both use it.
// See docs/superpowers/specs/2026-10-02-supervised-workspaces-design.md.
package work

import "time"

// Role is a session's part in a supervised workspace.
type Role string

const (
	RoleSupervisor Role = "supervisor"
	RoleWorker     Role = "worker"
)

// Kind is what an entry records.
type Kind string

const (
	// KindState is a worker declaring its own work state.
	KindState Kind = "state"
	// KindVerify is the supervisor's review passing a ready worker.
	KindVerify Kind = "verify"
	// KindNote is a routine decision the supervisor logged.
	KindNote Kind = "note"
	// KindRemove is loom forgetting a killed session.
	KindRemove Kind = "remove"
)

// State is a work state.
type State string

const (
	StateWorking State = "working"
	StateBlocked State = "blocked"
	StateReady   State = "ready"
	// StateVerified comes from a verify entry; no state entry declares it.
	StateVerified State = "verified"
)

// FormatVersion is the entry format this build writes and folds.
const FormatVersion = 1

// ByLoom is the By of the entries loom writes itself.
const ByLoom = "loom"

// Entry is one line of the log. Which fields after Kind are set depends on
// Kind (see Validate).
type Entry struct {
	V    int       `json:"v"`
	At   time.Time `json:"at"`
	By   string    `json:"by"`
	Role Role      `json:"role,omitempty"`
	Kind Kind      `json:"kind"`

	State   State  `json:"state,omitempty"`
	Summary string `json:"summary,omitempty"`
	Head    string `json:"head,omitempty"`
	Report  string `json:"report,omitempty"`
	Target  string `json:"target,omitempty"`
	Notes   string `json:"notes,omitempty"`
	Text    string `json:"text,omitempty"`
	Title   string `json:"title,omitempty"`
}
```

`work/log.go`:

```go
package work

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const logFileName = "log.jsonl"

// Dir is a workspace's work folder inside its config dir (<repo>/.loom).
// Nothing at its top level may be named config, HEAD, objects or refs: the
// Claude sandbox write-denies those names in any writable directory.
func Dir(configDir string) string { return filepath.Join(configDir, "work") }

// LogPath is the log in the work folder dir.
func LogPath(dir string) string { return filepath.Join(dir, logFileName) }

// BriefPath is where a session's brief lives in the work folder dir. The
// title is path-escaped into one segment, so a "/" in it can't reach
// another folder.
func BriefPath(dir, title string) string {
	return filepath.Join(dir, "briefs", url.PathEscape(title)+".md")
}

// ReportPath is where a session's report lives; see BriefPath.
func ReportPath(dir, title string) string {
	return filepath.Join(dir, "reports", url.PathEscape(title)+".md")
}

// Append adds one entry to the log in dir under an exclusive lock. build
// receives every complete entry already in the log, read under the same
// lock, and returns the entry to append, or an error to refuse with, in
// which case nothing is written. The line goes out in a single write,
// after a newline of its own when the log's last line was left torn.
func Append(dir string, build func(existing []Entry) (Entry, error)) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("work: create %s: %w", dir, err)
	}
	f, err := os.OpenFile(LogPath(dir), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("work: open log: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := lockFile(f); err != nil {
		return fmt.Errorf("work: lock log: %w", err)
	}
	defer func() { _ = unlockFile(f) }()

	existing, _, err := decode(f)
	if err != nil {
		return err
	}
	e, err := build(existing)
	if err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("work: encode entry: %w", err)
	}
	line = append(line, '\n')
	// A crash or a hand edit can leave the last line without its newline.
	// Written straight after it, this entry would merge into that line and
	// neither would decode, so start a fresh line first.
	torn, err := endsTorn(f)
	if err != nil {
		return err
	}
	if torn {
		line = append([]byte{'\n'}, line...)
	}
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("work: append entry: %w", err)
	}
	return nil
}

// endsTorn reports whether f is non-empty and its last byte is not a
// newline: a line left incomplete. Under the lock no loom writer can be
// mid-line, so only a crash or another program leaves one.
func endsTorn(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("work: stat log: %w", err)
	}
	if info.Size() == 0 {
		return false, nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false, fmt.Errorf("work: read log: %w", err)
	}
	return last[0] != '\n', nil
}

// Read returns the complete entries of the log in dir and how many lines
// it could not decode. A missing log is empty. It takes no lock: every
// line is written whole, and a last line still missing its newline is left
// for the next read.
func Read(dir string) ([]Entry, int, error) {
	f, err := os.Open(LogPath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("work: open log: %w", err)
	}
	defer func() { _ = f.Close() }()
	return decode(f)
}

// decode reads every newline-terminated line of r from its start.
func decode(r io.ReadSeeker) ([]Entry, int, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("work: rewind log: %w", err)
	}
	var (
		entries []Entry
		bad     int
	)
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return entries, bad, nil // a line without its newline is incomplete
		}
		if err != nil {
			return nil, 0, fmt.Errorf("work: read log: %w", err)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil {
			bad++
			continue
		}
		entries = append(entries, e)
	}
}

// Forget records that loom killed the session titled title: it appends a
// remove entry, so a later session with the same title starts clean, and
// deletes the title's brief and report. A workspace that never had a log
// is left untouched.
func Forget(configDir, title string, now time.Time) error {
	dir := Dir(configDir)
	if _, err := os.Stat(LogPath(dir)); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	err := Append(dir, func([]Entry) (Entry, error) {
		return Entry{V: FormatVersion, At: now.UTC(), By: ByLoom, Kind: KindRemove, Title: title}, nil
	})
	if err != nil {
		return err
	}
	for _, p := range []string{BriefPath(dir, title), ReportPath(dir, title)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("work: remove %s: %w", p, err)
		}
	}
	return nil
}
```

`work/lock_unix.go`:

```go
//go:build !windows

package work

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on f, blocking until it is free. Each
// open file description holds its own lock, so two appends in one process
// exclude each other as well as appends from other processes.
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
```

`work/lock_windows.go`:

```go
//go:build windows

package work

import (
	"errors"
	"os"
)

// errNoLock: the work log needs flock, which Windows lacks; loom's hooks
// don't run there either.
var errNoLock = errors.New("supervised workspaces are not supported on Windows")

func lockFile(*os.File) error { return errNoLock }

func unlockFile(*os.File) error { return nil }
```

- [ ] **Step 4: Run the tests**

Run: `go test ./work/ -v && GOOS=windows go vet ./work/`
Expected: PASS, and the Windows build vets cleanly.

- [ ] **Step 5: Commit**

```bash
gofmt -w work/*.go
git add work/
git commit -m "feat(work): add the append-only work log with locked appends

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Folding the log: the authority rules and each session's state

**Files:**
- Create: `work/fold.go`
- Test: `work/fold_test.go`

- [ ] **Step 1: Write the failing test**

`work/fold_test.go`:

```go
package work

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

// st is a worker's state entry; verify the supervisor kermit's verify.
func st(by string, s State, head string) Entry {
	return Entry{V: FormatVersion, At: t0, By: by, Role: RoleWorker, Kind: KindState,
		State: s, Summary: string(s) + " summary", Head: head}
}

func verify(target, head string) Entry {
	return Entry{V: FormatVersion, At: t0.Add(time.Minute), By: "kermit", Role: RoleSupervisor,
		Kind: KindVerify, Target: target, Head: head, Notes: "ok"}
}

func TestFold_LatestStateWins(t *testing.T) {
	f := Fold([]Entry{st("a", StateWorking, ""), st("a", StateBlocked, "")})
	require.Contains(t, f.Items, "a")
	assert.Equal(t, StateBlocked, f.Items["a"].State)
	assert.Zero(t, f.Skipped)
}

func TestFold_VerifyNeedsTheReadyHead(t *testing.T) {
	f := Fold([]Entry{st("a", StateReady, "abc"), verify("a", "abc")})
	assert.Equal(t, StateVerified, f.Items["a"].State)
	assert.Equal(t, "abc", f.Items["a"].Head)
	assert.Equal(t, "kermit", f.Items["a"].VerifiedBy)

	f = Fold([]Entry{st("a", StateReady, "abc"), verify("a", "def")})
	assert.Equal(t, StateReady, f.Items["a"].State, "a verify of another head is skipped")
	assert.Equal(t, 1, f.Skipped)
}

func TestFold_NewStateClearsTheVerification(t *testing.T) {
	f := Fold([]Entry{st("a", StateReady, "abc"), verify("a", "abc"), st("a", StateWorking, "")})
	assert.Equal(t, StateWorking, f.Items["a"].State)
	assert.Empty(t, f.Items["a"].Head)
	assert.Empty(t, f.Items["a"].VerifiedBy)
}

func TestFold_RemoveForgetsTheTitle(t *testing.T) {
	f := Fold([]Entry{
		st("a", StateReady, "abc"),
		{V: FormatVersion, At: t0, By: ByLoom, Kind: KindRemove, Title: "a"},
		st("a", StateWorking, ""),
	})
	assert.Equal(t, StateWorking, f.Items["a"].State, "a reused title starts clean")
	assert.Empty(t, f.Items["a"].Head)
}

func TestFold_Notes(t *testing.T) {
	f := Fold([]Entry{{V: FormatVersion, At: t0, By: "kermit", Role: RoleSupervisor, Kind: KindNote, Text: "land a before b"}})
	require.Len(t, f.Notes, 1)
	assert.Equal(t, "land a before b", f.Notes[0].Text)
}

func TestFold_SkipsOtherVersionsButPassesOverUnknownKinds(t *testing.T) {
	newer := st("a", StateWorking, "")
	newer.V = FormatVersion + 1
	later := Entry{V: FormatVersion, At: t0, By: "kermit", Role: RoleSupervisor, Kind: "propose", Title: "b"}
	f := Fold([]Entry{newer, later})
	assert.Empty(t, f.Items)
	assert.Equal(t, 1, f.Skipped, "only the other format version counts")
}

func TestValidate(t *testing.T) {
	ready := Fold([]Entry{st("a", StateReady, "abc")})
	verified := Fold([]Entry{st("a", StateReady, "abc"), verify("a", "abc")})
	working := Fold([]Entry{st("a", StateWorking, "")})

	supervisorState := st("kermit", StateWorking, "")
	supervisorState.Role = RoleSupervisor
	workerVerify := verify("a", "abc")
	workerVerify.Role = RoleWorker

	cases := []struct {
		name string
		f    Folded
		e    Entry
		ok   bool
	}{
		{"a worker declares", Folded{}, st("a", StateWorking, ""), true},
		{"the supervisor can't declare a state", Folded{}, supervisorState, false},
		{"an unknown state", Folded{}, st("a", "done", ""), false},
		{"ready needs a head", Folded{}, st("a", StateReady, ""), false},
		{"verify a ready worker", ready, verify("a", "abc"), true},
		{"verify needs the supervisor", ready, workerVerify, false},
		{"verify a working worker", working, verify("a", "abc"), false},
		{"verify an unknown session", Folded{}, verify("a", "abc"), false},
		{"verify twice", verified, verify("a", "abc"), false},
		{"verify a moved head", ready, verify("a", "def"), false},
		{"a worker can't note", Folded{}, Entry{V: FormatVersion, By: "a", Role: RoleWorker, Kind: KindNote, Text: "x"}, false},
		{"an empty note", Folded{}, Entry{V: FormatVersion, By: "kermit", Role: RoleSupervisor, Kind: KindNote, Text: "  "}, false},
		{"only loom removes", Folded{}, Entry{V: FormatVersion, By: "a", Role: RoleWorker, Kind: KindRemove, Title: "b"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.f, tc.e)
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./work/ -run 'TestFold|TestValidate' -v`
Expected: FAIL (`undefined: Fold`, `undefined: Validate`, `undefined: Folded`).

- [ ] **Step 3: Implement**

`work/fold.go`:

```go
package work

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Item is one session's work state, folded from the log.
type Item struct {
	Title   string
	State   State
	Summary string
	// At is when the current state was declared, or verified.
	At time.Time
	// Head is the branch tip recorded with ready, kept through verified.
	Head string
	// Report is the report recorded with ready.
	Report string
	// VerifiedBy and Notes come from the verify entry, once verified.
	VerifiedBy string
	Notes      string
}

// Note is a routine decision the supervisor logged.
type Note struct {
	At   time.Time `json:"at"`
	By   string    `json:"by"`
	Text string    `json:"text"`
}

// Folded is the log replayed in order.
type Folded struct {
	// Items holds the sessions that declared a work state, by title.
	Items map[string]*Item
	// Notes are the supervisor's decisions, oldest first.
	Notes []Note
	// Skipped counts entries that broke the rules (a hand edit, a race the
	// CLI lost) or were written in another format version.
	Skipped int
}

// Fold replays entries in order. Entries of a kind this build doesn't know
// (a later stage's) are passed over without being counted.
func Fold(entries []Entry) Folded {
	f := Folded{Items: map[string]*Item{}}
	for _, e := range entries {
		if e.V != FormatVersion {
			f.Skipped++
			continue
		}
		if !knownKind(e.Kind) {
			continue
		}
		if Validate(f, e) != nil {
			f.Skipped++
			continue
		}
		f.apply(e)
	}
	return f
}

func knownKind(k Kind) bool {
	switch k {
	case KindState, KindVerify, KindNote, KindRemove:
		return true
	}
	return false
}

// apply folds e, which Validate accepted, into f.
func (f *Folded) apply(e Entry) {
	switch e.Kind {
	case KindState:
		it := &Item{Title: e.By, State: e.State, Summary: e.Summary, At: e.At}
		if e.State == StateReady {
			it.Head, it.Report = e.Head, e.Report
		}
		f.Items[e.By] = it
	case KindVerify:
		it := f.Items[e.Target]
		it.State, it.At, it.VerifiedBy, it.Notes = StateVerified, e.At, e.By, e.Notes
	case KindNote:
		f.Notes = append(f.Notes, Note{At: e.At, By: e.By, Text: e.Text})
	case KindRemove:
		delete(f.Items, e.Title)
	}
}

// Validate reports why e may not follow the entries folded into f, or nil.
// The CLI refuses such an entry before writing it; Fold skips one already
// written. The rules catch protocol mistakes, not malice: any agent can
// write any file as the user.
func Validate(f Folded, e Entry) error {
	switch e.Kind {
	case KindState:
		if e.Role != RoleWorker {
			return errors.New("only a worker declares a work state; the supervisor verifies with `loom work verify`")
		}
		if e.By == "" {
			return errors.New("a work state needs the declaring session's title")
		}
		switch e.State {
		case StateWorking, StateBlocked:
		case StateReady:
			if e.Head == "" {
				return errors.New("ready needs the branch tip it was declared at")
			}
		default:
			return fmt.Errorf("unknown work state %q (want working, blocked or ready)", e.State)
		}
	case KindVerify:
		if e.Role != RoleSupervisor {
			return errors.New("only the supervisor verifies a worker")
		}
		it := f.Items[e.Target]
		switch {
		case it == nil:
			return fmt.Errorf("%s has not declared a work state", e.Target)
		case it.State == StateVerified:
			return fmt.Errorf("%s is already verified at %s", e.Target, short(it.Head))
		case it.State != StateReady:
			return fmt.Errorf("%s is %s, not ready", e.Target, it.State)
		case it.Head != e.Head:
			return fmt.Errorf("%s declared ready at %s, not %s", e.Target, short(it.Head), short(e.Head))
		}
	case KindNote:
		if e.Role != RoleSupervisor {
			return errors.New("only the supervisor logs decisions")
		}
		if strings.TrimSpace(e.Text) == "" {
			return errors.New("the decision is empty")
		}
	case KindRemove:
		if e.By != ByLoom {
			return errors.New("only loom removes a session from the board")
		}
	default:
		return fmt.Errorf("unknown entry kind %q", e.Kind)
	}
	return nil
}

// short abbreviates a commit SHA for messages.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./work/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w work/fold.go work/fold_test.go
git add work/fold.go work/fold_test.go
git commit -m "feat(work): fold the log into work states under the authority rules

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Launch facts: session names, variables and the CLI's path

**Files:**
- Create: `work/launch.go`
- Test: `work/launch_test.go`

- [ ] **Step 1: Write the failing test**

`work/launch_test.go`:

```go
package work

import (
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionName(t *testing.T) {
	assert.Equal(t, "kermit", SessionName("kermit", "kermit", RoleSupervisor))
	assert.Equal(t, "kermit/fix-ci", SessionName("kermit", "fix-ci", RoleWorker))
	assert.Equal(t, "kermit/fix-login-now", SessionName("kermit", "fix: login now", RoleWorker))
	assert.Equal(t, "kermit/session", SessionName("kermit", "日本", RoleWorker), "a title with nothing usable")
	assert.Equal(t, "kermit/x", SessionName("kermit", "--x", RoleWorker), "never starts like a flag")
}

func TestLaunchEnvAndRule(t *testing.T) {
	l := Launch{Workspace: "kermit", Title: "fix ci", Role: RoleWorker, WorkDir: "/r/.loom/work", Loom: "/bin/loom"}
	assert.Equal(t, []string{"LOOM_INSTANCE=fix ci", "LOOM_ROLE=worker", "LOOM_WORK_DIR=/r/.loom/work"}, l.Env())
	assert.Equal(t, "Bash(/bin/loom work *)", l.AllowRule())
	assert.Equal(t, "kermit/fix-ci", l.SessionName())
}

func TestResolveLaunch(t *testing.T) {
	repo := t.TempDir()
	cfgDir := filepath.Join(repo, ".loom")
	reg := &config.WorkspaceRegistry{Workspaces: []config.Workspace{{Name: "kermit", Path: repo}}}

	assert.Nil(t, ResolveLaunch(reg, cfgDir, "fix-ci", false), "a normal workspace")
	assert.Nil(t, ResolveLaunch(nil, cfgDir, "fix-ci", false))

	reg.Workspaces[0].Mode = config.ModeSupervised
	reg.Workspaces[0].Account = "personal"
	worker := ResolveLaunch(reg, cfgDir, "fix-ci", false)
	require.NotNil(t, worker)
	assert.Equal(t, Launch{Workspace: "kermit", Title: "fix-ci", Role: RoleWorker,
		WorkDir: Dir(cfgDir), Account: "personal", Loom: Executable()}, *worker)

	main := ResolveLaunch(reg, cfgDir, "kermit", true)
	require.NotNil(t, main)
	assert.Equal(t, RoleSupervisor, main.Role)

	assert.Nil(t, ResolveLaunch(reg, filepath.Join(t.TempDir(), ".loom"), "x", false),
		"another workspace's config dir")
}

func TestExecutable(t *testing.T) {
	exe := Executable()
	assert.True(t, exe == "loom" || filepath.IsAbs(exe), exe)
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./work/ -run 'TestSessionName|TestLaunch|TestResolveLaunch|TestExecutable' -v`
Expected: FAIL (`undefined: SessionName`, `undefined: Launch`, …).

- [ ] **Step 3: Implement**

`work/launch.go`:

```go
package work

import (
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/aidan-bailey/loom/config"
)

// Variables loom exports into every session of a supervised workspace;
// `loom work` refuses without them.
const (
	EnvInstance = "LOOM_INSTANCE"
	EnvRole     = "LOOM_ROLE"
	EnvWorkDir  = "LOOM_WORK_DIR"
)

// Launch is what a supervised launch adds for one session.
type Launch struct {
	// Workspace is the workspace's name; Title the session's.
	Workspace, Title string
	Role             Role
	// WorkDir is the workspace's work folder (Dir).
	WorkDir string
	// Account is the Claude account the workspace's sessions share ("" =
	// the default account).
	Account string
	// Loom is the loom binary the protocol tells the agent to run.
	Loom string
}

// ResolveLaunch returns the supervision of a launch of the session titled
// title, whose instance keeps its data in configDir, or nil when that
// workspace is unregistered or normal. main marks the workspace's main
// session, which supervises.
func ResolveLaunch(reg *config.WorkspaceRegistry, configDir, title string, main bool) *Launch {
	if reg == nil {
		return nil
	}
	ws := reg.FindByConfigDir(configDir)
	if ws == nil || ws.Mode != config.ModeSupervised {
		return nil
	}
	role := RoleWorker
	if main {
		role = RoleSupervisor
	}
	return &Launch{Workspace: ws.Name, Title: title, Role: role, WorkDir: Dir(configDir),
		Account: ws.Account, Loom: Executable()}
}

// SessionName is the session's cross-session message address.
func (l Launch) SessionName() string { return SessionName(l.Workspace, l.Title, l.Role) }

// Env is the launch's session-environment variables.
func (l Launch) Env() []string {
	return []string{EnvInstance + "=" + l.Title, EnvRole + "=" + string(l.Role), EnvWorkDir + "=" + l.WorkDir}
}

// AllowRule is the permission rule that lets the session run the CLI
// without a prompt.
func (l Launch) AllowRule() string { return "Bash(" + l.Loom + " work *)" }

// SessionName is the address other sessions message: the workspace's name
// for its main session, <workspace>/<title> for a worker. Without --name,
// Claude derives a new name on each launch. Both parts are reduced to
// characters that are safe in a shell word.
func SessionName(workspace, title string, role Role) string {
	if role == RoleSupervisor {
		return nameToken(workspace)
	}
	return nameToken(workspace) + "/" + nameToken(title)
}

var nameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._/-]+`)

// nameToken turns whitespace runs into "-", drops anything else outside
// [A-Za-z0-9._/-], and keeps the result from starting like a flag or a path.
func nameToken(s string) string {
	s = nameUnsafe.ReplaceAllString(strings.Join(strings.Fields(s), "-"), "")
	s = strings.TrimLeft(s, "-./")
	if s == "" {
		return "session"
	}
	return s
}

var (
	exeOnce sync.Once
	exePath string
)

// Executable is the loom binary agents call. It is this process's own, so
// an agent runs the build that launched it whatever its PATH holds (a dev
// sandbox, a Nix store path). A path containing a character that would
// need shell quoting falls back to "loom" on PATH.
func Executable() string {
	exeOnce.Do(func() {
		exePath = "loom"
		if p, err := os.Executable(); err == nil && !strings.ContainsAny(p, " \t\n'\"\\$`;&|<>()*?[]{}~!#") {
			exePath = p
		}
	})
	return exePath
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./work/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w work/launch.go work/launch_test.go
git add work/launch.go work/launch_test.go
git commit -m "feat(work): resolve supervised launches, session names and variables

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The protocol text

**Files:**
- Create: `work/protocol/supervisor.md`, `work/protocol/worker.md`, `work/protocol.go`
- Test: `work/protocol_test.go`

- [ ] **Step 1: Write the failing test**

`work/protocol_test.go`:

```go
package work

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProtocol(t *testing.T) {
	for _, role := range []Role{RoleSupervisor, RoleWorker} {
		text := Protocol(role, "/nix/store/x-loom/bin/loom")
		assert.NotContains(t, text, "{{loom}}", "%s: every placeholder is filled", role)
		assert.Contains(t, text, "/nix/store/x-loom/bin/loom work board", role)
	}
	assert.Contains(t, Protocol(RoleSupervisor, "loom"), "# Supervisor protocol")
	assert.Contains(t, Protocol(RoleSupervisor, "loom"), "loom work verify")
	assert.Contains(t, Protocol(RoleWorker, "loom"), "# Worker protocol")
	assert.Contains(t, Protocol(RoleWorker, "loom"), "loom work state ready")
	assert.NotContains(t, Protocol(RoleWorker, "loom"), "loom work verify", "workers never verify")
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./work/ -run TestProtocol -v`
Expected: FAIL (`undefined: Protocol`).

- [ ] **Step 3: Write the protocol files and `Protocol`**

`work/protocol/supervisor.md`:

````markdown
# Supervisor protocol (loom)

You are the **supervisor** of this supervised loom workspace. Every other
session in it is a worker, and loom keeps each one's work state in the
workspace's work log. You don't implement. You own priority, coordination,
verification and issue housekeeping, and you route decisions to the user.

## The board

Run `{{loom}} work board` before you plan new work and before each landing.
It lists every session with its message address, its declared state and
summary, the HEAD a ready worker recorded, whether that HEAD has moved
(stale) or landed, and your logged decisions. `{{loom}} work board <title>`
shows one worker in full: its brief, report and push command.

Message a worker by the address the board shows. Messages carry content
(briefs, reports, questions), never status: status lives on the board.

## Before you plan work

Ask whether the work is *necessary* for the goal, not merely complete.
Check its file overlap with active workers, any shared versions, schemas or
locks, and the host's load.

## Briefs

Write each worker's brief to `$LOOM_WORK_DIR/briefs/<title>.md`. Then ask
the user to start a session with that title and the one-line prompt
`Read your brief at <path>, then start.` A brief states:

- why the work matters;
- the code locations, checked at a named commit;
- the invariants to keep;
- how to verify it, naming the tests that must actually run, not skip;
- coordination: which files other workers own, the landing order, and who
  bumps shared versions;
- hypotheses, labelled as hypotheses.

## Decisions

Make routine calls yourself and log each one with
`{{loom}} work note "<decision>"`: landing order, who does what, test and
doc details, and issue housekeeping. Escalate to the user, through
`AskUserQuestion`:

- scope and priorities;
- anything irreversible or outward-facing;
- anything that changes measurements or data.

Always include a recommendation, and batch related questions into one
prompt.

## Landing gate

Before `{{loom}} work verify <title> "<notes>"`, confirm that:

- the branch fast-forwards the current base;
- the changes stay in scope;
- no lock or toolchain bump is included unless assigned;
- you have read the invariant at risk in the diff;
- the worker's report says which tests actually ran.

`verify` refuses a worker whose branch moved after it declared ready. The
user pushes; the board shows the push command for a verified worker. Land
branches one at a time, in order, and check CI after each one. Then tell
the next worker to merge the new base. Merging moves its HEAD, so it
re-runs its checks and declares ready again.

## Shared resources

Each shared version or lock has one owner. When the host misbehaves,
investigate (journal, process groups) before blaming another session.

## Noise

No acknowledgement-only messages. Relay only what changes another
session's work.
````

`work/protocol/worker.md`:

````markdown
# Worker protocol (loom)

You are a **worker** in a supervised loom workspace. The workspace's main
session supervises: it briefs you, verifies your branch and decides the
landing order. Loom keeps your work state in the workspace's work log.
Declare it with `{{loom}} work state`, and keep it current.

## Start

Your first prompt points at your brief, or is the user's own request. Read
the brief, then run `{{loom}} work state working "<plan in one line>"` and
stay within the brief. After a restart or a compaction, read the brief
again.

## Blocked

Run `{{loom}} work state blocked "<on what>"` when you are waiting on a
decision, another landing or the host. Put questions for the user through
`AskUserQuestion`, and send questions about coordination to the supervisor.

## Ready

When your checks pass, write a report covering:

- your branch and HEAD;
- the files you touched;
- which tests ran and which were skipped;
- the evidence;
- any judgement calls;
- every question you put to the user, with its answer.

Then run `{{loom}} work state ready "<summary>" --report <file>`. It
records your branch tip, so commit first. Send the supervisor the report as
one message, using the address `{{loom}} work board` shows. If the message
can't reach the supervisor, the report on the board stands: tell the user.

## Never

Push to the base branch, or bump a lock or toolchain file unless assigned.

## Resources

Run builds in the foreground with limited jobs, detach long jobs fully,
run heavy analysis under a memory cap, and kill only processes you started.

## Hypotheses

Hypotheses in the brief are hypotheses. Check them before relying on them,
and report any corrections.
````

`work/protocol.go`:

```go
package work

import (
	_ "embed"
	"strings"
)

//go:embed protocol/supervisor.md
var supervisorProtocol string

//go:embed protocol/worker.md
var workerProtocol string

// Protocol returns the role's protocol, with {{loom}} replaced by the CLI
// to run. It is the part of a supervised session's appended system prompt
// that makes the session a supervisor or a worker.
func Protocol(role Role, loom string) string {
	text := workerProtocol
	if role == RoleSupervisor {
		text = supervisorProtocol
	}
	return strings.ReplaceAll(text, "{{loom}}", loom)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./work/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w work/protocol.go work/protocol_test.go
git add work/protocol.go work/protocol_test.go work/protocol/
git commit -m "feat(work): embed the supervisor and worker protocols

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: The board: sessions, derived states and rendering

**Files:**
- Create: `work/board.go`
- Test: `work/board_test.go`

- [ ] **Step 1: Write the failing test**

`work/board_test.go` (it uses `st`, `verify` and `t0` from `fold_test.go`):

```go
//go:build !windows

package work

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// commitT commits a new file on the checked-out branch and returns the commit.
func commitT(t *testing.T, dir, name string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644))
	gitT(t, dir, "add", name)
	gitT(t, dir, "commit", "-q", "-m", "add "+name)
	return gitT(t, dir, "rev-parse", "HEAD")
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "config", "user.email", "t@example.com")
	gitT(t, dir, "config", "user.name", "T")
	commitT(t, dir, "README.md")
}

// originRepo is a repo whose main is pushed to a bare origin, like a
// workspace a user cloned.
func originRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	initRepo(t, repo)
	gitT(t, tmp, "init", "-q", "--bare", "origin.git")
	gitT(t, repo, "remote", "add", "origin", filepath.Join(tmp, "origin.git"))
	gitT(t, repo, "push", "-q", "origin", "main")
	return repo
}

func TestSessions(t *testing.T) {
	cfg := t.TempDir()
	got, err := Sessions(cfg)
	require.NoError(t, err)
	assert.Empty(t, got, "no state.json yet")

	data, err := json.Marshal(map[string]any{"instances": []any{
		map[string]any{"title": "kermit", "path": "/r", "is_workspace_terminal": true},
		map[string]any{"title": "fix-ci", "path": "/r", "branch": "u/fix-ci",
			"worktree": map[string]any{"repo_path": "/r", "branch_name": "u/fix-ci", "base_commit_sha": "abc"}},
		map[string]any{"title": "old", "path": "/r", "branch": "u/old"},
		"not an object",
	}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfg, config.StateFileName), data, 0o644))

	got, err = Sessions(cfg)
	require.NoError(t, err)
	assert.Equal(t, []Session{
		{Title: "kermit", Main: true, RepoPath: "/r"},
		{Title: "fix-ci", Branch: "u/fix-ci", RepoPath: "/r", BaseCommit: "abc"},
		{Title: "old", Branch: "u/old", RepoPath: "/r"},
	}, got)
}

func TestBaseBranch(t *testing.T) {
	cfg := t.TempDir()
	assert.Empty(t, BaseBranch(cfg))
	require.NoError(t, os.WriteFile(filepath.Join(cfg, config.ConfigFileName), []byte(`{"base_branch":"develop"}`), 0o644))
	assert.Equal(t, "develop", BaseBranch(cfg))
}

func TestOriginBase(t *testing.T) {
	ref, branch, ok := OriginBase(originRepo(t), "", nil)
	require.True(t, ok)
	assert.Equal(t, "origin/main", ref)
	assert.Equal(t, "main", branch)

	local := filepath.Join(t.TempDir(), "local")
	initRepo(t, local)
	_, _, ok = OriginBase(local, "", nil)
	assert.False(t, ok, "a repo without origin has nothing to land on")
}

func TestLanded(t *testing.T) {
	repo := originRepo(t)
	base := gitT(t, repo, "rev-parse", "main")
	assert.False(t, Landed(repo, base, base, "origin/main", nil),
		"a branch with no commits past its base never landed, though git calls it an ancestor")

	gitT(t, repo, "checkout", "-q", "-b", "fix-ci")
	head := commitT(t, repo, "fix.txt")
	assert.False(t, Landed(repo, head, base, "origin/main", nil), "not pushed yet")

	gitT(t, repo, "push", "-q", "origin", "fix-ci:main")
	assert.True(t, Landed(repo, head, base, "origin/main", nil))
	assert.False(t, Landed(repo, head, "", "origin/main", nil), "an unknown base is never landed")
}

func TestBuildBoard(t *testing.T) {
	repo := originRepo(t)
	base := gitT(t, repo, "rev-parse", "main")
	gitT(t, repo, "checkout", "-q", "-b", "fix-ci")
	verifiedHead := commitT(t, repo, "fix.txt")
	gitT(t, repo, "checkout", "-q", "-b", "docs", "main")
	readyHead := commitT(t, repo, "docs.txt")
	commitT(t, repo, "docs2.txt") // docs moved after it declared ready
	gitT(t, repo, "checkout", "-q", "main")

	dir := Dir(filepath.Join(repo, ".loom"))
	require.NoError(t, os.MkdirAll(filepath.Dir(BriefPath(dir, "fix-ci")), 0o755))
	require.NoError(t, os.WriteFile(BriefPath(dir, "fix-ci"), []byte("brief"), 0o644))

	f := Fold([]Entry{
		st("fix-ci", StateReady, verifiedHead),
		verify("fix-ci", verifiedHead),
		st("docs", StateReady, readyHead),
		st("gone", StateWorking, ""),
		{V: FormatVersion, At: t0, By: "kermit", Role: RoleSupervisor, Kind: KindNote, Text: "fix-ci lands first"},
	})
	sessions := []Session{
		{Title: "fix-ci", Branch: "fix-ci", RepoPath: repo, BaseCommit: base},
		{Title: "kermit", Main: true, RepoPath: repo},
		{Title: "docs", Branch: "docs", RepoPath: repo, BaseCommit: base},
		{Title: "idle", Branch: "idle", RepoPath: repo, BaseCommit: base},
	}
	ws := config.Workspace{Name: "kermit", Path: repo, Mode: config.ModeSupervised}

	b := BuildBoard(ws, dir, f, sessions, nil)

	require.Len(t, b.Rows, 4, "the gone session's entry is left out")
	assert.Equal(t, "kermit", b.Rows[0].Address, "the main session heads the board")
	assert.True(t, b.Rows[0].Main)
	assert.Equal(t, "default", b.Account)
	require.Len(t, b.Notes, 1)

	docs, ok := b.Row("docs")
	require.True(t, ok)
	assert.Equal(t, "kermit/docs", docs.Address)
	assert.True(t, docs.Stale)
	assert.Empty(t, docs.Push)

	fix, ok := b.Row("fix-ci")
	require.True(t, ok)
	assert.Equal(t, StateVerified, fix.State)
	assert.False(t, fix.Stale)
	assert.Equal(t, "git push origin "+verifiedHead+":refs/heads/main", fix.Push)
	assert.Equal(t, BriefPath(dir, "fix-ci"), fix.Brief)

	idle, ok := b.Row("idle")
	require.True(t, ok)
	assert.Empty(t, idle.State, "a session that never declared a state has none")

	var out bytes.Buffer
	require.NoError(t, b.Write(&out, t0.Add(5*time.Minute)))
	text := out.String()
	assert.Contains(t, text, "kermit/docs")
	assert.Contains(t, text, "ready (stale)")
	assert.Contains(t, text, "supervisor")
	assert.Contains(t, text, "fix-ci lands first")

	out.Reset()
	require.NoError(t, fix.Write(&out, t0.Add(5*time.Minute)))
	assert.Contains(t, out.String(), "git push origin "+verifiedHead)
}

func TestBuildBoard_LandedTrumpsTheDeclaredState(t *testing.T) {
	repo := originRepo(t)
	base := gitT(t, repo, "rev-parse", "main")
	gitT(t, repo, "checkout", "-q", "-b", "fix-ci")
	head := commitT(t, repo, "fix.txt")
	gitT(t, repo, "push", "-q", "origin", "fix-ci:main")

	f := Fold([]Entry{st("fix-ci", StateReady, head), verify("fix-ci", head)})
	b := BuildBoard(config.Workspace{Name: "kermit", Path: repo}, Dir(filepath.Join(repo, ".loom")), f,
		[]Session{{Title: "fix-ci", Branch: "fix-ci", RepoPath: repo, BaseCommit: base}}, nil)
	r, ok := b.Row("fix-ci")
	require.True(t, ok)
	assert.True(t, r.Landed)
	assert.Empty(t, r.Push, "nothing left to push")
	assert.Equal(t, "landed", r.label())
}

func TestOneLine(t *testing.T) {
	assert.Equal(t, "a[31m b", oneLine("a\x1b[31m\nb", 80), "control characters never reach a terminal")
	assert.Equal(t, "abcd…", oneLine("abcdefgh", 5))
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./work/ -run 'TestSessions|TestBaseBranch|TestOriginBase|TestLanded|TestBuildBoard|TestOneLine' -v`
Expected: FAIL (`undefined: Sessions`, `undefined: BuildBoard`, …).

- [ ] **Step 3: Implement**

`work/board.go`:

```go
package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/git"
)

// Session is what the board needs to know about one loom instance.
type Session struct {
	Title      string
	Main       bool
	Branch     string
	RepoPath   string
	BaseCommit string
}

// Sessions reads the instances in <configDir>/state.json. It only reads:
// config.LoadStateFrom would move a corrupt file aside, which an agent's
// command must never do to loom's state.
func Sessions(configDir string) ([]Session, error) {
	data, err := os.ReadFile(filepath.Join(configDir, config.StateFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("work: read loom state: %w", err)
	}
	var doc struct {
		Instances []json.RawMessage `json:"instances"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("work: parse loom state: %w", err)
	}
	var out []Session
	for _, raw := range doc.Instances {
		var rec struct {
			Title    string `json:"title"`
			Path     string `json:"path"`
			Branch   string `json:"branch"`
			Main     bool   `json:"is_workspace_terminal"`
			Worktree struct {
				RepoPath      string `json:"repo_path"`
				BranchName    string `json:"branch_name"`
				BaseCommitSHA string `json:"base_commit_sha"`
			} `json:"worktree"`
		}
		if json.Unmarshal(raw, &rec) != nil || rec.Title == "" {
			continue // loom keeps a record it can't decode; the board skips it
		}
		s := Session{Title: rec.Title, Main: rec.Main, Branch: rec.Worktree.BranchName,
			RepoPath: rec.Worktree.RepoPath, BaseCommit: rec.Worktree.BaseCommitSHA}
		if s.Branch == "" {
			s.Branch = rec.Branch
		}
		if s.RepoPath == "" {
			s.RepoPath = rec.Path
		}
		out = append(out, s)
	}
	return out, nil
}

// FindSession returns the session titled title.
func FindSession(sessions []Session, title string) (Session, bool) {
	for _, s := range sessions {
		if s.Title == title {
			return s, true
		}
	}
	return Session{}, false
}

// BaseBranch reads base_branch from <configDir>/config.json, read-only like
// Sessions. It returns "" when the value is unset or unreadable, which
// leaves the choice to git.ResolveBaseCommit.
func BaseBranch(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, config.ConfigFileName))
	if err != nil {
		return ""
	}
	var cfg struct {
		BaseBranch string `json:"base_branch"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	return cfg.BaseBranch
}

// Tip is the commit a session's branch points at.
func Tip(s Session, runner git.CommandRunner) (string, bool) {
	if s.RepoPath == "" || s.Branch == "" {
		return "", false
	}
	return git.ResolveRef(s.RepoPath, "refs/heads/"+s.Branch, runner)
}

// OriginBase returns the remote-tracking ref (origin/<name>) of the branch
// loom cuts worktrees from, and that branch's name. ok is false when the
// repo has no such ref.
func OriginBase(repo, configured string, runner git.CommandRunner) (ref, branch string, ok bool) {
	_, name, err := git.ResolveBaseCommit(repo, configured, runner)
	if err != nil || name == "HEAD" {
		return "", "", false
	}
	branch = strings.TrimPrefix(name, "origin/")
	ref = "origin/" + branch
	if _, found := git.ResolveRef(repo, "refs/remotes/"+ref, runner); !found {
		return "", "", false
	}
	return ref, branch, true
}

// Landed reports whether head has reached originRef. head was recorded by
// a session whose branch started at base, and it counts only once it has
// commits past base. A fresh branch's tip is its base commit, which git
// counts as its own ancestor, so without that condition every new worker
// would read as landed. An unknown base never counts as landed.
func Landed(repo, head, base, originRef string, runner git.CommandRunner) bool {
	if head == "" || base == "" || originRef == "" {
		return false
	}
	if noCommits, err := git.IsAncestor(repo, head, base, runner); err != nil || noCommits {
		return false
	}
	in, err := git.IsAncestor(repo, head, originRef, runner)
	return err == nil && in
}

// Row is one session on the board.
type Row struct {
	Title string `json:"title"`
	// Address is the name other sessions message it by.
	Address string    `json:"address"`
	Main    bool      `json:"main,omitempty"`
	State   State     `json:"state,omitempty"`
	Summary string    `json:"summary,omitempty"`
	At      time.Time `json:"at,omitempty"`
	Head    string    `json:"head,omitempty"`
	// Stale: the branch moved after ready or verified.
	Stale bool `json:"stale,omitempty"`
	// Landed: the recorded head reached the base branch's origin ref.
	Landed bool   `json:"landed,omitempty"`
	Report string `json:"report,omitempty"`
	Brief  string `json:"brief,omitempty"`
	// Push lands exactly the verified commit, and git refuses it if the
	// base has moved since.
	Push  string `json:"push,omitempty"`
	Notes string `json:"notes,omitempty"`
}

// Board is a supervised workspace's sessions and decisions.
type Board struct {
	Workspace string `json:"workspace"`
	Account   string `json:"account"`
	Rows      []Row  `json:"rows"`
	Notes     []Note `json:"notes"`
	Skipped   int    `json:"skipped,omitempty"`
}

// BuildBoard joins the folded log with loom's sessions and git, listing the
// main session first and then the workers by title. Log entries for a
// title loom no longer has are left out.
func BuildBoard(ws config.Workspace, dir string, f Folded, sessions []Session, runner git.CommandRunner) Board {
	b := Board{Workspace: ws.Name, Account: ws.Account, Notes: f.Notes, Skipped: f.Skipped}
	if b.Account == "" {
		b.Account = "default"
	}
	var originRef, baseBranch string
	if needsGit(f, sessions) {
		originRef, baseBranch, _ = OriginBase(ws.Path, BaseBranch(filepath.Dir(dir)), runner)
	}
	for _, s := range sessions {
		b.Rows = append(b.Rows, buildRow(ws.Name, dir, s, f.Items[s.Title], originRef, baseBranch, runner))
	}
	sort.SliceStable(b.Rows, func(i, j int) bool {
		if b.Rows[i].Main != b.Rows[j].Main {
			return b.Rows[i].Main
		}
		return b.Rows[i].Title < b.Rows[j].Title
	})
	return b
}

// needsGit reports whether a live worker is ready or verified, the only
// states checked for staleness and landing.
func needsGit(f Folded, sessions []Session) bool {
	for _, s := range sessions {
		if it := f.Items[s.Title]; it != nil && !s.Main && (it.State == StateReady || it.State == StateVerified) {
			return true
		}
	}
	return false
}

func buildRow(workspace, dir string, s Session, it *Item, originRef, baseBranch string, runner git.CommandRunner) Row {
	role := RoleWorker
	if s.Main {
		role = RoleSupervisor
	}
	r := Row{Title: s.Title, Address: SessionName(workspace, s.Title, role), Main: s.Main}
	if p := BriefPath(dir, s.Title); fileExists(p) {
		r.Brief = p
	}
	if it == nil || s.Main {
		return r
	}
	r.State, r.Summary, r.At, r.Head, r.Report, r.Notes = it.State, it.Summary, it.At, it.Head, it.Report, it.Notes
	if it.State != StateReady && it.State != StateVerified {
		return r
	}
	if tip, ok := Tip(s, runner); ok {
		r.Stale = tip != it.Head
	}
	r.Landed = Landed(s.RepoPath, it.Head, s.BaseCommit, originRef, runner)
	if it.State == StateVerified && !r.Landed && baseBranch != "" {
		r.Push = "git push origin " + it.Head + ":refs/heads/" + baseBranch
	}
	return r
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Row returns the row of the session titled title.
func (b Board) Row(title string) (Row, bool) {
	for _, r := range b.Rows {
		if r.Title == title {
			return r, true
		}
	}
	return Row{}, false
}

// maxNotes is how many decisions the board's text shows, latest last.
const maxNotes = 10

// Write renders the board as text for a terminal or an agent.
func (b Board) Write(w io.Writer, now time.Time) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: supervised; sessions share the %s account\n\n", b.Workspace, b.Account)
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ADDRESS\tSTATE\tAGE\tSUMMARY")
	for _, r := range b.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Address, r.label(), age(now, r.At), oneLine(r.Summary, 72))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if n := len(b.Notes); n > 0 {
		shown := b.Notes
		if n > maxNotes {
			shown = b.Notes[n-maxNotes:]
		}
		fmt.Fprintf(&sb, "\nDecisions (latest %d of %d):\n", len(shown), n)
		for _, note := range shown {
			fmt.Fprintf(&sb, "  %s  %s\n", note.At.Local().Format("Jan 2 15:04"), oneLine(note.Text, 100))
		}
	}
	if b.Skipped > 0 {
		fmt.Fprintf(&sb, "\n%d log entries broke the rules and were skipped.\n", b.Skipped)
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

// Write renders one session in full.
func (r Row) Write(w io.Writer, now time.Time) error {
	var sb strings.Builder
	sb.WriteString(r.Title + "\n")
	field := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&sb, "  %-8s %s\n", name, value)
		}
	}
	state := r.label()
	switch a := age(now, r.At); a {
	case "-":
	case "now":
		state += " (just now)"
	default:
		state += " (" + a + " ago)"
	}
	field("address", r.Address)
	field("state", state)
	field("summary", oneLine(r.Summary, 500))
	field("head", r.Head)
	field("notes", oneLine(r.Notes, 500))
	field("report", r.Report)
	field("brief", r.Brief)
	field("push", r.Push)
	_, err := io.WriteString(w, sb.String())
	return err
}

// label is the state column. The main session supervises; a landed head
// takes precedence over the state it was declared in; a moved branch
// marks the state stale.
func (r Row) label() string {
	switch {
	case r.Main:
		return "supervisor"
	case r.State == "":
		return "-"
	case r.Landed:
		return "landed"
	case r.Stale:
		return string(r.State) + " (stale)"
	}
	return string(r.State)
}

// age is roughly how long ago at was.
func age(now, at time.Time) string {
	if at.IsZero() {
		return "-"
	}
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// oneLine collapses s onto one line of at most n runes, without control
// characters: summaries come from agents and end up in a terminal.
func oneLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.Join(strings.Fields(s), " "))
	if runes := []rune(s); len(runes) > n {
		return string(runes[:n-1]) + "…"
	}
	return s
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./work/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w work/board.go work/board_test.go
git add work/board.go work/board_test.go
git commit -m "feat(work): build the board with derived landed and stale states

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: The agent CLI: `loom work`

**Files:**
- Create: `cmd/work.go`
- Modify: `main.go` (the `init()` function's `rootCmd.AddCommand` calls)
- Test: `cmd/work_test.go`

- [ ] **Step 1: Write the failing test**

`cmd/work_test.go`:

```go
//go:build !windows

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/work"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func commitIn(t *testing.T, dir, name string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644))
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-q", "-m", "add "+name)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// supervisedRepo registers kermit, supervised, at a repo pushed to a bare
// origin, whose loom state lists the main session and a worker fix-ci on
// its own branch. It returns the repo and the work folder.
func supervisedRepo(t *testing.T) (repo, dir string) {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	tmp := t.TempDir()
	repo = filepath.Join(tmp, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "config", "user.email", "t@example.com")
	gitIn(t, repo, "config", "user.name", "T")
	base := commitIn(t, repo, "README.md")
	gitIn(t, tmp, "init", "-q", "--bare", "origin.git")
	gitIn(t, repo, "remote", "add", "origin", filepath.Join(tmp, "origin.git"))
	gitIn(t, repo, "push", "-q", "origin", "main")
	gitIn(t, repo, "branch", "fix-ci")

	require.NoError(t, config.SaveWorkspaceRegistry(&config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "kermit", Path: repo, Mode: config.ModeSupervised}}}))
	cfgDir := filepath.Join(repo, ".loom")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	state, err := json.Marshal(map[string]any{"instances": []any{
		map[string]any{"title": "kermit", "path": repo, "is_workspace_terminal": true},
		map[string]any{"title": "fix-ci", "path": repo, "branch": "fix-ci",
			"worktree": map[string]any{"repo_path": repo, "branch_name": "fix-ci", "base_commit_sha": base}},
	}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), state, 0o644))
	return repo, work.Dir(cfgDir)
}

// as makes the following commands run as the session titled title.
func as(t *testing.T, title string, role work.Role, dir string) {
	t.Helper()
	t.Setenv(work.EnvInstance, title)
	t.Setenv(work.EnvRole, string(role))
	t.Setenv(work.EnvWorkDir, dir)
}

func runWork(t *testing.T, args ...string) (string, error) {
	t.Helper()
	workReportFlag, workJSONFlag = "", false
	var out bytes.Buffer
	WorkCmd.SetOut(&out)
	WorkCmd.SetErr(&out)
	WorkCmd.SetArgs(args)
	_, err := WorkCmd.ExecuteC()
	return out.String(), err
}

// readyReport commits on fix-ci and writes a report, as a worker would
// before declaring ready. It returns the new tip and the report's path.
func readyReport(t *testing.T, repo, file string) (tip, report string) {
	t.Helper()
	gitIn(t, repo, "checkout", "-q", "fix-ci")
	tip = commitIn(t, repo, file)
	gitIn(t, repo, "checkout", "-q", "main")
	report = filepath.Join(t.TempDir(), "report.md")
	require.NoError(t, os.WriteFile(report, []byte("tests: all ran"), 0o644))
	return tip, report
}

func TestWork_RefusesOutsideASupervisedSession(t *testing.T) {
	as(t, "", "", "")
	_, err := runWork(t, "state", "working", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), work.EnvInstance)
}

func TestWork_RefusesOnceTheWorkspaceIsNormal(t *testing.T) {
	_, dir := supervisedRepo(t)
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.SetMode("kermit", config.ModeNormal, nil))
	as(t, "fix-ci", work.RoleWorker, dir)
	_, err = runWork(t, "state", "working", "plan")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no longer supervised")
}

func TestWork_WorkerStateShowsOnTheBoard(t *testing.T) {
	_, dir := supervisedRepo(t)
	as(t, "fix-ci", work.RoleWorker, dir)
	out, err := runWork(t, "state", "working", "make CI green")
	require.NoError(t, err)
	assert.Equal(t, "fix-ci is working: make CI green\n", out)

	out, err = runWork(t, "board")
	require.NoError(t, err)
	assert.Contains(t, out, "kermit/fix-ci")
	assert.Contains(t, out, "make CI green")
}

func TestWork_ReadyRecordsTheTipAndTheReport(t *testing.T) {
	repo, dir := supervisedRepo(t)
	tip, report := readyReport(t, repo, "fix.txt")
	as(t, "fix-ci", work.RoleWorker, dir)

	_, err := runWork(t, "state", "ready", "CI green")
	require.Error(t, err, "ready needs a report")

	_, err = runWork(t, "state", "ready", "CI green", "--report", report)
	require.NoError(t, err)
	entries, _, err := work.Read(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, tip, entries[0].Head)
	saved, err := os.ReadFile(work.ReportPath(dir, "fix-ci"))
	require.NoError(t, err)
	assert.Equal(t, "tests: all ran", string(saved))
}

func TestWork_SupervisorCannotDeclareAState(t *testing.T) {
	_, dir := supervisedRepo(t)
	as(t, "kermit", work.RoleSupervisor, dir)
	_, err := runWork(t, "state", "working", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only a worker")
}

func TestWork_VerifyRefusesAMovedBranch(t *testing.T) {
	repo, dir := supervisedRepo(t)
	_, report := readyReport(t, repo, "fix.txt")
	as(t, "fix-ci", work.RoleWorker, dir)
	_, err := runWork(t, "state", "ready", "done", "--report", report)
	require.NoError(t, err)

	moved, _ := readyReport(t, repo, "more.txt") // the branch moves on after ready
	as(t, "kermit", work.RoleSupervisor, dir)
	_, err = runWork(t, "verify", "fix-ci", "looks right")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "moved since it declared ready")

	as(t, "fix-ci", work.RoleWorker, dir)
	_, err = runWork(t, "state", "ready", "done again", "--report", report)
	require.NoError(t, err)
	as(t, "kermit", work.RoleSupervisor, dir)
	out, err := runWork(t, "verify", "fix-ci", "looks right")
	require.NoError(t, err)
	assert.Equal(t, "fix-ci is verified at "+moved[:12]+"\n", out)

	out, err = runWork(t, "board", "fix-ci")
	require.NoError(t, err)
	assert.Contains(t, out, "git push origin "+moved+":refs/heads/main")
}

func TestWork_WorkersCannotVerifyOrNote(t *testing.T) {
	_, dir := supervisedRepo(t)
	as(t, "fix-ci", work.RoleWorker, dir)
	_, err := runWork(t, "note", "land me first")
	assert.Error(t, err)
	_, err = runWork(t, "verify", "fix-ci", "self-review")
	assert.Error(t, err)
}

func TestWork_NotesReachTheBoardAsJSON(t *testing.T) {
	_, dir := supervisedRepo(t)
	as(t, "kermit", work.RoleSupervisor, dir)
	_, err := runWork(t, "note", "fix-ci lands before docs")
	require.NoError(t, err)
	out, err := runWork(t, "board", "--json")
	require.NoError(t, err)
	var b work.Board
	require.NoError(t, json.Unmarshal([]byte(out), &b))
	require.Len(t, b.Notes, 1)
	assert.Equal(t, "fix-ci lands before docs", b.Notes[0].Text)
}

// The user reads the board from any shell in the repo, where loom exports
// no session variables.
func TestWork_BoardFromAShellInTheRepo(t *testing.T) {
	repo, _ := supervisedRepo(t)
	as(t, "", "", "")
	t.Chdir(repo)
	out, err := runWork(t, "board")
	require.NoError(t, err)
	assert.Contains(t, out, "kermit/fix-ci")
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./cmd/ -run TestWork -v`
Expected: FAIL to build (`undefined: WorkCmd`, `undefined: workReportFlag`, …).

- [ ] **Step 3: Implement the CLI**

`cmd/work.go`:

```go
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/work"
	"github.com/spf13/cobra"
)

// WorkCmd is `loom work`: the commands the sessions of a supervised
// workspace use to declare and read coordination state. Loom exports the
// variables they need into every supervised session, and the commands
// refuse without them. The one exception is `board`, which reads and so
// also works from a shell inside the workspace's repo.
var WorkCmd = &cobra.Command{
	Use:   "work",
	Short: "Declare and read a supervised workspace's work state (run by its sessions)",
}

var (
	workReportFlag string
	workJSONFlag   bool
)

// workCaller is the session a `loom work` command runs for.
type workCaller struct {
	title     string
	role      work.Role
	dir       string // the workspace's work folder
	configDir string
	ws        config.Workspace
}

// loadWorkCaller reads the variables loom exports into a supervised session
// and checks that the session's workspace is still supervised.
func loadWorkCaller() (workCaller, error) {
	c := workCaller{
		title: os.Getenv(work.EnvInstance),
		role:  work.Role(os.Getenv(work.EnvRole)),
		dir:   os.Getenv(work.EnvWorkDir),
	}
	if c.title == "" || c.dir == "" || (c.role != work.RoleSupervisor && c.role != work.RoleWorker) {
		return c, fmt.Errorf("loom work runs only in a session of a supervised workspace: %s, %s and %s are not all set",
			work.EnvInstance, work.EnvRole, work.EnvWorkDir)
	}
	c.configDir = filepath.Dir(c.dir)
	ws, err := supervisedWorkspace(func(reg *config.WorkspaceRegistry) *config.Workspace {
		return reg.FindByConfigDir(c.configDir)
	})
	if err != nil {
		return c, err
	}
	c.ws = ws
	return c, nil
}

// supervisedWorkspace loads the registry and returns the workspace find
// picks, provided it is supervised.
func supervisedWorkspace(find func(*config.WorkspaceRegistry) *config.Workspace) (config.Workspace, error) {
	reg, err := config.LoadWorkspaceRegistry()
	if err != nil {
		return config.Workspace{}, err
	}
	ws := find(reg)
	if ws == nil {
		return config.Workspace{}, errors.New("no registered workspace matches this session or directory")
	}
	if ws.Mode != config.ModeSupervised {
		return config.Workspace{}, fmt.Errorf("workspace %q is no longer supervised (loom workspace mode %s supervised turns it back on)", ws.Name, ws.Name)
	}
	return *ws, nil
}

func (c workCaller) entry(kind work.Kind) work.Entry {
	return work.Entry{V: work.FormatVersion, At: time.Now().UTC(), By: c.title, Role: c.role, Kind: kind}
}

// session returns loom's record of the session titled title.
func (c workCaller) session(title string) (work.Session, error) {
	sessions, err := work.Sessions(c.configDir)
	if err != nil {
		return work.Session{}, err
	}
	s, ok := work.FindSession(sessions, title)
	if !ok {
		return work.Session{}, fmt.Errorf("loom has no session titled %q in workspace %q", title, c.ws.Name)
	}
	return s, nil
}

var workStateCmd = &cobra.Command{
	Use:   "state <working|blocked|ready> <summary>",
	Short: "Declare this session's work state (workers)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := loadWorkCaller()
		if err != nil {
			return err
		}
		if c.role != work.RoleWorker {
			return errors.New("only a worker declares a work state; the supervisor verifies with `loom work verify`")
		}
		e := c.entry(work.KindState)
		e.State, e.Summary = work.State(args[0]), args[1]
		if e.State == work.StateReady {
			if e.Head, e.Report, err = c.readyFacts(workReportFlag); err != nil {
				return err
			}
		}
		if err := work.Append(c.dir, func(existing []work.Entry) (work.Entry, error) {
			return e, work.Validate(work.Fold(existing), e)
		}); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s is %s: %s\n", c.title, e.State, e.Summary)
		return nil
	},
}

// readyFacts gathers what ready records: the branch tip, and a copy of the
// report in the work folder, where the supervisor and the user read it.
func (c workCaller) readyFacts(report string) (head, saved string, err error) {
	if report == "" {
		return "", "", errors.New("ready needs --report <file>: write your report first")
	}
	s, err := c.session(c.title)
	if err != nil {
		return "", "", err
	}
	head, ok := work.Tip(s, nil)
	if !ok {
		return "", "", fmt.Errorf("can't read the tip of branch %q", s.Branch)
	}
	body, err := os.ReadFile(report)
	if err != nil {
		return "", "", fmt.Errorf("read report: %w", err)
	}
	saved = work.ReportPath(c.dir, c.title)
	if err := os.MkdirAll(filepath.Dir(saved), 0o755); err != nil {
		return "", "", fmt.Errorf("save report: %w", err)
	}
	if err := config.AtomicWriteFile(saved, body, 0o644); err != nil {
		return "", "", fmt.Errorf("save report: %w", err)
	}
	return head, saved, nil
}

var workVerifyCmd = &cobra.Command{
	Use:   "verify <title> <notes>",
	Short: "Mark a ready worker verified at the HEAD it reported (supervisor)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := loadWorkCaller()
		if err != nil {
			return err
		}
		target := args[0]
		s, err := c.session(target)
		if err != nil {
			return err
		}
		tip, ok := work.Tip(s, nil)
		if !ok {
			return fmt.Errorf("can't read the tip of %s's branch %q", target, s.Branch)
		}
		var head string
		err = work.Append(c.dir, func(existing []work.Entry) (work.Entry, error) {
			f := work.Fold(existing)
			e := c.entry(work.KindVerify)
			e.Target, e.Notes = target, args[1]
			if it := f.Items[target]; it != nil {
				e.Head = it.Head
				if it.State == work.StateReady && tip != it.Head {
					return e, fmt.Errorf("%s's branch moved since it declared ready (%.12s, now %.12s): ask it to re-run its checks and declare ready again",
						target, it.Head, tip)
				}
			}
			head = e.Head
			return e, work.Validate(f, e)
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s is verified at %.12s\n", target, head)
		return nil
	},
}

var workNoteCmd = &cobra.Command{
	Use:   "note <decision>",
	Short: "Log a routine decision (supervisor)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := loadWorkCaller()
		if err != nil {
			return err
		}
		e := c.entry(work.KindNote)
		e.Text = args[0]
		if err := work.Append(c.dir, func(existing []work.Entry) (work.Entry, error) {
			return e, work.Validate(work.Fold(existing), e)
		}); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "decision logged")
		return nil
	},
}

var workBoardCmd = &cobra.Command{
	Use:   "board [title]",
	Short: "Show every session's work state, or one session in full",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, dir, err := boardWorkspace()
		if err != nil {
			return err
		}
		entries, bad, err := work.Read(dir)
		if err != nil {
			return err
		}
		sessions, err := work.Sessions(filepath.Dir(dir))
		if err != nil {
			return err
		}
		f := work.Fold(entries)
		f.Skipped += bad
		b := work.BuildBoard(ws, dir, f, sessions, nil)
		return writeWorkBoard(cmd.OutOrStdout(), b, args, workJSONFlag, time.Now())
	},
}

// boardWorkspace is the workspace `loom work board` shows: the calling
// session's, or, at a shell where loom exported nothing, the one holding
// the working directory. Reading needs no role.
func boardWorkspace() (config.Workspace, string, error) {
	if os.Getenv(work.EnvWorkDir) != "" {
		c, err := loadWorkCaller()
		return c.ws, c.dir, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return config.Workspace{}, "", fmt.Errorf("failed to get working directory: %w", err)
	}
	ws, err := supervisedWorkspace(func(reg *config.WorkspaceRegistry) *config.Workspace {
		return reg.FindByPath(cwd)
	})
	if err != nil {
		return ws, "", err
	}
	return ws, work.Dir(config.WorkspaceConfigDir(&ws)), nil
}

func writeWorkBoard(w io.Writer, b work.Board, args []string, asJSON bool, now time.Time) error {
	if len(args) == 1 {
		r, ok := b.Row(args[0])
		if !ok {
			return fmt.Errorf("no session titled %q on the board", args[0])
		}
		if asJSON {
			return writeJSON(w, r)
		}
		return r.Write(w, now)
	}
	if asJSON {
		return writeJSON(w, b)
	}
	return b.Write(w, now)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func init() {
	workStateCmd.Flags().StringVar(&workReportFlag, "report", "", "Report file to record with ready")
	workBoardCmd.Flags().BoolVar(&workJSONFlag, "json", false, "Print JSON")
	WorkCmd.AddCommand(workStateCmd, workVerifyCmd, workNoteCmd, workBoardCmd)
}
```

In `main.go` `init()`, after `rootCmd.AddCommand(cmd2.AccountCmd)`, add:

```go
	rootCmd.AddCommand(cmd2.WorkCmd)
```

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/ -run TestWork -v && go build ./...`
Expected: PASS, and the build succeeds.

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/work.go cmd/work_test.go main.go
git add cmd/work.go cmd/work_test.go main.go
git commit -m "feat(cmd): add the loom work agent CLI

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: `loom workspace mode`

**Files:**
- Modify: `cmd/workspace.go` (imports, a new command, `init()`)
- Test: `cmd/workspace_mode_test.go`

- [ ] **Step 1: Write the failing test**

`cmd/workspace_mode_test.go`:

```go
package cmd

import (
	"bytes"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registerKermit(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	require.NoError(t, config.SaveWorkspaceRegistry(&config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "kermit", Path: t.TempDir()}}}))
}

// withAccount registers a Claude account in the global dir.
func withAccount(t *testing.T, name string) {
	t.Helper()
	globalDir, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	_, _, err = account.LoadRegistry(globalDir).Create(name, t.TempDir())
	require.NoError(t, err)
}

func runWorkspace(t *testing.T, args ...string) (string, error) {
	t.Helper()
	workspaceModeAccountFlag = ""
	workspaceModeCmd.Flags().Lookup("account").Changed = false
	var out bytes.Buffer
	WorkspaceCmd.SetOut(&out)
	WorkspaceCmd.SetErr(&out)
	WorkspaceCmd.SetArgs(args)
	_, err := WorkspaceCmd.ExecuteC()
	return out.String(), err
}

func kermit(t *testing.T) config.Workspace {
	t.Helper()
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	ws := reg.Get("kermit")
	require.NotNil(t, ws)
	return *ws
}

func TestWorkspaceMode_Supervised(t *testing.T) {
	registerKermit(t)
	out, err := runWorkspace(t, "mode", "kermit", "supervised")
	require.NoError(t, err)
	assert.Equal(t, config.ModeSupervised, kermit(t).Mode)
	assert.Contains(t, out, "share the default account")
	assert.Contains(t, out, "exit Claude")
}

func TestWorkspaceMode_Account(t *testing.T) {
	registerKermit(t)
	_, err := runWorkspace(t, "mode", "kermit", "supervised", "--account", "personal")
	require.Error(t, err, "an account that doesn't exist")

	withAccount(t, "personal")
	out, err := runWorkspace(t, "mode", "kermit", "supervised", "--account", "personal")
	require.NoError(t, err)
	assert.Equal(t, "personal", kermit(t).Account)
	assert.Contains(t, out, "share the personal account")

	_, err = runWorkspace(t, "mode", "kermit", "normal")
	require.NoError(t, err)
	assert.Equal(t, config.ModeNormal, kermit(t).Mode)
	assert.Equal(t, "personal", kermit(t).Account, "turning supervision off keeps the account for next time")

	_, err = runWorkspace(t, "mode", "kermit", "supervised", "--account", account.DefaultName)
	require.NoError(t, err)
	assert.Empty(t, kermit(t).Account, "the default account is stored as empty")
}

func TestWorkspaceMode_Refusals(t *testing.T) {
	registerKermit(t)
	_, err := runWorkspace(t, "mode", "kermit", "babysit")
	assert.Error(t, err)
	_, err = runWorkspace(t, "mode", "nope", "supervised")
	assert.Error(t, err)
	_, err = runWorkspace(t, "mode", "kermit", "normal", "--account", "personal")
	assert.Error(t, err, "--account only applies to supervised mode")
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./cmd/ -run TestWorkspaceMode -v`
Expected: FAIL to build (`undefined: workspaceModeAccountFlag`, `undefined: workspaceModeCmd`).

- [ ] **Step 3: Implement**

In `cmd/workspace.go`, extend the imports to:

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/work"

	"github.com/spf13/cobra"
)
```

Add, before `func init()`:

```go
var workspaceModeAccountFlag string

var workspaceModeCmd = &cobra.Command{
	Use:   "mode [name] <normal|supervised>",
	Short: "Turn supervision on or off for a workspace",
	Long: "In a supervised workspace the main session supervises and every worktree\n" +
		"session is a worker. Sessions pick the change up at their next launch.\n" +
		"--account sets the Claude account the workspace's sessions share: messages\n" +
		"between sessions on different accounts don't arrive.",
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		mode, err := parseWorkspaceMode(args[len(args)-1])
		if err != nil {
			return err
		}
		reg, err := config.LoadWorkspaceRegistry()
		if err != nil {
			return err
		}
		ws, err := workspaceArg(reg, args[:len(args)-1])
		if err != nil {
			return err
		}
		var acct *string
		if cmd.Flags().Changed("account") {
			if mode != config.ModeSupervised {
				return errors.New("--account applies only to supervised mode")
			}
			name, err := supervisedAccount(workspaceModeAccountFlag)
			if err != nil {
				return err
			}
			acct = &name
		}
		name := ws.Name
		if err := reg.SetMode(name, mode, acct); err != nil {
			return err
		}
		ws = reg.Get(name)
		out := cmd.OutOrStdout()
		if mode == config.ModeNormal {
			fmt.Fprintf(out, "Workspace %q is normal again. Sessions keep their supervised setup until their next launch; the work log stays in %s.\n",
				name, work.Dir(config.WorkspaceConfigDir(ws)))
			return nil
		}
		shared := ws.Account
		if shared == "" {
			shared = account.DefaultName
		}
		fmt.Fprintf(out, "Workspace %q is supervised; its sessions share the %s account.\n", name, shared)
		fmt.Fprintf(out, "Restart its main session to make it the supervisor: exit Claude in the %q session and loom relaunches it. Workers pick the change up at their next launch.\n", name)
		return nil
	},
}

// parseWorkspaceMode maps the CLI's mode words onto config's modes.
func parseWorkspaceMode(s string) (string, error) {
	switch s {
	case "normal":
		return config.ModeNormal, nil
	case "supervised":
		return config.ModeSupervised, nil
	}
	return "", fmt.Errorf("unknown mode %q (want normal or supervised)", s)
}

// workspaceArg is the named workspace, or the one holding the working
// directory when no name is given.
func workspaceArg(reg *config.WorkspaceRegistry, args []string) (*config.Workspace, error) {
	if len(args) == 1 {
		if ws := reg.Get(args[0]); ws != nil {
			return ws, nil
		}
		return nil, fmt.Errorf("workspace %q not found", args[0])
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}
	if ws := reg.FindByPath(cwd); ws != nil {
		return ws, nil
	}
	return nil, errors.New("no workspace matches current directory; specify a name")
}

// supervisedAccount checks --account against the account registry. The
// default account is stored as "".
func supervisedAccount(name string) (string, error) {
	if name == "" || name == account.DefaultName {
		return "", nil
	}
	reg, err := loadAccountRegistry()
	if err != nil {
		return "", err
	}
	if _, ok := reg.Get(name); !ok {
		return "", fmt.Errorf("no account %q (see loom account list)", name)
	}
	return name, nil
}
```

In `init()`, after `WorkspaceCmd.AddCommand(workspaceStatusCmd)`, add:

```go
	workspaceModeCmd.Flags().StringVar(&workspaceModeAccountFlag, "account", "",
		"Claude account the supervised workspace's sessions share (default: the default account)")
	WorkspaceCmd.AddCommand(workspaceModeCmd)
```

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/ -v -run 'TestWorkspaceMode|TestWork'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/workspace.go cmd/workspace_mode_test.go
git add cmd/workspace.go cmd/workspace_mode_test.go
git commit -m "feat(cmd): add loom workspace mode with a shared account

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Hook settings that carry permission and sandbox extras

**Files:**
- Modify: `session/hooks/hooks.go` (`settingsDoc`, `SettingsJSON`, `Prepare`)
- Test: `session/hooks/hooks_extra_test.go`

- [ ] **Step 1: Write the failing test**

`session/hooks/hooks_extra_test.go`:

```go
package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsJSONWith_AddsPermissionsAndSandbox(t *testing.T) {
	got, err := SettingsJSONWith("/cfg/hooks/loom_x/events", Extra{
		Allow:      []string{"Bash(/bin/loom work *)"},
		AllowWrite: []string{"/repo/.loom/work"},
	})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(got, &doc))
	assert.Contains(t, doc, "hooks")
	assert.Equal(t, map[string]any{"allow": []any{"Bash(/bin/loom work *)"}}, doc["permissions"])
	assert.Equal(t, map[string]any{"filesystem": map[string]any{"allowWrite": []any{"/repo/.loom/work"}}}, doc["sandbox"])
}

func TestSettingsJSONWith_ZeroExtraIsTheHooksOnlyFile(t *testing.T) {
	plain, err := SettingsJSON("/e")
	require.NoError(t, err)
	with, err := SettingsJSONWith("/e", Extra{})
	require.NoError(t, err)
	assert.Equal(t, string(plain), string(with))
}

func TestPrepareWith_WritesTheExtras(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loom_x")
	_, err := PrepareWith(dir, Extra{Allow: []string{"Bash(loom work *)"}})
	require.NoError(t, err)
	data, err := os.ReadFile(SettingsPath(dir))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"Bash(loom work *)"`)
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./session/hooks/ -run 'With' -v`
Expected: FAIL (`undefined: SettingsJSONWith`, `undefined: Extra`, `undefined: PrepareWith`).

- [ ] **Step 3: Implement**

In `session/hooks/hooks.go`, replace `type settingsDoc struct { … }` and `SettingsJSON` (with its comment) with:

```go
// Extra is settings a launch adds to loom's hooks file: permission allow
// rules, and paths sandboxed commands may write. The zero value adds
// nothing, so a launch without extras writes exactly the hooks-only file.
type Extra struct {
	// Allow lists permission allow rules (permissions.allow).
	Allow []string
	// AllowWrite lists paths sandboxed shell commands may write
	// (sandbox.filesystem.allowWrite). It doesn't open them to Claude's
	// file tools, unlike permissions.additionalDirectories.
	AllowWrite []string
}

type permissionsDoc struct {
	Allow []string `json:"allow,omitempty"`
}

type sandboxDoc struct {
	Filesystem *filesystemDoc `json:"filesystem,omitempty"`
}

type filesystemDoc struct {
	AllowWrite []string `json:"allowWrite,omitempty"`
}

type settingsDoc struct {
	Hooks       map[string][]hookMatcher `json:"hooks"`
	Permissions *permissionsDoc          `json:"permissions,omitempty"`
	Sandbox     *sandboxDoc              `json:"sandbox,omitempty"`
}

// SettingsJSON returns the settings file registering HookCommand for every
// event in HookEvents. Claude adds these hooks to the user's own.
func SettingsJSON(eventsDir string) ([]byte, error) { return SettingsJSONWith(eventsDir, Extra{}) }

// SettingsJSONWith is SettingsJSON plus extra. Claude merges both
// additions with the user's own settings.
func SettingsJSONWith(eventsDir string, extra Extra) ([]byte, error) {
	cmd := HookCommand(eventsDir)
	doc := settingsDoc{Hooks: make(map[string][]hookMatcher, len(HookEvents))}
	for _, name := range HookEvents {
		doc.Hooks[name] = []hookMatcher{{Hooks: []hookCommand{{Type: "command", Command: cmd}}}}
	}
	if len(extra.Allow) > 0 {
		doc.Permissions = &permissionsDoc{Allow: extra.Allow}
	}
	if len(extra.AllowWrite) > 0 {
		doc.Sandbox = &sandboxDoc{Filesystem: &filesystemDoc{AllowWrite: extra.AllowWrite}}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep the command's > readable
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("hooks: encode settings: %w", err)
	}
	return buf.Bytes(), nil
}
```

Replace `Prepare` (with its comment) with:

```go
// Prepare is PrepareWith with no extras.
func Prepare(dir string) (string, error) { return PrepareWith(dir, Extra{}) }

// PrepareWith empties dir and writes a fresh settings.json (with extra),
// events folder and launch-id for a new launch, returning the launch ID.
// Events from an earlier launch describe agents that no longer exist, so
// nothing is kept. launch-id is written last, so a concurrent scan never
// sees a new ID without its settings.
func PrepareWith(dir string, extra Extra) (string, error) {
	if !SafePath(dir) {
		return "", fmt.Errorf("hooks: hooks folder %q contains a single quote", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("hooks: clear hooks folder: %w", err)
	}
	if err := os.MkdirAll(EventsDir(dir), 0o700); err != nil {
		return "", fmt.Errorf("hooks: create hooks folder: %w", err)
	}
	settings, err := SettingsJSONWith(EventsDir(dir), extra)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(SettingsPath(dir), settings, 0o600); err != nil {
		return "", fmt.Errorf("hooks: write settings: %w", err)
	}
	id, err := newLaunchID()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(launchIDPath(dir), []byte(id), 0o600); err != nil {
		return "", fmt.Errorf("hooks: write launch-id: %w", err)
	}
	return id, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./session/hooks/ -v`
Expected: PASS, including the unchanged `TestSettingsJSON_Golden`.

- [ ] **Step 5: Commit**

```bash
gofmt -w session/hooks/hooks.go session/hooks/hooks_extra_test.go
git add session/hooks/hooks.go session/hooks/hooks_extra_test.go
git commit -m "feat(hooks): let a launch add permission and sandbox settings

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: `ApplyNameFlag` on every adapter

**Files:**
- Modify: `session/agent/adapter.go` (the `Adapter` interface), `session/agent/claude.go`, `session/agent/aider.go`, `session/agent/gemini.go`, `session/agent/default.go`
- Test: `session/agent/adapter_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `session/agent/adapter_test.go`:

```go
func TestApplyNameFlag(t *testing.T) {
	c := Claude()
	assert.Equal(t, "claude --name 'kermit/fix-ci' --model opus", c.ApplyNameFlag("claude --model opus", "kermit/fix-ci"))
	assert.Equal(t, "claude --name mine", c.ApplyNameFlag("claude --name mine", "kermit/x"), "the user's name wins")
	assert.Equal(t, "claude -n mine", c.ApplyNameFlag("claude -n mine", "kermit/x"))
	assert.Equal(t, "claude", c.ApplyNameFlag("claude", ""))
	assert.Equal(t, "claude", c.ApplyNameFlag("claude", "it's"), "a name the quoting can't hold")
	for _, a := range []Adapter{Aider(), Gemini(), Default()} {
		assert.Equal(t, "x", a.ApplyNameFlag("x", "n"), a.Name())
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./session/agent/ -run TestApplyNameFlag -v`
Expected: FAIL (`c.ApplyNameFlag undefined`).

- [ ] **Step 3: Implement**

In `session/agent/adapter.go`, add to the `Adapter` interface, after `ApplySettingsFlag`:

```go
	// ApplyNameFlag returns the program string with the agent's flag for
	// naming the session inserted (e.g. "claude --name 'kermit/fix-ci'").
	// For Claude the name is also the session's cross-session message
	// address. name == "" is a no-op. Returns the input unchanged when a
	// name flag is already present, and for agents without session names.
	ApplyNameFlag(program, name string) string
```

In `session/agent/claude.go`, add after `ApplySettingsFlag`:

```go
// ApplyNameFlag inserts "--name '<name>'" after "claude". The name is
// single-quoted for the same reason as ApplyLoomContextFlag. Returns
// program unchanged when name is empty or holds a single quote, when
// program is empty, or when a --name/-n flag is already present: the
// user's own name wins.
func (claudeAdapter) ApplyNameFlag(program, name string) string {
	if name == "" || strings.ContainsRune(name, '\'') {
		return program
	}
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}
	for _, p := range parts[1:] {
		if p == "--name" || p == "-n" || strings.HasPrefix(p, "--name=") {
			return program
		}
	}
	return insertAfterCommand(program, "--name '"+name+"'")
}
```

Add after `ApplySettingsFlag` in `aider.go`, `gemini.go` and `default.go` respectively:

```go
func (aiderAdapter) ApplyNameFlag(program, _ string) string { return program }
```

```go
func (geminiAdapter) ApplyNameFlag(program, _ string) string { return program }
```

```go
func (defaultAdapter) ApplyNameFlag(program, _ string) string { return program }
```

- [ ] **Step 4: Run the tests**

Run: `go test ./session/agent/ -v && go build ./...`
Expected: PASS, and everything builds (nothing else implements `Adapter`).

- [ ] **Step 5: Commit**

```bash
gofmt -w session/agent/*.go
git add session/agent/
git commit -m "feat(agent): add ApplyNameFlag to pin a Claude session's name

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Supervised launches

**Files:**
- Create: `session/supervision.go`
- Modify: `session/agent_restart.go` (`LaunchEnv`, `InstanceEnv`), `session/instance.go` (the `launchWarning` field, `launchEnv`, `Restart`, `Start`), `session/subagent_hooks.go` (`launchProgram`, `recoveryLaunch`, `prepareHooks`), `session/loom_context.go` (`writeContextFile`), `session/subagent_hooks_test.go` (call shape)
- Test: `session/supervision_test.go`

- [ ] **Step 1: Write the failing test**

`session/supervision_test.go`:

```go
package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/work"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// supervisedRegistry registers kermit at a fresh repo, in mode and with
// account, in a throwaway global dir, and returns its config dir.
func supervisedRegistry(t *testing.T, mode, account string) string {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	repo := t.TempDir()
	require.NoError(t, config.SaveWorkspaceRegistry(&config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "kermit", Path: repo, Mode: mode, Account: account}}}))
	cfgDir := filepath.Join(repo, ".loom")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	return cfgDir
}

func newSupervisionInstance(t *testing.T, cfgDir, title string, main bool) *Instance {
	t.Helper()
	inst, err := NewInstance(InstanceOptions{Title: title, Path: filepath.Dir(cfgDir), Program: "claude",
		ConfigDir: cfgDir, IsWorkspaceTerminal: main})
	require.NoError(t, err)
	return inst
}

func TestLaunch_NormalWorkspaceIsUnchanged(t *testing.T) {
	cfgDir := supervisedRegistry(t, config.ModeNormal, "")
	inst := newSupervisionInstance(t, cfgDir, "fix-ci", false)
	le, err := inst.launchEnv(true)
	require.NoError(t, err)
	assert.Nil(t, le.Supervision)

	got := inst.launchProgram(le, true)
	assert.NotContains(t, got, "--name")
	assert.NotContains(t, got, "claude-loom-context-")
	for _, kv := range InstanceEnv(le) {
		assert.False(t, strings.HasPrefix(kv, "LOOM_"), kv)
	}
	settings, err := os.ReadFile(filepath.Join(SubagentHooksDir(cfgDir, "fix-ci"), "settings.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(settings), "permissions")
}

func TestLaunch_SupervisedWorker(t *testing.T) {
	cfgDir := supervisedRegistry(t, config.ModeSupervised, "")
	inst := newSupervisionInstance(t, cfgDir, "fix-ci", false)
	le, err := inst.launchEnv(true)
	require.NoError(t, err)
	require.NotNil(t, le.Supervision)
	assert.Equal(t, work.RoleWorker, le.Supervision.Role)

	got := inst.launchProgram(le, true)
	ctxFile := filepath.Join(cfgDir, "claude-loom-context-worker.md")
	assert.Contains(t, got, "--append-system-prompt-file '"+ctxFile+"'")
	assert.Contains(t, got, "--name 'kermit/fix-ci'")
	body, err := os.ReadFile(ctxFile)
	require.NoError(t, err)
	assert.Contains(t, string(body), "# Worker protocol", "the protocol is there even with loom-context off")
	assert.DirExists(t, work.Dir(cfgDir), "the work folder exists before the agent runs")

	env := InstanceEnv(le)
	assert.Contains(t, env, "LOOM_INSTANCE=fix-ci")
	assert.Contains(t, env, "LOOM_ROLE=worker")
	assert.Contains(t, env, "LOOM_WORK_DIR="+work.Dir(cfgDir))

	settings, err := os.ReadFile(filepath.Join(SubagentHooksDir(cfgDir, "fix-ci"), "settings.json"))
	require.NoError(t, err)
	assert.Contains(t, string(settings), "Bash("+work.Executable()+" work *)")
	assert.Contains(t, string(settings), `"allowWrite"`)
}

func TestLaunch_SupervisedMainSessionKeepsTheBaseContextFirst(t *testing.T) {
	cfgDir := supervisedRegistry(t, config.ModeSupervised, "")
	SetLoomContextEnabled(true)
	t.Cleanup(func() { SetLoomContextEnabled(false) })
	inst := newSupervisionInstance(t, cfgDir, "kermit", true)
	le, err := inst.launchEnv(true)
	require.NoError(t, err)

	got := inst.launchProgram(le, true)
	assert.Contains(t, got, "--name 'kermit'")
	body, err := os.ReadFile(filepath.Join(cfgDir, "claude-loom-context-supervisor.md"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(body), string(loomContextWorkspaceBytes)), "the base context comes first")
	assert.Contains(t, string(body), "# Supervisor protocol")
}

func TestLaunch_AReattachResolvesNoSupervision(t *testing.T) {
	cfgDir := supervisedRegistry(t, config.ModeSupervised, "")
	inst := newSupervisionInstance(t, cfgDir, "fix-ci", false)
	le, err := inst.launchEnv(false)
	require.NoError(t, err)
	assert.Nil(t, le.Supervision, "a reattach launches nothing")
}

// The mode is read per instance: one process holds workspaces in both modes.
func TestLaunch_EachWorkspaceUsesItsOwnMode(t *testing.T) {
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	plainRepo, supRepo := t.TempDir(), t.TempDir()
	require.NoError(t, config.SaveWorkspaceRegistry(&config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "plain", Path: plainRepo},
		{Name: "kermit", Path: supRepo, Mode: config.ModeSupervised},
	}}))
	a := newSupervisionInstance(t, filepath.Join(plainRepo, ".loom"), "a", false)
	b := newSupervisionInstance(t, filepath.Join(supRepo, ".loom"), "b", false)
	leA, err := a.launchEnv(true)
	require.NoError(t, err)
	leB, err := b.launchEnv(true)
	require.NoError(t, err)
	assert.Nil(t, leA.Supervision)
	require.NotNil(t, leB.Supervision)
	assert.Equal(t, "kermit", leB.Supervision.Workspace)
}

func TestLaunch_MainSessionTakesTheWorkspacesAccount(t *testing.T) {
	cfgDir := supervisedRegistry(t, config.ModeSupervised, "personal")
	acctDir := t.TempDir()
	withAccountDirs(t, map[string]string{"personal": acctDir})

	inst := newSupervisionInstance(t, cfgDir, "kermit", true)
	le, err := inst.launchEnv(true)
	require.NoError(t, err)
	assert.Equal(t, "personal", inst.Account(), "recorded on the instance")
	assert.Equal(t, acctDir, le.ClaudeConfigDir)

	worker := newSupervisionInstance(t, cfgDir, "fix-ci", false)
	_, err = worker.launchEnv(true)
	require.NoError(t, err)
	assert.Empty(t, worker.Account(), "a worker's account is the user's choice in Launch Options")
}

// A supervised launch that can't add loom's settings still launches, but
// leaves a warning for the app's status bar; loom.log alone goes unseen.
func TestLaunch_WithoutItsSettingsASupervisedLaunchWarns(t *testing.T) {
	cfgDir := supervisedRegistry(t, config.ModeSupervised, "")
	inst := newSupervisionInstance(t, cfgDir, "fix-ci", false)
	le, err := inst.launchEnv(true)
	require.NoError(t, err)
	le.Program = "claude --settings /mine.json"

	got := inst.launchProgram(le, true)
	assert.Contains(t, got, "--name 'kermit/fix-ci'", "the rest of the supervised launch stands")
	w := inst.TakeLaunchWarning()
	assert.Contains(t, w, "program already passes --settings")
	assert.Contains(t, w, "loom work")
	assert.Empty(t, inst.TakeLaunchWarning(), "each warning is taken once")

	plain := newSupervisionInstance(t, supervisedRegistry(t, config.ModeNormal, ""), "x", false)
	plainEnv, err := plain.launchEnv(true)
	require.NoError(t, err)
	plainEnv.Program = "claude --settings /mine.json"
	plain.launchProgram(plainEnv, true)
	assert.Empty(t, plain.TakeLaunchWarning(), "a normal launch without hooks is only logged, as today")
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./session/ -run TestLaunch_ -v`
Expected: FAIL to build (`le.Supervision undefined`, `inst.launchProgram` called with `LaunchEnv`).

- [ ] **Step 3: Implement**

(a) `session/loom_context.go`: extract the write-if-changed step. In `WriteLoomContextFiles`, replace the `for _, f := range files { … }` loop (keep the `return nil` and closing brace after it) with:

```go
	for _, f := range files {
		path := filepath.Join(configDir, f.name)
		if err := writeContextFile(path, f.content); err != nil {
			return fmt.Errorf("write loom context %s: %w", f.name, err)
		}
	}
```

and add after the function:

```go
// writeContextFile writes content to path unless the file already holds
// exactly that content, so an unchanged prompt file keeps its inode and
// mtime.
func writeContextFile(path string, content []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, content) {
		return nil
	}
	return config.AtomicWriteFile(path, content, 0o644)
}
```

(b) `session/supervision.go`:

```go
package session

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/work"
)

// BuildNameCommand returns program with the agent's session-name flag set
// to name. The adapter registry no-ops for non-Claude programs.
func BuildNameCommand(program, name string) string {
	return defaultRegistry.Lookup(program).ApplyNameFlag(program, name)
}

// supervision returns how a launch of this instance is supervised. It
// returns nil for a normal or unregistered workspace, for a program other
// than Claude, and when there is no config dir. It reads the registry at
// every launch, so a mode switched from the CLI reaches each session at its
// next launch. A registry that can't be read is logged, and the session
// launches as in a normal workspace.
func (i *Instance) supervision(program string) *work.Launch {
	if i.ConfigDir == "" || !IsClaudeProgram(program) {
		return nil
	}
	reg, err := config.LoadWorkspaceRegistry()
	if err != nil {
		i.getLogger().Warn("supervision.registry_load_failed", "err", err.Error())
		return nil
	}
	return work.ResolveLaunch(reg, i.ConfigDir, i.Title, i.IsWorkspaceTerminal)
}

// supervisedProgram applies a supervised launch to program: the role's
// composed context and the pinned session name. It also creates the work
// folder, which a sandboxed session may write in but cannot create.
func (i *Instance) supervisedProgram(program string, sv work.Launch) string {
	if err := os.MkdirAll(sv.WorkDir, 0o755); err != nil {
		i.getLogger().Warn("supervision.work_dir_failed", "dir", sv.WorkDir, "err", err.Error())
	}
	return BuildNameCommand(i.supervisedContextProgram(program, sv), sv.SessionName())
}

// supervisedContextProgram points Claude at the role's composed context
// file, claude-loom-context-<role>.md in the config dir. The file holds
// today's base context when the loom-context setting is on, then the
// role's protocol. The protocol is what makes the session a supervisor or
// a worker, so unlike the base context it ignores the setting. If the file
// can't be written, the normal context flag stands (logged).
func (i *Instance) supervisedContextProgram(program string, sv work.Launch) string {
	var buf bytes.Buffer
	if loomContextEnabled.Load() {
		if i.IsWorkspaceTerminal {
			buf.Write(loomContextWorkspaceBytes)
		} else {
			buf.Write(loomContextWorktreeBytes)
		}
		buf.WriteString("\n\n")
	}
	buf.WriteString(work.Protocol(sv.Role, sv.Loom))
	path := filepath.Join(i.ConfigDir, "claude-loom-context-"+string(sv.Role)+".md")
	if err := writeContextFile(path, buf.Bytes()); err != nil {
		i.getLogger().Warn("supervision.context_write_failed", "path", path, "err", err.Error())
		return loomContextProgram(program, i.ConfigDir, i.IsWorkspaceTerminal)
	}
	return BuildLoomContextCommand(program, path)
}

// noSupervisionSettings records a supervised launch that went without
// loom's settings file: the CLI may then prompt, and a sandboxed session
// can't write the work log. Besides the log line, it leaves a launch
// warning, which the app's health tick shows in the status bar
// (showLaunchWarnings) whichever path launched the session. A normal
// launch (sv nil) is only logged, by the caller, as before.
func (i *Instance) noSupervisionSettings(sv *work.Launch, reason string) {
	if sv == nil {
		return
	}
	i.getLogger().Warn("supervision.settings_skipped", "reason", reason)
	i.SetLaunchWarning(fmt.Sprintf("launched without loom's supervision settings (%s): `loom work` may ask for approval, and a sandboxed session can't write the work log", reason))
}

// SetLaunchWarning records something the user must hear about this
// session's latest launch. A later warning replaces one not yet taken.
func (i *Instance) SetLaunchWarning(msg string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.launchWarning = msg
}

// TakeLaunchWarning returns the launch warning not yet shown and clears
// it, so each warning is shown once.
func (i *Instance) TakeLaunchWarning() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	w := i.launchWarning
	i.launchWarning = ""
	return w
}
```

(c) `session/agent_restart.go`: add `"github.com/aidan-bailey/loom/work"` to the imports. Add a field at the end of `LaunchEnv`:

```go
	// Supervision is the launch's part in a supervised workspace, nil in a
	// normal one. launchEnv resolves it once per launch, so the program and
	// the environment can't disagree about it.
	Supervision *work.Launch
```

Replace `InstanceEnv` and its comment with:

```go
// InstanceEnv combines every per-session environment variable derived
// from an instance's launch (Headroom Proxy, Cache TTL, the account's
// config dir, a supervised launch's LOOM_* variables) plus the always-on
// Claude fullscreen renderer into the single slice that
// tmux.NewTmuxSession's variadic env parameter needs. It is centralized
// here so the four Instance call sites that construct a TmuxSession don't
// each repeat the same combination.
func InstanceEnv(e LaunchEnv) []string {
	env := append(HeadroomProxyEnv(e.HeadroomProxy, e.Program), CacheTTL1hEnv(e.CacheTTL1h, e.Program)...)
	env = append(env, ClaudeFullscreenEnv(e.Program)...)
	env = append(env, ClaudeConfigDirEnv(e.ClaudeConfigDir, e.Program)...)
	if e.Supervision != nil && IsClaudeProgram(e.Program) {
		env = append(env, e.Supervision.Env()...)
	}
	return env
}
```

(d) `session/instance.go`: add `"github.com/aidan-bailey/loom/work"` to the imports. In the `Instance` struct, after the `waitReason` field, add:

```go
	// launchWarning is what the latest launch needs the user to hear (a
	// supervised launch that went without loom's settings), until the
	// app's health tick takes it (TakeLaunchWarning). Ephemeral: never
	// serialized.
	launchWarning string
```

In `launchEnv`, between `i.mu.RUnlock()` and `dir, err := accountDir(name)`, insert:

```go
	if launching {
		env.Supervision = i.supervision(env.Program)
		if sv := env.Supervision; sv != nil && sv.Role == work.RoleSupervisor && sv.Account != name {
			// A supervised workspace's main session runs on the account its
			// sessions share, because cross-session messages don't cross
			// accounts. The account is recorded on the instance, so the
			// roster join and the badge follow it.
			name = sv.Account
			i.SetAccount(name)
		}
	}
```

In `Restart`, change `i.launchProgram(env.Program, true)` to `i.launchProgram(env, true)`. In `Start`, change `launchProgram := i.launchProgram(env.Program, firstTimeSetup)` to `launchProgram := i.launchProgram(env, firstTimeSetup)`.

(e) `session/subagent_hooks.go`: add `"github.com/aidan-bailey/loom/work"` to the imports. Replace `launchProgram` (with its comment) by:

```go
// launchProgram composes the command for a new tmux session from le. It
// adds loom's context flag (in a supervised workspace, the role's composed
// context and the pinned session name) and, when launching is true, loom's
// subagent hooks. launching is false only for Start(false), which
// reattaches to a live session with Restore. That Claude is still writing
// to its existing hooks folder, and preparing a new one would wipe its
// history. When launching is true, resetHookLaunch first clears any state
// left by the previous launch, so a relaunch that ends up skipping hooks
// (tracking turned off, program no longer Claude, etc.) never keeps a
// stale row, a stale launch ID or the old hooks folder around.
func (i *Instance) launchProgram(le LaunchEnv, launching bool) string {
	program := le.Program
	if le.Supervision != nil {
		program = i.supervisedProgram(program, *le.Supervision)
	} else {
		program = loomContextProgram(program, i.ConfigDir, i.IsWorkspaceTerminal)
	}
	if launching {
		i.resetHookLaunch()
		program = i.prepareHooks(program, le.Supervision)
	}
	return program
}
```

In `recoveryLaunch`, change `return i.launchProgram(le.Program, true), InstanceEnv(le), nil` to `return i.launchProgram(le, true), InstanceEnv(le), nil`.

Replace `prepareHooks` (with its comment) by:

```go
// prepareHooks readies a fresh hooks folder and returns program with
// --settings added. On any failure it returns program unchanged, so the
// session still launches, just untracked. It also adopts the new launch ID
// and resets the tracker, so scan results from before this launch are
// dropped. A supervised launch (sv non-nil) also gets the agent CLI's
// allow rule, and the work folder as a path sandboxed commands may write.
func (i *Instance) prepareHooks(program string, sv *work.Launch) string {
	if i.ConfigDir == "" || runtime.GOOS == "windows" || !IsClaudeProgram(program) {
		return program
	}
	if agent.HasSettingsFlag(program) {
		i.getLogger().Info("subagent_hooks.skipped", "reason", "program already passes --settings")
		i.noSupervisionSettings(sv, "program already passes --settings")
		return program
	}
	dir := SubagentHooksDir(i.ConfigDir, i.Title)
	if !hooks.SafePath(dir) {
		i.getLogger().Debug("subagent_hooks.skipped", "reason", "single quote in hooks folder path")
		i.noSupervisionSettings(sv, "single quote in hooks folder path")
		return program
	}
	var extra hooks.Extra
	if sv != nil {
		extra = hooks.Extra{Allow: []string{sv.AllowRule()}, AllowWrite: []string{sv.WorkDir}}
	}
	launchID, err := hooks.PrepareWith(dir, extra)
	if err != nil {
		i.getLogger().Warn("subagent_hooks.prepare_failed", "err", err.Error())
		i.noSupervisionSettings(sv, "hooks folder could not be prepared")
		return program
	}
	i.mu.Lock()
	i.hookLaunchID = launchID
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.mu.Unlock()
	return BuildSettingsCommand(program, hooks.SettingsPath(dir))
}
```

(f) Move the existing tests to the new `launchProgram` signature:

```bash
sed -i -E 's/\.launchProgram\(("claude"|tc\.program), (true|false)\)/.launchProgram(LaunchEnv{Program: \1}, \2)/' session/subagent_hooks_test.go
grep -n "launchProgram(" session/subagent_hooks_test.go
```

Expected: every line now reads `launchProgram(LaunchEnv{Program: …}, …)` (12 lines).

- [ ] **Step 4: Run the tests**

Run: `go test ./session/ -run 'TestLaunch_|Hook|Subagent|LoomContext|Account' -v 2>&1 | tail -30 && go test ./session/... && go build ./...`
Expected: PASS everywhere.

- [ ] **Step 5: Commit**

```bash
gofmt -w session/supervision.go session/supervision_test.go
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session/
git commit -m "feat(session): launch supervised sessions with protocol, name, env and settings

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Launch Options warnings

**Files:**
- Modify: `ui/overlay/sessionLaunchOptions.go` (imports, the struct, `Render`, `renderAccountNotice`)
- Test: `ui/overlay/sessionLaunchOptions_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `ui/overlay/sessionLaunchOptions_test.go`:

```go
func TestSessionLaunchOptions_SupervisorAccountWarning(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{Account: "personal", PermissionMode: "default"}, false, "")
	lo.SetAccounts([]AccountChoice{{Name: "default"}, {Name: "personal"}})
	lo.SetSupervisor("personal", "default")
	assert.NotContains(t, lo.Render(), "can't message each other", "matching choices warn about nothing")

	toAccountRow(lo)
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // personal → default
	assert.Contains(t, lo.Render(), "sessions on personal and default can't message each other")
}

func TestSessionLaunchOptions_SupervisorPermissionModeWarning(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{PermissionMode: "acceptEdits"}, false, "")
	lo.SetSupervisor("", "default")
	assert.Contains(t, lo.Render(), "messages may wait for approval")
	assert.NotContains(t, lo.Render(), "can't message each other", `"" and default are the same account`)
}

func TestSessionLaunchOptions_NoSupervisorNoWarnings(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{Account: "max-2", PermissionMode: "plan"}, false, "")
	assert.NotContains(t, lo.Render(), "supervisor")
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./ui/overlay/ -run 'TestSessionLaunchOptions_(Supervisor|NoSupervisor)' -v`
Expected: FAIL (`lo.SetSupervisor undefined`).

- [ ] **Step 3: Implement**

In `ui/overlay/sessionLaunchOptions.go`, add `"fmt"` to the imports. Add a field at the end of the `SessionLaunchOptions` struct:

```go
	// supervisor, when set, holds the supervised workspace's account and
	// its main session's permission mode. A choice that differs from either
	// gets a warning under the rows; see SetSupervisor.
	supervisor *supervisorLaunch
```

Add after `SetAccountNotice`:

```go
// supervisorLaunch is what SetSupervisor records.
type supervisorLaunch struct{ account, permissionMode string }

// SetSupervisor marks the modal as launching a worker of a supervised
// workspace. account is the account the workspace's sessions share, and
// permissionMode is the one its main session runs in. While the chosen
// account or mode differs, the modal warns: sessions on different accounts
// can't message each other, and a session in another permission mode may
// hold messages for approval.
func (l *SessionLaunchOptions) SetSupervisor(account, permissionMode string) {
	l.supervisor = &supervisorLaunch{account: account, permissionMode: permissionMode}
}

// supervisionWarnings are the current choice's conflicts with SetSupervisor.
func (l *SessionLaunchOptions) supervisionWarnings() []string {
	if l.supervisor == nil {
		return nil
	}
	var warnings []string
	if want, got := defaultedName(l.supervisor.account), defaultedName(l.opts.Account); want != got {
		warnings = append(warnings, fmt.Sprintf("sessions on %s and %s can't message each other", want, got))
	}
	if want, got := defaultedName(l.supervisor.permissionMode), defaultedName(l.opts.PermissionMode); want != got {
		warnings = append(warnings, fmt.Sprintf("the supervisor runs in %s mode: messages may wait for approval", want))
	}
	return warnings
}

// defaultedName shows "" as "default", the spelling both the Account and
// Permission Mode rows use for it.
func defaultedName(s string) string {
	if s == "" {
		return "default"
	}
	return s
}
```

In `Render`, replace:

```go
		if l.accountNotice != "" {
			content += l.renderAccountNotice() + "\n"
		}
	}
	content += "\n" + sessionLaunchOptionsHintStyle.Render(l.hint())
```

with:

```go
		if l.accountNotice != "" {
			content += l.renderNotice(l.accountNotice) + "\n"
		}
	}
	for _, w := range l.supervisionWarnings() {
		content += l.renderNotice(w) + "\n"
	}
	content += "\n" + sessionLaunchOptionsHintStyle.Render(l.hint())
```

Replace `renderAccountNotice` (with its comment) by:

```go
// renderNotice renders msg indented under the rows, wrapped to the modal's
// content width (its width less the border and padding), so a long name
// wraps with the indent kept.
func (l *SessionLaunchOptions) renderNotice(msg string) string {
	style := sessionLaunchOptionsNoticeStyle.PaddingLeft(sessionLaunchOptionsNoticeIndent)
	if w := l.width - 6; w > sessionLaunchOptionsNoticeIndent+10 {
		style = style.Width(w)
	}
	return style.Render(msg)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./ui/overlay/ -v -run TestSessionLaunchOptions`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w ui/overlay/sessionLaunchOptions.go ui/overlay/sessionLaunchOptions_test.go
git add ui/overlay/sessionLaunchOptions.go ui/overlay/sessionLaunchOptions_test.go
git commit -m "feat(overlay): warn when a worker's account or mode leaves the supervisor's

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: The app: preselect, warnings, and forgetting killed sessions

**Files:**
- Create: `app/supervision.go`
- Modify: `app/accounts.go` (`newLaunchOptionsOverlay`), `app/intents.go` (`killActionFor`), `app/app.go` (the `tickUpdateMetadataMessage` case)
- Test: `app/supervision_test.go`

- [ ] **Step 1: Write the failing test**

`app/supervision_test.go`:

```go
package app

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/work"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// supervisedHome is a test home focused on kermit, a supervised workspace
// whose sessions share acct.
func supervisedHome(t *testing.T, acct string) *home {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	repo := t.TempDir()
	require.NoError(t, config.SaveWorkspaceRegistry(&config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "kermit", Path: repo, Mode: config.ModeSupervised, Account: acct}}}))
	m := newTestHome(t)
	m.wsCtx = &config.WorkspaceContext{Name: "kermit", ConfigDir: filepath.Join(repo, ".loom"), RepoPath: repo}
	return m
}

func TestNewLaunchOptionsOverlay_SupervisedWorkspacePreselectsItsAccount(t *testing.T) {
	m := supervisedHome(t, "personal")
	withAccounts(t, m, "personal")
	lo, _ := m.newLaunchOptionsOverlay(bareOpts(""), "claude")
	assert.Equal(t, "personal", lo.Options().Account)
	assert.NotContains(t, lo.Render(), "can't message each other")
}

func TestNewLaunchOptionsOverlay_SupervisedWorkspaceWarnsOnAnotherAccount(t *testing.T) {
	m := supervisedHome(t, "personal")
	withAccounts(t, m, "personal")
	lo, _ := m.newLaunchOptionsOverlay(bareOpts(account.DefaultName), "claude") // R on a session on default
	assert.Equal(t, account.DefaultName, lo.Options().Account, "R keeps the session's own account")
	assert.Contains(t, lo.Render(), "can't message each other")
}

func TestNewLaunchOptionsOverlay_NormalWorkspaceHasNoSupervisor(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "personal")
	m.wsCtx = &config.WorkspaceContext{Name: "x", ConfigDir: t.TempDir()}
	lo, _ := m.newLaunchOptionsOverlay(bareOpts(""), "claude")
	assert.NotContains(t, lo.Render(), "supervisor")
	assert.NotContains(t, lo.Render(), "can't message each other")
}

// The permission-mode warning compares with the mode the main session
// actually runs in, not the configured default, which may have changed
// since the main session launched.
func TestNewLaunchOptionsOverlay_ComparesWithTheMainSessionsOwnMode(t *testing.T) {
	m := supervisedHome(t, "")
	main, err := session.NewInstance(session.InstanceOptions{Title: "kermit", Path: t.TempDir(),
		Program: "claude --permission-mode plan", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	m.list.AddInstance(main)

	lo, _ := m.newLaunchOptionsOverlay(bareOpts(""), "claude") // the configured default mode
	assert.Contains(t, lo.Render(), "the supervisor runs in plan mode")
}

// A launch warning reaches the status bar on the health tick, once.
func TestShowLaunchWarnings(t *testing.T) {
	m := newTestHome(t)
	inst, err := session.NewInstance(session.InstanceOptions{Title: "fix-ci", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	m.list.AddInstance(inst)
	inst.SetLaunchWarning("launched without loom's supervision settings")

	m.showLaunchWarnings()
	assert.Contains(t, m.errBox.String(), "fix-ci: launched without loom's supervision settings")

	m.errBox.Clear()
	m.showLaunchWarnings()
	assert.NotContains(t, m.errBox.String(), "fix-ci", "a warning is shown once")
}

func TestKill_ForgetsTheSessionOnTheWorkLog(t *testing.T) {
	isolateTmux(t)
	repo := t.TempDir()
	out, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").CombinedOutput()
	require.NoError(t, err, string(out))
	cfgDir := t.TempDir()
	dir := work.Dir(cfgDir)
	require.NoError(t, work.Append(dir, func([]work.Entry) (work.Entry, error) {
		return work.Entry{V: work.FormatVersion, By: "fix-ci", Role: work.RoleWorker,
			Kind: work.KindState, State: work.StateWorking, Summary: "x"}, nil
	}))

	m := newTestHome(t)
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: "fix-ci", Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: t.TempDir(), BranchName: "loom/fix-ci", SessionName: "fix-ci"},
	}, cfgDir)
	require.NoError(t, err)
	m.list.AddInstance(inst)

	_, killAction := killActionFor(m, inst)
	_ = killAction()

	entries, _, err := work.Read(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, work.KindRemove, entries[1].Kind)
	assert.Equal(t, "fix-ci", entries[1].Title)
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./app/ -run 'TestNewLaunchOptionsOverlay_(Supervised|Normal|Compares)|TestKill_Forgets|TestShowLaunchWarnings' -v`
Expected: FAIL to build (`m.showLaunchWarnings undefined`). Once it builds, the other tests fail for the missing preselect, warnings and `remove` entry.

- [ ] **Step 3: Implement**

`app/supervision.go`:

```go
package app

import (
	"strings"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
)

// supervisedWorkspace returns the focused workspace's registry entry when
// it is supervised, or nil. It reads workspaces.json afresh, so a mode or
// account set from the CLI applies to the next session created here.
func (m *home) supervisedWorkspace() *config.Workspace {
	if m.wsCtx == nil || m.wsCtx.ConfigDir == "" {
		return nil
	}
	reg, err := config.LoadWorkspaceRegistry()
	if err != nil {
		log.For("app").Warn("supervision.registry_load_failed", "err", err.Error())
		return nil
	}
	ws := reg.FindByConfigDir(m.wsCtx.ConfigDir)
	if ws == nil || ws.Mode != config.ModeSupervised {
		return nil
	}
	return ws
}

// supervisorPermissionMode is the permission mode the focused workspace's
// main session runs in, decoded from its launch command. The configured
// default may have changed since that session launched, so it stands in
// only when there is no main session.
func (m *home) supervisorPermissionMode() string {
	for _, inst := range m.list.GetInstances() {
		if inst.IsWorkspaceTerminal {
			if opts, _ := ParseLaunchOptions(inst.Program()); opts.PermissionMode != "" {
				return opts.PermissionMode
			}
		}
	}
	return launchOptionsFromConfig(m.appConfig).PermissionMode
}

// showLaunchWarnings puts every launch warning not yet shown into the
// status bar, one line per session. It runs on the health tick, which
// every launch path (start, resume, crash restart, the main session's
// relaunch) reaches within one tick, so no path has to carry a warning
// back to the app itself. Update goroutine only.
func (m *home) showLaunchWarnings() {
	var lines []string
	for _, inst := range m.allInstances() {
		if w := inst.TakeLaunchWarning(); w != "" {
			lines = append(lines, inst.Title+": "+w)
		}
	}
	if len(lines) > 0 {
		m.errBox.SetInfo(strings.Join(lines, "\n"))
	}
}
```

In `app/app.go`, in the `case tickUpdateMetadataMessage:` block, right after `m.errBox.ExpireIfDue(time.Now())`, add:

```go

		// A launch that went without part of its setup (a supervised
		// session without loom's settings) says so here, whichever path
		// launched it.
		m.showLaunchWarnings()
```

In `app/accounts.go`, `newLaunchOptionsOverlay`: after `claude := session.IsClaudeProgram(program)`, insert:

```go
	var supervised *config.Workspace
	if claude {
		supervised = m.supervisedWorkspace()
	}
	if supervised != nil && opts.Account == "" {
		// A new worker preselects the account the workspace's sessions
		// share: cross-session messages don't cross accounts.
		opts.Account = accountOrDefault(supervised.Account)
	}
```

Before its final `return lo, reloaded`, insert:

```go
	if supervised != nil {
		lo.SetSupervisor(accountOrDefault(supervised.Account), m.supervisorPermissionMode())
	}
```

Add `"github.com/aidan-bailey/loom/config"` to `app/accounts.go`'s imports if it is not there yet. Append to the function's doc comment:

```go
// In a supervised workspace, a new session preselects the account the
// workspace's sessions share, and the modal warns while the chosen account
// or permission mode differs from the supervisor's.
```

In `app/intents.go`, `killActionFor`: below `splitPane, storage := m.splitPane, m.storage`, add:

```go
	configDir := selected.ConfigDir
```

and, in `killAction`, right after the `storage.DeleteInstance` error handling and before `return killInstanceMsg{…}`, add:

```go
		// A supervised workspace's board forgets the session, and its brief
		// and report go with it. A workspace that was never supervised has
		// no log and is left alone.
		if err := work.Forget(configDir, title, time.Now()); err != nil {
			log.For("app").Warn("kill.work_forget_failed", "title", title, "err", err)
		}
```

Add `"github.com/aidan-bailey/loom/work"` and, if missing, `"time"` to `app/intents.go`'s imports.

- [ ] **Step 4: Run the tests**

Run: `go test ./app/ -run 'TestNewLaunchOptionsOverlay|TestKill_Forgets|Kill' -v 2>&1 | tail -20 && go test ./app/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w app/supervision.go app/supervision_test.go app/accounts.go app/intents.go app/app.go
git add app/supervision.go app/supervision_test.go app/accounts.go app/intents.go app/app.go
git commit -m "feat(app): preselect a supervised workspace's account, show launch warnings, forget killed sessions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: `loom reset` removes the work log

**Files:**
- Modify: `main.go` (the reset command's `RunE`, after the worktrees cleanup; imports)
- Test: `main_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `main_test.go` (add `"github.com/aidan-bailey/loom/work"` to its imports):

```go
// TestResetCmd_RemovesTheWorkLog: a reset deletes the workspace's
// instances, so the work log about them goes too.
func TestResetCmd_RemovesTheWorkLog(t *testing.T) {
	isolateLoomEnv(t)
	stubNesting(t, nil)
	t.Cleanup(func() { resetForceFlag = false })
	stubResetTmux(t, &resetTmux{})
	dir := work.Dir(os.Getenv(config.EnvGlobalDir))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(work.LogPath(dir), []byte("{}\n"), 0o644))

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"reset", "--force"})
		require.NoError(t, rootCmd.Execute())
	})

	assert.Contains(t, out, "Work log has been removed")
	assert.NoDirExists(t, dir)
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test . -run TestResetCmd_RemovesTheWorkLog -v`
Expected: FAIL (`Work log has been removed` missing, folder still there).

- [ ] **Step 3: Implement**

In `main.go`, add `"github.com/aidan-bailey/loom/work"` to the imports. In the reset `RunE`, after `fmt.Println("Worktrees have been cleaned up")`, add:

```go
			if err := os.RemoveAll(work.Dir(wsCtx.ConfigDir)); err != nil {
				return fmt.Errorf("failed to remove the work log: %w", err)
			}
			fmt.Println("Work log has been removed")
```

- [ ] **Step 4: Run the tests**

Run: `go test . -v -run TestResetCmd`
Expected: PASS (all reset tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w main.go main_test.go
git add main.go main_test.go
git commit -m "feat: loom reset removes the workspace's work log

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 16: End to end: a fake worker declares its state through the real CLI

**Files:**
- Modify: `tools/fakeagent/agent.go` (`usage`, `exec`, a new `shell` method, imports)
- Test: `tools/fakeagent/agent_test.go` (append), `e2e/e2e_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `tools/fakeagent/agent_test.go`:

```go
func TestRun_RunExecutesAShellCommandLine(t *testing.T) {
	out, code, _ := runScript(t, "claude", "run echo hi from sh\n")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "hi from sh")
	assert.Contains(t, out, "run done")
}
```

Append to `e2e/e2e_test.go` (add `"github.com/aidan-bailey/loom/config"` to its imports if missing). The test's own `t.Setenv` only points its `SetMode` at the sandbox's registry. The CLI the fake agent runs finds that registry because the agent's pane inherits `LOOM_GLOBAL_DIR`. `devsandbox.tmuxCmd` starts the private tmux server with the sandbox's environment, and every pane takes the server's global environment (checked 2026-10-03).

```go
// A supervised workspace launches its workers with the work-log variables,
// so the real `loom work` CLI works from inside a session: the fake claude
// persona runs it the way Claude's Bash tool would.
func TestE2E_SupervisedWorkerDeclaresItsState(t *testing.T) {
	sb := newSandbox(t, "fake-claude")
	t.Setenv(config.EnvGlobalDir, sb.GlobalDir())
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.SetMode(devsandbox.WorkspaceName, config.ModeSupervised, nil))

	startLoom(t, sb)
	createSession(t, sb, "worker1")

	sendToAgent(t, sb, "run "+sb.LoomBin()+" work state working e2e-plan")
	require.NoError(t, sb.WaitFor("worker1 is working", uiTimeout))

	sendToAgent(t, sb, "run "+sb.LoomBin()+" work board")
	require.NoError(t, sb.WaitFor(devsandbox.WorkspaceName+"/worker1", uiTimeout))
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./tools/fakeagent/ -run TestRun_RunExecutes -v`
Expected: FAIL (`unknown command "run"`).

- [ ] **Step 3: Implement `run` in the fake agent**

In `tools/fakeagent/agent.go`, add `"os/exec"` to the imports. Extend the usage string to:

```go
	usage                = "commands: work N | ask | trust | bell | title TEXT | edit | commit | run CMD | crash | exit"
```

In `exec`'s second `switch cmd`, add before `case "crash":`:

```go
	case "run":
		a.shell(arg)
```

Add after `commit`:

```go
// shell runs a command line with sh in the agent's directory and
// environment, the way Claude's Bash tool does, and prints its output.
func (a *fakeAgent) shell(line string) {
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = a.dir
	out, err := cmd.CombinedOutput()
	fmt.Fprint(a.out, string(out))
	if err != nil {
		fmt.Fprintf(a.out, "run: %v\n", err)
	}
	fmt.Fprintln(a.out, "run done")
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./tools/fakeagent/ -v && CGO_ENABLED=0 go test -tags e2e ./e2e/ -run 'TestE2E_SupervisedWorkerDeclaresItsState|TestE2E_FakeClaudeHooksDriveStatus' -v -timeout 10m`
Expected: PASS. The e2e test needs tmux.

- [ ] **Step 5: Commit**

```bash
gofmt -w tools/fakeagent/agent.go tools/fakeagent/agent_test.go e2e/e2e_test.go
git add tools/fakeagent/agent.go tools/fakeagent/agent_test.go e2e/e2e_test.go
git commit -m "test(e2e): a supervised worker declares its state through loom work

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 17: Documentation

**Files:**
- Modify: `CLAUDE.md`, `USAGE.md`

- [ ] **Step 1: CLAUDE.md, CLI Usage.** After the line `loom workspace migrate       # Migrate instances to workspaces`, add:

```bash
loom workspace mode [name] <normal|supervised> [--account <a>]  # supervise a workspace; --account: the Claude account its sessions share

# Agent CLI, run by the sessions of a supervised workspace (loom exports LOOM_INSTANCE/LOOM_ROLE/LOOM_WORK_DIR into them)
loom work state <working|blocked|ready> "<summary>" [--report <file>]  # a worker declares its state
loom work verify <title> "<notes>"   # the supervisor passes a ready worker
loom work note "<decision>"          # the supervisor logs a routine decision
loom work board [--json] [title]     # the board; also works from a shell inside the repo
```

- [ ] **Step 2: CLAUDE.md, Environment Variables.** After the `LOOM_ALLOW_NESTED` bullet, add:

```markdown
- `LOOM_INSTANCE`, `LOOM_ROLE`, `LOOM_WORK_DIR` — exported by loom into every session of a supervised workspace (the title, `supervisor`/`worker`, and the workspace's work folder). `loom work` refuses without them; never set them in your shell.
```

- [ ] **Step 3: CLAUDE.md, Key Packages.** Before the bullet that starts `- **\`review/\`**`, add:

```markdown
- **`work/`** — The coordination store of a supervised workspace (spec `docs/superpowers/specs/2026-10-02-supervised-workspaces-design.md`). It holds:
  - **The log:** an append-only, `flock`-guarded `<repo>/.loom/work/log.jsonl`. `Append` reads and validates under the lock, then writes one line in one write. When a crash or a hand edit left the last line torn, it writes a newline first (`endsTorn`); otherwise the new entry would merge into the torn line and be lost while the CLI reported success. `Read` returns complete lines only.
  - **The fold** into each session's work state (`Fold`/`Validate`): workers declare `working`/`blocked`/`ready`, only the supervisor verifies or notes, and only loom removes.
  - **The board** (`BuildBoard`): `landed` and stale are derived from git, never stored. `landed` needs commits past the session's base commit, because `merge-base --is-ancestor` counts a fresh branch's tip as an ancestor.
  - **Launch facts:** the embedded role protocols (`protocol/*.md`), session names (`SessionName`), and `ResolveLaunch`/`Launch.Env`/`AllowRule`/`Executable`.

  It imports no app, ui, session or cmd code, so both the launch path and the `loom work` CLI (`cmd/work.go`) use it. Its readers of loom's files (`Sessions`, `BaseBranch`) decode state.json and config.json read-only: `config.LoadStateFrom`/`LoadConfigFrom` would move a corrupt file aside, which an agent's command must never do.
```

- [ ] **Step 4: CLAUDE.md, Gotchas.** At the end of the Gotchas list, after the `handleMenuHighlighting` bullet, add:

```markdown
- **Supervision is resolved per instance, at each real launch.**
  - **Where the mode lives:** a workspace's `mode` and `account` are in `workspaces.json` (`config.Workspace`). `Instance.launchEnv(true)` reads the registry (`work.ResolveLaunch`, matched with `FindByConfigDir(i.ConfigDir)`) into `LaunchEnv.Supervision`. `launchProgram` and `InstanceEnv` both take it from there, so a launch's program and environment can't disagree.
  - **Never a process-wide flag:** don't wire it the way `applySessionConfig` sets `loomContextEnabled`, where the last-loaded workspace wins; a worker would get another workspace's role.
  - **What a supervised launch adds:**
    - `--name`, the cross-session message address (without it, Claude derives a per-launch name from the cwd);
    - the role's composed `claude-loom-context-<role>.md`, which includes the protocol even with loom-context off;
    - `LOOM_INSTANCE`, `LOOM_ROLE` and `LOOM_WORK_DIR`;
    - in the per-launch hooks settings (`hooks.PrepareWith`), `permissions.allow` for `Bash(<loom> work *)` and `sandbox.filesystem.allowWrite` for the work folder. Probed: without them the CLI prompts in manual mode, and the sandbox refuses the write with "read-only file system".
  - **When the settings can't be added:** the program has its own `--settings`, the path holds a `'`, or the hooks folder can't be written. The session still launches, and `prepareHooks` leaves a launch warning on the instance (`SetLaunchWarning`). The app's health tick shows it in the status bar (`showLaunchWarnings`, which takes each warning once). Every launch path reaches the tick, so none has to carry the warning back itself. Don't downgrade it to a `loom.log` line: the user would never see why `loom work` prompts or fails.
  - **Launch Options' permission-mode warning** compares with the main session's own mode, decoded from its program (`supervisorPermissionMode` → `ParseLaunchOptions`). The configured default stands in only when there is no main session.
  - **The main session's account:** the main session of a supervised workspace launches on the workspace's account and records it on the instance (`SetAccount`), because cross-session messages don't cross Claude accounts.
  - **What doesn't change:** `Start(false)` (a reattach) resolves nothing, and normal workspaces launch exactly as before.
- **The work log, not hooks or state.json, carries coordination state.**
  - **Writers:** agents write it through `loom work`, which validates under the lock and reports refusals on stderr. Loom writes only `remove`, on a `D` kill (`work.Forget`), and `loom reset` removes the work folder.
  - **Don't route agent-declared state elsewhere:** the hooks folder is wiped at every real launch, and startup relaunches dead sessions before any scan; `InstanceData` reaches state.json only at lifecycle events.
  - **Reserved names:** nothing at the work folder's top level may be named `config`, `HEAD`, `objects` or `refs`, because the Claude sandbox write-denies those names in any writable directory.
```

- [ ] **Step 5: CLAUDE.md, Persistent State.** Replace the `workspaces.json` bullet with the first line below, and add the second after it:

```markdown
- `workspaces.json` — registered workspaces with name, path and last-used tracking, plus each workspace's `mode` (`""` or `supervised`) and `account` (the Claude account a supervised workspace's sessions share; `""` is the default account)
- `<repo>/.loom/work/` (per workspace, not in `~/.loom`) — a supervised workspace's work log (`log.jsonl`), `briefs/` and `reports/`; next to it, `claude-loom-context-{supervisor,worker}.md`, composed at each supervised launch
```

- [ ] **Step 6: USAGE.md, Workflows.** Before `### Work Across Multiple Workspaces`, add:

```markdown
### Supervise a Workspace

In a supervised workspace the main session supervises: it briefs workers,
verifies their branches and decides the landing order, while you approve,
decide and push. Each session keeps its own work state up to date in a log
that loom reads.

1. Turn it on: `loom workspace mode kermit supervised --account personal`.
   `--account` names the Claude account every session of the workspace
   shares, because cross-session messages don't cross accounts. Leave it
   out to use the default account.
2. Restart the main session so it launches as the supervisor: exit Claude
   in the `kermit` session, and loom relaunches it. Worker sessions pick
   the change up at their next launch.
3. New sessions (`n`, `N`, `I`) are workers. Session Launch Options
   preselect the workspace's account, and warn when you pick another
   account or a permission mode other than the supervisor's. If a session
   launches without loom's settings (its program passes its own
   `--settings`, say), the status bar says so: `loom work` may then ask
   for approval there.
4. Sessions run `loom work state`, `verify`, `note` and `board` themselves,
   as their protocol tells them. To read the board, run `loom work board`
   in any shell inside the repo. It shows each session's message address,
   state, summary and age; stale and landed branches; the push command for
   a verified branch; and the supervisor's decisions.
5. Turn supervision off with `loom workspace mode kermit normal`. The work
   log stays in `<repo>/.loom/work/`.

Each session gets a stable message address: the workspace's name for the
main session, and `kermit/<title>` for a worker.
```

- [ ] **Step 7: USAGE.md, CLI Reference.** In the **Commands** table, after the `workspace` row, add:

```markdown
| `work` | Agent CLI of supervised workspaces (see below) |
```

In **Workspace Subcommands**, after the `workspace migrate` row, add:

```markdown
| `workspace mode [name] <normal\|supervised> [--account <a>]` | Turn supervision on or off; `--account` sets the Claude account the workspace's sessions share |
```

After the **Workspace Subcommands** table, add:

```markdown
### Work Subcommands

Run by the sessions of a supervised workspace, which loom launches with
`LOOM_INSTANCE`, `LOOM_ROLE` and `LOOM_WORK_DIR` set. Anywhere else they
refuse, except `work board`, which also works from a shell inside the
workspace's repo.

| Command | Description |
|---------|-------------|
| `work state <working\|blocked\|ready> "<summary>" [--report <file>]` | A worker declares its own state; `ready` records the branch tip and needs a report |
| `work verify <title> "<notes>"` | The supervisor marks a ready worker verified at the HEAD it reported (refused if the branch moved since) |
| `work note "<decision>"` | The supervisor logs a routine decision |
| `work board [--json] [title]` | The workspace's board, or one session in full |
```

- [ ] **Step 8: Commit**

```bash
git add CLAUDE.md USAGE.md
git commit -m "docs: document supervised workspaces stage 1

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 18: Final verification

- [ ] **Step 1: Format and vet**

Run: `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/') && go vet ./...`
Expected: no files listed, and no vet findings.

- [ ] **Step 2: Build and run the full suite**

Run: `CGO_ENABLED=0 go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 3: Race detector on the packages this plan touched**

Run: `CC=clang CGO_ENABLED=1 go test -race ./work/... ./cmd/... ./config/... ./session/... ./app/... ./ui/overlay/...`
Expected: PASS.

- [ ] **Step 4: End-to-end suite**

Run: `CGO_ENABLED=0 go test -tags e2e ./e2e/... -timeout 20m`
Expected: PASS.

- [ ] **Step 5 (manual, optional): see it in a dev sandbox.** Use the `loom-dev` skill (`go run ./tools/loomdev up`, then `start`), never `./loom` in a loom pane. Then:

1. Run `loom workspace mode toy supervised` against the sandbox's `LOOM_GLOBAL_DIR`.
2. Create a session.
3. In the sandbox's tmux server, confirm the agent's command line holds `--name 'toy/<title>'` and `claude-loom-context-worker.md`.
4. Confirm the agent's environment holds the three `LOOM_*` variables.

- [ ] **Step 6: Commit any formatting fixes**

```bash
git status --short
# only if Step 1 or 2 changed files:
git commit -am "chore: format

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-review notes

How the spec's stage-1 items map to tasks:

| Spec item | Task |
|---|---|
| Mode, account and `loom workspace mode` (§1, §2) | 2, 9 |
| Roles derived from the mode, resolved per instance (§1) | 5, 12 |
| Composed context, with the protocol regardless of the toggle (§2.1) | 12 |
| CLI path from `os.Executable` (§2.2) | 5, 12 |
| `--name` and the `LOOM_*` variables (§2.3) | 5, 11, 12 |
| Settings: allow rule and `sandbox.filesystem.allowWrite` (§2.4) | 10, 12 |
| One account: main-session account, preselect and warnings (§2) | 12, 13, 14 |
| Protocol text (§3) | 6 |
| Work log: entries, locking, authority, reading, derived states, lifetime (§4) | 3, 4, 7, 14, 15 |
| Agent CLI (§5) | 8 |
| Failure handling (§10): refusals, wrong role, moved branch, crashes, a torn last line, missing settings (warned in the status bar) | 3, 4, 8, 12, 14 |
| Permission-mode warning against the main session's own mode (§2) | 14 |
| Testing section | each task's tests, and Task 16's e2e |
| Documentation (CLAUDE.md gotchas; USAGE.md mode and CLI) | 17 |

Deliberately left to stage 2: the TUI board, the decisions tab, the push
command on cards, the settings-overlay toggle, the offer to restart the
main session, the health-tick reload of `workspaces.json`, and `propose`.
