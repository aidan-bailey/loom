package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/session/subagent"
)

// fixtureTranscript is the parent transcript under testdata/subagents; its
// subagents folder holds the agent files modelled on Claude's. Tests only
// read it.
func fixtureTranscript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "subagents", conv+".jsonl"))
	require.NoError(t, err)
	return p
}

// withAgents writes f as the conversation's transcript, and files (name to
// content) into its subagents folder, and returns the transcript's path.
func withAgents(t *testing.T, f *tfile, files map[string]string) string {
	t.Helper()
	path := f.path()
	dir := subagent.SubagentsDir(path)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	for name, data := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600))
	}
	return path
}

func TestRead_AgentReadsTheSubagentsOwnTurns(t *testing.T) {
	p, err := Read(fixtureTranscript(t), conv, Query{Agent: "a1f00dcafe0001"})
	require.NoError(t, err)
	assert.Equal(t, conv, p.Conversation)
	assert.Zero(t, p.Earlier)
	assert.False(t, p.Reset)
	assert.Zero(t, p.Skipped)
	require.Len(t, p.Turns, 2, "the brief, and the prompt that continued the agent")

	brief := p.Turns[0]
	assert.Equal(t, FromBrief, brief.From)
	assert.Equal(t, "List the web layer's routes. Report in two lines.", brief.Text)
	assert.Zero(t, brief.ID, "the brief is the file's first record")
	assert.False(t, brief.Command)
	require.Equal(t, []string{KindThinking, KindSteps, KindText}, kinds(brief.Items),
		"the sidechain records are read, the blank thinking block is not")
	assert.Equal(t, "Routes live in internal/web/reads.go.", brief.Items[0].Text)
	assert.Equal(t, []string{"internal/web/reads.go"}, summaries(brief.Items[1].Steps))
	assert.Equal(t, []string{StateDone}, states(brief.Items[1].Steps))
	assert.Equal(t, "Seven routes, all under /api/v1.", brief.Items[2].Text)

	follow := p.Turns[1]
	assert.Equal(t, FromYou, follow.From, "a later prompt parses as it does in the conversation")
	assert.Equal(t, "And the websocket?", follow.Text)
	require.Len(t, follow.Items, 1)
	assert.Equal(t, "None; it polls.", follow.Items[0].Text)
}

func TestRead_AConversationReadHasNoAgentInfo(t *testing.T) {
	p, err := Read(fixtureTranscript(t), conv, Query{})
	require.NoError(t, err)
	assert.Nil(t, p.Agent)
	for _, turn := range p.Turns {
		assert.NotEqual(t, FromBrief, turn.From)
	}
}

func TestRead_AgentInfoComesFromTheSidecarAndTheFile(t *testing.T) {
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339Nano, s)
		require.NoError(t, err)
		return ts
	}
	for name, tc := range map[string]struct {
		id      string
		want    AgentInfo
		started time.Time
		turns   bool
	}{
		"finished": {"a1f00dcafe0001",
			AgentInfo{ID: "a1f00dcafe0001", Type: "Explore", Description: "Survey the web layer", State: AgentFinished},
			at("2026-11-06T09:00:02.000Z"), true},
		"running": {"a2f00dcafe0002",
			AgentInfo{ID: "a2f00dcafe0002", Type: "general-purpose", Description: "Run the slow checks", State: AgentRunning},
			at("2026-11-06T09:00:07.800Z"), true},
		"a sidecar without the finished key": {"a4f00dcafe0004",
			AgentInfo{ID: "a4f00dcafe0004", Type: "Plan", Description: "Plan the migration", State: AgentUnknown},
			at("2026-11-06T09:00:09.000Z"), true},
		"no sidecar: the brief's first line describes it": {"a5f00dcafe0005",
			AgentInfo{ID: "a5f00dcafe0005", Description: "Check the sidecar docs", State: AgentUnknown},
			at("2026-11-06T09:00:11.000Z"), true},
		"no files: the record is gone": {"a3f00dcafe0003",
			AgentInfo{ID: "a3f00dcafe0003", State: AgentUnknown, Missing: true},
			time.Time{}, false},
	} {
		p, err := Read(fixtureTranscript(t), conv, Query{Agent: tc.id})
		require.NoError(t, err, name)
		require.NotNil(t, p.Agent, name)
		got := *p.Agent
		assert.True(t, tc.started.Equal(got.Started), "%s: started %v", name, got.Started)
		got.Started = time.Time{}
		assert.Equal(t, tc.want, got, name)
		assert.Equal(t, tc.turns, len(p.Turns) > 0, name)
		assert.Equal(t, conv, p.Conversation, name)
	}
}

