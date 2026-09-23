package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sweepServer is a fake tmux server for CleanupOrphanedSessions: it answers
// the sweep's list with a scripted "name<TAB>session_path" listing and
// records the target of every kill.
type sweepServer struct {
	listing string
	listErr error
	list    []string // argv of the list call
	killed  []string // kill-session targets, verbatim
}

func (s *sweepServer) executor() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			s.list = append([]string(nil), c.Args...)
			return []byte(s.listing), s.listErr
		},
		RunFunc: func(c *exec.Cmd) error {
			if len(c.Args) >= 3 && c.Args[1] == "kill-session" {
				s.killed = append(s.killed, c.Args[len(c.Args)-1])
			}
			return nil
		},
	}
}

// listing renders name/dir pairs the way the sweep's list format does.
func listing(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString(pairs[i] + "\t" + pairs[i+1] + "\n")
	}
	return b.String()
}

// sweep runs CleanupOrphanedSessions against list and returns the kill
// targets.
func sweep(t *testing.T, claimed map[string]bool, scope SweepScope, list string) []string {
	t.Helper()
	srv := &sweepServer{listing: list}
	require.NoError(t, CleanupOrphanedSessions(claimed, scope, srv.executor()))
	return srv.killed
}

// workspaceAt lays out a workspace repo with its worktrees dir and returns
// its context.
func workspaceAt(t *testing.T, repo string) *config.WorkspaceContext {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".loom", "worktrees"), 0o755))
	return &config.WorkspaceContext{Name: filepath.Base(repo), RepoPath: repo, ConfigDir: filepath.Join(repo, ".loom")}
}

func ownedScope(ctxs ...*config.WorkspaceContext) SweepScope {
	var s SweepScope
	for _, c := range ctxs {
		s.Owned = append(s.Owned, WorkspaceSweepRoots(c.RepoPath, c.ConfigDir)...)
	}
	return s
}

func TestCleanupOrphanedSessions_KillsUnclaimedUnderOwnedRoots(t *testing.T) {
	mine := workspaceAt(t, filepath.Join(t.TempDir(), "mine"))
	wt := filepath.Join(mine.ConfigDir, "worktrees")
	srv := &sweepServer{listing: listing(
		"loom_stale", filepath.Join(wt, "stale"),
		"loom_term_stale", filepath.Join(wt, "stale", "sub", "dir"),
		"loom_gone", filepath.Join(wt, "deleted-worktree"), // never created: the dir is gone
		"claudesquad_legacy", filepath.Join(wt, "legacy"),
		"loom_oldterminal", mine.RepoPath, // a workspace terminal runs at the repo root
	)}

	require.NoError(t, CleanupOrphanedSessions(map[string]bool{}, ownedScope(mine), srv.executor()))

	assert.Equal(t, []string{"ls", "-F", "#{session_name}\t#{session_path}"}, srv.list[len(srv.list)-3:],
		"the sweep reads each session's start directory")
	assert.ElementsMatch(t, []string{
		"-t=loom_stale", "-t=loom_term_stale", "-t=loom_gone", "-t=claudesquad_legacy", "-t=loom_oldterminal",
	}, srv.killed, "unclaimed sessions under an owned root are killed, by exact name")
}

// TestCleanupOrphanedSessions_SparesAnotherWorkspacesSessions is the
// regression for the incident: a second loom's startup sweep killed all 20
// loom sessions on the server, including the live agents of a still-running
// loom in workspaces the second one never loaded.
func TestCleanupOrphanedSessions_SparesAnotherWorkspacesSessions(t *testing.T) {
	base := t.TempDir()
	mine := workspaceAt(t, filepath.Join(base, "mine"))
	theirs := workspaceAt(t, filepath.Join(base, "theirs"))
	globalWT := filepath.Join(base, "home", ".loom", "worktrees")

	killed := sweep(t, map[string]bool{}, ownedScope(mine), listing(
		"loom_theirs-agent", filepath.Join(theirs.ConfigDir, "worktrees", "agent_18d7"),
		"loom_term_theirs-agent", filepath.Join(theirs.ConfigDir, "worktrees", "agent_18d7"),
		"loom_theirs", theirs.RepoPath, // the other workspace's terminal
		"loom_global-agent", filepath.Join(globalWT, "global-agent"),
		"loom_scratch", filepath.Join(base, "elsewhere"),
		"loom_mine-stale", filepath.Join(mine.ConfigDir, "worktrees", "stale"),
	))

	assert.Equal(t, []string{"-t=loom_mine-stale"}, killed,
		"only the session started under this process's own workspace may die")
}

