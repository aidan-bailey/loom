package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/internal/testpty"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/require"
)

// isolateTmuxCounter numbers isolateTmux's servers within this process.
var isolateTmuxCounter atomic.Int64

// isolateTmux points every tmux.Command in this test at a fresh private
// server, killed when the test ends (app's helper of the same name). A test
// that starts real sessions needs one: on the package's shared server, the
// previous test's cleanup may have killed the last session, and a tmux
// server exits when it has none, so a new-session that reaches it while it
// exits fails with "server exited unexpectedly" (often under load).
func isolateTmux(t *testing.T) {
	t.Helper()
	sock := fmt.Sprintf("lt-c-%d-%d", os.Getpid(), isolateTmuxCounter.Add(1))
	t.Setenv(tmux.EnvTmuxSocket, sock)
	t.Cleanup(func() { _ = tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run() })
}

// newInst builds an unstarted instance titled title in a temp dir.
func newInst(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	return inst
}

// newTerminal builds an unstarted workspace terminal titled title.
func newTerminal(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	return inst
}

// pausedInst builds a started, Paused instance titled title, as a
// reconciled record comes back (FromInstanceData; no tmux contacted).
// Persistable keeps it, and a resume moves it to Loading.
func pausedInst(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{Title: title, Status: session.Paused, Program: "claude"}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// storedWorkspace builds a workspace named name over a fresh temp config
// dir and its (empty) storage. A storage never loaded loads before its
// first write (Storage.writeLocked), so no explicit load is needed.
func storedWorkspace(t *testing.T, name string) *Workspace {
	t.Helper()
	dir := t.TempDir()
	state := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(state, dir)
	require.NoError(t, err)
	return NewWorkspace(WorkspaceParts{Ctx: &config.WorkspaceContext{Name: name, ConfigDir: dir}, Storage: storage, Config: config.DefaultConfig(), State: state})
}

// gitRepo creates a repository on branch main with one commit (adapted
// from app's setupMergeRepo; runGit is load_test.go's).
func gitRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "f"), []byte("x"), 0o644))
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-qm", "init")
	return repoDir
}

// pausedWorktreeInst builds a Paused instance backed by a real worktree of
// repoDir on a new branch (app's pausedInstanceWithRealWorktree): a
// started instance whose GetGitWorktree resolves, with no tmux session
// running.
func pausedWorktreeInst(t *testing.T, repoDir, title, branch string) *session.Instance {
	t.Helper()
	worktreePath := filepath.Join(t.TempDir(), title)
	runGit(t, repoDir, "worktree", "add", "-b", branch, worktreePath)
	inst, err := session.FromInstanceData(session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         title,
		Path:          repoDir,
		Branch:        branch,
		Status:        session.Paused,
		Worktree: session.GitWorktreeData{
			RepoPath:         repoDir,
			WorktreePath:     worktreePath,
			SessionName:      title,
			BranchName:       branch,
			IsExistingBranch: true,
		},
	}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// recordingInstanceStorage counts SaveInstances calls and keeps the last
// payload, standing in for state.json (a copy of app's fixture of the
// same name).
type recordingInstanceStorage struct {
	calls    int
	lastData json.RawMessage
}

func (r *recordingInstanceStorage) SaveInstances(data json.RawMessage) error {
	r.calls++
	r.lastData = data
	return nil
}

func (r *recordingInstanceStorage) GetInstances() json.RawMessage { return r.lastData }
func (r *recordingInstanceStorage) DeleteAllInstances() error     { return nil }

// runningPtyFactory runs a session's new-session through cmdExec and hands
// it a fake PTY, open until the test ends (app's fixture of the same name).
type runningPtyFactory struct {
	t       *testing.T
	cmdExec cmd_test.MockCmdExec
}

func (f runningPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	attach, _ := testpty.Pair(f.t)
	_ = f.cmdExec.Run(cmd)
	return attach, nil
}

func (f runningPtyFactory) Close() {}

// fakePtyFactory hands a session a fake PTY without running anything
// (app's fixture of the same name).
type fakePtyFactory struct{ t *testing.T }

func (f fakePtyFactory) Start(*exec.Cmd) (*os.File, error) {
	attach, _ := testpty.Pair(f.t)
	return attach, nil
}

func (f fakePtyFactory) Close() {}

// aliveExec answers every tmux command with success, so has-session reads
// the session alive (app's aliveCmdExecForTest).
func aliveExec() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
}

