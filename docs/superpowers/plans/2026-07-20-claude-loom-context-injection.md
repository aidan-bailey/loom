# Claude Loom-Context Injection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Launch every loom-managed Claude session with `--append-system-prompt-file` pointing at a loom-owned markdown file that tells the agent it runs inside loom and how to avoid disrupting loom's git lifecycle — using a worktree variant for isolated sessions and a workspace-terminal variant for the root-repo main session.

**Architecture:** A global (not per-instance) enabled flag lives as an `atomic.Bool` in package `session`, set from config by the app. Two prompt variants are embedded via `go:embed` and written to the config dir on workspace activation. At launch (`instance.go`, where the tmux session is created), loom wraps the program string with `--append-system-prompt-file '<path>'`, selecting the variant by `Instance.IsWorkspaceTerminal` and skipping injection when disabled, non-Claude, or the file is missing. No `InstanceData` schema change.

**Tech Stack:** Go 1.23 (toolchain 1.24), `CGO_ENABLED=0` builds, `go:embed`, `sync/atomic`, testify/assert, Bubble Tea overlays.

---

## Design notes (read before starting)

- **The flag is real but hidden.** `--append-system-prompt-file` works in claude 2.1.210 but is absent from `--help` (verified empirically: bogus flags exit 1 with `error: unknown option`; this flag is accepted). Do not "fix" its absence from help.
- **The path must be single-quoted.** `session/tmux/tmux.go` appends the whole program as one shell-interpreted argument (`args = append(args, t.program)`), so `--append-system-prompt-file '<path>'` guards against spaces/metacharacters in the config-dir path. Config-dir paths are assumed free of single quotes.
- **Fail-safe:** injection is skipped when the target file does not exist, so a failed file-write never produces a launch that points at a missing file.
- **Coexistence:** idempotency keys on loom's *own* exact flag+path fragment. A user's *different* `--append-system-prompt-file` is left untouched and loom's is still added (both passed).

## File structure

| File | Responsibility | Create/Modify |
|------|----------------|---------------|
| `config/config.go` | `ClaudeLoomContext *bool` field + `LoomContextEnabled()` helper | Modify |
| `config/config_test.go` | tri-state helper test | Modify |
| `session/claude-loom-context.md` | worktree-session prompt (embedded) | Create |
| `session/claude-loom-context-workspace.md` | workspace-terminal prompt (embedded) | Create |
| `session/loom_context.go` | embed, `atomic.Bool` enabled flag + setter, `WriteLoomContextFiles`, `loomContextProgram`, `BuildLoomContextCommand` | Create |
| `session/loom_context_test.go` | selection/write/skip tests | Create |
| `session/agent/adapter.go` | add `ApplyLoomContextFlag` to interface | Modify |
| `session/agent/claude.go` | real `ApplyLoomContextFlag` impl | Modify |
| `session/agent/aider.go`, `gemini.go`, `default.go` | no-op impls | Modify |
| `session/agent/adapter_test.go` | adapter method tests | Modify |
| `session/instance.go` | wrap program at launch (~line 590) | Modify |
| `app/app.go` | `activateWorkspace`: write files + sync enabled flag | Modify |
| `app/state_settings.go` | re-sync enabled flag after a settings toggle | Modify |
| `ui/overlay/claudePreferences.go` | "Loom Context" toggle row | Modify |
| `ui/overlay/claudePreferences_test.go` | toggle-row test | Modify |

---

## Task 1: Config field + helper

