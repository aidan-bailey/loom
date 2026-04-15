package session

import (
	"claude-squad/log"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"path/filepath"

	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
)

type Status int

const (
	// Running is the status when the instance is running and claude is working.
	Running Status = iota
	// Ready is if the claude instance is ready to be interacted with (waiting for user input).
	Ready
	// Loading is if the instance is loading (if we are starting it up or something).
	Loading
	// Paused is if the instance is paused (worktree removed but branch preserved).
	Paused
	// Prompting is when the agent is asking for user permission.
	Prompting
	// Deleting is a transient status set immediately when the user confirms
	// deletion. Cleanup runs asynchronously; on failure the status reverts.
	Deleting
)

// Instance is a running instance of claude code.
type Instance struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Branch is the branch of the instance.
	Branch string
	// Status is the status of the instance.
	Status Status
	// Program is the program to run in the instance.
	Program string
	// Height is the height of the instance.
	Height int
	// Width is the width of the instance.
	Width int
	// CreatedAt is the time the instance was created.
	CreatedAt time.Time
	// UpdatedAt is the time the instance was last updated.
	UpdatedAt time.Time
	// AutoYes is true if the instance should automatically press enter when prompted.
	AutoYes bool
	// Prompt is the initial prompt to pass to the instance on startup
	Prompt string
	// ConfigDir is the workspace config directory for worktree resolution.
	ConfigDir string
	// IsWorkspaceTerminal is true if this instance operates in the root repo without a worktree.
	IsWorkspaceTerminal bool

	// DiffStats stores the current git diff statistics
	diffStats *git.DiffStats

	// selectedBranch is the existing branch to start on (empty = new branch from HEAD)
	selectedBranch string

	// The below fields are initialized upon calling Start().

	started bool
	// tmuxSession is the tmux session for the instance.
	tmuxSession *tmux.TmuxSession
	// gitWorktree is the git worktree for the instance.
	gitWorktree *git.GitWorktree

	// mu guards concurrent access to fields that can be read from
	// tick-fanout goroutines (Status, diffStats, Branch) and from
	// lifecycle Cmd goroutines (tmuxSession, gitWorktree, started).
	// Held for writes; RLock for reads. Do not hold across I/O.
	//
	// Every accessor on Instance goes through SetStatus/GetStatus
	// or the unexported get*/set* helpers below. Do not read or
	// write these fields directly from outside the locked accessors.
	// ToInstanceData still reads Status/Branch/Path-level fields
	// unlocked; Task 2.7 replaces it with Snapshot() that takes a
	// single RLock across the whole copy.
	mu sync.RWMutex
}

// ToInstanceData converts an Instance to its serializable form
func (i *Instance) ToInstanceData() InstanceData {
	data := InstanceData{
		Title:               i.Title,
		Path:                i.Path,
		Branch:              i.Branch,
		Status:              i.Status,
		Height:              i.Height,
		Width:               i.Width,
		CreatedAt:           i.CreatedAt,
		UpdatedAt:           time.Now(),
		Program:             i.Program,
		AutoYes:             i.AutoYes,
		IsWorkspaceTerminal: i.IsWorkspaceTerminal,
	}

	// Only include worktree data if gitWorktree is initialized
	if gw := i.getGitWorktree(); gw != nil {
		data.Worktree = GitWorktreeData{
			RepoPath:         gw.GetRepoPath(),
			WorktreePath:     gw.GetWorktreePath(),
			SessionName:      i.Title,
			BranchName:       gw.GetBranchName(),
			BaseCommitSHA:    gw.GetBaseCommitSHA(),
			IsExistingBranch: gw.IsExistingBranch(),
		}
	}

	// Only include diff stats if they exist
	if ds := i.GetDiffStats(); ds != nil {
		data.DiffStats = DiffStatsData{
			Added:   ds.Added,
			Removed: ds.Removed,
			Content: ds.Content,
		}
	}

	return data
}

