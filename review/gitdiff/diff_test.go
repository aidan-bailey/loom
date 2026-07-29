package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initRepo creates a repo with one committed file, then applies
// working-tree changes: modify a.txt, add new.txt.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nTWO\nthree\nfour\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hi\n"), 0o644))
	return dir
}

func TestChangedFiles_RunsAgainstDirNotCwd(t *testing.T) {
	dir := initRepo(t)
	files, err := ChangedFiles(dir)
	assert.NoError(t, err)

	byPath := map[string]ChangeStatus{}
	for _, f := range files {
		byPath[f.Path] = f.Status
	}
	assert.Equal(t, StatusModified, byPath["a.txt"])
	assert.Equal(t, StatusUntracked, byPath["new.txt"])
}

func TestDiffFile_ChangedLines(t *testing.T) {
	dir := initRepo(t)
	info, err := DiffFile(dir, "a.txt", "HEAD")
	assert.NoError(t, err)
	require.NotNil(t, info)
	assert.True(t, info.ChangedLines[2], "line 2 (TWO) modified")
	assert.True(t, info.ChangedLines[4], "line 4 (four) added")
	assert.False(t, info.ChangedLines[1])
}
