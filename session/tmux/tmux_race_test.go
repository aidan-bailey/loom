package tmux

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
)

// racePtyFactory hands out throwaway file-backed PTYs without touching
// testing.T from a goroutine (the dir is created once, up front).
type racePtyFactory struct{ dir string }

func (f *racePtyFactory) Start(*exec.Cmd) (*os.File, error) {
	return os.OpenFile(filepath.Join(f.dir, fmt.Sprintf("pty-%d", rand.Int31())), os.O_CREATE|os.O_RDWR, 0644)
}
func (f *racePtyFactory) Close() {}

// TestTmuxSession_ConcurrentCaptureAndRestore reproduces the data race the
// audit identified between the metadata fan-out (CaptureAndProcess/HasUpdated,
// which read t.ptmx via the trust-prompt TapEnter and mutate t.monitor) and
// the attach lifecycle (Restore, which reassigns both t.ptmx and t.monitor).
// In production these run on the metadata goroutine and the Update goroutine
// respectively. Must pass under `go test -race`.
func TestTmuxSession_ConcurrentCaptureAndRestore(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if strings.Contains(c.String(), "capture-pane") {
				// Trust-prompt content so CaptureAndProcess takes the
				// TapEnter branch, which reads t.ptmx.
				return []byte("Do you trust the files in this folder?"), nil
			}
			return []byte{}, nil
		},
	}
	ts := newTmuxSession("race-session", ProgramClaude, &racePtyFactory{dir: t.TempDir()}, cmdExec)
	if err := ts.Restore(); err != nil { // initialize ptmx + monitor
		t.Fatalf("initial restore: %v", err)
	}
	t.Cleanup(func() { _ = ts.Close() })

	var wg sync.WaitGroup
	const n = 300
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_, _, _, _, _ = ts.CaptureAndProcess()
			_, _ = ts.HasUpdated()
			_ = ts.GetContentHash()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = ts.Restore()
		}
	}()
	wg.Wait()
}
