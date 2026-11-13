package watch

import (
	"encoding/json"
	"errors"
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
