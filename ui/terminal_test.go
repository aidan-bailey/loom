package ui

import (
	"context"
	"fmt"
	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newMockTmuxSession creates a mock tmux session backed by MockCmdExec.
// The returned session will report as existing and support capture-pane commands.
func newMockTmuxSession(t *testing.T, name string, cmdExec cmd_test.MockCmdExec) *tmux.TmuxSession {
	t.Helper()
	ptyFactory := &MockPtyFactory{
		t:       t,
		cmdExec: cmdExec,
	}
	return tmux.NewTmuxSessionWithDeps(name, "bash", ptyFactory, cmdExec)
}

// mockCmdExec returns a MockCmdExec that simulates a working tmux session.
// captureContent is returned for capture-pane commands.
func mockCmdExec(captureContent string, sessionExists bool) cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			cmdStr := cmd.String()
			if strings.Contains(cmdStr, "has-session") {
				if sessionExists {
					return nil
				}
				return fmt.Errorf("session does not exist")
			}
			if strings.Contains(cmdStr, "new-session") {
				return nil
			}
			if strings.Contains(cmdStr, "kill-session") {
				return nil
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			cmdStr := cmd.String()
			if strings.Contains(cmdStr, "capture-pane") {
				return []byte(captureContent), nil
			}
			return []byte(""), nil
		},
	}
}

// makeStartedInstance creates a minimal instance that reports as started with the given title.
func makeStartedInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	workdir := t.TempDir()
	setupGitRepo(t, workdir)

	random := time.Now().UnixNano() % 10000000
	sessionName := fmt.Sprintf("test-terminal-%s-%d-%d", title, time.Now().UnixNano(), random)

	sessionCreated := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			cmdStr := cmd.String()
			if strings.Contains(cmdStr, "has-session") {
				if sessionCreated {
					return nil
				}
				return fmt.Errorf("session does not exist")
			}
			if strings.Contains(cmdStr, "new-session") {
				sessionCreated = true
				return nil
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte(""), nil
		},
	}

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   sessionName,
		Path:    workdir,
		Program: "bash",
	})
	require.NoError(t, err)

	ptyFactory := &MockPtyFactory{
		t:       t,
		cmdExec: cmdExec,
	}
	instance.SetTmuxSession(tmux.NewSessionWithDeps(sessionName, "bash", ptyFactory, cmdExec))

	err = instance.Start(true)
	require.NoError(t, err)

	return instance
}

// injectSession injects a mock tmux session into the TerminalPane's sessions map.
func injectSession(tp *TerminalPane, title string, ts *tmux.TmuxSession, worktreePath string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.sessions[title] = &terminalSession{
		tmuxSession:  ts,
		worktreePath: worktreePath,
	}
	tp.currentTitle = title
}

func TestTerminalUpdateContent(t *testing.T) {
	_ = log.Initialize("", false)
	defer log.Close()

	expectedContent := "$ whoami\nuser\n$ ls\nfile1.txt  file2.txt"

	cmdExec := mockCmdExec(expectedContent, true)

	instance := makeStartedInstance(t, "update-content")
	defer func() { _ = instance.Kill() }()

	tp := NewTerminalPane()
	tp.SetSize(80, 30)

	// Inject a mock session that returns expectedContent on capture-pane
	ts := newMockTmuxSession(t, "mock-update", cmdExec)
	// Start the session so DoesSessionExist returns true
	injectSession(tp, instance.Title, ts, t.TempDir())

	// UpdateContent should set fallback=false and capture content
	err := tp.UpdateContent(viewPtr(instance))
	require.NoError(t, err)

	tp.mu.Lock()
	require.False(t, tp.fallback, "should not be in fallback mode after successful content update")
	require.Equal(t, expectedContent, tp.content, "content should match captured pane output")
	tp.mu.Unlock()

	// Verify String() output contains the content
	rendered := tp.String()
	require.Contains(t, rendered, "whoami", "rendered output should contain captured content")
}

