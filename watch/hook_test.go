package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookTestInput is an input for issue 96 that already knows one session and
// one open PR of the watcher's. Point is left empty: the runner sets it.
func hookTestInput() HookInput {
	return HookInput{
		Workspace: "polyoxide",
		Repo:      "owner/name",
		Base:      "main",
		BaseSHA:   "abc123",
		Issue: github.WatchIssue{
			Number: 96,
			Title:  "drift in kalshi-ws",
			Body:   "the body",
			URL:    "https://github.com/owner/name/issues/96",
			Labels: []string{"schema-drift", "spec:kalshi-ws"},
		},
		Sessions: []HookSession{{
			Title:  "heal kalshi 94 R32",
			Issue:  94,
			Branch: "aidanb/heal-kalshi-94-r32",
			Status: "Running",
			Vars:   map[string]string{"spec": "kalshi", "row": "R32"},
		}},
		PRs: []HookPR{{Number: 101, Issue: 94, Branch: "aidanb/heal-kalshi-94-r32", Body: "Closes #94"}},
	}
}

func TestHookInput_MarshalJSON_Start(t *testing.T) {
	in := hookTestInput()
	in.Point = "start"
	// The closed flag and the comments are the watcher's, not the hook's.
	in.Issue.Closed = true
	in.Issue.Comments = []github.IssueComment{{
		ID: "IC_1", Author: "someone", Association: "OWNER", Body: "a comment",
		CreatedAt: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC),
	}}

	got, err := json.Marshal(in)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"point": "start", "workspace": "polyoxide", "repo": "owner/name", "base": "main", "base_sha": "abc123",
		"issue": {"number": 96, "title": "drift in kalshi-ws", "labels": ["schema-drift", "spec:kalshi-ws"],
		          "body": "the body", "url": "https://github.com/owner/name/issues/96"},
		"pr": null,
		"sessions": [{"title": "heal kalshi 94 R32", "issue": 94, "branch": "aidanb/heal-kalshi-94-r32",
		              "status": "Running", "vars": {"spec": "kalshi", "row": "R32"}}],
		"prs": [{"number": 101, "issue": 94, "branch": "aidanb/heal-kalshi-94-r32", "body": "Closes #94"}]
	}`, string(got))
}

func TestHookInput_MarshalJSON_EmptyCollections(t *testing.T) {
	got, err := json.Marshal(HookInput{Point: "start", Issue: github.WatchIssue{Number: 1}})
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"point": "start", "workspace": "", "repo": "", "base": "", "base_sha": "",
		"issue": {"number": 1, "title": "", "labels": [], "body": "", "url": ""},
		"pr": null, "sessions": [], "prs": []
	}`, string(got), "nil slices are sent as [], and pr as null")
}

