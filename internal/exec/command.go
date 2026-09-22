package exec

import (
	"context"
	"os"
	"os/exec"
)

// GitCommand builds a git invocation bound to ctx. Every git subprocess loom
// runs is built here (TestNoRawGitGhExec enforces it) so it runs with
// LC_ALL=C: callers classify failures by matching git's English stderr
// (isBranchAbsentErr, isWorktreeAbsentErr, …) and parse its English output
// (parseShortStat's "insertion"/"deletion"), and a user's non-English locale
// would otherwise silently misclassify both.
//
// A non-empty dir is passed as `git -C dir`; "" adds nothing and git runs in
// the process working directory (or c.Dir, if the caller sets one). The
// caller's args slice is never modified.
//
// c.Env is always set. A caller that needs extra variables appends them
// (`c.Env = append(c.Env, "GIT_INDEX_FILE=…")`) rather than rebuilding from
// os.Environ(), which would drop LC_ALL=C.
func GitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := make([]string, 0, len(args)+2)
	if dir != "" {
		full = append(full, "-C", dir)
	}
	full = append(full, args...)
	c := exec.CommandContext(ctx, "git", full...)
	// LC_ALL goes last: exec keeps the last value of a duplicated key, so
	// this outranks any LC_ALL the user exported.
	c.Env = append(os.Environ(), "LC_ALL=C")
	return c
}

// GhCommand builds a gh invocation bound to ctx. Every gh subprocess loom
// runs is built here (TestNoRawGitGhExec enforces it). loom's gh calls never
// have a terminal: GH_PROMPT_DISABLED=1 guarantees a would-be prompt fails
// fast instead of waiting out the deadline, and GH_NO_UPDATE_NOTIFIER=1
// keeps the "new release available" check off the network and out of the
// captured output. gh already skips both without a TTY; the variables pin
// that rather than relying on gh's detection.
func GhCommand(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, "gh", args...)
	c.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	return c
}
