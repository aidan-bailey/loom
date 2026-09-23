package tmux

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTmuxNames_MapTargetSeparators pins that no name loom gives a session
// holds ':' or '.'. tmux creates "loom_fix:login" literally, but every
// target naming it parses ':' as the session/window separator (and '.' as
// window/pane), so the session could never be probed, attached or killed.
func TestTmuxNames_MapTargetSeparators(t *testing.T) {
	for title, want := range map[string]string{
		"plain":       "plain",
		"fix: login":  "fix_login",
		"feat:0":      "feat_0",
		"a.b":         "a_b",
		"v1.2:rc 3":   "v1_2_rc3",
		"api-v2":      "api-v2",
		"\tspaced  x": "spacedx",
	} {
		assert.Equal(t, TmuxPrefix+want, ToLoomTmuxName(title), title)
		assert.Equal(t, LegacyTmuxPrefix+want, ToLegacyTmuxName(title), title)
		assert.False(t, strings.ContainsAny(ToLoomTmuxName(title), ":."), title)
	}
}

// TestColonTitle_StartsProbesAliveAndCloses_RealTmux is the end-to-end
// regression for a title like "fix: login". Unmapped, its session was
// created as loom_fix:login, but has-session -t=loom_fix:login asks for
// window "login" of "loom_fix": Start probed Dead until it timed out, the
// cleanup Close could not kill it, and the caller then removed the
// worktree under the running agent.
func TestColonTitle_StartsProbesAliveAndCloses_RealTmux(t *testing.T) {
	privateTmux(t, "n")
	ts := NewTmuxSession("fix: login", "sh")

	start := time.Now()
	require.NoError(t, ts.Start(t.TempDir()))
	assert.Less(t, time.Since(start), 2*time.Second, "Start must see the session at once, not time out")
	assert.Equal(t, LivenessAlive, ts.SessionLiveness())
	assert.Equal(t, []string{ToLoomTmuxName("fix: login")}, serverSessions(t))

	require.NoError(t, ts.Close())
	assert.Equal(t, LivenessDead, ts.SessionLiveness())
	assert.Empty(t, serverSessions(t), "Close must kill the session it started")
}
