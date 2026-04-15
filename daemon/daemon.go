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
	"syscall"
	"time"
)

// reloadInstances reads state.json and returns the fresh instance set.
// Called every tick so the daemon observes instances added or removed
// by the main app (DAEMON-03). configDir is the workspace config
// directory; if empty, falls back to GetConfigDir().
func reloadInstances(configDir string) ([]*session.Instance, error) {
	state := config.LoadStateFrom(configDir)
	storage, err := session.NewStorage(state, configDir)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	return storage.LoadInstances()
}

// RunDaemon runs the daemon process which iterates over all sessions and runs AutoYes mode on them.
// It's expected that the main process kills the daemon when the main process starts.
// configDir is the workspace config directory; if empty, falls back to GetConfigDir().
func RunDaemon(cfg *config.Config, configDir string) error {
	log.InfoLog.Printf("starting daemon")

	// Initial load so that startup errors fail fast (e.g. corrupt state.json).
	instances, err := reloadInstances(configDir)
	if err != nil {
		return fmt.Errorf("failed to load instances: %w", err)
	}

	pollInterval := time.Duration(cfg.DaemonPollInterval) * time.Millisecond

	// If we get an error for a session, it's likely that we'll keep getting the error. Log every 30 seconds.
	everyN := log.NewEvery(60 * time.Second)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	stopCh := make(chan struct{})
	go func() {
		defer wg.Done()
		ticker := time.NewTimer(pollInterval)
		for {
			// Reload from disk every tick so the daemon picks up
			// instances created or deleted by the main app since the
			// last poll (DAEMON-03). On error keep using the previous
			// list to stay resilient to transient I/O.
			//
			// NOTE: FromInstanceData calls Start(false) on non-paused
			// instances which spawns a fresh tmux attach PTY each tick.
			// That is the DAEMON-05 followup; addressing it here is
			// out of scope for Phase 4.
			if fresh, err := reloadInstances(configDir); err != nil {
				if everyN.ShouldLog() {
					log.WarningLog.Printf("daemon reload failed: %v", err)
				}
			} else {
				instances = fresh
			}

			for _, instance := range instances {
				// Respect per-instance AutoYes (DAEMON-13). The user may
				// have opted individual instances out via the main app.
				if !instance.AutoYes {
					continue
				}
				// We only store started instances, but check anyway.
				if instance.Started() && !instance.Paused() {
					if _, hasPrompt := instance.HasUpdated(); hasPrompt {
						instance.TapEnter()
						if err := instance.UpdateDiffStats(); err != nil {
							if everyN.ShouldLog() {
								log.WarningLog.Printf("could not update diff stats for %s: %v", instance.Title, err)
							}
						}
					}
				}
			}

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
	log.InfoLog.Printf("received signal %s", sig.String())

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
// configDir is the workspace config directory; if empty, falls back to GetConfigDir().
func LaunchDaemon(configDir string) error {
	// Find the claude squad binary.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	args := []string{"--daemon"}
	if configDir != "" {
		args = append(args, "--config-dir", configDir)
	}
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

	log.InfoLog.Printf("started daemon child process with PID: %d", cmd.Process.Pid)

	// Save PID to a file for later management
	pidDir := configDir
	if pidDir == "" {
		pidDir, err = config.GetConfigDir()
		if err != nil {
			return fmt.Errorf("failed to get config directory: %w", err)
		}
	}

	pidFile := filepath.Join(pidDir, "daemon.pid")
	if err := config.AtomicWriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	// Don't wait for the child to exit, it's detached
	return nil
}

// StopDaemon attempts to stop a running daemon process if it exists. Returns no error if the daemon is not found
// (assumes the daemon does not exist).
// configDir is the workspace config directory; if empty, falls back to GetConfigDir().
func StopDaemon(configDir string) error {
	pidDir := configDir
	if pidDir == "" {
		var err error
		pidDir, err = config.GetConfigDir()
		if err != nil {
			return fmt.Errorf("failed to get config directory: %w", err)
		}
	}

	pidFile := filepath.Join(pidDir, "daemon.pid")
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

	log.InfoLog.Printf("daemon process (PID: %d) stopped successfully", pid)
	return nil
}
