package session

import (
	"errors"
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/aidan-bailey/loom/session/tmux"
	"path/filepath"

	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atotto/clipboard"
)

// Status is the Instance lifecycle state. Transitions are gated by
// allowedTransitions in instance.go; use Instance.TransitionTo for
// every write so invariants hold across the app and tests.
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
	// Recoverable is an orphaned worktree found on disk and surfaced inline
	// for the user to recover or discard. It has a worktree but no live tmux
	// and is never persisted to state.json (re-derived from disk each load).
	// Appended last so existing serialized Status ints are unchanged.
	Recoverable
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
	case Recoverable:
		return "Recoverable"
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
	Loading:   {Ready: true, Running: true, Prompting: true, Paused: true, Deleting: true, Recoverable: true},
	Running:   {Ready: true, Loading: true, Prompting: true, Paused: true, Deleting: true},
	Prompting: {Ready: true, Loading: true, Running: true, Paused: true, Deleting: true},
	Paused:    {Loading: true, Running: true, Deleting: true},
	// Deleting → X and Loading/Deleting → Recoverable are the failed-op
	// revert paths: a discard whose cleanup failed goes back to
	// Recoverable, a recover whose adoption failed likewise.
	Deleting:    {Ready: true, Loading: true, Running: true, Prompting: true, Paused: true, Recoverable: true},
	Recoverable: {Loading: true, Running: true, Deleting: true},
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

