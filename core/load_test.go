package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func TestRecoverySummary_String(t *testing.T) {
	assert.True(t, RecoverySummary{}.Empty())
	assert.Equal(t, "Recovery: cleaned 1 stale worktree", RecoverySummary{Cleaned: 1}.String())
	assert.Equal(t, "Recovery: cleaned 2 stale worktrees · 3 sessions need review (in list)",
		RecoverySummary{Cleaned: 2, Review: 3}.String())
	assert.Equal(t, "Recovery: 1 session needs review (in list)", RecoverySummary{Review: 1}.String())
	assert.False(t, RecoverySummary{Undecodable: 1}.Empty(), "undecodable records alone must still be surfaced")
	assert.Equal(t, "Recovery: 1 session record could not be read by this version of loom and was preserved unchanged",
		RecoverySummary{Undecodable: 1}.String())
	assert.Equal(t, "Recovery: 1 session failed to load (kept; see loom.log) · 2 session records could not be read by this version of loom and were preserved unchanged",
		RecoverySummary{Failed: 1, Undecodable: 2}.String())
}

// TestReconcileOrphans_CleanAutoRemoved_DirtyBecomesRecoverable exercises the
// full reconcile path against real git worktrees: a clean orphan is auto-removed
// (branch preserved), a dirty orphan surfaces as an inline Recoverable entry.
func TestReconcileOrphans_CleanAutoRemoved_DirtyBecomesRecoverable(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")

	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "u")
	require.NoError(t, os.MkdirAll(userDir, 0o755))

	// Suffixes must look like a plausible generated nanosecond timestamp
	// (~16 hex digits) — DiscoverOrphans now rejects short hex-looking
	// tokens like "dead0001" as part of the branch name instead of a
	// generated suffix, so a fixture using one would never get stripped.
	cleanWT := filepath.Join(userDir, "clean_18be000000000001")
	dirtyWT := filepath.Join(userDir, "dirty_18be000000000002")
	runGit(t, repo, "worktree", "add", "-b", "u/clean", cleanWT)
	runGit(t, repo, "worktree", "add", "-b", "u/dirty", dirtyWT)
	// Make the dirty one dirty (uncommitted change).
	require.NoError(t, os.WriteFile(filepath.Join(dirtyWT, "UNSAVED.txt"), []byte("wip"), 0o644))

	ws := NewWorkspace(WorkspaceParts{})
	m := NewForTest(Options{})

	summary := m.reconcileOrphans(ws, cfgDir, "true", cmd2.MakeExecutor())

	assert.Equal(t, 1, summary.Cleaned, "clean orphan should be auto-removed")
	assert.Equal(t, 1, summary.Review, "dirty orphan should surface for review")
	assert.NoDirExists(t, cleanWT, "clean worktree dir should be removed")
	assert.DirExists(t, dirtyWT, "dirty worktree dir must be preserved")

	insts := ws.instances()
	require.Len(t, insts, 1, "exactly the dirty orphan is added inline")
	assert.Equal(t, session.Recoverable, insts[0].GetStatus())
	assert.Equal(t, "dirty", insts[0].Title)

	// The placeholder must round-trip to InstanceData that preserves the
	// worktree + IsExistingBranch — the contract that lets recover adopt the
	// existing worktree in place and discard keep the branch.
	rt := insts[0].ToInstanceData()
	assert.Equal(t, dirtyWT, rt.Worktree.WorktreePath)
	assert.True(t, rt.Worktree.IsExistingBranch)
	assert.Equal(t, "u/dirty", rt.Branch)

	// Branch of the auto-cleaned worktree must survive.
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "u/clean").Output()
	assert.Contains(t, string(out), "u/clean")
}

