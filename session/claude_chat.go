package session

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/claudetmp"
)

// ClaudeChatArchiveDir is where configDir's chat archives go: a "chats"
// folder beside its Claude temp-dir archives when the global config.json
// sets claude_tmp_archive_dir (see ClaudeTmpArchiveDir), else
// claudetmp.ChatArchiveDir in the workspace's own config folder. Every
// snapshot, restore and sweep asks here.
func ClaudeChatArchiveDir(configDir string) string {
	root := configuredArchiveRoot()
	if root == "" {
		return claudetmp.ChatArchiveDir(configDir)
	}
	return filepath.Join(root, archiveSubdir(configDir), "chats")
}

// ClaudeProjectsRoots lists the dirs Claude writes chats into: mainDir's
// projects/, then each account dir's in name order, symlinks resolved,
// without duplicates and leaving out any that don't exist. An account's
// projects/ is usually a link into mainDir's (account.Sync), so it adds
// nothing; one that is a real dir holds chats nowhere else.
func ClaudeProjectsRoots(mainDir string, accountDirs map[string]string) []string {
	dirs := []string{mainDir}
	for _, name := range slices.Sorted(maps.Keys(accountDirs)) {
		dirs = append(dirs, accountDirs[name])
	}
	var out []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		p, err := filepath.EvalSymlinks(filepath.Join(d, "projects"))
		if err != nil || slices.Contains(out, p) {
			continue
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// claudeProjectsDir is the projects dir of the Claude config dir a session
// on accountName ("" for the default account) writes its chats into.
func claudeProjectsDir(accountName string) string {
	dir := bestEffortAccountDir(accountName)
	if dir == "" {
		dir = account.MainDir(account.Identity{})
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "projects")
}

// chatDir returns the Claude project dir holding a session's chats: the
// dir of transcriptPath when Claude reported one and the dir is there,
// else the dir under projectsDir that worktreePath encodes to
// (claudetmp.Locate, which fails closed).
func chatDir(transcriptPath, projectsDir, worktreePath string) (string, bool) {
	if transcriptPath != "" {
		d := filepath.Dir(transcriptPath)
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d, true
		}
	}
	if projectsDir == "" || worktreePath == "" {
		return "", false
	}
	return claudetmp.Locate(projectsDir, worktreePath)
}

// archiveClaudeChatFn is how Pause and Kill reach archiveClaudeChat, so a
// test can watch it.
var archiveClaudeChatFn = archiveClaudeChat

// archiveClaudeChat snapshots src, a session's Claude project dir, as
// <chat archive dir>/<name>.zip (claudetmp.Snapshot), where name is the
// scratchpad zip's for the same dir, and logs the outcome. It reports
// whether it wrote a zip: an unchanged chat writes none.
func archiveClaudeChat(configDir, src, worktreePath, reason string) (bool, error) {
	dir := ClaudeChatArchiveDir(configDir)
	name := claudetmp.ArchiveName(filepath.Base(src), claudetmp.WorktreePrefixes(configDir))
	zipPath := filepath.Join(dir, name+".zip")
	res, wrote, err := claudetmp.Snapshot(src, zipPath, claudetmp.Manifest{Worktree: worktreePath, Reason: reason})
	lg := log.For("claudechat")
	if err != nil {
		lg.Warn("claudechat.archive_failed", "source", src, "zip", zipPath, "reason", reason, "err", err.Error())
		return false, err
	}
	if wrote {
		lg.Info("claudechat.archived", "source", src, "zip", zipPath, "reason", reason,
			"bytes_in", res.BytesIn, "bytes_out", res.BytesOut, "carried_forward", res.Carried,
			"archive_dir_bytes", claudetmp.DirSize(dir))
	}
	return wrote, nil
}

// archiveChat snapshots the chats of this session, whose worktree was
// worktreePath, for Pause and Kill. Best effort: a failure is logged and
// the next sweep retries. A no-op when no project dir is found, so
// non-Claude programs and sessions that never sent a message need no
// special case.
func (i *Instance) archiveChat(worktreePath, reason string) {
	if i.ConfigDir == "" || worktreePath == "" {
		return
	}
	_, transcript := i.ClaudeSession()
	src, ok := chatDir(transcript, claudeProjectsDir(i.Account()), worktreePath)
	if !ok {
		return
	}
	_, _ = archiveClaudeChatFn(i.ConfigDir, src, worktreePath, reason)
}

// restoreChat is restoreClaudeChat for this session's recorded transcript.
func (i *Instance) restoreChat() error {
	_, transcript := i.ClaudeSession()
	return restoreClaudeChat(i.ConfigDir, transcript)
}

// restoreClaudeChat puts back a transcript Claude deleted while its
// session was paused (cleanupPeriodDays counts from the last write), from
// the session's chat archive, before the agent relaunches: BuildResumeCommand
// resumes the conversation only when the file is there. It looks in the
// archive dir in effect, then in the workspace's own folder (a chat
// archived before claude_tmp_archive_dir was set). A no-op when no
// transcript is recorded, the file exists or can't be checked, or no
// archive is found. It fills in only what is missing (claudetmp.Fill) and
// keeps the zip. A failure returns what the user must hear; the launch
// goes ahead and starts over with --continue.
func restoreClaudeChat(configDir, transcriptPath string) error {
	if configDir == "" || transcriptPath == "" {
		return nil
	}
	if _, err := os.Lstat(transcriptPath); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	dir := filepath.Dir(transcriptPath)
	name := claudetmp.ArchiveName(filepath.Base(dir), claudetmp.WorktreePrefixes(configDir))
	dirs := []string{ClaudeChatArchiveDir(configDir)}
	if own := claudetmp.ChatArchiveDir(configDir); own != dirs[0] {
		dirs = append(dirs, own)
	}
	var zipPath string
	for _, d := range dirs {
		p := filepath.Join(d, name+".zip")
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			zipPath = p
			break
		}
	}
	if zipPath == "" {
		return nil
	}
	lg := log.For("claudechat")
	n, err := claudetmp.Fill(zipPath, dir)
	if err != nil {
		lg.Warn("claudechat.restore_failed", "zip", zipPath, "transcript", transcriptPath, "err", err.Error())
		return fmt.Errorf("couldn't restore the conversation: %w; archive at %s", err, zipPath)
	}
	lg.Info("claudechat.restored", "zip", zipPath, "transcript", transcriptPath, "entries", n)
	return nil
}

