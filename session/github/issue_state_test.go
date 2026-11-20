package github

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueStateOf_ReadsTheStateAndTheReason(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want IssueState
	}{
		{"completed", `{"state":"CLOSED","stateReason":"COMPLETED"}`, IssueState{Closed: true, Reason: "completed"}},
		{"not planned", `{"state":"CLOSED","stateReason":"NOT_PLANNED"}`, IssueState{Closed: true, Reason: "not planned"}},
		{"duplicate", `{"state":"CLOSED","stateReason":"DUPLICATE"}`, IssueState{Closed: true, Reason: "duplicate"}},
		{"an unfamiliar reason", `{"state":"CLOSED","stateReason":"SOMETHING_NEW"}`, IssueState{Closed: true, Reason: "something new"}},
		{"closed with an empty reason", `{"state":"CLOSED","stateReason":""}`, IssueState{Closed: true}},
		{"closed with a null reason", `{"state":"CLOSED","stateReason":null}`, IssueState{Closed: true}},
		{"open", `{"state":"OPEN","stateReason":null}`, IssueState{}},
		{"open with a reason", `{"state":"OPEN","stateReason":"REOPENED"}`, IssueState{Reason: "reopened"}},
		{"open with a completed reason", `{"state":"OPEN","stateReason":"COMPLETED"}`, IssueState{Reason: "completed"}},
		{"a reason that is no word", `{"state":"CLOSED","stateReason":"bad\nreason"}`, IssueState{Closed: true}},
		{"a reason too long to be one", `{"state":"CLOSED","stateReason":"ABCDEFGHIJKLMNOPQRSTUVWXYZABCDEFGHIJKLMNOP"}`, IssueState{Closed: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ex := &watchExec{fn: answer(tc.out)}
			got, err := IssueStateOf(context.Background(), "/fb", "aidan-bailey/loom", 57, ex)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, []string{"gh issue view 57 -R aidan-bailey/loom --json state,stateReason"}, ex.calls)
			assert.Equal(t, []string{"/fb"}, ex.dirs, "gh runs in the directory it is given")
		})
	}
}

func TestIssueStateOf_AFailedOrUnreadableAnswerIsAnError(t *testing.T) {
	failing := &watchExec{fn: func(string) (string, error) { return "", errors.New("exit status 1") }}
	_, err := IssueStateOf(context.Background(), "/fb", "o/r", 5, failing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gh issue view")
	assert.Contains(t, err.Error(), "exit status 1")

	for name, out := range map[string]string{
		"not json":   `not json`,
		"no state":   `{}`,
		"null state": `{"state":null,"stateReason":"COMPLETED"}`,
	} {
		_, err := IssueStateOf(context.Background(), "/fb", "o/r", 5, &watchExec{fn: answer(out)})
		assert.Error(t, err, name)
	}
}

func TestIssueStateOf_RefusesWhatItCannotNameBeforeCallingGh(t *testing.T) {
	for name, tc := range map[string]struct {
		repo   string
		number int
	}{
		"no repository":   {"", 5},
		"no owner":        {"loom", 5},
		"a flag as owner": {"-x/loom", 5},
		"number zero":     {"o/r", 0},
		"a negative":      {"o/r", -3},
	} {
		ex := &watchExec{fn: answer(`{"state":"OPEN"}`)}
		_, err := IssueStateOf(context.Background(), "/fb", tc.repo, tc.number, ex)
		assert.Error(t, err, name)
		assert.Empty(t, ex.calls, name)
	}
}

func TestIssueStateOf_TheCallersContextEndsTheCall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the child is /bin/sh and sleep, which Windows lacks")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := IssueStateOf(ctx, t.TempDir(), "o/r", 5, sleepExec{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 3*time.Second, "the poll's budget ends a call; it does not wait for ghTimeout")
}
