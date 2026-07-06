# Headroom Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Headroom Wrap feature's command-string rewrite (`claude` → `headroom wrap claude`) with an `ANTHROPIC_BASE_URL` environment variable set only on the tmux session, per `docs/superpowers/specs/2026-07-06-headroom-proxy-design.md`.

**Architecture:** `tmux new-session -e KEY=VALUE` sets a per-session env var without touching the command tmux execs. A new `session.HeadroomProxyEnv(enabled, program)` replaces `BuildHeadroomWrapCommand`; a new persisted `Instance.HeadroomProxy bool` field (plus matching `InstanceData`/schema-version/config/UI renames) tracks the toggle since it no longer lives inside `Program`. Everywhere that used to strip a `headroom wrap` prefix from `program` (adapter matching, recovery-flag injection, remote-control auth) goes back to parsing `program` directly.

**Tech Stack:** Go 1.23, tmux 3.2+ (`-e` flag on `new-session`), testify/assert.

---

## Task 1: tmux per-session environment variables

**Files:**
- Modify: `session/tmux/tmux.go`
- Test: `session/tmux/tmux_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/tmux/tmux_test.go` (place it directly after `TestStartTmuxSession`, which ends around line 296):

```go
func TestStartTmuxSessionWithEnv(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)

	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session already exists")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("output"), nil
		},
	}

	workdir := t.TempDir()
	session := newTmuxSession("test-session", "claude", ptyFactory, cmdExec, "ANTHROPIC_BASE_URL=http://127.0.0.1:8787")

	err := session.Start(workdir)
	require.NoError(t, err)
	require.Equal(t, 2, len(ptyFactory.cmds))
	require.Equal(t,
		fmt.Sprintf("tmux new-session -d -s loom_test-session -c %s -e ANTHROPIC_BASE_URL=http://127.0.0.1:8787 claude", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
}

func TestStartTmuxSessionNoEnvUnchanged(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)

	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session already exists")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("output"), nil
		},
	}

	workdir := t.TempDir()
	session := newTmuxSession("test-session", "claude", ptyFactory, cmdExec)

	err := session.Start(workdir)
	require.NoError(t, err)
	require.Equal(t,
		fmt.Sprintf("tmux new-session -d -s loom_test-session -c %s claude", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./session/tmux/... -run 'TestStartTmuxSessionWithEnv|TestStartTmuxSessionNoEnvUnchanged' -v`
Expected: `TestStartTmuxSessionWithEnv` FAILS to compile — `newTmuxSession` doesn't accept a 5th argument yet. `TestStartTmuxSessionNoEnvUnchanged` would pass once compiling, since it doesn't need the new behavior; both fail together right now because the package won't build.

- [ ] **Step 3: Add the `env` field and thread it through the three constructors**

In `session/tmux/tmux.go`, add `env []string` to the struct (`session/tmux/tmux.go:61-70`):

```go
type TmuxSession struct {
	// Initialized by NewTmuxSession
	//
	// The name of the tmux session and the sanitized name used for tmux commands.
	sanitizedName string
	program       string
	// env holds "KEY=VALUE" entries applied to the tmux session via
	// `new-session -e` — e.g. ANTHROPIC_BASE_URL when Headroom Proxy is
	// enabled. Scoped to just this session; never touches t.program.
	env []string
	// ptyFactory is used to create a PTY for the tmux session.
	ptyFactory PtyFactory
	// cmdExec is used to execute commands in the tmux session.
	cmdExec internalexec.Executor
```

Update the three constructors (`session/tmux/tmux.go:191-222`) to make `env` variadic at every level, so every existing call site (2-arg `NewTmuxSession`, 4-arg `NewTmuxSessionWithDeps`, 4-arg `newTmuxSession`) keeps compiling unchanged:

```go
// NewTmuxSession constructs a TmuxSession wired to the production PTY
// factory and subprocess executor. The tmux session is NOT created at
// this point — call Start (for a fresh session) or Restore (to attach
// to one that already exists on disk). env, if given, is a set of
// "KEY=VALUE" pairs applied to the tmux session via `new-session -e`.
func NewTmuxSession(name string, program string, env ...string) *TmuxSession {
	return newTmuxSession(name, program, MakePtyFactory(), internalexec.Default{}, env...)
}

// NewTmuxSessionWithDeps is [NewTmuxSession] with injected dependencies
// for tests. Pass a fake [PtyFactory] and [internalexec.Executor] to
// avoid spawning real subprocesses or allocating real PTYs.
func NewTmuxSessionWithDeps(name string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *TmuxSession {
	return newTmuxSession(name, program, ptyFactory, cmdExec, env...)
}

func newTmuxSession(name string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *TmuxSession {
	return &TmuxSession{
		sanitizedName: ToLoomTmuxName(name),
		program:       program,
		env:           env,
		ptyFactory:    ptyFactory,
		cmdExec:       cmdExec,
		// monitor is always non-nil for the session's lifetime so HasUpdated
		// and CaptureAndProcess can read it without a guard. Restore reassigns
		// a fresh instance on every PTY attach, so the initial value is only
		// load-bearing for paused sessions (constructed without Restore).
		monitor: newStatusMonitor(),
		// Default geometry until the first SetDetachedSize; a fresh emulator in
		// Restore starts here so it is never zero-sized.
		lastCols: 80,
		lastRows: 24,
	}
}
```

- [ ] **Step 4: Emit `-e KEY=VALUE` args in `Start()`**

In `session/tmux/tmux.go`, replace the `cmd := exec.CommandContext(...)` line inside `Start` (`session/tmux/tmux.go:247`):

```go
	// Create a new detached tmux session and start claude in it.
	// tmuxStartTimeout allows the agent process's initial exec before tmux
	// returns control; tmux itself is quick, but the wrapped program may not be.
	startCtx, startCancel := context.WithTimeout(context.Background(), tmuxStartTimeout)
	defer startCancel()
	args := []string{"new-session", "-d", "-s", t.sanitizedName, "-c", workDir}
	for _, e := range t.env {
		args = append(args, "-e", e)
	}
	args = append(args, t.program)
	cmd := exec.CommandContext(startCtx, "tmux", args...)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./session/tmux/... -run 'TestStartTmuxSessionWithEnv|TestStartTmuxSessionNoEnvUnchanged' -v`
Expected: both PASS.

- [ ] **Step 6: Run the full tmux package test suite (regression check)**

