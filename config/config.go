package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/aidan-bailey/loom/log"
)

// ConfigFileName is the on-disk filename of the Loom user config,
// resolved relative to the config directory (LOOM_HOME or ~/.loom).
const (
	ConfigFileName = "config.json"
	defaultProgram = "claude"
)

// EnvHome names the override env var for the application's config
// directory. Legacy CLAUDE_SQUAD_HOME is honored as a deprecated
// fallback with a one-time warning so shell configs continue working
// across the rename from claude-squad to loom.
const (
	EnvHome       = "LOOM_HOME"
	legacyEnvHome = "CLAUDE_SQUAD_HOME"
)

// GetConfigDir returns the path to the application's configuration directory.
// If LOOM_HOME is set, that value is used directly as the config directory.
// CLAUDE_SQUAD_HOME is honored as a deprecated fallback with a one-time
// warning. The value must be an absolute path (after ~ expansion). Falls
// back to ~/.loom otherwise.
func GetConfigDir() (string, error) {
	if envDir := log.GetEnvWithLegacy(EnvHome, legacyEnvHome); envDir != "" {
		if envDir == "~" || strings.HasPrefix(envDir, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to expand ~ in %s: %w", EnvHome, err)
			}
			envDir = filepath.Join(homeDir, envDir[1:])
		}
		if !filepath.IsAbs(envDir) {
			return "", fmt.Errorf("%s must be an absolute path, got: %s", EnvHome, envDir)
		}
		return envDir, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config home directory: %w", err)
	}
	return filepath.Join(homeDir, ".loom"), nil
}

// Profile is a named shortcut for a program invocation. Profiles let
// users store multiple agent configurations (e.g. "aider-gpt4",
// "claude-fast") and pick between them at session creation or by
// setting DefaultProgram to a profile's Name.
type Profile struct {
	// Name is the short identifier shown in the profile picker. Must be
	// unique within a Config's Profiles list.
	Name string `json:"name"`
	// Program is the literal command string run when this profile is
	// selected (e.g. "aider --model gpt-4"). No templating is applied.
	Program string `json:"program"`
}

// Config represents the application configuration
type Config struct {
	// mu guards mutation of every field below once the settings overlay
	// makes Config mutable at runtime. Before the settings overlay,
	// Config was loaded once and never mutated, so nothing raced on it.
	// scriptHost.BranchPrefix() (app/app_scripts.go) reads BranchPrefix
	// from the Lua dispatch goroutine — a tea.Cmd body running
	// concurrently with Update — while the settings overlay writes it
	// from the main goroutine. Mutate/GetBranchPrefix close that race
	// the same way config.State.mu already guards InstancesData/
	// HelpScreensSeen against the same class of race. Unexported, so
	// encoding/json skips it (same precedent as State.mu).
	mu sync.RWMutex

	// DefaultProgram is the default program to run in new instances
	DefaultProgram string `json:"default_program"`
	// DaemonPollInterval is retained for config.json backward compatibility
	// only — its consumer (the background daemon) has been removed.
	DaemonPollInterval int `json:"daemon_poll_interval"`
	// BranchPrefix is the prefix used for git branches created by the application.
	BranchPrefix string `json:"branch_prefix"`
	// Profiles is a list of named program profiles.
	Profiles []Profile `json:"profiles,omitempty"`
	// ClaudeRemoteControl controls whether new Claude sessions launch
	// with `--remote-control` (named after the session title). It is a
	// pointer so a config file predating this field (nil) is treated as
	// enabled rather than taking the bool zero value; only an explicit
	// false disables it. Read it through RemoteControlEnabled.
	ClaudeRemoteControl *bool `json:"claude_remote_control,omitempty"`
	// ClaudePermissionMode is the --permission-mode value new Claude
	// sessions launch with. Unlike ClaudeRemoteControl, DefaultConfig
	// sets this explicitly to "default" rather than leaving it nil — nil
	// only occurs for a config.json predating this field, and is
	// treated identically to "default" (no flag injected; Claude's own
	// default applies). Read it through PermissionMode.
	ClaudePermissionMode *string `json:"claude_permission_mode,omitempty"`
}

