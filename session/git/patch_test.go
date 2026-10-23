package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// patchRepo makes a repository whose one commit holds files, and returns it
// and the commit's SHA.
func patchRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "t@example.com")
	runGit(t, repo, "config", "user.name", "T")
	for name, body := range files {
		writeFile(t, repo, name, body)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "init")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return repo, strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, repo, name, body string) {
	t.Helper()
	path := filepath.Join(repo, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// splitWorktree is the patch of repo's work tree against base, split.
func splitWorktree(t *testing.T, repo, base string) []FilePatch {
	t.Helper()
	text, err := NewGitWorktreeFromStorage(repo, repo, "t", "main", base, false, "").Patch()
	require.NoError(t, err)
	return SplitPatch(text)
}

func fileAt(t *testing.T, files []FilePatch, path string) FilePatch {
	t.Helper()
	var names []string
	for _, f := range files {
		if f.Path == path {
			return f
		}
		names = append(names, f.Path)
	}
	t.Fatalf("no file %q among %q", path, names)
	return FilePatch{}
}

func TestPatch_SplitsEachKindOfChange(t *testing.T) {
	repo, base := patchRepo(t, map[string]string{
		"src.txt":     "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n",
		"gone.txt":    "old\n",
		"mode.sh":     "echo\n",
		"before.txt":  "rename me\nline2\nline3\nline4\n",
		"nonl.txt":    "nonl",
		"bin.dat":     "a\x00b",
		"my file.txt": "keep\n",
		`q"uote.txt`:  "x\n",
		"café.txt":    "y\n",
		"blank.txt":   "one\n\nthree\n",
		"plus.txt":    "a\n",
		"dash.txt":    "-- y\nkeep\n",
	})
	runGit(t, repo, "config", "diff.renames", "copies")
	runGit(t, repo, "config", "diff.suppressBlankEmpty", "true")
	changed := "one\ntwo\nTHREE\nfour\nfive\nsix\nseven\neight\n"
	writeFile(t, repo, "src.txt", changed)
	writeFile(t, repo, "copy.txt", changed)
	require.NoError(t, os.Remove(filepath.Join(repo, "gone.txt")))
	require.NoError(t, os.Chmod(filepath.Join(repo, "mode.sh"), 0o755))
	runGit(t, repo, "mv", "before.txt", "after.txt")
	writeFile(t, repo, "after.txt", "rename me\nline2\nline3\nline4\nline5\n")
	writeFile(t, repo, "nonl.txt", "nonl2")
	writeFile(t, repo, "bin.dat", "a\x00c")
	writeFile(t, repo, "new.txt", "fresh\n")
	writeFile(t, repo, "empty.txt", "")
	writeFile(t, repo, "my file.txt", "keep\nmore\n")
	writeFile(t, repo, `q"uote.txt`, "x2\n")
	writeFile(t, repo, "café.txt", "y2\n")
	writeFile(t, repo, "blank.txt", "one\n\nTHREE\n")
	writeFile(t, repo, "plus.txt", "a\n++ x\n")
	writeFile(t, repo, "dash.txt", "keep\n")

	files := splitWorktree(t, repo, base)
	type shape struct {
		old, status    string
		binary         bool
		added, removed int
		hunks          int
	}
	for path, want := range map[string]shape{
		"src.txt":     {"", StatusModified, false, 1, 1, 1},
		"copy.txt":    {"src.txt", StatusCopied, false, 1, 1, 1},
		"gone.txt":    {"", StatusDeleted, false, 0, 1, 1},
		"mode.sh":     {"", StatusModified, false, 0, 0, 0},
		"after.txt":   {"before.txt", StatusRenamed, false, 1, 0, 1},
		"nonl.txt":    {"", StatusModified, false, 1, 1, 1},
		"bin.dat":     {"", StatusModified, true, 0, 0, 0},
		"new.txt":     {"", StatusAdded, false, 1, 0, 1},
		"empty.txt":   {"", StatusAdded, false, 0, 0, 0},
		"my file.txt": {"", StatusModified, false, 1, 0, 1},
		`q"uote.txt`:  {"", StatusModified, false, 1, 1, 1},
		"café.txt":    {"", StatusModified, false, 1, 1, 1},
		"blank.txt":   {"", StatusModified, false, 1, 1, 1},
		"plus.txt":    {"", StatusModified, false, 1, 0, 1},
		"dash.txt":    {"", StatusModified, false, 0, 1, 1},
	} {
		f := fileAt(t, files, path)
		assert.Equal(t, want, shape{f.OldPath, f.Status, f.Binary, f.Added, f.Removed, len(f.Hunks)}, path)
	}
	assert.Len(t, files, 15)
	assert.Equal(t, []string{" one", "", "-three", "+THREE"}, fileAt(t, files, "blank.txt").Hunks[0].Lines,
		"a blank context line, written empty")
	assert.Contains(t, fileAt(t, files, "plus.txt").Hunks[0].Lines, "+++ x", "an added line reading +++ x stays a line")
	assert.Contains(t, fileAt(t, files, "dash.txt").Hunks[0].Lines, "--- y", "a removed line reading --- y stays a line")
	nonl := fileAt(t, files, "nonl.txt").Hunks[0].Lines
	assert.Equal(t, `\ No newline at end of file`, nonl[len(nonl)-1])
}

func TestPatch_TheUsersDiffConfigChangesNothing(t *testing.T) {
	// The file sits under a directory named b, so a patch without its a/
	// and b/ prefixes (diff.noprefix) would name it a.txt.
	repo, base := patchRepo(t, map[string]string{"b/a.txt": "one\n"})
	for _, kv := range [][2]string{
		{"color.diff", "always"}, {"color.ui", "always"}, {"diff.noprefix", "true"},
		{"diff.mnemonicPrefix", "true"}, {"diff.external", "false"},
	} {
		runGit(t, repo, "config", kv[0], kv[1])
	}
	writeFile(t, repo, "b/a.txt", "one\ntwo\n")

	text, err := NewGitWorktreeFromStorage(repo, repo, "t", "main", base, false, "").Patch()
	require.NoError(t, err)
	assert.NotContains(t, text, "\x1b[", "no colour")
	files := SplitPatch(text)
	require.Len(t, files, 1)
	assert.Equal(t, "b/a.txt", files[0].Path)
	assert.Equal(t, 1, files[0].Added)
}

func TestPatch_NeedsABaseCommit(t *testing.T) {
	repo, _ := patchRepo(t, map[string]string{"a.txt": "one\n"})
	_, err := NewGitWorktreeFromStorage(repo, repo, "t", "main", "", false, "").Patch()
	assert.ErrorContains(t, err, "base commit SHA not set")
}

func TestSplitPatch_NamesAFileFromItsHeaderWhenNothingElseDoes(t *testing.T) {
	text := strings.Join([]string{
		"diff --git a/my file.bin b/my file.bin",
		"index 1111111..2222222 100644",
		"Binary files a/my file.bin and b/my file.bin differ",
		`diff --git "a/caf\303\251.bin" "b/caf\303\251.bin"`,
		"new file mode 100644",
		"index 0000000..3333333",
		`Binary files /dev/null and "b/caf\303\251.bin" differ`,
		"diff --git a/plain b/plain",
		"old mode 100644",
		"new mode 100755",
	}, "\n") + "\n"
	files := SplitPatch(text)
	require.Len(t, files, 3)
	assert.Equal(t, "my file.bin", files[0].Path)
	assert.True(t, files[0].Binary)
	assert.Equal(t, "café.bin", files[1].Path)
	assert.Equal(t, StatusAdded, files[1].Status)
	assert.True(t, files[1].Binary)
	assert.Equal(t, "plain", files[2].Path)
	assert.Empty(t, files[2].Hunks)
}

func TestSplitPatch_KeepsWithinItsBounds(t *testing.T) {
	var b strings.Builder
	file := func(name string, lines []string) {
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -0,0 +1,%d @@\n", name, name, name, name, len(lines))
		for _, l := range lines {
			b.WriteString("+" + l + "\n")
		}
	}
	file("long.txt", []string{strings.Repeat("x", maxPatchLineBytes-2) + "é" + "y", "short"})
	many := make([]string, maxFileLines+1)
	for i := range many {
		many[i] = "l"
	}
	file("many.txt", many)
	big := make([]string, 1100)
	for i := range big {
		big[i] = strings.Repeat("z", 1000)
	}
	file("big.txt", big)
	file("after.txt", []string{"tiny"})

	files := SplitPatch(b.String())
	require.Len(t, files, 4)
	h := files[0].Hunks[0]
	assert.Equal(t, []int{0}, h.Cut)
	assert.Equal(t, "+"+strings.Repeat("x", maxPatchLineBytes-2), h.Lines[0], "cut back to a rune boundary")
	assert.Equal(t, "+short", h.Lines[1])
	assert.Equal(t, OmittedLines, files[1].Omitted)
	assert.Empty(t, files[1].Hunks)
	assert.Equal(t, maxFileLines+1, files[1].Added, "the counts stay exact")
	assert.Equal(t, OmittedBudget, files[2].Omitted)
	assert.Equal(t, 1100, files[2].Added)
	assert.Equal(t, OmittedBudget, files[3].Omitted, "every file after the budget ran out")
	assert.Equal(t, 1, files[3].Added)
}
