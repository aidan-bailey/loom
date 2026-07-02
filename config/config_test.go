package config

import (
	"fmt"
	"github.com/aidan-bailey/loom/log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	// Initialize the logger before any tests run
	_ = log.Initialize("", false)

	// Prevent LOOM_HOME / CLAUDE_SQUAD_HOME from polluting tests
	os.Unsetenv("LOOM_HOME")
	os.Unsetenv("CLAUDE_SQUAD_HOME")

	exitCode := m.Run()
	log.Close()
	os.Exit(exitCode)
}

func TestGetClaudeCommand(t *testing.T) {
	originalShell := os.Getenv("SHELL")
	originalPath := os.Getenv("PATH")
	defer func() {
		os.Setenv("SHELL", originalShell)
		os.Setenv("PATH", originalPath)
	}()

	t.Run("finds claude in PATH", func(t *testing.T) {
		// Create a temporary directory with a mock claude executable
		tempDir := t.TempDir()
		claudePath := filepath.Join(tempDir, "claude")

		// Create a mock executable
		err := os.WriteFile(claudePath, []byte("#!/bin/bash\necho 'mock claude'"), 0755)
		require.NoError(t, err)

		// Set PATH to include our temp directory
		os.Setenv("PATH", tempDir+":"+originalPath)
		os.Setenv("SHELL", "/bin/bash")

		result, err := GetClaudeCommand()

		assert.NoError(t, err)
		assert.True(t, strings.Contains(result, "claude"))
	})

	t.Run("handles missing claude command", func(t *testing.T) {
		// Set PATH to a directory that doesn't contain claude
		tempDir := t.TempDir()
		os.Setenv("PATH", tempDir)
		os.Setenv("SHELL", "/bin/bash")

		result, err := GetClaudeCommand()

		assert.Error(t, err)
		assert.Equal(t, "", result)
		assert.Contains(t, err.Error(), "claude command not found")
	})

	t.Run("handles empty SHELL environment", func(t *testing.T) {
		// Create a temporary directory with a mock claude executable
		tempDir := t.TempDir()
		claudePath := filepath.Join(tempDir, "claude")

		// Create a mock executable
		err := os.WriteFile(claudePath, []byte("#!/bin/bash\necho 'mock claude'"), 0755)
		require.NoError(t, err)

		// Set PATH and unset SHELL
		os.Setenv("PATH", tempDir+":"+originalPath)
		os.Unsetenv("SHELL")

		result, err := GetClaudeCommand()

		assert.NoError(t, err)
		assert.True(t, strings.Contains(result, "claude"))
	})

	t.Run("handles alias parsing", func(t *testing.T) {
		// Test core alias formats
		aliasRegex := regexp.MustCompile(`(?:aliased to|->|=)\s*([^\s]+)`)

		// Standard alias format
		output := "claude: aliased to /usr/local/bin/claude"
		matches := aliasRegex.FindStringSubmatch(output)
		assert.Len(t, matches, 2)
		assert.Equal(t, "/usr/local/bin/claude", matches[1])

		// Direct path (no alias)
		output = "/usr/local/bin/claude"
		matches = aliasRegex.FindStringSubmatch(output)
		assert.Len(t, matches, 0)
	})
}

func TestDefaultConfig(t *testing.T) {
	t.Run("creates config with default values", func(t *testing.T) {
		config := DefaultConfig()

		assert.NotNil(t, config)
		assert.NotEmpty(t, config.DefaultProgram)
		assert.Equal(t, 1000, config.DaemonPollInterval)
		assert.NotEmpty(t, config.BranchPrefix)
		assert.True(t, strings.HasSuffix(config.BranchPrefix, "/"))
	})

}

