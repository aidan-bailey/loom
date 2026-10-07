package claudetmp

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTree lays out files (slash paths relative to dir) with contents.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
}

// requireNoPartials fails when a *.partial file is left in dir.
func requireNoPartials(t *testing.T, dir string) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(dir, "*.partial"))
	require.NoError(t, err)
	assert.Empty(t, left, "no partial file remains")
}

// zipEntries opens zipPath for the rest of the test and indexes its entries.
func zipEntries(t *testing.T, zipPath string) map[string]*zip.File {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	t.Cleanup(func() { zr.Close() })
	out := map[string]*zip.File{}
	for _, f := range zr.File {
		out[f.Name] = f
	}
	return out
}

func readZipEntry(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	require.NoError(t, err)
	defer rc.Close()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(data)
}

func readZipManifest(t *testing.T, zipPath string) Manifest {
	t.Helper()
	f := zipEntries(t, zipPath)[ManifestName]
	require.NotNil(t, f, "every archive carries a manifest")
	var m Manifest
	require.NoError(t, json.Unmarshal([]byte(readZipEntry(t, f)), &m))
	return m
}

func TestArchive_SkipsTaggedCachesKeepsMalformedTags(t *testing.T) {
	src := filepath.Join(t.TempDir(), "proj")
	tag := cacheDirSignature + "\n# cargo\n"
	writeTree(t, src, map[string]string{
		"s1/scratchpad/notes.md":            "notes",
		"s1/scratchpad/target/CACHEDIR.TAG": tag,
		"s1/scratchpad/target/debug/big":    strings.Repeat("x", 1000),
		"s1/scratchpad/fake/CACHEDIR.TAG":   "Signature: not the real one",
		"s1/scratchpad/fake/keep.txt":       "kept",
	})
	zipPath := filepath.Join(t.TempDir(), "archive", "proj.zip")

	res, err := Archive(src, zipPath, Manifest{Worktree: "/wt", Reason: "pause"})
	require.NoError(t, err)

	entries := zipEntries(t, zipPath)
	assert.Contains(t, entries, "s1/scratchpad/notes.md")
	assert.Contains(t, entries, "s1/scratchpad/fake/keep.txt", "a malformed tag is ordinary content")
	assert.NotContains(t, entries, "s1/scratchpad/target/debug/big")
	assert.NotContains(t, entries, "s1/scratchpad/target/CACHEDIR.TAG")
	m := readZipManifest(t, zipPath)
	assert.Equal(t, "proj", m.DirName)
	assert.Equal(t, src, m.Source)
	assert.Equal(t, "/wt", m.Worktree)
	assert.Equal(t, "pause", m.Reason)
	assert.False(t, m.ArchivedAt.IsZero())
	require.Len(t, m.SkippedCaches, 1)
	assert.Equal(t, "s1/scratchpad/target", m.SkippedCaches[0].Path)
	assert.EqualValues(t, 1000+len(tag), m.SkippedCaches[0].Bytes)
	assert.Equal(t, m.SkippedCaches[0].Bytes, res.CacheBytes)
	assert.NoDirExists(t, src, "the source goes once the zip is in place")
	fi, err := os.Stat(zipPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "scratchpads can hold secrets")
	assert.Equal(t, fi.Size(), res.BytesOut)
	dfi, err := os.Stat(filepath.Dir(zipPath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dfi.Mode().Perm())
}

// TestArchive_StoresSymlinksAsLinks: following a link could archive $HOME.
func TestArchive_StoresSymlinksAsLinks(t *testing.T) {
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"secret": "do not archive"})
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})
	require.NoError(t, os.Symlink(outside, filepath.Join(src, "s1", "scratchpad", "home")))
	zipPath := filepath.Join(t.TempDir(), "proj.zip")

	_, err := Archive(src, zipPath, Manifest{})
	require.NoError(t, err)

	entries := zipEntries(t, zipPath)
	link := entries["s1/scratchpad/home"]
	require.NotNil(t, link)
	assert.NotZero(t, link.Mode()&fs.ModeSymlink)
	assert.Equal(t, outside, readZipEntry(t, link))
	for name := range entries {
		assert.NotContains(t, name, "secret")
	}
	assert.FileExists(t, filepath.Join(outside, "secret"), "deleting the source never follows a link")
}

