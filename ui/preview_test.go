package ui

import (
	"fmt"
	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testSetup holds common test setup data
type testSetup struct {
	workdir     string
	instance    *session.Instance
	sessionName string
	cleanupFn   func()
}

// setupTestEnvironment creates a common test environment with git repo and instance
func setupTestEnvironment(t *testing.T, cmdExec cmd_test.MockCmdExec) *testSetup {
	t.Helper()

	// These tests drive pane content through the mocked capture-pane, not the
	// live PTY stream, so run the pane in snapshot (capture-pane) mode. Must be
	// set before instance.Start, which builds the emulator in Restore. The
	// emulator render path is covered by session/vt and session/tmux tests.
	t.Setenv("LOOM_PANE_RENDERER", "snapshot")

	// Initialize logging
	_ = log.Initialize("", false)

	// Set up a temp working directory
	workdir := t.TempDir()

	// Initialize git repository
	setupGitRepo(t, workdir)

	// Create unique session name
	random := time.Now().UnixNano() % 10000000
	sessionName := fmt.Sprintf("test-preview-%s-%d-%d", t.Name(), time.Now().UnixNano(), random)

	// Clean up any existing tmux session
	cleanupCmd := exec.Command("tmux", "kill-session", "-t", "loom_"+sessionName)
	_ = cleanupCmd.Run() // Ignore errors if session doesn't exist

	// Create instance
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   sessionName,
		Path:    workdir,
		Program: "bash",
	})
	require.NoError(t, err)

	// Create MockPtyFactory
	ptyFactory := &MockPtyFactory{
		t:       t,
		cmdExec: cmdExec,
	}

	// Set up tmux session with mocks
	tmuxSession := tmux.NewTmuxSessionWithDeps(sessionName, "bash", ptyFactory, cmdExec)
	instance.SetTmuxSession(tmuxSession)

	// Start the tmux session
	err = instance.Start(true)
	require.NoError(t, err)

	// Create cleanup function
	cleanupFn := func() {
		if instance != nil {
			_ = instance.Kill() // Ignore errors during cleanup
		}
		log.Close()
	}

	return &testSetup{
		workdir:     workdir,
		instance:    instance,
		sessionName: sessionName,
		cleanupFn:   cleanupFn,
	}
}

// setupGitRepo initializes a git repository in the given directory
func setupGitRepo(t *testing.T, workdir string) {
	t.Helper()

	// Initialize git repository
	initCmd := exec.Command("git", "init")
	initCmd.Dir = workdir
	err := initCmd.Run()
	require.NoError(t, err)

	// Create basic git config (local to this repo only)
	configCmd := exec.Command("git", "config", "--local", "user.email", "test@example.com")
	configCmd.Dir = workdir
	err = configCmd.Run()
	require.NoError(t, err)

	configCmd = exec.Command("git", "config", "--local", "user.name", "Test User")
	configCmd.Dir = workdir
	err = configCmd.Run()
	require.NoError(t, err)

	// Create and commit a test file
	testFile := filepath.Join(workdir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	addCmd := exec.Command("git", "add", "test.txt")
	addCmd.Dir = workdir
	err = addCmd.Run()
	require.NoError(t, err)

	commitCmd := exec.Command("git", "commit", "-m", "initial commit")
	commitCmd.Dir = workdir
	err = commitCmd.Run()
	require.NoError(t, err)
}

// MockPtyFactory for testing tmux sessions
type MockPtyFactory struct {
	t       *testing.T
	cmdExec cmd_test.MockCmdExec

	// Array of commands and the corresponding file handles representing PTYs.
	cmds  []*exec.Cmd
	files []*os.File
}

func (pt *MockPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	filePath := filepath.Join(pt.t.TempDir(), fmt.Sprintf("pty-%s-%d", pt.t.Name(), len(pt.cmds)))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err == nil {
		pt.cmds = append(pt.cmds, cmd)
		pt.files = append(pt.files, f)

		// Execute the command through our mock to trigger session creation logic
		_ = pt.cmdExec.Run(cmd)
	}
	return f, err
}

func (pt *MockPtyFactory) Close() {}

// TestPreviewContentWithoutScrolling tests that the preview pane correctly displays content
// for a new instance without requiring scrolling
func TestPreviewContentWithoutScrolling(t *testing.T) {
	// Create test content
	expectedContent := "$ echo test\ntest"

	// Track session creation state
	sessionCreated := false

	// Mock command execution
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			cmdStr := cmd.String()

			// Handle tmux session creation and existence checking
			if strings.Contains(cmdStr, "has-session") {
				if sessionCreated {
					return nil // Session exists
				} else {
					return fmt.Errorf("session does not exist")
				}
			}

			// Handle session creation
			if strings.Contains(cmdStr, "new-session") {
				sessionCreated = true
				return nil
			}

			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			cmdStr := cmd.String()

			// Handle capture-pane commands for normal preview
			if strings.Contains(cmdStr, "capture-pane") {
				// Return our test content for normal preview
				return []byte(expectedContent), nil
			}

			return []byte(""), nil
		},
	}

	// Setup test environment
	setup := setupTestEnvironment(t, cmdExec)
	defer setup.cleanupFn()

	// Create the preview pane
	previewPane := NewPreviewPane()
	previewPane.SetSize(80, 30) // Set reasonable size for testing

	// Update the preview content (this should display the content without scrolling)
	err := previewPane.UpdateContent(setup.instance)
	require.NoError(t, err)

	// Verify we're not in scrolling mode
	require.False(t, previewPane.IsScrolling(), "Should not be in scrolling mode")

	// Verify that the preview state is not in fallback mode
	require.False(t, previewPane.previewState.fallback, "Preview should not be in fallback mode")

	// Verify that the preview state contains the expected content
	require.Equal(t, expectedContent, previewPane.previewState.text, "Preview state should contain the expected content")

	// Verify the rendered string contains the content
	renderedString := previewPane.String()
	require.Contains(t, renderedString, "test", "Rendered preview should contain the test content")
}

