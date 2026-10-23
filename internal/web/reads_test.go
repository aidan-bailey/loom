package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webConv = "0c6f9a52-3b1d-4e7a-9f20-6d1b8e4c2a11"

// apiWithTranscript is a fixture Claude session whose launch named a
// transcript of lines, as a restored record carries it.
func apiWithTranscript(t *testing.T, title string, lines ...string) *session.Instance {
	t.Helper()
	path := filepath.Join(t.TempDir(), webConv+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude",
		ClaudeSessionID: webConv, ClaudeTranscriptPath: path,
	}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// apiWithWorktree is a fixture Running session on a new repository whose
// file gained a line since the base commit; it returns the base's SHA.
func apiWithWorktree(t *testing.T, title string) (*session.Instance, string) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "notes.md"), []byte("one\n"), 0o644))
	git("add", ".")
	git("commit", "-qm", "init")
	base := git("rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "notes.md"), []byte("one\ntwo\n"), 0o644))
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Path: repo, Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: repo, SessionName: title, BranchName: "main", BaseCommitSHA: base},
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst, base
}

// readAnswer makes a read whose job the loop holds, runs the job as the
// loop would, and returns the handler's answer.
func (e *apiEnv) readAnswer(path string) apiAnswer {
	e.t.Helper()
	ch := e.async(http.MethodGet, path, nil)
	job := e.heldJob()
	e.finish(job())
	return e.answer(ch)
}

// tracked reports whether the layer still tracks request req.
func (e *apiEnv) tracked(req core.ReqID) bool {
	e.layer.reqs.mu.Lock()
	defer e.layer.reqs.mu.Unlock()
	_, ok := e.layer.reqs.entries[req]
	return ok
}

var talkLines = []string{
	`{"type":"user","timestamp":"2026-10-23T12:20:00.000Z","promptSource":"typed","message":{"role":"user","content":"open the PR"}}`,
	`{"type":"assistant","timestamp":"2026-10-23T12:20:05.000Z","cwd":"/w","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"gh pr create"}}]}}`,
	`{"type":"assistant","timestamp":"2026-10-23T12:20:09.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Opened **#318**."}]}}`,
}

func TestAPI_TurnsIsAPageOfTheConversation(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiWithTranscript(t, "talk", talkLines...)}})

	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"]))
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	assert.JSONEq(t, `{
		"conversation": "`+webConv+`",
		"turns": [{
			"id": 0, "at": "2026-10-23T12:20:00Z", "from": "you", "name": "", "text": "open the PR",
			"cut": false, "command": false, "partial": false,
			"items": [
				{"kind": "steps", "at": "2026-10-23T12:20:05Z", "total": 1,
				 "steps": [{"tool": "Bash", "summary": "gh pr create", "at": "2026-10-23T12:20:05Z", "state": "pending"}]},
				{"kind": "text", "at": "2026-10-23T12:20:09Z",
				 "blocks": [{"t": "p", "c": [{"t": "text", "v": "Opened "}, {"t": "strong", "c": [{"t": "text", "v": "#318"}]}, {"t": "text", "v": "."}]}]}
			]
		}],
		"earlier": null, "reset": false, "skipped": 0
	}`, string(got.body), "a text item crosses as its tree, never as its text")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "turns", session: env.ids["talk"], result: "200 ok"},
		"logged without its content")
	assert.False(t, env.tracked(env.lastReq()), "an answered read holds no Reply")
}

func TestAPI_TurnsTakesItsCursorsAndRefusesBadOnes(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiWithTranscript(t, "talk", talkLines...)}})
	base := fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"])

	for _, q := range []string{"?before=-1", "?before=x", "?after=1.5", "?before=10&after=20"} {
		status, out := env.do(http.MethodGet, base+q, nil)
		assert.Equal(t, http.StatusBadRequest, status, q)
		assert.NotEmpty(t, apiErrorOf(t, out), q)
	}
	assert.Empty(t, env.loop.JobsForTest(), "a refused query reaches no model")

	got := env.readAnswer(base + "?conversation=an-earlier-one")
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	var page turnsJSON
	require.NoError(t, json.Unmarshal(got.body, &page))
	assert.True(t, page.Reset, "another conversation's turns are replaced")
	assert.Len(t, page.Turns, 1)
}

func TestAPI_DiffIsTheWorktreesPatch(t *testing.T) {
	inst, base := apiWithWorktree(t, "live")
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})

	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/diff", env.ids["live"]))
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	assert.JSONEq(t, `{"base": "`+base+`", "files": [{
		"path": "notes.md", "old_path": "", "status": "M", "binary": false, "added": 1, "removed": 0, "omitted": "",
		"hunks": [{"header": "@@ -1 +1,2 @@", "lines": [" one", "+two"], "cut": []}]
	}]}`, string(got.body))
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "diff", session: env.ids["live"], result: "200 ok"})
}

func TestAPI_AReadTheModelRefusesIs409WithItsText(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "fresh", session.Ready), apiInstance(t, "resting", session.Paused)}})

	status, out := env.do(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["fresh"]), nil)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "read the conversation of fresh: loom has not seen this session's transcript yet", apiErrorOf(t, out))

	status, out = env.do(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/diff", env.ids["resting"]), nil)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "read the diff of resting: the session is not running (Paused)", apiErrorOf(t, out))
}

func TestAPI_ReadsOfAWorkspaceTerminalOrAnUnknownSessionAre404(t *testing.T) {
	term, err := session.NewInstance(session.InstanceOptions{Title: "term", Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{term}})
	for _, path := range []string{
		fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["term"]),
		fmt.Sprintf("/api/v1/sessions/%d/diff", env.ids["term"]),
		"/api/v1/sessions/999999/turns",
		"/api/v1/sessions/999999/diff",
	} {
		status, out := env.do(http.MethodGet, path, nil)
		assert.Equal(t, http.StatusNotFound, status, path)
		assert.Equal(t, "no such session", apiErrorOf(t, out), path)
	}
}

func TestAPI_AReadIsNotTrackedOnceAnsweredAndATimedOutOneIs504(t *testing.T) {
	inst, _ := apiWithWorktree(t, "live")
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})
	path := fmt.Sprintf("/api/v1/sessions/%d/diff", env.ids["live"])

	got := env.readAnswer(path)
	require.Equal(t, http.StatusOK, got.status)
	assert.False(t, env.tracked(env.lastReq()), "an answered read holds no Reply")

	late := env.lateRecorder()
	env.layer.reqs.timeout = 100 * time.Millisecond
	ch := env.async(http.MethodGet, path, nil)
	job := env.heldJob()
	slow := env.answer(ch)
	assert.Equal(t, http.StatusGatewayTimeout, slow.status)
	assert.Equal(t, "loom did not answer in time", apiErrorOf(t, slow.body))
	assert.False(t, env.tracked(env.lastReq()))

	env.finish(job())
	select {
	case n := <-late:
		t.Fatalf("a read's late Reply reached the stream: %+v", n)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSessionOf_ClaudeSaysTheSessionRunsClaude(t *testing.T) {
	assert.True(t, sessionOf(1, core.InstanceView{Program: "claude"}).Claude)
	assert.True(t, sessionOf(1, core.InstanceView{Program: "claude --model opus"}).Claude)
	assert.False(t, sessionOf(1, core.InstanceView{Program: "aider"}).Claude)
}
