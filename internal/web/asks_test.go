package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAskOf_AQuestion(t *testing.T) {
	got := askOf(core.AskView{ID: "q1", Kind: core.AskKindQuestion, AgentType: "general-purpose", Questions: []core.AskQuestion{
		{Question: "Which?", Header: "Pick", MultiSelect: true, Options: []core.AskOption{{Label: "A", Description: "a", Preview: "pre"}}},
	}})
	assert.Equal(t, askJSON{ID: "q1", Kind: "question", Agent: "general-purpose", Questions: []askQuestionJSON{
		{Question: "Which?", Header: "Pick", Multi: true, Options: []askOptionJSON{{Label: "A", Description: "a", Preview: "pre"}}},
	}}, got)
}

func TestInputBlocks_PerTool(t *testing.T) {
	for _, tc := range []struct {
		tool, input string
		want        []blockJSON
	}{
		{"Bash", `{"command":"ls -la","description":"List files"}`, []blockJSON{{Label: "command", Text: "ls -la", Mono: true}, {Label: "description", Text: "List files"}}},
		{"Edit", `{"file_path":"/a.go","old_string":"x","new_string":"y"}`, []blockJSON{{Label: "file", Text: "/a.go", Mono: true}, {Label: "replace", Text: "x", Mono: true}, {Label: "with", Text: "y", Mono: true}}},
		{"MultiEdit", `{"file_path":"/a.go","edits":[{"old_string":"x","new_string":"y"}]}`, []blockJSON{{Label: "file", Text: "/a.go", Mono: true}, {Label: "replace", Text: "x", Mono: true}, {Label: "with", Text: "y", Mono: true}}},
		{"Write", `{"file_path":"/a.go","content":"package a"}`, []blockJSON{{Label: "file", Text: "/a.go", Mono: true}, {Label: "content", Text: "package a", Mono: true}}},
		{"WebFetch", `{"url":"https://x.dev","prompt":"read it"}`, []blockJSON{{Label: "url", Text: "https://x.dev", Mono: true}, {Label: "prompt", Text: "read it"}}},
		{"mcp__gh__issue", `{"b":1,"a":"x"}`, []blockJSON{{Label: "input", Text: "{\n  \"a\": \"x\",\n  \"b\": 1\n}", Mono: true}}},
		{"Bash", `{"command":7}`, []blockJSON{{Label: "input", Text: "{\n  \"command\": 7\n}", Mono: true}}},
	} {
		assert.Equal(t, tc.want, inputBlocks(tc.tool, tc.input), tc.tool+" "+tc.input)
	}
}

func TestInputBlocks_CapsALongText(t *testing.T) {
	long := make([]byte, maxBlockRunes+10)
	for i := range long {
		long[i] = 'x'
	}
	b := inputBlocks("Bash", `{"command":"`+string(long)+`"}`)
	assert.Equal(t, maxBlockRunes, len([]rune(b[0].Text)))
	assert.True(t, b[0].Cut)
}

func TestSuggestionOf_NamesWhatAndHowLong(t *testing.T) {
	for _, tc := range []struct {
		s    core.AskSuggestion
		want suggestionJSON
	}{
		{core.AskSuggestion{Type: "addRules", Behavior: "allow", Destination: "localSettings", Rules: []core.AskRule{{ToolName: "Bash", RuleContent: "npm test:*"}}},
			suggestionJSON{Label: "Yes, and don't ask again for Bash(npm test:*) in this project, for you", Persists: true}},
		{core.AskSuggestion{Type: "addRules", Behavior: "allow", Destination: "projectSettings", Rules: []core.AskRule{{ToolName: "Read"}}},
			suggestionJSON{Label: "Yes, and don't ask again for Read in this project", Persists: true}},
		{core.AskSuggestion{Type: "addRules", Behavior: "allow", Destination: "userSettings", Rules: []core.AskRule{{ToolName: "WebFetch", RuleContent: "domain:x.dev"}}},
			suggestionJSON{Label: "Yes, and don't ask again for WebFetch(domain:x.dev) in every project", Persists: true}},
		{core.AskSuggestion{Type: "setMode", Mode: "acceptEdits", Destination: "session"},
			suggestionJSON{Label: "Yes, and switch to accept edits for this session"}},
		{core.AskSuggestion{Type: "addDirectories", Directories: []string{"/w"}, Destination: "session"},
			suggestionJSON{Label: "Yes, and allow access to /w for this session"}},
		{core.AskSuggestion{Type: "brandNew", Destination: "session"},
			suggestionJSON{Label: "Yes, and apply Claude's brandNew suggestion for this session"}},
	} {
		assert.Equal(t, tc.want, suggestionOf(tc.s))
	}
}

