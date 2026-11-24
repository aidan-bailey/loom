package memguard

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLimit(t *testing.T) {
	cases := []struct {
		spec  string
		limit uint64
		on    bool
		bad   bool
	}{
		{spec: "", limit: DefaultLimit, on: true},
		{spec: "  ", limit: DefaultLimit, on: true},
		{spec: "off"},
		{spec: "OFF"},
		{spec: "0"},
		{spec: "false"},
		{spec: "6G", limit: 6 << 30, on: true},
		{spec: "512M", limit: 512 << 20, on: true},
		{spec: "512m", limit: 512 << 20, on: true},
		{spec: "8GiB", limit: 8 << 30, on: true},
		{spec: "8gb", limit: 8 << 30, on: true},
		{spec: "64K", limit: 64 << 10, on: true},
		{spec: "2T", limit: 2 << 40, on: true},
		{spec: "1048576", limit: 1 << 20, on: true},
		{spec: "1048576B", limit: 1 << 20, on: true},
		{spec: "abc", bad: true},
		{spec: "-1G", bad: true},
		{spec: "1.5G", bad: true},
		{spec: "6X", bad: true},
		{spec: "G", bad: true},
		{spec: "0G", bad: true},
		{spec: "99999999999T", bad: true},
	}
	for _, c := range cases {
		limit, on, err := ParseLimit(c.spec)
		if c.bad {
			assert.Error(t, err, "spec %q", c.spec)
			assert.False(t, on, "spec %q", c.spec)
			continue
		}
		require.NoError(t, err, "spec %q", c.spec)
		assert.Equal(t, c.on, on, "spec %q", c.spec)
		assert.Equal(t, c.limit, limit, "spec %q", c.spec)
	}
}

func TestFromEnv_ABadLimitKeepsTheDefault(t *testing.T) {
	t.Setenv(EnvVar, "lots")
	g := FromEnv(func(Breach) {})
	require.NotNil(t, g, "a limit it cannot read must not turn the guard off")
	defer g.Stop()
	assert.Equal(t, DefaultLimit, g.limit)
}

func TestFromEnv_OffWatchesNothing(t *testing.T) {
	t.Setenv(EnvVar, "off")
	g := FromEnv(func(Breach) {})
	assert.Nil(t, g)
	g.Stop() // a nil guard stops nothing
}

func TestFromEnv_UnsetIsTheDefault(t *testing.T) {
	t.Setenv(EnvVar, "")
	g := FromEnv(func(Breach) {})
	require.NotNil(t, g)
	defer g.Stop()
	assert.Equal(t, DefaultLimit, g.limit)
}

// fakeGuard is a guard over limit 100 whose readings come from bytes in
// order, recording its writes and breaches.
type fakeGuard struct {
	*Guard
	stages   []string
	breaches []Breach
}

func newFakeGuard(t *testing.T, writeErr error, bytes ...uint64) *fakeGuard {
	t.Helper()
	f := &fakeGuard{}
	i := 0
	g := New(100, t.TempDir(), func(b Breach) { f.breaches = append(f.breaches, b) })
	g.read = func() Reading {
		r := Reading{Bytes: bytes[i]}
		if i < len(bytes)-1 {
			i++
		}
		return r
	}
	g.write = func(dir, stage string, at time.Time) ([]string, error) {
		f.stages = append(f.stages, stage)
		if writeErr != nil {
			return nil, writeErr
		}
		return []string{dir + "/" + stage + "-heap", dir + "/" + stage + "-goroutines"}, nil
	}
	f.Guard = g
	return f
}

func TestGuard_BelowHalfTheLimitDoesNothing(t *testing.T) {
	f := newFakeGuard(t, nil, 10, 49, 30)
	for range 3 {
		assert.False(t, f.check())
	}
	assert.Empty(t, f.stages)
	assert.Empty(t, f.breaches)
}

func TestGuard_ProfilesOnceAtHalfTheLimit(t *testing.T) {
	f := newFakeGuard(t, nil, 40, 50, 70, 99)
	for range 4 {
		assert.False(t, f.check())
	}
	assert.Equal(t, []string{"half"}, f.stages, "profiles once, at half the limit, however long it stays there")
	assert.Empty(t, f.breaches)
}

func TestGuard_QuitsAtTheLimitWithItsProfiles(t *testing.T) {
	f := newFakeGuard(t, nil, 60, 100, 120)
	assert.False(t, f.check())
	assert.True(t, f.check(), "the limit ends the watch")
	assert.True(t, f.check())
	assert.Equal(t, []string{"half", "limit"}, f.stages)
	require.Len(t, f.breaches, 1, "one breach, however many readings follow")
	b := f.breaches[0]
	assert.Equal(t, uint64(100), b.Bytes)
	assert.Equal(t, uint64(100), b.Limit)
	assert.Len(t, b.Profiles, 2)
	assert.Contains(t, b.Error(), EnvVar)
	assert.Contains(t, b.Error(), b.Profiles[0])
}

