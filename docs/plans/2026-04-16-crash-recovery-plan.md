# Crash Recovery Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make claude-squad resilient to unclean shutdowns (OOM, SIGKILL) by reconciling persisted state with reality on startup, preserving scrollback history to disk, and restarting agents with conversation context.

**Architecture:** Four layers — (1) startup reconciliation checks tmux/worktree health before restoring instances, (2) periodic scrollback snapshots write tmux pane content to disk, (3) agent-aware restart appends `--continue` to Claude programs after crash recovery, (4) checkpoint saves during Pause/Resume reduce the inconsistency window.

**Tech Stack:** Go, tmux CLI, existing `AtomicWriteFile`, `testify/assert`, mock `cmd.Executor`

---

## Task 1: Reconciliation — Health Check Helpers

Add functions to check whether a tmux session and worktree exist for a given instance, without requiring a fully constructed TmuxSession or started Instance.

**Files:**
- Create: `session/reconcile.go`
- Create: `session/reconcile_test.go`

**Step 1: Write the failing tests**

```go
// session/reconcile_test.go
package session

import (
	"os"
	"os/exec"
	"testing"

	"claude-squad/cmd/cmd_test"
	"claude-squad/session/tmux"

	"github.com/stretchr/testify/assert"
)

func TestCheckTmuxAlive_SessionExists(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error { return nil }, // has-session succeeds
	}
	assert.True(t, CheckTmuxAlive("test-session", cmdExec))
}

func TestCheckTmuxAlive_SessionDead(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			return &exec.ExitError{}
		},
	}
	assert.False(t, CheckTmuxAlive("test-session", cmdExec))
}

func TestCheckWorktreeExists_Exists(t *testing.T) {
	dir := t.TempDir()
	assert.True(t, CheckWorktreeExists(dir))
}

func TestCheckWorktreeExists_Missing(t *testing.T) {
	assert.False(t, CheckWorktreeExists("/nonexistent/path/worktree"))
}

func TestDetermineRecoveryAction(t *testing.T) {
	tests := []struct {
		name       string
		status     Status
		tmuxAlive  bool
		wtExists   bool
		isWsTerm   bool
		expected   RecoveryAction
	}{
		{"paused_no_change", Paused, false, false, false, ActionNoChange},
		{"running_all_healthy", Running, true, true, false, ActionRestore},
		{"running_tmux_dead_wt_exists", Running, false, true, false, ActionRestart},
		{"running_tmux_dead_wt_gone", Running, false, false, false, ActionMarkPaused},
		{"running_tmux_alive_wt_gone", Running, true, false, false, ActionKillAndPause},
		{"ws_terminal_tmux_dead", Running, false, false, true, ActionRestartWsTerminal},
		{"ready_tmux_dead", Ready, false, true, false, ActionRestart},
		{"prompting_tmux_dead", Prompting, false, false, false, ActionMarkPaused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := DetermineRecoveryAction(tt.status, tt.tmuxAlive, tt.wtExists, tt.isWsTerm)
			assert.Equal(t, tt.expected, action)
		})
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run TestCheck ./session/`
Expected: FAIL — `CheckTmuxAlive`, `CheckWorktreeExists`, `DetermineRecoveryAction` undefined

**Step 3: Write minimal implementation**

```go
// session/reconcile.go
package session

import (
	"claude-squad/cmd"
	"claude-squad/session/tmux"
	"os"
	"os/exec"
)

// RecoveryAction describes what to do with an instance during startup reconciliation.
type RecoveryAction int

const (
	// ActionNoChange means the instance is already in a consistent state (e.g. Paused).
	ActionNoChange RecoveryAction = iota
	// ActionRestore means tmux + worktree are healthy; do a normal restore.
	ActionRestore
	// ActionRestart means tmux is dead but worktree exists; restart with scrollback + agent context.
	ActionRestart
	// ActionMarkPaused means both tmux and worktree are gone; mark as Paused (branch is preserved).
	ActionMarkPaused
	// ActionKillAndPause means tmux is alive but worktree is gone; kill tmux, mark Paused.
	ActionKillAndPause
	// ActionRestartWsTerminal means workspace terminal's tmux is dead; recreate it.
	ActionRestartWsTerminal
)

// CheckTmuxAlive checks if a tmux session exists by its sanitized name.
func CheckTmuxAlive(sessionTitle string, cmdExec cmd.Executor) bool {
	sanitized := tmux.ToClaudeSquadTmuxName(sessionTitle)
	existsCmd := exec.Command("tmux", "has-session", "-t="+sanitized)
	return cmdExec.Run(existsCmd) == nil
}

// CheckWorktreeExists checks if the worktree directory exists on disk.
func CheckWorktreeExists(worktreePath string) bool {
	if worktreePath == "" {
		return false
	}
	_, err := os.Stat(worktreePath)
	return err == nil
}

// DetermineRecoveryAction decides what to do with a loaded instance based on
// its persisted status and current filesystem/tmux state.
func DetermineRecoveryAction(status Status, tmuxAlive, worktreeExists, isWorkspaceTerminal bool) RecoveryAction {
	if status == Paused {
		return ActionNoChange
	}

	if isWorkspaceTerminal {
		if tmuxAlive {
			return ActionRestore
		}
		return ActionRestartWsTerminal
	}

	switch {
	case tmuxAlive && worktreeExists:
		return ActionRestore
	case tmuxAlive && !worktreeExists:
		return ActionKillAndPause
	case !tmuxAlive && worktreeExists:
		return ActionRestart
	default: // !tmuxAlive && !worktreeExists
		return ActionMarkPaused
	}
}
```