func TestCleanupOrphanedSessions_UnreadableDirIsSpared(t *testing.T) {
	mine := workspaceAt(t, filepath.Join(t.TempDir(), "mine"))

	killed := sweep(t, map[string]bool{}, ownedScope(mine),
		"loom_nodir\n"+ // no directory field at all
			"loom_empty\t\n"+
			"loom_relative\t.loom/worktrees/x\n"+
			"loom_blank\t   \n")

	assert.Empty(t, killed, "a session whose directory can't be read is never killed")
}

func TestCleanupOrphanedSessions_SiblingPrefixIsNotInside(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "repo2"), 0o755))

	killed := sweep(t, map[string]bool{}, SweepScope{Owned: []string{repo}}, listing(
		"loom_sibling", filepath.Join(base, "repo2"),
		"loom_sibling-wt", filepath.Join(base, "repo2", ".loom", "worktrees", "x"),
		"loom_parent", base,
		"loom_own", repo,
	))

	assert.Equal(t, []string{"-t=loom_own"}, killed, "/a/repo must not own /a/repo2 or /a")
}

func TestCleanupOrphanedSessions_SymlinksCompareByTarget(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	mine := workspaceAt(t, filepath.Join(realDir, "mine"))
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(realDir, link))
	foreign := filepath.Join(base, "foreign")
	require.NoError(t, os.MkdirAll(foreign, 0o755))
	// A symlink inside the owned repo that leads out of it.
	require.NoError(t, os.Symlink(foreign, filepath.Join(mine.RepoPath, "escape")))

	viaLink := func(p string) string { return filepath.Join(link, strings.TrimPrefix(p, realDir)) }

	t.Run("root spelled through a symlink", func(t *testing.T) {
		scope := ownedScope(&config.WorkspaceContext{RepoPath: viaLink(mine.RepoPath), ConfigDir: viaLink(mine.ConfigDir)})
		killed := sweep(t, map[string]bool{}, scope, listing(
			"loom_a", filepath.Join(mine.ConfigDir, "worktrees", "a"),
			"loom_deleted", filepath.Join(mine.ConfigDir, "worktrees", "deleted"),
		))
		assert.ElementsMatch(t, []string{"-t=loom_a", "-t=loom_deleted"}, killed)
	})

	t.Run("session dir spelled through a symlink", func(t *testing.T) {
		killed := sweep(t, map[string]bool{}, ownedScope(mine), listing(
			"loom_b", viaLink(filepath.Join(mine.ConfigDir, "worktrees", "b")),
		))
		assert.Equal(t, []string{"-t=loom_b"}, killed)
	})

	t.Run("a symlink out of an owned root is outside it", func(t *testing.T) {
		killed := sweep(t, map[string]bool{}, ownedScope(mine), listing(
			"loom_escaped", filepath.Join(mine.RepoPath, "escape", "wt"),
		))
		assert.Empty(t, killed, "the directory really lives in %s", foreign)
	})
}

func TestCleanupOrphanedSessions_ClaimedAndPreservedTitlesAreSpared(t *testing.T) {
	mine := workspaceAt(t, filepath.Join(t.TempDir(), "mine"))
	wt := filepath.Join(mine.ConfigDir, "worktrees")
	// claimTitles folds both the live list and Storage.PreservedTitles
	// into one set; "future" stands for a newer loom's preserved record.
	claimed := map[string]bool{"live": true, "future": true}

	killed := sweep(t, claimed, ownedScope(mine), listing(
		"loom_live", filepath.Join(wt, "live"),
		"loom_term_live", filepath.Join(wt, "live"),
		"claudesquad_live", filepath.Join(wt, "live"),
		"loom_future", filepath.Join(wt, "future"),
		"loom_term_future", filepath.Join(wt, "future"),
		"loom_stray", filepath.Join(wt, "stray"),
		"other_session", filepath.Join(wt, "other"), // not loom's at all
	))

	assert.Equal(t, []string{"-t=loom_stray"}, killed)
}

