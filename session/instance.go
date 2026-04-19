package session

import (
	"errors"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/agent"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"
	"path/filepath"

	"fmt"
	"log/slog"
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

// String implements fmt.Stringer for debugging and transition-error messages.
func (s Status) String() string {
	switch s {
	case Running:
		return "Running"
	case Ready:
		return "Ready"
	case Loading:
		return "Loading"
	case Paused:
		return "Paused"
	case Prompting:
		return "Prompting"
	case Deleting:
		return "Deleting"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// allowedTransitions encodes the Status state machine. A Paused instance
// has no live tmux session, so jumping to Prompting/Ready without first
// going through Loading/Running would produce an inconsistent UI state —
// that's the main invariant this table enforces.
var allowedTransitions = map[Status]map[Status]bool{
	Ready:     {Loading: true, Running: true, Prompting: true, Paused: true, Deleting: true},
	Loading:   {Ready: true, Running: true, Prompting: true, Paused: true, Deleting: true},
	Running:   {Ready: true, Loading: true, Prompting: true, Paused: true, Deleting: true},
	Prompting: {Ready: true, Loading: true, Running: true, Paused: true, Deleting: true},
	Paused:    {Loading: true, Running: true, Deleting: true},
	Deleting:  {Ready: true, Loading: true, Running: true, Prompting: true, Paused: true},
}

// IsAllowedTransition reports whether from → to is permitted by the
// Status state machine. Self-transitions are always allowed.
func IsAllowedTransition(from, to Status) bool {
	if from == to {
		return true
	}
	targets, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

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
	// CrashRecovered is a runtime-only flag set when this instance was restored
	// after a crash. Used to trigger agent-aware restart (e.g. --continue).
	CrashRecovered bool

	// DiffStats stores the current git diff statistics
	diffStats *git.DiffStats

	// selectedBranch is the existing branch to start on (empty = new branch from HEAD)
	selectedBranch string

	// The below fields are initialized upon calling Start().

	started bool
	// starting is true while a Start() call is in progress. Combined with
	// started, it makes the Start() idempotency guard atomic: without it,
	// two concurrent callers could both observe started=false, both unlock,
	// and both proceed to allocate a tmux session and worktree.
	starting bool
	// tmuxSession is the tmux session for the instance.
	tmuxSession *tmux.TmuxSession
	// gitWorktree is the git worktree for the instance.
	gitWorktree *git.GitWorktree

	// mu guards concurrent access to fields that can be read from
	// tick-fanout goroutines (Status, diffStats, Branch) and from
	// lifecycle Cmd goroutines (tmuxSession, gitWorktree, started).
	// Held for writes; RLock for reads. Do not hold across I/O.
	//
	// Every accessor on Instance goes through TransitionTo/GetStatus,
	// GetBranch, GetDiffStats, Snapshot, or the unexported get*/set*
	// helpers below. Do not read or write these fields directly from
	// outside the locked accessors.
	mu sync.RWMutex

	// logger is a per-instance slog.Logger pre-tagged with
	// subsystem=instance and title. Populated by NewInstance and
	// FromInstanceData; tests that build Instance directly are covered
	// by the getLogger() fallback.
	logger *slog.Logger
}

// Snapshot returns a serialization-safe copy of every Instance field
// under a single RLock. This is the only safe way to read every field
// at once from outside the main goroutine. Callers that previously
// invoked ToInstanceData from background Cmd goroutines (e.g.
// storage.DeleteInstance) must use this to avoid racing with the main
// loop's writes (INST-11, STORE-16).
//
// GitWorktree's Get* accessors are pure field reads, so it is safe to
// call them while holding i.mu — no I/O happens under the lock.
func (i *Instance) Snapshot() InstanceData {
	i.mu.RLock()
	defer i.mu.RUnlock()

	data := InstanceData{
		SchemaVersion:       CurrentSchemaVersion,
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

	if i.gitWorktree != nil {
		data.Worktree = GitWorktreeData{
			RepoPath:         i.gitWorktree.GetRepoPath(),
			WorktreePath:     i.gitWorktree.GetWorktreePath(),
			SessionName:      i.Title,
			BranchName:       i.gitWorktree.GetBranchName(),
			BaseCommitSHA:    i.gitWorktree.GetBaseCommitSHA(),
			IsExistingBranch: i.gitWorktree.IsExistingBranch(),
		}
	}

	if i.diffStats != nil {
		data.DiffStats = DiffStatsData{
			Added:   i.diffStats.Added,
			Removed: i.diffStats.Removed,
			Content: i.diffStats.Content,
		}
	}

	return data
}

// ToInstanceData converts an Instance to its serializable form. Kept
// as a thin wrapper around Snapshot for backwards compatibility with
// existing callers; new code should prefer Snapshot.
func (i *Instance) ToInstanceData() InstanceData {
	return i.Snapshot()
}

// FromInstanceData creates a new Instance from serialized data without
// spawning a tmux PTY attachment. Paused instances are constructed fully
// (started=true, TmuxSession object present, no PTY — matches their on-disk
// shape). Non-paused instances are returned with started=false; the caller
// must invoke EnsureRunning to attach the PTY. configDir is injected for
// workspace-scoped worktree resolution.
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
		logger:              log.For("instance", "title", data.Title),
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
	}

	return instance, nil
}

// EnsureRunning attaches a PTY to the instance's tmux session, restoring
// any previously-persisted session state. A no-op for paused instances
// (they deliberately have no PTY) and for already-started instances.
// Idempotent: the underlying Start guards against double-attaches.
func (i *Instance) EnsureRunning() error {
	if i.Paused() {
		return nil
	}
	if i.isStarted() {
		return nil
	}
	return i.Start(false)
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
		logger:              log.For("instance", "title", opts.Title),
	}, nil
}

func (i *Instance) RepoName() (string, error) {
	return i.backend().RepoName()
}

// TransitionTo validates from→to against the state-machine allow-list and
// updates Status atomically. Disallowed transitions return an error and
// leave Status unchanged. Self-transitions are no-ops (success).
func (i *Instance) TransitionTo(to Status) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	from := i.Status
	if from == to {
		return nil
	}
	if !IsAllowedTransition(from, to) {
		return fmt.Errorf("illegal status transition for %q: %s → %s", i.Title, from, to)
	}
	i.Status = to
	return nil
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
	if v {
		i.starting = false
	}
}