func TestTerminalFallbackStates(t *testing.T) {
	_ = log.Initialize("", false)
	defer log.Close()

	tp := NewTerminalPane()
	tp.SetSize(80, 30)

	t.Run("nil instance", func(t *testing.T) {
		err := tp.UpdateContent(nil)
		require.NoError(t, err)

		tp.mu.Lock()
		require.True(t, tp.fallback, "should be in fallback mode for nil instance")
		require.Contains(t, tp.fallbackText, "Select an instance", "fallback text should prompt to select instance")
		require.Empty(t, tp.content, "content should be empty in fallback mode")
		tp.mu.Unlock()
	})

	t.Run("paused instance", func(t *testing.T) {
		// Create an instance without starting it, then set status to Paused.
		// UpdateContent checks Paused status before Started(), so no need to start.
		instance, err := session.NewInstance(session.InstanceOptions{
			Title:   "paused-inst",
			Path:    t.TempDir(),
			Program: "bash",
		})
		require.NoError(t, err)
		_ = instance.TransitionTo(session.Paused)

		err = tp.UpdateContent(viewPtr(instance))
		require.NoError(t, err)

		tp.mu.Lock()
		require.True(t, tp.fallback, "should be in fallback mode for paused instance")
		require.Contains(t, tp.fallbackText, "paused", "fallback text should mention paused")
		tp.mu.Unlock()
	})

	t.Run("not started instance", func(t *testing.T) {
		// Create an instance that hasn't been started
		instance, err := session.NewInstance(session.InstanceOptions{
			Title:   "not-started",
			Path:    t.TempDir(),
			Program: "bash",
		})
		require.NoError(t, err)

		err = tp.UpdateContent(viewPtr(instance))
		require.NoError(t, err)

		tp.mu.Lock()
		require.True(t, tp.fallback, "should be in fallback mode for not-started instance")
		require.Contains(t, tp.fallbackText, "not started", "fallback text should indicate not started")
		tp.mu.Unlock()
	})
}

func TestTerminalSessionCaching(t *testing.T) {
	_ = log.Initialize("", false)
	defer log.Close()

	tp := NewTerminalPane()
	tp.SetSize(80, 30)

	content1 := "session-1-content"
	cmdExec1 := mockCmdExec(content1, true)
	ts1 := newMockTmuxSession(t, "cache-test-1", cmdExec1)

	content2 := "session-2-content"
	cmdExec2 := mockCmdExec(content2, true)
	ts2 := newMockTmuxSession(t, "cache-test-2", cmdExec2)

	instance1 := makeStartedInstance(t, "cache1")
	defer func() { _ = instance1.Kill() }()
	instance2 := makeStartedInstance(t, "cache2")
	defer func() { _ = instance2.Kill() }()

	// Inject two separate sessions
	injectSession(tp, instance1.Title, ts1, t.TempDir())

	tp.mu.Lock()
	tp.sessions[instance2.Title] = &terminalSession{
		tmuxSession:  ts2,
		worktreePath: t.TempDir(),
	}
	tp.mu.Unlock()

	// Switch to instance1 and capture
	tp.mu.Lock()
	tp.currentTitle = instance1.Title
	tp.mu.Unlock()

	err := tp.UpdateContent(viewPtr(instance1))
	require.NoError(t, err)
	tp.mu.Lock()
	require.Equal(t, content1, tp.content)
	tp.mu.Unlock()

	// Switch to instance2 and capture
	tp.mu.Lock()
	tp.currentTitle = instance2.Title
	tp.mu.Unlock()

	err = tp.UpdateContent(viewPtr(instance2))
	require.NoError(t, err)
	tp.mu.Lock()
	require.Equal(t, content2, tp.content)
	tp.mu.Unlock()

	// Switch back to instance1 — session should still exist (cached)
	tp.mu.Lock()
	tp.currentTitle = instance1.Title
	tp.mu.Unlock()

	err = tp.UpdateContent(viewPtr(instance1))
	require.NoError(t, err)
	tp.mu.Lock()
	require.Equal(t, content1, tp.content, "should get cached session content when switching back")
	// Verify both sessions are still in the map
	require.Len(t, tp.sessions, 2, "both sessions should be cached")
	tp.mu.Unlock()
}

func TestTerminalPane_GotoBottomResetsOffset(t *testing.T) {
	tp := NewTerminalPane()
	tp.snapFallback.offset = 5
	tp.GotoBottom()
	if tp.snapFallback.offset != 0 || tp.IsScrolling() {
		t.Fatalf("GotoBottom must reset to live tail; offset=%d", tp.snapFallback.offset)
	}
}

