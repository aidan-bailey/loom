package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/claudetmp"
)

// archiveClaudeTempFn is how Pause and Kill reach archiveClaudeTemp, so a
// test can observe the instance at the moment the archive runs.
var archiveClaudeTempFn = archiveClaudeTemp

// archiveClaudeTemp zips Claude's temp dir for the session whose worktree
// was worktreePath into configDir's archive and deletes it (see claudetmp).
// Best-effort: a failure is logged and the dir stays where it is, for a
// resume to find in place or a later sweep to retry. A no-op when Claude's
// root or the dir is absent, so non-Claude programs need no special case.
func archiveClaudeTemp(configDir, worktreePath, reason string) {
	if configDir == "" || worktreePath == "" {
		return
	}
	root, ok := claudetmp.Root()
	if !ok {
		return
	}
	src, ok := claudetmp.Locate(root, worktreePath)
	if !ok {
		return
	}
	_ = archiveClaudeTempDir(configDir, src, worktreePath, reason)
}

// archiveClaudeTempDir archives src, a temp dir already found, as
// <archive dir>/<name>.zip (claudetmp.ArchiveAs) and logs the outcome.
func archiveClaudeTempDir(configDir, src, worktreePath, reason string) error {
	dir := claudetmp.ArchiveDir(configDir)
	name := claudetmp.ArchiveName(filepath.Base(src), claudetmp.WorktreePrefixes(configDir))
	zipPath, res, err := claudetmp.ArchiveAs(dir, name, src, claudetmp.Manifest{Worktree: worktreePath, Reason: reason})
	lg := log.For("claudetmp")
	if err != nil {
		lg.Warn("claudetmp.archive_failed", "source", src, "zip", zipPath, "reason", reason, "err", err.Error())
		return err
	}
	lg.Info("claudetmp.archived", "source", src, "zip", zipPath, "reason", reason,
		"bytes_in", res.BytesIn, "bytes_out", res.BytesOut, "cache_bytes_skipped", res.CacheBytes,
		"archive_dir_bytes", claudetmp.DirSize(dir))
	return nil
}

// restoreClaudeTemp puts back the temp dir a pause archived for the
// session whose worktree is worktreePath, before the agent launches in it.
// A no-op when no zip is parked for it, or when the dir is already there
// (a pause whose archive failed left it in place). A failed restore
// returns what the user must hear; the zip is kept and the launch goes
// ahead without the scratchpad.
func restoreClaudeTemp(configDir, worktreePath string) error {
	if configDir == "" || worktreePath == "" {
		return nil
	}
	zipPath, ok := claudetmp.Parked(claudetmp.ArchiveDir(configDir), worktreePath, claudetmp.WorktreePrefixes(configDir))
	if !ok {
		return nil
	}
	root, exists := claudetmp.Root()
	if exists {
		if _, found := claudetmp.Locate(root, worktreePath); found {
			return nil
		}
	}
	lg := log.For("claudetmp")
	if err := claudetmp.Restore(zipPath, root); err != nil {
		lg.Warn("claudetmp.restore_failed", "zip", zipPath, "worktree", worktreePath, "err", err.Error())
		return fmt.Errorf("couldn't restore Claude's scratchpad: %w; archive kept at %s", err, zipPath)
	}
	lg.Info("claudetmp.restored", "zip", zipPath, "worktree", worktreePath)
	return nil
}

// sweepFailedDirs holds every temp dir (by full path) a sweep in this
// process failed to archive. Later sweeps skip them: a dir that can never be
// archived costs one warning per loom run, not one per workspace load. A
// restart retries.
var sweepFailedDirs sync.Map

// sweepTrashAge is how old a ".loom-trash-" tombstone under Claude's root
// must be before a sweep removes it (claudetmp.PurgeTrash).
const sweepTrashAge = time.Hour

// sweepPartialAge is how old a ".partial" file in the archive dir must be
// before a sweep removes it (claudetmp.PurgePartials).
const sweepPartialAge = 24 * time.Hour

// sweepQuietPeriod is how long a candidate dir must have been untouched
// before the sweep takes it (SweepClaudeTemp's check 6). A var so tests can
// shorten it; 0 turns the check off.
var sweepQuietPeriod = 24 * time.Hour

