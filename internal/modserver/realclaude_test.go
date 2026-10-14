package modserver

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

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/require"
)

// claudeConfigDir is the developer's CLAUDE_CONFIG_DIR, read as the package
// initializes, before TestMain points it at an empty dir: the real Claude
// runs logged in as the developer.
var claudeConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")

// queueHost is a ModHost for launch L1 whose messages the test queues.
type queueHost struct {
	mu     sync.Mutex
	hello  bool
	msgs   []core.ModMessage
	wake   chan struct{}
	events []core.ModEvent
}

func (h *queueHost) ModHello(launch string, _ core.ModHello) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hello = true
	return nil
}

func (h *queueHost) ModInbox(launch string) ([]core.ModMessage, <-chan struct{}, error) {
	if launch != "L1" {
		return nil, nil, core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake != nil {
		close(h.wake)
	}
	h.wake = make(chan struct{})
	return append([]core.ModMessage(nil), h.msgs...), h.wake, nil
}

func (h *queueHost) ModEvents(launch string, events []core.ModEvent) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ev := range events {
		h.events = append(h.events, ev)
		if ev.Kind != "queued" {
			continue
		}
		for i, m := range h.msgs {
			if m.ID == ev.ID {
				h.msgs = append(h.msgs[:i], h.msgs[i+1:]...)
				break
			}
		}
	}
	return nil
}

func (h *queueHost) helloed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hello
}

func (h *queueHost) queue(id, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, core.ModMessage{ID: id, Text: text})
	if h.wake != nil {
		close(h.wake)
		h.wake = nil
	}
}

// event is the first event of kind for message id.
func (h *queueHost) event(kind, id string) (core.ModEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ev := range h.events {
		if ev.Kind == kind && ev.ID == id {
			return ev, true
		}
	}
	return core.ModEvent{}, false
}

func (h *queueHost) said() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return fmt.Sprintf("hello %v, events %+v", h.hello, h.events)
}

