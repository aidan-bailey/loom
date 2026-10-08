package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspaceDef lays out a registered workspace named name: a repository
// directory whose config dir holds state (the instances array, raw JSON)
// and a config.json launching program.
func workspaceDef(t *testing.T, name, instancesJSON, program string) config.Workspace {
	t.Helper()
	def := config.Workspace{Name: name, Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(`{"instances":`+instancesJSON+`}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName), []byte(`{"default_program":"`+program+`"}`), 0o644))
	return def
}

// bootModel is a model over registry defs, its own global dir, and a tmux
// server on which every session is dead (no sweep kills anything).
func bootModel(t *testing.T, defs ...config.Workspace) *Model {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	dead := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return &exec.ExitError{} },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	return NewForTest(Options{Registry: &config.WorkspaceRegistry{Workspaces: defs}, CmdExec: dead})
}

// served is the model's workspace of def, nil when it serves none.
func served(m *Model, def config.Workspace) *Workspace {
	return m.wsByConfigDir(config.WorkspaceConfigDir(&def))
}

// killSessionAtEnd kills title's tmux session on the test's private server
// when the test ends.
func killSessionAtEnd(t *testing.T, title string) {
	t.Cleanup(func() {
		_ = tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(title))).Run()
	})
}

// The model serves every registered workspace and the global one from the
// moment it boots, each loaded once; one that fails to load is kept,
// latched, so nothing overwrites its unreadable state.
func TestBoot_LoadsEveryRegisteredWorkspaceOnce(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	broken := workspaceDef(t, "broken", `{"not":"an array"}`, "true")
	m := bootModel(t, a, b, broken)

	m.boot()

	require.NotNil(t, served(m, a))
	require.NotNil(t, served(m, b))
	assert.NotNil(t, served(m, b).byTitle("x"), "its records are loaded")
	global, err := m.globalWS()
	require.NoError(t, err)
	assert.Len(t, m.Loaded(), 4, "the three registered workspaces and the global one")
	require.NotNil(t, served(m, broken), "a failed load is kept")
	assert.Error(t, served(m, broken).loadErr)
	assert.True(t, served(m, broken).storage.WritesRefused(), "latched shut")
	for _, ws := range m.Loaded() {
		assert.False(t, ws.opened, "nobody has opened %s", ws.Label())
	}

	m.boot()
	assert.Len(t, m.Loaded(), 4, "boot loads once")
	assert.Same(t, global, m.Loaded()[0], "the global workspace first: no startup context here")
}

// Two registry entries can name one directory through a symlink (the
// registry dedups by the path as spelled): the model serves it once, or a
// stale twin's quit save would overwrite the other's changes.
func TestBoot_ADirectoryRegisteredTwiceThroughASymlinkIsServedOnce(t *testing.T) {
	a := workspaceDef(t, "a", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	// fresh has no config dir yet (registered, never used): its two
	// spellings resolve through the repository.
	fresh := config.Workspace{Name: "fresh", Path: t.TempDir()}
	links := t.TempDir()
	twin := func(def config.Workspace) config.Workspace {
		link := filepath.Join(links, def.Name)
		require.NoError(t, os.Symlink(def.Path, link))
		return config.Workspace{Name: def.Name + "-via-link", Path: link}
	}
	aTwin, freshTwin := twin(a), twin(fresh)
	m := bootModel(t, a, aTwin, fresh, freshTwin)

	m.boot()

	assert.Len(t, m.Loaded(), 3, "the global workspace, a and fresh, each once")
	require.NotNil(t, served(m, aTwin))
	assert.Same(t, served(m, a), served(m, aTwin))
	assert.Len(t, served(m, a).insts, 1, "its records loaded once")
	require.NotNil(t, served(m, freshTwin))
	assert.Same(t, served(m, fresh), served(m, freshTwin))
}

// A workspace terminal is the workspace's first open's to start: booting
// loads every workspace but starts no terminal, and a terminal record whose
// session died waits for that open to be relaunched, rather than being
// probed, found dead and paused for a workspace nobody has looked at.
func TestBoot_AnUnopenedWorkspacesTerminalIsDormant(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[{"title":"b","status":0,"program":"claude","is_workspace_terminal":true}]`, "true")
	m := bootModel(t, a, b)
	m.boot()

	assert.Nil(t, served(m, a).terminal(), "no terminal created before the first open")
	term := served(m, b).terminal()
	require.NotNil(t, term)
	assert.True(t, term.CrashRecovered(), "its session is dead: the relaunch waits for the first open")
	assert.NotContains(t, m.activeInstances(), term, "and nothing probes it meanwhile")

	served(m, b).opened = true
	assert.Contains(t, m.activeInstances(), term, "an opened workspace's terminal is probed")
}

// Loading a workspace sets the session flags of its own config dir from
// its own config: every served workspace launches with its own settings,
// not whichever loaded last.
func TestBoot_EachWorkspaceSetsItsOwnSessionFlags(t *testing.T) {
	off := workspaceDef(t, "flags-off", `[]`, "true")
	on := workspaceDef(t, "flags-on", `[]`, "true")
	for def, v := range map[*config.Workspace]string{&off: "false", &on: "true"} {
		require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(def), config.ConfigFileName),
			[]byte(`{"default_program":"true","claude_loom_context":`+v+`,"claude_subagent_tracking":`+v+`}`), 0o644))
	}
	offDir, onDir := config.WorkspaceConfigDir(&off), config.WorkspaceConfigDir(&on)
	// Each dir starts at the opposite of its config, so a load that sets
	// some other dir's flags shows.
	session.SetLoomContextEnabled(offDir, true)
	session.SetSubagentTrackingEnabled(offDir, true)
	session.SetLoomContextEnabled(onDir, false)
	session.SetSubagentTrackingEnabled(onDir, false)
	m := bootModel(t, off, on)

	m.boot()

	assert.False(t, session.LoomContextEnabled(offDir))
	assert.False(t, session.SubagentTrackingEnabled(offDir))
	assert.True(t, session.LoomContextEnabled(onDir))
	assert.True(t, session.SubagentTrackingEnabled(onDir))
}

