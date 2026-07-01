package daemon

import (
	"encoding/json"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReloadInstances_SeesFreshInstancesOnDisk verifies that
// reloadInstanceData picks up new entries written to state.json after
// startup (addresses DAEMON-03).
func TestReloadInstances_SeesFreshInstancesOnDisk(t *testing.T) {
	dir := t.TempDir()
	writeStateJSON(t, dir, []string{"alpha"})

	data, err := reloadInstanceData(dir)
	assert.NoError(t, err)
	assert.Len(t, data, 1)
	assert.Equal(t, "alpha", data[0].Title)

	writeStateJSON(t, dir, []string{"alpha", "beta"})

	data, err = reloadInstanceData(dir)
	assert.NoError(t, err)
	assert.Len(t, data, 2)
}

// writeStateJSON writes a state.json containing instances with the given
// titles. Instances are marked Paused + IsWorkspaceTerminal so
// FromInstanceData skips both the Start() path and the git worktree
// branch — the test cares only about reload picking up changes.
func writeStateJSON(t *testing.T, dir string, titles []string) {
	t.Helper()

	instances := make([]session.InstanceData, len(titles))
	for i, title := range titles {
		instances[i] = session.InstanceData{
			Title:               title,
			Status:              session.Paused,
			IsWorkspaceTerminal: true,
			Program:             "true",
		}
	}
	instancesJSON, err := json.Marshal(instances)
	if err != nil {
		t.Fatalf("marshal instances: %v", err)
	}

	state := struct {
		HelpScreensSeen uint32          `json:"help_screens_seen"`
		InstancesData   json.RawMessage `json:"instances"`
	}{
		HelpScreensSeen: 0,
		InstancesData:   instancesJSON,
	}
	stateJSON, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := config.AtomicWriteFile(filepath.Join(dir, config.StateFileName), stateJSON, 0644); err != nil {
		t.Fatalf("write state.json: %v", err)
	}
}

func TestRefreshPollInterval_PicksUpChange(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DaemonPollInterval = 1000
	require.NoError(t, config.SaveConfigTo(cfg, dir))

	got := refreshPollInterval(dir, 999*time.Millisecond)
	assert.Equal(t, 1000*time.Millisecond, got)

	cfg.DaemonPollInterval = 2500
	require.NoError(t, config.SaveConfigTo(cfg, dir))

	got = refreshPollInterval(dir, 1000*time.Millisecond)
	assert.Equal(t, 2500*time.Millisecond, got)
}

func TestRefreshPollInterval_FallsBackOnMissingConfig(t *testing.T) {
	dir := t.TempDir() // no config.json written
	got := refreshPollInterval(dir, 1500*time.Millisecond)
	// LoadConfigFrom falls back to DefaultConfig() (1000ms) when no file
	// exists, so the fallback path here is really "non-positive value",
	// not "missing file" — assert the actual DefaultConfig behavior.
	assert.Equal(t, time.Duration(config.DefaultConfig().DaemonPollInterval)*time.Millisecond, got)
}