// Instance is a single agent session managed by Loom. Each Instance owns
// a git worktree (or, for workspace terminals, targets the root repo) and a
// tmux session where the agent command runs. Instances move through the
// Status state machine — Ready → Loading → Running → Paused → Deleting —
// governed by allowedTransitions; use TransitionTo for every status write.
//
// Field access rules: mu guards Status, diffStats, Branch, tmuxSession,
// gitWorktree, the started/starting flags, and the launch fields (program,
// headroomProxy, cacheTTL1h, prompt, crashRecovered). External callers must
// go through the exported accessors (GetStatus, GetBranch, GetDiffStats,
// Snapshot, TmuxSession, Program/SetProgram, SetLaunchOptions, Prompt/
// SetPrompt, …) or the unexported get*/set* helpers — never read or write
// those fields directly. Holding mu across I/O is forbidden, and so is
// calling a locking accessor while holding it: sync.RWMutex is not
// reentrant, so session code that already holds mu reads the private
// fields instead.
//
// Lifecycle entry points: NewInstance creates a blank instance (call
// Start(true) to materialize worktree + tmux). FromInstanceData rehydrates
// persisted state and does not spawn a PTY — callers must invoke
// EnsureRunning to bring a non-paused instance back online.
type Instance struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Branch is the branch of the instance.
	Branch string
	// Status is the status of the instance.
	Status Status
	// program is the program to run in the instance. Read it with
	// Program(); set it with SetProgram or SetLaunchOptions.
	program string
	// headroomProxy controls whether this instance's tmux session gets
	// ANTHROPIC_BASE_URL pointed at Headroom's proxy (see
	// session.HeadroomProxyEnv). A no-op unless program resolves to
	// Claude. Set once before a launch (via SetLaunchOptions, alongside
	// program) and persisted so pause/resume and crash recovery — which
	// construct a brand new TmuxSession for the same instance — still
	// apply it.
	headroomProxy bool
	// cacheTTL1h controls whether this instance's tmux session gets
	// ENABLE_PROMPT_CACHING_1H=1, extending Claude's prompt cache from
	// the default 5-minute TTL to 1 hour (see session.CacheTTL1hEnv). A
	// no-op unless program resolves to Claude. Same set-once/persisted
	// convention as headroomProxy.
	cacheTTL1h bool
	// account is the Claude account this instance launches under: a
	// registered account's name, or "" for the default account. Every real
	// launch resolves it to a CLAUDE_CONFIG_DIR (see launchEnv). Set with
	// SetAccount alongside SetLaunchOptions; persisted (InstanceData v8).
	account string
	// Height is the height of the instance.
	Height int
	// Width is the width of the instance.
	Width int
	// CreatedAt is the time the instance was created.
	CreatedAt time.Time
	// UpdatedAt is the time the instance was last updated.
	UpdatedAt time.Time
	// prompt is the initial prompt the start-completion handler sends to
	// the agent once Start succeeds (then clears). Never serialized.
	prompt string
	// ConfigDir is the workspace config directory for worktree resolution.
	ConfigDir string
	// IsWorkspaceTerminal is true if this instance operates in the root repo without a worktree.
	IsWorkspaceTerminal bool
	// crashRecovered is a runtime-only flag set when this instance was restored
	// after a crash. Used to trigger agent-aware restart (e.g. --continue).
	crashRecovered bool

	// DiffStats stores the current git diff statistics
	diffStats *git.DiffStats

	// selectedBranch is the existing branch to start on (empty = new branch from HEAD)
	selectedBranch string
	// branchPrefix overrides config.BranchPrefix when composing this
	// session's branch name. nil means "use the configured prefix"; a
	// non-nil empty string means no prefix at all. Deliberately not
	// persisted: it is consumed once, during first-time setup, and the
	// branch name it produced is stored in Branch — so a resume has no use
	// for it and InstanceData needs no schema bump.
	branchPrefix *string

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
	// restartFailureCount counts consecutive metadata ticks that observed
	// this workspace terminal's tmux dead right after a Restart — i.e.
	// Restart "succeeded" (no error) but the session died again before the
	// next tick, as happens when its persisted Program is permanently
	// broken. The app's tick loop uses this to trip a circuit breaker
	// instead of restart-looping forever at tick cadence; see
	// RecordRestartFailure/ResetRestartFailures.
	restartFailureCount int

	// bellPending marks that the pane rang BEL while this instance was not
	// selected — surfaced as an attention badge in the session list and
	// cleared on selection. Ephemeral: never serialized (absent from
	// InstanceData). atomic because the list renderer and Update goroutine
	// both touch it.
	bellPending atomic.Bool

	// mu guards concurrent access to fields that can be read from
	// tick-fanout goroutines (Status, statusChangedAt, diffStats,
	// Branch) and from lifecycle Cmd goroutines (tmuxSession,
	// gitWorktree, started, and the launch fields program/headroomProxy/
	// cacheTTL1h/account, which Start/Resume/CrashRestart read via launchEnv).
	// Held for writes; RLock for reads. Do not hold across I/O.
	//
	// Every accessor on Instance goes through TransitionTo/GetStatus,
	// StatusAge, GetBranch, GetDiffStats, Snapshot, or the unexported
	// get*/set* helpers below. Do not read or write these fields
	// directly from outside the locked accessors.
	mu sync.RWMutex

	// statusChangedAt is when Status last changed (in-memory only, not
	// serialized — ages reset on restart, which is acceptable for the
	// "waiting 4m" card labels it feeds).
	statusChangedAt time.Time

	// waitReason is Claude's own account of what this session is blocked
	// on ("permission: Bash" from a PermissionRequest hook, "sandbox
	// request" from the roster's waitingFor). Only ever set while Claude's
	// report drives a Prompting status (adoptClaudeStatus), and cleared
	// the moment it does not, so a dismissed dialog cannot leave a label
	// behind. Empty for non-Claude agents and whenever the scraper is
	// deciding. Ephemeral: never serialized (absent from InstanceData).
	waitReason string

	// issue is the linked GitHub issue number (0 = none). Persisted as
	// InstanceData.Issue.
	issue int
	// githubState is the poller's join for this session (see
	// app/github.go). Transient: never serialized; Known=false until the
	// first successful poll after startup.
	githubState github.State
	// ahead/behind count commits relative to the base branch
	// (git.AheadBehind); hasParity is false until the first successful
	// count and after any failure. Transient.
	ahead, behind int
	hasParity     bool

	// subagents, hookLaunchID and subagentWarm track the agents this
	// session has spawned, from loom's hook events (see
	// subagent_hooks.go). hookLaunchID is the hooks folder generation a
	// scan result must come from; subagentWarm records whether the tracker
	// has applied a result for it yet. Guarded by mu. Ephemeral: never
	// serialized (absent from InstanceData).
	subagents    *subagent.Tracker
	hookLaunchID string
	subagentWarm bool

	// claude is what Claude's hooks and the agent roster report about this
	// session: its status, the conversation to resume and its last
	// message (see claude_state.go). Guarded by mu. sessionID and
	// transcriptPath are persisted (InstanceData v7); the rest is not.
	claude claudeState

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
		Program:             i.program,
		HeadroomProxy:       i.headroomProxy,
		CacheTTL1h:          i.cacheTTL1h,
		IsWorkspaceTerminal: i.IsWorkspaceTerminal,
		Issue:               i.issue,
		Account:             i.account,

		ClaudeSessionID:      i.claude.sessionID,
		ClaudeTranscriptPath: i.claude.transcriptPath,
	}

	if i.gitWorktree != nil {
		data.Worktree = GitWorktreeData{
			RepoPath:         i.gitWorktree.GetRepoPath(),
			WorktreePath:     i.gitWorktree.GetWorktreePath(),
			SessionName:      i.Title,
			BranchName:       i.gitWorktree.GetBranchName(),
			BaseCommitSHA:    i.gitWorktree.GetBaseCommitSHA(),
			IsExistingBranch: i.gitWorktree.IsExistingBranch(),
			StashRef:         i.gitWorktree.GetStashRef(),
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
	// Normalized the same way SetAccount normalizes it on write: "" and
	// account.DefaultName both mean the default account. SetAccount never
	// persists the literal "default" itself, but a hand-edited state.json
	// or a future writer could, and Instance.Account()'s "" means default"
	// invariant must hold regardless of what is on disk.
	acctName := data.Account
	if acctName == account.DefaultName {
		acctName = ""
	}
	instance := &Instance{
		Title:               data.Title,
		Path:                data.Path,
		Branch:              data.Branch,
		Status:              data.Status,
		Height:              data.Height,
		Width:               data.Width,
		CreatedAt:           data.CreatedAt,
		UpdatedAt:           data.UpdatedAt,
		program:             data.Program,
		headroomProxy:       data.HeadroomProxy,
		cacheTTL1h:          data.CacheTTL1h,
		ConfigDir:           configDir,
		IsWorkspaceTerminal: data.IsWorkspaceTerminal,
		issue:               data.Issue,
		account:             acctName,
		claude:              claudeState{sessionID: data.ClaudeSessionID, transcriptPath: data.ClaudeTranscriptPath},
		logger:              log.For("instance", "title", data.Title),
	}

	// Workspace terminals don't use git worktrees
	if !data.IsWorkspaceTerminal {
		gw := git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
			data.Worktree.IsExistingBranch,
			configDir,
		)
		gw.SetStashRef(data.Worktree.StashRef)
		instance.setGitWorktree(gw)
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

	// Recoverable placeholders (inline orphan-review entries) point at a
	// real worktree/tmux session on disk just like Paused, even though no
	// PTY has been attached yet — every isStarted()-gated accessor
	// (GetGitWorktree, RepoName, Kill) needs started=true here, or discard
	// silently no-ops and the row never leaves the list.
	if instance.Paused() || instance.GetStatus() == Recoverable {
		instance.setStarted(true)
		// Unpublished: nothing else can see instance yet, so the
		// launch fields are read directly.
		instance.setTmuxSession(tmux.NewTmuxSession(instance.Title, instance.program, InstanceEnv(LaunchEnv{Program: instance.program, HeadroomProxy: instance.headroomProxy, CacheTTL1h: instance.cacheTTL1h, ClaudeConfigDir: bestEffortAccountDir(instance.account)})...))
	}

	return instance, nil
}

// Restart brings an instance back online after its tmux session died
// out-of-band (e.g. a workspace terminal whose shell the user exited,
// or the tmux server was killed). Start(true) alone is a silent no-op
// on an already-started instance because reserveStart sees started=true
// and bails — that is the correct INST-04 behavior for a concurrent
// Start race, but the wrong behavior when the caller knows the tmux
// session is gone and wants to recreate it. Restart clears the flags
// first so Start can run its real path.
//
// A restart is a real launch: the dead session object is closed and
// replaced by one running a command freshly composed by launchProgram
// under a freshly resolved env — the account's CLAUDE_CONFIG_DIR (or
// Headroom Proxy / Cache TTL toggle) may have changed since the dead
// session was built — so the previous launch's subagent state and hooks
// folder are reset, new hooks are prepared, and the loom context flag is
// re-applied. Reusing the old object would relaunch its old command — for
// a session restored after a loom restart, the bare Program, with no
// hooks or context at all — while the tracker kept the dead process's
// rows. A launch whose account no longer resolves refuses before touching
// anything: no close, no flag reset, nothing to undo.
func (i *Instance) Restart() error {
	// Resolved before taking i.mu: launchEnv takes its own RLock, and
	// sync.RWMutex is not reentrant.
	env, err := i.launchEnv(true)
	if err != nil {
		return err
	}

	i.mu.Lock()
	old := i.tmuxSession
	i.started = false
	i.starting = false
	i.mu.Unlock()
	if old != nil {
		// Already dead; this only releases the PTY, emulator and pump.
		if closeErr := old.Close(); closeErr != nil {
			i.getLogger().Debug("instance.restart.close_old_failed", "err", closeErr.Error())
		}
		i.setTmuxSession(old.WithProgramEnv(i.launchProgram(env.Program, true), InstanceEnv(env)))
	}
	return i.Start(true)
}

// EnsureRunning attaches a PTY to the instance's tmux session, restoring
// any previously-persisted session state. A no-op for paused instances
// (they deliberately have no PTY) and for already-started instances.
// Idempotent: the underlying Start guards against double-attaches.
func (i *Instance) EnsureRunning() error {
	if i.GetStatus() == Recoverable {
		// An orphan surfaced inline; never auto-spawn its PTY. It goes
		// live only via the explicit recover action (ReconcileAndRestore).
		return nil
	}
	if i.Paused() {
		return nil
	}
	if i.isStarted() {
		return nil
	}
	return i.Start(false)
}

// InstanceOptions collects the arguments for NewInstance. Title, Path,
// Program, and ConfigDir are expected; the launch toggles, Prompt, Branch,
// and IsWorkspaceTerminal are optional.
type InstanceOptions struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Program is the program to run in the instance (e.g. "claude", "aider --model ollama_chat/gemma3:1b")
	Program string
	// HeadroomProxy controls whether this instance's tmux session gets
	// ANTHROPIC_BASE_URL pointed at Headroom's proxy. See Instance.HeadroomProxy.
	HeadroomProxy bool
	// CacheTTL1h controls whether this instance's tmux session gets
	// ENABLE_PROMPT_CACHING_1H=1. See Instance.CacheTTL1h.
	CacheTTL1h bool
	// Prompt is the initial prompt sent to the agent once the instance
	// has started (optional). See Instance.Prompt.
	Prompt string
	// Branch is an existing branch name to start the session on (empty = new branch from HEAD)
	Branch string
	// ConfigDir is the workspace config directory for worktree resolution.
	ConfigDir string
	// IsWorkspaceTerminal creates a workspace terminal instance (no worktree).
	IsWorkspaceTerminal bool
}

