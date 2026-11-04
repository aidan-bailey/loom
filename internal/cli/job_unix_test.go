//go:build !windows

package cli

import (
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real notifySignals (the other tests replace it) catches each signal
// that interrupts a follower sent to the process (a Bash timeout sends
// SIGTERM), instead of letting it kill the follower; the stop it returns
// releases the handler.
func TestNotifySignals_CatchesTheInterruptSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			sigs, stop := notifySignals()
			defer stop()

			require.NoError(t, syscall.Kill(syscall.Getpid(), sig))

			select {
			case got := <-sigs:
				assert.Equal(t, sig, got)
			case <-time.After(10 * time.Second):
				t.Fatalf("%v never reached the channel", sig)
			}
		})
	}
}
