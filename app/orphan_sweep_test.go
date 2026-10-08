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

// TestOrphanSweep_SparesWhatThisLoomCannotVouchFor: the model serves every
// registered workspace and the global one (daemon stage 3A), and one loom
// runs per global dir (the takeover lock), so the boot's sweep owns all
// their roots and kills an unclaimed session under any of them. It still
// spares what it cannot vouch for: the sessions of a registered workspace
// whose load failed, whose titles it could not read, and any session
// started outside every root it serves (a loom with another global dir, a
// user's own). The model boots before the TUI decides what to show, so a
// restore of saved tabs and a startup on one workspace sweep alike.
func TestOrphanSweep_SparesWhatThisLoomCannotVouchFor(t *testing.T) {
	isolateTmux(t)
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)

	// A preserved terminal record keeps activation from starting a real
	// workspace terminal.
	mine := preservedTerminalWorkspace(t, "ws-mine")
	broken := writeWorkspaceState(t, "ws-broken", `{"not":"an array"}`)
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.Add("ws-mine", mine.Path))
	require.NoError(t, reg.Add("ws-broken", broken.Path))

	listing := strings.Join([]string{
		"loom_mine-stale\t" + filepath.Join(config.WorkspaceConfigDir(&mine), "worktrees", "stale"),
		"loom_broken-agent\t" + filepath.Join(config.WorkspaceConfigDir(&broken), "worktrees", "agent"),
		"loom_term_broken-agent\t" + filepath.Join(config.WorkspaceConfigDir(&broken), "worktrees", "agent"),
		"loom_ws-broken\t" + broken.Path,
		"loom_global-stale\t" + filepath.Join(globalDir, "worktrees", "global-stale"),
		"loom_elsewhere\t" + t.TempDir(),
	}, "\n") + "\n"
	want := []string{"=loom_mine-stale", "=loom_global-stale"}

	t.Run("multi-tab restore", func(t *testing.T) {
		require.NoError(t, reg.SetOpenWorkspaces([]string{"ws-mine"}))
		rec := &listingExec{listing: listing}
		m := startupHome(t, rec, reg, "", "")

		require.Len(t, m.slots, 1)
		require.True(t, rec.ran("ls"), "the sweep must run")
		assert.Equal(t, want, rec.killed())
	})

	t.Run("startup on one workspace", func(t *testing.T) {
		require.NoError(t, reg.SetOpenWorkspaces(nil))
		rec := &listingExec{listing: listing}
		m := startupHome(t, rec, reg, "ws-mine", "")

		require.Empty(t, m.slots)
		assert.Equal(t, "ws-mine", m.name())
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
			m := newRestoreHome(t, rec)
			t.Setenv(config.EnvGlobalDir, t.TempDir())
			registerWorkspaces(t, m, ws)

			_, err := m.activateWorkspace(ws)
			require.NoError(t, err)

			assert.Equal(t, tc.want, rec.killed())
		})
	}
}