// TestPreviewPane_ScrollsIntoHistory is the end-to-end regression guard for
// "scrolling does nothing": with a buffer larger than the screen, scrolling up
// must move the window into older history (capture-pane -S -), away from the
// live tail. The mock returns 200 history lines for the -S capture and the
// bottom 24 for the plain (visible) capture, mirroring real tmux.
func TestPreviewPane_ScrollsIntoHistory(t *testing.T) {
	sessionCreated := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			s := cmd.String()
			if strings.Contains(s, "has-session") {
				if sessionCreated {
					return nil
				}
				return fmt.Errorf("session does not exist")
			}
			if strings.Contains(s, "new-session") {
				sessionCreated = true
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			s := cmd.String()
			if strings.Contains(s, "capture-pane") {
				// CaptureHistory uses `-S` (full scrollback); the plain visible
				// capture does not. Return 200 lines vs the bottom 24.
				if strings.Contains(s, "-S") {
					var b strings.Builder
					for i := 1; i <= 200; i++ {
						fmt.Fprintf(&b, "histline%d\n", i)
					}
					return []byte(strings.TrimRight(b.String(), "\n")), nil
				}
				var b strings.Builder
				for i := 177; i <= 200; i++ {
					fmt.Fprintf(&b, "histline%d\n", i)
				}
				return []byte(strings.TrimRight(b.String(), "\n")), nil
			}
			return []byte(""), nil
		},
	}

	setup := setupTestEnvironment(t, cmdExec)
	defer setup.cleanupFn()

	p := NewPreviewPane()
	p.SetSize(80, 24)

	// Live tail: shows the newest lines.
	require.NoError(t, p.UpdateContent(setup.instance))
	liveText := p.previewState.text
	require.Contains(t, liveText, "histline200", "live tail should show the newest line")
	require.False(t, p.IsScrolling(), "fresh pane is at the live tail")

	// Scroll well up into history, then refresh.
	for i := 0; i < 60; i++ {
		require.NoError(t, p.ScrollUp(setup.instance))
	}
	require.NoError(t, p.UpdateContent(setup.instance))
	scrolledText := p.previewState.text

	require.True(t, p.IsScrolling(), "pane should be scrolled after scrolling up")
	require.NotEqual(t, liveText, scrolledText, "scrolled content must differ from the live tail")
	require.NotContains(t, scrolledText, "histline200", "scrolled-up view must not show the newest line")
	require.Contains(t, scrolledText, "histline130", "scrolled-up view should show older history")
}

