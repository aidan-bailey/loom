package claudetmp

import (
	"archive/zip"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// archived archives a fresh temp dir named dirName under root and returns
// its path (now gone) and the zip.
func archived(t *testing.T, root, dirName string, files map[string]string) (src, zipPath string) {
	t.Helper()
	src = filepath.Join(root, dirName)
	writeTree(t, src, files)
	zipPath = filepath.Join(t.TempDir(), "a.zip")
	_, err := Archive(src, zipPath, Manifest{})
	require.NoError(t, err)
	require.NoDirExists(t, src)
	return src, zipPath
}

func TestRestore_RoundTripsContentModesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "-wt-feat-18be000000000001")
	writeTree(t, src, map[string]string{
		"s1/scratchpad/run.sh": "#!/bin/sh\n",
		"s1/tasks/out.log":     "log",
	})
	require.NoError(t, os.Chmod(filepath.Join(src, "s1", "scratchpad", "run.sh"), 0o755))
	require.NoError(t, os.Symlink("run.sh", filepath.Join(src, "s1", "scratchpad", "latest")))
	tasks := filepath.Join(src, "s1", "tasks")
	require.NoError(t, os.Chmod(tasks, 0o555))
	t.Cleanup(func() { _ = os.Chmod(tasks, 0o755) })
	zipPath := filepath.Join(t.TempDir(), "feat.zip")
	_, err := Archive(src, zipPath, Manifest{})
	require.NoError(t, err)
	require.NoDirExists(t, src)

	require.NoError(t, Restore(zipPath, root))

	assert.NoFileExists(t, zipPath, "a restored archive is deleted")
	data, err := os.ReadFile(filepath.Join(tasks, "out.log"))
	require.NoError(t, err)
	assert.Equal(t, "log", string(data))
	fi, err := os.Stat(filepath.Join(src, "s1", "scratchpad", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
	target, err := os.Readlink(filepath.Join(src, "s1", "scratchpad", "latest"))
	require.NoError(t, err)
	assert.Equal(t, "run.sh", target)
	fi, err = os.Stat(tasks)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o555), fi.Mode().Perm())
	assert.NoFileExists(t, filepath.Join(root, ManifestName), "the manifest is not restored")
}

// TestRestore_UsesTheManifestsName: a truncated name carries Claude's hash,
// which loom cannot compute, so only the manifest knows it.
func TestRestore_UsesTheManifestsName(t *testing.T) {
	root := t.TempDir()
	long := "-" + strings.Repeat("w", 199) + "-abc123"
	src, zipPath := archived(t, root, long, map[string]string{"s1/scratchpad/a": "a"})

	require.NoError(t, Restore(zipPath, root))
	assert.FileExists(t, filepath.Join(src, "s1", "scratchpad", "a"))
}

// TestRestore_AFileNamedLikeTheManifestIsContent: the manifest is the last
// entry of its name, which Archive writes last, so a user file of that name
// at the top level cannot stand in for it.
func TestRestore_AFileNamedLikeTheManifestIsContent(t *testing.T) {
	root := t.TempDir()
	src, zipPath := archived(t, root, "-wt-real", map[string]string{
		ManifestName:         `{"dir_name":"-other"}`,
		"s1/scratchpad/a.md": "a",
	})

	require.NoError(t, Restore(zipPath, root))

	data, err := os.ReadFile(filepath.Join(src, ManifestName))
	require.NoError(t, err, "the user's file round-trips as content")
	assert.Equal(t, `{"dir_name":"-other"}`, string(data))
	assert.FileExists(t, filepath.Join(src, "s1", "scratchpad", "a.md"))
	assert.NoDirExists(t, filepath.Join(root, "-other"))
}

func TestRestore_RefusesAnExistingTarget(t *testing.T) {
	root := t.TempDir()
	src, zipPath := archived(t, root, "-wt-x", map[string]string{"s1/scratchpad/a": "a"})
	require.NoError(t, os.Mkdir(src, 0o700)) // Claude started again before the restore

	require.Error(t, Restore(zipPath, root))
	assert.FileExists(t, zipPath, "a refused restore keeps the archive")
	assert.NoFileExists(t, filepath.Join(src, "s1", "scratchpad", "a"))
}

// TestRestore_CreatesAMissingRoot: /tmp is often tmpfs, emptied by a reboot.
func TestRestore_CreatesAMissingRoot(t *testing.T) {
	_, zipPath := archived(t, t.TempDir(), "-wt-x", map[string]string{"s1/scratchpad/a": "a"})
	root := filepath.Join(t.TempDir(), "claude-1000")

	require.NoError(t, Restore(zipPath, root))
	assert.FileExists(t, filepath.Join(root, "-wt-x", "s1", "scratchpad", "a"))
	fi, err := os.Stat(root)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
}

type handEntry struct {
	name, body string
	symlink    bool
}

// handZip writes an archive by hand, the way a crafted one could look: a
// manifest naming dirName, then entries.
func handZip(t *testing.T, dirName string, entries []handEntry) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "hand.zip")
	f, err := os.Create(zipPath)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	m, err := json.Marshal(Manifest{DirName: dirName})
	require.NoError(t, err)
	w, err := zw.Create(ManifestName)
	require.NoError(t, err)
	_, err = w.Write(m)
	require.NoError(t, err)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name}
		if e.symlink {
			hdr.SetMode(fs.ModeSymlink | 0o777)
		} else {
			hdr.SetMode(0o644)
		}
		w, err := zw.CreateHeader(hdr)
		require.NoError(t, err)
		_, err = io.WriteString(w, e.body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	return zipPath
}

