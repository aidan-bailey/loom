package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetGlobalConfigDir(t *testing.T) {
	t.Run("returns ~/.claude-squad regardless of CLAUDE_SQUAD_HOME", func(t *testing.T) {
		customDir := t.TempDir()
		os.Setenv("CLAUDE_SQUAD_HOME", customDir)
		defer os.Unsetenv("CLAUDE_SQUAD_HOME")

		globalDir, err := GetGlobalConfigDir()
		assert.NoError(t, err)
		assert.NotEqual(t, customDir, globalDir)
		assert.True(t, filepath.IsAbs(globalDir))
		assert.Contains(t, globalDir, ".claude-squad")
	})
}

func TestLoadWorkspaceRegistry(t *testing.T) {
	t.Run("returns empty registry when file doesn't exist", func(t *testing.T) {
		tempHome := t.TempDir()
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		reg, err := LoadWorkspaceRegistry()
		assert.NoError(t, err)
		assert.NotNil(t, reg)
		assert.Empty(t, reg.Workspaces)
		assert.Empty(t, reg.LastUsed)
	})

	t.Run("loads valid registry file", func(t *testing.T) {
		tempHome := t.TempDir()
		globalDir := filepath.Join(tempHome, ".claude-squad")
		require.NoError(t, os.MkdirAll(globalDir, 0755))

		content := `{"workspaces":[{"name":"test","path":"/tmp/repo","added_at":"2025-01-01T00:00:00Z"}],"last_used":"test"}`
		require.NoError(t, os.WriteFile(filepath.Join(globalDir, "workspaces.json"), []byte(content), 0644))

		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		reg, err := LoadWorkspaceRegistry()
		assert.NoError(t, err)
		assert.Len(t, reg.Workspaces, 1)
		assert.Equal(t, "test", reg.Workspaces[0].Name)
		assert.Equal(t, "/tmp/repo", reg.Workspaces[0].Path)
		assert.Equal(t, "test", reg.LastUsed)
	})
}

