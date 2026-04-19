package tmux

import (
	cmd2 "claude-squad/cmd"
	"claude-squad/log"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-squad/cmd/cmd_test"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	_ = log.Initialize("", false)
	defer log.Close()
	os.Exit(m.Run())
}

type MockPtyFactory struct {
	t *testing.T

	// Array of commands and the corresponding file handles representing PTYs.
	cmds  []*exec.Cmd
	files []*os.File
}

func (pt *MockPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	filePath := filepath.Join(pt.t.TempDir(), fmt.Sprintf("pty-%s-%d", pt.t.Name(), rand.Int31()))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err == nil {
		pt.cmds = append(pt.cmds, cmd)
		pt.files = append(pt.files, f)
	}
	return f, err
}

func (pt *MockPtyFactory) Close() {}

func NewMockPtyFactory(t *testing.T) *MockPtyFactory {
	return &MockPtyFactory{
		t: t,
	}
}

func TestSanitizeName(t *testing.T) {
	session := NewTmuxSession("asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf", session.sanitizedName)

	session = NewTmuxSession("a sd f . . asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf__asdf", session.sanitizedName)
}

// TestFullScreenAttachCmd verifies that the returned exec.Cmd is shaped so
// tea.ExecProcess can hand the terminal to `tmux attach-session -t <name>`.
func TestFullScreenAttachCmd(t *testing.T) {
	session := NewTmuxSession("attach-shape", "program")
	cmd := session.FullScreenAttachCmd()
	require.Equal(t,
		[]string{"tmux", "attach-session", "-t", TmuxPrefix + "attach-shape"},
		cmd.Args,
	)
}

func TestCaptureAndProcessCapturesOnce(t *testing.T) {
	captureCount := 0
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if strings.Contains(c.String(), "capture-pane") {
				captureCount++
				return []byte("some output"), nil
			}
			return []byte{}, nil
		},
	}

	ts := newTmuxSession("test-session", ProgramClaude, &MockPtyFactory{t: t}, cmdExec)
	ts.monitor = newStatusMonitor()

	_, _, _, _, _ = ts.CaptureAndProcess()
	require.Equal(t, 1, captureCount, "CaptureAndProcess should call capture-pane exactly once")
}

// TestCaptureAndProcessHashesOnce guards against reintroducing the
// double-hash pattern that previously computed SHA-256 over the full
// pane content twice per call (once to compare, once to store).
func TestCaptureAndProcessHashesOnce(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if strings.Contains(c.String(), "capture-pane") {
				return []byte("some output"), nil
			}
			return []byte{}, nil
		},
	}

	ts := newTmuxSession("test-session", ProgramClaude, &MockPtyFactory{t: t}, cmdExec)
	ts.monitor = newStatusMonitor()

	_, _, _, _, _ = ts.CaptureAndProcess()
	require.Equal(t, 1, ts.monitor.hashCalls,
		"CaptureAndProcess should hash pane content exactly once")

	_, _, _, _, _ = ts.CaptureAndProcess()
	require.Equal(t, 2, ts.monitor.hashCalls,
		"second CaptureAndProcess should add exactly one hash call")
}

// TestHasUpdatedHashesOnce mirrors TestCaptureAndProcessHashesOnce for the
// daemon's HasUpdated path.
func TestHasUpdatedHashesOnce(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if strings.Contains(c.String(), "capture-pane") {
				return []byte("some output"), nil
			}
			return []byte{}, nil
		},
	}

	ts := newTmuxSession("test-session", ProgramClaude, &MockPtyFactory{t: t}, cmdExec)
	ts.monitor = newStatusMonitor()

	updated, _ := ts.HasUpdated()
	require.True(t, updated, "first HasUpdated on fresh content should report updated")
	require.Equal(t, 1, ts.monitor.hashCalls, "HasUpdated should hash exactly once on new content")

	updated, _ = ts.HasUpdated()
	require.False(t, updated, "HasUpdated on unchanged content should not report updated")
	require.Equal(t, 2, ts.monitor.hashCalls, "HasUpdated should still hash exactly once per call")
}

// TestRestoreClosesPriorPty ensures that calling Restore twice does not leak
// the first PTY handle. Before the fix, each Pause→Resume cycle leaked one FD
// and one pump goroutine because Restore overwrote t.ptmx without closing.
func TestRestoreClosesPriorPty(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(cmd *exec.Cmd) error { return nil },
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) { return nil, nil },
	}
	session := newTmuxSession("test-session", "claude", ptyFactory, cmdExec)

	require.NoError(t, session.Restore())
	require.Len(t, ptyFactory.files, 1)
	firstPty := ptyFactory.files[0]

	require.NoError(t, session.Restore())
	require.Len(t, ptyFactory.files, 2)

	// First PTY must be closed now; second must still be open.
	_, err := firstPty.Stat()
	require.Error(t, err, "prior PTY should be closed after second Restore")
	_, err = ptyFactory.files[1].Stat()
	require.NoError(t, err, "new PTY should remain open")
}