**Files:**
- Modify: `config/config.go` (field near `ClaudeRemoteControl` ~line 98; helper near `RemoteControlEnabled` ~line 175)
- Test: `config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add to `config/config_test.go`:

```go
func TestLoomContextEnabled(t *testing.T) {
	// nil => enabled (mirrors RemoteControlEnabled)
	c := &Config{}
	assert.True(t, c.LoomContextEnabled())

	tru := true
	c.ClaudeLoomContext = &tru
	assert.True(t, c.LoomContextEnabled())

	fls := false
	c.ClaudeLoomContext = &fls
	assert.False(t, c.LoomContextEnabled())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./config -run TestLoomContextEnabled -v`
Expected: FAIL — compile error, `c.ClaudeLoomContext` and `c.LoomContextEnabled` undefined.

- [ ] **Step 3: Add the field**

In `config/config.go`, immediately after the `ClaudeRemoteControl *bool` field, add:

```go
	// ClaudeLoomContext controls whether new Claude sessions launch with
	// --append-system-prompt-file pointing at loom's embedded context
	// file (see session.WriteLoomContextFiles). nil is treated as enabled
	// (read via LoomContextEnabled), matching ClaudeRemoteControl. A no-op
	// for agents other than Claude.
	ClaudeLoomContext *bool `json:"claude_loom_context,omitempty"`
```

- [ ] **Step 4: Add the helper**

In `config/config.go`, immediately after the `RemoteControlEnabled` method, add:

```go
// LoomContextEnabled reports whether new Claude sessions should launch
// with loom's context file injected. nil (unset) is treated as enabled,
// mirroring RemoteControlEnabled. Read only from the main goroutine.
func (c *Config) LoomContextEnabled() bool {
	return c.ClaudeLoomContext == nil || *c.ClaudeLoomContext
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./config -run TestLoomContextEnabled -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): add ClaudeLoomContext with nil-on default"
```

---

## Task 2: Embedded prompt files

**Files:**
- Create: `session/claude-loom-context.md`
- Create: `session/claude-loom-context-workspace.md`

These are data files (no test of their own; they are exercised by Task 4). Content is the approved spec text verbatim.

- [ ] **Step 1: Create the worktree-session prompt**

Create `session/claude-loom-context.md` with exactly:

```markdown
# Loom session context

You are running inside **loom**, a terminal UI that runs multiple coding agents in parallel, each in its own isolated git worktree and tmux session. This note explains the parts of your environment loom manages, so you don't inadvertently disrupt them.

**Your workspace is a loom-managed worktree.** Your working directory is a git worktree pinned to a branch loom created for this session (typically `<username>/<session-title>`). Loom owns this worktree and branch's lifecycle — creation, pausing, resuming, and merging between sessions.

**What this means for you:**

- **Stay on loom's branch.** Don't `git checkout`/`switch` to another branch, create new branches, create or remove worktrees, or rebase onto other branches. Loom identifies this session by its branch and worktree and assumes both stay put — its pause, resume, and merge operations desync if you change them. If parallel or branching work is needed, let the user spin up another loom session.
- **Commit normally.** Loom moves work between sessions via commits: pausing commits outstanding changes; merging pulls another session's committed work. Committed work is what loom can act on.
- **You may not be alone.** Other loom sessions may be running in sibling worktrees on their own branches against this same repo. Don't assume you're the only actor, and don't reach into other worktrees.

Everything else — editing files, running builds and tests, committing, diffing, and viewing history within your branch — works exactly as normal.
```

- [ ] **Step 2: Create the workspace-terminal prompt**

Create `session/claude-loom-context-workspace.md` with exactly:

```markdown
# Loom session context

You are running inside **loom**, a terminal UI that runs multiple coding agents in parallel. This is a loom workspace's **main session**: you're operating directly in the workspace's **root git repository**, not an isolated worktree.

**What this means for you:**

- **Don't create git worktrees or branches.** Loom gives each agent session it spawns a dedicated worktree and branch of its own. If you create worktrees or branches yourself, you bypass loom and collide with how it tracks work — leave that to loom, and if parallel work is needed, suggest the user start a new loom session.
- **You may not be alone.** Other loom sessions may be running concurrently in sibling worktrees on their own branches off this repo. Don't assume you're the only actor, and don't reach into those worktrees.
- **No loom safety net here.** Unlike loom's worktree sessions, the main session can't be paused, resumed, or merged by loom — your uncommitted changes are the repository's actual working state.

Editing files, running builds and tests, committing, and viewing diffs/history all work exactly as normal.
```

- [ ] **Step 3: Commit**

```bash
git add session/claude-loom-context.md session/claude-loom-context-workspace.md
git commit -m "feat(session): add embedded loom-context prompt files"
```

---

## Task 3: Adapter `ApplyLoomContextFlag`

**Files:**
- Modify: `session/agent/adapter.go` (interface, after `ApplyEffortFlag` ~line 87)
- Modify: `session/agent/claude.go` (real impl, append after `ApplyEffortFlag` ~line 159)
- Modify: `session/agent/aider.go`, `session/agent/gemini.go`, `session/agent/default.go` (no-op impls)
- Test: `session/agent/adapter_test.go`

- [ ] **Step 1: Write the failing test**

Add to `session/agent/adapter_test.go`:

```go
func TestApplyLoomContextFlag(t *testing.T) {
	reg := DefaultRegistry()
	path := "/home/u/.loom/claude-loom-context.md"

	// claude: inserts single-quoted flag right after the command token
	got := reg.Lookup("claude").ApplyLoomContextFlag("claude", path)
	assert.Equal(t, "claude --append-system-prompt-file '"+path+"'", got)

	// preserves existing flags, inserting after the command
	got = reg.Lookup("claude").ApplyLoomContextFlag("claude --permission-mode plan", path)
	assert.Equal(t, "claude --append-system-prompt-file '"+path+"' --permission-mode plan", got)

	// idempotent on loom's own path
	assert.Equal(t, got, reg.Lookup("claude").ApplyLoomContextFlag(got, path))

	// coexists with a DIFFERENT user-supplied append-file (both present)
	user := "claude --append-system-prompt-file /my/own.md"
	got = reg.Lookup("claude").ApplyLoomContextFlag(user, path)
	assert.Contains(t, got, "--append-system-prompt-file '"+path+"'")
	assert.Contains(t, got, "--append-system-prompt-file /my/own.md")

	// empty path is a no-op
	assert.Equal(t, "claude", reg.Lookup("claude").ApplyLoomContextFlag("claude", ""))

	// non-claude adapters are no-ops
	assert.Equal(t, "aider", reg.Lookup("aider").ApplyLoomContextFlag("aider", path))
	assert.Equal(t, "gemini", reg.Lookup("gemini").ApplyLoomContextFlag("gemini", path))
	assert.Equal(t, "unknownprog", reg.Lookup("unknownprog").ApplyLoomContextFlag("unknownprog", path))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./session/agent -run TestApplyLoomContextFlag -v`
Expected: FAIL — compile error, `ApplyLoomContextFlag` not in the `Adapter` interface.

- [ ] **Step 3: Add the interface method**

In `session/agent/adapter.go`, inside the `Adapter` interface, immediately after the `ApplyEffortFlag(program, effort string) string` method, add:

```go
	// ApplyLoomContextFlag returns the program string with
	// "--append-system-prompt-file '<filePath>'" inserted (e.g. "claude
	// --append-system-prompt-file '/home/u/.loom/claude-loom-context.md'"),
	// pointing Claude at loom's context file. filePath == "" is a no-op.
	// Idempotent on loom's own path: if the exact flag+path is already
	// present the input is returned unchanged, but a different
	// user-supplied --append-system-prompt-file is left in place and
	// loom's is still added. Returns the input unchanged for agents
	// without a system-prompt-file concept.
	ApplyLoomContextFlag(program, filePath string) string
```

- [ ] **Step 4: Add the claude implementation**

At the end of `session/agent/claude.go`, add:

```go
// ApplyLoomContextFlag inserts "--append-system-prompt-file '<filePath>'"
// after "claude". The path is single-quoted because tmux runs the whole
// program string through the shell (session/tmux/tmux.go appends it as
// one argument), so an unquoted path with spaces would be word-split;
// loom's config-dir paths are assumed free of single quotes. filePath ==
// "" or an empty program is a no-op. Idempotent on loom's own flag+path
// fragment — a different user-supplied --append-system-prompt-file is
// left untouched and loom's is still added, so both coexist.
func (claudeAdapter) ApplyLoomContextFlag(program, filePath string) string {
	if filePath == "" {
		return program
	}
	if len(strings.Fields(program)) == 0 {
		return program
	}
	flag := "--append-system-prompt-file '" + filePath + "'"
	if strings.Contains(program, flag) {
		return program
	}
	return insertAfterCommand(program, flag)
}
```

- [ ] **Step 5: Add no-op implementations**

In `session/agent/aider.go`, after `ApplyEffortFlag`, add:

```go
// ApplyLoomContextFlag is a no-op for aider — it has no
// system-prompt-file concept.
func (aiderAdapter) ApplyLoomContextFlag(program, _ string) string {
	return program
}
```

In `session/agent/gemini.go`, after `ApplyEffortFlag`, add:

```go
// ApplyLoomContextFlag is a no-op for gemini — it has no
// system-prompt-file concept.
func (geminiAdapter) ApplyLoomContextFlag(program, _ string) string {
	return program
}
```

In `session/agent/default.go`, after `ApplyEffortFlag`, add:

```go
// ApplyLoomContextFlag implements Adapter. The fallback adapter never
// injects a loom-context flag.
func (defaultAdapter) ApplyLoomContextFlag(program, _ string) string { return program }
```

- [ ] **Step 6: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./session/agent -run TestApplyLoomContextFlag -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add session/agent/adapter.go session/agent/claude.go session/agent/aider.go session/agent/gemini.go session/agent/default.go session/agent/adapter_test.go
git commit -m "feat(agent): add ApplyLoomContextFlag for --append-system-prompt-file"
```

---

## Task 4: session enabled flag, file writer, program wrapper

**Files:**
- Create: `session/loom_context.go`
- Test: `session/loom_context_test.go`

- [ ] **Step 1: Write the failing test**

Create `session/loom_context_test.go`:

```go
package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteLoomContextFiles(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, WriteLoomContextFiles(dir))

	wt := filepath.Join(dir, "claude-loom-context.md")
	ws := filepath.Join(dir, "claude-loom-context-workspace.md")
	for _, p := range []string{wt, ws} {
		b, err := os.ReadFile(p)
		assert.NoError(t, err)
		assert.Contains(t, string(b), "Loom session context")
	}

	// stale content is rewritten
	assert.NoError(t, os.WriteFile(wt, []byte("stale"), 0o644))
	assert.NoError(t, WriteLoomContextFiles(dir))
	b, _ := os.ReadFile(wt)
	assert.NotEqual(t, "stale", string(b))

	// empty configDir is a no-op (no error)
	assert.NoError(t, WriteLoomContextFiles(""))
}

func TestLoomContextProgram(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, WriteLoomContextFiles(dir))
	wtPath := filepath.Join(dir, "claude-loom-context.md")
	wsPath := filepath.Join(dir, "claude-loom-context-workspace.md")

	// disabled => unchanged
	SetLoomContextEnabled(false)
	assert.Equal(t, "claude", loomContextProgram("claude", dir, false))

	SetLoomContextEnabled(true)
	t.Cleanup(func() { SetLoomContextEnabled(false) })

	// worktree instance => worktree file
	assert.Equal(t,
		"claude --append-system-prompt-file '"+wtPath+"'",
		loomContextProgram("claude", dir, false))

	// workspace terminal => workspace file
	assert.Equal(t,
		"claude --append-system-prompt-file '"+wsPath+"'",
		loomContextProgram("claude", dir, true))

	// non-claude program => unchanged
	assert.Equal(t, "aider", loomContextProgram("aider", dir, false))

	// empty configDir => unchanged
	assert.Equal(t, "claude", loomContextProgram("claude", "", false))

	// missing file => unchanged (fail-safe)
	assert.Equal(t, "claude", loomContextProgram("claude", t.TempDir(), false))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./session -run 'TestWriteLoomContextFiles|TestLoomContextProgram' -v`
Expected: FAIL — compile error, `WriteLoomContextFiles`, `SetLoomContextEnabled`, `loomContextProgram` undefined.

- [ ] **Step 3: Write the implementation**

Create `session/loom_context.go`:

```go
package session

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/aidan-bailey/loom/session/agent"
)

// Loom-context prompt files, embedded at build time and written to the
// config dir by WriteLoomContextFiles. The worktree variant orients an
// isolated-worktree session; the workspace variant orients the root-repo
// main session (IsWorkspaceTerminal).
const (
	loomContextFileWorktree  = "claude-loom-context.md"
	loomContextFileWorkspace = "claude-loom-context-workspace.md"
)

//go:embed claude-loom-context.md
var loomContextWorktreeBytes []byte

//go:embed claude-loom-context-workspace.md
var loomContextWorkspaceBytes []byte

// loomContextEnabled mirrors config.LoomContextEnabled(); the app sets it
// from config (SetLoomContextEnabled) on startup, workspace activation,
// and after a settings toggle. atomic.Bool keeps the launch-time read
// race-safe against the main-goroutine writes.
var loomContextEnabled atomic.Bool

// SetLoomContextEnabled updates the global loom-context toggle.
func SetLoomContextEnabled(enabled bool) { loomContextEnabled.Store(enabled) }

// WriteLoomContextFiles writes both embedded prompt files into configDir,
// (re)writing a file only when it is missing or its bytes differ from the
// embedded content (so a loom upgrade refreshes the prose automatically).
// A no-op when configDir is empty.
func WriteLoomContextFiles(configDir string) error {
	if configDir == "" {
		return nil
	}
	files := []struct {
		name    string
		content []byte
	}{
		{loomContextFileWorktree, loomContextWorktreeBytes},
		{loomContextFileWorkspace, loomContextWorkspaceBytes},
	}
	for _, f := range files {
		path := filepath.Join(configDir, f.name)
		if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, f.content) {
			continue
		}
		if err := os.WriteFile(path, f.content, 0o644); err != nil {
			return fmt.Errorf("write loom context %s: %w", f.name, err)
		}
	}
	return nil
}

// BuildLoomContextCommand returns program with Claude's
// --append-system-prompt-file flag pointing at filePath. The adapter
// registry no-ops for non-Claude programs.
func BuildLoomContextCommand(program, filePath string) string {
	return defaultRegistry.Lookup(program).ApplyLoomContextFlag(program, filePath)
}

// loomContextProgram wraps program with the loom-context flag for launch.
// Returns program unchanged when the feature is disabled, configDir is
// empty, or the selected file is missing (fail-safe: never point Claude
// at a nonexistent file). Selects the workspace variant for workspace
// terminals, else the worktree variant. Non-Claude programs are a no-op
// via BuildLoomContextCommand.
func loomContextProgram(program, configDir string, isWorkspaceTerminal bool) string {
	if !loomContextEnabled.Load() || configDir == "" {
		return program
	}
	name := loomContextFileWorktree
	if isWorkspaceTerminal {
		name = loomContextFileWorkspace
	}
	path := filepath.Join(configDir, name)
	if _, err := os.Stat(path); err != nil {
		return program
	}
	return BuildLoomContextCommand(program, path)
}

// ensure the agent import is used even though only defaultRegistry (from
// agent_restart.go) references the package directly.
var _ = agent.DefaultRegistry
```

Note: `defaultRegistry` already exists in `session/agent_restart.go` (same package). The `agent` import line and the trailing `var _` are only needed if `go` reports an unused import — if `session/loom_context.go` does not reference `agent` directly, delete both the `agent` import and the `var _ = agent.DefaultRegistry` line before building.

- [ ] **Step 4: Remove the unused import if present**

Run: `CGO_ENABLED=0 go build ./session/ 2>&1 | head`
If it reports `"github.com/aidan-bailey/loom/session/agent" imported and not used`, delete the `agent` import line and the `var _ = agent.DefaultRegistry` line. Re-run until the package builds clean.

- [ ] **Step 5: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./session -run 'TestWriteLoomContextFiles|TestLoomContextProgram' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add session/loom_context.go session/loom_context_test.go
git commit -m "feat(session): add loom-context enabled flag, file writer, launch wrapper"
```

---

## Task 5: Inject at instance launch

**Files:**
- Modify: `session/instance.go` (~line 590, the `if ts == nil` branch of setup)

The selection logic is already covered by `TestLoomContextProgram` (Task 4); this task is the 2-line wiring, verified by build + the Task 8 smoke test.

- [ ] **Step 1: Apply the wrapping**

In `session/instance.go`, locate:

```go
	ts := i.getTmuxSession()
	if ts == nil {
		// Create new tmux session
		ts = tmux.NewTmuxSession(i.Title, i.Program, InstanceEnv(i.Program, i.HeadroomProxy, i.CacheTTL1h)...)
	}
```

Replace the `if ts == nil` body with:

```go
	if ts == nil {
		// Create new tmux session. loomContextProgram wraps the program
		// with --append-system-prompt-file for Claude sessions (no-op when
		// disabled, non-Claude, or the file is missing); InstanceEnv still
		// keys off the bare i.Program.
		launchProgram := loomContextProgram(i.Program, i.ConfigDir, i.IsWorkspaceTerminal)
		ts = tmux.NewTmuxSession(i.Title, launchProgram, InstanceEnv(i.Program, i.HeadroomProxy, i.CacheTTL1h)...)
	}
```

- [ ] **Step 2: Verify the package builds and existing tests pass**

Run: `CGO_ENABLED=0 go build ./session/ && CGO_ENABLED=0 go test ./session -run TestLoomContextProgram -v`
Expected: build succeeds; PASS

- [ ] **Step 3: Commit**

```bash
git add session/instance.go
git commit -m "feat(session): inject loom-context flag at instance launch"
```

---

## Task 6: App wiring — write files + sync enabled flag

**Files:**
- Modify: `app/app.go` (`activateWorkspace`, after `appConfig := config.LoadConfigFrom(wsCtx.ConfigDir)` ~line 2011)
- Modify: `app/state_settings.go` (after `SaveConfigTo` succeeds ~line 40)

No new unit test — behavior is exercised by Task 4 (`loomContextProgram`/`WriteLoomContextFiles`) and the Task 8 smoke test. Verified here by build + full test suite.

- [ ] **Step 1: Wire activation (write files + initial sync)**

In `app/app.go`, inside `activateWorkspace`, immediately after the line:

```go
	appConfig := config.LoadConfigFrom(wsCtx.ConfigDir)
```

add:

```go
	// Loom-context injection: keep the config-dir prompt files current and
	// sync the global enabled flag on every workspace load, before any
	// Claude session (workspace terminal, crash-restart, resume) launches.
	session.SetLoomContextEnabled(appConfig.LoomContextEnabled())
	if err := session.WriteLoomContextFiles(wsCtx.ConfigDir); err != nil {
		log.For("app").Warn("loom_context.write_failed", "err", err.Error())
	}
```

(`app/app.go` already imports `session` and `log`.)

- [ ] **Step 2: Wire the settings toggle (re-sync)**

In `app/state_settings.go`, add the `session` import:

```go
import (
	"fmt"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"

	tea "charm.land/bubbletea/v2"
)
```

Then, in `handleStateSettingsKey`, inside the `if changed {` block, immediately after the `m.program = m.appConfig.GetProgram()` line, add:

```go
		// Re-sync the loom-context toggle so an in-place change takes
		// effect on the next session launch without a workspace switch.
		session.SetLoomContextEnabled(m.appConfig.LoomContextEnabled())
```

- [ ] **Step 3: Verify build + full suite**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./app ./config ./session ./session/agent`
Expected: build succeeds; all PASS

- [ ] **Step 4: Commit**

```bash
git add app/app.go app/state_settings.go
git commit -m "feat(app): write loom-context files and sync enabled flag"
```

---

## Task 7: Claude Preferences toggle row

**Files:**
- Modify: `ui/overlay/claudePreferences.go`
- Test: `ui/overlay/claudePreferences_test.go`

- [ ] **Step 1: Write the failing test**

Add to `ui/overlay/claudePreferences_test.go` (the file already imports `tea`, `config`, and testify `assert`, and uses the `cp` variable name and the `tea.KeyPressMsg{Code: 'j', Text: "j"}` / `{Code: tea.KeyEnter}` idiom — matched below):

```go
func TestClaudePreferences_LoomContextToggle(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")

	// Row 6 is Loom Context. Move the cursor there from row 0.
	for i := 0; i < 6; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}

	// Default (nil) is enabled; toggling once turns it off.
	assert.True(t, cfg.LoomContextEnabled())
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.LoomContextEnabled())

	// Render shows the row.
	assert.Contains(t, cp.Render(), "Loom Context")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/overlay -run TestClaudePreferences_LoomContextToggle -v`
Expected: FAIL — cursor can't reach row 6 (rowCount is 6) and/or no "Loom Context" row rendered; assertion fails.

- [ ] **Step 3: Bump the row count and update the doc comment**

In `ui/overlay/claudePreferences.go`, change:

```go
const claudePrefsRowCount = 6
```

to:

```go
const claudePrefsRowCount = 7
```

And update the type doc comment "today it holds six rows: Remote Control, Permission Mode, Model, Headroom Proxy, Effort, and Cache TTL (1h)." to "today it holds seven rows: Remote Control, Permission Mode, Model, Headroom Proxy, Effort, Cache TTL (1h), and Loom Context."

- [ ] **Step 4: Add the toggle case**

In `HandleKeyPress`, inside the `switch c.cursor {` block, immediately after `case 5:` (the Cache TTL case ending in `})`), add:

```go
		case 6:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.LoomContextEnabled()
				cc.ClaudeLoomContext = &v
			})
```

- [ ] **Step 5: Add the render block**

In `Render`, immediately after the `cacheRow = ...` block (ending before the `content :=` assignment), add:

```go
	loomCheck := "[ ]"
	if c.cfg.LoomContextEnabled() {
		loomCheck = "[x]"
	}
	loomCursor := "  "
	if c.cursor == 6 {
		loomCursor = "> "
	}
	loomRow := loomCursor + "Loom Context      " + loomCheck
	if c.cursor == 6 {
		loomRow = claudePrefsSelectedStyle.Render(loomRow)
	} else {
		loomRow = claudePrefsRowStyle.Render(loomRow)
	}
```

Then, in the `content :=` concatenation, add `loomRow` after `cacheRow`:

```go
	content := claudePrefsTitleStyle.Render("Claude Preferences") + "\n\n" +
		rcRow + "\n" +
		pmRow + "\n" +
		modelRow + "\n" +
		hwRow + "\n" +
		effortRow + "\n" +
		cacheRow + "\n" +
		loomRow + "\n\n" +
		claudePrefsHintStyle.Render("up/down move • enter/space toggle/cycle • esc back")
```

- [ ] **Step 6: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./ui/overlay -run TestClaudePreferences_LoomContextToggle -v`
Expected: PASS

- [ ] **Step 7: Run the overlay package tests to catch row-count regressions**

Run: `CGO_ENABLED=0 go test ./ui/overlay`
Expected: PASS (existing Claude-preferences tests still green)

- [ ] **Step 8: Commit**

```bash
git add ui/overlay/claudePreferences.go ui/overlay/claudePreferences_test.go
git commit -m "feat(ui): add Loom Context toggle to Claude Preferences"
```

---

## Task 8: Full verification + manual smoke

**Files:** none (verification only)

- [ ] **Step 1: gofmt**

Run: `gofmt -l . | grep -v '^web/' || echo "clean"`
Expected: `clean` (no files listed). If any listed, run `gofmt -w <file>` and re-check.

- [ ] **Step 2: Full build**

Run: `CGO_ENABLED=0 go build -o loom`
Expected: builds, no errors.

- [ ] **Step 3: Full test suite**

Run: `CGO_ENABLED=0 go test ./...`
Expected: all PASS.

- [ ] **Step 4: Race check on the changed packages**

Run: `CC=clang CGO_ENABLED=1 go test -race ./session ./config ./ui/overlay ./app`
Expected: all PASS, no race reports. (Uses clang because the repo default build disables CGO; see CLAUDE.md.)

- [ ] **Step 5: Manual smoke — worktree session gets the worktree file**

Use the `/run` skill (or run `./loom` manually) to launch loom, create a new session (`n`), and confirm the Claude pane launched with the injected flag. Verify from a shell:

```bash
tmux list-panes -a -F '#{pane_start_command}' 2>/dev/null | grep -- '--append-system-prompt-file' || echo "NO INJECTION FOUND"
```

Expected: a line containing `--append-system-prompt-file '<configdir>/claude-loom-context.md'` for the worktree session. Also confirm `~/.loom/claude-loom-context.md` and `~/.loom/claude-loom-context-workspace.md` exist.

- [ ] **Step 6: Manual smoke — workspace terminal gets the workspace file**

Confirm the auto-created workspace terminal's command references the **workspace** variant:

```bash
tmux list-panes -a -F '#{pane_start_command}' 2>/dev/null | grep -- 'claude-loom-context-workspace.md' || echo "NO WORKSPACE INJECTION"
```

Expected: the workspace-terminal pane references `claude-loom-context-workspace.md`; a worktree session references the plain `claude-loom-context.md`.

- [ ] **Step 7: Manual smoke — toggle off suppresses injection**

Open Settings (`S`) → Claude Preferences → toggle **Loom Context** off. Create a new session and confirm its command has **no** `--append-system-prompt-file`. Toggle back on to restore the default.

- [ ] **Step 8: Final commit (if any fmt fixes were made)**

```bash
git add -A
git commit -m "chore: gofmt + verification fixups for loom-context injection" || echo "nothing to commit"
```

---

## Self-review checklist (completed by plan author)

- **Spec coverage:** config field/helper (T1), embedded content both variants (T2), adapter flag + coexistence + quoting (T3), enabled flag/file writer/launch wrapper/fail-safe (T4), launch injection gated on `IsWorkspaceTerminal` (T5), app wiring on activation + settings toggle (T6), Claude Preferences toggle (T7), verification incl. race + manual per-variant smoke (T8). No `InstanceData`/schema change — none introduced. ✔
- **Placeholder scan:** every code step shows complete code; the only conditional is the T4 unused-import guard, which has an explicit build-and-delete procedure. ✔
- **Type/name consistency:** `ClaudeLoomContext`/`LoomContextEnabled`, `SetLoomContextEnabled`/`WriteLoomContextFiles`/`loomContextProgram`/`BuildLoomContextCommand`/`ApplyLoomContextFlag`, files `claude-loom-context.md`/`claude-loom-context-workspace.md`, row index 6 / count 7 — used identically across T1–T8. ✔
