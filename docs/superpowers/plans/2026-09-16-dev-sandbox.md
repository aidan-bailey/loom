# Dev Sandbox Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make it safe and easy to run, drive, and screenshot a dev build of loom from inside loom, for both the human (interactive) and the agent (headless verification).

**Architecture:** Three layers. (1) Loom itself gains isolation knobs: every tmux exec goes through one builder that honors `LOOM_TMUX_SOCKET`, `LOOM_GLOBAL_DIR` relocates the workspace registry, and a nesting guard refuses to start the TUI (or `reset`) inside a `loom_*` tmux session on the default server. (2) `internal/devsandbox` creates named, persistent sandboxes (toy repo, bare origin, seeded config/state, private tmux socket) and drives a headless dev loom in a `dev-driver` tmux session. (3) Thin binaries — `tools/loomdev` (CLI) and `tools/fakeagent` (deterministic agent stand-in) — plus an e2e suite, a project skill, and docs.

**Tech Stack:** Go 1.23 module `github.com/aidan-bailey/loom` (vendored deps: cobra, testify), tmux, git, Nix flake.

**Spec:** `docs/superpowers/specs/2026-09-16-dev-sandbox-design.md`

## Global Constraints

- Module path `github.com/aidan-bailey/loom`; deps are vendored (`vendor/`) — add no new third-party dependencies (stdlib, cobra, testify only).
- Builds and tests run with `CGO_ENABLED=0` (no C compiler locally). Local lint substitute: `CGO_ENABLED=0 go vet ./...` and `gofmt -l .` (the local golangci-lint is v2 and cannot read the repo's v1 config; CI runs it).
- CI's revive `exported` rule applies outside `internal/` and tests: every new exported identifier needs a doc comment starting with its name.
- Error wrapping: `fmt.Errorf("context: %w", err)`. Tests use `testify` (`assert`/`require`).
- Env var names, verbatim: `LOOM_TMUX_SOCKET`, `LOOM_GLOBAL_DIR`, `LOOM_ALLOW_NESTED` (bypass value `1`), existing `LOOM_HOME`.
- Sandbox names match `^[a-z0-9][a-z0-9._-]{0,62}$`; socket is `loomdev-<name>`; driver session is `dev-driver`; workspace name is `toy`; sandbox root is `${XDG_STATE_HOME:-~/.local/state}/loom-dev/<name>/`.
- **Never** run `./loom`, `go run .`, `loom reset`, `clean.sh`, or `clean_hard.sh` directly while working inside a loom pane — until Task 3 lands, that kills the host's sessions. Every verification step below uses a safe probe.
- Commit messages: Conventional Commits, ending with the trailer lines:
  ```
  Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB
  ```
  (Shown once here; every commit step below uses them.)

## Deviations from the spec (discovered while planning; Task 9 amends the spec)

1. **Nix:** use `excludedPackages = [ "tools" ]`, not `subPackages = [ "." ]` — `buildGoModule` uses the same directory list for `checkPhase`, so `subPackages` would silently stop Nix from running every other package's tests.
2. **Config roots:** `config.GlobalWorkspaceContext().ConfigDir` comes from `GetGlobalConfigDir()` (not `LOOM_HOME`), and `--workspace toy` reads `<repo>/.loom/`. So sandbox `config.json`/`state.json` are seeded into `global/` **and** `repo/.loom/`; `home/` (`LOOM_HOME`) only receives startup logs. `default_program` is the profile *name* (`fake`), because a non-empty `profiles` list makes `DefaultProgram` a profile name. `branch_prefix` is `dev/`. `state.json` is seeded with every help screen marked seen so first-run overlays never block the headless driver.
3. **Registration:** `Up` writes `global/workspaces.json` directly through `config.WorkspaceRegistry` (a test reads it back via `config.LoadWorkspaceRegistry`, guarding drift) instead of shelling out to `loom workspace add`, so `Up` needs no built binary and stays fast to test.
4. **CLI shape:** the sandbox is selected with a persistent `--sandbox/-s` flag (default: branch leaf) instead of positional names; `keys -l` sends literal text; `up` also builds and takes `--default-profile`; `run`/`start` take `--no-build`.
5. **fakeagent:** adds `trust` and `exit` commands; persona prompt texts are read from loom's own adapter registry; answering a prompt clears the screen so the pattern stops matching.
6. **Driver:** `status off` is set server-wide on the private socket *before* `new-session` (so the pane starts at exactly the requested size; loom already turns it off on its own sessions), and window-level `remain-on-exit on` is set on the driver window only.

## File Structure

| File | Responsibility |
|---|---|
| `session/tmux/command.go` (new) | `EnvTmuxSocket`, `Socket`, `Command`, `CommandOnSocket`, `EnclosingSessionName` — the only file allowed to exec `"tmux"` literally |
| `session/tmux/command_test.go`, `command_enforce_test.go` (new) | builder argv tests; AST walk forbidding raw tmux execs elsewhere |
| `session/tmux/nesting.go`, `nesting_test.go` (new) | `EnvAllowNested`, `NestedError`, `CheckNesting`, `CheckNestingFromEnv` |
| `session/tmux/tmux.go`, `session/reconcile.go` (modify) | migrate 22 tmux execs to the builder |
| `session/reconcile_socket_test.go` (new) | real-tmux proof the sweep spares the `$TMUX` server |
| `config/config.go`, `config/workspace.go`, `config/migration.go` (modify) | `resolveEnvDir` helper, `EnvGlobalDir`, migration skip |
| `main.go`, `main_test.go` (modify) | guard wiring for root + `reset`; `debug` output |
| `clean.sh`, `clean_hard.sh` (modify) | refuse inside a loom pane |
| `tools/fakeagent/{main,agent}.go` + test (new) | deterministic agent stand-in |
| `flake.nix` (modify) | `excludedPackages = [ "tools" ]` |
| `internal/devsandbox/sandbox.go` (new) | names, paths, env, meta, `Down`, `List`, `TailLogs` |
| `internal/devsandbox/up.go` (new) | `Up`, `Build`, config/state seeding, registry write |
| `internal/devsandbox/toyrepo.go` (new) | toy repo + bare origin |
| `internal/devsandbox/driver.go` (new) | `Start`, `Stop`, `SendKeys`, `SendText`, `Screen`, `WaitFor` |
| `internal/devsandbox/*_test.go` (new) | unit + real-tmux/git tests |
| `tools/loomdev/{main,cmds,logs}.go` + test (new) | cobra CLI |
| `e2e/e2e_test.go` (new, `//go:build e2e`) | three smoke tests |
| `.claude/skills/loom-dev/SKILL.md` (new) | agent recipe |
| `CLAUDE.md`, `USAGE.md`, `CONTRIBUTING.md`, spec (modify) | docs |

---

### Task 1: Single tmux command builder

**Files:**
- Create: `session/tmux/command.go`, `session/tmux/command_test.go`, `session/tmux/command_enforce_test.go`, `session/reconcile_socket_test.go`
- Modify: `session/tmux/tmux.go` (18 exec sites: lines ~204, 276, 283, 318, 326, 337, 348, 472, 811, 877, 909, 950, 1049, 1066, 1081, 1133, 1217, 1238), `session/reconcile.go` (4 sites: ~40, 52, 205, 241), `session/tmux/tmux_test.go` (TestMain), `session/session_test.go` (TestMain)

**Interfaces:**
- Consumes: nothing new.
- Produces (package `tmux`):
  - `const EnvTmuxSocket = "LOOM_TMUX_SOCKET"`
  - `func Socket() string`
  - `func Command(ctx context.Context, args ...string) *exec.Cmd`
  - `func CommandOnSocket(ctx context.Context, socket string, args ...string) *exec.Cmd`

- [ ] **Step 1: Write the failing builder tests**

Create `session/tmux/command_test.go`:

```go
package tmux

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommand_DefaultServerLeavesArgvUnchanged(t *testing.T) {
	t.Setenv(EnvTmuxSocket, "")
	cmd := Command(context.Background(), "has-session", "-t=loom_x")
	assert.Equal(t, []string{"tmux", "has-session", "-t=loom_x"}, cmd.Args)
}

func TestCommand_PrivateSocketPrependsL(t *testing.T) {
	t.Setenv(EnvTmuxSocket, "loomdev-demo")
	cmd := Command(context.Background(), "ls")
	assert.Equal(t, []string{"tmux", "-L", "loomdev-demo", "ls"}, cmd.Args)
}

func TestCommandOnSocket_IgnoresEnv(t *testing.T) {
	t.Setenv(EnvTmuxSocket, "from-env")
	cmd := CommandOnSocket(context.Background(), "explicit", "ls")
	assert.Equal(t, []string{"tmux", "-L", "explicit", "ls"}, cmd.Args)
}

func TestCommandOnSocket_DoesNotMutateCallerArgs(t *testing.T) {
	args := make([]string, 1, 8) // spare capacity would expose an in-place append
	args[0] = "ls"
	_ = CommandOnSocket(context.Background(), "s", args...)
	assert.Equal(t, []string{"ls"}, args)
}

func TestCommand_BoundToContext(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, Command(ctx, "ls").Run(), context.Canceled)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./session/tmux -run 'TestCommand' -v`
Expected: FAIL to compile — `undefined: Command`, `undefined: EnvTmuxSocket`, `undefined: CommandOnSocket`.

- [ ] **Step 3: Implement the builder**

Create `session/tmux/command.go`:

```go
package tmux

import (
	"context"
	"os"
	"os/exec"
)

// EnvTmuxSocket names the environment variable that points every loom tmux
// invocation at a private server via `tmux -L <name>`. Unset (or empty)
// keeps tmux's own server selection: $TMUX when running inside tmux, else
// the default socket.
const EnvTmuxSocket = "LOOM_TMUX_SOCKET"

// Socket returns the private tmux socket name from LOOM_TMUX_SOCKET, or ""
// for the default server. It is read on every call so tests can t.Setenv it.
func Socket() string { return os.Getenv(EnvTmuxSocket) }

// Command builds a tmux invocation bound to ctx on the server selected by
// LOOM_TMUX_SOCKET. Every tmux exec in loom goes through Command or
// CommandOnSocket (TestNoRawTmuxExec enforces it): an explicit -L outranks
// $TMUX, which is what keeps a dev build started inside a loom pane from
// sweeping the host's sessions.
func Command(ctx context.Context, args ...string) *exec.Cmd {
	return CommandOnSocket(ctx, Socket(), args...)
}

// CommandOnSocket is Command with an explicit socket name; "" selects the
// default server. The caller's args slice is never modified.
func CommandOnSocket(ctx context.Context, socket string, args ...string) *exec.Cmd {
	full := make([]string, 0, len(args)+2)
	if socket != "" {
		full = append(full, "-L", socket)
	}
	full = append(full, args...)
	return exec.CommandContext(ctx, "tmux", full...)
}
```

- [ ] **Step 4: Run builder tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session/tmux -run 'TestCommand' -v`
Expected: PASS (the context test skips only if tmux is absent).

- [ ] **Step 5: Write the failing sweep-isolation test**

This test reproduces the real hazard safely: it creates its *own* "host" server, points `$TMUX` at it (as a terminal pane would), and sets `LOOM_TMUX_SOCKET` to a second private server. Before migration the sweep ignores the socket, follows `$TMUX`, and kills the decoy — on the test's own throwaway server, never the real host.

Create `session/reconcile_socket_test.go`:

```go
package session

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCleanupOrphanedSessions_PrivateSocketSparesEnclosingServer pins the
// dev-sandbox safety property: with LOOM_TMUX_SOCKET set, the orphan sweep
// only touches that server — even when $TMUX names another server holding
// unclaimed loom_* sessions (a dev build started from a loom pane).
func TestCleanupOrphanedSessions_PrivateSocketSparesEnclosingServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	host, private := "loomtest-host-"+suffix, "loomtest-priv-"+suffix
	for _, sock := range []string{host, private} {
		sock := sock
		t.Cleanup(func() { _ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run() })
	}
	require.NoError(t, tmux.CommandOnSocket(ctx, host, "new-session", "-d", "-s", "loom_decoy", "sleep 120").Run())
	require.NoError(t, tmux.CommandOnSocket(ctx, private, "new-session", "-d", "-s", "loom_stray", "sleep 120").Run())

	out, err := tmux.CommandOnSocket(ctx, host, "display-message", "-p", "-t", "loom_decoy", "#{socket_path}").Output()
	require.NoError(t, err)
	// Pretend this process runs inside the host server, exactly like a pane.
	t.Setenv("TMUX", strings.TrimSpace(string(out))+",1,0")
	t.Setenv(tmux.EnvTmuxSocket, private)

	require.NoError(t, CleanupOrphanedSessions(map[string]bool{}, internalexec.Default{}))

	assert.NoError(t, tmux.CommandOnSocket(ctx, host, "has-session", "-t=loom_decoy").Run(),
		"the enclosing server's loom_* session must survive a sweep aimed at the private socket")
	assert.Error(t, tmux.CommandOnSocket(ctx, private, "has-session", "-t=loom_stray").Run(),
		"the sweep must still clean unclaimed sessions on its own server")
}
```

- [ ] **Step 6: Run to verify it fails for the right reason**

Run: `CGO_ENABLED=0 go test ./session -run TestCleanupOrphanedSessions_PrivateSocketSparesEnclosingServer -v`
Expected: FAIL — both assertions: the decoy was killed (sweep followed `$TMUX`) and `loom_stray` survived. (If tmux is absent it SKIPs; install tmux before continuing.)

- [ ] **Step 7: Write the failing enforcement test**

Create `session/tmux/command_enforce_test.go`:

```go
package tmux

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoRawTmuxExec fails on any exec.Command/exec.CommandContext whose
// program is the literal "tmux" outside command.go. Raw execs would ignore
// LOOM_TMUX_SOCKET and follow $TMUX to the host server.
func TestNoRawTmuxExec(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))
	allowed := filepath.Join(root, "session", "tmux", "command.go")

	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// vendor/ holds third-party code; dot-dirs include .git and
			// .loom (whose worktrees are other checkouts of this repo).
			if path != root && (d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == allowed {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "exec" {
				return true
			}
			progArg := map[string]int{"Command": 0, "CommandContext": 1}
			idx, ok := progArg[sel.Sel.Name]
			if !ok || len(call.Args) <= idx {
				return true
			}
			if lit, ok := call.Args[idx].(*ast.BasicLit); ok && lit.Value == `"tmux"` {
				offenders = append(offenders, fset.Position(call.Pos()).String())
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, offenders, "invoke tmux via tmux.Command / tmux.CommandOnSocket (session/tmux/command.go)")
}
```

- [ ] **Step 8: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/tmux -run TestNoRawTmuxExec -v`
Expected: FAIL listing 22 offenders — 18 in `session/tmux/tmux.go`, 4 in `session/reconcile.go`.

- [ ] **Step 9: Migrate every call site**

```bash
sed -i -E 's/exec\.CommandContext\(([A-Za-z]+), "tmux", /Command(\1, /' session/tmux/tmux.go
sed -i -E 's/exec\.Command\("tmux", /Command(context.Background(), /' session/tmux/tmux.go
sed -i -E 's/exec\.CommandContext\(([A-Za-z]+), "tmux", /tmux.Command(\1, /' session/reconcile.go
grep -n '"tmux"' session/tmux/tmux.go session/reconcile.go | grep -v 'log.For'
grep -n 'exec\.' session/reconcile.go
```

Expected: the first grep prints nothing. If the second grep prints nothing, delete the `"os/exec"` line from `session/reconcile.go`'s import block. Spot-check two rewrites:
- `session/tmux/tmux.go` start path now reads `cmd := Command(startCtx, args...)`.
- `FullScreenAttachCmd` now reads `return Command(context.Background(), "attach-session", "-t", t.sanitizedName)`.

(`context.Background()` never fires, so the long-lived attach clients keep their existing lifetime.)

- [ ] **Step 10: Pin the default server in argv-exact test packages**

In `session/tmux/tmux_test.go`, change `TestMain` to:

```go
func TestMain(m *testing.M) {
	_ = log.Initialize("", false)
	defer log.Close()
	// Argv-exact mock assertions assume the default server; a shell that
	// exported LOOM_TMUX_SOCKET (e.g. via `loomdev env`) would prepend -L.
	os.Unsetenv(EnvTmuxSocket)
	os.Exit(m.Run())
}
```

In `session/session_test.go`, make it:

```go
package session

import (
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = log.Initialize("", false)
	defer log.Close()
	// Argv-exact mock assertions (reconcile_test.go) assume the default server.
	os.Unsetenv(tmux.EnvTmuxSocket)
	os.Exit(m.Run())
}
```

- [ ] **Step 11: Build and run the affected packages**

Run: `gofmt -l session && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./session/... ./app/... 2>&1 | tail -20`
Expected: no gofmt output; build OK; all PASS, including `TestNoRawTmuxExec` and `TestCleanupOrphanedSessions_PrivateSocketSparesEnclosingServer`. (`TestScrollbackAccumulation_RealTmux` in `session/vt` is a known timing flake under a full run — re-run it alone if it fails.)

- [ ] **Step 12: Commit**

```bash
git add session/tmux/command.go session/tmux/command_test.go session/tmux/command_enforce_test.go \
  session/tmux/tmux.go session/tmux/tmux_test.go session/reconcile.go session/reconcile_socket_test.go session/session_test.go
git commit -m "feat(tmux): route every tmux exec through a socket-aware builder

LOOM_TMUX_SOCKET prepends -L so loom targets a private server even when
\$TMUX names the host's. An AST test forbids raw tmux execs elsewhere.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB"
```

---

### Task 2: `LOOM_GLOBAL_DIR`

**Files:**
- Modify: `config/config.go` (`GetConfigDir`, new `resolveEnvDir`), `config/workspace.go` (`GetGlobalConfigDir`, new `EnvGlobalDir`), `config/migration.go` (`MigrateLegacyHome`)
- Test: `config/workspace_test.go`, `config/migration_test.go`, `config/config_test.go` (TestMain)

**Interfaces:**
- Consumes: nothing.
- Produces (package `config`): `const EnvGlobalDir = "LOOM_GLOBAL_DIR"`; `GetGlobalConfigDir()` honors it; unexported `resolveEnvDir(name, dir string) (string, error)`.

- [ ] **Step 1: Isolate the config test package from the new variable**

In `config/config_test.go` `TestMain`, after `os.Unsetenv("CLAUDE_SQUAD_HOME")` add:

```go
	os.Unsetenv("LOOM_GLOBAL_DIR")
```

In `config/migration_test.go` `withTempHome`, after `t.Setenv("CLAUDE_SQUAD_HOME", "")` add:

```go
	t.Setenv("LOOM_GLOBAL_DIR", "")
```

(These use the string literal because `EnvGlobalDir` does not exist yet.)

- [ ] **Step 2: Write the failing tests**

Append to `config/workspace_test.go`:

```go
func TestGetGlobalConfigDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvGlobalDir, dir)
	got, err := GetGlobalConfigDir()
	require.NoError(t, err)
	assert.Equal(t, dir, got)
}

func TestGetGlobalConfigDir_EnvTildeExpands(t *testing.T) {
	home := withTempHome(t)
	t.Setenv(EnvGlobalDir, "~/sandbox-global")
	got, err := GetGlobalConfigDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "sandbox-global"), got)
}

func TestGetGlobalConfigDir_EnvRelativeRejected(t *testing.T) {
	t.Setenv(EnvGlobalDir, "relative/dir")
	_, err := GetGlobalConfigDir()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvGlobalDir)
}

func TestWorkspaceRegistry_FollowsGlobalDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvGlobalDir, dir)
	require.NoError(t, SaveWorkspaceRegistry(&WorkspaceRegistry{LastUsed: "sandbox"}))
	assert.FileExists(t, filepath.Join(dir, "workspaces.json"))
	reg, err := LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, "sandbox", reg.LastUsed)
}
```

Append to `config/migration_test.go`:

```go
func TestMigrateLegacyHome_SkipsWhenGlobalDirSet(t *testing.T) {
	home := withTempHome(t)
	legacy := filepath.Join(home, ".claude-squad")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	t.Setenv(EnvGlobalDir, t.TempDir())

	stderr := captureStderr(t, func() {
		require.NoError(t, MigrateLegacyHome())
	})

	assert.Empty(t, strings.TrimSpace(stderr))
	assert.DirExists(t, legacy, "legacy dir must survive when LOOM_GLOBAL_DIR is set")
	assert.NoDirExists(t, filepath.Join(home, ".loom"))
}
```

- [ ] **Step 3: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./config -run 'GlobalDir|GlobalConfigDir' -v`
Expected: FAIL to compile — `undefined: EnvGlobalDir`.

- [ ] **Step 4: Extract `resolveEnvDir` in `config/config.go`**

Replace the body of `GetConfigDir` and add the helper below it:

```go
func GetConfigDir() (string, error) {
	if envDir := log.GetEnvWithLegacy(EnvHome, legacyEnvHome); envDir != "" {
		return resolveEnvDir(EnvHome, envDir)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config home directory: %w", err)
	}
	return filepath.Join(homeDir, ".loom"), nil
}

// resolveEnvDir expands a leading ~ in dir (the value of env var name) and
// requires the result to be absolute.
func resolveEnvDir(name, dir string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to expand ~ in %s: %w", name, err)
		}
		dir = filepath.Join(homeDir, dir[1:])
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("%s must be an absolute path, got: %s", name, dir)
	}
	return dir, nil
}
```

- [ ] **Step 5: Add `EnvGlobalDir` in `config/workspace.go`**

Replace the existing `GetGlobalConfigDir` (and its comment) with:

```go
// EnvGlobalDir overrides the directory GetGlobalConfigDir returns — the home
// of workspaces.json and of the global workspace context. Deliberately
// separate from LOOM_HOME, which GetGlobalConfigDir ignores by design; the
// dev sandbox (internal/devsandbox) sets both.
const EnvGlobalDir = "LOOM_GLOBAL_DIR"