// ClaudePermissionModes lists the values --permission-mode accepts, in
// the order the Claude Preferences screen cycles through them.
var ClaudePermissionModes = []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"}

// Mutate runs fn with the write lock held. Callers outside this package
// (the settings overlay) use this instead of writing exported fields
// directly, so a concurrent GetBranchPrefix cannot observe a torn write.
// fn must not call GetBranchPrefix or Mutate itself (would deadlock).
func (c *Config) Mutate(fn func(*Config)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
}

// GetBranchPrefix returns BranchPrefix under a read lock. Use this
// instead of reading the field directly from any goroutine other than
// the one currently calling Mutate — in practice, scriptHost.BranchPrefix,
// which runs on the Lua dispatch goroutine.
func (c *Config) GetBranchPrefix() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.BranchPrefix
}

// RemoteControlEnabled reports whether new Claude sessions should launch
// with the --remote-control flag. Defaults to true when unset so the
// feature is on out of the box; only an explicit false disables it.
func (c *Config) RemoteControlEnabled() bool {
	return c.ClaudeRemoteControl == nil || *c.ClaudeRemoteControl
}

// PermissionMode returns the configured --permission-mode value under a
// read lock, defaulting to "default" when unset (nil).
func (c *Config) PermissionMode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ClaudePermissionMode == nil {
		return "default"
	}
	return *c.ClaudePermissionMode
}

// GetProgram returns the program to run. If Profiles is non-empty and
// DefaultProgram matches a profile name, that profile's Program is returned.
// Otherwise DefaultProgram is returned as-is.
func (c *Config) GetProgram() string {
	for _, p := range c.Profiles {
		if p.Name == c.DefaultProgram {
			return p.Program
		}
	}
	return c.DefaultProgram
}

// GetProfiles returns a unified list of profiles. If Profiles is defined,
// those are returned with the default profile first. Otherwise, a single
// profile is synthesized from DefaultProgram.
func (c *Config) GetProfiles() []Profile {
	if len(c.Profiles) == 0 {
		return []Profile{{Name: c.DefaultProgram, Program: c.DefaultProgram}}
	}
	// Reorder so the default profile comes first.
	profiles := make([]Profile, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		if p.Name == c.DefaultProgram {
			profiles = append(profiles, p)
			break
		}
	}
	for _, p := range c.Profiles {
		if p.Name != c.DefaultProgram {
			profiles = append(profiles, p)
		}
	}
	return profiles
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	program, err := GetClaudeCommand()
	if err != nil {
		log.For("config").Error("get_claude_command_failed", "err", err)
		program = defaultProgram
	}

	return &Config{
		DefaultProgram:     program,
		DaemonPollInterval: 1000,
		BranchPrefix: func() string {
			user, err := user.Current()
			if err != nil || user == nil || user.Username == "" {
				log.For("config").Error("get_current_user_failed", "err", err)
				return "session/"
			}
			return fmt.Sprintf("%s/", strings.ToLower(user.Username))
		}(),
		ClaudeRemoteControl:  boolPtr(true),
		ClaudePermissionMode: stringPtr("default"),
	}
}

// boolPtr returns a pointer to b. Used for config fields whose absent
// (nil) state must be distinguished from the false zero value.
func boolPtr(b bool) *bool { return &b }

// stringPtr returns a pointer to s. Used for config fields whose absent
// (nil) state must be distinguished from the empty-string zero value.
func stringPtr(s string) *string { return &s }

