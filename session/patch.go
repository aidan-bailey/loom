package session

import (
	"fmt"

	"github.com/aidan-bailey/loom/session/git"
)

// ReadPatch returns the worktree's diff against its base commit, file by
// file (git.SplitPatch), with Base set. It leaves the cached diff stats
// alone: the published view carries those to every client, and a patch is
// read for one. A session with no worktree is an error.
func (i *Instance) ReadPatch() (git.Patch, error) {
	gw := i.getGitWorktree()
	if gw == nil || i.IsWorkspaceTerminal {
		return git.Patch{}, fmt.Errorf("read the diff of %s: the session has no worktree", i.Title)
	}
	base := gw.GetBaseCommitSHA()
	text, err := gw.Patch()
	if err != nil {
		return git.Patch{}, fmt.Errorf("read the diff of %s: %w", i.Title, err)
	}
	return git.Patch{Base: base, Files: git.SplitPatch(text)}, nil
}
