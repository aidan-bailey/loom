package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/spf13/cobra"
)

// serveStopTimeout bounds `loom serve stop`'s wait: the daemon first waits
// for in-flight lifecycle jobs (up to 30s), then saves.
const serveStopTimeout = 60 * time.Second

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the loom daemon for this global config dir (loom starts it when needed)",
	Long: `Run the loom daemon: the process that owns every session of this global
config dir (LOOM_GLOBAL_DIR, else ~/.loom), whether or not a loom TUI is
open. loom starts it on demand and it keeps running after the last TUI
quits, until 'loom serve stop' or a signal. It logs to logs/serve.log in the
global config dir.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		globalDir, err := config.GetGlobalConfigDir()
		if err != nil {
			return err
		}
		logDir := filepath.Dir(daemon.LogPath(globalDir))
		if logErr := log.Initialize(logDir, true); logErr != nil {
			fmt.Fprintf(os.Stderr, "loom: %v\n", logErr)
		}
		defer log.Close()
		// The daemon sweeps tmux sessions, so the nesting guard is its. It
		// logs the refusal too: a daemon a client started has no stderr,
		// and the client quotes the log (daemon.Connect).
		if err := nestingCheck(); err != nil {
			log.For("serve").Error("serve.refused", "err", err)
			return err
		}
		// Pinned before the boot touches tmux, and named in the hello, so
		// the daemon and every client use one server whatever their
		// environments say.
		server, err := tmux.ResolveServer()
		if err != nil {
			log.For("serve").Error("serve.refused", "err", err)
			return fmt.Errorf("find the tmux server: %w", err)
		}
		tmux.UseServer(server)
		// A runtime crash (a concurrent map write, a goroutine's unrecovered
		// panic) goes to stderr, which a daemon started on demand has none
		// of: keep it in a file beside serve.log.
		if f, err := os.OpenFile(filepath.Join(logDir, "serve-crash.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			_ = debug.SetCrashOutput(f, debug.CrashOptions{})
			_ = f.Close()
		}

		stop := make(chan struct{})
		var once sync.Once
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		defer signal.Stop(sigs)
		go func() {
			for s := range sigs {
				log.For("serve").Info("serve.signal", "signal", s.String())
				once.Do(func() { close(stop) })
			}
		}()

		err = daemon.Serve(daemon.Options{
			GlobalDir: globalDir,
			Build:     rpc.Build(),
			Tmux:      server,
			Stop:      stop,
			NewModel: func() (*core.Model, error) {
				registry, err := config.LoadWorkspaceRegistry()
				if err != nil {
					// Served without it: the global workspace alone, and a
					// notice the first client shows.
					log.For("serve").Error("serve.registry_load_failed", "err", err)
					registry = nil
				}
				program := config.LoadConfigFrom(globalDir).GetProgram()
				return core.New(core.Options{Registry: registry, Program: program}), nil
			},
		})
		if errors.Is(err, daemon.ErrRunning) {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}
		return err
	},
}

var serveStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the loom daemon (sessions keep running; the next loom starts a new one)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		globalDir, err := config.GetGlobalConfigDir()
		if err != nil {
			return err
		}
		err = daemon.Stop(globalDir, serveStopTimeout)
		if errors.Is(err, daemon.ErrNotRunning) {
			fmt.Println("no loom daemon is running")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Println("loom daemon stopped")
		return nil
	},
}

func init() {
	serveCmd.AddCommand(serveStopCmd)
	rootCmd.AddCommand(serveCmd)
}
