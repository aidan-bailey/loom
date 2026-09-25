package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadRegistry_MissingFileIsEmpty(t *testing.T) {
	r := LoadRegistry(t.TempDir())
	require.NoError(t, r.LoadErr())
	assert.False(t, r.HasExtra())
	assert.Equal(t, DefaultName, r.Default())
	assert.Equal(t, []string{DefaultName}, r.Names())
}

func TestRegistry_UpdateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	r := LoadRegistry(dir)
	dirFor := filepath.Join(r.AccountsDir(), "max-2")
	require.NoError(t, r.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "max-2", Dir: dirFor})
		return nil
	}))
	require.NoError(t, r.SetDefault("max-2"))

	again := LoadRegistry(dir)
	require.NoError(t, again.LoadErr())
	assert.Equal(t, "max-2", again.Default())
	assert.Equal(t, []string{DefaultName, "max-2"}, again.Names())
	assert.Equal(t, map[string]string{"max-2": dirFor}, again.Dirs())
	env, err := again.Env("max-2")
	require.NoError(t, err)
	assert.Equal(t, []string{"CLAUDE_CONFIG_DIR=" + dirFor}, env)
}

func TestRegistry_UpdateMergesAConcurrentWriter(t *testing.T) {
	dir := t.TempDir()
	a, b := LoadRegistry(dir), LoadRegistry(dir)
	require.NoError(t, a.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "one", Dir: filepath.Join(a.AccountsDir(), "one")})
		return nil
	}))
	require.NoError(t, b.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "two", Dir: filepath.Join(b.AccountsDir(), "two")})
		return nil
	}))
	assert.Equal(t, []string{DefaultName, "one", "two"}, LoadRegistry(dir).Names())
}

func TestRegistry_CorruptFileLatchesWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))

	r := LoadRegistry(dir)
	require.Error(t, r.LoadErr())
	assert.False(t, r.HasExtra())
	assert.ErrorIs(t, r.SetDefault(DefaultName), ErrRegistryLoadFailed)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "not json", string(data), "a corrupt registry must never be overwritten")
}

func TestRegistry_DefaultFallsBackWhenUnregistered(t *testing.T) {
	r := &Registry{DefaultAccount: "gone"}
	assert.Equal(t, DefaultName, r.Default())
}

func TestRegistry_EnvForDefaultAndUnknown(t *testing.T) {
	r := &Registry{}
	env, err := r.Env(DefaultName)
	require.NoError(t, err)
	assert.Nil(t, env)
	env, err = r.Env("")
	require.NoError(t, err)
	assert.Nil(t, env)
	_, err = r.Env("nope")
	assert.Error(t, err)
}

func TestRegistry_SetDefaultRejectsUnknown(t *testing.T) {
	r := LoadRegistry(t.TempDir())
	assert.Error(t, r.SetDefault("nope"))
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"max-2", "work", "a1", "0", strings.Repeat("a", 32)} {
		assert.NoError(t, ValidName(ok), ok)
	}
	for _, bad := range []string{"", "default", "Max", "-x", "a/b", "a b", "a.b", "..", strings.Repeat("a", 33)} {
		assert.Error(t, ValidName(bad), bad)
	}
}

func TestUnavailableRefusesWrites(t *testing.T) {
	r := Unavailable(errors.New("no home"))
	require.Error(t, r.LoadErr())
	assert.ErrorIs(t, r.SetDefault(DefaultName), ErrRegistryLoadFailed)
}

func TestUnavailable_NilErrIsStillLatched(t *testing.T) {
	r := Unavailable(nil)
	require.Error(t, r.LoadErr())
	assert.ErrorIs(t, r.SetDefault(DefaultName), ErrRegistryLoadFailed)
}

func TestRegistry_Path(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, filepath.Join(dir, "accounts.json"), LoadRegistry(dir).Path())
	assert.Equal(t, "", Unavailable(nil).Path(), "no file to stat")
}

// TestUnavailable_ReloadKeepsItsOwnError: an Unavailable registry has no
// file to reload, but why it is unavailable is still the error to show.
func TestUnavailable_ReloadKeepsItsOwnError(t *testing.T) {
	orig := errors.New("no home directory")
	r := Unavailable(orig)

	err := r.Reload()

	assert.ErrorIs(t, err, orig)
	assert.ErrorIs(t, r.LoadErr(), orig)
	assert.ErrorIs(t, r.SetDefault(DefaultName), ErrRegistryLoadFailed, "still latched")
}

