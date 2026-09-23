package subagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_TeammateLifecycle repeats the 2026-09-16 probe against a
// real interactive Claude session on a private tmux server. It spends a
// few cents of haiku usage and leaves entries in ~/.claude/projects and
// ~/.claude/teams, so it only runs with LOOM_TEST_REAL_CLAUDE=1 and never
// in CI. It covers the parts of the hook contract that were observed
// rather than documented: background_tasks and the teammate event order.
func TestRealClaude_TeammateLifecycle(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"tmux", "claude", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}

	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks")
	launchID, err := hooks.Prepare(hooksDir)
	require.NoError(t, err)
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))

	sock := fmt.Sprintf("loomsub-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-L", sock}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })
	screen := func() string { return tm("capture-pane", "-p", "-t", "probe") }
	send := func(text string) {
		tm("send-keys", "-t", "probe", "-l", text)
		time.Sleep(300 * time.Millisecond)
		tm("send-keys", "-t", "probe", "Enter")
	}

	tm("new-session", "-d", "-s", "probe", "-x", "160", "-y", "45", "-c", work)
	tm("send-keys", "-t", "probe", fmt.Sprintf("claude --model haiku --settings '%s'", hooks.SettingsPath(hooksDir)), "Enter")

	// Wait for Claude itself, not a "❯" that a shell prompt may also print.
	const trustDialog = "trust this folder"
	waitFor(t, 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, trustDialog) || strings.Contains(s, "Claude Code")
	})
	if s := screen(); strings.Contains(s, trustDialog) {
		// "No, exit" is always listed; it is the default only for some
		// folders (those under a temp dir, for one).
		if strings.Contains(s, "❯ No, exit") {
			tm("send-keys", "-t", "probe", "Down")
		}
		tm("send-keys", "-t", "probe", "Enter")
	}
	waitFor(t, 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, "Claude Code") && !strings.Contains(s, trustDialog)
	})
	time.Sleep(2 * time.Second) // let the input box take focus

	tracker := NewTracker()
	cold := true
	var seen []hooks.Event
	collect := func() {
		res, err := hooks.Scan(hooks.Request{Dir: hooksDir, Cold: cold}, time.Now())
		require.NoError(t, err)
		require.Equal(t, launchID, res.LaunchID)
		if res.Replayed {
			tracker.Reset()
		}
		cold = false
		tracker.Apply(res.Events, ReadMeta(res.Events, tracker.MissingMeta()))
		seen = append(seen, res.Events...)
	}

	send("Automated test. Use the Agent tool exactly once with name 'probe-mate', " +
		"subagent_type general-purpose, description 'probe teammate', " +
		"prompt 'Reply with the word pong. Use no tools.' Then wait for its reply and say DONE.")
	waitFor(t, 3*time.Minute, func() bool {
		collect()
		return idleThenStop(seen, "probe-mate")
	})
	requireOrder(t, seen, hooks.EventSubagentStart, hooks.EventSubagentStop, hooks.EventTeammateIdle, hooks.EventStop)
	waitFor(t, 30*time.Second, func() bool { collect(); return len(tracker.Visible()) == 1 })
	require.Equal(t, []View{{Name: "probe-mate", Description: "probe teammate", Idle: true}}, tracker.Visible())

	before := len(seen)
	send("Send probe-mate a shutdown request with SendMessage, wait until it has terminated, then say ALLDONE.")
	waitFor(t, 3*time.Minute, func() bool {
		collect()
		return len(tracker.Visible()) == 0
	})
	requireShutdownSequence(t, seen[before:], "probe-mate")
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// idleThenStop reports whether a TeammateIdle for name is followed by a
// parent Stop.
func idleThenStop(events []hooks.Event, name string) bool {
	idle := false
	for _, e := range events {
		if e.Name == hooks.EventTeammateIdle && e.TeammateName == name {
			idle = true
		}
		if idle && e.Name == hooks.EventStop {
			return true
		}
	}
	return false
}

// requireOrder asserts want appears in events as a subsequence.
func requireOrder(t *testing.T, events []hooks.Event, want ...string) {
	t.Helper()
	i := 0
	for _, e := range events {
		if i < len(want) && e.Name == want[i] {
			i++
		}
	}
	got := make([]string, len(events))
	for j, e := range events {
		got[j] = e.Name
	}
	require.Equal(t, len(want), i, "event order %v does not contain %v in order", got, want)
}

// requireShutdownSequence asserts finding 4 of the design: the teammate is
// started to handle the shutdown request and stopped, it does not go idle
// after that final stop, and a later parent Stop lists no running
// teammates. A teammate's agent_type is its name.
func requireShutdownSequence(t *testing.T, events []hooks.Event, name string) {
	t.Helper()
	started, lastStop := false, -1
	for i, e := range events {
		if e.Name == hooks.EventSubagentStart && e.AgentType == name {
			started = true
		}
		if started && e.Name == hooks.EventSubagentStop && e.AgentType == name {
			lastStop = i
		}
	}
	require.GreaterOrEqual(t, lastStop, 0,
		"shutdown: no SubagentStart followed by SubagentStop for %s", name)

	emptied := false
	for _, e := range events[lastStop+1:] {
		require.False(t, e.Name == hooks.EventTeammateIdle && e.TeammateName == name,
			"shutdown: %s went idle after its final stop", name)
		if e.Name == hooks.EventStop && e.HasTasks && runningTeammates(e.Tasks) == 0 {
			emptied = true
		}
	}
	require.True(t, emptied,
		"shutdown: no parent Stop listed zero running teammates after %s stopped", name)
}

// runningTeammates counts the live teammate entries in a background_tasks list.
func runningTeammates(tasks []hooks.Task) int {
	n := 0
	for _, task := range tasks {
		if task.Type == "teammate" && task.Status == "running" {
			n++
		}
	}
	return n
}
