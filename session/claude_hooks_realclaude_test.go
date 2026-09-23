package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_HookStatusContract repeats the core of the 2026-09-23
// probe (docs/superpowers/specs/2026-09-23-claude-hook-events-design.md)
// against a real interactive Claude session on a private tmux server. It
// pins the payload fields the status mapping reads, and the ordering the
// newest-wins merge relies on: the roster has already moved when a hook
// is stamped. It spends a few cents of haiku usage and leaves entries in
// ~/.claude/projects, so it only runs with LOOM_TEST_REAL_CLAUDE=1 and
// never in CI.
func TestRealClaude_HookStatusContract(t *testing.T) {
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
	cwd, err := filepath.EvalSymlinks(work) // the roster reports a resolved cwd
	require.NoError(t, err)

	ctx := context.Background()
	sock := fmt.Sprintf("loomhooks-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		out, err := tmux.CommandOnSocket(ctx, sock, args...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	t.Cleanup(func() { _ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run() })
	target := tmux.PaneTarget("probe")
	screen := func() string { return tm("capture-pane", "-p", "-t", target) }
	send := func(text string) {
		tm("send-keys", "-t", target, "-l", text)
		time.Sleep(300 * time.Millisecond)
		tm("send-keys", "-t", target, "Enter")
	}

	tm("new-session", "-d", "-s", "probe", "-x", "160", "-y", "45", "-c", work)
	tm("send-keys", "-t", target, fmt.Sprintf("claude --model haiku --settings '%s'", hooks.SettingsPath(hooksDir)), "Enter")
	const trustDialog = "trust this folder"
	waitForRealClaude(t, screen, 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, trustDialog) || strings.Contains(s, "Claude Code")
	})
	if s := screen(); strings.Contains(s, trustDialog) {
		// "No, exit" is the default for folders under a temp dir.
		if strings.Contains(s, "❯ No, exit") {
			tm("send-keys", "-t", target, "Down")
		}
		tm("send-keys", "-t", target, "Enter")
	}
	waitForRealClaude(t, screen, 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, "Claude Code") && !strings.Contains(s, trustDialog)
	})
	time.Sleep(2 * time.Second) // let the input box take focus

	// Sample the roster continuously. A sample is stamped with its start,
	// as loom stamps a query, and also records its end: the answer may
	// reflect any moment in between.
	type rosterSample struct {
		at, end time.Time
		status  RosterStatus
		listed  bool
	}
	var (
		mu      sync.Mutex
		samples []rosterSample
		wg      sync.WaitGroup
	)
	quit := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-quit:
				return
			default:
			}
			at := time.Now()
			entries, err := QueryClaudeRoster("claude", internalexec.Default{})
			end := time.Now()
			e, ok := entries[cwd]
			mu.Lock()
			samples = append(samples, rosterSample{at: at, end: end, status: e.Status, listed: err == nil && ok})
			mu.Unlock()
		}
	}()
	stopPoller := sync.OnceFunc(func() { close(quit); wg.Wait() })
	t.Cleanup(stopPoller)

	var seen []hooks.Event
	cold := true
	collect := func() {
		res, err := hooks.Scan(hooks.Request{Dir: hooksDir, Cold: cold}, time.Now())
		require.NoError(t, err)
		require.Equal(t, launchID, res.LaunchID)
		if res.Replayed {
			seen = nil
		}
		cold = false
		seen = append(seen, res.Events...)
	}
	parent := func(name string) []hooks.Event {
		var out []hooks.Event
		for _, e := range seen {
			if e.Name == name && e.AgentID == "" {
				out = append(out, e)
			}
		}
		return out
	}

	// Startup names the conversation.
	waitForRealClaude(t, screen, 30*time.Second, func() bool { collect(); return len(parent(hooks.EventSessionStart)) == 1 })
	start := parent(hooks.EventSessionStart)[0]
	require.Equal(t, "startup", start.Source)
	require.NotEmpty(t, start.SessionID)

	// A plain turn: UserPromptSubmit, then a Stop carrying the reply.
	send("Automated test. Reply with exactly the word PONG and nothing else. Use no tools.")
	waitForRealClaude(t, screen, 2*time.Minute, func() bool { collect(); return len(parent(hooks.EventStop)) == 1 })
	firstStop := parent(hooks.EventStop)[0]
	require.Contains(t, firstStop.LastAssistantMessage, "PONG")
	require.Len(t, parent(hooks.EventUserPromptSubmit), 1)
	// Claude writes the transcript with the first message, not at startup;
	// BuildResumeCommand falls back to --continue until it exists.
	require.FileExists(t, start.TranscriptPath)
	time.Sleep(time.Second) // let the roster poller sample the idle session

	// A permission prompt, answered.
	secondPrompt := time.Now()
	// A command that writes a file: Claude runs read-only ones such as
	// `true` without asking.
	send("Automated test. Use the Bash tool exactly once to run: date > probe.txt  Then reply DONE.")
	waitForRealClaude(t, screen, 2*time.Minute, func() bool { collect(); return len(parent(hooks.EventPermissionRequest)) == 1 })
	perm := parent(hooks.EventPermissionRequest)[0]
	require.Equal(t, "Bash", perm.ToolName)
	time.Sleep(time.Second) // let the roster poller sample the wait
	approved := time.Now()
	tm("send-keys", "-t", target, "Enter")
	waitForRealClaude(t, screen, 2*time.Minute, func() bool { collect(); return len(parent(hooks.EventStop)) == 2 })

	// /clear ends the conversation and starts a new one.
	send("/clear")
	waitForRealClaude(t, screen, 30*time.Second, func() bool { collect(); return len(parent(hooks.EventSessionStart)) == 2 })
	cleared := parent(hooks.EventSessionStart)[1]
	require.Equal(t, "clear", cleared.Source)
	require.NotEqual(t, start.SessionID, cleared.SessionID)

	// The status mapping over everything seen ends Ready, naming the new
	// conversation.
	var s claudeState
	for _, ev := range seen {
		s.applyEvent(ev)
	}
	require.Equal(t, "Ready", describe(s))
	require.Equal(t, cleared.SessionID, s.sessionID)

	// The roster leads the hooks: a query that started after a hook was
	// stamped already reports what the hook reported. Only samples wholly
	// inside the window count; one that ends after `to` may have read the
	// state the next action caused.
	stopPoller()
	mu.Lock()
	defer mu.Unlock()
	requireRoster := func(from, to time.Time, want RosterStatus, what string) {
		t.Helper()
		n := 0
		for _, sm := range samples {
			if !sm.listed || !sm.at.After(from) || !sm.end.Before(to) {
				continue
			}
			n++
			require.Equal(t, want, sm.status,
				"%s: a roster query that started %s after the hook reported %v; the newest-wins merge assumes the roster has already moved",
				what, sm.at.Sub(from), sm.status)
		}
		require.NotZero(t, n, "%s: no roster sample in the window", what)
	}
	requireRoster(firstStop.At, secondPrompt, RosterStatusIdle, "after Stop")
	requireRoster(perm.At, approved, RosterStatusWaiting, "after PermissionRequest")
}

// waitForRealClaude polls cond until it holds, failing after timeout with
// the pane's screen, which is the only way to see what Claude did instead.
func waitForRealClaude(t *testing.T, screen func() string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s; screen:\n%s", timeout, screen())
}
