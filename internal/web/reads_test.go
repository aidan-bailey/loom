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
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/aidan-bailey/loom/session/transcript"
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
				 "steps": [{"tool": "Bash", "summary": "gh pr create", "at": "2026-10-23T12:20:05Z", "state": "pending", "finished": false}]},
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

func TestTurnsOf_MapsThinkingStepsAndTheAgentPage(t *testing.T) {
	at := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	page := transcript.Page{
		Conversation: webConv,
		Turns: []transcript.Turn{{
			At: at, From: transcript.FromBrief, Text: "Map the tree.",
			Items: []transcript.Item{
				{Kind: transcript.KindThinking, At: at, Text: "Still in design.", Cut: true},
				{Kind: transcript.KindThinking, At: at, Text: "Next."},
				{Kind: transcript.KindSteps, At: at, Total: 2, Steps: []transcript.Step{
					{ID: "t1", Tool: "Agent", Summary: "map", At: at, State: transcript.StateDone, Agent: "a1f00dcafe0001", Finished: true},
					{ID: "t2", Tool: "Bash", At: at, State: transcript.StatePending},
				}},
			},
		}},
		Agent: &transcript.AgentInfo{
			ID: "a1f00dcafe0001", Type: "Explore", Description: "map", Teammate: true,
			State: transcript.AgentRunning, Started: at, Missing: true,
		},
	}

	got, err := json.Marshal(turnsOf(page))
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"conversation": "`+webConv+`",
		"turns": [{
			"id": 0, "at": "2026-11-06T09:00:00Z", "from": "brief", "name": "", "text": "Map the tree.",
			"cut": false, "command": false, "partial": false,
			"items": [
				{"kind": "thinking", "at": "2026-11-06T09:00:00Z", "text": "Still in design.", "truncated": true},
				{"kind": "thinking", "at": "2026-11-06T09:00:00Z", "text": "Next."},
				{"kind": "steps", "at": "2026-11-06T09:00:00Z", "total": 2, "steps": [
					{"tool": "Agent", "summary": "map", "at": "2026-11-06T09:00:00Z", "state": "done", "agent": "a1f00dcafe0001", "finished": true},
					{"tool": "Bash", "summary": "", "at": "2026-11-06T09:00:00Z", "state": "pending", "finished": false}
				]}
			]
		}],
		"earlier": null, "reset": false, "skipped": 0,
		"agent": {"id": "a1f00dcafe0001", "type": "Explore", "description": "map", "teammate": true,
			"state": "running", "started": "2026-11-06T09:00:00Z", "missing": true}
	}`, string(got), "a thinking item is text and truncated; a step names its agent only when it has one")

	page.Agent = nil
	got, err = json.Marshal(turnsOf(page))
	require.NoError(t, err)
	assert.NotContains(t, string(got), `"agent":{`, "a conversation page has no agent object")
}

// agentFixture writes a subagent's own transcript and meta file beside the
// session's transcript, as Claude lays them out.
func agentFixture(t *testing.T, inst *session.Instance, agentID, meta string, lines ...string) {
	t.Helper()
	_, path := inst.ClaudeSession()
	dir := subagent.SubagentsDir(path)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	if meta != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-"+agentID+".meta.json"), []byte(meta), 0o600))
	}
	if len(lines) > 0 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-"+agentID+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	}
}

func TestAPI_TurnsLinkAnAgentStepToItsSubagentAndKeepThinkingText(t *testing.T) {
	inst := apiWithTranscript(t, "talk",
		`{"type":"user","timestamp":"2026-11-06T09:00:00.000Z","promptSource":"typed","message":{"role":"user","content":"survey the tree"}}`,
		`{"type":"assistant","timestamp":"2026-11-06T09:00:02.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"thinking","thinking":"Still in design: items 1 and 2 are worked out.","signature":"sig"},{"type":"redacted_thinking","data":"x"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-06T09:00:05.000Z","cwd":"/w","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_agent1","name":"Agent","input":{"description":"map the tree","subagent_type":"Explore","prompt":"Map the tree."}}]}}`,
		`{"type":"assistant","timestamp":"2026-11-06T09:00:06.000Z","cwd":"/w","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_agent2","name":"Task","input":{"description":"later survey","prompt":"Survey."}}]}}`,
		`{"type":"user","timestamp":"2026-11-06T09:00:40.000Z","toolUseResult":{"agentId":"a1f00dcafe0001","status":"completed"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_agent1","content":"done"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-06T09:00:45.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Mapped."}]}}`,
	)
	agentFixture(t, inst, "a1f00dcafe0001",
		`{"agentType":"Explore","description":"map the tree","toolUseId":"toolu_agent1","finished":true}`)
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})

	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"]))
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)

	var page turnsJSON
	require.NoError(t, json.Unmarshal(got.body, &page))
	require.Len(t, page.Turns, 1)
	assert.Nil(t, page.Agent, "a conversation page has no agent object")
	var thinking []itemJSON
	steps := map[string]stepJSON{}
	for _, it := range page.Turns[0].Items {
		switch it.Kind {
		case transcript.KindThinking:
			thinking = append(thinking, it)
		case transcript.KindSteps:
			for _, s := range it.Steps {
				steps[s.Summary] = s
			}
		}
	}
	require.Len(t, thinking, 1, "a blank and a redacted thinking block add nothing")
	assert.Equal(t, "Still in design: items 1 and 2 are worked out.", thinking[0].Text)
	assert.False(t, thinking[0].Truncated)
	assert.Empty(t, thinking[0].Blocks, "thinking crosses as text, not as a tree")

	linked := steps["map the tree"]
	assert.Equal(t, "Agent", linked.Tool)
	assert.Equal(t, "a1f00dcafe0001", linked.Agent)
	assert.True(t, linked.Finished, "meta says finished")
	unlinked := steps["later survey"]
	assert.Equal(t, "Task", unlinked.Tool)
	assert.Empty(t, unlinked.Agent, "no meta and no result: no link is guessed")
	assert.False(t, unlinked.Finished)

	assert.Contains(t, string(got.body), `"agent":"a1f00dcafe0001"`)
	assert.Contains(t, string(got.body), `"finished":true`)
	assert.NotContains(t, string(got.body), `"agent":""`, "an unlinked step leaves the key out")
}

func TestAPI_TurnsOfASubagentAreItsOwnWithItsAgentPage(t *testing.T) {
	inst := apiWithTranscript(t, "talk", talkLines...)
	agentFixture(t, inst, "a1f00dcafe0001",
		`{"agentType":"Explore","description":"map the tree","toolUseId":"toolu_agent1","finished":true}`,
		`{"type":"user","isSidechain":true,"agentId":"a1f00dcafe0001","timestamp":"2026-11-06T09:00:01.000Z","message":{"role":"user","content":"Map the tree and report."}}`,
		`{"type":"assistant","isSidechain":true,"agentId":"a1f00dcafe0001","timestamp":"2026-11-06T09:00:04.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Mapped."}]}}`,
	)
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})
	base := fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"])

	got := env.readAnswer(base + "?agent=a1f00dcafe0001")
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	var page turnsJSON
	require.NoError(t, json.Unmarshal(got.body, &page))
	require.NotNil(t, page.Agent)
	assert.Equal(t, agentPageJSON{
		ID: "a1f00dcafe0001", Type: "Explore", Description: "map the tree",
		State: transcript.AgentFinished, Started: "2026-11-06T09:00:01Z",
	}, *page.Agent)
	require.Len(t, page.Turns, 1)
	assert.Equal(t, transcript.FromBrief, page.Turns[0].From)
	assert.Equal(t, "Map the tree and report.", page.Turns[0].Text)
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "turns", session: env.ids["talk"], result: "200 ok"})

	gone := env.readAnswer(base + "?agent=a2e2e2e2e2e2")
	require.Equal(t, http.StatusOK, gone.status, "an agent with no file is a page, not an error; body: %s", gone.body)
	var empty turnsJSON
	require.NoError(t, json.Unmarshal(gone.body, &empty))
	require.NotNil(t, empty.Agent)
	assert.True(t, empty.Agent.Missing)
	assert.Equal(t, "a2e2e2e2e2e2", empty.Agent.ID)
	assert.Equal(t, transcript.AgentUnknown, empty.Agent.State, "no meta file, no state")
	assert.Empty(t, empty.Agent.Started)
	assert.Empty(t, empty.Turns)

	main := env.readAnswer(base + "?agent=")
	require.Equal(t, http.StatusOK, main.status, "body: %s", main.body)
	var conv turnsJSON
	require.NoError(t, json.Unmarshal(main.body, &conv))
	assert.Nil(t, conv.Agent, "an empty agent reads the conversation")
	require.Len(t, conv.Turns, 1)
	assert.Equal(t, "open the PR", conv.Turns[0].Text)
}

func TestAPI_TurnsRefusesABadAgentID(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiWithTranscript(t, "talk", talkLines...)}})
	base := fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"])

	for _, c := range []struct{ query, id string }{
		{"?agent=..", ".."},
		{"?agent=.", "."},
		{"?agent=a%2Fb", "a/b"},
		{"?agent=a%5Cb", `a\b`},
		{"?agent=..%2F..%2Fx", "../../x"},
	} {
		status, out := env.do(http.MethodGet, base+c.query, nil)
		assert.Equal(t, http.StatusBadRequest, status, c.query)
		assert.Equal(t, fmt.Sprintf("agent %q: not an agent id", c.id), apiErrorOf(t, out), c.query)
	}
	assert.Empty(t, env.loop.JobsForTest(), "a refused query reaches no model")
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
		fmt.Sprintf("/api/v1/sessions/%d/mockup", env.ids["term"]),
		"/api/v1/sessions/999999/turns",
		"/api/v1/sessions/999999/diff",
		"/api/v1/sessions/999999/mockup",
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