// NewInstance constructs a blank Instance in the Ready state. No git
// worktree or tmux session is allocated until Start(true) runs — this
// constructor only captures the caller's intent. The workspace path is
// resolved to an absolute path so worktree setup is not affected by later
// chdir calls.
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
		program:             opts.Program,
		headroomProxy:       opts.HeadroomProxy,
		cacheTTL1h:          opts.CacheTTL1h,
		prompt:              opts.Prompt,
		Height:              0,
		Width:               0,
		CreatedAt:           t,
		UpdatedAt:           t,
		selectedBranch:      opts.Branch,
		ConfigDir:           opts.ConfigDir,
		IsWorkspaceTerminal: opts.IsWorkspaceTerminal,
		logger:              log.For("instance", "title", opts.Title),
	}, nil
}

// RepoName returns the human-visible repo/workspace name, delegating
// to the instance's backend. Errors if called before the instance has
// been started.
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
	i.statusChangedAt = time.Now()
	return nil
}

// GetStatus returns the current status under a read lock.
func (i *Instance) GetStatus() Status {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.Status
}

// StatusAge returns how long the instance has been in its current
// status, or 0 when no transition has been observed this process.
func (i *Instance) StatusAge() time.Duration {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.statusChangedAt.IsZero() {
		return 0
	}
	return time.Since(i.statusChangedAt)
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

// SetBranchPrefix overrides the configured branch prefix for this instance.
// Call before Start(true); it has no effect afterwards, since the branch name
// is composed during first-time setup and fixed from then on.
func (i *Instance) SetBranchPrefix(prefix string) {
	i.branchPrefix = &prefix
}

// BranchPrefixOverride returns the per-session branch prefix override, or nil
// when none was set and the configured prefix applies.
func (i *Instance) BranchPrefixOverride() *string {
	return i.branchPrefix
}

// Program returns the agent command this instance launches (the bare
// program, before loom's context flag and subagent hooks are added).
func (i *Instance) Program() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.program
}

// SetProgram replaces the agent command. It takes effect at the next
// launch (Start, Resume, CrashRestart, Restart); a running session keeps
// the command it was started with.
func (i *Instance) SetProgram(program string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.program = program
}

// HeadroomProxy reports whether launches point ANTHROPIC_BASE_URL at
// Headroom's proxy (see HeadroomProxyEnv). Set via SetLaunchOptions.
func (i *Instance) HeadroomProxy() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.headroomProxy
}

// CacheTTL1h reports whether launches set ENABLE_PROMPT_CACHING_1H=1 (see
// CacheTTL1hEnv). Set via SetLaunchOptions.
func (i *Instance) CacheTTL1h() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.cacheTTL1h
}

// SetLaunchOptions sets the program and both launch-env toggles under one
// lock, so no reader (Snapshot, a launch in a Cmd goroutine) can observe a
// program from one confirmation paired with toggles from another. Like
// SetProgram, it takes effect at the next launch.
func (i *Instance) SetLaunchOptions(program string, headroomProxy, cacheTTL1h bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.program = program
	i.headroomProxy = headroomProxy
	i.cacheTTL1h = cacheTTL1h
}

// Account returns the Claude account this instance launches under: a
// registered account's name, or "" for the default account.
func (i *Instance) Account() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.account
}