// TestTerminalPane_SnapshotCaptureRevalidatesTitle exercises the headline
// lock-fix: the scrolled snapshot path releases t.mu across CaptureHistory,
// and if the displayed instance changes during that window the stale capture
// must be DISCARDED. The mock flips t.currentTitle from inside the capture
// (same goroutine, while t.mu is released — the exact race the guard covers),
// and the test asserts the prior content and scroll state survive untouched.
func TestTerminalPane_SnapshotCaptureRevalidatesTitle(t *testing.T) {
	_ = log.Initialize("", false)
	defer log.Close()

	tp := NewTerminalPane()
	tp.SetSize(80, 30)

	instance := makeStartedInstance(t, "reval")
	defer func() { _ = instance.Kill() }()

	const sentinel = "PRIOR-CONTENT-MUST-SURVIVE"
	flipped := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error { return nil }, // has-session → exists
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			if strings.Contains(cmd.String(), "capture-pane") && !flipped {
				// The displayed instance changes while t.mu is released for
				// the capture — the switched-away view must not win.
				flipped = true
				tp.currentTitle = "switched-away"
			}
			return []byte("h1\nh2\nh3\nh4\nh5\nh6\nh7\nh8"), nil
		},
	}
	ts := newMockTmuxSession(t, "reval-mock", cmdExec)
	injectSession(tp, instance.Title, ts, t.TempDir())

	// Force the scrolled snapshot branch (offset > 0, no emulator) and pin a
	// sentinel we can prove was not overwritten.
	tp.mu.Lock()
	tp.snapFallback.offset = 3
	tp.snapFallback.lastTotal = 100 // pre-existing gesture state
	tp.content = sentinel
	tp.mu.Unlock()

	err := tp.UpdateContent(viewPtr(instance))
	require.NoError(t, err)

	tp.mu.Lock()
	defer tp.mu.Unlock()
	require.True(t, flipped, "the capture must have run (and flipped the displayed title)")
	require.Equal(t, sentinel, tp.content,
		"a stale capture for a switched-away instance must not overwrite content")
	require.Equal(t, 3, tp.snapFallback.offset,
		"snapshot scroll state must be left intact for the new instance's own render")
}

// TestTerminalPane_AltScreenScrollForwards drives the pane's snapshot-path
// scroll against a full-screen TUI (alternate screen on): the wheel must be
// forwarded to the app instead of moving the pane's window (audit finding 5 —
// the terminal pane previously had no alt-screen routing at all).
func TestTerminalPane_AltScreenScrollForwards(t *testing.T) {
	_ = log.Initialize("", false)
	defer log.Close()

	tp := NewTerminalPane()
	tp.SetSize(80, 30)

	instance := makeStartedInstance(t, "alt")
	defer func() { _ = instance.Kill() }()

	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error { return nil }, // has-session → exists
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			if strings.Contains(cmd.String(), "alternate_on") {
				return []byte("1"), nil // foreground app is on the alternate screen
			}
			return []byte(""), nil
		},
	}
	ts := newMockTmuxSession(t, "alt-mock", cmdExec)
	injectSession(tp, instance.Title, ts, t.TempDir())

	// Snapshot-mode session (no emulator): ScrollUp probes alt-screen and, on
	// the alternate screen, forwards a wheel event rather than windowing. The
	// ForwardWheel PTY write itself may error on the mock; the ROUTING is what
	// we assert — no snapshot offset movement, no emulator scroll engagement.
	_ = tp.ScrollUp()

	tp.mu.Lock()
	defer tp.mu.Unlock()
	require.Zero(t, tp.snapFallback.offset,
		"alt-screen scroll must forward the wheel, not move the snapshot window")
	require.False(t, tp.scroll.IsScrolling(),
		"alt-screen scroll must not engage the emulator scroll model")
}

// TestTerminalPane_ProbesRunOffLock pins that the session-resolving
// methods hold t.mu only for the cache lookup. A has-session probe that
// never answers must not block String() (the render path) — the script
// engine calls SendKeysToInstance from its own goroutine, concurrently
// with View.
func TestTerminalPane_ProbesRunOffLock(t *testing.T) {
	calls := map[string]func(*TerminalPane) error{
		"SendKeysToInstance": func(p *TerminalPane) error { return p.SendKeysToInstance("inst", "x") },
		"SendKeysRaw":        func(p *TerminalPane) error { return p.SendKeysRaw([]byte("x")) },
		"SendPrompt":         func(p *TerminalPane) error { return p.SendPrompt("x") },
		"ForwardMouse":       func(p *TerminalPane) error { return p.ForwardMouse(0, 1, 1, true) },
		"Paste":              func(p *TerminalPane) error { return p.Paste("x") },
		"CurrentTmuxSession": func(p *TerminalPane) error {
			if p.CurrentTmuxSession() != nil {
				return nil
			}
			return fmt.Errorf("no live session")
		},
		"ScrollUp":   func(p *TerminalPane) error { return p.ScrollUp() },
		"ScrollDown": func(p *TerminalPane) error { return p.ScrollDown() },
		"PageUp":     func(p *TerminalPane) error { return p.PageUp() },
		"PageDown":   func(p *TerminalPane) error { return p.PageDown() },
		"GotoTop":    func(p *TerminalPane) error { return p.GotoTop() },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			probing := make(chan struct{}, 1)
			release := make(chan struct{})
			cmdExec := cmd_test.MockCmdExec{
				RunFunc: func(c *exec.Cmd) error {
					if strings.Contains(c.String(), "has-session") {
						select {
						case probing <- struct{}{}:
						default:
						}
						<-release
						return fmt.Errorf("session does not exist")
					}
					return nil
				},
				OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
			}
			pane := NewTerminalPane()
			pane.InjectSessionForTest("inst", newMockTmuxSession(t, "term", cmdExec), t.TempDir())

			done := make(chan error, 1)
			go func() { done <- call(pane) }()
			<-probing

			rendered := make(chan struct{})
			go func() {
				_ = pane.String()
				close(rendered)
			}()
			select {
			case <-rendered:
			case <-time.After(time.Second):
				close(release)
				t.Fatalf("String() blocked while %s's has-session probe was in flight", name)
			}
			close(release)
			<-done
		})
	}
}

