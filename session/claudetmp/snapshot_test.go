package claudetmp

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatTree lays out a Claude project dir as a finished session leaves it:
// a transcript, a subagent's transcript and a tool result, all an hour old.
func chatTree(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "-wt-u-chat-18be000000000001")
	writeTree(t, src, map[string]string{
		"s1.jsonl":                     "turn 1\n",
		"s1/subagents/agent-1.jsonl":   "sub\n",
		"s1/tool-results/result-1.txt": "out\n",
	})
	require.NoError(t, filepath.WalkDir(src, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		backdate(t, p, time.Hour)
		return nil
	}))
	return src
}

// snapshotted takes a first snapshot of src (reason "pause") and returns
// the zip's path.
func snapshotted(t *testing.T, src string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "chats", "u-chat.zip")
	_, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "pause", Worktree: "/wt/u/chat_18be000000000001"})
	require.NoError(t, err)
	require.True(t, wrote)
	return zipPath
}

func TestSnapshot_CopiesAndLeavesTheSource(t *testing.T) {
	src := chatTree(t)
	zipPath := filepath.Join(t.TempDir(), "chats", "u-chat.zip")

	res, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "kill", Worktree: "/wt/u/chat_18be000000000001"})

	require.NoError(t, err)
	assert.True(t, wrote)
	assert.Positive(t, res.BytesOut)
	entries := zipEntries(t, zipPath)
	assert.Equal(t, "turn 1\n", readZipEntry(t, entries["s1.jsonl"]))
	assert.Equal(t, "sub\n", readZipEntry(t, entries["s1/subagents/agent-1.jsonl"]))
	assert.Equal(t, "out\n", readZipEntry(t, entries["s1/tool-results/result-1.txt"]))
	m := readZipManifest(t, zipPath)
	assert.Equal(t, "kill", m.Reason)
	assert.Equal(t, filepath.Base(src), m.DirName)
	assert.Equal(t, "/wt/u/chat_18be000000000001", m.Worktree)
	assert.Empty(t, m.CarriedForward)
	data, err := os.ReadFile(filepath.Join(src, "s1.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, "turn 1\n", string(data), "the source is never touched")
	fi, err := os.Stat(zipPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	requireNoPartials(t, filepath.Dir(zipPath))
}

func TestSnapshot_AnUnchangedSourceWritesNothing(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	before, err := os.Stat(zipPath)
	require.NoError(t, err)

	_, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "kill"})

	require.NoError(t, err)
	assert.False(t, wrote)
	after, err := os.Stat(zipPath)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())
	assert.Equal(t, "pause", readZipManifest(t, zipPath).Reason, "the earlier snapshot stands")
}

func TestSnapshot_ANewerFileAnywhereTriggersAWrite(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	writeTree(t, src, map[string]string{"s1/subagents/agent-2.jsonl": "sub 2\n"})

	_, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "kill"})

	require.NoError(t, err)
	assert.True(t, wrote)
	assert.Equal(t, "sub 2\n", readZipEntry(t, zipEntries(t, zipPath)["s1/subagents/agent-2.jsonl"]))
	assert.Equal(t, "kill", readZipManifest(t, zipPath).Reason)
}

// TestSnapshot_CarriesForwardWhatTheSourceLost: Claude's cleanup deletes a
// transcript between two snapshots. The second must not drop it: the zip
// may be its only copy.
func TestSnapshot_CarriesForwardWhatTheSourceLost(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	require.NoError(t, os.Remove(filepath.Join(src, "s1.jsonl")))
	writeTree(t, src, map[string]string{"s1/subagents/agent-1.jsonl": "sub, later\n"})

	res, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "sweep"})

	require.NoError(t, err)
	assert.True(t, wrote)
	assert.Equal(t, 1, res.Carried)
	entries := zipEntries(t, zipPath)
	assert.Equal(t, "turn 1\n", readZipEntry(t, entries["s1.jsonl"]), "a file Claude deleted stays archived")
	assert.Equal(t, "sub, later\n", readZipEntry(t, entries["s1/subagents/agent-1.jsonl"]), "the file on disk wins")
	assert.Equal(t, []string{"s1.jsonl"}, readZipManifest(t, zipPath).CarriedForward)

	writeTree(t, src, map[string]string{"s2.jsonl": "next\n"})
	_, wrote, err = Snapshot(src, zipPath, Manifest{Reason: "sweep"})
	require.NoError(t, err)
	require.True(t, wrote)
	assert.Equal(t, "turn 1\n", readZipEntry(t, zipEntries(t, zipPath)["s1.jsonl"]), "carried again by every later snapshot")
}