// GetGlobalConfigDir returns $LOOM_GLOBAL_DIR when set, otherwise ~/.loom/ —
// regardless of LOOM_HOME.
func GetGlobalConfigDir() (string, error) {
	if dir := os.Getenv(EnvGlobalDir); dir != "" {
		return resolveEnvDir(EnvGlobalDir, dir)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".loom"), nil
}
```

- [ ] **Step 6: Skip migration under the override in `config/migration.go`**

Change the guard at the top of `MigrateLegacyHome` to:

```go
	if os.Getenv(EnvHome) != "" || os.Getenv(legacyEnvHome) != "" || os.Getenv(EnvGlobalDir) != "" {
		return nil
	}
```

and extend the doc comment paragraph that begins "When LOOM_HOME or CLAUDE_SQUAD_HOME is set" to read "When LOOM_HOME, CLAUDE_SQUAD_HOME, or LOOM_GLOBAL_DIR is set".

- [ ] **Step 7: Run the config package**

Run: `gofmt -l config && CGO_ENABLED=0 go test ./config -v 2>&1 | tail -15`
Expected: all PASS (existing `TestGetConfigDir*` tests guard the refactor).

- [ ] **Step 8: Commit**

```bash
git add config/
git commit -m "feat(config): LOOM_GLOBAL_DIR relocates the workspace registry

Lets a dev sandbox isolate workspaces.json without swapping HOME.
Legacy-home migration is skipped when it is set.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB"
```

---

### Task 3: Nesting guard, `loom debug` output, cleanup-script guards

**Files:**
- Create: `session/tmux/nesting.go`, `session/tmux/nesting_test.go`
- Modify: `session/tmux/command.go` (add `EnclosingSessionName`), `session/tmux/command_test.go`, `main.go`, `main_test.go`, `clean.sh`, `clean_hard.sh`

**Interfaces:**
- Consumes: `tmux.EnvTmuxSocket`, `tmux.CommandOnSocket`, `tmux.Socket` (Task 1); `config.EnvGlobalDir`, `config.GetGlobalConfigDir` (Task 2); existing `tmux.TmuxPrefix`, `tmux.LegacyTmuxPrefix`, `tmuxTimeout`.
- Produces (package `tmux`):
  - `const EnvAllowNested = "LOOM_ALLOW_NESTED"`
  - `type NestedError struct{ Session string }` with `Error() string`
  - `func CheckNesting(getenv func(string) string, enclosing func() (string, error)) error`
  - `func CheckNestingFromEnv() error`
  - `func EnclosingSessionName() (string, error)`
- Produces (package `main`): `var nestingCheck = tmux.CheckNestingFromEnv`

- [ ] **Step 1: Write the failing guard tests**

Create `session/tmux/nesting_test.go`:

```go
package tmux

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckNesting(t *testing.T) {
	const tmuxEnv = "/tmp/tmux-1000/default,123,0"
	lookup := func(name string, err error) func() (string, error) {
		return func() (string, error) { return name, err }
	}
	mustNotLookup := func() (string, error) {
		t.Fatal("enclosing-session lookup must not run")
		return "", nil
	}
	tests := []struct {
		name        string
		env         map[string]string
		enclosing   func() (string, error)
		wantSession string // "" means no error
	}{
		{"not inside tmux", map[string]string{}, mustNotLookup, ""},
		{"inside a loom agent session", map[string]string{"TMUX": tmuxEnv}, lookup("loom_feature", nil), "loom_feature"},
		{"inside a loom terminal pane", map[string]string{"TMUX": tmuxEnv}, lookup("loom_term_feature", nil), "loom_term_feature"},
		{"inside a legacy session", map[string]string{"TMUX": tmuxEnv}, lookup("claudesquad_old", nil), "claudesquad_old"},
		{"inside unrelated tmux", map[string]string{"TMUX": tmuxEnv}, lookup("work", nil), ""},
		{"lookup fails", map[string]string{"TMUX": tmuxEnv}, lookup("", errors.New("no server")), ""},
		{"private socket configured", map[string]string{"TMUX": tmuxEnv, EnvTmuxSocket: "loomdev-x"}, mustNotLookup, ""},
		{"explicit override", map[string]string{"TMUX": tmuxEnv, EnvAllowNested: "1"}, mustNotLookup, ""},
		{"override needs the value 1", map[string]string{"TMUX": tmuxEnv, EnvAllowNested: "yes"}, lookup("loom_feature", nil), "loom_feature"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			err := CheckNesting(getenv, tc.enclosing)
			if tc.wantSession == "" {
				assert.NoError(t, err)
				return
			}
			var nested *NestedError
			require.ErrorAs(t, err, &nested)
			assert.Equal(t, tc.wantSession, nested.Session)
			assert.Contains(t, err.Error(), tc.wantSession)
			assert.Contains(t, err.Error(), "go run ./tools/loomdev run")
			assert.Contains(t, err.Error(), EnvTmuxSocket)
		})
	}
}
```

Append to `session/tmux/command_test.go` (add `"fmt"`, `"strings"`, `"time"` and `"github.com/stretchr/testify/require"` to its imports):

```go
func TestEnclosingSessionName_AsksTheTmuxEnvServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	sock := fmt.Sprintf("loomtest-encl-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = CommandOnSocket(ctx, sock, "kill-server").Run() })
	require.NoError(t, CommandOnSocket(ctx, sock, "new-session", "-d", "-s", "loom_term_probe", "sleep 60").Run())
	path, err := CommandOnSocket(ctx, sock, "display-message", "-p", "-t", "loom_term_probe", "#{socket_path}").Output()
	require.NoError(t, err)
	pane, err := CommandOnSocket(ctx, sock, "display-message", "-p", "-t", "loom_term_probe", "#{pane_id}").Output()
	require.NoError(t, err)

	t.Setenv("TMUX", strings.TrimSpace(string(path))+",1,0")
	t.Setenv("TMUX_PANE", strings.TrimSpace(string(pane)))
	t.Setenv(EnvTmuxSocket, "ignored-by-design") // the question is about the enclosing server

	name, err := EnclosingSessionName()
	require.NoError(t, err)
	assert.Equal(t, "loom_term_probe", name)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./session/tmux -run 'TestCheckNesting|TestEnclosingSessionName' -v`
Expected: FAIL to compile — `undefined: CheckNesting`, `NestedError`, `EnvAllowNested`, `EnclosingSessionName`.

- [ ] **Step 3: Implement `EnclosingSessionName`**

Append to `session/tmux/command.go` (add `"fmt"` and `"strings"` to its imports):

```go
// EnclosingSessionName returns the name of the tmux session this process
// runs inside, asking the server named by $TMUX (pinned to $TMUX_PANE when
// set). It deliberately ignores LOOM_TMUX_SOCKET — the question is about the
// enclosing server, not the one loom would manage — which is why it lives in
// the one file allowed to exec tmux directly.
func EnclosingSessionName() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	args := []string{"display-message", "-p"}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args, "-t", pane)
	}
	args = append(args, "#S")
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
```

- [ ] **Step 4: Implement the guard**

Create `session/tmux/nesting.go`:

```go
package tmux

import (
	"fmt"
	"os"
	"strings"
)

// EnvAllowNested bypasses CheckNesting when set to "1".
const EnvAllowNested = "LOOM_ALLOW_NESTED"

// NestedError reports that loom was started inside one of its own tmux
// sessions while targeting that same (default) server.
type NestedError struct {
	// Session is the enclosing loom-managed tmux session.
	Session string
}

// Error implements error.
func (e *NestedError) Error() string {
	return fmt.Sprintf("loom: refusing to start inside a loom-managed tmux session (%s) — its orphan sweep would kill the host's sessions. Use `go run ./tools/loomdev run` or set %s.",
		e.Session, EnvTmuxSocket)
}

// CheckNesting returns a *NestedError when the process runs inside a
// loom-managed tmux session (loom_* or claudesquad_*) and no private socket
// is configured. A failing lookup means "not nested": with no reachable
// enclosing server there is nothing for the sweep to harm.
func CheckNesting(getenv func(string) string, enclosing func() (string, error)) error {
	if getenv(EnvAllowNested) == "1" || getenv("TMUX") == "" || getenv(EnvTmuxSocket) != "" {
		return nil
	}
	name, err := enclosing()
	if err != nil {
		return nil
	}
	if strings.HasPrefix(name, TmuxPrefix) || strings.HasPrefix(name, LegacyTmuxPrefix) {
		return &NestedError{Session: name}
	}
	return nil
}

