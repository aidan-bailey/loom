package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/app"
	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/claudetmp"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	version            = "0.13.0"
	programFlag        string
	noScriptsFlag      bool
	workspaceFlag      string
	resetWorkspaceFlag string
	resetForceFlag     bool
	logLevelFlag       string
	rootCmd            = &cobra.Command{
		Use:     "loom [directory]",
		Short:   "Loom — Manage multiple AI agents like Claude Code, Aider, Codex, and Amp.",
		Version: version,
		Args:    cobra.MaximumNArgs(1),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Promote --log-level to the env var before any subcommand
			// calls log.Initialize. Using os.Setenv (rather than
			// log.SetLevel) also propagates to the daemon child, which
			// is spawned via os/exec and inherits the env.
			if logLevelFlag != "" {
				_ = os.Setenv(log.EnvLogLevel, logLevelFlag)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// From here an error is loom's, not a usage mistake: main
			// prints it once, with no usage text after it.
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			ctx := context.Background()
			configDir, err := config.GetConfigDir()
			if err != nil {
				configDir = os.TempDir()
			}
			if logErr := log.Initialize(filepath.Join(configDir, "logs"), false); logErr != nil {
				// Non-fatal: loggers fall back to stderr so the app still
				// runs; we only lose the on-disk log file. Surface once so
				// operators notice.
				fmt.Fprintf(os.Stderr, "loom: %v\n", logErr)
			}
			defer log.Close()

			globalDir, err := config.GetGlobalConfigDir()
			if err != nil {
				return fmt.Errorf("failed to get global config dir: %w", err)
			}

			// Resolve workspace context. The registry is the daemon's: it
			// is only read here, to resolve the startup workspace.
			registry, regErr := config.LoadWorkspaceRegistry()
			if regErr != nil {
				log.For("main").Error("workspace_registry_load_failed", "err", regErr)
			}

			var wsCtx *config.WorkspaceContext
			var pendingDir string
			if len(args) > 0 {
				// Directory argument: open workspace at the given path.
				dirPath, err := filepath.Abs(args[0])
				if err != nil {
					return fmt.Errorf("failed to resolve directory path: %w", err)
				}
				info, err := os.Stat(dirPath)
				if err != nil {
					return fmt.Errorf("cannot access %q: %w", dirPath, err)
				}
				if !info.IsDir() {
					return fmt.Errorf("%q is not a directory", dirPath)
				}
				if !git.IsGitRepo(dirPath, nil) {
					return fmt.Errorf("%q is not a git repository", dirPath)
				}
				if regErr == nil {
					if ws := registry.FindByPath(dirPath); ws != nil {
						wsCtx = config.WorkspaceContextFor(ws)
					} else {
						// Unregistered directory: defer to TUI confirmation.
						pendingDir = dirPath
						wsCtx, err = config.GlobalWorkspaceContext()
						if err != nil {
							return fmt.Errorf("failed to get global config dir: %w", err)
						}
					}
				} else {
					return fmt.Errorf("failed to load workspace registry: %w", regErr)
				}
			} else if workspaceFlag != "" {
				// Explicit workspace selection via --workspace flag.
				if regErr != nil {
					return fmt.Errorf("cannot use --workspace: %w", regErr)
				}
				ws := registry.Get(workspaceFlag)
				if ws == nil {
					return fmt.Errorf("workspace %q not found", workspaceFlag)
				}
				wsCtx = config.WorkspaceContextFor(ws)
			} else {
				// No arg, no flag: use global context. The TUI will show the
				// workspace picker if workspaces are registered.
				var err error
				wsCtx, err = config.GlobalWorkspaceContext()
				if err != nil {
					return fmt.Errorf("failed to get global config dir: %w", err)
				}
			}

			// Enforce git repo requirement only when no workspaces are registered
			// and no directory arg was given.
			currentDir, err := filepath.Abs(".")
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}
			if pendingDir == "" && wsCtx.Name == "" && (regErr != nil || len(registry.Workspaces) == 0) && !git.IsGitRepo(currentDir, nil) {
				return fmt.Errorf("error: loom must be run from within a git repository")
			}

			// Load config from resolved workspace context.
			cfg := config.LoadConfigFrom(wsCtx.ConfigDir)

			// Program flag overrides config
			program := cfg.GetProgram()
			if programFlag != "" {
				program = programFlag
			}

			// The TUI is a client of the daemon, which owns the sessions
			// and is started here when none runs, and whose tmux server it
			// uses from here on. A TUI inside a loom pane is one too: the
			// nesting guard is the daemon's.
			client, err := joinDaemon(globalDir)
			if err != nil {
				return err
			}
			// A TUI that loses the daemon joins one again (rejoinDaemon),
			// quietly, under its banner; a newer daemon there makes it exit.
			rejoin := func(spawn bool, say func(string)) (*rpc.Client, error) {
				return rejoinDaemon(globalDir, spawn, say)
			}
			err = app.Run(ctx, client, rejoin, wsCtx.Name, cfg, program, pendingDir, noScriptsFlag)
			if newer := (*newerDaemonError)(nil); errors.As(err, &newer) {
				return fmt.Errorf("loom: the loom daemon was replaced by a newer loom (%s); run loom again", describeBuild(newer.peer))
			}
			return err
		},
	}

	resetCmd = &cobra.Command{
		Use:   "reset",
		Short: "Delete a workspace's instances, tmux sessions, worktrees, and their branches (destructive)",
		Long: "Reset deletes the workspace's stored instances, kills its Loom tmux sessions\n" +
			"(those started in its repo or worktrees directory), and removes all its managed\n" +
			"worktrees INCLUDING their branches — unpushed commits on those branches are\n" +
			"lost. Without --workspace it resets the global workspace. Sessions of other\n" +
			"workspaces are left running. If the tmux cleanup fails, reset stops before\n" +
			"removing any worktree. This cannot be undone; it requires --force.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Reset sweeps the tmux server a daemon started now would
			// pin (daemon.TmuxServer: the last daemon's while it runs),
			// whatever this environment selects: run after a `loom serve
			// stop` from another environment, it would otherwise find
			// none of the workspace's sessions, then delete the worktrees
			// under its live agents. The nesting guard is decided against
			// that server, as the daemon's is.
			globalDir, err := config.GetGlobalConfigDir()
			if err != nil {
				return err
			}
			server, err := daemon.TmuxServer(globalDir)
			if err != nil {
				return fmt.Errorf("find the tmux server: %w", err)
			}
			if err := nestingCheck(server); err != nil {
				return err
			}
			// Reset writes state.json itself, which the daemon would
			// overwrite, until reset is the daemon's client (stage 3C).
			if err := refuseWhileServed(); err != nil {
				return err
			}
			if !resetForceFlag {
				return fmt.Errorf("loom reset deletes the workspace's instances, kills its tmux sessions (only those started in its repo or worktrees directory), and removes its worktrees AND their branches (unpushed commits are lost); re-run with --force to proceed")
			}
			tmux.UseServer(server)
			// Resolve target workspace explicitly — per
			// docs/specs/workspaces.md §3, empty-string fallbacks are
			// disallowed so each subsystem gets a concrete ConfigDir.
			wsCtx, err := resolveResetWorkspace()
			if err != nil {
				return err
			}

			if logErr := log.Initialize(filepath.Join(wsCtx.ConfigDir, "logs"), false); logErr != nil {
				fmt.Fprintf(os.Stderr, "loom: %v\n", logErr)
			}
			defer log.Close()

			state := config.LoadStateFrom(wsCtx.ConfigDir)
			storage, err := session.NewStorage(state, wsCtx.ConfigDir)
			if err != nil {
				return fmt.Errorf("failed to initialize storage: %w", err)
			}
			if err := storage.DeleteAllInstances(); err != nil {
				return fmt.Errorf("failed to reset storage: %w", err)
			}
			fmt.Println("Storage has been reset successfully")

			// Only this workspace's sessions: the tmux server is shared by
			// every loom process and workspace, and this reset deletes only
			// this workspace's records and worktrees. The records are gone,
			// so nothing is claimed.
			registry, regErr := config.LoadWorkspaceRegistry()
			if regErr != nil {
				fmt.Fprintf(os.Stderr, "loom: reading workspace registry: %v (workspaces nested in this one are not told apart)\n", regErr)
				registry = nil
			}
			scope := session.NewSweepScope([]*config.WorkspaceContext{wsCtx}, registry)
			swept, err := session.CleanupOrphanedSessions(nil, scope, resetExecutor())
			if err != nil {
				// Some of this workspace's sessions may still be running,
				// with agents inside the worktrees removed below: stop here.
				return fmt.Errorf("tmux cleanup failed (%d killed, %d kills failed); worktrees and branches were NOT removed, since an agent may still be running in one — re-run once tmux answers: %w",
					swept.Killed, swept.Failed, err)
			}
			fmt.Printf("Tmux sessions have been cleaned up: %d killed, %d left running (started outside this workspace), %d left running (name tmux cannot target exactly)\n",
				swept.Killed, swept.Unowned, swept.Untargetable)

			if err := git.CleanupWorktrees(wsCtx.ConfigDir, nil); err != nil {
				return fmt.Errorf("failed to cleanup worktrees: %w", err)
			}
			fmt.Println("Worktrees have been cleaned up")

			return nil
		},
	}

	debugCmd = &cobra.Command{
		Use:   "debug",
		Short: "Print debug information like config paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			wsCtx, err := config.GlobalWorkspaceContext()
			if err != nil {
				return fmt.Errorf("failed to resolve workspace context: %w", err)
			}
			if logErr := log.Initialize(filepath.Join(wsCtx.ConfigDir, "logs"), false); logErr != nil {
				fmt.Fprintf(os.Stderr, "loom: %v\n", logErr)
			}
			defer log.Close()

			cfg := config.LoadConfigFrom(wsCtx.ConfigDir)
			configJson, _ := json.MarshalIndent(cfg, "", "  ")

			fmt.Printf("Config: %s\n%s\n", filepath.Join(wsCtx.ConfigDir, config.ConfigFileName), configJson)
			fmt.Printf("Log file: %s\n", log.LogFilePath())
			level := os.Getenv(log.EnvLogLevel)
			if level == "" {
				level = "info (default)"
			}
			fmt.Printf("Log level: %s (env %s)\n", level, log.EnvLogLevel)
			format := os.Getenv(log.EnvLogFormat)
			if format == "" {
				format = "text (default)"
			}
			fmt.Printf("Log format: %s (env %s)\n", format, log.EnvLogFormat)

			globalDir, globalErr := config.GetGlobalConfigDir()
			socket := tmux.Socket()
			if socket == "" {
				socket = "default (" + tmux.EnvTmuxSocket + " unset)"
			}
			fmt.Printf("Tmux socket: %s\n", socket)
			// The server a daemon started now pins: the last daemon's
			// while it runs, else this environment's (daemon.TmuxServer).
			var server string
			if globalErr == nil {
				server, err = daemon.TmuxServer(globalDir)
			} else {
				err = globalErr
			}
			if err != nil {
				fmt.Printf("Tmux server: error: %v\n", err)
			} else {
				fmt.Printf("Tmux server: %s (a daemon started now uses it, and its TUIs with it)\n", server)
			}
			if root, ok := claudetmp.Root(); ok {
				fmt.Printf("Claude temp root: %s\n", root)
			} else {
				fmt.Printf("Claude temp root: %s (absent)\n", root)
			}
			switch root, err := cfg.ClaudeTmpArchiveRoot(); {
			case err != nil:
				fmt.Printf("Claude temp archives: %s (claude_tmp_archive_dir ignored: %v; a workspace's: <repo>/.loom/archive/claude-tmp)\n", claudetmp.ArchiveDir(wsCtx.ConfigDir), err)
			case root != "":
				fmt.Printf("Claude temp archives: %s (claude_tmp_archive_dir: each workspace in its own subfolder of %s)\n", session.ClaudeTmpArchiveDir(wsCtx.ConfigDir), root)
			default:
				fmt.Printf("Claude temp archives: %s (a workspace's: <repo>/.loom/archive/claude-tmp; set claude_tmp_archive_dir in %s to move them)\n", claudetmp.ArchiveDir(wsCtx.ConfigDir), filepath.Join(wsCtx.ConfigDir, config.ConfigFileName))
			}
			if globalErr != nil {
				fmt.Printf("Global dir: error: %v\n", globalErr)
			} else {
				fmt.Printf("Global dir: %s (env %s)\n", globalDir, config.EnvGlobalDir)
				switch rec, held := daemon.ReadRecord(globalDir); {
				case !held:
					fmt.Println("Daemon: not running")
				case rec.IsPreDaemon():
					fmt.Printf("Daemon: none; a loom from before the daemon holds the lock (%s)\n", rec)
				case rec.IsDaemon():
					fmt.Printf("Daemon: %s, socket %s, build %s, tmux server %s (its TUIs use it)\n", rec, rec.Socket, rec.Build, recordTmux(rec))
				default:
					fmt.Printf("Daemon: %s, starting, build %s, tmux server %s\n", rec, rec.Build, recordTmux(rec))
				}
				fmt.Printf("Daemon log: %s\n", daemon.LogPath(globalDir))
			}
			// The guard `loom serve` and reset run, on the server they
			// would pin.
			if server == "" {
				fmt.Println("Nesting guard: unknown (no tmux server)")
			} else if err := nestingCheck(server); err != nil {
				fmt.Printf("Nesting guard: would refuse — %v\n", err)
			} else {
				fmt.Println("Nesting guard: ok")
			}

			return nil
		},
	}

	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version number of loom",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("loom version %s\n", version)
			fmt.Printf("https://github.com/aidan-bailey/loom/releases/tag/v%s\n", version)
		},
	}
)

