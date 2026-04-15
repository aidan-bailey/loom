package daemon

import (
	"claude-squad/config"
	"claude-squad/session"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestReloadInstances_SeesFreshInstancesOnDisk verifies that
// reloadInstances picks up new entries written to state.json after
// startup (addresses DAEMON-03).
func TestReloadInstances_SeesFreshInstancesOnDisk(t *testing.T) {
	dir := t.TempDir()
	writeStateJSON(t, dir, []string{"alpha"})

	insts, err := reloadInstances(dir)
	assert.NoError(t, err)
	assert.Len(t, insts, 1)
	assert.Equal(t, "alpha", insts[0].Title)

	writeStateJSON(t, dir, []string{"alpha", "beta"})

	insts, err = reloadInstances(dir)
	assert.NoError(t, err)
	assert.Len(t, insts, 2)
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