// SetAccount records the account the next launch runs under.
// account.DefaultName and "" both mean the default account.
func (i *Instance) SetAccount(name string) {
	if name == account.DefaultName {
		name = ""
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.account = name
}

// launchEnv snapshots the launch fields under one lock, so a concurrent
// SetLaunchOptions can't tear them, and resolves the account's config dir.
// When launching, an unregistered account on a Claude program is an error
// (*MissingAccountError); otherwise it just yields no config dir.
func (i *Instance) launchEnv(launching bool) (LaunchEnv, error) {
	i.mu.RLock()
	env := LaunchEnv{Program: i.program, HeadroomProxy: i.headroomProxy, CacheTTL1h: i.cacheTTL1h}
	name := i.account
	i.mu.RUnlock()
	dir, err := accountDir(name)
	if err != nil && launching && IsClaudeProgram(env.Program) {
		return env, err
	}
	env.ClaudeConfigDir = dir
	return env, nil
}

// Prompt returns the initial prompt still waiting to be sent to the agent,
// or "" when there is none (or it has already been sent).
func (i *Instance) Prompt() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.prompt
}

// SetPrompt sets the initial prompt sent once the instance has started.
// Pass "" to clear it after sending.
func (i *Instance) SetPrompt(prompt string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.prompt = prompt
}

// CrashRecovered reports whether this instance was restored after a crash
// and still needs CrashRestart (an agent-aware relaunch, e.g. --continue).
// Runtime-only: never serialized.
func (i *Instance) CrashRecovered() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.crashRecovered
}

// SetCrashRecovered sets the crash-recovered flag; the app clears it once
// it has attempted CrashRestart.
func (i *Instance) SetCrashRecovered(v bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.crashRecovered = v
}

// Start brings the instance online: sets up the worktree, spawns the
// tmux session, and transitions Ready → Loading → Running. Pass
// firstTimeSetup=true when the instance is newly created (initial
// commit, copy user-supplied prompt) and false when loaded from
// storage. Returns an error if setup fails, leaving the instance in
// its prior status.
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

	// Error handler: release the start reservation on ANY failure below so
	// a retry isn't blocked by this failed attempt. Registered before the
	// worktree-construction block — its early returns previously leaked
	// the reservation, permanently wedging the instance. Resource cleanup
	// happens at the failure sites themselves: Kill() is useless here
	// because it no-ops until started is set true, which only happens in
	// the success branch.
	var setupErr error
	defer func() {
		if setupErr != nil {
			i.releaseStart()
		} else {
			i.setStarted(true)
		}
	}()

	// Resolved before the ts == nil branch below, and unconditionally: a
	// caller that pre-builds ts itself (Restart, via WithProgramEnv) still
	// goes through Start to launch it, and the account must be checked
	// every time Start actually launches (firstTimeSetup), not only when
	// Start also has to construct the session object.
	env, envErr := i.launchEnv(firstTimeSetup)
	if envErr != nil {
		setupErr = envErr
		return setupErr
	}

	ts := i.getTmuxSession()
	if ts == nil {
		// Create new tmux session. launchProgram adds loom's context flag
		// and, only when this Start actually launches (firstTimeSetup),
		// the subagent hooks. Start(false) reattaches with Restore, and
		// that Claude keeps writing to its existing hooks folder.
		// InstanceEnv still keys off the bare program.
		launchProgram := i.launchProgram(env.Program, firstTimeSetup)
		ts = tmux.NewTmuxSession(i.Title, launchProgram, InstanceEnv(env)...)
	}
	i.setTmuxSession(ts)

	// Workspace terminals skip worktree creation entirely
	var gw *git.GitWorktree
	if firstTimeSetup && !i.IsWorkspaceTerminal {
		if i.selectedBranch != "" {
			gitWorktree, err := git.NewGitWorktreeFromBranch(i.Path, i.selectedBranch, i.Title, i.ConfigDir)
			if err != nil {
				setupErr = fmt.Errorf("failed to create git worktree from branch: %w", err)
				return setupErr
			}
			i.mu.Lock()
			i.gitWorktree = gitWorktree
			i.Branch = i.selectedBranch
			i.mu.Unlock()
			gw = gitWorktree
		} else {
			gitWorktree, branchName, err := git.NewGitWorktreeFromSpec(git.WorktreeSpec{
				RepoPath:     i.Path,
				SessionName:  i.Title,
				ConfigDir:    i.ConfigDir,
				BranchPrefix: i.branchPrefix,
			})
			if err != nil {
				setupErr = fmt.Errorf("failed to create git worktree: %w", err)
				return setupErr
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
			setupErr = i.failedStartCleanup(ts, gw, err)
			return setupErr
		}
	}

	_ = i.TransitionTo(Running)

	return nil
}

// failedStartCleanup undoes the worktree Setup just made for a session
// whose ts.Start failed, and returns Start's error. The branch goes too
// only if Setup created it: a reused title checks out the branch an
// earlier session left behind.
//
// Only once the agent is known not to be running in it. A refused name
// (ErrSessionExists) launched nothing; any other failure may come after
// the agent was launched — the existence poll reads an unanswered probe
// as "not yet" under load, and the cleanup Close can fail too — so the
// session must then be confirmed Dead, as Pause requires before it removes
// a worktree. Deleting the tree under a live agent would orphan it,
// writing into a directory git has unlinked.
func (i *Instance) failedStartCleanup(ts *tmux.TmuxSession, gw *git.GitWorktree, startErr error) error {
	if !errors.Is(startErr, tmux.ErrSessionExists) && ts.SessionLiveness() != tmux.LivenessDead {
		i.getLogger().Warn("instance.start.cleanup_skipped", "worktree", gw.GetWorktreePath(), "err", startErr.Error())
		return fmt.Errorf("failed to start new session, and its agent may still be running: tmux session %s could not be confirmed gone, so the worktree %s and branch %s were left in place (check `tmux ls`; the next workspace load offers the worktree for recovery, or cleans it up once nothing runs in it): %w",
			ts.SessionName(), gw.GetWorktreePath(), gw.GetBranchName(), startErr)
	}
	if cleanupErr := gw.CleanupFailedStart(); cleanupErr != nil {
		startErr = fmt.Errorf("%v (cleanup error: %v)", startErr, cleanupErr)
	}
	return fmt.Errorf("failed to start new session: %w", startErr)
}