// TestArchive_DeletesReadOnlyDirectories: Go's module cache makes its own
// directories read-only, which a plain RemoveAll refuses.
func TestArchive_DeletesReadOnlyDirectories(t *testing.T) {
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/gomod/pkg@v1/file.go": "package p"})
	ro := []string{filepath.Join(src, "s1", "gomod", "pkg@v1"), filepath.Join(src, "s1", "gomod")}
	for _, d := range ro {
		require.NoError(t, os.Chmod(d, 0o555))
	}
	t.Cleanup(func() {
		for _, d := range ro {
			_ = os.Chmod(d, 0o755)
		}
	})

	_, err := Archive(src, filepath.Join(t.TempDir(), "proj.zip"), Manifest{})

	require.NoError(t, err)
	assert.NoDirExists(t, src)
}

// TestArchive_LeavesNoTombstoneBesideTheSource: the source is renamed to a
// .loom-trash-* tombstone before it is deleted, and that goes too.
func TestArchive_LeavesNoTombstoneBesideTheSource(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})

	_, err := Archive(src, filepath.Join(t.TempDir(), "proj.zip"), Manifest{})

	require.NoError(t, err)
	left, err := os.ReadDir(parent)
	require.NoError(t, err)
	assert.Empty(t, left, "neither the source nor a tombstone remains")
}

// TestArchive_IsAllOrNothingOnTheSourcesName: when the source cannot be
// moved aside (its parent is read-only), the zip is taken back and the
// source left whole, so a later Locate still finds it and nothing is
// half-deleted.
func TestArchive_IsAllOrNothingOnTheSourcesName(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	parent := t.TempDir()
	src := filepath.Join(parent, "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})
	require.NoError(t, os.Chmod(parent, 0o555))
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	zipPath := filepath.Join(t.TempDir(), "proj.zip")

	_, err := Archive(src, zipPath, Manifest{})

	require.Error(t, err)
	assert.FileExists(t, filepath.Join(src, "s1", "scratchpad", "a.txt"), "the source is intact under its name")
	assert.NoFileExists(t, zipPath)
	requireNoPartials(t, filepath.Dir(zipPath))
}

// TestArchive_ASourceThatVanishedKeepsTheZip: when another process has
// already moved src (two looms archiving one dir), the zip is a complete
// copy and the only one, so a failed rename must not delete it.
func TestArchive_ASourceThatVanishedKeepsTheZip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})
	orig := afterZipInPlace
	afterZipInPlace = func() { require.NoError(t, os.RemoveAll(src)) }
	t.Cleanup(func() { afterZipInPlace = orig })
	zipPath := filepath.Join(t.TempDir(), "proj.zip")

	_, err := Archive(src, zipPath, Manifest{})

	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), zipPath, "the error says where the kept archive is")
	assert.Contains(t, zipEntries(t, zipPath), "s1/scratchpad/a.txt", "the archive is kept whole")
	requireNoPartials(t, filepath.Dir(zipPath))
}

// TestArchive_AZipAnotherProcessReplacedIsNotRemoved: B's zip can replace
// A's at the same path while A moves the shared src aside; B's own move
// then fails, and removing "its" zip would delete the only copy.
func TestArchive_AZipAnotherProcessReplacedIsNotRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	parent := t.TempDir()
	src := filepath.Join(parent, "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})
	require.NoError(t, os.Chmod(parent, 0o555)) // the move aside fails: src still exists
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	zipDir := t.TempDir()
	zipPath := filepath.Join(zipDir, "proj.zip")
	orig := afterZipInPlace
	afterZipInPlace = func() {
		theirs := filepath.Join(zipDir, "theirs")
		require.NoError(t, os.WriteFile(theirs, []byte("theirs"), 0o600))
		require.NoError(t, os.Rename(theirs, zipPath))
	}
	t.Cleanup(func() { afterZipInPlace = orig })

	_, err := Archive(src, zipPath, Manifest{})

	require.Error(t, err)
	data, readErr := os.ReadFile(zipPath)
	require.NoError(t, readErr, "a zip this call did not write stays")
	assert.Equal(t, "theirs", string(data))
	assert.Contains(t, err.Error(), zipPath)
	assert.FileExists(t, filepath.Join(src, "s1", "scratchpad", "a.txt"))
}

// failingFile fails every write once more than left bytes have gone through.
type failingFile struct {
	*os.File
	left int
}