Note: `tmux.ToClaudeSquadTmuxName` is currently unexported (`toClaudeSquadTmuxName`). Export it:

In `session/tmux/tmux.go:75`, rename `toClaudeSquadTmuxName` → `ToClaudeSquadTmuxName` and update all callers (grep for `toClaudeSquadTmuxName`).

**Step 4: Run tests to verify they pass**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run "TestCheck|TestDetermine" ./session/`
Expected: PASS

**Step 5: Commit**

```bash
git add session/reconcile.go session/reconcile_test.go session/tmux/tmux.go
git commit -m "feat(session): add crash recovery health check and action determination"
```

---

## Task 2: Reconciliation — Integrate Into Startup

Replace the current `FromInstanceData` → `Start(false)` path in `newHome` with a reconciliation loop.

**Files:**
- Modify: `session/instance.go:147-198` (FromInstanceData)
- Modify: `session/reconcile.go` (add ReconcileAndRestore)
- Modify: `app/app.go:208-224` (newHome instance loading)

**Step 1: Write the failing test**

Add to `session/reconcile_test.go`:

```go
func TestReconcileInstance_DeadTmux_MarkedPaused(t *testing.T) {
	// Simulate: instance was Running, tmux is dead, no worktree
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return &exec.ExitError{} }, // tmux dead
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}

	data := InstanceData{
		Title:   "dead-session",
		Path:    t.TempDir(),
		Branch:  "test-branch",
		Status:  Running,
		Program: "claude",
	}

	instance, err := ReconcileAndRestore(data, "", cmdExec)
	assert.NoError(t, err)
	assert.Equal(t, Paused, instance.GetStatus())
}
```

**Step 2: Run test to verify it fails**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run TestReconcileInstance ./session/`
Expected: FAIL — `ReconcileAndRestore` undefined

**Step 3: Implement ReconcileAndRestore and refactor FromInstanceData**

Add to `session/reconcile.go`:

```go
// ReconcileAndRestore loads an instance from serialized data, checks the health
// of its tmux session and worktree, and takes the appropriate recovery action.
// This replaces the direct FromInstanceData path for startup loading.
func ReconcileAndRestore(data InstanceData, configDir string, cmdExec cmd.Executor) (*Instance, error) {
	tmuxAlive := CheckTmuxAlive(data.Title, cmdExec)
	wtExists := CheckWorktreeExists(data.Worktree.WorktreePath)
	action := DetermineRecoveryAction(data.Status, tmuxAlive, wtExists, data.IsWorkspaceTerminal)

	switch action {
	case ActionNoChange:
		return fromInstanceDataPaused(data, configDir)

	case ActionRestore:
		return FromInstanceData(data, configDir)

	case ActionRestart:
		instance, err := fromInstanceDataPaused(data, configDir)
		if err != nil {
			return nil, err
		}
		instance.CrashRecovered = true
		return instance, nil

	case ActionMarkPaused:
		data.Status = Paused
		return fromInstanceDataPaused(data, configDir)

	case ActionKillAndPause:
		// Kill the orphaned tmux session
		sanitized := tmux.ToClaudeSquadTmuxName(data.Title)
		killCmd := exec.Command("tmux", "kill-session", "-t="+sanitized)
		_ = cmdExec.Run(killCmd) // best-effort
		data.Status = Paused
		return fromInstanceDataPaused(data, configDir)

	case ActionRestartWsTerminal:
		// Create the instance as paused-like, caller will Start it
		instance, err := fromInstanceDataPaused(data, configDir)
		if err != nil {
			return nil, err
		}
		instance.CrashRecovered = true
		return instance, nil

	default:
		return nil, fmt.Errorf("unknown recovery action: %d", action)
	}
}
```