// Kill terminates the instance and cleans up all resources. A stash entry
// it cannot drop does not fail the kill (everything else is gone): Kill
// then returns a Notice carrying DropStash's error, which says what is
// left on the stash list.
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
	var notices []error

	// Always try to cleanup both resources, even if one fails
	// Clean up tmux session first since it's using the git worktree
	if tmuxSess != nil {
		if err := tmuxSess.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close tmux session: %w", err))
		}
		// See the matching comment in Pause: the terminal pane's tmux
		// session is untracked by Instance and must be killed alongside
		// the agent session, or it leaks until the next app-startup
		// orphan sweep. Best-effort: "no such session" is the common case.
		if err := tmuxSess.CloseRelatedSession(tmux.TerminalSessionName(i.Title)); err != nil {
			log.For("session").Debug("kill_close_terminal_tmux_failed", "title", i.Title, "err", err)
		}
	}

	// After the agent's tmux session is gone, so a SessionEnd hook firing
	// on exit finds no folder and exits harmlessly.
	i.removeSubagentHooks()

	// Then clean up git worktree (workspace terminals don't have one)
	if gitWT != nil && !isWorkspaceTerm {
		// A Paused-then-killed instance may still have an unapplied
		// stash (never resumed). Drop it so it doesn't leak on the
		// shared refs/stash stack forever, referencing a branch that
		// Cleanup is about to delete.
		if sha := gitWT.GetStashRef(); sha != "" {
			if err := gitWT.DropStash(sha); err != nil {
				log.For("session").Warn("kill.stash_drop_failed", "err", err.Error())
				notices = append(notices, fmt.Errorf("killed %s, but could not drop its paused stash %s from `git stash list`: %w", i.Title, sha, err))
			}
		}
		if err := gitWT.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup git worktree: %w", err))
		}
	}

	if err := i.combineErrors(errs); err != nil {
		// Cleanup failed, so resources still exist on disk — restore the
		// snapshot so a retried Kill actually re-attempts the cleanup
		// instead of no-oping on the started guard above.
		i.mu.Lock()
		i.started = true
		i.tmuxSession = tmuxSess
		i.gitWorktree = gitWT
		i.mu.Unlock()
		return errors.Join(err, NewNotice(notices...))
	}
	return NewNotice(notices...)
}

// combineErrors combines multiple errors into a single error. Uses
// errors.Join so callers can still use errors.Is/errors.As against
// any underlying cause — stringifying would have broken that chain.
func (i *Instance) combineErrors(errs []error) error {
	return errors.Join(errs...)
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

// Started reports whether Start or Resume has completed setup, so the
// instance has a live tmux session and worktree (or root-repo terminal).
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

// Paused reports whether the instance is currently in the Paused state
// (can be resumed with Resume). A real Pause removed the worktree, but an
// agent that exited on its own is marked Paused with its worktree still on
// disk; see decideResume.
func (i *Instance) Paused() bool {
	return i.GetStatus() == Paused
}

// RecordRestartFailure increments and returns the consecutive-failure
// counter backing the workspace-terminal auto-restart circuit breaker.
// Call once per metadata tick this instance's tmux is observed dead.
func (i *Instance) RecordRestartFailure() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.restartFailureCount++
	return i.restartFailureCount
}

// ResetRestartFailures clears the consecutive-failure counter. Call once
// per metadata tick this instance's tmux is observed alive.
func (i *Instance) ResetRestartFailures() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.restartFailureCount = 0
}

