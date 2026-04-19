package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadStateFrom_MissingFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()

	state := LoadStateFrom(dir)
	assert.NotNil(t, state)
	assert.Equal(t, uint32(0), state.HelpScreensSeen)
	assert.Equal(t, json.RawMessage("[]"), state.InstancesData)
}

func TestSaveStateTo_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &State{
		HelpScreensSeen: 7,
		InstancesData:   json.RawMessage(`[{"title":"t"}]`),
	}
	assert.NoError(t, SaveStateTo(original, dir))

	assert.FileExists(t, filepath.Join(dir, StateFileName))

	loaded := LoadStateFrom(dir)
	assert.Equal(t, original.HelpScreensSeen, loaded.HelpScreensSeen)
	assert.JSONEq(t, string(original.InstancesData), string(loaded.InstancesData))
}

func TestLoadStateFrom_CorruptFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, StateFileName), []byte("not-json"), 0644))

	state := LoadStateFrom(dir)
	assert.NotNil(t, state)
	assert.Equal(t, uint32(0), state.HelpScreensSeen)
	assert.Equal(t, json.RawMessage("[]"), state.InstancesData)
}

// TestLoadStateFrom_CorruptFileQuarantined drives the F8 policy:
// a corrupt state.json is renamed to state.json.corrupted-<timestamp>
// so it is preserved for post-mortem and cannot silently round-trip
// as defaults-overwriting-real-state on the next save. Before this
// fix the corrupt file sat in place and the user saw an empty
// instance list — masking the real issue while potentially orphaning
// on-disk tmux sessions and worktrees.
func TestLoadStateFrom_CorruptFileQuarantined(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, StateFileName)
	assert.NoError(t, os.WriteFile(statePath, []byte("not-json"), 0644))

	state := LoadStateFrom(dir)
	assert.NotNil(t, state, "defaults must still be returned on corrupt file")

	// The original path must no longer hold the corrupt bytes — either
	// it was removed, or the helper moved it aside.
	_, err := os.Stat(statePath)
	assert.True(t, os.IsNotExist(err), "corrupt state.json must be moved aside")

	// A sibling file named state.json.corrupted-* must now exist and
	// hold the corrupt bytes so an operator can debug.
	entries, err := os.ReadDir(dir)
	assert.NoError(t, err)
	var quarantined string
	for _, e := range entries {
		if len(e.Name()) > len(StateFileName) &&
			e.Name()[:len(StateFileName)+len(".corrupted-")] == StateFileName+".corrupted-" {
			quarantined = filepath.Join(dir, e.Name())
			break
		}
	}
	assert.NotEmpty(t, quarantined, "quarantined corrupt file must exist with .corrupted-<ts> suffix")
	if quarantined != "" {
		body, err := os.ReadFile(quarantined)
		assert.NoError(t, err)
		assert.Equal(t, "not-json", string(body), "quarantined file must preserve original bytes")
	}
}

func TestSaveStateTo_CreatesMissingDirectory(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "a", "b")

	assert.NoError(t, SaveStateTo(DefaultState(), nested))
	assert.FileExists(t, filepath.Join(nested, StateFileName))
}

// TestState_SaveInstancesRoundTrips exercises the InstanceStorage
// implementation: LoadStateFrom remembers the directory, so SaveInstances
// writes back to the same place.
func TestState_SaveInstancesRoundTrips(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, SaveStateTo(DefaultState(), dir))

	state := LoadStateFrom(dir)
	assert.NoError(t, state.SaveInstances(json.RawMessage(`[{"title":"x"}]`)))

	reloaded := LoadStateFrom(dir)
	assert.JSONEq(t, `[{"title":"x"}]`, string(reloaded.InstancesData))
}

func TestState_SetHelpScreensSeenPersists(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, SaveStateTo(DefaultState(), dir))

	state := LoadStateFrom(dir)
	assert.NoError(t, state.SetHelpScreensSeen(42))

	reloaded := LoadStateFrom(dir)
	assert.Equal(t, uint32(42), reloaded.HelpScreensSeen)
}

// TestSaveStateTo_SkipsWriteWhenUnchanged verifies the byte-comparison
// short-circuit: identical back-to-back saves must not rewrite the file
// (AtomicWriteFile renames through a new inode, so we detect writes via
// os.SameFile). A state mutation in between must still trigger a write.
func TestSaveStateTo_SkipsWriteWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	state := &State{HelpScreensSeen: 1, InstancesData: json.RawMessage(`[]`)}

	require := func(cond bool, msg string) {
		t.Helper()
		if !cond {
			t.Fatal(msg)
		}
	}

	assert.NoError(t, SaveStateTo(state, dir))
	path := filepath.Join(dir, StateFileName)
	fi1, err := os.Stat(path)
	assert.NoError(t, err)

	assert.NoError(t, SaveStateTo(state, dir))
	fi2, err := os.Stat(path)
	assert.NoError(t, err)
	require(os.SameFile(fi1, fi2), "identical save must not rewrite the file")

	state.HelpScreensSeen = 2
	assert.NoError(t, SaveStateTo(state, dir))
	fi3, err := os.Stat(path)
	assert.NoError(t, err)
	require(!os.SameFile(fi2, fi3), "mutated save must rewrite the file")
}