Run: `go test ./session/tmux/...`
Expected: PASS (no other test's expected command string changes, since none of them pass `env`).

- [ ] **Step 7: Commit**

```bash
git add session/tmux/tmux.go session/tmux/tmux_test.go
git commit -m "$(cat <<'EOF'
feat(tmux): support per-session env vars via new-session -e

Lets a caller set environment variables scoped to just one tmux
session (e.g. ANTHROPIC_BASE_URL for Headroom Proxy) without touching
the command tmux execs. Variadic at every constructor layer so no
existing call site needs to change.
EOF
)"
```

---

## Task 2: Remove headroom-wrap-aware parsing from adapter matching, recovery, and remote-control auth

**Files:**
- Modify: `session/agent/adapter.go`
- Modify: `session/agent/claude.go`
- Modify: `session/remote_control_auth.go`
- Test: `session/agent/adapter_test.go`
- Test: `session/remote_control_auth_test.go`

This task deletes the `SplitHeadroomWrap`/`headroomWrapPrefix` machinery and every place that called it, in dependency order (callers first, then the definition), so the package compiles at every step.

- [ ] **Step 1: Stop `ApplyRecoveryFlag` from stripping a headroom-wrap prefix**

In `session/agent/claude.go`, replace `ApplyRecoveryFlag` (`session/agent/claude.go:39-54`):

```go
// ApplyRecoveryFlag inserts --continue after "claude", preserving the
// original program's remaining flags. Returns program unchanged if
// --continue or --resume is already present.
func (claudeAdapter) ApplyRecoveryFlag(program string) string {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}
	for _, p := range parts[1:] {
		if p == "--continue" || p == "--resume" {
			return program
		}
	}
	return parts[0] + " --continue" + strings.TrimPrefix(program, parts[0])
}
```

Delete the now-obsolete comment block above `ApplyRemoteControlFlag` (`session/agent/claude.go:63-69`) — it explained why those methods were "deliberately NOT headroom-wrap-aware", a distinction that no longer exists since nothing rewrites `program` anymore:

```go
// ApplyRemoteControlFlag, ApplyPermissionModeFlag, and ApplyModelFlag
// (below) are deliberately NOT headroom-wrap-aware, unlike
// ApplyRecoveryFlag above: app/remote_control.go's applyLaunchOptions
// always composes Headroom Wrap last/outermost, so none of these three
// ever see an already-wrapped program in this codebase's call graph.
// If that composition order ever changes, these three would need the
// same splitHeadroomWrap treatment ApplyRecoveryFlag already has.
```

(Delete the whole comment block; leave a blank line before `// ApplyRemoteControlFlag inserts...`, the doc comment on the function itself.)

- [ ] **Step 2: Remove the now-obsolete headroom-wrap test case in `adapter_test.go`**

In `session/agent/adapter_test.go`, delete `TestClaudeRecoveryFlagThroughHeadroomWrap` (`session/agent/adapter_test.go:57-62`):

```go
func TestClaudeRecoveryFlagThroughHeadroomWrap(t *testing.T) {
	c := Claude()
	assert.Equal(t, "headroom wrap claude --continue", c.ApplyRecoveryFlag("headroom wrap claude"))
	assert.Equal(t, "headroom wrap claude --continue --model opus", c.ApplyRecoveryFlag("headroom wrap claude --model opus"))
	assert.Equal(t, "headroom wrap claude --continue", c.ApplyRecoveryFlag("headroom wrap claude --continue"))
}
```

(Delete this whole function — remove it entirely, don't replace it with anything. Loom never produces a `headroom wrap`-prefixed `program` anymore, so there's nothing for `ApplyRecoveryFlag` to be tested against here.)

- [ ] **Step 3: Run the `session/agent` package tests**

Run: `go test ./session/agent/... -v`
Expected: PASS. `TestClaudeRecoveryFlag` (the plain-parsing cases) still passes unchanged; `TestDefaultRegistryLookupThroughHeadroomWrap` still passes too (adapter.go hasn't changed yet — that's Step 6 below).

- [ ] **Step 4: Stop `DetectClaudeRemoteControlAuth` from stripping a headroom-wrap prefix**

In `session/remote_control_auth.go`, replace the two lines that split the prefix (`session/remote_control_auth.go:94-95`):

```go
	fields := strings.Fields(program)
```

Remove the now-unused import `"github.com/aidan-bailey/loom/session/agent"` from the import block at the top of the file (`session/remote_control_auth.go:1-13`) — it was only used for this one call:

```go
package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)
```

- [ ] **Step 5: Delete the now-obsolete headroom-wrap test in `remote_control_auth_test.go`**

In `session/remote_control_auth_test.go`, delete `TestDetectClaudeRemoteControlAuth_HeadroomWrap` (`session/remote_control_auth_test.go:80-87`):

```go
func TestDetectClaudeRemoteControlAuth_HeadroomWrap(t *testing.T) {
	clearOverrideEnv(t)
	fake := &fakeAuthExecutor{out: []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`)}
	got := DetectClaudeRemoteControlAuth("headroom wrap claude --model opus", fake)
	assert.Equal(t, RemoteControlAuthOK, got.State)
	assert.True(t, fake.called)
	assert.Equal(t, "claude", fake.gotArgs[0])
}
```

(Delete this whole function.)

- [ ] **Step 6: Run the `session` package tests**

Run: `go test ./session/... -run TestDetectClaudeRemoteControlAuth -v`
Expected: PASS (remaining `TestDetectClaudeRemoteControlAuth` and `TestDetectClaudeRemoteControlAuth_EnvOverride` cases still work — `DetectClaudeRemoteControlAuth` never saw a wrapped prefix in those cases anyway).

- [ ] **Step 7: Delete `SplitHeadroomWrap`/`headroomWrapPrefix` and simplify `firstField`**

In `session/agent/adapter.go`, delete the constant and function (`session/agent/adapter.go:108-126`):

```go
// headroomWrapPrefix is the literal prefix BuildHeadroomWrapCommand
// (session/agent_restart.go) adds to a fully-composed program string
// when the Headroom Wrap setting is enabled. Adapter matching and
// flag insertion both need to see through it to find the real agent
// invocation underneath — otherwise a headroom-wrapped session stops
// being recognized as (and correctly modified as) whatever agent it
// actually wraps, breaking trust-prompt detection, prompting-status
// detection, and crash-recovery's --continue injection.
const headroomWrapPrefix = "headroom wrap "

// SplitHeadroomWrap separates a leading headroomWrapPrefix from the
// rest of program, if present. prefix is "" when program isn't
// wrapped.
func SplitHeadroomWrap(program string) (prefix, rest string) {
	if strings.HasPrefix(program, headroomWrapPrefix) {
		return headroomWrapPrefix, strings.TrimPrefix(program, headroomWrapPrefix)
	}
	return "", program
}
```

(Delete this whole block — both the const and the function, plus their doc comments.)

Replace `firstField` (`session/agent/adapter.go:128-138`):

```go
// firstField returns the first whitespace-separated token of program,
// or the empty string if program is all whitespace.
func firstField(program string) string {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
```

Update the doc comment on `basenameMatch` (`session/agent/adapter.go:140-144`) to drop the now-inaccurate "after stripping any headroom-wrap prefix" clause:

```go
// basenameMatch reports whether the program's first token has the
// given basename (after path stripping). Absolute paths like
// /nix/store/.../bin/claude still match "claude".
func basenameMatch(program, name string) bool {
	first := firstField(program)
	if first == "" {
		return false
	}
	return filepath.Base(first) == name
}
```

- [ ] **Step 8: Delete the now-obsolete headroom-wrap lookup test**

In `session/agent/adapter_test.go`, delete `TestDefaultRegistryLookupThroughHeadroomWrap` (`session/agent/adapter_test.go:64-69`):

```go
func TestDefaultRegistryLookupThroughHeadroomWrap(t *testing.T) {
	r := DefaultRegistry()
	assert.Equal(t, "claude", r.Lookup("headroom wrap claude").Name())
	assert.Equal(t, "claude", r.Lookup("headroom wrap claude --model opus").Name())
	assert.Equal(t, "aider", r.Lookup("headroom wrap aider --model gemma").Name())
}
```

(Delete this whole function — after this task, `Lookup("headroom wrap claude")` resolves to `"default"`, not `"claude"`, since nothing produces that prefix anymore and adapter matching no longer special-cases it.)

- [ ] **Step 9: Run the full `session/agent` and `session` package test suites**

Run: `go test ./session/agent/... ./session/... -v 2>&1 | tail -60`
Expected: PASS. No remaining references to `SplitHeadroomWrap` or `headroomWrapPrefix` anywhere.

Run: `grep -rn "SplitHeadroomWrap\|headroomWrapPrefix" --include="*.go" .`
Expected: no output.

- [ ] **Step 10: Commit**

```bash
git add session/agent/adapter.go session/agent/claude.go session/agent/adapter_test.go session/remote_control_auth.go session/remote_control_auth_test.go
git commit -m "$(cat <<'EOF'
refactor(agent): stop parsing a headroom-wrap prefix out of program

Adapter matching, recovery-flag injection, and remote-control auth
detection go back to parsing `program` directly. Env-var-based
Headroom Proxy (next commit) never rewrites program, so there's
nothing left to strip.
EOF
)"
```

---

## Task 3: `HeadroomProxyEnv` replaces `BuildHeadroomWrapCommand`

**Files:**
- Modify: `session/agent_restart.go`
- Test: `session/agent_restart_test.go`

- [ ] **Step 1: Write the failing tests**

In `session/agent_restart_test.go`, replace the `TestBuildHeadroomWrapCommand_*` block (`session/agent_restart_test.go:148-184`) with:

```go
func TestHeadroomProxyEnv_DisabledIsNoOp(t *testing.T) {
	assert.Nil(t, HeadroomProxyEnv(false, "claude"))
}