// The boot sweep claims every served workspace's terminal title: a live
// terminal with no record (its workspace not opened yet) waits for that
// open, which replaces it, rather than being killed as an orphan. An
// unclaimed session under the same roots is still swept.
func TestBoot_TheSweepSparesEveryWorkspacesTerminal(t *testing.T) {
	a := workspaceDef(t, "sweep-a", `[]`, "true")
	b := workspaceDef(t, "sweep-b", `[]`, "true")
	list := tmux.ToLoomTmuxName(a.Name) + "\t" + a.Path + "\n" +
		tmux.ToLoomTmuxName(b.Name) + "\t" + b.Path + "\n" +
		"loom_stray\t" + a.Path + "\n"
	var killed []string
	server := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if slices.Contains(c.Args, "kill-session") {
				killed = append(killed, c.Args[len(c.Args)-1])
				return nil
			}
			return &exec.ExitError{} // has-session: no record's session runs
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if c.Args[0] == "tmux" && slices.Contains(c.Args, "ls") {
				return []byte(list), nil
			}
			return nil, nil
		},
	}
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	m := NewForTest(Options{Registry: &config.WorkspaceRegistry{Workspaces: []config.Workspace{a, b}}, CmdExec: server})

	m.boot()

	assert.Equal(t, []string{tmux.SessionTarget("loom_stray")}, killed, "only the unclaimed session")
}

// A workspace that loaded is not loaded again when it is opened: only a
// failed load is retried (retryLoad). Reloading would add every record a
// second time on each open.
func TestOpen_DoesNotReloadAWorkspaceThatLoaded(t *testing.T) {
	def := workspaceDef(t, "loaded-once", `[]`, "true")
	recs, err := json.Marshal([]map[string]any{
		// A newer loom's terminal record: preserved, so the open starts no
		// terminal here.
		{"schema_version": 99, "title": def.Name, "program": "claude", "is_workspace_terminal": true, "worktree": map[string]any{}},
		{"title": "x", "status": int(session.Paused), "program": "claude"},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&def), config.StateFileName), []byte(`{"instances":`+string(recs)+`}`), 0o644))
	m := bootModel(t, def)
	m.boot()
	ws := served(m, def)
	require.Len(t, ws.insts, 1, "fixture: x loaded")

	for range 2 {
		_, err := openDef(t, m, def)
		require.NoError(t, err)
	}

	assert.Len(t, ws.insts, 1, "x once, however often the workspace is opened")
}

