// Package claudetmp finds, archives and restores the per-session temp
// directory Claude Code keeps outside the worktree:
// <root>/<encoded cwd>/<session id>/{scratchpad,tasks}/. Loom removes a
// session's worktree on pause and kill; this package keeps the temp dir
// (on tmpfs, often) from outliving it.
//
// The layout is Claude's own and undocumented, read from its 2.1.288
// bundle (docs/superpowers/specs/2026-10-05-worktree-locks-and-claude-temp-archive-design.md).
// TestRealClaude_TempDirLayout (package session) pins it. Every operation fails closed:
// what loom cannot match exactly, it leaves alone.
//
// No tmux, UI, app or config dependency.
package claudetmp

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// EnvTmpDir is Claude Code's own override for its temp root's parent.
const EnvTmpDir = "CLAUDE_CODE_TMPDIR"

// MaxDirName is the longest encoded cwd Claude uses as a name as is. A
// longer one becomes its first MaxDirName characters, "-", and a hash of
// the cwd that loom cannot compute.
const MaxDirName = 200

// Root returns Claude's temp root for this user, symlinks resolved, and
// whether it exists as a directory: the first of $CLAUDE_CODE_TMPDIR,
// $TMPDIR, $TMP and $TEMP that is set to an absolute path (else /tmp),
// joined with claude-<uid>. A relative value names a different directory
// for every process, so it is skipped. When the root does not exist, the
// path returned is unresolved.
func Root() (string, bool) {
	base := "/tmp"
	for _, v := range []string{EnvTmpDir, "TMPDIR", "TMP", "TEMP"} {
		if s := os.Getenv(v); filepath.IsAbs(s) {
			base = s
			break
		}
	}
	root := filepath.Join(base, fmt.Sprintf("claude-%d", os.Getuid()))
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root, false
	}
	fi, err := os.Stat(resolved)
	return resolved, err == nil && fi.IsDir()
}

// encode is Claude's encoding of a path as a directory name: every
// character outside [a-zA-Z0-9] becomes "-". Claude replaces UTF-16 code
// units, so a character outside the Basic Multilingual Plane becomes "--".
// The result is ASCII, so its byte length is its length to Claude.
func encode(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// DirName returns the directory name Claude gives a session whose cwd is
// physicalPath (symlinks already resolved: Claude uses its physical cwd).
// Past MaxDirName characters it returns the part loom can compute, the
// first MaxDirName characters and "-", with truncated set.
func DirName(physicalPath string) (name string, truncated bool) {
	n := encode(physicalPath)
	if len(n) <= MaxDirName {
		return n, false
	}
	return n[:MaxDirName] + "-", true
}

// Name is one directory name Claude may have used for a cwd. A Truncated
// name is only the part loom can compute: the real one continues with
// Claude's hash of the full path, so it identifies no directory.
type Name struct {
	Value     string
	Truncated bool
}

// Matches reports whether dirName is the directory n names. A Truncated
// name never matches: two sessions whose encoded cwds share their first
// MaxDirName characters differ only in the hash loom cannot compute, so a
// directory with that prefix may belong to another, running session.
func (n Name) Matches(dirName string) bool {
	return !n.Truncated && dirName == n.Value
}

// Names returns the names Claude may have used for a session whose cwd was
// path: the encoding of its physical path first (see physical), then of
// the path exactly as given, when that differs. Truncated ones are
// included so a caller can tell such a path apart, but they match nothing.
func Names(path string) []Name {
	var out []Name
	for _, p := range []string{physical(path), filepath.Clean(path)} {
		v, trunc := DirName(p)
		if n := (Name{Value: v, Truncated: trunc}); !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// physical resolves path's symlinks as far as it exists: the deepest
// existing ancestor is resolved and the missing components appended. A
// worktree is usually gone by the time loom looks for its temp dir, but
// its parents are not. (session.canonicalPath does the same; this package
// cannot import session.)
func physical(path string) string {
	p := filepath.Clean(path)
	rest := ""
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Clean(path)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// Locate returns Claude's temp dir under root for a session whose cwd was
// worktreePath. Exactly one directory may be named by one of
// Names(worktreePath); none or several report not found. A path whose
// encoding is truncated (see Name.Matches) is never found, so its dir is
// left alone. Symlinks never match.
func Locate(root, worktreePath string) (string, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	names := Names(worktreePath)
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, n := range names {
			if n.Matches(e.Name()) {
				found = append(found, e.Name())
				break
			}
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return filepath.Join(root, found[0]), true
}

// WorktreePrefixes returns the prefix every temp dir name of configDir's
// sessions starts with: its worktrees directory, encoded, plus the "-" a
// separator encodes to. Resolved first, then as given when that differs.
func WorktreePrefixes(configDir string) []string {
	var out []string
	for _, d := range []string{physical(configDir), filepath.Clean(configDir)} {
		if p := encode(filepath.Join(d, "worktrees")) + "-"; !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// ArchiveName is the archive file name, without ".zip", for the temp dir
// dirName: dirName less the first of prefixes it starts with, and less any
// leading "-", which unzip would read as a flag.
func ArchiveName(dirName string, prefixes []string) string {
	name := dirName
	for _, p := range prefixes {
		if len(dirName) > len(p) && strings.HasPrefix(dirName, p) {
			name = dirName[len(p):]
			break
		}
	}
	if trimmed := strings.TrimLeft(name, "-"); trimmed != "" {
		return trimmed
	}
	return "session"
}
