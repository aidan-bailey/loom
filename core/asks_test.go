package core

import (
	"encoding/json"
	"testing"
	"time"

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

// answerTaken has the mod take the answer to launch L1's ask id and returns
// what it carries.
func answerTaken(t *testing.T, m *Model, id string) string {
	t.Helper()
	payload, _, err := m.ModAnswer("L1", "", id)
	require.NoError(t, err)
	require.NotNil(t, payload)
	return string(payload)
}

func TestAnswer_AQuestionComposesClaudesResult(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	_, wake, _ := m.ModAnswer("L1", "", "q1")

	m.Answer(m.idOf(inst), "q1", AskAnswer{Questions: map[string]QuestionAnswer{
		"Which color do you prefer?": {Labels: []string{"Blue"}, Note: "  greener the better "},
		"Which pets do you have?":    {Labels: []string{"Fish", "Cat"}, Other: "Hamster"},
	}}, 7)
	assert.Empty(t, replies(m.Drain()), "no reply until the mod takes it")
	assert.True(t, closed(wake), "the held poll ends")
	assert.True(t, asksOf(m, inst)[0].Answering)

	got := answerTaken(t, m, "q1")
	assert.JSONEq(t, `{"result":{"questions":`+questionsOf(t, questionAsk("q1"))+`,
		"answers":{"Which color do you prefer?":"Blue","Which pets do you have?":"Cat, Fish, Hamster"},
		"annotations":{"Which color do you prefer?":{"notes":"greener the better"}}}}`, got)
	assert.Equal(t, []Reply{{Req: 7, ID: m.idOf(inst)}}, replies(m.Drain()))
	assert.Empty(t, asksOf(m, inst))
}

func questionsOf(t *testing.T, a ModAsk) string {
	t.Helper()
	var in struct{ Questions json.RawMessage }
	require.NoError(t, json.Unmarshal(a.Input, &in))
	return string(in.Questions)
}

func TestAnswer_ChatDeniesWithClaudesClarifyText(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))

	m.Answer(m.idOf(inst), "q1", AskAnswer{Chat: true, ChatText: " the colors are wrong "}, 7)
	var got struct{ Deny string }
	require.NoError(t, json.Unmarshal([]byte(answerTaken(t, m, "q1")), &got))

	assert.Equal(t, "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). To tell you how to proceed, the user said:\n"+
		"The user wants to clarify these questions.\n"+
		"    This means they may have additional information, context or questions for you.\n"+
		"    Take their response into account and then reformulate the questions if appropriate.\n"+
		"    Start by asking them what they would like to clarify.\n\n"+
		"    Questions asked:\n"+
		"- \"Which color do you prefer?\"\n  (No answer provided)\n"+
		"- \"Which pets do you have?\"\n  (No answer provided)", got.Deny)
	msgs, _, _ := m.ModInbox("L1", "")
	require.Len(t, msgs, 1, "the chat's words follow as the person's reply")
	assert.Equal(t, "the colors are wrong", msgs[0].Text)
	assert.True(t, msgs[0].AsUser)
}

func TestAnswer_APermissionComposesClaudesDecision(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	for _, tc := range []struct {
		name   string
		answer AskAnswer
		want   string
		follow string
	}{
		{"allow", AskAnswer{Allow: true}, `{"decision":{"behavior":"allow"}}`, ""},
		{"allow with a rule", AskAnswer{Allow: true, Suggestion: 2},
			`{"decision":{"behavior":"allow","updatedPermissions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"npm test:*"}],"behavior":"allow","destination":"localSettings"}]}}`, ""},
		{"deny", AskAnswer{}, `{"decision":{"behavior":"deny","interrupt":true}}`, ""},
		{"deny with a message", AskAnswer{Message: " use third-dir "}, `{"decision":{"behavior":"deny","interrupt":true}}`, "use third-dir"},
	} {
		id := "p-" + tc.name
		require.NoError(t, m.ModAsk("L1", "", permissionAsk(id, "")))
		m.Answer(m.idOf(inst), id, tc.answer, 9)
		assert.JSONEq(t, tc.want, answerTaken(t, m, id), tc.name)
		msgs, _, _ := m.ModInbox("L1", "")
		if tc.follow == "" {
			assert.Empty(t, msgs, tc.name)
			continue
		}
		require.Len(t, msgs, 1, tc.name)
		assert.Equal(t, tc.follow, msgs[0].Text)
		require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
	}
}