// openDef opens def's served workspace (Open) and returns its view.
func openDef(t *testing.T, m *Model, def config.Workspace) (WorkspaceView, error) {
	t.Helper()
	ws := served(m, def)
	require.NotNil(t, ws, "the model serves %s", def.Name)
	return m.Open(m.wsIDOf(ws))
}

// Opening a workspace the first time starts its terminal, once; a later
// open (another tab, another client) shows the same workspace, under the
// same ID.
func TestOpen_StartsTheWorkspaceTerminalOnTheFirstOpen(t *testing.T) {
	isolateTmux(t)
	a := workspaceDef(t, "term-a", `[]`, fakeClaude(t))
	killSessionAtEnd(t, "term-a")
	m := bootModel(t, a)
	m.boot()
	ws := served(m, a)
	require.Nil(t, ws.terminal())

	v, err := openDef(t, m, a)
	require.NoError(t, err)
	term := ws.terminal()
	require.NotNil(t, term, "the first open created it")
	assert.True(t, term.Started())

	again, err := openDef(t, m, a)
	require.NoError(t, err)
	assert.Len(t, ws.insts, 1, "a later open creates no second terminal")
	assert.Equal(t, v.ID, again.ID, "the same workspace, the same ID")
}

// An unknown workspace ID opens nothing.
func TestOpen_AnUnknownWorkspaceIsAnError(t *testing.T) {
	m := bootModel(t)
	m.boot()
	_, err := m.Open(99)
	assert.Error(t, err)
}

// A name check tmux leaves unanswered (its listing timed out under load) is
// no evidence: the first open changes and saves nothing, rather than pausing
// the terminal for good (only a first open relaunches one), and the health
// tick asks again until tmux answers, then relaunches it.
func TestEnsureTerminal_AnUnansweredNameCheckIsRetriedByTheTick(t *testing.T) {
	for _, crashed := range []bool{true, false} {
		name := map[bool]string{true: "crash-recovered", false: "paused"}[crashed]
		t.Run(name, func(t *testing.T) {
			f := newUnsettledTerminal(t, name, crashed)
			status := f.term.GetStatus()

			f.m.ensureTerminal(f.ws)

			assert.Equal(t, status, f.term.GetStatus(), "no answer, no change")
			assert.Equal(t, crashed, f.term.CrashRecovered())
			assert.NoFileExists(t, filepath.Join(f.ws.ctx.ConfigDir, config.StateFileName), "nothing saved")

			f.m.Tick()
			out := f.m.Drain()
			*f.starved = false
			for _, job := range out.Jobs {
				f.m.Deliver(job())
			}

			assert.Equal(t, session.Running, f.term.GetStatus(), "the tick asked again and relaunched it")
			assert.False(t, f.term.CrashRecovered())
			assert.True(t, f.term.Pane().TmuxAlive())
			recs := savedRecords(t, f.ws.ctx.ConfigDir)
			require.Len(t, recs, 1)
			assert.Equal(t, session.Running, recs[0].Status)
			assert.False(t, f.ws.terminalUnsettled, "settled")
		})
	}
}

// unsettledTerminal is an opened workspace whose terminal waits for a
// relaunch (crash-recovered, or Paused by its breaker), on a model whose
// tmux listings go unanswered while *starved is true and then answer that
// nothing holds the terminal's name.
type unsettledTerminal struct {
	m       *Model
	ws      *Workspace
	term    *session.Instance
	starved *bool
}