// CheckNestingFromEnv is CheckNesting wired to the process environment and
// the real enclosing-session lookup.
func CheckNestingFromEnv() error {
	return CheckNesting(os.Getenv, EnclosingSessionName)
}
```

- [ ] **Step 5: Run to verify the tmux package passes**

Run: `CGO_ENABLED=0 go test ./session/tmux -run 'TestCheckNesting|TestEnclosingSessionName|TestNoRawTmuxExec' -v`
Expected: PASS.

- [ ] **Step 6: Write the failing `main` tests**

Each test isolates every loom path and the tmux socket, so a missing guard fails harmlessly instead of touching real state. The root-command test passes a nonexistent `--workspace`: unguarded, it errors with "not found" before `app.Run`.

Append to `main_test.go` (add imports `"fmt"`, `"io"`, `"os"`, `"time"`, `"github.com/aidan-bailey/loom/config"`, `"github.com/aidan-bailey/loom/session/tmux"`):

```go
// isolateLoomEnv points every loom path and the tmux socket at throwaway
// locations so a missing guard cannot touch real state.
func isolateLoomEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	t.Setenv(tmux.EnvTmuxSocket, fmt.Sprintf("loomtest-none-%d", time.Now().UnixNano()))
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
}

func stubNesting(t *testing.T, err error) {
	t.Helper()
	orig := nestingCheck
	nestingCheck = func() error { return err }
	t.Cleanup(func() { nestingCheck = orig })
}

func TestResetCmd_RefusesWhenNested(t *testing.T) {
	isolateLoomEnv(t)
	stubNesting(t, &tmux.NestedError{Session: "loom_term_x"})
	t.Cleanup(func() { resetForceFlag = false })

	rootCmd.SetArgs([]string{"reset", "--force"})
	err := rootCmd.Execute()

	var nested *tmux.NestedError
	require.ErrorAs(t, err, &nested)
	assert.Equal(t, "loom_term_x", nested.Session)
}

func TestRootCmd_RefusesWhenNested(t *testing.T) {
	isolateLoomEnv(t)
	stubNesting(t, &tmux.NestedError{Session: "loom_agent"})
	t.Cleanup(func() { workspaceFlag = "" })

	rootCmd.SetArgs([]string{"--workspace", "__loomtest_missing__"})
	err := rootCmd.Execute()

	var nested *tmux.NestedError
	require.ErrorAs(t, err, &nested, "the guard must run before workspace resolution")
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	require.NoError(t, w.Close())
	os.Stdout = orig
	return <-done
}

func TestDebugCmd_ReportsIsolationKnobs(t *testing.T) {
	isolateLoomEnv(t)
	t.Setenv(tmux.EnvTmuxSocket, "loomdev-probe")
	stubNesting(t, nil)

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"debug"})
		require.NoError(t, rootCmd.Execute())
	})

	assert.Contains(t, out, "Tmux socket: loomdev-probe")
	assert.Contains(t, out, "Global dir: "+os.Getenv(config.EnvGlobalDir))
	assert.Contains(t, out, "Nesting guard: ok")
}
```

- [ ] **Step 7: Run to verify failure**

Run: `CGO_ENABLED=0 go test . -run 'Nested|DebugCmd' -v`
Expected: FAIL to compile — `undefined: nestingCheck`.

- [ ] **Step 8: Wire the guard into `main.go`**

Add below the `var (...)` block's closing paren (before `resolveResetWorkspace`):

```go
// nestingCheck guards the commands whose startup sweeps tmux sessions (the
// TUI and reset). A package var so tests can stub the environment probe.
var nestingCheck = tmux.CheckNestingFromEnv
```

Make the first statement of `rootCmd`'s `RunE`:

```go
			if err := nestingCheck(); err != nil {
				return err
			}
```

Make the first statement of `resetCmd`'s `RunE` (before the `--force` check):

```go
			if err := nestingCheck(); err != nil {
				return err
			}
```

In `debugCmd`'s `RunE`, immediately before its final `return nil`, add:

```go
			socket := tmux.Socket()
			if socket == "" {
				socket = "default (" + tmux.EnvTmuxSocket + " unset)"
			}
			fmt.Printf("Tmux socket: %s\n", socket)
			if globalDir, err := config.GetGlobalConfigDir(); err != nil {
				fmt.Printf("Global dir: error: %v\n", err)
			} else {
				fmt.Printf("Global dir: %s (env %s)\n", globalDir, config.EnvGlobalDir)
			}
			if err := nestingCheck(); err != nil {
				fmt.Printf("Nesting guard: would refuse — %v\n", err)
			} else {
				fmt.Println("Nesting guard: ok")
			}
```

- [ ] **Step 9: Run the `main` tests**

Run: `gofmt -l . | grep -v '^vendor/'; CGO_ENABLED=0 go test . -v 2>&1 | tail -15`
Expected: no gofmt output; all PASS.

- [ ] **Step 10: Live-probe the guard safely**

You are (probably) inside a loom pane right now. The probes below are harmless whether or not the guard works: the unguarded paths error out before touching tmux, and `LOOM_HOME`/`LOOM_GLOBAL_DIR` point at a throwaway dir so even log initialization (which can rotate `loom.log`) stays off the host's files. Neither variable affects the guard.

```bash
D=$(mktemp -d) && CGO_ENABLED=0 go build -o "$D/loom" .
P() { LOOM_HOME="$D/home" LOOM_GLOBAL_DIR="$D/global" "$D/loom" "$@"; }
P --workspace __loomtest_missing__; echo "exit=$?"
P reset; echo "exit=$?"
P debug | tail -3
```

Expected when `$TMUX` is set and `tmux display-message -p '#S'` starts with `loom_`: both commands print `loom: refusing to start inside a loom-managed tmux session (…)` and exit 1; `debug` ends with `Nesting guard: would refuse — …`. Outside loom: the first prints `workspace "__loomtest_missing__" not found`, the second the `--force` message, and debug shows `Nesting guard: ok`. Never run the binary without those arguments here.

- [ ] **Step 11: Guard the cleanup scripts**

Replace `clean.sh` with:

```bash
#!/usr/bin/env bash
# Wipes ALL loom state: every tmux session on the server and ~/.loom.
# Refuses inside a loom-managed tmux session, where that would destroy the
# loom instance hosting this shell. For dev sandboxes use
# `go run ./tools/loomdev down` instead.
if [ -n "${TMUX:-}" ]; then
  current="$(tmux display-message -p ${TMUX_PANE:+-t "$TMUX_PANE"} '#S' 2>/dev/null || true)"
  case "$current" in
    loom_*|claudesquad_*)
      echo "clean.sh: refusing to run inside loom-managed tmux session '$current'" >&2
      exit 1
      ;;
  esac
fi

tmux kill-server
rm -rf worktree*
rm -rf ~/.loom
rm -rf ~/.claude-squad
```

Replace `clean_hard.sh` with:

```bash
#!/usr/bin/env bash
# clean.sh (including its loom-pane guard) plus `git worktree prune`.
bash "$(dirname "$0")/clean.sh" || exit 1
git worktree prune
```

- [ ] **Step 12: Verify the script guard in a sandboxed environment**

`HOME`, `PATH` (fake `tmux`) and the working directory are all throwaway, so even a broken guard deletes nothing real:

```bash
REPO=$(pwd); T=$(mktemp -d); mkdir -p "$T/bin" "$T/home/.loom"
printf '#!/bin/sh\n[ "$1" = display-message ] && echo loom_term_fake\nexit 0\n' > "$T/bin/tmux"; chmod +x "$T/bin/tmux"
(cd "$T" && HOME="$T/home" TMUX=/fake,1,0 PATH="$T/bin:$PATH" bash "$REPO/clean.sh"); echo "exit=$?"
test -d "$T/home/.loom" && echo "guard held"
(cd "$T" && HOME="$T/home" TMUX=/fake,1,0 PATH="$T/bin:$PATH" bash "$REPO/clean_hard.sh"); echo "exit=$?"
(cd "$T" && HOME="$T/home" PATH="$T/bin:$PATH" env -u TMUX bash "$REPO/clean.sh"); echo "exit=$?"
test -d "$T/home/.loom" || echo "outside tmux the script still cleans"
```

Expected: refusal message + `exit=1` + `guard held`; `clean_hard.sh` also refuses with `exit=1`; the last run prints `exit=0` and `outside tmux the script still cleans`.

- [ ] **Step 13: Commit**

```bash
git add session/tmux/nesting.go session/tmux/nesting_test.go session/tmux/command.go session/tmux/command_test.go \
  main.go main_test.go clean.sh clean_hard.sh
git commit -m "feat: refuse to start loom inside its own tmux session

The TUI and reset now bail out when run from a loom_* session on the
default server (LOOM_ALLOW_NESTED=1 overrides). loom debug reports the
socket, global dir and guard state; clean scripts refuse likewise.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB"
```

---

### Task 4: `tools/fakeagent`

**Files:**
- Create: `tools/fakeagent/main.go`, `tools/fakeagent/agent.go`, `tools/fakeagent/agent_test.go`
- Modify: `flake.nix`

**Interfaces:**
- Consumes: `agent.DefaultRegistry() *agent.Registry`, `(*Registry).Lookup(program string) agent.Adapter`, `Adapter.Name/PendingPromptPattern/TrustPromptPatterns` (existing, `session/agent`).
- Produces: a `main` package built by the sandbox as `bin/fakeagent` (Task 5 builds `./tools/fakeagent`). Stdin commands: `work N`, `ask`, `trust`, `bell`, `title TEXT`, `edit`, `commit`, `crash`, `exit`.

- [ ] **Step 1: Write the failing tests**

Create `tools/fakeagent/agent_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runScript(t *testing.T, argv0, script string) (string, int, *fakeAgent) {
	t.Helper()
	var out bytes.Buffer
	a := newFakeAgent(personaFor(argv0), strings.NewReader(script), &out, t.TempDir())
	a.sleep = func(time.Duration) {}
	code := a.run()
	return out.String(), code, a
}

func TestPersonaFor_MatchesLoomAdapters(t *testing.T) {
	reg := agent.DefaultRegistry()
	for _, argv0 := range []string{"/sbx/bin/personas/claude", "/sbx/bin/personas/aider"} {
		p := personaFor(argv0)
		ad := reg.Lookup(argv0)
		assert.Equal(t, ad.Name(), p.name, argv0)
		assert.Equal(t, ad.PendingPromptPattern(), p.pendingPrompt, argv0)
		assert.Equal(t, ad.TrustPromptPatterns()[0], p.trustPrompt, argv0)
	}
}

func TestPersonaFor_GenericFallback(t *testing.T) {
	p := personaFor("/sbx/bin/personas/fakeagent")
	assert.Equal(t, genericPendingPrompt, p.pendingPrompt)
	assert.Equal(t, genericTrustPrompt, p.trustPrompt)
}

func TestRun_AskPrintsPendingPatternThenClears(t *testing.T) {
	out, code, _ := runScript(t, "aider", "ask\ny\n")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "(Y)es/(N)o/(D)on't ask again")
	assert.Contains(t, out, clearScreen+`answered: "y"`)
}

func TestRun_TrustWaitsForDismissal(t *testing.T) {
	out, _, _ := runScript(t, "claude", "trust\n\n")
	assert.Contains(t, out, "Do you trust the files in this folder?")
	assert.Contains(t, out, clearScreen+`trusted: ""`)
}

func TestRun_WorkTicksTenTimesPerSecond(t *testing.T) {
	var sleeps int
	var out bytes.Buffer
	a := newFakeAgent(personaFor("fakeagent"), strings.NewReader("work 2\n"), &out, t.TempDir())
	a.sleep = func(time.Duration) { sleeps++ }
	a.run()
	assert.Equal(t, 20, sleeps)
	assert.Contains(t, out.String(), "working 20/20")
	assert.Contains(t, out.String(), "work done")
}

func TestRun_WorkRejectsBadArg(t *testing.T) {
	out, _, _ := runScript(t, "fakeagent", "work x\n")
	assert.Contains(t, out, `want a non-negative number of seconds, got "x"`)
}

func TestRun_BellAndTitleEmitControlSequences(t *testing.T) {
	out, _, _ := runScript(t, "fakeagent", "bell\ntitle hello world\n")
	assert.Contains(t, out, "\a")
	assert.Contains(t, out, "\x1b]2;hello world\x07")
}

func TestRun_EditAppendsLines(t *testing.T) {
	out, _, a := runScript(t, "fakeagent", "edit\nedit\n")
	data, err := os.ReadFile(filepath.Join(a.dir, "fakeagent.txt"))
	require.NoError(t, err)
	assert.Equal(t, "edit 1\nedit 2\n", string(data))
	assert.Contains(t, out, "edited ")
}

func TestRun_CommitStagesThenCommits(t *testing.T) {
	var calls [][]string
	var out bytes.Buffer
	a := newFakeAgent(personaFor("fakeagent"), strings.NewReader("commit\n"), &out, t.TempDir())
	a.git = func(_ string, args ...string) error {
		calls = append(calls, args)
		return nil
	}
	a.run()
	assert.Equal(t, [][]string{{"add", "-A"}, {"commit", "--no-verify", "-m", "fakeagent: commit"}}, calls)
	assert.Contains(t, out.String(), "committed")
}

func TestRun_ExitCodes(t *testing.T) {
	_, code, _ := runScript(t, "fakeagent", "crash\nexit\n")
	assert.Equal(t, 1, code, "crash exits 1 before reading further")
	_, code, _ = runScript(t, "fakeagent", "exit\n")
	assert.Equal(t, 0, code)
	_, code, _ = runScript(t, "fakeagent", "")
	assert.Equal(t, 0, code, "EOF exits cleanly")
}

func TestRun_UnknownCommand(t *testing.T) {
	out, _, _ := runScript(t, "fakeagent", "dance\n")
	assert.Contains(t, out, `unknown command "dance"`)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./tools/fakeagent -v`
Expected: FAIL to compile — `undefined: newFakeAgent`, `personaFor`, etc.

- [ ] **Step 3: Implement the agent**

Create `tools/fakeagent/agent.go`:

```go
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/session/agent"
)

const (
	genericPendingPrompt = "Proceed? (y/n)"
	genericTrustPrompt   = "Trust this folder? (y/n)"
	usage                = "commands: work N | ask | trust | bell | title TEXT | edit | commit | crash | exit"
	// clearScreen wipes the visible screen after a prompt is answered so
	// the pattern stops matching loom's scan, the way a real agent redraws.
	clearScreen = "\x1b[2J\x1b[H"
)

// persona is the agent fakeagent impersonates. It is resolved through
// loom's own adapter registry, so prompt texts cannot drift from what loom
// scans for.
type persona struct {
	name          string
	pendingPrompt string
	trustPrompt   string
}

func personaFor(argv0 string) persona {
	ad := agent.DefaultRegistry().Lookup(argv0)
	p := persona{name: ad.Name(), pendingPrompt: ad.PendingPromptPattern(), trustPrompt: genericTrustPrompt}
	if p.pendingPrompt == "" {
		p.pendingPrompt = genericPendingPrompt
	}
	if pats := ad.TrustPromptPatterns(); len(pats) > 0 {
		p.trustPrompt = pats[0]
	}
	return p
}

type fakeAgent struct {
	p     persona
	in    *bufio.Scanner
	out   io.Writer
	dir   string
	sleep func(time.Duration)
	git   func(dir string, args ...string) error
	edits int
}