func TestHeadroomProxyEnv_EnabledClaude(t *testing.T) {
	assert.Equal(t, []string{"ANTHROPIC_BASE_URL=http://127.0.0.1:8787"}, HeadroomProxyEnv(true, "claude"))
}

func TestHeadroomProxyEnv_EnabledClaudeWithFlags(t *testing.T) {
	assert.Equal(t, []string{"ANTHROPIC_BASE_URL=http://127.0.0.1:8787"}, HeadroomProxyEnv(true, "claude --model opus"))
}

func TestHeadroomProxyEnv_EnabledClaudeAbsolutePath(t *testing.T) {
	assert.Equal(t,
		[]string{"ANTHROPIC_BASE_URL=http://127.0.0.1:8787"},
		HeadroomProxyEnv(true, "/etc/profiles/per-user/aidanb/bin/claude --model sonnet"),
	)
}

func TestHeadroomProxyEnv_EnabledNonClaudeIsNoOp(t *testing.T) {
	assert.Nil(t, HeadroomProxyEnv(true, "aider --model gemma"))
	assert.Nil(t, HeadroomProxyEnv(true, "gemini"))
	assert.Nil(t, HeadroomProxyEnv(true, "codex"))
}

func TestHeadroomProxyEnv_EmptyProgramIsNoOp(t *testing.T) {
	assert.Nil(t, HeadroomProxyEnv(true, ""))
}
```

Also delete the now-obsolete `TestBuildRecoveryCommand_ThroughHeadroomWrap` case (`session/agent_restart_test.go:25-27`):

```go
func TestBuildRecoveryCommand_ThroughHeadroomWrap(t *testing.T) {
	assert.Equal(t, "headroom wrap claude --continue", BuildRecoveryCommand("headroom wrap claude"))
}
```

(Delete this whole function — `BuildRecoveryCommand` no longer sees headroom-wrapped input.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./session/... -run TestHeadroomProxyEnv -v`
Expected: FAIL to compile — `HeadroomProxyEnv` is not defined yet (`BuildHeadroomWrapCommand` still exists but is about to be deleted in the next step).

- [ ] **Step 3: Replace `BuildHeadroomWrapCommand`/`headroomSupportedTools` with `HeadroomProxyEnv`**

In `session/agent_restart.go`, replace the block from the `headroomSupportedTools` doc comment through the end of `BuildHeadroomWrapCommand` (`session/agent_restart.go:49-86`):

```go
// HeadroomProxyURL is the base URL Loom points ANTHROPIC_BASE_URL at
// when the Headroom Proxy launch option is enabled — Headroom's own
// default proxy address (`headroom proxy`, port 8787). Loom does not
// start or manage the proxy process itself; the user is expected to
// have it running already (e.g. via `headroom install`'s persistent
// deployment).
const HeadroomProxyURL = "http://127.0.0.1:8787"

// HeadroomProxyEnv returns the tmux session environment variables
// needed to route program's API calls through Headroom's proxy. A
// no-op (nil) unless enabled and program resolves to Claude —
// ANTHROPIC_BASE_URL is Anthropic-API-specific, so setting it for any
// other agent wouldn't do anything useful.
func HeadroomProxyEnv(enabled bool, program string) []string {
	if !enabled || !IsClaudeProgram(program) {
		return nil
	}
	return []string{"ANTHROPIC_BASE_URL=" + HeadroomProxyURL}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./session/... -run 'TestHeadroomProxyEnv|TestBuildRecoveryCommand' -v`
Expected: PASS.

- [ ] **Step 5: Confirm no remaining references to the deleted function**