func newUnsettledTerminal(t *testing.T, name string, crashed bool) unsettledTerminal {
	t.Helper()
	isolateTmux(t)
	killSessionAtEnd(t, name)
	repo := t.TempDir()
	status := session.Paused
	if crashed {
		status = session.Running
	}
	term, err := session.FromInstanceData(session.InstanceData{
		Title: name, Path: repo, Status: status, Program: fakeClaude(t), IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	term.SetCrashRecovered(crashed)
	ws := storedWorkspace(t, name)
	ws.ctx.RepoPath = repo
	ws.add(term)
	starved := true
	server := cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) {
			if starved {
				return nil, errors.New("signal: killed")
			}
			return nil, nil // answered: no session holds the name
		},
	}
	m := NewForTest(Options{CmdExec: server})
	m.SetWorkspacesForTest(ws)
	m.SetGateForTest("github", true, time.Now())
	m.Drain()
	return unsettledTerminal{m: m, ws: ws, term: term, starved: &starved}
}

// tickAndDeliver runs the model's tick and delivers what its jobs return,
// reporting how many of them were name checks.
func (f unsettledTerminal) tickAndDeliver() (checks int) {
	f.m.Tick()
	for _, job := range f.m.Drain().Jobs {
		r := job()
		if _, ok := r.(terminalChecked); ok {
			checks++
		}
		f.m.Deliver(r)
	}
	return checks
}

// The tick keeps asking while tmux keeps not answering: each answer that
// is no answer frees the next tick to ask again, so a terminal is never
// left waiting for a check nobody makes.
func TestSettleTerminals_AsksAgainAfterEveryUnansweredCheck(t *testing.T) {
	f := newUnsettledTerminal(t, "asks-again", false)
	f.m.ensureTerminal(f.ws)

	for i := range 2 {
		assert.Equal(t, 1, f.tickAndDeliver(), "tick %d asks", i+1)
	}
	require.Equal(t, session.Paused, f.term.GetStatus(), "fixture: still no answer")
	*f.starved = false
	assert.Equal(t, 1, f.tickAndDeliver())

	assert.Equal(t, session.Running, f.term.GetStatus())
}

// One check per workspace in flight: a tick while one is still running
// asks nothing more.
func TestSettleTerminals_OneCheckInFlight(t *testing.T) {
	f := newUnsettledTerminal(t, "one-check", false)
	f.m.ensureTerminal(f.ws)

	f.m.Tick()
	f.m.Tick()
	checks := 0
	for _, job := range f.m.Drain().Jobs {
		if _, ok := job().(terminalChecked); ok {
			checks++
		}
	}

	assert.Equal(t, 1, checks)
}

// A check's answer settles only the terminal it was about: one killed
// while the check ran is not relaunched.
func TestSettleTerminals_AnAnswerForAGoneTerminalSettlesNothing(t *testing.T) {
	f := newUnsettledTerminal(t, "gone", false)
	f.m.ensureTerminal(f.ws)
	f.m.Tick()
	jobs := f.m.Drain().Jobs
	require.True(t, f.ws.remove(f.term), "killed while the check ran")
	*f.starved = false

	for _, job := range jobs {
		f.m.Deliver(job())
	}

	assert.Equal(t, session.Paused, f.term.GetStatus(), "not relaunched")
	assert.NotContains(t, f.m.Drain().Events, Event(SessionLaunched{ID: f.m.idOf(f.term)}))
}

// Boot detects the default account's remote-control auth (a `claude auth
// status` run) only when the global config enables remote control (or an
// extra account is registered): the global config's setting, whatever the
// startup config dir's says.
func TestBoot_DetectsRemoteControlAuthByTheGlobalConfig(t *testing.T) {
	for _, globalOn := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[globalOn], func(t *testing.T) {
			global, home := t.TempDir(), t.TempDir()
			t.Setenv(config.EnvGlobalDir, global)
			t.Setenv(config.EnvHome, home)
			write := func(dir string, on bool) {
				data := fmt.Sprintf(`{"claude_remote_control":%t}`, on)
				require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(data), 0o644))
			}
			write(global, globalOn)
			write(home, !globalOn) // the startup config dir says the opposite
			asked := false
			server := cmd_test.MockCmdExec{
				RunFunc: func(*exec.Cmd) error { return &exec.ExitError{} },
				OutputFunc: func(c *exec.Cmd) ([]byte, error) {
					if slices.Contains(c.Args, "auth") && slices.Contains(c.Args, "status") {
						asked = true
						return []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), nil
					}
					return nil, nil
				},
			}
			m := NewForTest(Options{Program: "claude", Registry: &config.WorkspaceRegistry{}, CmdExec: server})

			m.Boot()

			assert.Equal(t, globalOn, asked)
		})
	}
}