Extract `fromInstanceDataPaused` from `FromInstanceData` — the branch that creates the instance without calling `Start(false)`:

In `session/instance.go`, extract the common instance construction into a helper:

```go
// fromInstanceDataPaused creates an Instance from serialized data in a paused/stopped
// state. It sets started=true and creates a TmuxSession object but does not connect.
func fromInstanceDataPaused(data InstanceData, configDir string) (*Instance, error) {
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
		AutoYes:             data.AutoYes,
		ConfigDir:           configDir,
		IsWorkspaceTerminal: data.IsWorkspaceTerminal,
	}

	if !data.IsWorkspaceTerminal {
		instance.setGitWorktree(git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
			data.Worktree.IsExistingBranch,
			configDir,
		))
	}

	if data.DiffStats.Added != 0 || data.DiffStats.Removed != 0 || data.DiffStats.Content != "" {
		instance.setDiffStats(&git.DiffStats{
			Added:   data.DiffStats.Added,
			Removed: data.DiffStats.Removed,
			Content: data.DiffStats.Content,
		})
	}

	instance.setStarted(true)
	instance.setTmuxSession(tmux.NewTmuxSession(instance.Title, instance.Program))
	return instance, nil
}
```

Add `CrashRecovered` field to Instance struct (instance.go:63, after `IsWorkspaceTerminal`):

```go
// CrashRecovered is a runtime-only flag set when this instance was restored
// after a crash. Used to trigger agent-aware restart (e.g. --continue).
CrashRecovered bool
```

Modify `newHome` (app.go:208-224) to use `ReconcileAndRestore`:

```go
cmdExec := cmd.MakeExecutor()
instances, err := storage.LoadInstanceData() // new method: returns []InstanceData without constructing instances
if err != nil {
	fmt.Printf("Failed to load instances: %v\n", err)
	os.Exit(1)
}

hasWorkspaceTerminal := false
for _, data := range instances {
	if data.IsWorkspaceTerminal {
		hasWorkspaceTerminal = true
	}
	instance, err := session.ReconcileAndRestore(data, cfgDir, cmdExec)
	if err != nil {
		log.ErrorLog.Printf("failed to reconcile instance %q: %v (skipping)", data.Title, err)
		continue // skip broken instance instead of os.Exit(1)
	}
	h.list.AddInstance(instance)()
	if autoYes {
		instance.AutoYes = true
	}
}
```

Add `LoadInstanceData()` to `session/storage.go`:

```go
// LoadInstanceData loads raw serialized instance data without constructing Instance objects.
// Used by reconciliation to inspect state before deciding how to restore.
func (s *Storage) LoadInstanceData() ([]InstanceData, error) {
	jsonData := s.state.GetInstances()
	var data []InstanceData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instances: %w", err)
	}
	return data, nil
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run TestReconcile ./session/`
Expected: PASS

Run: `cd /tb/Source/Personal/claude-squad && go test -v ./...`
Expected: All tests PASS (no regressions)

**Step 5: Commit**

```bash
git add session/reconcile.go session/reconcile_test.go session/instance.go session/storage.go app/app.go
git commit -m "feat(app): reconcile instance state on startup for crash recovery"
```

---

## Task 3: Reconciliation — Orphan Cleanup

After reconciling known instances, kill any orphaned `cs-*` tmux sessions not claimed by a loaded instance.

**Files:**
- Modify: `session/reconcile.go` (add CleanupOrphanedSessions)
- Modify: `session/reconcile_test.go`
- Modify: `app/app.go` (call after instance loading)

**Step 1: Write the failing test**

Add to `session/reconcile_test.go`:

```go
func TestCleanupOrphanedSessions(t *testing.T) {
	killedSessions := []string{}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			// Track kill-session calls
			for i, arg := range c.Args {
				if arg == "kill-session" && i+2 < len(c.Args) {
					killedSessions = append(killedSessions, c.Args[i+2])
				}
			}
			return nil
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			// Simulate tmux ls output: two sessions, one claimed, one orphaned
			return []byte("claudesquad_claimed: 1 windows\nclaudesquad_orphan: 1 windows\nother_session: 1 windows\n"), nil
		},
	}

	claimedTitles := map[string]bool{"claimed": true}
	err := CleanupOrphanedSessions(claimedTitles, cmdExec)
	assert.NoError(t, err)
	assert.Contains(t, killedSessions, "claudesquad_orphan")
	assert.NotContains(t, killedSessions, "claudesquad_claimed")
	assert.NotContains(t, killedSessions, "other_session")
}
```

