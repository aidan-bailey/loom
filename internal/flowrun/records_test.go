package flowrun

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentRecord_SaveLoadAndRefusals(t *testing.T) {
	dir := t.TempDir()
	rec := &AgentRecord{Schema: AgentSchema, ID: "r-s1-dev1", Role: "intermediate-dev", Status: AgentRunning, Jobs: []AgentRecordJob{{ID: 7}}}
	require.NoError(t, SaveAgent(dir, rec))
	got, err := LoadAgent(dir, "r-s1-dev1")
	require.NoError(t, err)
	assert.Equal(t, rec, got)
	assert.False(t, got.Ended())
	assert.Equal(t, sched.JobID(7), got.Current().ID)

	require.NoError(t, os.WriteFile(AgentPath(dir, "other"), []byte(`{"schema":99,"jobs":[{"id":1}]}`), 0o600))
	_, err = LoadAgent(dir, "other")
	assert.ErrorContains(t, err, "schema 99")
	require.NoError(t, os.WriteFile(AgentPath(dir, "nojob"), []byte(`{"schema":1,"jobs":[]}`), 0o600))
	_, err = LoadAgent(dir, "nojob")
	assert.ErrorContains(t, err, "no job")
	require.NoError(t, os.WriteFile(AgentPath(dir, "torn"), []byte(`{`), 0o600))
	_, err = LoadAgent(dir, "torn")
	assert.Error(t, err)
}

func TestGateRecord_SaveLoadAndVerdicts(t *testing.T) {
	dir := t.TempDir()
	rec := &GateRecord{Schema: GateSchema, Name: "r-s1-gates1", Kind: "story", Steps: []GateRecordStep{
		{Line: "RUN go test ./...", Run: true, Jobs: []sched.JobID{3, 9}, Verdict: "PASS"},
		{Line: "RUN go vet ./...", Run: true, Jobs: []sched.JobID{4}, Verdict: "FAIL"},
	}}
	require.NoError(t, SaveGate(dir, rec))
	got, err := LoadGate(dir, "r-s1-gates1")
	require.NoError(t, err)
	assert.Equal(t, rec, got)
	assert.True(t, got.Ended())
	assert.True(t, got.Failed())
	assert.Equal(t, sched.JobID(9), got.Steps[0].Job())
	assert.Equal(t, sched.JobID(0), GateRecordStep{}.Job())
	assert.Equal(t, "go test ./...", got.Steps[0].Text())
	assert.Equal(t, 2, got.GateOf(4))
	assert.Equal(t, 0, got.GateOf(0))

	open := &GateRecord{Steps: []GateRecordStep{{Line: "RUN x", Verdict: "PASS"}, {Line: "RUN y"}}}
	assert.False(t, open.Ended())
	assert.False(t, open.Failed())
	open.Cancelled = true
	assert.True(t, open.Ended())

	_, err = LoadGate(dir, "missing")
	assert.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.WriteFile(GatePath(dir, "old"), []byte(`{"schema":2}`), 0o600))
	_, err = LoadGate(dir, "old")
	assert.ErrorContains(t, err, "schema 2")
}
