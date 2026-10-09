package main

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// While the daemon stops, only a user's Ctrl-C ends it before its save: a
// second SIGTERM (another stop, or two looms replacing it together) and a
// SIGHUP are only logged.
func TestForcesExit_OnlyAnInterrupt(t *testing.T) {
	assert.True(t, forcesExit(os.Interrupt))
	assert.False(t, forcesExit(syscall.SIGTERM))
	assert.False(t, forcesExit(syscall.SIGHUP))
}