// getLogger returns a per-instance slog.Logger tagged with
// subsystem=instance and title=<title>. Falls back to log.For for
// instances constructed directly without going through
// NewInstance/FromInstanceData (primarily tests). Safe to call before
// log.Initialize — log.For returns a no-op logger in that case.
func (i *Instance) getLogger() *slog.Logger {
	if i.logger != nil {
		return i.logger
	}
	return log.For("instance", "title", i.Title)
}

// reserveStart atomically reserves the right to run Start() setup. Returns
// true if the caller acquired the reservation and must proceed; false if the
// instance is already started or a concurrent Start is in progress. Pairs
// with releaseStart (on failure) or setStarted(true) (on success).
func (i *Instance) reserveStart() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.started || i.starting {
		return false
	}
	i.starting = true
	return true
}

// releaseStart clears the starting flag after a failed Start attempt so a
// subsequent caller can retry. Not needed on success — setStarted(true)
// clears starting for us.
func (i *Instance) releaseStart() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.starting = false
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
func (i *Instance) Start(firstTimeSetup bool) (err error) {
	lg := i.getLogger()
	t0 := time.Now()
	lg.Debug("instance.start.begin", "first_time", firstTimeSetup)
	defer func() {
		args := []any{"duration_ms", time.Since(t0).Milliseconds(), "first_time", firstTimeSetup}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		lg.Debug("instance.start.end", args...)
	}()

	if i.Title == "" {
		return fmt.Errorf("instance title cannot be empty")
	}

	// Idempotency guard: a second Start on an already-started instance is
	// a no-op so we don't orphan the existing tmux session (INST-04).
	// reserveStart atomically rejects both "already started" and "another
	// Start is in flight", closing the TOCTOU hole the prior check had.
	if !i.reserveStart() {
		return nil
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
			// Clear the starting reservation first so a retry isn't blocked
			// by this failed attempt. Kill() below releases any resources
			// that did get allocated.
			i.releaseStart()
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

	_ = i.TransitionTo(Running)

	return nil
}

// Kill terminates the instance and cleans up all resources
func (i *Instance) Kill() (err error) {
	lg := i.getLogger()
	t0 := time.Now()
	lg.Debug("instance.kill.begin")
	defer func() {
		args := []any{"duration_ms", time.Since(t0).Milliseconds()}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		lg.Debug("instance.kill.end", args...)
	}()

	// Snapshot handles under lock and clear the started flag up-front so a
	// concurrent or repeated Kill bails out before touching the same
	// resources twice. Resource cleanup (tmux Close / worktree Cleanup) is
	// then performed without holding the lock.
	i.mu.Lock()
	if !i.started {
		i.mu.Unlock()
		return nil
	}
	tmuxSess := i.tmuxSession
	gitWT := i.gitWorktree
	isWorkspaceTerm := i.IsWorkspaceTerminal
	i.started = false
	i.tmuxSession = nil
	i.gitWorktree = nil
	i.mu.Unlock()

	var errs []error

	// Always try to cleanup both resources, even if one fails
	// Clean up tmux session first since it's using the git worktree
	if tmuxSess != nil {
		if err := tmuxSess.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close tmux session: %w", err))
		}
	}

	// Then clean up git worktree (workspace terminals don't have one)
	if gitWT != nil && !isWorkspaceTerm {
		if err := gitWT.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup git worktree: %w", err))
		}
	}

	return i.combineErrors(errs)
}

