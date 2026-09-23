package session

import (
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"
)

// resumeAction is what Resume should do about the instance's worktree.
type resumeAction int

const (
	// resumeRebuild recreates the worktree from the branch. Only for a
	// worktree that is absent (the normal paused case) or gutted, whose
	// leftovers the rebuild moves aside rather than deletes.
	resumeRebuild resumeAction = iota
	// resumeReattach leaves the worktree untouched and reattaches to the
	// session already using it.
	resumeReattach
	// resumeRefuse aborts without touching the worktree, because we
	// cannot establish that doing so is safe.
	resumeRefuse
	// resumeRelaunchInPlace leaves an intact worktree exactly as it is and
	// starts a fresh agent session in it: the agent exited (or crashed)
	// on its own, so the tree was never paused and may hold uncommitted
	// work that only exists on disk.
	resumeRelaunchInPlace
)

// decideResume chooses how Resume treats the worktree, given what the
// liveness probe established and what InspectTree found on disk.
//
// The governing rule: never destroy a worktree on evidence we do not
// have. Rebuilding runs `git worktree remove -f`, which deletes an intact
// tree's uncommitted work outright, and deletes the directory out from
// under an agent that is still running in it. So:
//   - Nothing on disk: rebuild — there is nothing to lose.
//   - A live agent: reattach to it.
//   - A probe that never answered cannot rule out a live agent: refuse.
//   - A dead agent in an intact tree is the agent having exited on its
//     own: the health tick marks that Paused without a real Pause, so
//     nothing was stashed and the tree still holds the work. Relaunch in
//     place; never rebuild it.
//   - A dead agent in a gutted tree (no .git): rebuild, which moves the
//     leftovers aside rather than deleting them.
//   - A tree git could not vouch for: refuse — it may be intact.
func decideResume(live tmux.Liveness, tree git.TreeState) resumeAction {
	if tree == git.TreeAbsent {
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
	}
	// LivenessDead — tmux answered, nothing is using the tree.
	switch tree {
	case git.TreeIntact:
		return resumeRelaunchInPlace
	case git.TreeGutted:
		return resumeRebuild
	default: // TreeUnverified
		return resumeRefuse
	}
}
