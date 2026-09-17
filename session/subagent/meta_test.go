package subagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