// TestTerminalPane_DetachAllHandsOverWithoutKilling: DetachAll empties the
// cache and returns the sessions for the caller to release, without
// closing (killing) any of them.
func TestTerminalPane_DetachAllHandsOverWithoutKilling(t *testing.T) {
	killed := false
	cmdExec := mockCmdExec("", true)
	run := cmdExec.RunFunc
	cmdExec.RunFunc = func(c *exec.Cmd) error {
		if strings.Contains(strings.Join(c.Args, " "), "kill-session") {
			killed = true
		}
		return run(c)
	}
	pane := NewTerminalPane()
	a, b := newMockTmuxSession(t, "term-a", cmdExec), newMockTmuxSession(t, "term-b", cmdExec)
	pane.InjectSessionForTest("a", a, t.TempDir())
	pane.InjectSessionForTest("b", b, t.TempDir())

	got := pane.DetachAll()

	require.ElementsMatch(t, []*tmux.TmuxSession{a, b}, got)
	pane.mu.Lock()
	require.Empty(t, pane.sessions, "the cache is empty")
	pane.mu.Unlock()
	require.Nil(t, pane.CurrentTmuxSession())
	require.False(t, killed, "DetachAll must not kill the shells")
	require.Empty(t, pane.DetachAll())
}

// TestTerminalPane_NoNewShellForInactiveRow: only an active row gets a new
// terminal shell. A kill (Deleting) or a pause (Loading) ends the row's
// shell in its job while the row still reads Deleting or Loading, and the
// pane refreshes meanwhile; a shell started then outlived the row, since
// the prune that follows only detaches its client (a fast D, y kill hit
// that window every time once the model moved behind the wire). A resume
// or recover (Loading) has no worktree to start one in yet. A live shell
// the row already has stays on show, and the Running row is the control:
// the same ended shell is replaced there, on the same tmux server.
func TestTerminalPane_NoNewShellForInactiveRow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH; skipping real-tmux test")
	}
	// The control's shell attaches a client, which needs a usable TERM on
	// tty-less runners.
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("SHELL", "/bin/sh")

	const (
		none  = iota // no shell cached
		ended        // a cached shell the operation has since ended
		live         // a cached shell still running
	)
	cases := []struct {
		name      string
		status    session.Status
		cache     int
		wantShell bool // a new shell started on the server
	}{
		{"deleting, shell ended", session.Deleting, ended, false},
		{"deleting, none cached", session.Deleting, none, false},
		{"loading, shell ended", session.Loading, ended, false},
		{"loading, none cached", session.Loading, none, false},
		{"deleting, shell live", session.Deleting, live, false},
		{"running, shell ended", session.Running, ended, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title := fmt.Sprintf("inactive-%d", i)
			name := tmux.ToLoomTmuxName(tmux.TerminalSessionName(title))
			tp := NewTerminalPane()
			tp.SetSize(80, 24)
			t.Cleanup(func() {
				_ = tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget(name)).Run()
				for _, ts := range tp.DetachAll() {
					_ = ts.PausePreview()
				}
			})
			switch tc.cache {
			case ended:
				tp.InjectSessionForTest(title, newMockTmuxSession(t, title, mockCmdExec("", false)), t.TempDir())
			case live:
				tp.InjectSessionForTest(title, newMockTmuxSession(t, title, mockCmdExec("live shell", true)), t.TempDir())
			}

			v := &core.InstanceView{Title: title, Started: true, Status: tc.status, WorktreePath: t.TempDir()}
			require.NoError(t, tp.UpdateContent(v))

			require.Equal(t, tc.wantShell, tmux.NewSessionNamed(name, "").DoesSessionExist(),
				"a new shell on the server for a %s row", tc.status)
			if tc.wantShell {
				return
			}
			if tc.cache == live {
				require.Contains(t, tp.String(), "live shell", "the live shell stays on show")
			} else {
				require.True(t, tp.ShowingFallback(), "no shell to show")
			}
		})
	}
}
