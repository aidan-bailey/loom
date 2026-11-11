package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// privateIndexName is the diff probe's own index file, in the worktree's
// git dir (<repo>/.git/worktrees/<name>/), where git ignores a file it
// doesn't know and `git worktree remove` and prune delete it with the dir.
const privateIndexName = "loom-diff-index"

// probeEnv points a probe command at the private index and keeps it from
// taking optional locks, so it never touches the index an agent's git uses.
func probeEnv(privateIndex string) []string {
	return []string{"GIT_INDEX_FILE=" + privateIndex, noOptionalLocks}
}

// runProbe runs a probe command on the private index, stdout and stderr
// combined.
func (g *GitWorktree) runProbe(env []string, args ...string) (string, error) {
	return g.runGitCommandEnv(env, g.worktreePath, args...)
}

// prepareProbe readies the private index for a diff and returns the
// environment every probe command runs with. The probe never touches the
// worktree's real index: `git add -N .` there takes index.lock, and any git
// command an agent started meanwhile (a merge) failed on it (feedback
// thread 17). Instead the real index is copied into the private one when it
// has changed since the last copy, and untracked files are added there.
//
// probeMu serializes the copy and the add, which both write the private
// index; the diff that follows only reads it and runs outside the lock.
func (g *GitWorktree) prepareProbe(now func() time.Time) ([]string, error) {
	g.probeMu.Lock()
	defer g.probeMu.Unlock()
	gitDir, err := worktreeGitDir(g.worktreePath, g.runner)
	if err != nil {
		return nil, err
	}
	private := filepath.Join(gitDir, privateIndexName)
	if err := g.refreshPrivateIndex(gitDir, private); err != nil {
		return nil, err
	}
	env := probeEnv(private)
	if err := g.ensureUntrackedStaged(env, now); err != nil {
		return nil, err
	}
	return env, nil
}

// refreshPrivateIndex copies the real index into the private one before the
// first probe and whenever the real index has been replaced since (git
// writes it by rename, so a new file, size or mtime means a new index, and
// a read sees a whole file). With no real index it starts the private one
// empty. A copy drops what earlier probes added, so it resets the untracked
// cache. Called with probeMu held.
func (g *GitWorktree) refreshPrivateIndex(gitDir, private string) error {
	real, err := os.Stat(filepath.Join(gitDir, "index"))
	if errors.Is(err, fs.ErrNotExist) {
		real = nil
	} else if err != nil {
		return fmt.Errorf("read the worktree's index: %w", err)
	}
	if g.probeIndex == private && sameIndexFile(g.probeIndexFrom, real) {
		if real == nil {
			return nil
		}
		if _, err := os.Stat(private); err == nil {
			return nil
		}
	}
	if real == nil {
		if err := os.Remove(private); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("empty the diff probe's index: %w", err)
		}
	} else if err := copyIndex(filepath.Join(gitDir, "index"), private); err != nil {
		return err
	}
	g.probeIndex, g.probeIndexFrom = private, real
	g.untrackedCacheMu.Lock()
	g.untrackedCheckedAt = time.Time{}
	g.untrackedCacheMu.Unlock()
	return nil
}

