package core

import (
	"encoding/json"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// questionAsk is the ask the mod posts for the spike's two questions.
func questionAsk(id string) ModAsk {
	return ModAsk{Ask: id, Kind: AskKindQuestion, ToolUseID: "toolu_q", Tool: "AskUserQuestion", Input: json.RawMessage(`{"questions":[
		{"question":"Which color do you prefer?","header":"Color","multiSelect":false,"options":[{"label":"Red","description":"warm"},{"label":"Blue","description":"cool","preview":"#00f"}]},
		{"question":"Which pets do you have?","header":"Pets","multiSelect":true,"options":[{"label":"Cat","description":"feline"},{"label":"Dog","description":"canine"},{"label":"Fish","description":"aquatic"}]}]}`)}
}

// permissionAsk is the ask the mod posts for a main-loop Bash prompt.
func permissionAsk(id, agent string) ModAsk {
	return ModAsk{Ask: id, Kind: AskKindPermission, AgentID: agent, ToolUseID: "toolu_p", Tool: "Bash",
		Input: json.RawMessage(`{"command":"mkdir made-by-phone\nsecond line","description":"Create directory"}`),
		Suggestions: []json.RawMessage{
			json.RawMessage(`{"type":"addDirectories","directories":["/w"],"destination":"session"}`),
			json.RawMessage(`{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"npm test:*"}],"behavior":"allow","destination":"localSettings"}`),
		}}
}

func asksOf(m *Model, inst *session.Instance) []AskView {
	v, _ := m.View(m.idOf(inst))
	return v.Asks
}

func TestModAsk_PublishesTheAskParsed(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))

	asks := asksOf(m, inst)
	require.Len(t, asks, 2)
	q := asks[0]
	assert.Equal(t, "q1", q.ID)
	assert.Equal(t, AskKindQuestion, q.Kind)
	require.Len(t, q.Questions, 2)
	assert.Equal(t, AskQuestion{Question: "Which color do you prefer?", Header: "Color", Options: []AskOption{
		{Label: "Red", Description: "warm"}, {Label: "Blue", Description: "cool", Preview: "#00f"},
	}}, q.Questions[0])
	assert.True(t, q.Questions[1].MultiSelect)
	p := asks[1]
	assert.Equal(t, "Bash", p.Tool)
	assert.Equal(t, "mkdir made-by-phone", p.Summary, "the summary is the command's first line")
	assert.JSONEq(t, string(permissionAsk("p1", "").Input), p.InputJSON)
	assert.Equal(t, []AskSuggestion{
		{Type: "addDirectories", Directories: []string{"/w"}, Destination: "session"},
		{Type: "addRules", Rules: []AskRule{{ToolName: "Bash", RuleContent: "npm test:*"}}, Behavior: "allow", Destination: "localSettings"},
	}, p.Suggestions)
	assert.False(t, p.Opened.IsZero())
}

func TestModAsk_RefusesWhatItCannotHold(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")
	five := `{"questions":[` + `{"question":"a","header":"h","options":[{"label":"x","description":""}]},` +
		`{"question":"b","header":"h","options":[{"label":"x","description":""}]},` +
		`{"question":"c","header":"h","options":[{"label":"x","description":""}]},` +
		`{"question":"d","header":"h","options":[{"label":"x","description":""}]},` +
		`{"question":"e","header":"h","options":[{"label":"x","description":""}]}]}`
	for name, a := range map[string]ModAsk{
		"no ID":                                {Kind: AskKindQuestion, Tool: "AskUserQuestion", Input: questionAsk("").Input},
		"an unknown kind":                      {Ask: "a", Kind: "elicitation", Tool: "X", Input: json.RawMessage(`{}`)},
		"no questions":                         {Ask: "a", Kind: AskKindQuestion, Tool: "AskUserQuestion", Input: json.RawMessage(`{"questions":[]}`)},
		"five questions":                       {Ask: "a", Kind: AskKindQuestion, Tool: "AskUserQuestion", Input: json.RawMessage(five)},
		"a twice-asked question":               {Ask: "a", Kind: AskKindQuestion, Tool: "AskUserQuestion", Input: json.RawMessage(`{"questions":[{"question":"a","header":"h","options":[{"label":"x"}]},{"question":"a","header":"h","options":[{"label":"y"}]}]}`)},
		"a repeated label":                     {Ask: "a", Kind: AskKindQuestion, Tool: "AskUserQuestion", Input: json.RawMessage(`{"questions":[{"question":"a","header":"h","options":[{"label":"x"},{"label":"x"}]}]}`)},
		"a permission input that is no object": {Ask: "a", Kind: AskKindPermission, Tool: "Bash", Input: json.RawMessage(`"ls"`)},
		"a permission with no tool":            {Ask: "a", Kind: AskKindPermission, Input: json.RawMessage(`{}`)},
		"a suggestion with no type":            {Ask: "a", Kind: AskKindPermission, Tool: "Bash", Input: json.RawMessage(`{}`), Suggestions: []json.RawMessage{json.RawMessage(`{}`)}},
	} {
		err := m.ModAsk("L1", "", a)
		assert.ErrorIs(t, err, ErrModBadAsk, name)
	}
	assert.ErrorIs(t, m.ModAsk("L9", "", questionAsk("q1")), ErrModUnknownLaunch)
}