**Step 2: Run test to verify it fails**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run TestCleanupOrphaned ./session/`
Expected: FAIL — `CleanupOrphanedSessions` undefined

**Step 3: Implement**

Add to `session/reconcile.go`:

```go
// CleanupOrphanedSessions kills any tmux sessions with the claude-squad prefix
// that are not claimed by a loaded instance. This prevents tmux session leaks
// across crashes.
func CleanupOrphanedSessions(claimedTitles map[string]bool, cmdExec cmd.Executor) error {
	listCmd := exec.Command("tmux", "ls")
	output, err := cmdExec.Output(listCmd)
	if err != nil {
		// No tmux server running — nothing to clean up
		return nil
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, tmux.TmuxPrefix) {
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		sessionName := line[:colonIdx]

		// Check if any claimed instance owns this session
		claimed := false
		for title := range claimedTitles {
			if tmux.ToClaudeSquadTmuxName(title) == sessionName {
				claimed = true
				break
			}
		}

		if !claimed {
			log.InfoLog.Printf("killing orphaned tmux session: %s", sessionName)
			killCmd := exec.Command("tmux", "kill-session", "-t", sessionName)
			if err := cmdExec.Run(killCmd); err != nil {
				log.ErrorLog.Printf("failed to kill orphaned session %s: %v", sessionName, err)
			}
		}
	}
	return nil
}
```

Wire into `app.go` after the instance loading loop:

```go
// Clean up orphaned tmux sessions from previous crashes
claimedTitles := make(map[string]bool)
for _, inst := range h.list.GetInstances() {
	claimedTitles[inst.Title] = true
}
if err := session.CleanupOrphanedSessions(claimedTitles, cmdExec); err != nil {
	log.ErrorLog.Printf("orphan cleanup failed: %v", err)
}
```

**Step 4: Run tests**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run TestCleanupOrphaned ./session/`
Expected: PASS

Run: `cd /tb/Source/Personal/claude-squad && go test -v ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add session/reconcile.go session/reconcile_test.go app/app.go
git commit -m "feat(session): clean up orphaned tmux sessions on startup"
```

---

## Task 4: Scrollback Snapshots — Write and Read

Add functions to save and load scrollback snapshots to/from `~/.claude-squad/scrollback/`.

**Files:**
- Create: `session/scrollback.go`
- Create: `session/scrollback_test.go`

**Step 1: Write the failing tests**

```go
// session/scrollback_test.go
package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSaveAndLoadScrollback(t *testing.T) {
	dir := t.TempDir()
	mgr := NewScrollbackManager(dir)

	content := "line 1\nline 2\nline 3\n"
	err := mgr.Save("test-session", content)
	assert.NoError(t, err)

	loaded, err := mgr.Load("test-session")
	assert.NoError(t, err)
	assert.Equal(t, content, loaded)
}

func TestLoadScrollback_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewScrollbackManager(dir)

	loaded, err := mgr.Load("nonexistent")
	assert.NoError(t, err)
	assert.Equal(t, "", loaded)
}

func TestDeleteScrollback(t *testing.T) {
	dir := t.TempDir()
	mgr := NewScrollbackManager(dir)

	_ = mgr.Save("test-session", "data")
	err := mgr.Delete("test-session")
	assert.NoError(t, err)

	loaded, _ := mgr.Load("test-session")
	assert.Equal(t, "", loaded)
}

func TestDeleteAllScrollback(t *testing.T) {
	dir := t.TempDir()
	mgr := NewScrollbackManager(dir)

	_ = mgr.Save("session-1", "data1")
	_ = mgr.Save("session-2", "data2")

	err := mgr.DeleteAll()
	assert.NoError(t, err)

	entries, _ := os.ReadDir(filepath.Join(dir, "scrollback"))
	assert.Empty(t, entries)
}

func TestSaveScrollback_SkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	mgr := NewScrollbackManager(dir)

	content := "same content"
	err := mgr.Save("test-session", content)
	assert.NoError(t, err)

	// Get mod time
	path := filepath.Join(dir, "scrollback", "test-session.log")
	info1, _ := os.Stat(path)

	// Save same content again — file should not be rewritten
	err = mgr.Save("test-session", content)
	assert.NoError(t, err)

	info2, _ := os.Stat(path)
	assert.Equal(t, info1.ModTime(), info2.ModTime(), "file should not be rewritten for unchanged content")
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run TestSave.*Scrollback -run TestLoad.*Scrollback -run TestDelete.*Scrollback ./session/`
Expected: FAIL — `NewScrollbackManager` undefined

**Step 3: Implement**