// RestartFailureCount reports the current consecutive-failure count.
func (i *Instance) RestartFailureCount() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.restartFailureCount
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

	// Stash any uncommitted changes (tracked and untracked) so Resume
	// can restore them without polluting the branch's real history
	// with a synthetic checkpoint commit.
	stashMsg := fmt.Sprintf("[loom] stash from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
	sha, stashErr := gw.StashChanges(stashMsg)
	if stashErr != nil {
		errs = append(errs, fmt.Errorf("failed to stash changes: %w", stashErr))
		// Return early if we can't stash changes to avoid corrupted state
		return i.combineErrors(errs)
	}
	gw.SetStashRef(sha)

	// Checkpoint the stash ref to disk before any destructive step. If
	// Remove() fails (or Loom dies) past this point, the next launch
	// otherwise reconciles the record with StashRef="" and Resume never
	// re-applies the stash — the user's uncommitted work would sit
	// invisible in `git stash list`.
	if sha != "" && saveState != nil {
		if err := saveState(); err != nil {
			errs = append(errs, fmt.Errorf("pause stash checkpoint save: %w", err))
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
		// A Close failure usually just means the session was already dead,
		// which is harmless — carry on. But a session that is still alive
		// still has an agent running with its cwd inside the worktree
		// removed below. Deleting it would orphan that process, leaving it
		// writing into a tree git has unlinked: the process leaks, and its
		// writes race `worktree remove` into leaving a half-removed
		// worktree that no later resume can recover. Abort instead; the
		// instance stays Running and the pause can be retried.
		//
		// Only an answered "no such session" clears us to proceed. The
		// same load that makes kill-session fail also starves the
		// liveness probe, and DoesSessionExist would read that
		// inconclusive probe as "already dead" — removing the worktree
		// under an agent we never established was gone.
		if ts.SessionLiveness() != tmux.LivenessDead {
			errs = append(errs, fmt.Errorf(
				"failed to stop the agent's tmux session and cannot confirm it is gone; worktree left intact: %w", err))
			// The agent keeps working in the worktree, which still holds
			// everything the stash captured (StashChanges never touches
			// it). Left pending, the stash is a stale copy: a later resume
			// would find the tree diverged and refuse, and the next pause
			// would overwrite the reference and leak the entry. Drop it and
			// save the cleared reference. Only here — past this point the
			// stash can be the only copy of the work.
			if sha != "" {
				if err := gw.DropStash(sha); err != nil {
					log.For("session").Warn("pause_abort_drop_stash_failed", "stash", sha, "err", err)
					errs = append(errs, fmt.Errorf("could not drop the abandoned pause stash %s from `git stash list` (the worktree still holds its changes, so the entry is a stale copy to drop by hand): %w", sha, err))
				}
				gw.SetStashRef("")
				if saveState != nil {
					if err := saveState(); err != nil {
						errs = append(errs, fmt.Errorf("pause abort save: %w", err))
					}
				}
			}
			return i.combineErrors(errs)
		}
	}

	// The terminal pane (ui.TerminalPane) runs its own tmux session for
	// this instance, keyed by the same title but tracked entirely outside
	// Instance/TmuxSession. If it survives past this point, its shell stays
	// cd'd into the worktree directory we're about to delete below; Resume
	// would then silently reattach to that now-orphaned directory instead
	// of the freshly recreated worktree. Kill it here so the invariant
	// holds for every caller, not just the UI's pauseActionFor. Best-effort:
	// "no such session" (never opened) is the common case.
	if err := ts.CloseRelatedSession(tmux.TerminalSessionName(i.Title)); err != nil {
		log.For("session").Debug("pause_close_terminal_tmux_failed", "err", err)
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

// Resume brings a Paused instance back to Running. It recreates the
// worktree from the branch only when the worktree is absent or gutted;
// an intact one is kept as it is and the agent relaunched in it, and a
// live session is reattached (see decideResume). Stashed changes are
// restored either way. If saveState is non-nil, it is called after the
// instance is Running, providing a checkpoint that reduces the crash
// inconsistency window.
//
// A resume that succeeded but forgot a stash no longer in `git stash
// list`, or restored one it then could not drop, returns a Notice saying
// so (see OnlyNotice); a failed one includes those notices in its error.
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

	// Setup (below) removes and re-adds the worktree directory, so it
	// may only run when nothing on disk can be lost (see decideResume).
	//
	// A live tmux session means this instance was never really paused:
	// Pause always kills the session before touching the worktree, so a
	// surviving one implies loom itself died while the agent was running.
	// That agent is still executing with its cwd inside the worktree, so
	// rebuilding it here would delete the tree out from under a live
	// process. It would keep running, orphaned, writing into a directory
	// git had unlinked, which both leaks the process and races
	// `worktree remove` into leaving a half-removed worktree behind.
	// Reattach to what is already there.
	//
	// A dead session over an intact worktree is the other way to arrive
	// here unpaused: the agent exited or crashed on its own and the health
	// tick only marked the instance Paused. Nothing was stashed, so the
	// worktree holds the only copy of any uncommitted work, and the
	// `git worktree remove -f` a rebuild starts with would delete it.
	// Relaunch the agent in the tree as it stands.
	live := ts.SessionLiveness()
	tree, treeErr := gw.InspectTree()
	action := decideResume(live, tree)

	// Resolved before anything below rebuilds the worktree or touches a
	// stash: a resume refused over an unresolvable account must change
	// nothing on disk. Reattaching to an already-live session launches
	// nothing, so it is let through regardless — an account removed with
	// --force, or an accounts.json that has since gone corrupt, must not
	// strand an otherwise-reachable agent. finishResume's own launch
	// paths (startFreshWithRecovery, CrashRestart) resolve the account
	// again at the moment they actually launch, which covers the rare
	// case where reattach itself falls back to a fresh launch (a dead
	// Restore).
	if action != resumeReattach {
		if _, envErr := i.launchEnv(true); envErr != nil {
			return envErr
		}
	}

	switch action {
	case resumeReattach:
		lg.Debug("instance.resume.reattach_live_session", "worktree", gw.GetWorktreePath())
		return i.finishResume(saveState, ts, gw)
	case resumeRelaunchInPlace:
		lg.Info("instance.resume.relaunch_in_place", "worktree", gw.GetWorktreePath())
		note, err := restoreStashInPlace(gw)
		if err != nil {
			return err
		}
		// A rebuild records a missing base commit in Setup; so must this.
		if err := gw.EnsureBaseCommit(); err != nil {
			lg.Warn("instance.resume.base_commit_failed", "worktree", gw.GetWorktreePath(), "err", err.Error())
		}
		return withNotices(i.finishResume(saveState, ts, gw), note)
	case resumeRefuse:
		if live == tmux.LivenessUnknown {
			return fmt.Errorf("cannot tell whether this session's agent is still running (tmux did not answer in time); leaving the worktree untouched — retry once the machine is less busy")
		}
		return unverifiedTreeError(gw.GetWorktreePath(), treeErr)
	}
	lg.Debug("instance.resume.rebuild", "worktree", gw.GetWorktreePath(), "tree", tree.String())

	// A pending stash no longer in `git stash list` was dropped by the
	// user (or by an apply whose cleared reference was never saved): it is
	// no longer wanted, and re-applying it would resurrect discarded work.
	// Settle that before anything on disk changes, as the in-place path
	// does, and fail closed when the list cannot be read.
	var notes []error
	sha := gw.GetStashRef()
	if sha != "" {
		ref, err := gw.StashListed(sha)
		if err != nil {
			return fmt.Errorf("cannot tell whether stash %s is still wanted; nothing was changed: %w", sha, err)
		}
		if ref == "" {
			notes = append(notes, forgetUnlistedStash(gw, sha))
			sha = ""
		}
	}

	// Setup git worktree
	if err := gw.Setup(); err != nil {
		if errors.Is(err, git.ErrBranchGone) {
			return withNotices(fmt.Errorf("branch %q was deleted externally — kill this instance (D) to clean up: %w", gw.GetBranchName(), err), notes...)
		}
		return withNotices(fmt.Errorf("failed to setup git worktree: %w", err), notes...)
	}

	// Restore any changes stashed by Pause. A failed apply (e.g. a
	// conflict) is surfaced rather than silently dropped or retried —
	// mirrors git's own refusal to drop a stash that didn't apply
	// cleanly. StashRef stays set so the entry (and any conflict
	// markers left in the worktree) remain available for the user to
	// resolve manually. An apply whose entry then could not be dropped
	// did restore the work: forget the reference, and pass the drop's
	// error on as a notice.
	if sha != "" {
		if err := gw.ApplyStash(sha); err != nil {
			if !errors.Is(err, git.ErrStashNotDropped) {
				// Name the stash so the user can act on it — it is preserved
				// (visible in `git stash list`) and this error is the only
				// place they ever hear about it.
				return withNotices(fmt.Errorf("failed to restore stashed changes (kept as stash %s — resolve in the worktree or apply manually via git stash): %w", sha, err), notes...)
			}
			notes = append(notes, err)
		}
		gw.SetStashRef("")
	}

	return withNotices(i.finishResume(saveState, ts, gw), notes...)
}

// withNotices returns err with notes joined in, or, when err is nil, the
// notes as a Notice (nil when there are none).
func withNotices(err error, notes ...error) error {
	if err != nil {
		return errors.Join(err, NewNotice(notes...))
	}
	return NewNotice(notes...)
}

// forgetUnlistedStash clears gw's pending stash sha, which is no longer in
// `git stash list`, and returns the notice the user must see: the stash
// may be the only copy of work, and after this loom no longer knows it.
func forgetUnlistedStash(gw *git.GitWorktree, sha string) error {
	log.For("session").Warn("resume.pending_stash_gone", "stash", sha, "worktree", gw.GetWorktreePath())
	gw.SetStashRef("")
	repo := gw.GetRepoPath()
	return fmt.Errorf("stash %s from this session's pause is no longer in `git stash list`, so loom did not re-apply it and has forgotten it. If it holds work you still need, apply it by its SHA before git garbage-collects it: `git -C %s stash apply %s` (compare `git -C %s stash list`; `git -C %s fsck --unreachable | grep commit` finds other dropped stashes)",
		sha, shellQuote(gw.GetWorktreePath()), sha, shellQuote(repo), shellQuote(repo))
}

// unverifiedTreeError is Resume's refusal for a worktree InspectTree could
// not vouch for. What the user should do depends on why: a git that never
// answered is worth retrying, while a tree git rejects (a .git pointing at
// a deleted admin dir, another repository, an unfinished `worktree add`)
// will be rejected again — say how to get past it instead.
func unverifiedTreeError(path string, err error) error {
	if git.IsTimeout(err) {
		return fmt.Errorf("git did not answer in time while checking the worktree at %s; leaving it untouched — retry once the machine is less busy: %w", path, err)
	}
	return fmt.Errorf("loom cannot verify %s as this session's worktree, so it may hold work loom cannot see; nothing was changed. If it holds nothing you need, move it aside (`mv %s %s`) and resume again: the worktree is then rebuilt from the branch: %w",
		path, shellQuote(path), shellQuote(path+".bak"), err)
}

// shellQuote quotes s for a POSIX shell, so a path in a command the user
// is told to run survives spaces and quotes: s goes in single quotes, and
// each single quote in it is closed, backslash-escaped and reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// restoreStashInPlace handles a pending StashRef when Resume relaunches in
// an existing worktree rather than a freshly rebuilt one. Pause's stash
// never modifies the worktree, so the reference can be pending here only
// if something interrupted the pause before the tree was removed (the
// stashed work is then still on disk), or a rebuild was interrupted
// before, or failed while, applying the stash.
//
//   - No longer in `git stash list`: whoever dropped it (the user, after a
//     refusal below, or an apply whose cleared reference was never saved)
//     no longer wants it applied. Forget it, whatever state the tree is
//     in — its commit may survive until gc, but applying it would
//     resurrect discarded work — and say so in the returned notice, since
//     on a clean tree it may be the only copy.
//   - Clean worktree: nothing on disk can be overwritten, so apply the
//     stash exactly as the rebuild path does.
//   - Worktree already matches the stash: the work is on disk; applying
//     it again is redundant. Forget the reference and drop the duplicate
//     entry.
//   - Worktree dirty and different: applying is not safe. `git stash
//     apply` refuses to overwrite unstaged changes, but merges into
//     staged ones and can leave conflict markers in them. Refuse, and
//     leave both untouched until the user reconciles them and drops the
//     entry.
//
// note is what the user must hear even though the restore went ahead: a
// forgotten stash, or a stash entry that could not be dropped.
func restoreStashInPlace(gw *git.GitWorktree) (note, err error) {
	sha := gw.GetStashRef()
	if sha == "" {
		return nil, nil
	}
	lg := log.For("session")
	ref, err := gw.StashListed(sha)
	if err != nil {
		return nil, fmt.Errorf("cannot tell whether stash %s is still wanted; leaving the worktree untouched: %w", sha, err)
	}
	if ref == "" {
		return forgetUnlistedStash(gw, sha), nil
	}
	dirty, err := gw.IsDirty()
	if err != nil {
		return nil, fmt.Errorf("cannot check the worktree for changes before restoring stash %s; leaving both untouched: %w", sha, err)
	}
	if !dirty {
		if err := gw.ApplyStash(sha); err != nil {
			if !errors.Is(err, git.ErrStashNotDropped) {
				return nil, fmt.Errorf("failed to restore stashed changes (kept as stash %s — resolve in the worktree or apply manually via git stash): %w", sha, err)
			}
			note = err // restored; only the entry is left
		}
		gw.SetStashRef("")
		return note, nil
	}
	onDisk, err := gw.StashOnDisk(sha)
	if err != nil {
		return nil, fmt.Errorf("cannot compare the worktree with stash %s; leaving both untouched: %w", sha, err)
	}
	if onDisk {
		lg.Info("resume.stash_already_on_disk", "stash", sha, "worktree", gw.GetWorktreePath())
		gw.SetStashRef("")
		if err := gw.DropStash(sha); err != nil {
			lg.Warn("resume.drop_redundant_stash_failed", "stash", sha, "err", err.Error())
			note = fmt.Errorf("the worktree already holds stash %s's changes, but its now redundant entry could not be dropped from `git stash list`: %w", sha, err)
		}
		return note, nil
	}
	wt := gw.GetWorktreePath()
	return nil, fmt.Errorf("the worktree at %s has uncommitted changes that differ from stash %s, left by an interrupted pause or restore; loom will not merge them. Compare them with `git -C %s stash show -p %s`, then either apply it yourself or keep the worktree as it is. Either way, drop that entry — find it by its SHA with `git -C %s stash list --format='%%gd %%H'`, since its stash@{N} position shifts as other sessions stash — and resume again",
		wt, sha, shellQuote(wt), sha, shellQuote(gw.GetRepoPath()))
}

// finishResume reattaches (or relaunches) the tmux session and marks the
// instance Running. Shared by every Resume path: the rebuild, which has
// just recreated the worktree, and the reattach and relaunch-in-place
// paths, which deliberately left the worktree alone.
func (i *Instance) finishResume(saveState func() error, ts *tmux.TmuxSession, gw *git.GitWorktree) error {
	switch ts.SessionLiveness() {
	case tmux.LivenessAlive:
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
	case tmux.LivenessDead:
		// The dead session object may still hold an attach client, an
		// emulator and an output pump; release them before replacing it,
		// as Restart does. Close kills by exact name, so this cannot reach
		// another session — and it must run before the new session under
		// the same name exists.
		if err := ts.Close(); err != nil {
			log.For("session").Debug("resume_close_dead_session", "err", err.Error())
		}
		if err := i.startFreshWithRecovery(gw); err != nil {
			return err
		}
	default:
		// No answer: the session may be live. Closing it could kill a
		// running agent, and starting another under its name would
		// collide with it. Leave both; the resume can be retried.
		return fmt.Errorf("cannot tell whether this session's tmux session is running (tmux did not answer in time); nothing was started — retry once the machine is less busy")
	}

	_ = i.TransitionTo(Running)
	if saveState != nil {
		if err := saveState(); err != nil {
			return fmt.Errorf("resume checkpoint save: %w", err)
		}
	}
	return nil
}

// newRecoverySession builds the tmux session a recovery launch
// (startFreshWithRecovery, CrashRestart) starts. A var so tests can
// substitute a session with fake PTY/exec dependencies: a real one would
// run tmux against whatever server the test process can reach.
var newRecoverySession = tmux.NewTmuxSession

// startFreshWithRecovery creates a brand-new tmux session for an instance
// whose previous session no longer exists (normal after crash or kill-server).
// The program is rewritten via BuildResumeCommand so Claude resumes its
// prior conversation (`claude --resume <id>`, or `--continue` when none is
// recorded).
//
// A failed start leaves the worktree alone. It is never scratch here: it
// is the tree the agent exited from, one a live session was using, or a
// rebuild with any stashed work already applied (and dropped from the
// stash list). Cleaning it up would also delete the session's branch.
// The instance stays Paused, and the next resume relaunches in place.
func (i *Instance) startFreshWithRecovery(gw *git.GitWorktree) error {
	launchProgram, env, err := i.recoveryLaunch()
	if err != nil {
		return err
	}
	ts := newRecoverySession(i.Title, launchProgram, env...)
	if err := ts.Start(gw.GetWorktreePath()); err != nil {
		return fmt.Errorf("failed to start new session: %w", err)
	}
	i.setTmuxSession(ts)
	return nil
}

// CrashRestart starts a new tmux session for a crash-recovered instance.
// The worktree already exists (for regular instances) or is unnecessary
// (for workspace terminals). The program is modified with --resume <id>
// (or --continue) for Claude, via BuildResumeCommand.
//
// Like Resume, it relaunches only into an intact worktree. Reconcile
// picks this path because the directory exists, but a gutted one has no
// .git, so git run inside it would answer for whatever repo encloses it
// (the workspace's own, when worktrees live under <repo>/.loom). Callers
// mark a failed restart Paused, and Resume then rebuilds a gutted tree
// with its leftovers moved aside.
func (i *Instance) CrashRestart() error {
	var workDir string
	if i.IsWorkspaceTerminal {
		workDir = i.Path
	} else {
		gw := i.getGitWorktree()
		if gw == nil {
			return fmt.Errorf("no git worktree for crash restart of %q", i.Title)
		}
		if tree, err := gw.InspectTree(); tree != git.TreeIntact {
			if err == nil {
				err = fmt.Errorf("worktree is %s", tree)
			}
			return fmt.Errorf("crash restart of %q: worktree %s is not an intact working tree: %w", i.Title, gw.GetWorktreePath(), err)
		}
		workDir = gw.GetWorktreePath()
	}

	launchProgram, env, launchErr := i.recoveryLaunch()
	if launchErr != nil {
		return launchErr
	}
	ts := newRecoverySession(i.Title, launchProgram, env...)

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
// branch is also short-circuited so a paused instance's tick stays cheap.
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

// BellPending reports whether an unseen bell is pending for this instance.
func (i *Instance) BellPending() bool { return i.bellPending.Load() }

// SetBellPending sets or clears the pending-bell attention flag.
func (i *Instance) SetBellPending(v bool) { i.bellPending.Store(v) }

// WaitReason returns Claude's reason for blocking, or "" when none is
// known — the roster is the only source, so a non-Claude agent or a
// scraper-driven status always yields "".
func (i *Instance) WaitReason() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.waitReason
}

// SetWaitReason records (or with "" clears) Claude's reason for blocking.
func (i *Instance) SetWaitReason(reason string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.waitReason = reason
}

// IssueNumber returns the linked GitHub issue, or 0.
func (i *Instance) IssueNumber() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.issue
}

// SetIssue links this session to a GitHub issue (0 unlinks).
func (i *Instance) SetIssue(n int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.issue = n
}

// GitHubState returns the last joined GitHub state for this session.
func (i *Instance) GitHubState() github.State {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.githubState
}

// SetGitHubState records the poller's join result.
func (i *Instance) SetGitHubState(s github.State) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.githubState = s
}