func TestGetConfigDir(t *testing.T) {
	t.Run("returns default config directory when env var not set", func(t *testing.T) {
		originalEnv := os.Getenv("LOOM_HOME")
		os.Unsetenv("LOOM_HOME")
		defer os.Setenv("LOOM_HOME", originalEnv)

		configDir, err := GetConfigDir()

		assert.NoError(t, err)
		assert.NotEmpty(t, configDir)
		assert.True(t, strings.HasSuffix(configDir, ".loom"))
		assert.True(t, filepath.IsAbs(configDir))
	})

	t.Run("uses LOOM_HOME when set", func(t *testing.T) {
		customDir := t.TempDir()
		os.Setenv("LOOM_HOME", customDir)
		defer os.Unsetenv("LOOM_HOME")

		configDir, err := GetConfigDir()

		assert.NoError(t, err)
		assert.Equal(t, customDir, configDir)
	})

	t.Run("LOOM_HOME takes precedence over HOME", func(t *testing.T) {
		customDir := t.TempDir()
		os.Setenv("LOOM_HOME", customDir)
		defer os.Unsetenv("LOOM_HOME")

		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", t.TempDir())
		defer os.Setenv("HOME", originalHome)

		configDir, err := GetConfigDir()

		assert.NoError(t, err)
		assert.Equal(t, customDir, configDir)
	})

	t.Run("expands tilde in LOOM_HOME", func(t *testing.T) {
		originalHome := os.Getenv("HOME")
		tempHome := t.TempDir()
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		os.Setenv("LOOM_HOME", "~/.my-loom")
		defer os.Unsetenv("LOOM_HOME")

		configDir, err := GetConfigDir()

		assert.NoError(t, err)
		assert.Equal(t, filepath.Join(tempHome, ".my-loom"), configDir)
	})

	t.Run("expands bare tilde in LOOM_HOME", func(t *testing.T) {
		originalHome := os.Getenv("HOME")
		tempHome := t.TempDir()
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		os.Setenv("LOOM_HOME", "~")
		defer os.Unsetenv("LOOM_HOME")

		configDir, err := GetConfigDir()

		assert.NoError(t, err)
		assert.Equal(t, tempHome, configDir)
	})

	t.Run("rejects relative path in LOOM_HOME", func(t *testing.T) {
		os.Setenv("LOOM_HOME", "relative/path")
		defer os.Unsetenv("LOOM_HOME")

		_, err := GetConfigDir()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LOOM_HOME must be an absolute path")
	})

	t.Run("legacy CLAUDE_SQUAD_HOME still honored when LOOM_HOME unset", func(t *testing.T) {
		os.Unsetenv("LOOM_HOME")
		customDir := t.TempDir()
		os.Setenv("CLAUDE_SQUAD_HOME", customDir)
		defer os.Unsetenv("CLAUDE_SQUAD_HOME")

		configDir, err := GetConfigDir()

		assert.NoError(t, err)
		assert.Equal(t, customDir, configDir)
	})
}

func TestLoadConfig(t *testing.T) {
	t.Run("returns default config when file doesn't exist", func(t *testing.T) {
		// Use a temporary home directory to avoid interfering with real config
		originalHome := os.Getenv("HOME")
		tempHome := t.TempDir()
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		config := LoadConfig()

		assert.NotNil(t, config)
		assert.NotEmpty(t, config.DefaultProgram)
		assert.Equal(t, 1000, config.DaemonPollInterval)
		assert.NotEmpty(t, config.BranchPrefix)
	})

	t.Run("loads valid config file", func(t *testing.T) {
		// Create a temporary config directory
		tempHome := t.TempDir()
		configDir := filepath.Join(tempHome, ".loom")
		err := os.MkdirAll(configDir, 0755)
		require.NoError(t, err)

		// Create a test config file
		configPath := filepath.Join(configDir, ConfigFileName)
		configContent := `{
			"default_program": "test-claude",
			"daemon_poll_interval": 2000,
			"branch_prefix": "test/"
		}`
		err = os.WriteFile(configPath, []byte(configContent), 0644)
		require.NoError(t, err)

		// Override HOME environment
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		config := LoadConfig()

		assert.NotNil(t, config)
		assert.Equal(t, "test-claude", config.DefaultProgram)
		assert.Equal(t, 2000, config.DaemonPollInterval)
		assert.Equal(t, "test/", config.BranchPrefix)
	})

	t.Run("returns default config on invalid JSON", func(t *testing.T) {
		// Create a temporary config directory
		tempHome := t.TempDir()
		configDir := filepath.Join(tempHome, ".loom")
		err := os.MkdirAll(configDir, 0755)
		require.NoError(t, err)

		// Create an invalid config file
		configPath := filepath.Join(configDir, ConfigFileName)
		invalidContent := `{"invalid": json content}`
		err = os.WriteFile(configPath, []byte(invalidContent), 0644)
		require.NoError(t, err)

		// Override HOME environment
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		config := LoadConfig()

		// Should return default config when JSON is invalid
		assert.NotNil(t, config)
		assert.NotEmpty(t, config.DefaultProgram)
		assert.Equal(t, 1000, config.DaemonPollInterval) // Default value
	})
}

