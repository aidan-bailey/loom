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