// sameIndexFile reports whether a and b describe the same index file
// unchanged: both absent, or the same file with the same size and mtime.
func sameIndexFile(a, b fs.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// copyIndex copies the index at src to dst through a temporary file
// renamed into place, so a probe reading dst never sees half a copy.
func copyIndex(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read the worktree's index: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), privateIndexName+".tmp*")
	if err != nil {
		return fmt.Errorf("copy the worktree's index: %w", err)
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("copy the worktree's index: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("copy the worktree's index: %w", err)
	}
	return nil
}

// ensureUntrackedStaged adds untracked files to the private index as
// intent-to-add, so the diff shows them as new files, behind a cached
// untracked-file probe. ls-files runs on the private index, so a file an
// earlier probe added there is no longer "other": once every untracked file
// is added, the probe finds none and the cache holds for untrackedCacheTTL,
// skipping ls-files. A copy of the real index resets the cache. A deleted
// file's leftover intent-to-add entry shows nothing in a diff, so the
// private index needs no pruning.
//
// now is injected so tests can pin the clock without touching time.Now().
func (g *GitWorktree) ensureUntrackedStaged(env []string, now func() time.Time) error {
	g.untrackedCacheMu.Lock()
	fresh := !g.untrackedCheckedAt.IsZero() &&
		now().Sub(g.untrackedCheckedAt) < untrackedCacheTTL &&
		!g.untrackedHadAny
	g.untrackedCacheMu.Unlock()
	if fresh {
		return nil
	}

	untrackedOutput, err := g.runProbe(env, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	hadUntracked := strings.TrimSpace(untrackedOutput) != ""

	g.untrackedCacheMu.Lock()
	g.untrackedCheckedAt = now()
	g.untrackedHadAny = hadUntracked
	g.untrackedCacheMu.Unlock()

	if hadUntracked {
		if _, err := g.runProbe(env, "add", "-N", "."); err != nil {
			return err
		}
	}
	return nil
}

// DiffStats holds statistics about the changes in a diff
type DiffStats struct {
	// Content is the full diff content
	Content string
	// Added is the number of added lines
	Added int
	// Removed is the number of removed lines
	Removed int
	// Error holds any error that occurred during diff computation
	// This allows propagating setup errors (like missing base commit) without breaking the flow
	Error error
}

// diffStatsJSON is DiffStats as it crosses a process boundary: the error
// as its message, which is all a client shows of it.
type diffStatsJSON struct {
	Content        string
	Added, Removed int
	Error          string `json:",omitempty"`
}

// MarshalJSON encodes d with its error as text.
func (d DiffStats) MarshalJSON() ([]byte, error) {
	j := diffStatsJSON{Content: d.Content, Added: d.Added, Removed: d.Removed}
	if d.Error != nil {
		j.Error = d.Error.Error()
	}
	return json.Marshal(j)
}

// UnmarshalJSON decodes a DiffStats MarshalJSON encoded.
func (d *DiffStats) UnmarshalJSON(b []byte) error {
	var j diffStatsJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	*d = DiffStats{Content: j.Content, Added: j.Added, Removed: j.Removed}
	if j.Error != "" {
		d.Error = errors.New(j.Error)
	}
	return nil
}

// IsEmpty reports whether the diff contains no added lines, no
// removed lines, and no body content — the state presented when a
// session has not yet diverged from its base commit.
func (d *DiffStats) IsEmpty() bool {
	return d.Added == 0 && d.Removed == 0 && d.Content == ""
}

// CurrentBranch returns the current branch name for the given repo directory.
// Pass nil for runner to use the default subprocess runner.
func CurrentBranch(repoPath string, runner CommandRunner) (string, error) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := internalexec.GitCommand(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := r.CombinedOutput(cmd)
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %s (%w)", output, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// noOptionalLocks keeps a read-only git command from taking index.lock to
// refresh stat info in the user's own checkout.
const noOptionalLocks = "GIT_OPTIONAL_LOCKS=0"

// DiffUncommitted returns the diff of uncommitted changes in the given repo directory.
// Used for workspace terminals that operate on the root repo without a worktree.
// Only shows tracked file changes, and takes no optional locks, to avoid
// touching the user's git index.
// Pass nil for runner to use the default subprocess runner.
func DiffUncommitted(repoPath string, runner CommandRunner) *DiffStats {
	r := defaultRunner(runner)
	stats := &DiffStats{}

	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := internalexec.GitCommand(ctx, repoPath, "--no-pager", "diff", "HEAD")
	cmd.Env = append(cmd.Env, noOptionalLocks)
	output, err := r.CombinedOutput(cmd)
	if err != nil {
		stats.Error = fmt.Errorf("git diff failed: %s (%w)", output, err)
		return stats
	}

	content := string(output)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			stats.Added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			stats.Removed++
		}
	}
	stats.Content = content

	return stats
}

// parseShortStat parses the output of git diff --shortstat.
// Example: " 3 files changed, 10 insertions(+), 5 deletions(-)\n"
func parseShortStat(output string) (added, removed int) {
	output = strings.TrimSpace(output)
	if output == "" {
		return 0, 0
	}
	parts := strings.Split(output, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		fields := strings.Fields(part)
		if len(fields) >= 2 {
			if strings.Contains(part, "insertion") {
				fmt.Sscanf(fields[0], "%d", &added)
			} else if strings.Contains(part, "deletion") {
				fmt.Sscanf(fields[0], "%d", &removed)
			}
		}
	}
	return added, removed
}

// DiffShortStat returns only the line counts (Added/Removed) without full diff content.
// Uses git diff --shortstat which is significantly cheaper for large diffs.
func (g *GitWorktree) DiffShortStat() *DiffStats {
	stats := &DiffStats{}

	if g.GetBaseCommitSHA() == "" {
		stats.Error = fmt.Errorf("base commit SHA not set")
		return stats
	}

	env, err := g.prepareProbe(time.Now)
	if err != nil {
		stats.Error = err
		return stats
	}

	content, err := g.runProbe(env, "--no-pager", "diff", "--shortstat", g.GetBaseCommitSHA())
	if err != nil {
		stats.Error = err
		return stats
	}
	stats.Added, stats.Removed = parseShortStat(content)
	return stats
}

// DiffUncommittedShortStat returns only line counts for uncommitted changes.
// Pass nil for runner to use the default subprocess runner.
func DiffUncommittedShortStat(repoPath string, runner CommandRunner) *DiffStats {
	r := defaultRunner(runner)
	stats := &DiffStats{}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := internalexec.GitCommand(ctx, repoPath, "--no-pager", "diff", "--shortstat", "HEAD")
	cmd.Env = append(cmd.Env, noOptionalLocks)
	output, err := r.CombinedOutput(cmd)
	if err != nil {
		stats.Error = fmt.Errorf("git diff --shortstat failed: %s (%w)", output, err)
		return stats
	}
	stats.Added, stats.Removed = parseShortStat(string(output))
	return stats
}

// Diff returns the git diff between the worktree and its base commit —
// the commit the session branched from (see ResolveBaseCommit) — along
// with statistics.
func (g *GitWorktree) Diff() *DiffStats {
	stats := &DiffStats{}

	if g.GetBaseCommitSHA() == "" {
		stats.Error = fmt.Errorf("base commit SHA not set")
		return stats
	}

	env, err := g.prepareProbe(time.Now)
	if err != nil {
		stats.Error = err
		return stats
	}

	content, err := g.runProbe(env, "--no-pager", "diff", g.GetBaseCommitSHA())
	if err != nil {
		stats.Error = err
		return stats
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			stats.Added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			stats.Removed++
		}
	}
	stats.Content = content

	return stats
}