// A terminal its restart breaker stopped stays Paused, since nothing can
// resume a workspace terminal: the first open after a start is the user
// asking for it again, so it relaunches with its breaker reset.
func TestEnsureTerminal_ATrippedTerminalIsRelaunched(t *testing.T) {
	isolateTmux(t)
	repo := t.TempDir()
	killSessionAtEnd(t, "tripped")
	term, err := session.FromInstanceData(session.InstanceData{
		Title: "tripped", Path: repo, Status: session.Paused, Program: fakeClaude(t), IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	for range maxWorkspaceTerminalRestartFailures {
		term.RecordRestartFailure()
	}
	ws := storedWorkspace(t, "tripped")
	ws.ctx.RepoPath = repo
	ws.add(term)
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)
	m.Drain()

	m.ensureTerminal(ws)

	assert.Equal(t, session.Running, term.GetStatus())
	assert.Zero(t, term.RestartFailureCount(), "its breaker is reset")
	assert.Contains(t, m.Drain().Events, Event(SessionLaunched{ID: m.idOf(term)}))
	recs := savedRecords(t, ws.ctx.ConfigDir)
	require.Len(t, recs, 1)
	assert.Equal(t, session.Running, recs[0].Status, "saved at once")
}

// startForeignSession starts title's tmux session in dir, standing in for
// another workspace's (or another loom's) session that holds the same name,
// and kills it when the test ends. The test runs on a server of its own
// (isolateTmux), which this new-session starts.
func startForeignSession(t *testing.T, title, dir string) {
	t.Helper()
	killSessionAtEnd(t, title)
	out, err := tmux.Command(context.Background(), "new-session", "-d", "-s", tmux.ToLoomTmuxName(title), "-c", dir, "sleep 300").CombinedOutput()
	require.NoError(t, err, "%s", out)
}

// sessionPath is the start directory of title's live tmux session on the
// test's private server, "" when it is not running.
func sessionPath(t *testing.T, title string) string {
	t.Helper()
	out, err := tmux.Command(context.Background(), "display-message", "-p", "-t", tmux.PaneTarget(tmux.ToLoomTmuxName(title)), "#{session_path}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// A workspace terminal's name is unique per tmux server, not per
// workspace: another workspace's session (or another loom's) can hold it,
// and so can an agent of this workspace titled after it, whose worktree
// lies in the repository (<repo>/.loom/worktrees), or the terminal of a
// workspace nested in the repository: a terminal's own session starts in
// the repository itself, nowhere below it. A first open must
// neither kill that session to relaunch the terminal nor adopt it: the
// terminal stays Paused and the other session runs on. Both starts of the
// reproduction: a terminal saved Running whose name reconcile finds held,
// and one saved Paused (the first start's outcome).
func TestOpen_ATerminalNameHeldElsewhereIsLeftRunning(t *testing.T) {
	for _, where := range []string{"another directory", "its agents' worktrees", "a nested workspace's repo"} {
		for _, status := range []session.Status{session.Running, session.Paused} {
			t.Run(where+"/"+status.String(), func(t *testing.T) {
				isolateTmux(t)
				name := "held-" + strings.ToLower(status.String())
				def := workspaceDef(t, name, `[]`, "true")
				elsewhere := t.TempDir()
				switch where {
				case "its agents' worktrees":
					elsewhere = filepath.Join(config.WorkspaceConfigDir(&def), "worktrees", name+"_1")
				case "a nested workspace's repo":
					// Another workspace whose repository lies inside this one
					// (a submodule, say), its terminal titled like this one's.
					elsewhere = filepath.Join(def.Path, "nested")
				}
				require.NoError(t, os.MkdirAll(elsewhere, 0o755))
				startForeignSession(t, name, elsewhere)
				rec, err := json.Marshal([]map[string]any{{
					"title": name, "path": def.Path, "status": int(status), "program": fakeClaude(t), "is_workspace_terminal": true,
				}})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&def), config.StateFileName), []byte(`{"instances":`+string(rec)+`}`), 0o644))
				t.Setenv(config.EnvGlobalDir, t.TempDir())
				// The production executor: reconcile, the sweep and the open
				// all see the private server's sessions.
				m := NewForTest(Options{Registry: &config.WorkspaceRegistry{Workspaces: []config.Workspace{def}}})
				m.boot()
				term := served(m, def).terminal()
				require.NotNil(t, term)
				require.Equal(t, session.Paused, term.GetStatus(), "reconcile pauses a record whose name another session holds")

				_, err = openDef(t, m, def)
				require.NoError(t, err)

				assert.Equal(t, canonical(t, elsewhere), canonical(t, sessionPath(t, name)), "the other session still runs, where it started")
				assert.Equal(t, session.Paused, term.GetStatus(), "the terminal stays Paused")
				assert.False(t, term.CrashRecovered())
			})
		}
	}
}

