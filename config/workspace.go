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

// WorkspaceRegistry holds all registered workspaces, tracks the last-focused
// one, and records the ordered list of open workspace tabs for restore.
type WorkspaceRegistry struct {
	Workspaces     []Workspace `json:"workspaces"`
	LastUsed       string      `json:"last_used"`
	OpenWorkspaces []string    `json:"open_workspaces,omitempty"`
}

const workspacesFileName = "workspaces.json"

// WorkspaceContext carries the resolved identity of the active workspace.
// A nil *WorkspaceContext means "global" (no workspace).
type WorkspaceContext struct {
	// Name is the workspace name. Empty string means global.
	Name string
	// ConfigDir is the absolute path to the config directory
	// (e.g., /repo/.loom or ~/.loom).
	ConfigDir string
	// RepoPath is the absolute path to the repo root. Empty for global.
	RepoPath string
}

// GlobalWorkspaceContext returns a WorkspaceContext pointing at ~/.loom.
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

// GetGlobalConfigDir returns ~/.loom/ regardless of LOOM_HOME.
func GetGlobalConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".loom"), nil
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

// Remove removes a workspace by name. Does NOT delete the .loom/ directory.
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

	r.OpenWorkspaces = removeString(r.OpenWorkspaces, name)

	return SaveWorkspaceRegistry(r)
}

func removeString(s []string, target string) []string {
	out := s[:0]
	for _, v := range s {
		if v != target {
			out = append(out, v)
		}
	}
	return out
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
	for i, n := range r.OpenWorkspaces {
		if n == oldName {
			r.OpenWorkspaces[i] = newName
		}
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

// WorkspaceConfigDir returns the .loom directory inside the workspace repo.
func WorkspaceConfigDir(ws *Workspace) string {
	return filepath.Join(ws.Path, ".loom")
}

// SetOpenWorkspaces replaces the ordered list of open workspace names and
// persists the registry. Unknown names are silently dropped so the stored list
// always references workspaces that still exist.
func (r *WorkspaceRegistry) SetOpenWorkspaces(names []string) error {
	filtered := make([]string, 0, len(names))
	for _, n := range names {
		if r.Get(n) != nil {
			filtered = append(filtered, n)
		}
	}
	r.OpenWorkspaces = filtered
	return SaveWorkspaceRegistry(r)
}

// GetOpenWorkspaces returns the workspaces named in OpenWorkspaces, in order.
// Names that no longer exist in the registry are skipped silently.
func (r *WorkspaceRegistry) GetOpenWorkspaces() []Workspace {
	out := make([]Workspace, 0, len(r.OpenWorkspaces))
	for _, n := range r.OpenWorkspaces {
		if ws := r.Get(n); ws != nil {
			out = append(out, *ws)
		}
	}
	return out
}

// EnsureGitignore ensures .loom/ is listed in the repo's .gitignore.
// Writes atomically via AtomicWriteFile so a crash or a concurrent call can't
// leave a half-written entry behind. An O_APPEND-based write would issue two
// syscalls (newline fixup, then entry), and racing callers each saw an absent
// entry and each appended their own copy.
func EnsureGitignore(repoPath string) error {
	gitignorePath := filepath.Join(repoPath, ".gitignore")

	const entry = ".loom/"
	const comment = "# loom local data"

	data, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read .gitignore: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == entry || trimmed == entry+"/" {
			return nil
		}
	}

	newContent := make([]byte, 0, len(data)+len(comment)+len(entry)+3)
	newContent = append(newContent, data...)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		newContent = append(newContent, '\n')
	}
	newContent = append(newContent, comment...)
	newContent = append(newContent, '\n')
	newContent = append(newContent, entry...)
	newContent = append(newContent, '\n')

	if err := AtomicWriteFile(gitignorePath, newContent, 0644); err != nil {
		return fmt.Errorf("failed to write .gitignore: %w", err)
	}
	return nil
}