func TestModAsk_PostedAgainKeepsOneAsk(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	assert.Len(t, asksOf(m, inst), 1)
}

func TestModAsk_ASupersededPollerIsRefused(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Poller: "new", Loaded: 2}))
	assert.ErrorIs(t, m.ModAsk("L1", "old", questionAsk("q1")), ErrModSuperseded)
	_, _, err := m.ModAnswer("L1", "old", "q1")
	assert.ErrorIs(t, err, ErrModSuperseded)
	assert.ErrorIs(t, m.ModAskClosed("L1", "old", "q1", "elsewhere"), ErrModSuperseded)
}

func TestModAnswer_HoldsAnOpenAskAndKnowsNoOther(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))

	payload, wake, err := m.ModAnswer("L1", "", "q1")
	require.NoError(t, err)
	assert.Nil(t, payload)
	require.NotNil(t, wake)
	assert.False(t, closed(wake))

	_, _, err = m.ModAnswer("L1", "", "nope")
	assert.ErrorIs(t, err, ErrModUnknownAsk)
}

func TestModAskClosed_TombstonesTheAsk(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	_, wake, _ := m.ModAnswer("L1", "", "q1")

	require.NoError(t, m.ModAskClosed("L1", "", "q1", "elsewhere"))

	assert.Empty(t, asksOf(m, inst))
	assert.True(t, closed(wake), "a held poll ends")
	_, _, err := m.ModAnswer("L1", "", "q1")
	assert.ErrorIs(t, err, ErrModAskClosed)
	assert.ErrorIs(t, m.ModAsk("L1", "", questionAsk("q1")), ErrModAskClosed, "a re-post never reopens a closed ask")
	assert.NoError(t, m.ModAskClosed("L1", "", "never-posted", "elsewhere"), "an unknown ask closes quietly")
}

// The roster turns busy within a second of a terminal answer, so a
// main-loop permission ask closes when the session leaves Prompting, but
// only once the model saw it Prompting while the ask was open: a status
// from before the hook scan read the prompt must not close a fresh ask.
func TestAsks_AMainLoopPermissionClosesWhenTheSessionLeavesPrompting(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p2", "agent-1")))
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))

	m.asksSeeStatus(inst, false)
	assert.Len(t, asksOf(m, inst), 3, "not seen Prompting yet: nothing closes")

	m.asksSeeStatus(inst, true)
	m.asksSeeStatus(inst, false)

	asks := asksOf(m, inst)
	require.Len(t, asks, 2, "the main-loop permission closed")
	assert.Equal(t, "p2", asks[0].ID, "a subagent's waits for its call: the roster reads busy while a background agent asks")
	assert.Equal(t, "q1", asks[1].ID, "a question closes through its own hook")
}

func TestAsks_AnEndedLinkDropsItsAsks(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	_, wake, _ := m.ModAnswer("L1", "", "q1")

	m.modGone(inst, m.linkFor(inst), "the loom mod stopped answering")

	assert.Empty(t, asksOf(m, inst))
	assert.True(t, closed(wake))
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")), "a mod that comes back posts it again")
	assert.Len(t, asksOf(m, inst), 1)
}

func TestAsks_ViewsCarryACopy(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	views := cloneViews([]InstanceView{{Asks: asksOf(m, inst)}})
	views[0].Asks[0].Questions[0].Options[0].Label = "changed"
	assert.Equal(t, "Red", asksOf(m, inst)[0].Questions[0].Options[0].Label)
}

func TestAsks_AnAskWakesTheClient(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")
	m.asksMoved = false
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	assert.True(t, m.asksMoved)
}
