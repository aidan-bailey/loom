package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/internal/testenv"
)

// fakeDaemonEnv, set to a global dir, makes the test binary a fake `loom
// serve` of that dir (runFakeDaemon) instead of running the tests, and
// fakeDaemonModeEnv picks its record (the fake… modes).
const (
	fakeDaemonEnv     = "LOOMDEV_FAKE_DAEMON"
	fakeDaemonModeEnv = "LOOMDEV_FAKE_DAEMON_MODE"
)

const (
	// fakeBooting holds the lock with the record of a daemon still
	// booting: a build, no socket yet.
	fakeBooting = "booting"
	// fakePreDaemon holds it with the record of a loom from before the
	// daemon (a pid, no socket, no build), which daemon.Stop refuses.
	fakePreDaemon = "predaemon"
)

// runFakeDaemon stands in for `loom serve`, as far as loomdev sees one: it
// holds dir's lock with a daemon's record until SIGTERM, which is how
// daemon.Stop asks a daemon to go. mode varies the record; any mode but
// the default is never stopped by a signal.
func runFakeDaemon(dir, mode string) int {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	rec := daemon.Record{PID: os.Getpid(), Started: time.Now(), Build: "fake", Socket: filepath.Join(dir, "fake.sock")}
	switch mode {
	case fakeBooting:
		rec.Socket = ""
	case fakePreDaemon:
		rec = daemon.Record{PID: os.Getpid(), Started: time.Now()}
	}
	lock, _, err := daemon.TryAcquire(dir, rec)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for mode != "" { // until killed
		time.Sleep(time.Hour)
	}
	<-sigs
	_ = lock.Close()
	return 0
}

// TestMain points LOOM_HOME and LOOM_GLOBAL_DIR at throwaway directories,
// so no test here can resolve the developer's real ~/.loom. Tests that
// need directories of their own still t.Setenv over them. Started as a
// fake daemon (startFakeDaemon), the test binary runs that instead.
func TestMain(m *testing.M) {
	if dir := os.Getenv(fakeDaemonEnv); dir != "" {
		os.Exit(runFakeDaemon(dir, os.Getenv(fakeDaemonModeEnv)))
	}
	cleanup := testenv.MustIsolateLoomDirs()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
