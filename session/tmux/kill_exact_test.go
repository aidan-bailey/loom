package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// TestKillIsExactMatch_RealTmux is the regression guard for tmux's target
// matching. `kill-session -t name` falls back to a prefix match when no
// session is named exactly name, so closing an instance whose session had
// already died ("api") killed whichever live session's name began with
// it ("api-v2") — another agent, gone. Close and CloseRelatedSession act
// on sessions that may well be dead (Kill, Pause and Restart after the
// agent exited), so they must match exactly.
func TestKillIsExactMatch_RealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH; skipping real-tmux test")
	}
	// A private server, as in TestCaptureHistoryRealTmux: nothing here may
	// reach the developer's default server.
	tmuxTmpDir, err := os.MkdirTemp("", "lt")
	if err != nil {
		t.Fatalf("mkdir tmux tmpdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	t.Setenv("TMUX", "")
	sock := fmt.Sprintf("lt-k-%d", os.Getpid())
	t.Setenv(EnvTmuxSocket, sock)
	t.Cleanup(func() { _ = CommandOnSocket(context.Background(), sock, "kill-server").Run() })

	alive := func(name string) bool {
		return Command(context.Background(), "has-session", "-t="+name).Run() == nil
	}
	sibling := ToLoomTmuxName("api-v2")
	termSibling := ToLoomTmuxName(TerminalSessionName("api-v2"))
	for _, name := range []string{sibling, termSibling} {
		if out, err := Command(context.Background(), "new-session", "-d", "-s", name, "sleep 300").CombinedOutput(); err != nil {
			t.Fatalf("start %s: %v: %s", name, err, out)
		}
	}

	dead := NewTmuxSession("api", "sh") // never started: its session does not exist
	_ = dead.Close()
	if !alive(sibling) {
		t.Fatalf("Close of the dead session %q killed the live session %q", ToLoomTmuxName("api"), sibling)
	}

	_ = dead.CloseRelatedSession(TerminalSessionName("api"))
	if !alive(termSibling) {
		t.Fatalf("CloseRelatedSession of the dead %q killed the live session %q",
			ToLoomTmuxName(TerminalSessionName("api")), termSibling)
	}
}