// SweepClaudeChats snapshots (reason "sweep") the chats of configDir's
// sessions that are not running: dead ones (killed, orphaned, or gone
// before loom archived chats) and paused ones, so Claude's cleanup, which
// deletes a transcript a month after its last write, never takes the only
// copy. roots are Claude's projects dirs (ClaudeProjectsRoots). It returns
// how many zips it wrote, and first removes the ".partial" files older
// than sweepPartialAge that an interrupted snapshot left.
//
// A dir under a root is snapshotted when all of these hold:
//  1. Its name starts with configDir's encoded worktrees prefix and with no
//     other known config dir's (otherConfigDirs; SweepClaudeTemp's check 1).
//  2. Its last "-" segment is a plausible worktree timestamp and the name is
//     not truncated (SweepClaudeTemp's check 2).
//  3. No path in running encodes to it: a running session's chat changes
//     with every turn, and Pause or Kill snapshots it when it stops.
//  4. Something in it is newer than its archive (claudetmp.Snapshot).
//
// Nothing is deleted, so unlike SweepClaudeTemp it needs no on-disk recheck
// and no quiet period: snapshotting a dir a session is still writing costs
// a zip at worst, and the next snapshot replaces it.
func SweepClaudeChats(configDir string, roots []string, running map[string]bool, otherConfigDirs []string) int {
	if configDir == "" {
		return 0
	}
	lg := log.For("claudechat")
	adir := ClaudeChatArchiveDir(configDir)
	if n := claudetmp.PurgePartials(adir, sweepPartialAge); n > 0 {
		lg.Info("claudechat.partials_purged", "dir", adir, "removed", n)
	}
	own := claudetmp.WorktreePrefixes(configDir)
	foreign := foreignWorktreePrefixes(configDir, otherConfigDirs)
	var runningNames []claudetmp.Name
	for p := range running {
		runningNames = append(runningNames, claudetmp.Names(p)...)
	}
	wrote := 0
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			lg.Debug("claudechat.sweep_read_failed", "root", root, "err", err.Error())
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || !sweepableClaudeTemp(name, own, foreign) || matchesAnyName(runningNames, name) {
				continue
			}
			if ok, _ := archiveClaudeChat(configDir, filepath.Join(root, name), "", "sweep"); ok {
				wrote++
			}
		}
	}
	if wrote > 0 {
		lg.Info("claudechat.sweep_done", "config_dir", configDir, "archived", wrote)
	}
	return wrote
}