// TestPreviewPane_TUIAgentForwardsWheel verifies the agent-pane scroll fork:
// when the agent is a full-screen TUI (alternate_on=1), scrolling takes the
// forward path (detects alt-screen, leaves Loom at the live tail) rather than
// engaging its useless offset window. The wheel bytes themselves are covered by
// the tmux-layer ForwardWheel test.
func TestPreviewPane_TUIAgentForwardsWheel(t *testing.T) {
	sessionCreated := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			s := cmd.String()
			if strings.Contains(s, "has-session") {
				if sessionCreated {
					return nil
				}
				return fmt.Errorf("session does not exist")
			}
			if strings.Contains(s, "new-session") {
				sessionCreated = true
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			s := cmd.String()
			if strings.Contains(s, "alternate_on") {
				return []byte("1\n"), nil // agent is a full-screen TUI
			}
			if strings.Contains(s, "capture-pane") {
				return []byte("live screen"), nil
			}
			return []byte(""), nil
		},
	}

	setup := setupTestEnvironment(t, cmdExec)
	defer setup.cleanupFn()

	p := NewPreviewPane()
	p.SetSize(80, 24)
	require.NoError(t, p.UpdateContent(setup.instance)) // live tail

	require.NoError(t, p.ScrollUp(setup.instance))
	require.NoError(t, p.PageUp(setup.instance))

	require.True(t, p.altScreen, "alt-screen TUI agent must be detected")
	require.False(t, p.IsScrolling(), "TUI agent: Loom stays at the live tail, no offset window")
	require.Equal(t, 0, p.scrollOffset, "offset model must not be engaged for a TUI agent")
}

func TestPreviewPane_ScrollOffsetFloorsAtZero(t *testing.T) {
	p := NewPreviewPane()
	p.SetSize(80, 10)
	// ScrollDown from the live tail floors at 0 — never a negative offset. (The
	// top-of-buffer clamp lives in UpdateContent, which needs a captured
	// buffer; setOffset only floors at the bottom.)
	_ = p.ScrollDown(nil)
	if p.scrollOffset != 0 {
		t.Fatalf("ScrollDown from live tail must stay at 0; got %d", p.scrollOffset)
	}
	if p.IsScrolling() {
		t.Fatal("live tail is not a scrolled state")
	}
	if p.ScrollPercent() != 1.0 {
		t.Fatalf("live tail must report ScrollPercent 1.0; got %v", p.ScrollPercent())
	}
}

// TestPreviewPane_windowLines covers the pure windowing helper that both panes
// use to slice a captured history buffer.
func TestPreviewPane_windowLines(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"} // 5 lines
	// fromBottom=0, rows=2 -> the last 2 lines.
	got := windowLines(lines, 0, 2)
	if len(got) != 2 || got[0] != "d" || got[1] != "e" {
		t.Fatalf("window at bottom = %v, want [d e]", got)
	}
	// fromBottom=2, rows=2 -> two lines, two up from the bottom.
	got = windowLines(lines, 2, 2)
	if got[0] != "b" || got[1] != "c" {
		t.Fatalf("window 2-from-bottom = %v, want [b c]", got)
	}
	// Past the top -> leading blank padding (3 blanks), then a..e.
	got = windowLines(lines, 0, 8)
	if len(got) != 8 || got[0] != "" || got[2] != "" || got[3] != "a" || got[7] != "e" {
		t.Fatalf("over-tall window = %v, want [\"\" \"\" \"\" a b c d e]", got)
	}
}

func TestPreviewPane_ScrolledFooterShowsNewLines(t *testing.T) {
	p := NewPreviewPane()
	p.SetSize(80, 10)
	p.scrollOffset = 3
	p.newLinesBelow = 5
	p.previewState = previewState{fallback: false, text: "some\ncontent"}
	out := p.String()
	if !strings.Contains(out, "5") || !strings.Contains(out, "jump to bottom") {
		t.Fatalf("scrolled footer should mention new lines + jump to bottom; got %q", out)
	}
}

func TestPreviewPane_GotoBottomResetsOffset(t *testing.T) {
	p := NewPreviewPane()
	p.scrollOffset = 7
	_ = p.GotoBottom(nil) // nil instance still resets to live tail
	if p.scrollOffset != 0 || p.IsScrolling() {
		t.Fatalf("GotoBottom must reset to live tail; offset=%d", p.scrollOffset)
	}
}