func TestGuard_AJumpPastBothWritesOneSetOfProfiles(t *testing.T) {
	f := newFakeGuard(t, nil, 10, 500)
	assert.False(t, f.check())
	assert.True(t, f.check())
	assert.Equal(t, []string{"limit"}, f.stages)
	assert.Len(t, f.breaches, 1)
}

func TestGuard_AFailedWriteStillQuits(t *testing.T) {
	f := newFakeGuard(t, errors.New("disk full"), 100)
	assert.True(t, f.check())
	require.Len(t, f.breaches, 1, "profiles are evidence; the quit is what protects the host")
	assert.Empty(t, f.breaches[0].Profiles)
}

func TestGuard_StartEndsAtTheLimit(t *testing.T) {
	f := newFakeGuard(t, nil, 100)
	f.Start()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not end at the limit")
	}
	assert.Len(t, f.breaches, 1)
	f.Stop()
}

func TestGuard_StopEndsTheWatch(t *testing.T) {
	f := newFakeGuard(t, nil, 10)
	f.Start()
	f.Stop()
	select {
	case <-f.done:
	default:
		t.Fatal("Stop returned with the watch still running")
	}
	assert.Empty(t, f.breaches)
}

func TestWriteProfiles_WritesHeapAndGoroutines(t *testing.T) {
	dir := t.TempDir()
	paths, err := WriteProfiles(dir, "half", time.Date(2026, 11, 24, 9, 42, 56, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, paths, 2)
	for _, p := range paths {
		assert.True(t, strings.HasPrefix(p, dir), p)
		assert.Contains(t, p, "tui-memory-20261124-094256-")
		assert.Contains(t, p, "-half-")
		st, err := os.Stat(p)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}
	heap, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(heap), 2)
	assert.Equal(t, []byte{0x1f, 0x8b}, heap[:2], "the heap profile is gzipped protobuf, for go tool pprof")
	goroutines, err := os.ReadFile(paths[1])
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(goroutines), "goroutine profile: total"), "the goroutine profile is grouped text")
}

func TestWriteProfiles_AnUnwritableDirIsAnError(t *testing.T) {
	paths, err := WriteProfiles(t.TempDir()+"/missing", "limit", time.Now())
	assert.Error(t, err)
	assert.Empty(t, paths)
}

func TestRead_CountsGoroutineStacks(t *testing.T) {
	before := Read()
	const n = 2000
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(n)
	for range n {
		go func() {
			started.Done()
			<-release
		}()
	}
	started.Wait()
	after := Read()
	close(release)
	assert.GreaterOrEqual(t, after.Goroutines, before.Goroutines+n)
	assert.GreaterOrEqual(t, after.Stacks, before.Stacks+n*2048, "parked goroutines' stacks count: they were the 2026-11-24 runaway's suspect")
	assert.Greater(t, after.Bytes, after.Stacks)
}

// blockingWriter never returns from Write, as a terminal that stopped
// reading does not.
type blockingWriter struct{ block chan struct{} }

func (w blockingWriter) Write(p []byte) (int, error) {
	<-w.block
	return len(p), nil
}

func TestQuit_ExitsWhenKillNeverReturns(t *testing.T) {
	stuck := make(chan struct{})
	defer close(stuck)
	killed := make(chan struct{})
	exited := make(chan int, 1)
	var stderr syncBuffer
	go Quit(Breach{Reading: Reading{Bytes: 7 << 30}, Limit: 6 << 30}, func() {
		close(killed)
		<-stuck // a shutdown waiting on a stalled terminal
	}, 10*time.Millisecond, &stderr, func(code int) { exited <- code })
	select {
	case <-killed:
	case <-time.After(5 * time.Second):
		t.Fatal("Quit never asked the program to stop")
	}
	select {
	case code := <-exited:
		assert.Equal(t, 1, code)
	case <-time.After(5 * time.Second):
		t.Fatal("Quit never exited while kill was stuck")
	}
	assert.Contains(t, stderr.String(), EnvVar)
}

func TestQuit_ExitsWhenStderrBlocks(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	exited := make(chan int, 1)
	go Quit(Breach{Limit: 6 << 30}, func() {}, 10*time.Millisecond, blockingWriter{block}, func(code int) { exited <- code })
	select {
	case code := <-exited:
		assert.Equal(t, 1, code)
	case <-time.After(5 * time.Second):
		t.Fatal("Quit waited on a stderr that never took the message")
	}
}

// syncBuffer is a bytes.Buffer safe to write from Quit's goroutine and read
// from the test's.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