// nestingCheck guards the commands whose startup sweeps tmux sessions (the
// daemon and reset), on the tmux server they pin. A package var so tests
// can stub the environment probe.
var nestingCheck = tmux.CheckNestingFromEnv

// recordTmux names the tmux server a lock record names, for loom debug.
func recordTmux(rec daemon.Record) string {
	if rec.Tmux == "" {
		return "unnamed"
	}
	return rec.Tmux
}

// resolveResetWorkspace resolves the workspace context for the reset
// subcommand. When --workspace is supplied, the named workspace is
// used; otherwise the global context is returned. Per
// docs/specs/workspaces.md §3, every subsystem needs a concrete
// ConfigDir.
// resetExecutor runs reset's tmux commands. A var so tests can make the
// sweep fail without a real tmux.
var resetExecutor = cmd2.MakeExecutor

func resolveResetWorkspace() (*config.WorkspaceContext, error) {
	if resetWorkspaceFlag != "" {
		registry, err := config.LoadWorkspaceRegistry()
		if err != nil {
			return nil, fmt.Errorf("failed to load workspace registry: %w", err)
		}
		ws := registry.Get(resetWorkspaceFlag)
		if ws == nil {
			return nil, fmt.Errorf("workspace %q not found", resetWorkspaceFlag)
		}
		return config.WorkspaceContextFor(ws), nil
	}
	return config.GlobalWorkspaceContext()
}

