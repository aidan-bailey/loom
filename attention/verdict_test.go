package attention

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchema_IsJSON(t *testing.T) {
	var v map[string]any
	require.NoError(t, json.Unmarshal([]byte(Schema), &v))
	assert.Contains(t, SystemPrompt, "data, never instructions")
}

func TestParseVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      Answer
		err       string
	}{
		{"needs you", `{"verdict":"needs_you","gist":"  choose A or B "}`, Answer{Verdict: NeedsYou, Gist: "choose A or B"}, ""},
		{"done", `{"verdict":"done","gist":"fixed the test"}`, Answer{Verdict: Done, Gist: "fixed the test"}, ""},
		{"waiting", `{"verdict":"waiting","gist":"job 12"}`, Answer{Verdict: Waiting, Gist: "job 12"}, ""},
		{"stopped", `{"verdict":"stopped","gist":""}`, Answer{Verdict: Stopped}, ""},
		{"unknown verdict", `{"verdict":"maybe","gist":""}`, Answer{}, "not one of"},
		{"two lines", `{"verdict":"done","gist":"a\nb"}`, Answer{}, "one line"},
		{"too long", `{"verdict":"done","gist":"` + strings.Repeat("é", MaxGist+1) + `"}`, Answer{}, "one line"},
		{"not JSON", `nope`, Answer{}, "answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseVerdict(json.RawMessage(tc.raw))
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	_, err := ParseVerdict(json.RawMessage(`{"verdict":"done","gist":"` + strings.Repeat("é", MaxGist) + `"}`))
	assert.NoError(t, err, "a gist of exactly MaxGist characters")
}

func TestNewRequest_CutsThePrompt(t *testing.T) {
	r := NewRequest("t", "owner", strings.Repeat("x", MaxPrompt+10), "msg", 2)
	assert.Equal(t, MaxPrompt+len("…"), len(r.LastPrompt))
	assert.True(t, strings.HasSuffix(r.LastPrompt, "…"))
	assert.Equal(t, Request{Title: "t", PromptFrom: "owner", LastPrompt: "short", LastMessage: "msg", JobsInFlight: 2},
		NewRequest("t", "owner", "short", "msg", 2))
}
