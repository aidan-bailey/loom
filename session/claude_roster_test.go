package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRosterExecutor returns canned output/err for `agents --json` and
// records the argv it was asked to run.
type fakeRosterExecutor struct {
	out     []byte
	err     error
	gotArgs []string
	called  bool
}

func (f *fakeRosterExecutor) Run(c *exec.Cmd) error { return f.err }
func (f *fakeRosterExecutor) Output(c *exec.Cmd) ([]byte, error) {
	f.called = true
	f.gotArgs = c.Args
	return f.out, f.err
}
func (f *fakeRosterExecutor) CombinedOutput(c *exec.Cmd) ([]byte, error) { return f.out, f.err }

// rosterJSON is a trimmed sample of real `claude agents --json` output,
// covering the three observed statuses.
const rosterJSON = `[
  {"pid":1,"cwd":"/w/busy","kind":"interactive","sessionId":"s-busy","name":"busy-1","status":"busy"},
  {"pid":2,"cwd":"/w/idle","kind":"interactive","sessionId":"s-idle","name":"idle-2","status":"idle"},
  {"pid":3,"cwd":"/w/waiting","kind":"interactive","sessionId":"s-wait","name":"wait-3","status":"waiting","waitingFor":"dialog open"}
]`

func TestQueryClaudeRoster_ParsesEntriesByCwd(t *testing.T) {
	fake := &fakeRosterExecutor{out: []byte(rosterJSON)}

	got, err := QueryClaudeRoster("claude --model opus", fake)

	assert.NoError(t, err)
	assert.True(t, fake.called)
	assert.Contains(t, fake.gotArgs, "agents")
	assert.Contains(t, fake.gotArgs, "--json")
	assert.Len(t, got, 3)
	assert.Equal(t, RosterStatusBusy, got["/w/busy"].Status)
	assert.Equal(t, RosterStatusIdle, got["/w/idle"].Status)
	assert.Equal(t, RosterStatusWaiting, got["/w/waiting"].Status)
	assert.Equal(t, "dialog open", got["/w/waiting"].WaitingFor)
	assert.Equal(t, "s-busy", got["/w/busy"].SessionID)
}

func TestQueryClaudeRoster_NonClaudeProgramSkipsExec(t *testing.T) {
	fake := &fakeRosterExecutor{out: []byte(rosterJSON)}

	got, err := QueryClaudeRoster("aider --model x", fake)

	assert.NoError(t, err)
	assert.Empty(t, got)
	assert.False(t, fake.called, "non-claude program must not shell out")
}

func TestQueryClaudeRoster_TwoInteractiveInOneCwdIsDropped(t *testing.T) {
	// Genuine ambiguity: the user ran claude by hand inside a Loom
	// worktree, so two interactive sessions share the directory and
	// neither can be attributed to the instance. Drop it and let the
	// caller fall back rather than driving transitions off a coin flip.
	dup := `[
	  {"pid":1,"cwd":"/w/dup","kind":"interactive","sessionId":"a","status":"busy"},
	  {"pid":2,"cwd":"/w/dup","kind":"interactive","sessionId":"b","status":"idle"},
	  {"pid":3,"cwd":"/w/solo","kind":"interactive","sessionId":"c","status":"idle"}
	]`
	fake := &fakeRosterExecutor{out: []byte(dup)}

	got, err := QueryClaudeRoster("claude", fake)

	assert.NoError(t, err)
	assert.NotContains(t, got, "/w/dup")
	assert.Contains(t, got, "/w/solo")
}

func TestQueryClaudeRoster_BackgroundSiblingDoesNotBlindInteractive(t *testing.T) {
	// A `claude --bg` session started inside a Loom worktree shares the
	// cwd with Loom's own tmux-hosted session, but that is not ambiguous:
	// the instance IS the interactive one. Joining to it keeps the status
	// override alive instead of silently falling back to the scraper.
	mixed := `[
	  {"pid":1,"cwd":"/w/mixed","kind":"interactive","sessionId":"live","status":"waiting","waitingFor":"sandbox request"},
	  {"id":"bg1","cwd":"/w/mixed","kind":"background","sessionId":"bg","state":"running"}
	]`
	fake := &fakeRosterExecutor{out: []byte(mixed)}

	got, err := QueryClaudeRoster("claude", fake)

	assert.NoError(t, err)
	assert.Contains(t, got, "/w/mixed")
	assert.Equal(t, "live", got["/w/mixed"].SessionID)
	assert.Equal(t, RosterStatusWaiting, got["/w/mixed"].Status)
	assert.Equal(t, "sandbox request", got["/w/mixed"].WaitingFor)
}