// TestCleanupOrphanedSessions_NestedForeignWorkspaceWins: a workspace
// registered inside another's repo (a submodule, a linked worktree) owns
// the sessions under it, even though the outer repo root holds them too.
func TestCleanupOrphanedSessions_NestedForeignWorkspaceWins(t *testing.T) {
	outer := workspaceAt(t, filepath.Join(t.TempDir(), "outer"))
	inner := workspaceAt(t, filepath.Join(outer.RepoPath, "libs", "inner"))
	scope := ownedScope(outer)
	// The registry lists both; the outer one is also owned.
	scope.Foreign = append(WorkspaceSweepRoots(outer.RepoPath, outer.ConfigDir),
		WorkspaceSweepRoots(inner.RepoPath, inner.ConfigDir)...)

	killed := sweep(t, map[string]bool{}, scope, listing(
		"loom_inner-agent", filepath.Join(inner.ConfigDir, "worktrees", "agent"),
		"loom_inner", inner.RepoPath,
		"loom_outer-stale", filepath.Join(outer.ConfigDir, "worktrees", "stale"),
		"loom_outer-sub", filepath.Join(outer.RepoPath, "libs"),
	))

	assert.ElementsMatch(t, []string{"-t=loom_outer-stale", "-t=loom_outer-sub"}, killed)
}

func TestCleanupOrphanedSessions_FilesystemRootOwnsNothing(t *testing.T) {
	killed := sweep(t, map[string]bool{}, SweepScope{Owned: []string{"/", ""}}, listing(
		"loom_x", t.TempDir(),
	))
	assert.Empty(t, killed)
}

func TestCleanupOrphanedSessions_NoTmux(t *testing.T) {
	mine := workspaceAt(t, filepath.Join(t.TempDir(), "mine"))
	srv := &sweepServer{listErr: &exec.ExitError{}}
	assert.NoError(t, CleanupOrphanedSessions(nil, ownedScope(mine), srv.executor()))
	assert.Empty(t, srv.killed)

	srv = &sweepServer{listing: "loom_x\t" + mine.RepoPath + "\n", listErr: errors.New("timed out")}
	assert.NoError(t, CleanupOrphanedSessions(nil, ownedScope(mine), srv.executor()))
	assert.Empty(t, srv.killed, "a failed list kills nothing")
}

func TestNewSweepScope(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	global := filepath.Join(base, "global")
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvGlobalDir, global)

	open := &config.WorkspaceContext{Name: "open", RepoPath: filepath.Join(base, "open"), ConfigDir: filepath.Join(base, "open", ".loom")}
	reg := &config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "open", Path: open.RepoPath},
		{Name: "closed", Path: filepath.Join(base, "closed")},
		{Name: "broken"}, // no path: contributes nothing
	}}

	s := NewSweepScope([]*config.WorkspaceContext{open, nil, {ConfigDir: ""}}, reg)

	assert.Equal(t, []string{
		open.RepoPath, filepath.Join(open.ConfigDir, "worktrees"),
		filepath.Join(home, "worktrees"), // empty ConfigDir: the default config dir
	}, s.Owned, "a nil context owns nothing")
	assert.Equal(t, []string{
		open.RepoPath, filepath.Join(open.ConfigDir, "worktrees"),
		filepath.Join(base, "closed"), filepath.Join(base, "closed", ".loom", "worktrees"),
		filepath.Join(global, "worktrees"),
	}, s.Foreign)

	assert.Equal(t, []string{filepath.Join(global, "worktrees")}, NewSweepScope(nil, nil).Foreign)
}