// TestLoadRegistry_RejectsInvalidStoredEntries covers both static shape
// checks (json, no filesystem needed) and cases needing real symlinks on
// disk (setup, given the case's own temp dir — used as the global dir —
// to build both the filesystem fixture and the accounts.json payload).
// The dotdot-through-a-symlink cases specifically exercise
// resolvedOrClean's not-yet-existing-path fallback: the escaped path's
// kernel target isn't fully resolvable (not yet created, a self-loop, or
// a dangling link), so the raw EvalSymlinks attempt fails and, before the
// a.Dir != filepath.Clean(a.Dir) check existed, the fallback's own Clean
// step silently cancelled the "lnk/.." pair, accepting the disguise.
func TestLoadRegistry_RejectsInvalidStoredEntries(t *testing.T) {
	cases := []struct {
		name  string
		json  string                                // used when setup is nil
		setup func(t *testing.T, dir string) string // returns the json payload; dir is the case's own temp dir
	}{
		{name: "invalid name", json: `{"accounts":[{"name":"Bad Name","dir":"/a"}]}`},
		{name: "reserved name", json: `{"accounts":[{"name":"default","dir":"/a"}]}`},
		{name: "duplicate name", json: `{"accounts":[{"name":"max-2","dir":"/a"},{"name":"max-2","dir":"/b"}]}`},
		{name: "relative dir", json: `{"accounts":[{"name":"max-2","dir":"a/b"}]}`},
		{name: "empty dir", json: `{"accounts":[{"name":"max-2","dir":""}]}`},
		{name: "foreign dir", json: `{"accounts":[{"name":"max-2","dir":"/definitely/not/loom-owned"}]}`},
		{name: "unclean dir (dot segment and trailing slash)", json: `{"accounts":[{"name":"max-2","dir":"/a/./max-2/"}]}`},
		{
			name: "dotdot through a symlink whose target does not exist yet",
			setup: func(t *testing.T, dir string) string {
				accountsDir := filepath.Join(dir, "accounts")
				require.NoError(t, os.MkdirAll(accountsDir, 0o755))
				target := t.TempDir() // real, but has no "max-2" entry (yet)
				require.NoError(t, os.Symlink(target, filepath.Join(accountsDir, "lnk")))
				escaped := filepath.Join(accountsDir, "lnk") + string(filepath.Separator) + ".." + string(filepath.Separator) + "max-2"
				return fmt.Sprintf(`{"accounts":[{"name":"max-2","dir":%q}]}`, escaped)
			},
		},
		{
			name: "dotdot through a symlink loop",
			setup: func(t *testing.T, dir string) string {
				accountsDir := filepath.Join(dir, "accounts")
				require.NoError(t, os.MkdirAll(accountsDir, 0o755))
				loop := filepath.Join(accountsDir, "loop")
				require.NoError(t, os.Symlink(loop, loop))
				escaped := loop + string(filepath.Separator) + ".." + string(filepath.Separator) + "max-2"
				return fmt.Sprintf(`{"accounts":[{"name":"max-2","dir":%q}]}`, escaped)
			},
		},
		{
			name: "dotdot through a dangling symlink",
			setup: func(t *testing.T, dir string) string {
				accountsDir := filepath.Join(dir, "accounts")
				require.NoError(t, os.MkdirAll(accountsDir, 0o755))
				dang := filepath.Join(accountsDir, "dang")
				require.NoError(t, os.Symlink(filepath.Join(dir, "nope-does-not-exist"), dang))
				escaped := dang + string(filepath.Separator) + ".." + string(filepath.Separator) + "max-2"
				return fmt.Sprintf(`{"accounts":[{"name":"max-2","dir":%q}]}`, escaped)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			payload := tc.json
			if tc.setup != nil {
				payload = tc.setup(t, dir)
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(payload), 0o644))

			r := LoadRegistry(dir)

			require.Error(t, r.LoadErr())
			assert.False(t, r.HasExtra())
			assert.ErrorIs(t, r.SetDefault(DefaultName), ErrRegistryLoadFailed)
		})
	}
}