func TestQueryClaudeRoster_BackgroundOnlyCwdYieldsNoEntry(t *testing.T) {
	// Background sessions carry {id,state} where interactive ones carry
	// {pid,status}; Loom instances are always interactive. A directory
	// with only background sessions therefore backs no instance, and the
	// roster must express no opinion rather than guess at `state`.
	bg := `[{"id":"bg1","cwd":"/w/bgonly","kind":"background","sessionId":"bg","state":"running"}]`
	fake := &fakeRosterExecutor{out: []byte(bg)}

	got, err := QueryClaudeRoster("claude", fake)

	assert.NoError(t, err)
	assert.NotContains(t, got, "/w/bgonly")
}

func TestQueryClaudeRoster_MissingKindTreatedAsInteractive(t *testing.T) {
	// `kind` is absent in older CLI output. Defaulting an unlabeled entry
	// to interactive preserves the pre-kind join behavior; defaulting it
	// to background would silently drop every entry on those builds.
	nokind := `[{"pid":1,"cwd":"/w/nokind","sessionId":"x","status":"busy"}]`
	fake := &fakeRosterExecutor{out: []byte(nokind)}

	got, err := QueryClaudeRoster("claude", fake)

	assert.NoError(t, err)
	assert.Contains(t, got, "/w/nokind")
	assert.Equal(t, RosterStatusBusy, got["/w/nokind"].Status)
}

func TestQueryClaudeRoster_UnparseableOutputErrors(t *testing.T) {
	fake := &fakeRosterExecutor{out: []byte("not json")}

	got, err := QueryClaudeRoster("claude", fake)

	assert.Error(t, err)
	assert.Empty(t, got)
}

func TestQueryClaudeRoster_SubcommandMissingErrors(t *testing.T) {
	fake := &fakeRosterExecutor{err: errors.New("unknown command 'agents'")}

	got, err := QueryClaudeRoster("claude", fake)

	assert.Error(t, err)
	assert.Empty(t, got)
}

func TestQueryClaudeRoster_EmptyProgramSkipsExec(t *testing.T) {
	fake := &fakeRosterExecutor{out: []byte(rosterJSON)}

	got, err := QueryClaudeRoster("", fake)

	assert.NoError(t, err)
	assert.Empty(t, got)
	assert.False(t, fake.called)
}

func TestRosterEntryLoomStatus(t *testing.T) {
	cases := []struct {
		name   string
		status RosterStatus
		want   Status
		wantOK bool
	}{
		{"busy maps to Running", RosterStatusBusy, Running, true},
		{"waiting maps to Prompting", RosterStatusWaiting, Prompting, true},
		{"idle maps to Ready", RosterStatusIdle, Ready, true},
		{"unknown yields no opinion", RosterStatusUnknown, Ready, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RosterEntry{Status: tc.status}.LoomStatus()
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestQueryClaudeRoster_UnknownStatusStringYieldsNoOpinion(t *testing.T) {
	fake := &fakeRosterExecutor{out: []byte(`[{"cwd":"/w/x","status":"hibernating"}]`)}

	got, err := QueryClaudeRoster("claude", fake)

	assert.NoError(t, err)
	assert.Equal(t, RosterStatusUnknown, got["/w/x"].Status)
	_, ok := got["/w/x"].LoomStatus()
	assert.False(t, ok, "an unrecognized status string must not drive a transition")
}

// envRecordingExec records the env of the command it runs.
type envRecordingExec struct {
	out []byte
	env []string
}

func (f *envRecordingExec) Run(c *exec.Cmd) error { f.env = c.Env; return nil }
func (f *envRecordingExec) Output(c *exec.Cmd) ([]byte, error) {
	f.env = c.Env
	return f.out, nil
}
func (f *envRecordingExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return f.Output(c) }

func TestQueryClaudeRosterEnv_RunsAsTheAccount(t *testing.T) {
	dir := t.TempDir()
	f := &envRecordingExec{out: []byte(rosterJSON)}
	got, err := QueryClaudeRosterEnv("claude", account.EnvFor(dir), f)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	assert.Contains(t, f.env, "CLAUDE_CONFIG_DIR="+dir)
}

func TestQueryClaudeRoster_InheritsLoomsEnv(t *testing.T) {
	f := &envRecordingExec{out: []byte(rosterJSON)}
	_, err := QueryClaudeRoster("claude", f)
	require.NoError(t, err)
	assert.Nil(t, f.env)
}

func TestQueryClaudeRosterEnv_RefusesAMissingAccountDir(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	f := &envRecordingExec{out: []byte(rosterJSON)}

	_, err := QueryClaudeRosterEnv("claude", account.EnvFor(gone), f)

	require.Error(t, err)
	assert.ErrorIs(t, err, account.ErrAccountDirMissing)
	assert.Nil(t, f.env, "must not run claude against a dir the CLI would recreate fresh")
}

// bridgeRosterJSON is one interactive entry, and bridgeSessionFile the
// sessions/41.json Claude keeps for it while Remote Control is connected.
const (
	bridgeRosterJSON  = `[{"pid":41,"cwd":"/w/rc","kind":"interactive","sessionId":"s-rc","name":"rc-1","status":"idle"}]`
	bridgeSessionFile = `{"pid":41,"sessionId":"s-rc","cwd":"/w/rc","status":"idle","bridgeSessionId":"session_01AbC-9"}`
)

// bridgeFileFor is the session file of bridgeRosterJSON's entry holding id.
func bridgeFileFor(id string) string {
	return `{"sessionId":"s-rc","bridgeSessionId":` + strconv.Quote(id) + `}`
}

// writeSessionFile writes <dir>/sessions/<pid>.json.
func writeSessionFile(t *testing.T, dir string, pid int, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sessions"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json"), []byte(content), 0o600))
}

