package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
)

// OrphanCandidate describes a worktree directory found on disk that no
// loaded Instance refers to — typically the consequence of state.json
// being overwritten without removing the underlying worktree, or a
// crash mid-cleanup. Each field is populated to the extent possible
// from the filesystem and a quick git probe; downstream code uses this
// to reconstruct an InstanceData entry without losing the user's work.
type OrphanCandidate struct {
	// WorktreePath is the absolute path of the orphaned worktree.
	WorktreePath string
	// BranchName is the sanitized branch this worktree was created on,
	// recovered by stripping the trailing `_<hex>` timestamp suffix
	// from the worktree directory name.
	BranchName string
	// RepoPath is the main repository's working tree (resolved via
	// `git -C <worktreePath> rev-parse --git-common-dir`). Discovery
	// drops candidates where this lookup fails — there's no way to
	// reconstruct a usable Instance without it.
	RepoPath string
	// BaseCommitSHA is the worktree's current HEAD, captured as the
	// recovered instance's diff-stats baseline. Without it, the diff
	// pipeline emits "base commit SHA not set" warnings on every
	// metadata tick until the user pause+resumes the instance.
	BaseCommitSHA string
	// Title is the humanized branch leaf (e.g. branch
	// "aidanb/example-jupyter-notebook" → title "example jupyter
	// notebook"). Used as the recovered instance's display title.
	Title string
	// HasLiveTmux reports whether a tmux session named
	// loom_<sanitized-title> is currently running. When true, recovery
	// can adopt the live PTY rather than spawning a new one.
	HasLiveTmux bool
}

// orphanProbeTimeout caps each git/tmux subprocess invocation during
// discovery. Discovery runs once at startup and shouldn't add
// noticeable latency even with many worktrees.
const orphanProbeTimeout = 3 * time.Second

// probeWorktreeRepo resolves a worktree's main-repo working tree and
// HEAD commit sha. Defined as a package-level variable so tests can
// stub it out without setting up real `git init`/`git worktree`
// fixtures — every other piece of orphan discovery (path parsing,
// claimed-paths filtering, tmux liveness via the injected Executor)
// is exercisable without a git binary on disk.
var probeWorktreeRepo = func(worktreePath string) (repoPath, headSHA string, err error) {
	repo, err := findMainRepoForWorktree(worktreePath)
	if err != nil {
		return "", "", err
	}
	head, err := readWorktreeHEAD(worktreePath)
	if err != nil {
		return "", "", err
	}
	return repo, head, nil
}

// DiscoverOrphans scans configDir's worktrees directory and returns
// every entry that is not referenced by claimedPaths. Each returned
// candidate is enriched with the branch name, repo path (best-effort),
// humanized title, and a tmux liveness probe.
//
// claimedPaths is the set of WorktreePaths already accounted for in
// state.json — typically built by the caller from
// instance.GetGitWorktree().GetWorktreePath() across the loaded list.
//
// Returns an empty slice (not nil) when configDir has no worktree dir
// or no orphans exist. Errors from individual worktree probes are
// logged but do not abort discovery; the function only returns an
// error when the worktrees directory itself is unreadable for a
// non-NotExist reason.
func DiscoverOrphans(configDir string, claimedPaths map[string]bool, cmdExec internalexec.Executor) ([]OrphanCandidate, error) {
	worktreesDir := filepath.Join(configDir, "worktrees")

	// Per-user grouping mirrors the layout produced by
	// session/git/worktree.go's resolveWorktreePaths: branch names
	// like "aidanb/feature" expand to <worktreesDir>/aidanb/feature_<hex>.
	userDirs, err := os.ReadDir(worktreesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []OrphanCandidate{}, nil
		}
		return nil, fmt.Errorf("read worktrees dir: %w", err)
	}

	out := make([]OrphanCandidate, 0)
	for _, userEntry := range userDirs {
		if !userEntry.IsDir() {
			continue
		}
		userDir := filepath.Join(worktreesDir, userEntry.Name())
		wtEntries, err := os.ReadDir(userDir)
		if err != nil {
			log.For("session").Warn("orphan_scan.read_user_dir_failed", "dir", userDir, "err", err)
			continue
		}
		for _, wt := range wtEntries {
			if !wt.IsDir() {
				continue
			}
			wtPath := filepath.Join(userDir, wt.Name())
			if claimedPaths[wtPath] {
				continue
			}
			cand, ok := buildOrphanCandidate(wtPath, userEntry.Name(), wt.Name(), cmdExec)
			if !ok {
				// Discovery dropped the candidate because we can't
				// reconstruct a recoverable instance from it (typically
				// a stale dir whose underlying repo was deleted).
				// Skipping is safe — CleanupOrphanedSessions will sweep
				// the tmux session if one exists.
				continue
			}
			out = append(out, cand)
		}
	}
	return out, nil
}