// FromInstanceData creates a new Instance from serialized data.
// configDir is injected into the instance for workspace-scoped worktree resolution.
func FromInstanceData(data InstanceData, configDir string) (*Instance, error) {
	instance := &Instance{
		Title:               data.Title,
		Path:                data.Path,
		Branch:              data.Branch,
		Status:              data.Status,
		Height:              data.Height,
		Width:               data.Width,
		CreatedAt:           data.CreatedAt,
		UpdatedAt:           data.UpdatedAt,
		Program:             data.Program,
		AutoYes:             data.AutoYes,
		ConfigDir:           configDir,
		IsWorkspaceTerminal: data.IsWorkspaceTerminal,
	}

	// Workspace terminals don't use git worktrees
	if !data.IsWorkspaceTerminal {
		instance.setGitWorktree(git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
			data.Worktree.IsExistingBranch,
			configDir,
		))
	}

	// Only restore DiffStats if any field is non-zero, preserving nil for
	// instances that were serialized without diff stats.
	if data.DiffStats.Added != 0 || data.DiffStats.Removed != 0 || data.DiffStats.Content != "" {
		instance.setDiffStats(&git.DiffStats{
			Added:   data.DiffStats.Added,
			Removed: data.DiffStats.Removed,
			Content: data.DiffStats.Content,
		})
	}

	if instance.Paused() {
		instance.setStarted(true)
		instance.setTmuxSession(tmux.NewTmuxSession(instance.Title, instance.Program))
	} else {
		if err := instance.Start(false); err != nil {
			return nil, err
		}
	}

	return instance, nil
}

// Options for creating a new instance
type InstanceOptions struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Program is the program to run in the instance (e.g. "claude", "aider --model ollama_chat/gemma3:1b")
	Program string
	// If AutoYes is true, then
	AutoYes bool
	// Branch is an existing branch name to start the session on (empty = new branch from HEAD)
	Branch string
	// ConfigDir is the workspace config directory for worktree resolution.
	ConfigDir string
	// IsWorkspaceTerminal creates a workspace terminal instance (no worktree).
	IsWorkspaceTerminal bool
}

func NewInstance(opts InstanceOptions) (*Instance, error) {
	t := time.Now()

	// Convert path to absolute
	absPath, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	return &Instance{
		Title:               opts.Title,
		Status:              Ready,
		Path:                absPath,
		Program:             opts.Program,
		Height:              0,
		Width:               0,
		CreatedAt:           t,
		UpdatedAt:           t,
		AutoYes:             false,
		selectedBranch:      opts.Branch,
		ConfigDir:           opts.ConfigDir,
		IsWorkspaceTerminal: opts.IsWorkspaceTerminal,
	}, nil
}

func (i *Instance) RepoName() (string, error) {
	if i.IsWorkspaceTerminal {
		return filepath.Base(i.Path), nil
	}
	if !i.isStarted() {
		return "", fmt.Errorf("cannot get repo name for instance that has not been started")
	}
	return i.getGitWorktree().GetRepoName(), nil
}

func (i *Instance) SetStatus(status Status) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Status = status
}

// GetStatus returns the current status under a read lock.
func (i *Instance) GetStatus() Status {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.Status
}

func (i *Instance) getTmuxSession() *tmux.TmuxSession {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.tmuxSession
}

func (i *Instance) setTmuxSession(s *tmux.TmuxSession) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.tmuxSession = s
}

func (i *Instance) getGitWorktree() *git.GitWorktree {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.gitWorktree
}

func (i *Instance) setGitWorktree(w *git.GitWorktree) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.gitWorktree = w
}

func (i *Instance) isStarted() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.started
}

func (i *Instance) setStarted(v bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.started = v
}

// GetBranch returns the branch name under a read lock. Safe to call
// concurrently with UpdateDiffStats* for workspace terminals, which
// refresh Branch on every tick.
func (i *Instance) GetBranch() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.Branch
}

// SetSelectedBranch sets the branch to use when starting the instance.
func (i *Instance) SetSelectedBranch(branch string) {
	i.selectedBranch = branch
}