func TestSnapshot_AWriteFailureKeepsThePreviousZip(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	before, err := os.ReadFile(zipPath)
	require.NoError(t, err)
	writeTree(t, src, map[string]string{"s1.jsonl": strings.Repeat("y", 1<<20)})
	orig := openArchiveFile
	openArchiveFile = func(dir, pattern string) (archiveFile, string, error) {
		f, name, err := orig(dir, pattern)
		if err != nil {
			return nil, "", err
		}
		return &failingFile{File: f.(*os.File), left: 100}, name, nil
	}
	t.Cleanup(func() { openArchiveFile = orig })

	_, wrote, err := Snapshot(src, zipPath, Manifest{})

	require.Error(t, err)
	assert.False(t, wrote)
	after, err := os.ReadFile(zipPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the previous snapshot is untouched")
	requireNoPartials(t, filepath.Dir(zipPath))
}

// TestSnapshot_AnUnreadablePreviousZipIsMovedAsideAndKept: never replace
// what might be an archive loom cannot read.
func TestSnapshot_AnUnreadablePreviousZipIsMovedAsideAndKept(t *testing.T) {
	src := chatTree(t)
	dir := filepath.Join(t.TempDir(), "chats")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	zipPath := filepath.Join(dir, "u-chat.zip")
	require.NoError(t, os.WriteFile(zipPath, []byte("not a zip"), 0o600))
	backdate(t, zipPath, 2*time.Hour) // older than the source, which then reads as newer

	_, wrote, err := Snapshot(src, zipPath, Manifest{})

	require.NoError(t, err)
	assert.True(t, wrote)
	demoted, err := filepath.Glob(filepath.Join(dir, "u-chat.*.zip"))
	require.NoError(t, err)
	require.Len(t, demoted, 1)
	data, err := os.ReadFile(demoted[0])
	require.NoError(t, err)
	assert.Equal(t, "not a zip", string(data))
	assert.Contains(t, zipEntries(t, zipPath), "s1.jsonl")
}

func TestFill_RestoresOnlyWhatIsMissing(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	require.NoError(t, os.Remove(filepath.Join(src, "s1.jsonl")))
	writeTree(t, src, map[string]string{"s1/subagents/agent-1.jsonl": "local\n"})

	n, err := Fill(zipPath, src)

	require.NoError(t, err)
	assert.Equal(t, 1, n)
	data, err := os.ReadFile(filepath.Join(src, "s1.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, "turn 1\n", string(data))
	fi, err := os.Stat(filepath.Join(src, "s1.jsonl"))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), fi.ModTime(), time.Minute, "a restored transcript reads as fresh to Claude's cleanup")
	local, err := os.ReadFile(filepath.Join(src, "s1", "subagents", "agent-1.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, "local\n", string(local), "Fill never overwrites")
	assert.FileExists(t, zipPath, "the archive stays")
}

func TestFill_CreatesAMissingDir(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	require.NoError(t, os.RemoveAll(src))

	n, err := Fill(zipPath, src)

	require.NoError(t, err)
	assert.Equal(t, 3, n, "files and links are counted, directories are not")
	assert.FileExists(t, filepath.Join(src, "s1.jsonl"))
	assert.FileExists(t, filepath.Join(src, "s1", "subagents", "agent-1.jsonl"))
	assert.FileExists(t, filepath.Join(src, "s1", "tool-results", "result-1.txt"))
}

func TestFill_RefusesAnotherDirsArchive(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	other := filepath.Join(t.TempDir(), "-wt-u-other-18be000000000002")

	_, err := Fill(zipPath, other)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds")
	assert.NoDirExists(t, other)
}

func TestFill_RejectsEscapes(t *testing.T) {
	outside := t.TempDir()
	for _, tc := range []struct {
		name    string
		entries []handEntry
	}{
		{"dot-dot entry", []handEntry{{name: "../x", body: "evil"}}},
		{"absolute entry", []handEntry{{name: "/x", body: "evil"}}},
		{"file beneath an outside symlink", []handEntry{
			{name: "link", body: outside, symlink: true},
			{name: "link/x", body: "evil"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "-wt-x")
			zipPath := handZip(t, "-wt-x", tc.entries)

			_, err := Fill(zipPath, dir)

			require.Error(t, err)
			assert.NoFileExists(t, filepath.Join(parent, "x"))
			assert.NoFileExists(t, filepath.Join(outside, "x"))
		})
	}
}