Run: `grep -rn "BuildHeadroomWrapCommand\|headroomSupportedTools" --include="*.go" .`
Expected: no output (the only other caller, `app/remote_control.go`'s `headroomWrapProgram`, is removed in Task 9).

Note: this will actually still show a hit in `app/remote_control.go` at this point in the plan — that's expected and fixed in Task 9. Confirm the only hit is there:

Run: `grep -rln "BuildHeadroomWrapCommand" --include="*.go" .`
Expected: `./app/remote_control.go` only.

- [ ] **Step 6: Run the full `session` package test suite**

Run: `go test ./session/... -v 2>&1 | tail -40`
Expected: PASS (the package itself compiles fine even with `app/remote_control.go` temporarily broken — different package, fixed in Task 9. `go build ./session/...` succeeds; `go build ./...` will fail until Task 9 — that's fine, this is a multi-task refactor).

- [ ] **Step 7: Commit**

```bash
git add session/agent_restart.go session/agent_restart_test.go
git commit -m "$(cat <<'EOF'
feat(session): add HeadroomProxyEnv, delete BuildHeadroomWrapCommand

Replaces the headroom-wrap command-string rewrite with a function that
returns the ANTHROPIC_BASE_URL env var for Claude sessions. app package
callers are updated in a later commit — this repo-wide refactor
proceeds bottom-up through its package dependency graph.
EOF
)"
```

---

## Task 4: `InstanceData` schema bump (v2 → v3) for `HeadroomProxy`

**Files:**
- Modify: `session/storage.go`
- Modify: `session/storage_migrate.go`
- Test: `session/storage_migrate_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/storage_migrate_test.go`, after `TestMigrate_V1UpgradesDropsAutoYes`:

```go
// TestMigrate_V2UpgradesAddsHeadroomProxy verifies a v2 record with no
// headroom_proxy key migrates to v3 with HeadroomProxy defaulting to
// false — the zero value already matches the desired default, so this
// is a pure version-stamp upgrade.
func TestMigrate_V2UpgradesAddsHeadroomProxy(t *testing.T) {
	raw := []byte(`{"schema_version":2,"title":"legacy","program":"claude"}`)

	data, err := Migrate(raw)
	assert.NoError(t, err)
	assert.Equal(t, CurrentSchemaVersion, data.SchemaVersion)
	assert.False(t, data.HeadroomProxy)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./session/... -run TestMigrate_V2UpgradesAddsHeadroomProxy -v`
Expected: FAIL — `CurrentSchemaVersion` is still 2, so a record already carrying `"schema_version":2` never enters the migration loop and `data.SchemaVersion` comes back as `2`, not `3`.

- [ ] **Step 3: Add the `HeadroomProxy` field to `InstanceData`**

In `session/storage.go`, add the field to the struct (`session/storage.go:44-47`):

```go
	Program             string          `json:"program"`
	HeadroomProxy       bool            `json:"headroom_proxy,omitempty"`
	Worktree            GitWorktreeData `json:"worktree"`
	DiffStats           DiffStatsData   `json:"diff_stats"`
	IsWorkspaceTerminal bool            `json:"is_workspace_terminal"`
```

Bump the schema version (`session/storage.go:25`):

```go
const CurrentSchemaVersion = 3
```

- [ ] **Step 4: Add the v2→v3 migration case**

In `session/storage_migrate.go`, add a new case to the switch (`session/storage_migrate.go:36-47`):

```go
		switch data.SchemaVersion {
		case 0:
			// v0 → v1: no payload changes. Just stamp the version so
			// future decodes skip this branch.
			data.SchemaVersion = 1
		case 1:
			// v1 → v2: AutoYes removed. No payload changes needed —
			// unmarshal already dropped the field — just stamp the version.
			data.SchemaVersion = 2
		case 2:
			// v2 → v3: HeadroomProxy added. No payload changes needed —
			// the zero value (false) already matches the desired default
			// for pre-existing records — just stamp the version.
			data.SchemaVersion = 3
		default:
			return InstanceData{}, fmt.Errorf("no upgrade path from schema version %d", data.SchemaVersion)
		}
```

Update the doc comment above `Migrate` (`session/storage_migrate.go:8-24`) so its description of the version history stays accurate:

```go
// Migrate decodes a single raw JSON object representing a stored instance
// and upgrades it to CurrentSchemaVersion. The function is idempotent: a
// record already at CurrentSchemaVersion round-trips unchanged.
//
// v0 records (missing SchemaVersion field → decodes to 0) are treated as
// pre-versioning. The v0→v1 step is a pure field-default upgrade and
// exists to establish the migration plumbing. v1→v2 drops the AutoYes
// field (encoding/json already ignores the now-unknown "auto_yes" key
// on unmarshal, so this step too is just a version stamp). v2→v3 adds
// HeadroomProxy, defaulting to false for pre-existing records — again
// just a version stamp, since the zero value is already correct.
//
// Contributor protocol: when adding/renaming/removing an InstanceData
// field, bump CurrentSchemaVersion and append a new case to the switch
// that upgrades from the previous version. The JSON fixture in
// cmd/workspace_migrate_shape_test.go is a drift guard for the
// `workspace migrate` CLI's typed mirror struct and must be updated in
// the same commit.
func Migrate(raw []byte) (InstanceData, error) {
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./session/... -run TestMigrate_V2UpgradesAddsHeadroomProxy -v`
Expected: PASS.

- [ ] **Step 6: Run the full storage/migration test suite**

Run: `go test ./session/... -run 'TestMigrate|TestStorage' -v 2>&1 | tail -60`
Expected: PASS. `TestMigrate_Idempotent` still passes since it constructs `InstanceData{SchemaVersion: CurrentSchemaVersion, ...}` symbolically.

Note: `go build ./cmd/...` will fail after this step until Task 5 updates `migrationInstance` — that's expected (`TestMigrationInstance_TypeDriftGuard` in `cmd/workspace_migrate_shape_test.go` will fail to reflect-match). Don't run `go test ./...` yet; scope this task's verification to `./session/...`.

- [ ] **Step 7: Commit**

```bash
git add session/storage.go session/storage_migrate.go session/storage_migrate_test.go
git commit -m "$(cat <<'EOF'
feat(session): add InstanceData.HeadroomProxy, bump schema to v3

Persists the per-instance Headroom Proxy toggle so pause/resume and
crash recovery (which construct a brand new TmuxSession) still apply
it. Pure version-stamp migration — the bool zero value already matches
the desired default for pre-existing records.
EOF
)"
```

---

## Task 5: Update the `cmd` package's `InstanceData` mirror

**Files:**
- Modify: `cmd/workspace_migrate.go`
- Test: `cmd/workspace_migrate_shape_test.go`

- [ ] **Step 1: Update the JSON fixture (failing-test-first for the shape guard)**

In `cmd/workspace_migrate_shape_test.go`, add `"headroom_proxy"` to the fixture (`cmd/workspace_migrate_shape_test.go:19-42`):

```go
	src := `{
		"schema_version": 1,
		"title": "t",
		"path": "/p",
		"branch": "b",
		"status": 2,
		"height": 10,
		"width": 20,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z",
		"program": "claude",
		"headroom_proxy": true,
		"worktree": {
			"repo_path": "/r",
			"worktree_path": "/wt",
			"session_name": "t",
			"branch_name": "b",
			"base_commit_sha": "abc",
			"is_existing_branch": false
		},
		"diff_stats": {
			"added": 1,
			"removed": 2,
			"content": "x"
		},
		"is_workspace_terminal": false
	}`
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/... -run TestMigrationInstance -v`
Expected: FAIL. `TestMigrationInstance_MirrorsInstanceData_JSON` fails because `migrationInstance` doesn't have a `HeadroomProxy`/`headroom_proxy` field yet, so it round-trips without that key and the map comparison mismatches. `TestMigrationInstance_TypeDriftGuard` also fails — `session.InstanceData` now has a field `migrationInstance` doesn't mirror.

- [ ] **Step 3: Add the matching field to `migrationInstance`**

In `cmd/workspace_migrate.go`, add the field (`cmd/workspace_migrate.go:33-38`):

```go
	Program             string                `json:"program"`
	HeadroomProxy       bool                  `json:"headroom_proxy,omitempty"`
	Worktree            migrationWorktreeData `json:"worktree"`
	DiffStats           migrationDiffStats    `json:"diff_stats"`
	IsWorkspaceTerminal bool                  `json:"is_workspace_terminal"`
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/... -run TestMigrationInstance -v`
Expected: PASS.

- [ ] **Step 5: Run the full `cmd` package test suite**

Run: `go test ./cmd/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/workspace_migrate.go cmd/workspace_migrate_shape_test.go
git commit -m "$(cat <<'EOF'
fix(cmd): mirror InstanceData.HeadroomProxy in migrationInstance

Keeps the workspace-migrate CLI's hand-duplicated type in sync with
session.InstanceData's v3 schema, per the drift-guard tests' own
contributor protocol.
EOF
)"
```

---

## Task 6: `Instance.HeadroomProxy` — persisted field, construction, and tmux env wiring

**Files:**
- Modify: `session/instance.go`
- Test: `session/instance_load_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `session/instance_load_test.go`, after `TestFromInstanceData_Paused_PreservesShape`:

```go
// TestFromInstanceData_PreservesHeadroomProxy asserts the HeadroomProxy
// toggle survives a Snapshot → FromInstanceData round trip, the same
// way Program does — needed so pause/resume and crash recovery (which
// construct a brand new TmuxSession from InstanceData) still apply it.
func TestFromInstanceData_PreservesHeadroomProxy(t *testing.T) {
	data := InstanceData{
		Title:               "hp-ws",
		Status:              Paused,
		IsWorkspaceTerminal: true,
		Program:             "claude",
		HeadroomProxy:       true,
	}

	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)
	assert.True(t, inst.HeadroomProxy)
}

// TestSnapshot_IncludesHeadroomProxy asserts Snapshot carries
// HeadroomProxy through to InstanceData, mirroring how Program does.
func TestSnapshot_IncludesHeadroomProxy(t *testing.T) {
	inst := &Instance{Title: "hp-ws", Status: Paused, Program: "claude", HeadroomProxy: true}
	data := inst.Snapshot()
	assert.True(t, data.HeadroomProxy)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./session/... -run 'TestFromInstanceData_PreservesHeadroomProxy|TestSnapshot_IncludesHeadroomProxy' -v`
Expected: FAIL to compile — `Instance` has no `HeadroomProxy` field yet.

- [ ] **Step 3: Add the `HeadroomProxy` field to `Instance`**

In `session/instance.go`, add the field to the struct (`session/instance.go:121-122`):

```go
	// Program is the program to run in the instance.
	Program string
	// HeadroomProxy controls whether this instance's tmux session gets
	// ANTHROPIC_BASE_URL pointed at Headroom's proxy (see
	// session.HeadroomProxyEnv). A no-op unless Program resolves to
	// Claude. Set once before Start() (same convention as Program) and
	// persisted so pause/resume and crash recovery — which construct a
	// brand new TmuxSession for the same instance — still apply it.
	HeadroomProxy bool
```

- [ ] **Step 4: Thread it through `Snapshot` and `FromInstanceData`**

In `session/instance.go`'s `Snapshot` (`session/instance.go:191-203`):

```go
	data := InstanceData{
		SchemaVersion:       CurrentSchemaVersion,
		Title:               i.Title,
		Path:                i.Path,
		Branch:              i.Branch,
		Status:              i.Status,
		Height:              i.Height,
		Width:               i.Width,
		CreatedAt:           i.CreatedAt,
		UpdatedAt:           time.Now(),
		Program:             i.Program,
		HeadroomProxy:       i.HeadroomProxy,
		IsWorkspaceTerminal: i.IsWorkspaceTerminal,
	}
```

In `session/instance.go`'s `FromInstanceData` (`session/instance.go:241-254`):

```go
	instance := &Instance{
		Title:               data.Title,
		Path:                data.Path,
		Branch:              data.Branch,
		Status:              data.Status,
		Height:              data.Height,
		Width:               data.Width,
		CreatedAt:           data.CreatedAt,
		UpdatedAt:           data.UpdatedAt,
		Program:             data.Program,
		HeadroomProxy:       data.HeadroomProxy,
		ConfigDir:           configDir,
		IsWorkspaceTerminal: data.IsWorkspaceTerminal,
		logger:              log.For("instance", "title", data.Title),
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./session/... -run 'TestFromInstanceData_PreservesHeadroomProxy|TestSnapshot_IncludesHeadroomProxy' -v`
Expected: PASS.

- [ ] **Step 6: Add `HeadroomProxy` to `InstanceOptions` and wire it through `NewInstance`**

In `session/instance.go`'s `InstanceOptions` struct (`session/instance.go:329-342`):

```go
type InstanceOptions struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Program is the program to run in the instance (e.g. "claude", "aider --model ollama_chat/gemma3:1b")
	Program string
	// HeadroomProxy controls whether this instance's tmux session gets
	// ANTHROPIC_BASE_URL pointed at Headroom's proxy. See Instance.HeadroomProxy.
	HeadroomProxy bool
	// Branch is an existing branch name to start the session on (empty = new branch from HEAD)
	Branch string
	// ConfigDir is the workspace config directory for worktree resolution.
	ConfigDir string
	// IsWorkspaceTerminal creates a workspace terminal instance (no worktree).
	IsWorkspaceTerminal bool
}
```

In `NewInstance` (`session/instance.go:358-371`):

```go
	return &Instance{
		Title:               opts.Title,
		Status:              Ready,
		Path:                absPath,
		Program:             opts.Program,
		HeadroomProxy:       opts.HeadroomProxy,
		Height:              0,
		Width:               0,
		CreatedAt:           t,
		UpdatedAt:           t,
		selectedBranch:      opts.Branch,
		ConfigDir:           opts.ConfigDir,
		IsWorkspaceTerminal: opts.IsWorkspaceTerminal,
		logger:              log.For("instance", "title", opts.Title),
	}, nil
```

- [ ] **Step 7: Pass the resolved env to all four `tmux.NewTmuxSession` call sites**

In `session/instance.go`'s `FromInstanceData` (`session/instance.go:284-287`):

```go
	if instance.Paused() || instance.GetStatus() == Recoverable {
		instance.setStarted(true)
		instance.setTmuxSession(tmux.NewTmuxSession(instance.Title, instance.Program, HeadroomProxyEnv(instance.HeadroomProxy, instance.Program)...))
	}
```

In `Start` (`session/instance.go:524-527`):

```go
	ts := i.getTmuxSession()
	if ts == nil {
		// Create new tmux session
		ts = tmux.NewTmuxSession(i.Title, i.Program, HeadroomProxyEnv(i.HeadroomProxy, i.Program)...)
	}
```

In `startFreshWithRecovery` (`session/instance.go:1073-1075`):

```go
func (i *Instance) startFreshWithRecovery(gw *git.GitWorktree) error {
	program := BuildRecoveryCommand(i.Program)
	ts := tmux.NewTmuxSession(i.Title, program, HeadroomProxyEnv(i.HeadroomProxy, program)...)
```

In `CrashRestart` (`session/instance.go:1090-1092`):

```go
func (i *Instance) CrashRestart() error {
	program := BuildRecoveryCommand(i.Program)
	ts := tmux.NewTmuxSession(i.Title, program, HeadroomProxyEnv(i.HeadroomProxy, program)...)
```

- [ ] **Step 8: Run the full `session` package test suite**

Run: `go test ./session/... -v 2>&1 | tail -80`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add session/instance.go session/instance_load_test.go
git commit -m "$(cat <<'EOF'
feat(session): wire Instance.HeadroomProxy into tmux session creation

Every call site that constructs a TmuxSession for an instance now
passes HeadroomProxyEnv(i.HeadroomProxy, program) — covers first
start, pause/resume's existing-session path (constructed but not
re-Started), crash recovery, and startFreshWithRecovery.
EOF
)"
```

---

## Task 7: Config rename — `HeadroomWrap` → `HeadroomProxy`

**Files:**
- Modify: `config/config.go`
- Test: `config/config_test.go`

- [ ] **Step 1: Update the test names and field references first (still red — compiles against the not-yet-renamed field)**

In `config/config_test.go`, replace `TestHeadroomWrapEnabled` (`config/config_test.go:396-415`):

```go
func TestHeadroomProxyEnabled(t *testing.T) {
	t.Run("nil (absent from config) defaults to disabled", func(t *testing.T) {
		cfg := &Config{}
		assert.False(t, cfg.HeadroomProxyEnabled())
	})

	t.Run("explicit true is enabled", func(t *testing.T) {
		cfg := &Config{HeadroomProxy: boolPtr(true)}
		assert.True(t, cfg.HeadroomProxyEnabled())
	})

	t.Run("explicit false is disabled", func(t *testing.T) {
		cfg := &Config{HeadroomProxy: boolPtr(false)}
		assert.False(t, cfg.HeadroomProxyEnabled())
	})

	t.Run("DefaultConfig disables it", func(t *testing.T) {
		assert.False(t, DefaultConfig().HeadroomProxyEnabled())
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./config/... -run TestHeadroomProxyEnabled -v`
Expected: FAIL to compile — `Config.HeadroomProxy`/`HeadroomProxyEnabled` don't exist yet.

- [ ] **Step 3: Rename the field, accessor, and `DefaultConfig` entry**

In `config/config.go`, replace the field (`config/config.go:109-121`):

```go
	// HeadroomProxy controls whether new Claude sessions launch with
	// ANTHROPIC_BASE_URL pointed at Headroom's proxy (see
	// session.HeadroomProxyEnv). A no-op for agents other than Claude.
	// Loom does not start or manage the headroom proxy process itself —
	// the user is expected to have it running separately. Defaults to
	// off (DefaultConfig sets it explicitly to false) since it's
	// opt-in. Mutually exclusive with ClaudeRemoteControl: enabling one
	// disables the other, enforced in the Claude Preferences toggle
	// handler, the Session Launch Options modal, and defensively again
	// in applyLaunchOptions so a hand-edited config.json with both
	// fields true still can't launch both at once. Read it through
	// HeadroomProxyEnabled.
	HeadroomProxy *bool `json:"headroom_proxy,omitempty"`
```

Replace the accessor (`config/config.go:181-185`):

```go
// HeadroomProxyEnabled reports whether new Claude sessions should
// launch with ANTHROPIC_BASE_URL pointed at Headroom's proxy. Defaults
// to false when unset.
func (c *Config) HeadroomProxyEnabled() bool {
	return c.HeadroomProxy != nil && *c.HeadroomProxy
}
```

In `DefaultConfig` (`config/config.go:250-253`):

```go
		ClaudeRemoteControl:  boolPtr(true),
		ClaudePermissionMode: stringPtr("default"),
		HeadroomProxy:        boolPtr(false),
		ClaudeModel:          stringPtr("default"),
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./config/... -run TestHeadroomProxyEnabled -v`
Expected: PASS.

- [ ] **Step 5: Run the full `config` package test suite**

Run: `go test ./config/...`
Expected: PASS.

- [ ] **Step 6: Confirm no remaining references to the old names in `config`**

Run: `grep -rn "HeadroomWrap" config/`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "$(cat <<'EOF'
refactor(config): rename HeadroomWrap to HeadroomProxy

Matches the new env-var mechanism (session.HeadroomProxyEnv) rather
than the old command-rewrite. An old config.json with headroom_wrap
is simply ignored (unknown key); the renamed field defaults to off,
same as the feature always defaulted before.
EOF
)"
```

---

## Task 8: UI rename — `overlay.LaunchOptions.HeadroomWrap` → `HeadroomProxy`, label text

**Files:**
- Modify: `ui/overlay/sessionLaunchOptions.go`
- Modify: `ui/overlay/claudePreferences.go`
- Test: `ui/overlay/sessionLaunchOptions_test.go`
- Test: `ui/overlay/claudePreferences_test.go`

- [ ] **Step 1: Update the tests first (red — compiles against the not-yet-renamed field/label)**

In `ui/overlay/sessionLaunchOptions_test.go`, replace every `HeadroomWrap` occurrence:

```go
func TestSessionLaunchOptionsHeadroomProxyExcludesRemoteControl(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true}, false, "")

	for i := 0; i < 3; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // toggle Headroom Proxy on

	assert.True(t, lo.Options().HeadroomProxy)
	assert.False(t, lo.Options().RemoteControl, "enabling Headroom Proxy must disable Remote Control")
}

func TestSessionLaunchOptionsRemoteControlExcludesHeadroomProxy(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{HeadroomProxy: true}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "}) // row 0: toggle Remote Control on

	assert.True(t, lo.Options().RemoteControl)
	assert.False(t, lo.Options().HeadroomProxy, "enabling Remote Control must disable Headroom Proxy")
}
```

And in `TestSessionLaunchOptionsRowNavigationClamps`:

```go
func TestSessionLaunchOptionsRowNavigationClamps(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true}, false, "")

	lo.HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"}) // up from row 0 stays at row 0
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.False(t, lo.Options().RemoteControl)

	for i := 0; i < 5; i++ {
		lo.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}) // clamps at row 3
	}
	lo.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, lo.Options().HeadroomProxy)
}
```

And `TestSessionLaunchOptionsRendersAllFourRows`:

```go
func TestSessionLaunchOptionsRendersAllFourRows(t *testing.T) {
	lo := NewSessionLaunchOptions(LaunchOptions{RemoteControl: true, PermissionMode: "plan", Model: "opus", HeadroomProxy: false}, false, "")
	rendered := lo.Render()
	assert.Contains(t, rendered, "Remote Control")
	assert.Contains(t, rendered, "Permission Mode")
	assert.Contains(t, rendered, "plan")
	assert.Contains(t, rendered, "Model")
	assert.Contains(t, rendered, "opus")
	assert.Contains(t, rendered, "Headroom Proxy")
}
```

In `ui/overlay/claudePreferences_test.go`, replace `TestClaudePreferencesHeadroomWrapExcludesRemoteControl`, `TestClaudePreferencesRemoteControlExcludesHeadroomWrap`, and `TestClaudePreferencesRendersHeadroomWrap`:

```go
func TestClaudePreferencesHeadroomProxyExcludesRemoteControl(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.True(t, cfg.RemoteControlEnabled())

	// Move focus down to the Headroom Proxy row (row 3) and enable it.
	for i := 0; i < 3; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.True(t, cfg.HeadroomProxyEnabled())
	assert.False(t, cfg.RemoteControlEnabled(), "enabling Headroom Proxy must disable Remote Control")
}