// firstTimeSetup is true if this is a new instance. Otherwise, it's one loaded from storage.
func (i *Instance) Start(firstTimeSetup bool) error {
	if i.Title == "" {
		return fmt.Errorf("instance title cannot be empty")
	}

	ts := i.getTmuxSession()
	if ts == nil {
		// Create new tmux session
		ts = tmux.NewTmuxSession(i.Title, i.Program)
	}
	i.setTmuxSession(ts)

	// Workspace terminals skip worktree creation entirely
	var gw *git.GitWorktree
	if firstTimeSetup && !i.IsWorkspaceTerminal {
		if i.selectedBranch != "" {
			gitWorktree, err := git.NewGitWorktreeFromBranch(i.Path, i.selectedBranch, i.Title, i.ConfigDir)
			if err != nil {
				return fmt.Errorf("failed to create git worktree from branch: %w", err)
			}
			i.mu.Lock()
			i.gitWorktree = gitWorktree
			i.Branch = i.selectedBranch
			i.mu.Unlock()
			gw = gitWorktree
		} else {
			gitWorktree, branchName, err := git.NewGitWorktree(i.Path, i.Title, i.ConfigDir)
			if err != nil {
				return fmt.Errorf("failed to create git worktree: %w", err)
			}
			i.mu.Lock()
			i.gitWorktree = gitWorktree
			i.Branch = branchName
			i.mu.Unlock()
			gw = gitWorktree
		}
	} else {
		gw = i.getGitWorktree()
	}

	// Setup error handler to cleanup resources on any error
	var setupErr error
	defer func() {
		if setupErr != nil {
			if cleanupErr := i.Kill(); cleanupErr != nil {
				setupErr = fmt.Errorf("%v (cleanup error: %v)", setupErr, cleanupErr)
			}
		} else {
			i.setStarted(true)
		}
	}()

	if !firstTimeSetup {
		// Reuse existing session
		if err := ts.Restore(); err != nil {
			setupErr = fmt.Errorf("failed to restore existing session: %w", err)
			return setupErr
		}
	} else if i.IsWorkspaceTerminal {
		// Workspace terminal: start tmux directly in root repo, no worktree
		if err := ts.Start(i.Path); err != nil {
			setupErr = fmt.Errorf("failed to start workspace terminal session: %w", err)
			return setupErr
		}
	} else {
		// Setup git worktree first
		if err := gw.Setup(); err != nil {
			setupErr = fmt.Errorf("failed to setup git worktree: %w", err)
			return setupErr
		}

		// Create new session
		if err := ts.Start(gw.GetWorktreePath()); err != nil {
			// Cleanup git worktree if tmux session creation fails
			if cleanupErr := gw.Cleanup(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			setupErr = fmt.Errorf("failed to start new session: %w", err)
			return setupErr
		}
	}

	i.SetStatus(Running)

	return nil
}

// Kill terminates the instance and cleans up all resources
func (i *Instance) Kill() error {
	if !i.isStarted() {
		// If instance was never started, just return success
		return nil
	}

	var errs []error

	// Always try to cleanup both resources, even if one fails
	// Clean up tmux session first since it's using the git worktree
	if ts := i.getTmuxSession(); ts != nil {
		if err := ts.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close tmux session: %w", err))
		}
	}

	// Then clean up git worktree (workspace terminals don't have one)
	if gw := i.getGitWorktree(); gw != nil && !i.IsWorkspaceTerminal {
		if err := gw.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup git worktree: %w", err))
		}
	}

	return i.combineErrors(errs)
}

// combineErrors combines multiple errors into a single error
func (i *Instance) combineErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	errMsg := "multiple cleanup errors occurred:"
	for _, err := range errs {
		errMsg += "\n  - " + err.Error()
	}
	return fmt.Errorf("%s", errMsg)
}

func (i *Instance) Preview() (string, error) {
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", nil
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return "", nil
	}
	return ts.CapturePaneContent()
}

func (i *Instance) HasUpdated() (updated bool, hasPrompt bool) {
	if !i.isStarted() {
		return false, false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return false, false
	}
	return ts.HasUpdated()
}

