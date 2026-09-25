package account

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mainDirWith builds a main config dir holding shared entries and the
// per-account ones the deny-list keeps out.
func mainDirWith(t *testing.T) string {
	t.Helper()
	main := t.TempDir()
	for _, f := range []string{"CLAUDE.md", "RTK.md", "settings.json", ".credentials.json", ".claude.json", ".claude.json.backup"} {
		require.NoError(t, os.WriteFile(filepath.Join(main, f), []byte(f), 0o600))
	}
	for _, d := range []string{"projects", "skills", "sessions", "daemon"} {
		require.NoError(t, os.Mkdir(filepath.Join(main, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(main, "projects", "keep.jsonl"), []byte("x"), 0o600))
	return main
}

func notExist(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

func TestSync_LinksSharedEntriesOnly(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()

	rep, err := Sync(acct, main)
	require.NoError(t, err)

	assert.Equal(t, []string{"CLAUDE.md", "RTK.md", "projects", "settings.json", "skills"}, rep.Linked)
	assert.Empty(t, rep.Diverged)
	target, err := os.Readlink(filepath.Join(acct, "projects"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(main, "projects"), target)
	for _, private := range []string{".credentials.json", ".claude.json", ".claude.json.backup", "sessions", "daemon"} {
		assert.True(t, notExist(t, filepath.Join(acct, private)), "%s must stay per account", private)
	}
}

func TestSync_IsIdempotent(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	_, err := Sync(acct, main)
	require.NoError(t, err)

	rep, err := Sync(acct, main)
	require.NoError(t, err)
	assert.Empty(t, rep.Linked)
	assert.Empty(t, rep.Diverged)
}

func TestSync_ReportsADivergedRealFileAndKeepsIt(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(acct, "settings.json"), []byte("mine"), 0o600))

	rep, err := Sync(acct, main)
	require.NoError(t, err)

	assert.Equal(t, []string{"settings.json"}, rep.Diverged)
	data, err := os.ReadFile(filepath.Join(acct, "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, "mine", string(data), "a diverged file may hold the only copy of a change")
}

func TestSync_PicksUpNewMainEntries(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	_, err := Sync(acct, main)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(main, "agents"), 0o755))

	rep, err := Sync(acct, main)
	require.NoError(t, err)
	assert.Equal(t, []string{"agents"}, rep.Linked)
}

func TestSync_MissingMainDirFails(t *testing.T) {
	_, err := Sync(t.TempDir(), filepath.Join(t.TempDir(), "nope"))
	assert.Error(t, err)
}

func TestCreate_MakesALinkedDirAndRegistersIt(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)

	acct, rep, err := r.Create("max-2", main)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(global, "accounts", "max-2"), acct.Dir)
	assert.Contains(t, rep.Linked, "projects")
	fi, err := os.Stat(acct.Dir)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o700), fi.Mode().Perm(), "the dir will hold credentials")
	got, ok := LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	assert.Equal(t, acct, got)
}

func TestCreate_RejectsInvalidAndDuplicateNames(t *testing.T) {
	r, main := LoadRegistry(t.TempDir()), mainDirWith(t)
	_, _, err := r.Create("Bad Name", main)
	assert.Error(t, err)
	_, _, err = r.Create(DefaultName, main)
	assert.Error(t, err)
	_, _, err = r.Create("max-2", main)
	require.NoError(t, err)
	_, _, err = r.Create("max-2", main)
	assert.Error(t, err)
}

func TestCreate_FailureLeavesNothingBehind(t *testing.T) {
	global := t.TempDir()
	r := LoadRegistry(global)

	_, _, err := r.Create("max-2", filepath.Join(global, "no-such-main"))

	require.Error(t, err)
	assert.True(t, notExist(t, filepath.Join(global, "accounts", "max-2")))
	assert.False(t, LoadRegistry(global).HasExtra())
}

func TestRemove_DeletesLinksButNeverTheirTargets(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	acct, _, err := r.Create("max-2", main)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(acct.Dir, ".credentials.json"), []byte("secret"), 0o600))

	deleted, err := r.Remove("max-2")

	require.NoError(t, err)
	assert.True(t, deleted)
	assert.True(t, notExist(t, acct.Dir))
	data, err := os.ReadFile(filepath.Join(main, "projects", "keep.jsonl"))
	require.NoError(t, err, "shared content behind the links must survive")
	assert.Equal(t, "x", string(data))
	assert.False(t, LoadRegistry(global).HasExtra())
}

func TestRemove_ClearsTheDefaultAndRefusesDefaultAccount(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	_, _, err := r.Create("max-2", main)
	require.NoError(t, err)
	require.NoError(t, r.SetDefault("max-2"))

	_, err = r.Remove(DefaultName)
	assert.Error(t, err)
	_, err = r.Remove("max-2")
	require.NoError(t, err)
	assert.Equal(t, DefaultName, LoadRegistry(global).Default())
}

func TestRemove_OutsideAccountsDirOnlyUnregisters(t *testing.T) {
	global, outside := t.TempDir(), t.TempDir()
	r := LoadRegistry(global)
	require.NoError(t, r.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "byo", Dir: outside})
		return nil
	}))

	deleted, err := r.Remove("byo")

	require.NoError(t, err)
	assert.False(t, deleted)
	_, err = os.Stat(outside)
	assert.NoError(t, err, "loom only deletes dirs it created")
}
