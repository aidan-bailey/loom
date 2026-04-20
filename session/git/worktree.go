package git

import (
	"fmt"
	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"path/filepath"
	"sync"
	"time"
)

// CommandRunner is an alias for internal/exec.Executor. Kept as a type alias
// so git-package callers keep the familiar name while the concrete interface
// lives in a leaf package shared with cmd/.
type CommandRunner = internalexec.Executor

// DefaultRunner returns the production subprocess runner.
func DefaultRunner() CommandRunner { return internalexec.Default{} }

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

	// untrackedCacheMu guards the fields below. The cache short-circuits
	// `git ls-files --others --exclude-standard` on back-to-back Diff
	// calls — that subprocess runs on every metadata tick and is pure
	// overhead once we know there are no new untracked files.
	untrackedCacheMu sync.Mutex
	// untrackedCheckedAt is zero when no check has been recorded.
	untrackedCheckedAt time.Time
	// untrackedHadAny is the result of the most recent ls-files probe.
	untrackedHadAny bool
}

// untrackedCacheTTL caps how long an untracked-file check is trusted.
// Short enough that newly-created files appear in diff stats within a few
// ticks; long enough to skip the ls-files subprocess on the majority of
// metadata ticks. Tests rewind the cache timestamp directly rather than
// tightening this value.
var untrackedCacheTTL = 2 * time.Second

func defaultRunner(r CommandRunner) CommandRunner {
	if r == nil {
		return internalexec.Default{}
	}
	return r
}

// NewGitWorktreeFromStorage rehydrates a GitWorktree from its persisted
// [session.GitWorktreeData] fields. Unlike [NewGitWorktree], this
// constructor does no git work — it simply reattaches the in-memory
// wrapper to an on-disk worktree that already exists (or that needs to
// be rebuilt by a caller via Setup on resume).
func NewGitWorktreeFromStorage(repoPath string, worktreePath string, sessionName string, branchName string, baseCommitSHA string, isExistingBranch bool, configDir string) *GitWorktree {
	return NewGitWorktreeFromStorageWithRunner(repoPath, worktreePath, sessionName, branchName, baseCommitSHA, isExistingBranch, configDir, nil)
}

// NewGitWorktreeFromStorageWithRunner is [NewGitWorktreeFromStorage]
// with an injected [CommandRunner] for tests. Passing nil falls back to
// the default subprocess runner.
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
		log.For("git").Error("worktree_path_abs_failed", "fallback_repo_path", repoPath, "err", err)
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
// (used by tests). Passing nil falls back to the default runner. configDir
// must be a concrete workspace config directory (callers thread this
// through via config.WorkspaceContext); passing empty string falls back to
// the global directory and logs a warning.
func NewGitWorktreeWithRunner(repoPath string, sessionName string, configDir string, runner CommandRunner) (tree *GitWorktree, branchname string, err error) {
	if configDir == "" {
		log.For("git").Warn("new_worktree_missing_config_dir", "action", "falling_back_to_global")
		resolved, resolveErr := config.GetConfigDir()
		if resolveErr != nil {
			return nil, "", fmt.Errorf("resolve config dir: %w", resolveErr)
		}
		configDir = resolved
	}
	cfg := config.LoadConfigFrom(configDir)
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

// NewGitWorktreeFromBranchWithRunner is [NewGitWorktreeFromBranch]
// with an injected [CommandRunner] for tests. Passing nil falls back
// to the default subprocess runner.
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