// TapEnter sends an enter key press to the tmux session if AutoYes is enabled.
// CheckAndHandleTrustPrompt checks for and dismisses the trust prompt for supported programs.
func (i *Instance) CheckAndHandleTrustPrompt() bool {
	if !i.isStarted() {
		return false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return false
	}
	program := i.Program
	if !strings.HasSuffix(program, tmux.ProgramClaude) &&
		!strings.HasSuffix(program, tmux.ProgramAider) &&
		!strings.HasSuffix(program, tmux.ProgramGemini) {
		return false
	}
	return ts.CheckAndHandleTrustPrompt()
}

// CaptureAndProcessStatus captures tmux pane content once and checks for
// trust prompts and content updates. Avoids duplicate CapturePaneContent calls.
func (i *Instance) CaptureAndProcessStatus() (updated bool, hasPrompt bool) {
	if !i.isStarted() {
		return false, false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return false, false
	}

	program := i.Program
	isSupportedProgram := strings.HasSuffix(program, tmux.ProgramClaude) ||
		strings.HasSuffix(program, tmux.ProgramAider) ||
		strings.HasSuffix(program, tmux.ProgramGemini)

	if !isSupportedProgram {
		// For unsupported programs, just check for updates.
		return ts.HasUpdated()
	}

	_, updated, hasPrompt, _ = ts.CaptureAndProcess()
	return updated, hasPrompt
}

func (i *Instance) TapEnter() {
	if !i.isStarted() || i.GetStatus() == Paused || !i.AutoYes {
		return
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return
	}
	if err := ts.TapEnter(); err != nil {
		log.ErrorLog.Printf("error tapping enter: %v", err)
	}
}

func (i *Instance) Attach() (chan struct{}, error) {
	if !i.isStarted() {
		return nil, fmt.Errorf("cannot attach instance that has not been started")
	}
	return i.getTmuxSession().Attach()
}

func (i *Instance) SetPreviewSize(width, height int) error {
	if !i.isStarted() || i.GetStatus() == Paused {
		return fmt.Errorf("cannot set preview size for instance that has not been started or " +
			"is paused")
	}
	return i.getTmuxSession().SetDetachedSize(width, height)
}

// GetGitWorktree returns the git worktree for the instance
func (i *Instance) GetGitWorktree() (*git.GitWorktree, error) {
	if !i.isStarted() {
		return nil, fmt.Errorf("cannot get git worktree for instance that has not been started")
	}
	return i.getGitWorktree(), nil
}

// GetWorktreePath returns the worktree path for the instance, or empty string if unavailable.
// For workspace terminals, returns the root repo path.
func (i *Instance) GetWorktreePath() string {
	if i.IsWorkspaceTerminal {
		return i.Path
	}
	gw := i.getGitWorktree()
	if gw == nil {
		return ""
	}
	return gw.GetWorktreePath()
}

func (i *Instance) Started() bool {
	return i.isStarted()
}

// SetTitle sets the title of the instance. Returns an error if the instance has started.
// We cant change the title once it's been used for a tmux session etc.
func (i *Instance) SetTitle(title string) error {
	if i.isStarted() {
		return fmt.Errorf("cannot change title of a started instance")
	}
	i.Title = title
	return nil
}

func (i *Instance) Paused() bool {
	return i.GetStatus() == Paused
}

// TmuxAlive returns true if the tmux session is alive. This is a sanity check before attaching.
func (i *Instance) TmuxAlive() bool {
	ts := i.getTmuxSession()
	if ts == nil {
		return false
	}
	return ts.DoesSessionExist()
}

