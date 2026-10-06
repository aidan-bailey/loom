package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/takeover"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runningLoom(t *testing.T, dir string) *takeover.Lock {
	t.Helper()
	l, _, err := takeover.TryAcquire(dir, takeover.Holder{PID: 3713275, TTY: "/dev/pts/2"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestAcquireUILock_Free(t *testing.T) {
	var out, errOut bytes.Buffer
	l, err := acquireUILock(t.TempDir(), strings.NewReader(""), &out, &errOut, true)
	require.NoError(t, err)
	require.NotNil(t, l)
	_ = l.Close()
	assert.Empty(t, out.String(), "no prompt when nothing else runs")
}

// Without a terminal to ask on, a second loom refuses rather than
// overwriting the running one's sessions.
func TestAcquireUILock_HeldNonInteractiveRefuses(t *testing.T) {
	dir := t.TempDir()
	runningLoom(t, dir)
	var out, errOut bytes.Buffer

	_, err := acquireUILock(dir, strings.NewReader("y\n"), &out, &errOut, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pid 3713275 on /dev/pts/2")
	assert.Contains(t, err.Error(), "quit it first")
}

func TestAcquireUILock_Declined(t *testing.T) {
	dir := t.TempDir()
	runningLoom(t, dir)
	var out, errOut bytes.Buffer

	_, err := acquireUILock(dir, strings.NewReader("n\n"), &out, &errOut, true)

	assert.ErrorIs(t, err, errTakeoverDeclined)
	assert.Contains(t, out.String(), "pid 3713275 on /dev/pts/2")
	assert.Contains(t, out.String(), "Take over?")
}

// The handshake: the running loom gets the request, saves and quits
// (here: releases the lock), and only then does the newcomer hold it.
func TestAcquireUILock_TakesOver(t *testing.T) {
	dir := t.TempDir()
	running := runningLoom(t, dir)
	asked := make(chan takeover.Holder, 1)
	require.NoError(t, running.Listen(func(by takeover.Holder) {
		asked <- by
		go func() { _ = running.Close() }() // saves and quits
	}))
	var out, errOut bytes.Buffer

	l, err := acquireUILock(dir, strings.NewReader("y\n"), &out, &errOut, true)

	require.NoError(t, err)
	_ = l.Close()
	assert.Equal(t, takeover.Self().PID, (<-asked).PID, "the running loom learns who took over")
	assert.Contains(t, out.String(), "Waiting for it to save and quit")
}

func TestAcquireUILock_HolderNotListening(t *testing.T) {
	dir := t.TempDir()
	runningLoom(t, dir) // holds the lock, serves no requests
	var out, errOut bytes.Buffer

	_, err := acquireUILock(dir, strings.NewReader("y\n"), &out, &errOut, true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "kill 3713275")
}

// A holder that takes the request but never quits (its save failed, or
// it sits in an editor) times the newcomer out.
func TestAcquireUILock_HolderNeverQuits(t *testing.T) {
	dir := t.TempDir()
	running := runningLoom(t, dir)
	require.NoError(t, running.Listen(func(takeover.Holder) {}))
	orig := takeoverWait
	takeoverWait = 200 * time.Millisecond
	t.Cleanup(func() { takeoverWait = orig })
	var out, errOut bytes.Buffer

	_, err := acquireUILock(dir, strings.NewReader("y\n"), &out, &errOut, true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "didn't quit")
}
