package main

import (
	"claude-squad/app"
	cmd2 "claude-squad/cmd"
	"claude-squad/config"
	"claude-squad/daemon"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	version            = "1.0.17"
	programFlag        string
	autoYesFlag        bool
	noScriptsFlag      bool
	daemonFlag         bool
	configDirFlag      string
	workspaceFlag      string
	resetWorkspaceFlag string
	logLevelFlag       string
	rootCmd            = &cobra.Command{
		Use:   "claude-squad [directory]",
		Short: "Claude Squad - Manage multiple AI agents like Claude Code, Aider, Codex, and Amp.",
		Args:  cobra.MaximumNArgs(1),
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
			ctx := context.Background()
			configDir, err := config.GetConfigDir()
			if err != nil {
				configDir = os.TempDir()
			}
			log.Initialize(filepath.Join(configDir, "logs"), daemonFlag)
			defer log.Close()

			if daemonFlag {
				// Daemon child inherits --config-dir from the parent (see
				// daemon.LaunchDaemon). When absent, fall back to the
				// global config dir explicitly so state files go to a
				// concrete location — never an empty-string shim.
				daemonDir := configDirFlag
				var cfg *config.Config
				if daemonDir == "" {
					cfg = config.LoadConfigFromGlobal()
					globalDir, gErr := config.GetConfigDir()
					if gErr != nil {
						return fmt.Errorf("failed to get global config directory: %w", gErr)
					}
					daemonDir = globalDir
				} else {
					cfg = config.LoadConfigFrom(daemonDir)
				}
				daemonCtx := &config.WorkspaceContext{ConfigDir: daemonDir}
				err := daemon.RunDaemon(cfg, daemonCtx)
				if err != nil {
					log.ErrorLog.Printf("failed to start daemon: %v", err)
				}
				return err
			}

			// Resolve workspace context.
			registry, regErr := config.LoadWorkspaceRegistry()
			if regErr != nil {
				log.ErrorLog.Printf("failed to load workspace registry: %v", regErr)
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

			// Update LastUsed when a workspace is selected.
			if wsCtx.Name != "" && regErr == nil {
				_ = registry.UpdateLastUsed(wsCtx.Name)
			}

			// Enforce git repo requirement only when no workspaces are registered
			// and no directory arg was given.
			currentDir, err := filepath.Abs(".")
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}
			if pendingDir == "" && wsCtx.Name == "" && (regErr != nil || len(registry.Workspaces) == 0) && !git.IsGitRepo(currentDir, nil) {
				return fmt.Errorf("error: claude-squad must be run from within a git repository")
			}

			// Load config from resolved workspace context.
			cfg := config.LoadConfigFrom(wsCtx.ConfigDir)

			// Program flag overrides config
			program := cfg.GetProgram()
			if programFlag != "" {
				program = programFlag
			}
			// AutoYes flag overrides config
			autoYes := cfg.AutoYes
			if autoYesFlag {
				autoYes = true
			}
			if autoYes {
				defer func() {
					if err := daemon.LaunchDaemon(wsCtx); err != nil {
						log.ErrorLog.Printf("failed to launch daemon: %v", err)
					}
				}()
			}
			// Kill any daemon that's running.
			if err := daemon.StopDaemon(wsCtx); err != nil {
				log.ErrorLog.Printf("failed to stop daemon: %v", err)
			}

			return app.Run(ctx, wsCtx, registry, cfg, program, autoYes, pendingDir, noScriptsFlag)
		},
	}

	resetCmd = &cobra.Command{
		Use:   "reset",
		Short: "Reset all stored instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve target workspace explicitly — per
			// docs/specs/workspaces.md §3, empty-string fallbacks are
			// disallowed so each subsystem gets a concrete ConfigDir.
			wsCtx, err := resolveResetWorkspace()
			if err != nil {
				return err
			}

			log.Initialize(filepath.Join(wsCtx.ConfigDir, "logs"), false)
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

			if err := tmux.CleanupSessions(cmd2.MakeExecutor()); err != nil {
				return fmt.Errorf("failed to cleanup tmux sessions: %w", err)
			}
			fmt.Println("Tmux sessions have been cleaned up")

			if err := git.CleanupWorktrees(wsCtx.ConfigDir, nil); err != nil {
				return fmt.Errorf("failed to cleanup worktrees: %w", err)
			}
			fmt.Println("Worktrees have been cleaned up")

			if err := daemon.StopDaemon(wsCtx); err != nil {
				return err
			}
			fmt.Println("daemon has been stopped")

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
			log.Initialize(filepath.Join(wsCtx.ConfigDir, "logs"), false)
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

			return nil
		},
	}

	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version number of claude-squad",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("claude-squad version %s\n", version)
			fmt.Printf("https://github.com/smtg-ai/claude-squad/releases/tag/v%s\n", version)
		},
	}
)

// resolveResetWorkspace resolves the workspace context for the reset
// subcommand. When --workspace is supplied, the named workspace is
// used; otherwise the global context is returned. Per
// docs/specs/workspaces.md §3, every subsystem needs a concrete
// ConfigDir.
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
	rootCmd.Flags().StringVarP(&programFlag, "program", "p", "",
		"Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')")
	rootCmd.Flags().BoolVarP(&autoYesFlag, "autoyes", "y", false,
		"[experimental] If enabled, all instances will automatically accept prompts")
	rootCmd.Flags().BoolVar(&noScriptsFlag, "no-scripts", false,
		"Skip loading ~/.claude-squad/scripts (embedded defaults still load). Use to recover from a broken user script.")
	rootCmd.Flags().StringVarP(&workspaceFlag, "workspace", "w", "",
		"Select workspace by name (bypasses auto-detection)")
	rootCmd.Flags().BoolVar(&daemonFlag, "daemon", false, "Run a program that loads all sessions"+
		" and runs autoyes mode on them.")
	rootCmd.Flags().StringVar(&configDirFlag, "config-dir", "", "Config directory (internal use by daemon)")
	rootCmd.PersistentFlags().StringVar(&logLevelFlag, "log-level", "",
		"Override log level for the Structured logger (debug|info|warn|error). "+
			"Takes precedence over CLAUDE_SQUAD_LOG_LEVEL.")

	// Hide internal flags. A MarkHidden failure means the flag name is
	// wrong — a programmer error that the panic would have obscured
	// with a stack trace instead of naming the offending flag.
	for _, name := range []string{"daemon", "config-dir"} {
		if err := rootCmd.Flags().MarkHidden(name); err != nil {
			fmt.Fprintf(os.Stderr, "mark hidden %q: %v\n", name, err)
			os.Exit(2)
		}
	}

	resetCmd.Flags().StringVarP(&resetWorkspaceFlag, "workspace", "w", "",
		"Reset a specific workspace by name (default: global)")

	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(resetCmd)
	rootCmd.AddCommand(cmd2.WorkspaceCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}
