package takeover

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func holder(pid int, tty string) Holder {
	return Holder{PID: pid, TTY: tty, Started: time.Date(2026, 10, 6, 6, 23, 0, 0, time.Local)}
}

func TestTryAcquire_RecordsHolder(t *testing.T) {
	dir := t.TempDir()
	l, _, err := TryAcquire(dir, holder(11, "/dev/pts/2"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	_, got, err := TryAcquire(dir, holder(22, "/dev/pts/13"))
	assert.ErrorIs(t, err, ErrHeld)
	assert.Equal(t, 11, got.PID, "a contender learns who holds the lock")
	assert.Equal(t, "/dev/pts/2", got.TTY)
}

func TestClose_ReleasesLock(t *testing.T) {
	dir := t.TempDir()
	l, _, err := TryAcquire(dir, holder(11, ""))
	require.NoError(t, err)
	require.NoError(t, l.Close())

	l2, _, err := TryAcquire(dir, holder(22, ""))
	require.NoError(t, err)
	_ = l2.Close()
	_, err = os.Stat(filepath.Join(dir, lockFile))
	assert.NoError(t, err, "the lock file stays: removing it while a contender waits would split the lock across two inodes")
}

func TestRequest_DeliversRequesterToListener(t *testing.T) {
	dir := t.TempDir()
	l, _, err := TryAcquire(dir, holder(11, ""))
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	got := make(chan Holder, 1)
	require.NoError(t, l.Listen(func(by Holder) { got <- by }))

	require.NoError(t, Request(dir, holder(22, "/dev/pts/13"), time.Second))

	select {
	case by := <-got:
		assert.Equal(t, 22, by.PID)
		assert.Equal(t, "/dev/pts/13", by.TTY)
	case <-time.After(2 * time.Second):
		t.Fatal("listener never saw the request")
	}
}

// A holder that never listens (a loom from before takeover existed holds
// no lock at all, but a hung one might) fails the request cleanly.
func TestRequest_NoListenerErrors(t *testing.T) {
	dir := t.TempDir()
	l, _, err := TryAcquire(dir, holder(11, ""))
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	assert.Error(t, Request(dir, holder(22, ""), 200*time.Millisecond))
}

// A holder that crashed leaves its socket file behind; the next holder
// replaces it rather than failing to listen.
func TestListen_ReplacesStaleSocket(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, sockFile), nil, 0o600))
	l, _, err := TryAcquire(dir, holder(11, ""))
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	assert.NoError(t, l.Listen(func(Holder) {}))
}

func TestWait_AcquiresOnceReleased(t *testing.T) {
	dir := t.TempDir()
	l, _, err := TryAcquire(dir, holder(11, ""))
	require.NoError(t, err)
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = l.Close()
	}()

	l2, err := Wait(dir, holder(22, ""), 2*time.Second)
	require.NoError(t, err)
	_ = l2.Close()
}

func TestWait_TimesOut(t *testing.T) {
	dir := t.TempDir()
	l, _, err := TryAcquire(dir, holder(11, ""))
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	_, err = Wait(dir, holder(22, ""), 200*time.Millisecond)
	assert.ErrorIs(t, err, ErrHeld)
}

func TestHolderString(t *testing.T) {
	h := holder(3713275, "/dev/pts/2")
	now := time.Date(2026, 10, 6, 10, 0, 0, 0, time.Local)
	assert.Equal(t, "pid 3713275 on /dev/pts/2, since 06:23", h.describe(now))
	assert.Equal(t, "pid 3713275 on /dev/pts/2, since Oct 6 06:23", h.describe(now.AddDate(0, 0, 1)))
	assert.Equal(t, "pid 9", Holder{PID: 9}.describe(now), "unknown tty and start are left out")
}
