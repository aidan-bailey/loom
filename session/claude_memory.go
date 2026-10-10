package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aidan-bailey/loom/session/claudetmp"
)

// accountMemoryDir returns the auto-memory directory to hand Claude
// (autoMemoryDirectory) for a session on the account whose config dir is
// claudeConfigDir, in the repository repoPath or one of its worktrees. It
// returns "" when Claude's own dir works, and "" with why when Claude's
// own dir will prompt but loom can't name the right one.
//
// Claude keeps memory at <CLAUDE_CONFIG_DIR>/projects/<key>/memory/, its
// config dir taken as given, and lets writes inside it through without
// asking. It also checks every write under the symlink-resolved path, and
// an account's projects/ is a link into the main config dir
// (account.Sync), so that spelling falls outside the exemption and every
// memory write prompts (Claude Code 2.1.292). The resolved dir has one
// spelling.
//
// The key is the main repository's root, which Claude reads from a
// worktree's .git file; git writes it symlink-resolved, so loom encodes
// the physical repo path (claudetmp.DirName: the same encoding). Anything
// loom can't match exactly gets "": a settings.json that sets, or might
// set, its own dir (loom's flag settings would outrank it), a repository
// without a .git directory (a linked worktree or a submodule, whose root
// Claude finds otherwise), and a key Claude would cut short and hash.
func accountMemoryDir(claudeConfigDir, repoPath string) (dir, why string) {
	if claudeConfigDir == "" {
		return "", ""
	}
	projects := filepath.Join(claudeConfigDir, "projects")
	if _, err := os.Lstat(projects); errors.Is(err, fs.ErrNotExist) {
		return "", "" // Claude creates a real one
	}
	resolved, err := filepath.EvalSymlinks(projects)
	if err != nil {
		return "", fmt.Sprintf("resolve %s: %v", projects, err)
	}
	if resolved == projects {
		return "", ""
	}
	if why := userMemoryDirSetting(claudeConfigDir); why != "" {
		return "", why
	}
	physRepo, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		return "", fmt.Sprintf("resolve repository: %v", err)
	}
	if fi, err := os.Lstat(filepath.Join(physRepo, ".git")); err != nil || !fi.IsDir() {
		return "", "the repository has no .git directory"
	}
	key, truncated := claudetmp.DirName(physRepo)
	if truncated {
		return "", "the repository path is too long for Claude's key"
	}
	return filepath.Join(resolved, key, "memory"), ""
}

// userMemoryDirSetting returns why loom must leave the memory dir to the
// user's settings.json in claudeConfigDir: it sets autoMemoryDirectory, or
// it can't be read to tell. "" when it is absent or sets none.
func userMemoryDirSetting(claudeConfigDir string) string {
	data, err := os.ReadFile(filepath.Join(claudeConfigDir, "settings.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("read settings.json: %v", err)
	}
	var s struct {
		AutoMemoryDirectory *json.RawMessage `json:"autoMemoryDirectory"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Sprintf("parse settings.json: %v", err)
	}
	if s.AutoMemoryDirectory != nil {
		return "settings.json sets autoMemoryDirectory"
	}
	return ""
}
