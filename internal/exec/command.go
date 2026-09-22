package exec

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// GitCommand builds a git invocation bound to ctx. Every git subprocess loom
// runs is built here (TestNoRawGitGhExec enforces it) so its messages are
// untranslated: callers classify failures by matching git's English stderr
// (isBranchAbsentErr, isWorktreeAbsentErr, …) and parse its English output
// (parseShortStat's "insertion"/"deletion"), and a user's non-English locale
// would otherwise silently misclassify both. Only the message language is
// forced (see gitEnv); the character set, collation and every other locale
// category stay the user's, because hooks, filters and credential helpers
// that git spawns inherit this environment and some (Ruby hooks, for one)
// break under an ASCII LC_CTYPE.
//
// A non-empty dir is passed as `git -C dir`; "" adds nothing and git runs in
// the process working directory (or c.Dir, if the caller sets one). The
// caller's args slice is never modified.
//
// c.Env is always set. A caller that needs extra variables appends them
// (`c.Env = append(c.Env, "GIT_INDEX_FILE=…")`) rather than rebuilding from
// os.Environ(), which would drop the locale overrides.
func GitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := make([]string, 0, len(args)+2)
	if dir != "" {
		full = append(full, "-C", dir)
	}
	full = append(full, args...)
	c := exec.CommandContext(ctx, "git", full...)
	c.Env = gitEnv(os.Environ())
	return c
}

// localeCategories lists every locale category except LC_MESSAGES: the ones
// gitEnv pins to the user's LC_ALL when it has to drop LC_ALL.
var localeCategories = []string{
	"LC_CTYPE", "LC_NUMERIC", "LC_TIME", "LC_COLLATE", "LC_MONETARY",
	"LC_PAPER", "LC_NAME", "LC_ADDRESS", "LC_TELEPHONE", "LC_MEASUREMENT",
	"LC_IDENTIFICATION",
}

// gitEnv returns environ with git's message language forced to C and every
// other locale category left as the user had it.
//
// LC_MESSAGES=C (appended last) is what gettext and strerror translate from,
// and once it resolves to C gettext ignores LANGUAGE too. It only takes
// effect with LC_ALL unset, since LC_ALL outranks every category, so LC_ALL
// is dropped. A non-empty LC_ALL=X was the user's effective value for every
// category, overriding any individual LC_* they set, so each category other
// than LC_MESSAGES is re-pinned to X, which keeps their effective locale
// exactly. LANGUAGE is dropped as well: it cannot win against a C
// LC_MESSAGES, and removing it leaves nothing to reason about. LANG is left
// alone; it only fills categories nothing else set.
//
// environ is never modified.
func gitEnv(environ []string) []string {
	lcAll := ""
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "LC_ALL" {
			lcAll = v // last wins, as exec would dedupe it
		}
	}
	drop := map[string]bool{"LC_ALL": true, "LANGUAGE": true, "LC_MESSAGES": true}
	if lcAll != "" {
		for _, cat := range localeCategories {
			drop[cat] = true
		}
	}
	env := make([]string, 0, len(environ)+len(localeCategories)+1)
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			env = append(env, kv)
		}
	}
	if lcAll != "" {
		for _, cat := range localeCategories {
			env = append(env, cat+"="+lcAll)
		}
	}
	return append(env, "LC_MESSAGES=C")
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