// TestLoadRegistry_ToleratesADifferentlySpelledGlobalDirAcrossRuns: an
// account's stored Dir was written under one spelling of the global dir
// (here, reached through a symlink); a later LoadRegistry call given a
// differently-spelled but equivalent global dir (the symlink's resolved
// target) must not latch the registry just because the two spellings
// differ as strings — an exact byte comparison would refuse every launch
// after a respelled LOOM_GLOBAL_DIR or a symlinked $HOME, until someone
// hand-edited accounts.json back into agreement. And once accepted, the
// account's Dir is canonicalized to *this* load's own spelling — never
// left as whatever was on disk — so a later operation that reuses the
// in-memory value without reloading (a plain resume, a crash restart)
// never depends on a symlinked prefix still resolving the same way.
func TestLoadRegistry_ToleratesADifferentlySpelledGlobalDirAcrossRuns(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(real, link))

	// Written under the symlinked spelling.
	r := LoadRegistry(link)
	main := mainDirWith(t)
	acct, _, err := r.Create("max-2", main)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(link, "accounts", "max-2"), acct.Dir, "stored exactly as written")

	// Loaded again under the resolved spelling.
	again := LoadRegistry(real)

	require.NoError(t, again.LoadErr())
	got, ok := again.Get("max-2")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(real, "accounts", "max-2"), got.Dir,
		"canonicalized to this load's own spelling, not whatever was stored")
}

// TestRegistry_UpdatePersistsTheCanonicalSpelling: update() already
// rewrites the whole file on every call (marshal the reloaded-and-mutated
// copy, atomic write) — canonicalizeDirs adds no new write path, it only
// changes what ends up in that one existing rewrite. An update for an
// unrelated reason (SetDefault, here) on a registry loaded through a
// differently-spelled global dir persists the *canonical* spelling to
// disk, self-healing the drift rather than preserving the old one.
func TestRegistry_UpdatePersistsTheCanonicalSpelling(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(real, link))

	r := LoadRegistry(link)
	main := mainDirWith(t)
	_, _, err := r.Create("max-2", main)
	require.NoError(t, err)

	again := LoadRegistry(real)
	require.NoError(t, again.LoadErr())
	require.NoError(t, again.SetDefault("max-2"))

	data, err := os.ReadFile(filepath.Join(real, "accounts.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), filepath.Join(real, "accounts", "max-2"))
	assert.NotContains(t, string(data), link, "the old symlinked spelling must not remain on disk after a write")
}

// TestLoadRegistry_RejectsALnkDotDotDisguisedDir keeps the Critical fix
// (OwnedDir returning the canonical path, never the stored one — see
// link.go) backed up at load time too: a stored dir like
// "<AccountsDir>/lnk/../name", where "lnk" is a real symlink to somewhere
// else, must still latch the registry even now that dir comparison
// tolerates symlinked *ancestors* of an equivalent spelling — this one
// resolves to a genuinely different, real location, not an equivalent one.
func TestLoadRegistry_RejectsALnkDotDotDisguisedDir(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "accounts")
	require.NoError(t, os.MkdirAll(accountsDir, 0o755))
	victim := t.TempDir()
	sub := filepath.Join(victim, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(victim, "max-2"), 0o755))
	require.NoError(t, os.Symlink(sub, filepath.Join(accountsDir, "lnk")))
	escaped := filepath.Join(accountsDir, "lnk") + string(filepath.Separator) + ".." + string(filepath.Separator) + "max-2"
	data := fmt.Sprintf(`{"accounts":[{"name":"max-2","dir":%q}]}`, escaped)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(data), 0o644))

	r := LoadRegistry(dir)

	require.Error(t, r.LoadErr())
	assert.False(t, r.HasExtra())
}

func TestRegistry_ReloadPicksUpAConcurrentWrite(t *testing.T) {
	dir := t.TempDir()
	a, b := LoadRegistry(dir), LoadRegistry(dir)
	require.NoError(t, b.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "max-2", Dir: filepath.Join(b.AccountsDir(), "max-2")})
		return nil
	}))
	assert.Equal(t, []string{DefaultName}, a.Names(), "a hasn't reloaded yet")

	require.NoError(t, a.Reload())

	assert.Equal(t, []string{DefaultName, "max-2"}, a.Names())
}

func TestRegistry_ReloadLatchesOnACorruptFile(t *testing.T) {
	dir := t.TempDir()
	r := LoadRegistry(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "accounts.json"), []byte("not json"), 0o644))

	err := r.Reload()

	require.Error(t, err)
	assert.Equal(t, err, r.LoadErr())
	assert.ErrorIs(t, r.SetDefault(DefaultName), ErrRegistryLoadFailed)
}