func init() {
	rpc.SetVersion(version)

	// Match the `loom version` subcommand format so `--version` and the
	// subcommand emit identical output. The default Cobra template is a
	// single "Name version X" line; we add the releases URL to match.
	rootCmd.SetVersionTemplate(
		"loom version {{.Version}}\nhttps://github.com/aidan-bailey/loom/releases/tag/v{{.Version}}\n",
	)

	rootCmd.Flags().StringVarP(&programFlag, "program", "p", "",
		"Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')")
	rootCmd.Flags().BoolVar(&noScriptsFlag, "no-scripts", false,
		"Skip loading ~/.loom/scripts (embedded defaults still load). Use to recover from a broken user script.")
	rootCmd.Flags().StringVarP(&workspaceFlag, "workspace", "w", "",
		"Select workspace by name (bypasses auto-detection)")
	rootCmd.PersistentFlags().StringVar(&logLevelFlag, "log-level", "",
		"Override log level for the Structured logger (debug|info|warn|error). "+
			"Takes precedence over LOOM_LOG_LEVEL.")

	resetCmd.Flags().StringVarP(&resetWorkspaceFlag, "workspace", "w", "",
		"Reset a specific workspace by name (default: global)")
	resetCmd.Flags().BoolVar(&resetForceFlag, "force", false,
		"Confirm the reset: deletes instances, tmux sessions, worktrees, and branches")

	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(resetCmd)
	rootCmd.AddCommand(cmd2.WorkspaceCmd)
	cmd2.StateWriteGuard = refuseWhileServed
	rootCmd.AddCommand(cmd2.AccountCmd)
}

func main() {
	// Legacy-home migration runs before any command so the debug/reset
	// subcommands both observe the new directory. Failure is non-fatal:
	// the migration emits its own diagnostic and subsequent
	// GetConfigDir calls still resolve either ~/.loom or
	// CLAUDE_SQUAD_HOME as a deprecated fallback.
	if err := config.MigrateLegacyHome(); err != nil {
		fmt.Fprintf(os.Stderr, "loom: legacy-home migration failed: %v\n", err)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