func TestAnswer_Refusals(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	id := m.idOf(inst)
	both := func(color, pets QuestionAnswer) map[string]QuestionAnswer {
		return map[string]QuestionAnswer{"Which color do you prefer?": color, "Which pets do you have?": pets}
	}
	cat := QuestionAnswer{Labels: []string{"Cat"}}
	for _, tc := range []struct {
		name   string
		ask    string
		answer AskAnswer
		want   string
	}{
		{"an unknown ask", "zzz", AskAnswer{Allow: true}, "answer x: it asks nothing by that ID now"},
		{"a question left out", "q1", AskAnswer{Questions: map[string]QuestionAnswer{"Which pets do you have?": cat}}, `answer x: "Which color do you prefer?" has no answer`},
		{"an empty answer", "q1", AskAnswer{Questions: both(QuestionAnswer{}, cat)}, `answer x: "Which color do you prefer?" has no answer`},
		{"a label not offered", "q1", AskAnswer{Questions: both(QuestionAnswer{Labels: []string{"Green"}}, cat)}, `answer x: "Green" is not an option of "Which color do you prefer?"`},
		{"two answers to a single select", "q1", AskAnswer{Questions: both(QuestionAnswer{Labels: []string{"Red"}, Other: "Teal"}, cat)}, `answer x: "Which color do you prefer?" takes one answer`},
		{"a question not asked", "q1", AskAnswer{Questions: map[string]QuestionAnswer{"Which color do you prefer?": {Other: "x"}, "Which pets do you have?": cat, "Why?": {Other: "y"}}}, `answer x: it asks no question "Why?"`},
		{"chat and answers", "q1", AskAnswer{Chat: true, Questions: both(cat, cat)}, "answer x: chat about the questions or answer them, not both"},
		{"allow on a question", "q1", AskAnswer{Allow: true}, "answer x: a question takes answers or a chat, not an allow or a deny"},
		{"answers on a permission", "p1", AskAnswer{Questions: both(cat, cat)}, "answer x: a permission takes an allow or a deny"},
		{"a suggestion not offered", "p1", AskAnswer{Allow: true, Suggestion: 3}, "answer x: suggestion 3 was not offered"},
		{"a message on an allow", "p1", AskAnswer{Allow: true, Message: "x"}, "answer x: a message goes with a deny"},
		{"a suggestion on a deny", "p1", AskAnswer{Suggestion: 1}, "answer x: a suggestion goes with an allow"},
	} {
		m.Answer(id, tc.ask, tc.answer, 3)
		r := replies(m.Drain())
		require.Len(t, r, 1, tc.name)
		require.Error(t, r[0].Err, tc.name)
		assert.Equal(t, tc.want, r[0].Err.Error(), tc.name)
		assert.ErrorIs(t, r[0].Err, ErrRefused, tc.name)
	}
}

func TestAnswer_TheFirstAnswerWins(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 3)
	m.Answer(m.idOf(inst), "p1", AskAnswer{}, 4)
	r := replies(m.Drain())
	require.Len(t, r, 1)
	assert.Equal(t, ReqID(4), r[0].Req)
	assert.EqualError(t, r[0].Err, "answer x: it was already answered from another device")
	assert.JSONEq(t, `{"decision":{"behavior":"allow"}}`, answerTaken(t, m, "p1"))
}

func TestAnswer_ClaudeAnsweringFirstRefusesAWaitingAnswer(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 3)

	require.NoError(t, m.ModAskClosed("L1", "", "p1", "settled"))

	r := replies(m.Drain())
	require.Len(t, r, 1)
	assert.EqualError(t, r[0].Err, "answer x: it was answered in Claude first")
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 5)
	assert.EqualError(t, replies(m.Drain())[0].Err, "answer x: it was answered in Claude first")
}

func TestAnswer_AnAnswerNoPollTakesIsWithdrawn(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1})) // live, so the tick's grace leaves the link alone
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 3)

	m.modTick(time.Now().Add(askTakeTimeout - time.Second))
	assert.Empty(t, replies(m.Drain()))
	m.modTick(time.Now().Add(askTakeTimeout + time.Second))

	r := replies(m.Drain())
	require.Len(t, r, 1)
	assert.EqualError(t, r[0].Err, "answer x: the session did not take the answer within 30s; answer in Claude")
	payload, wake, err := m.ModAnswer("L1", "", "p1")
	require.NoError(t, err)
	assert.Nil(t, payload, "a late poll gets nothing")
	assert.NotNil(t, wake)
	assert.False(t, asksOf(m, inst)[0].Answering)
}

func TestAnswer_AnEndingLinkRefusesAWaitingAnswer(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 3)

	m.endLink(inst, m.linkFor(inst), "the session stopped")

	r := replies(m.Drain())
	require.Len(t, r, 1)
	assert.EqualError(t, r[0].Err, "answer x: the session stopped before Claude took the answer")
}