// combineErrors combines multiple errors into a single error. Uses
// errors.Join so callers can still use errors.Is/errors.As against
// any underlying cause — stringifying would have broken that chain.
func (i *Instance) combineErrors(errs []error) error {
	return errors.Join(errs...)
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
	// The adapter registry tells us whether this program has a trust
	// prompt to dismiss; the default fallback returns TrustPromptNone,
	// which short-circuits here so unknown programs get no handling.
	if defaultRegistry.Lookup(i.Program).TrustPromptResponse() == agent.TrustPromptNone {
		return false
	}
	return ts.CheckAndHandleTrustPrompt()
}

// CaptureAndProcessStatus captures tmux pane content once and checks for
// trust prompts and content updates. Avoids duplicate CapturePaneContent calls.
// Returns a non-nil err when the underlying capture failed — previously
// the error was swallowed inside tmux.CaptureAndProcess and surfaced only
// in the log file, so callers saw "no updates" instead of a genuine
// capture failure.
func (i *Instance) CaptureAndProcessStatus() (updated bool, hasPrompt bool, err error) {
	if !i.isStarted() {
		return false, false, nil
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return false, false, nil
	}

	// Unknown programs (no adapter match beyond fallback) don't have
	// trust/prompt patterns — skip the combined scan and just check for
	// pane updates. Supported agents flow through CaptureAndProcess,
	// which handles both trust dismissal and prompt detection in one
	// CapturePaneContent call.
	ad := defaultRegistry.Lookup(i.Program)
	if ad.Name() == "default" {
		updated, hasPrompt = ts.HasUpdated()
		return updated, hasPrompt, nil
	}

	_, updated, hasPrompt, _, err = ts.CaptureAndProcess()
	return updated, hasPrompt, err
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
		log.For("session").Error("tap_enter_failed", "err", err)
	}
}