func TestSessionOf_TheFirstAskIsTheWaitDetail(t *testing.T) {
	v := core.InstanceView{WaitDetail: "ls", Asks: []core.AskView{
		{ID: "p1", Kind: core.AskKindPermission, Tool: "Bash", Summary: "mkdir x"},
		{ID: "q1", Kind: core.AskKindQuestion, Questions: []core.AskQuestion{{Question: "Which?"}}},
	}}
	s := sessionOf(1, v)
	assert.Equal(t, "Bash: mkdir x (+1 more)", s.Wait.Detail)
	assert.Len(t, s.Asks, 2)
	assert.NotNil(t, sessionOf(1, core.InstanceView{}).Asks, "an empty list, never null")
}

func TestAPI_AnswerCarriesTheAnswer(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "idle", session.Running)}})
	path := fmt.Sprintf("/api/v1/sessions/%d/asks/a1/answer", env.ids["idle"])

	// The model refuses it (never started), in its own words; the call is
	// what is pinned.
	status, out := env.do(http.MethodPost, path, map[string]any{
		"answers": map[string]any{"Which?": map[string]any{"labels": []string{"A"}, "other": " x ", "note": "n"}},
	})

	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, apiErrorOf(t, out), "answer idle: the session has not started")
	env.rb.mu.Lock()
	defer env.rb.mu.Unlock()
	require.Len(t, env.rb.answered, 1)
	assert.Equal(t, answerCall{ask: "a1", answer: core.AskAnswer{Questions: map[string]core.QuestionAnswer{
		"Which?": {Labels: []string{"A"}, Other: " x ", Note: "n"},
	}}}, env.rb.answered[0])
}

func TestAPI_AnswerCarriesAPermissionsAnswer(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "idle", session.Running)}})
	path := fmt.Sprintf("/api/v1/sessions/%d/asks/p1/answer", env.ids["idle"])

	env.do(http.MethodPost, path, map[string]any{"allow": true, "suggestion": 2})
	env.do(http.MethodPost, path, map[string]any{"allow": false, "message": "no"})
	env.do(http.MethodPost, path, map[string]any{"chat": true, "chat_text": "why?"})

	env.rb.mu.Lock()
	defer env.rb.mu.Unlock()
	assert.Equal(t, []answerCall{
		{ask: "p1", answer: core.AskAnswer{Allow: true, Suggestion: 2}},
		{ask: "p1", answer: core.AskAnswer{Message: "no"}},
		{ask: "p1", answer: core.AskAnswer{Chat: true, ChatText: "why?"}},
	}, env.rb.answered)
}

func TestAPI_AnswerChecksTheBody(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "idle", session.Running)}})
	path := fmt.Sprintf("/api/v1/sessions/%d/asks/a1/answer", env.ids["idle"])
	nine := map[string]any{}
	for i := 0; i < 9; i++ {
		nine[fmt.Sprint(i)] = map[string]any{"other": "x"}
	}

	for _, c := range []struct {
		name string
		path string
		body any
	}{
		{"a field it does not know", path, map[string]any{"allow": true, "always": true}},
		{"a body that is not JSON", path, "not json"},
		{"an ask ID over 128 bytes", fmt.Sprintf("/api/v1/sessions/%d/asks/%s/answer", env.ids["idle"], strings.Repeat("a", 129)), map[string]any{"allow": true}},
		{"nine answers", path, map[string]any{"answers": nine}},
		{"nine labels", path, map[string]any{"answers": map[string]any{"q": map[string]any{"labels": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}}}}},
		{"a message over 64 KiB", path, map[string]any{"message": strings.Repeat("a", 64<<10+1)}},
	} {
		status, out := env.do(http.MethodPost, c.path, c.body)
		assert.Equal(t, http.StatusBadRequest, status, c.name)
		assert.NotEmpty(t, apiErrorOf(t, out), c.name)
	}
	env.rb.mu.Lock()
	defer env.rb.mu.Unlock()
	assert.Empty(t, env.rb.answered, "nothing it refused reached the model")
}