func newFakeAgent(p persona, in io.Reader, out io.Writer, dir string) *fakeAgent {
	return &fakeAgent{p: p, in: bufio.NewScanner(in), out: out, dir: dir, sleep: time.Sleep, git: runGit}
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

// run executes stdin commands until exit, crash, or EOF and returns the
// process exit code.
func (a *fakeAgent) run() int {
	fmt.Fprintf(a.out, "fakeagent: %s persona\n%s\n", a.p.name, usage)
	for {
		fmt.Fprint(a.out, "> ")
		if !a.in.Scan() {
			return 0
		}
		if code, done := a.exec(strings.TrimSpace(a.in.Text())); done {
			return code
		}
	}
}

func (a *fakeAgent) exec(line string) (code int, done bool) {
	cmd, arg, _ := strings.Cut(line, " ")
	switch cmd {
	case "":
	case "work":
		a.work(arg)
	case "ask":
		a.await(a.p.pendingPrompt, "answered")
	case "trust":
		a.await(a.p.trustPrompt, "trusted")
	case "bell":
		fmt.Fprint(a.out, "\a")
	case "title":
		fmt.Fprintf(a.out, "\x1b]2;%s\x07", arg)
	case "edit":
		a.edit()
	case "commit":
		a.commit()
	case "crash":
		fmt.Fprintln(a.out, "fakeagent: crashing")
		return 1, true
	case "exit":
		return 0, true
	default:
		fmt.Fprintf(a.out, "unknown command %q; %s\n", cmd, usage)
	}
	return 0, false
}

func (a *fakeAgent) work(arg string) {
	secs, err := strconv.Atoi(arg)
	if err != nil || secs < 0 {
		fmt.Fprintf(a.out, "work: want a non-negative number of seconds, got %q\n", arg)
		return
	}
	ticks := secs * 10
	for i := 1; i <= ticks; i++ {
		fmt.Fprintf(a.out, "working %d/%d\n", i, ticks)
		a.sleep(100 * time.Millisecond)
	}
	fmt.Fprintln(a.out, "work done")
}

func (a *fakeAgent) await(prompt, verb string) {
	fmt.Fprintln(a.out, prompt)
	if !a.in.Scan() {
		return
	}
	fmt.Fprintf(a.out, "%s%s: %q\n", clearScreen, verb, a.in.Text())
}

func (a *fakeAgent) edit() {
	a.edits++
	path := filepath.Join(a.dir, "fakeagent.txt")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(a.out, "edit: %v\n", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "edit %d\n", a.edits); err != nil {
		fmt.Fprintf(a.out, "edit: %v\n", err)
		return
	}
	fmt.Fprintf(a.out, "edited %s\n", path)
}

func (a *fakeAgent) commit() {
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--no-verify", "-m", "fakeagent: commit"}} {
		if err := a.git(a.dir, args...); err != nil {
			fmt.Fprintf(a.out, "commit: %v\n", err)
			return
		}
	}
	fmt.Fprintln(a.out, "committed")
}
```

Create `tools/fakeagent/main.go`:

```go
// Command fakeagent is a deterministic, token-free stand-in for an AI
// coding agent, used by loom's dev sandbox (tools/loomdev). The sandbox
// installs it under persona names (claude, aider); loom's adapter registry
// matches on the program's basename and applies the real adapter to it.
// Command-line flags — such as loom's Claude launch flags — are ignored.
package main

import "os"

func main() {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	os.Exit(newFakeAgent(personaFor(os.Args[0]), os.Stdin, os.Stdout, dir).run())
}
```

- [ ] **Step 4: Run to verify the tests pass**

Run: `gofmt -l tools && CGO_ENABLED=0 go test ./tools/fakeagent -v 2>&1 | tail -15`
Expected: no gofmt output; all PASS.

- [ ] **Step 5: Keep `tools/` out of the Nix package**

In `flake.nix`, inside `buildGoModule { ... }`, directly after `vendorHash = null;` add:

```nix
            # tools/ holds dev-only binaries (loomdev, fakeagent). Excluded
            # rather than using subPackages = [ "." ], which would also drop
            # every other package's tests from checkPhase.
            excludedPackages = [ "tools" ];
```

- [ ] **Step 6: Verify the Nix package (skip if `nix` is not installed)**

Run: `git add -N tools && nix build .#loom --no-link --print-out-paths | xargs -I{} ls {}/bin`
Expected: exactly `loom` (no `fakeagent`). The build's checkPhase still runs the other packages' tests.

- [ ] **Step 7: Commit**

```bash
git add tools/fakeagent flake.nix
git commit -m "feat(tools): add fakeagent, a deterministic agent stand-in

Reads its persona from argv[0] through loom's adapter registry so the
real Claude/Aider adapters apply. Nix excludes tools/ from the package.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB"
```

---

### Task 5: `internal/devsandbox` — layout, `Up`, `Build`, `Down`, `List`

**Files:**
- Create: `internal/devsandbox/sandbox.go`, `internal/devsandbox/up.go`, `internal/devsandbox/toyrepo.go`, `internal/devsandbox/helpers_test.go`, `internal/devsandbox/sandbox_test.go`, `internal/devsandbox/up_test.go`, `internal/devsandbox/toyrepo_test.go`

**Interfaces:**
- Consumes: `tmux.EnvTmuxSocket`, `tmux.CommandOnSocket` (Task 1); `config.EnvGlobalDir` (Task 2); existing `config.EnvHome`, `config.ConfigFileName`, `config.StateFileName`, `config.AtomicWriteFile(path string, data []byte, perm os.FileMode) error`, `config.Profile`, `config.Workspace`, `config.WorkspaceRegistry`, `config.LoadWorkspaceRegistry`, `config.LoadStateFrom`, `config.WorkspaceConfigDir`.
- Produces (package `devsandbox`):
  - `const WorkspaceName = "toy"`
  - `type Sandbox struct { Name, Dir string }`
  - `func BaseDir() (string, error)`, `func Open(name string) (*Sandbox, error)`, `func DefaultName(branch string) string`
  - path methods: `Socket, BinDir, LoomBin, FakeAgentBin, PersonaDir, GlobalDir, HomeDir, RepoDir, OriginDir, WorkspaceConfigDir() string`, `LogFiles() []string`
  - `Env() []string`, `Environ() []string`
  - `type Meta struct { Name, Socket, SourceWorktree, BuildSHA string; RealClaude bool; CreatedAt time.Time }`, `LoadMeta() (*Meta, error)`
  - `type UpOptions struct { SourceWorktree string; RealClaude bool; DefaultProfile string; Warn io.Writer }`, `Up(UpOptions) error`
  - `Build(srcDir string) error`, `Down() error`, `TailLogs(n int) string`
  - `type Info struct { Name, Dir string; ServerAlive bool; Meta *Meta }`, `func List() ([]Info, error)`

- [ ] **Step 1: Write the shared test helpers**

Create `internal/devsandbox/helpers_test.go`:

```go
package devsandbox

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/require"
)

func useTempBase(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	return filepath.Join(state, "loom-dev")
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func profileNames(ps []config.Profile) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return names
}
```

- [ ] **Step 2: Write the failing sandbox tests**

Create `internal/devsandbox/sandbox_test.go`:

```go
package devsandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaseDir_XDGThenHomeFallback(t *testing.T) {
	base := useTempBase(t)
	got, err := BaseDir()
	require.NoError(t, err)
	assert.Equal(t, base, got)

	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	got, err = BaseDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".local", "state", "loom-dev"), got)
}

func TestDefaultName(t *testing.T) {
	for in, want := range map[string]string{
		"aidanb/dev-env":       "dev-env",
		"main":                 "main",
		"feature/Fancy Thing!": "fancy-thing",
		"a/_hidden":            "hidden",
		"x/..":                 "default",
		"":                     "default",
	} {
		assert.Equal(t, want, DefaultName(in), in)
	}
}

func TestOpen_ValidatesNames(t *testing.T) {
	useTempBase(t)
	for _, bad := range []string{"", "../evil", "Bad", "-lead", "a/b"} {
		_, err := Open(bad)
		assert.Error(t, err, bad)
	}
}

func TestOpen_PathsAndEnv(t *testing.T) {
	base := useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "demo"), sb.Dir)
	assert.Equal(t, "loomdev-demo", sb.Socket())
	assert.Equal(t, filepath.Join(sb.Dir, "repo", ".loom"), sb.WorkspaceConfigDir())
	assert.Equal(t, filepath.Join(sb.Dir, "bin", "personas"), sb.PersonaDir())
	assert.Equal(t, []string{
		"LOOM_TMUX_SOCKET=loomdev-demo",
		"LOOM_GLOBAL_DIR=" + filepath.Join(sb.Dir, "global"),
		"LOOM_HOME=" + filepath.Join(sb.Dir, "home"),
	}, sb.Env())
	env := sb.Environ()
	assert.Equal(t, sb.Env(), env[len(env)-3:], "overlay must come last so it wins")
}

func TestDown_RefusesOutsideBase(t *testing.T) {
	useTempBase(t)
	victim := t.TempDir()
	sb := &Sandbox{Name: "demo", Dir: victim}
	require.Error(t, sb.Down())
	assert.DirExists(t, victim)
}

func TestDown_RemovesSandbox(t *testing.T) {
	useTempBase(t)
	// Unique name: Down kills tmux server loomdev-<name>, and socket names
	// are shared with any real sandbox on this machine.
	sb, err := Open(fmt.Sprintf("t%d", time.Now().UnixNano()))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(sb.Dir, "repo"), 0o755))
	require.NoError(t, sb.Down())
	assert.NoDirExists(t, sb.Dir)
}

func TestList_ReportsSandboxesWithMeta(t *testing.T) {
	base := useTempBase(t)
	for _, name := range []string{"beta", "alpha"} {
		sb, err := Open(name)
		require.NoError(t, err)
		require.NoError(t, sb.saveMeta(&Meta{Name: name, Socket: sb.Socket(), CreatedAt: time.Now()}))
	}
	require.NoError(t, os.WriteFile(filepath.Join(base, "stray.txt"), nil, 0o644))

	infos, err := List()
	require.NoError(t, err)
	require.Len(t, infos, 2)
	assert.Equal(t, "alpha", infos[0].Name)
	assert.Equal(t, "beta", infos[1].Name)
	assert.False(t, infos[0].ServerAlive)
	require.NotNil(t, infos[0].Meta)
	assert.Equal(t, "loomdev-alpha", infos[0].Meta.Socket)
}

func TestList_MissingBaseIsEmpty(t *testing.T) {
	useTempBase(t)
	infos, err := List()
	require.NoError(t, err)
	assert.Empty(t, infos)
}

func TestTailLogs_LastLinesPerFile(t *testing.T) {
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	path := sb.LogFiles()[0]
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644))
	out := sb.TailLogs(2)
	assert.Contains(t, out, "==> "+path+" <==\ntwo\nthree\n")
	assert.NotContains(t, out, "one")
}
```

- [ ] **Step 3: Write the failing toy-repo and `Up` tests**

Create `internal/devsandbox/toyrepo_test.go`:

```go
package devsandbox

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitToyRepo_HistoryAndOrigin(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	repo, origin := filepath.Join(dir, "repo"), filepath.Join(dir, "origin.git")
	require.NoError(t, initToyRepo(repo, origin))

	assert.Equal(t, "docs: add notes\nfeat: add hello program\nchore: initial commit",
		gitOut(t, repo, "log", "--format=%s"))
	assert.Equal(t, gitOut(t, repo, "rev-parse", "HEAD"), gitOut(t, origin, "rev-parse", "main"))
	assert.Equal(t, "origin/main", gitOut(t, repo, "rev-parse", "--abbrev-ref", "origin/HEAD"))
	assert.Empty(t, gitOut(t, repo, "status", "--porcelain"), ".loom/ must be ignored")
	assert.FileExists(t, filepath.Join(repo, "docs", "notes.md"))

	require.NoError(t, initToyRepo(repo, origin), "re-running is a no-op")
}
```

Create `internal/devsandbox/up_test.go`:

```go
package devsandbox

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func upSandbox(t *testing.T, opts UpOptions) *Sandbox {
	t.Helper()
	requireGit(t)
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	if opts.SourceWorktree == "" {
		opts.SourceWorktree = "/src/loom"
	}
	require.NoError(t, sb.Up(opts))
	return sb
}

func readConfig(t *testing.T, dir string) *config.Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, config.ConfigFileName))
	require.NoError(t, err)
	var cfg config.Config
	require.NoError(t, json.Unmarshal(data, &cfg))
	return &cfg
}

func TestUp_CreatesLayoutAndMeta(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	for _, d := range []string{sb.GlobalDir(), sb.HomeDir(), sb.RepoDir(), sb.OriginDir(), sb.WorkspaceConfigDir()} {
		assert.DirExists(t, d)
	}
	for _, name := range []string{"fakeagent", "claude", "aider"} {
		target, err := os.Readlink(filepath.Join(sb.PersonaDir(), name))
		require.NoError(t, err, name)
		assert.Equal(t, filepath.Join("..", "fakeagent"), target)
	}
	meta, err := sb.LoadMeta()
	require.NoError(t, err)
	assert.Equal(t, "demo", meta.Name)
	assert.Equal(t, "loomdev-demo", meta.Socket)
	assert.Equal(t, "/src/loom", meta.SourceWorktree)
	assert.False(t, meta.RealClaude)
}

func TestUp_WritesSandboxConfigToBothConfigDirs(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	for _, dir := range []string{sb.GlobalDir(), sb.WorkspaceConfigDir()} {
		cfg := readConfig(t, dir)
		assert.Equal(t, "fake", cfg.DefaultProgram, dir)
		assert.Equal(t, "dev/", cfg.BranchPrefix, dir)
		assert.False(t, cfg.RemoteControlEnabled(), dir)
		assert.Equal(t, []string{"fake", "fake-claude", "fake-aider", "shell"}, profileNames(cfg.Profiles), dir)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "fakeagent"), cfg.Profiles[0].Program)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "claude"), cfg.Profiles[1].Program)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "aider"), cfg.Profiles[2].Program)
	}
}

func TestUp_KeepsEditedConfigUnlessFlagsGiven(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	path := filepath.Join(sb.WorkspaceConfigDir(), config.ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte(`{"default_program":"shell"}`), 0o644))

	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/loom"}))
	assert.Equal(t, "shell", readConfig(t, sb.WorkspaceConfigDir()).DefaultProgram)

	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/loom", RealClaude: true, DefaultProfile: "fake-aider"}))
	cfg := readConfig(t, sb.WorkspaceConfigDir())
	assert.Equal(t, "fake-aider", cfg.DefaultProgram)
	assert.Contains(t, profileNames(cfg.Profiles), "claude")

	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/loom"}))
	meta, err := sb.LoadMeta()
	require.NoError(t, err)
	assert.True(t, meta.RealClaude, "--real-claude is sticky")
}

func TestUp_RejectsUnknownDefaultProfileBeforeCreatingAnything(t *testing.T) {
	requireGit(t)
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	err = sb.Up(UpOptions{SourceWorktree: "/src/loom", DefaultProfile: "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"nope"`)
	assert.NoDirExists(t, sb.Dir)
}

