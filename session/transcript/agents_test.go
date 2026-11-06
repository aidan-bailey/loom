package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// agentBack is the user record that carries an agent tool's result, with
// the toolUseResult Claude writes beside it (nil writes none).
func agentBack(id string, res map[string]any) map[string]any {
	return user([]any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": false, "content": "done"}},
		map[string]any{"toolUseResult": res})
}

// stepByID is the step with id in any turn of p.
func stepByID(t *testing.T, p Page, id string) Step {
	t.Helper()
	for _, turn := range p.Turns {
		for _, it := range turn.Items {
			for _, s := range it.Steps {
				if s.ID == id {
					return s
				}
			}
		}
	}
	require.Fail(t, "no step "+id)
	return Step{}
}

func TestRead_AgentStepsLinkToTheirSubagents(t *testing.T) {
	p, err := Read(fixtureTranscript(t), conv, Query{})
	require.NoError(t, err)
	require.Len(t, p.Turns, 1)
	turn := p.Turns[0]
	require.Equal(t, []string{KindThinking, KindSteps, KindText}, kinds(turn.Items))
	assert.Equal(t, "Still in design: the web layer first, then the docs.", turn.Items[0].Text,
		"the blank and the redacted thinking blocks before it add nothing")
	steps := turn.Items[1].Steps
	require.Len(t, steps, 4)

	type link struct {
		id, tool, agent string
		finished        bool
		state           string
	}
	var got []link
	for _, s := range steps {
		got = append(got, link{s.ID, s.Tool, s.Agent, s.Finished, s.State})
	}
	assert.Equal(t, []link{
		{"toolu_fg", "Agent", "a1f00dcafe0001", true, StateDone},  // sidecar and result agree
		{"toolu_bg", "Agent", "a2f00dcafe0002", false, StateDone}, // launched: the result settles, the sidecar says running
		{"toolu_old", "Task", "a3f00dcafe0003", true, StateDone},  // no sidecar: the result names it
		{"toolu_wait", "Agent", "", false, StatePending},          // no result, no sidecar
	}, got)
	assert.Equal(t, "Survey the web layer", steps[0].Summary)
}

