package mockup

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealCompanion_FindAndFetch starts the brainstorming skill's real
// companion on a temp project, as the skill does (start-server.sh
// --project-dir), and holds Find and Fetch to what it writes and serves:
// its session dir and server-info, its page's type and marker, the waiting
// screen, and stop-server.sh's stop. The companion is superpowers' and
// changes with it, so this is the reader's guard. It needs node and runs
// only with LOOM_TEST_REAL_COMPANION set to the skill's scripts dir
// (…/skills/brainstorming/scripts).
func TestRealCompanion_FindAndFetch(t *testing.T) {
	scripts := os.Getenv("LOOM_TEST_REAL_COMPANION")
	if scripts == "" {
		t.Skip("set LOOM_TEST_REAL_COMPANION to the brainstorming skill's scripts dir")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	tree := t.TempDir()
	out, err := exec.Command(filepath.Join(scripts, "start-server.sh"), "--project-dir", tree, "--background").Output()
	require.NoError(t, err, "start-server.sh: %s", out)
	var started struct {
		Port      int    `json:"port"`
		ScreenDir string `json:"screen_dir"`
	}
	require.NoError(t, json.Unmarshal(out, &started), "start-server.sh said: %s", out)
	session := filepath.Dir(started.ScreenDir)
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = exec.Command(filepath.Join(scripts, "stop-server.sh"), session).Run()
		}
	}
	t.Cleanup(stop)

	require.NoError(t, os.WriteFile(filepath.Join(started.ScreenDir, "layout.html"), []byte(`<h2>Which layout works better?</h2>`), 0o644))
	var c Companion
	require.Eventually(t, func() bool {
		var ok bool
		c, ok, err = Find(tree)
		return err == nil && ok
	}, 5*time.Second, 50*time.Millisecond, "Find never saw the companion")
	assert.Equal(t, started.Port, c.Port)
	assert.Equal(t, "layout.html", c.Screen)

	page, err := Fetch(context.Background(), c.Port)
	require.NoError(t, err)
	assert.Contains(t, page, "Which layout works better?", "the fragment, wrapped in the companion's frame")

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(started.ScreenDir, "waiting.html"), []byte(`<p class="subtitle">Continuing in terminal...</p>`), 0o644))
	_, ok, err := Find(tree)
	require.NoError(t, err)
	assert.False(t, ok, "the waiting screen is no mockup")

	stop()
	_, ok, err = Find(tree)
	require.NoError(t, err)
	assert.False(t, ok, "stop-server.sh's stop is seen")
	_, err = Fetch(context.Background(), started.Port)
	assert.ErrorIs(t, err, ErrNotRunning)
}