func TestUp_RejectsWhitespaceInPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "has space"))
	sb, err := Open("demo")
	require.NoError(t, err)
	err = sb.Up(UpOptions{SourceWorktree: "/src/loom"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whitespace")
}

func TestUp_SeedsHelpScreensSeen(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	for _, dir := range []string{sb.GlobalDir(), sb.WorkspaceConfigDir()} {
		assert.Equal(t, uint32(math.MaxUint32), config.LoadStateFrom(dir).GetHelpScreensSeen(), dir)
	}
}

func TestUp_RegistersToyWorkspaceOnce(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/loom"}))

	t.Setenv(config.EnvGlobalDir, sb.GlobalDir())
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.Len(t, reg.Workspaces, 1)
	ws := reg.Get(WorkspaceName)
	require.NotNil(t, ws)
	assert.Equal(t, sb.RepoDir(), ws.Path)
	assert.Equal(t, sb.WorkspaceConfigDir(), config.WorkspaceConfigDir(ws))
	assert.Equal(t, WorkspaceName, reg.LastUsed)
}

func TestUp_WarnsWhenSourceWorktreeChanges(t *testing.T) {
	sb := upSandbox(t, UpOptions{SourceWorktree: "/src/a"})
	var warn bytes.Buffer
	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/b", Warn: &warn}))
	assert.Contains(t, warn.String(), "/src/a")
	meta, err := sb.LoadMeta()
	require.NoError(t, err)
	assert.Equal(t, "/src/b", meta.SourceWorktree)
}

func TestBuild_RequiresUp(t *testing.T) {
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	assert.ErrorContains(t, sb.Build("/src/loom"), "not up")
}

func TestBuild_ProducesRunnableBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles loom; skipped with -short")
	}
	sb := upSandbox(t, UpOptions{})
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.NoError(t, sb.Build(root))

	cmd := exec.Command(sb.LoomBin(), "version")
	cmd.Env = sb.Environ() // LOOM_HOME set: legacy-home migration stays off
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "loom version")
	assert.FileExists(t, sb.FakeAgentBin())

	meta, err := sb.LoadMeta()
	require.NoError(t, err)
	assert.NotEmpty(t, meta.BuildSHA)
}
```

- [ ] **Step 4: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./internal/devsandbox -v 2>&1 | head -20`
Expected: FAIL to compile — `undefined: Open`, `BaseDir`, `initToyRepo`, `UpOptions`, …

- [ ] **Step 5: Implement `sandbox.go`**

Create `internal/devsandbox/sandbox.go`:

```go
// Package devsandbox creates and drives isolated loom dev environments: a
// named directory holding a dev build, a toy workspace repo, seeded config,
// and a private tmux server, so a dev loom can run from inside a loom pane
// without touching the host's sessions or state. See
// docs/superpowers/specs/2026-09-16-dev-sandbox-design.md.
package devsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/tmux"
)

// WorkspaceName is the name the toy repo is registered under.
const WorkspaceName = "toy"

const (
	socketPrefix = "loomdev-"
	metaFileName = "sandbox.json"
)

var (
	validName  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	invalidRun = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// Sandbox is one named, persistent dev environment.
type Sandbox struct {
	Name string
	Dir  string
}

// Meta is the sandbox's sandbox.json.
type Meta struct {
	Name           string    `json:"name"`
	Socket         string    `json:"socket"`
	SourceWorktree string    `json:"source_worktree"`
	BuildSHA       string    `json:"build_sha,omitempty"`
	RealClaude     bool      `json:"real_claude"`
	CreatedAt      time.Time `json:"created_at"`
}

// Info summarizes one sandbox for List.
type Info struct {
	Name        string
	Dir         string
	ServerAlive bool
	Meta        *Meta // nil when sandbox.json is missing or unreadable
}

// BaseDir returns the directory holding every sandbox:
// ${XDG_STATE_HOME:-~/.local/state}/loom-dev.
func BaseDir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "loom-dev"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "loom-dev"), nil
}

// Open resolves the sandbox called name without touching disk.
func Open(name string) (*Sandbox, error) {
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("invalid sandbox name %q (want %s)", name, validName)
	}
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}
	return &Sandbox{Name: name, Dir: filepath.Join(base, name)}, nil
}

// DefaultName derives a sandbox name from a git branch: the leaf after the
// last "/", lowercased, with runs of other characters collapsed to "-".
func DefaultName(branch string) string {
	leaf := branch
	if i := strings.LastIndex(branch, "/"); i >= 0 {
		leaf = branch[i+1:]
	}
	leaf = invalidRun.ReplaceAllString(strings.ToLower(leaf), "-")
	leaf = strings.Trim(leaf, "-._")
	if len(leaf) > 63 {
		leaf = strings.TrimRight(leaf[:63], "-._")
	}
	if leaf == "" {
		return "default"
	}
	return leaf
}

// Socket is the sandbox's private tmux socket name (tmux -L).
func (s *Sandbox) Socket() string { return socketPrefix + s.Name }

// BinDir holds the sandbox's built binaries.
func (s *Sandbox) BinDir() string { return filepath.Join(s.Dir, "bin") }

// LoomBin is the dev build of loom.
func (s *Sandbox) LoomBin() string { return filepath.Join(s.BinDir(), "loom") }

// FakeAgentBin is the built fakeagent.
func (s *Sandbox) FakeAgentBin() string { return filepath.Join(s.BinDir(), "fakeagent") }

// PersonaDir holds the fakeagent persona symlinks.
func (s *Sandbox) PersonaDir() string { return filepath.Join(s.BinDir(), "personas") }

// GlobalDir is LOOM_GLOBAL_DIR: workspaces.json plus the global context's config and state.
func (s *Sandbox) GlobalDir() string { return filepath.Join(s.Dir, "global") }

// HomeDir is LOOM_HOME: startup logs.
func (s *Sandbox) HomeDir() string { return filepath.Join(s.Dir, "home") }

// RepoDir is the toy workspace repo.
func (s *Sandbox) RepoDir() string { return filepath.Join(s.Dir, "repo") }

// OriginDir is the toy repo's bare remote.
func (s *Sandbox) OriginDir() string { return filepath.Join(s.Dir, "origin.git") }

// WorkspaceConfigDir is the toy workspace's .loom directory.
func (s *Sandbox) WorkspaceConfigDir() string { return filepath.Join(s.RepoDir(), ".loom") }

// LogFiles lists the loom.log files a sandboxed loom writes.
func (s *Sandbox) LogFiles() []string {
	return []string{
		filepath.Join(s.HomeDir(), "logs", "loom.log"),
		filepath.Join(s.WorkspaceConfigDir(), "logs", "loom.log"),
	}
}

// Env is the environment overlay every sandboxed loom process runs with.
func (s *Sandbox) Env() []string {
	return []string{
		tmux.EnvTmuxSocket + "=" + s.Socket(),
		config.EnvGlobalDir + "=" + s.GlobalDir(),
		config.EnvHome + "=" + s.HomeDir(),
	}
}

// Environ is os.Environ() with Env() appended; os/exec uses the last value
// of a duplicated key, so the overlay wins.
func (s *Sandbox) Environ() []string { return append(os.Environ(), s.Env()...) }

func (s *Sandbox) metaPath() string { return filepath.Join(s.Dir, metaFileName) }

// LoadMeta reads sandbox.json; the error wraps os.ErrNotExist when the
// sandbox has never been brought up.
func (s *Sandbox) LoadMeta() (*Meta, error) {
	data, err := os.ReadFile(s.metaPath())
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.metaPath(), err)
	}
	return &m, nil
}

func (s *Sandbox) saveMeta(m *Meta) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(s.metaPath(), append(data, '\n'), 0o644)
}

func (s *Sandbox) serverAlive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tmux.CommandOnSocket(ctx, s.Socket(), "list-sessions").Run() == nil
}

// Down kills the sandbox's private tmux server and deletes its directory.
// It refuses any Dir that is not <BaseDir>/<Name>.
func (s *Sandbox) Down() error {
	base, err := BaseDir()
	if err != nil {
		return err
	}
	if !validName.MatchString(s.Name) || s.Dir != filepath.Join(base, s.Name) {
		return fmt.Errorf("refusing to remove %s: not the sandbox directory %s", s.Dir, filepath.Join(base, s.Name))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tmux.CommandOnSocket(ctx, s.Socket(), "kill-server").Run() // no server is fine
	return os.RemoveAll(s.Dir)
}

// List returns every sandbox under BaseDir, sorted by name.
func List() ([]Info, error) {
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var infos []Info
	for _, e := range entries {
		if !e.IsDir() || !validName.MatchString(e.Name()) {
			continue
		}
		sb := &Sandbox{Name: e.Name(), Dir: filepath.Join(base, e.Name())}
		info := Info{Name: sb.Name, Dir: sb.Dir, ServerAlive: sb.serverAlive()}
		if m, err := sb.LoadMeta(); err == nil {
			info.Meta = m
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}

// TailLogs returns the last n lines of each existing sandbox log, each
// under a tail(1)-style "==> path <==" header.
func (s *Sandbox) TailLogs(n int) string {
	var b strings.Builder
	for _, path := range s.LogFiles() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		fmt.Fprintf(&b, "==> %s <==\n%s\n", path, strings.Join(lines, "\n"))
	}
	return b.String()
}
```

- [ ] **Step 6: Implement `toyrepo.go`**

Create `internal/devsandbox/toyrepo.go`:

```go
package devsandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type toyCommit struct {
	msg   string
	files map[string]string
}

var toyHistory = []toyCommit{
	{"chore: initial commit", map[string]string{
		".gitignore": ".loom/\n",
		"README.md":  "# toy\n\nThe loom dev sandbox's workspace repo.\n",
	}},
	{"feat: add hello program", map[string]string{
		"go.mod":  "module toy\n\ngo 1.23\n",
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello from toy\")\n}\n",
	}},
	{"docs: add notes", map[string]string{
		"docs/notes.md": "# Notes\n\n## Why\n\nMarkdown for the workbench's markdown and review tabs.\n\n## Todo\n\n- nothing yet\n",
	}},
}

// initToyRepo creates the sandbox workspace repo with a short history,
// pushed to a bare origin. A repoDir that already holds a git repo is left
// untouched (a half-built one needs `loomdev down`).
func initToyRepo(repoDir, originDir string) error {
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "loom dev sandbox"},
		{"config", "user.email", "loomdev@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		if err := git(repoDir, args...); err != nil {
			return err
		}
	}
	for _, c := range toyHistory {
		for rel, body := range c.files {
			path := filepath.Join(repoDir, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return err
			}
		}
		if err := git(repoDir, "add", "-A"); err != nil {
			return err
		}
		if err := git(repoDir, "commit", "-q", "--no-verify", "-m", c.msg); err != nil {
			return err
		}
	}
	if err := git(filepath.Dir(originDir), "init", "-q", "--bare", originDir); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"remote", "add", "origin", originDir},
		{"push", "-q", "--no-verify", "-u", "origin", "main"},
		{"remote", "set-head", "origin", "main"},
	} {
		if err := git(repoDir, args...); err != nil {
			return err
		}
	}
	return nil
}

func git(dir string, args ...string) error {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}
```

- [ ] **Step 7: Implement `up.go`**

Create `internal/devsandbox/up.go`:

```go
package devsandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
)

// registryFileName mirrors config's unexported workspacesFileName;
// TestUp_RegistersToyWorkspaceOnce reads it back via config to catch drift.
const registryFileName = "workspaces.json"

var personaNames = []string{"fakeagent", "claude", "aider"}

// UpOptions configures Up.
type UpOptions struct {
	// SourceWorktree is the loom checkout the sandbox builds from.
	SourceWorktree string
	// RealClaude adds a profile running the real claude CLI. Sticky.
	RealClaude bool
	// DefaultProfile selects config.json's default profile ("" keeps the
	// existing file, or "fake" for a new one). Non-empty rewrites config.json.
	DefaultProfile string
	// Warn, when non-nil, receives non-fatal warnings.
	Warn io.Writer
}

// Up creates the sandbox or tops up a partial one. Each step checks for its
// own artifact, so re-running is safe; config.json is only rewritten when
// it is missing or opts asks for a profile change.
func (s *Sandbox) Up(opts UpOptions) error {
	if strings.ContainsAny(s.Dir, " \t\n") {
		return fmt.Errorf("sandbox path %q contains whitespace; loom splits program strings on spaces (set XDG_STATE_HOME elsewhere)", s.Dir)
	}
	meta, err := s.LoadMeta()
	switch {
	case errors.Is(err, os.ErrNotExist):
		meta = &Meta{Name: s.Name, Socket: s.Socket(), CreatedAt: time.Now()}
	case err != nil:
		return err
	case meta.SourceWorktree != opts.SourceWorktree && opts.Warn != nil:
		fmt.Fprintf(opts.Warn, "loomdev: sandbox %q was last used from %s; now %s\n", s.Name, meta.SourceWorktree, opts.SourceWorktree)
	}
	meta.SourceWorktree = opts.SourceWorktree
	meta.RealClaude = meta.RealClaude || opts.RealClaude

	cfg, err := sandboxConfig(s.PersonaDir(), meta.RealClaude, opts.DefaultProfile)
	if err != nil {
		return err
	}
	for _, dir := range []string{s.GlobalDir(), s.HomeDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := s.ensurePersonas(); err != nil {
		return err
	}
	if err := initToyRepo(s.RepoDir(), s.OriginDir()); err != nil {
		return fmt.Errorf("toy repo: %w", err)
	}
	force := opts.RealClaude || opts.DefaultProfile != ""
	for _, dir := range []string{s.GlobalDir(), s.WorkspaceConfigDir()} {
		if err := writeConfig(dir, cfg, force); err != nil {
			return err
		}
		if err := seedState(dir); err != nil {
			return err
		}
	}
	if err := s.registerWorkspace(); err != nil {
		return err
	}
	return s.saveMeta(meta)
}

func sandboxConfig(personaDir string, realClaude bool, defaultProfile string) (map[string]any, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	profiles := []config.Profile{
		{Name: "fake", Program: filepath.Join(personaDir, "fakeagent")},
		{Name: "fake-claude", Program: filepath.Join(personaDir, "claude")},
		{Name: "fake-aider", Program: filepath.Join(personaDir, "aider")},
		{Name: "shell", Program: shell},
	}
	if realClaude {
		profiles = append(profiles, config.Profile{Name: "claude", Program: "claude"})
	}
	if defaultProfile == "" {
		defaultProfile = "fake"
	}
	known := false
	for _, p := range profiles {
		known = known || p.Name == defaultProfile
	}
	if !known {
		return nil, fmt.Errorf("unknown default profile %q (have %v)", defaultProfile, profileList(profiles))
	}
	// Keys mirror config.Config's json tags; TestUp_WritesSandboxConfigToBothConfigDirs
	// unmarshals the result into config.Config to catch drift.
	return map[string]any{
		"default_program":       defaultProfile,
		"branch_prefix":         "dev/",
		"profiles":              profiles,
		"claude_remote_control": false,
	}, nil
}

func profileList(ps []config.Profile) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return names
}

func writeConfig(dir string, cfg map[string]any, force bool) error {
	path := filepath.Join(dir, config.ConfigFileName)
	if _, err := os.Stat(path); err == nil && !force {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, append(data, '\n'), 0o644)
}

// seedState pre-marks every help screen as seen so first-run overlays never
// block a headless driver. Existing state is left alone.
func seedState(dir string) error {
	path := filepath.Join(dir, config.StateFileName)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return config.AtomicWriteFile(path, []byte("{\"help_screens_seen\": 4294967295, \"instances\": []}\n"), 0o644)
}

func (s *Sandbox) ensurePersonas() error {
	if err := os.MkdirAll(s.PersonaDir(), 0o755); err != nil {
		return err
	}
	for _, name := range personaNames {
		link := filepath.Join(s.PersonaDir(), name)
		if _, err := os.Lstat(link); err == nil {
			continue
		}
		if err := os.Symlink(filepath.Join("..", "fakeagent"), link); err != nil {
			return fmt.Errorf("persona %s: %w", name, err)
		}
	}
	return nil
}

func (s *Sandbox) registerWorkspace() error {
	path := filepath.Join(s.GlobalDir(), registryFileName)
	var reg config.WorkspaceRegistry
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &reg); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if reg.Get(WorkspaceName) != nil {
			return nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	reg.Workspaces = append(reg.Workspaces, config.Workspace{Name: WorkspaceName, Path: s.RepoDir(), AddedAt: time.Now()})
	reg.LastUsed = WorkspaceName
	out, err := json.MarshalIndent(&reg, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, out, 0o644)
}

// Build compiles loom and fakeagent from the module rooted at srcDir into
// the sandbox and records the source revision. The sandbox must be Up.
func (s *Sandbox) Build(srcDir string) error {
	meta, err := s.LoadMeta()
	if err != nil {
		return fmt.Errorf("sandbox %q is not up (run `loomdev up`): %w", s.Name, err)
	}
	for _, t := range []struct{ out, pkg string }{
		{s.LoomBin(), "."},
		{s.FakeAgentBin(), "./tools/fakeagent"},
	} {
		cmd := exec.Command("go", "build", "-o", t.out, t.pkg)
		cmd.Dir = srcDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go build %s: %w\n%s", t.pkg, err, out)
		}
	}
	meta.BuildSHA = sourceRevision(srcDir)
	return s.saveMeta(meta)
}

// sourceRevision describes srcDir as "<sha>" or "<sha>-dirty", or "unknown"
// when it is not a git checkout (e.g. a Nix build source).
func sourceRevision(srcDir string) string {
	head, err := exec.Command("git", "-C", srcDir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	rev := strings.TrimSpace(string(head))
	status, err := exec.Command("git", "-C", srcDir, "status", "--porcelain").Output()
	if err == nil && len(bytes.TrimSpace(status)) > 0 {
		rev += "-dirty"
	}
	return rev
}
```