```go
// session/scrollback.go
package session

import (
	"claude-squad/config"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ScrollbackManager handles saving and loading tmux scrollback snapshots to disk.
type ScrollbackManager struct {
	baseDir string
	// hashes tracks content hashes to skip unchanged writes.
	hashes map[string][32]byte
	mu     sync.Mutex
}

// NewScrollbackManager creates a new ScrollbackManager rooted at baseDir.
// Scrollback files are stored in baseDir/scrollback/.
func NewScrollbackManager(baseDir string) *ScrollbackManager {
	return &ScrollbackManager{
		baseDir: baseDir,
		hashes:  make(map[string][32]byte),
	}
}

func (m *ScrollbackManager) dir() string {
	return filepath.Join(m.baseDir, "scrollback")
}

func (m *ScrollbackManager) path(sessionTitle string) string {
	// Sanitize title for filesystem safety
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, sessionTitle)
	return filepath.Join(m.dir(), safe+".log")
}

// Save writes scrollback content for a session. Skips the write if content
// is unchanged since the last save (hash comparison).
func (m *ScrollbackManager) Save(sessionTitle string, content string) error {
	hash := sha256.Sum256([]byte(content))

	m.mu.Lock()
	if prev, ok := m.hashes[sessionTitle]; ok && prev == hash {
		m.mu.Unlock()
		return nil
	}
	m.hashes[sessionTitle] = hash
	m.mu.Unlock()

	if err := os.MkdirAll(m.dir(), 0o755); err != nil {
		return fmt.Errorf("create scrollback dir: %w", err)
	}

	return config.AtomicWriteFile(m.path(sessionTitle), []byte(content), 0o644)
}

// Load reads the saved scrollback for a session. Returns empty string if not found.
func (m *ScrollbackManager) Load(sessionTitle string) (string, error) {
	data, err := os.ReadFile(m.path(sessionTitle))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read scrollback: %w", err)
	}
	return string(data), nil
}

// Delete removes the scrollback file for a session.
func (m *ScrollbackManager) Delete(sessionTitle string) error {
	m.mu.Lock()
	delete(m.hashes, sessionTitle)
	m.mu.Unlock()

	err := os.Remove(m.path(sessionTitle))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// DeleteAll removes all scrollback files.
func (m *ScrollbackManager) DeleteAll() error {
	m.mu.Lock()
	m.hashes = make(map[string][32]byte)
	m.mu.Unlock()

	return os.RemoveAll(m.dir())
}
```