// savedRecords reads the instance records dir's state.json holds.
func savedRecords(t *testing.T, dir string) []session.InstanceData {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, config.StateFileName))
	require.NoError(t, err)
	var st struct {
		Instances []session.InstanceData `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(data, &st))
	return st.Instances
}

// A first open that creates, relaunches or pauses the workspace terminal
// saves the workspace at once. Waiting for the next save (a Create, or
// quit) left a live terminal with no record after a crash, which the next
// first open killed and recreated, losing its conversation.
func TestOpen_SavesTheTerminalItChanged(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		isolateTmux(t)
		def := workspaceDef(t, "saved-new", `[]`, fakeClaude(t))
		killSessionAtEnd(t, def.Name)
		m := bootModel(t, def)
		m.boot()

		_, err := openDef(t, m, def)
		require.NoError(t, err)

		recs := savedRecords(t, config.WorkspaceConfigDir(&def))
		require.Len(t, recs, 1)
		assert.Equal(t, def.Name, recs[0].Title)
		assert.True(t, recs[0].IsWorkspaceTerminal)
	})

	t.Run("paused, its name held elsewhere", func(t *testing.T) {
		isolateTmux(t)
		def := workspaceDef(t, "saved-held", `[]`, "true")
		rec, err := json.Marshal([]map[string]any{{
			"title": def.Name, "path": def.Path, "status": int(session.Running), "program": fakeClaude(t), "is_workspace_terminal": true,
		}})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&def), config.StateFileName), []byte(`{"instances":`+string(rec)+`}`), 0o644))
		t.Setenv(config.EnvGlobalDir, t.TempDir())
		m := NewForTest(Options{Registry: &config.WorkspaceRegistry{Workspaces: []config.Workspace{def}}})
		m.boot()
		require.True(t, served(m, def).terminal().CrashRecovered(), "fixture: its session died with loom")
		startForeignSession(t, def.Name, t.TempDir()) // taken while nobody had it open

		_, err = openDef(t, m, def)
		require.NoError(t, err)

		recs := savedRecords(t, config.WorkspaceConfigDir(&def))
		require.Len(t, recs, 1)
		assert.Equal(t, session.Paused, recs[0].Status)
	})
}

// canonical resolves p's symlinks (a temp dir may be reached through one).
func canonical(t *testing.T, p string) string {
	t.Helper()
	if p == "" {
		return ""
	}
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}

// Opening a workspace whose load failed loads it again, rereading it from
// disk: the user may have fixed what broke it. Until then the open reports
// the load's error, and the workspace's view carries it (LoadErr).
func TestOpen_RetriesAFailedLoadAndReportsItsError(t *testing.T) {
	def := workspaceDef(t, "flaky", `{"not":"an array"}`, "true")
	m := bootModel(t, def)
	m.boot()
	require.Error(t, served(m, def).loadErr)
	id := m.wsIDOf(served(m, def))
	v, ok := m.Workspace(id)
	require.True(t, ok, "a failed workspace is still served")
	assert.Contains(t, v.LoadErr, "flaky", "its view names the failure")

	_, err := m.Open(id)
	require.Error(t, err, "still broken")
	assert.Contains(t, err.Error(), "load instances for workspace flaky")
	assert.False(t, served(m, def).opened, "a failed open is no first open")

	require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&def), config.StateFileName),
		[]byte(`{"instances":[{"title":"x","status":3,"program":"claude"}]}`), 0o644))
	v, err = m.Open(id)
	require.NoError(t, err)
	assert.Equal(t, id, v.ID)
	assert.Empty(t, v.LoadErr, "loaded at last")
	assert.NotNil(t, served(m, def).byTitle("x"))
	assert.False(t, served(m, def).storage.WritesRefused())
}

// A workspace registered, or found registered on a reread, is served from
// then on, like every registered workspace.
func TestRegisterAndReloadRegistry_LoadTheNewWorkspace(t *testing.T) {
	m := bootModel(t)
	m.boot()
	n := len(m.Loaded())

	repo := t.TempDir()
	v, err := m.Register("new", repo)
	require.NoError(t, err)
	assert.Len(t, m.Loaded(), n+1)
	assert.Equal(t, "new", v.Name)
	assert.True(t, m.IsLoaded(v.ID), "its view names the served workspace")
	assert.NotNil(t, served(m, config.Workspace{Name: "new", Path: repo}))

	other := workspaceDef(t, "elsewhere", `[]`, "true")
	elsewhere, err := config.LoadWorkspaceRegistry() // another process's
	require.NoError(t, err)
	require.NoError(t, elsewhere.Add(other.Name, other.Path))
	require.NoError(t, m.ReloadRegistry())
	assert.NotNil(t, served(m, other))
}

// Opening the global workspace, as a client entering global mode does,
// shows the one the model has served since boot: nothing loads or is
// dropped, and the workspaces the client stops showing stay served.
func TestOpen_TheGlobalWorkspaceIsTheServedOne(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	m := bootModel(t, a)
	m.boot()
	global, err := m.globalWS()
	require.NoError(t, err)
	_, err = openDef(t, m, a)
	require.NoError(t, err)
	n := len(m.Loaded())

	v, err := m.Open(m.wsIDOf(global))
	require.NoError(t, err)

	assert.Equal(t, m.wsIDOf(global), v.ID)
	assert.Equal(t, "global", v.Label)
	assert.Len(t, m.Loaded(), n, "nothing loads")
	assert.True(t, m.isLoadedWS(served(m, a)), "the workspace no longer shown is still served")
}

// Quitting saves every workspace the model serves, not only the opened
// ones.
func TestSaveForQuit_SavesEveryServedWorkspace(t *testing.T) {
	isolateTmux(t) // the open starts a's terminal
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[]`, "true")
	m := bootModel(t, a, b)
	m.boot()
	_, err := openDef(t, m, a)
	require.NoError(t, err)
	served(m, b).add(pausedInst(t, "unshown"))

	require.NoError(t, m.SaveForQuit())

	data, err := os.ReadFile(filepath.Join(config.WorkspaceConfigDir(&b), config.StateFileName))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"unshown"`)
}

// A workspace nobody opened in this run must not hold quit hostage: one
// whose repository is gone is skipped, its config dir never recreated, and
// one whose save fails is only logged. A workspace a client opened keeps
// the sticky quit, so the user can fix the cause and retry.
func TestSaveForQuit_AnUnopenedWorkspaceNeverBlocksQuit(t *testing.T) {
	gone := workspaceDef(t, "gone", `[]`, "true")
	stuck := workspaceDef(t, "stuck", `[]`, "true")
	m := bootModel(t, gone, stuck)
	m.boot()
	require.NoError(t, os.RemoveAll(gone.Path), "a deleted (or unmounted) repository")
	// A directory where state.json goes: the save's rename fails, as on a
	// read-only path, for root too.
	statePath := filepath.Join(config.WorkspaceConfigDir(&stuck), config.StateFileName)
	require.NoError(t, os.Remove(statePath))
	require.NoError(t, os.MkdirAll(filepath.Join(statePath, "x"), 0o755))

	assert.NoError(t, m.SaveForQuit(), "nobody opened either")
	assert.NoDirExists(t, config.WorkspaceConfigDir(&gone), "a quit never recreates a vanished config dir")

	served(m, stuck).opened = true
	err := m.SaveForQuit()
	require.Error(t, err, "an opened workspace's failed save still refuses the quit")
	assert.Contains(t, err.Error(), "stuck")
}

// The published state is every served workspace and its instances, in
// serve order (Workspaces): which of them a client shows is its own.
func TestPublish_EveryServedWorkspace(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	broken := workspaceDef(t, "broken", `{"not":"an array"}`, "true")
	m := bootModel(t, a, b, broken)
	m.boot()
	global, err := m.globalWS()
	require.NoError(t, err)

	views := m.Workspaces()
	require.Len(t, views, 4)
	for i, ws := range []*Workspace{global, served(m, a), served(m, b), served(m, broken)} {
		assert.Equal(t, m.wsIDOf(ws), views[i].ID, "serve order: the global workspace, then the registry's")
	}
	assert.Empty(t, views[1].LoadErr)
	assert.NotEmpty(t, views[3].LoadErr, "a failed load is published with its error")

	out := m.Sync()
	wc := workspacesEvent(out.Events)
	require.NotNil(t, wc)
	assert.Equal(t, views, wc.Views)
	var vb *ViewsChanged
	for _, ev := range out.Events {
		if vc, ok := ev.(ViewsChanged); ok && vc.WS == m.wsIDOf(served(m, b)) {
			vb = &vc
		}
	}
	require.NotNil(t, vb, "an unopened workspace's instances are published too")
	require.Len(t, vb.Views, 1)
	assert.Equal(t, "x", vb.Views[0].Title)
	assert.True(t, m.IsLoaded(m.wsIDOf(served(m, b))))
}

// Which workspaces a client shows is its own state (daemon stage 3A): an
// open publishes nothing tab-like, so one client opening a workspace
// changes nothing another client shows. The workspace views carry nothing
// of who opened what, and an open of a workspace that loaded publishes no
// workspace view at all.
func TestOpen_PublishesNothingTabLike(t *testing.T) {
	a := preservedTerminalWorkspace(t, "ws-a") // its terminal record is preserved: no launch
	b := preservedTerminalWorkspace(t, "ws-b")
	m := bootModel(t, a, b)
	m.boot()
	before := m.Workspaces()
	m.Sync()

	_, err := openDef(t, m, a)
	require.NoError(t, err)

	assert.Equal(t, before, m.Workspaces(), "no view says a is open")
	assert.Nil(t, workspacesEvent(m.Sync().Events), "the open published no workspace view")
}

// PersistOpenList writes exactly the names a client gives, in its order:
// the open list is the client's, and the model keeps none of its own.
func TestPersistOpenList_WritesExactlyTheNamesGiven(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[]`, "true")
	c := workspaceDef(t, "c", `[]`, "true")
	m := bootModel(t)
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	for _, def := range []config.Workspace{a, b, c} {
		require.NoError(t, reg.Add(def.Name, def.Path))
	}
	m.SetRegistryForTest(reg)
	onDisk := func() []string {
		t.Helper()
		fresh, err := config.LoadWorkspaceRegistry()
		require.NoError(t, err)
		return fresh.OpenWorkspaces
	}

	m.PersistOpenList([]string{"c", "a"})
	assert.Equal(t, []string{"c", "a"}, onDisk())

	m.PersistOpenList(nil)
	assert.Empty(t, onDisk(), "an empty list clears it")
}

// Boot loads the account registry before anything else and hands back
// the notices it raised, since no client is connected yet to be sent
// them; it leaves none behind for the first Sync.
func TestBoot_ReturnsTheAccountRegistrysNotices(t *testing.T) {
	m := bootModel(t)
	global, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(global, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(global, "accounts.json"), []byte(`{not json`), 0o644))

	notices := m.Boot()

	require.Len(t, notices, 1)
	n, ok := notices[0].(Notice)
	require.True(t, ok)
	assert.Contains(t, n.Err.Error(), "accounts:")
	assert.True(t, m.booted, "and boots")
	for _, ev := range m.Sync().Events {
		assert.NotEqual(t, notices[0], ev, "nothing left for the first Sync")
	}
}