func TestRestore_RejectsEscapes(t *testing.T) {
	outside := t.TempDir()
	for _, tc := range []struct {
		name    string
		dirName string
		entries []handEntry
	}{
		{"dot-dot entry", "-wt-x", []handEntry{{name: "../x", body: "evil"}}},
		{"absolute entry", "-wt-x", []handEntry{{name: "/x", body: "evil"}}},
		{"file beneath an outside symlink", "-wt-x", []handEntry{
			{name: "link", body: outside, symlink: true},
			{name: "link/x", body: "evil"},
		}},
		{"manifest naming a path", "../x", []handEntry{{name: "a", body: "a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "root")
			require.NoError(t, os.Mkdir(root, 0o700))
			zipPath := handZip(t, tc.dirName, tc.entries)

			require.Error(t, Restore(zipPath, root))

			assert.NoDirExists(t, filepath.Join(root, "-wt-x"))
			assert.FileExists(t, zipPath, "a failed restore keeps the archive")
			staging, _ := filepath.Glob(filepath.Join(root, ".loom-restore-*"))
			assert.Empty(t, staging, "the staging dir is removed")
			assert.NoFileExists(t, filepath.Join(parent, "x"))
			assert.NoFileExists(t, filepath.Join(outside, "x"))
		})
	}
}

// TestRestore_QuotesEntryNamesInErrors: an entry name is attacker-shaped
// text that ends up in the TUI, so a control character in it must not.
func TestRestore_QuotesEntryNamesInErrors(t *testing.T) {
	outside := t.TempDir()
	root := filepath.Join(t.TempDir(), "root")
	require.NoError(t, os.Mkdir(root, 0o700))
	zipPath := handZip(t, "-wt-x", []handEntry{
		{name: "link", body: outside, symlink: true},
		{name: "link/x\ny", body: "evil"},
	})

	err := Restore(zipPath, root)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "\n", "the entry name is quoted")
	assert.Contains(t, err.Error(), `"link/x\ny"`)
}

// TestRestore_UnreadableZipNamesTheCauseWithoutThePath: the caller appends
// the zip's path itself, and the TUI's error bar truncates a long message,
// so the path must not already be in the error (twice, as os.Open's own
// message carries it).
func TestRestore_UnreadableZipNamesTheCauseWithoutThePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	root := t.TempDir()
	_, zipPath := archived(t, root, "-wt-x", map[string]string{"s1/scratchpad/a": "a"})
	require.NoError(t, os.Chmod(zipPath, 0))
	t.Cleanup(func() { _ = os.Chmod(zipPath, 0o600) })

	err := Restore(zipPath, root)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	assert.NotContains(t, err.Error(), zipPath, "the caller names the archive")
	assert.NotContains(t, err.Error(), filepath.Base(zipPath))
	assert.FileExists(t, zipPath, "a failed restore keeps the archive")
}

// TestRestore_NoErrorNamesTheZip: the caller names the archive, so none of
// Restore's failures may carry its path.
func TestRestore_NoErrorNamesTheZip(t *testing.T) {
	garbage := func(t *testing.T) (string, string) {
		root := t.TempDir()
		zipPath := filepath.Join(t.TempDir(), "garbage.zip")
		require.NoError(t, os.WriteFile(zipPath, []byte("not a zip"), 0o600))
		return zipPath, root
	}
	noManifest := func(t *testing.T) (string, string) {
		zipPath := filepath.Join(t.TempDir(), "bare.zip")
		f, err := os.Create(zipPath)
		require.NoError(t, err)
		zw := zip.NewWriter(f)
		w, err := zw.Create("s1/scratchpad/a")
		require.NoError(t, err)
		_, err = w.Write([]byte("a"))
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		require.NoError(t, f.Close())
		return zipPath, t.TempDir()
	}
	existingTarget := func(t *testing.T) (string, string) {
		root := t.TempDir()
		src, zipPath := archived(t, root, "-wt-x", map[string]string{"s1/scratchpad/a": "a"})
		require.NoError(t, os.Mkdir(src, 0o700))
		return zipPath, root
	}
	badManifest := func(t *testing.T) (string, string) {
		return handZip(t, "../x", []handEntry{{name: "a", body: "a"}}), t.TempDir()
	}
	escapingEntry := func(t *testing.T) (string, string) {
		return handZip(t, "-wt-x", []handEntry{{name: "../x", body: "evil"}}), t.TempDir()
	}
	for name, setup := range map[string]func(*testing.T) (string, string){
		"not a zip": garbage, "no manifest": noManifest, "existing target": existingTarget,
		"invalid manifest": badManifest, "escaping entry": escapingEntry,
	} {
		t.Run(name, func(t *testing.T) {
			zipPath, root := setup(t)

			err := Restore(zipPath, root)

			require.Error(t, err)
			assert.NotContains(t, err.Error(), zipPath)
			assert.NotContains(t, err.Error(), filepath.Base(zipPath))
		})
	}
}