func (f *failingFile) Write(p []byte) (int, error) {
	if len(p) > f.left {
		return 0, errors.New("disk full")
	}
	f.left -= len(p)
	return f.File.Write(p)
}

func TestArchive_WriteFailureLeavesTheSourceAlone(t *testing.T) {
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/big": strings.Repeat("y", 1<<20)})
	orig := openArchiveFile
	openArchiveFile = func(dir, pattern string) (archiveFile, string, error) {
		f, name, err := orig(dir, pattern)
		if err != nil {
			return nil, "", err
		}
		return &failingFile{File: f.(*os.File), left: 100}, name, nil
	}
	t.Cleanup(func() { openArchiveFile = orig })
	zipPath := filepath.Join(t.TempDir(), "proj.zip")

	_, err := Archive(src, zipPath, Manifest{})

	require.Error(t, err)
	assert.NoFileExists(t, zipPath)
	requireNoPartials(t, filepath.Dir(zipPath))
	assert.FileExists(t, filepath.Join(src, "s1", "scratchpad", "big"))
}

// TestArchive_EachWriteUsesItsOwnPartialFile: two processes archiving into
// one directory (two looms on one workspace) must never share the file
// being written, or the first to finish would rename the other's
// interleaved, corrupt bytes into place and delete its source.
func TestArchive_EachWriteUsesItsOwnPartialFile(t *testing.T) {
	var opened []string
	orig := openArchiveFile
	openArchiveFile = func(dir, pattern string) (archiveFile, string, error) {
		f, name, err := orig(dir, pattern)
		opened = append(opened, name)
		return f, name, err
	}
	t.Cleanup(func() { openArchiveFile = orig })
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "proj.zip")
	for _, body := range []string{"one", "two"} {
		src := filepath.Join(t.TempDir(), "proj")
		writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": body})
		_, err := Archive(src, zipPath, Manifest{})
		require.NoError(t, err)
	}

	require.Len(t, opened, 2)
	assert.NotEqual(t, opened[0], opened[1], "the same zipPath gets a fresh partial file each time")
	for _, name := range opened {
		assert.Equal(t, dir, filepath.Dir(name), "the partial file sits beside the zip, so its rename is atomic")
		assert.True(t, strings.HasSuffix(name, ".partial"), name)
	}
	requireNoPartials(t, dir)
}

// TestOpenArchiveFile_GivesEachCallItsOwnPrivateFile: two writers open at
// the same time never share a file, whatever the zip's name.
func TestOpenArchiveFile_GivesEachCallItsOwnPrivateFile(t *testing.T) {
	dir := t.TempDir()
	a, aName, err := openArchiveFile(dir, "x.zip.*.partial")
	require.NoError(t, err)
	defer a.Close()
	b, bName, err := openArchiveFile(dir, "x.zip.*.partial")
	require.NoError(t, err)
	defer b.Close()

	assert.NotEqual(t, aName, bName)
	for _, name := range []string{aName, bName} {
		fi, err := os.Stat(name)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "scratchpads can hold secrets")
	}
}

func TestArchiveAs_DemotesAParkedZip(t *testing.T) {
	dir := t.TempDir()
	parked := filepath.Join(dir, "feat-18be000000000001.zip")
	require.NoError(t, os.WriteFile(parked, []byte("earlier"), 0o600))
	archivedAt := time.Date(2026, 10, 5, 13, 29, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(parked, archivedAt, archivedAt))
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})

	zipPath, _, err := ArchiveAs(dir, "feat-18be000000000001", src, Manifest{})

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "feat-18be000000000001.zip"), zipPath)
	demoted, err := filepath.Glob(filepath.Join(dir, "feat-18be000000000001.*Z.zip"))
	require.NoError(t, err)
	require.Len(t, demoted, 1)
	assert.Equal(t, filepath.Join(dir, "feat-18be000000000001.20261005T132900Z.zip"), demoted[0],
		"the stamp is when the earlier zip was archived (its mtime), not when it was moved aside")
	data, err := os.ReadFile(demoted[0])
	require.NoError(t, err)
	assert.Equal(t, "earlier", string(data), "the earlier zip becomes a permanent archive")
	assert.Contains(t, zipEntries(t, zipPath), "s1/scratchpad/a.txt")
}