// Pause stops the tmux session and removes the worktree, preserving the branch
func (i *Instance) Pause() error {
	if i.IsWorkspaceTerminal {
		return fmt.Errorf("cannot pause workspace terminal")
	}
	if !i.isStarted() {
		return fmt.Errorf("cannot pause instance that has not been started")
	}
	if i.GetStatus() == Paused {
		return fmt.Errorf("instance is already paused")
	}

	gw := i.getGitWorktree()
	ts := i.getTmuxSession()
	var errs []error

	// Check if there are any changes to commit
	if dirty, err := gw.IsDirty(); err != nil {
		errs = append(errs, fmt.Errorf("failed to check if worktree is dirty: %w", err))
	} else if dirty {
		// Commit changes locally (without pushing to GitHub)
		commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
		if err := gw.CommitChanges(commitMsg); err != nil {
			errs = append(errs, fmt.Errorf("failed to commit changes: %w", err))
			// Return early if we can't commit changes to avoid corrupted state
			return i.combineErrors(errs)
		}
	}

	// Detach from tmux session instead of closing to preserve session output
	if err := ts.DetachSafely(); err != nil {
		errs = append(errs, fmt.Errorf("failed to detach tmux session: %w", err))
		// Continue with pause process even if detach fails
	}

	// Check if worktree exists before trying to remove it
	if _, err := os.Stat(gw.GetWorktreePath()); err == nil {
		// Remove worktree but keep branch
		if err := gw.Remove(); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove git worktree: %w", err))
			return i.combineErrors(errs)
		}

		// Prune stale worktree references. This is non-critical — the worktree
		// is already removed, so don't abort the pause if prune fails.
		if err := gw.Prune(); err != nil {
			log.ErrorLog.Printf("failed to prune git worktrees (non-critical): %v", err)
		}
	}

	if err := i.combineErrors(errs); err != nil {
		return err
	}

	i.SetStatus(Paused)
	_ = clipboard.WriteAll(gw.GetBranchName())
	return nil
}

// Resume recreates the worktree and restarts the tmux session
func (i *Instance) Resume() error {
	if i.IsWorkspaceTerminal {
		return fmt.Errorf("cannot resume workspace terminal")
	}
	if !i.isStarted() {
		return fmt.Errorf("cannot resume instance that has not been started")
	}
	if i.GetStatus() != Paused {
		return fmt.Errorf("can only resume paused instances")
	}

	gw := i.getGitWorktree()
	ts := i.getTmuxSession()

	// Check if branch is checked out
	if checked, err := gw.IsBranchCheckedOut(); err != nil {
		return fmt.Errorf("failed to check if branch is checked out: %w", err)
	} else if checked {
		return fmt.Errorf("cannot resume: branch is checked out, please switch to a different branch")
	}

	// Setup git worktree
	if err := gw.Setup(); err != nil {
		return fmt.Errorf("failed to setup git worktree: %w", err)
	}

	// Check if tmux session still exists from pause, otherwise create new one
	if ts.DoesSessionExist() {
		// Session exists, just restore PTY connection to it
		if err := ts.Restore(); err != nil {
			// Kill the broken session before creating a new one,
			// because Start() rejects sessions that already exist.
			if closeErr := ts.Close(); closeErr != nil {
				log.ErrorLog.Printf("failed to close broken session: %v", closeErr)
			}
			// Fall back to creating new session
			if err := ts.Start(gw.GetWorktreePath()); err != nil {
				// Cleanup git worktree if tmux session creation fails
				if cleanupErr := gw.Cleanup(); cleanupErr != nil {
					err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
				}
				return fmt.Errorf("failed to start new session: %w", err)
			}
		}
	} else {
		// Create new tmux session
		if err := ts.Start(gw.GetWorktreePath()); err != nil {
			// Cleanup git worktree if tmux session creation fails
			if cleanupErr := gw.Cleanup(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			return fmt.Errorf("failed to start new session: %w", err)
		}
	}

	i.SetStatus(Running)
	return nil
}

// UpdateDiffStats updates the git diff statistics for this instance
func (i *Instance) UpdateDiffStats() error {
	if !i.isStarted() {
		i.setDiffStats(nil)
		return nil
	}

	if i.GetStatus() == Paused {
		// Keep the previous diff stats if the instance is paused
		return nil
	}

	var stats *git.DiffStats
	if i.IsWorkspaceTerminal {
		// Refresh the current branch name for the root repo
		if branch, err := git.CurrentBranch(i.Path); err == nil {
			i.mu.Lock()
			i.Branch = branch
			i.mu.Unlock()
		}
		stats = git.DiffUncommitted(i.Path)
	} else {
		stats = i.getGitWorktree().Diff()
	}
	if stats.Error != nil {
		if strings.Contains(stats.Error.Error(), "base commit SHA not set") {
			// Worktree is not fully set up yet, not an error
			i.setDiffStats(nil)
			return nil
		}
		return fmt.Errorf("failed to get diff stats: %w", stats.Error)
	}

	i.setDiffStats(stats)
	return nil
}

// UpdateDiffStatsShort updates only the line counts (Added/Removed) without
// fetching full diff content. Cheaper for non-selected instances that only
// display counts in the list view.
func (i *Instance) UpdateDiffStatsShort() error {
	if !i.isStarted() {
		i.setDiffStats(nil)
		return nil
	}
	if i.GetStatus() == Paused {
		return nil
	}
	var stats *git.DiffStats
	if i.IsWorkspaceTerminal {
		if branch, err := git.CurrentBranch(i.Path); err == nil {
			i.mu.Lock()
			i.Branch = branch
			i.mu.Unlock()
		}
		stats = git.DiffUncommittedShortStat(i.Path)
	} else {
		stats = i.getGitWorktree().DiffShortStat()
	}
	if stats.Error != nil {
		if strings.Contains(stats.Error.Error(), "base commit SHA not set") {
			i.setDiffStats(nil)
			return nil
		}
		return fmt.Errorf("failed to get diff stats: %w", stats.Error)
	}
	i.setDiffStats(stats)
	return nil
}

// GetDiffStats returns the current git diff statistics
func (i *Instance) GetDiffStats() *git.DiffStats {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.diffStats
}

// setDiffStats assigns diffStats under the instance mutex. Unexported so
// that external callers must go through UpdateDiffStats*.
func (i *Instance) setDiffStats(s *git.DiffStats) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.diffStats = s
}