// SweepClaudeTemp archives (reason "sweep") the Claude temp dirs of
// configDir's sessions that no longer exist: orphans the auto-clean just
// removed, sessions an older loom killed, and the backlog from before loom
// archived anything. It returns how many it archived. It first removes the
// ".loom-trash-" tombstones older than sweepTrashAge and the ".partial" zips
// older than sweepPartialAge in configDir's archive dir that an interrupted
// archive left behind (claudetmp.PurgeTrash, claudetmp.PurgePartials).
//
// A dir under Claude's root is taken only when all of these hold:
//  1. Its name starts with configDir's encoded worktrees prefix (resolved
//     or as given) and with no other known config dir's: Claude's encoding
//     is lossy (foo_bar and foo-bar share a prefix), and a workspace can be
//     registered inside another's worktrees dir. otherConfigDirs lists
//     every known config dir; configDir itself may be among them.
//  2. Its last "-" segment is a plausible worktree timestamp suffix, and
//     the name is not truncated (past claudetmp.MaxDirName its tail is a
//     hash nothing here can compare).
//  3. No path in claimed encodes to it.
//  4. Checked again just before it is archived: no directory under
//     configDir's worktrees dirs (worktreeOnDisk) encodes to it.
//  5. This process did not already fail to archive it (sweepFailedDirs).
//  6. Neither it nor anything in its top three levels was modified within
//     sweepQuietPeriod (recentlyActive): defence in depth for a live
//     session checks 1 to 4 cannot see, such as an agent that outlived a
//     kill whose tmux close timed out. An orphan's dir is archived a day
//     after it goes quiet; Pause and Kill are not delayed.
//
// A live session's dir cannot be taken: its worktree exists before its
// Claude creates the dir, so it fails check 4 even when another loom
// started it after claimed was snapshotted. Paused sessions are claimed.
func SweepClaudeTemp(configDir string, claimed map[string]bool, otherConfigDirs []string) int {
	if configDir == "" {
		return 0
	}
	root, ok := claudetmp.Root()
	if !ok {
		return 0
	}
	// A tombstone only holds content already archived, and one a quit or
	// takeover interrupted mid-delete is otherwise never retried.
	if n := claudetmp.PurgeTrash(root, sweepTrashAge); n > 0 {
		log.For("claudetmp").Info("claudetmp.trash_purged", "root", root, "removed", n)
	}
	// An archive a crash or quit cut short leaves a partial zip that nothing
	// finishes; its source is only deleted after the zip is complete.
	adir := claudetmp.ArchiveDir(configDir)
	if n := claudetmp.PurgePartials(adir, sweepPartialAge); n > 0 {
		log.For("claudetmp").Info("claudetmp.partials_purged", "dir", adir, "removed", n)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		log.For("claudetmp").Debug("claudetmp.sweep_read_failed", "root", root, "err", err.Error())
		return 0
	}
	own := claudetmp.WorktreePrefixes(configDir)
	foreign := foreignWorktreePrefixes(configDir, otherConfigDirs)
	var claimedNames []claudetmp.Name
	for p := range claimed {
		claimedNames = append(claimedNames, claudetmp.Names(p)...)
	}
	archived := 0
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !sweepableClaudeTemp(name, own, foreign) || matchesAnyName(claimedNames, name) {
			continue
		}
		dir := filepath.Join(root, name)
		if _, failed := sweepFailedDirs.Load(dir); failed {
			log.For("claudetmp").Debug("claudetmp.sweep_skip_failed", "dir", dir)
			continue
		}
		if worktreeOnDisk(configDir, name) {
			continue
		}
		if recentlyActive(dir, sweepQuietPeriod) {
			log.For("claudetmp").Debug("claudetmp.sweep_skip_recent", "dir", dir)
			continue
		}
		if archiveClaudeTempDir(configDir, dir, "", "sweep") != nil {
			sweepFailedDirs.Store(dir, struct{}{})
			continue
		}
		archived++
	}
	if archived > 0 {
		log.For("claudetmp").Info("claudetmp.sweep_done", "config_dir", configDir, "archived", archived)
	}
	return archived
}

