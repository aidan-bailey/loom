//go:build unix

package tmux

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A tmux client hands its stdin and stdout to the server when it connects,
// so a server that never answers (frozen here with SIGSTOP, as heavy load
// starves it) holds the write end of a reading command's stdout pipe after
// the deadline has killed the client. Output must still return near its
// deadline: before WaitDelay, it waited for EOF until the server woke, and
// a booting daemon's reconcile sat in sessionStartDir for as long.
func TestCommand_OutputReturnsAtItsDeadlineWhenTheServerNeverAnswers_RealTmux(t *testing.T) {
	privateTmux(t, "frz")
	newRawSession(t, "frozen", "sleep 600")
	out, err := Command(context.Background(), "display-message", "-p", "-t", SessionTarget("frozen"), "#{pid}").Output()
	require.NoError(t, err)
	server, err := strconv.Atoi(strings.TrimSpace(string(out)))
	require.NoError(t, err, "%q", out)
	require.NoError(t, syscall.Kill(server, syscall.SIGSTOP))
	t.Cleanup(func() { _ = syscall.Kill(server, syscall.SIGCONT) }) // before privateTmux's kill-server

	const deadline = time.Second
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		_, err := Command(ctx, "list-sessions", "-F", "#{session_name}").Output()
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err, "a frozen server answered")
		require.Less(t, time.Since(start), deadline+commandWaitDelay+2*time.Second,
			"Output outlived its deadline by more than the wait delay")
	case <-time.After(deadline + commandWaitDelay + 10*time.Second):
		t.Fatal("Output is still waiting on the frozen server's copy of its stdout, long past its deadline")
	}
}
