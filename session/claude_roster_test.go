package session

import (
	"errors"
	"os/exec"
	"testing"

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
	f := &envRecordingExec{out: []byte(rosterJSON)}
	got, err := QueryClaudeRosterEnv("claude", []string{"CLAUDE_CONFIG_DIR=/acct/max-2"}, f)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	assert.Contains(t, f.env, "CLAUDE_CONFIG_DIR=/acct/max-2")
}

func TestQueryClaudeRoster_InheritsLoomsEnv(t *testing.T) {
	f := &envRecordingExec{out: []byte(rosterJSON)}
	_, err := QueryClaudeRoster("claude", f)
	require.NoError(t, err)
	assert.Nil(t, f.env)
}