// buildOrphanCandidate reconstructs the metadata for one orphaned
// worktree. Returns ok=false when the worktree is unrecoverable — its
// repo can't be located or its HEAD can't be read — so the caller
// drops it instead of surfacing a candidate the user can't actually
// recover.
func buildOrphanCandidate(worktreePath, userPrefix, leafDirName string, cmdExec internalexec.Executor) (OrphanCandidate, bool) {
	branchLeaf, suffixOK := stripTimestampSuffix(leafDirName)
	if !suffixOK {
		// Directory doesn't match the `_<hex>` convention. Keep the
		// raw leaf — the git probes below still gate whether we
		// surface this candidate, so non-loom dirs (like a stray
		// `.git` clone) won't pass through.
		branchLeaf = leafDirName
	}
	branchName := branchLeaf
	if userPrefix != "" {
		branchName = filepath.Join(userPrefix, branchLeaf)
	}

	repoPath, headSHA, err := probeWorktreeRepo(worktreePath)
	if err != nil {
		log.For("session").Debug("orphan_scan.probe_failed", "worktree", worktreePath, "err", err)
		return OrphanCandidate{}, false
	}

	title := HumanizeBranchLeaf(branchName)
	return OrphanCandidate{
		WorktreePath:  worktreePath,
		BranchName:    branchName,
		RepoPath:      repoPath,
		BaseCommitSHA: headSHA,
		Title:         title,
		HasLiveTmux:   CheckTmuxAlive(title, cmdExec),
	}, true
}

// stripTimestampSuffix removes the trailing `_<hex>` segment that
// resolveWorktreePaths appends. Returns ok=false when the input has no
// underscore — those directories aren't loom-managed and the caller
// should fall back to using the raw name.
//
// Splits from the right so branch names containing underscores
// (e.g. "feature_x") still recover correctly.
func stripTimestampSuffix(name string) (string, bool) {
	idx := strings.LastIndex(name, "_")
	if idx <= 0 {
		return name, false
	}
	suffix := name[idx+1:]
	if !isHexString(suffix) {
		// The trailing token isn't hex — the underscore is part of
		// the branch name, not a timestamp delimiter.
		return name, false
	}
	return name[:idx], true
}

func isHexString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// HumanizeBranchLeaf turns a branch name into a plausible display
// title by taking the last `/`-segment and replacing dashes with
// spaces. e.g. "aidanb/example-jupyter-notebook" → "example jupyter
// notebook". This is the lossy fallback used when the original title
// is unrecoverable; users can rename via the existing rename flow if
// the auto-derived title is wrong.
func HumanizeBranchLeaf(branch string) string {
	parts := strings.Split(branch, "/")
	leaf := parts[len(parts)-1]
	return strings.ReplaceAll(leaf, "-", " ")
}

// findMainRepoForWorktree returns the working tree of the main
// repository that owns the given worktree path. Implementation parallels
// session/git's findMainRepoRoot but is duplicated here to avoid an
// import cycle for one helper.
func findMainRepoForWorktree(worktreePath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), orphanProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	commonDir := strings.TrimSpace(string(out))
	if commonDir == "" {
		return "", fmt.Errorf("git returned empty common-dir for %s", worktreePath)
	}
	// `--git-common-dir` returns the .git dir of the main repo;
	// stripping its parent yields the working tree.
	return filepath.Dir(commonDir), nil
}

// readWorktreeHEAD returns the worktree's current HEAD commit SHA via
// `git rev-parse HEAD`. Used as the recovered instance's
// BaseCommitSHA so its diff pipeline doesn't emit "base commit SHA
// not set" errors on every metadata tick. Empty branches (no commits
// yet) return an error from git, which buildOrphanCandidate treats as
// "corrupt — drop this candidate."
func readWorktreeHEAD(worktreePath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), orphanProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("git returned empty HEAD sha for %s", worktreePath)
	}
	return sha, nil
}

// SanitizedToTmuxName mirrors tmux.ToLoomTmuxName. Re-exporting the
// computation here keeps orphan discovery self-contained — callers
// don't have to reach into the tmux package directly.
func SanitizedToTmuxName(title string) string {
	return tmux.ToLoomTmuxName(title)
}