// TestReconcileOrphans_ReportsUndecodableRecords: records this binary
// cannot decode never reach the list, so like reconcile failures they
// must be counted in the recovery summary or they read as lost sessions.
func TestReconcileOrphans_ReportsUndecodableRecords(t *testing.T) {
	rec := &recordingInstanceStorage{lastData: json.RawMessage(
		`[{"schema_version":99,"title":"future","worktree":{"worktree_path":"/tmp/wt-future"}}]`)}
	storage, err := session.NewStorage(rec, t.TempDir())
	require.NoError(t, err)
	instances, err := storage.LoadAndReconcile(cmd2.MakeExecutor())
	require.NoError(t, err)
	require.Empty(t, instances)

	ws := NewWorkspace(WorkspaceParts{Storage: storage})
	m := NewForTest(Options{})
	summary := m.reconcileOrphans(ws, t.TempDir(), "true", cmd2.MakeExecutor())

	assert.Equal(t, 1, summary.Undecodable)
	assert.Zero(t, summary.Failed, "undecodable records are not reconcile failures")
	assert.Contains(t, summary.String(), "could not be read by this version of loom")
}

// futureOnlyStorage returns a loaded Storage whose only record was written
// by a newer loom, so it is preserved on disk but absent from any list.
func futureOnlyStorage(t *testing.T) *session.Storage {
	t.Helper()
	rec := &recordingInstanceStorage{lastData: json.RawMessage(
		`[{"schema_version":99,"title":"future","worktree":{"worktree_path":"/tmp/wt-future"}}]`)}
	storage, err := session.NewStorage(rec, t.TempDir())
	require.NoError(t, err)
	instances, err := storage.LoadAndReconcile(cmd2.MakeExecutor())
	require.NoError(t, err)
	require.Empty(t, instances)
	return storage
}

// TestClaimTitles_IncludesPreservedRecords pins the claimed set both orphan
// tmux sweep sites (classic startup, multi-tab restore) build per
// workspace. CleanupOrphanedSessions kills whatever is unclaimed under the
// workspace's own roots, so a preserved record missing here — e.g. a newer
// loom's session after a downgrade — would lose its still-running agent.
func TestClaimTitles_IncludesPreservedRecords(t *testing.T) {
	storage := futureOnlyStorage(t)
	live, err := session.FromInstanceData(session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         "live",
		Status:        session.Paused,
		Program:       "claude",
	}, t.TempDir())
	require.NoError(t, err)
	ws := NewWorkspace(WorkspaceParts{Storage: storage})
	ws.add(live)
	listOnlyWS := NewWorkspace(WorkspaceParts{})
	listOnlyWS.add(live)

	claimed := map[string]bool{}
	claimTitles(claimed, ws)
	assert.Equal(t, map[string]bool{"live": true, "future": true}, claimed)

	listOnly := map[string]bool{}
	claimTitles(listOnly, listOnlyWS)
	assert.Equal(t, map[string]bool{"live": true}, listOnly, "a nil storage contributes nothing")
}

// TestReconcileOrphans_KeepsHooksOfPreservedRecords: the hooks sweep in
// reconcileOrphans deletes every folder no title claims once its tmux
// session is gone. A preserved record is not in the live list, so unless
// storage's preserved titles are claimed its hooks folder is deleted.
func TestReconcileOrphans_KeepsHooksOfPreservedRecords(t *testing.T) {
	storage := futureOnlyStorage(t)
	cfgDir := t.TempDir()
	preserved := session.SubagentHooksDir(cfgDir, "future")
	stray := session.SubagentHooksDir(cfgDir, "nobody")
	require.NoError(t, os.MkdirAll(preserved, 0o755))
	require.NoError(t, os.MkdirAll(stray, 0o755))

	// tmux reports no live sessions, so every unclaimed folder is swept.
	noSessions := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	m := NewForTest(Options{})
	m.reconcileOrphans(NewWorkspace(WorkspaceParts{Storage: storage}), cfgDir, "true", noSessions)

	assert.DirExists(t, preserved, "a preserved record's hooks folder must survive the sweep")
	assert.NoDirExists(t, stray, "the sweep itself must still run")
}
