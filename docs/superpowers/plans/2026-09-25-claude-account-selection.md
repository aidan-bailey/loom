# Claude Account Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user run each Claude session on one of several Claude subscriptions, chosen per session, with every account's plan usage visible while choosing and at a glance.

**Architecture:** An account is a `CLAUDE_CONFIG_DIR` that loom creates under `<globalDir>/accounts/<name>/` and links to the main config dir (everything but credentials and runtime state), registered in `<globalDir>/accounts.json`. A new `account/` package owns the registry, the links, `claude auth status`/`login`, and a headless `get_usage` usage probe. `session` sets `CLAUDE_CONFIG_DIR` on launch from a published name→dir map and fails closed on a removed account. `app` fans the roster out per account, polls usage on its own `pollGate`, and wires an Account row into Launch Options, a usage strip above the tab bar, card badges, and an Accounts screen in Settings. `loom account …` is the CLI.

**Tech Stack:** Go 1.25, Bubble Tea v2 (`charm.land/bubbletea/v2`), lipgloss v2, cobra, testify. Spec: `docs/superpowers/specs/2026-09-25-claude-account-selection-design.md`.

---

## Conventions for every task

- Run Go commands from the worktree root. Plain tests need `CGO_ENABLED=0` (the repo builds without CGO): `CGO_ENABLED=0 go test ./account/...`.
- Race detector: `CC=clang CGO_ENABLED=1 go test -race ./app/... ./session/... ./account/...`.
- Format only tracked non-vendor files: `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') <new files>`. Never `gofmt -w .` (it rewrites `vendor/`).
- Local golangci-lint is v2 and the repo config is v1-shaped; use `go vet ./...` instead.
- Commit messages end with the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Never run `./loom` directly; to see the TUI use the `loom-dev` skill (`go run ./tools/loomdev …`).

## File structure

**New**
| File | Responsibility |
|---|---|
| `account/registry.go` | Package doc, `Registry` (accounts.json load/latch/update), `ValidName`, `EnvFor`, `DefaultName` |
| `account/link.go` | `Sync` (deny-list symlinking), `Registry.Create`, `Registry.Remove` |
| `account/auth.go` | `Identity`, `AuthStatus`, `LoginCmd`, `Binary`, `MainDir` |
| `account/usage.go` | `Usage`, `Window`, `ProbeUsage` (headless `get_usage`), `decodeUsage` |
| `account/users.go` | `CountUsers`, `KnownStateDirs` (read-only in-use check) |
| `account/testdata/get_usage_2.1.281.jsonl` | Trimmed real `get_usage` response |
| `account/*_test.go`, `account/testmain_test.go` | Tests; loom-dir isolation |
| `session/account_env.go` | `SetAccountDirs`, `MissingAccountError`, account dir resolution |
| `ui/account_usage.go` | `AccountStatus`, `AccountUsageText` |
| `ui/account_strip.go` | `AccountStrip` (the one-row usage strip) |
| `ui/overlay/accountsManager.go` | Accounts screen (Settings sub-screen) + `AccountRequest` |
| `app/accounts.go` | Registry wiring, `rcAuthFor`, refresh Cmd, views, request handling, `topChromeHeight` |
| `app/usage.go` | `gateUsage` job: probe Cmd, result handling |
| `cmd/account.go` | `loom account add/login/list/use/sync/remove` |

**Modified**
| File | Change |
|---|---|
| `session/agent_restart.go` | `LaunchEnv`, `InstanceEnv(LaunchEnv)`, `ClaudeConfigDirEnv` |
| `session/instance.go` | `account` field, `Account/SetAccount`, `launchEnv`, Start fail-closed, snapshot/restore |
| `session/subagent_hooks.go` | `recoveryLaunch` returns an error |
| `session/reconcile.go` | `InstanceEnv(LaunchEnv{…})` |
| `session/storage.go`, `session/storage_migrate.go` | Schema v8 `account` |
| `session/claude_roster.go` | `QueryClaudeRosterEnv` |
| `session/remote_control_auth.go` | `DetectClaudeRemoteControlAuthEnv`, `Identity` on `RemoteControlAuth` |
| `cmd/workspace_migrate.go`, `cmd/workspace_migrate_shape_test.go` | Mirror field + fixture |
| `ui/card.go`, `ui/overview.go`, `ui/split_pane.go` | Account badge |
| `ui/overlay/sessionLaunchOptions.go` | Account row |
| `ui/overlay/settingsOverlay.go` | Accounts row + sub-screen |
| `app/app.go`, `app/app_init.go`, `app/events.go`, `app/pollgate.go`, `app/remote_control.go`, `app/state_prompt.go`, `app/state_issue_picker.go`, `app/intents.go`, `app/state_settings.go`, `app/interact.go`, `app/workspaces.go` | Wiring |
| `main.go`, `cmd/doc.go` | Register `loom account` |
| `CLAUDE.md`, `USAGE.md` | Docs |

---

## Phase A — `account` package

### Task 1: Account registry

**Files:**
- Create: `account/registry.go`
- Create: `account/testmain_test.go`
- Test: `account/registry_test.go`

- [ ] **Step 1: Write the TestMain** (the package imports `config`, so `TestEveryConfigReachingPackageIsolatesLoomDirs` requires it)

`account/testmain_test.go`:
```go
package account

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

- [ ] **Step 2: Write the failing tests**

`account/registry_test.go`:
```go
package account

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadRegistry_MissingFileIsEmpty(t *testing.T) {
	r := LoadRegistry(t.TempDir())
	require.NoError(t, r.LoadErr())
	assert.False(t, r.HasExtra())
	assert.Equal(t, DefaultName, r.Default())
	assert.Equal(t, []string{DefaultName}, r.Names())
}

func TestRegistry_UpdateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	r := LoadRegistry(dir)
	require.NoError(t, r.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "max-2", Dir: "/a/max-2"})
		return nil
	}))
	require.NoError(t, r.SetDefault("max-2"))

	again := LoadRegistry(dir)
	require.NoError(t, again.LoadErr())
	assert.Equal(t, "max-2", again.Default())
	assert.Equal(t, []string{DefaultName, "max-2"}, again.Names())
	assert.Equal(t, map[string]string{"max-2": "/a/max-2"}, again.Dirs())
	env, err := again.Env("max-2")
	require.NoError(t, err)
	assert.Equal(t, []string{"CLAUDE_CONFIG_DIR=/a/max-2"}, env)
}

func TestRegistry_UpdateMergesAConcurrentWriter(t *testing.T) {
	dir := t.TempDir()
	a, b := LoadRegistry(dir), LoadRegistry(dir)
	require.NoError(t, a.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "one", Dir: "/1"})
		return nil
	}))
	require.NoError(t, b.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "two", Dir: "/2"})
		return nil
	}))
	assert.Equal(t, []string{DefaultName, "one", "two"}, LoadRegistry(dir).Names())
}

func TestRegistry_CorruptFileLatchesWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))

	r := LoadRegistry(dir)
	require.Error(t, r.LoadErr())
	assert.False(t, r.HasExtra())
	assert.ErrorIs(t, r.SetDefault(DefaultName), ErrRegistryLoadFailed)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "not json", string(data), "a corrupt registry must never be overwritten")
}

func TestRegistry_DefaultFallsBackWhenUnregistered(t *testing.T) {
	r := &Registry{DefaultAccount: "gone"}
	assert.Equal(t, DefaultName, r.Default())
}

func TestRegistry_EnvForDefaultAndUnknown(t *testing.T) {
	r := &Registry{}
	env, err := r.Env(DefaultName)
	require.NoError(t, err)
	assert.Nil(t, env)
	env, err = r.Env("")
	require.NoError(t, err)
	assert.Nil(t, env)
	_, err = r.Env("nope")
	assert.Error(t, err)
}

func TestRegistry_SetDefaultRejectsUnknown(t *testing.T) {
	r := LoadRegistry(t.TempDir())
	assert.Error(t, r.SetDefault("nope"))
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"max-2", "work", "a1", "0"} {
		assert.NoError(t, ValidName(ok), ok)
	}
	for _, bad := range []string{"", "default", "Max", "-x", "a/b", "a b", "a.b", ".."} {
		assert.Error(t, ValidName(bad), bad)
	}
}

func TestUnavailableRefusesWrites(t *testing.T) {
	r := Unavailable(errors.New("no home"))
	require.Error(t, r.LoadErr())
	assert.ErrorIs(t, r.SetDefault(DefaultName), ErrRegistryLoadFailed)
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./account/...`
Expected: FAIL to compile, `undefined: LoadRegistry` (and the rest).

- [ ] **Step 4: Implement**

`account/registry.go`:
```go
// Package account manages the extra Claude Code accounts loom can launch
// sessions under. Claude Code has no account flag: an account is whatever
// credentials live in a config dir, and CLAUDE_CONFIG_DIR picks the dir.
// loom keeps one dir per extra account under <globalDir>/accounts/<name>,
// links the main config dir's shared entries into it (link.go), and reads
// each account's identity and plan usage through the claude CLI (auth.go,
// usage.go). The implicit account "default" is Claude with no override;
// it is never stored.
//
// No app, ui or session imports. Every subprocess runs through an injected
// internalexec.Executor.
package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/aidan-bailey/loom/config"
)

// DefaultName names the implicit account: Claude with no CLAUDE_CONFIG_DIR
// override.
const DefaultName = "default"

const (
	registryFileName = "accounts.json"
	accountsDirName  = "accounts"
)

// Account is one registered extra account.
type Account struct {
	Name string `json:"name"`
	// Dir is the account's CLAUDE_CONFIG_DIR.
	Dir string `json:"dir"`
}

// Registry is accounts.json: the extra accounts and which account new
// sessions preselect. Every mutation reloads the file first and writes it
// back atomically, so a `loom account` run and a running TUI don't clobber
// each other (the same reload-before-save rule as config.WorkspaceRegistry).
type Registry struct {
	// DefaultAccount is the account new sessions preselect; empty means
	// DefaultName.
	DefaultAccount string    `json:"default,omitempty"`
	Accounts       []Account `json:"accounts"`

	path    string
	loadErr error
}

// ErrRegistryLoadFailed is returned by every write to a registry whose file
// failed to load, so a corrupt accounts.json is never overwritten.
var ErrRegistryLoadFailed = errors.New("accounts.json failed to load; refusing to overwrite it")

// LoadRegistry reads <globalDir>/accounts.json. A missing file is an empty
// registry. A file that cannot be read or parsed yields an empty registry
// whose LoadErr is set and whose writes all fail.
func LoadRegistry(globalDir string) *Registry {
	r := &Registry{path: filepath.Join(globalDir, registryFileName)}
	data, err := os.ReadFile(r.path)
	if err != nil {
		if !os.IsNotExist(err) {
			r.loadErr = fmt.Errorf("read %s: %w", r.path, err)
		}
		return r
	}
	if err := json.Unmarshal(data, r); err != nil {
		r.DefaultAccount, r.Accounts = "", nil
		r.loadErr = fmt.Errorf("parse %s: %w", r.path, err)
	}
	return r
}

// Unavailable is a registry that could not even be located (no global
// config dir). It holds no accounts and refuses every write.
func Unavailable(err error) *Registry {
	return &Registry{loadErr: err}
}

// LoadErr reports why the registry failed to load, or nil.
func (r *Registry) LoadErr() error { return r.loadErr }

// AccountsDir is where Create makes account dirs: <globalDir>/accounts.
func (r *Registry) AccountsDir() string {
	return filepath.Join(filepath.Dir(r.path), accountsDirName)
}

// HasExtra reports whether any account besides DefaultName is registered.
func (r *Registry) HasExtra() bool { return len(r.Accounts) > 0 }

// Get returns the registered account called name.
func (r *Registry) Get(name string) (Account, bool) {
	for _, a := range r.Accounts {
		if a.Name == name {
			return a, true
		}
	}
	return Account{}, false
}

// Default is the account new sessions preselect: DefaultAccount when it
// still names a registered account, else DefaultName.
func (r *Registry) Default() string {
	if _, ok := r.Get(r.DefaultAccount); ok {
		return r.DefaultAccount
	}
	return DefaultName
}

// Names lists every account: DefaultName first, then registration order.
func (r *Registry) Names() []string {
	names := []string{DefaultName}
	for _, a := range r.Accounts {
		names = append(names, a.Name)
	}
	return names
}

// Dirs maps each extra account's name to its config dir.
func (r *Registry) Dirs() map[string]string {
	dirs := make(map[string]string, len(r.Accounts))
	for _, a := range r.Accounts {
		dirs[a.Name] = a.Dir
	}
	return dirs
}

// EnvFor returns the environment entry that points Claude at config dir dir.
func EnvFor(dir string) []string { return []string{"CLAUDE_CONFIG_DIR=" + dir} }

// Env returns the environment entries that run Claude as name: nil for
// DefaultName (or ""), CLAUDE_CONFIG_DIR for a registered account, and an
// error for anything else.
func (r *Registry) Env(name string) ([]string, error) {
	if name == "" || name == DefaultName {
		return nil, nil
	}
	a, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("account %q is not registered", name)
	}
	return EnvFor(a.Dir), nil
}

