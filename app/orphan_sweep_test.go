package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listingExec is a recordingExec whose orphan-sweep list ("ls") answers
// with a scripted "name<TAB>session_path" listing, so a test can see which
// sessions the sweep kills.
type listingExec struct {
	recordingExec
	listing string
}

func (e *listingExec) Output(c *exec.Cmd) ([]byte, error) {
	e.record(c)
	if slices.Contains(c.Args, "ls") {
		return []byte(e.listing), nil
	}
	return nil, nil
}

// killed returns the target of every kill-session, verbatim.
func (e *listingExec) killed() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, argv := range e.args {
		if slices.Contains(argv, "kill-session") {
			out = append(out, argv[len(argv)-1])
		}
	}
	return out
}

var _ cmd2.Executor = (*listingExec)(nil)

// TestOrphanSweep_SparesWorkspacesThisProcessDidNotLoad is the app-level
// regression for the incident: a loom that started while another was
// running killed every loom session on the shared server, because every
// session of a workspace it had not loaded looked unclaimed. Both sweep
// sites must pass the loaded workspaces' roots as owned and the rest of
// the registry, and the global worktrees dir, as foreign.
func TestOrphanSweep_SparesWorkspacesThisProcessDidNotLoad(t *testing.T) {
	isolateTmux(t)
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)

	// A preserved terminal record keeps activation from starting a real
	// workspace terminal.
	mine := preservedTerminalWorkspace(t, "ws-mine")
	theirs := writeWorkspaceState(t, "ws-theirs", `[]`)
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.Add("ws-mine", mine.Path))
	require.NoError(t, reg.Add("ws-theirs", theirs.Path))

	listing := strings.Join([]string{
		"loom_mine-stale\t" + filepath.Join(config.WorkspaceConfigDir(&mine), "worktrees", "stale"),
		"loom_theirs-agent\t" + filepath.Join(config.WorkspaceConfigDir(&theirs), "worktrees", "agent"),
		"loom_term_theirs-agent\t" + filepath.Join(config.WorkspaceConfigDir(&theirs), "worktrees", "agent"),
		"loom_ws-theirs\t" + theirs.Path,
		"loom_global-agent\t" + filepath.Join(globalDir, "worktrees", "global-agent"),
	}, "\n") + "\n"
	want := []string{"=loom_mine-stale"}

	t.Run("multi-tab restore", func(t *testing.T) {
		rec := &listingExec{listing: listing}
		m := newRestoreHome(rec)
		m.registry = reg
		m.restoreSavedWorkspaces([]config.Workspace{mine})

		require.Len(t, m.slots, 1)
		require.True(t, rec.ran("ls"), "the sweep must run")
		assert.Equal(t, want, rec.killed())
	})

	t.Run("classic startup", func(t *testing.T) {
		rec := &listingExec{listing: listing}
		m := newRestoreHome(rec)
		m.registry = reg
		m.wsCtx = config.WorkspaceContextFor(&mine)
		state := config.LoadStateFrom(m.wsCtx.ConfigDir)
		storage, err := session.NewStorage(state, m.wsCtx.ConfigDir)
		require.NoError(t, err)
		m.appState, m.storage = state, storage

		_, err = m.loadStartupStorage(rec, true)
		require.NoError(t, err)

		require.True(t, rec.ran("ls"), "the sweep must run")
		assert.Equal(t, want, rec.killed())
	})
}

// TestActivateWorkspace_TerminalOrphanKillIsOwnershipGated: before
// auto-creating a workspace terminal, activation clears a leftover session
// under its title (the workspace's name). The tmux server is shared, so a
// session of that name another loom started elsewhere — a same-named
// workspace in another checkout — must survive; only one this workspace's
// roots own is killed, by exact name.
func TestActivateWorkspace_TerminalOrphanKillIsOwnershipGated(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  func(ws config.Workspace) string
		want []string
	}{
		{"started in this workspace: killed", func(ws config.Workspace) string { return ws.Path }, []string{"=loom_ws-term"}},
		{"started elsewhere: spared", func(config.Workspace) string { return t.TempDir() }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			ws := writeWorkspaceState(t, "ws-term", `[]`)
			// A program that stays up, so the terminal's real Start (on
			// isolateTmux's private server) is quick.
			require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&ws), config.ConfigFileName),
				[]byte(`{"default_program":"sleep 30"}`), 0o644))
			rec := &listingExec{listing: "loom_ws-term-v2\t" + ws.Path + "\n" +
				"loom_ws-term\t" + tc.dir(ws) + "\n"}
			m := newRestoreHome(rec)
			m.registry = &config.WorkspaceRegistry{}

			_, err := m.activateWorkspace(ws)
			require.NoError(t, err)

			assert.Equal(t, tc.want, rec.killed())
		})
	}
}
