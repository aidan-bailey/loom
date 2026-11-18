package gen

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/internal/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContract_ReadsTheTablesAndUnions(t *testing.T) {
	c := web.Contract()
	require.NotEmpty(t, c.Routes)
	var open []string
	for _, r := range c.Routes {
		if r.Open {
			open = append(open, r.Method+" "+r.Path)
		}
		assert.NotEmpty(t, r.Client, r.Method+" "+r.Path)
		assert.NotEmpty(t, r.Doc, r.Method+" "+r.Path)
	}
	assert.Equal(t, []string{"POST /api/v1/pair"}, open, "the table's open set")

	var events []string
	for _, e := range c.Events {
		events = append(events, e.Name)
		assert.NotNil(t, e.Data, e.Name)
	}
	assert.Equal(t, []string{"snapshot", "sessions", "workspaces", "usage", "quick_replies", "device", "notice", "ping", "bye"}, events)

	names := map[string]bool{}
	for _, u := range c.Unions {
		names[u.Name] = true
		assert.NotEmpty(t, u.Values, u.Name)
	}
	for _, want := range []string{
		"SessionGroup", "SessionStatus", "StopVerdict", "JobState", "JobControl", "TurnFrom", "ItemKind",
		"StepState", "AgentPageState", "FileStatus", "FileOmitted", "AskKind", "PrState", "PrChecks",
		"FlowStageName", "FlowStageState", "FlowStatus", "FlowCell", "FlowStepKind", "FlowQuestionKind",
	} {
		assert.True(t, names[want], "the contract's unions hold %s", want)
	}
}

func TestFields_ReadFormatAndDrop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.fields")
	require.NoError(t, os.WriteFile(path, []byte("A.x: string\n\nB.y?: number\n"), 0o644))
	got, err := ReadFields(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"A.x: string", "B.y?: number"}, got, "blank lines are not lines")
	assert.Equal(t, "A.x: string\nB.y?: number\n", string(FormatFields(got)))
	assert.Nil(t, FormatFields(nil))

	assert.Equal(t, []string{"A.x: string"}, Dropped(got, []string{"B.y?: number", "A.x: number"}),
		"a committed line the fresh set lacks is dropped, even when only its type changed")
	assert.Empty(t, Dropped(got, []string{"A.x: string", "B.y?: number", "C.z: boolean"}), "a new line drops nothing")

	_, err = ReadFields(filepath.Join(t.TempDir(), "missing"))
	assert.ErrorIs(t, err, fs.ErrNotExist)
}