// TmuxSession returns the backing tmux session, or nil if the instance has
// not been started yet. Exposed so the app layer can drive a full-screen
// attach via tea.ExecProcess.
func (i *Instance) TmuxSession() *tmux.TmuxSession {
	if !i.isStarted() {
		return nil
	}
	return i.getTmuxSession()
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
	return i.backend().WorkTreePath()
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

// Pause stops the tmux session and removes the worktree, preserving the branch.
// If saveState is non-nil, it is called after committing changes and marking the
// instance as Paused, providing a checkpoint that reduces the crash inconsistency window.
func (i *Instance) Pause(saveState func() error) (err error) {
	lg := i.getLogger()
	t0 := time.Now()
	lg.Debug("instance.pause.begin")
	defer func() {
		args := []any{"duration_ms", time.Since(t0).Milliseconds()}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		lg.Debug("instance.pause.end", args...)
	}()

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
		commitMsg := fmt.Sprintf("[loom] update from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
		if err := gw.CommitChanges(commitMsg); err != nil {
			errs = append(errs, fmt.Errorf("failed to commit changes: %w", err))
			// Return early if we can't commit changes to avoid corrupted state
			return i.combineErrors(errs)
		}
	}

	// Kill the tmux session so the agent process actually stops. Otherwise
	// claude/aider would keep running inside a session whose worktree we are
	// about to delete. Resume rebuilds the session with BuildRecoveryCommand
	// so --continue (or equivalent) restores the conversation for agents
	// that support it.
	if err := ts.Close(); err != nil {
		log.For("session").Warn("pause_close_tmux_failed", "err", err)
		// Continue with pause process; the tmux session may already be dead.
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
			lg.Warn("instance.prune.failed", "err", err.Error())
		}
	}

	if err := i.combineErrors(errs); err != nil {
		return err
	}

	// Checkpoint: mark as Paused immediately after cleanup succeeds.
	// If we crash after this point, the instance is safely Paused.
	_ = i.TransitionTo(Paused)
	_ = clipboard.WriteAll(gw.GetBranchName())
	if saveState != nil {
		if err := saveState(); err != nil {
			return fmt.Errorf("pause checkpoint save: %w", err)
		}
	}
	return nil
}

// Resume recreates the worktree and restarts the tmux session.
// If saveState is non-nil, it is called after the instance is Running,
// providing a checkpoint that reduces the crash inconsistency window.
func (i *Instance) Resume(saveState func() error) (err error) {
	lg := i.getLogger()
	t0 := time.Now()
	lg.Debug("instance.resume.begin")
	defer func() {
		args := []any{"duration_ms", time.Since(t0).Milliseconds()}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		lg.Debug("instance.resume.end", args...)
	}()

	if i.IsWorkspaceTerminal {
		return fmt.Errorf("cannot resume workspace terminal")
	}
	if !i.isStarted() {
		return fmt.Errorf("cannot resume instance that has not been started")
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
		if errors.Is(err, git.ErrBranchGone) {
			return fmt.Errorf("branch %q was deleted externally — kill this instance (D) to clean up: %w", gw.GetBranchName(), err)
		}
		return fmt.Errorf("failed to setup git worktree: %w", err)
	}

	// Check if tmux session still exists from pause, otherwise create new one
	if ts.DoesSessionExist() {
		// Session exists, just restore PTY connection to it
		if err := ts.Restore(); err != nil {
			// Kill the broken session before creating a new one,
			// because Start() rejects sessions that already exist.
			if closeErr := ts.Close(); closeErr != nil {
				log.For("session").Error("broken_session_close_failed", "err", closeErr)
			}
			if err := i.startFreshWithRecovery(gw); err != nil {
				return err
			}
		}
	} else {
		if err := i.startFreshWithRecovery(gw); err != nil {
			return err
		}
	}

	_ = i.TransitionTo(Running)
	if saveState != nil {
		if err := saveState(); err != nil {
			return fmt.Errorf("resume checkpoint save: %w", err)
		}
	}
	return nil
}