// TestLoadConfigFrom_CorruptFileQuarantined drives F8 for config.json.
// Mirrors the state.json policy: rename corrupt input to
// config.json.corrupted-<timestamp> so the next save cannot silently
// clobber the evidence with defaults.
func TestLoadConfigFrom_CorruptFileQuarantined(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ConfigFileName)
	require.NoError(t, os.WriteFile(configPath, []byte("{not-json"), 0644))

	cfg := LoadConfigFrom(dir)
	assert.NotNil(t, cfg)
	assert.NotEmpty(t, cfg.DefaultProgram, "defaults must still be returned")

	_, err := os.Stat(configPath)
	assert.True(t, os.IsNotExist(err), "corrupt config.json must be moved aside")

	entries, err := os.ReadDir(dir)
	assert.NoError(t, err)
	var quarantined string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ConfigFileName+".corrupted-") {
			quarantined = filepath.Join(dir, e.Name())
			break
		}
	}
	assert.NotEmpty(t, quarantined, "quarantined corrupt config must exist with .corrupted-<ts> suffix")
	if quarantined != "" {
		body, err := os.ReadFile(quarantined)
		assert.NoError(t, err)
		assert.Equal(t, "{not-json", string(body))
	}
}

func TestGetProgram(t *testing.T) {
	t.Run("no profiles returns default_program as-is", func(t *testing.T) {
		cfg := &Config{DefaultProgram: "/usr/local/bin/claude"}
		assert.Equal(t, "/usr/local/bin/claude", cfg.GetProgram())
	})

	t.Run("profiles defined and default_program matches a profile name", func(t *testing.T) {
		cfg := &Config{
			DefaultProgram: "claude",
			Profiles: []Profile{
				{Name: "claude", Program: "/usr/local/bin/claude"},
				{Name: "aider", Program: "aider --model ollama_chat/gemma3:1b"},
			},
		}
		assert.Equal(t, "/usr/local/bin/claude", cfg.GetProgram())
	})

	t.Run("profiles defined but default_program does not match any profile", func(t *testing.T) {
		cfg := &Config{
			DefaultProgram: "some-other-program",
			Profiles: []Profile{
				{Name: "claude", Program: "/usr/local/bin/claude"},
			},
		}
		assert.Equal(t, "some-other-program", cfg.GetProgram())
	})
}

func TestRemoteControlEnabled(t *testing.T) {
	t.Run("nil (absent from config) defaults to enabled", func(t *testing.T) {
		cfg := &Config{}
		assert.True(t, cfg.RemoteControlEnabled())
	})

	t.Run("explicit true is enabled", func(t *testing.T) {
		cfg := &Config{ClaudeRemoteControl: boolPtr(true)}
		assert.True(t, cfg.RemoteControlEnabled())
	})

	t.Run("explicit false disables", func(t *testing.T) {
		cfg := &Config{ClaudeRemoteControl: boolPtr(false)}
		assert.False(t, cfg.RemoteControlEnabled())
	})

	t.Run("DefaultConfig enables it", func(t *testing.T) {
		assert.True(t, DefaultConfig().RemoteControlEnabled())
	})
}