- [ ] **Step 8: Run to verify the package passes**

Run: `gofmt -l internal && CGO_ENABLED=0 go test ./internal/devsandbox -v 2>&1 | tail -30`
Expected: no gofmt output; all PASS (`TestBuild_ProducesRunnableBinaries` compiles loom — allow ~30s on a cold cache).

- [ ] **Step 9: Commit**

```bash
git add internal/devsandbox
git commit -m "feat(devsandbox): named sandboxes with toy repo and seeded config

Up creates the layout, persona symlinks, a toy repo pushed to a bare
origin, sandbox config/state in both config roots, and the registry
entry; Build compiles loom and fakeagent; Down and List manage them.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB"
```

---

### Task 6: `internal/devsandbox` — headless driver

**Files:**
- Create: `internal/devsandbox/driver.go`, `internal/devsandbox/driver_test.go`

**Interfaces:**
- Consumes: `Sandbox`, `Open`, `Socket`, `Env`, `Environ`, `RepoDir`, `LoomBin`, `Down`, `WorkspaceName` (Task 5); `tmux.CommandOnSocket` (Task 1).
- Produces (package `devsandbox`):
  - `const DriverSession = "dev-driver"`
  - `type StartOptions struct { Width, Height int; Restart bool; Command []string }`
  - `Start(StartOptions) error`, `Stop(grace time.Duration) error`, `DriverRunning() bool`
  - `SendKeys(keys ...string) error`, `SendText(text string) error`
  - `Screen(ansi bool) (string, error)`, `WaitFor(text string, timeout time.Duration) error`
  - `type WaitTimeoutError struct { Text string; Timeout time.Duration; Screen string }`

- [ ] **Step 1: Write the failing driver tests**

These use tiny shell programs instead of loom, so they run in about a second. Create `internal/devsandbox/driver_test.go`:

```go
package devsandbox

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func driverSandbox(t *testing.T) *Sandbox {
	t.Helper()
	requireTmux(t)
	useTempBase(t)
	sb, err := Open(fmt.Sprintf("t%d", time.Now().UnixNano()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sb.Down() })
	require.NoError(t, os.MkdirAll(sb.RepoDir(), 0o755))
	return sb
}

func TestShellJoin(t *testing.T) {
	assert.Equal(t, `'a b' 'it'\''s' 'plain'`, shellJoin([]string{"a b", "it's", "plain"}))
}

func TestDriver_KeysReachThePane(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo ready; exec cat"}}))
	require.NoError(t, sb.WaitFor("ready", 5*time.Second))
	assert.True(t, sb.DriverRunning())

	require.NoError(t, sb.SendText("hello-driver"))
	require.NoError(t, sb.SendKeys("Enter"))
	require.NoError(t, sb.WaitFor("hello-driver", 5*time.Second))
}

func TestDriver_EnvReachesThePane(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo sock=$LOOM_TMUX_SOCKET; exec cat"}}))
	require.NoError(t, sb.WaitFor("sock="+sb.Socket(), 5*time.Second))
}

func TestDriver_StartIsIdempotentUnlessRestart(t *testing.T) {
	sb := driverSandbox(t)
	cmd := []string{"sh", "-c", "echo pid=$$; exec cat"}
	require.NoError(t, sb.Start(StartOptions{Command: cmd}))
	require.NoError(t, sb.WaitFor("pid=", 5*time.Second))
	first, err := sb.Screen(false)
	require.NoError(t, err)

	require.NoError(t, sb.Start(StartOptions{Command: cmd}))
	again, err := sb.Screen(false)
	require.NoError(t, err)
	assert.Equal(t, first, again, "a second Start must not replace a running driver")

	require.NoError(t, sb.Start(StartOptions{Command: cmd, Restart: true}))
	require.NoError(t, sb.WaitFor("pid=", 5*time.Second))
	restarted, err := sb.Screen(false)
	require.NoError(t, err)
	assert.NotEqual(t, first, restarted)
}

func TestDriver_SizeIsApplied(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Width: 100, Height: 30, Command: []string{"sh", "-c", "sleep 0.2; stty size; exec cat"}}))
	require.NoError(t, sb.WaitFor("30 100", 5*time.Second))
}

func TestDriver_DeadPaneKeptForDiagnosis(t *testing.T) {
	sb := driverSandbox(t)
	// The short sleep keeps the exit after remain-on-exit is applied.
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo boom; sleep 0.3; exit 3"}}))
	require.NoError(t, sb.WaitFor("boom", 5*time.Second))
	require.Eventually(t, func() bool { return !sb.DriverRunning() }, 5*time.Second, 50*time.Millisecond)
	screen, err := sb.Screen(false)
	require.NoError(t, err)
	assert.Contains(t, screen, "boom", "remain-on-exit keeps the crash output capturable")

	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo fresh; exec cat"}}))
	require.NoError(t, sb.WaitFor("fresh", 5*time.Second), "Start replaces a dead driver")
}

func TestDriver_StopUsesQuitKeyFirst(t *testing.T) {
	sb := driverSandbox(t)
	// Exits on the first byte it reads, like loom exiting on `q`.
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "stty -icanon -echo; echo ready; head -c1 >/dev/null"}}))
	require.NoError(t, sb.WaitFor("ready", 5*time.Second))
	start := time.Now()
	require.NoError(t, sb.Stop(5*time.Second))
	assert.Less(t, time.Since(start), 4*time.Second, "q should end the program well before the grace period")
	assert.False(t, sb.driverExists())
}

func TestDriver_StopKillsAStubbornProgram(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo ready; exec cat"}}))
	require.NoError(t, sb.WaitFor("ready", 5*time.Second))
	require.NoError(t, sb.Stop(300*time.Millisecond))
	assert.False(t, sb.driverExists())
	require.NoError(t, sb.Stop(time.Second), "stopping a stopped driver is a no-op")
}

func TestDriver_WaitForTimeoutCarriesScreen(t *testing.T) {
	sb := driverSandbox(t)
	require.NoError(t, sb.Start(StartOptions{Command: []string{"sh", "-c", "echo visible; exec cat"}}))
	require.NoError(t, sb.WaitFor("visible", 5*time.Second))
	err := sb.WaitFor("never-there", 300*time.Millisecond)
	var timeout *WaitTimeoutError
	require.True(t, errors.As(err, &timeout))
	assert.Equal(t, "never-there", timeout.Text)
	assert.Contains(t, timeout.Screen, "visible")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./internal/devsandbox -run 'Driver|ShellJoin' -v 2>&1 | head`
Expected: FAIL to compile — `undefined: StartOptions`, `shellJoin`, …

- [ ] **Step 3: Implement the driver**

Create `internal/devsandbox/driver.go`:

```go
package devsandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/session/tmux"
)

// DriverSession is the tmux session the headless dev loom runs in. It has
// no loom_ prefix, so loom's orphan sweep never touches it.
const DriverSession = "dev-driver"

const (
	defaultWidth  = 160
	defaultHeight = 48
	driverTimeout = 10 * time.Second
	pollInterval  = 100 * time.Millisecond
)

// StartOptions configures Start.
type StartOptions struct {
	// Width and Height size the driver pane (0 means 160×48).
	Width, Height int
	// Restart replaces an already-running driver instead of keeping it.
	Restart bool
	// Command overrides the program (default: the dev loom on the toy
	// workspace). Tests use it to drive plain shell programs.
	Command []string
}

// WaitTimeoutError is returned by WaitFor when the text never appears.
type WaitTimeoutError struct {
	Text    string
	Timeout time.Duration
	// Screen is the last successful capture, for diagnosis.
	Screen string
}

func (e *WaitTimeoutError) Error() string {
	return fmt.Sprintf("timed out after %s waiting for %q", e.Timeout, e.Text)
}

func (s *Sandbox) tmuxCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := tmux.CommandOnSocket(ctx, s.Socket(), args...)
	// The first command starts the private server, whose global environment
	// then carries the sandbox overlay into every pane it spawns.
	cmd.Env = s.Environ()
	return cmd
}

func (s *Sandbox) runTmux(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), driverTimeout)
	defer cancel()
	out, err := s.tmuxCmd(ctx, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (s *Sandbox) driverExists() bool {
	_, err := s.runTmux("has-session", "-t="+DriverSession)
	return err == nil
}

// DriverRunning reports whether the driver session exists and its program
// is still alive (a dead pane is kept by remain-on-exit).
func (s *Sandbox) DriverRunning() bool {
	out, err := s.runTmux("display-message", "-p", "-t", DriverSession, "#{pane_dead}")
	return err == nil && strings.TrimSpace(out) == "0"
}

// Start launches the driver session on the private server. A running
// driver is kept unless opts.Restart is set; a dead one is replaced.
func (s *Sandbox) Start(opts StartOptions) error {
	if s.DriverRunning() {
		if !opts.Restart {
			return nil
		}
		if err := s.Stop(5 * time.Second); err != nil {
			return err
		}
	} else if s.driverExists() {
		if _, err := s.runTmux("kill-session", "-t="+DriverSession); err != nil {
			return err
		}
	}
	w, h := opts.Width, opts.Height
	if w <= 0 {
		w = defaultWidth
	}
	if h <= 0 {
		h = defaultHeight
	}
	argv := opts.Command
	if len(argv) == 0 {
		argv = []string{s.LoomBin(), "--workspace", WorkspaceName}
	}
	// status off is set server-wide *before* new-session so the pane starts
	// at exactly w×h (loom turns the status line off on its own sessions
	// anyway). new-session in the same command list starts the server.
	args := []string{"set-option", "-g", "status", "off", ";",
		"new-session", "-d", "-s", DriverSession,
		"-x", strconv.Itoa(w), "-y", strconv.Itoa(h), "-c", s.RepoDir()}
	for _, e := range s.Env() {
		args = append(args, "-e", e)
	}
	args = append(args, shellJoin(argv),
		";", "set-option", "-w", "-t", DriverSession, "remain-on-exit", "on")
	_, err := s.runTmux(args...)
	return err
}

// Stop asks the driver's program to quit with `q`, waits up to grace for it
// to exit, then removes the driver session. Loom's own agent sessions stay
// on the private server, as after a real quit.
func (s *Sandbox) Stop(grace time.Duration) error {
	if !s.driverExists() {
		return nil
	}
	if s.DriverRunning() && s.SendKeys("q") == nil {
		deadline := time.Now().Add(grace)
		for s.DriverRunning() && time.Now().Before(deadline) {
			time.Sleep(pollInterval)
		}
	}
	if _, err := s.runTmux("kill-session", "-t="+DriverSession); err != nil && s.driverExists() {
		return err
	}
	return nil
}

// SendKeys sends tmux key names (e.g. "n", "Enter", "Escape", "C-c") to the
// driver pane. A word tmux does not recognize is typed as characters.
func (s *Sandbox) SendKeys(keys ...string) error {
	_, err := s.runTmux(append([]string{"send-keys", "-t", DriverSession}, keys...)...)
	return err
}

// SendText types text literally, with no key-name interpretation.
func (s *Sandbox) SendText(text string) error {
	_, err := s.runTmux("send-keys", "-t", DriverSession, "-l", text)
	return err
}

// Screen captures the driver pane; ansi keeps colors and attributes.
func (s *Sandbox) Screen(ansi bool) (string, error) {
	args := []string{"capture-pane", "-p", "-t", DriverSession}
	if ansi {
		args = append(args, "-e")
	}
	return s.runTmux(args...)
}

// WaitFor polls the driver screen until text appears or timeout elapses.
func (s *Sandbox) WaitFor(text string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		if screen, err := s.Screen(false); err == nil {
			last = screen
			if strings.Contains(screen, text) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return &WaitTimeoutError{Text: text, Timeout: timeout, Screen: last}
		}
		time.Sleep(pollInterval)
	}
}

// shellJoin single-quotes argv for the shell tmux runs the command with.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
```

- [ ] **Step 4: Run to verify the driver tests pass**

Run: `gofmt -l internal && CGO_ENABLED=0 go test ./internal/devsandbox -run 'Driver|ShellJoin' -v 2>&1 | tail -25`
Expected: all PASS. If `Start` fails with a "no server running" error from the leading `set-option -g`, prepend `"start-server", ";"` to its args (tmux normally starts the server for any command list containing `new-session`). If `TestDriver_SizeIsApplied` fails with a different size, check `tmux -V` (needs ≥ 3.0); do not "fix" it by loosening the assertion.

- [ ] **Step 5: Run the whole package with the race detector if a C compiler exists, else plainly**

Run: `CGO_ENABLED=0 go test ./internal/devsandbox 2>&1 | tail -5`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/devsandbox/driver.go internal/devsandbox/driver_test.go
git commit -m "feat(devsandbox): headless driver on the private tmux server

Start/Stop a dev-driver session (status off, remain-on-exit), send keys
or literal text, capture the screen, and wait for text with a timeout
error that carries the last screen.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB"
```

---

### Task 7: `tools/loomdev` CLI

**Files:**
- Create: `tools/loomdev/main.go`, `tools/loomdev/cmds.go`, `tools/loomdev/logs.go`, `tools/loomdev/main_test.go`

**Interfaces:**
- Consumes: everything `devsandbox` produces in Tasks 5–6.
- Produces: `go run ./tools/loomdev` with subcommands `up`, `build`, `run`, `start`, `stop`, `keys`, `shot`, `wait`, `logs`, `env`, `ls`, `down`, and persistent flag `--sandbox/-s`.