// startedInst builds a started instance titled title running program, in a
// worktree of a fresh repository, on a mock tmux session (no tmux server
// contacted) whose has-session answers once new-session ran. Start finds
// that session preset, so it never calls launchProgram and prepares no
// hooks folder. A Claude program still reads HooksLaunched: its launch ID
// stays "", never reset to the no-hooks sentinel, so like a restored
// instance it adopts the first scan result's (applyHookEvents). It is
// app's startedInstanceWithProgram minus the pane client and the captured
// content, which are the TUI's.
func startedInst(t *testing.T, title, program string) *session.Instance {
	t.Helper()

	workdir := t.TempDir()
	runGit(t, workdir, "init")
	runGit(t, workdir, "config", "--local", "user.email", "t@t.com")
	runGit(t, workdir, "config", "--local", "user.name", "T")
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "f.txt"), []byte("x"), 0644))
	runGit(t, workdir, "add", ".")
	runGit(t, workdir, "commit", "-m", "init")

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:     title,
		Path:      workdir,
		Program:   program,
		ConfigDir: t.TempDir(),
	})
	require.NoError(t, err)

	sessionCreated := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			s := cmd.String()
			if strings.Contains(s, "has-session") {
				if sessionCreated {
					return nil
				}
				return fmt.Errorf("session does not exist")
			}
			if strings.Contains(s, "new-session") {
				sessionCreated = true
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return []byte(""), nil },
	}
	inst.SetTmuxSession(tmux.NewSessionWithDeps(title, program, runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec))
	require.NoError(t, inst.Start(true))
	return inst
}

// activeInst builds a started, Running instance titled title on a mock
// tmux session (no tmux server contacted), held by m's first workspace
// (hold).
func activeInst(t *testing.T, m *Model, title string) *session.Instance {
	t.Helper()
	inst := startedInst(t, title, "claude")
	require.Equal(t, session.Running, inst.GetStatus(), "fixture: a started instance runs")
	hold(m, inst)
	return inst
}

// hold adds insts to m's first served workspace, installing an empty one
// first when m serves none: what app's fixtures did with m.ws.Add.
func hold(m *Model, insts ...*session.Instance) {
	if len(m.workspaces) == 0 {
		m.SetWorkspacesForTest(NewWorkspace(WorkspaceParts{}))
	}
	ws := m.workspaces[0]
	for _, inst := range insts {
		ws.add(inst)
	}
}

// withAccounts registers extra accounts on m, linked against a throwaway
// main dir, and publishes them; the package-level publication is undone at
// cleanup. Returns the main dir (app's fixture of the same name).
func withAccounts(t *testing.T, m *Model, names ...string) string {
	t.Helper()
	reg := account.LoadRegistry(t.TempDir())
	main := t.TempDir()
	for _, n := range names {
		_, _, err := reg.Create(n, main)
		require.NoError(t, err)
	}
	m.adoptAccounts(reg)
	t.Cleanup(func() { session.SetAccountDirs(nil, nil) })
	return main
}

// otherTerminal is a second handle on m's accounts.json, the way a `loom
// account` run in another terminal sees it.
func otherTerminal(t *testing.T, m *Model) *account.Registry {
	t.Helper()
	return account.LoadRegistry(filepath.Dir(m.accounts.Path()))
}

// editRCAuth edits the model's default-account remote-control auth.
func editRCAuth(m *Model, edit func(*session.RemoteControlAuth)) {
	edit(&m.rcAuth)
}