// SendPrompt sends a prompt to the tmux session
func (i *Instance) SendPrompt(prompt string) error {
	if !i.isStarted() {
		return fmt.Errorf("instance not started")
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return fmt.Errorf("tmux session not initialized")
	}
	if err := ts.SendKeys(prompt); err != nil {
		return fmt.Errorf("error sending keys to tmux session: %w", err)
	}

	// Brief pause to prevent carriage return from being interpreted as newline
	time.Sleep(100 * time.Millisecond)
	if err := ts.TapEnter(); err != nil {
		return fmt.Errorf("error tapping enter: %w", err)
	}

	return nil
}

// PreviewFullHistory captures the entire tmux pane output including full scrollback history
func (i *Instance) PreviewFullHistory() (string, error) {
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", nil
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return "", nil
	}
	return ts.CapturePaneContentWithOptions("-", "-")
}

// GetContentHash returns the content hash of the last captured tmux pane.
func (i *Instance) GetContentHash() []byte {
	if !i.isStarted() {
		return nil
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return nil
	}
	return ts.GetContentHash()
}

// SetTmuxSession sets the tmux session for testing purposes
func (i *Instance) SetTmuxSession(session *tmux.TmuxSession) {
	i.setTmuxSession(session)
}

// SendKeys sends keys to the tmux session
func (i *Instance) SendKeys(keys string) error {
	if !i.isStarted() || i.GetStatus() == Paused {
		return fmt.Errorf("cannot send keys to instance that has not been started or is paused")
	}
	return i.getTmuxSession().SendKeys(keys)
}

// SendKeysRaw writes raw bytes to the tmux PTY. Used by inline attach mode.
func (i *Instance) SendKeysRaw(b []byte) error {
	if !i.isStarted() {
		return fmt.Errorf("instance not started or tmux session not initialized")
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return fmt.Errorf("instance not started or tmux session not initialized")
	}
	return ts.SendKeysRaw(b)
}
