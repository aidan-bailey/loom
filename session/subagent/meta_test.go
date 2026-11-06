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

func TestValidAgentID(t *testing.T) {
	for _, id := range []string{"a1", "a1f00dcafe0001", "agent-x", "a.b"} {
		assert.True(t, ValidAgentID(id), "%q", id)
	}
	for _, id := range []string{"", " ", "\t", ".", "..", "../x", "a/b", `a\b`, "/", `\`} {
		assert.False(t, ValidAgentID(id), "%q", id)
	}
}

func TestSubagentsDir(t *testing.T) {
	assert.Equal(t, "/p/-work/sess/subagents", SubagentsDir("/p/-work/sess.jsonl"))
	assert.Equal(t, "/p/-work/sess/subagents", SubagentsDir("/p/-work/sess"), "no suffix to trim")
	assert.Equal(t, "", SubagentsDir(""))
}

func TestMetaPath_UsesValidAgentID(t *testing.T) {
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", " "))
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", "."))
	assert.Equal(t, "/p/sess/subagents/agent-a.b.meta.json", MetaPath("/p/sess.jsonl", "a.b"))
}

func TestParseMeta_LinkFields(t *testing.T) {
	m, err := ParseMeta([]byte(`{"agentType":"Explore","toolUseId":"toolu_01","finished":false}`))
	require.NoError(t, err)
	assert.Equal(t, "toolu_01", m.ToolUseID)
	require.NotNil(t, m.Finished)
	assert.False(t, *m.Finished)

	m, err = ParseMeta([]byte(`{"finished":true}`))
	require.NoError(t, err)
	require.NotNil(t, m.Finished)
	assert.True(t, *m.Finished)

	m, err = ParseMeta([]byte(`{"agentType":"Explore"}`))
	require.NoError(t, err)
	assert.Empty(t, m.ToolUseID)
	assert.Nil(t, m.Finished, "an absent key is nil, not false")
}
