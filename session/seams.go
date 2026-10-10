package session

import (
	"fmt"

	"github.com/aidan-bailey/loom/session/tmux"
)

// Test seams. Tests in this package and the ones above it (core, app, ui)
// reach instance and storage state through these where no production path
// does. Production code never calls them (TestNoProductionCallsOfTestSeams).

// SetTmuxSessionForTest sets the instance's tmux session, so a test can run
// the lifecycle and pane paths over a mock executor.
func (i *Instance) SetTmuxSessionForTest(session *tmux.Session) {
	i.setTmuxSession(session)
}

// RestartFailureCountForTest reports the current consecutive-failure count
// of the workspace terminal's restart breaker (RecordRestartFailure).
func (i *Instance) RestartFailureCountForTest() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.restartFailureCount
}

// TmuxAliveForTest reports whether the tmux session answers a has-session
// probe, so a test can check what its fixture's mock executor says.
// Production code asks TmuxLiveness, which tells "no" from "no answer".
func (p AgentPane) TmuxAliveForTest() bool {
	ts := p.i.getTmuxSession()
	if ts == nil {
		return false
	}
	return ts.DoesSessionExist()
}

// LoadInstanceDataForTest loads the raw serialized instance data, as the
// write paths see it: every record that decodes, migrated to
// CurrentSchemaVersion, and none that does not (those are preserved on the
// next write).
func (s *Storage) LoadInstanceDataForTest() ([]InstanceData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadInstanceDataLocked()
}

// UpdateInstanceForTest replaces the persisted record for an existing
// instance with its in-memory snapshot, through the raw-data write path
// DeleteInstance takes, so the other stored entries are never
// reconstructed.
func (s *Storage) UpdateInstanceForTest(instance *Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadForWriteLocked()
	if err != nil {
		return err
	}

	snap := instance.ToInstanceData()
	found := false
	for i, d := range data {
		if d.Title == snap.Title {
			data[i] = snap
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("%w: %s", ErrInstanceNotFound, snap.Title)
	}

	return s.writeLocked(data)
}
