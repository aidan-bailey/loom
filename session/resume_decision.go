package session

import "github.com/aidan-bailey/loom/session/tmux"

// resumeAction is what Resume should do about the instance's worktree.
type resumeAction int

const (
	// resumeRebuild recreates the worktree — the normal paused case.
	resumeRebuild resumeAction = iota
	// resumeReattach leaves the worktree untouched and reattaches to the
	// session already using it.
	resumeReattach
	// resumeRefuse aborts without touching the worktree, because we
	// cannot establish that doing so is safe.
	resumeRefuse
)

// decideResume chooses how Resume treats the worktree, given what the
// liveness probe established and whether the directory is still on disk.
//
// The governing rule: never destroy a worktree on evidence we do not
// have. Rebuilding runs `git worktree remove -f`, so doing it while an
// agent is live inside the tree deletes the directory out from under a
// running process. A probe that never answered cannot rule that out, so
// it refuses rather than guesses — unless there is no worktree at all, in
// which case there is nothing to destroy and rebuilding is safe.
func decideResume(live tmux.Liveness, worktreeExists bool) resumeAction {
	if !worktreeExists {
		// Nothing on disk to lose, so the probe's answer cannot make
		// rebuilding unsafe.
		return resumeRebuild
	}
	switch live {
	case tmux.LivenessAlive:
		// An agent is working in there; reattach instead of rebuilding.
		return resumeReattach
	case tmux.LivenessUnknown:
		return resumeRefuse
	default: // LivenessDead — tmux answered, nothing is using the tree.
		return resumeRebuild
	}
}