// startFreshWithRecovery creates a brand-new tmux session for an instance
// whose previous session no longer exists (normal after crash or kill-server).
// The program is rewritten via BuildRecoveryCommand so supported agents resume
// their prior conversation (e.g. `claude --continue`).
func (i *Instance) startFreshWithRecovery(gw *git.GitWorktree) error {
	program := BuildRecoveryCommand(i.Program)
	ts := tmux.NewTmuxSession(i.Title, program)
	if err := ts.Start(gw.GetWorktreePath()); err != nil {
		if cleanupErr := gw.Cleanup(); cleanupErr != nil {
			err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
		}
		return fmt.Errorf("failed to start new session: %w", err)
	}
	i.setTmuxSession(ts)
	return nil
}

// CrashRestart starts a new tmux session for a crash-recovered instance.
// The worktree already exists (for regular instances) or is unnecessary
// (for workspace terminals). The program is modified with --continue for
// supported agents.
func (i *Instance) CrashRestart() error {
	program := BuildRecoveryCommand(i.Program)
	ts := tmux.NewTmuxSession(i.Title, program)

	var workDir string
	if i.IsWorkspaceTerminal {
		workDir = i.Path
	} else {
		gw := i.getGitWorktree()
		if gw == nil {
			return fmt.Errorf("no git worktree for crash restart of %q", i.Title)
		}
		workDir = gw.GetWorktreePath()
	}

	if err := ts.Start(workDir); err != nil {
		return fmt.Errorf("crash restart failed for %q: %w", i.Title, err)
	}

	// Only replace the tmux session after Start succeeds. Otherwise a failed
	// CrashRestart would leave i.tmuxSession pointing at a session whose
	// program string carries --continue, and a later Resume would start that
	// modified program on a fresh conversation.
	i.setTmuxSession(ts)
	_ = i.TransitionTo(Running)
	return nil
}

// UpdateDiffStats updates the git diff statistics for this instance
func (i *Instance) UpdateDiffStats() error {
	return i.updateDiffStats(SessionBackend.Diff)
}

// UpdateDiffStatsShort updates only the line counts (Added/Removed) without
// fetching full diff content. Cheaper for non-selected instances that only
// display counts in the list view.
func (i *Instance) UpdateDiffStatsShort() error {
	return i.updateDiffStats(SessionBackend.DiffShort)
}

// ShouldRefreshDiff reports whether a metadata tick needs to re-run the
// diff backend for this instance. It returns true when:
//   - the instance has no cached diff stats yet (first-time fetch),
//   - the tmux pane content changed since the last tick (agent wrote files),
//   - the caller needs full diff content but only short-stats are cached
//     (selection-change upgrade path).
//
// When false, the tick can skip the git subprocess entirely. The paused
// branch is also short-circuited so the daemon's AutoYes path stays cheap.
func (i *Instance) ShouldRefreshDiff(tmuxUpdated, wantFull bool) bool {
	if !i.isStarted() || i.GetStatus() == Paused {
		return false
	}
	if tmuxUpdated {
		return true
	}
	stats := i.GetDiffStats()
	if stats == nil {
		return true
	}
	// Upgrade from short to full when selection changed to an instance
	// whose cached stats were recorded without content.
	if wantFull && !stats.IsEmpty() && stats.Content == "" {
		return true
	}
	return false
}

// updateDiffStats is the shared body for UpdateDiffStats and
// UpdateDiffStatsShort. The diffFn parameter selects which backend
// method runs; everything else (paused guard, branch refresh,
// base-commit error handling) is identical.
func (i *Instance) updateDiffStats(diffFn func(SessionBackend) *git.DiffStats) error {
	if !i.isStarted() {
		i.setDiffStats(nil)
		return nil
	}
	if i.GetStatus() == Paused {
		return nil
	}

	b := i.backend()
	if branch := b.RefreshBranch(); branch != "" {
		i.mu.Lock()
		i.Branch = branch
		i.mu.Unlock()
	}

	stats := diffFn(b)
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