func TestRead_AgentStepFinishedAndLinkRules(t *testing.T) {
	meta := func(m string) map[string]string { return map[string]string{"agent-a9.meta.json": m} }
	for _, tc := range []struct {
		name         string
		tool         string // "Agent" when empty
		pending      bool   // the step has no result yet
		result       map[string]any
		files        map[string]string
		wantAgent    string
		wantFinished bool
	}{
		{name: "a sidecar links a step with no result, and says running", pending: true,
			files: meta(`{"toolUseId":"tu","finished":false}`), wantAgent: "a9"},
		{name: "a sidecar that says finished settles a step with no result", pending: true,
			files: meta(`{"toolUseId":"tu","finished":true}`), wantAgent: "a9", wantFinished: true},
		{name: "a completed result names the agent", result: map[string]any{"status": "completed", "agentId": "a8"},
			wantAgent: "a8", wantFinished: true},
		{name: "a launch result names the agent but is not the end", result: map[string]any{"status": "async_launched", "agentId": "a8"},
			wantAgent: "a8"},
		{name: "the sidecar's finished beats a launch result", result: map[string]any{"status": "async_launched", "agentId": "a9"},
			files: meta(`{"toolUseId":"tu","finished":true}`), wantAgent: "a9", wantFinished: true},
		{name: "the sidecar's finished false beats a completed result", result: map[string]any{"status": "completed", "agentId": "a9"},
			files: meta(`{"toolUseId":"tu","finished":false}`), wantAgent: "a9"},
		{name: "a sidecar without the key falls back to a launch result", result: map[string]any{"status": "async_launched", "agentId": "a9"},
			files: meta(`{"toolUseId":"tu"}`), wantAgent: "a9"},
		{name: "a sidecar without the key falls back to a completed result", result: map[string]any{"status": "completed", "agentId": "a9"},
			files: meta(`{"toolUseId":"tu"}`), wantAgent: "a9", wantFinished: true},
		{name: "the sidecar names the agent before the result does", result: map[string]any{"status": "completed", "agentId": "a8"},
			files: meta(`{"toolUseId":"tu"}`), wantAgent: "a9", wantFinished: true},
		{name: "no agent anywhere: unlinked, though the result ended the step", result: map[string]any{"status": "completed"},
			files: meta(`{"agentType":"Explore"}`), wantFinished: true},
		{name: "a result with no toolUseResult at all", files: meta(`{"agentType":"Explore"}`), wantFinished: true},
		{name: "two sidecars claim one tool use: neither links", pending: true,
			files: map[string]string{"agent-a1.meta.json": `{"toolUseId":"tu"}`, "agent-a2.meta.json": `{"toolUseId":"tu"}`}},
		{name: "two sidecars claim one tool use: the result still names it", result: map[string]any{"status": "completed", "agentId": "a8"},
			files:     map[string]string{"agent-a1.meta.json": `{"toolUseId":"tu"}`, "agent-a2.meta.json": `{"toolUseId":"tu"}`},
			wantAgent: "a8", wantFinished: true},
		{name: "an agent id that would escape the folder is dropped", result: map[string]any{"status": "completed", "agentId": "../x"},
			wantFinished: true},
		{name: "a sidecar over 64 KiB is not read", pending: true,
			files: meta(`{"toolUseId":"tu","description":"` + strings.Repeat("x", 70<<10) + `"}`)},
		{name: "a broken sidecar is skipped and the others still link", pending: true,
			files: map[string]string{"agent-a7.meta.json": "{", "agent-a9.meta.json": `{"toolUseId":"tu"}`}, wantAgent: "a9"},
		{name: "a sidecar of another tool use does not link", pending: true,
			files: meta(`{"toolUseId":"another"}`)},
		{name: "Task is an agent tool too", tool: "Task", result: map[string]any{"status": "completed", "agentId": "a8"},
			wantAgent: "a8", wantFinished: true},
		{name: "another tool is never linked or finished", tool: "Read", result: map[string]any{"agentId": "a8"},
			files: meta(`{"toolUseId":"tu","finished":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.tool
			if name == "" {
				name = "Agent"
			}
			f := newTFile(t)
			f.add(typed("go"))
			f.add(tool("tu", name, map[string]any{"description": "work", "prompt": "p"}))
			if !tc.pending {
				f.add(agentBack("tu", tc.result))
			}
			p, err := Read(withAgents(t, f, tc.files), conv, Query{})
			require.NoError(t, err)
			s := stepByID(t, p, "tu")
			assert.Equal(t, tc.wantAgent, s.Agent)
			assert.Equal(t, tc.wantFinished, s.Finished)
		})
	}
}

func TestRead_AResultWithSeveralToolResultsNamesNoAgent(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	f.add(tool("u1", "Agent", map[string]any{"description": "one"}))
	f.add(tool("u2", "Agent", map[string]any{"description": "two"}))
	f.add(user([]any{
		map[string]any{"type": "tool_result", "tool_use_id": "u1", "content": "a"},
		map[string]any{"type": "tool_result", "tool_use_id": "u2", "content": "b"},
	}, map[string]any{"toolUseResult": map[string]any{"status": "completed", "agentId": "a8"}}))
	p := f.read(Query{})
	for _, id := range []string{"u1", "u2"} {
		s := stepByID(t, p, id)
		assert.Empty(t, s.Agent, id)
		assert.Equal(t, StateDone, s.State, id)
	}
}

func TestRead_AnUnreadableSubagentsFolderLeavesStepsUnlinkedAndTheResultsLinks(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	f.add(tool("t1", "Agent", map[string]any{"description": "no result id"}))
	f.add(agentBack("t1", map[string]any{"status": "completed"}))
	f.add(tool("t2", "Agent", map[string]any{"description": "named by its result"}))
	f.add(agentBack("t2", map[string]any{"status": "completed", "agentId": "a8"}))
	path := f.path()
	// A file where the folder should be: listing it fails, even for root.
	require.NoError(t, os.MkdirAll(filepath.Dir(subagent.SubagentsDir(path)), 0o700))
	require.NoError(t, os.WriteFile(subagent.SubagentsDir(path), []byte("x"), 0o600))

	p, err := Read(path, conv, Query{})
	require.NoError(t, err, "the page still answers")
	assert.Empty(t, stepByID(t, p, "t1").Agent)
	assert.Equal(t, "a8", stepByID(t, p, "t2").Agent)
}

func TestRead_AgentPageLinksItsOwnAgentSteps(t *testing.T) {
	sub := newTFile(t)
	sub.add(typed("Split the work"))
	sub.add(tool("tn", "Agent", map[string]any{"description": "nested"}))
	parent := newTFile(t)
	parent.add(typed("go"))
	path := withAgents(t, parent, map[string]string{
		"agent-ap.jsonl":       sub.b.String(),
		"agent-nest.meta.json": `{"toolUseId":"tn","finished":false}`,
	})

	p, err := Read(path, conv, Query{Agent: "ap"})
	require.NoError(t, err)
	s := stepByID(t, p, "tn")
	assert.Equal(t, "nest", s.Agent, "a nested agent's sidecar sits in the same folder")
	assert.False(t, s.Finished)
}

func TestRead_AgentInfoIgnoresASidecarOver64KiB(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	path := withAgents(t, f, map[string]string{
		"agent-big.jsonl":     `{"isSidechain":true,"type":"user","timestamp":"2026-11-06T09:00:00.000Z","message":{"role":"user","content":"the brief"}}` + "\n",
		"agent-big.meta.json": `{"agentType":"Explore","finished":true,"description":"` + strings.Repeat("x", 70<<10) + `"}`,
	})
	p, err := Read(path, conv, Query{Agent: "big"})
	require.NoError(t, err)
	require.NotNil(t, p.Agent)
	assert.Equal(t, AgentInfo{ID: "big", Description: "the brief", State: AgentUnknown, Started: p.Agent.Started}, *p.Agent)
	assert.False(t, p.Agent.Started.IsZero())
}
