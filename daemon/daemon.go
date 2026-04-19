package daemon

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// tickInstanceTimeout bounds the wall-clock budget for one instance's
// per-tick work (HasUpdated + TapEnter + UpdateDiffStats). Beyond this
// we abandon the goroutine and move on, so a single wedged tmux
// capture or git diff cannot stall auto-yes for every other tracked
// instance. Declared as var (not const) so tests can shorten it.
var tickInstanceTimeout = 5 * time.Second

// runWithTimeout runs work in a goroutine and returns when either the
// work completes or tickInstanceTimeout elapses. On timeout the
// goroutine is intentionally leaked — the thing wedging it (a hung
// ptmx capture, a git lockfile) will likely keep it hung regardless
// of how we cancel, but isolating the wedge to one goroutine lets
// the daemon continue serving every other instance. Logging is rate-
// limited via everyN so a persistently stuck session does not spam
// the log.
func runWithTimeout(title string, work func(), everyN *log.Every) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		work()
	}()
	select {
	case <-done:
	case <-time.After(tickInstanceTimeout):
		if everyN.ShouldLog() {
			log.For("daemon").Warn("tick_exceeded_timeout", "title", title, "timeout", tickInstanceTimeout)
		}
	}
}

// syncTracked reconciles the daemon's in-memory map of live Instance
// objects with the fresh on-disk data. Instances newly present on disk
// are constructed and, if not paused, have their PTY spawned. Instances
// that have disappeared from disk are dropped (we rely on the main app
// to have torn down their tmux session). EnsureRunning is only called
// once per instance over the daemon's lifetime, fixing DAEMON-05.
//
// syncTracked is a function (not a method) so that tests can drive it
// directly against a stub filesystem.
func syncTracked(
	tracked map[string]*session.Instance,
	fresh []session.InstanceData,
	configDir string,
	everyN *log.Every,
) {
	present := make(map[string]bool, len(fresh))
	for _, d := range fresh {
		present[d.Title] = true
	}
	for title := range tracked {
		if !present[title] {
			delete(tracked, title)
		}
	}
	for _, d := range fresh {
		if _, ok := tracked[d.Title]; ok {
			continue
		}
		inst, err := session.FromInstanceData(d, configDir)
		if err != nil {
			if everyN.ShouldLog() {
				log.For("daemon").Warn("construct_failed", "instance", d.Title, "err", err)
			}
			continue
		}
		// EnsureRunning is a no-op for Paused instances and a one-shot
		// PTY attach otherwise. Paused instances remain inert (no PTY)
		// — a bare AutoYes tick reads Started+!Paused and skips them.
		if err := inst.EnsureRunning(); err != nil {
			if everyN.ShouldLog() {
				log.For("daemon").Warn("ensure_running_failed", "instance", d.Title, "err", err)
			}
			continue
		}
		tracked[d.Title] = inst
	}
}

// autoYesMaxConcurrent caps how many instance probes run in parallel per
// tick. Sized for real workloads: users with 3-5 AutoYes sessions see full
// parallelism, while pathological cases (20+ instances) stay bounded so a
// tick cannot spawn a goroutine storm. Not a const so tests can tighten it.
var autoYesMaxConcurrent = 4

// eligibleForAutoYes filters tracked instances down to those the daemon
// should probe this tick: per-instance AutoYes must be on, and the
// instance must be Started + !Paused. Separated out so the parallel
// fan-out in RunDaemon focuses on I/O, not gating.
func eligibleForAutoYes(tracked map[string]*session.Instance) []*session.Instance {
	out := make([]*session.Instance, 0, len(tracked))
	for _, instance := range tracked {
		if !instance.AutoYes {
			continue
		}
		if !instance.Started() || instance.Paused() {
			continue
		}
		out = append(out, instance)
	}
	return out
}