func TestClaudePreferencesRemoteControlExcludesHeadroomProxy(t *testing.T) {
	cfg := &config.Config{HeadroomProxy: boolPtr(true), ClaudeRemoteControl: boolPtr(false)}
	cp := NewClaudePreferences(cfg, false, "")
	assert.True(t, cfg.HeadroomProxyEnabled())

	// Row 0 (Remote Control) is already focused by default.
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.True(t, cfg.RemoteControlEnabled())
	assert.False(t, cfg.HeadroomProxyEnabled(), "enabling Remote Control must disable Headroom Proxy")
}
```

```go
func TestClaudePreferencesRendersHeadroomProxy(t *testing.T) {
	cfg := &config.Config{HeadroomProxy: boolPtr(true)}
	cp := NewClaudePreferences(cfg, false, "")
	rendered := cp.Render()
	assert.Contains(t, rendered, "Headroom Proxy")
	assert.Contains(t, rendered, "[x]")
}
```

And in `TestClaudePreferencesRowNavigationClamps`, update the comment and assertion:

```go
	// Down four times stays at row 3 (only four rows): toggles Headroom
	// Proxy, not any earlier row.
	for i := 0; i < 4; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.True(t, cfg.HeadroomProxyEnabled())
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./ui/overlay/... -run 'HeadroomProxy' -v`
Expected: FAIL to compile — `LaunchOptions.HeadroomProxy` and `Config.HeadroomProxy` (production side) don't have that name change reflected in `ui/overlay` sources yet at this point... actually `Config.HeadroomProxy` was already renamed in Task 7, so only `overlay.LaunchOptions.HeadroomWrap` is the remaining mismatch. Expected failure: compile error on `LaunchOptions{... HeadroomProxy: ...}` / `lo.Options().HeadroomProxy` — field doesn't exist on `LaunchOptions` yet.

- [ ] **Step 3: Rename `LaunchOptions.HeadroomWrap` and both UI labels**

In `ui/overlay/sessionLaunchOptions.go`, rename the field (`ui/overlay/sessionLaunchOptions.go:15-20`):

```go
type LaunchOptions struct {
	RemoteControl  bool
	PermissionMode string
	Model          string
	HeadroomProxy  bool
}
```

Update `toggleCursor` (`ui/overlay/sessionLaunchOptions.go:82-99`):

```go
func (l *SessionLaunchOptions) toggleCursor() {
	switch l.cursor {
	case 0:
		l.opts.RemoteControl = !l.opts.RemoteControl
		if l.opts.RemoteControl {
			l.opts.HeadroomProxy = false
		}
	case 1:
		l.opts.PermissionMode = nextInList(config.ClaudePermissionModes, l.opts.PermissionMode)
	case 2:
		l.opts.Model = nextInList(config.ClaudeModels, l.opts.Model)
	case 3:
		l.opts.HeadroomProxy = !l.opts.HeadroomProxy
		if l.opts.HeadroomProxy {
			l.opts.RemoteControl = false
		}
	}
}
```

Update `Render` (`ui/overlay/sessionLaunchOptions.go:125-140`):

```go
	rcCheck := "[ ]"
	if l.opts.RemoteControl {
		rcCheck = "[x]"
	}
	hwCheck := "[ ]"
	if l.opts.HeadroomProxy {
		hwCheck = "[x]"
	}

	content := sessionLaunchOptionsTitleStyle.Render("Session Launch Options") + "\n\n" +
		row(0, "Remote Control    ", rcCheck) + "\n" +
		row(1, "Permission Mode   ", "< "+l.opts.PermissionMode+" >") + "\n" +
		row(2, "Model             ", "< "+l.opts.Model+" >") + "\n" +
		row(3, "Headroom Proxy    ", hwCheck) + "\n\n" +
		sessionLaunchOptionsHintStyle.Render("up/down move • space toggle/cycle • enter start • esc cancel")