func TestParked_FindsOnlyTheUndemotedZip(t *testing.T) {
	dir := t.TempDir()
	wt := "/r/.loom/worktrees/u/feat_18be000000000001"
	prefixes := WorktreePrefixes("/r/.loom")
	n, _ := DirName(wt)
	name := ArchiveName(n, prefixes)
	require.Equal(t, "u-feat-18be000000000001", name)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".20261005T132900Z.zip"), nil, 0o600))

	_, ok := Parked(dir, wt, prefixes)
	assert.False(t, ok, "a demoted archive is never restored")

	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".zip"), nil, 0o600))
	got, ok := Parked(dir, wt, prefixes)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(dir, name+".zip"), got)
}

// backdate sets name's modification time to age ago.
func backdate(t *testing.T, name string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(name, old, old))
}

func TestPurgeTrash_RemovesOnlyOldTombstoneDirs(t *testing.T) {
	root := t.TempDir()
	oldTrash := filepath.Join(root, ".loom-trash-1-abc")
	writeTree(t, oldTrash, map[string]string{"s1/scratchpad/a.txt": "already archived"})
	// Go's module cache makes read-only dirs; the purge must cope.
	require.NoError(t, os.Chmod(filepath.Join(oldTrash, "s1"), 0o555))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(oldTrash, "s1"), 0o755) })
	backdate(t, oldTrash, 48*time.Hour)

	freshTrash := filepath.Join(root, ".loom-trash-2-def")
	writeTree(t, freshTrash, map[string]string{"a.txt": "a"})

	oldSession := filepath.Join(root, "-wt-feat-18be000000000001")
	writeTree(t, oldSession, map[string]string{"s1/scratchpad/a.txt": "a live session's"})
	backdate(t, oldSession, 48*time.Hour)

	oldFile := filepath.Join(root, ".loom-trash-x")
	require.NoError(t, os.WriteFile(oldFile, []byte("not a dir"), 0o600))
	backdate(t, oldFile, 48*time.Hour)

	// A Restore a quit or crash cut short leaves its staging dir behind.
	oldStaging := filepath.Join(root, ".loom-restore-abc")
	writeTree(t, oldStaging, map[string]string{"s1/scratchpad/a.txt": "half restored"})
	backdate(t, oldStaging, 48*time.Hour)
	freshStaging := filepath.Join(root, ".loom-restore-def")
	writeTree(t, freshStaging, map[string]string{"a.txt": "a"})

	n := PurgeTrash(root, 24*time.Hour)

	assert.Equal(t, 2, n)
	assert.NoDirExists(t, oldTrash, "an old tombstone goes")
	assert.NoDirExists(t, oldStaging, "so does the staging dir of an interrupted restore")
	assert.DirExists(t, freshStaging, "a restore may be running right now")
	assert.DirExists(t, freshTrash, "a fresh one may still be being deleted")
	assert.DirExists(t, oldSession, "only tombstones are ever purged, however old")
	assert.FileExists(t, oldFile, "a file is not a tombstone")
	assert.FileExists(t, filepath.Join(oldSession, "s1", "scratchpad", "a.txt"))
}

func TestPurgeTrash_MissingRootRemovesNothing(t *testing.T) {
	assert.Zero(t, PurgeTrash(filepath.Join(t.TempDir(), "no-such-root"), time.Hour))
}

func TestPurgePartials_RemovesOnlyOldPartialFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		backdate(t, p, age)
		return p
	}
	oldPartial := write("feat.zip.123456.partial", 48*time.Hour)
	freshPartial := write("feat2.zip.654321.partial", 0)
	oldZip := write("feat3.zip", 48*time.Hour)
	oldDir := filepath.Join(dir, "x.partial")
	require.NoError(t, os.Mkdir(oldDir, 0o700))
	backdate(t, oldDir, 48*time.Hour)

	n := PurgePartials(dir, 24*time.Hour)

	assert.Equal(t, 1, n)
	assert.NoFileExists(t, oldPartial, "a stale partial from a crashed archive goes")
	assert.FileExists(t, freshPartial, "a fresh one may be being written right now")
	assert.FileExists(t, oldZip, "only .partial files are purged")
	assert.DirExists(t, oldDir, "a directory is not a partial file")
}

func TestPurgePartials_MissingDirRemovesNothing(t *testing.T) {
	assert.Zero(t, PurgePartials(filepath.Join(t.TempDir(), "no-such-dir"), time.Hour))
}