- [ ] **Step 1: Write the failing CLI tests**

Create `tools/loomdev/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd(&out, &out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestRoot_HasEverySubcommand(t *testing.T) {
	root := newRootCmd(io.Discard, io.Discard)
	var names []string
	for _, c := range root.Commands() {
		names = append(names, c.Name())
	}
	for _, want := range []string{"up", "build", "run", "start", "stop", "keys", "shot", "wait", "logs", "env", "ls", "down"} {
		assert.Contains(t, names, want)
	}
}

func TestEnv_PrintsExports(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out, err := execute(t, "env", "--sandbox", "demo")
	require.NoError(t, err)
	assert.Contains(t, out, "export LOOM_TMUX_SOCKET='loomdev-demo'\n")
	assert.Contains(t, out, "export LOOM_GLOBAL_DIR='")
	assert.Contains(t, out, "export LOOM_HOME='")
}

func TestSandboxFlag_RejectsBadNames(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, err := execute(t, "env", "--sandbox", "../evil")
	assert.Error(t, err)
}

func TestKeys_RequiresArgs(t *testing.T) {
	_, err := execute(t, "keys", "--sandbox", "demo")
	assert.Error(t, err)
}

func TestWait_RequiresText(t *testing.T) {
	_, err := execute(t, "wait", "--sandbox", "demo")
	assert.Error(t, err)
}

func TestLs_EmptyBase(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out, err := execute(t, "ls")
	require.NoError(t, err)
	assert.Contains(t, out, "no sandboxes")
}

func TestParseSize(t *testing.T) {
	w, h, err := parseSize("160x48")
	require.NoError(t, err)
	assert.Equal(t, 160, w)
	assert.Equal(t, 48, h)
	for _, bad := range []string{"x", "0x10", "10x0", "abc", "10x"} {
		_, _, err := parseSize(bad)
		assert.Error(t, err, bad)
	}
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, `'it'\''s'`, shellQuote("it's"))
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestFollowLogs_StreamsAppendedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loom.log")
	require.NoError(t, os.WriteFile(path, []byte("old line\n"), 0o644))
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- followLogs(ctx, &out, []string{path}, 20*time.Millisecond) }()

	time.Sleep(60 * time.Millisecond)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("new line\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	require.Eventually(t, func() bool { return strings.Contains(out.String(), "new line") }, 2*time.Second, 20*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	assert.NotContains(t, out.String(), "old line", "follow starts at the current end")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./tools/loomdev -v 2>&1 | head`
Expected: FAIL to compile — `undefined: newRootCmd`, `parseSize`, `shellQuote`, `followLogs`.

- [ ] **Step 3: Implement `main.go`**

Create `tools/loomdev/main.go`:

```go
// Command loomdev manages isolated loom dev sandboxes so a dev build can be
// run, driven, and screenshotted from inside loom without touching the host
// loom's tmux sessions or state. See .claude/skills/loom-dev/SKILL.md and
// docs/superpowers/specs/2026-09-16-dev-sandbox-design.md.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/spf13/cobra"
)

const modulePath = "github.com/aidan-bailey/loom"

type app struct {
	out, errOut io.Writer
	sandbox     string
}

// exitError carries the dev loom's own exit status out of `run`.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("loom exited with status %d", e.code) }

func main() {
	if err := newRootCmd(os.Stdout, os.Stderr).Execute(); err != nil {
		var exit *exitError
		if errors.As(err, &exit) {
			os.Exit(exit.code)
		}
		fmt.Fprintln(os.Stderr, "loomdev:", err)
		os.Exit(1)
	}
}

func newRootCmd(out, errOut io.Writer) *cobra.Command {
	a := &app{out: out, errOut: errOut}
	root := &cobra.Command{
		Use:           "loomdev",
		Short:         "Isolated loom-in-loom dev sandboxes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.PersistentFlags().StringVarP(&a.sandbox, "sandbox", "s", "",
		"sandbox name (default: leaf of the current git branch)")
	root.AddCommand(a.upCmd(), a.buildCmd(), a.runCmd(), a.startCmd(), a.stopCmd(),
		a.keysCmd(), a.shotCmd(), a.waitCmd(), a.logsCmd(), a.envCmd(), a.lsCmd(), a.downCmd())
	return root
}

// moduleRoot returns the top of the loom checkout loomdev runs in.
func moduleRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("loomdev must run inside a loom checkout: %w", err)
	}
	root := strings.TrimSpace(string(out))
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.HasPrefix(string(data), "module "+modulePath+"\n") {
		return "", fmt.Errorf("%s is not a %s checkout", root, modulePath)
	}
	return root, nil
}

func (a *app) open() (*devsandbox.Sandbox, error) {
	name := a.sandbox
	if name == "" {
		out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err != nil {
			return nil, fmt.Errorf("derive the sandbox name from the branch (or pass --sandbox): %w", err)
		}
		name = devsandbox.DefaultName(strings.TrimSpace(string(out)))
	}
	return devsandbox.Open(name)
}

// prepare brings the sandbox up without changing its config, then rebuilds
// it unless skipBuild is set.
func (a *app) prepare(skipBuild bool) (*devsandbox.Sandbox, error) {
	sb, err := a.open()
	if err != nil {
		return nil, err
	}
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	if err := sb.Up(devsandbox.UpOptions{SourceWorktree: root, Warn: a.errOut}); err != nil {
		return nil, err
	}
	if skipBuild {
		if _, err := os.Stat(sb.LoomBin()); err != nil {
			return nil, fmt.Errorf("sandbox %q has no build yet; drop --no-build", sb.Name)
		}
		return sb, nil
	}
	return sb, sb.Build(root)
}
```

- [ ] **Step 4: Implement `cmds.go`**

Create `tools/loomdev/cmds.go`:

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/spf13/cobra"
)

func (a *app) upCmd() *cobra.Command {
	var realClaude bool
	var profile string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Create or top up the sandbox, then build loom into it",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			root, err := moduleRoot()
			if err != nil {
				return err
			}
			if err := sb.Up(devsandbox.UpOptions{SourceWorktree: root, RealClaude: realClaude, DefaultProfile: profile, Warn: a.errOut}); err != nil {
				return err
			}
			if err := sb.Build(root); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "sandbox %s ready at %s\n", sb.Name, sb.Dir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&realClaude, "real-claude", false, "add a `claude` profile running the real CLI (sticky)")
	cmd.Flags().StringVar(&profile, "default-profile", "", "default profile: fake, fake-claude, fake-aider, shell, claude (rewrites config.json)")
	return cmd
}

func (a *app) buildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "Rebuild loom and fakeagent into the sandbox",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.prepare(false)
			if err != nil {
				return err
			}
			meta, err := sb.LoadMeta()
			if err != nil {
				return err
			}
			fmt.Fprintf(a.out, "built %s into %s\n", meta.BuildSHA, sb.BinDir())
			return nil
		},
	}
}

func (a *app) runCmd() *cobra.Command {
	var noBuild bool
	cmd := &cobra.Command{
		Use:   "run [-- loom-args...]",
		Short: "Run the sandboxed loom interactively in this terminal",
		RunE: func(_ *cobra.Command, args []string) error {
			sb, err := a.prepare(noBuild)
			if err != nil {
				return err
			}
			c := exec.Command(sb.LoomBin(), append([]string{"--workspace", devsandbox.WorkspaceName}, args...)...)
			c.Dir = sb.RepoDir()
			c.Env = sb.Environ()
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					return &exitError{code: ee.ExitCode()}
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "skip rebuilding")
	return cmd
}

func (a *app) startCmd() *cobra.Command {
	var noBuild, restart bool
	var size string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the sandboxed loom headlessly in the driver session",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			w, h, err := parseSize(size)
			if err != nil {
				return err
			}
			sb, err := a.prepare(noBuild)
			if err != nil {
				return err
			}
			if err := sb.Start(devsandbox.StartOptions{Width: w, Height: h, Restart: restart}); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "driver running on tmux -L %s (session %s)\n", sb.Socket(), devsandbox.DriverSession)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "skip rebuilding")
	cmd.Flags().BoolVar(&restart, "restart", false, "replace a running driver")
	cmd.Flags().StringVar(&size, "size", "160x48", "driver pane size WIDTHxHEIGHT")
	return cmd
}

func (a *app) stopCmd() *cobra.Command {
	var grace time.Duration
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Quit the headless loom and remove the driver session",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			return sb.Stop(grace)
		},
	}
	cmd.Flags().DurationVar(&grace, "grace", 5*time.Second, "how long to wait for loom to quit before killing it")
	return cmd
}

func (a *app) keysCmd() *cobra.Command {
	var literal bool
	cmd := &cobra.Command{
		Use:   "keys KEY...",
		Short: "Send tmux key names (Enter, Escape, Up, C-c, n …) to the headless loom",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			if literal {
				return sb.SendText(strings.Join(args, " "))
			}
			return sb.SendKeys(args...)
		},
	}
	cmd.Flags().BoolVarP(&literal, "literal", "l", false, "type the arguments as literal text")
	return cmd
}

func (a *app) shotCmd() *cobra.Command {
	var ansi bool
	cmd := &cobra.Command{
		Use:   "shot",
		Short: "Print the headless loom's screen",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			screen, err := sb.Screen(ansi)
			if err != nil {
				return err
			}
			fmt.Fprint(a.out, screen)
			return nil
		},
	}
	cmd.Flags().BoolVar(&ansi, "ansi", false, "keep colors and attributes")
	return cmd
}

func (a *app) waitCmd() *cobra.Command {
	var text string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait --text TEXT",
		Short: "Wait until TEXT appears on the headless loom's screen",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			err = sb.WaitFor(text, timeout)
			var timedOut *devsandbox.WaitTimeoutError
			if errors.As(err, &timedOut) {
				fmt.Fprintf(a.out, "--- last screen ---\n%s\n--- sandbox logs ---\n%s", timedOut.Screen, sb.TailLogs(20))
			}
			return err
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "text to wait for (required)")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "how long to wait")
	_ = cmd.MarkFlagRequired("text")
	return cmd
}

func (a *app) logsCmd() *cobra.Command {
	var lines int
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the sandbox's loom.log files",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			fmt.Fprint(a.out, sb.TailLogs(lines))
			if !follow {
				return nil
			}
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
			defer stop()
			return followLogs(ctx, a.out, sb.LogFiles(), 250*time.Millisecond)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "lines per file")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines until interrupted")
	return cmd
}

func (a *app) envCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "env",
		Short: "Print export lines for the sandbox environment (eval \"$(loomdev env)\")",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			for _, kv := range sb.Env() {
				k, v, _ := strings.Cut(kv, "=")
				fmt.Fprintf(a.out, "export %s=%s\n", k, shellQuote(v))
			}
			return nil
		},
	}
}

func (a *app) lsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List sandboxes",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			infos, err := devsandbox.List()
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				fmt.Fprintln(a.out, "no sandboxes")
				return nil
			}
			tw := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSERVER\tBUILD\tSOURCE")
			for _, in := range infos {
				server, build, source := "down", "-", "-"
				if in.ServerAlive {
					server = "up"
				}
				if in.Meta != nil {
					build, source = in.Meta.BuildSHA, in.Meta.SourceWorktree
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", in.Name, server, build, source)
			}
			return tw.Flush()
		},
	}
}

func (a *app) downCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Kill the sandbox's tmux server and delete the sandbox",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			if err := sb.Down(); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "removed %s\n", sb.Dir)
			return nil
		},
	}
}

func parseSize(s string) (int, int, error) {
	ws, hs, ok := strings.Cut(s, "x")
	w, errW := strconv.Atoi(ws)
	h, errH := strconv.Atoi(hs)
	if !ok || errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("invalid size %q (want WIDTHxHEIGHT, e.g. 160x48)", s)
	}
	return w, h, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

- [ ] **Step 5: Implement `logs.go`**

Create `tools/loomdev/logs.go`:

```go
package main

import (
	"context"
	"io"
	"os"
	"time"
)

// followLogs streams bytes appended to files (tail -F style, starting at the
// current end) until ctx is canceled. A file that shrinks is treated as
// rotated and read from the start.
func followLogs(ctx context.Context, w io.Writer, files []string, poll time.Duration) error {
	offsets := make(map[string]int64, len(files))
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			offsets[f] = st.Size()
		}
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, f := range files {
				offsets[f] = copyNew(w, f, offsets[f])
			}
		}
	}
}

func copyNew(w io.Writer, path string, offset int64) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return offset
	}
	if st.Size() < offset {
		offset = 0
	}
	if st.Size() == offset {
		return offset
	}
	fh, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer func() { _ = fh.Close() }()
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	n, _ := io.Copy(w, fh)
	return offset + n
}
```

- [ ] **Step 6: Run the CLI tests**

Run: `gofmt -l tools && CGO_ENABLED=0 go test ./tools/... -v 2>&1 | tail -20 && CGO_ENABLED=0 go vet ./tools/... ./internal/...`
Expected: no gofmt output; all PASS; vet clean.

- [ ] **Step 7: Smoke the real flow end to end**

```bash
go run ./tools/loomdev --sandbox plan-smoke up
go run ./tools/loomdev -s plan-smoke start
go run ./tools/loomdev -s plan-smoke wait --text toy --timeout 30s
go run ./tools/loomdev -s plan-smoke shot | head -20
go run ./tools/loomdev -s plan-smoke ls
go run ./tools/loomdev -s plan-smoke stop
go run ./tools/loomdev -s plan-smoke down
```

Expected: `up` prints `sandbox plan-smoke ready at …/loom-dev/plan-smoke`; `wait` returns 0; `shot` shows the loom UI with the `toy` workspace terminal in the rail; `ls` shows `plan-smoke  up`; `down` prints `removed …`. Confirm the host is untouched: `tmux ls` (host server) lists the same sessions as before.

- [ ] **Step 8: Commit**

```bash
git add tools/loomdev
git commit -m "feat(tools): add loomdev, the dev sandbox CLI

up/build/run/start/stop/keys/shot/wait/logs/env/ls/down over
internal/devsandbox; --sandbox defaults to the branch leaf.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB"
```

---

### Task 8: End-to-end smoke suite

**Files:**
- Create: `e2e/e2e_test.go`

**Interfaces:**
- Consumes: `devsandbox.Open/Up/Build/Start/Stop/SendKeys/SendText/WaitFor/Screen/TailLogs/Down/Socket/DriverRunning/WorkspaceName/UpOptions/StartOptions` (Tasks 5–6); `tmux.CommandOnSocket`, `tmux.ToLoomTmuxName` (existing + Task 1).
- Produces: `go test -tags e2e ./e2e/...`.

UI facts the tests rely on (verified while planning):
- The workspace terminal is titled with the workspace name, so `toy` appears in the rail once loom is up.
- `n` starts inline title entry; `enter` opens the modal titled **Session Launch Options**; `enter` again starts the session.
- `a` opens the quick-input bar; `enter` sends the text plus a carriage return to the selected agent.
- A prompting session's rail card reads **❯ awaiting input**.
- `q` quits loom and leaves agent tmux sessions running.

- [ ] **Step 1: Write the suite**

Create `e2e/e2e_test.go`:

```go
//go:build e2e

// Package e2e drives a real dev build of loom inside an isolated sandbox.
// Run with: go test -tags e2e ./e2e/...
package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const uiTimeout = 30 * time.Second

func newSandbox(t *testing.T, profile string) *devsandbox.Sandbox {
	t.Helper()
	for _, bin := range []string{"tmux", "git", "go"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	sb, err := devsandbox.Open(fmt.Sprintf("e2e%d", time.Now().UnixNano()))
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			screen, _ := sb.Screen(false)
			t.Logf("last screen:\n%s\nsandbox logs:\n%s", screen, sb.TailLogs(40))
		}
		_ = sb.Down()
	})
	require.NoError(t, sb.Up(devsandbox.UpOptions{SourceWorktree: root, DefaultProfile: profile}))
	require.NoError(t, sb.Build(root))
	return sb
}

func startLoom(t *testing.T, sb *devsandbox.Sandbox) {
	t.Helper()
	require.NoError(t, sb.Start(devsandbox.StartOptions{}))
	require.NoError(t, sb.WaitFor(devsandbox.WorkspaceName, uiTimeout))
}

func createSession(t *testing.T, sb *devsandbox.Sandbox, title string) {
	t.Helper()
	require.NoError(t, sb.SendKeys("n"))
	require.NoError(t, sb.SendText(title))
	require.NoError(t, sb.SendKeys("Enter"))
	require.NoError(t, sb.WaitFor("Session Launch Options", uiTimeout))
	require.NoError(t, sb.SendKeys("Enter"))
	// The title is already on screen while typing, so wait on the agent's
	// tmux session and the fake agent's banner to know the start finished.
	require.Eventually(t, func() bool {
		return tmux.CommandOnSocket(context.Background(), sb.Socket(),
			"has-session", "-t="+tmux.ToLoomTmuxName(title)).Run() == nil
	}, uiTimeout, 200*time.Millisecond, "agent session for %q never started", title)
	require.NoError(t, sb.WaitFor("commands: work N", uiTimeout))
}

func TestE2E_SandboxLeavesOtherServersAlone(t *testing.T) {
	sb := newSandbox(t, "")
	ctx := context.Background()
	decoy := fmt.Sprintf("loomtest-decoy-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = tmux.CommandOnSocket(ctx, decoy, "kill-server").Run() })
	require.NoError(t, tmux.CommandOnSocket(ctx, decoy, "new-session", "-d", "-s", "loom_decoy", "sleep 300").Run())
	path, err := tmux.CommandOnSocket(ctx, decoy, "display-message", "-p", "-t", "loom_decoy", "#{socket_path}").Output()
	require.NoError(t, err)
	// Behave like a shell inside the decoy server: the driver's tmux client
	// (and the private server it starts) inherit this $TMUX.
	t.Setenv("TMUX", strings.TrimSpace(string(path))+",1,0")

	startLoom(t, sb) // startup has run the orphan sweep

	assert.NoError(t, tmux.CommandOnSocket(ctx, decoy, "has-session", "-t=loom_decoy").Run(),
		"a sandboxed loom must never sweep another server's loom_* sessions")
}

func TestE2E_FakeAiderPromptSurfacesAsAwaitingInput(t *testing.T) {
	sb := newSandbox(t, "fake-aider")
	startLoom(t, sb)
	createSession(t, sb, "asker")

	require.NoError(t, sb.SendKeys("a"))
	require.NoError(t, sb.SendText("ask"))
	require.NoError(t, sb.SendKeys("Enter"))

	require.NoError(t, sb.WaitFor("awaiting input", uiTimeout))
}

func TestE2E_SessionSurvivesRestart(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "keeper")

	require.NoError(t, sb.Stop(10*time.Second))
	require.False(t, sb.DriverRunning())
	ctx := context.Background()
	require.NoError(t, tmux.CommandOnSocket(ctx, sb.Socket(), "has-session", "-t="+tmux.ToLoomTmuxName("keeper")).Run(),
		"quitting loom must leave the agent session running")

	startLoom(t, sb)
	require.NoError(t, sb.WaitFor("keeper", uiTimeout))
}
```

- [ ] **Step 2: Confirm the default build ignores the suite**

Run: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./e2e/... 2>&1 | tail -3`
Expected: vet clean; the test run reports `build constraints exclude all Go files` (or `no test files`) — not a failure.

- [ ] **Step 3: Run the suite**

Run: `CGO_ENABLED=0 go test -tags e2e ./e2e/... -v -timeout 10m 2>&1 | tail -30`
Expected: three PASS.

If a `WaitFor` times out, the failure log prints the last screen and sandbox logs. Inspect interactively before changing anything:

```bash
go run ./tools/loomdev -s e2e-debug up --default-profile fake-aider
go run ./tools/loomdev -s e2e-debug start && go run ./tools/loomdev -s e2e-debug wait --text toy --timeout 30s
go run ./tools/loomdev -s e2e-debug keys n; go run ./tools/loomdev -s e2e-debug keys -l asker
go run ./tools/loomdev -s e2e-debug keys Enter; go run ./tools/loomdev -s e2e-debug shot
```

Fix the test's key sequence or wait text to match what the UI really shows; do not change product code to suit the test. Clean up with `go run ./tools/loomdev -s e2e-debug down`.

- [ ] **Step 4: Commit**

```bash
git add e2e/e2e_test.go
git commit -m "test(e2e): smoke-test a sandboxed loom end to end

Covers sweep isolation from an enclosing server, fake-aider prompt
detection, and session survival across a quit/restart.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB"
```

---

### Task 9: Project skill, docs, spec amendments

**Files:**
- Create: `.claude/skills/loom-dev/SKILL.md`
- Modify: `CLAUDE.md`, `USAGE.md` (Environment Variables table, ~line 691), `CONTRIBUTING.md`, `docs/superpowers/specs/2026-09-16-dev-sandbox-design.md`

**Interfaces:**
- Consumes: the CLI surface from Task 7 and the env vars from Tasks 1–3.
- Produces: documentation only.

- [ ] **Step 1: Write the project skill**

Create `.claude/skills/loom-dev/SKILL.md`:

````markdown
---
name: loom-dev
description: Launch, drive, and screenshot a dev build of loom safely from inside loom. Use whenever you need to run loom itself — to see a change working in the real TUI or to reproduce a UI bug. Never run ./loom or `go run .` directly in a loom pane.
---

# Loom dev sandbox

Loom's startup orphan sweep kills every unclaimed `loom_*` tmux session on
the server it talks to. Inside a loom pane that is the host's server, so the
binary refuses to start there (nesting guard). Use the sandbox instead: a
private tmux socket, a private registry, and a toy workspace named `toy`.

Run everything from the repo root as `go run ./tools/loomdev <cmd>`. The
sandbox is named after the current branch's leaf; `--sandbox NAME` (`-s`)
picks another.

## Verify a change headlessly

1. `go run ./tools/loomdev up` — create or refresh the sandbox and build it (idempotent).
2. `go run ./tools/loomdev start --restart` — run the dev build in the driver session (160×48).
3. `go run ./tools/loomdev wait --text toy` — wait until the UI is up.
4. Drive it with tmux key names — `keys n`, `keys Enter`, `keys Escape`, `keys Up`, `keys C-c` — and type text with `keys -l some text`.
5. `go run ./tools/loomdev shot` prints the screen (`--ansi` keeps colors). Use `wait --text` instead of sleeping.
6. `go run ./tools/loomdev stop` when done; `down` deletes the sandbox.

On a `wait` timeout the tool prints the last screen and the sandbox log
tail. Read them before retrying. `logs -f` follows the logs.

## Recipes

- New session: `keys n` → `keys -l <title>` → `keys Enter` (Session Launch Options) → `keys Enter`.
- Send text to the selected agent: `keys a` → `keys -l <text>` → `keys Enter`.
- Fake agent commands (send them as agent text): `work N`, `ask`, `trust`, `bell`, `title X`, `edit`, `commit`, `crash`, `exit`.
- Profiles: `fake` (default), `fake-claude`, `fake-aider`, `shell`; `up --real-claude` adds the real `claude` (costs tokens). Change the default with `up --default-profile fake-aider`.
- Restore path: `stop`, then `start`; sessions persist on the private tmux server.
- Interactive, for a human: `go run ./tools/loomdev run`. The host loom intercepts `ctrl+q` and double-`esc`, so test the dev loom's interact-exit from a plain OS terminal.
- End-to-end suite: `go test -tags e2e ./e2e/...`.

## Rules

- Never run `./loom`, `go run .`, or `loom reset` directly in a loom pane.
- Never run `clean.sh` / `clean_hard.sh` from inside loom (they refuse anyway).
- Fix the product, not the sandbox, when the UI misbehaves; fix the test's key sequence when the UI is right.
````

- [ ] **Step 2: Update `CLAUDE.md`**

In **Build & Development Commands**, directly after the `./clean_hard.sh` line inside the code block, change the two cleanup comments and add the sandbox block so that part reads:

```bash
# Cleanup scripts (refuse to run inside a loom-managed tmux session)
./clean.sh        # Kill tmux server, remove worktrees and ~/.loom/
./clean_hard.sh   # Same as clean.sh + git worktree prune

# Dev sandbox — run a dev build safely from inside loom (.claude/skills/loom-dev)
go run ./tools/loomdev up                 # create + build (named after the branch leaf)
go run ./tools/loomdev run                # interactive, in this terminal
go run ./tools/loomdev start              # headless, then: wait --text toy / keys … / shot
go run ./tools/loomdev down               # delete the sandbox and its tmux server
go test -tags e2e ./e2e/...               # end-to-end smoke suite (needs tmux)
```

In **Environment Variables**, after the `LOOM_PANE_RENDERER` bullet, add:

```markdown
- `LOOM_TMUX_SOCKET` — Private tmux socket name: every tmux invocation gets `-L <name>` (via `tmux.Command`), overriding `$TMUX`. Used by the dev sandbox.
- `LOOM_GLOBAL_DIR` — Relocates `GetGlobalConfigDir()` (workspace registry + global context), which ignores `LOOM_HOME` by design. Absolute; supports `~`. Also disables the legacy-home migration.
- `LOOM_ALLOW_NESTED` — Set to `1` to bypass the nesting guard (see Gotchas).
```

In **Key Packages**, after the `log/` bullet, add:

```markdown
- **`internal/devsandbox/`** — Dev sandboxes for developing loom inside loom: `Up` (toy repo + bare origin, sandbox config/state seeded into both `global/` and `repo/.loom/`, registry entry), `Build`, and a headless driver (`Start`/`SendKeys`/`Screen`/`WaitFor`) on a private tmux socket. CLI: `tools/loomdev`; deterministic agent stand-in: `tools/fakeagent` (persona from `argv[0]` via the adapter registry). `tools/` is excluded from the Nix package.
```

In **Gotchas**, add two bullets at the end of the list:

```markdown
- **Every tmux exec goes through `tmux.Command`/`tmux.CommandOnSocket`.** They honor `LOOM_TMUX_SOCKET`; a raw `exec.Command("tmux", …)` would follow `$TMUX` to whatever server encloses the process. `TestNoRawTmuxExec` fails the build on any raw tmux exec outside `session/tmux/command.go` (whose `EnclosingSessionName` is the one deliberate exception).
- **The nesting guard exists because the orphan sweep is server-wide.** `CleanupOrphanedSessions` kills every `loom_*` session the process didn't load, on the server it talks to. `main.go`'s `nestingCheck` refuses the TUI and `reset` inside a `loom_*`/`claudesquad_*` session unless `LOOM_TMUX_SOCKET` is set (or `LOOM_ALLOW_NESTED=1`). Run dev builds through `tools/loomdev`, never directly in a pane.
```

- [ ] **Step 3: Update `USAGE.md` and `CONTRIBUTING.md`**

In `USAGE.md`'s **Environment Variables** table, add rows below `LOOM_HOME`:

```markdown
| `LOOM_TMUX_SOCKET` | Run all of loom's tmux commands against a private server (`tmux -L <name>`). |
| `LOOM_GLOBAL_DIR` | Override the directory holding `workspaces.json` (default: `~/.loom`). Absolute path; supports `~`. |
| `LOOM_ALLOW_NESTED` | Set to `1` to start loom inside one of its own tmux sessions anyway (normally refused, because startup cleanup would kill the enclosing loom's sessions). |
```

In `CONTRIBUTING.md`, after the **Development Setup** list, add:

````markdown
### Dev loop

Run your build through the dev sandbox rather than directly — especially from inside loom, where a bare `./loom` is refused:

```bash
go run ./tools/loomdev up      # create + build a sandbox named after your branch
go run ./tools/loomdev run     # try it interactively
go run ./tools/loomdev down    # clean up
```

`go test -tags e2e ./e2e/...` runs the end-to-end smoke suite (requires tmux).
````

- [ ] **Step 4: Amend the spec**

In `docs/superpowers/specs/2026-09-16-dev-sandbox-design.md`:
1. Change **Status** to `Approved design (amended during planning — see "Planning amendments")`.
2. Append this section at the end:

```markdown
## Planning amendments

Discovered while writing `docs/superpowers/plans/2026-09-16-dev-sandbox.md`:

1. **Nix:** `excludedPackages = [ "tools" ]` replaces `subPackages = [ "." ]`; `buildGoModule` uses the same directory list for `checkPhase`, so `subPackages` would drop every other package's tests.
2. **Config roots:** the global context's `ConfigDir` is `GetGlobalConfigDir()` (not `LOOM_HOME`) and `--workspace toy` reads `repo/.loom/`, so `config.json` and `state.json` are seeded into `global/` and `repo/.loom/`; `home/` only receives startup logs. `default_program` is the profile name `fake`; `branch_prefix` is `dev/`; `state.json` marks every help screen seen so first-run overlays never block the driver.
3. **Registration:** `Up` writes `global/workspaces.json` through `config.WorkspaceRegistry` (read back via `config.LoadWorkspaceRegistry` in tests) instead of invoking `loom workspace add`.
4. **CLI:** the sandbox is chosen with a persistent `--sandbox/-s` flag (default: branch leaf) instead of positional names; `keys -l` types literal text; `up` also builds and takes `--default-profile`; `run`/`start` take `--no-build`.
5. **fakeagent:** adds `trust` and `exit`; prompt texts come from loom's adapter registry; answered prompts clear the screen.
6. **Driver:** `status off` is set server-wide on the private socket before `new-session` (exact pane size; loom disables it on its own sessions anyway); window-level `remain-on-exit on` applies to the driver window only.
```

- [ ] **Step 5: Final verification**

Run:

```bash
gofmt -l . | grep -v '^vendor/'
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go build -o /dev/null .
CGO_ENABLED=0 go test ./... 2>&1 | tail -25
```

Expected: no gofmt output; vet clean; build OK; every package `ok` (re-run `TestScrollbackAccumulation_RealTmux` alone if it flakes). If a C compiler is available, also run `CGO_ENABLED=1 go test -race ./session/... ./internal/... ./tools/...`.

- [ ] **Step 6: Commit**

```bash
git add .claude/skills/loom-dev/SKILL.md CLAUDE.md USAGE.md CONTRIBUTING.md docs/superpowers/specs/2026-09-16-dev-sandbox-design.md
git commit -m "docs: document the dev sandbox and its isolation knobs

Adds the loom-dev project skill, env var docs, CLAUDE.md gotchas, a
contributor dev loop, and the planning amendments to the spec.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019aCg9hAEr1eBeK6qh5KoQB"
```
