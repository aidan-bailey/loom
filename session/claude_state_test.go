package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var t0 = time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)

func hookObs(st Status, reason string, dt time.Duration) observation {
	return observation{status: st, reason: reason, at: t0.Add(dt), source: obsHook, valid: true}
}

func rosterObs(st Status, reason string, dt time.Duration) observation {
	return observation{status: st, reason: reason, at: t0.Add(dt), source: obsRoster, valid: true}
}

// describe renders a state's status for table assertions: "-" for no
// opinion, else the status and any wait reason.
func describe(s claudeState) string {
	st, reason, ok := s.status()
	if !ok {
		return "-"
	}
	if reason != "" {
		return st.String() + ": " + reason
	}
	return st.String()
}

func TestClaudeState_NewestWinsAcrossSources(t *testing.T) {
	var s claudeState
	assert.True(t, s.offer(hookObs(Ready, "", 0)))
	assert.True(t, s.offer(rosterObs(Running, "", time.Second)), "a newer roster answer replaces a hook observation")
	assert.True(t, s.offer(hookObs(Ready, "", 2*time.Second)), "a newer hook event replaces a roster answer")
	assert.False(t, s.offer(rosterObs(Running, "", 1500*time.Millisecond)), "an older roster answer delivered late is dropped")
	assert.Equal(t, "Ready", describe(s))
}

func TestClaudeState_EqualTimeIsNotNewer(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Ready, "", 0))
	assert.False(t, s.offer(rosterObs(Running, "", 0)))
	assert.Equal(t, "Ready", describe(s))
}

func TestClaudeState_SameStatusIsNotAChange(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Running, "", 0))
	assert.False(t, s.offer(rosterObs(Running, "", time.Second)))
	assert.Equal(t, obsRoster, s.obs.source, "the newer observation is still stored")
}

func TestClaudeState_RosterKeepsHookReason(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Prompting, "permission: Bash", 0))
	assert.False(t, s.offer(rosterObs(Prompting, "permission prompt", time.Second)))
	assert.Equal(t, "Prompting: permission: Bash", describe(s), "the hook names the tool, the roster only the kind of wait")

	s.offer(rosterObs(Running, "", 2*time.Second))
	s.offer(rosterObs(Prompting, "sandbox request", 3*time.Second))
	assert.Equal(t, "Prompting: sandbox request", describe(s), "with no hook observation the roster's reason stands")
}

func TestClaudeState_NoOpinionStillOrdersByTime(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Ready, "", 0))
	assert.True(t, s.offer(observation{at: t0.Add(time.Second), source: obsHook}), "SessionEnd: no opinion")
	assert.Equal(t, "-", describe(s))
	assert.False(t, s.offer(rosterObs(Running, "", 500*time.Millisecond)),
		"an answer from before the SessionEnd must not revive a status")
}

func TestClaudeState_RosterSilence(t *testing.T) {
	var s claudeState
	s.offer(rosterObs(Running, "", 0))
	assert.False(t, s.rosterSilent(t0.Add(-time.Second)), "an older silence changes nothing")
	assert.True(t, s.rosterSilent(t0.Add(time.Second)))
	assert.Equal(t, "-", describe(s), "a roster status must not outlive the roster")

	s.offer(hookObs(Ready, "", 2*time.Second))
	assert.False(t, s.rosterSilent(t0.Add(3*time.Second)), "silence never voids a hook observation")
	assert.Equal(t, "Ready", describe(s))
}
