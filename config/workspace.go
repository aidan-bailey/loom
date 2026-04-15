package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Workspace represents a registered workspace tied to a git repository.
type Workspace struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	AddedAt time.Time `json:"added_at"`
}

// WorkspaceRegistry holds all registered workspaces and tracks the last used one.
type WorkspaceRegistry struct {
	Workspaces []Workspace `json:"workspaces"`
	LastUsed   string      `json:"last_used"`
}

const workspacesFileName = "workspaces.json"

// WorkspaceContext carries the resolved identity of the active workspace.
// A nil *WorkspaceContext means "global" (no workspace).
type WorkspaceContext struct {
	// Name is the workspace name. Empty string means global.
	Name string
	// ConfigDir is the absolute path to the config directory
	// (e.g., /repo/.claude-squad or ~/.claude-squad).
	ConfigDir string
	// RepoPath is the absolute path to the repo root. Empty for global.
	RepoPath string
}

// GlobalWorkspaceContext returns a WorkspaceContext pointing at ~/.claude-squad.
func GlobalWorkspaceContext() (*WorkspaceContext, error) {
	dir, err := GetGlobalConfigDir()
	if err != nil {
		return nil, err
	}
	return &WorkspaceContext{ConfigDir: dir}, nil
}

// WorkspaceContextFor returns a WorkspaceContext for the given workspace.
func WorkspaceContextFor(ws *Workspace) *WorkspaceContext {
	return &WorkspaceContext{
		Name:      ws.Name,
		ConfigDir: WorkspaceConfigDir(ws),
		RepoPath:  ws.Path,
	}
}

// ResolveWorkspace determines the workspace context for the given working directory.
// If a registered workspace matches, returns its context. Otherwise returns the global context.
func ResolveWorkspace(cwd string, registry *WorkspaceRegistry) (*WorkspaceContext, error) {
	if registry != nil {
		if ws := registry.FindByPath(cwd); ws != nil {
			return WorkspaceContextFor(ws), nil
		}
	}
	return GlobalWorkspaceContext()
}

// GetGlobalConfigDir returns ~/.claude-squad/ regardless of CLAUDE_SQUAD_HOME.
func GetGlobalConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".claude-squad"), nil
}

// LoadWorkspaceRegistry reads the workspace registry from the global config dir.
// Returns an empty registry if the file doesn't exist.
func LoadWorkspaceRegistry() (*WorkspaceRegistry, error) {
	globalDir, err := GetGlobalConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get global config dir: %w", err)
	}

	regPath := filepath.Join(globalDir, workspacesFileName)
	data, err := os.ReadFile(regPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &WorkspaceRegistry{}, nil
		}
		return nil, fmt.Errorf("failed to read workspace registry: %w", err)
	}

	var reg WorkspaceRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("failed to parse workspace registry: %w", err)
	}
	return &reg, nil
}

// SaveWorkspaceRegistry writes the workspace registry to the global config dir.
func SaveWorkspaceRegistry(reg *WorkspaceRegistry) error {
	globalDir, err := GetGlobalConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get global config dir: %w", err)
	}

	if err := os.MkdirAll(globalDir, 0755); err != nil {
		return fmt.Errorf("failed to create global config dir: %w", err)
	}

	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workspace registry: %w", err)
	}

	regPath := filepath.Join(globalDir, workspacesFileName)
	return AtomicWriteFile(regPath, data, 0644)
}

// Add registers a new workspace. Validates uniqueness of name and that the path
// is a git repository. Automatically calls EnsureGitignore.
func (r *WorkspaceRegistry) Add(name, repoPath string) error {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Validate unique name.
	for _, ws := range r.Workspaces {
		if ws.Name == name {
			return fmt.Errorf("workspace with name %q already exists", name)
		}
	}

	// Validate unique path.
	for _, ws := range r.Workspaces {
		if ws.Path == absPath {
			return fmt.Errorf("workspace for path %q already exists (name: %s)", absPath, ws.Name)
		}
	}

	r.Workspaces = append(r.Workspaces, Workspace{
		Name:    name,
		Path:    absPath,
		AddedAt: time.Now(),
	})

	if err := EnsureGitignore(absPath); err != nil {
		return fmt.Errorf("failed to update .gitignore: %w", err)
	}

	return SaveWorkspaceRegistry(r)
}

// Remove removes a workspace by name. Does NOT delete the .claude-squad/ directory.
func (r *WorkspaceRegistry) Remove(name string) error {
	idx := -1
	for i, ws := range r.Workspaces {
		if ws.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("workspace %q not found", name)
	}

	r.Workspaces = append(r.Workspaces[:idx], r.Workspaces[idx+1:]...)

	if r.LastUsed == name {
		r.LastUsed = ""
	}

	return SaveWorkspaceRegistry(r)
}

// FindByPath finds a workspace whose Path matches or is a parent of the given path.
func (r *WorkspaceRegistry) FindByPath(path string) *Workspace {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil
	}

	for i, ws := range r.Workspaces {
		if absPath == ws.Path || strings.HasPrefix(absPath, ws.Path+string(filepath.Separator)) {
			return &r.Workspaces[i]
		}
	}
	return nil
}

// Get finds a workspace by name.
func (r *WorkspaceRegistry) Get(name string) *Workspace {
	for i, ws := range r.Workspaces {
		if ws.Name == name {
			return &r.Workspaces[i]
		}
	}
	return nil
}

// Rename changes a workspace's name. Updates LastUsed if it pointed to the old name.
func (r *WorkspaceRegistry) Rename(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	// Check new name doesn't conflict.
	for _, ws := range r.Workspaces {
		if ws.Name == newName {
			return fmt.Errorf("workspace with name %q already exists", newName)
		}
	}
	found := false
	for i, ws := range r.Workspaces {
		if ws.Name == oldName {
			r.Workspaces[i].Name = newName
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("workspace %q not found", oldName)
	}
	if r.LastUsed == oldName {
		r.LastUsed = newName
	}
	return SaveWorkspaceRegistry(r)
}

// UpdateLastUsed sets the last used workspace and saves the registry.
// Returns an error if no workspace with the given name exists.
func (r *WorkspaceRegistry) UpdateLastUsed(name string) error {
	if r.Get(name) == nil {
		return fmt.Errorf("workspace %q not found", name)
	}
	r.LastUsed = name
	return SaveWorkspaceRegistry(r)
}

// WorkspaceConfigDir returns the .claude-squad directory inside the workspace repo.
func WorkspaceConfigDir(ws *Workspace) string {
	return filepath.Join(ws.Path, ".claude-squad")
}

// EnsureGitignore ensures .claude-squad/ is listed in the repo's .gitignore.
func EnsureGitignore(repoPath string) error {
	gitignorePath := filepath.Join(repoPath, ".gitignore")

	const entry = ".claude-squad/"
	const comment = "# claude-squad local data"

	data, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read .gitignore: %w", err)
	}

	// Check if already present.
	if err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == entry || trimmed == entry+"/" {
				return nil // already present
			}
		}
	}

	// Append the entry.
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open .gitignore: %w", err)
	}
	defer f.Close()

	// Add a newline before comment if file has content and doesn't end with newline.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	_, err = f.WriteString(comment + "\n" + entry + "\n")
	return err
}
