package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generatedRun is runFixture for one story whose developer changes a
// generator's source, src.txt, and commits only that, with a story gate
// that rewrites the generated gen.txt from it and fails when the committed
// copy was stale (prader's export_bindings beside bindings_are_current,
// feedback thread 22). generated is the supervisor block's Generated list.
func generatedRun(t *testing.T, generated string) string {
	t.Helper()
	_, state, _ := runFixture(t, runStory("5", "intermediate-dev"))
	newRunTools = func(string) runTools {
		return fakeRunTools{stories: []flowrun.Story{runStory("5", "intermediate-dev")}, generated: generated}
	}
	script := strings.Replace(fakeRunClaude, `*"Build Story"*) commit dev;`, `*"Build Story"*) echo two > src.txt; git add src.txt; commit dev;`, 1)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(firstOnPath(t, "claude")), "claude"), []byte(script), 0o700))
	require.NoError(t, os.WriteFile("src.txt", []byte("one\n"), 0o600))
	require.NoError(t, os.WriteFile("gen.txt", []byte("one\n"), 0o600))
	gitIn(t, ".", "config", "user.name", "t")
	gitIn(t, ".", "config", "user.email", "t@t")
	gitIn(t, ".", "add", "src.txt", "gen.txt")
	gitIn(t, ".", "commit", "-q", "-m", "a generated file")
	c, _, err := newFlowConf()
	require.NoError(t, err)
	c.(*fakeConf).gates = []string{`RUN sh -c 'cp src.txt gen.new; if cmp -s gen.new gen.txt; then rm gen.new; else mv gen.new gen.txt; exit 1; fi'`}
	return state
}

// The gate's rewrite of a generated file is committed by the engine and
// the gates run again, so no fixer is sent and nothing parks as tree-moved.
func TestFlowRun_AGateThatRewritesOnlyGeneratedFilesGetsThemCommitted(t *testing.T) {
	state := generatedRun(t, "`gen.txt`, `*.gen.ts`")
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)
	out, _, err := runRun(t, "", "wait", "plan")
	require.NoError(t, err, out)
	assert.Contains(t, out, "ended plan: done", "no tree-moved question")

	assert.Empty(t, gitIn(t, ".", "status", "--porcelain"))
	assert.Equal(t, "two", gitIn(t, ".", "show", "HEAD:gen.txt"))
	msg := gitIn(t, ".", "log", "-1", "--format=%B", "--grep=Loom-Flow-Gate")
	assert.True(t, strings.HasPrefix(msg, "chore: regenerate gen.txt\n"), msg)
	assert.Contains(t, msg, "\nLoom-Flow-Gate: plan-s5-gates1")
	st, err := flowrun.Load(filepath.Join(state, "runs", "plan.json"))
	require.NoError(t, err)
	assert.Equal(t, []string{"gen.txt", "*.gen.ts"}, st.Generated)
	assert.Equal(t, []string{"plan-s5-gates1", "plan-s5-gates2", "plan-final-gates1"}, st.Gates, "gated again")
	assert.Equal(t, []string{"plan-s5-dev1", "plan-s5-review1", "plan-s5-docs1", "plan-final-review1"}, st.Agents, "no fixer")
	record, err := os.ReadFile(st.Record)
	require.NoError(t, err)
	assert.Contains(t, string(record), "Story 5: plan-s5-gates1 left gen.txt changed, which the engine committed as ")
}

// A flow root from before the fix prints no Generated line: the run
// commits nothing, and the fixer's tree check parks as before.
func TestFlowRun_WithoutGeneratedGlobsTheRewriteStillParks(t *testing.T) {
	generatedRun(t, "")
	_, _, err := runRun(t, "", "start", "docs/plan.md")
	require.NoError(t, err)
	_, _, err = runRun(t, "", "engine", "plan")
	require.NoError(t, err)
	out, _, err := runRun(t, "", "wait", "plan")
	require.NoError(t, err, out)
	assert.Contains(t, out, "parked plan: tree-moved (story 5)\nthe tree has uncommitted changes")
	assert.Equal(t, "M gen.txt", gitIn(t, ".", "status", "--porcelain"))
}

// generatedRepo is a repository with gen/api.ts, schema.json and main.go
// committed, and a runFX on it.
func generatedRepo(t *testing.T) (string, *runFX) {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "config", "user.name", "t")
	gitIn(t, repo, "config", "user.email", "t@t")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "gen"), 0o700))
	for _, f := range []string{"gen/api.ts", "schema.json", "main.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(repo, f), []byte("v1\n"), 0o600))
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	return repo, &runFX{cwd: repo, out: &strings.Builder{}}
}

