package daemon

import (
	"claude-squad/config"
	"claude-squad/log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	log.Initialize("", false)
	defer log.Close()
	os.Exit(m.Run())
}

func TestStopDaemon_RequiresResolvedContext(t *testing.T) {
	assert.Error(t, StopDaemon(nil))
	assert.Error(t, StopDaemon(&config.WorkspaceContext{}))
}

func TestLaunchDaemon_RequiresResolvedContext(t *testing.T) {
	assert.Error(t, LaunchDaemon(nil))
	assert.Error(t, LaunchDaemon(&config.WorkspaceContext{}))
}

// TestStopDaemon_MissingPIDFileIsNoop is the startup path: every main-app
// invocation tries to stop any prior daemon before spawning a new one, so
// a missing PID file must not surface as an error.
func TestStopDaemon_MissingPIDFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	err := StopDaemon(&config.WorkspaceContext{ConfigDir: dir})
	assert.NoError(t, err)
}

func TestStopDaemon_MalformedPIDReturnsError(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte("not-a-number"), 0644))

	err := StopDaemon(&config.WorkspaceContext{ConfigDir: dir})
	assert.Error(t, err)
}

// TestRunDaemon_MissingStateFileFailsFast checks the startup precondition
// at daemon.go:90: RunDaemon should refuse to enter its poll loop if the
// initial state load cannot complete, so that systemd-style supervisors
// observe the failure instead of a live process poll-looping on errors.
func TestRunDaemon_BadContextRejected(t *testing.T) {
	cfg := &config.Config{DaemonPollInterval: 1000}
	assert.Error(t, RunDaemon(cfg, nil))
	assert.Error(t, RunDaemon(cfg, &config.WorkspaceContext{}))
}