```

In `ui/overlay/claudePreferences.go`, update the cursor-3 handler in `HandleKeyPress` (`ui/overlay/claudePreferences.go:82-90`):

```go
		case 3:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.HeadroomProxyEnabled()
				cc.HeadroomProxy = &v
				if v {
					rc := false
					cc.ClaudeRemoteControl = &rc
				}
			})
```

Update the cursor-0 handler's inner variable name for clarity (`ui/overlay/claudePreferences.go:63-71`) — no field rename needed here beyond the one already-correct `cc.HeadroomWrap` reference:

```go
		case 0:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.RemoteControlEnabled()
				cc.ClaudeRemoteControl = &v
				if v {
					hp := false
					cc.HeadroomProxy = &hp
				}
			})
```

Update `Render`'s Headroom row (`ui/overlay/claudePreferences.go:159-172`):

```go
	hwCheck := "[ ]"
	if c.cfg.HeadroomProxyEnabled() {
		hwCheck = "[x]"
	}
	hwCursor := "  "
	if c.cursor == 3 {
		hwCursor = "> "
	}
	hwRow := hwCursor + "Headroom Proxy    " + hwCheck
	if c.cursor == 3 {
		hwRow = claudePrefsSelectedStyle.Render(hwRow)
	} else {
		hwRow = claudePrefsRowStyle.Render(hwRow)
	}
```

Update the doc comments mentioning "Headroom Wrap" in both files' headers (`ui/overlay/claudePreferences.go:15,33`, `ui/overlay/sessionLaunchOptions.go:11,37`) to say "Headroom Proxy" instead — e.g. `claudePreferences.go:15`:

```go
// rows: Remote Control, Permission Mode, Model, and Headroom Proxy.
```

and `claudePreferences.go:33`:

```go
// Permission Mode, Model, and Headroom Proxy.
```

and `sessionLaunchOptions.go:11` (struct doc):

```go
// SessionLaunchOptions is the per-instance "Session Launch Options"
```

(no change needed on that specific line — it doesn't mention Headroom by name) and `sessionLaunchOptions.go:37`:

```go
// sessionLaunchOptionsRowCount is the number of navigable rows: Remote
// Control, Permission Mode, Model, and Headroom Proxy.
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./ui/overlay/... -v 2>&1 | tail -80`
Expected: PASS.

- [ ] **Step 5: Confirm no remaining "Headroom Wrap" text or `HeadroomWrap` identifiers in `ui/`**

Run: `grep -rn "HeadroomWrap\|Headroom Wrap" ui/`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add ui/overlay/sessionLaunchOptions.go ui/overlay/claudePreferences.go ui/overlay/sessionLaunchOptions_test.go ui/overlay/claudePreferences_test.go
git commit -m "$(cat <<'EOF'
refactor(ui): rename Headroom Wrap to Headroom Proxy in both screens

Pure rename — same row, same exclusivity behavior with Remote
Control, just matching the new env-var mechanism's name.
EOF
)"
```