func TestRunFX_CommitGenerated(t *testing.T) {
	globs := []string{"gen/*", "schema.json"}
	write := func(t *testing.T, repo, file string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(repo, file)), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(repo, file), []byte("v2\n"), 0o600))
	}
	t.Run("a clean tree", func(t *testing.T) {
		repo, fx := generatedRepo(t)
		base := gitIn(t, repo, "rev-parse", "HEAD")
		r, err := fx.CommitGenerated("r-s1-gates1", globs)
		require.NoError(t, err)
		assert.Equal(t, flowrun.Regenerated{}, r)
		assert.Equal(t, base, gitIn(t, repo, "rev-parse", "HEAD"))
	})
	t.Run("only generated files, changed and new, past a hook that refuses", func(t *testing.T) {
		repo, fx := generatedRepo(t)
		hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
		require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700))
		write(t, repo, "gen/api.ts")
		write(t, repo, "gen/sub/new.ts")
		r, err := fx.CommitGenerated("r-s1-gates1", globs)
		require.NoError(t, err)
		head := gitIn(t, repo, "rev-parse", "HEAD")
		assert.Equal(t, flowrun.Regenerated{Head: head, Paths: []string{"gen/api.ts", "gen/sub/new.ts"}}, r)
		assert.Empty(t, gitIn(t, repo, "status", "--porcelain"))
		assert.Equal(t, "chore: regenerate api.ts, new.ts\n\nThe gate run r-s1-gates1 left these generated files changed, and loom flow run committed them:\n- gen/api.ts\n- gen/sub/new.ts\n\nLoom-Flow-Gate: r-s1-gates1",
			gitIn(t, repo, "log", "-1", "--format=%B"))

		again, err := fx.CommitGenerated("r-s1-gates1", globs)
		require.NoError(t, err)
		assert.Equal(t, r, again, "its own commit at HEAD, after a crash")
		assert.Equal(t, head, gitIn(t, repo, "rev-parse", "HEAD"), "and no second commit")
		other, err := fx.CommitGenerated("r-s1-gates2", globs)
		require.NoError(t, err)
		assert.Equal(t, flowrun.Regenerated{}, other, "another gate run's: the tree is clean")
	})
	for _, c := range []struct {
		name  string
		dirty func(t *testing.T, repo string)
	}{
		{"beside another change", func(t *testing.T, repo string) { write(t, repo, "schema.json"); write(t, repo, "main.go") }},
		{"a new file no glob matches", func(t *testing.T, repo string) { write(t, repo, "gen/api.ts"); write(t, repo, "notes.txt") }},
		{"a deleted generated file", func(t *testing.T, repo string) { require.NoError(t, os.Remove(filepath.Join(repo, "schema.json"))) }},
		{"a renamed generated file", func(t *testing.T, repo string) { gitIn(t, repo, "mv", "gen/api.ts", "gen/b.ts") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo, fx := generatedRepo(t)
			base := gitIn(t, repo, "rev-parse", "HEAD")
			c.dirty(t, repo)
			status := gitIn(t, repo, "status", "--porcelain")
			r, err := fx.CommitGenerated("r-s1-gates1", globs)
			require.NoError(t, err)
			assert.Equal(t, flowrun.Regenerated{}, r)
			assert.Equal(t, base, gitIn(t, repo, "rev-parse", "HEAD"), "nothing committed")
			assert.Equal(t, status, gitIn(t, repo, "status", "--porcelain"), "nothing staged")
		})
	}
}

// globMatch answers as flow's glob_match (flow/lib/flowconf.py) does; each
// want is Python's answer.
func TestGlobMatch(t *testing.T) {
	for _, c := range []struct {
		path, glob string
		want       bool
	}{
		{"prader-app/src/app/core/services/bindings.ts", "prader-app/src/app/core/services/bindings.ts", true},
		{"a/b/bindings.ts", "bindings.ts", true},
		{"a/b/c.ts", "*.ts", true},
		{"gen/a/b.rs", "gen/*", true},
		{"src/gen.rs", "gen/*", false},
		{"a.txt", "?.txt", true},
		{"ab.txt", "?.txt", false},
		{"c.txt", "[ab].txt", false},
		{"c.txt", "[!ab].txt", true},
		{"a.txt", "[!ab].txt", false},
		{"a_ts", "a.ts", false},
		{"foobar", "foo", false},
		{"x[1].txt", "x[1].txt", false},
		{"x1.txt", "x[1].txt", true},
		{"a[b", "a[b", true},
		{"]", "[]]", true},
		{"é.ts", "?.ts", true},
		{"docs/API.md", "docs/*.md", true},
		{"a+b.ts", "a+b.ts", true},
		{`a\b`, `a\b`, true},
	} {
		assert.Equal(t, c.want, globMatch(c.path, []string{c.glob}), "%s against %s", c.path, c.glob)
	}
	assert.False(t, globMatch("a.ts", nil))
}

func TestListedGlobs(t *testing.T) {
	assert.Nil(t, listedGlobs(""))
	assert.Nil(t, listedGlobs("none"))
	assert.Equal(t, []string{"gen/*.ts", "schema.json"}, listedGlobs("`gen/*.ts`, `schema.json`"))
}
