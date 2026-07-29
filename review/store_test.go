package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	return string(b), err
}

func TestLoad_MissingReturnsEmptyState(t *testing.T) {
	root := t.TempDir()
	st, err := Load(root, filepath.Join(root, "plan.md"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "plan.md"), st.File)
	assert.Empty(t, st.Comments)
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, "plan.md")
	st := &ReviewState{File: doc}
	st.AddComment(Comment{
		ID: "abc12345", Line: 3, EndLine: 5,
		ContentSnippet: "## Rollout", Body: "phase this",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	})
	assert.NoError(t, Save(root, st))

	got, err := Load(root, doc)
	assert.NoError(t, err)
	assert.Len(t, got.Comments, 1)
	assert.Equal(t, st.Comments[0].Body, got.Comments[0].Body)
	assert.Equal(t, 5, got.Comments[0].EndLine)
}

func TestDeleteComment(t *testing.T) {
	st := &ReviewState{File: "x"}
	st.AddComment(Comment{ID: "a"})
	st.AddComment(Comment{ID: "b"})
	st.DeleteComment("a")
	assert.Len(t, st.Comments, 1)
	assert.Equal(t, "b", st.Comments[0].ID)
}

func TestSessionRoundtrip(t *testing.T) {
	root := t.TempDir()
	s := &CodeReviewSession{Files: []string{"a.go", "b.go"}, DiffBase: "HEAD", CreatedAt: time.Now()}
	assert.NoError(t, SaveSession(root, s))
	got, err := LoadSession(root)
	assert.NoError(t, err)
	assert.Equal(t, []string{"a.go", "b.go"}, got.Files)
}

func TestStore_InvisibleToGitStatus(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		assert.NoError(t, err, string(out))
		return string(out)
	}
	run("init", "-q")
	st := &ReviewState{File: filepath.Join(root, "plan.md")}
	st.AddComment(Comment{ID: "a", Line: 1, Body: "x", CreatedAt: time.Now()})
	assert.NoError(t, Save(root, st))
	assert.Empty(t, strings.TrimSpace(run("status", "--porcelain")),
		".crit must be fully self-ignored")
}
