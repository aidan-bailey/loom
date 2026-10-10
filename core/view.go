package core

import (
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/subagent"
)

// InstanceID names one record: a hash of its workspace's canonical config
// dir, its title and its creation time (idOf), so every daemon over one
// disk gives it the same ID and a client that rejoins another daemon keeps
// naming the same sessions. A request naming an ID can't reach another
// instance: a same-titled one in another workspace differs by config dir,
// and one created under the title of an instance killed meanwhile by
// creation time (a collision between two live records probes to the next
// free value). 0 is never assigned; the TUI uses it for a draft row.
type InstanceID uint64

// InstanceView is an instance as a client sees it: a value copied out of the
// model (viewOf) carrying everything the TUI renders or gates a key on, and
// nothing that acts. Clients change an instance only through requests that
// name its ID. The model publishes each workspace's views when they change
// (Sync, ViewsChanged).
type InstanceView struct {
	ID    InstanceID
	Title string
	// RepoPath is the repository the session works in (Instance.Path).
	RepoPath     string
	WorktreePath string
	// WorktreeRepoPath is the repository root its worktree belongs to, as
	// git resolved it when the worktree was made (GitWorktree.GetRepoPath):
	// RepoPath through symlinks, or the root above a subdirectory. "" until
	// the instance has started, and for a workspace terminal. Lua's
	// worktree handle is built on it.
	WorktreeRepoPath string
	Branch           string
	// TmuxSession is the agent's tmux session name ("" before it has one);
	// pane clients attach by it. SessionProgram is the program that session
	// was launched with, which picks the client's adapter (trust-prompt and
	// pending-prompt patterns); Program can already name a new one after R.
	TmuxSession    string
	SessionProgram string
	Program        string
	Status         session.Status
	// StatusSince is when Status was entered (zero: not this process).
	StatusSince time.Time
	// StatusReported is set when Claude's hooks or roster have an opinion
	// on the status (Instance.ClaudeStatus). Without one the TUI may show its
	// own pane-scraped status instead (its ladder overlay).
	StatusReported bool
	WaitReason     string
	LastMessage    string
	HasLastMessage bool
	Subagents      []subagent.View
	// Diff is the latest diff stats, valid when HasDiff.
	Diff                git.DiffStats
	HasDiff             bool
	GitHub              github.State
	Ahead, Behind       int
	ParityKnown         bool
	Issue               int
	Account             string
	HeadroomProxy       bool
	CacheTTL1h          bool
	IsWorkspaceTerminal bool
	Started             bool
	// Bell is the TUI's own overlay (a bell rang in the pane since it was
	// last focused). The model never sets it.
	Bell bool
}

// Paused reports whether the view's status is Paused.
func (v InstanceView) Paused() bool { return v.Status == session.Paused }

// StatusAge is how long the instance has been in its status, 0 when no
// transition was observed this process (as Instance.StatusAge).
func (v InstanceView) StatusAge() time.Duration {
	if v.StatusSince.IsZero() {
		return 0
	}
	return time.Since(v.StatusSince)
}

// Active reports whether the instance is running for the purposes of the
// background jobs and the TUI's pane clients: started, and not Paused,
// Deleting, Recoverable or Loading. It is activeInstance's and
// statusEligible's rule, for a view.
func (v InstanceView) Active() bool {
	switch v.Status {
	case session.Paused, session.Deleting, session.Recoverable, session.Loading:
		return false
	}
	return v.Started
}
