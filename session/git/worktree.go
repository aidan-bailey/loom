package git

import (
	"claude-squad/config"
	"claude-squad/log"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

// CommandRunner abstracts subprocess execution so tests can mock git
// invocations. Satisfied by cmd.Executor via structural typing — defining it
// here avoids an import cycle (cmd/workspace.go already imports session/git).
type CommandRunner interface {
	Run(c *exec.Cmd) error
	Output(c *exec.Cmd) ([]byte, error)
	CombinedOutput(c *exec.Cmd) ([]byte, error)
}

// realRunner is the default subprocess runner. Mirrors cmd.Exec but kept local
// so callers that don't already depend on claude-squad/cmd (like main.go's
// cleanup path) don't have to import it just to satisfy CommandRunner.
type realRunner struct{}

func (realRunner) Run(c *exec.Cmd) error                      { return c.Run() }
func (realRunner) Output(c *exec.Cmd) ([]byte, error)         { return c.Output() }
func (realRunner) CombinedOutput(c *exec.Cmd) ([]byte, error) { return c.CombinedOutput() }

// DefaultRunner returns the production subprocess runner.
func DefaultRunner() CommandRunner { return realRunner{} }

func getWorktreeDirectory(configDir string) (string, error) {
	if configDir == "" {
		var err error
		configDir, err = config.GetConfigDir()
		if err != nil {
			return "", err
		}
	}

	return filepath.Join(configDir, "worktrees"), nil
}

// GitWorktree manages git worktree operations for a session
type GitWorktree struct {
	// Path to the repository
	repoPath string
	// Path to the worktree
	worktreePath string
	// Name of the session
	sessionName string
	// Branch name for the worktree
	branchName string
	// Base commit hash for the worktree
	baseCommitSHA string
	// isExistingBranch is true if the branch existed before the session was created.
	// When true, the branch will not be deleted on cleanup.
	isExistingBranch bool
	// configDir is the resolved config directory for workspace-scoped worktrees.
	configDir string
	// runner executes git/gh subprocesses; injected so tests can mock them.
	runner CommandRunner
}

func defaultRunner(r CommandRunner) CommandRunner {
	if r == nil {
		return realRunner{}
	}
	return r
}

func NewGitWorktreeFromStorage(repoPath string, worktreePath string, sessionName string, branchName string, baseCommitSHA string, isExistingBranch bool, configDir string) *GitWorktree {
	return NewGitWorktreeFromStorageWithRunner(repoPath, worktreePath, sessionName, branchName, baseCommitSHA, isExistingBranch, configDir, nil)
}

func NewGitWorktreeFromStorageWithRunner(repoPath string, worktreePath string, sessionName string, branchName string, baseCommitSHA string, isExistingBranch bool, configDir string, runner CommandRunner) *GitWorktree {
	return &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     worktreePath,
		sessionName:      sessionName,
		branchName:       branchName,
		baseCommitSHA:    baseCommitSHA,
		isExistingBranch: isExistingBranch,
		configDir:        configDir,
		runner:           defaultRunner(runner),
	}
}

// resolveWorktreePaths resolves the repo root and generates a unique worktree path for the given branch name.
func resolveWorktreePaths(repoPath string, branchName string, configDir string, runner CommandRunner) (resolvedRepo string, worktreePath string, err error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		log.ErrorLog.Printf("git worktree path abs error, falling back to repoPath %s: %s", repoPath, err)
		absPath = repoPath
	}

	resolvedRepo, err = findGitRepoRoot(absPath, runner)
	if err != nil {
		return "", "", err
	}

	worktreeDir, err := getWorktreeDirectory(configDir)
	if err != nil {
		return "", "", err
	}

	worktreePath = filepath.Join(worktreeDir, sanitizeBranchName(branchName))
	worktreePath = worktreePath + "_" + fmt.Sprintf("%x", time.Now().UnixNano())

	return resolvedRepo, worktreePath, nil
}

// NewGitWorktree creates a new GitWorktree instance.
// configDir is the workspace config directory; if empty, falls back to GetConfigDir().
func NewGitWorktree(repoPath string, sessionName string, configDir string) (tree *GitWorktree, branchname string, err error) {
	return NewGitWorktreeWithRunner(repoPath, sessionName, configDir, nil)
}

// NewGitWorktreeWithRunner is NewGitWorktree with an injected CommandRunner
// (used by tests). Passing nil falls back to the default runner.
func NewGitWorktreeWithRunner(repoPath string, sessionName string, configDir string, runner CommandRunner) (tree *GitWorktree, branchname string, err error) {
	var cfg *config.Config
	if configDir != "" {
		cfg = config.LoadConfigFrom(configDir)
	} else {
		cfg = config.LoadConfig()
	}
	branchName := fmt.Sprintf("%s%s", cfg.BranchPrefix, sessionName)
	// Sanitize the final branch name to handle invalid characters from any source
	// (e.g., backslashes from Windows domain usernames like DOMAIN\user)
	branchName = sanitizeBranchName(branchName)

	r := defaultRunner(runner)
	repoPath, worktreePath, err := resolveWorktreePaths(repoPath, branchName, configDir, r)
	if err != nil {
		return nil, "", err
	}

	return &GitWorktree{
		repoPath:     repoPath,
		sessionName:  sessionName,
		branchName:   branchName,
		worktreePath: worktreePath,
		configDir:    configDir,
		runner:       r,
	}, branchName, nil
}

// NewGitWorktreeFromBranch creates a new GitWorktree that uses an existing branch.
// The branch will not be deleted on cleanup.
// configDir is the workspace config directory; if empty, falls back to GetConfigDir().
func NewGitWorktreeFromBranch(repoPath string, branchName string, sessionName string, configDir string) (*GitWorktree, error) {
	return NewGitWorktreeFromBranchWithRunner(repoPath, branchName, sessionName, configDir, nil)
}

func NewGitWorktreeFromBranchWithRunner(repoPath string, branchName string, sessionName string, configDir string, runner CommandRunner) (*GitWorktree, error) {
	r := defaultRunner(runner)
	repoPath, worktreePath, err := resolveWorktreePaths(repoPath, branchName, configDir, r)
	if err != nil {
		return nil, err
	}

	return &GitWorktree{
		repoPath:         repoPath,
		sessionName:      sessionName,
		branchName:       branchName,
		worktreePath:     worktreePath,
		isExistingBranch: true,
		configDir:        configDir,
		runner:           r,
	}, nil
}

// IsExistingBranch returns whether this worktree uses a pre-existing branch
func (g *GitWorktree) IsExistingBranch() bool {
	return g.isExistingBranch
}

// GetWorktreePath returns the path to the worktree
func (g *GitWorktree) GetWorktreePath() string {
	return g.worktreePath
}

// GetBranchName returns the name of the branch associated with this worktree
func (g *GitWorktree) GetBranchName() string {
	return g.branchName
}

// GetRepoPath returns the path to the repository
func (g *GitWorktree) GetRepoPath() string {
	return g.repoPath
}

// GetRepoName returns the name of the repository (last part of the repoPath).
func (g *GitWorktree) GetRepoName() string {
	return filepath.Base(g.repoPath)
}

// GetBaseCommitSHA returns the base commit SHA for the worktree
func (g *GitWorktree) GetBaseCommitSHA() string {
	return g.baseCommitSHA
}