func TestWorkspaceRegistryAdd(t *testing.T) {
	t.Run("adds workspace with absolute path", func(t *testing.T) {
		tempHome := t.TempDir()
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		repoDir := t.TempDir()
		// Create a .gitignore-friendly directory
		reg := &WorkspaceRegistry{}
		err := reg.Add("myrepo", repoDir)
		assert.NoError(t, err)
		assert.Len(t, reg.Workspaces, 1)
		assert.Equal(t, "myrepo", reg.Workspaces[0].Name)
		assert.Equal(t, repoDir, reg.Workspaces[0].Path)
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		tempHome := t.TempDir()
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		repo1 := t.TempDir()
		repo2 := t.TempDir()
		reg := &WorkspaceRegistry{}
		require.NoError(t, reg.Add("myrepo", repo1))
		err := reg.Add("myrepo", repo2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("rejects duplicate path", func(t *testing.T) {
		tempHome := t.TempDir()
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		repoDir := t.TempDir()
		reg := &WorkspaceRegistry{}
		require.NoError(t, reg.Add("repo1", repoDir))
		err := reg.Add("repo2", repoDir)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})
}

func TestWorkspaceRegistryRemove(t *testing.T) {
	t.Run("removes existing workspace", func(t *testing.T) {
		tempHome := t.TempDir()
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		repoDir := t.TempDir()
		reg := &WorkspaceRegistry{}
		require.NoError(t, reg.Add("myrepo", repoDir))
		require.Len(t, reg.Workspaces, 1)

		err := reg.Remove("myrepo")
		assert.NoError(t, err)
		assert.Empty(t, reg.Workspaces)
	})

	t.Run("clears last_used when removing that workspace", func(t *testing.T) {
		tempHome := t.TempDir()
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		repoDir := t.TempDir()
		reg := &WorkspaceRegistry{}
		require.NoError(t, reg.Add("myrepo", repoDir))
		reg.LastUsed = "myrepo"

		require.NoError(t, reg.Remove("myrepo"))
		assert.Empty(t, reg.LastUsed)
	})

	t.Run("returns error for non-existent workspace", func(t *testing.T) {
		reg := &WorkspaceRegistry{}
		err := reg.Remove("nope")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestWorkspaceRegistryFindByPath(t *testing.T) {
	t.Run("finds exact match", func(t *testing.T) {
		reg := &WorkspaceRegistry{
			Workspaces: []Workspace{
				{Name: "repo", Path: "/home/user/repo"},
			},
		}
		ws := reg.FindByPath("/home/user/repo")
		assert.NotNil(t, ws)
		assert.Equal(t, "repo", ws.Name)
	})

	t.Run("finds parent path match", func(t *testing.T) {
		reg := &WorkspaceRegistry{
			Workspaces: []Workspace{
				{Name: "repo", Path: "/home/user/repo"},
			},
		}
		ws := reg.FindByPath("/home/user/repo/src/main.go")
		assert.NotNil(t, ws)
		assert.Equal(t, "repo", ws.Name)
	})

	t.Run("returns nil for no match", func(t *testing.T) {
		reg := &WorkspaceRegistry{
			Workspaces: []Workspace{
				{Name: "repo", Path: "/home/user/repo"},
			},
		}
		ws := reg.FindByPath("/home/user/other")
		assert.Nil(t, ws)
	})

	t.Run("does not match partial directory names", func(t *testing.T) {
		reg := &WorkspaceRegistry{
			Workspaces: []Workspace{
				{Name: "repo", Path: "/home/user/repo"},
			},
		}
		ws := reg.FindByPath("/home/user/repo-fork")
		assert.Nil(t, ws)
	})
}

func TestWorkspaceRegistryGet(t *testing.T) {
	t.Run("finds by name", func(t *testing.T) {
		reg := &WorkspaceRegistry{
			Workspaces: []Workspace{
				{Name: "alpha", Path: "/a"},
				{Name: "beta", Path: "/b"},
			},
		}
		ws := reg.Get("beta")
		assert.NotNil(t, ws)
		assert.Equal(t, "/b", ws.Path)
	})

	t.Run("returns nil for unknown name", func(t *testing.T) {
		reg := &WorkspaceRegistry{}
		ws := reg.Get("nope")
		assert.Nil(t, ws)
	})
}

func TestWorkspaceRegistryUpdateLastUsed(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", originalHome)

	reg := &WorkspaceRegistry{
		Workspaces: []Workspace{{Name: "ws1", Path: "/tmp/ws1"}},
	}
	require.NoError(t, SaveWorkspaceRegistry(reg))

	err := reg.UpdateLastUsed("ws1")
	assert.NoError(t, err)
	assert.Equal(t, "ws1", reg.LastUsed)

	// Verify persisted.
	loaded, err := LoadWorkspaceRegistry()
	assert.NoError(t, err)
	assert.Equal(t, "ws1", loaded.LastUsed)
}

func TestSetOpenWorkspaces(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", originalHome)

	reg := &WorkspaceRegistry{
		Workspaces: []Workspace{
			{Name: "alpha", Path: "/a"},
			{Name: "beta", Path: "/b"},
		},
	}
	require.NoError(t, SaveWorkspaceRegistry(reg))

	t.Run("persists ordered list and drops unknown names", func(t *testing.T) {
		require.NoError(t, reg.SetOpenWorkspaces([]string{"beta", "ghost", "alpha"}))
		assert.Equal(t, []string{"beta", "alpha"}, reg.OpenWorkspaces)

		loaded, err := LoadWorkspaceRegistry()
		require.NoError(t, err)
		assert.Equal(t, []string{"beta", "alpha"}, loaded.OpenWorkspaces)
	})

	t.Run("empty list clears the field", func(t *testing.T) {
		require.NoError(t, reg.SetOpenWorkspaces(nil))
		assert.Empty(t, reg.OpenWorkspaces)
	})
}

func TestGetOpenWorkspaces(t *testing.T) {
	reg := &WorkspaceRegistry{
		Workspaces: []Workspace{
			{Name: "alpha", Path: "/a"},
			{Name: "beta", Path: "/b"},
		},
		OpenWorkspaces: []string{"beta", "ghost", "alpha"},
	}

	open := reg.GetOpenWorkspaces()
	require.Len(t, open, 2)
	assert.Equal(t, "beta", open[0].Name)
	assert.Equal(t, "alpha", open[1].Name)
}

func TestRemovePropagatesToOpenWorkspaces(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", originalHome)

	repo1 := t.TempDir()
	repo2 := t.TempDir()
	reg := &WorkspaceRegistry{}
	require.NoError(t, reg.Add("alpha", repo1))
	require.NoError(t, reg.Add("beta", repo2))
	reg.OpenWorkspaces = []string{"alpha", "beta"}
	require.NoError(t, SaveWorkspaceRegistry(reg))

	require.NoError(t, reg.Remove("alpha"))
	assert.Equal(t, []string{"beta"}, reg.OpenWorkspaces)

	loaded, err := LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, []string{"beta"}, loaded.OpenWorkspaces)
}

func TestRenamePropagatesToOpenWorkspaces(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", originalHome)

	repo1 := t.TempDir()
	reg := &WorkspaceRegistry{}
	require.NoError(t, reg.Add("alpha", repo1))
	reg.OpenWorkspaces = []string{"alpha"}
	require.NoError(t, SaveWorkspaceRegistry(reg))

	require.NoError(t, reg.Rename("alpha", "alpha-renamed"))
	assert.Equal(t, []string{"alpha-renamed"}, reg.OpenWorkspaces)
}

func TestWorkspaceConfigDir(t *testing.T) {
	ws := &Workspace{Name: "test", Path: "/home/user/myrepo"}
	assert.Equal(t, "/home/user/myrepo/.claude-squad", WorkspaceConfigDir(ws))
}

func TestEnsureGitignore(t *testing.T) {
	t.Run("creates .gitignore if missing", func(t *testing.T) {
		repoDir := t.TempDir()
		err := EnsureGitignore(repoDir)
		assert.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(repoDir, ".gitignore"))
		require.NoError(t, err)
		assert.Contains(t, string(data), ".claude-squad/")
		assert.Contains(t, string(data), "# claude-squad local data")
	})

	t.Run("appends to existing .gitignore", func(t *testing.T) {
		repoDir := t.TempDir()
		existing := "node_modules/\n"
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte(existing), 0644))

		err := EnsureGitignore(repoDir)
		assert.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(repoDir, ".gitignore"))
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, "node_modules/")
		assert.Contains(t, content, ".claude-squad/")
	})

	t.Run("idempotent - does not duplicate entry", func(t *testing.T) {
		repoDir := t.TempDir()
		require.NoError(t, EnsureGitignore(repoDir))
		require.NoError(t, EnsureGitignore(repoDir))

		data, err := os.ReadFile(filepath.Join(repoDir, ".gitignore"))
		require.NoError(t, err)

		content := string(data)
		// Count occurrences — should appear exactly once.
		count := 0
		for _, line := range splitLines(content) {
			if line == ".claude-squad/" {
				count++
			}
		}
		assert.Equal(t, 1, count)
	})

	t.Run("handles file without trailing newline", func(t *testing.T) {
		repoDir := t.TempDir()
		existing := "node_modules/" // no trailing newline
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte(existing), 0644))

		err := EnsureGitignore(repoDir)
		assert.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(repoDir, ".gitignore"))
		require.NoError(t, err)
		content := string(data)
		// The comment should start on its own line.
		assert.Contains(t, content, "\n# claude-squad local data")
	})
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
