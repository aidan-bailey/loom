package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/transcript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const readsConv = "6f1e0c2a-2a4b-4c55-9d1e-0b6c2f3a4d5e"

// transcriptInst is a Paused Claude session whose launch named the
// transcript at path, as a restored record carries it.
func transcriptInst(t *testing.T, title, path string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude",
		ClaudeSessionID: readsConv, ClaudeTranscriptPath: path,
	}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// worktreeInst is a Running session on a worktree of a new repository,
// branched at its one commit, whose file gained a line since.
func worktreeInst(t *testing.T, title string) (*session.Instance, string) {
	t.Helper()
	repo := gitRepo(t)
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	base := strings.TrimSpace(string(out))
	tree := filepath.Join(t.TempDir(), title)
	runGit(t, repo, "worktree", "add", "-q", "-b", "b-"+title, tree)
	require.NoError(t, os.WriteFile(filepath.Join(tree, "f"), []byte("x\ntwo\n"), 0o644))
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Path: repo, Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: tree, SessionName: title, BranchName: "b-" + title, BaseCommitSHA: base},
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst, base
}

func TestReadTurns_RepliesWithAPageOfTheTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), readsConv+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(
		`{"type":"user","timestamp":"2026-10-23T12:00:00.000Z","promptSource":"typed","message":{"role":"user","content":"run it"}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-10-23T12:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}`+"\n"), 0o600))
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := transcriptInst(t, "paused", path)
	ws.add(inst)
	m.SetWorkspacesForTest(ws)

	m.ReadTurns(m.idOf(inst), transcript.Query{}, 7)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	assert.Equal(t, ReqID(7), rs[0].Req)
	assert.Equal(t, readsConv, rs[0].Turns.Conversation)
	require.Len(t, rs[0].Turns.Turns, 1)
	assert.Equal(t, "run it", rs[0].Turns.Turns[0].Text)
	assert.Equal(t, "Done.", rs[0].Turns.Turns[0].Items[0].Text)
}

func TestReadTurns_RefusesASessionWhoseTranscriptIsUnknown(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := statusInst(t, "fresh", session.Running)
	ws.add(inst)
	m.SetWorkspacesForTest(ws)

	m.ReadTurns(m.idOf(inst), transcript.Query{}, 3)
	out := m.Drain()
	assert.Empty(t, out.Jobs, "nothing to read")
	rs := replies(out)
	require.Len(t, rs, 1)
	assert.EqualError(t, rs[0].Err, "read the conversation of fresh: loom has not seen this session's transcript yet")
	assert.ErrorIs(t, rs[0].Err, ErrRefused)
}

func TestReadDiff_RepliesWithTheWorktreesPatch(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst, base := worktreeInst(t, "live")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)

	m.ReadDiff(m.idOf(inst), 4)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	assert.Equal(t, base, rs[0].Diff.Base)
	require.Len(t, rs[0].Diff.Files, 1)
	f := rs[0].Diff.Files[0]
	assert.Equal(t, "f", f.Path)
	assert.Equal(t, 2, f.Added)
	assert.Equal(t, 1, f.Removed)
	assert.Nil(t, inst.GetDiffStats(), "the read caches nothing the views publish")
}