// runBoundedParallel invokes fn(0)..fn(n-1) with at most max concurrent
// invocations, waiting for all to finish before returning. A slow fn(i)
// no longer starves the rest — that's the whole point for the daemon,
// where one stalled git diff previously blocked prompt detection on every
// other AutoYes instance.
func runBoundedParallel(n, max int, fn func(i int)) {
	if n == 0 {
		return
	}
	if max <= 0 {
		max = 1
	}
	sem := make(chan struct{}, max)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// reloadInstanceData reads state.json and returns the fresh raw instance
// records. Called every tick so the daemon observes instances added or
// removed by the main app (DAEMON-03). Returns raw data (not live Instance
// objects) so that each tick does not re-spawn a fresh PTY attachment for
// every non-paused instance (DAEMON-05).
func reloadInstanceData(configDir string) ([]session.InstanceData, error) {
	state := config.LoadStateFrom(configDir)
	storage, err := session.NewStorage(state, configDir)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	return storage.LoadInstanceData()
}

// RunDaemon runs the daemon process which iterates over all sessions and runs AutoYes mode on them.
// It's expected that the main process kills the daemon when the main process starts.
// wsCtx must carry a resolved ConfigDir.
func RunDaemon(cfg *config.Config, wsCtx *config.WorkspaceContext) error {
	if wsCtx == nil || wsCtx.ConfigDir == "" {
		return fmt.Errorf("RunDaemon: workspace context with resolved ConfigDir required")
	}
	configDir := wsCtx.ConfigDir
	dlog := log.For("component", "daemon")
	dlog.Info("daemon.start", "config_dir", configDir, "poll_ms", cfg.DaemonPollInterval)

	// Initial load so that startup errors fail fast (e.g. corrupt state.json).
	if _, err := reloadInstanceData(configDir); err != nil {
		return fmt.Errorf("failed to load instances: %w", err)
	}

	pollInterval := time.Duration(cfg.DaemonPollInterval) * time.Millisecond

	// If we get an error for a session, it's likely that we'll keep getting the error. Log every 30 seconds.
	everyN := log.NewEvery(60 * time.Second)

	// tracked is keyed by instance Title and caches live Instance objects
	// across ticks so we do not re-spawn a PTY every poll (DAEMON-05).
	tracked := map[string]*session.Instance{}

	wg := &sync.WaitGroup{}
	wg.Add(1)
	stopCh := make(chan struct{})
	go func() {
		defer wg.Done()
		ticker := time.NewTimer(pollInterval)
		for {
			fresh, err := reloadInstanceData(configDir)
			if err != nil {
				if everyN.ShouldLog() {
					dlog.Warn("daemon.reload_failed", "err", err.Error())
				}
			} else {
				syncTracked(tracked, fresh, configDir, everyN)
			}

			// Filter eligible instances outside the parallel fan-out so
			// the worker body is focused on I/O. Preserves the prior
			// gating: per-instance AutoYes opt-out + Started/!Paused.
			eligible := eligibleForAutoYes(tracked)
			var fired atomic.Int32
			runBoundedParallel(len(eligible), autoYesMaxConcurrent, func(i int) {
				instance := eligible[i]
				runWithTimeout(instance.Title, func() {
					if _, hasPrompt := instance.HasUpdated(); hasPrompt {
						dlog.Debug("daemon.autoyes.fire", "instance", instance.Title)
						instance.TapEnter()
						fired.Add(1)
						if err := instance.UpdateDiffStats(); err != nil {
							if everyN.ShouldLog() {
								dlog.Warn("daemon.diff_stats_failed", "instance", instance.Title, "err", err.Error())
							}
						}
					}
				}, everyN)
			})
			dlog.Debug("daemon.tick", "tracked", len(tracked), "eligible", len(eligible), "fired", fired.Load())

			// Handle stop before ticker.
			select {
			case <-stopCh:
				return
			default:
			}

			<-ticker.C
			ticker.Reset(pollInterval)
		}
	}()

	// Notify on SIGINT (Ctrl+C) and SIGTERM so we can drain the poll
	// goroutine before exiting.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	dlog.Info("daemon.signal", "signal", sig.String())

	// Stop the goroutine so we don't race.
	close(stopCh)
	wg.Wait()

	// NOTE: we do NOT call storage.SaveInstances here. The daemon is
	// strictly a read-only client of state.json; the main app is the
	// sole writer. Writing from here would clobber any concurrent
	// writes by the main app (DAEMON-04).
	return nil
}

// LaunchDaemon launches the daemon process.
// wsCtx must carry a resolved ConfigDir.
func LaunchDaemon(wsCtx *config.WorkspaceContext) error {
	if wsCtx == nil || wsCtx.ConfigDir == "" {
		return fmt.Errorf("LaunchDaemon: workspace context with resolved ConfigDir required")
	}
	configDir := wsCtx.ConfigDir

	// Find the claude squad binary.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	args := []string{"--daemon", "--config-dir", configDir}
	cmd := exec.Command(execPath, args...)

	// Detach the process from the parent
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Set process group to prevent signals from propagating
	cmd.SysProcAttr = getSysProcAttr()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start child process: %w", err)
	}

	log.For("daemon").Info("launched", "pid", cmd.Process.Pid)

	pidFile := filepath.Join(configDir, "daemon.pid")
	if err := config.AtomicWriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	// Don't wait for the child to exit, it's detached
	return nil
}

// StopDaemon attempts to stop a running daemon process if it exists. Returns no error if the daemon is not found
// (assumes the daemon does not exist).
// wsCtx must carry a resolved ConfigDir.
func StopDaemon(wsCtx *config.WorkspaceContext) error {
	if wsCtx == nil || wsCtx.ConfigDir == "" {
		return fmt.Errorf("StopDaemon: workspace context with resolved ConfigDir required")
	}
	pidFile := filepath.Join(wsCtx.ConfigDir, "daemon.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return fmt.Errorf("invalid PID file format: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find daemon process: %w", err)
	}

	if err := proc.Kill(); err != nil {
		return fmt.Errorf("failed to stop daemon process: %w", err)
	}

	// Clean up PID file
	if err := os.Remove(pidFile); err != nil {
		return fmt.Errorf("failed to remove PID file: %w", err)
	}

	log.For("daemon").Info("stopped", "pid", pid)
	return nil
}