// TestRealClaude_TheModCarriesAMessage launches a real Claude (haiku) with the
// embedded mod on a private tmux server and serves it from queueHost: hello;
// a message's queued, started and complete (complete carrying Claude's
// answer); and a message delivered while a permission dialog is open, which
// leaves the dialog alone and runs after it. It spends a few cents and leaves
// entries in the developer's Claude config dir (projects/, the trust entry),
// so it runs only with LOOM_TEST_REAL_CLAUDE=1. Its waits add up to about 8
// minutes at worst, inside go test's default 10-minute timeout.
func TestRealClaude_TheModCarriesAMessage(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"tmux", "claude"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	mod := filepath.Join(root, "mod")
	require.NoError(t, claudemod.WriteTo(mod))
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	host := &queueHost{}
	socket := shortSocket(t)
	s, err := Listen(socket, host, 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(s.Close)

	ctx := context.Background()
	sock := fmt.Sprintf("loommod-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		out, err := tmux.CommandOnSocket(ctx, sock, args...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	var serverPath string // the server's socket, which kill-server leaves behind
	t.Cleanup(func() {
		_ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run()
		if serverPath != "" {
			_ = os.Remove(serverPath)
		}
	})
	target := tmux.PaneTarget("probe")
	screen := func() string { return tm("capture-pane", "-p", "-t", target) }
	waitFor := func(what string, d time.Duration, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(d); !ok(); time.Sleep(250 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; %s; screen:\n%s", what, host.said(), screen())
			}
		}
	}
	// Text goes in as loom types it (TypeText: a paste buffer), keys by
	// name; both reach the private server through LOOM_TMUX_SOCKET.
	t.Setenv(tmux.EnvTmuxSocket, sock)
	// A server UseServer pinned in this binary would outrank it.
	cmdArgs := strings.Join(tmux.Command(ctx).Args, " ")
	require.Contains(t, cmdArgs, " -L "+sock, "the pane's helpers reach the private server")
	require.NotContains(t, cmdArgs, " -S ", "and no pinned one")
	pane := tmux.NewSessionNamed("probe", "")
	press := func(keys ...string) {
		t.Helper()
		require.NoError(t, pane.PressKeys(keys...))
	}
	typeLine := func(text string) {
		t.Helper()
		require.NoError(t, pane.TypeText(text))
		time.Sleep(300 * time.Millisecond)
		press("Enter")
	}

	// The server, and so the probe's shell, starts from a clean environment
	// (the 2026-10-14 probe recipe): nothing of this test binary's, whose
	// TestMain isolates Claude's dirs, or of a Claude the test runs inside,
	// which would hand the probe its session. TMUX_TMPDIR stays, or the
	// server's socket would land where tm's clients don't look.
	env := []string{"TERM=xterm-256color"}
	for _, k := range []string{"HOME", "PATH", "USER", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "XDG_RUNTIME_DIR", "TMUX_TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	if claudeConfigDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+claudeConfigDir)
	}
	start := tmux.CommandOnSocket(ctx, sock, "-f", "/dev/null", "new-session", "-d", "-s", "probe", "-x", "160", "-y", "45", "-c", work)
	start.Env = env
	out, err := start.CombinedOutput()
	require.NoError(t, err, string(out))
	serverPath = strings.TrimSpace(tm("display-message", "-p", "-t", target, "#{socket_path}"))
	typeLine(fmt.Sprintf("%s=%s %s=L1 claude --model haiku --permission-mode default --plugin-dir '%s'",
		claudemod.EnvSocket, socket, claudemod.EnvLaunch, mod))
	// Claude asks whether to trust a new folder, with "No, exit" selected
	// for one under a temp dir, and drops a key pressed as the dialog
	// appears: press Down until a capture shows the cursor on Yes, and Enter
	// only then, as loom's launch watch answers it.
	const trustYes = "Yes, I trust this folder"
	waitFor("the trust dialog or the mod's hello", 60*time.Second, func() bool {
		return host.helloed() || strings.Contains(screen(), trustYes)
	})
	if !host.helloed() {
		waitFor("the trust dialog's cursor on Yes", 30*time.Second, func() bool {
			if strings.Contains(screen(), "❯ "+trustYes) {
				return true
			}
			press("Down")
			time.Sleep(500 * time.Millisecond)
			return strings.Contains(screen(), "❯ "+trustYes)
		})
		press("Enter")
	}
	waitFor("the mod's hello", 60*time.Second, host.helloed)

	host.queue("m1", "Reply with exactly the word PONG.")
	waitFor("m1's answer", 90*time.Second, func() bool {
		ev, ok := host.event("complete", "m1")
		return ok && strings.Contains(ev.Text, "PONG")
	})
	_, queued := host.event("queued", "m1")
	require.True(t, queued, "m1 was reported queued")
	started, ok := host.event("started", "m1")
	require.True(t, ok, "m1's turn was reported started")
	complete, _ := host.event("complete", "m1")
	require.NotEmpty(t, started.TurnID)
	require.Equal(t, started.TurnID, complete.TurnID, "m1 started and completed in one turn")

	typeLine("Run this exact bash command: mkdir made-by-test. Then reply DONE.")
	waitFor("the permission dialog", 90*time.Second, func() bool { return strings.Contains(screen(), "Do you want to proceed?") })
	host.queue("m2", "Reply with exactly the word QUEUED.")
	waitFor("m2 queued", 30*time.Second, func() bool { _, ok := host.event("queued", "m2"); return ok })
	time.Sleep(time.Second)
	still := screen()
	require.Contains(t, still, "Do you want to proceed?", "the queued message left the dialog open")
	require.Contains(t, still, "❯ 1. Yes", "with its choice where it was")
	_, early := host.event("started", "m2")
	require.False(t, early, "m2 waits for the dialog's turn to end")
	require.NoDirExists(t, filepath.Join(work, "made-by-test"), "nothing answered the dialog")
	press("Enter")
	waitFor("m2's answer", 90*time.Second, func() bool {
		ev, ok := host.event("complete", "m2")
		return ok && strings.Contains(ev.Text, "QUEUED")
	})
	require.DirExists(t, filepath.Join(work, "made-by-test"), "the dialog's own answer ran the command")

	t.Log(host.said())
}