// foreignWorktreePrefixes returns the worktrees prefixes of every config dir
// in others except configDir itself and empty entries.
func foreignWorktreePrefixes(configDir string, others []string) []string {
	self := canonicalPath(configDir)
	var foreign []string
	for _, d := range others {
		// An empty entry names no config dir, and WorktreePrefixes would
		// turn it into a prefix of its own.
		if d != "" && canonicalPath(d) != self {
			foreign = append(foreign, claudetmp.WorktreePrefixes(d)...)
		}
	}
	return foreign
}

// sweepableClaudeTemp is SweepClaudeTemp's checks 1 and 2 on a dir name.
func sweepableClaudeTemp(name string, own, foreign []string) bool {
	if len(name) > claudetmp.MaxDirName || !hasAnyPrefix(name, own) || hasAnyPrefix(name, foreign) {
		return false
	}
	return looksLikeTimestampSuffix(name[strings.LastIndex(name, "-")+1:])
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func matchesAnyName(names []claudetmp.Name, dirName string) bool {
	for _, n := range names {
		if n.Matches(dirName) {
			return true
		}
	}
	return false
}

// nestedInAny reports whether dirName is the temp dir of a cwd inside a
// directory that one of names encodes: the encoding of a subdirectory
// continues its parent's with "-". A truncated name is already a prefix
// (Name.Matches), so only whole names need the extra "-".
func nestedInAny(names []claudetmp.Name, dirName string) bool {
	for _, n := range names {
		if !n.Truncated && strings.HasPrefix(dirName, n.Value+"-") {
			return true
		}
	}
	return false
}

// worktreeOnDisk is SweepClaudeTemp's check 4: whether some directory under
// one of configDir's worktrees dirs encodes to name, or is a worktree whose
// subdirectory does (nestedInAny). That is worktrees/ and any look-alike
// beside it (worktrees-backup/, worktrees.old/), whose sessions' names start
// with the same prefix. It walks the way DiscoverOrphans does — through
// prefix dirs, never into a worktree — and fails closed: a directory it
// cannot read, or a prefix dir below its depth limit, counts as a match.
func worktreeOnDisk(configDir, name string) bool {
	top, err := os.ReadDir(configDir)
	if err != nil {
		return !os.IsNotExist(err)
	}
	var walk func(dir string, depth int) bool
	walk = func(dir string, depth int) bool {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return true
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(dir, e.Name())
			names := claudetmp.Names(p)
			if matchesAnyName(names, name) {
				return true
			}
			if _, isWorktree := stripTimestampSuffix(e.Name()); isWorktree || isGitWorktreeRoot(p) {
				// A Claude session started in a subdirectory of a live
				// worktree has a temp dir named after that subdirectory,
				// which continues the root's own name with "-".
				if nestedInAny(names, name) {
					return true
				}
				continue
			}
			// Past the depth limit the walk cannot tell whether a worktree
			// is nested in p (a branch prefix with many slashes does that),
			// so it fails closed.
			if depth >= maxOrphanScanDepth || walk(p, depth+1) {
				return true
			}
		}
		return false
	}
	for _, e := range top {
		if e.IsDir() && strings.HasPrefix(e.Name(), "worktrees") && walk(filepath.Join(configDir, e.Name()), 0) {
			return true
		}
	}
	return false
}

// quietDepth is how far below a temp dir recentlyActive looks: the dir (0),
// the session id dirs (1), their scratchpad and tasks dirs (2), and the
// entries directly in those (3).
const quietDepth = 3

// recentlyActive reports whether dir, or an entry within its top quietDepth
// levels, was modified less than period ago. A period of 0 turns the check
// off. It uses Lstat (a symlink is its own mtime, never followed) and fails
// closed: an entry it cannot stat or list, and an mtime in the future, count
// as recent.
func recentlyActive(dir string, period time.Duration) bool {
	if period <= 0 {
		return false
	}
	cutoff := time.Now().Add(-period)
	var visit func(p string, depth int) bool
	visit = func(p string, depth int) bool {
		fi, err := os.Lstat(p)
		if err != nil || fi.ModTime().After(cutoff) {
			return true
		}
		if !fi.IsDir() || depth == quietDepth {
			return false
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return true
		}
		for _, e := range entries {
			if visit(filepath.Join(p, e.Name()), depth+1) {
				return true
			}
		}
		return false
	}
	return visit(dir, 0)
}
