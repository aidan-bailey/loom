package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFromInstanceData_NoPTY_ForRunning asserts that FromInstanceData
// does not spawn a tmux PTY attachment for Running-status instances.
// Callers must opt into the PTY via EnsureRunning. This fixes DAEMON-05:
// the daemon's per-tick reload no longer restarts every instance.
func TestFromInstanceData_NoPTY_ForRunning(t *testing.T) {
	data := InstanceData{
		Title:               "running-ws",
		Status:              Running,
		IsWorkspaceTerminal: true, // skip worktree setup
		Program:             "true",
	}

	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)
	assert.False(t, inst.isStarted(), "FromInstanceData must not start a Running instance")
	assert.Nil(t, inst.getTmuxSession(), "FromInstanceData must not create a TmuxSession for Running")
}

// TestFromInstanceData_Paused_PreservesShape asserts that paused
// instances still come back fully constructed (started=true, TmuxSession
// object present, no PTY attachment) — unchanged from prior behaviour.
func TestFromInstanceData_Paused_PreservesShape(t *testing.T) {
	data := InstanceData{
		Title:               "paused-ws",
		Status:              Paused,
		IsWorkspaceTerminal: true,
		Program:             "true",
	}

	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)
	assert.True(t, inst.isStarted(), "Paused instance should be marked started")
	assert.NotNil(t, inst.getTmuxSession(), "Paused instance should have a TmuxSession object")
}

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

// TestFromInstanceData_PreservesCacheTTL1h asserts the CacheTTL1h
// toggle survives a Snapshot → FromInstanceData round trip, mirroring
// TestFromInstanceData_PreservesHeadroomProxy.
func TestFromInstanceData_PreservesCacheTTL1h(t *testing.T) {
	data := InstanceData{
		Title:               "cache-ws",
		Status:              Paused,
		IsWorkspaceTerminal: true,
		Program:             "claude",
		CacheTTL1h:          true,
	}

	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)
	assert.True(t, inst.CacheTTL1h)
}

// TestSnapshot_IncludesCacheTTL1h asserts Snapshot carries CacheTTL1h
// through to InstanceData, mirroring TestSnapshot_IncludesHeadroomProxy.
func TestSnapshot_IncludesCacheTTL1h(t *testing.T) {
	inst := &Instance{Title: "cache-ws", Status: Paused, Program: "claude", CacheTTL1h: true}
	data := inst.Snapshot()
	assert.True(t, data.CacheTTL1h)
}

// TestEnsureRunning_NoOpForPaused asserts EnsureRunning does not spawn a
// PTY for paused instances.
func TestEnsureRunning_NoOpForPaused(t *testing.T) {
	data := InstanceData{
		Title:               "paused-ws",
		Status:              Paused,
		IsWorkspaceTerminal: true,
		Program:             "true",
	}

	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)

	priorTs := inst.getTmuxSession()
	assert.NoError(t, inst.EnsureRunning())
	assert.Same(t, priorTs, inst.getTmuxSession(),
		"EnsureRunning must not replace the TmuxSession for paused instances")
}

// TestFromInstanceData_Recoverable_PreservesShape asserts a Recoverable
// placeholder comes back fully constructed (started=true, TmuxSession object
// present) just like Paused — it models a real worktree/tmux on disk, just
// without a PTY attached yet. Every isStarted()-gated accessor (GetGitWorktree,
// RepoName, Kill) needs this or the inline discard action silently no-ops
// instead of removing the worktree and the list row.
func TestFromInstanceData_Recoverable_PreservesShape(t *testing.T) {
	data := InstanceData{
		SchemaVersion: CurrentSchemaVersion,
		Title:         "orphan",
		Path:          t.TempDir(),
		Branch:        "u/orphan",
		Status:        Recoverable,
		Worktree: GitWorktreeData{
			RepoPath:         t.TempDir(),
			WorktreePath:     t.TempDir(),
			BranchName:       "u/orphan",
			IsExistingBranch: true,
		},
	}
	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)
	assert.True(t, inst.isStarted(), "Recoverable instance should be marked started")
	assert.NotNil(t, inst.getTmuxSession(), "Recoverable instance should have a TmuxSession object")

	// This is the exact accessor the discard ('D') path calls first
	// (app/intents.go killActionFor). Before this fix it always errored
	// with "not been started", so discard silently no-opped and the
	// Recoverable row never left the list.
	wt, err := inst.GetGitWorktree()
	assert.NoError(t, err, "discard's first step, GetGitWorktree, must succeed for Recoverable")
	assert.NotNil(t, wt)
}

// TestEnsureRunning_NoOpForRecoverable asserts EnsureRunning does not spawn a
// PTY for a Recoverable orphan placeholder (it goes live only via the explicit
// recover action). EnsureRunning checks GetStatus() == Recoverable before it
// ever consults isStarted(), so this holds regardless of the started flag.
func TestEnsureRunning_NoOpForRecoverable(t *testing.T) {
	data := InstanceData{
		SchemaVersion: CurrentSchemaVersion,
		Title:         "orphan",
		Path:          t.TempDir(),
		Branch:        "u/orphan",
		Status:        Recoverable,
		Worktree: GitWorktreeData{
			RepoPath:         t.TempDir(),
			WorktreePath:     t.TempDir(),
			BranchName:       "u/orphan",
			IsExistingBranch: true,
		},
	}
	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)

	priorTs := inst.getTmuxSession()
	assert.NoError(t, inst.EnsureRunning(), "EnsureRunning must no-op for Recoverable")
	assert.Same(t, priorTs, inst.getTmuxSession(),
		"EnsureRunning must not replace the TmuxSession for Recoverable instances")
}