// TestStartTmuxSession_MultiWordProgram ensures the full program string
// (e.g. "claude --continue" produced by BuildRecoveryCommand) reaches tmux as
// a single shell-command argument. tmux's shell then splits on whitespace, so
// --continue is delivered to claude as a separate argv. Regression guard for
// the crash-recovery path.
func TestStartTmuxSession_MultiWordProgram(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session already exists")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) { return []byte("output"), nil },
	}

	session := newTmuxSession("resume-test", "claude --continue", ptyFactory, cmdExec)
	require.NoError(t, session.Start(t.TempDir()))

	args := ptyFactory.cmds[0].Args
	require.Equal(t, "claude --continue", args[len(args)-1],
		"tmux must receive the full program string as its final argv; tmux's shell splits on whitespace")
}

// TestPumpWaitBoundedOnPausePreview verifies that PausePreview does not
// block forever when the pump goroutine fails to exit. In normal
// operation ptmx.Close unblocks the pump's Read loop, but a stuck
// tmux client (or platform-specific Read pathology) can leave the
// pump goroutine live. Without a bounded wait, PausePreview wedges
// any flow that calls it — including the full-screen attach path.
func TestPumpWaitBoundedOnPausePreview(t *testing.T) {
	session := newTmuxSession("stuck-pause", "claude", NewMockPtyFactory(t), cmd_test.MockCmdExec{
		RunFunc:    func(cmd *exec.Cmd) error { return nil },
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) { return nil, nil },
	})

	// Simulate a stuck pump: live channel that nobody closes.
	session.pumpDone = make(chan struct{})
	session.ptmx = nil

	done := make(chan error, 1)
	go func() { done <- session.PausePreview() }()

	select {
	case <-done:
		// Method returned within the bound — correct behavior.
	case <-time.After(5 * time.Second):
		t.Fatal("PausePreview blocked on pumpDone with no timeout")
	}
}

// TestPumpWaitBoundedOnClose verifies that Close does not block forever
// on a stuck pump. Close is called from Instance.Pause and Kill, so a
// hung pump here propagates into hung lifecycle ops and — via the
// daemon's per-instance UpdateDiffStats — wedges the whole tick loop
// for every other instance.
func TestPumpWaitBoundedOnClose(t *testing.T) {
	session := newTmuxSession("stuck-close", "claude", NewMockPtyFactory(t), cmd_test.MockCmdExec{
		RunFunc:    func(cmd *exec.Cmd) error { return nil },
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) { return nil, nil },
	})

	session.pumpDone = make(chan struct{})
	session.ptmx = nil

	done := make(chan error, 1)
	go func() { done <- session.Close() }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on pumpDone with no timeout")
	}
}

// TestPumpWaitBoundedOnRestore verifies that Restore does not block
// forever if the prior pump has not exited. Restore closes the old
// ptmx and waits before starting a new PTY; a stuck old pump would
// otherwise block Resume flows indefinitely.
func TestPumpWaitBoundedOnRestore(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	session := newTmuxSession("stuck-restore", "claude", ptyFactory, cmd_test.MockCmdExec{
		RunFunc:    func(cmd *exec.Cmd) error { return nil },
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) { return nil, nil },
	})

	session.pumpDone = make(chan struct{})
	session.ptmx = nil

	done := make(chan error, 1)
	go func() { done <- session.Restore() }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Restore blocked on pumpDone with no timeout")
	}
}

func TestStartTmuxSession(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)

	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session already exists")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("output"), nil
		},
	}

	workdir := t.TempDir()
	session := newTmuxSession("test-session", "claude", ptyFactory, cmdExec)

	err := session.Start(workdir)
	require.NoError(t, err)
	require.Equal(t, 2, len(ptyFactory.cmds))
	require.Equal(t, fmt.Sprintf("tmux new-session -d -s claudesquad_test-session -c %s claude", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
	require.Equal(t, "tmux attach-session -t claudesquad_test-session",
		cmd2.ToString(ptyFactory.cmds[1]))

	require.Equal(t, 2, len(ptyFactory.files))

	// File should be closed.
	_, err = ptyFactory.files[0].Stat()
	require.Error(t, err)
	// File should be open
	_, err = ptyFactory.files[1].Stat()
	require.NoError(t, err)
}