// Parity returns commits ahead/behind the base branch; ok is false
// when no count has succeeded yet.
func (i *Instance) Parity() (ahead, behind int, ok bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.ahead, i.behind, i.hasParity
}

func (i *Instance) setParity(ahead, behind int, ok bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.ahead, i.behind, i.hasParity = ahead, behind, ok
}

// UpdateParity recounts ahead/behind against base (a ref name such as
// "origin/main"). Skipped — leaving parity unknown — for workspace
// terminals, unstarted, paused or recoverable instances, and when base
// is empty. Runs one local git subprocess; call it from the metadata
// fan-out, never from Update.
func (i *Instance) UpdateParity(base string) {
	if base == "" || i.IsWorkspaceTerminal || !i.isStarted() {
		i.setParity(0, 0, false)
		return
	}
	if s := i.GetStatus(); s == Paused || s == Recoverable {
		i.setParity(0, 0, false)
		return
	}
	gw := i.getGitWorktree()
	if gw == nil {
		i.setParity(0, 0, false)
		return
	}
	ahead, behind, err := git.AheadBehind(gw.GetRepoPath(), gw.GetBranchName(), base, nil)
	if err != nil {
		i.getLogger().Debug("parity.failed", "base", base, "err", err.Error())
		i.setParity(0, 0, false)
		return
	}
	i.setParity(ahead, behind, true)
}

// SetTmuxSession sets the tmux session for testing purposes
func (i *Instance) SetTmuxSession(session *tmux.TmuxSession) {
	i.setTmuxSession(session)
}
