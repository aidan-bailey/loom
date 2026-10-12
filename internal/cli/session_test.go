package cli

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSession runs `loom session args…` with stdin, on a fresh command tree
// (newSessionCmd), so no flag carries over from another test.
func runSession(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newSessionCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// pausedRecords are two Paused sessions, alpha and beta, as state.json
// holds them.
const pausedRecords = `[{"title":"alpha","status":3,"program":"true","branch":"me/alpha","worktree":{"worktree_path":"/tmp/wt-alpha"}},` +
	`{"title":"beta","status":3,"program":"true","worktree":{"worktree_path":"/tmp/wt-beta"}}]`

func TestSessionList_ShowsTheWorkspacesSessions(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	registered(t, "ws-b", `[{"title":"other","status":3,"program":"true"}]`)
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a")

	require.NoError(t, err)
	assert.Regexp(t, `TITLE\s+STATUS\s+BRANCH\s+ACCOUNT\s+SINCE`, out)
	assert.Regexp(t, `alpha\s+Paused\s+me/alpha`, out)
	assert.Regexp(t, `beta\s+Paused\s+-`, out)
	assert.NotContains(t, out, "other", "another workspace's session")
}

func TestSessionList_TheWorkingDirectoryPicksTheWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	ws := registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")
	t.Chdir(ws.Path)

	out, _, err := runSession(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "alpha")
}

func TestSessionList_RefusesADirectoryNoWorkspaceHolds(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")
	t.Chdir(t.TempDir())

	_, _, err := runSession(t, "", "list")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workspace matches current directory")
}

func TestSessionList_RefusesAnUnknownWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	serveDaemon(t, "true")

	_, _, err := runSession(t, "", "list", "-w", "nope")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `workspace "nope" not found`)
}

func TestSessionList_SaysWhenThereAreNone(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, "No sessions in ws-a")
}

// The JSON's field names are a contract (scrum's board reads them).
func TestSessionList_JSONFieldNames(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a", "--json")

	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &rows))
	require.Len(t, rows, 2)
	var keys []string
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{"account", "branch", "id", "issue", "pr", "program", "repo", "status", "status_since",
		"title", "wait_reason", "worktree"}, keys)
	assert.Equal(t, "alpha", rows[0]["title"])
	assert.Equal(t, "Paused", rows[0]["status"])
	assert.Equal(t, "me/alpha", rows[0]["branch"])
	assert.NotZero(t, rows[0]["id"])
}

func TestSessionList_JSONOfNoSessionsIsAnEmptyArray(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a", "--json")

	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestFindSession(t *testing.T) {
	views := []core.InstanceView{
		{ID: 1, Title: "ws-a", IsWorkspaceTerminal: true},
		{ID: 2, Title: "alpha"},
		{ID: 3, Title: "twin"},
		{ID: 4, Title: "twin"},
	}
	for _, tc := range []struct {
		name   string
		title  string
		id     core.InstanceID
		want   core.InstanceID
		errHas string
	}{
		{"an exact title", "alpha", 0, 2, ""},
		{"never a prefix", "alp", 0, 0, `no session "alp"`},
		{"never the workspace terminal", "ws-a", 0, 0, `no session "ws-a"`},
		{"a shared title is refused", "twin", 0, 0, "2 sessions are titled \"twin\": name one with --id"},
		{"--id picks among them", "twin", 4, 4, ""},
		{"--id must match the title", "alpha", 4, 0, `no session "alpha" with id 4`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findSession(views, tc.title, tc.id)
			if tc.errHas != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errHas)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.ID)
		})
	}
}

func TestSince(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "-", since(time.Time{}, now))
	assert.Equal(t, "<1m", since(now.Add(-30*time.Second), now))
	assert.Equal(t, "5m", since(now.Add(-5*time.Minute), now))
	assert.Equal(t, "3h", since(now.Add(-3*time.Hour), now))
	assert.Equal(t, "2d", since(now.Add(-50*time.Hour), now))
}
