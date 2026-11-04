//go:build !windows

package cli

import (
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real notifySignals (the other tests replace it) catches a SIGTERM sent
// to the process, as a Bash timeout sends it, instead of letting it kill the
// follower; the stop it returns releases the handler.
func TestNotifySignals_CatchesSIGTERM(t *testing.T) {
	sigs, stop := notifySignals()
	defer stop()

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))

	select {
	case got := <-sigs:
		assert.Equal(t, syscall.SIGTERM, got)
	case <-time.After(10 * time.Second):
		t.Fatal("SIGTERM never reached the channel")
	}
}