func TestHookInput_MarshalJSON_Nudges(t *testing.T) {
	in := hookTestInput()
	in.Point = "nudges"
	in.Sessions = nil
	in.PRs = nil
	in.PR = &HookPR{Number: 101, Issue: 96, Branch: "aidanb/heal-kalshi-ws-96-r33", Body: "Closes #96"}
	in.Session = &HookSession{Title: "heal kalshi-ws 96 R33", Issue: 96, Branch: "aidanb/heal-kalshi-ws-96-r33", Status: "Ready"}
	in.HeadSHA = "deadbeef"

	got, err := json.Marshal(in)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"point": "nudges", "workspace": "polyoxide", "repo": "owner/name", "base": "main", "base_sha": "abc123",
		"issue": {"number": 96, "title": "drift in kalshi-ws", "labels": ["schema-drift", "spec:kalshi-ws"],
		          "body": "the body", "url": "https://github.com/owner/name/issues/96"},
		"pr": {"number": 101, "issue": 96, "branch": "aidanb/heal-kalshi-ws-96-r33", "body": "Closes #96"},
		"sessions": [], "prs": [],
		"session": {"title": "heal kalshi-ws 96 R33", "issue": 96, "branch": "aidanb/heal-kalshi-ws-96-r33",
		            "status": "Ready", "vars": {}},
		"head_sha": "deadbeef"
	}`, string(got), "a nil vars is sent as {}, and session and head_sha are sent when set")
}

func TestHookError(t *testing.T) {
	cause := errors.New("exit status 3")
	he := &HookError{Point: "start", Err: cause, Stderr: "first\nboom: it crashed\n\n"}

	assert.Equal(t, "hook start: exit status 3; stderr: boom: it crashed", he.Error(),
		"the last non-blank stderr line rides along")
	assert.ErrorIs(t, he, cause)
	assert.Equal(t, "hook nudges: timed out",
		(&HookError{Point: "nudges", Err: errors.New("timed out")}).Error(), "no stderr, no suffix")
}

// hookVarsJSON returns a JSON object of n variables, v0 to v<n-1>.
func hookVarsJSON(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf(`"v%d": "x"`, i)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func hookNudgeJSON(kind, key, text string) string {
	return fmt.Sprintf(`{"kind": %q, "key": %q, "text": %q}`, kind, key, text)
}

func hookNudgesJSON(items ...string) string {
	return `{"nudges": [` + strings.Join(items, ", ") + `]}`
}

func TestDecodeHookStart_Accepts(t *testing.T) {
	ans, err := decodeHookStart([]byte("{\"start\": true, \"vars\": {\"spec\": \"kalshi-ws\", \"row\": \"R33\"}}\n \n"))
	require.NoError(t, err)
	assert.Equal(t, StartAnswer{Start: true, Vars: map[string]string{"spec": "kalshi-ws", "row": "R33"}}, ans,
		"trailing whitespace is not data")

	ans, err = decodeHookStart([]byte(`{"start": true}`))
	require.NoError(t, err)
	assert.Equal(t, StartAnswer{Start: true}, ans, "variables are optional")

	ans, err = decodeHookStart([]byte(`{"start": true, "vars": {"issue_row": "1", "pr_kind": "2", "base2": "3"}}`))
	require.NoError(t, err)
	assert.Len(t, ans.Vars, 3, "a name that merely contains a builtin's is not one")
}

func TestDecodeHookStart_Limits(t *testing.T) {
	out := `{"start": true, "reason": "` + strings.Repeat("r", 1024) + `", "vars": ` + hookVarsJSON(16) + `}`
	ans, err := decodeHookStart([]byte(out))
	require.NoError(t, err)
	assert.Len(t, ans.Vars, 16, "16 variables are the most")
	assert.Len(t, ans.Reason, 1024, "a 1 KiB reason is the longest")

	out = `{"start": true, "vars": {"spec": "` + strings.Repeat("v", 64) + `"}}`
	ans, err = decodeHookStart([]byte(out))
	require.NoError(t, err)
	assert.Len(t, ans.Vars["spec"], 64, "a 64-byte value is the longest")
}

func TestDecodeHookStart_Refusal(t *testing.T) {
	ans, err := decodeHookStart([]byte(`{"start": false, "reason": "no free row", "vars": ` + hookVarsJSON(17) + `}`))
	require.NoError(t, err)
	assert.Equal(t, StartAnswer{Reason: "no free row"}, ans, "a refusal keeps its reason and drops its variables unchecked")

	ans, err = decodeHookStart([]byte(`{"start": false}`))
	require.NoError(t, err)
	assert.Equal(t, StartAnswer{}, ans, "a refusal may have no reason")
}

func TestDecodeHookStart_Rejects(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"empty stdout", ``, "not the expected JSON object"},
		{"not JSON", `this is not json`, "not the expected JSON object"},
		{"an array", `[]`, "not the expected JSON object"},
		{"null", `null`, `lacks "start"`},
		{"no start key", `{"vars": {"spec": "kalshi"}}`, `lacks "start"`},
		{"a key outside the shape", `{"start": true, "extra": 1}`, `unknown field "extra"`},
		{"a second object", `{"start": true}{"start": false}`, "data after the JSON object"},
		{"trailing text", `{"start": true} x`, "data after the JSON object"},
		{"a variable that is not a string", `{"start": true, "vars": {"spec": 1}}`, "not the expected JSON object"},
		{"an upper-case variable name", `{"start": true, "vars": {"Spec": "x"}}`, `name "Spec"`},
		{"a variable name starting with a digit", `{"start": true, "vars": {"1row": "x"}}`, `name "1row"`},
		{"a dashed variable name", `{"start": true, "vars": {"a-b": "x"}}`, `name "a-b"`},
		{"17 variables", `{"start": true, "vars": ` + hookVarsJSON(17) + `}`, "17 entries"},
		{"a 65-byte value", `{"start": true, "vars": {"spec": "` + strings.Repeat("v", 65) + `"}}`, `value of "spec" is 65 bytes`},
		{"a reason over 1 KiB", `{"start": false, "reason": "` + strings.Repeat("r", 1025) + `"}`, "reason is 1025 bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ans, err := decodeHookStart([]byte(tc.out))

			assert.ErrorContains(t, err, tc.want)
			assert.Equal(t, StartAnswer{}, ans)
		})
	}
}

func TestDecodeHookStart_RejectsEveryPromptBuiltin(t *testing.T) {
	for _, name := range []string{"issue", "issue_title", "issue_url", "pr", "pr_url", "branch", "base"} {
		_, err := decodeHookStart([]byte(fmt.Sprintf(`{"start": true, "vars": {%q: "x"}}`, name)))

		assert.ErrorContains(t, err, "is a prompt variable", name)
	}
}

func TestDecodeHookNudges_Accepts(t *testing.T) {
	got, err := decodeHookNudges([]byte(hookNudgesJSON(
		hookNudgeJSON("redrift", "k1", "fix it"),
		hookNudgeJSON("ci-fail", "k2", "and this"),
	)))
	require.NoError(t, err)
	assert.Equal(t, []Nudge{
		{Kind: "redrift", Key: "k1", Text: "fix it"},
		{Kind: "ci-fail", Key: "k2", Text: "and this"},
	}, got, "in the order the hook returned them")

	got, err = decodeHookNudges([]byte(`{"nudges": []}`))
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got, "an empty list is an answer: no nudge")
}

func TestDecodeHookNudges_Limits(t *testing.T) {
	items := make([]string, 8)
	for i := range items {
		items[i] = hookNudgeJSON("redrift", fmt.Sprintf("k%d", i), "t")
	}
	got, err := decodeHookNudges([]byte(hookNudgesJSON(items...)))
	require.NoError(t, err)
	assert.Len(t, got, 8, "8 nudges are the most")

	got, err = decodeHookNudges([]byte(hookNudgesJSON(
		hookNudgeJSON("a", strings.Repeat("k", 256), strings.Repeat("t", 16<<10)))))
	require.NoError(t, err)
	assert.Len(t, got[0].Key, 256, "a 256-byte key is the longest")
	assert.Len(t, got[0].Text, 16<<10, "a 16 KiB text is the longest")
}

func TestDecodeHookNudges_Rejects(t *testing.T) {
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = hookNudgeJSON("redrift", fmt.Sprintf("k%d", i), "t")
	}
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"empty stdout", ``, "not the expected JSON object"},
		{"no nudges key", `{}`, `lacks "nudges"`},
		{"a null list", `{"nudges": null}`, `lacks "nudges"`},
		{"a key outside the shape", `{"nudges": [], "extra": 1}`, `unknown field "extra"`},
		{"a nudge key outside the shape", hookNudgesJSON(`{"kind": "redrift", "key": "k", "text": "t", "extra": 1}`), `unknown field "extra"`},
		{"a second object", `{"nudges": []}{"nudges": []}`, "data after the JSON object"},
		{"a nudge that is not an object", hookNudgesJSON(`"redrift"`), "not the expected JSON object"},
		{"a null nudge", hookNudgesJSON(`null`), `nudges[0]: kind ""`},
		{"an empty kind", hookNudgesJSON(hookNudgeJSON("", "k", "t")), `nudges[0]: kind ""`},
		{"an upper-case kind", hookNudgesJSON(hookNudgeJSON("Redrift", "k", "t")), `kind "Redrift"`},
		{"a kind starting with a digit", hookNudgesJSON(hookNudgeJSON("1x", "k", "t")), `kind "1x"`},
		{"a kind with an underscore", hookNudgesJSON(hookNudgeJSON("re_drift", "k", "t")), `kind "re_drift"`},
		{"the reserved kind conflict", hookNudgesJSON(hookNudgeJSON("conflict", "k", "t")), `kind "conflict" is reserved`},
		{"the reserved kind ci", hookNudgesJSON(hookNudgeJSON("ci", "k", "t")), `kind "ci" is reserved`},
		{"the reserved kind issue-changed", hookNudgesJSON(hookNudgeJSON("issue-changed", "k", "t")), `kind "issue-changed" is reserved`},
		{"an empty key", hookNudgesJSON(hookNudgeJSON("redrift", "", "t")), "key is empty"},
		{"a 257-byte key", hookNudgesJSON(hookNudgeJSON("redrift", strings.Repeat("k", 257), "t")), "key is 257 bytes"},
		{"an empty text", hookNudgesJSON(hookNudgeJSON("redrift", "k", "")), "text is empty"},
		{"a text over 16 KiB", hookNudgesJSON(hookNudgeJSON("redrift", "k", strings.Repeat("t", 16<<10+1))), "text is 16385 bytes"},
		{"nine nudges", hookNudgesJSON(nine...), "9 nudges"},
		{"a bad nudge after a good one", hookNudgesJSON(hookNudgeJSON("redrift", "k", "t"), hookNudgeJSON("ci", "k", "t")), `nudges[1]: kind "ci" is reserved`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeHookNudges([]byte(tc.out))

			assert.ErrorContains(t, err, tc.want)
			assert.Nil(t, got, "one bad nudge fails the whole answer")
		})
	}
}