// SetDefault makes name the account new sessions preselect.
func (r *Registry) SetDefault(name string) error {
	return r.update(func(fresh *Registry) error {
		if name == DefaultName {
			fresh.DefaultAccount = ""
			return nil
		}
		if _, ok := fresh.Get(name); !ok {
			return fmt.Errorf("account %q is not registered", name)
		}
		fresh.DefaultAccount = name
		return nil
	})
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidName checks an account name: lowercase letters, digits and dashes,
// not starting with a dash, and not DefaultName. The name becomes a
// directory name and appears in badges.
func ValidName(name string) error {
	if name == DefaultName {
		return fmt.Errorf("%q is reserved for the account Claude uses without loom", DefaultName)
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid account name %q: use lowercase letters, digits and dashes", name)
	}
	return nil
}

// update reloads the file, applies fn to the fresh copy, writes it back,
// and adopts the result. It refuses when this registry or the fresh load
// failed to load.
func (r *Registry) update(fn func(fresh *Registry) error) error {
	if r.loadErr != nil {
		return fmt.Errorf("%w: %v", ErrRegistryLoadFailed, r.loadErr)
	}
	fresh := LoadRegistry(filepath.Dir(r.path))
	if fresh.loadErr != nil {
		return fmt.Errorf("%w: %v", ErrRegistryLoadFailed, fresh.loadErr)
	}
	if err := fn(fresh); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(r.path), err)
	}
	data, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal accounts: %w", err)
	}
	if err := config.AtomicWriteFile(r.path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", r.path, err)
	}
	r.DefaultAccount, r.Accounts = fresh.DefaultAccount, fresh.Accounts
	return nil
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./account/... ./internal/testenv/...`
Expected: PASS (the testenv guard sees the new TestMain).

- [ ] **Step 6: Commit**

```bash
gofmt -w account/*.go
git add account/
git commit -m "feat(account): registry of extra Claude accounts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Linking, create and remove

**Files:**
- Create: `account/link.go`
- Test: `account/link_test.go`

- [ ] **Step 1: Write the failing tests**

`account/link_test.go`:
```go
package account

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mainDirWith builds a main config dir holding shared entries and the
// per-account ones the deny-list keeps out.
func mainDirWith(t *testing.T) string {
	t.Helper()
	main := t.TempDir()
	for _, f := range []string{"CLAUDE.md", "RTK.md", "settings.json", ".credentials.json", ".claude.json", ".claude.json.backup"} {
		require.NoError(t, os.WriteFile(filepath.Join(main, f), []byte(f), 0o600))
	}
	for _, d := range []string{"projects", "skills", "sessions", "daemon"} {
		require.NoError(t, os.Mkdir(filepath.Join(main, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(main, "projects", "keep.jsonl"), []byte("x"), 0o600))
	return main
}

func notExist(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

func TestSync_LinksSharedEntriesOnly(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()

	rep, err := Sync(acct, main)
	require.NoError(t, err)

	assert.Equal(t, []string{"CLAUDE.md", "RTK.md", "projects", "settings.json", "skills"}, rep.Linked)
	assert.Empty(t, rep.Diverged)
	target, err := os.Readlink(filepath.Join(acct, "projects"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(main, "projects"), target)
	for _, private := range []string{".credentials.json", ".claude.json", ".claude.json.backup", "sessions", "daemon"} {
		assert.True(t, notExist(t, filepath.Join(acct, private)), "%s must stay per account", private)
	}
}

func TestSync_IsIdempotent(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	_, err := Sync(acct, main)
	require.NoError(t, err)

	rep, err := Sync(acct, main)
	require.NoError(t, err)
	assert.Empty(t, rep.Linked)
	assert.Empty(t, rep.Diverged)
}

func TestSync_ReportsADivergedRealFileAndKeepsIt(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(acct, "settings.json"), []byte("mine"), 0o600))

	rep, err := Sync(acct, main)
	require.NoError(t, err)

	assert.Equal(t, []string{"settings.json"}, rep.Diverged)
	data, err := os.ReadFile(filepath.Join(acct, "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, "mine", string(data), "a diverged file may hold the only copy of a change")
}

func TestSync_PicksUpNewMainEntries(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	_, err := Sync(acct, main)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(main, "agents"), 0o755))

	rep, err := Sync(acct, main)
	require.NoError(t, err)
	assert.Equal(t, []string{"agents"}, rep.Linked)
}

func TestSync_MissingMainDirFails(t *testing.T) {
	_, err := Sync(t.TempDir(), filepath.Join(t.TempDir(), "nope"))
	assert.Error(t, err)
}

func TestCreate_MakesALinkedDirAndRegistersIt(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)

	acct, rep, err := r.Create("max-2", main)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(global, "accounts", "max-2"), acct.Dir)
	assert.Contains(t, rep.Linked, "projects")
	fi, err := os.Stat(acct.Dir)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o700), fi.Mode().Perm(), "the dir will hold credentials")
	got, ok := LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	assert.Equal(t, acct, got)
}

func TestCreate_RejectsInvalidAndDuplicateNames(t *testing.T) {
	r, main := LoadRegistry(t.TempDir()), mainDirWith(t)
	_, _, err := r.Create("Bad Name", main)
	assert.Error(t, err)
	_, _, err = r.Create(DefaultName, main)
	assert.Error(t, err)
	_, _, err = r.Create("max-2", main)
	require.NoError(t, err)
	_, _, err = r.Create("max-2", main)
	assert.Error(t, err)
}

func TestCreate_FailureLeavesNothingBehind(t *testing.T) {
	global := t.TempDir()
	r := LoadRegistry(global)

	_, _, err := r.Create("max-2", filepath.Join(global, "no-such-main"))

	require.Error(t, err)
	assert.True(t, notExist(t, filepath.Join(global, "accounts", "max-2")))
	assert.False(t, LoadRegistry(global).HasExtra())
}

func TestRemove_DeletesLinksButNeverTheirTargets(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	acct, _, err := r.Create("max-2", main)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(acct.Dir, ".credentials.json"), []byte("secret"), 0o600))

	deleted, err := r.Remove("max-2")

	require.NoError(t, err)
	assert.True(t, deleted)
	assert.True(t, notExist(t, acct.Dir))
	data, err := os.ReadFile(filepath.Join(main, "projects", "keep.jsonl"))
	require.NoError(t, err, "shared content behind the links must survive")
	assert.Equal(t, "x", string(data))
	assert.False(t, LoadRegistry(global).HasExtra())
}

func TestRemove_ClearsTheDefaultAndRefusesDefaultAccount(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	_, _, err := r.Create("max-2", main)
	require.NoError(t, err)
	require.NoError(t, r.SetDefault("max-2"))

	_, err = r.Remove(DefaultName)
	assert.Error(t, err)
	_, err = r.Remove("max-2")
	require.NoError(t, err)
	assert.Equal(t, DefaultName, LoadRegistry(global).Default())
}

func TestRemove_OutsideAccountsDirOnlyUnregisters(t *testing.T) {
	global, outside := t.TempDir(), t.TempDir()
	r := LoadRegistry(global)
	require.NoError(t, r.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "byo", Dir: outside})
		return nil
	}))

	deleted, err := r.Remove("byo")

	require.NoError(t, err)
	assert.False(t, deleted)
	_, err = os.Stat(outside)
	assert.NoError(t, err, "loom only deletes dirs it created")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./account/...`
Expected: FAIL to compile, `undefined: Sync`.

- [ ] **Step 3: Implement**

`account/link.go`:
```go
package account

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// sharedDenyList names the main config dir's entries that stay per
// account: its credentials and account state, and the runtime state of the
// Claude processes running under it. Everything else is linked, which is
// why this is a deny-list: a user file such as an RTK.md that CLAUDE.md
// imports by relative path must follow too.
var sharedDenyList = map[string]bool{
	".credentials.json": true,
	".claude.json":      true,
	"sessions":          true,
	"daemon":            true,
	"session-env":       true,
	"ide":               true,
	"debug":             true,
	"cache":             true,
	"backups":           true,
	"shell-snapshots":   true,
	"statsig":           true,
}

// shared reports whether main-dir entry name is linked into account dirs.
// Besides the deny-list, .claude.json's siblings (its backups and
// atomic-write temp files) stay per account too.
func shared(name string) bool {
	return !sharedDenyList[name] && !strings.HasPrefix(name, ".claude.json")
}

// SyncReport is what one Sync did. Both lists are sorted.
type SyncReport struct {
	// Linked lists the entries this run linked.
	Linked []string
	// Diverged lists entries that are real files or directories in the
	// account dir although the main dir has them too: something replaced
	// the link (a tool rewriting the file atomically through it, say).
	// Sync leaves them alone, since the account's copy may hold the only
	// copy of a change.
	Diverged []string
}

// Sync links every shared entry of mainDir that acctDir does not have yet.
// Idempotent. Existing links are left as they are, wherever they point,
// and a real file or directory is never replaced.
func Sync(acctDir, mainDir string) (SyncReport, error) {
	var rep SyncReport
	entries, err := os.ReadDir(mainDir)
	if err != nil {
		return rep, fmt.Errorf("read main config dir %s: %w", mainDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !shared(name) {
			continue
		}
		link := filepath.Join(acctDir, name)
		fi, err := os.Lstat(link)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Symlink(filepath.Join(mainDir, name), link); err != nil {
				return rep, fmt.Errorf("link %s: %w", name, err)
			}
			rep.Linked = append(rep.Linked, name)
		case err != nil:
			return rep, fmt.Errorf("inspect %s: %w", link, err)
		case fi.Mode()&fs.ModeSymlink == 0:
			rep.Diverged = append(rep.Diverged, name)
		}
	}
	return rep, nil
}

// Create registers a new account called name: it makes the account's
// config dir under AccountsDir, links mainDir's shared entries into it,
// and records it. The dir must not exist yet. Logging in is the caller's
// next step.
func (r *Registry) Create(name, mainDir string) (Account, SyncReport, error) {
	if err := ValidName(name); err != nil {
		return Account{}, SyncReport{}, err
	}
	if r.loadErr != nil {
		return Account{}, SyncReport{}, fmt.Errorf("%w: %v", ErrRegistryLoadFailed, r.loadErr)
	}
	if _, ok := r.Get(name); ok {
		return Account{}, SyncReport{}, fmt.Errorf("account %q already exists", name)
	}
	acct := Account{Name: name, Dir: filepath.Join(r.AccountsDir(), name)}
	if _, err := os.Lstat(acct.Dir); err == nil {
		return Account{}, SyncReport{}, fmt.Errorf("%s already exists; remove it or pick another name", acct.Dir)
	}
	if err := os.MkdirAll(acct.Dir, 0o700); err != nil {
		return Account{}, SyncReport{}, fmt.Errorf("create %s: %w", acct.Dir, err)
	}
	rep, err := Sync(acct.Dir, mainDir)
	if err == nil {
		err = r.update(func(fresh *Registry) error {
			if _, ok := fresh.Get(name); ok {
				return fmt.Errorf("account %q already exists", name)
			}
			fresh.Accounts = append(fresh.Accounts, acct)
			return nil
		})
	}
	if err != nil {
		// Nothing but links lives there yet, and RemoveAll never follows them.
		_ = os.RemoveAll(acct.Dir)
		return Account{}, SyncReport{}, err
	}
	return acct, rep, nil
}

// Remove unregisters name and deletes its config dir, which holds that
// account's credentials and state plus links into the main dir. RemoveAll
// deletes the links themselves, never what they point at. A dir outside
// AccountsDir (a hand-edited registry) is unregistered but not deleted;
// deleted reports which happened.
func (r *Registry) Remove(name string) (deleted bool, err error) {
	if name == DefaultName {
		return false, fmt.Errorf("the %q account cannot be removed", DefaultName)
	}
	acct, ok := r.Get(name)
	if !ok {
		return false, fmt.Errorf("account %q is not registered", name)
	}
	if err := r.update(func(fresh *Registry) error {
		kept := fresh.Accounts[:0]
		for _, a := range fresh.Accounts {
			if a.Name != name {
				kept = append(kept, a)
			}
		}
		fresh.Accounts = kept
		if fresh.DefaultAccount == name {
			fresh.DefaultAccount = ""
		}
		return nil
	}); err != nil {
		return false, err
	}
	if !strings.HasPrefix(acct.Dir, r.AccountsDir()+string(filepath.Separator)) {
		return false, nil
	}
	if err := os.RemoveAll(acct.Dir); err != nil {
		return false, fmt.Errorf("account %q unregistered, but deleting %s failed: %w", name, acct.Dir, err)
	}
	return true, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./account/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w account/*.go
git add account/
git commit -m "feat(account): link the main config dir into account dirs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Auth status and login

**Files:**
- Create: `account/auth.go`
- Test: `account/auth_test.go`

- [ ] **Step 1: Write the failing tests**

`account/auth_test.go`:
```go
package account

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingExec answers every command with out/err and records the last
// one, including what it would have read on stdin.
type recordingExec struct {
	out   []byte
	err   error
	cmd   *exec.Cmd
	stdin string
}

func (f *recordingExec) record(c *exec.Cmd) {
	f.cmd = c
	if c.Stdin != nil {
		b, _ := io.ReadAll(c.Stdin)
		f.stdin = string(b)
	}
}
func (f *recordingExec) Run(c *exec.Cmd) error                      { f.record(c); return f.err }
func (f *recordingExec) Output(c *exec.Cmd) ([]byte, error)         { f.record(c); return f.out, f.err }
func (f *recordingExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { f.record(c); return f.out, f.err }

// envValue returns key's effective value in env: the last one, as os/exec
// resolves duplicates.
func envValue(env []string, key string) (string, bool) {
	val, found := "", false
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			val, found = strings.TrimPrefix(kv, key+"="), true
		}
	}
	return val, found
}

// authJSON is `claude auth status` output from Claude Code 2.1.281.
const authJSON = `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","analyticsDisabled":false,"projectsDirectory":"/home/u/.claude/projects","configDirectory":"/home/u/.claude","email":"you@example.com","orgId":"o","orgName":"Org","subscriptionType":"max"}`

func TestAuthStatus_DecodesTheIdentityUnderTheAccountEnv(t *testing.T) {
	f := &recordingExec{out: []byte(authJSON)}

	id, err := AuthStatus("/nix/store/x/bin/claude --model opus", EnvFor("/acct/max-2"), f)

	require.NoError(t, err)
	assert.Equal(t, Identity{LoggedIn: true, AuthMethod: "claude.ai", Email: "you@example.com", OrgName: "Org", Plan: "max", ConfigDir: "/home/u/.claude"}, id)
	assert.Equal(t, []string{"/nix/store/x/bin/claude", "auth", "status"}, f.cmd.Args)
	dir, ok := envValue(f.cmd.Env, "CLAUDE_CONFIG_DIR")
	assert.True(t, ok)
	assert.Equal(t, "/acct/max-2", dir)
}

func TestAuthStatus_DefaultAccountInheritsLoomsEnv(t *testing.T) {
	f := &recordingExec{out: []byte(authJSON)}
	_, err := AuthStatus("claude", nil, f)
	require.NoError(t, err)
	assert.Nil(t, f.cmd.Env, "nil Env inherits loom's own environment unchanged")
}

func TestAuthStatus_LoggedOutStillDecodes(t *testing.T) {
	// Logged out, the CLI prints its JSON and exits non-zero.
	f := &recordingExec{out: []byte(`{"loggedIn":false,"authMethod":"none","configDirectory":"/acct/x"}`), err: errors.New("exit status 1")}
	id, err := AuthStatus("claude", EnvFor("/acct/x"), f)
	require.NoError(t, err)
	assert.False(t, id.LoggedIn)
	assert.Equal(t, "/acct/x", id.ConfigDir)
}

func TestAuthStatus_Failures(t *testing.T) {
	_, err := AuthStatus("", nil, &recordingExec{})
	assert.Error(t, err, "no program")
	_, err = AuthStatus("claude", nil, &recordingExec{err: errors.New("not found")})
	assert.Error(t, err, "failed with no output")
	_, err = AuthStatus("claude", nil, &recordingExec{out: []byte("not json")})
	assert.Error(t, err, "unparseable output")
}

func TestLoginCmd(t *testing.T) {
	c := LoginCmd("claude --model opus", EnvFor("/acct/max-2"))
	assert.Equal(t, []string{"claude", "auth", "login"}, c.Args)
	dir, ok := envValue(c.Env, "CLAUDE_CONFIG_DIR")
	assert.True(t, ok)
	assert.Equal(t, "/acct/max-2", dir)
}

func TestBinary(t *testing.T) {
	assert.Equal(t, "/nix/store/x/bin/claude", Binary("/nix/store/x/bin/claude --model opus"))
	assert.Equal(t, "", Binary("  "))
}

func TestMainDir(t *testing.T) {
	assert.Equal(t, "/reported", MainDir(Identity{ConfigDir: "/reported"}))

	t.Setenv("CLAUDE_CONFIG_DIR", "/from-env")
	assert.Equal(t, "/from-env", MainDir(Identity{}))

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".claude"), MainDir(Identity{}))
}
```
(`TestMainDir` needs `"os"` and `"path/filepath"` in the test imports.)

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./account/...`
Expected: FAIL to compile, `undefined: AuthStatus`.

- [ ] **Step 3: Implement**

`account/auth.go`:
```go
package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// Identity is the subset of `claude auth status` loom reads. Field names
// match the CLI's JSON (Claude Code 2.1.281).
type Identity struct {
	LoggedIn   bool   `json:"loggedIn"`
	AuthMethod string `json:"authMethod"`
	Email      string `json:"email"`
	OrgName    string `json:"orgName"`
	Plan       string `json:"subscriptionType"`
	// ConfigDir is the config dir the CLI resolved. For the default
	// account it is the main dir extra accounts link to.
	ConfigDir string `json:"configDirectory"`
}

// authTimeout bounds `claude auth status`, a local read.
const authTimeout = 5 * time.Second

// Binary is the executable of a program string: its first field. The
// account commands run the same CLI the sessions launch, so an absolute or
// Nix store path resolves the same way.
func Binary(program string) string {
	fields := strings.Fields(program)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func runner(r internalexec.Executor) internalexec.Executor {
	if r == nil {
		return internalexec.Default{}
	}
	return r
}

// withEnv gives c loom's own environment plus env. os/exec keeps the last
// value of a duplicated key, so env overrides a CLAUDE_CONFIG_DIR loom
// itself inherited. A nil env leaves c.Env nil, which inherits unchanged.
func withEnv(c *exec.Cmd, env []string) *exec.Cmd {
	if env != nil {
		c.Env = append(os.Environ(), env...)
	}
	return c
}

// AuthStatus runs `claude auth status` under env and decodes it. A logged
// out account still prints its JSON (and exits non-zero), so output is
// decoded whenever there is some.
func AuthStatus(program string, env []string, r internalexec.Executor) (Identity, error) {
	bin := Binary(program)
	if bin == "" {
		return Identity{}, errors.New("no claude program configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()
	out, err := runner(r).Output(withEnv(exec.CommandContext(ctx, bin, "auth", "status"), env))
	if err != nil && len(out) == 0 {
		return Identity{}, fmt.Errorf("claude auth status: %w", err)
	}
	var id Identity
	if jerr := json.Unmarshal(out, &id); jerr != nil {
		return Identity{}, fmt.Errorf("parse claude auth status: %w", jerr)
	}
	return id, nil
}

// LoginCmd is `claude auth login` under env. It is an interactive browser
// flow, so the caller runs it in the foreground terminal: the CLI
// directly, the TUI through tea.ExecProcess.
func LoginCmd(program string, env []string) *exec.Cmd {
	return withEnv(exec.Command(Binary(program), "auth", "login"), env)
}

// MainDir is the main config dir extra accounts link to, given the default
// account's identity: the dir `claude auth status` reported, else
// $CLAUDE_CONFIG_DIR (which the default account inherits), else ~/.claude.
// "" when none can be found.
func MainDir(id Identity) string {
	if id.ConfigDir != "" {
		return id.ConfigDir
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude")
	}
	return ""
}
```
(add `"path/filepath"` to `auth.go`'s imports).

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./account/... && CGO_ENABLED=0 go test ./internal/exec/...`
Expected: PASS (`TestNoRawGitGhExec` only flags literal `git`/`gh`).

- [ ] **Step 5: Commit**

```bash
gofmt -w account/*.go
git add account/
git commit -m "feat(account): read an account's identity and log it in

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Usage probe

**Files:**
- Create: `account/usage.go`
- Create: `account/testdata/get_usage_2.1.281.jsonl`
- Test: `account/usage_test.go`

- [ ] **Step 1: Add the fixture**

`account/testdata/get_usage_2.1.281.jsonl` (one line; captured from Claude Code 2.1.281 on 2026-09-25, trimmed, `request_id` rewritten to loom's):
```
{"type":"control_response","response":{"subtype":"success","request_id":"loom-usage","response":{"session":{"total_cost_usd":0,"total_api_duration_ms":0,"total_duration_ms":588,"total_lines_added":0,"total_lines_removed":0,"model_usage":{}},"subscription_type":"max","rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":8,"resets_at":"2026-09-25T11:20:00.356471+00:00","limit_dollars":null,"used_dollars":null,"remaining_dollars":null,"locked_reason":null},"seven_day":{"utilization":30,"resets_at":"2026-09-29T23:00:00.356491+00:00","limit_dollars":null,"used_dollars":null,"remaining_dollars":null,"locked_reason":null},"seven_day_oauth_apps":null,"seven_day_opus":null,"seven_day_sonnet":null,"cinder_cove":null,"extra_usage":{"is_enabled":false,"monthly_limit":null,"used_credits":null,"utilization":null,"currency":null,"disabled_reason":null,"decimal_places":null,"user_disabled":false,"spend_limit_reached":false,"credits_ever_enabled":false,"daily":null,"weekly":null},"limits":[{"kind":"session","group":"session","percent":8,"resets_at":"2026-09-25T11:20:00.356471+00:00","severity":"normal","is_active":false,"scope":null},{"kind":"weekly_all","group":"weekly","percent":30,"resets_at":"2026-09-29T23:00:00.356491+00:00","severity":"normal","is_active":true,"scope":null}],"nimbus_quill":{"utilization":0,"resets_at":null,"limit_dollars":null,"used_dollars":null,"remaining_dollars":null,"locked_reason":null}}}}
```

- [ ] **Step 2: Write the failing tests**

`account/usage_test.go`:
```go
package account

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	out, err := os.ReadFile(filepath.Join("testdata", "get_usage_2.1.281.jsonl"))
	require.NoError(t, err)
	return out
}

func TestDecodeUsage_RealResponse(t *testing.T) {
	u, err := decodeUsage(fixture(t))
	require.NoError(t, err)

	assert.True(t, u.Available)
	assert.Equal(t, "max", u.Plan)
	require.NotNil(t, u.FiveHour)
	require.NotNil(t, u.SevenDay)
	assert.Equal(t, 8.0, u.FiveHour.Pct)
	assert.Equal(t, 30.0, u.SevenDay.Pct)
	assert.True(t, u.FiveHour.ResetsAt.Equal(time.Date(2026, 9, 25, 11, 20, 0, 356471000, time.UTC)))
}

func TestDecodeUsage_SkipsNoiseAndOtherRequests(t *testing.T) {
	out := `{"type":"system","subtype":"hook_started"}
not json
{"type":"control_response","response":{"subtype":"success","request_id":"someone-else","response":{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":99}}}}}
{"type":"control_response","response":{"subtype":"success","request_id":"loom-usage","response":{"subscription_type":"pro","rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":12.4,"resets_at":null},"seven_day":null}}}}
`
	u, err := decodeUsage([]byte(out))
	require.NoError(t, err)
	assert.Equal(t, "pro", u.Plan)
	require.NotNil(t, u.FiveHour)
	assert.Equal(t, 12.4, u.FiveHour.Pct)
	assert.True(t, u.FiveHour.ResetsAt.IsZero())
	assert.Nil(t, u.SevenDay)
}

func TestDecodeUsage_NoPlanLimits(t *testing.T) {
	out := `{"type":"control_response","response":{"subtype":"success","request_id":"loom-usage","response":{"subscription_type":null,"rate_limits_available":false,"rate_limits":null}}}`
	u, err := decodeUsage([]byte(out))
	require.NoError(t, err)
	assert.False(t, u.Available)
	assert.Empty(t, u.Plan)
	assert.Nil(t, u.FiveHour)
	assert.Nil(t, u.SevenDay)
}

func TestDecodeUsage_ErrorResponse(t *testing.T) {
	out := `{"type":"control_response","response":{"subtype":"error","request_id":"loom-usage","error":"boom"}}`
	_, err := decodeUsage([]byte(out))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestDecodeUsage_NoResponse(t *testing.T) {
	_, err := decodeUsage([]byte(`{"type":"system"}`))
	assert.ErrorIs(t, err, errNoUsageResponse)
}

func TestProbeUsage_RunsAHeadlessControlRequest(t *testing.T) {
	f := &recordingExec{out: fixture(t)}
	before := time.Now()

	u, err := ProbeUsage("claude --model opus", EnvFor("/acct/max-2"), "/acct/max-2", f)

	require.NoError(t, err)
	assert.False(t, u.At.Before(before), "At is when the probe started")
	assert.Equal(t, []string{"claude", "-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--no-session-persistence", "--setting-sources", ""}, f.cmd.Args)
	assert.Equal(t, "/acct/max-2", f.cmd.Dir)
	assert.Equal(t, usageRequest, f.stdin)
	dir, _ := envValue(f.cmd.Env, "CLAUDE_CONFIG_DIR")
	assert.Equal(t, "/acct/max-2", dir)
}

func TestProbeUsage_ExitWithoutResponseFails(t *testing.T) {
	_, err := ProbeUsage("claude", nil, "", &recordingExec{err: errors.New("exit status 1")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 1")
}

func TestWindowText(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	var none *Window
	assert.Equal(t, "", none.Text(now))
	assert.Equal(t, "64%", (&Window{Pct: 63.6, ResetsAt: now.Add(time.Hour)}).Text(now))
	assert.Equal(t, "reset", (&Window{Pct: 90, ResetsAt: now.Add(-time.Minute)}).Text(now))
	assert.Equal(t, "12%", (&Window{Pct: 12}).Text(now), "no reset time means no reset")
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./account/...`
Expected: FAIL to compile, `undefined: decodeUsage`.

- [ ] **Step 4: Implement**

`account/usage.go`:
```go
package account

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// Window is one plan rate-limit window.
type Window struct {
	// Pct is the share of the window used, 0-100.
	Pct float64
	// ResetsAt is when the window resets; zero when the server gave none.
	ResetsAt time.Time
}

// Text renders w for display: "64%", or "reset" once ResetsAt has passed
// (the percentage would describe a window that no longer exists). "" for a
// nil window.
func (w *Window) Text(now time.Time) string {
	if w == nil {
		return ""
	}
	if !w.ResetsAt.IsZero() && !now.Before(w.ResetsAt) {
		return "reset"
	}
	return fmt.Sprintf("%.0f%%", w.Pct)
}

// Usage is one account's plan usage, from ProbeUsage.
type Usage struct {
	// Available is false when plan limits do not apply (API key, Bedrock,
	// Vertex); the windows are then nil.
	Available bool
	// Plan is the subscription ("pro", "max", …); empty for API-key auth.
	Plan string
	// FiveHour and SevenDay are nil when the server did not report them.
	FiveHour, SevenDay *Window
	// At is when the probe started. Zero means never probed.
	At time.Time
}

// usageTimeout bounds one probe. It measured ~1.4s on 2.1.281, but the
// usage endpoint is a network call, so this is a network budget.
const usageTimeout = 15 * time.Second

const usageRequestID = "loom-usage"

// usageRequest is the SDK control request for the structured /usage data.
// skip_behaviors skips a scan of a week of local transcripts that only the
// /usage dialog needs; the CLI's own schema describes it as being "for
// callers that need only the plan rate limits, such as a usage meter".
// get_usage is marked experimental, so decodeUsage reads only the
// documented rate_limits windows.
const usageRequest = `{"type":"control_request","request_id":"` + usageRequestID +
	`","request":{"subtype":"get_usage","skip_behaviors":true}}` + "\n"

// ProbeUsage asks the account's CLI for its plan usage without starting a
// conversation: a headless `claude -p` over stream-json that answers the
// one control request on stdin and exits when stdin closes. No model call,
// no cost. The CLI answers from its own usage snapshot when that is under
// a minute old, so polling does not hammer the usage endpoint.
// --setting-sources "" keeps user and project settings, and the plugin
// hooks they enable, out of the probe (auth is read from the config dir
// regardless); --no-session-persistence writes no transcript. cwd is where
// the probe runs: pass the account's config dir so no project entry is
// recorded for an arbitrary directory.
func ProbeUsage(program string, env []string, cwd string, r internalexec.Executor) (Usage, error) {
	bin := Binary(program)
	if bin == "" {
		return Usage{}, errors.New("no claude program configured")
	}
	at := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	defer cancel()
	c := withEnv(exec.CommandContext(ctx, bin,
		"-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--no-session-persistence", "--setting-sources", ""), env)
	c.Dir = cwd
	c.Stdin = strings.NewReader(usageRequest)
	out, err := runner(r).Output(c)
	u, derr := decodeUsage(out)
	if derr != nil {
		if err != nil {
			return Usage{}, fmt.Errorf("claude usage probe: %w (%v)", err, derr)
		}
		return Usage{}, derr
	}
	u.At = at
	return u, nil
}

// controlLine is one stream-json line, decoded far enough to find loom's
// control_response.
type controlLine struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Error     string          `json:"error"`
		Response  json.RawMessage `json:"response"`
	} `json:"response"`
}

type usagePayload struct {
	SubscriptionType    *string `json:"subscription_type"`
	RateLimitsAvailable bool    `json:"rate_limits_available"`
	RateLimits          *struct {
		FiveHour *rawWindow `json:"five_hour"`
		SevenDay *rawWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

type rawWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

var errNoUsageResponse = errors.New("claude usage probe: no get_usage response")

// decodeUsage finds loom's control_response among the stream-json lines
// (hook events and the like may precede it) and decodes its windows.
func decodeUsage(out []byte) (Usage, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var line controlLine
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "control_response" ||
			line.Response.RequestID != usageRequestID {
			continue
		}
		if line.Response.Subtype != "success" {
			return Usage{}, fmt.Errorf("claude usage probe: get_usage failed: %s", line.Response.Error)
		}
		var p usagePayload
		if err := json.Unmarshal(line.Response.Response, &p); err != nil {
			return Usage{}, fmt.Errorf("claude usage probe: decode response: %w", err)
		}
		u := Usage{Available: p.RateLimitsAvailable}
		if p.SubscriptionType != nil {
			u.Plan = *p.SubscriptionType
		}
		if p.RateLimits != nil {
			u.FiveHour = p.RateLimits.FiveHour.window()
			u.SevenDay = p.RateLimits.SevenDay.window()
		}
		return u, nil
	}
	return Usage{}, errNoUsageResponse
}

// window converts a reported window: nil when absent or without a
// utilization. An unparseable reset time is dropped, not fatal.
func (w *rawWindow) window() *Window {
	if w == nil || w.Utilization == nil {
		return nil
	}
	out := &Window{Pct: *w.Utilization}
	if w.ResetsAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *w.ResetsAt); err == nil {
			out.ResetsAt = t
		}
	}
	return out
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./account/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w account/*.go
git add account/
git commit -m "feat(account): probe plan usage with a headless get_usage request

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Opt-in real-CLI contract test

**Files:**
- Test: `account/usage_real_test.go`

- [ ] **Step 1: Write the test**

`account/usage_real_test.go`:
```go
package account

import (
	"os"
	"os/exec"
	"testing"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_UsageProbe probes the installed CLI's default account, so
// a change to the experimental get_usage response shows up on upgrade. It
// costs nothing (no model call) but needs a logged-in claude.ai account:
//
//	LOOM_TEST_REAL_CLAUDE=1 go test ./account -run TestRealClaude
func TestRealClaude_UsageProbe(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to probe the installed claude CLI")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	id, err := AuthStatus(bin, nil, internalexec.Default{})
	require.NoError(t, err)
	if !id.LoggedIn {
		t.Skip("the default account is not logged in")
	}

	u, err := ProbeUsage(bin, nil, id.ConfigDir, internalexec.Default{})

	require.NoError(t, err)
	if !u.Available {
		t.Skipf("no plan rate limits for auth method %q", id.AuthMethod)
	}
	assert.NotEmpty(t, u.Plan)
	require.True(t, u.FiveHour != nil || u.SevenDay != nil, "a subscriber reports at least one window")
	for _, w := range []*Window{u.FiveHour, u.SevenDay} {
		if w != nil {
			assert.GreaterOrEqual(t, w.Pct, 0.0)
		}
	}
}
```

- [ ] **Step 2: Run it skipped, then for real**

Run: `CGO_ENABLED=0 go test ./account -run TestRealClaude -v`
Expected: `SKIP` (env unset).

Run: `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./account -run TestRealClaude -v`
Expected: PASS on a machine logged in to a claude.ai subscription (SKIP otherwise).

- [ ] **Step 3: Commit**

```bash
gofmt -w account/usage_real_test.go
git add account/usage_real_test.go
git commit -m "test(account): opt-in contract test for the usage probe

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Phase B — session

### Task 6: `LaunchEnv` and `CLAUDE_CONFIG_DIR`

**Files:**
- Modify: `session/agent_restart.go:128-137`
- Modify: `session/instance.go:394`, `session/instance.go:800`, `session/reconcile.go:278`, `session/subagent_hooks.go:124`
- Test: `session/agent_restart_test.go:224-237`, `session/subagent_hooks_test.go:171,427`

- [ ] **Step 1: Rewrite the InstanceEnv tests for the new signature and add the config-dir case**

In `session/agent_restart_test.go`, replace `TestInstanceEnv_CombinesBothTogglesIndependently` and `TestInstanceEnv_NonClaudeIsEmpty` with:
```go
func TestInstanceEnv_CombinesBothTogglesIndependently(t *testing.T) {
	const fullscreen = "CLAUDE_CODE_NO_FLICKER=1"
	assert.Equal(t, []string{fullscreen}, InstanceEnv(LaunchEnv{Program: "claude"}))
	assert.Equal(t, []string{"ANTHROPIC_BASE_URL=http://127.0.0.1:8787", fullscreen},
		InstanceEnv(LaunchEnv{Program: "claude", HeadroomProxy: true}))
	assert.Equal(t, []string{"ENABLE_PROMPT_CACHING_1H=1", fullscreen},
		InstanceEnv(LaunchEnv{Program: "claude", CacheTTL1h: true}))
	assert.Equal(t,
		[]string{"ANTHROPIC_BASE_URL=http://127.0.0.1:8787", "ENABLE_PROMPT_CACHING_1H=1", fullscreen},
		InstanceEnv(LaunchEnv{Program: "claude", HeadroomProxy: true, CacheTTL1h: true}),
	)
}

func TestInstanceEnv_NonClaudeIsEmpty(t *testing.T) {
	assert.Empty(t, InstanceEnv(LaunchEnv{Program: "aider --model gemma", HeadroomProxy: true, CacheTTL1h: true, ClaudeConfigDir: "/acct"}))
}

func TestInstanceEnv_ClaudeConfigDirComesLast(t *testing.T) {
	assert.Equal(t,
		[]string{"CLAUDE_CODE_NO_FLICKER=1", "CLAUDE_CONFIG_DIR=/acct/max-2"},
		InstanceEnv(LaunchEnv{Program: "claude --model opus", ClaudeConfigDir: "/acct/max-2"}),
	)
}
```

In `session/subagent_hooks_test.go`, change the two expectations:
- line 171: `InstanceEnv("claude --continue", true, true)` → `InstanceEnv(LaunchEnv{Program: "claude --continue", HeadroomProxy: true, CacheTTL1h: true})`
- line 427: `InstanceEnv("claude --resume "+id, false, false)` → `InstanceEnv(LaunchEnv{Program: "claude --resume " + id})`

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/ -run 'InstanceEnv|RecoveryLaunch'`
Expected: FAIL to compile, `undefined: LaunchEnv`.

- [ ] **Step 3: Implement**

In `session/agent_restart.go`, replace the `InstanceEnv` doc comment and function (lines 128-137) with:
```go
// LaunchEnv is everything about an instance's launch that becomes tmux
// session environment rather than part of the program string.
type LaunchEnv struct {
	Program       string
	HeadroomProxy bool
	CacheTTL1h    bool
	// ClaudeConfigDir is the CLAUDE_CONFIG_DIR of the account the session
	// runs on; empty for the default account.
	ClaudeConfigDir string
}

// ClaudeConfigDirEnv returns the tmux session environment variable that
// runs Claude as the account whose config dir is dir. A no-op (nil) for the
// default account (empty dir) and for every program but Claude.
func ClaudeConfigDirEnv(dir, program string) []string {
	if dir == "" || !IsClaudeProgram(program) {
		return nil
	}
	return []string{"CLAUDE_CONFIG_DIR=" + dir}
}

// InstanceEnv combines every per-session environment variable derived
// from an instance's launch (Headroom Proxy, Cache TTL, the account's
// config dir) plus the always-on Claude fullscreen renderer into the
// single slice tmux.NewTmuxSession's variadic env parameter needs.
// Centralized here so the four Instance call sites that construct a
// TmuxSession don't each repeat the same combination.
func InstanceEnv(e LaunchEnv) []string {
	env := append(HeadroomProxyEnv(e.HeadroomProxy, e.Program), CacheTTL1hEnv(e.CacheTTL1h, e.Program)...)
	env = append(env, ClaudeFullscreenEnv(e.Program)...)
	return append(env, ClaudeConfigDirEnv(e.ClaudeConfigDir, e.Program)...)
}
```

Update the four call sites (the account dir is wired in Task 8; for now they keep today's behavior):
- `session/instance.go:394` → `instance.setTmuxSession(tmux.NewTmuxSession(instance.Title, instance.program, InstanceEnv(LaunchEnv{Program: instance.program, HeadroomProxy: instance.headroomProxy, CacheTTL1h: instance.cacheTTL1h})...))`
- `session/reconcile.go:278` → the same expression.
- `session/instance.go:800` → `ts = tmux.NewTmuxSession(i.Title, launchProgram, InstanceEnv(LaunchEnv{Program: program, HeadroomProxy: headroomProxy, CacheTTL1h: cacheTTL1h})...)`
- `session/subagent_hooks.go:124` → `return i.launchProgram(program, true), InstanceEnv(LaunchEnv{Program: program, HeadroomProxy: headroomProxy, CacheTTL1h: cacheTTL1h})`

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./session/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w session/agent_restart.go session/instance.go session/reconcile.go session/subagent_hooks.go session/agent_restart_test.go session/subagent_hooks_test.go
git add session/
git commit -m "refactor(session): launch env as a LaunchEnv struct with CLAUDE_CONFIG_DIR

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: `Instance.account` and schema v8

**Files:**
- Modify: `session/instance.go` (struct near line 147, accessors near line 693, `Snapshot` line 294-301, `FromInstanceData` line 350-356)
- Modify: `session/storage.go:32` and `InstanceData` (after line 66)
- Modify: `session/storage_migrate.go` (doc comment + `case 7`)
- Modify: `cmd/workspace_migrate.go:47-48`, `cmd/workspace_migrate_shape_test.go:48-49`
- Test: `session/storage_migrate_test.go`, `session/account_env_test.go` (new)

- [ ] **Step 1: Write the failing tests**

Append to `session/storage_migrate_test.go`:
```go
func TestMigrate_V7UpgradesToTheDefaultAccount(t *testing.T) {
	raw := []byte(`{"schema_version":7,"title":"t","path":"/p","branch":"b","status":0,"height":1,"width":1,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","program":"claude","worktree":{},"diff_stats":{},"is_workspace_terminal":false}`)
	data, err := Migrate(raw)
	require.NoError(t, err)
	assert.Equal(t, CurrentSchemaVersion, data.SchemaVersion)
	assert.Equal(t, "", data.Account, "absent means the default account")
}
```

Create `session/account_env_test.go`:
```go
package session

import (
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstanceAccount_DefaultNameIsStoredEmpty(t *testing.T) {
	inst := &Instance{}
	inst.SetAccount("max-2")
	assert.Equal(t, "max-2", inst.Account())
	inst.SetAccount(account.DefaultName)
	assert.Equal(t, "", inst.Account())
}

func TestInstanceAccount_SurvivesSnapshotAndRestore(t *testing.T) {
	inst := &Instance{Title: "acct-roundtrip", program: "claude"}
	inst.SetAccount("max-2")

	data := inst.Snapshot()
	require.Equal(t, "max-2", data.Account)
	restored, err := FromInstanceData(data, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "max-2", restored.Account())
}
```

In `cmd/workspace_migrate_shape_test.go`, add a line to the fixture after `"claude_transcript_path": …` (add a comma to that line):
```
		"claude_transcript_path": "/home/u/.claude/projects/-wt/8c634184-0fe5-4b62-b437-8f364eeeefcc.jsonl",
		"account": "max-2"
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/ -run 'Migrate_V7|InstanceAccount' ; CGO_ENABLED=0 go test ./cmd/ -run MigrationInstance`
Expected: session FAILS to compile (`inst.SetAccount undefined`); cmd FAILS (`TestMigrationInstance_MirrorsInstanceData_JSON`: the mirror drops `account`).

- [ ] **Step 3: Implement**

`session/instance.go`, in the `Instance` struct right after the `cacheTTL1h bool` field:
```go
	// account is the Claude account this instance launches under: a
	// registered account's name, or "" for the default account. Every real
	// launch resolves it to a CLAUDE_CONFIG_DIR (see launchEnv). Set with
	// SetAccount alongside SetLaunchOptions; persisted (InstanceData v8).
	account string
```
Also add `account` to the list of launch fields in the `mu` doc comment (the sentence ending "…program/headroomProxy/cacheTTL1h, which Start/Resume/CrashRestart read via launchSpec)" becomes "…program/headroomProxy/cacheTTL1h/account, which Start/Resume/CrashRestart read via launchEnv)").

After `SetLaunchOptions` (line ~699), add:
```go
// Account returns the Claude account this instance launches under: a
// registered account's name, or "" for the default account.
func (i *Instance) Account() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.account
}

// SetAccount records the account the next launch runs under.
// account.DefaultName and "" both mean the default account.
func (i *Instance) SetAccount(name string) {
	if name == account.DefaultName {
		name = ""
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.account = name
}
```
Add the import `"github.com/aidan-bailey/loom/account"` to `session/instance.go`.

In `Snapshot`, after `Issue: i.issue,` add `Account: i.account,`.
In `FromInstanceData`, after `issue: data.Issue,` add `account: data.Account,`.

`session/storage.go`: `const CurrentSchemaVersion = 8`, and at the end of `InstanceData`:
```go
	// Account is the Claude account the session runs on ("" = default),
	// resolved to a CLAUDE_CONFIG_DIR at every launch. Added in schema v8.
	Account string `json:"account,omitempty"`
```

`session/storage_migrate.go`: add to the step list in the doc comment `//   - v7 → v8: Account added.` (match the list's existing format) and, before `default:`, add:
```go
		case 7:
			// v7 → v8: Account added. Empty (the default account) is the
			// correct default for pre-existing records — version stamp only.
			data.SchemaVersion = 8
```

`cmd/workspace_migrate.go`, in `migrationInstance` after `ClaudeTranscriptPath`:
```go
	Account              string `json:"account,omitempty"`
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/... ./cmd/...`
Expected: PASS (`TestMigrationInstance_TypeDriftGuard` sees matching tags).

- [ ] **Step 5: Commit**

```bash
gofmt -w session/instance.go session/storage.go session/storage_migrate.go session/storage_migrate_test.go session/account_env_test.go cmd/workspace_migrate.go cmd/workspace_migrate_shape_test.go
git add session/ cmd/
git commit -m "feat(session): persist each instance's Claude account (schema v8)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Launches resolve the account and fail closed

**Files:**
- Create: `session/account_env.go`
- Modify: `session/instance.go` (`launchSpec` → `launchEnv`, `Start` near 798, `FromInstanceData` 394, `startFreshWithRecovery` 1512, `CrashRestart` 1550)
- Modify: `session/subagent_hooks.go:115-125`, `session/reconcile.go:278`
- Test: `session/account_env_test.go`, `session/subagent_hooks_test.go:137,169,423`, `session/instance_race_test.go:134,178`

- [ ] **Step 1: Write the failing tests**

Append to `session/account_env_test.go` (add imports `errors`, `os`, `strings` as needed):
```go
// withAccountDirs publishes dirs for one test.
func withAccountDirs(t *testing.T, dirs map[string]string) {
	t.Helper()
	SetAccountDirs(dirs)
	t.Cleanup(func() { SetAccountDirs(nil) })
}

func TestAccountDir(t *testing.T) {
	withAccountDirs(t, map[string]string{"max-2": "/acct/max-2"})

	dir, err := accountDir("")
	require.NoError(t, err)
	assert.Empty(t, dir)
	dir, err = accountDir("max-2")
	require.NoError(t, err)
	assert.Equal(t, "/acct/max-2", dir)

	_, err = accountDir("gone")
	var missing *MissingAccountError
	require.True(t, errors.As(err, &missing))
	assert.Equal(t, "gone", missing.Name)
	assert.Contains(t, err.Error(), "press R")
}

func TestLaunchEnv_MissingAccountFailsOnlyWhenLaunching(t *testing.T) {
	withAccountDirs(t, nil)
	inst := &Instance{program: "claude"}
	inst.SetAccount("gone")

	_, err := inst.launchEnv(true)
	assert.Error(t, err, "a launch never falls back to the default account")

	env, err := inst.launchEnv(false)
	require.NoError(t, err, "building a detached session object launches nothing")
	assert.Empty(t, env.ClaudeConfigDir)
}

func TestLaunchEnv_NonClaudeIgnoresTheAccount(t *testing.T) {
	withAccountDirs(t, nil)
	inst := &Instance{program: "aider"}
	inst.SetAccount("gone")
	_, err := inst.launchEnv(true)
	assert.NoError(t, err)
}

func TestRecoveryLaunch_RunsAsTheAccount(t *testing.T) {
	withAccountDirs(t, map[string]string{"max-2": "/acct/max-2"})
	inst := hooksInstance(t, "claude")
	inst.SetAccount("max-2")

	_, env, err := inst.recoveryLaunch()

	require.NoError(t, err)
	assert.Contains(t, env, "CLAUDE_CONFIG_DIR=/acct/max-2")
}

func TestRecoveryLaunch_MissingAccountFails(t *testing.T) {
	withAccountDirs(t, nil)
	inst := hooksInstance(t, "claude")
	inst.SetAccount("gone")

	_, _, err := inst.recoveryLaunch()

	var missing *MissingAccountError
	assert.True(t, errors.As(err, &missing))
}

func TestStart_MissingAccountFailsBeforeAnySetup(t *testing.T) {
	withAccountDirs(t, nil)
	inst, err := NewInstance(InstanceOptions{Title: "acct-missing", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetAccount("gone")

	err = inst.Start(true)

	var missing *MissingAccountError
	require.True(t, errors.As(err, &missing))
	assert.False(t, inst.Started(), "the failed start releases its reservation")
}
```

Update existing callers of the changed signatures:
- `session/subagent_hooks_test.go:137`: `inst.recoveryLaunch()` → `_, _, _ = inst.recoveryLaunch()`
- `session/subagent_hooks_test.go:169` and `:423`: `launch, env := inst.recoveryLaunch()` → `launch, env, err := inst.recoveryLaunch()` followed by `require.NoError(t, err)`
- `session/instance_race_test.go:134` and `:178`: `program, hp, ttl := inst.launchSpec()` → `le, _ := inst.launchEnv(false)` then `program, hp, ttl := le.Program, le.HeadroomProxy, le.CacheTTL1h`

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/ -run 'AccountDir|LaunchEnv|RecoveryLaunch|Start_Missing'`
Expected: FAIL to compile, `undefined: SetAccountDirs`.

- [ ] **Step 3: Implement**

`session/account_env.go`:
```go
package session

import (
	"fmt"
	"sync/atomic"

	"github.com/aidan-bailey/loom/account"
)

// accountDirs maps each registered extra account to its config dir. app
// publishes it (SetAccountDirs) at startup and after every registry change;
// launches read it on lifecycle goroutines, hence the atomic pointer.
var accountDirs atomic.Pointer[map[string]string]

// SetAccountDirs publishes the account registry's name → config dir map.
// It copies dirs, so the caller may keep mutating its own.
func SetAccountDirs(dirs map[string]string) {
	cp := make(map[string]string, len(dirs))
	for k, v := range dirs {
		cp[k] = v
	}
	accountDirs.Store(&cp)
}

// MissingAccountError is a launch refused because the instance's account
// is no longer registered. A launch never falls back to the default
// account: that would bill the wrong subscription.
type MissingAccountError struct{ Name string }

func (e *MissingAccountError) Error() string {
	return fmt.Sprintf("account %q no longer exists — press R to relaunch on another account", e.Name)
}

// accountDir resolves an instance's account name to its config dir: "" for
// the default account, a *MissingAccountError for an unregistered one.
func accountDir(name string) (string, error) {
	if name == "" || name == account.DefaultName {
		return "", nil
	}
	if p := accountDirs.Load(); p != nil {
		if dir, ok := (*p)[name]; ok {
			return dir, nil
		}
	}
	return "", &MissingAccountError{Name: name}
}

// bestEffortAccountDir is accountDir for a session object that is built
// but not launched (a restored Paused record): an unresolvable account
// yields no config dir, and the real launch fails closed later.
func bestEffortAccountDir(name string) string {
	dir, _ := accountDir(name)
	return dir
}
```

`session/instance.go`: replace `launchSpec` (lines ~701-710, including its doc comment) with:
```go
// launchEnv snapshots the launch fields under one lock, so a concurrent
// SetLaunchOptions can't tear them, and resolves the account's config dir.
// When launching, an unregistered account on a Claude program is an error
// (*MissingAccountError); otherwise it just yields no config dir.
func (i *Instance) launchEnv(launching bool) (LaunchEnv, error) {
	i.mu.RLock()
	env := LaunchEnv{Program: i.program, HeadroomProxy: i.headroomProxy, CacheTTL1h: i.cacheTTL1h}
	name := i.account
	i.mu.RUnlock()
	dir, err := accountDir(name)
	if err != nil && launching && IsClaudeProgram(env.Program) {
		return env, err
	}
	env.ClaudeConfigDir = dir
	return env, nil
}
```

In `Start`, replace the three lines inside `if ts == nil {` (the `launchSpec` call through `ts = tmux.NewTmuxSession(...)`) with:
```go
		env, envErr := i.launchEnv(firstTimeSetup)
		if envErr != nil {
			setupErr = envErr
			return setupErr
		}
		launchProgram := i.launchProgram(env.Program, firstTimeSetup)
		ts = tmux.NewTmuxSession(i.Title, launchProgram, InstanceEnv(env)...)
```
(The comment above them stays; its last line becomes "InstanceEnv still keys off the bare program.")

In `FromInstanceData` (line 394) and `fromInstanceDataPaused` (`session/reconcile.go:278`), use:
```go
InstanceEnv(LaunchEnv{Program: instance.program, HeadroomProxy: instance.headroomProxy, CacheTTL1h: instance.cacheTTL1h, ClaudeConfigDir: bestEffortAccountDir(instance.account)})
```

`session/subagent_hooks.go`, replace `recoveryLaunch`:
```go
// recoveryLaunch returns the full launch command and the tmux session env
// for a recovery launch. The env keys off the recovery program (the bare
// program rewritten by BuildResumeCommand: --resume <id> for the recorded
// conversation, else --continue), not the full command.
// startFreshWithRecovery and CrashRestart always start a new Claude
// process, so an unregistered account fails the launch here.
func (i *Instance) recoveryLaunch() (launch string, env []string, err error) {
	le, err := i.launchEnv(true)
	if err != nil {
		return "", nil, err
	}
	sessionID, transcriptPath := i.ClaudeSession()
	le.Program = BuildResumeCommand(le.Program, sessionID, transcriptPath)
	return i.launchProgram(le.Program, true), InstanceEnv(le), nil
}
```

In `startFreshWithRecovery` and `CrashRestart`, replace `launchProgram, env := i.recoveryLaunch()` with:
```go
	launchProgram, env, err := i.recoveryLaunch()
	if err != nil {
		return err
	}
```
(In `CrashRestart` name the error so it doesn't shadow later `err`s; if the linter flags shadowing, use `launchErr`.)

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./session/... && CC=clang CGO_ENABLED=1 go test -race ./session/ -run 'Race|Account|LaunchEnv'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w session/*.go
git add session/
git commit -m "feat(session): launch as the instance's account; fail closed when it is gone

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Roster query and remote-control auth under an account env

**Files:**
- Modify: `session/claude_roster.go:92-116`
- Modify: `session/remote_control_auth.go`
- Test: `session/claude_roster_test.go`, `session/remote_control_auth_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `session/claude_roster_test.go`:
```go
// envRecordingExec records the env of the command it runs.
type envRecordingExec struct {
	out []byte
	env []string
}

func (f *envRecordingExec) Run(c *exec.Cmd) error { f.env = c.Env; return nil }
func (f *envRecordingExec) Output(c *exec.Cmd) ([]byte, error) {
	f.env = c.Env
	return f.out, nil
}
func (f *envRecordingExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return f.Output(c) }

func TestQueryClaudeRosterEnv_RunsAsTheAccount(t *testing.T) {
	f := &envRecordingExec{out: []byte(rosterJSON)}
	got, err := QueryClaudeRosterEnv("claude", []string{"CLAUDE_CONFIG_DIR=/acct/max-2"}, f)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	assert.Contains(t, f.env, "CLAUDE_CONFIG_DIR=/acct/max-2")
}

func TestQueryClaudeRoster_InheritsLoomsEnv(t *testing.T) {
	f := &envRecordingExec{out: []byte(rosterJSON)}
	_, err := QueryClaudeRoster("claude", f)
	require.NoError(t, err)
	assert.Nil(t, f.env)
}
```
(Add `"github.com/stretchr/testify/require"` to the file's imports if absent.)

Append to `session/remote_control_auth_test.go`:
```go
func TestDetectClaudeRemoteControlAuthEnv_CarriesTheIdentity(t *testing.T) {
	clearOverrideEnv(t)
	f := &envRecordingExec{out: []byte(`{"loggedIn":true,"authMethod":"claude.ai","email":"you@example.com","subscriptionType":"max","configDirectory":"/acct/max-2"}`)}

	got := DetectClaudeRemoteControlAuthEnv("claude", []string{"CLAUDE_CONFIG_DIR=/acct/max-2"}, f)

	assert.True(t, got.OK())
	assert.Equal(t, "you@example.com", got.Identity.Email)
	assert.Equal(t, "/acct/max-2", got.Identity.ConfigDir)
	assert.Contains(t, f.env, "CLAUDE_CONFIG_DIR=/acct/max-2")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/ -run 'RosterEnv|InheritsLoomsEnv|AuthEnv'`
Expected: FAIL to compile, `undefined: QueryClaudeRosterEnv`.

- [ ] **Step 3: Implement**

`session/claude_roster.go`: rename the body of `QueryClaudeRoster` into a new function and make the old one delegate. Replace the signature line and the `runner.Output(...)` line:
```go
// QueryClaudeRoster is QueryClaudeRosterEnv for the default account.
func QueryClaudeRoster(program string, runner internalexec.Executor) (map[string]RosterEntry, error) {
	return QueryClaudeRosterEnv(program, nil, runner)
}

// QueryClaudeRosterEnv runs `claude agents --json` with env appended to
// loom's own environment (nil: inherit unchanged) and returns its entries
// keyed by cwd. The roster lists only the sessions of the config dir the
// CLI runs under, so each account's sessions need a query run as that
// account (env = its CLAUDE_CONFIG_DIR).
func QueryClaudeRosterEnv(program string, env []string, runner internalexec.Executor) (map[string]RosterEntry, error) {
```
Keep the existing doc comment above `QueryClaudeRosterEnv` (move it there), and replace
```go
	out, err := runner.Output(exec.CommandContext(ctx, fields[0], "agents", "--json"))
```
with
```go
	c := exec.CommandContext(ctx, fields[0], "agents", "--json")
	if env != nil {
		c.Env = append(os.Environ(), env...)
	}
	out, err := runner.Output(c)
```
(add `"os"` to the imports).

`session/remote_control_auth.go`:
- Add `Identity account.Identity` to `RemoteControlAuth` with the comment `// Identity is what `+"`claude auth status`"+` reported; zero when it did not run or did not parse.` (plain `//` comment: "Identity is what claude auth status reported; zero when it did not run or did not parse.")
- Delete the `claudeAuthStatus` type and `remoteControlAuthTimeout` (account.AuthStatus owns both now).
- Replace `DetectClaudeRemoteControlAuth` with:
```go
// DetectClaudeRemoteControlAuth is DetectClaudeRemoteControlAuthEnv for
// the default account.
func DetectClaudeRemoteControlAuth(program string, runner internalexec.Executor) RemoteControlAuth {
	return DetectClaudeRemoteControlAuthEnv(program, nil, runner)
}

// DetectClaudeRemoteControlAuthEnv determines whether the Claude account
// selected by env (nil: the default account) can establish a
// --remote-control session, and carries the identity `claude auth status`
// reported. It is a no-op (Unknown) for non-Claude programs. Remote control
// requires a claude.ai OAuth login; API keys, Console accounts, and
// inference-scoped tokens are rejected by Claude, so this reports Blocked
// for them.
func DetectClaudeRemoteControlAuthEnv(program string, env []string, runner internalexec.Executor) RemoteControlAuth {
	if !IsClaudeProgram(program) {
		return RemoteControlAuth{State: RemoteControlAuthUnknown}
	}

	for _, name := range remoteControlOverrideEnv {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return RemoteControlAuth{
				State:  RemoteControlAuthBlocked,
				Reason: name + " is set — remote control needs a claude.ai login. Unset it, or run `claude auth login`.",
			}
		}
	}

	id, err := account.AuthStatus(program, env, runner)
	if err != nil {
		// Subcommand missing, no output, or unparseable — can't tell.
		return RemoteControlAuth{State: RemoteControlAuthUnknown}
	}
	if id.LoggedIn && id.AuthMethod == "claude.ai" {
		return RemoteControlAuth{State: RemoteControlAuthOK, Identity: id}
	}
	reason := "not logged in to Claude — run `claude auth login`."
	if id.LoggedIn {
		reason = "you're authenticated with a non-claude.ai account; remote control needs a claude.ai login. Run `claude auth login`."
	}
	return RemoteControlAuth{State: RemoteControlAuthBlocked, Reason: reason, Identity: id}
}
```
Fix imports: drop `context`, `encoding/json`, `os/exec`, `time` if now unused; add `"github.com/aidan-bailey/loom/account"`.

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./session/...`
Expected: PASS, including the unchanged `TestDetectClaudeRemoteControlAuth` table.

- [ ] **Step 5: Commit**

```bash
gofmt -w session/claude_roster.go session/remote_control_auth.go session/claude_roster_test.go session/remote_control_auth_test.go
git add session/
git commit -m "feat(session): query the roster and remote-control auth as an account

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Phase C — ui

### Task 10: Usage text and the account strip

**Files:**
- Create: `ui/account_usage.go`, `ui/account_strip.go`
- Test: `ui/account_strip_test.go`

- [ ] **Step 1: Write the failing tests**

`ui/account_strip_test.go`:
```go
package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/account"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

var stripNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func probed(five, week float64) account.Usage {
	return account.Usage{
		Available: true, Plan: "max", At: stripNow.Add(-time.Minute),
		FiveHour: &account.Window{Pct: five, ResetsAt: stripNow.Add(time.Hour)},
		SevenDay: &account.Window{Pct: week, ResetsAt: stripNow.Add(72 * time.Hour)},
	}
}

func TestAccountUsageText(t *testing.T) {
	fresh := probed(12, 31)
	stale := probed(12, 31)
	stale.At = stripNow.Add(-9 * time.Minute)
	reset := probed(90, 31)
	reset.FiveHour.ResetsAt = stripNow.Add(-time.Minute)

	cases := map[string]struct {
		s    AccountStatus
		want string
	}{
		"fresh":        {AccountStatus{Usage: fresh}, "5h 12% · 7d 31%"},
		"stale":        {AccountStatus{Usage: stale}, "5h 12% · 7d 31% · 9m ago"},
		"window reset": {AccountStatus{Usage: reset}, "5h reset · 7d 31%"},
		"never probed": {AccountStatus{}, "—"},
		"no limits":    {AccountStatus{Usage: account.Usage{At: stripNow}}, "n/a"},
		"logged out":   {AccountStatus{Usage: fresh, LoggedOut: true}, "logged out"},
	}
	for name, tc := range cases {
		assert.Equal(t, tc.want, accountUsageText(tc.s, stripNow, true), name)
	}
	assert.Equal(t, "5h 12%", accountUsageText(AccountStatus{Usage: fresh}, stripNow, false), "compact drops 7d")
}

func TestAccountStrip_HiddenWithOnlyTheDefaultAccount(t *testing.T) {
	s := NewAccountStrip()
	s.SetWidth(120)
	s.SetAccounts([]AccountStatus{{Name: account.DefaultName, IsDefault: true, Usage: probed(1, 2)}})
	assert.Equal(t, 0, s.Height())
	assert.Equal(t, "", s.render(stripNow))
}

func TestAccountStrip_RendersEveryAccount(t *testing.T) {
	s := NewAccountStrip()
	s.SetWidth(120)
	s.SetAccounts([]AccountStatus{
		{Name: account.DefaultName, IsDefault: true, Usage: probed(64, 40)},
		{Name: "max-2", Usage: probed(12, 31)},
		{Name: "max-3", LoggedOut: true},
	})
	assert.Equal(t, 1, s.Height())
	out := ansi.Strip(s.render(stripNow))
	assert.Contains(t, out, "*default")
	assert.Contains(t, out, "5h 64% · 7d 40%")
	assert.Contains(t, out, "max-2")
	assert.Contains(t, out, "max-3  logged out")
}

func TestAccountStrip_NarrowDropsTheWeekThenTruncates(t *testing.T) {
	s := NewAccountStrip()
	s.SetAccounts([]AccountStatus{
		{Name: account.DefaultName, IsDefault: true, Usage: probed(64, 40)},
		{Name: "max-2", Usage: probed(12, 31)},
	})
	s.SetWidth(40)
	out := s.render(stripNow)
	assert.NotContains(t, ansi.Strip(out), "7d")
	assert.LessOrEqual(t, lipgloss.Width(out), 40)

	s.SetWidth(12)
	out = s.render(stripNow)
	assert.LessOrEqual(t, lipgloss.Width(out), 12)
	assert.False(t, strings.Contains(out, "\n"))
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'AccountUsageText|AccountStrip'`
Expected: FAIL to compile, `undefined: AccountStatus`.

- [ ] **Step 3: Implement**

`ui/account_usage.go`:
```go
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/account"
)

// AccountStatus is the render-ready view of one Claude account, shared by
// the usage strip, the Launch Options Account row and the Accounts screen.
type AccountStatus struct {
	Name string
	// IsDefault marks the account new sessions preselect ("*").
	IsDefault bool
	// Usage is the last good probe; a zero At means never probed.
	Usage account.Usage
	// Failing reports that the latest probe failed (Usage is older).
	Failing bool
	// LoggedOut reports that `claude auth status` said so.
	LoggedOut bool
}

// UsageStaleAfter is when a usage sample renders dimmed with its age: two
// of app's 2-minute probe intervals.
const UsageStaleAfter = 4 * time.Minute

// AccountUsageText is the plain usage summary: "5h 12% · 7d 31%", plus
// " · 9m ago" once stale; "logged out"; "n/a" when plan limits don't apply
// (API-key auth); "—" before the first successful probe.
func AccountUsageText(s AccountStatus, now time.Time) string {
	return accountUsageText(s, now, true)
}

func accountUsageText(s AccountStatus, now time.Time, withWeek bool) string {
	switch {
	case s.LoggedOut:
		return "logged out"
	case s.Usage.At.IsZero():
		return "—"
	case !s.Usage.Available:
		return "n/a"
	}
	var parts []string
	if v := s.Usage.FiveHour.Text(now); v != "" {
		parts = append(parts, "5h "+v)
	}
	if v := s.Usage.SevenDay.Text(now); withWeek && v != "" {
		parts = append(parts, "7d "+v)
	}
	if len(parts) == 0 {
		parts = append(parts, "—")
	}
	if usageStale(s, now) {
		parts = append(parts, formatUsageAge(now.Sub(s.Usage.At))+" ago")
	}
	return strings.Join(parts, " · ")
}

func usageStale(s AccountStatus, now time.Time) bool {
	return !s.Usage.At.IsZero() && now.Sub(s.Usage.At) >= UsageStaleAfter
}

func formatUsageAge(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// usageSeverity ranks the fullest live window: 2 at ≥95%, 1 at ≥80%, else
// 0. A window past its reset counts as empty.
func usageSeverity(s AccountStatus, now time.Time) int {
	sev := 0
	for _, w := range []*account.Window{s.Usage.FiveHour, s.Usage.SevenDay} {
		if w == nil || (!w.ResetsAt.IsZero() && !now.Before(w.ResetsAt)) {
			continue
		}
		switch {
		case w.Pct >= 95:
			sev = 2
		case w.Pct >= 80 && sev < 1:
			sev = 1
		}
	}
	return sev
}
```

`ui/account_strip.go`:
```go
package ui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	stripNameStyle, stripDefaultStyle, stripUsageStyle, stripWarnStyle,
	stripErrStyle, stripStaleStyle lipgloss.Style
)

func init() { RegisterThemeHook(rebuildAccountStripStyles) }

func rebuildAccountStripStyles() {
	stripNameStyle = lipgloss.NewStyle().Foreground(Text)
	stripDefaultStyle = lipgloss.NewStyle().Foreground(Text).Bold(true)
	stripUsageStyle = lipgloss.NewStyle().Foreground(Dim)
	stripWarnStyle = lipgloss.NewStyle().Foreground(Highlight)
	stripErrStyle = lipgloss.NewStyle().Foreground(ErrorColor)
	stripStaleStyle = lipgloss.NewStyle().Foreground(Faint)
}

// AccountStrip is the one-row usage summary above the workspace tab bar:
// every Claude account with its 5-hour and weekly plan usage. It shows only
// when an extra account exists (two or more accounts).
type AccountStrip struct {
	width    int
	accounts []AccountStatus
}

// NewAccountStrip creates an empty (hidden) strip.
func NewAccountStrip() *AccountStrip { return &AccountStrip{} }

// SetWidth sets the render width.
func (s *AccountStrip) SetWidth(w int) { s.width = w }

// SetAccounts replaces the accounts shown, default first.
func (s *AccountStrip) SetAccounts(a []AccountStatus) { s.accounts = a }

// Height is 1 while the strip shows, else 0.
func (s *AccountStrip) Height() int {
	if len(s.accounts) < 2 {
		return 0
	}
	return 1
}

// String renders the strip at the current time; "" when hidden.
func (s *AccountStrip) String() string { return s.render(time.Now()) }

func (s *AccountStrip) render(now time.Time) string {
	if s.Height() == 0 || s.width <= 0 {
		return ""
	}
	line := s.compose(now, true)
	if lipgloss.Width(line) > s.width {
		line = s.compose(now, false)
	}
	if lipgloss.Width(line) > s.width {
		line = ansi.Truncate(line, s.width, "…")
	}
	return line
}

func (s *AccountStrip) compose(now time.Time, withWeek bool) string {
	segs := make([]string, 0, len(s.accounts))
	for _, a := range s.accounts {
		name := stripNameStyle.Render(a.Name)
		if a.IsDefault {
			name = stripDefaultStyle.Render("*" + a.Name)
		}
		usage := accountUsageText(a, now, withWeek)
		style := stripUsageStyle
		switch {
		case a.LoggedOut:
			style = stripErrStyle
		case a.Failing || usageStale(a, now):
			style = stripStaleStyle
		default:
			switch usageSeverity(a, now) {
			case 2:
				style = stripErrStyle
			case 1:
				style = stripWarnStyle
			}
		}
		segs = append(segs, name+"  "+style.Render(usage))
	}
	return " " + strings.Join(segs, "    ")
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'AccountUsageText|AccountStrip'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w ui/account_usage.go ui/account_strip.go ui/account_strip_test.go
git add ui/
git commit -m "feat(ui): account usage strip

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Account badge on cards and the agent pane title

**Files:**
- Modify: `ui/card.go` (`CardData` line ~62-95, `BuildCardData` ~105-154, `RenderCard` ~500-560)
- Modify: `ui/overview.go` (`renderOverviewCard` ~247-322)
- Modify: `ui/split_pane.go` (`agentPaneTitle` ~651-672)
- Test: `ui/card_account_test.go`

- [ ] **Step 1: Write the failing tests**

`ui/card_account_test.go`:
```go
package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withShowAccounts(t *testing.T, on bool) {
	t.Helper()
	SetShowAccounts(on)
	t.Cleanup(func() { SetShowAccounts(false) })
}

func claudeInstance(t *testing.T, program, acct string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: "badge", Path: t.TempDir(), Program: program})
	require.NoError(t, err)
	inst.SetAccount(acct)
	return inst
}

func TestAccountLabel(t *testing.T) {
	withShowAccounts(t, false)
	assert.Equal(t, "", accountLabel(claudeInstance(t, "claude", "max-2")), "badges off")

	withShowAccounts(t, true)
	assert.Equal(t, "max-2", accountLabel(claudeInstance(t, "claude", "max-2")))
	assert.Equal(t, "default", accountLabel(claudeInstance(t, "claude", "")))
	assert.Equal(t, "", accountLabel(claudeInstance(t, "aider", "max-2")), "only Claude sessions have an account")
	assert.Equal(t, "", accountLabel(nil))
}

func TestBuildCardData_CarriesTheAccount(t *testing.T) {
	withShowAccounts(t, true)
	d := BuildCardData(claudeInstance(t, "claude", "max-2"), false, "", 0)
	assert.Equal(t, "max-2", d.Account)
}

func TestRenderCard_RailShowsTheAccountBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: "max-2"}
	out := RenderCard(d, DensityRail, 40)
	first := ansi.Strip(strings.Split(out, "\n")[0])
	assert.Contains(t, first, "1. fix-login")
	assert.Contains(t, first, "@max-2")
	for _, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 40)
	}
}

func TestRenderCard_NarrowRailDropsTheBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: "max-2"}
	out := ansi.Strip(RenderCard(d, DensityRail, 12))
	assert.NotContains(t, out, "@max-2")
}

func TestRenderOverviewCard_ShowsTheAccount(t *testing.T) {
	d := CardData{Title: "fix-login", Branch: "aidanb/fix", Status: session.Running, Account: "max-2"}
	assert.Contains(t, ansi.Strip(renderOverviewCard(d, 50)), "@max-2")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'AccountLabel|CarriesTheAccount|AccountBadge|DropsTheBadge|OverviewCard_ShowsTheAccount'`
Expected: FAIL to compile, `undefined: SetShowAccounts`.

- [ ] **Step 3: Implement**

`ui/card.go`:
- Add to `CardData` after `WaitReason`:
```go
	// Account is the Claude account badge ("max-2", "default"); empty when
	// badges are off (no extra account) or the session isn't Claude.
	Account string
```
- Add near the top of the file (after the imports/consts):
```go
// showAccounts turns account badges on. app sets it (SetShowAccounts)
// while an extra account is registered. Main goroutine only.
var showAccounts bool

// SetShowAccounts turns account badges on or off.
func SetShowAccounts(on bool) { showAccounts = on }

// ShowAccounts reports whether account badges are on.
func ShowAccounts() bool { return showAccounts }

// accountLabel is inst's account badge text: its account's name
// (account.DefaultName for the default account), or "" when badges are off
// or inst is not a Claude session.
func accountLabel(inst *session.Instance) string {
	if !showAccounts || inst == nil || !session.IsClaudeProgram(inst.Program()) {
		return ""
	}
	if name := inst.Account(); name != "" {
		return name
	}
	return account.DefaultName
}

// accountToken is the rail's dim "@name" badge, "" without an account.
func accountToken(d CardData, solidBg bool) string {
	if d.Account == "" {
		return ""
	}
	st := lipgloss.NewStyle().Foreground(Dim)
	if solidBg {
		st = st.Background(Panel)
	}
	return st.Render("@" + d.Account)
}
```
(add import `"github.com/aidan-bailey/loom/account"`).
- In `BuildCardData`, add `Account: accountLabel(inst),` to the `CardData` literal.
- In `RenderCard`, replace
```go
	prefix := fmt.Sprintf("%d. ", d.Index)
	inner := width - 2 // bar + space
	title := truncate(prefix+d.Title, inner)
	titleLine := bar + sep + titleStyleC.Render(title)
```
with
```go
	prefix := fmt.Sprintf("%d. ", d.Index)
	inner := width - 2 // bar + space
	// The account badge rides the title line's right edge. Like the GitHub
	// token it is dropped whole when it would squeeze the title below the
	// status floor.
	acct := accountToken(d, solidBg)
	if acct != "" && lipgloss.Width(acct) > inner-railStatusFloor-1 {
		acct = ""
	}
	titleW := inner
	if acct != "" {
		titleW = inner - lipgloss.Width(acct) - 1
	}
	title := truncate(prefix+d.Title, titleW)
	composeTitle := func(st lipgloss.Style) string {
		if acct == "" {
			return bar + sep + st.Render(title)
		}
		return bar + sep + spreadLine(st.Render(title), acct, inner)
	}
	titleLine := composeTitle(titleStyleC)
```
and further down replace `titleLine = bar + sep + titleStyleC.Foreground(Dim).Render(title)` with `titleLine = composeTitle(titleStyleC.Foreground(Dim))`.

`ui/overview.go`, in `renderOverviewCard`, right after `var right []string`:
```go
	if d.Account != "" {
		right = append(right, dim.Render("@"+d.Account))
	}
```

`ui/split_pane.go`, in `agentPaneTitle`, inside `if s.instance != nil && s.instance.Started() {` before the branch:
```go
		if lbl := accountLabel(s.instance); lbl != "" {
			base += " · @" + lbl
		}
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./ui/...`
Expected: PASS (existing card/overview golden tests are unaffected: badges are off by default).

- [ ] **Step 5: Commit**

```bash
gofmt -w ui/card.go ui/overview.go ui/split_pane.go ui/card_account_test.go
git add ui/
git commit -m "feat(ui): show each Claude session's account on cards and the pane title

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Launch Options Account row

**Files:**
- Modify: `ui/overlay/sessionLaunchOptions.go`
- Test: `ui/overlay/sessionLaunchOptions_test.go`

The row is appended after Branch Prefix and only exists when `SetAccounts` got two or more choices, so every existing row keeps its index and no existing test changes.

- [ ] **Step 1: Write the failing tests**

Append to `ui/overlay/sessionLaunchOptions_test.go`:
```go
func accountChoices() []AccountChoice {
	return []AccountChoice{
		{Name: "default", Summary: "5h 64% · 7d 40%"},
		{Name: "max-2", Summary: "5h 12% · 7d 31%", RCBlocked: true, RCReason: "not logged in"},
	}
}

// toAccountRow moves the cursor to the Account row (the last one).
func toAccountRow(lo *SessionLaunchOptions) {
	for i := 0; i < sessionLaunchOptionsAccountRow; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
}

func TestSessionLaunchOptions_NoAccountRowWithoutChoices(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{}, false, "")
	lo.SetAccounts([]AccountChoice{{Name: "default"}})
	assert.NotContains(t, lo.Render(), "Account")
	toAccountRow(lo)
	assert.Equal(t, sessionLaunchOptionsBranchPrefixRow, lo.cursor, "the cursor stops at Branch Prefix")
}

func TestSessionLaunchOptions_AccountRowCycles(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{Account: "default"}, false, "")
	lo.SetAccounts(accountChoices())
	toAccountRow(lo)
	require.Equal(t, sessionLaunchOptionsAccountRow, lo.cursor)

	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "max-2", lo.Options().Account)
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, "default", lo.Options().Account)
}

func TestSessionLaunchOptions_AccountRowShowsUsage(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{Account: "max-2"}, false, "")
	lo.SetAccounts(accountChoices())
	out := lo.Render()
	assert.Contains(t, out, "Account")
	assert.Contains(t, out, "max-2")
	assert.Contains(t, out, "5h 12% · 7d 31%")
}

func TestSessionLaunchOptions_BlockedHintFollowsTheSelectedAccount(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{Account: "default"}, false, "")
	lo.SetAccounts(accountChoices())
	assert.NotContains(t, lo.Render(), "not logged in")

	toAccountRow(lo)
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // → max-2
	assert.Contains(t, lo.Render(), "not logged in")
}

func TestSessionLaunchOptions_UnknownAccountFallsToTheFirstChoice(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{Account: "gone"}, false, "")
	lo.SetAccounts(accountChoices())
	assert.Equal(t, "default", lo.Options().Account)
}
```
(Add `"github.com/stretchr/testify/require"` to the imports.)

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/overlay/ -run SessionLaunchOptions`
Expected: FAIL to compile, `undefined: AccountChoice`.

- [ ] **Step 3: Implement**

In `ui/overlay/sessionLaunchOptions.go`:
- Add to `LaunchOptions` after `BranchPrefix`:
```go
	// Account is the Claude account to launch under (account.DefaultName
	// for the default). Like BranchPrefix it never reaches the command
	// line: app records it on the instance (session.Instance.SetAccount),
	// and a launch turns it into CLAUDE_CONFIG_DIR.
	Account string
```
- Add after the `LaunchOptions` type:
```go
// AccountChoice is one option on the Account row.
type AccountChoice struct {
	Name string
	// Summary is the account's usage, e.g. "5h 12% · 7d 31%".
	Summary string
	// RCBlocked/RCReason mirror the account's own remote-control auth, so
	// the Remote Control row's blocked hint follows the selected account.
	RCBlocked bool
	RCReason  string
}
```
- Add field `accounts []AccountChoice` to `SessionLaunchOptions` with comment `// accounts are the Account row's options; the row shows only with two or more (an extra account exists).`
- Add after `sessionLaunchOptionsBranchPrefixRow`:
```go
// sessionLaunchOptionsAccountRow is the cursor index of the Account row. It
// is appended after Branch Prefix, and only while SetAccounts has given two
// or more choices, so no other row moves when it appears.
const sessionLaunchOptionsAccountRow = sessionLaunchOptionsRowCount
```
- Add methods:
```go
// SetAccounts supplies the Account row's choices, default first. Fewer
// than two hides the row. An Account the choices don't include (removed,
// or never set) falls to the first choice.
func (l *SessionLaunchOptions) SetAccounts(choices []AccountChoice) {
	l.accounts = choices
	if l.accountRowShown() && l.accountIndex() < 0 {
		l.opts.Account = choices[0].Name
	}
}

func (l *SessionLaunchOptions) accountRowShown() bool { return len(l.accounts) >= 2 }

func (l *SessionLaunchOptions) accountIndex() int {
	for i, c := range l.accounts {
		if c.Name == l.opts.Account {
			return i
		}
	}
	return -1
}

// rowCount is the number of navigable rows, the Account row included when
// it shows.
func (l *SessionLaunchOptions) rowCount() int {
	if l.accountRowShown() {
		return sessionLaunchOptionsRowCount + 1
	}
	return sessionLaunchOptionsRowCount
}

// blocked is the Remote Control row's auth state: the selected account's
// when the Account row shows, else the one the modal was built with.
func (l *SessionLaunchOptions) blocked() (bool, string) {
	if l.accountRowShown() {
		if i := l.accountIndex(); i >= 0 {
			return l.accounts[i].RCBlocked, l.accounts[i].RCReason
		}
	}
	return l.authBlocked, l.authReason
}
```
- In `HandleKeyPress`, change `if l.cursor < sessionLaunchOptionsRowCount-1 {` to `if l.cursor < l.rowCount()-1 {`.
- In `toggleCursor`, add a case:
```go
	case sessionLaunchOptionsAccountRow:
		if l.accountRowShown() {
			l.opts.Account = l.accounts[(l.accountIndex()+1)%len(l.accounts)].Name
		}
```
(`accountIndex()` is never -1 here: `SetAccounts` normalized it.)
- In `Render`, replace the blocked check inside `row`:
```go
		if idx == 0 {
			if blocked, reason := l.blocked(); blocked {
				line += "  " + sessionLaunchOptionsBlockedText.Render("(blocked: "+reason+")")
			}
		}
```
and replace the Branch Prefix line and what follows up to the hint:
```go
		row(sessionLaunchOptionsBranchPrefixRow, "Branch Prefix     ", l.branchPrefixValue()) + "\n"
	if l.accountRowShown() {
		content += row(sessionLaunchOptionsAccountRow, "Account           ", l.accountValue()) + "\n"
	}
	content += "\n" + sessionLaunchOptionsHintStyle.Render(l.hint())
```
(the `content :=` expression now ends after the Branch Prefix row's `"\n"`), and add:
```go
// accountValue renders the Account row's right-hand side: the selected
// account and its usage summary. Plain text: the row style wraps it.
func (l *SessionLaunchOptions) accountValue() string {
	i := l.accountIndex()
	if i < 0 {
		return "< " + l.opts.Account + " >"
	}
	c := l.accounts[i]
	if c.Summary == "" {
		return "< " + c.Name + " >"
	}
	return "< " + c.Name + " >  " + c.Summary
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./ui/overlay/...`
Expected: PASS — the new tests and every pre-existing `SessionLaunchOptions` test (the row is absent without `SetAccounts`).

- [ ] **Step 5: Commit**

```bash
gofmt -w ui/overlay/sessionLaunchOptions.go ui/overlay/sessionLaunchOptions_test.go
git add ui/overlay/
git commit -m "feat(overlay): Account row in Session Launch Options

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Accounts screen in Settings

**Files:**
- Create: `ui/overlay/accountsManager.go`
- Modify: `ui/overlay/settingsOverlay.go`
- Test: `ui/overlay/accountsManager_test.go`

The Accounts screen is a Settings sub-screen like Profiles. It does no I/O: each action becomes an `AccountRequest` the app polls with `TakeAccountRequest` (the `TakeError` pattern) and carries out.

- [ ] **Step 1: Write the failing tests**

`ui/overlay/accountsManager_test.go`:
```go
package overlay

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func accountRows() []AccountRow {
	return []AccountRow{
		{Name: "default", Email: "you@example.com", Plan: "max", Usage: "5h 64% · 7d 40%", IsDefault: true},
		{Name: "max-2", Email: "you+2@example.com", Plan: "max", Usage: "5h 12% · 7d 31%", Warning: "not shared: settings.json"},
	}
}

func press(a *AccountsManager, keys ...string) (closed bool) {
	for _, k := range keys {
		switch k {
		case "enter":
			closed = a.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "esc":
			closed = a.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		default:
			r := []rune(k)[0]
			closed = a.HandleKeyPress(tea.KeyPressMsg{Code: r, Text: k})
		}
	}
	return closed
}

func TestAccountsManager_AddRequestsTheTypedName(t *testing.T) {
	a := NewAccountsManager(accountRows())
	press(a, "a", "m", "a", "x", "-", "3", "enter")

	req, ok := a.TakeRequest()
	require.True(t, ok)
	assert.Equal(t, AccountRequest{Kind: AccountRequestAdd, Name: "max-3"}, req)
	_, ok = a.TakeRequest()
	assert.False(t, ok, "a request is taken once")
}

func TestAccountsManager_EscCancelsAddThenCloses(t *testing.T) {
	a := NewAccountsManager(accountRows())
	assert.False(t, press(a, "a", "esc"), "esc leaves the name prompt, not the screen")
	_, ok := a.TakeRequest()
	assert.False(t, ok)
	assert.True(t, press(a, "esc"))
}

func TestAccountsManager_RowActions(t *testing.T) {
	a := NewAccountsManager(accountRows())
	press(a, "j", "enter")
	req, _ := a.TakeRequest()
	assert.Equal(t, AccountRequest{Kind: AccountRequestSetDefault, Name: "max-2"}, req)

	press(a, "l")
	req, _ = a.TakeRequest()
	assert.Equal(t, AccountRequest{Kind: AccountRequestLogin, Name: "max-2"}, req)
}

func TestAccountsManager_RemoveConfirmsAndSparesDefault(t *testing.T) {
	a := NewAccountsManager(accountRows())
	press(a, "x")
	assert.NotContains(t, a.Render(), "Remove account", "the default account has no remove")

	press(a, "j", "x")
	assert.Contains(t, a.Render(), `Remove account "max-2"`)
	press(a, "n")
	_, ok := a.TakeRequest()
	assert.False(t, ok)

	press(a, "x", "y")
	req, ok := a.TakeRequest()
	require.True(t, ok)
	assert.Equal(t, AccountRequest{Kind: AccountRequestRemove, Name: "max-2"}, req)
}

func TestAccountsManager_RendersRows(t *testing.T) {
	out := NewAccountsManager(accountRows()).Render()
	assert.Contains(t, out, "* default")
	assert.Contains(t, out, "you+2@example.com")
	assert.Contains(t, out, "5h 12% · 7d 31%")
	assert.Contains(t, out, "not shared: settings.json")
}

func TestSettingsOverlay_OpensAccountsAndPassesRequestsThrough(t *testing.T) {
	s := NewSettingsOverlay(testConfig(t), false, "")
	s.SetAccountRows(accountRows())
	for i := 0; i < int(settingsFieldCount); i++ {
		s.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	assert.Contains(t, s.Render(), "Accounts")
	s.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	s.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	s.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})

	req, ok := s.TakeAccountRequest()
	require.True(t, ok)
	assert.Equal(t, AccountRequest{Kind: AccountRequestSetDefault, Name: "max-2"}, req)
}
```
`testConfig(t)` must return a `*config.Config`. First check `ui/overlay/settingsOverlay_test.go` for an existing helper that builds one; if there is none, add to the new test file:
```go
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return config.DefaultConfig()
}
```
(importing `"github.com/aidan-bailey/loom/config"`), and if a helper with another name exists, use it instead of `testConfig`.

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/overlay/ -run 'AccountsManager|OpensAccounts'`
Expected: FAIL to compile, `undefined: NewAccountsManager`.

- [ ] **Step 3: Implement**

`ui/overlay/accountsManager.go`:
```go
package overlay

import (
	"fmt"
	"strings"

	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// AccountRow is one line of the Accounts screen, formatted by app.
type AccountRow struct {
	Name  string
	Email string
	Plan  string
	// Usage is ui.AccountUsageText for the account.
	Usage string
	// Warning is "logged out", "not shared: settings.json", … or "".
	Warning   string
	IsDefault bool
}

// AccountRequestKind names an action the user asked the Accounts screen
// for. The screen does no I/O; app carries each request out.
type AccountRequestKind int

const (
	AccountRequestAdd AccountRequestKind = iota + 1
	AccountRequestLogin
	AccountRequestRemove
	AccountRequestSetDefault
)

// AccountRequest is one action for app to carry out on account Name.
type AccountRequest struct {
	Kind AccountRequestKind
	Name string
}

type accountsMode int

const (
	accountsBrowsing accountsMode = iota
	accountsAddingName
	accountsConfirmingRemove
)

// defaultAccountName mirrors account.DefaultName; overlay stays free of
// the account package.
const defaultAccountName = "default"

// AccountsManager is the Accounts drill-in of the Settings overlay: the
// Claude accounts with their login and usage, and keys to add, log in,
// remove, and pick the default. Rows are supplied (and refreshed) by app.
type AccountsManager struct {
	rows   []AccountRow
	cursor int
	width  int

	mode    accountsMode
	input   *TextInputOverlay
	request *AccountRequest
}

// NewAccountsManager creates the Accounts screen over rows.
func NewAccountsManager(rows []AccountRow) *AccountsManager {
	a := &AccountsManager{width: 60}
	a.SetRows(rows)
	return a
}

// SetRows replaces the rows, keeping the cursor in range.
func (a *AccountsManager) SetRows(rows []AccountRow) {
	a.rows = rows
	if a.cursor >= len(rows) {
		a.cursor = max(len(rows)-1, 0)
	}
}

// SetWidth propagates the available width to any embedded text input.
func (a *AccountsManager) SetWidth(w int) {
	a.width = w
	if a.input != nil {
		a.input.SetSize(w, 3)
	}
}

// TakeRequest returns and clears the pending request.
func (a *AccountsManager) TakeRequest() (AccountRequest, bool) {
	if a.request == nil {
		return AccountRequest{}, false
	}
	req := *a.request
	a.request = nil
	return req, true
}

func (a *AccountsManager) selected() (AccountRow, bool) {
	if a.cursor < 0 || a.cursor >= len(a.rows) {
		return AccountRow{}, false
	}
	return a.rows[a.cursor], true
}

func (a *AccountsManager) ask(kind AccountRequestKind, name string) {
	a.request = &AccountRequest{Kind: kind, Name: name}
}

// HandleKeyPress processes one key press. closed reports whether the
// screen should return control to the parent SettingsOverlay.
func (a *AccountsManager) HandleKeyPress(msg tea.KeyPressMsg) (closed bool) {
	switch a.mode {
	case accountsAddingName:
		// Enter/Esc are owned here, not by the textarea (see
		// SettingsOverlay.handleEditingText for why).
		switch msg.Code {
		case tea.KeyEnter:
			if name := strings.TrimSpace(a.input.GetValue()); name != "" {
				a.ask(AccountRequestAdd, name)
			}
			a.mode, a.input = accountsBrowsing, nil
		case tea.KeyEsc:
			a.mode, a.input = accountsBrowsing, nil
		default:
			a.input.HandleKeyPress(msg)
		}
		return false
	case accountsConfirmingRemove:
		switch msg.String() {
		case "y":
			if row, ok := a.selected(); ok {
				a.ask(AccountRequestRemove, row.Name)
			}
			a.mode = accountsBrowsing
		case "n", "esc":
			a.mode = accountsBrowsing
		}
		return false
	}

	switch msg.String() {
	case "up", "k":
		if a.cursor > 0 {
			a.cursor--
		}
	case "down", "j":
		if a.cursor < len(a.rows)-1 {
			a.cursor++
		}
	case "esc", "q":
		return true
	case "a":
		a.mode = accountsAddingName
		a.input = NewTextInputOverlay("Account name (a-z, 0-9, -)", "")
		a.input.SetSize(a.width, 3)
	case "l":
		if row, ok := a.selected(); ok {
			a.ask(AccountRequestLogin, row.Name)
		}
	case "enter", " ", "space":
		if row, ok := a.selected(); ok {
			a.ask(AccountRequestSetDefault, row.Name)
		}
	case "x":
		if row, ok := a.selected(); ok && row.Name != defaultAccountName {
			a.mode = accountsConfirmingRemove
		}
	}
	return false
}

var (
	accountsTitleStyle, accountsSelectedStyle, accountsNormalStyle,
	accountsHintStyle, accountsWarnStyle lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildAccountsManagerStyles) }

func rebuildAccountsManagerStyles() {
	accountsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	accountsSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	accountsNormalStyle = lipgloss.NewStyle().Foreground(ui.Text)
	accountsHintStyle = lipgloss.NewStyle().Foreground(ui.Faint)
	accountsWarnStyle = lipgloss.NewStyle().Foreground(ui.ErrorColor)
}

// Render renders whichever mode is active.
func (a *AccountsManager) Render() string {
	if a.mode == accountsAddingName {
		return a.input.Render()
	}
	content := accountsTitleStyle.Render("Accounts") + "\n\n"
	if len(a.rows) == 0 {
		content += accountsNormalStyle.Render("No accounts — press 'a' to add one") + "\n"
	}
	for i, r := range a.rows {
		cursor := "  "
		if i == a.cursor {
			cursor = "> "
		}
		mark := "  "
		if r.IsDefault {
			mark = "* "
		}
		line := fmt.Sprintf("%s%s%-12s %-26s %-5s %s", cursor, mark, r.Name, r.Email, r.Plan, r.Usage)
		if i == a.cursor {
			content += accountsSelectedStyle.Render(line)
		} else {
			content += accountsNormalStyle.Render(line)
		}
		if r.Warning != "" {
			content += "  " + accountsWarnStyle.Render(r.Warning)
		}
		content += "\n"
	}
	if row, ok := a.selected(); ok && a.mode == accountsConfirmingRemove {
		content += "\n" + accountsHintStyle.Render(fmt.Sprintf("Remove account %q and delete its config dir? y/n", row.Name))
	} else {
		content += "\n" + accountsHintStyle.Render("a add • l log in • enter set default • x remove • esc back")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(a.width).
		Render(content)
}
```

`ui/overlay/settingsOverlay.go`:
- Add `settingsFieldAccounts` after `settingsFieldTheme` (before `settingsFieldCount`), and to `label()`: `case settingsFieldAccounts: return "Accounts"`.
- Add `settingsAccountsSub` to the `settingsMode` consts.
- Add fields to `SettingsOverlay`: `accounts *AccountsManager` and `accountRows []AccountRow`.
- Add methods:
```go
// SetAccountRows supplies the Accounts screen's rows (app refreshes them as
// probes land), updating the screen if it is open.
func (s *SettingsOverlay) SetAccountRows(rows []AccountRow) {
	s.accountRows = rows
	if s.accounts != nil {
		s.accounts.SetRows(rows)
	}
}

// TakeAccountRequest returns and clears the Accounts screen's pending
// request. Callers poll it after HandleKeyPress, like TakeError.
func (s *SettingsOverlay) TakeAccountRequest() (AccountRequest, bool) {
	if s.accounts == nil {
		return AccountRequest{}, false
	}
	return s.accounts.TakeRequest()
}
```
- In `HandleKeyPress`, add a case to the mode switch:
```go
	case settingsAccountsSub:
		if s.accounts.HandleKeyPress(msg) {
			s.mode = settingsBrowsing
			s.accounts = nil
		}
		return false, false
```
- In `activateRow`:
```go
	case settingsFieldAccounts:
		s.accounts = NewAccountsManager(s.accountRows)
		s.accounts.SetWidth(s.width)
		s.mode = settingsAccountsSub
```
- In `Render`'s mode switch: `case settingsAccountsSub: return s.accounts.Render()`.
- In `valueFor`: `case settingsFieldAccounts: return fmt.Sprintf("(%d) →", max(len(s.accountRows)-1, 0))` (the count of extra accounts).

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./ui/overlay/...`
Expected: PASS. If a pre-existing Settings test pressed `down` past the end expecting to stop on Theme, it now stops on Accounts: change it to move exactly to Theme's index (`int(settingsFieldTheme)` presses).

- [ ] **Step 5: Commit**

```bash
gofmt -w ui/overlay/accountsManager.go ui/overlay/accountsManager_test.go ui/overlay/settingsOverlay.go
git add ui/overlay/
git commit -m "feat(overlay): Accounts screen in Settings

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Phase D — app

### Task 14: Load and publish the registry; per-account auth refresh

**Files:**
- Create: `app/accounts.go`
- Modify: `app/app.go` (home fields near `rcAuth` line 246-249; `Init` line 606-620; `Update` message cases)
- Modify: `app/app_init.go:780-786`
- Test: `app/accounts_test.go`

- [ ] **Step 1: Write the failing tests**

`app/accounts_test.go`:
```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withAccounts registers extra accounts on m, linked against a throwaway
// main dir, and publishes them; the package-level publication is undone at
// cleanup. Returns the main dir.
func withAccounts(t *testing.T, m *home, names ...string) string {
	t.Helper()
	reg := account.LoadRegistry(t.TempDir())
	main := t.TempDir()
	for _, n := range names {
		_, _, err := reg.Create(n, main)
		require.NoError(t, err)
	}
	m.accounts = reg
	if m.accountStrip == nil {
		m.accountStrip = ui.NewAccountStrip()
	}
	m.publishAccounts()
	t.Cleanup(func() {
		session.SetAccountDirs(nil)
		ui.SetShowAccounts(false)
	})
	return main
}

func TestRcAuthFor(t *testing.T) {
	m := newTestHome(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "logged out"}}

	assert.True(t, m.rcAuthFor("").OK())
	assert.True(t, m.rcAuthFor(account.DefaultName).OK())
	assert.True(t, m.rcAuthFor("max-2").Blocked())
	assert.Equal(t, session.RemoteControlAuthUnknown, m.rcAuthFor("max-3").State,
		"an account whose auth has not been read yet fails closed: no flag, no prompt")
}

func TestPublishAccounts_BadgesOnlyWithAnExtraAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m)
	assert.False(t, ui.ShowAccounts())
	assert.False(t, m.hasExtraAccounts())

	withAccounts(t, m, "max-2")
	assert.True(t, ui.ShowAccounts())
	assert.True(t, m.hasExtraAccounts())
}

func TestHandleAccountsRefreshed_StoresAuthAndSync(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	def := session.RemoteControlAuth{State: session.RemoteControlAuthOK}

	m.Update(accountsRefreshedMsg{
		defaultAuth: &def,
		auth:        map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "x"}},
		sync:        map[string]account.SyncReport{"max-2": {Diverged: []string{"settings.json"}}},
	})

	assert.True(t, m.rcAuth.OK())
	assert.True(t, m.rcAuthFor("max-2").Blocked())
	assert.Equal(t, []string{"settings.json"}, m.accountSync["max-2"].Diverged)
}

func TestAccountStatuses_DefaultFirstAndMarked(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	require.NoError(t, m.accounts.SetDefault("max-2"))
	m.accountAuth = map[string]session.RemoteControlAuth{
		"max-2": {Identity: account.Identity{ConfigDir: "/acct", LoggedIn: false}},
	}

	st := m.accountStatuses()

	require.Len(t, st, 2)
	assert.Equal(t, account.DefaultName, st[0].Name)
	assert.False(t, st[0].IsDefault)
	assert.False(t, st[0].LoggedOut, "no auth read yet is not logged out")
	assert.Equal(t, "max-2", st[1].Name)
	assert.True(t, st[1].IsDefault)
	assert.True(t, st[1].LoggedOut)
}

func TestRefreshAccountViews_RequestsAResizeWhenTheStripAppears(t *testing.T) {
	m := newTestHome(t)
	m.accountStrip = ui.NewAccountStrip()
	withAccounts(t, m)
	assert.Nil(t, m.refreshAccountViews(), "one account: no strip, no resize")

	withAccounts(t, m, "max-2")
	assert.NotNil(t, m.refreshAccountViews(), "the strip appearing changes the content height")
	assert.Nil(t, m.refreshAccountViews(), "no change, no resize")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./app/ -run 'RcAuthFor|PublishAccounts|AccountsRefreshed|AccountStatuses|RefreshAccountViews'`
Expected: FAIL to compile, `withAccounts` references `m.accounts` (undefined field).

- [ ] **Step 3: Implement**

Add home fields in `app/app.go` right after `rcAuth session.RemoteControlAuth`:
```go
	// accounts is the Claude account registry (account/), loaded from the
	// global config dir at startup. Update-goroutine only: launches read the
	// published dir map (session.SetAccountDirs) instead.
	accounts *account.Registry
	// accountAuth is each extra account's remote-control auth, with the
	// identity `claude auth status` reported, filled by accountsRefreshedMsg.
	// The default account's lives in rcAuth.
	accountAuth map[string]session.RemoteControlAuth
	// accountSync is each extra account's last link report.
	accountSync map[string]account.SyncReport
	// usage is each account's latest probe state (usage.go).
	usage map[string]accountUsage
	// accountStrip is the usage strip above the tab bar; empty (height 0)
	// until an extra account exists.
	accountStrip *ui.AccountStrip
```
(import `"github.com/aidan-bailey/loom/account"`).

`app/accounts.go`:
```go
package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
)

// accountUsage is one account's probe state: the last good sample and the
// latest probe's error. Usage is display-only, so a failed probe keeps the
// sample, which the views dim with its age, instead of blanking it.
type accountUsage struct {
	last account.Usage
	err  error
}

// initAccounts loads the account registry and publishes it. Called once
// from newHome, before the startup auth probe.
func (m *home) initAccounts() {
	m.ensureAccountMaps()
	m.accountStrip = ui.NewAccountStrip()
	if globalDir, err := config.GetGlobalConfigDir(); err != nil {
		m.accounts = account.Unavailable(err)
	} else {
		m.accounts = account.LoadRegistry(globalDir)
	}
	if err := m.accounts.LoadErr(); err != nil {
		log.For("account").Error("registry.load_failed", "err", err.Error())
		m.errBox.SetError(fmt.Errorf("accounts: %w", err))
	}
	m.publishAccounts()
}

func (m *home) ensureAccountMaps() {
	if m.accountAuth == nil {
		m.accountAuth = map[string]session.RemoteControlAuth{}
	}
	if m.accountSync == nil {
		m.accountSync = map[string]account.SyncReport{}
	}
	if m.usage == nil {
		m.usage = map[string]accountUsage{}
	}
}

// hasExtraAccounts reports whether an account besides default is
// registered; all account UI and polling is gated on it.
func (m *home) hasExtraAccounts() bool { return m.accounts != nil && m.accounts.HasExtra() }

// accountDirs is the registry's name → config dir map (nil without one).
func (m *home) accountDirs() map[string]string {
	if m.accounts == nil {
		return nil
	}
	return m.accounts.Dirs()
}

// publishAccounts hands the registry to session (launch env) and ui
// (badges). Call after every registry change. Update goroutine only.
func (m *home) publishAccounts() {
	session.SetAccountDirs(m.accountDirs())
	ui.SetShowAccounts(m.hasExtraAccounts())
}

// rcAuthFor returns acct's remote-control auth: the startup probe for the
// default account, the refreshed one for an extra account, and Unknown —
// fail closed, no flag and no prompt — until that has landed.
func (m *home) rcAuthFor(acct string) session.RemoteControlAuth {
	if acct == "" || acct == account.DefaultName {
		return m.rcAuth
	}
	if a, ok := m.accountAuth[acct]; ok {
		return a
	}
	return session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}
}

// claudeProgram is the Claude CLI loom runs account commands with: the
// configured program when it is Claude, else a live Claude session's, else
// "" (no probes).
func (m *home) claudeProgram() string {
	if session.IsClaudeProgram(m.program) {
		return m.program
	}
	for _, inst := range m.activeInstances() {
		if p := inst.Program(); session.IsClaudeProgram(p) {
			return p
		}
	}
	return ""
}

// mainConfigDir is the default account's config dir, which extra accounts
// link to (account.MainDir over the identity read at startup).
func (m *home) mainConfigDir() string {
	return account.MainDir(m.rcAuth.Identity)
}

// accountLoggedOut reports that `claude auth status` ran for acct and said
// it is logged out. Not having asked yet is not logged out.
func (m *home) accountLoggedOut(acct string) bool {
	id := m.rcAuthFor(acct).Identity
	return id.ConfigDir != "" && !id.LoggedIn
}

// accountStatuses builds the view of every account, default first, for the
// strip, the Launch Options row and the Accounts screen.
func (m *home) accountStatuses() []ui.AccountStatus {
	if m.accounts == nil {
		return nil
	}
	def := m.accounts.Default()
	var out []ui.AccountStatus
	for _, name := range m.accounts.Names() {
		u := m.usage[name]
		out = append(out, ui.AccountStatus{
			Name:      name,
			IsDefault: name == def,
			Usage:     u.last,
			Failing:   u.err != nil,
			LoggedOut: m.accountLoggedOut(name),
		})
	}
	return out
}

// refreshAccountViews pushes the current account state into every view
// that shows it. Returns tea.RequestWindowSize when the strip appeared or
// disappeared, since that changes the content height.
func (m *home) refreshAccountViews() tea.Cmd {
	statuses := m.accountStatuses()
	if m.accountStrip == nil {
		return nil
	}
	before := m.accountStrip.Height()
	m.accountStrip.SetAccounts(statuses)
	if m.accountStrip.Height() != before {
		return tea.RequestWindowSize
	}
	return nil
}

// accountsRefreshedMsg carries accountsRefreshCmd's results.
type accountsRefreshedMsg struct {
	// defaultAuth is the default account's re-read auth; nil when the
	// refresh did not cover it.
	defaultAuth *session.RemoteControlAuth
	auth        map[string]session.RemoteControlAuth
	sync        map[string]account.SyncReport
	errs        map[string]error
}

// accountsRefreshCmd re-links every extra account against the main config
// dir and re-reads its auth (and the default account's too when
// withDefault). Its inputs are copied here; the Cmd touches no model state.
func (m *home) accountsRefreshCmd(withDefault bool) tea.Cmd {
	var accts []account.Account
	if m.accounts != nil {
		accts = append(accts, m.accounts.Accounts...)
	}
	if len(accts) == 0 && !withDefault {
		return nil
	}
	program, mainDir := m.claudeProgram(), m.mainConfigDir()
	return func() tea.Msg {
		r := internalexec.Default{}
		msg := accountsRefreshedMsg{
			auth: map[string]session.RemoteControlAuth{},
			sync: map[string]account.SyncReport{},
			errs: map[string]error{},
		}
		if withDefault && program != "" {
			a := session.DetectClaudeRemoteControlAuth(program, r)
			msg.defaultAuth = &a
		}
		for _, a := range accts {
			if mainDir != "" {
				if rep, err := account.Sync(a.Dir, mainDir); err != nil {
					msg.errs[a.Name] = err
				} else {
					msg.sync[a.Name] = rep
				}
			}
			if program != "" {
				msg.auth[a.Name] = session.DetectClaudeRemoteControlAuthEnv(program, account.EnvFor(a.Dir), r)
			}
		}
		return msg
	}
}

// handleAccountsRefreshed stores a refresh's results and redraws the views.
func (m *home) handleAccountsRefreshed(msg accountsRefreshedMsg) tea.Cmd {
	m.ensureAccountMaps()
	if msg.defaultAuth != nil {
		m.rcAuth = *msg.defaultAuth
	}
	for name, a := range msg.auth {
		m.accountAuth[name] = a
	}
	for name, rep := range msg.sync {
		m.accountSync[name] = rep
		if len(rep.Diverged) > 0 {
			log.For("account").Warn("sync.diverged", "account", name, "entries", strings.Join(rep.Diverged, ","))
		}
	}
	for name, err := range msg.errs {
		log.For("account").Warn("sync.failed", "account", name, "err", err.Error())
	}
	return m.refreshAccountViews()
}
```

`app/app_init.go`: replace
```go
	cmdExec := cmd2.MakeExecutor()
	// Probe Claude auth once up front (before any workspace terminal is
	// created) so remote-control launch decisions are synchronous and
	// startup terminals aren't stripped of the flag by fail-closed timing.
	if appConfig != nil && appConfig.RemoteControlEnabled() {
		h.rcAuth = session.DetectClaudeRemoteControlAuth(program, cmdExec)
	}
```
with
```go
	cmdExec := cmd2.MakeExecutor()
	h.initAccounts()
	// Probe Claude auth once up front (before any workspace terminal is
	// created) so remote-control launch decisions are synchronous and
	// startup terminals aren't stripped of the flag by fail-closed timing.
	// The identity it reads also locates the main config dir extra
	// accounts link to, so it runs whenever one is registered too.
	if appConfig != nil && (appConfig.RemoteControlEnabled() || h.hasExtraAccounts()) {
		h.rcAuth = session.DetectClaudeRemoteControlAuth(program, cmdExec)
	}
```

`app/app.go` `Init`: after `tickUpdateMetadataCmd,` in the `cmds` literal add `m.accountsRefreshCmd(false),` (a nil Cmd is dropped by `tea.Batch`).

`app/app.go` `Update`: add, next to `case ghReadyMsg:`,
```go
	case accountsRefreshedMsg:
		return m, m.handleAccountsRefreshed(msg)
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./app/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w app/accounts.go app/accounts_test.go app/app.go app/app_init.go
git add app/
git commit -m "feat(app): load the account registry and read each account's auth

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: Roster per account

**Files:**
- Modify: `app/events.go:108-298` (`rosterReadyMsg`, `rosterQueryCmd`, `maybeRosterQuery`, `rosterStatusFor`)
- Modify: `app/app.go` (`roster` field ~376-381; `case rosterReadyMsg:` ~793-808)
- Test: `app/roster_accounts_test.go`; `app/roster_status_test.go:136,144`

- [ ] **Step 1: Write the failing tests**

`app/roster_accounts_test.go`:
```go
package app

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRosterStatusFor_JoinsTheInstancesAccountRoster(t *testing.T) {
	inst := startedInstanceWithProgram(t, "acct-join", "claude", "x")
	inst.SetAccount("max-2")
	m := homeWithAppState(t)
	wt := inst.GetWorktreePath()
	m.roster = map[string]session.RosterEntry{wt: {Status: session.RosterStatusBusy}}
	m.rosterByAccount = map[string]map[string]session.RosterEntry{"max-2": {wt: {Status: session.RosterStatusIdle}}}

	status, _, ok := m.rosterStatusFor(inst)

	require.True(t, ok)
	assert.Equal(t, session.Ready, status, "an account's session is joined against that account's roster")
}

func TestRosterStatusFor_TheDefaultRosterNeverAnswersForAnotherAccount(t *testing.T) {
	inst := startedInstanceWithProgram(t, "acct-none", "claude", "x")
	inst.SetAccount("max-2")
	m := homeWithAppState(t)
	m.roster = map[string]session.RosterEntry{inst.GetWorktreePath(): {Status: session.RosterStatusBusy}}

	_, _, ok := m.rosterStatusFor(inst)

	assert.False(t, ok)
}

func TestRosterReady_OneAccountsFailureKeepsTheOthers(t *testing.T) {
	m := homeWithAppState(t)

	m.Update(rosterReadyMsg{
		entries:   map[string]session.RosterEntry{"/w": {Status: session.RosterStatusBusy}},
		extra:     map[string]map[string]session.RosterEntry{"max-3": {"/v": {Status: session.RosterStatusIdle}}},
		extraErrs: map[string]error{"max-2": errors.New("daemon down")},
	})

	assert.Len(t, m.roster, 1)
	assert.Contains(t, m.rosterByAccount, "max-3")
	assert.NotContains(t, m.rosterByAccount, "max-2")
}
```
In `app/roster_status_test.go`, lines 136 and 144: `rosterQueryCmd([]*session.Instance{…})` → `rosterQueryCmd([]*session.Instance{…}, nil)`.

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./app/ -run 'RosterStatusFor_|RosterReady_|RosterQueryCmd'`
Expected: FAIL to compile, `m.rosterByAccount undefined`.

- [ ] **Step 3: Implement**

`app/app.go`, after the `roster` field:
```go
	// rosterByAccount is each extra account's roster, keyed by account then
	// working directory: `claude agents --json` lists only its own config
	// dir's sessions, so each account is queried as itself. The default
	// account's stays in roster. Update-goroutine only.
	rosterByAccount map[string]map[string]session.RosterEntry
```

`app/events.go`:
- Add to `rosterReadyMsg` after `err error`:
```go
	// extra holds each non-default account's roster and extraErrs its
	// failed queries; entries/err stay the default account's.
	extra     map[string]map[string]session.RosterEntry
	extraErrs map[string]error
```
- Replace `rosterQueryCmd` (keep the doc comment's first sentence and extend it):
```go
// rosterQueryCmd schedules the roster queries covering the whole fleet: one
// per account in use, since `claude agents --json` lists only the sessions
// of the config dir it runs under (dirs maps extra accounts to theirs).
// Returns nil when no active instance runs Claude, so a fleet of aider or
// shell sessions never pays for a Claude subprocess. The binary is taken
// from a live instance's Program rather than assumed to be "claude" on
// PATH, so absolute paths (a Nix store path, a version-pinned install)
// resolve to the same CLI the agents were launched with. The queries run in
// parallel inside the one Cmd, which still returns a single message.
func rosterQueryCmd(active []*session.Instance, dirs map[string]string) tea.Cmd {
	var program string
	accounts := map[string]bool{}
	for _, inst := range active {
		p := inst.Program()
		if !session.IsClaudeProgram(p) {
			continue
		}
		if program == "" {
			program = p
		}
		accounts[inst.Account()] = true
	}
	if program == "" {
		return nil
	}
	return func() tea.Msg {
		msg := rosterReadyMsg{at: time.Now()}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for name := range accounts {
			var env []string
			if name != "" {
				dir, ok := dirs[name]
				if !ok {
					continue // a removed account: its instances get no opinion
				}
				env = account.EnvFor(dir)
			}
			wg.Add(1)
			go func(name string, env []string) {
				defer wg.Done()
				entries, err := session.QueryClaudeRosterEnv(program, env, internalexec.Default{})
				mu.Lock()
				defer mu.Unlock()
				if name == "" {
					msg.entries, msg.err = entries, err
					return
				}
				if msg.extra == nil {
					msg.extra = map[string]map[string]session.RosterEntry{}
					msg.extraErrs = map[string]error{}
				}
				if err != nil {
					msg.extraErrs[name] = err
				} else {
					msg.extra[name] = entries
				}
			}(name, env)
		}
		wg.Wait()
		return msg
	}
}
```
(imports: `"sync"`, `"github.com/aidan-bailey/loom/account"`).
- In `maybeRosterQuery`: `return rosterQueryCmd(active, m.accountDirs())`.
- In `rosterStatusFor`, replace the first two statements with:
```go
	if inst == nil || !session.IsClaudeProgram(inst.Program()) {
		return session.Ready, "", false
	}
	roster := m.roster
	if acct := inst.Account(); acct != "" {
		roster = m.rosterByAccount[acct]
	}
	if len(roster) == 0 {
		return session.Ready, "", false
	}
	entry, ok := roster[inst.GetWorktreePath()]
```
and add to its doc comment: "An extra account's session is joined against that account's roster only."

`app/app.go`, `case rosterReadyMsg:` — after the existing `if msg.err != nil {…} else {…}` block and before `m.observeRoster(msg.at)`:
```go
		// Another account's failed query clears only its own entries: the
		// map is replaced wholesale and a failed account is absent from it.
		for name, err := range msg.extraErrs {
			log.DebugKV("app.roster.query_failed", "account", name, "err", err.Error())
		}
		m.rosterByAccount = msg.extra
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./app/... && CC=clang CGO_ENABLED=1 go test -race ./app/ -run Roster`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w app/events.go app/app.go app/roster_accounts_test.go app/roster_status_test.go
git add app/
git commit -m "feat(app): query the Claude roster once per account in use

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 16: Usage polling

**Files:**
- Create: `app/usage.go`
- Modify: `app/pollgate.go` (enum, `String`, `gateIntervals`, `redispatch`)
- Modify: `app/app.go` (`Init`, health tick ~908-972, `Update` case)
- Test: `app/usage_test.go`

- [ ] **Step 1: Write the failing tests**

`app/usage_test.go`:
```go
package app

import (
	"errors"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageProbe_NotDispatchedWithoutAnExtraAccount(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "claude"
	withAccounts(t, m)

	assert.Nil(t, m.maybeUsageProbe())
	assert.False(t, m.gate(gateUsage).inFlight, "no Cmd, nothing armed")
}

func TestUsageProbe_NotDispatchedWithoutAClaudeProgram(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "aider"
	withAccounts(t, m, "max-2")

	assert.Nil(t, m.maybeUsageProbe())
}

func TestUsageProbe_DispatchesOnceAndThrottles(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "claude"
	withAccounts(t, m, "max-2")

	require.NotNil(t, m.maybeUsageProbe())
	assert.True(t, m.gate(gateUsage).inFlight)
	assert.Nil(t, m.maybeUsageProbe(), "one probe in flight at a time")
}

func TestUsageReady_KeepsTheLastGoodSampleOnError(t *testing.T) {
	m := homeWithAppState(t)
	withAccounts(t, m, "max-2")
	good := account.Usage{Available: true, At: time.Now(), FiveHour: &account.Window{Pct: 12}}

	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{results: map[string]account.Usage{"max-2": good}}})
	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{errs: map[string]error{"max-2": errors.New("timeout")}}})

	got := m.usage["max-2"]
	assert.Equal(t, good, got.last, "display-only: a failed probe keeps the sample")
	assert.Error(t, got.err)
	assert.False(t, m.gate(gateUsage).inFlight)
	st := m.accountStatuses()
	assert.True(t, st[1].Failing)
}

func TestRequestUsageProbe_BringsTheNextProbeForward(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "claude"
	withAccounts(t, m, "max-2")
	require.NotNil(t, m.maybeUsageProbe())
	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{}})
	assert.Nil(t, m.maybeUsageProbe(), "throttled by usageInterval")

	assert.NotNil(t, m.requestUsageProbe())
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./app/ -run 'UsageProbe|UsageReady'`
Expected: FAIL to compile, `undefined: gateUsage`.

- [ ] **Step 3: Implement**

`app/pollgate.go`:
- In the `gateKind` consts, before `numGateKinds`:
```go
	// gateUsage throttles the account usage probes (maybeUsageProbe).
	gateUsage
```
- In `String()`: `case gateUsage: return "usage"`.
- In `gateIntervals`: `gateUsage: usageInterval,`.
- In `redispatch`: `case gateUsage: return m.maybeUsageProbe()`.

`app/usage.go`:
```go
package app

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
)

// usageInterval is the usage probes' cadence. Plan usage moves slowly and
// the views dim a sample older than two intervals (ui.UsageStaleAfter);
// the moments the user is choosing an account expedite a probe instead
// (requestUsageProbe).
const usageInterval = 2 * time.Minute

// usageTarget is one account to probe. dir is its config dir, "" for the
// default account.
type usageTarget struct{ name, dir string }

// usageReadyMsg carries one round of probes: results for the accounts that
// answered, errs for the ones that did not.
type usageReadyMsg struct {
	results map[string]account.Usage
	errs    map[string]error
}

// usageProbeCmd probes every target in parallel and returns one message.
// Each probe runs in its account's config dir (the main dir for default)
// so no project entry is recorded for an arbitrary directory.
func usageProbeCmd(program, mainDir string, targets []usageTarget, r internalexec.Executor) tea.Cmd {
	return func() tea.Msg {
		msg := usageReadyMsg{results: map[string]account.Usage{}, errs: map[string]error{}}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, t := range targets {
			wg.Add(1)
			go func(t usageTarget) {
				defer wg.Done()
				cwd, env := mainDir, []string(nil)
				if t.dir != "" {
					cwd, env = t.dir, account.EnvFor(t.dir)
				}
				u, err := account.ProbeUsage(program, env, cwd, r)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					msg.errs[t.name] = err
				} else {
					msg.results[t.name] = u
				}
			}(t)
		}
		wg.Wait()
		return msg
	}
}

// maybeUsageProbe returns a probe round when gateUsage is due, an extra
// account exists, and a Claude CLI is configured. Update goroutine only.
func (m *home) maybeUsageProbe() tea.Cmd {
	return m.dispatchGated(gateUsage, time.Now(), func() tea.Cmd {
		if !m.hasExtraAccounts() {
			return nil
		}
		program := m.claudeProgram()
		if program == "" {
			return nil
		}
		targets := []usageTarget{{name: account.DefaultName}}
		for _, a := range m.accounts.Accounts {
			targets = append(targets, usageTarget{name: a.Name, dir: a.Dir})
		}
		return usageProbeCmd(program, m.mainConfigDir(), targets, internalexec.Default{})
	})
}

// requestUsageProbe brings the next probe round forward: the user is about
// to choose an account (a picker opened) or the accounts changed.
func (m *home) requestUsageProbe() tea.Cmd {
	m.gate(gateUsage).request()
	return m.maybeUsageProbe()
}

// handleUsageReady stores a probe round. A failed probe keeps the account's
// last good sample and records the error, which the views show as a dimmed,
// aged value; usage never drives a status, so stale beats blank here.
func (m *home) handleUsageReady(msg usageReadyMsg) tea.Cmd {
	m.ensureAccountMaps()
	for name, u := range msg.results {
		m.usage[name] = accountUsage{last: u}
	}
	for name, err := range msg.errs {
		cur := m.usage[name]
		cur.err = err
		m.usage[name] = cur
		log.For("account").Debug("usage.probe_failed", "account", name, "err", err.Error())
	}
	return m.refreshAccountViews()
}
```

`app/app.go`:
- `Init`: add `m.maybeUsageProbe(),` after `m.accountsRefreshCmd(false),`.
- Health tick (`case tickUpdateMetadataMessage:`), after the GitHub poll block:
```go
		// Account plan usage, on its own 2-minute cadence (see
		// maybeUsageProbe). nil when not due, in flight, or no extra
		// account is registered.
		if usage := m.maybeUsageProbe(); usage != nil {
			cmds = append(cmds, usage)
		}
```
- `Update`: next to `case accountsRefreshedMsg:`
```go
	case usageReadyMsg:
		return m, m.handleUsageReady(msg)
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./app/... && CC=clang CGO_ENABLED=1 go test -race ./app/ -run 'Usage|Gate'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w app/usage.go app/usage_test.go app/pollgate.go app/app.go
git add app/
git commit -m "feat(app): poll each account's plan usage on its own gate

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 17: Launch flows choose the account

**Files:**
- Modify: `app/accounts.go` (helpers; `refreshAccountViews`)
- Modify: `app/remote_control.go` (`remoteControlBlocked`, the two prompt functions)
- Modify: `app/state_prompt.go:74-107`, `app/state_issue_picker.go:252-294`, `app/intents.go:428-491`
- Test: `app/launch_account_test.go`; `app/flow_selection_test.go:408`

- [ ] **Step 1: Write the failing tests**

`app/launch_account_test.go`:
```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bareOpts(acct string) overlay.LaunchOptions {
	return overlay.LaunchOptions{PermissionMode: "default", Model: "default", Effort: "default", Account: acct}
}

func TestNewLaunchOptionsOverlay_NoAccountRowWithoutExtras(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m)
	lo := m.newLaunchOptionsOverlay(bareOpts(""))
	assert.NotContains(t, lo.Render(), "Account")
}

func TestNewLaunchOptionsOverlay_PreselectsTheRegistryDefault(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	require.NoError(t, m.accounts.SetDefault("max-2"))

	lo := m.newLaunchOptionsOverlay(bareOpts(""))

	assert.Equal(t, "max-2", lo.Options().Account)
	assert.Contains(t, lo.Render(), "Account")
}

func TestNewLaunchOptionsOverlay_AnUnknownAccountFallsBackToTheDefault(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m)
	lo := m.newLaunchOptionsOverlay(bareOpts("gone"))
	assert.Equal(t, account.DefaultName, lo.Options().Account,
		"R on a session whose account was removed must not relaunch as it again")
}

func TestApplyChosenLaunch_RecordsTheAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	inst, err := session.NewInstance(session.InstanceOptions{Title: "acct-launch", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)

	m.applyChosenLaunch(inst, bareOpts("max-2"), "claude")
	assert.Equal(t, "max-2", inst.Account())

	m.applyChosenLaunch(inst, bareOpts(account.DefaultName), "claude")
	assert.Equal(t, "", inst.Account())
}

func TestApplyChosenLaunch_UsesTheAccountsRemoteControlAuth(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "logged out"}}
	inst, err := session.NewInstance(session.InstanceOptions{Title: "acct-rc", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)

	opts := bareOpts("max-2")
	opts.RemoteControl = true
	m.applyChosenLaunch(inst, opts, "claude")
	assert.NotContains(t, inst.Program(), "--remote-control")
	assert.True(t, m.remoteControlBlockedOn("max-2", true, "claude"))

	opts.Account = account.DefaultName
	m.applyChosenLaunch(inst, opts, "claude")
	assert.Contains(t, inst.Program(), "--remote-control")
	assert.False(t, m.remoteControlBlockedOn(account.DefaultName, true, "claude"))
}

func TestRestartWithOptions_PresetsTheSessionsAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2", "max-3")
	inst, err := session.NewInstance(session.InstanceOptions{Title: "acct-restart", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetAccount("max-3")
	m.list.AddInstance(inst)
	require.Equal(t, inst, m.list.GetSelectedInstance())

	runRestartWithOptionsSelected(m)

	lo := m.launchOptionsOverlay()
	require.NotNil(t, lo)
	assert.Equal(t, "max-3", lo.Options().Account)
}
```
In `app/flow_selection_test.go:408` change `m.promptRemoteControlBlocked(overlay.ConfirmationTask{})` to `m.promptRemoteControlBlocked(overlay.ConfirmationTask{}, "")`.

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./app/ -run 'NewLaunchOptionsOverlay|ApplyChosenLaunch|RestartWithOptions_Presets'`
Expected: FAIL to compile, `m.newLaunchOptionsOverlay undefined`.

- [ ] **Step 3: Implement**

Append to `app/accounts.go` (add imports `"time"` and `"github.com/aidan-bailey/loom/ui/overlay"`):
```go
// accountChoices are the Launch Options Account row's options, default
// first; nil (the row hidden) without an extra account.
func (m *home) accountChoices(statuses []ui.AccountStatus) []overlay.AccountChoice {
	if !m.hasExtraAccounts() {
		return nil
	}
	now := time.Now()
	out := make([]overlay.AccountChoice, 0, len(statuses))
	for _, s := range statuses {
		a := m.rcAuthFor(s.Name)
		out = append(out, overlay.AccountChoice{
			Name:      s.Name,
			Summary:   ui.AccountUsageText(s, now),
			RCBlocked: a.Blocked(),
			RCReason:  a.Reason,
		})
	}
	return out
}

// newLaunchOptionsOverlay builds the Session Launch Options modal for opts,
// with the Account row when an extra account exists. An empty or
// unregistered opts.Account becomes the registry default, so R on a session
// whose account was removed can't relaunch as it again.
func (m *home) newLaunchOptionsOverlay(opts overlay.LaunchOptions) *overlay.SessionLaunchOptions {
	switch {
	case m.accounts == nil:
		opts.Account = ""
	case opts.Account == "":
		opts.Account = m.accounts.Default()
	case opts.Account != account.DefaultName:
		if _, ok := m.accounts.Get(opts.Account); !ok {
			opts.Account = m.accounts.Default()
		}
	}
	lo := overlay.NewSessionLaunchOptions(opts, m.rcAuth.Blocked(), m.rcAuth.Reason)
	lo.SetAccounts(m.accountChoices(m.accountStatuses()))
	return lo
}

// applyChosenLaunch records the chosen launch options on inst: the program
// composed from base with the chosen account's remote-control auth, the env
// toggles, and the account itself.
func (m *home) applyChosenLaunch(inst *session.Instance, opts overlay.LaunchOptions, base string) {
	inst.SetLaunchOptions(applyLaunchOptions(opts, m.rcAuthFor(opts.Account), base, inst.Title), opts.HeadroomProxy, opts.CacheTTL1h)
	inst.SetAccount(opts.Account)
}

// accountOrDefault maps an instance's stored account ("" = default) to the
// name the Account row shows.
func accountOrDefault(name string) string {
	if name == "" {
		return account.DefaultName
	}
	return name
}
```
Update `refreshAccountViews` (same file) so an open Launch Options modal follows new usage:
```go
func (m *home) refreshAccountViews() tea.Cmd {
	statuses := m.accountStatuses()
	if lo := m.launchOptionsOverlay(); lo != nil {
		lo.SetAccounts(m.accountChoices(statuses))
	}
	if m.accountStrip == nil {
		return nil
	}
	before := m.accountStrip.Height()
	m.accountStrip.SetAccounts(statuses)
	if m.accountStrip.Height() != before {
		return tea.RequestWindowSize
	}
	return nil
}
```

`app/remote_control.go`:
```go
// remoteControlBlocked is remoteControlBlockedOn for the default account
// (workspace terminals, which always run on it).
func (m *home) remoteControlBlocked(rcEnabled bool, program string) bool {
	return m.remoteControlBlockedOn("", rcEnabled, program)
}

// remoteControlBlockedOn reports whether a launch of program on account
// acct should be interrupted to tell the user remote control can't work:
// the toggle is on, the program is Claude, and acct's auth was clearly
// determined incompatible.
func (m *home) remoteControlBlockedOn(acct string, rcEnabled bool, program string) bool {
	return rcEnabled && session.IsClaudeProgram(program) && m.rcAuthFor(acct).Blocked()
}
```
(replacing the old `remoteControlBlocked` and its doc comment). Give `promptRemoteControlBlocked` and `promptRestartRemoteControlBlocked` a trailing `reason string` parameter and use it in place of `m.rcAuth.Reason` in their messages (mention in each doc comment: "reason is the launching account's auth reason").

`app/state_prompt.go` inside the `pendingLaunchOptions` closure:
- `selected.SetLaunchOptions(applyLaunchOptions(opts, m.rcAuth, selected.Program(), selected.Title), opts.HeadroomProxy, opts.CacheTTL1h)` → `m.applyChosenLaunch(selected, opts, selected.Program())`
- `if m.remoteControlBlocked(effectiveRemoteControl(opts), selected.Program()) {` → `if m.remoteControlBlockedOn(opts.Account, effectiveRemoteControl(opts), selected.Program()) {`
- `return m, m.promptRemoteControlBlocked(startTask)` → `return m, m.promptRemoteControlBlocked(startTask, m.rcAuthFor(opts.Account).Reason)`
- after the closure: `m.setOverlay(overlay.NewSessionLaunchOptions(launchOptionsFromConfig(m.appConfig), m.rcAuth.Blocked(), m.rcAuth.Reason), overlayLaunchOptions)` → `m.setOverlay(m.newLaunchOptionsOverlay(launchOptionsFromConfig(m.appConfig)), overlayLaunchOptions)`, and its `return m, tea.RequestWindowSize` → `return m, tea.Batch(tea.RequestWindowSize, m.requestUsageProbe())`

`app/state_issue_picker.go` `openLaunchOptionsForNew`: the same four edits (`instance` instead of `selected`).

`app/intents.go` `runRestartWithOptionsSelected`:
- after `opts.CacheTTL1h = selected.CacheTTL1h()` add:
```go
	// The account never reaches the program either; seed it from the
	// instance so R preselects the session's own account.
	opts.Account = accountOrDefault(selected.Account())
```
- `selected.SetLaunchOptions(applyLaunchOptions(newOpts, m.rcAuth, base, selected.Title), newOpts.HeadroomProxy, newOpts.CacheTTL1h)` → `m.applyChosenLaunch(selected, newOpts, base)`
- `if m.remoteControlBlocked(effectiveRemoteControl(newOpts), selected.Program()) {` → `if m.remoteControlBlockedOn(newOpts.Account, effectiveRemoteControl(newOpts), selected.Program()) {`
- `return m, m.promptRestartRemoteControlBlocked(resumeTask)` → `return m, m.promptRestartRemoteControlBlocked(resumeTask, m.rcAuthFor(newOpts.Account).Reason)`
- `lo := overlay.NewSessionLaunchOptions(opts, m.rcAuth.Blocked(), m.rcAuth.Reason)` → `lo := m.newLaunchOptionsOverlay(opts)`
- the final `return m, tea.RequestWindowSize` → `return m, tea.Batch(tea.RequestWindowSize, m.requestUsageProbe())`

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./app/...`
Expected: PASS, including the existing `state_restart_options_test.go`, `state_prompt_test.go`, `state_new_test.go` and `remote_control_test.go`.

- [ ] **Step 5: Commit**

```bash
gofmt -w app/accounts.go app/remote_control.go app/state_prompt.go app/state_issue_picker.go app/intents.go app/launch_account_test.go app/flow_selection_test.go
git add app/
git commit -m "feat(app): choose the Claude account in Launch Options and on R

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 18: Strip in the layout; `topChromeHeight`

**Files:**
- Modify: `app/app.go` (sites at ~431, 461, 1249, 1272, 2537-2538; `updateHandleWindowSizeEvent`; `View` ~2406), `app/interact.go:19,32`, `app/workspaces.go:190,482`
- Modify: `app/accounts.go` (`topChromeHeight`)
- Test: `app/top_chrome_test.go`

- [ ] **Step 1: Write the failing test**

`app/top_chrome_test.go`:
```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
)

func TestTopChromeHeight_CountsTheStrip(t *testing.T) {
	m := newTestHome(t)
	m.accountStrip = ui.NewAccountStrip()
	withAccounts(t, m)
	m.refreshAccountViews()
	base := m.topChromeHeight()
	assert.Equal(t, m.tabBar.Height(), base, "no extra account: no strip row")

	withAccounts(t, m, "max-2")
	m.refreshAccountViews()
	assert.Equal(t, base+1, m.topChromeHeight())
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./app/ -run TopChromeHeight`
Expected: FAIL to compile, `m.topChromeHeight undefined`.

- [ ] **Step 3: Implement**

Replace every `m.tabBar.Height()` in the three files first (before adding the helper, so the helper's own use is not rewritten):
```bash
sed -i 's/m\.tabBar\.Height()/m.topChromeHeight()/g' app/app.go app/interact.go app/workspaces.go
```
Then append to `app/accounts.go`:
```go
// topChromeHeight is the rows above the content: the account strip (when
// shown) plus the workspace tab bar. Every content-height and mouse/cursor
// offset goes through it, so both rows stay accounted for.
func (m *home) topChromeHeight() int {
	h := m.tabBar.Height()
	if m.accountStrip != nil {
		h += m.accountStrip.Height()
	}
	return h
}
```
Fix up the comment at `app/app.go` ~2537 so it reads `// HitTest(mouse.X - m.listWidth, mouse.Y - m.topChromeHeight()).` (sed already did) and the comment at ~460 so it reads `//   top chrome + 1 (agent top border) + content + 1 (agent bottom border) - 1`.

In `updateHandleWindowSizeEvent`, after `m.tabBar.SetWidth(msg.Width)`:
```go
	if m.accountStrip != nil {
		m.accountStrip.SetWidth(msg.Width)
	}
```
In `View`, before `if tabBarStr := m.tabBar.String(); tabBarStr != "" {`:
```go
	if m.accountStrip != nil {
		if strip := m.accountStrip.String(); strip != "" {
			sections = append(sections, strip)
		}
	}
```

- [ ] **Step 4: Verify every offset site moved, then run the tests**

Run: `grep -rn "tabBar.Height()" app/*.go | grep -v _test`
Expected: exactly one hit, inside `topChromeHeight`.

Run: `CGO_ENABLED=0 go test ./app/...`
Expected: PASS (mouse-selection tests are unaffected: without extra accounts the offset is unchanged).

The strip's look and the drag-select alignment are checked in the real TUI in Task 22, step 2: showing the strip needs an account, which the TUI can add only after Task 19.

- [ ] **Step 5: Commit**

```bash
gofmt -w app/app.go app/interact.go app/workspaces.go app/accounts.go app/top_chrome_test.go
git add app/
git commit -m "feat(app): account usage strip above the tab bar

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 19: Accounts screen actions and the in-use check

**Files:**
- Create: `account/users.go`
- Modify: `app/accounts.go`, `app/intents.go:647-658` (`runOpenSettings`), `app/state_settings.go`, `app/app.go` (`Update` case)
- Test: `account/users_test.go`, `app/accounts_overlay_test.go`

- [ ] **Step 1: Write the failing tests**

`account/users_test.go`:
```go
package account

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeState(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o644))
}

func TestCountUsers(t *testing.T) {
	a, b, missing := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "none")
	writeState(t, a, `{"instances":[{"title":"x","account":"max-2"},{"title":"y"}]}`)
	writeState(t, b, `{"instances":[{"title":"z","account":"max-2"}]}`)

	n, err := CountUsers([]string{a, b, missing, a}, "max-2")

	require.NoError(t, err)
	assert.Equal(t, 2, n, "a missing file counts zero and a repeated dir counts once")
}

func TestCountUsers_CorruptStateIsAnError(t *testing.T) {
	a := t.TempDir()
	writeState(t, a, "not json")
	_, err := CountUsers([]string{a}, "max-2")
	assert.Error(t, err, "in-use must not be answered with a guess")
	data, _ := os.ReadFile(filepath.Join(a, "state.json"))
	assert.Equal(t, "not json", string(data), "read-only: never quarantined")
}

func TestKnownStateDirs(t *testing.T) {
	global, home := t.TempDir(), t.TempDir()
	t.Setenv("LOOM_GLOBAL_DIR", global)
	t.Setenv("LOOM_HOME", home)
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(global, "workspaces.json"),
		[]byte(`{"workspaces":[{"name":"r","path":"`+repo+`"}]}`), 0o644))

	dirs, err := KnownStateDirs()

	require.NoError(t, err)
	assert.Equal(t, []string{global, home, filepath.Join(repo, ".loom")}, dirs)
}
```

`app/accounts_overlay_test.go`:
```go
package app

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountRequest_SetDefault(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestSetDefault, Name: "max-2"})
	assert.Equal(t, "max-2", m.accounts.Default())
}

func TestAccountRequest_AddCreatesAndLogsIn(t *testing.T) {
	m := newTestHome(t)
	main := withAccounts(t, m)
	m.rcAuth.Identity = account.Identity{LoggedIn: true, ConfigDir: main}

	cmd := m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestAdd, Name: "max-3"})

	require.NotNil(t, cmd, "the login runs next")
	_, ok := m.accounts.Get("max-3")
	assert.True(t, ok)
	assert.True(t, m.hasExtraAccounts())
}

func TestAccountRequest_RemoveIsRefusedWhileASessionUsesIt(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	inst, err := session.NewInstance(session.InstanceOptions{Title: "on-max-2", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetAccount("max-2")
	m.list.AddInstance(inst)

	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestRemove, Name: "max-2"})

	_, ok := m.accounts.Get("max-2")
	assert.True(t, ok, "in use: not removed")
}

func TestAccountRequest_RemoveDeletesAnUnusedAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	acct, _ := m.accounts.Get("max-2")

	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestRemove, Name: "max-2"})

	_, ok := m.accounts.Get("max-2")
	assert.False(t, ok)
	_, err := os.Stat(acct.Dir)
	assert.True(t, os.IsNotExist(err))
	assert.False(t, m.hasExtraAccounts())
}

func TestRunOpenSettings_ListsTheAccounts(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	runOpenSettings(m)
	so := m.settingsOverlay()
	require.NotNil(t, so)
	assert.Contains(t, so.Render(), "Accounts")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./account/ -run 'CountUsers|KnownStateDirs'; CGO_ENABLED=0 go test ./app/ -run 'AccountRequest|RunOpenSettings_Lists'`
Expected: both FAIL to compile (`undefined: CountUsers`, `m.handleAccountRequest undefined`).

- [ ] **Step 3: Implement**

`account/users.go`:
```go
package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aidan-bailey/loom/config"
)

// CountUsers counts the stored sessions that run on account name across
// the state.json files in configDirs. Read-only: unlike
// config.LoadStateFrom it never quarantines a corrupt file. A missing file
// counts zero; an unreadable or corrupt one is an error, because "is this
// account in use" must not be answered with a guess. A repeated dir counts
// once.
func CountUsers(configDirs []string, name string) (int, error) {
	seen := map[string]bool{}
	n := 0
	for _, dir := range configDirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		path := filepath.Join(dir, config.StateFileName)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", path, err)
		}
		var st struct {
			Instances []struct {
				Account string `json:"account"`
			} `json:"instances"`
		}
		if err := json.Unmarshal(data, &st); err != nil {
			return 0, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, in := range st.Instances {
			if in.Account == name {
				n++
			}
		}
	}
	return n, nil
}

// KnownStateDirs lists every config dir that can hold sessions: the global
// dir, LOOM_HOME's when it differs, and each registered workspace's.
func KnownStateDirs() ([]string, error) {
	global, err := config.GetGlobalConfigDir()
	if err != nil {
		return nil, err
	}
	dirs := []string{global}
	if home, err := config.GetConfigDir(); err == nil && home != global {
		dirs = append(dirs, home)
	}
	reg, err := config.LoadWorkspaceRegistry()
	if err != nil {
		return nil, err
	}
	for i := range reg.Workspaces {
		dirs = append(dirs, config.WorkspaceConfigDir(&reg.Workspaces[i]))
	}
	return dirs, nil
}
```

Append to `app/accounts.go` (add imports `"errors"`, `"github.com/aidan-bailey/loom/ui/overlay"` if not yet present):
```go
// accountRows formats the Accounts screen's rows.
func (m *home) accountRows(statuses []ui.AccountStatus) []overlay.AccountRow {
	now := time.Now()
	rows := make([]overlay.AccountRow, 0, len(statuses))
	for _, s := range statuses {
		id := m.rcAuthFor(s.Name).Identity
		row := overlay.AccountRow{Name: s.Name, Email: id.Email, Plan: id.Plan, Usage: ui.AccountUsageText(s, now), IsDefault: s.IsDefault}
		var warns []string
		if s.LoggedOut {
			warns = append(warns, "logged out")
		}
		if rep, ok := m.accountSync[s.Name]; ok && len(rep.Diverged) > 0 {
			warns = append(warns, "not shared: "+strings.Join(rep.Diverged, ", "))
		}
		row.Warning = strings.Join(warns, "; ")
		rows = append(rows, row)
	}
	return rows
}

// accountUsers counts the sessions on acct: every loaded slot's live
// instances plus the stored records of every workspace (open or not).
func (m *home) accountUsers(acct string) (int, error) {
	n := 0
	for _, inst := range m.allInstances() {
		if inst.Account() == acct {
			n++
		}
	}
	dirs, err := account.KnownStateDirs()
	if err != nil {
		return n, err
	}
	stored, err := account.CountUsers(dirs, acct)
	return n + stored, err
}

// afterAccountsChanged republishes the registry and refreshes every view.
func (m *home) afterAccountsChanged() tea.Cmd {
	m.publishAccounts()
	return tea.Batch(m.refreshAccountViews(), m.requestUsageProbe())
}

// accountLoginDoneMsg is returned when `claude auth login` hands the
// terminal back.
type accountLoginDoneMsg struct {
	name string
	err  error
}

// accountLoginCmd suspends the TUI and runs `claude auth login` as acct in
// the real terminal (it is a browser flow), like $EDITOR in the file
// explorer.
func (m *home) accountLoginCmd(acct string) tea.Cmd {
	program := m.claudeProgram()
	if program == "" {
		program = "claude"
	}
	env, err := m.accounts.Env(acct)
	if err != nil {
		return m.handleError(err)
	}
	return tea.ExecProcess(account.LoginCmd(program, env), func(err error) tea.Msg {
		return accountLoginDoneMsg{name: acct, err: err}
	})
}

// handleAccountRequest carries out one Accounts-screen action.
func (m *home) handleAccountRequest(req overlay.AccountRequest) tea.Cmd {
	if m.accounts == nil {
		return m.handleError(errors.New("the account registry is unavailable"))
	}
	m.ensureAccountMaps()
	switch req.Kind {
	case overlay.AccountRequestAdd:
		acct, rep, err := m.accounts.Create(req.Name, m.mainConfigDir())
		if err != nil {
			return m.handleError(err)
		}
		m.accountSync[acct.Name] = rep
		return tea.Batch(m.afterAccountsChanged(), m.accountLoginCmd(acct.Name))
	case overlay.AccountRequestLogin:
		return m.accountLoginCmd(req.Name)
	case overlay.AccountRequestSetDefault:
		if err := m.accounts.SetDefault(req.Name); err != nil {
			return m.handleError(err)
		}
		return m.afterAccountsChanged()
	case overlay.AccountRequestRemove:
		n, err := m.accountUsers(req.Name)
		if err != nil {
			return m.handleError(fmt.Errorf("can't tell whether sessions use %s, so it was kept: %w", req.Name, err))
		}
		if n > 0 {
			return m.handleError(fmt.Errorf("%d session(s) use %s: kill them or relaunch them on another account (R) first", n, req.Name))
		}
		if _, err := m.accounts.Remove(req.Name); err != nil {
			return m.handleError(err)
		}
		delete(m.accountAuth, req.Name)
		delete(m.accountSync, req.Name)
		delete(m.usage, req.Name)
		return m.afterAccountsChanged()
	}
	return nil
}
```
Update `refreshAccountViews` to also refresh an open Settings overlay (final version):
```go
func (m *home) refreshAccountViews() tea.Cmd {
	statuses := m.accountStatuses()
	if so := m.settingsOverlay(); so != nil {
		so.SetAccountRows(m.accountRows(statuses))
	}
	if lo := m.launchOptionsOverlay(); lo != nil {
		lo.SetAccounts(m.accountChoices(statuses))
	}
	if m.accountStrip == nil {
		return nil
	}
	before := m.accountStrip.Height()
	m.accountStrip.SetAccounts(statuses)
	if m.accountStrip.Height() != before {
		return tea.RequestWindowSize
	}
	return nil
}
```

`app/app.go` `Update`, next to `case usageReadyMsg:`:
```go
	case accountLoginDoneMsg:
		// tea.ExecProcess has returned the terminal. Re-read the auth of the
		// account that just logged in (and the default's, which the
		// refresh covers when that is the one) and probe its usage.
		var cmds []tea.Cmd
		if msg.err != nil {
			cmds = append(cmds, m.handleError(fmt.Errorf("claude auth login for %s: %w", msg.name, msg.err)))
		}
		cmds = append(cmds, tea.RequestWindowSize, m.accountsRefreshCmd(msg.name == account.DefaultName), m.requestUsageProbe())
		return m, tea.Batch(cmds...)
```

`app/intents.go` `runOpenSettings`: after `so := overlay.NewSettingsOverlay(...)` add `so.SetAccountRows(m.accountRows(m.accountStatuses()))`, and change its final `return m, nil` to `return m, m.requestUsageProbe()`.

`app/state_settings.go` `handleStateSettingsKey`: after the `TakeError` block add
```go
	var cmds []tea.Cmd
	if req, ok := so.TakeAccountRequest(); ok {
		cmds = append(cmds, m.handleAccountRequest(req))
	}
```
and change the function's final `return m, nil` to `return m, tea.Batch(cmds...)`.

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./account/... ./app/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w account/users.go account/users_test.go app/accounts.go app/accounts_overlay_test.go app/intents.go app/state_settings.go app/app.go
git add account/ app/
git commit -m "feat(app): add, log in, remove and pick Claude accounts from Settings

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Phase E — CLI

### Task 20: `loom account`

**Files:**
- Create: `cmd/account.go`
- Modify: `main.go` (`init`, after `rootCmd.AddCommand(cmd2.WorkspaceCmd)`), `cmd/doc.go`
- Test: `cmd/account_test.go`

- [ ] **Step 1: Write the failing tests**

`cmd/account_test.go`:
```go
package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedAccountExec answers `auth status` and the usage probe.
type scriptedAccountExec struct{ auth, usage string }

func (s *scriptedAccountExec) out(c *exec.Cmd) ([]byte, error) {
	switch {
	case slices.Contains(c.Args, "status"):
		return []byte(s.auth), nil
	case slices.Contains(c.Args, "-p"):
		return []byte(s.usage), nil
	}
	return nil, errors.New("unexpected: " + strings.Join(c.Args, " "))
}
func (s *scriptedAccountExec) Run(c *exec.Cmd) error                      { _, err := s.out(c); return err }
func (s *scriptedAccountExec) Output(c *exec.Cmd) ([]byte, error)         { return s.out(c) }
func (s *scriptedAccountExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return s.out(c) }

const cliAuthJSON = `{"loggedIn":true,"authMethod":"claude.ai","email":"you@example.com","subscriptionType":"max","configDirectory":"/main"}`
const cliUsageJSON = `{"type":"control_response","response":{"subtype":"success","request_id":"loom-usage","response":{"subscription_type":"max","rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":12},"seven_day":{"utilization":31}}}}}`

// isolateAccounts points the account commands at throwaway dirs and a fake
// CLI. Returns the global dir.
func isolateAccounts(t *testing.T) string {
	t.Helper()
	global, main := t.TempDir(), t.TempDir()
	t.Setenv("LOOM_GLOBAL_DIR", global)
	t.Setenv("LOOM_HOME", t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(main, "CLAUDE.md"), []byte("x"), 0o600))
	origMain, origLogin, origExec := accountMainDir, accountLogin, accountExec
	accountMainDir = func(string) string { return main }
	accountLogin = func(string, []string) error { return nil }
	accountExec = &scriptedAccountExec{auth: cliAuthJSON, usage: cliUsageJSON}
	t.Cleanup(func() {
		accountMainDir, accountLogin, accountExec = origMain, origLogin, origExec
		accountNoLogin, accountForce = false, false
	})
	return global
}

func runAccount(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	AccountCmd.SetOut(&out)
	AccountCmd.SetErr(&out)
	AccountCmd.SetIn(strings.NewReader(stdin))
	AccountCmd.SetArgs(args)
	err := AccountCmd.Execute()
	return out.String(), err
}

func TestAccountAdd_NoLoginCreatesALinkedAccount(t *testing.T) {
	global := isolateAccounts(t)

	out, err := runAccount(t, "", "add", "max-2", "--no-login")

	require.NoError(t, err)
	assert.Contains(t, out, "Created")
	acct, ok := account.LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	_, err = os.Readlink(filepath.Join(acct.Dir, "CLAUDE.md"))
	assert.NoError(t, err)
}

func TestAccountAdd_LogsInAsTheNewAccount(t *testing.T) {
	isolateAccounts(t)
	var gotEnv []string
	accountLogin = func(_ string, env []string) error { gotEnv = env; return nil }

	out, err := runAccount(t, "", "add", "max-2")

	require.NoError(t, err)
	require.Len(t, gotEnv, 1)
	assert.True(t, strings.HasPrefix(gotEnv[0], "CLAUDE_CONFIG_DIR="))
	assert.Contains(t, out, "Logged in max-2 as you@example.com (max)")
}

func TestAccountUse_SetsTheDefault(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	_, err = runAccount(t, "", "use", "max-2")

	require.NoError(t, err)
	assert.Equal(t, "max-2", account.LoadRegistry(global).Default())
}

func TestAccountList_ShowsUsage(t *testing.T) {
	isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "max-2")
	assert.Contains(t, out, "12%")
	assert.Contains(t, out, "31%")
	assert.Contains(t, out, "*")
}

func TestAccountRemove_RefusedWhileInUse(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(global, "state.json"),
		[]byte(`{"instances":[{"title":"t","account":"max-2"}]}`), 0o644))

	_, err = runAccount(t, "y\n", "remove", "max-2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 session(s) use max-2")
	_, ok := account.LoadRegistry(global).Get("max-2")
	assert.True(t, ok)
}

func TestAccountRemove_ConfirmsAndDeletes(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, _ := account.LoadRegistry(global).Get("max-2")

	out, err := runAccount(t, "y\n", "remove", "max-2")

	require.NoError(t, err)
	assert.Contains(t, out, "Removed max-2")
	_, statErr := os.Stat(acct.Dir)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAccountRemove_AbortsWithoutYes(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	out, err := runAccount(t, "\n", "remove", "max-2")

	require.NoError(t, err)
	assert.Contains(t, out, "Aborted")
	_, ok := account.LoadRegistry(global).Get("max-2")
	assert.True(t, ok)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./cmd/ -run Account`
Expected: FAIL to compile, `undefined: accountMainDir`.

- [ ] **Step 3: Implement**

`cmd/account.go`:
```go
package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"

	"github.com/spf13/cobra"
)

var (
	accountNoLogin bool
	accountForce   bool

	// accountExec runs the claude CLI for the account commands; a var so
	// tests answer it without a real claude.
	accountExec Executor = Exec{}
	// accountMainDir resolves the main config dir accounts link to; a var
	// so tests never read the developer's ~/.claude.
	accountMainDir = defaultMainConfigDir
	// accountLogin runs the interactive login; a var so tests skip it.
	accountLogin = runAccountLogin
)

// AccountCmd is the parent command for Claude account management.
var AccountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage the Claude accounts sessions can run on",
}

func loadAccountRegistry() (*account.Registry, error) {
	globalDir, err := config.GetGlobalConfigDir()
	if err != nil {
		return nil, err
	}
	reg := account.LoadRegistry(globalDir)
	return reg, reg.LoadErr()
}

// claudeProgram is the Claude CLI the account commands run: the global
// config's program when it is Claude (so a pinned or Nix path is honored),
// else "claude" on PATH.
func claudeProgram() string {
	if p := config.LoadConfigFromGlobal().GetProgram(); session.IsClaudeProgram(p) {
		return p
	}
	return "claude"
}

// defaultMainConfigDir is the default account's config dir
// (account.MainDir over what `claude auth status` reports).
func defaultMainConfigDir(program string) string {
	id, _ := account.AuthStatus(program, nil, accountExec)
	return account.MainDir(id)
}

func runAccountLogin(program string, env []string) error {
	c := account.LoginCmd(program, env)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// loginAndReport runs the login, then says who the account is now.
func loginAndReport(out io.Writer, program, name string, env []string) error {
	if err := accountLogin(program, env); err != nil {
		return fmt.Errorf("claude auth login for %s: %w", name, err)
	}
	id, err := account.AuthStatus(program, env, accountExec)
	switch {
	case err != nil:
		fmt.Fprintf(out, "Could not confirm the login: %v\n", err)
	case !id.LoggedIn:
		fmt.Fprintf(out, "%s is still logged out\n", name)
	default:
		fmt.Fprintf(out, "Logged in %s as %s (%s)\n", name, id.Email, id.Plan)
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

var accountAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create an account, share your Claude setup with it, and log it in",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		program := claudeProgram()
		main := accountMainDir(program)
		if main == "" {
			return fmt.Errorf("cannot locate your main Claude config dir")
		}
		acct, rep, err := reg.Create(args[0], main)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Created %s (linked %d entries from %s)\n", acct.Dir, len(rep.Linked), main)
		for _, d := range rep.Diverged {
			fmt.Fprintf(out, "  not shared: %s\n", d)
		}
		if accountNoLogin {
			fmt.Fprintf(out, "Log in later with: loom account login %s\n", acct.Name)
			return nil
		}
		return loginAndReport(out, program, acct.Name, account.EnvFor(acct.Dir))
	},
}

var accountLoginCmd = &cobra.Command{
	Use:   "login <name>",
	Short: `Log an account in to Claude ("default" is your main login)`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		env, err := reg.Env(args[0])
		if err != nil {
			return err
		}
		return loginAndReport(cmd.OutOrStdout(), claudeProgram(), args[0], env)
	},
}

var accountListCmd = &cobra.Command{
	Use:   "list",
	Short: "List accounts with their login and plan usage",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		program := claudeProgram()
		main := accountMainDir(program)
		now := time.Now()
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\tNAME\tEMAIL\tPLAN\t5H\t7D\tNOTE")
		for _, name := range reg.Names() {
			env, _ := reg.Env(name)
			cwd := main
			if a, ok := reg.Get(name); ok {
				cwd = a.Dir
			}
			mark := ""
			if name == reg.Default() {
				mark = "*"
			}
			var u account.Usage
			note := ""
			id, err := account.AuthStatus(program, env, accountExec)
			switch {
			case err != nil:
				note = err.Error()
			case !id.LoggedIn:
				note = "logged out"
			default:
				if u, err = account.ProbeUsage(program, env, cwd, accountExec); err != nil {
					note = err.Error()
				} else if !u.Available {
					note = "no plan limits"
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", mark, name, dash(id.Email), dash(id.Plan),
				dash(u.FiveHour.Text(now)), dash(u.SevenDay.Text(now)), note)
		}
		return w.Flush()
	},
}

var accountUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the account new sessions preselect",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		if err := reg.SetDefault(args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Default account set to %q\n", args[0])
		return nil
	},
}

var accountSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Share new entries of your main Claude config dir with every account",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		main := accountMainDir(claudeProgram())
		for _, a := range reg.Accounts {
			rep, err := account.Sync(a.Dir, main)
			if err != nil {
				return fmt.Errorf("%s: %w", a.Name, err)
			}
			fmt.Fprintf(out, "%s: linked %d new\n", a.Name, len(rep.Linked))
			for _, d := range rep.Diverged {
				fmt.Fprintf(out, "  not shared: %s\n", d)
			}
		}
		return nil
	},
}

var accountRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an account and delete its config dir",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, name := cmd.OutOrStdout(), args[0]
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		acct, ok := reg.Get(name)
		if !ok {
			return fmt.Errorf("account %q is not registered", name)
		}
		if !accountForce {
			dirs, err := account.KnownStateDirs()
			if err != nil {
				return fmt.Errorf("can't tell whether sessions use %s: %w (--force removes it anyway)", name, err)
			}
			n, err := account.CountUsers(dirs, name)
			if err != nil {
				return fmt.Errorf("can't tell whether sessions use %s: %w (--force removes it anyway)", name, err)
			}
			if n > 0 {
				return fmt.Errorf("%d session(s) use %s: kill them or relaunch them on another account first (or use --force)", n, name)
			}
			fmt.Fprintf(out, "Remove account %q and delete %s? [y/N] ", name, acct.Dir)
			line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				fmt.Fprintln(out, "Aborted.")
				return nil
			}
		}
		deleted, err := reg.Remove(name)
		if err != nil {
			return err
		}
		if deleted {
			fmt.Fprintf(out, "Removed %s and deleted %s\n", name, acct.Dir)
		} else {
			fmt.Fprintf(out, "Removed %s (left %s in place: loom did not create it)\n", name, acct.Dir)
		}
		return nil
	},
}

func init() {
	accountAddCmd.Flags().BoolVar(&accountNoLogin, "no-login", false, "Create the account without logging it in")
	accountRemoveCmd.Flags().BoolVar(&accountForce, "force", false, "Skip the confirmation and the in-use check")
	AccountCmd.AddCommand(accountAddCmd, accountLoginCmd, accountListCmd, accountUseCmd, accountSyncCmd, accountRemoveCmd)
}
```

`main.go` `init`: after `rootCmd.AddCommand(cmd2.WorkspaceCmd)` add `rootCmd.AddCommand(cmd2.AccountCmd)`.

`cmd/doc.go`: change the first paragraph's opening to "Package cmd holds the `loom workspace` and `loom account` Cobra subcommands and the subprocess seam the rest of Loom shells out through." and add a paragraph after the workspace one: "[AccountCmd] (account.go) groups add, login, list, use, sync and remove over the Claude account registry in the account package."

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go build -o /tmp/loom-acct . && CGO_ENABLED=0 go test ./cmd/... . && /tmp/loom-acct account --help`
Expected: PASS, and the help lists the six subcommands.

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/account.go cmd/account_test.go cmd/doc.go main.go
git add cmd/ main.go
git commit -m "feat(cmd): loom account add/login/list/use/sync/remove

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Phase F — docs and verification

### Task 21: Documentation

**Files:**
- Modify: `CLAUDE.md` (CLI Usage, Key Packages, Gotchas, Persistent State)
- Modify: `USAGE.md` (Table of Contents, Workflows)

- [ ] **Step 1: CLAUDE.md**

In `## CLI Usage`, after the workspace block, add:
```bash
# Claude accounts (spread sessions across several subscriptions)
loom account add <name> [--no-login]   # create ~/.loom/accounts/<name>, link your ~/.claude setup, claude auth login
loom account login <name>              # "default" is your main login
loom account list                      # email, plan, 5h/7d usage per account
loom account use <name>                # account new sessions preselect
loom account sync                      # share entries added to ~/.claude since
loom account remove <name> [--force]   # refused while a session uses it
```

In `### Key Packages`, add after the `session/github/` bullet:
```markdown
- **`account/`** — Extra Claude accounts. An account is a `CLAUDE_CONFIG_DIR` under `<globalDir>/accounts/<name>/`, registered in `<globalDir>/accounts.json` (`Registry`: reload-before-save, latched shut when the file fails to load); `default` is Claude with no override and is never stored. `Sync` links the main config dir into it, `AuthStatus`/`LoginCmd` wrap `claude auth`, `ProbeUsage` reads plan usage, `CountUsers` answers "in use" read-only. No app, ui or session imports; injected executor.
```

In `### Gotchas`, add:
```markdown
- **Claude accounts are config dirs, linked, and resolved at launch.** An extra account's dir symlinks every top-level entry of the main config dir except a deny-list (`.credentials.json`, `.claude.json*`, `sessions`, `daemon`, `session-env`, `ide`, `debug`, `cache`, `backups`, `shell-snapshots`, `statsig`), so `projects/` (transcripts, auto-memory) is shared and `--resume` works across accounts. `Sync` never replaces a real file: a link turned into a file (an atomic rewrite through it) is reported as diverged, since it may hold the only copy of a change; `Remove` relies on `os.RemoveAll` deleting links without following them, and only deletes dirs under `accounts/`. Instances store the account *name* (`InstanceData.Account`, schema v8, "" = default); every real launch resolves it through the map app publishes with `session.SetAccountDirs` and fails closed (`MissingAccountError`) rather than falling back to default, which would bill the wrong subscription; only Claude programs get `CLAUDE_CONFIG_DIR` (`LaunchEnv`). The roster is per config dir (`claude agents --json` under an empty dir returns `[]`), so `rosterQueryCmd` runs one query per account in use and `rosterStatusFor` joins `rosterByAccount[inst.Account()]` (the default account's stays in `m.roster`); one account's failed query clears only its own entries. Remote-control auth is per account too (`rcAuthFor`; an extra account not read yet is Unknown: no flag). Usage comes from `account.ProbeUsage`, a headless `claude -p` stream-json run answering the SDK's **experimental** `get_usage` control request (`skip_behaviors`, `--setting-sources ""`, no model call, ~1.4s), polled every 2 min on `gateUsage` and expedited when a picker opens; `TestRealClaude_UsageProbe` (opt-in, free) pins its shape. Usage is display-only: a failed probe keeps the last sample, dimmed with its age — the opposite of the roster's "stale is worse than none", because nothing acts on it. All account UI (strip, Launch Options row, badges) and polling stay off until an extra account exists; the strip's row goes through `topChromeHeight`, never `tabBar.Height()` directly.
```

In `### Persistent State`, add bullets:
```markdown
- `accounts.json` (global dir) — extra Claude accounts (`name`, `dir`) and the `default` new sessions preselect
- `accounts/<name>/` (global dir) — each extra account's `CLAUDE_CONFIG_DIR`: its own `.credentials.json`/`.claude.json`/runtime dirs, everything else symlinked to the main config dir
```
and in the `instances` bullet append "; `account`, since v8, names the Claude account the session runs on (empty = default)".

- [ ] **Step 2: USAGE.md**

Under `## Workflows`, after `### Session Recovery (orphaned worktrees)`, add:
```markdown
### Run Sessions on Several Claude Accounts

If you have more than one Claude subscription, loom can put each session on whichever has headroom.

1. Add an account: `loom account add max-2` (or **Settings → Accounts → a**). Loom creates `~/.loom/accounts/max-2`, shares your `~/.claude` setup with it (settings, `CLAUDE.md`, skills, plugins, transcripts and memory), and opens `claude auth login` for it.
2. A usage strip appears above the tabs: each account's 5-hour and weekly plan usage, refreshed every two minutes.
3. When you create a session, the **Account** row in Session Launch Options picks where it runs (preselected to the default; change it with `loom account use` or **enter** in Settings → Accounts). Cards and the agent pane title show `@account`.
4. To move a session that hit its limit, pause it and press **R**: choose another account and the conversation resumes there.

Removing an account (`loom account remove`, or **x** in Settings → Accounts) is refused while any session uses it. User-scope MCP servers (`claude mcp add --scope user`) live in each account's own `.claude.json` and are not shared.
```
Add the matching entry to the Table of Contents if it lists `### Workflows` subsections.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md USAGE.md
git commit -m "docs: Claude account selection

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 22: Full verification

- [ ] **Step 1: Everything green**

Run:
```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./account/... ./session/... ./app/... ./ui/... ./cmd/...
```
Expected: `gofmt -l` prints nothing; vet clean; all tests PASS.

- [ ] **Step 2: End-to-end in a sandbox**

With the `loom-dev` skill: `go run ./tools/loomdev up`, `go run ./tools/loomdev start`, then drive it with `keys` and check each step with `shot`:
- `S`, move to **Accounts**, `enter`, `a`, type `toy`, `enter`. The login hands the terminal to `claude auth login`; in the sandbox the agent is the fake one, so an error toast about the login is expected and fine. `esc` back out of Settings.
- The strip above the tab bar shows `*default` and `toy` (usage `—` or `logged out`, since nothing logged in).
- `n`, type a title, `enter`: Session Launch Options has an **Account** row at the bottom; `space` on it cycles `default` ↔ `toy`. `esc` cancels.
- The rail card of the sandbox's Claude session shows `@default`, and a mouse drag-select in the agent pane still highlights the rows under the pointer (the strip shifted the content down one row).
- Settings → **Accounts**, move to `toy`, `x`, `y`: it is removed and the strip disappears.

`go run ./tools/loomdev down` afterwards.

- [ ] **Step 3: Real contract test (needs a logged-in claude.ai account; free)**

Run: `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./account -run TestRealClaude -v`
Expected: PASS.
