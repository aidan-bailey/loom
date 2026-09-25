package account

import (
	"errors"
	"os"
	"path/filepath"
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
	require.NoError(t, r.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "max-2", Dir: "/a/max-2"})
		return nil
	}))
	require.NoError(t, r.SetDefault("max-2"))

	again := LoadRegistry(dir)
	require.NoError(t, again.LoadErr())
	assert.Equal(t, "max-2", again.Default())
	assert.Equal(t, []string{DefaultName, "max-2"}, again.Names())
	assert.Equal(t, map[string]string{"max-2": "/a/max-2"}, again.Dirs())
	env, err := again.Env("max-2")
	require.NoError(t, err)
	assert.Equal(t, []string{"CLAUDE_CONFIG_DIR=/a/max-2"}, env)
}

func TestRegistry_UpdateMergesAConcurrentWriter(t *testing.T) {
	dir := t.TempDir()
	a, b := LoadRegistry(dir), LoadRegistry(dir)
	require.NoError(t, a.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "one", Dir: "/1"})
		return nil
	}))
	require.NoError(t, b.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "two", Dir: "/2"})
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
	for _, ok := range []string{"max-2", "work", "a1", "0"} {
		assert.NoError(t, ValidName(ok), ok)
	}
	for _, bad := range []string{"", "default", "Max", "-x", "a/b", "a b", "a.b", ".."} {
		assert.Error(t, ValidName(bad), bad)
	}
}

func TestUnavailableRefusesWrites(t *testing.T) {
	r := Unavailable(errors.New("no home"))
	require.Error(t, r.LoadErr())
	assert.ErrorIs(t, r.SetDefault(DefaultName), ErrRegistryLoadFailed)
}