// GetClaudeCommand attempts to find the "claude" command in the user's shell
// It checks in the following order:
// 1. Shell alias resolution: using "which" command
// 2. PATH lookup
//
// If both fail, it returns an error.
func GetClaudeCommand() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash" // Default to bash if SHELL is not set
	}

	// Force the shell to load the user's profile and then run the command
	// For zsh, source .zshrc; for bash, source .bashrc
	var shellCmd string
	if strings.Contains(shell, "zsh") {
		shellCmd = "source ~/.zshrc &>/dev/null || true; which claude"
	} else if strings.Contains(shell, "bash") {
		shellCmd = "source ~/.bashrc &>/dev/null || true; which claude"
	} else {
		shellCmd = "which claude"
	}

	cmd := exec.Command(shell, "-c", shellCmd)
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		path := strings.TrimSpace(string(output))
		if path != "" {
			// Check if the output is an alias definition and extract the actual path
			// Handle formats like "claude: aliased to /path/to/claude" or other shell-specific formats
			aliasRegex := regexp.MustCompile(`(?:aliased to|->|=)\s*([^\s]+)`)
			matches := aliasRegex.FindStringSubmatch(path)
			if len(matches) > 1 {
				path = matches[1]
			}
			return path, nil
		}
	}

	// Otherwise, try to find in PATH directly
	claudePath, err := exec.LookPath("claude")
	if err == nil {
		return claudePath, nil
	}

	return "", fmt.Errorf("claude command not found in aliases or PATH")
}

// LoadConfig loads configuration from the default config directory.
// It is a convenience wrapper around LoadConfigFrom(""). If the config
// file does not yet exist, a default config is written to disk.
func LoadConfig() *Config {
	configDir, err := GetConfigDir()
	if err != nil {
		log.For("config").Error("get_config_dir_failed", "err", err)
		return DefaultConfig()
	}

	// If no config file exists, write defaults so future runs are stable.
	if _, err := os.Stat(filepath.Join(configDir, ConfigFileName)); os.IsNotExist(err) {
		defaultCfg := DefaultConfig()
		if saveErr := SaveConfigTo(defaultCfg, configDir); saveErr != nil {
			log.For("config").Warn("save_default_config_failed", "err", saveErr)
		}
		return defaultCfg
	}

	return LoadConfigFrom(configDir)
}

// SaveConfigTo saves the configuration to an explicit directory.
func SaveConfigTo(config *Config, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(dir, ConfigFileName)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return AtomicWriteFile(configPath, data, 0644)
}

// LoadConfigFromGlobal loads configuration from the global
// (LOOM_HOME or ~/.loom) directory. Prefer this over
// LoadConfigFrom("") at call sites that truly want the global config —
// the explicit name prevents workspace-context leaks.
func LoadConfigFromGlobal() *Config {
	dir, err := GetConfigDir()
	if err != nil {
		log.For("config").Error("get_global_config_dir_failed", "err", err)
		return DefaultConfig()
	}
	return LoadConfigFrom(dir)
}

// LoadConfigFrom loads configuration from an explicit directory.
// Empty `dir` is a soft shim: a warning is logged and the global config
// directory is used. Per docs/specs/workspaces.md §3 internal callers
// must pass a resolved WorkspaceContext.ConfigDir.
func LoadConfigFrom(dir string) *Config {
	if dir == "" {
		log.For("config").Warn("load_config_empty_dir", "action", "fallback_to_global", "fix", "call LoadConfigFromGlobal() explicitly")
		resolved, err := GetConfigDir()
		if err != nil {
			log.For("config").Error("get_config_dir_failed", "err", err)
			return DefaultConfig()
		}
		dir = resolved
	}
	configPath := filepath.Join(dir, ConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return DefaultConfig()
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		quarantined := quarantineCorruptFile(configPath)
		if quarantined != "" {
			log.For("config").Error("config_file_corrupt", "path", configPath, "quarantine_path", quarantined, "action", "starting_with_defaults")
		} else {
			log.For("config").Error("config_file_corrupt", "path", configPath, "err", err, "action", "starting_with_defaults")
		}
		return DefaultConfig()
	}
	return &cfg
}