---

## Task 9: `app/remote_control.go` — drop the command-rewrite step, rename fields

**Files:**
- Modify: `app/remote_control.go`
- Test: `app/remote_control_test.go`

- [ ] **Step 1: Update the tests first (red — compiles against not-yet-updated production code)**

In `app/remote_control_test.go`, delete `TestHeadroomWrapProgram` (`app/remote_control_test.go:96-108`):

```go
func TestHeadroomWrapProgram(t *testing.T) {
	t.Run("disabled is a no-op", func(t *testing.T) {
		assert.Equal(t, "claude --model sonnet", headroomWrapProgram(false, "claude --model sonnet"))
	})

	t.Run("enabled wraps the whole command", func(t *testing.T) {
		assert.Equal(t, "headroom wrap claude --model sonnet", headroomWrapProgram(true, "claude --model sonnet"))
	})

	t.Run("agent-agnostic: wraps non-claude programs too", func(t *testing.T) {
		assert.Equal(t, "headroom wrap aider --model gemma", headroomWrapProgram(true, "aider --model gemma"))
	})
}
```

(Delete this whole function — `headroomWrapProgram` no longer exists; Headroom Proxy no longer touches `program`.)

Replace `TestLaunchOptionsFromConfig` (`app/remote_control_test.go:110-139`):

```go
func TestLaunchOptionsFromConfig(t *testing.T) {
	t.Run("nil cfg returns zero value", func(t *testing.T) {
		assert.Equal(t, overlay.LaunchOptions{}, launchOptionsFromConfig(nil))
	})

	t.Run("populated cfg maps every field", func(t *testing.T) {
		got := launchOptionsFromConfig(config.DefaultConfig())
		assert.Equal(t, overlay.LaunchOptions{
			RemoteControl:  true,
			PermissionMode: "default",
			Model:          "default",
			HeadroomProxy:  false,
		}, got)
	})

	t.Run("threads through explicit overrides", func(t *testing.T) {
		cfg := &config.Config{
			ClaudeRemoteControl:  boolPtrTest(false),
			ClaudePermissionMode: stringPtrTest("plan"),
			ClaudeModel:          stringPtrTest("opus"),
			HeadroomProxy:        boolPtrTest(true),
		}
		assert.Equal(t, overlay.LaunchOptions{
			RemoteControl:  false,
			PermissionMode: "plan",
			Model:          "opus",
			HeadroomProxy:  true,
		}, launchOptionsFromConfig(cfg))
	})
}
```

Replace `TestEffectiveRemoteControl` and `TestRemoteControlBlockedAgreesWithComposedCommandWhenHeadroomWrapForcesRCOff` (`app/remote_control_test.go:141-156`):

```go
func TestEffectiveRemoteControl(t *testing.T) {
	assert.True(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: true, HeadroomProxy: false}))
	assert.False(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: false, HeadroomProxy: false}))
	assert.False(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: true, HeadroomProxy: true}))
	assert.False(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: false, HeadroomProxy: true}))
}

func TestRemoteControlBlockedAgreesWithComposedCommandWhenHeadroomProxyForcesRCOff(t *testing.T) {
	// A config.json (or a Session Launch Options selection) with both
	// RemoteControl and HeadroomProxy true must not report "blocked" for
	// a conflict the composed command doesn't actually have.
	opts := overlay.LaunchOptions{RemoteControl: true, HeadroomProxy: true}
	m := &home{rcAuth: session.RemoteControlAuth{State: session.RemoteControlAuthBlocked}}
	assert.False(t, m.remoteControlBlocked(effectiveRemoteControl(opts), "claude"))
}
```

Replace `TestApplyLaunchOptions` (`app/remote_control_test.go:158-184`):

```go
func TestApplyLaunchOptions(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}

	t.Run("stacks remote-control, permission-mode, and model", func(t *testing.T) {
		opts := overlay.LaunchOptions{RemoteControl: true, PermissionMode: "acceptEdits", Model: "opus", HeadroomProxy: false}
		got := applyLaunchOptions(opts, authOK, "claude", "my task")
		assert.Equal(t, "claude --model opus --permission-mode acceptEdits --remote-control my-task", got)
	})

	t.Run("headroom proxy never touches program", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "acceptEdits", Model: "opus", HeadroomProxy: true}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "claude --model opus --permission-mode acceptEdits", got)
	})

	t.Run("headroom proxy forcibly disables remote control even if both are true", func(t *testing.T) {
		// applyLaunchOptions calls remoteControlProgram with
		// effectiveRemoteControl(opts), not raw opts.RemoteControl — this
		// is the authoritative enforcement of the RC/HeadroomProxy
		// exclusivity rule (see TestEffectiveRemoteControl), not just a
		// UI-level nicety.
		opts := overlay.LaunchOptions{RemoteControl: true, HeadroomProxy: true}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "claude", got)
	})

	t.Run("all defaults/disabled is a no-op", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "default", Model: "default"}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "claude", got)
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./app/... -run 'TestLaunchOptionsFromConfig|TestEffectiveRemoteControl|TestApplyLaunchOptions|TestRemoteControlBlockedAgreesWithComposedCommandWhenHeadroomProxyForcesRCOff' -v`
Expected: FAIL to compile. `overlay.LaunchOptions.HeadroomProxy` already exists (renamed in Task 8), but `app/remote_control.go` still references the old `opts.HeadroomWrap` field and calls `headroomWrapProgram`/`session.BuildHeadroomWrapCommand` (deleted in Task 3) — those are the compile errors this step's production-code change fixes.

- [ ] **Step 3: Update `app/remote_control.go`**

Delete `headroomWrapProgram` (`app/remote_control.go:41-50`):

```go
// headroomWrapProgram returns program wrapped as "headroom wrap
// <tool> <args>" when enabled, for whichever adapter matches program —
// a no-op for agents Headroom doesn't support (see
// session.BuildHeadroomWrapCommand).
func headroomWrapProgram(enabled bool, program string) string {
	if !enabled {
		return program
	}
	return session.BuildHeadroomWrapCommand(program)
}
```

(Delete this whole function.)

Replace `launchOptionsFromConfig` (`app/remote_control.go:58-68`):

```go
func launchOptionsFromConfig(cfg *config.Config) overlay.LaunchOptions {
	if cfg == nil {
		return overlay.LaunchOptions{}
	}
	return overlay.LaunchOptions{
		RemoteControl:  cfg.RemoteControlEnabled(),
		PermissionMode: cfg.PermissionMode(),
		Model:          cfg.Model(),
		HeadroomProxy:  cfg.HeadroomProxyEnabled(),
	}
}
```

Replace `effectiveRemoteControl` and `applyLaunchOptions` (`app/remote_control.go:70-96`):

```go
// effectiveRemoteControl reports whether opts should actually apply
// the --remote-control flag once Headroom Proxy's exclusivity is
// accounted for. applyLaunchOptions callers and every remoteControlBlocked
// call site must agree on this value — otherwise a config.json
// hand-edited to set both ClaudeRemoteControl and HeadroomProxy true
// (or a Session Launch Options selection reaching that state) would
// make remoteControlBlocked report a conflict the composed command
// doesn't actually have.
func effectiveRemoteControl(opts overlay.LaunchOptions) bool {
	return opts.RemoteControl && !opts.HeadroomProxy
}

// applyLaunchOptions composes program in order: remote-control,
// permission-mode, then model. Headroom Proxy is intentionally absent
// from composition — it never touches program (see
// session.HeadroomProxyEnv, applied separately to the tmux session's
// environment via Instance.HeadroomProxy) — but it still affects
// composition indirectly: remoteControlProgram receives
// effectiveRemoteControl(opts), not raw opts.RemoteControl, so Headroom
// Proxy being on still forces --remote-control off. This is the
// authoritative enforcement of the RC/HeadroomProxy exclusivity rule —
// the UI-level auto-disable (Claude Preferences, Session Launch
// Options) is the good-UX layer on top, not the only guarantee.
func applyLaunchOptions(opts overlay.LaunchOptions, auth session.RemoteControlAuth, program, title string) string {
	program = remoteControlProgram(effectiveRemoteControl(opts), auth, program, title)
	program = permissionModeProgram(opts.PermissionMode, program)
	program = modelProgram(opts.Model, program)
	return program
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./app/... -run 'TestLaunchOptionsFromConfig|TestEffectiveRemoteControl|TestApplyLaunchOptions|TestRemoteControlBlockedAgreesWithComposedCommandWhenHeadroomProxyForcesRCOff|TestHeadroomWrapProgram' -v`
Expected: PASS (and `TestHeadroomWrapProgram` reports "no tests to run", confirming it was fully deleted, not just renamed).

