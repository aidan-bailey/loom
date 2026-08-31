package session

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestQueryClaudeRoster_AmbiguousCwdIsDropped(t *testing.T) {
	// Two Claude sessions in one directory (e.g. the user ran claude by
	// hand inside a Loom worktree): we cannot tell which one backs the
	// instance, so the entry must be omitted and the caller falls back.
	dup := `[
	  {"cwd":"/w/dup","kind":"interactive","sessionId":"a","status":"busy"},
	  {"cwd":"/w/dup","kind":"background","sessionId":"b","status":"idle"},
	  {"cwd":"/w/solo","kind":"interactive","sessionId":"c","status":"idle"}
	]`
	fake := &fakeRosterExecutor{out: []byte(dup)}

	got, err := QueryClaudeRoster("claude", fake)

	assert.NoError(t, err)
	assert.NotContains(t, got, "/w/dup")
	assert.Contains(t, got, "/w/solo")
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