**Step 4: Run tests**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run "Scrollback" ./session/`
Expected: PASS

**Step 5: Commit**

```bash
git add session/scrollback.go session/scrollback_test.go
git commit -m "feat(session): add scrollback snapshot manager with hash-based dedup"
```

---

## Task 5: Scrollback Snapshots — Periodic Capture in Metadata Tick

Wire the scrollback manager into the metadata tick to capture snapshots every 30 seconds.

**Files:**
- Modify: `app/app.go:173-278` (newHome — create ScrollbackManager)
- Modify: `app/app.go:388-467` (tickUpdateMetadata — add snapshot logic)

**Step 1: Add ScrollbackManager to home struct and initialize it**

In `app/app.go`, add to the `home` struct (around line 130):

```go
scrollbackMgr *session.ScrollbackManager
scrollbackTick int // counter to throttle snapshots
```

In `newHome` (after storage creation, around line 185):

```go
scrollbackDir := ""
if wsCtx != nil {
	scrollbackDir = wsCtx.ConfigDir
} else {
	scrollbackDir, _ = config.GetConfigDir()
}
h.scrollbackMgr = session.NewScrollbackManager(scrollbackDir)
```

**Step 2: Add snapshot capture to metadata tick**

In the metadata tick handler (app.go:438-466), after the results loop, add:

```go
// Scrollback snapshots every ~30 seconds (60 ticks at 500ms each)
m.scrollbackTick++
if m.scrollbackTick >= 60 {
	m.scrollbackTick = 0
	for _, r := range results {
		if r.tmuxAlive {
			content, err := r.instance.PreviewFullHistory()
			if err != nil {
				log.WarningLog.Printf("scrollback capture for %q: %v", r.instance.Title, err)
				continue
			}
			if err := m.scrollbackMgr.Save(r.instance.Title, content); err != nil {
				log.WarningLog.Printf("scrollback save for %q: %v", r.instance.Title, err)
			}
		}
	}
}
```

**Step 3: Add cleanup to Kill and reset paths**

In `app/app.go`, where instance Kill is handled (around line 1083):

```go
// After successful kill, clean up scrollback
if m.scrollbackMgr != nil {
	_ = m.scrollbackMgr.Delete(selected.Title)
}
```

In `main.go` reset command (around line 178, after worktree cleanup):

```go
// Clean up scrollback snapshots
scrollbackDir := filepath.Join(configDir, "scrollback")
if err := os.RemoveAll(scrollbackDir); err != nil {
	log.ErrorLog.Printf("failed to clean scrollback: %v", err)
}
fmt.Println("Scrollback snapshots have been cleaned up")
```

**Step 4: Run full test suite**

Run: `cd /tb/Source/Personal/claude-squad && go test -v ./...`
Expected: PASS

**Step 5: Build and verify**

Run: `cd /tb/Source/Personal/claude-squad && CGO_ENABLED=0 go build -o claude-squad`
Expected: Build succeeds

**Step 6: Commit**

```bash
git add app/app.go main.go
git commit -m "feat(app): periodic scrollback snapshots every 30s with cleanup"
```

---

## Task 6: Scrollback Restoration on Crash Recovery

When reconciliation detects a dead tmux + existing worktree (ActionRestart), restore the saved scrollback by prepending it to the new tmux session.

**Files:**
- Modify: `session/tmux/tmux.go` (add StartWithScrollback method)
- Modify: `session/instance.go` (use StartWithScrollback when CrashRecovered)
- Create: `session/tmux/tmux_test.go` (if not exists, add test)

**Step 1: Add StartWithScrollback to TmuxSession**

In `session/tmux/tmux.go`, add after `Start()`:

```go
// StartWithScrollback starts a tmux session that first displays saved scrollback
// content, then launches the program. The scrollback file is displayed via cat
// before exec-ing the actual program, so the user can scroll up to see history.
func (t *TmuxSession) StartWithScrollback(workDir string, scrollbackPath string) error {
	if t.DoesSessionExist() {
		return fmt.Errorf("tmux session already exists: %s", t.sanitizedName)
	}

	// Wrap the program: cat scrollback then exec program
	wrappedProgram := fmt.Sprintf("cat %q; exec %s", scrollbackPath, t.program)
	shellCmd := fmt.Sprintf("bash -c %q", wrappedProgram)

	cmd := exec.Command("tmux", "new-session", "-d", "-s", t.sanitizedName, "-c", workDir, "bash", "-c", wrappedProgram)
	ptmx, err := t.ptyFactory.Start(cmd)
	if err != nil {
		if t.DoesSessionExist() {
			cleanupCmd := exec.Command("tmux", "kill-session", "-t", t.sanitizedName)
			if cleanupErr := t.cmdExec.Run(cleanupCmd); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
		}
		return fmt.Errorf("error starting tmux session with scrollback: %w", err)
	}

	// Same polling and setup as Start()
	timeout := time.After(2 * time.Second)
	sleepDuration := 5 * time.Millisecond
	for !t.DoesSessionExist() {
		select {
		case <-timeout:
			if cleanupErr := t.Close(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			return fmt.Errorf("timed out waiting for tmux session %s: %v", t.sanitizedName, err)
		default:
			time.Sleep(sleepDuration)
			if sleepDuration < 50*time.Millisecond {
				sleepDuration *= 2
			}
		}
	}
	ptmx.Close()

	historyCmd := exec.Command("tmux", "set-option", "-t", t.sanitizedName, "history-limit", "10000")
	if err := t.cmdExec.Run(historyCmd); err != nil {
		log.WarningLog.Printf("failed to set history-limit for session %s: %v", t.sanitizedName, err)
	}

	mouseCmd := exec.Command("tmux", "set-option", "-t", t.sanitizedName, "mouse", "on")
	if err := t.cmdExec.Run(mouseCmd); err != nil {
		log.WarningLog.Printf("failed to enable mouse scrolling for session %s: %v", t.sanitizedName, err)
	}

	err = t.Restore()
	if err != nil {
		if cleanupErr := t.Close(); cleanupErr != nil {
			err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
		}
		return fmt.Errorf("error restoring tmux session: %w", err)
	}

	return nil
}
```

**Step 2: Wire CrashRecovered instances to use scrollback on Resume**

In the reconciliation flow (app.go, where CrashRecovered instances that need restart are handled), after loading the instance, start it with scrollback awareness.

Modify `app/app.go` in the instance loading loop. After `ReconcileAndRestore`, for instances with `CrashRecovered=true` that need a restart:

```go
if instance.CrashRecovered && !instance.Paused() {
	// Try to restore with scrollback
	scrollback, _ := h.scrollbackMgr.Load(instance.Title)
	if err := instance.StartWithScrollback(scrollback); err != nil {
		log.ErrorLog.Printf("crash-recovery start for %q failed: %v", instance.Title, err)
		instance.SetStatus(session.Paused) // fall back to Paused
	}
}
```

Add `StartWithScrollback` method to Instance (in `session/instance.go`):

```go
// StartWithScrollback starts the instance, restoring saved scrollback content
// into the tmux session. Used for crash recovery.
func (i *Instance) StartWithScrollback(scrollbackContent string) error {
	ts := i.getTmuxSession()
	if ts == nil {
		ts = tmux.NewTmuxSession(i.Title, i.Program)
		i.setTmuxSession(ts)
	}

	gw := i.getGitWorktree()
	workDir := i.Path
	if gw != nil {
		workDir = gw.GetWorktreePath()
	}

	var err error
	if scrollbackContent != "" {
		// Write scrollback to temp file
		tmpFile, tmpErr := os.CreateTemp("", "cs-scrollback-*.log")
		if tmpErr != nil {
			// Fall back to normal start
			err = ts.Start(workDir)
		} else {
			tmpFile.WriteString(scrollbackContent)
			tmpFile.Close()
			defer os.Remove(tmpFile.Name())
			err = ts.StartWithScrollback(workDir, tmpFile.Name())
		}
	} else {
		err = ts.Start(workDir)
	}

	if err != nil {
		return err
	}

	i.setStarted(true)
	i.SetStatus(Running)
	return nil
}
```

**Step 3: Run full test suite**

Run: `cd /tb/Source/Personal/claude-squad && go test -v ./...`
Expected: PASS

**Step 4: Build**

Run: `cd /tb/Source/Personal/claude-squad && CGO_ENABLED=0 go build -o claude-squad`
Expected: Build succeeds

**Step 5: Commit**

```bash
git add session/tmux/tmux.go session/instance.go app/app.go
git commit -m "feat(session): restore scrollback content on crash recovery restart"
```

---

## Task 7: Agent-Aware Restart (--continue flag)

When `CrashRecovered` is true and the program is `claude`, append `--continue` to resume the conversation.

**Files:**
- Create: `session/agent_restart.go`
- Create: `session/agent_restart_test.go`

**Step 1: Write the failing tests**

```go
// session/agent_restart_test.go
package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildRecoveryCommand_Claude(t *testing.T) {
	assert.Equal(t, "claude --continue", BuildRecoveryCommand("claude"))
}

func TestBuildRecoveryCommand_ClaudeWithFlags(t *testing.T) {
	assert.Equal(t, "claude --continue --model sonnet", BuildRecoveryCommand("claude --model sonnet"))
}

func TestBuildRecoveryCommand_ClaudeAlreadyHasContinue(t *testing.T) {
	assert.Equal(t, "claude --continue", BuildRecoveryCommand("claude --continue"))
}

func TestBuildRecoveryCommand_ClaudeAlreadyHasResume(t *testing.T) {
	assert.Equal(t, "claude --resume", BuildRecoveryCommand("claude --resume"))
}

func TestBuildRecoveryCommand_Aider(t *testing.T) {
	assert.Equal(t, "aider --model gemma", BuildRecoveryCommand("aider --model gemma"))
}

func TestBuildRecoveryCommand_Unknown(t *testing.T) {
	assert.Equal(t, "codex", BuildRecoveryCommand("codex"))
}

func TestBuildRecoveryCommand_ClaudeSubstring(t *testing.T) {
	// "claudette" should NOT match
	assert.Equal(t, "claudette", BuildRecoveryCommand("claudette"))
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run TestBuildRecovery ./session/`
Expected: FAIL — `BuildRecoveryCommand` undefined

**Step 3: Implement**

```go
// session/agent_restart.go
package session

import "strings"

// BuildRecoveryCommand modifies a program command string for crash recovery.
// For supported agents (claude), it appends resume flags (--continue).
// Unsupported agents are returned unchanged.
func BuildRecoveryCommand(program string) string {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}

	base := parts[0]

	// Only modify claude commands
	if base != "claude" {
		return program
	}

	// Don't add if already has --continue or --resume
	for _, p := range parts[1:] {
		if p == "--continue" || p == "--resume" {
			return program
		}
	}

	// Insert --continue after "claude"
	return parts[0] + " --continue" + strings.TrimPrefix(program, parts[0])
}
```

**Step 4: Run tests**

Run: `cd /tb/Source/Personal/claude-squad && go test -v -run TestBuildRecovery ./session/`
Expected: PASS

**Step 5: Wire into StartWithScrollback**

In `session/instance.go` `StartWithScrollback()`, when `i.CrashRecovered` is true, modify the program before creating the tmux session:

```go
func (i *Instance) StartWithScrollback(scrollbackContent string) error {
	program := i.Program
	if i.CrashRecovered {
		program = BuildRecoveryCommand(program)
	}

	ts := tmux.NewTmuxSession(i.Title, program)
	i.setTmuxSession(ts)
	// ... rest of method
}
```

**Step 6: Run full test suite and build**

Run: `cd /tb/Source/Personal/claude-squad && go test -v ./... && CGO_ENABLED=0 go build -o claude-squad`
Expected: PASS + build succeeds

**Step 7: Commit**

```bash
git add session/agent_restart.go session/agent_restart_test.go session/instance.go
git commit -m "feat(session): append --continue to claude on crash recovery restart"
```

---

## Task 8: Checkpoint Saves in Pause/Resume

Add a `SaveFunc` callback parameter to `Pause()` and `Resume()` so the caller can persist state at key transition points.

**Files:**
- Modify: `session/instance.go:611-727` (Pause and Resume signatures)
- Modify: `app/app.go` (all Pause/Resume call sites)
- Modify: `session/instance_lifecycle_test.go` (if Pause/Resume are tested)

**Step 1: Modify Pause signature and add checkpoint save**

In `session/instance.go`, change `Pause()` signature:

```go
func (i *Instance) Pause(saveState func() error) error {
```

After the successful commit (line 636), add the checkpoint:

```go
	} else if dirty {
		commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
		if err := gw.CommitChanges(commitMsg); err != nil {
			errs = append(errs, fmt.Errorf("failed to commit changes: %w", err))
			return i.combineErrors(errs)
		}
	}

	// Checkpoint: mark as Paused immediately after commit succeeds.
	// If we crash after this point, the instance is safely Paused.
	i.SetStatus(Paused)
	if saveState != nil {
		if err := saveState(); err != nil {
			log.WarningLog.Printf("checkpoint save during pause: %v", err)
		}
	}
```

Remove the `i.SetStatus(Paused)` at the end (line 664) since it's now earlier.

**Step 2: Modify Resume signature and add checkpoint save**

Change `Resume()` signature:

```go
func (i *Instance) Resume(saveState func() error) error {
```

After successful tmux start/restore, before `i.SetStatus(Running)` (line 725):

```go
	i.SetStatus(Running)
	if saveState != nil {
		if err := saveState(); err != nil {
			log.WarningLog.Printf("checkpoint save during resume: %v", err)
		}
	}
	return nil
```

**Step 3: Update all call sites in app.go**

Search for `.Pause()` and `.Resume()` calls in `app/app.go` and pass the save callback:

```go
// For Pause calls:
saveFunc := func() error {
	return m.storage.SaveInstances(persistableInstances(m.list.GetInstances()))
}
if err := selected.Pause(saveFunc); err != nil {
```

```go
// For Resume calls:
saveFunc := func() error {
	return m.storage.SaveInstances(persistableInstances(m.list.GetInstances()))
}
if err := selected.Resume(saveFunc); err != nil {
```

**Step 4: Run full test suite**

Run: `cd /tb/Source/Personal/claude-squad && go test -v ./...`
Expected: PASS (update any tests that call Pause()/Resume() to pass `nil`)

**Step 5: Build**

Run: `cd /tb/Source/Personal/claude-squad && CGO_ENABLED=0 go build -o claude-squad`
Expected: Build succeeds

**Step 6: Commit**

```bash
git add session/instance.go app/app.go
git commit -m "feat(session): checkpoint save during Pause/Resume for crash resilience"
```

---

## Task 9: End-to-End Verification

Manual testing to verify crash recovery works correctly.

**Step 1: Build and start**

```bash
cd /tb/Source/Personal/claude-squad && CGO_ENABLED=0 go build -o claude-squad && ./claude-squad
```

**Step 2: Create test instances**

- Press `n` to create a new instance, give it a title
- Let the agent run for a bit so there's scrollback content
- Wait 30+ seconds for a scrollback snapshot to be saved

**Step 3: Verify scrollback was saved**

In another terminal:
```bash
ls -la ~/.claude-squad/scrollback/
```
Expected: `.log` file(s) for active session(s)

**Step 4: Simulate crash**

```bash
kill -9 $(pgrep claude-squad)
```

**Step 5: Restart and verify recovery**

```bash
./claude-squad
```

Expected:
- App starts without errors
- Previously running instances are either restored or marked Paused
- Workspace terminal is functional
- If an instance was restarted, scrollback history is visible when scrolling up
- If the program was `claude`, it should have `--continue` in the command

**Step 6: Verify orphan cleanup**

```bash
tmux ls
```
Expected: No orphaned `claudesquad_*` sessions from the killed process

**Step 7: Final commit**

```bash
git add -A
git commit -m "feat: crash recovery with startup reconciliation, scrollback snapshots, and agent-aware restart"
```