func TestPermissionMode(t *testing.T) {
	t.Run("nil (absent from config) defaults to \"default\"", func(t *testing.T) {
		cfg := &Config{}
		assert.Equal(t, "default", cfg.PermissionMode())
	})

	t.Run("explicit value round-trips", func(t *testing.T) {
		cfg := &Config{ClaudePermissionMode: stringPtr("acceptEdits")}
		assert.Equal(t, "acceptEdits", cfg.PermissionMode())
	})

	t.Run("explicit \"default\" round-trips", func(t *testing.T) {
		cfg := &Config{ClaudePermissionMode: stringPtr("default")}
		assert.Equal(t, "default", cfg.PermissionMode())
	})

	t.Run("DefaultConfig sets \"default\" explicitly", func(t *testing.T) {
		cfg := DefaultConfig()
		if assert.NotNil(t, cfg.ClaudePermissionMode) {
			assert.Equal(t, "default", *cfg.ClaudePermissionMode)
		}
		assert.Equal(t, "default", cfg.PermissionMode())
	})
}

func TestClaudePermissionModes(t *testing.T) {
	assert.Equal(t, []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"}, ClaudePermissionModes)
}

func TestGetProfiles(t *testing.T) {
	t.Run("no profiles returns single synthetic profile", func(t *testing.T) {
		cfg := &Config{DefaultProgram: "/usr/local/bin/claude"}
		profiles := cfg.GetProfiles()
		assert.Len(t, profiles, 1)
		assert.Equal(t, "/usr/local/bin/claude", profiles[0].Name)
		assert.Equal(t, "/usr/local/bin/claude", profiles[0].Program)
	})

	t.Run("profiles defined returns them with default first", func(t *testing.T) {
		cfg := &Config{
			DefaultProgram: "aider",
			Profiles: []Profile{
				{Name: "claude", Program: "/usr/local/bin/claude"},
				{Name: "aider", Program: "aider --model gemma"},
			},
		}
		profiles := cfg.GetProfiles()
		assert.Len(t, profiles, 2)
		assert.Equal(t, "aider", profiles[0].Name)
		assert.Equal(t, "claude", profiles[1].Name)
	})

	t.Run("profiles defined but default not matching preserves order", func(t *testing.T) {
		cfg := &Config{
			DefaultProgram: "other",
			Profiles: []Profile{
				{Name: "claude", Program: "/usr/local/bin/claude"},
				{Name: "aider", Program: "aider --model gemma"},
			},
		}
		profiles := cfg.GetProfiles()
		assert.Len(t, profiles, 2)
		assert.Equal(t, "claude", profiles[0].Name)
		assert.Equal(t, "aider", profiles[1].Name)
	})
}

func TestSaveConfigTo(t *testing.T) {
	t.Run("saves config to file", func(t *testing.T) {
		configDir := t.TempDir()

		testConfig := &Config{
			DefaultProgram:     "test-program",
			DaemonPollInterval: 3000,
			BranchPrefix:       "test-branch/",
		}

		err := SaveConfigTo(testConfig, configDir)
		assert.NoError(t, err)

		configPath := filepath.Join(configDir, ConfigFileName)
		assert.FileExists(t, configPath)

		loadedConfig := LoadConfigFrom(configDir)
		assert.Equal(t, testConfig.DefaultProgram, loadedConfig.DefaultProgram)
		assert.Equal(t, testConfig.DaemonPollInterval, loadedConfig.DaemonPollInterval)
		assert.Equal(t, testConfig.BranchPrefix, loadedConfig.BranchPrefix)
	})
}

func TestConfigMutateIsRaceSafeWithGetBranchPrefix(t *testing.T) {
	cfg := DefaultConfig()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 1000; i++ {
			cfg.Mutate(func(c *Config) { c.BranchPrefix = fmt.Sprintf("user-%d/", i) })
		}
		close(done)
	}()

	for i := 0; i < 1000; i++ {
		_ = cfg.GetBranchPrefix()
	}
	<-done
}