// bridgeOf runs the roster query as an account whose config dir is dir and
// returns the entry for /w/rc.
func bridgeOf(t *testing.T, dir, roster string) RosterEntry {
	t.Helper()
	got, err := QueryClaudeRosterEnv("claude", account.EnvFor(dir), &fakeRosterExecutor{out: []byte(roster)})
	require.NoError(t, err)
	require.Contains(t, got, "/w/rc")
	return got["/w/rc"]
}

func TestQueryClaudeRoster_KeepsEachEntrysPID(t *testing.T) {
	got, err := QueryClaudeRoster("claude", &fakeRosterExecutor{out: []byte(rosterJSON)})

	require.NoError(t, err)
	assert.Equal(t, 1, got["/w/busy"].PID)
	assert.Equal(t, 2, got["/w/idle"].PID)
	assert.Equal(t, 3, got["/w/waiting"].PID)
}

// The bridge ID is a convenience read from a file Claude owns: it is taken
// only when every check holds, and no failure of one touches the entry's
// status, which drives the session.
func TestQueryClaudeRoster_BridgeIDRules(t *testing.T) {
	const capBytes = 64 << 10 // the limit on one of Claude's session files
	padded := func(n int) string { return bridgeSessionFile + strings.Repeat(" ", n-len(bridgeSessionFile)) }
	type bridgeCase struct {
		name   string
		roster string // bridgeRosterJSON when empty
		file   string // the contents of sessions/41.json
		noFile bool
		want   string
	}
	cases := []bridgeCase{
		{name: "Remote Control is connected", file: bridgeSessionFile, want: "session_01AbC-9"},
		{name: "the file is missing", noFile: true},
		{name: "another session's file: the pid was reused", file: `{"sessionId":"s-other","bridgeSessionId":"session_01Other"}`},
		{name: "the file names no session", file: `{"bridgeSessionId":"session_01AbC"}`},
		{name: "the entry names no session", roster: `[{"pid":41,"cwd":"/w/rc","kind":"interactive","status":"idle"}]`, file: `{"bridgeSessionId":"session_01AbC"}`},
		{name: "Remote Control is not connected", file: `{"sessionId":"s-rc","status":"idle"}`},
		{name: "the file is not JSON", file: `{"sessionId":"s-rc",`},
		{name: "the bridge ID is not a string", file: `{"sessionId":"s-rc","bridgeSessionId":7}`},
		{name: "the entry has no pid", roster: `[{"cwd":"/w/rc","kind":"interactive","sessionId":"s-rc","status":"idle"}]`, file: bridgeSessionFile},
		{name: "the file is exactly the size cap", file: padded(capBytes), want: "session_01AbC-9"},
		{name: "the file is a byte over the cap", file: padded(capBytes + 1)},
		{name: "an ID of the longest length", file: bridgeFileFor(strings.Repeat("a", 128)), want: strings.Repeat("a", 128)},
	}
	for _, id := range []string{strings.Repeat("a", 129), "../session_01AbC", "session_01 AbC", "session_01?x=1", "a/b", "séssion", ""} {
		cases = append(cases, bridgeCase{name: "an ID that cannot be a path segment: " + id, file: bridgeFileFor(id)})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if !tc.noFile {
				writeSessionFile(t, dir, 41, tc.file)
			}
			roster := tc.roster
			if roster == "" {
				roster = bridgeRosterJSON
			}

			got := bridgeOf(t, dir, roster)

			assert.Equal(t, tc.want, got.RemoteSession)
			assert.Equal(t, RosterStatusIdle, got.Status, "the roster's own answer stands whatever the file says")
		})
	}
}

