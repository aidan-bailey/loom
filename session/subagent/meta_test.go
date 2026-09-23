package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

func TestParseMeta_Plain(t *testing.T) {
	m, err := ParseMeta(fixture(t, "meta_plain.json"))
	require.NoError(t, err)
	assert.Equal(t, "Explore", m.AgentType)
	assert.Equal(t, "probe sync", m.Description)
	assert.False(t, m.IsTeammate())
	assert.Equal(t, "Explore", m.DisplayName(), "no name falls back to the agent type")
}

func TestParseMeta_Teammate(t *testing.T) {
	m, err := ParseMeta(fixture(t, "meta_teammate.json"))
	require.NoError(t, err)
	assert.True(t, m.IsTeammate())
	assert.Equal(t, "probe-mate", m.DisplayName())
	assert.Equal(t, "probe teammate", m.Description)
}

func TestParseMeta_Invalid(t *testing.T) {
	_, err := ParseMeta([]byte("{"))
	assert.Error(t, err)
}

func TestMetaPath(t *testing.T) {
	assert.Equal(t,
		"/p/-work/sess/subagents/agent-a1.meta.json",
		MetaPath("/p/-work/sess.jsonl", "a1"))
	assert.Equal(t, "", MetaPath("", "a1"))
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", ""))
	// agent IDs come from a payload; never let one escape the folder.
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", "../x"))
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", `a\b`))
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", ".."))
}

func TestReadMeta_StartsAndMissing(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "sess.jsonl")
	subagents := filepath.Join(root, "sess", "subagents")
	require.NoError(t, os.MkdirAll(subagents, 0o700))
	writeMeta := func(id, desc string) {
		require.NoError(t, os.WriteFile(filepath.Join(subagents, "agent-"+id+".meta.json"),
			[]byte(fmt.Sprintf(`{"agentType":"Explore","description":%q}`, desc)), 0o600))
	}
	writeMeta("a1", "from start")
	writeMeta("a2", "from retry")

	events := []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: transcriptPath}}
	got := ReadMeta(events, []MetaRef{
		{AgentID: "a2", Path: filepath.Join(subagents, "agent-a2.meta.json")},
		{AgentID: "a3", Path: filepath.Join(subagents, "agent-a3.meta.json")},
	})

	assert.Equal(t, map[string]Meta{
		"a1": {AgentType: "Explore", Description: "from start"},
		"a2": {AgentType: "Explore", Description: "from retry"},
	}, got)
}
