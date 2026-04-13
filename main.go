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
	version       = "1.0.17"
	programFlag   string
	autoYesFlag   bool
	daemonFlag    bool
	configDirFlag string
	workspaceFlag string
	rootCmd       = &cobra.Command{
		Use:   "claude-squad [directory]",
		Short: "Claude Squad - Manage multiple AI agents like Claude Code, Aider, Codex, and Amp.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			log.Initialize(daemonFlag)
			defer log.Close()

			if daemonFlag {
				cfg := config.LoadConfigFrom(configDirFlag)
				err := daemon.RunDaemon(cfg, configDirFlag)
				log.ErrorLog.Printf("failed to start daemon %v", err)
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
				if !git.IsGitRepo(dirPath) {
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
			if pendingDir == "" && wsCtx.Name == "" && (regErr != nil || len(registry.Workspaces) == 0) && !git.IsGitRepo(currentDir) {
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
					if err := daemon.LaunchDaemon(wsCtx.ConfigDir); err != nil {
						log.ErrorLog.Printf("failed to launch daemon: %v", err)
					}
				}()
			}
			// Kill any daemon that's running.
			if err := daemon.StopDaemon(wsCtx.ConfigDir); err != nil {
				log.ErrorLog.Printf("failed to stop daemon: %v", err)
			}

			return app.Run(ctx, wsCtx, registry, cfg, program, autoYes, pendingDir)
		},
	}

	resetCmd = &cobra.Command{
		Use:   "reset",
		Short: "Reset all stored instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			state := config.LoadState()
			storage, err := session.NewStorage(state, "")
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

			if err := git.CleanupWorktrees(""); err != nil {
				return fmt.Errorf("failed to cleanup worktrees: %w", err)
			}
			fmt.Println("Worktrees have been cleaned up")

			// Kill any daemon that's running.
			if err := daemon.StopDaemon(""); err != nil {
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
			log.Initialize(false)
			defer log.Close()

			cfg := config.LoadConfig()

			configDir, err := config.GetConfigDir()
			if err != nil {
				return fmt.Errorf("failed to get config directory: %w", err)
			}
			configJson, _ := json.MarshalIndent(cfg, "", "  ")

			fmt.Printf("Config: %s\n%s\n", filepath.Join(configDir, config.ConfigFileName), configJson)

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

func init() {
	rootCmd.Flags().StringVarP(&programFlag, "program", "p", "",
		"Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')")
	rootCmd.Flags().BoolVarP(&autoYesFlag, "autoyes", "y", false,
		"[experimental] If enabled, all instances will automatically accept prompts")
	rootCmd.Flags().StringVarP(&workspaceFlag, "workspace", "w", "",
		"Select workspace by name (bypasses auto-detection)")
	rootCmd.Flags().BoolVar(&daemonFlag, "daemon", false, "Run a program that loads all sessions"+
		" and runs autoyes mode on them.")
	rootCmd.Flags().StringVar(&configDirFlag, "config-dir", "", "Config directory (internal use by daemon)")

	// Hide internal flags
	for _, name := range []string{"daemon", "config-dir"} {
		if err := rootCmd.Flags().MarkHidden(name); err != nil {
			panic(err)
		}
	}

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
