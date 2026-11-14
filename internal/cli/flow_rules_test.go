package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentRules(t *testing.T) {
	// The repo sits in a directory with rules of its own, which a path
	// that leaves the tree would reach.
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o700))
	write := func(rel, body string) {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	write("../CLAUDE.md", "outer rules\n")
	write("../x/CLAUDE.md", "outer x rules\n")
	gitIn(t, repo, "init", "-q")
	write("CLAUDE.md", "root rules\n")
	write("pkg/CLAUDE.md", "pkg rules\n")     // 10 bytes
	write("pkg/sub/CLAUDE.md", "sub rules\n") // 10 bytes
	write("other/CLAUDE.md", "other rules\n") // 12 bytes
	write("pkg/sub/a.go", "package sub\n")
	write("plain/b.go", "package plain\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	base := gitIn(t, repo, "rev-parse", "HEAD")
	write("other/c.go", "package other\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "change")
	head := gitIn(t, repo, "rev-parse", "HEAD")
	at := func(rel ...string) []string {
		var out []string
		for _, r := range rel {
			out = append(out, filepath.Join(repo, filepath.FromSlash(r)))
		}
		return out
	}

	t.Run("every package above the files, the root's left out, docs counted", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{Files: []string{"pkg/sub/a.go", "plain/b.go", "main.go", "pkg/README.md"}})
		require.NoError(t, err)
		assert.Equal(t, at("pkg", "pkg/sub"), dirs)
		assert.Equal(t, 20, size)
	})
	t.Run("a new file in a new directory gets the rules above it", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{Files: []string{"pkg/new/dir/x.go"}})
		require.NoError(t, err)
		assert.Equal(t, at("pkg"), dirs)
		assert.Equal(t, 10, size)
	})
	t.Run("the diff's files join the plan's", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{Files: []string{"pkg/sub/a.go"}, Base: base, Head: head})
		require.NoError(t, err)
		assert.Equal(t, at("other", "pkg", "pkg/sub"), dirs)
		assert.Equal(t, 32, size)
	})
	t.Run("a path that leaves the tree adds nothing", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{Files: []string{"../x/y.go", "/pkg/a.go", "pkg/../../w.go"}})
		require.NoError(t, err)
		assert.Empty(t, dirs)
		assert.Zero(t, size)
	})
	t.Run("no scope, no rules", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{})
		require.NoError(t, err)
		assert.Empty(t, dirs)
		assert.Zero(t, size)
	})
	t.Run("a diff git can't give is an error", func(t *testing.T) {
		_, _, err := agentRules(repo, flowrun.Scope{Base: "nope", Head: head})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "git diff nope..")
	})
}

// A diff runFX.Agent can't read fails the step before anything starts: an
// agent never runs without the rules its scope covers.
func TestRunFX_AnUnreadableDiffFailsTheAgentStep(t *testing.T) {
	_, fx := generatedRepo(t)
	fx.agents = t.TempDir()
	r, err := fx.Agent("", "r-s1-review1", "story-reviewer", "", "Review.", flowrun.Scope{Base: "nope", Head: "HEAD"})
	require.NoError(t, err)
	assert.Equal(t, agentFailed, r.Status)
	assert.Contains(t, r.Why, "its package rules: git diff nope..HEAD")
	_, statErr := os.Stat(agentPath(fx.agents, "r-s1-review1"))
	assert.True(t, errors.Is(statErr, fs.ErrNotExist), "nothing started")
}

// A resumed run passes the rules its record holds.
func TestAnswerArgv_KeepsTheRunsRules(t *testing.T) {
	agentFixture(t, "story-reviewer")
	root := os.Getenv(flow.EnvRoot)
	run := &agentRun{ID: "r-s1-review1", Role: "story-reviewer", Cwd: t.TempDir(), Session: "sess-1", Status: "NEEDS_CONTEXT",
		Rules: []string{"/w/pkg", "/w/pkg/sub"}}
	argv, err := answerArgv(run, root, "Blue.")
	require.NoError(t, err)
	assert.Contains(t, strings.Join(argv, "\n"), "\n--add-dir\n/w/pkg\n--add-dir\n/w/pkg/sub\n")
	run.Rules = nil
	argv, err = answerArgv(run, root, "Blue.")
	require.NoError(t, err)
	assert.NotContains(t, argv, "--add-dir")
}