// Only a regular file is read: anything else at the path, a FIFO included,
// could block the query that the whole fleet's status waits on. A directory
// fails the read on its own; a symlink to a valid session file is read happily
// unless the Lstat check refuses it, so that case alone pins the guard.
func TestQueryClaudeRoster_BridgeIDNeedsARegularFile(t *testing.T) {
	t.Run("a directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "sessions", "41.json"), 0o755))

		got := bridgeOf(t, dir, bridgeRosterJSON)

		assert.Empty(t, got.RemoteSession)
		assert.Equal(t, RosterStatusIdle, got.Status)
	})
	t.Run("a symlink to a valid session file", func(t *testing.T) {
		dir, elsewhere := t.TempDir(), t.TempDir()
		target := filepath.Join(elsewhere, "41.json")
		require.NoError(t, os.WriteFile(target, []byte(bridgeSessionFile), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "sessions"), 0o755))
		if err := os.Symlink(target, filepath.Join(dir, "sessions", "41.json")); err != nil {
			t.Skipf("cannot create a symlink here: %v", err)
		}

		got := bridgeOf(t, dir, bridgeRosterJSON)

		assert.Empty(t, got.RemoteSession)
		assert.Equal(t, RosterStatusIdle, got.Status)
	})
}

// A duplicated CLAUDE_CONFIG_DIR resolves to its last value, as os/exec does
// for the claude process the roster ran under.
func TestQueryClaudeRoster_BridgeIDUsesTheLastConfigDirInEnv(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, 41, bridgeSessionFile)
	env := []string{"CLAUDE_CONFIG_DIR=/a", "CLAUDE_CONFIG_DIR=" + dir}

	got, err := QueryClaudeRosterEnv("claude", env, &fakeRosterExecutor{out: []byte(bridgeRosterJSON)})

	require.NoError(t, err)
	require.Contains(t, got, "/w/rc")
	assert.Equal(t, "session_01AbC-9", got["/w/rc"].RemoteSession)
}

// A pid this build cannot read costs its entry the bridge ID and nothing
// else, not the whole query.
func TestQueryClaudeRoster_AnUnreadablePIDCostsOnlyTheBridgeID(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, 41, bridgeSessionFile)
	for _, pid := range []string{`"41"`, `true`, `-41`, `41.5`, `null`, `[41]`, `{}`, `0`} {
		roster := `[{"pid":` + pid + `,"cwd":"/w/rc","kind":"interactive","sessionId":"s-rc","status":"idle"}]`

		got := bridgeOf(t, dir, roster)

		assert.Zero(t, got.PID, pid)
		assert.Empty(t, got.RemoteSession, pid)
		assert.Equal(t, RosterStatusIdle, got.Status, pid)
	}
}

// The files are read in the dir the query ran under: the account's, else the
// daemon's own CLAUDE_CONFIG_DIR, else ~/.claude. An account whose dir lacks
// the file is not answered from another's.
func TestQueryClaudeRoster_BridgeIDIsReadUnderTheDirTheQueryRanUnder(t *testing.T) {
	acctDir, emptyDir, daemonDir, home := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	writeSessionFile(t, acctDir, 41, bridgeFileFor("session_01Account"))
	writeSessionFile(t, daemonDir, 41, bridgeFileFor("session_01Daemon"))
	writeSessionFile(t, filepath.Join(home, ".claude"), 41, bridgeFileFor("session_01Home"))
	t.Setenv("CLAUDE_CONFIG_DIR", daemonDir)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	query := func(env []string) string {
		got, err := QueryClaudeRosterEnv("claude", env, &fakeRosterExecutor{out: []byte(bridgeRosterJSON)})
		require.NoError(t, err)
		return got["/w/rc"].RemoteSession
	}

	assert.Equal(t, "session_01Account", query(account.EnvFor(acctDir)), "an account's sessions are in its own dir")
	assert.Empty(t, query(account.EnvFor(emptyDir)), "and never in the daemon's")
	assert.Equal(t, "session_01Daemon", query(nil), "the default account runs under the daemon's own CLAUDE_CONFIG_DIR")

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	assert.Equal(t, "session_01Home", query(nil), "and without one under ~/.claude")
}