func TestRead_AgentTeammateComesFromTheSidecar(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, map[string]string{
		"agent-mate.jsonl":     `{"isSidechain":true,"type":"user","timestamp":"2026-11-06T09:00:00.000Z","message":{"role":"user","content":"hi"}}` + "\n",
		"agent-mate.meta.json": `{"name":"probe-mate","taskKind":"in_process_teammate","finished":false}`,
	})
	p, err := Read(path, conv, Query{Agent: "mate"})
	require.NoError(t, err)
	assert.True(t, p.Agent.Teammate)
	assert.Equal(t, AgentRunning, p.Agent.State)
}

func TestRead_AgentWithASidecarButNoFileIsAPageNotAnError(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, map[string]string{
		"agent-early.meta.json": `{"agentType":"Explore","description":"just launched","finished":false}`,
	})
	p, err := Read(path, conv, Query{Agent: "early"})
	require.NoError(t, err)
	assert.Empty(t, p.Turns)
	assert.Zero(t, p.Earlier)
	require.NotNil(t, p.Agent)
	assert.Equal(t, AgentInfo{ID: "early", Type: "Explore", Description: "just launched", State: AgentRunning, Missing: true}, *p.Agent)
}

func TestRead_AgentRefusesABadID(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, nil)
	for _, id := range []string{" ", "\t", ".", "..", "../x", "a/b", `a\b`, "../" + conv} {
		_, err := Read(path, conv, Query{Agent: id})
		assert.EqualError(t, err, fmt.Sprintf("transcript: agent %q: not an agent id", id), "%q", id)
	}
}

func TestRead_AgentFileThatIsNotAFileIsAnError(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, nil)
	require.NoError(t, os.Mkdir(filepath.Join(subagent.SubagentsDir(path), "agent-dir.jsonl"), 0o700))
	_, err := Read(path, conv, Query{Agent: "dir"})
	assert.ErrorContains(t, err, "not a regular file")
}

func TestRead_AgentStillChecksTheConversationsPath(t *testing.T) {
	_, err := Read(conv+".jsonl", conv, Query{Agent: "a1"})
	assert.ErrorContains(t, err, "not an absolute path")
	_, err = Read(fixtureTranscript(t), "other", Query{Agent: "a1"})
	assert.ErrorContains(t, err, "not the transcript of conversation")
	_, err = Read(fixtureTranscript(t), conv, Query{Agent: "a1", Before: 5, After: 5})
	assert.ErrorContains(t, err, "before or after")
}

func TestRead_AgentPagesAndRefreshesLikeTheConversation(t *testing.T) {
	sub := newTFile(t)
	brief := sub.add(typed("Count the routes"))
	require.Zero(t, brief)
	sub.add(said("counting"))
	var ids []int64
	for i := 1; i <= 12; i++ {
		ids = append(ids, sub.add(typed(fmt.Sprintf("p%d", i))))
		sub.add(said(fmt.Sprintf("a%d", i)))
	}
	parent := newTFile(t)
	parent.add(typed("go"))
	path := withAgents(t, parent, map[string]string{"agent-ap.jsonl": sub.b.String()})

	newest, err := Read(path, conv, Query{Agent: "ap"})
	require.NoError(t, err)
	require.Len(t, newest.Turns, maxTurns)
	assert.Equal(t, "p3", newest.Turns[0].Text)
	assert.Equal(t, ids[2], newest.Earlier)
	require.NotNil(t, newest.Agent)
	assert.Equal(t, "ap", newest.Agent.ID)

	before, err := Read(path, conv, Query{Agent: "ap", Before: newest.Earlier})
	require.NoError(t, err)
	assert.Equal(t, []string{"Count the routes", "p1", "p2"}, texts(before.Turns))
	assert.Equal(t, FromBrief, before.Turns[0].From)
	assert.Equal(t, FromYou, before.Turns[1].From, "only the first record is the brief")
	assert.Zero(t, before.Earlier)
	require.NotNil(t, before.Agent)
	assert.Equal(t, "Count the routes", before.Agent.Description, "no sidecar: the brief's first line")

	after, err := Read(path, conv, Query{Agent: "ap", Conversation: conv, After: ids[10]})
	require.NoError(t, err)
	assert.Equal(t, []string{"p11", "p12"}, texts(after.Turns))
	assert.Equal(t, FromYou, after.Turns[0].From)
	assert.False(t, after.Reset)
	assert.Zero(t, after.Earlier)
	require.NotNil(t, after.Agent)

	other, err := Read(path, conv, Query{Agent: "ap", Conversation: "an-earlier-conversation"})
	require.NoError(t, err)
	assert.True(t, other.Reset, "another conversation's cursor is a reset of the agent's newest page")
	assert.Len(t, other.Turns, maxTurns)
}
