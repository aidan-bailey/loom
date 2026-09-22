package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingExec is a cmd.Executor that runs nothing and records every
// command's argv, so the workspace load paths can be driven through the
// home.cmdExec seam and asserted on without touching a tmux server.
type recordingExec struct {
	mu   sync.Mutex
	args [][]string
}

func (r *recordingExec) record(c *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args = append(r.args, append([]string(nil), c.Args...))
}

func (r *recordingExec) Run(c *exec.Cmd) error { r.record(c); return nil }

func (r *recordingExec) Output(c *exec.Cmd) ([]byte, error) { r.record(c); return nil, nil }

func (r *recordingExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { r.record(c); return nil, nil }

// ran reports whether any recorded command carried arg as a whole argument
// (e.g. "ls" for the orphan sweep, "kill-session" for a targeted kill).
func (r *recordingExec) ran(arg string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, argv := range r.args {
		for _, a := range argv {
			if a == arg {
				return true
			}
		}
	}
	return false
}

var _ cmd2.Executor = (*recordingExec)(nil)

// isolateTmux points every tmux.Command in this test at a private, throwaway
// server. The recording executor already keeps the load paths off tmux; this
// is the safety belt for code that builds its own executor (Instance.Start),
// so a regression can never reach the developer's real server.
func isolateTmux(t *testing.T) {
	t.Helper()
	sock := fmt.Sprintf("loomtest-app-%d", time.Now().UnixNano())
	t.Setenv(tmux.EnvTmuxSocket, sock)
	t.Cleanup(func() { _ = tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run() })
}

// writeWorkspaceState lays out a workspace directory whose state.json holds
// instancesJSON, with a harmless default program.
func writeWorkspaceState(t *testing.T, name, instancesJSON string) config.Workspace {
	t.Helper()
	ws := config.Workspace{Name: name, Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&ws)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName),
		[]byte(`{"instances":`+instancesJSON+`}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName),
		[]byte(`{"default_program":"true"}`), 0o644))
	return ws
}

// preservedTerminalWorkspace is a workspace whose only record is its
// workspace terminal, written by a newer loom (schema_version 99). A newer
// binary stamps every record with its own version, so after a downgrade
// this is exactly what each workspace looks like.
func preservedTerminalWorkspace(t *testing.T, name string) config.Workspace {
	t.Helper()
	rec, err := json.Marshal(map[string]any{
		"schema_version":        99,
		"title":                 name,
		"program":               "claude",
		"is_workspace_terminal": true,
		"worktree":              map[string]any{},
	})
	require.NoError(t, err)
	return writeWorkspaceState(t, name, "["+string(rec)+"]")
}

// newRestoreHome is a bare home for the workspace load paths, with every
// executor they build replaced by exec.
func newRestoreHome(exec cmd2.Executor) *home {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		tabBar:    ui.NewWorkspaceTabBar(),
		errBox:    ui.NewErrBox(),
		cmdExec:   exec,
	}
	h.list = ui.NewList(&h.spinner)
	return h
}

// TestActivateWorkspace_PreservedTerminalIsNotReplaced: after a downgrade the
// workspace terminal's record is undecodable, so the decoded list has no
// terminal. Activation used to take that as "none exists": it killed the
// tmux session backing the preserved terminal and auto-created a second
// record under the same title, and both were persisted.
func TestActivateWorkspace_PreservedTerminalIsNotReplaced(t *testing.T) {
	isolateTmux(t)
	ws := preservedTerminalWorkspace(t, "ws-term")
	rec := &recordingExec{}
	m := newRestoreHome(rec)

	require.NoError(t, m.activateWorkspace(ws))
	require.Len(t, m.slots, 1)
	slot := m.slots[0]

	assert.False(t, rec.ran("kill-session"), "the preserved terminal's tmux session must not be killed")
	for _, inst := range slot.list.GetInstances() {
		assert.NotEqual(t, "ws-term", inst.Title, "no second workspace terminal under the preserved record's title")
	}

	require.NoError(t, slot.storage.SaveInstances(persistableInstances(slot.list.GetInstances())))
	raw, err := os.ReadFile(filepath.Join(config.WorkspaceConfigDir(&ws), config.StateFileName))
	require.NoError(t, err)
	var st struct {
		Instances []struct {
			Title string `json:"title"`
		} `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(raw, &st))
	count := 0
	for _, r := range st.Instances {
		if r.Title == "ws-term" {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly the preserved record may carry the terminal's title")
}

// TestRestoreSavedWorkspaces_SkipsSweepWhenAWorkspaceFailsToLoad: the
// restore-time orphan sweep is server-wide and spares only the titles it can
// see. A workspace whose load failed contributes none — they can't be read —
// so sweeping would kill that workspace's live sessions. Fail closed: skip.
func TestRestoreSavedWorkspaces_SkipsSweepWhenAWorkspaceFailsToLoad(t *testing.T) {
	isolateTmux(t)

	t.Run("control: every workspace loads, sweep runs", func(t *testing.T) {
		rec := &recordingExec{}
		m := newRestoreHome(rec)
		m.restoreSavedWorkspaces([]config.Workspace{preservedTerminalWorkspace(t, "ws-good")})

		require.Len(t, m.slots, 1)
		assert.True(t, rec.ran("ls"), "with every workspace loaded the sweep must still run")
	})

	t.Run("one workspace fails, sweep skipped", func(t *testing.T) {
		rec := &recordingExec{}
		m := newRestoreHome(rec)
		bad := writeWorkspaceState(t, "ws-bad", `{"not":"an array"}`)
		m.restoreSavedWorkspaces([]config.Workspace{bad, preservedTerminalWorkspace(t, "ws-good")})

		require.Len(t, m.slots, 1, "the good workspace still opens")
		assert.False(t, rec.ran("ls"), "the sweep must not run while a workspace's titles are unknown")
	})
}

// restoreModeHome is a home as newHome leaves it in restore mode: the
// startup (global) storage — a real state.json holding instancesJSON — is
// on m.storage but has never been loaded; restoreSavedWorkspaces owns that.
func restoreModeHome(t *testing.T, exec cmd2.Executor, instancesJSON string) (*home, string) {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, config.StateFileName)
	require.NoError(t, os.WriteFile(statePath, []byte(`{"instances":`+instancesJSON+`}`), 0o644))
	appState := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(appState, dir)
	require.NoError(t, err)
	m := newRestoreHome(exec)
	m.storage = storage
	m.appState = appState
	m.activeCtx = &config.WorkspaceContext{ConfigDir: dir}
	m.program = "true"
	m.errBox.SetSize(400, 1)
	return m, statePath
}

func corruptWorkspaces(t *testing.T, names ...string) []config.Workspace {
	t.Helper()
	out := make([]config.Workspace, 0, len(names))
	for _, n := range names {
		out = append(out, writeWorkspaceState(t, n, `{"not":"an array"}`))
	}
	return out
}

func listTitles(m *home) []string {
	var titles []string
	for _, inst := range m.list.GetInstances() {
		titles = append(titles, inst.Title)
	}
	return titles
}

// TestRestoreSavedWorkspaces_AllFail_LoadsStartupStorage: in restore mode
// newHome never loads the startup storage. When every workspace failed to
// restore, the user used to land in global mode over that never-loaded
// storage with an empty list, and the first save (here: quit) replaced its
// readable records with nothing. The fallback now loads it like classic
// startup does — minus the orphan sweep, since the failed workspaces'
// sessions are still unidentifiable.
func TestRestoreSavedWorkspaces_AllFail_LoadsStartupStorage(t *testing.T) {
	isolateTmux(t)
	rec := &recordingExec{}
	m, statePath := restoreModeHome(t, rec,
		`[{"title":"keeper","status":3,"program":"claude","worktree":{"worktree_path":"/tmp/wt-keeper"}}]`)

	m.restoreSavedWorkspaces(corruptWorkspaces(t, "ws-bad-1", "ws-bad-2"))

	require.Empty(t, m.slots)
	assert.Contains(t, listTitles(m), "keeper", "the global list must show its real sessions")
	assert.False(t, rec.ran("ls"), "the fallback must not run the orphan sweep either")

	_, _ = m.handleQuit()
	raw, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"keeper"`, "quitting must not drop the global record")
}

// TestRestoreSavedWorkspaces_AllFail_StartupLoadFailsClosed: when the startup
// storage is unreadable too, the fallback fails closed — the error is shown,
// nothing is written, and the user can still open a workspace (which saves
// the global storage first; its refusal must not block the transition, as
// there is nothing loaded to lose).
func TestRestoreSavedWorkspaces_AllFail_StartupLoadFailsClosed(t *testing.T) {
	isolateTmux(t)
	rec := &recordingExec{}
	m, statePath := restoreModeHome(t, rec, `{"not":"an array"}`)
	before, err := os.ReadFile(statePath)
	require.NoError(t, err)

	m.restoreSavedWorkspaces(corruptWorkspaces(t, "ws-bad"))

	require.Empty(t, m.slots)
	assert.Empty(t, listTitles(m))
	assert.Contains(t, m.errBox.String(), "no workspace could be restored")

	_, _ = m.handleQuit()
	after, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the unreadable startup state.json must be untouched")

	m.applyWorkspaceToggle([]config.Workspace{preservedTerminalWorkspace(t, "ws-good")})
	require.Len(t, m.slots, 1, "the user must still be able to open a workspace")
	assert.Equal(t, "ws-good", m.slots[0].wsCtx.Name)
}
