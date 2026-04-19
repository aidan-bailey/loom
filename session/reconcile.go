package session

import (
	internalexec "claude-squad/internal/exec"
	"claude-squad/log"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const reconcileTmuxTimeout = 5 * time.Second

// RecoveryAction describes what to do with an instance during startup reconciliation.
type RecoveryAction int

const (
	// ActionNoChange means the instance is already in a consistent state (e.g. Paused).
	ActionNoChange RecoveryAction = iota
	// ActionRestore means tmux + worktree are healthy; do a normal restore.
	ActionRestore
	// ActionRestart means tmux is dead but worktree exists; restart with scrollback + agent context.
	ActionRestart
	// ActionMarkPaused means both tmux and worktree are gone; mark as Paused (branch is preserved).
	ActionMarkPaused
	// ActionKillAndPause means tmux is alive but worktree is gone; kill tmux, mark Paused.
	ActionKillAndPause
	// ActionRestartWsTerminal means workspace terminal's tmux is dead; recreate it.
	ActionRestartWsTerminal
)

// CheckTmuxAlive checks if a tmux session exists by its sanitized name.
func CheckTmuxAlive(sessionTitle string, cmdExec internalexec.Executor) bool {
	sanitized := tmux.ToClaudeSquadTmuxName(sessionTitle)
	ctx, cancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
	defer cancel()
	existsCmd := exec.CommandContext(ctx, "tmux", "has-session", "-t="+sanitized)
	return cmdExec.Run(existsCmd) == nil
}

// CheckWorktreeExists checks if the worktree directory exists on disk.
func CheckWorktreeExists(worktreePath string) bool {
	if worktreePath == "" {
		return false
	}
	_, err := os.Stat(worktreePath)
	return err == nil
}

// DetermineRecoveryAction decides what to do with a loaded instance based on
// its persisted status and current filesystem/tmux state.
func DetermineRecoveryAction(status Status, tmuxAlive, worktreeExists, isWorkspaceTerminal bool) RecoveryAction {
	if status == Paused {
		return ActionNoChange
	}

	if isWorkspaceTerminal {
		if tmuxAlive {
			return ActionRestore
		}
		return ActionRestartWsTerminal
	}

	switch {
	case tmuxAlive && worktreeExists:
		return ActionRestore
	case tmuxAlive && !worktreeExists:
		return ActionKillAndPause
	case !tmuxAlive && worktreeExists:
		return ActionRestart
	default: // !tmuxAlive && !worktreeExists
		return ActionMarkPaused
	}
}

// ReconcileAndRestore loads an instance from serialized data, checks the health
// of its tmux session and worktree, and takes the appropriate recovery action.
func ReconcileAndRestore(data InstanceData, configDir string, cmdExec internalexec.Executor) (*Instance, error) {
	tmuxAlive := CheckTmuxAlive(data.Title, cmdExec)
	wtExists := CheckWorktreeExists(data.Worktree.WorktreePath)
	action := DetermineRecoveryAction(data.Status, tmuxAlive, wtExists, data.IsWorkspaceTerminal)
	logRecoveryAction(data.Title, action)

	switch action {
	case ActionNoChange:
		return fromInstanceDataPaused(data, configDir)

	case ActionRestore:
		instance, err := FromInstanceData(data, configDir)
		if err != nil {
			return nil, err
		}
		if err := instance.EnsureRunning(); err != nil {
			return nil, err
		}
		return instance, nil

	case ActionRestart:
		instance, err := fromInstanceDataPaused(data, configDir)
		if err != nil {
			return nil, err
		}
		instance.CrashRecovered = true
		return instance, nil

	case ActionMarkPaused:
		data.Status = Paused
		return fromInstanceDataPaused(data, configDir)

	case ActionKillAndPause:
		sanitized := tmux.ToClaudeSquadTmuxName(data.Title)
		killCtx, killCancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
		killCmd := exec.CommandContext(killCtx, "tmux", "kill-session", "-t="+sanitized)
		_ = cmdExec.Run(killCmd) // best-effort
		killCancel()
		data.Status = Paused
		return fromInstanceDataPaused(data, configDir)

	case ActionRestartWsTerminal:
		instance, err := fromInstanceDataPaused(data, configDir)
		if err != nil {
			return nil, err
		}
		instance.CrashRecovered = true
		return instance, nil

	default:
		return nil, fmt.Errorf("unknown recovery action: %d", action)
	}
}

// fromInstanceDataPaused creates an Instance from serialized data in a paused/stopped
// state. It sets started=true and creates a TmuxSession object but does not connect.
func fromInstanceDataPaused(data InstanceData, configDir string) (*Instance, error) {
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

	if data.DiffStats.Added != 0 || data.DiffStats.Removed != 0 || data.DiffStats.Content != "" {
		instance.setDiffStats(&git.DiffStats{
			Added:   data.DiffStats.Added,
			Removed: data.DiffStats.Removed,
			Content: data.DiffStats.Content,
		})
	}

	instance.setStarted(true)
	instance.setTmuxSession(tmux.NewTmuxSession(instance.Title, instance.Program))
	return instance, nil
}

// CleanupOrphanedSessions kills any tmux sessions with the claude-squad prefix
// that are not claimed by a loaded instance.
func CleanupOrphanedSessions(claimedTitles map[string]bool, cmdExec internalexec.Executor) error {
	listCtx, listCancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
	defer listCancel()
	listCmd := exec.CommandContext(listCtx, "tmux", "ls")
	output, err := cmdExec.Output(listCmd)
	if err != nil {
		// No tmux server running — nothing to clean up
		return nil
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, tmux.TmuxPrefix) {
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		sessionName := line[:colonIdx]

		// Check if any claimed instance owns this session
		claimed := false
		for title := range claimedTitles {
			if tmux.ToClaudeSquadTmuxName(title) == sessionName {
				claimed = true
				break
			}
		}

		if !claimed {
			log.InfoLog.Printf("killing orphaned tmux session: %s", sessionName)
			killCtx, killCancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
			killCmd := exec.CommandContext(killCtx, "tmux", "kill-session", "-t", sessionName)
			if err := cmdExec.Run(killCmd); err != nil {
				log.ErrorLog.Printf("failed to kill orphaned session %s: %v", sessionName, err)
			}
			killCancel()
		}
	}
	return nil
}

// logRecoveryAction logs the recovery action taken for an instance.
func logRecoveryAction(title string, action RecoveryAction) {
	switch action {
	case ActionRestore:
		log.InfoLog.Printf("instance %q: tmux+worktree healthy, restoring", title)
	case ActionRestart:
		log.InfoLog.Printf("instance %q: tmux dead but worktree exists, will restart", title)
	case ActionMarkPaused:
		log.InfoLog.Printf("instance %q: tmux+worktree gone, marking paused", title)
	case ActionKillAndPause:
		log.InfoLog.Printf("instance %q: tmux alive but worktree gone, killing tmux and pausing", title)
	case ActionRestartWsTerminal:
		log.InfoLog.Printf("instance %q: workspace terminal tmux dead, will restart", title)
	}
}