- [ ] **Step 5: Build check**

`app/state_new.go`, `app/state_prompt.go`, and `app/app.go`'s two workspace-terminal sites still only call `applyLaunchOptions(opts, ...)` and assign `instance.Program` — they don't yet set `instance.HeadroomProxy`/`InstanceOptions.HeadroomProxy` (that wiring is Task 10), but nothing about this task's changes requires them to. `app` should build cleanly now.

Run: `go build ./app/...`
Expected: SUCCESS.

- [ ] **Step 6: Commit**

```bash
git add app/remote_control.go app/remote_control_test.go
git commit -m "$(cat <<'EOF'
refactor(app): drop headroom-wrap command composition step

applyLaunchOptions no longer touches program for Headroom Proxy —
composition is down to remote-control, permission-mode, and model.
effectiveRemoteControl keeps the RC/HeadroomProxy exclusivity rule,
renamed to match the config/overlay rename.
EOF
)"
```

---

## Task 10: Wire `HeadroomProxy` through instance-creation call sites

**Files:**
- Modify: `app/state_new.go`
- Modify: `app/state_prompt.go`
- Modify: `app/app.go`
- Modify: `app/state_launch_options_test.go`

- [ ] **Step 1: `app/state_new.go` — set `instance.HeadroomProxy` alongside `instance.Program`**

In `app/state_new.go`'s `pendingLaunchOptions` closure (`app/state_new.go:58-67`):

```go
		m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
			startTask := overlay.ConfirmationTask{
				Sync: func() {
					instance.Program = applyLaunchOptions(opts, m.rcAuth, instance.Program, instance.Title)
					instance.HeadroomProxy = opts.HeadroomProxy
					_ = instance.TransitionTo(session.Loading)
					m.newInstanceFinalizer()
					m.promptAfterName = false
					m.state = stateDefault
					m.menu.SetState(ui.StateDefault)
				},
```

- [ ] **Step 2: `app/state_prompt.go` — same wiring**

In `app/state_prompt.go`'s `pendingLaunchOptions` closure (`app/state_prompt.go:55-63`):

```go
				m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
					startTask := overlay.ConfirmationTask{
						Sync: func() {
							selected.Program = applyLaunchOptions(opts, m.rcAuth, selected.Program, selected.Title)
							selected.HeadroomProxy = opts.HeadroomProxy
							_ = selected.TransitionTo(session.Loading)
							m.newInstanceFinalizer()
							m.state = stateDefault
							m.menu.SetState(ui.StateDefault)
						},
```

- [ ] **Step 3: `app/app.go` — both auto-created-workspace-terminal `InstanceOptions` literals**

First site (`app/app.go:404-410`):

```go
			wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
				Title:               wtTitle,
				Path:                wsCtx.RepoPath,
				Program:             applyLaunchOptions(wtOpts, h.rcAuth, program, wtTitle),
				HeadroomProxy:       wtOpts.HeadroomProxy,
				IsWorkspaceTerminal: true,
				ConfigDir:           cfgDir,
			})
```

Second site (`app/app.go:1710-1716`):

```go
		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             applyLaunchOptions(wtOpts, m.rcAuth, appConfig.GetProgram(), wtTitle),
			HeadroomProxy:       wtOpts.HeadroomProxy,
			IsWorkspaceTerminal: true,
			ConfigDir:           wsCtx.ConfigDir,
		})
```

- [ ] **Step 4: Keep the `app/state_launch_options_test.go` helper faithful to production wiring**

In `app/state_launch_options_test.go`'s `newPendingLaunchOptionsHome` (`app/state_launch_options_test.go:34-38`):

```go
	m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		instance.Program = applyLaunchOptions(opts, m.rcAuth, instance.Program, instance.Title)
		instance.HeadroomProxy = opts.HeadroomProxy
		return m, nil
	}
```

- [ ] **Step 5: Add a regression test confirming the wiring**

Add to `app/state_launch_options_test.go`, after `TestHandleStateLaunchOptionsKeyConfirmComposesAndClearsPending`:

```go
func TestHandleStateLaunchOptionsKeyConfirmSetsHeadroomProxy(t *testing.T) {
	m, instance := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{PermissionMode: "default", Model: "default", HeadroomProxy: true})

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.True(t, instance.HeadroomProxy)
}
```

- [ ] **Step 6: Run the full `app` package test suite**

Run: `go test ./app/... -v 2>&1 | tail -100`
Expected: PASS.

- [ ] **Step 7: Full-repo build check**

Run: `go build ./...`
Expected: SUCCESS — every package in the repo now compiles.

- [ ] **Step 8: Commit**

```bash
git add app/state_new.go app/state_prompt.go app/app.go app/state_launch_options_test.go
git commit -m "$(cat <<'EOF'
feat(app): set Instance.HeadroomProxy at every instance-creation site

Program composition and HeadroomProxy are now two independent
assignments at each of the four places that used to just set Program
from applyLaunchOptions — the plain n/N flows, and both auto-created
workspace terminals.
EOF
)"
```

---

## Task 11: Full-repo verification

**Files:** none (verification only)

- [ ] **Step 1: Format check**

Run: `gofmt -l .`
Expected: no output (no files need formatting). If any file is listed, run `gofmt -w <file>` and re-check.

- [ ] **Step 2: Full test suite**

Run: `go test ./... 2>&1 | tail -80`
Expected: all packages `ok`, no `FAIL`.

- [ ] **Step 3: Race detector on the packages touched in this plan**

Run: `CC=clang CGO_ENABLED=1 go test -race ./session/... ./session/tmux/... ./session/agent/... ./config/... ./ui/overlay/... ./app/... ./cmd/...`
(Per `docs/superpowers/plans`/CLAUDE.md guidance: this repo's default `CGO_ENABLED=0` build disables the race detector, so it needs `CC=clang CGO_ENABLED=1` — use `CC=gcc` if `clang` isn't installed.)
Expected: PASS, no `WARNING: DATA RACE` output.

- [ ] **Step 4: Lint**

Run: `golangci-lint run --timeout=3m --fast`
Expected: no issues reported. If `headroomSupportedTools`/`BuildHeadroomWrapCommand`/`SplitHeadroomWrap`/`HeadroomWrap` show up anywhere as an unused-symbol or dead-code finding, that means a deletion was missed — go back and remove it.

- [ ] **Step 5: Final grep sweep for stale naming**

Run: `grep -rn "HeadroomWrap\|headroom wrap\|BuildHeadroomWrapCommand\|SplitHeadroomWrap\|headroomWrapPrefix\|headroomSupportedTools\|headroomWrapProgram" --include="*.go" . | grep -v vendor/`
Expected: no output.

- [ ] **Step 6: Manual smoke check of the spec's one behavioral claim worth double-checking**

Run: `tmux -V` (already confirmed 3.6a supports `new-session -e` during design — this just re-confirms the dev environment, not a repo change):
Expected: `tmux 3.6a` (or any version ≥ 3.2).

- [ ] **Step 7: If everything above is green, this plan is complete.** No further commit needed for this task — it's verification-only. If anything failed, fix it in a small follow-up commit scoped to the specific failure (formatting, a missed rename, a race) rather than reopening earlier tasks' commits.
